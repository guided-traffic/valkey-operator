//go:build integration

package integration

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/builder"
)

// TestForeignSidecarServiceAccount_Integration covers the refusal and, more
// importantly, the recovery from it: a Valkey whose derived name collides with an
// existing ServiceAccount must not hand that identity the sidecar grant, must say
// so on the CR, and must finish provisioning by itself once an administrator
// removes the collision — with no edit to the CR and no operator restart
// (docs/adr/0020-write-only-what-the-operator-owns.md, D3, D6).
//
// Why this tier: the recovery has no watch behind it. The colliding ServiceAccount
// carries no ownerReference to the CR, so the Owns(&corev1.ServiceAccount{})
// registration in SetupWithManager never maps its deletion to a reconcile request,
// and GenerationChangedPredicate drops the operator's own status writes. What brings
// the pass back is the work queue re-entering it after the returned error, which
// only exists when a real manager drives a real controller. A unit test can assert
// the refusal; only this tier can show the operator picking itself back up.
func TestForeignSidecarServiceAccount_Integration(t *testing.T) {
	ctx := testCtx
	crName := "foreign-sa-test"
	rbacName := crName + "-sidecar"
	key := types.NamespacedName{Name: rbacName, Namespace: "default"}

	// A ServiceAccount someone else owns, sitting on the name the operator derives.
	// No ownerReference to any Valkey — that is what makes it foreign.
	foreign := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:        rbacName,
			Namespace:   "default",
			Labels:      map[string]string{"owner": "someone-else"},
			Annotations: map[string]string{"eks.amazonaws.com/role-arn": "arn:aws:iam::1:role/theirs"},
		},
	}
	require.NoError(t, k8sClient.Create(ctx, foreign))

	v := &vkov1.Valkey{
		ObjectMeta: metav1.ObjectMeta{Name: crName, Namespace: "default"},
		Spec:       vkov1.ValkeySpec{Replicas: 1, Image: "valkey/valkey:8.0"},
	}
	require.NoError(t, k8sClient.Create(ctx, v))
	t.Cleanup(func() { _ = k8sClient.Delete(ctx, v) })

	t.Run("the grant is refused and reported", func(t *testing.T) {
		require.Eventually(t, func() bool {
			current := &vkov1.Valkey{}
			if err := k8sClient.Get(ctx, types.NamespacedName{Name: crName, Namespace: "default"}, current); err != nil {
				return false
			}
			blocked := meta.FindStatusCondition(current.Status.Conditions, vkov1.ConditionTypeReconcileBlocked)
			return blocked != nil &&
				blocked.Status == metav1.ConditionTrue &&
				blocked.Reason == vkov1.ReasonForeignObject
		}, 30*time.Second, 250*time.Millisecond,
			"the collision is only actionable if the CR names it: ReconcileBlocked/ForeignObject")

		current := &vkov1.Valkey{}
		require.NoError(t, k8sClient.Get(ctx, types.NamespacedName{Name: crName, Namespace: "default"}, current))
		assert.Equal(t, vkov1.ValkeyPhaseError, current.Status.Phase,
			"status.phase is the only field with a print column, so a cluster that cannot work must not read OK")

		// The grant itself: neither object exists, so nothing was handed to the
		// foreign identity.
		assert.True(t, apierrors.IsNotFound(k8sClient.Get(ctx, key, &rbacv1.RoleBinding{})),
			"the RoleBinding names the ServiceAccount by name; writing it would grant pods/patch to a stranger")
		assert.True(t, apierrors.IsNotFound(k8sClient.Get(ctx, key, &rbacv1.Role{})),
			"no Role is written for a ServiceAccount the operator does not own")

		// And the foreign object came through untouched.
		got := &corev1.ServiceAccount{}
		require.NoError(t, k8sClient.Get(ctx, key, got))
		assert.Equal(t, "someone-else", got.Labels["owner"])
		assert.Equal(t, "arn:aws:iam::1:role/theirs", got.Annotations["eks.amazonaws.com/role-arn"],
			"erasing this breaks whatever else runs under that identity")
		assert.Empty(t, got.OwnerReferences, "the operator must not adopt it either")
	})

	t.Run("removing the collision finishes the provisioning without an operator restart", func(t *testing.T) {
		require.NoError(t, k8sClient.Delete(ctx, foreign))

		// Nothing is touched here but the colliding object. No CR edit, no restart,
		// no watch event that maps back to this CR — the work queue is the only
		// thing that brings the pass back.
		require.Eventually(t, func() bool {
			return k8sClient.Get(ctx, key, &rbacv1.RoleBinding{}) == nil
		}, 90*time.Second, 500*time.Millisecond,
			"the operator has to pick itself back up; an administrator restarting it is the failure this guards")

		sa := &corev1.ServiceAccount{}
		require.NoError(t, k8sClient.Get(ctx, key, sa))
		assert.True(t, metav1.IsControlledBy(sa, v), "the replacement ServiceAccount is the operator's own")

		require.Eventually(t, func() bool {
			current := &vkov1.Valkey{}
			if err := k8sClient.Get(ctx, types.NamespacedName{Name: crName, Namespace: "default"}, current); err != nil {
				return false
			}
			blocked := meta.FindStatusCondition(current.Status.Conditions, vkov1.ConditionTypeReconcileBlocked)
			return blocked != nil && blocked.Status == metav1.ConditionFalse
		}, 30*time.Second, 250*time.Millisecond, "the condition must clear once the collision is gone")
	})
}

