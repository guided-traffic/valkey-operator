package controller

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
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

			// NA32(a) flipped this expectation. It used to require four Warning
			// Events (both budgets, both passes) and pinned that as intended. The
			// cleanup path runs on every pass of every CR whose spec.podDisruptionBudget
			// is absent, so after the operator upgrade every user who had hand-written
			// the documented pre-feature workaround budget got a permanent Warning
			// stream without having changed anything. With the feature off the operator
			// has no intention towards that budget, so it has nothing to report; the
			// non-destruction the rest of this test asserts is unchanged.
			assert.Empty(t, rec.withReason(reasonPodDisruptionBudgetNotOwned),
				"a CR that never opted in must not warn about a user's own budget")
		})
	}
}

// TestCleanupPodDisruptionBudget_WarnsWhenEnabledButNotApplicable is the other half
// of NA32(a): the gate is the opt-in, not the cleanup path itself. With the feature
// enabled but no budget applicable (fewer than MinPDBReplicas replicas on both
// StatefulSets) the CR asked for operator-managed budgets and the names are taken by
// someone else, so scaling back up would silently produce no budget at all. That
// user needs the warning.
func TestCleanupPodDisruptionBudget_WarnsWhenEnabledButNotApplicable(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.UID = testValkeyUID
		v.Spec.Replicas = 1
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 1}
		v.Spec.PodDisruptionBudget = &vkov1.PodDisruptionBudgetSpec{Enabled: true}
	})
	r, _ := newTestReconciler(v, foreignPDB("test"), foreignPDB("test-sentinel"))
	rec := &fakeEventRecorder{}
	r.Recorder = rec
	ctx := context.Background()

	require.NoError(t, r.reconcilePodDisruptionBudgets(ctx, v))

	assertPDBUntouched(t, r, "test")
	assertPDBUntouched(t, r, "test-sentinel")

	warnings := rec.withReason(reasonPodDisruptionBudgetNotOwned)
	require.Len(t, warnings, 2, "both budget names warn while the feature is enabled")
	assert.Equal(t, corev1.EventTypeWarning, warnings[0].eventType)
	assert.Contains(t, warnings[0].note, "PodDisruptionBudget test exists but is not owned")
	assert.Contains(t, warnings[0].note, "the operator only deletes budgets it created")
}

// TestReconcilePodDisruptionBudget_NoContentWarningsForForeignBudget is NA32(b): with
// the feature enabled against a foreign budget nothing is written, so the NA6/NA7
// content warnings would describe spec values that reached no object — and contradict
// the PodDisruptionBudgetNotOwned warning emitted in the same pass. Both conditions
// hold here (maxUnavailable 2 >= replicas 2, Sentinel quorum 2 == 2 replicas), so a
// missing ownership verdict is immediately visible.
func TestReconcilePodDisruptionBudget_NoContentWarningsForForeignBudget(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.UID = testValkeyUID
		v.Spec.Replicas = 2
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 2}
		v.Spec.PodDisruptionBudget = &vkov1.PodDisruptionBudgetSpec{
			Enabled:        true,
			MaxUnavailable: ptr.To(int32(2)),
		}
	})
	r, _ := newTestReconciler(v, foreignPDB("test"), foreignPDB("test-sentinel"))
	rec := &fakeEventRecorder{}
	r.Recorder = rec
	ctx := context.Background()

	require.NoError(t, r.reconcilePodDisruptionBudgets(ctx, v))

	assert.Len(t, rec.withReason(reasonPodDisruptionBudgetNotOwned), 2,
		"the ownership warning is the one that applies")
	assert.Empty(t, rec.withReason(reasonPodDisruptionBudgetTooPermissive),
		"maxUnavailable was never written to any object")
	assert.Empty(t, rec.withReason(reasonSentinelPodDisruptionBudgetBlocksDrains),
		"the operator does not describe a foreign budget with its own quorum")
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

// --- NA31: the cleanup delete is bound to the object the pass inspected ---

