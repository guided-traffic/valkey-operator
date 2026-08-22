//go:build e2e && fleetupgrade

// This file carries the operator upgrade test that the rest of the e2e suite
// cannot: it installs the *previously released* operator chart, provisions a
// small fleet on it, and then performs a real `helm upgrade` to the locally
// built chart while the clusters are serving data.
//
// It is behind its own build tag on purpose. Every other e2e test runs in
// parallel against one shared operator installation; a test that reinstalls and
// upgrades that operator would pull the ground out from under all of them. So
// this one needs a dedicated cluster and runs alone:
//
//	make e2e-fleet-upgrade-local          # Kind cluster + cert-manager + both installs
//	make test-e2e-fleet-upgrade           # against a cluster prepared by the above
//
// What it proves, and why each part is here:
//
//   - The chart upgrade itself succeeds, including the `manager migrate`
//     pre-upgrade hook Job, the new CRD schema and the three ClusterRole grants
//     the new operator needs (secrets:delete, events.k8s.io, policy PDBs).
//   - Every cluster converges back to OK without a human touching it.
//   - No data is lost while the failover-aware rolling update replaces every
//     data pod, on both cluster shapes.
//   - The Sentinel StatefulSet rolls too. Its pod-spec hash changes in this
//     release because buildSentinelPodSpec now sets terminationGracePeriodSeconds
//     explicitly, and a Sentinel rollout is the half of the upgrade that the
//     ordinary rolling-update tests never exercise.
//   - The ownership guard (ADR 0020) adopts nothing and refuses nothing on
//     objects that a *previous operator release* created. That is the assertion
//     that cannot be faked by creating the objects with the current build.
package e2e

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/guided-traffic/valkey-operator/test/testimages"
)

const (
	// operatorNamespace is where the chart installs the operator. It matches the
	// namespace the Makefile and the CI workflow use.
	operatorNamespace = "valkey-operator-system"

	// operatorReleaseName is the Helm release name used by every install path in
	// this repository.
	operatorReleaseName = "valkey-operator"

	// defaultUpgradeFromVersion is the released chart version the fleet is
	// provisioned on before the upgrade. Override with E2E_UPGRADE_FROM to test a
	// different starting point — the fleet this was written for runs 1.10.48.
	defaultUpgradeFromVersion = "1.10.48"

	// defaultUpgradeFromRepo is the published chart repository.
	defaultUpgradeFromRepo = "https://guided-traffic.github.io/valkey-operator/"

	// defaultUpgradeToImage is the locally built image the upgrade moves to. It
	// matches the tag `make docker-build IMG=valkey-operator:test` produces and
	// the pullPolicy: Never in test/e2e/helm-values.yaml.
	defaultUpgradeToImage = "valkey-operator:test"

	// helmTimeout bounds a single helm install/upgrade. The upgrade waits for the
	// pre-upgrade hook Job and the new Deployment, not for any Valkey rollout.
	helmTimeout = "300s"

	// podSpecHashAnnotation is the pod-template annotation the operator uses to
	// decide whether a pod is stale. Duplicated here so the test file does not
	// import the internal builder package.
	podSpecHashAnnotation = "vko.gtrfc.com/pod-spec-hash"
)

// fleetMember describes one Valkey CR the test provisions on the old operator
// and follows through the upgrade.
type fleetMember struct {
	// name and namespace of the CR.
	name      string
	namespace string
	// spec is the CR spec handed to buildValkeyObject.
	spec map[string]interface{}
	// sentinel is true when the member runs Sentinel, which decides whether a
	// Sentinel StatefulSet must roll and which port the data checks use.
	sentinel bool
	// tls is true when the data plane speaks TLS, which decides the exec helper
	// and the port used for the data checks.
	tls bool
	// keyPrefix namespaces this member's test keys so a mixed-up connection
	// cannot make another member's data look intact.
	keyPrefix string

	// Captured before the upgrade.
	masterPod             string
	dbsizeBefore          string
	sentinelHash          string
	sentinelPodUIDs       map[string]string
	operatorVersionBefore string
}

