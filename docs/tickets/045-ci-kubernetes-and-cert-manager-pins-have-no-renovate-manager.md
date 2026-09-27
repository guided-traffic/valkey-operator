---
id: T45
title: Renovate coverage gaps - the CI Kubernetes and cert-manager pins, ENVTEST_VERSION, the dead release-template.hbs, the Valkey test-image manager that never runs, and releases cut by CI-only bumps
state: analysed       # every decision costed, one option recommended each
severity: low         # only the E2E environment ages, nothing shipped depends on it; medium if the fleet runs a Kubernetes minor newer than 1.33
security: none
urgency: now          # rule 1: false statements in tracked files (Makefile:49, renovate.json:29, DEVELOPER.md:268, the Valkey test-image statements)
effort: S             # each change is XS; together two managers, a few rules, one Makefile line, docs and one ADR decision
blocked-by: decision  # Q1, Q2, Q3; Q1 together with T54's decision 1
filed-from: the documentation restructure (DEVELOPER.md toolchain table)
opened: 2026-09-27
decided:
done:
---

# T45 - Renovate coverage gaps in CI pins, envtest, the release template, the test images and commit types

## Current state

Renovate runs self-hosted ([`renovate.yml:42-51`](../../.github/workflows/renovate.yml), 44.115.10;
Dependency Dashboard issue #229). The Kind and Helm `version:` inputs are moved by the built-in
github-actions manager and are fine. The rest is not:

- **Kubernetes in CI.** `KUBERNETES_VERSION: '1.33.4'` ([`release.yml:23`](../../.github/workflows/release.yml))
  selects the node image of every E2E leg (`:158`). The github-actions manager skips it as
  `contains-variable`, so nothing moves it. 1.33 is end of life; Kind `v0.33.0` builds `v1.34.11`
  to `v1.37.0` (default) and asks for the `@sha256` digest. CI is green on the pair anyway.
- **kubectl in CI.** The `azure/setup-kubectl` step (`release.yml:148-151`) is shadowed:
  `helm/kind-action@v1.15.0` (`:155`) puts its default kubectl `v1.37.0` first on `PATH`, and the
  e2e tests shell out to it ([`e2e_test.go:252`](../../test/e2e/e2e_test.go)). That is four minors
  from the 1.33.4 server; the skew policy supports one. Nothing before the Kind step uses kubectl.
  kind-action caches kubectl keyed on the Kind version only, so a persistent tool cache ignores a
  changed `kubectl_version`.
- **cert-manager.** `v1.17.2` is applied from a release URL at [`Makefile:200`](../../Makefile) and
  [`release.yml:331`](../../.github/workflows/release.yml); no manager matches. 1.17 is end of life;
  supported are 1.21 (declares Kubernetes 1.33-1.36) and 1.20 (1.32-1.35). **No released
  cert-manager declares 1.37**, the tag a manager proposes first (Renovate offers only the newest
  minor unless `separateMultipleMinor` is set).
- **ENVTEST_VERSION.** [`Makefile:49-50`](../../Makefile) carries a `# renovate:` comment above
  `ENVTEST_VERSION ?= release-0.19`, a branch (pseudo-version of 2025-03-08). The Makefile regex
  (`renovate.json:300`) captures only `v[\d.]+`, so it never matches. setup-envtest is tagged at
  every controller-runtime release since `v0.24.0`, same commit; [`go.mod:16`](../../go.mod)
  requires controller-runtime `v0.25.1` (no `replace`). The tool feeds the required `Unit Tests`
  and `Integration Tests (envtest)` jobs. Out of scope: `ENVTEST_K8S_VERSION = 1.29.0`
  (`Makefile:5`, the declared Kubernetes floor) and `E2E_UPGRADE_FROM` stay hand-pinned.
- **Dead release template.** [`.github/release-template.hbs`](../../.github/release-template.hbs) is
  loaded by nothing ([`.releaserc.json`](../../.releaserc.json) uses the preset without
  `writerOpts`). Only `renovate.json:29`, its manager (`renovate.json:331-344`) and
  [`DEVELOPER.md:90`](../../DEVELOPER.md), `:265` name it. Its `go-1.26` badge never moves because
  Renovate's semver rejects the two-part `1.26`.
- **Valkey test images.** The manager at [`renovate.json:358-368`](../../renovate.json) targets
  [`test/testimages/images.go`](../../test/testimages/images.go) (9.1.1, 8.1.9) and never extracts:
  `config:recommended` includes `:ignoreModulesAndTests`, whose `ignorePaths` contain `**/test/**`.
  9.1.2 and 8.1.10 exist; no PR proposes them. Stating the opposite: ADR 0017 `:31` and D43
  (`:729`), [`DEVELOPER.md:270`](../../DEVELOPER.md), [`testing.md:116-117`](../developer/testing.md),
  [`images.go:31`](../../test/testimages/images.go), CLAUDE.md. `ignorePaths` is not mergeable, so
  a repository value replaces the preset's; no other file under `test/` matches a built-in manager.
- **Double load.** [`renovate.yml:46`](../../.github/workflows/renovate.yml) passes
  `configurationFile: renovate.json` as global config and `RENOVATE_REQUIRE_CONFIG` (`:51`) loads it
  again, so every custom manager runs twice (the dashboard lists each regex file twice). No key in
  it is global-only.
- **CI-only bumps are releases.** `renovate.json:225-236` automerges minor, patch and digest updates
  of every custom regex manager. `:274-283` makes them `fix`; only github-actions is `chore`
  (`:284-290`). Every `fix` is a patch release (a golangci-lint bump alone cut `v1.12.1`), and under
  ADR 0005 D11 a release rolls every multi-replica data StatefulSet once deployed (not on kustomize
  or a floating Helm `image.tag`). golangci-lint, gocyclo, gosec, govulncheck and kustomize never
  ship; controller-gen stamps the shipped CRD
  ([`crd.yaml:8`](../../deploy/helm/valkey-operator/templates/crd.yaml)); the npm manager covers
  only release tooling.

**Impact:** every E2E result is measured on an end-of-life Kubernetes and cert-manager, with a
kubectl outside the skew policy, and on Valkey test images the docs wrongly call maintained. A
Kubernetes change after 1.33 reaches CI only by a hand edit. CI-only bumps cut releases that roll
the fleet for nothing shipped.

## Required changes

### Independent of the open questions

1. **kubectl:** add `kubectl_version: v${{ env.KUBERNETES_VERSION }}` to the kind-action step
   (`release.yml:155-162`, or E's literal under Q1 E) and delete `:148-151`. Proof: the job log
   shows "Installing kubectl..." and a `Client Version` equal to the node version.
2. **Test images:** top-level `ignorePaths` = the preset list without `**/test/**`
   (`**/node_modules/**`, `**/bower_components/**`, `**/vendor/**`, `**/examples/**`,
   `**/__tests__/**`, `**/tests/**`, `**/__fixtures__/**`). With or after Q3, or 9.1.2 and 8.1.10
   land as `fix` releases. Proof: the dashboard lists `images.go` with two `valkey/valkey`.
3. **Template:** delete `.github/release-template.hbs` and `renovate.json:331-344`; fix
   `renovate.json:29`, `DEVELOPER.md:90`, `:265`. Proof: `git grep -n release-template --
   ':!docs/tickets'` empty, `make test-release-tooling` green.
4. **Double load:** one debug run (`workflow_dispatch`, `logLevel: debug`) to confirm, then delete
   `renovate.yml:46`. Proof: the dashboard lists each regex file once.
5. **DEVELOPER.md toolchain table:** `:268` (drop "and kubectl"; the github-actions manager moves
   Kind and Helm), `:266` and `:273` (regex and npm manager are now observed on the dashboard).

### Depends on the answers

- **Q1:** managers and rules as chosen, the rule as the **last** packageRule (after `:284-290`).
  Point `DEVELOPER.md:143`, `:213`, `:268-269`, [`testing.md:164`](../developer/testing.md) and
  [`workload-pod-posture.md:185`](../security/workload-pod-posture.md) at the variable instead of a
  version. Proof: dashboard lists `kindest/node` and `cert-manager/cert-manager` as dependencies
  (not only the files); a broken regex in a scratch copy removes them from a dry run;
  `renovate-config-validator` passes and the resolved config shows `automerge: false`, `chore`.
- **Q2:** the `Makefile:50` line, then `make test-unit` and `make test-integration`.
- **Q3:** the `chore` rules, in one `renovate.json` edit with Q1 and T54's decision 1. Proof: the
  next CI-only bump is `chore(deps)` and cuts no tag.
- **Close:** new decision D56 in [ADR 0017](../adr/0017-test-and-ci-policy.md) (Status, index row):
  CI-environment pins are Renovate-managed with manual review and `chore`, with Q1's review
  criterion and Q3's rule, naming test-only Go modules left at `fix`.

## Open questions

### Q1: How are the CI Kubernetes version and the cert-manager pin kept current?

A new custom manager inherits automerge and `fix`. The catch-up is cert-manager first (1.21 runs on
1.33.4), then Kubernetes inside its range; each node image also swaps the containerd ADR 0017
`:270` measures against.

- **A - two custom regex managers, manual review (recommended).** One on `KUBERNETES_VERSION`
  (`docker`, `kindest/node`, `extractVersionTemplate: "^v(?<version>.+)$"`), one on the cert-manager
  URL in `Makefile` and workflows (one PR moves both). Last rule: `automerge: false`, `chore`,
  `separateMultipleMinor` for `kindest/node`. Criterion: CI tests the newest Kubernetes minor inside
  the newest supported cert-manager's range, cert-manager first. Cost S.
- **B - the same managers, automerged.** One rule line less; a green matrix merges `v1.37.0`, which
  no released cert-manager declares.
- **E - literals in the kind-action inputs** (`node_image` with digest, `kubectl_version`), read by
  the built-in extractor as `chore`; needs an `automerge: false` rule, a `groupName` and the
  cert-manager manager anyway. Cost S. Native digest pin, but the version lives in two literals.

A keeps one literal for node image, kubectl and docs, and every move reaches a reviewer, which the
first ones need. Pick E if the digest pin weighs more than the single literal.

**Answer:** _open_

### Q2: How is `ENVTEST_VERSION` kept current?

- **B - pin a tag, `v0.25.1`.** The existing manager matches. Cost XS. Each bump is its own `fix`
  release unless Q3 lists it, and tool and controller-runtime can drift a minor.
- **C - derive it from `go.mod`, delete the comment (recommended).**
  `$(shell awk '$$1=="sigs.k8s.io/controller-runtime"{print $$2}' go.mod)` with Kubebuilder's
  empty-result guard. Cost XS. Moves inside the controller-runtime PR; a missing tag turns it red.

C removes the separate releases and the drift; its only failure mode is a red PR.

**Answer:** _open_

### Q3: Which Renovate bumps get the `chore` type so they cut no release?

`chore` defers a change to the next release. Shipped Go modules, the Go toolchain and T54's exporter
stay `fix` in both options.

- **b - an explicit list (recommended).** Last rule: golangci-lint, gocyclo, gosec, govulncheck,
  kustomize (full Makefile `depName`s), `kindest/node`, `cert-manager/cert-manager`, setup-envtest
  if Q2 is B, `valkey/valkey` scoped to `test/testimages/images.go`; a second rule for npm.
  controller-gen stays `fix`. A forgotten CI-only tool causes an extra release.
- **c - by file** (`Makefile`, `images.go`, npm), controller-gen included. No list, but a
  controller-gen bump changes the shipped CRD without its own release.

b fails toward an extra release, c toward a silent non-release of a shipped change.

**Answer:** _open_

## Not verified

- The production fleet's Kubernetes minor; it decides whether severity becomes medium.
- Whether all self-hosted runners are ephemeral (kubectl caching); change 1's log check settles it.
- The causes of the `images.go` skip and the doubled counts; change 4's debug run settles both.
- That setup-envtest `v0.25.1` installs the 1.29.0 assets with both tiers green; Q2's test run.

## Related

- T54: shares the `automerge: false` rule; its exporter ships and stays out of the `chore` rules.
- T44: retiring the kustomize path removes the kustomize pin and drops it from Q3's list.
- T30: embargoed security finding.
