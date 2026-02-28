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
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ---------------------------------------------------------------------------
// Helper methods for sidecar-specific E2E tests
// ---------------------------------------------------------------------------

// waitForPodLabel waits until a pod has the expected value for a given label key.
func (tc *testClients) waitForPodLabel(t *testing.T, namespace, podName, labelKey, expectedValue string) {
	t.Helper()
	require.Eventually(t, func() bool {
		pod, err := tc.kube.CoreV1().Pods(namespace).Get(
			context.Background(), podName, metav1.GetOptions{})
		if err != nil {
			return false
		}
		actual := pod.Labels[labelKey]
		if actual != expectedValue {
			t.Logf("Pod %s label %s=%s (want %s)", podName, labelKey, actual, expectedValue)
		}
		return actual == expectedValue
	}, testTimeout, pollInterval,
		"Pod %s/%s did not get label %s=%s", namespace, podName, labelKey, expectedValue)
}

// getEndpointPodNames returns the names of pods backing a service's endpoints.
func (tc *testClients) getEndpointPodNames(t *testing.T, namespace, serviceName string) []string {
	t.Helper()
	ep, err := tc.kube.CoreV1().Endpoints(namespace).Get(
		context.Background(), serviceName, metav1.GetOptions{})
	if err != nil {
		t.Logf("Failed to get endpoints for %s: %v", serviceName, err)
		return nil
	}
	var podNames []string
	for _, subset := range ep.Subsets {
		for _, addr := range subset.Addresses {
			if addr.TargetRef != nil && addr.TargetRef.Kind == "Pod" {
				podNames = append(podNames, addr.TargetRef.Name)
			}
		}
	}
	return podNames
}

// waitForEndpointPodCount waits until a service has the expected number of ready
// endpoint addresses.
func (tc *testClients) waitForEndpointPodCount(t *testing.T, namespace, serviceName string, expected int) {
	t.Helper()
	require.Eventually(t, func() bool {
		ep, err := tc.kube.CoreV1().Endpoints(namespace).Get(
			context.Background(), serviceName, metav1.GetOptions{})
		if err != nil {
			return false
		}
		count := 0
		for _, subset := range ep.Subsets {
			count += len(subset.Addresses)
		}
		t.Logf("Service %s endpoints: %d (want %d)", serviceName, count, expected)
		return count == expected
	}, testTimeout, pollInterval,
		"Service %s/%s did not reach %d endpoints", namespace, serviceName, expected)
}

// deletePod deletes a pod by name.
func (tc *testClients) deletePod(t *testing.T, namespace, name string) {
	t.Helper()
	err := tc.kube.CoreV1().Pods(namespace).Delete(
		context.Background(), name, metav1.DeleteOptions{})
	require.NoError(t, err, "Failed to delete pod %s/%s", namespace, name)
}

// ensureServiceCreated creates a Kubernetes Service resource, deleting any
// existing service with the same name first to avoid AlreadyExists errors.
func (tc *testClients) ensureServiceCreated(t *testing.T, namespace string, svc *corev1.Service) {
	t.Helper()
	_ = tc.kube.CoreV1().Services(namespace).Delete(
		context.Background(), svc.Name, metav1.DeleteOptions{})
	// Brief wait so the API server processes the delete.
	time.Sleep(500 * time.Millisecond)
	_, err := tc.kube.CoreV1().Services(namespace).Create(
		context.Background(), svc, metav1.CreateOptions{})
	require.NoError(t, err, "Failed to create service %s/%s", namespace, svc.Name)
}

// waitForServiceAbsent waits until a service no longer exists.
func (tc *testClients) waitForServiceAbsent(t *testing.T, namespace, name string) {
	t.Helper()
	require.Eventually(t, func() bool {
		_, err := tc.kube.CoreV1().Services(namespace).Get(
			context.Background(), name, metav1.GetOptions{})
		return apierrors.IsNotFound(err)
	}, testTimeout, pollInterval,
		"Service %s/%s should be deleted", namespace, name)
}

// ---------------------------------------------------------------------------
// TestE2E_SidecarRoleLabelingAndRouting
// Phase 4.1 / 4.3: Verify sidecar labels pods and that services route correctly.
// ---------------------------------------------------------------------------