// port returns the port the data-plane checks use for this member.
func (m *fleetMember) port() int {
	if m.tls {
		return tlsValkeyPort
	}
	return 6379
}

// exec runs a valkey-cli command against one of this member's pods, over TLS
// when the member uses it.
func (m *fleetMember) exec(t *testing.T, tc *testClients, pod string, args ...string) string {
	t.Helper()
	if m.tls {
		return tc.valkeyTLSExec(t, m.namespace, pod, m.port(), args...)
	}
	return tc.valkeyExec(t, m.namespace, pod, m.port(), args...)
}

// TestE2E_FleetUpgrade provisions a small fleet on the previous operator release
// and upgrades the operator underneath it with `helm upgrade`, the same way a
// GitOps HelmRelease does.
//
// It deliberately does not call t.Parallel(): it owns the operator installation
// for the whole run.
func TestE2E_FleetUpgrade(t *testing.T) {
	tc := newTestClients(t)

	fromVersion := envOrDefault("E2E_UPGRADE_FROM", defaultUpgradeFromVersion)
	fromRepo := envOrDefault("E2E_UPGRADE_FROM_REPO", defaultUpgradeFromRepo)
	toImage := envOrDefault("E2E_UPGRADE_TO_IMAGE", defaultUpgradeToImage)
	toRepository, toTag := splitImage(t, toImage)

	t.Logf("Upgrading operator: chart %s (%s) -> local chart with image %s",
		fromVersion, fromRepo, toImage)

	// The two shapes that behave differently across this upgrade. The HA member
	// mirrors the production instances (3 data + 3 Sentinel + TLS); the plain
	// member is the multi-replica non-Sentinel shape whose init-container script
	// changes in this release even without an image bump, and it carries the
	// observer so the newly introduced token-less observer ServiceAccount is
	// created against a namespace the old operator already owned.
	fleet := []*fleetMember{
		{
			name:      "fleet-ha",
			namespace: "e2e-fleet-ha",
			sentinel:  true,
			tls:       true,
			keyPrefix: "fleet:ha",
			spec: map[string]interface{}{
				"replicas": int64(3),
				"image":    testimages.Default(),
				"tls":      tlsSpec(),
				"sentinel": map[string]interface{}{
					"enabled":  true,
					"replicas": int64(3),
				},
			},
		},
		{
			name:      "fleet-plain",
			namespace: "e2e-fleet-plain",
			sentinel:  false,
			tls:       false,
			keyPrefix: "fleet:plain",
			spec: map[string]interface{}{
				"replicas": int64(3),
				"image":    testimages.Default(),
				"observer": map[string]interface{}{
					"enabled": true,
				},
			},
		},
	}

	t.Run("install the previous operator release", func(t *testing.T) {
		helmRepoAdd(t, operatorReleaseName, fromRepo)
		helmRun(t, "upgrade", "--install", operatorReleaseName,
			operatorReleaseName+"/"+operatorReleaseName,
			"--version", fromVersion,
			"--namespace", operatorNamespace,
			"--create-namespace",
			"--set", "leaderElection.enabled=false",
			"--wait", "--timeout", helmTimeout)

		image := tc.operatorImage(t)
		require.Contains(t, image, fromVersion,
			"the running operator should be the release under test")
		t.Logf("Operator running: %s", image)
	})

	t.Run("provision the fleet on the previous release", func(t *testing.T) {
		for _, m := range fleet {
			cleanup := tc.createNamespace(t, m.namespace)
			t.Cleanup(cleanup)

			tc.createValkey(t, m.namespace, buildValkeyObject(m.name, m.namespace, m.spec))
			t.Cleanup(func() { tc.deleteValkey(t, m.namespace, m.name) })
		}

		for _, m := range fleet {
			tc.waitForStatefulSetReady(t, m.namespace, m.name, 3)
			if m.sentinel {
				tc.waitForStatefulSetReady(t, m.namespace, m.name+"-sentinel", 3)
			}
			tc.waitForValkeyPhase(t, m.namespace, m.name, "OK")
			t.Logf("%s/%s is up on the previous release", m.namespace, m.name)
		}
	})

	t.Run("write a dataset and record the pre-upgrade state", func(t *testing.T) {
		for _, m := range fleet {
			m.masterPod = tc.findFleetMaster(t, m)
			tc.waitForFleetReplicas(t, m, 2)

			data := make(map[string]string, fleetKeyCount)
			for i := 0; i < fleetKeyCount; i++ {
				data[fmt.Sprintf("%s:key:%d", m.keyPrefix, i)] = fmt.Sprintf("value-%d", i)
			}
			tc.fleetMSET(t, m, data)
			tc.waitForFleetReplicas(t, m, 2)

			m.dbsizeBefore = m.exec(t, tc, m.masterPod, "DBSIZE")
			m.operatorVersionBefore = tc.valkeyOperatorVersion(t, m.namespace, m.name)
			if m.sentinel {
				m.sentinelHash = tc.statefulSetPodSpecHash(t, m.namespace, m.name+"-sentinel")
				m.sentinelPodUIDs = tc.podUIDs(t, m.namespace, m.name+"-sentinel", 3)
			}

			t.Logf("%s/%s master=%s dbsize=%s operatorVersion=%s sentinelHash=%s",
				m.namespace, m.name, m.masterPod, m.dbsizeBefore, m.operatorVersionBefore, m.sentinelHash)

			require.NotEmpty(t, m.operatorVersionBefore,
				"the previous release should stamp status.operatorVersion")
		}
	})

	t.Run("helm upgrade to the local chart", func(t *testing.T) {
		helmRun(t, "upgrade", operatorReleaseName, "deploy/helm/valkey-operator",
			"--namespace", operatorNamespace,
			"--values", "test/e2e/helm-values.yaml",
			"--set", "image.repository="+toRepository,
			"--set", "image.tag="+toTag,
			"--wait", "--timeout", helmTimeout)

		image := tc.operatorImage(t)
		require.Equal(t, toImage, image, "the operator should run the locally built image")

		// The pre-upgrade hook is what writes current field defaults into CRs that
		// predate them. `helm upgrade` fails when the hook Job fails, so reaching
		// this point already proves it ran — the assertion states which object
		// carried that proof, so a chart change that silently drops the hook is
		// visible here rather than three releases later.
		tc.requirePreUpgradeHookSucceeded(t)
	})

	t.Run("every cluster converges without intervention", func(t *testing.T) {
		for _, m := range fleet {
			tc.waitForAllPodsSidecarImage(t, m.namespace, m.name, 3, toImage)
			tc.waitForStatefulSetReady(t, m.namespace, m.name, 3)
			if m.sentinel {
				tc.waitForStatefulSetReady(t, m.namespace, m.name+"-sentinel", 3)
			}
			tc.waitForValkeyPhaseAfterRollingUpdate(t, m.namespace, m.name, "OK")
			t.Logf("%s/%s converged on the new operator", m.namespace, m.name)
		}
	})

	t.Run("no data was lost", func(t *testing.T) {
		for _, m := range fleet {
			master := tc.findFleetMaster(t, m)
			t.Logf("%s/%s master after upgrade: %s (was %s)", m.namespace, m.name, master, m.masterPod)

			after := m.exec(t, tc, master, "DBSIZE")
			assert.Equal(t, m.dbsizeBefore, after,
				"%s/%s lost keys across the operator upgrade", m.namespace, m.name)

			for i := 0; i < fleetKeyCount; i += 10 {
				key := fmt.Sprintf("%s:key:%d", m.keyPrefix, i)
				assert.Equal(t, "1", m.exec(t, tc, master, "EXISTS", key),
					"%s/%s should still hold %s", m.namespace, m.name, key)
			}
		}
	})

	t.Run("the Sentinel StatefulSet rolled too", func(t *testing.T) {
		for _, m := range fleet {
			if !m.sentinel {
				continue
			}
			hashAfter := tc.statefulSetPodSpecHash(t, m.namespace, m.name+"-sentinel")
			assert.NotEqual(t, m.sentinelHash, hashAfter,
				"%s/%s-sentinel pod-spec hash should change (terminationGracePeriodSeconds is now explicit)",
				m.namespace, m.name)

			uidsAfter := tc.podUIDs(t, m.namespace, m.name+"-sentinel", 3)
			for pod, before := range m.sentinelPodUIDs {
				assert.NotEqual(t, before, uidsAfter[pod],
					"Sentinel pod %s should have been replaced, not left on the old template", pod)
			}
		}
	})

	t.Run("status reports the new operator version", func(t *testing.T) {
		for _, m := range fleet {
			require.Eventually(t, func() bool {
				return tc.valkeyOperatorVersion(t, m.namespace, m.name) != m.operatorVersionBefore
			}, testTimeout, pollInterval,
				"%s/%s should report a new status.operatorVersion", m.namespace, m.name)
		}
	})

	t.Run("nothing was refused as foreign", func(t *testing.T) {
		// The ownership guard compares the controller ownerReference UID. Every
		// object here was written by the *previous* release, so this is the only
		// place in the suite where "the guard accepts what an older operator
		// created" is actually tested rather than assumed.
		for _, m := range fleet {
			tc.requireNoReconcileBlocked(t, m.namespace, m.name)
			tc.requireNoNotOwnedEvents(t, m.namespace, m.name)
		}
	})

	t.Run("the new observer ServiceAccount is created and owned", func(t *testing.T) {
		// Introduced by this release: the observer runs under its own token-less
		// ServiceAccount. It does not exist on the old release, so the upgrade has
		// to create it — through the same guarded path that would refuse a foreign
		// object under that name.
		m := fleet[1]
		require.False(t, m.sentinel, "the observer member is the plain one")

		saName := m.name + "-observer"
		require.Eventually(t, func() bool {
			_, err := tc.kube.CoreV1().ServiceAccounts(m.namespace).Get(
				context.Background(), saName, metav1.GetOptions{})
			return err == nil
		}, testTimeout, pollInterval, "observer ServiceAccount %s should be created by the upgrade", saName)

		sa, err := tc.kube.CoreV1().ServiceAccounts(m.namespace).Get(
			context.Background(), saName, metav1.GetOptions{})
		require.NoError(t, err)
		assert.True(t, hasControllerOwner(sa.OwnerReferences, "Valkey", m.name),
			"observer ServiceAccount should be controller-owned by the Valkey CR")
	})
}

