package builder

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
)

// The Sentinel init container pins "sentinel myid" to the pod's ordinal so a
// replacement pod keeps its identity instead of appearing to its peers as an
// additional Sentinel (ADR 0022). Asserting on the script text would only prove a
// line exists, not that the branch producing it is taken or that the value it
// writes is a legal Sentinel id, so these tests execute the generated script and
// read the config it produced.

// sentinelInitEnv stands in for the two config mounts of the Sentinel init
// container, plus the stub binaries on PATH.
type sentinelInitEnv struct {
	readonlyConf string
	writableConf string
	binDir       string
}

// sentinelReadonlyMountPath is the mount the init container copies from. Unlike
// the writable path it is a literal in the generated script rather than a
// constant, so the test names it once here.
const sentinelReadonlyMountPath = "/etc/sentinel-readonly"

// sentinelMyIDLine matches the directive the script appends. Sentinel accepts a
// myid of exactly 40 characters and rejects anything else with "Malformed
// Sentinel id in myid option", which is a start failure, not a degradation --
// so the length is asserted, not assumed.
var sentinelMyIDLine = regexp.MustCompile(`(?m)^sentinel myid ([0-9a-f]{40})$`)

func newSentinelInitEnv(t *testing.T, v *vkov1.Valkey) *sentinelInitEnv {
	t.Helper()

	root := t.TempDir()
	e := &sentinelInitEnv{
		readonlyConf: filepath.Join(root, "readonly"),
		writableConf: filepath.Join(root, "writable"),
		binDir:       filepath.Join(root, "bin"),
	}
	for _, dir := range []string{e.readonlyConf, e.writableConf, e.binDir} {
		require.NoError(t, os.MkdirAll(dir, 0o755))
	}
	writeFile(t, filepath.Join(e.readonlyConf, SentinelConfigKey), GenerateSentinelConf(v))

	// `timeout` is not installed on every developer machine (macOS ships without
	// it), so the stub drops the duration and execs the command.
	writeFile(t, filepath.Join(e.binDir, "timeout"), "#!/bin/sh\nshift\nexec \"$@\"\n")
	require.NoError(t, os.Chmod(filepath.Join(e.binDir, "timeout"), 0o755))

	// Every Valkey peer is unreachable, which sends the script through its
	// discovery loop and out the other side with the configured master kept. What
	// is under test here is the identity step, not master validation.
	writeFile(t, filepath.Join(e.binDir, "valkey-cli"), "#!/bin/sh\nexit 1\n")
	require.NoError(t, os.Chmod(filepath.Join(e.binDir, "valkey-cli"), 0o755))

	// A portable stand-in for sha1sum: deterministic, input-dependent, and exactly
	// 40 hex characters, which is all the script requires of it. The real binary is
	// checked against the real image by `make test-image-tools` -- this tier cannot
	// answer whether it is present there, and stubbing it keeps the test from
	// depending on which coreutils the developer's machine ships.
	writeFile(t, filepath.Join(e.binDir, "sha1sum"),
		"#!/bin/sh\nn=$(cksum | cut -d' ' -f1)\nprintf '%040x  -\\n' \"$n\"\n")
	require.NoError(t, os.Chmod(filepath.Join(e.binDir, "sha1sum"), 0o755))

	return e
}

// run executes the Sentinel init script as the named pod and returns the config
// it produced in the writable mount, plus its log output. An empty hostname runs
// the script with HOSTNAME unset.
func (e *sentinelInitEnv) run(t *testing.T, v *vkov1.Valkey, hostname string) (string, string) {
	t.Helper()

	sts := BuildSentinelStatefulSet(v)
	require.NotEmpty(t, sts.Spec.Template.Spec.InitContainers)
	script := sts.Spec.Template.Spec.InitContainers[0].Command[2]

	// Redirect the absolute mount paths to the temporary directories. The
	// read-only mount must be replaced first: SentinelConfigMountPath is a prefix
	// of it, so the other order rewrites half of it and the copy reads nothing.
	script = strings.ReplaceAll(script, sentinelReadonlyMountPath, e.readonlyConf)
	script = strings.ReplaceAll(script, SentinelConfigMountPath, e.writableConf)

	cmd := exec.Command("sh", "-c", script)
	env := append(os.Environ(), "PATH="+e.binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if hostname == "" {
		// os/exec has no "remove this variable", so the inherited environment is
		// filtered instead. Without it the developer's own hostname would leak in
		// and the unset case would never be exercised.
		filtered := env[:0]
		for _, kv := range env {
			if !strings.HasPrefix(kv, "HOSTNAME=") {
				filtered = append(filtered, kv)
			}
		}
		env = append(filtered, "HOSTNAME=")
	} else {
		env = append(env, "HOSTNAME="+hostname)
	}
	cmd.Env = env

	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "sentinel init script failed: %s", string(out))

	produced, readErr := os.ReadFile(filepath.Join(e.writableConf, SentinelConfigKey))
	require.NoError(t, readErr)
	return string(produced), string(out)
}

