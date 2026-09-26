//go:build e2e

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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/utils/ptr"

	"github.com/guided-traffic/valkey-operator/test/testimages"
)

// docs/adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md
// on a node: what envtest cannot show, because it runs no kubelet and its API server
// drops hostUsers. The claims measured here are the ones the ADR could otherwise
// only read from the code:
//
//   - an existing persistent Sentinel cluster moves to a user namespace, a Localhost
//     profile and a digest-pinned image in one failover-aware roll, and keeps its
//     dataset -- the data files written before the move stay owned by uid 999 as the
//     container sees them (idmapped mounts);
//   - the pods run in a user namespace (uid_map is not the identity map) under a
//     seccomp filter, and the node really loaded the named profile file (a second
//     cluster naming a missing file does not start);
//   - the namespace enforces Pod Security "restricted" throughout, and the operator's
//     own namespace would pass it too.

// hardeningProfile is a node-local seccomp profile for the e2e only: allow by
// default, refuse a set of syscalls none of the generated containers makes. It is a
// fixture that proves the Localhost path, not a recommended profile -- the
// runtime's default filter refuses more.
const hardeningProfile = `{
  "defaultAction": "SCMP_ACT_ALLOW",
  "syscalls": [
    {
      "names": ["kexec_load", "kexec_file_load", "open_by_handle_at", "init_module", "finit_module",
        "delete_module", "reboot", "swapon", "swapoff", "bpf", "perf_event_open", "userfaultfd",
        "add_key", "request_key", "keyctl", "acct", "quotactl", "pivot_root"],
      "action": "SCMP_ACT_ERRNO"
    }
  ]
}`

const hardeningProfilePath = "profiles/vko-e2e.json"

// installSeccompProfile writes the profile below the kubelet's seccomp directory on
// every node. Kind nodes are containers named after the node, so this is a docker
// exec; there is no API for a node-local file.
func (tc *testClients) installSeccompProfile(t *testing.T, relPath, content string) {
	t.Helper()
	nodes, err := tc.kube.CoreV1().Nodes().List(context.Background(), metav1.ListOptions{})
	require.NoError(t, err)
	require.NotEmpty(t, nodes.Items)
	target := "/var/lib/kubelet/seccomp/" + relPath
	for _, node := range nodes.Items {
		cmd := exec.Command("docker", "exec", "-i", node.Name, "sh", "-c",
			fmt.Sprintf("mkdir -p \"$(dirname %s)\" && cat > %s", target, target))
		cmd.Stdin = strings.NewReader(content)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		require.NoError(t, cmd.Run(), "installing %s on node %s: %s", target, node.Name, stderr.String())
	}
}

