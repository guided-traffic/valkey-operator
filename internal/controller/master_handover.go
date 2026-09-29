package controller

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/common"
	"github.com/guided-traffic/valkey-operator/internal/valkeyclient"
)

// This file is the gate in front of the one irreversible step of a Sentinel data-tier
// roll: the delete of the outgoing master
// (docs/adr/0037-the-master-handover-loses-no-acknowledged-write-and-no-dataset.md).
//
// Before that delete three things have to be true, and each is asked of the pod that
// can answer it:
//
//   - every current replica holds the dataset and is on the new master (D3):
//     the replica's own replication answer, and the new master's connected_slaves;
//   - deleting the outgoing pod discards no dataset (D5): the ADR 0028 veto,
//     demotionRefusalReason, pointed at the delete;
//   - the outgoing pod no longer answers role:master (D5).
//
// A refusal is a wait, never a failure, and never handed to pauseRollingUpdate: that
// clears the rolling-update state, which ADR 0010 forbids as the target of an expiry,
// and releases the Sentinel roll onto the spec the data tier is stuck on. The wait is
// clocked by its own bound and past spec.rollingUpdate.syncTimeout it is
// reported as MasterHandoverStalled in the shape of terminationWait (D6): the state is
// kept, the pass continues to the recovery checks and the status write, and the
// Sentinel roll stays held.

// handoverRefusal is why the handover gate holds the outgoing pod's delete: the
// MasterHandoverStalled reason it maps to and a message naming the pods involved.
type handoverRefusal struct {
	reason  string
	message string
}

// gateOutgoingPodDelete returns nil when outgoing may be deleted, and the result the
// pass returns otherwise.
//
// Every check holds the delete; the order decides only which reason is reported. The
// dataset veto is asked first because its reason is the one that carries the repair,
// and it is the one nothing else on the CR shows: in the shape the REPLICAOF veto leaves
// (D4) the new master has no replica attached, so the replica check refuses too, and
// reporting that alone told the reader to wait for a sync that is never coming.
func (r *ValkeyReconciler) gateOutgoingPodDelete(ctx context.Context, v *vkov1.Valkey,
	pods []podState, outgoing podState, checker InstanceChecker) *RollingUpdateResult {
	keys := keyCountsOnce(r.dbSizeReader(ctx, v))

	newMaster, info, notOnNewMaster, result := r.verifyNewMasterReady(ctx, v, pods, checker)
	if newMaster == nil {
		return result
	}
	refusal := datasetRefusal(keys, *newMaster, outgoing)
	if refusal == nil {
		refusal = notOnNewMaster
	}
	if refusal == nil {
		refusal = stillMasterRefusal(ctx, v, checker, *newMaster, outgoing)
	}
	if refusal != nil {
		return r.holdHandover(ctx, v, outgoing.name, *refusal)
	}

	dbsize, _ := readKeyCount(keys, newMaster.name) // read and remembered by the veto
	log.FromContext(ctx).Info("New master verified",
		"newMaster", newMaster.name, "dbsize", dbsize, "connectedSlaves", info.ConnectedSlaves,
		"outgoing", outgoing.name)
	return nil
}

// verifyNewMasterReady finds the new master and asks whether every current replica
// holds the dataset and is on it (D3). It returns that master with its replication
// answer and the refusal, if any; while there is no new master to verify, it returns
// nil and the result the pass returns.
//
// Neither signal alone proves the handover. The new master's connected_slaves proves
// the attachment -- a replica chained through an outgoing pod that still answers
// master passes the predicate, Sentinel reconfigures with parallel-syncs 1 -- and each
// replica's own answer proves the dataset, because connected_slaves counts a replica
// from its sync request on. master_host is not compared: the init script writes two
// FQDN forms.
func (r *ValkeyReconciler) verifyNewMasterReady(ctx context.Context, v *vkov1.Valkey, pods []podState,
	checker InstanceChecker) (*podState, *valkeyclient.ReplicationInfo, *handoverRefusal, *RollingUpdateResult) {
	newMaster, info, result := r.currentMaster(ctx, v, pods, checker)
	if newMaster == nil {
		return nil, nil, nil, result
	}
	return newMaster, info, replicasNotOnNewMaster(ctx, v, pods, *newMaster, info, checker), nil
}