// fleetKeyCount is the size of the dataset written to every fleet member before
// the upgrade. One MSET round-trip, enough keys that a partial resync would show.
const fleetKeyCount = 100

// --- fleet helpers -------------------------------------------------------

// findFleetMaster returns the current master pod of a fleet member, using the
// TLS or plaintext path as the member requires.
func (tc *testClients) findFleetMaster(t *testing.T, m *fleetMember) string {
	t.Helper()
	if m.tls {
		return tc.findMasterPodTLS(t, m.namespace, m.name, 3)
	}
	return tc.findMasterPod(t, m.namespace, m.name, 3)
}

// waitForFleetReplicas waits until the member's master reports the expected
// number of connected replicas.
func (tc *testClients) waitForFleetReplicas(t *testing.T, m *fleetMember, expected int) {
	t.Helper()
	require.Eventually(t, func() bool {
		info := m.exec(t, tc, m.masterPod, "INFO", "replication")
		return strings.Contains(info, fmt.Sprintf("connected_slaves:%d", expected))
	}, testTimeout, pollInterval,
		"%s/%s should have %d connected replicas", m.namespace, m.name, expected)
}

// fleetMSET writes the dataset in one round-trip, over TLS when the member uses it.
func (tc *testClients) fleetMSET(t *testing.T, m *fleetMember, data map[string]string) {
	t.Helper()
	if !m.tls {
		tc.valkeyMSET(t, m.namespace, m.masterPod, m.port(), data)
		return
	}
	args := make([]string, 0, len(data)*2+1)
	args = append(args, "MSET")
	for k, v := range data {
		args = append(args, k, v)
	}
	tc.valkeyTLSExec(t, m.namespace, m.masterPod, m.port(), args...)
}

