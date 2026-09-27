---
id: T43
title: lint and vet skip every build-tagged test file, and two of those files no CI job compiles
state: filed
severity: low         # no production code is affected; test code rots unseen
security: none
urgency: later        # rule 4: a cheap known fix; no false statement, severity below medium
effort: S             # the configuration is small; what the first run finds is not known
blocked-by: decision  # which option, below
filed-from: the documentation restructure of 2026-09-27 (DEVELOPER.md build notes)
opened: 2026-09-27
decided:              # not recorded - no decision yet
done:                 # not recorded - not done
---

Filed on 2026-09-27 from a finding of the documentation restructure: the page that documents the
Makefile noted that the lint targets pass no build tags. Everything below was read in the working
tree of `feat/rootless` on 2026-09-27 (`HEAD` = `f5c6886` plus the uncommitted restructure, which
touches neither the Makefile nor `.golangci.yml` nor the workflows). No make target was run for
this file. *(Re-verified 2026-09-27 on `chore/maintenance-2026-09-27` at `4a7543e`: every fact
below holds; moved line numbers are corrected in place.)*

## Fact

**Verified:**

- **Every Go file in three test directories carries a build tag.** `test/integration/`: 15
  files, all `//go:build integration`. `test/e2e/`: 25 files, 23 `//go:build e2e`, one
  `//go:build e2e && e2e_helm` ([`migrate_e2e_test.go`](../../test/e2e/migrate_e2e_test.go)), one
  `//go:build e2e && fleetupgrade` ([`fleet_upgrade_test.go`](../../test/e2e/fleet_upgrade_test.go)).
  `test/imagetools/`: 2 files, both `//go:build imagetools`. `test/testimages/` carries no tag.
  All 42 tagged files are `_test.go` files.
- **The static-analysis targets pass no tag.** ~~[`Makefile:76-78`](../../Makefile)~~ *(corrected
  2026-09-27: [`Makefile:77-78`](../../Makefile))* `vet` is
  `go vet ./...`; ~~[`Makefile:80-85`](../../Makefile)~~ *(corrected 2026-09-27:
  [`Makefile:81-85`](../../Makefile))* `lint` is `go vet ./...`, `gofmt -l .` and
  `golangci-lint run --timeout=5m`. [`.golangci.yml`](../../.golangci.yml) sets no
  `run.build-tags` (read in full; its `run:` block, lines 3-4, holds only
  `allow-parallel-runners`). A file whose build constraint is not satisfied is not part of
  the package `go vet` or golangci-lint loads, so none of the 42 files is vetted with the full
  analyzer set or linted. `golangci-lint run -h` (v2.14.0, `bin/`) lists `--tests` as default
  true, so test files as such are in scope; the tag is what excludes these.
- **CI compiles them only in their own test jobs.** The integration job runs
  `make test-integration-coverage` ([`release.yml:778`](../../.github/workflows/release.yml),
  `-tags=integration`), the E2E legs run `make test-e2e`
  (~~[`release.yml:414`](../../.github/workflows/release.yml)~~ *(corrected 2026-09-27:
  [`release.yml:415`](../../.github/workflows/release.yml))*, `-tags=e2e`), the image-tools job
  runs `make test-image-tools` ([`release.yml:658`](../../.github/workflows/release.yml),
  `-tags=imagetools`). Compiling a test binary runs only a subset of vet: `go help test` (Go
  1.27.1) names it as "atomic, bools, buildtag, directive, errorsas, ifaceassert, nilfunc,
  printf, stdversion, stringintconv, and tests".
- **Two tagged files are compiled by no CI job at all.** No workflow names `test-e2e-helm`,
  `test-e2e-fleet-upgrade`, `e2e_helm` or `fleetupgrade` (grep over `.github/workflows/`,
  2026-09-27). `migrate_e2e_test.go` and `fleet_upgrade_test.go` therefore reach `main` without
  ever being built; a helper renamed in the rest of `test/e2e/` breaks them with CI green. The
  fleet-upgrade e2e is the test the rootless release was validated with (ADR 0032, ADR 0033).
- **The `gofmt` line of `lint` cannot fail.** `gofmt -l` lists unformatted files and exits 0:
  piping an unformatted snippet into `gofmt -l` (Go 1.27.1) printed `<standard input>` and
  exit status 0. So `make lint` reports an unformatted file in its log and still passes on that
  line. `gofmt` walks files, not packages, so this line does see the tagged files; it just does
  not fail on them.
