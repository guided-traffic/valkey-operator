package controller

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// rateLimiterRequest returns a distinct work queue item.
func rateLimiterRequest(name string) reconcile.Request {
	return reconcile.Request{NamespacedName: types.NamespacedName{Name: name, Namespace: "default"}}
}

// worstDelayOver drains n retries of one item and returns the largest delay seen.
// n stays well below reconcileRetryBurst so the overall token bucket contributes
// nothing and the measurement is purely the per-item exponential backoff.
func worstDelayOver(t *testing.T, n int) time.Duration {
	t.Helper()
	require.Less(t, n, reconcileRetryBurst, "the token bucket must not interfere with this measurement")

	limiter := newReconcileRateLimiter()
	req := rateLimiterRequest("test")

	var worst time.Duration
	for i := 0; i < n; i++ {
		if d := limiter.When(req); d > worst {
			worst = d
		}
	}
	return worst
}

// maxTolerableRetryDelay is the policy ceiling for the retry backoff, asserted
// independently of reconcileRetryMaxDelay.
//
// Checking the measured delay only against reconcileRetryMaxDelay would be
// circular: raising the constant back to controller-runtime's 1000 s default
// would keep such a test green while reintroducing exactly the defect. The
// number that matters is how long a CR may keep reporting a ReconcileBlocked
// condition whose cause is already gone, and that must stay in seconds.
const maxTolerableRetryDelay = 60 * time.Second

// TestReconcileRateLimiter_CapsBackoff is the regression guard for the unbounded
// retry delay. With controller-runtime's default cap of 1000 s the wait before the
// next pass grows to roughly the length of the outage, so a CR keeps reporting a
// ReconcileBlocked condition long after the rejecting webhook is healthy again.
func TestReconcileRateLimiter_CapsBackoff(t *testing.T) {
	assert.LessOrEqual(t, reconcileRetryMaxDelay, maxTolerableRetryDelay,
		"the cap only helps while it is short enough to bound how long a CR reports a stale condition")

	// 20 consecutive failures: 5 ms doubling passes any cap in that range.
	worst := worstDelayOver(t, 20)

	assert.LessOrEqual(t, worst, maxTolerableRetryDelay,
		"the retry backoff must never exceed the policy ceiling")
	assert.Equal(t, reconcileRetryMaxDelay, worst,
		"the cap must actually be reached, otherwise this test proves nothing")
}

func TestReconcileRateLimiter_RetriesFirstFailureImmediately(t *testing.T) {
	limiter := newReconcileRateLimiter()

	assert.Equal(t, reconcileRetryBaseDelay, limiter.When(rateLimiterRequest("test")),
		"a single transient failure must be retried almost immediately, not backed off")
}

func TestReconcileRateLimiter_BacksOffProgressively(t *testing.T) {
	limiter := newReconcileRateLimiter()
	req := rateLimiterRequest("test")

	first := limiter.When(req)
	second := limiter.When(req)

	assert.Greater(t, second, first,
		"repeated failures must back off; a flat retry would hammer a failing API server")
}

func TestReconcileRateLimiter_ForgetResetsBackoff(t *testing.T) {
	limiter := newReconcileRateLimiter()
	req := rateLimiterRequest("test")

	for i := 0; i < 20; i++ {
		limiter.When(req)
	}
	limiter.Forget(req)

	assert.Equal(t, reconcileRetryBaseDelay, limiter.When(req),
		"a CR that recovered must not inherit the penalty of its previous failures")
}

func TestReconcileRateLimiter_IsPerItem(t *testing.T) {
	limiter := newReconcileRateLimiter()
	blocked := rateLimiterRequest("blocked")

	for i := 0; i < 20; i++ {
		limiter.When(blocked)
	}

	assert.Equal(t, reconcileRetryBaseDelay, limiter.When(rateLimiterRequest("healthy")),
		"one persistently failing Valkey must not slow down the retries of another")
}

// TestReconcileControllerOptions_UsesCappedRateLimiter guards the wiring: the
// capped limiter is worthless if SetupWithManager does not actually pass it.
func TestReconcileControllerOptions_UsesCappedRateLimiter(t *testing.T) {
	limiter := reconcileControllerOptions(DefaultMaxConcurrentReconciles).RateLimiter
	require.NotNil(t, limiter, "the controller must be configured with an explicit rate limiter")

	req := rateLimiterRequest("test")
	var worst time.Duration
	for i := 0; i < 20; i++ {
		if d := limiter.When(req); d > worst {
			worst = d
		}
	}

	assert.LessOrEqual(t, worst, maxTolerableRetryDelay,
		"SetupWithManager must use the capped limiter, not controller-runtime's 1000 s default")
	assert.Equal(t, reconcileRetryMaxDelay, worst,
		"the options must carry the same limiter newReconcileRateLimiter builds")
}

// TestReconcileControllerOptions_ConcurrencyIsWired guards the fleet decoupling of
// ADR 0019. controller-runtime defaults MaxConcurrentReconciles to 1, and with one
// worker a cluster whose pods stopped answering holds the queue for replicas x the
// 5 s client timeout while every other Valkey CR waits.
func TestReconcileControllerOptions_ConcurrencyIsWired(t *testing.T) {
	assert.Greater(t, DefaultMaxConcurrentReconciles, 1,
		"a single worker is the defect this default exists to close")

	assert.Equal(t, 7, reconcileControllerOptions(7).MaxConcurrentReconciles,
		"an explicitly configured worker count must reach the controller")
}

// TestReconcileControllerOptions_UnsetConcurrencyFallsBack covers the reconcilers
// built without the field — the integration suite and every test helper. Leaving
// them at controller-runtime's implicit 1 would mean the decoupling is untested
// exactly where it is observable.
func TestReconcileControllerOptions_UnsetConcurrencyFallsBack(t *testing.T) {
	for _, unset := range []int{0, -1} {
		assert.Equal(t, DefaultMaxConcurrentReconciles,
			reconcileControllerOptions(unset).MaxConcurrentReconciles,
			"an unconfigured worker count must fall back to the default, not to one worker")
	}
}
