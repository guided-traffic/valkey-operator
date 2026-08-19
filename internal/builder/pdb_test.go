package builder

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/common"
)

// pdbEnabled turns on PDBs with the given maxUnavailable (nil = default).
func pdbEnabled(maxUnavailable *int32) func(*vkov1.Valkey) {
	return func(v *vkov1.Valkey) {
		v.Spec.PodDisruptionBudget = &vkov1.PodDisruptionBudgetSpec{
			Enabled:        true,
			MaxUnavailable: maxUnavailable,
		}
	}
}

func withSentinel(replicas int32) func(*vkov1.Valkey) {
	return func(v *vkov1.Valkey) {
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: replicas}
	}
}

// --- names ---

func TestPodDisruptionBudgetNames(t *testing.T) {
	v := newTestValkey("my-valkey")
	assert.Equal(t, "my-valkey", PodDisruptionBudgetName(v))
	assert.Equal(t, "my-valkey-sentinel", SentinelPodDisruptionBudgetName(v))
}

// --- data PDB ---

func TestBuildValkeyPodDisruptionBudget_Defaults(t *testing.T) {
	v := newTestValkey("test", pdbEnabled(nil), func(v *vkov1.Valkey) { v.Spec.Replicas = 3 })

	pdb := BuildValkeyPodDisruptionBudget(v)

	assert.Equal(t, "test", pdb.Name)
	assert.Equal(t, "default", pdb.Namespace)
	require.NotNil(t, pdb.Spec.MaxUnavailable)
	assert.Equal(t, intstr.FromInt32(1), *pdb.Spec.MaxUnavailable)
	assert.Nil(t, pdb.Spec.MinAvailable, "data PDB must not set minAvailable")
	require.NotNil(t, pdb.Spec.Selector)
	assert.Equal(t, common.SelectorLabels(v, common.ComponentValkey), pdb.Spec.Selector.MatchLabels)
	assert.Equal(t, common.ComponentValkey, pdb.Labels[common.LabelComponent])
}

func TestBuildValkeyPodDisruptionBudget_CustomMaxUnavailable(t *testing.T) {
	maxUnavailable := int32(2)
	v := newTestValkey("test", pdbEnabled(&maxUnavailable), func(v *vkov1.Valkey) { v.Spec.Replicas = 5 })

	pdb := BuildValkeyPodDisruptionBudget(v)

	require.NotNil(t, pdb.Spec.MaxUnavailable)
	assert.Equal(t, intstr.FromInt32(2), *pdb.Spec.MaxUnavailable)
}

// --- Sentinel PDB ---

func TestBuildSentinelPodDisruptionBudget_QuorumMinAvailable(t *testing.T) {
	tests := []struct {
		name             string
		sentinelReplicas int32
		expectedQuorum   int32
	}{
		{name: "three sentinels", sentinelReplicas: 3, expectedQuorum: 2},
		{name: "five sentinels", sentinelReplicas: 5, expectedQuorum: 3},
		// Two sentinels have no majority below the full set: the PDB then permits
		// no voluntary disruption at all.
		{name: "two sentinels", sentinelReplicas: 2, expectedQuorum: 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := newTestValkey("test", pdbEnabled(nil), withSentinel(tt.sentinelReplicas))

			pdb := BuildSentinelPodDisruptionBudget(v)

			assert.Equal(t, "test-sentinel", pdb.Name)
			require.NotNil(t, pdb.Spec.MinAvailable)
			assert.Equal(t, intstr.FromInt32(tt.expectedQuorum), *pdb.Spec.MinAvailable)
			assert.Nil(t, pdb.Spec.MaxUnavailable, "sentinel PDB must not set maxUnavailable")
			require.NotNil(t, pdb.Spec.Selector)
			assert.Equal(t, common.SelectorLabels(v, common.ComponentSentinel), pdb.Spec.Selector.MatchLabels)
		})
	}
}

// TestBuildSentinelPodDisruptionBudget_IgnoresMaxUnavailable guards the decision
// that the Sentinel budget is quorum-derived and cannot be weakened from the spec.
func TestBuildSentinelPodDisruptionBudget_IgnoresMaxUnavailable(t *testing.T) {
	maxUnavailable := int32(3)
	v := newTestValkey("test", pdbEnabled(&maxUnavailable), withSentinel(3))

	pdb := BuildSentinelPodDisruptionBudget(v)

	require.NotNil(t, pdb.Spec.MinAvailable)
	assert.Equal(t, intstr.FromInt32(2), *pdb.Spec.MinAvailable)
	assert.Nil(t, pdb.Spec.MaxUnavailable)
}

// --- quorum helper ---

func TestSentinelQuorumFor(t *testing.T) {
	assert.Equal(t, int32(SentinelQuorum), SentinelQuorumFor(0), "unset replicas fall back to the default quorum")
	assert.Equal(t, int32(1), SentinelQuorumFor(1))
	assert.Equal(t, int32(2), SentinelQuorumFor(2))
	assert.Equal(t, int32(2), SentinelQuorumFor(3))
	assert.Equal(t, int32(3), SentinelQuorumFor(4))
	assert.Equal(t, int32(3), SentinelQuorumFor(5))
}

// --- change detection ---

func TestPodDisruptionBudgetHasChanged(t *testing.T) {
	v := newTestValkey("test", pdbEnabled(nil), func(v *vkov1.Valkey) { v.Spec.Replicas = 3 })
	current := BuildValkeyPodDisruptionBudget(v)

	assert.False(t, PodDisruptionBudgetHasChanged(BuildValkeyPodDisruptionBudget(v), current),
		"identical specs must not be reported as changed")

	maxUnavailable := int32(2)
	changed := newTestValkey("test", pdbEnabled(&maxUnavailable), func(v *vkov1.Valkey) { v.Spec.Replicas = 3 })
	assert.True(t, PodDisruptionBudgetHasChanged(BuildValkeyPodDisruptionBudget(changed), current),
		"a different maxUnavailable must be detected")

	sentinel := newTestValkey("test", pdbEnabled(nil), withSentinel(3))
	assert.True(t, PodDisruptionBudgetHasChanged(BuildSentinelPodDisruptionBudget(sentinel), current),
		"switching from maxUnavailable to minAvailable must be detected")
}

// TestPodDisruptionBudgetHasChanged_IgnoresUnmanagedFields guards against an
// operator-vs-policy-webhook update loop: a cluster policy that injects
// unhealthyPodEvictionPolicy must not make every reconcile see a drift.
func TestPodDisruptionBudgetHasChanged_IgnoresUnmanagedFields(t *testing.T) {
	v := newTestValkey("test", pdbEnabled(nil), func(v *vkov1.Valkey) { v.Spec.Replicas = 3 })
	desired := BuildValkeyPodDisruptionBudget(v)

	current := BuildValkeyPodDisruptionBudget(v)
	policy := policyv1.AlwaysAllow
	current.Spec.UnhealthyPodEvictionPolicy = &policy

	assert.False(t, PodDisruptionBudgetHasChanged(desired, current),
		"a field the operator does not manage must not count as drift")

	ApplyPodDisruptionBudgetSpec(desired, current)
	require.NotNil(t, current.Spec.UnhealthyPodEvictionPolicy,
		"applying the desired spec must not drop unmanaged fields")
	assert.Equal(t, policyv1.AlwaysAllow, *current.Spec.UnhealthyPodEvictionPolicy)
}
