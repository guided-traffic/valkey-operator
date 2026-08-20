package controller

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/builder"
)

// haWithPDB is a three-replica Sentinel cluster with PodDisruptionBudgets enabled.
func haWithPDB(v *vkov1.Valkey) {
	v.Spec.Replicas = 3
	v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
	v.Spec.PodDisruptionBudget = &vkov1.PodDisruptionBudgetSpec{Enabled: true}
}

func getPDB(ctx context.Context, r *ValkeyReconciler, name string) (*policyv1.PodDisruptionBudget, error) {
	pdb := &policyv1.PodDisruptionBudget{}
	err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, pdb)
	return pdb, err
}

func TestReconcilePodDisruptionBudgets_CreatesBoth(t *testing.T) {
	v := newTestValkey("test", "default", haWithPDB)
	r, _ := newTestReconciler(v)
	ctx := context.Background()

	require.NoError(t, r.reconcilePodDisruptionBudgets(ctx, v))

	data, err := getPDB(ctx, r, "test")
	require.NoError(t, err, "data PDB must be created")
	require.NotNil(t, data.Spec.MaxUnavailable)
	assert.Equal(t, intstr.FromInt32(1), *data.Spec.MaxUnavailable)

	sentinel, err := getPDB(ctx, r, "test-sentinel")
	require.NoError(t, err, "sentinel PDB must be created")
	require.NotNil(t, sentinel.Spec.MinAvailable)
	assert.Equal(t, intstr.FromInt32(2), *sentinel.Spec.MinAvailable, "quorum of 3 sentinels is 2")

	require.Len(t, data.OwnerReferences, 1)
	assert.Equal(t, v.Name, data.OwnerReferences[0].Name)
	require.Len(t, sentinel.OwnerReferences, 1)
	assert.Equal(t, v.Name, sentinel.OwnerReferences[0].Name)
}

func TestReconcilePodDisruptionBudgets_NoneWhenOmitted(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
	})
	r, _ := newTestReconciler(v)
	ctx := context.Background()

	require.NoError(t, r.reconcilePodDisruptionBudgets(ctx, v))

	_, err := getPDB(ctx, r, "test")
	assert.True(t, apierrors.IsNotFound(err), "no data PDB without spec.podDisruptionBudget")
	_, err = getPDB(ctx, r, "test-sentinel")
	assert.True(t, apierrors.IsNotFound(err), "no sentinel PDB without spec.podDisruptionBudget")
}

// TestReconcilePodDisruptionBudgets_SkipsSingleReplica guards the decision that a
// one-pod StatefulSet gets no PDB: maxUnavailable=1 would be useless and
// minAvailable=1 would block node drains forever.
func TestReconcilePodDisruptionBudgets_SkipsSingleReplica(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 1
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 1}
		v.Spec.PodDisruptionBudget = &vkov1.PodDisruptionBudgetSpec{Enabled: true}
	})
	r, _ := newTestReconciler(v)
	ctx := context.Background()

	require.NoError(t, r.reconcilePodDisruptionBudgets(ctx, v))

	_, err := getPDB(ctx, r, "test")
	assert.True(t, apierrors.IsNotFound(err), "no data PDB for a single replica")
	_, err = getPDB(ctx, r, "test-sentinel")
	assert.True(t, apierrors.IsNotFound(err), "no sentinel PDB for a single sentinel")
}

// TestReconcilePodDisruptionBudgets_SkipsSentinelWhenDisabled verifies the data PDB
// is created on a multi-replica cluster without Sentinel, and no sentinel PDB is.
func TestReconcilePodDisruptionBudgets_SkipsSentinelWhenDisabled(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.PodDisruptionBudget = &vkov1.PodDisruptionBudgetSpec{Enabled: true}
	})
	r, _ := newTestReconciler(v)
	ctx := context.Background()

	require.NoError(t, r.reconcilePodDisruptionBudgets(ctx, v))

	_, err := getPDB(ctx, r, "test")
	require.NoError(t, err, "data PDB must exist without Sentinel")
	_, err = getPDB(ctx, r, "test-sentinel")
	assert.True(t, apierrors.IsNotFound(err), "no sentinel PDB when Sentinel is disabled")
}

func TestReconcilePodDisruptionBudgets_CleanupWhenDisabled(t *testing.T) {
	v := newTestValkey("test", "default", haWithPDB)
	r, _ := newTestReconciler(v)
	ctx := context.Background()

	require.NoError(t, r.reconcilePodDisruptionBudgets(ctx, v))
	_, err := getPDB(ctx, r, "test")
	require.NoError(t, err)

	v.Spec.PodDisruptionBudget.Enabled = false
	require.NoError(t, r.reconcilePodDisruptionBudgets(ctx, v))

	_, err = getPDB(ctx, r, "test")
	assert.True(t, apierrors.IsNotFound(err), "data PDB must be deleted when disabled")
	_, err = getPDB(ctx, r, "test-sentinel")
	assert.True(t, apierrors.IsNotFound(err), "sentinel PDB must be deleted when disabled")
}

