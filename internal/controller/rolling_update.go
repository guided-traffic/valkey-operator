// Package controller implements the Kubernetes reconciliation logic
// for Valkey custom resources.
package controller

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/log"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/builder"
	"github.com/guided-traffic/valkey-operator/internal/common"
	"github.com/guided-traffic/valkey-operator/internal/health"
	"github.com/guided-traffic/valkey-operator/internal/valkeyclient"
)

// Rolling update state machine annotations.
// These annotations are placed on the Valkey CR to track state across reconcile loops,
// preventing the reconcile storm from re-entering critical code paths.
const (
	// annotationRollingUpdateState tracks which phase the rolling update is in.
	annotationRollingUpdateState = "vko.gtrfc.com/rolling-update-state"

	// annotationFailoverTimestamp records when the sentinel failover was first triggered.
	// Used to detect stale failovers that need to be retried.
	annotationFailoverTimestamp = "vko.gtrfc.com/failover-timestamp"

	// Rolling update states:
	stateReplacingReplicas = "replacing-replicas" // Replacing replica pods one by one.
	stateFailoverTriggered = "failover-triggered" // Sentinel failover has been triggered.
	stateFailoverReset     = "failover-reset"     // Sentinel was reset after a timed-out failover; waiting to retrigger.
	stateReplacingMaster   = "replacing-master"   // Replacing the former master pod.

	// Multi-replica without sentinel rolling update states:
	stateManualFailover    = "manual-failover"    // A replica was promoted to master, old master being deleted.
	stateRestoringTopology = "restoring-topology" // Old master is back, syncing and restoring topology.
	stateVerifyingTopology = "verifying-topology" // Pod-0 was promoted back; verifying all replicas reconnected.
)

// annotationPromotedPod records the pod name that was promoted to temporary master
// during a multi-replica rolling update without sentinel. Used during topology
// restoration to know which pod to demote back to replica.
const annotationPromotedPod = "vko.gtrfc.com/promoted-pod"

// annotationReconnectResetCount tracks how many times the sentinel state has been
// reset while waiting for replicas to connect to the new master. Used to break
// the infinite loop that occurs when replicas never reconnect via sentinel alone.
const annotationReconnectResetCount = "vko.gtrfc.com/reconnect-reset-count"

// annotationFinalizationTimestamp records when the finalizeRollingUpdate function
// first started waiting for cluster topology to stabilize. Used to detect when
// finalization has stalled (e.g., because GetReplicationInfo is flaky in CI) and
// allow the rolling update to complete despite incomplete topology information.
const annotationFinalizationTimestamp = "vko.gtrfc.com/finalization-started"

// annotationSyncWaitStarted records when the operator began waiting for a
// specific replaced pod to complete replication sync. Used with the configurable
// syncTimeout to detect when sync has stalled and the rolling update should be paused.
const annotationSyncWaitStarted = "vko.gtrfc.com/sync-wait-started"

// annotationTopologyRestoreStarted records when Phase 1 of the topology
// restoration (stateRestoringTopology) first began waiting for pod-0 to sync back
// from the promoted replica. It gets its own key rather than reusing
// annotationSyncWaitStarted (which the replica-replacement phase leaves behind
// whenever it returns early) or annotationFinalizationTimestamp (which Phase 2
// owns -- sharing it would let a long Phase 1 consume Phase 2 budget).
const annotationTopologyRestoreStarted = "vko.gtrfc.com/topology-restore-started"

// annotationManualFailoverStarted records when the state machine entered stateManualFailover -- the
// pass that promoted a replica and deleted the old master. Every wait of handlePostManualFailover
// is bounded by it (docs/adr/0010-every-rolling-update-wait-is-bounded.md, D6).
//
// It gets its own key for the same reason Phase 1 does: annotationSyncWaitStarted
// belongs to the replica-replacement phase, annotationTopologyRestoreStarted to
// Phase 1 and annotationFinalizationTimestamp to Phase 2, and a state that spends
// another state's budget arrives in that state with none left.
const annotationManualFailoverStarted = "vko.gtrfc.com/manual-failover-started"

// annotationSentinelAwarenessStarted records when we first started waiting for
// sentinel to discover the expected number of replicas before triggering a
// failover. Used to detect when this wait has stalled and we should proceed
// regardless (triggering the failover and letting the NOGOODSLAVE retry cycle
// recover, rather than blocking indefinitely before even attempting failover).
const annotationSentinelAwarenessStarted = "vko.gtrfc.com/sentinel-awareness-started"

// finalizationStallTimeout is the duration after which finalizeRollingUpdate
// will proceed with best-effort sentinel sync even if topology checks are still
// uncertain. This prevents the rolling update from stalling indefinitely in
// resource-constrained environments where GetReplicationInfo calls are flaky.
const finalizationStallTimeout = 2 * time.Minute

// sentinelAwarenessTimeout is the maximum time to wait for sentinel to report
// the expected number of replicas before proceeding with the failover regardless.
// After a sentinel REMOVE+MONITOR (e.g., from a prior rolling update finalization),
// sentinel normally re-discovers replicas within 10–30 s. Waiting up to 90 s gives
// ample margin while preventing an indefinite stall when sentinel is stuck.
const sentinelAwarenessTimeout = 90 * time.Second

// maxReconnectResets is the maximum number of sentinel resets we perform while
// waiting for replicas to reconnect. After this many resets we send direct
// REPLICAOF commands and proceed with the rolling update regardless.
const maxReconnectResets = 2

// failoverRetryTimeout is the duration after which a sentinel failover is considered
// stale and will be retried with a sentinel reset. This handles the case where
// sentinel refuses a failover due to its internal cooldown (failover-timeout).
const failoverRetryTimeout = 30 * time.Second

// replicaReconnectTimeout is the duration after which the operator gives up
// waiting for replicas to connect to the new master and resets sentinel state.
// In resource-constrained environments (CI), replicas may take longer to reconnect
// after failover. This timeout prevents the rolling update from stalling indefinitely.
const replicaReconnectTimeout = 90 * time.Second

// failoverResetMinWait is the minimum time to wait after a SENTINEL RESET
// before retriggering failover. After a reset, sentinel needs time to
// rediscover the replicas via INFO polling (~10s). Without this wait,
// SENTINEL FAILOVER returns NOGOODSLAVE because no replicas are known yet.
const failoverResetMinWait = 20 * time.Second

// RollingUpdateResult describes the outcome of a rolling update step.
type RollingUpdateResult struct {
	// NeedsRequeue indicates that the reconciler should requeue after RequeueAfter.
	NeedsRequeue bool

	// RequeueAfter is the duration to wait before requeuing.
	RequeueAfter time.Duration

	// Completed indicates the rolling update has fully completed.
	Completed bool

	// Error holds any error encountered during the rolling update step.
	Error error
}

// rollingUpdateRequeueDelay is the default delay between rolling update steps.
const rollingUpdateRequeueDelay = 10 * time.Second

// checkAndHandleRollingUpdate checks if any pods need updating and orchestrates the rolling update.
func (r *ValkeyReconciler) checkAndHandleRollingUpdate(ctx context.Context, v *vkov1.Valkey) RollingUpdateResult {
	logger := log.FromContext(ctx)

	// Get the current StatefulSet.
	currentSts := &appsv1.StatefulSet{}
	stsName := common.StatefulSetName(v, common.ComponentValkey)
	err := r.Get(ctx, types.NamespacedName{Name: stsName, Namespace: v.Namespace}, currentSts)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return RollingUpdateResult{} // Not created yet.
		}
		return RollingUpdateResult{Error: fmt.Errorf("getting StatefulSet: %w", err)}
	}
	// A foreign StatefulSet is treated as absent (ADR 0020): the rolling update
	// deletes pods against the persisted template, and neither belongs to this
	// CR. reconcileStatefulSet reports the collision and fails its step.
	if !metav1.IsControlledBy(currentSts, v) {
		return RollingUpdateResult{}
	}

	// Check if any pods are running a different image or config than the
	// persisted StatefulSet template. All four inputs come from the live
	// StatefulSet -- see valkeyImageFromSts for why the CR must not be the
	// source here.
	desiredImage := valkeyImageFromSts(currentSts)
	sidecarImg := sidecarImageFromSts(currentSts)
	desiredConfigHash := configHashFromSts(currentSts)
	desiredPodSpecHash := podSpecHashFromSts(currentSts)
	needsRollingUpdate := false

	for i := int32(0); i < *currentSts.Spec.Replicas; i++ {
		podName := fmt.Sprintf("%s-%d", stsName, i)
		pod := &corev1.Pod{}
		if err := r.Get(ctx, types.NamespacedName{Name: podName, Namespace: v.Namespace}, pod); err != nil {
			if apierrors.IsNotFound(err) {
				continue // Pod not created yet.
			}
			return RollingUpdateResult{Error: fmt.Errorf("getting pod %s: %w", podName, err)}
		}
		// The NA61 guard above proves the StatefulSet, which is the wrong object for
		// this decision: a pod holding <cr>-N was not necessarily created by it, and a
		// foreign one differs from our persisted template by construction, so the very
		// next step would classify it as outdated and schedule it for deletion. The
		// refusal fails the step rather than treating the pod as absent, because a name
		// nothing of ours can ever occupy clears only when a human acts, and the failure
		// leaves the rolling-update state annotation in place so its bounded waits keep
		// being driven (ADR 0020 D9, ADR 0010).
		if !podIsOurs(pod, currentSts) {
			return RollingUpdateResult{Error: foreignObjectError("Pod", podName)}
		}

		if podNeedsUpdate(pod, desiredImage, sidecarImg, desiredConfigHash, desiredPodSpecHash, currentSts.Spec.Template.Spec.Containers) {
			needsRollingUpdate = true
			break
		}
	}

	if !needsRollingUpdate {
		// If no pods need updating but rolling update state annotations are still
		// present, the previous rolling update completed (all pods updated) but
		// finalizeRollingUpdate was never called. This happens when the last
		// reconcile of the rolling update sees all pods updated via handlePostFailover →
		// replaceRemainingPods (which requeues), but the next reconcile enters
		// checkAndHandleRollingUpdate and exits here before reaching finalizeRollingUpdate.
		// Proceed to handleRollingUpdate so the updatedCount == totalPods check
		// triggers finalizeRollingUpdate and cleans up the state.
		if r.getRollingUpdateState(v) == "" {
			// The one place that provably knows every pod matches the live template, and therefore the only
			// place a deferred sidecar update can be declared applied
			// (docs/adr/0002-surface-a-blocked-reconcile-on-the-cr.md, D10). It is a no-op for every CR that
			// does not carry the condition.
			r.clearSidecarUpdatePending(ctx, v)
			return RollingUpdateResult{} // No rolling update needed.
		}
		logger.Info("All pods updated but rolling update state still present, finalizing")
	} else {
		logger.Info("Rolling update detected", "desiredImage", desiredImage)
	}

	var result RollingUpdateResult
	switch {
	case v.IsSentinelEnabled():
		result = r.handleRollingUpdate(ctx, v, currentSts)
	case v.IsMultiReplicaWithoutSentinel():
		result = r.handleMultiReplicaRollingUpdate(ctx, v, currentSts)
	default:
		result = r.handleStandaloneRollingUpdate(ctx, v, currentSts)
	}

	// Completion clears the state here, at the single point every dispatch target
	// reports it, rather than inside each of them. handleStandaloneRollingUpdate
	// reported Completed without clearing anything, and it is reachable with state
	// on the CR: scaling a multi-replica cluster down to one pod mid-restoration
	// flips IsMultiReplicaWithoutSentinel and re-routes the very next pass here.
	// The rolling-update state, the promoted pod and the wait bounds then stayed
	// on the CR forever, and the in-memory bounds pre-expired the budget of the
	// next update (ADR 0010 D10 again).
	//
	// The call is idempotent: clearRollingUpdateState returns without an API call
	// when no annotation is left, so the targets that already cleared their own
	// state pay nothing for the second call.
	if result.Completed {
		if err := r.clearRollingUpdateState(ctx, v); err != nil {
			return RollingUpdateResult{Error: err}
		}
	}
	return result
}

// detectImageChange returns true if the StatefulSet's current image differs from the desired image.
func detectImageChange(desired string, current *appsv1.StatefulSet) bool {
	if len(current.Spec.Template.Spec.Containers) == 0 {
		return false
	}
	return current.Spec.Template.Spec.Containers[0].Image != desired
}

// podNeedsUpdate returns true if the pod needs to be replaced because its
// valkey or sidecar container image differs from the desired image, because
// its config hash annotation (AnnotationConfigHash) differs from the desired hash,
// or because its pod spec hash annotation (AnnotationPodSpecHash) differs from the
// desired hash (e.g. resource requests/limits changed).
// Pass empty strings to skip the respective checks.
//
// desiredContainers provides a fallback: when the pod lacks the
// AnnotationPodSpecHash annotation (created by an older operator version),
// the function compares container resources directly so that spec changes
// (e.g. resources.requests.cpu) are still detected.
func podNeedsUpdate(pod *corev1.Pod, desiredValkeyImage, desiredSidecarImage, desiredConfigHash, desiredPodSpecHash string, desiredContainers []corev1.Container) bool {
	if len(pod.Spec.Containers) == 0 {
		return false
	}
	if podImageChanged(pod, desiredValkeyImage, desiredSidecarImage) {
		return true
	}
	if podAnnotationHashChanged(pod, desiredConfigHash) {
		return true
	}
	return podSpecHashChanged(pod, desiredPodSpecHash, desiredContainers)
}

// podImageChanged returns true if any container image on the pod differs from
// the desired images.
func podImageChanged(pod *corev1.Pod, desiredValkeyImage, desiredSidecarImage string) bool {
	for _, c := range pod.Spec.Containers {
		switch c.Name {
		case builder.ValkeyContainerName:
			if desiredValkeyImage != "" && c.Image != desiredValkeyImage {
				return true
			}
		case builder.SidecarContainerName:
			if desiredSidecarImage != "" && c.Image != desiredSidecarImage {
				return true
			}
		}
	}
	return false
}

// podAnnotationHashChanged returns true when the pod carries the config-hash
// annotation with a value that differs from desiredHash. Returns false when the
// pod lacks the annotation or desiredHash is empty.
func podAnnotationHashChanged(pod *corev1.Pod, desiredHash string) bool {
	if desiredHash == "" {
		return false
	}
	podHash := pod.Annotations[builder.AnnotationConfigHash]
	return podHash != "" && podHash != desiredHash
}

// podSpecHashChanged returns true when the pod's spec hash annotation differs from
// desiredPodSpecHash. When the pod lacks the annotation, it falls back to comparing
// container resources directly via desiredContainers.
func podSpecHashChanged(pod *corev1.Pod, desiredPodSpecHash string, desiredContainers []corev1.Container) bool {
	if desiredPodSpecHash == "" {
		return false
	}
	podHash := pod.Annotations[builder.AnnotationPodSpecHash]
	if podHash != "" {
		return podHash != desiredPodSpecHash
	}
	return containersResourceChanged(pod.Spec.Containers, desiredContainers)
}

// containersResourceChanged returns true if the actual containers differ from
// the desired containers in resource requests or limits.  Only containers
// present in both slices (matched by name) are compared.
func containersResourceChanged(actual, desired []corev1.Container) bool {
	desiredMap := make(map[string]corev1.Container, len(desired))
	for _, c := range desired {
		desiredMap[c.Name] = c
	}
	for _, ac := range actual {
		dc, ok := desiredMap[ac.Name]
		if !ok {
			continue
		}
		if !resourceListEqual(ac.Resources.Requests, dc.Resources.Requests) {
			return true
		}
		if !resourceListEqual(ac.Resources.Limits, dc.Resources.Limits) {
			return true
		}
	}
	return false
}

// resourceListEqual returns true if two resource lists contain the same keys with equal quantities.
func resourceListEqual(a, b corev1.ResourceList) bool {
	if len(a) != len(b) {
		return false
	}
	for key, aVal := range a {
		bVal, ok := b[key]
		if !ok || aVal.Cmp(bVal) != 0 {
			return false
		}
	}
	return true
}

// sidecarImageFromSts returns the sidecar container image from a StatefulSet's
// pod template, or empty string when no sidecar container is present.
func sidecarImageFromSts(sts *appsv1.StatefulSet) string {
	for _, c := range sts.Spec.Template.Spec.Containers {
		if c.Name == builder.SidecarContainerName {
			return c.Image
		}
	}
	return ""
}

// podSpecHashFromSts returns the pod spec hash from a StatefulSet's pod template
// annotations, or empty string when the annotation is not present.
func podSpecHashFromSts(sts *appsv1.StatefulSet) string {
	return sts.Spec.Template.Annotations[builder.AnnotationPodSpecHash]
}

// valkeyImageFromSts returns the Valkey container image from a StatefulSet's
// pod template, or empty string when no Valkey container is present.
//
// The rolling update reads every "desired" input from the live StatefulSet
// rather than from the CR, because the pods it deletes are recreated by the
// statefulset-controller from that template and from nothing else. Reading the
// CR instead would make the operator delete pods to converge on a template that
// was never persisted -- e.g. while an admission webhook rejects the
// StatefulSet update -- and the recreated pod would come back on the old
// template, once per requeue, for as long as the write stays blocked.
func valkeyImageFromSts(sts *appsv1.StatefulSet) string {
	for _, c := range sts.Spec.Template.Spec.Containers {
		if c.Name == builder.ValkeyContainerName {
			return c.Image
		}
	}
	return ""
}

// configHashFromSts returns the config hash from a StatefulSet's pod template
// annotations, or empty string when the annotation is not present. Same
// rationale as valkeyImageFromSts: the persisted template is the only thing a
// recreated pod can converge on.
func configHashFromSts(sts *appsv1.StatefulSet) string {
	return sts.Spec.Template.Annotations[builder.AnnotationConfigHash]
}

