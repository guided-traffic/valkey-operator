package controller

import (
	"context"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
)

// SentinelReconciler reconciles a Sentinel object.
type SentinelReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=vko.gtrfc.com,resources=sentinels,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=vko.gtrfc.com,resources=sentinels/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=vko.gtrfc.com,resources=sentinels/finalizers,verbs=update

// Reconcile handles a reconciliation request for a Sentinel resource.
func (r *SentinelReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = log.FromContext(ctx)

	// TODO: implement reconciliation logic
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *SentinelReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&vkov1.Sentinel{}).
		Complete(r)
}
