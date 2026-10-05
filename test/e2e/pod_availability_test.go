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
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/guided-traffic/valkey-operator/test/testimages"
)

// This file covers docs/adr/0026-a-pod-being-deleted-is-not-available.md, D11 (T32)
// on a running cluster: a replaced pod that never becomes available is reported
// within spec.rollingUpdate.syncTimeout, holds the Sentinel roll, and is replaced by
// the operator once the spec is fixed -- the test deletes nothing itself.

// availabilitySyncTimeout is spec.rollingUpdate.syncTimeout for this test, two-sided
// like topologyAbandonSyncTimeout. Downward it is the time from the stuck pod's
// creation to the condition, so a shorter one reaches the assertion sooner. Upward
// the same budget bounds the replica sync after the spec fix: the replacement of
// the stuck pod has to be synced within it or the roll pauses and the phase goes
// Error. 60 s is well above a 100-key full sync on Kind.
const availabilitySyncTimeout = "60s"

// unpullableValkeyImage names a registry that refuses the connection at once, so
// the pull fails deterministically and without depending on network access or on
// a tag that might one day exist.
const unpullableValkeyImage = "localhost:1/vko-e2e/unpullable:0"

const (
	// availabilityStallTimeout covers the delete of the first replica, the creation
	// of its replacement, the syncTimeout above measured from the replacement's own
	// not-Ready clock, and the requeue after it.
	availabilityStallTimeout = 4 * time.Minute

	// availabilityHoldWindow is how long the test keeps watching after the report,
	// to make sure the Sentinel tier is held for more than one pass.
	availabilityHoldWindow = 30 * time.Second
)

