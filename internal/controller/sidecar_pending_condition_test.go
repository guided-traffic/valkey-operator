package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/builder"
)

// ADR 0002 D10. SidecarUpdatePending has a False branch, and on the transition that matters
// it was unreachable. Its only caller sits at the end of handleStandaloneRollingUpdate,
// which is only entered while a rolling update is needed or a state annotation is set
// -- so the moment the deferred update actually applies (the pod is deleted, comes
// back on the current template) checkAndHandleRollingUpdate returns before dispatching
// and the condition stays True with reason SidecarImageDrift forever. A converged
// cluster then reports permanent drift, indistinguishable from one that never applied
// the update.

// standaloneUpToDate builds a single-pod cluster whose pod matches the persisted
// StatefulSet template in every respect -- the state right after the deferred sidecar
// update was picked up by a pod restart.
func standaloneUpToDate(t *testing.T, name string) (*ValkeyReconciler, *vkov1.Valkey) {
	t.Helper()
	v := newTestValkey(name, testNamespace, func(v *vkov1.Valkey) { v.Spec.Replicas = 1 })
	sts := stsForValkey(v)
	r, c := newTestReconciler(v, sts, podFromStsTemplate(v, sts, 0))
	return r, crGet(t, c, name)
}

func sidecarCondition(v *vkov1.Valkey) *metav1.Condition {
	return apimeta.FindStatusCondition(v.Status.Conditions, vkov1.ConditionTypeSidecarUpdatePending)
}

// [REGRESSION] The pending-to-resolved transition.
func TestCheckAndHandleRollingUpdate_ClearsSidecarUpdatePendingOnceApplied(t *testing.T) {
	const name = "sidecar-clear"
	r, v := standaloneUpToDate(t, name)
	ctx := context.Background()

	// The condition an earlier pass left behind while the sidecar image had drifted.
	r.setSidecarUpdatePendingCondition(ctx, v, v.Name+"-0")
	require.Equal(t, metav1.ConditionTrue, sidecarCondition(v).Status)

	result := r.checkAndHandleRollingUpdate(ctx, v)
	require.Nil(t, result.Error)
	require.False(t, result.NeedsRequeue, "no pod needs an update, so nothing is in flight")

	cond := sidecarCondition(v)
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionFalse, cond.Status,
		"every pod matches the template here, which is exactly what 'no longer pending' means")
	assert.Equal(t, "SidecarUpToDate", cond.Reason)
}

// The blast radius of the clear, deliberately kept at zero: a cluster that never had
// a sidecar drift must not gain a condition it never had. meta.SetStatusCondition adds
// an absent condition and reports a change, so an unguarded call would write
// SidecarUpdatePending=False onto every CR in the fleet on the first upgraded pass.
func TestCheckAndHandleRollingUpdate_DoesNotAddSidecarConditionToACleanCluster(t *testing.T) {
	const name = "sidecar-clean"
	r, v := standaloneUpToDate(t, name)

	result := r.checkAndHandleRollingUpdate(context.Background(), v)

	require.Nil(t, result.Error)
	assert.Nil(t, sidecarCondition(v),
		"a cluster that never deferred a sidecar update has nothing to report")
}

// The clear must not fire while a rolling update is still in flight -- that is the
// one state in which the condition is telling the truth. A CR carrying rolling-update
// state does not reach the early return at all.
func TestCheckAndHandleRollingUpdate_KeepsThePendingConditionWhileWorkRemains(t *testing.T) {
	const name = "sidecar-pending"
	v := newTestValkey(name, testNamespace, func(v *vkov1.Valkey) { v.Spec.Replicas = 1 })
	sts := stsForValkey(v)
	pod0 := podFromStsTemplate(v, sts, 0)
	for i := range pod0.Spec.Containers {
		if pod0.Spec.Containers[i].Name == builder.SidecarContainerName {
			pod0.Spec.Containers[i].Image = "ghcr.io/guided-traffic/valkey-operator:previous"
		}
	}
	r, c := newTestReconciler(v, sts, pod0)
	cr := crGet(t, c, name)

	result := r.checkAndHandleRollingUpdate(context.Background(), cr)

	require.Nil(t, result.Error)
	cond := sidecarCondition(cr)
	require.NotNil(t, cond, "the drift is real and still deferred")
	assert.Equal(t, metav1.ConditionTrue, cond.Status)
	assert.Equal(t, "SidecarImageDrift", cond.Reason)
}

