// Package controller implements the Kubernetes reconciliation logic
// for Valkey custom resources.
package controller

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/builder"
	"github.com/guided-traffic/valkey-operator/internal/common"
)

// steadyStateRecheckDelay is how long the operator waits before re-examining a
// cluster in which it confirmed a second master outside a rolling update. It is
// comfortably longer than the sidecar label poll (1s default), so the next pass
// sees converged labels rather than the state the demotion just left behind.
const steadyStateRecheckDelay = 15 * time.Second

// The three pieces of evidence that separate a promotion the operator did not
// perform from a pod that gave itself the master role. They are quoted into the
// MasterAdopted Event so the reason for an adoption is readable off the CR.
const (
	evidenceDrainStamp = "the sidecar drain handler stamped it as the pod it promoted on SIGTERM"
	evidenceStructural = "it cannot have given itself the master role: the init script grants that only " +
		"to ordinal 0 or to the pod the replica ConfigMap names, and this pod is neither"
	evidenceRecordedYielded = "the pod the known-master annotation names answers and no longer reports the " +
		"master role, so the record has already been overtaken by the topology"
)

// checkSteadyStateSplitBrain consolidates a multi-master state that appears outside a rolling
// update (docs/adr/0011-evidence-based-steady-state-split-brain-resolution.md, D1). Nothing else
// re-detects one: after verifyTopologyRestored clears the state annotation,
// checkAndHandleRollingUpdate early-returns and detectAndResolveSplitBrain has no other caller,
// while checkAndRecoverNoMaster only handles masterCount == 0. The -rw Service selects on the
// instanceRole label, so two labeled masters means writes round-robin across two independent
// datasets.
//
// The trigger is free: the pod label the -rw Service already selects on. A single
// labeled master that the annotation also names costs nothing beyond the cached
// List -- no API write, no Valkey connection.
//
// # Why the annotation alone cannot decide anything
//
// A demotion is a REPLICAOF, which discards the demoted pod's dataset, and the
// known-master annotation is unreliable in exactly one way the operator cannot fix
// from its own side: on SIGTERM of a master pod the sidecar drain handler promotes
// a replica (internal/sidecar/drain.go) and has no CR access to record it. Every
// operator-driven promotion funnels through recordPromotedMaster, so the annotation
// is trustworthy precisely when the operator promoted and untrustworthy precisely
// when the sidecar did.
//
// Three independent pieces of evidence tell the two apart, and the check refuses to
// act when it has none:
//
//   - The drain stamp (common.AnnotationDrainPromotedAt). The drain handler stamps
//     the pod it promotes. Positive evidence of a promotion nobody recorded.
//   - The structural rule. The init script (internal/builder/statefulset.go, Phase 3)
//     grants the master config to ordinal 0 on the ordinal fallback, and otherwise
//     only through the ADR 0008 D8, D9 self-claim, which requires the mounted replica config to
//     name the pod itself. A labeled master with ordinal > 0 that the live replica
//     ConfigMap does not name therefore cannot have elected itself -- it was promoted
//     out of a running replication chain.
//   - The recorded pod yielded. The pod the annotation names answers a probe and
//     reports a role other than master. A pod that replicates from somewhere else has
//     already given up its own dataset, so pointing the replica ConfigMap away from it
//     destroys nothing: the record is simply out of date.
//
// None is required, and the third exists because the second is blind to exactly the
// pod a drain promotes most often. buildReplicaAddrs (internal/sidecar/drain.go)
// walks the ordinals ascending and findSyncedReplica takes the first synced peer, so
// draining a non-pod-0 master promotes pod-0 whenever pod-0 is healthy -- and a
// non-pod-0 master is the routine output of this design, since every adoption leaves
// one behind. couldNotHaveSelfElected can never exonerate pod-0, so for that drain
// the structural rule is inert and only the stamp and the recorded pod's own answer
// remain. The operator-upgrade window, in which old sidecars write no stamp,
// therefore rests on the structural rule for a pod-0 master and on the recorded
// pod's answer for any other.
//
// # What is deliberately not evidence for an adoption
//
// The creation order. "The pod the annotation names is the younger Pod object" is
// true after a drain -- and just as true after the recorded master's node hard-failed
// with no SIGTERM, hence no drain and no stamp, while a peer that could reach nobody
// took the init script's ordinal fallback and elected itself. Adopting there
// republishes the replica ConfigMap toward the self-elected pod, and the real master
// full-resyncs its newer dataset away the moment it finishes booting -- silently, and
// caused by the operator. Being newer is evidence of nothing, so the creation order
// may only ever REFUSE a demotion (refuseDemotion), never grant an adoption.
//
// # The decision table
//
// Let labeled be the pods carrying instanceRole=master, i.e. the set the -rw Service
// routes writes to.
//
//	len(labeled) == 0  -> return; checkAndRecoverNoMaster owns it.
//
//	len(labeled) == 1:
//	  name == knownMasterPodName(v) -> no-op, and no probe at all.
//	  otherwise                     -> probe; only if it confirms role:master:
//	      drain stamp                                   -> adopt
//	      ordinal > 0 and the ConfigMap names elsewhere -> adopt
//	      the recorded pod answers and is not master    -> adopt
//	      none of them                                  -> refuse, Warning Event
//
//	len(labeled) >= 2, evidence first and annotation second:
//	  1. exactly one labeled master carries a stamp and confirms role:master
//	     -> record it and demote the others toward it
//	     more than one such pod -> refuse, Warning Event. Ambiguous evidence has to
//	        end in the refusal, never fall through into a demotion that then picks
//	        its target by the annotation alone.
//	  2. else, if the annotation names one of them and it confirms role:master
//	     -> refuse where the shape is the missed drain: the annotation names pod-0
//	        while a second labeled master has ordinal > 0 and the ConfigMap does not
//	        name it, OR the annotation names a pod that was recreated after the one it
//	        would demote, recently enough for that recreation to explain the split
//	        brain (see refuseDemotion). Demoting is the destructive direction and the
//	        evidence is ambiguous there.
//	     -> otherwise demote toward the annotation-named pod.
//	  3. else refuse, Warning Event.
//
// Return tuple, same contract as handlePostRollingUpdateChecks:
//   - (Result{}, false, nil) nothing was demoted; the pass continues to
//     updateStatus.
//   - (Result{RequeueAfter: steadyStateRecheckDelay}, false, nil) a confirmed
//     second master could not be demoted, or the operator refused to demote it
//     because the shape says a drain promoted it. The pass still has to reach
//     updateStatus -- an unresolved split brain is a lasting condition, and
//     suppressing the status write would freeze the CR at its last verdict,
//     typically OK -- but it also needs a guaranteed next look, so the requeue
//     rides along non-terminally and reconcileWorkload applies it after the status
//     write. Those cases leave a SplitBrainUnresolved respectively a
//     SplitBrainDemotionRefused Event behind.
//   - (Result{RequeueAfter: steadyStateRecheckDelay}, true, nil) a second master
//     was demoted this pass; the caller returns immediately. Status computed in
//     the same pass would describe the topology the demotion just replaced, and
//     status writes do not re-trigger reconcile (GenerationChangedPredicate), so
//     the requeue is the only guarantee of a confirming pass.
//   - (Result{}, true, err) the pod List failed; the caller returns the error and
//     the rate limiter owns the retry.
func (r *ValkeyReconciler) checkSteadyStateSplitBrain(ctx context.Context, v *vkov1.Valkey) (ctrl.Result, bool, error) {
	// Sentinel clusters have their own authority, and the sidecar cross-check
	// already demotes a pod Sentinel disagrees with.
	if !v.IsMultiReplicaWithoutSentinel() {
		return ctrl.Result{}, false, nil
	}

	// While a rolling update runs, the state machine owns the topology and
	// deliberately produces a two-master window. Same guard as
	// checkAndRecoverNoMaster.
	if r.getRollingUpdateState(v) != "" {
		return ctrl.Result{}, false, nil
	}

	labeled, err := r.listMasterLabeledPods(ctx, v)
	if err != nil {
		return ctrl.Result{}, true, fmt.Errorf("listing master-labeled pods: %w", err)
	}
	if len(labeled) == 0 {
		// Zero labeled masters belongs to checkAndRecoverNoMaster.
		return ctrl.Result{}, false, nil
	}
	if len(labeled) == 1 {
		r.adoptUnrecordedPromotion(ctx, v, &labeled[0])
		return ctrl.Result{}, false, nil
	}

	log.FromContext(ctx).Info("Multiple pods labeled master outside a rolling update",
		"cluster", v.Name, "count", len(labeled))
	return r.resolveMultiMaster(ctx, v, labeled)
}

