package controller

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/common"
	"github.com/guided-traffic/valkey-operator/internal/valkeyclient"
)

// The rules of docs/adr/0026-a-pod-being-deleted-is-not-available.md, D11 (T32).
//
// A pod that exists, is not being deleted and never becomes available used to hold
// the rolling update of its tier with no bound, no condition and -- after a spec fix
// -- no way forward, because every delete site required the pod to be available
// first. Two rules replace that:
//
//   - an outdated pod is replaced, not waited for; only a terminating one is waited on;
//   - every remaining wait on such a pod is a bounded observation on the pod's own
//     clock, reported as PodAvailabilityStalled by the tier's evaluator.
//
// Fixture rule, inherited from pod_termination_test.go: only a pod that is meant to
// be terminating carries a finalizer.

// pastSyncBudget is a not-Ready age safely beyond the default syncTimeout (5m).
const pastSyncBudget = 5*time.Minute + time.Minute

// notReadyFor puts a pod into the state kubelet reports for a pod whose Ready
// condition turned False `age` ago.
func notReadyFor(pod *corev1.Pod, age time.Duration) *corev1.Pod {
	pod.Status.Conditions = []corev1.PodCondition{{
		Type:               corev1.PodReady,
		Status:             corev1.ConditionFalse,
		LastTransitionTime: metav1.NewTime(time.Now().Add(-age)),
	}}
	return pod
}

// availabilityStalledTrue seeds the CR with a standing PodAvailabilityStalled=True
// carrying the given tier's reason.
func availabilityStalledTrue(reason string) func(*vkov1.Valkey) {
	return func(v *vkov1.Valkey) {
		meta.SetStatusCondition(&v.Status.Conditions, metav1.Condition{
			Type:    vkov1.ConditionTypePodAvailabilityStalled,
			Status:  metav1.ConditionTrue,
			Reason:  reason,
			Message: "seeded",
		})
	}
}

func availabilityCondition(t *testing.T, c client.Client, v *vkov1.Valkey) *metav1.Condition {
	t.Helper()
	fresh := &vkov1.Valkey{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: v.Name, Namespace: v.Namespace}, fresh))
	return meta.FindStatusCondition(fresh.Status.Conditions, vkov1.ConditionTypePodAvailabilityStalled)
}

// masterOnPod0 answers INFO like a healthy cluster whose master is pod-0.
func masterOnPod0(v *vkov1.Valkey) *mockInstanceChecker {
	master := fmt.Sprintf("%s-0", common.StatefulSetName(v, common.ComponentValkey))
	return &mockInstanceChecker{
		replicationInfoFn: func(pod string) (*valkeyclient.ReplicationInfo, error) {
			if pod == master {
				return &valkeyclient.ReplicationInfo{Role: common.RoleMaster, ConnectedSlaves: 2}, nil
			}
			return &valkeyclient.ReplicationInfo{Role: common.RoleReplica, MasterLinkStatus: "up"}, nil
		},
	}
}

// --- the clock ----------------------------------------------------------------

func TestPodNotReadySince(t *testing.T) {
	created := time.Now().Add(-time.Hour).Truncate(time.Second)
	flipped := time.Now().Add(-10 * time.Minute).Truncate(time.Second)

	for _, tc := range []struct {
		name       string
		conditions []corev1.PodCondition
		want       time.Time
	}{
		{"Ready False with a transition time is that time",
			[]corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionFalse,
				LastTransitionTime: metav1.NewTime(flipped)}},
			flipped},
		{"no Ready condition yet (Pending) is the creation time",
			[]corev1.PodCondition{{Type: corev1.PodScheduled, Status: corev1.ConditionFalse,
				LastTransitionTime: metav1.NewTime(flipped)}},
			created},
		{"Ready False with a zero time is the creation time",
			[]corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionFalse}},
			created},
		{"Ready Unknown with a transition time is that time",
			[]corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionUnknown,
				LastTransitionTime: metav1.NewTime(flipped)}},
			flipped},
		{"Ready True is no clock at all",
			[]corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue,
				LastTransitionTime: metav1.NewTime(flipped)}},
			time.Time{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{CreationTimestamp: metav1.NewTime(created)},
				Status:     corev1.PodStatus{Conditions: tc.conditions},
			}
			assert.True(t, tc.want.Equal(podNotReadySince(pod)), "got %v, want %v", podNotReadySince(pod), tc.want)
		})
	}
}

// The collector carries the clock on the state, so no reader has to dereference
// the pod -- fixtures build podStates with pod == nil.
func TestCollectPodStates_CarriesTheNotReadyClock(t *testing.T) {
	v, sts, pods := threeReplicaCluster(t)
	notReadyFor(pods[1], 7*time.Minute)

	r, _ := newTestReconciler(v, sts, pods[0], pods[1], pods[2])
	states, _, err := r.collectPodStates(context.Background(), v, sts)
	require.NoError(t, err)

	assert.WithinDuration(t, time.Now().Add(-7*time.Minute), states[1].notReadySince, 2*time.Second)
	assert.True(t, states[0].notReadySince.IsZero(), "a Ready pod has no not-Ready clock")
}

// --- availabilityWait -----------------------------------------------------------

