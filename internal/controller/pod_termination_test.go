package controller

import (
	"context"
	"crypto/tls"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/builder"
	"github.com/guided-traffic/valkey-operator/internal/common"
	valkeyclient "github.com/guided-traffic/valkey-operator/internal/valkeyclient"
)

// The rules of docs/adr/0026-a-pod-being-deleted-is-not-available.md.
//
// Every test here exists because the same blind spot -- kubelet keeps PodReady
// True for the whole termination of a pod whose readiness probe still passes --
// reached five different decisions of the rolling update. Six characterization
// tests recorded the old behaviour before the fix; the ones below are those six
// inverted, plus the shapes an adversarial re-review found afterwards.
//
// Fixture rule (and the trap it avoids): the fake client refuses an object that
// carries a DeletionTimestamp without a finalizer, and a pod that has one is
// undeletable -- deleteOwnedPod on it is a silent no-op. A test that seeds every
// pod that way would pass whether or not the gate exists. So the pod a test
// expects to survive is always a plain, genuinely deletable pod, and only the pod
// that is meant to be terminating carries the finalizer.

// outdatedValkeyImage is an image no fixture template carries, so a pod stamped
// with it genuinely needs an update. The CR default is 8.0, which is why setting a
// pod to "valkey/valkey:8.0" changes nothing.
const outdatedValkeyImage = "valkey/valkey:7.2"

// outdateValkeyContainer puts a pod off the persisted template by container name
// rather than by position: podNeedsUpdate compares the container called
// ValkeyContainerName, and containers[0] is not reliably it.
func outdateValkeyContainer(pod *corev1.Pod) {
	for i := range pod.Spec.Containers {
		if pod.Spec.Containers[i].Name == builder.ValkeyContainerName {
			pod.Spec.Containers[i].Image = outdatedValkeyImage
			return
		}
	}
	panic("pod has no " + builder.ValkeyContainerName + " container")
}

// clientOf returns the fake client the reconciler was built with, so a test can
// assert which pods actually survived a pass.
func clientOf(t *testing.T, r *ValkeyReconciler) client.Client {
	t.Helper()
	require.NotNil(t, r.Client)
	return r.Client
}

// terminatingFor puts a pod into the state the API server shows for a graceful
// delete issued `elapsed` ago: deletionTimestamp is now + grace - elapsed, because
// the field is the moment the deletion is *due*, not the moment it was requested
// (k8s.io/apiserver, pkg/registry/rest/delete.go).
func terminatingFor(pod *corev1.Pod, grace, elapsed time.Duration) {
	due := metav1.NewTime(time.Now().Add(grace - elapsed))
	pod.DeletionTimestamp = &due
	pod.Finalizers = []string{"foregroundDeletion"}
}

// terminatingState is the podState the collector builds for a pod that is being
// deleted and is still inside its graceful deadline.
func terminatingState(ps podState) podState {
	ps.terminating = true
	ps.terminatingSince = time.Now().Add(30 * time.Second)
	return ps
}

// stalledState is terminatingState for a pod that is past podTerminationOverrun.
func stalledState(ps podState) podState {
	ps.terminating = true
	ps.terminatingSince = time.Now().Add(-podTerminationOverrun - time.Minute)
	return ps
}

// --- E1: readiness answers one question, availability the other -------------

// TestCollectPodStates_TerminatingPodIsReachableButNotAvailable is the inverted
// first characterization test. It used to assert ps.ready == true on a pod the
// operator had just deleted, and every downstream "is it healthy" guard believed
// it.
func TestCollectPodStates_TerminatingPodIsReachableButNotAvailable(t *testing.T) {
	v, sts, pods := threeReplicaCluster(t)
	terminatingFor(pods[1], 75*time.Second, 0)

	r, _ := newTestReconciler(v, sts, pods[0], pods[1], pods[2])
	states, _, err := r.collectPodStates(context.Background(), v, sts)
	require.NoError(t, err)

	assert.True(t, states[1].readyCondition,
		"kubelet keeps PodReady True for the whole termination -- the fixture must reproduce that")
	assert.True(t, states[1].reachable(),
		"a terminating pod that still answers is still reachable")
	assert.False(t, states[1].available(),
		"a pod being deleted must never be counted as available")
	assert.False(t, states[1].terminatingSince.IsZero(),
		"the graceful deadline is carried on the state, so no reader has to dereference the pod")

	assert.True(t, states[0].available(), "the untouched pods are unaffected")
	assert.True(t, states[2].available())
}

func TestPodState_AvailableIsReachableMinusTerminating(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		ready, terminating   bool
		reachable, available bool
	}{
		{"ready and staying", true, false, true, true},
		{"ready and going", true, true, true, false},
		{"not ready and staying", false, false, false, false},
		{"not ready and going", false, true, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ps := podState{readyCondition: tc.ready, terminating: tc.terminating}
			assert.Equal(t, tc.reachable, ps.reachable())
			assert.Equal(t, tc.available, ps.available())
		})
	}
}

// --- E1: the sites that spend a pod ----------------------------------------

// TestFindPromotionCandidate_RefusesATerminatingPod is the inverted
// characterization test for consequence (a). The promoted pod takes REPLICAOF NO
// ONE, is recorded as the known master, and the outgoing master is deleted
// seconds later -- so promoting a pod that is on its way out loses the dataset on
// a cluster without persistence.
func TestFindPromotionCandidate_RefusesATerminatingPod(t *testing.T) {
	base := []podState{
		{name: "c-0", exists: true, readyCondition: true, isMaster: true},
		{name: "c-1", exists: true, readyCondition: true},
		{name: "c-2", exists: true, readyCondition: true},
	}

	assert.Equal(t, 1, findPromotionCandidate(base, 0),
		"control: the first updated replica away from pod-0 is the candidate")

	terminating := append([]podState(nil), base...)
	terminating[1] = terminatingState(terminating[1])
	assert.Equal(t, 2, findPromotionCandidate(terminating, 0),
		"a terminating replica is skipped in favour of a live one")

	allGoing := append([]podState(nil), terminating...)
	allGoing[2] = terminatingState(allGoing[2])
	assert.Equal(t, -1, findPromotionCandidate(allGoing, 0),
		"with no live candidate the promotion waits rather than picking a dying pod")
}

func TestWaitForReplicasReady_WaitsWhileAReplicaTerminates(t *testing.T) {
	v := newTestValkey("wfr", "default", func(v *vkov1.Valkey) { v.Spec.Replicas = 3 })
	r, _ := newTestReconciler(v)

	pods := []podState{
		{name: "wfr-0", exists: true, readyCondition: true, isMaster: true},
		terminatingState(podState{name: "wfr-1", exists: true, readyCondition: true}),
		{name: "wfr-2", exists: true, readyCondition: true},
	}

	result := r.waitForReplicasReady(context.Background(), v, pods, 0)
	require.NotNil(t, result, "the last gate in front of the promotion must not wave a dying replica through")
	assert.True(t, result.NeedsRequeue)
	assert.Nil(t, result.Error)
	assert.Empty(t, v.Annotations[annotationSyncWaitStarted],
		"waiting out a termination must not spend the sync budget of the replication waits")
}

