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
	k8stypes "k8s.io/apimachinery/pkg/types"

	"github.com/guided-traffic/valkey-operator/test/testimages"
)

// ---------------------------------------------------------------------------
// TestE2E_SentinelNamingAnotherMasterIsReplaced
//
// Regression test for ADR 0040. On wds18 one Sentinel kept naming the former master
// for hours after a failover: it had taken the new config epoch from a hello, failed
// to resolve the new master's hostname, and kept the old address at the new epoch.
// Its peers announce that same epoch, and Sentinel adopts an address only from a
// higher one, so nothing corrected it. The CR read Ready=True and phase OK.
//
// The fixture builds the same stuck state without a failover. On a fresh cluster
// every Sentinel holds config epoch 0 for its master; pointing one at a replica with
// SENTINEL REMOVE + MONITOR leaves it at epoch 0 too, so no hello moves it. The test
// reads that back before relying on it.
// ---------------------------------------------------------------------------
func TestE2E_SentinelNamingAnotherMasterIsReplaced(t *testing.T) {
	t.Parallel()
	tc := newTestClients(t)
	ns := "e2e-sentinel-diverged"
	cleanup := tc.createNamespace(t, ns)
	defer cleanup()

	name := "diverged"
	valkey := buildValkeyObject(name, ns, map[string]interface{}{
		"replicas": int64(3),
		"image":    testimages.Default(),
		"sentinel": map[string]interface{}{
			"enabled":  true,
			"replicas": int64(3),
		},
	})

	t.Log("Creating HA Valkey CR")
	tc.createValkey(t, ns, valkey)
	defer tc.deleteValkey(t, ns, name)

	sentinelSts := fmt.Sprintf("%s-sentinel", name)
	tc.waitForStatefulSetReady(t, ns, name, 3)
	tc.waitForStatefulSetReady(t, ns, sentinelSts, 3)
	tc.waitForValkeyPhase(t, ns, name, "OK")
	tc.waitForSentinelSlaves(t, ns, name, 2)

	master := tc.findMasterPod(t, ns, name, 3)
	replica := ""
	for i := 0; i < 3; i++ {
		if pod := fmt.Sprintf("%s-%d", name, i); pod != master {
			replica = pod
			break
		}
	}
	masterHost := dataPodHost(ns, name, master)
	replicaHost := dataPodHost(ns, name, replica)
	stuck := fmt.Sprintf("%s-sentinel-2", name)

	require.Equal(t, "0", tc.sentinelMasterField(t, ns, fmt.Sprintf("%s-sentinel-0", name), name, "config-epoch"),
		"the fixture needs the peers at the epoch a fresh monitor starts at; at a higher one their "+
			"hellos correct the injected Sentinel")

	t.Logf("Pointing %s at the replica %s", stuck, replica)
	require.Equal(t, "OK", tc.valkeyExec(t, ns, stuck, 26379, "SENTINEL", "REMOVE", name))
	require.Equal(t, "OK", tc.valkeyExec(t, ns, stuck, 26379, "SENTINEL", "MONITOR", name, replicaHost, "6379", "2"))

	for start := time.Now(); time.Since(start) < 20*time.Second; time.Sleep(pollInterval) {
		require.Equal(t, replicaHost, tc.sentinelNamedMaster(t, ns, stuck, name),
			"the peers corrected the injected Sentinel, so the fixture does not reproduce the stuck state")
	}

	// A Sentinel command changes no Kubernetes object, so nothing starts a pass before
	// the five-minute poll (ADR 0040 D2, unit-tested). Touching the Sentinel
	// StatefulSet starts one now.
	tc.pokeStatefulSet(t, ns, sentinelSts)

	diverged := tc.waitForValkeyCondition(t, ns, name, "SentinelMasterDiverged", "True", time.Minute)
	assert.Contains(t, diverged["message"], fmt.Sprintf("%s names %s:6379", stuck, replicaHost))

	previous := tc.getPod(t, ns, stuck)
	t.Logf("Waiting for the operator to replace %s (bound %s)", stuck, "2m")
	tc.waitForPodRecreated(t, ns, stuck, previous.UID)

	require.Eventually(t, func() bool {
		for i := 0; i < 3; i++ {
			pod := fmt.Sprintf("%s-sentinel-%d", name, i)
			if got := tc.sentinelNamedMaster(t, ns, pod, name); got != masterHost {
				t.Logf("%s names %q, want %s", pod, got, masterHost)
				return false
			}
		}
		return true
	}, 2*time.Minute, pollInterval, "every Sentinel, the replaced one included, must name the master")

	tc.waitForValkeyCondition(t, ns, name, "SentinelMasterDiverged", "False", 2*time.Minute)
	tc.waitForValkeyEvent(t, ns, name, "SentinelMasterDiverged", time.Minute,
		"the replacement must be reported as a SentinelMasterDiverged Event")

	// The repair touches no data pod: the master is unchanged, and a write reaches
	// every replica.
	assert.Equal(t, master, tc.findMasterPod(t, ns, name, 3), "replacing a Sentinel must not move the master")
	require.Equal(t, "OK", tc.valkeyExec(t, ns, master, 6379, "SET", "after-sentinel-repair", "42"))
	for i := 0; i < 3; i++ {
		pod := fmt.Sprintf("%s-%d", name, i)
		if pod == master {
			continue
		}
		require.Eventually(t, func() bool {
			return tc.valkeyExecQuick(t, ns, pod, 6379, "GET", "after-sentinel-repair") == "42"
		}, time.Minute, pollInterval, "the write must replicate to %s", pod)
	}
}