func TestAvailabilityWait(t *testing.T) {
	for _, tc := range []struct {
		name      string
		since     time.Time
		timeout   *metav1.Duration
		wantStall bool
	}{
		{"inside the budget: the plain requeue", time.Now().Add(-time.Minute), nil, false},
		{"past the budget: the stall shape", time.Now().Add(-pastSyncBudget), nil, true},
		{"no clock: nothing can have expired", time.Time{}, nil, false},
		{"the budget is spec.rollingUpdate.syncTimeout", time.Now().Add(-pastSyncBudget),
			&metav1.Duration{Duration: time.Hour}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := newTestValkey("aw", "default", func(v *vkov1.Valkey) {
				if tc.timeout != nil {
					v.Spec.RollingUpdate = &vkov1.RollingUpdateSpec{SyncTimeout: tc.timeout}
				}
			})
			r, c := newTestReconciler(v)

			result := r.availabilityWait(context.Background(), v,
				unavailablePod{tier: common.ComponentValkey, name: "aw-1", since: tc.since}, "waiting")

			require.NotNil(t, result)
			if tc.wantStall {
				assert.False(t, result.NeedsRequeue, "past the budget the pass must not end on the wait")
				assert.Equal(t, rollingUpdateRequeueDelay, result.DeferredRequeueAfter)
				require.NotNil(t, result.availabilityStall)
				assert.Equal(t, "aw-1", result.availabilityStall.name)
				assert.True(t, tc.since.Equal(result.availabilityStall.since))
			} else {
				assert.True(t, result.NeedsRequeue)
				assert.Equal(t, rollingUpdateRequeueDelay, result.RequeueAfter)
				assert.Zero(t, result.DeferredRequeueAfter)
				assert.Nil(t, result.availabilityStall)
			}
			assert.Nil(t, availabilityCondition(t, c, v),
				"the wait writes nothing: the tier's evaluator is the one writer")
		})
	}
}

// --- D2: the waits on a current pod ---------------------------------------------

// Row 4 of T32: a replaced replica that never comes up held the next delete with no
// bound. It is reported past the budget -- and the next replica is still not deleted.
//
// Mutation check: routing the non-terminating case of waitForUnavailablePod back to
// a plain NeedsRequeue fails the DeferredRequeueAfter assertion.
func TestVerifyReplacedReplicasSynced_ReportsAReplacementThatNeverComesUp(t *testing.T) {
	v := newTestValkey("vrs", "default", func(v *vkov1.Valkey) { v.Spec.Replicas = 3 })
	r, c := newTestReconciler(v)
	pods := []podState{
		{name: "vrs-0", exists: true, readyCondition: true, isMaster: true},
		{name: "vrs-1", exists: true, notReadySince: time.Now().Add(-pastSyncBudget)},
		{name: "vrs-2", exists: true, readyCondition: true, needsUpdate: true},
	}

	result := r.verifyReplacedReplicasSynced(context.Background(), v, pods)

	require.NotNil(t, result)
	assert.False(t, result.NeedsRequeue)
	assert.Equal(t, rollingUpdateRequeueDelay, result.DeferredRequeueAfter)
	require.NotNil(t, result.availabilityStall)
	assert.Equal(t, "vrs-1", result.availabilityStall.name)
	assert.Empty(t, crGet(t, c, "vrs").Annotations[annotationSyncWaitStarted],
		"the sync-wait bound is not armed against a pod that never answered")
}

// Row 7 of T32: every pod is current, one is not Ready, and the dispatch lands in
// replaceRemainingPods' fall-through on every pass. It used to requeue there with
// no pod named.
//
// Mutation check: deleting the firstUnavailableExisting branch leaves the anonymous
// requeue and fails the DeferredRequeueAfter assertion.
func TestReplaceRemainingPods_FallThroughReportsTheUnavailablePod(t *testing.T) {
	v := sentinelClusterCR("rrp-fall", 3)
	r, _ := newTestReconciler(v)
	pods := []podState{
		{name: "rrp-fall-0", exists: true, readyCondition: true, isMaster: true},
		{name: "rrp-fall-1", exists: true, notReadySince: time.Now().Add(-pastSyncBudget)},
		{name: "rrp-fall-2", exists: true, readyCondition: true},
	}

	result := r.replaceRemainingPods(context.Background(), v, pods)

	assert.False(t, result.NeedsRequeue)
	assert.Equal(t, rollingUpdateRequeueDelay, result.DeferredRequeueAfter)
	require.NotNil(t, result.availabilityStall)
	assert.Equal(t, "rrp-fall-1", result.availabilityStall.name)
}

// Inside the budget the fall-through keeps the plain requeue, and with nothing
// unavailable it keeps the one it always had.
func TestReplaceRemainingPods_FallThroughInsideTheBudgetIsThePlainRequeue(t *testing.T) {
	v := sentinelClusterCR("rrp-fall2", 3)
	r, _ := newTestReconciler(v)
	pods := []podState{
		{name: "rrp-fall2-0", exists: true, readyCondition: true, isMaster: true},
		{name: "rrp-fall2-1", exists: true, notReadySince: time.Now().Add(-time.Minute)},
	}

	result := r.replaceRemainingPods(context.Background(), v, pods)
	assert.True(t, result.NeedsRequeue)
	assert.Nil(t, result.availabilityStall)
}

