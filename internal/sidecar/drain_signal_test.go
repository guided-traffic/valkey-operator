package sidecar

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/guided-traffic/valkey-operator/internal/common"
	"github.com/guided-traffic/valkey-operator/internal/valkeyclient"
)

// The Valkey container's preStop hook blocks until this handler writes the marker
// (internal/builder/statefulset.go, drainPreStop). Every exit path of Handle must
// therefore release it: a path that forgets costs each pod deletion the full
// preStop bound, which is the opposite of what the handshake exists to buy.

// flippingRoleDetector answers with first on the initial call and with then on every
// later one, which is the shape of a real failover seen from the draining pod.
type flippingRoleDetector struct {
	mu    sync.Mutex
	first string
	then  string
	calls int
}

func (d *flippingRoleDetector) DetectRole() (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.calls++
	if d.calls == 1 {
		return d.first, nil
	}
	return d.then, nil
}

// signalDirOf points a handler at a temporary directory and returns the marker path.
func signalDirOf(t *testing.T, h *DrainHandler) string {
	t.Helper()
	dir := t.TempDir()
	h.signalDir = dir
	return filepath.Join(dir, common.DrainCompleteFile)
}

// TestSignalDrainComplete_EveryExitPathReleasesValkey walks the ways out of Handle.
// They differ in what the drain achieves and agree in what they owe the Valkey
// container.
func TestSignalDrainComplete_EveryExitPathReleasesValkey(t *testing.T) {
	promotable := &mockValkeyCommander{
		infoResult: &valkeyclient.ReplicationInfo{
			Role: "slave", MasterLinkStatus: "up",
		},
	}

	cases := []struct {
		name    string
		handler func(t *testing.T) *DrainHandler
	}{
		{
			name: "role detection fails",
			handler: func(_ *testing.T) *DrainHandler {
				return newTestDrainHandler(
					&changingRoleDetector{err: assert.AnError},
					&mockPodPatcher{},
					&mockValkeyClientFactory{clients: map[string]*mockValkeyCommander{}})
			},
		},
		{
			name: "pod is a replica and has nothing to do",
			handler: func(_ *testing.T) *DrainHandler {
				return newTestDrainHandler(
					&changingRoleDetector{role: "slave"},
					&mockPodPatcher{},
					&mockValkeyClientFactory{clients: map[string]*mockValkeyCommander{}})
			},
		},
		{
			name: "master finds no promotable peer",
			handler: func(_ *testing.T) *DrainHandler {
				return newTestDrainHandler(
					&changingRoleDetector{role: common.RoleMaster},
					&mockPodPatcher{},
					&mockValkeyClientFactory{clients: map[string]*mockValkeyCommander{}})
			},
		},
		{
			name: "master promotes a peer and waits out the role change",
			handler: func(_ *testing.T) *DrainHandler {
				// Master on the first read, replica afterwards: Handle has to enter the
				// failover, and waitForRoleChange has to leave it. A detector that is
				// already a replica would take the replica branch and test nothing.
				detector := &flippingRoleDetector{first: common.RoleMaster, then: "slave"}
				return newTestDrainHandler(detector, &mockPodPatcher{}, &mockValkeyClientFactory{
					clients: map[string]*mockValkeyCommander{
						"test-1.test-headless.default.svc.cluster.local:6379": promotable,
						"test-2.test-headless.default.svc.cluster.local:6379": promotable,
					},
				})
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.handler(t)
			marker := signalDirOf(t, h)

			_ = h.Handle(context.Background())

			content, err := os.ReadFile(marker) //nolint:gosec // path is built from t.TempDir
			require.NoError(t, err,
				"the Valkey container waits on this file; without it the pod deletion stalls "+
					"until the preStop bound expires")
			assert.NotEmpty(t, content, "the marker carries the time the drain finished")
		})
	}
}

// TestSignalDrainComplete_MissingMountIsNotAFailure covers the clusters the operator
// does not mount the volume for -- Sentinel and standalone. The mount is the switch,
// so an absent directory means "handshake not active here" and must stay silent
// rather than log an error on every pod shutdown in the fleet.
func TestSignalDrainComplete_MissingMountIsNotAFailure(t *testing.T) {
	h := newTestDrainHandler(
		&changingRoleDetector{role: "slave"},
		&mockPodPatcher{},
		&mockValkeyClientFactory{clients: map[string]*mockValkeyCommander{}})
	h.signalDir = filepath.Join(t.TempDir(), "never-mounted")

	require.NotPanics(t, func() {
		require.NoError(t, h.Handle(context.Background()))
	})
	assert.NoFileExists(t, filepath.Join(h.signalDir, common.DrainCompleteFile))
}

// TestSignalDrainComplete_DefaultsToTheMountPath pins the contract with the builder:
// an unconfigured handler writes where the operator mounts the volume. The two
// constants are shared, but nothing else forces the production path to be used.
func TestSignalDrainComplete_DefaultsToTheMountPath(t *testing.T) {
	h := &DrainHandler{}
	assert.Empty(t, h.signalDir,
		"the production handler carries no directory, so the default is what runs in the cluster")
	assert.Equal(t, "/var/run/vko", common.DrainSignalMountPath)
	assert.Equal(t, "drain-complete", common.DrainCompleteFile)
}
