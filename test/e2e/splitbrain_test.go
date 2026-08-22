//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
)

// annotationDrainPromotedAtKey is the stamp the sidecar drain handler writes on the
// pod it promoted (internal/common/annotations.go, written by stampPromotion in
// internal/sidecar/drain.go).
const annotationDrainPromotedAtKey = "vko.gtrfc.com/drain-promoted-at"

// drainPromotionTimeout bounds the wait for the drain to fail over. It is a
// generous multiple of what the drain needs -- a promotion is one REPLICAOF NO ONE
// against a pod that is already up -- and still well inside the time the deleted
// pod needs to be rescheduled, which is what makes the check meaningful.
const drainPromotionTimeout = time.Minute

// TestE2E_NoSentinel_MasterKill_NoSplitBrain is a regression test for the
// split-brain scenario observed after a chaos-monkey run: when a non-sentinel
// multi-replica cluster loses the master pod, the sidecar drain handler promotes
// a replica via REPLICAOF NO ONE. On restart the init container must discover the
// already-promoted master and configure itself as replica — NOT blindly assume
// pod-0 is master based on ordinal. Before the master-discovery init container
// fix, pod-0 would always restart as a second master, creating a split-brain.
func TestE2E_NoSentinel_MasterKill_NoSplitBrain(t *testing.T) {
	t.Parallel()
	tc := newTestClients(t)
	ns := "e2e-splitbrain"
	cleanup := tc.createNamespace(t, ns)
	defer cleanup()

	name := "sb-norec"
	const replicas = 3

	valkey := buildValkeyObject(name, ns, map[string]interface{}{
		"replicas": int64(replicas),
		"image":    "valkey/valkey:8.0",
	})

	t.Log("Creating 3-replica Valkey CR without Sentinel")
	tc.createValkey(t, ns, valkey)
	defer tc.deleteValkey(t, ns, name)

	tc.waitForStatefulSetReady(t, ns, name, replicas)
	tc.waitForValkeyPhase(t, ns, name, "OK")

	// Establish baseline: find master and wait for replication.
	initialMaster := tc.findMasterPod(t, ns, name, replicas)
	tc.waitForConnectedReplicas(t, ns, initialMaster, 6379, replicas-1)
	t.Logf("Initial master: %s", initialMaster)

	// Write test data so we can verify the surviving master retains it.
	t.Run("write test data", func(t *testing.T) {
		tc.valkeyMSET(t, ns, initialMaster, 6379, map[string]string{
			"sb:key:0": "split-brain-check-0",
			"sb:key:1": "split-brain-check-1",
			"sb:key:2": "split-brain-check-2",
		})

		// Verify replication to at least one replica.
		for i := 0; i < replicas; i++ {
			podName := fmt.Sprintf("%s-%d", name, i)
			if podName == initialMaster {
				continue
			}
			require.Eventually(t, func() bool {
				resp := tc.valkeyExecQuick(t, ns, podName, 6379, "GET", "sb:key:0")
				return resp == "split-brain-check-0"
			}, 30*time.Second, 2*time.Second, "Data should replicate to %s", podName)
			break
		}
	})

	// Kill the master pod. The StatefulSet controller will recreate it.
	// The sidecar drain handler on the dying pod promotes a replica.
	t.Run("delete master pod", func(t *testing.T) {
		killedUID := tc.getPod(t, ns, initialMaster).UID
		t.Logf("Deleting master pod %s", initialMaster)
		tc.deletePod(t, ns, initialMaster)

		// Before anything else: the drain either failed over or it did not, and
		// every later assertion in this test is unreadable if it did not.
		tc.requireDrainPromotedAReplica(t, ns, name, replicas, initialMaster)

		// Wait for the pod to be recreated and the cluster to become ready. The
		// StatefulSet counts the terminating pod as ready, so the replacement is
		// waited for by UID first -- see waitForPodRecreated.
		tc.waitForPodRecreated(t, ns, initialMaster, killedUID)
		tc.waitForStatefulSetReady(t, ns, name, replicas)
		tc.waitForValkeyPhase(t, ns, name, "OK")
	})

	// Core assertion: after restart there must be exactly ONE master in the cluster.
	// The old init container would make pod-0 master unconditionally, resulting in
	// two masters (split-brain). The master-discovery init container queries peers
	// first and configures itself as replica when it discovers an existing master.
	t.Run("exactly one master after restart", func(t *testing.T) {
		require.Eventually(t, func() bool {
			masterCount := 0
			for i := 0; i < replicas; i++ {
				podName := fmt.Sprintf("%s-%d", name, i)
				info := tc.valkeyExecQuick(t, ns, podName, 6379, "INFO", "replication")
				if strings.Contains(info, "role:master") {
					masterCount++
				}
			}
			t.Logf("Master count: %d (want 1)", masterCount)
			return masterCount == 1
		}, 90*time.Second, 3*time.Second, "Exactly one master should exist after pod restart")
	})

	// Verify the restarted pod joined as replica, not master.
	t.Run("restarted pod is replica", func(t *testing.T) {
		require.Eventually(t, func() bool {
			info := tc.valkeyExecQuick(t, ns, initialMaster, 6379, "INFO", "replication")
			return strings.Contains(info, "role:slave")
		}, 60*time.Second, 2*time.Second,
			"Restarted pod %s should be a replica, not master", initialMaster)
	})

	// Verify replication is fully established with the new topology.
	t.Run("replication re-established", func(t *testing.T) {
		newMaster := tc.findMasterPod(t, ns, name, replicas)
		t.Logf("New master after restart: %s", newMaster)
		assert.NotEqual(t, initialMaster, newMaster,
			"New master should be a different pod than the killed one")
		tc.waitForConnectedReplicas(t, ns, newMaster, 6379, replicas-1)
	})

	// Verify data survived the failover — the promoted master should have all keys.
	t.Run("data survives failover", func(t *testing.T) {
		newMaster := tc.findMasterPod(t, ns, name, replicas)
		for i := 0; i < 3; i++ {
			key := fmt.Sprintf("sb:key:%d", i)
			expected := fmt.Sprintf("split-brain-check-%d", i)
			resp := tc.valkeyExec(t, ns, newMaster, 6379, "GET", key)
			assert.Equal(t, expected, resp, "Key %s should survive failover", key)
		}
	})

	// Write new data to confirm the cluster is fully writable after recovery.
	t.Run("cluster is writable after recovery", func(t *testing.T) {
		newMaster := tc.findMasterPod(t, ns, name, replicas)
		resp := tc.valkeyExec(t, ns, newMaster, 6379, "SET", "sb:post-recovery", "works")
		assert.Equal(t, "OK", resp)

		// Verify replication of new data to the restarted pod.
		require.Eventually(t, func() bool {
			resp := tc.valkeyExecQuick(t, ns, initialMaster, 6379, "GET", "sb:post-recovery")
			return resp == "works"
		}, 30*time.Second, 2*time.Second,
			"New data should replicate to restarted pod %s", initialMaster)
	})
}