// operatorOwnedPDB is a budget controlled by the Valkey CR, carrying an explicit UID.
// The UID matters: the cleanup delete sends it as a precondition, and the fake client
// does not mint UIDs on Create, so a budget written by the reconcile path would carry
// an empty one and make the assertion vacuous.
func operatorOwnedPDB(name string, uid types.UID) *policyv1.PodDisruptionBudget {
	maxUnavailable := intstr.FromInt32(1)
	return &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
			UID:       uid,
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: vkov1.GroupVersion.String(),
				Kind:       "Valkey",
				Name:       "test",
				UID:        testValkeyUID,
				Controller: ptr.To(true),
			}},
		},
		Spec: policyv1.PodDisruptionBudgetSpec{MaxUnavailable: &maxUnavailable},
	}
}

// capturedDeleteOptions records the options of every PodDisruptionBudget delete a
// pass issues, and lets the delete itself be replaced. The fake client enforces only
// the ResourceVersion precondition, never the UID one, so the UID guard can be
// verified on the call and its failure mode has to be injected.
type capturedDeleteOptions struct {
	opts []client.DeleteOptions
	fail error
}

func (c *capturedDeleteOptions) intercept() interceptor.Funcs {
	return interceptor.Funcs{
		Delete: func(ctx context.Context, cl client.WithWatch, obj client.Object,
			opts ...client.DeleteOption) error {
			if _, ok := obj.(*policyv1.PodDisruptionBudget); ok {
				resolved := client.DeleteOptions{}
				resolved.ApplyOptions(opts)
				c.opts = append(c.opts, resolved)
				if c.fail != nil {
					return c.fail
				}
			}
			return cl.Delete(ctx, obj, opts...)
		},
	}
}

// TestCleanupPodDisruptionBudget_DeletesWithUIDPrecondition is NA31: the ownership
// decision is made on a cache-backed Get, and a delete by name alone still lands on
// whatever holds the name at that moment. If the operator budget was already removed
// and the user recreated their own under the same name before the cache caught up,
// the by-name delete destroyed the user's object — the outcome the ownership guard
// promises against. The UID precondition binds the delete to the inspected object.
func TestCleanupPodDisruptionBudget_DeletesWithUIDPrecondition(t *testing.T) {
	const budgetUID = types.UID("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")
	// spec.podDisruptionBudget absent: the cleanup path for an owned budget.
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.UID = testValkeyUID
		v.Spec.Replicas = 3
	})
	deletes := &capturedDeleteOptions{}
	r, _ := newInterceptedReconciler(deletes.intercept(), v, operatorOwnedPDB("test", budgetUID))
	ctx := context.Background()

	require.NoError(t, r.reconcileDataPodDisruptionBudget(ctx, v))

	require.Len(t, deletes.opts, 1, "the owned budget is deleted")
	require.NotNil(t, deletes.opts[0].Preconditions, "the delete must carry a precondition")
	require.NotNil(t, deletes.opts[0].Preconditions.UID, "the precondition must pin the UID")
	assert.Equal(t, budgetUID, *deletes.opts[0].Preconditions.UID,
		"the UID of the object the pass inspected, not of any later namesake")
	assert.Nil(t, deletes.opts[0].Preconditions.ResourceVersion,
		"no ResourceVersion precondition: the disruption controller rewrites PDB status "+
			"constantly, so a cached read is routinely stale and cleanup would never succeed")

	_, err := getPDB(ctx, r, "test")
	assert.True(t, apierrors.IsNotFound(err), "the owned budget is still actually deleted")
}

// TestCleanupPodDisruptionBudget_ToleratesPreconditionConflict pins the handling of
// the error the precondition produces. A UID mismatch means the name holds a
// different object than the one this pass decided about — the guard worked, there is
// nothing left to delete and nothing to fix, so the pass must not fail over it. The
// next pass re-reads the name and, if the new object is foreign, refuses it there.
func TestCleanupPodDisruptionBudget_ToleratesPreconditionConflict(t *testing.T) {
	const budgetUID = types.UID("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.UID = testValkeyUID
		v.Spec.Replicas = 3
	})
	deletes := &capturedDeleteOptions{
		fail: apierrors.NewConflict(
			schema.GroupResource{Group: "policy", Resource: "poddisruptionbudgets"}, "test",
			errors.New("the UID in the precondition does not match the UID in record")),
	}
	r, _ := newInterceptedReconciler(deletes.intercept(), v, operatorOwnedPDB("test", budgetUID))
	ctx := context.Background()

	require.NoError(t, r.reconcileDataPodDisruptionBudget(ctx, v),
		"a failed UID precondition is the guard working, not a pass failure")

	_, err := getPDB(ctx, r, "test")
	require.NoError(t, err, "the object under that name is left alone")
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

