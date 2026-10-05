package controller

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/builder"
)

// A Sentinel cluster with one data pod is rolled as a single pod
// (docs/adr/0007-failover-aware-rolling-update.md, D11). The failover roll it used to
// take has no replica to promote: Sentinel refused every failover, the next pass
// discarded the recorded state as stale and asked again, and neither the data pod nor
// the Sentinel tier behind it was ever replaced.

// sentinelSinglePod is the default shape of a CR that enables Sentinel without
// setting spec.replicas: one data pod beside three Sentinels.
func sentinelSinglePod(name string, opts ...func(*vkov1.Valkey)) *vkov1.Valkey {
	return newTestValkey(name, testNamespace, append([]func(*vkov1.Valkey){func(v *vkov1.Valkey) {
		v.Spec.Replicas = 1
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
	}}, opts...)...)
}

func withDataPersistence(v *vkov1.Valkey) {
	v.Spec.Persistence = &vkov1.PersistenceSpec{Enabled: true}
}

func outdateSidecarContainer(pod *corev1.Pod) {
	for i := range pod.Spec.Containers {
		if pod.Spec.Containers[i].Name == builder.SidecarContainerName {
			pod.Spec.Containers[i].Image = "ghcr.io/guided-traffic/valkey-operator:previous"
			return
		}
	}
	panic("pod has no " + builder.SidecarContainerName + " container")
}

// The only data pod is replaced the way a single pod without Sentinel is: an image
// change on a pod without a volume, and the rootless posture on a pod with one
// (ADR 0032 D3). No failover is asked for, and the replacement is recorded.
//
// Mutation check: routing every Sentinel cluster to handleRollingUpdate again (the old
// dispatch) keeps the pod, records failover-triggered with its timestamp and emits
// FailoverTriggered -- the first step of the loop this route replaces.
func TestCheckAndHandleRollingUpdate_SentinelSinglePodIsReplacedWithoutAFailover(t *testing.T) {
	for _, tc := range []struct {
		name    string
		opts    []func(*vkov1.Valkey)
		outdate func(*corev1.Pod)
	}{
		{"an image change", nil, outdateValkeyContainer},
		{"a persistent pod that still runs as root", []func(*vkov1.Valkey){withDataPersistence},
			func(pod *corev1.Pod) { legacy(pod) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := sentinelSinglePod("ssp", tc.opts...)
			sts := stsForValkey(v)
			pod0 := podFromStsTemplate(v, sts, 0)
			tc.outdate(pod0)
			r, c := newTestReconciler(v, sts, pod0)
			r.InstanceChecker = masterOnPod0(v)

			result := r.checkAndHandleRollingUpdate(context.Background(), crGet(t, c, v.Name))

			require.NoError(t, result.Error)
			assert.True(t, result.NeedsRequeue)
			assert.False(t, podExists(t, c, pod0.Name), "the only data pod is replaced")
			cr := crGet(t, c, v.Name)
			assert.Equal(t, stateReplacingReplicas, cr.Annotations[annotationRollingUpdateState],
				"the replacement is recorded, so the passes until it is back hold the Sentinel roll")
			assert.Empty(t, cr.Annotations[annotationFailoverTimestamp], "no failover was asked for")
			assert.Empty(t, r.Recorder.(*fakeEventRecorder).withReason("FailoverTriggered"))
			assert.Nil(t, conditionOf(t, c, v, vkov1.ConditionTypePodSecurityUpdatePending),
				"a replaced pod holds nothing back")
		})
	}
}

// A cluster the old loop ran on carries its failover state into the first pass of
// this route. It is restated as a replacement in flight, and a deferral of an
// available pod settles it: nothing on this route completes while a change is
// deferred, so without the settle the state would stay.
//
// Mutation check: dropping the settle case in handleSentinelSinglePodRollingUpdate
// leaves replacing-replicas on the CR.
func TestCheckAndHandleRollingUpdate_SentinelSinglePodClearsALeftoverFailoverState(t *testing.T) {
	v := sentinelSinglePod("ssl", func(v *vkov1.Valkey) {
		v.Annotations = map[string]string{
			annotationRollingUpdateState: stateFailoverTriggered,
			annotationFailoverTimestamp:  time.Now().UTC().Format(time.RFC3339),
		}
	})
	sts := stsForValkey(v)
	pod0 := podFromStsTemplate(v, sts, 0)
	outdateSidecarContainer(pod0)
	r, c := newTestReconciler(v, sts, pod0)

	result := r.checkAndHandleRollingUpdate(context.Background(), crGet(t, c, v.Name))

	require.NoError(t, result.Error)
	assert.False(t, result.NeedsRequeue, "a deferral ends the pass on nothing")
	assert.False(t, result.Completed)
	assert.True(t, podExists(t, c, pod0.Name), "a sidecar-only change does not restart the only pod")
	cr := crGet(t, c, v.Name)
	assert.Empty(t, cr.Annotations[annotationRollingUpdateState])
	assert.Empty(t, cr.Annotations[annotationFailoverTimestamp])
	cond := conditionOf(t, c, v, vkov1.ConditionTypeSidecarUpdatePending)
	require.NotNil(t, cond, "the deferral is reported")
	assert.Equal(t, metav1.ConditionTrue, cond.Status)
}