// TestE2E_SidecarRoleLabelingAndRouting deploys an HA cluster with sentinel,
// verifies the sidecar labels pods as master/replica, and checks that the
// -rw, -r, and -all service endpoints match the expected pod roles.
func TestE2E_SidecarRoleLabelingAndRouting(t *testing.T) {
	t.Parallel()
	tc := newTestClients(t)
	ns := "e2e-sc-routing"
	cleanup := tc.createNamespace(t, ns)
	defer cleanup()

	name := "sc-route"
	valkey := buildValkeyObject(name, ns, map[string]interface{}{
		"replicas": int64(3),
		"image":    "valkey/valkey:8.0",
		"sentinel": map[string]interface{}{
			"enabled":  true,
			"replicas": int64(3),
		},
	})

	t.Log("Creating HA Valkey CR for sidecar routing test")
	tc.createValkey(t, ns, valkey)
	defer tc.deleteValkey(t, ns, name)

	tc.waitForStatefulSetReady(t, ns, name, 3)
	tc.waitForStatefulSetReady(t, ns, fmt.Sprintf("%s-sentinel", name), 3)
	tc.waitForValkeyPhase(t, ns, name, "OK")

	// Wait for replication to establish.
	masterPod := tc.findMasterPod(t, ns, name, 3)
	tc.waitForConnectedReplicas(t, ns, masterPod, 6379, 2)

	t.Run("pods are labeled with correct roles by sidecar", func(t *testing.T) {
		masterCount := 0
		replicaCount := 0
		for i := 0; i < 3; i++ {
			podName := fmt.Sprintf("%s-%d", name, i)
			pod := tc.getPod(t, ns, podName)
			role := pod.Labels["vko.gtrfc.com/instanceRole"]
			t.Logf("Pod %s role label: %s", podName, role)
			switch role {
			case "master":
				masterCount++
			case "replica":
				replicaCount++
			default:
				t.Errorf("Pod %s has unexpected role label: %q", podName, role)
			}
		}
		assert.Equal(t, 1, masterCount, "Exactly one pod should be labeled as master")
		assert.Equal(t, 2, replicaCount, "Exactly two pods should be labeled as replica")
	})

	t.Run("role labels match INFO replication output", func(t *testing.T) {
		for i := 0; i < 3; i++ {
			podName := fmt.Sprintf("%s-%d", name, i)
			pod := tc.getPod(t, ns, podName)
			labelRole := pod.Labels["vko.gtrfc.com/instanceRole"]
			info := tc.valkeyExec(t, ns, podName, 6379, "INFO", "replication")
			if labelRole == "master" {
				assert.Contains(t, info, "role:master",
					"Pod %s labeled master should report role:master", podName)
			} else {
				assert.Contains(t, info, "role:slave",
					"Pod %s labeled replica should report role:slave", podName)
			}
		}
	})

	t.Run("rw service endpoint is the master pod only", func(t *testing.T) {
		tc.waitForEndpointPodCount(t, ns, fmt.Sprintf("%s-rw", name), 1)
		pods := tc.getEndpointPodNames(t, ns, fmt.Sprintf("%s-rw", name))
		require.Len(t, pods, 1, "-rw should have exactly 1 endpoint")
		assert.Equal(t, masterPod, pods[0],
			"-rw endpoint should be the master pod")
	})

	t.Run("readonly service endpoints are replica pods only", func(t *testing.T) {
		tc.waitForEndpointPodCount(t, ns, fmt.Sprintf("%s-r", name), 2)
		pods := tc.getEndpointPodNames(t, ns, fmt.Sprintf("%s-r", name))
		assert.Len(t, pods, 2, "-r should have exactly 2 endpoints")
		for _, p := range pods {
			assert.NotEqual(t, masterPod, p,
				"-r endpoint should not include master pod %s", masterPod)
		}
	})

	t.Run("all service endpoints include all pods", func(t *testing.T) {
		tc.waitForEndpointPodCount(t, ns, fmt.Sprintf("%s-all", name), 3)
		pods := tc.getEndpointPodNames(t, ns, fmt.Sprintf("%s-all", name))
		assert.Len(t, pods, 3, "-all should have 3 endpoints")
	})

	t.Run("sidecar container is present in pod spec", func(t *testing.T) {
		sts := tc.getStatefulSet(t, ns, name)
		var hasSidecar bool
		for _, c := range sts.Spec.Template.Spec.Containers {
			if c.Name == "sidecar" {
				hasSidecar = true
				break
			}
		}
		assert.True(t, hasSidecar, "StatefulSet should have sidecar container")

		require.NotNil(t, sts.Spec.Template.Spec.TerminationGracePeriodSeconds)
		assert.Equal(t, int64(75), *sts.Spec.Template.Spec.TerminationGracePeriodSeconds,
			"terminationGracePeriodSeconds should be 75")
	})
}