func testValkeyForSentinelInitScript() *vkov1.Valkey {
	return newTestValkey("test", func(v *vkov1.Valkey) {
		v.Namespace = "ns"
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
	})
}

// The identity has to be written at all, and it has to be a legal one: Sentinel
// refuses to start on a myid that is not exactly 40 characters.
func TestSentinelInitScript_WritesALegalPinnedIdentity(t *testing.T) {
	v := testValkeyForSentinelInitScript()
	env := newSentinelInitEnv(t, v)

	conf, logs := env.run(t, v, "test-sentinel-0")

	matches := sentinelMyIDLine.FindAllStringSubmatch(conf, -1)
	require.Len(t, matches, 1, "exactly one myid directive belongs in the config:\n%s", conf)
	assert.Contains(t, logs, "Pinned sentinel myid "+matches[0][1])
}

// The whole point: the same pod name yields the same identity across
// replacements, so the peers take Sentinel's address-switch path instead of
// recording the replacement as an additional Sentinel.
func TestSentinelInitScript_SamePodKeepsItsIdentityAcrossReplacements(t *testing.T) {
	v := testValkeyForSentinelInitScript()

	first, _ := newSentinelInitEnv(t, v).run(t, v, "test-sentinel-1")
	second, _ := newSentinelInitEnv(t, v).run(t, v, "test-sentinel-1")

	assert.Equal(t,
		sentinelMyIDLine.FindStringSubmatch(first)[1],
		sentinelMyIDLine.FindStringSubmatch(second)[1],
		"a replacement pod of the same ordinal must not change its Sentinel identity")
}

// The converse, and the failure that would be worse than the drift this
// prevents: two Sentinels sharing one identity collapse three voters into one.
func TestSentinelInitScript_DistinctPodsGetDistinctIdentities(t *testing.T) {
	v := testValkeyForSentinelInitScript()

	seen := map[string]string{}
	for i := 0; i < 3; i++ {
		hostname := fmt.Sprintf("test-sentinel-%d", i)
		conf, _ := newSentinelInitEnv(t, v).run(t, v, hostname)
		id := sentinelMyIDLine.FindStringSubmatch(conf)[1]
		if other, clash := seen[id]; clash {
			t.Fatalf("%s and %s would share the Sentinel id %s", other, hostname, id)
		}
		seen[id] = hostname
	}
}

// Two clusters of the same name in different namespaces must not derive the same
// identities. Their Sentinels never meet -- they monitor different masters and
// discover each other through that master alone -- so this is defence in depth
// rather than a live failure, but the namespace costs nothing to include.
func TestSentinelInitScript_IdentityIsNamespaceScoped(t *testing.T) {
	here := testValkeyForSentinelInitScript()
	there := testValkeyForSentinelInitScript()
	there.Namespace = "other"

	hereConf, _ := newSentinelInitEnv(t, here).run(t, here, "test-sentinel-0")
	thereConf, _ := newSentinelInitEnv(t, there).run(t, there, "test-sentinel-0")

	assert.NotEqual(t,
		sentinelMyIDLine.FindStringSubmatch(hereConf)[1],
		sentinelMyIDLine.FindStringSubmatch(thereConf)[1])
}

// Without a hostname the script must leave the identity to Sentinel rather than
// derive one from an empty string, which every Sentinel of the cluster would
// derive identically.
func TestSentinelInitScript_NoHostnameLeavesTheIdentityToSentinel(t *testing.T) {
	v := testValkeyForSentinelInitScript()
	env := newSentinelInitEnv(t, v)

	conf, logs := env.run(t, v, "")

	assert.NotRegexp(t, sentinelMyIDLine, conf)
	assert.Contains(t, logs, "HOSTNAME is unset")
}

// The identity step is an append and nothing else: what the ConfigMap carried
// must reach Sentinel unchanged, monitor line included.
//
// The fixture carries no auth on purpose. With auth the script substitutes the
// password placeholder through `sed -i`, whose BSD form takes a mandatory backup
// suffix -- so the assertion would describe the developer's sed rather than the
// operator. Placeholder substitution is covered by
// TestBuildSentinelInitCommand_WithAuth and by e2e, where the shell is the
// image's.
func TestSentinelInitScript_OnlyAppendsToTheConfig(t *testing.T) {
	v := testValkeyForSentinelInitScript()
	env := newSentinelInitEnv(t, v)

	conf, _ := env.run(t, v, "test-sentinel-0")

	assert.True(t, strings.HasPrefix(conf, GenerateSentinelConf(v)),
		"the generated config must reach Sentinel unchanged:\n%s", conf)
	assert.Contains(t, conf, "sentinel monitor "+SentinelMonitorName(v)+" "+MasterAddress(v))
}
