package sidecar

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/guided-traffic/valkey-operator/internal/common"
	"github.com/guided-traffic/valkey-operator/internal/valkeyclient"
)

// --- Mock implementations for drain tests ---

// changingRoleDetector is a thread-safe mock that allows changing the role during a test.
type changingRoleDetector struct {
	mu   sync.Mutex
	role string
	err  error
}

func (d *changingRoleDetector) DetectRole() (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.role, d.err
}

func (d *changingRoleDetector) SetRole(role string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.role = role
}

// mockValkeyCommander implements ValkeyCommander for testing.
type mockValkeyCommander struct {
	mu                  sync.Mutex
	infoResult          *valkeyclient.ReplicationInfo
	infoErr             error
	sentinelFailoverErr error
	replicaOfErr        error
	pingErr             error
	replicaOfCalls      []replicaOfRecord
	failoverCalls       []string
}

type replicaOfRecord struct {
	host string
	port string
}

func (m *mockValkeyCommander) InfoReplication() (*valkeyclient.ReplicationInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.infoResult, m.infoErr
}

func (m *mockValkeyCommander) SentinelFailover(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failoverCalls = append(m.failoverCalls, name)
	return m.sentinelFailoverErr
}

func (m *mockValkeyCommander) ReplicaOf(host, port string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.replicaOfCalls = append(m.replicaOfCalls, replicaOfRecord{host, port})
	return m.replicaOfErr
}

func (m *mockValkeyCommander) Ping() error {
	return m.pingErr
}

// mockValkeyClientFactory returns pre-configured mock clients per address.
type mockValkeyClientFactory struct {
	clients map[string]*mockValkeyCommander
}

func (f *mockValkeyClientFactory) NewClient(addr string) ValkeyCommander {
	if c, ok := f.clients[addr]; ok {
		return c
	}
	return &mockValkeyCommander{
		infoErr: fmt.Errorf("no mock for address %s", addr),
	}
}

// --- Helper ---