// ---------------------------------------------------------------------------
// TestE2E_SidecarFailoverDrainMaster
// Phase 4.2 / 4.4: Delete the master pod, verify graceful failover.
// ---------------------------------------------------------------------------

// TestE2E_SidecarFailoverDrainMaster deploys an HA cluster, writes data to the
// master, then deletes the master pod. It verifies that a new master is elected,
// data survives the failover, and the -rw service switches to the new master.
func TestE2E_SidecarFailoverDrainMaster(t *testing.T) {
	t.Parallel()
	tc := newTestClients(t)
	ns := "e2e-sc-drain-m"
	cleanup := tc.createNamespace(t, ns)
	defer cleanup()

	name := "sc-drain"
	valkey := buildValkeyObject(name, ns, map[string]interface{}{
		"replicas": int64(3),
		"image":    "valkey/valkey:8.0",
		"sentinel": map[string]interface{}{
			"enabled":  true,
			"replicas": int64(3),
		},
	})

	t.Log("Creating HA Valkey CR for drain master test")
	tc.createValkey(t, ns, valkey)
	defer tc.deleteValkey(t, ns, name)

	tc.waitForStatefulSetReady(t, ns, name, 3)
	tc.waitForStatefulSetReady(t, ns, fmt.Sprintf("%s-sentinel", name), 3)
	tc.waitForValkeyPhase(t, ns, name, "OK")

	// Find master and wait for full replication.
	initialMaster := tc.findMasterPod(t, ns, name, 3)
	tc.waitForConnectedReplicas(t, ns, initialMaster, 6379, 2)
	t.Logf("Initial master: %s", initialMaster)

	// Write test data to the master before drain.
	t.Run("write test data", func(t *testing.T) {
		for i := 0; i < 50; i++ {
			key := fmt.Sprintf("drain:key:%d", i)
			resp := tc.valkeyExec(t, ns, initialMaster, 6379,
				"SET", key, fmt.Sprintf("value-%d", i))
			require.Equal(t, "OK", resp)
		}
		t.Log("Wrote 50 test keys to master")

		// Wait for replication sync.
		require.Eventually(t, func() bool {
			info := tc.valkeyExec(t, ns, initialMaster, 6379, "INFO", "replication")
			return strings.Contains(info, "connected_slaves:2")
		}, 30*time.Second, time.Second, "Master should have 2 connected replicas")
	})

	// Record DBSIZE before drain.
	dbsizeBefore := tc.valkeyExec(t, ns, initialMaster, 6379, "DBSIZE")
	t.Logf("DBSIZE before drain: %s", dbsizeBefore)

	// Delete the master pod — triggers SIGTERM → sidecar drain handler.
	t.Run("delete master pod triggers failover", func(t *testing.T) {
		t.Logf("Deleting master pod %s", initialMaster)
		tc.deletePod(t, ns, initialMaster)

		// Wait for StatefulSet to recreate all pods.
		tc.waitForStatefulSetReady(t, ns, name, 3)
		tc.waitForValkeyPhase(t, ns, name, "OK")

		// Wait for all pods to be ready (including sidecar).
		for i := 0; i < 3; i++ {
			tc.waitForPodReady(t, ns, fmt.Sprintf("%s-%d", name, i))
		}

		// Wait for exactly one master to exist (failover may take a moment to settle).
		// Use valkeyExecAllowError because pods may still be starting and kubectl exec
		// can fail transiently with "container not found" during pod restarts in CI.
		require.Eventually(t, func() bool {
			masterCount := 0
			for i := 0; i < 3; i++ {
				podName := fmt.Sprintf("%s-%d", name, i)
				info := tc.valkeyExecAllowError(t, ns, podName, 6379, "INFO", "replication")
				if strings.Contains(info, "role:master") {
					masterCount++
				}
			}
			t.Logf("Master count after failover: %d", masterCount)
			return masterCount == 1
		}, 90*time.Second, 3*time.Second, "Exactly one master should exist after failover")
	})

	// Verify data survived the failover.
	t.Run("data survives failover", func(t *testing.T) {
		// Combine master-detection and data-verification in one retry loop.
		// The deleted pod restarts fast (no PVC) and may briefly report role:master
		// before Sentinel reconfigures it as a replica, resulting in a transient master
		// with DBSIZE=0.  By accepting only the pod that is master AND already holds
		// the expected data we avoid acting on that transient state.
		var newMaster string
		require.Eventually(t, func() bool {
			for i := 0; i < 3; i++ {
				podName := fmt.Sprintf("%s-%d", name, i)
				info := tc.valkeyExecAllowError(t, ns, podName, 6379, "INFO", "replication")
				if !strings.Contains(info, "role:master") {
					continue
				}
				dbsize := tc.valkeyExecAllowError(t, ns, podName, 6379, "DBSIZE")
				t.Logf("Pod %s: role=master DBSIZE=%s (want %s)", podName, dbsize, dbsizeBefore)
				if dbsize == dbsizeBefore {
					newMaster = podName
					return true
				}
			}
			return false
		}, 90*time.Second, 3*time.Second,
			"New master with DBSIZE=%s should be found after failover", dbsizeBefore)

		t.Logf("New master after failover: %s (DBSIZE=%s confirmed)", newMaster, dbsizeBefore)

		// Wait for all replicas to reconnect so the cluster is fully stable before
		// spot-checking individual keys.
		tc.waitForConnectedReplicas(t, ns, newMaster, 6379, 2)

		// Spot-check keys exist.
		for i := 0; i < 50; i += 5 {
			key := fmt.Sprintf("drain:key:%d", i)
			resp := tc.valkeyExec(t, ns, newMaster, 6379, "EXISTS", key)
			assert.Equal(t, "1", resp, "Key %s should exist after failover", key)
		}
	})

	// Verify the -rw service now routes to the new master.
	t.Run("rw service points to new master after failover", func(t *testing.T) {
		newMaster := tc.findMasterPod(t, ns, name, 3)
		tc.waitForEndpointPodCount(t, ns, fmt.Sprintf("%s-rw", name), 1)
		pods := tc.getEndpointPodNames(t, ns, fmt.Sprintf("%s-rw", name))
		require.Len(t, pods, 1, "-rw should have 1 endpoint after failover")
		assert.Equal(t, newMaster, pods[0],
			"-rw endpoint should point to new master %s", newMaster)
	})

	// Verify pod labels are correct after failover.
	t.Run("pod labels correct after failover", func(t *testing.T) {
		// Wait for the sidecar to label all pods correctly after failover.
		require.Eventually(t, func() bool {
			masterCount := 0
			replicaCount := 0
			for i := 0; i < 3; i++ {
				podName := fmt.Sprintf("%s-%d", name, i)
				pod := tc.getPod(t, ns, podName)
				role := pod.Labels["vko.gtrfc.com/instanceRole"]
				switch role {
				case "master":
					masterCount++
				case "replica":
					replicaCount++
				}
			}
			t.Logf("Pod labels: master=%d replica=%d", masterCount, replicaCount)
			return masterCount == 1 && replicaCount == 2
		}, 60*time.Second, 3*time.Second, "Pod labels should be 1 master + 2 replicas after failover")
	})

	// Verify the cluster is fully functional after failover.
	t.Run("cluster functional after failover", func(t *testing.T) {
		newMaster := tc.findMasterPod(t, ns, name, 3)

		// Write new data.
		resp := tc.valkeyExec(t, ns, newMaster, 6379, "SET", "post-drain-key", "works")
		assert.Equal(t, "OK", resp)
		resp = tc.valkeyExec(t, ns, newMaster, 6379, "GET", "post-drain-key")
		assert.Equal(t, "works", resp)

		// Verify Sentinel still monitoring.
		sentinelPod := fmt.Sprintf("%s-sentinel-0", name)
		sentinelResp := tc.valkeyExec(t, ns, sentinelPod, 26379, "SENTINEL", "master", name)
		assert.NotEmpty(t, sentinelResp, "Sentinel should still be monitoring after failover")
	})
}

