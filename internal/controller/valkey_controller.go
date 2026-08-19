// Package controller implements the Kubernetes reconciliation logic
// for Valkey custom resources.
package controller

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"reflect"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	policyv1 "k8s.io/api/policy/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlbuilder "sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/builder"
	"github.com/guided-traffic/valkey-operator/internal/common"
	"github.com/guided-traffic/valkey-operator/internal/health"
	"github.com/guided-traffic/valkey-operator/internal/valkeyclient"
)

const (
	// certManagerGroup is the API group of cert-manager resources.
	certManagerGroup = "cert-manager.io"
	// certManagerKindCertificate is the Kind of cert-manager Certificate resources.
	certManagerKindCertificate = "Certificate"
	// conditionTypeReady is the standard "Ready" condition type.
	conditionTypeReady = "Ready"
)

// InstanceChecker verifies connectivity and health of Valkey instances.
// Implementations must provide PingPod for basic connectivity checks,
// CheckCluster for full HA cluster health verification, and GetReplicationInfo
// to query per-pod replication state during rolling updates.
type InstanceChecker interface {
	PingPod(ctx context.Context, v *vkov1.Valkey, podName string) error
	CheckCluster(ctx context.Context, v *vkov1.Valkey) *health.ClusterState
	GetReplicationInfo(ctx context.Context, v *vkov1.Valkey, podName string) (*valkeyclient.ReplicationInfo, error)
}

// ValkeyReconciler reconciles a Valkey object.
type ValkeyReconciler struct {
	client.Client
	Scheme            *runtime.Scheme
	InstanceChecker   InstanceChecker
	Recorder          events.EventRecorder
	OperatorImage     string
	OperatorNamespace string
	OperatorVersion   string
	// NewValkeyClientFn overrides the default Valkey client factory.
	// Used in unit tests to avoid real TCP connections.
	NewValkeyClientFn func(addr, password string, tlsConfig *tls.Config) *valkeyclient.Client

	// nudges tracks how long each StatefulSet has been short of pods.
	// See nudgeShortStatefulSets.
	nudges nudgeTracker
}

// getInstanceChecker returns the configured InstanceChecker or creates a default one.
func (r *ValkeyReconciler) getInstanceChecker() InstanceChecker {
	if r.InstanceChecker != nil {
		return r.InstanceChecker
	}
	return health.NewChecker(r.Client)
}

// buildTLSConfig reads the TLS CA certificate from the specified Secret
// and returns a tls.Config suitable for connecting to TLS-enabled Valkey/Sentinel pods.
// Returns nil if TLS is not enabled.
func (r *ValkeyReconciler) buildTLSConfig(ctx context.Context, v *vkov1.Valkey, secretName string) (*tls.Config, error) {
	if !v.IsTLSEnabled() {
		return nil, nil
	}

	secret := &corev1.Secret{}
	err := r.Get(ctx, types.NamespacedName{
		Name:      secretName,
		Namespace: v.Namespace,
	}, secret)
	if err != nil {
		return nil, fmt.Errorf("reading TLS secret %s: %w", secretName, err)
	}

	caCert, ok := secret.Data["ca.crt"]
	if !ok {
		return nil, fmt.Errorf("TLS secret %s missing ca.crt", secretName)
	}

	certPool := x509.NewCertPool()
	if !certPool.AppendCertsFromPEM(caCert) {
		return nil, fmt.Errorf("failed to parse CA certificate from secret %s", secretName)
	}

	return &tls.Config{
		RootCAs:    certPool,
		MinVersion: tls.VersionTLS12,
	}, nil
}

// newValkeyClient creates a Valkey RESP client, using TLS if tlsConfig is non-nil.
// If NewValkeyClientFn is set on the reconciler (e.g. in tests), it is used instead.
func (r *ValkeyReconciler) newValkeyClient(addr, password string, tlsConfig *tls.Config) *valkeyclient.Client {
	if r.NewValkeyClientFn != nil {
		return r.NewValkeyClientFn(addr, password, tlsConfig)
	}
	if tlsConfig != nil && password != "" {
		return valkeyclient.NewTLSWithPassword(addr, tlsConfig, password)
	}
	if tlsConfig != nil {
		return valkeyclient.NewTLS(addr, tlsConfig)
	}
	if password != "" {
		return valkeyclient.NewWithPassword(addr, password)
	}
	return valkeyclient.New(addr)
}

// readValkeyPassword reads the Valkey auth password from the configured Secret.
// Returns empty string if authentication is not configured or if the secret
// cannot be read (connections are then attempted without auth).
func (r *ValkeyReconciler) readValkeyPassword(ctx context.Context, v *vkov1.Valkey) string {
	if !v.IsAuthEnabled() {
		return ""
	}
	secret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{
		Name:      v.Spec.Auth.SecretName,
		Namespace: v.Namespace,
	}, secret); err != nil {
		return ""
	}
	return string(secret.Data[v.Spec.Auth.SecretPasswordKey])
}

// sentinelPassword returns the password to use when connecting to Sentinel.
// When sentinel auth is disabled (disableAuth: true), Sentinel does not
// require client authentication, so an empty password is returned.
func (r *ValkeyReconciler) sentinelPassword(ctx context.Context, v *vkov1.Valkey) string {
	if v.IsSentinelAuthDisabled() {
		return ""
	}
	return r.readValkeyPassword(ctx, v)
}

// +kubebuilder:rbac:groups=vko.gtrfc.com,resources=valkeys,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=vko.gtrfc.com,resources=valkeys/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=vko.gtrfc.com,resources=valkeys/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch;delete;patch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;delete
// +kubebuilder:rbac:groups=networking.k8s.io,resources=networkpolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=policy,resources=poddisruptionbudgets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=cert-manager.io,resources=certificates,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=monitoring.coreos.com,resources=servicemonitors,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=roles,verbs=get;list;watch;create;update;patch;delete;escalate;bind
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=rolebindings,verbs=get;list;watch;create;update;patch;delete

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

	// If the resource is being deleted, do not reconcile any managed resources.
	// Kubernetes garbage collection (via owner references) handles child resource cleanup.
	// This prevents a reboot loop on partially provisioned clusters that are being deleted.
	if !valkey.DeletionTimestamp.IsZero() {
		logger.Info("Valkey resource is being deleted, skipping reconciliation")
		return ctrl.Result{}, nil
	}

	// Set initial provisioning status if phase is empty.
	if valkey.Status.Phase == "" {
		if err := r.updatePhase(ctx, valkey, vkov1.ValkeyPhaseProvisioning, "Setting up Valkey resources"); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Reconcile all managed resources. The outcome is mirrored into the
	// ReconcileBlocked condition so an admission rejection is distinguishable
	// from any other write failure without reading operator logs.
	//
	// A failure no longer aborts the pass: the data plane (rolling update,
	// nudge, status) is handled either way, so a rejected write on one
	// sub-resource cannot leave the CR status stale about everything else.
	resourceErr := r.reconcileResources(ctx, valkey)
	r.setReconcileBlockedCondition(ctx, valkey, resourceErr)

	if resourceErr != nil {
		// Silence every intermediate phase write for the rest of this pass; the
		// single write below is the phase authority while blocked.
		ctx = withBlockedPass(ctx)
	}

	result, workloadErr := r.reconcileWorkload(ctx, valkey)

	if resourceErr != nil {
		// The one phase write of a blocked pass. It runs even when the workload
		// pass failed, so an early return in there can never leave the phase on
		// whatever the previous pass wrote. writePhase bypasses the suppression
		// that withBlockedPass installed.
		//
		// The error is returned so the controller-runtime rate limiter backs the
		// retry off instead of spinning on the 10 s requeue; a workload failure
		// is joined in rather than dropped.
		_ = r.writePhase(ctx, valkey, vkov1.ValkeyPhaseError,
			fmt.Sprintf("Failed to reconcile resources: %s", compactErrorMessage(resourceErr)))
		return ctrl.Result{}, errors.Join(resourceErr, workloadErr)
	}

	return result, workloadErr
}

