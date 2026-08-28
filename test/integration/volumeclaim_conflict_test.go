//go:build integration

package integration

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/builder"
	"github.com/guided-traffic/valkey-operator/internal/common"
)

// The volumeClaimTemplates guard (docs/adr/0023-volume-claim-templates-are-immutable.md)
// makes two claims that a unit test cannot settle, and both of them are about a
// real API server:
//
//   - The comparison must not false-positive. The fake client stores what it is
//     given; a kube-apiserver defaults fields onto the stored volumeClaimTemplates
//     that the builder never sets, and the claim's labels freeze at creation time
//     while the builder restamps them from spec.image on every pass. A DeepEqual —
//     or a comparison that included labels — is green under the fake client and
//     reports a permanent conflict on every persistent cluster here.
//   - The refusal must be observable on the CR and must clear by itself. That is a
//     property of a pass driven by a real work queue, not of one function call.
//
// TestStatefulSetImmutability_Integration pins the premise underneath all of it,
// which is not the operator's behaviour at all but the API server's.
//
// No Event assertions: the shared reconciler in suite_test.go is built without a
// Recorder, so recordEvent returns before writing anything. The conditions are the
// durable half and the one this tier can see.

const (
	claimGuardInterval        = 250 * time.Millisecond
	claimGuardTimeout         = 60 * time.Second
	claimGuardRecoveryTimeout = 90 * time.Second

	// Two image strings that differ, which is the whole requirement for a fixture
	// image in this tier (ADR 0017 D43 — only e2e pulls one). They differ in the
	// tag on purpose: the tag is what common.ExtractVersionFromImage puts into the
	// app.kubernetes.io/version label the builder stamps on the claim.
	claimGuardBaseImage   = "valkey/valkey:8.0"
	claimGuardBaseVersion = "8.0"
	claimGuardNextImage   = "valkey/valkey:8.1"
)

