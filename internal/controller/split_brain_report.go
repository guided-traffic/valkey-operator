package controller

import (
	"context"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
)

// splitBrainWarnAfter is how long more than one pod may answer that it is the
// master before the operator calls it a split brain in an Event.
//
// Two masters are a designed state, not an anomaly: a controlled failover
// promotes the replica before the outgoing master stops answering, and Sentinel
// reconfigures the old master only after it has promoted the new one. Reporting
// that window as a Warning trained operators to ignore the one Warning that must
// never be ignored, which is the whole point of this bound
// (docs/adr/0025-a-split-brain-warning-means-one-that-did-not-resolve-itself.md).
//
// 90 s is chosen above every duration a legitimately outgoing master can occupy:
// the 75 s terminationGracePeriodSeconds of the data pod and the 60 s drain
// preStop hook (internal/builder/statefulset.go). It is also below
// finalizationStallTimeout, so the operator cannot abandon a topology
// restoration with rogue masters still present without the Warning having fired
// first -- asserted by TestSplitBrainWarnAfterIsBelowTheFinalizationBound. The
// value joins the existing 90 s family (replicaReconnectTimeout,
// sentinelAwarenessTimeout) rather than inventing a new number.
const splitBrainWarnAfter = 90 * time.Second

// boundMultipleMasters keys the in-memory copy of the double-master deadline in
// the shared nudge tracker. Unlike the other wait bounds it has no annotation:
// the durable copy is the LastTransitionTime of the MultipleMasters condition,
// which the same pass has to write anyway.
const boundMultipleMasters = "multiple-masters"

// resolveSplitBrain is detectAndResolveSplitBrain plus the report about it. It is
// the entry point for the two callers that own the reporting -- the Sentinel and
// the non-Sentinel rolling-update passes -- and the reason the resolver itself
// stays free of any Event or status write.
//
// The split matters twice. The resolver is the ADR 0007 D8 and ADR 0008 D10/D11
// decision path and its tests assert demotion behaviour with no recorder in play;
// keeping it pure is what lets those tests stand unchanged across this change.
// And the report writes status, which re-Gets the CR (writeStatusCondition), so
// it must run where the caller holds no unpersisted annotation edits -- true at
// both call sites, not true everywhere the resolver is reachable.
//
// The master list is taken before resolution: the resolver clears isMaster on
// every pod it demotes, so afterwards the observation is gone.
func (r *ValkeyReconciler) resolveSplitBrain(ctx context.Context, v *vkov1.Valkey, pods []podState, masterIdx int, knownMaster string) ([]podState, int) {
	observed := mastersReportingRole(pods)
	pods, masterIdx = r.detectAndResolveSplitBrain(ctx, v, pods, masterIdx, knownMaster)
	r.reportMultipleMasters(ctx, v, observed, knownMaster)
	return pods, masterIdx
}

// mastersReportingRole returns the names of the pods that answered master, in
// ordinal order.
func mastersReportingRole(pods []podState) []string {
	var names []string
	for _, ps := range pods {
		if ps.isMaster {
			names = append(names, ps.name)
		}
	}
	return names
}

