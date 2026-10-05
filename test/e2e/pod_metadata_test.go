//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/guided-traffic/valkey-operator/test/testimages"
)

// TestE2E_PodMetadataChangeRollsBothTiers changes spec.podLabels and
// spec.sentinel.podLabels on a three-pod Sentinel cluster, then removes them. The data
// and Sentinel StatefulSets use OnDelete, so the statefulset-controller applies template
// metadata to no running pod: before the pod metadata record
// (docs/adr/0007-failover-aware-rolling-update.md, D2) the operator rewrote both
// templates, rolled nothing, and every pod kept the labels it was born with while the
// StatefulSets reported a revision no pod ran.
//
// The convergence assertion is the revision each pod carries against
// status.updateRevision, and status.updatedReplicas. status.currentRevision is not
// asserted: under OnDelete the statefulset-controller never advances it.
func TestE2E_PodMetadataChangeRollsBothTiers(t *testing.T) {
	t.Parallel()
	tc := newTestClients(t)
	ns := "e2e-pod-metadata"
	cleanup := tc.createNamespace(t, ns)
	defer cleanup()

	name := "meta"
	sentinelSts := name + "-sentinel"
	const key = "e2e.vko.gtrfc.com/metadata"

	tc.createValkey(t, ns, buildValkeyObject(name, ns, map[string]interface{}{
		"replicas": int64(3),
		"image":    testimages.Default(),
		"sentinel": map[string]interface{}{"enabled": true, "replicas": int64(3)},
	}))
	defer tc.deleteValkey(t, ns, name)

	tc.waitForStatefulSetReady(t, ns, name, 3)
	tc.waitForStatefulSetReady(t, ns, sentinelSts, 3)
	tc.waitForValkeyPhase(t, ns, name, "OK")
	master := tc.findMasterPod(t, ns, name, 3)
	tc.waitForConnectedReplicas(t, ns, master, 6379, 2)
	tc.waitForSentinelSlaves(t, ns, name, 2)

	t.Run("an added label reaches every data and Sentinel pod", func(t *testing.T) {
		started := metav1.Now()
		dataUIDs := tc.dataPodUIDs(t, ns, name, 3)
		sentinelUIDs := tc.dataPodUIDs(t, ns, sentinelSts, 3)

		tc.patchValkeySpec(t, ns, name, map[string]interface{}{
			"podLabels":          map[string]interface{}{key: "data"},
			"sentinel.podLabels": map[string]interface{}{key: "sentinel"},
		})

		tc.waitForTierOnUpdateRevision(t, ns, name, 3, key, "data")
		tc.waitForTierOnUpdateRevision(t, ns, sentinelSts, 3, key, "sentinel")
		tc.requireRollCompletedSince(t, ns, name, started)

		assert.Positive(t, tc.countValkeyEventsSince(t, ns, name, "FailoverTriggered", started),
			"the data tier must have taken the failover-aware roll (ADR 0007 D1)")
		requireAllReplaced(t, dataUIDs, tc.dataPodUIDs(t, ns, name, 3))
		requireAllReplaced(t, sentinelUIDs, tc.dataPodUIDs(t, ns, sentinelSts, 3))
	})

	t.Run("a removed label leaves every data and Sentinel pod", func(t *testing.T) {
		started := metav1.Now()
		tc.patchValkeySpec(t, ns, name, map[string]interface{}{
			"podLabels":          map[string]interface{}{},
			"sentinel.podLabels": map[string]interface{}{},
		})

		tc.waitForTierOnUpdateRevision(t, ns, name, 3, key, "")
		tc.waitForTierOnUpdateRevision(t, ns, sentinelSts, 3, key, "")
		tc.requireRollCompletedSince(t, ns, name, started)
	})

	t.Run("the cluster settles to OK with one master", func(t *testing.T) {
		tc.waitForValkeyPhaseAfterRollingUpdate(t, ns, name, "OK")
		master := tc.findMasterPod(t, ns, name, 3)
		tc.waitForConnectedReplicas(t, ns, master, 6379, 2)
	})

	t.Run("a clean roll raises no Warning Event", func(t *testing.T) {
		tc.requireNoWarningEvents(t, ns, name)
	})
}

// waitForTierOnUpdateRevision waits until every ordinal of the StatefulSet is Ready, runs
// status.updateRevision and carries label key with value want -- or, for an empty want,
// does not carry key at all.
func (tc *testClients) waitForTierOnUpdateRevision(t *testing.T, namespace, stsName string,
	replicas int, key, want string) {
	t.Helper()
	pollUntil(t, pollInterval, rollingUpdateTimeout, func() (bool, string) {
		sts, err := tc.kube.AppsV1().StatefulSets(namespace).Get(context.Background(), stsName, metav1.GetOptions{})
		if err != nil {
			return false, err.Error()
		}
		if sts.Status.ObservedGeneration < sts.Generation || sts.Status.UpdatedReplicas != int32(replicas) ||
			sts.Status.ReadyReplicas != int32(replicas) {
			return false, fmt.Sprintf("generation %d observed %d, updated %d, ready %d of %d", sts.Generation,
				sts.Status.ObservedGeneration, sts.Status.UpdatedReplicas, sts.Status.ReadyReplicas, replicas)
		}
		for i := 0; i < replicas; i++ {
			if ok, why := podOnRevisionWithLabel(tc, namespace, sts, i, key, want); !ok {
				return false, why
			}
		}
		return true, ""
	}, "%s/%s never ran every pod on its update revision with %s=%q", namespace, stsName, key, want)
}

func podOnRevisionWithLabel(tc *testClients, namespace string, sts *appsv1.StatefulSet, ordinal int,
	key, want string) (bool, string) {
	podName := fmt.Sprintf("%s-%d", sts.Name, ordinal)
	pod, err := tc.kube.CoreV1().Pods(namespace).Get(context.Background(), podName, metav1.GetOptions{})
	if err != nil {
		return false, err.Error()
	}
	if rev := pod.Labels[appsv1.StatefulSetRevisionLabel]; rev != sts.Status.UpdateRevision {
		return false, fmt.Sprintf("%s runs revision %s, update revision is %s", podName, rev, sts.Status.UpdateRevision)
	}
	got, has := pod.Labels[key]
	if want == "" && has {
		return false, fmt.Sprintf("%s still carries %s=%q", podName, key, got)
	}
	if want != "" && got != want {
		return false, fmt.Sprintf("%s carries %s=%q, want %q", podName, key, got, want)
	}
	return true, ""
}

// requireRollCompletedSince waits for both tier completion Events after a point in time.
func (tc *testClients) requireRollCompletedSince(t *testing.T, namespace, name string, since metav1.Time) {
	t.Helper()
	for _, reason := range []string{"RollingUpdateComplete", "SentinelUpdateComplete"} {
		pollUntil(t, pollInterval, rollingUpdateTimeout, func() (bool, string) {
			n := tc.countValkeyEventsSince(t, namespace, name, reason, since)
			return n > 0, fmt.Sprintf("%d %s Events", n, reason)
		}, "%s/%s emitted no %s after %s", namespace, name, reason, since)
	}
}

func requireAllReplaced(t *testing.T, before, after map[string]string) {
	t.Helper()
	require.Len(t, after, len(before))
	for pod, uid := range before {
		assert.NotEqual(t, uid, after[pod], "%s must have been replaced", pod)
	}
}
