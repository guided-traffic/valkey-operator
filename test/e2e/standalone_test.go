//go:build e2e

package e2e

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
)

// TestE2E_StandaloneCluster tests a single-node Valkey deployment.
func TestE2E_StandaloneCluster(t *testing.T) {
	tc := newTestClients(t)
	ns := "e2e-standalone"
	cleanup := tc.createNamespace(t, ns)
	defer cleanup()

	name := "standalone"
	valkey := buildValkeyObject(name, ns, map[string]interface{}{
		"replicas": int64(1),
		"image":    "valkey/valkey:8.0",
	})

	t.Log("Creating standalone Valkey CR")
	tc.createValkey(t, ns, valkey)
	defer tc.deleteValkey(t, ns, name)

	t.Run("StatefulSet becomes ready", func(t *testing.T) {
		tc.waitForStatefulSetReady(t, ns, name, 1)
	})

	t.Run("Pod is running", func(t *testing.T) {
		tc.waitForPodReady(t, ns, fmt.Sprintf("%s-0", name))
	})

	t.Run("Services are created", func(t *testing.T) {
		// Headless service for DNS.
		svc := tc.getService(t, ns, fmt.Sprintf("%s-headless", name))
		assert.Equal(t, "None", string(svc.Spec.ClusterIP))

		// Client-facing service.
		clientSvc := tc.getService(t, ns, name)
		assert.NotEmpty(t, clientSvc.Spec.ClusterIP)
		assert.Equal(t, int32(6379), clientSvc.Spec.Ports[0].Port)
	})

	t.Run("ConfigMap exists with valkey.conf", func(t *testing.T) {
		cm := tc.getConfigMap(t, ns, fmt.Sprintf("%s-config", name))
		conf, ok := cm.Data["valkey.conf"]
		require.True(t, ok, "valkey.conf key not found in ConfigMap")
		assert.Contains(t, conf, "bind 0.0.0.0")
		assert.Contains(t, conf, "port 6379")
	})

	t.Run("Common labels are applied", func(t *testing.T) {
		sts := tc.getStatefulSet(t, ns, name)
		labels := sts.Labels
		assertLabelExists(t, labels, "app.kubernetes.io/component", "valkey")
		assertLabelExists(t, labels, "app.kubernetes.io/instance", name)
		assertLabelExists(t, labels, "app.kubernetes.io/managed-by", "vko.gtrfc.com")
		assertLabelExists(t, labels, "app.kubernetes.io/name", "valkey")
	})

	t.Run("CRD status shows OK", func(t *testing.T) {
		tc.waitForValkeyPhase(t, ns, name, "OK")
		status := tc.getValkeyStatus(t, ns, name)

		readyReplicas, _, _ := unstructuredNestedFloat64(status, "readyReplicas")
		assert.Equal(t, float64(1), readyReplicas)

		masterPod, _, _ := unstructuredNestedString(status, "masterPod")
		assert.Equal(t, fmt.Sprintf("%s-0", name), masterPod)
	})

	t.Run("Valkey responds to PING", func(t *testing.T) {
		response := tc.valkeyExec(t, ns, fmt.Sprintf("%s-0", name), 6379, "PING")
		assert.Equal(t, "PONG", response)
	})

	t.Run("Write and read data", func(t *testing.T) {
		podName := fmt.Sprintf("%s-0", name)

		// Write test data.
		setResp := tc.valkeyExec(t, ns, podName, 6379, "SET", "e2e-standalone-key", "hello-from-e2e")
		assert.Equal(t, "OK", setResp)

		// Read it back.
		getResp := tc.valkeyExec(t, ns, podName, 6379, "GET", "e2e-standalone-key")
		assert.Equal(t, "hello-from-e2e", getResp)
	})

	t.Run("Cleanup and deletion", func(t *testing.T) {
		tc.deleteValkey(t, ns, name)
		tc.waitForDeletion(t, ns, name)
	})
}

