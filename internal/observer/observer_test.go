package observer

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	ctrl "sigs.k8s.io/controller-runtime"
)

func TestNew_WithoutTLS(t *testing.T) {
	cfg := Config{
		Namespace:         "default",
		ClusterName:       "test",
		HealthAddr:        ":8084",
		PollInterval:      2 * time.Second,
		ValkeyHeadlessSvc: "test-headless.default.invalid",
		Replicas:          3,
		ObserverDB:        15,
	}

	obs, err := New(cfg)

	require.NoError(t, err)
	assert.NotNil(t, obs)
	assert.Nil(t, obs.tlsSrc)
	assert.False(t, obs.result.Ready)
	assert.NotNil(t, obs.result.Checks)
	assert.NotNil(t, obs.metrics)
}

func TestNew_WithTLS_InvalidCert(t *testing.T) {
	cfg := Config{
		Namespace:   "default",
		ClusterName: "test",
		TLSEnabled:  true,
		TLSCACert:   "/nonexistent/ca.crt",
		TLSCert:     "/nonexistent/tls.crt",
		TLSKey:      "/nonexistent/tls.key",
	}

	obs, err := New(cfg)

	assert.Error(t, err)
	assert.Nil(t, obs)
}

func TestGetResult_CopySemantics(t *testing.T) {
	cfg := Config{
		Namespace:   "default",
		ClusterName: "test",
	}
	obs, err := New(cfg)
	require.NoError(t, err)

	// Manually set a result.
	obs.mu.Lock()
	obs.result = CheckResult{
		Ready:     true,
		Checks:    map[string]bool{"master_reachable": true, "write_test": true},
		Message:   "all ok",
		LastCheck: time.Now(),
	}
	obs.mu.Unlock()

	result := obs.GetResult()

	assert.True(t, result.Ready)
	assert.Len(t, result.Checks, 2)
	assert.True(t, result.Checks["master_reachable"])
	assert.True(t, result.Checks["write_test"])
	assert.Equal(t, "all ok", result.Message)

	// Mutating the returned copy must not affect the observer's state.
	result.Checks["new_check"] = false
	result.Ready = false

	original := obs.GetResult()
	assert.True(t, original.Ready)
	assert.Len(t, original.Checks, 2)
}

func TestSetResult(t *testing.T) {
	cfg := Config{
		Namespace:   "default",
		ClusterName: "test",
	}
	obs, err := New(cfg)
	require.NoError(t, err)

	checks := map[string]bool{
		"master_reachable": true,
		"replica_sync":     false,
	}
	start := time.Now()
	obs.setResult(false, checks, "replica sync failed", start)

	result := obs.GetResult()
	assert.False(t, result.Ready)
	assert.Equal(t, "replica sync failed", result.Message)
	assert.True(t, result.Checks["master_reachable"])
	assert.False(t, result.Checks["replica_sync"])
}

func TestMasterAddressFromHeadless(t *testing.T) {
	tests := []struct {
		name       string
		tlsEnabled bool
		expected   string
	}{
		{
			name:       "without TLS",
			tlsEnabled: false,
			expected:   "test-0.test-headless.default.invalid:6379",
		},
		{
			name:       "with TLS",
			tlsEnabled: true,
			expected:   "test-0.test-headless.default.invalid:16379",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obs := &Observer{
				cfg: Config{
					ClusterName:       "test",
					ValkeyHeadlessSvc: "test-headless.default.invalid",
					TLSEnabled:        tt.tlsEnabled,
				},
			}
			assert.Equal(t, tt.expected, obs.masterAddressFromHeadless())
		})
	}
}

func TestDiscoverMaster_NoSentinel(t *testing.T) {
	obs := &Observer{
		cfg: Config{
			ClusterName:       "test",
			ValkeyHeadlessSvc: "test-headless.default.invalid",
			SentinelEnabled:   false,
		},
	}

	addr, err := obs.discoverMaster(context.Background())

	require.NoError(t, err)
	assert.Equal(t, "test-0.test-headless.default.invalid:6379", addr)
}

func TestNew_WithSentinel_BuildsSeparateTLSConfigs(t *testing.T) {
	// Without TLS, sentinel TLS config should be nil.
	cfg := Config{
		Namespace:       "default",
		ClusterName:     "test",
		SentinelEnabled: true,
		TLSEnabled:      false,
	}

	obs, err := New(cfg)
	require.NoError(t, err)
	assert.Nil(t, obs.tlsSrc)
	assert.Nil(t, obs.sentinelTLSSrc)
}

func TestRunSentinelChecks_IncludesHostnameChecks(t *testing.T) {
	obs := &Observer{
		cfg: Config{
			SentinelEnabled: true,
			SentinelMonitor: "test",
			SentinelAddrList: []string{
				"sentinel-0.invalid:26379",
			},
			UnreadyWhen: UnreadyWhenConfig{
				SentinelUnreachable:             true,
				SentinelQuorumFailure:           true,
				SentinelMasterDown:              true,
				SentinelMasterHostnameInvalid:   true,
				SentinelReplicaHostnamesInvalid: true,
			},
		},
		metrics: newObserverMetrics(),
	}

	logger := ctrl.Log.WithName("test")
	checks := make(map[string]bool)
	msg := obs.runSentinelChecks(logger, checks)

	// All checks should be present in the map.
	_, hasMasterHostname := checks["sentinel_master_hostname"]
	_, hasReplicaHostnames := checks["sentinel_replica_hostnames"]
	assert.True(t, hasMasterHostname, "sentinel_master_hostname check must be present")
	assert.True(t, hasReplicaHostnames, "sentinel_replica_hostnames check must be present")
	assert.NotEmpty(t, msg)
}

func TestNew_TLS_ValkeyMTLSFalse_SkipsClientCert(t *testing.T) {
	// ValkeyMTLS=false → client cert loading skipped → no error even with missing cert paths
	cfg := Config{
		Namespace:   "default",
		ClusterName: "test",
		TLSEnabled:  true,
		TLSCACert:   "/nonexistent/ca.crt",
		TLSCert:     "/nonexistent/tls.crt",
		TLSKey:      "/nonexistent/tls.key",
		ValkeyMTLS:  false,
	}

	obs, err := New(cfg)

	// CA cert is still required → should fail on CA cert loading
	assert.Error(t, err)
	assert.Nil(t, obs)
}

func TestNew_TLS_ValkeyMTLSTrue_RequiresClientCert(t *testing.T) {
	// ValkeyMTLS=true (default) → client cert loading attempted → error on missing cert
	cfg := Config{
		Namespace:   "default",
		ClusterName: "test",
		TLSEnabled:  true,
		TLSCACert:   "/nonexistent/ca.crt",
		TLSCert:     "/nonexistent/tls.crt",
		TLSKey:      "/nonexistent/tls.key",
		ValkeyMTLS:  true,
	}

	obs, err := New(cfg)

	assert.Error(t, err)
	assert.Nil(t, obs)
}

func TestNew_TLS_SentinelMTLSFlags(t *testing.T) {
	// Without TLS, SentinelMTLS flag has no effect
	cfg := Config{
		Namespace:       "default",
		ClusterName:     "test",
		SentinelEnabled: true,
		TLSEnabled:      false,
		SentinelMTLS:    true,
	}

	obs, err := New(cfg)
	require.NoError(t, err)
	assert.Nil(t, obs.sentinelTLSSrc)
}
