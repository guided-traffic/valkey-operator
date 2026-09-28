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

// The tests in this file pin the detached-pod hold (ADR 0020 D9, ADR 0026 D11): a
// pod at a generated data ordinal that the StatefulSet did not control holds the
// roll instead of failing the workload pass, so the recovery checks and the status
// write still run and the tier's standing reports are not retracted on that silence.
// The collision itself is reported to ReconcileBlocked by the resource step
// (reportPodNameCollision), pinned in pod_name_collision_report_test.go.

// The entry scan proves every ordinal before it acts on an outdated one: an owned
// outdated pod at a lower ordinal must not dispatch the roll while a collision stands
// at a higher one. So the hold wins over the roll regardless of ordering.
//
// Mutation check: restoring the `break` on the first outdated pod lets the roll
// dispatch and this holds assertion fails (the outdated pod-0 is replaced instead).
func TestDispatchDataRollingUpdate_HoldsAtAnUnprovenOrdinalBeyondAnOutdatedOne(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 2
		v.Spec.Image = "valkey/valkey:9.0"
	})
	sts := stsForValkey(v)
	// pod-0 is ours and outdated; pod-1 is a stray. Without the full ownership sweep
	// the loop would break at pod-0 and roll it.
	outdated := createPodForSts(v, 0, "valkey/valkey:8.0", true)
	ownedByTestSts(v, common.ComponentValkey, outdated)
	r, c := newTestReconciler(v, sts, outdated, foreignPod(v, "test-1"))

	result := r.checkAndHandleRollingUpdate(context.Background(), crGet(t, c, "test"))

	require.NoError(t, result.Error)
	assert.True(t, result.heldByPodCollision, "a collision at a higher ordinal holds the roll")
	assert.True(t, podExists(t, c, "test-0"), "the outdated owned pod is not rolled while a collision stands")
	assert.True(t, podExists(t, c, "test-1"), "the foreign pod is untouched")
}

// A collision hold keeps a standing PodSecurityUpdatePending=True: the pass returned
// at the unproven ordinal before the deferral was decided, so retracting the report
// would flap it (ADR 0026 D11, ADR 0032 D3).
func TestCheckAndHandleRollingUpdate_CollisionHoldKeepsAStandingPodSecurityUpdatePending(t *testing.T) {
	v := newTestValkey("psp", "default", func(v *vkov1.Valkey) { v.Spec.Replicas = 2 },
		func(v *vkov1.Valkey) {
			meta.SetStatusCondition(&v.Status.Conditions, metav1.Condition{
				Type:    vkov1.ConditionTypePodSecurityUpdatePending,
				Status:  metav1.ConditionTrue,
				Reason:  vkov1.ReasonPodRunsAsRoot,
				Message: "seeded",
			})
		})
	sts := stsForValkey(v)
	r, c := newTestReconciler(v, sts, foreignPod(v, "psp-0"), podFromStsTemplate(v, sts, 1))

	result := r.checkAndHandleRollingUpdate(context.Background(), crGet(t, c, "psp"))
	require.NoError(t, result.Error)
	assert.True(t, result.heldByPodCollision)

	cond := meta.FindStatusCondition(crGet(t, c, "psp").Status.Conditions, vkov1.ConditionTypePodSecurityUpdatePending)
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionTrue, cond.Status)
	assert.Equal(t, "seeded", cond.Message, "the report is not retracted on the hold's silence")
}

// A full pass over a Sentinel cluster with a data-ordinal collision continues to the
// status write and holds the Sentinel roll: the data collision blocks the pass
// (phase Error, ReconcileBlocked=True/ForeignObject via the resource step), but the
// workload half no longer ends on an error, so the status subresource is still
// written and the Sentinel pods are never deleted while the data tier holds.
func TestReconcile_DataCollisionHoldContinuesThePassAndHoldsSentinel(t *testing.T) {
	v := newTestValkey("ha", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
		v.Spec.Image = "valkey/valkey:9.0"
	})
	dataSts := stsForValkey(v)
	sentinelSts := buildTestSentinelSts(v)
	// Sentinel pods on an outdated image: without the data hold, the Sentinel roll
	// would delete one. The data collision must hold it.
	s0 := createSentinelPod(v, 0, "valkey/valkey:8.0", true)
	s1 := createSentinelPod(v, 1, "valkey/valkey:8.0", true)
	s2 := createSentinelPod(v, 2, "valkey/valkey:8.0", true)
	stray := foreignPod(v, "ha-0")
	r, c := newTestReconciler(v, dataSts, sentinelSts, stray,
		podFromStsTemplate(v, dataSts, 1), podFromStsTemplate(v, dataSts, 2), s0, s1, s2)

	_ = reconcileFor(t, r, v)

	// The collision reached the CR through the resource step.
	blocked := meta.FindStatusCondition(crGet(t, c, "ha").Status.Conditions, vkov1.ConditionTypeReconcileBlocked)
	require.NotNil(t, blocked)
	assert.Equal(t, metav1.ConditionTrue, blocked.Status)
	assert.Equal(t, vkov1.ReasonForeignObject, blocked.Reason)

	// Every Sentinel pod survives: the data hold held the Sentinel roll.
	for _, name := range []string{"ha-sentinel-0", "ha-sentinel-1", "ha-sentinel-2"} {
		assert.True(t, podExists(t, c, name), "%s must not be rolled while the data tier holds", name)
	}
	// The foreign data pod is untouched.
	require.NoError(t, c.Get(context.Background(),
		types.NamespacedName{Name: "ha-0", Namespace: "default"}, &corev1.Pod{}))
}
