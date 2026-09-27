---
id: T44
title: make targets and CI settings that do not do what they say - test-e2e-helm's binary path, install/uninstall, E2E_TESTS, deploy's ClusterRole binding
state: analysed       # every fact read and every open item has weighed options; the runtime proof is outstanding
severity: low         # dev targets that no CI job, test or doc instruction runs; worst case strips or doubles the chart's operator on a cluster a developer targets
security: none        # developer Make targets, a test path and doc lines
urgency: now          # false statements in tracked files: the install/uninstall help at Makefile:355, :359, ADR 0006:91-93 and ADR 0017:1105
effort: S             # deletions, one token, doc lines and three in-place ADR notes
blocked-by: decision  # Q1 is open; Q2 needs the owner's go-ahead; the binary path waits on nothing
filed-from: the documentation restructure of 2026-09-27 (docs/developer/testing.md)
opened: 2026-09-27
decided:              # no decision yet
done:                 # not done
---

# T44 - make targets and CI settings that do not do what they say

## Current state

**`make test-e2e-helm` points its test at a binary that does not exist.**

- [`Makefile:175-178`](../../Makefile): `test-e2e-helm: build`, then
  `MANAGER_BINARY=./bin/manager go test -v -tags=e2e,e2e_helm -count=1 -timeout=10m -run TestE2E_Migrate ./test/e2e/...`.
  `build` writes `bin/manager` at the repository root ([`Makefile:322-324`](../../Makefile)).
- [`migrate_e2e_test.go`](../../test/e2e/migrate_e2e_test.go): `runMigrateBinary` (`:139`) reads
  `MANAGER_BINARY` (`:142`), falls back to `defaultManagerBinary` = `"../../bin/manager"` (`:35`)
  only when it is empty, and runs `exec.Command(binaryPath, "migrate")` (`:147`) without `cmd.Dir`.
  It logs `Running: %s migrate` (`:153`); `TestE2E_MigrateDefaults` fails on any error (`:81`, `:160`).
- `go test` runs the test binary in the package directory, so `./bin/manager` resolves to
  `test/e2e/bin/manager`, which nothing builds (`test/e2e/bin` does not exist; `bin/manager` does).
  The test's own default would reach the right binary; the target overrides it with the wrong one.
  No workflow runs the target, and CI does not even compile the file (T43).
- `DEVELOPER.md` and [`docs/developer/testing.md`](../developer/testing.md) list the target as the way
  to run the Helm-migration e2e. Whoever runs it gets a failure at the migrate step that looks like a
  defect in `manager migrate`.
- What the test adds: only its `secretPasswordKey` assertion depends on `migrate`. `simulateOldCR`
  patches `secretPasswordKey: ""` (`migrate_e2e_test.go:128`), a present empty value structural
  defaulting does not fill, and `applyDefaults` rewrites it
  ([`migrate.go:114-116`](../../cmd/migrate/migrate.go)). The replicas subtest (`:101-116`) asserts
  what the CRD already guarantees (`Minimum=1`,
  [`valkey_types.go:1027-1030`](../../api/v1/valkey_types.go)).
- The test runs `manager migrate` on the host as the kubeconfig user
  ([`migrate.go:46`](../../cmd/migrate/migrate.go), `ctrl.GetConfigOrDie()`), never as the
  pre-upgrade hook's ServiceAccount. `TestE2E_FleetUpgrade` runs `migrate` as the hook Job through a
  real `helm upgrade` ([`fleet_upgrade_test.go:318-323`](../../test/e2e/fleet_upgrade_test.go),
  `:992-994`).
- The help text at `Makefile:176` and [`testing.md:18`](../developer/testing.md) say the test needs a
  cluster with the operator. By reading, a Kind cluster with the CRD is enough: the body only
  creates, patches and reads a CR and runs `migrate` (`migrate_e2e_test.go:42-117`), the package has
  no `TestMain`, and the chart ships no admission webhook.

**`make install` and `make uninstall` do not touch a CRD** ([`Makefile:354-360`](../../Makefile)).

- The help texts promise CRDs; both recipes pipe `$(KUSTOMIZE) build config/rbac` into
  `kubectl apply` / `kubectl delete`. `config/crd/` has no `kustomization.yaml`, so no target
  installs the CRD.
- `config/rbac` renders a ServiceAccount in namespace `system`
  ([`service_account.yaml:5`](../../config/rbac/service_account.yaml)), which nothing creates, a
  ClusterRoleBinding `valkey-operator` whose `roleRef` is ClusterRole `valkey-operator`
  ([`role_binding.yaml:7-8`](../../config/rbac/role_binding.yaml)), and the generated ClusterRole
  `valkey-operator-role` ([`Makefile:404`](../../Makefile)). The RBAC does not fit together.
