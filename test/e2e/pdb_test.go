//go:build e2e

package e2e

// Tests in this file cover the PodDisruptionBudgets the operator manages for the
// data and Sentinel StatefulSets (scenario T3 of the admission-gap ticket).
//
// Incident this guards against (infra-d, 2026-08-19): a single node drain evicted
// all three data pods at once, because nothing serialized the evictions. The
// Eviction API is the only mechanism that does — and only when a PodDisruptionBudget
// covers the pods. The Sentinel budget is the second half: losing the Sentinel
// majority in one drain removes automatic failover exactly when it is needed.
//
// The assertions use the Eviction API directly rather than `kubectl drain`, so they
// hold on a single-node cluster as well: a budget is enforced per pod set, not per
// node. The multi-node Kind config (Makefile kind-create) is what makes the real
// drain path reproducible locally.

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/wait"
)

// pdbSettleTimeout is how long the PDB controller may take to publish a status
// (currentHealthy / disruptionsAllowed) that matches the pods.
const pdbSettleTimeout = 60 * time.Second

// pdbSkipObservationWindow is how long a single-replica instance is watched to
// prove that no PDB appears — long enough to cover several reconcile passes.
const pdbSkipObservationWindow = 30 * time.Second

// TestE2E_PodDisruptionBudget_SerializesEvictions is scenario T3: with
// spec.podDisruptionBudget.enabled, both budgets exist and the Eviction API
// refuses the second concurrent disruption for data pods and for Sentinel.
func TestE2E_PodDisruptionBudget_SerializesEvictions(t *testing.T) {
	t.Parallel()
	tc := newTestClients(t)

	ns := "e2e-pdb"
	cleanup := tc.createNamespace(t, ns)
	defer cleanup()

	name := "pdb-test"
	t.Log("Creating an HA Valkey CR with PodDisruptionBudgets enabled")
	tc.createValkey(t, ns, buildValkeyObject(name, ns, map[string]interface{}{
		"replicas": int64(3),
		"image":    "valkey/valkey:8.0",
		"sentinel": map[string]interface{}{
			"enabled":  true,
			"replicas": int64(3),
		},
		"podDisruptionBudget": map[string]interface{}{
			"enabled": true,
		},
	}))
	defer tc.deleteValkey(t, ns, name)

	tc.waitForStatefulSetReady(t, ns, name, 3)
	tc.waitForStatefulSetReady(t, ns, name+"-sentinel", 3)

	t.Run("both budgets exist with the documented shape", func(t *testing.T) {
		data := tc.waitForPodDisruptionBudget(t, ns, name, func(pdb *policyv1.PodDisruptionBudget) bool {
			return pdb.Status.CurrentHealthy == 3 && pdb.Status.DisruptionsAllowed == 1
		}, "data PDB never reported 3 healthy pods with 1 allowed disruption")
		require.NotNil(t, data.Spec.MaxUnavailable, "data PDB must use maxUnavailable")
		assert.Equal(t, "1", data.Spec.MaxUnavailable.String())
		assert.Nil(t, data.Spec.MinAvailable, "data PDB must not use minAvailable")

		sentinel := tc.waitForPodDisruptionBudget(t, ns, name+"-sentinel", func(pdb *policyv1.PodDisruptionBudget) bool {
			return pdb.Status.CurrentHealthy == 3 && pdb.Status.DisruptionsAllowed == 1
		}, "sentinel PDB never reported 3 healthy pods with 1 allowed disruption")
		require.NotNil(t, sentinel.Spec.MinAvailable, "sentinel PDB must use the quorum as minAvailable")
		assert.Equal(t, "2", sentinel.Spec.MinAvailable.String(), "quorum of 3 sentinels is 2")
		assert.Nil(t, sentinel.Spec.MaxUnavailable, "sentinel PDB must not use maxUnavailable")
	})

	t.Run("second data eviction is refused while one pod is down", func(t *testing.T) {
		// Evict replicas, not the master: a master eviction would additionally
		// trigger a failover and is not what this assertion is about.
		victims := tc.replicaPodNames(t, ns, name, 2)
		tc.assertSecondEvictionRefused(t, ns, name, victims[0], victims[1])
	})

	// Recover before the sentinel round so the sentinel budget starts from a full
	// pod set rather than from a data-plane still catching up.
	tc.waitForStatefulSetReady(t, ns, name, 3)

	t.Run("second sentinel eviction is refused while one is down", func(t *testing.T) {
		sentinels := tc.podNamesForComponent(t, ns, name, "sentinel")
		require.GreaterOrEqual(t, len(sentinels), 2, "need at least two sentinel pods")
		tc.assertSecondEvictionRefused(t, ns, name+"-sentinel", sentinels[0], sentinels[1])
	})

	tc.waitForStatefulSetReady(t, ns, name+"-sentinel", 3)

	t.Run("budgets are removed when disabled", func(t *testing.T) {
		tc.patchValkeySpec(t, ns, name, map[string]interface{}{
			"podDisruptionBudget.enabled": false,
		})
		tc.waitForNoPodDisruptionBudget(t, ns, name)
		tc.waitForNoPodDisruptionBudget(t, ns, name+"-sentinel")
	})
}

