package builder

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/common"
)

// withAntiAffinity sets the anti-affinity block.
func withAntiAffinity(mode, topologyKey string) func(*vkov1.Valkey) {
	return func(v *vkov1.Valkey) {
		v.Spec.AntiAffinity = &vkov1.AntiAffinitySpec{Mode: mode, TopologyKey: topologyKey}
	}
}

func withReplicas(replicas int32) func(*vkov1.Valkey) {
	return func(v *vkov1.Valkey) { v.Spec.Replicas = replicas }
}

// --- defaults (block omitted → off) ---

func TestBuildPodAntiAffinity_DefaultOffRendersNothing(t *testing.T) {
	v := newTestValkey("test", withReplicas(3))

	assert.Nil(t, BuildPodAntiAffinity(v, common.ComponentValkey),
		"an omitted antiAffinity block must not change scheduling")
}

func TestBuildPodAntiAffinity_ExplicitOffRendersNothing(t *testing.T) {
	v := newTestValkey("test", withReplicas(3), withAntiAffinity(vkov1.AntiAffinityModeOff, ""))

	assert.Nil(t, BuildPodAntiAffinity(v, common.ComponentValkey))
}

func TestBuildPodAntiAffinity_SoftOnHostname(t *testing.T) {
	v := newTestValkey("test", withReplicas(3), withAntiAffinity(vkov1.AntiAffinityModeSoft, ""))

	affinity := BuildPodAntiAffinity(v, common.ComponentValkey)

	require.NotNil(t, affinity)
	require.NotNil(t, affinity.PodAntiAffinity)
	assert.Empty(t, affinity.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution,
		"soft must not block scheduling")

	preferred := affinity.PodAntiAffinity.PreferredDuringSchedulingIgnoredDuringExecution
	require.Len(t, preferred, 1)
	assert.Equal(t, int32(100), preferred[0].Weight)
	assert.Equal(t, "kubernetes.io/hostname", preferred[0].PodAffinityTerm.TopologyKey)
}

// --- component scoping ---

func TestBuildPodAntiAffinity_SelectorIsComponentScoped(t *testing.T) {
	v := newTestValkey("test", withReplicas(3), withSentinel(3),
		withAntiAffinity(vkov1.AntiAffinityModeSoft, ""))

	data := BuildPodAntiAffinity(v, common.ComponentValkey)
	sentinel := BuildPodAntiAffinity(v, common.ComponentSentinel)

	require.NotNil(t, data)
	require.NotNil(t, sentinel)

	dataSelector := data.PodAntiAffinity.PreferredDuringSchedulingIgnoredDuringExecution[0].
		PodAffinityTerm.LabelSelector
	sentinelSelector := sentinel.PodAntiAffinity.PreferredDuringSchedulingIgnoredDuringExecution[0].
		PodAffinityTerm.LabelSelector

	assert.Equal(t, map[string]string{
		common.LabelInstance:  "test",
		common.LabelManagedBy: common.ManagedBy,
		common.LabelComponent: common.ComponentValkey,
	}, dataSelector.MatchLabels)
	assert.Equal(t, common.ComponentSentinel, sentinelSelector.MatchLabels[common.LabelComponent],
		"sentinel pods must repel only sentinel pods")
}

// --- modes ---

func TestBuildPodAntiAffinity_HardMode(t *testing.T) {
	v := newTestValkey("test", withReplicas(3), withAntiAffinity(vkov1.AntiAffinityModeHard, ""))

	affinity := BuildPodAntiAffinity(v, common.ComponentValkey)

	require.NotNil(t, affinity)
	assert.Empty(t, affinity.PodAntiAffinity.PreferredDuringSchedulingIgnoredDuringExecution)

	required := affinity.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution
	require.Len(t, required, 1)
	assert.Equal(t, "kubernetes.io/hostname", required[0].TopologyKey)
	assert.Equal(t, common.ComponentValkey, required[0].LabelSelector.MatchLabels[common.LabelComponent])
}

func TestBuildPodAntiAffinity_ExplicitSoftMode(t *testing.T) {
	v := newTestValkey("test", withReplicas(2), withAntiAffinity(vkov1.AntiAffinityModeSoft, ""))

	affinity := BuildPodAntiAffinity(v, common.ComponentValkey)

	require.NotNil(t, affinity)
	assert.Len(t, affinity.PodAntiAffinity.PreferredDuringSchedulingIgnoredDuringExecution, 1)
	assert.Empty(t, affinity.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution)
}

func TestBuildPodAntiAffinity_UnknownModeFallsBackToOff(t *testing.T) {
	v := newTestValkey("test", withReplicas(3), withAntiAffinity("bogus", ""))

	assert.Nil(t, BuildPodAntiAffinity(v, common.ComponentValkey),
		"an unvalidated value must not silently become any constraint")
}

// --- topologyKey ---