// TestVerifyNewMasterReady_RefusesATerminatingNewMaster covers the Sentinel-path
// mirror of consequence (a): this is the gate immediately in front of the old
// master's delete.
func TestVerifyNewMasterReady_RefusesATerminatingNewMaster(t *testing.T) {
	v := newTestValkey("vnm", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
	})
	r, _ := newTestReconciler(v)
	// The DBSIZE check behind this loop must succeed, or the test would pass for
	// the wrong reason: an unreachable pod refuses too.
	addr := fakeValkeyServer(t)
	r.NewValkeyClientFn = func(_, _ string, _ *tls.Config) *valkeyclient.Client {
		return valkeyclient.New(addr)
	}
	checker := &mockInstanceChecker{
		replicationInfoFn: func(podName string) (*valkeyclient.ReplicationInfo, error) {
			if podName == "vnm-1" {
				return &valkeyclient.ReplicationInfo{Role: common.RoleMaster, ConnectedSlaves: 2}, nil
			}
			return &valkeyclient.ReplicationInfo{Role: common.RoleReplica}, nil
		},
	}

	pods := []podState{
		{name: "vnm-0", exists: true, readyCondition: true, needsUpdate: true},
		terminatingState(podState{name: "vnm-1", exists: true, readyCondition: true}),
		{name: "vnm-2", exists: true, readyCondition: true},
	}

	verified, result := r.verifyNewMasterReady(context.Background(), v, pods, checker)
	assert.False(t, verified, "a terminating pod must not be accepted as the new master")
	assert.True(t, result.NeedsRequeue)

	// Control: the same pod, not terminating, is accepted -- so the refusal above
	// is the availability guard and nothing else in the fixture.
	pods[1].terminating = false
	pods[1].terminatingSince = time.Time{}
	verified, _ = r.verifyNewMasterReady(context.Background(), v, pods, checker)
	assert.True(t, verified)
}

// TestSortReplicaCandidates_TerminatingPodStaysFirst pins the one thing this
// change must NOT do. Removing the terminating pod from the candidate list hands
// position 0 to the next live replica, so the operator deletes a second pod while
// the first is still terminating -- the exact invariant ADR 0007 D1 exists for.
// The terminating pod stays the candidate and is waited on.
func TestSortReplicaCandidates_TerminatingPodStaysFirst(t *testing.T) {
	older := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Name: "s-1", CreationTimestamp: metav1.NewTime(time.Now().Add(-time.Hour))}}
	younger := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Name: "s-2", CreationTimestamp: metav1.NewTime(time.Now())}}

	pods := []podState{
		{name: "s-0", exists: true, readyCondition: true, isMaster: true},
		{name: "s-1", pod: older, exists: true, readyCondition: true, needsUpdate: true},
		terminatingState(podState{name: "s-2", pod: younger, exists: true, readyCondition: true, needsUpdate: true}),
	}

	candidates := sortReplicaCandidates(pods)
	require.Len(t, candidates, 2)
	assert.Equal(t, "s-2", candidates[0].name,
		"the youngest-first sort is termination-blind on purpose")
	assert.True(t, candidates[0].terminating)
}

// --- E1: the sites that only talk to a pod (the ADR 0025 carve-out) ---------

// TestDemoteRogueMaster_StillDemotesATerminatingMaster is the one regression this
// change could plausibly have caused. A blanket readiness change would refuse the
// demotion of the outgoing master, which then keeps accepting writes as a master
// for the rest of its termination -- up to the 60 s cap of the drain hook, and
// with no write fencing on either side (T12).
func TestDemoteRogueMaster_StillDemotesATerminatingMaster(t *testing.T) {
	v := newTestValkey("drm", "default", func(v *vkov1.Valkey) { v.Spec.Replicas = 3 })
	r, _ := newTestReconciler(v)
	rec := recorderOf(t, r)

	rogue := terminatingState(podState{name: "drm-0", exists: true, readyCondition: true, isMaster: true})

	err := r.demoteRogueMaster(context.Background(), v, rogue, "drm-1")

	// The unit reconciler points every Valkey client at 127.0.0.1, so the REPLICAOF
	// itself fails with a connection error. What matters is which error: the
	// readiness refusal would never have sent the command at all.
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "not ready for demotion",
		"a terminating master that still answers must still be demoted (ADR 0025)")
	assert.Empty(t, rec.all(), "the demotion path reports nothing; the wrapper does")
}

// TestWaitForWriteSync_CountsATerminatingReplica: excluding a terminating replica
// lowers numReplicas, and at zero the function skips WAIT entirely. The exclusion
// would relax the gate in front of a promotion instead of tightening it.
func TestWaitForWriteSync_CountsATerminatingReplica(t *testing.T) {
	v := newTestValkey("wws", "default", func(v *vkov1.Valkey) { v.Spec.Replicas = 2 })
	r, _ := newTestReconciler(v)

	pods := []podState{
		{name: "wws-0", exists: true, readyCondition: true, isMaster: true},
		terminatingState(podState{name: "wws-1", exists: true, readyCondition: true}),
	}

	result := r.waitForWriteSync(context.Background(), v, pods, 0)
	require.NotNil(t, result,
		"the terminating replica still counts, so WAIT is attempted rather than skipped")
	assert.True(t, result.NeedsRequeue)
}

// --- E2: countUpdatedPods keeps counting, and the hold moves to the finalizer

// TestCountUpdatedPods_StillCountsATerminatingPod is the one characterization
// test that is NOT inverted, and the reason is a regression the fix would
// otherwise have introduced: handleRollingUpdate dispatches on
// updatedCount == totalPods with no state switch under it. Excluding a
// terminating pod there does not delay the completion, it drops the pass back
// into the post-failover state machine with a failover timestamp that is minutes
// old -- straight into the timed-out branch, which either resets Sentinel through
// a dying master (ADR 0022) or triggers a real failover on a healthy cluster.
func TestCountUpdatedPods_StillCountsATerminatingPod(t *testing.T) {
	pods := []podState{
		{name: "c-0", exists: true, readyCondition: true},
		terminatingState(podState{name: "c-1", exists: true, readyCondition: true}),
		{name: "c-2", exists: true, readyCondition: true},
	}
	assert.Equal(t, 3, countUpdatedPods(pods),
		"the dispatch predicate is computed exactly as before the change")
}

