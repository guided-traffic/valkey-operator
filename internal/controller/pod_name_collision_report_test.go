package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/common"
)

// The tests in this file pin the report-only resource step that carries a pod-name
// collision the rolling update refuses to the ReconcileBlocked condition, so the
// critical ValkeyReconcileBlocked alert can see it
// (docs/adr/0002-surface-a-blocked-reconcile-on-the-cr.md, D13; ADR 0020 D9). The
// refusal itself is unchanged and pinned by the tests in foreign_object_test.go;
// what is new is that it reaches the condition through the one evaluator.
//
// Mutation check: removing the step from resourceReconcileSteps() (or making
// reportPodNameCollision return nil) leaves ReconcileBlocked absent and these tests
// red on the condition assertion.

// TestReconcile_ReportsADataPodNameCollisionThroughReconcileBlocked drives a full
// pass with a foreign pod at an in-range data ordinal: the pass is blocked, the
// phase is Error, and ReconcileBlocked is True/ForeignObject naming the pod. Then
// the foreign pod is deleted and the next pass clears the condition.
func TestReconcile_ReportsADataPodNameCollisionThroughReconcileBlocked(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) { v.Spec.Replicas = 2 })
	sts := stsForValkey(v)
	stray := foreignPod(v, "test-0")
	r, c := newTestReconciler(v, sts, stray, podFromStsTemplate(v, sts, 1))

	require.Error(t, reconcileFor(t, r, v), "the pass fails while a data pod name is held by a foreign pod")

	got := crGet(t, c, "test")
	assert.Equal(t, vkov1.ValkeyPhaseError, got.Status.Phase)
	cond := meta.FindStatusCondition(got.Status.Conditions, vkov1.ConditionTypeReconcileBlocked)
	require.NotNil(t, cond, "the collision must reach ReconcileBlocked")
	assert.Equal(t, metav1.ConditionTrue, cond.Status)
	assert.Equal(t, vkov1.ReasonForeignObject, cond.Reason)
	assert.Contains(t, cond.Message, "test-0", "the condition names the colliding pod")

	require.NoError(t, c.Get(context.Background(),
		types.NamespacedName{Name: "test-0", Namespace: "default"}, &corev1.Pod{}),
		"the foreign pod is never deleted")

	// The collision is removed: the next pass measures a clean tier and clears the
	// condition through the same single evaluator.
	require.NoError(t, c.Delete(context.Background(), stray))
	_ = reconcileFor(t, r, v)

	cleared := meta.FindStatusCondition(crGet(t, c, "test").Status.Conditions, vkov1.ConditionTypeReconcileBlocked)
	require.NotNil(t, cleared)
	assert.Equal(t, metav1.ConditionFalse, cleared.Status, "a clean pass clears the collision report")
}

// TestReconcile_ReportsASentinelPodNameCollisionThroughReconcileBlocked is the same
// for a foreign pod at a Sentinel ordinal: the step walks the Sentinel tier too.
func TestReconcile_ReportsASentinelPodNameCollisionThroughReconcileBlocked(t *testing.T) {
	v := newTestValkey("ha", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
	})
	dataSts := stsForValkey(v)
	sentinelSts := buildTestSentinelSts(v)
	sentinelName := common.StatefulSetName(v, common.ComponentSentinel) + "-0"
	stray := foreignPod(v, sentinelName)
	r, c := newTestReconciler(v, dataSts, sentinelSts, stray)

	require.Error(t, reconcileFor(t, r, v))

	cond := meta.FindStatusCondition(crGet(t, c, "ha").Status.Conditions, vkov1.ConditionTypeReconcileBlocked)
	require.NotNil(t, cond, "a Sentinel-ordinal collision must reach ReconcileBlocked too")
	assert.Equal(t, metav1.ConditionTrue, cond.Status)
	assert.Equal(t, vkov1.ReasonForeignObject, cond.Reason)
	assert.Contains(t, cond.Message, sentinelName)

	require.NoError(t, c.Get(context.Background(),
		types.NamespacedName{Name: sentinelName, Namespace: "default"}, &corev1.Pod{}))
}

// reportPodNameCollision does not report a Sentinel collision on a cluster without
// Sentinel: the tier does not apply, so its ordinals are not walked.
func TestReportPodNameCollision_SkipsTheSentinelTierWhenDisabled(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) { v.Spec.Replicas = 1 })
	sts := stsForValkey(v)
	// A foreign pod at the Sentinel ordinal of a non-Sentinel cluster is not this
	// step's concern; only the applicable tiers are walked.
	stray := foreignPod(v, common.StatefulSetName(v, common.ComponentSentinel)+"-0")
	r, _ := newTestReconciler(v, sts, podFromStsTemplate(v, sts, 0), stray)

	assert.NoError(t, r.reportPodNameCollision(context.Background(), v))
}

// A foreign StatefulSet is treated as absent: its own step is the one reporter for
// it, so the collision report walks no ordinal under it.
func TestReportPodNameCollision_TreatsAForeignStatefulSetAsAbsent(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) { v.Spec.Replicas = 1 })
	sts := stsForValkey(v)
	sts.OwnerReferences = nil
	r, _ := newTestReconciler(v, sts, foreignPod(v, "test-0"))

	assert.NoError(t, r.reportPodNameCollision(context.Background(), v))
}
