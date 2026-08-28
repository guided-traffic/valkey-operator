package builder

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
)

// The invariant this file pins: the ServiceAccount token reaches the sidecar
// container and nothing else (ADR 0012 D8 step 4).
//
// It is a file of its own because the property is not about any one topology.
// The mount is what carries the "pods: patch on this cluster's data pods" grant,
// and every container that holds it can forge the instanceRole label, the drain
// stamp, all three pod-template hashes, ownerReferences, finalizers and the
// container image. A future container added to the pod inherits the token unless
// somebody remembers -- these tests are that memory.

// tokenTopologies are the pod shapes the token invariant has to hold for. Each
// adds a container or an init container to the data pod, which is exactly what
// could reintroduce the leak.
func tokenTopologies() []struct {
	name string
	v    *vkov1.Valkey
} {
	return []struct {
		name string
		v    *vkov1.Valkey
	}{
		{"standalone", newTestValkey("test")},
		{"multi-replica without sentinel", newTestValkey("test", func(v *vkov1.Valkey) {
			v.Spec.Replicas = 3
		})},
		{"sentinel", newTestValkey("test", func(v *vkov1.Valkey) {
			v.Spec.Replicas = 3
			v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
		})},
		{"tls", newTestValkey("test", func(v *vkov1.Valkey) {
			v.Spec.Replicas = 3
			v.Spec.TLS = &vkov1.TLSSpec{Enabled: true}
		})},
		{"metrics exporter", newTestValkey("test", func(v *vkov1.Valkey) {
			v.Spec.Metrics = &vkov1.MetricsSpec{Enabled: true}
		})},
		{"everything", newTestValkey("test", func(v *vkov1.Valkey) {
			v.Spec.Replicas = 3
			v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
			v.Spec.TLS = &vkov1.TLSSpec{Enabled: true}
			v.Spec.Metrics = &vkov1.MetricsSpec{Enabled: true}
		})},
	}
}

// mountsTokenPath reports whether the container mounts anything at the path
// client-go reads the in-cluster credentials from. The path is the thing that
// matters, not the volume name: a mount of any volume there is a token as far as
// rest.InClusterConfig is concerned.
func mountsTokenPath(c corev1.Container) bool {
	for _, m := range c.VolumeMounts {
		if m.MountPath == ServiceAccountTokenMountPath {
			return true
		}
	}
	return false
}

func TestBuildStatefulSet_TokenReachesTheSidecarAndNothingElse(t *testing.T) {
	for _, tc := range tokenTopologies() {
		t.Run(tc.name, func(t *testing.T) {
			spec := BuildStatefulSet(tc.v, testOperatorImage).Spec.Template.Spec

			require.NotNil(t, spec.AutomountServiceAccountToken,
				"an unset flag means the admission plugin mounts the token into every container")
			assert.False(t, *spec.AutomountServiceAccountToken)

			var withToken []string
			for _, c := range append(append([]corev1.Container{}, spec.InitContainers...), spec.Containers...) {
				if mountsTokenPath(c) {
					withToken = append(withToken, c.Name)
				}
			}
			assert.Equal(t, []string{SidecarContainerName}, withToken,
				"exactly the sidecar may reach the Kubernetes API from a data pod")
		})
	}
}

func TestBuildStatefulSet_TokenVolumeIsPresentOnEveryTopology(t *testing.T) {
	for _, tc := range tokenTopologies() {
		t.Run(tc.name, func(t *testing.T) {
			spec := BuildStatefulSet(tc.v, testOperatorImage).Spec.Template.Spec

			count := 0
			for _, vol := range spec.Volumes {
				if vol.Name == SidecarTokenVolumeName {
					count++
				}
			}
			assert.Equal(t, 1, count,
				"the projection is appended unconditionally; the sidecar runs in every topology")
		})
	}
}

