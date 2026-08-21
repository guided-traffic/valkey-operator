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
	// certManagerCertificateNameAnnotation is the annotation cert-manager stamps
	// on every Secret it issues, naming the Certificate that produced it. Verified
	// against cert-manager v1.21.1: present on all 119 issued Secrets of the
	// reference cluster, and the value is the Certificate name — not the Secret
	// name, which can differ (spec.secretName).
	certManagerCertificateNameAnnotation = "cert-manager.io/certificate-name"
	// reasonLegacySentinelTLSNotOwned is the Event reason for legacy Sentinel TLS material that the
	// operator refused to delete for lack of provenance
	// (docs/adr/0006-delete-only-what-the-operator-owns.md, D4-D11).
	reasonLegacySentinelTLSNotOwned = "LegacySentinelTLSNotOwned"
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

	// MaxConcurrentReconciles is how many Valkey CRs are reconciled at the same
	// time. Zero means DefaultMaxConcurrentReconciles; see
	// reconcileControllerOptions.
	MaxConcurrentReconciles int

	// nudges tracks first-seen timestamps for two disjoint key sets: how long
	// each StatefulSet has been short of pods (nudgeShortStatefulSets), and the
	// in-memory copies of the rolling-update wait bounds, keyed by CR name plus
	// a bound suffix (waitBoundKey). See the nudgeTracker type doc.
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
// +kubebuilder:rbac:groups=events.k8s.io,resources=events,verbs=create;patch
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
			r.forgetNudges(req.Namespace, req.Name)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// If the resource is being deleted, do not reconcile any managed resources.
	// Kubernetes garbage collection (via owner references) handles child resource cleanup.
	// This prevents a reboot loop on partially provisioned clusters that are being deleted.
	if !valkey.DeletionTimestamp.IsZero() {
		logger.Info("Valkey resource is being deleted, skipping reconciliation")
		r.forgetNudges(valkey.Namespace, valkey.Name)
		return ctrl.Result{}, nil
	}

	// Set initial provisioning status if phase is empty.
	//
	// A failure here does not end the pass. Returning made a rejected status write
	// — a webhook guarding the CR status subresource, or lost valkeys/status RBAC —
	// skip reconcileResources entirely, so a brand-new CR kept an empty phase and
	// never got a ReconcileBlocked condition: invisible for exactly the failure
	// class that condition exists to surface.
	//
	// Nothing is lost by proceeding. The phase is recomputed and written later in
	// the same pass (persistStatus, or the single writePhase of a blocked pass),
	// and where an earlier return skips that write, this branch is idempotent —
	// the next pass still finds an empty phase and retries it.
	if valkey.Status.Phase == "" {
		if err := r.updatePhase(ctx, valkey, vkov1.ValkeyPhaseProvisioning, "Setting up Valkey resources"); err != nil {
			logger.Error(err, "Failed to write the initial Provisioning phase; continuing the pass")
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

	// Nudge StatefulSets that are short of pods so the statefulset-controller
	// resyncs immediately instead of waiting out its exponential backoff.
	//
	// This runs before the rolling-update checks, not after. Both of them return
	// early while they wait, and both wait for a deleted pod that is being
	// recreated — so when that recreation is blocked, reaching the nudge only
	// afterwards made it unreachable in exactly the situation it exists for.
	// Both StatefulSets are nudged unconditionally; this call's position only
	// guarantees the nudge is reached, it does not decide who gets one.
	//
	// The rolling update keeps requeue authority: shortOfPods is read at the very
	// end of this function, after every rolling-update return, so the 5 s nudge
	// clock never preempts a rolling-update wait.
	shortOfPods := r.nudgeShortStatefulSets(ctx, valkey)

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
	//
	// A non-terminal result (done == false) is not discarded: it is a recheck
	// cadence the check needs but cannot take, because ending the pass here would
	// skip the status write. It is applied at the very end, where every other
	// requeue reason has already had its say.
	pending, done, err := r.handlePostRollingUpdateChecks(ctx, valkey)
	if done {
		return pending, err
	}

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

	// The recheck a post-update check asked for without ending the pass. Zero
	// unless one of them set it, so the healthy path still returns no requeue.
	return pending, nil
}

// handlePostRollingUpdateChecks runs Sentinel rolling updates and no-master recovery
// after the main Valkey rolling update is done. Returns (result, true, err) if the
// caller should return immediately, or (result, false, nil) if processing should
// continue — where a non-zero result is a requeue the pass wants applied only
// after updateStatus has run.
func (r *ValkeyReconciler) handlePostRollingUpdateChecks(ctx context.Context, v *vkov1.Valkey) (ctrl.Result, bool, error) {
	// Sentinel pods use OnDelete strategy — the operator replaces them one by one
	// while verifying sentinel quorum before each deletion.
	if v.IsSentinelEnabled() {
		sentinelResult := r.checkAndHandleSentinelRollingUpdate(ctx, v)
		if sentinelResult.Error != nil {
			_ = r.updatePhase(ctx, v, vkov1.ValkeyPhaseError, fmt.Sprintf("Sentinel rolling update error: %v", sentinelResult.Error))
			// The error is returned, not swallowed: without it the pass ends with
			// no requeue and no error, and because status writes do not re-trigger
			// (GenerationChangedPredicate) reconciliation stalls until an unrelated
			// owned-object event arrives. Returning it hands the retry to the
			// rate limiter, mirroring the data rolling-update error path.
			return ctrl.Result{}, true, sentinelResult.Error
		}
		if sentinelResult.NeedsRequeue {
			return ctrl.Result{RequeueAfter: sentinelResult.RequeueAfter}, true, nil
		}
	}

	// For multi-replica non-Sentinel clusters, detect a no-master state and recover
	// by promoting pod-0. This catches edge cases where all pods come up as replicas
	// (e.g. after staggered restarts where the master pod was the last to restart).
	if v.IsMultiReplicaWithoutSentinel() {
		if recovered, err := r.checkAndRecoverNoMaster(ctx, v); err != nil {
			// Kept as an explicit requeue rather than a returned error: this path
			// already carries its own retry clock.
			_ = r.updatePhase(ctx, v, vkov1.ValkeyPhaseError, fmt.Sprintf("No-master recovery failed: %v", err))
			return ctrl.Result{RequeueAfter: 10 * time.Second}, true, nil
		} else if recovered {
			return ctrl.Result{RequeueAfter: 5 * time.Second}, true, nil
		}
	}

	// Outside a rolling update nothing else re-detects a split brain
	// (docs/adr/0011-evidence-based-steady-state-split-brain-resolution.md, D1). The function
	// self-gates on topology and rolling-update state.
	//
	// Its result is returned even when the pass continues: a split brain it could
	// confirm but not resolve asks for a recheck without ending the pass, because
	// ending it would skip the status write. Dropping the result here would make
	// that recheck unreachable just as surely as dropping it in reconcileWorkload.
	return r.checkSteadyStateSplitBrain(ctx, v)
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
	return r.runReconcileSteps(ctx, valkey, r.resourceReconcileSteps())
}

// resourceReconcileSteps returns the steps of a resource pass, in the order they run.
//
// The order carries one guarantee: **"sidecar RBAC" runs before "StatefulSet"**. The
// sidecar Role grants patch on named pods (ADR 0012 D8 step 3), so on a scale-up the
// Role has to name pod N before the StatefulSet write creates it — otherwise the new
// pod's sidecar 403s on its own role label until the next pass. Moving the StatefulSet
// step ahead of the RBAC step reopens that window; TestResourceReconcileSteps_RBACBeforeStatefulSet
// exists to catch that.
func (r *ValkeyReconciler) resourceReconcileSteps() []reconcileStep {
	return []reconcileStep{
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
	}
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

// reconcileObserver creates the observer ServiceAccount and Deployment when enabled,
// or cleans both up when disabled. The ServiceAccount is written first: the Deployment
// names it, and a pod whose ServiceAccount does not exist yet is rejected by the
// ServiceAccount admission plugin.
func (r *ValkeyReconciler) reconcileObserver(ctx context.Context, valkey *vkov1.Valkey) error {
	if valkey.IsObserverEnabled() {
		if err := r.reconcileObserverServiceAccount(ctx, valkey); err != nil {
			return err
		}
		return r.reconcileObserverDeployment(ctx, valkey)
	}
	return r.cleanupObserverDeployment(ctx, valkey)
}

// reconcileObserverServiceAccount creates or updates the observer ServiceAccount.
// No Role and no RoleBinding accompany it — that is the point of it (ADR 0012 D8 step 2).
func (r *ValkeyReconciler) reconcileObserverServiceAccount(ctx context.Context, v *vkov1.Valkey) error {
	logger := log.FromContext(ctx)
	desired := builder.BuildObserverServiceAccount(v)
	builder.ApplyOperatorVersion(desired, r.OperatorVersion)
	if err := controllerutil.SetControllerReference(v, desired, r.Scheme); err != nil {
		return fmt.Errorf("setting owner reference on Observer ServiceAccount: %w", err)
	}
	current := &corev1.ServiceAccount{}
	err := r.Get(ctx, types.NamespacedName{Name: desired.Name, Namespace: desired.Namespace}, current)
	if apierrors.IsNotFound(err) {
		logger.Info("Creating Observer ServiceAccount", "name", desired.Name)
		if err := r.Create(ctx, desired); err != nil {
			return fmt.Errorf("creating Observer ServiceAccount: %w", err)
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
	logger.Info("Updating Observer ServiceAccount", "name", desired.Name)
	current.Labels = desired.Labels
	current.Annotations = desired.Annotations
	if err := r.Update(ctx, current); err != nil {
		return fmt.Errorf("updating Observer ServiceAccount: %w", err)
	}
	return nil
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

// listDataPodNames returns the names of the pods that currently carry this cluster's
// data-pod selector labels, including pods that are terminating — a pod on its way out
// is exactly the one whose sidecar still needs to patch.
func (r *ValkeyReconciler) listDataPodNames(ctx context.Context, v *vkov1.Valkey) ([]string, error) {
	podList := &corev1.PodList{}
	if err := r.List(ctx, podList,
		client.InNamespace(v.Namespace),
		client.MatchingLabels(common.SelectorLabels(v, common.ComponentValkey)),
	); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(podList.Items))
	for i := range podList.Items {
		names = append(names, podList.Items[i].Name)
	}
	return names, nil
}

// reconcileSidecarRole creates or updates the sidecar Role.
//
// The grant is scoped to named pods, so the pass has to know which pods exist: a
// scale-down keeps a departing pod in the list until it is actually gone, or its
// drain handler could not set its own draining label (builder.SidecarRolePodNames).
// A failed List fails the step rather than narrowing the Role on incomplete
// information — the Role already in the cluster is the wider one, and leaving it
// is the safe direction.
func (r *ValkeyReconciler) reconcileSidecarRole(ctx context.Context, v *vkov1.Valkey) error {
	logger := log.FromContext(ctx)
	livePodNames, err := r.listDataPodNames(ctx, v)
	if err != nil {
		return fmt.Errorf("listing data pods for the sidecar Role: %w", err)
	}
	desired := builder.BuildSidecarRole(v, livePodNames)
	builder.ApplyOperatorVersion(desired, r.OperatorVersion)
	if err := controllerutil.SetControllerReference(v, desired, r.Scheme); err != nil {
		return fmt.Errorf("setting owner reference on sidecar Role: %w", err)
	}
	current := &rbacv1.Role{}
	err = r.Get(ctx, types.NamespacedName{Name: desired.Name, Namespace: desired.Namespace}, current)
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
//
// Neither object is deleted on its name alone. <cr>-sentinel-tls is a name a user
// object can legitimately carry, and a principal who may create Valkey CRs in a
// namespace picks the CR name — so the name is attacker-chosen input, not evidence
// of ownership (docs/adr/0006-delete-only-what-the-operator-owns.md, D4-D11). The two helpers below
// each establish provenance first.
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

	ourCertificate, err := r.deleteLegacySentinelCertificate(ctx, v, legacyName)
	if err != nil {
		return err
	}

	return r.deleteLegacySentinelSecret(ctx, v, legacyName, ourCertificate)
}

// deleteLegacySentinelCertificate removes the legacy per-Sentinel Certificate and
// reports whether the object under that name was one this Valkey owned and whose
// spec.secretName pointed at the legacy Secret name. That verdict is the in-pass
// provenance proof the Secret deletion below consumes; it is returned rather than
// re-read so the Secret decision costs no second API call.
//
// A Certificate that is NOT controlled by this Valkey is left untouched
// (docs/adr/0006-delete-only-what-the-operator-owns.md, D4-D11). The operator sets the
// ownerReference itself on every Certificate it creates (see reconcileCertificate), so ownership is
// a self-issued fact here and needs no external convention. A foreign Certificate under this name
// belongs to someone else; deleting it stops their issuance and renewal.
//
// We GET first so a missing resource costs zero delete-permission attempts: the
// apiserver evaluates authz before existence, so a Delete against a non-existent
// resource on a cluster without `delete` RBAC returns 403 (Forbidden) rather than
// 404 (NotFound) and would loop the reconciler.
func (r *ValkeyReconciler) deleteLegacySentinelCertificate(
	ctx context.Context, v *vkov1.Valkey, legacyName string,
) (bool, error) {
	logger := log.FromContext(ctx)

	cert := &unstructured.Unstructured{}
	cert.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   certManagerGroup,
		Version: "v1",
		Kind:    certManagerKindCertificate,
	})
	err := r.Get(ctx, types.NamespacedName{Name: legacyName, Namespace: v.Namespace}, cert)
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("get legacy certificate %s: %w", legacyName, err)
	}

	if !metav1.IsControlledBy(cert, v) {
		r.warnLegacySentinelTLSNotOwned(ctx, v, certManagerKindCertificate, legacyName,
			"it is not controlled by this Valkey")
		return false, nil
	}

	// The Secret this Certificate issues into. Under the operator's own naming
	// these coincide with legacyName (SentinelCertificateName and
	// SentinelTLSSecretName both derive <cr>-sentinel-tls in split-cert mode),
	// but the check is explicit so a future split of those two derivations
	// fails loudly instead of silently authorising the wrong Secret.
	secretName, _, _ := unstructured.NestedString(cert.Object, "spec", "secretName")

	// UID precondition, same reasoning as cleanupPodDisruptionBudget
	// (docs/adr/0006-delete-only-what-the-operator-owns.md, D8, D9): the ownership decision above was
	// made on a cache-backed read, so the name can hold a different object by the time the Delete
	// lands.
	uid := cert.GetUID()
	switch err := r.Delete(ctx, cert, client.Preconditions{UID: &uid}); {
	case err == nil || apierrors.IsNotFound(err):
		logger.Info("Deleted legacy Sentinel Certificate (unified mode)", "name", legacyName)
	case apierrors.IsConflict(err):
		// The name now holds a different object, which by definition is not the
		// Certificate this pass inspected. Nothing went wrong and nothing is left
		// to do, but the provenance proof no longer applies to whatever is there.
		logger.Info("Skipping legacy Sentinel Certificate deletion: the object was replaced under its name",
			"name", legacyName, "uid", uid)
		return false, nil
	default:
		return false, fmt.Errorf("delete certificate %s: %w", legacyName, err)
	}

	return secretName == legacyName, nil
}

// deleteLegacySentinelSecret removes the Secret the legacy Sentinel Certificate
// produced. cert-manager does not garbage-collect it — the Secrets it issues carry
// no ownerReference unless the controller runs with --enable-certificate-owner-ref
// (verified absent on the reference cluster) — so without this delete the stale TLS
// material lingers and the name stays occupied.
//
// The delete is never taken on the name alone (docs/adr/0006-delete-only-what-the-operator-owns.md,
// D4-D11). One of two provenance proofs must hold, and the Secret must additionally be a TLS
// Secret:
//
//   - ourCertificate: this same pass found a Certificate under legacyName that this
//     Valkey controls and that issues into legacyName. Fully self-issued evidence,
//     but only available while that Certificate still exists.
//   - the cert-manager provenance annotation names legacyName. Retroactive: it sits
//     on Secrets issued long before this guard existed, which is the population the
//     migration actually has to clean up. Verified against cert-manager v1.21.1 on
//     the reference cluster: present on 119 of 119 issued Secrets, and its value is
//     the CERTIFICATE name, not the Secret name.
//
// A Secret that satisfies neither is left alone and reported as an Event. Failing
// this way round leaves stale TLS material, which is recoverable by hand; the other
// direction destroys a Secret the operator never created.
func (r *ValkeyReconciler) deleteLegacySentinelSecret(
	ctx context.Context, v *vkov1.Valkey, legacyName string, ourCertificate bool,
) error {
	logger := log.FromContext(ctx)

	secret := &corev1.Secret{}
	err := r.Get(ctx, types.NamespacedName{Name: legacyName, Namespace: v.Namespace}, secret)
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get legacy secret %s: %w", legacyName, err)
	}

	if reason, ok := legacySentinelSecretIsOurs(secret, legacyName, ourCertificate); !ok {
		r.warnLegacySentinelTLSNotOwned(ctx, v, "Secret", legacyName, reason)
		return nil
	}

	uid := secret.GetUID()
	switch err := r.Delete(ctx, secret, client.Preconditions{UID: &uid}); {
	case err == nil || apierrors.IsNotFound(err):
		logger.Info("Deleted legacy Sentinel TLS Secret (unified mode)", "name", legacyName)
		return nil
	case apierrors.IsConflict(err):
		logger.Info("Skipping legacy Sentinel TLS Secret deletion: the object was replaced under its name",
			"name", legacyName, "uid", uid)
		return nil
	default:
		return fmt.Errorf("delete secret %s: %w", legacyName, err)
	}
}