// listMasterLabeledPods returns the pods the sidecar currently labels master --
// exactly the set the -rw Service routes writes to. The read is served from the
// controller-runtime cache (the Pod informer already exists: every reconcile Gets
// pods in checkAndHandleRollingUpdate), so the healthy case costs no API call and
// no Valkey connection.
// A pod this cluster's StatefulSet did not create is filtered out and never
// reaches the caller (ADR 0020 D9). Both directions of that matter. The resolver
// this feeds issues REPLICAOF, the data-discarding command the whole master
// authority is built to contain, so a foreign pod must never be a demotion target.
// And the count itself is load-bearing: one labeled master is a healthy cluster and
// two are a split brain, so an unfiltered stray would turn a healthy cluster into a
// resolution that demotes a real master.
func (r *ValkeyReconciler) listMasterLabeledPods(ctx context.Context, v *vkov1.Valkey) ([]corev1.Pod, error) {
	podList := &corev1.PodList{}
	if err := r.List(ctx, podList,
		client.InNamespace(v.Namespace),
		client.MatchingLabels(common.MasterSelectorLabels(v)),
	); err != nil {
		return nil, err
	}

	sts, err := r.ownedDataStatefulSet(ctx, v)
	if err != nil {
		return nil, err
	}

	owned, _ := filterOwnedPods(podList.Items, sts)
	return owned, nil
}

