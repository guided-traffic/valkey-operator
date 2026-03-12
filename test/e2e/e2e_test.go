//go:build e2e

// Package e2e provides end-to-end tests for the valkey-operator.
// These tests run against a real Kubernetes cluster (typically Kind) with the
// operator deployed via Helm. They verify the full lifecycle of Valkey
// standalone and HA clusters, including data replication.
package e2e

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// testTimeout is the maximum time to wait for resources to become ready.
const testTimeout = 5 * time.Minute

// rollingUpdateTimeout is a longer timeout for rolling update operations.
// Each HA rolling update involves 3 pod replacements + sentinel failover + sentinel
// sync, which can take several minutes on a loaded single-node Kind cluster.
// 12 minutes gives enough headroom even when the cluster is moderately loaded.
const rollingUpdateTimeout = 12 * time.Minute

// pollInterval is the interval between polling attempts for short-lived operations.
const pollInterval = 2 * time.Second

// rollingUpdatePollInterval is a coarser polling interval for long-running
// rolling update waits. Using a larger interval reduces the number of API
// server requests made by parallel tests, preventing rate-limiter exhaustion
// ("client rate limiter Wait returned an error: context deadline exceeded").
const rollingUpdatePollInterval = 5 * time.Second

// valkeyGVR is the GroupVersionResource for the Valkey CRD.
var valkeyGVR = schema.GroupVersionResource{
	Group:    "vko.gtrfc.com",
	Version:  "v1",
	Resource: "valkeys",
}

// testClients holds shared Kubernetes clients for all e2e tests.
type testClients struct {
	kube    kubernetes.Interface
	dynamic dynamic.Interface
}

// newTestClients creates Kubernetes clients from the current kubeconfig.
func newTestClients(t *testing.T) *testClients {
	t.Helper()

	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		home, err := os.UserHomeDir()
		require.NoError(t, err)
		kubeconfig = home + "/.kube/config"
	}

	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	require.NoError(t, err, "Failed to build kubeconfig")

	// Raise API client rate limits to avoid "client rate limiter Wait returned an
	// error: context deadline exceeded" when multiple parallel e2e tests poll the
	// API server simultaneously. The default is 5 QPS / 10 burst which is too
	// conservative for concurrent long-lived polling loops.
	config.QPS = 50
	config.Burst = 100

	kubeClient, err := kubernetes.NewForConfig(config)
	require.NoError(t, err, "Failed to create kubernetes client")

	dynClient, err := dynamic.NewForConfig(config)
	require.NoError(t, err, "Failed to create dynamic client")

	return &testClients{
		kube:    kubeClient,
		dynamic: dynClient,
	}
}

// createNamespace creates a test namespace and returns a cleanup function.
func (tc *testClients) createNamespace(t *testing.T, name string) func() {
	t.Helper()
	ctx := context.Background()

	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}

	_, err := tc.kube.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		// Namespace already exists, clean it up first.
		_ = tc.kube.CoreV1().Namespaces().Delete(ctx, name, metav1.DeleteOptions{})
		require.Eventually(t, func() bool {
			_, err := tc.kube.CoreV1().Namespaces().Get(ctx, name, metav1.GetOptions{})
			return apierrors.IsNotFound(err)
		}, 60*time.Second, time.Second, "Namespace %s did not get deleted", name)
		_, err = tc.kube.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
	}
	require.NoError(t, err, "Failed to create namespace %s", name)

	return func() {
		_ = tc.kube.CoreV1().Namespaces().Delete(ctx, name, metav1.DeleteOptions{})
	}
}

// createValkey creates a Valkey CR and returns it as an unstructured object.
func (tc *testClients) createValkey(t *testing.T, namespace string, valkey *unstructured.Unstructured) {
	t.Helper()
	ctx := context.Background()

	_, err := tc.dynamic.Resource(valkeyGVR).Namespace(namespace).Create(ctx, valkey, metav1.CreateOptions{})
	require.NoError(t, err, "Failed to create Valkey CR")
}

// waitForStatefulSetReady waits until a StatefulSet has the expected number of ready replicas.
func (tc *testClients) waitForStatefulSetReady(t *testing.T, namespace, name string, replicas int32) {
	t.Helper()
	ctx := context.Background()

	err := wait.PollUntilContextTimeout(ctx, pollInterval, testTimeout, true, func(ctx context.Context) (bool, error) {
		sts, err := tc.kube.AppsV1().StatefulSets(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				return false, nil
			}
			return false, err
		}
		t.Logf("StatefulSet %s: ready=%d/%d", name, sts.Status.ReadyReplicas, replicas)
		return sts.Status.ReadyReplicas == replicas, nil
	})
	require.NoError(t, err, "StatefulSet %s/%s did not become ready with %d replicas", namespace, name, replicas)
}

