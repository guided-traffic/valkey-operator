package controller

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/builder"
	"github.com/guided-traffic/valkey-operator/internal/health"
	"github.com/guided-traffic/valkey-operator/internal/valkeyclient"
)

// The shape measured on wds18 on 2026-11-01: after a failover from data pod 2 to data
// pod 0, one Sentinel kept naming pod 2 at the new config epoch, because resolving the
// new address had failed after the epoch was already taken. Its peers announce that
// same epoch, so nothing ever corrected it.

// sentinelNaming is the SENTINEL MASTER reply of a Sentinel that monitors data pod
// ordinal with the given flags.
func sentinelNaming(v *vkov1.Valkey, ordinal int, flags string) valkeyclient.SentinelMasterInfo {
	return valkeyclient.SentinelMasterInfo{
		IP:    podFQDN(v, ordinal),
		Port:  strconv.Itoa(int(builder.ServicePort(v))),
		Flags: flags,
	}
}

// divergenceFixture is a healthy Sentinel cluster of three data pods and the given
// number of Sentinels, all Ready and current. Data pod 0 is the master; the last
// Sentinel names data pod 2 instead, every other one names pod 0.
func divergenceFixture(name string, sentinels int32) (*vkov1.Valkey, *health.ClusterState, []client.Object) {
	v := sentinelHA(name, func(v *vkov1.Valkey) {
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: sentinels}
	})
	objs := []client.Object{v, buildTestSentinelSts(v)}

	masters := map[string]valkeyclient.SentinelMasterInfo{}
	for i := 0; i < int(sentinels); i++ {
		objs = append(objs, createSentinelPod(v, i, sentinelTestNewImage, true))
		masters[sentinelPodName(v, i)] = sentinelNaming(v, 0, "master")
	}
	masters[sentinelPodName(v, int(sentinels)-1)] = sentinelNaming(v, 2, "s_down,master")

	state := &health.ClusterState{
		MasterPod:             fmt.Sprintf("%s-0", v.Name),
		MasterAddress:         fmt.Sprintf("%s:%d", podFQDN(v, 0), builder.ServicePort(v)),
		MasterCandidates:      1,
		ReadyReplicas:         2,
		TotalReplicas:         2,
		AllSynced:             true,
		SentinelPeersExpected: int(sentinels) - 1,
		SentinelMasters:       masters,
	}
	return v, state, objs
}

// divergedLongAgo starts the clock of pod well past the bound, the state of a
// Sentinel the operator has seen naming another master for minutes.
func divergedLongAgo(r *ValkeyReconciler, v *vkov1.Valkey, pod string) {
	r.sentinelDivergence.observe(crKey(v), []string{pod}, time.Now().Add(-sentinelMasterDivergenceBound-time.Minute))
}

func divergenceCondition(v *vkov1.Valkey) *metav1.Condition {
	return meta.FindStatusCondition(v.Status.Conditions, vkov1.ConditionTypeSentinelMasterDiverged)
}

func TestSentinelMasterDivergence_ReportsTheSentinelThatNamesAnotherMaster(t *testing.T) {
	v, state, objs := divergenceFixture("smd-report", 3)
	r, c := newTestReconciler(objs...)
	pass := &passState{}

	r.handleSentinelMasterDivergence(withPassState(context.Background(), pass), v, state)

	condition := divergenceCondition(v)
	require.NotNil(t, condition)
	assert.Equal(t, metav1.ConditionTrue, condition.Status)
	assert.Equal(t, "SentinelNamesAnotherMaster", condition.Reason)
	assert.Equal(t, v.Generation, condition.ObservedGeneration)
	assert.Contains(t, condition.Message, "The data tier answers master from smd-report-0")
	assert.Contains(t, condition.Message,
		fmt.Sprintf("%s names %s:6379", sentinelPodName(v, 2), podFQDN(v, 2)))
	assert.NotContains(t, condition.Message, sentinelPodName(v, 0)+" names",
		"a Sentinel naming the data tier's master is not an offender")

	assert.Equal(t, sentinelMasterDivergenceRecheckInterval, pass.interval(),
		"a divergence changes no Kubernetes object, so the bound needs a clock of its own")
	assert.True(t, podExists(t, c, sentinelPodName(v, 2)),
		"a Sentinel seen naming another master for the first time is inside the bound")
}

