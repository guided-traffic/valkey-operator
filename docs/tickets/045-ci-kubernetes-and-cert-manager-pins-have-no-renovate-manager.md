---
id: T45
title: Renovate coverage gaps - the CI Kubernetes and cert-manager pins, ENVTEST_VERSION, the dead release-template.hbs
state: filed
severity: low         # the E2E environment ages; nothing shipped depends on it
security: none
urgency: now          # rule 1 since the 2026-09-27 appendix: D4 and F4 are false statements in tracked files (was later, rule 4)
effort: S             # XS until the 2026-09-27 enrichment: two managers, the override rule, D4, F4, docs and an ADR 0017 line at close
blocked-by: decision  # manage them, and with or without automerge
filed-from: the documentation restructure of 2026-09-27 (DEVELOPER.md toolchain table)
opened: 2026-09-27
decided:              # not recorded - no decision yet
done:                 # not recorded - not done
---

Filed on 2026-09-27 from a finding of the documentation restructure, whose toolchain table asked
whether any Renovate manager moves the Kind and Helm version inputs of the workflows. Verification
answered that question with **yes** and found two other pins that nothing moves. Everything below
was read on 2026-09-27; no Renovate run was observed or started. *(Re-read 2026-09-27 at
`4a7543e` on `chore/maintenance-2026-09-27`: every line cited below still holds, except where a
correction says otherwise.)*

## Fact

**Verified:**

- **The Kind and Helm inputs are maintained — the premise as handed over was wrong.** The
  `with: version:` inputs of `helm/kind-action` ([`release.yml:157`](../../.github/workflows/release.yml))
  and of `azure/setup-helm` ([`release.yml:316`](../../.github/workflows/release.yml),
  [`build.yml:153`](../../.github/workflows/build.yml)) were last changed by the Renovate bot:
  `795d332` (2026-09-16, "update dependency kubernetes-sigs/kind to v0.33.0 (#219)", `v0.30.0` →
  `v0.33.0`) and `d3e731f` (2026-09-10, "update dependency helm to v4.3.0 (#215)", both files,
  `v4.2.4` → `v4.3.0`). No custom manager in [`renovate.json`](../../renovate.json) names them, so
  a built-in manager extracts them; which one is not recorded in the commits. *(2026-09-27: the
  commit type points at one. Both are minor updates committed as `chore(deps)`.
  `renovate.json:274-283` sets `fix` for every minor, patch, digest and pin update, and the only
  later rule that sets `chore` is the github-actions rule at `:284-290`. So the github-actions
  manager extracts them. This is inferred from the rule order and the commit type, not read from
  a Renovate log.)*
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
- *(Added 2026-09-27, upstream release lists read on the GitHub API and Docker Hub.)*
  - The Kind `v0.33.0` release notes (published 2026-08-26) list the node images built for it:
    `v1.37.0` (the default), `v1.36.4`, `v1.35.8` and `v1.34.11`. `v1.33.4`, the image every CI
    leg runs, is not in that list. The newest `kindest/node` tag on the 1.33 line is `v1.33.12`
    (2026-06-02).
  - cert-manager's newest releases are `v1.21.2` (2026-09-11) and `v1.20.4` (2026-09-16), so the
    pinned `v1.17.2` is four minors behind.
- *(Added 2026-09-27.)* **A new custom manager automerges unless a later rule says otherwise.**
  [`renovate.json:225-236`](../../renovate.json) is described as "Makefile-pinned Go tools", but
  it matches `matchManagers: ["custom.regex"]` with no file or dependency filter. It sets
  `automerge: true` for minor, patch and digest updates of every custom regex manager: today the
  Go-version managers (`:304-357`) and the Valkey test images (`:358-368`) as well as the
  Makefile. Majors are already manual (`:237-250`).
- *(Added 2026-09-27.)* **A new custom manager also inherits the `fix` commit type, and on this
  repository `fix` is a release.** `:274-283` sets `semanticCommitType: "fix"` for minor, patch,
  digest and pin updates of every manager, and only github-actions is switched back to `chore`
  (`:284-290`). [`.releaserc.json`](../../.releaserc.json) runs the conventionalcommits
  commit-analyzer. Measured: `21c0b85`, a Makefile-tool bump
  (`fix(deps): update module …golangci-lint… to v2.13.2`), is tagged `v1.12.1`, so a bump of a
  CI-only tool cut an operator release with nothing shipped changed. ~~Not every such commit is
  tagged (`235fb45`, the next golangci-lint bump, is not), and why was not checked.~~
  *(corrected 2026-09-27, review: checked. `git log v1.12.6..v1.12.7` holds `235fb45` and
  `9925539`, another `fix(deps)` bump; the `v1.12.7` tag sits on `9925539`. So `235fb45` was
  released too, together with the next commit, and `v1.12.0..v1.12.1` holds `21c0b85` alone.
  Every such bump lands in a release; some share one.)*

