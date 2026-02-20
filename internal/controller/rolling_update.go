// Package controller implements the Kubernetes reconciliation logic
// for Valkey custom resources.
package controller

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/log"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/builder"
	"github.com/guided-traffic/valkey-operator/internal/common"
	"github.com/guided-traffic/valkey-operator/internal/health"
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
)

// failoverRetryTimeout is the duration after which a sentinel failover is considered
// stale and will be retried with a sentinel reset. This handles the case where
// sentinel refuses a failover due to its internal cooldown (failover-timeout).
const failoverRetryTimeout = 30 * time.Second

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

	// Check if any pods are running a different image than desired.
	desiredImage := v.Spec.Image
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

		if podNeedsUpdate(pod, desiredImage) {
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
			return RollingUpdateResult{} // No rolling update needed.
		}
		logger.Info("All pods updated but rolling update state still present, finalizing")
	} else {
		logger.Info("Rolling update detected", "desiredImage", desiredImage)
	}

	if v.IsSentinelEnabled() {
		return r.handleRollingUpdate(ctx, v, currentSts)
	}
	return r.handleStandaloneRollingUpdate(ctx, v, currentSts)
}

// detectImageChange returns true if the StatefulSet's current image differs from the desired image.
func detectImageChange(desired string, current *appsv1.StatefulSet) bool {
	if len(current.Spec.Template.Spec.Containers) == 0 {
		return false
	}
	return current.Spec.Template.Spec.Containers[0].Image != desired
}

