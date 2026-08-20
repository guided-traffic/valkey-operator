//go:build e2e

package e2e

// Tests in this file cover the pod anti-affinity the operator renders into the
// data and Sentinel pod templates (scenario T5 of the admission-gap ticket).
//
// Incident this guards against (infra-d, 2026-08-19): all three data pods of one
// cluster sat on the same node, so a single drain took the whole data plane down
// at once. Anti-affinity prevents that co-location up front; the PodDisruptionBudget
// (T3) only serializes the evictions that remain.
//
// The default is off — an operator upgrade must not change the scheduling of
// existing clusters — so spreading is an explicit opt-in to soft or hard. The
// off-default and soft assertions run on any cluster shape. The hard-mode spread
// assertion needs at least three schedulable nodes (Makefile kind-create locally,
// the multi-node CI leg in .github/workflows/release.yml) and skips otherwise —
// unless E2E_REQUIRE_MULTI_NODE=true, which the multi-node leg sets so a shrunken
// cluster fails instead of skipping. The Pending negative case is node-count
// agnostic: instead of cordoning nodes — which would disturb the tests running in
// parallel — it collapses the spread domains by pointing topologyKey at a label
// every node shares.

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
)

// hostnameTopologyKey is the default spread domain: one pod per node.
const hostnameTopologyKey = "kubernetes.io/hostname"

// singleDomainTopologyKey is a label every node carries, so all nodes collapse into
// one spread domain. With hard anti-affinity this leaves every pod but the first
// Pending — regardless of how many nodes the cluster has.
const singleDomainTopologyKey = "kubernetes.io/os"

// pendingObservationTimeout is how long the surplus pods of the hard-mode negative
// case are given to reach (and stay in) Pending.
const pendingObservationTimeout = 90 * time.Second

// multiNodeRequiredEnv turns the "not enough nodes" skip below into a failure. CI
// sets it on the multi-node leg (.github/workflows/release.yml): a cluster that
// came up smaller than requested would otherwise skip the one assertion that leg
// exists for, and a skip reads as green.
const multiNodeRequiredEnv = "E2E_REQUIRE_MULTI_NODE"

// TestE2E_AntiAffinity_OffByDefault guards the default half of T5: a CR that says
// nothing about anti-affinity gets no term on either StatefulSet, so upgrading the
// operator changes nothing about how existing clusters are scheduled.
func TestE2E_AntiAffinity_OffByDefault(t *testing.T) {
	t.Parallel()
	tc := newTestClients(t)

	ns := "e2e-antiaffinity-off"
	cleanup := tc.createNamespace(t, ns)
	defer cleanup()

	name := "aa-off"
	t.Log("Creating an HA Valkey CR without an antiAffinity block")
	tc.createValkey(t, ns, buildValkeyObject(name, ns, map[string]interface{}{
		"replicas": int64(3),
		"image":    "valkey/valkey:8.0",
		"sentinel": map[string]interface{}{
			"enabled":  true,
			"replicas": int64(3),
		},
	}))
	defer tc.deleteValkey(t, ns, name)

	tc.waitForStatefulSetReady(t, ns, name, 3)
	tc.waitForStatefulSetReady(t, ns, name+"-sentinel", 3)

	for _, sts := range []string{name, name + "-sentinel"} {
		assert.Nil(t, tc.getStatefulSet(t, ns, sts).Spec.Template.Spec.Affinity,
			"StatefulSet %s must carry no affinity without an explicit opt-in", sts)
	}
}

