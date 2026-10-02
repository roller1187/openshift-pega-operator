package controller

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"

	pegav1alpha1 "github.com/redhat-et/pega-operator/api/v1alpha1"
)

const (
	dbSecretName     = "pega-postgresql"
	dbPVCName        = "pega-postgresql"
	dbDeploymentName = "pega-postgresql"
	dbServiceName    = "pega-postgresql"
	dbPort           = 5432
)

func (r *PegaPlatformReconciler) reconcileDatabase(ctx context.Context, pega *pegav1alpha1.PegaPlatform) (bool, error) {
	logger := log.FromContext(ctx)
	db := &pega.Spec.Database

	if !db.Managed {
		if db.URL == "" {
			return false, fmt.Errorf("database.url is required when database.managed is false")
		}
		logger.V(1).Info("using external database", "url", db.URL)
		return true, nil
	}

	logger.V(1).Info("reconciling managed PostgreSQL")

	if err := r.reconcileDBSecret(ctx, pega); err != nil {
		return false, fmt.Errorf("reconciling database secret: %w", err)
	}

	if err := r.reconcileDBPVC(ctx, pega); err != nil {
		return false, fmt.Errorf("reconciling database PVC: %w", err)
	}

	if err := r.reconcileDBDeployment(ctx, pega); err != nil {
		return false, fmt.Errorf("reconciling database deployment: %w", err)
	}

	if err := r.reconcileDBService(ctx, pega); err != nil {
		return false, fmt.Errorf("reconciling database service: %w", err)
	}

	return r.isDBReady(ctx, pega)
}

func (r *PegaPlatformReconciler) reconcileDBSecret(ctx context.Context, pega *pegav1alpha1.PegaPlatform) error {
	db := &pega.Spec.Database

	if db.CredentialsSecret != "" {
		existing := &corev1.Secret{}
		return r.Get(ctx, types.NamespacedName{Name: db.CredentialsSecret, Namespace: pega.Namespace}, existing)
	}

	username := db.Username
	if username == "" {
		username = "postgres"
	}
	password := db.Password
	if password == "" {
		password = "postgres"
	}
	adminPassword := password

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      dbSecretName,
			Namespace: pega.Namespace,
			Labels:    labelsForPega(pega.Name, "database"),
		},
		Type: corev1.SecretTypeOpaque,
		StringData: map[string]string{
			"database-name":     "postgres",
			"database-user":     username,
			"database-password": password,
			"admin-password":    adminPassword,
		},
	}

	if err := ctrl.SetControllerReference(pega, secret, r.Scheme); err != nil {
		return err
	}

	existing := &corev1.Secret{}
	err := r.Get(ctx, types.NamespacedName{Name: dbSecretName, Namespace: pega.Namespace}, existing)
	if errors.IsNotFound(err) {
		return r.Create(ctx, secret)
	}
	if err != nil {
		return err
	}
	if _, ok := existing.Data["admin-password"]; !ok {
		if existing.StringData == nil {
			existing.StringData = map[string]string{}
		}
		existing.StringData["admin-password"] = adminPassword
		return r.Update(ctx, existing)
	}
	return nil
}

func (r *PegaPlatformReconciler) reconcileDBPVC(ctx context.Context, pega *pegav1alpha1.PegaPlatform) error {
	db := &pega.Spec.Database

	storageSize := db.StorageSize
	if storageSize == "" {
		storageSize = "100Gi"
	}

	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      dbPVCName,
			Namespace: pega.Namespace,
			Labels:    labelsForPega(pega.Name, "database"),
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: resource.MustParse(storageSize),
				},
			},
		},
	}

	if db.StorageClass != "" {
		pvc.Spec.StorageClassName = &db.StorageClass
	}

	if err := ctrl.SetControllerReference(pega, pvc, r.Scheme); err != nil {
		return err
	}

	existing := &corev1.PersistentVolumeClaim{}
	err := r.Get(ctx, types.NamespacedName{Name: dbPVCName, Namespace: pega.Namespace}, existing)
	if errors.IsNotFound(err) {
		return r.Create(ctx, pvc)
	}
	return err
}