// reconcileWorkload handles everything that depends on the running data plane
// rather than on the managed objects themselves: rolling updates, the
// StatefulSet nudge and the status update.
func (r *ValkeyReconciler) reconcileWorkload(ctx context.Context, valkey *vkov1.Valkey) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Check for rolling update (image change on running pods).
	rollingResult := r.checkAndHandleRollingUpdate(ctx, valkey)
	if rollingResult.Error != nil {
		_ = r.updatePhase(ctx, valkey, vkov1.ValkeyPhaseError, fmt.Sprintf("Rolling update error: %v", rollingResult.Error))
		return ctrl.Result{}, rollingResult.Error
	}
	if rollingResult.NeedsRequeue {
		return ctrl.Result{RequeueAfter: rollingResult.RequeueAfter}, nil
	}

	// Check for sentinel pod updates (only when no Valkey rolling update is active).
	if result, done := r.handlePostRollingUpdateChecks(ctx, valkey); done {
		return result, nil
	}

	// Nudge StatefulSets that are short of pods so the statefulset-controller
	// resyncs immediately instead of waiting out its exponential backoff.
	shortOfPods := r.nudgeShortStatefulSets(ctx, valkey)

	// Update status based on StatefulSet readiness.
	if err := r.updateStatus(ctx, valkey); err != nil {
		return ctrl.Result{}, err
	}

	// Requeue when the instance is not yet healthy so transient connectivity
	// failures (e.g. pod just restarted) are retried automatically.
	// Status-only updates do not trigger the GenerationChangedPredicate,
	// so without this explicit requeue the resource would stay in Error/Syncing.
	if valkey.Status.Phase == vkov1.ValkeyPhaseError || valkey.Status.Phase == vkov1.ValkeyphaseSyncing {
		logger.Info("Instance not healthy, requeuing", "phase", valkey.Status.Phase)
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	// A StatefulSet short of pods needs its own requeue. Rejected pod creates leave
	// the CR in Provisioning, which the branch above does not cover — and with no
	// pods, no spec drift and GenerationChangedPredicate on the CR watch, nothing
	// else re-enters Reconcile. The nudge would then never get the second pass its
	// grace period requires, which is exactly the stall it exists to break.
	if shortOfPods {
		return ctrl.Result{RequeueAfter: nudgeRequeueInterval}, nil
	}

	return ctrl.Result{}, nil
}

// handlePostRollingUpdateChecks runs Sentinel rolling updates and no-master recovery
// after the main Valkey rolling update is done. Returns (result, true) if the caller
// should return immediately, or (_, false) if processing should continue.
func (r *ValkeyReconciler) handlePostRollingUpdateChecks(ctx context.Context, v *vkov1.Valkey) (ctrl.Result, bool) {
	// Sentinel pods use OnDelete strategy — the operator replaces them one by one
	// while verifying sentinel quorum before each deletion.
	if v.IsSentinelEnabled() {
		sentinelResult := r.checkAndHandleSentinelRollingUpdate(ctx, v)
		if sentinelResult.Error != nil {
			_ = r.updatePhase(ctx, v, vkov1.ValkeyPhaseError, fmt.Sprintf("Sentinel rolling update error: %v", sentinelResult.Error))
			return ctrl.Result{}, true
		}
		if sentinelResult.NeedsRequeue {
			return ctrl.Result{RequeueAfter: sentinelResult.RequeueAfter}, true
		}
	}

	// For multi-replica non-Sentinel clusters, detect a no-master state and recover
	// by promoting pod-0. This catches edge cases where all pods come up as replicas
	// (e.g. after staggered restarts where the master pod was the last to restart).
	if v.IsMultiReplicaWithoutSentinel() {
		if recovered, err := r.checkAndRecoverNoMaster(ctx, v); err != nil {
			_ = r.updatePhase(ctx, v, vkov1.ValkeyPhaseError, fmt.Sprintf("No-master recovery failed: %v", err))
			return ctrl.Result{RequeueAfter: 10 * time.Second}, true
		} else if recovered {
			return ctrl.Result{RequeueAfter: 5 * time.Second}, true
		}
	}

	return ctrl.Result{}, false
}

// reconcileStep is one unit of work inside a reconcile pass: a display name, an
// optional applicability predicate and the function that performs the write.
type reconcileStep struct {
	name string
	// when reports whether the step applies to this CR. A nil predicate means
	// the step always runs.
	when func(*vkov1.Valkey) bool
	run  func(context.Context, *vkov1.Valkey) error
}

// runReconcileSteps executes every applicable step and returns the joined error
// of all failures.
//
// A failing step does not stop the pass. Steps only reference the objects of
// earlier steps by name (a StatefulSet names its ConfigMap, a Service its
// selector labels), so a rejected write never invalidates the later ones — while
// aborting the pass would leave NetworkPolicies, monitoring and the status
// unreconciled for as long as a single write keeps failing. That is the
// 2026-08-19 infra-d failure mode: one webhook rejection on the Sentinel
// StatefulSet silenced the rest of the reconcile.
func (r *ValkeyReconciler) runReconcileSteps(ctx context.Context, v *vkov1.Valkey, steps []reconcileStep) error {
	var errs []error
	for _, step := range steps {
		if step.when != nil && !step.when(v) {
			continue
		}
		if err := step.run(ctx, v); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", step.name, err))
		}
	}
	return errors.Join(errs...)
}

// needsReplicaConfigMap reports whether the replica ConfigMap applies: it is used
// by every multi-replica topology, with or without Sentinel.
func needsReplicaConfigMap(v *vkov1.Valkey) bool {
	return v.IsSentinelEnabled() || v.IsMultiReplicaWithoutSentinel()
}

// reconcileResources reconciles all Kubernetes resources managed by the operator.
// The returned error joins every step that failed; the caller mirrors it into the
// ReconcileBlocked condition and the CR phase.
func (r *ValkeyReconciler) reconcileResources(ctx context.Context, valkey *vkov1.Valkey) error {
	return r.runReconcileSteps(ctx, valkey, []reconcileStep{
		{name: "ConfigMap", run: r.reconcileConfigMap},
		{name: "replica ConfigMap", when: needsReplicaConfigMap, run: r.reconcileReplicaConfigMap},
		{name: "TLS Certificates", when: (*vkov1.Valkey).IsCertManagerEnabled, run: r.reconcileTLSCertificates},
		{name: "Services", run: r.reconcileServices},
		{name: "sidecar RBAC", run: r.reconcileSidecarRBAC},
		{name: "StatefulSet", run: r.reconcileStatefulSet},
		{name: "Sentinel resources", when: (*vkov1.Valkey).IsSentinelEnabled, run: r.reconcileSentinelResources},
		{name: "PodDisruptionBudgets", run: r.reconcilePodDisruptionBudgets},
		{name: "NetworkPolicies", when: (*vkov1.Valkey).IsNetworkPolicyEnabled, run: r.reconcileNetworkPolicies},
		{name: "monitoring", run: r.reconcileMonitoringResources},
	})
}

// reconcileMonitoringResources reconciles the Observer deployment and the metrics
// exporter Service + ServiceMonitor.
func (r *ValkeyReconciler) reconcileMonitoringResources(ctx context.Context, valkey *vkov1.Valkey) error {
	return r.runReconcileSteps(ctx, valkey, []reconcileStep{
		{name: "Observer", run: r.reconcileObserver},
		{name: "metrics", run: r.reconcileMetrics},
	})
}

// reconcileMetrics reconciles the metrics exporter Service and ServiceMonitor,
// creating them when enabled and cleaning them up when disabled. The exporter
// sidecar container itself is part of the StatefulSet pod template (see
// builder.BuildStatefulSet); enabling/disabling it changes the pod-spec hash and
// is therefore rolled out through the normal failover-aware rolling update.
func (r *ValkeyReconciler) reconcileMetrics(ctx context.Context, valkey *vkov1.Valkey) error {
	return r.runReconcileSteps(ctx, valkey, []reconcileStep{
		{name: "metrics Service", run: r.reconcileMetricsService},
		{name: "ServiceMonitor", run: r.reconcileMetricsServiceMonitor},
	})
}

// reconcileMetricsService creates the metrics Service when metrics are enabled
// and removes it otherwise.
func (r *ValkeyReconciler) reconcileMetricsService(ctx context.Context, valkey *vkov1.Valkey) error {
	if valkey.IsMetricsServiceEnabled() {
		return r.reconcileService(ctx, valkey, builder.BuildMetricsService(valkey))
	}
	return r.cleanupMetricsService(ctx, valkey)
}

// reconcileMetricsServiceMonitor creates the Prometheus-Operator ServiceMonitor
// when enabled and removes it otherwise.
func (r *ValkeyReconciler) reconcileMetricsServiceMonitor(ctx context.Context, valkey *vkov1.Valkey) error {
	if valkey.IsServiceMonitorEnabled() {
		return r.reconcileServiceMonitor(ctx, valkey)
	}
	return r.cleanupServiceMonitor(ctx, valkey)
}

// cleanupMetricsService deletes the metrics Service if it exists.
func (r *ValkeyReconciler) cleanupMetricsService(ctx context.Context, v *vkov1.Valkey) error {
	logger := log.FromContext(ctx)
	svc := &corev1.Service{}
	name := types.NamespacedName{Name: builder.MetricsServiceName(v), Namespace: v.Namespace}
	if err := r.Get(ctx, name, svc); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return err
	}
	logger.Info("Deleting metrics Service", "name", svc.Name)
	if err := r.Delete(ctx, svc); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("deleting metrics service: %w", err)
	}
	return nil
}