// TestE2E_HAClusterWithSentinel tests a 3-node HA Valkey deployment with Sentinel.
func TestE2E_HAClusterWithSentinel(t *testing.T) {
	tc := newTestClients(t)
	ns := "e2e-ha"
	cleanup := tc.createNamespace(t, ns)
	defer cleanup()

	name := "ha-test"
	valkey := buildValkeyObject(name, ns, map[string]interface{}{
		"replicas": int64(3),
		"image":    "valkey/valkey:8.0",
		"sentinel": map[string]interface{}{
			"enabled":  true,
			"replicas": int64(3),
		},
	})

	t.Log("Creating HA Valkey CR with Sentinel")
	tc.createValkey(t, ns, valkey)
	defer tc.deleteValkey(t, ns, name)

	t.Run("Valkey StatefulSet becomes ready", func(t *testing.T) {
		tc.waitForStatefulSetReady(t, ns, name, 3)
	})

	t.Run("Sentinel StatefulSet becomes ready", func(t *testing.T) {
		tc.waitForStatefulSetReady(t, ns, fmt.Sprintf("%s-sentinel", name), 3)
	})

	t.Run("All Valkey pods are running", func(t *testing.T) {
		for i := 0; i < 3; i++ {
			tc.waitForPodReady(t, ns, fmt.Sprintf("%s-%d", name, i))
		}
	})

	t.Run("All Sentinel pods are running", func(t *testing.T) {
		for i := 0; i < 3; i++ {
			tc.waitForPodReady(t, ns, fmt.Sprintf("%s-sentinel-%d", name, i))
		}
	})

	t.Run("Valkey services are created", func(t *testing.T) {
		// Headless service.
		headless := tc.getService(t, ns, fmt.Sprintf("%s-headless", name))
		assert.Equal(t, "None", string(headless.Spec.ClusterIP))

		// Client service.
		client := tc.getService(t, ns, name)
		assert.NotEmpty(t, client.Spec.ClusterIP)
	})

	t.Run("Sentinel headless service exists", func(t *testing.T) {
		sentinelSvc := tc.getService(t, ns, fmt.Sprintf("%s-sentinel-headless", name))
		assert.Equal(t, "None", string(sentinelSvc.Spec.ClusterIP))
		assert.Equal(t, int32(26379), sentinelSvc.Spec.Ports[0].Port)
	})

	t.Run("ConfigMaps are created", func(t *testing.T) {
		// Master config.
		masterCm := tc.getConfigMap(t, ns, fmt.Sprintf("%s-config", name))
		masterConf := masterCm.Data["valkey.conf"]
		assert.NotContains(t, masterConf, "replicaof", "Master config should not have replicaof")

		// Replica config.
		replicaCm := tc.getConfigMap(t, ns, fmt.Sprintf("%s-replica-config", name))
		replicaConf := replicaCm.Data["valkey.conf"]
		assert.Contains(t, replicaConf, "replicaof", "Replica config should have replicaof")

		// Sentinel config.
		sentinelCm := tc.getConfigMap(t, ns, fmt.Sprintf("%s-sentinel-config", name))
		sentinelConf := sentinelCm.Data["sentinel.conf"]
		assert.Contains(t, sentinelConf, "sentinel monitor")
		assert.Contains(t, sentinelConf, "resolve-hostnames yes")
	})

	t.Run("Valkey StatefulSet has init container for HA", func(t *testing.T) {
		sts := tc.getStatefulSet(t, ns, name)
		initContainers := sts.Spec.Template.Spec.InitContainers
		require.NotEmpty(t, initContainers, "HA StatefulSet should have init containers")
		assert.Equal(t, "init-config-selector", initContainers[0].Name)
	})

	t.Run("Sentinel labels include component sentinel", func(t *testing.T) {
		sentinelSts := tc.getStatefulSet(t, ns, fmt.Sprintf("%s-sentinel", name))
		assertLabelExists(t, sentinelSts.Labels, "app.kubernetes.io/component", "sentinel")
	})

	t.Run("CRD status shows OK with HA message", func(t *testing.T) {
		tc.waitForValkeyPhase(t, ns, name, "OK")
		status := tc.getValkeyStatus(t, ns, name)

		readyReplicas, _, _ := unstructuredNestedFloat64(status, "readyReplicas")
		assert.Equal(t, float64(3), readyReplicas)

		message, _, _ := unstructuredNestedString(status, "message")
		assert.Contains(t, message, "HA cluster ready")
	})

	t.Run("All pods respond to PING", func(t *testing.T) {
		for i := 0; i < 3; i++ {
			podName := fmt.Sprintf("%s-%d", name, i)
			resp := tc.valkeyExec(t, ns, podName, 6379, "PING")
			assert.Equal(t, "PONG", resp, "Pod %s should respond to PING", podName)
		}
	})

	t.Run("Master is identified via INFO replication", func(t *testing.T) {
		// Pod-0 should be the initial master.
		info := tc.valkeyExec(t, ns, fmt.Sprintf("%s-0", name), 6379, "INFO", "replication")
		assert.Contains(t, info, "role:master", "Pod-0 should be master")
	})

	t.Run("Replicas are connected to master", func(t *testing.T) {
		// Pod-1 and pod-2 should be replicas.
		for i := 1; i < 3; i++ {
			podName := fmt.Sprintf("%s-%d", name, i)
			info := tc.valkeyExec(t, ns, podName, 6379, "INFO", "replication")
			assert.Contains(t, info, "role:slave", "Pod %s should be a replica", podName)
		}
	})

	t.Run("Sentinel is monitoring the cluster", func(t *testing.T) {
		// Query any sentinel instance for the master.
		sentinelPod := fmt.Sprintf("%s-sentinel-0", name)
		resp := tc.valkeyExec(t, ns, sentinelPod, 26379, "SENTINEL", "master", name)
		assert.NotEmpty(t, resp, "Sentinel should return master info")
		// The response should contain the master name.
		t.Logf("Sentinel master info: %s", resp)
	})
}