// adoptUnrecordedPromotion reconciles the known-master annotation with a single,
// undisputed master the operator did not promote itself.
//
// The annotation is the tie-breaker AMONG MULTIPLE masters and never overrules a
// single, undisputed one -- but a label on its own is not evidence either, so the
// adoption still needs the stamp, the structural rule or the recorded pod's own
// answer behind it. Adopting the wrong pod republishes the replica ConfigMap, and
// the real master then full-resyncs its newer dataset away the moment it returns:
// silently and completely.
//
// Cost discipline: both names are already in hand, so the healthy case (they agree)
// is a string comparison. The probe only runs on a disagreement, which is not the
// steady state.
//
// It deliberately reports nothing to the caller. Adoption moves no replication
// state, so the pass must continue to updateStatus either way, and a failed
// adoption is retried by the next pass with no state left behind. A refusal gets
// no recheck requeue on purpose: a single labeled master is not a split brain --
// writes reach exactly one dataset -- and no amount of polling resolves a record
// only a human can correct. The Warning Event is the signal.
func (r *ValkeyReconciler) adoptUnrecordedPromotion(ctx context.Context, v *vkov1.Valkey, pod *corev1.Pod) {
	logger := log.FromContext(ctx)

	recorded := knownMasterPodName(v)
	if recorded == "" || recorded == pod.Name {
		// Nothing recorded to contradict, or they already agree. This is the
		// abandoned-restoration end state too: the annotation names the promoted
		// replica, which is the single labeled master, so nothing happens.
		return
	}

	info, err := r.getInstanceChecker().GetReplicationInfo(ctx, v, pod.Name)
	if err != nil {
		logger.Info("Sole master-labeled pod is unreachable; leaving the known master untouched",
			"pod", pod.Name, "knownMaster", recorded, "error", err)
		return
	}
	if info.Role != common.RoleMaster {
		// A stale label is not a promotion. Adopting it would point the replica
		// ConfigMap at a pod that is itself replicating from somewhere else.
		logger.Info("Sole master-labeled pod does not report master role; the label is stale",
			"pod", pod.Name, "knownMaster", recorded, "role", info.Role)
		return
	}

	evidence, ok := r.promotionEvidence(ctx, v, pod, recorded)
	if !ok {
		r.recordRefusedAdoption(ctx, v, pod.Name, recorded)
		return
	}
	_ = r.adoptMaster(ctx, v, pod, recorded, evidence)
}

// promotionEvidence reports why the operator may treat pod as a master somebody
// else promoted, or false when it has no reason to.
//
// Order matters for cost: the stamp is already on the listed Pod, the structural rule
// needs the replica ConfigMap (cache-served), and only the third rule opens a Valkey
// connection -- to the pod the annotation names, and only on a disagreement between
// that annotation and the label, which is not the steady state.
//
// The third rule is what keeps a refusal here from manufacturing the destructive shape one state
// later. Refusing leaves the annotation and the replica ConfigMap naming the drained pod, so that
// pod self-claims off its own mount when it returns
// (docs/adr/0008-known-master-annotation-is-the-recorded-authority.md, D8, D9), and the cluster
// arrives at two masters -- where the operator can then only refuse again. Adopting the pod the
// drain promoted while the drained one is back as a replica republishes the ConfigMap, so the next
// restart keeps it a replica.
func (r *ValkeyReconciler) promotionEvidence(ctx context.Context, v *vkov1.Valkey,
	pod *corev1.Pod, recorded string) (string, bool) {
	if hasDrainStamp(pod) {
		return evidenceDrainStamp, true
	}
	cmMaster, known := r.replicaConfigMaster(ctx, v)
	if known && couldNotHaveSelfElected(pod.Name, cmMaster) {
		return evidenceStructural, true
	}
	if r.recordedGaveUpTheRole(ctx, v, recorded) {
		return evidenceRecordedYielded, true
	}
	return "", false
}

// recordedGaveUpTheRole reports whether the pod the known-master annotation names
// answers a probe and reports a role other than master.
//
// It is the positive form of "the record is stale". A pod that replicates from
// somewhere else holds no dataset the operator can destroy by republishing the
// replica ConfigMap: whatever it had, it gave up on its own. Anything else --
// unreachable, or still reporting master -- is not evidence and reads as false, so
// the adoption waits for a pass that can tell. That fail-closed reading is the whole
// point: the pod may be a master that is merely restarting, and the writes it holds
// are exactly the ones an adoption would discard.
func (r *ValkeyReconciler) recordedGaveUpTheRole(ctx context.Context, v *vkov1.Valkey, recorded string) bool {
	info, err := r.getInstanceChecker().GetReplicationInfo(ctx, v, recorded)
	if err != nil {
		log.FromContext(ctx).Info("Cannot reach the recorded master; it may still hold the newer dataset",
			"pod", recorded, "error", err)
		return false
	}
	return info.Role != common.RoleMaster
}

