---
id: T47
title: no rule decides when a new CRD field needs a line in the migrate hook's applyDefaults
state: analysed       # facts verified, options weighed, nothing decided
severity: low         # nothing behaves wrongly for a running cluster; a missing rule, a blocking upgrade step with nothing to do, and an unused grant
security: hardening
threat: "would additionally cover the one privilege the hook ServiceAccount holds beyond the operator's own ServiceAccount in the same namespace - cluster-wide customresourcedefinitions get/list/patch/update, held by no other principal of the chart - usable by the hook's operator image and by anyone who may create pods in the release namespace, for the life of the hook Job on every helm upgrade and until the next upgrade after a failed hook; the hook's code needs valkeys list, patch only"
urgency: now          # rule 1: tracked files state what the code contradicts (Required changes, item 1); later (rule 4) once item 1 lands
effort: M             # the recommended option C of Q1; A is S
blocked-by: decision  # Q1, then Q2
filed-from: the documentation restructure (DEVELOPER.md, "Adding things", CRD field step 6)
opened: 2026-09-27
decided:
done:
---

# T47 - no rule decides when a new CRD field needs a line in the migrate hook's applyDefaults

## Current state

The CRD-field checklist in [DEVELOPER.md](../../DEVELOPER.md#adding-things) (step 6) tells a
contributor to check `applyDefaults`
([`cmd/migrate/migrate.go:104-146`](../../cmd/migrate/migrate.go#L104-L146)); no ADR says what
belongs there. [ADR 0032](../adr/0032-generated-pods-run-rootless.md) (Alternatives, `:419-423`)
already rejected a hook-based default because the hook runs before the new CRD.

**What the hook does.** The chart's pre-upgrade Job
([`pre-upgrade-job.yaml:11-13`](../../deploy/helm/valkey-operator/templates/pre-upgrade-job.yaml#L11-L13),
weight `-5`, `backoffLimit: 3` at `:15`, enabled by default at
[`values.yaml:153`](../../deploy/helm/valkey-operator/values.yaml#L153)) runs `./manager migrate`
(`:36`, dispatch [`cmd/main.go:137-141`](../../cmd/main.go#L137-L141)). It lists every `Valkey`
cluster-wide ([`migrate.go:55`](../../cmd/migrate/migrate.go#L55)), merge-patches each one where
`applyDefaults` reports a change ([`:86`](../../cmd/migrate/migrate.go#L86)) and exits 1 on any
failure (`:66-69`). Helm fails the release when a hook fails, so the hook is a blocking step of
every upgrade.

**What `applyDefaults` writes**, each only when empty and its parent set: `spec.replicas` 0 -> 1;
`auth.secretPasswordKey` `""` -> `password`; `tls.certManager.issuer.group` `""` ->
`cert-manager.io`; `sentinel.replicas` 0 -> 3; `persistence.mode` `""` -> `rdb`;
`persistence.size` zero -> `1Gi`. Unit tests in
[`migrate_test.go`](../../cmd/migrate/migrate_test.go); `TestMigrateAll_UpToDateCRIsNotPatched`
([`migrate_all_test.go:98`](../../cmd/migrate/migrate_all_test.go#L98)) asserts a CR with nothing
empty is not patched.

**The CRD already defaults all six.** Each has a `+kubebuilder:default` marker with the same value
in [`api/v1/valkey_types.go`](../../api/v1/valkey_types.go) (`:1029`, `:570`, `:578`, `:481`,
`:999`, `:1008`). A scan of `templates/crd.yaml` in all 104 release tags finds the six defaults
with the same values in every one, plus `Minimum=1` on both `replicas`, the enum `rdb;aof;both` on
`persistence.mode`, and no `minLength` on `secretPasswordKey` or `issuer.group`.

**The API server applies those defaults on every request and every read, to absent keys only**
(`apiextensions-apiserver@v0.37.1`: `customresource_handler.go:1185-1195`, `:1289-1297`;
`schema/defaulting/algorithm.go:45`). An explicit `""` or `0` is kept. So the hook can only change
a CR stored with an explicit empty value, and each such case is neutral or already broken:

| Stored value | Effect today | Effect of the hook's patch |
|---|---|---|
| `size: "0"` | builder falls back to `1Gi` ([`statefulset.go:1191-1195`](../../internal/builder/statefulset.go#L1191-L1195)) | none |
| `issuer.group: ""` | left out of `issuerRef` ([`certificate.go:183`](../../internal/builder/certificate.go#L183-L184), `:227`) | adds `cert-manager.io`, which cert-manager treats as equal |
| `secretPasswordKey: ""` with `secretName` | the API server refuses the empty `secretKeyRef` key, the data StatefulSet write is blocked | unblocks it, but only at the next upgrade |
| `secretPasswordKey: ""` without `secretName` | auth disabled ([`valkey_types.go:1171-1173`](../../api/v1/valkey_types.go#L1171-L1173)) | none |

`replicas`, `sentinel.replicas` and `persistence.mode` cannot be stored empty (validation).

**Typed clients.** `SecretPasswordKey` and `Group` are `omitempty` strings, so a typed write never
stores `""` and the operator's own full `r.Update` of a CR re-defaults them. `Size` is a
`resource.Quantity`, so a typed write of a persistence block without a size stores `size: "0"`.
`ValkeySpec.Resources` is a non-pointer struct, so the operator's first full `r.Update` of a CR
without it stores `resources: {}` and bumps `metadata.generation`.

**A line for a new field cannot act in its own release.** A `pre-upgrade` hook runs before any
release resource is updated, the CRD ships as a template (no `crds/`), the CRD carries no
`x-kubernetes-preserve-unknown-fields`, and `migrate.go:46` uses plain `client.New` (field
validation `Warn`), so the patch of an unknown field is pruned silently.

**The grant.** [`pre-upgrade-rbac.yaml`](../../deploy/helm/valkey-operator/templates/pre-upgrade-rbac.yaml)
grants `valkeys: get,list,patch,update` (`:30-39`) and `customresourcedefinitions:
get,list,patch,update` (`:42-50`) cluster-wide; the comment at `:17` still says "read+patch access
to Valkey CRs and the CRD itself". The code needs `valkeys: list, patch` plus discovery (covered by
`system:discovery`). The operator's ServiceAccount in the same namespace holds a superset of the
`valkeys` rule ([`clusterrole.yaml:9-20`](../../deploy/helm/valkey-operator/templates/clusterrole.yaml#L9-L20));
no other chart principal holds `customresourcedefinitions`. The delete policy
`hook-succeeded,before-hook-creation` (`:15`, `:28`, `:63`, `pre-upgrade-job.yaml:13`) has no
`hook-failed`, so after a failed hook the RBAC objects, the Job and its pods stay.

**Object names.** `<fullname>-upgrade` (ServiceAccount, ClusterRole, ClusterRoleBinding) and
`<fullname>-pre-upgrade` (Job); `<fullname>` is `<release>-valkey-operator` unless the release name
contains the chart name ([`_helpers.tpl:11-22`](../../deploy/helm/valkey-operator/templates/_helpers.tpl#L11-L22)).

**Tracked statements the code contradicts:**

- [`DEVELOPER.md:46-47`](../../DEVELOPER.md) and `:348-350`: the hook "writes field defaults into
  every existing CR" (contradicted by `TestMigrateAll_UpToDateCRIsNotPatched`).
- [`upgrading.md:149-153`](../operations/upgrading.md): the hook ensures the new operator "never
  reconciles a CR that predates its defaults" (that is the new CRD's read-time defaulting, which
  the hook runs before); the Job is named `valkey-operator-pre-upgrade`.
- ADR 0013 D10 (`:296`), `privilege-footprint.md:127`, `trust-boundaries.md:13`: `<release>-upgrade`.
- [`migrate.go:2-3`](../../cmd/migrate/migrate.go#L2-L3), `:40-41`; `test/e2e/fleet_upgrade_test.go:331-335`;
  `test/e2e/migrate_e2e_test.go:7`, `:39`, `:60-62`, `:119-120`, `:125`: defaults "introduced in
  the current version" or CRs that "predate" them - the tag scan refutes both.
- [ADR 0021](../adr/0021-per-resource-metrics-and-the-alert-that-was-missing.md) D7 (`:125-127`):
  the hook's absence "breaks ... the upgrade path".

**Coverage.** `TestE2E_Migrate*` ([`migrate_e2e_test.go`](../../test/e2e/migrate_e2e_test.go),
tag `e2e && e2e_helm`) runs in no CI workflow, runs the binary under the tester's kubeconfig
(`:136-147`) and fabricates an explicit `secretPasswordKey: ""`. `TestE2E_FleetUpgrade`
([`fleet_upgrade_test.go`](../../test/e2e/fleet_upgrade_test.go), local Kind only,
`make e2e-fleet-upgrade-local`) runs the real hook and asserts it succeeded
(`requirePreUpgradeHookSucceeded`, `:848-884`, `t.Fatalf` at `:882`), but its fleet sets every
field, so the hook patches nothing there. No integration test asserts any of the six defaults.

**Impact.** A contributor adding a defaulted field has no rule, and a line added for a new field
duplicates its marker and cannot act in its release. Every upgrade runs a blocking hook that, on
any CR not stored with an explicit empty value, has nothing to do. Live today with default values:
every `helm upgrade` hands the hook ServiceAccount cluster-wide `patch`/`update` on any CRD in the
cluster (principal: the hook's operator image, or anyone who may create pods in the release
namespace; window: the hook Job, and after a failed hook until the next upgrade). The `valkeys`
verbs add nothing beyond the operator's own ServiceAccount.

## Required changes

### Independent of the open questions

1. **Correct the contradicted statements** listed above (under Q1 = C they are deleted with the
   hook instead). `DEVELOPER.md`: the hook fills any of six fields stored empty. `upgrading.md`:
   name what provides the guarantee (Helm applies the CRD template before the Deployment, and the
   API server defaults on read) and the Job `<fullname>-pre-upgrade`. Names `<fullname>-upgrade`.
   ADR 0021 D7: the hook's absence does not break the upgrade path (read in
   `apiextensions-apiserver` source; say so, or land after item 2).
2. **Envtest defaults assertion** (ADR 0017 D14) in `test/integration`: a `Valkey` created with the
   six fields absent (parents present) is stored with `1`, `password`, `cert-manager.io`, `3`,
   `rdb`, `1Gi`; an explicit `secretPasswordKey: ""` is stored as `""`. Write raw JSON (unstructured
   create or `client.RawPatch`, as [`pod_hardening_test.go:101-104`](../../test/integration/pod_hardening_test.go#L101-L104))
   and read from the write's response, since the client is cache-backed. Removing one marker must
   fail it.
3. **Fleet-upgrade defaults read**: in `TestE2E_FleetUpgrade`, add a member created through the
   dynamic client with `sentinel` without `replicas`, `persistence` without `mode`/`size`,
   `tls.certManager.issuer` without `group`, `auth: {}` (auth disabled), `spec.replicas` explicit;
   read it back before `helm upgrade` and assert `3`, `rdb`, `1Gi`, `cert-manager.io`, `password`.
   Do not assert `metadata.generation` unchanged (the `resources: {}` bump above).
4. **Owner, read-only**: list production CRs holding an explicit empty value, e.g.
   `kubectl get valkeys -A -o json | jq -r '.items[] | select(.spec.auth.secretPasswordKey=="" or .spec.tls.certManager.issuer.group=="" or ((.spec.persistence.size // "x")|tostring)=="0") | .metadata.namespace+"/"+.metadata.name'`.
   Expected empty; a hit with `auth.secretName` set is a blocked CR to fix in Git.

Items 2-4 are preconditions of Q1 under either answer.

### Depends on the answers

**Q1 = C (retire the hook):**

- New ADR: defaults are schema markers; a default that needs code is a read-time getter or
  fallback (as `GetSyncTimeout`, `GetObserverResources`, `GetSeccompProfile`,
  `buildVolumeClaimTemplates`); nothing writes defaults into stored CRs on upgrade. Supersede ADR
  0013 D10 and close its residual risk (`:580-581`) and [H-3](../security/privilege-footprint.md#h-3);
  amend ADR 0021 D7, ADR 0017 D34 (`:634-637`) and D55 (`:1039`). DEVELOPER.md step 6 cites the ADR.
- Remove `pre-upgrade-job.yaml`, `pre-upgrade-rbac.yaml`, `preUpgradeHook` in `values.yaml:147-164`
  (and mentions at `:11`, `:16`), `cmd/migrate/` with its tests, the import and dispatch in
  `cmd/main.go` (`:18`, `:137-141`), `test/e2e/migrate_e2e_test.go`, `test-e2e-helm`
  (`Makefile:175-178`).
- Same change, or the tree breaks: in `fleet_upgrade_test.go` remove `requirePreUpgradeHookSucceeded`
  (`:848-884`) and the comments at `:18-19`, `:85`, `:331-336`; keep item 3.
- Keep `./manager migrate` as an explicit subcommand that logs its retirement and exits 0.
  Without it `main` falls through to `flag.Parse` ([`cmd/main.go:151-157`](../../cmd/main.go#L151-L157))
  and starts a full operator, so an older chart running a newer image via `image.tag` fails its
  upgrade.
- Documents: `README.md:153`, `:551`, `:559`, `:578-580`; `DEVELOPER.md:20-24`, `:46-47`, `:72`,
  `:189`, `:348-350`; `upgrading.md:149-153`, `installation.md:62`; `testing.md:18`, `:108`,
  `:182`; `package-map.md:21`, `:95`, `:106`; `architecture.md:88`; `privilege-footprint.md:3`,
  `:125-134`, `:162-178`; `trust-boundaries.md:13`; `operator-pod-posture.md:3`, `:11`;
  `_helpers.tpl:80`; ADR 0013 `:49`, `:230`, `:296-304`, `:580-581`, `:600`; ADR 0014 `:27`,
  `:181`; ADR 0017 `:636`, `:926`, `:1039`; ADR 0021 `:126`; ADR 0033 `:151`, `:278`; ADR 0035
  `:80`; ADR 0036 `:137`, `:157`. `CLAUDE.md:946` needs the owner. Leave ADR 0030 `:255`, `:526`
  ("pre-upgrade Sentinel pod") and `cmd/main_test.go` alone.
- `upgrading.md` upgrade note: Helm does not delete hook objects the chart stops rendering, so after
  a failed last hook, `<fullname>-upgrade` (ServiceAccount, ClusterRole, ClusterRoleBinding) and the
  Job `<fullname>-pre-upgrade` with its pods need manual cleanup. Leftover `preUpgradeHook` values
  keep working (no `values.schema.json`).
- Proof: `helm template` renders no hook object; `make e2e-fleet-upgrade-local` green with item 3
  and without the hook assertion; `./manager migrate` exits 0; `make test-unit`, `make lint`,
  `make generate-all` leave a clean tree.

**Q1 = A (keep the hook):** new ADR (or ADR 0005 amendment): a marker default gets no line; a line
only for a value computed from other fields or cluster state that must be persisted. The six lines
are dropped or kept with a note; DEVELOPER.md step 6 cites the ADR.

**Q2 = G1 (narrow now):** in `pre-upgrade-rbac.yaml:30-50` keep only `valkeys: list, patch`; fix the
comment at `:17`; amend ADR 0013 D10 and close its CRD residual-risk bullet; update
`privilege-footprint.md` (hook section, H-3) and `trust-boundaries.md:13`. Proof: `helm template`
renders exactly `valkeys: list, patch` and no `apiextensions.k8s.io` rule; one
`make e2e-fleet-upgrade-local` run (the same run as item 3) with one fleet CR seeded by raw merge
patch with `spec.auth: {secretPasswordKey: ""}` and no `secretName`, read back as `""` immediately
before `helm upgrade` and as `password` after it, and `requirePreUpgradeHookSucceeded` green. (The
Job log cannot be read: `hook-succeeded` deletes the Job.)

**Closing (ADR 0034):** the rule in an ADR, the operator-visible consequence in `upgrading.md` and
the security pages, the contributor rule in DEVELOPER.md step 6; then archive this file.

## Open questions

### Q1: Which rule governs `applyDefaults` - does the migrate hook stay at all?

The API server already applies all six defaults to every stored CR, a line for a new field cannot
act in its release, and the hook's only reachable effects are neutral or unblock a CR that cannot
deploy. The question is whether to keep a blocking upgrade step for a category of default that has
no instance today.

- **C - retire the hook and the `migrate` subcommand (recommended).** Defaults are schema markers or
  read-time getters. Cost M (chart, subcommand, tests, about 30 files of docs, items 2-4 first).
  Every upgrade loses a blocking step and the cluster-wide CRD grant; H-3 closes; the explicit-`""`
  repair is lost, which no running cluster needs.
- **A - keep the hook; a line only for a default a marker cannot express.** Cost S (ADR plus the
  DEVELOPER.md step). The hook keeps running on every upgrade, doing nothing, with its grant; the
  permitted category can only write fields the previous release's CRD already has.

C, because each premise is checkable (tag scan, `algorithm.go:45`, items 2 and 3) and A keeps a
mechanism alive speculatively. A is the fallback if item 2 or 3 contradicts read-time defaulting.

**Answer:** _open_

### Q2: Should the hook's grant be narrowed now, before Q1's outcome ships?

A role that is too narrow fails every `helm upgrade`, so any narrowing needs one real upgrade run
before release. Under Q1 = C the narrowed file is deleted again; under A it is permanent.

- **G1 - `valkeys: list, patch` only, now (recommended).** Cost XS in the chart, S with the
  documents; the verification run is the one item 3 needs anyway. Removes the CRD grant from the
  next release on.
- **G3 - no interim change; fold it into C.** No cost now, documents amended once. The unused CRD
  write grant stays on every upgrade until C ships.

G1, because C is M effort with preconditions and no date, and G1 removes the one privilege that
matters (the CRD rule) early. G3 is right only if C is decided and ships in the same release.

**Answer:** _open_

## Not verified

- Read-time defaulting against a real API server (read in source): items 2 and 3 settle it.
- Helm's hook order and the silent pruning of an unknown field (read in docs and source, not run).
- That the hook makes no request beyond `List`, `Patch` and discovery at runtime: the G1 run
  settles it.
- That `./manager migrate` without the dispatch starts an operator and fails the upgrade (by
  reading).
- That cert-manager treats `group: cert-manager.io` as equal to an empty group (read on `master`,
  no reissue measured).
- That Flux re-applies a `""` Git sets after the hook patches it (expected from server-side apply).
- The production fleet: item 4.

## Related

- [T43](043-lint-and-vet-skip-every-build-tagged-test-file.md): its lint tag list includes
  `e2e_helm`, which disappears under C.
- [T44](044-test-e2e-helm-points-its-test-at-a-binary-that-is-not-there.md): its Kind verification
  run is moot under C; `test-e2e-helm` can never verify the hook's role (tester's kubeconfig).
- T49: a selector on the chart's selector labels also selects the hook pod; gone under C.
- T53: the hook is a tag-pinned consumer of the operator image; drops out under C.
- T56: cites the hook ClusterRole at `pre-upgrade-rbac.yaml:29-50`, which changes under G1 or C.
- T58: no CI gate renders the chart, so the `helm template` checks here stay manual.