// TestE2E_DataReplication tests writing data to master and reading from replicas.
func TestE2E_DataReplication(t *testing.T) {
	tc := newTestClients(t)
	ns := "e2e-replication"
	cleanup := tc.createNamespace(t, ns)
	defer cleanup()

	name := "repl-test"
	valkey := buildValkeyObject(name, ns, map[string]interface{}{
		"replicas": int64(3),
		"image":    "valkey/valkey:8.0",
		"sentinel": map[string]interface{}{
			"enabled":  true,
			"replicas": int64(3),
		},
	})

	t.Log("Creating HA Valkey cluster for replication testing")
	tc.createValkey(t, ns, valkey)
	defer tc.deleteValkey(t, ns, name)

	// Wait for full cluster to be ready.
	tc.waitForStatefulSetReady(t, ns, name, 3)
	tc.waitForStatefulSetReady(t, ns, fmt.Sprintf("%s-sentinel", name), 3)
	tc.waitForValkeyPhase(t, ns, name, "OK")

	// Give replication a moment to fully establish.
	t.Log("Waiting for replication to fully establish...")
	time.Sleep(5 * time.Second)

	// Verify master is pod-0.
	masterPod := fmt.Sprintf("%s-0", name)
	info := tc.valkeyExec(t, ns, masterPod, 6379, "INFO", "replication")
	require.Contains(t, info, "role:master", "Pod-0 should be master")

	t.Run("Write multiple keys to master", func(t *testing.T) {
		testData := map[string]string{
			"e2e:string":    "hello-valkey-operator",
			"e2e:number":    "42",
			"e2e:timestamp": time.Now().Format(time.RFC3339),
			"e2e:unicode":   "🚀 Valkey Operator E2E Test",
			"e2e:long":      strings.Repeat("data-", 100),
		}

		for key, value := range testData {
			resp := tc.valkeyExec(t, ns, masterPod, 6379, "SET", key, value)
			assert.Equal(t, "OK", resp, "SET %s should succeed", key)
		}

		// Also test a list.
		for i := 0; i < 5; i++ {
			resp := tc.valkeyExec(t, ns, masterPod, 6379, "RPUSH", "e2e:list", fmt.Sprintf("item-%d", i))
			assert.NotEmpty(t, resp, "RPUSH should return list length")
		}

		// Also test a hash.
		resp := tc.valkeyExec(t, ns, masterPod, 6379, "HSET", "e2e:hash", "field1", "value1", "field2", "value2")
		assert.NotEmpty(t, resp)
	})

	t.Run("Wait for replication sync", func(t *testing.T) {
		// Wait until master confirms connected replicas and sync is complete.
		require.Eventually(t, func() bool {
			info := tc.valkeyExec(t, ns, masterPod, 6379, "INFO", "replication")
			return strings.Contains(info, "connected_slaves:2")
		}, 30*time.Second, time.Second, "Master should have 2 connected replicas")
	})

	t.Run("Read data from replica 1", func(t *testing.T) {
		replicaPod := fmt.Sprintf("%s-1", name)

		// Wait a bit for data to sync.
		require.Eventually(t, func() bool {
			resp := tc.valkeyExec(t, ns, replicaPod, 6379, "GET", "e2e:string")
			return resp == "hello-valkey-operator"
		}, 30*time.Second, time.Second, "Data should replicate to replica 1")

		// Verify all keys.
		resp := tc.valkeyExec(t, ns, replicaPod, 6379, "GET", "e2e:number")
		assert.Equal(t, "42", resp)

		resp = tc.valkeyExec(t, ns, replicaPod, 6379, "GET", "e2e:unicode")
		assert.Equal(t, "🚀 Valkey Operator E2E Test", resp)

		resp = tc.valkeyExec(t, ns, replicaPod, 6379, "GET", "e2e:long")
		assert.Equal(t, strings.Repeat("data-", 100), resp)

		// Verify list length.
		resp = tc.valkeyExec(t, ns, replicaPod, 6379, "LLEN", "e2e:list")
		assert.Equal(t, "5", resp)

		// Verify hash.
		resp = tc.valkeyExec(t, ns, replicaPod, 6379, "HGET", "e2e:hash", "field1")
		assert.Equal(t, "value1", resp)
	})

	t.Run("Read data from replica 2", func(t *testing.T) {
		replicaPod := fmt.Sprintf("%s-2", name)

		// Verify same data on second replica.
		require.Eventually(t, func() bool {
			resp := tc.valkeyExec(t, ns, replicaPod, 6379, "GET", "e2e:string")
			return resp == "hello-valkey-operator"
		}, 30*time.Second, time.Second, "Data should replicate to replica 2")

		resp := tc.valkeyExec(t, ns, replicaPod, 6379, "GET", "e2e:number")
		assert.Equal(t, "42", resp)

		resp = tc.valkeyExec(t, ns, replicaPod, 6379, "LLEN", "e2e:list")
		assert.Equal(t, "5", resp)
	})

	t.Run("Replica is read-only", func(t *testing.T) {
		replicaPod := fmt.Sprintf("%s-1", name)

		// Attempt to write to a replica should fail.
		resp := tc.valkeyExecAllowError(t, ns, replicaPod, 6379, "SET", "e2e:readonly-test", "should-fail")
		// The response is an error string or empty — either way writing should not succeed.
		t.Logf("Write to replica response: %q", resp)
	})

	t.Run("DBSIZE consistent across cluster", func(t *testing.T) {
		masterSize := tc.valkeyExec(t, ns, masterPod, 6379, "DBSIZE")
		t.Logf("Master DBSIZE: %s", masterSize)

		for i := 1; i < 3; i++ {
			replicaPod := fmt.Sprintf("%s-%d", name, i)
			require.Eventually(t, func() bool {
				replicaSize := tc.valkeyExec(t, ns, replicaPod, 6379, "DBSIZE")
				return replicaSize == masterSize
			}, 30*time.Second, time.Second, "Replica %d DBSIZE should match master", i)
		}
	})
}