// ---------------------------------------------------------------------------
// TestE2E_SidecarDrainReplica
// Phase 4.5: Delete a replica pod, verify immediate termination.
// ---------------------------------------------------------------------------

// TestE2E_SidecarDrainReplica deploys an HA cluster and deletes a replica pod.
// It verifies the master does not change, the replica is recreated quickly,
// and replication re-establishes.
func TestE2E_SidecarDrainReplica(t *testing.T) {
	t.Parallel()
	tc := newTestClients(t)
	ns := "e2e-sc-drain-r"
	cleanup := tc.createNamespace(t, ns)
	defer cleanup()

	name := "sc-repdr"
	valkey := buildValkeyObject(name, ns, map[string]interface{}{
		"replicas": int64(3),
		"image":    "valkey/valkey:8.0",
		"sentinel": map[string]interface{}{
			"enabled":  true,
			"replicas": int64(3),
		},
	})

	t.Log("Creating HA Valkey CR for drain replica test")
	tc.createValkey(t, ns, valkey)
	defer tc.deleteValkey(t, ns, name)

	tc.waitForStatefulSetReady(t, ns, name, 3)
	tc.waitForStatefulSetReady(t, ns, fmt.Sprintf("%s-sentinel", name), 3)
	tc.waitForValkeyPhase(t, ns, name, "OK")

	// Find master and a replica.
	initialMaster := tc.findMasterPod(t, ns, name, 3)
	tc.waitForConnectedReplicas(t, ns, initialMaster, 6379, 2)

	var replicaPod string
	for i := 0; i < 3; i++ {
		podName := fmt.Sprintf("%s-%d", name, i)
		if podName != initialMaster {
			replicaPod = podName
			break
		}
	}
	require.NotEmpty(t, replicaPod, "Should find a replica pod to delete")
	t.Logf("Initial master: %s, deleting replica: %s", initialMaster, replicaPod)

	// Write test data.
	tc.valkeyExec(t, ns, initialMaster, 6379, "SET", "replica-drain-key", "test-value")

	// Delete the replica pod.
	t.Run("delete replica does not trigger master failover", func(t *testing.T) {
		tc.deletePod(t, ns, replicaPod)

		// Wait for cluster to recover (all 3 pods ready).
		tc.waitForStatefulSetReady(t, ns, name, 3)
		tc.waitForValkeyPhase(t, ns, name, "OK")

		// Master should remain the same.
		currentMaster := tc.findMasterPod(t, ns, name, 3)
		assert.Equal(t, initialMaster, currentMaster,
			"Master should not change when a replica is deleted")
	})

	// Verify data is intact on the master.
	t.Run("data intact after replica deletion", func(t *testing.T) {
		resp := tc.valkeyExec(t, ns, initialMaster, 6379, "GET", "replica-drain-key")
		assert.Equal(t, "test-value", resp)
	})

	// Verify the recreated replica gets labeled correctly by the sidecar.
	t.Run("recreated replica labeled correctly by sidecar", func(t *testing.T) {
		tc.waitForPodLabel(t, ns, replicaPod, "vko.gtrfc.com/instanceRole", "replica")

		masterCount := 0
		for i := 0; i < 3; i++ {
			podName := fmt.Sprintf("%s-%d", name, i)
			pod := tc.getPod(t, ns, podName)
			if pod.Labels["vko.gtrfc.com/instanceRole"] == "master" {
				masterCount++
			}
		}
		assert.Equal(t, 1, masterCount, "Exactly one master after replica restart")
	})

	// Verify replication re-establishes.
	t.Run("replication re-established after replica restart", func(t *testing.T) {
		tc.waitForConnectedReplicas(t, ns, initialMaster, 6379, 2)

		// Verify data also reached the recreated replica.
		// Use valkeyExecAllowError because the recreated replica may still be
		// starting its valkey container (kubectl exec can fail transiently).
		require.Eventually(t, func() bool {
			resp := tc.valkeyExecAllowError(t, ns, replicaPod, 6379, "GET", "replica-drain-key")
			return resp == "test-value"
		}, 60*time.Second, 2*time.Second,
			"Data should replicate to recreated replica %s", replicaPod)
	})

	// Verify -r service endpoints include the recreated replica.
	t.Run("readonly service includes recreated replica", func(t *testing.T) {
		tc.waitForEndpointPodCount(t, ns, fmt.Sprintf("%s-r", name), 2)
		pods := tc.getEndpointPodNames(t, ns, fmt.Sprintf("%s-r", name))
		assert.Len(t, pods, 2, "-r should have 2 endpoints after replica restart")

		found := false
		for _, p := range pods {
			if p == replicaPod {
				found = true
				break
			}
		}
		assert.True(t, found,
			"Recreated replica %s should appear in -r service endpoints", replicaPod)
	})
}

