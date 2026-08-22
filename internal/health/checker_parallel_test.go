package health

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
)

// probeDelay is how long each stand-in pod takes to answer INFO replication. It
// stands in for the 5 s client timeout of a pod that is Running but not
// answering, scaled down to keep the unit tier fast.
const probeDelay = 300 * time.Millisecond

// answersAfter replies like answers, but only after delay — a pod that is slow
// or dead rather than absent. AUTH is answered immediately so the delay is
// counted once per probe, not once per command.
func answersAfter(delay time.Duration, reply string) respondFn {
	return func(request string) string {
		if strings.Contains(strings.ToUpper(request), "AUTH") {
			return "+OK\r\n"
		}
		time.Sleep(delay)
		return reply
	}
}

func withReplicas(n int32) func(*vkov1.Valkey) {
	return func(v *vkov1.Valkey) { v.Spec.Replicas = n }
}

// TestFindMaster_ProbesPodsConcurrently is the regression guard for the second
// half of ADR 0019: the reconcile worker must not be held for one client timeout
// per pod.
//
// Sequentially the five probes below cost 5 x probeDelay. That is the shape that
// coupled the whole fleet to one unresponsive cluster — every other Valkey CR
// waited behind it in the single-worker queue. The bound is deliberately loose
// (3 x probeDelay): it fails hard on any sequential implementation and does not
// depend on how fast the test machine schedules five goroutines.
func TestFindMaster_ProbesPodsConcurrently(t *testing.T) {
	ctx, _ := newProbeContext(t)
	v := newTestValkey("test", "default", noSentinel, withReplicas(5))

	router := newProbeRouter()
	for i := 0; i < 5; i++ {
		reply := replicaInfo()
		if i == 4 {
			reply = masterInfo(4)
		}
		router.serve(t, fmt.Sprintf("test-%d", i), answersAfter(probeDelay, reply))
	}
	checker := router.install(newFakeChecker(runningTestPods(5)...))

	start := time.Now()
	podName, addr, err := checker.findMaster(ctx, v, "", nil)
	elapsed := time.Since(start)

	require.NoError(t, err)
	assert.Equal(t, "test-4", podName, "the pod reporting role:master is the master")
	assert.Equal(t, "test-4.test-headless.default.svc.cluster.local:6379", addr)
	assert.Len(t, router.dialedAddrs(), 5, "every pod must still be probed exactly once")
	assert.Less(t, elapsed, 3*probeDelay,
		"the probes must run concurrently; %v is the sequential shape that holds the reconcile worker", elapsed)
}

// TestFindMaster_SlowPodDoesNotHideAFastMaster pins that concurrency did not turn
// the probe set into a race: a master answering last is still found, and a pod
// that never answers does not suppress the others.
func TestFindMaster_SlowPodDoesNotHideAFastMaster(t *testing.T) {
	ctx, _ := newProbeContext(t)
	v := newTestValkey("test", "default", noSentinel, withReplicas(3))

	router := newProbeRouter()
	router.serve(t, "test-0", answersAfter(probeDelay, replicaInfo()))
	router.serve(t, "test-1", answers(replicaInfo()))
	router.serve(t, "test-2", answersAfter(probeDelay, masterInfo(2)))

	podName, _, err := router.install(newFakeChecker(runningTestPods(3)...)).findMaster(ctx, v, "", nil)

	require.NoError(t, err)
	assert.Equal(t, "test-2", podName,
		"findMaster must wait for every probe, not return the first answer that arrives")
}

// TestFindMaster_TiedMastersResolveToTheLowestOrdinal covers the determinism the
// parallel probes force. Two masters with the same connected_slaves count used to
// be arbitrated by sort.Slice, which is not stable, and appending in completion
// order would have made the winner depend on which pod replied first — a
// non-Sentinel master authority that flips between passes for no observable
// reason (docs/adr/0008-known-master-annotation-is-the-recorded-authority.md).
//
// The run count is what makes this a real check: one iteration passes by luck.
func TestFindMaster_TiedMastersResolveToTheLowestOrdinal(t *testing.T) {
	ctx, _ := newProbeContext(t)
	v := newTestValkey("test", "default", noSentinel, withReplicas(3))

	for i := 0; i < 20; i++ {
		router := newProbeRouter()
		// test-2 answers instantly, test-1 with a delay: in completion order the
		// later pod would win.
		router.serve(t, "test-0", answers(replicaInfo()))
		router.serve(t, "test-1", answersAfter(20*time.Millisecond, masterInfo(1)))
		router.serve(t, "test-2", answers(masterInfo(1)))

		podName, _, err := router.install(newFakeChecker(runningTestPods(3)...)).findMaster(ctx, v, "", nil)

		require.NoError(t, err)
		require.Equal(t, "test-1", podName,
			"run %d: equal slave counts must resolve to the lowest ordinal, not to whoever answered first", i)
	}
}

// TestFindMaster_MoreSlavesStillWins guards that the ordinal tie-break did not
// displace the rule it breaks ties for: the master serving real replicas wins
// even when a lower-ordinal pod also claims the role.
func TestFindMaster_MoreSlavesStillWins(t *testing.T) {
	ctx, _ := newProbeContext(t)
	v := newTestValkey("test", "default", noSentinel, withReplicas(3))

	router := newProbeRouter()
	router.serve(t, "test-0", answers(masterInfo(0)))
	router.serve(t, "test-1", answers(replicaInfo()))
	router.serve(t, "test-2", answers(masterInfo(2)))

	podName, _, err := router.install(newFakeChecker(runningTestPods(3)...)).findMaster(ctx, v, "", nil)

	require.NoError(t, err)
	assert.Equal(t, "test-2", podName,
		"the master with the most connected replicas is the one holding client data")
}

// TestFindMaster_ConcurrentProbesAreRaceFree exercises the shared result slice
// under -race with every pod answering at once. Without the index-keyed slice the
// probes would append to one slice from several goroutines.
func TestFindMaster_ConcurrentProbesAreRaceFree(t *testing.T) {
	ctx, _ := newProbeContext(t)
	v := newTestValkey("test", "default", noSentinel, withReplicas(5))

	router := newProbeRouter()
	for i := 0; i < 5; i++ {
		router.serve(t, fmt.Sprintf("test-%d", i), answers(masterInfo(i)))
	}
	checker := router.install(newFakeChecker(runningTestPods(5)...))

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			podName, _, err := checker.findMaster(ctx, v, "", nil)
			assert.NoError(t, err)
			assert.Equal(t, "test-4", podName, "the master with the most replicas wins in every pass")
		}()
	}
	wg.Wait()
}