// waitForValkeyPhase waits until the Valkey CR reaches the expected phase.
// waitForValkeyPhase waits until the Valkey CR reaches the expected phase.
// Uses testTimeout (5 min) — for rolling update operations use waitForValkeyPhaseAfterRollingUpdate.
func (tc *testClients) waitForValkeyPhase(t *testing.T, namespace, name, expectedPhase string) {
	t.Helper()
	tc.waitForValkeyPhaseWithTimeout(t, namespace, name, expectedPhase, testTimeout)
}

// waitForValkeyPhaseAfterRollingUpdate waits for the Valkey CR to reach the expected
// phase after a rolling update operation, using the longer rollingUpdateTimeout.
// This accounts for the extra time needed to finalize HA rolling updates in CI
// (sentinel sync, replica reconnection, pod recreation after master replacement).
func (tc *testClients) waitForValkeyPhaseAfterRollingUpdate(t *testing.T, namespace, name, expectedPhase string) {
	t.Helper()
	tc.waitForValkeyPhaseWithTimeout(t, namespace, name, expectedPhase, rollingUpdateTimeout)
}

// waitForValkeyPhaseWithTimeout waits until the Valkey CR reaches the expected phase
// within the given timeout. Uses rollingUpdatePollInterval for long timeouts to
// reduce API server load from parallel tests.
func (tc *testClients) waitForValkeyPhaseWithTimeout(t *testing.T, namespace, name, expectedPhase string, timeout time.Duration) {
	t.Helper()
	ctx := context.Background()

	interval := pollInterval
	if timeout >= rollingUpdateTimeout {
		interval = rollingUpdatePollInterval
	}

	err := wait.PollUntilContextTimeout(ctx, interval, timeout, true, func(ctx context.Context) (bool, error) {
		valkey, err := tc.dynamic.Resource(valkeyGVR).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return false, err
		}

		phase, found, err := unstructured.NestedString(valkey.Object, "status", "phase")
		if err != nil || !found {
			return false, nil
		}
		msg, _, _ := unstructured.NestedString(valkey.Object, "status", "message")
		t.Logf("Valkey %s phase: %s (want: %s) message: %s", name, phase, expectedPhase, msg)
		return phase == expectedPhase, nil
	})
	require.NoError(t, err, "Valkey %s/%s did not reach phase %s", namespace, name, expectedPhase)
}

// getValkeyStatus returns the current status fields of a Valkey CR.
func (tc *testClients) getValkeyStatus(t *testing.T, namespace, name string) map[string]interface{} {
	t.Helper()
	ctx := context.Background()

	valkey, err := tc.dynamic.Resource(valkeyGVR).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	require.NoError(t, err, "Failed to get Valkey CR %s/%s", namespace, name)

	status, found, err := unstructured.NestedMap(valkey.Object, "status")
	require.NoError(t, err)
	require.True(t, found, "status not found on Valkey CR")
	return status
}

// valkeyExec executes a Valkey command via kubectl exec + valkey-cli inside the pod.
// This avoids direct TCP connections to Pod IPs which are unreachable from
// the host on macOS with Kind/Docker Desktop.
// Retries up to 5 times with exponential backoff on transient kubectl failures
// to handle slow pod starts in resource-constrained CI environments.
func (tc *testClients) valkeyExec(t *testing.T, namespace, podName string, port int, args ...string) string {
	t.Helper()

	const maxAttempts = 5
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if attempt > 1 {
			delay := time.Duration(attempt) * 2 * time.Second
			t.Logf("Retrying valkeyExec on pod %s (attempt %d/%d, backoff %v)", podName, attempt, maxAttempts, delay)
			time.Sleep(delay)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		cliArgs := []string{
			"exec", podName,
			"-n", namespace,
			"--", "valkey-cli",
			"--raw",
			"-p", fmt.Sprintf("%d", port),
		}
		cliArgs = append(cliArgs, args...)

		cmd := exec.CommandContext(ctx, "kubectl", cliArgs...)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		err := cmd.Run()
		cancel()

		if err == nil {
			return strings.TrimSpace(stdout.String())
		}
		lastErr = fmt.Errorf("kubectl exec failed for pod %s: %w (stderr: %s)", podName, err, stderr.String())
	}

	require.NoError(t, lastErr, "valkeyExec failed after %d attempts for pod %s", maxAttempts, podName)
	return "" // unreachable
}

// valkeyMSET writes multiple key/value pairs to Valkey via a single MSET command.
// This is significantly faster than calling valkeyExec separately for each key
// because it only spawns one kubectl exec subprocess.
func (tc *testClients) valkeyMSET(t *testing.T, namespace, podName string, port int, data map[string]string) {
	t.Helper()
	args := make([]string, 0, 1+len(data)*2)
	args = append(args, "MSET")
	for k, v := range data {
		args = append(args, k, v)
	}
	resp := tc.valkeyExec(t, namespace, podName, port, args...)
	require.Equal(t, "OK", resp, "MSET should succeed on pod %s", podName)
}

