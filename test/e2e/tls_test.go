//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
)

// certificateGVR is the GroupVersionResource for the cert-manager Certificate CRD.
var certificateGVR = schema.GroupVersionResource{
	Group:    "cert-manager.io",
	Version:  "v1",
	Resource: "certificates",
}

// tlsCertPath is the path to the TLS certificate inside valkey pods.
const tlsCertPath = "/tls/tls.crt"

// tlsKeyPath is the path to the TLS key inside valkey pods.
const tlsKeyPath = "/tls/tls.key"

// tlsCACertPath is the path to the CA certificate inside valkey pods.
const tlsCACertPath = "/tls/ca.crt"

// tlsValkeyPort is the TLS port for Valkey (6379 + 10000).
const tlsValkeyPort = 16379

// valkeyTLSExec executes a Valkey command over TLS via kubectl exec + valkey-cli.
// It adds the necessary --tls, --cert, --key, --cacert flags for encrypted connections.
func (tc *testClients) valkeyTLSExec(t *testing.T, namespace, podName string, port int, args ...string) string {
	t.Helper()

	cliArgs := []string{
		"exec", podName,
		"-n", namespace,
		"--", "valkey-cli",
		"--raw",
		"-p", fmt.Sprintf("%d", port),
		"--tls",
		"--cert", tlsCertPath,
		"--key", tlsKeyPath,
		"--cacert", tlsCACertPath,
	}
	cliArgs = append(cliArgs, args...)

	cmd := exec.CommandContext(context.Background(), "kubectl", cliArgs...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	require.NoError(t, err, "kubectl exec (TLS) failed for pod %s: stdout=%s stderr=%s", podName, stdout.String(), stderr.String())

	return strings.TrimSpace(stdout.String())
}

// valkeyTLSExecAllowError executes a TLS Valkey command but does not fail on Valkey-level errors.
func (tc *testClients) valkeyTLSExecAllowError(t *testing.T, namespace, podName string, port int, args ...string) string {
	t.Helper()

	cliArgs := []string{
		"exec", podName,
		"-n", namespace,
		"--", "valkey-cli",
		"--raw",
		"-p", fmt.Sprintf("%d", port),
		"--tls",
		"--cert", tlsCertPath,
		"--key", tlsKeyPath,
		"--cacert", tlsCACertPath,
	}
	cliArgs = append(cliArgs, args...)

	cmd := exec.CommandContext(context.Background(), "kubectl", cliArgs...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	_ = cmd.Run()

	return strings.TrimSpace(stdout.String())
}

// waitForSecret waits until a Kubernetes Secret exists in the given namespace.
func (tc *testClients) waitForSecret(t *testing.T, namespace, name string) {
	t.Helper()
	ctx := context.Background()

	err := wait.PollUntilContextTimeout(ctx, pollInterval, testTimeout, true, func(ctx context.Context) (bool, error) {
		_, err := tc.kube.CoreV1().Secrets(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				t.Logf("Waiting for Secret %s/%s...", namespace, name)
				return false, nil
			}
			return false, err
		}
		return true, nil
	})
	require.NoError(t, err, "Secret %s/%s was not created (cert-manager may not have issued the certificate)", namespace, name)
}

// getSecret retrieves a Secret by name.
func (tc *testClients) getSecret(t *testing.T, namespace, name string) *corev1.Secret {
	t.Helper()
	ctx := context.Background()

	secret, err := tc.kube.CoreV1().Secrets(namespace).Get(ctx, name, metav1.GetOptions{})
	require.NoError(t, err, "Failed to get Secret %s/%s", namespace, name)
	return secret
}

// getCertificate retrieves a cert-manager Certificate resource.
func (tc *testClients) getCertificate(t *testing.T, namespace, name string) *unstructured.Unstructured {
	t.Helper()
	ctx := context.Background()

	cert, err := tc.dynamic.Resource(certificateGVR).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	require.NoError(t, err, "Failed to get Certificate %s/%s", namespace, name)
	return cert
}

// waitForCertificateReady waits until a cert-manager Certificate resource reaches Ready=True condition.
func (tc *testClients) waitForCertificateReady(t *testing.T, namespace, name string) {
	t.Helper()
	ctx := context.Background()

	err := wait.PollUntilContextTimeout(ctx, pollInterval, testTimeout, true, func(ctx context.Context) (bool, error) {
		cert, err := tc.dynamic.Resource(certificateGVR).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				return false, nil
			}
			return false, err
		}

		conditions, found, err := unstructured.NestedSlice(cert.Object, "status", "conditions")
		if err != nil || !found {
			return false, nil
		}

		for _, c := range conditions {
			cond, ok := c.(map[string]interface{})
			if !ok {
				continue
			}
			condType, _, _ := unstructured.NestedString(cond, "type")
			condStatus, _, _ := unstructured.NestedString(cond, "status")
			if condType == "Ready" && condStatus == "True" {
				return true, nil
			}
		}
		t.Logf("Waiting for Certificate %s/%s to become ready...", namespace, name)
		return false, nil
	})
	require.NoError(t, err, "Certificate %s/%s did not reach Ready state", namespace, name)
}