func newTestDrainHandler(
	detector RoleDetector,
	patcher PodPatcher,
	factory ValkeyClientFactory,
	opts ...func(*DrainHandler),
) *DrainHandler {
	h := &DrainHandler{
		detector:      detector,
		patcher:       patcher,
		clientFactory: factory,
		podName:       "test-0",
		podNamespace:  "default",
		headlessSvc:   "test-headless.default.svc.cluster.local",
		replicas:      3,
		valkeyPort:    "6379",
	}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// --- Tests ---

func TestDrainHandler_ReplicaExitsImmediately(t *testing.T) {
	detector := &mockRoleDetector{role: common.RoleReplica}
	patcher := &mockPodPatcher{}
	factory := &mockValkeyClientFactory{clients: map[string]*mockValkeyCommander{}}

	handler := newTestDrainHandler(detector, patcher, factory)

	err := handler.Handle(context.Background())

	assert.NoError(t, err)
	assert.Empty(t, patcher.patches, "no label patch should occur for replica")
}

func TestDrainHandler_UnknownRoleExitsImmediately(t *testing.T) {
	detector := &mockRoleDetector{role: "loading"}
	patcher := &mockPodPatcher{}
	factory := &mockValkeyClientFactory{clients: map[string]*mockValkeyCommander{}}

	handler := newTestDrainHandler(detector, patcher, factory)

	err := handler.Handle(context.Background())

	assert.NoError(t, err)
	assert.Empty(t, patcher.patches)
}

func TestDrainHandler_DetectionErrorExitsImmediately(t *testing.T) {
	detector := &mockRoleDetector{err: errors.New("connection refused")}
	patcher := &mockPodPatcher{}
	factory := &mockValkeyClientFactory{clients: map[string]*mockValkeyCommander{}}

	handler := newTestDrainHandler(detector, patcher, factory)

	err := handler.Handle(context.Background())

	assert.NoError(t, err, "detection error should not be returned")
	assert.Empty(t, patcher.patches)
}

func TestDrainHandler_MasterSentinelFailover(t *testing.T) {
	detector := &changingRoleDetector{role: common.RoleMaster}
	patcher := &mockPodPatcher{}

	sentinelClient := &mockValkeyCommander{}
	factory := &mockValkeyClientFactory{
		clients: map[string]*mockValkeyCommander{
			"sentinel-0.test-sentinel-headless.default.svc.cluster.local:26379": sentinelClient,
		},
	}

	handler := newTestDrainHandler(detector, patcher, factory, func(h *DrainHandler) {
		h.sentinelEnabled = true
		h.sentinelMonitor = "test"
		h.sentinelAddrs = []string{
			"sentinel-0.test-sentinel-headless.default.svc.cluster.local:26379",
			"sentinel-1.test-sentinel-headless.default.svc.cluster.local:26379",
		}
	})

	// Simulate role change after sentinel failover is triggered.
	go func() {
		time.Sleep(200 * time.Millisecond)
		detector.SetRole(common.RoleReplica)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := handler.Handle(ctx)

	assert.NoError(t, err)

	// Should have patched label to draining.
	require.Len(t, patcher.patches, 1)
	assert.Equal(t, common.RoleDraining, patcher.patches[0].labelValue)

	// Sentinel failover should have been called.
	require.Len(t, sentinelClient.failoverCalls, 1)
	assert.Equal(t, "test", sentinelClient.failoverCalls[0])
}

func TestDrainHandler_MasterManualFailover(t *testing.T) {
	detector := &changingRoleDetector{role: common.RoleMaster}
	patcher := &mockPodPatcher{}

	// pod test-1 is a synced replica.
	replicaClient1 := &mockValkeyCommander{
		infoResult: &valkeyclient.ReplicationInfo{
			Role:             "slave",
			MasterLinkStatus: "up",
		},
	}
	// pod test-2 is a synced replica.
	replicaClient2 := &mockValkeyCommander{
		infoResult: &valkeyclient.ReplicationInfo{
			Role:             "slave",
			MasterLinkStatus: "up",
		},
	}

	factory := &mockValkeyClientFactory{
		clients: map[string]*mockValkeyCommander{
			"test-1.test-headless.default.svc.cluster.local:6379": replicaClient1,
			"test-2.test-headless.default.svc.cluster.local:6379": replicaClient2,
		},
	}

	handler := newTestDrainHandler(detector, patcher, factory)

	// Simulate role change after manual failover.
	go func() {
		time.Sleep(200 * time.Millisecond)
		detector.SetRole(common.RoleReplica)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := handler.Handle(ctx)

	assert.NoError(t, err)

	// Should have patched label to draining.
	require.Len(t, patcher.patches, 1)
	assert.Equal(t, common.RoleDraining, patcher.patches[0].labelValue)

	// Should have promoted test-1 (first synced replica found).
	require.Len(t, replicaClient1.replicaOfCalls, 1)
	assert.Equal(t, "NO", replicaClient1.replicaOfCalls[0].host)
	assert.Equal(t, "ONE", replicaClient1.replicaOfCalls[0].port)

	// test-2 should have been reconfigured to follow the new master.
	require.Len(t, replicaClient2.replicaOfCalls, 1)
	assert.Equal(t, "test-1.test-headless.default.svc.cluster.local", replicaClient2.replicaOfCalls[0].host)
	assert.Equal(t, "6379", replicaClient2.replicaOfCalls[0].port)
}

func TestDrainHandler_SentinelAllFail(t *testing.T) {
	detector := &mockRoleDetector{role: common.RoleMaster}
	patcher := &mockPodPatcher{}

	sentinelClient := &mockValkeyCommander{
		sentinelFailoverErr: errors.New("sentinel unreachable"),
	}

	factory := &mockValkeyClientFactory{
		clients: map[string]*mockValkeyCommander{
			"sentinel-0:26379": sentinelClient,
			"sentinel-1:26379": sentinelClient,
		},
	}

	handler := newTestDrainHandler(detector, patcher, factory, func(h *DrainHandler) {
		h.sentinelEnabled = true
		h.sentinelMonitor = "test"
		h.sentinelAddrs = []string{"sentinel-0:26379", "sentinel-1:26379"}
	})

	err := handler.Handle(context.Background())

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "all sentinels failed")
}

func TestDrainHandler_NoSyncedReplicaFound(t *testing.T) {
	detector := &mockRoleDetector{role: common.RoleMaster}
	patcher := &mockPodPatcher{}

	// Replicas are syncing (not fully synced).
	replicaClient := &mockValkeyCommander{
		infoResult: &valkeyclient.ReplicationInfo{
			Role:                 "slave",
			MasterLinkStatus:     "up",
			MasterSyncInProgress: true,
		},
	}

	factory := &mockValkeyClientFactory{
		clients: map[string]*mockValkeyCommander{
			"test-1.test-headless.default.svc.cluster.local:6379": replicaClient,
			"test-2.test-headless.default.svc.cluster.local:6379": replicaClient,
		},
	}

	handler := newTestDrainHandler(detector, patcher, factory)

	err := handler.Handle(context.Background())

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no synced replica found")
}

func TestDrainHandler_WaitForRoleChangeTimeout(t *testing.T) {
	// Role never changes from master.
	detector := &mockRoleDetector{role: common.RoleMaster}
	patcher := &mockPodPatcher{}

	sentinelClient := &mockValkeyCommander{}

	factory := &mockValkeyClientFactory{
		clients: map[string]*mockValkeyCommander{
			"sentinel-0:26379": sentinelClient,
		},
	}

	handler := newTestDrainHandler(detector, patcher, factory, func(h *DrainHandler) {
		h.sentinelEnabled = true
		h.sentinelMonitor = "test"
		h.sentinelAddrs = []string{"sentinel-0:26379"}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := handler.Handle(ctx)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "timeout waiting for failover completion")
}

func TestDrainHandler_PatchLabelFailsContinuesWithFailover(t *testing.T) {
	detector := &changingRoleDetector{role: common.RoleMaster}
	patcher := &mockPodPatcher{err: errors.New("forbidden")}

	sentinelClient := &mockValkeyCommander{}

	factory := &mockValkeyClientFactory{
		clients: map[string]*mockValkeyCommander{
			"sentinel-0:26379": sentinelClient,
		},
	}

	handler := newTestDrainHandler(detector, patcher, factory, func(h *DrainHandler) {
		h.sentinelEnabled = true
		h.sentinelMonitor = "test"
		h.sentinelAddrs = []string{"sentinel-0:26379"}
	})

	// Simulate role change.
	go func() {
		time.Sleep(200 * time.Millisecond)
		detector.SetRole(common.RoleReplica)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := handler.Handle(ctx)

	// Failover should still succeed even though label patch failed.
	assert.NoError(t, err)
	require.Len(t, sentinelClient.failoverCalls, 1)
}

func TestDrainHandler_SentinelFirstFailsSecondSucceeds(t *testing.T) {
	detector := &changingRoleDetector{role: common.RoleMaster}
	patcher := &mockPodPatcher{}

	sentinel0 := &mockValkeyCommander{
		sentinelFailoverErr: errors.New("not available"),
	}
	sentinel1 := &mockValkeyCommander{}

	factory := &mockValkeyClientFactory{
		clients: map[string]*mockValkeyCommander{
			"sentinel-0:26379": sentinel0,
			"sentinel-1:26379": sentinel1,
		},
	}

	handler := newTestDrainHandler(detector, patcher, factory, func(h *DrainHandler) {
		h.sentinelEnabled = true
		h.sentinelMonitor = "test"
		h.sentinelAddrs = []string{"sentinel-0:26379", "sentinel-1:26379"}
	})

	// Simulate role change.
	go func() {
		time.Sleep(200 * time.Millisecond)
		detector.SetRole(common.RoleReplica)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := handler.Handle(ctx)

	assert.NoError(t, err)
	// First sentinel failed, second triggered the failover.
	require.Len(t, sentinel0.failoverCalls, 1)
	require.Len(t, sentinel1.failoverCalls, 1)
}

func TestDrainHandler_ManualFailoverReplicaQueryErrors(t *testing.T) {
	detector := &changingRoleDetector{role: common.RoleMaster}
	patcher := &mockPodPatcher{}

	// test-1 is unreachable, test-2 is synced.
	replicaClient2 := &mockValkeyCommander{
		infoResult: &valkeyclient.ReplicationInfo{
			Role:             "slave",
			MasterLinkStatus: "up",
		},
	}

	factory := &mockValkeyClientFactory{
		clients: map[string]*mockValkeyCommander{
			// test-1 has no mock, so will return error.
			"test-2.test-headless.default.svc.cluster.local:6379": replicaClient2,
		},
	}

	handler := newTestDrainHandler(detector, patcher, factory)

	// Simulate role change.
	go func() {
		time.Sleep(200 * time.Millisecond)
		detector.SetRole(common.RoleReplica)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := handler.Handle(ctx)

	assert.NoError(t, err)
	// test-2 should have been promoted (test-1 was unreachable).
	require.Len(t, replicaClient2.replicaOfCalls, 1)
	assert.Equal(t, "NO", replicaClient2.replicaOfCalls[0].host)
}

// --- Unit tests for helper functions ---

func TestPodBaseName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"test-0", "test"},
		{"my-cluster-2", "my-cluster"},
		{"single", "single"},
		{"a-b-c-0", "a-b-c"},
	}
	for _, tc := range tests {
		assert.Equal(t, tc.expected, podBaseName(tc.input), "podBaseName(%q)", tc.input)
	}
}

func TestSplitHostPort(t *testing.T) {
	tests := []struct {
		input        string
		expectedHost string
		expectedPort string
	}{
		{"localhost:6379", "localhost", "6379"},
		{"my-pod.svc.cluster.local:16379", "my-pod.svc.cluster.local", "16379"},
		{"no-port", "no-port", ""},
	}
	for _, tc := range tests {
		host, port := splitHostPort(tc.input)
		assert.Equal(t, tc.expectedHost, host, "host from %q", tc.input)
		assert.Equal(t, tc.expectedPort, port, "port from %q", tc.input)
	}
}

func TestBuildReplicaAddrs(t *testing.T) {
	handler := &DrainHandler{
		podName:     "test-0",
		headlessSvc: "test-headless.default.svc.cluster.local",
		replicas:    3,
		valkeyPort:  "6379",
	}

	addrs := handler.buildReplicaAddrs()

	assert.Len(t, addrs, 2)
	assert.Equal(t, "test-1.test-headless.default.svc.cluster.local:6379", addrs[0])
	assert.Equal(t, "test-2.test-headless.default.svc.cluster.local:6379", addrs[1])
}

func TestBuildReplicaAddrs_SkipsSelf(t *testing.T) {
	handler := &DrainHandler{
		podName:     "test-1",
		headlessSvc: "test-headless.ns.svc.cluster.local",
		replicas:    3,
		valkeyPort:  "16379",
	}

	addrs := handler.buildReplicaAddrs()

	assert.Len(t, addrs, 2)
	// test-0 and test-2, not test-1.
	for _, addr := range addrs {
		assert.NotContains(t, addr, "test-1.")
	}
}

func TestBuildReplicaAddrs_SingleReplica(t *testing.T) {
	handler := &DrainHandler{
		podName:     "test-0",
		headlessSvc: "test-headless.default.svc.cluster.local",
		replicas:    1,
		valkeyPort:  "6379",
	}

	addrs := handler.buildReplicaAddrs()

	assert.Empty(t, addrs)
}

func TestIsSyncedReplica(t *testing.T) {
	tests := []struct {
		name     string
		info     *valkeyclient.ReplicationInfo
		expected bool
	}{
		{
			name: "fully synced replica",
			info: &valkeyclient.ReplicationInfo{
				Role:             "slave",
				MasterLinkStatus: "up",
			},
			expected: true,
		},
		{
			name: "master node",
			info: &valkeyclient.ReplicationInfo{
				Role:             "master",
				MasterLinkStatus: "",
			},
			expected: false,
		},
		{
			name: "link down",
			info: &valkeyclient.ReplicationInfo{
				Role:             "slave",
				MasterLinkStatus: "down",
			},
			expected: false,
		},
		{
			name: "sync in progress",
			info: &valkeyclient.ReplicationInfo{
				Role:                 "slave",
				MasterLinkStatus:     "up",
				MasterSyncInProgress: true,
			},
			expected: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, isSyncedReplica(tc.info))
		})
	}
}