// TestVolumeClaimTemplates_PersistentClusterKeepsConverging_Integration is the
// negative case, and the most valuable one: an ordinary persistent cluster that
// keeps taking spec changes must never see the storage guard.
//
// Why only this tier can prove it. The comparison runs against the volumeClaimTemplates
// as the API server stored them, and those are not the ones the builder produced:
// the apps/v1 defaulting fills in spec.volumeMode, and the claim keeps the labels
// it was created with — including app.kubernetes.io/version, which the builder
// re-derives from spec.image on every pass. Comparing labels, or comparing the
// whole claim, therefore reports a structural or parameter conflict on the first
// image bump of every persistent cluster in the fleet and never stops. The fake
// client defaults nothing and unit fixtures carry no drift, so a unit test agrees
// with either implementation.
//
// The test asserts both halves: that the drift is really present in the stored
// object, and that the operator kept converging anyway.
func TestVolumeClaimTemplates_PersistentClusterKeepsConverging_Integration(t *testing.T) {
	ctx := testCtx
	crName := "vct-persistent-test"
	key := types.NamespacedName{Name: crName, Namespace: "default"}

	v := claimGuardCR(crName, &vkov1.PersistenceSpec{
		Enabled: true,
		Mode:    vkov1.PersistenceModeRDB,
		Size:    resource.MustParse("1Gi"),
	})
	require.NoError(t, k8sClient.Create(ctx, v))
	t.Cleanup(func() { _ = k8sClient.Delete(ctx, v) })

	created := waitForStatefulSet(t, crName, claimGuardTimeout)
	require.Len(t, created.Spec.VolumeClaimTemplates, 1, "persistence is enabled, so the builder attaches one claim")

	// Precondition, and the reason this test is here: what the API server stored is
	// not what the builder handed it.
	stored := created.Spec.VolumeClaimTemplates[0]
	require.NotNil(t, stored.Spec.VolumeMode,
		"the API server defaults volumeMode onto the stored claim; a DeepEqual against the "+
			"builder's claim would report a conflict from the very first pass")
	assert.Equal(t, corev1.PersistentVolumeFilesystem, *stored.Spec.VolumeMode)
	assert.Equal(t, claimGuardBaseVersion, stored.Labels[common.LabelVersion],
		"the claim is stamped with the version of the image it was created with")

	t.Run("an image bump still reaches the StatefulSet", func(t *testing.T) {
		updateValkeyForClaimGuard(t, key, "bump the image", func(current *vkov1.Valkey) {
			current.Spec.Image = claimGuardNextImage
		})

		awaitClaimGuard(t, claimGuardTimeout,
			"the image change must reach the StatefulSet; a structural false positive would hold it",
			func(ctx context.Context) (bool, string) {
				sts := &appsv1.StatefulSet{}
				if err := k8sClient.Get(ctx, key, sts); err != nil {
					return false, err.Error()
				}
				got := sts.Spec.Template.Spec.Containers[0].Image
				return got == claimGuardNextImage, "container image " + got
			})

		sts := &appsv1.StatefulSet{}
		require.NoError(t, k8sClient.Get(ctx, key, sts))
		require.Len(t, sts.Spec.VolumeClaimTemplates, 1)

		// The drift the comparison must ignore, now demonstrably present: the pod
		// template runs the new image while the claim still carries the old
		// version label. A comparison that included labels fails from here on.
		assert.Equal(t, claimGuardBaseVersion, sts.Spec.VolumeClaimTemplates[0].Labels[common.LabelVersion],
			"the operator never writes volumeClaimTemplates, so the claim's version label stays "+
				"at the image the cluster was created with")
		assert.Equal(t, claimGuardNextImage, sts.Spec.Template.Spec.Containers[0].Image)
	})

	t.Run("a second spec change is applied too", func(t *testing.T) {
		// A second driver, so the assertion below covers more than one pass through
		// the guard against the drifted claim.
		updateValkeyForClaimGuard(t, key, "add a pod label", func(current *vkov1.Valkey) {
			current.Spec.PodLabels = map[string]string{"vct": "second-pass"}
		})

		awaitClaimGuard(t, claimGuardTimeout, "the pod label must reach the StatefulSet",
			func(ctx context.Context) (bool, string) {
				sts := &appsv1.StatefulSet{}
				if err := k8sClient.Get(ctx, key, sts); err != nil {
					return false, err.Error()
				}
				return sts.Spec.Template.Labels["vct"] == "second-pass",
					fmt.Sprintf("template labels %v", sts.Spec.Template.Labels)
			})
	})

	t.Run("the CR never carried the storage conditions", func(t *testing.T) {
		current := &vkov1.Valkey{}
		require.NoError(t, k8sClient.Get(ctx, key, current))

		// StorageSpecNotApplied is written from inside guardVolumeClaimTemplates,
		// before the StatefulSet update that the waits above observed. A parameter
		// false positive would therefore already be on the CR — and it is the one
		// shape that does not fail the pass, so nothing else would have caught it.
		storage := meta.FindStatusCondition(current.Status.Conditions, vkov1.ConditionTypeStorageSpecNotApplied)
		assert.Nil(t, storage,
			"a cluster whose storage was never touched must carry no storage condition at all, "+
				"got %s", describeClaimGuardCondition(storage))

		blocked := meta.FindStatusCondition(current.Status.Conditions, vkov1.ConditionTypeReconcileBlocked)
		if blocked != nil {
			assert.NotEqual(t, vkov1.ReasonRecreateRequired, blocked.Reason,
				"nothing about this cluster needs a StatefulSet recreation")
		}
		assert.NotEqual(t, vkov1.ValkeyPhaseError, current.Status.Phase,
			"status.phase is the only field with a print column; an image bump must not turn it red")
	})
}

