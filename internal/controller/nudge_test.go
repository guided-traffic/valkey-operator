package controller

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/builder"
	"github.com/guided-traffic/valkey-operator/internal/common"
)

// nudgeTestNamespace is the namespace all nudge unit tests operate in.
const nudgeTestNamespace = "default"

// nudgeTestReplicas is the desired replica count of every StatefulSet built here.
const nudgeTestReplicas int32 = 3

// newNudgeStatefulSet builds a StatefulSet that wants nudgeTestReplicas pods and
// reports `created` of them in status.replicas, as the statefulset-controller would.
func newNudgeStatefulSet(name string, created int32) *appsv1.StatefulSet {
	desired := nudgeTestReplicas
	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: nudgeTestNamespace},
		Spec:       appsv1.StatefulSetSpec{Replicas: &desired},
		Status:     appsv1.StatefulSetStatus{Replicas: created},
	}
}

// newSentinelValkey returns a 3-replica HA Valkey CR.
func newSentinelValkey() *vkov1.Valkey {
	return newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
	})
}

// pastGrace makes the reconciler believe the StatefulSet has been short of pods
// for longer than nudgeGracePeriod.
func pastGrace(r *ValkeyReconciler, keys ...types.NamespacedName) {
	for _, key := range keys {
		r.nudges.observe(key, time.Now().Add(-nudgeGracePeriod-time.Second))
	}
}

func nudgeAnnotation(t *testing.T, c client.Client, name string) string {
	t.Helper()
	sts := &appsv1.StatefulSet{}
	require.NoError(t, c.Get(context.Background(),
		types.NamespacedName{Name: name, Namespace: nudgeTestNamespace}, sts))
	return sts.Annotations[builder.AnnotationNudge]
}

// nudgeKey returns the tracker/client key for a StatefulSet name.
func nudgeKey(name string) types.NamespacedName {
	return types.NamespacedName{Name: name, Namespace: nudgeTestNamespace}
}

func TestNudgeShortStatefulSets_NoNudgeWhenHealthy(t *testing.T) {
	v := newSentinelValkey()
	sts := newNudgeStatefulSet(common.StatefulSetName(v, common.ComponentValkey), 3)
	r, c := newTestReconciler(v, sts)

	key := nudgeKey(sts.Name)
	pastGrace(r, key)

	r.nudgeShortStatefulSets(context.Background(), v)

	assert.Empty(t, nudgeAnnotation(t, c, sts.Name),
		"a StatefulSet with all pods created must not be nudged")
	r.nudges.mu.Lock()
	_, tracked := r.nudges.first[key]
	r.nudges.mu.Unlock()
	assert.False(t, tracked, "a healthy StatefulSet must be dropped from the tracker")
}

func TestNudgeShortStatefulSets_NoNudgeWithinGracePeriod(t *testing.T) {
	v := newSentinelValkey()
	sts := newNudgeStatefulSet(common.StatefulSetName(v, common.ComponentValkey), 0)
	r, c := newTestReconciler(v, sts)

	// No tracker seeding: this is the first observation of the short state.
	r.nudgeShortStatefulSets(context.Background(), v)

	assert.Empty(t, nudgeAnnotation(t, c, sts.Name),
		"normal pod churn must not be nudged before the grace period elapses")
}

func TestNudgeShortStatefulSets_NudgesAfterGracePeriod(t *testing.T) {
	v := newSentinelValkey()
	sts := newNudgeStatefulSet(common.StatefulSetName(v, common.ComponentValkey), 0)
	r, c := newTestReconciler(v, sts)

	pastGrace(r, nudgeKey(sts.Name))

	r.nudgeShortStatefulSets(context.Background(), v)

	stamp := nudgeAnnotation(t, c, sts.Name)
	require.NotEmpty(t, stamp, "a StatefulSet short of pods must be nudged")
	_, err := time.Parse(time.RFC3339, stamp)
	assert.NoError(t, err, "nudge annotation must carry an RFC3339 timestamp")
}

func TestNudgeShortStatefulSets_RateLimitHonored(t *testing.T) {
	v := newSentinelValkey()
	sts := newNudgeStatefulSet(common.StatefulSetName(v, common.ComponentValkey), 0)
	recent := time.Now().Add(-builder.NudgeInterval / 2).UTC().Format(time.RFC3339)
	sts.Annotations = map[string]string{builder.AnnotationNudge: recent}
	r, c := newTestReconciler(v, sts)

	pastGrace(r, nudgeKey(sts.Name))

	r.nudgeShortStatefulSets(context.Background(), v)

	assert.Equal(t, recent, nudgeAnnotation(t, c, sts.Name),
		"a nudge younger than NudgeInterval must not be re-bumped")
}

