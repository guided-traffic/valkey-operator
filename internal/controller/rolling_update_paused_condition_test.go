package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/builder"
)

// T15 / ADR 0002 D10, third instance. RollingUpdatePaused had exactly one clear,
// inside finalizeRollingUpdate, which only the Sentinel dispatch arm ever calls —
// so a multi-replica cluster without Sentinel could set the condition through
// three chains and had no code path that ever cleared it. A stale True is not
// cosmetic there: the collector exports every condition as
// vko_valkey_status_condition, so it is a permanently firing series.
//
// The second half of the same defect ran the other way. That single clear was not
// presence-guarded, and meta.SetStatusCondition ADDS an absent condition, so every
// Sentinel CR that ever completed a roll GAINED a condition it never had.
//
// Both halves are covered here. The fixtures are deliberately the ones
// sidecar_pending_condition_test.go already established: seeding the condition on a
// converged cluster proves nothing on its own, because checkAndHandleRollingUpdate
// has to actually reach a clear site.

// pausedCluster returns a three-replica cluster without Sentinel — the topology
// that could never clear the condition — carrying RollingUpdatePaused=True from an
// earlier pass that hit syncTimeout. annotations lets a caller decide whether the
// pass takes the converged early return (none) or the completion branch (rolling
// update state present).
func pausedCluster(t *testing.T, name string, annotations map[string]string,
	mutate func(*vkov1.Valkey, []*corev1.Pod) []client.Object,
) (*ValkeyReconciler, client.Client, *vkov1.Valkey) {
	t.Helper()

	v := newTestValkey(name, testNamespace, func(v *vkov1.Valkey) { v.Spec.Replicas = 3 })
	v.Annotations = annotations
	sts := stsForValkey(v)
	pods := []*corev1.Pod{
		podFromStsTemplate(v, sts, 0),
		podFromStsTemplate(v, sts, 1),
		podFromStsTemplate(v, sts, 2),
	}

	objs := []client.Object{v, sts}
	if mutate != nil {
		objs = append(objs, mutate(v, pods)...)
	} else {
		for _, pod := range pods {
			objs = append(objs, pod)
		}
	}

	r, c := newTestReconciler(objs...)
	cr := crGet(t, c, name)
	r.setStatusCondition(context.Background(), cr, vkov1.ConditionTypeRollingUpdatePaused,
		metav1.ConditionTrue, "SyncTimeout", "Pod "+name+"-1 replication sync timed out")
	require.Equal(t, metav1.ConditionTrue,
		conditionOf(t, c, cr, vkov1.ConditionTypeRollingUpdatePaused).Status,
		"the fixture must start paused, or nothing below proves a clear")
	return r, c, cr
}

func pausedCondition(t *testing.T, c client.Client, v *vkov1.Valkey) *metav1.Condition {
	t.Helper()
	return conditionOf(t, c, v, vkov1.ConditionTypeRollingUpdatePaused)
}

// [REGRESSION] N — the completing pass. This is the shape the Sentinel arm handled
// in finalizeRollingUpdate and the other two arms did not: every pod is on the
// current template while rolling update state is still recorded, so the pass gets
// past the converged early return and reports Completed.
func TestCheckAndHandleRollingUpdate_CompletionClearsRollingUpdatePaused(t *testing.T) {
	const name = "paused-complete"
	r, c, cr := pausedCluster(t, name,
		map[string]string{annotationRollingUpdateState: stateFailoverTriggered}, nil)

	result := r.checkAndHandleRollingUpdate(context.Background(), cr)

	require.Nil(t, result.Error)
	require.True(t, result.Completed,
		"the fixture must reach the completion branch, or this test proves nothing")

	cond := pausedCondition(t, c, cr)
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionFalse, cond.Status,
		"a roll that completed is not a roll that is paused")
	assert.Equal(t, vkov1.ReasonRollingUpdateCompleted, cond.Reason)
}

// [REGRESSION] N2 — the converged early return, which the completion branch alone
// does not cover and which hits SENTINEL clusters too. After a pause the state
// annotation is gone (pauseRollingUpdate clears it), so putting the spec back to
// what the pods already run leaves nothing for the pass to do: it takes the early
// return, finalizeRollingUpdate is never reached, and under a completion-only fix
// nobody clears.
func TestCheckAndHandleRollingUpdate_ConvergedEarlyReturnClearsRollingUpdatePaused(t *testing.T) {
	const name = "paused-converged"
	r, c, cr := pausedCluster(t, name, nil, nil)

	result := r.checkAndHandleRollingUpdate(context.Background(), cr)

	require.Nil(t, result.Error)
	require.False(t, result.NeedsRequeue, "no pod needs an update, so nothing is in flight")
	require.False(t, result.Completed, "the early return reports no completion")

	cond := pausedCondition(t, c, cr)
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionFalse, cond.Status,
		"every pod matches the live template and is Ready, so there is no roll left to pause")
	assert.Equal(t, vkov1.ReasonRollingUpdateConverged, cond.Reason,
		"the roll did not complete here, it became unnecessary; saying 'completed' would be a "+
			"false statement on the CR")
}

