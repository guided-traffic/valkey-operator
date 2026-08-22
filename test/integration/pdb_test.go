//go:build integration

package integration

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
)

// TestPodDisruptionBudget_Integration runs against a real API server (envtest), so
// it also exercises the generated CRD schema — including the maxUnavailable default
// the fake client in the unit tests never applies.
func TestPodDisruptionBudget_Integration(t *testing.T) {
	ctx := testCtx

	getPDB := func(name string) (*policyv1.PodDisruptionBudget, error) {
		pdb := &policyv1.PodDisruptionBudget{}
		err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, pdb)
		return pdb, err
	}

	v := &vkov1.Valkey{
		ObjectMeta: metav1.ObjectMeta{Name: "pdb-test", Namespace: "default"},
		Spec: vkov1.ValkeySpec{
			Replicas: 3,
			Image:    "valkey/valkey:8.0",
			Sentinel: &vkov1.SentinelSpec{Enabled: true, Replicas: 3},
			// MaxUnavailable intentionally unset: the CRD default must fill it in.
			PodDisruptionBudget: &vkov1.PodDisruptionBudgetSpec{Enabled: true},
		},
	}
	require.NoError(t, k8sClient.Create(ctx, v))
	defer func() { _ = k8sClient.Delete(ctx, v) }()

	t.Run("both budgets are created", func(t *testing.T) {
		require.Eventually(t, func() bool {
			_, err := getPDB("pdb-test")
			return err == nil
		}, 10*time.Second, 250*time.Millisecond, "data PDB should be created")

		require.Eventually(t, func() bool {
			_, err := getPDB("pdb-test-sentinel")
			return err == nil
		}, 10*time.Second, 250*time.Millisecond, "sentinel PDB should be created")

		data, err := getPDB("pdb-test")
		require.NoError(t, err)
		require.NotNil(t, data.Spec.MaxUnavailable)
		assert.Equal(t, "1", data.Spec.MaxUnavailable.String(), "CRD default for maxUnavailable is 1")
		assert.Nil(t, data.Spec.MinAvailable)
		require.Len(t, data.OwnerReferences, 1)
		assert.Equal(t, "pdb-test", data.OwnerReferences[0].Name)

		sentinel, err := getPDB("pdb-test-sentinel")
		require.NoError(t, err)
		require.NotNil(t, sentinel.Spec.MinAvailable)
		assert.Equal(t, "2", sentinel.Spec.MinAvailable.String(), "quorum of 3 sentinels is 2")
		assert.Nil(t, sentinel.Spec.MaxUnavailable)
	})

	t.Run("scaling below two replicas removes the data budget", func(t *testing.T) {
		require.Eventually(t, func() bool {
			current := &vkov1.Valkey{}
			if err := k8sClient.Get(ctx, types.NamespacedName{Name: "pdb-test", Namespace: "default"}, current); err != nil {
				return false
			}
			current.Spec.Replicas = 1
			return k8sClient.Update(ctx, current) == nil
		}, 10*time.Second, 250*time.Millisecond, "CR should be scaled down")

		require.Eventually(t, func() bool {
			_, err := getPDB("pdb-test")
			return apierrors.IsNotFound(err)
		}, 10*time.Second, 250*time.Millisecond, "data PDB should be deleted for a single replica")

		_, err := getPDB("pdb-test-sentinel")
		assert.NoError(t, err, "the sentinel budget is independent of the data replica count")
	})

	t.Run("disabling removes the remaining budget", func(t *testing.T) {
		require.Eventually(t, func() bool {
			current := &vkov1.Valkey{}
			if err := k8sClient.Get(ctx, types.NamespacedName{Name: "pdb-test", Namespace: "default"}, current); err != nil {
				return false
			}
			current.Spec.PodDisruptionBudget.Enabled = false
			return k8sClient.Update(ctx, current) == nil
		}, 10*time.Second, 250*time.Millisecond, "PDBs should be disabled")

		require.Eventually(t, func() bool {
			_, err := getPDB("pdb-test-sentinel")
			return apierrors.IsNotFound(err)
		}, 10*time.Second, 250*time.Millisecond, "sentinel PDB should be deleted when disabled")
	})
}
