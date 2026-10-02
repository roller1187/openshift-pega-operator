package controller

import (
	"context"
	"fmt"
	"time"

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
// +kubebuilder:rbac:groups=kafka.strimzi.io,resources=kafkas,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=kafka.strimzi.io,resources=kafkanodepools,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apiextensions.k8s.io,resources=customresourcedefinitions,verbs=get;list;watch
// +kubebuilder:rbac:groups=config.openshift.io,resources=ingresses,verbs=get;list
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=rolebindings,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=security.openshift.io,resources=securitycontextconstraints,resourceNames=nonroot-v2,verbs=use

type PegaPlatformReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

const requeueDelay = 30 * time.Second

func (r *PegaPlatformReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	pega := &pegav1alpha1.PegaPlatform{}
	if err := r.Get(ctx, req.NamespacedName, pega); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
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
		ready, err := step.reconcile(ctx, pega)
		if err != nil {
			logger.Error(err, "reconciliation failed", "phase", step.phase)
			meta.SetStatusCondition(&pega.Status.Conditions, metav1.Condition{
				Type:    step.condition,
				Status:  metav1.ConditionFalse,
				Reason:  "ReconcileError",
				Message: err.Error(),
			})
			pega.Status.Phase = pegav1alpha1.PhaseFailed
			pega.Status.Message = fmt.Sprintf("Failed during %s: %v", step.phase, err)
			_ = r.Status().Update(ctx, pega)
			return ctrl.Result{}, err
		}

		if !ready {
			logger.V(1).Info("waiting for phase to become ready", "phase", step.phase)
			meta.SetStatusCondition(&pega.Status.Conditions, metav1.Condition{
				Type:    step.condition,
				Status:  metav1.ConditionFalse,
				Reason:  "InProgress",
				Message: step.message,
			})
			pega.Status.Phase = step.phase
			pega.Status.Message = step.message
			_ = r.Status().Update(ctx, pega)
			return ctrl.Result{RequeueAfter: requeueDelay}, nil
		}

		meta.SetStatusCondition(&pega.Status.Conditions, metav1.Condition{
			Type:    step.condition,
			Status:  metav1.ConditionTrue,
			Reason:  "Ready",
			Message: fmt.Sprintf("%s is ready", step.phase),
		})
	}

	if pega.Status.Phase != pegav1alpha1.PhaseReady {
		pega.Status.Phase = pegav1alpha1.PhaseReady
		pega.Status.Message = "Pega platform is fully deployed"
		if err := r.Status().Update(ctx, pega); err != nil {
			return ctrl.Result{}, err
		}
		logger.Info("reconciliation complete", "webURL", pega.Status.WebURL)
	}

	return ctrl.Result{RequeueAfter: 60 * time.Second}, nil
}

func labelsForPega(name string, component string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       "pega",
		"app.kubernetes.io/instance":   name,
		"app.kubernetes.io/component":  component,
		"app.kubernetes.io/managed-by": "pega-operator",
	}
}

func int64Ptr(i int64) *int64 { return &i }
func boolPtr(b bool) *bool   { return &b }

func (r *PegaPlatformReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&pegav1alpha1.PegaPlatform{}).
		Complete(r)
}