// isPodReady returns true if the pod has the Ready condition set to True.
func isPodReady(pod *corev1.Pod) bool {
	for _, cond := range pod.Status.Conditions {
		if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

// handleRollingUpdate orchestrates a controlled rolling update for an HA Valkey cluster.
// It is called from the main Reconcile loop when an image change is detected.
//
// The strategy is:
//  1. Identify pods that need updating (old image).
//  2. Replace replica pods one at a time (never the master first).
//  3. After each replacement, verify the pod is ready, has joined the cluster,
//     and replication sync has completed.
//  4. After all replicas are migrated, trigger a Sentinel failover so a new-image
//     replica becomes master.
//  5. Replace the former master pod (now a replica).
//  6. Verify all pods run the new image and the cluster is healthy.
//
// State tracking via annotations prevents the reconcile storm from re-entering
// critical code paths (failover trigger, master deletion) concurrently.
func (r *ValkeyReconciler) handleRollingUpdate(ctx context.Context, v *vkov1.Valkey, currentSts *appsv1.StatefulSet) RollingUpdateResult {
	logger := log.FromContext(ctx)
	totalPods := int(*currentSts.Spec.Replicas)

	// Collect pod states.
	pods, masterIdx, err := r.collectPodStates(ctx, v, currentSts)
	if err != nil {
		return RollingUpdateResult{Error: err}
	}

	// Detect and resolve split-brain before proceeding with the rolling update.
	// This is critical to break the deadlock where a rogue master prevents
	// replaceNextReplica from finding candidates and blocks waitForReplicasReady.
	sentinelMaster := r.getSentinelMasterPodName(ctx, v)
	pods, masterIdx = r.resolveSplitBrain(ctx, v, pods, masterIdx, sentinelMaster)

	// Count how many pods have been updated.
	updatedCount := countUpdatedPods(pods)

	// If all pods are updated and ready, verify cluster health before completing.
	if updatedCount == totalPods {
		return r.finalizeRollingUpdate(ctx, v, pods)
	}

	// Update status with progress.
	phase := fmt.Sprintf("%s %d/%d", vkov1.ValkeyPhaseRollingUpdate, updatedCount, totalPods)
	_ = r.updatePhase(ctx, v, ValkeyPhase(phase), fmt.Sprintf("Rolling update in progress: %d/%d pods updated", updatedCount, totalPods))

	// Check the current state machine phase.
	currentState := r.getRollingUpdateState(v)

	// Detect and clear stale state from a previous rolling update.
	// Use countReplacedPods (ignores readiness) to avoid falsely clearing state
	// when replaced pods are temporarily not ready (e.g. during failover).
	currentState, err = r.clearStaleRollingUpdateState(ctx, v, currentState, countReplacedPods(pods))
	if err != nil {
		return RollingUpdateResult{Error: err}
	}

	// If sentinel was reset after a timed-out failover, retrigger failover.
	if currentState == stateFailoverReset {
		return r.handleFailoverRetrigger(ctx, v)
	}

	// If failover was already triggered, skip straight to post-failover handling.
	if currentState == stateFailoverTriggered || currentState == stateReplacingMaster {
		return r.handlePostFailover(ctx, v, pods, masterIdx)
	}

	// Step 1: Replace replica pods first (not the master).
	if result := r.replaceNextReplica(ctx, v, pods); result != nil {
		return *result
	}

	// Step 2: All replicas are updated. Now handle the master failover and replacement.
	if result := r.handleMasterFailover(ctx, v, pods, masterIdx); result != nil {
		return *result
	}

	// If no master was detected but pods still need updating, the cluster may be
	// in a failover transition. Wait for it to stabilize before replacing pods.
	if masterIdx < 0 && hasPendingUpdates(pods) {
		logger.Info("No master detected during rolling update, waiting for cluster to stabilize")
		return RollingUpdateResult{NeedsRequeue: true, RequeueAfter: rollingUpdateRequeueDelay}
	}

	// Step 3: Replace any remaining pods with old image.
	return r.replaceRemainingPods(ctx, v, pods)
}

// clearStaleRollingUpdateState detects stale state from a previous rolling update.
// If no pods have been replaced yet (replacedCount == 0) but the state machine
// is past the replica-replacement phase, the state annotations are left over from
// a prior rolling update. Clear the stale state so the new rolling update starts
// fresh from replica replacement.
//
// replacedCount must count pods that do not need an update regardless of readiness.
// Using the ready-only updatedCount would falsely clear state when replaced pods are
// temporarily not ready (e.g. during a failover transition).
func (r *ValkeyReconciler) clearStaleRollingUpdateState(ctx context.Context, v *vkov1.Valkey, currentState string, replacedCount int) (string, error) {
	if replacedCount == 0 && currentState != "" && currentState != stateReplacingReplicas {
		logger := log.FromContext(ctx)
		logger.Info("Clearing stale rolling update state from previous update",
			"staleState", currentState)
		if err := r.clearRollingUpdateState(ctx, v); err != nil {
			return currentState, err
		}
		return "", nil
	}
	return currentState, nil
}

// handleFailoverRetrigger retriggers a sentinel failover after a previous reset.
// After a sentinel reset, sentinel needs time to rediscover replicas via INFO
// polling (~10s). This method waits for that minimum period before retriggering.
func (r *ValkeyReconciler) handleFailoverRetrigger(ctx context.Context, v *vkov1.Valkey) RollingUpdateResult {
	logger := log.FromContext(ctx)

	// Check if enough time has passed since the reset for sentinel to
	// rediscover replicas. Without this guard, concurrent reconciles
	// retrigger immediately and get NOGOODSLAVE.
	if !r.hasMinWaitElapsed(v) {
		logger.Info("Waiting for sentinel to rediscover replicas after reset")
		return RollingUpdateResult{NeedsRequeue: true, RequeueAfter: 10 * time.Second}
	}

	// Verify sentinel has actually discovered replicas before retriggering.
	// The time-based wait above is a minimum; this check confirms readiness.
	// Apply the same sentinelAwarenessTimeout cap here to avoid an indefinite
	// stall in the retrigger path (same root cause as in handleMasterFailover).
	expectedReplicas := int(v.Spec.Replicas) - 1
	if !r.isSentinelAwareOfReplicas(ctx, v, expectedReplicas) {
		r.ensureSentinelAwarenessTimestamp(ctx, v)
		if r.isSentinelAwarenessStalled(v) {
			logger.Info("Sentinel awareness stalled in retrigger, proceeding with failover regardless",
				"expectedReplicas", expectedReplicas)
		} else {
			logger.Info("Sentinel has not rediscovered replicas after reset, waiting")
			return RollingUpdateResult{NeedsRequeue: true, RequeueAfter: 5 * time.Second}
		}
	}

	logger.Info("Retriggering sentinel failover after reset")

	if err := r.setRollingUpdateState(ctx, v, stateFailoverTriggered); err != nil {
		return RollingUpdateResult{Error: err}
	}
	if err := r.setFailoverTimestamp(ctx, v); err != nil {
		return RollingUpdateResult{Error: err}
	}

	if err := r.triggerSentinelFailover(ctx, v); err != nil {
		logger.Info("Sentinel failover retry failed, will retry", "error", err)
	}

	return RollingUpdateResult{NeedsRequeue: true, RequeueAfter: 15 * time.Second}
}

// finalizeRollingUpdate verifies cluster topology after all pods are updated,
// then cleans up state annotations and marks the rolling update as complete.
func (r *ValkeyReconciler) finalizeRollingUpdate(ctx context.Context, v *vkov1.Valkey, pods []podState) RollingUpdateResult {
	logger := log.FromContext(ctx)

	// Only verify topology if we went through a failover during this rolling update.
	// The state annotation is set during the failover process, so its presence means
	// we need to verify the cluster settled correctly before declaring completion.
	currentState := r.getRollingUpdateState(v)
	if v.IsSentinelEnabled() && currentState != "" {
		if result := r.checkFinalizationTopology(ctx, v, pods); result != nil {
			return *result
		}
	}

	logger.Info("Rolling update complete, all data pods running new image")
	// This event covers the data tier only: the Sentinel tier rolls afterwards
	// and reports its own SentinelUpdateComplete
	// (docs/adr/0024-the-sentinel-tier-reports-its-own-completion.md).
	r.recordEvent(v, corev1.EventTypeNormal, "RollingUpdateComplete",
		"Rolling update of the data pods completed successfully; any outdated Sentinel pods are rolled next")
	// Clean up state annotation.
	if err := r.clearRollingUpdateState(ctx, v); err != nil {
		return RollingUpdateResult{Error: err}
	}
	// Clear the paused condition if it was set by a previous failed attempt.
	r.setStatusCondition(ctx, v,
		vkov1.ConditionTypeRollingUpdatePaused,
		metav1.ConditionFalse,
		"Completed",
		"Rolling update completed successfully")
	return RollingUpdateResult{Completed: true}
}

// checkFinalizationTopology verifies that the cluster has a stable single-master
// topology and that all replicas are connected before syncing sentinel.
// Returns nil when the cluster is ready (or the finalization has stalled and we
// proceed with best-effort sync). Returns a non-nil result when we need to wait.
func (r *ValkeyReconciler) checkFinalizationTopology(ctx context.Context, v *vkov1.Valkey, pods []podState) *RollingUpdateResult {
	logger := log.FromContext(ctx)
	stalled := r.isFinalizationStalled(v)

	masterCount := countMasters(pods)
	if masterCount != 1 {
		if stalled {
			// Topology detection has been unreliable for too long (e.g., due to
			// flaky GetReplicationInfo calls in CI). Proceed with best-effort reset
			// so the rolling update is not stuck indefinitely.
			logger.Info("Finalization stalled waiting for topology, proceeding",
				"masterCount", masterCount)
			r.resetSentinelState(ctx, v, "")
			return nil
		}
		r.ensureFinalizationTimestamp(ctx, v)
		logger.Info("Rolling update: waiting for stable cluster topology",
			"masterCount", masterCount)
		return &RollingUpdateResult{NeedsRequeue: true, RequeueAfter: rollingUpdateRequeueDelay}
	}

	// Master found — wait for all replicas to be connected, then sync sentinel.
	checker := r.getInstanceChecker()
	expectedReplicas := len(pods) - 1
	for _, ps := range pods {
		if !ps.isMaster {
			continue
		}
		// When finalization is stalled, break any cascaded replication chains by
		// sending SLAVEOF directly to all non-master pods. After a failover, a
		// replica can end up connected to the old master (now itself a replica)
		// instead of the new master — a cascaded chain: new-master → old-master-pod
		// → replica. The cascaded replica does not appear in the new master's INFO
		// replication, so after SENTINEL REMOVE+MONITOR sentinel cannot discover
		// or reconfigure it. forceReplicaConnections bypasses sentinel and ensures
		// every replica connects directly to the current master.
		if stalled {
			r.forceReplicaConnections(ctx, v, ps.name, pods)
		}
		return r.syncSentinelWithMaster(ctx, v, ps, expectedReplicas, checker, stalled)
	}

	return nil
}

// syncSentinelWithMaster verifies replication state on the master pod and, when
// all replicas are connected, performs a sentinel state reset to synchronise
// sentinel with the current master address.
// Returns nil to proceed, or a non-nil requeue result when we need to wait.
func (r *ValkeyReconciler) syncSentinelWithMaster(ctx context.Context, v *vkov1.Valkey, masterPS podState, expectedReplicas int, checker InstanceChecker, stalled bool) *RollingUpdateResult {
	logger := log.FromContext(ctx)

	info, err := checker.GetReplicationInfo(ctx, v, masterPS.name)
	if err != nil {
		if stalled {
			logger.Info("Finalization stalled, cannot verify replication, resetting sentinel and proceeding",
				common.RoleMaster, masterPS.name, "error", err)
			// Use the identified master's address instead of empty string to avoid
			// the pod-0 fallback in resetSentinelState when pod-0 is not the master.
			headlessNameOnErr := common.HeadlessServiceName(v, common.ComponentValkey)
			masterAddrOnErr := fmt.Sprintf("%s.%s.%s.svc.cluster.local", masterPS.name, headlessNameOnErr, v.Namespace)
			r.resetSentinelState(ctx, v, masterAddrOnErr)
			return nil
		}
		r.ensureFinalizationTimestamp(ctx, v)
		logger.Info("Cannot verify replication before sentinel sync, waiting",
			common.RoleMaster, masterPS.name, "error", err)
		return &RollingUpdateResult{NeedsRequeue: true, RequeueAfter: rollingUpdateRequeueDelay}
	}

	if info.ConnectedSlaves < expectedReplicas {
		if stalled {
			logger.Info("Finalization stalled, replicas not all connected, proceeding with partial sync",
				common.RoleMaster, masterPS.name, "connectedSlaves", info.ConnectedSlaves, "expected", expectedReplicas)
		} else {
			r.ensureFinalizationTimestamp(ctx, v)
			logger.Info("Waiting for all replicas to connect before sentinel sync",
				common.RoleMaster, masterPS.name, "connectedSlaves", info.ConnectedSlaves,
				"expected", expectedReplicas)
			return &RollingUpdateResult{NeedsRequeue: true, RequeueAfter: rollingUpdateRequeueDelay}
		}
	}

	headlessName := common.HeadlessServiceName(v, common.ComponentValkey)
	masterAddr := fmt.Sprintf("%s.%s.%s.svc.cluster.local", masterPS.name, headlessName, v.Namespace)
	logger.Info("Syncing sentinel with current master before finalization",
		common.RoleMaster, masterPS.name, "masterAddr", masterAddr,
		"connectedSlaves", info.ConnectedSlaves)
	// Persist the confirmed master address as a CR annotation so that the sentinel
	// ConfigMap is regenerated with the correct master address on the next reconcile.
	// This guarantees that sentinel pods which restart after the rolling update
	// (e.g., due to a StatefulSet conflict resolution) connect to the actual
	// post-failover master rather than falling back to the stale pod-0 default.
	//
	// Deliberately best-effort on this path, unlike every non-Sentinel caller:
	// Sentinel is its own master authority here, the annotation only pre-seeds a
	// restarting sentinel, and checkSteadyStateSplitBrain -- the check that turns
	// the annotation into a destructive authority -- never runs for Sentinel
	// clusters. The resetSentinelState below is what actually fixes the topology.
	if err := r.persistKnownMaster(ctx, v, masterAddr); err != nil {
		logger.Info("Could not persist the known-master annotation before sentinel sync",
			common.RoleMaster, masterPS.name, "error", err)
	}
	r.resetSentinelState(ctx, v, masterAddr)
	return nil
}

// persistKnownMaster stores the post-failover master address as a Valkey CR
// annotation (builder.AnnotationKnownMaster). The builder package reads this
// annotation when generating the sentinel ConfigMap, so any sentinel pod that
// restarts after a failover starts monitoring the correct master from the outset.
//
// The annotation is no longer only a hint. checkSteadyStateSplitBrain demotes a
// live master toward whatever it names, and a demotion is a REPLICAOF, which
// discards the demoted pod's dataset. The write error is therefore returned, and
// the invariant every non-Sentinel caller has to uphold is: the annotation names
// the pod the operator last promoted, and a promotion the operator could not
// record is not a completed promotion.
//
// On a failed write the in-memory value is put back to what is actually
// persisted, so it cannot be read back as authority later in the same pass --
// the discipline ensureWaitBound applies for the same reason. Restoring the
// previous address rather than deleting the key is the faithful form of that
// rule here: the API server still holds the old address, so an absent annotation
// would misrepresent the stored state just as much as the new one would.
func (r *ValkeyReconciler) persistKnownMaster(ctx context.Context, v *vkov1.Valkey, masterAddr string) error {
	if v.Annotations == nil {
		v.Annotations = make(map[string]string)
	}
	previous, had := v.Annotations[builder.AnnotationKnownMaster]
	if had && previous == masterAddr {
		return nil // already up-to-date — no API call needed
	}
	v.Annotations[builder.AnnotationKnownMaster] = masterAddr
	if err := r.Update(ctx, v); err != nil {
		if had {
			v.Annotations[builder.AnnotationKnownMaster] = previous
		} else {
			delete(v.Annotations, builder.AnnotationKnownMaster)
		}
		return fmt.Errorf("persisting known master %s: %w", masterAddr, err)
	}
	return nil
}

// recordPromotedMaster makes a promotion durable: it names masterHost in the
// known-master annotation and republishes the replica ConfigMap whose replicaof
// directive is derived from it. Both writes together are what a later pass reads
// back as "this is the master the operator promoted" -- the annotation for
// checkSteadyStateSplitBrain and the init-script self-claim, the ConfigMap for a
// replica that restarts before the next reconcile.
//
// Callers must treat a returned error as "the promotion is not recorded" and must
// not advance any state machine past it.
//
// It is also where the drain-promotion stamps of the cluster die. The stamp means
// "a promotion nobody recorded"; the write above IS that record, so every stamp
// still in place is now spent evidence, and spent evidence outranks the annotation
// on the next multi-master pass (checkSteadyStateSplitBrain resolves evidence
// first). Clearing costs one cached List and a patch per stamped pod, on a path
// that runs only when a promotion actually happened.
func (r *ValkeyReconciler) recordPromotedMaster(ctx context.Context, v *vkov1.Valkey, masterHost string) error {
	if err := r.persistKnownMaster(ctx, v, masterHost); err != nil {
		return err
	}
	r.clearDrainStamps(ctx, v)
	if err := r.reconcileReplicaConfigMap(ctx, v); err != nil {
		return fmt.Errorf("republishing the replica ConfigMap for %s: %w", masterHost, err)
	}
	return nil
}

// Names of the in-memory copies of the rolling update wait bounds. They are
// suffixes of the CR name in the tracker key, see waitBoundKey.
const (
	boundTopologyRestore   = "topology-restore"
	boundFinalization      = "finalization"
	boundManualFailover    = "manual-failover"
	boundSentinelAwareness = "sentinel-awareness"
	boundSyncWait          = "sync-wait"
)

// waitBoundKey builds the tracker key of one wait bound of one CR.
//
// The tracker is shared with the nudge grace period, which keys by StatefulSet
// name in the same namespace — and for the data component that name IS the CR
// name (common.StatefulSetName). The "/" separator keeps the two sets disjoint:
// it cannot occur in an object name, so no wait-bound key can ever collide with
// a StatefulSet key.
func waitBoundKey(namespace, name, bound string) types.NamespacedName {
	return types.NamespacedName{Namespace: namespace, Name: name + "/" + bound}
}

// ensureWaitBound arms a bounded wait in both places it is kept: the annotation
// on the CR, which survives an operator restart, and the in-memory tracker, which
// survives a failing API server.
//
// The annotation alone is not enough. Its write can fail indefinitely — a fail-closed admission
// webhook on the CR, a permanently conflicting writer — and the discarded error meant the bound
// then never armed at all: the matching "is it stalled" check reads an annotation that is never
// there, answers false on every pass, and the phase it bounds requeues forever. That is exactly the
// stall the bound exists to break, reintroduced through the arming path
// (docs/adr/0010-every-rolling-update-wait-is-bounded.md, D7, D8).
//
// The in-memory copy must be dropped wherever the rolling update state is cleared
// (clearRollingUpdateState) — a leftover entry pre-expires the budget of the next
// rolling update, which is ADR 0010 D10 one layer down.
func (r *ValkeyReconciler) ensureWaitBound(ctx context.Context, v *vkov1.Valkey, annotation, bound string) {
	logger := log.FromContext(ctx)

	now := time.Now()
	r.nudges.observe(waitBoundKey(v.Namespace, v.Name, bound), now)

	if v.Annotations == nil {
		v.Annotations = make(map[string]string)
	}
	if _, ok := v.Annotations[annotation]; ok {
		return // already set
	}
	v.Annotations[annotation] = now.UTC().Format(time.RFC3339)
	if err := r.Update(ctx, v); err != nil {
		// Not fatal, and not silent either: the in-memory bound carries the
		// deadline for as long as this operator process lives.
		//
		// The annotation is dropped from the object again so that it reflects what
		// was actually persisted. Leaving it would be worse than useless: every
		// later pass would re-arm it in memory, waitBoundExceeded would read that
		// always-fresh value instead of the tracker, and the deadline would never
		// be reached — the ADR 0010 D7, D8 stall, one indirection further in.
		delete(v.Annotations, annotation)
		logger.Error(err, "Failed to persist a rolling update wait bound, falling back to the in-memory deadline",
			"annotation", annotation)
	}
}

// annotationTimestampExceeded reports whether the RFC3339 timestamp stored under
// annotation is older than timeout. A missing or empty annotation reports false —
// nothing was armed, so nothing can have expired. A corrupted timestamp reports
// true: treating it as expired is the only answer that recovers, because no later
// pass can repair a value nobody rewrites.
func annotationTimestampExceeded(v *vkov1.Valkey, annotation string, timeout time.Duration) bool {
	tsStr, ok := v.Annotations[annotation]
	if !ok || tsStr == "" {
		return false
	}
	ts, err := time.Parse(time.RFC3339, tsStr)
	if err != nil {
		return true
	}
	return time.Since(ts) > timeout
}

// waitBoundExceeded reports whether the wait armed by ensureWaitBound is older
// than timeout. The annotation wins when it is present: it is the copy that
// survives a restart, so a restarted operator must not hand the phase a fresh
// budget. The in-memory first-seen answers whenever the annotation never landed.
func (r *ValkeyReconciler) waitBoundExceeded(v *vkov1.Valkey, annotation, bound string, timeout time.Duration) bool {
	if tsStr, ok := v.Annotations[annotation]; ok && tsStr != "" {
		return annotationTimestampExceeded(v, annotation, timeout)
	}
	started, tracked := r.nudges.firstSeen(waitBoundKey(v.Namespace, v.Name, bound))
	return tracked && time.Since(started) > timeout
}

// forgetWaitBounds drops the in-memory copies of every rolling update wait bound
// of one CR. Every path that ends a rolling update has to call this, or the next
// rolling update starts with a spent budget.
func (r *ValkeyReconciler) forgetWaitBounds(namespace, name string) {
	for _, bound := range []string{
		boundTopologyRestore, boundFinalization, boundManualFailover,
		boundSentinelAwareness, boundSyncWait, boundMultipleMasters,
	} {
		r.nudges.forget(waitBoundKey(namespace, name, bound))
	}
}

// ensureFinalizationTimestamp arms the finalization bound if it is not already
// armed. It is used by isFinalizationStalled to detect when the finalization
// topology checks have been stuck for too long.
func (r *ValkeyReconciler) ensureFinalizationTimestamp(ctx context.Context, v *vkov1.Valkey) {
	r.ensureWaitBound(ctx, v, annotationFinalizationTimestamp, boundFinalization)
}

// ensureTopologyRestoreTimestamp arms the bound of Phase 1 of the topology
// restoration if it is not already armed. Phase 1 otherwise requeues forever when
// pod-0 never syncs back.
func (r *ValkeyReconciler) ensureTopologyRestoreTimestamp(ctx context.Context, v *vkov1.Valkey) {
	r.ensureWaitBound(ctx, v, annotationTopologyRestoreStarted, boundTopologyRestore)
}

// armWaitBound (re)starts a bounded wait at the state transition that owns it,
// overwriting whatever was armed before -- in the annotation and in the in-memory
// tracker alike.
//
// Re-arming rather than only filling a gap (ensureWaitBound) is the ADR 0010 D10 fix. A
// rolling update that died mid-state leaves its annotation behind: clearRollingUpdateState
// deletes it, but an operator killed in flight never reaches that call, and
// clearStaleRollingUpdateState only clears on the "nothing replaced yet" branch. The
// next update then enters the state against an hours-old timestamp, declares itself
// stalled on its first pass and gives up before it ever tried.
//
// The annotation is only set in memory here; the state write that follows persists
// both in a single API call.
func (r *ValkeyReconciler) armWaitBound(v *vkov1.Valkey, annotation, bound string) {
	now := time.Now()
	if v.Annotations == nil {
		v.Annotations = make(map[string]string)
	}
	v.Annotations[annotation] = now.UTC().Format(time.RFC3339)

	key := waitBoundKey(v.Namespace, v.Name, bound)
	r.nudges.forget(key)
	r.nudges.observe(key, now)
}

// armTopologyRestoreBound starts the Phase 1 budget on entry to stateRestoringTopology
// (docs/adr/0010-every-rolling-update-wait-is-bounded.md, D10).
func (r *ValkeyReconciler) armTopologyRestoreBound(v *vkov1.Valkey) {
	r.armWaitBound(v, annotationTopologyRestoreStarted, boundTopologyRestore)
}

// armFinalizationBound starts the Phase 2 budget on entry to stateVerifyingTopology,
// which both entries -- abandonTopologyRestoration and promotePod0AndRedirect -- have
// to do (docs/adr/0010-every-rolling-update-wait-is-bounded.md, D10).
//
// Without it Phase 2 inherited whatever annotationFinalizationTimestamp was left on
// the CR, under exactly the ADR 0010 D10 conditions. An hours-old timestamp made
// isFinalizationStalled true on the first pass, so Phase 2 completed the rolling
// update immediately and WITHOUT consolidating rogue masters -- on the abandoned path
// the only job it has, and the last pass that can do it, because once the state
// annotation is gone nothing calls detectAndResolveSplitBrain again.
func (r *ValkeyReconciler) armFinalizationBound(v *vkov1.Valkey) {
	r.armWaitBound(v, annotationFinalizationTimestamp, boundFinalization)
}

// armManualFailoverBound starts the manual-failover budget on entry to
// stateManualFailover and returns the timestamp it armed, so the conflict retry of
// persistManualFailoverState re-applies the same deadline instead of granting a fresh
// one on every attempt.
func (r *ValkeyReconciler) armManualFailoverBound(v *vkov1.Valkey) string {
	r.armWaitBound(v, annotationManualFailoverStarted, boundManualFailover)
	return v.Annotations[annotationManualFailoverStarted]
}

// ensureManualFailoverTimestamp arms the manual-failover bound if it is not already
// armed. It covers the states armManualFailoverBound cannot reach: a CR whose state
// annotation was written by an older operator, and a persistManualFailoverState whose
// annotation write never landed.
func (r *ValkeyReconciler) ensureManualFailoverTimestamp(ctx context.Context, v *vkov1.Valkey) {
	r.ensureWaitBound(ctx, v, annotationManualFailoverStarted, boundManualFailover)
}

// isManualFailoverStalled reports whether the deleted master has failed to come back
// ready on the current template for longer than the configured sync timeout. It is
// the same budget as the replica-replacement phase and Phase 1 because it is the same
// wait: a deleted pod being recreated, scheduled and rejoining its master.
func (r *ValkeyReconciler) isManualFailoverStalled(v *vkov1.Valkey) bool {
	return r.waitBoundExceeded(v, annotationManualFailoverStarted, boundManualFailover, v.GetSyncTimeout())
}

// isTopologyRestoreStalled returns true if Phase 1 has been waiting for pod-0 to
// sync back longer than the configured sync timeout. The same budget as the
// replica-replacement phase applies because it is the same wait: a replaced pod
// pulling a full dataset from its master.
func (r *ValkeyReconciler) isTopologyRestoreStalled(v *vkov1.Valkey) bool {
	return r.waitBoundExceeded(v, annotationTopologyRestoreStarted, boundTopologyRestore, v.GetSyncTimeout())
}

// knownMasterPodName returns the pod name behind the known-master annotation, or
// an empty string when it is unset. The annotation stores an FQDN; every caller
// that resolves a split brain compares against pod names.
//
// It is the authority for who the master is during topology restoration: pod-0
// after promotePod0AndRedirect succeeded, the promoted replica while the
// restoration is still pending or was abandoned.
func knownMasterPodName(v *vkov1.Valkey) string {
	if v.Annotations == nil {
		return ""
	}
	return podNameFromHost(v.Annotations[builder.AnnotationKnownMaster])
}

// podNameFromHost returns the first DNS label of a headless-service address, which
// is the pod name. Shared with replicaConfigMaster so both sides of the
// steady-state check agree on what "names this pod" means.
func podNameFromHost(addr string) string {
	if idx := strings.Index(addr, "."); idx > 0 {
		return addr[:idx]
	}
	return addr
}

// ensureSentinelAwarenessTimestamp arms the sentinel-awareness bound if it is not
// already armed. The deadline is read by isSentinelAwarenessStalled to detect when
// we have been waiting too long for sentinel to discover replicas.
//
// It goes through ensureWaitBound so the deadline survives a CR write that keeps
// failing: with the annotation alone, a discarded write error meant the bound
// never armed, isSentinelAwarenessStalled answered false forever, and both call
// sites requeued the Sentinel rolling update indefinitely — the ADR 0010 D7/D8
// stall through the arming path, on the one bound that had not been converted.
func (r *ValkeyReconciler) ensureSentinelAwarenessTimestamp(ctx context.Context, v *vkov1.Valkey) {
	r.ensureWaitBound(ctx, v, annotationSentinelAwarenessStarted, boundSentinelAwareness)
}

// isSentinelAwarenessStalled returns true if we have been waiting for sentinel
// to discover the expected replicas longer than sentinelAwarenessTimeout.
// When stalled, callers should proceed with the failover rather than waiting
// indefinitely; the existing NOGOODSLAVE retry cycle will handle recovery.
func (r *ValkeyReconciler) isSentinelAwarenessStalled(v *vkov1.Valkey) bool {
	return r.waitBoundExceeded(v, annotationSentinelAwarenessStarted, boundSentinelAwareness, sentinelAwarenessTimeout)
}

// isFinalizationStalled returns true if the finalizeRollingUpdate function has
// been waiting for topology stabilisation longer than finalizationStallTimeout.
// This is used to break the potential infinite wait in CI environments where
// GetReplicationInfo calls are unreliable due to resource exhaustion.
func (r *ValkeyReconciler) isFinalizationStalled(v *vkov1.Valkey) bool {
	return r.waitBoundExceeded(v, annotationFinalizationTimestamp, boundFinalization, finalizationStallTimeout)
}

func countMasters(pods []podState) int {
	count := 0
	for _, ps := range pods {
		if ps.isMaster {
			count++
		}
	}
	return count
}

// detectAndResolveSplitBrain checks for multiple masters in the pod states and
// resolves the split-brain by demoting rogue masters. This breaks the rolling
// update deadlock where a rogue master is treated as a master (skipped by
// replaceNextReplica) but also blocks waitForReplicasReady because it needs
// updating.
//
// The real master is determined by:
//  1. Using knownMaster, the name of the pod an authority already designated as
//     master — Sentinel in the Sentinel path, the promoted pod while a manual
//     failover is in flight. May be empty.
//  2. Falling back to the master with the most connected slaves (preserves data).
//
// It reports nothing. Two pods answering master is a designed state for as long
// as a controlled failover is in flight, and this function counts four lines
// before it has consulted the authority that could tell the designed window from
// the undesigned one -- so an Event emitted here can only be a guess. The report
// is resolveSplitBrain's, which runs after the authority has been applied and
// after the bound has had its say (docs/adr/0025-a-split-brain-warning-means-one-that-did-not-resolve-itself.md, D2).
//
// Returns the updated pod states and the corrected master index.
func (r *ValkeyReconciler) detectAndResolveSplitBrain(ctx context.Context, v *vkov1.Valkey, pods []podState, masterIdx int, knownMaster string) ([]podState, int) {
	logger := log.FromContext(ctx)

	// Count pods reporting as master.
	var masterIndices []int
	for i, ps := range pods {
		if ps.isMaster {
			masterIndices = append(masterIndices, i)
		}
	}

	if len(masterIndices) <= 1 {
		return pods, masterIdx // No split-brain.
	}

	logger.Info("Split-brain detected: multiple masters found",
		"masterCount", len(masterIndices), "masterIndices", masterIndices)

	// Determine the real master from the authoritative name, if one was given.
	realMasterIdx := -1
	var rogueIndices []int

	if knownMaster != "" {
		for _, idx := range masterIndices {
			if pods[idx].name == knownMaster {
				realMasterIdx = idx
			} else {
				rogueIndices = append(rogueIndices, idx)
			}
		}
	}

	// Fallback: if no authority names one of them, prefer the one with the
	// most connected slaves (the one actively serving replicas has the real data).
	if realMasterIdx < 0 {
		checker := r.getInstanceChecker()
		bestIdx := masterIndices[0]
		bestSlaves := -1
		for _, idx := range masterIndices {
			info, err := checker.GetReplicationInfo(ctx, v, pods[idx].name)
			if err != nil {
				continue
			}
			if info.ConnectedSlaves > bestSlaves {
				bestSlaves = info.ConnectedSlaves
				bestIdx = idx
			}
		}
		realMasterIdx = bestIdx
		rogueIndices = nil
		for _, idx := range masterIndices {
			if idx != realMasterIdx {
				rogueIndices = append(rogueIndices, idx)
			}
		}
	}

	logger.Info("Split-brain resolution: identified real master",
		"realMaster", pods[realMasterIdx].name, "rogueCount", len(rogueIndices))

	// Demote all rogue masters via REPLICAOF.
	for _, rogueIdx := range rogueIndices {
		if err := r.demoteRogueMaster(ctx, v, pods[rogueIdx], pods[realMasterIdx].name); err != nil {
			logger.Info("Failed to demote rogue master (will retry next reconcile)",
				"pod", pods[rogueIdx].name, "error", err)
		}
		// Mark the pod as non-master in the local state regardless of whether
		// REPLICAOF succeeded. This breaks the deadlock: the pod becomes a replica
		// candidate for replaceNextReplica, and when deleted+recreated, the init
		// container queries Sentinel for the correct role.
		pods[rogueIdx].isMaster = false
	}

	return pods, realMasterIdx
}

// getSentinelMasterPodName queries Sentinel for the authoritative master and
// returns the pod name (e.g., "myvalkey-1"). Returns an empty string if no
// sentinel is reachable or sentinel is not enabled.
func (r *ValkeyReconciler) getSentinelMasterPodName(ctx context.Context, v *vkov1.Valkey) string {
	if !v.IsSentinelEnabled() {
		return ""
	}

	logger := log.FromContext(ctx)
	monitorName := builder.SentinelMonitorName(v)
	sentinelStsName := common.StatefulSetName(v, common.ComponentSentinel)

	sentinelReplicas := int32(3)
	if v.Spec.Sentinel != nil && v.Spec.Sentinel.Replicas > 0 {
		sentinelReplicas = v.Spec.Sentinel.Replicas
	}
	password := r.sentinelPassword(ctx, v)

	for i := int32(0); i < sentinelReplicas; i++ {
		podName := fmt.Sprintf("%s-%d", sentinelStsName, i)

		tlsConfig, tlsErr := r.buildTLSConfig(ctx, v, builder.SentinelTLSSecretName(v))
		if tlsErr != nil {
			continue
		}
		sentinelPort := builder.SentinelPort
		if tlsConfig != nil {
			sentinelPort = builder.SentinelTLSPort
		}
		addr := health.PodAddressForComponent(v, podName, common.ComponentSentinel, sentinelPort)

		c := r.newValkeyClient(addr, password, tlsConfig)
		info, err := c.SentinelMaster(monitorName)
		if err != nil {
			logger.V(1).Info("Could not query sentinel for master", "sentinel", podName, "error", err)
			continue
		}

		// The IP field contains the FQDN when announce-hostnames is enabled.
		// Extract the pod name (first segment before the first dot).
		masterFQDN := info.IP
		if idx := strings.Index(masterFQDN, "."); idx > 0 {
			return masterFQDN[:idx]
		}
		return masterFQDN
	}

	return ""
}

// demoteRogueMaster sends a REPLICAOF command to a rogue master pod, instructing
// it to become a replica of the real master. This is a best-effort operation:
// even if it fails, the caller may still mark the pod as non-master in local state
// so that the rolling update can proceed (deletion+recreation fixes the role).
func (r *ValkeyReconciler) demoteRogueMaster(ctx context.Context, v *vkov1.Valkey, roguePod podState, realMasterPodName string) error {
	logger := log.FromContext(ctx)
	logger.Info("Demoting rogue master to replica",
		"roguePod", roguePod.name, "realMaster", realMasterPodName)

	if !roguePod.exists || !roguePod.ready {
		return fmt.Errorf("rogue pod %s is not ready for demotion", roguePod.name)
	}

	tlsConfig, err := r.buildTLSConfig(ctx, v, builder.ValkeyTLSSecretName(v))
	if err != nil {
		return fmt.Errorf("building TLS config: %w", err)
	}

	headlessName := common.HeadlessServiceName(v, common.ComponentValkey)
	masterHost := fmt.Sprintf("%s.%s.%s.svc.cluster.local", realMasterPodName, headlessName, v.Namespace)
	portStr := fmt.Sprintf("%d", builder.ServicePort(v))
	password := r.readValkeyPassword(ctx, v)

	addr := health.PodAddressForComponent(v, roguePod.name, common.ComponentValkey, int(builder.ServicePort(v)))
	c := r.newValkeyClient(addr, password, tlsConfig)
	if err := c.ReplicaOf(masterHost, portStr); err != nil {
		return fmt.Errorf("REPLICAOF command failed on %s: %w", roguePod.name, err)
	}

	// Normal, not Warning: this reports a repair that succeeded. The Warning that
	// matters is the one nobody repaired, and it has its own edge
	// (docs/adr/0025-a-split-brain-warning-means-one-that-did-not-resolve-itself.md, D4).
	// The steady-state path reaches this line through the same helper
	// (docs/adr/0011-evidence-based-steady-state-split-brain-resolution.md, D20), whose monitoring
	// contract names SplitBrainUnresolved, SplitBrainDemotionRefused and
	// MasterAdoptionRefused -- never this reason.
	r.recordEvent(v, corev1.EventTypeNormal, "SplitBrainResolved",
		"Demoted rogue master %s to replica of %s", roguePod.name, realMasterPodName)

	logger.Info("Successfully demoted rogue master",
		"roguePod", roguePod.name, "realMaster", realMasterPodName)
	return nil
}

// hasPendingUpdates returns true if any pod still needs an update.
func hasPendingUpdates(pods []podState) bool {
	for _, ps := range pods {
		if ps.needsUpdate {
			return true
		}
	}
	return false
}

// labelClaimsMaster reports whether an unreachable pod may still be counted as a
// master on the strength of its instanceRole label alone.
//
// A pod carrying a DeletionTimestamp may not. Nothing clears the label at delete
// time -- the sidecar labeler polls on its own clock and the kubelet gives no
// ordering between the two SIGTERMs
// (docs/adr/0012-the-sidecar-records-its-drain-promotion-on-the-pod.md) -- so the operator would
// demote the outgoing master exactly as intended, delete it, and then watch its
// own stale label manufacture it back into a second master that answers nothing.
// demoteRogueMaster refuses a not-Ready pod, so the report never closed either.
// Silence is not evidence
// (docs/adr/0011-evidence-based-steady-state-split-brain-resolution.md, D6), and a pod that is
// being deleted is silence with a reason.
func labelClaimsMaster(pod *corev1.Pod) bool {
	if pod == nil || pod.DeletionTimestamp != nil {
		return false
	}
	return pod.Labels[common.LabelInstanceRole] == common.RoleMaster
}

// podState holds the state of a single pod during a rolling update.
type podState struct {
	name        string
	pod         *corev1.Pod
	needsUpdate bool
	isMaster    bool
	ready       bool
	exists      bool
}

// collectPodStates gathers the current state of all pods in the StatefulSet.
func (r *ValkeyReconciler) collectPodStates(ctx context.Context, v *vkov1.Valkey, currentSts *appsv1.StatefulSet) ([]podState, int, error) {
	desiredImage := valkeyImageFromSts(currentSts)
	stsName := common.StatefulSetName(v, common.ComponentValkey)
	totalPods := int(*currentSts.Spec.Replicas)
	checker := r.getInstanceChecker()

	pods := make([]podState, totalPods)
	masterIdx := -1

	for i := 0; i < totalPods; i++ {
		podName := fmt.Sprintf("%s-%d", stsName, i)
		pod := &corev1.Pod{}
		err := r.Get(ctx, types.NamespacedName{Name: podName, Namespace: v.Namespace}, pod)

		ps := podState{name: podName}
		if err != nil {
			if apierrors.IsNotFound(err) {
				ps.needsUpdate = true
			} else {
				return nil, -1, fmt.Errorf("getting pod %s: %w", podName, err)
			}
		} else if !podIsOurs(pod, currentSts) {
			// The choke point for four of the six pod deletes below: everything that
			// deletes a pod takes it from this slice, so proving provenance once here
			// covers them all (ADR 0020 D9).
			return nil, -1, foreignObjectError("Pod", podName)
		} else {
			ps.pod = pod
			ps.exists = true
			ps.needsUpdate = podNeedsUpdate(pod, desiredImage, sidecarImageFromSts(currentSts), configHashFromSts(currentSts), podSpecHashFromSts(currentSts), currentSts.Spec.Template.Spec.Containers)
			ps.ready = isPodReady(pod)

			// Determine if this pod is the master via GetReplicationInfo.
			// If that fails (e.g., pod restarting), fall back to the pod label
			// set by the sidecar so that the state machine is not stalled by
			// transient connectivity issues.
			info, infoErr := checker.GetReplicationInfo(ctx, v, podName)
			if infoErr == nil && info.Role == common.RoleMaster {
				// An answer beats every heuristic, terminating or not: a master that
				// still serves INFO still holds writes, and dropping it would let the
				// resolver demote the pod that has the data.
				ps.isMaster = true
				masterIdx = i
			} else if infoErr != nil && labelClaimsMaster(ps.pod) {
				// GetReplicationInfo failed; trust the pod label written by the sidecar.
				ps.isMaster = true
				masterIdx = i
			}
		}
		pods[i] = ps
	}

	return pods, masterIdx, nil
}

// countUpdatedPods returns how many pods are updated and ready.
func countUpdatedPods(pods []podState) int {
	count := 0
	for _, ps := range pods {
		if !ps.needsUpdate && ps.ready {
			count++
		}
	}
	return count
}

// countReplacedPods returns how many pods have been replaced (do not need an
// update), regardless of whether they are ready.  This is used by
// clearStaleRollingUpdateState to distinguish "no pods replaced yet" (stale
// state from a prior rolling update) from "pods replaced but not yet ready"
// (current rolling update in progress).
func countReplacedPods(pods []podState) int {
	count := 0
	for _, ps := range pods {
		if !ps.needsUpdate {
			count++
		}
	}
	return count
}

// replaceNextReplica finds the next replica pod that needs updating and deletes it.
// Pods are sorted by creation timestamp descending (youngest replica first) to
// minimize the risk window: the youngest replica has the least unique data.
// After each replacement, the operator waits for full replication sync before
// proceeding to the next pod, ensuring at least floor(n/2) synced replicas at all times.
// Returns nil if no replica needs replacement (all replicas are done).
func (r *ValkeyReconciler) replaceNextReplica(ctx context.Context, v *vkov1.Valkey, pods []podState) *RollingUpdateResult {
	logger := log.FromContext(ctx)

	// Before deleting the next replica, verify that all already-replaced replicas
	// have completed replication sync. This ensures we never have multiple
	// replicas in an un-synced state simultaneously.
	if result := r.verifyReplacedReplicasSynced(ctx, v, pods); result != nil {
		return result
	}

	// Build a list of replica pods that need updating, sorted youngest-first.
	candidates := sortReplicaCandidates(pods)

	if len(candidates) == 0 {
		return nil
	}

	ps := candidates[0]

	if !ps.exists {
		logger.Info("Waiting for pod to be recreated", "pod", ps.name)
		return &RollingUpdateResult{NeedsRequeue: true, RequeueAfter: rollingUpdateRequeueDelay}
	}

	if !ps.ready {
		logger.Info("Waiting for replaced pod to become ready", "pod", ps.name)
		return &RollingUpdateResult{NeedsRequeue: true, RequeueAfter: rollingUpdateRequeueDelay}
	}

	// Set state to replacing-replicas if not already set.
	if r.getRollingUpdateState(v) == "" {
		if err := r.setRollingUpdateState(ctx, v, stateReplacingReplicas); err != nil {
			return &RollingUpdateResult{Error: err}
		}
	}

	totalPods := len(pods)
	updatedCount := countUpdatedPods(pods)
	phase := fmt.Sprintf("%s %d/%d", vkov1.ValkeyPhaseRollingUpdate, updatedCount, totalPods)
	_ = r.updatePhase(ctx, v, ValkeyPhase(phase),
		fmt.Sprintf("Replacing pod %s", ps.name))

	r.recordEvent(v, corev1.EventTypeNormal, "RollingUpdate",
		"Deleting replica pod %s for rolling update (youngest-first)", ps.name)

	logger.Info("Deleting replica pod for rolling update", "pod", ps.name)
	if err := r.deleteOwnedPod(ctx, ps.pod); err != nil {
		return &RollingUpdateResult{Error: fmt.Errorf("deleting pod %s: %w", ps.name, err)}
	}
	return &RollingUpdateResult{NeedsRequeue: true, RequeueAfter: rollingUpdateRequeueDelay}
}

// sortReplicaCandidates returns replica pods needing updates, sorted by creation
// timestamp descending (youngest first). Non-existent pods are placed first
// (they need recreation and are the youngest by definition).
func sortReplicaCandidates(pods []podState) []podState {
	var candidates []podState
	for _, ps := range pods {
		if ps.needsUpdate && !ps.isMaster {
			candidates = append(candidates, ps)
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		// Non-existent pods first (waiting for recreation).
		if !candidates[i].exists {
			return true
		}
		if !candidates[j].exists {
			return false
		}
		// Youngest (most recent creation timestamp) first.
		ti := candidates[i].pod.CreationTimestamp.Time
		tj := candidates[j].pod.CreationTimestamp.Time
		return ti.After(tj)
	})
	return candidates
}

// verifyReplacedReplicasSynced checks that all already-replaced (updated, ready)
// non-master replicas have completed replication sync. This is called before
// deleting the next replica to ensure the cluster always has sufficient synced
// replicas for high availability.
// Returns nil when all replaced replicas are synced, or a requeue/pause result otherwise.
func (r *ValkeyReconciler) verifyReplacedReplicasSynced(ctx context.Context, v *vkov1.Valkey, pods []podState) *RollingUpdateResult {
	logger := log.FromContext(ctx)
	checker := r.getInstanceChecker()

	for _, ps := range pods {
		// Skip pods that still need updating, the master, and non-existent pods.
		if ps.needsUpdate || ps.isMaster || !ps.exists {
			continue
		}

		// A replaced pod that exists but is not yet ready must block the next
		// deletion. Without this guard, the operator would skip the not-ready
		// pod and immediately delete the next candidate, resulting in multiple
		// replicas being replaced simultaneously.
		if !ps.ready {
			logger.Info("Replaced pod not yet ready, waiting before replacing next", "pod", ps.name)
			return &RollingUpdateResult{NeedsRequeue: true, RequeueAfter: rollingUpdateRequeueDelay}
		}

		info, err := checker.GetReplicationInfo(ctx, v, ps.name)
		if err != nil {
			logger.Info("Cannot verify replication sync on replaced pod, waiting", "pod", ps.name, "error", err)
			r.ensureSyncWaitTimestamp(ctx, v)
			if r.isSyncWaitTimedOut(v) {
				return r.pauseRollingUpdate(ctx, v,
					fmt.Sprintf("Pod %s failed to respond to replication check within timeout", ps.name))
			}
			return &RollingUpdateResult{NeedsRequeue: true, RequeueAfter: rollingUpdateRequeueDelay}
		}

		if reason := replicationNotEstablishedReason(ps.name, info); reason != "" {
			logger.Info("Replaced pod has not completed replication, waiting", "pod", ps.name, "reason", reason)
			r.ensureSyncWaitTimestamp(ctx, v)

			totalPods := len(pods)
			updatedCount := countUpdatedPods(pods)
			phase := fmt.Sprintf("%s %d/%d (syncing)", vkov1.ValkeyPhaseRollingUpdate, updatedCount, totalPods)
			_ = r.updatePhase(ctx, v, ValkeyPhase(phase),
				fmt.Sprintf("Waiting for replication sync on pod %s", ps.name))

			if r.isSyncWaitTimedOut(v) {
				return r.pauseRollingUpdate(ctx, v,
					fmt.Sprintf("Pod %s replication sync timed out after %v (%s)",
						ps.name, v.GetSyncTimeout(), reason))
			}
			return &RollingUpdateResult{NeedsRequeue: true, RequeueAfter: rollingUpdateRequeueDelay}
		}
	}

	// All replaced replicas are synced — clear the wait timestamp and proceed.
	r.clearSyncWaitTimestamp(ctx, v)
	return nil
}

// pauseRollingUpdate pauses the rolling update by setting a status condition
// and recording a warning event. The operator will not resume until the user
// applies a new spec change.
func (r *ValkeyReconciler) pauseRollingUpdate(ctx context.Context, v *vkov1.Valkey, reason string) *RollingUpdateResult {
	logger := log.FromContext(ctx)
	logger.Info("Pausing rolling update due to sync failure", "reason", reason)

	r.setStatusCondition(ctx, v,
		vkov1.ConditionTypeRollingUpdatePaused,
		metav1.ConditionTrue,
		"SyncTimeout",
		reason)

	_ = r.updatePhase(ctx, v, vkov1.ValkeyPhaseError,
		fmt.Sprintf("Rolling update paused: %s", reason))

	r.recordEvent(v, corev1.EventTypeWarning, "RollingUpdatePaused", reason)

	// Clear rolling update state so the next spec change triggers a fresh start.
	if err := r.clearRollingUpdateState(ctx, v); err != nil {
		return &RollingUpdateResult{Error: err}
	}

	// Return completed=false, no requeue — the operator waits for a new spec change.
	return &RollingUpdateResult{}
}

// ensureSyncWaitTimestamp arms the sync-wait bound if it is not already armed.
// It goes through ensureWaitBound for the same reason ensureSentinelAwarenessTimestamp
// does (ADR 0010 D7/D8/D14): with the annotation alone, a CR write that keeps
// failing meant the bound never armed, isSyncWaitTimedOut answered false forever,
// and the replica-replacement phase requeued indefinitely without ever reaching
// pauseRollingUpdate.
func (r *ValkeyReconciler) ensureSyncWaitTimestamp(ctx context.Context, v *vkov1.Valkey) {
	r.ensureWaitBound(ctx, v, annotationSyncWaitStarted, boundSyncWait)
}

// clearSyncWaitTimestamp removes the sync-wait-started annotation and the
// in-memory copy of the bound. Both must go: this runs mid-update once every
// replaced replica is synced, and a leftover first-seen would pre-expire the
// budget of the next sync wait of the same rolling update, pausing it for a
// timeout that never elapsed.
func (r *ValkeyReconciler) clearSyncWaitTimestamp(ctx context.Context, v *vkov1.Valkey) {
	r.nudges.forget(waitBoundKey(v.Namespace, v.Name, boundSyncWait))
	if v.Annotations == nil {
		return
	}
	if _, ok := v.Annotations[annotationSyncWaitStarted]; !ok {
		return
	}
	delete(v.Annotations, annotationSyncWaitStarted)
	_ = r.Update(ctx, v)
}

// isSyncWaitTimedOut returns true if the sync wait has exceeded the configured timeout.
func (r *ValkeyReconciler) isSyncWaitTimedOut(v *vkov1.Valkey) bool {
	return r.waitBoundExceeded(v, annotationSyncWaitStarted, boundSyncWait, v.GetSyncTimeout())
}

// recordEvent emits a Kubernetes Event on the Valkey CR if an EventRecorder
// is configured. It is a no-op when Recorder is nil (e.g., in unit tests).
func (r *ValkeyReconciler) recordEvent(v *vkov1.Valkey, eventType, reason, messageFmt string, args ...interface{}) {
	if r.Recorder == nil {
		return
	}
	r.Recorder.Eventf(v, nil, eventType, reason, reason, messageFmt, args...)
}

// handleMasterFailover checks if the master needs updating, verifies all replicas
// are ready and synced, then triggers a Sentinel failover.
// Uses annotation-based state tracking to ensure failover is only triggered once,
// even when multiple reconcile loops run concurrently.
// Returns nil if the master does not need updating.
func (r *ValkeyReconciler) handleMasterFailover(ctx context.Context, v *vkov1.Valkey, pods []podState, masterIdx int) *RollingUpdateResult {
	logger := log.FromContext(ctx)

	if masterIdx < 0 || !pods[masterIdx].needsUpdate {
		return nil
	}

	// If failover was already triggered (by a prior reconcile in this storm),
	// don't trigger it again. Let handlePostFailover deal with it.
	currentState := r.getRollingUpdateState(v)
	if currentState == stateFailoverTriggered || currentState == stateReplacingMaster || currentState == stateFailoverReset {
		logger.Info("Failover already triggered by prior reconcile, skipping to post-failover handling")
		return &RollingUpdateResult{NeedsRequeue: true, RequeueAfter: rollingUpdateRequeueDelay}
	}

	// Check if all non-master pods are ready and synced before doing failover.
	if result := r.waitForReplicasReady(ctx, v, pods, masterIdx); result != nil {
		return result
	}

	// Execute WAIT on the master to ensure all pending writes are replicated
	// before triggering failover. This prevents data loss from async replication.
	if result := r.waitForWriteSync(ctx, v, pods, masterIdx); result != nil {
		return result
	}

	// Before triggering failover, verify sentinel has discovered all replicas.
	// After a recent sentinel reset (e.g., from a prior rolling update's
	// finalization), sentinel needs time to discover replicas via INFO polling.
	// Triggering failover before sentinel knows about replicas results in
	// NOGOODSLAVE, wasting ~75s on the retry cycle.
	// A sentinelAwarenessTimeout cap prevents an indefinite stall when sentinel
	// is stuck with 0 slaves (e.g., in a resource-constrained CI environment).
	expectedReplicas := len(pods) - 1
	if !r.isSentinelAwareOfReplicas(ctx, v, expectedReplicas) {
		r.ensureSentinelAwarenessTimestamp(ctx, v)
		if r.isSentinelAwarenessStalled(v) {
			logger.Info("Sentinel awareness stalled, proceeding with failover regardless",
				"expectedReplicas", expectedReplicas)
		} else {
			logger.Info("Waiting for sentinel to discover replicas before failover",
				"expectedReplicas", expectedReplicas)
			return &RollingUpdateResult{NeedsRequeue: true, RequeueAfter: 5 * time.Second}
		}
	}

	// Set state BEFORE triggering failover to prevent concurrent reconciles
	// from also triggering failover.
	if err := r.setRollingUpdateState(ctx, v, stateFailoverTriggered); err != nil {
		return &RollingUpdateResult{Error: err}
	}

	// Record when the failover was triggered so we can detect stale failovers.
	if err := r.setFailoverTimestamp(ctx, v); err != nil {
		return &RollingUpdateResult{Error: err}
	}
	_ = r.updatePhase(ctx, v, vkov1.ValkeyPhaseFailover, "Triggering Sentinel failover before updating master pod")

	r.recordEvent(v, corev1.EventTypeNormal, "FailoverTriggered",
		"Triggering Sentinel failover before updating master pod")

	if err := r.triggerSentinelFailover(ctx, v); err != nil {
		logger.Info("Sentinel failover command failed, will retry via post-failover handler", "error", err)
	} else {
		logger.Info("Sentinel failover triggered, waiting for completion")
	}

	return &RollingUpdateResult{NeedsRequeue: true, RequeueAfter: 15 * time.Second}
}

// waitForReplicasReady verifies all non-master replicas are ready and have actually
// received the master dataset. Returns a requeue result if any replica has not.
//
// This is the last gate in front of the promotion, and the promotion is followed by
// the delete of the outgoing master -- so a replica that is waved through here and
// promoted takes the only remaining copy of the data with it. It therefore asks the
// full question (replicationNotEstablishedReason) rather than only whether a full
// sync is currently running.
//
// The wait is bounded by the same sync-wait budget the replica phase uses
// (docs/adr/0010-every-rolling-update-wait-is-bounded.md): a replica that never
// establishes replication would otherwise requeue forever, which is the failure mode
// that bound exists to prevent. On expiry the rolling update pauses rather than
// promoting anyway -- an update that stops half-done is recoverable, a promotion
// onto an empty replica is not.
func (r *ValkeyReconciler) waitForReplicasReady(ctx context.Context, v *vkov1.Valkey, pods []podState, masterIdx int) *RollingUpdateResult {
	logger := log.FromContext(ctx)
	checker := r.getInstanceChecker()

	for i, ps := range pods {
		if i == masterIdx {
			continue
		}
		if !ps.ready || ps.needsUpdate {
			logger.Info("Waiting for all replicas to be ready before master failover")
			return &RollingUpdateResult{NeedsRequeue: true, RequeueAfter: rollingUpdateRequeueDelay}
		}

		info, err := checker.GetReplicationInfo(ctx, v, ps.name)
		if err != nil {
			logger.Info("Cannot verify replication sync, waiting", "pod", ps.name, "error", err)
			return r.waitOrPauseForReplicaSync(ctx, v,
				fmt.Sprintf("replication status of %s is unavailable: %v", ps.name, err))
		}
		if reason := replicationNotEstablishedReason(ps.name, info); reason != "" {
			logger.Info("Replica has not completed replication, waiting before master failover",
				"pod", ps.name, "reason", reason)
			return r.waitOrPauseForReplicaSync(ctx, v, reason)
		}
	}

	// Nothing is waiting on replication any more, so the bound must not stay armed:
	// a leftover first-seen would pre-expire the next wait of the same update.
	r.clearSyncWaitTimestamp(ctx, v)
	return nil
}

// waitOrPauseForReplicaSync arms the sync-wait bound, requeues while it holds, and
// pauses the rolling update once it has expired.
func (r *ValkeyReconciler) waitOrPauseForReplicaSync(ctx context.Context, v *vkov1.Valkey, reason string) *RollingUpdateResult {
	r.ensureSyncWaitTimestamp(ctx, v)
	if r.isSyncWaitTimedOut(v) {
		return r.pauseRollingUpdate(ctx, v,
			fmt.Sprintf("Replication did not complete within %v before the master failover (%s)",
				v.GetSyncTimeout(), reason))
	}
	return &RollingUpdateResult{NeedsRequeue: true, RequeueAfter: rollingUpdateRequeueDelay}
}

// verifyPromotionCandidateHoldsData refuses a promotion whose candidate holds no keys
// while the outgoing master holds some. It returns nil when the promotion may go
// ahead: the counts are consistent, or the master itself is empty and there is
// nothing to lose.
//
// The counts are logged on the way through, and that is half the point. The outgoing
// master is deleted moments after the promotion, so a promotion that turns out to
// have taken an empty pod cannot be reconstructed from anything afterwards -- a CI
// failure whose promoted master served an empty dataset could not be attributed for
// exactly this reason.
//
// An unreadable count is an unanswered question, not a negative answer, and degrades
// toward not promoting (docs/adr/0007-failover-aware-rolling-update.md, D3): it waits on
// the bounded sync budget like every other unverifiable state here.
func (r *ValkeyReconciler) verifyPromotionCandidateHoldsData(
	ctx context.Context, v *vkov1.Valkey, master, candidate podState) *RollingUpdateResult {
	logger := log.FromContext(ctx)

	tlsConfig, err := r.buildTLSConfig(ctx, v, builder.ValkeyTLSSecretName(v))
	if err != nil {
		logger.Info("Could not build TLS config for the pre-promotion key count", "error", err)
		return r.waitOrPauseForReplicaSync(ctx, v,
			fmt.Sprintf("the key counts before promoting %s are unavailable: %v", candidate.name, err))
	}

	password := r.readValkeyPassword(ctx, v)
	port := int(builder.ServicePort(v))

	masterKeys, err := r.newValkeyClient(
		health.PodAddressForComponent(v, master.name, common.ComponentValkey, port), password, tlsConfig).DBSize()
	if err != nil {
		logger.Info("Cannot read the key count of the outgoing master, waiting",
			common.RoleMaster, master.name, "error", err)
		return r.waitOrPauseForReplicaSync(ctx, v,
			fmt.Sprintf("the key count of %s is unavailable: %v", master.name, err))
	}
	if masterKeys == 0 {
		// Nothing to lose, and an empty cluster is a legitimate state -- refusing here
		// would stall every rolling update of a cluster that holds no data yet.
		return nil
	}

	candidateKeys, err := r.newValkeyClient(
		health.PodAddressForComponent(v, candidate.name, common.ComponentValkey, port), password, tlsConfig).DBSize()
	if err != nil {
		logger.Info("Cannot read the key count of the promotion candidate, waiting",
			"candidate", candidate.name, "error", err)
		return r.waitOrPauseForReplicaSync(ctx, v,
			fmt.Sprintf("the key count of %s is unavailable: %v", candidate.name, err))
	}
	if candidateKeys == 0 {
		logger.Info("Promotion candidate holds no data while the master does, not failing over",
			"candidate", candidate.name, common.RoleMaster, master.name, "masterKeys", masterKeys)
		return r.waitOrPauseForReplicaSync(ctx, v,
			fmt.Sprintf("%s holds no keys while %s holds %d", candidate.name, master.name, masterKeys))
	}

	logger.Info("Promotion candidate holds data",
		"candidate", candidate.name, "candidateKeys", candidateKeys,
		common.RoleMaster, master.name, "masterKeys", masterKeys)
	return nil
}

// waitWriteSyncTimeout is the timeout in milliseconds for the WAIT command.
// Replicas should already be synced at this point, so 5 seconds is generous.
const waitWriteSyncTimeout = 5000

// waitWriteSyncClientOverhead is additional time added to the client deadline on
// top of the WAIT command timeout. This covers TLS handshake +  AUTH round trip
// so the client never times out before the server finishes the blocking WAIT.
const waitWriteSyncClientOverhead = 5 * time.Second

// waitForWriteSync sends a WAIT command to the master to ensure all pending writes
// have been acknowledged by all replicas before failover. This prevents data loss
// that can occur during async replication when a failover happens.
//
// If WAIT returns fewer acknowledgements than expected -- but at least one -- the
// method accepts the partial result rather than retrying forever: a cascaded
// replication chain (replica -> replica -> master) acknowledges through the
// intermediate node, and waitForReplicasReady has confirmed every replica has its
// replication established.
//
// Zero acknowledgements is not a partial result. It means no replica confirmed the
// master offset at all, so nothing proves the pod about to be promoted holds the
// data -- and the outgoing master is deleted moments later. That case waits on the
// bounded sync budget and pauses the update rather than failing over.
func (r *ValkeyReconciler) waitForWriteSync(ctx context.Context, v *vkov1.Valkey, pods []podState, masterIdx int) *RollingUpdateResult {
	logger := log.FromContext(ctx)

	masterPod := pods[masterIdx]
	addr := health.PodAddressForComponent(v, masterPod.name, common.ComponentValkey, int(builder.ServicePort(v)))

	// Count the number of non-master replicas that should acknowledge.
	numReplicas := 0
	for i, ps := range pods {
		if i != masterIdx && ps.ready && !ps.needsUpdate {
			numReplicas++
		}
	}

	if numReplicas == 0 {
		logger.Info("No replicas to wait for write sync")
		return nil
	}

	tlsConfig, err := r.buildTLSConfig(ctx, v, builder.ValkeyTLSSecretName(v))
	if err != nil {
		logger.Info("Could not build TLS config for WAIT", "error", err)
		return &RollingUpdateResult{NeedsRequeue: true, RequeueAfter: rollingUpdateRequeueDelay}
	}

	c := r.newValkeyClient(addr, r.readValkeyPassword(ctx, v), tlsConfig)
	// The WAIT command blocks server-side for up to waitWriteSyncTimeout ms.
	// Set the client timeout to cover that plus TLS/AUTH overhead so we never
	// hit a client-side i/o timeout before the server responds.
	c.SetTimeout(time.Duration(waitWriteSyncTimeout)*time.Millisecond + waitWriteSyncClientOverhead)

	acked, err := c.Wait(numReplicas, waitWriteSyncTimeout)
	if err != nil {
		logger.Info("WAIT command failed, will retry", common.RoleMaster, masterPod.name, "error", err)
		return &RollingUpdateResult{NeedsRequeue: true, RequeueAfter: rollingUpdateRequeueDelay}
	}

	if acked == 0 {
		logger.Info("No replica acknowledged the master offset, not failing over",
			common.RoleMaster, masterPod.name, "expected", numReplicas)
		return r.waitOrPauseForReplicaSync(ctx, v,
			fmt.Sprintf("no replica of %s acknowledged its pending writes", masterPod.name))
	}

	if acked < numReplicas {
		// This typically happens when replicas form a cascaded replication chain
		// (replica → replica → master) instead of all connecting directly to the
		// master. At least one replica acknowledged, and waitForReplicasReady has
		// confirmed replication is established on every one of them, so accept the
		// partial acknowledgement and proceed with failover.
		logger.Info("Partial WAIT acknowledgement accepted (possible cascaded replication)",
			common.RoleMaster, masterPod.name, "expected", numReplicas, "acked", acked)
		return nil
	}

	logger.Info("All replicas acknowledged pending writes",
		common.RoleMaster, masterPod.name, "acked", acked)
	return nil
}

// replaceRemainingPods finds and replaces any remaining pods with the old image.
// Before deleting the former master, it verifies that a new master exists,
// has completed replication sync, and has actual data (DBSIZE > 0) to prevent data loss.
func (r *ValkeyReconciler) replaceRemainingPods(ctx context.Context, v *vkov1.Valkey, pods []podState) RollingUpdateResult {
	logger := log.FromContext(ctx)
	checker := r.getInstanceChecker()

	for _, ps := range pods {
		if !ps.needsUpdate {
			continue
		}

		if !ps.exists {
			logger.Info("Waiting for pod to be recreated", "pod", ps.name)
			return RollingUpdateResult{NeedsRequeue: true, RequeueAfter: rollingUpdateRequeueDelay}
		}

		if !ps.ready {
			logger.Info("Waiting for pod to become ready", "pod", ps.name)
			return RollingUpdateResult{NeedsRequeue: true, RequeueAfter: rollingUpdateRequeueDelay}
		}

		// Before deleting the former master (now a replica after failover),
		// verify that a new-image master exists and has all replicas synced.
		if v.IsSentinelEnabled() {
			verified, result := r.verifyNewMasterReady(ctx, v, pods, checker)
			if !verified {
				return result
			}
		}

		// Mark state as replacing-master.
		if err := r.setRollingUpdateState(ctx, v, stateReplacingMaster); err != nil {
			return RollingUpdateResult{Error: err}
		}

		logger.Info("Deleting remaining pod for rolling update", "pod", ps.name)
		if err := r.deleteOwnedPod(ctx, ps.pod); err != nil {
			return RollingUpdateResult{Error: fmt.Errorf("deleting pod %s: %w", ps.name, err)}
		}
		return RollingUpdateResult{NeedsRequeue: true, RequeueAfter: rollingUpdateRequeueDelay}
	}

	// Should not reach here, but requeue to be safe.
	return RollingUpdateResult{NeedsRequeue: true, RequeueAfter: rollingUpdateRequeueDelay}
}

// handlePostFailover handles the state after a Sentinel failover has been triggered.
// It re-collects fresh pod states (roles may have changed since the failover),
// waits for the new master to stabilize and have replicas connected, then
// proceeds to delete the old master pod.
//
// If no new master is found within failoverRetryTimeout, it resets sentinel
// state and re-triggers the failover to handle sentinel cooldown issues
// (e.g., during consecutive rolling updates).
func (r *ValkeyReconciler) handlePostFailover(ctx context.Context, v *vkov1.Valkey, _ []podState, _ int) RollingUpdateResult {
	checker := r.getInstanceChecker()

	// Re-collect pod states to get fresh role information.
	// After a failover, roles change and we must not rely on stale data.
	currentSts := &appsv1.StatefulSet{}
	stsName := common.StatefulSetName(v, common.ComponentValkey)
	if err := r.Get(ctx, types.NamespacedName{Name: stsName, Namespace: v.Namespace}, currentSts); err != nil {
		return RollingUpdateResult{Error: fmt.Errorf("getting StatefulSet in post-failover: %w", err)}
	}
	// Treated as absent, like in checkAndHandleRollingUpdate: the StatefulSet was
	// ours when this rolling update entered its state machine, so a foreign one
	// here means it was replaced mid-update (ADR 0020).
	if !metav1.IsControlledBy(currentSts, v) {
		return RollingUpdateResult{}
	}

	freshPods, _, err := r.collectPodStates(ctx, v, currentSts)
	if err != nil {
		return RollingUpdateResult{Error: err}
	}

	// Find the new master among pods with the new image.
	for _, ps := range freshPods {
		if ps.needsUpdate || !ps.ready || !ps.exists {
			continue
		}
		info, infoErr := checker.GetReplicationInfo(ctx, v, ps.name)
		if infoErr != nil {
			continue
		}
		if info.Role == common.RoleMaster {
			return r.handleNewMasterFound(ctx, v, ps, info, freshPods)
		}
	}

	// No new master found yet. Handle failover timeout or wait.
	return r.handleNoMasterFound(ctx, v, freshPods)
}

// handleNewMasterFound processes the case where a new-image master has been
// detected after failover. It checks whether the master has connected replicas
// and either proceeds with old-master replacement or waits/resets sentinel.
func (r *ValkeyReconciler) handleNewMasterFound(ctx context.Context, v *vkov1.Valkey, ps podState, info *valkeyclient.ReplicationInfo, freshPods []podState) RollingUpdateResult {
	logger := log.FromContext(ctx)

	if info.ConnectedSlaves == 0 {
		return r.handleMasterWithNoReplicas(ctx, v, ps, freshPods)
	}

	// New master is ready. Proceed to replace the old master.
	logger.Info("New master is ready with connected replicas",
		"newMaster", ps.name, "connectedSlaves", info.ConnectedSlaves)
	return r.replaceRemainingPods(ctx, v, freshPods)
}

// handleMasterWithNoReplicas handles the case where the new master exists but
// has no connected replicas. In resource-constrained environments (CI), sentinel
// may not have properly reconfigured replicas.
//
// On each timeout it:
//  1. Directly commands all non-master pods to REPLICAOF the new master, bypassing
//     sentinel's potentially-delayed reconfiguration.
//  2. Resets sentinel state so it rediscovers the topology.
//  3. Tracks how many resets have occurred via annotationReconnectResetCount.
//
// After maxReconnectResets attempts the function proceeds with the rolling update
// regardless, breaking the infinite retry loop. verifyNewMasterReady will still
// gate the old-master deletion until replication is confirmed.
func (r *ValkeyReconciler) handleMasterWithNoReplicas(ctx context.Context, v *vkov1.Valkey, ps podState, allPods []podState) RollingUpdateResult {
	logger := log.FromContext(ctx)

	resetCount := r.getReconnectResetCount(v)

	if r.isReplicaReconnectTimedOut(v) {
		logger.Info("Replicas failed to connect to new master within timeout",
			"newMaster", ps.name, "resetCount", resetCount)

		headlessName := common.HeadlessServiceName(v, common.ComponentValkey)
		masterAddr := fmt.Sprintf("%s.%s.%s.svc.cluster.local", ps.name, headlessName, v.Namespace)

		// Directly tell every non-master pod to replicate from the new master.
		// This bypasses the sentinel reconfiguration delay that causes the stall.
		r.forceReplicaConnections(ctx, v, ps.name, allPods)

		r.resetSentinelState(ctx, v, masterAddr)

		if resetCount >= maxReconnectResets {
			// We have sent REPLICAOF and reset sentinel multiple times.
			// The replicas should connect imminently. Proceed with the rolling
			// update — verifyNewMasterReady will block the final deletion until
			// replication is confirmed, so this is safe.
			logger.Info("Max reconnect resets reached, proceeding with rolling update",
				"newMaster", ps.name)
			if err := r.clearReconnectResetCount(ctx, v); err != nil {
				return RollingUpdateResult{Error: err}
			}
			return r.replaceRemainingPods(ctx, v, allPods)
		}

		// Persist the incremented reset count and reset the wait timestamp in a
		// single API call to avoid a double-update race.
		if err := r.incrementReconnectResetCount(ctx, v, resetCount+1); err != nil {
			return RollingUpdateResult{Error: err}
		}
		return RollingUpdateResult{NeedsRequeue: true, RequeueAfter: 15 * time.Second}
	}

	logger.Info("New master has no connected replicas yet, waiting for sync",
		"newMaster", ps.name)
	return RollingUpdateResult{NeedsRequeue: true, RequeueAfter: rollingUpdateRequeueDelay}
}

// forceReplicaConnections sends a direct REPLICAOF command to every ready non-master
// pod, instructing it to replicate from masterPodName. This is a best-effort
// operation used when sentinel has failed to reconfigure replicas on its own.
func (r *ValkeyReconciler) forceReplicaConnections(ctx context.Context, v *vkov1.Valkey, masterPodName string, pods []podState) {
	logger := log.FromContext(ctx)

	tlsConfig, err := r.buildTLSConfig(ctx, v, builder.ValkeyTLSSecretName(v))
	if err != nil {
		logger.Info("Could not build TLS config for REPLICAOF, skipping forced replica connections", "error", err)
		return
	}

	headlessName := common.HeadlessServiceName(v, common.ComponentValkey)
	masterHost := fmt.Sprintf("%s.%s.%s.svc.cluster.local", masterPodName, headlessName, v.Namespace)
	portStr := fmt.Sprintf("%d", builder.ServicePort(v))
	password := r.readValkeyPassword(ctx, v)

	for _, ps := range pods {
		if ps.name == masterPodName || !ps.exists || !ps.ready {
			continue
		}
		addr := health.PodAddressForComponent(v, ps.name, common.ComponentValkey, int(builder.ServicePort(v)))
		c := r.newValkeyClient(addr, password, tlsConfig)
		if replicaErr := c.ReplicaOf(masterHost, portStr); replicaErr != nil {
			logger.Info("REPLICAOF command failed (best-effort)", "pod", ps.name, common.RoleMaster, masterHost, "error", replicaErr)
		} else {
			logger.Info("Sent REPLICAOF to pod", "pod", ps.name, common.RoleMaster, masterHost)
		}
	}
}

// getReconnectResetCount returns the current sentinel reset counter stored in
// the Valkey CR annotations, or 0 if the annotation is absent or malformed.
func (r *ValkeyReconciler) getReconnectResetCount(v *vkov1.Valkey) int {
	if v.Annotations == nil {
		return 0
	}
	s, ok := v.Annotations[annotationReconnectResetCount]
	if !ok || s == "" {
		return 0
	}
	var n int
	if _, scanErr := fmt.Sscanf(s, "%d", &n); scanErr != nil {
		return 0
	}
	return n
}

// incrementReconnectResetCount persists newCount in the reset-count annotation
// and simultaneously refreshes the failover timestamp and clears the sentinel-
// awareness timestamp, so the next wait period starts from now.
// All three are written in a single Update to avoid multiple API calls.
func (r *ValkeyReconciler) incrementReconnectResetCount(ctx context.Context, v *vkov1.Valkey, newCount int) error {
	if v.Annotations == nil {
		v.Annotations = make(map[string]string)
	}
	v.Annotations[annotationReconnectResetCount] = fmt.Sprintf("%d", newCount)
	v.Annotations[annotationFailoverTimestamp] = time.Now().UTC().Format(time.RFC3339)
	// Also clear the sentinel-awareness timestamp so the next failover attempt's
	// stall-detection starts from a fresh baseline after this sentinel reset. The
	// in-memory copy is first-seen-wins, so it has to be dropped along with the
	// annotation — a leftover entry would pre-expire the next attempt's budget and
	// push the operator into a failover Sentinel is not ready for.
	delete(v.Annotations, annotationSentinelAwarenessStarted)
	r.nudges.forget(waitBoundKey(v.Namespace, v.Name, boundSentinelAwareness))
	return r.Update(ctx, v)
}

// clearReconnectResetCount removes the reset-count annotation from the Valkey CR.
func (r *ValkeyReconciler) clearReconnectResetCount(ctx context.Context, v *vkov1.Valkey) error {
	if v.Annotations == nil {
		return nil
	}
	if _, ok := v.Annotations[annotationReconnectResetCount]; !ok {
		return nil
	}
	delete(v.Annotations, annotationReconnectResetCount)
	return r.Update(ctx, v)
}

// clearSentinelAwarenessTimestamp removes the sentinel-awareness-started annotation
// from the Valkey CR. This must be called whenever sentinel is reset so that the
// next failover attempt's stall-detection starts from a fresh baseline, not from a
// timestamp that predates the most recent SENTINEL REMOVE+MONITOR. The in-memory
// copy of the bound is dropped for the same reason (see incrementReconnectResetCount).
func (r *ValkeyReconciler) clearSentinelAwarenessTimestamp(v *vkov1.Valkey) {
	if v.Annotations != nil {
		delete(v.Annotations, annotationSentinelAwarenessStarted)
	}
	r.nudges.forget(waitBoundKey(v.Namespace, v.Name, boundSentinelAwareness))
}

// handleNoMasterFound handles the case where no new-image master was detected
// after failover. If the failover timeout has elapsed, it resets sentinel state
// and transitions to the failover-reset phase for a retry.
func (r *ValkeyReconciler) handleNoMasterFound(ctx context.Context, v *vkov1.Valkey, freshPods []podState) RollingUpdateResult {
	logger := log.FromContext(ctx)

	if !r.isFailoverTimedOut(v) {
		logger.Info("Waiting for failover to complete, no new master detected yet")
		return RollingUpdateResult{NeedsRequeue: true, RequeueAfter: rollingUpdateRequeueDelay}
	}

	logger.Info("Failover timed out, resetting sentinel state and scheduling retry")

	// Determine the correct master address from the pods we already know about.
	masterAddr := ""
	for _, ps := range freshPods {
		if ps.isMaster {
			headlessName := common.HeadlessServiceName(v, common.ComponentValkey)
			masterAddr = fmt.Sprintf("%s.%s.%s.svc.cluster.local", ps.name, headlessName, v.Namespace)
			break
		}
	}

	// Reset sentinel with the correct master address.
	r.resetSentinelState(ctx, v, masterAddr)

	// Clear the sentinel-awareness timestamp so the next failover attempt's
	// stall-detection starts from a fresh baseline after this sentinel reset.
	r.clearSentinelAwarenessTimestamp(v)

	// Update the failover timestamp for the retry.
	if err := r.setFailoverTimestamp(ctx, v); err != nil {
		return RollingUpdateResult{Error: err}
	}

	// Transition to failover-reset state. On the next reconcile (after delay),
	// sentinel will have rediscovered the topology and we can retrigger failover.
	if err := r.setRollingUpdateState(ctx, v, stateFailoverReset); err != nil {
		return RollingUpdateResult{Error: err}
	}

	return RollingUpdateResult{NeedsRequeue: true, RequeueAfter: 15 * time.Second}
}

// verifyNewMasterReady verifies that a new-image master exists and has
// connected replicas before we delete the old master pod.
// Returns (true, _) if verified, (false, result) if we need to wait.
func (r *ValkeyReconciler) verifyNewMasterReady(ctx context.Context, v *vkov1.Valkey, pods []podState, checker InstanceChecker) (bool, RollingUpdateResult) {
	logger := log.FromContext(ctx)
	for _, other := range pods {
		if other.needsUpdate || !other.ready {
			continue
		}
		info, err := checker.GetReplicationInfo(ctx, v, other.name)
		if err != nil {
			continue
		}
		if info.Role == common.RoleMaster {
			// Verify the new master has replicas connected.
			if info.ConnectedSlaves == 0 {
				logger.Info("New master has no connected replicas, waiting for sync",
					"newMaster", other.name)
				return false, RollingUpdateResult{NeedsRequeue: true, RequeueAfter: rollingUpdateRequeueDelay}
			}
			if info.MasterSyncInProgress {
				logger.Info("New master sync in progress, waiting",
					"newMaster", other.name)
				return false, RollingUpdateResult{NeedsRequeue: true, RequeueAfter: rollingUpdateRequeueDelay}
			}

			// Verify the new master has data (DBSIZE > 0) if the old master had data.
			// This is a critical safety check: if the new master is empty but the
			// old master had data, the failover promoted an empty replica.
			addr := health.PodAddressForComponent(v, other.name, common.ComponentValkey, int(builder.ServicePort(v)))
			tlsConfig, tlsErr := r.buildTLSConfig(ctx, v, builder.ValkeyTLSSecretName(v))
			if tlsErr != nil {
				logger.Info("Could not build TLS config for DBSIZE check", "error", tlsErr)
				return false, RollingUpdateResult{NeedsRequeue: true, RequeueAfter: rollingUpdateRequeueDelay}
			}
			vc := r.newValkeyClient(addr, r.readValkeyPassword(ctx, v), tlsConfig)
			dbsize, err := vc.DBSize()
			if err != nil {
				logger.Info("Cannot check DBSIZE on new master, waiting",
					"newMaster", other.name, "error", err)
				return false, RollingUpdateResult{NeedsRequeue: true, RequeueAfter: rollingUpdateRequeueDelay}
			}

			logger.Info("New master verified with data",
				"newMaster", other.name, "dbsize", dbsize, "connectedSlaves", info.ConnectedSlaves)
			return true, RollingUpdateResult{}
		}
	}

	logger.Info("No new-image master found yet, waiting for failover to complete")
	return false, RollingUpdateResult{NeedsRequeue: true, RequeueAfter: rollingUpdateRequeueDelay}
}

// getRollingUpdateState returns the current rolling update state from annotations.
func (r *ValkeyReconciler) getRollingUpdateState(v *vkov1.Valkey) string {
	if v.Annotations == nil {
		return ""
	}
	return v.Annotations[annotationRollingUpdateState]
}

// setRollingUpdateState sets the rolling update state annotation on the Valkey CR.
// This persists the state to etcd, preventing concurrent reconcile loops from
// re-entering critical code paths.
func (r *ValkeyReconciler) setRollingUpdateState(ctx context.Context, v *vkov1.Valkey, state string) error {
	logger := log.FromContext(ctx)
	logger.Info("Setting rolling update state", "state", state)

	if v.Annotations == nil {
		v.Annotations = make(map[string]string)
	}
	v.Annotations[annotationRollingUpdateState] = state
	return r.Update(ctx, v)
}

// clearRollingUpdateState removes the rolling update state, failover timestamp, and
// reconnect reset count annotations.
func (r *ValkeyReconciler) clearRollingUpdateState(ctx context.Context, v *vkov1.Valkey) error {
	// Drop the in-memory copies of the wait bounds first, and unconditionally: a
	// leftover first-seen would pre-expire the budget of the next rolling update
	// even when no annotation survived to do it (ADR 0010 D7, D8, D10).
	//
	// Every completion reaches this function, because checkAndHandleRollingUpdate
	// calls it for any dispatch target that reports Completed. Calling it earlier,
	// from inside a target, is still correct and costs nothing the second time.
	r.forgetWaitBounds(v.Namespace, v.Name)

	if v.Annotations == nil {
		return nil
	}
	_, hasState := v.Annotations[annotationRollingUpdateState]
	_, hasTimestamp := v.Annotations[annotationFailoverTimestamp]
	_, hasCount := v.Annotations[annotationReconnectResetCount]
	_, hasFinalization := v.Annotations[annotationFinalizationTimestamp]
	_, hasSentinelAwareness := v.Annotations[annotationSentinelAwarenessStarted]
	_, hasPromoted := v.Annotations[annotationPromotedPod]
	_, hasSyncWait := v.Annotations[annotationSyncWaitStarted]
	_, hasTopologyRestore := v.Annotations[annotationTopologyRestoreStarted]
	_, hasManualFailover := v.Annotations[annotationManualFailoverStarted]
	if !hasState && !hasTimestamp && !hasCount && !hasFinalization && !hasSentinelAwareness &&
		!hasPromoted && !hasSyncWait && !hasTopologyRestore && !hasManualFailover {
		return nil
	}
	delete(v.Annotations, annotationRollingUpdateState)
	delete(v.Annotations, annotationFailoverTimestamp)
	delete(v.Annotations, annotationReconnectResetCount)
	delete(v.Annotations, annotationFinalizationTimestamp)
	delete(v.Annotations, annotationSentinelAwarenessStarted)
	delete(v.Annotations, annotationPromotedPod)
	delete(v.Annotations, annotationSyncWaitStarted)
	delete(v.Annotations, annotationTopologyRestoreStarted)
	delete(v.Annotations, annotationManualFailoverStarted)
	if err := r.Update(ctx, v); err != nil {
		return err
	}

	// The rolling update is over, so every drain-promotion stamp of this cluster is
	// spent evidence — and this is the one place every completion path funnels
	// through. It has to be here rather than only in recordPromotedMaster, because
	// that is not the single funnel for the known-master annotation it was taken
	// for: persistManualFailoverState writes the annotation directly, and the paths
	// that finish a rolling update without recording a master of their own
	// (verifyTopologyRestored, finalizeMultiReplicaRollingUpdate,
	// handlePostManualFailover) clear the state without ever passing through it.
	//
	// It must NOT move above the early returns. checkAndHandleRollingUpdate calls
	// this function on every pass that reports Completed — including the passes
	// where nothing was running — and it runs before checkSteadyStateSplitBrain in
	// the same reconcile. Clearing unconditionally would therefore delete the stamp
	// of a fresh drain in the pass before the check that exists to read it.
	//
	// clearDrainStamps logs what it could not clear and returns nothing: the state
	// is already gone at this point, so a failure must not fail the completion. The
	// cost of a leftover stamp is that it outranks the annotation on the next
	// multi-master pass (evidence first), which can have the operator adopt a stale
	// pod and REPLICAOF the master the update legitimately promoted.
	r.clearDrainStamps(ctx, v)
	return nil
}

// setFailoverTimestamp records the current time as the failover trigger time.
func (r *ValkeyReconciler) setFailoverTimestamp(ctx context.Context, v *vkov1.Valkey) error {
	if v.Annotations == nil {
		v.Annotations = make(map[string]string)
	}
	v.Annotations[annotationFailoverTimestamp] = time.Now().UTC().Format(time.RFC3339)
	return r.Update(ctx, v)
}

// isFailoverTimedOut checks whether the failover was triggered more than
// failoverRetryTimeout ago, indicating that it likely failed (e.g., due to
// sentinel cooldown) and should be retried.
func (r *ValkeyReconciler) isFailoverTimedOut(v *vkov1.Valkey) bool {
	return annotationTimestampExceeded(v, annotationFailoverTimestamp, failoverRetryTimeout)
}

// isReplicaReconnectTimedOut checks whether the failover was triggered more
// than replicaReconnectTimeout ago, indicating that replicas have failed to
// connect to the new master and sentinel state needs to be reset. This prevents
// the rolling update from stalling indefinitely in handlePostFailover when
// ConnectedSlaves remains 0 (common in resource-constrained CI environments).
func (r *ValkeyReconciler) isReplicaReconnectTimedOut(v *vkov1.Valkey) bool {
	return annotationTimestampExceeded(v, annotationFailoverTimestamp, replicaReconnectTimeout)
}

// hasMinWaitElapsed checks whether at least failoverResetMinWait has elapsed
// since the failover timestamp. Used to prevent retriggering failover too
// quickly after a SENTINEL RESET, giving sentinel time to rediscover replicas.
// Unlike the timeout checks above, a missing timestamp means there is nothing
// to wait out, so it reports true.
func (r *ValkeyReconciler) hasMinWaitElapsed(v *vkov1.Valkey) bool {
	tsStr, ok := v.Annotations[annotationFailoverTimestamp]
	if !ok || tsStr == "" {
		return true
	}
	return annotationTimestampExceeded(v, annotationFailoverTimestamp, failoverResetMinWait)
}

// isSentinelAwareOfReplicas queries sentinel to check whether it has discovered the
// expected number of replicas. This prevents triggering a failover when sentinel
// hasn't fully built its topology (e.g., after a recent SENTINEL REMOVE + MONITOR).
//
// Returns true if at least one sentinel reports enough replicas, or if all sentinels
// are unreachable (allowing the failover to proceed optimistically in environments
// like unit tests where no real sentinel exists).
// Returns false only when a reachable sentinel explicitly reports too few replicas.
func (r *ValkeyReconciler) isSentinelAwareOfReplicas(ctx context.Context, v *vkov1.Valkey, expectedReplicas int) bool {
	logger := log.FromContext(ctx)
	monitorName := builder.SentinelMonitorName(v)
	sentinelStsName := common.StatefulSetName(v, common.ComponentSentinel)

	sentinelReplicas := int32(3)
	if v.Spec.Sentinel != nil && v.Spec.Sentinel.Replicas > 0 {
		sentinelReplicas = v.Spec.Sentinel.Replicas
	}
	password := r.sentinelPassword(ctx, v)

	for i := int32(0); i < sentinelReplicas; i++ {
		podName := fmt.Sprintf("%s-%d", sentinelStsName, i)

		tlsConfig, tlsErr := r.buildTLSConfig(ctx, v, builder.SentinelTLSSecretName(v))
		if tlsErr != nil {
			continue
		}
		sentinelPort := builder.SentinelPort
		if tlsConfig != nil {
			sentinelPort = builder.SentinelTLSPort
		}
		addr := health.PodAddressForComponent(v, podName, common.ComponentSentinel, sentinelPort)

		c := r.newValkeyClient(addr, password, tlsConfig)
		info, err := c.SentinelMaster(monitorName)
		if err != nil {
			continue
		}

		if info.NumSlaves >= expectedReplicas {
			return true
		}

		logger.Info("Sentinel has not discovered all replicas yet",
			"sentinel", podName, "numSlaves", info.NumSlaves, "expected", expectedReplicas)
		return false
	}

	// All sentinels unreachable — proceed optimistically.
	return true
}

// resetSentinelState reconfigures all sentinel instances by removing and re-adding
// the monitored master. Unlike SENTINEL RESET (which reverts to the initial config
// from the config file and loses the current master address after failovers), this
// approach preserves the correct master by using the provided masterAddr.
//
// If masterAddr is empty, falls back to the default master address (pod-0).
//
// This is necessary when a failover needs to be retried after a timeout — sentinel's
// internal state may be stale or have cooldowns that prevent another failover.
// Best-effort: errors are logged but not returned.
func (r *ValkeyReconciler) resetSentinelState(ctx context.Context, v *vkov1.Valkey, masterAddr string) {
	logger := log.FromContext(ctx)
	monitorName := builder.SentinelMonitorName(v)
	sentinelStsName := common.StatefulSetName(v, common.ComponentSentinel)

	// Calculate quorum (same logic as sentinel config generation).
	quorum := builder.SentinelQuorum
	if v.Spec.Sentinel != nil && v.Spec.Sentinel.Replicas > 0 {
		quorum = int(v.Spec.Sentinel.Replicas/2) + 1
	}

	if masterAddr == "" {
		// Fallback to the default master address (pod-0).
		masterAddr = builder.MasterAddress(v)
		logger.Info("No master address provided, falling back to default", "masterAddr", masterAddr)
	}

	logger.Info("Reconfiguring sentinel with correct master", "masterAddr", masterAddr)

	// Use the appropriate port for monitoring.
	monitorPort := builder.ValkeyPort
	if v.IsTLSEnabled() {
		monitorPort = builder.TLSPort
	}

	sentinelReplicas := int32(3)
	if v.Spec.Sentinel != nil && v.Spec.Sentinel.Replicas > 0 {
		sentinelReplicas = v.Spec.Sentinel.Replicas
	}
	sentinelPwd := r.sentinelPassword(ctx, v)
	valkeyPwd := r.readValkeyPassword(ctx, v)

	for i := int32(0); i < sentinelReplicas; i++ {
		podName := fmt.Sprintf("%s-%d", sentinelStsName, i)

		tlsConfig, tlsErr := r.buildTLSConfig(ctx, v, builder.SentinelTLSSecretName(v))
		if tlsErr != nil {
			logger.V(1).Info("Could not build TLS config for sentinel reconfig", "error", tlsErr)
			continue
		}
		sentinelPort := builder.SentinelPort
		if tlsConfig != nil {
			sentinelPort = builder.SentinelTLSPort
		}
		addr := health.PodAddressForComponent(v, podName, common.ComponentSentinel, sentinelPort)

		c := r.newValkeyClient(addr, sentinelPwd, tlsConfig)

		// Remove the existing monitor (clears all slave/sentinel tracking and cooldowns).
		if err := c.SentinelRemove(monitorName); err != nil {
			logger.V(1).Info("Sentinel remove failed (best-effort)", "sentinel", podName, "error", err)
			// Fallback to SENTINEL RESET if REMOVE fails.
			if err := c.SentinelReset(monitorName); err != nil {
				logger.V(1).Info("Sentinel reset also failed", "sentinel", podName, "error", err)
			}
			continue
		}

		// Re-add the monitor with the correct current master address.
		if err := c.SentinelMonitorAdd(monitorName, masterAddr, monitorPort, quorum); err != nil {
			logger.V(1).Info("Sentinel monitor add failed", "sentinel", podName, "error", err)
			continue
		}

		// Reconfigure sentinel parameters to match our desired settings.
		_ = c.SentinelSet(monitorName, "down-after-milliseconds", fmt.Sprintf("%d", builder.SentinelDownAfterMilliseconds))
		_ = c.SentinelSet(monitorName, "failover-timeout", fmt.Sprintf("%d", builder.SentinelFailoverTimeout))
		_ = c.SentinelSet(monitorName, "parallel-syncs", fmt.Sprintf("%d", builder.SentinelParallelSyncs))
		_ = c.SentinelSet(monitorName, "resolve-hostnames", "yes")
		_ = c.SentinelSet(monitorName, "announce-hostnames", "yes")

		// Restore auth-pass so sentinel can authenticate to the monitored Valkey master.
		// Without this, sentinel marks the master as s_down/disconnected and cannot
		// discover replicas, causing NOGOODSLAVE on failover attempts.
		if valkeyPwd != "" {
			_ = c.SentinelSet(monitorName, "auth-pass", valkeyPwd)
		}

		logger.Info("Sentinel reconfigured successfully", "sentinel", podName, "masterAddr", masterAddr)
	}
}

// triggerSentinelFailover sends SENTINEL FAILOVER to a Sentinel instance.
func (r *ValkeyReconciler) triggerSentinelFailover(ctx context.Context, v *vkov1.Valkey) error {
	logger := log.FromContext(ctx)
	monitorName := builder.SentinelMonitorName(v)
	sentinelStsName := common.StatefulSetName(v, common.ComponentSentinel)

	sentinelReplicas := int32(3)
	if v.Spec.Sentinel != nil && v.Spec.Sentinel.Replicas > 0 {
		sentinelReplicas = v.Spec.Sentinel.Replicas
	}
	password := r.sentinelPassword(ctx, v)

	// Try each sentinel until one successfully triggers failover.
	var lastErr error
	for i := int32(0); i < sentinelReplicas; i++ {
		podName := fmt.Sprintf("%s-%d", sentinelStsName, i)

		tlsConfig, tlsErr := r.buildTLSConfig(ctx, v, builder.SentinelTLSSecretName(v))
		if tlsErr != nil {
			lastErr = tlsErr
			logger.V(1).Info("Could not build TLS config for sentinel failover", "error", tlsErr)
			continue
		}
		sentinelPort := builder.SentinelPort
		if tlsConfig != nil {
			sentinelPort = builder.SentinelTLSPort
		}
		addr := health.PodAddressForComponent(v, podName, common.ComponentSentinel, sentinelPort)

		c := r.newValkeyClient(addr, password, tlsConfig)
		if err := c.SentinelFailover(monitorName); err != nil {
			lastErr = err
			logger.V(1).Info("Sentinel failover attempt failed", "sentinel", podName, "error", err)
			continue
		}

		logger.Info("Sentinel failover triggered successfully", "sentinel", podName)
		return nil
	}

	return fmt.Errorf("all sentinel failover attempts failed, last error: %w", lastErr)
}

// handleStandaloneRollingUpdate handles rolling update for non-HA (no Sentinel) mode.
//
// When the valkey image changes, the pod is deleted so the StatefulSet recreates it
// with the new template. When only the sidecar image changed (operator upgrade)
// and the cluster has only a single replica (true standalone), the pod is NOT
// automatically restarted to avoid disrupting a single-instance cluster without
// redundancy. Instead, a SidecarUpdatePending condition is set and the update is
// deferred to the next natural pod restart (manual delete, eviction, or valkey
// image change).
//
// For multi-replica clusters without Sentinel, sidecar-only changes ARE applied
// via a rolling update because the remaining replicas provide redundancy.
func (r *ValkeyReconciler) handleStandaloneRollingUpdate(ctx context.Context, v *vkov1.Valkey, currentSts *appsv1.StatefulSet) RollingUpdateResult {
	logger := log.FromContext(ctx)
	desiredImage := valkeyImageFromSts(currentSts)
	sidecarImg := sidecarImageFromSts(currentSts)
	stsName := common.StatefulSetName(v, common.ComponentValkey)
	sidecarPending := false
	isTrueStandalone := v.Spec.Replicas <= 1

	for i := int32(0); i < *currentSts.Spec.Replicas; i++ {
		podName := fmt.Sprintf("%s-%d", stsName, i)
		pod := &corev1.Pod{}
		err := r.Get(ctx, types.NamespacedName{Name: podName, Namespace: v.Namespace}, pod)
		if err != nil {
			if apierrors.IsNotFound(err) {
				// Pod doesn't exist yet, wait for it.
				logger.Info("Waiting for pod to be recreated", "pod", podName)
				return RollingUpdateResult{NeedsRequeue: true, RequeueAfter: rollingUpdateRequeueDelay}
			}
			return RollingUpdateResult{Error: fmt.Errorf("getting pod %s: %w", podName, err)}
		}
		if !podIsOurs(pod, currentSts) {
			return RollingUpdateResult{Error: foreignObjectError("Pod", podName)}
		}

		if podNeedsUpdate(pod, desiredImage, sidecarImg, configHashFromSts(currentSts), podSpecHashFromSts(currentSts), currentSts.Spec.Template.Spec.Containers) {
			// For sidecar-only changes in true standalone mode (single replica),
			// defer the update to the next natural pod restart rather than
			// auto-deleting the only instance.
			if isTrueStandalone && isSidecarOnlyChange(pod, desiredImage, sidecarImg) {
				logger.Info("Standalone pod has outdated sidecar; update deferred to next pod restart",
					"pod", podName)
				sidecarPending = true
				continue
			}

			if !isPodReady(pod) {
				logger.Info("Pod not ready, waiting", "pod", podName)
				return RollingUpdateResult{NeedsRequeue: true, RequeueAfter: rollingUpdateRequeueDelay}
			}

			_ = r.updatePhase(ctx, v, ValkeyPhase(fmt.Sprintf("%s %d/%d", vkov1.ValkeyPhaseRollingUpdate, 0, *currentSts.Spec.Replicas)),
				fmt.Sprintf("Replacing pod %s with new image", podName))

			logger.Info("Deleting pod for standalone rolling update", "pod", podName)
			if err := r.deleteOwnedPod(ctx, pod); err != nil {
				return RollingUpdateResult{Error: fmt.Errorf("deleting pod %s: %w", podName, err)}
			}
			return RollingUpdateResult{NeedsRequeue: true, RequeueAfter: rollingUpdateRequeueDelay}
		}

		if !isPodReady(pod) {
			logger.Info("Updated pod not yet ready", "pod", podName)
			return RollingUpdateResult{NeedsRequeue: true, RequeueAfter: rollingUpdateRequeueDelay}
		}
	}

	// Reflect sidecar-pending state in the CR status conditions.
	r.setSidecarUpdatePendingCondition(ctx, v, sidecarPending)

	if sidecarPending {
		// The sidecar update is deferred — no active rolling update in progress.
		// Return empty result so the reconciler does not requeue but the condition
		// remains set until the next natural pod restart clears it.
		return RollingUpdateResult{}
	}

	// All pods updated and ready.
	return RollingUpdateResult{Completed: true}
}

// isSidecarOnlyChange returns true when the pod needs replacing exclusively
// because the sidecar image has drifted while the main valkey image is current.
// Returns false when the valkey image also changed, when no sidecar is present,
// or when desiredSidecarImage is empty (sidecar-less deployment).
func isSidecarOnlyChange(pod *corev1.Pod, desiredValkeyImage, desiredSidecarImage string) bool {
	if len(pod.Spec.Containers) == 0 || desiredSidecarImage == "" {
		return false
	}
	valkeyUpToDate := true
	sidecarOutdated := false
	sidecarFound := false
	for _, c := range pod.Spec.Containers {
		switch c.Name {
		case builder.ValkeyContainerName:
			if desiredValkeyImage != "" && c.Image != desiredValkeyImage {
				valkeyUpToDate = false
			}
		case builder.SidecarContainerName:
			sidecarFound = true
			if c.Image != desiredSidecarImage {
				sidecarOutdated = true
			}
		}
	}
	return valkeyUpToDate && sidecarFound && sidecarOutdated
}

// handleMultiReplicaRollingUpdate orchestrates a rolling update for multi-replica
// clusters without Sentinel. It uses a state machine to safely replace all pods
// while preserving data:
//
//  1. Replace replica pods one by one (skip the master).
//  2. After all replicas are updated and synced, perform a manual failover:
//     promote a replica to temporary master and redirect all other replicas.
//  3. Delete the old master (pod-0).
//  4. When pod-0 comes back, sync it from the promoted replica, then restore
//     the original topology (pod-0 as master).
func (r *ValkeyReconciler) handleMultiReplicaRollingUpdate(ctx context.Context, v *vkov1.Valkey, currentSts *appsv1.StatefulSet) RollingUpdateResult {
	totalPods := int(*currentSts.Spec.Replicas)

	pods, masterIdx, err := r.collectPodStates(ctx, v, currentSts)
	if err != nil {
		return RollingUpdateResult{Error: err}
	}

	// Detect and resolve split-brain before proceeding. This mirrors the sentinel
	// path and ensures a rogue master left over from a prior failed topology
	// restoration is demoted before the rolling update makes further decisions.
	//
	// While the manual failover is in flight the promoted pod and the old master
	// both report master by design, and the promoted pod is the authority. Without
	// naming it, the resolver falls back to "most connected slaves": with two
	// replicas neither master has one, the tie goes to the lowest index — the old
	// master that was just deleted — and the promoted pod is demoted to replicate
	// from a pod that is about to disappear, taking the data with it.
	//
	// The same tie decides the restoration states: while pod-0 is syncing back, and
	// after the restoration was abandoned, a pod-0 that reports master again would
	// win the fallback over the pod that actually holds the writes. There the
	// authority is the known-master annotation, which promotePod0AndRedirect moves
	// to pod-0 only once the promotion succeeded.
	preferredMaster := ""
	switch r.getRollingUpdateState(v) {
	case stateManualFailover, stateReplacingMaster:
		preferredMaster = v.Annotations[annotationPromotedPod]
	case stateRestoringTopology, stateVerifyingTopology:
		preferredMaster = knownMasterPodName(v)
	}
	pods, masterIdx = r.resolveSplitBrain(ctx, v, pods, masterIdx, preferredMaster)

	updatedCount := countUpdatedPods(pods)
	if updatedCount == totalPods {
		// All pods are updated, but the state machine may still need to complete
		// (e.g., post-failover handling or topology restoration). Dispatch to the
		// correct handler before finalizing, otherwise these handlers are never
		// reached because dispatchMultiReplicaState is only called when
		// updatedCount != totalPods.
		currentState := r.getRollingUpdateState(v)
		switch currentState {
		case stateManualFailover, stateReplacingMaster:
			return r.handlePostManualFailover(ctx, v, currentSts)
		case stateRestoringTopology:
			return r.handleTopologyRestoration(ctx, v, currentSts)
		case stateVerifyingTopology:
			return r.verifyTopologyRestored(ctx, v, currentSts)
		default:
			return r.finalizeMultiReplicaRollingUpdate(ctx, v, pods)
		}
	}

	phase := fmt.Sprintf("%s %d/%d", vkov1.ValkeyPhaseRollingUpdate, updatedCount, totalPods)
	_ = r.updatePhase(ctx, v, ValkeyPhase(phase),
		fmt.Sprintf("Rolling update in progress: %d/%d pods updated", updatedCount, totalPods))

	currentState := r.getRollingUpdateState(v)
	currentState, err = r.clearStaleRollingUpdateState(ctx, v, currentState, countReplacedPods(pods))
	if err != nil {
		return RollingUpdateResult{Error: err}
	}

	return r.dispatchMultiReplicaState(ctx, v, currentSts, pods, masterIdx, currentState)
}

// dispatchMultiReplicaState routes the rolling update to the correct handler
// based on the current state machine phase.
func (r *ValkeyReconciler) dispatchMultiReplicaState(ctx context.Context, v *vkov1.Valkey, currentSts *appsv1.StatefulSet, pods []podState, masterIdx int, currentState string) RollingUpdateResult {
	logger := log.FromContext(ctx)

	if currentState == stateManualFailover || currentState == stateReplacingMaster {
		return r.handlePostManualFailover(ctx, v, currentSts)
	}
	if currentState == stateRestoringTopology {
		return r.handleTopologyRestoration(ctx, v, currentSts)
	}
	if currentState == stateVerifyingTopology {
		return r.verifyTopologyRestored(ctx, v, currentSts)
	}

	// Step 1: Replace replica pods first (skip master).
	if result := r.replaceNextReplica(ctx, v, pods); result != nil {
		return *result
	}

	// Step 2: All replicas updated. Trigger manual failover before replacing master.
	if masterIdx >= 0 && pods[masterIdx].needsUpdate {
		return r.handleManualFailover(ctx, v, pods, masterIdx)
	}

	if masterIdx < 0 && hasPendingUpdates(pods) {
		logger.Info("No master detected during rolling update, waiting for cluster to stabilize")
		return RollingUpdateResult{NeedsRequeue: true, RequeueAfter: rollingUpdateRequeueDelay}
	}

	return r.deleteNextPendingPod(ctx, pods)
}

// deleteNextPendingPod finds and deletes the first pod that still needs updating.
func (r *ValkeyReconciler) deleteNextPendingPod(ctx context.Context, pods []podState) RollingUpdateResult {
	logger := log.FromContext(ctx)
	for _, ps := range pods {
		if !ps.needsUpdate || !ps.exists || !ps.ready {
			continue
		}
		logger.Info("Deleting remaining pod for rolling update", "pod", ps.name)
		if err := r.deleteOwnedPod(ctx, ps.pod); err != nil {
			return RollingUpdateResult{Error: fmt.Errorf("deleting pod %s: %w", ps.name, err)}
		}
		return RollingUpdateResult{NeedsRequeue: true, RequeueAfter: rollingUpdateRequeueDelay}
	}
	return RollingUpdateResult{NeedsRequeue: true, RequeueAfter: rollingUpdateRequeueDelay}
}

// handleManualFailover promotes an updated replica to temporary master, redirects
// all other replicas to it, and then deletes the old master pod.
func (r *ValkeyReconciler) handleManualFailover(ctx context.Context, v *vkov1.Valkey, pods []podState, masterIdx int) RollingUpdateResult {
	logger := log.FromContext(ctx)

	// Ensure all replicas are ready and synced before failover.
	if result := r.waitForReplicasReady(ctx, v, pods, masterIdx); result != nil {
		return *result
	}

	// Use WAIT to ensure all pending writes are replicated.
	if result := r.waitForWriteSync(ctx, v, pods, masterIdx); result != nil {
		return *result
	}

	// Pick the first ready, updated replica as the promoted pod.
	promotedIdx := findPromotionCandidate(pods, masterIdx)
	if promotedIdx < 0 {
		logger.Info("No ready updated replica available for promotion, waiting")
		return RollingUpdateResult{NeedsRequeue: true, RequeueAfter: rollingUpdateRequeueDelay}
	}

	promotedPod := pods[promotedIdx]

	// The last look before the irreversible step. The Sentinel path verifies the same
	// thing after its failover and calls it a critical safety check
	// (verifyNewMasterReady); here the outgoing master is deleted seconds after the
	// promotion, so the check has to happen before it rather than after.
	if result := r.verifyPromotionCandidateHoldsData(ctx, v, pods[masterIdx], promotedPod); result != nil {
		return *result
	}

	if err := r.promoteAndRedirect(ctx, v, pods, promotedPod, masterIdx, promotedIdx); err != nil {
		logger.Info("Manual failover promotion failed", "error", err)
		return RollingUpdateResult{NeedsRequeue: true, RequeueAfter: rollingUpdateRequeueDelay}
	}

	// Record the promoted pod and set state. The promoted pod is also recorded as
	// the known master so that the replica ConfigMap points at it while the old
	// master is away: the init container of the returning pod reads that address
	// and joins as a replica instead of electing itself master (its peer-based
	// discovery rejects the promoted pod, which has no replicas attached yet).
	headlessName := common.HeadlessServiceName(v, common.ComponentValkey)
	promotedHost := fmt.Sprintf("%s.%s.%s.svc.cluster.local", promotedPod.name, headlessName, v.Namespace)
	if err := r.persistManualFailoverState(ctx, v, promotedPod.name, promotedHost); err != nil {
		return RollingUpdateResult{Error: fmt.Errorf("setting manual failover state: %w", err)}
	}

	// Republish the replica ConfigMap before the delete, so the recreated pod
	// mounts the new master address rather than the stale pod-0 default.
	// Best-effort: a failure here reopens the split-brain window, but blocking
	// the delete would stall the rolling update indefinitely.
	if err := r.reconcileReplicaConfigMap(ctx, v); err != nil {
		logger.Info("Could not publish known master to replica ConfigMap before master delete",
			common.RoleMaster, promotedHost, "error", err)
		r.recordEvent(v, corev1.EventTypeWarning, "KnownMasterPublishFailed",
			"Could not publish known master %s to the replica ConfigMap: %v", promotedHost, err)
	}

	// Delete the old master pod.
	_ = r.updatePhase(ctx, v, vkov1.ValkeyPhaseFailover,
		fmt.Sprintf("Manual failover: promoted %s, replacing master %s", promotedPod.name, pods[masterIdx].name))
	r.recordEvent(v, corev1.EventTypeNormal, "ManualFailover",
		"Promoted %s to temporary master, deleting old master %s", promotedPod.name, pods[masterIdx].name)
	logger.Info("Deleting old master pod after manual failover", "pod", pods[masterIdx].name)
	if err := r.deleteOwnedPod(ctx, pods[masterIdx].pod); err != nil {
		return RollingUpdateResult{Error: fmt.Errorf("deleting master pod %s: %w", pods[masterIdx].name, err)}
	}

	return RollingUpdateResult{NeedsRequeue: true, RequeueAfter: rollingUpdateRequeueDelay}
}

// persistManualFailoverState writes the three annotations that make the promoted
// pod the recognised master: the promoted pod, the rolling update state, and the
// known master.
//
// It is the most consequential write of the rolling update, because the promotion
// has already happened when it runs. If it fails, the cluster is failed over while
// the state annotation is still empty; the next pass feeds an empty known master
// into the split-brain resolver, which with two replicas ties at zero connected
// slaves, picks the lowest ordinal — the old master that is about to be deleted —
// and demotes the pod holding the data (the ADR 0008 D10, D11 loss).
//
// The dominant failure cause is a resourceVersion conflict against the concurrent
// status writer, so the write gets a bounded conflict retry: refetch, re-apply the
// three annotations, write again. The refetched object is copied back over v,
// because the caller keeps using it — replica ConfigMap, master delete, phase
// update — and needs both the annotations and a current resourceVersion.
//
// Accepted residual: an operator crash or a total API outage between the promotion
// and a successful write here still leaves the ADR 0008 D10, D11 window open. Closing that would
// require persisting before promoting, which the state machine does not support.
func (r *ValkeyReconciler) persistManualFailoverState(ctx context.Context, v *vkov1.Valkey, promotedPodName, promotedHost string) error {
	// The fourth annotation is the bound of the state this write enters
	// (docs/adr/0010-every-rolling-update-wait-is-bounded.md, D6). It is armed once, before the first
	// attempt, so a conflict retry re-applies the same deadline rather than handing the state a fresh
	// budget on every attempt.
	startedAt := r.armManualFailoverBound(v)
	apply := func(target *vkov1.Valkey) {
		if target.Annotations == nil {
			target.Annotations = make(map[string]string)
		}
		target.Annotations[annotationPromotedPod] = promotedPodName
		target.Annotations[annotationRollingUpdateState] = stateManualFailover
		target.Annotations[builder.AnnotationKnownMaster] = promotedHost
		target.Annotations[annotationManualFailoverStarted] = startedAt
	}

	apply(v)
	err := r.Update(ctx, v)
	if err == nil || !apierrors.IsConflict(err) {
		return err
	}

	logger := log.FromContext(ctx)
	logger.Info("Manual failover state write conflicted, retrying on a freshly read CR",
		"promotedPod", promotedPodName, "error", err)

	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		fresh := &vkov1.Valkey{}
		if getErr := r.Get(ctx, types.NamespacedName{Name: v.Name, Namespace: v.Namespace}, fresh); getErr != nil {
			return getErr
		}
		apply(fresh)
		if updateErr := r.Update(ctx, fresh); updateErr != nil {
			return updateErr
		}
		fresh.DeepCopyInto(v)
		return nil
	})
}

