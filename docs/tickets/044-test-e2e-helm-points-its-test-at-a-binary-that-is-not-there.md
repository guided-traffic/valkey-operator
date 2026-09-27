---
id: T44
title: make targets and CI settings that do not do what they say - test-e2e-helm's binary path, install/uninstall, E2E_TESTS
state: filed
severity: low         # a local-only target; no CI job runs it
security: none
urgency: now          # rule 1: a false statement in a tracked file (the comment at migrate_e2e_test.go:147)
effort: S             # XS until the 2026-09-27 appendix
filed-from: the documentation restructure of 2026-09-27 (docs/developer/testing.md)
opened: 2026-09-27
decided:              # not recorded - no decision yet
done:                 # not recorded - not done
---

Filed on 2026-09-27 from a finding of the documentation restructure. Everything below was read in
the working tree of `feat/rootless` on 2026-09-27; **the target was not run**, because it needs a
Kind cluster with the operator installed.

## Fact

**Verified (read):**

- [`Makefile:175-178`](../../Makefile): `test-e2e-helm: build`, then
  `MANAGER_BINARY=./bin/manager go test -v -tags=e2e,e2e_helm -count=1 -timeout=10m -run TestE2E_Migrate ./test/e2e/...`.
- [`Makefile:322-324`](../../Makefile): `build` writes the binary to `bin/manager` at the
  repository root.
- [`migrate_e2e_test.go`](../../test/e2e/migrate_e2e_test.go): `runMigrateBinary` (`:139`) takes
  `MANAGER_BINARY` from the environment (`:142`), falls back to `defaultManagerBinary` =
  `"../../bin/manager"` (`:35`) only when it is empty, and runs
  `exec.Command(binaryPath, "migrate")` (`:148`) without setting `cmd.Dir`.
  `TestE2E_MigrateDefaults` calls it at `:81` and fails on any error (`require.NoError`).
- `go help testflag` (Go 1.27.1): "When 'go test' runs a test binary, it does so from within the
  corresponding package's source code directory." `go doc os/exec.Command`: a name with a path
  separator is not looked up on `PATH`; `go doc os/exec.Cmd`: an empty `Dir` runs the command in
  the calling process's current directory.
- Together: the test process runs in `test/e2e/`, so `./bin/manager` names
  `test/e2e/bin/manager`, which nothing builds. The test's own default, `../../bin/manager`, is
  the path that reaches the binary the target has just built; the target overrides it with the
  wrong one.
- **The comment at `migrate_e2e_test.go:147` is false.** It reads "Build the absolute path if
  relative." and is followed directly by `exec.Command(binaryPath, "migrate")`; no code in the
  function makes the path absolute.
- No workflow runs the target (grep over `.github/workflows/`, 2026-09-27), so CI never noticed;
  the file is not even compiled there, see
  [ticket 043](043-lint-and-vet-skip-every-build-tagged-test-file.md).

**Not verified:**

- That `make test-e2e-helm` actually fails at the migrate step. It follows from the three reads
  above, but the target was not run, and a stray file at `test/e2e/bin/manager` on a developer's
  machine would make it pass.
- Whether the rest of `TestE2E_MigrateDefaults` passes once the binary is found.

## Impact

Whoever runs `make test-e2e-helm` — `DEVELOPER.md` and `docs/developer/testing.md` list it as the
way to run the Helm-migration e2e — gets a failure at the migrate step that looks like a defect
in `manager migrate` and is a path in the Makefile. The migration e2e therefore has no working
entry point, and the `migrate` subcommand has no end-to-end check that anyone can run as
documented.

## Options

- **A — an absolute path from the Makefile (best).** `MANAGER_BINARY=$(CURDIR)/bin/manager`. The
  Makefile builds the binary and names it, so one file owns both ends, and the path no longer
  depends on the package depth of `test/e2e/`.
- **B — drop the variable from the target.** The test's default already resolves. One token
  less, but it couples the target to a constant in a test file that says nothing about the
  Makefile.
- **C — resolve relative paths in the test** against the module root, as the false comment
  claims it does. Fixes every caller, costs code in a test helper that A makes unnecessary.

Whichever is taken, the comment at `:147` is corrected or removed in the same change.

## Decision

None yet.

## Verification

- `make test-e2e-helm` passes on a Kind cluster with the operator installed, with its output
  showing `Running: <absolute path>/bin/manager migrate`.
- Revert check: with `MANAGER_BINARY=./bin/manager` restored in a scratch copy, the same run fails
  at `runMigrateBinary` with a "no such file or directory" error.

## Appendix 2026-09-27: install/uninstall and E2E_TESTS

Two members of the same family, moved here on 2026-09-27 from a ticket that had bundled them
with unrelated work (owner decision of that day: non-security items do not stay in an embargoed
file). Re-read against the working tree of `feat/rootless` on 2026-09-27.

### F1 — `make install` and `make uninstall` do not touch a CRD

**Verified:**

- [`Makefile:355-360`](../../Makefile): `install: kustomize ## Install CRDs into the K8s cluster
  specified in ~/.kube/config.` runs `$(KUSTOMIZE) build config/rbac | kubectl apply -f -`;
  `uninstall` is the same with `kubectl delete`. Both help texts say CRDs; both build
  `config/rbac`.
- `config/crd/` holds only `bases/vko.gtrfc.com_valkeys.yaml` - there is no
  `config/crd/kustomization.yaml` - so no target installs or removes the CRD.
- Nothing in the repository tells anyone to run either target (`grep` over every markdown file,
  the workflows and the Makefile, 2026-09-27). `make run` (`Makefile:327`) runs the controller
  from the host and needs the CRD in the cluster; it does not need the RBAC, because it runs as
  the kubeconfig user.

**Not verified:** that the targets were ever used as they are. `git log -S` was not run for them.

**Impact:** whoever follows `make help` gets RBAC and no CRD, and a `make run` that cannot watch
a `Valkey`.

**Options:**

- **A - make the help true (best).** `install` applies `config/crd/bases/`
  (`kubectl apply --server-side -f config/crd/bases`), `uninstall` deletes it. That is what the
  help promises and what `make run` needs, and it follows the kubebuilder convention the target
  names come from.
- **B - make the help match the code.** Say RBAC. Leaves `make run` without a documented way to
  get the CRD.
- **C - delete both targets.** Nobody references them; the Helm chart installs the CRD for every
  real deployment.

**Verification:** on a scratch Kind cluster `make install` creates
`valkeys.vko.gtrfc.com` and `make uninstall` removes it.

### F3 — CI sets `E2E_TESTS: "true"` and nothing reads it

**Verified:** [`release.yml:429`](../../.github/workflows/release.yml) sets `E2E_TESTS: "true"`
on the E2E step; `git grep -n E2E_TESTS` finds that one line and nothing else (2026-09-27). The
tier is selected by the `e2e` build tag. A dead knob invites someone to "fix" a skipped suite by
touching it.

**Options:** **A - delete the line (best)**, the build tag already decides; B - make a test read
it, which would add a second switch for the same thing.

**Verification:** `git grep -n E2E_TESTS` is empty.

## History

- 2026-09-27 — F1 (`install`/`uninstall`) and F3 (`E2E_TESTS`) appended as members of the same
  family, moved from a ticket that had bundled them with unrelated work; both re-verified the same
  day. Title widened, effort XS -> S.
- 2026-09-27 — filed from the documentation restructure; the false comment at `:147` was found
  while verifying.
