package controller

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"

	pegav1alpha1 "github.com/redhat-et/pega-operator/api/v1alpha1"
)

func (r *PegaPlatformReconciler) reconcileStreaming(ctx context.Context, pega *pegav1alpha1.PegaPlatform) (bool, error) {
	logger := log.FromContext(ctx).WithName("streaming")

	if !pega.Spec.Stream.Enabled {
		meta.SetStatusCondition(&pega.Status.Conditions, metav1.Condition{
			Type:    pegav1alpha1.ConditionStreamReady,
			Status:  metav1.ConditionTrue,
			Reason:  "Disabled",
			Message: "Kafka streaming is disabled",
		})
		return true, nil
	}

	if !pega.Spec.Stream.Managed {
		meta.SetStatusCondition(&pega.Status.Conditions, metav1.Condition{
			Type:    pegav1alpha1.ConditionStreamReady,
			Status:  metav1.ConditionTrue,
			Reason:  "ExternalStream",
			Message: fmt.Sprintf("Using external Kafka at %s", pega.Spec.Stream.BootstrapServer),
		})
		return true, nil
	}

	logger.Info("reconciling managed Kafka cluster via Strimzi")

	// Verify the Strimzi Kafka CRD exists
	if !r.crdExists(ctx, "kafkas.kafka.strimzi.io") {
		meta.SetStatusCondition(&pega.Status.Conditions, metav1.Condition{
			Type:    pegav1alpha1.ConditionStreamReady,
			Status:  metav1.ConditionFalse,
			Reason:  "StrimziNotFound",
			Message: "Strimzi/AMQ Streams operator not found — install it before enabling managed Kafka",
		})
		return false, nil
	}

	kafkaName := "pega-kafka-cluster"
	replicas := pega.Spec.Stream.Replicas
	if replicas == 0 {
		replicas = 3
	}
	version := pega.Spec.Stream.Version
	if version == "" {
		version = "3.7.0"
	}
	kafkaStorageSize := pega.Spec.Stream.StorageSize
	if kafkaStorageSize == "" {
		kafkaStorageSize = "100Gi"
	}
	zkReplicas := pega.Spec.Stream.ZookeeperReplicas
	if zkReplicas == 0 {
		zkReplicas = 3
	}
	zkStorageSize := pega.Spec.Stream.ZookeeperStorageSize
	if zkStorageSize == "" {
		zkStorageSize = "10Gi"
	}
	replicationFactor := pega.Spec.Stream.ReplicationFactor
	if replicationFactor == 0 {
		replicationFactor = 3
	}

	kafkaStorage := map[string]interface{}{
		"type": "persistent-claim",
		"size": kafkaStorageSize,
	}
	if pega.Spec.Stream.StorageClass != "" {
		kafkaStorage["class"] = pega.Spec.Stream.StorageClass
	}

	zkStorage := map[string]interface{}{
		"type": "persistent-claim",
		"size": zkStorageSize,
	}
	if pega.Spec.Stream.StorageClass != "" {
		zkStorage["class"] = pega.Spec.Stream.StorageClass
	}

	kafka := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "kafka.strimzi.io/v1beta2",
			"kind":       "Kafka",
			"metadata": map[string]interface{}{
				"name":      kafkaName,
				"namespace": pega.Namespace,
			},
			"spec": map[string]interface{}{
				"kafka": map[string]interface{}{
					"version":  version,
					"replicas": int64(replicas),
					"listeners": []interface{}{
						map[string]interface{}{
							"name": "plain",
							"port": int64(9092),
							"type": "internal",
							"tls":  false,
						},
					},
					"config": map[string]interface{}{
						"offsets.topic.replication.factor":         int64(replicationFactor),
						"transaction.state.log.replication.factor": int64(replicationFactor),
						"transaction.state.log.min.isr":            int64(2),
					},
					"storage": kafkaStorage,
				},
				"zookeeper": map[string]interface{}{
					"replicas": int64(zkReplicas),
					"storage":  zkStorage,
				},
			},
		},
	}

	if err := ctrl.SetControllerReference(pega, kafka, r.Scheme); err != nil {
		return false, err
	}

	existing := &unstructured.Unstructured{}
	existing.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "kafka.strimzi.io",
		Version: "v1beta2",
		Kind:    "Kafka",
	})
	err := r.Get(ctx, types.NamespacedName{Name: kafkaName, Namespace: pega.Namespace}, existing)
	if errors.IsNotFound(err) {
		logger.Info("creating Kafka cluster", "name", kafkaName)
		if err := r.Create(ctx, kafka); err != nil {
			return false, fmt.Errorf("creating Kafka CR: %w", err)
		}
	} else if err != nil {
		return false, fmt.Errorf("getting Kafka CR: %w", err)
	} else {
		kafka.SetResourceVersion(existing.GetResourceVersion())
		if err := r.Update(ctx, kafka); err != nil {
			return false, fmt.Errorf("updating Kafka CR: %w", err)
		}
	}

	// Check readiness from Kafka status
	found := &unstructured.Unstructured{}
	found.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "kafka.strimzi.io",
		Version: "v1beta2",
		Kind:    "Kafka",
	})
	if err := r.Get(ctx, types.NamespacedName{Name: kafkaName, Namespace: pega.Namespace}, found); err != nil {
		return false, err
	}

	ready := isKafkaReady(found)
	status := metav1.ConditionFalse
	reason := "NotReady"
	msg := "Kafka cluster is provisioning"
	if ready {
		status = metav1.ConditionTrue
		reason = "Ready"
		msg = "Kafka cluster is ready"
		logger.Info("Kafka cluster ready")
	}

	meta.SetStatusCondition(&pega.Status.Conditions, metav1.Condition{
		Type:    pegav1alpha1.ConditionStreamReady,
		Status:  status,
		Reason:  reason,
		Message: msg,
	})
	return ready, nil
}

func isKafkaReady(kafka *unstructured.Unstructured) bool {
	conditions, found, err := unstructured.NestedSlice(kafka.Object, "status", "conditions")
	if err != nil || !found {
		return false
	}
	for _, c := range conditions {
		cond, ok := c.(map[string]interface{})
		if !ok {
			continue
		}
		condType, _, _ := unstructured.NestedString(cond, "type")
		condStatus, _, _ := unstructured.NestedString(cond, "status")
		if condType == "Ready" && condStatus == "True" {
			return true
		}
	}
	return false
}

func (r *PegaPlatformReconciler) crdExists(ctx context.Context, name string) bool {
	crd := &unstructured.Unstructured{}
	crd.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "apiextensions.k8s.io",
		Version: "v1",
		Kind:    "CustomResourceDefinition",
	})
	err := r.Get(ctx, types.NamespacedName{Name: name}, crd)
	return err == nil
}