func TestNudgeShortStatefulSets_RebumpsStaleNudge(t *testing.T) {
	v := newSentinelValkey()
	sts := newNudgeStatefulSet(common.StatefulSetName(v, common.ComponentValkey), 0)
	stale := time.Now().Add(-builder.NudgeInterval - time.Minute).UTC().Format(time.RFC3339)
	sts.Annotations = map[string]string{builder.AnnotationNudge: stale}
	r, c := newTestReconciler(v, sts)

	pastGrace(r, nudgeKey(sts.Name))

	r.nudgeShortStatefulSets(context.Background(), v)

	assert.NotEqual(t, stale, nudgeAnnotation(t, c, sts.Name),
		"a nudge older than NudgeInterval must be re-bumped so the resync repeats")
}

func TestNudgeShortStatefulSets_NoNudgeDuringRollingUpdate(t *testing.T) {
	v := newSentinelValkey()
	v.Annotations = map[string]string{annotationRollingUpdateState: stateReplacingReplicas}
	sts := newNudgeStatefulSet(common.StatefulSetName(v, common.ComponentValkey), 2)
	r, c := newTestReconciler(v, sts)

	key := nudgeKey(sts.Name)
	pastGrace(r, key)

	r.nudgeShortStatefulSets(context.Background(), v)

	assert.Empty(t, nudgeAnnotation(t, c, sts.Name),
		"pods are deleted on purpose during a rolling update; nudging must be suppressed")
	r.nudges.mu.Lock()
	_, tracked := r.nudges.first[key]
	r.nudges.mu.Unlock()
	assert.False(t, tracked, "the rolling update must reset the grace period tracker")
}

func TestNudgeShortStatefulSets_NudgesSentinelStatefulSet(t *testing.T) {
	v := newSentinelValkey()
	dataName := common.StatefulSetName(v, common.ComponentValkey)
	sentinelName := common.StatefulSetName(v, common.ComponentSentinel)
	data := newNudgeStatefulSet(dataName, 3)
	sentinel := newNudgeStatefulSet(sentinelName, 1)
	r, c := newTestReconciler(v, data, sentinel)

	pastGrace(r,
		nudgeKey(dataName),
		nudgeKey(sentinelName),
	)

	r.nudgeShortStatefulSets(context.Background(), v)

	assert.Empty(t, nudgeAnnotation(t, c, dataName), "healthy data StatefulSet must be untouched")
	assert.NotEmpty(t, nudgeAnnotation(t, c, sentinelName),
		"the Sentinel StatefulSet has the same failure mode and must be nudged too")
}

func TestNudgeShortStatefulSets_SkipsSentinelWhenDisabled(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) { v.Spec.Replicas = 3 })
	sentinelName := common.StatefulSetName(v, common.ComponentSentinel)
	// A leftover Sentinel StatefulSet from a previous HA configuration.
	sentinel := newNudgeStatefulSet(sentinelName, 0)
	r, c := newTestReconciler(v, sentinel)

	pastGrace(r, nudgeKey(sentinelName))

	r.nudgeShortStatefulSets(context.Background(), v)

	assert.Empty(t, nudgeAnnotation(t, c, sentinelName),
		"Sentinel is disabled; its StatefulSet must not be nudged")
}

func TestNudgeShortStatefulSets_MissingStatefulSetIsNoOp(t *testing.T) {
	v := newSentinelValkey()
	r, _ := newTestReconciler(v)

	key := nudgeKey(common.StatefulSetName(v, common.ComponentValkey))
	pastGrace(r, key)

	// Must not panic or fail when the StatefulSet does not exist yet.
	r.nudgeShortStatefulSets(context.Background(), v)

	r.nudges.mu.Lock()
	_, tracked := r.nudges.first[key]
	r.nudges.mu.Unlock()
	assert.False(t, tracked, "a missing StatefulSet must be dropped from the tracker")
}

func TestNudgeShortStatefulSets_ReportsShortOfPods(t *testing.T) {
	v := newSentinelValkey()
	dataName := common.StatefulSetName(v, common.ComponentValkey)
	data := newNudgeStatefulSet(dataName, 0)
	sentinel := newNudgeStatefulSet(common.StatefulSetName(v, common.ComponentSentinel), 3)
	r, _ := newTestReconciler(v, data, sentinel)

	// Deliberately no pastGrace: the very first observation must already report the
	// short state, otherwise the caller stops requeueing and the grace period can
	// never elapse.
	assert.True(t, r.nudgeShortStatefulSets(context.Background(), v),
		"a StatefulSet short of pods must be reported even while inside the grace period")
}