// TestE2E_AntiAffinity_SoftWhenRequested is the soft half of T5: with mode: soft
// both StatefulSets get a preferred term on the hostname, and — because a
// preference never blocks scheduling — the cluster becomes ready even when the
// nodes cannot satisfy the spread.
func TestE2E_AntiAffinity_SoftWhenRequested(t *testing.T) {
	t.Parallel()
	tc := newTestClients(t)

	ns := "e2e-antiaffinity-soft"
	cleanup := tc.createNamespace(t, ns)
	defer cleanup()

	name := "aa-soft"
	t.Log("Creating an HA Valkey CR with antiAffinity.mode: soft")
	tc.createValkey(t, ns, buildValkeyObject(name, ns, map[string]interface{}{
		"replicas": int64(3),
		"image":    "valkey/valkey:8.0",
		"sentinel": map[string]interface{}{
			"enabled":  true,
			"replicas": int64(3),
		},
		"antiAffinity": map[string]interface{}{
			"mode": "soft",
		},
	}))
	defer tc.deleteValkey(t, ns, name)

	tc.waitForStatefulSetReady(t, ns, name, 3)
	tc.waitForStatefulSetReady(t, ns, name+"-sentinel", 3)

	t.Run("data pods prefer distinct nodes", func(t *testing.T) {
		term := requirePreferredAntiAffinity(t, tc.getStatefulSet(t, ns, name))
		assert.Equal(t, int32(100), term.Weight, "the spread must be the strongest preference")
		assert.Equal(t, hostnameTopologyKey, term.PodAffinityTerm.TopologyKey)
		assertComponentSelector(t, term.PodAffinityTerm, name, "valkey")
	})

	t.Run("sentinel pods prefer distinct nodes and repel only sentinels", func(t *testing.T) {
		term := requirePreferredAntiAffinity(t, tc.getStatefulSet(t, ns, name+"-sentinel"))
		assert.Equal(t, hostnameTopologyKey, term.PodAffinityTerm.TopologyKey)
		assertComponentSelector(t, term.PodAffinityTerm, name, "sentinel")
	})
}

// TestE2E_AntiAffinity_HardSpreadsAcrossNodes is the hard half of T5: with
// mode: hard the three data pods and the three Sentinel pods each land on three
// distinct nodes. Needs a cluster with at least three schedulable nodes.
func TestE2E_AntiAffinity_HardSpreadsAcrossNodes(t *testing.T) {
	t.Parallel()
	tc := newTestClients(t)

	tc.requireThreeSchedulableNodes(t)

	ns := "e2e-antiaffinity-hard"
	cleanup := tc.createNamespace(t, ns)
	defer cleanup()

	name := "aa-hard"
	t.Log("Creating an HA Valkey CR with antiAffinity.mode: hard")
	tc.createValkey(t, ns, buildValkeyObject(name, ns, map[string]interface{}{
		"replicas": int64(3),
		"image":    "valkey/valkey:8.0",
		"sentinel": map[string]interface{}{
			"enabled":  true,
			"replicas": int64(3),
		},
		"antiAffinity": map[string]interface{}{
			"mode": "hard",
		},
	}))
	defer tc.deleteValkey(t, ns, name)

	tc.waitForStatefulSetReady(t, ns, name, 3)
	tc.waitForStatefulSetReady(t, ns, name+"-sentinel", 3)

	t.Run("data pods are on three distinct nodes", func(t *testing.T) {
		term := requireRequiredAntiAffinity(t, tc.getStatefulSet(t, ns, name))
		assert.Equal(t, hostnameTopologyKey, term.TopologyKey)
		assertComponentSelector(t, term, name, "valkey")
		assertDistinctNodes(t, tc.podNodeNames(t, ns, name, "valkey"))
	})

	t.Run("sentinel pods are on three distinct nodes", func(t *testing.T) {
		term := requireRequiredAntiAffinity(t, tc.getStatefulSet(t, ns, name+"-sentinel"))
		assertComponentSelector(t, term, name, "sentinel")
		assertDistinctNodes(t, tc.podNodeNames(t, ns, name, "sentinel"))
	})
}