// TestFinalizeRollingUpdate_HoldsCompletionWhileADataPodTerminates is where the
// completion hold actually lives (E2). ADR 0024 made RollingUpdateComplete
// load-bearing for external sequencing, so it must not fire over a pod that is
// not running.
func TestFinalizeRollingUpdate_HoldsCompletionWhileADataPodTerminates(t *testing.T) {
	v := newTestValkey("frh", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
	})
	v.Annotations = map[string]string{annotationRollingUpdateState: stateReplacingMaster}
	r, _ := newTestReconciler(v)
	// A topology that would otherwise finalize: one master with both replicas
	// connected. Without this the test would pass on checkFinalizationTopology
	// waiting for a reply it never gets.
	r.InstanceChecker = &mockInstanceChecker{
		replicationInfoFn: func(podName string) (*valkeyclient.ReplicationInfo, error) {
			if podName == "frh-0" {
				return &valkeyclient.ReplicationInfo{Role: common.RoleMaster, ConnectedSlaves: 2}, nil
			}
			return &valkeyclient.ReplicationInfo{Role: common.RoleReplica, MasterLinkStatus: "up"}, nil
		},
	}
	rec := recorderOf(t, r)

	pods := []podState{
		{name: "frh-0", exists: true, readyCondition: true, isMaster: true},
		terminatingState(podState{name: "frh-1", exists: true, readyCondition: true}),
		{name: "frh-2", exists: true, readyCondition: true},
	}

	result := r.finalizeRollingUpdate(context.Background(), v, pods)

	assert.False(t, result.Completed)
	assert.True(t, result.NeedsRequeue)
	assert.Empty(t, rec.withReason("RollingUpdateComplete"),
		"the data tier completion marker must not fire over a terminating pod")
	assert.Equal(t, stateReplacingMaster, v.Annotations[annotationRollingUpdateState],
		"the rolling update state survives the hold, so its bounded waits keep being driven")
	assert.NotEmpty(t, v.Annotations[annotationFinalizationTimestamp],
		"the hold arms the finalization bound that caps it at finalizationStallTimeout")

	// Control: the same topology with nothing terminating completes.
	pods[1].terminating = false
	pods[1].terminatingSince = time.Time{}
	assert.True(t, r.finalizeRollingUpdate(context.Background(), v, pods).Completed)
	assert.Len(t, rec.withReason("RollingUpdateComplete"), 1)
}

// TestFinalizeRollingUpdate_CompletesOnceTheFinalizationBoundExpires is the other
// half: the hold is bounded, and expiry proceeds rather than stalling.
func TestFinalizeRollingUpdate_CompletesOnceTheFinalizationBoundExpires(t *testing.T) {
	v := newTestValkey("frb", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
	})
	v.Annotations = map[string]string{
		annotationRollingUpdateState:    stateReplacingMaster,
		annotationFinalizationTimestamp: time.Now().Add(-finalizationStallTimeout - time.Minute).UTC().Format(time.RFC3339),
	}
	r, c := newTestReconciler(v)
	require.NoError(t, c.Update(context.Background(), v))
	rec := recorderOf(t, r)

	pods := []podState{
		{name: "frb-0", exists: true, readyCondition: true, isMaster: true},
		terminatingState(podState{name: "frb-1", exists: true, readyCondition: true}),
		{name: "frb-2", exists: true, readyCondition: true},
	}

	result := r.finalizeRollingUpdate(context.Background(), v, pods)

	assert.True(t, result.Completed, "a stalled finalization proceeds best-effort rather than waiting forever")
	assert.Len(t, rec.withReason("RollingUpdateComplete"), 1)
}

// TestFinalizeMultiReplicaRollingUpdate_DoesNotHoldOnATerminatingPod records the
// deliberate asymmetry (E2): finalizeMultiReplicaRollingUpdate has no bound of its
// own, so holding there would create exactly the unbounded class this change
// exists to avoid. Consequence (c) is accepted on the non-Sentinel path.
func TestFinalizeMultiReplicaRollingUpdate_DoesNotHoldOnATerminatingPod(t *testing.T) {
	v := newTestValkey("fmr", "default", func(v *vkov1.Valkey) { v.Spec.Replicas = 3 })
	v.Annotations = map[string]string{annotationRollingUpdateState: stateRestoringTopology}
	r, _ := newTestReconciler(v)

	pods := []podState{
		{name: "fmr-0", exists: true, readyCondition: true, isMaster: true},
		terminatingState(podState{name: "fmr-1", exists: true, readyCondition: true}),
		{name: "fmr-2", exists: true, readyCondition: true},
	}

	result := r.finalizeMultiReplicaRollingUpdate(context.Background(), v, pods)
	assert.True(t, result.Completed)
}

// TestHandleRollingUpdate_TerminatingDataPodStillReachesTheFinalizer is the S1
// regression guard, and the one this change could most plausibly re-introduce:
// the Sentinel dispatcher must keep taking the `updatedCount == totalPods` branch
// while a data pod terminates, never fall through to handlePostFailover.
func TestHandleRollingUpdate_TerminatingDataPodStillReachesTheFinalizer(t *testing.T) {
	v := newTestValkey("hru", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
	})
	sts := stsForValkey(v)
	pods := []*corev1.Pod{
		podFromStsTemplate(v, sts, 0),
		podFromStsTemplate(v, sts, 1),
		podFromStsTemplate(v, sts, 2),
	}
	terminatingFor(pods[1], 75*time.Second, 0)

	r, c := newTestReconciler(v, sts, pods[0], pods[1], pods[2])
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: v.Name, Namespace: v.Namespace}, v))
	v.Annotations = map[string]string{
		annotationRollingUpdateState: stateReplacingMaster,
		annotationFailoverTimestamp:  time.Now().Add(-time.Hour).UTC().Format(time.RFC3339),
	}
	require.NoError(t, c.Update(context.Background(), v))

	result := r.handleRollingUpdate(context.Background(), v, sts)

	assert.True(t, result.NeedsRequeue)
	assert.Nil(t, result.Error)
	assert.Equal(t, stateReplacingMaster, v.Annotations[annotationRollingUpdateState],
		"the pass must not reach handleFailoverRetrigger and rewrite the state to failover-reset")
	assert.NotEqual(t, stateFailoverReset, v.Annotations[annotationRollingUpdateState])
}

// --- E3: the delete gate ----------------------------------------------------

// dataClusterForGate builds a non-Sentinel three-replica cluster whose pod-1 and
// pod-2 are genuinely deletable, so an assertion that one of them survived is
// about the gate and not about a finalizer.
func dataClusterForGate(t *testing.T, name string) (*vkov1.Valkey, *ValkeyReconciler, []*corev1.Pod) {
	t.Helper()
	v := newTestValkey(name, "default", func(v *vkov1.Valkey) { v.Spec.Replicas = 3 })
	sts := stsForValkey(v)
	pods := []*corev1.Pod{
		podFromStsTemplate(v, sts, 0),
		podFromStsTemplate(v, sts, 1),
		podFromStsTemplate(v, sts, 2),
	}
	r, _ := newTestReconciler(v, sts, pods[0], pods[1], pods[2])
	return v, r, pods
}

