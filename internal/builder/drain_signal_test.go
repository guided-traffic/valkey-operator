package builder

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/common"
)

// The drain handshake: the sidecar tells the Valkey container when it may exit, so
// the drain handler can read its own role and find a promotion target while the
// local Valkey is still answering. Without it the kubelet SIGTERMs both containers
// at once, a Valkey with `save ""` wins the race often enough to show up in CI, and
// the drain promotes nobody -- after which the returning pod self-claims the master
// role with an empty dataset.

// containerByName returns a container of the built pod spec.
func containerByName(t *testing.T, v *vkov1.Valkey, name string) corev1.Container {
	t.Helper()
	spec := buildPodSpec(v, testOperatorImage)
	for _, c := range spec.Containers {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("container %q not found in the pod spec", name)
	return corev1.Container{}
}

// hasVolume reports whether the built pod spec carries the named volume.
func hasVolume(t *testing.T, v *vkov1.Valkey, name string) bool {
	t.Helper()
	for _, vol := range buildPodSpec(v, testOperatorImage).Volumes {
		if vol.Name == name {
			return vol.EmptyDir != nil
		}
	}
	return false
}

// drainMountPathOf returns where a container mounts the drain-signal volume, or "".
func drainMountPathOf(c corev1.Container) string {
	for _, m := range c.VolumeMounts {
		if m.Name == DrainSignalVolumeName {
			return m.MountPath
		}
	}
	return ""
}

func multiReplicaNoSentinel(name string) *vkov1.Valkey {
	return newTestValkey(name, func(v *vkov1.Valkey) { v.Spec.Replicas = 3 })
}

// TestDrainSignal_OnlyWhereTheDrainPerformsAFailover pins the scope. A Sentinel
// cluster loses at most failover latency when the drain bails, because Sentinel's
// own timer takes over, and a standalone pod has nothing to fail over to. Neither
// should pay a preStop on every pod deletion.
func TestDrainSignal_OnlyWhereTheDrainPerformsAFailover(t *testing.T) {
	cases := []struct {
		name   string
		valkey *vkov1.Valkey
		want   bool
	}{
		{
			name:   "multi-replica without sentinel",
			valkey: multiReplicaNoSentinel("no-sentinel"),
			want:   true,
		},
		{
			name: "standalone",
			valkey: newTestValkey("standalone", func(v *vkov1.Valkey) {
				v.Spec.Replicas = 1
			}),
			want: false,
		},
		{
			name: "multi-replica with sentinel",
			valkey: newTestValkey("with-sentinel", func(v *vkov1.Valkey) {
				v.Spec.Replicas = 3
				v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
			}),
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			valkey := containerByName(t, tc.valkey, ValkeyContainerName)
			sidecar := containerByName(t, tc.valkey, SidecarContainerName)

			if !tc.want {
				assert.Nil(t, valkey.Lifecycle, "no preStop where the drain performs no failover")
				assert.False(t, hasVolume(t, tc.valkey, DrainSignalVolumeName))
				assert.Empty(t, drainMountPathOf(valkey))
				assert.Empty(t, drainMountPathOf(sidecar))
				return
			}

			require.NotNil(t, valkey.Lifecycle)
			require.NotNil(t, valkey.Lifecycle.PreStop)
			require.NotNil(t, valkey.Lifecycle.PreStop.Exec)

			// The three halves have to agree or the hook waits for a file nobody can
			// write: the volume must exist, both containers must mount it at the same
			// path, and the hook must watch that path.
			assert.True(t, hasVolume(t, tc.valkey, DrainSignalVolumeName),
				"the shared volume is what lets the sidecar reach the file the hook waits for")
			assert.Equal(t, common.DrainSignalMountPath, drainMountPathOf(valkey))
			assert.Equal(t, common.DrainSignalMountPath, drainMountPathOf(sidecar),
				"the sidecar writes the marker, so it needs the same mount")

			marker := common.DrainSignalMountPath + "/" + common.DrainCompleteFile
			assert.Contains(t, strings.Join(valkey.Lifecycle.PreStop.Exec.Command, " "), marker)
		})
	}
}

// TestDrainPreStop_ExpiresInsideTheGracePeriod is the invariant that keeps the hook
// a bound rather than a hang. A sidecar that cannot write the marker at all -- it
// crashed, the volume is missing -- decides how long a pod deletion takes, and the
// hook has to give up before the kubelet stops waiting, leaving Valkey room to shut
// down on its own terms.
func TestDrainPreStop_ExpiresInsideTheGracePeriod(t *testing.T) {
	spec := buildPodSpec(multiReplicaNoSentinel("bound"), testOperatorImage)
	require.NotNil(t, spec.TerminationGracePeriodSeconds)

	assert.Less(t, int64(drainPreStopTimeoutSeconds), *spec.TerminationGracePeriodSeconds,
		"a preStop that outlives the grace period is not a bound, it is a hang the kubelet cuts short")
}

// TestDrainPreStop_ShellLoopIsBounded reads the generated command back rather than
// trusting the template: an unbounded loop here would hold every pod deletion of
// the cluster for the full grace period.
func TestDrainPreStop_ShellLoopIsBounded(t *testing.T) {
	valkey := containerByName(t, multiReplicaNoSentinel("loop"), ValkeyContainerName)
	command := valkey.Lifecycle.PreStop.Exec.Command

	require.Equal(t, "sh", command[0])
	require.Equal(t, "-c", command[1])
	script := command[2]

	assert.Contains(t, script, fmt.Sprintf("-lt %d", drainPreStopTimeoutSeconds),
		"the loop must terminate on a counter, not on the marker alone")
	assert.Contains(t, script, "exit 0",
		"the hook returns success as soon as the drain released it")
	assert.Contains(t, script, "i=$((i+1))", "the counter must actually advance")
}

// TestDrainPreStop_IsPartOfThePodSpecHash proves the hook reaches existing clusters.
// The hash rides the pod-template annotation that podTemplateChanged compares first,
// so a field the hash does not cover would silently apply to new pods only.
func TestDrainPreStop_IsPartOfThePodSpecHash(t *testing.T) {
	v := multiReplicaNoSentinel("hashed")
	withHook := buildPodSpec(v, testOperatorImage)

	withoutHook := *withHook.DeepCopy()
	for i := range withoutHook.Containers {
		if withoutHook.Containers[i].Name == ValkeyContainerName {
			withoutHook.Containers[i].Lifecycle = nil
		}
	}

	before, err := json.Marshal(withoutHook)
	require.NoError(t, err)
	after, err := json.Marshal(withHook)
	require.NoError(t, err)

	assert.NotEqual(t, string(before), string(after),
		"the preStop must be inside the hashed spec, otherwise running pods never get it")
	assert.Equal(t, ComputePodSpecHash(v, testOperatorImage), ComputePodSpecHash(v, testOperatorImage),
		"the hash must stay stable for an unchanged spec")
}
