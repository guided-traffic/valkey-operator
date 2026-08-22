package controller

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/builder"
	"github.com/guided-traffic/valkey-operator/internal/common"
)

// The write half of docs/adr/0020-write-only-what-the-operator-owns.md: a generated
// name held by an object this Valkey does not control is left alone, and the sidecar
// grant is refused rather than handed to whatever identity holds the name.

// foreignServiceAccount returns a ServiceAccount under name that carries no
// ownerReference to any Valkey, plus the metadata a real one would have — the
// annotations are the point, because assigning the whole map instead of merging it
// is what used to erase an IRSA or Workload-Identity binding.
func foreignServiceAccount(name string) *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   "default",
			Labels:      map[string]string{"owner": "someone-else"},
			Annotations: map[string]string{"eks.amazonaws.com/role-arn": "arn:aws:iam::1:role/theirs"},
		},
	}
}

// observerEnabled switches the observer on, which is what gates its ServiceAccount
// and Deployment.
func observerEnabled(v *vkov1.Valkey) { v.Spec.Observer = &vkov1.ObserverSpec{Enabled: true} }

// passCtx returns a context carrying per-pass state, the way Reconcile builds it,
// plus the state so a test can read what the pass asked for.
func passCtx() (context.Context, *passState) {
	state := &passState{}
	return withPassState(context.Background(), state), state
}

// --- observer ServiceAccount: refuse, report, keep going ---

func TestReconcileObserverServiceAccount_LeavesAForeignServiceAccountUntouched(t *testing.T) {
	v := newTestValkey("test", "default", observerEnabled)
	foreign := foreignServiceAccount(builder.ObserverServiceAccountName(v))
	r, c := newReconcilerWithInterceptor("1.0.0", interceptor.Funcs{}, v, foreign)
	rec := &fakeEventRecorder{}
	r.Recorder = rec

	ctx, state := passCtx()
	require.NoError(t, r.reconcileObserverServiceAccount(ctx, v),
		"a refusal must not fail the step: the observer Deployment is still written")

	got := &corev1.ServiceAccount{}
	require.NoError(t, c.Get(context.Background(),
		types.NamespacedName{Name: foreign.Name, Namespace: "default"}, got))
	assert.Equal(t, foreign.Labels, got.Labels, "a foreign ServiceAccount keeps its labels")
	assert.Equal(t, foreign.Annotations, got.Annotations,
		"erasing these breaks whatever else runs under that identity")
	assert.Empty(t, got.OwnerReferences, "the operator must not take ownership of it either")

	require.Len(t, rec.withReason(reasonObserverServiceAccountNotOwned), 1,
		"the collision is only findable if it is reported on the CR")
	assert.Equal(t, foreignObjectRecheckInterval, state.interval(),
		"nothing else re-enters Reconcile once the administrator removes the collision")
}

func TestReconcileObserver_StillWritesTheDeploymentWhenTheServiceAccountIsForeign(t *testing.T) {
	// The observer gains nothing from a foreign identity — it mounts no token and
	// makes no API call — so a name collision must not take the diagnostic
	// component down with it (ADR 0020 D2).
	v := newTestValkey("test", "default", observerEnabled)
	foreign := foreignServiceAccount(builder.ObserverServiceAccountName(v))
	r, c := newReconcilerWithInterceptor("1.0.0", interceptor.Funcs{}, v, foreign)
	r.Recorder = &fakeEventRecorder{}

	ctx, _ := passCtx()
	require.NoError(t, r.reconcileObserver(ctx, v))

	deploy := &appsv1.Deployment{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{
		Name: builder.ObserverDeploymentName(v), Namespace: "default",
	}, deploy), "the observer Deployment must still be created")
}

func TestReconcileObserverServiceAccount_MergesAnnotationsOnAnOwnedServiceAccount(t *testing.T) {
	// The case no ownership guard covers: the ServiceAccount is ours, and a second
	// writer annotated it (ADR 0020 D4).
	v := newTestValkey("test", "default", observerEnabled)
	owned := builder.BuildObserverServiceAccount(v)
	owned.Labels = map[string]string{"stale": "yes"}
	owned.Annotations = map[string]string{"iam.gke.io/gcp-service-account": "svc@project.iam"}
	ownedByValkey(t, v, owned)

	r, c := newReconcilerWithInterceptor("1.0.0", interceptor.Funcs{}, v, owned)

	ctx, _ := passCtx()
	require.NoError(t, r.reconcileObserverServiceAccount(ctx, v))

	got := &corev1.ServiceAccount{}
	require.NoError(t, c.Get(context.Background(),
		types.NamespacedName{Name: owned.Name, Namespace: "default"}, got))
	assert.Equal(t, builder.ObserverLabels(v), got.Labels, "labels are operator-owned and are replaced")
	assert.Equal(t, "svc@project.iam", got.Annotations["iam.gke.io/gcp-service-account"],
		"a foreign annotation on an owned ServiceAccount must survive the update")
	assert.Equal(t, "1.0.0", got.Annotations[builder.AnnotationOperatorVersion],
		"the operator-owned annotation is still written")
}

