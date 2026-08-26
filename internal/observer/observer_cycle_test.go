package observer

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	ctrl "sigs.k8s.io/controller-runtime"
)

// logLevelWarn is the level the observer runs at in these tests; see the comment
// on TestBuildObserverLogger_WarnLevelSuppressesInfoLogs for why it must be the
// same one everywhere in this package.
const logLevelWarn = "warn"

// This file exercises a complete poll cycle against real RESP endpoints, plus
// the polling lifecycle itself. The logger test comes first on purpose - see the
// comment on TestBuildObserverLogger_WarnLevelSuppressesInfoLogs.

// captureStderr redirects os.Stderr to a file for the remainder of the test and
// returns a reader for what was written. A file rather than a pipe is used on
// purpose: controller-runtime fulfils its global log sink exactly once per
// process, so the zap logger built against this writer keeps it forever and a
// closed pipe would break logging for every later test.
func captureStderr(t *testing.T) func() string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "stderr-*.log")
	require.NoError(t, err)

	original := os.Stderr
	os.Stderr = f
	t.Cleanup(func() { os.Stderr = original })

	return func() string {
		data, readErr := os.ReadFile(f.Name())
		require.NoError(t, readErr)
		return string(data)
	}
}

// The observer runs at info level by default; operators lower it to warn to keep
// expected check failures out of the logs. Only the first configuration in a
// process is observable, because controller-runtime fulfils its delegating log
// sink exactly once - which is why this test must be the first one in the
// package to configure a logger, and why TestRun_PollsUntilContextCancelled also
// runs at warn level.
func TestBuildObserverLogger_WarnLevelSuppressesInfoLogs(t *testing.T) {
	read := captureStderr(t)

	buildObserverLogger(logLevelWarn)
	logger := ctrl.Log.WithName("observer")
	logger.Info("info-probe")
	logger.Error(errors.New("boom"), "error-probe")

	// The remaining branches are exercised for their option wiring; they cannot
	// change the global sink any more.
	for _, level := range []string{"debug", "info", "error", "unspecified"} {
		buildObserverLogger(level)
	}

	out := read()
	if out == "" {
		// Nothing reached this writer, so the sink had already been fulfilled
		// before this test ran - which only happens under go test -count>1.
		t.Skip("the global log sink was already fulfilled by an earlier run in this process")
	}
	assert.NotContains(t, out, "info-probe", "warn level must drop Info records")
	assert.Contains(t, out, "error-probe", "warn level must keep Error records")
	assert.NotContains(t, out, "goroutine ",
		"stack traces are suppressed below DPanic so expected failures stay readable")
}

// --- fixtures ---

// greenCluster is a Sentinel-fronted single-node cluster where every check
// passes. Tests break exactly one thing and assert on the verdict.
type greenCluster struct {
	node       *fakeValkeyNode
	nodeEP     *fakeRESP
	sentinel   *fakeSentinelNode
	sentinelEP *fakeRESP
	cfg        Config
}

func newGreenCluster(t *testing.T) *greenCluster {
	t.Helper()

	node := newFakeValkeyNode()
	node.connectedSlaves = 2
	nodeEP := startFakeRESP(t, node.handle)

	// Sentinel reports the data node under a hostname, which is both what the
	// hostname checks demand and an address that resolves back to the endpoint.
	sentinel := newFakeSentinelNode("localhost", nodeEP.port)
	sentinelEP := startFakeRESP(t, sentinel.handle)

	return &greenCluster{
		node:       node,
		nodeEP:     nodeEP,
		sentinel:   sentinel,
		sentinelEP: sentinelEP,
		cfg: Config{
			Namespace:         "default",
			ClusterName:       "test",
			ValkeyHeadlessSvc: "test-headless.invalid",
			Replicas:          1,
			ObserverDB:        9,
			SentinelEnabled:   true,
			SentinelMonitor:   "mymaster",
			SentinelAddrList:  []string{sentinelEP.addr},
		},
	}
}

func mustObserver(t *testing.T, cfg Config) *Observer {
	t.Helper()
	obs, err := New(cfg)
	require.NoError(t, err)
	return obs
}