// recreatedAfter reports whether the pod named later exists, was created strictly
// after the pod earlier, and was created recently enough for that recreation to
// explain the split brain the caller is looking at.
//
// It is the operator's only handle on "this pod was deleted and came back", and it
// needs no signal from Valkey at all. A drain deletes the master and its replacement
// carries a fresh creationTimestamp, while the peer its sidecar promoted keeps the
// old one; a human REPLICAOF NO ONE and a stale-mount self-claim both leave the
// recorded pod's timestamp untouched, so those shapes stay resolvable.
//
// The freshness window is what makes this a statement about an event instead of about
// a pair of objects. "earlier was created before later" is a permanent property of
// two Pod objects: it becomes true after any single reschedule of the recorded master
// -- for any reason, days or weeks ago -- and then holds forever. Unbounded, the rule
// would refuse every future split brain of that pair whatever its cause, and the
// operator would never consolidate that cluster again.
//
// The window is v.GetSyncTimeout() (spec.rollingUpdate.syncTimeout, default 5m). It
// is the operator's own budget for a pod that was deleted to come back and rejoin its
// master -- the replica-replacement phase and Phase 1 of the topology restoration
// both wait exactly that long for exactly that -- so the same knob widens all three,
// which is what an environment with slow image pulls or slow PVC rebinds needs.
// Past the window the operator resolves the split brain the way it did before the
// creation-order rule existed: toward the annotation, on the structural rule alone.
//
// Deliberately strict about the order, and deliberately unaware of how much newer
// inside the window: the resolution of creationTimestamp is one second. A missing or
// unreadable pod reports false -- the same fail-closed reading the other rules use.
func (r *ValkeyReconciler) recreatedAfter(ctx context.Context, v *vkov1.Valkey,
	later string, earlier *corev1.Pod) bool {
	pod := &corev1.Pod{}
	if err := r.Get(ctx, types.NamespacedName{Name: later, Namespace: v.Namespace}, pod); err != nil {
		log.FromContext(ctx).Info("Cannot read the recorded master pod; treating the creation order as unknown",
			"pod", later, "error", err)
		return false
	}

	// A pod this cluster's StatefulSet did not create is the same answer as a missing
	// one: unknown, which reads as false here (ADR 0020 D9). The creation-order rule
	// may only ever refuse a demotion, never grant an adoption, so failing closed on
	// an unproven pod keeps the rule on the side it is allowed to be wrong.
	sts, err := r.ownedDataStatefulSet(ctx, v)
	if err != nil || !podIsOurs(pod, sts) {
		log.FromContext(ctx).Info("Cannot prove the recorded master pod belongs to this cluster; "+
			"treating the creation order as unknown", "pod", later)
		return false
	}
	if !earlier.CreationTimestamp.Before(&pod.CreationTimestamp) {
		return false
	}
	return time.Since(pod.CreationTimestamp.Time) <= v.GetSyncTimeout()
}

// resolveMultiMaster decides a confirmed multi-master state: evidence first, the
// annotation second. See the decision table on checkSteadyStateSplitBrain.
func (r *ValkeyReconciler) resolveMultiMaster(ctx context.Context, v *vkov1.Valkey,
	labeled []corev1.Pod) (ctrl.Result, bool, error) {
	// Rule 1. The ordinary node drain with the single-master window missed. The
	// stamped pod holds the writes of the drain window, so resolving toward it is
	// the only direction that keeps them.
	stamped := r.stampedMasters(ctx, v, labeled)
	switch {
	case len(stamped) == 1:
		return r.adoptAndConsolidate(ctx, v, stamped[0], labeled)
	case len(stamped) > 1:
		// Two live stamped masters are two promotions nobody recorded, and nothing
		// says which one holds the writes that matter. Ambiguous evidence ends here
		// rather than falling through to rule 2, which would demote BOTH stamped
		// pods toward whatever the annotation names -- a destructive action taken
		// precisely because the evidence did not resolve.
		r.recordAmbiguousStamps(ctx, v, stamped)
		return ctrl.Result{RequeueAfter: steadyStateRecheckDelay}, false, nil
	}

	// Rule 2. No evidence of an unrecorded promotion: the annotation decides, as
	// it did before the stamp existed.
	authority, ok := r.confirmedMasterAuthority(ctx, v)
	if !ok {
		r.recordEvent(v, corev1.EventTypeWarning, "SplitBrainUnresolved",
			"%d pods are labeled master but no confirmed known master is available; leaving replication untouched",
			len(labeled))
		return ctrl.Result{}, false, nil
	}

	// The missed-drain shape, refused -- see refuseDemotion for the two forms it
	// takes. cmMaster is read once for the whole loop; it is empty when the
	// ConfigMap cannot be read, which makes the structural form fire: a destructive
	// action needs positive justification, and missing evidence is not evidence.
	cmMaster, _ := r.replicaConfigMaster(ctx, v)
	refuse := func(rogue *corev1.Pod) (string, bool) {
		return r.refuseDemotion(ctx, v, authority, cmMaster, rogue)
	}

	demoted, unresolved, refused := r.demoteConfirmedRogues(ctx, v, authority, refuse, labeled)
	return r.reportDemotionOutcome(v, authority, demoted, unresolved, refused)
}

// refuseDemotion reports whether demoting rogue toward the recorded authority would
// destroy the writes of a drain window, and says why in a clause the Event quotes.
//
// Two shapes say "a drain promoted this pod and nobody recorded it", and either is
// enough:
//
//   - The structural one. The annotation names pod-0 -- the ordinal the init script
//     hands the master config to unconditionally -- while the rogue could not have
//     reached the role on its own.
//   - The creation order. The recorded pod is younger than the rogue and was
//     recreated inside the freshness window (recreatedAfter), so it was deleted and
//     came back while the rogue kept running. That is the only shape a drain of a
//     non-pod-0 master leaves, and it is the one the structural rule cannot see,
//     because the pod such a drain promotes is usually pod-0.
//
// The price, stated rather than hidden: the creation order cannot distinguish a
// returning drained master from a master that merely crashed and came back while a
// peer self-elected in the gap. Such a state used to be resolved toward the
// annotation; inside the window it now stays a visible split brain until a human
// resolves it.
//
// Where it does NOT fire, so nobody reads more into the rule than it says: a
// simultaneous restart of the whole pod set. The data StatefulSet runs with
// PodManagementPolicy: Parallel (internal/builder/statefulset.go), so a co-restart
// recreates every pod at once and ties the timestamps -- Before is strict, so the
// rule stays inert and the annotation decides. It fires on the asymmetric shape
// only: one pod recreated while the other kept running. And it stops firing once
// that recreation is older than the window, because a reschedule from last week
// explains nothing about a split brain that appeared today.
//
// The trade is deliberate and it is the one the rest of this file makes: the drain
// that this shape is indistinguishable from is the routine case, and demoting there
// discards the only copy of the drain-window writes. Two masters a human can see
// beat one dataset silently discarded.
func (r *ValkeyReconciler) refuseDemotion(ctx context.Context, v *vkov1.Valkey,
	authority, cmMaster string, rogue *corev1.Pod) (string, bool) {
	if podOrdinal(authority) == 0 && couldNotHaveSelfElected(rogue.Name, cmMaster) {
		return fmt.Sprintf("%s cannot have given itself the master role -- its ordinal is not 0 and the "+
			"replica ConfigMap does not name it", rogue.Name), true
	}
	if r.recreatedAfter(ctx, v, authority, rogue) {
		return fmt.Sprintf("%s was created after %s, so the recorded master was deleted and recreated "+
			"while %s kept running", authority, rogue.Name, rogue.Name), true
	}
	return "", false
}

