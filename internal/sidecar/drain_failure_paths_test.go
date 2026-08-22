package sidecar

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/guided-traffic/valkey-operator/internal/common"
)

// The drain handler runs while the pod is already terminating. Everything after
// the promotion is best-effort, and "best-effort" is only true if a single failing
// peer does not take the rest of the sequence with it. The tests here drive the
// two failure branches that decide that.

// recordedLogLine is one call the drain handler made on its logger.
type recordedLogLine struct {
	level string // "info" or "error"
	msg   string
	err   error
	kv    []interface{}
}

// recordingDrainLog captures what the drain handler logged. The failure branches
// under test have no other observable effect on the pod being drained, so the log
// line IS the outcome: it is the only trace an operator gets of a peer that was
// left pointing at a master that no longer exists.
type recordingDrainLog struct {
	mu    sync.Mutex
	lines []recordedLogLine
}

func (l *recordingDrainLog) Info(msg string, kv ...interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, recordedLogLine{level: "info", msg: msg, kv: kv})
}

func (l *recordingDrainLog) Error(err error, msg string, kv ...interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, recordedLogLine{level: "error", msg: msg, err: err, kv: kv})
}

// errors returns every line logged at error level.
func (l *recordingDrainLog) errors() []recordedLogLine {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []recordedLogLine
	for _, line := range l.lines {
		if line.level == "error" {
			out = append(out, line)
		}
	}
	return out
}

// value returns the value of a key/value pair of a recorded line.
func (line recordedLogLine) value(key string) interface{} {
	for i := 0; i+1 < len(line.kv); i += 2 {
		if line.kv[i] == key {
			return line.kv[i+1]
		}
	}
	return nil
}

// A replica that cannot be repointed at the new master is the common case during a
// node drain: several pods go down together. If that aborted the reconfiguration,
// the peers after the failing one would keep following the pod that is shutting
// down, and the operator would find them as replicas of a dead master. Every
// remaining peer must still be told, and the failure must be reported with the
// address so it can be chased.
func TestDrainHandler_ReconfigureReplicasContinuesPastAFailingPeer(t *testing.T) {
	const (
		unreachable = "test-1.test-headless.default.svc.cluster.local:6379"
		reachable   = "test-2.test-headless.default.svc.cluster.local:6379"
	)
	factory := &mockValkeyClientFactory{clients: map[string]*mockValkeyCommander{
		unreachable: {replicaOfErr: fmt.Errorf("dial tcp: connect: no route to host")},
		reachable:   {},
	}}
	handler := newTestDrainHandler(&changingRoleDetector{role: common.RoleMaster},
		&mockPodPatcher{}, factory)
	log := &recordingDrainLog{}

	handler.reconfigureReplicas("test-0.test-headless.default.svc.cluster.local",
		"test-0.test-headless.default.svc.cluster.local:6379", log)

	assert.Len(t, factory.clients[unreachable].calls(), 1, "the failing peer is still attempted")
	require.Len(t, factory.clients[reachable].calls(), 1,
		"a peer that fails must not stop the peers behind it from being repointed")
	assert.Equal(t, replicaOfRecord{host: "test-0.test-headless.default.svc.cluster.local", port: "6379"},
		factory.clients[reachable].calls()[0])

	failures := log.errors()
	require.Len(t, failures, 1, "exactly the failing peer is reported")
	assert.Equal(t, unreachable, failures[0].value("addr"),
		"the report names the peer that was left behind")
	assert.Equal(t, "test-0.test-headless.default.svc.cluster.local", failures[0].value("newMaster"))
	require.Error(t, failures[0].err)
}

// scriptedRoleDetector answers DetectRole from a fixed script and repeats the last
// entry once the script runs out, so a test can put a specific answer on a specific
// poll instead of racing a timer against the handler's 1s ticker.
type scriptedRoleDetector struct {
	mu      sync.Mutex
	script  []scriptedRole
	callNum int
}

type scriptedRole struct {
	role string
	err  error
}

func (d *scriptedRoleDetector) DetectRole() (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	i := d.callNum
	d.callNum++
	if i >= len(d.script) {
		i = len(d.script) - 1
	}
	return d.script[i].role, d.script[i].err
}

func (d *scriptedRoleDetector) calls() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.callNum
}

// A role probe can fail for reasons that say nothing about the failover: a slow
// INFO, a dropped connection, an AUTH hiccup. Only "connection refused" means the
// local server is gone and the drain may finish. Anything else has to be retried,
// because giving up early lets the container exit while this pod is still the
// master -- the writes in flight then have nowhere to go.
func TestDrainHandler_WaitForRoleChangeRetriesATransientProbeFailure(t *testing.T) {
	detector := &scriptedRoleDetector{script: []scriptedRole{
		{err: fmt.Errorf("read tcp 127.0.0.1:6379: i/o timeout")},
		{role: common.RoleReplica},
	}}
	handler := newTestDrainHandler(detector, &mockPodPatcher{}, &mockValkeyClientFactory{})
	log := &recordingDrainLog{}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	require.NoError(t, handler.waitForRoleChange(ctx, log),
		"a transient probe failure is not a failed drain")
	assert.Equal(t, 2, detector.calls(),
		"the loop must poll again after the transient failure instead of returning on it")

	failures := log.errors()
	require.Len(t, failures, 1, "the transient failure is reported once, not swallowed")
	require.Error(t, failures[0].err)
	assert.Contains(t, failures[0].err.Error(), "i/o timeout")
}

// The same script through the public entry point: Handle must not report the drain
// as failed, and the local pod must have been polled again after the failure.
func TestDrainHandler_TransientProbeFailureDoesNotFailTheDrain(t *testing.T) {
	detector := &scriptedRoleDetector{script: []scriptedRole{
		{role: common.RoleMaster},                                 // Handle's own role check
		{err: fmt.Errorf("read tcp 127.0.0.1:6379: i/o timeout")}, // first poll
		{role: common.RoleReplica},                                // second poll
	}}
	sentinelClient := &mockValkeyCommander{}
	factory := &mockValkeyClientFactory{clients: map[string]*mockValkeyCommander{
		"sentinel-0:26379": sentinelClient,
	}}
	handler := newTestDrainHandler(detector, &mockPodPatcher{}, factory, func(h *DrainHandler) {
		h.sentinelEnabled = true
		h.sentinelMonitor = "test"
		h.sentinelAddrs = []string{"sentinel-0:26379"}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	require.NoError(t, handler.Handle(ctx))
	assert.Equal(t, []string{"test"}, sentinelClient.failoverCalls, "the failover was still requested")
	assert.Equal(t, 3, detector.calls(),
		"one role check plus two polls: the loop retried instead of exiting on the failure")
}