// TestReconcilePodDisruptionBudgets_CleanupWhenScaledDown covers the scale-to-one
// path: the budget that protected three pods must not survive as a drain blocker.
func TestReconcilePodDisruptionBudgets_CleanupWhenScaledDown(t *testing.T) {
	v := newTestValkey("test", "default", haWithPDB)
	r, _ := newTestReconciler(v)
	ctx := context.Background()

	require.NoError(t, r.reconcilePodDisruptionBudgets(ctx, v))
	_, err := getPDB(ctx, r, "test")
	require.NoError(t, err)

	v.Spec.Replicas = 1
	v.Spec.Sentinel.Replicas = 1
	require.NoError(t, r.reconcilePodDisruptionBudgets(ctx, v))

	_, err = getPDB(ctx, r, "test")
	assert.True(t, apierrors.IsNotFound(err), "data PDB must be deleted when scaled below the minimum")
	_, err = getPDB(ctx, r, "test-sentinel")
	assert.True(t, apierrors.IsNotFound(err), "sentinel PDB must be deleted when scaled below the minimum")
}

func TestReconcilePodDisruptionBudgets_UpdatesMaxUnavailable(t *testing.T) {
	v := newTestValkey("test", "default", haWithPDB)
	r, _ := newTestReconciler(v)
	ctx := context.Background()

	require.NoError(t, r.reconcilePodDisruptionBudgets(ctx, v))

	updated := int32(2)
	v.Spec.PodDisruptionBudget.MaxUnavailable = &updated
	require.NoError(t, r.reconcilePodDisruptionBudgets(ctx, v))

	data, err := getPDB(ctx, r, "test")
	require.NoError(t, err)
	require.NotNil(t, data.Spec.MaxUnavailable)
	assert.Equal(t, intstr.FromInt32(2), *data.Spec.MaxUnavailable)
}

// TestReconcilePodDisruptionBudgets_SentinelQuorumFollowsReplicas verifies the
// sentinel budget tracks the replica count instead of staying at its first value.
func TestReconcilePodDisruptionBudgets_SentinelQuorumFollowsReplicas(t *testing.T) {
	v := newTestValkey("test", "default", haWithPDB)
	r, _ := newTestReconciler(v)
	ctx := context.Background()

	require.NoError(t, r.reconcilePodDisruptionBudgets(ctx, v))

	v.Spec.Sentinel.Replicas = 5
	require.NoError(t, r.reconcilePodDisruptionBudgets(ctx, v))

	sentinel, err := getPDB(ctx, r, "test-sentinel")
	require.NoError(t, err)
	require.NotNil(t, sentinel.Spec.MinAvailable)
	assert.Equal(t, intstr.FromInt32(3), *sentinel.Spec.MinAvailable, "quorum of 5 sentinels is 3")
}

func TestReconcilePodDisruptionBudgets_SetsOperatorVersionAnnotation(t *testing.T) {
	const version = "1.2.3"
	v := newTestValkey("test", "default", haWithPDB)
	r, _ := newTestReconcilerWithVersion(version, v)
	ctx := context.Background()

	require.NoError(t, r.reconcilePodDisruptionBudgets(ctx, v))

	data, err := getPDB(ctx, r, "test")
	require.NoError(t, err)
	assert.Equal(t, version, data.Annotations[builder.AnnotationOperatorVersion])
}

// TestReconcile_CreatesPodDisruptionBudgets verifies the PDB step is wired into the
// full reconcile pass, not only reachable through the helper.
func TestReconcile_CreatesPodDisruptionBudgets(t *testing.T) {
	v := newTestValkey("test", "default", haWithPDB)
	r, _ := newTestReconciler(v)
	ctx := context.Background()

	reconcileOnce(t, r, "test", "default")

	_, err := getPDB(ctx, r, "test")
	require.NoError(t, err, "full reconcile must create the data PDB")
	_, err = getPDB(ctx, r, "test-sentinel")
	require.NoError(t, err, "full reconcile must create the sentinel PDB")
}

// --- NA6: the too-permissive-budget warning must not be gated on a PDB write ---

// recordedEvent is one Event the reconciler handed to its recorder.
type recordedEvent struct {
	eventType string
	reason    string
	note      string
}

// fakeEventRecorder collects Events instead of sending them to the API server.
// It implements k8s.io/client-go/tools/events.EventRecorder.
type fakeEventRecorder struct {
	events []recordedEvent
}

func (f *fakeEventRecorder) Eventf(_ runtime.Object, _ runtime.Object,
	eventType, reason, _, note string, args ...interface{}) {
	f.events = append(f.events, recordedEvent{
		eventType: eventType,
		reason:    reason,
		note:      fmt.Sprintf(note, args...),
	})
}

