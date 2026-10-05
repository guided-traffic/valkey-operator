package builder

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
)

// The pod metadata record is a function of the user-supplied maps alone: the key
// order of a map literal and the difference between an absent and an empty map
// must not move it, or an unchanged CR would roll its pods.
func TestComputePodMetadataHash_OrderAndEmptinessDoNotMoveIt(t *testing.T) {
	a := ComputePodMetadataHash(map[string]string{"a": "1", "b": "2"}, map[string]string{"x": "1", "y": "2"})
	b := ComputePodMetadataHash(map[string]string{"b": "2", "a": "1"}, map[string]string{"y": "2", "x": "1"})
	assert.Equal(t, a, b)

	assert.Equal(t, ComputePodMetadataHash(nil, nil), ComputePodMetadataHash(map[string]string{}, map[string]string{}))
	assert.Len(t, ComputePodMetadataHash(nil, nil), 8)
}

// Every change a CR author can make to the maps moves the record: adding, removing
// and changing an entry, and moving an entry between labels and annotations.
func TestComputePodMetadataHash_EveryChangeMovesIt(t *testing.T) {
	base := ComputePodMetadataHash(map[string]string{"team": "a"}, map[string]string{"note": "x"})
	for name, other := range map[string]string{
		"label added":              ComputePodMetadataHash(map[string]string{"team": "a", "tier": "db"}, map[string]string{"note": "x"}),
		"label removed":            ComputePodMetadataHash(nil, map[string]string{"note": "x"}),
		"label value changed":      ComputePodMetadataHash(map[string]string{"team": "b"}, map[string]string{"note": "x"}),
		"annotation added":         ComputePodMetadataHash(map[string]string{"team": "a"}, map[string]string{"note": "x", "more": ""}),
		"annotation removed":       ComputePodMetadataHash(map[string]string{"team": "a"}, nil),
		"annotation value changed": ComputePodMetadataHash(map[string]string{"team": "a"}, map[string]string{"note": "y"}),
		"entry moved to labels":    ComputePodMetadataHash(map[string]string{"team": "a", "note": "x"}, nil),
		"every entry removed":      ComputePodMetadataHash(nil, nil),
	} {
		assert.NotEqual(t, base, other, name)
	}
}

// A metadata record that differs replaces even a single data pod whose sidecar
// deferral stands (ADR 0007 D6), so a release that changes the recipe restarts
// every non-persistent single pod at the upgrade, with its dataset. This test fails
// that release first; the decision it asks for is ADR 0007 D7's.
func TestComputePodMetadataHash_Pinned(t *testing.T) {
	for _, tc := range []struct {
		name                string
		labels, annotations map[string]string
		want                string
	}{
		{"no maps", nil, nil, "5465b825"},
		{"one label", map[string]string{"app": "valkey"}, nil, "e7493c39"},
		{"labels and annotations",
			map[string]string{"gnp/monitoring-client": "", "gnp/k8s-api-access": ""},
			map[string]string{"example.com/annotation": "true"}, "c74c0d1d"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, ComputePodMetadataHash(tc.labels, tc.annotations),
				"the pod metadata recipe changed; decide whether this release may restart "+
					"single-replica pods at the upgrade (ADR 0007 D7), then pin the new value")
		})
	}
}

// Each tier's record is computed from that tier's maps only, and a CR without a
// sentinel block yields the record of the empty maps instead of dereferencing nil.
func TestTierPodMetadataHash_ReadsTheTiersOwnMaps(t *testing.T) {
	v := newTestValkey("meta", func(v *vkov1.Valkey) {
		v.Spec.PodLabels = map[string]string{"team": "data"}
		v.Spec.PodAnnotations = map[string]string{"note": "data"}
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3,
			PodLabels: map[string]string{"team": "sentinel"}}
	})
	assert.Equal(t, ComputePodMetadataHash(v.Spec.PodLabels, v.Spec.PodAnnotations), DataPodMetadataHash(v))
	assert.Equal(t, ComputePodMetadataHash(v.Spec.Sentinel.PodLabels, nil), SentinelPodMetadataHash(v))
	assert.Equal(t, ComputePodMetadataHash(nil, nil), SentinelPodMetadataHash(newTestValkey("nosentinel")))
}

// The record goes onto the carrier container of the built StatefulSet, once, and a
// second stamp replaces the value instead of adding a duplicate env entry, which
// the API server would reject together with the StatefulSet write. The pod-spec
// hash is computed from the builder's own spec and does not move with it, so a
// single pod can tell a metadata change from a sidecar bump (ADR 0007 D7).
func TestStampPodMetadataHash_OneEntryOnTheCarrier(t *testing.T) {
	v := newTestValkey("meta", func(v *vkov1.Valkey) { v.Spec.Replicas = 3 })
	sts := BuildStatefulSet(v, "op:test")
	before := sts.Spec.Template.Annotations[AnnotationPodSpecHash]

	StampPodMetadataHash(sts, SidecarContainerName, "first")
	StampPodMetadataHash(sts, SidecarContainerName, "second")

	assert.Equal(t, "second", RecordedPodMetadataHash(&sts.Spec.Template.Spec))
	for _, c := range sts.Spec.Template.Spec.Containers {
		count := 0
		for _, env := range c.Env {
			if env.Name == PodMetadataHashEnvName {
				count++
			}
		}
		if c.Name == SidecarContainerName {
			assert.Equal(t, 1, count, "the sidecar carries the record exactly once")
		} else {
			assert.Zero(t, count, "%s carries no record", c.Name)
		}
	}

	labelled := v.DeepCopy()
	labelled.Spec.PodLabels = map[string]string{"team": "data"}
	assert.Equal(t, before, ComputePodSpecHash(labelled, "op:test"), "the pod-spec hash is not the carrier")
	assert.Equal(t, ComputeSentinelPodSpecHash(v), ComputeSentinelPodSpecHash(labelled))
}

// A spec without the record reads as the empty string, which every consumer treats
// as "cannot tell" (ADR 0007 D3) on the template side.
func TestRecordedPodMetadataHash_Absent(t *testing.T) {
	assert.Empty(t, RecordedPodMetadataHash(nil))
	assert.Empty(t, RecordedPodMetadataHash(&corev1.PodSpec{Containers: []corev1.Container{{Name: "x"}}}))

	sts := BuildSentinelStatefulSet(newTestValkey("s", func(v *vkov1.Valkey) {
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
	}))
	require.Empty(t, RecordedPodMetadataHash(&sts.Spec.Template.Spec), "the builder itself stamps nothing")
	StampPodMetadataHash(sts, SentinelContainerName, "h")
	assert.Equal(t, "h", RecordedPodMetadataHash(&sts.Spec.Template.Spec))
}
