package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/common"
)

// rwReportFixture builds a 3-replica cluster whose pods all exist; masterOrdinal
// names the pod carrying the master label, and a negative value leaves every pod
// without it.
func rwReportFixture(t *testing.T, name string, masterOrdinal int) (*ValkeyReconciler, *vkov1.Valkey) {
	t.Helper()

	v := newTestValkey(name, "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Image = "valkey/valkey:8.0"
	})
	sts := stsForValkey(v)
	pod0 := podFromStsTemplate(v, sts, 0)
	pod1 := podFromStsTemplate(v, sts, 1)
	pod2 := podFromStsTemplate(v, sts, 2)
	for i, p := range []*corev1.Pod{pod0, pod1, pod2} {
		if i == masterOrdinal {
			p.Labels[common.LabelInstanceRole] = common.RoleMaster
		}
	}
	r, _ := newTestReconciler(v, sts, pod0, pod1, pod2)
	return r, v
}

func TestReportRWServiceEndpoints_NoLabeledMasterOnASettledClusterIsReported(t *testing.T) {
	r, v := rwReportFixture(t, "rw-empty", -1)

	r.reportRWServiceEndpoints(context.Background(), v, 3)

	cond := apimeta.FindStatusCondition(v.Status.Conditions, vkov1.ConditionTypeRWServiceEmpty)
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionTrue, cond.Status)
	assert.Equal(t, vkov1.ReasonNoPodLabeledMaster, cond.Reason)
	assert.Contains(t, cond.Message, "rw-empty-rw")
	assert.Contains(t, cond.Message, "sidecar")
}

// The fleet must not gain the condition from an upgrade: a healthy cluster that
// never exhibited the state stays without the row (the T24(d) neutrality style).
func TestReportRWServiceEndpoints_AHealthyClusterNeverGainsTheCondition(t *testing.T) {
	r, v := rwReportFixture(t, "rw-healthy", 1)

	r.reportRWServiceEndpoints(context.Background(), v, 3)

	assert.Nil(t, apimeta.FindStatusCondition(v.Status.Conditions, vkov1.ConditionTypeRWServiceEmpty))
}

func TestReportRWServiceEndpoints_ARelabeledMasterClearsTheCondition(t *testing.T) {
	r, v := rwReportFixture(t, "rw-back", 1)
	apimeta.SetStatusCondition(&v.Status.Conditions, metav1.Condition{
		Type:   vkov1.ConditionTypeRWServiceEmpty,
		Status: metav1.ConditionTrue,
		Reason: vkov1.ReasonNoPodLabeledMaster,
	})

	r.reportRWServiceEndpoints(context.Background(), v, 3)

	cond := apimeta.FindStatusCondition(v.Status.Conditions, vkov1.ConditionTypeRWServiceEmpty)
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionFalse, cond.Status)
	assert.Equal(t, vkov1.ReasonMasterLabeled, cond.Reason)
}

// A rolling update in flight has legitimate label-less windows; nothing is
// judged there.
func TestReportRWServiceEndpoints_SilentWhileARollingUpdateIsInFlight(t *testing.T) {
	r, v := rwReportFixture(t, "rw-roll", -1)
	v.Annotations = map[string]string{annotationRollingUpdateState: stateVerifyingTopology}

	r.reportRWServiceEndpoints(context.Background(), v, 3)

	assert.Nil(t, apimeta.FindStatusCondition(v.Status.Conditions, vkov1.ConditionTypeRWServiceEmpty))
}

// A cluster that is not fully ready is not settled either.
func TestReportRWServiceEndpoints_SilentWhileReplicasAreMissing(t *testing.T) {
	r, v := rwReportFixture(t, "rw-partial", -1)

	r.reportRWServiceEndpoints(context.Background(), v, 2)

	assert.Nil(t, apimeta.FindStatusCondition(v.Status.Conditions, vkov1.ConditionTypeRWServiceEmpty))
}