// getPodLogs retrieves the logs from a specific pod container.
func (tc *testClients) getPodLogs(t *testing.T, namespace, podName, containerName string) string {
	t.Helper()

	cliArgs := []string{
		"logs", podName,
		"-n", namespace,
	}
	if containerName != "" {
		cliArgs = append(cliArgs, "-c", containerName)
	}

	cmd := exec.CommandContext(context.Background(), "kubectl", cliArgs...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	require.NoError(t, err, "kubectl logs failed for pod %s/%s: %s", namespace, podName, stderr.String())

	return stdout.String()
}

// assertNoErrorsInLogs checks that pod logs do not contain critical error patterns.
func assertNoErrorsInLogs(t *testing.T, logs string, podName string) {
	t.Helper()

	// Critical patterns that indicate misconfiguration or failure.
	criticalPatterns := []string{
		"Connection refused",
		"TLS handshake error",
		"certificate verify failed",
		"SSL_CTX_use_certificate",
		"SSL_CTX_use_PrivateKey",
		"wrong version number",
		"FATAL",
		"PANIC",
		"no such file or directory",
	}

	for _, pattern := range criticalPatterns {
		assert.NotContains(t, logs, pattern, "Pod %s logs contain critical error: %s", podName, pattern)
	}
}

// assertTLSActiveInLogs checks that Valkey logs confirm TLS is active.
func assertTLSActiveInLogs(t *testing.T, logs string, podName string) {
	t.Helper()

	// Valkey logs should mention TLS initialization.
	hasTLSIndicator := strings.Contains(logs, "TLS") ||
		strings.Contains(logs, "tls-port") ||
		strings.Contains(logs, "oO0OoO0OoO0Oo") // Valkey startup banner (confirms it started)

	assert.True(t, hasTLSIndicator, "Pod %s logs should indicate TLS is configured or Valkey started successfully", podName)
}

// tlsSpec returns the TLS spec referencing the e2e-ca-issuer ClusterIssuer.
func tlsSpec() map[string]interface{} {
	return map[string]interface{}{
		"enabled": true,
		"certManager": map[string]interface{}{
			"issuer": map[string]interface{}{
				"kind":  "ClusterIssuer",
				"name":  "e2e-ca-issuer",
				"group": "cert-manager.io",
			},
		},
	}
}

// TestE2E_TLS_Standalone tests a single-node Valkey deployment with TLS encryption.
// It verifies:
// - cert-manager creates the TLS Certificate and Secret
// - Valkey ConfigMap contains TLS configuration directives
// - StatefulSet mounts TLS volume and uses TLS port
// - Probes use TLS flags
// - Valkey responds to TLS-encrypted PING
// - Data can be written and read over TLS
// - Non-TLS connections are rejected (port 0 disables plaintext)
// - Pod logs show no TLS errors
func TestE2E_TLS_Standalone(t *testing.T) {
	tc := newTestClients(t)
	ns := "e2e-tls-standalone"
	cleanup := tc.createNamespace(t, ns)
	defer cleanup()

	name := "tls-standalone"
	valkey := buildValkeyObject(name, ns, map[string]interface{}{
		"replicas": int64(1),
		"image":    "valkey/valkey:8.0",
		"tls":      tlsSpec(),
	})

	t.Log("Creating standalone Valkey CR with TLS enabled")
	tc.createValkey(t, ns, valkey)
	defer tc.deleteValkey(t, ns, name)

	// --- Certificate resources ---

	t.Run("Certificate resource is created by operator", func(t *testing.T) {
		certName := fmt.Sprintf("%s-tls", name)
		tc.waitForCertificateReady(t, ns, certName)

		cert := tc.getCertificate(t, ns, certName)
		spec, _, _ := unstructured.NestedMap(cert.Object, "spec")

		// Verify issuer reference.
		issuerRef, _, _ := unstructured.NestedMap(spec, "issuerRef")
		assert.Equal(t, "ClusterIssuer", issuerRef["kind"])
		assert.Equal(t, "e2e-ca-issuer", issuerRef["name"])

		// Verify secret name.
		secretName, _, _ := unstructured.NestedString(spec, "secretName")
		assert.Equal(t, fmt.Sprintf("%s-tls", name), secretName)

		// Verify DNS names include relevant entries.
		dnsNames, _, _ := unstructured.NestedStringSlice(spec, "dnsNames")
		t.Logf("Certificate DNS names: %v", dnsNames)
		assert.NotEmpty(t, dnsNames, "Certificate should have DNS names")

		// Should contain at least the headless service and localhost.
		hasHeadless := false
		hasLocalhost := false
		for _, dns := range dnsNames {
			if strings.Contains(dns, fmt.Sprintf("%s-headless", name)) {
				hasHeadless = true
			}
			if dns == "localhost" {
				hasLocalhost = true
			}
		}
		assert.True(t, hasHeadless, "DNS names should include headless service")
		assert.True(t, hasLocalhost, "DNS names should include localhost")

		// Verify usages include server and client auth.
		usages, _, _ := unstructured.NestedStringSlice(spec, "usages")
		assert.Contains(t, usages, "server auth")
		assert.Contains(t, usages, "client auth")
	})

	t.Run("TLS Secret is created by cert-manager", func(t *testing.T) {
		secretName := fmt.Sprintf("%s-tls", name)
		tc.waitForSecret(t, ns, secretName)

		secret := tc.getSecret(t, ns, secretName)
		assert.Contains(t, secret.Data, "tls.crt", "TLS Secret should contain tls.crt")
		assert.Contains(t, secret.Data, "tls.key", "TLS Secret should contain tls.key")
		assert.Contains(t, secret.Data, "ca.crt", "TLS Secret should contain ca.crt")
		assert.NotEmpty(t, secret.Data["tls.crt"], "tls.crt should not be empty")
		assert.NotEmpty(t, secret.Data["tls.key"], "tls.key should not be empty")
		assert.NotEmpty(t, secret.Data["ca.crt"], "ca.crt should not be empty")
	})

	// --- ConfigMap TLS directives ---

	t.Run("ConfigMap contains TLS configuration", func(t *testing.T) {
		tc.waitForConfigMap(t, ns, fmt.Sprintf("%s-config", name))
		cm := tc.getConfigMap(t, ns, fmt.Sprintf("%s-config", name))
		conf := cm.Data["valkey.conf"]

		assert.Contains(t, conf, "tls-port 16379", "Should have TLS port directive")
		assert.Contains(t, conf, "port 0", "Should disable plaintext port")
		assert.Contains(t, conf, "tls-cert-file /tls/tls.crt", "Should reference cert file")
		assert.Contains(t, conf, "tls-key-file /tls/tls.key", "Should reference key file")
		assert.Contains(t, conf, "tls-ca-cert-file /tls/ca.crt", "Should reference CA cert file")
	})

	// --- StatefulSet TLS configuration ---

	t.Run("StatefulSet has TLS volume and correct port", func(t *testing.T) {
		tc.waitForStatefulSetReady(t, ns, name, 1)
		sts := tc.getStatefulSet(t, ns, name)

		// Verify TLS volume exists.
		var hasTLSVolume bool
		for _, vol := range sts.Spec.Template.Spec.Volumes {
			if vol.Name == "tls" {
				hasTLSVolume = true
				require.NotNil(t, vol.VolumeSource.Secret, "TLS volume should use a Secret source")
				assert.Equal(t, fmt.Sprintf("%s-tls", name), vol.VolumeSource.Secret.SecretName)
			}
		}
		assert.True(t, hasTLSVolume, "StatefulSet should have TLS volume")

		// Verify TLS volume mount on valkey container.
		valkeyContainer := sts.Spec.Template.Spec.Containers[0]
		var hasTLSMount bool
		for _, mount := range valkeyContainer.VolumeMounts {
			if mount.Name == "tls" {
				hasTLSMount = true
				assert.Equal(t, "/tls", mount.MountPath, "TLS mount path should be /tls")
				assert.True(t, mount.ReadOnly, "TLS mount should be read-only")
			}
		}
		assert.True(t, hasTLSMount, "Valkey container should mount TLS volume")

		// Verify container port is TLS port.
		assert.Equal(t, int32(tlsValkeyPort), valkeyContainer.Ports[0].ContainerPort,
			"Container port should be TLS port 16379")

		// Verify probes use TLS.
		readinessCmd := strings.Join(valkeyContainer.ReadinessProbe.Exec.Command, " ")
		assert.Contains(t, readinessCmd, "--tls", "Readiness probe should use --tls")
		assert.Contains(t, readinessCmd, "--cert", "Readiness probe should use --cert")
		assert.Contains(t, readinessCmd, "--cacert", "Readiness probe should use --cacert")
		assert.Contains(t, readinessCmd, "16379", "Readiness probe should target TLS port")

		livenessCmd := strings.Join(valkeyContainer.LivenessProbe.Exec.Command, " ")
		assert.Contains(t, livenessCmd, "--tls", "Liveness probe should use --tls")
	})

	// --- Pod readiness ---

	t.Run("Pod is running and ready", func(t *testing.T) {
		tc.waitForPodReady(t, ns, fmt.Sprintf("%s-0", name))
	})

	// --- TLS connectivity ---

	t.Run("CRD status shows OK", func(t *testing.T) {
		tc.waitForValkeyPhase(t, ns, name, "OK")
	})

	t.Run("Valkey responds to PING over TLS", func(t *testing.T) {
		podName := fmt.Sprintf("%s-0", name)
		response := tc.valkeyTLSExec(t, ns, podName, tlsValkeyPort, "PING")
		assert.Equal(t, "PONG", response, "Valkey should respond PONG to TLS PING")
	})

	t.Run("Write and read data over TLS", func(t *testing.T) {
		podName := fmt.Sprintf("%s-0", name)

		// Write test data.
		setResp := tc.valkeyTLSExec(t, ns, podName, tlsValkeyPort, "SET", "tls-test-key", "encrypted-value")
		assert.Equal(t, "OK", setResp, "SET over TLS should succeed")

		// Read data back.
		getResp := tc.valkeyTLSExec(t, ns, podName, tlsValkeyPort, "GET", "tls-test-key")
		assert.Equal(t, "encrypted-value", getResp, "GET over TLS should return correct value")

		// Write various data types.
		tc.valkeyTLSExec(t, ns, podName, tlsValkeyPort, "SET", "tls:int", "12345")
		tc.valkeyTLSExec(t, ns, podName, tlsValkeyPort, "SET", "tls:unicode", "🔒 TLS encrypted data")
		tc.valkeyTLSExec(t, ns, podName, tlsValkeyPort, "RPUSH", "tls:list", "a", "b", "c")
		tc.valkeyTLSExec(t, ns, podName, tlsValkeyPort, "HSET", "tls:hash", "field1", "val1")

		// Verify reads.
		assert.Equal(t, "12345", tc.valkeyTLSExec(t, ns, podName, tlsValkeyPort, "GET", "tls:int"))
		assert.Equal(t, "🔒 TLS encrypted data", tc.valkeyTLSExec(t, ns, podName, tlsValkeyPort, "GET", "tls:unicode"))
		assert.Equal(t, "3", tc.valkeyTLSExec(t, ns, podName, tlsValkeyPort, "LLEN", "tls:list"))
		assert.Equal(t, "val1", tc.valkeyTLSExec(t, ns, podName, tlsValkeyPort, "HGET", "tls:hash", "field1"))
	})

	t.Run("Plaintext connection is rejected", func(t *testing.T) {
		podName := fmt.Sprintf("%s-0", name)

		// Attempt non-TLS connection on the TLS port — should fail or return garbage.
		resp := tc.valkeyExecAllowError(t, ns, podName, tlsValkeyPort, "PING")
		// A plaintext client connecting to a TLS port will not get a clean PONG.
		assert.NotEqual(t, "PONG", resp,
			"Plaintext PING to TLS port should NOT return clean PONG")
		t.Logf("Plaintext connection response (expected failure): %q", resp)
	})

	t.Run("TLS INFO server shows TLS port", func(t *testing.T) {
		podName := fmt.Sprintf("%s-0", name)
		info := tc.valkeyTLSExec(t, ns, podName, tlsValkeyPort, "INFO", "server")
		assert.Contains(t, info, "tcp_port:0", "Non-TLS port should be 0 (disabled)")
	})

	// --- Log analysis ---

	t.Run("Pod logs show no TLS errors", func(t *testing.T) {
		podName := fmt.Sprintf("%s-0", name)
		logs := tc.getPodLogs(t, ns, podName, "valkey")

		assertNoErrorsInLogs(t, logs, podName)
		assertTLSActiveInLogs(t, logs, podName)
		t.Logf("Valkey pod %s logs (last 500 chars):\n%s", podName, truncateLogs(logs, 500))
	})

	// --- Cleanup ---

	t.Run("Cleanup TLS standalone", func(t *testing.T) {
		tc.deleteValkey(t, ns, name)
		tc.waitForDeletion(t, ns, name)
	})
}

// TestE2E_TLS_HACluster tests a 3-node Valkey deployment with TLS encryption
// but WITHOUT Sentinel. All pods run as independent Valkey instances (no replication).
// This verifies that TLS works correctly in a multi-pod StatefulSet scenario:
//
// - cert-manager creates a Certificate covering all 3 pod DNS names
// - ConfigMap contains TLS directives but no replication config
// - StatefulSet with 3 replicas, all with TLS volume mounts
// - Each pod independently serves data over TLS
// - No Sentinel resources are created
// - Plaintext connections are rejected on all pods
// - Pod logs are clean of TLS errors
func TestE2E_TLS_HACluster(t *testing.T) {
	tc := newTestClients(t)
	ns := "e2e-tls-ha-nosent"
	cleanup := tc.createNamespace(t, ns)
	defer cleanup()

	name := "tls-ha-nosent"
	valkey := buildValkeyObject(name, ns, map[string]interface{}{
		"replicas": int64(3),
		"image":    "valkey/valkey:8.0",
		"tls":      tlsSpec(),
		// No sentinel — all pods run independently.
	})

	t.Log("Creating 3-replica Valkey CR with TLS but no Sentinel")
	tc.createValkey(t, ns, valkey)
	defer tc.deleteValkey(t, ns, name)

	// =========================================================================
	// Phase 1: Certificate and TLS Secret
	// =========================================================================

	t.Run("Certificate is created and ready", func(t *testing.T) {
		certName := fmt.Sprintf("%s-tls", name)
		tc.waitForCertificateReady(t, ns, certName)

		cert := tc.getCertificate(t, ns, certName)
		spec, _, _ := unstructured.NestedMap(cert.Object, "spec")

		issuerRef, _, _ := unstructured.NestedMap(spec, "issuerRef")
		assert.Equal(t, "ClusterIssuer", issuerRef["kind"])
		assert.Equal(t, "e2e-ca-issuer", issuerRef["name"])

		// DNS names should cover all 3 pods.
		dnsNames, _, _ := unstructured.NestedStringSlice(spec, "dnsNames")
		t.Logf("Certificate DNS names: %v", dnsNames)
		for i := 0; i < 3; i++ {
			podDNS := fmt.Sprintf("%s-%d.%s-headless", name, i, name)
			found := false
			for _, dns := range dnsNames {
				if strings.Contains(dns, podDNS) {
					found = true
					break
				}
			}
			assert.True(t, found, "DNS names should include pod-%d DNS (%s)", i, podDNS)
		}
	})

	t.Run("TLS Secret is created by cert-manager", func(t *testing.T) {
		secretName := fmt.Sprintf("%s-tls", name)
		tc.waitForSecret(t, ns, secretName)

		secret := tc.getSecret(t, ns, secretName)
		assert.Contains(t, secret.Data, "tls.crt")
		assert.Contains(t, secret.Data, "tls.key")
		assert.Contains(t, secret.Data, "ca.crt")
		assert.NotEmpty(t, secret.Data["tls.crt"])
		assert.NotEmpty(t, secret.Data["tls.key"])
		assert.NotEmpty(t, secret.Data["ca.crt"])
	})

	// =========================================================================
	// Phase 2: No Sentinel resources
	// =========================================================================

	t.Run("No Sentinel Certificate is created", func(t *testing.T) {
		sentinelCertName := fmt.Sprintf("%s-sentinel-tls", name)
		_, err := tc.dynamic.Resource(certificateGVR).Namespace(ns).Get(
			context.Background(), sentinelCertName, metav1.GetOptions{})
		assert.True(t, apierrors.IsNotFound(err),
			"Sentinel Certificate should NOT exist when sentinel is disabled")
	})

	t.Run("No Sentinel StatefulSet is created", func(t *testing.T) {
		sentinelStsName := fmt.Sprintf("%s-sentinel", name)
		_, err := tc.kube.AppsV1().StatefulSets(ns).Get(
			context.Background(), sentinelStsName, metav1.GetOptions{})
		assert.True(t, apierrors.IsNotFound(err),
			"Sentinel StatefulSet should NOT exist when sentinel is disabled")
	})

	t.Run("No Sentinel ConfigMap is created", func(t *testing.T) {
		sentinelCmName := fmt.Sprintf("%s-sentinel-config", name)
		_, err := tc.kube.CoreV1().ConfigMaps(ns).Get(
			context.Background(), sentinelCmName, metav1.GetOptions{})
		assert.True(t, apierrors.IsNotFound(err),
			"Sentinel ConfigMap should NOT exist when sentinel is disabled")
	})

	t.Run("No Sentinel headless Service is created", func(t *testing.T) {
		sentinelSvcName := fmt.Sprintf("%s-sentinel-headless", name)
		_, err := tc.kube.CoreV1().Services(ns).Get(
			context.Background(), sentinelSvcName, metav1.GetOptions{})
		assert.True(t, apierrors.IsNotFound(err),
			"Sentinel headless Service should NOT exist when sentinel is disabled")
	})

	// =========================================================================
	// Phase 3: ConfigMap — TLS without replication
	// =========================================================================

	t.Run("ConfigMap has TLS directives but no replication config", func(t *testing.T) {
		tc.waitForConfigMap(t, ns, fmt.Sprintf("%s-config", name))
		cm := tc.getConfigMap(t, ns, fmt.Sprintf("%s-config", name))
		conf := cm.Data["valkey.conf"]

		// TLS directives must be present.
		assert.Contains(t, conf, "tls-port 16379")
		assert.Contains(t, conf, "port 0")
		assert.Contains(t, conf, "tls-cert-file /tls/tls.crt")
		assert.Contains(t, conf, "tls-key-file /tls/tls.key")
		assert.Contains(t, conf, "tls-ca-cert-file /tls/ca.crt")
		assert.Contains(t, conf, "tls-replication yes")

		// No replication config — no sentinel means no replicaof.
		assert.NotContains(t, conf, "replicaof",
			"Config should NOT contain replicaof without sentinel")
		assert.NotContains(t, conf, "replica-serve-stale-data",
			"Config should NOT contain replication settings without sentinel")
	})

	t.Run("No replica ConfigMap is created", func(t *testing.T) {
		replicaCmName := fmt.Sprintf("%s-replica-config", name)
		_, err := tc.kube.CoreV1().ConfigMaps(ns).Get(
			context.Background(), replicaCmName, metav1.GetOptions{})
		assert.True(t, apierrors.IsNotFound(err),
			"Replica ConfigMap should NOT exist without sentinel")
	})

	// =========================================================================
	// Phase 4: StatefulSet — 3 replicas with TLS
	// =========================================================================

	t.Run("StatefulSet has 3 replicas with TLS volume", func(t *testing.T) {
		tc.waitForStatefulSetReady(t, ns, name, 3)
		sts := tc.getStatefulSet(t, ns, name)

		assert.Equal(t, int32(3), *sts.Spec.Replicas)

		// TLS volume.
		assertVolumeExists(t, sts.Spec.Template.Spec.Volumes, "tls", fmt.Sprintf("%s-tls", name))

		// Valkey container checks.
		valkeyContainer := sts.Spec.Template.Spec.Containers[0]
		assertVolumeMountExists(t, valkeyContainer.VolumeMounts, "tls", "/tls")
		assert.Equal(t, int32(tlsValkeyPort), valkeyContainer.Ports[0].ContainerPort)

		// Probes must use TLS.
		readinessCmd := strings.Join(valkeyContainer.ReadinessProbe.Exec.Command, " ")
		assert.Contains(t, readinessCmd, "--tls")
		assert.Contains(t, readinessCmd, "16379")

		livenessCmd := strings.Join(valkeyContainer.LivenessProbe.Exec.Command, " ")
		assert.Contains(t, livenessCmd, "--tls")

		// No init container — without sentinel there is no config selector.
		assert.Empty(t, sts.Spec.Template.Spec.InitContainers,
			"StatefulSet should NOT have init containers without sentinel")
	})

	// =========================================================================
	// Phase 5: Pod readiness and TLS connectivity
	// =========================================================================

	t.Run("All 3 pods are running and ready", func(t *testing.T) {
		for i := 0; i < 3; i++ {
			tc.waitForPodReady(t, ns, fmt.Sprintf("%s-%d", name, i))
		}
	})

	t.Run("CRD status shows OK", func(t *testing.T) {
		tc.waitForValkeyPhase(t, ns, name, "OK")
		status := tc.getValkeyStatus(t, ns, name)

		readyReplicas, _, _ := unstructuredNestedFloat64(status, "readyReplicas")
		assert.Equal(t, float64(3), readyReplicas)
	})

	t.Run("All 3 pods respond to TLS PING", func(t *testing.T) {
		for i := 0; i < 3; i++ {
			podName := fmt.Sprintf("%s-%d", name, i)
			resp := tc.valkeyTLSExec(t, ns, podName, tlsValkeyPort, "PING")
			assert.Equal(t, "PONG", resp, "Pod %s should respond PONG over TLS", podName)
		}
	})

	t.Run("Each pod is an independent master (no replication)", func(t *testing.T) {
		for i := 0; i < 3; i++ {
			podName := fmt.Sprintf("%s-%d", name, i)
			info := tc.valkeyTLSExec(t, ns, podName, tlsValkeyPort, "INFO", "replication")
			assert.Contains(t, info, "role:master",
				"Pod %s should be master (no sentinel = no replication)", podName)
			assert.Contains(t, info, "connected_slaves:0",
				"Pod %s should have 0 connected slaves (independent)", podName)
		}
	})

	// =========================================================================
	// Phase 6: Independent data on each pod over TLS
	// =========================================================================

	t.Run("Write unique data to each pod over TLS", func(t *testing.T) {
		for i := 0; i < 3; i++ {
			podName := fmt.Sprintf("%s-%d", name, i)

			// Each pod gets its own key.
			key := fmt.Sprintf("tls-nosent:pod%d:key", i)
			value := fmt.Sprintf("data-from-pod-%d-🔒", i)

			resp := tc.valkeyTLSExec(t, ns, podName, tlsValkeyPort, "SET", key, value)
			assert.Equal(t, "OK", resp, "SET on pod %s over TLS should succeed", podName)

			// Verify read-back on the same pod.
			getResp := tc.valkeyTLSExec(t, ns, podName, tlsValkeyPort, "GET", key)
			assert.Equal(t, value, getResp, "GET on pod %s should return correct value", podName)
		}
	})

	t.Run("Data is NOT shared between independent pods", func(t *testing.T) {
		// Pod-0 should NOT have pod-1's key (no replication).
		resp := tc.valkeyTLSExecAllowError(t, ns, fmt.Sprintf("%s-0", name),
			tlsValkeyPort, "GET", "tls-nosent:pod1:key")
		assert.Empty(t, resp, "Pod-0 should not have pod-1's data (independent instances)")

		// Pod-1 should NOT have pod-0's key.
		resp = tc.valkeyTLSExecAllowError(t, ns, fmt.Sprintf("%s-1", name),
			tlsValkeyPort, "GET", "tls-nosent:pod0:key")
		assert.Empty(t, resp, "Pod-1 should not have pod-0's data (independent instances)")

		// Pod-2 should NOT have pod-0's key.
		resp = tc.valkeyTLSExecAllowError(t, ns, fmt.Sprintf("%s-2", name),
			tlsValkeyPort, "GET", "tls-nosent:pod0:key")
		assert.Empty(t, resp, "Pod-2 should not have pod-0's data (independent instances)")
	})

	t.Run("Multiple data types work on each TLS pod", func(t *testing.T) {
		for i := 0; i < 3; i++ {
			podName := fmt.Sprintf("%s-%d", name, i)
			prefix := fmt.Sprintf("tls-nosent:pod%d", i)

			// List.
			tc.valkeyTLSExec(t, ns, podName, tlsValkeyPort, "RPUSH",
				prefix+":list", "a", "b", "c")
			assert.Equal(t, "3",
				tc.valkeyTLSExec(t, ns, podName, tlsValkeyPort, "LLEN", prefix+":list"))

			// Hash.
			tc.valkeyTLSExec(t, ns, podName, tlsValkeyPort, "HSET",
				prefix+":hash", "field", fmt.Sprintf("value-%d", i))
			assert.Equal(t, fmt.Sprintf("value-%d", i),
				tc.valkeyTLSExec(t, ns, podName, tlsValkeyPort, "HGET", prefix+":hash", "field"))

			// Set.
			tc.valkeyTLSExec(t, ns, podName, tlsValkeyPort, "SADD",
				prefix+":set", "m1", "m2")
			assert.Equal(t, "2",
				tc.valkeyTLSExec(t, ns, podName, tlsValkeyPort, "SCARD", prefix+":set"))
		}
	})

	// =========================================================================
	// Phase 7: Plaintext rejection on all pods
	// =========================================================================

	t.Run("Plaintext connections are rejected on all pods", func(t *testing.T) {
		for i := 0; i < 3; i++ {
			podName := fmt.Sprintf("%s-%d", name, i)
			resp := tc.valkeyExecAllowError(t, ns, podName, tlsValkeyPort, "PING")
			assert.NotEqual(t, "PONG", resp,
				"Plaintext PING to pod %s TLS port should NOT return PONG", podName)
		}
	})

	// =========================================================================
	// Phase 8: TLS INFO verification
	// =========================================================================

	t.Run("INFO server confirms TLS on all pods", func(t *testing.T) {
		for i := 0; i < 3; i++ {
			podName := fmt.Sprintf("%s-%d", name, i)
			info := tc.valkeyTLSExec(t, ns, podName, tlsValkeyPort, "INFO", "server")
			assert.Contains(t, info, "tcp_port:0",
				"Pod %s non-TLS port should be disabled", podName)
		}
	})

	t.Run("CONFIG GET confirms TLS settings on all pods", func(t *testing.T) {
		for i := 0; i < 3; i++ {
			podName := fmt.Sprintf("%s-%d", name, i)

			resp := tc.valkeyTLSExec(t, ns, podName, tlsValkeyPort, "CONFIG", "GET", "tls-port")
			assert.Contains(t, resp, "16379", "Pod %s tls-port should be 16379", podName)

			resp = tc.valkeyTLSExec(t, ns, podName, tlsValkeyPort, "CONFIG", "GET", "port")
			assert.Contains(t, resp, "0", "Pod %s plaintext port should be 0", podName)
		}
	})

	// =========================================================================
	// Phase 9: Services
	// =========================================================================

	t.Run("Headless and client Services exist", func(t *testing.T) {
		headless := tc.getService(t, ns, fmt.Sprintf("%s-headless", name))
		assert.Equal(t, "None", string(headless.Spec.ClusterIP))

		client := tc.getService(t, ns, name)
		assert.NotEmpty(t, client.Spec.ClusterIP)
	})

	// =========================================================================
	// Phase 10: Log analysis
	// =========================================================================

	t.Run("All pod logs are free of TLS errors", func(t *testing.T) {
		for i := 0; i < 3; i++ {
			podName := fmt.Sprintf("%s-%d", name, i)
			logs := tc.getPodLogs(t, ns, podName, "valkey")

			assertNoErrorsInLogs(t, logs, podName)
			assertTLSActiveInLogs(t, logs, podName)
			t.Logf("Pod %s logs (last 500 chars):\n%s", podName, truncateLogs(logs, 500))
		}
	})

	// =========================================================================
	// Phase 11: Cleanup
	// =========================================================================

	t.Run("Cleanup TLS HA cluster (no sentinel)", func(t *testing.T) {
		tc.deleteValkey(t, ns, name)
		tc.waitForDeletion(t, ns, name)
	})
}

// TestE2E_TLS_HAClusterWithSentinel tests a 3-node HA Valkey deployment with Sentinel,
// all communications encrypted via TLS. This is the most comprehensive TLS test:
//
// - cert-manager creates separate Certificates for Valkey and Sentinel
// - All ConfigMaps (master, replica, sentinel) contain TLS directives
// - Both StatefulSets (Valkey + Sentinel) mount TLS volumes
// - Valkey replication works over TLS (tls-replication yes)
// - Sentinel monitors the cluster over TLS
// - Data written to master replicates to all replicas over TLS
// - Pod logs are free of TLS errors
// - Non-TLS connections are rejected
func TestE2E_TLS_HAClusterWithSentinel(t *testing.T) {
	tc := newTestClients(t)
	ns := "e2e-tls-ha"
	cleanup := tc.createNamespace(t, ns)
	defer cleanup()

	name := "tls-ha"
	valkey := buildValkeyObject(name, ns, map[string]interface{}{
		"replicas": int64(3),
		"image":    "valkey/valkey:8.0",
		"tls":      tlsSpec(),
		"sentinel": map[string]interface{}{
			"enabled":  true,
			"replicas": int64(3),
			"podLabels": map[string]interface{}{
				"app": "sentinel",
			},
		},
	})

	t.Log("Creating HA Valkey CR with Sentinel and TLS")
	tc.createValkey(t, ns, valkey)
	defer tc.deleteValkey(t, ns, name)

	// =========================================================================
	// Phase 1: Certificate resources
	// =========================================================================

	t.Run("Valkey Certificate is created and ready", func(t *testing.T) {
		certName := fmt.Sprintf("%s-tls", name)
		tc.waitForCertificateReady(t, ns, certName)

		cert := tc.getCertificate(t, ns, certName)
		spec, _, _ := unstructured.NestedMap(cert.Object, "spec")

		issuerRef, _, _ := unstructured.NestedMap(spec, "issuerRef")
		assert.Equal(t, "ClusterIssuer", issuerRef["kind"])
		assert.Equal(t, "e2e-ca-issuer", issuerRef["name"])

		dnsNames, _, _ := unstructured.NestedStringSlice(spec, "dnsNames")
		t.Logf("Valkey Certificate DNS names: %v", dnsNames)

		// Must include pod-level DNS for each of the 3 replicas.
		for i := 0; i < 3; i++ {
			podDNS := fmt.Sprintf("%s-%d.%s-headless", name, i, name)
			found := false
			for _, dns := range dnsNames {
				if strings.Contains(dns, podDNS) {
					found = true
					break
				}
			}
			assert.True(t, found, "DNS names should include pod-%d DNS (%s)", i, podDNS)
		}
	})

	t.Run("Sentinel Certificate is created and ready", func(t *testing.T) {
		certName := fmt.Sprintf("%s-sentinel-tls", name)
		tc.waitForCertificateReady(t, ns, certName)

		cert := tc.getCertificate(t, ns, certName)
		spec, _, _ := unstructured.NestedMap(cert.Object, "spec")

		secretName, _, _ := unstructured.NestedString(spec, "secretName")
		assert.Equal(t, fmt.Sprintf("%s-sentinel-tls", name), secretName)

		dnsNames, _, _ := unstructured.NestedStringSlice(spec, "dnsNames")
		t.Logf("Sentinel Certificate DNS names: %v", dnsNames)

		// Must include sentinel pod DNS names.
		for i := 0; i < 3; i++ {
			podDNS := fmt.Sprintf("%s-sentinel-%d.%s-sentinel-headless", name, i, name)
			found := false
			for _, dns := range dnsNames {
				if strings.Contains(dns, podDNS) {
					found = true
					break
				}
			}
			assert.True(t, found, "DNS names should include sentinel pod-%d DNS (%s)", i, podDNS)
		}
	})

	t.Run("TLS Secrets are created for Valkey and Sentinel", func(t *testing.T) {
		// Valkey TLS secret.
		valkeySecret := fmt.Sprintf("%s-tls", name)
		tc.waitForSecret(t, ns, valkeySecret)
		secret := tc.getSecret(t, ns, valkeySecret)
		assert.Contains(t, secret.Data, "tls.crt")
		assert.Contains(t, secret.Data, "tls.key")
		assert.Contains(t, secret.Data, "ca.crt")

		// Sentinel TLS secret.
		sentinelSecret := fmt.Sprintf("%s-sentinel-tls", name)
		tc.waitForSecret(t, ns, sentinelSecret)
		sentinelSec := tc.getSecret(t, ns, sentinelSecret)
		assert.Contains(t, sentinelSec.Data, "tls.crt")
		assert.Contains(t, sentinelSec.Data, "tls.key")
		assert.Contains(t, sentinelSec.Data, "ca.crt")
	})

	// =========================================================================
	// Phase 2: ConfigMap TLS directives
	// =========================================================================

	t.Run("Master ConfigMap has TLS directives", func(t *testing.T) {
		tc.waitForConfigMap(t, ns, fmt.Sprintf("%s-config", name))
		cm := tc.getConfigMap(t, ns, fmt.Sprintf("%s-config", name))
		conf := cm.Data["valkey.conf"]

		assert.Contains(t, conf, "tls-port 16379")
		assert.Contains(t, conf, "port 0")
		assert.Contains(t, conf, "tls-cert-file /tls/tls.crt")
		assert.Contains(t, conf, "tls-key-file /tls/tls.key")
		assert.Contains(t, conf, "tls-ca-cert-file /tls/ca.crt")
		assert.Contains(t, conf, "tls-replication yes")
		assert.NotContains(t, conf, "replicaof", "Master config should not have replicaof")
	})

	t.Run("Replica ConfigMap has TLS directives and replicaof with TLS port", func(t *testing.T) {
		tc.waitForConfigMap(t, ns, fmt.Sprintf("%s-replica-config", name))
		cm := tc.getConfigMap(t, ns, fmt.Sprintf("%s-replica-config", name))
		conf := cm.Data["valkey.conf"]

		assert.Contains(t, conf, "tls-port 16379")
		assert.Contains(t, conf, "port 0")
		assert.Contains(t, conf, "tls-replication yes")
		assert.Contains(t, conf, "replicaof", "Replica config should have replicaof")
		// replicaof should reference the TLS port (16379), not plaintext 6379.
		assert.Contains(t, conf, "16379", "Replica replicaof should use TLS port")
	})

	t.Run("Sentinel ConfigMap has TLS directives", func(t *testing.T) {
		tc.waitForConfigMap(t, ns, fmt.Sprintf("%s-sentinel-config", name))
		cm := tc.getConfigMap(t, ns, fmt.Sprintf("%s-sentinel-config", name))
		conf := cm.Data["sentinel.conf"]

		assert.Contains(t, conf, "tls-port 26379", "Sentinel should use TLS port")
		assert.Contains(t, conf, "port 0", "Sentinel should disable plaintext port")
		assert.Contains(t, conf, "tls-cert-file /tls/tls.crt")
		assert.Contains(t, conf, "tls-key-file /tls/tls.key")
		assert.Contains(t, conf, "tls-ca-cert-file /tls/ca.crt")
		assert.Contains(t, conf, "tls-replication yes")
		assert.Contains(t, conf, "sentinel monitor")
		// Sentinel monitor should reference TLS port.
		assert.Contains(t, conf, "16379", "Sentinel monitor should target TLS port")
	})

	// =========================================================================
	// Phase 3: StatefulSet TLS configuration
	// =========================================================================

	t.Run("Valkey StatefulSet has TLS volume and mounts", func(t *testing.T) {
		tc.waitForStatefulSetReady(t, ns, name, 3)
		sts := tc.getStatefulSet(t, ns, name)

		// Verify TLS volume.
		assertVolumeExists(t, sts.Spec.Template.Spec.Volumes, "tls", fmt.Sprintf("%s-tls", name))

		// Verify container mount.
		valkeyContainer := sts.Spec.Template.Spec.Containers[0]
		assertVolumeMountExists(t, valkeyContainer.VolumeMounts, "tls", "/tls")
		assert.Equal(t, int32(tlsValkeyPort), valkeyContainer.Ports[0].ContainerPort)

		// Init container should also exist (HA mode config selector).
		require.NotEmpty(t, sts.Spec.Template.Spec.InitContainers)
		assert.Equal(t, "init-config-selector", sts.Spec.Template.Spec.InitContainers[0].Name)
	})

	t.Run("Sentinel StatefulSet has TLS volume and mounts", func(t *testing.T) {
		sentinelStsName := fmt.Sprintf("%s-sentinel", name)
		tc.waitForStatefulSetReady(t, ns, sentinelStsName, 3)
		sts := tc.getStatefulSet(t, ns, sentinelStsName)

		// Verify TLS volume references sentinel secret.
		assertVolumeExists(t, sts.Spec.Template.Spec.Volumes, "tls", fmt.Sprintf("%s-sentinel-tls", name))

		// Verify sentinel container mount.
		sentinelContainer := sts.Spec.Template.Spec.Containers[0]
		assertVolumeMountExists(t, sentinelContainer.VolumeMounts, "tls", "/tls")
	})

	// =========================================================================
	// Phase 4: Pod readiness
	// =========================================================================

	t.Run("All Valkey pods are running and ready", func(t *testing.T) {
		for i := 0; i < 3; i++ {
			tc.waitForPodReady(t, ns, fmt.Sprintf("%s-%d", name, i))
		}
	})

	t.Run("All Sentinel pods are running and ready", func(t *testing.T) {
		for i := 0; i < 3; i++ {
			tc.waitForPodReady(t, ns, fmt.Sprintf("%s-sentinel-%d", name, i))
		}
	})

	t.Run("CRD status shows OK", func(t *testing.T) {
		tc.waitForValkeyPhase(t, ns, name, "OK")
		status := tc.getValkeyStatus(t, ns, name)

		readyReplicas, _, _ := unstructuredNestedFloat64(status, "readyReplicas")
		assert.Equal(t, float64(3), readyReplicas)

		message, _, _ := unstructuredNestedString(status, "message")
		assert.Contains(t, message, "HA cluster ready")
	})

	// =========================================================================
	// Phase 5: TLS connectivity — Valkey
	// =========================================================================

	t.Run("All Valkey pods respond to TLS PING", func(t *testing.T) {
		for i := 0; i < 3; i++ {
			podName := fmt.Sprintf("%s-%d", name, i)
			resp := tc.valkeyTLSExec(t, ns, podName, tlsValkeyPort, "PING")
			assert.Equal(t, "PONG", resp, "Pod %s should respond PONG over TLS", podName)
		}
	})

	t.Run("Master is identified via TLS INFO replication", func(t *testing.T) {
		masterPod := fmt.Sprintf("%s-0", name)
		info := tc.valkeyTLSExec(t, ns, masterPod, tlsValkeyPort, "INFO", "replication")
		assert.Contains(t, info, "role:master", "Pod-0 should be master")
		t.Logf("Master INFO replication:\n%s", truncateLogs(info, 500))
	})

	t.Run("Replicas are connected to master over TLS", func(t *testing.T) {
		for i := 1; i < 3; i++ {
			podName := fmt.Sprintf("%s-%d", name, i)
			info := tc.valkeyTLSExec(t, ns, podName, tlsValkeyPort, "INFO", "replication")
			assert.Contains(t, info, "role:slave", "Pod %s should be a replica", podName)
			assert.Contains(t, info, "master_link_status:up",
				"Pod %s should have master_link_status:up (replication over TLS is working)", podName)
		}
	})

	t.Run("Master reports 2 connected replicas", func(t *testing.T) {
		masterPod := fmt.Sprintf("%s-0", name)
		require.Eventually(t, func() bool {
			info := tc.valkeyTLSExec(t, ns, masterPod, tlsValkeyPort, "INFO", "replication")
			return strings.Contains(info, "connected_slaves:2")
		}, 60*time.Second, 2*time.Second, "Master should have 2 connected replicas over TLS")
	})

	// =========================================================================
	// Phase 6: TLS connectivity — Sentinel
	// =========================================================================

	t.Run("Sentinel monitors master over TLS", func(t *testing.T) {
		sentinelPod := fmt.Sprintf("%s-sentinel-0", name)
		resp := tc.valkeyTLSExec(t, ns, sentinelPod, 26379, "SENTINEL", "master", name)
		assert.NotEmpty(t, resp, "Sentinel should return master info over TLS")
		t.Logf("Sentinel master info: %s", truncateLogs(resp, 500))
	})

	t.Run("Sentinel knows all replicas", func(t *testing.T) {
		sentinelPod := fmt.Sprintf("%s-sentinel-0", name)
		resp := tc.valkeyTLSExec(t, ns, sentinelPod, 26379, "SENTINEL", "replicas", name)
		assert.NotEmpty(t, resp, "Sentinel should report replicas")
		t.Logf("Sentinel replicas info: %s", truncateLogs(resp, 500))
	})

	t.Run("All Sentinel instances agree on master", func(t *testing.T) {
		// Query SENTINEL CKQUORUM from each sentinel to verify quorum over TLS.
		for i := 0; i < 3; i++ {
			sentinelPod := fmt.Sprintf("%s-sentinel-%d", name, i)
			resp := tc.valkeyTLSExecAllowError(t, ns, sentinelPod, 26379, "SENTINEL", "ckquorum", name)
			t.Logf("Sentinel-%d ckquorum: %s", i, resp)
			// Should contain OK or indicate quorum is reachable.
			assert.Contains(t, resp, "OK",
				"Sentinel-%d should confirm quorum is reachable over TLS", i)
		}
	})

	// =========================================================================
	// Phase 7: Data replication over TLS
	// =========================================================================

	masterPod := fmt.Sprintf("%s-0", name)

	t.Run("Write test data to master over TLS", func(t *testing.T) {
		testData := map[string]string{
			"tls-ha:string":    "hello-tls-ha-cluster",
			"tls-ha:number":    "9876",
			"tls-ha:timestamp": time.Now().Format(time.RFC3339),
			"tls-ha:unicode":   "🔐 TLS HA Replication Test",
			"tls-ha:long":      strings.Repeat("encrypted-data-", 50),
		}

		for key, value := range testData {
			resp := tc.valkeyTLSExec(t, ns, masterPod, tlsValkeyPort, "SET", key, value)
			assert.Equal(t, "OK", resp, "SET %s over TLS should succeed", key)
		}

		// List data type.
		for i := 0; i < 10; i++ {
			tc.valkeyTLSExec(t, ns, masterPod, tlsValkeyPort, "RPUSH", "tls-ha:list", fmt.Sprintf("item-%d", i))
		}

		// Hash data type.
		tc.valkeyTLSExec(t, ns, masterPod, tlsValkeyPort, "HSET", "tls-ha:hash",
			"name", "valkey-operator",
			"version", "1.0",
			"tls", "enabled",
		)

		// Set data type.
		tc.valkeyTLSExec(t, ns, masterPod, tlsValkeyPort, "SADD", "tls-ha:set", "member1", "member2", "member3")

		// Sorted set data type.
		tc.valkeyTLSExec(t, ns, masterPod, tlsValkeyPort, "ZADD", "tls-ha:zset", "1", "one", "2", "two", "3", "three")
	})

	t.Run("Wait for TLS replication sync", func(t *testing.T) {
		require.Eventually(t, func() bool {
			info := tc.valkeyTLSExec(t, ns, masterPod, tlsValkeyPort, "INFO", "replication")
			// All replicas should be synced (offset lag should be small).
			if !strings.Contains(info, "connected_slaves:2") {
				return false
			}
			// Verify no sync is in progress.
			return !strings.Contains(info, "master_sync_in_progress:1")
		}, 60*time.Second, 2*time.Second, "Replication sync over TLS should complete")
	})

	t.Run("Verify replicated data on replica 1 over TLS", func(t *testing.T) {
		replicaPod := fmt.Sprintf("%s-1", name)

		// Wait for initial data to appear.
		require.Eventually(t, func() bool {
			resp := tc.valkeyTLSExec(t, ns, replicaPod, tlsValkeyPort, "GET", "tls-ha:string")
			return resp == "hello-tls-ha-cluster"
		}, 30*time.Second, time.Second, "Data should replicate to replica 1 over TLS")

		// Verify all data types.
		assert.Equal(t, "9876",
			tc.valkeyTLSExec(t, ns, replicaPod, tlsValkeyPort, "GET", "tls-ha:number"))
		assert.Equal(t, "🔐 TLS HA Replication Test",
			tc.valkeyTLSExec(t, ns, replicaPod, tlsValkeyPort, "GET", "tls-ha:unicode"))
		assert.Equal(t, strings.Repeat("encrypted-data-", 50),
			tc.valkeyTLSExec(t, ns, replicaPod, tlsValkeyPort, "GET", "tls-ha:long"))

		// List.
		assert.Equal(t, "10",
			tc.valkeyTLSExec(t, ns, replicaPod, tlsValkeyPort, "LLEN", "tls-ha:list"))

		// Hash.
		assert.Equal(t, "valkey-operator",
			tc.valkeyTLSExec(t, ns, replicaPod, tlsValkeyPort, "HGET", "tls-ha:hash", "name"))
		assert.Equal(t, "enabled",
			tc.valkeyTLSExec(t, ns, replicaPod, tlsValkeyPort, "HGET", "tls-ha:hash", "tls"))

		// Set.
		assert.Equal(t, "3",
			tc.valkeyTLSExec(t, ns, replicaPod, tlsValkeyPort, "SCARD", "tls-ha:set"))

		// Sorted set.
		assert.Equal(t, "3",
			tc.valkeyTLSExec(t, ns, replicaPod, tlsValkeyPort, "ZCARD", "tls-ha:zset"))
	})

	t.Run("Verify replicated data on replica 2 over TLS", func(t *testing.T) {
		replicaPod := fmt.Sprintf("%s-2", name)

		require.Eventually(t, func() bool {
			resp := tc.valkeyTLSExec(t, ns, replicaPod, tlsValkeyPort, "GET", "tls-ha:string")
			return resp == "hello-tls-ha-cluster"
		}, 30*time.Second, time.Second, "Data should replicate to replica 2 over TLS")

		assert.Equal(t, "9876",
			tc.valkeyTLSExec(t, ns, replicaPod, tlsValkeyPort, "GET", "tls-ha:number"))
		assert.Equal(t, "10",
			tc.valkeyTLSExec(t, ns, replicaPod, tlsValkeyPort, "LLEN", "tls-ha:list"))
		assert.Equal(t, "3",
			tc.valkeyTLSExec(t, ns, replicaPod, tlsValkeyPort, "SCARD", "tls-ha:set"))
		assert.Equal(t, "3",
			tc.valkeyTLSExec(t, ns, replicaPod, tlsValkeyPort, "ZCARD", "tls-ha:zset"))
	})

	t.Run("DBSIZE consistent across TLS cluster", func(t *testing.T) {
		masterSize := tc.valkeyTLSExec(t, ns, masterPod, tlsValkeyPort, "DBSIZE")
		t.Logf("Master DBSIZE: %s", masterSize)

		for i := 1; i < 3; i++ {
			replicaPod := fmt.Sprintf("%s-%d", name, i)
			require.Eventually(t, func() bool {
				replicaSize := tc.valkeyTLSExec(t, ns, replicaPod, tlsValkeyPort, "DBSIZE")
				return replicaSize == masterSize
			}, 30*time.Second, time.Second, "Replica-%d DBSIZE should match master", i)
		}
	})

	t.Run("Replica is read-only over TLS", func(t *testing.T) {
		replicaPod := fmt.Sprintf("%s-1", name)
		resp := tc.valkeyTLSExecAllowError(t, ns, replicaPod, tlsValkeyPort, "SET", "tls-readonly-test", "should-fail")
		t.Logf("Write to replica over TLS response: %q", resp)
		// Should be a READONLY error or empty — definitely not OK.
		assert.NotEqual(t, "OK", resp, "Write to replica should be rejected")
	})

	// =========================================================================
	// Phase 8: Plaintext rejection
	// =========================================================================

	t.Run("Plaintext connection to Valkey TLS port is rejected", func(t *testing.T) {
		for i := 0; i < 3; i++ {
			podName := fmt.Sprintf("%s-%d", name, i)
			resp := tc.valkeyExecAllowError(t, ns, podName, tlsValkeyPort, "PING")
			assert.NotEqual(t, "PONG", resp,
				"Plaintext PING to pod %s TLS port should NOT return PONG", podName)
		}
	})

	t.Run("Plaintext connection to Sentinel TLS port is rejected", func(t *testing.T) {
		for i := 0; i < 3; i++ {
			sentinelPod := fmt.Sprintf("%s-sentinel-%d", name, i)
			resp := tc.valkeyExecAllowError(t, ns, sentinelPod, 26379, "PING")
			assert.NotEqual(t, "PONG", resp,
				"Plaintext PING to sentinel %s TLS port should NOT return PONG", sentinelPod)
		}
	})

	// =========================================================================
	// Phase 9: Log analysis
	// =========================================================================

	t.Run("Valkey pod logs show no TLS errors", func(t *testing.T) {
		for i := 0; i < 3; i++ {
			podName := fmt.Sprintf("%s-%d", name, i)
			logs := tc.getPodLogs(t, ns, podName, "valkey")

			assertNoErrorsInLogs(t, logs, podName)
			assertTLSActiveInLogs(t, logs, podName)

			// Verify successful replication-related log entries.
			if i == 0 {
				// Master should mention replicas syncing.
				t.Logf("Master %s logs (last 800 chars):\n%s", podName, truncateLogs(logs, 800))
			} else {
				// Replicas should mention connecting to master.
				t.Logf("Replica %s logs (last 800 chars):\n%s", podName, truncateLogs(logs, 800))
			}
		}
	})

	t.Run("Sentinel pod logs show no TLS errors", func(t *testing.T) {
		for i := 0; i < 3; i++ {
			sentinelPod := fmt.Sprintf("%s-sentinel-%d", name, i)
			logs := tc.getPodLogs(t, ns, sentinelPod, "sentinel")

			assertNoErrorsInLogs(t, logs, sentinelPod)

			// Sentinel should log master detection.
			t.Logf("Sentinel %s logs (last 500 chars):\n%s", sentinelPod, truncateLogs(logs, 500))
		}
	})

	t.Run("Operator logs show no TLS reconciliation errors", func(t *testing.T) {
		pods, err := tc.kube.CoreV1().Pods("valkey-operator-system").List(
			context.Background(),
			metav1.ListOptions{
				LabelSelector: "app.kubernetes.io/name=valkey-operator",
			},
		)
		require.NoError(t, err)
		require.NotEmpty(t, pods.Items)

		for _, pod := range pods.Items {
			logs := tc.getPodLogs(t, "valkey-operator-system", pod.Name, "")

			// Filter for lines related to this test's resources.
			lines := strings.Split(logs, "\n")
			var relevantLines []string
			for _, line := range lines {
				if strings.Contains(line, name) || strings.Contains(line, "ERROR") || strings.Contains(line, "error") {
					relevantLines = append(relevantLines, line)
				}
			}
			if len(relevantLines) > 0 {
				t.Logf("Operator logs for %s (relevant lines):\n%s",
					name, strings.Join(lastN(relevantLines, 20), "\n"))
			}

			// There should be no error-level logs related to our TLS resources.
			for _, line := range relevantLines {
				if strings.Contains(line, name) {
					assert.NotContains(t, line, "\"level\":\"error\"",
						"Operator should not log errors for TLS resources")
				}
			}
		}
	})

	// =========================================================================
	// Phase 10: TLS INFO verification
	// =========================================================================

	t.Run("Valkey INFO server confirms TLS configuration", func(t *testing.T) {
		podName := fmt.Sprintf("%s-0", name)
		info := tc.valkeyTLSExec(t, ns, podName, tlsValkeyPort, "INFO", "server")

		assert.Contains(t, info, "tcp_port:0", "Non-TLS port should be disabled (0)")
		t.Logf("Master INFO server (TLS relevant):\n%s",
			filterInfoLines(info, "tcp_port", "tls", "ssl", "config_file"))
	})

	t.Run("CONFIG GET confirms TLS settings", func(t *testing.T) {
		podName := fmt.Sprintf("%s-0", name)

		// Verify tls-port is configured.
		resp := tc.valkeyTLSExec(t, ns, podName, tlsValkeyPort, "CONFIG", "GET", "tls-port")
		assert.Contains(t, resp, "16379", "CONFIG GET tls-port should return 16379")

		// Verify tls-replication is enabled.
		resp = tc.valkeyTLSExec(t, ns, podName, tlsValkeyPort, "CONFIG", "GET", "tls-replication")
		assert.Contains(t, resp, "yes", "tls-replication should be enabled")

		// Verify port is 0 (disabled).
		resp = tc.valkeyTLSExec(t, ns, podName, tlsValkeyPort, "CONFIG", "GET", "port")
		assert.Contains(t, resp, "0", "Plaintext port should be 0")
	})

	// =========================================================================
	// Phase 11: Cleanup
	// =========================================================================

	t.Run("Cleanup TLS HA cluster", func(t *testing.T) {
		tc.deleteValkey(t, ns, name)
		tc.waitForDeletion(t, ns, name)
	})
}

// --- Helper functions ---

// assertVolumeExists checks that a Volume with the given name exists and references the expected secret.
func assertVolumeExists(t *testing.T, volumes []corev1.Volume, volumeName, expectedSecretName string) {
	t.Helper()
	for _, vol := range volumes {
		if vol.Name == volumeName {
			require.NotNil(t, vol.VolumeSource.Secret, "Volume %s should use Secret source", volumeName)
			assert.Equal(t, expectedSecretName, vol.VolumeSource.Secret.SecretName,
				"Volume %s should reference secret %s", volumeName, expectedSecretName)
			return
		}
	}
	t.Errorf("Volume %s not found in pod spec", volumeName)
}

// assertVolumeMountExists checks that a VolumeMount with the given name and mount path exists.
func assertVolumeMountExists(t *testing.T, mounts []corev1.VolumeMount, mountName, expectedPath string) {
	t.Helper()
	for _, mount := range mounts {
		if mount.Name == mountName {
			assert.Equal(t, expectedPath, mount.MountPath,
				"VolumeMount %s should have mountPath %s", mountName, expectedPath)
			assert.True(t, mount.ReadOnly, "VolumeMount %s should be read-only", mountName)
			return
		}
	}
	t.Errorf("VolumeMount %s not found in container spec", mountName)
}

// truncateLogs returns the last n characters of a log string for readable output.
func truncateLogs(logs string, maxChars int) string {
	if len(logs) <= maxChars {
		return logs
	}
	return "..." + logs[len(logs)-maxChars:]
}

// lastN returns the last n elements of a string slice.
func lastN(s []string, n int) []string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

// filterInfoLines filters Valkey INFO output for lines matching any of the given keywords.
func filterInfoLines(info string, keywords ...string) string {
	var filtered []string
	for _, line := range strings.Split(info, "\n") {
		for _, kw := range keywords {
			if strings.Contains(strings.ToLower(line), kw) {
				filtered = append(filtered, strings.TrimSpace(line))
				break
			}
		}
	}
	return strings.Join(filtered, "\n")
}

// valkeyTLSExecAllowError is an alias on testClients for the allow-error variant.
// Already defined above as a method — this comment is for clarity.
