package sidecar

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"path/filepath"
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

func (d *changingRoleDetector) SetError(err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.err = err
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

// calls returns a snapshot of the recorded REPLICAOF calls.
func (m *mockValkeyCommander) calls() []replicaOfRecord {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]replicaOfRecord(nil), m.replicaOfCalls...)
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
		detector:              detector,
		patcher:               patcher,
		clientFactory:         factory,
		sentinelClientFactory: factory,
		podName:               "test-0",
		podNamespace:          "default",
		headlessSvc:           "test-headless.default.svc.cluster.local",
		replicas:              3,
		valkeyPort:            "6379",
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

func TestDrainHandler_WaitForRoleChange_ConnectionRefused_Sentinel(t *testing.T) {
	// Valkey is initially master but becomes unreachable after sentinel failover.
	detector := &changingRoleDetector{role: common.RoleMaster}
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

	// Simulate Valkey process dying (connection refused) after sentinel trigger.
	go func() {
		time.Sleep(200 * time.Millisecond)
		detector.SetError(fmt.Errorf(
			"info replication localhost:16379: cannot connect to localhost:16379: " +
				"connection to localhost:16379 refused — the service is not running",
		))
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := handler.Handle(ctx)

	assert.NoError(t, err, "connection refused during wait should be treated as success")
	require.Len(t, sentinelClient.failoverCalls, 1)
}

func TestDrainHandler_WaitForRoleChange_ConnectionRefused_Manual(t *testing.T) {
	// Valkey is initially master but becomes unreachable after manual failover.
	detector := &changingRoleDetector{role: common.RoleMaster}
	patcher := &mockPodPatcher{}

	replicaClient := &mockValkeyCommander{
		infoResult: &valkeyclient.ReplicationInfo{
			Role:             "slave",
			MasterLinkStatus: "up",
		},
	}

	factory := &mockValkeyClientFactory{
		clients: map[string]*mockValkeyCommander{
			"test-1.test-headless.default.svc.cluster.local:6379": replicaClient,
			"test-2.test-headless.default.svc.cluster.local:6379": replicaClient,
		},
	}

	handler := newTestDrainHandler(detector, patcher, factory)

	// Simulate Valkey process dying (standard Go net error) after manual failover.
	go func() {
		time.Sleep(200 * time.Millisecond)
		detector.SetError(fmt.Errorf(
			"dial tcp 127.0.0.1:6379: connect: connection refused",
		))
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := handler.Handle(ctx)

	assert.NoError(t, err, "connection refused during wait should be treated as success")
	// replica was promoted
	require.GreaterOrEqual(t, len(replicaClient.replicaOfCalls), 1)
}

// The deterministic form of this scenario lives in drain_failure_paths_test.go:
// TestDrainHandler_TransientProbeFailureDoesNotFailTheDrain scripts the probe
// answers instead of racing sleeps against the handler's 1s ticker, and asserts
// that the loop polled again rather than only that Handle returned nil.

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

func TestIsConnectionRefused(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
		{
			name:     "standard Go net error",
			err:      fmt.Errorf("dial tcp 127.0.0.1:6379: connect: connection refused"),
			expected: true,
		},
		{
			name: "valkey client custom error",
			err: fmt.Errorf(
				"info replication localhost:16379: cannot connect to localhost:16379: " +
					"connection to localhost:16379 refused \u2014 the service is not running",
			),
			expected: true,
		},
		{
			name:     "unrelated error",
			err:      fmt.Errorf("timeout reading response"),
			expected: false,
		},
		{
			name:     "auth error",
			err:      fmt.Errorf("NOAUTH Authentication required"),
			expected: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, isConnectionRefused(tc.err))
		})
	}
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

// --- Edge case tests for drain handler functions ---

func TestPodBaseName_EmptyString(t *testing.T) {
	assert.Equal(t, "", podBaseName(""))
}

