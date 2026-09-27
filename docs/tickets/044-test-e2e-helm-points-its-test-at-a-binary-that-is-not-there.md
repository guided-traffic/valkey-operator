---
id: T44
title: make targets and CI settings that do not do what they say - test-e2e-helm's binary path, install/uninstall, E2E_TESTS, deploy's ClusterRole binding
state: analysed       # was filed; every fact re-verified at 84a39c2, every open item analysed (History 2026-09-27)
severity: low         # dev targets only, run by no CI job, test or doc instruction. Worst cases, each needing a target run against a cluster where the chart runs as valkey-operator: install/uninstall strip the operator's ClusterRoleBinding (F1), deploy adds a second operator bound to the chart's ClusterRole (E1)
security: none        # developer Make targets, a test path and a CI env line
urgency: now          # rule 1: measured-false statements in tracked files - the install/uninstall help at Makefile:355, :359, ADR 0006:91-93 and ADR 0017:1105 (was: the comment at migrate_e2e_test.go:147, deleted in bcc63c9, and the help text)
effort: S             # deletions, one token, doc lines and three in-place ADR notes; XS until the 2026-09-27 appendix
blocked-by: decision  # F1 is the one open decision; E1 needs only the owner's go-ahead; the binary path waits on nothing (was: F1, the path and E1, with the path's Kind run waiting on T47's option)
filed-from: the documentation restructure of 2026-09-27 (docs/developer/testing.md)
opened: 2026-09-27
decided:              # not recorded - no decision yet
done:                 # not recorded - not done
---

Filed on 2026-09-27 from a finding of the documentation restructure. Everything below was read in
the working tree of `feat/rootless` on 2026-09-27; **the target was not run**, because it needs a
Kind cluster with the operator installed. *(Re-read 2026-09-27 at `4a7543e` on
`chore/maintenance-2026-09-27`: every line cited below still holds, except where a correction
says otherwise.)* *(Re-verified 2026-09-27 at `84a39c2`, History: locations re-read, false and
outdated claims corrected in place, the Options rewritten. No target, test or cluster was run;
the renders were made offline with `KUBECONFIG=/dev/null`.)*

## Fact

**Verified (read):**

- [`Makefile:175-178`](../../Makefile): `test-e2e-helm: build`, then
  `MANAGER_BINARY=./bin/manager go test -v -tags=e2e,e2e_helm -count=1 -timeout=10m -run TestE2E_Migrate ./test/e2e/...`.
- [`Makefile:322-324`](../../Makefile): `build` writes the binary to `bin/manager` at the
  repository root.
- [`migrate_e2e_test.go`](../../test/e2e/migrate_e2e_test.go): `runMigrateBinary` (`:139`) takes
  `MANAGER_BINARY` from the environment (`:142`), falls back to `defaultManagerBinary` =
  `"../../bin/manager"` (`:35`) only when it is empty, and runs
  `exec.Command(binaryPath, "migrate")` (`:147`) without setting `cmd.Dir`.
  `TestE2E_MigrateDefaults` calls it at `:81` and fails on any error (`require.NoError`, `:160`).
  It logs the path it runs at `:153` (`Running: %s migrate`), which the verification below reads.
- `go help testflag` (Go 1.27.1): "When 'go test' runs a test binary, it does so from within the
  corresponding package's source code directory." `go doc os/exec.Command`: a name with a path
  separator is not looked up on `PATH`; `go doc os/exec.Cmd`: an empty `Dir` runs the command in
  the calling process's current directory.