// podNeedsUpdate returns true if the pod's container image does not match the desired image.
func podNeedsUpdate(pod *corev1.Pod, desiredImage string) bool {
	if len(pod.Spec.Containers) == 0 {
		return false
	}
	return pod.Spec.Containers[0].Image != desiredImage
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
	currentState, err = r.clearStaleRollingUpdateState(ctx, v, currentState, updatedCount)
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
// If no pods have the desired image yet (updatedCount == 0) but the state machine
// is past the replica-replacement phase, the state annotations are left over from
// a prior rolling update. Clear the stale state so the new rolling update starts
// fresh from replica replacement.
func (r *ValkeyReconciler) clearStaleRollingUpdateState(ctx context.Context, v *vkov1.Valkey, currentState string, updatedCount int) (string, error) {
	if updatedCount == 0 && currentState != "" && currentState != stateReplacingReplicas {
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
		masterCount := countMasters(pods)
		if masterCount != 1 {
			logger.Info("Rolling update: waiting for stable cluster topology",
				"masterCount", masterCount)
			return RollingUpdateResult{NeedsRequeue: true, RequeueAfter: rollingUpdateRequeueDelay}
		}

		// Wait for all replicas to be connected to the master before syncing sentinel.
		// The sentinel reset (REMOVE + MONITOR) is destructive and can temporarily
		// disrupt replica connections. We must ensure the cluster is fully stable
		// (all replicas connected and synced) before performing the reset.
		checker := health.NewChecker(r.Client)
		expectedReplicas := len(pods) - 1
		for _, ps := range pods {
			if ps.isMaster {
				info, err := checker.GetReplicationInfo(ctx, v, ps.name)
				if err != nil {
					logger.Info("Cannot verify replication before sentinel sync, waiting",
						"master", ps.name, "error", err)
					return RollingUpdateResult{NeedsRequeue: true, RequeueAfter: rollingUpdateRequeueDelay}
				}
				if info.ConnectedSlaves < expectedReplicas {
					logger.Info("Waiting for all replicas to connect before sentinel sync",
						"master", ps.name, "connectedSlaves", info.ConnectedSlaves,
						"expected", expectedReplicas)
					return RollingUpdateResult{NeedsRequeue: true, RequeueAfter: rollingUpdateRequeueDelay}
				}

				headlessName := common.HeadlessServiceName(v, common.ComponentValkey)
				masterAddr := fmt.Sprintf("%s.%s.%s.svc.cluster.local", ps.name, headlessName, v.Namespace)
				logger.Info("Syncing sentinel with current master before finalization",
					"master", ps.name, "masterAddr", masterAddr,
					"connectedSlaves", info.ConnectedSlaves)
				r.resetSentinelState(ctx, v, masterAddr)
				break
			}
		}
	}

	logger.Info("Rolling update complete, all pods running new image")
	// Clean up state annotation.
	if err := r.clearRollingUpdateState(ctx, v); err != nil {
		return RollingUpdateResult{Error: err}
	}
	return RollingUpdateResult{Completed: true}
}

// countMasters returns the number of pods with the master role.
func countMasters(pods []podState) int {
	count := 0
	for _, ps := range pods {
		if ps.isMaster {
			count++
		}
	}
	return count
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
	desiredImage := v.Spec.Image
	stsName := common.StatefulSetName(v, common.ComponentValkey)
	totalPods := int(*currentSts.Spec.Replicas)
	checker := health.NewChecker(r.Client)

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
		} else {
			ps.pod = pod
			ps.exists = true
			ps.needsUpdate = podNeedsUpdate(pod, desiredImage)
			ps.ready = isPodReady(pod)

			// Determine if this pod is the master.
			info, infoErr := checker.GetReplicationInfo(ctx, v, podName)
			if infoErr == nil && info.Role == common.RoleMaster {
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

// replaceNextReplica finds the next replica pod that needs updating and deletes it.
// Returns nil if no replica needs replacement (all replicas are done).
func (r *ValkeyReconciler) replaceNextReplica(ctx context.Context, v *vkov1.Valkey, pods []podState) *RollingUpdateResult {
	logger := log.FromContext(ctx)

	for i, ps := range pods {
		if !ps.needsUpdate || ps.isMaster {
			continue
		}

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

		logger.Info("Deleting replica pod for rolling update", "pod", ps.name, "ordinal", i)
		if err := r.Delete(ctx, ps.pod); err != nil {
			return &RollingUpdateResult{Error: fmt.Errorf("deleting pod %s: %w", ps.name, err)}
		}
		return &RollingUpdateResult{NeedsRequeue: true, RequeueAfter: rollingUpdateRequeueDelay}
	}

	return nil
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

	// Set state BEFORE triggering failover to prevent concurrent reconciles
	// from also triggering failover.
	if err := r.setRollingUpdateState(ctx, v, stateFailoverTriggered); err != nil {
		return &RollingUpdateResult{Error: err}
	}

	// Record when the failover was triggered so we can detect stale failovers.
	if err := r.setFailoverTimestamp(ctx, v); err != nil {
		return &RollingUpdateResult{Error: err}
	}

	// Optimistic failover: try directly without resetting sentinel first.
	// In the common case (first rolling update) there is no cooldown and
	// failover succeeds immediately, preserving HA without interruption.
	// If sentinel has a cooldown from a previous failover, the failover
	// will silently fail and the retry path in handlePostFailover will
	// reset sentinel and retrigger after a delay.
	_ = r.updatePhase(ctx, v, vkov1.ValkeyPhaseFailover, "Triggering Sentinel failover before updating master pod")

	if err := r.triggerSentinelFailover(ctx, v); err != nil {
		logger.Info("Sentinel failover command failed, will retry via post-failover handler", "error", err)
	} else {
		logger.Info("Sentinel failover triggered, waiting for completion")
	}

	return &RollingUpdateResult{NeedsRequeue: true, RequeueAfter: 15 * time.Second}
}

// waitForReplicasReady verifies all non-master replicas are ready and have completed
// replication sync. Returns a requeue result if any replica is not ready.
func (r *ValkeyReconciler) waitForReplicasReady(ctx context.Context, v *vkov1.Valkey, pods []podState, masterIdx int) *RollingUpdateResult {
	logger := log.FromContext(ctx)
	checker := health.NewChecker(r.Client)

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
			return &RollingUpdateResult{NeedsRequeue: true, RequeueAfter: rollingUpdateRequeueDelay}
		}
		if info.MasterSyncInProgress {
			logger.Info("Replication sync still in progress, waiting", "pod", ps.name)
			return &RollingUpdateResult{NeedsRequeue: true, RequeueAfter: rollingUpdateRequeueDelay}
		}
	}

	return nil
}

// waitWriteSyncTimeout is the timeout in milliseconds for the WAIT command.
// Replicas should already be synced at this point, so 5 seconds is generous.
const waitWriteSyncTimeout = 5000

// waitForWriteSync sends a WAIT command to the master to ensure all pending writes
// have been acknowledged by all replicas before failover. This prevents data loss
// that can occur during async replication when a failover happens.
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

	c := r.newValkeyClient(addr, tlsConfig)
	acked, err := c.Wait(numReplicas, waitWriteSyncTimeout)
	if err != nil {
		logger.Info("WAIT command failed, will retry", "master", masterPod.name, "error", err)
		return &RollingUpdateResult{NeedsRequeue: true, RequeueAfter: rollingUpdateRequeueDelay}
	}

	if acked < numReplicas {
		logger.Info("Not all replicas acknowledged writes, will retry",
			"master", masterPod.name, "expected", numReplicas, "acked", acked)
		return &RollingUpdateResult{NeedsRequeue: true, RequeueAfter: rollingUpdateRequeueDelay}
	}

	logger.Info("All replicas acknowledged pending writes",
		"master", masterPod.name, "acked", acked)
	return nil
}

