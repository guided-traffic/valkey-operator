//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/guided-traffic/valkey-operator/test/testimages"
)

// This file covers docs/adr/0032-generated-pods-run-rootless.md on a running
// cluster. The namespace enforces Pod Security "restricted", so the API server is
// the oracle: a generated pod that violates the profile is refused at creation and
// its StatefulSet never becomes ready. On top of admission the test reads the
// identity the Valkey processes actually run under, writes and replicates data,
// and drives the two operations whose sidecar half depends on the token and the
// drain marker surviving the new uid and fsGroup: a Sentinel image roll and a
// drain failover -- both with zero Warning Events.

const restrictedAuthPassword = "vko-e2e-restricted"

// createRestrictedNamespace is createNamespace with Pod Security enforcement.
func (tc *testClients) createRestrictedNamespace(t *testing.T, name string) func() {
	t.Helper()
	cleanup := tc.createNamespace(t, name)
	ctx := context.Background()
	ns, err := tc.kube.CoreV1().Namespaces().Get(ctx, name, metav1.GetOptions{})
	require.NoError(t, err)
	if ns.Labels == nil {
		ns.Labels = map[string]string{}
	}
	ns.Labels["pod-security.kubernetes.io/enforce"] = "restricted"
	ns.Labels["pod-security.kubernetes.io/enforce-version"] = "latest"
	_, err = tc.kube.CoreV1().Namespaces().Update(ctx, ns, metav1.UpdateOptions{})
	require.NoError(t, err, "labelling namespace %s restricted", name)
	return cleanup
}

// procStatus returns the Uid, CapEff and NoNewPrivs lines of PID 1 in a container.
func procStatus(t *testing.T, namespace, pod, container string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "kubectl", "exec", pod, "-n", namespace, "-c", container,
		"--", "cat", "/proc/1/status")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	require.NoError(t, cmd.Run(), "reading /proc/1/status of %s/%s: %s", pod, container, stderr.String())
	var lines []string
	for _, line := range strings.Split(stdout.String(), "\n") {
		if strings.HasPrefix(line, "Uid:") || strings.HasPrefix(line, "CapEff:") ||
			strings.HasPrefix(line, "CapBnd:") || strings.HasPrefix(line, "NoNewPrivs:") {
			lines = append(lines, strings.Join(strings.Fields(line), " "))
		}
	}
	return strings.Join(lines, "\n")
}

// requireRootless asserts the identity the ADR 0032 posture promises.
func requireRootless(t *testing.T, namespace, pod, container string) {
	t.Helper()
	status := procStatus(t, namespace, pod, container)
	assert.Contains(t, status, "Uid: 999 999 999 999", "%s/%s must run as the valkey user", pod, container)
	assert.Contains(t, status, "CapEff: 0000000000000000", "%s/%s must hold no capability", pod, container)
	// CapEff is empty for any non-root uid; the bounding set is what shows every
	// capability was dropped.
	assert.Contains(t, status, "CapBnd: 0000000000000000", "%s/%s must have dropped ALL", pod, container)
	assert.Contains(t, status, "NoNewPrivs: 1", "%s/%s must run with no_new_privs", pod, container)
}

func (tc *testClients) authTLSExec(t *testing.T, namespace, pod string, args ...string) string {
	t.Helper()
	return tc.valkeyTLSExec(t, namespace, pod, 16379,
		append([]string{"-a", restrictedAuthPassword, "--no-auth-warning"}, args...)...)
}

