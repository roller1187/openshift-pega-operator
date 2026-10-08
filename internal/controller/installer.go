package controller

import (
	"context"
	"fmt"
	"path"
	"strings"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"

	pegav1alpha1 "github.com/redhat-et/pega-operator/api/v1alpha1"
)

const (
	jdbcDriverVolumeName       = "jdbc-driver"
	jdbcDriverMountPath        = "/pega-jdbc"
	defaultDriverDownloadImage = "registry.access.redhat.com/ubi9/ubi-minimal:latest"
)

// Fetches every declared driver into DEST_DIR. `set -e` aborts the init
// container on the first failure so the installer never starts with a missing
// driver. Query strings are stripped from the filename the same way Pega's own
// common_functions.sh does.
const jdbcDriverDownloadScript = `set -eu
mkdir -p "$DEST_DIR"
for url in $(echo "$DRIVER_URIS" | tr ',' ' '); do
  fname=$(basename "$(echo "$url" | cut -d'?' -f1)")
  echo "Downloading ${url}"
  curl -fsSL --retry 3 --retry-delay 2 -o "${DEST_DIR}/${fname}" "${url}"
done
ls -l "$DEST_DIR"
`

// jdbcDriverDownload builds the init container, volume and mount that stage the
// JDBC driver on local disk. Both the installer Job and the platform tiers need
// this: each runs a Pega image that would otherwise fetch the driver itself over
// TLS, which fails on FIPS-enabled clusters. Returns nils when no driver URI is
// configured.
func jdbcDriverDownload(driverURI, downloadImage string) (*corev1.Container, *corev1.Volume, []corev1.VolumeMount) {
	if localJDBCDriverURIs(driverURI) == "" {
		return nil, nil, nil
	}
	if downloadImage == "" {
		downloadImage = defaultDriverDownloadImage
	}
	mounts := []corev1.VolumeMount{{Name: jdbcDriverVolumeName, MountPath: jdbcDriverMountPath}}
	return &corev1.Container{
			Name:    "download-jdbc-driver",
			Image:   downloadImage,
			Command: []string{"/bin/sh", "-c", jdbcDriverDownloadScript},
			Env: []corev1.EnvVar{
				{Name: "DRIVER_URIS", Value: driverURI},
				{Name: "DEST_DIR", Value: jdbcDriverMountPath},
			},
			VolumeMounts: mounts,
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceMemory: resource.MustParse("64Mi"),
					corev1.ResourceCPU:    resource.MustParse("50m"),
				},
				Limits: corev1.ResourceList{
					corev1.ResourceMemory: resource.MustParse("256Mi"),
					corev1.ResourceCPU:    resource.MustParse("500m"),
				},
			},
		}, &corev1.Volume{
			Name:         jdbcDriverVolumeName,
			VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
		}, mounts
}

// localJDBCDriverURIs maps the user-supplied driver URIs onto the local file://
// paths the init container writes them to. Returns "" for empty input, which
// makes Pega's installer skip driver handling entirely.
func localJDBCDriverURIs(driverURI string) string {
	var locals []string
	for _, raw := range strings.Split(driverURI, ",") {
		u := strings.TrimSpace(raw)
		if u == "" {
			continue
		}
		name := path.Base(strings.SplitN(u, "?", 2)[0])
		locals = append(locals, "file://"+jdbcDriverMountPath+"/"+name)
	}
	return strings.Join(locals, ",")
}

