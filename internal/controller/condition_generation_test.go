package controller

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
)

// Tests in this file cover NA17 of the admission-gap ticket: conditions written
// through setStatusCondition (ReconcileBlocked, SidecarUpdatePending,
// RollingUpdatePaused) carried no ObservedGeneration, so anything judging
// condition staleness by observedGeneration — kstatus and the tooling modelled
// on it — read them as generation 0 and therefore as never matching the live
// spec. The Ready condition always set it; these did not.

// conditionOf reads a named condition from the stored CR.
func conditionOf(t *testing.T, c client.Client, v *vkov1.Valkey, condType string) *metav1.Condition {
	t.Helper()
	stored := &vkov1.Valkey{}
	require.NoError(t, c.Get(context.Background(),
		types.NamespacedName{Name: v.Name, Namespace: v.Namespace}, stored))
	return meta.FindStatusCondition(stored.Status.Conditions, condType)
}

// bumpGeneration simulates a spec edit: the stored CR moves to a new generation
// and the caller's in-memory copy is refreshed from it, which is what the
// reconciler sees at the start of the next pass.
func bumpGeneration(t *testing.T, c client.Client, v *vkov1.Valkey, generation int64) {
	t.Helper()
	ctx := context.Background()
	stored := &vkov1.Valkey{}
	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: v.Name, Namespace: v.Namespace}, stored))
	stored.Generation = generation
	require.NoError(t, c.Update(ctx, stored))
	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: v.Name, Namespace: v.Namespace}, v))
	require.Equal(t, generation, v.Generation)
}

func TestSetStatusCondition_CarriesObservedGeneration(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) { v.Generation = 7 })
	r, c := newTestReconciler(v)

	r.setStatusCondition(context.Background(), v,
		vkov1.ConditionTypeRollingUpdatePaused, metav1.ConditionTrue, "SyncTimeout", "replica never synced")

	cond := conditionOf(t, c, v, vkov1.ConditionTypeRollingUpdatePaused)
	require.NotNil(t, cond)
	assert.Equal(t, int64(7), cond.ObservedGeneration,
		"a condition must name the spec generation it was computed against")
}

// TestSetStatusCondition_UsesRefreshedGeneration pins that the generation comes
// from the refresh Get, not from the caller's copy. A pass that started before a
// spec edit holds a stale object; the condition it writes describes the state the
// refresh just read, so it must be stamped with the refreshed generation.
func TestSetStatusCondition_UsesRefreshedGeneration(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) { v.Generation = 2 })
	r, c := newTestReconciler(v)

	stale := v.DeepCopy()
	bumpGeneration(t, c, v, 9)

	r.setStatusCondition(context.Background(), stale,
		vkov1.ConditionTypeRollingUpdatePaused, metav1.ConditionTrue, "SyncTimeout", "replica never synced")

	cond := conditionOf(t, c, v, vkov1.ConditionTypeRollingUpdatePaused)
	require.NotNil(t, cond)
	assert.Equal(t, int64(9), cond.ObservedGeneration)
}

func TestSetSidecarUpdatePendingCondition_CarriesObservedGeneration(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) { v.Generation = 4 })
	r, c := newTestReconciler(v)
	ctx := context.Background()

	r.setSidecarUpdatePendingCondition(ctx, v, true)
	cond := conditionOf(t, c, v, vkov1.ConditionTypeSidecarUpdatePending)
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionTrue, cond.Status)
	assert.Equal(t, int64(4), cond.ObservedGeneration)

	// The sidecar image lands with the next spec edit: the cleared condition must
	// name the generation that cleared it, not the one that first reported drift.
	bumpGeneration(t, c, v, 5)
	r.setSidecarUpdatePendingCondition(ctx, v, false)
	cond = conditionOf(t, c, v, vkov1.ConditionTypeSidecarUpdatePending)
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionFalse, cond.Status)
	assert.Equal(t, int64(5), cond.ObservedGeneration)
}

func TestSetReconcileBlockedCondition_CarriesObservedGeneration(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) { v.Generation = 3 })
	r, c := newTestReconciler(v)

	r.setReconcileBlockedCondition(context.Background(), v, webhookUnreachableError())

	cond := blockedCondition(t, c, v)
	require.NotNil(t, cond)
	assert.Equal(t, int64(3), cond.ObservedGeneration)
}

