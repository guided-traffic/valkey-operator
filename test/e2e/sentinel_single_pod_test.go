//go:build e2e

package e2e

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/guided-traffic/valkey-operator/test/testimages"
)

// TestE2E_RollingUpdate_SentinelSingleDataPod rolls a Sentinel cluster with one data pod --
// the default shape of a CR that enables Sentinel without setting spec.replicas -- through
// an image change. It is rolled as a single pod
// (docs/adr/0007-failover-aware-rolling-update.md, D11): the only data pod is replaced
// without a failover, the key it saved to its volume survives, Sentinel names it as the
// master again, and the data tier completes before the Sentinel tier is replaced. The
// failover roll this topology used to take asked Sentinel for a failover it had no replica
// for, about every 15 s, and neither tier ever took the change.
func TestE2E_RollingUpdate_SentinelSingleDataPod(t *testing.T) {
	t.Parallel()
	tc := newTestClients(t)
	ns := "e2e-sentinel-single-pod"
	cleanup := tc.createNamespace(t, ns)
	defer cleanup()

	name := "sen-one"
	sentinelSts := name + "-sentinel"
	tc.createValkey(t, ns, buildValkeyObject(name, ns, map[string]interface{}{
		"replicas": int64(1),
		"image":    testimages.UpgradeFrom,
		"sentinel": map[string]interface{}{"enabled": true, "replicas": int64(3)},
		"persistence": map[string]interface{}{
			"enabled": true, "mode": "rdb", "size": "256Mi",
		},
	}))
	defer tc.deleteValkey(t, ns, name)

	tc.waitForStatefulSetReady(t, ns, name, 1)
	tc.waitForStatefulSetReady(t, ns, sentinelSts, 3)
	tc.waitForValkeyPhase(t, ns, name, "OK")

	pod0 := name + "-0"
	require.Equal(t, "OK", tc.valkeyExec(t, ns, pod0, 6379, "SET", "single-pod", "kept"))
	// The snapshot is what carries the key across the restart; nothing replicates it.
	require.Equal(t, "OK", tc.valkeyExec(t, ns, pod0, 6379, "SAVE"))
	dataUID := tc.getPod(t, ns, pod0).UID
	sentinelUIDs := tc.dataPodUIDs(t, ns, sentinelSts, 3)
	updateStarted := metav1.Now()

	tc.updateValkeyImage(t, ns, name, testimages.UpgradeTo)

	t.Run("the only data pod is replaced without a failover", func(t *testing.T) {
		tc.waitForPodRecreated(t, ns, pod0, dataUID)
		tc.waitForAllPodsImage(t, ns, name, 1, testimages.UpgradeTo)
		tc.waitForValkeyEvent(t, ns, name, "RollingUpdateComplete", rollingUpdateTimeout,
			"the data tier of %s/%s never completed its roll", ns, name)
		assert.Zero(t, tc.countValkeyEventsSince(t, ns, name, "FailoverTriggered", updateStarted),
			"one data pod has nothing to fail over to")
	})

	t.Run("the Sentinel tier is replaced after the data tier", func(t *testing.T) {
		tc.waitForValkeyEvent(t, ns, name, "SentinelUpdateComplete", rollingUpdateTimeout,
			"the Sentinel tier of %s/%s never completed its roll", ns, name)
		// Every replaced Sentinel pod was created after the data tier completed; the
		// creation time has second precision, so the event time is truncated to match.
		dataDone := tc.firstValkeyEventTime(t, ns, name, "RollingUpdateComplete").Truncate(time.Second)
		after := tc.dataPodUIDs(t, ns, sentinelSts, 3)
		for pod, uid := range sentinelUIDs {
			assert.NotEqual(t, uid, after[pod], "%s must have been replaced", pod)
			replaced := tc.getPod(t, ns, pod)
			assert.Equal(t, testimages.UpgradeTo, containerImage(replaced, "sentinel"))
			assert.False(t, replaced.CreationTimestamp.Time.Before(dataDone),
				"%s was created at %s, before the data tier completed at %s", pod, replaced.CreationTimestamp, dataDone)
		}
		tc.waitForValkeyPhaseAfterRollingUpdate(t, ns, name, "OK")
	})

	t.Run("the saved key survived and Sentinel names the pod as master", func(t *testing.T) {
		assert.Equal(t, "kept", tc.valkeyExec(t, ns, pod0, 6379, "GET", "single-pod"))
		pollUntil(t, 2*time.Second, 2*time.Minute, func() (bool, string) {
			raw := tc.valkeyExecAllowError(t, ns, sentinelSts+"-0", 26379,
				"SENTINEL", "get-master-addr-by-name", name)
			addr := strings.TrimSpace(strings.Split(raw, "\n")[0])
			return strings.Contains(addr, pod0), addr
		}, "Sentinel does not name %s as the master", pod0)
	})

	t.Run("a clean roll raises no Warning Event", func(t *testing.T) {
		tc.requireNoWarningEvents(t, ns, name)
	})
}

// firstValkeyEventTime is the time of the earliest Event with the given reason on the CR.
func (tc *testClients) firstValkeyEventTime(t *testing.T, namespace, name, reason string) time.Time {
	t.Helper()
	events, err := tc.kube.EventsV1().Events(namespace).List(context.Background(), metav1.ListOptions{})
	require.NoError(t, err)
	var first time.Time
	for i := range events.Items {
		ev := &events.Items[i]
		if ev.Regarding.Kind != "Valkey" || ev.Regarding.Name != name || ev.Reason != reason {
			continue
		}
		at := ev.EventTime.Time
		if at.IsZero() {
			at = ev.DeprecatedFirstTimestamp.Time
		}
		if first.IsZero() || at.Before(first) {
			first = at
		}
	}
	require.False(t, first.IsZero(), "no %s Event on %s/%s", reason, namespace, name)
	return first
}