// currentMaster returns the first current, available pod answering role:master and
// its replication answer, or nil and the result to return: the termination wait when
// the only candidate is being deleted, a plain requeue while no current pod answers
// master yet.
func (r *ValkeyReconciler) currentMaster(ctx context.Context, v *vkov1.Valkey, pods []podState,
	checker InstanceChecker) (*podState, *valkeyclient.ReplicationInfo, *RollingUpdateResult) {
	var skippedTerminating terminatingPod
	for i := range pods {
		candidate := pods[i]
		// available(): this is the gate in front of the old master's delete, so a
		// pod that is itself being deleted must not be accepted as the new master
		// (docs/adr/0026-a-pod-being-deleted-is-not-available.md, D1).
		if candidate.needsUpdate || !candidate.available() {
			if candidate.terminating && skippedTerminating.name == "" {
				skippedTerminating = terminatingPod{name: candidate.name, since: candidate.terminatingSince}
			}
			continue
		}
		info, err := checker.GetReplicationInfo(ctx, v, candidate.name)
		if err == nil && info.Role == common.RoleMaster {
			return &candidate, info, nil
		}
	}

	if skippedTerminating.name != "" {
		// The only candidate was on its way out. Report it through the same bounded
		// observation as every other termination wait (ADR 0026 D5).
		return nil, nil, r.terminationWait(ctx, v, common.ComponentValkey, skippedTerminating,
			"Waiting for a terminating pod before verifying the new master")
	}

	log.FromContext(ctx).Info("No new-image master found yet, waiting for failover to complete")
	return nil, nil, &RollingUpdateResult{NeedsRequeue: true, RequeueAfter: rollingUpdateRequeueDelay}
}

// replicasNotOnNewMaster asks every other pod on the current template that exists and
// is not terminating for its own replication answer, and the new master for at least
// that many attached replicas -- at least one, so a tier of two keeps its refusal on
// connected_slaves == 0: there the outgoing pod is the new master's only replica once
// Sentinel has converted it, and no other pod is asked.
func replicasNotOnNewMaster(ctx context.Context, v *vkov1.Valkey, pods []podState, newMaster podState,
	info *valkeyclient.ReplicationInfo, checker InstanceChecker) *handoverRefusal {
	current := 0
	for _, ps := range pods {
		if ps.name == newMaster.name || ps.needsUpdate || !ps.exists || ps.terminating {
			continue
		}
		current++
		replicaInfo, err := checker.GetReplicationInfo(ctx, v, ps.name)
		if err != nil {
			return &handoverRefusal{
				reason:  vkov1.ReasonReplicaNotSynced,
				message: fmt.Sprintf("the replication status of %s is unavailable: %v", ps.name, err),
			}
		}
		if reason := replicationNotEstablishedReason(ps.name, replicaInfo); reason != "" {
			return &handoverRefusal{reason: vkov1.ReasonReplicaNotSynced, message: reason}
		}
	}

	want := max(current, 1)
	if info.ConnectedSlaves < want {
		return &handoverRefusal{
			reason: vkov1.ReasonReplicaNotSynced,
			message: fmt.Sprintf("the new master %s has %d replicas attached, %d expected",
				newMaster.name, info.ConnectedSlaves, want),
		}
	}
	return nil
}

// datasetRefusal is the first precondition on the pod about to be deleted (D5): the
// ADR 0028 veto pointed at the delete. The new master with keys, or both empty, may
// delete; an empty new master next to a pod holding keys, or either count unreadable,
// holds. It reads the new master's count, which the gate's log line reuses.
func datasetRefusal(keys func(string) (int, error), newMaster, outgoing podState) *handoverRefusal {
	if reason := demotionRefusalReason(keys, newMaster, outgoing); reason != "" {
		return &handoverRefusal{
			reason:  vkov1.ReasonDatasetWouldBeDiscarded,
			message: fmt.Sprintf("deleting %s would discard the only dataset: %s", outgoing.name, reason),
		}
	}
	return nil
}

