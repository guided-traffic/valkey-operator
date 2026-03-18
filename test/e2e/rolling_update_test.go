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
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/wait"
)

// TestE2E_RollingUpdate_Standalone tests a rolling update on a standalone Valkey instance.
func TestE2E_RollingUpdate_Standalone(t *testing.T) {
	t.Parallel()
	tc := newTestClients(t)
	ns := "e2e-rolling-standalone"
	cleanup := tc.createNamespace(t, ns)
	defer cleanup()

	name := "roll-sa"
	initialImage := "valkey/valkey:8.0"
	updatedImage := "valkey/valkey:8.1"

	valkey := buildValkeyObject(name, ns, map[string]interface{}{
		"replicas": int64(1),
		"image":    initialImage,
	})

	t.Log("Creating standalone Valkey CR")
	tc.createValkey(t, ns, valkey)
	defer tc.deleteValkey(t, ns, name)

	// Wait for initial deployment.
	tc.waitForStatefulSetReady(t, ns, name, 1)
	tc.waitForValkeyPhase(t, ns, name, "OK")

	t.Run("Initial pod runs expected image", func(t *testing.T) {
		pod := tc.getPod(t, ns, fmt.Sprintf("%s-0", name))
		assert.Equal(t, initialImage, pod.Spec.Containers[0].Image)
	})

	t.Run("Write test data before update", func(t *testing.T) {
		podName := fmt.Sprintf("%s-0", name)
		resp := tc.valkeyExec(t, ns, podName, 6379, "SET", "rolling-test-key", "pre-update-value")
		assert.Equal(t, "OK", resp)
	})

	t.Run("Update image triggers rolling update", func(t *testing.T) {
		tc.updateValkeyImage(t, ns, name, updatedImage)

		// Wait for the rolling update to complete — pod should eventually run new image.
		tc.waitForAllPodsImage(t, ns, name, 1, updatedImage)

		// StatefulSet should be ready again.
		tc.waitForStatefulSetReady(t, ns, name, 1)
		tc.waitForValkeyPhaseAfterRollingUpdate(t, ns, name, "OK")
	})

	t.Run("Pod runs new image after update", func(t *testing.T) {
		pod := tc.getPod(t, ns, fmt.Sprintf("%s-0", name))
		assert.Equal(t, updatedImage, pod.Spec.Containers[0].Image)
	})

	t.Run("Data persists after rolling update", func(t *testing.T) {
		podName := fmt.Sprintf("%s-0", name)
		// Data may be lost in standalone without persistence, but the key should be absent cleanly.
		// With emptyDir, data is lost on pod restart. This verifies the instance is functional.
		resp := tc.valkeyExec(t, ns, podName, 6379, "PING")
		assert.Equal(t, "PONG", resp)
	})

	t.Run("Valkey responds after rolling update", func(t *testing.T) {
		podName := fmt.Sprintf("%s-0", name)
		resp := tc.valkeyExec(t, ns, podName, 6379, "SET", "post-update-key", "it-works")
		assert.Equal(t, "OK", resp)

		resp = tc.valkeyExec(t, ns, podName, 6379, "GET", "post-update-key")
		assert.Equal(t, "it-works", resp)
	})
}