**Not verified:**

- That no built-in Renovate manager extracts `KUBERNETES_VERSION` or the cert-manager URL. The
  seven months without a bot commit suggest it, and the literals' shapes do not match any
  custom manager; only a Renovate run with debug logging, or its dependency dashboard, would
  settle it.
- ~~Whether Kubernetes 1.33 and cert-manager 1.17 are still inside their upstream support windows
  on 2026-09-27. Not checked against either project.~~ *(corrected 2026-09-27: the release lists
  were read, see Verified. Kubernetes 1.37 and cert-manager 1.21 are out, and 1.33 and 1.17 are
  four minors behind each. That both lie outside the support windows (Kubernetes maintains the
  three newest minors; cert-manager supports the newest two) is the upstream policy as
  remembered, not re-read today.)*
- ~~Whether a newer `kindest/node` tag works with Kind `v0.33.0`. Kind publishes the node images it
  supports per release; not read.~~ *(corrected 2026-09-27: read. Kind `v0.33.0` lists
  `v1.34.11` to `v1.37.0`, see Verified. Whether CI has run green on the pair Kind `v0.33.0` and
  `v1.33.4` since `795d332` was not checked against a run log.)*
- *(Added 2026-09-27.)* That a packageRule placed after `:225-236` overrides its `automerge`.
  Later rules override earlier ones per Renovate's documented merge order, and the existing
  major rule at `:237-250` relies on that. No Renovate run was made.

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
CLAUDE.md) already differ. *(Added 2026-09-27.)* CI already runs a node image that is not among
the ones its own Kind release was built for (Fact). With either manager option, the first PR is
a jump rather than a step: Renovate proposes the newest tag, `v1.37.0`, which changes every E2E
leg at once, including what ADR 0017's user-namespace probe finds. Expect manual work on that
first PR.

## Options

Decision 1 of this ticket. The numbers name the decisions, not the order they are taken in;
the order is in the work list below. Two Fact bullets added on 2026-09-27 shape every option:
whatever option is taken, the rule at `renovate.json:225-236` applies to a new manager, and so
does the `fix` commit type from `:274-283`.

- **A — custom regex managers, manual review (recommended).** One for `KUBERNETES_VERSION` against the
  `kindest/node` Docker tags, one for the cert-manager URL against its GitHub releases (matching
  both files, so the two copies move together), both with `automerge: false`. They stop aging
  silently, and a human sees each change, which matters here because a Kubernetes bump changes
  the environment every E2E result is measured on and may change what the user-namespace probe
  of ADR 0017 finds. *(Added 2026-09-27.)* Cost S:
  - the two managers. `KUBERNETES_VERSION` carries no `v` while the tags do, so the manager
    needs `extractVersionTemplate: "^v(?<version>.+)$"`;
  - **one packageRule placed after `:225-236`** *(corrected 2026-09-27, review:
    placed after `:274-283` too, so in practice at the end of `packageRules`, after `:284-290`.
    Later rules win per option, so a rule between `:236` and `:274` would get its `chore`
    overwritten by the `fix` rule at `:274-283`)* that sets `automerge: false` and
    `semanticCommitType: "chore"` for these two dependency names. Without it, A quietly becomes
    B (Fact). With the commit type left at `fix`, every merge cuts an operator release (Fact).

  Nothing shipped rolls; each PR runs the full E2E matrix on the CI runners. It leaves
  the docs that name the versions in the present tense, and those go stale at the first merge
  unless they point at the variable instead: `DEVELOPER.md:143`, `:213`, `:268-269`,
  [`testing.md:164`](../developer/testing.md) and
  [`workload-pod-posture.md:185`](../security/workload-pod-posture.md). The dated measurements
  in ADR 0017 (`:135`, `:270`) and `pod_hardening_test.go:113` stay as history.
