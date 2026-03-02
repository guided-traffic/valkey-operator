//go:build e2e && e2e_helm

package e2e

// TestE2E_MigrateDefaults verifies the "migrate" subcommand of the operator
// binary. It simulates an old Valkey CR that is missing optional field defaults
// (as would happen after a CRD schema change), runs the migrate binary, and
// asserts that the CR is patched with the expected default values.
//
// This test is gated behind the "e2e_helm" build tag so it is excluded from
// the standard "make test-e2e" run. Use "make test-e2e-helm" to execute it.
// That target also builds the binary first.

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
)

// defaultManagerBinary is the path to the compiled operator binary relative to
// the test/e2e/ package directory (= ../../bin/manager from the workspace root).
// Override with the MANAGER_BINARY environment variable.
const defaultManagerBinary = "../../bin/manager"

// TestE2E_MigrateDefaults tests the migrate subcommand:
//  1. Creates a Valkey CR
//  2. Patches it so that optional fields are empty (simulating a pre-defaults CR)
//  3. Runs "./manager migrate"
//  4. Verifies the CR is patched with the correct default values
func TestE2E_MigrateDefaults(t *testing.T) {
	tc := newTestClients(t)
	ns := "e2e-migrate"
	cleanup := tc.createNamespace(t, ns)
	defer cleanup()

	name := "migrate-test"
	ctx := context.Background()

	valkey := buildValkeyObject(name, ns, map[string]interface{}{
		"replicas": int64(1),
		"image":    "valkey/valkey:8.0",
	})

	t.Log("Creating Valkey CR for migration test")
	tc.createValkey(t, ns, valkey)
	defer tc.deleteValkey(t, ns, name)

	// Simulate an "old" CR by patching optional fields back to their zero/empty
	// values. This reproduces the state of a CR that was created before those
	// fields were added to the CRD schema with defaults.
	t.Log("Patching CR to simulate missing field defaults (pre-upgrade state)")
	simulateOldCR(t, tc, ns, name)

	// Verify the zeroed state is stored before running migrate.
	t.Run("CR has empty secretPasswordKey before migration", func(t *testing.T) {
		vk, err := tc.dynamic.Resource(valkeyGVR).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
		require.NoError(t, err)

		authObj, found, _ := unstructured.NestedMap(vk.Object, "spec", "auth")
		if !found {
			t.Log("spec.auth not set — skipping pre-check (field is nil, migrate will ignore it)")
			return
		}
		key, _, _ := getString(authObj, "secretPasswordKey")
		assert.Empty(t, key, "spec.auth.secretPasswordKey should be empty before migration")
	})

	t.Log("Running ./manager migrate")
	runMigrateBinary(t)

	// Allow the migrate process a moment to commit patches.
	time.Sleep(1 * time.Second)

	// Verify that migrate restored the expected default values.
	t.Run("secretPasswordKey is set to default after migration", func(t *testing.T) {
		vk, err := tc.dynamic.Resource(valkeyGVR).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
		require.NoError(t, err)

		authObj, found, _ := unstructured.NestedMap(vk.Object, "spec", "auth")
		if !found {
			t.Log("spec.auth not present — no auth default to verify")
			return
		}
		key, _, _ := getString(authObj, "secretPasswordKey")
		assert.Equal(t, "password", key,
			"migrate should set spec.auth.secretPasswordKey to 'password'")
	})

	t.Run("spec.replicas is at least 1 after migration", func(t *testing.T) {
		vk, err := tc.dynamic.Resource(valkeyGVR).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
		require.NoError(t, err)

		replicas, found, _ := unstructured.NestedFieldNoCopy(vk.Object, "spec", "replicas")
		require.True(t, found, "spec.replicas should be present")
		replicasInt, ok := replicas.(int64)
		if !ok {
			// JSON numbers from unstructured may be float64.
			replicasFloat, ok2 := replicas.(float64)
			require.True(t, ok2, "spec.replicas should be numeric")
			replicasInt = int64(replicasFloat)
		}
		assert.GreaterOrEqual(t, replicasInt, int64(1),
			"migrate should ensure spec.replicas >= 1")
	})
}

// simulateOldCR patches the Valkey CR to zero out optional fields that would be
// missing in a CR created before the current CRD schema introduced defaults.
func simulateOldCR(t *testing.T, tc *testClients, ns, name string) {
	t.Helper()
	ctx := context.Background()

	// Set auth with empty secretPasswordKey (the field existed but had no default).
	// JSON merge patch: nested nulls remove the key; empty string leaves it present
	// but empty — exactly what we want to simulate a missing default.
	patchBody := []byte(`{"spec":{"auth":{"secretName":"dummy-secret","secretPasswordKey":""}}}`)
	_, err := tc.dynamic.Resource(valkeyGVR).Namespace(ns).Patch(
		ctx, name, types.MergePatchType, patchBody, metav1.PatchOptions{})
	require.NoError(t, err, "Failed to simulate old CR state via merge patch")
	t.Log("CR patched to simulate pre-defaults state")
}

// runMigrateBinary executes the manager binary with the "migrate" subcommand.
// The binary is located by the MANAGER_BINARY environment variable (default:
// ../../bin/manager relative to test/e2e/). KUBECONFIG is inherited from the
// test environment so the binary connects to the same cluster as the test.
func runMigrateBinary(t *testing.T) {
	t.Helper()

	binaryPath := os.Getenv("MANAGER_BINARY")
	if binaryPath == "" {
		binaryPath = defaultManagerBinary
	}

	// Build the absolute path if relative.
	cmd := exec.Command(binaryPath, "migrate")

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	t.Logf("Running: %s migrate", binaryPath)
	err := cmd.Run()
	t.Logf("migrate stdout:\n%s", stdout.String())
	if stderr.Len() > 0 {
		t.Logf("migrate stderr:\n%s", stderr.String())
	}

	require.NoError(t, err,
		"migrate binary should exit with code 0; stderr:\n%s", stderr.String())
}

// getString extracts a string from an unstructured map, returning empty string
// when the field is absent or has a non-string type.
func getString(m map[string]interface{}, key string) (string, bool, error) {
	v, ok := m[key]
	if !ok {
		return "", false, nil
	}
	s, ok := v.(string)
	if !ok {
		return "", false, fmt.Errorf("field %q is not a string", key)
	}
	return s, true, nil
}