// TestVolumeClaimTemplates_EnablingPersistenceIsRefusedAndRecovers_Integration is
// the structural conflict in the direction users actually take: a cluster that was
// created without persistence, later asked to have it.
//
// The refusal fails the step, so everything the same StatefulSet write would have
// carried is held with it. The CR update below therefore changes the image at the
// same time — that is the observable half of "replica, image and label changes are
// held", and it is what makes this test fail if the guard is removed: without it
// reconcileStatefulSet copies only replicas, template and labels onto the live
// object, leaving the live volumeClaimTemplates untouched, so the image would land
// on a StatefulSet whose pods mount a claim that does not exist.
//
// The second subtest is the half no unit test reaches: nothing watches for the
// spec being put back except the work queue and the generation bump, and the
// condition has to clear on its own.
func TestVolumeClaimTemplates_EnablingPersistenceIsRefusedAndRecovers_Integration(t *testing.T) {
	ctx := testCtx
	crName := "vct-enable-test"
	key := types.NamespacedName{Name: crName, Namespace: "default"}

	v := claimGuardCR(crName, nil)
	require.NoError(t, k8sClient.Create(ctx, v))
	t.Cleanup(func() { _ = k8sClient.Delete(ctx, v) })

	created := waitForStatefulSet(t, crName, claimGuardTimeout)
	require.Empty(t, created.Spec.VolumeClaimTemplates, "persistence is off, so there is no claim")
	require.True(t, claimGuardHasDataEmptyDir(created),
		"without persistence the builder backs the data mount with an emptyDir")

	updateValkeyForClaimGuard(t, key, "enable persistence and bump the image", func(current *vkov1.Valkey) {
		current.Spec.Image = claimGuardNextImage
		current.Spec.Persistence = &vkov1.PersistenceSpec{
			Enabled: true,
			Mode:    vkov1.PersistenceModeRDB,
			Size:    resource.MustParse("1Gi"),
		}
	})

	t.Run("the write is refused and reported on the CR", func(t *testing.T) {
		awaitClaimGuard(t, claimGuardTimeout,
			"the conflict is only actionable if the CR names it: ReconcileBlocked and "+
				"StorageSpecNotApplied, both with reason RecreateRequired",
			func(ctx context.Context) (bool, string) {
				current := &vkov1.Valkey{}
				if err := k8sClient.Get(ctx, key, current); err != nil {
					return false, err.Error()
				}
				blocked := meta.FindStatusCondition(current.Status.Conditions, vkov1.ConditionTypeReconcileBlocked)
				storage := meta.FindStatusCondition(current.Status.Conditions, vkov1.ConditionTypeStorageSpecNotApplied)
				ok := blocked != nil && blocked.Status == metav1.ConditionTrue &&
					blocked.Reason == vkov1.ReasonRecreateRequired &&
					storage != nil && storage.Status == metav1.ConditionTrue &&
					storage.Reason == vkov1.ReasonRecreateRequired
				return ok, fmt.Sprintf("%s, %s",
					describeClaimGuardCondition(blocked), describeClaimGuardCondition(storage))
			})

		current := &vkov1.Valkey{}
		require.NoError(t, k8sClient.Get(ctx, key, current))
		assert.Equal(t, vkov1.ValkeyPhaseError, current.Status.Phase,
			"a spec the operator cannot apply must not read OK in kubectl get")

		storage := meta.FindStatusCondition(current.Status.Conditions, vkov1.ConditionTypeStorageSpecNotApplied)
		require.NotNil(t, storage)
		assert.Contains(t, storage.Message, builder.DataVolumeName,
			"the message has to name the claim, or nobody can tell which storage is stuck")

		// The object itself: nothing was written to it.
		sts := &appsv1.StatefulSet{}
		require.NoError(t, k8sClient.Get(ctx, key, sts))
		assert.Empty(t, sts.Spec.VolumeClaimTemplates,
			"the operator never writes volumeClaimTemplates, so enabling persistence cannot "+
				"reach the live object")
		assert.True(t, claimGuardHasDataEmptyDir(sts),
			"the pod template still has to back the data mount; dropping the emptyDir while no "+
				"claim exists is a template whose pods cannot be created")
		assert.Equal(t, claimGuardBaseImage, sts.Spec.Template.Spec.Containers[0].Image,
			"the image rode along in the same StatefulSet write, so the refusal holds it too")
	})

	t.Run("putting the spec back releases the held update without an operator restart", func(t *testing.T) {
		updateValkeyForClaimGuard(t, key, "drop the persistence block again", func(current *vkov1.Valkey) {
			current.Spec.Persistence = nil
		})

		awaitClaimGuard(t, claimGuardRecoveryTimeout,
			"once the claims agree again the held image change has to land by itself",
			func(ctx context.Context) (bool, string) {
				sts := &appsv1.StatefulSet{}
				if err := k8sClient.Get(ctx, key, sts); err != nil {
					return false, err.Error()
				}
				got := sts.Spec.Template.Spec.Containers[0].Image
				return got == claimGuardNextImage, "container image " + got
			})

		awaitClaimGuard(t, claimGuardTimeout, "both conditions have to resolve themselves",
			func(ctx context.Context) (bool, string) {
				current := &vkov1.Valkey{}
				if err := k8sClient.Get(ctx, key, current); err != nil {
					return false, err.Error()
				}
				blocked := meta.FindStatusCondition(current.Status.Conditions, vkov1.ConditionTypeReconcileBlocked)
				storage := meta.FindStatusCondition(current.Status.Conditions, vkov1.ConditionTypeStorageSpecNotApplied)
				ok := blocked != nil && blocked.Status == metav1.ConditionFalse &&
					storage != nil && storage.Status == metav1.ConditionFalse &&
					storage.Reason == vkov1.ReasonStorageSpecApplied
				return ok, fmt.Sprintf("%s, %s",
					describeClaimGuardCondition(blocked), describeClaimGuardCondition(storage))
			})
	})
}