// --- cluster inspection --------------------------------------------------

// operatorImage returns the image the operator Deployment currently runs.
func (tc *testClients) operatorImage(t *testing.T) string {
	t.Helper()
	deploy, err := tc.kube.AppsV1().Deployments(operatorNamespace).Get(
		context.Background(), operatorReleaseName, metav1.GetOptions{})
	require.NoError(t, err, "operator Deployment should exist")
	require.NotEmpty(t, deploy.Spec.Template.Spec.Containers)
	return deploy.Spec.Template.Spec.Containers[0].Image
}

// statefulSetPodSpecHash returns the pod-spec hash annotation the operator wrote
// onto a StatefulSet's pod template.
func (tc *testClients) statefulSetPodSpecHash(t *testing.T, namespace, name string) string {
	t.Helper()
	sts := tc.getStatefulSet(t, namespace, name)
	return sts.Spec.Template.Annotations[podSpecHashAnnotation]
}

// podUIDs maps pod name to UID for the ordinals of a StatefulSet, so a later
// comparison can tell a replaced pod from a surviving one.
func (tc *testClients) podUIDs(t *testing.T, namespace, stsName string, replicas int) map[string]string {
	t.Helper()
	uids := make(map[string]string, replicas)
	for i := 0; i < replicas; i++ {
		podName := fmt.Sprintf("%s-%d", stsName, i)
		pod, err := tc.kube.CoreV1().Pods(namespace).Get(
			context.Background(), podName, metav1.GetOptions{})
		require.NoError(t, err, "pod %s should exist", podName)
		uids[podName] = string(pod.UID)
	}
	return uids
}

