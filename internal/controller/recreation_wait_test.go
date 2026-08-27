package controller

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
)

func recreationStalledCondition(t *testing.T, r *ValkeyReconciler, v *vkov1.Valkey) *metav1.Condition {
	t.Helper()
	current := &vkov1.Valkey{}
	require.NoError(t, r.Get(context.Background(),
		types.NamespacedName{Name: v.Name, Namespace: v.Namespace}, current))
	return apimeta.FindStatusCondition(current.Status.Conditions, vkov1.ConditionTypePodRecreationStalled)
}

// Within the budget the wait keeps its old shape: the pass ends on a plain
// requeue and nothing is reported -- the StatefulSet controller recreates a
// deleted pod within seconds and the ordinary roll never sees more than that.
func TestRecreationWait_PlainRequeueWithinTheBudget(t *testing.T) {
	v := newTestValkey("recwait", "default")
	r, _ := newTestReconciler(v)

	result := r.recreationWait(context.Background(), v, "recwait-1")

	require.NotNil(t, result)
	assert.True(t, result.NeedsRequeue)
	assert.Zero(t, result.DeferredRequeueAfter)
	assert.Nil(t, recreationStalledCondition(t, r, v), "no report within the budget")
	assert.NotEmpty(t, v.Annotations[annotationRecreationWaitStarted], "the bound must be armed")
}

// Past the overrun the pass stops ending on the wait: DeferredRequeueAfter lets
// the status write, the steady-state split-brain check and the Sentinel roll run
// again, and the condition says whose controller is being waited for (T10).
func TestRecreationWait_PastTheOverrunHoldsAndReports(t *testing.T) {
	v := newTestValkey("recwait-old", "default")
	v.Annotations = map[string]string{
		annotationRecreationWaitStarted: time.Now().
			Add(-(podRecreationOverrun + time.Minute)).UTC().Format(time.RFC3339),
	}
	r, _ := newTestReconciler(v)

	result := r.recreationWait(context.Background(), v, "recwait-old-0")

	require.NotNil(t, result)
	assert.False(t, result.NeedsRequeue, "the pass must not end on the wait any more")
	assert.NotZero(t, result.DeferredRequeueAfter)

	cond := recreationStalledCondition(t, r, v)
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionTrue, cond.Status)
	assert.Equal(t, vkov1.ReasonPodNotRecreated, cond.Reason)
	assert.Contains(t, cond.Message, "recwait-old-0")
	assert.Contains(t, cond.Message, "FailedCreate")
}

// The pod exists again: the episode ends, the condition is retracted, and the
// bound is reset in both halves so the next episode gets its own budget -- one
// roll waits for several pods in sequence.
func TestClearRecreationWait_EndsTheEpisodeAndResetsTheBudget(t *testing.T) {
	v := newTestValkey("recwait-clear", "default")
	v.Annotations = map[string]string{
		annotationRecreationWaitStarted: time.Now().
			Add(-(podRecreationOverrun + time.Minute)).UTC().Format(time.RFC3339),
	}
	r, c := newTestReconciler(v)

	// The stalled episode reports first.
	result := r.recreationWait(context.Background(), v, "recwait-clear-2")
	require.NotNil(t, result)
	require.NotZero(t, result.DeferredRequeueAfter)

	r.clearRecreationWait(context.Background(), v)

	cond := recreationStalledCondition(t, r, v)
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionFalse, cond.Status)
	assert.Equal(t, vkov1.ReasonPodRecreated, cond.Reason)

	stored := &vkov1.Valkey{}
	require.NoError(t, c.Get(context.Background(),
		types.NamespacedName{Name: v.Name, Namespace: v.Namespace}, stored))
	assert.NotContains(t, stored.Annotations, annotationRecreationWaitStarted,
		"a surviving timestamp would pre-expire the next episode")

	// The next episode starts with a fresh budget: plain requeue, no report.
	next := r.recreationWait(context.Background(), v, "recwait-clear-1")
	require.NotNil(t, next)
	assert.True(t, next.NeedsRequeue)
	assert.Zero(t, next.DeferredRequeueAfter)
}

// A cluster that never stalled never gains the condition from the clear path.
func TestClearRecreationWait_NeverStampsTheFleet(t *testing.T) {
	v := newTestValkey("recwait-clean", "default")
	r, _ := newTestReconciler(v)

	r.clearRecreationWait(context.Background(), v)

	assert.Nil(t, recreationStalledCondition(t, r, v))
}