func TestPodBaseName_EndsWithDash(t *testing.T) {
	assert.Equal(t, "pod", podBaseName("pod-"))
}

func TestPodBaseName_StartsWithDash(t *testing.T) {
	assert.Equal(t, "", podBaseName("-0"))
}

func TestPodBaseName_MultipleDashes(t *testing.T) {
	assert.Equal(t, "my-fancy-cluster", podBaseName("my-fancy-cluster-5"))
}

func TestSplitHostPort_EmptyString(t *testing.T) {
	host, port := splitHostPort("")
	assert.Equal(t, "", host)
	assert.Equal(t, "", port)
}

func TestSplitHostPort_PortOnly(t *testing.T) {
	host, port := splitHostPort(":6379")
	assert.Equal(t, "", host)
	assert.Equal(t, "6379", port)
}

func TestSplitHostPort_MultipleColons(t *testing.T) {
	// For IPv6-like addresses, splits at last colon.
	host, port := splitHostPort("fd00::1:6379")
	assert.Equal(t, "fd00::1", host)
	assert.Equal(t, "6379", port)
}

func TestBuildReplicaAddrs_ZeroReplicas(t *testing.T) {
	handler := &DrainHandler{
		podName:     "test-0",
		headlessSvc: "test-headless",
		replicas:    0,
		valkeyPort:  "6379",
	}
	addrs := handler.buildReplicaAddrs()
	assert.Empty(t, addrs)
}

func TestBuildReplicaAddrs_LargeReplicas(t *testing.T) {
	handler := &DrainHandler{
		podName:     "test-5",
		headlessSvc: "test-headless",
		replicas:    10,
		valkeyPort:  "6379",
	}
	addrs := handler.buildReplicaAddrs()
	// Should have 9 addresses (10 replicas - 1 self).
	assert.Len(t, addrs, 9)
	// Ensure self is excluded.
	for _, addr := range addrs {
		assert.NotContains(t, addr, "test-5.")
	}
}

func TestBuildReplicaAddrs_TLSPort(t *testing.T) {
	handler := &DrainHandler{
		podName:     "test-0",
		headlessSvc: "test-headless",
		replicas:    2,
		valkeyPort:  "16379",
	}
	addrs := handler.buildReplicaAddrs()
	assert.Len(t, addrs, 1)
	assert.Contains(t, addrs[0], ":16379")
}

func TestIsConnectionRefused_WrappedError(t *testing.T) {
	inner := fmt.Errorf("connection refused")
	wrapped := fmt.Errorf("dial tcp: %w", inner)
	assert.True(t, isConnectionRefused(wrapped))
}

func TestIsSyncedReplica_EmptyRole(t *testing.T) {
	info := &valkeyclient.ReplicationInfo{
		Role:             "",
		MasterLinkStatus: "up",
	}
	assert.False(t, isSyncedReplica(info))
}

func TestIsSyncedReplica_LoadingRole(t *testing.T) {
	info := &valkeyclient.ReplicationInfo{
		Role:             "loading",
		MasterLinkStatus: "up",
	}
	assert.False(t, isSyncedReplica(info))
}

func TestIsSyncedReplica_EmptyLinkStatus(t *testing.T) {
	info := &valkeyclient.ReplicationInfo{
		Role:             "slave",
		MasterLinkStatus: "",
	}
	assert.False(t, isSyncedReplica(info))
}

