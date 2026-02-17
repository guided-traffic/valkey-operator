//go:build e2e

// Package e2e provides end-to-end tests for the valkey-operator.
// These tests run against a real Kubernetes cluster (typically Kind) with the
// operator deployed via Helm. They verify the full lifecycle of Valkey
// standalone and HA clusters, including data replication.
package e2e

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
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

// pollInterval is the interval between polling attempts.
const pollInterval = 2 * time.Second

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
func (tc *testClients) waitForValkeyPhase(t *testing.T, namespace, name, expectedPhase string) {
	t.Helper()
	ctx := context.Background()

	err := wait.PollUntilContextTimeout(ctx, pollInterval, testTimeout, true, func(ctx context.Context) (bool, error) {
		valkey, err := tc.dynamic.Resource(valkeyGVR).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return false, err
		}

		phase, found, err := unstructured.NestedString(valkey.Object, "status", "phase")
		if err != nil || !found {
			return false, nil
		}
		t.Logf("Valkey %s phase: %s (want: %s)", name, phase, expectedPhase)
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

// getPodIP returns the IP of a pod.
func (tc *testClients) getPodIP(t *testing.T, namespace, name string) string {
	t.Helper()
	ctx := context.Background()

	pod, err := tc.kube.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
	require.NoError(t, err, "Failed to get pod %s/%s", namespace, name)
	require.NotEmpty(t, pod.Status.PodIP, "Pod %s has no IP yet", name)
	return pod.Status.PodIP
}

// valkeyExec executes a Valkey command via a RESP connection to a pod.
// Returns the response string.
func (tc *testClients) valkeyExec(t *testing.T, namespace, podName string, port int, args ...string) string {
	t.Helper()
	ctx := context.Background()

	// Get the pod IP directly.
	ip := tc.getPodIP(t, namespace, podName)
	addr := fmt.Sprintf("%s:%d", ip, port)

	return valkeyCommand(t, ctx, addr, args...)
}

// valkeyCommand sends a RESP command to a Valkey server and returns the response.
func valkeyCommand(t *testing.T, ctx context.Context, addr string, args ...string) string {
	t.Helper()

	dialer := net.Dialer{Timeout: 5 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	require.NoError(t, err, "Failed to connect to Valkey at %s", addr)
	defer conn.Close()

	// Set a deadline for the entire operation.
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))

	// Send RESP command.
	cmd := formatRESP(args)
	_, err = conn.Write([]byte(cmd))
	require.NoError(t, err, "Failed to write command to Valkey")

	// Read response.
	reader := bufio.NewReader(conn)
	response, err := readRESPResponse(reader)
	require.NoError(t, err, "Failed to read response from Valkey")

	return response
}

// formatRESP formats a command as a RESP array.
func formatRESP(args []string) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("*%d\r\n", len(args)))
	for _, arg := range args {
		sb.WriteString(fmt.Sprintf("$%d\r\n%s\r\n", len(arg), arg))
	}
	return sb.String()
}

// readRESPResponse reads a full RESP response from a buffered reader.
func readRESPResponse(reader *bufio.Reader) (string, error) {
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("reading response: %w", err)
	}
	line = strings.TrimRight(line, "\r\n")

	switch {
	case strings.HasPrefix(line, "+"):
		// Simple string.
		return line[1:], nil
	case strings.HasPrefix(line, "-"):
		// Error.
		return "", fmt.Errorf("valkey error: %s", line[1:])
	case strings.HasPrefix(line, ":"):
		// Integer.
		return line[1:], nil
	case strings.HasPrefix(line, "$"):
		// Bulk string.
		length := 0
		_, err := fmt.Sscanf(line, "$%d", &length)
		if err != nil {
			return "", fmt.Errorf("parsing bulk string length: %w", err)
		}
		if length == -1 {
			return "(nil)", nil
		}
		data := make([]byte, length+2) // +2 for \r\n
		_, err = reader.Read(data)
		if err != nil {
			return "", fmt.Errorf("reading bulk string data: %w", err)
		}
		return string(data[:length]), nil
	case strings.HasPrefix(line, "*"):
		// Array — read all elements and join with newlines.
		count := 0
		_, err := fmt.Sscanf(line, "*%d", &count)
		if err != nil {
			return "", fmt.Errorf("parsing array count: %w", err)
		}
		if count == -1 {
			return "(nil)", nil
		}
		var parts []string
		for i := 0; i < count; i++ {
			elem, err := readRESPResponse(reader)
			if err != nil {
				return "", fmt.Errorf("reading array element %d: %w", i, err)
			}
			parts = append(parts, elem)
		}
		return strings.Join(parts, "\n"), nil
	default:
		return line, nil
	}
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

// Ensure all types used are available for linting.
var _ = types.NamespacedName{}