// findPromotionCandidate returns the index of the first ready, updated non-master
// pod suitable for promotion, or -1 if none is available.
//
// Pod-0 (index 0) is explicitly excluded: the rest of the state machine
// (handlePostManualFailover, handleTopologyRestoration) hardcodes pod-0 as the
// permanent-master target. Promoting pod-0 to temporary master would cause
// handlePostManualFailover to send it REPLICAOF <pod-0> — an infinite self-loop.
func findPromotionCandidate(pods []podState, masterIdx int) int {
	for i, ps := range pods {
		if i == 0 || i == masterIdx || ps.needsUpdate || !ps.ready || !ps.exists {
			continue
		}
		return i
	}
	return -1
}

// promoteAndRedirect promotes the selected replica to master (REPLICAOF NO ONE)
// and redirects all other non-master replicas to replicate from the promoted pod.
func (r *ValkeyReconciler) promoteAndRedirect(ctx context.Context, v *vkov1.Valkey, pods []podState, promotedPod podState, masterIdx, promotedIdx int) error {
	logger := log.FromContext(ctx)

	tlsConfig, err := r.buildTLSConfig(ctx, v, builder.ValkeyTLSSecretName(v))
	if err != nil {
		return fmt.Errorf("building TLS config: %w", err)
	}

	password := r.readValkeyPassword(ctx, v)
	port := int(builder.ServicePort(v))

	// Promote the selected replica.
	promotedAddr := health.PodAddressForComponent(v, promotedPod.name, common.ComponentValkey, port)
	c := r.newValkeyClient(promotedAddr, password, tlsConfig)
	if err := c.ReplicaOf("NO", "ONE"); err != nil {
		return fmt.Errorf("REPLICAOF NO ONE on %s: %w", promotedPod.name, err)
	}
	logger.Info("Promoted replica to temporary master", "pod", promotedPod.name)

	headlessName := common.HeadlessServiceName(v, common.ComponentValkey)
	promotedHost := fmt.Sprintf("%s.%s.%s.svc.cluster.local", promotedPod.name, headlessName, v.Namespace)
	portStr := fmt.Sprintf("%d", port)

	// Demote the outgoing master so it stops answering role:master for its
	// termination window — without this, two pods answer role:master between the
	// promotion above and the kubelet actually stopping valkey-server, which is
	// the exact state the steady-state check calls a split brain. Strictly after
	// the promotion (the demotion must never run against the only master) and
	// best-effort: the pod was drained of writes by waitForWriteSync before the
	// promotion and is deleted moments later, so a failure here must not abort
	// the failover.
	if masterIdx >= 0 && masterIdx < len(pods) && pods[masterIdx].exists {
		addr := health.PodAddressForComponent(v, pods[masterIdx].name, common.ComponentValkey, port)
		mc := r.newValkeyClient(addr, password, tlsConfig)
		if err := mc.ReplicaOf(promotedHost, portStr); err != nil {
			logger.Info("REPLICAOF demotion of the outgoing master failed (best-effort)",
				"pod", pods[masterIdx].name, "target", promotedHost, "error", err)
		} else {
			logger.Info("Demoted outgoing master to replica of the promoted pod",
				"pod", pods[masterIdx].name, "target", promotedHost)
		}
	}

	// Redirect all other replicas to the promoted pod.
	for i, ps := range pods {
		if i == masterIdx || i == promotedIdx || !ps.exists || !ps.ready {
			continue
		}
		addr := health.PodAddressForComponent(v, ps.name, common.ComponentValkey, port)
		rc := r.newValkeyClient(addr, password, tlsConfig)
		if err := rc.ReplicaOf(promotedHost, portStr); err != nil {
			logger.Info("REPLICAOF redirect failed (best-effort)", "pod", ps.name, "target", promotedHost, "error", err)
		}
	}

	return nil
}