// A pod without a volume that still runs as root is held under the single-pod rule of
// ADR 0032 D3 and named, where the loop kept it on root without a word.
//
// Mutation check: the old dispatch leaves PodSecurityUpdatePending unwritten.
func TestCheckAndHandleRollingUpdate_SentinelSinglePodReportsAHeldRootPod(t *testing.T) {
	v := sentinelSinglePod("ssr")
	sts := stsForValkey(v)
	pod0 := legacy(podFromStsTemplate(v, sts, 0))
	r, c := newTestReconciler(v, sts, pod0)

	result := r.checkAndHandleRollingUpdate(context.Background(), crGet(t, c, v.Name))

	require.NoError(t, result.Error)
	assert.True(t, podExists(t, c, pod0.Name), "replacing it would discard the dataset")
	assert.Empty(t, crGet(t, c, v.Name).Annotations[annotationRollingUpdateState], "a deferral records no roll")
	cond := conditionOf(t, c, v, vkov1.ConditionTypePodSecurityUpdatePending)
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionTrue, cond.Status)
	assert.Equal(t, vkov1.ReasonPodRunsAsRoot, cond.Reason)
	assert.Contains(t, cond.Message, pod0.Name)
}

// Pass by pass through a replacement. While the only data pod is missing and while
// its replacement is not available, the recorded state keeps the dispatch on this
// route and its waits hold the Sentinel roll (ADR 0026 D11). Once the replacement is
// available the data roll completes, announces RollingUpdateComplete and clears its
// state, and the Sentinel roll starts in the same pass (ADR 0024 D1).
//
// Mutation checks: dropping recordSinglePodReplacement lets the second pass delete a
// Sentinel pod while the data tier has none; dropping the RollingUpdateComplete Event
// in handleSentinelSinglePodRollingUpdate fails the last pass.
func TestReconcileWorkload_SentinelSinglePodReplacementHoldsTheSentinelRoll(t *testing.T) {
	v := sentinelSinglePod("ssh", withDataPersistence)
	sts := stsForValkey(v)
	pod0 := podFromStsTemplate(v, sts, 0)
	outdateValkeyContainer(pod0)

	sentinelSts := buildTestSentinelSts(v)
	// Reports its pods, so the nudge does not supply a requeue of its own.
	sentinelSts.Status.Replicas = 3
	sentinelSts.Status.ReadyReplicas = 3
	objs := []client.Object{v, sts, pod0, sentinelSts}
	for i := 0; i < 3; i++ {
		objs = append(objs, createSentinelPod(v, i, "valkey/valkey:8.0", true))
	}
	r, c := newTestReconciler(objs...)
	ctx := context.Background()
	sentinelPods := func() int {
		n := 0
		for i := 0; i < 3; i++ {
			if podExists(t, c, sentinelPodName(v, i)) {
				n++
			}
		}
		return n
	}
	pass := func() {
		t.Helper()
		_, err := r.reconcileWorkload(ctx, crGet(t, c, v.Name))
		require.NoError(t, err)
	}

	pass()
	require.False(t, podExists(t, c, pod0.Name), "the outdated data pod is deleted first")
	assert.Equal(t, 3, sentinelPods(), "the pass that deletes the data pod ends on it")

	pass()
	assert.Equal(t, 3, sentinelPods(), "the Sentinel roll waits while the data tier has no pod")

	booting := podFromStsTemplate(v, sts, 0)
	booting.Status.Conditions = []corev1.PodCondition{{
		Type: corev1.PodReady, Status: corev1.ConditionFalse, LastTransitionTime: metav1.Now(),
	}}
	require.NoError(t, c.Create(ctx, booting))
	pass()
	assert.Equal(t, 3, sentinelPods(), "and while the replacement is not available")
	assert.Empty(t, r.Recorder.(*fakeEventRecorder).withReason("RollingUpdateComplete"))

	markReady(t, c, booting.Name)
	pass()
	assert.Len(t, r.Recorder.(*fakeEventRecorder).withReason("RollingUpdateComplete"), 1,
		"the data roll announces its completion")
	assert.Empty(t, crGet(t, c, v.Name).Annotations[annotationRollingUpdateState], "and clears its state")
	assert.Equal(t, 2, sentinelPods(), "the Sentinel roll starts in the pass that completes the data roll")
	assert.Empty(t, r.Recorder.(*fakeEventRecorder).withReason("FailoverTriggered"))
}

