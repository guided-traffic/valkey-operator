---
id: T45
title: Renovate coverage gaps - the CI Kubernetes and cert-manager pins, ENVTEST_VERSION, the dead release-template.hbs
state: filed
severity: low         # the E2E environment ages; nothing shipped depends on it
security: none
urgency: now          # rule 1 since the 2026-09-27 appendix: D4 and F4 are false statements in tracked files (was later, rule 4)
effort: XS
blocked-by: decision  # manage them, and with or without automerge
filed-from: the documentation restructure of 2026-09-27 (DEVELOPER.md toolchain table)
opened: 2026-09-27
decided:              # not recorded - no decision yet
done:                 # not recorded - not done
---

Filed on 2026-09-27 from a finding of the documentation restructure, whose toolchain table asked
whether any Renovate manager moves the Kind and Helm version inputs of the workflows. Verification
answered that question with **yes** and found two other pins that nothing moves. Everything below
was read on 2026-09-27; no Renovate run was observed or started.

## Fact

**Verified:**

- **The Kind and Helm inputs are maintained — the premise as handed over was wrong.** The
  `with: version:` inputs of `helm/kind-action` ([`release.yml:157`](../../.github/workflows/release.yml))
  and of `azure/setup-helm` ([`release.yml:316`](../../.github/workflows/release.yml),
  [`build.yml:153`](../../.github/workflows/build.yml)) were last changed by the Renovate bot:
  `795d332` (2026-09-16, "update dependency kubernetes-sigs/kind to v0.33.0 (#219)", `v0.30.0` →
  `v0.33.0`) and `d3e731f` (2026-09-10, "update dependency helm to v4.3.0 (#215)", both files,
  `v4.2.4` → `v4.3.0`). No custom manager in [`renovate.json`](../../renovate.json) names them, so
  a built-in manager extracts them; which one is not recorded in the commits.
- **`KUBERNETES_VERSION: '1.33.4'`** ([`release.yml:23`](../../.github/workflows/release.yml))
  selects the Kind node image (`kindest/node:v${{ env.KUBERNETES_VERSION }}`, `:158`) and the
  kubectl version (`:151`) of every E2E leg. It has not changed since `0a90483` (2026-02-17, by
  hand). The only workflow custom manager matches `GO_VERSION:` (`renovate.json:345-357`); the
  two consumers read the value through a `${{ env… }}` expression, which is not a version literal.
- **cert-manager `v1.17.2`** is applied from a release-asset URL in two places,
  [`Makefile:200`](../../Makefile) (`cert-manager-install`, a prerequisite of `e2e-local` and `e2e-fleet-upgrade-local`) and
  [`release.yml:331`](../../.github/workflows/release.yml) (every E2E leg). Neither line carries a
  `# renovate:` comment, and no custom manager matches a GitHub release URL. The version has not
  changed since `88b721b` and `5eacaba` (2026-02-17 and 2026-02-18, by hand). Two copies of one
  pin can also drift apart; today they agree.

**Not verified:**

- That no built-in Renovate manager extracts `KUBERNETES_VERSION` or the cert-manager URL. The
  seven months without a bot commit suggest it, and the literals' shapes do not match any
  custom manager; only a Renovate run with debug logging, or its dependency dashboard, would
  settle it.
- Whether Kubernetes 1.33 and cert-manager 1.17 are still inside their upstream support windows
  on 2026-09-27. Not checked against either project.
- Whether a newer `kindest/node` tag works with Kind `v0.33.0`. Kind publishes the node images it
  supports per release; not read.

**Deliberately not included:** `ENVTEST_K8S_VERSION = 1.29.0` ([`Makefile:5`](../../Makefile))
sits on the Kubernetes 1.29 floor the project declares
([ADR 0031](../adr/0031-a-record-the-operator-trusts-lives-in-pod-spec.md) line 195), and several
ADRs record measurements "on envtest 1.29"; moving it automatically would be wrong. Whether it is
meant to track the floor is not written down anywhere found. `E2E_UPGRADE_FROM ?= 1.10.48` is
hand-pinned on purpose (its comment at `Makefile:164-166`).

## Impact

Every E2E result — the full suites on both Valkey lines and the multi-node leg — is measured on a
Kubernetes and a cert-manager that age silently. [ADR 0017](../adr/0017-test-and-ci-policy.md)
records its CI runs on Kubernetes 1.33.4 and its user-namespace measurement on `kindest/node`
v1.33.4, so it describes that environment; a Kubernetes behaviour change after 1.33 is
not in CI until someone bumps the literal by hand, while local runs (Kubernetes 1.36.1 per
CLAUDE.md) already differ.

## Options

- **A — custom regex managers, manual review (best).** One for `KUBERNETES_VERSION` against the
  `kindest/node` Docker tags, one for the cert-manager URL against its GitHub releases (matching
  both files, so the two copies move together), both with `automerge: false`. They stop aging
  silently, and a human sees each change, which matters here because a Kubernetes bump changes
  the environment every E2E result is measured on and may change what the user-namespace probe
  of ADR 0017 finds.