// TestReplaceNextReplica_TerminatingReplacedReplicaHoldsTheNextDelete is
// re-review Finding 1. verifyReplacedReplicasSynced is the data tier's own
// redundancy gate -- its comment says so -- and a replaced replica that is being
// deleted for an unrelated reason (chaos, eviction, node drain) passed it: Ready
// stays True and master_link_status:up until the process stops.
func TestReplaceNextReplica_TerminatingReplacedReplicaHoldsTheNextDelete(t *testing.T) {
	v, r, pods := dataClusterForGate(t, "f1")
	// The replaced replica reports healthy replication, which is exactly what makes
	// this shape dangerous: it answers Ready and master_link_status:up until the
	// process stops. Without it the test would pass on the probe failing.
	r.InstanceChecker = &mockInstanceChecker{
		replicationInfoFn: func(string) (*valkeyclient.ReplicationInfo, error) {
			return &valkeyclient.ReplicationInfo{Role: common.RoleReplica, MasterLinkStatus: "up"}, nil
		},
	}

	states := []podState{
		{name: pods[0].Name, pod: pods[0], exists: true, readyCondition: true, isMaster: true},
		// Already replaced, and now terminating for an unrelated reason.
		terminatingState(podState{name: pods[1].Name, pod: pods[1], exists: true, readyCondition: true}),
		// Still outdated -- the next candidate.
		{name: pods[2].Name, pod: pods[2], exists: true, readyCondition: true, needsUpdate: true},
	}

	result := r.replaceNextReplica(context.Background(), v, states)

	require.NotNil(t, result)
	assert.True(t, result.NeedsRequeue)
	assert.Nil(t, result.Error)
	assert.True(t, podExists(t, clientOf(t, r), pods[2].Name),
		"the next candidate must not be deleted while an already-replaced replica terminates")
}

// TestReplaceNextReplica_TerminatingOutdatedReplicaAwayFromPositionZeroHoldsTheDelete
// is re-review Finding 2. verifyReplacedReplicasSynced skips needsUpdate pods and
// candidates[0] is chosen by age, so a terminating outdated replica that is not
// the youngest blocked nothing at all before the gate.
func TestReplaceNextReplica_TerminatingOutdatedReplicaAwayFromPositionZeroHoldsTheDelete(t *testing.T) {
	v, r, pods := dataClusterForGate(t, "f2")
	c := clientOf(t, r)

	pods[1].CreationTimestamp = metav1.NewTime(time.Now().Add(-time.Hour)) // older
	pods[2].CreationTimestamp = metav1.NewTime(time.Now())                 // younger -> candidates[0]

	states := []podState{
		{name: pods[0].Name, pod: pods[0], exists: true, readyCondition: true, isMaster: true},
		terminatingState(podState{name: pods[1].Name, pod: pods[1], exists: true, readyCondition: true, needsUpdate: true}),
		{name: pods[2].Name, pod: pods[2], exists: true, readyCondition: true, needsUpdate: true},
	}

	result := r.replaceNextReplica(context.Background(), v, states)

	require.NotNil(t, result)
	assert.True(t, result.NeedsRequeue)
	assert.Nil(t, result.Error)
	assert.True(t, podExists(t, c, pods[2].Name),
		"the younger candidate must not be deleted while another replica of the tier terminates")
}

// TestReplaceNextReplica_TerminationWaitDoesNotArmTheSyncWaitBound is S3. A gate
// above verifyReplacedReplicasSynced would leave that bound armed and ageing
// unobserved; a naive one below it would arm the bound against the terminating
// pod. Either way the next genuine sync wait inherits a spent budget and lands in
// pauseRollingUpdate -- a Warning Event plus clearRollingUpdateState, the handover
// ADR 0010 D2-D4 forbids.
func TestReplaceNextReplica_TerminationWaitDoesNotArmTheSyncWaitBound(t *testing.T) {
	v, r, pods := dataClusterForGate(t, "f3")
	rec := recorderOf(t, r)

	pods[1].CreationTimestamp = metav1.NewTime(time.Now().Add(-time.Hour))
	pods[2].CreationTimestamp = metav1.NewTime(time.Now())

	states := []podState{
		{name: pods[0].Name, pod: pods[0], exists: true, readyCondition: true, isMaster: true},
		terminatingState(podState{name: pods[1].Name, pod: pods[1], exists: true, readyCondition: true, needsUpdate: true}),
		{name: pods[2].Name, pod: pods[2], exists: true, readyCondition: true, needsUpdate: true},
	}

	require.NotNil(t, r.replaceNextReplica(context.Background(), v, states))

	assert.Empty(t, v.Annotations[annotationSyncWaitStarted],
		"a termination wait must not consume the replication sync budget")
	_, tracked := r.nudges.firstSeen(waitBoundKey(v.Namespace, v.Name, boundSyncWait))
	assert.False(t, tracked, "and must not arm the in-memory copy either")
	assert.Empty(t, rec.withReason("RollingUpdatePaused"))
}

// TestReplaceNextReplica_ReturnsNilWhenNothingIsLeftToReplace pins the second
// half of S3: this function's nil is what both dispatchers read as "advance", so
// a gate at the function head would make handleMasterFailover and
// handleManualFailover unreachable.
func TestReplaceNextReplica_ReturnsNilWhenNothingIsLeftToReplace(t *testing.T) {
	v, r, pods := dataClusterForGate(t, "f4")
	r.InstanceChecker = &mockInstanceChecker{
		replicationInfoFn: func(string) (*valkeyclient.ReplicationInfo, error) {
			return &valkeyclient.ReplicationInfo{Role: common.RoleReplica, MasterLinkStatus: "up"}, nil
		},
	}

	states := []podState{
		{name: pods[0].Name, pod: pods[0], exists: true, readyCondition: true, isMaster: true, needsUpdate: true},
		{name: pods[1].Name, pod: pods[1], exists: true, readyCondition: true},
		{name: pods[2].Name, pod: pods[2], exists: true, readyCondition: true},
	}

	assert.Nil(t, r.replaceNextReplica(context.Background(), v, states),
		"only the master is left, so the failover path must be reachable")
}

// TestDeleteNextPendingPod_DeletesNothingWhileAPendingPodTerminates is re-review
// Finding 3. The loop skips to the next pod rather than waiting, so without the
// gate the second pending pod is deleted while the first terminates.
func TestDeleteNextPendingPod_DeletesNothingWhileAPendingPodTerminates(t *testing.T) {
	v, r, pods := dataClusterForGate(t, "f5")
	c := clientOf(t, r)

	states := []podState{
		{name: pods[0].Name, pod: pods[0], exists: true, readyCondition: true, needsUpdate: true, isMaster: true},
		terminatingState(podState{name: pods[1].Name, pod: pods[1], exists: true, readyCondition: true, needsUpdate: true}),
		{name: pods[2].Name, pod: pods[2], exists: true, readyCondition: true, needsUpdate: true},
	}

	result := r.deleteNextPendingPod(context.Background(), v, states)

	assert.True(t, result.NeedsRequeue)
	assert.Nil(t, result.Error)
	assert.True(t, podExists(t, c, pods[0].Name), "no pod is deleted while one of the tier terminates")
	assert.True(t, podExists(t, c, pods[2].Name))
}

// TestReplaceRemainingPods_HoldsWhileAnotherPodTerminates covers the third data
// delete. The gate sits inside the loop, not at the function head, because
// handleMasterWithNoReplicas clears the reconnect counter and then tail calls
// this function.
func TestReplaceRemainingPods_HoldsWhileAnotherPodTerminates(t *testing.T) {
	v, r, pods := dataClusterForGate(t, "f6")
	c := clientOf(t, r)

	states := []podState{
		{name: pods[0].Name, pod: pods[0], exists: true, readyCondition: true, needsUpdate: true},
		terminatingState(podState{name: pods[1].Name, pod: pods[1], exists: true, readyCondition: true}),
		{name: pods[2].Name, pod: pods[2], exists: true, readyCondition: true},
	}

	result := r.replaceRemainingPods(context.Background(), v, states)

	assert.True(t, result.NeedsRequeue)
	assert.Nil(t, result.Error)
	assert.True(t, podExists(t, c, pods[0].Name))
	assert.Empty(t, v.Annotations[annotationRollingUpdateState],
		"the hold happens before the state write, so a held pass does not advance the state machine")
}

