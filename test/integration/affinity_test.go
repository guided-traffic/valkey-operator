//go:build integration

package integration

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
)

// TestAntiAffinity_Integration runs against a real API server (envtest), so it also
// exercises the generated CRD schema — including the mode/topologyKey defaults the
// fake client in the unit tests never applies. The default is off: an operator
// upgrade must not change the scheduling of existing clusters, so a term appears
// only after an explicit opt-in to soft or hard.
func TestAntiAffinity_Integration(t *testing.T) {
	ctx := testCtx

	getSTS := func(name string) (*appsv1.StatefulSet, error) {
		sts := &appsv1.StatefulSet{}
		err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, sts)
		return sts, err
	}

	setMode := func(mode string) {
		require.Eventually(t, func() bool {
			current := &vkov1.Valkey{}
			if err := k8sClient.Get(ctx, types.NamespacedName{Name: "aa-test", Namespace: "default"}, current); err != nil {
				return false
			}
			current.Spec.AntiAffinity.Mode = mode
			return k8sClient.Update(ctx, current) == nil
		}, 10*time.Second, 250*time.Millisecond, "CR should switch to mode %s", mode)
	}

	v := &vkov1.Valkey{
		ObjectMeta: metav1.ObjectMeta{Name: "aa-test", Namespace: "default"},
		Spec: vkov1.ValkeySpec{
			Replicas: 3,
			Image:    "valkey/valkey:8.0",
			Sentinel: &vkov1.SentinelSpec{Enabled: true, Replicas: 3},
			// Mode and TopologyKey intentionally unset: the CRD defaults must fill them in.
			AntiAffinity: &vkov1.AntiAffinitySpec{},
		},
	}
	require.NoError(t, k8sClient.Create(ctx, v))
	defer func() { _ = k8sClient.Delete(ctx, v) }()

	t.Run("CRD defaults are applied", func(t *testing.T) {
		stored := &vkov1.Valkey{}
		require.Eventually(t, func() bool {
			return k8sClient.Get(ctx, types.NamespacedName{Name: "aa-test", Namespace: "default"}, stored) == nil
		}, 10*time.Second, 250*time.Millisecond)

		require.NotNil(t, stored.Spec.AntiAffinity)
		assert.Equal(t, vkov1.AntiAffinityModeOff, stored.Spec.AntiAffinity.Mode,
			"the CRD default must be off so an upgrade changes nothing")
		assert.Equal(t, vkov1.DefaultAntiAffinityTopologyKey, stored.Spec.AntiAffinity.TopologyKey)
	})

	t.Run("no pod template carries a term by default", func(t *testing.T) {
		for _, name := range []string{"aa-test", "aa-test-sentinel"} {
			require.Eventually(t, func() bool {
				_, err := getSTS(name)
				return err == nil
			}, 10*time.Second, 250*time.Millisecond, "StatefulSet %s never appeared", name)

			sts, err := getSTS(name)
			require.NoError(t, err)
			assert.Nil(t, sts.Spec.Template.Spec.Affinity,
				"StatefulSet %s must carry no affinity while the mode is off", name)
		}
	})

	t.Run("opting into soft adds the preferred term to both pod templates", func(t *testing.T) {
		setMode(vkov1.AntiAffinityModeSoft)

		for _, name := range []string{"aa-test", "aa-test-sentinel"} {
			require.Eventually(t, func() bool {
				sts, err := getSTS(name)
				return err == nil && sts.Spec.Template.Spec.Affinity != nil
			}, 30*time.Second, 250*time.Millisecond, "StatefulSet %s never got an affinity", name)

			sts, err := getSTS(name)
			require.NoError(t, err)
			antiAffinity := sts.Spec.Template.Spec.Affinity.PodAntiAffinity
			require.NotNil(t, antiAffinity)
			assert.Empty(t, antiAffinity.RequiredDuringSchedulingIgnoredDuringExecution,
				"soft must never block scheduling")
			require.Len(t, antiAffinity.PreferredDuringSchedulingIgnoredDuringExecution, 1)
			assert.Equal(t, vkov1.DefaultAntiAffinityTopologyKey,
				antiAffinity.PreferredDuringSchedulingIgnoredDuringExecution[0].PodAffinityTerm.TopologyKey)
		}
	})

	t.Run("switching to hard replaces the term", func(t *testing.T) {
		setMode(vkov1.AntiAffinityModeHard)

		require.Eventually(t, func() bool {
			sts, err := getSTS("aa-test")
			if err != nil || sts.Spec.Template.Spec.Affinity == nil {
				return false
			}
			return len(sts.Spec.Template.Spec.Affinity.PodAntiAffinity.
				RequiredDuringSchedulingIgnoredDuringExecution) == 1
		}, 30*time.Second, 250*time.Millisecond, "data StatefulSet should get the required term")

		sts, err := getSTS("aa-test")
		require.NoError(t, err)
		assert.Empty(t, sts.Spec.Template.Spec.Affinity.PodAntiAffinity.
			PreferredDuringSchedulingIgnoredDuringExecution, "hard mode must replace the preference")
	})

	t.Run("scaling to a single replica removes the term", func(t *testing.T) {
		require.Eventually(t, func() bool {
			current := &vkov1.Valkey{}
			if err := k8sClient.Get(ctx, types.NamespacedName{Name: "aa-test", Namespace: "default"}, current); err != nil {
				return false
			}
			current.Spec.Replicas = 1
			return k8sClient.Update(ctx, current) == nil
		}, 10*time.Second, 250*time.Millisecond, "CR should be scaled down")

		require.Eventually(t, func() bool {
			sts, err := getSTS("aa-test")
			return err == nil && sts.Spec.Template.Spec.Affinity == nil
		}, 30*time.Second, 250*time.Millisecond, "a singleton must not keep an anti-affinity term")
	})
}