- **B — the same managers with automerge** like the other minor and patch updates. Cheaper to
  run, but a node-image change would land on the strength of one green E2E matrix.
  *(Added 2026-09-27.)* ~~B needs no packageRule at all, because `:225-236` already does it.~~
  *(corrected 2026-09-27, review: B needs no automerge rule, because `:225-236` already sets it,
  but it does need a packageRule for the commit type, placed after `:274-283`.)* It
  still needs the `chore` override, or each automerged bump cuts a release.
- **C — declare them hand-maintained.** A comment at each literal saying so, and a periodic
  manual bump. Costs nothing now and relies on memory. *(Added 2026-09-27.)* Memory has
  measurably failed. After seven months of hand-kept pins, `v1.33.4` is eight patches behind its
  own line and outside Kind `v0.33.0`'s list, and cert-manager is four minors behind.
- **D — drop `node_image` and use Kind's default image** *(added 2026-09-27)*. The Kind input
  already moves by the github-actions manager, which automerges even majors (`:166-175`). So the
  Kubernetes version under test would change with no reviewer and no literal in the repository
  naming it, and kubectl (`release.yml:151`) would need a pin of its own. It is B with less
  visibility.

A wins because it is the only option where the E2E environment changes through a reviewed PR,
and ADR 0017 records its measurements against that environment (`:135`, `:270`). B and D put that
change on automerge, and C has already failed. **Shared with
[T54](054-renovate-does-not-track-the-default-exporter-image.md):** the `automerge: false` rule
can be one rule that lists both tickets' dependency names. The commit type cannot be shared.
T45's pins are CI-only and must not cut a release (`chore`), while T54's exporter image ships
with the operator, so a bump there is a release (`fix`, the `:274-283` default). Take this
decision together with T54's.

## Work list (2026-09-27)

**XS items that need no decision: none.** Every item waits on one of three decisions. Each
false statement that makes this ticket `now` (the D4 comment, the `renovate.json:29`
description) is removed differently depending on the option taken.

**Decision order.** F4 and D4 first, in either order. Both are independent of the managers and
of every other ticket, each is a one-liner, and each removes a rule-1 statement, so the ticket
stops being `now` once both land. The managers (decision 1) come next, taken together with T54's
option, because the two tickets share the override rule. Recommended: decision 1 **A**,
D4 **B**, F4 **A**.

**Once decided:**

1. The two managers and the override rule (decision 1), then the docs listed under option A.
2. D4: the one-line change of the chosen option, then `make test-integration` (option B)
   *(and `make test-unit`, review 2026-09-27)*.
3. F4: delete the file and its manager, and fix `renovate.json:29`, `DEVELOPER.md:90` and `:265`.

**Close (ADR 0034):** "CI-environment pins are Renovate-managed with manual review and a
`chore` commit type" is a durable rule. It becomes a new decision in
[ADR 0017](../adr/0017-test-and-ci-policy.md) (D56; D55 is the last today), with a Status line
and its row in `docs/adr/README.md`. The toolchain table in `DEVELOPER.md:260-270` is the
contributor-facing home. `git grep -nE 'T45\b|045-'` outside `docs/tickets/` is empty today.

## Decision

None yet.

## Verification

- A Renovate dry run (or the dependency dashboard) lists `kindest/node` and
  `cert-manager/cert-manager` as dependencies of `release.yml`, and the cert-manager one of the
  `Makefile` as well.
- Revert check: with the new manager's regex broken in a scratch copy of `renovate.json`, the
  same dry run no longer lists them.
- *(Added 2026-09-27.)* The config validates with `renovate-config-validator`, and the resolved
  config of a dry run shows `automerge: false` and `semanticCommitType: chore` for both new
  dependencies. Without a dry run, the rule order in `renovate.json` is the only evidence, and
  that is a read, not a measurement.

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
- *(Added 2026-09-27, read through the Go module proxy with `go list -m`.)* The module
  `sigs.k8s.io/controller-runtime/tools/setup-envtest` publishes **semver tags**: `v0.24.0`
  (2026-04-30), `v0.24.1`, `v0.25.0` and `v0.25.1` (2026-09-13). `release-0.19` resolves to
  `v0.0.0-20250308055145-5fe7bb3edc86`, a commit of 2025-03-08. The existing regex
  (`v[\d.]+`, `renovate.json:300`) would match `v0.25.1` as written. The controller-tools envtest
  index (`envtest-releases.yaml` at HEAD) lists `v1.29.0`, the assets `ENVTEST_K8S_VERSION`
  asks for.