// TestVolumeClaimTemplates_ResizeIsReportedWithoutBlockingThePass_Integration is
// the other fail direction, and the one a unit test cannot make stick: a refusal
// that must NOT fail the pass.
//
// A size change reaches only the claim, so holding the StatefulSet write for it
// would wedge every other change travelling in the same spec edit — for a
// difference no write can ever settle. The CR update therefore carries a size the
// operator cannot apply and an image it can, and the test asserts the image
// arrives while the CR still says the storage did not.
//
// Why this tier: what the CR ends up saying is decided by two authorities in the
// same pass. guardVolumeClaimTemplates writes StorageSpecNotApplied and returns
// nil; setReconcileBlockedCondition and updateStatus then run over a pass that did
// not fail. Only a real manager driving a real pass shows what the two agree on.
func TestVolumeClaimTemplates_ResizeIsReportedWithoutBlockingThePass_Integration(t *testing.T) {
	ctx := testCtx
	crName := "vct-resize-test"
	key := types.NamespacedName{Name: crName, Namespace: "default"}

	v := claimGuardCR(crName, &vkov1.PersistenceSpec{
		Enabled: true,
		Mode:    vkov1.PersistenceModeRDB,
		Size:    resource.MustParse("1Gi"),
	})
	require.NoError(t, k8sClient.Create(ctx, v))
	t.Cleanup(func() { _ = k8sClient.Delete(ctx, v) })

	created := waitForStatefulSet(t, crName, claimGuardTimeout)
	require.Len(t, created.Spec.VolumeClaimTemplates, 1)
	requested := created.Spec.VolumeClaimTemplates[0].Spec.Resources.Requests[corev1.ResourceStorage]
	require.Equal(t, "1Gi", requested.String())

	updateValkeyForClaimGuard(t, key, "grow the volume and bump the image", func(current *vkov1.Valkey) {
		current.Spec.Image = claimGuardNextImage
		current.Spec.Persistence.Size = resource.MustParse("2Gi")
	})

	awaitClaimGuard(t, claimGuardTimeout,
		"the image travelled in the same spec edit as the resize and must still be applied",
		func(ctx context.Context) (bool, string) {
			sts := &appsv1.StatefulSet{}
			if err := k8sClient.Get(ctx, key, sts); err != nil {
				return false, err.Error()
			}
			got := sts.Spec.Template.Spec.Containers[0].Image
			return got == claimGuardNextImage, "container image " + got
		})

	awaitClaimGuard(t, claimGuardTimeout,
		"the storage that will not arrive has to be on the CR: "+
			"StorageSpecNotApplied=True/VolumeClaimTemplatesImmutable",
		func(ctx context.Context) (bool, string) {
			current := &vkov1.Valkey{}
			if err := k8sClient.Get(ctx, key, current); err != nil {
				return false, err.Error()
			}
			storage := meta.FindStatusCondition(current.Status.Conditions, vkov1.ConditionTypeStorageSpecNotApplied)
			ok := storage != nil && storage.Status == metav1.ConditionTrue &&
				storage.Reason == vkov1.ReasonVolumeClaimTemplatesImmutable
			return ok, describeClaimGuardCondition(storage)
		})

	current := &vkov1.Valkey{}
	require.NoError(t, k8sClient.Get(ctx, key, current))

	storage := meta.FindStatusCondition(current.Status.Conditions, vkov1.ConditionTypeStorageSpecNotApplied)
	require.NotNil(t, storage)
	assert.Contains(t, storage.Message, "2Gi", "the message names what was asked for")
	assert.Contains(t, storage.Message, "1Gi", "and what is in use")

	blocked := meta.FindStatusCondition(current.Status.Conditions, vkov1.ConditionTypeReconcileBlocked)
	if blocked != nil {
		assert.Equal(t, metav1.ConditionFalse, blocked.Status,
			"a storage parameter nobody can apply is not a blocked reconcile: everything else was applied")
	}
	assert.NotEqual(t, vkov1.ValkeyPhaseError, current.Status.Phase,
		"the cluster runs exactly as before, on the volume it already had")

	sts := &appsv1.StatefulSet{}
	require.NoError(t, k8sClient.Get(ctx, key, sts))
	require.Len(t, sts.Spec.VolumeClaimTemplates, 1)
	inUse := sts.Spec.VolumeClaimTemplates[0].Spec.Resources.Requests[corev1.ResourceStorage]
	assert.Equal(t, "1Gi", inUse.String(),
		"the live claim keeps the size it was created with; the operator promises nothing else")
}

