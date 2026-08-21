package controller

import (
	"time"

	"golang.org/x/time/rate"
	"k8s.io/client-go/util/workqueue"
	ctrlcontroller "sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	// reconcileRetryBaseDelay is the delay before the first retry of a failed
	// reconcile. Kept at controller-runtime's default so short-lived glitches are
	// still retried almost immediately.
	reconcileRetryBaseDelay = 5 * time.Millisecond

	// reconcileRetryMaxDelay caps the per-item exponential retry backoff.
	//
	// controller-runtime defaults this to 1000 s. The exponent grows by one per
	// failed pass and the delay between passes is the backoff itself, so the wait
	// before the next look grows to roughly the length of the outage: about 82 s
	// after 1.4 min of continuous failure, about 5.5 min after 5.5 min, and the
	// 1000 s ceiling after some 22 min.
	//
	// That is the wrong shape for this operator. A failing pass here is usually a
	// cluster-side admission gate rejecting a managed write, and the operator is
	// the only thing that notices when the gate opens again — nothing else wakes
	// it, because the CR watch filters status-only writes and the rejected object
	// never changes. Everything that hangs off a pass inherits the delay: the
	// ReconcileBlocked condition keeps naming a webhook that is long healthy, the
	// phase stays Error, and the StatefulSet nudge (which only runs inside a pass)
	// slows down with it.
	//
	// 30 s keeps a genuine backoff — 5 ms doubling reaches the cap only after 13
	// consecutive failures, about 41 s of continuous rejection — while bounding
	// how long a CR can report something that is no longer true.
	reconcileRetryMaxDelay = 30 * time.Second

	// reconcileRetryQPS and reconcileRetryBurst are the overall (not per-item) token
	// bucket of client-go's DefaultTypedControllerRateLimiter, added back so that many
	// CRs failing at once still cannot saturate the API client.
	reconcileRetryQPS   = 10
	reconcileRetryBurst = 100
)

// DefaultMaxConcurrentReconciles is how many Valkey CRs the operator reconciles at
// the same time when the flag is not set.
//
// controller-runtime defaults this to 1, and one worker couples every CR in the
// fleet to the slowest of them: a reconcile pass dials the pods of its cluster with
// a 5 s timeout each (internal/valkeyclient), so a cluster whose pods stopped
// answering — a node drain in progress, a NetworkPolicy mistake, a hung Valkey —
// holds the single worker for tens of seconds while every other Valkey CR waits.
//
// 4 is a bar, not a guarantee: with four clusters stuck at once the coupling is
// back. The per-CR serialisation everything in this package relies on is unaffected,
// because the work queue never runs two passes for the same key
// (docs/adr/0019-reconcile-concurrency-and-the-cost-of-a-stuck-pass.md).
const DefaultMaxConcurrentReconciles = 4

// newReconcileRateLimiter builds the work queue rate limiter for the Valkey
// controller: client-go's classic DefaultTypedControllerRateLimiter shape — the
// maximum of a per-item exponential backoff and an overall token bucket — with the
// exponential part capped at reconcileRetryMaxDelay instead of 1000 s.
//
// The bucket is added, not kept: controller-runtime v0.24.1 defaults to the bare
// per-item exponential limiter whenever the priority queue is on, and it is on
// unless UsePriorityQueue is set to false, which SetupWithManager does not do
// (docs/adr/0001-continue-reconciling-past-a-rejected-write.md, D6).
func newReconcileRateLimiter() workqueue.TypedRateLimiter[reconcile.Request] {
	return workqueue.NewTypedMaxOfRateLimiter(
		workqueue.NewTypedItemExponentialFailureRateLimiter[reconcile.Request](
			reconcileRetryBaseDelay, reconcileRetryMaxDelay),
		&workqueue.TypedBucketRateLimiter[reconcile.Request]{
			Limiter: rate.NewLimiter(rate.Limit(reconcileRetryQPS), reconcileRetryBurst),
		},
	)
}

// reconcileControllerOptions returns the controller options used by
// SetupWithManager. It exists so the wiring itself is unit-testable.
//
// maxConcurrent of zero or less means "not configured" and falls back to
// DefaultMaxConcurrentReconciles, so a reconciler built without the field — every
// test helper, and the integration suite — still gets the fleet decoupling rather
// than controller-runtime's single worker.
func reconcileControllerOptions(maxConcurrent int) ctrlcontroller.Options {
	if maxConcurrent <= 0 {
		maxConcurrent = DefaultMaxConcurrentReconciles
	}
	return ctrlcontroller.Options{
		RateLimiter:             newReconcileRateLimiter(),
		MaxConcurrentReconciles: maxConcurrent,
	}
}