// Row 5 of T32, split in two: an unavailable replica is the bounded availability
// wait; an available but outdated pod (a second master replaceNextReplica does not
// take) keeps the plain requeue and is never reported as unavailable.
func TestWaitForReplicasReady_SplitsUnavailableFromOutdated(t *testing.T) {
	v := sentinelClusterCR("wrr", 3)
	r, _ := newTestReconciler(v)

	unavailable := []podState{
		{name: "wrr-0", exists: true, readyCondition: true, isMaster: true, needsUpdate: true},
		{name: "wrr-1", exists: true, notReadySince: time.Now().Add(-pastSyncBudget)},
	}
	result := r.waitForReplicasReady(context.Background(), v, unavailable, 0)
	require.NotNil(t, result)
	assert.Equal(t, rollingUpdateRequeueDelay, result.DeferredRequeueAfter)
	require.NotNil(t, result.availabilityStall)
	assert.Equal(t, "wrr-1", result.availabilityStall.name)

	outdated := []podState{
		{name: "wrr-0", exists: true, readyCondition: true, isMaster: true, needsUpdate: true},
		{name: "wrr-1", exists: true, readyCondition: true, needsUpdate: true},
	}
	result = r.waitForReplicasReady(context.Background(), v, outdated, 0)
	require.NotNil(t, result)
	assert.True(t, result.NeedsRequeue)
	assert.Nil(t, result.availabilityStall)
}

// --- D5: the data tier's evaluator -----------------------------------------------

// dataStallFixture is a non-Sentinel three-replica cluster mid-roll: pod-0 is the
// master, pod-1 is a replacement on the current template that has not been Ready
// for notReady, and pod-2 still needs the update.
func dataStallFixture(t *testing.T, notReady time.Duration, opts ...func(*vkov1.Valkey)) (
	*ValkeyReconciler, client.Client, *vkov1.Valkey, []*corev1.Pod) {
	t.Helper()
	v := newTestValkey("dst", "default", append([]func(*vkov1.Valkey){
		func(v *vkov1.Valkey) { v.Spec.Replicas = 3 },
	}, opts...)...)
	sts := stsForValkey(v)
	pods := []*corev1.Pod{
		podFromStsTemplate(v, sts, 0),
		podFromStsTemplate(v, sts, 1),
		podFromStsTemplate(v, sts, 2),
	}
	if notReady > 0 {
		notReadyFor(pods[1], notReady)
	}
	outdateValkeyContainer(pods[2])
	r, c := newTestReconciler(v, sts, pods[0], pods[1], pods[2])
	r.InstanceChecker = masterOnPod0(v)
	return r, c, crGet(t, c, "dst"), pods
}

// The evaluator writes True naming the pod, keeps the message stable across passes,
// and retracts to False once the pod is Ready. The healthy pass that follows also
// deletes the next replica, which shows the stall held it until then.
func TestCheckAndHandleRollingUpdate_ReportsAndRetractsTheDataStall(t *testing.T) {
	r, c, v, pods := dataStallFixture(t, pastSyncBudget)

	result := r.checkAndHandleRollingUpdate(context.Background(), v)
	require.NoError(t, result.Error)
	assert.Equal(t, rollingUpdateRequeueDelay, result.DeferredRequeueAfter)
	assert.True(t, podExists(t, c, pods[2].Name), "the stall holds the next delete")

	cond := availabilityCondition(t, c, v)
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionTrue, cond.Status)
	assert.Equal(t, vkov1.ReasonValkeyPodNotAvailable, cond.Reason)
	assert.Contains(t, cond.Message, pods[1].Name, "the condition names the pod")
	first := *cond

	// A second stalled pass writes the same message: it names an instant, not a
	// running duration.
	result = r.checkAndHandleRollingUpdate(context.Background(), crGet(t, c, "dst"))
	require.NoError(t, result.Error)
	cond = availabilityCondition(t, c, v)
	require.NotNil(t, cond)
	assert.Equal(t, first.Message, cond.Message)
	assert.True(t, first.LastTransitionTime.Equal(&cond.LastTransitionTime))

	// The pod comes up.
	live := &corev1.Pod{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: pods[1].Name, Namespace: "default"}, live))
	live.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}
	require.NoError(t, c.Status().Update(context.Background(), live))

	result = r.checkAndHandleRollingUpdate(context.Background(), crGet(t, c, "dst"))
	require.NoError(t, result.Error)
	cond = availabilityCondition(t, c, v)
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionFalse, cond.Status)
	assert.Equal(t, vkov1.ReasonPodAvailable, cond.Reason)
	assert.False(t, podExists(t, c, pods[2].Name), "with the stall over the roll moves on")
}

// Inside the budget the same shape is the ordinary requeue and the CR never gains
// the condition -- nor does a cluster that never stalled at all.
func TestCheckAndHandleRollingUpdate_NoConditionWithoutAStall(t *testing.T) {
	r, c, v, _ := dataStallFixture(t, time.Minute)
	result := r.checkAndHandleRollingUpdate(context.Background(), v)
	require.NoError(t, result.Error)
	assert.True(t, result.NeedsRequeue)
	assert.Nil(t, availabilityCondition(t, c, v), "inside the budget nothing is reported")

	r, c, v, _ = dataStallFixture(t, 0)
	result = r.checkAndHandleRollingUpdate(context.Background(), v)
	require.NoError(t, result.Error)
	assert.Nil(t, availabilityCondition(t, c, v), "a cluster that never stalled never gains the condition")
}

// An error result did not measure the tier, so a standing report stays standing.
func TestCheckAndHandleRollingUpdate_ErrorLeavesTheReportAsItIs(t *testing.T) {
	v := newTestValkey("dse", "default", func(v *vkov1.Valkey) { v.Spec.Replicas = 3 },
		availabilityStalledTrue(vkov1.ReasonValkeyPodNotAvailable))
	sts := stsForValkey(v)
	pod0 := podFromStsTemplate(v, sts, 0)
	foreign := podFromStsTemplate(v, sts, 1)
	foreign.OwnerReferences = nil // not ours: the dispatch fails the step
	r, c := newTestReconciler(v, sts, pod0, foreign)

	result := r.checkAndHandleRollingUpdate(context.Background(), crGet(t, c, "dse"))
	require.Error(t, result.Error)

	cond := availabilityCondition(t, c, v)
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionTrue, cond.Status)
	assert.Equal(t, "seeded", cond.Message)
}

