package controller

import (
	"context"
	"fmt"
	"strconv"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	networkingv1 "k8s.io/api/networking/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	pegav1alpha1 "github.com/redhat-et/pega-operator/api/v1alpha1"
)

const (
	srsDeploymentName = "pega-srs"
	srsPort           = int32(8080)
)

func (r *PegaPlatformReconciler) reconcileSRS(ctx context.Context, pega *pegav1alpha1.PegaPlatform) (bool, error) {
	if !pega.Spec.SRS.Enabled {
		meta.SetStatusCondition(&pega.Status.Conditions, metav1.Condition{
			Type:    pegav1alpha1.ConditionSRSReady,
			Status:  metav1.ConditionTrue,
			Reason:  "Disabled",
			Message: "SRS is disabled",
		})
		return true, nil
	}

	labels := labelsForPega(pega.Name, "srs")
	replicas := pega.Spec.SRS.Replicas
	if replicas == 0 {
		replicas = 2
	}

	// Deployment
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      srsDeploymentName,
			Namespace: pega.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "srs",
							Image: pega.Spec.SRS.Image,
							Ports: []corev1.ContainerPort{
								{ContainerPort: srsPort, Protocol: corev1.ProtocolTCP},
							},
							Env: []corev1.EnvVar{
								{Name: "ELASTICSEARCH_HOST", Value: r.searchHost(pega)},
								{Name: "ELASTICSEARCH_PORT", Value: r.searchPort(pega)},
								{Name: "ELASTICSEARCH_PROTO", Value: r.searchProto(pega)},
								{Name: "AUTH_ENABLED", Value: strconv.FormatBool(pega.Spec.SRS.AuthEnabled)},
								{Name: "MICRONAUT_SERVER_HOST", Value: "0.0.0.0"},
							},
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceMemory: resource.MustParse("1Gi"),
									corev1.ResourceCPU:    resource.MustParse("500m"),
								},
								Limits: corev1.ResourceList{
									corev1.ResourceMemory: resource.MustParse("2Gi"),
									corev1.ResourceCPU:    resource.MustParse("1000m"),
								},
							},
							ReadinessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									TCPSocket: &corev1.TCPSocketAction{
										Port: intstr.FromInt32(srsPort),
									},
								},
								InitialDelaySeconds: 15,
								PeriodSeconds:       10,
							},
							LivenessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									TCPSocket: &corev1.TCPSocketAction{
										Port: intstr.FromInt32(srsPort),
									},
								},
								InitialDelaySeconds: 60,
								PeriodSeconds:       15,
							},
						},
					},
				},
			},
		},
	}
	if err := ctrl.SetControllerReference(pega, dep, r.Scheme); err != nil {
		return false, err
	}
	if err := r.createOrUpdate(ctx, dep); err != nil {
		return false, fmt.Errorf("srs deployment: %w", err)
	}

	// Service
	svcName := pega.Spec.SRS.DeploymentName
	if svcName == "" {
		svcName = "pega-search"
	}
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      svcName,
			Namespace: pega.Namespace,
			Labels:    labels,
		},
		Spec: corev1.ServiceSpec{
			Selector: labels,
			Type:     corev1.ServiceTypeClusterIP,
			Ports: []corev1.ServicePort{
				{Name: "http", Port: srsPort, TargetPort: intstr.FromInt32(srsPort), Protocol: corev1.ProtocolTCP},
			},
		},
	}
	if err := ctrl.SetControllerReference(pega, svc, r.Scheme); err != nil {
		return false, err
	}
	if err := r.createOrUpdate(ctx, svc); err != nil {
		return false, fmt.Errorf("srs service: %w", err)
	}

	// NetworkPolicy
	protocolTCP := corev1.ProtocolTCP
	np := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pega-srs",
			Namespace: pega.Namespace,
			Labels:    labels,
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{MatchLabels: labels},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
			Ingress: []networkingv1.NetworkPolicyIngressRule{
				{
					From: []networkingv1.NetworkPolicyPeer{
						{
							NamespaceSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{
									"kubernetes.io/metadata.name": pega.Namespace,
								},
							},
						},
					},
					Ports: []networkingv1.NetworkPolicyPort{
						{Port: &intstr.IntOrString{Type: intstr.Int, IntVal: srsPort}, Protocol: &protocolTCP},
					},
				},
			},
		},
	}
	if err := ctrl.SetControllerReference(pega, np, r.Scheme); err != nil {
		return false, err
	}
	if err := r.createOrUpdate(ctx, np); err != nil {
		return false, fmt.Errorf("srs network policy: %w", err)
	}

	// Check readiness
	found := &appsv1.Deployment{}
	if err := r.Get(ctx, types.NamespacedName{Name: srsDeploymentName, Namespace: pega.Namespace}, found); err != nil {
		return false, err
	}

	for _, cond := range found.Status.Conditions {
		if cond.Type == appsv1.DeploymentAvailable && cond.Status == corev1.ConditionTrue {
			meta.SetStatusCondition(&pega.Status.Conditions, metav1.Condition{
				Type:    pegav1alpha1.ConditionSRSReady,
				Status:  metav1.ConditionTrue,
				Reason:  "Ready",
				Message: "SRS deployment is available",
			})
			return true, nil
		}
	}

	meta.SetStatusCondition(&pega.Status.Conditions, metav1.Condition{
		Type:    pegav1alpha1.ConditionSRSReady,
		Status:  metav1.ConditionFalse,
		Reason:  "NotReady",
		Message: "Waiting for SRS deployment to become available",
	})
	return false, nil
}

func (r *PegaPlatformReconciler) searchHost(pega *pegav1alpha1.PegaPlatform) string {
	if pega.Spec.Search.Managed {
		return "pega-opensearch-client"
	}
	return pega.Spec.Search.URL
}

func (r *PegaPlatformReconciler) searchPort(pega *pegav1alpha1.PegaPlatform) string {
	port := pega.Spec.Search.Port
	if port == 0 {
		port = 9200
	}
	return strconv.Itoa(int(port))
}

func (r *PegaPlatformReconciler) searchProto(pega *pegav1alpha1.PegaPlatform) string {
	if pega.Spec.Search.Protocol != "" {
		return pega.Spec.Search.Protocol
	}
	return "http"
}

