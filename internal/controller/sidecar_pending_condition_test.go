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
	r.setSidecarUpdatePendingCondition(ctx, v, true)
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