- *(Added 2026-09-27.)* `Integration Tests (envtest)` is among the required contexts that ADR
  0017 D47 enumerates (`:535`), and it is ~~the~~ *(corrected 2026-09-27, review: a)* job that
  runs setup-envtest. *(Review, 2026-09-27: `Unit Tests` runs it too, through
  `make test-unit-coverage` ([`release.yml:610`](../../.github/workflows/release.yml),
  `Makefile:118`); `make test-unit` and `make test` call it as well (`Makefile:112`, `:103`).
  Both jobs are required contexts at ADR 0017 `:535`.)* Whether branch
  protection still carries it was not read, since that is repository state.

**Not verified:** whether a Renovate go datasource could resolve a `release-0.x` branch ref at all
(no Renovate run), and whether `setup-envtest` from `release-0.19` differs from a current one for
the `ENVTEST_K8S_VERSION = 1.29.0` assets this repo downloads - the integration tier runs green
on it (ADR 0017), which says it works, not that it is current. *(2026-09-27: also not verified
is whether `setup-envtest v0.25.1 use 1.29.0` installs the assets and the integration tier stays
green on it. Option B needs exactly that run. Nor was it verified that Renovate's go datasource
lists this submodule's tags. The Go proxy lists them, and the datasource reads the proxy by
default, but no Renovate run confirmed it.)*

**Options:** (decision 2 of this ticket, independent of the others)

- **A - declare it hand-maintained.** ~~(best)~~ Replace the `# renovate:` comment with one that says
  the value is a controller-runtime release branch bumped by hand together with the
  controller-runtime minor. A branch ref has no version order a manager could move, so a
  manager would be a promise the tooling cannot keep either. *(2026-09-27: the premise holds
  for the branch, but the module has tagged versions since `v0.24.0`, so the pin does not have
  to be a branch.)* Cost: one comment line. It leaves the tool on a 2025-03 commit until someone
  remembers.
- **B - pin a tagged setup-envtest version** ~~(a `v0.x.y` pseudo-version)~~ so the existing regex
  matches. ~~Automatable, but ties the envtest tool to a pseudo-version nobody reads.~~
  *(corrected 2026-09-27: the tags are real semver releases, so no pseudo-version is involved.)*
  **(recommended)** `ENVTEST_VERSION ?= v0.25.1`, the same minor as controller-runtime in
  `go.mod:16`. The comment at `Makefile:49` becomes true without any `renovate.json` change, and
  the tool joins the six other Makefile tools under `:225-236`: minor and patch automerge, gated
  by the required integration job that consumes it *(and the required unit job, review
  2026-09-27)*. Cost: one token plus one
  `make test-integration` run *(corrected 2026-09-27, review: and one `make test-unit` run,
  since the unit targets use the same `$(ENVTEST)`)*. The tool path (`Makefile:38`) carries the version, so the bump
  installs itself (ADR 0017 D49). What it leaves: each bump is committed as `fix(deps)` and can
  cut a release, as the six other tools already do (Fact under decision 1). The tool and
  controller-runtime can also drift a minor apart, because the `k8s-go-modules` group
  (`:251-262`) covers only `gomod`.

B over A: A's premise is refuted for the tagged module. B makes the stale comment true instead
of removing the automation, puts the tool under the regime the repository already uses for its
other Makefile tools, and the job that would catch a bad bump is the one that runs the tool.

**Verification:** either the manager's regex, run over the Makefile, lists `setup-envtest` among
its matches, or no `# renovate:` comment stands above `ENVTEST_VERSION`.

### F4 — `.github/release-template.hbs` is loaded by nothing and kept current by Renovate

**Verified:**

- [`.releaserc.json`](../../.releaserc.json) configures
  `@semantic-release/release-notes-generator` with `preset: conventionalcommits` only - no
  `writerOpts`, no `template` - so no release note has ever been rendered from
  [`.github/release-template.hbs`](../../.github/release-template.hbs).
- The only references to the file are in `renovate.json`: the Go group description (line 29)
  and a custom manager for its badge (lines 332-335). *(corrected 2026-09-27, at `4a7543e`: the
  restructure added two more, [`DEVELOPER.md:90`](../../DEVELOPER.md) (layout tree) and `:265`
  (toolchain table). Both describe the file truthfully as unreferenced and lagging. Ticket 054
  names it too, at `:31`. The manager spans `renovate.json:331-344`.)* The badge still reads `go-1.26`
  ([`release-template.hbs:8`](../../.github/release-template.hbs)) while `go.mod` declares
  `go 1.27.1` - the automation that keeps a dead file current does not even do that.
