package sidecar

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/guided-traffic/valkey-operator/internal/common"
)

// mockDrainRunner records how the run loop invoked the drain step.
type mockDrainRunner struct {
	mu       sync.Mutex
	calls    int
	deadline time.Time
	err      error
}

func (m *mockDrainRunner) Handle(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	m.deadline, _ = ctx.Deadline()
	return m.err
}

func (m *mockDrainRunner) snapshot() (int, time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls, m.deadline
}

// runLoopConfig is the minimal config the run loop needs on a loopback host.
func runLoopConfig() Config {
	return Config{
		PodName:      "test-0",
		PodNamespace: "default",
		HealthAddr:   "127.0.0.1:0",
		PollInterval: 20 * time.Millisecond,
	}
}

func TestRunSidecar_LabelsThePodThenDrains(t *testing.T) {
	detector := &mockRoleDetector{role: common.RoleMaster}
	patcher := &mockPodPatcher{}
	drain := &mockDrainRunner{}

	cfg := runLoopConfig()
	cfg.FailoverTimeout = 3 * time.Second

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()

	require.NoError(t, runSidecar(ctx, cfg, detector, patcher, drain))

	require.Len(t, patcher.patches, 1)
	assert.Equal(t, "test-0", patcher.patches[0].name)
	assert.Equal(t, common.RoleMaster, patcher.patches[0].labelValue)

	calls, deadline := drain.snapshot()
	assert.Equal(t, 1, calls, "the drain must run exactly once, after the context was cancelled")
	assert.WithinDuration(t, time.Now().Add(3*time.Second), deadline, time.Second)
}

func TestRunSidecar_DefaultsTheDrainTimeout(t *testing.T) {
	drain := &mockDrainRunner{}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()

	require.NoError(t, runSidecar(ctx, runLoopConfig(), &mockRoleDetector{role: common.RoleReplica}, &mockPodPatcher{}, drain))

	_, deadline := drain.snapshot()
	assert.WithinDuration(t, time.Now().Add(60*time.Second), deadline, 2*time.Second)
}

func TestRunSidecar_DrainErrorStillShutsDownCleanly(t *testing.T) {
	drain := &mockDrainRunner{err: assert.AnError}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()

	// A failed drain is logged, not propagated: the process is exiting anyway.
	require.NoError(t, runSidecar(ctx, runLoopConfig(), &mockRoleDetector{role: common.RoleMaster}, &mockPodPatcher{}, drain))

	calls, _ := drain.snapshot()
	assert.Equal(t, 1, calls)
}

func TestRunSidecar_HealthServerBindFailureDoesNotStopTheSidecar(t *testing.T) {
	drain := &mockDrainRunner{}
	cfg := runLoopConfig()
	cfg.HealthAddr = "127.0.0.1:not-a-port"

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()

	require.NoError(t, runSidecar(ctx, cfg, &mockRoleDetector{role: common.RoleMaster}, &mockPodPatcher{}, drain))

	calls, _ := drain.snapshot()
	assert.Equal(t, 1, calls)
}

func TestRunSidecar_SentinelCrossCheckOverridesTheLocalRole(t *testing.T) {
	// Sentinel names a different pod as master, so the local "master" answer
	// must not reach the -rw Service.
	sentinelAddr := fakeValkeyServer(t, nil, func([]string) string {
		return sentinelMasterReply("test-1.test-headless.default.svc.cluster.local")
	})

	patcher := &mockPodPatcher{}
	cfg := runLoopConfig()
	cfg.SentinelEnabled = true
	cfg.SentinelAddrs = sentinelAddr
	cfg.SentinelMonitor = "mymaster"
	cfg.HeadlessSvc = "test-headless.default.svc.cluster.local"

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()

	require.NoError(t, runSidecar(ctx, cfg, &mockRoleDetector{role: common.RoleMaster}, patcher, &mockDrainRunner{}))

	require.Len(t, patcher.patches, 1)
	assert.Equal(t, common.RoleReplica, patcher.patches[0].labelValue)
}

func TestRunSidecar_SentinelQuerierError(t *testing.T) {
	cfg := runLoopConfig()
	cfg.SentinelEnabled = true
	cfg.SentinelAddrs = "127.0.0.1:26379"
	cfg.TLSEnabled = true
	cfg.TLSCACert = filepath.Join(t.TempDir(), "absent.crt")

	err := runSidecar(context.Background(), cfg, &mockRoleDetector{}, &mockPodPatcher{}, &mockDrainRunner{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "creating sentinel master querier")
}

func TestRun_RoleDetectorConstructionError(t *testing.T) {
	cfg := runLoopConfig()
	cfg.TLSEnabled = true
	cfg.TLSCACert = filepath.Join(t.TempDir(), "absent.crt")

	err := Run(context.Background(), cfg)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "creating role detector")
}

func TestRun_NeedsAnInClusterConfigForThePodPatcher(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	t.Setenv("KUBERNETES_SERVICE_PORT", "")

	cfg := runLoopConfig()
	cfg.ValkeyAddr = "127.0.0.1:6379"

	err := Run(context.Background(), cfg)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "creating pod patcher")
}
