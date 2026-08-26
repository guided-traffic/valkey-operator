package controller

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/builder"
	"github.com/guided-traffic/valkey-operator/internal/common"
)

// The controller half of docs/adr/0023-volume-claim-templates-are-immutable.md: a
// StatefulSet whose live volumeClaimTemplates are not the ones spec.persistence
// asks for is refused rather than written, reported on the CR, and — where only a
// storage parameter differs — left to take every other change.
//
// The two structural directions are not symmetric, which is why each has its own
// test. Enabling persistence produces a write the API server rejects on every pass
// ("spec: Forbidden: updates to statefulset spec for fields other than 'replicas',
// 'ordinals', 'template', 'updateStrategy', 'persistentVolumeClaimRetentionPolicy'
// and 'minReadySeconds' are forbidden"). Disabling it produces a write the API
// server *accepts*: the pod template gains an emptyDir named "data" while the live
// claim of the same name stays on the object. Measured against a real kube-apiserver
// (envtest 1.29), not reproducible from the fake client here — which accepts both,
// and is therefore only able to show that the operator submits neither.

// persistenceEnabled turns spec.persistence on with the requested size, the shape
// of every persistent cluster the operator manages.
func persistenceEnabled(size string) func(*vkov1.Valkey) {
	return func(v *vkov1.Valkey) {
		v.Spec.Persistence = &vkov1.PersistenceSpec{
			Enabled: true,
			Mode:    vkov1.PersistenceModeRDB,
			Size:    resource.MustParse(size),
		}
	}
}

// haCluster is a three-replica cluster with three Sentinels, the topology whose
// Sentinel StatefulSet the shared guard call site runs on.
func haCluster(v *vkov1.Valkey) {
	v.Spec.Replicas = 3
	v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
}

// sentinelStsFor builds the Sentinel StatefulSet the operator would persist for v,
// owned by it and carrying the deterministic UID the ownership guards compare
// (ADR 0020) — the Sentinel counterpart of stsForValkey.
func sentinelStsFor(v *vkov1.Valkey) *appsv1.StatefulSet {
	sts := builder.BuildSentinelStatefulSet(v)
	sts.UID = testStsUID(v, common.ComponentSentinel)
	controllerRefTo(v, sts)
	return sts
}

// statefulSetUpdates counts the Update calls a pass issues against StatefulSets.
//
// The stored object alone cannot carry the assertion: reconcileStatefulSet copies
// only replicas, pod template and labels, so a write that reached the API server
// would leave the volumeClaimTemplates looking untouched either way. "The operator
// submitted nothing" is the property under test, and only the call count shows it.
type statefulSetUpdates struct {
	count int
}

func (s *statefulSetUpdates) intercept() interceptor.Funcs {
	return interceptor.Funcs{
		Update: func(ctx context.Context, c client.WithWatch, obj client.Object,
			opts ...client.UpdateOption) error {
			if _, ok := obj.(*appsv1.StatefulSet); ok {
				s.count++
			}
			return c.Update(ctx, obj, opts...)
		},
	}
}

// dataVolumeSource returns the VolumeSource backing the pod template's "data"
// volume, or nil when the template declares none. A persistence-disabled template
// carries an emptyDir under that name; a persistence-enabled one carries no volume
// at all, because the claim supplies it.
func dataVolumeSource(tpl corev1.PodTemplateSpec) *corev1.VolumeSource {
	for i := range tpl.Spec.Volumes {
		if tpl.Spec.Volumes[i].Name == builder.DataVolumeName {
			return &tpl.Spec.Volumes[i].VolumeSource
		}
	}
	return nil
}

// findCondition is meta.FindStatusCondition on a Valkey, so a test file that does
// not import apimachinery's meta package can still read a condition.
func findCondition(v *vkov1.Valkey, condType string) *metav1.Condition {
	return meta.FindStatusCondition(v.Status.Conditions, condType)
}

// storedValkey and storedSts read an object back from the fake client.
func storedValkey(t *testing.T, c client.Client, v *vkov1.Valkey) *vkov1.Valkey {
	t.Helper()
	got := &vkov1.Valkey{}
	require.NoError(t, c.Get(context.Background(),
		types.NamespacedName{Name: v.Name, Namespace: v.Namespace}, got))
	return got
}