- *(Added 2026-09-27.)* `git log -S'go-1.26-blue'` finds only `0a90483` (2026-02-17). Renovate
  has never moved the badge, although the Go version it names has moved (`edcbf39`, golang
  v1.27.1).

**Not verified:** why Renovate never moved the badge (a guess: its versioning rejects the
two-part `1.26`).

**Options:** (decision 3 of this ticket, independent of the others)

- **A - delete the file and its manager (best, recommended).** Release notes come from the preset, which
  [ADR 0017](../adr/0017-test-and-ci-policy.md) D46 pins and `make test-release-tooling`
  renders; a second, unused template only creates a location that looks authoritative.
  *(Added 2026-09-27.)* Cost XS: one file, `renovate.json:331-344`, the `:29` description,
  `DEVELOPER.md:90` and `:265`. It changes no release note, because nothing loads the file.
- **B - wire it in** through `writerOpts` in `.releaserc.json`. Changes every release note and
  needs the release-tooling check extended to render it. *(Added 2026-09-27, read, not
  rendered:)* it also needs more than a path. The template calls a `gte` helper and reads
  `process.env.TEST_COVERAGE` (`release-template.hbs:5-6`). Handlebars has no built-in `gte`,
  and no step in `.releaserc.json` or the release job passes a `process` object into the writer
  context. `git grep` finds no `TEST_COVERAGE`, `writerOpts` or `registerHelper` outside the
  template itself. So the template as written would have to be rewritten first.

A over B: B is a product change to every release note, bought to keep a file whose one dynamic
value has never been set up. A removes a lagging file and changes no output.

**Verification:** ~~`git grep -n release-template` is empty~~ *(corrected 2026-09-27:
`git grep -n release-template -- ':!docs/tickets'` is empty, which needs the `DEVELOPER.md:90`
and `:265` edits)*, `renovate.json` names no such file, and `make test-release-tooling` is green.

## History

- 2026-09-27: adversarial review of the enrichment. Corrected in place: the override rule of
  option A (and B's commit-type rule) must sit after `:274-283`, not only after `:225-236`, or
  the `fix` rule overwrites `chore`; `235fb45` was released in `v1.12.7` together with
  `9925539`, so the "not tagged" note is struck; setup-envtest also runs in the required
  `Unit Tests` job, so D4 option B's proof is `make test-unit` plus `make test-integration`.
  Spot-checked and holding: `release.yml:23`, `:151`, `:157-158`, `:316`, `:331`, `build.yml:153`,
  `Makefile:5`, `:38`, `:49-50`, `:164-166`, `:200`, `go.mod:16`, `renovate.json:29`, `:166-175`,
  `:225-250`, `:274-290`, `:300`, `:331-357`, ADR 0017 `:135`, `:270`, `:535` (and D55 as the last
  decision), ADR 0031 `:195`, the `DEVELOPER.md` lines and `release-template.hbs:5-8`. No XS item
  without a decision, confirmed. Frontmatter values unchanged; the recommendations stand.
- 2026-09-27: enriched. Re-verified at `4a7543e`. Recorded that `renovate.json:225-236`
  automerges every custom regex manager and that `:274-283` makes each bump a `fix` (and
  `21c0b85` was tagged `v1.12.1`). Both upstream release lists read. D4 option B corrected, since
  setup-envtest has semver tags since `v0.24.0`, and it is now the recommended option. F4's
  reference list corrected, option D added, and decisions ordered (F4 and D4 first, then the
  managers together with T54). No XS item needs no decision. Effort XS -> S: two managers, an
  override rule with a commit type, D4's integration run, F4, docs, and ADR 0017 D56 at close.
  Urgency stays `now` (rule 1, the D4 comment and `renovate.json:29`).
- 2026-09-27 — D4 (`ENVTEST_VERSION`) and F4 (`release-template.hbs`) appended as members of the
  same family, moved from a ticket that had bundled them with unrelated work; both re-verified the
  same day, and D4's original verification ("count 6, not 5") corrected, because govulncheck is
  now pinned and the count is 6 without envtest. Title widened; urgency later -> now by rule 1
  (false statements in tracked files).
- 2026-09-27 — filed from the documentation restructure. The Kind and Helm half of the finding as
  handed over was refuted by the bot commits `795d332` and `d3e731f`; the two unmanaged pins were
  found while verifying.