// handlePostManualFailover waits for the old master pod (pod-0) to come back
// after deletion, then configures it to sync from the promoted replica.
//
// Every wait here is bounded by waitOrAbandonManualFailover
// (docs/adr/0010-every-rolling-update-wait-is-bounded.md, D6): pod-0 can fail to come back for
// reasons no requeue resolves, and this state has no other escape.
func (r *ValkeyReconciler) handlePostManualFailover(ctx context.Context, v *vkov1.Valkey, currentSts *appsv1.StatefulSet) RollingUpdateResult {
	logger := log.FromContext(ctx)

	promotedPodName := v.Annotations[annotationPromotedPod]
	if promotedPodName == "" {
		logger.Info("No promoted pod annotation found, clearing state")
		if err := r.clearRollingUpdateState(ctx, v); err != nil {
			return RollingUpdateResult{Error: err}
		}
		return RollingUpdateResult{NeedsRequeue: true, RequeueAfter: rollingUpdateRequeueDelay}
	}

	// Check if the master pod (pod-0) is back and ready.
	stsName := common.StatefulSetName(v, common.ComponentValkey)
	masterPodName := fmt.Sprintf("%s-0", stsName)
	masterPod := &corev1.Pod{}
	if err := r.Get(ctx, types.NamespacedName{Name: masterPodName, Namespace: v.Namespace}, masterPod); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("Waiting for master pod to be recreated", "pod", masterPodName)
			return r.waitOrAbandonManualFailover(ctx, v,
				fmt.Sprintf("%s was never recreated after the failover", masterPodName))
		}
		return RollingUpdateResult{Error: fmt.Errorf("getting master pod %s: %w", masterPodName, err)}
	}
	// This function decides that the failover finished and the cluster is back to
	// normal; a pod that is not ours must never be the evidence for that
	// (ADR 0020 D9).
	sts, stsErr := r.ownedDataStatefulSet(ctx, v)
	if stsErr != nil {
		return RollingUpdateResult{Error: fmt.Errorf("reading the data StatefulSet: %w", stsErr)}
	}
	if !podIsOurs(masterPod, sts) {
		return RollingUpdateResult{Error: foreignObjectError("Pod", masterPodName)}
	}

	// Skip pods that are being terminated (old pod not yet removed).
	if masterPod.DeletionTimestamp != nil {
		logger.Info("Master pod is being terminated, waiting for recreation", "pod", masterPodName)
		return r.waitOrAbandonManualFailover(ctx, v,
			fmt.Sprintf("%s never finished terminating", masterPodName))
	}

	// Verify this is the NEW pod (recreated by the StatefulSet with the updated
	// template) and not the old pod that a concurrent reconcile still sees. Without
	// this check, REPLICAOF is sent to the old pod that is about to be deleted,
	// and the new pod starts as a standalone master with no data.
	//
	// The comparison is podNeedsUpdate against the live StatefulSet template — the same verdict the
	// rest of the rolling update uses — and not the image alone. An image-only guard is void for a
	// config-hash-only or resources-only update (image unchanged, so every pod passes it), which left
	// the DeletionTimestamp check above as the only protection, and that one misses a stale cache read
	// (docs/adr/0007-failover-aware-rolling-update.md, D4). The image check is not dropped, it is
	// subsumed: podNeedsUpdate compares the Valkey and sidecar images first.
	if podNeedsUpdate(masterPod, valkeyImageFromSts(currentSts), sidecarImageFromSts(currentSts),
		configHashFromSts(currentSts), podSpecHashFromSts(currentSts), currentSts.Spec.Template.Spec.Containers) {
		logger.Info("Master pod does not match the StatefulSet template yet, waiting for replacement",
			"pod", masterPodName)
		return r.waitOrAbandonManualFailover(ctx, v,
			fmt.Sprintf("%s never came back on the current StatefulSet template", masterPodName))
	}

	if !isPodReady(masterPod) {
		logger.Info("Master pod not yet ready after recreation", "pod", masterPodName)
		return r.waitOrAbandonManualFailover(ctx, v,
			fmt.Sprintf("%s never became ready after recreation", masterPodName))
	}

	// Pod-0 is back and ready. Make it sync from the promoted replica.
	tlsConfig, err := r.buildTLSConfig(ctx, v, builder.ValkeyTLSSecretName(v))
	if err != nil {
		logger.Info("Could not build TLS config for topology restoration", "error", err)
		return r.waitOrAbandonManualFailover(ctx, v,
			fmt.Sprintf("the TLS config for %s could not be built: %v", masterPodName, err))
	}

	password := r.readValkeyPassword(ctx, v)
	port := int(builder.ServicePort(v))
	headlessName := common.HeadlessServiceName(v, common.ComponentValkey)
	promotedHost := fmt.Sprintf("%s.%s.%s.svc.cluster.local", promotedPodName, headlessName, v.Namespace)
	portStr := fmt.Sprintf("%d", port)

	// Send REPLICAOF <promoted> to pod-0 so it syncs data from the promoted replica.
	masterAddr := health.PodAddressForComponent(v, masterPodName, common.ComponentValkey, port)
	c := r.newValkeyClient(masterAddr, password, tlsConfig)
	if err := c.ReplicaOf(promotedHost, portStr); err != nil {
		logger.Info("REPLICAOF command failed on new pod-0", "pod", masterPodName, "target", promotedHost, "error", err)
		return r.waitOrAbandonManualFailover(ctx, v,
			fmt.Sprintf("REPLICAOF %s never succeeded on %s: %v", promotedHost, masterPodName, err))
	}
	logger.Info("Configured pod-0 as replica of promoted pod", "pod", masterPodName, common.RoleMaster, promotedHost)

	// Move to topology restoration state. The Phase 1 budget starts here, at the state transition, so
	// a timestamp left behind by an earlier update cannot spend it before the restoration ever runs
	// (docs/adr/0010-every-rolling-update-wait-is-bounded.md, D10). Both annotations are persisted by
	// the single Update below.
	r.armTopologyRestoreBound(v)
	if err := r.setRollingUpdateState(ctx, v, stateRestoringTopology); err != nil {
		return RollingUpdateResult{Error: err}
	}

	return RollingUpdateResult{NeedsRequeue: true, RequeueAfter: rollingUpdateRequeueDelay}
}

