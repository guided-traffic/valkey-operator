---
id: T47
title: no rule decides when a new CRD field needs a line in the migrate hook's applyDefaults
state: filed
severity: low         # nothing behaves wrongly today; the gap is a missing rule and a grant it may not need
security: hardening
threat: "no attacker; if the hook is retired (option C), no upgrade takes the cluster-wide valkeys and CRD write grant of H-3 any more - a grant whose CRD half the hook's code never uses"
urgency: later        # rule 4: options A and B are a cheap known fix; no false statement, severity below medium
effort: S             # A or B: an ADR amendment and one DEVELOPER.md line; C is M (chart, subcommand, tests, docs)
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
`deploy/helm/`). No make target and no cluster was run for this file.

## Fact

The open question: **when does a new CRD field need a line in `applyDefaults`**
([`cmd/migrate/migrate.go:104`](../../cmd/migrate/migrate.go))? No ADR answers it. A grep of
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
- **The hook's CRD grant is not used by its code.**
  [`pre-upgrade-rbac.yaml`](../../deploy/helm/valkey-operator/templates/pre-upgrade-rbac.yaml)
  grants `customresourcedefinitions: get,list,patch,update` under the comment "Update the CRD to
  the latest schema before migrating existing CRs". `migrate.go` makes two API calls, `List` of a
  `ValkeyList` and `Patch` of a `Valkey`, and no non-test Go file under `cmd/` or `internal/`
  imports `apiextensions` (grep, 2026-09-27). The comment describes a step the code does not take.
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

## Impact

A contributor adding a defaulted field (DEVELOPER.md, "Adding things", CRD field step 6) has no
rule, so what the hook holds drifts by habit: a line added for a new field duplicates its marker
and, per the ordering read above, may not act in the release that introduces it. Nothing breaks
today.

What is live today is the grant: every `helm upgrade` with the default values takes
`valkeys: get,list,patch,update` and `customresourcedefinitions: get,list,patch,update`
cluster-wide for the lifetime of the hook Job
([H-3](../security/privilege-footprint.md#h-3), [ADR 0013](../adr/0013-operator-is-cluster-wide-privileged.md)
D10), and the CRD half is used by no code. `security: hardening`, because the rule itself adds or
removes no attack path; option C would additionally remove that grant.

[ADR 0005](../adr/0005-upgrade-neutral-defaults-and-anti-affinity.md) D1 bears on the question:
a new feature defaults to off, and a default that builds the same pod spec as the field's
absence rolls nothing, so for a field that conforms to D1, writing its default into stored CRs
changes no behaviour.

## Options

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

C is marked because every line the hook holds duplicates a marker that every released CRD
carries and that the API server applies on create and on read (upstream-documented, to be
measured), because a line for a field the same release
introduces cannot act in that release if the ordering reads correctly, and because the hook makes
every upgrade take a cluster-wide write grant on `valkeys` and on every CRD in the cluster, the
CRD half of which its code never uses. If the measurement contradicts the read-time defaulting,
A is the fallback.

Independently of this decision, the CRD rule of the hook's ClusterRole is unused by the code
(Fact). Narrowing it is a separate decision under H-3 and is not taken here.

## Decision

None yet.

## Verification

- **A or B:** the chosen ADR carries the rule, step 6 of the CRD-field checklist in
  DEVELOPER.md cites that ADR instead of the caveat that no rule decides yet, and this ticket is
  archived.
- **C:** before the removal, the measurement described under option C is green on Kind; after it,
  `helm template` of the chart renders no hook object, `grep -rn 'preUpgradeHook\|manager migrate'`
  over `deploy/`, `README.md` and `docs/` finds only history, H-3 and ADR 0013 D10 are updated in
  the same change, and `make test-unit`, `make lint` and `make generate-all` leave a clean tree.
  Step 6 of the checklist is replaced by a citation of the ADR that records the retirement.

## History

- 2026-09-27 — filed from the documentation restructure. While verifying, found that the hook's
  CRD grant is used by no code, that every release tag already carried all six default markers
  before the hook existed, and that by Helm's documented order the hook runs against the
  previous release's CRD (read, not measured).