- Under the documented release name `valkey-operator` (`make e2e-local`, `Makefile:218`; CI,
  `release.yml:353`; [`installation.md:18`, `:28`](../operations/installation.md)) the chart renders
  a ClusterRole and a ClusterRoleBinding of that same name. Both targets therefore act on the chart's
  binding, and the chart's operator loses its ClusterRole until the next `helm upgrade` and stops
  reconciling on permission errors. Nothing guards the kubeconfig context.
- `make run` ([`Makefile:326-328`](../../Makefile)) needs only the CRD, and
  [`DEVELOPER.md:251-253`](../../DEVELOPER.md) already gives the one-line `kubectl apply` for it.
  `DEVELOPER.md:144` and `:214` describe the targets; no doc, test or workflow runs them.
- A `helm install` over an existing object without Helm's ownership metadata fails with "exists and
  cannot be imported into the current release" unless `--take-ownership` is passed; the tree CRD
  carries no such metadata ([`vko.gtrfc.com_valkeys.yaml:1-7`](../../config/crd/bases/vko.gtrfc.com_valkeys.yaml)).

**`make deploy` produces no working operator** ([`Makefile:362-369`](../../Makefile)).

- `deploy` runs `kustomize edit set image` in `config/manager`, rewriting the tracked
  `config/manager/kustomization.yaml`, then applies `config/default`; `undeploy` deletes it.
- The render's defects: `namePrefix` renames the ClusterRole to
  `valkey-operator-valkey-operator-role` but the `roleRef` stays `valkey-operator`; no Namespace
  object for `valkey-operator-system`; no `--operator-image`/`OPERATOR_IMAGE`, so the sidecar falls
  back to `ghcr.io/guided-traffic/valkey-operator:latest`
  ([`statefulset.go:989-992`](../../internal/builder/statefulset.go)), which does not exist, and the
  observer gets an empty image ([`observer.go:82`](../../internal/builder/observer.go)); no
  `POD_NAMESPACE` ([`main.go:173`](../../cmd/main.go)); and no `leases` rule in `role.yaml` although
  the Deployment passes `--leader-elect` (the rule is chart-only by design,
  [`clusterrole.yaml:180`](../../deploy/helm/valkey-operator/templates/clusterrole.yaml)).
- Impact: on a cluster without the chart the operator cannot start. On a cluster with the chart, the
  unrenamed `roleRef` binds to the chart's ClusterRole and a second operator runs in the chart's
  namespace with the same `LeaderElectionID` ([`main.go:104-108`](../../cmd/main.go)); on the
  `make e2e-local` cluster (chart leader election off) both reconcile every `Valkey` CR with
  different sidecar images. Whoever runs `make deploy` is cluster-admin, so it has no security class.

**Two ADRs state false facts about this tooling.**

- [`0006-delete-only-what-the-operator-owns.md:91-93`](../adr/0006-delete-only-what-the-operator-owns.md)
  says `9e5634d` is branch-only and that the kubebuilder marker and `config/rbac/role.yaml` carried
  the verb all along, so a kustomize install had it. `9e5634d` is in `v1.11.0` and every later tag;
  `role.yaml` carried `secrets: get, list, watch` until `ee217dd` added `delete`; and no overlay this
  repository ships ever bound a ClusterRole carrying the verb. The sentence holds only for a
  user-built overlay that binds `role.yaml` correctly.
- [`0017-test-and-ci-policy.md:1105`](../adr/0017-test-and-ci-policy.md) names kustomize as a
  generator tool whose bump blocks its own automerge. `generate-all` runs controller-gen only
  (`Makefile:331`, `:403-405`), the `generated-manifests` job runs only `make generate-all`, and no
  workflow or Renovate rule names kustomize.
- `DEVELOPER.md:64`, `:214` and [`package-map.md:104`](../developer/package-map.md) call
  `role_binding.yaml` the binding of `role.yaml`; its `roleRef` names `valkey-operator`, not
  `valkey-operator-role`.

## Required changes

### Independent of the open questions

1. `Makefile:178`: `MANAGER_BINARY=$(CURDIR)/bin/manager`. `$(CURDIR)` is where `build` writes;
   not `$(LOCALBIN)`, which is `?=`-overridable (`Makefile:26`) while `build` hardcodes `bin/manager`.
   In the same change: [`testing.md:108`](../developer/testing.md) names the new path, the
   `testing.md:182-186` bullet about the defect is deleted, and `DEVELOPER.md:189` is updated.