// withReason returns the collected Events carrying the given reason.
func (f *fakeEventRecorder) withReason(reason string) []recordedEvent {
	var matching []recordedEvent
	for _, e := range f.events {
		if e.reason == reason {
			matching = append(matching, e)
		}
	}
	return matching
}

func (f *fakeEventRecorder) reset() { f.events = nil }

// pdbWriteCounter counts the create and update calls a pass issues against
// PodDisruptionBudgets, so a warning can be asserted to be independent of them.
type pdbWriteCounter struct {
	writes int
}

func (p *pdbWriteCounter) intercept() interceptor.Funcs {
	return interceptor.Funcs{
		Create: func(ctx context.Context, c client.WithWatch, obj client.Object,
			opts ...client.CreateOption) error {
			if _, ok := obj.(*policyv1.PodDisruptionBudget); ok {
				p.writes++
			}
			return c.Create(ctx, obj, opts...)
		},
		Update: func(ctx context.Context, c client.WithWatch, obj client.Object,
			opts ...client.UpdateOption) error {
			if _, ok := obj.(*policyv1.PodDisruptionBudget); ok {
				p.writes++
			}
			return c.Update(ctx, obj, opts...)
		},
	}
}

func (p *pdbWriteCounter) reset() { p.writes = 0 }

// TestReconcileDataPodDisruptionBudget_WarnsAfterScaleDownWithoutWrite is NA6:
// scaling spec.replicas down into maxUnavailable >= replicas leaves the PDB object
// unchanged, so a warning gated on the write never fired for the very change that
// removed the protection.
func TestReconcileDataPodDisruptionBudget_WarnsAfterScaleDownWithoutWrite(t *testing.T) {
	maxUnavailable := int32(2)
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 5
		v.Spec.PodDisruptionBudget = &vkov1.PodDisruptionBudgetSpec{
			Enabled: true, MaxUnavailable: &maxUnavailable,
		}
	})
	writes := &pdbWriteCounter{}
	r, _ := newInterceptedReconciler(writes.intercept(), v)
	rec := &fakeEventRecorder{}
	r.Recorder = rec
	ctx := context.Background()

	require.NoError(t, r.reconcileDataPodDisruptionBudget(ctx, v))
	require.Equal(t, 1, writes.writes, "precondition: the first pass creates the PDB")
	require.Empty(t, rec.withReason(reasonPodDisruptionBudgetTooPermissive),
		"maxUnavailable 2 with 5 replicas still protects the cluster")

	// Scale 5 -> 2: maxUnavailable now equals replicas, while the PDB object keeps
	// the maxUnavailable and the selector it already has.
	v.Spec.Replicas = 2
	writes.reset()
	rec.reset()
	require.NoError(t, r.reconcileDataPodDisruptionBudget(ctx, v))

	assert.Zero(t, writes.writes, "the scale-down does not touch the PDB object")
	warnings := rec.withReason(reasonPodDisruptionBudgetTooPermissive)
	require.Len(t, warnings, 1, "the pass that removed the protection must warn")
	assert.Equal(t, corev1.EventTypeWarning, warnings[0].eventType)
	assert.Contains(t, warnings[0].note, "maxUnavailable 2 is not smaller than replicas 2")
	assert.Contains(t, warnings[0].note, builder.PodDisruptionBudgetName(v))
}

// TestReconcileDataPodDisruptionBudget_WarnsOnEveryApplicablePass guards that the
// warning keeps being emitted while the condition holds, not once per write.
func TestReconcileDataPodDisruptionBudget_WarnsOnEveryApplicablePass(t *testing.T) {
	maxUnavailable := int32(3)
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.PodDisruptionBudget = &vkov1.PodDisruptionBudgetSpec{
			Enabled: true, MaxUnavailable: &maxUnavailable,
		}
	})
	r, _ := newTestReconciler(v)
	rec := &fakeEventRecorder{}
	r.Recorder = rec
	ctx := context.Background()

	require.NoError(t, r.reconcileDataPodDisruptionBudget(ctx, v))
	require.NoError(t, r.reconcileDataPodDisruptionBudget(ctx, v))

	assert.Len(t, rec.withReason(reasonPodDisruptionBudgetTooPermissive), 2,
		"the create pass and the following no-op pass must both warn")
}

// TestReconcileDataPodDisruptionBudget_NoWarningBelowReplicas pins the other
// direction: a budget that still protects the cluster stays quiet.
func TestReconcileDataPodDisruptionBudget_NoWarningBelowReplicas(t *testing.T) {
	v := newTestValkey("test", "default", haWithPDB)
	r, _ := newTestReconciler(v)
	rec := &fakeEventRecorder{}
	r.Recorder = rec
	ctx := context.Background()

	require.NoError(t, r.reconcileDataPodDisruptionBudget(ctx, v))

	assert.Empty(t, rec.events, "the default maxUnavailable 1 with 3 replicas is not a warning")
}
