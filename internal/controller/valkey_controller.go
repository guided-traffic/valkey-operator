package controller

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/builder"
)

// ValkeyReconciler reconciles a Valkey object.
type ValkeyReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=vko.gtrfc.com,resources=valkeys,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=vko.gtrfc.com,resources=valkeys/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=vko.gtrfc.com,resources=valkeys/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

// Reconcile handles a reconciliation request for a Valkey resource.
func (r *ValkeyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Fetch the Valkey instance.
	valkey := &vkov1.Valkey{}
	if err := r.Get(ctx, req.NamespacedName, valkey); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("Valkey resource not found, probably deleted")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Set initial provisioning status if phase is empty.
	if valkey.Status.Phase == "" {
		if err := r.updatePhase(ctx, valkey, vkov1.ValkeyPhaseProvisioning, "Setting up Valkey resources"); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Reconcile ConfigMap.
	if err := r.reconcileConfigMap(ctx, valkey); err != nil {
		_ = r.updatePhase(ctx, valkey, vkov1.ValkeyPhaseError, fmt.Sprintf("Failed to reconcile ConfigMap: %v", err))
		return ctrl.Result{}, err
	}

	// Reconcile headless Service.
	if err := r.reconcileHeadlessService(ctx, valkey); err != nil {
		_ = r.updatePhase(ctx, valkey, vkov1.ValkeyPhaseError, fmt.Sprintf("Failed to reconcile headless Service: %v", err))
		return ctrl.Result{}, err
	}

	// Reconcile client Service.
	if err := r.reconcileClientService(ctx, valkey); err != nil {
		_ = r.updatePhase(ctx, valkey, vkov1.ValkeyPhaseError, fmt.Sprintf("Failed to reconcile client Service: %v", err))
		return ctrl.Result{}, err
	}

	// Reconcile StatefulSet.
	if err := r.reconcileStatefulSet(ctx, valkey); err != nil {
		_ = r.updatePhase(ctx, valkey, vkov1.ValkeyPhaseError, fmt.Sprintf("Failed to reconcile StatefulSet: %v", err))
		return ctrl.Result{}, err
	}

	// Update status based on StatefulSet readiness.
	if err := r.updateStatus(ctx, valkey); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// reconcileConfigMap ensures the Valkey ConfigMap matches the desired state.
func (r *ValkeyReconciler) reconcileConfigMap(ctx context.Context, v *vkov1.Valkey) error {
	logger := log.FromContext(ctx)
	desired := builder.BuildConfigMap(v)

	if err := controllerutil.SetControllerReference(v, desired, r.Scheme); err != nil {
		return fmt.Errorf("setting owner reference on ConfigMap: %w", err)
	}

	current := &corev1.ConfigMap{}
	err := r.Get(ctx, types.NamespacedName{Name: desired.Name, Namespace: desired.Namespace}, current)
	if apierrors.IsNotFound(err) {
		logger.Info("Creating ConfigMap", "name", desired.Name)
		return r.Create(ctx, desired)
	}
	if err != nil {
		return err
	}

	// Update if config content has changed.
	if !equality.Semantic.DeepEqual(current.Data, desired.Data) {
		logger.Info("Updating ConfigMap", "name", desired.Name)
		current.Data = desired.Data
		current.Labels = desired.Labels
		return r.Update(ctx, current)
	}

	return nil
}

// reconcileHeadlessService ensures the headless Service exists and matches the desired state.
func (r *ValkeyReconciler) reconcileHeadlessService(ctx context.Context, v *vkov1.Valkey) error {
	desired := builder.BuildHeadlessService(v)
	return r.reconcileService(ctx, v, desired)
}

// reconcileClientService ensures the client-facing Service exists and matches the desired state.
func (r *ValkeyReconciler) reconcileClientService(ctx context.Context, v *vkov1.Valkey) error {
	desired := builder.BuildClientService(v)
	return r.reconcileService(ctx, v, desired)
}

// reconcileService is a generic service reconciler.
func (r *ValkeyReconciler) reconcileService(ctx context.Context, v *vkov1.Valkey, desired *corev1.Service) error {
	logger := log.FromContext(ctx)

	if err := controllerutil.SetControllerReference(v, desired, r.Scheme); err != nil {
		return fmt.Errorf("setting owner reference on Service %s: %w", desired.Name, err)
	}

	current := &corev1.Service{}
	err := r.Get(ctx, types.NamespacedName{Name: desired.Name, Namespace: desired.Namespace}, current)
	if apierrors.IsNotFound(err) {
		logger.Info("Creating Service", "name", desired.Name)
		return r.Create(ctx, desired)
	}
	if err != nil {
		return err
	}

	// Update ports, selector, labels if they changed.
	if !equality.Semantic.DeepEqual(current.Spec.Ports, desired.Spec.Ports) ||
		!equality.Semantic.DeepEqual(current.Spec.Selector, desired.Spec.Selector) {
		logger.Info("Updating Service", "name", desired.Name)
		current.Spec.Ports = desired.Spec.Ports
		current.Spec.Selector = desired.Spec.Selector
		current.Labels = desired.Labels
		return r.Update(ctx, current)
	}

	return nil
}

// reconcileStatefulSet ensures the StatefulSet exists and matches the desired state.
func (r *ValkeyReconciler) reconcileStatefulSet(ctx context.Context, v *vkov1.Valkey) error {
	logger := log.FromContext(ctx)
	desired := builder.BuildStatefulSet(v)

	if err := controllerutil.SetControllerReference(v, desired, r.Scheme); err != nil {
		return fmt.Errorf("setting owner reference on StatefulSet: %w", err)
	}

	current := &appsv1.StatefulSet{}
	err := r.Get(ctx, types.NamespacedName{Name: desired.Name, Namespace: desired.Namespace}, current)
	if apierrors.IsNotFound(err) {
		logger.Info("Creating StatefulSet", "name", desired.Name)
		return r.Create(ctx, desired)
	}
	if err != nil {
		return err
	}

	// Detect drift and update.
	if builder.StatefulSetHasChanged(desired, current) {
		logger.Info("Updating StatefulSet", "name", desired.Name)
		current.Spec.Replicas = desired.Spec.Replicas
		current.Spec.Template = desired.Spec.Template
		current.Labels = desired.Labels
		return r.Update(ctx, current)
	}

	return nil
}

// updateStatus reads the current StatefulSet and updates the Valkey status accordingly.
func (r *ValkeyReconciler) updateStatus(ctx context.Context, v *vkov1.Valkey) error {
	sts := &appsv1.StatefulSet{}
	stsName := types.NamespacedName{
		Name:      builder.ClientServiceName(v),
		Namespace: v.Namespace,
	}

	if err := r.Get(ctx, stsName, sts); err != nil {
		if apierrors.IsNotFound(err) {
			return r.updatePhase(ctx, v, vkov1.ValkeyPhaseProvisioning, "Waiting for StatefulSet creation")
		}
		return err
	}

	// Refresh the Valkey object to avoid conflicts.
	if err := r.Get(ctx, types.NamespacedName{Name: v.Name, Namespace: v.Namespace}, v); err != nil {
		return err
	}

	readyReplicas := sts.Status.ReadyReplicas
	v.Status.ReadyReplicas = readyReplicas

	// Determine phase.
	switch {
	case readyReplicas == v.Spec.Replicas:
		v.Status.Phase = vkov1.ValkeyPhaseOK
		v.Status.Message = "All replicas are ready"

		// In standalone mode, the single pod is the master.
		if v.Spec.Replicas == 1 && !v.IsSentinelEnabled() {
			v.Status.MasterPod = fmt.Sprintf("%s-0", v.Name)
		}

		meta.SetStatusCondition(&v.Status.Conditions, metav1.Condition{
			Type:               "Ready",
			Status:             metav1.ConditionTrue,
			ObservedGeneration: v.Generation,
			Reason:             "AllReplicasReady",
			Message:            "All Valkey replicas are ready",
		})
	case readyReplicas > 0:
		v.Status.Phase = vkov1.ValkeyPhaseProvisioning
		v.Status.Message = fmt.Sprintf("Waiting for replicas: %d/%d ready", readyReplicas, v.Spec.Replicas)

		meta.SetStatusCondition(&v.Status.Conditions, metav1.Condition{
			Type:               "Ready",
			Status:             metav1.ConditionFalse,
			ObservedGeneration: v.Generation,
			Reason:             "ReplicasNotReady",
			Message:            fmt.Sprintf("%d/%d replicas ready", readyReplicas, v.Spec.Replicas),
		})
	default:
		v.Status.Phase = vkov1.ValkeyPhaseProvisioning
		v.Status.Message = "Waiting for replicas to become ready"

		meta.SetStatusCondition(&v.Status.Conditions, metav1.Condition{
			Type:               "Ready",
			Status:             metav1.ConditionFalse,
			ObservedGeneration: v.Generation,
			Reason:             "NoReplicasReady",
			Message:            "No replicas are ready yet",
		})
	}

	return r.Status().Update(ctx, v)
}

// updatePhase is a convenience function to update only the phase and message.
func (r *ValkeyReconciler) updatePhase(ctx context.Context, v *vkov1.Valkey, phase vkov1.ValkeyPhase, message string) error {
	// Refresh the object first to avoid update conflicts.
	if err := r.Get(ctx, types.NamespacedName{Name: v.Name, Namespace: v.Namespace}, v); err != nil {
		return err
	}

	v.Status.Phase = phase
	v.Status.Message = message
	return r.Status().Update(ctx, v)
}

// SetupWithManager sets up the controller with the Manager.
func (r *ValkeyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&vkov1.Valkey{}).
		Owns(&appsv1.StatefulSet{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&corev1.Service{}).
		Complete(r)
}