// The data evaluator retracts only its own report. A Sentinel report is the Sentinel
// tier's to retract.
//
// Mutation check: dropping the reason comparison in reportAvailabilityStall retracts
// the Sentinel report here.
func TestCheckAndHandleRollingUpdate_NeverRetractsASentinelReport(t *testing.T) {
	v := newTestValkey("dsr", "default", func(v *vkov1.Valkey) { v.Spec.Replicas = 3 },
		availabilityStalledTrue(vkov1.ReasonSentinelPodNotAvailable))
	sts := stsForValkey(v)
	r, c := newTestReconciler(v, sts,
		podFromStsTemplate(v, sts, 0), podFromStsTemplate(v, sts, 1), podFromStsTemplate(v, sts, 2))

	result := r.checkAndHandleRollingUpdate(context.Background(), crGet(t, c, "dsr"))
	require.NoError(t, result.Error)

	cond := availabilityCondition(t, c, v)
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionTrue, cond.Status)
	assert.Equal(t, vkov1.ReasonSentinelPodNotAvailable, cond.Reason)
}

// --- D3: the Sentinel tier ---------------------------------------------------------

func sentinelPodName(v *vkov1.Valkey, ordinal int) string {
	return fmt.Sprintf("%s-%d", common.StatefulSetName(v, common.ComponentSentinel), ordinal)
}

func sentinelHA(name string, opts ...func(*vkov1.Valkey)) *vkov1.Valkey {
	return newTestValkey(name, "default", append([]func(*vkov1.Valkey){func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
	}}, opts...)...)
}

// Three Sentinels on an outdated spec, the last one not Ready -- the shape after a
// spec fix, where sentinel-2 is the replacement that never came up. It is the
// target, and it costs no vote, so it is deleted although readyCount-1 < quorum.
//
// Mutation check: returning firstOutdatedPod from deleteTarget deletes sentinel-0
// (readyCount-1 then also fails the guard, so the pass waits) and fails the first
// assertion.
func TestSentinelRollingUpdate_ReplacesTheUnavailableOutdatedSentinelFirst(t *testing.T) {
	v := sentinelHA("sru")
	const oldImg = "valkey/valkey:8.0"
	sts := buildTestSentinelSts(v)
	p0 := createSentinelPod(v, 0, oldImg, true)
	p1 := createSentinelPod(v, 1, oldImg, true)
	p2 := createSentinelPod(v, 2, oldImg, false)
	r, c := newTestReconciler(v, sts, p0, p1, p2)

	result := r.checkAndHandleSentinelRollingUpdate(context.Background(), v)
	require.NoError(t, result.Error)

	assert.False(t, podExists(t, c, sentinelPodName(v, 2)),
		"the unavailable outdated Sentinel is the target, and deleting it spends no vote")
	assert.True(t, podExists(t, c, sentinelPodName(v, 0)))
	assert.True(t, podExists(t, c, sentinelPodName(v, 1)))
}

// A Sentinel pod on the current spec that never comes up is what a quorum wait is
// really waiting on. Past the budget it is named, and the healthy target is still
// refused -- the guard itself is unchanged.
func TestSentinelRollingUpdate_QuorumWaitReportsTheUnavailableReplacement(t *testing.T) {
	v := sentinelHA("sqw")
	const oldImg = "valkey/valkey:8.0"
	sts := buildTestSentinelSts(v)
	p0 := createSentinelPod(v, 0, oldImg, true)
	p1 := createSentinelPod(v, 1, oldImg, true)
	p2 := notReadyFor(createSentinelPod(v, 2, sentinelTestNewImage, false), pastSyncBudget)
	r, c := newTestReconciler(v, sts, p0, p1, p2)

	result := r.checkAndHandleSentinelRollingUpdate(context.Background(), v)
	require.NoError(t, result.Error)
	assert.False(t, result.NeedsRequeue)
	assert.Equal(t, rollingUpdateRequeueDelay, result.DeferredRequeueAfter)

	for i := 0; i < 3; i++ {
		assert.True(t, podExists(t, c, sentinelPodName(v, i)), "quorum guard intact: nothing is deleted")
	}
	cond := availabilityCondition(t, c, v)
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionTrue, cond.Status)
	assert.Equal(t, vkov1.ReasonSentinelPodNotAvailable, cond.Reason)
	assert.Contains(t, cond.Message, sentinelPodName(v, 2))
}

// The completion hold used to requeue with no bound on a replacement that never
// came up. Past the budget it reports the pod and the roll stays pending.
func TestSentinelRollingUpdate_CompletionHoldReportsTheUnavailableReplacement(t *testing.T) {
	v := sentinelHA("sch", sentinelUpdatePendingTrue)
	sts := buildTestSentinelSts(v)
	p0 := createSentinelPod(v, 0, sentinelTestNewImage, true)
	p1 := createSentinelPod(v, 1, sentinelTestNewImage, true)
	p2 := notReadyFor(createSentinelPod(v, 2, sentinelTestNewImage, false), pastSyncBudget)
	r, c := newTestReconciler(v, sts, p0, p1, p2)

	result := r.checkAndHandleSentinelRollingUpdate(context.Background(), v)
	require.NoError(t, result.Error)
	assert.Equal(t, rollingUpdateRequeueDelay, result.DeferredRequeueAfter)

	cond := availabilityCondition(t, c, v)
	require.NotNil(t, cond)
	assert.Equal(t, vkov1.ReasonSentinelPodNotAvailable, cond.Reason)
	pending := getSentinelUpdatePending(t, c, v)
	require.NotNil(t, pending)
	assert.Equal(t, metav1.ConditionTrue, pending.Status, "the roll is not complete while a pod is down")

	// The pod comes up: the report is retracted and the roll completes.
	live := &corev1.Pod{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: p2.Name, Namespace: "default"}, live))
	live.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}
	require.NoError(t, c.Status().Update(context.Background(), live))

	result = r.checkAndHandleSentinelRollingUpdate(context.Background(), crGet(t, c, "sch"))
	require.NoError(t, result.Error)
	cond = availabilityCondition(t, c, v)
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionFalse, cond.Status)
	assert.Equal(t, vkov1.ReasonPodAvailable, cond.Reason)
	assert.Equal(t, metav1.ConditionFalse, getSentinelUpdatePending(t, c, v).Status)
}