func allUnreadyWhen(enabled bool) UnreadyWhenConfig {
	return UnreadyWhenConfig{
		MasterUnreachable:               enabled,
		WriteTestFailure:                enabled,
		ReadTestFailure:                 enabled,
		ReplicaSyncFailure:              enabled,
		ReplicaReadTestFailure:          enabled,
		SentinelUnreachable:             enabled,
		SentinelQuorumFailure:           enabled,
		SentinelMasterDown:              enabled,
		SentinelMasterHostnameInvalid:   enabled,
		SentinelReplicaHostnamesInvalid: enabled,
	}
}

// --- full cycle ---

func TestRunChecks_HealthyClusterPassesEveryCheck(t *testing.T) {
	c := newGreenCluster(t)
	c.cfg.UnreadyWhen = allUnreadyWhen(true)
	obs := mustObserver(t, c.cfg)

	obs.runChecks(context.Background())

	res := obs.GetResult()
	require.True(t, res.Ready, "message: %s", res.Message)
	assert.Empty(t, res.Message)
	assert.WithinDuration(t, time.Now(), res.LastCheck, time.Minute)
	for _, name := range []string{
		checkMasterReachable, "write_test", "read_test",
		"sentinel_reachable", "sentinel_quorum", "sentinel_flags",
		"sentinel_master_hostname", "sentinel_replica_hostnames",
	} {
		assert.True(t, res.Checks[name], "check %s must pass", name)
	}
	assert.NotContains(t, res.Checks, "replica_sync", "a single-replica cluster has no replicas to sync")
	assert.NotContains(t, res.Checks, "replica_read_test")

	// The read check passed against the value the write check stored, in the
	// configured observer DB.
	stored, ok := c.node.storedHealthValue()
	require.True(t, ok, "the write check must have stored the health key")
	assert.NotEmpty(t, stored)
	assert.Equal(t, "9", c.node.lastSelectedDB())
}

func TestRunChecks_MultiReplicaAddsTheReplicaChecks(t *testing.T) {
	c := newGreenCluster(t)
	c.cfg.Replicas = 3
	c.cfg.UnreadyWhen = allUnreadyWhen(false)
	obs := mustObserver(t, c.cfg)

	obs.runChecks(context.Background())

	res := obs.GetResult()
	assert.True(t, res.Checks["replica_sync"], "the master reports both replicas connected")
	// The replica read check addresses pods by their StatefulSet hostname, which
	// does not resolve in a unit test - it must still have been attempted.
	assert.Contains(t, res.Checks, "replica_read_test")
	assert.False(t, res.Checks["replica_read_test"])
}

func TestRunChecks_MasterDiscoveryFailureShortCircuitsTheCycle(t *testing.T) {
	obs := mustObserver(t, Config{
		ClusterName:       "test",
		ValkeyHeadlessSvc: "test-headless.invalid",
		Replicas:          3,
		SentinelEnabled:   true,
		SentinelMonitor:   "mymaster",
		SentinelAddrList:  []string{closedAddr(t)},
		UnreadyWhen:       allUnreadyWhen(true),
	})

	obs.runChecks(context.Background())

	res := obs.GetResult()
	assert.False(t, res.Ready)
	assert.Contains(t, res.Message, "[masterUnreachable] master discovery failed")
	assert.Equal(t, map[string]bool{checkMasterReachable: false}, res.Checks,
		"no other check may run while the master is unknown")
}

// A failed write makes the read check meaningless, so it must not be attempted -
// it would otherwise compare against a value that was never stored and report a
// second, misleading failure.
func TestRunCoreChecks_NoReadIsAttemptedAfterAFailedWrite(t *testing.T) {
	c := newGreenCluster(t)
	c.node.configure(func(n *fakeValkeyNode) {
		n.setReply = respError("READONLY You can't write against a read only replica")
	})
	c.cfg.UnreadyWhen = allUnreadyWhen(true)
	obs := mustObserver(t, c.cfg)

	obs.runChecks(context.Background())

	res := obs.GetResult()
	assert.False(t, res.Checks["write_test"])
	assert.False(t, res.Checks["read_test"], "the read check is reported failed, not omitted")
	assert.False(t, c.nodeEP.sawCommand(cmdGet), "no GET may reach the master after the write failed")
	assert.Contains(t, res.Message, "[writeTestFailure]")
}

