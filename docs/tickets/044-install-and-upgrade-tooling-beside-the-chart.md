---
id: T44
title: install and upgrade tooling beside the chart - test-e2e-helm, install/uninstall, the kustomize overlays and the migrate hook with its defaulting rule and grant
state: analysed       # every fact read and every open item has weighed options; nothing decided, runtime proofs outstanding
severity: low         # dev targets nobody runs, a missing contributor rule, a blocking upgrade step with nothing to do, an unused grant
security: hardening   # the hook's cluster-wide CRD grant; the Make targets and overlays have no security class
threat: "every helm upgrade gives the pre-upgrade hook ServiceAccount cluster-wide customresourcedefinitions get/list/patch/update, held by no other chart principal and unused by the hook's code, usable by the hook's operator image and by anyone who may create pods in the release namespace for the life of the hook Job, and after a failed hook until the next upgrade"
urgency: now          # tracked files state what the code contradicts: Makefile install/uninstall help, ADR 0006, ADR 0017, ADR 0021 D7 and the hook's descriptions in DEVELOPER.md and upgrading.md
effort: M             # driven by retiring the hook (Q3 = C); everything else is S
blocked-by: decision  # Q1-Q4 open; Q2 and Q3 amend ADRs and need the owner
filed-from: the documentation restructure of 2026-09-27 (docs/developer/testing.md)
opened: 2026-09-27
decided:              # no decision yet
done:                 # not done
---

# T44 - install and upgrade tooling beside the chart - test-e2e-helm, install/uninstall, the kustomize overlays and the migrate hook with its defaulting rule and grant

**Scope.** The chart is the one supported install and upgrade path (ADR 0014 D8), yet the repository
carries Make targets, kustomize overlays and an upgrade hook around it whose descriptions do not match
what they do, and one of which holds a privilege nothing uses. Deciding them together settles which
of these tools survive at all, so the fixes to the survivors are not wasted work.

- Make targets and overlays beside the chart: `make test-e2e-helm`, `make install`/`uninstall`,
  `make deploy`/`undeploy` with `config/default` and `config/manager`, and two ADR statements about
  this tooling.
- The chart's pre-upgrade migrate hook: no rule for what belongs in `applyDefaults`, a blocking
  upgrade step that has nothing to do, and its grant.

## Current state

### The Helm-migration e2e (`make test-e2e-helm`, `TestE2E_Migrate*`)

- [`Makefile:175-178`](../../Makefile): `test-e2e-helm: build`, then
  `MANAGER_BINARY=./bin/manager go test -v -tags=e2e,e2e_helm -count=1 -timeout=10m -run TestE2E_Migrate ./test/e2e/...`;
  `build` writes `bin/manager` at the repository root ([`Makefile:322-324`](../../Makefile)).
- [`migrate_e2e_test.go`](../../test/e2e/migrate_e2e_test.go): `runMigrateBinary` (`:139`) reads
  `MANAGER_BINARY` (`:142`), falls back to `defaultManagerBinary` = `"../../bin/manager"` (`:35`) only
  when empty, runs `exec.Command(binaryPath, "migrate")` (`:147`) without `cmd.Dir`, logs
  `Running: %s migrate` (`:153`); `TestE2E_MigrateDefaults` fails on any error (`:81`, `:160`).
- `go test` runs in the package directory, so `./bin/manager` resolves to `test/e2e/bin/manager`,
  which nothing builds. The test's own default would work; the target overrides it with the wrong
  path. `DEVELOPER.md` and [`docs/developer/testing.md`](../developer/testing.md) list the target as
  the way to run the test; whoever runs it gets a failure that looks like a defect in
  `manager migrate`.