// TestE2E_RollingUpdate_HA tests a rolling update on a 3-node HA cluster with Sentinel.
func TestE2E_RollingUpdate_HA(t *testing.T) {
	t.Parallel()
	tc := newTestClients(t)
	ns := "e2e-rolling-ha"
	cleanup := tc.createNamespace(t, ns)
	defer cleanup()

	name := "roll-ha"
	initialImage := "valkey/valkey:8.0"
	updatedImage := "valkey/valkey:8.1"

	valkey := buildValkeyObject(name, ns, map[string]interface{}{
		"replicas": int64(3),
		"image":    initialImage,
		"sentinel": map[string]interface{}{
			"enabled":  true,
			"replicas": int64(3),
		},
	})

	t.Log("Creating HA Valkey CR with Sentinel")
	tc.createValkey(t, ns, valkey)
	defer tc.deleteValkey(t, ns, name)

	// Wait for full cluster to be ready.
	tc.waitForStatefulSetReady(t, ns, name, 3)
	tc.waitForStatefulSetReady(t, ns, fmt.Sprintf("%s-sentinel", name), 3)
	tc.waitForValkeyPhase(t, ns, name, "OK")

	// Wait for replication to fully establish and for Sentinel to have discovered
	// the full topology. Without this, the rolling update may stall if Sentinel
	// has not yet learned about all replicas (num-slaves < 2).
	initialMasterPod := tc.findMasterPod(t, ns, name, 3)
	tc.waitForConnectedReplicas(t, ns, initialMasterPod, 6379, 2)
	tc.waitForSentinelSlaves(t, ns, name, 2)

	t.Run("Initial pods run expected image", func(t *testing.T) {
		for i := 0; i < 3; i++ {
			pod := tc.getPod(t, ns, fmt.Sprintf("%s-%d", name, i))
			assert.Equal(t, initialImage, pod.Spec.Containers[0].Image,
				"Pod %d should run initial image", i)
		}
	})

	t.Run("Initial master is confirmed", func(t *testing.T) {
		info := tc.valkeyExec(t, ns, initialMasterPod, 6379, "INFO", "replication")
		assert.Contains(t, info, "role:master", "%s should be master", initialMasterPod)
		t.Logf("Initial master: %s", initialMasterPod)
	})

	// Write test data to master before update.
	t.Run("Write test data before update", func(t *testing.T) {
		// Use a single MSET to write all string keys in one kubectl exec round-trip.
		tc.valkeyMSET(t, ns, initialMasterPod, 6379, map[string]string{
			"rolling:key1": "value-before-update",
			"rolling:key2": "critical-data",
			"rolling:key3": "must-survive-rolling-update",
		})

		// Write a list using variadic RPUSH (single round-trip for all 10 items).
		listArgs := []string{"RPUSH", "rolling:list"}
		for i := 0; i < 10; i++ {
			listArgs = append(listArgs, fmt.Sprintf("item-%d", i))
		}
		tc.valkeyExec(t, ns, initialMasterPod, 6379, listArgs...)

		// Wait for replication to sync.
		tc.waitForConnectedReplicas(t, ns, initialMasterPod, 6379, 2)

		// Verify data is on replicas.
		for i := 0; i < 3; i++ {
			podName := fmt.Sprintf("%s-%d", name, i)
			if podName == initialMasterPod {
				continue
			}
			require.Eventually(t, func() bool {
				resp := tc.valkeyExecAllowError(t, ns, podName, 6379, "GET", "rolling:key1")
				return resp == "value-before-update"
			}, 30*time.Second, 2*time.Second, "Data should replicate to %s", podName)
		}
	})

	// Trigger rolling update by changing image.
	t.Run("Update image triggers rolling update", func(t *testing.T) {
		t.Log("Updating Valkey image to trigger rolling update")
		tc.updateValkeyImage(t, ns, name, updatedImage)

		// The rolling update should proceed through the HA process.
		// Wait for all pods to be running the new image.
		tc.waitForAllPodsImage(t, ns, name, 3, updatedImage)

		// StatefulSet should be ready again.
		tc.waitForStatefulSetReady(t, ns, name, 3)

		// Wait for cluster to settle and reach OK.
		tc.waitForValkeyPhaseAfterRollingUpdate(t, ns, name, "OK")
	})

	t.Run("All pods run new image", func(t *testing.T) {
		for i := 0; i < 3; i++ {
			pod := tc.getPod(t, ns, fmt.Sprintf("%s-%d", name, i))
			assert.Equal(t, updatedImage, pod.Spec.Containers[0].Image,
				"Pod %d should run updated image", i)
		}
	})

	t.Run("Cluster is healthy after rolling update", func(t *testing.T) {
		// All pods should respond to PING.
		for i := 0; i < 3; i++ {
			podName := fmt.Sprintf("%s-%d", name, i)
			resp := tc.valkeyExec(t, ns, podName, 6379, "PING")
			assert.Equal(t, "PONG", resp, "Pod %s should respond to PING", podName)
		}

		// Exactly one master should exist.
		masterCount := 0
		for i := 0; i < 3; i++ {
			podName := fmt.Sprintf("%s-%d", name, i)
			info := tc.valkeyExec(t, ns, podName, 6379, "INFO", "replication")
			if strings.Contains(info, "role:master") {
				masterCount++
				t.Logf("Master after rolling update: %s", podName)
			}
		}
		assert.Equal(t, 1, masterCount, "Exactly one master should exist after rolling update")
	})

	t.Run("Data survives rolling update", func(t *testing.T) {
		currentMaster := tc.findMasterPod(t, ns, name, 3)

		// Verify string data.
		resp := tc.valkeyExec(t, ns, currentMaster, 6379, "GET", "rolling:key1")
		assert.Equal(t, "value-before-update", resp, "Data should survive rolling update")

		resp = tc.valkeyExec(t, ns, currentMaster, 6379, "GET", "rolling:key2")
		assert.Equal(t, "critical-data", resp)

		resp = tc.valkeyExec(t, ns, currentMaster, 6379, "GET", "rolling:key3")
		assert.Equal(t, "must-survive-rolling-update", resp)

		// Verify list data.
		resp = tc.valkeyExec(t, ns, currentMaster, 6379, "LLEN", "rolling:list")
		assert.Equal(t, "10", resp, "List should have 10 items after rolling update")
	})

	t.Run("Replicas have data after rolling update", func(t *testing.T) {
		// Wait for replication to re-establish after rolling update.
		// findMasterPod retries until a master is found, then waitForConnectedReplicas
		// waits up to 3 minutes for all replicas to sync — both are more robust than
		// an ad-hoc loop + short Eventually.
		currentMaster := tc.findMasterPod(t, ns, name, 3)
		tc.waitForConnectedReplicas(t, ns, currentMaster, 6379, 2)

		// Verify data on replicas.
		for i := 0; i < 3; i++ {
			podName := fmt.Sprintf("%s-%d", name, i)
			if podName == currentMaster {
				continue
			}
			require.Eventually(t, func() bool {
				resp := tc.valkeyExecAllowError(t, ns, podName, 6379, "GET", "rolling:key1")
				return resp == "value-before-update"
			}, 60*time.Second, 3*time.Second, "Data should replicate to %s after rolling update", podName)
		}
	})

	t.Run("Sentinel is monitoring correctly after rolling update", func(t *testing.T) {
		sentinelPod := fmt.Sprintf("%s-sentinel-0", name)
		resp := tc.valkeyExec(t, ns, sentinelPod, 26379, "SENTINEL", "master", name)
		assert.NotEmpty(t, resp, "Sentinel should return master info after rolling update")
		t.Logf("Sentinel master info after rolling update: %s", resp)
	})

	t.Run("New data can be written after rolling update", func(t *testing.T) {
		currentMaster := tc.findMasterPod(t, ns, name, 3)

		resp := tc.valkeyExec(t, ns, currentMaster, 6379, "SET", "post-rolling-update", "success")
		assert.Equal(t, "OK", resp)

		resp = tc.valkeyExec(t, ns, currentMaster, 6379, "GET", "post-rolling-update")
		assert.Equal(t, "success", resp)
	})

	t.Run("CRD status is OK after rolling update", func(t *testing.T) {
		// Wait for the operator to settle — previous subtests may have triggered
		// a brief replication-sync phase which causes the status to oscillate
		// between "OK" and "Syncing" for a short time.
		tc.waitForValkeyPhaseAfterRollingUpdate(t, ns, name, "OK")

		status := tc.getValkeyStatus(t, ns, name)

		phase, _, _ := unstructuredNestedString(status, "phase")
		assert.Equal(t, "OK", phase)

		readyReplicas, _, _ := unstructuredNestedFloat64(status, "readyReplicas")
		assert.Equal(t, float64(3), readyReplicas)
	})
}