// A terminating Sentinel in the completion hold gets terminationWait, as every other
// wait on a terminating pod does (ADR 0026 D5) -- it used to be the one that did not.
// Past the overrun the stall is reported and the pass continues; once the pod is
// gone and the tier is complete, the completion clears it.
func TestSentinelRollingUpdate_CompletionHoldRoutesATerminatingPodThroughTerminationWait(t *testing.T) {
	v := sentinelHA("sct", sentinelUpdatePendingTrue)
	sts := buildTestSentinelSts(v)
	p0 := createSentinelPod(v, 0, sentinelTestNewImage, true)
	p1 := createSentinelPod(v, 1, sentinelTestNewImage, true)
	p2 := createSentinelPod(v, 2, sentinelTestNewImage, true)
	terminatingFor(p2, 30*time.Second, 30*time.Second+podTerminationOverrun+time.Minute)
	r, c := newTestReconciler(v, sts, p0, p1, p2)

	result := r.checkAndHandleSentinelRollingUpdate(context.Background(), v)
	require.NoError(t, result.Error)
	assert.False(t, result.NeedsRequeue)
	assert.Equal(t, rollingUpdateRequeueDelay, result.DeferredRequeueAfter)
	fresh := crGet(t, c, "sct")
	stalled := meta.FindStatusCondition(fresh.Status.Conditions, vkov1.ConditionTypePodTerminationStalled)
	require.NotNil(t, stalled)
	assert.Equal(t, metav1.ConditionTrue, stalled.Status)
	assert.True(t, strings.HasPrefix(stalled.Message, common.ComponentSentinel+" pod "),
		"reported under the Sentinel tier: %s", stalled.Message)

	// The pod is gone and its replacement is Ready: the tier completes and the
	// termination report is retracted -- the completion hold passes no delete gate.
	live := &corev1.Pod{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: p2.Name, Namespace: "default"}, live))
	live.Finalizers = nil
	require.NoError(t, c.Update(context.Background(), live))
	require.NoError(t, c.Create(context.Background(), createSentinelPod(v, 2, sentinelTestNewImage, true)))

	result = r.checkAndHandleSentinelRollingUpdate(context.Background(), crGet(t, c, "sct"))
	require.NoError(t, result.Error)
	fresh = crGet(t, c, "sct")
	stalled = meta.FindStatusCondition(fresh.Status.Conditions, vkov1.ConditionTypePodTerminationStalled)
	require.NotNil(t, stalled)
	assert.Equal(t, metav1.ConditionFalse, stalled.Status)
	assert.Equal(t, metav1.ConditionFalse, getSentinelUpdatePending(t, c, v).Status)
}

// The Sentinel evaluator retracts only its own report, never the data tier's.
func TestCheckAndHandleSentinelRollingUpdate_NeverRetractsADataReport(t *testing.T) {
	v := sentinelHA("ssr", availabilityStalledTrue(vkov1.ReasonValkeyPodNotAvailable))
	sts := buildTestSentinelSts(v)
	r, c := newTestReconciler(v, sts,
		createSentinelPod(v, 0, sentinelTestNewImage, true),
		createSentinelPod(v, 1, sentinelTestNewImage, true),
		createSentinelPod(v, 2, sentinelTestNewImage, true))

	result := r.checkAndHandleSentinelRollingUpdate(context.Background(), v)
	require.NoError(t, result.Error)
	cond := availabilityCondition(t, c, v)
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionTrue, cond.Status)
	assert.Equal(t, vkov1.ReasonValkeyPodNotAvailable, cond.Reason)
}

// The Sentinel result's DeferredRequeueAfter reaches the pass result instead of
// being dropped, so a stalled Sentinel wait schedules its own recheck.
//
// Mutation check: returning ctrl.Result{} instead of the Sentinel
// DeferredRequeueAfter at the end of runSentinelRollingUpdate fails the assertion.
func TestHandlePostRollingUpdateChecks_AppliesTheSentinelDeferredRequeue(t *testing.T) {
	v := sentinelHA("spd", sentinelUpdatePendingTrue)
	sts := buildTestSentinelSts(v)
	r, _ := newTestReconciler(v, sts,
		createSentinelPod(v, 0, sentinelTestNewImage, true),
		createSentinelPod(v, 1, sentinelTestNewImage, true),
		notReadyFor(createSentinelPod(v, 2, sentinelTestNewImage, false), pastSyncBudget))

	result, done, err := r.handlePostRollingUpdateChecks(context.Background(), v, false)
	require.NoError(t, err)
	assert.False(t, done, "a stalled Sentinel wait continues the pass")
	assert.Equal(t, rollingUpdateRequeueDelay, result.RequeueAfter)
}

// --- D4: a holding data tier holds the Sentinel roll --------------------------------