// dataPodHost is the headless-Service name of a data pod, the form Sentinel names a
// master in.
func dataPodHost(namespace, valkeyName, pod string) string {
	return fmt.Sprintf("%s.%s-headless.%s.svc.cluster.local", pod, valkeyName, namespace)
}

// sentinelNamedMaster is the host a Sentinel hands a client that asks it for the
// master. Empty when the Sentinel did not answer.
func (tc *testClients) sentinelNamedMaster(t *testing.T, namespace, sentinelPod, valkeyName string) string {
	t.Helper()
	raw := tc.valkeyExecQuick(t, namespace, sentinelPod, 26379, "SENTINEL", "GET-MASTER-ADDR-BY-NAME", valkeyName)
	return strings.TrimSpace(strings.SplitN(raw, "\n", 2)[0])
}

// sentinelMasterField reads one field of SENTINEL MASTER, a flat list of names and
// values in --raw output.
func (tc *testClients) sentinelMasterField(t *testing.T, namespace, sentinelPod, valkeyName, field string) string {
	t.Helper()
	lines := strings.Split(tc.valkeyExec(t, namespace, sentinelPod, 26379, "SENTINEL", "MASTER", valkeyName), "\n")
	for i := 0; i+1 < len(lines); i += 2 {
		if strings.TrimSpace(lines[i]) == field {
			return strings.TrimSpace(lines[i+1])
		}
	}
	require.Failf(t, "field missing", "SENTINEL MASTER on %s has no %s", sentinelPod, field)
	return ""
}

// pokeStatefulSet writes an annotation onto a StatefulSet the operator owns, which
// starts a reconcile pass through its Owns watch and changes nothing it compares.
func (tc *testClients) pokeStatefulSet(t *testing.T, namespace, name string) {
	t.Helper()
	patch := fmt.Sprintf(`{"metadata":{"annotations":{"e2e.vko.gtrfc.com/poke":%q}}}`, time.Now().Format(time.RFC3339Nano))
	_, err := tc.kube.AppsV1().StatefulSets(namespace).Patch(context.Background(), name,
		k8stypes.MergePatchType, []byte(patch), metav1.PatchOptions{})
	require.NoError(t, err, "poking StatefulSet %s/%s", namespace, name)
}