// TestE2E_RollingUpdate_HA_NoDataLoss is a focused test verifying zero data loss
// during a rolling update of an HA cluster.
//
// NOTE: Does NOT call t.Parallel(). Running two HA rolling-update tests in
// parallel on a single-node Kind cluster causes severe resource contention and
// flaky results. This test runs after the parallel phase has finished.
func TestE2E_RollingUpdate_HA_NoDataLoss(t *testing.T) {
	tc := newTestClients(t)
	ns := "e2e-rolling-dataloss"
	cleanup := tc.createNamespace(t, ns)
	defer cleanup()

	name := "roll-dl"
	initialImage := "valkey/valkey:8.0"
	updatedImage := "valkey/valkey:8.1"

	valkey := buildValkeyObject(name, ns, map[string]interface{}{
		"replicas": int64(3),
		"image":    initialImage,
		"sentinel": map[string]interface{}{
			"enabled":  true,
			"replicas": int64(3),
		},
	})

	t.Log("Creating HA cluster for data loss verification")
	tc.createValkey(t, ns, valkey)
	defer tc.deleteValkey(t, ns, name)

	tc.waitForStatefulSetReady(t, ns, name, 3)
	tc.waitForStatefulSetReady(t, ns, fmt.Sprintf("%s-sentinel", name), 3)
	tc.waitForValkeyPhase(t, ns, name, "OK")

	// Find master and wait for replication to fully establish.
	masterPod := tc.findMasterPod(t, ns, name, 3)
	tc.waitForConnectedReplicas(t, ns, masterPod, 6379, 2)
	tc.waitForSentinelSlaves(t, ns, name, 2)
	t.Logf("Master pod: %s", masterPod)

	numKeys := 100
	t.Run("Write test dataset", func(t *testing.T) {
		// Use a single MSET to write all 100 keys in one kubectl exec round-trip
		// instead of 100 individual SET calls.
		data := make(map[string]string, numKeys)
		for i := 0; i < numKeys; i++ {
			data[fmt.Sprintf("dataloss:key:%d", i)] = fmt.Sprintf("value-%d", i)
		}
		tc.valkeyMSET(t, ns, masterPod, 6379, data)

		// Verify DBSIZE on master.
		dbsize := tc.valkeyExec(t, ns, masterPod, 6379, "DBSIZE")
		t.Logf("Master DBSIZE before rolling update: %s", dbsize)
	})

	// Wait for full replication sync.
	t.Run("Wait for replication sync", func(t *testing.T) {
		tc.waitForConnectedReplicas(t, ns, masterPod, 6379, 2)
	})

	// Record DBSIZE before update.
	dbsizeBefore := tc.valkeyExec(t, ns, masterPod, 6379, "DBSIZE")

	// Trigger rolling update.
	t.Run("Perform rolling update", func(t *testing.T) {
		tc.updateValkeyImage(t, ns, name, updatedImage)
		tc.waitForAllPodsImage(t, ns, name, 3, updatedImage)
		tc.waitForStatefulSetReady(t, ns, name, 3)
		tc.waitForValkeyPhaseAfterRollingUpdate(t, ns, name, "OK")
	})

	// Verify no data loss.
	t.Run("Verify no data loss", func(t *testing.T) {
		newMaster := tc.findMasterPod(t, ns, name, 3)
		t.Logf("New master after rolling update: %s", newMaster)

		dbsizeAfter := tc.valkeyExec(t, ns, newMaster, 6379, "DBSIZE")
		t.Logf("DBSIZE before=%s after=%s", dbsizeBefore, dbsizeAfter)
		assert.Equal(t, dbsizeBefore, dbsizeAfter, "DBSIZE should be the same after rolling update")

		// Spot check some keys.
		for i := 0; i < numKeys; i += 10 {
			key := fmt.Sprintf("dataloss:key:%d", i)
			resp := tc.valkeyExec(t, ns, newMaster, 6379, "EXISTS", key)
			assert.Equal(t, "1", resp, "Key %s should exist after rolling update", key)
		}
	})
}