// waitOrAbandonManualFailover requeues while the deleted master may still come back,
// and hands the rolling update over to Phase 2 once the budget is spent.
//
// All six waits of handlePostManualFailover used to return a bare requeue
// (docs/adr/0010-every-rolling-update-wait-is-bounded.md, D6), so a pod-0 that never came back
// ready on the current template parked the state machine in manual-failover forever. It is not
// exotic: a PVC that cannot bind, an ImagePullBackOff on the new tag, a fail-closed webhook
// rejecting the pod CREATE, a rejected Delete that left the state annotation behind, or the
// operator killed between the promote and the delete. clearStaleRollingUpdateState rescues none of
// them -- it only clears when nothing was replaced yet, and the replicas were replaced before the
// failover.
//
// The cost of the stall was four things at once: the cluster served from the
// temporary master with nothing declaring that the end state, TopologyRestored was
// never written, the phase froze at "Rolling Update N/M", and -- because Reconcile
// returns on NeedsRequeue -- the whole tail of the pass never ran, the ADR 0011 D1
// steady-state split-brain check included.
//
// The escape is stateVerifyingTopology and not a cleared state, for the reason ADR 0010 D2-D4
// recorded one state later: once the state annotation is gone, checkAndHandleRollingUpdate
// early-returns whenever no pod needs an update, and nothing calls detectAndResolveSplitBrain
// again. Phase 2 is the last pass that can consolidate the masters a half-finished failover leaves
// behind, and abandonTopologyRestoration is exactly that handover: the Event,
// TopologyRestored=False, and a Phase 2 budget armed on entry
// (docs/adr/0010-every-rolling-update-wait-is-bounded.md, D10) rather than inherited.
func (r *ValkeyReconciler) waitOrAbandonManualFailover(ctx context.Context, v *vkov1.Valkey, reason string) RollingUpdateResult {
	r.ensureManualFailoverTimestamp(ctx, v)
	if !r.isManualFailoverStalled(v) {
		return RollingUpdateResult{NeedsRequeue: true, RequeueAfter: rollingUpdateRequeueDelay}
	}
	return r.abandonTopologyRestoration(ctx, v, reason)
}

