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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/common"
	"github.com/guided-traffic/valkey-operator/internal/valkeyclient"
)

// The tests in this file pin ADR 0025: a Warning named split-brain means one that
// nobody resolved, the level lives in the MultipleMasters condition, and a pod
// that is being deleted is not evidence of a master.

// --- helpers ---------------------------------------------------------------

// recorderOf returns the fakeEventRecorder every test reconciler carries.
func recorderOf(t *testing.T, r *ValkeyReconciler) *fakeEventRecorder {
	t.Helper()
	rec, ok := r.Recorder.(*fakeEventRecorder)
	require.True(t, ok, "newTestReconciler must install a fakeEventRecorder")
	return rec
}

// masterLabelled stamps the sidecar's role label onto a pod.
func masterLabelled(pod *corev1.Pod) *corev1.Pod {
	pod.Labels[common.LabelInstanceRole] = common.RoleMaster
	return pod
}

// terminating puts a pod into the state the API server shows between the delete
// call and the last kubelet report: a DeletionTimestamp plus the finalizer that
// keeps it visible.
func terminating(pod *corev1.Pod) {
	now := metav1.Now()
	pod.DeletionTimestamp = &now
	pod.Finalizers = []string{"foregroundDeletion"}
}

// standingMultipleMasters persists MultipleMasters=True with a LastTransitionTime
// of age ago, which is how a window that has already lasted that long looks to the
// next pass -- including the first pass after an operator restart.
func standingMultipleMasters(t *testing.T, c client.Client, v *vkov1.Valkey, reason string, age time.Duration) {
	t.Helper()
	v.Status.Conditions = []metav1.Condition{{
		Type:               vkov1.ConditionTypeMultipleMasters,
		Status:             metav1.ConditionTrue,
		Reason:             reason,
		Message:            "2 pods report the master role",
		LastTransitionTime: metav1.NewTime(time.Now().Add(-age)),
	}}
	require.NoError(t, c.Status().Update(context.Background(), v))
}

// threeReplicaCluster is a non-Sentinel three-replica CR plus its persisted
// StatefulSet and three up-to-date pods.
func threeReplicaCluster(t *testing.T) (*vkov1.Valkey, *appsv1.StatefulSet, []*corev1.Pod) {
	t.Helper()
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) { v.Spec.Replicas = 3 })
	sts := stsForValkey(v)
	pods := make([]*corev1.Pod, 3)
	for i := range pods {
		pods[i] = podFromStsTemplate(v, sts, i)
	}
	return v, sts, pods
}

// --- (a) a pod that is being deleted is not evidence of a master -----------

// TestCollectPodStates_TerminatingPodWithAStaleMasterLabelIsNoMaster is the
// largest single contributor to the observed Warning storm: the operator demotes
// the outgoing master, deletes it, the pod stops answering INFO, and its own
// never-cleared label manufactures it back into a second master that
// demoteRogueMaster then refuses because the pod is not Ready -- so the report
// re-fired every pass with nothing ever closing it.
func TestCollectPodStates_TerminatingPodWithAStaleMasterLabelIsNoMaster(t *testing.T) {
	v, sts, pods := threeReplicaCluster(t)
	terminating(masterLabelled(pods[0]))

	r, _ := newTestReconciler(v, sts, pods[0], pods[1], pods[2])
	// The default mockInstanceChecker fails GetReplicationInfo for every pod, which
	// is exactly the state of a pod whose Valkey has already stopped.
	states, masterIdx, err := r.collectPodStates(context.Background(), v, sts)

	require.NoError(t, err)
	assert.False(t, states[0].isMaster,
		"a pod carrying a DeletionTimestamp must not be counted as master on the strength of its label")
	assert.Equal(t, -1, masterIdx, "no pod answers master, so there is no master index")
}

// TestCollectPodStates_TerminatingPodThatStillAnswersMasterCounts is the other
// half of the same rule: the guard drops the heuristic, never the answer. A
// master that still serves INFO still holds writes, and dropping it would let the
// resolver demote the pod that has the data.
func TestCollectPodStates_TerminatingPodThatStillAnswersMasterCounts(t *testing.T) {
	v, sts, pods := threeReplicaCluster(t)
	terminating(masterLabelled(pods[0]))

	r, _ := newTestReconciler(v, sts, pods[0], pods[1], pods[2])
	r.InstanceChecker = &mockInstanceChecker{
		replicationInfoFn: func(podName string) (*valkeyclient.ReplicationInfo, error) {
			if podName == "test-0" {
				return &valkeyclient.ReplicationInfo{Role: common.RoleMaster}, nil
			}
			return nil, fmt.Errorf("mock: no info for %s", podName)
		},
	}
	states, masterIdx, err := r.collectPodStates(context.Background(), v, sts)

	require.NoError(t, err)
	assert.True(t, states[0].isMaster, "an INFO-confirmed master counts, terminating or not")
	assert.Equal(t, 0, masterIdx)
}