func TestNudgeShortStatefulSets_ReportsHealthy(t *testing.T) {
	v := newSentinelValkey()
	data := newNudgeStatefulSet(common.StatefulSetName(v, common.ComponentValkey), 3)
	sentinel := newNudgeStatefulSet(common.StatefulSetName(v, common.ComponentSentinel), 3)
	r, _ := newTestReconciler(v, data, sentinel)

	assert.False(t, r.nudgeShortStatefulSets(context.Background(), v),
		"a cluster with every pod created must not keep the reconciler awake")
}

func TestNudgeShortStatefulSets_ReportsShortSentinelOnly(t *testing.T) {
	v := newSentinelValkey()
	data := newNudgeStatefulSet(common.StatefulSetName(v, common.ComponentValkey), 3)
	sentinel := newNudgeStatefulSet(common.StatefulSetName(v, common.ComponentSentinel), 1)
	r, _ := newTestReconciler(v, data, sentinel)

	assert.True(t, r.nudgeShortStatefulSets(context.Background(), v),
		"a short Sentinel StatefulSet must keep the reconciler awake even when the data pods are complete")
}

func TestNudgeShortStatefulSets_NoRequeueSignalDuringRollingUpdate(t *testing.T) {
	v := newSentinelValkey()
	v.Annotations = map[string]string{annotationRollingUpdateState: stateReplacingReplicas}
	data := newNudgeStatefulSet(common.StatefulSetName(v, common.ComponentValkey), 0)
	r, _ := newTestReconciler(v, data)

	assert.False(t, r.nudgeShortStatefulSets(context.Background(), v),
		"the data rolling update drives its own requeue; the data nudge must not add a second clock")
}

// TestNudgeShortStatefulSets_NudgesSentinelDuringDataRollingUpdate pins the scope of
// the rolling-update suppression: it belongs to the StatefulSet whose pods that
// update deletes, and to no other. A data rolling update never touches a Sentinel
// pod, so a short Sentinel StatefulSet during one is a genuine stall — and the
// quorum it costs is what the data rolling update itself waits on.
func TestNudgeShortStatefulSets_NudgesSentinelDuringDataRollingUpdate(t *testing.T) {
	v := newSentinelValkey()
	v.Annotations = map[string]string{annotationRollingUpdateState: stateReplacingReplicas}
	dataName := common.StatefulSetName(v, common.ComponentValkey)
	sentinelName := common.StatefulSetName(v, common.ComponentSentinel)
	data := newNudgeStatefulSet(dataName, 2)
	sentinel := newNudgeStatefulSet(sentinelName, 1)
	r, c := newTestReconciler(v, data, sentinel)

	pastGrace(r, nudgeKey(dataName), nudgeKey(sentinelName))

	assert.True(t, r.nudgeShortStatefulSets(context.Background(), v),
		"a short Sentinel StatefulSet must keep the reconciler awake during a data rolling update")
	assert.Empty(t, nudgeAnnotation(t, c, dataName),
		"data pods are deleted on purpose during a data rolling update; that nudge stays suppressed")
	assert.NotEmpty(t, nudgeAnnotation(t, c, sentinelName),
		"a data rolling update never deletes a Sentinel pod, so its short StatefulSet must still be nudged")
}

// TestReconcileWorkload_RequeuesWhileShortOfPods is the regression guard for the
// defect that made T1 fail: a StatefulSet whose pod creates are rejected leaves the
// CR in Provisioning, and Provisioning is not covered by the health-based requeue.
// Without a requeue of its own the operator goes dormant — no pods means no pod
// events, an unwritten StatefulSet means no informer event, and
// GenerationChangedPredicate swallows the status writes — so the nudge never fires.
func TestReconcileWorkload_RequeuesWhileShortOfPods(t *testing.T) {
	v := newTestValkey("test", nudgeTestNamespace)
	desired := int32(1)
	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: v.Name, Namespace: nudgeTestNamespace},
		Spec:       appsv1.StatefulSetSpec{Replicas: &desired},
		Status:     appsv1.StatefulSetStatus{Replicas: 0},
	}
	r, _ := newTestReconciler(v, sts)

	result, err := r.reconcileWorkload(context.Background(), v)
	require.NoError(t, err)

	require.Equal(t, vkov1.ValkeyPhaseProvisioning, v.Status.Phase,
		"precondition: zero created pods must leave the CR in Provisioning")
	assert.Positive(t, result.RequeueAfter,
		"a StatefulSet short of pods must be requeued, otherwise the nudge never gets a second pass")
}

