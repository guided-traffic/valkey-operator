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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/builder"
)

// haWithPDB is a three-replica Sentinel cluster with PodDisruptionBudgets enabled.
// The UID is set so the ownerReference the operator writes is distinguishable from
// the empty one of a foreign object (NA14).
func haWithPDB(v *vkov1.Valkey) {
	v.UID = testValkeyUID
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

// TestReconcileSentinelPodDisruptionBudget_WarnsWhenQuorumEqualsReplicas is NA7:
// with 2 Sentinels the quorum equals the replica count, so the budget refuses every
// eviction and a node drain hosting a Sentinel pod stalls. Only a builder comment
// said so before; nothing warned at runtime.
func TestReconcileSentinelPodDisruptionBudget_WarnsWhenQuorumEqualsReplicas(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		haWithPDB(v)
		v.Spec.Sentinel.Replicas = 2
	})
	r, _ := newTestReconciler(v)
	rec := &fakeEventRecorder{}
	r.Recorder = rec
	ctx := context.Background()

	require.NoError(t, r.reconcileSentinelPodDisruptionBudget(ctx, v))

	pdb, err := getPDB(ctx, r, "test-sentinel")
	require.NoError(t, err, "the budget is still created: the quorum is what protects failover")
	require.NotNil(t, pdb.Spec.MinAvailable)
	assert.Equal(t, intstr.FromInt32(2), *pdb.Spec.MinAvailable, "the formula is unchanged")

	warnings := rec.withReason(reasonSentinelPodDisruptionBudgetBlocksDrains)
	require.Len(t, warnings, 1)
	assert.Equal(t, corev1.EventTypeWarning, warnings[0].eventType)
	assert.Contains(t, warnings[0].note, "minAvailable 2 equals spec.sentinel.replicas 2")
	assert.Contains(t, warnings[0].note, builder.SentinelPodDisruptionBudgetName(v))
}

// TestReconcileSentinelPodDisruptionBudget_WarnsAfterScaleDownWithoutWrite is the
// Sentinel counterpart of the NA6 scale-down path: 3 -> 2 Sentinels keeps the
// quorum at 2, so the PDB object never changes while the budget turns into a drain
// blocker. A write-gated warning would be silent for exactly that transition.
func TestReconcileSentinelPodDisruptionBudget_WarnsAfterScaleDownWithoutWrite(t *testing.T) {
	v := newTestValkey("test", "default", haWithPDB)
	writes := &pdbWriteCounter{}
	r, _ := newInterceptedReconciler(writes.intercept(), v)
	rec := &fakeEventRecorder{}
	r.Recorder = rec
	ctx := context.Background()

	require.NoError(t, r.reconcileSentinelPodDisruptionBudget(ctx, v))
	require.Equal(t, 1, writes.writes, "precondition: the first pass creates the PDB")
	require.Empty(t, rec.withReason(reasonSentinelPodDisruptionBudgetBlocksDrains),
		"a quorum of 2 out of 3 Sentinels still permits one eviction")

	v.Spec.Sentinel.Replicas = 2
	writes.reset()
	rec.reset()
	require.NoError(t, r.reconcileSentinelPodDisruptionBudget(ctx, v))

	assert.Zero(t, writes.writes, "the quorum stays 2, so the scale-down does not touch the PDB object")
	assert.Len(t, rec.withReason(reasonSentinelPodDisruptionBudgetBlocksDrains), 1,
		"the pass that turned the budget into a drain blocker must warn")
}

// TestReconcileSentinelPodDisruptionBudget_WarnsOnEveryApplicablePass guards that
// the warning keeps being emitted while the condition holds, not once per write.
func TestReconcileSentinelPodDisruptionBudget_WarnsOnEveryApplicablePass(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		haWithPDB(v)
		v.Spec.Sentinel.Replicas = 2
	})
	r, _ := newTestReconciler(v)
	rec := &fakeEventRecorder{}
	r.Recorder = rec
	ctx := context.Background()

	require.NoError(t, r.reconcileSentinelPodDisruptionBudget(ctx, v))
	require.NoError(t, r.reconcileSentinelPodDisruptionBudget(ctx, v))

	assert.Len(t, rec.withReason(reasonSentinelPodDisruptionBudgetBlocksDrains), 2,
		"the create pass and the following no-op pass must both warn")
}