// TestCollectPodStates_LivePodWithAMasterLabelStillCounts guards the label
// fallback itself. It exists so the DeletionTimestamp guard cannot be widened
// into "never trust the label", which would stall the state machine on every
// transient connectivity failure -- the reason the fallback was added.
func TestCollectPodStates_LivePodWithAMasterLabelStillCounts(t *testing.T) {
	v, sts, pods := threeReplicaCluster(t)
	masterLabelled(pods[1])

	r, _ := newTestReconciler(v, sts, pods[0], pods[1], pods[2])
	states, masterIdx, err := r.collectPodStates(context.Background(), v, sts)

	require.NoError(t, err)
	assert.True(t, states[1].isMaster, "an unreachable but live pod is still described by its label")
	assert.Equal(t, 1, masterIdx)
}

// --- (b) the condition is the level, the Warning is the edge ---------------

// TestReportMultipleMasters_FirstPassSetsTheConditionAndStaysSilent is the
// property the whole change exists for: the pass that observes the designed
// double-master window of a controlled failover raises no alarm.
func TestReportMultipleMasters_FirstPassSetsTheConditionAndStaysSilent(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) { v.Spec.Replicas = 3 })
	r, c := newTestReconciler(v)

	r.reportMultipleMasters(context.Background(), v, []string{"test-0", "test-1"}, "test-1")

	cond := conditionOf(t, c, v, vkov1.ConditionTypeMultipleMasters)
	require.NotNil(t, cond, "the level must be visible from the first pass")
	assert.Equal(t, metav1.ConditionTrue, cond.Status)
	assert.Equal(t, vkov1.ReasonMultipleMastersTransitional, cond.Reason)
	assert.Contains(t, cond.Message, "test-0", "the message must name the pods, not just count them")
	assert.Contains(t, cond.Message, "authority names test-1")
	assert.Empty(t, recorderOf(t, r).withReason("SplitBrainDetected"),
		"no Warning below the bound: two masters during a controlled failover are the design")
}

// TestReportMultipleMasters_WarnsOnceOnceTheWindowOutlivesTheBound pins the edge.
// The reason of the condition is what remembers that the Warning fired, so the
// second pass over the same unresolved state must stay silent.
func TestReportMultipleMasters_WarnsOnceOnceTheWindowOutlivesTheBound(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) { v.Spec.Replicas = 3 })
	r, c := newTestReconciler(v)
	standingMultipleMasters(t, c, v, vkov1.ReasonMultipleMastersTransitional, splitBrainWarnAfter+time.Second)

	r.reportMultipleMasters(context.Background(), v, []string{"test-0", "test-1"}, "")

	warnings := recorderOf(t, r).withReason("SplitBrainDetected")
	require.Len(t, warnings, 1, "the Warning is the edge past the bound")
	assert.Equal(t, corev1.EventTypeWarning, warnings[0].eventType)
	assert.Contains(t, warnings[0].note, "test-0, test-1",
		"the events API freezes the note of the first occurrence, so it has to name the pods")

	cond := conditionOf(t, c, v, vkov1.ConditionTypeMultipleMasters)
	require.NotNil(t, cond)
	assert.Equal(t, vkov1.ReasonMultipleMastersPersisted, cond.Reason)

	r.reportMultipleMasters(context.Background(), v, []string{"test-0", "test-1"}, "")
	assert.Len(t, recorderOf(t, r).withReason("SplitBrainDetected"), 1,
		"a still-unresolved split brain must not re-warn on every 10 s pass")
}

