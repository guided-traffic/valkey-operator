//go:build integration

package integration

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/builder"
)

// What only a real API server decides about docs/adr/0032-generated-pods-run-rootless.md:
// whether the posture survives validation and defaulting unchanged, so the drift
// comparisons -- which now include the securityContext -- report nothing on the
// object read back. A field the API server defaults inside a securityContext would
// otherwise read as drift on every pass, and the operator would write the
// StatefulSet forever.
//
// The objects are written directly, not through a CR: the reconciler would stamp
// ownership and the TLS record onto them, and neither is what this test is about.
// envtest runs no kubelet, so nothing here proves a pod starts under the posture;
// that is the restricted-namespace e2e.

func TestPodSecurity_TemplatesSurviveAPIServerDefaulting_Integration(t *testing.T) {
	ctx := testCtx
	v := &vkov1.Valkey{
		ObjectMeta: metav1.ObjectMeta{Name: "posture-it", Namespace: "default"},
		Spec: vkov1.ValkeySpec{
			Replicas:    3,
			Image:       "valkey/valkey:8.0",
			Sentinel:    &vkov1.SentinelSpec{Enabled: true, Replicas: 3},
			Metrics:     &vkov1.MetricsSpec{Enabled: true},
			Persistence: &vkov1.PersistenceSpec{Enabled: true, Mode: vkov1.PersistenceModeAOF},
			Auth:        &vkov1.AuthSpec{SecretName: "creds", SecretPasswordKey: "password"},
		},
	}

	roundTrip := func(t *testing.T, desired client.Object, readBack client.Object) {
		t.Helper()
		require.NoError(t, k8sClient.Create(ctx, desired))
		t.Cleanup(func() { _ = k8sClient.Delete(ctx, desired) })
		require.NoError(t, k8sClient.Get(ctx, types.NamespacedName{
			Name: desired.GetName(), Namespace: desired.GetNamespace()}, readBack))
	}

	t.Run("data StatefulSet", func(t *testing.T) {
		desired := builder.BuildStatefulSet(v, "ghcr.io/guided-traffic/valkey-operator:test")
		stored := &appsv1.StatefulSet{}
		roundTrip(t, desired.DeepCopy(), stored)
		assert.False(t, builder.StatefulSetHasChanged(desired, stored),
			"the API server must not default anything inside the posture the comparison reads")
		require.NotNil(t, stored.Spec.Template.Spec.SecurityContext)
		assert.Equal(t, builder.ValkeyUID, *stored.Spec.Template.Spec.SecurityContext.RunAsUser)
	})

	t.Run("data StatefulSet with the ownership repair", func(t *testing.T) {
		desired := builder.BuildStatefulSet(v, "ghcr.io/guided-traffic/valkey-operator:test")
		desired.Name = "posture-it-repair"
		builder.WithDataOwnershipRepair(desired)
		stored := &appsv1.StatefulSet{}
		roundTrip(t, desired.DeepCopy(), stored)
		assert.False(t, builder.StatefulSetHasChanged(desired, stored))
		assert.True(t, builder.HasDataOwnershipRepair(&stored.Spec.Template.Spec),
			"the API server accepts a root init container with CAP_CHOWN in an otherwise rootless pod")
	})

	t.Run("Sentinel StatefulSet", func(t *testing.T) {
		desired := builder.BuildSentinelStatefulSet(v)
		stored := &appsv1.StatefulSet{}
		roundTrip(t, desired.DeepCopy(), stored)
		assert.False(t, builder.SentinelStatefulSetHasChanged(desired, stored))
	})

	t.Run("observer Deployment", func(t *testing.T) {
		desired := builder.BuildObserverDeployment(v, "ghcr.io/guided-traffic/valkey-operator:test")
		stored := &appsv1.Deployment{}
		roundTrip(t, desired.DeepCopy(), stored)
		assert.False(t, builder.ObserverDeploymentHasChanged(desired, stored))
	})
}