// reconcileServiceMonitor ensures a Prometheus-Operator ServiceMonitor matches the
// desired state. The ServiceMonitor is handled as an unstructured object so the
// operator needs no typed dependency on prometheus-operator. When the
// monitoring.coreos.com CRDs are not installed, the reconcile is skipped
// gracefully rather than failing.
func (r *ValkeyReconciler) reconcileServiceMonitor(ctx context.Context, v *vkov1.Valkey) error {
	logger := log.FromContext(ctx)
	desired := builder.BuildServiceMonitor(v)

	ownerRef := builder.ServiceMonitorOwnerRef(v)
	blockOwnerDeletion := true
	isController := true
	ownerRef.BlockOwnerDeletion = &blockOwnerDeletion
	ownerRef.Controller = &isController
	desired.SetOwnerReferences([]metav1.OwnerReference{ownerRef})
	builder.ApplyOperatorVersion(desired, r.OperatorVersion)

	current := &unstructured.Unstructured{}
	current.SetGroupVersionKind(builder.ServiceMonitorGVK())

	err := r.Get(ctx, types.NamespacedName{Name: desired.GetName(), Namespace: desired.GetNamespace()}, current)
	if meta.IsNoMatchError(err) {
		logger.Info("ServiceMonitor CRD not installed; skipping ServiceMonitor reconcile", "name", desired.GetName())
		return nil
	}
	if apierrors.IsNotFound(err) {
		logger.Info("Creating ServiceMonitor", "name", desired.GetName())
		return r.Create(ctx, desired)
	}
	if err != nil {
		return err
	}

	if !equality.Semantic.DeepEqual(desired.Object["spec"], current.Object["spec"]) ||
		builder.OperatorVersionChanged(current, r.OperatorVersion) {
		logger.Info("Updating ServiceMonitor", "name", desired.GetName())
		current.Object["spec"] = desired.Object["spec"]
		current.SetLabels(desired.GetLabels())
		current.SetOwnerReferences(desired.GetOwnerReferences())
		builder.ApplyOperatorVersion(current, r.OperatorVersion)
		return r.Update(ctx, current)
	}

	return nil
}

// cleanupServiceMonitor deletes the ServiceMonitor if it exists. It tolerates the
// monitoring.coreos.com CRDs being absent.
func (r *ValkeyReconciler) cleanupServiceMonitor(ctx context.Context, v *vkov1.Valkey) error {
	logger := log.FromContext(ctx)
	sm := &unstructured.Unstructured{}
	sm.SetGroupVersionKind(builder.ServiceMonitorGVK())
	name := types.NamespacedName{Name: builder.ServiceMonitorName(v), Namespace: v.Namespace}
	if err := r.Get(ctx, name, sm); err != nil {
		if meta.IsNoMatchError(err) || apierrors.IsNotFound(err) {
			return nil
		}
		return err
	}
	logger.Info("Deleting ServiceMonitor", "name", sm.GetName())
	if err := r.Delete(ctx, sm); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("deleting servicemonitor: %w", err)
	}
	return nil
}

// reconcileObserver creates the observer deployment when enabled, or cleans it up when disabled.
func (r *ValkeyReconciler) reconcileObserver(ctx context.Context, valkey *vkov1.Valkey) error {
	if valkey.IsObserverEnabled() {
		return r.reconcileObserverDeployment(ctx, valkey)
	}
	return r.cleanupObserverDeployment(ctx, valkey)
}

// reconcileServices reconciles all Service resources: headless, -rw, and (for multi-replica)
// -all and -r. It also removes legacy services that no longer exist in the new naming scheme.
// A failing Service does not stop the others — see runReconcileSteps.
func (r *ValkeyReconciler) reconcileServices(ctx context.Context, valkey *vkov1.Valkey) error {
	isMultiReplica := func(v *vkov1.Valkey) bool { return v.Spec.Replicas > 1 }
	return r.runReconcileSteps(ctx, valkey, []reconcileStep{
		{name: "headless Service", run: r.reconcileHeadlessService},
		{name: "-rw Service", run: r.reconcileRWService},
		{name: "-all Service", when: isMultiReplica, run: r.reconcileAllService},
		{name: "-r Service", when: isMultiReplica, run: r.reconcileReadOnlyService},
		{name: "legacy Service cleanup", run: r.deleteLegacyServices},
	})
}

// reconcileConfigMap ensures the Valkey ConfigMap matches the desired state.
func (r *ValkeyReconciler) reconcileConfigMap(ctx context.Context, v *vkov1.Valkey) error {
	logger := log.FromContext(ctx)
	desired := builder.BuildConfigMap(v)
	builder.ApplyOperatorVersion(desired, r.OperatorVersion)

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

	// Update if config content or operator version annotation has changed.
	if !equality.Semantic.DeepEqual(current.Data, desired.Data) ||
		builder.OperatorVersionChanged(current, r.OperatorVersion) {
		logger.Info("Updating ConfigMap", "name", desired.Name)
		current.Data = desired.Data
		current.Labels = desired.Labels
		builder.ApplyOperatorVersion(current, r.OperatorVersion)
		return r.Update(ctx, current)
	}

	return nil
}

// reconcileReplicaConfigMap ensures the replica ConfigMap exists in HA mode.
func (r *ValkeyReconciler) reconcileReplicaConfigMap(ctx context.Context, v *vkov1.Valkey) error {
	logger := log.FromContext(ctx)
	desired := builder.BuildReplicaConfigMap(v)
	builder.ApplyOperatorVersion(desired, r.OperatorVersion)

	if err := controllerutil.SetControllerReference(v, desired, r.Scheme); err != nil {
		return fmt.Errorf("setting owner reference on replica ConfigMap: %w", err)
	}

	current := &corev1.ConfigMap{}
	err := r.Get(ctx, types.NamespacedName{Name: desired.Name, Namespace: desired.Namespace}, current)
	if apierrors.IsNotFound(err) {
		logger.Info("Creating replica ConfigMap", "name", desired.Name)
		return r.Create(ctx, desired)
	}
	if err != nil {
		return err
	}

	if !equality.Semantic.DeepEqual(current.Data, desired.Data) ||
		builder.OperatorVersionChanged(current, r.OperatorVersion) {
		logger.Info("Updating replica ConfigMap", "name", desired.Name)
		current.Data = desired.Data
		current.Labels = desired.Labels
		builder.ApplyOperatorVersion(current, r.OperatorVersion)
		return r.Update(ctx, current)
	}

	return nil
}

// reconcileHeadlessService ensures the headless Service exists and matches the desired state.
func (r *ValkeyReconciler) reconcileHeadlessService(ctx context.Context, v *vkov1.Valkey) error {
	desired := builder.BuildHeadlessService(v)
	return r.reconcileService(ctx, v, desired)
}

// reconcileRWService ensures the read-write Service (master-only) exists.
func (r *ValkeyReconciler) reconcileRWService(ctx context.Context, v *vkov1.Valkey) error {
	desired := builder.BuildRWService(v)
	return r.reconcileService(ctx, v, desired)
}

// reconcileAllService ensures the all-pods Service exists (multi-replica only).
func (r *ValkeyReconciler) reconcileAllService(ctx context.Context, v *vkov1.Valkey) error {
	desired := builder.BuildAllService(v)
	return r.reconcileService(ctx, v, desired)
}

// reconcileReadOnlyService ensures the read-only replica Service exists (multi-replica only).
func (r *ValkeyReconciler) reconcileReadOnlyService(ctx context.Context, v *vkov1.Valkey) error {
	desired := builder.BuildReadOnlyService(v)
	return r.reconcileService(ctx, v, desired)
}

// deleteLegacyServices deletes legacy services that have been superseded by the new naming scheme.
// Legacy: <name> (old client service) and <name>-read (old read service).
func (r *ValkeyReconciler) deleteLegacyServices(ctx context.Context, v *vkov1.Valkey) error {
	logger := log.FromContext(ctx)
	legacyNames := []string{
		v.Name,                         // old client service (now replaced by -rw + -all)
		fmt.Sprintf("%s-read", v.Name), // old read service (now replaced by -r)
	}
	for _, name := range legacyNames {
		svc := &corev1.Service{}
		err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: v.Namespace}, svc)
		if apierrors.IsNotFound(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("checking legacy service %s: %w", name, err)
		}
		// Only delete if owned by this Valkey instance (safety check).
		for _, ref := range svc.OwnerReferences {
			if ref.UID == v.UID {
				logger.Info("Deleting legacy Service", "name", name)
				if err := r.Delete(ctx, svc); err != nil && !apierrors.IsNotFound(err) {
					return fmt.Errorf("deleting legacy service %s: %w", name, err)
				}
				break
			}
		}
	}
	return nil
}

// reconcileSidecarRBAC ensures the sidecar ServiceAccount, Role, and RoleBinding exist.
func (r *ValkeyReconciler) reconcileSidecarRBAC(ctx context.Context, v *vkov1.Valkey) error {
	if err := r.reconcileSidecarServiceAccount(ctx, v); err != nil {
		return err
	}
	if err := r.reconcileSidecarRole(ctx, v); err != nil {
		return err
	}
	return r.reconcileSidecarRoleBinding(ctx, v)
}

