package testimages

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The pins are read by the E2E suite, which cannot assert on itself, so what the
// selector does with each input is pinned here instead.

func TestDefault_UsesValkey9WhenNothingIsSelected(t *testing.T) {
	assert.Equal(t, Valkey9, Default(),
		"the suites run against the current Valkey 9 release unless told otherwise")
}

func TestDefault_SelectsThePinnedLine(t *testing.T) {
	for line, want := range map[string]string{"": Valkey9, "9": Valkey9, "8": Valkey8} {
		t.Setenv(EnvValkeyLine, line)
		assert.Equal(t, want, Default(), "line %q", line)
	}
}

// TestDefault_ExplicitImageWinsOverTheLine covers the developer knob: trying an
// image that is not a pinned line at all must not be overridden by a line that
// happens to be set in the environment.
func TestDefault_ExplicitImageWinsOverTheLine(t *testing.T) {
	t.Setenv(EnvValkeyLine, "8")
	t.Setenv(EnvValkeyImage, "valkey/valkey:9.1.1-alpine")

	assert.Equal(t, "valkey/valkey:9.1.1-alpine", Default())
}

// TestDefault_UnknownLineIsLoud is the point of the panic. A mistyped selector in
// the CI matrix must not quietly run the Valkey 8 leg against Valkey 9 and report
// a pass -- that is a leg that tests nothing while looking like it tested
// everything.
func TestDefault_UnknownLineIsLoud(t *testing.T) {
	t.Setenv(EnvValkeyLine, "8.1")

	assert.PanicsWithValue(t,
		`E2E_VALKEY_LINE="8.1" names no pinned Valkey line; use "8", "9", or set E2E_VALKEY_IMAGE to a full image`,
		func() { _ = Default() })
}

// TestUpgradePair_CrossesTheMajorAndIgnoresTheLine pins both halves of the pair
// decision: it upgrades from the 8 line to the 9 line, and it does not follow the
// leg. The Valkey 8 leg runs the same upgrade as the Valkey 9 leg because what is
// under test is the operator, not the leg.
func TestUpgradePair_CrossesTheMajorAndIgnoresTheLine(t *testing.T) {
	t.Setenv(EnvValkeyLine, "8")

	require.NotEqual(t, UpgradeFrom, UpgradeTo,
		"an upgrade to the image the cluster already runs triggers no rolling update")
	assert.Equal(t, Valkey8, UpgradeFrom)
	assert.Equal(t, Valkey9, UpgradeTo)
}