- **`gosec` and `vuln` are not part of this gap, contrary to the finding as first stated.**
  Passing the tags to them would not bring these directories into scope: every tagged file is a
  `_test.go` file, and both tools skip test files by default. `bin/govulncheck -h` (v1.8.0) says
  of `-test` "analyze test files (only valid for source mode, default false)"; `bin/gosec -h`
  lists `-tests` "Scan tests files" as a boolean flag with no default shown.
- *(Added 2026-09-27.)* **The two uncompiled files have their own targets, which only a person
  runs:** `test-e2e-fleet-upgrade` ([`Makefile:170-173`](../../Makefile), `-tags=e2e,fleetupgrade`)
  and `test-e2e-helm` ([`Makefile:176-178`](../../Makefile), `-tags=e2e,e2e_helm`). The `Code
  Linting` job is [`release.yml:499-520`](../../.github/workflows/release.yml) and runs only
  `make lint` (line 520).
- *(Added 2026-09-27.)* **`vet` is a prerequisite of three other targets:** `test`
  ([`Makefile:102`](../../Makefile)), `build` (`Makefile:323`) and `run` (`Makefile:327`); through
  `build`, also `test-e2e-helm` (`Makefile:176`) *(precised 2026-09-27 by the review: and `all`,
  `Makefile:61`)*. `lint` is not a prerequisite of anything. This
  decides where the tags may go without side effects (Options, A).
- *(Added 2026-09-27.)* `gofmt -l .` at `4a7543e` prints nothing and exits 0, so making that line
  fail turns nothing red today.

**Not verified:**

- What golangci-lint or the full `go vet` would report over the tagged files today. Neither was
  run with tags, so the size of the first cleanup is unknown.
- Whether golangci-lint v2.14.0's `gofmt`/`goimports` formatters, enabled in `.golangci.yml`,
  fail `golangci-lint run` on an unformatted file of a loaded package. Read from the
  configuration only.
- The `gosec` default: the `bin/gosec` that answered is a local build reporting `Version: dev`,
  not the pinned `v2.29.0` the Makefile installs under `bin/gosec-v2.29.0`.
- Whether the two extra-tag files compile together with the rest of `test/e2e/` under one tag
  set (`e2e,e2e_helm,fleetupgrade`). A crude grep of their top-level declarations found no shared
  name; nothing was compiled.
- That `Code Linting` is a required status check on `main`. [ADR 0017](../adr/0017-test-and-ci-policy.md)
  line 534 lists it; GitHub was not queried.
- *(Added 2026-09-27.)* Whether golangci-lint's `govet` linter, at its v2.14.0 defaults, runs the
  same analyzer set as `go vet`; if it does, the `go vet` line of `lint` needs no tags of its own.

## Impact

Contributors editing the e2e, integration or image-tools tiers get no `staticcheck`, `errcheck`
(it is excluded for `_test.go` anyway), `unused`, `revive` or full `vet` feedback from CI, so
dead helpers and vet-class defects in roughly forty test files accumulate unseen. The larger
consequence is the two uncompiled files: the migration e2e and the fleet-upgrade e2e can be
broken by any change to shared e2e helpers, and the break surfaces only when somebody runs them
by hand — for the fleet-upgrade e2e, typically right before a release. No production code and no
security property is affected.

## Options

- **A — tags in the lint job (best).** Set `run.build-tags: [integration, e2e, e2e_helm,
  fleetupgrade, imagetools]` in `.golangci.yml`, pass the same list to `go vet` in `vet` and
  `lint`, and make the `gofmt` line fail (`test -z "$$(gofmt -l .)"`). It closes both halves in
  the job that already runs on every pull request, adds no CI job, and so needs no change to
  branch protection (CLAUDE.md: a new gating job must be added to the required checks in the same
  change). Cost: the first run may surface findings across the tagged files, and
  `test/e2e/` is then loaded with both extra tags at once, which nothing has compiled yet.
- **B — a compile-only step.** `go test -tags=integration,e2e,e2e_helm,fleetupgrade,imagetools
  -run '^$' ./test/...` in an existing job. Closes the uncompiled-files half, runs only the vet
  subset, lints nothing. Cheaper to land, leaves the lint half open.
- **C — leave it and keep it documented.** `DEVELOPER.md` and `docs/developer/testing.md` already
  state that the tagged tree is not linted. Costs nothing and closes nothing.

