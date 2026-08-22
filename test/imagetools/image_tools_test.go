//go:build imagetools

// Package imagetools checks that the Valkey images the suites run against
// actually contain what the operator executes inside them.
//
// The operator does not build this image. It consumes the upstream one and runs
// shell in it: both init containers are scripts, the container command wraps
// valkey-server in `sh -c` under auth, the probes exec valkey-cli, and the drain
// preStop hook is a shell loop. Every one of those is an assumption about a
// filesystem somebody else maintains and can change between tags -- a distroless
// or busybox rebase would take `rev`, `sed` or `timeout` with it.
//
// This tier exists because no other one can answer the question. Unit tests
// execute the generated scripts against the developer's own shell and explicitly
// stub `timeout` away because macOS does not ship it
// (internal/builder/init_script_exec_test.go); integration runs envtest, which has
// no kubelet and therefore no container at all. Only a real image can say what is
// in a real image.
//
// It needs docker and no cluster, which is why it is its own build tag and its own
// CI job: it answers in seconds and names the missing binary, instead of surfacing
// three minutes later as a rolling update that will not converge.
package imagetools

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/guided-traffic/valkey-operator/internal/builder"
	"github.com/guided-traffic/valkey-operator/test/testimages"
)

// imageProbeTimeout covers a cold pull of the image plus the probe itself.
const imageProbeTimeout = 5 * time.Minute

// runInImage executes a shell snippet inside the image and returns its output.
//
// --entrypoint sh is required: the Valkey images start valkey-server by default,
// which would ignore the script and hang.
func runInImage(t *testing.T, image, script string) string {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), imageProbeTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", "run", "--rm", "--entrypoint", "sh", image, "-c", script)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "docker run against %s failed:\n%s", image, out)
	return strings.TrimSpace(string(out))
}

// pinnedImages are the images the suites create clusters with. Both are checked,
// not just the default one: the Valkey 8 leg has to be able to rely on the same
// contract, and a leg that is skipped must not take the check with it.
func pinnedImages() map[string]string {
	return map[string]string{
		"valkey 9 (default for every suite)": testimages.Valkey9,
		"valkey 8 (second e2e leg)":          testimages.Valkey8,
	}
}

// TestImageProvidesEveryRequiredTool is the check the whole package exists for.
//
// It asks the image rather than the tag, so a rebase that drops a binary is caught
// on the Renovate PR that introduces it -- which is the only moment where the
// answer is still cheap.
func TestImageProvidesEveryRequiredTool(t *testing.T) {
	t.Parallel()

	required := builder.RequiredImageTools()
	require.NotEmpty(t, required)

	// One `command -v` per tool, reported in a single run so a missing image is one
	// pull rather than a dozen.
	script := fmt.Sprintf(
		`missing=""; for t in %s; do command -v "$t" >/dev/null 2>&1 || missing="$missing $t"; done; `+
			`[ -z "$missing" ] && echo OK || echo "MISSING:$missing"`,
		strings.Join(required, " "))

	for name, image := range pinnedImages() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			result := runInImage(t, image, script)
			assert.Equal(t, "OK", result,
				"%s does not provide every tool the generated scripts execute; the operator would "+
					"fail inside the container, where the failure is hardest to read", image)
		})
	}
}

// TestImageShellSupportsTheConstructsTheScriptsUse covers what presence cannot.
//
// A shell can exist and still not be the one the scripts were written for: the
// preStop hook counts with $((i+1)), the init containers leave nested discovery
// loops with `break 2`, and both read command substitution. Those are POSIX, and
// checking them costs one more container run.
func TestImageShellSupportsTheConstructsTheScriptsUse(t *testing.T) {
	t.Parallel()

	const script = `
i=0; i=$((i+1)); [ "$i" -eq 1 ] || { echo "FAIL:arithmetic"; exit 0; }
out=$(echo nested)
for a in 1 2; do for b in 1 2; do break 2; done; echo "FAIL:break2"; exit 0; done
[ "$out" = "nested" ] || { echo "FAIL:substitution"; exit 0; }
echo OK`

	for name, image := range pinnedImages() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, "OK", runInImage(t, image, script),
				"%s ships a shell that does not support what the generated scripts rely on", image)
		})
	}
}

// TestPinnedImagesAreDistinctMajors guards the pair the upgrade tests are built
// on. If Renovate ever moved the 8 pin onto the 9 line, every rolling update and
// upgrade test would set the image it already has, the operator would correctly do
// nothing, and the tests would pass while testing nothing at all.
func TestPinnedImagesAreDistinctMajors(t *testing.T) {
	t.Parallel()

	require.NotEqual(t, testimages.Valkey8, testimages.Valkey9,
		"an upgrade from an image to itself triggers no rolling update and proves nothing")
	assert.Contains(t, testimages.Valkey9, ":9.",
		"the default image must stay on the Valkey 9 line")
	assert.Contains(t, testimages.Valkey8, ":8.",
		"the second leg must stay on the Valkey 8 line; check the Renovate allowedVersions rule")
}