// TestHandleMasterWithNoReplicas_StillReachesReplaceRemainingPods is the S3
// tail-call case: after maxReconnectResets the reconnect counter is cleared and
// this function proceeds. A gate at replaceRemainingPods' head would have
// discarded that call, restarted the counter at zero and reopened the infinite
// retry loop the code comment claims to break.
func TestHandleMasterWithNoReplicas_StillReachesReplaceRemainingPods(t *testing.T) {
	v, r, pods := dataClusterForGate(t, "f7")
	c := clientOf(t, r)
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: v.Name, Namespace: v.Namespace}, v))
	v.Annotations = map[string]string{
		annotationFailoverTimestamp:   time.Now().Add(-2 * replicaReconnectTimeout).UTC().Format(time.RFC3339),
		annotationReconnectResetCount: fmt.Sprintf("%d", maxReconnectResets),
	}
	require.NoError(t, c.Update(context.Background(), v))

	states := []podState{
		{name: pods[0].Name, pod: pods[0], exists: true, readyCondition: true, needsUpdate: true},
		{name: pods[1].Name, pod: pods[1], exists: true, readyCondition: true},
		{name: pods[2].Name, pod: pods[2], exists: true, readyCondition: true},
	}

	result := r.handleMasterWithNoReplicas(context.Background(), v, states[1], states)

	assert.True(t, result.NeedsRequeue)
	assert.Empty(t, v.Annotations[annotationReconnectResetCount],
		"the counter is cleared exactly once, on the pass that proceeds")
	assert.False(t, podExists(t, c, pods[0].Name),
		"nothing is terminating, so the tail call reaches the delete it exists for")
}

// --- E3/E4: the manual-failover exemption -----------------------------------

// TestHandleManualFailover_OldMasterDeleteIsExemptFromTheGate records E4 as a
// test rather than as a comment, and pins what makes the exemption safe.
//
// On this path the only pod that can still be terminating when the delete is
// reached is the master itself: waitForReplicasReady runs first and refuses every
// *other* terminating pod of the tier one layer up. So the exemption never widens
// the two-down risk -- the promotion has already happened, and holding the delete
// of a master whose best-effort demotion may have failed would extend a genuine
// two-master state toward splitBrainWarnAfter = 90 s, the edge the e2e asserts on.
func TestHandleManualFailover_OldMasterDeleteIsExemptFromTheGate(t *testing.T) {
	v := newTestValkey("e4", "default", func(v *vkov1.Valkey) { v.Spec.Replicas = 3 })
	sts := stsForValkey(v)
	pod0 := podFromStsTemplate(v, sts, 0) // the outgoing master, outdated and dying
	pod1 := podFromStsTemplate(v, sts, 1) // the promotion candidate
	pod2 := podFromStsTemplate(v, sts, 2)
	outdateValkeyContainer(pod0)
	terminatingFor(pod0, 75*time.Second, 0)
	replicaCM := builder.BuildReplicaConfigMap(v)
	controllerRefTo(v, replicaCM)

	var deleted []string
	record := interceptor.Funcs{
		Delete: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
			if pod, ok := obj.(*corev1.Pod); ok {
				deleted = append(deleted, pod.Name)
			}
			return cl.Delete(ctx, obj, opts...)
		},
	}
	r, _ := newInterceptedReconciler(record, v, sts, replicaCM, pod0, pod1, pod2)
	addr := fakeValkeyServer(t)
	r.NewValkeyClientFn = func(_, _ string, _ *tls.Config) *valkeyclient.Client {
		return valkeyclient.New(addr)
	}
	r.InstanceChecker = &mockInstanceChecker{
		replicationInfoFn: func(string) (*valkeyclient.ReplicationInfo, error) {
			return &valkeyclient.ReplicationInfo{
				Role: common.RoleReplica, MasterLinkStatus: "up", ConnectedSlaves: 2,
			}, nil
		},
	}

	states := []podState{
		terminatingState(podState{
			name: pod0.Name, pod: pod0, exists: true, readyCondition: true,
			isMaster: true, needsUpdate: true,
		}),
		{name: pod1.Name, pod: pod1, exists: true, readyCondition: true},
		{name: pod2.Name, pod: pod2, exists: true, readyCondition: true},
	}

	result := r.handleManualFailover(context.Background(), v, states, 0)

	require.Nil(t, result.Error)
	assert.Contains(t, deleted, pod0.Name,
		"the delete of the master this function just failed over from is not gated")
}

// TestHandleManualFailover_TerminatingReplicaIsRefusedOneLayerUp is the other
// half of the exemption argument: a terminating pod that is NOT the master never
// reaches the exempt delete at all.
func TestHandleManualFailover_TerminatingReplicaIsRefusedOneLayerUp(t *testing.T) {
	v := newTestValkey("e4b", "default", func(v *vkov1.Valkey) { v.Spec.Replicas = 3 })
	sts := stsForValkey(v)
	pod0 := podFromStsTemplate(v, sts, 0)
	pod1 := podFromStsTemplate(v, sts, 1)
	pod2 := podFromStsTemplate(v, sts, 2)
	outdateValkeyContainer(pod0)

	var deleted []string
	record := interceptor.Funcs{
		Delete: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
			if pod, ok := obj.(*corev1.Pod); ok {
				deleted = append(deleted, pod.Name)
			}
			return cl.Delete(ctx, obj, opts...)
		},
	}
	r, _ := newInterceptedReconciler(record, v, sts, pod0, pod1, pod2)
	// A reachable Valkey on every address, so that the promotion and the delete
	// would really happen if the guard were not there.
	addr := fakeValkeyServer(t)
	r.NewValkeyClientFn = func(_, _ string, _ *tls.Config) *valkeyclient.Client {
		return valkeyclient.New(addr)
	}
	r.InstanceChecker = &mockInstanceChecker{
		replicationInfoFn: func(string) (*valkeyclient.ReplicationInfo, error) {
			return &valkeyclient.ReplicationInfo{
				Role: common.RoleReplica, MasterLinkStatus: "up", ConnectedSlaves: 2,
			}, nil
		},
	}

	states := []podState{
		{name: pod0.Name, pod: pod0, exists: true, readyCondition: true, isMaster: true, needsUpdate: true},
		terminatingState(podState{name: pod1.Name, pod: pod1, exists: true, readyCondition: true}),
		{name: pod2.Name, pod: pod2, exists: true, readyCondition: true},
	}

	result := r.handleManualFailover(context.Background(), v, states, 0)

	assert.True(t, result.NeedsRequeue)
	assert.Empty(t, deleted, "no promotion and no delete while a replica of the tier terminates")
	assert.Empty(t, v.Annotations[annotationPromotedPod])
}

// --- E6/E3: the Sentinel tier ----------------------------------------------