2. After the Kind run: `Makefile:176` and `testing.md:18` say "CRD" or "operator" according to what
   the run showed. Run: `make kind-create`, `kubectl apply -f config/crd/bases/`, `make test-e2e-helm`
   (can share T47's Kind cluster).
3. ADR 0006:91-93: in-place note stating the facts above, the old sentence marked in place.
4. ADR 0017:1105: drop "kustomize" from the parenthesis, marked in place.

### Depends on the answers

5. Q1 = C: delete `Makefile:354-360`; `DEVELOPER.md:144` (drop `install`), `:214` (drop the row),
   `:251-252` (drop "see `install` above").
6. Q2 = yes: delete `deploy`/`undeploy` (`Makefile:362-369`), `config/default/`, `config/manager/`,
   `config/rbac/kustomization.yaml`, `role_binding.yaml`, `service_account.yaml` and the kustomize pin
   (`Makefile:36`, `:45-46`, `:388-391`); keep `config/rbac/role.yaml` (read by
   `TestHelmClusterRoleCoversGeneratedRole`, [`rbac_drift_test.go:34`](../../internal/controller/rbac_drift_test.go)).
   Docs: `DEVELOPER.md:64`, `:65`, `:144`, `:147`, `:215`,
   [`package-map.md:104-105`](../developer/package-map.md). Amend ADR 0014 D8 in place: the chart is
   the only install path this repository ships, the kustomize overlays are retired, and "the
   kustomize path" in ADR 0005, 0007 and 0032 means a user-built overlay.
7. Once both `uninstall` and `undeploy` are gone: delete `Makefile:350-352` (the `ignore-not-found`
   default). The "its binding" wording goes with items 5 and 6.

### Verification

- `make -n test-e2e-helm` prints `MANAGER_BINARY=<repo>/bin/manager`; `test -x bin/manager` holds
  after `make build`.
- Kind run: `make test-e2e-helm` passes and logs `Running: <absolute path>/bin/manager migrate`
  (`migrate_e2e_test.go:153`). Revert check: with `./bin/manager` restored in a scratch copy the same
  run fails at `runMigrateBinary` with "no such file or directory".
- Q1 C: `grep -nE '^(install|uninstall):' Makefile` is empty.
- Q2: `git grep -n 'config/default\|config/manager\|KUSTOMIZE' -- ':!docs/tickets'` is empty, and
  `make generate-all` leaves a clean tree.
- ADR notes: `git grep -n 'branch-only' docs/adr/0006-delete-only-what-the-operator-owns.md` and
  `git grep -n 'controller-tools, kustomize' docs/adr/0017-test-and-ci-policy.md` find only
  struck-through text.

## Open questions

### Q1: What should `make install` and `make uninstall` do?

They apply and delete RBAC that collides with the chart's ClusterRoleBinding, while their help
promises CRDs. `make run` is the only local path that needs the CRD, and its one-line
`kubectl apply` is already documented. The choice does not touch the chart, `role.yaml` or operator
behaviour.

- **A - make the help true** (`install` applies `config/crd/bases/`, `uninstall` deletes it): two
  recipe lines and doc lines. `uninstall` deletes the CRD and with it every `Valkey` CR on the current
  context (non-persistent datasets gone); `install` writes the branch's CRD over a Helm-owned one, and
  a later `helm install` over a CRD `install` created needs `--take-ownership`.
- **A2 - `install` applies the CRD, `uninstall` is deleted**: removes the cascade delete, keeps the
  out-of-band CRD write on whatever context is current and the `--take-ownership` case.
- **C - delete both targets** (recommended): zero code, no Make target writes RBAC or a CRD, `make run`
  keeps the documented `kubectl apply`.

C is recommended because the targets have no consumer (no doc, test or workflow runs them), and it
removes the collision without adding a command that writes or cascade-deletes a CRD out of band.

**Answer:** _open_

### Q2: May the kustomize overlays (`make deploy`/`undeploy`, `config/default`, `config/manager`) be retired?

The overlay has at least five defects and yields no working operator; the chart is the one supported
install and upgrade path (ADR 0014 D8), and CI and `make e2e-local` install only the chart. Retiring
it is a durable decision that amends ADR 0014 D8, which is why it needs the owner's go-ahead. Fixing
only the `roleRef` leaves the other four defects.

- **Yes - retire it** (recommended): item 6 above, effort S. `role.yaml` and the drift test stay, and
  no second install path can drift from the chart.

Recommended because no doc, test or workflow uses the path and the chart already covers every
install.

**Answer:** _open_

## Not verified

- That `make test-e2e-helm` fails today (expected: `fork/exec ./bin/manager: no such file or
  directory`), that the rest of the test passes once the binary is found, and that no operator is
  needed. The Kind run in item 2 settles all three.
- The runtime effect of `install`/`uninstall` on the chart's binding and of `make deploy` on a
  chart-installed cluster (read and rendered, not run). One run of each target on a scratch Kind
  cluster with the chart installed would settle it; unnecessary if Q1 = C and Q2 = yes.

## Related

- T47: its option C deletes this target and `TestE2E_MigrateDefaults`; the Kind run can share its
  cluster. A working `test-e2e-helm` does not measure the hook's RBAC (kubeconfig user);
  `TestE2E_FleetUpgrade` does.
- T43: lint and vet skip build-tagged files, so `migrate_e2e_test.go` is not compiled in CI.
- T45: its count of Makefile lines the Renovate manager matches drops from 6 to 5 under Q2.
- T35, T57: "the kustomize path" stays valid for user-built overlays but should not cite
  `config/default`.
- T30: embargoed security finding.