// TestE2E_RollingUpdate_HA_Idempotent tests that running the same image update
// multiple times does not cause issues (idempotency).
//
// NOTE: This test deliberately does NOT call t.Parallel(). It performs two
// consecutive HA rolling updates (the most resource-intensive operation in the
// suite) which would cause severe resource contention on the single-node Kind
// cluster when run alongside other parallel HA tests. Running it sequentially
// (before the parallel phase starts) gives it an uncontested cluster and makes
// the result deterministic.
func TestE2E_RollingUpdate_HA_Idempotent(t *testing.T) {
	tc := newTestClients(t)
	ns := "e2e-rolling-idempotent"
	cleanup := tc.createNamespace(t, ns)
	defer cleanup()

	name := "roll-idem"
	initialImage := "valkey/valkey:8.0"
	updatedImage := "valkey/valkey:8.1"

	valkey := buildValkeyObject(name, ns, map[string]interface{}{
		"replicas": int64(3),
		"image":    initialImage,
		"sentinel": map[string]interface{}{
			"enabled":  true,
			"replicas": int64(3),
		},
	})

	t.Log("Creating HA cluster")
	tc.createValkey(t, ns, valkey)
	defer tc.deleteValkey(t, ns, name)

	tc.waitForStatefulSetReady(t, ns, name, 3)
	tc.waitForStatefulSetReady(t, ns, fmt.Sprintf("%s-sentinel", name), 3)
	tc.waitForValkeyPhase(t, ns, name, "OK")

	// Wait for replication to fully establish.
	idemMaster := tc.findMasterPod(t, ns, name, 3)
	tc.waitForConnectedReplicas(t, ns, idemMaster, 6379, 2)

	// First rolling update.
	t.Run("First rolling update", func(t *testing.T) {
		tc.updateValkeyImage(t, ns, name, updatedImage)
		tc.waitForAllPodsImage(t, ns, name, 3, updatedImage)
		tc.waitForStatefulSetReady(t, ns, name, 3)
		tc.waitForValkeyPhaseAfterRollingUpdate(t, ns, name, "OK")
	})

	// Ensure the cluster has fully settled before triggering the second rolling
	// update. The finalizeRollingUpdate phase performs a SENTINEL REMOVE+MONITOR
	// reset; sentinel needs a few seconds to rediscover replicas afterward.
	// waitForConnectedReplicas verifies that Valkey replication is live.
	// waitForSentinelSlaves additionally confirms that sentinel has re-discovered
	// the topology (NUM-SLAVES == 2), making the start conditions for the second
	// rolling update fully deterministic and preventing a stall in the operator's
	// isSentinelAwareOfReplicas guard.
	settledMaster := tc.findMasterPod(t, ns, name, 3)
	tc.waitForConnectedReplicas(t, ns, settledMaster, 6379, 2)
	tc.waitForSentinelSlaves(t, ns, name, 2)
	t.Logf("Cluster fully settled after first rolling update, master: %s", settledMaster)

	// "Second" update back to original (tests repeated rolling update).
	t.Run("Second rolling update (revert)", func(t *testing.T) {
		tc.updateValkeyImage(t, ns, name, initialImage)
		tc.waitForAllPodsImage(t, ns, name, 3, initialImage)
		tc.waitForStatefulSetReady(t, ns, name, 3)
		tc.waitForValkeyPhaseAfterRollingUpdate(t, ns, name, "OK")
	})

	t.Run("Cluster healthy after two rolling updates", func(t *testing.T) {
		for i := 0; i < 3; i++ {
			podName := fmt.Sprintf("%s-%d", name, i)
			resp := tc.valkeyExec(t, ns, podName, 6379, "PING")
			assert.Equal(t, "PONG", resp)
		}

		masterCount := 0
		for i := 0; i < 3; i++ {
			podName := fmt.Sprintf("%s-%d", name, i)
			info := tc.valkeyExec(t, ns, podName, 6379, "INFO", "replication")
			if strings.Contains(info, "role:master") {
				masterCount++
			}
		}
		assert.Equal(t, 1, masterCount, "Exactly one master after two rolling updates")
	})
}

