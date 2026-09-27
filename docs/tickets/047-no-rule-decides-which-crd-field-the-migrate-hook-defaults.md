---
id: T47
title: no rule decides when a new CRD field needs a line in the migrate hook's applyDefaults
state: analysed       # was filed until 2026-09-27: facts re-verified at 84a39c2 in code, git history and upstream source, options weighed; nothing decided
severity: low         # nothing behaves wrongly for a running cluster; the gap is a missing rule, a blocking upgrade step with nothing to do, and a grant the code does not use
security: hardening
threat: "would additionally cover the one privilege the hook ServiceAccount holds beyond the operator's own ServiceAccount in the same release namespace - cluster-wide customresourcedefinitions get/list/patch/update, held by no other principal of the chart - usable by the operator image the hook runs and by any principal who may create pods in the release namespace, for the lifetime of the hook Job on every helm upgrade and until the next upgrade after a failed hook; the hook's code needs valkeys list, patch only"  # rewritten 2026-09-27 at 84a39c2: the old line ("no attacker; ...") named no additional coverage, which the hardening row requires, and counted the valkeys verbs the operator's own ServiceAccount already holds
urgency: now          # rule 1 since 2026-09-27 at 84a39c2 (was later, rule 4): DEVELOPER.md:46-47 and :348-349 are contradicted by an executed check (TestMigrateAll_UpToDateCRIsNotPatched, migrate_all_test.go:98, run by make test-unit); upgrading.md:149-152, ADR 0021 D7, migrate.go:2-3 and :40-41 and the e2e comments listed under work list item 1c are false by reading of code, git history and upstream source; back to later (rule 4) once item 1c lands
effort: M             # the recommended option C of decision 1 (chart, subcommand, tests, about 30 files of docs, three preconditions); A is S. Was S until 2026-09-27
blocked-by: decision  # decision 1 (the rule, whether the hook stays), then decision 2 (the interim grant narrowing)
filed-from: the documentation restructure of 2026-09-27 (DEVELOPER.md, "Adding things", CRD field step 6)
opened: 2026-09-27
decided:              # not recorded - no decision yet
done:                 # not recorded - not done
---