// The availability stall version of TestReconcileWorkload_StalledTerminationHoldsTheSentinelRoll:
// a data replacement on the shared spec never comes up, and the Sentinel roll must
// not take a healthy Sentinel onto the same spec.
//
// Mutation check: passing false instead of rollingResult.DeferredRequeueAfter > 0 in
// reconcileWorkload deletes a Sentinel pod and fails the loop.
func TestReconcileWorkload_DataAvailabilityStallHoldsTheSentinelRoll(t *testing.T) {
	v := sentinelHA("dsh")
	sts := stsForValkey(v)
	dataPods := []*corev1.Pod{
		podFromStsTemplate(v, sts, 0),
		notReadyFor(podFromStsTemplate(v, sts, 1), pastSyncBudget),
		podFromStsTemplate(v, sts, 2),
	}
	outdateValkeyContainer(dataPods[2])

	sentinelSts := buildTestSentinelSts(v)
	// Reports its pods, so the nudge does not supply the requeue asserted below.
	sentinelSts.Status.Replicas = 3
	sentinelSts.Status.ReadyReplicas = 3
	objs := []client.Object{v, sts, dataPods[0], dataPods[1], dataPods[2], sentinelSts}
	for i := 0; i < 3; i++ {
		objs = append(objs, createSentinelPod(v, i, "valkey/valkey:8.0", true))
	}
	r, c := newTestReconciler(objs...)
	r.InstanceChecker = masterOnPod0(v)

	result, err := r.reconcileWorkload(context.Background(), crGet(t, c, "dsh"))
	require.NoError(t, err)

	for i := 0; i < 3; i++ {
		assert.True(t, podExists(t, c, sentinelPodName(v, i)),
			"a holding data tier holds the Sentinel roll: %s must not be deleted", sentinelPodName(v, i))
	}
	assert.True(t, podExists(t, c, dataPods[2].Name), "the next data replica is not deleted either")
	cond := availabilityCondition(t, c, v)
	require.NotNil(t, cond)
	assert.Equal(t, vkov1.ReasonValkeyPodNotAvailable, cond.Reason)
	assert.Nil(t, getSentinelUpdatePending(t, c, v), "the Sentinel roll was not entered")
	assert.Equal(t, rollingUpdateRequeueDelay, result.RequeueAfter, "the pass continued and applied the deferred cadence")
	assert.NotContains(t, string(crGet(t, c, "dsh").Status.Phase), string(vkov1.ValkeyPhaseRollingUpdate),
		"updateStatus ran after the stalled roll and wrote its own verdict")
}

// Class exit: disabling Sentinel retracts a standing Sentinel report, which no
// Sentinel evaluator will ever run again to retract.
func TestHandlePostRollingUpdateChecks_SentinelDisabledRetractsTheSentinelReport(t *testing.T) {
	v := newTestValkey("sxr", "default", availabilityStalledTrue(vkov1.ReasonSentinelPodNotAvailable))
	r, c := newTestReconciler(v)

	_, done, err := r.handlePostRollingUpdateChecks(context.Background(), v, false)
	require.NoError(t, err)
	assert.False(t, done)

	cond := availabilityCondition(t, c, v)
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionFalse, cond.Status)
	assert.Equal(t, vkov1.ReasonPodAvailable, cond.Reason)
}

// And the class exit leaves a data report alone, and stamps nothing on a CR that
// carries no report at all.
func TestHandlePostRollingUpdateChecks_SentinelDisabledLeavesOtherCRsAlone(t *testing.T) {
	data := newTestValkey("sxd", "default", availabilityStalledTrue(vkov1.ReasonValkeyPodNotAvailable))
	bare := newTestValkey("sxb", "default")
	r, c := newTestReconciler(data, bare)

	_, _, err := r.handlePostRollingUpdateChecks(context.Background(), data, false)
	require.NoError(t, err)
	_, _, err = r.handlePostRollingUpdateChecks(context.Background(), bare, false)
	require.NoError(t, err)

	cond := availabilityCondition(t, c, data)
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionTrue, cond.Status)
	assert.Nil(t, availabilityCondition(t, c, bare), "no condition may be created on a CR that never carried one")
}