// sentinelSinglePodFleet is a Sentinel cluster with one data pod and three Ready
// Sentinel pods on an outdated spec, so a pass that reaches the Sentinel roll deletes
// one of them.
func sentinelSinglePodFleet(t *testing.T, v *vkov1.Valkey, dataPods ...*corev1.Pod) (*ValkeyReconciler, client.Client) {
	t.Helper()
	sentinelSts := buildTestSentinelSts(v)
	// Reports its pods, so the nudge does not supply a requeue of its own.
	sentinelSts.Status.Replicas = 3
	sentinelSts.Status.ReadyReplicas = 3
	objs := []client.Object{v, stsForValkey(v), sentinelSts}
	for _, pod := range dataPods {
		objs = append(objs, pod)
	}
	for i := 0; i < 3; i++ {
		objs = append(objs, createSentinelPod(v, i, "valkey/valkey:8.0", true))
	}
	return newTestReconciler(objs...)
}

func sentinelPodsLeft(t *testing.T, c client.Client, v *vkov1.Valkey) int {
	t.Helper()
	n := 0
	for i := 0; i < 3; i++ {
		if podExists(t, c, sentinelPodName(v, i)) {
			n++
		}
	}
	return n
}

// A deferral of a pod no replacement is recorded for holds nothing: the Sentinel roll
// runs in the same pass beside the held data pod, and no RollingUpdateComplete fires,
// because the data tier did not roll.
//
// Mutation check: returning a DeferredRequeueAfter on the deferral keeps every
// Sentinel pod.
func TestReconcileWorkload_SentinelSinglePodDeferralReleasesTheSentinelRoll(t *testing.T) {
	v := sentinelSinglePod("ssf")
	pod0 := podFromStsTemplate(v, stsForValkey(v), 0)
	outdateSidecarContainer(pod0)
	r, c := sentinelSinglePodFleet(t, v, pod0)

	_, err := r.reconcileWorkload(context.Background(), crGet(t, c, v.Name))
	require.NoError(t, err)

	assert.True(t, podExists(t, c, pod0.Name), "a sidecar-only change does not restart the only pod")
	assert.Equal(t, 2, sentinelPodsLeft(t, c, v), "the Sentinel roll runs beside the held data pod")
	assert.Empty(t, r.Recorder.(*fakeEventRecorder).withReason("RollingUpdateComplete"))
}

// An operator upgrade that moves the sidecar while a replacement is on its way turns
// the replacement into a sidecar-only deferral. It must not release the Sentinel roll
// before the replacement is available -- it may be a pod that never starts -- and once
// it is, the recorded state is cleared.
//
// Mutation check: dropping the settle case in handleSentinelSinglePodRollingUpdate
// deletes a Sentinel pod in the first pass, while the replacement is not Ready.
func TestReconcileWorkload_SentinelSinglePodSettlesADeferredReplacementFirst(t *testing.T) {
	v := sentinelSinglePod("sss", func(v *vkov1.Valkey) {
		v.Annotations = map[string]string{annotationRollingUpdateState: stateReplacingReplicas}
	})
	pod0 := podFromStsTemplate(v, stsForValkey(v), 0)
	outdateSidecarContainer(pod0)
	pod0.Status.Conditions = []corev1.PodCondition{{
		Type: corev1.PodReady, Status: corev1.ConditionFalse, LastTransitionTime: metav1.Now(),
	}}
	r, c := sentinelSinglePodFleet(t, v, pod0)
	ctx := context.Background()

	_, err := r.reconcileWorkload(ctx, crGet(t, c, v.Name))
	require.NoError(t, err)
	assert.Equal(t, 3, sentinelPodsLeft(t, c, v), "the replacement is not available yet")
	assert.Equal(t, stateReplacingReplicas, crGet(t, c, v.Name).Annotations[annotationRollingUpdateState])
	assert.True(t, podExists(t, c, pod0.Name), "a deferred pod is not deleted")

	markReady(t, c, pod0.Name)
	_, err = r.reconcileWorkload(ctx, crGet(t, c, v.Name))
	require.NoError(t, err)
	assert.Empty(t, crGet(t, c, v.Name).Annotations[annotationRollingUpdateState], "the settled replacement is cleared")
	assert.Equal(t, 2, sentinelPodsLeft(t, c, v), "and the deferral releases the Sentinel roll")
	assert.Empty(t, r.Recorder.(*fakeEventRecorder).withReason("RollingUpdateComplete"))
}