- **B — the same managers with automerge** like the other minor and patch updates. Cheaper to
  run, but a node-image change would land on the strength of one green E2E matrix.
- **C — declare them hand-maintained.** A comment at each literal saying so, and a periodic
  manual bump. Costs nothing now and relies on memory.

## Decision

None yet.

## Verification

- A Renovate dry run (or the dependency dashboard) lists `kindest/node` and
  `cert-manager/cert-manager` as dependencies of `release.yml`, and the cert-manager one of the
  `Makefile` as well.
- Revert check: with the new manager's regex broken in a scratch copy of `renovate.json`, the
  same dry run no longer lists them.

## Appendix 2026-09-27: ENVTEST_VERSION and the dead release template

Two members of the same family, moved here on 2026-09-27 from a ticket that had bundled them
with unrelated work (owner decision of that day: non-security items do not stay in an embargoed
file). Re-read against the working tree of `feat/rootless` on 2026-09-27.

### D4 — the `# renovate:` comment above `ENVTEST_VERSION` advertises automation that does not run

**Verified:**

- [`Makefile:49-50`](../../Makefile): `# renovate: datasource=go
  depName=sigs.k8s.io/controller-runtime/tools/setup-envtest` above
  `ENVTEST_VERSION ?= release-0.19`, unchanged since `25483b2` (2026-02-17).
- The Makefile custom manager of [`renovate.json`](../../renovate.json) requires
  `currentValue` to match `v[\d.]+`. Run over the Makefile on 2026-09-27 it matches **6** lines -
  kustomize, controller-gen, golangci-lint, gocyclo, gosec, govulncheck - and not
  `ENVTEST_VERSION`. When the finding was first written the count was 5; govulncheck has been
  pinned since, so **a check that counts 6 would now pass with envtest still unmatched** - the
  verification below names envtest instead of a count.
- `release-0.19` is a branch of controller-runtime, not a version; [`go.mod:16`](../../go.mod)
  requires `sigs.k8s.io/controller-runtime v0.25.1`.

**Not verified:** whether a Renovate go datasource could resolve a `release-0.x` branch ref at all
(no Renovate run), and whether `setup-envtest` from `release-0.19` differs from a current one for
the `ENVTEST_K8S_VERSION = 1.29.0` assets this repo downloads - the integration tier runs green
on it (ADR 0017), which says it works, not that it is current.

**Options:**

- **A - declare it hand-maintained (best).** Replace the `# renovate:` comment with one that says
  the value is a controller-runtime release branch bumped by hand together with the
  controller-runtime minor. A branch ref has no version order a manager could move, so a
  manager would be a promise the tooling cannot keep either.
- **B - pin a tagged setup-envtest version** (a `v0.x.y` pseudo-version) so the existing regex
  matches. Automatable, but ties the envtest tool to a pseudo-version nobody reads.

**Verification:** either the manager's regex, run over the Makefile, lists `setup-envtest` among
its matches, or no `# renovate:` comment stands above `ENVTEST_VERSION`.

### F4 — `.github/release-template.hbs` is loaded by nothing and kept current by Renovate

**Verified:**

- [`.releaserc.json`](../../.releaserc.json) configures
  `@semantic-release/release-notes-generator` with `preset: conventionalcommits` only - no
  `writerOpts`, no `template` - so no release note has ever been rendered from
  [`.github/release-template.hbs`](../../.github/release-template.hbs).
- The only references to the file are in `renovate.json`: the Go group description (line 29)
  and a custom manager for its badge (lines 332-335). The badge still reads `go-1.26`
  ([`release-template.hbs:8`](../../.github/release-template.hbs)) while `go.mod` declares
  `go 1.27.1` - the automation that keeps a dead file current does not even do that.

**Not verified:** why Renovate never moved the badge (a guess: its versioning rejects the
two-part `1.26`).

**Options:**

- **A - delete the file and its manager (best).** Release notes come from the preset, which
  [ADR 0017](../adr/0017-test-and-ci-policy.md) D46 pins and `make test-release-tooling`
  renders; a second, unused template only creates a location that looks authoritative.
- **B - wire it in** through `writerOpts` in `.releaserc.json`. Changes every release note and
  needs the release-tooling check extended to render it.

**Verification:** `git grep -n release-template` is empty, `renovate.json` names no such file,
and `make test-release-tooling` is green.

## History

- 2026-09-27 — D4 (`ENVTEST_VERSION`) and F4 (`release-template.hbs`) appended as members of the
  same family, moved from a ticket that had bundled them with unrelated work; both re-verified the
  same day, and D4's original verification ("count 6, not 5") corrected, because govulncheck is
  now pinned and the count is 6 without envtest. Title widened; urgency later -> now by rule 1
  (false statements in tracked files).
- 2026-09-27 — filed from the documentation restructure. The Kind and Helm half of the finding as
  handed over was refuted by the bot commits `795d332` and `d3e731f`; the two unmanaged pins were
  found while verifying.