// adoptAndConsolidate records the drain-promoted pod as the known master and
// demotes every other confirmed master toward it.
//
// Recording comes first and gates the rest: an unrecorded promotion is not an
// authority, and demoting toward a pod the CR does not name would have the next
// pass read the old annotation back and undo the consolidation.
func (r *ValkeyReconciler) adoptAndConsolidate(ctx context.Context, v *vkov1.Valkey,
	promoted *corev1.Pod, labeled []corev1.Pod) (ctrl.Result, bool, error) {
	if err := r.adoptMaster(ctx, v, promoted, knownMasterPodName(v), evidenceDrainStamp); err != nil {
		return ctrl.Result{RequeueAfter: steadyStateRecheckDelay}, false, nil
	}
	demoted, unresolved, refused := r.demoteConfirmedRogues(ctx, v, promoted.Name, nil, labeled)
	return r.reportDemotionOutcome(v, promoted.Name, demoted, unresolved, refused)
}

// adoptMaster makes pod the recorded master and says why on the CR.
func (r *ValkeyReconciler) adoptMaster(ctx context.Context, v *vkov1.Valkey, pod *corev1.Pod,
	previous, evidence string) error {
	logger := log.FromContext(ctx)

	host := fmt.Sprintf("%s.%s.%s.svc.cluster.local", pod.Name,
		common.HeadlessServiceName(v, common.ComponentValkey), v.Namespace)
	if err := r.recordPromotedMaster(ctx, v, host); err != nil {
		logger.Info("Could not adopt the promoted master; the next pass retries",
			"pod", pod.Name, "knownMaster", previous, "error", err)
		return err
	}

	r.recordEvent(v, corev1.EventTypeNormal, "MasterAdopted",
		"Adopted %s as the known master (previously %s): %s, so the operator did not perform this promotion",
		pod.Name, previous, evidence)
	logger.Info("Adopted a promotion the operator did not perform",
		"pod", pod.Name, "previousKnownMaster", previous, "evidence", evidence)
	return nil
}

// stampedMasters returns the labeled masters the drain handler stamped that still
// confirm role:master. Exactly one of them is the unambiguous answer; more than one
// is ambiguous evidence, which the caller turns into a refusal.
//
// The set is counted over the pods that both carry a stamp and still answer
// role:master, not over the stamps: when two sequential drains left two stamps and
// the older stamped pod has meanwhile become a replica of the newer one, the newer
// one is still the unambiguous answer.
func (r *ValkeyReconciler) stampedMasters(ctx context.Context, v *vkov1.Valkey,
	labeled []corev1.Pod) []*corev1.Pod {
	var found []*corev1.Pod
	for i := range labeled {
		pod := &labeled[i]
		if !hasDrainStamp(pod) {
			continue
		}
		info, err := r.getInstanceChecker().GetReplicationInfo(ctx, v, pod.Name)
		if err != nil || info.Role != common.RoleMaster {
			continue
		}
		found = append(found, pod)
	}
	return found
}

// confirmedMasterAuthority returns the pod name the known-master annotation names,
// but only when that pod answers and itself reports role:master.
//
// The annotation is the only admissible authority here. Outside a rolling update there is no
// Sentinel, no promoted-pod annotation and no safe tie-break: with a shrunken cluster both masters
// report zero connected slaves, and picking by ordinal demotes the pod that holds the post-failover
// writes (docs/adr/0008-known-master-annotation-is-the-recorded-authority.md, D10, D11). Refusing
// is the correct outcome -- the cluster keeps two masters, visibly, instead of silently losing a
// dataset.
func (r *ValkeyReconciler) confirmedMasterAuthority(ctx context.Context, v *vkov1.Valkey) (string, bool) {
	logger := log.FromContext(ctx)

	name := knownMasterPodName(v)
	if name == "" {
		logger.Info("No known-master annotation; refusing to pick a master by tie-break",
			"cluster", v.Name)
		return "", false
	}

	info, err := r.getInstanceChecker().GetReplicationInfo(ctx, v, name)
	if err != nil {
		logger.Info("Known master is unreachable; leaving the multi-master state untouched",
			"pod", name, "error", err)
		return "", false
	}
	if info.Role != common.RoleMaster {
		logger.Info("Known master no longer reports master role; leaving the multi-master state untouched",
			"pod", name, "role", info.Role)
		return "", false
	}
	return name, true
}