// A scale-down to one data pod in the middle of the failover roll can leave that roll's
// state behind while the only pod is being replaced. The state is restated as a
// replacement in flight, not dropped, so the Sentinel roll keeps waiting for the pod.
//
// Mutation check: clearing the leftover state instead of restating it releases the
// Sentinel roll in the second pass, with no data pod.
func TestReconcileWorkload_SentinelSinglePodRestatesALeftoverFailoverState(t *testing.T) {
	v := sentinelSinglePod("ssm", func(v *vkov1.Valkey) {
		v.Annotations = map[string]string{
			annotationRollingUpdateState: stateReplacingMaster,
			annotationFailoverTimestamp:  time.Now().UTC().Format(time.RFC3339),
		}
	})
	r, c := sentinelSinglePodFleet(t, v)
	ctx := context.Background()

	for pass := 1; pass <= 2; pass++ {
		_, err := r.reconcileWorkload(ctx, crGet(t, c, v.Name))
		require.NoError(t, err)
		cr := crGet(t, c, v.Name)
		assert.Equal(t, stateReplacingReplicas, cr.Annotations[annotationRollingUpdateState], "pass %d", pass)
		assert.Empty(t, cr.Annotations[annotationFailoverTimestamp], "pass %d", pass)
		assert.Equal(t, 3, sentinelPodsLeft(t, c, v), "pass %d: the Sentinel roll waits for the data pod", pass)
	}
}

// A replacement somebody else started -- an eviction, a manual delete of the outdated
// pod -- is recorded like the operator's own, so the Sentinel roll waits for it too.
//
// Mutation check: recording after the terminating check leaves no state.
func TestCheckAndHandleRollingUpdate_SentinelSinglePodRecordsAReplacementItDidNotStart(t *testing.T) {
	v := sentinelSinglePod("sst")
	pod0 := podFromStsTemplate(v, stsForValkey(v), 0)
	outdateValkeyContainer(pod0)
	terminatingFor(pod0, 75*time.Second, 10*time.Second)
	r, c := newTestReconciler(v, stsForValkey(v), pod0)

	result := r.checkAndHandleRollingUpdate(context.Background(), crGet(t, c, v.Name))

	require.NoError(t, result.Error)
	assert.True(t, result.NeedsRequeue, "the terminating pod is waited on")
	assert.Equal(t, stateReplacingReplicas, crGet(t, c, v.Name).Annotations[annotationRollingUpdateState])
}

// A CR that asks for three data pods over a StatefulSet that still has one -- a
// scale-up the StatefulSet does not carry yet, or one a refused write keeps from it --
// takes the failover roll, whose only pod has no replica to fail over to. It waits for
// the scale-up with the pass continuing, instead of asking Sentinel for a failover it
// can only refuse.
//
// Mutation check: dropping the tier-of-one guard in handleRollingUpdate records
// failover-triggered and emits FailoverTriggered.
func TestHandleRollingUpdate_ATierOfOneAsksForNoFailover(t *testing.T) {
	v := sentinelSinglePod("sso", func(v *vkov1.Valkey) { v.Spec.Replicas = 3 })
	one := v.DeepCopy()
	one.Spec.Replicas = 1
	sts := stsForValkey(one)
	pod0 := podFromStsTemplate(v, sts, 0)
	outdateValkeyContainer(pod0)
	r, c := newTestReconciler(v, sts, pod0)
	r.InstanceChecker = masterOnPod0(v)

	result := r.rollDataTier(context.Background(), crGet(t, c, v.Name), getSts(t, c, sts.Name))

	require.NoError(t, result.Error)
	assert.False(t, result.NeedsRequeue, "the pass continues")
	assert.Equal(t, rollingUpdateRequeueDelay, result.DeferredRequeueAfter)
	assert.True(t, podExists(t, c, pod0.Name))
	assert.Empty(t, crGet(t, c, v.Name).Annotations[annotationRollingUpdateState], "no failover is recorded")
	assert.Empty(t, r.Recorder.(*fakeEventRecorder).withReason("FailoverTriggered"))
}