// --- sidecar: the grant follows the name, so a refusal has to reach the binding ---

func TestReconcileSidecarRBAC_WritesNoGrantWhenTheServiceAccountIsForeign(t *testing.T) {
	v := newTestValkey("test", "default")
	name := builder.SidecarServiceAccountName(v)
	foreign := foreignServiceAccount(name)
	r, c := newReconcilerWithInterceptor("1.0.0", interceptor.Funcs{}, v, foreign)
	rec := &fakeEventRecorder{}
	r.Recorder = rec

	err := r.reconcileSidecarRBAC(context.Background(), v)

	require.Error(t, err, "the cluster cannot work without the grant, so the pass must report it")
	assert.ErrorIs(t, err, errForeignObject)

	roleErr := c.Get(context.Background(), types.NamespacedName{Name: name, Namespace: "default"}, &rbacv1.Role{})
	assert.True(t, apierrors.IsNotFound(roleErr), "no Role for a ServiceAccount we do not own")
	bindErr := c.Get(context.Background(), types.NamespacedName{Name: name, Namespace: "default"}, &rbacv1.RoleBinding{})
	assert.True(t, apierrors.IsNotFound(bindErr),
		"the RoleBinding names the ServiceAccount by name, so writing it would grant pods/patch to a stranger")

	got := &corev1.ServiceAccount{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: name, Namespace: "default"}, got))
	assert.Equal(t, foreign.Annotations, got.Annotations)
	require.Len(t, rec.withReason(reasonSidecarServiceAccountNotOwned), 1)
}

func TestReconcileSidecarRBAC_WritesNoBindingWhenTheRoleIsForeign(t *testing.T) {
	// The other direction: RoleRef names the Role, so binding our ServiceAccount to
	// a Role the operator did not write grants the sidecar whatever it carries.
	v := newTestValkey("test", "default")
	name := builder.SidecarServiceAccountName(v)
	foreignRole := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Rules: []rbacv1.PolicyRule{{
			APIGroups: []string{""}, Resources: []string{"secrets"}, Verbs: []string{"get", "list"},
		}},
	}
	r, c := newReconcilerWithInterceptor("1.0.0", interceptor.Funcs{}, v, foreignRole)
	rec := &fakeEventRecorder{}
	r.Recorder = rec

	err := r.reconcileSidecarRBAC(context.Background(), v)

	require.Error(t, err)
	assert.ErrorIs(t, err, errForeignObject)

	gotRole := &rbacv1.Role{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: name, Namespace: "default"}, gotRole))
	assert.Equal(t, foreignRole.Rules, gotRole.Rules, "a foreign Role keeps its rules")
	bindErr := c.Get(context.Background(), types.NamespacedName{Name: name, Namespace: "default"}, &rbacv1.RoleBinding{})
	assert.True(t, apierrors.IsNotFound(bindErr))
	require.Len(t, rec.withReason(reasonSidecarRoleNotOwned), 1)
}

func TestReconcileSidecarRoleBinding_LeavesAForeignRoleBindingAlone(t *testing.T) {
	// RoleRef is immutable, so a hand-written binding under this name points at a
	// different Role by construction — which used to make the delete-and-recreate
	// path fire on it every pass. ADR 0006 carried this as an open residual.
	v := newTestValkey("test", "default")
	name := builder.SidecarServiceAccountName(v)
	foreign := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io", Kind: "ClusterRole", Name: "cluster-admin",
		},
	}
	deletes := 0
	funcs := interceptor.Funcs{
		Delete: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
			deletes++
			return cl.Delete(ctx, obj, opts...)
		},
	}
	r, c := newReconcilerWithInterceptor("1.0.0", funcs, v, foreign)
	rec := &fakeEventRecorder{}
	r.Recorder = rec

	err := r.reconcileSidecarRoleBinding(context.Background(), v)

	require.Error(t, err)
	assert.ErrorIs(t, err, errForeignObject)
	assert.Zero(t, deletes, "a RoleBinding the operator did not create must not be deleted")
	got := &rbacv1.RoleBinding{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: name, Namespace: "default"}, got))
	assert.Equal(t, "cluster-admin", got.RoleRef.Name)
	require.Len(t, rec.withReason(reasonSidecarRoleBindingNotOwned), 1)
}

func TestReconcileSidecarRoleBinding_RecreateCarriesTheUIDPrecondition(t *testing.T) {
	// The ownership decision above is made on a cache-backed read, so the name can
	// hold a different object by the time the Delete lands (ADR 0006 D8, D9).
	v := newTestValkey("test", "default")
	stale := staleRoleRefBinding(t, v)
	stale.UID = types.UID("the-binding-we-inspected")

	var seen *client.DeleteOptions
	funcs := interceptor.Funcs{
		Delete: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
			seen = &client.DeleteOptions{}
			seen.ApplyOptions(opts)
			return cl.Delete(ctx, obj, opts...)
		},
	}
	r, _ := newReconcilerWithInterceptor("1.0.0", funcs, v, stale)

	require.NoError(t, r.reconcileSidecarRoleBinding(context.Background(), v))

	require.NotNil(t, seen, "the RoleRef change must take the delete-and-recreate path")
	require.NotNil(t, seen.Preconditions, "a name collision passes a bare delete perfectly")
	require.NotNil(t, seen.Preconditions.UID)
	assert.Equal(t, stale.UID, *seen.Preconditions.UID)
	assert.Nil(t, seen.Preconditions.ResourceVersion,
		"a changed ResourceVersion is still the same object; only a changed UID is a different one")
}

