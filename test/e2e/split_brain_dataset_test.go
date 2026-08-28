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
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/guided-traffic/valkey-operator/test/testimages"
)

// This file is the field coverage of ADR 0028: a demotion may not discard the only
// dataset.
//
// The unit tier can construct the two masters and the key counts directly, and does
// (internal/controller/split_brain_dataset_test.go). What it cannot produce is the
// mechanism that made the loss silent: the recorded master is what points the replica
// ConfigMap, so a recorded master that comes back without its dataset takes the
// init-script self-claim and returns as an EMPTY master -- and the resolver then named
// it and demoted the pod that held the data. That needs a real kubelet, a real init
// container and a real sidecar drain, so it is an e2e or it is nothing
// (docs/adr/0017-test-and-ci-policy.md).

const (
	// splitBrainRollStateTimeout covers the CR write of the rolling-update state. The
	// first pass after the image patch sets it, so this is a scheduling budget.
	splitBrainRollStateTimeout = 3 * time.Minute

	// splitBrainRecoveryTimeout covers the killed pod being rescheduled, the operator
	// adopting the drain promotion and the rolling update finishing on top of it.
	splitBrainRecoveryTimeout = 8 * time.Minute
)

// TestE2E_SplitBrain_RecordedMasterReturningEmptyKeepsTheDataset reproduces item T11:
// the recorded master is deleted mid-roll on a cluster without persistence, its sidecar
// drain promotes the peer that holds the data, and the pod comes back on an empty
// volume still named by vko.gtrfc.com/known-master -- reporting master, because the
// replica ConfigMap names it.
//
// Before ADR 0028 the rolling-update resolver picked the recorded name unconditionally
// and sent REPLICAOF to the pod holding every key. The end state was phase OK,
// replication up and DBSIZE 0 everywhere: a healthy-looking cluster with nothing in it.
// The assertion is therefore on the dataset, not on the roles -- the roles were already
// correct in the incident.
func TestE2E_SplitBrain_RecordedMasterReturningEmptyKeepsTheDataset(t *testing.T) {
	t.Parallel()
	tc := newTestClients(t)

	ns := "e2e-sb-dataset"
	cleanup := tc.createNamespace(t, ns)
	defer cleanup()

	const replicas = 3
	name := "sb-empty"
	payload := map[string]string{
		"sb28:key:0": "must-survive-0",
		"sb28:key:1": "must-survive-1",
		"sb28:key:2": "must-survive-2",
	}

	// No persistence, deliberately: it is what makes the returning pod empty without
	// having to destroy a volume by hand, and it is the exposure ADR 0028 names --
	// a non-persistent cluster, a recreated PVC or a changed storageClass all reach
	// the same state.
	t.Log("Creating a 3-replica Valkey CR without Sentinel and without persistence")
	tc.createValkey(t, ns, buildValkeyObject(name, ns, map[string]interface{}{
		"replicas": int64(replicas),
		"image":    testimages.UpgradeFrom,
	}))
	defer tc.deleteValkey(t, ns, name)

	tc.waitForStatefulSetReady(t, ns, name, replicas)
	tc.waitForValkeyPhase(t, ns, name, "OK")

	initialMaster := tc.findMasterPod(t, ns, name, replicas)
	tc.waitForConnectedReplicas(t, ns, initialMaster, 6379, replicas-1)
	t.Logf("Initial master: %s", initialMaster)

	// The payload has to be on the replicas before the drain promotes one, otherwise
	// a later read from the promoted master proves nothing.
	tc.valkeyMSET(t, ns, initialMaster, 6379, payload)
	for i := 0; i < replicas; i++ {
		podName := fmt.Sprintf("%s-%d", name, i)
		if podName == initialMaster {
			continue
		}
		require.Eventually(t, func() bool {
			return tc.valkeyExecQuick(t, ns, podName, 6379, "GET", "sb28:key:0") == "must-survive-0"
		}, time.Minute, pollInterval, "the payload must reach %s before the master is killed", podName)
	}

	// The roll is what routes the split brain into detectAndResolveSplitBrain rather
	// than into checkSteadyStateSplitBrain, which resolves this shape correctly on its
	// own rules and would make the test pass without the fix.
	t.Log("Triggering the rolling update the split brain has to land inside")
	tc.updateValkeyImage(t, ns, name, testimages.UpgradeTo)

	recorded := tc.waitForRecordedMasterDuringRoll(t, ns, name)
	t.Logf("Rolling update in flight; the recorded master is %s", recorded)

	killedUID := tc.getPod(t, ns, recorded).UID
	t.Logf("Deleting the recorded master %s mid-roll", recorded)
	tc.deletePod(t, ns, recorded)

	// Without this the rest of the test is unreadable: a drain that did not run leaves
	// no second master and therefore no split brain to resolve.
	tc.requireDrainPromotedAReplica(t, ns, name, replicas, recorded)
	tc.waitForPodRecreated(t, ns, recorded, killedUID)

	t.Run("the dataset survives", func(t *testing.T) {
		// Polled rather than read once: the returning pod reports master for a while,
		// and the operator has to converge on top of that.
		var lastState string
		err := wait.PollUntilContextTimeout(context.Background(), pollInterval,
			splitBrainRecoveryTimeout, true, func(ctx context.Context) (bool, error) {
				masters, holders := 0, 0
				var state strings.Builder
				for i := 0; i < replicas; i++ {
					podName := fmt.Sprintf("%s-%d", name, i)
					info := tc.valkeyExecQuick(t, ns, podName, 6379, "INFO", "replication")
					size := tc.valkeyExecQuick(t, ns, podName, 6379, "DBSIZE")
					if strings.Contains(info, "role:master") {
						masters++
					}
					if size != "0" && size != "" {
						holders++
					}
					fmt.Fprintf(&state, "%s dbsize=%s master=%t; ",
						podName, size, strings.Contains(info, "role:master"))
				}
				lastState = state.String()
				return masters == 1 && holders == replicas, nil
			})
		require.NoError(t, err,
			"the cluster never settled on one master holding the dataset; last state: %s", lastState)

		// The verdict of the incident: the keys, read from whoever ended up master.
		master := tc.findMasterPod(t, ns, name, replicas)
		for key, want := range payload {
			assert.Equal(t, want, tc.valkeyExec(t, ns, master, 6379, "GET", key),
				"key %s must survive a recorded master that returned empty", key)
		}
	})

	t.Run("the rolling update still finishes", func(t *testing.T) {
		// The refusal of ADR 0028 D8 re-enters the deadlock it broke, so this is the
		// assertion that its bounds are real: a cluster that keeps its data and never
		// finishes an update is not a fixed cluster.
		tc.waitForAllPodsImage(t, ns, name, replicas, testimages.UpgradeTo)
		tc.waitForValkeyPhaseAfterRollingUpdate(t, ns, name, "OK")

		annotations := tc.getValkeyAnnotations(t, ns, name)
		assert.Empty(t, annotations[annotationRollingUpdateStateKey],
			"the rolling update state must be cleared once the update completes")

		master := tc.findMasterPod(t, ns, name, replicas)
		assert.Equal(t,
			fmt.Sprintf("%s.%s-headless.%s.svc.cluster.local", master, name, ns),
			annotations[annotationKnownMasterKey],
			"the record must name the pod that actually holds the master role, otherwise the next "+
				"restart resurrects the loop this test exists for")
	})
}

