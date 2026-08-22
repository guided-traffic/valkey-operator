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
// the operator's REPLICAOF and then never reports master_link_status:up. That is
// what jamPod0Replication produces, in a form that survives the operator's own
// REPLICAOF and therefore does not race the state transition.

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

// blackholeMasterIP is an unroutable address (RFC 1112 class E) used as the jammed
// replication target, so no packet the replica sends can be answered.
const blackholeMasterIP = "240.0.0.1"

// masterauthPoison is what makes the jam survive the operator's own REPLICAOF: a
// replica with masterauth set sends AUTH during the handshake, the master has no
// requirepass and answers with an error, and the replica aborts the handshake and
// retries forever. master_link_status therefore never reaches "up", whichever
// master the link is pointed at.
const masterauthPoison = "vko-e2e-abandon-poison"

const (
	// jamPollInterval is one cheap CR GET per tick while the master replacement is
	// still running; the kubectl exec only runs once the state gate matches.
	jamPollInterval = 500 * time.Millisecond

	// jamPollTimeout covers the whole replica replacement plus the failover and the
	// recreation of pod-0.
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

	tc.jamPod0Replication(t, ns, name)

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
		assert.Equal(t, "before-update", tc.valkeyExec(t, ns, pod1, 6379, "GET", "abandon:key1"),
			"the pre-update payload must still be served by the promoted master")
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
		// The jam lives in the process only -- the operator never issues CONFIG
		// REWRITE -- so restarting the pod removes it. The recreated pod-0 reads the
		// replica ConfigMap, whose replicaof still names the promoted master.
		tc.deletePod(t, ns, pod0)

		// Two gates, and each closes what the other leaves open. The master-side one
		// rejects the still-terminating old pod-0: it replicates the blackhole and
		// never registers, so waiting on the pod Ready condition first would accept
		// it. It does not prove the dataset arrived, though -- a replica is counted
		// from the moment it asks for synchronization, and the master then sits out
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

// jamPod0Replication breaks the replication link of the returning pod-0 so that
// Phase 1 of the topology restoration can never succeed and has to run into its
// sync timeout.
//
// Two commands, applied together and retried until both are accepted:
//
//   - CONFIG SET masterauth <poison> makes every future handshake fail at AUTH, so
//     the jam holds even when the operator afterwards points pod-0 at the promoted
//     master (that REPLICAOF is a real master change and restarts the handshake).
//   - REPLICAOF <blackhole> 6379 holds on its own when the jam lands after the
//     operator already re-pointed pod-0: nothing re-points it again while the state
//     is restoring-topology.
//
// The state gate is what keeps this safe. promotePod0AndRedirect is the only path
// that makes pod-0 master again, and it moves the state to verifying-topology in
// the same pass, so the gate closes before pod-0 can be master. The residual race
// (state read as restoring-topology, promotion lands during the exec) demotes a
// freshly promoted pod-0 and fails the TopologyRestored assertion loudly; it cannot
// produce a green false pass. Never widen the gate to verifying-topology or to the
// empty state -- there pod-0 may legitimately be master.
//
// If pod-0 is ever seen reporting master_link_status:up after the operator has
// re-pointed it, the masterauth half is not biting and the fallback is to re-issue
// REPLICAOF <blackhole> on a short interval until the abandon fires -- correct, but
// with a small window between the operator's REPLICAOF and the next re-jam.
func (tc *testClients) jamPod0Replication(t *testing.T, namespace, name string) {
	t.Helper()

	pod0 := fmt.Sprintf("%s-0", name)
	err := wait.PollUntilContextTimeout(context.Background(), jamPollInterval, jamPollTimeout, true,
		func(_ context.Context) (bool, error) {
			state := tc.getValkeyAnnotations(t, namespace, name)[annotationRollingUpdateStateKey]
			switch state {
			case "manual-failover", "replacing-master", "restoring-topology":
			default:
				// pod-0 is still the master, or it was already promoted back.
				return false, nil
			}

			info := tc.valkeyExecQuick(t, namespace, pod0, 6379, "INFO", "replication")
			if !strings.Contains(info, "role:slave") {
				return false, nil
			}

			// Both commands are idempotent, so a partially applied jam is simply
			// re-applied on the next tick.
			if tc.valkeyExecQuick(t, namespace, pod0, 6379, "CONFIG", "SET", "masterauth", masterauthPoison) != "OK" {
				return false, nil
			}
			if tc.valkeyExecQuick(t, namespace, pod0, 6379, "REPLICAOF", blackholeMasterIP, "6379") != "OK" {
				return false, nil
			}

			t.Logf("Jammed replication on %s while the rolling update was in state %q", pod0, state)
			return true, nil
		})
	require.NoError(t, err,
		"%s never came back as a replica while the manual failover was in flight; "+
			"without the jam the test cannot reach the abandon path", pod0)
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
