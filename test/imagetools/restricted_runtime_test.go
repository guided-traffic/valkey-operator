//go:build imagetools

package imagetools

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/builder"
	"github.com/guided-traffic/valkey-operator/internal/common"
)

// The posture of docs/adr/0032-generated-pods-run-rootless.md, run against the
// real images: uid 999, every capability dropped, no_new_privs, a read-only root
// filesystem and a tmpfs for every emptyDir -- the Docker equivalent of what the
// builder renders. The T31 analysis measured all of this by hand once; these tests
// make the measurement permanent, so a Valkey release that needs root, a capability
// or a writable root filesystem fails on the Renovate PR that brings it.
//
// The pre-flight, the repair, the probe and the drain hook are the generated
// commands, read out of the builder. valkey-server and valkey-sentinel run with a
// minimal hand-written configuration: what is measured is the process under the
// posture, not the generated config.
//
// What this does not cover: a Kubernetes node. containerd's RuntimeDefault seccomp
// profile, kubelet's fsGroup handling and the projected-token mode are the
// restricted-namespace e2e's.

// restrictedFlags is the Docker rendering of the posture.
var restrictedFlags = []string{
	"--user", "999:999", "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges",
}

// dockerRun runs docker with args and returns stdout; the error is returned rather
// than asserted, because some cases here expect the container to fail.
func dockerRun(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), imageProbeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return strings.TrimSpace(stdout.String()), stderr.String(), err
}

// restricted runs script under the posture, with a tmpfs owned by 999 at each path.
func restricted(t *testing.T, image string, tmpfs []string, script string) string {
	t.Helper()
	args := append([]string{"run", "--rm"}, restrictedFlags...)
	for _, p := range tmpfs {
		args = append(args, "--tmpfs", p+":rw,uid=999,gid=999,mode=0755")
	}
	args = append(args, "--entrypoint", "sh", image, "-c", script)
	out, stderr, err := dockerRun(t, args...)
	require.NoError(t, err, "restricted run failed:\nstdout:\n%s\nstderr:\n%s", out, stderr)
	return out
}

func persistentValkey(image string) *vkov1.Valkey {
	return &vkov1.Valkey{
		Spec: vkov1.ValkeySpec{
			Replicas:    3,
			Image:       image,
			Persistence: &vkov1.PersistenceSpec{Enabled: true, Mode: vkov1.PersistenceModeAOF},
		},
	}
}

// initScript returns the `sh -c` body of the named init container of the data
// template, with the ownership repair inserted as the controller does it.
func initScript(t *testing.T, v *vkov1.Valkey, name string) string {
	t.Helper()
	sts := builder.BuildStatefulSet(v, "operator:test")
	builder.WithDataOwnershipRepair(sts)
	for _, c := range sts.Spec.Template.Spec.InitContainers {
		if c.Name == name {
			require.Len(t, c.Command, 3)
			return c.Command[2]
		}
	}
	t.Fatalf("no init container %s", name)
	return ""
}