func TestRunCoreChecks_ReplicaReadIsSkippedWhenTheWriteFailed(t *testing.T) {
	c := newGreenCluster(t)
	c.cfg.Replicas = 3
	c.cfg.UnreadyWhen = allUnreadyWhen(true)
	c.node.configure(func(n *fakeValkeyNode) {
		n.setReply = respError("READONLY You can't write against a read only replica")
	})
	obs := mustObserver(t, c.cfg)

	obs.runChecks(context.Background())

	res := obs.GetResult()
	assert.True(t, res.Checks["replica_sync"])
	assert.NotContains(t, res.Checks, "replica_read_test",
		"comparing replicas against a value that was never written proves nothing")
}

// --- unreadyWhen matrix ---

// unreadyCase breaks exactly one check in an otherwise healthy cluster and pins
// what spec.observer.unreadyWhen does with the failure.
type unreadyCase struct {
	name     string
	label    string
	checkKey string
	setFlag  func(uw *UnreadyWhenConfig, enabled bool)
	arrange  func(t *testing.T, c *greenCluster)
}

func unreadyCases() []unreadyCase {
	staleValue := "value-from-an-earlier-cycle"
	readOnly := respError("READONLY You can't write against a read only replica")

	return []unreadyCase{
		{
			name:     "masterUnreachable",
			label:    "masterUnreachable",
			checkKey: checkMasterReachable,
			setFlag:  func(uw *UnreadyWhenConfig, v bool) { uw.MasterUnreachable = v },
			arrange: func(t *testing.T, c *greenCluster) {
				dead := closedPort(t)
				c.sentinel.configure(func(s *fakeSentinelNode) { s.masterPort = dead })
			},
		},
		{
			name:     "writeTestFailure",
			label:    "writeTestFailure",
			checkKey: "write_test",
			setFlag:  func(uw *UnreadyWhenConfig, v bool) { uw.WriteTestFailure = v },
			arrange: func(_ *testing.T, c *greenCluster) {
				c.node.configure(func(n *fakeValkeyNode) { n.setReply = readOnly })
			},
		},
		{
			name:     "readTestFailure",
			label:    "readTestFailure",
			checkKey: "read_test",
			setFlag:  func(uw *UnreadyWhenConfig, v bool) { uw.ReadTestFailure = v },
			arrange: func(_ *testing.T, c *greenCluster) {
				c.node.configure(func(n *fakeValkeyNode) { n.getOverride = &staleValue })
			},
		},
		{
			name:     "replicaSyncFailure",
			label:    "replicaSyncFailure",
			checkKey: "replica_sync",
			setFlag:  func(uw *UnreadyWhenConfig, v bool) { uw.ReplicaSyncFailure = v },
			arrange: func(_ *testing.T, c *greenCluster) {
				c.cfg.Replicas = 3
				c.node.configure(func(n *fakeValkeyNode) { n.connectedSlaves = 0 })
			},
		},
		{
			name:     "replicaReadTestFailure",
			label:    "replicaReadTestFailure",
			checkKey: "replica_read_test",
			setFlag:  func(uw *UnreadyWhenConfig, v bool) { uw.ReplicaReadTestFailure = v },
			arrange: func(_ *testing.T, c *greenCluster) {
				// Replica sync passes (two replicas connected), only the direct
				// read against the replica pods cannot be served.
				c.cfg.Replicas = 3
			},
		},
		{
			name:     "sentinelUnreachable",
			label:    "sentinelUnreachable",
			checkKey: "sentinel_reachable",
			setFlag:  func(uw *UnreadyWhenConfig, v bool) { uw.SentinelUnreachable = v },
			arrange: func(t *testing.T, c *greenCluster) {
				// The healthy sentinel stays first so master discovery still works.
				c.cfg.SentinelAddrList = append(c.cfg.SentinelAddrList, closedAddr(t))
			},
		},
		{
			name:     "sentinelQuorumFailure",
			label:    "sentinelQuorumFailure",
			checkKey: "sentinel_quorum",
			setFlag:  func(uw *UnreadyWhenConfig, v bool) { uw.SentinelQuorumFailure = v },
			arrange: func(t *testing.T, c *greenCluster) {
				disagreeing := startFakeRESP(t, newFakeSentinelNode("valkey-9.example.invalid", "6379").handle)
				c.cfg.SentinelAddrList = append(c.cfg.SentinelAddrList, disagreeing.addr)
			},
		},
		{
			name:     "sentinelMasterDown",
			label:    "sentinelMasterDown",
			checkKey: "sentinel_flags",
			setFlag:  func(uw *UnreadyWhenConfig, v bool) { uw.SentinelMasterDown = v },
			arrange: func(_ *testing.T, c *greenCluster) {
				c.sentinel.configure(func(s *fakeSentinelNode) { s.flags = "master,o_down" })
			},
		},
		{
			name:     "sentinelMasterHostnameInvalid",
			label:    "sentinelMasterHostnameInvalid",
			checkKey: "sentinel_master_hostname",
			setFlag:  func(uw *UnreadyWhenConfig, v bool) { uw.SentinelMasterHostnameInvalid = v },
			arrange: func(_ *testing.T, c *greenCluster) {
				// Still reachable, only reported as a raw IP.
				c.sentinel.configure(func(s *fakeSentinelNode) { s.masterIP = "127.0.0.1" })
			},
		},
		{
			name:     "sentinelReplicaHostnamesInvalid",
			label:    "sentinelReplicaHostnamesInvalid",
			checkKey: "sentinel_replica_hostnames",
			setFlag:  func(uw *UnreadyWhenConfig, v bool) { uw.SentinelReplicaHostnamesInvalid = v },
			arrange: func(_ *testing.T, c *greenCluster) {
				c.sentinel.configure(func(s *fakeSentinelNode) {
					s.replicas = [][2]string{{"10.0.0.9", "6379"}}
				})
			},
		},
	}
}

