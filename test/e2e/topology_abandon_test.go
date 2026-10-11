//go:build e2e

package e2e

// Tests in this file cover ADR 0010 D2-D4: the bounded escape from Phase 1 of the topology
// restoration. After a two-replica non-Sentinel rolling update the operator waits
// for the returning pod-0 to sync back from the promoted replica; when that never
// happens the wait must end within spec.rollingUpdate.syncTimeout, emit
// TopologyRestoreAbandoned, record TopologyRestored=False and let Phase 2 finish
// the rolling update with the promoted replica as master.
//
// Why the returning pod-0 is not simply blocked from coming back (the obvious
// mechanism, and the one the ticket sketched): the state machine cannot reach
// Phase 1 without it. handlePostManualFailover requeues without a bound while
// pod-0 is missing, terminating, off-template or not ready, and
// clearStaleRollingUpdateState only clears a stale state while nothing has been
// replaced yet -- with pod-1 already replaced that escape is disarmed. A pod-0
// that never returns therefore stalls in manual-failover forever, Phase 1 is never
// entered and TopologyRestoreAbandoned can never fire.
//
// What does reach the abandon path is a pod-0 that returns, becomes Ready, accepts
// the operator's REPLICAOF and then never reports master_link_status:up. The jam
// that produces it sits on the other end of the link: jamSyncFromPromotedReplica
// tells the replacement pod-1 -- the pod the roll is about to promote -- to hold every
// full sync it is asked for, before the failover has happened.
//
// It used to sit on pod-0, and that is why it moved. A jam on pod-0 can only land on
// the pod-0 that comes back, in the seconds between its return and Phase 1 seeing a
// healthy link; a jam that lands on the outgoing pod-0 dies with it (measured on runs
// 35312304295, 35315129844 and 35317540380), and one that misses the seconds fails
// the test without the abandon path ever being entered (run 37881026168: "never came
// back as a replica ... while the manual failover was in flight"). The replacement
// pod-1 exists from the first step of the roll and is promoted, not replaced, so the
// jam has from its start until the failover to land, and nothing the roll does
// afterwards removes it.
//
// One property of the scenario is worth knowing before reading a failure: from the
// promotion until the jam is lifted, the promoted replica holds the only copy of the
// dataset. Persistence is off, pod-0 is deliberately unable to replicate, and the
// outgoing master was deleted -- so anything that restarts the promoted pod in that
// window empties the cluster, and the data assertion below fails for a reason that is
// not a promotion decision. That is what valkeyPodForensics separates.

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

	"github.com/guided-traffic/valkey-operator/test/testimages"
)

// Rolling update annotations the operator writes on the Valkey CR
// (internal/controller/rolling_update.go, internal/builder/sentinel.go).
const (
	annotationRollingUpdateStateKey     = "vko.gtrfc.com/rolling-update-state"
	annotationPromotedPodKey            = "vko.gtrfc.com/promoted-pod"
	annotationTopologyRestoreStartedKey = "vko.gtrfc.com/topology-restore-started"
	annotationKnownMasterKey            = "vko.gtrfc.com/known-master"
)

// topologyAbandonSyncTimeout is spec.rollingUpdate.syncTimeout for this test. The
// CRD schema accepts sub-minute values (the field carries no validation marker and
// is a bare `type: string` in the generated schema), and GetSyncTimeout returns it
// verbatim.
//
// The value is two-sided. The same budget bounds the replica-replacement phase:
// verifyReplacedReplicasSynced arms sync-wait-started whenever a ready replaced pod
// is unreachable or still syncing, and pauseRollingUpdate then clears the rolling
// update state and puts the CR into phase Error -- which would kill this test
// before the failover it needs. Upward, the abandon lands one syncTimeout plus one
// rollingUpdateRequeueDelay (10 s) after Phase 1 starts. 60 s tolerates six
// consecutive failing passes in the replica phase and still leaves the abandon
// well inside abandonConditionTimeout, so the flake-cheap direction is chosen
// deliberately.
const topologyAbandonSyncTimeout = "60s"

// syncJamDelay is the repl-diskless-sync-delay, in seconds, the jammed master runs
// with. With diskless sync on (the operator's configuration) a master starts the
// transfer only once a waiting replica has waited this long, and it answers the
// replica's PSYNC only when the transfer starts. Until then the replica reports
// master_link_status:down (measured on Valkey 9.1.1 at the abandon:
// master_sync_in_progress:0, master_last_io_seconds_ago:-1), and a replica that gives
// up and asks again waits the delay again. Nothing in the jam is a failure, so the
// operator's REPLICAOF -- a no-op on a pod already replicating the promoted master --
// cannot clear it.
const syncJamDelay = "3600"