// reportMultipleMasters carries the level in the MultipleMasters condition and
// emits the SplitBrainDetected Warning on the single edge where the double-master
// window outlives splitBrainWarnAfter.
//
// The reason of the condition is the memory of whether that Warning already
// fired. Nothing else could be: an annotation write can fail (ADR 0010 D7) and
// process memory does not survive an operator restart, while the condition is a
// write this path performs anyway and re-reads on the next pass. The status stays
// True across the reason change, so meta.SetStatusCondition keeps the original
// LastTransitionTime -- which is exactly the deadline the bound is measured from.
func (r *ValkeyReconciler) reportMultipleMasters(ctx context.Context, v *vkov1.Valkey, masters []string, authority string) {
	if len(masters) <= 1 {
		r.clearMultipleMasters(ctx, v)
		return
	}

	// The in-memory copy answers only while the condition write is failing. It is
	// armed before the write for that reason.
	started := r.nudges.observe(waitBoundKey(v.Namespace, v.Name, boundMultipleMasters), time.Now())

	alreadyWarned := multipleMastersReason(v) == vkov1.ReasonMultipleMastersPersisted
	persisted := alreadyWarned || r.multipleMastersOutlivedBound(v, started)

	reason := vkov1.ReasonMultipleMastersTransitional
	if persisted {
		reason = vkov1.ReasonMultipleMastersPersisted
	}

	r.setStatusCondition(ctx, v, vkov1.ConditionTypeMultipleMasters, metav1.ConditionTrue,
		reason, multipleMastersMessage(masters, authority))

	if persisted && !alreadyWarned {
		log.FromContext(ctx).Info("Split-brain outlived the resolution bound",
			"masters", masters, "authority", authority, "bound", splitBrainWarnAfter)
		r.recordEvent(v, corev1.EventTypeWarning, "SplitBrainDetected",
			"Split-brain unresolved after %v: %d pods report master role (%s)",
			splitBrainWarnAfter, len(masters), strings.Join(masters, ", "))
	}
}

// clearMultipleMasters flips MultipleMasters to False, but only for a CR that
// actually carries it -- the presence guard every condition here needs, because
// meta.SetStatusCondition adds an absent condition and an unconditional call
// would write the condition onto every cluster in the fleet on upgrade
// (docs/adr/0005-upgrade-neutral-defaults-and-anti-affinity.md).
//
// The in-memory deadline is dropped with it. A leftover entry would pre-expire
// the budget of the next double-master window and turn the very first pass of the
// next controlled failover into a Warning, which is ADR 0010 D10 one layer down.
func (r *ValkeyReconciler) clearMultipleMasters(ctx context.Context, v *vkov1.Valkey) {
	r.nudges.forget(waitBoundKey(v.Namespace, v.Name, boundMultipleMasters))
	if meta.FindStatusCondition(v.Status.Conditions, vkov1.ConditionTypeMultipleMasters) == nil {
		return
	}
	r.setStatusCondition(ctx, v, vkov1.ConditionTypeMultipleMasters, metav1.ConditionFalse,
		vkov1.ReasonSingleMaster, "At most one pod reports the master role")
}

// multipleMastersReason returns the reason of a standing MultipleMasters=True, or
// the empty string when the condition is absent or False.
func multipleMastersReason(v *vkov1.Valkey) string {
	cond := meta.FindStatusCondition(v.Status.Conditions, vkov1.ConditionTypeMultipleMasters)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		return ""
	}
	return cond.Reason
}

// multipleMastersOutlivedBound reports whether the current double-master window is
// older than splitBrainWarnAfter.
//
// The condition wins when it stands: it is the copy that survives an operator
// restart, so a restarted operator must not hand an unresolved split brain a
// fresh 90 s of silence. The in-memory first-seen answers whenever the condition
// never landed, which is the case a status write that keeps failing produces --
// the same two-copy discipline as waitBoundExceeded, with the condition standing
// in for the annotation.
func (r *ValkeyReconciler) multipleMastersOutlivedBound(v *vkov1.Valkey, started time.Time) bool {
	cond := meta.FindStatusCondition(v.Status.Conditions, vkov1.ConditionTypeMultipleMasters)
	if cond != nil && cond.Status == metav1.ConditionTrue {
		return time.Since(cond.LastTransitionTime.Time) > splitBrainWarnAfter
	}
	return time.Since(started) > splitBrainWarnAfter
}

// multipleMastersMessage names the pods and the authority, because "2 pods report
// master role" is the message the events API freezes on the first occurrence of a
// series and it says nothing an operator can act on.
func multipleMastersMessage(masters []string, authority string) string {
	named := "no authority names one of them"
	if authority != "" {
		named = fmt.Sprintf("authority names %s", authority)
	}
	return fmt.Sprintf("%d pods report the master role (%s); %s",
		len(masters), strings.Join(masters, ", "), named)
}