func storedSts(t *testing.T, c client.Client, name, namespace string) *appsv1.StatefulSet {
	t.Helper()
	got := &appsv1.StatefulSet{}
	require.NoError(t, c.Get(context.Background(),
		types.NamespacedName{Name: name, Namespace: namespace}, got))
	return got
}

// --- structural conflict: the direction the API server rejects ---

func TestReconcileStatefulSet_RefusesTheDoomedUpdateWhenPersistenceIsEnabledLater(t *testing.T) {
	// The live cluster was created without persistence and the spec now asks for
	// it. BuildStatefulSet answers with a volumeClaimTemplate the live object can
	// never gain and a pod template that drops the emptyDir and mounts the claim
	// instead, so the update is rejected by the API server on every pass — while
	// the operator keeps submitting it, because the drift never converges.
	live := stsForValkey(newTestValkey("test", "default"))

	v := newTestValkey("test", "default", persistenceEnabled("1Gi"))
	updates := &statefulSetUpdates{}
	r, c := newTestReconcilerWithInterceptor(updates.intercept(), v, live)
	rec := &fakeEventRecorder{}
	r.Recorder = rec

	before := storedSts(t, c, v.Name, v.Namespace)
	require.True(t, builder.StatefulSetHasChanged(builder.BuildStatefulSet(v, testOperatorImage), before),
		"the fixture must present real drift, or the zero-write assertions below hold "+
			"for a pass that had nothing to write in the first place (ADR 0017 D10)")

	err := r.reconcileStatefulSet(context.Background(), v)

	require.Error(t, err,
		"persistence.enabled=true on a StatefulSet without claims is a durability statement "+
			"that is not true; the CR cannot do the job it was asked to do (ADR 0020 D2)")
	assert.ErrorIs(t, err, errRecreateRequired)

	assert.Zero(t, updates.count, "the operator must submit no write it knows the API server rejects")
	after := storedSts(t, c, v.Name, v.Namespace)
	assert.Equal(t, before.ResourceVersion, after.ResourceVersion, "nothing may have been written")
	assert.Equal(t, before.Spec.Template, after.Spec.Template,
		"without the guard the pod template is replaced by one that mounts a claim the object does not have")

	events := rec.withReason(reasonStatefulSetRecreateRequired)
	require.Len(t, events, 1, "a refusal nobody can see is indistinguishable from a stuck operator")
	assert.Equal(t, corev1.EventTypeWarning, events[0].eventType)
	assert.Contains(t, events[0].note, "back it up",
		"the Event is the surface a user reaches first, and the migration is not lossless: it has to "+
			"say so before a user starts one (ADR 0023 D6)")

	cond := findCondition(storedValkey(t, c, v), vkov1.ConditionTypeStorageSpecNotApplied)
	require.NotNil(t, cond, "the Events expire; the condition is the durable record")
	assert.Equal(t, metav1.ConditionTrue, cond.Status)
	assert.Equal(t, vkov1.ReasonRecreateRequired, cond.Reason)
}

// --- structural conflict: the direction the API server accepts ---

