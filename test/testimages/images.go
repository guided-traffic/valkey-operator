// Package testimages pins the Valkey container images the test suites run
// against, in one place, so Renovate can keep them current.
//
// Only the tiers that actually pull an image use these constants. Unit and
// integration tests never do -- envtest starts an API server and etcd and no
// kubelet, so nothing there runs a container -- and their "valkey/valkey:8.0"
// strings are fixtures whose only requirement is to differ from one another.
// Pinning those would churn dozens of call sites on every Renovate bump without
// changing a single byte that executes.
package testimages

import (
	"fmt"
	"os"
)

const (
	// EnvValkeyLine selects which pinned line the suite runs against: "8" or "9".
	//
	// The CI matrix carries a selector rather than an image so the pins never leave
	// this file. A copy in the workflow would be a second thing Renovate has to move
	// in lockstep, and the failure mode of it lagging is the worst kind: the leg goes
	// green while testing the line it was supposed to stop testing.
	EnvValkeyLine = "E2E_VALKEY_LINE"

	// EnvValkeyImage names an image directly, for trying something that is not a
	// pinned line at all. It wins over EnvValkeyLine.
	EnvValkeyImage = "E2E_VALKEY_IMAGE"
)

// Renovate keeps both pins on their own major line, and deliberately does not
// advance either across one: the packageRules in renovate.json cap them with
// allowedVersions, keyed on the major each currently carries. A Valkey 10 release
// is therefore a decision somebody makes here, not a pull request that arrives on
// its own -- which is what "the tests run against the current 9 and the current 8"
// has to mean if it is to stay true.
const (
	// Valkey9 is the current Valkey 9 release and the default for every suite.
	// renovate: datasource=docker depName=valkey/valkey
	Valkey9 = "valkey/valkey:9.1.1"

	// Valkey8 is the current Valkey 8 release. It is what the second e2e leg runs
	// the suite against, and the starting point of every upgrade the suite performs.
	// renovate: datasource=docker depName=valkey/valkey
	Valkey8 = "valkey/valkey:8.1.9"
)

// Default is the image a test creates its cluster with unless it has a reason to
// name a specific one.
//
// An unrecognised EnvValkeyLine panics instead of falling back to the default. A
// typo in the CI matrix would otherwise run the Valkey 8 leg against Valkey 9 and
// report it as a pass -- a leg that tests nothing while looking like it tested
// everything, which is the failure mode E2E_REQUIRE_MULTI_NODE exists to prevent
// one dimension over.
func Default() string {
	if image := os.Getenv(EnvValkeyImage); image != "" {
		return image
	}
	switch line := os.Getenv(EnvValkeyLine); line {
	case "", "9":
		return Valkey9
	case "8":
		return Valkey8
	default:
		panic(fmt.Sprintf("%s=%q names no pinned Valkey line; use \"8\", \"9\", or set %s to a full image",
			EnvValkeyLine, line, EnvValkeyImage))
	}
}

// UpgradeFrom and UpgradeTo are the pair every test needs that has to change a
// running cluster's image: a rolling update is triggered by a pod-spec change, and
// the image is the change users actually make.
//
// The pair is deliberately the two pinned lines rather than two tags of one line.
// Both ends stay current without a third pin that Renovate cannot maintain, and
// the tests double as continuous proof that the upgrade path users will take --
// the latest 8 to the latest 9 -- loses no data. The accepted cost: a genuine
// cross-major replication break upstream turns these tests red for something that
// is not this operator, which is information worth having early rather than after
// a support request.
//
// The pair does not follow EnvValkeyImage. The Valkey 8 leg runs the same upgrade
// as the Valkey 9 leg, because what is under test is the operator, not the leg.
const (
	UpgradeFrom = Valkey8
	UpgradeTo   = Valkey9
)