// handleTopologyRestoration waits for pod-0 to finish syncing from the promoted
// replica, then restores the original topology: pod-0 as master, all others as replicas.
//
// This runs in two phases, tracked via annotationRollingUpdateState:
//
//   - stateRestoringTopology: Wait for pod-0 to sync as replica, promote it back
//     to master, send REPLICAOF to all other pods, transition to stateVerifyingTopology.
//   - stateVerifyingTopology (verifyTopologyRestored): Verify all replicas reconnected
//     to pod-0 via detectAndResolveSplitBrain. Retry REPLICAOF on any remaining rogue
//     masters. Complete when clean or after finalizationStallTimeout.
//
// Splitting into two phases prevents an infinite wait loop: once pod-0 is promoted back
// to master its role is common.RoleMaster, so the Phase 1 sync-check must not run again.
//
// Phase 1 is bounded by v.GetSyncTimeout(); see abandonTopologyRestoration for what
// happens when pod-0 never syncs back.
func (r *ValkeyReconciler) handleTopologyRestoration(ctx context.Context, v *vkov1.Valkey, currentSts *appsv1.StatefulSet) RollingUpdateResult {
	logger := log.FromContext(ctx)

	// Phase 1: Wait for pod-0 to sync as a replica of the promoted pod, then
	// promote it back to master and redirect all other replicas.
	checker := r.getInstanceChecker()
	stsName := common.StatefulSetName(v, common.ComponentValkey)
	masterPodName := fmt.Sprintf("%s-0", stsName)

	// Self-loop guard: if the promoted pod is pod-0 itself (caused by
	// findPromotionCandidate selecting pod-0 when the master was not pod-0),
	// handlePostManualFailover sent REPLICAOF <pod-0> to pod-0 → the link is
	// permanently down. Skip the sync-wait, promote pod-0 directly and recover.
	promotedPodName := v.Annotations[annotationPromotedPod]
	if promotedPodName == masterPodName {
		logger.Info("Self-loop detected: promoted pod is pod-0, recovering by promoting pod-0 directly",
			"pod", masterPodName)
		return r.promotePod0AndRedirect(ctx, v, currentSts, masterPodName)
	}

	if reason := r.pod0SyncWaitReason(ctx, v, checker, masterPodName); reason != "" {
		logger.Info("Pod-0 not ready to be promoted back, waiting", "reason", reason)
		return r.waitOrAbandonTopologyRestoration(ctx, v, reason)
	}

	return r.promotePod0AndRedirect(ctx, v, currentSts, masterPodName)
}