// sentinelTierForGate builds a Sentinel tier of `replicas` pods on the old image,
// with the ordinals in `upToDate` already on the desired one. Only
// `terminatingOrdinal` carries a DeletionTimestamp (and therefore the finalizer
// that keeps it visible); every other pod is genuinely deletable, so an assertion
// that one survived is about the gate rather than about a finalizer. Pass -1 for
// no terminating pod.
func sentinelTierForGate(t *testing.T, name string, replicas int32, terminatingOrdinal int,
	upToDate map[int]bool) (*vkov1.Valkey, *ValkeyReconciler, []*corev1.Pod) {
	t.Helper()
	v := newTestValkey(name, "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: replicas}
	})
	sts := buildTestSentinelSts(v)

	pods := make([]*corev1.Pod, replicas)
	objs := make([]client.Object, 0, 2+int(replicas))
	objs = append(objs, v, sts)
	for i := range pods {
		img := "valkey/valkey:8.0"
		if upToDate[i] {
			img = sentinelTestNewImage
		}
		pods[i] = createSentinelPod(v, i, img, true)
		if i == terminatingOrdinal {
			terminatingFor(pods[i], 30*time.Second, 0)
		}
		objs = append(objs, pods[i])
	}

	r, _ := newTestReconciler(objs...)
	return v, r, pods
}

// TestScanSentinelPods_TerminatingSentinelCountsForNeitherCounter pins E6 on its
// own: the quorum guard and the delete gate each block this shape by themselves,
// so only a scan-level assertion shows which of them is doing it.
func TestScanSentinelPods_TerminatingSentinelCountsForNeitherCounter(t *testing.T) {
	v, r, _ := sentinelTierForGate(t, "sg0", 3, 1, map[int]bool{0: true, 1: true, 2: true})
	sts := &appsv1.StatefulSet{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{
		Name: common.StatefulSetName(v, common.ComponentSentinel), Namespace: v.Namespace}, sts))

	scan, err := r.scanSentinelPods(context.Background(), v, sts)
	require.NoError(t, err)

	assert.Equal(t, 2, scan.readyCount,
		"a Sentinel that is being deleted must not hold up a quorum it is about to leave")
	assert.Equal(t, 2, scan.updatedReadyCount,
		"and must not be counted as converged by the ADR 0024 completion marker")
	assert.Equal(t, "sg0-sentinel-1", scan.terminating.name)
	assert.False(t, scan.terminating.since.IsZero())
	assert.Nil(t, scan.firstOutdatedPod)
}

// TestSentinelRollingUpdate_TerminatingUpToDateSentinelDoesNotAuthoriseASecondDelete
// is the inverted characterization test for consequence (b). With three sentinels
// and quorum two, a terminating-but-Ready sentinel used to make readyCount = 3, so
// readyCount-1 = 2 >= 2 authorised deleting a second one -- leaving one live
// Sentinel of three and no quorum for a failover for the union of both
// termination windows (ADR 0004, ADR 0022).
func TestSentinelRollingUpdate_TerminatingUpToDateSentinelDoesNotAuthoriseASecondDelete(t *testing.T) {
	// pod-1 is already on the new image and terminating (chaos, eviction, drain);
	// pod-0 and pod-2 are outdated, so firstOutdatedPod points at pod-0.
	v, r, pods := sentinelTierForGate(t, "sg1", 3, 1, map[int]bool{1: true})
	c := clientOf(t, r)

	result := r.checkAndHandleSentinelRollingUpdate(context.Background(), v)

	assert.True(t, result.NeedsRequeue)
	assert.Nil(t, result.Error)
	assert.True(t, podExists(t, c, pods[0].Name),
		"the stale Ready of a terminating sentinel is what used to authorise this delete")
}

// TestSentinelRollingUpdate_NoSecondDeleteAtFiveReplicas is re-review Finding 4:
// the shape where the quorum guard alone would permit the second delete. Five
// sentinels, quorum three, one terminating -- readyCount would be 4 and
// 4-1 = 3 >= 3.
func TestSentinelRollingUpdate_NoSecondDeleteAtFiveReplicas(t *testing.T) {
	v, r, pods := sentinelTierForGate(t, "sg2", 5, 1, map[int]bool{1: true})
	c := clientOf(t, r)

	result := r.checkAndHandleSentinelRollingUpdate(context.Background(), v)

	assert.True(t, result.NeedsRequeue)
	assert.Nil(t, result.Error)
	assert.True(t, podExists(t, c, pods[0].Name),
		"the delete gate keeps the Sentinel roll serialized where the quorum arithmetic would not")
}

// TestSentinelRollingUpdate_TerminatingOutdatedSentinelStaysTheSelection mirrors
// the data tier's candidates[0] rule: firstOutdatedPod keeps selecting the
// terminating outdated pod, and the gate waits on it instead of skipping to a
// live one.
func TestSentinelRollingUpdate_TerminatingOutdatedSentinelStaysTheSelection(t *testing.T) {
	v, r, pods := sentinelTierForGate(t, "sg3", 3, 0, map[int]bool{})
	c := clientOf(t, r)

	result := r.checkAndHandleSentinelRollingUpdate(context.Background(), v)

	assert.True(t, result.NeedsRequeue)
	assert.Nil(t, result.Error)
	assert.True(t, podExists(t, c, pods[1].Name), "no other sentinel is deleted in its place")
	assert.True(t, podExists(t, c, pods[2].Name))
}

// TestSentinelRollingUpdate_CompletionWaitsOutATerminatingSentinel is E6: one
// predicate feeds the quorum guard and the SentinelUpdatePending flip, so the
// completion marker no longer fires while a Sentinel pod is on its way out. That
// changes a marker ADR 0024 made load-bearing for external sequencing, which is
// why the ADR says so.
func TestSentinelRollingUpdate_CompletionWaitsOutATerminatingSentinel(t *testing.T) {
	v, r, _ := sentinelTierForGate(t, "sg4", 3, 1, map[int]bool{0: true, 1: true, 2: true})
	c := clientOf(t, r)
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: v.Name, Namespace: v.Namespace}, v))
	v.Status.Conditions = []metav1.Condition{{
		Type:               vkov1.ConditionTypeSentinelUpdatePending,
		Status:             metav1.ConditionTrue,
		Reason:             vkov1.ReasonSentinelPodsOutdated,
		Message:            "Sentinel rolling update in progress: 2/3 pods updated and ready",
		LastTransitionTime: metav1.Now(),
	}}
	require.NoError(t, c.Status().Update(context.Background(), v))
	rec := recorderOf(t, r)

	result := r.checkAndHandleSentinelRollingUpdate(context.Background(), v)

	assert.True(t, result.NeedsRequeue)
	assert.Empty(t, rec.withReason("SentinelUpdateComplete"),
		"the tier is not converged while one of its pods is terminating")
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: v.Name, Namespace: v.Namespace}, v))
	cond := meta.FindStatusCondition(v.Status.Conditions, vkov1.ConditionTypeSentinelUpdatePending)
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionTrue, cond.Status)
}

// --- E5: the refusal stays unbounded, the observation does not --------------