func TestReconcileStatefulSet_RefusesTheAcceptedUpdateWhenPersistenceIsDisabledLater(t *testing.T) {
	// The dangerous direction. The API server takes this write: volumeClaimTemplates
	// are not touched by it at all, so the object ends up with an emptyDir named
	// "data" in its pod template and the claim of the same name still attached —
	// and the statefulset-controller keeps creating pods from a template whose
	// storage silently became ephemeral. Nothing about the result is an error, which
	// is exactly why the operator has to refuse it rather than rely on a rejection.
	live := stsForValkey(newTestValkey("test", "default", persistenceEnabled("1Gi")))
	require.Len(t, live.Spec.VolumeClaimTemplates, 1, "the fixture must be a persistent cluster")

	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Persistence = &vkov1.PersistenceSpec{Enabled: false}
	})
	updates := &statefulSetUpdates{}
	r, c := newTestReconcilerWithInterceptor(updates.intercept(), v, live)
	rec := &fakeEventRecorder{}
	r.Recorder = rec

	desired := builder.BuildStatefulSet(v, testOperatorImage)
	before := storedSts(t, c, v.Name, v.Namespace)
	require.True(t, builder.StatefulSetHasChanged(desired, before),
		"the fixture must present real drift, or the zero-write assertions below hold "+
			"for a pass that had nothing to write in the first place (ADR 0017 D10)")
	require.NotNil(t, dataVolumeSource(desired.Spec.Template), "the write under test is the one that")
	require.NotNil(t, dataVolumeSource(desired.Spec.Template).EmptyDir,
		"adds an emptyDir named data next to a live claim of the same name")

	err := r.reconcileStatefulSet(context.Background(), v)

	require.Error(t, err, "a write the API server accepts and that breaks the cluster must fail the step")
	assert.ErrorIs(t, err, errRecreateRequired)

	assert.Zero(t, updates.count, "the operator must not submit the write the API server would take")
	after := storedSts(t, c, v.Name, v.Namespace)
	assert.Equal(t, before.ResourceVersion, after.ResourceVersion, "nothing may have been written")
	assert.Nil(t, dataVolumeSource(after.Spec.Template),
		"an emptyDir named data on a StatefulSet that still carries the claim is the failure this guard exists for")
	assert.Len(t, after.Spec.VolumeClaimTemplates, 1, "and the claim is still there to collide with it")

	events := rec.withReason(reasonStatefulSetRecreateRequired)
	require.Len(t, events, 1)
	assert.Equal(t, corev1.EventTypeWarning, events[0].eventType)
	assert.Contains(t, events[0].note, "back it up")
	assert.Contains(t, events[0].note, "Reverting spec.persistence",
		"the free way out has to be named: putting the spec back clears the block with no downtime")

	cond := findCondition(storedValkey(t, c, v), vkov1.ConditionTypeStorageSpecNotApplied)
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionTrue, cond.Status)
	assert.Equal(t, vkov1.ReasonRecreateRequired, cond.Reason)
}

// --- parameter conflict: the pod template is still written ---

func TestReconcileStatefulSet_AppliesOtherChangesWhenOnlyStorageParametersDiffer(t *testing.T) {
	// The claims the spec asks for are the claims that exist; only the requested
	// size differs. The pod template update is legal, unrelated to the claims and
	// carries the replica count with it, so holding it would wedge an apply that
	// changes storage and image together for a difference no write can ever settle
	// (ADR 0020 D2 applied to a conflict that is not structural).
	live := stsForValkey(newTestValkey("test", "default", persistenceEnabled("1Gi")))

	v := newTestValkey("test", "default", persistenceEnabled("5Gi"), func(v *vkov1.Valkey) {
		v.Spec.Image = "valkey/valkey:9.0"
	})
	r, c := newTestReconciler(v, live)
	rec := &fakeEventRecorder{}
	r.Recorder = rec

	require.NoError(t, r.reconcileStatefulSet(context.Background(), v),
		"a storage parameter nothing can ever apply must not block the changes that can be")

	after := storedSts(t, c, v.Name, v.Namespace)
	assert.Equal(t, "valkey/valkey:9.0", after.Spec.Template.Spec.Containers[0].Image,
		"the image change rides in the same apply and must land")

	cond := findCondition(storedValkey(t, c, v), vkov1.ConditionTypeStorageSpecNotApplied)
	require.NotNil(t, cond, "a size that never reaches the cluster is not something to apply silently")
	assert.Equal(t, metav1.ConditionTrue, cond.Status)
	assert.Equal(t, vkov1.ReasonVolumeClaimTemplatesImmutable, cond.Reason,
		"this is not a recreate: recreating rebinds the same claims by name and changes nothing")
	assert.Contains(t, cond.Message, "size 5Gi requested, 1Gi in use",
		"the condition has to name the difference, not just its existence")

	events := rec.withReason(reasonStatefulSetRecreateRequired)
	require.Len(t, events, 1)
	assert.Equal(t, corev1.EventTypeWarning, events[0].eventType)
	assert.Contains(t, events[0].note, "allowVolumeExpansion",
		"growing a volume is an edit on each PersistentVolumeClaim, and the Event says so")
	assert.NotContains(t, events[0].note, "back it up",
		"recreating the StatefulSet rebinds the existing claims by name, so this shape must not send "+
			"a user through a maintenance window at all")
}