// TestStatefulSetImmutability_Integration pins the premise the whole guard rests
// on, and it is not a claim about this operator: it is a claim about the API
// server, so this is the only tier that can make it.
//
// Both directions matter and they differ, which is exactly why the guard cannot be
// left to the API server:
//
//   - Writing volumeClaimTemplates is rejected. Whitelist violation, one error that
//     names the whitelist and not the field.
//   - Writing only the pod template is accepted — including a pod template that
//     adds an emptyDir under the name of an existing claim. That is the shape a
//     persistence=false edit produces, because reconcileStatefulSet copies replicas,
//     template and labels and never touches the claims. Nothing rejects it, the
//     object ends up carrying both, and the statefulset-controller keeps generating
//     pods backed by the claim. The refusal is the only thing standing there.
//
// The StatefulSets here carry no ownerReference to any Valkey, so no Owns() watch
// maps them to a reconcile request and the operator never touches them.
//
// This is a behaviour-pinning test and it passes with the guard removed (ADR 0017
// D8): it asserts nothing about this operator. What it protects against is the
// opposite direction — a future API server that relaxes either half would leave
// the guard reasoning from a premise that no longer holds, and this is the only
// place that would go red.
func TestStatefulSetImmutability_Integration(t *testing.T) {
	ctx := testCtx

	t.Run("adding a volumeClaimTemplate to an existing StatefulSet is rejected", func(t *testing.T) {
		sts := createClaimGuardFixture(t, "vct-premise-add", nil)

		doomed := sts.DeepCopy()
		doomed.Spec.VolumeClaimTemplates = []corev1.PersistentVolumeClaim{claimGuardFixtureClaim()}

		err := k8sClient.Update(ctx, doomed)
		require.Error(t, err,
			"if this ever succeeds, enabling persistence in place is possible and the guard is wrong")
		assert.True(t, apierrors.IsInvalid(err), "expected an Invalid error, got %T: %v", err, err)
		assert.Contains(t, err.Error(), "updates to statefulset spec for fields other than",
			"the API server names the whitelist and not the field, which is why the operator "+
				"has to diagnose the difference itself")

		// Positive control: the same object takes a whitelisted update, so the
		// rejection above is about the claims and not about the object.
		allowed := sts.DeepCopy()
		allowed.Spec.Template.Spec.Containers[0].Image = "registry.example/app:2"
		require.NoError(t, k8sClient.Update(ctx, allowed),
			"a pod-template change is inside the whitelist")
	})

	t.Run("shadowing an existing claim with an emptyDir is accepted", func(t *testing.T) {
		sts := createClaimGuardFixture(t, "vct-premise-shadow",
			[]corev1.PersistentVolumeClaim{claimGuardFixtureClaim()})
		require.Len(t, sts.Spec.VolumeClaimTemplates, 1)

		// Exactly what reconcileStatefulSet would submit for persistence=false: the
		// template gains the emptyDir, the claims are left as they are.
		disabled := sts.DeepCopy()
		disabled.Spec.Template.Spec.Volumes = append(disabled.Spec.Template.Spec.Volumes, corev1.Volume{
			Name:         builder.DataVolumeName,
			VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
		})
		require.NoError(t, k8sClient.Update(ctx, disabled),
			"this is the dangerous direction precisely because the API server does not stop it")

		// Asserted on the Update response rather than on a read: that response is
		// the stored object, and reading it back would go through the manager cache
		// and race the write.
		assert.Len(t, disabled.Spec.VolumeClaimTemplates, 1,
			"the claim survives a write that never mentioned it")
		assert.True(t, claimGuardHasDataEmptyDir(disabled),
			"and the object now carries an emptyDir under the same name")
	})
}