// reconcileSidecarServiceAccount creates or updates the sidecar ServiceAccount.
func (r *ValkeyReconciler) reconcileSidecarServiceAccount(ctx context.Context, v *vkov1.Valkey) error {
	logger := log.FromContext(ctx)
	desired := builder.BuildSidecarServiceAccount(v)
	builder.ApplyOperatorVersion(desired, r.OperatorVersion)
	if err := controllerutil.SetControllerReference(v, desired, r.Scheme); err != nil {
		return fmt.Errorf("setting owner reference on sidecar ServiceAccount: %w", err)
	}
	current := &corev1.ServiceAccount{}
	err := r.Get(ctx, types.NamespacedName{Name: desired.Name, Namespace: desired.Namespace}, current)
	if apierrors.IsNotFound(err) {
		logger.Info("Creating sidecar ServiceAccount", "name", desired.Name)
		if err := r.Create(ctx, desired); err != nil {
			return fmt.Errorf("creating sidecar ServiceAccount: %w", err)
		}
		return nil
	}
	if err != nil {
		return err
	}
	if equality.Semantic.DeepEqual(current.Labels, desired.Labels) &&
		equality.Semantic.DeepEqual(current.Annotations, desired.Annotations) {
		return nil
	}
	logger.Info("Updating sidecar ServiceAccount", "name", desired.Name)
	current.Labels = desired.Labels
	current.Annotations = desired.Annotations
	if err := r.Update(ctx, current); err != nil {
		return fmt.Errorf("updating sidecar ServiceAccount: %w", err)
	}
	return nil
}

// reconcileSidecarRole creates or updates the sidecar Role.
func (r *ValkeyReconciler) reconcileSidecarRole(ctx context.Context, v *vkov1.Valkey) error {
	logger := log.FromContext(ctx)
	desired := builder.BuildSidecarRole(v)
	builder.ApplyOperatorVersion(desired, r.OperatorVersion)
	if err := controllerutil.SetControllerReference(v, desired, r.Scheme); err != nil {
		return fmt.Errorf("setting owner reference on sidecar Role: %w", err)
	}
	current := &rbacv1.Role{}
	err := r.Get(ctx, types.NamespacedName{Name: desired.Name, Namespace: desired.Namespace}, current)
	if apierrors.IsNotFound(err) {
		logger.Info("Creating sidecar Role", "name", desired.Name)
		if err := r.Create(ctx, desired); err != nil {
			return fmt.Errorf("creating sidecar Role: %w", err)
		}
		return nil
	}
	if err != nil {
		return err
	}
	if equality.Semantic.DeepEqual(current.Rules, desired.Rules) &&
		!builder.OperatorVersionChanged(current, r.OperatorVersion) {
		return nil
	}
	logger.Info("Updating sidecar Role", "name", desired.Name)
	current.Rules = desired.Rules
	builder.ApplyOperatorVersion(current, r.OperatorVersion)
	if err := r.Update(ctx, current); err != nil {
		return fmt.Errorf("updating sidecar Role: %w", err)
	}
	return nil
}

// reconcileSidecarRoleBinding creates or updates the sidecar RoleBinding.
// If the RoleRef changed (immutable field), the RoleBinding is deleted and recreated.
func (r *ValkeyReconciler) reconcileSidecarRoleBinding(ctx context.Context, v *vkov1.Valkey) error {
	logger := log.FromContext(ctx)
	desired := builder.BuildSidecarRoleBinding(v)
	builder.ApplyOperatorVersion(desired, r.OperatorVersion)
	if err := controllerutil.SetControllerReference(v, desired, r.Scheme); err != nil {
		return fmt.Errorf("setting owner reference on sidecar RoleBinding: %w", err)
	}
	current := &rbacv1.RoleBinding{}
	err := r.Get(ctx, types.NamespacedName{Name: desired.Name, Namespace: desired.Namespace}, current)
	if apierrors.IsNotFound(err) {
		logger.Info("Creating sidecar RoleBinding", "name", desired.Name)
		if err := r.Create(ctx, desired); err != nil {
			return fmt.Errorf("creating sidecar RoleBinding: %w", err)
		}
		return nil
	}
	if err != nil {
		return err
	}
	if !equality.Semantic.DeepEqual(current.RoleRef, desired.RoleRef) {
		// RoleRef is immutable — delete and recreate.
		logger.Info("Recreating sidecar RoleBinding (RoleRef changed)", "name", desired.Name)
		if err := r.Delete(ctx, current); err != nil {
			return fmt.Errorf("deleting sidecar RoleBinding for recreation: %w", err)
		}
		if err := r.Create(ctx, desired); err != nil {
			return fmt.Errorf("recreating sidecar RoleBinding: %w", err)
		}
		return nil
	}
	if equality.Semantic.DeepEqual(current.Subjects, desired.Subjects) &&
		equality.Semantic.DeepEqual(current.Labels, desired.Labels) &&
		!builder.OperatorVersionChanged(current, r.OperatorVersion) {
		return nil
	}
	logger.Info("Updating sidecar RoleBinding", "name", desired.Name)
	current.Subjects = desired.Subjects
	current.Labels = desired.Labels
	builder.ApplyOperatorVersion(current, r.OperatorVersion)
	if err := r.Update(ctx, current); err != nil {
		return fmt.Errorf("updating sidecar RoleBinding: %w", err)
	}
	return nil
}

// reconcileService is a generic service reconciler.
func (r *ValkeyReconciler) reconcileService(ctx context.Context, v *vkov1.Valkey, desired *corev1.Service) error {
	logger := log.FromContext(ctx)
	builder.ApplyOperatorVersion(desired, r.OperatorVersion)

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

	// Update ports, selector, labels, or operator version annotation if they changed.
	if !equality.Semantic.DeepEqual(current.Spec.Ports, desired.Spec.Ports) ||
		!equality.Semantic.DeepEqual(current.Spec.Selector, desired.Spec.Selector) ||
		builder.OperatorVersionChanged(current, r.OperatorVersion) {
		logger.Info("Updating Service", "name", desired.Name)
		current.Spec.Ports = desired.Spec.Ports
		current.Spec.Selector = desired.Spec.Selector
		current.Labels = desired.Labels
		builder.ApplyOperatorVersion(current, r.OperatorVersion)
		return r.Update(ctx, current)
	}

	return nil
}

// reconcileStatefulSet ensures the StatefulSet exists and matches the desired state.
func (r *ValkeyReconciler) reconcileStatefulSet(ctx context.Context, v *vkov1.Valkey) error {
	logger := log.FromContext(ctx)
	desired := builder.BuildStatefulSet(v, r.OperatorImage)
	builder.ApplyOperatorVersion(desired, r.OperatorVersion)

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
	if builder.StatefulSetHasChanged(desired, current) || builder.OperatorVersionChanged(current, r.OperatorVersion) {
		logger.Info("Updating StatefulSet", "name", desired.Name)
		current.Spec.Replicas = desired.Spec.Replicas
		current.Spec.Template = desired.Spec.Template
		current.Labels = desired.Labels
		builder.ApplyOperatorVersion(current, r.OperatorVersion)
		return r.Update(ctx, current)
	}

	return nil
}

// reconcileSentinelResources reconciles all Sentinel-related resources.
func (r *ValkeyReconciler) reconcileSentinelResources(ctx context.Context, v *vkov1.Valkey) error {
	// Sentinel ConfigMap.
	if err := r.reconcileSentinelConfigMap(ctx, v); err != nil {
		return fmt.Errorf("sentinel configmap: %w", err)
	}

	// Sentinel headless Service.
	if err := r.reconcileSentinelHeadlessService(ctx, v); err != nil {
		return fmt.Errorf("sentinel headless service: %w", err)
	}

	// Sentinel StatefulSet.
	if err := r.reconcileSentinelStatefulSet(ctx, v); err != nil {
		return fmt.Errorf("sentinel statefulset: %w", err)
	}

	return nil
}

// reconcileSentinelConfigMap ensures the Sentinel ConfigMap matches the desired state.
func (r *ValkeyReconciler) reconcileSentinelConfigMap(ctx context.Context, v *vkov1.Valkey) error {
	logger := log.FromContext(ctx)
	desired := builder.BuildSentinelConfigMap(v)
	builder.ApplyOperatorVersion(desired, r.OperatorVersion)

	if err := controllerutil.SetControllerReference(v, desired, r.Scheme); err != nil {
		return fmt.Errorf("setting owner reference on Sentinel ConfigMap: %w", err)
	}

	current := &corev1.ConfigMap{}
	err := r.Get(ctx, types.NamespacedName{Name: desired.Name, Namespace: desired.Namespace}, current)
	if apierrors.IsNotFound(err) {
		logger.Info("Creating Sentinel ConfigMap", "name", desired.Name)
		return r.Create(ctx, desired)
	}
	if err != nil {
		return err
	}

	if !equality.Semantic.DeepEqual(current.Data, desired.Data) ||
		builder.OperatorVersionChanged(current, r.OperatorVersion) {
		logger.Info("Updating Sentinel ConfigMap", "name", desired.Name)
		current.Data = desired.Data
		current.Labels = desired.Labels
		builder.ApplyOperatorVersion(current, r.OperatorVersion)
		return r.Update(ctx, current)
	}

	return nil
}