func TestHoldDeleteWhileTerminating_InsideTheOverrunEndsThePass(t *testing.T) {
	v := newTestValkey("e5a", "default")
	r, _ := newTestReconciler(v)
	rec := recorderOf(t, r)

	pods := []podState{terminatingState(podState{name: "e5a-0", exists: true, readyCondition: true})}
	result := r.holdDeleteWhileTerminating(context.Background(), v, common.ComponentValkey, firstTerminatingPod(pods))

	require.NotNil(t, result)
	assert.True(t, result.NeedsRequeue)
	assert.Zero(t, result.DeferredRequeueAfter)
	assert.Empty(t, rec.all(), "a clean roll emits no Event for a wait that lasts about a second")
	assert.Nil(t, meta.FindStatusCondition(v.Status.Conditions, vkov1.ConditionTypePodTerminationStalled),
		"the condition is the stall marker, not the wait marker")
}

func TestHoldDeleteWhileTerminating_PastTheOverrunDefersInsteadOfEndingThePass(t *testing.T) {
	v := newTestValkey("e5b", "default")
	r, c := newTestReconciler(v)
	rec := recorderOf(t, r)

	pods := []podState{stalledState(podState{name: "e5b-0", exists: true, readyCondition: true})}
	result := r.holdDeleteWhileTerminating(context.Background(), v, common.ComponentValkey, firstTerminatingPod(pods))

	require.NotNil(t, result)
	assert.False(t, result.NeedsRequeue,
		"the pass tail -- Sentinel roll, no-master recovery, steady-state split brain, status -- must run again")
	assert.Equal(t, rollingUpdateRequeueDelay, result.DeferredRequeueAfter)
	assert.Empty(t, rec.all(),
		"ADR 0025 promises a clean rolling update emits zero Warnings; the stall reports through the condition")

	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: v.Name, Namespace: v.Namespace}, v))
	cond := meta.FindStatusCondition(v.Status.Conditions, vkov1.ConditionTypePodTerminationStalled)
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionTrue, cond.Status)
	assert.Equal(t, vkov1.ReasonPodStuckTerminating, cond.Reason)
	assert.Contains(t, cond.Message, "e5b-0", "the condition names the pod that is stuck")
}

// TestHoldDeleteWhileTerminating_StalledStillRefusesTheDelete is the half of E5
// that must not drift: expiry restores the pass tail, never the delete.
func TestHoldDeleteWhileTerminating_StalledStillRefusesTheDelete(t *testing.T) {
	v, r, pods := dataClusterForGate(t, "e5c")
	c := clientOf(t, r)

	states := []podState{
		{name: pods[0].Name, pod: pods[0], exists: true, readyCondition: true, needsUpdate: true, isMaster: true},
		stalledState(podState{name: pods[1].Name, pod: pods[1], exists: true, readyCondition: true, needsUpdate: true}),
		{name: pods[2].Name, pod: pods[2], exists: true, readyCondition: true, needsUpdate: true},
	}

	result := r.deleteNextPendingPod(context.Background(), v, states)

	assert.False(t, result.NeedsRequeue)
	assert.Equal(t, rollingUpdateRequeueDelay, result.DeferredRequeueAfter)
	assert.True(t, podExists(t, c, pods[0].Name), "a stalled termination never resumes the delete")
	assert.True(t, podExists(t, c, pods[2].Name))
}

func TestHoldDeleteWhileTerminating_ClearsTheConditionWhenNothingTerminates(t *testing.T) {
	v := newTestValkey("e5d", "default")
	r, c := newTestReconciler(v)
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: v.Name, Namespace: v.Namespace}, v))
	v.Status.Conditions = []metav1.Condition{{
		Type:               vkov1.ConditionTypePodTerminationStalled,
		Status:             metav1.ConditionTrue,
		Reason:             vkov1.ReasonPodStuckTerminating,
		Message:            "e5d-0 is stuck",
		LastTransitionTime: metav1.Now(),
	}}
	require.NoError(t, c.Status().Update(context.Background(), v))

	pods := []podState{{name: "e5d-0", exists: true, readyCondition: true}}
	assert.Nil(t, r.holdDeleteWhileTerminating(context.Background(), v, common.ComponentValkey, firstTerminatingPod(pods)))

	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: v.Name, Namespace: v.Namespace}, v))
	cond := meta.FindStatusCondition(v.Status.Conditions, vkov1.ConditionTypePodTerminationStalled)
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionFalse, cond.Status)
	assert.Equal(t, vkov1.ReasonPodTerminationCleared, cond.Reason)
}

// TestHoldDeleteWhileTerminating_DoesNotStampTheConditionOnACleanCR is the
// presence guard: meta.SetStatusCondition adds an absent condition and reports a
// change, so an unconditional clear would write PodTerminationStalled=False onto
// every cluster in the fleet on the next upgrade.
func TestHoldDeleteWhileTerminating_DoesNotStampTheConditionOnACleanCR(t *testing.T) {
	v := newTestValkey("e5e", "default")
	r, c := newTestReconciler(v)

	pods := []podState{{name: "e5e-0", exists: true, readyCondition: true}}
	assert.Nil(t, r.holdDeleteWhileTerminating(context.Background(), v, common.ComponentValkey, firstTerminatingPod(pods)))

	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: v.Name, Namespace: v.Namespace}, v))
	assert.Nil(t, meta.FindStatusCondition(v.Status.Conditions, vkov1.ConditionTypePodTerminationStalled),
		"a cluster that never stalled must not gain the condition")
}

// TestReconcileWorkload_StalledTerminationStillRunsTheSentinelRoll is the S4 fix
// end to end: a NeedsRequeue return leaves everything after it suspended, and on
// a NotReady node the DeletionTimestamp never clears, so that blackout is
// permanent. Past the overrun the pass tail runs again.
func TestReconcileWorkload_StalledTerminationStillRunsTheSentinelRoll(t *testing.T) {
	v := newTestValkey("e5f", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
	})
	sts := stsForValkey(v)
	dataPods := []*corev1.Pod{
		podFromStsTemplate(v, sts, 0),
		podFromStsTemplate(v, sts, 1),
		podFromStsTemplate(v, sts, 2),
	}
	// pod-2 still needs the update; pod-1 is wedged Terminating well past its
	// graceful deadline, which is what holds the data roll.
	outdateValkeyContainer(dataPods[2])
	terminatingFor(dataPods[1], 75*time.Second, 75*time.Second+podTerminationOverrun+time.Minute)

	sentinelSts := buildTestSentinelSts(v)
	sentinelPods := []*corev1.Pod{
		createSentinelPod(v, 0, "valkey/valkey:8.0", true),
		createSentinelPod(v, 1, sentinelTestNewImage, true),
		createSentinelPod(v, 2, sentinelTestNewImage, true),
	}

	r, c := newTestReconciler(v, sts, dataPods[0], dataPods[1], dataPods[2],
		sentinelSts, sentinelPods[0], sentinelPods[1], sentinelPods[2])
	// Healthy replication everywhere, so the pass reaches the terminating pod
	// instead of stopping on an unanswered probe one pod earlier.
	r.InstanceChecker = &mockInstanceChecker{
		replicationInfoFn: func(string) (*valkeyclient.ReplicationInfo, error) {
			return &valkeyclient.ReplicationInfo{Role: common.RoleReplica, MasterLinkStatus: "up"}, nil
		},
	}

	_, err := r.reconcileWorkload(context.Background(), v)
	require.NoError(t, err)

	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: v.Name, Namespace: v.Namespace}, v))
	assert.NotNil(t, meta.FindStatusCondition(v.Status.Conditions, vkov1.ConditionTypeSentinelUpdatePending),
		"the Sentinel roll sits behind the rolling-update return and must be reached while the data tier stalls")
	assert.True(t, podExists(t, c, dataPods[2].Name),
		"and the data delete is still refused")
}

