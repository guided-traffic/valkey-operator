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
//   - The Sentinel StatefulSet rolls too, and exactly once. Its pod-spec hash
//     changes whenever a release changes buildSentinelPodSpec -- the explicit
//     terminationGracePeriodSeconds of v1.11.0 and the rootless posture of
//     ADR 0032 both did -- and a Sentinel rollout is the half of the upgrade that
//     the ordinary rolling-update tests never exercise.
//   - The ownership guard (ADR 0020) adopts nothing and refuses nothing on
//     objects that a *previous operator release* created. That is the assertion
//     that cannot be faked by creating the objects with the current build.
//   - The rootless migration (docs/adr/0032-generated-pods-run-rootless.md) on
//     real root-written data: this is the only test that starts from pods and
//     volumes a released operator built as uid 0. Every rolled cluster ends
//     rootless with its keys on every replica; the persistent members' pods ran
//     the ownership repair on the way up, and the repair then leaves the template
//     without a second roll; the persistent single pod restarts once, the
//     non-persistent one is not restarted at all and reports the deferral.
package e2e

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	// replicas is the data pod count.
	replicas int
	// persistent is true when the member keeps its data on a PVC, which is what
	// the ownership repair exists for (ADR 0032 D2).
	persistent bool
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
	dataPodUIDs           map[string]string
	operatorVersionBefore string
}