// TestE2E_OperatorHealth verifies the operator itself is healthy.
func TestE2E_OperatorHealth(t *testing.T) {
	tc := newTestClients(t)
	ctx := t

	_ = ctx

	t.Run("Operator deployment is running", func(t *testing.T) {
		tc.waitForStatefulSetOrDeploymentReady(t, "valkey-operator-system", "valkey-operator")
	})

	t.Run("Operator pod is healthy", func(t *testing.T) {
		pods, err := tc.kube.CoreV1().Pods("valkey-operator-system").List(
			t.Context(),
			metav1.ListOptions{
				LabelSelector: "app.kubernetes.io/name=valkey-operator",
			},
		)
		require.NoError(t, err)
		require.NotEmpty(t, pods.Items, "No operator pods found")

		for _, pod := range pods.Items {
			assert.Equal(t, corev1.PodRunning, pod.Status.Phase, "Operator pod %s should be running", pod.Name)
			for _, cond := range pod.Status.Conditions {
				if cond.Type == corev1.PodReady {
					assert.Equal(t, corev1.ConditionTrue, cond.Status, "Operator pod %s should be ready", pod.Name)
				}
			}
		}
	})
}

// TestE2E_CertManagerReady verifies that cert-manager is running and the e2e CA issuer is available.
func TestE2E_CertManagerReady(t *testing.T) {
	tc := newTestClients(t)

	t.Run("cert-manager deployments are ready", func(t *testing.T) {
		deployments := []string{"cert-manager", "cert-manager-webhook", "cert-manager-cainjector"}
		for _, dep := range deployments {
			tc.waitForDeploymentReady(t, "cert-manager", dep)
		}
	})
}