// TestForeignObserverServiceAccount_Integration is the counterpart decision: the
// observer keeps running under a ServiceAccount the operator does not own, because
// it gains nothing from it — no token is mounted, no Role is bound to it, and the
// observer makes no Kubernetes API call. Refusing the Deployment as well would turn
// a name collision into an outage of the diagnostic component and buy no security
// (ADR 0020 D2).
func TestForeignObserverServiceAccount_Integration(t *testing.T) {
	ctx := testCtx
	crName := "foreign-observer-test"
	saName := crName + "-observer"
	key := types.NamespacedName{Name: saName, Namespace: "default"}

	foreign := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:        saName,
			Namespace:   "default",
			Annotations: map[string]string{"iam.gke.io/gcp-service-account": "svc@project.iam"},
		},
	}
	require.NoError(t, k8sClient.Create(ctx, foreign))
	t.Cleanup(func() { _ = k8sClient.Delete(ctx, foreign) })

	v := &vkov1.Valkey{
		ObjectMeta: metav1.ObjectMeta{Name: crName, Namespace: "default"},
		Spec: vkov1.ValkeySpec{
			Replicas: 1,
			Image:    "valkey/valkey:8.0",
			Observer: &vkov1.ObserverSpec{Enabled: true},
		},
	}
	require.NoError(t, k8sClient.Create(ctx, v))
	t.Cleanup(func() { _ = k8sClient.Delete(ctx, v) })

	require.Eventually(t, func() bool {
		return k8sClient.Get(ctx, types.NamespacedName{
			Name: builder.ObserverDeploymentName(v), Namespace: "default",
		}, &appsv1.Deployment{}) == nil
	}, 30*time.Second, 250*time.Millisecond,
		"a name collision must not take the observer down: it mounts no token and is bound to no Role")

	got := &corev1.ServiceAccount{}
	require.NoError(t, k8sClient.Get(ctx, key, got))
	assert.Equal(t, "svc@project.iam", got.Annotations["iam.gke.io/gcp-service-account"],
		"the foreign ServiceAccount keeps the annotations another workload depends on")
	assert.Empty(t, got.OwnerReferences,
		"leaving it unowned is what keeps it out of this CR's garbage collection")
}