// Every unreadyWhen flag decides one thing only: whether the failure of its
// check turns the pod unready. The check itself always runs and is always
// reported, and the readiness message always names the flag that tripped.
func TestRunChecks_UnreadyWhenMatrix(t *testing.T) {
	for _, tc := range unreadyCases() {
		t.Run(tc.name+"/enabled makes the cluster unready", func(t *testing.T) {
			c := newGreenCluster(t)
			tc.arrange(t, c)
			c.cfg.UnreadyWhen = allUnreadyWhen(false)
			tc.setFlag(&c.cfg.UnreadyWhen, true)
			obs := mustObserver(t, c.cfg)

			obs.runChecks(context.Background())

			res := obs.GetResult()
			assert.False(t, res.Ready, "checks: %v", res.Checks)
			assert.False(t, res.Checks[tc.checkKey], "the broken check must be reported as failed")
			assert.Contains(t, res.Message, "["+tc.label+"]",
				"the readiness message must name the flag that tripped")
		})

		t.Run(tc.name+"/disabled keeps the cluster ready", func(t *testing.T) {
			c := newGreenCluster(t)
			tc.arrange(t, c)
			c.cfg.UnreadyWhen = allUnreadyWhen(false)
			obs := mustObserver(t, c.cfg)

			obs.runChecks(context.Background())

			res := obs.GetResult()
			assert.True(t, res.Ready, "message: %s", res.Message)
			assert.Empty(t, res.Message)
			assert.False(t, res.Checks[tc.checkKey],
				"the check still runs and still fails, it just does not gate readiness")
		})
	}
}