// execInContainer runs a command in a container and returns its trimmed output.
func execInContainer(t *testing.T, namespace, pod, container string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmdArgs := append([]string{"exec", pod, "-n", namespace, "-c", container, "--"}, args...)
	cmd := exec.CommandContext(ctx, "kubectl", cmdArgs...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	require.NoError(t, cmd.Run(), "%s/%s %v: %s", pod, container, args, stderr.String())
	return strings.TrimSpace(stdout.String())
}

// requireUserNamespace asserts the container runs in a user namespace of its own:
// the host's is the identity map "0 0 4294967295".
func requireUserNamespace(t *testing.T, namespace, pod, container string) {
	t.Helper()
	uidMap := strings.Join(strings.Fields(execInContainer(t, namespace, pod, container, "cat", "/proc/self/uid_map")), " ")
	require.NotEmpty(t, uidMap, "%s/%s", pod, container)
	assert.NotEqual(t, "0 0 4294967295", uidMap, "%s/%s runs in the node's user namespace", pod, container)
	assert.True(t, strings.HasPrefix(uidMap, "0 "), "%s/%s: uid 0 inside maps somewhere else: %q", pod, container, uidMap)
}

// requireSeccompFilter asserts PID 1 of the container runs under a seccomp filter.
func requireSeccompFilter(t *testing.T, namespace, pod, container string) {
	t.Helper()
	status := execInContainer(t, namespace, pod, container, "cat", "/proc/1/status")
	assert.Contains(t, status, "Seccomp:\t2", "%s/%s must run under a seccomp filter", pod, container)
}

// userNamespacesRequiredEnv turns "this node cannot run a user namespace" into a
// failure. The CI legs run Kind inside Docker-in-Docker with containerd's native
// snapshotter, where a pod with hostUsers: false does not start -- measured
// 2026-09-26 with the CI Kind config (kindest/node v1.33.4, containerd 2.1.3):
// "mount callback failed ... container ID 1109000192 cannot be mapped to a host
// ID", and Kind's own createContainer hook failing with permission denied -- so CI
// does not set it; a local Kind cluster on overlayfs runs the user-namespace half.
const userNamespacesRequiredEnv = "E2E_REQUIRE_USER_NAMESPACES"

// userNamespacesSupported starts a restricted probe pod with hostUsers: false and
// reports whether its container starts, or the runtime's reason why not. A probe
// rather than a node field: the kubelet and the runtime can both refuse, and only a
// started container answers for both.
func (tc *testClients) userNamespacesSupported(t *testing.T, namespace string) (bool, string) {
	t.Helper()
	ctx := context.Background()
	probe := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "userns-probe", Namespace: namespace},
		Spec: corev1.PodSpec{
			HostUsers:     ptr.To(false),
			RestartPolicy: corev1.RestartPolicyNever,
			SecurityContext: &corev1.PodSecurityContext{
				RunAsNonRoot:   ptr.To(true),
				RunAsUser:      ptr.To(int64(999)),
				RunAsGroup:     ptr.To(int64(999)),
				SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
			},
			Containers: []corev1.Container{{
				Name:    "probe",
				Image:   testimages.Default(),
				Command: []string{"sh", "-c", "cat /proc/self/uid_map"},
				SecurityContext: &corev1.SecurityContext{
					AllowPrivilegeEscalation: ptr.To(false),
					ReadOnlyRootFilesystem:   ptr.To(true),
					Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
				},
			}},
		},
	}
	_, err := tc.kube.CoreV1().Pods(namespace).Create(ctx, probe, metav1.CreateOptions{})
	require.NoError(t, err, "creating the user-namespace probe")
	defer func() {
		_ = tc.kube.CoreV1().Pods(namespace).Delete(ctx, probe.Name, metav1.DeleteOptions{})
	}()

	supported, reason := false, ""
	pollUntil(t, 2*time.Second, 2*time.Minute, func() (bool, string) {
		pod, err := tc.kube.CoreV1().Pods(namespace).Get(ctx, probe.Name, metav1.GetOptions{})
		if err != nil {
			return false, err.Error()
		}
		if pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodRunning {
			supported = true
			return true, string(pod.Status.Phase)
		}
		for _, st := range pod.Status.ContainerStatuses {
			if w := st.State.Waiting; w != nil && w.Reason != "ContainerCreating" && w.Reason != "PodInitializing" {
				reason = w.Reason + ": " + w.Message
				return true, reason
			}
			if term := st.State.Terminated; term != nil && term.ExitCode != 0 {
				reason = term.Reason + ": " + term.Message
				return true, reason
			}
		}
		return false, string(pod.Status.Phase)
	}, "the user-namespace probe neither started nor reported why")
	return supported, reason
}