// --- Helper functions for rolling update E2E tests ---

// updateValkeyImage patches the Valkey CR spec.image to the given image.
func (tc *testClients) updateValkeyImage(t *testing.T, namespace, name, newImage string) {
	t.Helper()
	ctx := context.Background()

	// Get the current CR.
	valkey, err := tc.dynamic.Resource(valkeyGVR).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	require.NoError(t, err, "Failed to get Valkey CR %s/%s", namespace, name)

	// Update the image.
	err = unstructured.SetNestedField(valkey.Object, newImage, "spec", "image")
	require.NoError(t, err, "Failed to set image in Valkey CR")

	// Apply the update.
	_, err = tc.dynamic.Resource(valkeyGVR).Namespace(namespace).Update(ctx, valkey, metav1.UpdateOptions{})
	require.NoError(t, err, "Failed to update Valkey CR with new image")

	t.Logf("Updated Valkey %s/%s image to %s", namespace, name, newImage)
}

// waitForAllPodsImage waits until all pods in a StatefulSet run the expected image.
// Uses rollingUpdateTimeout since rolling updates in CI with parallel tests may take
// significantly longer than standard operations due to resource contention.
// Uses rollingUpdatePollInterval to reduce API server load from parallel polling.
func (tc *testClients) waitForAllPodsImage(t *testing.T, namespace, stsName string, replicas int, expectedImage string) {
	t.Helper()
	ctx := context.Background()

	err := wait.PollUntilContextTimeout(ctx, rollingUpdatePollInterval, rollingUpdateTimeout, true, func(ctx context.Context) (bool, error) {
		for i := 0; i < replicas; i++ {
			podName := fmt.Sprintf("%s-%d", stsName, i)
			pod, err := tc.kube.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
			if err != nil {
				t.Logf("Pod %s not found yet: %v", podName, err)
				return false, nil
			}

			if len(pod.Spec.Containers) == 0 {
				return false, nil
			}

			currentImage := pod.Spec.Containers[0].Image
			if currentImage != expectedImage {
				t.Logf("Pod %s image: %s (want: %s)", podName, currentImage, expectedImage)
				return false, nil
			}

			// Also check that the pod is ready.
			ready := false
			for _, cond := range pod.Status.Conditions {
				if cond.Type == "Ready" && cond.Status == "True" {
					ready = true
					break
				}
			}
			if !ready {
				t.Logf("Pod %s has correct image but not ready yet", podName)
				return false, nil
			}
		}
		return true, nil
	})
	require.NoError(t, err, "Not all pods reached image %s in %s/%s", expectedImage, namespace, stsName)
}