// demoteConfirmedRogues demotes every labeled master that is not the authority,
// that confirms role:master when probed, and that refuse -- when the caller passes
// one -- does not veto.
//
// It reports the three outcomes separately, because they mean different things to
// the caller: demoted counts the pods that accepted the REPLICAOF, so the topology
// just changed and the status computed in this pass would already be stale;
// unresolved counts the confirmed second masters the operator could not demote (a
// not-ready rogue, a refused command), a transient failure worth retrying and worth
// stating on the CR; refused counts the ones it deliberately did not demote, which
// is neither a failure to report as one nor something a retry can fix -- it needs a
// human. Both lasting outcomes have to keep the recheck alive, only the transient
// one gets the "could not be demoted" Event.
func (r *ValkeyReconciler) demoteConfirmedRogues(ctx context.Context, v *vkov1.Valkey,
	authority string, refuse func(rogue *corev1.Pod) (string, bool),
	labeled []corev1.Pod) (demoted, unresolved, refused int) {
	logger := log.FromContext(ctx)
	checker := r.getInstanceChecker()

	for i := range labeled {
		pod := &labeled[i]
		if pod.Name == authority {
			continue
		}
		info, err := checker.GetReplicationInfo(ctx, v, pod.Name)
		if err != nil || info.Role != common.RoleMaster {
			// A stale label or an unreachable pod is not evidence of a second
			// master, and REPLICAOF on a pod that is already a replica of a third
			// pod would move data-plane state on a guess.
			continue
		}
		if refuse != nil {
			if because, no := refuse(pod); no {
				r.recordRefusedDemotion(ctx, v, pod.Name, authority, because)
				refused++
				continue
			}
		}

		// terminating is filled in even though this path only ever asks reachable():
		// available() must never be structurally false at a construction site, or the
		// next reader of this podState gets the safe-looking answer for the wrong
		// reason (docs/adr/0026-a-pod-being-deleted-is-not-available.md, D6).
		rogue := podState{
			name:           pod.Name,
			pod:            pod,
			exists:         true,
			readyCondition: isPodReady(pod),
			terminating:    pod.DeletionTimestamp != nil,
		}
		if demoteErr := r.demoteRogueMaster(ctx, v, rogue, authority); demoteErr != nil {
			logger.Info("Steady-state demotion failed, will retry on the next pass",
				"pod", pod.Name, "authority", authority, "error", demoteErr)
			unresolved++
			continue
		}
		demoted++
	}
	return demoted, unresolved, refused
}

// reportDemotionOutcome maps the three demotion counters onto the return contract
// of checkSteadyStateSplitBrain.
func (r *ValkeyReconciler) reportDemotionOutcome(v *vkov1.Valkey, authority string,
	demoted, unresolved, refused int) (ctrl.Result, bool, error) {
	if unresolved > 0 {
		r.recordEvent(v, corev1.EventTypeWarning, "SplitBrainUnresolved",
			"%d confirmed second master(s) could not be demoted toward %s; writes are still split across two datasets",
			unresolved, authority)
	}
	if demoted == 0 {
		// Nothing changed this pass: either only labels disagreed and the sidecar
		// repatches within its poll interval, or every confirmed rogue refused the
		// REPLICAOF, or the operator refused to send it. Suppressing the status write
		// is only justified by a demotion that actually happened -- otherwise an
		// unresolvable split brain would freeze the CR status at whatever it last
		// said, typically OK, while the operator loops on it invisibly.
		//
		// Keeping the status write cost the guaranteed retry, though: a ready rogue
		// that merely refuses REPLICAOF leaves the phase at OK, and nothing else
		// schedules the next look -- the CR watch is generation-gated, there is no
		// Pod watch and no SyncPeriod override, so the next pass is the 10 h cache
		// resync. The recheck therefore travels as a non-terminal Result, which
		// reconcileWorkload applies once the pass has reached updateStatus.
		//
		// Only a still-split cluster asks for it, whether the operator could not
		// demote or would not. A stale label alone is not a data split (a real
		// replica rejects writes) and the operator does not repatch role labels, so
		// requeueing on it would poll for a fix it cannot perform.
		if unresolved+refused > 0 {
			return ctrl.Result{RequeueAfter: steadyStateRecheckDelay}, false, nil
		}
		return ctrl.Result{}, false, nil
	}
	return ctrl.Result{RequeueAfter: steadyStateRecheckDelay}, true, nil
}

// hasDrainStamp reports whether the sidecar drain handler recorded a promotion on
// this pod (common.AnnotationDrainPromotedAt).
//
// An absent, empty or unparseable value reads as "no stamp", never as "corrupt,
// therefore fresh": the stamp only ever adds permission to act, so failing to
// recognise one degrades to the structural rule or to a refusal.
//
// The timestamp is parsed but not compared. It exists for the operator log and for
// a human reading the pod, because the operator does not need it to be fresh: the
// two sites that end a promotion -- recordPromotedMaster when it records one, and
// clearRollingUpdateState when a rolling update that recorded its own finishes --
// clear every stamp of the cluster, so a stamp that is still there was not
// superseded by anything the operator did.
func hasDrainStamp(pod *corev1.Pod) bool {
	stamp := pod.Annotations[common.AnnotationDrainPromotedAt]
	if stamp == "" {
		return false
	}
	_, err := time.Parse(time.RFC3339, stamp)
	return err == nil
}