// TestE2E_RollingUpdate_UnavailableReplacementIsReportedAndReplaced drives a 3+3
// Sentinel cluster onto an image that cannot be pulled and back.
func TestE2E_RollingUpdate_UnavailableReplacementIsReportedAndReplaced(t *testing.T) {
	t.Parallel()
	tc := newTestClients(t)

	ns := "e2e-pod-availability"
	cleanup := tc.createNamespace(t, ns)
	defer cleanup()

	const replicas = 3
	name := "avail"
	sentinelSts := fmt.Sprintf("%s-sentinel", name)
	image := testimages.Default()

	tc.createValkey(t, ns, buildValkeyObject(name, ns, map[string]interface{}{
		"replicas": int64(replicas),
		"image":    image,
		"sentinel": map[string]interface{}{
			"enabled":  true,
			"replicas": int64(3),
		},
		"rollingUpdate": map[string]interface{}{
			"syncTimeout": availabilitySyncTimeout,
		},
	}))
	defer tc.deleteValkey(t, ns, name)

	tc.waitForStatefulSetReady(t, ns, name, replicas)
	tc.waitForStatefulSetReady(t, ns, sentinelSts, 3)
	tc.waitForValkeyPhase(t, ns, name, "OK")

	master := tc.findMasterPod(t, ns, name, replicas)
	tc.waitForConnectedReplicas(t, ns, master, 6379, replicas-1)

	const numKeys = 100
	data := make(map[string]string, numKeys)
	for i := 0; i < numKeys; i++ {
		data[fmt.Sprintf("availability:key:%d", i)] = fmt.Sprintf("value-%d", i)
	}
	tc.valkeyMSET(t, ns, master, 6379, data)
	tc.waitForConnectedReplicas(t, ns, master, 6379, replicas-1)

	sentinelUIDs := tc.dataPodUIDs(t, ns, sentinelSts, 3)

	t.Log("Setting spec.image to an image that cannot be pulled")
	tc.updateValkeyImage(t, ns, name, unpullableValkeyImage)

	var stuckPod, stuckUID string
	t.Run("The replacement that never comes up is reported", func(t *testing.T) {
		cond := tc.waitForValkeyCondition(t, ns, name, "PodAvailabilityStalled", "True", availabilityStallTimeout)
		assert.Equal(t, "ValkeyPodNotAvailable", cond["reason"])

		stuckPod = tc.podOnImage(t, ns, name, replicas, unpullableValkeyImage)
		require.NotEmpty(t, stuckPod, "exactly the replaced replica runs the unpullable image")
		assert.NotEqual(t, master, stuckPod, "the roll replaces replicas first, never the master")
		message, _ := cond["message"].(string)
		assert.Contains(t, message, stuckPod, "the condition names the pod the roll is waiting on")
		stuckUID = string(tc.getPod(t, ns, stuckPod).UID)
	})

	t.Run("The Sentinel roll is held while the data tier holds", func(t *testing.T) {
		// The Sentinel template moved with spec.image too; a Sentinel roll released
		// by the stall would take a healthy Sentinel onto the same image.
		deadline := time.Now().Add(availabilityHoldWindow)
		for time.Now().Before(deadline) {
			assert.Equal(t, sentinelUIDs, tc.dataPodUIDs(t, ns, sentinelSts, 3),
				"no Sentinel pod may be replaced while the data tier is stalled")
			time.Sleep(5 * time.Second)
		}
		assert.Equal(t, 1, tc.countPodsOnImage(t, ns, name, replicas, unpullableValkeyImage),
			"the stall holds the next data replica too")
	})

	// Conflict-retrying: during the reported stall every pass writes the status
	// (twice, the inherited phase alternation), and a plain Get-then-Update of the
	// CR loses that race.
	t.Log("Putting spec.image back")
	tc.patchValkeySpec(t, ns, name, map[string]interface{}{"image": image})

	t.Run("The operator replaces the stuck pod and the roll completes", func(t *testing.T) {
		require.NotEmpty(t, stuckPod)
		err := wait.PollUntilContextTimeout(context.Background(), pollInterval, availabilityStallTimeout, true,
			func(ctx context.Context) (bool, error) {
				pod, err := tc.kube.CoreV1().Pods(ns).Get(ctx, stuckPod, metav1.GetOptions{})
				if err != nil {
					return false, nil
				}
				return string(pod.UID) != stuckUID, nil
			})
		require.NoError(t, err, "the operator must replace the pod that never came up; the test deletes nothing")

		tc.waitForAllPodsImage(t, ns, name, replicas, image)
		tc.waitForStatefulSetReady(t, ns, name, replicas)
		tc.waitForValkeyPhaseAfterRollingUpdate(t, ns, name, "OK")

		cond := tc.waitForValkeyCondition(t, ns, name, "PodAvailabilityStalled", "False", 2*time.Minute)
		assert.Equal(t, "PodAvailable", cond["reason"])
		assert.Equal(t, sentinelUIDs, tc.dataPodUIDs(t, ns, sentinelSts, 3),
			"the Sentinel tier ends where it started: its template is back to what its pods run")
	})

	t.Run("Every replica holds the keys", func(t *testing.T) {
		newMaster := tc.findMasterPod(t, ns, name, replicas)
		tc.waitForConnectedReplicas(t, ns, newMaster, 6379, replicas-1)
		for i := 0; i < replicas; i++ {
			pod := fmt.Sprintf("%s-%d", name, i)
			tc.waitForReplicaSyncedOrMaster(t, ns, pod)
			assert.Equal(t, fmt.Sprintf("%d", numKeys), tc.valkeyExec(t, ns, pod, 6379, "DBSIZE"),
				"pod %s must hold every key after the roll", pod)
		}
	})
}

// podOnImage returns the data pod whose valkey container runs image, or "" when
// none does. The stuck pod is found by what it runs rather than by ordinal: the
// roll picks the youngest replica, which a test must not predict.
func (tc *testClients) podOnImage(t *testing.T, namespace, name string, replicas int, image string) string {
	t.Helper()
	for i := 0; i < replicas; i++ {
		podName := fmt.Sprintf("%s-%d", name, i)
		pod, err := tc.kube.CoreV1().Pods(namespace).Get(context.Background(), podName, metav1.GetOptions{})
		if err != nil {
			continue
		}
		if containerImage(pod, "valkey") == image {
			return podName
		}
	}
	return ""
}

// countPodsOnImage counts the data pods whose valkey container runs image.
func (tc *testClients) countPodsOnImage(t *testing.T, namespace, name string, replicas int, image string) int {
	t.Helper()
	count := 0
	for i := 0; i < replicas; i++ {
		pod, err := tc.kube.CoreV1().Pods(namespace).Get(context.Background(),
			fmt.Sprintf("%s-%d", name, i), metav1.GetOptions{})
		if err == nil && containerImage(pod, "valkey") == image {
			count++
		}
	}
	return count
}

