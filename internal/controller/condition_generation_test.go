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
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
)

// Tests in this file cover ADR 0002 D9: conditions written
// through setStatusCondition (ReconcileBlocked, SidecarUpdatePending,
// RollingUpdatePaused) carried no ObservedGeneration, so anything judging
// condition staleness by observedGeneration — kstatus and the tooling modelled
// on it — read them as generation 0 and therefore as never matching the live
// spec. The Ready condition always set it; these did not.

// conditionOf reads a named condition from the stored CR.
func conditionOf(t *testing.T, c client.Client, v *vkov1.Valkey, condType string) *metav1.Condition {
	t.Helper()
	return meta.FindStatusCondition(crGet(t, c, v.Name).Status.Conditions, condType)
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

	r.setSidecarUpdatePendingCondition(ctx, v, v.Name+"-0")
	cond := conditionOf(t, c, v, vkov1.ConditionTypeSidecarUpdatePending)
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionTrue, cond.Status)
	assert.Equal(t, int64(4), cond.ObservedGeneration)

	// The sidecar image lands with the next spec edit: the cleared condition must
	// name the generation that cleared it, not the one that first reported drift.
	bumpGeneration(t, c, v, 5)
	r.setSidecarUpdatePendingCondition(ctx, v, "")
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

// --- ADR 0002 D8: no status write when the condition would not change ---

// statusWriteCounter counts status subresource writes of a Valkey CR.
type statusWriteCounter struct{ writes int }

func (s *statusWriteCounter) intercept() interceptor.Funcs {
	return interceptor.Funcs{
		SubResourceUpdate: func(ctx context.Context, c client.Client, subResource string,
			obj client.Object, opts ...client.SubResourceUpdateOption) error {
			if _, ok := obj.(*vkov1.Valkey); ok && subResource == "status" {
				s.writes++
			}
			return c.SubResource(subResource).Update(ctx, obj, opts...)
		},
	}
}

// TestSetStatusCondition_SkipsWriteWhenNothingChanged pins ADR 0002 D8: the helper
// used to issue a status update on every call, so any caller reporting a steady
// state cost one write per reconcile pass.
func TestSetStatusCondition_SkipsWriteWhenNothingChanged(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) { v.Generation = 1 })
	counter := &statusWriteCounter{}
	r, _ := newInterceptedReconciler(counter.intercept(), v)
	ctx := context.Background()

	for i := 0; i < 4; i++ {
		r.setStatusCondition(ctx, v, vkov1.ConditionTypeSidecarUpdatePending,
			metav1.ConditionFalse, "SidecarUpToDate", "All sidecar containers are running the desired image")
	}

	assert.Equal(t, 1, counter.writes,
		"an unchanged condition must not cost a status write per reconcile pass")
}

// TestSetStatusCondition_WritesOnObservedGenerationBump guards the skip against
// the one change meta.SetStatusCondition could plausibly have ignored: an
// otherwise identical condition on a new generation. It counts as a change, so
// the bump must still be persisted.
func TestSetStatusCondition_WritesOnObservedGenerationBump(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) { v.Generation = 1 })
	counter := &statusWriteCounter{}
	r, c := newInterceptedReconciler(counter.intercept(), v)
	ctx := context.Background()

	r.setStatusCondition(ctx, v, vkov1.ConditionTypeSidecarUpdatePending,
		metav1.ConditionFalse, "SidecarUpToDate", "All sidecar containers are running the desired image")
	require.Equal(t, 1, counter.writes)

	bumpGeneration(t, c, v, 2)
	r.setStatusCondition(ctx, v, vkov1.ConditionTypeSidecarUpdatePending,
		metav1.ConditionFalse, "SidecarUpToDate", "All sidecar containers are running the desired image")

	assert.Equal(t, 2, counter.writes, "a new generation must be persisted even on an identical condition")
	cond := conditionOf(t, c, v, vkov1.ConditionTypeSidecarUpdatePending)
	require.NotNil(t, cond)
	assert.Equal(t, int64(2), cond.ObservedGeneration)
}

// TestSetStatusCondition_WritesOnReasonOrMessageChange pins the other half of
// what the skip must not swallow: reason and message changes without a status
// flip. meta.SetStatusCondition counts both as changes.
func TestSetStatusCondition_WritesOnReasonOrMessageChange(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) { v.Generation = 1 })
	counter := &statusWriteCounter{}
	r, c := newInterceptedReconciler(counter.intercept(), v)
	ctx := context.Background()

	r.setStatusCondition(ctx, v, vkov1.ConditionTypeReconcileBlocked,
		metav1.ConditionTrue, vkov1.ReasonAdmissionWebhookDenied, "mutate.kyverno.svc-fail")
	require.Equal(t, 1, counter.writes)

	// Same status, different message: another webhook now rejects the write.
	r.setStatusCondition(ctx, v, vkov1.ConditionTypeReconcileBlocked,
		metav1.ConditionTrue, vkov1.ReasonAdmissionWebhookDenied, "validate.kyverno.svc-fail")
	assert.Equal(t, 2, counter.writes)

	// Same status and message, different reason.
	r.setStatusCondition(ctx, v, vkov1.ConditionTypeReconcileBlocked,
		metav1.ConditionTrue, vkov1.ReasonWriteFailed, "validate.kyverno.svc-fail")
	assert.Equal(t, 3, counter.writes)

	cond := conditionOf(t, c, v, vkov1.ConditionTypeReconcileBlocked)
	require.NotNil(t, cond)
	assert.Equal(t, vkov1.ReasonWriteFailed, cond.Reason)
	assert.Equal(t, "validate.kyverno.svc-fail", cond.Message)
}

// TestSetSidecarUpdatePendingCondition_NoWritePerPass covers the live ADR 0002 D8 call
// site: handleStandaloneRollingUpdate calls this on every pass of every
// standalone cluster, drift or no drift.
func TestSetSidecarUpdatePendingCondition_NoWritePerPass(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) { v.Generation = 1 })
	counter := &statusWriteCounter{}
	r, c := newInterceptedReconciler(counter.intercept(), v)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		r.setSidecarUpdatePendingCondition(ctx, v, "")
	}
	assert.Equal(t, 1, counter.writes,
		"a standalone cluster without sidecar drift must not write its status on every pass")

	// A real transition must still be reported.
	r.setSidecarUpdatePendingCondition(ctx, v, v.Name+"-0")
	assert.Equal(t, 2, counter.writes)
	cond := conditionOf(t, c, v, vkov1.ConditionTypeSidecarUpdatePending)
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionTrue, cond.Status)
}