// deferred reports whether the upgrade must leave this member's only pod alone:
// a single pod without persistence, whose restart would discard the dataset
// (ADR 0032 D3).
func (m *fleetMember) deferred() bool {
	return m.replicas == 1 && !m.persistent
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
			replicas:  3,
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
			replicas:  3,
			keyPrefix: "fleet:plain",
			spec: map[string]interface{}{
				"replicas": int64(3),
				"image":    testimages.Default(),
				"observer": map[string]interface{}{
					"enabled": true,
				},
			},
		},
		// ADR 0032: the members whose data a root process wrote to a volume that
		// survives the upgrade. AOF is the shape that fails outright without the
		// repair (root-owned 0644 files in appendonlydir), RDB the one that fails
		// silently on the first BGSAVE; the single pods are the two sides of D3.
		fleetPersistent("fleet-aof", "aof", 3),
		fleetPersistent("fleet-rdb", "rdb", 3),
		fleetPersistent("fleet-single-persistent", "aof", 1),
		{
			name:      "fleet-single-ephemeral",
			namespace: "e2e-fleet-single-ephemeral",
			replicas:  1,
			keyPrefix: "fleet:single-ephemeral",
			spec: map[string]interface{}{
				"replicas": int64(1),
				"image":    testimages.Default(),
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

	// The fleet outlives the subtest that provisions it, so its cleanups belong to the
	// parent test: registered on the subtest they ran the moment provisioning
	// finished and deleted every CR before the upgrade.
	parent := t
	t.Run("provision the fleet on the previous release", func(t *testing.T) {
		for _, m := range fleet {
			cleanup := tc.createNamespace(t, m.namespace)
			parent.Cleanup(cleanup)

			tc.createValkey(t, m.namespace, buildValkeyObject(m.name, m.namespace, m.spec))
			parent.Cleanup(func() { tc.deleteValkey(parent, m.namespace, m.name) })
		}

		for _, m := range fleet {
			tc.waitForStatefulSetReady(t, m.namespace, m.name, int32(m.replicas))
			if m.sentinel {
				tc.waitForStatefulSetReady(t, m.namespace, m.name+"-sentinel", 3)
			}
			tc.waitForValkeyPhase(t, m.namespace, m.name, "OK")
			t.Logf("%s/%s is up on the previous release", m.namespace, m.name)
		}
	})

	t.Run("the Kind volumes are hostPath, so the repair is what re-owns the data", func(t *testing.T) {
		// The premise of the migration assertions below (ADR 0017 D29: fail loudly
		// when the environment stops being the one the test was written for).
		// kubelet applies no fsGroup to a hostPath volume, so on Kind nothing but
		// the ownership repair can make root-written data writable for uid 999. On
		// a volume type with fsGroup support these assertions would pass without
		// the repair doing anything.
		for _, m := range fleet {
			if !m.persistent {
				continue
			}
			tc.requireHostPathVolume(t, m.namespace, "data-"+m.name+"-0")
		}
	})

	t.Run("write a dataset and record the pre-upgrade state", func(t *testing.T) {
		for _, m := range fleet {
			m.masterPod = tc.findFleetMaster(t, m)
			tc.waitForFleetReplicas(t, m, m.replicas-1)

			data := make(map[string]string, fleetKeyCount)
			for i := 0; i < fleetKeyCount; i++ {
				data[fmt.Sprintf("%s:key:%d", m.keyPrefix, i)] = fmt.Sprintf("value-%d", i)
			}
			tc.fleetMSET(t, m, data)
			tc.waitForFleetReplicas(t, m, m.replicas-1)
			if m.persistent {
				// Put the dataset on the volume as the old, root-running pod writes it.
				assert.Contains(t, m.exec(t, tc, m.masterPod, "BGSAVE"), "Background saving started")
				pollUntil(t, pollInterval, testTimeout, func() (bool, string) {
					info := m.exec(t, tc, m.masterPod, "INFO", "persistence")
					return strings.Contains(info, "rdb_bgsave_in_progress:0") &&
						strings.Contains(info, "rdb_last_bgsave_status:ok"), keepLinesContaining(info, "rdb_")
				}, "%s/%s: the pre-upgrade snapshot never completed", m.namespace, m.name)
				tc.shapeLegacyVolumes(t, m)
			}

			m.dbsizeBefore = m.exec(t, tc, m.masterPod, "DBSIZE")
			m.operatorVersionBefore = tc.valkeyOperatorVersion(t, m.namespace, m.name)
			if m.sentinel {
				m.sentinelHash = tc.statefulSetPodSpecHash(t, m.namespace, m.name+"-sentinel")
				m.sentinelPodUIDs = tc.podUIDs(t, m.namespace, m.name+"-sentinel", 3)
			}
			m.dataPodUIDs = tc.podUIDs(t, m.namespace, m.name, m.replicas)

			t.Logf("%s/%s master=%s dbsize=%s operatorVersion=%s sentinelHash=%s",
				m.namespace, m.name, m.masterPod, m.dbsizeBefore, m.operatorVersionBefore, m.sentinelHash)

			require.NotEmpty(t, m.operatorVersionBefore,
				"the previous release should stamp status.operatorVersion")
		}
	})

	upgradeStarted := metav1.Now()
	t.Run("helm upgrade to the local chart", func(t *testing.T) {
		// go test runs in the package directory, so repository paths are relative to it.
		helmRun(t, "upgrade", operatorReleaseName, filepath.Join("..", "..", "deploy", "helm", "valkey-operator"),
			"--namespace", operatorNamespace,
			"--values", "helm-values.yaml",
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
			if m.deferred() {
				tc.waitForValkeyPhase(t, m.namespace, m.name, "OK")
				continue
			}
			tc.waitForAllPodsSidecarImage(t, m.namespace, m.name, m.replicas, toImage)
			tc.waitForStatefulSetReady(t, m.namespace, m.name, int32(m.replicas))
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

			// On every replica, not just the master: the migration replaced every
			// data pod, and a replica that came back on an unreadable volume would
			// show here first.
			for i := 0; i < m.replicas; i++ {
				pod := fmt.Sprintf("%s-%d", m.name, i)
				pollUntil(t, pollInterval, testTimeout, func() (bool, string) {
					got := m.exec(t, tc, pod, "DBSIZE")
					return got == m.dbsizeBefore, "DBSIZE " + got
				}, "%s/%s must hold %s keys", m.namespace, pod, m.dbsizeBefore)
			}

			for i := 0; i < fleetKeyCount; i += 10 {
				key := fmt.Sprintf("%s:key:%d", m.keyPrefix, i)
				assert.Equal(t, "1", m.exec(t, tc, master, "EXISTS", key),
					"%s/%s should still hold %s", m.namespace, m.name, key)
			}
		}
	})

	t.Run("every rolled pod runs rootless", func(t *testing.T) {
		for _, m := range fleet {
			if m.deferred() {
				continue
			}
			tc.requireRootlessPods(t, m.namespace, m.name, m.replicas)
			if m.sentinel {
				tc.requireRootlessPods(t, m.namespace, m.name+"-sentinel", 3)
			}
		}
	})

	t.Run("the persistent pods ran the ownership repair, and it left the template", func(t *testing.T) {
		for _, m := range fleet {
			if !m.persistent {
				continue
			}
			for i := 0; i < m.replicas; i++ {
				tc.requireRepairRan(t, m.namespace, fmt.Sprintf("%s-%d", m.name, i))
			}
			pollUntil(t, pollInterval, testTimeout, func() (bool, string) {
				sts := tc.getStatefulSet(t, m.namespace, m.name)
				for _, c := range sts.Spec.Template.Spec.InitContainers {
					if c.Name == dataOwnershipRepairContainer {
						return false, "the template still carries " + dataOwnershipRepairContainer
					}
				}
				return true, ""
			},
				"%s/%s: with every pod rootless the repair must leave the template", m.namespace, m.name)
		}
	})

	t.Run("a migrated persistent master writes: no silent MISCONF", func(t *testing.T) {
		// The RDB member's volume root was set to 0755 root before the upgrade, the
		// shape of a fresh ext4/xfs root, where uid 999 can read everything and --
		// without the repair -- could not write the snapshot: every write would then
		// answer MISCONF while the pod stays Ready (ADR 0032 Context).
		for _, m := range fleet {
			if !m.persistent {
				continue
			}
			master := tc.findFleetMaster(t, m)
			assert.Equal(t, "OK", m.exec(t, tc, master, "SET", m.keyPrefix+":after-migration", "1"))
			assert.Contains(t, m.exec(t, tc, master, "BGSAVE"), "Background saving started")
			pollUntil(t, pollInterval, testTimeout, func() (bool, string) {
				info := m.exec(t, tc, master, "INFO", "persistence")
				return strings.Contains(info, "rdb_bgsave_in_progress:0"), keepLinesContaining(info, "rdb_")
			}, "%s/%s: the snapshot never completed", m.namespace, master)
			info := m.exec(t, tc, master, "INFO", "persistence")
			assert.Contains(t, info, "rdb_last_bgsave_status:ok", "%s/%s", m.namespace, master)
			assert.Contains(t, info, "aof_last_write_status:ok", "%s/%s", m.namespace, master)
		}
	})

	t.Run("the observer received the posture", func(t *testing.T) {
		// ADR 0032 D5: the observer carries no pod-spec hash, so only the new
		// comparison lines move an observer an earlier release created.
		m := fleet[1]
		pollUntil(t, pollInterval, testTimeout, func() (bool, string) {
			d, err := tc.kube.AppsV1().Deployments(m.namespace).Get(context.Background(), m.name+"-observer", metav1.GetOptions{})
			if err != nil || len(d.Spec.Template.Spec.Containers) == 0 {
				return false, fmt.Sprintf("observer Deployment unreadable: %v", err)
			}
			pod := d.Spec.Template.Spec.SecurityContext
			c := d.Spec.Template.Spec.Containers[0].SecurityContext
			return pod != nil && pod.RunAsNonRoot != nil && *pod.RunAsNonRoot &&
					pod.SeccompProfile != nil && pod.SeccompProfile.Type == corev1.SeccompProfileTypeRuntimeDefault &&
					c != nil && c.ReadOnlyRootFilesystem != nil && *c.ReadOnlyRootFilesystem,
				fmt.Sprintf("pod securityContext %+v, container securityContext %+v", pod, c)
		}, "%s/%s-observer never received the rootless posture", m.namespace, m.name)
	})

	t.Run("each Sentinel tier completed exactly one roll", func(t *testing.T) {
		for _, m := range fleet {
			if !m.sentinel {
				continue
			}
			assert.Equal(t, 1, tc.countValkeyEventsSince(t, m.namespace, m.name, "SentinelUpdateComplete", upgradeStarted),
				"%s/%s: the Sentinel tier must roll exactly once for the upgrade", m.namespace, m.name)
		}
	})

	t.Run("the repair leaving the template rolls nothing", func(t *testing.T) {
		settled := map[string]map[string]string{}
		for _, m := range fleet {
			settled[m.name] = tc.podUIDs(t, m.namespace, m.name, m.replicas)
			if m.sentinel {
				settled[m.name+"-sentinel"] = tc.podUIDs(t, m.namespace, m.name+"-sentinel", 3)
			}
		}
		// Several reconcile passes: the template write that removes the repair has
		// long happened, and a second roll would have deleted a pod by now.
		time.Sleep(90 * time.Second)
		for _, m := range fleet {
			assert.Equal(t, settled[m.name], tc.podUIDs(t, m.namespace, m.name, m.replicas),
				"%s/%s: no second roll", m.namespace, m.name)
			if m.sentinel {
				assert.Equal(t, settled[m.name+"-sentinel"], tc.podUIDs(t, m.namespace, m.name+"-sentinel", 3),
					"%s/%s-sentinel rolled exactly once", m.namespace, m.name)
			}
		}
	})

	t.Run("single pods: the persistent one restarted once, the ephemeral one not at all", func(t *testing.T) {
		for _, m := range fleet {
			if m.replicas != 1 {
				continue
			}
			uids := tc.podUIDs(t, m.namespace, m.name, 1)
			pod := m.name + "-0"
			if m.persistent {
				assert.NotEqual(t, m.dataPodUIDs[pod], uids[pod],
					"%s: a persistent single pod is replaced at the upgrade", pod)
				continue
			}
			assert.Equal(t, m.dataPodUIDs[pod], uids[pod],
				"%s: replacing a single pod without persistence would discard its dataset", pod)
			cond := tc.valkeyStatusCondition(t, m.namespace, m.name, "PodSecurityUpdatePending")
			require.NotNil(t, cond, "%s/%s must report the deferral", m.namespace, m.name)
			assert.Equal(t, "True", cond["status"])
			assert.Equal(t, "PodRunsAsRoot", cond["reason"])
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

// dataOwnershipRepairContainer is the migration-only init container of ADR 0032
// D2. Duplicated for the same reason as podSpecHashAnnotation.
const dataOwnershipRepairContainer = "fix-data-ownership"

// fleetPersistent is a persistent non-Sentinel member.
func fleetPersistent(name, mode string, replicas int) *fleetMember {
	return &fleetMember{
		name:       name,
		namespace:  "e2e-" + name,
		replicas:   replicas,
		persistent: true,
		keyPrefix:  "fleet:" + name,
		spec: map[string]interface{}{
			"replicas": int64(replicas),
			"image":    testimages.Default(),
			"persistence": map[string]interface{}{
				"enabled": true, "mode": mode, "size": "256Mi",
			},
		},
	}
}

// shapeLegacyVolumes makes the volumes of a member look the way an earlier operator
// left them on storage with a root-owned 0755 root (a fresh ext4/xfs volume root):
// Kind's local-path creates 0777 directories, which hides the RDB failure mode. It
// runs in the old, root pods, and asserts the files are uid 0's.
func (tc *testClients) shapeLegacyVolumes(t *testing.T, m *fleetMember) {
	t.Helper()
	for i := 0; i < m.replicas; i++ {
		pod := fmt.Sprintf("%s-%d", m.name, i)
		out := kubectlExec(t, m.namespace, pod, "valkey", "sh", "-c",
			"chmod 0755 /data && stat -c '%u %a' /data && ls -ln /data | awk 'NR>1 {print $3}' | sort -u")
		lines := strings.Fields(out)
		require.GreaterOrEqual(t, len(lines), 2, "%s: unexpected output %q", pod, out)
		assert.Equal(t, "0", lines[0], "%s: /data must be root-owned before the upgrade", pod)
		assert.Equal(t, "755", lines[1], "%s", pod)
		assert.Contains(t, lines[2:], "0", "%s: the old pod must have written files as uid 0", pod)
	}
}

// kubectlExec runs a command in a container and returns its stdout.
func kubectlExec(t *testing.T, namespace, pod, container string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "kubectl", append([]string{"exec", pod, "-n", namespace, "-c", container, "--"}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	require.NoError(t, cmd.Run(), "kubectl exec %s/%s: %s", namespace, pod, stderr.String())
	return stdout.String()
}

// countValkeyEventsSince counts the Events with the given reason about a Valkey CR
// since a point in time, summing an aggregated series.
func (tc *testClients) countValkeyEventsSince(t *testing.T, namespace, name, reason string, since metav1.Time) int {
	t.Helper()
	events, err := tc.kube.EventsV1().Events(namespace).List(context.Background(), metav1.ListOptions{})
	require.NoError(t, err)
	count := 0
	for i := range events.Items {
		ev := &events.Items[i]
		if ev.Regarding.Kind != "Valkey" || ev.Regarding.Name != name || ev.Reason != reason {
			continue
		}
		if ev.EventTime.Time.Before(since.Time) && ev.DeprecatedLastTimestamp.Time.Before(since.Time) {
			continue
		}
		if ev.Series != nil && ev.Series.Count > 0 {
			count += int(ev.Series.Count)
			continue
		}
		count++
	}
	return count
}

// requireHostPathVolume asserts that the PV bound to the claim is a hostPath one.
func (tc *testClients) requireHostPathVolume(t *testing.T, namespace, claim string) {
	t.Helper()
	ctx := context.Background()
	pvc, err := tc.kube.CoreV1().PersistentVolumeClaims(namespace).Get(ctx, claim, metav1.GetOptions{})
	require.NoError(t, err, "claim %s/%s", namespace, claim)
	require.NotEmpty(t, pvc.Spec.VolumeName, "claim %s/%s is not bound", namespace, claim)
	pv, err := tc.kube.CoreV1().PersistentVolumes().Get(ctx, pvc.Spec.VolumeName, metav1.GetOptions{})
	require.NoError(t, err)
	require.NotNil(t, pv.Spec.HostPath,
		"PV %s is not hostPath (%+v): kubelet may apply fsGroup here, so the repair assertions would "+
			"pass without the repair doing anything -- revisit this test before trusting it",
		pv.Name, pv.Spec.PersistentVolumeSource)
}

// requireRootlessPods asserts every ordinal of a StatefulSet was built with the
// ADR 0032 posture.
func (tc *testClients) requireRootlessPods(t *testing.T, namespace, stsName string, replicas int) {
	t.Helper()
	for i := 0; i < replicas; i++ {
		pod := tc.getPod(t, namespace, fmt.Sprintf("%s-%d", stsName, i))
		sc := pod.Spec.SecurityContext
		require.NotNil(t, sc, "%s/%s has no pod securityContext", namespace, pod.Name)
		require.NotNil(t, sc.RunAsNonRoot, "%s/%s", namespace, pod.Name)
		assert.True(t, *sc.RunAsNonRoot, "%s/%s", namespace, pod.Name)
		require.NotNil(t, sc.RunAsUser, "%s/%s", namespace, pod.Name)
		assert.Equal(t, int64(999), *sc.RunAsUser, "%s/%s", namespace, pod.Name)
	}
}

// requireRepairRan asserts the pod carries the ownership repair and that it
// exited 0. Pod specs are immutable, so the init container on the pod is the
// durable proof the template carried it when the pod was created.
func (tc *testClients) requireRepairRan(t *testing.T, namespace, podName string) {
	t.Helper()
	pod := tc.getPod(t, namespace, podName)
	found := false
	for _, c := range pod.Spec.InitContainers {
		found = found || c.Name == dataOwnershipRepairContainer
	}
	require.True(t, found, "%s/%s was created without the ownership repair", namespace, podName)
	for _, st := range pod.Status.InitContainerStatuses {
		if st.Name != dataOwnershipRepairContainer {
			continue
		}
		require.NotNil(t, st.State.Terminated, "%s/%s: the repair has not finished", namespace, podName)
		assert.Equal(t, int32(0), st.State.Terminated.ExitCode, "%s/%s: the repair failed", namespace, podName)
		return
	}
	t.Errorf("%s/%s reports no status for the repair init container", namespace, podName)
}

// findFleetMaster returns the current master pod of a fleet member, using the
// TLS or plaintext path as the member requires.
func (tc *testClients) findFleetMaster(t *testing.T, m *fleetMember) string {
	t.Helper()
	if m.tls {
		return tc.findMasterPodTLS(t, m.namespace, m.name, m.replicas)
	}
	return tc.findMasterPod(t, m.namespace, m.name, m.replicas)
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
//
// The chart annotates the Job `helm.sh/hook-delete-policy: hook-succeeded`, so a
// successful hook is gone by the time `helm upgrade` returns -- the assertion used to
// demand the Job itself and could therefore never pass. The Job's own `Completed`
// Event outlives it and is the proof; a Job that is still there is checked directly.
func (tc *testClients) requirePreUpgradeHookSucceeded(t *testing.T) {
	t.Helper()
	ctx := context.Background()

	jobs, err := tc.kube.BatchV1().Jobs(operatorNamespace).List(ctx, metav1.ListOptions{})
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

	events, err := tc.kube.CoreV1().Events(operatorNamespace).List(ctx,
		metav1.ListOptions{FieldSelector: "involvedObject.kind=Job"})
	require.NoError(t, err, "listing Job Events in %s", operatorNamespace)
	for i := range events.Items {
		ev := &events.Items[i]
		if strings.Contains(ev.InvolvedObject.Name, "pre-upgrade") && ev.Reason == "Completed" {
			t.Logf("pre-upgrade hook Job %s completed and was deleted by its hook-succeeded policy",
				ev.InvolvedObject.Name)
			return
		}
	}
	t.Fatalf("no pre-upgrade hook Job and no Completed Event of one in %s; the chart is expected to "+
		"run `manager migrate`", operatorNamespace)
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