func (r *PegaPlatformReconciler) reconcileInstaller(ctx context.Context, pega *pegav1alpha1.PegaPlatform) (bool, error) {
	logger := log.FromContext(ctx).WithName("installer")

	jobName := pega.Name + "-installer"
	existing := &batchv1.Job{}
	err := r.Get(ctx, types.NamespacedName{Name: jobName, Namespace: pega.Namespace}, existing)

	if err == nil {
		// Job exists — check its status
		if existing.Status.Succeeded >= 1 {
			logger.V(1).Info("installer job completed successfully")
			pega.Status.InstallerJobName = jobName
			meta.SetStatusCondition(&pega.Status.Conditions, metav1.Condition{
				Type:    pegav1alpha1.ConditionInstallerDone,
				Status:  metav1.ConditionTrue,
				Reason:  "Complete",
				Message: "Pega schema installation completed",
			})
			return true, nil
		}

		if existing.Status.Failed >= 1 {
			logger.Info("installer job failed")
			meta.SetStatusCondition(&pega.Status.Conditions, metav1.Condition{
				Type:    pegav1alpha1.ConditionInstallerDone,
				Status:  metav1.ConditionFalse,
				Reason:  "Failed",
				Message: "Pega schema installation failed — check job logs",
			})
			return false, fmt.Errorf("installer job %s failed", jobName)
		}

		// Still running
		logger.V(1).Info("installer job in progress")
		meta.SetStatusCondition(&pega.Status.Conditions, metav1.Condition{
			Type:    pegav1alpha1.ConditionInstallerDone,
			Status:  metav1.ConditionFalse,
			Reason:  "InProgress",
			Message: "Pega schema installation is running",
		})
		return false, nil
	}

	if !errors.IsNotFound(err) {
		return false, err
	}

	// Create the installer job
	logger.Info("creating installer job", "name", jobName)

	jdbcURL := r.buildJDBCURL(pega)
	db := &pega.Spec.Database

	// The driver is fetched by an init container and handed to the installer as
	// a local file, so the installer never performs the remote download itself.
	// An empty DriverURI leaves JDBC_DRIVER_URI empty, which makes Pega's script
	// skip the download — the supported path for drivers baked into an image.
	localDriverURIs := localJDBCDriverURIs(db.DriverURI)

	env := []corev1.EnvVar{
		{Name: "ACTION", Value: "install"},
		{Name: "JDBC_URL", Value: jdbcURL},
		{Name: "JDBC_CLASS", Value: db.DriverClass},
		{Name: "DB_TYPE", Value: db.Type},
		{Name: "JDBC_DRIVER_URI", Value: localDriverURIs},
		{Name: "RULES_SCHEMA", Value: db.RulesSchema},
		{Name: "DATA_SCHEMA", Value: db.DataSchema},
	}

	if pega.Spec.FIPS1403Mode {
		env = append(env, corev1.EnvVar{Name: "FIPS_140_3_MODE", Value: "true"})
	}

	if db.CredentialsSecret != "" {
		env = append(env,
			corev1.EnvVar{
				Name: "DB_USERNAME",
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: db.CredentialsSecret},
						Key:                  "DB_USERNAME",
					},
				},
			},
			corev1.EnvVar{
				Name: "DB_PASSWORD",
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: db.CredentialsSecret},
						Key:                  "DB_PASSWORD",
					},
				},
			},
		)
	} else {
		username := db.Username
		if username == "" {
			username = "postgres"
		}
		password := db.Password
		if password == "" {
			password = "postgres"
		}
		env = append(env,
			corev1.EnvVar{Name: "DB_USERNAME", Value: username},
			corev1.EnvVar{Name: "DB_PASSWORD", Value: password},
		)
	}

	// Admin password
	inst := &pega.Spec.Installer
	if inst.AdminPasswordSecret != "" {
		env = append(env, corev1.EnvVar{
			Name: "ADMIN_PASSWORD",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: inst.AdminPasswordSecret},
					Key:                  "ADMIN_PASSWORD",
				},
			},
		})
	} else if inst.AdminPassword != "" {
		env = append(env, corev1.EnvVar{Name: "ADMIN_PASSWORD", Value: inst.AdminPassword})
	}

	labels := labelsForPega(pega.Name, "installer")
	backoffLimit := int32(0)

	var initContainers []corev1.Container
	var volumes []corev1.Volume

	dlContainer, dlVolume, driverMounts := jdbcDriverDownload(db.DriverURI, inst.DriverDownloadImage)
	if dlContainer != nil {
		initContainers = append(initContainers, *dlContainer)
		volumes = append(volumes, *dlVolume)
	}

	if pega.Spec.Database.Managed {
		dbUser := pega.Spec.Database.Username
		if dbUser == "" {
			dbUser = "postgres"
		}
		grantCmd := fmt.Sprintf(
			`PGPASSWORD="$PGPASSWORD" psql -h %s -U postgres -d postgres -c "GRANT ALL ON DATABASE postgres TO %s;"`,
			dbServiceName, dbUser,
		)
		initContainers = append(initContainers, corev1.Container{
			Name:    "grant-db-privileges",
			Image:   "registry.redhat.io/rhel8/postgresql-12:latest",
			Command: []string{"sh", "-c", grantCmd},
			Env: []corev1.EnvVar{
				{
					Name: "PGPASSWORD",
					ValueFrom: &corev1.EnvVarSource{
						SecretKeyRef: &corev1.SecretKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{Name: dbSecretName},
							Key:                  "admin-password",
						},
					},
				},
			},
		})
	}

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: pega.Namespace,
			Labels:    labels,
		},
		Spec: batchv1.JobSpec{
			BackoffLimit: &backoffLimit,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					RestartPolicy:  corev1.RestartPolicyNever,
					InitContainers: initContainers,
					Volumes:        volumes,
					Containers: []corev1.Container{
						{
							Name:         "pega-installer",
							Image:        inst.Image,
							Env:          env,
							VolumeMounts: driverMounts,
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceMemory: resource.MustParse("4Gi"),
									corev1.ResourceCPU:    resource.MustParse("2"),
								},
								Limits: corev1.ResourceList{
									corev1.ResourceMemory: resource.MustParse("8Gi"),
									corev1.ResourceCPU:    resource.MustParse("4"),
								},
							},
						},
					},
				},
			},
		},
	}

	if err := ctrl.SetControllerReference(pega, job, r.Scheme); err != nil {
		return false, err
	}

	if err := r.Create(ctx, job); err != nil {
		return false, fmt.Errorf("creating installer job: %w", err)
	}

	pega.Status.InstallerJobName = jobName
	return false, nil
}

func (r *PegaPlatformReconciler) buildJDBCURL(pega *pegav1alpha1.PegaPlatform) string {
	if pega.Spec.Database.Managed {
		return fmt.Sprintf("jdbc:postgresql://%s:%d/postgres", dbServiceName, dbPort)
	}
	return pega.Spec.Database.URL
}