// requireDrainPromotedAReplica proves that the sidecar drain handler on the dying
// master actually failed over, while the killed pod is still away and cannot
// confuse the picture.
//
// The assertion is on the promotion, not on the stamp. The stamp is the drain's own
// signature -- nothing else writes it -- and it is polled for and reported here, but
// it is transient: the operator clears every stamp of the cluster the moment it
// records the promoted master (clearDrainStamps, from recordPromotedMaster), so a
// loaded run can adopt and clear between two polls. Gating on it would trade this
// test's failure mode for a flake.
//
// What cannot flake is the promotion itself. A drain that runs promotes a replica
// within seconds, long before the deleted pod is rescheduled. A drain that does not
// run leaves the cluster with no master at all until the killed pod returns and
// self-claims the role from the replica ConfigMap -- with an empty dataset when
// persistence is off, which the replicas then full-resync away. That is what CI
// observed in run 32577672028's predecessor, and this reports it as one named
// failure instead of three downstream ones about roles and missing keys.
func (tc *testClients) requireDrainPromotedAReplica(t *testing.T, namespace, name string,
	replicas int, killedPod string) {
	t.Helper()

	promoted, stamped := "", ""
	err := wait.PollUntilContextTimeout(context.Background(), time.Second, drainPromotionTimeout, true,
		func(ctx context.Context) (bool, error) {
			for i := 0; i < replicas; i++ {
				podName := fmt.Sprintf("%s-%d", name, i)
				if podName == killedPod {
					continue
				}
				if stamped == "" {
					pod, err := tc.kube.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
					if err == nil && pod.Annotations[annotationDrainPromotedAtKey] != "" {
						stamped = podName
					}
				}
				info := tc.valkeyExecQuick(t, namespace, podName, 6379, "INFO", "replication")
				if strings.Contains(info, "role:master") {
					promoted = podName
					return true, nil
				}
			}
			return false, nil
		})
	require.NoError(t, err,
		"no surviving pod became master within %v of deleting %s: the sidecar drain handler did not "+
			"promote a replica, so the returning pod self-claims the role from the replica ConfigMap",
		drainPromotionTimeout, killedPod)

	if stamped == "" {
		t.Logf("Drain promoted %s; the %s stamp was already cleared when polled",
			promoted, annotationDrainPromotedAtKey)
		return
	}
	assert.Equal(t, promoted, stamped,
		"the drain stamp must sit on the pod that holds the master role, otherwise the operator "+
			"adopts the wrong one on the next multi-master pass")
	t.Logf("Drain promoted %s and stamped it with %s", promoted, annotationDrainPromotedAtKey)
}
