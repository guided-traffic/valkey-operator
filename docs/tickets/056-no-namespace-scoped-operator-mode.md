---
id: T56
title: the operator has no namespace-scoped mode
state: filed
severity: medium      # the operator stays equivalent to cluster-admin, with every Secret of the cluster in its memory
security: hardening
threat: "would additionally cover a compromise of the operator's ServiceAccount token or process: today it may get, list, watch and delete every Secret in the cluster and holds every Secret it watches in its informer cache; a namespace-scoped mode would confine that to the namespaces it serves"
urgency: icebox       # rule 5 since 2026-09-27: the H-1 / ADR 0013 correction landed; the namespace-scoped mode reopens ADR 0013 D1
effort: L             # the mode; the H-1/ADR 0013 correction alone is XS
blocked-by: decision  # ADR 0013 D1, see Options; the H-1/ADR 0013 correction is not blocked
filed-from: T31, section "Further security measures — not in this change, each open" (archive/031)
opened: 2026-09-27
decided:              # not recorded - no decision yet
done:                 # not recorded - not done
---

Filed on 2026-09-27 from the ninth row of the table
["Further security measures — not in this change, each open"](archive/031-generated-pods-run-as-root.md#further-security-measures--not-in-this-change-each-open)
in the archived ticket 031, which is `done` and tracks none of its rows. The operator-facing
statement is gap [H-1](../security/privilege-footprint.md#h-1).

## Fact

**Verified** (read 2026-09-27):

- The chart's ClusterRole grants `secrets: delete, get, list, watch`
  ([`clusterrole.yaml`](../../deploy/helm/valkey-operator/templates/clusterrole.yaml)
  lines 64–72), bound cluster-wide.
- The manager restricts its cache neither by namespace nor per object: a grep for
  `DefaultNamespaces` and `ByObject` in [`cmd/main.go`](../../cmd/main.go) finds nothing, and
  the Secret informer runs cluster-wide with no filter
  ([ADR 0016](../adr/0016-authentication-and-tls-posture.md) D2).
- [ADR 0013](../adr/0013-operator-is-cluster-wide-privileged.md) D1 treats the operator as
  equivalent to a cluster-admin credential and states that a namespace does not confine it; its
  *Alternatives Considered* record "a namespaced Role per watched namespace, or a cache filtered
  by label with the ClusterRole narrowed to match", with the cost that the operator stops being
  install-and-forget for new namespaces. *(2026-09-27: the second half cannot be built, see the
  appendix.)* *(2026-09-27, later: the ADR now strikes the second half in place and records one
  option only, work list item 1, History.)*
- Since 2026-08-26 the TLS Secret is read on every pass of every TLS cluster, for the material
  fingerprint ([H-1](../security/privilege-footprint.md#h-1)), so a filtered cache has one more
  consumer to satisfy.

*Added 2026-09-27 (enrichment, at `4a7543e`):*

- **Every rule of the chart ClusterRole is on a namespaced resource**
  (`clusterrole.yaml` lines 8–188), with no `nonResourceURLs`. The resources are `valkeys`
  with status and finalizers, `configmaps`, `services`, `serviceaccounts`, `pods`, `secrets`,
  `deployments`, `statefulsets`, `events`, `networkpolicies`, `poddisruptionbudgets`,
  `certificates`, `servicemonitors`, `roles` (with `bind` and `escalate`, lines 150–163),
  `rolebindings` and `leases`. Every rule can therefore move into a Role.
- The controller watches `Valkey`, owns nine namespaced kinds, and watches every `Secret`
  ([`valkey_controller.go`](../../internal/controller/valkey_controller.go) lines 2986–2999).
  That Secret watch is the cluster-wide informer that holds every Secret in memory.
- ServiceMonitor CRD absence is detected by `meta.IsNoMatchError` (`valkey_controller.go`
  lines 665 and 714), which is RESTMapper discovery and needs no RBAC rule.
- The pre-upgrade hook's grant stays cluster-scoped in any mode. It is a ClusterRole with
  `valkeys: get, list, patch, update` and `customresourcedefinitions: get, list, patch, update`
  ([`pre-upgrade-rbac.yaml`](../../deploy/helm/valkey-operator/templates/pre-upgrade-rbac.yaml)
  lines 29–49), bounded in time, not scope ([ADR 0013](../adr/0013-operator-is-cluster-wide-privileged.md) D10).
- **The drift guard sees one file.** `rbac_drift_test.go` compares `config/rbac/role.yaml` with
  the rules block of `clusterrole.yaml` only
  ([`rbac_drift_test.go`](../../internal/controller/rbac_drift_test.go) lines 34–35), and fails
  on any `{{` inside that block (lines 112–114). A Role rendered per namespace needs a guard of
  its own.
- The operator's `pods: list` is cluster-wide (`clusterrole.yaml` lines 50–59). A pod spec
  names every Secret it mounts or references, so a token holder learns the name of every Secret
  a pod uses from pods alone. Secrets no pod references, such as Helm release Secrets, are not
  named this way.

**Not verified:**

- ~~Which cluster-scoped reads remain in a namespace-scoped mode (how the operator detects the
  ServiceMonitor and cert-manager CRDs was not read).~~ *(partly answered 2026-09-27, above:
  ServiceMonitor by discovery, every chart rule namespaced, the hook's CRD grant cluster-scoped.
  How an absent cert-manager CRD is handled is still not read: there is no `IsNoMatchError` at
  the Certificate sites.)*
- Whether a namespaced Role still needs `escalate` to create the sidecar Role — the question
  ADR 0013 D3 leaves open.

### Appendix 2026-09-27: H-1 and ADR 0013 offer an option RBAC cannot express

**Verified** (read 2026-09-27):

- [`privilege-footprint.md`](../security/privilege-footprint.md) lines 146–148 (gap H-1)
  ~~says~~ *(said until 2026-09-27; struck and corrected in place, History)*:
  "Options: a namespaced Role per watched namespace, or a cache filtered by label with the
  ClusterRole narrowed to match."
- [ADR 0013](../adr/0013-operator-is-cluster-wide-privileged.md) line 417, under *Alternatives
  Considered*, ~~says~~ *(said until 2026-09-27; struck and corrected in place, History)*: "Or a
  cache filtered by label with the ClusterRole narrowed to match."
- **No ClusterRole can be narrowed to a label.** `rbacv1.PolicyRule` carries `Verbs`,
  `APIGroups`, `Resources`, `ResourceNames` and `NonResourceURLs`, and no selector
  (`k8s.io/api` v0.37.1, `rbac/v1/types.go`, the version [`go.mod`](../../go.mod) pins). A
  label-filtered cache shrinks the Secrets held in memory and leaves what the token may read
  unchanged. Option C below already says so. Both sentences are false as option descriptions
  (rule 1), and the correction is the XS slice in the work list.

## Impact

Every install: whoever obtains the operator's token or runs code in its process reads every
Secret in the cluster. Multi-tenant clusters cannot confine the operator to its tenants'
namespaces.

*(added 2026-09-27)* H-1 and ADR 0013 ~~describe~~ *(described, until work list item 1 on
2026-09-27)* an option as able to narrow the grant when it cannot (appendix). An administrator who picks it gets a smaller cache and an unchanged token.

## Options

The H-1/ADR 0013 correction needs no decision (work list). **Decision 1 comes first.**
Decision 2 exists only if decision 1 is B.

### Decision 1: whether to reopen ADR 0013 D1

- **A — keep D1.** Cluster-wide, install-and-forget.
- **B — an opt-in namespace-scoped mode (recommended).** ~~(best)~~ A chart value lists
  namespaces; the manager cache is restricted to them (controller-runtime
  `cache.Options.DefaultNamespaces`); the namespaced rules become a Role and RoleBinding per
  listed namespace, and the ClusterRole keeps only what is cluster-scoped. Default off, so
  install-and-forget stays the default. It adds a second RBAC shape to the three places ADR 0014
  keeps in sync, and a namespace added later needs a chart upgrade. *(added 2026-09-27)*
  Feasibility is better than filed: every chart rule is namespaced, so the cluster-scoped
  remainder is empty for the operator. The hook keeps its cluster-scoped grant. The Role shape
  needs its own drift guard (Fact), and leader election needs a `leases` Role in the release
  namespace. No multi-tenant user is named.
- **C — a label-filtered Secret cache** (`cache.Options.ByObject`) with the ClusterRole
  unchanged. Fewer Secrets resident in memory; the token may still read every one of them,
  because RBAC cannot narrow by label.
- *(added 2026-09-27)* **D — cluster-wide `get` only, no Secret informer.** Drop `list` and
  `watch` on `secrets` and read through the uncached `APIReader`, which the reconciler already
  carries (`valkey_controller.go` line 92). This keeps install-and-forget. It narrows less than
  it looks: the cluster-wide `pods: list` names every Secret a pod mounts or references (Fact),
  so `get` still reaches those. Only Secrets that no pod references drop out of reach. It also
  drops the Secret watch that turns a certificate rotation into a pass (`secretConcernsValkey`,
  [ADR 0030](../adr/0030-rotating-certificates-rotate-the-instances-that-cannot-reload-them.md)).

B is marked because it is the only option that narrows what the token may do, and it leaves
the default untouched. *(added 2026-09-27)* D loses on the pod-spec names, and C on RBAC's
missing selector.

### Decision 2 (only under B): how a CR in an unlisted namespace is reported

- **2A — not at all, documented (recommended).** The restricted cache never sees the CR. It
  keeps an empty status, and the chart values and README say which namespaces are served.
- **2B — a cluster-wide `Valkey` watch that writes a status.** This keeps `valkeys` out of the
  namespace restriction (`ByObject`) and writes a condition into CRs outside the list. It needs
  a cluster-wide `valkeys/status` write, the kind of cluster-scoped grant the mode exists to
  remove.

2A is marked because the list is the administrator's statement of scope, and reporting outside
it costs exactly the cluster-scoped write B removes.

## Work list

**Not waiting on a decision (XS, can be done today):**

1. **H-1 and ADR 0013 correction (rule 1).** Strike "or a cache filtered by label with the
   ClusterRole narrowed to match" in
   [`privilege-footprint.md`](../security/privilege-footprint.md) lines 146–147, and "Or a
   cache filtered by label with the ClusterRole narrowed to match." in
   [ADR 0013](../adr/0013-operator-is-cluster-wide-privileged.md) line 417. State what holds:
   RBAC has no label selector, so such a cache shrinks memory, not the grant. Adjust "Both
   are" in ADR 0013 lines 417–418, which then refers to one option. Doing this does not close
   the ticket. *(added 2026-09-27, review: both sentences then name one option, so the H-1
   lead-in "Options:" becomes "Option:", and in ADR 0013 "the options of the open gap" becomes
   "the option of the open gap", not "one of the options". The alternative's heading, "A
   namespaced Role per watched namespace", already names only the surviving option.)*
   **Done 2026-09-27**, in the review's wording ("Option:", "the option"), plus a dated Status
   line in ADR 0013 (History).

**Waiting on decision 1 (B):**

1. `cmd/main.go`: a namespace-list flag that sets `cache.Options.DefaultNamespaces`.
2. The chart: the value, a Role and RoleBinding per namespace, the ClusterRole only while the
   list is empty, and `leases` in the release namespace.
3. A drift guard for the Role shape in `rbac_drift_test.go`.
4. An e2e leg in the mode. Matrix legs are not required by name, so branch protection does not
   change ([ADR 0017](../adr/0017-test-and-ci-policy.md) D47).

**Waiting on decision 2:** the README and values text, or the status path.

**Close (ADR 0034):** amend ADR 0013 D1 and ADR 0014 (a second RBAC shape). Then H-1,
[the operator ClusterRole table](../security/privilege-footprint.md#the-operator-clusterrole)
and the README values. `git grep -n 'T56\|056-no-namespace'` outside `docs/tickets/` (none
today), then move to `archive/`.

## Decision

None yet.

## Verification

- *(added 2026-09-27, XS slice)* `grep -n "narrowed to match" docs/security/privilege-footprint.md
  docs/adr/0013-operator-is-cluster-wide-privileged.md` finds the phrase only struck through.
  *(Run 2026-09-27 after the fix: `privilege-footprint.md:147` and ADR 0013 `:424` inside struck
  text, and ADR 0013 `:426` inside the correction that says no ClusterRole can be narrowed to
  match. Done.)*
- In the mode: `kubectl auth can-i get secrets -n <unlisted namespace>` as the operator's
  ServiceAccount answers `no`, and the e2e suite is green in a listed namespace.
- A Valkey resource in an unlisted namespace is not reconciled; how that is reported is part
  of the decision.

## History

- 2026-09-27: urgency `now` -> `icebox` (rule 5): item 1, the only rule-1 statement, landed; the mode itself reopens ADR 0013 D1. Applied as the History entry below derived it.
- 2026-09-27 — work list item 1 landed, file by file (read in `git diff` of the working tree):
  - [`privilege-footprint.md`](../security/privilege-footprint.md) H-1 (`:146-150` now):
    "Options:" became "Option:", and ", or a cache filtered by label with the ClusterRole
    narrowed to match" is struck and corrected in place - an RBAC rule carries no label selector,
    `rbacv1.PolicyRule` has verbs, API groups, resources, resource names and non-resource URLs
    only, so a label-filtered cache shrinks the Secrets held in memory, not what the token may
    read.
  - [ADR 0013](../adr/0013-operator-is-cluster-wide-privileged.md), *Alternatives Considered*,
    "A namespaced Role per watched namespace" (`:424-430` now): the second option struck and
    corrected in place, "the options of the open gap" became "the option of the open gap"; a
    dated "Corrected 2026-09-27 (no decision changes)" Status line, which names `k8s.io/api`
    v0.37.1 as the version read.

  Decisions 1 and 2 are untouched. **Urgency not recomputed in this pass** (the orchestrating
  run left every urgency but one to the owner): by the frontmatter's own derivation it falls
  from `now` to `icebox` (rule 5, the mode reopens ADR 0013 D1), because item 1 was the only
  rule-1 statement. **Not verified:** nothing was run.
- 2026-09-27 — review - spot-checked the enrichment's file:line facts (all held) and
  made the XS slice's wording exact: one option survives the correction, so H-1 and ADR 0013 say
  "option", singular. Recommendations, urgency and frontmatter unchanged.
- 2026-09-27 — enriched - appended the H-1/ADR 0013 "ClusterRole narrowed to match" false
  statement (XS slice, rule 1). Added that every chart rule is namespaced, the watch list,
  ServiceMonitor discovery, the hook's cluster-scoped grant, the one-file drift guard, and the
  `pods: list` Secret-name path. Added option D (get-only) and decision 2 (unlisted namespace);
  B stays recommended.
- 2026-09-27 — urgency `icebox` → `now`: rule 1 now matches first, because H-1 and ADR 0013
  line 417 describe an RBAC narrowing that `rbacv1.PolicyRule` cannot express (the appendix).
  The mode itself would still be `icebox` (rule 5).
- 2026-09-27 — filed from the row "Namespace-scoped operator mode" of archive/031, which names
  ADR 0013. Gap [H-1](../security/privilege-footprint.md#h-1) states the scope it needs and the
  options' cost.