- Together: the test process runs in `test/e2e/`, so `./bin/manager` names
  `test/e2e/bin/manager`, which nothing builds. The test's own default, `../../bin/manager`, is
  the path that reaches the binary the target has just built; the target overrides it with the
  wrong one. *(Measured 2026-09-27 at `84a39c2`: `ls test/e2e/bin` answers "No such file or
  directory", `test -x bin/manager` holds at the repository root.)*
- **The comment at `migrate_e2e_test.go:147` ~~is~~ *(was, until 2026-09-27)* false.** It ~~reads~~
  *(read)* "Build the absolute path if relative." and ~~is~~ *(was)* followed directly by
  `exec.Command(binaryPath, "migrate")`; no code in the function makes the path absolute.
  *(Deleted 2026-09-27, History. `exec.Command` is at `:147` since, one line up; ~~the other
  `migrate_e2e_test.go` lines cited above it are unchanged.~~)* *(corrected 2026-09-27 at
  `84a39c2`: the deletion is commit `bcc63c9`. Every cited line below the deleted one moved up by
  one - `exec.Command` to `:147`, the log line to `:153`, `require.NoError` to `:160` - and only
  `:35`, `:81`, `:139` and `:142` are unchanged. The links above now carry the new numbers.)*
- No workflow runs the target (grep over `.github/workflows/`, 2026-09-27), so CI never noticed;
  the file is not even compiled there, see
  [ticket 043](043-lint-and-vet-skip-every-build-tagged-test-file.md).
- *(Added 2026-09-27 at `84a39c2`.)* **The target has pointed at the missing file for its whole
  life.** `git log -S'MANAGER_BINARY' -- Makefile test/e2e` finds only `73f6efe` (2026-03-02,
  "feat: Operator Self-Upgrade / Cluster Migration"), the commit that added the target, the test
  and the false comment together; `git show 73f6efe:Makefile` carries the same recipe.
  `git log -S'test-e2e-helm' -- .github` is empty, so no workflow ever named it.
- *(Added 2026-09-27 at `84a39c2`.)* **Only one assertion of `TestE2E_MigrateDefaults` depends on
  `migrate`.** `simulateOldCR` merge-patches `secretPasswordKey: ""`
  ([`migrate_e2e_test.go:128`](../../test/e2e/migrate_e2e_test.go)), a present empty value that
  structural defaulting does not fill (the CRD default is `password`,
  [`vko.gtrfc.com_valkeys.yaml:120`](../../config/crd/bases/vko.gtrfc.com_valkeys.yaml)), and
  `applyDefaults` rewrites exactly that ([`migrate.go:114-116`](../../cmd/migrate/migrate.go)).
  The replicas subtest (`migrate_e2e_test.go:101-116`) can fail on its own `Get` or `found`
  check, but nothing `migrate` does to `spec.replicas` can make it fail: the CR is created with
  `replicas: 1` (`:52`), the CRD refuses 0 (`+kubebuilder:validation:Minimum=1`,
  [`valkey_types.go:1027-1030`](../../api/v1/valkey_types.go)), and `applyDefaults` only raises
  0 to 1 (`migrate.go:108-111`). It asserts what the CRD already guarantees.
- *(Added 2026-09-27 at `84a39c2`.)* **The test runs `manager migrate` on the host, as the
  kubeconfig user**: `migrate` builds its client with `ctrl.GetConfigOrDie()`
  ([`migrate.go:46`](../../cmd/migrate/migrate.go)), and `runMigrateBinary` sets neither `Dir` nor
  `Env`. It therefore never exercises the pre-upgrade hook's ServiceAccount or RBAC.
  `TestE2E_FleetUpgrade` does run `migrate` as the hook Job: it runs a real `helm upgrade` to the
  local chart ([`fleet_upgrade_test.go:318-323`](../../test/e2e/fleet_upgrade_test.go), header
  `:16-19`), the hook is on by default
  ([`values.yaml:151-153`](../../deploy/helm/valkey-operator/values.yaml);
  `test/e2e/helm-values.yaml` does not override it), and `helmRun` fails the test on a non-zero
  exit (`fleet_upgrade_test.go:992-994`). What only `TestE2E_MigrateDefaults` adds is the
  assertion that the present-empty `secretPasswordKey` is patched against a real API server.

**Not verified:**

- That `make test-e2e-helm` actually fails at the migrate step. It follows from the three reads
  above, but the target was not run, and a stray file at `test/e2e/bin/manager` on a developer's
  machine would make it pass. *(2026-09-27 at `84a39c2`: still not run. What would settle it:
  one run on a Kind cluster, expecting `fork/exec ./bin/manager: no such file or directory`.)*
- Whether the rest of `TestE2E_MigrateDefaults` passes once the binary is found. *(2026-09-27 at
  `84a39c2`: by reading it should, see the Fact above; not run.)*
- *(Added 2026-09-27)* Whether the test needs a running operator, as the help text at
  `Makefile:176` says. Its body only creates, patches and reads a CR and runs `manager migrate`
  (`migrate_e2e_test.go:42-117`), and `deleteValkey` does not wait for the deletion
  ([`e2e_test.go:381-389`](../../test/e2e/e2e_test.go)). Read that way, a Kind cluster with the
  CRD would be enough. Not run. *(2026-09-27 at `84a39c2`: reading supports it further -
  `newTestClients` builds its clients from `KUBECONFIG` (`e2e_test.go:68-80`), `createValkey`
  is a bare `Create` (`:138-145`), the package has no `TestMain`, and the chart ships no
  admission webhook. The same unverified claim sits in
  [`docs/developer/testing.md:18`](../developer/testing.md) ("a Kind cluster with the
  operator"). What would settle both: the Kind run with only `kubectl apply -f
  config/crd/bases/` and no operator.)*

## Impact

Whoever runs `make test-e2e-helm` — `DEVELOPER.md` and `docs/developer/testing.md` list it as the
way to run the Helm-migration e2e — gets a failure at the migrate step that looks like a defect
in `manager migrate` and is a path in the Makefile. The migration e2e therefore has no working
entry point, and the `migrate` subcommand has no end-to-end check that anyone can run as
documented. *(Precised 2026-09-27 at `84a39c2`: `migrate` is exercised end to end, as the hook
Job, by `TestE2E_FleetUpgrade` (Fact). What has no working entry point is the one assertion on
the present-empty field.)*

The impact of F1 and E1 is under their own headings in the appendix.

## Options

*(Rewritten 2026-09-27 at `84a39c2` as the current analysis. The earlier option texts, and the
options removed now, are recorded in the History entry of that day.)* Of the three open items
only F1 is a decision with more than one sensible option. The binary path and E1 each have one
sensible answer left and are listed in the Work list.

### Decision F1 — what `make install` and `make uninstall` do

**Mechanism today.** [`Makefile:354-360`](../../Makefile) pipe `$(KUSTOMIZE) build config/rbac`
into `kubectl apply` and `kubectl delete` under help texts that promise CRDs. `config/rbac`
renders a ClusterRole `valkey-operator-role` (the generated `role.yaml`, `Makefile:404`), a
ClusterRoleBinding `valkey-operator` ([`role_binding.yaml`](../../config/rbac/role_binding.yaml))
and a ServiceAccount ([`service_account.yaml`](../../config/rbac/service_account.yaml)).
No CRD is involved. On a cluster where the chart runs under the documented release name
`valkey-operator` - the one `make e2e-local`, CI and the installation guide use - the chart's own
ClusterRoleBinding has the same name, so both targets act on the chart's binding, and the
chart's operator loses its ClusterRole until the next `helm upgrade` (appendix F1, Fact). `make run`
([`Makefile:326-328`](../../Makefile)) is the one local path that needs the CRD, and
[`DEVELOPER.md:251-253`](../../DEVELOPER.md) already gives the one-line `kubectl apply` for it.

**What the choice changes:** which command installs the CRD for `make run`, and whether any Make
target writes or deletes cluster objects that the chart owns. **What it does not change:** the
chart and the one supported install and upgrade path
([ADR 0014](../adr/0014-rbac-lives-in-three-places.md) D8), `config/rbac/role.yaml` (read by
`TestHelmClusterRoleCoversGeneratedRole`,
[`rbac_drift_test.go:34`](../../internal/controller/rbac_drift_test.go)), any operator behaviour,
and the larger wrong-context hazard of `make run` itself, which starts an operator without
leader election (`--leader-elect` defaults to false, [`main.go:71`](../../cmd/main.go)) against
whatever context is current.

- **A - make the help true: `install` applies `config/crd/bases/`, `uninstall` deletes it** (the
  kubebuilder convention, whose scaffold builds `config/crd` in both targets). Cost: two recipe
  lines, two help strings, `DEVELOPER.md:214` and `:251-253`. Removes the collision with the
  chart's binding. Consequences:
  - `uninstall` deletes the CRD, and with it every `Valkey` CR on whatever cluster the current
    context names; garbage collection removes their StatefulSets, Services and ConfigMaps. PVCs
    stay, the dataset of every non-persistent cluster is gone
    ([`installation.md:77-82`](../operations/installation.md)). Nothing guards the context, and a
    context guard would be more code.
  - `install` against a chart-installed cluster writes the working-tree CRD of whatever branch is
    checked out over the Helm-owned one, outside any release; it can be older or newer than the
    running operator. By reading, Helm's ownership label and annotations survive that apply (a
    client-side apply without a last-applied annotation removes no field its manifest lacks), so
    the next `helm upgrade` writes the chart's CRD back. Whether fields unknown to the older
    schema are pruned from stored CRs on their next write was not verified.
  - On a cluster where `make install` created the CRD first, a later `helm install` of the chart
    is refused unless it passes `--take-ownership` (appendix F1, Fact).
- **A2 - `install` applies the CRD, `uninstall` is deleted.** Cost: one recipe line, one help
  string, the `uninstall` target, `DEVELOPER.md:214` and `:251-253`. Removes the collision and
  the cascade delete. Keeps A's out-of-band CRD write on the wrong context and A's
  `--take-ownership` case, and leaves a kubebuilder-named `install` without its counterpart.
- **C - delete both targets (recommended).** `make run` keeps the documented one-line
  `kubectl apply -f config/crd/bases/vko.gtrfc.com_valkeys.yaml` (`DEVELOPER.md:251-253`). Cost:
  delete `Makefile:354-360` (and `:350-352`, the `ignore-not-found` default, once E1 also
  removes `undeploy`); `DEVELOPER.md:144` (drop `install`), `:214` (drop the row) and `:251-252`
  (drop "see `install` above"). No Make target writes or deletes RBAC or a CRD any more.

**C is recommended** because the targets have no consumer: they are unchanged since the scaffold
(`25483b2`), their binding has missed its own ClusterRole since `0aaa3a2`, and nobody noticed; no
doc, test or workflow runs them. C is zero code (minimum code), it removes both halves of the
collision with the chart's binding, and it adds no command that writes a CRD out of band.
Checkable: after C, `grep -nE '^(install|uninstall):' Makefile` is empty and
`git grep -n 'config/rbac' Makefile` finds only the controller-gen line (`:404`). **C beats the
runner-up A2** because A2's only gain is wrapping one documented command in a target, and for
that it keeps the out-of-band CRD write on whatever context is current. A2 deletes nothing, so
"no destructive target" does not separate the two; the deciding points are no consumer, zero
code and no CRD write. **C beats A** because A adds a cascade delete of every `Valkey` CR on the
current context to buy the same one command. *(The ticket marked A before 2026-09-27 at
`84a39c2`; History.)*

### The binary path - one sensible answer, no decision left

**Mechanism today.** `Makefile:178` sets `MANAGER_BINARY=./bin/manager`; `go test` runs the test
binary in `test/e2e/`, `runMigrateBinary` reads the variable (`migrate_e2e_test.go:142`), and
`exec.Command` with no `Dir` (`:147`) resolves it to `test/e2e/bin/manager`, which does not exist
(Fact). **The fix changes** only whether the documented target reaches the binary `build` has
just written (`go build -o bin/manager`, `Makefile:324`). **It does not change** `manager
migrate`, the pre-upgrade hook that runs it on every production `helm upgrade`, CI (no job runs
the target), or T47's question whether the hook should exist.

The one sensible answer is `MANAGER_BINARY=$(CURDIR)/bin/manager` at `Makefile:178`.
`$(CURDIR)` is the directory `build` writes into (make's working directory, after any `-C`), so
the path is correct whatever the depth of `test/e2e/`, and the Makefile owns both the builder
and the consumer of the path. `$(LOCALBIN)` is the wrong variable: it is `?=`-overridable
(`Makefile:26`), while `build` hardcodes `bin/manager`. An absolute path skips `LookPath` and does
not depend on `cmd.Dir` (`go doc os/exec`), so `make -n test-e2e-helm` showing
`MANAGER_BINARY=<repo>/bin/manager` plus `test -x bin/manager` proves the path; the Kind run is
needed only for the two reads it also settles (the rest of the test, and whether the operator is
needed). Nothing rolls, CI is unchanged. It needs no timing decision either: T47's option C would
delete the recipe with the token in it (its removal list names `Makefile:175-178`), so taking the
token now adds no removal work; only the Kind run could be spent for nothing, and it can share
the Kind cluster T47's own measurement needs. The removed alternatives are in the History entry
of 2026-09-27 at `84a39c2`.

### E1 - `make deploy`: one sensible answer, needs the owner's go-ahead

**Mechanism today.** [`Makefile:362-369`](../../Makefile): `deploy` runs `kustomize edit set
image controller=${IMG}` inside `config/manager`, which rewrites the tracked
`config/manager/kustomization.yaml` in the working tree, then applies `config/default`;
`undeploy` deletes it. The render has at least five defects (appendix E1): a `roleRef` that
misses its ClusterRole, no Namespace object, no operator image, no `POD_NAMESPACE`, and no
`leases` rule in the ClusterRole it creates while the Deployment passes `--leader-elect`.
**The choice changes** whether the repository ships a second, kustomize-based install path next
to the chart. **It does not change** the chart, `config/rbac/role.yaml` (the generated source the
drift test reads, ADR 0014 D1, D6), the production install path, or any operator behaviour. ADR
0005 (`:310`, `:395`), ADR 0007 (`:33`) and ADR 0032 (`:160`) speak of "the kustomize path" as
any install whose sidecar image does not move; that stays true for user-built overlays and
floating tags whichever way this goes.

The one sensible answer is **B - retire `deploy`/`undeploy`, `config/default` and
`config/manager`, and with them `config/rbac/kustomization.yaml`, `role_binding.yaml`,
`service_account.yaml` and the kustomize pin; keep `config/rbac/role.yaml`.** The three
`config/rbac` overlay files and the pin lose their last consumer under every remaining F1 option,
because none of A, A2 or C uses kustomize. Cost S: `Makefile:36`, `:45-46`, `:350-352` (with F1),
`:362-369`, `:388-391`; two directories and three files; `DEVELOPER.md:64`, `:65`, `:144`,
`:147`, `:215`; [`package-map.md:104-105`](../developer/package-map.md); an in-place amendment
of ADR 0014 D8 (below). Consequences: `make generate-all` still writes `role.yaml` and the drift
test still reads it, so ADR 0014's three places are unchanged; no second install path can drift
from the chart; T45's count of Makefile lines its Renovate manager matches drops from 6 to 5.
Justification: the chart is the one supported upgrade path (ADR 0014 D8,
[`0014-rbac-lives-in-three-places.md:135-143`](../adr/0014-rbac-lives-in-three-places.md)),
[`rbac_drift_test.go:7`](../../internal/controller/rbac_drift_test.go) calls it "the canonical
install path", and it is the only one CI and `make e2e-local` install (`release.yml:353`, `Makefile:218`); the overlay has
bound its operator to a ClusterRole it does not create in every release (`git tag --no-contains
0aaa3a2` is empty) and nobody noticed. Checkable: `git grep -n
'config/default\|config/manager\|KUSTOMIZE' -- ':!docs/tickets'` is empty (the ADR lines about
"the kustomize path" say lower-case "kustomize" and name neither directory, so they do not match
and stay), and `make generate-all` leaves a clean tree.

**It is not only developer tooling.** Retiring the only non-chart install path is a durable
refusal that three ADRs reason around, so the close amends ADR 0014 D8 in place with a dated
note: the chart is the only install path this repository ships, the kustomize overlays were
retired, and "the kustomize path" in ADR 0005, 0007 and 0032 means a user-built overlay. That is
why it needs the owner's go-ahead although no second option is left.

## Work list (2026-09-27)

**XS, no decision needed. These can be done today, independently of the rest:**

1. [`migrate_e2e_test.go:147`](../../test/e2e/migrate_e2e_test.go): delete the false comment
   "Build the absolute path if relative.". **Done 2026-09-27.** *(Commit `bcc63c9`, History.)*
2. F3: delete `E2E_TESTS: "true"` at
   [`release.yml:429`](../../.github/workflows/release.yml), and the bullet at
   [`docs/developer/testing.md:182-183`](../developer/testing.md) that describes it. It removes
   an env line and adds no job, so branch protection is untouched. **Done 2026-09-27**, both
   halves in the same change. *(Commit `bcc63c9`, History.)*

Neither closes the ticket. ~~Once both land, the only rule-1 statement left is the
`install`/`uninstall` help text (F1), so urgency stays `now` until F1 lands. *(2026-09-27: both
landed; urgency stays `now` for F1.)*~~ *(corrected 2026-09-27 at `84a39c2`: two more rule-1
statements of this family exist, ADR 0006:91-93 and ADR 0017:1105, items 4 and 5 below; urgency
stays `now` until F1 and both ADR notes land.)*

**Added 2026-09-27 at `84a39c2`, no decision needed:**

3. **The binary path**: `MANAGER_BINARY=$(CURDIR)/bin/manager` at `Makefile:178` (Options). XS.
   In the same change: the [`testing.md:108`](../developer/testing.md) row names the new path, the
   `testing.md:182-186` bullet is deleted. After the Kind run: `Makefile:176` and `testing.md:18`
   say "CRD" or "operator" according to what the run showed. The Kind run: `make kind-create`,
   `kubectl apply -f config/crd/bases/`, `make test-e2e-helm`; it can share T47's Kind cluster.
4. **ADR 0006, in place with a dated note**:
   [`0006-delete-only-what-the-operator-owns.md:91-93`](../adr/0006-delete-only-what-the-operator-owns.md)
   says `9e5634d` "is branch-only" and that "the kubebuilder marker and `config/rbac/role.yaml`
   carried the verb all along, so a kustomize install had it before that". All three are false:
   `git tag --contains 9e5634d` lists `v1.11.0` and every later tag; `role.yaml` carried
   `secrets: get, list, watch` until `ee217dd` (2026-04-28) added `delete`
   (`git show c6f97e2:config/rbac/role.yaml` against `git show ee217dd:...`); and no overlay this
   repository ships has bound `role.yaml`'s ClusterRole since `0aaa3a2` (2026-02-17 15:41), which
   precedes the first secrets marker (`ef90917`, 17:53 the same day), so at no revision did a
   shipped overlay bind a ClusterRole with the verb. It is true only for a user-built overlay that
   binds `role.yaml` correctly. XS, holds under every F1 and E1 outcome.
5. **ADR 0017, in place with a dated note**:
   [`0017-test-and-ci-policy.md:1105`](../adr/0017-test-and-ci-policy.md) names kustomize as a
   generator tool whose bump "blocks its own automerge". It does not: `generate-all` is
   `manifests generate sync-helm-crd` (`Makefile:331`), controller-gen only (`:403-405`); the
   `generated-manifests` job runs only `make generate-all` (`release.yml:743`, also
   `build.yml:160`); no workflow names kustomize, and `renovate.json` has no kustomize rule. Drop
   "kustomize" from the parenthesis. XS, holds under every option; `0017:185` is narrative
   history and stays.
6. **The "its binding" wording** at `DEVELOPER.md:64`, `:214` and
   [`package-map.md:104`](../developer/package-map.md): `role_binding.yaml`'s `roleRef` names
   `valkey-operator`, not `role.yaml`'s `valkey-operator-role`. Fold into the F1/E1 change, which
   rewrites or deletes those lines anyway.

**Waits on a decision or a go-ahead:**

1. **F1: what `install`/`uninstall` do** (Options, Decision F1). It is independent of every other
   ticket, and its outcome decides the text of `DEVELOPER.md:144`, `:214` and `:251-253`.
2. **E1: retire the kustomize overlays** (Options). One sensible answer; needs the owner's
   go-ahead because its close amends ADR 0014 D8. Take it together with F1: `Makefile:350-352`
   (the `ignore-not-found` default) can go only once `uninstall` and `undeploy`, its two users,
   are both gone, and both touch the same `DEVELOPER.md` lines.

On close, whatever was taken also changes the docs that describe today's defect:
[`testing.md:108`](../developer/testing.md) (the `MANAGER_BINARY` row) and ~~`:184-188`~~ `:182-186`
*(moved up two lines when the `E2E_TESTS` bullet was deleted, 2026-09-27)*,
[`DEVELOPER.md:189`](../../DEVELOPER.md), `:214` and `:251-253`. For E1 they are `DEVELOPER.md:65`,
`:144` and `:215` and [`package-map.md:105`](../developer/package-map.md). ~~None of this is an
ADR decision (developer tooling), so the extraction goes to `docs/developer/` and `DEVELOPER.md`.~~
*(corrected 2026-09-27 at `84a39c2`: the list is incomplete and the ADR sentence is wrong for
E1. Add [`testing.md:18`](../developer/testing.md) (after the Kind run), `DEVELOPER.md:64` and
`:147` (kustomize among the pinned tools), `package-map.md:104`, and the three ADR notes: ADR 0006
and ADR 0017 (items 4 and 5) and the ADR 0014 D8 amendment E1 needs. The extraction goes to those
ADRs, `docs/developer/` and `DEVELOPER.md`.)*
`git grep -nE 'T44\b|044-'` outside `docs/tickets/` is empty today. *(Re-run 2026-09-27 at
`84a39c2`: still empty.)*

**Cross-ticket findings (2026-09-27 at `84a39c2`; recorded here, the other tickets are not
edited from this one):**

- **T47.** Its decision 2 option C removes this target and `TestE2E_MigrateDefaults`; the path
  fix accepts that loss (one token and two doc lines). Its G1 paragraph cites this ticket as part
  of G1's verification gap, but a working `test-e2e-helm` would not close that gap: the test runs
  `migrate` as the kubeconfig user, never as the hook's ServiceAccount (Fact). The harness that
  does run the hook with the chart's hook RBAC is `make e2e-fleet-upgrade-local`
  (`fleet_upgrade_test.go:318-323`, `:992-994`), with one caveat for G1: if no CR in its fleet
  needs a default, `migrate` issues no Patch, so a missing `patch` verb would pass and only a
  missing `list` would fail. The test's present-empty `secretPasswordKey` passes under both of
  T47's readings of the hook, so it cannot serve as T47's measurement. T47 C's removal list omits
  `docs/developer/testing.md:182-186` (while the path fix has not landed), `DEVELOPER.md:72` and
  `package-map.md:95`, which name the `e2e_helm` tag. *(Precised 2026-09-27: all of that is
  047 as committed at `84a39c2`. The revision of 047 made in the same run already records the
  kubeconfig-user point and names `TestE2E_FleetUpgrade` as its harness, and its removal list now
  carries `testing.md:18`, `:108`, `:182`, `DEVELOPER.md:72`, `:189` and `package-map.md:95`.
  One sentence of it no longer matches this ticket: "T44 already says its binary-path fix and
  Kind run are wasted under this ticket's C" - this ticket now takes the path fix without waiting
  on T47, and only the Kind run is at stake; 047 is not edited from here.)*
- **T43.** Its work item that ordered it after T44 because of the `migrate_e2e_test.go` comment
  is obsolete since `bcc63c9`; T43 already records that.
- **T45.** Its count of Makefile lines the Renovate custom manager matches (6 of the 7
  `# renovate:` lines, kustomize at `Makefile:45` among them) becomes 5 under E1 B.
- **T41, T43, T45, T58.** ~~Their `release.yml` citations are still one line too high since the
  `E2E_TESTS` deletion~~ *(corrected 2026-09-27, consistency pass: each of the four corrected its
  citations in its own re-verification of the same day; their live text cites the lines below,
  and only their History entries keep the old ones)*: at `84a39c2` `linter:` is `:498`, `run: make test-unit-coverage` `:609`,
  `valkey-image-tools:` `:624`, `run: make test-image-tools` `:657`, `generated-manifests:`
  `:714`, `run: make test-integration-coverage` `:777`.
- **T35, T57.** Both reason about "the kustomize path" as an install whose sidecar image does not
  move. *(Precised 2026-09-27, consistency pass: 035's re-verification of the same day removed its
  sentence (`035:656` at `84a39c2`); 057 keeps two generic mentions, "kustomize, a floating tag or
  a pinned `image.tag`", which do not cite `config/default`.)* That stays valid for user-built overlays and floating tags, but they should not cite this
  repository's `config/default` as an example: it has not produced a bound operator since
  `0aaa3a2`.
- **T30** (severity high, security boundary, effort M, state ~~filed~~ analysed *(corrected
  2026-09-27, consistency pass)*): embargoed security finding, open - details in its own ticket
  file until it is fixed.

## Decision

None yet. The one open decision is F1; E1 waits on the owner's go-ahead.

## Verification

- `make test-e2e-helm` passes on a Kind cluster with ~~the operator installed~~ *(corrected
  2026-09-27 at `84a39c2`: the CRD applied, and without the operator if the run shows it is not
  needed, Fact)*, with its output showing `Running: <absolute path>/bin/manager migrate`
  (`migrate_e2e_test.go:153`).
- Revert check: with `MANAGER_BINARY=./bin/manager` restored in a scratch copy, the same run fails
  at `runMigrateBinary` with a "no such file or directory" error.
- *(Added 2026-09-27 at `84a39c2`.)* Before any cluster: `make -n test-e2e-helm` prints
  `MANAGER_BINARY=<repo>/bin/manager`, and `test -x bin/manager` holds after `make build`.
- *(Added 2026-09-27, XS item 1:)* `grep -n 'Build the absolute path' test/e2e/migrate_e2e_test.go`
  is empty. `make lint` does not see the file (build tag, ticket 043). Deleting a comment line
  cannot change what compiles, so the grep is the whole proof. XS item 2:
  `git grep -n E2E_TESTS -- ':!docs/tickets'` is empty (F3 below). *(Run 2026-09-27 after the
  fix: both print nothing. Done.)* *(Re-run 2026-09-27 at `84a39c2`: both still print nothing.)*
- *(Added 2026-09-27 at `84a39c2`.)* F1 C: `grep -nE '^(install|uninstall):' Makefile` is empty.
  E1 B: the grep in Options, E1, is empty and `make generate-all` leaves a clean tree. ADR notes:
  `git grep -n 'branch-only' docs/adr/0006-delete-only-what-the-operator-owns.md` and
  `git grep -n 'controller-tools, kustomize' docs/adr/0017-test-and-ci-policy.md` find only
  struck-through text.

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
  `make run`. Both change when F1 lands.)* *(2026-09-27 at `84a39c2`: `DEVELOPER.md:144` also
  lists `install` among the `kubectl` consumers.)*
- *(Added 2026-09-27.)* The RBAC the targets apply does not fit together either. Rendered with
  `kubectl kustomize config/rbac` ~~(kubectl's embedded kustomize, not the pinned v5.8.1)~~
  *(corrected 2026-09-27 at `84a39c2`: `KUBECONFIG=/dev/null kubectl version --client` reports
  Client Version v1.36.2 and Kustomize Version v5.8.1, the version `Makefile:46` pins, so the
  render is the pinned version's output)*:
  - the ServiceAccount goes into namespace `system`
    ([`service_account.yaml:5`](../../config/rbac/service_account.yaml)), which nothing creates;
  - the ClusterRoleBinding refers to a ClusterRole `valkey-operator`
    ([`role_binding.yaml:7-8`](../../config/rbac/role_binding.yaml));
  - the generated ClusterRole is named `valkey-operator-role`
    ([`Makefile:404`](../../Makefile), `rbac:roleName=valkey-operator-role`).

  So today's `install` would not even produce working RBAC. It was not run.
- *(Added 2026-09-27 at `84a39c2`.)* **The targets collide with the chart's own
  ClusterRoleBinding.** The chart is installed under the release name `valkey-operator` by
  `make e2e-local` (`Makefile:218`), by CI (`release.yml:353`) and by the installation guide
  ([`installation.md:18`, `:28`](../operations/installation.md)). Its `fullname` is then the
  release name ([`_helpers.tpl:11-22`](../../deploy/helm/valkey-operator/templates/_helpers.tpl)),
  and `KUBECONFIG=/dev/null helm template valkey-operator deploy/helm/valkey-operator --namespace
  valkey-operator-system` (Helm v3.21.3) renders ClusterRole `valkey-operator` and
  ClusterRoleBinding `valkey-operator`. `config/rbac`'s binding has the same name. By reading,
  not run:
  - `make uninstall` deletes the chart's ClusterRoleBinding by name.
  - `make install` applies `config/rbac`'s binding over it.
  - Either way the operator's own ServiceAccount loses its ClusterRole until a later
    `helm upgrade` re-applies the binding. That includes the Kind cluster `make e2e-local` builds,
    and any cluster the current context names; nothing in the targets guards the context.
- *(Added 2026-09-27 at `84a39c2`, Helm source, not run.)* A `helm install` over an existing
  object without Helm's ownership metadata fails with "exists and cannot be imported into the
  current release" ([validate.go:84 at v3.21.3](https://raw.githubusercontent.com/helm/helm/v3.21.3/pkg/action/validate.go));
  `checkOwnership` (`:94`) requires the label `app.kubernetes.io/managed-by: Helm` (`:105`) and
  the annotations `meta.helm.sh/release-name` (`:108`) and `meta.helm.sh/release-namespace`
  (`:111`). `--take-ownership` skips the check
  ([install.go:114-115, :353 at v3.21.3](https://raw.githubusercontent.com/helm/helm/v3.21.3/pkg/action/install.go)).
  The tree CRD carries none of that metadata
  ([`vko.gtrfc.com_valkeys.yaml:1-7`](../../config/crd/bases/vko.gtrfc.com_valkeys.yaml)). This
  settles the item F1 option A had left unverified.

**Not verified:** that the targets were ever used as they are. ~~`git log -S` was not run for
them.~~ *(corrected 2026-09-27: run. `git log -S'build config/rbac' -- Makefile` finds only
`25483b2` (2026-02-17, the project scaffold), so both targets are unchanged since the
scaffold.)* *(2026-09-27 at `84a39c2`: also not verified - every runtime consequence above,
including the collision with the chart's binding; what would settle it is one run of each target
against a scratch Kind cluster with the chart installed.)*

**Impact:** whoever follows `make help` gets RBAC and no CRD, and a `make run` that cannot watch
a `Valkey`. *(Added 2026-09-27 at `84a39c2`:)* whoever runs either target against a cluster where
the chart runs as `valkey-operator` - the Kind cluster of `make e2e-local` included - strips the
operator's ClusterRoleBinding until the next `helm upgrade`, and the operator stops
reconciling on permission errors. No CI job, test or doc instruction runs the targets, so it
takes a developer's hand.

**Options:** see Options, Decision F1, above. *(Moved there 2026-09-27 at `84a39c2`; the removed
option B and the earlier mark on A are in the History entry of that day.)*

**Verification:** ~~on a scratch Kind cluster `make install` creates `valkeys.vko.gtrfc.com` and
`make uninstall` removes it.~~ *(corrected 2026-09-27 at `84a39c2`: that was A's check; the
verification of the recommended C is in the Verification section above.)*

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
touching it. *(2026-09-27 at `84a39c2`: the deletion is commit `bcc63c9`.)*

**Options:** none left - done by deleting the line (the former option A). The former option B is
recorded as removed in the History entry of 2026-09-27 at `84a39c2`.

**Verification:** ~~`git grep -n E2E_TESTS` is empty.~~ *(corrected 2026-09-27:
`git grep -n E2E_TESTS -- ':!docs/tickets'` is empty. The tickets keep the name as history.)*
*(Run 2026-09-27 after the fix: empty. F3 is done.)*

### E1 — `make deploy` binds its ServiceAccount to a ClusterRole that does not exist

*(Added 2026-09-27 while enriching F1. It belongs to the same family: a Make target that does not
do what it says.)*

**Verified (rendered with `kubectl kustomize config/default`, ~~kubectl's embedded kustomize, not
the pinned v5.8.1~~ *(corrected 2026-09-27 at `84a39c2`: kubectl v1.36.2 embeds Kustomize
v5.8.1, the pinned version)*):** `namePrefix: valkey-operator-` renames the ClusterRole to
`valkey-operator-valkey-operator-role`, but the binding's `roleRef` stays `valkey-operator`
([`role_binding.yaml:8`](../../config/rbac/role_binding.yaml)). No ClusterRole of that name is
among the resources, so kustomize has nothing to rename. The Deployment's ServiceAccount
(`config/manager/manager.yaml:44`, renamed by the prefix) is therefore bound to nothing.
~~`git log` of `role_binding.yaml` shows `0aaa3a2` and `0a90483` (both 2026-02-17);~~
*(corrected 2026-09-27, review: `git log --follow -- config/rbac/role_binding.yaml` shows only
`0a90483` (2026-02-17), and `config/default` also dates from `0a90483`)*; the mismatch is
seven months old. No workflow or test names the target, and the only docs that do are
`DEVELOPER.md:65`, `:144` and `:215` and `package-map.md:105`.
*(Precised 2026-09-27 at `84a39c2`:)*
- **Where the mismatch comes from.** Not from `role_binding.yaml`: at `0a90483` the generated
  ClusterRole was named `valkey-operator` and matched the binding
  (`git show 0a90483:config/rbac/role.yaml`); `0aaa3a2` (2026-02-17 15:41) set
  `rbac:roleName=valkey-operator-role` (`git log -S'roleName=' -- Makefile`) and renamed it.
  `git tag --no-contains 0aaa3a2` is empty, so every release carries the mismatch.
- **"Bound to nothing" holds only where no ClusterRole `valkey-operator` exists.** On a cluster
  where the chart runs as `valkey-operator`, the unrenamed `roleRef` resolves to the chart's
  ClusterRole (F1, Fact), so `make deploy` binds its operator to it.
- **Further defects of the render.** It contains a ServiceAccount, a ClusterRole, a
  ClusterRoleBinding and a Deployment and no Namespace object, although
  `config/default/kustomization.yaml:3` puts everything into `valkey-operator-system`, so on a
  cluster without that namespace the namespaced objects are refused. The Deployment passes only
  `--leader-elect` ([`manager.yaml:20-23`](../../config/manager/manager.yaml)), image
  `controller:latest` rewritten to `${IMG}` = `guidedtraffic/valkey-operator:latest`
  (`Makefile:2`, `:364`): with no `--operator-image`/`OPERATOR_IMAGE` the sidecar falls back to
  `ghcr.io/guided-traffic/valkey-operator:latest`
  ([`statefulset.go:989-992`](../../internal/builder/statefulset.go)) - an image that does not
  exist (`docker buildx imagetools inspect ghcr.io/guided-traffic/valkey-operator:latest`:
  "not found", measured by T53 on 2026-09-27 and re-run in the consistency pass the same day),
  so the sidecar container of every data pod on this path cannot be pulled (inference) - and the observer gets an
  empty image ([`observer.go:82`](../../internal/builder/observer.go)); with no `POD_NAMESPACE`
  ([`main.go:173`](../../cmd/main.go)) the NetworkPolicy builders get an empty operator namespace
  ([`valkey_controller.go:1952`, `:1959`](../../internal/controller/valkey_controller.go)). The
  chart passes both ([`deployment.yaml:39`, `:48-51`](../../deploy/helm/valkey-operator/templates/deployment.yaml)).
- **No leases rule.** `grep -n leases config/rbac/role.yaml` is empty; the rule is chart-only by
  design ([`clusterrole.yaml:180`](../../deploy/helm/valkey-operator/templates/clusterrole.yaml),
  `rbac_drift_test.go:16-17`), and controller-runtime v0.25.1 (`go.mod:16`) defaults the lock to
  Leases (`pkg/leaderelection/leader_election.go:75`). So even with the `roleRef` fixed, an
  operator installed by `make deploy` on a cluster without the chart cannot acquire its lease and
  never starts reconciling (read, not run).
- **`deploy` rewrites a tracked file**: `cd config/manager && $(KUSTOMIZE) edit set image` (`:364`)
  edits `config/manager/kustomization.yaml` in the working tree.
- **A second operator on a chart-installed cluster.** The render puts a Deployment into
  `valkey-operator-system`, the chart's documented namespace, with `--leader-elect` and the same
  hardcoded `LeaderElectionID` `valkey-operator.vko.gtrfc.com` ([`main.go:104-108`](../../cmd/main.go)),
  bound to the chart's ClusterRole through the unrenamed `roleRef`. On the Kind cluster of
  `make e2e-local` the chart runs with `leaderElection.enabled: false`
  (`test/e2e/helm-values.yaml:17-18`), so the deployed operator takes a lease nobody contends and
  both reconcile every `Valkey` CR, with different sidecar images. Under the chart default
  (`leaderElection.enabled: true`, `values.yaml:87-88`) it waits as a standby and can take over
  whenever the chart's leader restarts. Read, not run.

**Not verified:** ~~`make deploy` was not run, so whether anything else in `config/manager` keeps
the operator from starting is unknown.~~ *(corrected 2026-09-27 at `84a39c2`: read and rendered,
see above; `make deploy` itself was still not run.)* Also not verified: whether
`guidedtraffic/valkey-operator:latest` is pullable, and what a deployed operator with the
fallback sidecar image and an empty observer image does to the data tiers it reconciles; what
would settle it is one `make deploy` on a scratch Kind cluster with the chart installed. Not a
security finding: a binding to a missing ClusterRole grants nothing, and whoever can create a
ClusterRole of that name can bind it anyway. *(Precised 2026-09-27 at `84a39c2`: on a
chart-installed cluster the binding resolves to the chart's ClusterRole for a ServiceAccount in
`valkey-operator-system`, a namespace the operator's administrator already controls; whoever runs
`make deploy` is cluster-admin. Still no security class.)*

**Impact** *(added 2026-09-27 at `84a39c2`)*: on a cluster without the chart `make deploy`
produces an operator that cannot start; on a cluster with it, a second operator that runs a
floating tag with a different sidecar image and can reconcile the fleet. No doc, test or workflow
runs the target.

**Options:** see Options, E1, above. *(Moved there 2026-09-27 at `84a39c2`; the removed options
A and C are in the History entry of that day.)*

**Verification:** ~~A: `kubectl kustomize config/default` renders a `roleRef` equal to the
ClusterRole's name.~~ *(Removed 2026-09-27 at `84a39c2` with option A.)* B: `git grep -n
'config/default\|config/manager\|make deploy'` outside `docs/tickets/` is empty, and
`make generate-all` leaves a clean tree. *(2026-09-27 at `84a39c2`: the grep in Options, E1, which
also covers `KUSTOMIZE`, replaces this one.)*

## History

- 2026-09-27: re-verified at `84a39c2` against an audit and two adversarial reviews of this run.
  **Checked:** every Makefile, test, config, chart, ADR and doc line the ticket cites, by reading;
  `KUBECONFIG=/dev/null kubectl kustomize config/rbac` and `config/default` (kubectl v1.36.2,
  Kustomize v5.8.1) and `KUBECONFIG=/dev/null helm template valkey-operator
  deploy/helm/valkey-operator --namespace valkey-operator-system` (Helm v3.21.3), rendered
  offline into the run's scratch directory; `git log -S` and `git tag --contains` for the history
  claims; Helm v3.21.3 `pkg/action/validate.go` and `install.go` (raw GitHub); `k8s.io/api`
  v0.37.1 `rbac/v1/types.go` and controller-runtime v0.25.1 `leader_election.go` from the module
  cache. No target, test, cluster or container was run.
  **Measured:** `ls test/e2e/bin` - no such directory; `bin/manager` executable;
  `go help testflag` (Go 1.27.1) - the package-directory sentence; `wc -c` of the CRD - 48791;
  the renders as recorded in F1 and E1. Locations re-read at `84a39c2`: `:148` -> `:147`,
  `:154` -> `:153`, `:161` -> `:160` in the Fact, fixed in the links.
  **False or outdated, corrected in place:** the claim that the other cited
  `migrate_e2e_test.go` lines were unchanged after the comment deletion; that the XS items were
  working-tree edits (they are commit `bcc63c9`; the earlier History entry "read in `git diff`
  of the working tree" was true when written and stays); the parenthetical "not the pinned
  v5.8.1" (twice); that the F1 help text is the only rule-1 statement left (ADR 0006:91-93 and
  ADR 0017:1105 are two more); that none of this is an ADR decision (E1's close amends ADR 0014
  D8); E1's "whether anything else keeps the operator from starting is unknown" (it is read now);
  the old Verification wording "with the operator installed".
  **New facts:** the target has carried `./bin/manager` since `73f6efe` and no workflow ever
  named it; the replicas subtest cannot be failed by `migrate`; the test never runs as the hook's
  ServiceAccount, while `TestE2E_FleetUpgrade` runs `migrate` as the hook Job; F1's targets
  collide with the chart's ClusterRoleBinding under the documented release name; Helm refuses a
  later install over a tree-applied CRD unless `--take-ownership` is passed (validate.go:84,
  :94-111; install.go:353); the E1 mismatch comes from `0aaa3a2`, which every tag contains;
  the overlay also lacks a Namespace, the operator image, `POD_NAMESPACE` and a leases rule, and
  can start a second operator on a chart-installed cluster; ADR 0006:91-93 is false in three
  respects and ADR 0017:1105 in one; `testing.md:18`, `DEVELOPER.md:64`, `:147` and
  `package-map.md:104` were missing from the close list.
  **Options removed**, each with its reason:
  - F1 B, "make the help text say RBAC": it would document a target that strips the chart's
    ClusterRoleBinding and applies RBAC that binds nothing; a true help text on a harmful target
    is not a fix, and it duplicates half of `deploy`.
  - Path B, "drop the variable and rely on the test's default `../../bin/manager`": dominated -
    same cost as `$(CURDIR)`, but correctness rests on the depth-tied constant at
    `migrate_e2e_test.go:35`, which never names the Makefile.
  - Path C, "resolve a relative `MANAGER_BINARY` against the module root in the test": its only
    reach beyond `$(CURDIR)` is a direct `go test` call, which the project rules forbid, and it
    adds code to a test helper.
  - Path "wait for T47's option before fixing": not a choice - T47 C would delete the recipe with
    the token in it, so fixing now adds no removal work; only the Kind run is at stake, and it
    can share T47's Kind cluster.
  - F3 B, "make a test read `E2E_TESTS`": the build tag already is the switch; F3 is done.
  - E1 A, "fix the `roleRef`": false premise - the `roleRef` is one of at least five defects
    (no Namespace, no operator image, no `POD_NAMESPACE`, no leases rule), so a fixed `roleRef`
    still yields no running operator, for a path no doc, test or workflow uses.
  - E1 C (considered in the review, never in the ticket), "make the overlay a real second
    install path": speculative scope with no user and M effort. Whether it would also contradict
    ADR 0014 D8 was disputed in the review (D8 governs the one supported upgrade path, and a
    dev-only overlay need not contradict it); the scope reason suffices on its own.
  **Not taken from the audit:** quoting the path as `"$(CURDIR)/bin/manager"` so that a checkout
  path with spaces survives. The Makefile already breaks on such a path (`LOCALBIN ?= $(shell
  pwd)/bin`, `Makefile:26`, is used unquoted in every tool recipe), so the quotes would protect
  nothing; they are harmless, not a reason.
  **Superseded option texts, in substance** (so the earlier analysis is not lost):
  - F1 A as it stood: `install` ran `kubectl apply --server-side -f config/crd/bases`, and
    `--server-side` was noted as optional because the CRD is 48,791 bytes (`wc -c`, measured again
    at `84a39c2`), far below the 262,144-byte annotation limit client-side apply runs into. Its
    open item was a guard refusing any context other than `kind-valkey-operator-test`; its
    unverified item was the Helm ownership refusal, now read in source (appendix F1).
  - F1 C as it stood cost "the recipe, the help and `DEVELOPER.md:214`"; the current cost adds
    `DEVELOPER.md:144` and `:251-252`.
  - The earlier mark "A over C": `make run` is the one local path that needs exactly the CRD, and
    A turns the documented hand step into a target; C was the fallback if the destructive
    `uninstall` was judged not worth it. It lost because the hand step is one documented line and
    A buys it with a cascade delete.
  - The path options were decision 2, ordered after T47 ("under T47's A or B, take this one"),
    with A (`$(CURDIR)`) marked; A is now the decision-free item 3 of the Work list.
  - E1 A cost XS and was rejected then for keeping a second install path next to the chart; the
    earlier E1 B cost list was `DEVELOPER.md:65`, `:144`, `:215` and `package-map.md:105`, with
    the kustomize pin going only "together with F1 A".
  **Recommendation changes:** F1 from A to C - A's `uninstall` is a cascade delete of every
  `Valkey` CR on whatever context is current, and the one CRD command `make run` needs is already
  documented; A2 (new, apply-only `install`) is the runner-up. The binary path is no longer a
  decision and no longer waits on T47. E1 B is unchanged but is now the only option, a go-ahead.
  **Frontmatter:** `state` filed -> analysed (every fact re-verified and every open item has its
  mechanism, weighed options and a justified mark; what remains unverified is the runtime proof);
  `severity` stays low, its comment now names the worst cases; `security` stays none; `urgency`
  stays `now`, the
  comment now names the help texts and the two ADR statements; `effort` stays S;
  `blocked-by` stays decision, the comment now names F1 as the one decision and drops the wait on
  T47.
  **Review of this revision, same day:** every cited line spot-checked again at `84a39c2`
  (Makefile, `migrate_e2e_test.go`, `migrate.go`, the CRD, `valkey_types.go`,
  `fleet_upgrade_test.go`, chart templates and values, `config/`, the ADR and doc lines, Helm
  v3.21.3 `validate.go:84`, `:94-111` and `install.go:114-115`, `:353`, `k8s.io/api` v0.37.1
  `types.go:238-240`, controller-runtime v0.25.1 `leader_election.go:75`, and the `git log`/`git
  tag` claims). Fixed: `main.go:72` -> `:71` for the `--leader-elect` default; F1 C's "RBAC that
  binds nothing" (on a chart cluster it binds the chart's ClusterRole) now reads "their binding
  has missed its own ClusterRole"; E1 B's justification says "upgrade path" as D8 does and
  regains the `rbac_drift_test.go:7` quote the rewrite had dropped; the claim that the History
  records the earlier option texts is now true (above); the note on `Makefile:350-352`; the T47
  bullet notes 047's concurrent revision.
  - Cross-ticket: in the consistency pass of the same day, the T41/T43/T45/T58 bullet was
    corrected (each fixed its `release.yml` citations in its own re-verification), the T35/T57
    bullet precised (035 dropped its sentence, 057's mentions are generic), T30's state
    corrected to `analysed`, and the E1 appendix now carries T53's measurement, re-run here,
    that `ghcr.io/guided-traffic/valkey-operator:latest` does not exist; T45 now records the
    kustomize count change under E1 B and F1 C, and T47 its corrected reading of this ticket.
  Filed: an item this revision had left open for the owner is handled outside this ticket; its
  bullet in the F1 appendix, its Work list item, its clause in Decision and the matching notes in
  the frontmatter and in this entry were removed. `security` stays `none`, now with a neutral
  comment. The F1 collision facts stay, rephrased as the availability facts F1 and E1 rest on:
  `make uninstall` deletes the chart's ClusterRoleBinding, `make install` overwrites it, and
  either way the chart's operator loses its ClusterRole until the next `helm upgrade`.
  No decision, option, severity or urgency of this ticket rested on the removed item.
  - Sweep: F1 Options, appendix F1, Impact and this entry's Filed sentence now say only that `make
    install` overwrites the chart's ClusterRoleBinding; the finer detail of how was removed before
    commit. The availability facts F1 and E1 rest on are unchanged, and no decision, option,
    severity or urgency rested on the removed detail.
  - Final pass: the F1 mechanism under Options and appendix F1 were shortened once more before
    commit - both now say that the two targets act on the chart's binding of the same name, and
    no longer describe the rendered binding's fields or how the apply goes through. The
    availability consequence (the chart's operator loses its ClusterRole until the next
    `helm upgrade`) is unchanged.
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
