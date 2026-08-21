//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// TestE2E_RollingUpdate_TwoReplicasNoSentinel pins ADR 0008 D4-D7 in a real cluster.
//
// With two replicas and no Sentinel the rolling update promotes pod-1 and deletes
// pod-0, and at that moment the promoted master has no replicas attached. The
// init container of the returning pod-0 therefore cannot recognize pod-1 as the
// master from peer state alone: its acceptance test requires role:master *and*
// connected_slaves > 0. Before the fix it fell through to the ordinal fallback and
// booted as a second, independent master; writes that reached it in that window
// were discarded once the operator repaired the topology.
//
// The assertion is the init container's own decision log rather than a race
// against the split window: the log records which branch elected the pod's role,
// which is exactly the behavior under test.
func TestE2E_RollingUpdate_TwoReplicasNoSentinel(t *testing.T) {
	t.Parallel()
	tc := newTestClients(t)
	ns := "e2e-rolling-two-replicas"
	cleanup := tc.createNamespace(t, ns)
	defer cleanup()

	name := "roll-2r"
	var replicas int32 = 2
	initialImage := "valkey/valkey:8.0"
	updatedImage := "valkey/valkey:8.1"

	valkey := buildValkeyObject(name, ns, map[string]interface{}{
		"replicas": int64(replicas),
		"image":    initialImage,
	})

	t.Log("Creating 2-replica Valkey CR without Sentinel")
	tc.createValkey(t, ns, valkey)
	defer tc.deleteValkey(t, ns, name)

	tc.waitForStatefulSetReady(t, ns, name, replicas)
	tc.waitForValkeyPhase(t, ns, name, "OK")

	masterPod := tc.findMasterPod(t, ns, name, int(replicas))
	require.Equal(t, fmt.Sprintf("%s-0", name), masterPod,
		"the initial master must be pod-0, otherwise this test does not exercise the ADR 0008 D4-D7 shape")
	tc.waitForConnectedReplicas(t, ns, masterPod, 6379, int(replicas)-1)

	tc.valkeyMSET(t, ns, masterPod, 6379, map[string]string{
		"two-rep:key1": "before-update",
	})
	tc.waitForConnectedReplicas(t, ns, masterPod, 6379, int(replicas)-1)

	t.Run("Rolling update replaces both pods", func(t *testing.T) {
		tc.updateValkeyImage(t, ns, name, updatedImage)
		tc.waitForAllPodsImage(t, ns, name, int(replicas), updatedImage)
		tc.waitForStatefulSetReady(t, ns, name, replicas)
		tc.waitForValkeyPhaseAfterRollingUpdate(t, ns, name, "OK")
	})

	t.Run("Returning pod-0 joins the promoted master instead of electing itself", func(t *testing.T) {
		logs := tc.getPodLogs(t, ns, fmt.Sprintf("%s-0", name), "init-config-selector")
		t.Logf("init-config-selector log of %s-0:\n%s", name, logs)

		assert.NotContains(t, logs, "using ordinal-based config",
			"pod-0 must not fall back to the ordinal config while pod-1 holds the data — "+
				"that is the ADR 0008 D4-D7 split-brain branch")
		assert.True(t,
			strings.Contains(logs, "Using known master from replica config") ||
				strings.Contains(logs, "Discovered existing master"),
			"pod-0 must elect its role from the operator-published master address")
	})

	t.Run("Exactly one master after the update", func(t *testing.T) {
		masters := 0
		for i := int32(0); i < replicas; i++ {
			podName := fmt.Sprintf("%s-%d", name, i)
			info := tc.valkeyExec(t, ns, podName, 6379, "INFO", "replication")
			if strings.Contains(info, "role:master") {
				masters++
			}
		}
		assert.Equal(t, 1, masters, "the cluster must have a single master after the rolling update")
	})

	t.Run("Known master points back at pod-0", func(t *testing.T) {
		annotations := tc.getValkeyAnnotations(t, ns, name)
		assert.Equal(t,
			fmt.Sprintf("%s-0.%s-headless.%s.svc.cluster.local", name, name, ns),
			annotations["vko.gtrfc.com/known-master"],
			"after topology restoration the replica config must point at pod-0 again")
	})

	t.Run("Data survives the update", func(t *testing.T) {
		newMaster := tc.findMasterPod(t, ns, name, int(replicas))
		tc.waitForConnectedReplicas(t, ns, newMaster, 6379, int(replicas)-1)
		assert.Equal(t, "before-update", tc.valkeyExec(t, ns, newMaster, 6379, "GET", "two-rep:key1"))
	})
}

// getValkeyAnnotations returns the metadata annotations of a Valkey CR.
func (tc *testClients) getValkeyAnnotations(t *testing.T, namespace, name string) map[string]string {
	t.Helper()

	valkey, err := tc.dynamic.Resource(valkeyGVR).Namespace(namespace).Get(
		context.Background(), name, metav1.GetOptions{})
	require.NoError(t, err, "Failed to get Valkey CR %s/%s", namespace, name)

	annotations, _, err := unstructured.NestedStringMap(valkey.Object, "metadata", "annotations")
	require.NoError(t, err)
	return annotations
}