// P — the pause is telling the truth while work remains. An outdated pod means the
// pass dispatches instead of returning, and neither clear site is reached.
func TestCheckAndHandleRollingUpdate_KeepsThePauseWhileAnOutdatedPodRemains(t *testing.T) {
	const name = "paused-work-left"
	r, c, cr := pausedCluster(t, name, nil,
		func(v *vkov1.Valkey, pods []*corev1.Pod) []client.Object {
			for i := range pods[2].Spec.Containers {
				if pods[2].Spec.Containers[i].Name == builder.SidecarContainerName {
					pods[2].Spec.Containers[i].Image = "ghcr.io/guided-traffic/valkey-operator:previous"
				}
			}
			return []client.Object{pods[0], pods[1], pods[2]}
		})

	_ = r.checkAndHandleRollingUpdate(context.Background(), cr)

	cond := pausedCondition(t, c, cr)
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionTrue, cond.Status,
		"a pod still on the old template means the roll is unfinished, and the pause is accurate")
}

// [REGRESSION] Q — the reason the early-return clear is gated rather than
// unconditional. A pod the operator has deleted and is waiting on is simply absent,
// and the ordinal loop skips an absent pod without ever marking the tier as
// needing an update. An ungated clear would therefore write False onto a roll that
// is stuck mid-replacement — turning a permanently stale True into a permanently
// wrong False, which is worse for the alerting surface this fix exists for.
func TestCheckAndHandleRollingUpdate_KeepsThePauseWhenAPodIsMissing(t *testing.T) {
	const name = "paused-pod-missing"
	r, c, cr := pausedCluster(t, name, nil,
		func(v *vkov1.Valkey, pods []*corev1.Pod) []client.Object {
			return []client.Object{pods[0], pods[1]} // pod-2 deleted, not yet recreated
		})

	result := r.checkAndHandleRollingUpdate(context.Background(), cr)

	require.Nil(t, result.Error)
	cond := pausedCondition(t, c, cr)
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionTrue, cond.Status,
		"an absent pod is not a converged tier: the roll is mid-replacement, not resolved")
}

// [REGRESSION] Q2 — the same gate from the other side. The pod came back on the
// current template but has not become Ready, so it matches the template while the
// tier has not converged.
func TestCheckAndHandleRollingUpdate_KeepsThePauseWhenAPodIsNotReady(t *testing.T) {
	const name = "paused-pod-unready"
	r, c, cr := pausedCluster(t, name, nil,
		func(v *vkov1.Valkey, pods []*corev1.Pod) []client.Object {
			pods[2].Status.Conditions = []corev1.PodCondition{
				{Type: corev1.PodReady, Status: corev1.ConditionFalse},
			}
			return []client.Object{pods[0], pods[1], pods[2]}
		})

	result := r.checkAndHandleRollingUpdate(context.Background(), cr)

	require.Nil(t, result.Error)
	cond := pausedCondition(t, c, cr)
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionTrue, cond.Status,
		"a pod on the current template that is not Ready has not finished being replaced")
}

// The blast radius of both clears, kept at zero. meta.SetStatusCondition adds an
// absent condition and reports a change, so an unguarded call writes the condition
// onto every CR in the fleet on the first upgraded pass (ADR 0005 D10).
func TestCheckAndHandleRollingUpdate_DoesNotAddThePausedConditionToACleanCluster(t *testing.T) {
	const name = "paused-clean"
	v := newTestValkey(name, testNamespace, func(v *vkov1.Valkey) { v.Spec.Replicas = 3 })
	sts := stsForValkey(v)
	r, c := newTestReconciler(v, sts,
		podFromStsTemplate(v, sts, 0), podFromStsTemplate(v, sts, 1), podFromStsTemplate(v, sts, 2))
	cr := crGet(t, c, name)

	result := r.checkAndHandleRollingUpdate(context.Background(), cr)

	require.Nil(t, result.Error)
	assert.Nil(t, pausedCondition(t, c, cr),
		"a cluster that never paused has nothing to report")
}

// [REGRESSION] G-live — half two of the defect, and the half that was already on
// the fleet rather than waiting to happen. finalizeRollingUpdate wrote
// RollingUpdatePaused=False unconditionally at the end of every Sentinel roll, so
// clusters that had never paused gained the condition. The write is gone; the clear
// now happens one frame up, presence-guarded, for every topology.
func TestFinalizeRollingUpdate_DoesNotAddThePausedConditionToACleanCluster(t *testing.T) {
	const name = "paused-finalize-clean"
	r, c, v, pods := finalizingCluster(t, name, nil, nil)

	result := r.finalizeRollingUpdate(context.Background(), v, pods)

	require.Nil(t, result.Error)
	require.True(t, result.Completed)
	assert.Nil(t, pausedCondition(t, c, v),
		"completing a roll is not a reason to stamp a pause report onto a cluster that never paused")
}