// TestE2E_AntiAffinity_HardLeavesSurplusPending documents the tradeoff of hard
// mode: with fewer spread domains than replicas the surplus pods stay Pending
// rather than silently co-locating. topologyKey points at a label every node
// carries, collapsing the whole cluster into one domain — so the assertion holds
// on a single-node CI cluster and on the multi-node Kind cluster alike, without
// cordoning nodes out from under the tests running in parallel.
func TestE2E_AntiAffinity_HardLeavesSurplusPending(t *testing.T) {
	t.Parallel()
	tc := newTestClients(t)

	ns := "e2e-antiaffinity-pending"
	cleanup := tc.createNamespace(t, ns)
	defer cleanup()

	name := "aa-pending"
	t.Log("Creating a Valkey CR with hard anti-affinity over a single spread domain")
	tc.createValkey(t, ns, buildValkeyObject(name, ns, map[string]interface{}{
		"replicas": int64(3),
		"image":    "valkey/valkey:8.0",
		"antiAffinity": map[string]interface{}{
			"mode":        "hard",
			"topologyKey": singleDomainTopologyKey,
		},
	}))
	defer tc.deleteValkey(t, ns, name)

	pending := tc.waitForUnschedulablePods(t, ns, name, "valkey", 2)
	require.Len(t, pending, 2,
		"exactly two of three pods must stay Pending when only one spread domain exists")

	scheduled := 0
	for _, node := range tc.podNodeNames(t, ns, name, "valkey") {
		if node != "" {
			scheduled++
		}
	}
	assert.Equal(t, 1, scheduled, "exactly one pod may be scheduled in a single spread domain")
}

// --- helpers ---

// requirePreferredAntiAffinity returns the single preferred (soft) anti-affinity
// term of a StatefulSet's pod template and fails if a required one is present.
func requirePreferredAntiAffinity(t *testing.T, sts *appsv1.StatefulSet) corev1.WeightedPodAffinityTerm {
	t.Helper()

	affinity := sts.Spec.Template.Spec.Affinity
	require.NotNil(t, affinity, "StatefulSet %s has no affinity", sts.Name)
	require.NotNil(t, affinity.PodAntiAffinity, "StatefulSet %s has no pod anti-affinity", sts.Name)
	assert.Empty(t, affinity.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution,
		"soft must never block scheduling")

	preferred := affinity.PodAntiAffinity.PreferredDuringSchedulingIgnoredDuringExecution
	require.Len(t, preferred, 1, "expected exactly one preferred anti-affinity term")
	return preferred[0]
}

// requireRequiredAntiAffinity returns the single required (hard) anti-affinity term
// of a StatefulSet's pod template.
func requireRequiredAntiAffinity(t *testing.T, sts *appsv1.StatefulSet) corev1.PodAffinityTerm {
	t.Helper()

	affinity := sts.Spec.Template.Spec.Affinity
	require.NotNil(t, affinity, "StatefulSet %s has no affinity", sts.Name)
	require.NotNil(t, affinity.PodAntiAffinity, "StatefulSet %s has no pod anti-affinity", sts.Name)
	assert.Empty(t, affinity.PodAntiAffinity.PreferredDuringSchedulingIgnoredDuringExecution,
		"hard mode must not additionally emit a preference")

	required := affinity.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution
	require.Len(t, required, 1, "expected exactly one required anti-affinity term")
	return required[0]
}

// assertComponentSelector checks that a term repels only pods of the same component
// of the same Valkey CR — never the other component, never another instance.
func assertComponentSelector(t *testing.T, term corev1.PodAffinityTerm, instance, component string) {
	t.Helper()

	require.NotNil(t, term.LabelSelector, "anti-affinity term must carry a label selector")
	assert.Equal(t, map[string]string{
		"app.kubernetes.io/instance":   instance,
		"app.kubernetes.io/managed-by": "vko.gtrfc.com",
		"app.kubernetes.io/component":  component,
	}, term.LabelSelector.MatchLabels)
}

// assertDistinctNodes requires every pod to sit on its own node.
func assertDistinctNodes(t *testing.T, nodes []string) {
	t.Helper()

	require.NotEmpty(t, nodes, "no pods found")
	seen := make(map[string]bool, len(nodes))
	for _, node := range nodes {
		require.NotEmpty(t, node, "a pod is unscheduled: %v", nodes)
		require.False(t, seen[node], "two pods share node %s: %v", node, nodes)
		seen[node] = true
	}
}

// podNodeNames returns the node of every pod of one component, in list order.
// An empty entry means the pod is not scheduled.
func (tc *testClients) podNodeNames(t *testing.T, namespace, instance, component string) []string {
	t.Helper()

	pods := tc.listComponentPods(t, namespace, instance, component)
	nodes := make([]string, 0, len(pods))
	for i := range pods {
		nodes = append(nodes, pods[i].Spec.NodeName)
	}
	return nodes
}

