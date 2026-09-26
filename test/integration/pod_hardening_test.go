//go:build integration

package integration

import (
	"context"
	"fmt"
	"strings"
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
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/builder"
)

// What only a real API server decides about
// docs/adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md:
// the CRD refuses an Unconfined profile and a Localhost one without a path, and a
// server whose UserNamespacesSupport gate is off drops hostUsers from a pod template
// without an error. envtest runs Kubernetes 1.29, where that gate is alpha and off,
// so the second half is the real server behaviour, not a simulation.

func hardeningCR(name string, ps *vkov1.PodSecuritySpec) *vkov1.Valkey {
	return &vkov1.Valkey{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: vkov1.ValkeySpec{
			Replicas:    1,
			Image:       "valkey/valkey:8.0",
			PodSecurity: ps,
		},
	}
}

func TestPodSecurity_CRDValidatesTheSeccompProfile_Integration(t *testing.T) {
	ctx := testCtx
	localhost := func(profile *string) *vkov1.PodSecuritySpec {
		return &vkov1.PodSecuritySpec{SeccompProfile: &vkov1.SeccompProfileSpec{
			Type: corev1.SeccompProfileTypeLocalhost, LocalhostProfile: profile,
		}}
	}
	for i, tc := range []struct {
		name    string
		ps      *vkov1.PodSecuritySpec
		allowed bool
	}{
		{"Unconfined is refused", &vkov1.PodSecuritySpec{SeccompProfile: &vkov1.SeccompProfileSpec{
			Type: corev1.SeccompProfileTypeUnconfined}}, false},
		{"Localhost without a profile is refused", localhost(nil), false},
		{"Localhost with an empty profile is refused", localhost(ptr.To("")), false},
		{"a profile with RuntimeDefault is refused", &vkov1.PodSecuritySpec{SeccompProfile: &vkov1.SeccompProfileSpec{
			Type: corev1.SeccompProfileTypeRuntimeDefault, LocalhostProfile: ptr.To("profiles/x.json")}}, false},
		{"Localhost with a profile is accepted", localhost(ptr.To("profiles/valkey.json")), true},
		{"an absolute profile path is refused", localhost(ptr.To("/etc/seccomp/valkey.json")), false},
		{"a leading '..' is refused", localhost(ptr.To("../valkey.json")), false},
		{"an inner '..' is refused", localhost(ptr.To("profiles/../../valkey.json")), false},
		{"a trailing '..' is refused", localhost(ptr.To("profiles/..")), false},
		{"dots inside a name are not a '..' element", localhost(ptr.To("profiles/..valkey..json")), true},
		{"RuntimeDefault is accepted", &vkov1.PodSecuritySpec{SeccompProfile: &vkov1.SeccompProfileSpec{
			Type: corev1.SeccompProfileTypeRuntimeDefault}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// A fresh name per row, and an unreconcilable namespace is not needed:
			// validation runs before anything is stored.
			v := hardeningCR(fmt.Sprintf("seccomp-crd-%d", i), tc.ps)
			err := k8sClient.Create(ctx, v)
			if err == nil {
				t.Cleanup(func() { _ = k8sClient.Delete(ctx, v) })
			}
			if tc.allowed {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.True(t, apierrors.IsInvalid(err), "expected Invalid, got %T: %v", err, err)
		})
	}
}

// TestPodSecurity_TypeDefaultsToRuntimeDefault: a seccompProfile block without a
// type is the default profile, not a refused object.
func TestPodSecurity_TypeDefaultsToRuntimeDefault_Integration(t *testing.T) {
	ctx := testCtx
	v := hardeningCR("seccomp-default", nil)
	require.NoError(t, k8sClient.Create(ctx, v))
	t.Cleanup(func() { _ = k8sClient.Delete(ctx, v) })

	// Written as unstructured JSON would omit type; the typed client always sends
	// it, so the defaulting is checked through a merge patch that leaves it out.
	patch := []byte(`{"spec":{"podSecurity":{"seccompProfile":{}}}}`)
	// Patch decodes the stored object into v; a Get would read the manager's cache.
	require.NoError(t, k8sClient.Patch(ctx, v, client.RawPatch(types.MergePatchType, patch)))
	stored := v
	require.NotNil(t, stored.Spec.PodSecurity)
	require.NotNil(t, stored.Spec.PodSecurity.SeccompProfile)
	assert.Equal(t, corev1.SeccompProfileTypeRuntimeDefault, stored.Spec.PodSecurity.SeccompProfile.Type)
	assert.False(t, stored.Spec.PodSecurity.UserNamespaces)
}

// TestPodSecurity_ADroppedUserNamespaceBlocksThePass_Integration is the D3 guard
// against the real 1.29 API server: the StatefulSet is stored without hostUsers,
// the CR says so -- ReconcileBlocked/UserNamespacesUnsupported and phase Error --
// and turning the opt-in off releases it.
func TestPodSecurity_ADroppedUserNamespaceBlocksThePass_Integration(t *testing.T) {
	ctx := testCtx
	name := "userns-dropped"
	key := types.NamespacedName{Name: name, Namespace: "default"}
	v := hardeningCR(name, &vkov1.PodSecuritySpec{UserNamespaces: true})
	require.NoError(t, k8sClient.Create(ctx, v))
	t.Cleanup(func() { _ = k8sClient.Delete(ctx, v) })

	sts := waitForStatefulSet(t, name, 30*time.Second)
	assert.Nil(t, sts.Spec.Template.Spec.HostUsers,
		"premise: this API server drops hostUsers (UserNamespacesSupport is off in 1.29); if this fails, "+
			"envtest moved to a version with the gate on and this test no longer exercises D3")

	blockedWith := func(reason string) wait.ConditionWithContextFunc {
		return func(ctx context.Context) (bool, error) {
			current := &vkov1.Valkey{}
			if err := k8sClient.Get(ctx, key, current); err != nil {
				return false, nil
			}
			c := meta.FindStatusCondition(current.Status.Conditions, vkov1.ConditionTypeReconcileBlocked)
			if reason == "" {
				return c == nil || c.Status == metav1.ConditionFalse, nil
			}
			return c != nil && c.Status == metav1.ConditionTrue && c.Reason == reason, nil
		}
	}

	require.NoError(t, wait.PollUntilContextTimeout(ctx, 200*time.Millisecond, 60*time.Second, true,
		blockedWith(vkov1.ReasonUserNamespacesUnsupported)),
		"a user namespace the cluster silently dropped must be reported on the CR")
	// The condition and the phase are two status writes of one pass, the phase the
	// later one, and k8sClient reads through the cache: polled, not read once.
	requirePhaseError(t, key, "UserNamespacesSupport")

	require.NoError(t, wait.PollUntilContextTimeout(ctx, 200*time.Millisecond, 30*time.Second, true,
		func(ctx context.Context) (bool, error) {
			latest := &vkov1.Valkey{}
			if err := k8sClient.Get(ctx, key, latest); err != nil {
				return false, nil
			}
			latest.Spec.PodSecurity.UserNamespaces = false
			return k8sClient.Update(ctx, latest) == nil, nil
		}))
	require.NoError(t, wait.PollUntilContextTimeout(ctx, 200*time.Millisecond, 60*time.Second, true,
		blockedWith("")), "setting the opt-in back releases the pass")
}

// TestPodSecurity_HardenedTemplatesSurviveAPIServerDefaulting_Integration: with a
// Localhost profile and Sentinel resources, the stored templates read back with no
// drift -- a field the server defaulted would otherwise be rewritten every pass.
// hostUsers is left out: this server drops it (previous test).
func TestPodSecurity_HardenedTemplatesSurviveAPIServerDefaulting_Integration(t *testing.T) {
	ctx := testCtx
	res := corev1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("32Mi")}}
	v := &vkov1.Valkey{
		ObjectMeta: metav1.ObjectMeta{Name: "hardened-it", Namespace: "default"},
		Spec: vkov1.ValkeySpec{
			Replicas: 3,
			Image:    "valkey/valkey:8.0@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			Sentinel: &vkov1.SentinelSpec{Enabled: true, Replicas: 3, Resources: &res},
			Metrics:  &vkov1.MetricsSpec{Enabled: true},
			PodSecurity: &vkov1.PodSecuritySpec{SeccompProfile: &vkov1.SeccompProfileSpec{
				Type: corev1.SeccompProfileTypeLocalhost, LocalhostProfile: ptr.To("profiles/valkey.json"),
			}},
		},
	}
	data := builder.BuildStatefulSet(v, "ghcr.io/guided-traffic/valkey-operator:test")
	data.Name = "hardened-it-data"
	// Create decodes the stored object into its argument; k8sClient reads through
	// the manager's cache, so a Get right after the Create could miss it.
	storedData := data.DeepCopy()
	require.NoError(t, k8sClient.Create(ctx, storedData), "a digest-pinned image yields a valid version label")
	t.Cleanup(func() { _ = k8sClient.Delete(ctx, storedData) })
	assert.False(t, builder.StatefulSetHasChanged(data, storedData))

	sentinel := builder.BuildSentinelStatefulSet(v)
	storedSentinel := sentinel.DeepCopy()
	require.NoError(t, k8sClient.Create(ctx, storedSentinel))
	t.Cleanup(func() { _ = k8sClient.Delete(ctx, storedSentinel) })
	assert.False(t, builder.SentinelStatefulSetHasChanged(sentinel, storedSentinel))

	observer := builder.BuildObserverDeployment(v, "ghcr.io/guided-traffic/valkey-operator:test")
	storedObserver := observer.DeepCopy()
	require.NoError(t, k8sClient.Create(ctx, storedObserver))
	t.Cleanup(func() { _ = k8sClient.Delete(ctx, storedObserver) })
	assert.False(t, builder.ObserverDeploymentHasChanged(observer, storedObserver))
}