func TestReconcileSidecarRoleBinding_RecreateConflictIsTheGuardWorking(t *testing.T) {
	// The name holds a different object than the one this pass inspected, so there
	// is nothing of ours to replace and the pass is not failed over it (ADR 0006 D10).
	v := newTestValkey("test", "default")
	stale := staleRoleRefBinding(t, v)
	creates := 0
	funcs := interceptor.Funcs{
		Delete: func(_ context.Context, _ client.WithWatch, _ client.Object, _ ...client.DeleteOption) error {
			return apierrors.NewConflict(
				rbacv1.Resource("rolebindings"), stale.Name, assert.AnError)
		},
		Create: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			creates++
			return cl.Create(ctx, obj, opts...)
		},
	}
	r, _ := newReconcilerWithInterceptor("1.0.0", funcs, v, stale)

	require.NoError(t, r.reconcileSidecarRoleBinding(context.Background(), v))
	assert.Zero(t, creates, "recreating after a lost precondition would write over the replacement")
}

// --- the refusal reaches the CR ---

func TestReconcileBlockedReason_ForeignObjectOutranksAnAdmissionRejection(t *testing.T) {
	admission := internalErr("failed calling webhook \"mutate.kyverno.svc-fail\"")

	assert.Equal(t, vkov1.ReasonAdmissionWebhookDenied, reconcileBlockedReason(admission))
	assert.Equal(t, vkov1.ReasonForeignObject,
		reconcileBlockedReason(foreignObjectError("sidecar ServiceAccount", "test-sidecar")))
	assert.Equal(t, vkov1.ReasonForeignObject,
		reconcileBlockedReason(errors.Join(admission, foreignObjectError("sidecar Role", "test-sidecar"))),
		"the admission gate reopens on its own; a name collision needs a human")
	assert.Equal(t, vkov1.ReasonWriteFailed, reconcileBlockedReason(internalErr("quota exceeded")))
}

// --- the recheck cadence ---

func TestApplyRecheck_NeverLengthensARequeueThePassAlreadyAskedFor(t *testing.T) {
	tests := []struct {
		name    string
		result  ctrl.Result
		recheck time.Duration
		want    time.Duration
	}{
		{"no request leaves the result alone", ctrl.Result{RequeueAfter: 5 * time.Second}, 0, 5 * time.Second},
		{"no requeue yet takes the request", ctrl.Result{}, 30 * time.Second, 30 * time.Second},
		{"the shorter of the two wins", ctrl.Result{RequeueAfter: 5 * time.Second}, 30 * time.Second, 5 * time.Second},
		{"a shorter request tightens it", ctrl.Result{RequeueAfter: time.Minute}, 30 * time.Second, 30 * time.Second},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, applyRecheck(tc.result, tc.recheck).RequeueAfter)
		})
	}
}

func TestPassState_KeepsTheShortestRequest(t *testing.T) {
	state := &passState{}
	assert.Zero(t, state.interval())
	state.requestRecheck(time.Minute)
	state.requestRecheck(0)
	state.requestRecheck(30 * time.Second)
	state.requestRecheck(time.Hour)
	assert.Equal(t, 30*time.Second, state.interval())
}

func TestRequestRecheck_IsANoOpWithoutPassState(t *testing.T) {
	// Unit tests call a single step directly, without the state Reconcile builds.
	assert.NotPanics(t, func() { requestRecheck(context.Background(), time.Second) })
}

// --- StatefulSets and observer Deployment: the NA61 half of ADR 0020 ---

// foreignStatefulSet returns a StatefulSet under name that no Valkey controls,
// with its own selector and workload — the shape of a pre-existing application
// that happens to hold the generated name.
func foreignStatefulSet(name string) *appsv1.StatefulSet {
	replicas := int32(1)
	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
			Labels:    map[string]string{"owner": "someone-else"},
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "theirs"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "theirs"}},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "app", Image: "registry.example/theirs:1"}},
				},
			},
		},
	}
}

func TestReconcileStatefulSet_RefusesAForeignStatefulSet(t *testing.T) {
	v := newTestValkey("test", "default")
	foreign := foreignStatefulSet(v.Name)
	r, c := newReconcilerWithInterceptor("1.0.0", interceptor.Funcs{}, v, foreign)
	rec := &fakeEventRecorder{}
	r.Recorder = rec

	err := r.reconcileStatefulSet(context.Background(), v)

	require.Error(t, err, "without the data StatefulSet the CR cannot do its job (ADR 0020 D2)")
	assert.ErrorIs(t, err, errForeignObject)

	got := &appsv1.StatefulSet{}
	require.NoError(t, c.Get(context.Background(),
		types.NamespacedName{Name: v.Name, Namespace: "default"}, got))
	assert.Equal(t, "registry.example/theirs:1", got.Spec.Template.Spec.Containers[0].Image,
		"writing the pod template onto a foreign StatefulSet replaces its workload")
	assert.Equal(t, foreign.Labels, got.Labels, "a foreign StatefulSet keeps its labels")
	assert.Empty(t, got.OwnerReferences, "the operator must not take ownership of it either")
	require.Len(t, rec.withReason(reasonStatefulSetNotOwned), 1)
}

