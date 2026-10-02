package controller

import (
	"context"
	"fmt"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	pegav1alpha1 "github.com/redhat-et/pega-operator/api/v1alpha1"
)

func (r *PegaPlatformReconciler) reconcileSearch(ctx context.Context, pega *pegav1alpha1.PegaPlatform) (bool, error) {
	logger := log.FromContext(ctx).WithName("search")

	if !pega.Spec.Search.Managed {
		meta.SetStatusCondition(&pega.Status.Conditions, metav1.Condition{
			Type:    pegav1alpha1.ConditionSearchReady,
			Status:  metav1.ConditionTrue,
			Reason:  "ExternalSearch",
			Message: "Using external OpenSearch endpoint",
		})
		return true, nil
	}

	labels := labelsForPega(pega.Name, "opensearch")
	replicas := pega.Spec.Search.Replicas
	if replicas == 0 {
		replicas = 3
	}

	image := pega.Spec.Search.Image
	if image == "" {
		image = "docker.io/opensearchproject/opensearch:2.19.1"
	}

	storageSize := pega.Spec.Search.StorageSize
	if storageSize == "" {
		storageSize = "30Gi"
	}

	javaOpts := pega.Spec.Search.JavaOpts
	if javaOpts == "" {
		javaOpts = "-Xms512m -Xmx512m"
	}

	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pega-opensearch",
			Namespace: pega.Namespace,
			Labels:    labels,
		},
	}
	if err := ctrl.SetControllerReference(pega, sa, r.Scheme); err != nil {
		return false, err
	}
	if err := r.createOrUpdate(ctx, sa); err != nil {
		return false, fmt.Errorf("service account: %w", err)
	}

	sccRB := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pega-opensearch-scc",
			Namespace: pega.Namespace,
			Labels:    labels,
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     "system:openshift:scc:nonroot-v2",
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      "pega-opensearch",
				Namespace: pega.Namespace,
			},
		},
	}
	if err := ctrl.SetControllerReference(pega, sccRB, r.Scheme); err != nil {
		return false, err
	}
	if err := r.createOrUpdate(ctx, sccRB); err != nil {
		return false, fmt.Errorf("opensearch scc rolebinding: %w", err)
	}

	// Headless service for StatefulSet DNS
	headlessSvc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pega-opensearch",
			Namespace: pega.Namespace,
			Labels:    labels,
		},
		Spec: corev1.ServiceSpec{
			ClusterIP:                "None",
			PublishNotReadyAddresses: true,
			Selector:                 labels,
			Ports: []corev1.ServicePort{
				{Name: "http", Port: 9200, TargetPort: intstr.FromInt32(9200), Protocol: corev1.ProtocolTCP},
				{Name: "transport", Port: 9300, TargetPort: intstr.FromInt32(9300), Protocol: corev1.ProtocolTCP},
			},
		},
	}
	if err := ctrl.SetControllerReference(pega, headlessSvc, r.Scheme); err != nil {
		return false, err
	}
	if err := r.createOrUpdate(ctx, headlessSvc); err != nil {
		return false, fmt.Errorf("headless service: %w", err)
	}

	// Client service
	clientSvc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pega-opensearch-client",
			Namespace: pega.Namespace,
			Labels:    labels,
		},
		Spec: corev1.ServiceSpec{
			Selector: labels,
			Ports: []corev1.ServicePort{
				{Name: "http", Port: 9200, TargetPort: intstr.FromInt32(9200), Protocol: corev1.ProtocolTCP},
			},
		},
	}
	if err := ctrl.SetControllerReference(pega, clientSvc, r.Scheme); err != nil {
		return false, err
	}
	if err := r.createOrUpdate(ctx, clientSvc); err != nil {
		return false, fmt.Errorf("client service: %w", err)
	}

	// Build pod FQDNs for cluster discovery via headless service
	var podFQDNs []string
	var podNames []string
	for i := int32(0); i < replicas; i++ {
		podNames = append(podNames, fmt.Sprintf("pega-opensearch-%d", i))
		podFQDNs = append(podFQDNs, fmt.Sprintf("pega-opensearch-%d.pega-opensearch.%s.svc.cluster.local", i, pega.Namespace))
	}
	seedHosts := strings.Join(podFQDNs, ",")

	env := []corev1.EnvVar{
		{Name: "cluster.name", Value: "pega-opensearch"},
		{Name: "node.name", ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.name"}}},
		{Name: "OPENSEARCH_JAVA_OPTS", Value: javaOpts},
		{Name: "DISABLE_SECURITY_PLUGIN", Value: "true"},
		{Name: "OPENSEARCH_INITIAL_ADMIN_PASSWORD", Value: "Admin_12345!"},
		{Name: "discovery.seed_hosts", Value: seedHosts},
		{Name: "cluster.initial_cluster_manager_nodes", Value: strings.Join(podNames, ",")},
	}

	storageQty := resource.MustParse(storageSize)
	var scName *string
	if pega.Spec.Search.StorageClass != "" {
		scName = &pega.Spec.Search.StorageClass
	}

	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pega-opensearch",
			Namespace: pega.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.StatefulSetSpec{
			ServiceName:         "pega-opensearch",
			PodManagementPolicy: appsv1.ParallelPodManagement,
			Replicas:            &replicas,
			Selector:            &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					ServiceAccountName: "pega-opensearch",
					SecurityContext: &corev1.PodSecurityContext{
						RunAsUser:    int64Ptr(1000),
						RunAsGroup:   int64Ptr(1000),
						FSGroup:      int64Ptr(1000),
						RunAsNonRoot: boolPtr(true),
					},
					Containers: []corev1.Container{
						{
							Name:  "opensearch",
							Image: image,
							Ports: []corev1.ContainerPort{
								{Name: "http", ContainerPort: 9200, Protocol: corev1.ProtocolTCP},
								{Name: "transport", ContainerPort: 9300, Protocol: corev1.ProtocolTCP},
							},
							Env: env,
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
									HTTPGet: &corev1.HTTPGetAction{
										Path:   "/_cluster/health",
										Port:   intstr.FromInt32(9200),
										Scheme: corev1.URISchemeHTTP,
									},
								},
								InitialDelaySeconds: 30,
								PeriodSeconds:       10,
								TimeoutSeconds:      5,
							},
							VolumeMounts: []corev1.VolumeMount{
								{Name: "data", MountPath: "/usr/share/opensearch/data"},
							},
						},
					},
				},
			},
			VolumeClaimTemplates: []corev1.PersistentVolumeClaim{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "data"},
					Spec: corev1.PersistentVolumeClaimSpec{
						AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
						StorageClassName: scName,
						Resources: corev1.VolumeResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceStorage: storageQty,
							},
						},
					},
				},
			},
		},
	}
	if err := ctrl.SetControllerReference(pega, sts, r.Scheme); err != nil {
		return false, err
	}
	if err := r.createOrUpdate(ctx, sts); err != nil {
		return false, fmt.Errorf("statefulset: %w", err)
	}

	// Check readiness
	found := &appsv1.StatefulSet{}
	if err := r.Get(ctx, types.NamespacedName{Name: "pega-opensearch", Namespace: pega.Namespace}, found); err != nil {
		return false, err
	}

	ready := found.Status.ReadyReplicas >= replicas
	status := metav1.ConditionFalse
	reason := "NotReady"
	msg := fmt.Sprintf("OpenSearch %d/%d replicas ready", found.Status.ReadyReplicas, replicas)
	if ready {
		status = metav1.ConditionTrue
		reason = "Ready"
		msg = "OpenSearch cluster is ready"
		logger.V(1).Info("OpenSearch cluster ready", "replicas", replicas)
	}

	meta.SetStatusCondition(&pega.Status.Conditions, metav1.Condition{
		Type:    pegav1alpha1.ConditionSearchReady,
		Status:  status,
		Reason:  reason,
		Message: msg,
	})
	return ready, nil
}

func (r *PegaPlatformReconciler) createOrUpdate(ctx context.Context, obj client.Object) error {
	existing := obj.DeepCopyObject().(client.Object)
	err := r.Get(ctx, types.NamespacedName{Name: obj.GetName(), Namespace: obj.GetNamespace()}, existing)
	if errors.IsNotFound(err) {
		return r.Create(ctx, obj)
	}
	if err != nil {
		return err
	}
	obj.SetResourceVersion(existing.GetResourceVersion())
	obj.SetUID(existing.GetUID())
	obj.SetCreationTimestamp(existing.GetCreationTimestamp())
	obj.SetManagedFields(existing.GetManagedFields())
	if equality.Semantic.DeepEqual(existing, obj) {
		return nil
	}
	return r.Update(ctx, obj)
}