// TestReportMultipleMasters_TheBoundSurvivesAnOperatorRestart is why the deadline
// is kept in the condition rather than in process memory: a restarted operator
// must not hand an unresolved split brain a fresh 90 s of silence. The reconciler
// here is brand new and its tracker is empty.
func TestReportMultipleMasters_TheBoundSurvivesAnOperatorRestart(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) { v.Spec.Replicas = 3 })
	r, c := newTestReconciler(v)
	standingMultipleMasters(t, c, v, vkov1.ReasonMultipleMastersTransitional, 10*time.Minute)

	r.reportMultipleMasters(context.Background(), v, []string{"test-0", "test-2"}, "")

	assert.Len(t, recorderOf(t, r).withReason("SplitBrainDetected"), 1,
		"the condition LastTransitionTime is the copy that survives a restart")
}

// TestReportMultipleMasters_InMemoryBoundAnswersWhenTheStatusWriteFails is the
// ADR 0010 D7 discipline for this bound: a deadline that can silently fail to arm
// is not a deadline. Every status write is rejected here, so the condition never
// lands and only the in-memory first-seen can answer.
func TestReportMultipleMasters_InMemoryBoundAnswersWhenTheStatusWriteFails(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) { v.Spec.Replicas = 3 })
	funcs := interceptor.Funcs{
		SubResourceUpdate: func(_ context.Context, _ client.Client, _ string,
			_ client.Object, _ ...client.SubResourceUpdateOption) error {
			return fmt.Errorf("status writes are rejected")
		},
	}
	r, c := newTestReconcilerWithInterceptor(funcs, v)

	// The window opened two minutes ago; only the tracker knows.
	r.nudges.observe(waitBoundKey(v.Namespace, v.Name, boundMultipleMasters),
		time.Now().Add(-2*splitBrainWarnAfter))

	r.reportMultipleMasters(context.Background(), v, []string{"test-0", "test-1"}, "")

	assert.Nil(t, conditionOf(t, c, v, vkov1.ConditionTypeMultipleMasters),
		"the premise of this test is that no status write lands")
	assert.Len(t, recorderOf(t, r).withReason("SplitBrainDetected"), 1,
		"a failing status write must not silence the Warning forever")
}

// TestReportMultipleMasters_ClearsWhenTheSecondMasterIsGone closes the level.
func TestReportMultipleMasters_ClearsWhenTheSecondMasterIsGone(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) { v.Spec.Replicas = 3 })
	r, c := newTestReconciler(v)
	standingMultipleMasters(t, c, v, vkov1.ReasonMultipleMastersPersisted, 5*time.Minute)
	r.nudges.observe(waitBoundKey(v.Namespace, v.Name, boundMultipleMasters), time.Now().Add(-5*time.Minute))

	r.reportMultipleMasters(context.Background(), v, []string{"test-1"}, "test-1")

	cond := conditionOf(t, c, v, vkov1.ConditionTypeMultipleMasters)
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionFalse, cond.Status)
	assert.Equal(t, vkov1.ReasonSingleMaster, cond.Reason)

	_, tracked := r.nudges.firstSeen(waitBoundKey(v.Namespace, v.Name, boundMultipleMasters))
	assert.False(t, tracked,
		"a leftover deadline would pre-expire the next controlled failover and warn on its first pass")
}

// TestReportMultipleMasters_ClearIsPresenceGuarded keeps the change
// upgrade-neutral: a cluster that never saw two masters must not acquire a new
// status condition, and must not be written to at all.
func TestReportMultipleMasters_ClearIsPresenceGuarded(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) { v.Spec.Replicas = 3 })
	r, c := newTestReconciler(v)

	r.reportMultipleMasters(context.Background(), v, []string{"test-0"}, "test-0")

	assert.Nil(t, conditionOf(t, c, v, vkov1.ConditionTypeMultipleMasters),
		"a healthy cluster must not gain a MultipleMasters=False condition on upgrade")
}

// TestSplitBrainWarnAfterIsBoundedByTheDurationsItMustOutlive pins the choice of
// 90 s at both ends. Below it, no legitimately terminating master can reach the
// Warning; above it, the operator cannot abandon a topology restoration with
// rogue masters still present without the Warning having fired first.
func TestSplitBrainWarnAfterIsBoundedByTheDurationsItMustOutlive(t *testing.T) {
	assert.Greater(t, splitBrainWarnAfter, 75*time.Second,
		"a pod may occupy its full terminationGracePeriodSeconds without being a split brain")
	assert.Less(t, splitBrainWarnAfter, finalizationStallTimeout,
		"giving up on a topology restore must never be the first anyone hears of the split brain")
}