// ---------------------------------------------------------------------------
// TestE2E_LegacyServiceCleanup
// Phase 4.6: Verify legacy services are auto-deleted.
// ---------------------------------------------------------------------------

// TestE2E_LegacyServiceCleanup deploys a Valkey cluster, then manually creates
// legacy-named services with owner references. It verifies the operator deletes
// them on the next reconcile while preserving the new-scheme services.
func TestE2E_LegacyServiceCleanup(t *testing.T) {
	t.Parallel()
	tc := newTestClients(t)
	ns := "e2e-sc-legacy"
	cleanup := tc.createNamespace(t, ns)
	defer cleanup()

	name := "sc-legacy"
	valkey := buildValkeyObject(name, ns, map[string]interface{}{
		"replicas": int64(3),
		"image":    "valkey/valkey:8.0",
		"sentinel": map[string]interface{}{
			"enabled":  true,
			"replicas": int64(3),
		},
	})

	t.Log("Creating HA Valkey CR for legacy cleanup test")
	tc.createValkey(t, ns, valkey)
	defer tc.deleteValkey(t, ns, name)

	// Wait for the cluster to be operational.
	tc.waitForStatefulSetReady(t, ns, name, 3)
	tc.waitForValkeyPhase(t, ns, name, "OK")

	// Get the Valkey CR UID to construct correct owner references.
	ctx := context.Background()
	valkeyObj, err := tc.dynamic.Resource(valkeyGVR).Namespace(ns).Get(
		ctx, name, metav1.GetOptions{})
	require.NoError(t, err)
	uid := valkeyObj.GetUID()

	isController := true
	blockOwnerDeletion := true
	ownerRef := metav1.OwnerReference{
		APIVersion:         "vko.gtrfc.com/v1",
		Kind:               "Valkey",
		Name:               name,
		UID:                uid,
		Controller:         &isController,
		BlockOwnerDeletion: &blockOwnerDeletion,
	}

	t.Run("legacy services with owner refs are deleted", func(t *testing.T) {
		// Create legacy client service (<name>).
		legacySvc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:            name,
				Namespace:       ns,
				OwnerReferences: []metav1.OwnerReference{ownerRef},
			},
			Spec: corev1.ServiceSpec{
				Ports: []corev1.ServicePort{{
					Name:     "valkey",
					Port:     6379,
					Protocol: corev1.ProtocolTCP,
				}},
			},
		}
		tc.ensureServiceCreated(t, ns, legacySvc)

		// Create legacy read service (<name>-read).
		legacyReadSvc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:            name + "-read",
				Namespace:       ns,
				OwnerReferences: []metav1.OwnerReference{ownerRef},
			},
			Spec: corev1.ServiceSpec{
				Ports: []corev1.ServicePort{{
					Name:     "valkey",
					Port:     6379,
					Protocol: corev1.ProtocolTCP,
				}},
			},
		}
		tc.ensureServiceCreated(t, ns, legacyReadSvc)

		// Wait for the operator to delete the legacy services.
		tc.waitForServiceAbsent(t, ns, name)
		tc.waitForServiceAbsent(t, ns, name+"-read")
	})

	t.Run("service without owner ref is not deleted", func(t *testing.T) {
		// Create a service with the legacy name pattern but NO owner reference.
		// The operator should NOT delete it (safety mechanism).
		unownedSvc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name + "-unrelated",
				Namespace: ns,
			},
			Spec: corev1.ServiceSpec{
				Ports: []corev1.ServicePort{{
					Name:     "dummy",
					Port:     12345,
					Protocol: corev1.ProtocolTCP,
				}},
			},
		}
		tc.ensureServiceCreated(t, ns, unownedSvc)

		// Give controller time to reconcile.
		time.Sleep(5 * time.Second)

		// The unrelated service should still exist.
		_, err := tc.kube.CoreV1().Services(ns).Get(
			context.Background(), name+"-unrelated", metav1.GetOptions{})
		assert.NoError(t, err,
			"Unrelated service should not be deleted by legacy cleanup")

		// Clean up the test service.
		_ = tc.kube.CoreV1().Services(ns).Delete(
			context.Background(), name+"-unrelated", metav1.DeleteOptions{})
	})

	t.Run("new-scheme services survive legacy cleanup", func(t *testing.T) {
		// All new-scheme services should still exist.
		rwSvc := tc.getService(t, ns, fmt.Sprintf("%s-rw", name))
		assert.NotNil(t, rwSvc)

		allSvc := tc.getService(t, ns, fmt.Sprintf("%s-all", name))
		assert.NotNil(t, allSvc)

		rSvc := tc.getService(t, ns, fmt.Sprintf("%s-r", name))
		assert.NotNil(t, rSvc)

		headlessSvc := tc.getService(t, ns, fmt.Sprintf("%s-headless", name))
		assert.NotNil(t, headlessSvc)
	})
}