const (
	// jamPollInterval is one cheap pod GET per tick while pod-1 is being replaced;
	// the kubectl exec only runs once the replacement exists.
	jamPollInterval = 500 * time.Millisecond

	// jamPollTimeout covers the replacement of pod-1, the first step of the roll.
	jamPollTimeout = 10 * time.Minute

	// abandonConditionTimeout covers pod-0 becoming Ready, one operator pass, the
	// syncTimeout above and the requeue that follows it.
	abandonConditionTimeout = 5 * time.Minute

	// abandonEventTimeout is a lookup budget, not a race budget:
	// abandonTopologyRestoration records the Event before it writes the condition,
	// and the condition is already waited for by then.
	abandonEventTimeout = 2 * time.Minute
)

// TestE2E_RollingUpdate_TopologyRestoreAbandoned drives a two-replica non-Sentinel
// rolling update into the ADR 0010 D2-D4 abandon path and asserts the end state it is meant
// to produce: a serviceable cluster whose master is the promoted replica, not pod-0.
func TestE2E_RollingUpdate_TopologyRestoreAbandoned(t *testing.T) {
	t.Parallel()
	tc := newTestClients(t)

	ns := "e2e-topology-abandon"
	cleanup := tc.createNamespace(t, ns)
	defer cleanup()

	const replicas = 2
	name := "abandon-2r"
	pod0 := fmt.Sprintf("%s-0", name)
	pod1 := fmt.Sprintf("%s-1", name)
	initialImage := testimages.UpgradeFrom
	updatedImage := testimages.UpgradeTo

	t.Log("Creating a 2-replica Valkey CR without Sentinel and with a short sync timeout")
	tc.createValkey(t, ns, buildValkeyObject(name, ns, map[string]interface{}{
		"replicas": int64(replicas),
		"image":    initialImage,
		"rollingUpdate": map[string]interface{}{
			"syncTimeout": topologyAbandonSyncTimeout,
		},
	}))
	defer tc.deleteValkey(t, ns, name)

	tc.waitForStatefulSetReady(t, ns, name, replicas)
	tc.waitForValkeyPhase(t, ns, name, "OK")

	require.Equal(t, pod0, tc.findMasterPod(t, ns, name, replicas),
		"the initial master must be pod-0, otherwise this test does not exercise the ADR 0010 D2-D4 shape")
	tc.waitForConnectedReplicas(t, ns, pod0, 6379, replicas-1)

	// The payload has to be on pod-1 before the failover promotes it, otherwise a
	// later read from the promoted master proves nothing.
	tc.valkeyMSET(t, ns, pod0, 6379, map[string]string{"abandon:key1": "before-update"})
	require.Eventually(t, func() bool {
		return tc.valkeyExecQuick(t, ns, pod1, 6379, "GET", "abandon:key1") == "before-update"
	}, time.Minute, pollInterval,
		"the pre-update write must reach %s before the rolling update promotes it", pod1)

	t.Log("Triggering the rolling update that ends in the manual failover")
	tc.updateValkeyImage(t, ns, name, updatedImage)

	liftJam := tc.jamSyncFromPromotedReplica(t, ns, name, updatedImage)

	t.Run("Phase 1 gives up and leaves the promoted replica as master", func(t *testing.T) {
		cond := tc.waitForValkeyCondition(t, ns, name, "TopologyRestored", "False", abandonConditionTimeout)
		assert.Equal(t, "RestoreTimeout", cond["reason"],
			"an abandoned restoration must be distinguishable from one that failed for another reason")
		message, _ := cond["message"].(string)
		assert.Contains(t, message, pod1+" stays master",
			"the condition must name the pod that keeps the master role")

		// The proof that the jam is what stalled Phase 1: the link is down while
		// master_host already names the pod the operator pointed pod-0 at.
		t.Logf("INFO replication on %s at the abandon:\n%s", pod0,
			tc.valkeyExecQuick(t, ns, pod0, 6379, "INFO", "replication"))

		tc.waitForValkeyEvent(t, ns, name, "TopologyRestoreAbandoned", abandonEventTimeout,
			"a TopologyRestoreAbandoned Event must appear on Valkey %s/%s", ns, name)
	})

	t.Run("Rolling update completes with a non-pod-0 master", func(t *testing.T) {
		tc.waitForAllPodsImage(t, ns, name, replicas, updatedImage)
		// Not a tautology: reconcileWorkload skips updateStatus for as long as the
		// rolling update requeues, so the phase stays "Failover in progress" until
		// Phase 2 reports the update completed.
		tc.waitForValkeyPhaseAfterRollingUpdate(t, ns, name, "OK")

		annotations := tc.getValkeyAnnotations(t, ns, name)
		assert.Empty(t, annotations[annotationRollingUpdateStateKey],
			"Phase 2 must clear the rolling update state even on the abandoned path")
		assert.Empty(t, annotations[annotationTopologyRestoreStartedKey],
			"the Phase 1 bound must be released with the rest of the state")
		assert.Empty(t, annotations[annotationPromotedPodKey],
			"the promoted-pod marker belongs to the running update only")
		assert.Equal(t,
			fmt.Sprintf("%s.%s-headless.%s.svc.cluster.local", pod1, name, ns),
			annotations[annotationKnownMasterKey],
			"known-master must still name the promoted replica: clearRollingUpdateState does not "+
				"delete it and promotePod0AndRedirect -- the only writer that would move it to pod-0 -- never ran")

		masters := 0
		masterPod := ""
		for i := 0; i < replicas; i++ {
			podName := fmt.Sprintf("%s-%d", name, i)
			if strings.Contains(tc.valkeyExec(t, ns, podName, 6379, "INFO", "replication"), "role:master") {
				masters++
				masterPod = podName
			}
		}
		assert.Equal(t, 1, masters, "the abandoned topology must still have exactly one master")
		assert.Equal(t, pod1, masterPod, "the promoted replica must keep the master role")
	})

	t.Run("Writes survive on the promoted master", func(t *testing.T) {
		// The dump on failure is what separates the two ways this can go wrong: a
		// promotion that took a replica which never had the dataset, or a promoted
		// master that had it and lost the process afterwards (no persistence is
		// configured here, so a restart empties it). The pod is gone by the time
		// the workflow collects anything, so the dump has to happen here.
		if !assert.Equal(t, "before-update", tc.valkeyExec(t, ns, pod1, 6379, "GET", "abandon:key1"),
			"the pre-update payload must still be served by the promoted master") {
			t.Log(tc.valkeyPodForensics(t, ns, pod1, 6379))
		}
		require.Equal(t, "OK", tc.valkeyExec(t, ns, pod1, 6379, "SET", "abandon:key2", "after-abandon"),
			"the abandoned end state must be writable, not just readable")
	})

	t.Run("The -rw Service selects only the promoted master", func(t *testing.T) {
		// The selector is instanceRole=master, written by the sidecar on a 1 s poll,
		// so a short wait covers the labeling lag.
		require.Eventually(t, func() bool {
			names := tc.getEndpointPodNames(t, ns, fmt.Sprintf("%s-rw", name))
			return len(names) == 1 && names[0] == pod1
		}, 90*time.Second, pollInterval,
			"the -rw Service must select only %s after the abandon", pod1)
	})

	t.Run("The abandoned topology converges once pod-0 restarts", func(t *testing.T) {
		// The restart proves the end state serviceable on its own terms: the recreated
		// pod-0 reads the replica ConfigMap, whose replicaof still names the promoted
		// master. It is awaited by UID before the jam is lifted, so every gate below can
		// only be met by the new pod -- lifted first, the jam would let the outgoing
		// pod-0 finish its own sync while it terminates, and it would answer them
		// (ADR 0017 D50).
		oldPod0 := tc.getPod(t, ns, pod0).UID
		tc.deletePod(t, ns, pod0)
		tc.waitForPodRecreated(t, ns, pod0, oldPod0)
		liftJam()

		// Two gates, and each closes what the other leaves open. The master-side one
		// proves pod-0 attached to the promoted master and not elsewhere. It does not
		// prove the dataset arrived, though -- a replica is counted from the moment it
		// asks for synchronization, and the master then sits out
		// repl-diskless-sync-delay before it even starts the BGSAVE. The replica-side
		// master_link_status:up is the moment the RDB is loaded and the GET below
		// can be answered.
		tc.waitForConnectedReplicas(t, ns, pod1, 6379, 1)
		tc.waitForReplicaSynced(t, ns, pod0, 6379)
		tc.waitForPodReady(t, ns, pod0)

		assert.Equal(t, "after-abandon", tc.valkeyExec(t, ns, pod0, 6379, "GET", "abandon:key2"),
			"pod-0 must re-sync from the promoted master, i.e. the abandoned topology is serviceable")
		tc.waitForValkeyPhaseAfterRollingUpdate(t, ns, name, "OK")
	})
}

