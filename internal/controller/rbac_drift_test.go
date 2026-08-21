package controller

// RBAC drift guard.
//
// The kubebuilder markers in this package are the single source of truth for
// the operator's permissions. `make manifests` renders them into
// config/rbac/role.yaml, but the Helm chart - the canonical install path -
// carries a hand-maintained ClusterRole that nothing compares against the
// generated one. Both known occurrences of that drift were silent until they
// hit a cluster: NA12 (Events discarded for every Helm install) and the
// missing `delete` on secrets, which wedged the reconciler on the unified-TLS
// migration.
//
// The check is containment, not equality: every (group, resource, verb) triple
// the generated role grants must also be granted by the chart. Chart-only
// extras stay legal - the chart adds coordination.k8s.io/leases for leader
// election, which has no marker because no controller reconciles Leases.

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/util/yaml"
)

const (
	generatedRolePath = "config/rbac/role.yaml"
	chartRolePath     = "deploy/helm/valkey-operator/templates/clusterrole.yaml"
)

// rbacTriple is one atomic permission: a single verb on a single resource in a
// single API group.
type rbacTriple struct {
	group    string
	resource string
	verb     string
}

func (t rbacTriple) String() string {
	group := t.group
	if group == "" {
		group = `""` // the core API group
	}
	return fmt.Sprintf("%s/%s:%s", group, t.resource, t.verb)
}

// rbacRulesDoc is the sliver of a ClusterRole this guard parses. The Helm file
// is a Go template and cannot be unmarshalled as a whole, so only the rules
// block is fed in (see readRulesBlock).
type rbacRulesDoc struct {
	Rules []rbacv1.PolicyRule `json:"rules"`
}

// repoRoot resolves the repository root from this file's own location, so the
// guard does not depend on the working directory `go test` was started in.
func repoRoot(t *testing.T) string {
	t.Helper()

	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller(0) failed; cannot locate the repository root")

	// <root>/internal/controller/rbac_drift_test.go
	root := filepath.Join(filepath.Dir(thisFile), "..", "..")
	_, err := os.Stat(filepath.Join(root, "go.mod"))
	require.NoErrorf(t, err, "resolved repository root %q has no go.mod; has this test file moved?", root)

	return root
}

// readRulesBlock returns the top-level `rules:` block of a manifest.
//
// The chart ClusterRole is a Go template whose metadata carries `{{ include
// ... }}` actions, which no YAML parser accepts. Its rules block, however, is
// plain YAML - and that is the only part this guard compares. If a Go-template
// action ever appears inside the block, the test fails right here instead of
// silently skipping the comparison.
func readRulesBlock(t *testing.T, path string) []byte {
	t.Helper()

	data, err := os.ReadFile(path) //nolint:gosec // fixed, repo-relative manifest paths
	require.NoErrorf(t, err, "cannot read %s - the RBAC drift guard needs both manifests; update the path constants if the file moved", path)

	lines := strings.Split(string(data), "\n")
	start := -1
	for i, line := range lines {
		if line == "rules:" {
			start = i
			break
		}
	}
	require.GreaterOrEqualf(t, start, 0, "no top-level `rules:` key in %s", path)

	// The block runs until the next top-level key. List items are either
	// flush left (generated role) or indented (chart), and comments and blank
	// lines belong to the block as well.
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		line := lines[i]
		if line == "" || strings.HasPrefix(line, " ") || strings.HasPrefix(line, "-") || strings.HasPrefix(line, "#") {
			continue
		}
		end = i
		break
	}

	block := strings.Join(lines[start:end], "\n")
	require.Falsef(t, strings.Contains(block, "{{"),
		"the rules block of %s contains a Go-template action; this guard parses it as plain YAML and can no longer verify it", path)

	return []byte(block)
}

// parsePolicyRules parses the rules block of a ClusterRole manifest.
func parsePolicyRules(t *testing.T, path string) []rbacv1.PolicyRule {
	t.Helper()

	var doc rbacRulesDoc
	require.NoErrorf(t, yaml.UnmarshalStrict(readRulesBlock(t, path), &doc), "parse rules block of %s", path)
	require.NotEmptyf(t, doc.Rules, "no rules parsed from %s", path)

	return doc.Rules
}

// expandRules flattens PolicyRules into (group, resource, verb) triples.
//
// A rule listing several groups and several resources grants their full cross
// product, which is exactly why a rule-by-rule comparison is useless here: the
// generated role's combined `"" + events.k8s.io` events rule has no 1:1
// counterpart on either side.
//
// Rules restricted by resourceNames or nonResourceURLs grant less than the
// bare triple suggests and are dropped - on the chart side counting them would
// hide a real gap. Wildcards (`*`) are not expanded either; neither manifest
// uses them, and a wildcard would produce a loud false failure rather than a
// silent pass.
func expandRules(rules []rbacv1.PolicyRule) map[rbacTriple]struct{} {
	triples := make(map[rbacTriple]struct{})
	for _, rule := range rules {
		if len(rule.ResourceNames) > 0 || len(rule.NonResourceURLs) > 0 {
			continue
		}
		for _, group := range rule.APIGroups {
			for _, resource := range rule.Resources {
				for _, verb := range rule.Verbs {
					triples[rbacTriple{group: group, resource: resource, verb: verb}] = struct{}{}
				}
			}
		}
	}
	return triples
}

func TestHelmClusterRoleCoversGeneratedRole(t *testing.T) {
	root := repoRoot(t)

	generated := parsePolicyRules(t, filepath.Join(root, generatedRolePath))
	chart := parsePolicyRules(t, filepath.Join(root, chartRolePath))

	// expandRules drops restricted rules. On the generated side that would
	// silently shrink the required set, so reject them outright instead.
	for i, rule := range generated {
		require.Emptyf(t, rule.ResourceNames,
			"%s rule %d is restricted by resourceNames; this guard cannot verify coverage of it", generatedRolePath, i)
		require.Emptyf(t, rule.NonResourceURLs,
			"%s rule %d carries nonResourceURLs; this guard cannot verify coverage of it", generatedRolePath, i)
	}

	required := expandRules(generated)
	covered := expandRules(chart)

	missing := make([]string, 0)
	for triple := range required {
		if _, ok := covered[triple]; !ok {
			missing = append(missing, triple.String())
		}
	}
	sort.Strings(missing)

	require.Emptyf(t, missing,
		"the Helm ClusterRole (%s) does not grant everything the generated role (%s) does.\n"+
			"Missing group/resource:verb triples:\n  %s\n"+
			"Add them to the chart template. If the generated role itself is stale, run `make manifests` first.",
		chartRolePath, generatedRolePath, strings.Join(missing, "\n  "))
}
