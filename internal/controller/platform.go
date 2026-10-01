package controller

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"

	pegav1alpha1 "github.com/redhat-et/pega-operator/api/v1alpha1"
)

func (r *PegaPlatformReconciler) reconcilePlatform(ctx context.Context, pega *pegav1alpha1.PegaPlatform) (bool, error) {
	logger := log.FromContext(ctx).WithName("platform")

	allReady := true

	for _, tier := range pega.Spec.Tiers {
		depName := pega.Name + "-" + tier.Name
		labels := labelsForPega(pega.Name, tier.Name)

		if err := r.reconcileTierDeployment(ctx, pega, tier, depName, labels); err != nil {
			return false, fmt.Errorf("tier %s deployment: %w", tier.Name, err)
		}

		if err := r.reconcileTierService(ctx, pega, tier, depName, labels); err != nil {
			return false, fmt.Errorf("tier %s service: %w", tier.Name, err)
		}

		if tier.Ingress != nil && tier.Ingress.Enabled {
			if err := r.reconcileTierRoute(ctx, pega, tier, depName, labels); err != nil {
				return false, fmt.Errorf("tier %s route: %w", tier.Name, err)
			}
		}

		if tier.HPA != nil && tier.HPA.Enabled {
			if err := r.reconcileTierHPA(ctx, pega, tier, depName, labels); err != nil {
				return false, fmt.Errorf("tier %s hpa: %w", tier.Name, err)
			}
		}

		ready, err := r.isTierReady(ctx, pega, depName)
		if err != nil {
			return false, err
		}
		if !ready {
			logger.Info("tier not yet ready", "tier", tier.Name)
			allReady = false
		}
	}

	if allReady {
		r.setWebURL(pega)
		meta.SetStatusCondition(&pega.Status.Conditions, metav1.Condition{
			Type:    pegav1alpha1.ConditionPlatformReady,
			Status:  metav1.ConditionTrue,
			Reason:  "Ready",
			Message: "All platform tiers are available",
		})
	} else {
		meta.SetStatusCondition(&pega.Status.Conditions, metav1.Condition{
			Type:    pegav1alpha1.ConditionPlatformReady,
			Status:  metav1.ConditionFalse,
			Reason:  "NotReady",
			Message: "Waiting for platform tiers to become available",
		})
	}

	return allReady, nil
}

func (r *PegaPlatformReconciler) reconcileTierDeployment(ctx context.Context, pega *pegav1alpha1.PegaPlatform, tier pegav1alpha1.TierSpec, depName string, labels map[string]string) error {
	replicas := tier.Replicas
	if replicas == 0 {
		replicas = 1
	}

	env := r.buildTierEnv(pega, tier)
	resources := tier.Resources
	if len(resources.Requests) == 0 {
		resources = corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("12Gi"),
				corev1.ResourceCPU:    resource.MustParse("3"),
			},
			Limits: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("12Gi"),
				corev1.ResourceCPU:    resource.MustParse("4"),
			},
		}
	}

	maxSurge := intstr.FromInt32(1)
	maxUnavailable := intstr.FromInt32(0)

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      depName,
			Namespace: pega.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Strategy: appsv1.DeploymentStrategy{
				Type: appsv1.RollingUpdateDeploymentStrategyType,
				RollingUpdate: &appsv1.RollingUpdateDeployment{
					MaxSurge:       &maxSurge,
					MaxUnavailable: &maxUnavailable,
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "pega",
							Image: pega.Spec.PegaImage,
							Ports: []corev1.ContainerPort{
								{Name: "app", ContainerPort: 8080, Protocol: corev1.ProtocolTCP},
								{Name: "management", ContainerPort: 8081, Protocol: corev1.ProtocolTCP},
							},
							Env:       env,
							Resources: resources,
							LivenessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path: "/health",
										Port: intstr.FromInt32(8081),
									},
								},
								InitialDelaySeconds: 120,
								PeriodSeconds:       20,
								TimeoutSeconds:      10,
							},
							ReadinessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path: "/health",
										Port: intstr.FromInt32(8081),
									},
								},
								InitialDelaySeconds: 60,
								PeriodSeconds:       10,
								TimeoutSeconds:      5,
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

func (r *PegaPlatformReconciler) reconcileTierService(ctx context.Context, pega *pegav1alpha1.PegaPlatform, tier pegav1alpha1.TierSpec, depName string, labels map[string]string) error {
	port := int32(80)
	targetPort := int32(8080)
	if tier.Service != nil {
		if tier.Service.Port != 0 {
			port = tier.Service.Port
		}
		if tier.Service.TargetPort != 0 {
			targetPort = tier.Service.TargetPort
		}
	}

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      depName,
			Namespace: pega.Namespace,
			Labels:    labels,
		},
		Spec: corev1.ServiceSpec{
			Selector: labels,
			Type:     corev1.ServiceTypeClusterIP,
			Ports: []corev1.ServicePort{
				{Name: "http", Port: port, TargetPort: intstr.FromInt32(targetPort), Protocol: corev1.ProtocolTCP},
			},
		},
	}

	if err := ctrl.SetControllerReference(pega, svc, r.Scheme); err != nil {
		return err
	}
	return r.createOrUpdate(ctx, svc)
}

