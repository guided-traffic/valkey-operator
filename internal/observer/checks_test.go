package observer

import (
	"context"
	"crypto/tls"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiscoverMaster_StandaloneMode(t *testing.T) {
	obs := &Observer{
		cfg: Config{
			ClusterName:       "mydb",
			ValkeyHeadlessSvc: "mydb-headless.ns.svc.cluster.local",
			SentinelEnabled:   false,
			TLSEnabled:        false,
		},
	}

	addr, err := obs.discoverMaster(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "mydb-0.mydb-headless.ns.svc.cluster.local:6379", addr)
}

func TestDiscoverMaster_StandaloneMode_TLS(t *testing.T) {
	obs := &Observer{
		cfg: Config{
			ClusterName:       "mydb",
			ValkeyHeadlessSvc: "mydb-headless.ns.svc.cluster.local",
			SentinelEnabled:   false,
			TLSEnabled:        true,
		},
	}

	addr, err := obs.discoverMaster(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "mydb-0.mydb-headless.ns.svc.cluster.local:16379", addr)
}

func TestDiscoverMaster_SentinelMode_NoAddrs(t *testing.T) {
	obs := &Observer{
		cfg: Config{
			ClusterName:      "mydb",
			SentinelEnabled:  true,
			SentinelAddrList: []string{},
		},
	}

	// With sentinel enabled but no addrs, falls back to headless.
	addr, err := obs.discoverMaster(context.Background())
	require.NoError(t, err)
	assert.Contains(t, addr, "mydb-0.")
}

func TestDiscoverMasterViaSentinel_AllFail(t *testing.T) {
	obs := &Observer{
		cfg: Config{
			ClusterName:     "mydb",
			SentinelEnabled: true,
			SentinelMonitor: "mydb",
			SentinelAddrList: []string{
				"sentinel-0.invalid:26379",
				"sentinel-1.invalid:26379",
			},
		},
	}

	_, err := obs.discoverMasterViaSentinel()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no sentinel responded")
}

func TestCheckReplicaRead_NoReplicasToCheck(t *testing.T) {
	// With replicas=0 the loop doesn't execute — no error expected.
	obs := &Observer{
		cfg: Config{
			ClusterName:       "test",
			ValkeyHeadlessSvc: "test-headless.default.svc.cluster.local",
			Replicas:          0,
		},
	}

	err := obs.checkReplicaRead("some-value")
	assert.NoError(t, err)
}

func TestCheckSentinelReachable_AllUnreachable(t *testing.T) {
	obs := &Observer{
		cfg: Config{
			SentinelAddrList: []string{
				"sentinel-0.invalid:26379",
				"sentinel-1.invalid:26379",
			},
		},
	}

	err := obs.checkSentinelReachable()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unreachable sentinels")
}

func TestCheckSentinelQuorumAndFlags_AllFail(t *testing.T) {
	obs := &Observer{
		cfg: Config{
			SentinelMonitor: "mydb",
			SentinelAddrList: []string{
				"sentinel-0.invalid:26379",
			},
		},
	}

	quorumOK, flagsOK, err := obs.checkSentinelQuorumAndFlags()
	assert.False(t, quorumOK)
	assert.True(t, flagsOK) // flags remain default true when no data
	assert.Error(t, err)
}

func TestCheckSentinelMasterHostname_AllFail(t *testing.T) {
	obs := &Observer{
		cfg: Config{
			SentinelMonitor: "mydb",
			SentinelAddrList: []string{
				"sentinel-0.invalid:26379",
			},
		},
	}

	err := obs.checkSentinelMasterHostname()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "master hostname check failed")
}

func TestCheckSentinelReplicaHostnames_AllFail(t *testing.T) {
	obs := &Observer{
		cfg: Config{
			SentinelMonitor: "mydb",
			SentinelAddrList: []string{
				"sentinel-0.invalid:26379",
			},
		},
	}

	err := obs.checkSentinelReplicaHostnames()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "replica hostname check failed")
}

func TestNewSentinelClient_FallbackToTLSConfig(t *testing.T) {
	obs := &Observer{
		cfg:       Config{},
		tlsConfig: nil,
	}

	// No TLS, no password.
	c := obs.newSentinelClient("localhost:26379", "")
	assert.NotNil(t, c)

	// With password, no TLS.
	c = obs.newSentinelClient("localhost:26379", "secret")
	assert.NotNil(t, c)
}

func TestNewSentinelClient_WithSentinelTLSConfig(t *testing.T) {
	obs := &Observer{
		cfg:               Config{},
		sentinelTLSConfig: &tls.Config{MinVersion: tls.VersionTLS12},
		tlsConfig:         &tls.Config{MinVersion: tls.VersionTLS13},
	}

	// Should use sentinelTLSConfig, not tlsConfig.
	c := obs.newSentinelClient("localhost:36379", "pass")
	assert.NotNil(t, c)
}