// TestForeignDataStatefulSet_Integration is the NA61 half of ADR 0020: the data
// StatefulSet carries the bare CR name, so it is the name a pre-existing foreign
// StatefulSet is most likely to hold. The operator must refuse the write — the
// destructive alternative is installing its pod template into someone else's
// workload — say so on the CR, leave the object entirely alone (no nudge patch,
// no ownerReference), and provision its own StatefulSet by itself once the
// collision is removed.
func TestForeignDataStatefulSet_Integration(t *testing.T) {
	ctx := testCtx
	crName := "foreign-sts-test"
	key := types.NamespacedName{Name: crName, Namespace: "default"}

	replicas := int32(1)
	foreign := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      crName,
			Namespace: "default",
			Labels:    map[string]string{"owner": "someone-else"},
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas:    &replicas,
			Selector:    &metav1.LabelSelector{MatchLabels: map[string]string{"app": "theirs"}},
			ServiceName: "theirs",
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "theirs"}},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "app", Image: "registry.example/theirs:1"}},
				},
			},
		},
	}
	require.NoError(t, k8sClient.Create(ctx, foreign))

	v := &vkov1.Valkey{
		ObjectMeta: metav1.ObjectMeta{Name: crName, Namespace: "default"},
		Spec:       vkov1.ValkeySpec{Replicas: 1, Image: "valkey/valkey:8.0"},
	}
	require.NoError(t, k8sClient.Create(ctx, v))
	t.Cleanup(func() { _ = k8sClient.Delete(ctx, v) })

	t.Run("the write is refused and reported", func(t *testing.T) {
		require.Eventually(t, func() bool {
			current := &vkov1.Valkey{}
			if err := k8sClient.Get(ctx, key, current); err != nil {
				return false
			}
			blocked := meta.FindStatusCondition(current.Status.Conditions, vkov1.ConditionTypeReconcileBlocked)
			return blocked != nil &&
				blocked.Status == metav1.ConditionTrue &&
				blocked.Reason == vkov1.ReasonForeignObject
		}, 30*time.Second, 250*time.Millisecond,
			"the collision is only actionable if the CR names it: ReconcileBlocked/ForeignObject")

		current := &vkov1.Valkey{}
		require.NoError(t, k8sClient.Get(ctx, key, current))
		assert.Equal(t, vkov1.ValkeyPhaseError, current.Status.Phase,
			"a CR whose data plane cannot exist must not read OK or Provisioning in kubectl get")

		// The foreign StatefulSet came through untouched: template, labels,
		// ownership — and no nudge annotation, which is also a write.
		got := &appsv1.StatefulSet{}
		require.NoError(t, k8sClient.Get(ctx, key, got))
		assert.Equal(t, "registry.example/theirs:1", got.Spec.Template.Spec.Containers[0].Image,
			"writing the pod template onto a foreign StatefulSet replaces its workload")
		assert.Equal(t, "someone-else", got.Labels["owner"])
		assert.Empty(t, got.OwnerReferences, "the operator must not adopt it either")
		assert.NotContains(t, got.Annotations, builder.AnnotationNudge,
			"the nudge patch is a write and must not land on a foreign StatefulSet")
	})

	t.Run("removing the collision provisions the operator's own StatefulSet", func(t *testing.T) {
		require.NoError(t, k8sClient.Delete(ctx, foreign))

		require.Eventually(t, func() bool {
			got := &appsv1.StatefulSet{}
			if err := k8sClient.Get(ctx, key, got); err != nil {
				return false
			}
			return metav1.IsControlledBy(got, v)
		}, 90*time.Second, 500*time.Millisecond,
			"the operator has to pick itself back up and create its own StatefulSet")

		require.Eventually(t, func() bool {
			current := &vkov1.Valkey{}
			if err := k8sClient.Get(ctx, key, current); err != nil {
				return false
			}
			blocked := meta.FindStatusCondition(current.Status.Conditions, vkov1.ConditionTypeReconcileBlocked)
			return blocked != nil && blocked.Status == metav1.ConditionFalse
		}, 30*time.Second, 250*time.Millisecond, "the condition must clear once the collision is gone")
	})
}

// TestForeignMetricsService_Integration is the half of ADR 0020 D2 that a unit test
// cannot make stick: a refusal that must NOT fail the pass. The metrics Service and
// the ServiceMonitor take the observer's direction, so a name collision in the
// monitoring surface has to leave the CR outside ReconcileBlocked and outside the
// Error phase.
//
// Why this tier: the phase is not written by the refusing step. updateStatus runs
// after reconcileResources in the same pass and recomputes it from the data plane,
// and setReconcileBlockedCondition writes the condition from the joined pass error.
// Only a real manager driving a real pass shows what those two authorities agree on
// after a downgraded refusal.
func TestForeignMetricsService_Integration(t *testing.T) {
	ctx := testCtx
	crName := "foreign-metrics-test"
	key := types.NamespacedName{Name: crName, Namespace: "default"}
	svcKey := types.NamespacedName{Name: crName + "-metrics", Namespace: "default"}

	foreign := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      svcKey.Name,
			Namespace: "default",
			Labels:    map[string]string{"owner": "someone-else"},
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": "theirs"},
			Ports:    []corev1.ServicePort{{Name: "http", Port: 8080}},
		},
	}
	require.NoError(t, k8sClient.Create(ctx, foreign))
	t.Cleanup(func() { _ = k8sClient.Delete(ctx, foreign) })

	v := &vkov1.Valkey{
		ObjectMeta: metav1.ObjectMeta{Name: crName, Namespace: "default"},
		Spec: vkov1.ValkeySpec{
			Replicas: 1,
			Image:    "valkey/valkey:8.0",
			Metrics:  &vkov1.MetricsSpec{Enabled: true},
		},
	}
	require.NoError(t, k8sClient.Create(ctx, v))
	t.Cleanup(func() { _ = k8sClient.Delete(ctx, v) })

	// Wait for the pass to have run at all, which the operator's own StatefulSet
	// proves; the assertions below are about what it did NOT do.
	require.Eventually(t, func() bool {
		return k8sClient.Get(ctx, key, &appsv1.StatefulSet{}) == nil
	}, 60*time.Second, 250*time.Millisecond, "the pass must complete despite the collision")

	current := &vkov1.Valkey{}
	require.NoError(t, k8sClient.Get(ctx, key, current))
	blocked := meta.FindStatusCondition(current.Status.Conditions, vkov1.ConditionTypeReconcileBlocked)
	if blocked != nil {
		assert.NotEqual(t, vkov1.ReasonForeignObject, blocked.Reason,
			"a monitoring name collision must not block the CR")
	}
	assert.NotEqual(t, vkov1.ValkeyPhaseError, current.Status.Phase,
		"scraping is observability: the data plane is untouched and the CR must not read Error")

	got := &corev1.Service{}
	require.NoError(t, k8sClient.Get(ctx, svcKey, got))
	assert.Equal(t, map[string]string{"app": "theirs"}, got.Spec.Selector,
		"a Service selector is mutable, so only the guard stops the traffic takeover")
	assert.Empty(t, got.OwnerReferences,
		"leaving it unowned is what keeps it out of this CR's garbage collection")
}

