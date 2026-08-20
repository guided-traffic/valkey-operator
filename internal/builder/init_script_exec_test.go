package builder

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
)

// The non-Sentinel init container elects the pod's replication role in shell code.
// Asserting on the script text only proves that a line exists, not that the branch
// it belongs to is taken, so these tests execute the generated script with stubbed
// `valkey-cli` and `timeout` binaries and inspect the config it writes.

// initScriptEnv is a temporary filesystem that stands in for the three config
// mounts of the init container, plus the stub binaries on PATH.
type initScriptEnv struct {
	root         string
	masterConf   string
	replicaConf  string
	writableConf string
	binDir       string
}

// newInitScriptEnv creates the mount layout. knownMaster is the address written
// into the replica config's `replicaof` directive — the operator maintains it via
// the known-master annotation. An empty value omits the directive.
func newInitScriptEnv(t *testing.T, knownMaster string) *initScriptEnv {
	t.Helper()

	root := t.TempDir()
	e := &initScriptEnv{
		root:         root,
		masterConf:   filepath.Join(root, "master"),
		replicaConf:  filepath.Join(root, "replica"),
		writableConf: filepath.Join(root, "writable"),
		binDir:       filepath.Join(root, "bin"),
	}
	for _, dir := range []string{e.masterConf, e.replicaConf, e.writableConf, e.binDir} {
		require.NoError(t, os.MkdirAll(dir, 0o755))
	}

	writeFile(t, filepath.Join(e.masterConf, ValkeyConfigKey), "# master config\n")
	replica := "# replica config\n"
	if knownMaster != "" {
		replica += fmt.Sprintf("replicaof %s %d\n", knownMaster, ValkeyPort)
	}
	writeFile(t, filepath.Join(e.replicaConf, ValkeyConfigKey), replica)

	// `timeout` is not installed on every developer machine (macOS ships without
	// it), so the stub simply drops the duration and execs the command.
	writeFile(t, filepath.Join(e.binDir, "timeout"), "#!/bin/sh\nshift\nexec \"$@\"\n")
	require.NoError(t, os.Chmod(filepath.Join(e.binDir, "timeout"), 0o755))

	return e
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

// stubValkeyCLI installs a `valkey-cli` that answers `INFO replication` from the
// given per-host canned output. A host that is absent from the map exits non-zero
// with no output, which is what an unreachable peer looks like to the script.
func (e *initScriptEnv) stubValkeyCLI(t *testing.T, responses map[string]string) {
	t.Helper()

	var sb strings.Builder
	sb.WriteString("#!/bin/sh\nHOST=\"\"\nwhile [ $# -gt 0 ]; do\n  case \"$1\" in\n    -h) HOST=\"$2\"; shift 2 ;;\n    *) shift ;;\n  esac\ndone\ncase \"$HOST\" in\n")
	hosts := make([]string, 0, len(responses))
	for host := range responses {
		hosts = append(hosts, host)
	}
	sort.Strings(hosts)
	for _, host := range hosts {
		fmt.Fprintf(&sb, "  %s) printf '%%s' '%s'; exit 0 ;;\n", host, responses[host])
	}
	sb.WriteString("  *) exit 1 ;;\nesac\n")

	path := filepath.Join(e.binDir, "valkey-cli")
	writeFile(t, path, sb.String())
	require.NoError(t, os.Chmod(path, 0o755))
}

// run executes the init script of the given Valkey CR as the named pod and
// returns the config the script produced in the writable mount, plus its log output.
func (e *initScriptEnv) run(t *testing.T, v *vkov1.Valkey, hostname string) (string, string) {
	t.Helper()

	sts := BuildStatefulSet(v, testOperatorImage)
	require.NotEmpty(t, sts.Spec.Template.Spec.InitContainers)
	script := sts.Spec.Template.Spec.InitContainers[0].Command[2]

	// Redirect the absolute mount paths to the temporary directories. The longer
	// paths must be replaced first, since ConfigMountPath is a prefix of both.
	script = strings.ReplaceAll(script, ReplicaConfigMountPath, e.replicaConf)
	script = strings.ReplaceAll(script, WritableConfigMountPath, e.writableConf)
	script = strings.ReplaceAll(script, ConfigMountPath, e.masterConf)

	cmd := exec.Command("sh", "-c", script)
	cmd.Env = append(os.Environ(),
		"HOSTNAME="+hostname,
		"PATH="+e.binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "init script failed: %s", string(out))

	produced, readErr := os.ReadFile(filepath.Join(e.writableConf, ValkeyConfigKey))
	require.NoError(t, readErr)
	return string(produced), string(out)
}

func infoMaster(connectedSlaves int) string {
	return fmt.Sprintf("role:master\r\nconnected_slaves:%d\r\n", connectedSlaves)
}

func infoReplica(masterHost string) string {
	return fmt.Sprintf("role:slave\r\nmaster_host:%s\r\nconnected_slaves:0\r\n", masterHost)
}

func testValkeyForInitScript(replicas int32) *vkov1.Valkey {
	return newTestValkey("test", func(v *vkov1.Valkey) {
		v.Namespace = "ns"
		v.Spec.Replicas = replicas
	})
}

func podFQDN(ordinal int) string {
	return fmt.Sprintf("test-%d.test-headless.ns.svc.cluster.local", ordinal)
}

// A returning pod-0 must not elect itself master while the operator has promoted
// pod-1: with two replicas the promoted master has no replicas attached yet, so
// peer discovery alone rejects it and the ordinal fallback would split the cluster.
func TestInitScript_ReturningPod0FollowsKnownMaster(t *testing.T) {
	env := newInitScriptEnv(t, podFQDN(1))
	env.stubValkeyCLI(t, map[string]string{podFQDN(1): infoMaster(0)})

	conf, logs := env.run(t, testValkeyForInitScript(2), "test-0")

	assert.Contains(t, conf, "replicaof "+podFQDN(1),
		"pod-0 must join the promoted master instead of booting as an independent master")
	assert.Contains(t, logs, "Using known master from replica config")
}

// Without a failover in progress the replica config points at pod-0 itself.
// Pod-0 must ignore that self-reference and keep the master config.
func TestInitScript_Pod0IgnoresSelfAsKnownMaster(t *testing.T) {
	env := newInitScriptEnv(t, podFQDN(0))
	env.stubValkeyCLI(t, map[string]string{podFQDN(1): infoReplica(podFQDN(0))})

	conf, logs := env.run(t, testValkeyForInitScript(2), "test-0")

	assert.NotContains(t, conf, "replicaof ",
		"pod-0 must not be configured as a replica of itself")
	assert.Contains(t, logs, "using ordinal-based config")
}

// A stale known-master entry (the recorded pod is gone or no longer a master)
// must degrade to the previous behavior instead of chaining replicas.
func TestInitScript_StaleKnownMasterFallsBackToOrdinal(t *testing.T) {
	env := newInitScriptEnv(t, podFQDN(2))
	env.stubValkeyCLI(t, map[string]string{
		podFQDN(1): infoReplica(podFQDN(0)),
		// pod-2 is absent from the map: unreachable.
	})

	conf, logs := env.run(t, testValkeyForInitScript(3), "test-0")

	assert.NotContains(t, conf, "replicaof ")
	assert.Contains(t, logs, "reports role=unreachable, ignoring")
	assert.Contains(t, logs, "using ordinal-based config")
}

// The known-master step must not shadow Phase 1: an established master with
// connected replicas still wins, even when the replica config names another pod.
func TestInitScript_EstablishedMasterWinsOverKnownMaster(t *testing.T) {
	env := newInitScriptEnv(t, podFQDN(2))
	env.stubValkeyCLI(t, map[string]string{
		podFQDN(1): infoMaster(1),
		podFQDN(2): infoMaster(0),
	})

	conf, logs := env.run(t, testValkeyForInitScript(3), "test-0")

	assert.Contains(t, conf, "replicaof "+podFQDN(1))
	assert.NotContains(t, conf, "replicaof "+podFQDN(2))
	assert.Contains(t, logs, "Discovered existing master")
}

// A restarting replica follows the known master too — during the failover window
// the promoted pod is the correct target for every pod, not just pod-0.
func TestInitScript_ReplicaFollowsKnownMaster(t *testing.T) {
	env := newInitScriptEnv(t, podFQDN(1))
	env.stubValkeyCLI(t, map[string]string{
		podFQDN(0): infoReplica(podFQDN(1)),
		podFQDN(1): infoMaster(0),
	})

	conf, _ := env.run(t, testValkeyForInitScript(3), "test-2")

	assert.Contains(t, conf, "replicaof "+podFQDN(1))
}

// The announce directives are appended on every branch — they carry the pod FQDN
// into replication info and must survive the new known-master path.
func TestInitScript_AnnouncesOwnHostname(t *testing.T) {
	env := newInitScriptEnv(t, podFQDN(1))
	env.stubValkeyCLI(t, map[string]string{podFQDN(1): infoMaster(0)})

	conf, _ := env.run(t, testValkeyForInitScript(2), "test-0")

	assert.Contains(t, conf, "replica-announce-ip "+podFQDN(0))
	assert.Contains(t, conf, fmt.Sprintf("replica-announce-port %d", ValkeyPort))
}