func TestE2E_PodSecurity_RestrictedNamespace(t *testing.T) {
	t.Parallel()
	tc := newTestClients(t)

	ns := "e2e-restricted"
	cleanup := tc.createRestrictedNamespace(t, ns)
	defer cleanup()

	t.Run("the namespace really enforces restricted", func(t *testing.T) {
		// The positive control: a pod the posture exists for -- no securityContext at
		// all -- must be refused, or the admissions below prove nothing.
		_, err := tc.kube.CoreV1().Pods(ns).Create(context.Background(), &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "restricted-control", Namespace: ns},
			Spec: corev1.PodSpec{Containers: []corev1.Container{{
				Name: "c", Image: testimages.Default(), Command: []string{"sleep", "1"},
			}}},
		}, metav1.CreateOptions{DryRun: []string{metav1.DryRunAll}})
		require.Error(t, err, "an unrestricted pod must be refused in %s", ns)
		assert.True(t, apierrors.IsForbidden(err), "refused by admission: %v", err)
		assert.Contains(t, err.Error(), `violates PodSecurity "restricted`)
	})

	_, err := tc.kube.CoreV1().Secrets(ns).Create(context.Background(), &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "restricted-auth", Namespace: ns},
		StringData: map[string]string{"password": restrictedAuthPassword},
	}, metav1.CreateOptions{})
	if !apierrors.IsAlreadyExists(err) {
		require.NoError(t, err)
	}

	standalone, multi, ha, drain := "rl-sa", "rl-mr", "rl-ha", "rl-drain"
	tc.createValkey(t, ns, buildValkeyObject(standalone, ns, map[string]interface{}{
		"replicas": int64(1),
		"image":    testimages.Default(),
		"persistence": map[string]interface{}{
			"enabled": true, "mode": "aof", "size": "256Mi",
		},
	}))
	defer tc.deleteValkey(t, ns, standalone)
	tc.createValkey(t, ns, buildValkeyObject(multi, ns, map[string]interface{}{
		"replicas": int64(3),
		"image":    testimages.Default(),
		"tls":      tlsSpec(),
		"auth":     map[string]interface{}{"secretName": "restricted-auth", "secretPasswordKey": "password"},
		"metrics":  map[string]interface{}{"enabled": true},
	}))
	defer tc.deleteValkey(t, ns, multi)
	tc.createValkey(t, ns, buildValkeyObject(ha, ns, map[string]interface{}{
		"replicas": int64(3),
		"image":    testimages.UpgradeFrom,
		"tls":      tlsSpec(),
		"sentinel": map[string]interface{}{"enabled": true, "replicas": int64(3)},
		"observer": map[string]interface{}{"enabled": true},
	}))
	defer tc.deleteValkey(t, ns, ha)
	tc.createValkey(t, ns, buildValkeyObject(drain, ns, map[string]interface{}{
		"replicas": int64(3),
		"image":    testimages.Default(),
	}))
	defer tc.deleteValkey(t, ns, drain)

	t.Run("every generated pod is admitted and becomes Ready", func(t *testing.T) {
		tc.waitForStatefulSetReady(t, ns, standalone, 1)
		tc.waitForStatefulSetReady(t, ns, multi, 3)
		tc.waitForStatefulSetReady(t, ns, ha, 3)
		tc.waitForStatefulSetReady(t, ns, ha+"-sentinel", 3)
		tc.waitForStatefulSetReady(t, ns, drain, 3)
		tc.waitForDeploymentReady(t, ns, ha+"-observer")
		for _, name := range []string{standalone, multi, ha, drain} {
			tc.waitForValkeyPhase(t, ns, name, "OK")
		}
	})

	t.Run("the Valkey-image processes run as uid 999 with no capability", func(t *testing.T) {
		requireRootless(t, ns, standalone+"-0", "valkey")
		for i := 0; i < 3; i++ {
			requireRootless(t, ns, fmt.Sprintf("%s-%d", multi, i), "valkey")
			requireRootless(t, ns, fmt.Sprintf("%s-sentinel-%d", ha, i), "sentinel")
		}
	})

	t.Run("a standalone AOF dataset is written, rewritten and snapshotted under the posture", func(t *testing.T) {
		pod := standalone + "-0"
		assert.Equal(t, "OK", tc.valkeyExec(t, ns, pod, 6379, "SET", "restricted:aof", "1"))
		// The status fields start at "ok"; the counters prove the forked children
		// actually ran and wrote under uid 999, the read-only root and RuntimeDefault.
		// One after the other: Valkey refuses a BGSAVE while a rewrite runs.
		assert.Contains(t, tc.valkeyExec(t, ns, pod, 6379, "BGREWRITEAOF"), "rewriting")
		pollUntil(t, 2*time.Second, time.Minute, func() (bool, string) {
			info := tc.valkeyExec(t, ns, pod, 6379, "INFO", "persistence")
			return strings.Contains(info, "aof_rewrite_in_progress:0") &&
					strings.Contains(info, "aof_rewrite_scheduled:0") && !strings.Contains(info, "aof_rewrites:0"),
				keepLinesContaining(info, "aof_rewrite")
		}, "the AOF rewrite never completed")
		assert.Contains(t, tc.valkeyExec(t, ns, pod, 6379, "BGSAVE"), "Background saving started")
		pollUntil(t, 2*time.Second, time.Minute, func() (bool, string) {
			info := tc.valkeyExec(t, ns, pod, 6379, "INFO", "persistence")
			return strings.Contains(info, "rdb_bgsave_in_progress:0") && !strings.Contains(info, "rdb_saves:0"),
				keepLinesContaining(info, "rdb_")
		}, "the snapshot never completed")
		info := tc.valkeyExec(t, ns, pod, 6379, "INFO", "persistence")
		assert.Contains(t, info, "aof_enabled:1")
		assert.Contains(t, info, "aof_last_bgrewrite_status:ok")
		assert.Contains(t, info, "rdb_last_bgsave_status:ok")
		assert.Contains(t, info, "aof_last_write_status:ok")
		assert.Equal(t, "OK", tc.valkeyExec(t, ns, pod, 6379, "SET", "restricted:after-rewrite", "1"))
	})

	t.Run("TLS and auth data is written and replicated", func(t *testing.T) {
		master := ""
		pollUntil(t, 2*time.Second, 2*time.Minute, func() (bool, string) {
			for i := 0; i < 3; i++ {
				pod := fmt.Sprintf("%s-%d", multi, i)
				if strings.Contains(tc.authTLSExec(t, ns, pod, "INFO", "replication"), "role:master") {
					master = pod
					return true, pod
				}
			}
			return false, "no pod answers role:master"
		}, "no master on %s", multi)
		assert.Equal(t, "OK", tc.authTLSExec(t, ns, master, "SET", "restricted:tls", "replicated"))
		for i := 0; i < 3; i++ {
			pod := fmt.Sprintf("%s-%d", multi, i)
			pollUntil(t, 2*time.Second, time.Minute, func() (bool, string) {
				got := tc.authTLSExec(t, ns, pod, "GET", "restricted:tls")
				return got == "replicated", got
			}, "the key never reached %s", pod)
		}
		// The sidecar reaches the API under the new uid and fsGroup: it labelled the
		// master, which is what the -rw Service selects on.
		tc.waitForPodLabel(t, ns, master, "vko.gtrfc.com/instanceRole", "master")
	})

	t.Run("a Sentinel image roll completes with zero Warning Events", func(t *testing.T) {
		tc.updateValkeyImage(t, ns, ha, testimages.UpgradeTo)
		tc.waitForAllPodsImage(t, ns, ha, 3, testimages.UpgradeTo)
		tc.waitForStatefulSetReady(t, ns, ha, 3)
		tc.waitForValkeyPhaseAfterRollingUpdate(t, ns, ha, "OK")
		tc.waitForValkeyCondition(t, ns, ha, "SentinelUpdatePending", "False", rollingUpdateTimeout)
		for i := 0; i < 3; i++ {
			requireRootless(t, ns, fmt.Sprintf("%s-sentinel-%d", ha, i), "sentinel")
		}
		tc.requireNoWarningEvents(t, ns, ha)
	})

	t.Run("a drain failover completes with zero Warning Events", func(t *testing.T) {
		master := tc.findMasterPod(t, ns, drain, 3)
		tc.waitForConnectedReplicas(t, ns, master, 6379, 2)
		assert.Equal(t, "OK", tc.valkeyExec(t, ns, master, 6379, "SET", "restricted:drain", "kept"))
		tc.waitForConnectedReplicas(t, ns, master, 6379, 2)

		killedUID := tc.getPod(t, ns, master).UID
		tc.deletePod(t, ns, master)
		tc.requireDrainPromotedAReplica(t, ns, drain, 3, master)
		// As in the split-brain e2e: the StatefulSet counts the terminating pod as
		// ready, so the replacement is waited for by UID, and the returning pod may
		// self-claim master from the known-master record for a moment before the
		// steady-state check consolidates (ADR 0008, ADR 0011) -- so the master is
		// read only once exactly one pod answers master.
		tc.waitForPodRecreated(t, ns, master, killedUID)
		tc.waitForStatefulSetReady(t, ns, drain, 3)
		tc.waitForValkeyPhase(t, ns, drain, "OK")
		pollUntil(t, 2*time.Second, 2*time.Minute, func() (bool, string) {
			masters := 0
			for i := 0; i < 3; i++ {
				info := tc.valkeyExecQuick(t, ns, fmt.Sprintf("%s-%d", drain, i), 6379, "INFO", "replication")
				if strings.Contains(info, "role:master") {
					masters++
				}
			}
			return masters == 1, fmt.Sprintf("%d pods answer role:master", masters)
		}, "exactly one master after the drain")

		newMaster := tc.findMasterPod(t, ns, drain, 3)
		assert.Equal(t, "kept", tc.valkeyExec(t, ns, newMaster, 6379, "GET", "restricted:drain"))
		tc.waitForPodLabel(t, ns, newMaster, "vko.gtrfc.com/instanceRole", "master")
		tc.requireNoWarningEvents(t, ns, drain)
	})
}
