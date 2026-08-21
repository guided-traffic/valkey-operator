package controller

import (
	"context"
	"sync"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

// nudgeTracker records the first time a key was observed in a condition the
// operator is waiting out. Losing the map on operator restart is harmless
// everywhere it is used: the durable copy of every deadline lives on the API
// objects, and the worst case here is that one wait starts counting again.
//
// It backs two disjoint sets of keys:
//
//   - the nudge grace period, keyed by StatefulSet (this file);
//   - the rolling-update wait bounds, keyed by CR name plus a bound suffix
//     (waitBoundKey in rolling_update.go), which back the annotation-based
//     deadlines for the passes where the annotation write can fail.
//
// Only the grace period is implemented here; the rate limit between nudge bumps
// lives in the nudge annotation itself.
type nudgeTracker struct {
	mu    sync.Mutex
	first map[types.NamespacedName]time.Time
}

// observe returns the time key was first seen, recording now when this is the
// first observation.
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

// firstSeen returns the recorded first observation for key without creating one.
// Callers that only want to read a deadline must not arm it as a side effect.
func (t *nudgeTracker) firstSeen(key types.NamespacedName) (time.Time, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	seen, ok := t.first[key]
	return seen, ok
}

// forget drops the recorded observation for key.
func (t *nudgeTracker) forget(key types.NamespacedName) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.first, key)
}

// forgetNudges drops the grace-period observations of both StatefulSets belonging
// to the named CR, plus the rolling-update wait bounds recorded for it.
//
// It exists for the two Reconcile exits that never look at a StatefulSet again:
// the CR is gone, or it is being deleted. nudgeStatefulSet is the only other place
// that forgets, and it is unreachable from there — so without this the tracker
// keeps two entries per deleted CR until the operator restarts. The leak is
// bytes-sized, but it is unbounded in a namespace that churns CRs. The wait bounds
// share the tracker and both exits, so they are dropped here for the same reason;
// their own forget site is clearRollingUpdateState, equally unreachable once the
// CR is gone.
func (r *ValkeyReconciler) forgetNudges(namespace, name string) {
	v := &vkov1.Valkey{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}}
	for _, component := range []string{common.ComponentValkey, common.ComponentSentinel} {
		r.nudges.forget(types.NamespacedName{
			Name:      common.StatefulSetName(v, component),
			Namespace: namespace,
		})
	}
	r.forgetWaitBounds(namespace, name)
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
// There is no rolling-update suppression, for either StatefulSet. Every delete site requeues and
// then blocks on exactly that pod coming back, so a blocked recreation is precisely when the nudge
// is needed, not a case to stand back from. Three sites say so literally, with a "waiting for pod
// to be recreated" branch: replaceNextReplica, replaceRemainingPods and
// handleStandaloneRollingUpdate. deleteNextPendingPod has no such branch — it skips a pod that does
// not exist and falls through to a bare terminal requeue — and the Sentinel path skips a missing
// pod so that its quorum guard blocks the next delete. Same net effect, different shape; the
// argument rests on the requeue, not on the log line
// (docs/adr/0003-nudge-a-short-of-pods-statefulset.md, D8).
//
// Bumping the annotation of a StatefulSet whose pod the operator itself
// deleted is at worst a no-op:
//
//   - Under OnDelete with Parallel pod management, creating a missing ordinal is
//     unconditional. A nudge can only make a recreation the statefulset-controller
//     already owes happen sooner; it cannot cause one it does not owe, and the
//     ordinal name makes a duplicate impossible.
//   - The desired pod template is written before every delete in the same pass —
//     reconcileResources runs its StatefulSet step before reconcileWorkload, and
//     outdated pods are the rolling update's trigger *because* the template is
//     already new. So the recreated pod comes back with the spec the update wants.
//   - The annotation lives on StatefulSet object metadata, never on
//     spec.template, so it enters neither StatefulSetHasChanged/podTemplateChanged
//     nor ComputePodSpecHash: no drift verdict, no hash change, no feedback loop.
//   - What separates an intentional deletion from a stalled one is duration, not
//     the rolling-update state. nudgeGracePeriod requires 10 s of observed
//     shortness, which a healthy recreation never reaches — status.replicas counts
//     created pods, so it recovers at pod creation, long before readiness. The
//     rolling-update state annotation is a phase marker that persists for the whole
//     phase, including while a recreation is stuck, so keying suppression on it
//     suppressed the nudge for the entire duration of exactly the stall it exists
//     to break.
//
// The rolling update keeps requeue authority regardless: reconcileWorkload
// returns the rolling-update result before it reads this function's return value,
// so a nudge never shortens a rolling-update wait.
//
// It reports whether any managed StatefulSet is currently short of pods, so the
// caller can keep requeueing. Without that the grace period would be
// unreachable: the pass that first observes the short state only records it, and
// in the blocked case no event ever produces a second pass (see
// nudgeRequeueInterval).
func (r *ValkeyReconciler) nudgeShortStatefulSets(ctx context.Context, v *vkov1.Valkey) bool {
	dataKey := types.NamespacedName{Name: common.StatefulSetName(v, common.ComponentValkey), Namespace: v.Namespace}
	sentinelKey := types.NamespacedName{Name: common.StatefulSetName(v, common.ComponentSentinel), Namespace: v.Namespace}

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
// rate limit still requires the caller to come back. A read that failed for any
// reason other than NotFound reports short too: unknown is not the same as
// recovered, and only the caller coming back can resolve it.
func (r *ValkeyReconciler) nudgeStatefulSet(ctx context.Context, v *vkov1.Valkey, key types.NamespacedName) bool {
	logger := log.FromContext(ctx)

	sts := &appsv1.StatefulSet{}
	if err := r.Get(ctx, key, sts); err != nil {
		if apierrors.IsNotFound(err) {
			r.nudges.forget(key)
			return false
		}
		// Any other error says nothing about the StatefulSet, so it must not be
		// read as "no longer short". Forgetting here would restart the grace
		// period, and returning false would end the requeue chain — which in
		// Provisioning is the only wakeup source (see nudgeRequeueInterval), so
		// one transient read error would park the very stall this exists to
		// break until an unrelated event arrives. Keep the observation, come back.
		logger.Error(err, "Failed to read StatefulSet for the nudge check", "statefulset", key.Name)
		return true
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
