//go:build e2e

package e2e

// Tests in this file cover the PodDisruptionBudgets the operator manages for the
// data and Sentinel StatefulSets (docs/adr/0004-opt-in-poddisruptionbudgets.md).
//
// Incident this guards against (infra-d, 2026-08-19): a single node drain evicted
// all three data pods at once, because nothing serialized the evictions. The
// Eviction API is the only mechanism that does — and only when a PodDisruptionBudget
// covers the pods. The Sentinel budget is the second half: losing the Sentinel
// majority in one drain removes automatic failover exactly when it is needed.
//
// The assertions use the Eviction API directly rather than `kubectl drain`, so they
// hold on a single-node cluster as well: a budget is enforced per pod set, not per
// node. A real drain stays out on purpose — it would evict the pods of every other
// e2e test sharing that node, and these tests run in parallel. The multi-node Kind
// config (Makefile kind-create locally, the multi-node CI leg in
// .github/workflows/release.yml) is what makes the real drain path reproducible by
// hand, and it is where CI re-runs these tests with more than one node under them.

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
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/wait"
)

// pdbSettleTimeout is how long the PDB controller may take to publish a status
// (currentHealthy / disruptionsAllowed) that matches the pods.
const pdbSettleTimeout = 60 * time.Second

// pdbSkipObservationWindow is how long a single-replica instance is watched to
// prove that no PDB appears — long enough to cover several reconcile passes.
const pdbSkipObservationWindow = 30 * time.Second

// TestE2E_PodDisruptionBudget_SerializesEvictions is ADR 0004 D1, D2: with
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