func TestDrainHandler_SeparateSentinelClientFactory(t *testing.T) {
	// Verify that sentinel connections use the separate sentinel factory.
	detector := &changingRoleDetector{role: common.RoleMaster}
	patcher := &mockPodPatcher{}

	mainFactory := &mockValkeyClientFactory{
		clients: map[string]*mockValkeyCommander{},
	}
	sentinelFactory := &mockValkeyClientFactory{
		clients: map[string]*mockValkeyCommander{
			"sentinel-0:26379": {},
		},
	}

	handler := &DrainHandler{
		detector:              detector,
		patcher:               patcher,
		clientFactory:         mainFactory,
		sentinelClientFactory: sentinelFactory,
		podName:               "test-0",
		podNamespace:          "default",
		sentinelEnabled:       true,
		sentinelMonitor:       "test",
		sentinelAddrs:         []string{"sentinel-0:26379"},
		headlessSvc:           "test-headless",
		replicas:              3,
		valkeyPort:            "6379",
	}

	// Simulate role change.
	go func() {
		time.Sleep(200 * time.Millisecond)
		detector.SetRole(common.RoleReplica)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := handler.Handle(ctx)
	assert.NoError(t, err)
}

func TestDrainHandler_ManualFailover_PromotionFails(t *testing.T) {
	detector := &mockRoleDetector{role: common.RoleMaster}
	patcher := &mockPodPatcher{}

	replicaClient := &mockValkeyCommander{
		infoResult: &valkeyclient.ReplicationInfo{
			Role:             "slave",
			MasterLinkStatus: "up",
		},
		replicaOfErr: errors.New("ERR command not allowed"),
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
	assert.Contains(t, err.Error(), "promoting replica")
}

func TestDrainHandler_EmptySentinelAddrs(t *testing.T) {
	detector := &mockRoleDetector{role: common.RoleMaster}
	patcher := &mockPodPatcher{}
	factory := &mockValkeyClientFactory{clients: map[string]*mockValkeyCommander{}}

	handler := newTestDrainHandler(detector, patcher, factory, func(h *DrainHandler) {
		h.sentinelEnabled = true
		h.sentinelMonitor = "test"
		h.sentinelAddrs = nil // No sentinel addresses.
	})

	err := handler.Handle(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "all sentinels failed")
}

func TestNewDrainHandlerWithDeps_NilSentinelFactory(t *testing.T) {
	detector := &mockRoleDetector{role: common.RoleReplica}
	patcher := &mockPodPatcher{}
	factory := &mockValkeyClientFactory{clients: map[string]*mockValkeyCommander{}}

	handler := NewDrainHandlerWithDeps(
		detector, patcher, factory, nil, // nil sentinel factory.
		"test-0", "default", false, "", nil, "test-headless", 3, "6379",
	)

	// Should use the main factory as sentinel factory.
	assert.NotNil(t, handler)
	assert.Equal(t, factory, handler.sentinelClientFactory)
}

// --- Promotion stamp (part F of the unrecorded-promotion design) ---

// syncedReplicaFactory builds a client factory in which every listed address is
// a synced replica, keyed exactly as buildReplicaAddrs renders them.
func syncedReplicaFactory(addrs ...string) *mockValkeyClientFactory {
	clients := make(map[string]*mockValkeyCommander, len(addrs))
	for _, addr := range addrs {
		clients[addr] = &mockValkeyCommander{
			infoResult: &valkeyclient.ReplicationInfo{
				Role:             "slave",
				MasterLinkStatus: "up",
			},
		}
	}
	return &mockValkeyClientFactory{clients: clients}
}

func TestDrainHandler_ManualFailoverStampsThePromotedPod(t *testing.T) {
	detector := &changingRoleDetector{role: common.RoleMaster}
	patcher := &mockPodPatcher{}
	factory := syncedReplicaFactory(
		"test-1.test-headless.default.svc.cluster.local:6379",
		"test-2.test-headless.default.svc.cluster.local:6379",
	)
	promoted := factory.clients["test-1.test-headless.default.svc.cluster.local:6379"]
	follower := factory.clients["test-2.test-headless.default.svc.cluster.local:6379"]

	// Record what the follower had already been told when the stamp was written:
	// the stamp must land before any best-effort reconfiguration.
	reconfiguredAtStampTime := -1
	patcher.onAnnotation = func() { reconfiguredAtStampTime = len(follower.calls()) }

	handler := newTestDrainHandler(detector, patcher, factory)
	go func() {
		time.Sleep(100 * time.Millisecond)
		detector.SetRole(common.RoleReplica)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, handler.Handle(ctx))

	require.Len(t, promoted.calls(), 1, "the promotion itself must still happen")

	require.Len(t, patcher.annotationPatches, 1)
	stamp := patcher.annotationPatches[0]
	assert.Equal(t, "default", stamp.namespace)
	assert.Equal(t, "test-1", stamp.name, "the stamp belongs on the promoted peer, not on the draining pod")
	assert.Equal(t, common.AnnotationDrainPromotedAt, stamp.key)

	at, err := time.Parse(time.RFC3339, stamp.value)
	require.NoError(t, err, "the stamp value must be RFC3339")
	assert.Equal(t, time.UTC, at.Location())
	assert.WithinDuration(t, time.Now(), at, time.Minute)

	assert.Equal(t, 0, reconfiguredAtStampTime,
		"the stamp must be written before the replicas are reconfigured")
}

func TestDrainHandler_StampFailureDoesNotAbortTheDrain(t *testing.T) {
	detector := &changingRoleDetector{role: common.RoleMaster}
	patcher := &mockPodPatcher{annotationErr: errors.New("forbidden")}
	factory := syncedReplicaFactory(
		"test-1.test-headless.default.svc.cluster.local:6379",
		"test-2.test-headless.default.svc.cluster.local:6379",
	)
	follower := factory.clients["test-2.test-headless.default.svc.cluster.local:6379"]

	handler := newTestDrainHandler(detector, patcher, factory)
	go func() {
		time.Sleep(100 * time.Millisecond)
		detector.SetRole(common.RoleReplica)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// The promotion already happened and cannot be undone, so a rejected stamp
	// must not stop the drain from finishing its remaining work.
	require.NoError(t, handler.Handle(ctx))
	require.Len(t, patcher.annotationPatches, 1)
	require.Len(t, follower.calls(), 1, "reconfiguration must still run after a failed stamp")
	assert.Equal(t, "test-1.test-headless.default.svc.cluster.local", follower.calls()[0].host)
}

func TestDrainHandler_SentinelPathDoesNotStamp(t *testing.T) {
	detector := &changingRoleDetector{role: common.RoleMaster}
	patcher := &mockPodPatcher{}
	sentinelClient := &mockValkeyCommander{}
	factory := &mockValkeyClientFactory{
		clients: map[string]*mockValkeyCommander{"sentinel-0:26379": sentinelClient},
	}

	handler := newTestDrainHandler(detector, patcher, factory, func(h *DrainHandler) {
		h.sentinelEnabled = true
		h.sentinelMonitor = "mymaster"
		h.sentinelAddrs = []string{"sentinel-0:26379"}
	})
	go func() {
		time.Sleep(100 * time.Millisecond)
		detector.SetRole(common.RoleReplica)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, handler.Handle(ctx))

	require.Len(t, sentinelClient.failoverCalls, 1)
	assert.Empty(t, patcher.annotationPatches,
		"Sentinel owns the promotion authority, so the drain handler must not stamp there")
}

func TestDrainHandler_NoStampWhenThePodNameCannotBeDerived(t *testing.T) {
	detector := &changingRoleDetector{role: common.RoleMaster}
	patcher := &mockPodPatcher{}
	// Empty headless service: the addresses degrade to "test-1.:6379", so no pod
	// name can be attributed to the headless Service of this StatefulSet.
	factory := syncedReplicaFactory("test-1.:6379", "test-2.:6379")
	promoted := factory.clients["test-1.:6379"]
	follower := factory.clients["test-2.:6379"]

	handler := newTestDrainHandler(detector, patcher, factory, func(h *DrainHandler) {
		h.headlessSvc = ""
	})
	go func() {
		time.Sleep(100 * time.Millisecond)
		detector.SetRole(common.RoleReplica)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, handler.Handle(ctx))

	require.Len(t, promoted.calls(), 1, "the failover must still happen without a stamp")
	require.Len(t, follower.calls(), 1)
	assert.Empty(t, patcher.annotationPatches, "a pod name that cannot be derived must not be guessed")
}

func TestPodNameFromHost(t *testing.T) {
	tests := []struct {
		name        string
		headlessSvc string
		host        string
		want        string
		wantOK      bool
	}{
		{
			name:        "pod of this headless service",
			headlessSvc: "test-headless.default.svc.cluster.local",
			host:        "test-1.test-headless.default.svc.cluster.local",
			want:        "test-1",
			wantOK:      true,
		},
		{
			name:        "different headless service",
			headlessSvc: "test-headless.default.svc.cluster.local",
			host:        "other-1.other-headless.default.svc.cluster.local",
			wantOK:      false,
		},
		{
			name:        "headless service not configured",
			headlessSvc: "",
			host:        "test-1.",
			wantOK:      false,
		},
		{
			name:        "empty pod name",
			headlessSvc: "test-headless.default.svc.cluster.local",
			host:        ".test-headless.default.svc.cluster.local",
			wantOK:      false,
		},
		{
			name:        "host carries extra labels before the service",
			headlessSvc: "test-headless.default.svc.cluster.local",
			host:        "a.b.test-headless.default.svc.cluster.local",
			wantOK:      false,
		},
		{
			name:        "headless service itself is not a pod",
			headlessSvc: "test-headless.default.svc.cluster.local",
			host:        "test-headless.default.svc.cluster.local",
			wantOK:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &DrainHandler{headlessSvc: tt.headlessSvc}
			got, ok := d.podNameFromHost(tt.host)
			assert.Equal(t, tt.wantOK, ok)
			assert.Equal(t, tt.want, got)
		})
	}
}

// --- Production client factory and drain handler wiring ---

// recordedFakeValkey starts a fake server that answers PING and records every
// command it received.
func recordedFakeValkey(t *testing.T, tlsCfg *tls.Config) (addr string, received func() [][]string) {
	t.Helper()
	var mu sync.Mutex
	var seen [][]string

	addr = fakeValkeyServer(t, tlsCfg, func(args []string) string {
		mu.Lock()
		seen = append(seen, args)
		mu.Unlock()
		if args[0] == "AUTH" {
			return "+OK\r\n"
		}
		return "+PONG\r\n"
	})

	return addr, func() [][]string {
		mu.Lock()
		defer mu.Unlock()
		return append([][]string(nil), seen...)
	}
}

func TestRealValkeyClientFactory_PlainConnection(t *testing.T) {
	addr, received := recordedFakeValkey(t, nil)
	factory, err := newRealValkeyClientFactory(Config{})
	require.NoError(t, err)

	require.NoError(t, factory.NewClient(addr).Ping())

	got := received()
	require.Len(t, got, 1)
	assert.Equal(t, "PING", got[0][0], "no AUTH must be sent when no password is configured")
}

func TestRealValkeyClientFactory_PasswordConnection(t *testing.T) {
	addr, received := recordedFakeValkey(t, nil)
	factory, err := newRealValkeyClientFactory(Config{Password: "s3cret"})
	require.NoError(t, err)

	require.NoError(t, factory.NewClient(addr).Ping())

	got := received()
	require.Len(t, got, 2)
	assert.Equal(t, []string{"AUTH", "s3cret"}, got[0])
}

func TestRealValkeyClientFactory_TLSVariants(t *testing.T) {
	certs := generateTestCerts(t)

	for _, password := range []string{"", "s3cret"} {
		t.Run("password="+password, func(t *testing.T) {
			addr, received := recordedFakeValkey(t, certs.serverTLSConfig(t))
			factory, err := newRealValkeyClientFactory(Config{
				TLSEnabled: true,
				TLSCACert:  certs.caPath,
				Password:   password,
			})
			require.NoError(t, err)
			require.NotNil(t, factory.tlsConfig)

			require.NoError(t, factory.NewClient(addr).Ping())
			assert.NotEmpty(t, received())
		})
	}
}

func TestRealValkeyClientFactory_TLSConfigError(t *testing.T) {
	_, err := newRealValkeyClientFactory(Config{TLSEnabled: true, TLSCACert: filepath.Join(t.TempDir(), "absent.crt")})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "building TLS config for drain handler")
}

func TestBuildDrainHandler_CopiesConfigAndDerivesPort(t *testing.T) {
	handler, err := buildDrainHandler(Config{
		PodName:      "test-0",
		PodNamespace: "valkey-ns",
		ValkeyAddr:   "127.0.0.1:16379",
		HeadlessSvc:  "test-headless.valkey-ns.svc.cluster.local",
		Replicas:     3,
	}, &mockRoleDetector{}, &mockPodPatcher{})

	require.NoError(t, err)
	assert.Equal(t, "test-0", handler.podName)
	assert.Equal(t, "valkey-ns", handler.podNamespace)
	assert.Equal(t, "test-headless.valkey-ns.svc.cluster.local", handler.headlessSvc)
	assert.Equal(t, 3, handler.replicas)
	assert.Equal(t, "16379", handler.valkeyPort)
	assert.Nil(t, handler.sentinelAddrs)
	assert.Same(t, handler.clientFactory, handler.sentinelClientFactory,
		"without disabled sentinel auth both paths share one factory")
}

func TestBuildDrainHandler_DefaultsThePortWhenTheAddressCarriesNone(t *testing.T) {
	handler, err := buildDrainHandler(Config{ValkeyAddr: "localhost"}, &mockRoleDetector{}, &mockPodPatcher{})

	require.NoError(t, err)
	assert.Equal(t, "6379", handler.valkeyPort)
}

func TestBuildDrainHandler_SeparateUnauthenticatedSentinelFactory(t *testing.T) {
	addr, received := recordedFakeValkey(t, nil)

	handler, err := buildDrainHandler(Config{
		ValkeyAddr:          "127.0.0.1:6379",
		Password:            "s3cret",
		SentinelEnabled:     true,
		SentinelDisableAuth: true,
		SentinelAddrs:       "a:26379,b:26379",
	}, &mockRoleDetector{}, &mockPodPatcher{})

	require.NoError(t, err)
	assert.Equal(t, []string{"a:26379", "b:26379"}, handler.sentinelAddrs)
	require.NotSame(t, handler.clientFactory, handler.sentinelClientFactory)

	// The sentinel factory must not AUTH; the Valkey factory must.
	require.NoError(t, handler.sentinelClientFactory.NewClient(addr).Ping())
	require.NoError(t, handler.clientFactory.NewClient(addr).Ping())

	got := received()
	require.Len(t, got, 3)
	assert.Equal(t, "PING", got[0][0])
	assert.Equal(t, []string{"AUTH", "s3cret"}, got[1])
}

func TestBuildDrainHandler_TLSConfigError(t *testing.T) {
	_, err := buildDrainHandler(Config{
		TLSEnabled: true,
		TLSCACert:  filepath.Join(t.TempDir(), "absent.crt"),
	}, &mockRoleDetector{}, &mockPodPatcher{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "building TLS config for drain handler")
}

func TestDrainHandler_PeerAlreadyMasterSkipsPromotionAndStamp(t *testing.T) {
	// The shape a rolling update produces: the operator promoted test-1 itself and
	// then deleted this pod without demoting it, so this drain runs on a master
	// while test-1 already answers role:master. test-2 follows test-1 and would
	// otherwise be promoted a second time, and stamped.
	detector := &changingRoleDetector{role: common.RoleMaster}
	patcher := &mockPodPatcher{}

	operatorMaster := &mockValkeyCommander{
		infoResult: &valkeyclient.ReplicationInfo{Role: common.RoleMaster},
	}
	follower := &mockValkeyCommander{
		infoResult: &valkeyclient.ReplicationInfo{
			Role:             "slave",
			MasterLinkStatus: "up",
		},
	}
	factory := &mockValkeyClientFactory{
		clients: map[string]*mockValkeyCommander{
			"test-1.test-headless.default.svc.cluster.local:6379": operatorMaster,
			"test-2.test-headless.default.svc.cluster.local:6379": follower,
		},
	}

	handler := newTestDrainHandler(detector, patcher, factory)
	go func() {
		time.Sleep(100 * time.Millisecond)
		detector.SetRole(common.RoleReplica)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// The drain still completes: the label is off the -rw Service and the handler
	// waits for the local role to change, it just does not promote anyone.
	require.NoError(t, handler.Handle(ctx))
	require.Len(t, patcher.patches, 1)
	assert.Equal(t, common.RoleDraining, patcher.patches[0].labelValue)

	assert.Empty(t, follower.calls(), "no third pod may be promoted or redirected")
	assert.Empty(t, operatorMaster.calls(), "the existing master must not be sent REPLICAOF")
	assert.Empty(t, patcher.annotationPatches,
		"a promotion that did not happen must not leave a stamp")
}

func TestDrainHandler_PeerMasterAfterASyncedReplicaStillSkipsPromotion(t *testing.T) {
	// The master sits behind a synced replica in ordinal order, so concluding
	// "no master" requires querying every peer, not returning at the first
	// promotable candidate.
	detector := &changingRoleDetector{role: common.RoleMaster}
	patcher := &mockPodPatcher{}

	follower := &mockValkeyCommander{
		infoResult: &valkeyclient.ReplicationInfo{
			Role:             "slave",
			MasterLinkStatus: "up",
		},
	}
	operatorMaster := &mockValkeyCommander{
		infoResult: &valkeyclient.ReplicationInfo{Role: common.RoleMaster},
	}
	factory := &mockValkeyClientFactory{
		clients: map[string]*mockValkeyCommander{
			"test-1.test-headless.default.svc.cluster.local:6379": follower,
			"test-2.test-headless.default.svc.cluster.local:6379": operatorMaster,
		},
	}

	handler := newTestDrainHandler(detector, patcher, factory)
	go func() {
		time.Sleep(100 * time.Millisecond)
		detector.SetRole(common.RoleReplica)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	require.NoError(t, handler.Handle(ctx))
	assert.Empty(t, follower.calls(), "the synced replica in front of the master must not be promoted")
	assert.Empty(t, patcher.annotationPatches)
}

func TestDrainHandler_GenuineDrainStillPromotesAndStamps(t *testing.T) {
	// No peer is master - the node this master sits on is going away. Promotion
	// and stamp must be exactly as before.
	detector := &changingRoleDetector{role: common.RoleMaster}
	patcher := &mockPodPatcher{}
	factory := syncedReplicaFactory(
		"test-1.test-headless.default.svc.cluster.local:6379",
		"test-2.test-headless.default.svc.cluster.local:6379",
	)
	promoted := factory.clients["test-1.test-headless.default.svc.cluster.local:6379"]
	follower := factory.clients["test-2.test-headless.default.svc.cluster.local:6379"]

	handler := newTestDrainHandler(detector, patcher, factory)
	go func() {
		time.Sleep(100 * time.Millisecond)
		detector.SetRole(common.RoleReplica)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, handler.Handle(ctx))

	require.Len(t, promoted.calls(), 1)
	assert.Equal(t, "NO", promoted.calls()[0].host)
	assert.Equal(t, "ONE", promoted.calls()[0].port)

	require.Len(t, follower.calls(), 1)
	assert.Equal(t, "test-1.test-headless.default.svc.cluster.local", follower.calls()[0].host)

	require.Len(t, patcher.annotationPatches, 1)
	assert.Equal(t, "test-1", patcher.annotationPatches[0].name)
	assert.Equal(t, common.AnnotationDrainPromotedAt, patcher.annotationPatches[0].key)
}