// TestReconcileSentinelPodDisruptionBudget_NoWarningAtOddCount pins the other
// direction: an odd Sentinel count leaves room for one voluntary disruption.
func TestReconcileSentinelPodDisruptionBudget_NoWarningAtOddCount(t *testing.T) {
	for _, replicas := range []int32{3, 5} {
		v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
			haWithPDB(v)
			v.Spec.Sentinel.Replicas = replicas
		})
		r, _ := newTestReconciler(v)
		rec := &fakeEventRecorder{}
		r.Recorder = rec
		ctx := context.Background()

		require.NoError(t, r.reconcileSentinelPodDisruptionBudget(ctx, v))

		assert.Empty(t, rec.events, "%d Sentinels keep a quorum below the replica count", replicas)
	}
}

// --- NA14: PDBs the operator does not own are never deleted and never adopted ---

// testValkeyUID is the UID of the Valkey CR in the PDB tests. metav1.IsControlledBy
// compares UIDs, so an empty one would make every ownerReference-less object look
// owned and the guard untestable.
const testValkeyUID = types.UID("11111111-2222-3333-4444-555555555555")

// foreignPDB is a PodDisruptionBudget under one of the operator's budget names that
// the operator did not create — the hand-written budget the incident remediation
// told users to add. minAvailable instead of maxUnavailable and a foreign selector
// make any operator write to it visible.
func foreignPDB(name string, owners ...metav1.OwnerReference) *policyv1.PodDisruptionBudget {
	minAvailable := intstr.FromInt32(2)
	return &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{
			Name:            name,
			Namespace:       "default",
			Labels:          map[string]string{"owner": "platform-team"},
			OwnerReferences: owners,
		},
		Spec: policyv1.PodDisruptionBudgetSpec{
			MinAvailable: &minAvailable,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "hand-written"},
			},
		},
	}
}

// assertPDBUntouched verifies the stored PDB is still the hand-written one.
func assertPDBUntouched(t *testing.T, r *ValkeyReconciler, name string) {
	t.Helper()
	pdb, err := getPDB(context.Background(), r, name)
	require.NoError(t, err, "the foreign PodDisruptionBudget must still exist")
	require.NotNil(t, pdb.Spec.MinAvailable, "minAvailable must survive")
	assert.Equal(t, intstr.FromInt32(2), *pdb.Spec.MinAvailable)
	assert.Nil(t, pdb.Spec.MaxUnavailable, "the operator must not add its maxUnavailable")
	require.NotNil(t, pdb.Spec.Selector)
	assert.Equal(t, map[string]string{"app": "hand-written"}, pdb.Spec.Selector.MatchLabels,
		"the selector must not be repointed at the operator's pods")
	assert.Equal(t, "platform-team", pdb.Labels["owner"], "foreign labels must not be dropped")
	assert.Empty(t, pdb.Annotations[builder.AnnotationOperatorVersion],
		"the operator must not stamp its version on a foreign object")
}

// TestCleanupPodDisruptionBudget_KeepsForeignBudget is NA14's severe half: the
// cleanup path runs on every pass of every CR whose PDBs are absent or disabled —
// which is every pre-existing CR after the operator upgrade — and deleted the
// same-named PDB by name. A hand-written budget for the data pods is called exactly
// that (the StatefulSet name), so it disappeared silently and stayed gone.
func TestCleanupPodDisruptionBudget_KeepsForeignBudget(t *testing.T) {
	otherOwner := metav1.OwnerReference{
		APIVersion: "apps/v1",
		Kind:       "Deployment",
		Name:       "someone-else",
		UID:        types.UID("99999999-9999-9999-9999-999999999999"),
		Controller: ptr.To(true),
	}
	cases := []struct {
		name   string
		owners []metav1.OwnerReference
	}{
		{name: "hand-written, no ownerReference"},
		{name: "controlled by another object", owners: []metav1.OwnerReference{otherOwner}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// spec.podDisruptionBudget is absent: the cleanup path for both budgets.
			v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
				v.UID = testValkeyUID
				v.Spec.Replicas = 3
				v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
			})
			data := foreignPDB("test", tc.owners...)
			sentinel := foreignPDB("test-sentinel", tc.owners...)
			r, _ := newTestReconciler(v, data, sentinel)
			rec := &fakeEventRecorder{}
			r.Recorder = rec
			ctx := context.Background()

			// Two passes: the deletion was not a one-off but ran on every pass.
			require.NoError(t, r.reconcilePodDisruptionBudgets(ctx, v))
			require.NoError(t, r.reconcilePodDisruptionBudgets(ctx, v))

			assertPDBUntouched(t, r, "test")
			assertPDBUntouched(t, r, "test-sentinel")

			warnings := rec.withReason(reasonPodDisruptionBudgetNotOwned)
			require.Len(t, warnings, 4, "both budgets warn on both passes")
			assert.Equal(t, corev1.EventTypeWarning, warnings[0].eventType)
			assert.Contains(t, warnings[0].note, "PodDisruptionBudget test exists but is not owned")
			assert.Contains(t, warnings[0].note, "the operator only deletes budgets it created")
		})
	}
}