func TestE2E_PodHardening_UserNamespacesLocalhostSeccompAndDigest(t *testing.T) {
	t.Parallel()
	tc := newTestClients(t)

	ns := "e2e-hardening"
	cleanup := tc.createRestrictedNamespace(t, ns)
	defer cleanup()
	tc.installSeccompProfile(t, hardeningProfilePath, hardeningProfile)

	name := "hard"
	tc.createValkey(t, ns, buildValkeyObject(name, ns, map[string]interface{}{
		"replicas": int64(3),
		"image":    testimages.Default(),
		"sentinel": map[string]interface{}{"enabled": true, "replicas": int64(3)},
		"persistence": map[string]interface{}{
			"enabled": true, "mode": "aof", "size": "256Mi",
		},
		"metrics":  map[string]interface{}{"enabled": true},
		"observer": map[string]interface{}{"enabled": true},
	}))
	defer tc.deleteValkey(t, ns, name)

	// Whether this node can run a user namespace at all decides the one opt-in the
	// move below can carry. Without support the move runs without it, and only the
	// user-namespace assertions are skipped, by name and with the runtime's reason.
	userns, why := tc.userNamespacesSupported(t, ns)
	if !userns {
		require.NotEqual(t, "true", os.Getenv(userNamespacesRequiredEnv),
			"%s=true, but this node cannot start a pod with hostUsers: false: %s", userNamespacesRequiredEnv, why)
		t.Logf("user namespaces unsupported on this node (%s): the move runs without them", why)
	}
	podSecurity := map[string]interface{}{
		"seccompProfile": map[string]interface{}{"type": "Localhost", "localhostProfile": hardeningProfilePath},
	}
	if userns {
		podSecurity["userNamespaces"] = true
	}

	data := map[string]string{"hard:a": "1", "hard:b": "2", "hard:c": "3"}
	var digestImage string
	// The owner of each volume root as the container saw it before the move. On
	// Kind the root is the provisioner's hostPath directory, root-owned and 0777,
	// and a cluster this operator built never ran the ADR 0032 repair, so it stays
	// uid 0; what the move must not change is what the container sees.
	rootOwnerBefore := map[string]string{}
	t.Run("the cluster runs and holds a dataset before the move", func(t *testing.T) {
		tc.waitForStatefulSetReady(t, ns, name, 3)
		tc.waitForStatefulSetReady(t, ns, name+"-sentinel", 3)
		tc.waitForValkeyPhase(t, ns, name, "OK")
		master := tc.findMasterPod(t, ns, name, 3)
		tc.valkeyMSET(t, ns, master, 6379, data)
		for i := 0; i < 3; i++ {
			pod := fmt.Sprintf("%s-%d", name, i)
			tc.waitForReplicaSyncedOrMaster(t, ns, pod)
			rootOwnerBefore[pod] = execInContainer(t, ns, pod, "valkey", "stat", "-c", "%u", "/data")
		}

		// The digest the node actually pulled for the tag, so the pinned reference
		// is resolvable offline in Kind and names exactly the image that runs.
		pod := tc.getPod(t, ns, name+"-0")
		for _, st := range pod.Status.ContainerStatuses {
			if st.Name == "valkey" {
				_, digest, ok := strings.Cut(st.ImageID, "@")
				require.True(t, ok, "imageID without a digest: %q", st.ImageID)
				digestImage = testimages.Default() + "@" + digest
			}
		}
		require.NotEmpty(t, digestImage)
	})

	t.Run("one patch moves the cluster: user namespace, Localhost profile, digest, Sentinel resources", func(t *testing.T) {
		tc.patchValkeySpec(t, ns, name, map[string]interface{}{
			"image":       digestImage,
			"podSecurity": podSecurity,
			"sentinel.resources": map[string]interface{}{
				"requests": map[string]interface{}{"cpu": "10m", "memory": "32Mi"},
				"limits":   map[string]interface{}{"memory": "128Mi"},
			},
		})
		tc.waitForValkeyEvent(t, ns, name, "RollingUpdateComplete", 10*time.Minute,
			"the data tier never completed the roll onto the hardened template")
		tc.waitForValkeyEvent(t, ns, name, "SentinelUpdateComplete", 10*time.Minute,
			"the Sentinel tier never completed the roll onto the hardened template")
		tc.waitForStatefulSetReady(t, ns, name, 3)
		tc.waitForStatefulSetReady(t, ns, name+"-sentinel", 3)
		tc.waitForDeploymentReady(t, ns, name+"-observer")
		tc.waitForValkeyPhase(t, ns, name, "OK")
	})

	t.Run("every generated pod carries the hardening", func(t *testing.T) {
		pods, err := tc.kube.CoreV1().Pods(ns).List(context.Background(), metav1.ListOptions{
			LabelSelector: "app.kubernetes.io/instance=" + name,
		})
		require.NoError(t, err)
		kinds := map[string]int{}
		for i := range pods.Items {
			p := &pods.Items[i]
			if p.DeletionTimestamp != nil {
				continue
			}
			kinds[p.Labels["app.kubernetes.io/component"]]++
			if userns {
				assert.Equal(t, false, derefBool(p.Spec.HostUsers, true), "%s: hostUsers", p.Name)
			} else {
				assert.Nil(t, p.Spec.HostUsers, "%s: hostUsers is set only when asked for", p.Name)
			}
			assert.Equal(t, false, derefBool(p.Spec.EnableServiceLinks, true), "%s: enableServiceLinks", p.Name)
			require.NotNil(t, p.Spec.SecurityContext.SeccompProfile, p.Name)
			assert.Equal(t, corev1.SeccompProfileTypeLocalhost, p.Spec.SecurityContext.SeccompProfile.Type, p.Name)
			for _, c := range append(append([]corev1.Container{}, p.Spec.InitContainers...), p.Spec.Containers...) {
				assert.Equal(t, false, derefBool(c.SecurityContext.Privileged, true), "%s/%s: privileged", p.Name, c.Name)
			}
		}
		assert.Equal(t, 3, kinds["valkey"], "data pods: %v", kinds)
		assert.Equal(t, 3, kinds["sentinel"], "Sentinel pods: %v", kinds)
		assert.GreaterOrEqual(t, kinds["observer"], 1, "observer pod: %v", kinds)

		// A tag and a digest: the version label is the tag, which the API server
		// refused as "sha256:..." before ADR 0033 D5.
		tag := testimages.Default()[strings.LastIndex(testimages.Default(), ":")+1:]
		assert.Equal(t, tag, tc.getPod(t, ns, name+"-0").Labels["app.kubernetes.io/version"])
		assert.Equal(t, digestImage, containerImage(tc.getPod(t, ns, name+"-0"), "valkey"))

		sentinel := tc.getPod(t, ns, name+"-sentinel-0")
		for _, c := range append(append([]corev1.Container{}, sentinel.Spec.InitContainers...), sentinel.Spec.Containers...) {
			assert.Equal(t, "32Mi", c.Resources.Requests.Memory().String(), "%s: spec.sentinel.resources", c.Name)
		}
	})

	t.Run("the processes run under the Localhost filter", func(t *testing.T) {
		for i := 0; i < 3; i++ {
			requireSeccompFilter(t, ns, fmt.Sprintf("%s-%d", name, i), "valkey")
			requireSeccompFilter(t, ns, fmt.Sprintf("%s-sentinel-%d", name, i), "sentinel")
		}
		// Inside, the posture of ADR 0032 is unchanged: uid 999, nothing in the
		// bounding set, no_new_privs -- the user namespace sits underneath it.
		requireRootless(t, ns, name+"-0", "valkey")
		// No Service environment, apart from the API server's own variables kubelet
		// always sets.
		env := execInContainer(t, ns, name+"-0", "valkey", "env")
		for _, line := range strings.Split(env, "\n") {
			if strings.Contains(line, "_SERVICE_HOST=") {
				assert.True(t, strings.HasPrefix(line, "KUBERNETES_SERVICE_HOST="), "service link injected: %s", line)
			}
		}
	})

	t.Run("the processes run in a user namespace", func(t *testing.T) {
		if !userns {
			t.Skipf("user namespaces unsupported on this node (%s); set %s=true to fail instead", why, userNamespacesRequiredEnv)
		}
		for i := 0; i < 3; i++ {
			requireUserNamespace(t, ns, fmt.Sprintf("%s-%d", name, i), "valkey")
			requireUserNamespace(t, ns, fmt.Sprintf("%s-sentinel-%d", name, i), "sentinel")
		}
	})

	t.Run("the data written before the move is intact and keeps its owners", func(t *testing.T) {
		for i := 0; i < 3; i++ {
			pod := fmt.Sprintf("%s-%d", name, i)
			tc.waitForReplicaSyncedOrMaster(t, ns, pod)
			for k, want := range data {
				assert.Equal(t, want, tc.valkeyExec(t, ns, pod, 6379, "GET", k), "%s: %s", pod, k)
			}
			// Through the idmapped mount (when the move added a user namespace) every
			// file keeps the owner the container saw before the move: what valkey-server wrote is 999, the volume root is
			// what it was. Without the mapping they would read as the overflow uid
			// 65534, and valkey-server could not write its AOF.
			owners := execInContainer(t, ns, pod, "valkey", "sh", "-c",
				"stat -c %u /data/appendonlydir /data/appendonlydir/* | sort -u")
			assert.Equal(t, "999", owners, "%s: owners of what valkey-server wrote", pod)
			assert.Equal(t, rootOwnerBefore[pod], execInContainer(t, ns, pod, "valkey", "stat", "-c", "%u", "/data"),
				"%s: the volume root reads as it did before the move", pod)
		}
		master := tc.findMasterPod(t, ns, name, 3)
		assert.Equal(t, "OK", tc.valkeyExec(t, ns, master, 6379, "SET", "hard:after", "1"),
			"the master writes after the move: no silent MISCONF")
	})

	t.Run("the roll raised no Warning", func(t *testing.T) {
		tc.requireNoWarningEvents(t, ns, name)
	})

	t.Run("a Localhost profile missing on the node keeps the pod from starting", func(t *testing.T) {
		// The negative control: the node resolves the named file, so the profile the
		// pods above run under is the fixture, not a silent fallback.
		missing := "hard-missing"
		tc.createValkey(t, ns, buildValkeyObject(missing, ns, map[string]interface{}{
			"replicas": int64(1),
			"image":    testimages.Default(),
			"podSecurity": map[string]interface{}{
				"seccompProfile": map[string]interface{}{"type": "Localhost", "localhostProfile": "profiles/vko-e2e-missing.json"},
			},
		}))
		defer tc.deleteValkey(t, ns, missing)
		pollUntil(t, 2*time.Second, 3*time.Minute, func() (bool, string) {
			pod, err := tc.kube.CoreV1().Pods(ns).Get(context.Background(), missing+"-0", metav1.GetOptions{})
			if err != nil {
				return false, err.Error()
			}
			statuses := append(append([]corev1.ContainerStatus{}, pod.Status.InitContainerStatuses...),
				pod.Status.ContainerStatuses...)
			last := ""
			for _, st := range statuses {
				if st.State.Waiting != nil {
					last = st.State.Waiting.Reason + ": " + st.State.Waiting.Message
					if strings.Contains(st.State.Waiting.Message, "vko-e2e-missing.json") {
						return true, last
					}
				}
			}
			return false, last
		}, "a pod naming a missing Localhost profile must be refused by the runtime, naming the file")
	})

	t.Run("a Localhost profile the operator does not allow is refused and reported", func(t *testing.T) {
		// ADR 0033 D9 through the real chart: test/e2e/helm-values.yaml allows the
		// two profiles above and nothing else, so this one is never written.
		refused := "hard-refused"
		tc.createValkey(t, ns, buildValkeyObject(refused, ns, map[string]interface{}{
			"replicas": int64(1),
			"image":    testimages.Default(),
			"podSecurity": map[string]interface{}{
				"seccompProfile": map[string]interface{}{"type": "Localhost", "localhostProfile": "profiles/vko-e2e-not-allowed.json"},
			},
		}))
		defer tc.deleteValkey(t, ns, refused)
		pollUntil(t, 2*time.Second, 2*time.Minute, func() (bool, string) {
			cr, err := tc.dynamic.Resource(valkeyGVR).Namespace(ns).Get(context.Background(), refused, metav1.GetOptions{})
			if err != nil {
				return false, err.Error()
			}
			phase, _, _ := unstructured.NestedString(cr.Object, "status", "phase")
			conditions, _, _ := unstructured.NestedSlice(cr.Object, "status", "conditions")
			for _, c := range conditions {
				cond, _ := c.(map[string]interface{})
				if cond["type"] == "ReconcileBlocked" {
					return cond["status"] == "True" && cond["reason"] == "SeccompProfileNotAllowed",
						fmt.Sprintf("%v/%v: %v", cond["status"], cond["reason"], cond["message"])
				}
			}
			return false, fmt.Sprintf("no ReconcileBlocked yet, phase %q", phase)
		}, "a Localhost profile outside the operator's allow-list must be reported")
		_, err := tc.kube.AppsV1().StatefulSets(ns).Get(context.Background(), refused, metav1.GetOptions{})
		assert.True(t, apierrors.IsNotFound(err), "the StatefulSet must never be created: %v", err)
	})

	t.Run("the operator's own namespace would pass Pod Security restricted", func(t *testing.T) {
		out, err := exec.Command("kubectl", "label", "--dry-run=server", "--overwrite", "ns",
			"valkey-operator-system", "pod-security.kubernetes.io/enforce=restricted").CombinedOutput()
		require.NoError(t, err, string(out))
		assert.NotContains(t, string(out), "Warning", "the operator pod violates restricted: %s", out)
	})
}

func derefBool(b *bool, def bool) bool {
	if b == nil {
		return def
	}
	return *b
}