// waitForPodReady waits until a specific pod is in Ready condition.
func (tc *testClients) waitForPodReady(t *testing.T, namespace, name string) {
	t.Helper()
	ctx := context.Background()

	err := wait.PollUntilContextTimeout(ctx, pollInterval, testTimeout, true, func(ctx context.Context) (bool, error) {
		pod, err := tc.kube.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				return false, nil
			}
			return false, err
		}
		for _, cond := range pod.Status.Conditions {
			if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
				return true, nil
			}
		}
		return false, nil
	})
	require.NoError(t, err, "Pod %s/%s did not become ready", namespace, name)
}

// getStatefulSet retrieves a StatefulSet.
func (tc *testClients) getStatefulSet(t *testing.T, namespace, name string) *appsv1.StatefulSet {
	t.Helper()
	ctx := context.Background()

	sts, err := tc.kube.AppsV1().StatefulSets(namespace).Get(ctx, name, metav1.GetOptions{})
	require.NoError(t, err, "Failed to get StatefulSet %s/%s", namespace, name)
	return sts
}

// getService retrieves a Service.
func (tc *testClients) getService(t *testing.T, namespace, name string) *corev1.Service {
	t.Helper()
	ctx := context.Background()

	svc, err := tc.kube.CoreV1().Services(namespace).Get(ctx, name, metav1.GetOptions{})
	require.NoError(t, err, "Failed to get Service %s/%s", namespace, name)
	return svc
}

// tryGetService attempts to retrieve a Service and returns the service and any error.
// Unlike getService it does not fail the test on error, allowing callers to assert absence.
func (tc *testClients) tryGetService(t *testing.T, namespace, name string) (*corev1.Service, error) {
	t.Helper()
	ctx := context.Background()
	return tc.kube.CoreV1().Services(namespace).Get(ctx, name, metav1.GetOptions{})
}

// getConfigMap retrieves a ConfigMap.
func (tc *testClients) getConfigMap(t *testing.T, namespace, name string) *corev1.ConfigMap {
	t.Helper()
	ctx := context.Background()

	cm, err := tc.kube.CoreV1().ConfigMaps(namespace).Get(ctx, name, metav1.GetOptions{})
	require.NoError(t, err, "Failed to get ConfigMap %s/%s", namespace, name)
	return cm
}

// deleteValkey deletes a Valkey CR.
func (tc *testClients) deleteValkey(t *testing.T, namespace, name string) {
	t.Helper()
	ctx := context.Background()

	err := tc.dynamic.Resource(valkeyGVR).Namespace(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if !apierrors.IsNotFound(err) {
		require.NoError(t, err, "Failed to delete Valkey CR %s/%s", namespace, name)
	}
}

// waitForDeletion waits until a Valkey CR and its owned resources are deleted.
func (tc *testClients) waitForDeletion(t *testing.T, namespace, name string) {
	t.Helper()
	ctx := context.Background()

	err := wait.PollUntilContextTimeout(ctx, pollInterval, testTimeout, true, func(ctx context.Context) (bool, error) {
		_, err := tc.dynamic.Resource(valkeyGVR).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return true, nil
		}
		return false, err
	})
	require.NoError(t, err, "Valkey CR %s/%s was not deleted", namespace, name)
}

// buildValkeyObject constructs an unstructured Valkey CR for use in e2e tests.
func buildValkeyObject(name, namespace string, spec map[string]interface{}) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "vko.gtrfc.com/v1",
			"kind":       "Valkey",
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": namespace,
			},
			"spec": spec,
		},
	}
}

// waitForServiceEndpoints waits until a Service has at least one endpoint with the expected port.
func (tc *testClients) waitForServiceEndpoints(t *testing.T, namespace, name string) {
	t.Helper()
	ctx := context.Background()

	err := wait.PollUntilContextTimeout(ctx, pollInterval, testTimeout, true, func(ctx context.Context) (bool, error) {
		ep, err := tc.kube.CoreV1().Endpoints(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				return false, nil
			}
			return false, err
		}
		for _, subset := range ep.Subsets {
			if len(subset.Addresses) > 0 {
				return true, nil
			}
		}
		return false, nil
	})
	require.NoError(t, err, "Service %s/%s did not get endpoints", namespace, name)
}