func (r *PegaPlatformReconciler) reconcileDBDeployment(ctx context.Context, pega *pegav1alpha1.PegaPlatform) error {
	db := &pega.Spec.Database

	image := db.Image
	if image == "" {
		image = "registry.redhat.io/rhel8/postgresql-12:latest"
	}

	secretName := dbSecretName
	if db.CredentialsSecret != "" {
		secretName = db.CredentialsSecret
	}

	labels := labelsForPega(pega.Name, "database")
	replicas := int32(1)

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      dbDeploymentName,
			Namespace: pega.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "postgresql",
							Image: image,
							Ports: []corev1.ContainerPort{
								{ContainerPort: dbPort, Protocol: corev1.ProtocolTCP},
							},
							Env: []corev1.EnvVar{
								{
									Name: "POSTGRESQL_DATABASE",
									ValueFrom: &corev1.EnvVarSource{
										SecretKeyRef: &corev1.SecretKeySelector{
											LocalObjectReference: corev1.LocalObjectReference{Name: secretName},
											Key:                  "database-name",
										},
									},
								},
								{
									Name: "POSTGRESQL_USER",
									ValueFrom: &corev1.EnvVarSource{
										SecretKeyRef: &corev1.SecretKeySelector{
											LocalObjectReference: corev1.LocalObjectReference{Name: secretName},
											Key:                  "database-user",
										},
									},
								},
								{
									Name: "POSTGRESQL_PASSWORD",
									ValueFrom: &corev1.EnvVarSource{
										SecretKeyRef: &corev1.SecretKeySelector{
											LocalObjectReference: corev1.LocalObjectReference{Name: secretName},
											Key:                  "database-password",
										},
									},
								},
								{
									Name: "POSTGRESQL_ADMIN_PASSWORD",
									ValueFrom: &corev1.EnvVarSource{
										SecretKeyRef: &corev1.SecretKeySelector{
											LocalObjectReference: corev1.LocalObjectReference{Name: secretName},
											Key:                  "admin-password",
										},
									},
								},
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "postgresql-data",
									MountPath: "/var/lib/pgsql/data",
								},
							},
							ReadinessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									TCPSocket: &corev1.TCPSocketAction{
										Port: intstr.FromInt32(dbPort),
									},
								},
								InitialDelaySeconds: 10,
								PeriodSeconds:       10,
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "postgresql-data",
							VolumeSource: corev1.VolumeSource{
								PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
									ClaimName: dbPVCName,
								},
							},
						},
					},
				},
			},
		},
	}

	if err := ctrl.SetControllerReference(pega, dep, r.Scheme); err != nil {
		return err
	}

	return r.createOrUpdate(ctx, dep)
}

func (r *PegaPlatformReconciler) reconcileDBService(ctx context.Context, pega *pegav1alpha1.PegaPlatform) error {
	labels := labelsForPega(pega.Name, "database")

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      dbServiceName,
			Namespace: pega.Namespace,
			Labels:    labels,
		},
		Spec: corev1.ServiceSpec{
			Selector: labels,
			Ports: []corev1.ServicePort{
				{
					Name:       "postgresql",
					Port:       dbPort,
					TargetPort: intstr.FromInt32(dbPort),
					Protocol:   corev1.ProtocolTCP,
				},
			},
			Type: corev1.ServiceTypeClusterIP,
		},
	}

	if err := ctrl.SetControllerReference(pega, svc, r.Scheme); err != nil {
		return err
	}

	existing := &corev1.Service{}
	err := r.Get(ctx, types.NamespacedName{Name: dbServiceName, Namespace: pega.Namespace}, existing)
	if errors.IsNotFound(err) {
		return r.Create(ctx, svc)
	}
	return err
}

func (r *PegaPlatformReconciler) isDBReady(ctx context.Context, pega *pegav1alpha1.PegaPlatform) (bool, error) {
	dep := &appsv1.Deployment{}
	if err := r.Get(ctx, types.NamespacedName{Name: dbDeploymentName, Namespace: pega.Namespace}, dep); err != nil {
		return false, err
	}

	for _, cond := range dep.Status.Conditions {
		if cond.Type == appsv1.DeploymentAvailable && cond.Status == corev1.ConditionTrue {
			return true, nil
		}
	}

	log.FromContext(ctx).V(1).Info("waiting for PostgreSQL deployment to become available")
	return false, nil
}
