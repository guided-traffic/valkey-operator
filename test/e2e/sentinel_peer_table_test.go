//go:build e2e

package e2e

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/guided-traffic/valkey-operator/test/testimages"
)

// ---------------------------------------------------------------------------
// TestE2E_SentinelPeerTableSurvivesPodReplacement
//
// Regression test for the peer-table drift ADR 0022 removes.
//
// Sentinel never forgets a peer it has seen. Before the fix a replacement pod
// booted from the ConfigMap template with a freshly generated "sentinel myid" and
// a new pod IP, so its peers matched neither by runid nor by address and recorded
// it next to the dead one. num-other-sentinels then climbed by one per
// replacement, and since a failover leader needs a majority of the whole table,
// the margin was spent on Sentinels that no longer exist. Measured before the
// fix: two live Sentinels with five known peers each never promoted a replica
// after the master was killed, where the same topology with clean tables promoted
// one in under ten seconds.
//
// The test replaces the same sentinel pod twice, which is the churn pattern that
// grows without bound -- chaos kills, evictions and node drains that hit a subset
// while the others stay up. A full tier roll is self-limiting because each pod
// destroys its own table when it is replaced, so it would not catch a regression
// here.
// ---------------------------------------------------------------------------
func TestE2E_SentinelPeerTableSurvivesPodReplacement(t *testing.T) {
	t.Parallel()
	tc := newTestClients(t)
	ns := "e2e-sentinel-peers"
	cleanup := tc.createNamespace(t, ns)
	defer cleanup()

	name := "peers"
	valkey := buildValkeyObject(name, ns, map[string]interface{}{
		"replicas": int64(3),
		"image":    testimages.Default(),
		"sentinel": map[string]interface{}{
			"enabled":  true,
			"replicas": int64(3),
		},
	})

	t.Log("Creating HA Valkey CR")
	tc.createValkey(t, ns, valkey)
	defer tc.deleteValkey(t, ns, name)

	tc.waitForStatefulSetReady(t, ns, name, 3)
	tc.waitForStatefulSetReady(t, ns, fmt.Sprintf("%s-sentinel", name), 3)
	tc.waitForValkeyPhase(t, ns, name, "OK")
	tc.waitForSentinelSlaves(t, ns, name, 2)

	// Two others each, on every sentinel, before anything is replaced.
	tc.waitForSentinelPeerCount(t, ns, name, 2)

	// The identity has to be pinned for the peers to take the address-switch path
	// at all, so read it now and prove it survives the replacement.
	victim := fmt.Sprintf("%s-sentinel-1", name)
	idBefore := tc.sentinelMyID(t, ns, victim)
	require.Len(t, idBefore, 40, "Sentinel ids are 40 characters; anything else fails to start")

	for generation := 1; generation <= 2; generation++ {
		t.Logf("Replacement generation %d: deleting %s", generation, victim)
		previous := tc.getPod(t, ns, victim)
		tc.deletePod(t, ns, victim)
		tc.waitForPodRecreated(t, ns, victim, previous.UID)
		tc.waitForPodReady(t, ns, victim)

		assert.Equal(t, idBefore, tc.sentinelMyID(t, ns, victim),
			"a replacement pod of the same ordinal must keep its Sentinel identity")

		// The survivors are what drifted before the fix: the replaced pod itself
		// always starts with an empty table and looks clean either way.
		tc.waitForSentinelPeerCount(t, ns, name, 2)
	}

	// The peer table is what a failover leader election counts, so prove the
	// cluster still fails over rather than only that a number looks right.
	master := tc.findMasterPod(t, ns, name, 3)
	t.Logf("Triggering a Sentinel failover away from %s", master)
	tc.valkeyExec(t, ns, fmt.Sprintf("%s-sentinel-0", name), 26379, "SENTINEL", "FAILOVER", name)

	require.Eventually(t, func() bool {
		current := tc.findMasterPod(t, ns, name, 3)
		t.Logf("Master is now %s (was %s)", current, master)
		return current != "" && current != master
	}, 3*time.Minute, 5*time.Second, "Sentinel must still be able to elect a leader and promote")

	tc.waitForValkeyPhase(t, ns, name, "OK")
}

// waitForSentinelPeerCount asserts that every sentinel pod reports exactly want
// other Sentinels. Exactly, not at least: the whole point is that the table does
// not grow, and "at least" would pass on the drift this test exists for.
func (tc *testClients) waitForSentinelPeerCount(t *testing.T, namespace, valkeyName string, want int) {
	t.Helper()

	var lastSeen map[string]int
	require.Eventually(t, func() bool {
		lastSeen = map[string]int{}
		for i := 0; i < 3; i++ {
			pod := fmt.Sprintf("%s-sentinel-%d", valkeyName, i)
			count, ok := tc.sentinelPeerCount(t, namespace, pod, valkeyName)
			if !ok {
				return false
			}
			lastSeen[pod] = count
		}
		for _, count := range lastSeen {
			if count != want {
				return false
			}
		}
		return true
	}, 2*time.Minute, 5*time.Second,
		"every sentinel must know exactly %d others, last seen %v", want, lastSeen)
	t.Logf("Sentinel peer tables: %v", lastSeen)
}

// sentinelPeerCount reads num-other-sentinels out of SENTINEL MASTER. The second
// return value is false when the reply could not be read at all, which is a retry
// rather than a failure while a pod is still coming up.
func (tc *testClients) sentinelPeerCount(t *testing.T, namespace, sentinelPod, valkeyName string) (int, bool) {
	t.Helper()

	raw := tc.valkeyExecQuick(t, namespace, sentinelPod, 26379, "SENTINEL", "MASTER", valkeyName)
	lines := strings.Split(raw, "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) != "num-other-sentinels" || i+1 >= len(lines) {
			continue
		}
		var count int
		if _, err := fmt.Sscanf(strings.TrimSpace(lines[i+1]), "%d", &count); err == nil {
			return count, true
		}
	}
	return 0, false
}

// sentinelMyID returns the Sentinel's own identity, which is what its peers key
// their table on.
func (tc *testClients) sentinelMyID(t *testing.T, namespace, sentinelPod string) string {
	t.Helper()

	var id string
	require.Eventually(t, func() bool {
		id = strings.TrimSpace(tc.valkeyExecQuick(t, namespace, sentinelPod, 26379, "SENTINEL", "MYID"))
		return len(id) == 40
	}, time.Minute, 3*time.Second, "SENTINEL MYID on %s returned %q", sentinelPod, id)
	return id
}