// TestSetReconcileBlockedCondition_RefreshesObservedGenerationOnNewSpec is the
// case the skip guard used to swallow: a cluster that stays blocked across a
// spec edit reports the same reason and message for the new generation. Skipping
// that write would leave the condition naming the old generation, which reads as
// "the new spec was never evaluated" — the opposite of what happened.
func TestSetReconcileBlockedCondition_RefreshesObservedGenerationOnNewSpec(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) { v.Generation = 1 })
	r, c := newTestReconciler(v)
	ctx := context.Background()

	r.setReconcileBlockedCondition(ctx, v, webhookUnreachableError())
	require.Equal(t, int64(1), blockedCondition(t, c, v).ObservedGeneration)

	bumpGeneration(t, c, v, 2)
	r.setReconcileBlockedCondition(ctx, v, webhookUnreachableError())

	cond := blockedCondition(t, c, v)
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionTrue, cond.Status)
	assert.Equal(t, int64(2), cond.ObservedGeneration,
		"a still-blocked new generation must be reported as blocked, not as unevaluated")
}

func TestSetReconcileBlockedCondition_ClearedRefreshesObservedGenerationOnNewSpec(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) { v.Generation = 1 })
	r, c := newTestReconciler(v)
	ctx := context.Background()

	r.setReconcileBlockedCondition(ctx, v, webhookUnreachableError())
	r.setReconcileBlockedCondition(ctx, v, nil)
	require.Equal(t, int64(1), blockedCondition(t, c, v).ObservedGeneration)

	bumpGeneration(t, c, v, 2)
	r.setReconcileBlockedCondition(ctx, v, nil)

	cond := blockedCondition(t, c, v)
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionFalse, cond.Status)
	assert.Equal(t, int64(2), cond.ObservedGeneration,
		"a new generation that reconciles cleanly must be reported as unblocked")
}

// TestSetReconcileBlockedCondition_NoWriteWhenGenerationUnchanged keeps the
// per-pass write suppression intact: the generation only moves on a spec edit, so
// the added ObservedGeneration comparison must not turn a steady state into a
// status write per reconcile.
func TestSetReconcileBlockedCondition_NoWriteWhenGenerationUnchanged(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) { v.Generation = 6 })
	r, c := newTestReconciler(v)
	ctx := context.Background()

	r.setReconcileBlockedCondition(ctx, v, webhookUnreachableError())
	blocked := resourceVersionOf(t, c, v)

	for i := 0; i < 3; i++ {
		r.setReconcileBlockedCondition(ctx, v, webhookUnreachableError())
	}
	assert.Equal(t, blocked, resourceVersionOf(t, c, v),
		"a cluster blocked on an unchanged spec must not rewrite its condition")

	r.setReconcileBlockedCondition(ctx, v, nil)
	cleared := resourceVersionOf(t, c, v)
	for i := 0; i < 3; i++ {
		r.setReconcileBlockedCondition(ctx, v, nil)
	}
	assert.Equal(t, cleared, resourceVersionOf(t, c, v),
		"a healthy cluster on an unchanged spec must not rewrite its condition")
}

// TestSetReconcileBlockedCondition_MessageChangeStillWrites guards that the
// generation comparison was added to the guard, not substituted for the message
// comparison: a different rejecting webhook on the same generation must still be
// reported.
func TestSetReconcileBlockedCondition_MessageChangeStillWrites(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) { v.Generation = 1 })
	r, _ := newTestReconciler(v)
	ctx := context.Background()

	r.setReconcileBlockedCondition(ctx, v, webhookUnreachableError())
	r.setReconcileBlockedCondition(ctx, v,
		fmt.Errorf("statefulset: %w", webhookDeniedError()))

	cond := meta.FindStatusCondition(v.Status.Conditions, vkov1.ConditionTypeReconcileBlocked)
	require.NotNil(t, cond)
	assert.Contains(t, cond.Message, "validate.kyverno.svc-fail")
}