// waitForConnectedReplicas waits until the master pod has the expected number of
// connected replicas with replication sync complete.
// This replaces unreliable time.Sleep for replication readiness.
func (tc *testClients) waitForConnectedReplicas(t *testing.T, namespace, masterPod string, port, expectedReplicas int) {
	t.Helper()
	expectedStr := fmt.Sprintf("connected_slaves:%d", expectedReplicas)
	// Allow 3 minutes — after a rolling update replicas need time to reconnect and
	// complete the initial replication sync, which can be slow on a loaded Kind cluster.
	require.Eventually(t, func() bool {
		info := tc.valkeyExecAllowError(t, namespace, masterPod, port, "INFO", "replication")
		if !strings.Contains(info, expectedStr) {
			return false
		}
		// Ensure no sync is in progress.
		return !strings.Contains(info, "master_sync_in_progress:1")
	}, 3*time.Minute, 3*time.Second, "Master %s should have %d connected replicas", masterPod, expectedReplicas)
	t.Logf("Replication established: %d replicas connected to %s", expectedReplicas, masterPod)
}

// waitForSentinelSlaves waits until a sentinel instance reports knowing about the
// expected number of slaves for the given Valkey cluster. This must be called after
// waitForConnectedReplicas to ensure sentinel has also re-discovered the topology
// following a sentinel REMOVE+MONITOR reset (which happens at the end of every HA
// rolling update finalization). Without this check, the next rolling update may
// start before sentinel is fully ready, causing a stall in the failover phase.
func (tc *testClients) waitForSentinelSlaves(t *testing.T, namespace, valkeyName string, expectedSlaves int) {
	t.Helper()
	sentinelPod := fmt.Sprintf("%s-sentinel-0", valkeyName)
	require.Eventually(t, func() bool {
		raw := tc.valkeyExecAllowError(t, namespace, sentinelPod, 26379,
			"SENTINEL", "MASTER", valkeyName)
		// SENTINEL MASTER returns alternating key/value lines with --raw.
		// Find "num-slaves" and read the following line as the count.
		lines := strings.Split(raw, "\n")
		for i, line := range lines {
			if strings.TrimSpace(line) == "num-slaves" && i+1 < len(lines) {
				var count int
				_, err := fmt.Sscanf(strings.TrimSpace(lines[i+1]), "%d", &count)
				if err == nil {
					t.Logf("Sentinel %s reports %d slaves for %s (want %d)",
						sentinelPod, count, valkeyName, expectedSlaves)
					return count >= expectedSlaves
				}
			}
		}
		t.Logf("Could not parse num-slaves from sentinel output for %s/%s", namespace, valkeyName)
		return false
	}, 2*time.Minute, 5*time.Second,
		"Sentinel should know about %d slaves for %s/%s", expectedSlaves, namespace, valkeyName)
	t.Logf("Sentinel topology confirmed: %d slaves known for %s", expectedSlaves, valkeyName)
}

// assertLabelExists checks that a specific label exists on a resource's metadata.
func assertLabelExists(t *testing.T, labels map[string]string, key, expectedValue string) {
	t.Helper()
	val, ok := labels[key]
	assert.True(t, ok, "Label %s not found", key)
	assert.Equal(t, expectedValue, val, "Label %s has wrong value", key)
}

// waitForConfigMap waits until a ConfigMap exists.
func (tc *testClients) waitForConfigMap(t *testing.T, namespace, name string) {
	t.Helper()
	ctx := context.Background()

	err := wait.PollUntilContextTimeout(ctx, pollInterval, testTimeout, true, func(ctx context.Context) (bool, error) {
		_, err := tc.kube.CoreV1().ConfigMaps(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				return false, nil
			}
			return false, err
		}
		return true, nil
	})
	require.NoError(t, err, "ConfigMap %s/%s was not created", namespace, name)
}

// getPod retrieves a pod by name.
func (tc *testClients) getPod(t *testing.T, namespace, name string) *corev1.Pod {
	t.Helper()
	ctx := context.Background()

	pod, err := tc.kube.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
	require.NoError(t, err, "Failed to get pod %s/%s", namespace, name)
	return pod
}

// waitForNoPods waits until there are no pods whose names start with the given
// prefix in the namespace. This is used to verify that deletion cleans up all
// Valkey and Sentinel pods without a reboot loop.
func (tc *testClients) waitForNoPods(t *testing.T, namespace, namePrefix string) {
	t.Helper()
	ctx := context.Background()

	err := wait.PollUntilContextTimeout(ctx, pollInterval, testTimeout, true, func(ctx context.Context) (bool, error) {
		pods, err := tc.kube.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return false, err
		}
		for _, pod := range pods.Items {
			if strings.HasPrefix(pod.Name, namePrefix) {
				t.Logf("Pod %s/%s still present (phase=%s), waiting...", namespace, pod.Name, pod.Status.Phase)
				return false, nil
			}
		}
		return true, nil
	})
	require.NoError(t, err, "Pods with prefix %q in namespace %s were not cleaned up after deletion", namePrefix, namespace)
}

// Ensure all types used are available for linting.
var _ = types.NamespacedName{}