// The message is compared by persistStatus, so it must not depend on map order or
// on the clock: a status rewritten on every pass would be a write per recheck.
func TestSentinelMasterDivergence_MessageIsStableAcrossPasses(t *testing.T) {
	v, state, objs := divergenceFixture("smd-stable", 5)
	state.SentinelMasters[sentinelPodName(v, 3)] = sentinelNaming(v, 1, "s_down,master")
	r, _ := newTestReconciler(objs...)

	r.handleSentinelMasterDivergence(context.Background(), v, state)
	want := divergenceCondition(v).Message
	assert.Contains(t, want, fmt.Sprintf("%s names %s:6379, %s names %s:6379",
		sentinelPodName(v, 3), podFQDN(v, 1), sentinelPodName(v, 4), podFQDN(v, 2)))

	for i := 0; i < 20; i++ {
		r.handleSentinelMasterDivergence(context.Background(), v, state)
		require.Equal(t, want, divergenceCondition(v).Message)
	}
}

func TestSentinelMasterDivergence_ClearsWhenEverySentinelAgrees(t *testing.T) {
	v, state, objs := divergenceFixture("smd-clear", 3)
	r, _ := newTestReconciler(objs...)
	r.handleSentinelMasterDivergence(context.Background(), v, state)
	require.Equal(t, metav1.ConditionTrue, divergenceCondition(v).Status)

	state.SentinelMasters[sentinelPodName(v, 2)] = sentinelNaming(v, 0, "master")
	pass := &passState{}
	r.handleSentinelMasterDivergence(withPassState(context.Background(), pass), v, state)

	condition := divergenceCondition(v)
	assert.Equal(t, metav1.ConditionFalse, condition.Status)
	assert.Equal(t, "SentinelMastersAgree", condition.Reason)
	assert.Equal(t, "Every Sentinel that answered names smd-clear-0, the pod the data tier answers master from",
		condition.Message)
	assert.Empty(t, r.sentinelDivergence.since, "an agreeing tier keeps no clock")
	assert.Equal(t, sentinelStateRecheckInterval, pass.interval(),
		"a healthy Sentinel cluster is still measured every few minutes")
}

// No Sentinel answered: nothing was measured, and a False on that evidence would
// clear the one signal that says a Sentinel is stuck (ADR 0022 D5).
func TestSentinelMasterDivergence_NoAnswerLeavesTheConditionAlone(t *testing.T) {
	v, state, objs := divergenceFixture("smd-silent", 3)
	r, _ := newTestReconciler(objs...)
	r.handleSentinelMasterDivergence(context.Background(), v, state)
	divergedLongAgo(r, v, sentinelPodName(v, 2))

	state.SentinelMasters = nil
	pass := &passState{}
	r.handleSentinelMasterDivergence(withPassState(context.Background(), pass), v, state)

	assert.Equal(t, metav1.ConditionTrue, divergenceCondition(v).Status)
	assert.NotEmpty(t, r.sentinelDivergence.since, "an unmeasured pass keeps the clock")
	assert.Equal(t, sentinelStateRecheckInterval, pass.interval())
}