func TestSidecarTokenVolume_ReproducesWhatInClusterConfigReads(t *testing.T) {
	vol := sidecarTokenVolume()

	require.NotNil(t, vol.Projected)
	require.Len(t, vol.Projected.Sources, 2,
		"token and CA bundle -- and deliberately no downwardAPI namespace projection, "+
			"because rest.InClusterConfig never reads that file")

	token := vol.Projected.Sources[0].ServiceAccountToken
	require.NotNil(t, token)
	assert.Equal(t, "token", token.Path)
	require.NotNil(t, token.ExpirationSeconds)
	assert.Equal(t, int64(serviceAccountTokenExpirationSeconds), *token.ExpirationSeconds)
	assert.Empty(t, token.Audience, "the default audience is the API server; a narrower one would not authenticate")

	require.NotNil(t, vol.Projected.DefaultMode)
	assert.Equal(t, int32(0o644), *vol.Projected.DefaultMode,
		"0o420 is r---w---- and the sidecar cannot read its own token; the 420 seen in "+
			"manifests is decimal for 0644")

	ca := vol.Projected.Sources[1].ConfigMap
	require.NotNil(t, ca)
	assert.Equal(t, rootCAConfigMapName, ca.Name)
	require.Len(t, ca.Items, 1)
	assert.Equal(t, corev1.KeyToPath{Key: TLSCACertKey, Path: TLSCACertKey}, ca.Items[0])

	for _, src := range vol.Projected.Sources {
		assert.Nil(t, src.DownwardAPI, "the sidecar takes its namespace from POD_NAMESPACE")
	}
}

func TestSidecarTokenVolume_NameDoesNotCollideWithTheAdmissionPlugin(t *testing.T) {
	// The plugin adopts the first volume whose name carries this prefix and mounts
	// it into every container. Colliding would turn the flag being lost from a
	// visible mistake into the exact leak this change closes.
	assert.False(t, strings.HasPrefix(SidecarTokenVolumeName, "kube-api-access-"))
}

func TestSidecarTokenMountPath_IsTheOneClientGoReads(t *testing.T) {
	// Hard-coded in client-go (rest/config.go) and not configurable. A test rather
	// than a comment because the failure is a sidecar that never becomes ready.
	assert.Equal(t, "/var/run/secrets/kubernetes.io/serviceaccount", ServiceAccountTokenMountPath)
}

func TestBuildSentinelStatefulSet_MountsNoTokenAtAll(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
	})

	spec := BuildSentinelStatefulSet(v).Spec.Template.Spec

	require.NotNil(t, spec.AutomountServiceAccountToken)
	assert.False(t, *spec.AutomountServiceAccountToken,
		"Sentinel runs valkey-sentinel and valkey-cli, never the Kubernetes API")

	for _, c := range append(append([]corev1.Container{}, spec.InitContainers...), spec.Containers...) {
		assert.False(t, mountsTokenPath(c), "container %s mounts a token it never uses", c.Name)
	}
	for _, vol := range spec.Volumes {
		assert.Nil(t, vol.Projected, "volume %s projects something; Sentinel needs no projection", vol.Name)
	}
}

// --- convergence ---
//
// The volume list changing from 2 entries to 3 is what carries the introduction
// of this change onto existing clusters. Flipping the flag back on a live
// StatefulSet changes no volume and no container, so it needs its own comparison
// -- the same hole ObserverDeploymentHasChanged closed for the observer.

func TestPodSpecChanged_AutomountFlagIsCompared(t *testing.T) {
	tests := []struct {
		name    string
		desired *bool
		current *bool
		want    bool
	}{
		{"unchanged false", ptr.To(false), ptr.To(false), false},
		{"flipped back to true", ptr.To(false), ptr.To(true), true},
		{"flipped back to the nil default", ptr.To(false), nil, true},
		{"nil equals explicit true", nil, ptr.To(true), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			desired := corev1.PodSpec{AutomountServiceAccountToken: tc.desired}
			current := corev1.PodSpec{AutomountServiceAccountToken: tc.current}
			assert.Equal(t, tc.want, podSpecChanged(desired, current))
		})
	}
}

func TestStatefulSetHasChanged_ConvergesTheAutomountFlagBack(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) { v.Spec.Replicas = 3 })

	desired := BuildStatefulSet(v, testOperatorImage)
	current := desired.DeepCopy()
	current.Spec.Template.Spec.AutomountServiceAccountToken = ptr.To(true)

	assert.True(t, StatefulSetHasChanged(desired, current))
	assert.False(t, StatefulSetHasChanged(desired, desired.DeepCopy()))
}

func TestSentinelStatefulSetHasChanged_ConvergesTheAutomountFlagBack(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
	})

	desired := BuildSentinelStatefulSet(v)
	current := desired.DeepCopy()
	current.Spec.Template.Spec.AutomountServiceAccountToken = nil

	assert.True(t, SentinelStatefulSetHasChanged(desired, current))
	assert.False(t, SentinelStatefulSetHasChanged(desired, desired.DeepCopy()))
}