// waitForAllPodsSidecarImage waits until every data pod runs the expected
// sidecar image and is Ready.
//
// It looks at the container named "sidecar" rather than at index 0, because the
// sidecar is what carries the operator image and therefore what the upgrade
// changes — the Valkey container keeps spec.image across an operator upgrade.
func (tc *testClients) waitForAllPodsSidecarImage(t *testing.T, namespace, stsName string,
	replicas int, expectedImage string) {
	t.Helper()

	err := wait.PollUntilContextTimeout(context.Background(), rollingUpdatePollInterval,
		rollingUpdateTimeout, true, func(ctx context.Context) (bool, error) {
			for i := 0; i < replicas; i++ {
				podName := fmt.Sprintf("%s-%d", stsName, i)
				pod, err := tc.kube.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
				if err != nil {
					return false, nil
				}
				sidecar := containerImage(pod, "sidecar")
				if sidecar != expectedImage {
					t.Logf("Pod %s sidecar image: %s (want %s)", podName, sidecar, expectedImage)
					return false, nil
				}
				if !podReady(pod) {
					t.Logf("Pod %s carries the new sidecar but is not Ready yet", podName)
					return false, nil
				}
			}
			return true, nil
		})
	require.NoError(t, err, "not all pods of %s/%s reached sidecar image %s",
		namespace, stsName, expectedImage)
}

// valkeyOperatorVersion returns status.operatorVersion of a CR, or "" when unset.
func (tc *testClients) valkeyOperatorVersion(t *testing.T, namespace, name string) string {
	t.Helper()
	status := tc.getValkeyStatus(t, namespace, name)
	version, _, _ := unstructured.NestedString(status, "operatorVersion")
	return version
}

// requirePreUpgradeHookSucceeded asserts the chart's `manager migrate` hook Job
// ran and completed.
func (tc *testClients) requirePreUpgradeHookSucceeded(t *testing.T) {
	t.Helper()

	jobs, err := tc.kube.BatchV1().Jobs(operatorNamespace).List(
		context.Background(), metav1.ListOptions{})
	require.NoError(t, err, "listing Jobs in %s", operatorNamespace)

	for i := range jobs.Items {
		job := &jobs.Items[i]
		if !strings.Contains(job.Name, "pre-upgrade") {
			continue
		}
		assert.Positive(t, job.Status.Succeeded,
			"pre-upgrade hook Job %s should have completed successfully", job.Name)
		return
	}
	t.Fatalf("no pre-upgrade hook Job found in %s; the chart is expected to run `manager migrate`",
		operatorNamespace)
}