// requirePhaseError waits for phase Error with a message naming want. The blocked
// condition is written before the phase in the same pass, so a single cached read
// right after the condition can still see the previous phase.
func requirePhaseError(t *testing.T, key types.NamespacedName, want string) {
	t.Helper()
	last := ""
	err := wait.PollUntilContextTimeout(testCtx, 200*time.Millisecond, 30*time.Second, true,
		func(ctx context.Context) (bool, error) {
			current := &vkov1.Valkey{}
			if err := k8sClient.Get(ctx, key, current); err != nil {
				return false, nil
			}
			last = string(current.Status.Phase) + ": " + current.Status.Message
			return current.Status.Phase == vkov1.ValkeyPhaseError && strings.Contains(current.Status.Message, want), nil
		})
	require.NoError(t, err, "phase Error naming %q never arrived; last: %s", want, last)
}

// allowedTestSeccompProfile is the one Localhost profile the suite's reconciler
// allows (suite_test.go).
const allowedTestSeccompProfile = "profiles/allowed.json"

// TestPodSecurity_LocalhostProfileAllowList_Integration is ADR 0033 D9 against the
// real API server and the real controller wiring: a profile the operator was not
// started with is never written -- no StatefulSet appears, and the CR says why --
// and a listed one reaches the StatefulSet.
func TestPodSecurity_LocalhostProfileAllowList_Integration(t *testing.T) {
	ctx := testCtx
	localhost := func(profile string) *vkov1.PodSecuritySpec {
		return &vkov1.PodSecuritySpec{SeccompProfile: &vkov1.SeccompProfileSpec{
			Type: corev1.SeccompProfileTypeLocalhost, LocalhostProfile: ptr.To(profile),
		}}
	}

	t.Run("an unlisted profile is refused and reported", func(t *testing.T) {
		name := "seccomp-refused"
		key := types.NamespacedName{Name: name, Namespace: "default"}
		v := hardeningCR(name, localhost("profiles/not-listed.json"))
		require.NoError(t, k8sClient.Create(ctx, v))
		t.Cleanup(func() { _ = k8sClient.Delete(ctx, v) })

		require.NoError(t, wait.PollUntilContextTimeout(ctx, 200*time.Millisecond, 60*time.Second, true,
			func(ctx context.Context) (bool, error) {
				current := &vkov1.Valkey{}
				if err := k8sClient.Get(ctx, key, current); err != nil {
					return false, nil
				}
				c := meta.FindStatusCondition(current.Status.Conditions, vkov1.ConditionTypeReconcileBlocked)
				return c != nil && c.Status == metav1.ConditionTrue && c.Reason == vkov1.ReasonSeccompProfileNotAllowed, nil
			}), "a Localhost profile outside the allow-list must be reported on the CR")
		requirePhaseError(t, key, "profiles/not-listed.json")

		// Read past the cache: the claim is that the object was never created.
		err := apiReader.Get(ctx, key, &appsv1.StatefulSet{})
		assert.True(t, apierrors.IsNotFound(err), "the data StatefulSet must not be created: %v", err)
	})

	t.Run("a listed profile reaches the StatefulSet", func(t *testing.T) {
		name := "seccomp-allowed"
		v := hardeningCR(name, localhost(allowedTestSeccompProfile))
		require.NoError(t, k8sClient.Create(ctx, v))
		t.Cleanup(func() { _ = k8sClient.Delete(ctx, v) })

		sts := waitForStatefulSet(t, name, 30*time.Second)
		require.NotNil(t, sts.Spec.Template.Spec.SecurityContext)
		assert.Equal(t, &corev1.SeccompProfile{
			Type: corev1.SeccompProfileTypeLocalhost, LocalhostProfile: ptr.To(allowedTestSeccompProfile),
		}, sts.Spec.Template.Spec.SecurityContext.SeccompProfile)
	})
}