// TestE2E_PodDisruptionBudget_SkippedForSingleReplica guards the single-replica
// skip rule: with one pod a budget is either useless (maxUnavailable 1) or blocks
// node drains forever (minAvailable 1), so the operator creates none even though
// PDBs are enabled.
func TestE2E_PodDisruptionBudget_SkippedForSingleReplica(t *testing.T) {
	t.Parallel()
	tc := newTestClients(t)

	ns := "e2e-pdb-single"
	cleanup := tc.createNamespace(t, ns)
	defer cleanup()

	name := "pdb-single"
	tc.createValkey(t, ns, buildValkeyObject(name, ns, map[string]interface{}{
		"replicas": int64(1),
		"image":    "valkey/valkey:8.0",
		"podDisruptionBudget": map[string]interface{}{
			"enabled": true,
		},
	}))
	defer tc.deleteValkey(t, ns, name)

	tc.waitForStatefulSetReady(t, ns, name, 1)
	tc.waitForValkeyPhase(t, ns, name, "OK")

	ctx := context.Background()
	assert.Never(t, func() bool {
		list, err := tc.kube.PolicyV1().PodDisruptionBudgets(ns).List(ctx, metav1.ListOptions{})
		return err == nil && len(list.Items) > 0
	}, pdbSkipObservationWindow, pollInterval,
		"no PodDisruptionBudget may be created for a single-replica instance")
}

// assertSecondEvictionRefused evicts firstPod, waits until the budget is spent and
// then requires the eviction of secondPod to be refused by the Eviction API.
func (tc *testClients) assertSecondEvictionRefused(t *testing.T, namespace, pdbName, firstPod, secondPod string) {
	t.Helper()

	t.Logf("Evicting %s/%s (within budget)", namespace, firstPod)
	require.NoError(t, tc.evictPod(namespace, firstPod), "the first eviction must be allowed")

	tc.waitForPodDisruptionBudget(t, namespace, pdbName, func(pdb *policyv1.PodDisruptionBudget) bool {
		return pdb.Status.DisruptionsAllowed == 0
	}, "budget never reported an exhausted disruption allowance after the first eviction")

	t.Logf("Evicting %s/%s (must be refused)", namespace, secondPod)
	err := tc.evictPod(namespace, secondPod)
	require.Error(t, err, "the second concurrent eviction must be refused — this is the drain protection")
	assert.True(t, apierrors.IsTooManyRequests(err),
		"expected a 429 from the Eviction API, got: %v", err)
}

// evictPod requests eviction of a pod through the Eviction API, which is what
// `kubectl drain` and the cluster autoscaler use — and the only path a
// PodDisruptionBudget can gate.
func (tc *testClients) evictPod(namespace, name string) error {
	return tc.kube.CoreV1().Pods(namespace).EvictV1(context.Background(), &policyv1.Eviction{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
	})
}

// waitForPodDisruptionBudget polls a PDB until it satisfies check and returns it.
func (tc *testClients) waitForPodDisruptionBudget(t *testing.T, namespace, name string,
	check func(*policyv1.PodDisruptionBudget) bool, message string) *policyv1.PodDisruptionBudget {
	t.Helper()

	var last *policyv1.PodDisruptionBudget
	err := wait.PollUntilContextTimeout(context.Background(), time.Second, pdbSettleTimeout, true,
		func(ctx context.Context) (bool, error) {
			pdb, err := tc.kube.PolicyV1().PodDisruptionBudgets(namespace).Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				if apierrors.IsNotFound(err) {
					return false, nil
				}
				return false, err
			}
			last = pdb
			return check(pdb), nil
		})
	require.NoError(t, err, "%s (PDB %s/%s, last observed status: %+v)", message, namespace, name, statusOf(last))
	return last
}

// waitForNoPodDisruptionBudget waits until the named PDB is gone.
func (tc *testClients) waitForNoPodDisruptionBudget(t *testing.T, namespace, name string) {
	t.Helper()

	err := wait.PollUntilContextTimeout(context.Background(), pollInterval, pdbSettleTimeout, true,
		func(ctx context.Context) (bool, error) {
			_, err := tc.kube.PolicyV1().PodDisruptionBudgets(namespace).Get(ctx, name, metav1.GetOptions{})
			if apierrors.IsNotFound(err) {
				return true, nil
			}
			return false, nil
		})
	require.NoError(t, err, "PodDisruptionBudget %s/%s was not removed", namespace, name)
}

// statusOf renders a PDB status for assertion messages, tolerating a nil PDB.
func statusOf(pdb *policyv1.PodDisruptionBudget) policyv1.PodDisruptionBudgetStatus {
	if pdb == nil {
		return policyv1.PodDisruptionBudgetStatus{}
	}
	return pdb.Status
}

// podNamesForComponent lists running pods of one component (valkey | sentinel).
func (tc *testClients) podNamesForComponent(t *testing.T, namespace, instance, component string) []string {
	t.Helper()

	selector := labels.SelectorFromSet(labels.Set{
		"app.kubernetes.io/instance":   instance,
		"app.kubernetes.io/managed-by": "vko.gtrfc.com",
		"app.kubernetes.io/component":  component,
	}).String()

	pods, err := tc.kube.CoreV1().Pods(namespace).List(context.Background(),
		metav1.ListOptions{LabelSelector: selector})
	require.NoError(t, err, "Failed to list %s pods in %s", component, namespace)

	names := make([]string, 0, len(pods.Items))
	for i := range pods.Items {
		if pods.Items[i].Status.Phase == corev1.PodRunning {
			names = append(names, pods.Items[i].Name)
		}
	}
	return names
}

// replicaPodNames returns up to count data pods that are not the current master.
func (tc *testClients) replicaPodNames(t *testing.T, namespace, instance string, count int) []string {
	t.Helper()

	status := tc.getValkeyStatus(t, namespace, instance)
	master, _ := status["masterPod"].(string)

	var replicas []string
	for _, name := range tc.podNamesForComponent(t, namespace, instance, "valkey") {
		if name != master {
			replicas = append(replicas, name)
		}
	}
	require.GreaterOrEqual(t, len(replicas), count,
		"need at least %d non-master data pods (master=%s)", count, master)
	return replicas[:count]
}