// claimGuardCR builds a single-replica Valkey on the base image, with an optional
// persistence block. Sentinel stays off, and the reason has been narrowed since this
// was written: what this tier verifies is what the API SERVER decides about an
// immutable volumeClaimTemplates field, which is the same on either topology.
//
// The original reason -- "a Sentinel tier would only add objects to wait for" --
// read as if the topology were irrelevant to the guard, and it was not: the
// Sentinel StatefulSet reconciler is a second evaluator of StorageSpecNotApplied,
// and until 2026-08-26 it cleared what the data tier had just reported, on every
// pass (ADR 0023 D4a). That defect lived entirely on the Sentinel topology and this
// file could not see it. What now covers it is the unit tier, where step order --
// an operator decision, not an API server one -- belongs (ADR 0017).
func claimGuardCR(name string, persistence *vkov1.PersistenceSpec) *vkov1.Valkey {
	return &vkov1.Valkey{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: vkov1.ValkeySpec{
			Replicas:    1,
			Image:       claimGuardBaseImage,
			Persistence: persistence,
		},
	}
}

// createClaimGuardFixture creates a StatefulSet owned by nobody, so the operator
// leaves it alone, and returns it as the API server stored it — including the
// resourceVersion, so the caller can submit updates without a cache read.
func createClaimGuardFixture(t *testing.T, name string, claims []corev1.PersistentVolumeClaim) *appsv1.StatefulSet {
	t.Helper()

	replicas := int32(1)
	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: appsv1.StatefulSetSpec{
			Replicas:    &replicas,
			ServiceName: name,
			Selector:    &metav1.LabelSelector{MatchLabels: map[string]string{"app": name}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": name}},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  "app",
						Image: "registry.example/app:1",
					}},
				},
			},
			VolumeClaimTemplates: claims,
		},
	}
	require.NoError(t, k8sClient.Create(testCtx, sts))
	t.Cleanup(func() { _ = k8sClient.Delete(testCtx, sts) })
	return sts
}

// claimGuardFixtureClaim builds the minimal claim template the API server accepts,
// under the name the operator uses.
func claimGuardFixtureClaim() corev1.PersistentVolumeClaim {
	return corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: builder.DataVolumeName},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("1Gi")},
			},
		},
	}
}

// claimGuardHasDataEmptyDir reports whether the pod template backs the data mount
// with an emptyDir, which is what a cluster without persistence looks like.
func claimGuardHasDataEmptyDir(sts *appsv1.StatefulSet) bool {
	for _, vol := range sts.Spec.Template.Spec.Volumes {
		if vol.Name == builder.DataVolumeName && vol.EmptyDir != nil {
			return true
		}
	}
	return false
}

// updateValkeyForClaimGuard applies mutate to the CR and writes it back, retrying
// the conflict a concurrent status write of the running operator produces.
func updateValkeyForClaimGuard(t *testing.T, key types.NamespacedName, what string, mutate func(*vkov1.Valkey)) {
	t.Helper()

	var last error
	err := wait.PollUntilContextTimeout(testCtx, claimGuardInterval, claimGuardTimeout, true,
		func(ctx context.Context) (bool, error) {
			current := &vkov1.Valkey{}
			if last = k8sClient.Get(ctx, key, current); last != nil {
				return false, nil
			}
			mutate(current)
			last = k8sClient.Update(ctx, current)
			return last == nil, nil
		})
	require.NoErrorf(t, err, "could not %s; last error: %v", what, last)
}

// awaitClaimGuard polls until cond holds and reports the last value cond observed
// when the budget runs out, so a failure names the state the operator was actually
// in rather than only the state it should have reached (ADR 0017 D25). It uses
// wait.PollUntilContextTimeout rather than the require.Eventually of the older
// files in this package for the reason D25 gives: an Eventually condition
// goroutine can outlive the test and touch a finished *testing.T.
func awaitClaimGuard(t *testing.T, timeout time.Duration, what string, cond func(ctx context.Context) (bool, string)) {
	t.Helper()

	var last string
	err := wait.PollUntilContextTimeout(testCtx, claimGuardInterval, timeout, true,
		func(ctx context.Context) (bool, error) {
			ok, observed := cond(ctx)
			last = observed
			return ok, nil
		})
	require.NoErrorf(t, err, "%s; last observed: %s", what, last)
}

// describeClaimGuardCondition renders a condition for a failure message, including
// the case that matters most here: the condition is not there at all.
func describeClaimGuardCondition(c *metav1.Condition) string {
	if c == nil {
		return "<absent>"
	}
	return fmt.Sprintf("%s=%s/%s", c.Type, c.Status, c.Reason)
}
