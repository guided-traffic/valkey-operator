package builder

import (
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
)

// RequiredImageTools is checked against the real image by test/imagetools. That
// check is only worth running while the list still describes what the operator
// actually executes, which is what these tests are for: one direction catches a
// script that grew a dependency nobody declared, the other catches a declaration
// nothing uses any more.

// shellCommandCatalog is the vocabulary the drift guard recognises. It is
// deliberately wider than RequiredImageTools -- every entry beyond the required
// set is a tool a future script could reach for, and the guard exists to notice
// exactly that.
//
// Its limit is worth stating plainly: a script that calls something outside this
// catalog passes unseen. The catalog covers the coreutils and busybox applets a
// container script realistically uses, not every binary that could exist.
var shellCommandCatalog = []string{
	"awk", "base64", "basename", "cat", "chmod", "chown", "cp", "cut", "date",
	"dirname", "echo", "env", "expr", "find", "grep", "head", "hostname", "id",
	"ln", "ls", "mkdir", "mktemp", "mv", "nc", "nslookup", "printf", "ps", "pwd",
	"readlink", "rev", "rm", "sed", "seq", "sh", "sleep", "sort", "stat", "tail",
	"tee", "test", "timeout", "touch", "tr", "uniq", "wc", "wget", "xargs",
	"valkey-cli", "valkey-server", "valkey-sentinel", "curl", "dig", "getent",
}

// valkeyImageScripts collects every exec command the builder places into a
// container that runs the Valkey image: container commands, init container
// scripts, probe execs and lifecycle hooks.
//
// Filtering by image is the point. The sidecar runs the operator image and the
// exporter runs the exporter image, so their commands say nothing about what the
// Valkey image must provide -- and scanning them would put `manager` on the list.
func valkeyImageScripts(v *vkov1.Valkey) []string {
	var scripts []string

	collect := func(containers []corev1.Container) {
		for _, c := range containers {
			if c.Image != v.Spec.Image {
				continue
			}
			scripts = append(scripts, strings.Join(c.Command, " "))
			scripts = append(scripts, strings.Join(c.Args, " "))
			if c.ReadinessProbe != nil && c.ReadinessProbe.Exec != nil {
				scripts = append(scripts, strings.Join(c.ReadinessProbe.Exec.Command, " "))
			}
			if c.LivenessProbe != nil && c.LivenessProbe.Exec != nil {
				scripts = append(scripts, strings.Join(c.LivenessProbe.Exec.Command, " "))
			}
			if c.Lifecycle != nil && c.Lifecycle.PreStop != nil && c.Lifecycle.PreStop.Exec != nil {
				scripts = append(scripts, strings.Join(c.Lifecycle.PreStop.Exec.Command, " "))
			}
		}
	}

	data := BuildStatefulSet(v, testOperatorImage)
	collect(data.Spec.Template.Spec.InitContainers)
	collect(data.Spec.Template.Spec.Containers)

	if v.IsSentinelEnabled() {
		sentinel := BuildSentinelStatefulSet(v)
		collect(sentinel.Spec.Template.Spec.InitContainers)
		collect(sentinel.Spec.Template.Spec.Containers)
	}

	return scripts
}

// imageRequirementFixtures spans the shapes that switch generated shell on and
// off: auth wraps the container command in `sh -c`, Sentinel and multi-replica
// each bring their own init container, and the drain preStop is non-Sentinel only.
func imageRequirementFixtures() []*vkov1.Valkey {
	return []*vkov1.Valkey{
		newTestValkey("standalone"),
		newTestValkey("multi", func(v *vkov1.Valkey) { v.Spec.Replicas = 3 }),
		newTestValkey("sentinel", func(v *vkov1.Valkey) {
			v.Spec.Replicas = 3
			v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
		}),
		newTestValkey("full", func(v *vkov1.Valkey) {
			v.Spec.Replicas = 3
			v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
			v.Spec.TLS = &vkov1.TLSSpec{Enabled: true}
			v.Spec.Auth = &vkov1.AuthSpec{SecretName: "creds", SecretPasswordKey: "password"}
		}),
	}
}

// commandsUsedBy returns the catalog entries that appear at a command position in
// the script: at the start, after a pipe, after a separator, or inside $( ).
func commandsUsedBy(script string) map[string]bool {
	used := map[string]bool{}
	for _, tool := range shellCommandCatalog {
		pattern := regexp.MustCompile(`(^|[|;&(\n]|\$\(|&&|\|\|)\s*` + regexp.QuoteMeta(tool) + `\s`)
		if pattern.MatchString(script) {
			used[tool] = true
		}
	}
	return used
}

// TestRequiredImageTools_CoversTheGeneratedScripts is the guard that keeps the
// list from going stale. A script that grows a dependency on a tool nobody
// declared would otherwise pass every tier here and fail in a user's cluster on an
// image that happens not to ship it.
func TestRequiredImageTools_CoversTheGeneratedScripts(t *testing.T) {
	declared := map[string]bool{}
	for _, tool := range RequiredImageTools() {
		declared[tool] = true
	}

	undeclared := map[string]string{}
	for _, v := range imageRequirementFixtures() {
		for _, script := range valkeyImageScripts(v) {
			for tool := range commandsUsedBy(script) {
				if !declared[tool] {
					undeclared[tool] = v.Name
				}
			}
		}
	}

	assert.Empty(t, undeclared,
		"a generated script uses a tool RequiredImageTools does not name, so "+
			"`make test-image-tools` would never check the Valkey image for it")
}

// TestRequiredImageTools_AreAllUsed is the same guard from the other side. A tool
// that no script reaches for any more turns the image check into a stricter
// contract than the operator needs, which is how an image gets rejected for a
// reason that stopped being true.
func TestRequiredImageTools_AreAllUsed(t *testing.T) {
	used := map[string]bool{}
	for _, v := range imageRequirementFixtures() {
		for _, script := range valkeyImageScripts(v) {
			for tool := range commandsUsedBy(script) {
				used[tool] = true
			}
		}
	}

	var unused []string
	for _, tool := range RequiredImageTools() {
		if !used[tool] {
			unused = append(unused, tool)
		}
	}
	sort.Strings(unused)

	assert.Empty(t, unused, "declared but no longer used by any generated script")
}

// TestRequiredImageTools_NamesTheBinariesTheOperatorCannotDoWithout pins the
// entries whose absence is not a degradation but a broken cluster, so a cleanup
// that trims the list cannot quietly drop one.
func TestRequiredImageTools_NamesTheBinariesTheOperatorCannotDoWithout(t *testing.T) {
	tools := RequiredImageTools()
	for _, essential := range []string{"sh", "valkey-server", "valkey-cli", "timeout", "sleep"} {
		assert.Contains(t, tools, essential)
	}
	require.NotEmpty(t, tools)
}
