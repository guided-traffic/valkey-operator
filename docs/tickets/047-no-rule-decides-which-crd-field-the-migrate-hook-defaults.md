---
id: T47
title: no rule decides when a new CRD field needs a line in the migrate hook's applyDefaults
state: filed
severity: low         # nothing behaves wrongly today; the gap is a missing rule and a grant it may not need
security: hardening
threat: "no attacker; every helm upgrade with default values takes a cluster-wide valkeys and CRD write grant (H-3) whose CRD rule and valkeys get/update verbs the hook's code never uses - narrowing it (decision 1) or retiring the hook (option C) removes that"
urgency: later        # rule 4 since 2026-09-27: items 1 and 1b landed (values.yaml, pre-upgrade-rbac.yaml, H-3 were false by code reading and are corrected); rule 1 held until then
effort: M             # the recommended option C (chart, subcommand, tests, docs, a Kind measurement); A or B is S. Was S until 2026-09-27
blocked-by: decision  # which option, below
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

## Fact

The open question: **when does a new CRD field need a line in `applyDefaults`**
([`cmd/migrate/migrate.go:104`](../../cmd/migrate/migrate.go#L104-L146))? No ADR answers it. A grep of
`docs/adr/` for `applyDefaults`, `migrate.go` and `cmd/migrate` on 2026-09-27 found only
[ADR 0017](../adr/0017-test-and-ci-policy.md) D34, which lists `migrate.Run` among the entry
points deliberately not unit-tested; [ADR 0013](../adr/0013-operator-is-cluster-wide-privileged.md)
D10 names "the field-default migration it performs" as the cost of disabling the hook and says
nothing about what belongs in it.

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
- **What `applyDefaults` writes: six fields, each only when empty.** `spec.replicas` 0 → 1;
  `spec.auth.secretPasswordKey` `""` → `password` (when `spec.auth` is set);
  `spec.tls.certManager.issuer.group` `""` → `cert-manager.io` (when `spec.tls.certManager` is
  set); `spec.sentinel.replicas` 0 → 3 (when `spec.sentinel` is set); `spec.persistence.mode`
  `""` → `rdb` and `spec.persistence.size` zero → `1Gi` (when `spec.persistence` is set). Each has
  a unit test in [`migrate_test.go`](../../cmd/migrate/migrate_test.go).
- **All six already carry a `+kubebuilder:default` marker with the same value** in
  [`api/v1/valkey_types.go`](../../api/v1/valkey_types.go): line 1027 (`replicas`, 1), 568
  (`password`), 576 (`cert-manager.io`), 479 (Sentinel `replicas`, 3), 997 (`rdb`), 1006
  (`1Gi`). The generated chart CRD carries each as `default:`
  ([`templates/crd.yaml`](../../deploy/helm/valkey-operator/templates/crd.yaml) lines 579, 122,
  794, 692, 448, 460).
- **Every release shipped those markers, and the hook came later.** All six are in `0aaa3a2`
  (2026-02-17, "feat: add Foundation & CRD"). Every release tag contains that commit
  (`git tag --no-contains 0aaa3a2` prints nothing; the first tag is `v1.0.0`), and the hook
  arrived in `73f6efe` (2026-03-02, "feat: Operator Self-Upgrade / Cluster Migration"). So
  whichever release's CRD is installed while the hook runs, it carries all six defaults.
- **Where the marker and the hook differ.** A structural-schema default fills an absent field
  only; `applyDefaults` also rewrites a present empty value. Per field: `replicas` and
  `sentinel.replicas` carry `Minimum=1` and `persistence.mode` an enum (`rdb;aof;both`), so an
  explicit empty value cannot be stored. `secretPasswordKey` has no length rule, so `""` can be
  stored, and the operator then reads the Secret key `""`
  ([`valkey_controller.go:188`](../../internal/controller/valkey_controller.go),
  [`checker.go:86`](../../internal/health/checker.go), and the `secretKeyRef` of every builder).
  An empty `issuer.group` is left out of the issuer reference by the builder
  ([`certificate.go:183`](../../internal/builder/certificate.go), line 227). A `size` of `0`
  falls back to `1Gi` in `buildVolumeClaimTemplates`
  ([`statefulset.go:1191-1195`](../../internal/builder/statefulset.go)). The hook repairs an
  explicit `secretPasswordKey: ""` only at the next upgrade, never on create, so it is no
  guarantee for that case either.
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
  [`pre-upgrade-rbac.yaml:40-49`](../../deploy/helm/valkey-operator/templates/pre-upgrade-rbac.yaml#L40-L49) with the comment at `:40`. The `valkeys` rule at
  [`:30-39`](../../deploy/helm/valkey-operator/templates/pre-upgrade-rbac.yaml#L30-L39) grants `get` (`:36`) and `update` (`:39`), which the code does not use
  either: its only calls are `List` ([`migrate.go:55`](../../cmd/migrate/migrate.go#L55)) and a merge `Patch`
  ([`:86`](../../cmd/migrate/migrate.go#L86)), and `client.New` ([`:46`](../../cmd/migrate/migrate.go#L46)) adds only API discovery for its REST
  mapper, which the default `system:discovery` binding covers (upstream default, not checked on a
  cluster). So the grant the code needs is `valkeys: list, patch`. The same false claim ~~is~~
  *(was, until work list item 1 on 2026-09-27)* in the chart values:
  [`values.yaml:149-150`](../../deploy/helm/valkey-operator/values.yaml#L147-L150) says the hook runs "ensuring the CRD schema and CR
  defaults are in place", and the hook never reads or writes a CRD. Both comments are work list
  item 1. *(Both fixed 2026-09-27, History. That leaves one tracked quote of the old comment as
  current: [H-3](../security/privilege-footprint.md#h-3), `privilege-footprint.md:173-176`, work
  list item 1b.)* The grant is also stated in
  [`trust-boundaries.md:13`](../security/trust-boundaries.md),
  [`privilege-footprint.md:125-134`](../security/privilege-footprint.md#the-pre-upgrade-hook) and
  [H-3](../security/privilege-footprint.md#h-3) (`:161-173`, which already says the CRD half is
  unused), and in [ADR 0013](../adr/0013-operator-is-cluster-wide-privileged.md) D10 (`:289-297`).
- **Coverage.** `TestE2E_Migrate*` in
  [`migrate_e2e_test.go`](../../test/e2e/migrate_e2e_test.go) carries
  `//go:build e2e && e2e_helm`, and no CI workflow runs it
  ([ticket 043](043-lint-and-vet-skip-every-build-tagged-test-file.md)).

**Not verified:**

- **That the hook ever patches anything.** The Kubernetes documentation for CRD structural
  schemas says defaults are applied in the request to the API server and again when an object is
  read from etcd. If that holds, every CR was stored with the six defaults when it was created or
  updated, the hook reads each one through the API server with them filled in, and
  `applyDefaults` reports a change only for a present empty value (`secretPasswordKey: ""`,
  `issuer.group: ""`, a `size` of `0`). That is upstream documentation plus the markers' history
  above; it was not measured here. The analysis of the 1.11.0 fleet upgrade
  ([archive/038, section 3.5](archive/038-fleet-upgrade-analysis-1-10-48-to-1-11-0.md#35-pre-upgrade-hook))
  expected the hook to set `issuer.group` on CRs that omit it in Git, which this reading
  contradicts for the stored object; nobody measured which one holds.
- **The upgrade order.** Helm's hook documentation says a `pre-upgrade` hook runs after the
  templates are rendered and before any resource of the release is updated, and this chart ships
  its CRD as a template (`templates/crd.yaml`), not under `crds/`. Read that way, the hook runs
  against the previous release's CRD, and a line for a field that the same release introduces is
  pruned from the patch as an unknown field, so it would take effect one upgrade later at the
  earliest. The ordering and the pruning were read, not run.
- Whether a code path other than those listed above reads one of the six fields without a
  fallback. Not audited.
- *(added 2026-09-27)* That the hook makes no request beyond `List` and `Patch` at runtime: read
  from the code, not from an API audit log of a hook run.

## Impact

A contributor adding a defaulted field (DEVELOPER.md, "Adding things", CRD field step 6) has no
rule, so what the hook holds drifts by habit: a line added for a new field duplicates its marker
and, per the ordering read above, may not act in the release that introduces it. Nothing breaks
today.

What is live today is the grant: every `helm upgrade` with the default values takes
`valkeys: get,list,patch,update` and `customresourcedefinitions: get,list,patch,update`
cluster-wide for the lifetime of the hook Job *(2026-09-27: of which the code uses `valkeys: list,
patch` only)*
([H-3](../security/privilege-footprint.md#h-3), [ADR 0013](../adr/0013-operator-is-cluster-wide-privileged.md)
D10), and the CRD half is used by no code. `security: hardening`, because the rule itself adds or
removes no attack path; option C would additionally remove that grant.

[ADR 0005](../adr/0005-upgrade-neutral-defaults-and-anti-affinity.md) D1 bears on the question:
a new feature defaults to off, and a default that builds the same pod spec as the field's
absence rolls nothing, so for a field that conforms to D1, writing its default into stored CRs
changes no behaviour.

## Options

Two decisions. **Decision 1 comes first**: it is independent of decision 2, and every outcome of
decision 2 agrees with its recommended option, so it cannot be wasted.

### Decision 1 — narrow the hook's grant to what its code calls *(added 2026-09-27)*

- **G1 — `valkeys: list, patch` and nothing else (recommended).** Drop `get` and `update` from the
  `valkeys` rule and the whole `customresourcedefinitions` rule
  ([`pre-upgrade-rbac.yaml:30-49`](../../deploy/helm/valkey-operator/templates/pre-upgrade-rbac.yaml#L30-L49)),
  and fix the ClusterRole comment at `:17`. Cost XS in the chart, S with its documents: ADR 0013
  D10 amended in place, `privilege-footprint.md` (the hook section and H-3), `trust-boundaries.md:13`.
  Rolls nothing; the hook objects are recreated on every upgrade anyway (`before-hook-creation`).
  Leaves the `valkeys` write grant, which the hook needs as long as it exists.
- **G2 — drop only the CRD rule.** Removes the part ADR 0013 D10 calls a cluster-wide
  schema-change grant; keeps two unused `valkeys` verbs. Same documents, marginally smaller diff.
- **G3 — leave it until decision 2.** Costs nothing now; every upgrade keeps taking the unused CRD
  write grant until option C lands, and C first needs a Kind measurement.

G1 is marked because it is the grant the code needs and nothing more, and because A and B keep a
hook that needs exactly `list` and `patch` while C deletes the rule outright. Its one gap is
verification: `TestE2E_Migrate*` is not run by CI and `make test-e2e-helm` points at a missing
binary ([044](044-test-e2e-helm-points-its-test-at-a-binary-that-is-not-there.md)), so a hook run
with the narrowed role needs a manual Kind upgrade and its Job log.

### Decision 2 — which rule governs `applyDefaults`

- **A — only a default the operator needs and a marker cannot express gets a line**, for
  example one computed from another field or from cluster state. The rule goes into ADR 0005 or a
  new ADR, and step 6 cites it. Cost: an ADR amendment. It leaves open what happens to the six
  existing lines, none of which qualifies, and keeps the grant on every upgrade for a hook that
  under this rule has nothing to do today. A default that needs code also has a home that needs
  no grant: a read-time fallback or getter, the pattern `buildVolumeClaimTemplates` already uses
  for `size` and `GetObserverResources` / `GetSeccompProfile` use in
  [`valkey_types.go`](../../api/v1/valkey_types.go).
- **B — every new defaulted field gets a line.** Cost: it duplicates every marker and keeps the
  grant, and, per the unverified upgrade order, the line cannot act in the release that
  introduces the field. For a feature that defaults to off (ADR 0005 D1) the written default
  changes nothing anyway.
- **C — retire `applyDefaults` and the hook (best, once the measurement below holds).** Defaults
  live in markers, and a default that needs code lives in a read-time fallback; nothing writes
  into stored CRs on upgrade. It removes `pre-upgrade-job.yaml`, `pre-upgrade-rbac.yaml`,
  `preUpgradeHook` in `values.yaml` and the README values table, the `migrate` subcommand, its
  unit tests and `TestE2E_Migrate*`, and closes H-3 and the ADR 0013 D10 residual risk. Cost: M,
  plus a measurement first: an upgrade on Kind from the oldest supported release with
  `preUpgradeHook.enabled: false`, reading the six fields of a CR created under that release back
  from the API server after the upgrade; and, with the hook enabled, its Job log reporting no
  migrated CR for that fleet. An explicit `secretPasswordKey: ""` loses its upgrade-time repair,
  which it never had on create.
  *(Added 2026-09-27:)* one cost this misses. The hook's ServiceAccount, ClusterRole and
  ClusterRoleBinding carry `hook-succeeded,before-hook-creation` and no `hook-failed`
  ([`pre-upgrade-rbac.yaml:15`](../../deploy/helm/valkey-operator/templates/pre-upgrade-rbac.yaml#L15),
  `:28`, `:62`; ADR 0013 D10). A cluster whose last hook failed keeps `<release>-upgrade` and its
  grant until the next upgrade's `before-hook-creation`, and once the templates are gone that
  never comes. C therefore needs an [upgrading.md](../operations/upgrading.md) note with the manual
  cleanup (the ClusterRoleBinding and ClusterRole `<release>-upgrade`, and the ServiceAccount of
  that name in the release namespace). That Helm leaves behind the hook objects a newer chart no
  longer renders is from Helm's hook documentation, not verified here. The removal list, with
  locations: `cmd/migrate/`, the dispatch at `cmd/main.go:137-141` and its test in
  `cmd/main_test.go`, the two templates, `values.yaml:147-164`, `README.md:578-580`,
  `upgrading.md:149-153`, `docs/developer/testing.md:18` and `:108`, the `test-e2e-helm` target
  (`Makefile:175-178`), `test/e2e/migrate_e2e_test.go`, and `DEVELOPER.md:46-47` and `:348-350`.

C is marked because every line the hook holds duplicates a marker that every released CRD
carries and that the API server applies on create and on read (upstream-documented, to be
measured), because a line for a field the same release
introduces cannot act in that release if the ordering reads correctly, and because the hook makes
every upgrade take a cluster-wide write grant on `valkeys` and on every CRD in the cluster, the
CRD half of which its code never uses. If the measurement contradicts the read-time defaulting,
A is the fallback.

Independently of this decision, the CRD rule of the hook's ClusterRole is unused by the code
(Fact). ~~Narrowing it is a separate decision under H-3 and is not taken here.~~ *(2026-09-27: it is
decision 1 above.)*

## Work list

1. **XS, no decision needed** *(added 2026-09-27)*: make the two false comments true. At
   [`pre-upgrade-rbac.yaml:40`](../../deploy/helm/valkey-operator/templates/pre-upgrade-rbac.yaml#L40)
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
   rule comment names H-3 rather than "pending the H-3 decision".
1b. **XS, no decision needed** *(added 2026-09-27, found by the implementer of item 1)*:
   [H-3](../security/privilege-footprint.md#h-3), `privilege-footprint.md:173-176`, quotes the old
   rule comment, "Update the CRD to the latest schema before migrating existing CRs", as the
   comment on the rule today. Since item 1 that comment does not exist, so the sentence is false
   (rule 1). Strike the quote in place with a dated correction that gives the comment's current
   text, or drop the quote and keep the fact it supports (the CRD rule is granted and unused).
   Doc only; does not close this ticket. *(Done 2026-09-27, see History.)*
2. **Decision 1**, then its option (G1: the template, ADR 0013 D10, the two security pages).
3. **Decision 2**, then its option (C: the Kind measurement first, then the removal and the
   cleanup note listed under C; A or B: the ADR and the DEVELOPER.md step).

## Decision

None yet.

## Verification

- **Item 1** *(added 2026-09-27)*: `git grep -n "CRD schema\|Update the CRD" -- deploy/` finds
  nothing *(run 2026-09-27 after the fix: nothing; the parsed-object comparison below was not
  run - no `helm template`)*; `helm template` of the chart renders the same objects as before (comments only).
  *(Review 2026-09-27: compare the parsed objects, not the text. `helm template` prints the
  comment lines of a template, so the rendered `pre-upgrade-rbac.yaml` text changes with the
  comment at `:40`; the `values.yaml` comments do not render.)*
- **Decision 1, G1:** `helm template` renders the ClusterRole with exactly `valkeys: list, patch`;
  a Kind upgrade with the hook enabled completes and its Job log reports the migration summary;
  ADR 0013 D10 and both security pages state the narrowed grant.
- **A or B:** the chosen ADR carries the rule, step 6 of the CRD-field checklist in
  DEVELOPER.md cites that ADR instead of the caveat that no rule decides yet, and this ticket is
  archived.
- **C:** before the removal, the measurement described under option C is green on Kind; after it,
  `helm template` of the chart renders no hook object, `grep -rn 'preUpgradeHook\|manager migrate'`
  over `deploy/`, `README.md` and `docs/` finds only history, H-3 and ADR 0013 D10 are updated in
  the same change, and `make test-unit`, `make lint` and `make generate-all` leave a clean tree.
  Step 6 of the checklist is replaced by a citation of the ADR that records the retirement.

## History

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