// listComponentPods lists all pods of one component (valkey | sentinel), regardless
// of phase — the Pending pods of the hard-mode negative case matter here.
func (tc *testClients) listComponentPods(t *testing.T, namespace, instance, component string) []corev1.Pod {
	t.Helper()

	pods, err := tc.kube.CoreV1().Pods(namespace).List(context.Background(), metav1.ListOptions{
		LabelSelector: "app.kubernetes.io/instance=" + instance +
			",app.kubernetes.io/managed-by=vko.gtrfc.com" +
			",app.kubernetes.io/component=" + component,
	})
	require.NoError(t, err, "Failed to list %s pods in %s", component, namespace)
	return pods.Items
}

// waitForUnschedulablePods waits until exactly count pods of a component report
// PodScheduled=False with reason Unschedulable, and returns their names.
func (tc *testClients) waitForUnschedulablePods(t *testing.T, namespace, instance, component string,
	count int) []string {
	t.Helper()

	var names []string
	err := wait.PollUntilContextTimeout(context.Background(), pollInterval, pendingObservationTimeout, true,
		func(_ context.Context) (bool, error) {
			names = nil
			for _, pod := range tc.listComponentPods(t, namespace, instance, component) {
				if isUnschedulable(pod) {
					names = append(names, pod.Name)
				}
			}
			return len(names) == count, nil
		})
	require.NoError(t, err, "expected %d unschedulable %s pods in %s, last saw %v",
		count, component, namespace, names)
	return names
}

// isUnschedulable reports whether the scheduler has rejected this pod.
func isUnschedulable(pod corev1.Pod) bool {
	if pod.Status.Phase != corev1.PodPending {
		return false
	}
	for _, cond := range pod.Status.Conditions {
		if cond.Type == corev1.PodScheduled && cond.Status == corev1.ConditionFalse &&
			cond.Reason == corev1.PodReasonUnschedulable {
			return true
		}
	}
	return false
}

// requireThreeSchedulableNodes skips the hard-mode spread assertion on a cluster
// too small to satisfy it - unless multiNodeRequiredEnv says the cluster was built
// for exactly this test, in which case a small cluster is the defect.
func (tc *testClients) requireThreeSchedulableNodes(t *testing.T) {
	t.Helper()

	nodes := tc.schedulableNodeCount(t)
	if nodes >= 3 {
		return
	}
	if os.Getenv(multiNodeRequiredEnv) == "true" {
		t.Fatalf("%s=true but the cluster has only %d schedulable nodes: hard-mode spread needs at least 3",
			multiNodeRequiredEnv, nodes)
	}
	t.Skipf("hard-mode spread needs at least 3 schedulable nodes, cluster has %d", nodes)
}

// schedulableNodeCount counts the nodes a pod without tolerations could actually
// land on: Ready, not cordoned and not carrying a NoSchedule taint. The taint
// check matters on multi-node Kind, where the control-plane node is Ready but
// tainted and would otherwise inflate the count past the skip threshold.
func (tc *testClients) schedulableNodeCount(t *testing.T) int {
	t.Helper()

	nodes, err := tc.kube.CoreV1().Nodes().List(context.Background(), metav1.ListOptions{})
	require.NoError(t, err, "Failed to list nodes")

	count := 0
	for i := range nodes.Items {
		node := nodes.Items[i]
		if !node.Spec.Unschedulable && isNodeReady(node) && !hasNoScheduleTaint(node) {
			count++
		}
	}
	return count
}

// hasNoScheduleTaint reports whether a node repels pods that carry no toleration.
func hasNoScheduleTaint(node corev1.Node) bool {
	for _, taint := range node.Spec.Taints {
		if taint.Effect == corev1.TaintEffectNoSchedule || taint.Effect == corev1.TaintEffectNoExecute {
			return true
		}
	}
	return false
}

// isNodeReady reports whether a node carries a Ready=True condition.
func isNodeReady(node corev1.Node) bool {
	for _, cond := range node.Status.Conditions {
		if cond.Type == corev1.NodeReady {
			return cond.Status == corev1.ConditionTrue
		}
	}
	return false
}