// TestCleanupPodDisruptionBudget_DeletesOwnedBudget is the positive control for the
// guard: with a real UID on the CR, the budget the operator created is still removed
// when the feature is switched off. Without it a UID-comparing guard could pass the
// foreign-object tests while breaking cleanup outright.
func TestCleanupPodDisruptionBudget_DeletesOwnedBudget(t *testing.T) {
	v := newTestValkey("test", "default", haWithPDB)
	r, _ := newTestReconciler(v)
	ctx := context.Background()

	require.NoError(t, r.reconcilePodDisruptionBudgets(ctx, v))
	data, err := getPDB(ctx, r, "test")
	require.NoError(t, err)
	require.True(t, metav1.IsControlledBy(data, v), "precondition: the operator owns the budget")

	v.Spec.PodDisruptionBudget.Enabled = false
	require.NoError(t, r.reconcilePodDisruptionBudgets(ctx, v))

	_, err = getPDB(ctx, r, "test")
	assert.True(t, apierrors.IsNotFound(err), "an owned budget is still deleted")
	_, err = getPDB(ctx, r, "test-sentinel")
	assert.True(t, apierrors.IsNotFound(err), "an owned sentinel budget is still deleted")
}

// TestReconcilePodDisruptionBudget_DoesNotAdoptForeignBudget is NA14's mirror image:
// enabling the feature next to a same-named hand-written budget silently adopted it —
// Get, HasChanged, Update overwrote the budget fields and the selector without ever
// asking who owns the object.
func TestReconcilePodDisruptionBudget_DoesNotAdoptForeignBudget(t *testing.T) {
	v := newTestValkey("test", "default", haWithPDB)
	data := foreignPDB("test")
	sentinel := foreignPDB("test-sentinel")
	r, _ := newTestReconcilerWithVersion("1.2.3", v, data, sentinel)
	rec := &fakeEventRecorder{}
	r.Recorder = rec
	ctx := context.Background()

	require.NoError(t, r.reconcilePodDisruptionBudgets(ctx, v))

	assertPDBUntouched(t, r, "test")
	assertPDBUntouched(t, r, "test-sentinel")

	stored, err := getPDB(ctx, r, "test")
	require.NoError(t, err)
	assert.Empty(t, stored.OwnerReferences, "the operator must not claim ownership")

	warnings := rec.withReason(reasonPodDisruptionBudgetNotOwned)
	require.Len(t, warnings, 2, "both budgets warn")
	assert.Equal(t, corev1.EventTypeWarning, warnings[0].eventType)
	assert.Contains(t, warnings[0].note,
		"spec.podDisruptionBudget cannot take effect until that budget is deleted or renamed")
}

// TestReconcilePodDisruptionBudget_NoWriteToForeignBudget pins that the refusal costs
// no API write at all, so a foreign budget cannot be churned by the reconcile loop.
func TestReconcilePodDisruptionBudget_NoWriteToForeignBudget(t *testing.T) {
	v := newTestValkey("test", "default", haWithPDB)
	writes := &pdbWriteCounter{}
	r, _ := newInterceptedReconciler(writes.intercept(), v, foreignPDB("test"), foreignPDB("test-sentinel"))
	r.Recorder = &fakeEventRecorder{}
	ctx := context.Background()

	require.NoError(t, r.reconcilePodDisruptionBudgets(ctx, v))

	assert.Zero(t, writes.writes, "no create and no update against a foreign PodDisruptionBudget")
}