func TestReconcileSentinelStatefulSet_RefusesAForeignStatefulSet(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
	})
	foreign := foreignStatefulSet(common.StatefulSetName(v, common.ComponentSentinel))
	r, c := newReconcilerWithInterceptor("1.0.0", interceptor.Funcs{}, v, foreign)
	rec := &fakeEventRecorder{}
	r.Recorder = rec

	err := r.reconcileSentinelStatefulSet(context.Background(), v)

	require.Error(t, err)
	assert.ErrorIs(t, err, errForeignObject)

	got := &appsv1.StatefulSet{}
	require.NoError(t, c.Get(context.Background(),
		types.NamespacedName{Name: foreign.Name, Namespace: "default"}, got))
	assert.Equal(t, "registry.example/theirs:1", got.Spec.Template.Spec.Containers[0].Image)
	require.Len(t, rec.withReason(reasonSentinelStatefulSetNotOwned), 1)
}

func TestReconcileObserverDeployment_LeavesAForeignDeploymentUntouched(t *testing.T) {
	// The observer is diagnostic: refusing it must not fail the step, but it must
	// ask for the recheck that brings the operator back (ADR 0020 D2, D6).
	v := newTestValkey("test", "default", observerEnabled)
	foreign := builder.BuildObserverDeployment(v, "registry.example/theirs:1")
	foreign.Labels = map[string]string{"owner": "someone-else"}
	r, c := newReconcilerWithInterceptor("1.0.0", interceptor.Funcs{}, v, foreign)
	rec := &fakeEventRecorder{}
	r.Recorder = rec

	ctx, state := passCtx()
	require.NoError(t, r.reconcileObserverDeployment(ctx, v))

	got := &appsv1.Deployment{}
	require.NoError(t, c.Get(context.Background(),
		types.NamespacedName{Name: foreign.Name, Namespace: "default"}, got))
	assert.Equal(t, foreign.Labels, got.Labels, "a foreign Deployment keeps its labels")
	assert.Empty(t, got.OwnerReferences)
	require.Len(t, rec.withReason(reasonObserverDeploymentNotOwned), 1)
	assert.Equal(t, foreignObjectRecheckInterval, state.interval())
}

func TestCleanupObserverDeployment_LeavesAForeignDeploymentAlone(t *testing.T) {
	// Refusing to write a foreign Deployment while still deleting it on disable
	// would be the guard's absurd mirror image (ADR 0006).
	v := newTestValkey("test", "default") // observer disabled: the cleanup path
	foreign := builder.BuildObserverDeployment(v, "registry.example/theirs:1")
	r, c := newReconcilerWithInterceptor("1.0.0", interceptor.Funcs{}, v, foreign)

	require.NoError(t, r.cleanupObserverDeployment(context.Background(), v))

	err := c.Get(context.Background(),
		types.NamespacedName{Name: foreign.Name, Namespace: "default"}, &appsv1.Deployment{})
	assert.NoError(t, err, "a foreign Deployment under the generated name must survive the cleanup")
}

func TestNudgeShortStatefulSets_DoesNotNudgeAForeignStatefulSet(t *testing.T) {
	// The nudge patch is a write; a StatefulSet the operator does not own is
	// treated as absent, exactly like NotFound.
	v := newSentinelValkey()
	sts := newNudgeStatefulSet(v, common.StatefulSetName(v, common.ComponentValkey), 0)
	sts.OwnerReferences = nil
	r, c := newTestReconciler(v, sts)

	pastGrace(r, nudgeKey(sts.Name))

	short := r.nudgeShortStatefulSets(context.Background(), v)

	assert.Empty(t, nudgeAnnotation(t, c, sts.Name),
		"a foreign StatefulSet must not be patched, however short it is")
	assert.False(t, short, "a foreign StatefulSet is treated as absent, not as short")
	assert.False(t, nudgeTracked(r, sts.Name), "and it must not keep a tracker entry")
}

func TestCheckAndHandleRollingUpdate_TreatsAForeignStatefulSetAsAbsent(t *testing.T) {
	// The rolling update deletes pods against the persisted template. When the
	// template is not ours, neither are the pods.
	old := newTestValkey("test", "default")
	oldSts := stsForValkey(old)
	pod0 := podFromStsTemplate(old, oldSts, 0)

	desired := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Image = "valkey/valkey:9.0"
	})
	newSts := stsForValkey(desired)
	newSts.OwnerReferences = nil // replaced under its name by something foreign

	r, c := newTestReconciler(desired, newSts, pod0)

	result := r.checkAndHandleRollingUpdate(context.Background(), desired)

	assert.Nil(t, result.Error)
	assert.False(t, result.NeedsRequeue)
	assert.True(t, podExists(t, c, pod0.Name),
		"pods of a foreign StatefulSet are not ours to delete")
}