- The test runs in no CI workflow, CI does not compile the file (T43), and it runs `migrate` on the
  host as the kubeconfig user ([`migrate.go:46`](../../cmd/migrate/migrate.go), `ctrl.GetConfigOrDie()`),
  never as the hook ServiceAccount, so it cannot verify the hook's role. Only its
  `secretPasswordKey` assertion depends on `migrate`: `simulateOldCR` fabricates an explicit
  `secretPasswordKey: ""` (`:128`), which structural defaulting keeps. The replicas subtest
  (`:101-116`) asserts what the CRD guarantees (`Minimum=1`,
  [`valkey_types.go:1027-1030`](../../api/v1/valkey_types.go)).
- `Makefile:176` and [`testing.md:18`](../developer/testing.md) say it needs a cluster with the
  operator; by reading, a Kind cluster with the CRD is enough (the body only creates, patches and
  reads a CR and runs `migrate`, `:42-117`; no `TestMain`; no admission webhook in the chart).

### `make install` and `make uninstall`

- [`Makefile:354-360`](../../Makefile): the help promises CRDs; both recipes pipe
  `$(KUSTOMIZE) build config/rbac` into `kubectl apply` / `kubectl delete`. `config/crd/` has no
  `kustomization.yaml`, so no target installs the CRD.
- `config/rbac` renders a ServiceAccount in namespace `system`
  ([`service_account.yaml:5`](../../config/rbac/service_account.yaml)), which nothing creates, a
  ClusterRoleBinding `valkey-operator` with `roleRef` ClusterRole `valkey-operator`
  ([`role_binding.yaml:7-8`](../../config/rbac/role_binding.yaml)), and the generated ClusterRole
  `valkey-operator-role` ([`Makefile:404`](../../Makefile)); the RBAC does not fit together.
  `DEVELOPER.md:64`, `:214` and [`package-map.md:104`](../developer/package-map.md) call
  `role_binding.yaml` the binding of `role.yaml`, which its `roleRef` contradicts.
- Under the documented release name `valkey-operator` (`make e2e-local`, `Makefile:218`;
  `release.yml:353`; [`installation.md:18`, `:28`](../operations/installation.md)) the chart renders a
  ClusterRole and ClusterRoleBinding of that same name, so both targets act on the chart's binding:
  the chart's operator loses its ClusterRole until the next `helm upgrade`. Nothing guards the
  kubeconfig context.
- `make run` ([`Makefile:326-328`](../../Makefile)) needs only the CRD, and
  [`DEVELOPER.md:251-253`](../../DEVELOPER.md) gives the one-line `kubectl apply` for it.
  `DEVELOPER.md:144`, `:214` describe the targets; no doc, test or workflow runs them.