Filed on 2026-09-27 from a finding of the documentation restructure: the CRD-field checklist in
[DEVELOPER.md](../../DEVELOPER.md#adding-things) tells a contributor to check `applyDefaults`,
and nothing tells them what to decide there. Everything below was read in the working tree of
`feat/rootless` on 2026-09-27 (`HEAD` = `f5c6886` plus the uncommitted restructure, which,
according to the session's starting `git status`, touches neither `cmd/` nor `api/v1/` nor
`deploy/helm/`). No make target and no cluster was run for this file. *(corrected 2026-09-27:
the restructure is committed as `4a7543e`; `git diff f5c6886 4a7543e` touches nothing under
`cmd/`, `api/v1/` or `deploy/helm/`, and every location below was re-read there.)*
*(Re-verified 2026-09-27 at 84a39c2, superseding the basis stated above: everything below was re-verified at `HEAD` = `84a39c2` on
branch `chore/maintenance-2026-09-27`, a clean tree. Since `4a7543e`, commit `bcc63c9`
("docs: correct comments and records that the code contradicts") changed
`api/v1/valkey_types.go` (two comment lines), `pre-upgrade-rbac.yaml`, `values.yaml`, ADR 0013,
`privilege-footprint.md`, `testing.md` and `migrate_e2e_test.go`; `git diff 34c351c 84a39c2 --
cmd/migrate/` is empty. No make target, no `go test`, no `helm` and no cluster was run for this
file; the evidence is reading, `git`, scratch scripts outside the repository (two tag scans and
two `go run` encoding checks) and upstream source.)*

## Fact

The open question: **when does a new CRD field need a line in `applyDefaults`**
([`cmd/migrate/migrate.go:104`](../../cmd/migrate/migrate.go#L104-L146))? No ADR answers it. A grep of
`docs/adr/` for `applyDefaults`, `migrate.go` and `cmd/migrate` on 2026-09-27 found only
[ADR 0017](../adr/0017-test-and-ci-policy.md) D34, which lists `migrate.Run` among the entry
points deliberately not unit-tested; [ADR 0013](../adr/0013-operator-is-cluster-wide-privileged.md)
D10 names "the field-default migration it performs" as the cost of disabling the hook and says
nothing about what belongs in it. *(Added 2026-09-27 at 84a39c2: those grep terms miss two ADRs
that bear on the question without deciding it.
[ADR 0032](../adr/0032-generated-pods-run-rootless.md), Alternatives Considered
([`:419-423`](../adr/0032-generated-pods-run-rootless.md)), already rejected a hook-based CRD
default pin because "the hook runs before the new CRD and its pin is pruned" - the Helm-order
mechanism this ticket lists under Not verified. [ADR 0021](../adr/0021-per-resource-metrics-and-the-alert-that-was-missing.md)
D7 ([`:121-127`](../adr/0021-per-resource-metrics-and-the-alert-that-was-missing.md)) justifies
`preUpgradeHook.enabled` being default-on as a value "whose absence breaks the install or the
upgrade path", which the facts below contradict for the upgrade path.)*

**Verified:**

- **What the hook does.** The chart's pre-upgrade Job
  ([`pre-upgrade-job.yaml`](../../deploy/helm/valkey-operator/templates/pre-upgrade-job.yaml),
  `helm.sh/hook: pre-upgrade`, weight `-5`, rendered by default through
  `preUpgradeHook.enabled: true` in [`values.yaml`](../../deploy/helm/valkey-operator/values.yaml))
  runs `./manager migrate` from the chart's operator image. `migrate.Run` lists every `Valkey`
  cluster-wide, and `migrateAll` merge-patches each one for which `applyDefaults` reports a
  change; a failing patch is counted, the loop continues, and any failure exits 1.
  *(Locations added 2026-09-27: hook annotations
  [`pre-upgrade-job.yaml:11-13`](../../deploy/helm/valkey-operator/templates/pre-upgrade-job.yaml#L11-L13),
  command `:36`; `enabled: true` at [`values.yaml:153`](../../deploy/helm/valkey-operator/values.yaml#L153); the dispatch at
  [`cmd/main.go:137-141`](../../cmd/main.go#L137-L141); `List` at
  [`migrate.go:55`](../../cmd/migrate/migrate.go#L55), `Patch` at [`:86`](../../cmd/migrate/migrate.go#L86), exit at `:66-69`.)*
  *(Added 2026-09-27 at 84a39c2:)* **The hook is a blocking step of every upgrade.** Helm fails
  the release when a hook fails ("if the hook fails, the release will fail. This is a blocking
  operation",
  [charts_hooks.md L76-79](https://github.com/helm/helm-www/blob/main/docs/topics/charts_hooks.md)),
  and the Job has `backoffLimit: 3`
  ([`pre-upgrade-job.yaml:15`](../../deploy/helm/valkey-operator/templates/pre-upgrade-job.yaml#L15)),
  so a hook that patches nothing can still fail every operator upgrade.
- **What `applyDefaults` writes: six fields, each only when empty.** `spec.replicas` 0 → 1;
  `spec.auth.secretPasswordKey` `""` → `password` (when `spec.auth` is set);
  `spec.tls.certManager.issuer.group` `""` → `cert-manager.io` (when `spec.tls.certManager` is
  set); `spec.sentinel.replicas` 0 → 3 (when `spec.sentinel` is set); `spec.persistence.mode`
  `""` → `rdb` and `spec.persistence.size` zero → `1Gi` (when `spec.persistence` is set). Each has
  a unit test in [`migrate_test.go`](../../cmd/migrate/migrate_test.go) (`:11`, `:32`, `:55`,
  `:92`, `:105`, `:121`), and
  [`migrate_all_test.go:98`](../../cmd/migrate/migrate_all_test.go#L98)
  (`TestMigrateAll_UpToDateCRIsNotPatched`) asserts that a CR with nothing empty is not patched.
- **All six already carry a `+kubebuilder:default` marker with the same value** in
  [`api/v1/valkey_types.go`](../../api/v1/valkey_types.go): line
  [1029](../../api/v1/valkey_types.go#L1029) (`replicas`, 1),
  [570](../../api/v1/valkey_types.go#L570) (`password`),
  [578](../../api/v1/valkey_types.go#L578) (`cert-manager.io`),
  [481](../../api/v1/valkey_types.go#L481) (Sentinel `replicas`, 3),
  [999](../../api/v1/valkey_types.go#L999) (`rdb`),
  [1008](../../api/v1/valkey_types.go#L1008) (`1Gi`). The generated chart CRD carries each as `default:`
  ([`templates/crd.yaml`](../../deploy/helm/valkey-operator/templates/crd.yaml) lines 579, 122,
  794, 692, 448, 460).
- **Every release shipped those markers, and the hook came later.** All six are in `0aaa3a2`
  (2026-02-17, "feat: add Foundation & CRD"). Every release tag contains that commit
  (`git tag --no-contains 0aaa3a2` prints nothing; the first tag is `v1.0.0`), and the hook
  arrived in `73f6efe` (2026-03-02, "feat: Operator Self-Upgrade / Cluster Migration"). So
  whichever release's CRD is installed while the hook runs, it carries all six defaults.
  *(Measured 2026-09-27 at 84a39c2, stronger than the commit ancestry:)* a script that parses
  `deploy/helm/valkey-operator/templates/crd.yaml` of every `git tag` with PyYAML 6.0.1 and
  compares the six spec defaults prints `104 tags checked; mismatches: []` (auditor's scan);
  an extended scan by the facts skeptic, which also compares the validation, prints
  `104 [] 0` - every one of the 104 tags (`v1.0.0`, 2026-02-18, to `v1.13.1`, 2026-09-27)
  carries the six defaults with the same values, `Minimum=1` on `spec.replicas` and
  `spec.sentinel.replicas`, the enum `rdb;aof;both` on `persistence.mode`, and no `minLength` on
  `secretPasswordKey` or `issuer.group`. `git tag --contains 73f6efe --sort=creatordate | head -1`
  prints `v1.2.0`; 94 of the 104 tags ship the hook.
- **Where the marker and the hook differ.** A structural-schema default fills an absent field
  only; `applyDefaults` also rewrites a present empty value. Per field: `replicas` and
  `sentinel.replicas` carry `Minimum=1` and `persistence.mode` an enum (`rdb;aof;both`), so an
  explicit empty value cannot be stored. `secretPasswordKey` has no length rule, so `""` can be
  stored, and the operator then reads the Secret key `""`
  ([`valkey_controller.go:188`](../../internal/controller/valkey_controller.go#L188),
  [`checker.go:86`](../../internal/health/checker.go#L86), and the `secretKeyRef` of every builder).
  An empty `issuer.group` is left out of the issuer reference by the builder
  ([`certificate.go:183`](../../internal/builder/certificate.go#L183-L184), line 227). A `size` of `0`
  falls back to `1Gi` in `buildVolumeClaimTemplates`
  ([`statefulset.go:1191-1195`](../../internal/builder/statefulset.go#L1191-L1195)). The hook repairs an
  explicit `secretPasswordKey: ""` only at the next upgrade, never on create, so it is no
  guarantee for that case either.
  *(Added 2026-09-27 at 84a39c2:)* such a CR cannot run at all while auth is enabled.
  `IsAuthEnabled` is `Auth != nil && SecretName != ""`
  ([`valkey_types.go:1171-1173`](../../api/v1/valkey_types.go#L1171-L1173)); with it, every
  builder puts the key unmodified into a `secretKeyRef`
  ([`statefulset.go:385`](../../internal/builder/statefulset.go#L385), `:570`, `:883`, `:955`,
  `:1084`, [`sentinel.go:360`](../../internal/builder/sentinel.go#L360), `:567`,
  [`observer.go:257`](../../internal/builder/observer.go#L257)), and the API server refuses an
  empty key (`validateSecretKeySelector`,
  [kubernetes v1.36.1 `pkg/apis/core/validation/validation.go:3038-3039`](https://github.com/kubernetes/kubernetes/blob/v1.36.1/pkg/apis/core/validation/validation.go#L3038-L3039),
  `field.Required(fldPath.Child("key"), "")`, reached from `validateEnvVarValueFrom` at `:2827`),
  so the data StatefulSet write is refused and the pass reports it (ADR 0002). The upgrade-time
  repair therefore only ever unblocks an already-blocked CR. A CR with `auth.secretName` empty and
  `secretPasswordKey: ""` deploys with auth disabled, and the hook's patch changes nothing there.
- *(Added 2026-09-27 at 84a39c2)* **The API server applies the six defaults on every request and
  on every read, and only to absent keys.** In the pinned module
  `k8s.io/apiextensions-apiserver@v0.37.1` (`go.mod:69`), the request decoder
  (`pkg/apiserver/customresource_handler.go:1185-1195`) and the storage codec that decodes
  objects read from etcd (`:1289-1297`) both carry the `unstructuredDefaulter`, and
  `pkg/apiserver/schema/defaulting/algorithm.go:45` applies a default only
  `if _, found := x[k]; !found || isNonNullableNull(x[k], &prop)` - an explicit `""` or `0` is
  kept. The upstream documentation says the same
  ([custom-resource-definitions.md L1460-1467](https://github.com/kubernetes/website/blob/main/content/en/docs/tasks/extend-kubernetes/custom-resources/custom-resource-definitions.md)).
  With the tag scan above, every stored CR got the six defaults when it was created, and the
  hook can change only a CR stored with an explicit `secretPasswordKey: ""`, an explicit
  `issuer.group: ""` or a `size` of `0`. Read in source, not measured against an API server.
- *(Added 2026-09-27 at 84a39c2)* **A typed Go client drops an empty string but writes a zero
  size.** Measured with `go run` of a scratch module outside the repository
  (`k8s.io/apimachinery v0.37.1`, `encoding/json`): `json.Marshal` of a struct shaped like
  `PersistenceSpec{}` prints `{"size":"0"}`, and of `AuthSpec{SecretName: "s"}` prints
  `{"secretName":"s"}`. `SecretPasswordKey` and `Group` are `omitempty` strings
  ([`valkey_types.go:572`](../../api/v1/valkey_types.go#L572), `:580`), so a typed write never
  stores `""` for them and the API server re-defaults them on that request; `Size` is a
  `resource.Quantity` struct ([`:1010`](../../api/v1/valkey_types.go#L1010)), which `omitempty`
  never omits, so any typed client that sets a persistence block without a size stores
  `size: "0"`. Consequences: the operator's own full `r.Update(ctx, v)` of the CR
  ([`rolling_update.go:1080`](../../internal/controller/rolling_update.go#L1080), `:1183`,
  `:2205`, `:3257` and others) re-defaults an explicit `""` in those two fields (inferred from the
  measurement and `algorithm.go:45`, not measured end to end, and only for a CR that reaches such
  an update); and an envtest that wants to store an explicit `""` has to write raw JSON (work list
  item 2). *(Added 2026-09-27 at 84a39c2, review:)* the same round trip also **adds** a key.
  `ValkeySpec.Resources` is a non-pointer `corev1.ResourceRequirements` under `omitempty`
  ([`valkey_types.go:1074`](../../api/v1/valkey_types.go#L1074)), and `encoding/json` never
  omits a struct: `go run` of a stdlib struct of the same shape (go1.26.5) prints
  `{"image":"x","resources":{}}`. So the operator's first full `r.Update` of a CR created without
  `spec.resources` stores `resources: {}`, and the API server increments `metadata.generation`
  for any difference outside `metadata`
  (`apiextensions-apiserver@v0.37.1` `pkg/registry/customresource/strategy.go:179-185`). That
  generation bump comes from the operator, not from the hook (by reading and the encoding
  measurement, not run against an API server).
- *(Added 2026-09-27 at 84a39c2)* **No code path reads one of the six fields without a fallback
  in a way the hook could matter for, except `secretPasswordKey`.** Audit of non-test readers
  (`grep -rn 'Issuer\.Group\|Persistence\.Size\|Persistence\.Mode' --include='*.go' internal api cmd`
  and the `secretKeyRef` sites above): `issuer.group` only at `certificate.go:183` and `:227`
  (left out when empty), `size` only at `statefulset.go:1193` (falls back to `1Gi`),
  `persistence.mode` at [`configmap.go:200`](../../internal/builder/configmap.go#L200) without a
  fallback (`""` would disable both RDB and AOF) but unstorable (enum), `sentinel.replicas`
  without a `> 0` guard at [`pdb.go:98`](../../internal/controller/pdb.go#L98), `:137`,
  [`builder/pdb.go:54`](../../internal/builder/pdb.go#L54), `statefulset.go:272` and
  `sentinel.go:113` but unstorable (`Minimum=1`). The only field with a storable empty value and
  fallback-free readers is `secretPasswordKey`, and that state cannot deploy (above).
- *(Added 2026-09-27 at 84a39c2)* **Both remaining patchable states are behaviour-neutral by
  reading.** For `size: "0"`, `buildVolumeClaimTemplates` already builds `1Gi`, so writing `1Gi`
  changes no built object. For `issuer.group: ""`, the Certificate's `issuerRef` gains
  `group: cert-manager.io`, which cert-manager treats as equal to an empty group
  (`IssuerGroupsEqual`,
  [cert-manager `pkg/api/util/issuers.go` L87-112](https://github.com/cert-manager/cert-manager/blob/master/pkg/api/util/issuers.go)).
  That source is `master`, not a pinned cert-manager release, and no reissue was measured.
- **The hook's CRD grant is not used by its code.** *(widened 2026-09-27, below)*
  [`pre-upgrade-rbac.yaml`](../../deploy/helm/valkey-operator/templates/pre-upgrade-rbac.yaml)
  grants `customresourcedefinitions: get,list,patch,update` under the comment ~~"Update the CRD to
  the latest schema before migrating existing CRs"~~ *(corrected 2026-09-27: since work list item
  1 the comment reads "Granted but unused: the hook lists and patches Valkey CRs only and never
  reads or writes a CRD (docs/security/privilege-footprint.md, H-3)." - the quote was the comment
  at `4a7543e`)*. `migrate.go` makes two API calls, `List` of a
  `ValkeyList` and `Patch` of a `Valkey`, and no non-test Go file under `cmd/` or `internal/`
  imports `apiextensions` (grep, 2026-09-27). The comment describes a step the code does not take.
  *(Widened 2026-09-27, re-read at `4a7543e`:)* the rule sits at
  [`pre-upgrade-rbac.yaml:42-50`](../../deploy/helm/valkey-operator/templates/pre-upgrade-rbac.yaml#L42-L50) with the comment at `:40-41`. The `valkeys` rule at
  [`:30-39`](../../deploy/helm/valkey-operator/templates/pre-upgrade-rbac.yaml#L30-L39) grants `get` (`:36`) and `update` (`:39`), which the code does not use
  either: its only calls are `List` ([`migrate.go:55`](../../cmd/migrate/migrate.go#L55)) and a merge `Patch`
  ([`:86`](../../cmd/migrate/migrate.go#L86)), and `client.New` ([`:46`](../../cmd/migrate/migrate.go#L46)) adds only API discovery for its REST
  mapper, which the default `system:discovery` binding covers (upstream default, not checked on a
  cluster). So the grant the code needs is `valkeys: list, patch`. The same false claim ~~is~~
  *(was, until work list item 1 on 2026-09-27)* in the chart values:
  [`values.yaml:149-150`](../../deploy/helm/valkey-operator/values.yaml#L147-L150) says the hook runs "ensuring the CRD schema and CR
  defaults are in place", and the hook never reads or writes a CRD. Both comments are work list
  item 1. *(Both fixed 2026-09-27, History. That leaves one tracked quote of the old comment as
  current: [H-3](../security/privilege-footprint.md#h-3), `privilege-footprint.md:173-178`, work
  list item 1b.)* The grant is also stated in
  [`trust-boundaries.md:13`](../security/trust-boundaries.md),
  [`privilege-footprint.md:125-134`](../security/privilege-footprint.md#the-pre-upgrade-hook) and
  [H-3](../security/privilege-footprint.md#h-3) (`:162-178`, which already says the CRD half is
  unused), and in [ADR 0013](../adr/0013-operator-is-cluster-wide-privileged.md) D10 (`:296-304`)
  and its residual-risk bullet "The pre-upgrade hook's cluster-wide CRD write grant" (`:580-581`).
  *(Added 2026-09-27 at 84a39c2:)* items 1 and 1b are **committed** in `bcc63c9`
  (2026-09-27 17:59), not only present in a working tree:
  `git show --stat bcc63c9` lists `pre-upgrade-rbac.yaml`, `values.yaml` and
  `privilege-footprint.md`, and `git grep -n "CRD schema\|Update the CRD" -- deploy/` prints
  nothing. The grant was never used, not even at the start:
  `git show 73f6efe:cmd/migrate/migrate.go | grep -c 'apiextensions\|CustomResourceDefinition'`
  prints `0` and `git log -S apiextensions --oneline -- cmd/` is empty.
- *(Added 2026-09-27 at 84a39c2)* **Only the CRD rule is a privilege the release namespace does
  not already hold.** The operator's own ServiceAccount is rendered into the same
  `{{ .Release.Namespace }}` as the hook's
  ([`serviceaccount.yaml:6`](../../deploy/helm/valkey-operator/templates/serviceaccount.yaml#L6),
  [`pre-upgrade-rbac.yaml:8`](../../deploy/helm/valkey-operator/templates/pre-upgrade-rbac.yaml#L8))
  and its ClusterRole grants `valkeys` `create, delete, get, list, patch, update, watch`
  ([`clusterrole.yaml:9-20`](../../deploy/helm/valkey-operator/templates/clusterrole.yaml#L9-L20)),
  a superset of the hook's `valkeys` rule. No other principal of the chart holds
  `customresourcedefinitions`: `grep -rn 'apiextensions\|customresourcedefinitions'
  deploy/helm/valkey-operator/templates/ config/rbac/` (without `crd.yaml`) finds only
  `pre-upgrade-rbac.yaml:43` and `:45`. So narrowing the hook's `valkeys` verbs is
  least-privilege hygiene; the security value of narrowing or retiring the hook is the CRD rule
  alone, for the hook's window.
- *(Added 2026-09-27 at 84a39c2)* **The hook objects are named after the chart fullname, not the
  release.** They are `<fullname>-upgrade` (ServiceAccount, ClusterRole, ClusterRoleBinding) and
  `<fullname>-pre-upgrade` (the Job,
  [`pre-upgrade-job.yaml:5`](../../deploy/helm/valkey-operator/templates/pre-upgrade-job.yaml#L5));
  `<fullname>` is the release name only when it contains the chart name, else
  `<release>-valkey-operator`
  ([`_helpers.tpl:11-22`](../../deploy/helm/valkey-operator/templates/_helpers.tpl#L11-L22)). ADR 0013
  D10 (`:296`), `privilege-footprint.md:127` and `trust-boundaries.md:13` say `<release>-upgrade`,
  and [`upgrading.md:150`](../operations/upgrading.md) names the Job
  `valkey-operator-pre-upgrade`; each is correct only for a release name containing
  `valkey-operator` (work list item 1c). The delete policy
  `hook-succeeded,before-hook-creation`, without `hook-failed`, sits at `pre-upgrade-rbac.yaml:15`,
  `:28`, `:63` and `pre-upgrade-job.yaml:13`, so after a failed hook the Job and its pods stay as
  well as the RBAC objects.
- **Coverage.** `TestE2E_Migrate*` in
  [`migrate_e2e_test.go`](../../test/e2e/migrate_e2e_test.go) carries
  `//go:build e2e && e2e_helm`, and no CI workflow runs it
  ([ticket 043](043-lint-and-vet-skip-every-build-tagged-test-file.md)).
  *(Added 2026-09-27 at 84a39c2:)* even when run, `TestE2E_MigrateDefaults` executes the binary
  locally with the tester's inherited `KUBECONFIG`
  ([`migrate_e2e_test.go:136-147`](../../test/e2e/migrate_e2e_test.go#L136-L147)), so it exercises
  neither the hook's ServiceAccount nor Helm's ordering, and it fabricates its "old CR" with an
  explicit `secretPasswordKey: ""` under a `secretName` (`:125-128`) - a state no released CRD
  produces from an absent field; only a request that sends `""` explicitly stores it, and that CR
  cannot deploy (above). The test that runs the real hook under its ServiceAccount is
  `TestE2E_FleetUpgrade` ([`fleet_upgrade_test.go`](../../test/e2e/fleet_upgrade_test.go), tag
  `e2e && fleetupgrade`, local Kind only through `make e2e-fleet-upgrade-local`,
  [`Makefile:229-238`](../../Makefile#L229-L238)): it upgrades to the local chart (`:320-336`,
  `helmRun` fails the test on a failed upgrade) and asserts the hook succeeded through the Job or
  its `Completed` Event (`requirePreUpgradeHookSucceeded`, `:855-884`, `t.Fatalf` at `:882`).
  [ADR 0017](../adr/0017-test-and-ci-policy.md) D55 (`:1039`) records it green from chart 1.12.8
  on 2026-09-26 with today's grant. Its fleet sets every one of the six fields explicitly or
  omits the parent (`fleet_upgrade_test.go:644` size `256Mi`; `tlsSpec()` sets `group:
  cert-manager.io`, [`tls_test.go:311-322`](../../test/e2e/tls_test.go#L311-L322); no `auth`), so
  in that run the hook patches nothing and exercises only `List` and discovery. No integration
  test asserts any of the six defaults, although ADR 0017 D14 (`:394-397`) puts CRD-default
  assertions in envtest: every CR in `test/integration` sets them explicitly (for example
  `integration_test.go:379`, `pod_security_test.go:39`).

**Not verified:**

- ~~**That the hook ever patches anything.** The Kubernetes documentation for CRD structural
  schemas says defaults are applied in the request to the API server and again when an object is
  read from etcd. If that holds, every CR was stored with the six defaults when it was created or
  updated, the hook reads each one through the API server with them filled in, and
  `applyDefaults` reports a change only for a present empty value (`secretPasswordKey: ""`,
  `issuer.group: ""`, a `size` of `0`). That is upstream documentation plus the markers' history
  above; it was not measured here.~~ *(corrected 2026-09-27 at 84a39c2: read in the pinned
  upstream source and confirmed by the 104-tag scan, now under Verified; still not measured
  against an API server - work list items 2 and 3 do that in the repository's own tiers.)* The
  analysis of the 1.11.0 fleet upgrade
  ([archive/038, section 3.5](archive/038-fleet-upgrade-analysis-1-10-48-to-1-11-0.md#35-pre-upgrade-hook))
  expected the hook to set `issuer.group` on CRs that omit it in Git, which this reading
  contradicts for the stored object; nobody measured which one holds. *(Added 2026-09-27 at
  84a39c2: archive/038 `:317` also states Helm applies "CRD -> ClusterRole -> pre-upgrade Job ->
  Deployment", which Helm's hook documentation contradicts (L30); the archive is history and is
  not relied on here.)*
- **The upgrade order.** Helm's hook documentation says a `pre-upgrade` hook runs after the
  templates are rendered and before any resource of the release is updated, and this chart ships
  its CRD as a template (`templates/crd.yaml`), not under `crds/`. Read that way, the hook runs
  against the previous release's CRD, and a line for a field that the same release introduces is
  pruned from the patch as an unknown field, so it would take effect one upgrade later at the
  earliest. The ordering and the pruning were read, not run. *(Added 2026-09-27 at 84a39c2: still
  not run, but upstream-read and already the stated reason in an accepted ADR. Order:
  [charts_hooks.md L30](https://github.com/helm/helm-www/blob/main/docs/topics/charts_hooks.md);
  `ls -a deploy/helm/valkey-operator` prints `Chart.yaml templates values.yaml` - no `crds/`, no
  `values.schema.json`. Pruning: `templates/crd.yaml` carries no
  `x-kubernetes-preserve-unknown-fields` (grep), pruning per
  [custom-resource-definitions.md L332](https://github.com/kubernetes/website/blob/main/content/en/docs/tasks/extend-kubernetes/custom-resources/custom-resource-definitions.md),
  and `migrate.go:46` uses plain `client.New` without `WithFieldValidation`, so the API server's
  default field validation (`Warn`) drops the field rather than refusing the patch. ADR 0032
  `:419-423` rejected a hook pin for this reason.)*
- ~~Whether a code path other than those listed above reads one of the six fields without a
  fallback. Not audited.~~ *(corrected 2026-09-27 at 84a39c2: audited, see Verified - only
  `secretPasswordKey` has a storable empty value and fallback-free readers.)*
- *(added 2026-09-27)* That the hook makes no request beyond `List` and `Patch` at runtime: read
  from the code, not from an API audit log of a hook run. *(Added 2026-09-27 at 84a39c2: by code,
  `migrate.go:46`, `:55`, `:86`, and controller-runtime v0.25.1 source (`go.mod:16`):
  `pkg/client/client.go:200` builds a `DynamicRESTMapper`, which calls only discovery
  (`pkg/client/apiutil/restmapper.go:170`, `:206`), covered by `system:discovery`
  ([rbac.md L614-616](https://github.com/kubernetes/website/blob/main/content/en/docs/reference/access-authn-authz/rbac.md)).
  What would settle it: a `TestE2E_FleetUpgrade` run with the narrowed role of decision 2 and a
  seeded CR the hook must patch (a 403 fails the Job and the test), or an API audit log of a hook
  run on Kind.)*
- *(added 2026-09-27 at 84a39c2)* That Flux re-applies a value the hook patches into a field Git
  sets to `""`. Expected from the server-side-apply contract (Flux owns the field and re-applies
  its value on the next reconcile), not measured.
- *(added 2026-09-27 at 84a39c2)* The production fleet: whether any CR holds an explicit empty
  value the hook would change (work list item 4, the owner's read-only query; expected empty).

**Related tickets (2026-09-27 at 84a39c2):**

- [T44](044-test-e2e-helm-points-its-test-at-a-binary-that-is-not-there.md) ~~already says its
  binary-path fix and Kind run are wasted under this ticket's C.~~ *(corrected 2026-09-27,
  consistency pass: 044's re-verification of the same day takes its binary-path fix now, as
  decision-free work that does not wait on this ticket, and accepts that C deletes it - one token
  and two doc lines; only its Kind verification run is at stake under C. 044 also records the
  kubeconfig-user point below and names `TestE2E_FleetUpgrade` as the harness that runs the real
  hook.)* It misses that even under A
  `TestE2E_MigrateDefaults` runs under the tester's kubeconfig (above), so `test-e2e-helm` can
  never verify the hook's role; decision 2's harness is `TestE2E_FleetUpgrade`. The false
  premise comments in `migrate_e2e_test.go` belong to work list item 1c here, not to 044.
- [T43](043-lint-and-vet-skip-every-build-tagged-test-file.md): its proposed lint tag list
  includes `e2e_helm`, which disappears under C. 043's close step cites this ticket's link to 043
  by line (`047:109`); that line moves with this revision, so the close step re-greps.
- T56 states the hook ClusterRole at `pre-upgrade-rbac.yaml` ~~lines 29-49; the rules are at
  `:30-50`~~ *(corrected 2026-09-27, consistency pass: 056 cites `:29-50` since its
  re-verification, the `rules:` key at `:29` and both rules through `:50`; consistent)*, and they
  change under decision 2's G1 or decision 1's C.
- T58: no CI gate renders the chart, so G1's "renders exactly `list, patch`" and C's "renders no
  hook object" stay manual `helm template` checks until 058's gate exists.
- T49 notes that a selector on the chart's selector labels also selects the hook pod
  (`pre-upgrade-job.yaml:19-20`); under C that caveat disappears.
- T53 counts the hook (`pre-upgrade-job.yaml:34`) among the tag-pinned consumers of the operator
  image; under C it drops out.
- [archive/031](archive/031-generated-pods-run-as-root.md) `:516` and ADR 0017 D55 record the hook completing in the
  2026-09-26 fleet-upgrade run with today's wide grant - the baseline G1's rerun compares
  against.

## Impact

A contributor adding a defaulted field (DEVELOPER.md, "Adding things", CRD field step 6) has no
rule, so what the hook holds drifts by habit: a line added for a new field duplicates its marker
and, per the ordering read above, may not act in the release that introduces it. Nothing breaks
today. *(Added 2026-09-27 at 84a39c2:)* the hook still runs as a blocking step of every upgrade
(Fact), so an upgrade can fail on a hook that, on every CR a released chart created without an
explicit empty value, has nothing to do.

What is live today is the grant: every `helm upgrade` with the default values takes
`valkeys: get,list,patch,update` and `customresourcedefinitions: get,list,patch,update`
cluster-wide for the lifetime of the hook Job *(2026-09-27: of which the code uses `valkeys: list,
patch` only)*
([H-3](../security/privilege-footprint.md#h-3), [ADR 0013](../adr/0013-operator-is-cluster-wide-privileged.md)
D10), and the CRD half is used by no code. ~~`security: hardening`, because the rule itself adds or
removes no attack path; option C would additionally remove that grant.~~ *(corrected 2026-09-27
at 84a39c2: `security: hardening` holds - there is no attack path beyond the reach the operator's
own ServiceAccount already has - but the hardening row asks what the fix would additionally
cover, and that is narrower than "that grant". Per case: the principal is the operator image the
hook runs ([`pre-upgrade-job.yaml:34`](../../deploy/helm/valkey-operator/templates/pre-upgrade-job.yaml#L34);
a compromised hook image is the case `privilege-footprint.md:128-130` already names) or anyone
who may create pods in the release namespace, since a pod may name any ServiceAccount of its
namespace; the verb is `patch`/`update` (and `get`/`list`) on `customresourcedefinitions`,
cluster-wide - the schema or conversion strategy of any CRD in the cluster; the window is the
hook Job on every upgrade, live today with the default values, and until the next upgrade after a
failed hook. The `valkeys` verbs add nothing, because the operator's ServiceAccount in the same
namespace holds a superset permanently.)*

[ADR 0005](../adr/0005-upgrade-neutral-defaults-and-anti-affinity.md) D1 bears on the question:
~~a new feature defaults to off, and a default that builds the same pod spec as the field's
absence rolls nothing, so for a field that conforms to D1, writing its default into stored CRs
changes no behaviour.~~ *(corrected 2026-09-27 at 84a39c2: D1 says a new feature defaults to off,
so an operator upgrade changes nothing (ADR 0005 `:124-126`); the half "a default that builds
the same pod spec as the field's absence rolls nothing" is DEVELOPER.md step 2
(`DEVELOPER.md:337-341`), not D1's text. Together they mean that for a field that conforms to D1,
writing its default into stored CRs changes no behaviour.)*

## Options

Two decisions, presented one at a time. *(Reordered 2026-09-27 at 84a39c2: the rule and the
hook's future is now decision 1, and the grant narrowing decision 2, because the recommended
outcome of the rule decision, C, deletes the role and so turns the narrowing into a sequencing
question. The option labels A, C, G1 and G3 are unchanged.)*

### Decision 1 — which rule governs `applyDefaults`, and whether the hook stays

**What the code does today.** `applyDefaults`
([`migrate.go:104-146`](../../cmd/migrate/migrate.go#L104-L146)) fills six fields when they are
empty and their parent is set, and the chart runs it as a blocking pre-upgrade hook on every
`helm upgrade` with default values ([`values.yaml:153`](../../deploy/helm/valkey-operator/values.yaml#L153)).
The API server fills the same six fields on every create and update request and on every read
from etcd, but only when the key is absent (`algorithm.go:45`), and all 104 release CRDs carry
the six defaults with the same values (tag scan). So the hook can change only a CR stored with an
explicit empty value, and each such change is either behaviour-neutral or repairs a CR that
cannot deploy: an explicit `issuer.group: ""` or `size: "0"` deploys and the patch changes no
built object (by reading); an auth-enabled `secretPasswordKey: ""` is refused by the API server
at the StatefulSet write and waits for the next operator upgrade; `secretPasswordKey: ""` without
a `secretName` is auth-disabled and unaffected. A line for a field the same release introduces
cannot act, because the hook runs against the previous release's CRD and the unknown field is
pruned (Helm order, relied on by ADR 0032 `:419-423`). In the Flux-applied fleet, a value Git
sets to `""` is expected to be re-applied after the hook patches it (not measured), and the
operator's own full `r.Update` of a CR re-defaults an explicit `""` in the two string fields
(Fact).

**What the choice changes, and what it does not.** It decides whether the chart keeps the hook,
the `migrate` subcommand and their tests, and which ADR records where defaults live - so step 6
of the CRD-field checklist gets a rule. It changes no CR, no pod and no CRD schema; the six
markers stay; nothing rolls. Under either option,
[ADR 0021](../adr/0021-per-resource-metrics-and-the-alert-that-was-missing.md) D7 (`:125-127`,
the hook's absence "breaks ... the upgrade path") and
[`upgrading.md:149-152`](../operations/upgrading.md) (the hook ensures "the new operator never
reconciles a CR that predates its defaults" - which the API server's read-time defaulting with
the new CRD provides, and the hook, running before that CRD, cannot) need correcting; that is
work list item 1c and needs no decision.

- **C — retire the hook and the `migrate` subcommand (recommended).** A new ADR states: defaults
  are schema markers; a default that needs code is a read-time getter or fallback, the pattern
  `GetSyncTimeout` ([`valkey_types.go:1450`](../../api/v1/valkey_types.go#L1450), a `5m` default
  with no marker), `GetObserverResources` (`:1408`), `GetSeccompProfile` (`:1431`) and
  `buildVolumeClaimTemplates` already use; nothing writes defaults into stored CRs on upgrade. It
  supersedes ADR 0013 D10 and closes its residual-risk bullet (`:580-581`) and H-3, amends ADR
  0021 D7, and amends ADR 0017 D34 (`:634-637`, four `cmd` entry points become three) and D55
  (`:1039`, the fleet upgrade no longer asserts a hook).
  **Scope** (`git grep -n -i 'pre-upgrade\|preUpgradeHook\|e2e_helm\|test-e2e-helm\|manager
  migrate\|cmd/migrate\|applyDefaults' -- ':!docs/tickets' ':!graphify-out'`, 30 files at
  84a39c2). Removed: `pre-upgrade-job.yaml`, `pre-upgrade-rbac.yaml`, `preUpgradeHook` in
  `values.yaml:147-164` (and the hook mentions at `values.yaml:11`, `:16`), `cmd/migrate/`
  (package and its two test files), the `migrate` dispatch and its import
  ([`cmd/main.go:18`](../../cmd/main.go#L18), `:137-141`), `test/e2e/migrate_e2e_test.go`, the
  `test-e2e-helm` target (`Makefile:175-178`). Changed in the same change, or the tree breaks:
  `test/e2e/fleet_upgrade_test.go` - `requirePreUpgradeHookSucceeded` (`:848-884`) is removed
  and the defaults read of work list item 3 stays, or `t.Fatalf` at `:882` fails the run once the
  chart renders no hook; its comments at `:18-19`, `:85`, `:331-336` go too. Documents:
  `README.md:153`, `:551`, `:559`, `:578-580`; `DEVELOPER.md:20-24` ("four modes"), `:46-47`,
  `:72`, `:189`, `:348-350`; `docs/operations/upgrading.md:149-153`,
  `docs/operations/installation.md:62`; `docs/developer/testing.md:18`, `:108`, `:182`,
  `package-map.md:21`, `:95`, `:106`, `architecture.md:88`; `docs/security/privilege-footprint.md:3`,
  `:125-134`, `:162-178`, `trust-boundaries.md:13`, `operator-pod-posture.md:3`, `:11`;
  `templates/_helpers.tpl:80`; ADR 0013 `:49`, `:230`, `:296-304`, `:580-581`, `:600`; ADR 0014
  `:27`, `:181`; ADR 0017 `:636`, `:926`, `:1039`; ADR 0021 `:126`; ADR 0033 `:151`, `:278`; ADR
  0035 `:80`; ADR 0036 `:137`, `:157` (the last four are structural or historical mentions and
  need at most a dated note). `CLAUDE.md:946` names the hook; editing `CLAUDE.md` needs the
  owner. False positives the implementer leaves alone: ADR 0030 `:255`, `:526` ("a pre-upgrade
  Sentinel pod"). `cmd/main_test.go` needs no removal: `hasSubcommand` is shared with `sidecar`
  and `observer`, and `migrate` is only a table key there (`:64`, `:69`).
  **One cost that is new** *(found 2026-09-27 at 84a39c2)*: with the dispatch gone,
  `./manager migrate` does not fail - `main` falls through to `flag.Parse`
  ([`cmd/main.go:151-157`](../../cmd/main.go#L151-L157)), which stops at the positional argument,
  and nothing checks `flag.Args()`, so the pod starts a full operator. That happens when an older
  chart, which still renders the hook, runs a newer image through an `image.tag` override
  ([`_helpers.tpl:69`](../../deploy/helm/valkey-operator/templates/_helpers.tpl#L69)); under the
  hook's grant that operator cannot run, so the Job fails or never completes and the upgrade fails
  (by reading, not run). C therefore keeps `migrate` as an explicit exit-0 subcommand that logs
  its retirement - a few lines in `cmd/main.go`. Refusing an unknown positional argument instead
  would turn the same mismatch into a failed upgrade and is not taken.
  **Upgrade note.** Hook objects are "not tracked or managed as part of the release"
  ([charts_hooks.md L92-97](https://github.com/helm/helm-www/blob/main/docs/topics/charts_hooks.md),
  L200-202), so a cluster whose last hook failed keeps the ClusterRole, ClusterRoleBinding and
  ServiceAccount `<fullname>-upgrade`, and the Job `<fullname>-pre-upgrade` with its failed pods,
  once the chart stops rendering them; `upgrading.md` gets a note with that manual cleanup.
  Leftover `preUpgradeHook` values keep working, because the chart has no `values.schema.json`.
  **Preconditions**, each valid under every outcome: work list items 2 (envtest defaults
  assertion), 3 (the defaults read in the fleet upgrade) and 4 (the owner's production
  query) are green or empty.
  **Cost:** M - two templates, one package, one make target, two e2e files and about 20 document
  locations, plus the three preconditions.
  **Consequences:** every upgrade loses a blocking step and the whole cluster-wide CRD write
  grant; H-3 and ADR 0013's residual risk close; the explicit-`""` repair is lost, and it was
  neither needed for a running cluster nor durable (Flux and the operator's own updates reverse
  or pre-empt it, Fact).
- **A — keep the hook; a line only for a default a marker cannot express.** The rule, in a new
  ADR (or ADR 0005): a marker default gets no line; a line is allowed only for a value computed
  from other fields or cluster state that has to be persisted in the CR. The six current lines
  qualify under neither half and are dropped or frozen with a dated note; step 6 cites the ADR.
  **Cost:** S - an ADR plus the DEVELOPER.md step. **Consequences:** the hook keeps running on
  every upgrade, blocking, and does nothing; it keeps the `valkeys` write grant (narrowed by
  decision 2's G1, or with the CRD rule if G1 is not taken). Its permitted category can only
  write fields the previous release's CRD already has (the order above), and in the Flux fleet
  only fields Git does not set; no such default exists today.

**C is recommended**, because each of its premises is checkable: (1) all 104 release CRDs
default the six fields and the API server applies them on create and on read
(`algorithm.go:45`, tag scan), which work list items 2 and 3 confirm in the repository's own
tiers; (2) a line for a new field cannot act in its release (Helm order plus pruning, the reason
ADR 0032 `:419-423` already gives); (3) the hook's reachable effects are behaviour-neutral or
repair a CR that cannot deploy (`validation.go:3038-3039`). **C beats A** because A keeps a
blocking upgrade step and a cluster-wide `valkeys` write grant alive for a category of default
that has no instance today and that, in the production fleet, could only write fields Git does
not own; global rule 2 forbids keeping that mechanism speculatively. **A is the fallback**,
with the six lines kept, if item 2 or item 3 contradicts the reading of read-time defaulting.

### Decision 2 — narrow the hook's grant ahead of decision 1's outcome

**What the code does today.** On every `helm upgrade` with default values the chart renders a
ServiceAccount, ClusterRole and ClusterRoleBinding `<fullname>-upgrade`
([`pre-upgrade-rbac.yaml:1-72`](../../deploy/helm/valkey-operator/templates/pre-upgrade-rbac.yaml))
that grant `valkeys: get,list,patch,update` (`:30-39`) and `customresourcedefinitions:
get,list,patch,update` (`:42-50`) cluster-wide for the life of the hook Job, and after a failed
hook until the next upgrade (`:15`, `:28`, `:63`). The code calls `List` (`migrate.go:55`) and a
merge `Patch` (`:86`) on `Valkey`, plus discovery. The operator's ServiceAccount in the same
namespace already holds a superset of the `valkeys` rule; the CRD rule is held by no other
principal of the chart (Fact).

**What the choice changes, and what it does not.** Narrowing changes only the rendered
ClusterRole, its comment at `:17` ("read+patch access to Valkey CRs and the CRD itself") and the
documents that state the grant: ADR 0013 D10 (`:296-304`) and its residual risk (`:580-581`),
`privilege-footprint.md:125-134` and H-3 (`:162-178`), `trust-boundaries.md:13` - where the
object name is corrected to `<fullname>-upgrade` in the same edit. It rolls no pod (hook objects
are not part of any pod spec) and does not change what the hook does. Its only risk: a role that
is too narrow fails the hook Job and with it every `helm upgrade`, so it must run once in a real
upgrade before release. Under decision 1's C the chart edit is deleted again and the document
amendments are superseded; under A it is permanent.

- **G1 — `valkeys: list, patch` and nothing else, now (recommended).** Drop `get` and `update`
  from the `valkeys` rule and the whole `customresourcedefinitions` rule
  ([`pre-upgrade-rbac.yaml:30-50`](../../deploy/helm/valkey-operator/templates/pre-upgrade-rbac.yaml#L30-L50)),
  fix the comment at `:17`, amend ADR 0013 D10 in place and close its CRD residual-risk bullet,
  update `privilege-footprint.md` (the hook section and H-3) and `trust-boundaries.md:13`.
  **Cost:** XS in the chart, S with the documents, plus one `make e2e-fleet-upgrade-local` run on
  Kind (on an arm64 host with the amd64 1.12.8 image loaded under emulation, as ADR 0017 D55
  records) - the same run that carries work list item 3, so no extra Kind run. **Verification:**
  in that run, one fleet CR is seeded before the upgrade, through a raw merge patch, with
  `spec.auth: {secretPasswordKey: ""}` and no `secretName` (auth stays disabled, so it deploys -
  by reading, not run), read back as `""` through the dynamic client immediately before
  `helm upgrade`, and asserted to read `password` after it - otherwise the run needs no patch and
  would pass with a role missing `patch`. The read immediately before the upgrade is needed
  because any full `r.Update` of that CR by the old operator drops the `""` and the API server
  re-defaults it to `password` (Fact), which would make the post-upgrade assertion pass without
  the hook; a window between that read and the hook remains and is accepted; `requirePreUpgradeHookSucceeded` then proves the narrowed
  role suffices for `List`, discovery and `Patch`. **Consequences:** every upgrade stops taking a
  cluster-wide CRD write grant from the next release on; ADR 0013's residual-risk bullet and the
  CRD half of H-3 close without waiting for C.
- **G3 — no interim change; fold the narrowing into C.** Nothing now; decision 1's C deletes
  `pre-upgrade-rbac.yaml`. **Cost:** none now, and ADR 0013 D10 and two security pages are
  amended once instead of twice. **Consequences:** the unused CRD write grant stays on every
  upgrade until C ships - with 104 releases between 2026-02-18 and 2026-09-27, many upgrades.
  Right only when C is decided and ships in the same release as the narrowing would.

**G1 is recommended** because it is exactly the grant `migrate.go:55` and `:86` need, it closes
ADR 0013's residual risk on its own, its chart edit is XS, and its one risk is checked by the
fleet-upgrade run that item 3's defaults read needs anyway. **It beats G3** because C is
M effort with three preconditions and no date, so the CRD grant would ride every release until
then; its security value is the CRD rule alone (the `valkeys` narrowing is hygiene, Fact), and
that is what it removes early. If decision 1 is taken as C and C ships in the same release, G3
is right and G1 folds into C.

## Work list

1. **XS, no decision needed** *(added 2026-09-27)*: make the two false comments true. At
   [`pre-upgrade-rbac.yaml:40`](../../deploy/helm/valkey-operator/templates/pre-upgrade-rbac.yaml#L40-L41)
   say the rule is unused by the hook's code (it lists and patches `Valkey` objects only) and is
   kept pending the H-3 decision; at
   [`values.yaml:149-150`](../../deploy/helm/valkey-operator/values.yaml#L149-L150) drop "the CRD
   schema" and say the hook lists `Valkey` CRs and patches six field defaults into them, and does
   not read or write the CRD. Comments only; renders the same objects. Does not close this ticket.
   *(Review 2026-09-27:)* say "fills any of six field defaults that is empty" rather than
   "patches six field defaults": `applyDefaults` writes a field only when it is empty
   ([`migrate.go:104-146`](../../cmd/migrate/migrate.go#L104-L146)), and the Not-verified reading
   above expects it to patch nothing on most clusters.
   **Done 2026-09-27**, with one deviation: the values comment says "It lists and patches Valkey
   CRs only (cmd/migrate); it does not read or write the CRD." and does not take the "fills any
   of six field defaults that is empty" precision; the unchanged sentence above it still says
   the hook "migrates existing Valkey CRs to the field defaults of the current operator
   version", which is imprecise (it writes a field only when it is empty) but not false. The
   rule comment names H-3 rather than "pending the H-3 decision". *(Committed in `bcc63c9`,
   verified 2026-09-27 at 84a39c2.)*
1b. **XS, no decision needed** *(added 2026-09-27, found by the implementer of item 1)*:
   [H-3](../security/privilege-footprint.md#h-3), `privilege-footprint.md:173-178`, quotes the old
   rule comment, "Update the CRD to the latest schema before migrating existing CRs", as the
   comment on the rule today. Since item 1 that comment does not exist, so the sentence is false
   (rule 1). Strike the quote in place with a dated correction that gives the comment's current
   text, or drop the quote and keep the fact it supports (the CRD rule is granted and unused).
   Doc only; does not close this ticket. *(Done 2026-09-27, see History; committed in `bcc63c9`.)*
1c. **XS, no decision needed, urgency `now`** *(added 2026-09-27 at 84a39c2)*: correct the
   statements in tracked files that the Fact section shows false. If decision 1 is taken as C in
   the same change, delete them with the hook instead.
   - [`DEVELOPER.md:46-47`](../../DEVELOPER.md) ("writes field defaults into every existing
     Valkey CR") and `:348-350` ("writes six field defaults into every existing CR"): say it
     fills any of six fields stored empty - contradicted today by
     `TestMigrateAll_UpToDateCRIsNotPatched` and `migrate.go:81-83`.
   - [`upgrading.md:149-153`](../operations/upgrading.md): drop the claim that the hook ensures
     the new operator "never reconciles a CR that predates its defaults" and name what provides
     it (Helm renders and applies the CRD template before the Deployment - `InstallOrder` lists
     `CustomResourceDefinition` before `Deployment`,
     [helm `main` `pkg/release/v1/util/kind_sorter.go:46`, `:60`](https://github.com/helm/helm/blob/main/pkg/release/v1/util/kind_sorter.go),
     that `helm upgrade` applies in that order was not checked in source - and the API server
     defaults on read); name the Job `<fullname>-pre-upgrade` rather than `valkey-operator-pre-upgrade`.
   - The object name `<release>-upgrade` → `<fullname>-upgrade` in ADR 0013 D10 (`:296`),
     `privilege-footprint.md:127` and `trust-boundaries.md:13` (a dated correction in place).
   - Go and test comments: [`cmd/migrate/migrate.go:2-3`](../../cmd/migrate/migrate.go#L2-L3) and
     `:40-41` ("field defaults introduced in the current operator version" - none was, and one
     that is cannot be written, Not verified); `test/e2e/fleet_upgrade_test.go:331-335` ("writes
     current field defaults into CRs that predate them"); `test/e2e/migrate_e2e_test.go:7`,
     `:39`, `:60-62`, `:119-120`, `:125` (CRs created "before those fields were added to the CRD
     schema with defaults", "the field existed but had no default" - the tag scan refutes both).
   - ADR 0021 D7 (`:125-127`): a dated correction in place that the hook's absence does not break
     the upgrade path. It rests on upstream source reading, so it lands after item 2 or says
     explicitly that it is read from `apiextensions-apiserver` source and not measured.
2. **S, no decision needed, precondition of decision 1 and valid under every outcome**
   *(added 2026-09-27 at 84a39c2)*: an envtest assertion (ADR 0017 D14) in `test/integration`
   that a `Valkey` created with `spec.replicas`, `auth.secretPasswordKey`,
   `tls.certManager.issuer.group`, `sentinel.replicas`, `persistence.mode` and
   `persistence.size` absent (their parents present) is stored with `1`, `password`,
   `cert-manager.io`, `3`, `rdb` and `1Gi`, and that an explicit `secretPasswordKey: ""` is
   stored as `""`. **It must write raw JSON** - an unstructured create, or `client.RawPatch` as
   [`pod_hardening_test.go:101-104`](../../test/integration/pod_hardening_test.go#L101-L104)
   already does - and read the stored object from the write's response, because the integration
   client is cache-backed. A typed create would test the wrong thing twice: `omitempty` drops the
   explicit `""`, and a zero `Quantity` is sent as `size: "0"`, which is kept and never defaulted
   (Fact, measured). A mutation check: removing one marker fails it.
3. **XS, no decision needed** *(added 2026-09-27 at 84a39c2)*: in `TestE2E_FleetUpgrade`, add
   one fleet member that the released chart's CRD creates with five of the six fields absent and
   their parents present (`sentinel` without `replicas`, `persistence` without `mode` and `size`,
   `tls.certManager.issuer` without `group`, `auth: {}` without `secretName`, so auth stays
   disabled), created through the dynamic client as the fleet already is, and read it back
   through the dynamic client before `helm upgrade`: the five fields hold `3`, `rdb`, `1Gi`,
   `cert-manager.io` and `password`. `spec.replicas` stays explicit, because a member defaulted
   to one data pod beside three Sentinels is not a topology worth adding; item 2 covers it. That
   measures create-time defaulting on a real API server under a released CRD, so the hook has
   nothing to patch on that member; item 2 measures the current CRD in envtest. Under C it stays
   when `requirePreUpgradeHookSucceeded` goes; under A or with G1 it runs beside it. That the
   member converges under 1.12.8 is by reading, not run.
   *(Review 2026-09-27 at 84a39c2: the first version of this item asserted `metadata.generation`
   unchanged across the upgrade, on the premise that the operator's full `r.Update` sends back
   the object it read. That premise is false: the typed round trip adds `spec.resources: {}`
   (Fact, typed-client bullet), and the API server increments the generation for it
   (`strategy.go:179-185`), so the assertion could fail on a CR the operator updates for the
   first time after the upgrade, or pass only because an earlier update already stored
   `resources: {}`. Not adopted. The grep that backed the premise,
   `grep -rnE '(v|vk|valkey|cr)\.Spec\.[A-Za-z.]+ *= ' internal cmd` (hits only in
   `cmd/migrate`), still holds: the operator assigns no `Valkey` spec field; the generation bump
   comes from serialisation, not from an assignment.)*
4. **Owner, read-only, needs cluster access this run may not use** *(added 2026-09-27 at
   84a39c2)*: list production `Valkey` CRs holding an explicit empty value, the only CRs the hook
   can change, for example
   `kubectl get valkeys -A -o json | jq -r '.items[] | select(.spec.auth.secretPasswordKey=="" or .spec.tls.certManager.issuer.group=="" or ((.spec.persistence.size // "x")|tostring)=="0") | .metadata.namespace+"/"+.metadata.name'`.
   Expected empty; a hit with `auth.secretName` set is a blocked CR worth fixing in Git either way.
5. **Decision 1**, then its option (C: items 2-4 first, then the removal, the exit-0 `migrate`
   stub and the upgrade note listed under C; A: the ADR and the DEVELOPER.md step).
6. **Decision 2**, then its option (G1: the template, ADR 0013 D10, the two security pages, the
   verification run with the seeded CR), or nothing if G3 folds it into C.
7. **Closing (ADR 0034)**: whichever way, the rule goes into an ADR, the operator-visible
   consequence into `upgrading.md` and the security pages, the contributor rule into DEVELOPER.md
   step 6; then `git grep` for `T47` and `047` and move this file to `archive/`.

## Decision

None yet.

## Verification

- **Item 1** *(added 2026-09-27)*: `git grep -n "CRD schema\|Update the CRD" -- deploy/` finds
  nothing *(run 2026-09-27 after the fix: nothing; the parsed-object comparison below was not
  run - no `helm template`)*; `helm template` of the chart renders the same objects as before (comments only).
  *(Review 2026-09-27: compare the parsed objects, not the text. `helm template` prints the
  comment lines of a template, so the rendered `pre-upgrade-rbac.yaml` text changes with the
  comment at `:40`; the `values.yaml` comments do not render.)*
- **Item 1c** *(added 2026-09-27 at 84a39c2)*: `git grep -n 'every existing\|predate\|introduced in
  the current\|had no default\|<release>-upgrade'` outside `docs/tickets/` finds none of the
  listed statements as current; ADR 0021 D7 carries the dated correction.
- **Item 2**: the new test passes in `make test-integration` and fails when one of the six
  markers is removed (then restored).
- **Decision 2, G1:** `helm template` renders the ClusterRole with exactly `valkeys: list, patch`
  and no `apiextensions.k8s.io` rule; ~~a Kind upgrade with the hook enabled completes and its Job
  log reports the migration summary;~~ *(corrected 2026-09-27 at 84a39c2: a successful hook Job is
  deleted by `hook-succeeded` before `helm upgrade` returns, so its log cannot be read. The proof
  is one `make e2e-fleet-upgrade-local` run with the narrowed chart: `requirePreUpgradeHookSucceeded`
  green and the seeded CR (decision 2, G1) read back with `secretPasswordKey: password`;)* ADR
  0013 D10 and both security pages state the narrowed grant.
- **A:** the chosen ADR carries the rule, step 6 of the CRD-field checklist in
  DEVELOPER.md cites that ADR instead of the caveat that no rule decides yet, ADR 0021 D7 and
  `upgrading.md` are corrected (item 1c), and this ticket is archived.
- **C:** ~~before the removal, the measurement described under option C is green on Kind;~~
  *(corrected 2026-09-27 at 84a39c2: the Kind measurement from the oldest release is replaced by
  the tag scan plus items 2-4, because every release CRD carries the six defaults and the Job
  log it relied on is gone after success)* before the removal, items 2 and 3 are green and item 4
  is empty; after it,
  `helm template` of the chart renders no hook object, `grep -rn 'preUpgradeHook\|manager migrate'`
  over `deploy/`, `README.md` and `docs/` finds only history, H-3 and ADR 0013 D10 are updated in
  the same change, `make e2e-fleet-upgrade-local` is green with item 3's defaults read and without
  the hook assertion, `./manager migrate` exits 0 and logs its retirement, and `make test-unit`,
  `make lint` and `make generate-all` leave a clean tree.
  Step 6 of the checklist is replaced by a citation of the ADR that records the retirement.

## History

- 2026-09-27: re-verified at 84a39c2 (clean tree on `chore/maintenance-2026-09-27`), by an
  auditor, a facts skeptic and a design skeptic, with the disputed points re-checked by the
  editor. **Checked:** every location of the Fact section; `git show bcc63c9`; the tag history
  (`git tag --no-contains 0aaa3a2` empty, `git tag --contains 73f6efe --sort=creatordate`
  starts at `v1.2.0`, 94 of 104 tags); the upstream defaulting source
  (`apiextensions-apiserver@v0.37.1` `defaulting/algorithm.go:45`,
  `customresource_handler.go:1185-1195`, `:1289-1297`), Kubernetes v1.36.1
  `validation.go:3038-3039`, Helm `charts_hooks.md` L30, L76-79, L92-97, controller-runtime
  v0.25.1 `client.go:200` and `restmapper.go:170`, `:206`, cert-manager `master` `issuers.go`
  L87-112; the operator ClusterRole and the hook's namespace; `cmd/main.go:128-156`; the
  fleet-upgrade e2e (`:320-336`, `:644`, `:848-884`); the removal-scope grep. **Measured:** the
  tag scan (`python3 .../work/t047/chk.py`: `104 tags checked; mismatches: []`; the extended
  `val.py` with validation rules: `104 [] 0`), and the JSON encoding
  (`go run` of a scratch module, apimachinery v0.37.1: `{"size":"0"} {"secretName":"s"}`, re-run
  by the editor). No Docker container was started - the ticket makes no Valkey behaviour claim.
  **Found false or outdated:** the header's "read at `4a7543e`"; items 1 and 1b are committed in
  `bcc63c9`, not only in a working tree; the "Not verified" bullets on read-time defaulting and on
  unaudited readers (now verified or audited, corrected in place); the ADR 0005 D1 attribution
  (the "rolls nothing" half is DEVELOPER.md step 2); the threat line (named no additional
  coverage, and counted `valkeys` verbs the operator's ServiceAccount already holds); "decision 1
  cannot be wasted" (C deletes G1's edit); G1's verification through `TestE2E_Migrate*` and a Job
  log (the test runs under the tester's kubeconfig, the Job is deleted on success); C's Kind
  measurement (replaced by work list items 2-4) and its removal list (missed about 20 locations,
  among them `fleet_upgrade_test.go:882`, which would fail); the cleanup note (missed the failed
  Job) and the object name `<release>-upgrade` (it is `<fullname>-upgrade`). **New facts:** the
  hook is a blocking upgrade step; an auth-enabled `secretPasswordKey: ""` cannot deploy; the
  other patchable states are behaviour-neutral by reading; a typed client writes `size: "0"` and
  drops `""`; the CRD grant was never used since `73f6efe`; ADR 0032 `:419-423` and ADR 0021 D7
  bear on the question; removing the dispatch would make `./manager migrate` start an operator;
  new false statements in tracked files (item 1c). Locations re-read at 84a39c2 and fixed in
  place: markers `valkey_types.go:1029`, `:570`, `:578`, `:481`, `:999`, `:1008`; rule comment
  `pre-upgrade-rbac.yaml:40-41`, rule `:42-50`, delete policies `:15`, `:28`, `:63`; H-3
  `:162-178`; ADR 0013 D10 `:296-304`. **Options:** reordered - the rule is now decision 1 and
  the grant narrowing decision 2, labels unchanged. Removed: **G2** (drop only the CRD rule,
  keep `valkeys` get/update) - dominated by G1 at equal security value, same documents and run,
  and ADR 0013 D11's reason for keeping unused verbs does not transfer to the hand-written hook
  role (ADR 0014 `:181`); **B** (every new defaulted field gets a line) - false premise: the line
  cannot act in the release that introduces its field, and for older fields the API server
  already applies the marker; **D** (stage the retirement by defaulting `preUpgradeHook.enabled`
  to false for one release), proposed in this audit and not adopted - it protects no case the tag
  scan and the defaulting rule leave, doubles the documentation churn, and the exit-0 `migrate`
  stub covers the one real transition hazard more cheaply. G3 is kept, reframed as the
  sequencing consequence of decision 1. **Recommendations unchanged in label** (C for decision 1,
  A the fallback; G1 for decision 2), with changed justifications: G1 no longer claims it
  "cannot be wasted" or any security value for the `valkeys` verbs, and C rests on the tag scan,
  upstream source and items 2-4 instead of a Kind measurement, with the exit-0 stub and the
  extended scope added to its cost. **Work list:** new items 1c (false statements), 2 (envtest,
  raw JSON), 3 (a defaults-absent fleet member read before the upgrade; a first draft asserting
  `metadata.generation` unchanged was dropped by the review because the operator's typed
  `r.Update` stores `spec.resources: {}` and bumps the generation), 4 (owner's production query),
  7 (closing). **Review of the edit (same day):** also added the `resources: {}` fact, the
  pre-upgrade read of G1's seeded CR (the old operator's `r.Update` would otherwise re-default it
  before the hook runs), Helm's `InstallOrder` citation in item 1c, and restored the header's
  earlier correction unstruck with a separate re-verification note instead of a struck correction. **Frontmatter:**
  `state` filed -> analysed (facts verified, options weighed, nothing decided); `urgency` later
  -> now (rule 1: DEVELOPER.md contradicted by an executed unit test, the other statements of
  item 1c false by reading); `threat` rewritten to the hardening schema; `blocked-by` comment
  names both decisions; `severity` low, `security` hardening and `effort` M re-checked and kept.
  Cross-ticket: in the consistency pass of the same day, the T44 bullet was corrected (044 takes
  its binary-path fix now and accepts its loss under C; only its Kind run is at stake) and the T56
  bullet corrected (056 cites `pre-upgrade-rbac.yaml:29-50`, consistent).
- 2026-09-27: work list item 1b landed in the text-vs-code review of the maintenance branch: H-3
  in [`privilege-footprint.md`](../security/privilege-footprint.md#h-3) strikes the quote of the
  old rule comment in place, with a dated correction giving the comment's current substance.
  Urgency `now` -> `later` (rule 4), as this file said it would once 1b lands. **Verified:**
  `helm template` of the working tree against the chart at `HEAD` (`git archive`, default
  values) differs in the one rule comment only, and with comment lines removed the two renders
  are identical. **Not verified:** non-default values were not
  rendered.
- 2026-09-27: work list item 1 landed, file by file (read in `git diff` of the working tree):
  - [`deploy/helm/valkey-operator/values.yaml`](../../deploy/helm/valkey-operator/values.yaml)
    (`preUpgradeHook` comment, `:149-150`): "ensuring the CRD schema and CR defaults are in
    place before the new operator starts reconciling" became "It lists and patches Valkey CRs
    only (cmd/migrate); it does not read or write the CRD."
  - [`pre-upgrade-rbac.yaml`](../../deploy/helm/valkey-operator/templates/pre-upgrade-rbac.yaml)
    (`:40`, now `:40-41`): "Update the CRD to the latest schema before migrating existing CRs"
    became "Granted but unused: the hook lists and patches Valkey CRs only and never reads or
    writes a CRD (docs/security/privilege-footprint.md, H-3)." No rule, verb or value changed.

  Deviations recorded under item 1. New item 1b: the H-3 paragraph of `privilege-footprint.md`
  quotes the removed comment as current. **Urgency stays `now`, unchanged, rule 1 by a new
  reason:** the frontmatter's "back to `later` once item 1 lands" does not happen, because item
  1b is a false statement in a tracked file that item 1 created; once 1b lands, `later` (rule 4).
  Verified: `git grep -n "CRD schema\|Update the CRD" -- deploy/` prints nothing; `git grep -n
  "Update the CRD"` outside `docs/tickets/` finds only `privilege-footprint.md:175`. **Not
  verified:** no `helm template` was run, so the rendered objects were not compared (the rule
  comment renders; the values comments do not).
- 2026-09-27: adversarial review of the enrichment. Re-read at `4a7543e`:
  `pre-upgrade-rbac.yaml:15-49`, `:62`, `values.yaml:147-153`, `pre-upgrade-job.yaml:11-13`,
  `:36`, `migrate.go:46`, `:55`, `:66-69`, `:86`, `:104`, `cmd/main.go:137-141`,
  `Makefile:175-178`, `README.md:578-580`, `upgrading.md:149-153`, ADR 0013 `:289-291` hold.
  Two precisions to work list item 1: the values comment says the hook fills empty defaults
  rather than "patches six field defaults", and its verification compares parsed objects,
  because template comments render. G1 and C stay marked; item 1 is confirmed as XS with no
  decision. **Verified:** by reading and grep. **Not verified:** nothing was run, no
  `helm template`.
- 2026-09-27: enriched - re-verified at `4a7543e`; widened the unused-grant finding to the
  `valkeys` `get` and `update` verbs; named the two false comments (`values.yaml:149-150`,
  `pre-upgrade-rbac.yaml:40`) as an XS no-decision item; split the grant narrowing out as decision
  1 (G1 recommended, taken first) ahead of the rule (decision 2, C still recommended); added the
  leftover-grant cost of C and its removal list. Urgency `later` -> `now` by rule 1 (the two
  comments are false by code reading; back to `later` under rule 4 once item 1 lands); effort
  `S` -> `M` (the recommended option C); the threat line names the unused verbs. **Verified:** by
  reading, grep and `git diff f5c6886 4a7543e`. **Not verified:** nothing was run; Helm's
  handling of hook objects a chart stops rendering is from its documentation.
- 2026-09-27 — filed from the documentation restructure. While verifying, found that the hook's
  CRD grant is used by no code, that every release tag already carried all six default markers
  before the hook existed, and that by Helm's documented order the hook runs against the
  previous release's CRD (read, not measured).
