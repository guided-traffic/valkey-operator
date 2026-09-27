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
this file.

## Fact

**Verified:**

- **Every Go file in three test directories carries a build tag.** `test/integration/`: 15
  files, all `//go:build integration`. `test/e2e/`: 25 files, 23 `//go:build e2e`, one
  `//go:build e2e && e2e_helm` ([`migrate_e2e_test.go`](../../test/e2e/migrate_e2e_test.go)), one
  `//go:build e2e && fleetupgrade` ([`fleet_upgrade_test.go`](../../test/e2e/fleet_upgrade_test.go)).
  `test/imagetools/`: 2 files, both `//go:build imagetools`. `test/testimages/` carries no tag.
  All 42 tagged files are `_test.go` files.
- **The static-analysis targets pass no tag.** [`Makefile:76-78`](../../Makefile) `vet` is
  `go vet ./...`; [`Makefile:80-85`](../../Makefile) `lint` is `go vet ./...`, `gofmt -l .` and
  `golangci-lint run --timeout=5m`. [`.golangci.yml`](../../.golangci.yml) sets no
  `run.build-tags` (read in full). A file whose build constraint is not satisfied is not part of
  the package `go vet` or golangci-lint loads, so none of the 42 files is vetted with the full
  analyzer set or linted. `golangci-lint run -h` (v2.14.0, `bin/`) lists `--tests` as default
  true, so test files as such are in scope; the tag is what excludes these.
- **CI compiles them only in their own test jobs.** The integration job runs
  `make test-integration-coverage` ([`release.yml:778`](../../.github/workflows/release.yml),
  `-tags=integration`), the E2E legs run `make test-e2e`
  ([`release.yml:414`](../../.github/workflows/release.yml), `-tags=e2e`), the image-tools job
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

## Decision

None yet.

## Verification

- `make lint` fails in a scratch copy where a tagged file carries an unused function (proves the
  tags reach golangci-lint), and in a scratch copy where a tagged file is unformatted (proves the
  `gofmt` line gates). Revert both and `make lint` is green.
- `make vet` fails in a scratch copy where `fleet_upgrade_test.go` calls a helper that does not
  exist, and passes on the real tree.
- The `Code Linting` job is green in CI on the fix commit, with the tagged files in its output
  or its package count.

## History

- 2026-09-27 — filed from the documentation restructure. The finding as handed over also named
  `gosec` and `vuln`; verification narrowed it to lint and vet, because both of those tools skip
  test files by default and every tagged file is a test file. The uncompiled-files half and the
  non-failing `gofmt` line were found while verifying.