// reconcileSentinelHeadlessService ensures the Sentinel headless Service exists.
func (r *ValkeyReconciler) reconcileSentinelHeadlessService(ctx context.Context, v *vkov1.Valkey) error {
	desired := builder.BuildSentinelHeadlessService(v)
	return r.reconcileService(ctx, v, desired)
}

// reconcileSentinelStatefulSet ensures the Sentinel StatefulSet exists and matches desired state.
func (r *ValkeyReconciler) reconcileSentinelStatefulSet(ctx context.Context, v *vkov1.Valkey) error {
	logger := log.FromContext(ctx)
	desired := builder.BuildSentinelStatefulSet(v)
	builder.ApplyOperatorVersion(desired, r.OperatorVersion)

	if err := controllerutil.SetControllerReference(v, desired, r.Scheme); err != nil {
		return fmt.Errorf("setting owner reference on Sentinel StatefulSet: %w", err)
	}

	current := &appsv1.StatefulSet{}
	err := r.Get(ctx, types.NamespacedName{Name: desired.Name, Namespace: desired.Namespace}, current)
	if apierrors.IsNotFound(err) {
		logger.Info("Creating Sentinel StatefulSet", "name", desired.Name)
		return r.Create(ctx, desired)
	}
	if err != nil {
		return err
	}

	if builder.SentinelStatefulSetHasChanged(desired, current) || builder.OperatorVersionChanged(current, r.OperatorVersion) {
		logger.Info("Updating Sentinel StatefulSet", "name", desired.Name)
		current.Spec.Replicas = desired.Spec.Replicas
		current.Spec.Template = desired.Spec.Template
		current.Labels = desired.Labels
		builder.ApplyOperatorVersion(current, r.OperatorVersion)
		return r.Update(ctx, current)
	}

	return nil
}

// reconcileTLSCertificates reconciles cert-manager Certificate resources for TLS.
//
// In default (split-cert) mode the Valkey and Sentinel StatefulSets each own a
// dedicated Certificate. In unified mode (spec.tls.certManager.unifiedCertificate=true)
// a single Valkey Certificate carries both Valkey and Sentinel SANs, and the
// Sentinel StatefulSet mounts the same Secret. The legacy <name>-sentinel-tls
// Certificate (and the Secret it produced) is garbage-collected by
// reconcileLegacySentinelCertificateCleanup once the Sentinel StatefulSet has
// switched to the shared Secret.
func (r *ValkeyReconciler) reconcileTLSCertificates(ctx context.Context, v *vkov1.Valkey) error {
	desired := builder.BuildValkeyCertificate(v)
	if err := r.reconcileCertificate(ctx, v, desired); err != nil {
		return fmt.Errorf("valkey certificate: %w", err)
	}

	if v.IsSentinelEnabled() && !v.IsUnifiedCertificateEnabled() {
		desiredSentinel := builder.BuildSentinelCertificate(v)
		if err := r.reconcileCertificate(ctx, v, desiredSentinel); err != nil {
			return fmt.Errorf("sentinel certificate: %w", err)
		}
	}

	// Garbage-collect the legacy per-Sentinel Certificate when migrating to
	// unified mode. The deletion is gated on the Sentinel StatefulSet already
	// referencing the unified Secret, so on the first pass (before the STS
	// reconcile updates the volume) this is a no-op and cleanup happens on a
	// subsequent reconcile.
	return r.reconcileLegacySentinelCertificateCleanup(ctx, v)
}

// reconcileLegacySentinelCertificateCleanup removes the standalone Sentinel
// Certificate and Secret that pre-date unified-certificate mode. Deletion is
// gated on the Sentinel StatefulSet rollout being fully complete on the unified
// Secret — that is, every Sentinel pod is from the new revision, mounts the
// unified Secret via its kubelet binding, and is Ready. This prevents pulling
// the legacy volume out from under any pod that is still bound to it.
// Idempotent: NotFound is OK.
func (r *ValkeyReconciler) reconcileLegacySentinelCertificateCleanup(ctx context.Context, v *vkov1.Valkey) error {
	if !v.IsCertManagerEnabled() || !v.IsUnifiedCertificateEnabled() {
		return nil
	}

	legacyName := builder.SentinelCertificateName(v)
	if legacyName == builder.ValkeyTLSSecretName(v) {
		// Defensive: never delete the active Secret.
		return nil
	}

	ready, err := r.sentinelRolloutComplete(ctx, v)
	if err != nil {
		return err
	}
	if !ready {
		// Some Sentinel pod still belongs to the previous revision and is
		// therefore kubelet-bound to the legacy Secret. Defer cleanup until
		// checkAndHandleSentinelRollingUpdate has finished rolling all pods.
		return nil
	}

	logger := log.FromContext(ctx)

	// Delete the legacy Certificate only if it actually exists. We GET first
	// so a missing resource costs zero delete-permission attempts: the
	// apiserver evaluates authz before existence, so a Delete against a
	// non-existent resource on a cluster without `delete` RBAC returns 403
	// (Forbidden) rather than 404 (NotFound) and would loop the reconciler.
	cert := &unstructured.Unstructured{}
	cert.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   certManagerGroup,
		Version: "v1",
		Kind:    certManagerKindCertificate,
	})
	if err := r.Get(ctx, types.NamespacedName{Name: legacyName, Namespace: v.Namespace}, cert); err == nil {
		if err := r.Delete(ctx, cert); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("delete certificate %s: %w", legacyName, err)
		}
		logger.Info("Deleted legacy Sentinel Certificate (unified mode)", "name", legacyName)
	} else if !apierrors.IsNotFound(err) {
		return fmt.Errorf("get legacy certificate %s: %w", legacyName, err)
	}

	// cert-manager does not garbage-collect the Secret it produced; drop it
	// explicitly so no stale TLS material lingers and the name is free for
	// future use. Same GET-first guard as above.
	secret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Name: legacyName, Namespace: v.Namespace}, secret); err == nil {
		if err := r.Delete(ctx, secret); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("delete secret %s: %w", legacyName, err)
		}
		logger.Info("Deleted legacy Sentinel TLS Secret (unified mode)", "name", legacyName)
	} else if !apierrors.IsNotFound(err) {
		return fmt.Errorf("get legacy secret %s: %w", legacyName, err)
	}

	return nil
}

// sentinelRolloutComplete reports whether every Sentinel pod has been recreated
// from the current StatefulSet revision and is Ready. It also verifies that the
// StatefulSet's template references the unified Secret as a sanity check.
//
// When Sentinel is disabled, or the StatefulSet does not exist yet, the rollout
// is trivially complete (no pods bound to the legacy Secret).
func (r *ValkeyReconciler) sentinelRolloutComplete(ctx context.Context, v *vkov1.Valkey) (bool, error) {
	if !v.IsSentinelEnabled() {
		return true, nil
	}

	sts := &appsv1.StatefulSet{}
	err := r.Get(ctx, types.NamespacedName{
		Name:      common.StatefulSetName(v, common.ComponentSentinel),
		Namespace: v.Namespace,
	}, sts)
	if apierrors.IsNotFound(err) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("get sentinel statefulset: %w", err)
	}

	// Wait until the StatefulSet controller has observed our latest spec
	// change and computed an updated revision.
	if sts.Status.ObservedGeneration < sts.Generation {
		return false, nil
	}
	if sts.Status.UpdateRevision == "" {
		return false, nil
	}
	if !sentinelStatefulSetUsesSecret(sts, builder.ValkeyTLSSecretName(v)) {
		return false, nil
	}

	desiredReplicas := int32(0)
	if sts.Spec.Replicas != nil {
		desiredReplicas = *sts.Spec.Replicas
	}

	for i := int32(0); i < desiredReplicas; i++ {
		podName := fmt.Sprintf("%s-%d", sts.Name, i)
		pod := &corev1.Pod{}
		if err := r.Get(ctx, types.NamespacedName{Name: podName, Namespace: v.Namespace}, pod); err != nil {
			if apierrors.IsNotFound(err) {
				return false, nil
			}
			return false, fmt.Errorf("get sentinel pod %s: %w", podName, err)
		}
		if pod.Labels[appsv1.StatefulSetRevisionLabel] != sts.Status.UpdateRevision {
			return false, nil
		}
		if !isPodReady(pod) {
			return false, nil
		}
	}

	return true, nil
}

// sentinelStatefulSetUsesSecret reports whether the given Sentinel StatefulSet
// already mounts the named TLS Secret in its "tls" volume.
func sentinelStatefulSetUsesSecret(sts *appsv1.StatefulSet, secretName string) bool {
	for _, vol := range sts.Spec.Template.Spec.Volumes {
		if vol.Name != builder.TLSVolumeName {
			continue
		}
		if vol.Secret != nil && vol.Secret.SecretName == secretName {
			return true
		}
		return false
	}
	return false
}