// replaceRemainingPods finds and replaces any remaining pods with the old image.
// Before deleting the former master, it verifies that a new master exists,
// has completed replication sync, and has actual data (DBSIZE > 0) to prevent data loss.
func (r *ValkeyReconciler) replaceRemainingPods(ctx context.Context, v *vkov1.Valkey, pods []podState) RollingUpdateResult {
	logger := log.FromContext(ctx)
	checker := health.NewChecker(r.Client)

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
		if err := r.Delete(ctx, ps.pod); err != nil {
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
	logger := log.FromContext(ctx)
	checker := health.NewChecker(r.Client)

	// Re-collect pod states to get fresh role information.
	// After a failover, roles change and we must not rely on stale data.
	currentSts := &appsv1.StatefulSet{}
	stsName := common.StatefulSetName(v, common.ComponentValkey)
	if err := r.Get(ctx, types.NamespacedName{Name: stsName, Namespace: v.Namespace}, currentSts); err != nil {
		return RollingUpdateResult{Error: fmt.Errorf("getting StatefulSet in post-failover: %w", err)}
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
			// New master found. Check if it has connected replicas.
			if info.ConnectedSlaves == 0 {
				logger.Info("New master has no connected replicas yet, waiting for sync",
					"newMaster", ps.name)
				return RollingUpdateResult{NeedsRequeue: true, RequeueAfter: rollingUpdateRequeueDelay}
			}

			// New master is ready. Proceed to replace the old master.
			logger.Info("New master is ready with connected replicas",
				"newMaster", ps.name, "connectedSlaves", info.ConnectedSlaves)
			return r.replaceRemainingPods(ctx, v, freshPods)
		}
	}

	// No new master found yet. Check if we've exceeded the failover retry timeout.
	if r.isFailoverTimedOut(v) {
		logger.Info("Failover timed out, resetting sentinel state and scheduling retry")

		// Determine the correct master address from the pods we already know about.
		// Since handlePostFailover didn't find a new-image master, use the old master
		// (which has the old image and still reports as master to its local INFO).
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

	// Failover still in progress, wait.
	logger.Info("Waiting for failover to complete, no new master detected yet")
	return RollingUpdateResult{NeedsRequeue: true, RequeueAfter: rollingUpdateRequeueDelay}
}

// verifyNewMasterReady verifies that a new-image master exists and has
// connected replicas before we delete the old master pod.
// Returns (true, _) if verified, (false, result) if we need to wait.
func (r *ValkeyReconciler) verifyNewMasterReady(ctx context.Context, v *vkov1.Valkey, pods []podState, checker *health.Checker) (bool, RollingUpdateResult) {
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
			vc := r.newValkeyClient(addr, tlsConfig)
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

// clearRollingUpdateState removes the rolling update state and failover timestamp annotations.
func (r *ValkeyReconciler) clearRollingUpdateState(ctx context.Context, v *vkov1.Valkey) error {
	if v.Annotations == nil {
		return nil
	}
	_, hasState := v.Annotations[annotationRollingUpdateState]
	_, hasTimestamp := v.Annotations[annotationFailoverTimestamp]
	if !hasState && !hasTimestamp {
		return nil
	}
	delete(v.Annotations, annotationRollingUpdateState)
	delete(v.Annotations, annotationFailoverTimestamp)
	return r.Update(ctx, v)
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
	if v.Annotations == nil {
		return false
	}
	tsStr, ok := v.Annotations[annotationFailoverTimestamp]
	if !ok || tsStr == "" {
		return false
	}
	ts, err := time.Parse(time.RFC3339, tsStr)
	if err != nil {
		return true // Corrupted timestamp — treat as timed out to recover.
	}
	return time.Since(ts) > failoverRetryTimeout
}

// hasMinWaitElapsed checks whether at least failoverResetMinWait has elapsed
// since the failover timestamp. Used to prevent retriggering failover too
// quickly after a SENTINEL RESET, giving sentinel time to rediscover replicas.
func (r *ValkeyReconciler) hasMinWaitElapsed(v *vkov1.Valkey) bool {
	if v.Annotations == nil {
		return true
	}
	tsStr, ok := v.Annotations[annotationFailoverTimestamp]
	if !ok || tsStr == "" {
		return true
	}
	ts, err := time.Parse(time.RFC3339, tsStr)
	if err != nil {
		return true // Corrupted timestamp — allow to proceed.
	}
	return time.Since(ts) >= failoverResetMinWait
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

	for i := int32(0); i < sentinelReplicas; i++ {
		podName := fmt.Sprintf("%s-%d", sentinelStsName, i)
		addr := health.PodAddressForComponent(v, podName, common.ComponentSentinel, builder.SentinelPort)

		tlsConfig, tlsErr := r.buildTLSConfig(ctx, v, builder.SentinelTLSSecretName(v))
		if tlsErr != nil {
			logger.V(1).Info("Could not build TLS config for sentinel reconfig", "error", tlsErr)
			continue
		}

		c := r.newValkeyClient(addr, tlsConfig)

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

	// Try each sentinel until one successfully triggers failover.
	var lastErr error
	for i := int32(0); i < sentinelReplicas; i++ {
		podName := fmt.Sprintf("%s-%d", sentinelStsName, i)
		addr := health.PodAddressForComponent(v, podName, common.ComponentSentinel, builder.SentinelPort)

		tlsConfig, tlsErr := r.buildTLSConfig(ctx, v, builder.SentinelTLSSecretName(v))
		if tlsErr != nil {
			lastErr = tlsErr
			logger.V(1).Info("Could not build TLS config for sentinel failover", "error", tlsErr)
			continue
		}

		c := r.newValkeyClient(addr, tlsConfig)
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

// handleStandaloneRollingUpdate handles rolling update for standalone (non-HA) mode.
// For standalone, we simply delete the pod and let StatefulSet recreate it with the new template.
func (r *ValkeyReconciler) handleStandaloneRollingUpdate(ctx context.Context, v *vkov1.Valkey, currentSts *appsv1.StatefulSet) RollingUpdateResult {
	logger := log.FromContext(ctx)
	desiredImage := v.Spec.Image
	stsName := common.StatefulSetName(v, common.ComponentValkey)

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

		if podNeedsUpdate(pod, desiredImage) {
			if !isPodReady(pod) {
				logger.Info("Pod not ready, waiting", "pod", podName)
				return RollingUpdateResult{NeedsRequeue: true, RequeueAfter: rollingUpdateRequeueDelay}
			}

			_ = r.updatePhase(ctx, v, ValkeyPhase(fmt.Sprintf("%s %d/%d", vkov1.ValkeyPhaseRollingUpdate, 0, *currentSts.Spec.Replicas)),
				fmt.Sprintf("Replacing pod %s with new image", podName))

			logger.Info("Deleting pod for standalone rolling update", "pod", podName)
			if err := r.Delete(ctx, pod); err != nil {
				return RollingUpdateResult{Error: fmt.Errorf("deleting pod %s: %w", podName, err)}
			}
			return RollingUpdateResult{NeedsRequeue: true, RequeueAfter: rollingUpdateRequeueDelay}
		}

		if !isPodReady(pod) {
			logger.Info("Updated pod not yet ready", "pod", podName)
			return RollingUpdateResult{NeedsRequeue: true, RequeueAfter: rollingUpdateRequeueDelay}
		}
	}

	// All pods updated and ready.
	return RollingUpdateResult{Completed: true}
}

// ValkeyPhase is a type alias to allow constructing rolling update phase strings.
type ValkeyPhase = vkov1.ValkeyPhase