// waitForRecordedMasterDuringRoll blocks until the CR carries a rolling-update state
// and returns the pod the known-master annotation names, which is the authority the
// resolver is fed (docs/adr/0008-known-master-annotation-is-the-recorded-authority.md, D10).
//
// Both halves are required. Without the state annotation the split brain would be
// resolved by checkSteadyStateSplitBrain, which refuses this shape on its own rules --
// the test would pass on an unfixed operator. Without the recorded name there is no
// authority to make empty.
func (tc *testClients) waitForRecordedMasterDuringRoll(t *testing.T, namespace, name string) string {
	t.Helper()

	recorded := ""
	err := wait.PollUntilContextTimeout(context.Background(), 500*time.Millisecond,
		splitBrainRollStateTimeout, true, func(_ context.Context) (bool, error) {
			annotations := tc.getValkeyAnnotations(t, namespace, name)
			if annotations[annotationRollingUpdateStateKey] == "" {
				return false, nil
			}
			host := annotations[annotationKnownMasterKey]
			if host == "" {
				return false, nil
			}
			recorded = strings.SplitN(host, ".", 2)[0]
			return true, nil
		})
	require.NoError(t, err,
		"the rolling update never reported a state with a recorded master on Valkey %s/%s",
		namespace, name)
	return recorded
}