// ---------------------------------------------------------------------------
// TestE2E_StandaloneServicesOnly
// Phase 4.7: Standalone mode — only -rw service, no -r or -all.
// ---------------------------------------------------------------------------

// TestE2E_StandaloneServicesOnly deploys a standalone (replicas=1) Valkey instance
// and verifies that only -rw and -headless services are created. It also confirms
// the sidecar labels the single pod as master and the -rw service routes to it.
func TestE2E_StandaloneServicesOnly(t *testing.T) {
	t.Parallel()
	tc := newTestClients(t)
	ns := "e2e-sc-standalone"
	cleanup := tc.createNamespace(t, ns)
	defer cleanup()

	name := "sc-sa"
	valkey := buildValkeyObject(name, ns, map[string]interface{}{
		"replicas": int64(1),
		"image":    "valkey/valkey:8.0",
	})

	t.Log("Creating standalone Valkey CR for service verification")
	tc.createValkey(t, ns, valkey)
	defer tc.deleteValkey(t, ns, name)

	tc.waitForStatefulSetReady(t, ns, name, 1)
	tc.waitForValkeyPhase(t, ns, name, "OK")

	t.Run("only rw and headless services exist", func(t *testing.T) {
		// -rw must exist.
		rwSvc := tc.getService(t, ns, fmt.Sprintf("%s-rw", name))
		assert.Equal(t, "master", rwSvc.Spec.Selector["vko.gtrfc.com/instanceRole"],
			"-rw service must select master pods")

		// -headless must exist.
		headlessSvc := tc.getService(t, ns, fmt.Sprintf("%s-headless", name))
		assert.Equal(t, "None", string(headlessSvc.Spec.ClusterIP))

		// -all must NOT exist for standalone.
		_, errAll := tc.tryGetService(t, ns, fmt.Sprintf("%s-all", name))
		assert.Error(t, errAll, "-all service must not exist for standalone")

		// -r must NOT exist for standalone.
		_, errR := tc.tryGetService(t, ns, fmt.Sprintf("%s-r", name))
		assert.Error(t, errR, "-r service must not exist for standalone")
	})

	t.Run("sidecar labels standalone pod as master", func(t *testing.T) {
		podName := fmt.Sprintf("%s-0", name)
		tc.waitForPodLabel(t, ns, podName, "vko.gtrfc.com/instanceRole", "master")

		// Verify via Valkey INFO.
		info := tc.valkeyExec(t, ns, podName, 6379, "INFO", "replication")
		assert.Contains(t, info, "role:master",
			"Standalone pod should report role:master")
	})

	t.Run("rw service endpoint is the standalone pod", func(t *testing.T) {
		tc.waitForEndpointPodCount(t, ns, fmt.Sprintf("%s-rw", name), 1)
		pods := tc.getEndpointPodNames(t, ns, fmt.Sprintf("%s-rw", name))
		require.Len(t, pods, 1, "-rw should have 1 endpoint")
		assert.Equal(t, fmt.Sprintf("%s-0", name), pods[0],
			"-rw endpoint should be the standalone pod")
	})

	t.Run("standalone pod responds to commands", func(t *testing.T) {
		podName := fmt.Sprintf("%s-0", name)

		resp := tc.valkeyExec(t, ns, podName, 6379, "SET", "standalone-test", "works")
		assert.Equal(t, "OK", resp)

		resp = tc.valkeyExec(t, ns, podName, 6379, "GET", "standalone-test")
		assert.Equal(t, "works", resp)
	})
}
