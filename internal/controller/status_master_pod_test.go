package controller

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/controller-runtime/pkg/client"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/builder"
	"github.com/guided-traffic/valkey-operator/internal/common"
)

// ADR 0002 D11. status.masterPod was pod-0 unconditionally on the non-Sentinel path, which
// contradicts two end states the operator produces on purpose: an abandoned topology
// restoration leaves the promoted replica as master (TopologyRestored=False records
// it), and every drain adoption leaves a non-pod-0 master behind. The field lied
// exactly where the condition beside it was telling the truth, and it has a second
// consumer -- the e2e suite picks "replicas" as "every pod except status.masterPod".

// statusFixture creates a three-pod non-Sentinel CR and its StatefulSet, marks the
// StatefulSet fully ready and hands back the reconciler plus the CR as the API server
// holds it. Three replicas because the pod-0 assumption only becomes wrong above one.
func statusFixture(t *testing.T) (*ValkeyReconciler, client.Client, *vkov1.Valkey) {
	t.Helper()
	v := newTestValkey("test", testNamespace, func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
	})
	r, c := newTestReconciler(v)
	require.NoError(t, reconcileFor(t, r, v))
	markStatefulSetReady(t, c, v)
	return r, c, crGet(t, c, "test")
}

// [REGRESSION] The abandoned-restoration end state: the promoted replica is master,
// the operator recorded it, and the sidecar labels it. The -rw Service routes writes
// to that label, so the status has to name the same pod.
func TestUpdateStatus_ReportsTheLabeledMasterInsteadOfPodZero(t *testing.T) {
	r, c, v := statusFixture(t)
	require.NoError(t, c.Create(context.Background(), masterLabeledPod(v, 0, common.RoleReplica)))
	require.NoError(t, c.Create(context.Background(), masterLabeledPod(v, 1, common.RoleReplica)))
	require.NoError(t, c.Create(context.Background(), masterLabeledPod(v, 2, common.RoleMaster)))
	v.Annotations = map[string]string{builder.AnnotationKnownMaster: podHost("test", 2)}
	require.NoError(t, c.Update(context.Background(), v))

	require.NoError(t, r.updateStatus(context.Background(), crGet(t, c, "test")))

	assert.Equal(t, "test-2", crGet(t, c, v.Name).Status.MasterPod,
		"the -rw Service selects on the instanceRole label, so the status must report the same pod")
}

// The label is the first authority, but it is the sidecar's to maintain and it is
// briefly wrong or missing on every restart. With no single labeled master the
// operator's own record answers -- it is what the replica ConfigMap is built from.
func TestUpdateStatus_FallsBackToTheKnownMasterRecord(t *testing.T) {
	r, c, v := statusFixture(t)
	// No pod carries the master label: the sidecar has not repatched yet.
	require.NoError(t, c.Create(context.Background(), masterLabeledPod(v, 0, common.RoleReplica)))
	require.NoError(t, c.Create(context.Background(), masterLabeledPod(v, 1, common.RoleReplica)))
	v.Annotations = map[string]string{builder.AnnotationKnownMaster: podHost("test", 1)}
	require.NoError(t, c.Update(context.Background(), v))

	require.NoError(t, r.updateStatus(context.Background(), crGet(t, c, "test")))

	assert.Equal(t, "test-1", crGet(t, c, v.Name).Status.MasterPod)
}

// Two labeled masters is a split brain, which checkSteadyStateSplitBrain owns. The
// status must not pick a winner there: it reports the record, which is the only pod
// the operator itself vouches for.
func TestUpdateStatus_DoesNotPickAWinnerBetweenTwoLabeledMasters(t *testing.T) {
	r, c, v := statusFixture(t)
	require.NoError(t, c.Create(context.Background(),
		createdAt(masterLabeledPod(v, 0, common.RoleMaster), time.Hour)))
	require.NoError(t, c.Create(context.Background(),
		createdAt(masterLabeledPod(v, 2, common.RoleMaster), time.Hour)))
	v.Annotations = map[string]string{builder.AnnotationKnownMaster: podHost("test", 2)}
	require.NoError(t, c.Update(context.Background(), v))

	require.NoError(t, r.updateStatus(context.Background(), crGet(t, c, "test")))

	assert.Equal(t, "test-2", crGet(t, c, v.Name).Status.MasterPod)
}

// The default that used to be the only answer, and it is still the right one: a
// cluster that has never failed over has neither a record nor -- in this fixture --
// a labeled pod, and pod-0 is where the init script puts the master.
func TestUpdateStatus_KeepsPodZeroWhenNothingElseAnswers(t *testing.T) {
	r, c, v := statusFixture(t)

	require.NoError(t, r.updateStatus(context.Background(), v))

	assert.Equal(t, "test-0", crGet(t, c, v.Name).Status.MasterPod)
}

// A single-pod cluster answers without reading anything: its only pod is pod-0, so
// the List the multi-replica path pays for would buy nothing.
func TestCurrentMasterPod_SinglePodNeedsNoLookup(t *testing.T) {
	v := newTestValkey("test", testNamespace)
	r, _ := newTestReconciler(v)

	assert.Equal(t, "test-0", r.currentMasterPod(context.Background(), v))
}