func (r *PegaPlatformReconciler) reconcileTierRoute(ctx context.Context, pega *pegav1alpha1.PegaPlatform, tier pegav1alpha1.TierSpec, depName string, labels map[string]string) error {
	route := &unstructured.Unstructured{}
	route.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "route.openshift.io",
		Version: "v1",
		Kind:    "Route",
	})
	route.SetName(depName)
	route.SetNamespace(pega.Namespace)
	route.SetLabels(labels)

	spec := map[string]interface{}{
		"host": tier.Ingress.Domain,
		"to": map[string]interface{}{
			"kind":   "Service",
			"name":   depName,
			"weight": int64(100),
		},
		"port": map[string]interface{}{
			"targetPort": int64(8080),
		},
	}

	if tier.Ingress.TLS != nil && tier.Ingress.TLS.Enabled {
		termination := tier.Ingress.TLS.Termination
		if termination == "" {
			termination = "edge"
		}
		spec["tls"] = map[string]interface{}{
			"termination":                   termination,
			"insecureEdgeTerminationPolicy": "Redirect",
		}
	}

	route.Object["spec"] = spec

	if err := ctrl.SetControllerReference(pega, route, r.Scheme); err != nil {
		return err
	}

	existing := &unstructured.Unstructured{}
	existing.SetGroupVersionKind(route.GroupVersionKind())
	err := r.Get(ctx, types.NamespacedName{Name: depName, Namespace: pega.Namespace}, existing)
	if err != nil {
		if apierrors.IsNotFound(err) || meta.IsNoMatchError(err) {
			return r.Create(ctx, route)
		}
		return err
	}

	route.SetResourceVersion(existing.GetResourceVersion())
	return r.Update(ctx, route)
}

func (r *PegaPlatformReconciler) reconcileTierHPA(ctx context.Context, pega *pegav1alpha1.PegaPlatform, tier pegav1alpha1.TierSpec, depName string, labels map[string]string) error {
	hpa := tier.HPA
	minReplicas := hpa.MinReplicas
	if minReplicas == 0 {
		minReplicas = 1
	}
	maxReplicas := hpa.MaxReplicas
	if maxReplicas == 0 {
		maxReplicas = 5
	}
	targetCPU := hpa.TargetCPUUtilization
	if targetCPU == 0 {
		targetCPU = 70
	}

	hpaObj := &autoscalingv2.HorizontalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{
			Name:      depName,
			Namespace: pega.Namespace,
			Labels:    labels,
		},
		Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
			ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{
				APIVersion: "apps/v1",
				Kind:       "Deployment",
				Name:       depName,
			},
			MinReplicas: &minReplicas,
			MaxReplicas: maxReplicas,
			Metrics: []autoscalingv2.MetricSpec{
				{
					Type: autoscalingv2.ResourceMetricSourceType,
					Resource: &autoscalingv2.ResourceMetricSource{
						Name: corev1.ResourceCPU,
						Target: autoscalingv2.MetricTarget{
							Type:               autoscalingv2.UtilizationMetricType,
							AverageUtilization: &targetCPU,
						},
					},
				},
			},
		},
	}

	if err := ctrl.SetControllerReference(pega, hpaObj, r.Scheme); err != nil {
		return err
	}
	return r.createOrUpdate(ctx, hpaObj)
}

func (r *PegaPlatformReconciler) buildTierEnv(pega *pegav1alpha1.PegaPlatform, tier pegav1alpha1.TierSpec) []corev1.EnvVar {
	db := &pega.Spec.Database
	jdbcURL := r.buildJDBCURL(pega)

	env := []corev1.EnvVar{
		{Name: "NODE_TYPE", Value: tier.NodeType},
		{Name: "JAVA_OPTS", Value: tier.JavaOpts},
		{Name: "JDBC_URL", Value: jdbcURL},
		{Name: "JDBC_CLASS", Value: db.DriverClass},
		{Name: "DB_TYPE", Value: db.Type},
		{Name: "JDBC_DRIVER_URI", Value: db.DriverURI},
		{Name: "RULES_SCHEMA", Value: db.RulesSchema},
		{Name: "DATA_SCHEMA", Value: db.DataSchema},
	}

	// Database credentials
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

	// Search
	if pega.Spec.SRS.Enabled {
		srsName := pega.Spec.SRS.DeploymentName
		if srsName == "" {
			srsName = "pega-search"
		}
		env = append(env, corev1.EnvVar{Name: "PEGA_SEARCH_URL", Value: fmt.Sprintf("http://%s:%d", srsName, srsPort)})
	}

	// Streaming
	if pega.Spec.Stream.Enabled {
		var bootstrap string
		if pega.Spec.Stream.Managed {
			bootstrap = "pega-kafka-cluster-kafka-bootstrap:9092"
		} else {
			bootstrap = pega.Spec.Stream.BootstrapServer
		}
		env = append(env, corev1.EnvVar{Name: "PEGA_STREAM_BOOTSTRAP_SERVERS", Value: bootstrap})
	}

	return env
}

func (r *PegaPlatformReconciler) isTierReady(ctx context.Context, pega *pegav1alpha1.PegaPlatform, depName string) (bool, error) {
	dep := &appsv1.Deployment{}
	if err := r.Get(ctx, types.NamespacedName{Name: depName, Namespace: pega.Namespace}, dep); err != nil {
		return false, err
	}

	for _, cond := range dep.Status.Conditions {
		if cond.Type == appsv1.DeploymentAvailable && cond.Status == corev1.ConditionTrue {
			return true, nil
		}
	}
	return false, nil
}

func (r *PegaPlatformReconciler) setWebURL(pega *pegav1alpha1.PegaPlatform) {
	for _, tier := range pega.Spec.Tiers {
		if tier.Ingress != nil && tier.Ingress.Enabled && tier.Ingress.Domain != "" {
			scheme := "http"
			if tier.Ingress.TLS != nil && tier.Ingress.TLS.Enabled {
				scheme = "https"
			}
			pega.Status.WebURL = fmt.Sprintf("%s://%s", scheme, tier.Ingress.Domain)
			return
		}
	}
}