func TestBuildPodAntiAffinity_CustomTopologyKey(t *testing.T) {
	v := newTestValkey("test", withReplicas(3),
		withAntiAffinity(vkov1.AntiAffinityModeHard, "topology.kubernetes.io/zone"))

	affinity := BuildPodAntiAffinity(v, common.ComponentValkey)

	require.NotNil(t, affinity)
	assert.Equal(t, "topology.kubernetes.io/zone",
		affinity.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution[0].TopologyKey)
}

// --- single-replica skip (mode soft, so the replica rule is what skips) ---

func TestBuildPodAntiAffinity_SkippedForSingleReplica(t *testing.T) {
	v := newTestValkey("test", withReplicas(1), withAntiAffinity(vkov1.AntiAffinityModeSoft, ""))

	assert.Nil(t, BuildPodAntiAffinity(v, common.ComponentValkey))
}

func TestBuildPodAntiAffinity_SkippedForSingleSentinel(t *testing.T) {
	v := newTestValkey("test", withReplicas(3), withSentinel(1),
		withAntiAffinity(vkov1.AntiAffinityModeSoft, ""))

	assert.NotNil(t, BuildPodAntiAffinity(v, common.ComponentValkey))
	assert.Nil(t, BuildPodAntiAffinity(v, common.ComponentSentinel))
}

func TestBuildPodAntiAffinity_SkippedWhenSentinelDisabled(t *testing.T) {
	v := newTestValkey("test", withReplicas(3), withAntiAffinity(vkov1.AntiAffinityModeSoft, ""))

	assert.Nil(t, BuildPodAntiAffinity(v, common.ComponentSentinel))
}

// --- rendered into the pod templates ---

func TestBuildStatefulSet_CarriesAntiAffinity(t *testing.T) {
	v := newTestValkey("test", withReplicas(3), withAntiAffinity(vkov1.AntiAffinityModeSoft, ""))

	sts := BuildStatefulSet(v, "operator:test")

	require.NotNil(t, sts.Spec.Template.Spec.Affinity)
	assert.Len(t, sts.Spec.Template.Spec.Affinity.PodAntiAffinity.
		PreferredDuringSchedulingIgnoredDuringExecution, 1)
}

func TestBuildStatefulSet_DefaultHasNoAffinity(t *testing.T) {
	v := newTestValkey("test", withReplicas(3))

	sts := BuildStatefulSet(v, "operator:test")

	assert.Nil(t, sts.Spec.Template.Spec.Affinity,
		"without an antiAffinity block the pod template must stay as it was before the feature")
}

func TestBuildSentinelStatefulSet_CarriesAntiAffinity(t *testing.T) {
	v := newTestValkey("test", withReplicas(3), withSentinel(3),
		withAntiAffinity(vkov1.AntiAffinityModeHard, ""))

	sts := BuildSentinelStatefulSet(v)

	require.NotNil(t, sts.Spec.Template.Spec.Affinity)
	required := sts.Spec.Template.Spec.Affinity.PodAntiAffinity.
		RequiredDuringSchedulingIgnoredDuringExecution
	require.Len(t, required, 1)
	assert.Equal(t, common.ComponentSentinel, required[0].LabelSelector.MatchLabels[common.LabelComponent])
}

func TestBuildStatefulSet_SingleReplicaHasNoAffinity(t *testing.T) {
	v := newTestValkey("test", withReplicas(1), withAntiAffinity(vkov1.AntiAffinityModeSoft, ""))

	sts := BuildStatefulSet(v, "operator:test")

	assert.Nil(t, sts.Spec.Template.Spec.Affinity,
		"a standalone instance must not get a pointless pod-spec hash change")
}

// --- pod-spec hash: a mode change must trigger a rolling update ---

func TestComputePodSpecHash_ChangesWithAntiAffinityMode(t *testing.T) {
	off := newTestValkey("test", withReplicas(3))
	soft := newTestValkey("test", withReplicas(3), withAntiAffinity(vkov1.AntiAffinityModeSoft, ""))
	hard := newTestValkey("test", withReplicas(3), withAntiAffinity(vkov1.AntiAffinityModeHard, ""))

	offHash := ComputePodSpecHash(off, "operator:test")
	softHash := ComputePodSpecHash(soft, "operator:test")
	hardHash := ComputePodSpecHash(hard, "operator:test")

	assert.NotEqual(t, offHash, softHash, "opting into soft must roll the pods")
	assert.NotEqual(t, softHash, hardHash, "switching soft to hard must roll the pods")
}

func TestComputeSentinelPodSpecHash_ChangesWithTopologyKey(t *testing.T) {
	host := newTestValkey("test", withReplicas(3), withSentinel(3),
		withAntiAffinity(vkov1.AntiAffinityModeSoft, ""))
	zone := newTestValkey("test", withReplicas(3), withSentinel(3),
		withAntiAffinity(vkov1.AntiAffinityModeSoft, "topology.kubernetes.io/zone"))

	assert.NotEqual(t, ComputeSentinelPodSpecHash(host), ComputeSentinelPodSpecHash(zone))
}