// --- (c) a repair that succeeded is not a Warning --------------------------

// TestDemoteRogueMaster_ReportsASucceededRepairAsNormal covers the second
// amplifier of the storm: a helper that reported a repair it had just completed
// as a Warning, so fixing only the detection site would have left the storm in
// place. The fake server accepts REPLICAOF, which is the only way the event site
// is reached at all.
func TestDemoteRogueMaster_ReportsASucceededRepairAsNormal(t *testing.T) {
	v, sts, pods := threeReplicaCluster(t)
	r, _ := newTestReconciler(v, sts, pods[0], pods[1])
	addr := fakeValkeyServer(t)
	r.NewValkeyClientFn = func(_, _ string, _ *tls.Config) *valkeyclient.Client {
		return valkeyclient.New(addr)
	}

	rogue := podState{name: pods[1].Name, pod: pods[1], exists: true, readyCondition: true}
	require.NoError(t, r.demoteRogueMaster(context.Background(), v, rogue, pods[0].Name))

	resolved := recorderOf(t, r).withReason("SplitBrainResolved")
	require.Len(t, resolved, 1)
	assert.Equal(t, corev1.EventTypeNormal, resolved[0].eventType,
		"a repair that succeeded is not a Warning")
}

// --- (d) one fact, one event ----------------------------------------------

// TestVerifyTopologyRestored_ReportsTheIncompleteRestoreOnce pins the
// double-report fix. rogueCount > 0 and "more than one master" are the same
// predicate, so TopologyRestoreIncomplete used to arrive with SplitBrainDetected
// every single pass -- 2 to 3 Warnings per pass, every 10 s, for up to 2 minutes.
func TestVerifyTopologyRestored_ReportsTheIncompleteRestoreOnce(t *testing.T) {
	v, sts, pods := threeReplicaCluster(t)
	v.Annotations = map[string]string{annotationRollingUpdateState: stateVerifyingTopology}
	masterLabelled(pods[0])
	masterLabelled(pods[1])

	r, _ := newTestReconciler(v, sts, pods[0], pods[1], pods[2])

	result := r.verifyTopologyRestored(context.Background(), v, sts)
	require.Nil(t, result.Error)

	rec := recorderOf(t, r)
	assert.Len(t, rec.withReason("TopologyRestoreIncomplete"), 1,
		"the incomplete restore is reported once")
	assert.Empty(t, rec.withReason("SplitBrainDetected"),
		"the resolver call inside verifyTopologyRestored reports nothing: the fact is already out")
}

// --- the clean path emits no Warning at all --------------------------------

// TestHandleMultiReplicaRollingUpdate_CleanPassEmitsNoWarning is the assertion
// whose absence let a Warning per controlled failover ship. It is the unit-tier
// half of the e2e check; the e2e legs assert the same property over a real
// rolling update on both topologies.
func TestHandleMultiReplicaRollingUpdate_CleanPassEmitsNoWarning(t *testing.T) {
	v, sts, pods := threeReplicaCluster(t)
	masterLabelled(pods[0])

	r, _ := newTestReconciler(v, sts, pods[0], pods[1], pods[2])

	r.handleMultiReplicaRollingUpdate(context.Background(), v, sts)

	assert.Empty(t, recorderOf(t, r).withType(corev1.EventTypeWarning),
		"a pass with a single master must raise no alarm of any kind")
}

// TestHandleMultiReplicaRollingUpdate_DoubleMasterPassSetsTheConditionSilently
// runs the same path with two masters, through the real call site rather than the
// helper: the level appears, the alarm does not.
func TestHandleMultiReplicaRollingUpdate_DoubleMasterPassSetsTheConditionSilently(t *testing.T) {
	v, sts, pods := threeReplicaCluster(t)
	masterLabelled(pods[0])
	masterLabelled(pods[1])

	r, c := newTestReconciler(v, sts, pods[0], pods[1], pods[2])

	r.handleMultiReplicaRollingUpdate(context.Background(), v, sts)

	cond := conditionOf(t, c, v, vkov1.ConditionTypeMultipleMasters)
	require.NotNil(t, cond, "two masters must be queryable from the first pass")
	assert.Equal(t, metav1.ConditionTrue, cond.Status)
	assert.Equal(t, vkov1.ReasonMultipleMastersTransitional, cond.Reason)
	assert.Empty(t, recorderOf(t, r).withReason("SplitBrainDetected"))
}
