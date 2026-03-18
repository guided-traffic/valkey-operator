//go:build e2e

package e2e

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// TestE2E_SentinelStaleMaster
// Regression test for: Sentinel keeps stale master after simultaneous pod restart.
//
// Scenario:
//  1. Create HA cluster with Sentinel, wait for full topology.
//  2. Trigger Sentinel failover so master moves away from pod-0.
//  3. Delete ALL Valkey AND Sentinel pods simultaneously (simulates namespace restart).
//  4. Wait for all pods to come back up.
//  5. Verify Sentinel correctly discovers the actual master (not the stale one).
//  6. Verify writes succeed through the Sentinel-reported master.
//
// ---------------------------------------------------------------------------
func TestE2E_SentinelStaleMaster(t *testing.T) {
	t.Parallel()
	tc := newTestClients(t)
	ns := "e2e-sentinel-stale"
	cleanup := tc.createNamespace(t, ns)
	defer cleanup()

	name := "stale-m"
	valkey := buildValkeyObject(name, ns, map[string]interface{}{
		"replicas": int64(3),
		"image":    "valkey/valkey:8.0",
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

	// Wait for full replication and Sentinel topology discovery.
	initialMaster := tc.findMasterPod(t, ns, name, 3)
	tc.waitForConnectedReplicas(t, ns, initialMaster, 6379, 2)
	tc.waitForSentinelSlaves(t, ns, name, 2)
	t.Logf("Initial master: %s", initialMaster)

	// Write test data.
	for i := 0; i < 20; i++ {
		resp := tc.valkeyExec(t, ns, initialMaster, 6379,
			"SET", fmt.Sprintf("stale:key:%d", i), fmt.Sprintf("value-%d", i))
		require.Equal(t, "OK", resp)
	}
	t.Log("Wrote 20 test keys")

	// Wait for replication sync.
	tc.waitForConnectedReplicas(t, ns, initialMaster, 6379, 2)
	dbsizeBefore := tc.valkeyExec(t, ns, initialMaster, 6379, "DBSIZE")
	t.Logf("DBSIZE before restart: %s", dbsizeBefore)

	// If initial master is already pod-0, trigger failover so master moves to pod-1 or pod-2.
	// This creates the conditions for the bug: after simultaneous restart, pod-0 will
	// become master again (ordinal fallback), but sentinel.conf still points to the old master.
	if initialMaster == fmt.Sprintf("%s-0", name) {
		t.Log("Master is pod-0, triggering Sentinel failover to move master away")
		sentinelPod := fmt.Sprintf("%s-sentinel-0", name)
		tc.valkeyExec(t, ns, sentinelPod, 26379, "SENTINEL", "FAILOVER", name)

		// Wait for failover to fully settle: exactly one master that is not pod-0,
		// and that master must have 2 connected replicas (replication re-established).
		var postFailoverMaster string
		require.Eventually(t, func() bool {
			masters := []string{}
			for i := 0; i < 3; i++ {
				podName := fmt.Sprintf("%s-%d", name, i)
				info := tc.valkeyExecAllowError(t, ns, podName, 6379, "INFO", "replication")
				if strings.Contains(info, "role:master") {
					masters = append(masters, podName)
				}
			}
			if len(masters) != 1 {
				t.Logf("Failover settling: %d masters found %v", len(masters), masters)
				return false
			}
			if masters[0] == fmt.Sprintf("%s-0", name) {
				t.Logf("Master still pod-0, waiting for failover")
				return false
			}
			// Also verify replication settled (2 connected slaves).
			info := tc.valkeyExecAllowError(t, ns, masters[0], 6379, "INFO", "replication")
			if !strings.Contains(info, "connected_slaves:2") {
				t.Logf("Master %s does not yet have 2 connected slaves", masters[0])
				return false
			}
			postFailoverMaster = masters[0]
			return true
		}, 3*time.Minute, 3*time.Second,
			"Failover should move master away from pod-0 with 2 replicas")

		tc.waitForSentinelSlaves(t, ns, name, 2)
		t.Logf("Post-failover master: %s", postFailoverMaster)
	}

	// Record pre-restart master (should NOT be pod-0).
	preRestartMaster := tc.findMasterPod(t, ns, name, 3)
	assert.NotEqual(t, fmt.Sprintf("%s-0", name), preRestartMaster,
		"Master should NOT be pod-0 before simultaneous restart")
	t.Logf("Pre-restart master: %s (not pod-0 ✓)", preRestartMaster)

	// --- Simultaneous restart: delete ALL Valkey + Sentinel pods ---
	t.Log("Deleting ALL Valkey and Sentinel pods simultaneously")
	for i := 0; i < 3; i++ {
		tc.deletePod(t, ns, fmt.Sprintf("%s-%d", name, i))
		tc.deletePod(t, ns, fmt.Sprintf("%s-sentinel-%d", name, i))
	}

	// Wait for all pods to come back.
	tc.waitForStatefulSetReady(t, ns, name, 3)
	tc.waitForStatefulSetReady(t, ns, fmt.Sprintf("%s-sentinel", name), 3)
	for i := 0; i < 3; i++ {
		tc.waitForPodReady(t, ns, fmt.Sprintf("%s-%d", name, i))
		tc.waitForPodReady(t, ns, fmt.Sprintf("%s-sentinel-%d", name, i))
	}
	t.Log("All pods restarted and ready")

	// Verify Sentinel reports a valid master (not s_down/o_down).
	t.Run("sentinel reports healthy master", func(t *testing.T) {
		sentinelPod := fmt.Sprintf("%s-sentinel-0", name)
		require.Eventually(t, func() bool {
			raw := tc.valkeyExecAllowError(t, ns, sentinelPod, 26379,
				"SENTINEL", "MASTER", name)
			// Check that flags do NOT contain s_down or o_down.
			lines := strings.Split(raw, "\n")
			for i, line := range lines {
				if strings.TrimSpace(line) == "flags" && i+1 < len(lines) {
					flags := strings.TrimSpace(lines[i+1])
					t.Logf("Sentinel master flags: %s", flags)
					return flags == "master"
				}
			}
			return false
		}, 2*time.Minute, 5*time.Second,
			"Sentinel should report master with flags=master (no s_down/o_down)")
	})

	// Verify Sentinel knows about 2 slaves.
	t.Run("sentinel discovers all replicas", func(t *testing.T) {
		tc.waitForSentinelSlaves(t, ns, name, 2)
	})

	// Verify the Sentinel-reported master is actually a master in Valkey.
	t.Run("sentinel master matches actual valkey master", func(t *testing.T) {
		sentinelPod := fmt.Sprintf("%s-sentinel-0", name)
		raw := tc.valkeyExecAllowError(t, ns, sentinelPod, 26379,
			"SENTINEL", "get-master-addr-by-name", name)
		sentinelMasterAddr := strings.TrimSpace(strings.Split(raw, "\n")[0])
		t.Logf("Sentinel reports master at: %s", sentinelMasterAddr)

		// Find the actual Valkey master.
		actualMaster := tc.findMasterPod(t, ns, name, 3)
		t.Logf("Actual Valkey master: %s", actualMaster)

		// The Sentinel-reported address should contain the actual master's pod name.
		assert.Contains(t, sentinelMasterAddr, actualMaster,
			"Sentinel-reported master should match actual Valkey master")
	})

	// Verify writes succeed through the actual master.
	t.Run("writes succeed after restart", func(t *testing.T) {
		master := tc.findMasterPod(t, ns, name, 3)
		tc.waitForConnectedReplicas(t, ns, master, 6379, 2)

		resp := tc.valkeyExec(t, ns, master, 6379, "SET", "post-restart-key", "works")
		assert.Equal(t, "OK", resp)
		resp = tc.valkeyExec(t, ns, master, 6379, "GET", "post-restart-key")
		assert.Equal(t, "works", resp)
	})

	// Verify no READONLY errors when writing to the Sentinel-reported master.
	t.Run("no READONLY errors via sentinel master", func(t *testing.T) {
		sentinelPod := fmt.Sprintf("%s-sentinel-0", name)
		raw := tc.valkeyExecAllowError(t, ns, sentinelPod, 26379,
			"SENTINEL", "get-master-addr-by-name", name)
		sentinelMasterAddr := strings.TrimSpace(strings.Split(raw, "\n")[0])

		// Find the pod that matches the sentinel-reported master address.
		for i := 0; i < 3; i++ {
			podName := fmt.Sprintf("%s-%d", name, i)
			podFQDN := fmt.Sprintf("%s.%s-headless.%s.svc.cluster.local", podName, name, ns)
			if strings.Contains(sentinelMasterAddr, podName) || sentinelMasterAddr == podFQDN {
				resp := tc.valkeyExecAllowError(t, ns, podName, 6379, "SET", "readonly-check", "ok")
				assert.Equal(t, "OK", resp,
					"Writing to Sentinel-reported master %s should not return READONLY", podName)
				break
			}
		}
	})
}