// TLS or auth switched on while the only data pod still runs the sidecar of an earlier
// operator is a configuration change, not a sidecar-only delta: the pod is replaced and
// the replacement recorded, so the Sentinel tier -- which takes the new configuration
// as well -- rolls only after the data pod serves it.
//
// Mutation check: dropping the podAnnotationHashChanged term of sidecarOnlyDelta defers
// the pod and records nothing.
func TestCheckAndHandleRollingUpdate_SentinelSinglePodReplacesAConfigChangeBehindAStaleSidecar(t *testing.T) {
	v := sentinelSinglePod("ssc")
	sts := stsForValkey(v)
	pod0 := podFromStsTemplate(v, sts, 0)
	outdateSidecarContainer(pod0)
	pod0.Annotations[builder.AnnotationConfigHash] = "00000000"
	r, c := newTestReconciler(v, sts, pod0)

	result := r.checkAndHandleRollingUpdate(context.Background(), crGet(t, c, v.Name))

	require.NoError(t, result.Error)
	assert.True(t, result.NeedsRequeue)
	assert.False(t, podExists(t, c, pod0.Name), "the configuration change replaces the pod")
	assert.Equal(t, stateReplacingReplicas, crGet(t, c, v.Name).Annotations[annotationRollingUpdateState])
	assert.Nil(t, conditionOf(t, c, v, vkov1.ConditionTypeSidecarUpdatePending), "nothing is deferred")
}

// A scale-down the StatefulSet does not carry yet keeps the failover roll: the CR
// asks for one data pod, but three exist, and the single-pod handler would delete the
// master without a failover.
//
// Mutation check: routing on the CR count alone sends this cluster to the single-pod
// handler, which deletes the outdated master test-0.
func TestRollDataTier_SentinelScaleDownInFlightKeepsTheFailoverRoll(t *testing.T) {
	v := sentinelSinglePod("ssd")
	three := v.DeepCopy()
	three.Spec.Replicas = 3
	sts := stsForValkey(three)
	pods := []*corev1.Pod{
		podFromStsTemplate(v, sts, 0),
		podFromStsTemplate(v, sts, 1),
		podFromStsTemplate(v, sts, 2),
	}
	for _, pod := range pods {
		outdateValkeyContainer(pod)
	}
	r, c := newTestReconciler(v, sts, pods[0], pods[1], pods[2])
	r.InstanceChecker = masterOnPod0(v)

	result := r.rollDataTier(context.Background(), crGet(t, c, v.Name), getSts(t, c, sts.Name))

	require.NoError(t, result.Error)
	assert.True(t, podExists(t, c, pods[0].Name), "the master is not deleted without a failover")
}

func TestSingleDataPodBehindSentinel(t *testing.T) {
	for _, tc := range []struct {
		name       string
		sentinel   bool
		crReplicas int32
		stsCount   *int32
		want       bool
	}{
		{"Sentinel, one pod by CR and StatefulSet", true, 1, ptr.To[int32](1), true},
		{"Sentinel, scale-down not yet carried by the StatefulSet", true, 1, ptr.To[int32](3), false},
		{"Sentinel, scale-up not yet carried by the StatefulSet", true, 3, ptr.To[int32](1), false},
		{"Sentinel, three pods", true, 3, ptr.To[int32](3), false},
		{"no Sentinel, one pod", false, 1, ptr.To[int32](1), false},
		{"Sentinel, StatefulSet without a count", true, 1, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := newTestValkey("sd", testNamespace, func(v *vkov1.Valkey) {
				v.Spec.Replicas = tc.crReplicas
				if tc.sentinel {
					v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
				}
			})
			sts := &appsv1.StatefulSet{Spec: appsv1.StatefulSetSpec{Replicas: tc.stsCount}}
			assert.Equal(t, tc.want, singleDataPodBehindSentinel(v, sts))
		})
	}
}

func markReady(t *testing.T, c client.Client, podName string) {
	t.Helper()
	pod := &corev1.Pod{}
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: podName, Namespace: testNamespace}, pod))
	pod.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}
	// The fake client serves Pod status as a subresource: a plain Update drops it.
	require.NoError(t, c.Status().Update(context.Background(), pod))
}