// sentinelWait is also where a resolved Sentinel termination stall is noticed: the
// completion hold and the quorum wait pass no delete gate, and a replacement that
// is still booting keeps the roll in one of them. Both entries retract a standing
// PodTerminationStalled once no Sentinel pod is terminating.
//
// Mutation check: deleting the clearPodTerminationStalled call from sentinelWait
// fails both rows.
func TestSentinelWait_ClearsAResolvedTerminationStall(t *testing.T) {
	stalledTrue := func(v *vkov1.Valkey) {
		meta.SetStatusCondition(&v.Status.Conditions, metav1.Condition{
			Type: vkov1.ConditionTypePodTerminationStalled, Status: metav1.ConditionTrue,
			Reason: vkov1.ReasonPodStuckTerminating, Message: "sentinel pod x-sentinel-2 is past its deadline",
		})
	}
	for _, tc := range []struct {
		name string
		pods func(v *vkov1.Valkey) []client.Object
	}{
		{"completion hold", func(v *vkov1.Valkey) []client.Object {
			return []client.Object{
				createSentinelPod(v, 0, sentinelTestNewImage, true),
				createSentinelPod(v, 1, sentinelTestNewImage, true),
				createSentinelPod(v, 2, sentinelTestNewImage, false),
			}
		}},
		{"quorum wait", func(v *vkov1.Valkey) []client.Object {
			return []client.Object{
				createSentinelPod(v, 0, "valkey/valkey:8.0", true),
				createSentinelPod(v, 1, "valkey/valkey:8.0", true),
				createSentinelPod(v, 2, sentinelTestNewImage, false),
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := sentinelHA("swc", sentinelUpdatePendingTrue, stalledTrue)
			objs := append([]client.Object{v, buildTestSentinelSts(v)}, tc.pods(v)...)
			r, c := newTestReconciler(objs...)

			result := r.checkAndHandleSentinelRollingUpdate(context.Background(), v)
			require.NoError(t, result.Error)
			assert.True(t, result.NeedsRequeue, "precondition: the pass holds in %s", tc.name)

			cond := meta.FindStatusCondition(crGet(t, c, "swc").Status.Conditions,
				vkov1.ConditionTypePodTerminationStalled)
			require.NotNil(t, cond)
			assert.Equal(t, metav1.ConditionFalse, cond.Status)
			assert.Equal(t, vkov1.ReasonPodTerminationCleared, cond.Reason)
		})
	}
}

// Row 2 of T32: standaloneWait carries the not-Ready clock too. It is reachable when
// a refused StatefulSet write leaves the live tier larger than spec.replicas: 1,
// and a current pod that never comes up is then waited on there.
//
// Mutation check: dropping notReadySince from standaloneWait's podState turns the
// stall back into the plain requeue and fails the DeferredRequeueAfter assertion.
func TestHandleStandaloneRollingUpdate_BoundsTheWaitOnACurrentPod(t *testing.T) {
	v := newTestValkey("saw", "default", func(v *vkov1.Valkey) { v.Spec.Replicas = 1 })
	sts := stsForValkey(v)
	two := int32(2)
	sts.Spec.Replicas = &two
	pod0 := notReadyFor(podFromStsTemplate(v, sts, 0), pastSyncBudget)
	pod1 := podFromStsTemplate(v, sts, 1)
	outdateValkeyContainer(pod1)
	r, c := newTestReconciler(v, sts, pod0, pod1)

	result := r.checkAndHandleRollingUpdate(context.Background(), crGet(t, c, "saw"))

	require.NoError(t, result.Error)
	assert.Equal(t, rollingUpdateRequeueDelay, result.DeferredRequeueAfter)
	cond := availabilityCondition(t, c, v)
	require.NotNil(t, cond)
	assert.Equal(t, vkov1.ReasonValkeyPodNotAvailable, cond.Reason)
	assert.Contains(t, cond.Message, "saw-0")
}

// Quorum already lost after a spec fix: two of three Sentinels stuck on the broken
// spec, one Ready. Replacing a stuck one spends no vote, and it is the only way the
// quorum comes back -- a guard charged against readyCount 1 refused forever.
//
// Mutation check: making the quorum guard unconditional again (dropping `cost > 0
// &&`) keeps every pod and fails the first assertion.
func TestSentinelRollingUpdate_ReplacesANonVotingPodWhenQuorumIsAlreadyLost(t *testing.T) {
	v := sentinelHA("sql")
	const oldImg = "valkey/valkey:8.0"
	sts := buildTestSentinelSts(v)
	r, c := newTestReconciler(v, sts,
		createSentinelPod(v, 0, oldImg, false),
		createSentinelPod(v, 1, oldImg, false),
		createSentinelPod(v, 2, oldImg, true))

	result := r.checkAndHandleSentinelRollingUpdate(context.Background(), v)
	require.NoError(t, result.Error)

	assert.False(t, podExists(t, c, sentinelPodName(v, 0)), "a non-voting outdated Sentinel is replaced")
	assert.True(t, podExists(t, c, sentinelPodName(v, 1)), "one at a time")
	assert.True(t, podExists(t, c, sentinelPodName(v, 2)), "the only voter is kept")
}

// A replacement that just came back on the same broken spec must not hide one that
// has been down for longer than the budget: the report names the oldest.
func TestSentinelRollingUpdate_ReportsTheLongestUnavailablePod(t *testing.T) {
	v := sentinelHA("sold", sentinelUpdatePendingTrue)
	sts := buildTestSentinelSts(v)
	r, _ := newTestReconciler(v, sts,
		createSentinelPod(v, 0, sentinelTestNewImage, true),
		notReadyFor(createSentinelPod(v, 1, sentinelTestNewImage, false), time.Minute),
		notReadyFor(createSentinelPod(v, 2, sentinelTestNewImage, false), pastSyncBudget))

	result := r.checkAndHandleSentinelRollingUpdate(context.Background(), v)
	require.NoError(t, result.Error)
	require.NotNil(t, result.availabilityStall)
	assert.Equal(t, sentinelPodName(v, 2), result.availabilityStall.name)
}

// kubelet stamps Ready=False at its first status sync, in the update that sets
// status.startTime. That is not a transition away from Ready: a pod that sat
// Pending longer than the budget keeps its creation clock when it is scheduled.
func TestPodNotReadySince_FirstSyncIsNotATransition(t *testing.T) {
	created := time.Now().Add(-10 * time.Minute).Truncate(time.Second)
	scheduled := time.Now().Add(-time.Minute).Truncate(time.Second)
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{CreationTimestamp: metav1.NewTime(created)},
		Status: corev1.PodStatus{
			StartTime: &metav1.Time{Time: scheduled},
			Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionFalse,
				LastTransitionTime: metav1.NewTime(scheduled.Add(2 * time.Second))}},
		},
	}
	assert.True(t, created.Equal(podNotReadySince(pod)), "never Ready since creation")

	flipped := time.Now().Add(-3 * time.Minute).Truncate(time.Second)
	pod.Status.StartTime = &metav1.Time{Time: time.Now().Add(-time.Hour)}
	pod.Status.Conditions[0].LastTransitionTime = metav1.NewTime(flipped)
	assert.True(t, flipped.Equal(podNotReadySince(pod)), "a pod that was Ready and flipped keeps the flip time")
}