func TestCheckAndHandleSentinelRollingUpdate_TreatsAForeignStatefulSetAsAbsent(t *testing.T) {
	v := newTestValkey("ha", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
	})
	sts := buildTestSentinelSts(v)
	sts.OwnerReferences = nil
	const oldImg = "valkey/valkey:8.0"
	p0 := createSentinelPod(v, 0, oldImg, true)
	p1 := createSentinelPod(v, 1, oldImg, true)
	p2 := createSentinelPod(v, 2, oldImg, true)

	r, c := newTestReconciler(v, sts, p0, p1, p2)

	result := r.checkAndHandleSentinelRollingUpdate(context.Background(), v)

	assert.Nil(t, result.Error)
	assert.False(t, result.NeedsRequeue)
	for i := 0; i < 3; i++ {
		name := fmt.Sprintf("%s-%d", sts.Name, i)
		assert.True(t, podExists(t, c, name), "no sentinel pod of a foreign StatefulSet may be deleted")
	}
}

func TestSentinelRolloutComplete_TreatsAForeignStatefulSetAsAbsent(t *testing.T) {
	v := newTestValkeyUnified()
	stsName := common.StatefulSetName(v, common.ComponentSentinel)
	sts := stagedSentinelStatefulSet(v, stsName, builder.ValkeyTLSSecretName(v))
	sts.OwnerReferences = nil
	r, _ := newTestReconciler(v, sts)

	ready, err := r.sentinelRolloutComplete(context.Background(), v)

	require.NoError(t, err)
	assert.True(t, ready, "absent means trivially complete: no pod of ours is bound to the legacy Secret")
}

func TestUpdateStatus_TreatsAForeignStatefulSetAsAbsent(t *testing.T) {
	// Its replica counts describe someone else's workload; the CR must not report
	// readiness from them.
	v := newTestValkey("test", "default")
	foreign := foreignStatefulSet(v.Name)
	foreign.Status.Replicas = 1
	foreign.Status.ReadyReplicas = 1
	r, _ := newTestReconciler(v, foreign)

	require.NoError(t, r.updateStatus(context.Background(), v))

	assert.Equal(t, vkov1.ValkeyPhaseProvisioning, v.Status.Phase,
		"a foreign StatefulSet reads as absent, not as a ready cluster")
	assert.Zero(t, v.Status.ReadyReplicas)
}

func TestReconcileResources_ForeignDataStatefulSetFailsThePass(t *testing.T) {
	// End to end through the step runner: the refusal must survive the step
	// wrapping so the ReconcileBlocked reason comes out as ForeignObject.
	v := newTestValkey("test", "default")
	foreign := foreignStatefulSet(v.Name)
	r, _ := newTestReconciler(v, foreign)
	rec := &fakeEventRecorder{}
	r.Recorder = rec

	err := r.reconcileResources(context.Background(), v)

	require.Error(t, err)
	assert.ErrorIs(t, err, errForeignObject)
	assert.Equal(t, vkov1.ReasonForeignObject, reconcileBlockedReason(err))
}

// --- Services, ConfigMaps, NetworkPolicies, ServiceMonitor and Certificate ---
//
// The kinds D7 used to leave out. The two unstructured ones are the sharpest: they
// wrote this CR's ownerReference onto whatever object held the name, with Controller
// and BlockOwnerDeletion set, so deleting the CR handed a foreign object to the
// garbage collector. Every assertion below that reads OwnerReferences on a refused
// object is that half of the rule.

// foreignService returns a Service under name that no Valkey controls, selecting
// its own pods — the selector is the field a takeover would rewrite, and it is
// mutable, so unlike the StatefulSet nothing upstream would have rejected the write.
func foreignService(name string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
			Labels:    map[string]string{"owner": "someone-else"},
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": "theirs"},
			Ports:    []corev1.ServicePort{{Name: "http", Port: 8080}},
		},
	}
}

// foreignConfigMap returns a ConfigMap under name that no Valkey controls.
func foreignConfigMap(name string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
			Labels:    map[string]string{"owner": "someone-else"},
		},
		Data: map[string]string{"theirs.conf": "# not ours\n"},
	}
}

// foreignCertificate returns a cert-manager Certificate under name that no Valkey
// controls. spec.secretName is the field that matters: writing ours onto it makes
// cert-manager maintain this cluster's Secret and abandon theirs, without waiting
// for any CR deletion.
func foreignCertificate(name string) *unstructured.Unstructured {
	c := newEmptyCert()
	c.SetName(name)
	c.SetNamespace("default")
	c.SetLabels(map[string]string{"owner": "someone-else"})
	c.Object["spec"] = map[string]interface{}{
		"secretName": "their-tls",
		"dnsNames":   []interface{}{"theirs.example.com"},
		"issuerRef": map[string]interface{}{
			"kind": "Issuer",
			"name": "their-issuer",
		},
	}
	return c
}