// --- the write paths fail closed ---
//
// A PodDisruptionBudget the operator could not write is a protection the CR asks
// for and the cluster does not have. Every one of these branches therefore has to
// end in an error the reconcile loop returns: the rate limiter then retries, and
// the failure is visible on the CR. Swallowing any of them produces a cluster that
// reports Ready while a node drain can take every data pod at once.

// rejectPDBCreate makes the API server refuse to create the named budget, the way a
// validating webhook or a quota would.
func rejectPDBCreate(name string, err error) interceptor.Funcs {
	return interceptor.Funcs{
		Create: func(ctx context.Context, c client.WithWatch, obj client.Object,
			opts ...client.CreateOption) error {
			if pdb, ok := obj.(*policyv1.PodDisruptionBudget); ok && pdb.Name == name {
				return err
			}
			return c.Create(ctx, obj, opts...)
		},
	}
}

// failPDBGet makes every read of the named budget fail with a non-NotFound error,
// the way an unavailable API server or a broken cache does.
func failPDBGet(name string, err error) interceptor.Funcs {
	return interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey,
			obj client.Object, opts ...client.GetOption) error {
			if _, ok := obj.(*policyv1.PodDisruptionBudget); ok && key.Name == name {
				return err
			}
			return c.Get(ctx, key, obj, opts...)
		},
	}
}

func TestReconcileDataPodDisruptionBudget_CreateRejectionFailsThePass(t *testing.T) {
	v := newTestValkey("test", "default", haWithPDB)
	denied := apierrors.NewForbidden(
		schema.GroupResource{Group: "policy", Resource: "poddisruptionbudgets"}, "test",
		errors.New("denied by webhook"))
	r, _ := newInterceptedReconciler(rejectPDBCreate("test", denied), v)
	rec := &fakeEventRecorder{}
	r.Recorder = rec
	ctx := context.Background()

	err := r.reconcileDataPodDisruptionBudget(ctx, v)

	require.Error(t, err, "a budget that could not be created must not pass as reconciled")
	assert.True(t, apierrors.IsForbidden(err), "the API server verdict must reach the caller unchanged")
	_, getErr := getPDB(ctx, r, "test")
	assert.True(t, apierrors.IsNotFound(getErr), "nothing was written")
	assert.Empty(t, rec.withReason(reasonPodDisruptionBudgetTooPermissive),
		"a budget that does not exist must not be described in a warning")
}

// The sentinel budget is reconciled by a second step, and its failure has to
// survive the step runner rather than be lost behind the successful data step.
func TestReconcileSentinelPodDisruptionBudget_CreateRejectionFailsThePass(t *testing.T) {
	v := newTestValkey("test", "default", haWithPDB)
	denied := apierrors.NewForbidden(
		schema.GroupResource{Group: "policy", Resource: "poddisruptionbudgets"}, "test-sentinel",
		errors.New("denied by webhook"))
	r, _ := newInterceptedReconciler(rejectPDBCreate("test-sentinel", denied), v)
	r.Recorder = &fakeEventRecorder{}
	ctx := context.Background()

	err := r.reconcilePodDisruptionBudgets(ctx, v)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "Sentinel PodDisruptionBudget",
		"the failing step must be named, the data step ran fine")
	_, dataErr := getPDB(ctx, r, "test")
	assert.NoError(t, dataErr, "the data budget of the same pass is still created")
}

