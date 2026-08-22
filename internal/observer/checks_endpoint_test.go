package observer

import (
	"context"
	"crypto/tls"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The tests in this file drive the individual checks against real RESP endpoints
// (see fake_endpoint_test.go) so that both the success and the failure verdicts
// are produced by actual protocol traffic.

func TestPingHost(t *testing.T) {
	t.Run("master answering PONG passes", func(t *testing.T) {
		node := newFakeValkeyNode()
		ep := startFakeRESP(t, node.handle)
		obs := &Observer{cfg: Config{}}

		require.NoError(t, obs.pingHost(ep.addr))
		assert.True(t, ep.sawCommand("PING"), "observer must send PING")
	})

	t.Run("reply without PONG is rejected", func(t *testing.T) {
		node := newFakeValkeyNode()
		node.pingReply = respSimple("LOADING dataset in memory")
		ep := startFakeRESP(t, node.handle)
		obs := &Observer{cfg: Config{}}

		err := obs.pingHost(ep.addr)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unexpected response")
	})

	t.Run("closed port yields a connection hint", func(t *testing.T) {
		obs := &Observer{cfg: Config{}}

		err := obs.pingHost(closedAddr(t))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cannot connect to")
	})
}

func TestCheckReplicaSync(t *testing.T) {
	tests := []struct {
		name            string
		replicas        int
		connectedSlaves int
		syncInProgress  bool
		infoReply       string
		wantErr         string
	}{
		{
			name:            "all replicas connected",
			replicas:        3,
			connectedSlaves: 2,
		},
		{
			name:            "more replicas than expected is accepted",
			replicas:        3,
			connectedSlaves: 5,
		},
		{
			name:            "one replica missing",
			replicas:        3,
			connectedSlaves: 1,
			wantErr:         "expected 2 connected replicas, got 1",
		},
		{
			name:            "full resync still running",
			replicas:        2,
			connectedSlaves: 1,
			syncInProgress:  true,
			wantErr:         "master sync in progress",
		},
		{
			name:      "master rejects INFO",
			replicas:  2,
			infoReply: respError("LOADING Valkey is loading the dataset in memory"),
			wantErr:   "INFO REPLICATION",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := newFakeValkeyNode()
			node.connectedSlaves = tt.connectedSlaves
			node.syncInProgress = tt.syncInProgress
			node.infoReply = tt.infoReply
			ep := startFakeRESP(t, node.handle)

			obs := &Observer{cfg: Config{Replicas: tt.replicas}}
			err := obs.checkReplicaSync(ep.addr)

			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestCheckReplicaSync_UnreachableMaster(t *testing.T) {
	obs := &Observer{cfg: Config{Replicas: 3}}

	err := obs.checkReplicaSync(closedAddr(t))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "INFO REPLICATION")
}

// The write check must land in the configured observer DB and must carry a TTL,
// otherwise it leaks a key into a user database forever.
func TestWriteHealthKey_SelectsObserverDBAndSetsTTL(t *testing.T) {
	node := newFakeValkeyNode()
	ep := startFakeRESP(t, node.handle)
	obs := &Observer{cfg: Config{ObserverDB: 7}}

	require.NoError(t, obs.writeHealthKey(ep.addr, "value-42"))

	assert.Equal(t, "7", node.lastSelectedDB(), "health key must be written to the configured DB")
	stored, ok := node.storedHealthValue()
	require.True(t, ok, "health key must be stored")
	assert.Equal(t, "value-42", stored)

	set := ep.findCommand(cmdSet)
	require.NotNil(t, set, "observer must send SET")
	assert.Equal(t, []string{cmdSet, observerHealthKey, "value-42", "EX", "10"}, set)
}

func TestWriteHealthKey_ServerRejectsSet(t *testing.T) {
	node := newFakeValkeyNode()
	node.setReply = respError("READONLY You can't write against a read only replica")
	ep := startFakeRESP(t, node.handle)
	obs := &Observer{cfg: Config{ObserverDB: 0}}

	err := obs.writeHealthKey(ep.addr, "value-42")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "READONLY")
}

func TestReadHealthKey(t *testing.T) {
	staleValue := "older-value"

	tests := []struct {
		name        string
		preloadVal  string
		getOverride *string
		wantErr     string
	}{
		{
			name:       "value written by the observer is read back",
			preloadVal: "value-42",
		},
		{
			name:        "different value means the read hit stale data",
			preloadVal:  "value-42",
			getOverride: &staleValue,
			wantErr:     `expected "value-42", got "older-value"`,
		},
		{
			name:    "missing key is reported with the empty value",
			wantErr: `expected "value-42", got ""`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := newFakeValkeyNode()
			node.getOverride = tt.getOverride
			ep := startFakeRESP(t, node.handle)
			obs := &Observer{cfg: Config{ObserverDB: 3}}

			if tt.preloadVal != "" {
				require.NoError(t, obs.writeHealthKey(ep.addr, tt.preloadVal))
			}

			err := obs.readHealthKey(ep.addr, "value-42")

			if tt.wantErr == "" {
				require.NoError(t, err)
				assert.Equal(t, "3", node.lastSelectedDB())
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestReadHealthKey_UnreachableHost(t *testing.T) {
	obs := &Observer{cfg: Config{ObserverDB: 0}}

	err := obs.readHealthKey(closedAddr(t), "value-42")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot connect to")
}

// The replica read check names the pod it failed on, which is what makes the
// readiness message actionable, and it addresses replicas on the TLS port when
// TLS is enabled.
func TestCheckReplicaRead_NamesTheFailingReplicaAndItsPort(t *testing.T) {
	tests := []struct {
		name       string
		tlsEnabled bool
		wantPort   string
	}{
		{name: "plaintext", tlsEnabled: false, wantPort: ":6379"},
		{name: "TLS", tlsEnabled: true, wantPort: ":16379"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obs := &Observer{
				cfg: Config{
					ClusterName:       "test",
					ValkeyHeadlessSvc: "test-headless.invalid",
					Replicas:          2,
					TLSEnabled:        tt.tlsEnabled,
				},
			}

			err := obs.checkReplicaRead("value-42")

			require.Error(t, err)
			assert.Contains(t, err.Error(), "replica test-0:", "the failing pod must be named")
			assert.Contains(t, err.Error(), "test-0.test-headless.invalid"+tt.wantPort)
		})
	}
}

func TestCheckSentinelReachable(t *testing.T) {
	t.Run("all sentinels answer", func(t *testing.T) {
		first := startFakeRESP(t, newFakeSentinelNode("valkey-0.example.invalid", "6379").handle)
		second := startFakeRESP(t, newFakeSentinelNode("valkey-0.example.invalid", "6379").handle)
		obs := &Observer{cfg: Config{SentinelAddrList: []string{first.addr, second.addr}}}

		require.NoError(t, obs.checkSentinelReachable())
		assert.True(t, first.sawCommand("PING"))
		assert.True(t, second.sawCommand("PING"))
	})

	t.Run("a reply other than PONG counts as unreachable", func(t *testing.T) {
		sentinel := newFakeSentinelNode("valkey-0.example.invalid", "6379")
		sentinel.pingReply = respSimple("LOADING")
		ep := startFakeRESP(t, sentinel.handle)
		obs := &Observer{cfg: Config{SentinelAddrList: []string{ep.addr}}}

		err := obs.checkSentinelReachable()

		require.Error(t, err)
		assert.Contains(t, err.Error(), "unexpected response")
	})

	t.Run("one dead sentinel is named, the healthy one is not", func(t *testing.T) {
		healthy := startFakeRESP(t, newFakeSentinelNode("valkey-0.example.invalid", "6379").handle)
		dead := closedAddr(t)
		obs := &Observer{cfg: Config{SentinelAddrList: []string{healthy.addr, dead}}}

		err := obs.checkSentinelReachable()

		require.Error(t, err)
		assert.Contains(t, err.Error(), "unreachable sentinels")
		assert.Contains(t, err.Error(), dead)
		assert.NotContains(t, err.Error(), healthy.addr)
	})
}

// Sentinel credentials: with disableAuth the observer must not send AUTH at all,
// because the Sentinel is then configured without a requirepass and would reject
// the command. Every Sentinel-facing call has its own copy of that decision, so
// each one is checked separately - a single forgotten copy would break all
// Sentinel checks on an unauthenticated Sentinel.
func TestSentinelCalls_DisableAuthControlsTheAuthCommand(t *testing.T) {
	calls := []struct {
		name   string
		invoke func(o *Observer) error
	}{
		{"discoverMasterViaSentinel", func(o *Observer) error { _, err := o.discoverMasterViaSentinel(); return err }},
		{"checkSentinelReachable", func(o *Observer) error { return o.checkSentinelReachable() }},
		{"checkSentinelQuorumAndFlags", func(o *Observer) error { _, _, err := o.checkSentinelQuorumAndFlags(); return err }},
		{"checkSentinelMasterHostname", func(o *Observer) error { return o.checkSentinelMasterHostname() }},
		{"checkSentinelReplicaHostnames", func(o *Observer) error { return o.checkSentinelReplicaHostnames() }},
	}
	modes := []struct {
		name        string
		disableAuth bool
		wantAuth    bool
	}{
		{name: "auth enabled sends the password", disableAuth: false, wantAuth: true},
		{name: "auth disabled sends no AUTH", disableAuth: true, wantAuth: false},
	}

	for _, call := range calls {
		for _, mode := range modes {
			t.Run(call.name+"/"+mode.name, func(t *testing.T) {
				sentinel := newFakeSentinelNode("valkey-0.example.invalid", "6379")
				ep := startFakeRESP(t, sentinel.handle)
				obs := &Observer{cfg: Config{
					SentinelMonitor:     "mymaster",
					SentinelAddrList:    []string{ep.addr},
					Password:            "s3cret",
					SentinelDisableAuth: mode.disableAuth,
				}}

				require.NoError(t, call.invoke(obs))

				sawAuth, password := sentinel.authObserved()
				assert.Equal(t, mode.wantAuth, sawAuth)
				if mode.wantAuth {
					assert.Equal(t, "s3cret", password)
				}
			})
		}
	}
}

func TestCheckSentinelQuorumAndFlags(t *testing.T) {
	t.Run("single sentinel with clean flags", func(t *testing.T) {
		ep := startFakeRESP(t, newFakeSentinelNode("valkey-0.example.invalid", "6379").handle)
		obs := &Observer{cfg: Config{SentinelMonitor: "mymaster", SentinelAddrList: []string{ep.addr}}}

		quorumOK, flagsOK, err := obs.checkSentinelQuorumAndFlags()

		assert.True(t, quorumOK)
		assert.True(t, flagsOK)
		assert.NoError(t, err)
	})

	t.Run("two sentinels agreeing on the same master", func(t *testing.T) {
		first := startFakeRESP(t, newFakeSentinelNode("valkey-0.example.invalid", "6379").handle)
		second := startFakeRESP(t, newFakeSentinelNode("valkey-0.example.invalid", "6379").handle)
		obs := &Observer{cfg: Config{
			SentinelMonitor:  "mymaster",
			SentinelAddrList: []string{first.addr, second.addr},
		}}

		quorumOK, flagsOK, err := obs.checkSentinelQuorumAndFlags()

		assert.True(t, quorumOK)
		assert.True(t, flagsOK)
		assert.NoError(t, err)
	})

	t.Run("split view of the master breaks quorum", func(t *testing.T) {
		first := startFakeRESP(t, newFakeSentinelNode("valkey-0.example.invalid", "6379").handle)
		second := startFakeRESP(t, newFakeSentinelNode("valkey-1.example.invalid", "6379").handle)
		obs := &Observer{cfg: Config{
			SentinelMonitor:  "mymaster",
			SentinelAddrList: []string{first.addr, second.addr},
		}}

		quorumOK, flagsOK, err := obs.checkSentinelQuorumAndFlags()

		assert.False(t, quorumOK, "sentinels naming different masters must break quorum")
		assert.True(t, flagsOK, "flags are unrelated to the quorum disagreement")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "sentinel quorum inconsistent")
		assert.Contains(t, err.Error(), "valkey-1.example.invalid:6379")
	})

	t.Run("subjectively down master clears the flags verdict", func(t *testing.T) {
		sentinel := newFakeSentinelNode("valkey-0.example.invalid", "6379")
		sentinel.flags = "master,s_down"
		ep := startFakeRESP(t, sentinel.handle)
		obs := &Observer{cfg: Config{SentinelMonitor: "mymaster", SentinelAddrList: []string{ep.addr}}}

		quorumOK, flagsOK, err := obs.checkSentinelQuorumAndFlags()

		assert.True(t, quorumOK, "a single sentinel always agrees with itself")
		assert.False(t, flagsOK)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "master,s_down")
	})

	t.Run("objectively down master clears the flags verdict", func(t *testing.T) {
		sentinel := newFakeSentinelNode("valkey-0.example.invalid", "6379")
		sentinel.flags = "master,o_down"
		ep := startFakeRESP(t, sentinel.handle)
		obs := &Observer{cfg: Config{SentinelMonitor: "mymaster", SentinelAddrList: []string{ep.addr}}}

		_, flagsOK, err := obs.checkSentinelQuorumAndFlags()

		assert.False(t, flagsOK)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "master,o_down")
	})

	t.Run("unreachable sentinel breaks quorum but leaves flags untouched", func(t *testing.T) {
		healthy := startFakeRESP(t, newFakeSentinelNode("valkey-0.example.invalid", "6379").handle)
		obs := &Observer{cfg: Config{
			SentinelMonitor:  "mymaster",
			SentinelAddrList: []string{healthy.addr, closedAddr(t)},
		}}

		quorumOK, flagsOK, err := obs.checkSentinelQuorumAndFlags()

		assert.False(t, quorumOK)
		assert.True(t, flagsOK, "a sentinel that never answered cannot report bad flags")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cannot connect to")
	})
}

// Sentinel must hand out hostnames: a raw IP breaks TLS verification and survives
// pod restarts only by accident.
func TestCheckSentinelMasterHostname(t *testing.T) {
	t.Run("hostname passes", func(t *testing.T) {
		ep := startFakeRESP(t, newFakeSentinelNode("valkey-0.example.invalid", "6379").handle)
		obs := &Observer{cfg: Config{SentinelMonitor: "mymaster", SentinelAddrList: []string{ep.addr}}}

		assert.NoError(t, obs.checkSentinelMasterHostname())
	})

	t.Run("raw IPv4 fails", func(t *testing.T) {
		ep := startFakeRESP(t, newFakeSentinelNode("10.0.0.5", "6379").handle)
		obs := &Observer{cfg: Config{SentinelMonitor: "mymaster", SentinelAddrList: []string{ep.addr}}}

		err := obs.checkSentinelMasterHostname()

		require.Error(t, err)
		assert.Contains(t, err.Error(), "returned IP 10.0.0.5 instead of hostname for master")
	})

	t.Run("raw IPv6 fails", func(t *testing.T) {
		ep := startFakeRESP(t, newFakeSentinelNode("fd00::5", "6379").handle)
		obs := &Observer{cfg: Config{SentinelMonitor: "mymaster", SentinelAddrList: []string{ep.addr}}}

		err := obs.checkSentinelMasterHostname()

		require.Error(t, err)
		assert.Contains(t, err.Error(), "returned IP fd00::5")
	})

	t.Run("both an unreachable sentinel and an IP answer are reported", func(t *testing.T) {
		ep := startFakeRESP(t, newFakeSentinelNode("10.0.0.5", "6379").handle)
		dead := closedAddr(t)
		obs := &Observer{cfg: Config{
			SentinelMonitor:  "mymaster",
			SentinelAddrList: []string{dead, ep.addr},
		}}

		err := obs.checkSentinelMasterHostname()

		require.Error(t, err)
		assert.Contains(t, err.Error(), dead)
		assert.Contains(t, err.Error(), "10.0.0.5")
	})
}

func TestCheckSentinelReplicaHostnames(t *testing.T) {
	t.Run("all replica hostnames pass", func(t *testing.T) {
		sentinel := newFakeSentinelNode("valkey-0.example.invalid", "6379")
		sentinel.replicas = [][2]string{
			{"valkey-1.example.invalid", "6379"},
			{"valkey-2.example.invalid", "6379"},
		}
		ep := startFakeRESP(t, sentinel.handle)
		obs := &Observer{cfg: Config{SentinelMonitor: "mymaster", SentinelAddrList: []string{ep.addr}}}

		assert.NoError(t, obs.checkSentinelReplicaHostnames())
	})

	t.Run("a single replica reported by IP fails the check", func(t *testing.T) {
		sentinel := newFakeSentinelNode("valkey-0.example.invalid", "6379")
		sentinel.replicas = [][2]string{
			{"valkey-1.example.invalid", "6379"},
			{"10.0.0.9", "6379"},
		}
		ep := startFakeRESP(t, sentinel.handle)
		obs := &Observer{cfg: Config{SentinelMonitor: "mymaster", SentinelAddrList: []string{ep.addr}}}

		err := obs.checkSentinelReplicaHostnames()

		require.Error(t, err)
		assert.Contains(t, err.Error(), "returned IP 10.0.0.9 instead of hostname for replica")
		assert.NotContains(t, err.Error(), "valkey-1.example.invalid")
	})

	t.Run("unreachable sentinel is reported", func(t *testing.T) {
		dead := closedAddr(t)
		obs := &Observer{cfg: Config{SentinelMonitor: "mymaster", SentinelAddrList: []string{dead}}}

		err := obs.checkSentinelReplicaHostnames()

		require.Error(t, err)
		assert.Contains(t, err.Error(), "replica hostname check failed")
		assert.Contains(t, err.Error(), dead)
	})
}

// A Sentinel process that is up but does not monitor the configured master - a
// wrong spec.sentinel.monitor, or a Sentinel that was reset - must surface as a
// Sentinel failure. The PING check alone would call that cluster healthy.
func TestSentinelChecks_ReachableSentinelThatDoesNotKnowTheMaster(t *testing.T) {
	noSuchMaster := respError("No such master with that name")
	sentinel := newFakeSentinelNode("valkey-0.example.invalid", "6379")
	sentinel.masterReply = noSuchMaster
	sentinel.replicasReply = noSuchMaster
	ep := startFakeRESP(t, sentinel.handle)
	obs := &Observer{cfg: Config{
		SentinelMonitor:  "a-name-this-sentinel-does-not-monitor",
		SentinelAddrList: []string{ep.addr},
	}}

	assert.NoError(t, obs.checkSentinelReachable(), "the Sentinel process itself answers")

	quorumOK, flagsOK, err := obs.checkSentinelQuorumAndFlags()
	assert.False(t, quorumOK, "a Sentinel that cannot name the master cannot form a quorum")
	assert.True(t, flagsOK, "no flags were reported, so none can be bad")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "No such master with that name")

	require.Error(t, obs.checkSentinelMasterHostname())
	require.Error(t, obs.checkSentinelReplicaHostnames())

	_, err = obs.discoverMasterViaSentinel()
	require.Error(t, err, "master discovery has no answer to fall back on")
	assert.Contains(t, err.Error(), "no sentinel responded")
}

func TestDiscoverMasterViaSentinel_SkipsSentinelsThatDoNotAnswer(t *testing.T) {
	ep := startFakeRESP(t, newFakeSentinelNode("valkey-2.example.invalid", "6379").handle)
	obs := &Observer{cfg: Config{
		SentinelMonitor:  "mymaster",
		SentinelAddrList: []string{closedAddr(t), ep.addr},
	}}

	addr, err := obs.discoverMasterViaSentinel()

	require.NoError(t, err)
	assert.Equal(t, "valkey-2.example.invalid:6379", addr,
		"the first sentinel that answers decides the master address")
}

func TestDiscoverMaster_SentinelAnswerWins(t *testing.T) {
	ep := startFakeRESP(t, newFakeSentinelNode("valkey-1.example.invalid", "6379").handle)
	obs := &Observer{cfg: Config{
		ClusterName:       "test",
		ValkeyHeadlessSvc: "test-headless.invalid",
		Replicas:          3,
		SentinelEnabled:   true,
		SentinelMonitor:   "mymaster",
		SentinelAddrList:  []string{ep.addr},
	}}

	addr, err := obs.discoverMaster(context.Background())

	require.NoError(t, err)
	assert.Equal(t, "valkey-1.example.invalid:6379", addr,
		"with Sentinel enabled the headless pod-0 fallback must not be used")
}

func TestDiscoverMaster_SentinelFailurePropagates(t *testing.T) {
	obs := &Observer{cfg: Config{
		ClusterName:       "test",
		ValkeyHeadlessSvc: "test-headless.invalid",
		SentinelEnabled:   true,
		SentinelMonitor:   "mymaster",
		SentinelAddrList:  []string{closedAddr(t)},
	}}

	_, err := obs.discoverMaster(context.Background())

	require.Error(t, err, "no silent fallback to pod-0 when Sentinel is the source of truth")
	assert.Contains(t, err.Error(), "no sentinel responded")
}

// --- transport selection ---
//
// newClient and newSentinelClient decide, per call, whether the cluster password
// and every subsequent command travel encrypted or in the clear. The returned
// *valkeyclient.Client keeps its address, TLS config and password unexported,
// and asserting on them would only restate the constructor anyway. The branches
// are therefore pinned by behaviour: a TLS-only endpoint drops a plaintext
// client during the handshake, and a plaintext endpoint cannot answer a TLS
// ClientHello. Reaching the RESP layer proves which transport was really used.

// trustingTLSConfig returns a client TLS config that trusts the given CA file.
func trustingTLSConfig(t *testing.T, caPath string) *tls.Config {
	t.Helper()
	cfg, err := buildTLSConfig(Config{TLSCACert: caPath}, false)
	require.NoError(t, err)
	return cfg
}

func TestNewClient_TLSVariants(t *testing.T) {
	caPath, certPath, keyPath := writeTestCerts(t)
	tlsCfg := trustingTLSConfig(t, caPath)

	t.Run("TLS without password negotiates TLS", func(t *testing.T) {
		ep := startFakeRESPTLS(t, newFakeValkeyNode().handle, certPath, keyPath)
		obs := &Observer{cfg: Config{}, tlsConfig: tlsCfg}

		require.NoError(t, obs.newClient(ep.addr, "").Ping())
		assert.True(t, ep.sawCommand("PING"), "the PING must arrive through the TLS tunnel")
		assert.False(t, ep.sawCommand("AUTH"), "no password configured means no AUTH")
	})

	t.Run("TLS with password sends AUTH through the tunnel", func(t *testing.T) {
		ep := startFakeRESPTLS(t, newFakeValkeyNode().handle, certPath, keyPath)
		obs := &Observer{cfg: Config{}, tlsConfig: tlsCfg}

		require.NoError(t, obs.newClient(ep.addr, "s3cret").Ping())
		assert.Equal(t, []string{"AUTH", "s3cret"}, ep.findCommand("AUTH"))
	})

	t.Run("without a TLS config the plaintext endpoint is used as is", func(t *testing.T) {
		ep := startFakeRESP(t, newFakeValkeyNode().handle)
		obs := &Observer{cfg: Config{}}

		require.NoError(t, obs.newClient(ep.addr, "").Ping())
		assert.False(t, ep.sawCommand("AUTH"))

		require.NoError(t, obs.newClient(ep.addr, "s3cret").Ping())
		assert.Equal(t, []string{"AUTH", "s3cret"}, ep.findCommand("AUTH"))
	})

	// The guard for the whole block: if the TLS branches ever handed back a
	// plaintext client, this is what the observer would be doing on the wire.
	t.Run("a plaintext client never reaches a TLS-only endpoint", func(t *testing.T) {
		ep := startFakeRESPTLS(t, newFakeValkeyNode().handle, certPath, keyPath)
		plaintext := &Observer{cfg: Config{}}

		require.Error(t, plaintext.newClient(ep.addr, "").Ping())
		require.Error(t, plaintext.newClient(ep.addr, "s3cret").Ping())
		assert.Empty(t, ep.commands(),
			"nothing may reach the RESP layer, least of all the password")
	})

	t.Run("a TLS client never reaches a plaintext endpoint", func(t *testing.T) {
		ep := startFakeRESP(t, newFakeValkeyNode().handle)
		obs := &Observer{cfg: Config{}, tlsConfig: tlsCfg}

		require.Error(t, obs.newClient(ep.addr, "s3cret").Ping())
		assert.Empty(t, ep.findCommand("AUTH"), "the password must not leak into a failed handshake")
	})
}

// New only builds a Sentinel TLS config when spec.sentinel.enabled is set, so a
// TLS cluster without that flag reaches Sentinel through the Valkey config.
// Without the fallback the observer would dial a TLS-only Sentinel in the clear
// and push the cluster password onto the wire unencrypted.
func TestNewSentinelClient_UsesValkeyConfigWhenSentinelConfigMissing(t *testing.T) {
	caPath, certPath, keyPath := writeTestCerts(t)
	sentinel := newFakeSentinelNode("valkey-0.example.invalid", "6379")
	ep := startFakeRESPTLS(t, sentinel.handle, certPath, keyPath)

	obs := &Observer{
		cfg: Config{
			SentinelMonitor:  "mymaster",
			SentinelAddrList: []string{ep.addr},
			Password:         "s3cret",
		},
		tlsConfig:         trustingTLSConfig(t, caPath),
		sentinelTLSConfig: nil,
	}

	require.NoError(t, obs.checkSentinelReachable(),
		"a TLS-only Sentinel answers only a client that negotiated TLS")
	sawAuth, password := sentinel.authObserved()
	assert.True(t, sawAuth)
	assert.Equal(t, "s3cret", password)

	require.NoError(t, obs.newSentinelClient(ep.addr, "").Ping(),
		"the password-less branch takes the same fallback")
}

// With both configs present the Sentinel one wins - it is the one built without
// the client certificate. Trust is used to tell the two apart here because it is
// observable from the client side: only one of them verifies this endpoint.
func TestNewSentinelClient_PrefersTheSentinelTLSConfig(t *testing.T) {
	caPath, certPath, keyPath := writeTestCerts(t)
	foreignCA, _, _ := writeTestCerts(t)
	trusting := trustingTLSConfig(t, caPath)
	distrusting := trustingTLSConfig(t, foreignCA)

	ep := startFakeRESPTLS(t, newFakeSentinelNode("valkey-0.example.invalid", "6379").handle, certPath, keyPath)

	obs := &Observer{cfg: Config{}, sentinelTLSConfig: trusting, tlsConfig: distrusting}
	require.NoError(t, obs.newSentinelClient(ep.addr, "s3cret").Ping(),
		"only the Sentinel config trusts this endpoint, so the handshake proves it was used")

	// Swapping the roles proves the success above was not an accident.
	swapped := &Observer{cfg: Config{}, sentinelTLSConfig: distrusting, tlsConfig: trusting}
	err := swapped.newSentinelClient(ep.addr, "s3cret").Ping()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "TLS handshake")
}

func TestNewSentinelClient_WithoutAnyTLSConfigStaysPlaintext(t *testing.T) {
	sentinel := newFakeSentinelNode("valkey-0.example.invalid", "6379")
	ep := startFakeRESP(t, sentinel.handle)
	obs := &Observer{cfg: Config{}}

	require.NoError(t, obs.newSentinelClient(ep.addr, "").Ping())
	sawAuth, _ := sentinel.authObserved()
	assert.False(t, sawAuth, "no password means no AUTH")

	require.NoError(t, obs.newSentinelClient(ep.addr, "s3cret").Ping())
	sawAuth, password := sentinel.authObserved()
	assert.True(t, sawAuth)
	assert.Equal(t, "s3cret", password)
}