// --- the condition, in both directions ---

func TestReconcileStatefulSet_ClearsTheConditionOnceTheClaimsAgreeAgain(t *testing.T) {
	// The spec was put back, or the StatefulSet was recreated. Clearing is explicit
	// rather than by removal: a series that disappears is harder to tell from one
	// that was never evaluated.
	v := newTestValkey("test", "default", persistenceEnabled("1Gi"))
	meta.SetStatusCondition(&v.Status.Conditions, metav1.Condition{
		Type:    vkov1.ConditionTypeStorageSpecNotApplied,
		Status:  metav1.ConditionTrue,
		Reason:  vkov1.ReasonRecreateRequired,
		Message: "from an earlier pass",
	})
	live := stsForValkey(v)
	r, c := newTestReconciler(v, live)
	rec := &fakeEventRecorder{}
	r.Recorder = rec

	require.NoError(t, r.reconcileStatefulSet(context.Background(), v))

	cond := findCondition(storedValkey(t, c, v), vkov1.ConditionTypeStorageSpecNotApplied)
	require.NotNil(t, cond, "the condition must be resolved, not deleted")
	assert.Equal(t, metav1.ConditionFalse, cond.Status)
	assert.Equal(t, vkov1.ReasonStorageSpecApplied, cond.Reason)
	assert.Empty(t, rec.events, "agreeing claims are not worth an Event on every pass")
}

func TestReconcileStatefulSet_LeavesAClusterThatNeverConflictedWithoutACondition(t *testing.T) {
	// The vast majority of clusters. A False condition written on every one of them
	// is a status write per new cluster and a permanent entry in `kubectl describe`
	// for a problem that never happened, which is why the clear is gated on the
	// condition already existing. This one does not break when the guard call is
	// removed — no call, no condition — it breaks when that gate is removed.
	v := newTestValkey("test", "default", persistenceEnabled("1Gi"))
	live := stsForValkey(v)
	r, c := newTestReconciler(v, live)

	require.NoError(t, r.reconcileStatefulSet(context.Background(), v))

	assert.Nil(t, findCondition(storedValkey(t, c, v), vkov1.ConditionTypeStorageSpecNotApplied),
		"a cluster that never conflicted must not gain a resolved condition it never had")
}

// --- the refusal reaches the CR ---

func TestReconcileBlockedReason_RecreateRequiredRanksBetweenForeignObjectAndAdmission(t *testing.T) {
	// Three causes that end differently. An admission gate reopens by itself, so
	// reporting it would hide the two that need a human; and a foreign object means
	// nothing under that name is ours at all, which has to be said before anything
	// about the object's fields.
	recreate := recreateRequiredError("StatefulSet", "test",
		"the spec asks for data, which the live StatefulSet does not have")
	admission := internalErr("failed calling webhook \"mutate.kyverno.svc-fail\"")

	assert.Equal(t, vkov1.ReasonRecreateRequired, reconcileBlockedReason(recreate))
	assert.Equal(t, vkov1.ReasonRecreateRequired,
		reconcileBlockedReason(errors.Join(admission, recreate)),
		"the admission gate reopens on its own; an immutable field needs a human")
	assert.Equal(t, vkov1.ReasonForeignObject,
		reconcileBlockedReason(errors.Join(recreate, foreignObjectError("StatefulSet", "test"))),
		"a name held by a stranger outranks a field of an object that is ours")
	assert.Equal(t, vkov1.ReasonWriteFailed, reconcileBlockedReason(internalErr("quota exceeded")),
		"positive control: the default reason must still be reachable (ADR 0017 D11)")
}

// --- the shared call site on the Sentinel reconciler ---

