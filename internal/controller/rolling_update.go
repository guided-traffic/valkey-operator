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
	"github.com/guided-traffic/valkey-operator/internal/valkeyclient"
)

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
		return RollingUpdateResult{} // No rolling update needed.
	}

	logger.Info("Rolling update detected", "desiredImage", desiredImage)

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

	// If all pods are updated and ready, the rolling update is complete.
	if updatedCount == totalPods {
		logger.Info("Rolling update complete, all pods running new image")
		return RollingUpdateResult{Completed: true}
	}

	// Update status with progress.
	phase := fmt.Sprintf("%s %d/%d", vkov1.ValkeyPhaseRollingUpdate, updatedCount, totalPods)
	_ = r.updatePhase(ctx, v, ValkeyPhase(phase), fmt.Sprintf("Rolling update in progress: %d/%d pods updated", updatedCount, totalPods))

	// Step 1: Replace replica pods first (not the master).
	if result := r.replaceNextReplica(ctx, pods); result != nil {
		return *result
	}

	// Step 2: All replicas are updated. Now handle the master failover and replacement.
	if result := r.handleMasterFailover(ctx, v, pods, masterIdx); result != nil {
		return *result
	}

	// Step 3: Replace any remaining pods with old image.
	return r.replaceRemainingPods(ctx, pods)
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
			if infoErr == nil && info.Role == "master" {
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
func (r *ValkeyReconciler) replaceNextReplica(ctx context.Context, pods []podState) *RollingUpdateResult {
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
// Returns nil if the master does not need updating.
func (r *ValkeyReconciler) handleMasterFailover(ctx context.Context, v *vkov1.Valkey, pods []podState, masterIdx int) *RollingUpdateResult {
	logger := log.FromContext(ctx)

	if masterIdx < 0 || !pods[masterIdx].needsUpdate {
		return nil
	}

	// Check if all non-master pods are ready and synced before doing failover.
	if result := r.waitForReplicasReady(ctx, v, pods, masterIdx); result != nil {
		return result
	}

	// Trigger a Sentinel failover to move master to a new-image pod.
	_ = r.updatePhase(ctx, v, vkov1.ValkeyPhaseFailover, "Triggering Sentinel failover before updating master pod")

	if err := r.triggerSentinelFailover(ctx, v); err != nil {
		logger.Info("Sentinel failover failed, will retry", "error", err)
		return &RollingUpdateResult{NeedsRequeue: true, RequeueAfter: rollingUpdateRequeueDelay}
	}

	logger.Info("Sentinel failover triggered, waiting for completion")
	return &RollingUpdateResult{NeedsRequeue: true, RequeueAfter: rollingUpdateRequeueDelay}
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

// replaceRemainingPods finds and replaces any remaining pods with the old image.
func (r *ValkeyReconciler) replaceRemainingPods(ctx context.Context, pods []podState) RollingUpdateResult {
	logger := log.FromContext(ctx)

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

		logger.Info("Deleting remaining pod for rolling update", "pod", ps.name)
		if err := r.Delete(ctx, ps.pod); err != nil {
			return RollingUpdateResult{Error: fmt.Errorf("deleting pod %s: %w", ps.name, err)}
		}
		return RollingUpdateResult{NeedsRequeue: true, RequeueAfter: rollingUpdateRequeueDelay}
	}

	// Should not reach here, but requeue to be safe.
	return RollingUpdateResult{NeedsRequeue: true, RequeueAfter: rollingUpdateRequeueDelay}
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

		c := valkeyclient.New(addr)
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