// TestForeignConfigMap_Integration is the opposite direction on the same rule: the
// Valkey ConfigMap is the data plane, so its refusal fails the pass, names itself on
// the CR, and clears by itself once the collision is removed
// (docs/adr/0020-write-only-what-the-operator-owns.md, D2, D6).
func TestForeignConfigMap_Integration(t *testing.T) {
	ctx := testCtx
	crName := "foreign-cm-test"
	key := types.NamespacedName{Name: crName, Namespace: "default"}
	cmKey := types.NamespacedName{Name: crName + "-config", Namespace: "default"}

	foreign := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cmKey.Name,
			Namespace: "default",
			Labels:    map[string]string{"owner": "someone-else"},
		},
		Data: map[string]string{"theirs.conf": "# not ours\n"},
	}
	require.NoError(t, k8sClient.Create(ctx, foreign))

	v := &vkov1.Valkey{
		ObjectMeta: metav1.ObjectMeta{Name: crName, Namespace: "default"},
		Spec:       vkov1.ValkeySpec{Replicas: 1, Image: "valkey/valkey:8.0"},
	}
	require.NoError(t, k8sClient.Create(ctx, v))
	t.Cleanup(func() { _ = k8sClient.Delete(ctx, v) })

	t.Run("the write is refused and reported", func(t *testing.T) {
		require.Eventually(t, func() bool {
			current := &vkov1.Valkey{}
			if err := k8sClient.Get(ctx, key, current); err != nil {
				return false
			}
			blocked := meta.FindStatusCondition(current.Status.Conditions, vkov1.ConditionTypeReconcileBlocked)
			return blocked != nil &&
				blocked.Status == metav1.ConditionTrue &&
				blocked.Reason == vkov1.ReasonForeignObject
		}, 30*time.Second, 250*time.Millisecond,
			"the collision is only actionable if the CR names it: ReconcileBlocked/ForeignObject")

		got := &corev1.ConfigMap{}
		require.NoError(t, k8sClient.Get(ctx, cmKey, got))
		assert.Equal(t, "# not ours\n", got.Data["theirs.conf"],
			"a foreign ConfigMap keeps the data another workload reads")
		assert.NotContains(t, got.Data, builder.ValkeyConfigKey,
			"and must not gain this cluster's configuration")
		assert.Empty(t, got.OwnerReferences, "the operator must not adopt it either")
	})

	t.Run("removing the collision provisions the operator's own ConfigMap", func(t *testing.T) {
		require.NoError(t, k8sClient.Delete(ctx, foreign))

		require.Eventually(t, func() bool {
			got := &corev1.ConfigMap{}
			if err := k8sClient.Get(ctx, cmKey, got); err != nil {
				return false
			}
			return metav1.IsControlledBy(got, v)
		}, 90*time.Second, 500*time.Millisecond,
			"the operator has to pick itself back up and create its own ConfigMap")

		require.Eventually(t, func() bool {
			current := &vkov1.Valkey{}
			if err := k8sClient.Get(ctx, key, current); err != nil {
				return false
			}
			blocked := meta.FindStatusCondition(current.Status.Conditions, vkov1.ConditionTypeReconcileBlocked)
			return blocked != nil && blocked.Status == metav1.ConditionFalse
		}, 30*time.Second, 250*time.Millisecond, "the condition must clear once the collision is gone")
	})
}