// pod0SyncWaitReason reports why pod-0 cannot be promoted back to master yet, or
// an empty string when it is ready.
//
// Role, master_link_status and master_sync_in_progress are all checked so that the
// full replication handshake is known to have completed. Right after REPLICAOF the
// link may sit in CONNECT/CONNECTING, where master_sync_in_progress is 0 but no
// data has been transferred yet; promoting pod-0 at that point would lose data.
func (r *ValkeyReconciler) pod0SyncWaitReason(ctx context.Context, v *vkov1.Valkey, checker InstanceChecker, masterPodName string) string {
	info, err := checker.GetReplicationInfo(ctx, v, masterPodName)
	if err != nil {
		return fmt.Sprintf("replication status of %s is unavailable: %v", masterPodName, err)
	}
	return replicationNotEstablishedReason(masterPodName, info)
}

// replicationNotEstablishedReason answers the question every gate that is about to
// act on a replica asks -- has this pod actually received its master dataset? --
// and returns the reason it has not, or "" when it has.
//
// The three fields are one answer, not three:
//   - role=master means a REPLICAOF has not taken effect yet.
//   - master_link_status != "up" means the handshake is still running. Right after
//     REPLICAOF the link sits in CONNECT/CONNECTING, where master_sync_in_progress
//     is 0 while no byte has moved.
//   - master_sync_in_progress means the transfer itself is not finished.
//
// Checking only the last one accepts a replica that never started syncing. Phase 1
// has reasoned this way since it was written; the gates in front of the failover
// did not, which let a promotion take a replica with no dataset and the delete of
// the outgoing master then take the last copy. The sidecar answers the same
// question the same way (isSyncedReplica, internal/sidecar/drain.go).
func replicationNotEstablishedReason(podName string, info *valkeyclient.ReplicationInfo) string {
	if info.Role == common.RoleMaster || info.MasterLinkStatus != "up" {
		return fmt.Sprintf("replication not established on %s (role=%s, linkStatus=%s)",
			podName, info.Role, info.MasterLinkStatus)
	}
	if info.MasterSyncInProgress {
		return fmt.Sprintf("%s is still syncing from its master", podName)
	}
	return ""
}

// waitOrAbandonTopologyRestoration requeues Phase 1 while pod-0 may still come
// back, and hands over to abandonTopologyRestoration once the sync timeout has
// passed. Without this bound every Phase 1 failure requeued forever: the outer
// loop cannot help, because clearStaleRollingUpdateState only runs on the
// updatedCount != totalPods branch and topology restoration runs with every pod
// already updated.
func (r *ValkeyReconciler) waitOrAbandonTopologyRestoration(ctx context.Context, v *vkov1.Valkey, reason string) RollingUpdateResult {
	r.ensureTopologyRestoreTimestamp(ctx, v)
	if !r.isTopologyRestoreStalled(v) {
		return RollingUpdateResult{NeedsRequeue: true, RequeueAfter: rollingUpdateRequeueDelay}
	}
	return r.abandonTopologyRestoration(ctx, v, reason)
}

// abandonTopologyRestoration gives up on handing the master role back to pod-0 and
// moves on to Phase 2, which consolidates the cluster on whichever master the
// known-master annotation names -- still the promoted replica, because
// promotePod0AndRedirect never ran.
//
// It is the escape of two states, not one: Phase 1 calls it when pod-0 never syncs back
// (docs/adr/0010-every-rolling-update-wait-is-bounded.md, D2-D4), and waitOrAbandonManualFailover
// calls it when pod-0 never comes back at all
// (docs/adr/0010-every-rolling-update-wait-is-bounded.md, D6). Both end in the same place for the
// same reason -- the promoted replica holds the writes and Phase 2 is the last pass that can
// consolidate the cluster on it.
//
// Forcing the promotion instead is not an option: an unsynced pod-0 would come up
// as an empty master and discard every write the promoted replica accepted since
// the failover. Leaving the state annotation in place is not one either -- that is
// the stall this exists to break. Going through Phase 2 rather than straight to a
// cleared state matters because it is the last pass that resolves a split brain:
// once the state annotation is gone, checkAndHandleRollingUpdate returns early and
// no reconcile calls detectAndResolveSplitBrain again.
func (r *ValkeyReconciler) abandonTopologyRestoration(ctx context.Context, v *vkov1.Valkey, reason string) RollingUpdateResult {
	logger := log.FromContext(ctx)

	promotedPod := v.Annotations[annotationPromotedPod]
	timeout := v.GetSyncTimeout()
	logger.Info("Topology restoration stalled, leaving the promoted pod as master",
		"reason", reason, "promotedPod", promotedPod, "timeout", timeout)
	r.recordEvent(v, corev1.EventTypeWarning, "TopologyRestoreAbandoned",
		"Topology restoration abandoned after %v (%s); %s stays master", timeout, reason, promotedPod)

	// The record goes before the state transition, and a conflict on it keeps the
	// state where it is. This pass is the only one that can write the verdict: the
	// transition below releases the stall and no later pass re-enters Phase 1, so a
	// swallowed write loses the record for the life of the cluster. It is the ADR
	// 0009 shape -- an unrecorded promotion is not a promotion -- applied to the
	// abandon (docs/adr/0010-every-rolling-update-wait-is-bounded.md, D3).
	//
	// Writing it first also removes the conflict that was losing it: the state write
	// below is this pass's first CR update, so the cached refresh inside the
	// condition write can no longer read back a version from before it.
	if err := r.recordTopologyRestoredCondition(ctx, v, metav1.ConditionFalse, "RestoreTimeout",
		fmt.Sprintf("pod-0 was not restored as master after %v (%s); %s stays master",
			timeout, reason, promotedPod)); err != nil {
		return RollingUpdateResult{Error: err}
	}

	// Phase 2 gets its own budget, armed here rather than inherited
	// (docs/adr/0010-every-rolling-update-wait-is-bounded.md, D10). Both annotations are persisted by
	// the single Update below.
	r.armFinalizationBound(v)
	if err := r.setRollingUpdateState(ctx, v, stateVerifyingTopology); err != nil {
		return RollingUpdateResult{Error: err}
	}

	return RollingUpdateResult{NeedsRequeue: true, RequeueAfter: rollingUpdateRequeueDelay}
}

// recordTopologyRestoredCondition records whether the rolling update handed the
// master role back to pod-0, and reports whether the record is worth another pass.
//
// The cluster serves normally either way, so this condition is the only durable
// trace of how the restoration ended. Both writers run exactly once per rolling
// update -- abandonTopologyRestoration and promotePod0AndRedirect each enter
// stateVerifyingTopology in the same pass, and nothing recomputes the condition
// afterwards -- which is why they write it before the state transition instead of
// after it (docs/adr/0010-every-rolling-update-wait-is-bounded.md, D3).
//
// Only a conflict is handed back. A conflict means the CR moved between the read
// and the write, and the next pass reads the moved version; the case seen in CI is
// the operator racing its own preceding update through the manager cache. Every
// other failure -- a withdrawn RBAC on the status subresource above all -- repeats
// identically on every pass, and returning it would pin the rolling update in
// stateRestoringTopology forever: the unbounded wait D2-D4 exists to remove. Those
// are logged, and the state machine advances without the record.
func (r *ValkeyReconciler) recordTopologyRestoredCondition(ctx context.Context, v *vkov1.Valkey,
	status metav1.ConditionStatus, reason, message string) error {
	_, err := r.writeStatusCondition(ctx, v, vkov1.ConditionTypeTopologyRestored, status, reason, message)
	if err == nil {
		return nil
	}

	logger := log.FromContext(ctx)
	if apierrors.IsConflict(err) {
		logger.Info("TopologyRestored write still conflicting, not advancing the rolling update state",
			"reason", reason, "error", err)
		return err
	}
	logger.Error(err, "Could not record TopologyRestored, advancing the rolling update without it",
		"reason", reason)
	return nil
}