// TestRestrictedRuntime_ValkeyServerPersistsAndAnswers: uid 999, zero capabilities,
// no_new_privs; RDB and AOF writes and an AOF rewrite succeed; the generated probe
// answers.
func TestRestrictedRuntime_ValkeyServerPersistsAndAnswers(t *testing.T) {
	t.Parallel()
	for name, image := range pinnedImages() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			probe := strings.Join(builder.ProbeCommand(&vkov1.Valkey{Spec: vkov1.ValkeySpec{Image: image}}), " ")
			script := fmt.Sprintf(`
grep -E '^(Uid|CapEff|CapBnd|NoNewPrivs):' /proc/1/status | tr -s '\t' ' '
cd /data
valkey-server --dir /data --appendonly yes --save "" --daemonize yes --pidfile /data/v.pid --logfile /data/v.log
i=0; while [ $i -lt 50 ]; do valkey-cli ping >/dev/null 2>&1 && break; sleep 0.2; i=$((i+1)); done
echo "probe:$(%s)"
valkey-cli set k v >/dev/null
echo "bgsave:$(valkey-cli bgsave | tr -d '\r')"
i=0; while [ $i -lt 50 ] && ! valkey-cli info persistence | grep -q '^rdb_bgsave_in_progress:0'; do sleep 0.2; i=$((i+1)); done
echo "rewrite:$(valkey-cli bgrewriteaof | tr -d '\r')"
i=0; while [ $i -lt 100 ]; do
  valkey-cli info persistence | tr -d '\r' | grep -q '^aof_rewrite_in_progress:0' &&
    valkey-cli info persistence | tr -d '\r' | grep -q '^aof_rewrite_scheduled:0' && break
  sleep 0.2; i=$((i+1))
done
valkey-cli info persistence | tr -d '\r' | grep -E '^(rdb_last_bgsave_status|rdb_saves|aof_last_bgrewrite_status|aof_rewrites|aof_last_write_status):'
ls /data/dump.rdb >/dev/null && echo "rdb-file:present"
valkey-cli set after-rewrite 1 | tr -d '\r'`, probe)
			out := restricted(t, image, []string{"/data"}, script)

			assert.Contains(t, out, "Uid: 999 999 999 999")
			assert.Contains(t, out, "CapEff: 0000000000000000")
			// CapEff is empty for any non-root uid; the bounding set is what proves
			// every capability was dropped.
			assert.Contains(t, out, "CapBnd: 0000000000000000")
			assert.Contains(t, out, "NoNewPrivs: 1")
			assert.Contains(t, out, "probe:PONG")
			// The replies and the counters, not only the status fields, which start
			// at "ok" before anything ran.
			assert.Contains(t, out, "bgsave:Background saving started")
			assert.Contains(t, out, "rewrite:Background append only file rewriting")
			assert.Contains(t, out, "rdb_saves:1")
			assert.Contains(t, out, "rdb_last_bgsave_status:ok")
			assert.Contains(t, out, "aof_rewrites:1")
			assert.Contains(t, out, "aof_last_bgrewrite_status:ok")
			assert.Contains(t, out, "aof_last_write_status:ok")
			assert.Contains(t, out, "rdb-file:present")
			assert.True(t, strings.HasSuffix(out, "OK"), "a write after the rewrite is accepted:\n%s", out)
		})
	}
}

// TestRestrictedRuntime_SentinelRewritesItsConfig: valkey-sentinel starts under the
// posture and persists a SENTINEL SET into its config file on the emptyDir.
func TestRestrictedRuntime_SentinelRewritesItsConfig(t *testing.T) {
	t.Parallel()
	for name, image := range pinnedImages() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			conf := builder.SentinelConfigMountPath + "/" + builder.SentinelConfigKey
			script := fmt.Sprintf(`
cd /data
valkey-server --port 6379 --daemonize yes --dir /data --save "" --logfile /data/v.log
printf 'port 26379\nsentinel monitor m 127.0.0.1 6379 1\n' > %[1]s
valkey-sentinel %[1]s --daemonize yes --logfile /data/s.log
i=0; while [ $i -lt 50 ]; do valkey-cli -p 26379 ping >/dev/null 2>&1 && break; sleep 0.2; i=$((i+1)); done
valkey-cli -p 26379 SENTINEL SET m down-after-milliseconds 5000 | tr -d '\r'
sleep 1
grep -c 'down-after-milliseconds m 5000' %[1]s`, conf)
			out := restricted(t, image, []string{"/data", builder.SentinelConfigMountPath}, script)
			lines := strings.Split(out, "\n")
			assert.Equal(t, "OK", lines[0], out)
			assert.Equal(t, "1", lines[len(lines)-1], "the rewrite landed in the config file:\n%s", out)
		})
	}
}