// TestReconcileSentinelStatefulSet_ReportsNoConflictOnAHealthySentinelCluster is
// the positive control for the guard on the Sentinel reconciler (ADR 0017 D11).
//
// It is partly a both-directions test (D8) and says so: the builder writes no
// volumeClaimTemplates for Sentinel, so deleting the guard call outright leaves it
// green. What it does catch, measured in the mutation audit, is a comparison that
// reported a conflict on empty against empty, which would touch the Sentinel
// StatefulSet of every HA cluster in the fleet.
//
// It used to claim the unconditional-clear half too. That half is gone: since
// ADR 0023 D4a this call site passes mayClear=false, so no mutation of the clear
// can reach it from here. TestReconcileSentinelStatefulSet_DoesNotClearAConflictItDidNotFind
// is where that half lives now, and it needs a seeded condition to say anything —
// which is exactly why this test could not carry it.
func TestReconcileSentinelStatefulSet_ReportsNoConflictOnAHealthySentinelCluster(t *testing.T) {
	v := newTestValkey("test", "default", haCluster)
	live := sentinelStsFor(v)
	r, c := newTestReconciler(v, live)
	rec := &fakeEventRecorder{}
	r.Recorder = rec

	require.NoError(t, r.reconcileSentinelStatefulSet(context.Background(), v))

	assert.Empty(t, rec.withReason(reasonSentinelStatefulSetRecreateRequired))
	assert.Nil(t, findCondition(storedValkey(t, c, v), vkov1.ConditionTypeStorageSpecNotApplied))
}

func TestReconcileSentinelStatefulSet_RefusesAClaimTheSpecNoLongerAsksFor(t *testing.T) {
	// The Sentinel reconciler shares no code with the data one, so the guard call
	// there is what stops a future Sentinel storage feature from reintroducing the
	// trap in a second place. Nothing in the builder can produce that today, so the
	// fixture stands in for it: a live Sentinel StatefulSet carrying a claim the
	// spec does not ask for. What is under test is that the call site is wired up
	// and reports under its own Event reason.
	v := newTestValkey("test", "default", haCluster)
	live := sentinelStsFor(newTestValkey("test", "default", haCluster, func(v *vkov1.Valkey) {
		v.Spec.Image = "valkey/valkey:7.2"
	}))
	live.Spec.VolumeClaimTemplates = []corev1.PersistentVolumeClaim{{
		ObjectMeta: metav1.ObjectMeta{Name: "sentinel-state"},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("1Gi")},
			},
		},
	}}

	updates := &statefulSetUpdates{}
	r, c := newTestReconcilerWithInterceptor(updates.intercept(), v, live)
	rec := &fakeEventRecorder{}
	r.Recorder = rec

	before := storedSts(t, c, live.Name, live.Namespace)
	require.True(t, builder.SentinelStatefulSetHasChanged(builder.BuildSentinelStatefulSet(v), before),
		"the fixture must present real drift, or the zero-write assertion holds for a pass "+
			"that had nothing to write in the first place (ADR 0017 D10)")

	err := r.reconcileSentinelStatefulSet(context.Background(), v)

	require.Error(t, err)
	assert.ErrorIs(t, err, errRecreateRequired)
	assert.Zero(t, updates.count, "the guard has to sit in front of the Sentinel drift check too")

	require.Len(t, rec.withReason(reasonSentinelStatefulSetRecreateRequired), 1)
	assert.Empty(t, rec.withReason(reasonStatefulSetRecreateRequired),
		"one reason per object family: an Event about the Sentinel tier must not read as one about the data tier")

	cond := findCondition(storedValkey(t, c, v), vkov1.ConditionTypeStorageSpecNotApplied)
	require.NotNil(t, cond)
	assert.Equal(t, vkov1.ReasonRecreateRequired, cond.Reason)
}

// --- two evaluators, one clear authority (T16) ---