// waitForReplicaSyncedOrMaster waits until the pod is either the master or a
// replica whose link is up and not syncing, so a DBSIZE read on it is final.
func (tc *testClients) waitForReplicaSyncedOrMaster(t *testing.T, namespace, pod string) {
	t.Helper()
	pollUntil(t, 2*time.Second, 2*time.Minute, func() (bool, string) {
		info := tc.valkeyExecQuick(t, namespace, pod, 6379, "INFO", "replication")
		if strings.Contains(info, "role:master") {
			return true, "role:master"
		}
		return strings.Contains(info, "master_link_status:up") && strings.Contains(info, "master_sync_in_progress:0"),
			keepLinesContaining(info, "master_")
	}, "pod %s never finished its replication sync", pod)
}

// pollUntil is the ADR 0017 D25 wait: wait.PollUntilContextTimeout with an explicit
// interval and budget, failing with the last value the condition observed. It
// replaces require.Eventually, whose condition goroutine can outlive the test.
func pollUntil(t *testing.T, interval, timeout time.Duration, cond func() (bool, string),
	format string, args ...interface{}) {
	t.Helper()
	last := ""
	err := wait.PollUntilContextTimeout(context.Background(), interval, timeout, true,
		func(context.Context) (bool, error) {
			ok, observed := cond()
			last = observed
			return ok, nil
		})
	require.NoError(t, err, "%s (last observed: %s)", fmt.Sprintf(format, args...), last)
}

// TestE2E_RollingUpdate_TwoSentinelsRollSerially covers ADR 0024 D10: a Sentinel
// tier of two has no spare vote, so the quorum guard could never pass and the tier
// never rolled. It now rolls one Sentinel at a time, each only while the other is
// available, and the whole update completes.
func TestE2E_RollingUpdate_TwoSentinelsRollSerially(t *testing.T) {
	t.Parallel()
	tc := newTestClients(t)

	ns := "e2e-two-sentinels"
	cleanup := tc.createNamespace(t, ns)
	defer cleanup()

	name := "two-sen"
	sentinelSts := name + "-sentinel"
	tc.createValkey(t, ns, buildValkeyObject(name, ns, map[string]interface{}{
		"replicas": int64(3),
		"image":    testimages.UpgradeFrom,
		"sentinel": map[string]interface{}{"enabled": true, "replicas": int64(2)},
	}))
	defer tc.deleteValkey(t, ns, name)

	tc.waitForStatefulSetReady(t, ns, name, 3)
	tc.waitForStatefulSetReady(t, ns, sentinelSts, 2)
	tc.waitForValkeyPhase(t, ns, name, "OK")
	master := tc.findMasterPod(t, ns, name, 3)
	tc.waitForConnectedReplicas(t, ns, master, 6379, 2)
	assert.Equal(t, "OK", tc.valkeyExec(t, ns, master, 6379, "SET", "two-sentinels", "kept"))
	sentinelUIDs := tc.dataPodUIDs(t, ns, sentinelSts, 2)

	tc.updateValkeyImage(t, ns, name, testimages.UpgradeTo)

	t.Run("both Sentinels are replaced and the update completes", func(t *testing.T) {
		tc.waitForAllPodsImage(t, ns, name, 3, testimages.UpgradeTo)
		tc.waitForValkeyCondition(t, ns, name, "SentinelUpdatePending", "False", rollingUpdateTimeout)
		after := tc.dataPodUIDs(t, ns, sentinelSts, 2)
		for pod, uid := range sentinelUIDs {
			assert.NotEqual(t, uid, after[pod], "%s must have been replaced", pod)
			assert.Equal(t, testimages.UpgradeTo, containerImage(tc.getPod(t, ns, pod), "sentinel"))
		}
		tc.waitForValkeyPhaseAfterRollingUpdate(t, ns, name, "OK")
	})

	t.Run("the data survived", func(t *testing.T) {
		newMaster := tc.findMasterPod(t, ns, name, 3)
		assert.Equal(t, "kept", tc.valkeyExec(t, ns, newMaster, 6379, "GET", "two-sentinels"))
	})
}