- A `helm install` over an object without Helm ownership metadata fails ("cannot be imported into the
  current release") unless `--take-ownership` is passed; the tree CRD carries none
  ([`vko.gtrfc.com_valkeys.yaml:1-7`](../../config/crd/bases/vko.gtrfc.com_valkeys.yaml)).

### The kustomize overlays (`make deploy`, `make undeploy`)

- [`Makefile:362-369`](../../Makefile): `deploy` runs `kustomize edit set image` in `config/manager`,
  rewriting the tracked `config/manager/kustomization.yaml`, then applies `config/default`;
  `undeploy` deletes it.
- Five defects in the render: `namePrefix` renames the ClusterRole to
  `valkey-operator-valkey-operator-role` but the `roleRef` stays `valkey-operator`; no Namespace
  object for `valkey-operator-system`; no `--operator-image`/`OPERATOR_IMAGE`, so the sidecar falls
  back to the non-existent `ghcr.io/guided-traffic/valkey-operator:latest`
  ([`statefulset.go:989-992`](../../internal/builder/statefulset.go)) and the observer gets an empty
  image ([`observer.go:82`](../../internal/builder/observer.go)); no `POD_NAMESPACE`
  ([`main.go:173`](../../cmd/main.go)); no `leases` rule although `--leader-elect` is passed (the rule
  is chart-only by design, [`clusterrole.yaml:180`](../../deploy/helm/valkey-operator/templates/clusterrole.yaml)).
- Impact: without the chart the operator cannot start; with the chart the unrenamed `roleRef` binds
  the chart's ClusterRole and a second operator runs in the chart's namespace with the same
  `LeaderElectionID` ([`main.go:104-108`](../../cmd/main.go)); on the `make e2e-local` cluster
  (chart leader election off) both reconcile every `Valkey` with different sidecar images. Whoever
  runs `make deploy` is cluster-admin, so this has no security class.
- CI and `make e2e-local` install only the chart.

### ADR statements about this tooling

- [`0006-delete-only-what-the-operator-owns.md:91-93`](../adr/0006-delete-only-what-the-operator-owns.md)
  says `9e5634d` is branch-only and that the marker and `config/rbac/role.yaml` carried the verb all
  along, so a kustomize install had it. `9e5634d` is in `v1.11.0` and every later tag; `role.yaml`
  carried `secrets: get, list, watch` until `ee217dd` added `delete`; no overlay this repository
  ships ever bound a ClusterRole carrying the verb. It holds only for a user-built overlay.
- [`0017-test-and-ci-policy.md:1105`](../adr/0017-test-and-ci-policy.md) names kustomize as a
  generator whose bump blocks its own automerge; `generate-all` runs controller-gen only
  (`Makefile:331`, `:403-405`), and no workflow or Renovate rule names kustomize.

### The migrate hook and its defaulting rule

- **What it does.** The pre-upgrade Job
  ([`pre-upgrade-job.yaml:11-13`](../../deploy/helm/valkey-operator/templates/pre-upgrade-job.yaml#L11-L13),
  weight `-5`, `backoffLimit: 3` at `:15`, on by default at
  [`values.yaml:153`](../../deploy/helm/valkey-operator/values.yaml#L153)) runs `./manager migrate`
  (`:36`, dispatch [`cmd/main.go:137-141`](../../cmd/main.go#L137-L141)). It lists every `Valkey`
  cluster-wide ([`migrate.go:55`](../../cmd/migrate/migrate.go#L55)), merge-patches each where
  `applyDefaults` ([`:104-146`](../../cmd/migrate/migrate.go#L104-L146)) reports a change (`:86`),
  and exits 1 on any failure (`:66-69`); Helm then fails the release, so the hook blocks every upgrade.
- **What `applyDefaults` writes**, each only when empty and its parent set: `spec.replicas` 0 -> 1;
  `auth.secretPasswordKey` `""` -> `password`; `tls.certManager.issuer.group` `""` ->
  `cert-manager.io`; `sentinel.replicas` 0 -> 3; `persistence.mode` `""` -> `rdb`;
  `persistence.size` zero -> `1Gi`. `TestMigrateAll_UpToDateCRIsNotPatched`
  ([`migrate_all_test.go:98`](../../cmd/migrate/migrate_all_test.go#L98)) asserts a CR with nothing
  empty is not patched.
- **The CRD already defaults all six**, with `+kubebuilder:default` markers of the same value in
  [`valkey_types.go`](../../api/v1/valkey_types.go) (`:1029`, `:570`, `:578`, `:481`, `:999`,
  `:1008`), and `templates/crd.yaml` carries them with the same values in all 104 release tags (plus
  `Minimum=1` on both `replicas`, enum `rdb;aof;both`, no `minLength` on the two strings). The API
  server defaults absent keys on every request and read and keeps an explicit `""` or `0`
  (`apiextensions-apiserver@v0.37.1`: `customresource_handler.go:1185-1195`, `:1289-1297`;
  `schema/defaulting/algorithm.go:45`). So the hook changes only a CR stored with an explicit empty
  value, and each such case is neutral or already broken:

  | Stored value | Effect today | Effect of the hook's patch |
  |---|---|---|
  | `size: "0"` | builder falls back to `1Gi` ([`statefulset.go:1191-1195`](../../internal/builder/statefulset.go#L1191-L1195)) | none |
  | `issuer.group: ""` | left out of `issuerRef` ([`certificate.go:183`](../../internal/builder/certificate.go#L183-L184), `:227`) | adds `cert-manager.io`, which cert-manager treats as equal |
  | `secretPasswordKey: ""` with `secretName` | the API server refuses the empty `secretKeyRef` key; the data StatefulSet write is blocked | unblocks it, only at the next upgrade |
  | `secretPasswordKey: ""` without `secretName` | auth disabled ([`valkey_types.go:1171-1173`](../../api/v1/valkey_types.go#L1171-L1173)) | none |

  `replicas`, `sentinel.replicas` and `persistence.mode` cannot be stored empty. Typed clients: the
  two strings are `omitempty`, so a typed write never stores `""`; `Size` is a `resource.Quantity`,
  so a typed persistence block without a size stores `size: "0"`; `ValkeySpec.Resources` is a
  non-pointer struct, so the operator's first full `r.Update` stores `resources: {}` and bumps
  `metadata.generation`.
- **A line for a new field cannot act in its own release.** A `pre-upgrade` hook runs before any
  release resource is updated, the CRD ships as a template (no `crds/`) without
  `x-kubernetes-preserve-unknown-fields`, and `client.New` uses field validation `Warn`, so the patch
  of an unknown field is pruned silently. [ADR 0032](../adr/0032-generated-pods-run-rootless.md)
  (`:419-423`) already rejected a hook-based default for that reason.
- **No rule.** [DEVELOPER.md](../../DEVELOPER.md#adding-things) CRD-field step 6 tells a contributor
  to check `applyDefaults`; no ADR says what belongs there.
- **Coverage.** `TestE2E_FleetUpgrade` ([`fleet_upgrade_test.go`](../../test/e2e/fleet_upgrade_test.go),
  local Kind only, `make e2e-fleet-upgrade-local`) runs the real hook through `helm upgrade`
  (`:318-323`, `:992-994`) and asserts success (`requirePreUpgradeHookSucceeded`, `:848-884`), but
  its fleet sets every field, so the hook patches nothing. No integration test asserts any of the six
  defaults.
- **Tracked statements the code contradicts:** [`DEVELOPER.md:46-47`](../../DEVELOPER.md), `:348-350`
  (the hook "writes field defaults into every existing CR");
  [`upgrading.md:149-153`](../operations/upgrading.md) (the hook ensures the operator "never
  reconciles a CR that predates its defaults" - that is read-time defaulting of the new CRD; and the
  Job name `valkey-operator-pre-upgrade`); ADR 0013 D10 (`:296`), `privilege-footprint.md:127`,
  `trust-boundaries.md:13` (`<release>-upgrade`); [`migrate.go:2-3`](../../cmd/migrate/migrate.go#L2-L3),
  `:40-41`, `fleet_upgrade_test.go:331-335`, `migrate_e2e_test.go:7`, `:39`, `:60-62`, `:119-120`,
  `:125` (defaults "introduced in the current version", CRs that "predate" them - the tag scan
  refutes both); [ADR 0021](../adr/0021-per-resource-metrics-and-the-alert-that-was-missing.md) D7
  (`:125-127`, the hook's absence "breaks ... the upgrade path").
- **Object names.** `<fullname>-upgrade` (ServiceAccount, ClusterRole, ClusterRoleBinding) and
  `<fullname>-pre-upgrade` (Job); `<fullname>` is `<release>-valkey-operator` unless the release name
  contains the chart name ([`_helpers.tpl:11-22`](../../deploy/helm/valkey-operator/templates/_helpers.tpl#L11-L22)).

### The hook's grant

- [`pre-upgrade-rbac.yaml`](../../deploy/helm/valkey-operator/templates/pre-upgrade-rbac.yaml) grants
  cluster-wide `valkeys: get,list,patch,update` (`:30-39`) and `customresourcedefinitions:
  get,list,patch,update` (`:42-50`); the comment at `:17` still says "read+patch access to Valkey CRs
  and the CRD itself". The code needs `valkeys: list, patch` plus discovery (`system:discovery`).
- The operator ServiceAccount in the same namespace holds a superset of the `valkeys` rule
  ([`clusterrole.yaml:9-20`](../../deploy/helm/valkey-operator/templates/clusterrole.yaml#L9-L20));
  no other chart principal holds `customresourcedefinitions`. The delete policy
  `hook-succeeded,before-hook-creation` (`:15`, `:28`, `:63`, `pre-upgrade-job.yaml:13`) has no
  `hook-failed`, so after a failed hook the RBAC objects, the Job and its pods stay. Live today with
  default values.

## Required changes

### Independent of the open questions

1. **Hook statements**: correct those listed above (deleted with the hook instead under Q3 = C).
   `DEVELOPER.md`: the hook fills any of six fields stored empty. `upgrading.md`: the guarantee comes
   from Helm applying the CRD template before the Deployment plus read-time defaulting; Job name
   `<fullname>-pre-upgrade`. Names `<fullname>-upgrade`. ADR 0021 D7: the hook's absence does not
   break the upgrade path (read in source; say so, or land after item 3).
2. **ADR notes**, marked in place: ADR 0006:91-93 states the facts above; ADR 0017:1105 drops
   "kustomize" from the parenthesis.
3. **Envtest defaults assertion** (ADR 0017 D14) in `test/integration`: a `Valkey` created with the six
   fields absent (parents present) is stored with `1`, `password`, `cert-manager.io`, `3`, `rdb`,
   `1Gi`; an explicit `secretPasswordKey: ""` is stored as `""`. Raw JSON (unstructured create or
   `client.RawPatch`, as [`pod_hardening_test.go:101-104`](../../test/integration/pod_hardening_test.go#L101-L104)),
   read from the write's response (cache-backed client). Removing one marker must fail it.
4. **Fleet-upgrade defaults read** in `TestE2E_FleetUpgrade`: a member created through the dynamic
   client with `sentinel` without `replicas`, `persistence` without `mode`/`size`,
   `tls.certManager.issuer` without `group`, `auth: {}`, explicit `spec.replicas`; read back before
   `helm upgrade` and assert `3`, `rdb`, `1Gi`, `cert-manager.io`, `password`. Do not assert
   `metadata.generation` unchanged (the `resources: {}` bump).
5. **Owner, read-only**: list production CRs holding an explicit empty value, e.g.
   `kubectl get valkeys -A -o json | jq -r '.items[] | select(.spec.auth.secretPasswordKey=="" or .spec.tls.certManager.issuer.group=="" or ((.spec.persistence.size // "x")|tostring)=="0") | .metadata.namespace+"/"+.metadata.name'`.
   Expected empty; a hit with `auth.secretName` set is a blocked CR to fix in Git.

Items 3-5 are preconditions of Q3 under either answer.

### Depends on the answers

6. **`test-e2e-helm` binary path** (only under Q3 = A; under C the target and the test are deleted):
   `Makefile:178`: `MANAGER_BINARY=$(CURDIR)/bin/manager` (not `$(LOCALBIN)`, which is
   `?=`-overridable at `Makefile:26` while `build` hardcodes `bin/manager`); same change:
   `testing.md:108` names the path, the `testing.md:182-186` defect bullet goes, `DEVELOPER.md:189`
   updated. Then one Kind run (`make kind-create`, `kubectl apply -f config/crd/bases/`,
   `make test-e2e-helm`) decides whether `Makefile:176` and `testing.md:18` say "CRD" or "operator".
7. **Q1 = C**: delete `Makefile:354-360`; `DEVELOPER.md:144` (drop `install`), `:214` (drop the row),
   `:251-252` (drop "see `install` above").
8. **Q2 = yes**: delete `deploy`/`undeploy` (`Makefile:362-369`), `config/default/`,
   `config/manager/`, `config/rbac/kustomization.yaml`, `role_binding.yaml`, `service_account.yaml`
   and the kustomize pin (`Makefile:36`, `:45-46`, `:388-391`); keep `config/rbac/role.yaml` (read by
   `TestHelmClusterRoleCoversGeneratedRole`, [`rbac_drift_test.go:34`](../../internal/controller/rbac_drift_test.go)).
   Docs: `DEVELOPER.md:64`, `:65`, `:144`, `:147`, `:215`,
   [`package-map.md:104-105`](../developer/package-map.md). Amend ADR 0014 D8 in place: the chart is
   the only shipped install path, the overlays are retired, "the kustomize path" in ADR 0005, 0007
   and 0032 means a user-built overlay. Once both `uninstall` and `undeploy` are gone (Q1 = C and
   Q2 = yes) also delete `Makefile:350-352` (the `ignore-not-found` default); the "its binding"
   wording goes with items 7 and 8.
9. **Q3 = C (retire the hook)**:
   - New ADR: defaults are schema markers; a default that needs code is a read-time getter or
     fallback (as `GetSyncTimeout`, `GetObserverResources`, `GetSeccompProfile`,
     `buildVolumeClaimTemplates`); nothing writes defaults into stored CRs on upgrade. Supersede ADR
     0013 D10, close its residual risk (`:580-581`) and
     [H-3](../security/privilege-footprint.md#h-3); amend ADR 0021 D7, ADR 0017 D34 (`:634-637`) and
     D55 (`:1039`). DEVELOPER.md step 6 cites the ADR.
   - Remove `pre-upgrade-job.yaml`, `pre-upgrade-rbac.yaml`, `preUpgradeHook` in
     `values.yaml:147-164` (mentions at `:11`, `:16`), `cmd/migrate/` with its tests, the import and
     dispatch in `cmd/main.go` (`:18`, `:137-141`), `test/e2e/migrate_e2e_test.go`, `test-e2e-helm`
     (`Makefile:175-178`). Same change, or the tree breaks: in `fleet_upgrade_test.go` remove
     `requirePreUpgradeHookSucceeded` (`:848-884`) and the comments at `:18-19`, `:85`, `:331-336`;
     keep item 4.
   - Keep `./manager migrate` as a subcommand that logs its retirement and exits 0; without it `main`
     falls through to `flag.Parse` ([`cmd/main.go:151-157`](../../cmd/main.go#L151-L157)) and starts
     a full operator, so an older chart running a newer image via `image.tag` fails its upgrade.
   - Documents: `README.md:153`, `:551`, `:559`, `:578-580`; `DEVELOPER.md:20-24`, `:46-47`, `:72`,
     `:189`, `:348-350`; `upgrading.md:149-153`, `installation.md:62`; `testing.md:18`, `:108`,
     `:182`; `package-map.md:21`, `:95`, `:106`; `architecture.md:88`; `privilege-footprint.md:3`,
     `:125-134`, `:162-178`; `trust-boundaries.md:13`; `operator-pod-posture.md:3`, `:11`;
     `_helpers.tpl:80`; ADR 0013 `:49`, `:230`, `:296-304`, `:580-581`, `:600`; ADR 0014 `:27`,
     `:181`; ADR 0017 `:636`, `:926`, `:1039`; ADR 0021 `:126`; ADR 0033 `:151`, `:278`; ADR 0035
     `:80`; ADR 0036 `:137`, `:157`. `CLAUDE.md:946` needs the owner. Leave ADR 0030 `:255`, `:526`
     ("pre-upgrade Sentinel pod") and `cmd/main_test.go` alone.
   - `upgrading.md` upgrade note: Helm does not delete hook objects the chart stops rendering, so after
     a failed last hook `<fullname>-upgrade` (ServiceAccount, ClusterRole, ClusterRoleBinding) and the
     Job `<fullname>-pre-upgrade` with its pods need manual cleanup. Leftover `preUpgradeHook` values
     keep working (no `values.schema.json`).
10. **Q3 = A (keep the hook)**: new ADR (or ADR 0005 amendment): a marker default gets no line; a line
    only for a value computed from other fields or cluster state that must be persisted. The six
    lines are dropped or kept with a note; DEVELOPER.md step 6 cites the ADR.
11. **Q4 = G1 (narrow now)**: in `pre-upgrade-rbac.yaml:30-50` keep only `valkeys: list, patch`; fix
    the comment at `:17`; amend ADR 0013 D10 and close its CRD residual-risk bullet; update
    `privilege-footprint.md` (hook section, H-3) and `trust-boundaries.md:13`.

**Closing (ADR 0034):** the rules in ADRs, the operator-visible consequence in `upgrading.md` and the
security pages, the contributor rule in DEVELOPER.md step 6; then archive this file.

### Verification

- Item 6: `make -n test-e2e-helm` prints `MANAGER_BINARY=<repo>/bin/manager`; the Kind run passes and
  logs `Running: <absolute path>/bin/manager migrate`; with `./bin/manager` restored in a scratch
  copy the same run fails at `runMigrateBinary` with "no such file or directory".
- Q1 = C: `grep -nE '^(install|uninstall):' Makefile` is empty. Q2: `git grep -n
  'config/default\|config/manager\|KUSTOMIZE' -- ':!docs/tickets'` is empty and `make generate-all`
  leaves a clean tree.
- Item 2: `git grep -n 'branch-only' docs/adr/0006-delete-only-what-the-operator-owns.md` and
  `git grep -n 'controller-tools, kustomize' docs/adr/0017-test-and-ci-policy.md` find only
  struck-through text.
- Q3 = C: `helm template` renders no hook object; `make e2e-fleet-upgrade-local` green with item 4
  and without the hook assertion; `./manager migrate` exits 0; `make test-unit`, `make lint`,
  `make generate-all` leave a clean tree.
- Q4 = G1: `helm template` renders exactly `valkeys: list, patch` and no `apiextensions.k8s.io` rule;
  one `make e2e-fleet-upgrade-local` run (shared with item 4) with one fleet CR seeded by raw merge
  patch with `spec.auth: {secretPasswordKey: ""}` and no `secretName`, read back as `""` just before
  `helm upgrade` and as `password` after it, `requirePreUpgradeHookSucceeded` green (the Job log
  cannot be read: `hook-succeeded` deletes the Job).

## Open questions

### Q1: What should `make install` and `make uninstall` do? (install/uninstall)

They apply and delete RBAC that collides with the chart's ClusterRoleBinding, while their help
promises CRDs; `make run` is the only local path needing the CRD, and its `kubectl apply` is already
documented. The choice touches neither the chart, `role.yaml` nor operator behaviour.

- **A - make the help true** (`install` applies `config/crd/bases/`, `uninstall` deletes it): two
  recipe lines and doc lines; `uninstall` cascade-deletes every `Valkey` CR on the current context
  (non-persistent datasets gone), `install` writes the branch's CRD over a Helm-owned one, and a later
  `helm install` over it needs `--take-ownership`.
- **A2 - `install` applies the CRD, `uninstall` is deleted**: no cascade delete, but keeps the
  out-of-band CRD write on whatever context is current and the `--take-ownership` case.
- **C - delete both targets (recommended)**: zero code, no Make target writes RBAC or a CRD.

C, because the targets have no consumer and it removes the collision without adding a command that
writes or cascade-deletes a CRD out of band.

**Answer:** _open_

### Q2: May the kustomize overlays (`make deploy`/`undeploy`, `config/default`, `config/manager`) be retired? (overlays)

The overlay has five defects and yields no working operator; the chart is the one supported path
(ADR 0014 D8) and the only one CI and `make e2e-local` install. Retiring it amends ADR 0014 D8, hence
the owner's go-ahead; fixing only the `roleRef` leaves four defects.

- **Yes - retire it (recommended)**: item 8, effort S; `role.yaml` and the drift test stay.
- **No - repair it**: all five defects, and a second install path that can drift from the chart.

Yes, because no doc, test or workflow uses the path and the chart already covers every install.

**Answer:** _open_

### Q3: Does the migrate hook stay, and which rule governs `applyDefaults`? (migrate hook)

The API server already applies all six defaults, a line for a new field cannot act in its release,
and the hook's only reachable effects are neutral or unblock a CR that cannot deploy. The question is
whether to keep a blocking upgrade step for a category of default with no instance today. The answer
also decides whether item 6 (the `test-e2e-helm` fix) is needed at all.

- **C - retire the hook and the `migrate` subcommand (recommended)**: defaults are schema markers or
  read-time getters. Cost M (chart, subcommand, tests, about 30 documents, items 3-5 first). Every
  upgrade loses a blocking step and the CRD grant, H-3 closes; the explicit-`""` repair is lost,
  which no running cluster needs.
- **A - keep the hook; a line only for a default a marker cannot express**: cost S (ADR plus the
  DEVELOPER.md step, plus item 6). The hook keeps running on every upgrade doing nothing, with its
  grant; the permitted category can only write fields the previous release's CRD already has.

C, because each premise is checkable (tag scan, `algorithm.go:45`, items 3 and 4) and A keeps a
mechanism alive speculatively; A is the fallback if item 3 or 4 contradicts read-time defaulting.

**Answer:** _open_

### Q4: Should the hook's grant be narrowed now, before Q3's outcome ships? (grant)

A role that is too narrow fails every `helm upgrade`, so any narrowing needs one real upgrade run
before release. Under Q3 = C the narrowed file is deleted again; under A it is permanent.

- **G1 - `valkeys: list, patch` only, now (recommended)**: XS in the chart, S with the documents; the
  verification run is the one item 4 needs anyway. Removes the CRD grant from the next release on.
- **G3 - no interim change; fold it into C**: no cost now, documents amended once; the unused CRD
  write grant stays on every upgrade until C ships.

G1, because C is M effort with preconditions and no date, and G1 removes the one privilege that
matters early. G3 is right only if Q3 = C and it ships in the same release.

**Answer:** _open_

## Not verified

- That `make test-e2e-helm` fails today (expected `fork/exec ./bin/manager: no such file or
  directory`), that the rest passes once the binary is found, and that no operator is needed: the
  Kind run of item 6 (moot under Q3 = C).
- The runtime effect of `install`/`uninstall` on the chart's binding and of `make deploy` on a
  chart-installed cluster (read and rendered, not run); unnecessary under Q1 = C and Q2 = yes.
- Read-time defaulting against a real API server (read in source): items 3 and 4.
- Helm's hook order and the silent pruning of an unknown field (read in docs and source, not run).
- That the hook makes no request beyond `List`, `Patch` and discovery: the Q4 = G1 run.
- That `./manager migrate` without the dispatch starts an operator and fails the upgrade (by reading).
- That cert-manager treats `group: cert-manager.io` as equal to an empty group (read on `master`, no
  reissue measured).
- That Flux re-applies a `""` Git sets after the hook patches it (expected from server-side apply).
- The production fleet: item 5.

## Related

- [T43](043-static-checks-and-ci-gates-miss-tagged-tests-standing-constraints-and-chart.md): lint and vet skip build-tagged files,
  so `migrate_e2e_test.go` is not compiled in CI; its tag list includes `e2e_helm`, gone under Q3 = C.
- T45: its count of Makefile lines the Renovate manager matches drops from 6 to 5 under Q2 = yes.
- T35, T52: "the kustomize path" stays valid for user-built overlays but should not cite
  `config/default`.
- T48: a selector on the chart's selector labels also selects the hook pod; gone under Q3 = C.
- T45: the hook is a tag-pinned consumer of the operator image; drops out under Q3 = C.
- T29: cites the hook ClusterRole at `pre-upgrade-rbac.yaml:29-50`, which changes under Q4 = G1 or
  Q3 = C.
- T43: no CI gate renders the chart, so the `helm template` checks here stay manual.
- T30: embargoed security finding, dropped.