// [REGRESSION] The completing pass clears it too, and it is a different pass than the one
// above.
//
// ADR 0002 D10 put the clear in front of the converged early return, which is the only
// site that proves every pod matches the live template. That left a gap nobody looked for:
// the pass that COMPLETES a roll got past that early return by definition -- a pod needed
// updating, or state was recorded -- so it never reached the clear, and completion cleared
// only the state annotation. The completing pass also schedules no follow-up, and the CR
// watch is generation-gated with no Pod watch behind it, so the next guaranteed pass is
// the owned-object cache resync.
//
// Measured on a fleet before the fix: four clusters completed their roll at 21:33, and the
// clear landed 1 s, 3 s, 6 min and 41 min later -- every one of them only because an
// unrelated pod-kill happened to enqueue a pass. A fleet audit read the CRs inside that
// window and filed a 41-minute lag as a permanent stall.
func TestCheckAndHandleRollingUpdate_CompletionClearsSidecarUpdatePending(t *testing.T) {
	const name = "sidecar-complete"
	v := newTestValkey(name, testNamespace, func(v *vkov1.Valkey) { v.Spec.Replicas = 3 })
	// All pods already match the template while state is still recorded: the shape the
	// dispatch comment describes, where the previous pass saw every pod updated and
	// requeued before finalizeRollingUpdate ran.
	v.Annotations = map[string]string{annotationRollingUpdateState: stateFailoverTriggered}
	sts := stsForValkey(v)
	r, c := newTestReconciler(v, sts,
		podFromStsTemplate(v, sts, 0), podFromStsTemplate(v, sts, 1), podFromStsTemplate(v, sts, 2))

	ctx := context.Background()
	cr := crGet(t, c, name)
	r.setSidecarUpdatePendingCondition(ctx, cr, name+"-0")
	require.Equal(t, metav1.ConditionTrue, sidecarCondition(cr).Status)

	result := r.checkAndHandleRollingUpdate(ctx, cr)
	require.Nil(t, result.Error)
	require.True(t, result.Completed,
		"the fixture must reach the completion branch, or this test proves nothing")

	cond := sidecarCondition(crGet(t, c, name))
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionFalse, cond.Status,
		"a completed roll replaced every pod, so a deferred sidecar update cannot still be pending")
	assert.Equal(t, "SidecarUpToDate", cond.Reason)
}

// The message names the pod and the replica count the deferral decision was made on. It
// used to say "Standalone pod has an outdated sidecar image" with no pod name, which was
// read on a three-replica cluster during a fleet audit and looked like a contradiction
// rather than the legacy value it was. The mismatch is still reachable today: the guard
// reads spec.replicas while the loop walks the live StatefulSet, and a refused StatefulSet
// write holds those two apart (ADR 0023).
func TestSetSidecarUpdatePendingCondition_MessageNamesThePodAndTheReplicaCount(t *testing.T) {
	const name = "sidecar-msg"
	r, v := standaloneUpToDate(t, name)

	r.setSidecarUpdatePendingCondition(context.Background(), v, name+"-0")

	cond := sidecarCondition(v)
	require.NotNil(t, cond)
	assert.Contains(t, cond.Message, name+"-0", "the message must name the pod it is about")
	assert.Contains(t, cond.Message, "spec.replicas is 1",
		"and the replica count the deferral was decided on, which is not a claim about how many pods exist")
}