// clearDrainStamps removes the drain-promotion stamp from every pod of the cluster.
//
// It is correctness, not hygiene. The stamp means "a promotion nobody recorded", so
// once the operator has recorded a known master the stamp is spent evidence. Left
// behind, it outranks the annotation on the very next multi-master pass (rule 1 is
// evidence-first): a leftover stamp on pod-N would have the operator adopt pod-N and
// send REPLICAOF to the master it legitimately promoted, discarding that pod's
// dataset -- the exact loss the stamp exists to prevent, one pass later.
//
// A failed clear is logged and nothing else: the promotion IS recorded at that
// point, so aborting would trade a possible wrong adoption later for a certainly
// unrecorded promotion now. The residual is the leftover stamp described above,
// until a later pass through one of the two clearing sites succeeds.
//
// Those sites are recordPromotedMaster and clearRollingUpdateState. The second one
// exists because recordPromotedMaster is NOT the only writer of the known-master
// annotation: persistManualFailoverState writes it directly (one Update, for the
// conflict retry), and syncSentinelWithMaster goes through persistKnownMaster. Both
// leave the stamps of the cluster in place, and the rolling-update completion paths
// that never record a master of their own -- verifyTopologyRestored,
// finalizeMultiReplicaRollingUpdate, handlePostManualFailover -- would otherwise end
// with a spent stamp still outranking the annotation the update just wrote.
func (r *ValkeyReconciler) clearDrainStamps(ctx context.Context, v *vkov1.Valkey) {
	logger := log.FromContext(ctx)

	podList := &corev1.PodList{}
	if err := r.List(ctx, podList,
		client.InNamespace(v.Namespace),
		client.MatchingLabels(common.SelectorLabels(v, common.ComponentValkey)),
	); err != nil {
		logger.Info("Could not list pods to clear drain-promotion stamps; a stale stamp may "+
			"outrank the known-master annotation on a later pass", "cluster", v.Name, "error", err)
		return
	}

	// The cheapest of the pod doors: this is a Patch on whatever carries the selector
	// labels, with no network, no password and no DNS in the way (ADR 0020 D9). A
	// List error above is already handled as "clear nothing"; a StatefulSet this
	// Valkey does not own reaches the same place, because filterOwnedPods keeps
	// nothing against a nil StatefulSet.
	sts, err := r.ownedDataStatefulSet(ctx, v)
	if err != nil {
		logger.Info("Could not read the data StatefulSet to prove pod provenance; leaving the "+
			"drain-promotion stamps in place", "cluster", v.Name, "error", err)
		return
	}
	owned, _ := filterOwnedPods(podList.Items, sts)

	for i := range owned {
		pod := &owned[i]
		if _, stamped := pod.Annotations[common.AnnotationDrainPromotedAt]; !stamped {
			continue
		}
		base := pod.DeepCopy()
		delete(pod.Annotations, common.AnnotationDrainPromotedAt)
		if err := r.Patch(ctx, pod, client.MergeFrom(base)); err != nil {
			logger.Info("Could not clear the drain-promotion stamp; it may outrank the "+
				"known-master annotation on a later pass", "pod", pod.Name, "error", err)
		}
	}
}

// couldNotHaveSelfElected reports whether the init script could have given podName
// the master role without anybody promoting it.
//
// It could, in exactly two ways (internal/builder/statefulset.go, Phase 3): the
// ordinal fallback, which only ever applies to ordinal 0, and the ADR 0008 D8, D9 self-claim,
// which requires the mounted replica config to name the pod itself. So a pod with
// ordinal > 0 that the replica ConfigMap does not name was promoted from a live
// replica by somebody -- the drain handler, or a human.
//
// cmMaster is the pod name the LIVE replica ConfigMap names, empty when that could
// not be determined. Callers decide what "unknown" means for them: it must never
// justify an adoption, and it must always justify refusing a demotion.
//
// The narrow spot, deliberately not hidden: the self-claim reads the MOUNTED copy of
// that ConfigMap, and kubelet refreshes a projected volume on its sync loop (~1 min
// by default). An in-place sandbox recreation -- node reboot, kubelet restart -- can
// therefore re-run the init script against content the operator has already replaced,
// and self-claim off a name this function no longer sees. The window is bounded by
// that refresh interval and needs a sandbox recreation inside it.
func couldNotHaveSelfElected(podName, cmMaster string) bool {
	return podOrdinal(podName) > 0 && cmMaster != podName
}

// replicaConfigMaster returns the pod name the live replica ConfigMap points its
// replicaof directive at, and whether that could be determined at all.
//
// The LIVE ConfigMap, not the CR annotation the operator derives it from: the two
// diverge exactly where it matters. handleManualFailover republishes it
// best-effort, and syncSentinelWithMaster never republishes at all, so the
// annotation can already name pod-X while the ConfigMap still names pod-N -- and a
// restarting pod-N self-claims off the ConfigMap, not off the annotation. The read
// is cache-served (SetupWithManager Owns ConfigMaps), so it costs no API call.
//
// Any failure reports "unknown" rather than an empty name that would compare
// unequal to everything; see couldNotHaveSelfElected for what callers do with it.
func (r *ValkeyReconciler) replicaConfigMaster(ctx context.Context, v *vkov1.Valkey) (string, bool) {
	name := builder.ReplicaConfigMapName(v)
	cm := &corev1.ConfigMap{}
	if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: v.Namespace}, cm); err != nil {
		log.FromContext(ctx).Info("Cannot read the replica ConfigMap; treating the published master as unknown",
			"configMap", name, "error", err)
		return "", false
	}

	// A ConfigMap this Valkey does not control is treated as absent, exactly like
	// the foreign StatefulSet (docs/adr/0020-write-only-what-the-operator-owns.md,
	// D8). Without this the replicaof directive of a stranger's ConfigMap would
	// become this cluster's published master and feed the steady-state resolver,
	// which is the input that decides who gets demoted with REPLICAOF
	// (docs/adr/0011-evidence-based-steady-state-split-brain-resolution.md).
	// reconcileReplicaConfigMap is the one reporter; this path stays quiet.
	if !metav1.IsControlledBy(cm, v) {
		log.FromContext(ctx).Info("Replica ConfigMap is held by an object this Valkey does not control; "+
			"treating the published master as unknown", "configMap", name)
		return "", false
	}

	// The same parse the init script performs:
	//   grep '^replicaof ' valkey.conf | awk '{print $2}'
	for _, line := range strings.Split(cm.Data[builder.ValkeyConfigKey], "\n") {
		rest, found := strings.CutPrefix(strings.TrimSpace(line), "replicaof ")
		if !found {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			continue
		}
		return podNameFromHost(fields[0]), true
	}
	return "", false
}

