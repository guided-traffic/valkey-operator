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

const sentinelAuthSwitchPassword = "e2e-sentinel-auth-switch"

// TestE2E_SentinelDisableAuthSwitchRollsBothTiers switches spec.sentinel.disableAuth on
// a running three-pod Sentinel cluster with auth, in both directions. The flag reaches
// the data pods (the sidecar's Sentinel credential) and the Sentinel config
// (requirepass), so the data tier rolls first and its master handover needs a Sentinel
// failover -- asked of Sentinels that still run the setting they booted with, because
// the Sentinel roll waits for the data tier. Without the Sentinel credential fallback
// (docs/adr/0016-authentication-and-tls-posture.md, D9) every SENTINEL command was
// refused, the data roll cycled before the master without end and the Sentinel tier
// never rolled: measured on a live cluster, direction true -> false.
func TestE2E_SentinelDisableAuthSwitchRollsBothTiers(t *testing.T) {
	t.Parallel()
	tc := newTestClients(t)
	ns := "e2e-sentinel-auth-switch"
	cleanup := tc.createNamespace(t, ns)
	defer cleanup()

	name := "sas"
	sentinelSts := name + "-sentinel"
	_, err := tc.kube.CoreV1().Secrets(ns).Create(context.Background(), &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name + "-auth", Namespace: ns},
		StringData: map[string]string{"password": sentinelAuthSwitchPassword},
	}, metav1.CreateOptions{})
	if !apierrors.IsAlreadyExists(err) {
		require.NoError(t, err)
	}

	tc.createValkey(t, ns, buildValkeyObject(name, ns, map[string]interface{}{
		"replicas": int64(3),
		"image":    testimages.Default(),
		"auth":     map[string]interface{}{"secretName": name + "-auth", "secretPasswordKey": "password"},
		"sentinel": map[string]interface{}{"enabled": true, "replicas": int64(3), "disableAuth": true},
	}))
	defer tc.deleteValkey(t, ns, name)

	tc.waitForStatefulSetReady(t, ns, name, 3)
	tc.waitForStatefulSetReady(t, ns, sentinelSts, 3)
	tc.waitForValkeyPhase(t, ns, name, "OK")
	tc.waitForSentinelPosture(t, ns, sentinelSts, 3, false)

	master := tc.waitForLabelledMaster(t, ns, name)
	require.Equal(t, "OK", tc.authedValkeyExec(t, ns, master, "SET", "sas:before", "1"))

	for _, step := range []struct {
		name        string
		disableAuth bool
	}{
		{"switching Sentinel client auth on completes both rolls", false},
		{"switching Sentinel client auth off completes both rolls", true},
	} {
		t.Run(step.name, func(t *testing.T) {
			started := metav1.Now()
			dataUIDs := tc.dataPodUIDs(t, ns, name, 3)
			sentinelUIDs := tc.dataPodUIDs(t, ns, sentinelSts, 3)

			tc.patchValkeySpec(t, ns, name, map[string]interface{}{"sentinel.disableAuth": step.disableAuth})

			tc.requireRollCompletedSince(t, ns, name, started)
			tc.waitForValkeyPhaseAfterRollingUpdate(t, ns, name, "OK")
			assert.Positive(t, tc.countValkeyEventsSince(t, ns, name, "FailoverTriggered", started),
				"the data tier must have handed the master over through Sentinel")
			requireAllReplaced(t, dataUIDs, tc.dataPodUIDs(t, ns, name, 3))
			requireAllReplaced(t, sentinelUIDs, tc.dataPodUIDs(t, ns, sentinelSts, 3))

			tc.waitForSentinelPosture(t, ns, sentinelSts, 3, !step.disableAuth)
			master := tc.waitForLabelledMaster(t, ns, name)
			assert.Equal(t, "1", tc.authedValkeyExec(t, ns, master, "GET", "sas:before"),
				"the dataset survives the roll")
		})
	}

	t.Run("no Warning Event on either switch", func(t *testing.T) {
		tc.requireNoWarningEvents(t, ns, name)
	})
}

// authedValkeyExec runs valkey-cli against the data port with the cluster password.
func (tc *testClients) authedValkeyExec(t *testing.T, namespace, pod string, args ...string) string {
	t.Helper()
	return tc.valkeyExec(t, namespace, pod, 6379,
		append([]string{"-a", sentinelAuthSwitchPassword, "--no-auth-warning"}, args...)...)
}

// waitForLabelledMaster waits until exactly one data pod carries instanceRole=master and
// returns it. The data port needs the password, so the label stands in for the
// INFO-based findMasterPod.
func (tc *testClients) waitForLabelledMaster(t *testing.T, namespace, name string) string {
	t.Helper()
	var master string
	pollUntil(t, pollInterval, rollingUpdateTimeout, func() (bool, string) {
		var masters []string
		for _, pod := range tc.listComponentPods(t, namespace, name, "valkey") {
			if pod.Labels["vko.gtrfc.com/instanceRole"] == "master" && pod.DeletionTimestamp == nil {
				masters = append(masters, pod.Name)
			}
		}
		if len(masters) != 1 {
			return false, fmt.Sprintf("master-labelled pods: %v", masters)
		}
		master = masters[0]
		return true, ""
	}, "%s/%s never had exactly one master-labelled pod", namespace, name)
	return master
}

// waitForSentinelPosture waits until every Sentinel either refuses an anonymous PING
// with NOAUTH (requirePassword) or answers it with PONG.
func (tc *testClients) waitForSentinelPosture(t *testing.T, namespace, stsName string, replicas int,
	requirePassword bool) {
	t.Helper()
	want := "PONG"
	if requirePassword {
		want = "NOAUTH"
	}
	pollUntil(t, pollInterval, rollingUpdateTimeout, func() (bool, string) {
		for i := 0; i < replicas; i++ {
			pod := fmt.Sprintf("%s-%d", stsName, i)
			if out := anonymousSentinelPing(namespace, pod); !strings.Contains(out, want) {
				return false, fmt.Sprintf("%s answered an anonymous PING with %q", pod, out)
			}
		}
		return true, ""
	}, "%s/%s: not every Sentinel answers an anonymous PING with %s", namespace, stsName, want)
}

// anonymousSentinelPing sends PING to the Sentinel port without AUTH and returns what
// valkey-cli printed on either stream; an error reply is the answer here, not a failure.
func anonymousSentinelPing(namespace, pod string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "kubectl", "exec", pod, "-n", namespace, "-c", "sentinel", "--",
		"valkey-cli", "--raw", "-p", "26379", "PING")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	_ = cmd.Run()
	return strings.TrimSpace(out.String())
}
