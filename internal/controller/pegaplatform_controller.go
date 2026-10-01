package controller

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	pegav1alpha1 "github.com/redhat-et/pega-operator/api/v1alpha1"
)

// +kubebuilder:rbac:groups=pega.openshift.io,resources=pegaplatforms,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=pega.openshift.io,resources=pegaplatforms/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=pega.openshift.io,resources=pegaplatforms/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=persistentvolumeclaims,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=route.openshift.io,resources=routes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=autoscaling,resources=horizontalpodautoscalers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.k8s.io,resources=networkpolicies,verbs=get;list;watch;create;update;patch;delete

type PegaPlatformReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

const requeueDelay = 15 * time.Second

func (r *PegaPlatformReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	pega := &pegav1alpha1.PegaPlatform{}
	if err := r.Get(ctx, req.NamespacedName, pega); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if pega.Status.Phase == "" {
		if err := r.setPhase(ctx, pega, pegav1alpha1.PhasePending, "Initializing deployment"); err != nil {
			return ctrl.Result{}, err
		}
	}

	type phaseStep struct {
		phase     pegav1alpha1.PegaPhase
		condition string
		message   string
		reconcile func(context.Context, *pegav1alpha1.PegaPlatform) (bool, error)
	}

	steps := []phaseStep{
		{pegav1alpha1.PhaseDatabase, pegav1alpha1.ConditionDatabaseReady, "Provisioning database", r.reconcileDatabase},
		{pegav1alpha1.PhaseSearch, pegav1alpha1.ConditionSearchReady, "Provisioning search engine", r.reconcileSearch},
		{pegav1alpha1.PhaseStreaming, pegav1alpha1.ConditionStreamReady, "Provisioning Kafka streaming", r.reconcileStreaming},
		{pegav1alpha1.PhaseSRS, pegav1alpha1.ConditionSRSReady, "Deploying Search and Reporting Service", r.reconcileSRS},
		{pegav1alpha1.PhaseInstalling, pegav1alpha1.ConditionInstallerDone, "Running Pega installer", r.reconcileInstaller},
		{pegav1alpha1.PhaseDeploying, pegav1alpha1.ConditionPlatformReady, "Deploying platform tiers", r.reconcilePlatform},
	}

	for _, step := range steps {
		if err := r.setPhase(ctx, pega, step.phase, step.message); err != nil {
			return ctrl.Result{}, err
		}

		ready, err := step.reconcile(ctx, pega)
		if err != nil {
			logger.Error(err, "reconciliation failed", "phase", step.phase)
			meta.SetStatusCondition(&pega.Status.Conditions, metav1.Condition{
				Type:               step.condition,
				Status:             metav1.ConditionFalse,
				Reason:             "ReconcileError",
				Message:            err.Error(),
				LastTransitionTime: metav1.Now(),
			})
			_ = r.setPhase(ctx, pega, pegav1alpha1.PhaseFailed, fmt.Sprintf("Failed during %s: %v", step.phase, err))
			return ctrl.Result{}, err
		}

		if !ready {
			logger.Info("waiting for phase to become ready", "phase", step.phase)
			meta.SetStatusCondition(&pega.Status.Conditions, metav1.Condition{
				Type:               step.condition,
				Status:             metav1.ConditionFalse,
				Reason:             "InProgress",
				Message:            step.message,
				LastTransitionTime: metav1.Now(),
			})
			_ = r.Status().Update(ctx, pega)
			return ctrl.Result{RequeueAfter: requeueDelay}, nil
		}

		meta.SetStatusCondition(&pega.Status.Conditions, metav1.Condition{
			Type:               step.condition,
			Status:             metav1.ConditionTrue,
			Reason:             "Ready",
			Message:            fmt.Sprintf("%s is ready", step.phase),
			LastTransitionTime: metav1.Now(),
		})
		if err := r.Status().Update(ctx, pega); err != nil {
			return ctrl.Result{}, err
		}
	}

	if err := r.setPhase(ctx, pega, pegav1alpha1.PhaseReady, "Pega platform is fully deployed"); err != nil {
		return ctrl.Result{}, err
	}

	logger.Info("reconciliation complete", "webURL", pega.Status.WebURL)
	return ctrl.Result{}, nil
}

func (r *PegaPlatformReconciler) setPhase(ctx context.Context, pega *pegav1alpha1.PegaPlatform, phase pegav1alpha1.PegaPhase, message string) error {
	pega.Status.Phase = phase
	pega.Status.Message = message
	return r.Status().Update(ctx, pega)
}

func labelsForPega(name string, component string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       "pega",
		"app.kubernetes.io/instance":   name,
		"app.kubernetes.io/component":  component,
		"app.kubernetes.io/managed-by": "pega-operator",
	}
}

func (r *PegaPlatformReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&pegav1alpha1.PegaPlatform{}).
		Owns(&appsv1.Deployment{}).
		Owns(&appsv1.StatefulSet{}).
		Owns(&corev1.Service{}).
		Owns(&batchv1.Job{}).
		Complete(r)
}