// Mutation check: dropping the bound comparison deletes the pod in
// TestSentinelMasterDivergence_ReportsTheSentinelThatNamesAnotherMaster; dropping the
// restart of the clock makes the second pass here delete again.
func TestSentinelMasterDivergence_ReplacesThePodPastTheBound(t *testing.T) {
	v, state, objs := divergenceFixture("smd-replace", 3)
	r, c := newTestReconciler(objs...)
	divergedLongAgo(r, v, sentinelPodName(v, 2))

	r.handleSentinelMasterDivergence(context.Background(), v, state)

	assert.False(t, podExists(t, c, sentinelPodName(v, 2)), "the Sentinel naming another master is replaced")
	assert.True(t, podExists(t, c, sentinelPodName(v, 0)))
	assert.True(t, podExists(t, c, sentinelPodName(v, 1)))

	events := r.Recorder.(*fakeEventRecorder).withReason("SentinelMasterDiverged")
	require.Len(t, events, 1)
	assert.Equal(t, corev1.EventTypeWarning, events[0].eventType)
	assert.Contains(t, events[0].note, sentinelPodName(v, 2))
	assert.Contains(t, events[0].note, podFQDN(v, 2))
	assert.Contains(t, events[0].note, "deleted its pod")

	// The StatefulSet recreates the pod under the same name. Until the new one has
	// waited a bound of its own, it is not deleted again.
	require.NoError(t, c.Create(context.Background(), createSentinelPod(v, 2, sentinelTestNewImage, true)))
	r.handleSentinelMasterDivergence(context.Background(), v, state)
	assert.True(t, podExists(t, c, sentinelPodName(v, 2)), "the clock restarts with the replacement")
	assert.Len(t, r.Recorder.(*fakeEventRecorder).withReason("SentinelMasterDiverged"), 1)
}

// Each gate stands between a measurement and a delete. Every case is past the bound;
// removing the gate it names makes its case delete the pod.
func TestSentinelMasterDivergence_RefusesTheReplacement(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(v *vkov1.Valkey, state *health.ClusterState, objs []client.Object)
	}{
		{
			name: "two data pods answer master",
			prepare: func(_ *vkov1.Valkey, state *health.ClusterState, _ []client.Object) {
				state.MasterCandidates = 2
			},
		},
		{
			name: "a data-tier roll is recorded",
			prepare: func(v *vkov1.Valkey, _ *health.ClusterState, _ []client.Object) {
				v.Annotations = map[string]string{annotationRollingUpdateState: stateFailoverTriggered}
			},
		},
		{
			name: "the diverged Sentinel is leading a failover",
			prepare: func(v *vkov1.Valkey, state *health.ClusterState, _ []client.Object) {
				state.SentinelMasters[sentinelPodName(v, 2)] = sentinelNaming(v, 2, "master,failover_in_progress")
			},
		},
		{
			name: "only one Sentinel names the data tier's master",
			prepare: func(v *vkov1.Valkey, state *health.ClusterState, _ []client.Object) {
				state.SentinelMasters[sentinelPodName(v, 1)] = sentinelNaming(v, 2, "master")
			},
		},
		{
			name: "the Sentinels naming the master do not see it healthy",
			prepare: func(v *vkov1.Valkey, state *health.ClusterState, _ []client.Object) {
				state.SentinelMasters[sentinelPodName(v, 1)] = sentinelNaming(v, 0, "s_down,master")
			},
		},
		{
			name: "an outdated Sentinel pod waits for the Sentinel roll",
			prepare: func(_ *vkov1.Valkey, _ *health.ClusterState, objs []client.Object) {
				objs[2].(*corev1.Pod).Spec.Containers[0].Image = "valkey/valkey:8.0"
			},
		},
		{
			name: "another Sentinel is not Ready, so the quorum guard holds",
			prepare: func(_ *vkov1.Valkey, _ *health.ClusterState, objs []client.Object) {
				objs[2].(*corev1.Pod).Status.Conditions = nil
			},
		},
		{
			name: "the diverged pod was not created by the Sentinel StatefulSet",
			prepare: func(_ *vkov1.Valkey, _ *health.ClusterState, objs []client.Object) {
				objs[4].(*corev1.Pod).OwnerReferences = nil
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v, state, objs := divergenceFixture("smd-refuse", 3)
			tc.prepare(v, state, objs)
			r, c := newTestReconciler(objs...)
			divergedLongAgo(r, v, sentinelPodName(v, 2))

			r.handleSentinelMasterDivergence(context.Background(), v, state)

			assert.True(t, podExists(t, c, sentinelPodName(v, 2)), "nothing may be deleted")
			assert.Empty(t, r.Recorder.(*fakeEventRecorder).withReason("SentinelMasterDiverged"))
			assert.Equal(t, metav1.ConditionTrue, divergenceCondition(v).Status,
				"a refused replacement is still reported")
		})
	}
}