// [REGRESSION] The Sentinel StatefulSet has no volumeClaimTemplates, so its guard
// call always lands in the default arm — and that arm used to clear. It runs after
// the data one (resourceReconcileSteps), so on a Sentinel cluster whose data tier
// had a claim conflict the pass reported the conflict and then erased it, and the
// CR ended the pass with StorageSpecNotApplied=False.
//
// Worse than a stale value: writeStatusCondition re-Gets the CR, so the presence
// guard in clearStorageSpecNotApplied found the True the data tier had just stored
// and cleared it. The condition flipped True→False on every single pass, two status
// writes and two LastTransitionTime moves per reconcile, for as long as the conflict
// stood.
//
// Driving one reconcile step proves nothing here — the defect is which step runs
// last — so this drives the whole resource pass. The pre-created Sentinel
// StatefulSet is load-bearing: without it reconcileSentinelStatefulSet creates the
// object and returns before ever reaching the guard, and the test is green on the
// unfixed code (ADR 0017 D10).
func TestReconcileResources_SentinelTierDoesNotClearTheDataTiersStorageConflict(t *testing.T) {
	// The live cluster was created without persistence; the spec now asks for it.
	live := stsForValkey(newTestValkey("test", "default", haCluster))

	v := newTestValkey("test", "default", haCluster, persistenceEnabled("1Gi"))
	sentinel := sentinelStsFor(v)
	r, c := newTestReconciler(v, live, sentinel)
	rec := &fakeEventRecorder{}
	r.Recorder = rec

	require.True(t, builder.StatefulSetHasChanged(builder.BuildStatefulSet(v, testOperatorImage),
		storedSts(t, c, live.Name, live.Namespace)),
		"the fixture must present a real data-tier conflict, or the assertion below holds "+
			"for a pass that had nothing to report (ADR 0017 D10)")
	require.False(t, builder.SentinelStatefulSetHasChanged(builder.BuildSentinelStatefulSet(v), sentinel),
		"the Sentinel StatefulSet must be the one the operator would write, so its guard "+
			"reaches the default arm — which is the arm under test")

	err := r.reconcileResources(context.Background(), v)

	require.Error(t, err, "the data StatefulSet step refuses")
	assert.ErrorIs(t, err, errRecreateRequired)

	cond := findCondition(storedValkey(t, c, v), vkov1.ConditionTypeStorageSpecNotApplied)
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionTrue, cond.Status,
		"the tier that has nothing to say must not be the last writer of a condition the "+
			"other tier raised")
	assert.Equal(t, vkov1.ReasonRecreateRequired, cond.Reason)
}

// The parameter-conflict shape is the one that matters most, and it is invisible
// without this: guardVolumeClaimTemplates returns nil there, so nothing blocks the
// reconcile, ReconcileBlocked stays False and the phase stays OK. The condition is
// then the only durable statement the CR makes about its storage — outliving the
// Warning Event is exactly what ADR 0023 D5 built it for — and it was wrong.
func TestReconcileResources_SentinelTierDoesNotClearAParameterConflict(t *testing.T) {
	live := stsForValkey(newTestValkey("test", "default", haCluster, persistenceEnabled("1Gi")))

	v := newTestValkey("test", "default", haCluster, persistenceEnabled("2Gi"))
	sentinel := sentinelStsFor(v)
	r, c := newTestReconciler(v, live, sentinel)
	rec := &fakeEventRecorder{}
	r.Recorder = rec

	err := r.reconcileResources(context.Background(), v)

	require.NoError(t, err,
		"a changed size leaves the pod template writable, so the pass is not blocked (ADR 0023 D2)")

	cond := findCondition(storedValkey(t, c, v), vkov1.ConditionTypeStorageSpecNotApplied)
	require.NotNil(t, cond, "with the reconcile unblocked this condition is the only durable signal")
	assert.Equal(t, metav1.ConditionTrue, cond.Status)
	assert.Equal(t, vkov1.ReasonVolumeClaimTemplatesImmutable, cond.Reason)
}

// The same rule at the site it lives at, in twelve lines. The full pass above pins
// the step order; this pins the guard itself, so a refactor that keeps the order
// and drops the ownership rule still goes red.
func TestReconcileSentinelStatefulSet_DoesNotClearAConflictItDidNotFind(t *testing.T) {
	v := newTestValkey("test", "default", haCluster)
	meta.SetStatusCondition(&v.Status.Conditions, metav1.Condition{
		Type:    vkov1.ConditionTypeStorageSpecNotApplied,
		Status:  metav1.ConditionTrue,
		Reason:  vkov1.ReasonRecreateRequired,
		Message: "raised by the data tier in this pass",
	})
	r, c := newTestReconciler(v, sentinelStsFor(v))

	require.NoError(t, r.reconcileSentinelStatefulSet(context.Background(), v))

	cond := findCondition(storedValkey(t, c, v), vkov1.ConditionTypeStorageSpecNotApplied)
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionTrue, cond.Status,
		"a tier whose builder writes no claims can never prove that the storage the spec "+
			"asks for is the storage that runs")
}