// podOrdinal returns the StatefulSet ordinal encoded in a pod name, or -1 when the
// name does not carry one.
func podOrdinal(podName string) int {
	idx := strings.LastIndex(podName, "-")
	if idx < 0 {
		return -1
	}
	ordinal, err := strconv.Atoi(podName[idx+1:])
	if err != nil {
		return -1
	}
	return ordinal
}

// recordRefusedAdoption makes a refused adoption readable off the CR.
//
// Nothing is broken in the data plane: one pod is labeled master, so writes reach
// exactly one dataset. What is wrong is the record -- the annotation names a
// different pod -- and the operator will not fix it by guessing, because adopting
// the wrong pod republishes the replica ConfigMap and has the recorded master
// full-resync its own data away when it returns.
func (r *ValkeyReconciler) recordRefusedAdoption(ctx context.Context, v *vkov1.Valkey,
	pod, recorded string) {
	log.FromContext(ctx).Info("Refusing to adopt a master with no evidence of a promotion",
		"pod", pod, "knownMaster", recorded)

	r.recordEvent(v, corev1.EventTypeWarning, "MasterAdoptionRefused",
		"%s is the only pod labeled master and confirms the role, but the known master is recorded as "+
			"%s and nothing shows that %s was promoted: it carries no drain stamp, it could have "+
			"given itself the role (ordinal 0, or the replica ConfigMap names it, or that ConfigMap "+
			"could not be read), and %s either does not answer or still reports the master role "+
			"itself. The record stays on %s. An operator decides which dataset survives, then "+
			"corrects the known-master annotation.",
		pod, recorded, pod, recorded, recorded)
}

// recordRefusedDemotion makes a refusal readable off the CR.
//
// It is worth being explicit about what this achieves: nothing is resolved. Both
// pods stay master, both answer role:master, and the -rw Service keeps round-robining
// writes across two independent datasets. The operator only declines to "resolve" the
// split brain by destroying the newer of the two, which is what an unattended
// annotation would have it do. A visible split brain is strictly better than a
// silently discarded dataset, and it is what the operator did before the steady-state
// check existed.
//
// Resolving it needs a human, and the Event says how: decide which dataset survives,
// point the other pod at it with REPLICAOF, and correct the known-master annotation
// so the next pass agrees.
func (r *ValkeyReconciler) recordRefusedDemotion(ctx context.Context, v *vkov1.Valkey,
	rogue, authority, because string) {
	log.FromContext(ctx).Info("Refusing a demotion that would discard the writes of a drain window",
		"pod", rogue, "knownMaster", authority, "reason", because)

	r.recordEvent(v, corev1.EventTypeWarning, "SplitBrainDemotionRefused",
		"Refusing to demote %s toward the recorded master %s: %s, which is what a node drain the "+
			"operator could not record looks like. Demoting it would discard the writes it took since. "+
			"Both pods stay master until an operator points the one holding the unwanted dataset at the "+
			"other with REPLICAOF and corrects the known-master annotation.",
		rogue, authority, because)
}

// recordAmbiguousStamps makes the one outcome that has no automatic resolution
// readable off the CR: two pods each carrying a drain stamp and each still answering
// role:master.
//
// Every direction out of here is destructive. Consolidating onto either stamped pod
// discards the drain-window writes of the other, and consolidating onto the pod the
// annotation names -- what this used to fall through to -- discards both. The
// operator keeps the split visible and says which pods are involved instead.
func (r *ValkeyReconciler) recordAmbiguousStamps(ctx context.Context, v *vkov1.Valkey,
	stamped []*corev1.Pod) {
	names := make([]string, 0, len(stamped))
	for _, pod := range stamped {
		names = append(names, pod.Name)
	}
	joined := strings.Join(names, ", ")

	log.FromContext(ctx).Info("More than one pod carries a drain-promotion stamp and reports master; "+
		"the evidence is ambiguous, refusing to consolidate", "cluster", v.Name, "pods", names)

	r.recordEvent(v, corev1.EventTypeWarning, "SplitBrainDemotionRefused",
		"%s each carry a drain-promotion stamp and each report master, so two promotions happened that "+
			"the operator did not record and nothing says which dataset is the one to keep. Demoting "+
			"either would discard the writes of a drain window, so both stay master. An operator decides "+
			"which dataset survives, points the others at it with REPLICAOF and corrects the "+
			"known-master annotation.",
		joined)
}