// jamSyncFromPromotedReplica makes the replacement pod-1 -- the replica the rolling
// update is about to promote -- hold every full sync it is asked for, so that the
// pod-0 which comes back after the failover can never finish syncing from it and
// Phase 1 has to run into its sync timeout. It returns the function that lifts the
// jam.
//
// The gate is the pod's identity, never controller state (ADR 0017 D50): the pod-1
// running the new image and carrying no deletionTimestamp is the one the roll
// promotes, and nothing in the roll replaces it again. The outgoing pod-1 runs the
// old image. The jam is read back rather than trusted: a CONFIG SET that answered
// OK from a process about to be replaced proves nothing.
//
// The setting is a master-side one and harmless while pod-1 is still a replica, so
// the jam can land at any time between pod-1's return and pod-0's first sync request
// after the failover. That window holds pod-1's own full sync, its readiness, the
// operator's WAIT and the replacement of pod-0, where the pod-0-side jam this replaces
// had the few seconds between pod-0's return and Phase 1. The role pod-1 answered when
// the jam landed is logged, so a jam that missed the window -- which can only fail the
// test, at the TopologyRestored wait, never pass it -- is visible as such.
func (tc *testClients) jamSyncFromPromotedReplica(t *testing.T, namespace, name, updatedImage string) func() {
	t.Helper()

	pod1 := fmt.Sprintf("%s-1", name)
	configValue := func(reply string) string {
		lines := strings.Split(strings.TrimSpace(reply), "\n")
		return strings.TrimSpace(lines[len(lines)-1])
	}

	original := ""
	err := wait.PollUntilContextTimeout(context.Background(), jamPollInterval, jamPollTimeout, true,
		func(ctx context.Context) (bool, error) {
			pod, err := tc.kube.CoreV1().Pods(namespace).Get(ctx, pod1, metav1.GetOptions{})
			if err != nil || pod.DeletionTimestamp != nil || containerImage(pod, valkeyContainerName) != updatedImage {
				return false, nil
			}
			if original == "" {
				if original = configValue(tc.valkeyExecQuick(t, namespace, pod1, 6379,
					"CONFIG", "GET", "repl-diskless-sync-delay")); original == "" {
					return false, nil
				}
			}
			// Diskless sync and an unset replica threshold are what the operator
			// configures; they are set again so that the jam does not rest on it.
			if tc.valkeyExecQuick(t, namespace, pod1, 6379, "CONFIG", "SET",
				"repl-diskless-sync", "yes",
				"repl-diskless-sync-max-replicas", "0",
				"repl-diskless-sync-delay", syncJamDelay) != "OK" {
				return false, nil
			}
			return configValue(tc.valkeyExecQuick(t, namespace, pod1, 6379,
				"CONFIG", "GET", "repl-diskless-sync-delay")) == syncJamDelay, nil
		})
	require.NoError(t, err,
		"the replacement %s on %s never accepted the sync jam; without it the test cannot reach the abandon path",
		pod1, updatedImage)

	role := "unknown"
	for _, line := range strings.Split(tc.valkeyExecQuick(t, namespace, pod1, 6379, "INFO", "replication"), "\n") {
		if strings.HasPrefix(line, "role:") {
			role = strings.TrimSpace(strings.TrimPrefix(line, "role:"))
		}
	}
	t.Logf("Jammed full syncs from %s (image %s, repl-diskless-sync-delay %s -> %s) while it answered role:%s",
		pod1, updatedImage, original, syncJamDelay, role)

	return func() {
		t.Helper()
		require.Equal(t, "OK", tc.valkeyExec(t, namespace, pod1, 6379,
			"CONFIG", "SET", "repl-diskless-sync-delay", original),
			"the sync jam on %s must be lifted before pod-0 is expected to sync", pod1)
	}
}

