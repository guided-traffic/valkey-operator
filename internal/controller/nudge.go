package controller

import (
	"context"
	"sync"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/builder"
	"github.com/guided-traffic/valkey-operator/internal/common"
)

// nudgeGracePeriod is how long a StatefulSet must be observed short of pods
// before the operator writes the first nudge annotation. It covers normal pod
// churn (a pod deleted and immediately recreated) without delaying recovery from
// a real stall noticeably: the reconciler requeues unhealthy instances every
// 10 s, so the grace period costs at most one requeue cycle.
const nudgeGracePeriod = 10 * time.Second

// nudgeRequeueInterval is how soon Reconcile is re-entered while a StatefulSet is
// short of pods.
//
// The nudge needs a clock of its own. The phase-based requeue at the end of
// reconcileWorkload only fires for Error and Syncing, but a StatefulSet whose pod
// creates are rejected leaves the CR in Provisioning. In that state nothing else
// wakes the operator: the CR watch uses GenerationChangedPredicate so status
// writes do not re-trigger, the StatefulSet is not written (no spec drift), and
// with zero pods there are no pod events — the exact dormancy the nudge exists to
// break. It is shorter than the grace period so the first bump lands one requeue
// after the short state is first observed.
const nudgeRequeueInterval = 5 * time.Second

// nudgeTracker records when a StatefulSet was first observed short of pods.
// It only implements the grace period; the rate limit between bumps lives in the
// nudge annotation itself. Losing the map on operator restart is harmless — the
// worst case is one extra annotation patch, which is the desired action anyway.
type nudgeTracker struct {
	mu    sync.Mutex
	first map[types.NamespacedName]time.Time
}

// observe returns the time key was first seen short of pods, recording now when
// this is the first observation.
func (t *nudgeTracker) observe(key types.NamespacedName, now time.Time) time.Time {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.first == nil {
		t.first = make(map[types.NamespacedName]time.Time)
	}
	if seen, ok := t.first[key]; ok {
		return seen
	}
	t.first[key] = now
	return now
}

// forget drops the recorded observation for key.
func (t *nudgeTracker) forget(key types.NamespacedName) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.first, key)
}

// nudgeShortStatefulSets patches a nudge annotation onto the data and Sentinel
// StatefulSets while they report fewer pods than requested.
//
// Both StatefulSets use updateStrategy OnDelete and podManagementPolicy Parallel,
// so pod creation is entirely the statefulset-controller's job. When its pod
// creates are rejected (e.g. by a fail-closed admission webhook whose backend is
// temporarily gone), it retries on an exponential workqueue backoff that reached
// 5 min 29 s in the incident this guards against — long after the rejection cause
// was resolved. Nothing else wakes it: the object is not written (no drift), and
// with zero pods there are no pod events either. Bumping an annotation is the
// operator's only lever, so it uses it.
//
// The nudge is suppressed while a rolling update is in progress, where the
// operator deletes pods on purpose and the short-of-pods state is expected.
//
// It reports whether any managed StatefulSet is currently short of pods, so the
// caller can keep requeueing. Without that the grace period would be
// unreachable: the pass that first observes the short state only records it, and
// in the blocked case no event ever produces a second pass (see
// nudgeRequeueInterval).
func (r *ValkeyReconciler) nudgeShortStatefulSets(ctx context.Context, v *vkov1.Valkey) bool {
	dataKey := types.NamespacedName{Name: common.StatefulSetName(v, common.ComponentValkey), Namespace: v.Namespace}
	sentinelKey := types.NamespacedName{Name: common.StatefulSetName(v, common.ComponentSentinel), Namespace: v.Namespace}

	if r.getRollingUpdateState(v) != "" {
		r.nudges.forget(dataKey)
		r.nudges.forget(sentinelKey)
		return false
	}

	short := r.nudgeStatefulSet(ctx, v, dataKey)
	if v.IsSentinelEnabled() && r.nudgeStatefulSet(ctx, v, sentinelKey) {
		short = true
	}
	return short
}

// nudgeStatefulSet bumps the nudge annotation on a single StatefulSet when it has
// been short of pods for longer than nudgeGracePeriod and the last bump is older
// than builder.NudgeInterval. Failures are logged and swallowed: a nudge is a
// recovery accelerator, never a reason to fail the reconcile.
//
// It reports whether the StatefulSet is short of pods — which is true on every
// path that did not bump as well, because waiting out the grace period or the
// rate limit still requires the caller to come back.
func (r *ValkeyReconciler) nudgeStatefulSet(ctx context.Context, v *vkov1.Valkey, key types.NamespacedName) bool {
	logger := log.FromContext(ctx)

	sts := &appsv1.StatefulSet{}
	if err := r.Get(ctx, key, sts); err != nil {
		r.nudges.forget(key)
		return false
	}

	desired := int32(1)
	if sts.Spec.Replicas != nil {
		desired = *sts.Spec.Replicas
	}

	// status.replicas counts created pods, not ready ones: a pod that exists but
	// is not ready is the kubelet's business and no nudge would help there.
	if sts.Status.Replicas >= desired {
		r.nudges.forget(key)
		return false
	}

	now := time.Now()
	if now.Sub(r.nudges.observe(key, now)) < nudgeGracePeriod {
		return true
	}
	if !builder.NudgeDue(sts, now) {
		return true
	}

	if err := r.Patch(ctx, sts, client.RawPatch(types.MergePatchType, builder.NudgePatch(now))); err != nil {
		logger.Error(err, "Failed to nudge StatefulSet", "statefulset", key.Name)
		return true
	}

	logger.Info("Nudged StatefulSet short of pods to force an immediate resync",
		"statefulset", key.Name, "current", sts.Status.Replicas, "desired", desired)
	r.recordEvent(v, corev1.EventTypeNormal, "StatefulSetNudged",
		"StatefulSet %s has %d/%d pods; bumped the nudge annotation to force an immediate resync",
		key.Name, sts.Status.Replicas, desired)
	return true
}