A is marked because it is the only option that removes both halves, and it does so inside a job
that is already required according to ADR 0017.

*(Refined 2026-09-27, one decision, still A vs B vs C.)* Within A, put the tags into
`.golangci.yml` and the `go vet` line of `lint` ([`Makefile:83`](../../Makefile)) **only, not into
the `vet` target** ([`Makefile:77-78`](../../Makefile)). `vet` is a prerequisite of `test`, `build`
and `run` (Fact), so a vet finding in an e2e helper would otherwise stop `make build` and, through
it, `make test-e2e-helm`. Nothing depends on `lint`, and CI runs it in the `Code Linting` job.
**A (recommended)**, in that form: B is a strict subset of it, because golangci-lint type-checks
every package it loads, so the tags alone compile the two orphaned files. C leaves the
fleet-upgrade e2e, which validated the rootless release (ADR 0032), breakable with CI green.
Whether the `$(GOFMT) -l .` line ([`Makefile:84`](../../Makefile)) is made to fail or dropped
is a measurement, not a second decision. If golangci-lint's `gofmt` formatter (`.golangci.yml:61-64`)
fails `golangci-lint run` once the tagged files are loaded, the line is redundant and goes.
Otherwise it becomes `test -z "$$($(GOFMT) -l .)"`.

## Decision

None yet.

## Work list

No item here is both XS and free of the decision above; every item waits on it.

1. *(waits on the decision)* `run.build-tags: [integration, e2e, e2e_helm, fleetupgrade,
   imagetools]` in `.golangci.yml`; the same list on the `go vet` line of `lint`
   (`Makefile:83`), not on `vet`.
2. *(waits on the decision)* Measure the `gofmt` formatter, then drop or gate `Makefile:84`
   (Options).
3. *(waits on the decision)* Run `make lint` and fix what the tagged files surface. The size of
   this step is unknown, and it decides whether effort stays S.
4. *(waits on the decision)* Land together with or after T44, whose false comment at
   `test/e2e/migrate_e2e_test.go:147` sits in a file this change starts linting.
5. Close ([ADR 0034](../adr/0034-tickets-are-work-lists-that-get-archived.md)): the rule "lint
   covers every build-tagged tier" into [ADR 0017](../adr/0017-test-and-ci-policy.md) with its
   index row, rewrite the `DEVELOPER.md:230` bullet and the `docs/developer/testing.md:189`
   bullet, fix the relative links to this file ~~in tickets 044 (line 45) and 047 (line 82)~~
   *(corrected 2026-09-27 by the review: incomplete and already moved. At `4a7543e` plus the
   uncommitted ticket edits, tickets 033 (lines 223, 418), 034 (lines 440, 571), 044 (line 49)
   and 047 (line 82) link to it, and so may an embargoed local ticket; list them at close time
   with `grep -rn '043-lint' docs/tickets`)*, `git grep` `043` and `T43`, then move to `archive/`.

## Verification

- `make lint` fails in a scratch copy where a tagged file carries an unused function (proves the
  tags reach golangci-lint), and in a scratch copy where a tagged file is unformatted (proves the
  `gofmt` line gates). Revert both and `make lint` is green.
- ~~`make vet`~~ *(corrected 2026-09-27: `make lint`, because the refined A leaves `vet`
  untagged)* fails in a scratch copy where `fleet_upgrade_test.go` calls a helper that does not
  exist, and passes on the real tree.
- *(Added 2026-09-27.)* `make build` still passes in that same scratch copy, which proves the
  tags stayed out of `vet`.
- The `Code Linting` job is green in CI on the fix commit, with the tagged files in its output
  or its package count.

## History

- 2026-09-27: reviewed - spot-checked the new line numbers at `4a7543e` (all hold). Corrected the
  close step's list of tickets that link here (it named two of four, one at a stale line) and
  added `all` to the targets that depend on `vet`. Frontmatter and recommendation unchanged.
- 2026-09-27: enriched - re-verified at `4a7543e` and corrected three moved line numbers in
  place. Recorded that `vet` feeds `build`, `run` and `test`, and refined A to tag `lint` only.
  Added a work list (no decision-free XS item) and the close steps. Frontmatter unchanged.
- 2026-09-27 — filed from the documentation restructure. The finding as handed over also named
  `gosec` and `vuln`; verification narrowed it to lint and vet, because both of those tools skip
  test files by default and every tagged file is a test file. The uncompiled-files half and the
  non-failing `gofmt` line were found while verifying.