// TestWaitForUnavailablePod_BootingPodStillGetsThePlainRequeue is the control
// for the split: only a terminating pod earns the bounded observation. A booting
// pod keeps the wait it always had, and never touches the stall condition.
func TestWaitForUnavailablePod_BootingPodStillGetsThePlainRequeue(t *testing.T) {
	v := newTestValkey("wup", "default")
	r, c := newTestReconciler(v)

	booting := podState{name: "wup-0", exists: true, readyCondition: false}
	result := r.waitForUnavailablePod(context.Background(), v, booting, "booting")

	require.NotNil(t, result)
	assert.True(t, result.NeedsRequeue)
	assert.Zero(t, result.DeferredRequeueAfter)
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: v.Name, Namespace: v.Namespace}, v))
	assert.Nil(t, meta.FindStatusCondition(v.Status.Conditions, vkov1.ConditionTypePodTerminationStalled))
}

// TestVerifyNewMasterReady_StalledTerminatingCandidateDefers closes the last
// termination wait this change created: the loop skips a terminating candidate,
// and its tail would otherwise add one more member to an unbounded requeue.
func TestVerifyNewMasterReady_StalledTerminatingCandidateDefers(t *testing.T) {
	v := newTestValkey("vnm2", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
	})
	r, _ := newTestReconciler(v)
	checker := &mockInstanceChecker{
		replicationInfoFn: func(string) (*valkeyclient.ReplicationInfo, error) {
			return &valkeyclient.ReplicationInfo{Role: common.RoleMaster, ConnectedSlaves: 2}, nil
		},
	}

	pods := []podState{
		{name: "vnm2-0", exists: true, readyCondition: true, needsUpdate: true},
		stalledState(podState{name: "vnm2-1", exists: true, readyCondition: true}),
		{name: "vnm2-2", exists: true, readyCondition: true, needsUpdate: true},
	}

	verified, result := r.verifyNewMasterReady(context.Background(), v, pods, checker)
	assert.False(t, verified)
	assert.False(t, result.NeedsRequeue)
	assert.Equal(t, rollingUpdateRequeueDelay, result.DeferredRequeueAfter)
}

// TestClearRollingUpdateState_ClearsAStandingStallCondition closes the one path
// with no delete site left: the stall is on the *last* pod the roll replaces, the
// pod returns, every pod is current, and the pass finalizes without ever passing a
// gate again. Without the clear here the condition would stand forever -- permanent
// status drift on a healthy cluster.
func TestClearRollingUpdateState_ClearsAStandingStallCondition(t *testing.T) {
	v := newTestValkey("crs", "default")
	r, c := newTestReconciler(v)
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: v.Name, Namespace: v.Namespace}, v))
	v.Annotations = map[string]string{annotationRollingUpdateState: stateReplacingReplicas}
	require.NoError(t, c.Update(context.Background(), v))
	v.Status.Conditions = []metav1.Condition{{
		Type:               vkov1.ConditionTypePodTerminationStalled,
		Status:             metav1.ConditionTrue,
		Reason:             vkov1.ReasonPodStuckTerminating,
		Message:            "crs-2 is stuck",
		LastTransitionTime: metav1.Now(),
	}}
	require.NoError(t, c.Status().Update(context.Background(), v))

	require.NoError(t, r.clearRollingUpdateState(context.Background(), v))

	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: v.Name, Namespace: v.Namespace}, v))
	cond := meta.FindStatusCondition(v.Status.Conditions, vkov1.ConditionTypePodTerminationStalled)
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionFalse, cond.Status)
}

// TestHandleStandaloneRollingUpdate_DoesNotReDeleteATerminatingPod: a single-pod
// tier is a tier too, and it is where the duplicate delete that started this whole
// item was cheapest to observe.
func TestHandleStandaloneRollingUpdate_DoesNotReDeleteATerminatingPod(t *testing.T) {
	v := newTestValkey("std", "default", func(v *vkov1.Valkey) { v.Spec.Replicas = 1 })
	sts := stsForValkey(v)
	pod := podFromStsTemplate(v, sts, 0)
	outdateValkeyContainer(pod)
	terminatingFor(pod, 30*time.Second, 0)

	var deleted []string
	record := interceptor.Funcs{
		Delete: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
			if p, ok := obj.(*corev1.Pod); ok {
				deleted = append(deleted, p.Name)
			}
			return cl.Delete(ctx, obj, opts...)
		},
	}
	r, _ := newInterceptedReconciler(record, v, sts, pod)

	result := r.handleStandaloneRollingUpdate(context.Background(), v, sts)

	assert.True(t, result.NeedsRequeue)
	assert.Nil(t, result.Error)
	assert.Empty(t, deleted, "the only pod is already on its way out; deleting it again is a no-op API call")
}

// --- S7: the tier is an ordinal range, never a label selector ---------------

// TestCollectPodStates_IgnoresSurplusOrdinalsFromAConcurrentScaleDown: a
// label-selector List would also see the ordinals a scale-down is draining, so a
// 5-to-3 scale-down applied together with an image bump would hold every delete
// for the whole drain of pods the roll never touched.
func TestCollectPodStates_IgnoresSurplusOrdinalsFromAConcurrentScaleDown(t *testing.T) {
	v, sts, pods := threeReplicaCluster(t)
	surplus := podFromStsTemplate(v, sts, 3)
	terminatingFor(surplus, 75*time.Second, 0)

	r, _ := newTestReconciler(v, sts, pods[0], pods[1], pods[2], surplus)
	states, _, err := r.collectPodStates(context.Background(), v, sts)
	require.NoError(t, err)

	require.Len(t, states, 3, "the tier is [0, *sts.Spec.Replicas)")
	assert.Empty(t, firstTerminatingPod(states).name,
		"an ordinal outside the range does not gate the roll")
}

// --- S8: the flag, never the pod object -------------------------------------

// TestFirstTerminatingPod_ReadsTheFlagNotThePodObject: several rolling-update
// fixtures pass pod: nil, and a gate written as ps.pod.DeletionTimestamp would
// panic rather than fail usefully.
func TestFirstTerminatingPod_ReadsTheFlagNotThePodObject(t *testing.T) {
	pods := []podState{
		{name: "n-0"},
		terminatingState(podState{name: "n-1"}),
	}
	assert.NotPanics(t, func() {
		assert.Equal(t, "n-1", firstTerminatingPod(pods).name)
	})
	assert.Empty(t, firstTerminatingPod([]podState{{name: "n-0"}}).name)
}