func TestRunCheck(t *testing.T) {
	boom := errors.New("boom")

	tests := []struct {
		name          string
		err           error
		causesUnready bool
		incoming      string
		wantMessage   string
		wantCheck     bool
	}{
		{
			name:          "success records the check and leaves the message empty",
			causesUnready: true,
			wantCheck:     true,
		},
		{
			name:          "failure that gates readiness produces a labelled message",
			err:           boom,
			causesUnready: true,
			wantMessage:   "[someCheck] boom",
		},
		{
			name:          "failure that does not gate readiness produces no message",
			err:           boom,
			causesUnready: false,
			wantMessage:   "",
		},
		{
			name:          "the first failure of a cycle keeps the message",
			err:           boom,
			causesUnready: true,
			incoming:      "[earlierCheck] earlier failure",
			wantMessage:   "[earlierCheck] earlier failure",
		},
		{
			name:          "a later success does not clear an earlier failure",
			causesUnready: true,
			incoming:      "[earlierCheck] earlier failure",
			wantMessage:   "[earlierCheck] earlier failure",
			wantCheck:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checks := make(map[string]bool)

			got := runCheck(logr.Discard(), checks, "some_check", "someCheck",
				tt.incoming, tt.causesUnready, func() error { return tt.err })

			assert.Equal(t, tt.wantMessage, got)
			assert.Equal(t, tt.wantCheck, checks["some_check"])
			assert.Contains(t, checks, "some_check", "every check reports a verdict")
		})
	}
}

// --- polling lifecycle ---

func TestRun_PollsUntilContextCancelled(t *testing.T) {
	c := newGreenCluster(t)
	c.cfg.UnreadyWhen = allUnreadyWhen(true)
	c.cfg.PollInterval = 20 * time.Millisecond
	c.cfg.LogLevel = logLevelWarn
	c.cfg.HealthAddr = closedAddr(t)

	// Run registers its collectors with the process-wide default registry, so
	// they have to be released again for a repeated run to succeed.
	t.Cleanup(func() {
		for _, collector := range newObserverMetrics().collectors() {
			prometheus.DefaultRegisterer.Unregister(collector)
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, c.cfg) }()

	// Readiness flips only after a poll cycle has actually run.
	var body []byte
	require.Eventually(t, func() bool {
		status, payload := httpGet(t, "http://"+c.cfg.HealthAddr+"/readyz")
		body = payload
		return status == http.StatusOK
	}, 10*time.Second, 20*time.Millisecond, "observer never became ready")

	var result CheckResult
	require.NoError(t, json.Unmarshal(body, &result))
	assert.True(t, result.Ready)
	assert.True(t, result.Checks[checkMasterReachable])

	// Run registers the observer metrics, and the same server exposes them.
	_, metrics := httpGet(t, "http://"+c.cfg.HealthAddr+"/metrics")
	assert.Contains(t, string(metrics), "valkey_observer_healthy 1")
	assert.Regexp(t, `valkey_observer_checks_total [1-9]`, string(metrics))
	assert.Regexp(t, `valkey_observer_check_duration_seconds_count [1-9]`, string(metrics))

	cancel()

	select {
	case err := <-done:
		require.NoError(t, err, "cancellation is a clean shutdown, not an error")
	case <-time.After(30 * time.Second):
		t.Fatal("Run did not return after the context was cancelled")
	}

	assert.Eventually(t, func() bool {
		status, _ := httpGet(t, "http://"+c.cfg.HealthAddr+"/readyz")
		return status == 0
	}, 10*time.Second, 20*time.Millisecond, "health server outlived the polling loop")
}

// A broken TLS configuration must stop the observer before it registers metrics
// or opens the health port, so the pod fails visibly instead of serving a
// readiness endpoint that never turns ready.
func TestRun_FailsFastOnAnInvalidTLSConfig(t *testing.T) {
	err := Run(context.Background(), Config{
		ClusterName:  "test",
		LogLevel:     logLevelWarn,
		PollInterval: time.Second,
		HealthAddr:   closedAddr(t),
		TLSEnabled:   true,
		TLSCACert:    "/nonexistent/ca.crt",
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "creating observer")
	assert.Contains(t, err.Error(), "reading TLS material")
}

// httpGet returns the status code (0 when the request failed) and the body.
func httpGet(t *testing.T, url string) (int, []byte) {
	t.Helper()
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return 0, nil
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, body
}