// The retraction needs evidence: a pass that stopped at another wait first did not
// measure the stuck pod, and must not write "available" while it is still down.
//
// Mutation check: deleting the expiredUnavailablePod check from
// reportAvailabilityStall retracts the report in the first half.
func TestReportAvailabilityStall_RetractsOnlyOnEvidence(t *testing.T) {
	r, c, v, pods := dataStallFixture(t, pastSyncBudget,
		availabilityStalledTrue(vkov1.ReasonValkeyPodNotAvailable))

	// A pass whose result carries no stall, while the pod is still down.
	r.reportAvailabilityStall(context.Background(), v, "valkey", nil)
	cond := availabilityCondition(t, c, v)
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionTrue, cond.Status, "the stuck pod is still down: the report stands")

	live := &corev1.Pod{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: pods[1].Name, Namespace: "default"}, live))
	live.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}
	require.NoError(t, c.Status().Update(context.Background(), live))

	r.reportAvailabilityStall(context.Background(), crGet(t, c, "dst"), "valkey", nil)
	cond = availabilityCondition(t, c, v)
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionFalse, cond.Status, "with the pod up the report is retracted")
}

// The same on the Sentinel tier, through the path that used to flap: a terminating
// Sentinel takes priority in sentinelWait, the pass ends in terminationWait, and the
// stuck replacement was never looked at.
func TestSentinelRollingUpdate_TerminationPriorityDoesNotRetractTheReport(t *testing.T) {
	v := sentinelHA("sflap", sentinelUpdatePendingTrue,
		availabilityStalledTrue(vkov1.ReasonSentinelPodNotAvailable))
	sts := buildTestSentinelSts(v)
	going := createSentinelPod(v, 1, sentinelTestNewImage, true)
	terminatingFor(going, 30*time.Second, 0)
	r, c := newTestReconciler(v, sts,
		createSentinelPod(v, 0, sentinelTestNewImage, true),
		going,
		notReadyFor(createSentinelPod(v, 2, sentinelTestNewImage, false), pastSyncBudget))

	result := r.checkAndHandleSentinelRollingUpdate(context.Background(), v)
	require.NoError(t, result.Error)
	require.True(t, result.NeedsRequeue, "precondition: the pass ends in the termination wait")

	cond := availabilityCondition(t, c, v)
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionTrue, cond.Status)
	assert.Equal(t, vkov1.ReasonSentinelPodNotAvailable, cond.Reason)
}

// A tier of one or two Sentinels has no spare vote -- its quorum equals its size --
// so the quorum guard could never pass and such a tier never rolled. It rolls
// serially: one Sentinel at a time, only while every other one is available
// (ADR 0024 D10, decided 2026-09-26).
//
// Mutation check: making sentinelDeleteKeepsVotes return readyCount-cost >= quorum
// for every size keeps both pods and fails the first two rows.
func TestSentinelRollingUpdate_SmallTiersRollSerially(t *testing.T) {
	const oldImg = "valkey/valkey:8.0"
	for _, tc := range []struct {
		name        string
		pods        func(v *vkov1.Valkey) []client.Object
		wantDeleted int
	}{
		{"one Sentinel, outdated: replaced", func(v *vkov1.Valkey) []client.Object {
			return []client.Object{createSentinelPod(v, 0, oldImg, true)}
		}, 1},
		{"two Sentinels, both outdated and Ready: one replaced", func(v *vkov1.Valkey) []client.Object {
			return []client.Object{createSentinelPod(v, 0, oldImg, true), createSentinelPod(v, 1, oldImg, true)}
		}, 1},
		{"two Sentinels, the replacement still booting: the other is kept", func(v *vkov1.Valkey) []client.Object {
			return []client.Object{
				createSentinelPod(v, 0, sentinelTestNewImage, false), createSentinelPod(v, 1, oldImg, true),
			}
		}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pods := tc.pods(newTestValkey("small", "default"))
			replicas := int32(len(pods))
			v := newTestValkey("small", "default", func(v *vkov1.Valkey) {
				v.Spec.Replicas = 3
				v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: replicas}
			})
			r, c := newTestReconciler(append([]client.Object{v, buildTestSentinelSts(v)}, tc.pods(v)...)...)

			result := r.checkAndHandleSentinelRollingUpdate(context.Background(), v)
			require.NoError(t, result.Error)

			deleted := 0
			for i := 0; i < len(pods); i++ {
				if !podExists(t, c, sentinelPodName(v, i)) {
					deleted++
				}
			}
			assert.Equal(t, tc.wantDeleted, deleted)
		})
	}
}

func TestSentinelDeleteKeepsVotes(t *testing.T) {
	assert.True(t, sentinelDeleteKeepsVotes(3, 1, 2, 3), "three Sentinels: 2 voters remain")
	assert.False(t, sentinelDeleteKeepsVotes(2, 1, 2, 3), "three Sentinels, one down: the quorum guard holds")
	assert.True(t, sentinelDeleteKeepsVotes(2, 1, 2, 2), "two Sentinels, both up: serial delete")
	assert.False(t, sentinelDeleteKeepsVotes(1, 1, 2, 2), "two Sentinels, one down: wait for it")
	assert.True(t, sentinelDeleteKeepsVotes(1, 1, 1, 1), "one Sentinel: the only delete there is")
}