// reconcileCertificate ensures a cert-manager Certificate resource matches the desired state.
func (r *ValkeyReconciler) reconcileCertificate(ctx context.Context, v *vkov1.Valkey, desired *unstructured.Unstructured) error {
	logger := log.FromContext(ctx)

	// Set owner reference manually for unstructured objects.
	ownerRef := builder.CertificateOwnerRef(v)
	blockOwnerDeletion := true
	isController := true
	ownerRef.BlockOwnerDeletion = &blockOwnerDeletion
	ownerRef.Controller = &isController
	desired.SetOwnerReferences([]metav1.OwnerReference{ownerRef})

	// Apply operator version annotation to the certificate resource.
	builder.ApplyOperatorVersion(desired, r.OperatorVersion)

	current := &unstructured.Unstructured{}
	current.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   certManagerGroup,
		Version: "v1",
		Kind:    certManagerKindCertificate,
	})

	name := desired.GetName()
	namespace := desired.GetNamespace()

	err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, current)
	if apierrors.IsNotFound(err) {
		logger.Info("Creating Certificate", "name", name)
		return r.Create(ctx, desired)
	}
	if err != nil {
		return err
	}

	// Compare spec content to determine if update is needed.
	// Remove fields that cert-manager's webhook adds/manages to avoid
	// infinite update loops (e.g., privateKey.rotationPolicy added in v1.18.0).
	desiredSpec, _, _ := unstructured.NestedMap(desired.Object, "spec")
	currentSpec, _, _ := unstructured.NestedMap(current.Object, "spec")
	cleanseCertificateSpec(desiredSpec)
	cleanseCertificateSpec(currentSpec)

	if !equality.Semantic.DeepEqual(desiredSpec, currentSpec) ||
		builder.OperatorVersionChanged(current, r.OperatorVersion) {
		logger.Info("Updating Certificate", "name", name)
		current.Object["spec"] = desired.Object["spec"]
		current.SetLabels(desired.GetLabels())
		current.SetOwnerReferences(desired.GetOwnerReferences())
		builder.ApplyOperatorVersion(current, r.OperatorVersion)
		return r.Update(ctx, current)
	}

	return nil
}

// cleanseCertificateSpec removes fields from a cert-manager Certificate spec
// that are added or managed by the cert-manager admission webhook.
// This prevents the operator from fighting with the webhook over defaulted fields
// (e.g., privateKey.rotationPolicy added in cert-manager v1.18.0).
func cleanseCertificateSpec(spec map[string]interface{}) {
	if spec == nil {
		return
	}
	// cert-manager's webhook adds spec.privateKey with defaults.
	// Remove the entire privateKey field if we didn't explicitly set it.
	delete(spec, "privateKey")
}

// reconcileNetworkPolicies reconciles all NetworkPolicy resources.
func (r *ValkeyReconciler) reconcileNetworkPolicies(ctx context.Context, v *vkov1.Valkey) error {
	// Valkey NetworkPolicy.
	desiredValkey := builder.BuildValkeyNetworkPolicy(v, r.OperatorNamespace)
	if err := r.reconcileNetworkPolicy(ctx, v, desiredValkey); err != nil {
		return fmt.Errorf("valkey networkpolicy: %w", err)
	}

	// Sentinel NetworkPolicy (only if Sentinel is enabled).
	if v.IsSentinelEnabled() {
		desiredSentinel := builder.BuildSentinelNetworkPolicy(v, r.OperatorNamespace)
		if err := r.reconcileNetworkPolicy(ctx, v, desiredSentinel); err != nil {
			return fmt.Errorf("sentinel networkpolicy: %w", err)
		}
	}

	// Observer NetworkPolicy (only if observer is enabled).
	if v.IsObserverEnabled() {
		desiredObserver := builder.BuildObserverNetworkPolicy(v)
		if err := r.reconcileNetworkPolicy(ctx, v, desiredObserver); err != nil {
			return fmt.Errorf("observer networkpolicy: %w", err)
		}
	}

	return nil
}

// reconcileNetworkPolicy ensures a single NetworkPolicy matches the desired state.
func (r *ValkeyReconciler) reconcileNetworkPolicy(ctx context.Context, v *vkov1.Valkey, desired *networkingv1.NetworkPolicy) error {
	logger := log.FromContext(ctx)
	builder.ApplyOperatorVersion(desired, r.OperatorVersion)

	if err := controllerutil.SetControllerReference(v, desired, r.Scheme); err != nil {
		return fmt.Errorf("setting owner reference on NetworkPolicy %s: %w", desired.Name, err)
	}

	current := &networkingv1.NetworkPolicy{}
	err := r.Get(ctx, types.NamespacedName{Name: desired.Name, Namespace: desired.Namespace}, current)
	if apierrors.IsNotFound(err) {
		logger.Info("Creating NetworkPolicy", "name", desired.Name)
		return r.Create(ctx, desired)
	}
	if err != nil {
		return err
	}

	if builder.NetworkPolicyHasChanged(desired, current) || builder.OperatorVersionChanged(current, r.OperatorVersion) {
		logger.Info("Updating NetworkPolicy", "name", desired.Name)
		current.Spec = desired.Spec
		current.Labels = desired.Labels
		builder.ApplyOperatorVersion(current, r.OperatorVersion)
		return r.Update(ctx, current)
	}

	return nil
}

// reconcileObserverDeployment ensures the Observer Deployment exists and matches the desired state.
func (r *ValkeyReconciler) reconcileObserverDeployment(ctx context.Context, v *vkov1.Valkey) error {
	logger := log.FromContext(ctx)
	desired := builder.BuildObserverDeployment(v, r.OperatorImage)
	builder.ApplyOperatorVersion(desired, r.OperatorVersion)

	if err := controllerutil.SetControllerReference(v, desired, r.Scheme); err != nil {
		return fmt.Errorf("setting owner reference on Observer Deployment: %w", err)
	}

	current := &appsv1.Deployment{}
	err := r.Get(ctx, types.NamespacedName{Name: desired.Name, Namespace: desired.Namespace}, current)
	if apierrors.IsNotFound(err) {
		logger.Info("Creating Observer Deployment", "name", desired.Name)
		return r.Create(ctx, desired)
	}
	if err != nil {
		return err
	}

	if builder.ObserverDeploymentHasChanged(desired, current) || builder.OperatorVersionChanged(current, r.OperatorVersion) {
		logger.Info("Updating Observer Deployment", "name", desired.Name)
		current.Spec = desired.Spec
		current.Labels = desired.Labels
		builder.ApplyOperatorVersion(current, r.OperatorVersion)
		return r.Update(ctx, current)
	}

	return nil
}

// cleanupObserverDeployment removes the Observer Deployment and NetworkPolicy if they exist.
func (r *ValkeyReconciler) cleanupObserverDeployment(ctx context.Context, v *vkov1.Valkey) error {
	logger := log.FromContext(ctx)

	// Delete Observer Deployment.
	deploy := &appsv1.Deployment{}
	deployName := types.NamespacedName{Name: builder.ObserverDeploymentName(v), Namespace: v.Namespace}
	if err := r.Get(ctx, deployName, deploy); err == nil {
		logger.Info("Deleting Observer Deployment", "name", deploy.Name)
		if err := r.Delete(ctx, deploy); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("deleting observer deployment: %w", err)
		}
	}

	// Delete Observer NetworkPolicy if NP is enabled.
	if v.IsNetworkPolicyEnabled() {
		np := &networkingv1.NetworkPolicy{}
		npName := types.NamespacedName{Name: builder.ObserverNetworkPolicyName(v), Namespace: v.Namespace}
		if err := r.Get(ctx, npName, np); err == nil {
			logger.Info("Deleting Observer NetworkPolicy", "name", np.Name)
			if err := r.Delete(ctx, np); err != nil && !apierrors.IsNotFound(err) {
				return fmt.Errorf("deleting observer network policy: %w", err)
			}
		}
	}

	return nil
}

// isObserverDeploymentReady returns true if the observer Deployment has at least one ready replica.
func (r *ValkeyReconciler) isObserverDeploymentReady(ctx context.Context, v *vkov1.Valkey) bool {
	deploy := &appsv1.Deployment{}
	err := r.Get(ctx, types.NamespacedName{
		Name:      builder.ObserverDeploymentName(v),
		Namespace: v.Namespace,
	}, deploy)
	if err != nil {
		return false
	}
	return deploy.Status.ReadyReplicas > 0
}