// foreignServiceMonitor returns a ServiceMonitor under name that no Valkey controls.
func foreignServiceMonitor(name string) *unstructured.Unstructured {
	sm := &unstructured.Unstructured{}
	sm.SetGroupVersionKind(builder.ServiceMonitorGVK())
	sm.SetName(name)
	sm.SetNamespace("default")
	sm.SetLabels(map[string]string{"owner": "someone-else"})
	sm.Object["spec"] = map[string]interface{}{
		"selector":  map[string]interface{}{"matchLabels": map[string]interface{}{"app": "theirs"}},
		"endpoints": []interface{}{map[string]interface{}{"port": "theirs"}},
	}
	return sm
}

func TestReconcileService_RefusesAForeignService(t *testing.T) {
	v := newTestValkey("test", "default")
	desired := builder.BuildHeadlessService(v)
	foreign := foreignService(desired.Name)
	r, c := newReconcilerWithInterceptor("1.0.0", interceptor.Funcs{}, v, foreign)
	rec := &fakeEventRecorder{}
	r.Recorder = rec

	err := r.reconcileService(context.Background(), v, builder.BuildHeadlessService(v))

	require.Error(t, err, "the -rw Service is how clients reach the master (ADR 0020 D2)")
	assert.ErrorIs(t, err, errForeignObject)

	got := &corev1.Service{}
	require.NoError(t, c.Get(context.Background(),
		types.NamespacedName{Name: foreign.Name, Namespace: "default"}, got))
	assert.Equal(t, map[string]string{"app": "theirs"}, got.Spec.Selector,
		"a Service selector is mutable, so only the guard stops the traffic takeover")
	assert.Equal(t, foreign.Labels, got.Labels)
	require.Len(t, rec.withReason(reasonServiceNotOwned), 1)
}

func TestReconcileMetricsService_ForeignServiceDoesNotFailThePass(t *testing.T) {
	// The one Service whose refusal is downgraded: a collision in the monitoring
	// surface costs scraping, not the data plane (ADR 0020 D2).
	v := serviceMonitorValkey()
	foreign := foreignService(builder.MetricsServiceName(v))
	r, c := newReconcilerWithInterceptor("1.0.0", interceptor.Funcs{}, v, foreign)
	rec := &fakeEventRecorder{}
	r.Recorder = rec

	ctx, state := passCtx()
	require.NoError(t, r.reconcileMetricsService(ctx, v))

	got := &corev1.Service{}
	require.NoError(t, c.Get(context.Background(),
		types.NamespacedName{Name: foreign.Name, Namespace: "default"}, got))
	assert.Equal(t, map[string]string{"app": "theirs"}, got.Spec.Selector)
	require.Len(t, rec.withReason(reasonServiceNotOwned), 1,
		"the Event is still emitted; only the error is downgraded")
	assert.Equal(t, foreignObjectRecheckInterval, state.interval())
}

func TestReconcileConfigMap_RefusesAForeignConfigMap(t *testing.T) {
	v := newTestValkey("test", "default")
	foreign := foreignConfigMap(builder.ConfigMapName(v))
	r, c := newReconcilerWithInterceptor("1.0.0", interceptor.Funcs{}, v, foreign)
	rec := &fakeEventRecorder{}
	r.Recorder = rec

	err := r.reconcileConfigMap(context.Background(), v)

	require.Error(t, err)
	assert.ErrorIs(t, err, errForeignObject)

	got := &corev1.ConfigMap{}
	require.NoError(t, c.Get(context.Background(),
		types.NamespacedName{Name: foreign.Name, Namespace: "default"}, got))
	assert.Equal(t, foreign.Data, got.Data, "a foreign ConfigMap keeps its data")
	require.Len(t, rec.withReason(reasonConfigMapNotOwned), 1)
}

func TestReconcileReplicaConfigMap_RefusesAForeignConfigMap(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) { v.Spec.Replicas = 3 })
	foreign := foreignConfigMap(builder.ReplicaConfigMapName(v))
	r, c := newReconcilerWithInterceptor("1.0.0", interceptor.Funcs{}, v, foreign)
	rec := &fakeEventRecorder{}
	r.Recorder = rec

	err := r.reconcileReplicaConfigMap(context.Background(), v)

	require.Error(t, err)
	assert.ErrorIs(t, err, errForeignObject)

	got := &corev1.ConfigMap{}
	require.NoError(t, c.Get(context.Background(),
		types.NamespacedName{Name: foreign.Name, Namespace: "default"}, got))
	assert.Equal(t, foreign.Data, got.Data,
		"publishing this cluster's master into a stranger's config is the write half of ADR 0020 D8")
	require.Len(t, rec.withReason(reasonConfigMapNotOwned), 1)
}