// TestE2E_PodDisruptionBudget_LeavesForeignBudgetAlone is ADR 0006 D1, D2: the operator must
// touch only budgets it owns.
//
// Both budget names are the StatefulSet names, which is exactly what a hand-written
// PDB covering the same pods is called — and hand-writing that PDB was the
// remediation for the incident this feature exists for. The cleanup path runs on
// every pass of every CR whose spec.podDisruptionBudget is absent (every CR that
// predates the feature), and it deleted that object by name; the update path adopted
// it by name once the feature was switched on.
//
// The assertions run against a real API server on purpose: the guard is an
// ownerReference comparison, and the unit tests observe it through a fake client
// that neither writes UIDs nor garbage-collects.
func TestE2E_PodDisruptionBudget_LeavesForeignBudgetAlone(t *testing.T) {
	t.Parallel()
	tc := newTestClients(t)

	ns := "e2e-pdb-foreign"
	cleanup := tc.createNamespace(t, ns)
	defer cleanup()

	name := "pdb-foreign"
	t.Log("Creating a hand-written PodDisruptionBudget under the operator's data-budget name")
	tc.createForeignPodDisruptionBudget(t, ns, name)

	// No spec.podDisruptionBudget block at all: this is the shape of every CR that
	// predates the feature, and it puts the cleanup path on every reconcile pass.
	tc.createValkey(t, ns, buildValkeyObject(name, ns, map[string]interface{}{
		"replicas": int64(2),
		"image":    "valkey/valkey:8.0",
	}))
	defer tc.deleteValkey(t, ns, name)

	tc.waitForStatefulSetReady(t, ns, name, 2)
	tc.waitForValkeyPhase(t, ns, name, "OK")

	t.Run("the cleanup path never deletes it", func(t *testing.T) {
		tc.assertForeignPodDisruptionBudgetIntact(t, ns, name)
	})

	t.Run("enabling the feature does not adopt it", func(t *testing.T) {
		tc.patchValkeySpec(t, ns, name, map[string]interface{}{
			"podDisruptionBudget.enabled": true,
		})

		// The Warning Event is the operator's own report that the update path ran
		// and refused; without it the assertion below could pass simply because no
		// reconcile happened yet.
		tc.waitForValkeyEvent(t, ns, name, "PodDisruptionBudgetNotOwned", pdbSettleTimeout,
			"no PodDisruptionBudgetNotOwned Event appeared on Valkey %s/%s", ns, name)
		tc.assertForeignPodDisruptionBudgetIntact(t, ns, name)
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

// evictionRaceAttempts bounds how often assertSecondEvictionRefused repeats its
// two-eviction sequence.
//
// The refusal window is only as long as the evicted pod stays gone or unready:
// once its replacement is Ready the disruption controller republishes
// disruptionsAllowed=1 and a second eviction is legitimately granted. That window
// was measured at 0.1 s for Sentinel pods locally, and nothing in the test
// controls it — so a single pass is a race the operator cannot lose but the test
// can. Retrying makes a lost race cost one more round instead of a red build,
// while a genuinely unenforced budget still fails: it loses every attempt.
const evictionRaceAttempts = 3

// evictionPollInterval is how fast the budget is polled while waiting for the
// first eviction to spend it. It is deliberately far below the 1 s of
// waitForPodDisruptionBudget: the state being observed can be shorter-lived than
// a single 1 s tick.
const evictionPollInterval = 100 * time.Millisecond

// evictionRaceTimeout bounds one attempt's wait for the exhausted budget. It is
// short on purpose — missing the window is a retryable outcome here, not a
// failure, so waiting out pdbSettleTimeout would only delay the next attempt.
const evictionRaceTimeout = 30 * time.Second

// assertSecondEvictionRefused evicts firstPod, waits until the budget is spent and
// then requires the eviction of secondPod to be refused by the Eviction API.
//
// Both halves are retried as a unit (see evictionRaceAttempts): the assertion is
// "while one pod is down, the next eviction is refused", and both ways of losing
// the race — the exhausted budget never observed, or the second eviction granted
// because the replacement was already Ready — mean the precondition was gone, not
// that the budget failed.
func (tc *testClients) assertSecondEvictionRefused(t *testing.T, namespace, pdbName, firstPod, secondPod string) {
	t.Helper()

	sts, err := tc.kube.AppsV1().StatefulSets(namespace).Get(context.Background(), pdbName, metav1.GetOptions{})
	require.NoError(t, err, "the StatefulSet covered by PDB %s/%s must exist", namespace, pdbName)
	require.NotNil(t, sts.Spec.Replicas)
	replicas := *sts.Spec.Replicas

	for attempt := 1; attempt <= evictionRaceAttempts; attempt++ {
		// The disruption controller republishes the budget a moment after the pods
		// are Ready, not with them. A retry that evicts in that gap gets a 429 on
		// the *first* eviction and would report the recovery lag as a defect.
		tc.waitForPodDisruptionBudget(t, namespace, pdbName, func(pdb *policyv1.PodDisruptionBudget) bool {
			return pdb.Status.DisruptionsAllowed > 0
		}, "budget never reported an allowed disruption before the first eviction")

		t.Logf("Attempt %d/%d: evicting %s/%s (within budget)", attempt, evictionRaceAttempts, namespace, firstPod)
		require.NoError(t, tc.evictPod(namespace, firstPod), "the first eviction must be allowed")

		observed, evictErr := tc.evictWhenBudgetExhausted(t, namespace, pdbName, secondPod)
		switch {
		case !observed:
			t.Logf("Budget never reported an exhausted allowance within %s — the replacement pod "+
				"was ready again before the window could be observed; retrying", evictionRaceTimeout)
		case evictErr == nil:
			t.Logf("The second eviction of %s/%s was granted — the budget had recovered before the "+
				"request reached the API server; retrying", namespace, secondPod)
		default:
			assert.True(t, apierrors.IsTooManyRequests(evictErr),
				"expected a 429 from the Eviction API, got: %v", evictErr)
			return
		}

		// Both retryable outcomes leave at least one pod missing, and the granted
		// case leaves two. Start the next attempt from a full, healthy pod set.
		tc.waitForStatefulSetReady(t, namespace, pdbName, replicas)
	}

	t.Fatalf("the second concurrent eviction was never refused in %d attempts — this is the drain "+
		"protection PDB %s/%s exists for", evictionRaceAttempts, namespace, pdbName)
}

// evictWhenBudgetExhausted polls the PDB and requests the eviction of pod as soon
// as it reports no allowed disruption, so the request leaves for the API server
// from the tightest point after the observation.
//
// It reports whether the exhausted budget was observed at all, and the eviction
// error (nil when the eviction was granted). A budget that never reports
// disruptionsAllowed=0 within evictionRaceTimeout returns observed=false and no
// eviction is attempted.
func (tc *testClients) evictWhenBudgetExhausted(t *testing.T, namespace, pdbName, pod string) (bool, error) {
	t.Helper()

	var evictErr error
	observed := false
	waitErr := wait.PollUntilContextTimeout(context.Background(), evictionPollInterval, evictionRaceTimeout, true,
		func(ctx context.Context) (bool, error) {
			pdb, err := tc.kube.PolicyV1().PodDisruptionBudgets(namespace).Get(ctx, pdbName, metav1.GetOptions{})
			if err != nil {
				if apierrors.IsNotFound(err) {
					return false, nil
				}
				return false, err
			}
			if pdb.Status.DisruptionsAllowed != 0 {
				return false, nil
			}
			observed = true
			t.Logf("Budget %s/%s is spent; evicting %s/%s (must be refused)", namespace, pdbName, namespace, pod)
			evictErr = tc.evictPod(namespace, pod)
			return true, nil
		})
	if !observed {
		require.True(t, waitErr != nil && wait.Interrupted(waitErr),
			"polling PDB %s/%s failed for a reason other than the timeout: %v", namespace, pdbName, waitErr)
	}
	return observed, evictErr
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

// createForeignPodDisruptionBudget creates a PDB the operator did not create: no
// ownerReference, minAvailable instead of maxUnavailable and a selector pointing at
// something else, so any operator write to it is visible.
func (tc *testClients) createForeignPodDisruptionBudget(t *testing.T, namespace, name string) {
	t.Helper()

	minAvailable := intstr.FromInt32(2)
	_, err := tc.kube.PolicyV1().PodDisruptionBudgets(namespace).Create(context.Background(),
		&policyv1.PodDisruptionBudget{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
				Labels:    map[string]string{"owner": "platform-team"},
			},
			Spec: policyv1.PodDisruptionBudgetSpec{
				MinAvailable: &minAvailable,
				Selector: &metav1.LabelSelector{
					MatchLabels: map[string]string{"app": "hand-written"},
				},
			},
		}, metav1.CreateOptions{})
	require.NoError(t, err, "failed to create the foreign PodDisruptionBudget %s/%s", namespace, name)
}

// assertForeignPodDisruptionBudgetIntact watches the hand-written budget across
// several reconcile passes and fails on deletion or on any operator write to it.
func (tc *testClients) assertForeignPodDisruptionBudgetIntact(t *testing.T, namespace, name string) {
	t.Helper()
	ctx := context.Background()

	assert.Never(t, func() bool {
		pdb, err := tc.kube.PolicyV1().PodDisruptionBudgets(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			t.Logf("foreign PodDisruptionBudget %s/%s: %v", namespace, name, err)
			return true
		}
		return pdb.Spec.MinAvailable == nil || pdb.Spec.MaxUnavailable != nil ||
			len(pdb.OwnerReferences) > 0 || pdb.Labels["owner"] != "platform-team"
	}, pdbSkipObservationWindow, pollInterval,
		"the operator must neither delete nor rewrite the hand-written PodDisruptionBudget %s/%s",
		namespace, name)
}

// waitForValkeyEvent waits for an Event with the given reason on a Valkey CR. The recorder
// broadcasts asynchronously, hence the poll; Events travel through events.k8s.io/v1, so a missing
// one can also mean missing operator RBAC (docs/adr/0014-rbac-lives-in-three-places.md, D7).
//
// This is the single Event poll of the suite; it lives here because pdb_test.go is
// where it started. Timeout and failure message are the caller's: what a missing
// Event means differs per scenario (see the RBAC wording in
// admission_recovery_test.go), and so does how long the emitting path may take.
func (tc *testClients) waitForValkeyEvent(t *testing.T, namespace, name, reason string,
	timeout time.Duration, failureMsg string, msgArgs ...interface{}) {
	t.Helper()

	err := wait.PollUntilContextTimeout(context.Background(), pollInterval, timeout, true,
		func(ctx context.Context) (bool, error) {
			events, err := tc.kube.EventsV1().Events(namespace).List(ctx, metav1.ListOptions{})
			if err != nil {
				return false, err
			}
			for _, ev := range events.Items {
				if ev.Regarding.Kind == "Valkey" && ev.Regarding.Name == name && ev.Reason == reason {
					return true, nil
				}
			}
			return false, nil
		})
	require.NoError(t, err, append([]interface{}{failureMsg}, msgArgs...)...)
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