// legacySentinelSecretIsOurs decides whether the Secret under the legacy name is
// the one the legacy Sentinel Certificate produced. It returns the reason for a
// refusal so the Event can state what was missing rather than only that something
// was. See deleteLegacySentinelSecret for why each proof is admissible.
func legacySentinelSecretIsOurs(secret *corev1.Secret, legacyName string, ourCertificate bool) (string, bool) {
	// A cert-manager-issued TLS Secret is always kubernetes.io/tls. This does not
	// establish provenance on its own — an attacker can point the name at a real
	// TLS Secret — but it removes the entire class of accidental collateral
	// (token, config and registry Secrets) before either proof is consulted.
	if secret.Type != corev1.SecretTypeTLS {
		return fmt.Sprintf("its type is %q, not %q", secret.Type, corev1.SecretTypeTLS), false
	}

	if ourCertificate {
		return "", true
	}

	if secret.Annotations[certManagerCertificateNameAnnotation] == legacyName {
		return "", true
	}

	return fmt.Sprintf("neither a Certificate owned by this Valkey nor the %s annotation identifies it "+
		"as the legacy Sentinel certificate material", certManagerCertificateNameAnnotation), false
}

// warnLegacySentinelTLSNotOwned reports legacy TLS material that carries the name
// the operator would clean up but could not be proven to belong to this Valkey.
//
// It fires on every applicable pass rather than on a transition: the condition is a
// property of the cluster, not of an event, and the recorder aggregates a repeated
// Event into one series. Same shape and rationale as warnPodDisruptionBudgetNotOwned.
func (r *ValkeyReconciler) warnLegacySentinelTLSNotOwned(
	ctx context.Context, v *vkov1.Valkey, kind, name, reason string,
) {
	log.FromContext(ctx).Info("Legacy Sentinel TLS object exists but was not proven to belong to this Valkey; "+
		"leaving it untouched", "kind", kind, "name", name, "reason", reason)
	r.recordEvent(v, corev1.EventTypeWarning, reasonLegacySentinelTLSNotOwned,
		"%s %s carries the legacy Sentinel TLS name but %s; leaving it untouched. "+
			"Remove it by hand once you have confirmed it is unused.", kind, name, reason)
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

	// Delete the Observer ServiceAccount. A deleted CR garbage-collects it through the
	// owner reference; this covers the observer being switched off instead.
	//
	// Ownership-checked and UID-preconditioned, unlike the two name-only cleanups above:
	// <cr-name>-observer is a name a CR author can aim at a pre-existing ServiceAccount,
	// and deleting a foreign one takes every Role bound to it out of service
	// (docs/adr/0006-delete-only-what-the-operator-owns.md).
	if err := r.cleanupObserverServiceAccount(ctx, v); err != nil {
		return err
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

// cleanupObserverServiceAccount deletes the observer ServiceAccount, but only the one
// this CR owns. A foreign ServiceAccount that merely shares the derived name is left
// alone, and the UID precondition keeps the delete on the object that was inspected:
// the ownership decision is made on a cache-backed read, so the name can hold a
// different object by the time the Delete lands (ADR 0006 D8, D9).
func (r *ValkeyReconciler) cleanupObserverServiceAccount(ctx context.Context, v *vkov1.Valkey) error {
	logger := log.FromContext(ctx)

	sa := &corev1.ServiceAccount{}
	name := types.NamespacedName{Name: builder.ObserverServiceAccountName(v), Namespace: v.Namespace}
	if err := r.Get(ctx, name, sa); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return err
	}

	if !metav1.IsControlledBy(sa, v) {
		logger.Info("Skipping Observer ServiceAccount deletion: not owned by this Valkey", "name", sa.Name)
		return nil
	}

	logger.Info("Deleting Observer ServiceAccount", "name", sa.Name)
	err := r.Delete(ctx, sa, client.Preconditions{UID: &sa.UID})
	switch {
	case err == nil || apierrors.IsNotFound(err):
		return nil
	case apierrors.IsConflict(err):
		// The name holds a different object than the one this pass inspected, so there
		// is nothing of ours left to delete. The guard did its job; the pass is not
		// failed over it.
		logger.Info("Skipping Observer ServiceAccount deletion: the object was replaced under its name",
			"name", sa.Name)
		return nil
	default:
		return fmt.Errorf("deleting observer service account: %w", err)
	}
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

			v.Status.MasterPod = r.currentMasterPod(ctx, v)

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

// currentMasterPod reports the pod the non-Sentinel cluster currently serves writes
// from. The HA path has its own answer (clusterState.MasterPod, from Sentinel).
//
// It used to be pod-0 unconditionally, which is a claim the rest of the operator
// contradicts by design: after an abandoned topology restoration the promoted replica
// stays master and TopologyRestored=False says so, and every drain adoption
// (checkSteadyStateSplitBrain) leaves a non-pod-0 master behind. A non-pod-0 master
// is a supported end state -- the -rw/-r Services select on the instanceRole label,
// not on the ordinal -- so the status field was lying exactly where the condition
// next to it was trying to tell the truth (docs/adr/0002-surface-a-blocked-reconcile-on-the-cr.md,
// D11).
//
// The order of the three answers is the order of their authority:
//
//  1. The instanceRole=master label, when exactly one pod carries it. This is the
//     literal selector of the -rw Service, so it is not merely a good guess at the
//     master: it IS the pod that receives writes. Zero or several labeled pods answer
//     nothing -- that is a no-master or split-brain state that checkAndRecoverNoMaster
//     and checkSteadyStateSplitBrain own, and status must not pick a winner there.
//  2. The known-master annotation, the operator's own record of the last promotion it
//     performed or adopted. It is what the replica ConfigMap is built from, so it is
//     the right answer while the labels are in flux (a sidecar that has not repatched
//     yet, or a pod that is restarting).
//  3. Pod-0, which is correct for a single-pod cluster and for any cluster that has
//     never failed over -- and is the honest default when nothing else answers.
//
// Cost: a single cache-served List, and only for multi-replica clusters. A single-pod
// cluster returns without reading anything, because its only pod is pod-0.
func (r *ValkeyReconciler) currentMasterPod(ctx context.Context, v *vkov1.Valkey) string {
	pod0 := fmt.Sprintf("%s-0", common.StatefulSetName(v, common.ComponentValkey))
	if v.Spec.Replicas <= 1 {
		return pod0
	}

	labeled, err := r.listMasterLabeledPods(ctx, v)
	if err != nil {
		log.FromContext(ctx).Info("Cannot list master-labeled pods for the status; falling back to the record",
			"cluster", v.Name, "error", err)
	} else if len(labeled) == 1 {
		return labeled[0].Name
	}

	if recorded := knownMasterPodName(v); recorded != "" {
		return recorded
	}
	return pod0
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
//
// ObservedGeneration is taken from the refreshed object, not from the caller's
// copy. It therefore names the generation the CR carried at the moment of the
// write — which is normally, but not necessarily, the generation the condition
// was computed against: a spec edit landing between the caller's evaluation and
// this refresh makes the condition claim a generation it did not evaluate. That
// over-claim lasts one pass, because the next reconcile recomputes the condition
// from the new spec and overwrites it. The field is stamped at all because tooling
// that judges staleness by observedGeneration (kstatus and everything modelled on
// it) reads a condition without one as generation 0 and therefore as permanently
// stale; every Ready condition already carries it.
//
// The status write is skipped when meta.SetStatusCondition reports no change.
// That helper counts a differing Status, Reason, Message or ObservedGeneration as
// a change (LastTransitionTime alone never is — it only moves on a status flip),
// and v was just refreshed from the API server, so "no change" means the stored
// condition already matches in every field this write would set. Without the skip
// every caller that reports a steady state — setSidecarUpdatePendingCondition on
// each standalone pass being the live one — issued a status update per reconcile,
// which is what the skip guards in setReconcileBlockedCondition exist to avoid.
//
// Both failures are logged and swallowed rather than returned: a condition is a
// report about the pass, never a reason to fail it, and every caller is a void
// helper. Losing one write is self-healing — the condition is recomputed from
// live state on the next pass and rewritten unless it already matches — but it is
// not silent any more: a persistent conflict or a lost permission shows up in the
// operator log instead of leaving the condition stale with no trace.
func (r *ValkeyReconciler) setStatusCondition(ctx context.Context, v *vkov1.Valkey, condType string, status metav1.ConditionStatus, reason, message string) {
	logger := log.FromContext(ctx)

	if err := r.Get(ctx, types.NamespacedName{Name: v.Name, Namespace: v.Namespace}, v); err != nil {
		if !apierrors.IsNotFound(err) {
			logger.Error(err, "Failed to refresh Valkey before writing a status condition", "condition", condType)
		}
		return
	}
	if !meta.SetStatusCondition(&v.Status.Conditions, metav1.Condition{
		Type:               condType,
		Status:             status,
		ObservedGeneration: v.Generation,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: metav1.Now(),
	}) {
		return
	}
	if err := r.Status().Update(ctx, v); err != nil {
		logger.Error(err, "Failed to write status condition; it will be retried on the next reconcile",
			"condition", condType, "reason", reason)
	}
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

// clearSidecarUpdatePending flips SidecarUpdatePending to False, but only for a CR
// that actually carries the condition.
//
// The False branch of setSidecarUpdatePendingCondition had exactly one caller,
// handleStandaloneRollingUpdate, and it was unreachable on the transition that matters
// (docs/adr/0002-surface-a-blocked-reconcile-on-the-cr.md, D10). That function is only entered
// while a rolling update is needed or a state annotation is set; the moment the deferred sidecar
// update actually applies -- an admin deletes the pod, it comes back on the current template --
// neither holds, checkAndHandleRollingUpdate returns before dispatching, and the condition stays
// True with reason SidecarImageDrift for the rest of the cluster's life. Indistinguishable from a
// cluster that never applied it, and permanent drift for anything keyed on the condition.
//
// The presence check is what keeps this from being a new condition on every CR in the
// fleet: meta.SetStatusCondition adds an absent condition and reports a change, so
// calling it unconditionally would write SidecarUpdatePending=False onto clusters that
// never had a sidecar drift -- an upgrade that changes the status of every existing
// cluster. Only a pending-to-resolved transition writes; everything else is one map
// lookup on the healthy path.
func (r *ValkeyReconciler) clearSidecarUpdatePending(ctx context.Context, v *vkov1.Valkey) {
	if meta.FindStatusCondition(v.Status.Conditions, vkov1.ConditionTypeSidecarUpdatePending) == nil {
		return
	}
	r.setSidecarUpdatePendingCondition(ctx, v, false)
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

	// Record the promotion before performing it. The known-master annotation is
	// what checkSteadyStateSplitBrain demotes toward and what the init script
	// self-claims on, so promoting without recording leaves a stale name pointing
	// at some other pod — and this function never gets a second chance to fix it,
	// because the next pass finds a master and short-circuits on hasMaster.
	//
	// Ordering the record before the REPLICAOF is what makes its failure
	// recoverable: nothing is promoted yet, so the returned error simply retries
	// the whole recovery on the next pass (the caller's error path requeues).
	// Naming a pod that is still a replica is harmless meanwhile —
	// confirmedMasterAuthority demotes nothing until the named pod itself reports
	// role:master.
	if err := r.recordPromotedMaster(ctx, v, masterHost); err != nil {
		return false, fmt.Errorf("recording %s as known master: %w", masterPodName, err)
	}

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
		WithOptions(reconcileControllerOptions(r.MaxConcurrentReconciles)).
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
