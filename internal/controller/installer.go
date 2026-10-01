package controller

import (
	"context"
	"fmt"

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

func (r *PegaPlatformReconciler) reconcileInstaller(ctx context.Context, pega *pegav1alpha1.PegaPlatform) (bool, error) {
	logger := log.FromContext(ctx).WithName("installer")

	jobName := pega.Name + "-installer"
	existing := &batchv1.Job{}
	err := r.Get(ctx, types.NamespacedName{Name: jobName, Namespace: pega.Namespace}, existing)

	if err == nil {
		// Job exists — check its status
		if existing.Status.Succeeded >= 1 {
			logger.Info("installer job completed successfully")
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
		logger.Info("installer job in progress")
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

	env := []corev1.EnvVar{
		{Name: "ACTION", Value: "install"},
		{Name: "JDBC_URL", Value: jdbcURL},
		{Name: "JDBC_CLASS", Value: db.DriverClass},
		{Name: "DB_TYPE", Value: db.Type},
		{Name: "JDBC_DRIVER_URI", Value: db.DriverURI},
		{Name: "RULES_SCHEMA", Value: db.RulesSchema},
		{Name: "DATA_SCHEMA", Value: db.DataSchema},
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
					RestartPolicy: corev1.RestartPolicyNever,
					Containers: []corev1.Container{
						{
							Name:  "pega-installer",
							Image: inst.Image,
							Env:   env,
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