// TestRestrictedRuntime_DrainPreStopReleases: the generated preStop hook returns as
// soon as the marker exists, under a read-only root.
func TestRestrictedRuntime_DrainPreStopReleases(t *testing.T) {
	t.Parallel()
	for name, image := range pinnedImages() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			v := &vkov1.Valkey{Spec: vkov1.ValkeySpec{Replicas: 3, Image: image}}
			sts := builder.BuildStatefulSet(v, "operator:test")
			var hook []string
			for _, c := range sts.Spec.Template.Spec.Containers {
				if c.Name == builder.ValkeyContainerName && c.Lifecycle != nil && c.Lifecycle.PreStop != nil {
					hook = c.Lifecycle.PreStop.Exec.Command
				}
			}
			require.Len(t, hook, 3, "multi-replica non-Sentinel pods carry the drain preStop")
			marker := common.DrainSignalMountPath + "/" + common.DrainCompleteFile
			// Bounded: the loop exits 0 after its 60 s bound whether or not it ever saw
			// the marker, so only a release well inside that bound proves it did. The
			// negative control shows the same bound fires without the marker.
			script := fmt.Sprintf(
				"touch %[1]s && timeout 5 sh -c '%[2]s' && echo RELEASED; "+
					"rm %[1]s; timeout 3 sh -c '%[2]s'; echo \"without-marker:$?\"", marker, hook[2])
			out := restricted(t, image, []string{common.DrainSignalMountPath}, script)
			assert.Contains(t, out, "RELEASED")
			assert.Contains(t, out, "without-marker:124", "without the marker the hook must still be waiting")
		})
	}
}

// TestRestrictedRuntime_PreflightAndRepair is the migration of ADR 0032 D2 in one
// volume: a root writer leaves a legacy AOF dataset, the pre-flight refuses it as
// uid 999 and names the fix, the repair re-owns it as uid 0 with CAP_CHOWN and
// nothing else, and the pre-flight then passes.
func TestRestrictedRuntime_PreflightAndRepair(t *testing.T) {
	t.Parallel()
	for name, image := range pinnedImages() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			v := persistentValkey(image)
			preflight := initScript(t, v, builder.DataWritableCheckContainerName)
			repair := initScript(t, v, builder.DataOwnershipRepairContainerName)

			volume := fmt.Sprintf("vko-imagetools-%s", strings.NewReplacer(".", "-", "/", "-", ":", "-").Replace(image))
			_, _, _ = dockerRun(t, "volume", "rm", "-f", volume)
			_, stderr, err := dockerRun(t, "volume", "create", volume)
			require.NoError(t, err, stderr)
			t.Cleanup(func() { _, _, _ = dockerRun(t, "volume", "rm", "-f", volume) })
			mount := volume + ":" + builder.DataDir + ":nocopy"

			// Today's shape: root, the runtime's capabilities, umask 0022 -- plus the
			// lost+found of an ext4 volume root, root:root 0700.
			_, stderr, err = dockerRun(t, "run", "--rm", "-v", mount, "--entrypoint", "sh", image, "-c",
				"chmod 0755 /data && mkdir -p /data/appendonlydir /data/lost+found && chmod 0700 /data/lost+found && "+
					"echo x > /data/appendonlydir/appendonly.aof.1.incr.aof && echo x > /data/dump.rdb")
			require.NoError(t, err, stderr)

			run := func(user string, caps []string, script string) (string, string, error) {
				args := []string{"run", "--rm", "--user", user, "--read-only", "--cap-drop", "ALL",
					"--security-opt", "no-new-privileges", "-v", mount}
				for _, c := range caps {
					args = append(args, "--cap-add", c)
				}
				return dockerRun(t, append(args, "--entrypoint", "sh", image, "-c", script)...)
			}

			_, stderr, err = run("999:999", nil, preflight)
			require.Error(t, err, "the pre-flight must refuse root-written data")
			assert.Contains(t, stderr, "chown -R 999:999", "and name the fix")

			_, stderr, err = run("0:0", []string{string(corev1.Capability("CHOWN"))}, repair)
			require.NoError(t, err, "the repair needs CAP_CHOWN and nothing else: %s", stderr)

			out, stderr, err := run("999:999", nil, preflight)
			assert.NoError(t, err, "after the repair the pre-flight passes:\n%s\n%s", out, stderr)

			// A migrated pod keeps the repair in its spec and re-runs it on every
			// sandbox restart. The second run cannot enter lost+found, now 999:999
			// 0700, without a DAC override -- and must not block the pod for it.
			_, stderr, err = run("0:0", []string{string(corev1.Capability("CHOWN"))}, repair)
			assert.NoError(t, err, "a second repair run is a best-effort no-op: %s", stderr)
			out, stderr, err = run("999:999", nil, preflight)
			assert.NoError(t, err, "and the pre-flight still passes:\n%s\n%s", out, stderr)
		})
	}
}