// requireNoReconcileBlocked asserts the CR carries no ReconcileBlocked condition
// in status True.
func (tc *testClients) requireNoReconcileBlocked(t *testing.T, namespace, name string) {
	t.Helper()

	status := tc.getValkeyStatus(t, namespace, name)
	conditions, _, _ := unstructured.NestedSlice(status, "conditions")
	for _, raw := range conditions {
		cond, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		condType, _, _ := unstructured.NestedString(cond, "type")
		condStatus, _, _ := unstructured.NestedString(cond, "status")
		if condType == "ReconcileBlocked" && condStatus == "True" {
			reason, _, _ := unstructured.NestedString(cond, "reason")
			message, _, _ := unstructured.NestedString(cond, "message")
			t.Errorf("%s/%s is blocked after the upgrade: reason=%s message=%s",
				namespace, name, reason, message)
		}
	}
}

// requireNoNotOwnedEvents asserts no ownership refusal was recorded against the CR.
//
// The reasons are matched by suffix rather than enumerated, so a refusal reason
// added by a later change (a guard on another managed kind) is caught by this
// test without editing it.
func (tc *testClients) requireNoNotOwnedEvents(t *testing.T, namespace, name string) {
	t.Helper()

	events, err := tc.kube.CoreV1().Events(namespace).List(context.Background(),
		metav1.ListOptions{FieldSelector: "involvedObject.name=" + name})
	require.NoError(t, err, "listing Events for %s/%s", namespace, name)

	for i := range events.Items {
		event := &events.Items[i]
		if strings.HasSuffix(event.Reason, "NotOwned") {
			t.Errorf("%s/%s recorded an ownership refusal after the upgrade: %s: %s",
				namespace, name, event.Reason, event.Message)
		}
	}
}

// --- small utilities -----------------------------------------------------

// containerImage returns the image of the named container, or "" when absent.
func containerImage(pod *corev1.Pod, container string) string {
	for i := range pod.Spec.Containers {
		if pod.Spec.Containers[i].Name == container {
			return pod.Spec.Containers[i].Image
		}
	}
	return ""
}

// podReady reports whether the pod carries a Ready condition in status True.
func podReady(pod *corev1.Pod) bool {
	for _, cond := range pod.Status.Conditions {
		if cond.Type == corev1.PodReady {
			return cond.Status == corev1.ConditionTrue
		}
	}
	return false
}

// hasControllerOwner reports whether refs carry a controller reference of the
// given kind and name.
func hasControllerOwner(refs []metav1.OwnerReference, kind, name string) bool {
	for _, ref := range refs {
		if ref.Controller != nil && *ref.Controller && ref.Kind == kind && ref.Name == name {
			return true
		}
	}
	return false
}

// envOrDefault returns the environment variable or the fallback when it is empty.
func envOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

// splitImage splits repository:tag. The chart takes them as two values, so the
// test cannot pass the image as one string.
func splitImage(t *testing.T, image string) (repository, tag string) {
	t.Helper()
	idx := strings.LastIndex(image, ":")
	require.Positive(t, idx, "image %q must carry an explicit tag", image)
	return image[:idx], image[idx+1:]
}

// helmRepoAdd registers the chart repository and refreshes the index. Adding an
// already-registered repository is not an error worth failing on, so the add is
// forced and only the update has to succeed.
func helmRepoAdd(t *testing.T, name, url string) {
	t.Helper()
	helmRun(t, "repo", "add", name, url, "--force-update")
	helmRun(t, "repo", "update", name)
}

// helmRun executes helm and fails the test with its combined output on error.
func helmRun(t *testing.T, args ...string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "helm", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	t.Logf("helm %s", strings.Join(args, " "))
	if err := cmd.Run(); err != nil {
		t.Fatalf("helm %s failed: %v\nstdout:\n%s\nstderr:\n%s",
			strings.Join(args, " "), err, stdout.String(), stderr.String())
	}
}