// A terminating Sentinel holds every delete of the tier (ADR 0026 D5). Five
// Sentinels, so the quorum guard alone would let the delete through.
func TestSentinelMasterDivergence_HoldsWhileASentinelTerminates(t *testing.T) {
	t.Run("in the cached sweep", func(t *testing.T) {
		v, state, objs := divergenceFixture("smd-term", 5)
		terminating(objs[3].(*corev1.Pod))
		r, c := newTestReconciler(objs...)
		r.APIReader = nil
		divergedLongAgo(r, v, sentinelPodName(v, 4))

		r.handleSentinelMasterDivergence(context.Background(), v, state)

		assert.True(t, podExists(t, c, sentinelPodName(v, 4)))
	})

	t.Run("in the live look the cache has not caught up with", func(t *testing.T) {
		v, state, objs := divergenceFixture("smd-live", 5)
		liveObjs := make([]client.Object, 0, len(objs))
		for _, obj := range objs {
			liveObjs = append(liveObjs, obj.DeepCopyObject().(client.Object))
		}
		terminating(liveObjs[3].(*corev1.Pod))
		_, live := newTestReconciler(liveObjs...)
		r, c := newTestReconciler(objs...)
		r.APIReader = live
		divergedLongAgo(r, v, sentinelPodName(v, 4))

		r.handleSentinelMasterDivergence(context.Background(), v, state)

		assert.True(t, podExists(t, c, sentinelPodName(v, 4)))
	})
}

// The call site: updateHAStatus measures, and the verdict rides the status write.
func TestUpdateHAStatus_ReportsASentinelThatNamesAnotherMaster(t *testing.T) {
	v := sentinelHA("smd-wired")
	r, c := newTestReconciler(v)
	require.NoError(t, reconcileFor(t, r, v))
	markStatefulSetReady(t, c, v)
	markSentinelStatefulSetReady(t, c, v)

	_, state, _ := divergenceFixture("smd-wired", 3)
	r.InstanceChecker = &mockInstanceChecker{clusterState: state}
	pass := &passState{}

	require.NoError(t, r.updateStatus(withPassState(context.Background(), pass), crGet(t, c, v.Name)))

	condition := meta.FindStatusCondition(crGet(t, c, v.Name).Status.Conditions,
		vkov1.ConditionTypeSentinelMasterDiverged)
	require.NotNil(t, condition)
	assert.Equal(t, metav1.ConditionTrue, condition.Status)
	assert.Equal(t, sentinelMasterDivergenceRecheckInterval, pass.interval())
}

func TestSentinelDivergenceTracker_KeepsTheFirstSightingPerPod(t *testing.T) {
	var tracker sentinelDivergenceTracker
	cr := crKey(newTestValkey("smd-tracker", "default"))
	start := time.Now()

	tracker.observe(cr, []string{"s-1", "s-2"}, start)
	since := tracker.observe(cr, []string{"s-2"}, start.Add(time.Minute))
	assert.Equal(t, map[string]time.Time{"s-2": start}, since, "s-2 keeps its first sighting, s-1 is forgotten")

	since = tracker.observe(cr, []string{"s-1", "s-2"}, start.Add(2*time.Minute))
	assert.Equal(t, start.Add(2*time.Minute), since["s-1"], "a pod that agreed in between starts over")

	tracker.restart(cr, "s-2", start.Add(3*time.Minute))
	assert.Equal(t, start.Add(3*time.Minute), tracker.observe(cr, []string{"s-2"}, start.Add(4*time.Minute))["s-2"])

	tracker.forget(cr)
	assert.Empty(t, tracker.since)
}
