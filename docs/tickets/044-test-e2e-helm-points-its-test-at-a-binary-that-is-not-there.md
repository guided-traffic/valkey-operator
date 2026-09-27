---
id: T44
title: make targets and CI settings that do not do what they say - test-e2e-helm's binary path, install/uninstall, E2E_TESTS, deploy's ClusterRole binding
state: filed
severity: low         # a local-only target; no CI job runs it
security: none
urgency: now          # rule 1: false statements in tracked files (~~the comment at migrate_e2e_test.go:147;~~ deleted 2026-09-27; the install/uninstall help at Makefile:355, :359)
effort: S             # XS until the 2026-09-27 appendix
blocked-by: decision  # F1, the binary path and E1; the path's Kind run also waits on T47's option
filed-from: the documentation restructure of 2026-09-27 (docs/developer/testing.md)
opened: 2026-09-27
decided:              # not recorded - no decision yet
done:                 # not recorded - not done
---

Filed on 2026-09-27 from a finding of the documentation restructure. Everything below was read in
the working tree of `feat/rootless` on 2026-09-27; **the target was not run**, because it needs a
Kind cluster with the operator installed. *(Re-read 2026-09-27 at `4a7543e` on
`chore/maintenance-2026-09-27`: every line cited below still holds, except where a correction
says otherwise.)*

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
  `TestE2E_MigrateDefaults` calls it at `:81` and fails on any error (`require.NoError`, `:161`).
  It logs the path it runs at `:154` (`Running: %s migrate`), which the verification below reads.
- `go help testflag` (Go 1.27.1): "When 'go test' runs a test binary, it does so from within the
  corresponding package's source code directory." `go doc os/exec.Command`: a name with a path
  separator is not looked up on `PATH`; `go doc os/exec.Cmd`: an empty `Dir` runs the command in
  the calling process's current directory.
- Together: the test process runs in `test/e2e/`, so `./bin/manager` names
  `test/e2e/bin/manager`, which nothing builds. The test's own default, `../../bin/manager`, is
  the path that reaches the binary the target has just built; the target overrides it with the
  wrong one.
- **The comment at `migrate_e2e_test.go:147` ~~is~~ *(was, until 2026-09-27)* false.** It ~~reads~~
  *(read)* "Build the absolute path if relative." and ~~is~~ *(was)* followed directly by
  `exec.Command(binaryPath, "migrate")`; no code in the function makes the path absolute.
  *(Deleted 2026-09-27, History. `exec.Command` is at `:147` since, one line up; the other
  `migrate_e2e_test.go` lines cited above it are unchanged.)*
- No workflow runs the target (grep over `.github/workflows/`, 2026-09-27), so CI never noticed;
  the file is not even compiled there, see
  [ticket 043](043-lint-and-vet-skip-every-build-tagged-test-file.md).

**Not verified:**

- That `make test-e2e-helm` actually fails at the migrate step. It follows from the three reads
  above, but the target was not run, and a stray file at `test/e2e/bin/manager` on a developer's
  machine would make it pass.
- Whether the rest of `TestE2E_MigrateDefaults` passes once the binary is found.
- *(Added 2026-09-27)* Whether the test needs a running operator, as the help text at
  `Makefile:176` says. Its body only creates, patches and reads a CR and runs `manager migrate`
  (`migrate_e2e_test.go:42-117`), and `deleteValkey` does not wait for the deletion
  ([`e2e_test.go:381-389`](../../test/e2e/e2e_test.go)). Read that way, a Kind cluster with the
  CRD would be enough. Not run.

## Impact

Whoever runs `make test-e2e-helm` — `DEVELOPER.md` and `docs/developer/testing.md` list it as the
way to run the Helm-migration e2e — gets a failure at the migrate step that looks like a defect
in `manager migrate` and is a path in the Makefile. The migration e2e therefore has no working
entry point, and the `migrate` subcommand has no end-to-end check that anyone can run as
documented.

## Options

Decision 2 of this ticket (the order is in the work list below). **It waits on
[T47](047-no-rule-decides-which-crd-field-the-migrate-hook-defaults.md):** T47's recommended
option C removes the `migrate` subcommand and `TestE2E_Migrate*`, and with them this target. The
path fix and its Kind run are wasted if C is taken there. Under T47's A or B, take this one.