// stillMasterRefusal is the second precondition (D5): the pod about to be deleted no
// longer answers role:master. A full new master does not veto a pod that still answers
// master: on the forced path the outgoing master keeps answering master until
// Sentinel converts it, and deleting it then is what used to let its drain handler
// fail over a second time. A pod that does not answer is not a master and passes --
// the dataset veto has already decided on its count.
func stillMasterRefusal(ctx context.Context, v *vkov1.Valkey, checker InstanceChecker,
	newMaster, outgoing podState) *handoverRefusal {
	info, err := checker.GetReplicationInfo(ctx, v, outgoing.name)
	if err == nil && info.Role == common.RoleMaster {
		return &handoverRefusal{
			reason:  vkov1.ReasonFormerMasterStillMaster,
			message: fmt.Sprintf("%s still answers role:master while %s is the new master", outgoing.name, newMaster.name),
		}
	}
	return nil
}

// holdHandover is the wait of the handover gate (D6). Inside syncTimeout, measured by
// the hold's own bound (annotationHandoverHoldStarted), it is a plain requeue. Past it, the wait
// is reported as MasterHandoverStalled and the pass continues: DeferredRequeueAfter
// keeps the rolling-update state and the Sentinel roll held, exactly as
// terminationWait does.
func (r *ValkeyReconciler) holdHandover(ctx context.Context, v *vkov1.Valkey, outgoing string,
	refusal handoverRefusal) *RollingUpdateResult {
	log.FromContext(ctx).Info("Holding the delete of the outgoing pod",
		"pod", outgoing, "reason", refusal.reason, "detail", refusal.message)
	r.ensureWaitBound(ctx, v, annotationHandoverHoldStarted, boundHandoverHold)
	if !r.waitBoundExceeded(v, annotationHandoverHoldStarted, boundHandoverHold, v.GetSyncTimeout()) {
		return &RollingUpdateResult{NeedsRequeue: true, RequeueAfter: rollingUpdateRequeueDelay}
	}
	r.reportMasterHandoverStalled(ctx, v, outgoing, refusal)
	return &RollingUpdateResult{DeferredRequeueAfter: rollingUpdateRequeueDelay}
}

// reportMasterHandoverStalled sets MasterHandoverStalled with the refusal's reason and
// the repair for it, and emits one Warning Event when the condition turns True.
//
// The Warning is the exception among the ...Stalled conditions, which are silent
// because their subject shows in kubectl get pods. This hold replaces a pause at this
// gate, and every pause emits one; and a dataset veto shows nowhere but here.
func (r *ValkeyReconciler) reportMasterHandoverStalled(ctx context.Context, v *vkov1.Valkey, outgoing string,
	refusal handoverRefusal) {
	cond := meta.FindStatusCondition(v.Status.Conditions, vkov1.ConditionTypeMasterHandoverStalled)
	wasTrue := cond != nil && cond.Status == metav1.ConditionTrue

	message := fmt.Sprintf("The rolling update has held the delete of the outgoing master %s for longer than "+
		"spec.rollingUpdate.syncTimeout (%v): %s. %s", outgoing, v.GetSyncTimeout(), refusal.message,
		handoverRepair(refusal.reason))
	changed, err := r.writeStatusCondition(ctx, v, vkov1.ConditionTypeMasterHandoverStalled,
		metav1.ConditionTrue, refusal.reason, message)
	if err != nil {
		log.FromContext(ctx).Error(err, "Failed to write MasterHandoverStalled; it is recomputed on the next pass")
		return
	}
	if changed && !wasTrue {
		r.recordEvent(v, corev1.EventTypeWarning, "MasterHandoverStalled", "%s", message)
	}
}