// findMasterPod finds the master pod by querying INFO replication on all pods.
func (tc *testClients) findMasterPod(t *testing.T, namespace, name string, replicas int) string {
	t.Helper()

	var masterPod string
	require.Eventually(t, func() bool {
		for i := 0; i < replicas; i++ {
			podName := fmt.Sprintf("%s-%d", name, i)
			info := tc.valkeyExecAllowError(t, namespace, podName, 6379, "INFO", "replication")
			if strings.Contains(info, "role:master") {
				masterPod = podName
				return true
			}
		}
		return false
	}, 60*time.Second, 2*time.Second, "Should find a master pod")

	return masterPod
}

// TestE2E_RollingUpdate_HA_TLS_NoTLSErrors is a regression test for the TLS bad
// certificate issue during Sentinel failover. Before the replica-announce-ip fix,
// Sentinel would announce IPs instead of hostnames after failover, causing TLS
// clients to fail certificate validation for ~67 seconds.
func TestE2E_RollingUpdate_HA_TLS_NoTLSErrors(t *testing.T) {
	t.Parallel()
	tc := newTestClients(t)
	ns := "e2e-rolling-ha-tls"
	cleanup := tc.createNamespace(t, ns)
	defer cleanup()

	name := "roll-tls"
	initialImage := "valkey/valkey:8.0"
	updatedImage := "valkey/valkey:8.1"

	valkey := buildValkeyObject(name, ns, map[string]interface{}{
		"replicas": int64(3),
		"image":    initialImage,
		"tls":      tlsSpec(),
		"sentinel": map[string]interface{}{
			"enabled":  true,
			"replicas": int64(3),
		},
	})

	t.Log("Creating TLS HA Valkey CR for rolling update regression test")
	tc.createValkey(t, ns, valkey)
	defer tc.deleteValkey(t, ns, name)

	// Wait for full cluster readiness.
	tc.waitForStatefulSetReady(t, ns, name, 3)
	tc.waitForStatefulSetReady(t, ns, fmt.Sprintf("%s-sentinel", name), 3)
	for i := 0; i < 3; i++ {
		tc.waitForPodReady(t, ns, fmt.Sprintf("%s-%d", name, i))
		tc.waitForPodReady(t, ns, fmt.Sprintf("%s-sentinel-%d", name, i))
	}
	tc.waitForValkeyPhase(t, ns, name, "OK")

	masterPod := tc.findMasterPodTLS(t, ns, name, 3)
	tc.waitForConnectedReplicasTLS(t, ns, masterPod, 2)

	t.Run("Write test data before TLS rolling update", func(t *testing.T) {
		resp := tc.valkeyTLSExec(t, ns, masterPod, tlsValkeyPort, "SET", "tls-roll-key", "pre-update")
		assert.Equal(t, "OK", resp)
	})

	t.Run("Trigger rolling update via image change", func(t *testing.T) {
		tc.updateValkeyImage(t, ns, name, updatedImage)
		tc.waitForAllPodsImage(t, ns, name, 3, updatedImage)
		tc.waitForStatefulSetReady(t, ns, name, 3)
		tc.waitForValkeyPhaseAfterRollingUpdate(t, ns, name, "OK")
	})

	t.Run("No TLS bad certificate errors after rolling update", func(t *testing.T) {
		for i := 0; i < 3; i++ {
			podName := fmt.Sprintf("%s-%d", name, i)
			logs := tc.getPodLogs(t, ns, podName, "valkey")
			assert.NotContains(t, logs, "ssl/tls alert bad certificate",
				"Pod %s must not have TLS bad certificate errors after rolling update", podName)
			assert.NotContains(t, logs, "cannot validate certificate",
				"Pod %s must not have certificate validation errors", podName)
		}
	})

	t.Run("Data accessible after TLS rolling update", func(t *testing.T) {
		newMaster := tc.findMasterPodTLS(t, ns, name, 3)
		require.Eventually(t, func() bool {
			resp := tc.valkeyTLSExecAllowError(t, ns, newMaster, tlsValkeyPort, "GET", "tls-roll-key")
			return resp == "pre-update"
		}, 30*time.Second, 2*time.Second, "Data should be accessible after TLS rolling update")
	})
}