- **A — an absolute path from the Makefile (recommended).** `MANAGER_BINARY=$(CURDIR)/bin/manager`. The
  Makefile builds the binary and names it, so one file owns both ends, and the path no longer
  depends on the package depth of `test/e2e/`. *(Added 2026-09-27.)* Cost: one token at
  `Makefile:178`. `$(CURDIR)` is the directory `build` writes into (`go build -o bin/manager`,
  `:324`, relative to make's working directory). `$(LOCALBIN)` is the wrong choice: it is
  `?=`-overridable (`:26`), while `build` hardcodes `bin/manager`. Breaks or rolls nothing.
  Proof needs one Kind run.
- **B — drop the variable from the target.** The test's default already resolves. One token
  less, but it couples the target to a constant in a test file that says nothing about the
  Makefile.
- **C — resolve relative paths in the test** against the module root, as the false comment
  claims it does. Fixes every caller, costs code in a test helper that A makes unnecessary.

A over B: both cost one token, but under B the target's correctness rests on
`defaultManagerBinary` (`migrate_e2e_test.go:35`), which is tied to the depth of `test/e2e/`,
while A keeps the builder and the consumer of the path in one file. A over C: C adds code whose
only purpose A removes.

Whichever is taken, the comment at `:147` is corrected or removed in the same change.
*(2026-09-27: deleting the comment is right under every option, because under C the new code
carries its own comment. So the deletion is an XS item that needs no decision; see the work
list.)*

## Work list (2026-09-27)

**XS, no decision needed. These can be done today, independently of the rest:**

1. [`migrate_e2e_test.go:147`](../../test/e2e/migrate_e2e_test.go): delete the false comment
   "Build the absolute path if relative.". **Done 2026-09-27.**
2. F3: delete `E2E_TESTS: "true"` at
   [`release.yml:429`](../../.github/workflows/release.yml), and the bullet at
   [`docs/developer/testing.md:182-183`](../developer/testing.md) that describes it. It removes
   an env line and adds no job, so branch protection is untouched. **Done 2026-09-27**, both
   halves in the same change.

Neither closes the ticket. Once both land, the only rule-1 statement left is the
`install`/`uninstall` help text (F1), so urgency stays `now` until F1 lands. *(2026-09-27: both
landed; urgency stays `now` for F1.)*

**Waits on a decision, in this order:**

1. **F1: what `install`/`uninstall` do** (options under F1 below). Take it first: it is
   independent of every other ticket, and its outcome decides the text of `DEVELOPER.md:214`
   and `:251-253`.
2. **The binary path** (Options above). It waits on T47's option. Proof needs a Kind run.
3. **E1: the `deploy` binding** (appendix below). It is independent of 1 and 2 and can be taken
   in any order.

On close, whatever was taken also changes the docs that describe today's defect:
[`testing.md:108`](../developer/testing.md) (the `MANAGER_BINARY` row) and ~~`:184-188`~~ `:182-186`
*(moved up two lines when the `E2E_TESTS` bullet was deleted, 2026-09-27)*,
[`DEVELOPER.md:189`](../../DEVELOPER.md), `:214` and `:251-253`. For E1 they are `DEVELOPER.md:65`,
`:144` and `:215` and [`package-map.md:105`](../developer/package-map.md). None of this is an
ADR decision (developer tooling), so the extraction goes to `docs/developer/` and `DEVELOPER.md`.
`git grep -nE 'T44\b|044-'` outside `docs/tickets/` is empty today.

## Decision

None yet.

## Verification

- `make test-e2e-helm` passes on a Kind cluster with the operator installed, with its output
  showing `Running: <absolute path>/bin/manager migrate`.
- Revert check: with `MANAGER_BINARY=./bin/manager` restored in a scratch copy, the same run fails
  at `runMigrateBinary` with a "no such file or directory" error.
- *(Added 2026-09-27, XS item 1:)* `grep -n 'Build the absolute path' test/e2e/migrate_e2e_test.go`
  is empty. `make lint` does not see the file (build tag, ticket 043). Deleting a comment line
  cannot change what compiles, so the grep is the whole proof. XS item 2:
  `git grep -n E2E_TESTS -- ':!docs/tickets'` is empty (F3 below). *(Run 2026-09-27 after the
  fix: both print nothing. Done.)*

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
  the kubeconfig user. *(2026-09-27, at `4a7543e`: still true, but the restructure now
  describes both targets. [`DEVELOPER.md:214`](../../DEVELOPER.md) documents the mismatch
  between help and code, and `:251-253` gives the manual `kubectl apply` of the CRD for
  `make run`. Both change when F1 lands.)*
- *(Added 2026-09-27.)* The RBAC the targets apply does not fit together either. Rendered with
  `kubectl kustomize config/rbac` (kubectl's embedded kustomize, not the pinned v5.8.1):
  - the ServiceAccount goes into namespace `system`
    ([`service_account.yaml:5`](../../config/rbac/service_account.yaml)), which nothing creates;
  - the ClusterRoleBinding refers to a ClusterRole `valkey-operator`
    ([`role_binding.yaml:7-8`](../../config/rbac/role_binding.yaml));
  - the generated ClusterRole is named `valkey-operator-role`
    ([`Makefile:404`](../../Makefile), `rbac:roleName=valkey-operator-role`).

  So today's `install` would not even produce working RBAC. It was not run.

**Not verified:** that the targets were ever used as they are. ~~`git log -S` was not run for
them.~~ *(corrected 2026-09-27: run. `git log -S'build config/rbac' -- Makefile` finds only
`25483b2` (2026-02-17, the project scaffold), so both targets are unchanged since the
scaffold.)*

**Impact:** whoever follows `make help` gets RBAC and no CRD, and a `make run` that cannot watch
a `Valkey`.

**Options:** (decision 1 of this ticket, take it first)

- **A - make the help true (recommended).** `install` applies `config/crd/bases/`
  (`kubectl apply --server-side -f config/crd/bases`), `uninstall` deletes it. That is what the
  help promises and what `make run` needs, and it follows the kubebuilder convention the target
  names come from. *(Added 2026-09-27.)* Cost: two recipe lines, two help strings,
  `DEVELOPER.md:214` and `:251-253`. `--server-side` is optional: the CRD is 48,791 bytes, far
  below the 262,144-byte annotation limit that client-side apply runs into. What it breaks, per
  case:
  - **`uninstall` becomes destructive.** It deletes the CRD, and with it every `Valkey` CR on
    whatever cluster the current kubeconfig context names, plus, through their ownerReferences,
    the objects the operator created for them. Today's `uninstall` deletes only three RBAC
    objects. The kubebuilder scaffold behaves the same way. What A leaves open is a guard that
    refuses unless the context is `kind-valkey-operator-test`.
  - **Not verified:** a later `helm install` of the chart on the same cluster. The chart ships
    the CRD under `templates/` ([`crd.yaml`](../../deploy/helm/valkey-operator/templates/crd.yaml)),
    and Helm will probably refuse to adopt a CRD it does not own, so `make uninstall` would have
    to run first. A dev-cluster nuisance; not run.
- **B - make the help match the code.** Say RBAC. Leaves `make run` without a documented way to
  get the CRD. *(Added 2026-09-27.)* It also documents RBAC that does not fit together (Fact
  above), and duplicates half of `deploy`, which applies `config/rbac` through
  [`config/default`](../../config/default/kustomization.yaml).
- **C - delete both targets.** Nobody references them; the Helm chart installs the CRD for every
  real deployment. *(Added 2026-09-27.)* Cost: the recipe, the help and `DEVELOPER.md:214`.
  `make run` keeps its manual step (`DEVELOPER.md:251-253`), which is what a developer does today
  anyway.

A over C: `make run` (`Makefile:327-328`) is the one local path that needs exactly the CRD, and
A turns the documented hand step into a target. C is the fallback if the destructive `uninstall`
is judged not worth it, and it is cheaper than a context guard. B loses to both.

**Verification:** on a scratch Kind cluster `make install` creates
`valkeys.vko.gtrfc.com` and `make uninstall` removes it.

### F3 — CI sets `E2E_TESTS: "true"` and nothing reads it

**Verified:** [`release.yml:429`](../../.github/workflows/release.yml) ~~sets~~ *(set, until the
deletion of 2026-09-27, History)* `E2E_TESTS: "true"` on the E2E step; ~~`git grep -n E2E_TESTS` finds that one line and nothing else (2026-09-27).~~
*(corrected 2026-09-27, at `4a7543e`: outside `docs/tickets/` it also finds
[`docs/developer/testing.md:182`](../developer/testing.md), a known-gap bullet the restructure
added. No Go file, script or Makefile line reads the variable. ~~The only env reads under `test/`
go through named constants such as `multiNodeRequiredEnv` and `EnvValkeyLine`.~~)*
*(corrected 2026-09-27, review: not only through constants. `os.Getenv("KUBECONFIG")`
([`e2e_test.go:71`](../../test/e2e/e2e_test.go)), `os.Getenv("MANAGER_BINARY")`
(`migrate_e2e_test.go:142`) and `envOrDefault("E2E_UPGRADE_FROM…")`
([`fleet_upgrade_test.go:160-162`](../../test/e2e/fleet_upgrade_test.go)) read literals. None of
the reads under `test/`, literal or constant, names `E2E_TESTS`, which is what matters here.)* The
tier is selected by the `e2e` build tag. A dead knob invites someone to "fix" a skipped suite by
touching it.

**Options:** **A - delete the line (best)**, the build tag already decides; B - make a test read
it, which would add a second switch for the same thing. *(2026-09-27: B is recorded but not a
live alternative, because the build tag already is the switch. Only the go-ahead is open, so
the deletion is an XS item that needs no decision; see the work list.)*

**Verification:** ~~`git grep -n E2E_TESTS` is empty.~~ *(corrected 2026-09-27:
`git grep -n E2E_TESTS -- ':!docs/tickets'` is empty. The tickets keep the name as history.)*
*(Run 2026-09-27 after the fix: empty. F3 is done.)*

### E1 — `make deploy` binds its ServiceAccount to a ClusterRole that does not exist

*(Added 2026-09-27 while enriching F1. It belongs to the same family: a Make target that does not
do what it says.)*

**Verified (rendered with `kubectl kustomize config/default`, kubectl's embedded kustomize, not
the pinned v5.8.1):** `namePrefix: valkey-operator-` renames the ClusterRole to
`valkey-operator-valkey-operator-role`, but the binding's `roleRef` stays `valkey-operator`
([`role_binding.yaml:8`](../../config/rbac/role_binding.yaml)). No ClusterRole of that name is
among the resources, so kustomize has nothing to rename. The Deployment's ServiceAccount
(`config/manager/manager.yaml:44`, renamed by the prefix) is therefore bound to nothing.
~~`git log` of `role_binding.yaml` shows `0aaa3a2` and `0a90483` (both 2026-02-17);~~
*(corrected 2026-09-27, review: `git log --follow -- config/rbac/role_binding.yaml` shows only
`0a90483` (2026-02-17), and `config/default` also dates from `0a90483`)*; the mismatch is
seven months old. No workflow or test names the target, and the only docs that do are
`DEVELOPER.md:65`, `:144` and `:215` and `package-map.md:105`.

**Not verified:** `make deploy` was not run, so whether anything else in `config/manager` keeps
the operator from starting is unknown. Not a security finding: a binding to a missing
ClusterRole grants nothing, and whoever can create a ClusterRole of that name can bind it anyway.

**Options:**

- **A - fix the `roleRef`** to `valkey-operator-role`, the name `Makefile:404` generates. Cost
  XS. It leaves the rest of the kustomize path unverified, and it keeps a second install path
  next to the chart.
- **B - retire `deploy`/`undeploy` with `config/default` and `config/manager`
  (recommended).** Keep `config/rbac/role.yaml`, which `TestHelmClusterRoleCoversGeneratedRole`
  reads ([`rbac_drift_test.go:34`](../../internal/controller/rbac_drift_test.go)). Cost S: two
  targets, two directories, `DEVELOPER.md:65`, `:144` and `:215`, and
  [`package-map.md:105`](../developer/package-map.md). Together with F1 A, kustomize has no
  consumer left, so its pin goes too (`Makefile:36`, `:45-46`, `:388-391`). The chart is the one
  supported upgrade path ([ADR 0014](../adr/0014-rbac-lives-in-three-places.md) D8), and
  `rbac_drift_test.go:7` calls it "the canonical install path". Seven months in which a broken
  `deploy` went unnoticed are the evidence that nobody uses it, so A would repair a path that
  has no user and no test.
  *(Added 2026-09-27, review.)* Under F1 A and E1 B together, `config/rbac/role_binding.yaml`,
  `service_account.yaml` and `kustomization.yaml` lose their last consumer as well: only
  `install`/`uninstall` (today) and `config/default` read them, and neither the chart nor the
  drift test does. They go in the same change, and `role.yaml` stays as the controller-gen
  output that ADR 0014 names as one of the three places.

**Verification:** A: `kubectl kustomize config/default` renders a `roleRef` equal to the
ClusterRole's name. B: `git grep -n 'config/default\|config/manager\|make deploy'` outside
`docs/tickets/` is empty, and `make generate-all` leaves a clean tree.

## History

- 2026-09-27: XS items 1 and 2 landed, file by file (read in `git diff` of the working tree):
  - [`test/e2e/migrate_e2e_test.go`](../../test/e2e/migrate_e2e_test.go): the comment line
    "Build the absolute path if relative." deleted; `exec.Command(binaryPath, "migrate")` is
    unchanged and now at `:147`.
  - [`.github/workflows/release.yml`](../../.github/workflows/release.yml): the line
    `E2E_TESTS: "true"` (was `:429`) deleted; the E2E step's `env:` now starts with `E2E_RUN`,
    and no other line of the file changed.
  - [`docs/developer/testing.md`](../developer/testing.md): the two-line `E2E_TESTS` bullet
    under "What is wrong today, or not verified" deleted.

  Verified afterwards: `grep -n 'Build the absolute path' test/e2e/migrate_e2e_test.go` and
  `git grep -n E2E_TESTS -- ':!docs/tickets'` print nothing; Python `yaml.safe_load` parses
  `release.yml`. A side effect for other tickets: every `release.yml` line after the deleted one
  moved up by one, so the `release.yml` line references above 429 in
  [041](041-the-integration-tier-writes-no-valkey-values.md),
  [043](043-lint-and-vet-skip-every-build-tagged-test-file.md),
  [045](045-ci-kubernetes-and-cert-manager-pins-have-no-renovate-manager.md) and
  [058](058-no-ci-gate-renders-the-chart.md), read at `4a7543e`, are one higher than the working
  tree now (checked for `:499`, `:610`, `:625`, `:658`, `:715`, `:778`); they were not rewritten
  in this pass. F1, the binary path and E1 still wait on their decisions; state, urgency (`now`,
  F1) and effort unchanged. **Not verified:** nothing was run beyond the greps and the YAML
  parse; no CI run carries the change yet.
- 2026-09-27: adversarial review of the enrichment. Two claims corrected in place: the F3 note
  that every env read under `test/` goes through a named constant (three read literals; none
  names `E2E_TESTS`), and E1's `git log` of `role_binding.yaml` (only `0a90483`, not `0aaa3a2`).
  E1 option B now names the three `config/rbac` files that lose their consumer under F1 A. The
  `blocked-by` comment names E1 too. Spot-checked and holding: `Makefile:26`, `:175-178`, `:324`,
  `:327`, `:355-360`, `:404`, `migrate_e2e_test.go:35`, `:142`, `:147-148`, `:154`, `:161`,
  `e2e_test.go:381-389`, `release.yml:429`, `testing.md:108` and `:182`, `package-map.md:105`, the
  `DEVELOPER.md` rows, `rbac_drift_test.go:7` and `:34`, and the `config/default` render
  (`roleRef` `valkey-operator`, ClusterRole `valkey-operator-valkey-operator-role`). Both XS items
  confirmed. Frontmatter values unchanged.
- 2026-09-27: enriched. Re-verified at `4a7543e`, and the two stale F3 claims corrected in place
  (`E2E_TESTS` is also named in `testing.md:182`). Findings added: F1's RBAC does not fit
  together, and E1, where `make deploy` binds a missing ClusterRole. Options were costed and
  ordered (F1 first, then the path after T47, E1 independent), and two XS items that need no
  decision were split out: the `:147` comment and F3. `blocked-by: decision` added, because F1,
  the path and E1 are open. Urgency stays `now` (rule 1, the comment and the F1 help text) and
  effort stays S. Title widened by E1.
- 2026-09-27 — F1 (`install`/`uninstall`) and F3 (`E2E_TESTS`) appended as members of the same
  family, moved from a ticket that had bundled them with unrelated work; both re-verified the same
  day. Title widened, effort XS -> S.
- 2026-09-27 — filed from the documentation restructure; the false comment at `:147` was found
  while verifying.