// updateStatus reads the current StatefulSet and updates the Valkey status accordingly.
func (r *ValkeyReconciler) updateStatus(ctx context.Context, v *vkov1.Valkey) error {
	sts := &appsv1.StatefulSet{}
	stsName := types.NamespacedName{
		Name:      common.StatefulSetName(v, common.ComponentValkey),
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

	// Always record which operator version last reconciled this resource.
	// NOTE: v.Status.OperatorVersion is set inside the sub-functions (after prevStatus capture)
	// so that a version change is detected by statusUnchanged.

	readyReplicas := sts.Status.ReadyReplicas
	v.Status.ReadyReplicas = readyReplicas

	// Update observer status.
	if v.IsObserverEnabled() {
		observerReady := r.isObserverDeploymentReady(ctx, v)
		v.Status.ObserverReady = &observerReady
	} else {
		v.Status.ObserverReady = nil
	}

	// In HA mode, also check Sentinel readiness.
	if v.IsSentinelEnabled() {
		return r.updateHAStatus(ctx, v, readyReplicas)
	}

	// Standalone mode status logic.
	return r.updateStandaloneStatus(ctx, v, readyReplicas)
}

// updateStandaloneStatus updates the status for standalone mode.
func (r *ValkeyReconciler) updateStandaloneStatus(ctx context.Context, v *vkov1.Valkey, readyReplicas int32) error {
	// Capture previous status to detect changes.
	prevStatus := v.Status.DeepCopy()

	switch {
	case readyReplicas == v.Spec.Replicas:
		// Verify actual connectivity to Valkey instances before reporting OK.
		if err := r.verifyValkeyConnectivity(ctx, v); err != nil {
			v.Status.Phase = vkov1.ValkeyPhaseError
			v.Status.Message = fmt.Sprintf("Instance unreachable: %v", err)

			meta.SetStatusCondition(&v.Status.Conditions, metav1.Condition{
				Type:               conditionTypeReady,
				Status:             metav1.ConditionFalse,
				ObservedGeneration: v.Generation,
				Reason:             "ConnectivityCheckFailed",
				Message:            fmt.Sprintf("Operator cannot reach Valkey instance: %v", err),
			})
		} else {
			v.Status.Phase = vkov1.ValkeyPhaseOK
			v.Status.Message = "All replicas are ready"

			// Pod-0 is the master (standalone single pod or ordinal-based multi-replica).
			v.Status.MasterPod = fmt.Sprintf("%s-0", v.Name)

			meta.SetStatusCondition(&v.Status.Conditions, metav1.Condition{
				Type:               conditionTypeReady,
				Status:             metav1.ConditionTrue,
				ObservedGeneration: v.Generation,
				Reason:             "AllReplicasReady",
				Message:            "All Valkey replicas are ready",
			})
		}
	case readyReplicas > 0:
		v.Status.Phase = vkov1.ValkeyPhaseProvisioning
		v.Status.Message = fmt.Sprintf("Waiting for replicas: %d/%d ready", readyReplicas, v.Spec.Replicas)

		meta.SetStatusCondition(&v.Status.Conditions, metav1.Condition{
			Type:               conditionTypeReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: v.Generation,
			Reason:             "ReplicasNotReady",
			Message:            fmt.Sprintf("%d/%d replicas ready", readyReplicas, v.Spec.Replicas),
		})
	default:
		v.Status.Phase = vkov1.ValkeyPhaseProvisioning
		v.Status.Message = "Waiting for replicas to become ready"

		meta.SetStatusCondition(&v.Status.Conditions, metav1.Condition{
			Type:               conditionTypeReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: v.Generation,
			Reason:             "NoReplicasReady",
			Message:            "No replicas are ready yet",
		})
	}

	return r.persistStatus(ctx, v, prevStatus)
}

// updateHAStatus updates the status for HA (Sentinel) mode.
func (r *ValkeyReconciler) updateHAStatus(ctx context.Context, v *vkov1.Valkey, readyReplicas int32) error {
	sentinelReady := int32(0)

	// Check Sentinel StatefulSet readiness.
	sentinelSts := &appsv1.StatefulSet{}
	sentinelName := types.NamespacedName{
		Name:      fmt.Sprintf("%s-sentinel", v.Name),
		Namespace: v.Namespace,
	}
	if err := r.Get(ctx, sentinelName, sentinelSts); err == nil {
		sentinelReady = sentinelSts.Status.ReadyReplicas
	}

	expectedSentinels := int32(3)
	if v.Spec.Sentinel != nil && v.Spec.Sentinel.Replicas > 0 {
		expectedSentinels = v.Spec.Sentinel.Replicas
	}

	allValkeyReady := readyReplicas == v.Spec.Replicas
	allSentinelReady := sentinelReady == expectedSentinels

	// Capture previous status to detect changes.
	prevStatus := v.Status.DeepCopy()

	switch {
	case allValkeyReady && allSentinelReady:
		// Verify actual cluster health before reporting OK.
		checker := r.getInstanceChecker()
		clusterState := checker.CheckCluster(ctx, v)

		if clusterState.Error != nil {
			v.Status.Phase = vkov1.ValkeyPhaseError
			v.Status.Message = fmt.Sprintf("Cluster health check failed: %v", clusterState.Error)

			meta.SetStatusCondition(&v.Status.Conditions, metav1.Condition{
				Type:               conditionTypeReady,
				Status:             metav1.ConditionFalse,
				ObservedGeneration: v.Generation,
				Reason:             "ClusterHealthCheckFailed",
				Message:            fmt.Sprintf("Operator cannot verify cluster health: %v", clusterState.Error),
			})
		} else if !clusterState.AllSynced {
			v.Status.Phase = vkov1.ValkeyphaseSyncing
			v.Status.MasterPod = clusterState.MasterPod
			v.Status.Message = fmt.Sprintf("Replication syncing: %d/%d replicas ready",
				clusterState.ReadyReplicas, clusterState.TotalReplicas)

			meta.SetStatusCondition(&v.Status.Conditions, metav1.Condition{
				Type:               conditionTypeReady,
				Status:             metav1.ConditionFalse,
				ObservedGeneration: v.Generation,
				Reason:             "ReplicationSyncing",
				Message:            fmt.Sprintf("Replication in progress: %d/%d replicas synced", clusterState.ReadyReplicas, clusterState.TotalReplicas),
			})
		} else {
			v.Status.Phase = vkov1.ValkeyPhaseOK
			v.Status.MasterPod = clusterState.MasterPod
			v.Status.Message = fmt.Sprintf("HA cluster ready: %d/%d valkey, %d/%d sentinel",
				readyReplicas, v.Spec.Replicas, sentinelReady, expectedSentinels)

			meta.SetStatusCondition(&v.Status.Conditions, metav1.Condition{
				Type:               conditionTypeReady,
				Status:             metav1.ConditionTrue,
				ObservedGeneration: v.Generation,
				Reason:             "HAClusterReady",
				Message:            "All Valkey and Sentinel instances are ready",
			})
		}
	case readyReplicas > 0 || sentinelReady > 0:
		v.Status.Phase = vkov1.ValkeyPhaseProvisioning
		v.Status.Message = fmt.Sprintf("HA cluster provisioning: %d/%d valkey, %d/%d sentinel",
			readyReplicas, v.Spec.Replicas, sentinelReady, expectedSentinels)

		meta.SetStatusCondition(&v.Status.Conditions, metav1.Condition{
			Type:               conditionTypeReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: v.Generation,
			Reason:             "HAClusterProvisioning",
			Message: fmt.Sprintf("Valkey: %d/%d, Sentinel: %d/%d ready",
				readyReplicas, v.Spec.Replicas, sentinelReady, expectedSentinels),
		})
	default:
		v.Status.Phase = vkov1.ValkeyPhaseProvisioning
		v.Status.Message = "Waiting for HA cluster pods to become ready"

		meta.SetStatusCondition(&v.Status.Conditions, metav1.Condition{
			Type:               conditionTypeReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: v.Generation,
			Reason:             "HAClusterNotReady",
			Message:            "No HA cluster pods are ready yet",
		})
	}

	return r.persistStatus(ctx, v, prevStatus)
}

// persistStatus writes the status computed by updateStandaloneStatus/updateHAStatus,
// skipping the write when nothing changed to prevent infinite reconcile loops.
//
// While the pass is blocked the computed phase and message are dropped and the
// previous ones kept, so the pass keeps its single Error phase write. Everything
// else — readyReplicas, masterPod, observerReady, conditions — keeps updating:
// a rejected managed write says nothing about the running data plane.
func (r *ValkeyReconciler) persistStatus(ctx context.Context, v *vkov1.Valkey, prevStatus *vkov1.ValkeyStatus) error {
	if passIsBlocked(ctx) {
		v.Status.Phase = prevStatus.Phase
		v.Status.Message = prevStatus.Message
	}

	// Set OperatorVersion after prevStatus is captured so a version upgrade triggers an update.
	v.Status.OperatorVersion = r.OperatorVersion
	if statusUnchanged(prevStatus, &v.Status) {
		return nil
	}

	return r.Status().Update(ctx, v)
}

// statusUnchanged compares the key fields of two ValkeyStatus values.
// It returns true if phase, message, readyReplicas, masterPod, operatorVersion, and conditions
// are all equal, meaning no status update is necessary.
func statusUnchanged(prev, curr *vkov1.ValkeyStatus) bool {
	if prev.Phase != curr.Phase {
		return false
	}
	if prev.Message != curr.Message {
		return false
	}
	if prev.ReadyReplicas != curr.ReadyReplicas {
		return false
	}
	if prev.MasterPod != curr.MasterPod {
		return false
	}
	if prev.OperatorVersion != curr.OperatorVersion {
		return false
	}
	if !reflect.DeepEqual(prev.ObserverReady, curr.ObserverReady) {
		return false
	}
	if !reflect.DeepEqual(prev.Conditions, curr.Conditions) {
		return false
	}
	return true
}

// updatePhase is a convenience function to update only the phase and message.
//
// While the pass is blocked the write is dropped: reconcileResources failed, and
// Reconcile ends the pass with a single Error phase write. Without this the health
// phase and the Error phase alternate on every pass and watchers see the CR flap.
func (r *ValkeyReconciler) updatePhase(ctx context.Context, v *vkov1.Valkey, phase vkov1.ValkeyPhase, message string) error {
	if passIsBlocked(ctx) {
		return nil
	}
	return r.writePhase(ctx, v, phase, message)
}

// writePhase updates phase and message unconditionally. Only the final write of a
// blocked pass may use it directly; everything else goes through updatePhase.
func (r *ValkeyReconciler) writePhase(ctx context.Context, v *vkov1.Valkey, phase vkov1.ValkeyPhase, message string) error {
	// Refresh the object first to avoid update conflicts.
	if err := r.Get(ctx, types.NamespacedName{Name: v.Name, Namespace: v.Namespace}, v); err != nil {
		return err
	}

	// Skip update if nothing changed.
	if v.Status.Phase == phase && v.Status.Message == message {
		return nil
	}

	v.Status.Phase = phase
	v.Status.Message = message
	return r.Status().Update(ctx, v)
}

// setStatusCondition sets a named status condition on the Valkey CR.
// It refreshes the object from the API server before writing to avoid conflicts.
func (r *ValkeyReconciler) setStatusCondition(ctx context.Context, v *vkov1.Valkey, condType string, status metav1.ConditionStatus, reason, message string) {
	if err := r.Get(ctx, types.NamespacedName{Name: v.Name, Namespace: v.Namespace}, v); err != nil {
		return
	}
	meta.SetStatusCondition(&v.Status.Conditions, metav1.Condition{
		Type:               condType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: metav1.Now(),
	})
	_ = r.Status().Update(ctx, v)
}

// setSidecarUpdatePendingCondition sets or clears the SidecarUpdatePending condition.
// When pending is true the condition is set to True, indicating that a standalone pod
// has an outdated sidecar image that will be updated on the next natural pod restart.
func (r *ValkeyReconciler) setSidecarUpdatePendingCondition(ctx context.Context, v *vkov1.Valkey, pending bool) {
	if pending {
		r.setStatusCondition(ctx, v,
			vkov1.ConditionTypeSidecarUpdatePending,
			metav1.ConditionTrue,
			"SidecarImageDrift",
			"Standalone pod has an outdated sidecar image; update will occur on the next pod restart")
		return
	}
	r.setStatusCondition(ctx, v,
		vkov1.ConditionTypeSidecarUpdatePending,
		metav1.ConditionFalse,
		"SidecarUpToDate",
		"All sidecar containers are running the desired image")
}

// checkAndRecoverNoMaster detects a no-master state in multi-replica non-Sentinel
// clusters and recovers by promoting pod-0 to master. This can happen when pods
// restart in a staggered fashion and the master pod is the last to restart — all
// pods come up as replicas with circular or broken replication chains.
//
// Returns (true, nil) if recovery was performed, (false, nil) if no recovery needed,
// or (false, err) if an error occurred during detection or recovery.
func (r *ValkeyReconciler) checkAndRecoverNoMaster(ctx context.Context, v *vkov1.Valkey) (bool, error) {
	logger := log.FromContext(ctx)

	// Only act when a rolling update is NOT in progress — during rolling updates
	// the state machine manages topology itself.
	if v.Annotations != nil && v.Annotations[annotationRollingUpdateState] != "" {
		return false, nil
	}

	checker := r.getInstanceChecker()
	stsName := common.StatefulSetName(v, common.ComponentValkey)

	// Check that all pods are reachable before drawing conclusions.
	// If any pod is unreachable, we cannot reliably determine cluster topology.
	hasMaster := false
	unreachable := 0
	for i := int32(0); i < v.Spec.Replicas; i++ {
		podName := fmt.Sprintf("%s-%d", stsName, i)
		info, err := checker.GetReplicationInfo(ctx, v, podName)
		if err != nil {
			unreachable++
			continue
		}
		if info.Role == common.RoleMaster {
			hasMaster = true
			break
		}
	}

	if hasMaster || unreachable > 0 {
		return false, nil
	}

	// All pods are reachable and none is master — recover by promoting pod-0.
	logger.Info("No-master state detected: all pods are replicas, promoting pod-0",
		"cluster", v.Name, "replicas", v.Spec.Replicas)

	if err := r.updatePhase(ctx, v, vkov1.ValkeyPhaseError,
		"No master detected, recovering by promoting pod-0"); err != nil {
		return false, fmt.Errorf("updating phase: %w", err)
	}

	password := r.readValkeyPassword(ctx, v)
	tlsConfig, err := r.buildTLSConfig(ctx, v, builder.ValkeyTLSSecretName(v))
	if err != nil {
		return false, fmt.Errorf("building TLS config: %w", err)
	}

	port := int(builder.ServicePort(v))
	masterPodName := fmt.Sprintf("%s-0", stsName)
	headlessName := common.HeadlessServiceName(v, common.ComponentValkey)
	masterHost := fmt.Sprintf("%s.%s.%s.svc.cluster.local", masterPodName, headlessName, v.Namespace)
	portStr := fmt.Sprintf("%d", port)

	// Promote pod-0 to master.
	masterAddr := health.PodAddressForComponent(v, masterPodName, common.ComponentValkey, port)
	c := r.newValkeyClient(masterAddr, password, tlsConfig)
	if err := c.ReplicaOf("NO", "ONE"); err != nil {
		return false, fmt.Errorf("REPLICAOF NO ONE on %s: %w", masterPodName, err)
	}
	logger.Info("Promoted pod-0 to master via REPLICAOF NO ONE", "pod", masterPodName)

	// Redirect all other pods to replicate from pod-0.
	for i := int32(1); i < v.Spec.Replicas; i++ {
		podName := fmt.Sprintf("%s-%d", stsName, i)
		addr := health.PodAddressForComponent(v, podName, common.ComponentValkey, port)
		rc := r.newValkeyClient(addr, password, tlsConfig)
		if err := rc.ReplicaOf(masterHost, portStr); err != nil {
			logger.Info("Failed to redirect replica to pod-0 (will retry on next reconcile)",
				"pod", podName, "error", err)
		} else {
			logger.Info("Redirected replica to pod-0", "pod", podName, common.RoleMaster, masterHost)
		}
	}

	return true, nil
}

// verifyValkeyConnectivity pings all Valkey pods to verify operator connectivity.
// Returns nil if all pods respond, or the first error encountered.
func (r *ValkeyReconciler) verifyValkeyConnectivity(ctx context.Context, v *vkov1.Valkey) error {
	checker := r.getInstanceChecker()
	for i := int32(0); i < v.Spec.Replicas; i++ {
		podName := fmt.Sprintf("%s-%d", v.Name, i)
		if err := checker.PingPod(ctx, v, podName); err != nil {
			return fmt.Errorf("%s: %w", podName, err)
		}
	}
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ValkeyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		WithOptions(reconcileControllerOptions()).
		For(&vkov1.Valkey{}, ctrlbuilder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Owns(&appsv1.StatefulSet{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.ServiceAccount{}).
		Owns(&rbacv1.Role{}).
		Owns(&rbacv1.RoleBinding{}).
		Owns(&networkingv1.NetworkPolicy{}).
		Owns(&policyv1.PodDisruptionBudget{}).
		Watches(
			&corev1.Secret{},
			handler.EnqueueRequestsFromMapFunc(r.findValkeyForSecret),
		).
		Complete(r)
}

// findValkeyForSecret maps a Secret change to the Valkey resources that reference it.
// This ensures reconciliation triggers when a referenced auth Secret changes.
func (r *ValkeyReconciler) findValkeyForSecret(ctx context.Context, obj client.Object) []reconcile.Request {
	logger := log.FromContext(ctx)

	secret, ok := obj.(*corev1.Secret)
	if !ok {
		return nil
	}

	// List all Valkey resources in the same namespace.
	valkeyList := &vkov1.ValkeyList{}
	if err := r.List(ctx, valkeyList, client.InNamespace(secret.Namespace)); err != nil {
		logger.Error(err, "failed to list Valkey resources for Secret watch")
		return nil
	}

	var requests []reconcile.Request
	for i := range valkeyList.Items {
		v := &valkeyList.Items[i]
		if v.IsAuthEnabled() && v.Spec.Auth.SecretName == secret.Name {
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      v.Name,
					Namespace: v.Namespace,
				},
			})
		}
	}

	if len(requests) > 0 {
		logger.Info("Secret changed, triggering reconcile for Valkey resources",
			"secret", secret.Name, "count", len(requests))
	}

	return requests
}