// A read that fails for any reason other than NotFound says nothing about whether
// the budget exists. Treating it as absent would make the operator create a second
// budget over the same pods, and the Eviction API refuses a pod covered by two.
func TestReconcilePodDisruptionBudget_UnreadableBudgetFailsThePass(t *testing.T) {
	v := newTestValkey("test", "default", haWithPDB)
	unavailable := apierrors.NewServiceUnavailable("etcd leader changed")
	writes := &pdbWriteCounter{}
	funcs := writes.intercept()
	funcs.Get = failPDBGet("test", unavailable).Get
	r, _ := newInterceptedReconciler(funcs, v)
	ctx := context.Background()

	err := r.reconcileDataPodDisruptionBudget(ctx, v)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "etcd leader changed")
	assert.Zero(t, writes.writes, "an unreadable budget must never be blind-written over")
}

// SetControllerReference is the step that makes the budget garbage-collected with
// the CR. It can only fail when the Scheme does not know the Valkey type, i.e. an
// operator wired wrong at startup -- and then the budget must not be written at
// all: an ownerless PDB under the StatefulSet name outlives the CR and blocks
// every future drain of those pods forever.
func TestReconcilePodDisruptionBudget_OwnerReferenceFailureWritesNothing(t *testing.T) {
	v := newTestValkey("test", "default", haWithPDB)
	writes := &pdbWriteCounter{}
	r, _ := newInterceptedReconciler(writes.intercept(), v)

	// A Scheme that knows PodDisruptionBudgets but not the Valkey CR.
	crippled := runtime.NewScheme()
	require.NoError(t, policyv1.AddToScheme(crippled))
	r.Scheme = crippled
	ctx := context.Background()

	err := r.reconcileDataPodDisruptionBudget(ctx, v)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "setting owner reference on PodDisruptionBudget test",
		"the failure must name the object it is about")
	assert.Zero(t, writes.writes, "no ownerless budget is left behind")
}

// The cleanup path reads before it deletes. A read failure other than NotFound
// leaves it unknown whether an operator-owned budget is still there, so the pass
// has to fail rather than report a cleanup it did not perform.
func TestCleanupPodDisruptionBudget_UnreadableBudgetFailsThePass(t *testing.T) {
	// spec.podDisruptionBudget absent: every pass takes the cleanup path.
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.UID = testValkeyUID
		v.Spec.Replicas = 3
	})
	unavailable := apierrors.NewServiceUnavailable("etcd leader changed")
	r, _ := newInterceptedReconciler(failPDBGet("test", unavailable), v,
		operatorOwnedPDB("test", "11111111-2222-3333-4444-555555555555"))

	err := r.reconcileDataPodDisruptionBudget(context.Background(), v)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "etcd leader changed")
}

// A delete that fails for a reason other than "gone" or "replaced under its name"
// is a real failure: the budget the operator no longer wants is still in place and
// still serializing evictions. Only the UID-precondition conflict is tolerated.
func TestCleanupPodDisruptionBudget_DeleteFailurePropagates(t *testing.T) {
	// spec.podDisruptionBudget absent while Sentinel runs: both cleanup paths apply,
	// and neither may report success while its budget is still in place.
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.UID = testValkeyUID
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
	})
	deletes := &capturedDeleteOptions{
		fail: apierrors.NewForbidden(
			schema.GroupResource{Group: "policy", Resource: "poddisruptionbudgets"}, "test",
			errors.New("no delete permission on poddisruptionbudgets")),
	}
	r, _ := newInterceptedReconciler(deletes.intercept(), v,
		operatorOwnedPDB("test", "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"),
		operatorOwnedPDB("test-sentinel", "bbbbbbbb-cccc-dddd-eeee-ffffffffffff"))
	ctx := context.Background()

	err := r.reconcilePodDisruptionBudgets(ctx, v)

	require.Error(t, err, "an RBAC failure must not be mistaken for the UID guard doing its job")
	assert.Contains(t, err.Error(), "data PodDisruptionBudget: deleting poddisruptionbudget")
	assert.Contains(t, err.Error(), "Sentinel PodDisruptionBudget: deleting poddisruptionbudget")
	assert.True(t, apierrors.IsForbidden(err), "the cause must survive the wrapping")
	for _, name := range []string{"test", "test-sentinel"} {
		_, getErr := getPDB(ctx, r, name)
		assert.NoError(t, getErr, "%s is still there, which is why the pass failed", name)
	}
}