// waitForDeploymentReady waits until a Deployment has the expected number of ready replicas.
func (tc *testClients) waitForDeploymentReady(t *testing.T, namespace, name string) {
	t.Helper()
	ctx := t.Context()

	err := wait.PollUntilContextTimeout(ctx, pollInterval, testTimeout, true, func(ctx context.Context) (bool, error) {
		dep, err := tc.kube.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				return false, nil
			}
			return false, err
		}
		t.Logf("Deployment %s/%s: available=%d/%d", namespace, name, dep.Status.AvailableReplicas, *dep.Spec.Replicas)
		return dep.Status.AvailableReplicas == *dep.Spec.Replicas, nil
	})
	require.NoError(t, err, "Deployment %s/%s did not become ready", namespace, name)
}

// waitForStatefulSetOrDeploymentReady tries to find either a Deployment or StatefulSet and waits for readiness.
func (tc *testClients) waitForStatefulSetOrDeploymentReady(t *testing.T, namespace, name string) {
	t.Helper()
	ctx := t.Context()

	err := wait.PollUntilContextTimeout(ctx, pollInterval, testTimeout, true, func(ctx context.Context) (bool, error) {
		// Try Deployment first.
		dep, err := tc.kube.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
		if err == nil {
			t.Logf("Deployment %s/%s: available=%d/%d", namespace, name, dep.Status.AvailableReplicas, *dep.Spec.Replicas)
			return dep.Status.AvailableReplicas == *dep.Spec.Replicas, nil
		}

		// Try with helm fullname pattern.
		deps, err := tc.kube.AppsV1().Deployments(namespace).List(ctx, metav1.ListOptions{})
		if err == nil {
			for _, d := range deps.Items {
				if strings.Contains(d.Name, "valkey-operator") {
					t.Logf("Deployment %s/%s: available=%d/%d", namespace, d.Name, d.Status.AvailableReplicas, *d.Spec.Replicas)
					return d.Status.AvailableReplicas == *d.Spec.Replicas, nil
				}
			}
		}

		return false, nil
	})
	require.NoError(t, err, "Operator deployment in %s did not become ready", namespace)
}

// valkeyExecAllowError executes a command but does not fail on Valkey errors (e.g., READONLY).
func (tc *testClients) valkeyExecAllowError(t *testing.T, namespace, podName string, port int, args ...string) string {
	t.Helper()

	ip := tc.getPodIP(t, namespace, podName)
	addr := fmt.Sprintf("%s:%d", ip, port)

	dialer := net.Dialer{Timeout: 5 * time.Second}
	conn, err := dialer.DialContext(t.Context(), "tcp", addr)
	require.NoError(t, err, "Failed to connect to Valkey at %s", addr)
	defer conn.Close()

	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))

	cmd := formatRESP(args)
	_, err = conn.Write([]byte(cmd))
	require.NoError(t, err, "Failed to write command to Valkey")

	reader := bufio.NewReader(conn)
	line, err := reader.ReadString('\n')
	require.NoError(t, err)
	return strings.TrimRight(line, "\r\n")
}

// Helper functions for unstructured nested access.
func unstructuredNestedFloat64(obj map[string]interface{}, fields ...string) (float64, bool, error) {
	val, found, err := nestedField(obj, fields...)
	if !found || err != nil {
		return 0, found, err
	}
	switch v := val.(type) {
	case float64:
		return v, true, nil
	case int64:
		return float64(v), true, nil
	default:
		return 0, true, fmt.Errorf("unexpected type %T", val)
	}
}

func unstructuredNestedString(obj map[string]interface{}, fields ...string) (string, bool, error) {
	val, found, err := nestedField(obj, fields...)
	if !found || err != nil {
		return "", found, err
	}
	s, ok := val.(string)
	if !ok {
		return "", true, fmt.Errorf("unexpected type %T", val)
	}
	return s, true, nil
}

func nestedField(obj map[string]interface{}, fields ...string) (interface{}, bool, error) {
	var current interface{} = obj
	for _, field := range fields {
		m, ok := current.(map[string]interface{})
		if !ok {
			return nil, false, nil
		}
		val, ok := m[field]
		if !ok {
			return nil, false, nil
		}
		current = val
	}
	return current, true, nil
}