func TestReconcileWorkload_NoRequeueWhenAllPodsCreated(t *testing.T) {
	v := newTestValkey("test", nudgeTestNamespace)
	desired := int32(1)
	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: v.Name, Namespace: nudgeTestNamespace},
		Spec:       appsv1.StatefulSetSpec{Replicas: &desired},
		Status:     appsv1.StatefulSetStatus{Replicas: 1, ReadyReplicas: 1},
	}
	r, _ := newTestReconciler(v, sts)

	result, err := r.reconcileWorkload(context.Background(), v)
	require.NoError(t, err)

	if v.Status.Phase == vkov1.ValkeyPhaseOK {
		assert.Zero(t, result.RequeueAfter,
			"a healthy cluster must not be polled; its events are enough")
	}
}

func TestNudgeTracker_ObserveKeepsFirstObservation(t *testing.T) {
	tracker := &nudgeTracker{}
	key := nudgeKey("test")
	first := time.Now().Add(-time.Minute)

	assert.Equal(t, first, tracker.observe(key, first))
	assert.Equal(t, first, tracker.observe(key, time.Now()),
		"a later observation must not reset the grace period")

	tracker.forget(key)
	now := time.Now()
	assert.Equal(t, now, tracker.observe(key, now), "forget must restart the grace period")
}

// --- NA4: the nudge must survive a Sentinel rolling update parked on quorum ---

// sentinelQuorumWaitFixture builds the constellation NA4 describes: no data
// rolling update is in progress, and the Sentinel rolling update is parked on its
// quorum guard because sentinel-2 was deleted and its replacement has not been
// created (a rejected pod create). The two surviving Sentinel pods are ready and
// still outdated, so readyCount-1 = 1 < quorum = 2 and checkAndHandleSentinelRollingUpdate
// returns NeedsRequeue on every pass — the early return that used to make the
// nudge unreachable.
//
// dataCreated is what the data StatefulSet reports in status.replicas; the
// Sentinel StatefulSet reports 2 of 3, matching the pods that exist.
func sentinelQuorumWaitFixture(dataCreated int32) (*vkov1.Valkey, []client.Object) {
	const outdatedImage = "valkey/valkey:8.0"

	v := newSentinelValkey()
	data := newNudgeStatefulSet(common.StatefulSetName(v, common.ComponentValkey), dataCreated)
	sentinel := buildTestSentinelSts(v)
	sentinel.Status.Replicas = 2

	return v, []client.Object{
		v, data, sentinel,
		createSentinelPod(v, 0, outdatedImage, true),
		createSentinelPod(v, 1, outdatedImage, true),
	}
}

// TestReconcileWorkload_NudgesDataStatefulSetDuringSentinelQuorumWait is the
// regression guard for NA4. The Sentinel quorum guard requeues forever while the
// deleted Sentinel pod cannot be recreated; with the nudge placed after the
// rolling-update checks, the data StatefulSet was never nudged in that state and
// the statefulset-controller backoff (measured at 5 min 29 s) was back.
func TestReconcileWorkload_NudgesDataStatefulSetDuringSentinelQuorumWait(t *testing.T) {
	v, objs := sentinelQuorumWaitFixture(0)
	r, c := newTestReconciler(objs...)
	dataName := common.StatefulSetName(v, common.ComponentValkey)

	pastGrace(r, nudgeKey(dataName))

	result, err := r.reconcileWorkload(context.Background(), v)
	require.NoError(t, err)
	require.Equal(t, rollingUpdateRequeueDelay, result.RequeueAfter,
		"precondition: the pass must end in the Sentinel quorum wait, not further down")

	assert.NotEmpty(t, nudgeAnnotation(t, c, dataName),
		"a Sentinel rolling update waiting on quorum must not starve the data StatefulSet of nudges")
}

// TestReconcileWorkload_NudgesSentinelStatefulSetWhileRecreationBlocked covers the
// second half of NA4: the Sentinel StatefulSet whose own pod recreation is blocked
// must be nudged as well. Suppressing it during a Sentinel rolling update would be
// self-defeating — the quorum guard is waiting for exactly that pod, and the nudge
// annotation lives on the StatefulSet metadata, so it cannot disturb the update.
func TestReconcileWorkload_NudgesSentinelStatefulSetWhileRecreationBlocked(t *testing.T) {
	v, objs := sentinelQuorumWaitFixture(3)
	r, c := newTestReconciler(objs...)
	sentinelName := common.StatefulSetName(v, common.ComponentSentinel)

	pastGrace(r, nudgeKey(sentinelName))

	result, err := r.reconcileWorkload(context.Background(), v)
	require.NoError(t, err)
	require.Equal(t, rollingUpdateRequeueDelay, result.RequeueAfter,
		"precondition: the pass must end in the Sentinel quorum wait, not further down")

	assert.NotEmpty(t, nudgeAnnotation(t, c, sentinelName),
		"a Sentinel pod whose recreation is blocked must be nudged, not waited out")
}