// handoverRepair is the part of the MasterHandoverStalled message that says what
// ends the hold.
func handoverRepair(reason string) string {
	switch reason {
	case vkov1.ReasonDatasetWouldBeDiscarded:
		return "The outgoing pod is kept because it may hold the only dataset. Pointing Sentinel at the outgoing " +
			"pod on every Sentinel (SENTINEL REMOVE, SENTINEL MONITOR it, and SENTINEL SET the auth-pass and timing " +
			"settings REMOVE dropped; docs/operations/rolling-updates.md lists the commands and the risk) lets the " +
			"operator demote the empty new master, which then resyncs from the outgoing pod, and the roll hands " +
			"over again through its forced retrigger -- which does not wait for that resync, so a dataset whose " +
			"full sync takes longer than the retrigger's wait can still be lost. A write that reaches the new " +
			"master first also ends the hold, and discards the outgoing pod's dataset"
	case vkov1.ReasonFormerMasterStillMaster:
		return "Sentinel converts a former master within seconds of the failover; check with SENTINEL REPLICAS " +
			"that every Sentinel still monitors it. The delete goes through once it answers as a replica"
	default:
		return "The delete goes through once every replica on the current spec has completed its sync from the " +
			"new master; check the replica's logs for the sync"
	}
}

// endHandoverHold ends a hold: the delete went through, or a new failover replaces the
// one the hold waited on. It drops both halves of the hold's bound -- a leftover
// first-seen would pre-expire the next hold (ADR 0010 D10) -- and retracts a standing
// report.
func (r *ValkeyReconciler) endHandoverHold(ctx context.Context, v *vkov1.Valkey) {
	r.nudges.forget(waitBoundKey(v.Namespace, v.Name, boundHandoverHold))
	r.clearMasterHandoverStalled(ctx, v)
	if _, ok := v.Annotations[annotationHandoverHoldStarted]; !ok {
		return
	}
	delete(v.Annotations, annotationHandoverHoldStarted)
	if err := r.Update(ctx, v); err != nil {
		log.FromContext(ctx).Error(err, "Failed to clear the handover hold bound annotation")
	}
}

// clearMasterHandoverStalled retracts the report once the delete went through or the
// rolling-update state was cleared. Presence-guarded: a cluster that never stalled
// never gains the condition (ADR 0005 D10).
func (r *ValkeyReconciler) clearMasterHandoverStalled(ctx context.Context, v *vkov1.Valkey) {
	if meta.FindStatusCondition(v.Status.Conditions, vkov1.ConditionTypeMasterHandoverStalled) == nil {
		return
	}
	r.setStatusCondition(ctx, v,
		vkov1.ConditionTypeMasterHandoverStalled,
		metav1.ConditionFalse,
		vkov1.ReasonMasterHandoverNotHeld,
		"The rolling update no longer holds the delete of the outgoing master")
}

// keyCountsOnce memoizes a key-count reader for one pass, so the gate and the veto read
// each pod once. A nil reader stays nil: demotionRefusalReason treats it as counts
// that are unavailable.
func keyCountsOnce(read func(string) (int, error)) func(string) (int, error) {
	if read == nil {
		return nil
	}
	type answer struct {
		keys int
		err  error
	}
	seen := map[string]answer{}
	return func(podName string) (int, error) {
		if a, ok := seen[podName]; ok {
			return a.keys, a.err
		}
		keys, err := read(podName)
		seen[podName] = answer{keys: keys, err: err}
		return keys, err
	}
}

// readKeyCount reads one count through a reader that may be nil.
func readKeyCount(keys func(string) (int, error), podName string) (int, error) {
	if keys == nil {
		return 0, fmt.Errorf("the key counts are unavailable")
	}
	return keys(podName)
}

// replicaOfRefusal returns why no pod may be pointed at master, or "" when all may:
// the ADR 0028 veto applied to every other existing pod, non-Ready ones included,
// joined into one line that says who holds what
// (docs/adr/0037-the-master-handover-loses-no-acknowledged-write-and-no-dataset.md, D4).
// A master holding keys refuses nothing and costs one count; only an empty or
// unreadable master has the other pods counted.
func replicaOfRefusal(keys func(string) (int, error), masterPodName string, pods []podState) string {
	if keys == nil {
		return "the key counts are unavailable"
	}
	master := podState{name: masterPodName}
	var refusals []string
	for _, ps := range pods {
		if ps.name == masterPodName || !ps.exists {
			continue
		}
		if reason := demotionRefusalReason(keys, master, ps); reason != "" {
			refusals = append(refusals, reason)
		}
	}
	return strings.Join(refusals, "; ")
}