func TestReplicaConfigMaster_TreatsAForeignConfigMapAsUnknown(t *testing.T) {
	// The read half: a stranger's replicaof directive must never become this
	// cluster's published master, because that value feeds the resolver that
	// issues REPLICAOF (ADR 0020 D8, ADR 0011).
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) { v.Spec.Replicas = 3 })
	foreign := foreignConfigMap(builder.ReplicaConfigMapName(v))
	foreign.Data = map[string]string{
		builder.ValkeyConfigKey: "replicaof test-2.test-headless.default.svc.cluster.local 6379\n",
	}
	r, _ := newTestReconciler(v, foreign)

	name, known := r.replicaConfigMaster(context.Background(), v)

	assert.False(t, known, "a ConfigMap this Valkey does not control is treated as absent")
	assert.Empty(t, name)
}

func TestReconcileSentinelConfigMap_RefusesAForeignConfigMap(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
	})
	foreign := foreignConfigMap(builder.SentinelConfigMapName(v))
	r, c := newReconcilerWithInterceptor("1.0.0", interceptor.Funcs{}, v, foreign)
	rec := &fakeEventRecorder{}
	r.Recorder = rec

	err := r.reconcileSentinelConfigMap(context.Background(), v)

	require.Error(t, err)
	assert.ErrorIs(t, err, errForeignObject)

	got := &corev1.ConfigMap{}
	require.NoError(t, c.Get(context.Background(),
		types.NamespacedName{Name: foreign.Name, Namespace: "default"}, got))
	assert.Equal(t, foreign.Data, got.Data)
	require.Len(t, rec.withReason(reasonConfigMapNotOwned), 1)
}

func TestReconcileNetworkPolicy_RefusesAForeignNetworkPolicy(t *testing.T) {
	// The one path where D2 is read wider than "does the data plane still serve":
	// a CR reporting OK while the policy it names belongs to somebody else is a
	// security statement that is not true.
	v := networkPolicyValkey()
	desired := builder.BuildValkeyNetworkPolicy(v, "valkey-system")
	foreign := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      desired.Name,
			Namespace: "default",
			Labels:    map[string]string{"owner": "someone-else"},
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{MatchLabels: map[string]string{"app": "theirs"}},
		},
	}
	r, c := newReconcilerWithInterceptor("1.0.0", interceptor.Funcs{}, v, foreign)
	rec := &fakeEventRecorder{}
	r.Recorder = rec

	err := r.reconcileNetworkPolicy(context.Background(), v,
		builder.BuildValkeyNetworkPolicy(v, "valkey-system"))

	require.Error(t, err)
	assert.ErrorIs(t, err, errForeignObject)

	got := &networkingv1.NetworkPolicy{}
	require.NoError(t, c.Get(context.Background(),
		types.NamespacedName{Name: foreign.Name, Namespace: "default"}, got))
	assert.Equal(t, map[string]string{"app": "theirs"}, got.Spec.PodSelector.MatchLabels,
		"a foreign policy keeps the pods it selects")
	assert.Empty(t, got.Spec.Ingress, "and keeps its own rules")
	require.Len(t, rec.withReason(reasonNetworkPolicyNotOwned), 1)
}

func TestReconcileNetworkPolicies_ForeignObserverPolicyDoesNotFailThePass(t *testing.T) {
	// The observer's own policy takes the observer's fail direction, not the
	// NetworkPolicy one (ADR 0020 D2).
	v := networkPolicyValkey()
	observerEnabled(v)
	foreign := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      builder.ObserverNetworkPolicyName(v),
			Namespace: "default",
			Labels:    map[string]string{"owner": "someone-else"},
		},
	}
	r, c := newReconcilerWithInterceptor("1.0.0", interceptor.Funcs{}, v, foreign)
	rec := &fakeEventRecorder{}
	r.Recorder = rec

	ctx, state := passCtx()
	require.NoError(t, r.reconcileNetworkPolicies(ctx, v))

	got := &networkingv1.NetworkPolicy{}
	require.NoError(t, c.Get(context.Background(),
		types.NamespacedName{Name: foreign.Name, Namespace: "default"}, got))
	assert.Equal(t, foreign.Labels, got.Labels)
	require.Len(t, rec.withReason(reasonNetworkPolicyNotOwned), 1)
	assert.Equal(t, foreignObjectRecheckInterval, state.interval())
}

func TestReconcileServiceMonitor_RefusesAForeignServiceMonitor(t *testing.T) {
	v := serviceMonitorValkey()
	foreign := foreignServiceMonitor(builder.ServiceMonitorName(v))
	r, c := newReconcilerWithInterceptor("1.0.0", interceptor.Funcs{}, v, foreign)
	rec := &fakeEventRecorder{}
	r.Recorder = rec

	ctx, state := passCtx()
	require.NoError(t, r.reconcileServiceMonitor(ctx, v),
		"scraping is observability; the refusal must not fail the pass (ADR 0020 D2)")

	got := getSM(t, c, foreign.GetName())
	assert.Empty(t, got.GetOwnerReferences(),
		"the CR must not become the controller owner: the garbage collector would delete it")
	endpoints, _, err := unstructured.NestedSlice(got.Object, "spec", "endpoints")
	require.NoError(t, err)
	assert.Equal(t, "theirs", endpoints[0].(map[string]interface{})["port"],
		"a foreign scrape config keeps its endpoints")
	assert.Equal(t, map[string]string{"owner": "someone-else"}, got.GetLabels())
	require.Len(t, rec.withReason(reasonServiceMonitorNotOwned), 1)
	assert.Equal(t, foreignObjectRecheckInterval, state.interval())
}