// valkeyStatusCondition returns one status condition of a Valkey CR by type, or nil
// while the CR has no status or no such condition yet.
//
// It is the only condition reader in the suite: admission_recovery_test.go used to
// carry a ReconcileBlocked-specific twin of it, which was folded onto this one
// (docs/adr/0010-every-rolling-update-wait-is-bounded.md, D14) the same way ADR 0017 D25, D26
// folded the duplicated Event pollers.
func (tc *testClients) valkeyStatusCondition(t *testing.T, namespace, name, condType string) map[string]interface{} {
	t.Helper()

	cr, err := tc.dynamic.Resource(valkeyGVR).Namespace(namespace).Get(
		context.Background(), name, metav1.GetOptions{})
	if err != nil {
		return nil
	}
	conditions, found, err := unstructured.NestedSlice(cr.Object, "status", "conditions")
	if err != nil || !found {
		return nil
	}
	for _, raw := range conditions {
		cond, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		if cond["type"] == condType {
			return cond
		}
	}
	return nil
}

// waitForValkeyCondition polls until the named status condition reaches the
// expected status, and returns it.
func (tc *testClients) waitForValkeyCondition(t *testing.T, namespace, name, condType, status string,
	timeout time.Duration) map[string]interface{} {
	t.Helper()

	var last map[string]interface{}
	err := wait.PollUntilContextTimeout(context.Background(), pollInterval, timeout, true,
		func(_ context.Context) (bool, error) {
			last = tc.valkeyStatusCondition(t, namespace, name, condType)
			return last != nil && last["status"] == status, nil
		})
	require.NoError(t, err,
		"Valkey %s/%s did not report %s=%s within %v (last: %v)",
		namespace, name, condType, status, timeout, last)
	return last
}