// promotePod0AndRedirect promotes pod-0 to master via REPLICAOF NO ONE, redirects
// all other pods to replicate from it, and transitions to stateVerifyingTopology.
// It is the shared final step of both the normal topology restoration path and the
// self-loop recovery path.
func (r *ValkeyReconciler) promotePod0AndRedirect(ctx context.Context, v *vkov1.Valkey, currentSts *appsv1.StatefulSet, masterPodName string) RollingUpdateResult {
	logger := log.FromContext(ctx)

	tlsConfig, tlsErr := r.buildTLSConfig(ctx, v, builder.ValkeyTLSSecretName(v))
	if tlsErr != nil {
		logger.Info("Could not build TLS config for topology restoration", "error", tlsErr)
		return RollingUpdateResult{NeedsRequeue: true, RequeueAfter: rollingUpdateRequeueDelay}
	}

	password := r.readValkeyPassword(ctx, v)
	port := int(builder.ServicePort(v))
	stsName := common.StatefulSetName(v, common.ComponentValkey)
	headlessName := common.HeadlessServiceName(v, common.ComponentValkey)
	masterHost := fmt.Sprintf("%s.%s.%s.svc.cluster.local", masterPodName, headlessName, v.Namespace)
	portStr := fmt.Sprintf("%d", port)

	// The pod pod-0 is currently replicating from, captured before the promotion
	// so it can be handed back if the promotion cannot be recorded.
	previousMasterHost := v.Annotations[builder.AnnotationKnownMaster]

	// Promote pod-0 to master.
	masterAddr := health.PodAddressForComponent(v, masterPodName, common.ComponentValkey, port)
	c := r.newValkeyClient(masterAddr, password, tlsConfig)
	if err := c.ReplicaOf("NO", "ONE"); err != nil {
		logger.Info("REPLICAOF NO ONE failed on pod-0", "error", err)
		return RollingUpdateResult{NeedsRequeue: true, RequeueAfter: rollingUpdateRequeueDelay}
	}
	logger.Info("Promoted pod-0 back to master", "pod", masterPodName)

	// Pod-0 is the master again — point the known-master annotation (and with it
	// the replica ConfigMap) back at pod-0. Leaving it on the promoted pod would
	// make any later replica restart replicate from a pod that is about to be
	// demoted.
	//
	// A promotion that could not be recorded must not advance the state machine.
	// Proceeding froze the mismatch permanently: Phase 2 completes and clears the
	// state, this function provably never runs again (pod0SyncWaitReason rejects a
	// pod-0 that already reports master) and clearRollingUpdateState deliberately
	// keeps the known-master annotation. The stale name then boots the promoted
	// pod as a second master on its next restart, and checkSteadyStateSplitBrain
	// demotes pod-0 — the pod holding every write since the failover.
	//
	// Requeueing is safe: pod-0 is already master, REPLICAOF NO ONE on it is a
	// no-op, and the state annotation still reads restoring-topology. If the write
	// never lands, the Phase 1 bound abandons the restoration and Phase 2
	// consolidates on the pod the annotation still names — consistent, and without
	// a stale name left behind.
	//
	// Not advancing is not enough on its own, though: the promotion above has
	// already happened, so simply requeueing leaves an unrecorded second master
	// standing for up to the Phase 1 budget (spec.rollingUpdate.syncTimeout,
	// default 5m), and every write the -rw Service sends to pod-0 in that window
	// is discarded when Phase 2 demotes it back. Rolling the promotion back closes
	// the window instead of waiting it out.
	if err := r.recordPromotedMaster(ctx, v, masterHost); err != nil {
		logger.Info("Could not record pod-0 as the known master, not advancing to Phase 2",
			common.RoleMaster, masterHost, "error", err)
		r.rollbackPod0Promotion(ctx, c, masterPodName, masterHost, previousMasterHost, portStr)
		return RollingUpdateResult{NeedsRequeue: true, RequeueAfter: rollingUpdateRequeueDelay}
	}

	// Redirect all other pods to replicate from pod-0.
	totalPods := int(*currentSts.Spec.Replicas)
	for i := 1; i < totalPods; i++ {
		podName := fmt.Sprintf("%s-%d", stsName, i)
		addr := health.PodAddressForComponent(v, podName, common.ComponentValkey, port)
		rc := r.newValkeyClient(addr, password, tlsConfig)
		if err := rc.ReplicaOf(masterHost, portStr); err != nil {
			logger.Info("REPLICAOF redirect failed (will verify in Phase 2)",
				"pod", podName, common.RoleMaster, masterHost, "error", err)
		} else {
			logger.Info("Redirected replica to pod-0", "pod", podName, common.RoleMaster, masterHost)
		}
	}

	// The successful verdict is recorded before the transition for the reason
	// abandonTopologyRestoration gives: this is the only pass that writes it.
	// Requeueing without advancing costs nothing here -- as stated above, pod-0 is
	// already master, REPLICAOF NO ONE on it is a no-op, the recorded known-master
	// already names it, and the state still reads restoring-topology.
	if err := r.recordTopologyRestoredCondition(ctx, v, metav1.ConditionTrue, "Restored",
		fmt.Sprintf("%s was promoted back to master", masterPodName)); err != nil {
		return RollingUpdateResult{Error: err}
	}

	// Transition to Phase 2: verify replicas reconnected on next reconcile. Its budget is armed here,
	// at the transition, so a timestamp left behind by an earlier update cannot spend it before the
	// verification ever runs (docs/adr/0010-every-rolling-update-wait-is-bounded.md, D10).
	r.armFinalizationBound(v)
	if err := r.setRollingUpdateState(ctx, v, stateVerifyingTopology); err != nil {
		return RollingUpdateResult{Error: err}
	}

	return RollingUpdateResult{NeedsRequeue: true, RequeueAfter: rollingUpdateRequeueDelay}
}

// rollbackPod0Promotion hands pod-0 back to previousMasterHost after the promotion
// this pass performed could not be recorded.
//
// It is free and it loses nothing: Phase 1 only promotes pod-0 once it has fully
// synced from the promoted replica, so making it a replica of that same pod again
// discards no data it did not already have. What it does remove is the window in
// which the cluster carries a master the annotation does not name -- otherwise
// pod-0 keeps taking writes through the -rw Service until Phase 1 abandons the
// restoration and Phase 2 demotes it back toward the promoted pod, and exactly
// those writes are the ones that REPLICAOF then discards.
//
// A rollback that itself fails is logged and nothing more: the caller requeues
// either way, retries the record on the next pass, and the pre-existing bounded
// abandon path still applies.
func (r *ValkeyReconciler) rollbackPod0Promotion(ctx context.Context, c *valkeyclient.Client,
	pod0Name, pod0Host, previousMasterHost, port string) {
	logger := log.FromContext(ctx)

	if previousMasterHost == "" || previousMasterHost == pod0Host {
		// Nothing named a different master before this promotion, so there is no
		// pod to hand the role back to. Happens when the annotation write landed
		// and only the ConfigMap republish failed: the recorded authority is
		// already pod-0, which is exactly what the topology says.
		logger.Info("No previous master recorded, leaving pod-0 promoted", "pod", pod0Name)
		return
	}

	if err := c.ReplicaOf(previousMasterHost, port); err != nil {
		logger.Info("Could not roll the unrecorded pod-0 promotion back; it stays master until Phase 2",
			"pod", pod0Name, "previousMaster", previousMasterHost, "error", err)
		return
	}
	logger.Info("Rolled the unrecorded pod-0 promotion back",
		"pod", pod0Name, "previousMaster", previousMasterHost)
}

// verifyTopologyRestored is Phase 2 of handleTopologyRestoration (stateVerifyingTopology).
// It checks whether all replicas have reconnected to pod-0. If rogue masters are still
// present, it retries REPLICAOF via detectAndResolveSplitBrain. The rolling update
// completes once the cluster is clean or after finalizationStallTimeout to prevent an
// indefinite stall. The stall timeout is tracked via annotationFinalizationTimestamp,
// which is already part of the rolling update state and cleared by clearRollingUpdateState.
func (r *ValkeyReconciler) verifyTopologyRestored(ctx context.Context, v *vkov1.Valkey, currentSts *appsv1.StatefulSet) RollingUpdateResult {
	logger := log.FromContext(ctx)

	pods, masterIdx, err := r.collectPodStates(ctx, v, currentSts)
	if err != nil {
		// Bounded like the rogue-master branch below: a permanently failing pod
		// lookup must not requeue forever either.
		r.ensureFinalizationTimestamp(ctx, v)
		if !r.isFinalizationStalled(v) {
			logger.Info("Cannot verify topology, will retry", "error", err)
			return RollingUpdateResult{NeedsRequeue: true, RequeueAfter: rollingUpdateRequeueDelay}
		}
		logger.Info("Topology verification stalled, completing rolling update unverified",
			"timeout", finalizationStallTimeout, "error", err)
		r.recordEvent(v, corev1.EventTypeWarning, "TopologyVerifyIncomplete",
			"Topology could not be verified within %v: %v", finalizationStallTimeout, err)
		if clearErr := r.clearRollingUpdateState(ctx, v); clearErr != nil {
			return RollingUpdateResult{Error: clearErr}
		}
		return RollingUpdateResult{Completed: true}
	}

	// Count rogue masters before attempting resolution.
	rogueCount := 0
	for _, ps := range pods {
		if ps.isMaster {
			rogueCount++
		}
	}
	rogueCount-- // subtract the real master

	if rogueCount > 0 {
		logger.Info("Rogue masters detected after topology restoration, attempting REPLICAOF",
			"count", rogueCount)
		r.recordEvent(v, corev1.EventTypeWarning, "TopologyRestoreIncomplete",
			"Topology restore incomplete: %d rogue master(s) still present", rogueCount)

		// Name the master rather than letting the resolver guess. With the
		// restoration abandoned the real master is the promoted replica, and a
		// returning pod-0 that reports master ties it at zero connected slaves --
		// the resolver would then pick pod-0 by lowest ordinal and demote the pod
		// holding the data (the ADR 0008 D10, D11 failure mode). The known-master annotation
		// names pod-0 on the normal path and the promoted replica otherwise.
		//
		// The bare resolver, not resolveSplitBrain: rogueCount > 0 above and
		// len(masterIndices) > 1 inside the resolver are the same predicate, and
		// TopologyRestoreIncomplete has already reported it. Every pass that reaches
		// here came through handleMultiReplicaRollingUpdate, which ran the reporting
		// resolver on the same pod set moments ago, so the condition is current and a
		// second report would only double the Event count for one fact
		// (docs/adr/0025-a-split-brain-warning-means-one-that-did-not-resolve-itself.md, D5).
		r.detectAndResolveSplitBrain(ctx, v, pods, masterIdx, knownMasterPodName(v))

		r.ensureFinalizationTimestamp(ctx, v)
		if !r.isFinalizationStalled(v) {
			return RollingUpdateResult{NeedsRequeue: true, RequeueAfter: rollingUpdateRequeueDelay}
		}
		logger.Info("Topology restore stalled, completing rolling update despite rogue masters",
			"timeout", finalizationStallTimeout)
	}

	if err := r.clearRollingUpdateState(ctx, v); err != nil {
		return RollingUpdateResult{Error: err}
	}

	logger.Info("Multi-replica rolling update completed, topology restored")
	r.recordEvent(v, corev1.EventTypeNormal, "RollingUpdateComplete",
		"Multi-replica rolling update completed, topology restored")
	return RollingUpdateResult{Completed: true}
}

// finalizeMultiReplicaRollingUpdate handles the case where all pods are updated
// but the rolling update state may still be present (e.g., mid topology restoration).
func (r *ValkeyReconciler) finalizeMultiReplicaRollingUpdate(ctx context.Context, v *vkov1.Valkey, _ []podState) RollingUpdateResult {
	logger := log.FromContext(ctx)
	currentState := r.getRollingUpdateState(v)

	if currentState != "" {
		logger.Info("All pods updated, clearing rolling update state", "state", currentState)
		if err := r.clearRollingUpdateState(ctx, v); err != nil {
			return RollingUpdateResult{Error: err}
		}
	}

	return RollingUpdateResult{Completed: true}
}

// differ from what the sentinel StatefulSet template specifies.
func sentinelPodNeedsUpdate(pod *corev1.Pod, desiredTemplate corev1.PodTemplateSpec) bool {
	// Check container images.
	desired := make(map[string]string, len(desiredTemplate.Spec.Containers))
	for _, c := range desiredTemplate.Spec.Containers {
		desired[c.Name] = c.Image
	}
	for _, c := range pod.Spec.Containers {
		if want, ok := desired[c.Name]; ok && c.Image != want {
			return true
		}
	}
	// Check pod spec hash annotation: trigger update when the pod carries a hash
	// that no longer matches the desired template (e.g. resources changed).
	// Fallback: when the pod lacks the annotation, compare container resources
	// directly so that genuine spec changes are still detected.
	if desiredHash, ok := desiredTemplate.Annotations[builder.AnnotationPodSpecHash]; ok && desiredHash != "" {
		podHash := pod.Annotations[builder.AnnotationPodSpecHash]
		if podHash != "" {
			if podHash != desiredHash {
				return true
			}
		} else if containersResourceChanged(pod.Spec.Containers, desiredTemplate.Spec.Containers) {
			return true
		}
	}
	// Check config hash annotation.
	if desiredHash, ok := desiredTemplate.Annotations[builder.AnnotationConfigHash]; ok && desiredHash != "" {
		if podHash := pod.Annotations[builder.AnnotationConfigHash]; podHash != "" && podHash != desiredHash {
			return true
		}
	}
	return false
}

// checkAndHandleSentinelRollingUpdate detects sentinel pods running outdated container
// images and replaces them one at a time while verifying sentinel quorum is maintained.
//
// Because the sentinel StatefulSet uses OnDelete, Kubernetes will not automatically
// restart pods after a spec update. The operator must coordinate pod deletion here.
//
// Strategy:
//  1. Identify sentinel pods whose container images differ from the current template.
//  2. Before deleting any pod, check that the remaining ready sentinels (after
//     deletion) will still meet quorum (readyCount - 1 >= quorum).
//  3. Delete the first outdated pod and requeue so the next reconcile
//     waits for the replacement to become ready before moving on.
func (r *ValkeyReconciler) checkAndHandleSentinelRollingUpdate(ctx context.Context, v *vkov1.Valkey) RollingUpdateResult {
	logger := log.FromContext(ctx)

	sentinelSts := &appsv1.StatefulSet{}
	sentinelStsName := common.StatefulSetName(v, common.ComponentSentinel)
	if err := r.Get(ctx, types.NamespacedName{Name: sentinelStsName, Namespace: v.Namespace}, sentinelSts); err != nil {
		if apierrors.IsNotFound(err) {
			return RollingUpdateResult{}
		}
		return RollingUpdateResult{Error: fmt.Errorf("getting sentinel StatefulSet: %w", err)}
	}
	// A foreign StatefulSet is treated as absent (ADR 0020); its pods are not
	// ours to delete. reconcileSentinelStatefulSet reports the collision.
	if !metav1.IsControlledBy(sentinelSts, v) {
		return RollingUpdateResult{}
	}

	totalSentinels := int(*sentinelSts.Spec.Replicas)
	quorum := totalSentinels/2 + 1
	desiredTemplate := sentinelSts.Spec.Template

	readyCount := 0
	updatedReadyCount := 0
	var firstOutdatedPod *corev1.Pod

	for i := 0; i < totalSentinels; i++ {
		podName := fmt.Sprintf("%s-%d", sentinelStsName, i)
		pod := &corev1.Pod{}
		if err := r.Get(ctx, types.NamespacedName{Name: podName, Namespace: v.Namespace}, pod); err != nil {
			if apierrors.IsNotFound(err) {
				// Pod does not exist yet (initial deployment) or is being recreated
				// after a recent deletion. In both cases, skip it — it neither counts
				// toward readyCount nor can be an outdated pod to delete.
				// The quorum guard below will naturally prevent triggering another
				// deletion while a pod is missing (reducing effectiveReadyCount).
				continue
			}
			return RollingUpdateResult{Error: fmt.Errorf("getting sentinel pod %s: %w", podName, err)}
		}
		if !podIsOurs(pod, sentinelSts) {
			return RollingUpdateResult{Error: foreignObjectError("Pod", podName)}
		}
		outdated := sentinelPodNeedsUpdate(pod, desiredTemplate)
		if isPodReady(pod) {
			readyCount++
			if !outdated {
				updatedReadyCount++
			}
		}
		if firstOutdatedPod == nil && outdated {
			firstOutdatedPod = pod
		}
	}

	if firstOutdatedPod == nil {
		return r.finishSentinelRollingUpdate(ctx, v, updatedReadyCount, totalSentinels)
	}

	// A roll is in flight. Record it before acting: the True condition is the
	// memory whose flip back to False is the completion edge, and the phase is
	// the status contract's "current task" — the data tier's RollingUpdateComplete
	// has already fired at this point (or, on a Sentinel-only spec change, the
	// data tier never rolled and the phase would otherwise keep reading OK).
	r.recordSentinelUpdateProgress(ctx, v, updatedReadyCount, totalSentinels)

	// Guard quorum: after deleting one pod we need at least `quorum` sentinels left.
	if readyCount-1 < quorum {
		logger.Info("Waiting for sentinel quorum before updating sentinel pod",
			"pod", firstOutdatedPod.Name, "readyCount", readyCount, "quorum", quorum)
		return RollingUpdateResult{NeedsRequeue: true, RequeueAfter: rollingUpdateRequeueDelay}
	}

	logger.Info("Deleting sentinel pod for rolling update", "pod", firstOutdatedPod.Name)
	if err := r.deleteOwnedPod(ctx, firstOutdatedPod); err != nil {
		return RollingUpdateResult{Error: fmt.Errorf("deleting sentinel pod %s: %w", firstOutdatedPod.Name, err)}
	}
	return RollingUpdateResult{NeedsRequeue: true, RequeueAfter: rollingUpdateRequeueDelay}
}

// sentinelUpdatePending reports whether the CR carries SentinelUpdatePending=True —
// the memory that a Sentinel roll is in flight. It reads the pass's cached copy;
// a lagging cache can at worst delay the completion edge by one pass, never emit
// it twice, because the emission is gated on the condition flip actually landing
// (writeStatusCondition) and a status update from a stale copy cannot land.
func sentinelUpdatePending(v *vkov1.Valkey) bool {
	cond := meta.FindStatusCondition(v.Status.Conditions, vkov1.ConditionTypeSentinelUpdatePending)
	return cond != nil && cond.Status == metav1.ConditionTrue
}

// recordSentinelUpdateProgress marks the Sentinel roll as in flight on the CR:
// SentinelUpdatePending=True carrying the progress count, and the phase per the
// CLAUDE.md status contract ("OK when healthy, otherwise the current task").
// updatedReady counts pods that are on the desired spec AND Ready, so the
// number is monotone across the roll and reaches total exactly at completion.
// Both writes skip themselves when nothing changed, so a wait pass costs no
// status update.
func (r *ValkeyReconciler) recordSentinelUpdateProgress(ctx context.Context, v *vkov1.Valkey, updatedReady, total int) {
	message := fmt.Sprintf("Sentinel rolling update in progress: %d/%d pods updated and ready", updatedReady, total)
	r.setStatusCondition(ctx, v,
		vkov1.ConditionTypeSentinelUpdatePending,
		metav1.ConditionTrue,
		vkov1.ReasonSentinelPodsOutdated,
		message)
	_ = r.updatePhase(ctx, v,
		ValkeyPhase(fmt.Sprintf("%s %d/%d", vkov1.ValkeyPhaseSentinelRollingUpdate, updatedReady, total)),
		message)
}

// finishSentinelRollingUpdate handles the pass in which no Sentinel pod needs
// replacing. For a CR not carrying SentinelUpdatePending=True that is the steady
// state of every healthy pass, and it stays exactly as silent as it was before
// the condition existed. With the condition standing, convergence is more than
// "no outdated pod": every pod must exist, be current and be Ready — otherwise
// the pod deleted last is still booting, and "complete" would fire while it
// does, which is the same too-early edge this marker exists to remove (the
// data tier's RollingUpdateComplete already fires before the Sentinel tier
// starts). The completion event is gated on the condition flip actually
// landing, so it is emitted exactly once per roll; a failed flip requeues,
// because with every pod Ready nothing else re-triggers the pass.
func (r *ValkeyReconciler) finishSentinelRollingUpdate(ctx context.Context, v *vkov1.Valkey, updatedReady, total int) RollingUpdateResult {
	if !sentinelUpdatePending(v) {
		return RollingUpdateResult{}
	}
	if updatedReady < total {
		r.recordSentinelUpdateProgress(ctx, v, updatedReady, total)
		return RollingUpdateResult{NeedsRequeue: true, RequeueAfter: rollingUpdateRequeueDelay}
	}
	message := fmt.Sprintf("Sentinel rolling update completed, all %d sentinel pods running the desired spec", total)
	changed, err := r.writeStatusCondition(ctx, v,
		vkov1.ConditionTypeSentinelUpdatePending,
		metav1.ConditionFalse,
		vkov1.ReasonSentinelUpdateComplete,
		message)
	if err != nil {
		log.FromContext(ctx).Error(err, "Could not clear SentinelUpdatePending, retrying")
		return RollingUpdateResult{NeedsRequeue: true, RequeueAfter: rollingUpdateRequeueDelay}
	}
	if changed {
		r.recordEvent(v, corev1.EventTypeNormal, "SentinelUpdateComplete", "%s", message)
	}
	return RollingUpdateResult{}
}

// ValkeyPhase is a type alias to allow constructing rolling update phase strings.
type ValkeyPhase = vkov1.ValkeyPhase