func TestReconcileCertificate_RefusesAForeignCertificate(t *testing.T) {
	v := newCertManagerValkey()
	foreign := foreignCertificate(builder.ValkeyCertificateName(v))
	r, c := newReconcilerWithInterceptor("1.0.0", interceptor.Funcs{}, v, foreign)
	rec := &fakeEventRecorder{}
	r.Recorder = rec

	err := r.reconcileCertificate(context.Background(), v, builder.BuildValkeyCertificate(v))

	require.Error(t, err, "the pods mount the TLS Secret by name, so the CR cannot do its job")
	assert.ErrorIs(t, err, errForeignObject)

	got := getCert(t, c, foreign.GetName())
	assert.Empty(t, got.GetOwnerReferences(),
		"deleting the CR must not garbage-collect a foreign Certificate and its Secret")
	secretName, _, _ := unstructured.NestedString(got.Object, "spec", "secretName")
	assert.Equal(t, "their-tls", secretName,
		"rewriting secretName would make cert-manager abandon their Secret for ours")
	assert.Equal(t, []string{"theirs.example.com"}, certDNSNames(t, got))
	require.Len(t, rec.withReason(reasonCertificateNotOwned), 1)
}

func TestReconcileResources_ForeignCertificateFailsThePass(t *testing.T) {
	// End to end through the step runner, so the ReconcileBlocked reason comes out
	// as ForeignObject rather than WriteFailed.
	v := newCertManagerValkey()
	foreign := foreignCertificate(builder.ValkeyCertificateName(v))
	r, _ := newTestReconciler(v, foreign)
	rec := &fakeEventRecorder{}
	r.Recorder = rec

	err := r.reconcileResources(context.Background(), v)

	require.Error(t, err)
	assert.ErrorIs(t, err, errForeignObject)
	assert.Equal(t, vkov1.ReasonForeignObject, reconcileBlockedReason(err))
}

// --- the delete half: a feature flag must not delete somebody else's object ---

func TestCleanupServiceMonitor_LeavesAForeignServiceMonitorAlone(t *testing.T) {
	// Reachable by flipping spec.metrics.serviceMonitor.enabled off
	// (docs/adr/0006-delete-only-what-the-operator-owns.md, D2).
	v := serviceMonitorValkey()
	foreign := foreignServiceMonitor(builder.ServiceMonitorName(v))
	r, c := newReconcilerWithInterceptor("1.0.0", interceptor.Funcs{}, v, foreign)

	require.NoError(t, r.cleanupServiceMonitor(context.Background(), v))

	getSM(t, c, foreign.GetName())
}

func TestCleanupMetricsService_LeavesAForeignServiceAlone(t *testing.T) {
	v := serviceMonitorValkey()
	foreign := foreignService(builder.MetricsServiceName(v))
	r, c := newReconcilerWithInterceptor("1.0.0", interceptor.Funcs{}, v, foreign)

	require.NoError(t, r.cleanupMetricsService(context.Background(), v))

	require.NoError(t, c.Get(context.Background(),
		types.NamespacedName{Name: foreign.Name, Namespace: "default"}, &corev1.Service{}))
}

func TestCleanupObserverDeployment_LeavesAForeignNetworkPolicyAlone(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.NetworkPolicy = &vkov1.NetworkPolicySpec{Enabled: true}
		// Observer left disabled: this is the "turned off" cleanup path.
	})
	foreign := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      builder.ObserverNetworkPolicyName(v),
			Namespace: "default",
			Labels:    map[string]string{"owner": "someone-else"},
		},
	}
	r, c := newReconcilerWithInterceptor("1.0.0", interceptor.Funcs{}, v, foreign)

	require.NoError(t, r.cleanupObserverDeployment(context.Background(), v))

	require.NoError(t, c.Get(context.Background(),
		types.NamespacedName{Name: foreign.Name, Namespace: "default"}, &networkingv1.NetworkPolicy{}),
		"a foreign policy under the generated name must survive the cleanup")
}

// TestDeleteIfOwned_ToleratesAReplacementUnderTheName pins the Conflict branch: the
// UID precondition is what makes the ownership decision and the delete describe the
// same object, and losing that race is not a pass failure (ADR 0006 D8, D9).
func TestDeleteIfOwned_ToleratesAReplacementUnderTheName(t *testing.T) {
	v := newTestValkey("test", "default")
	owned := builder.BuildMetricsService(v)
	controllerRefTo(v, owned)
	funcs := interceptor.Funcs{
		Delete: func(_ context.Context, _ client.WithWatch, _ client.Object, _ ...client.DeleteOption) error {
			return apierrors.NewConflict(
				schema.GroupResource{Resource: "services"}, owned.Name, errors.New("uid mismatch"))
		},
	}
	r, _ := newReconcilerWithInterceptor("1.0.0", funcs, v, owned)

	assert.NoError(t, r.deleteIfOwned(context.Background(), v, owned, "metrics Service"))
}
