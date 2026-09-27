---
id: T82
title: the operator is granted roles escalate and bind, which no Role or RoleBinding it writes needs
state: analysed       # both options costed, one recommended
severity: low         # nothing breaks; the operator token keeps one of its cluster-admin-equivalent channels
security: hardening   # the CR API reaches neither verb, and the token is cluster-admin equivalent through channels ADR 0013 D1 accepts; published as gap H-2
threat: "would additionally cover a compromise of the operator's ServiceAccount token, image or process: roles escalate lets it write a Role with any namespaced permission in any namespace, and roles bind lets it bind any existing Role to any subject; without them it could only hand out rules it already holds."
urgency: later        # no release gate, severity low, cheap known fix
effort: S             # two verbs out of one marker and one chart rule, one integration test, two unit assertions, ADR 0013 and the privilege-footprint page
blocked-by: decision  # dropping the verbs re-decides ADR 0013 D3; the documentation correction needs no decision
filed-from: T56 and gap H-2 of docs/security/privilege-footprint.md
opened: 2026-09-27
decided:
done:
---

# T82 - the operator is granted roles escalate and bind, which no Role or RoleBinding it writes needs

## Current state

**The API server check.** A Role create or update is admitted when the writer is in
`system:masters`, or may `escalate` on `roles`, or already holds every rule of the Role at its
scope. A RoleBinding create or update is admitted when the writer is in `system:masters`, or may
`bind` the referenced Role, or already holds every rule of that Role. A writer rule without
`resourceNames` covers a rule that lists them. So `escalate` and `bind` matter only for a Role
whose rules the writer does not hold itself. This holds in the Kubernetes v1.36.4 and v1.29.0
source (envtest's version and the README floor).

**What the operator writes and holds.** It writes exactly one Role and one RoleBinding per Valkey
resource, and both lie inside its own grant:

| What the operator writes | Its rules | What the operator holds |
|---|---|---|
| Role `<cr>-sidecar` ([`rbac.go:49-79`](../../internal/builder/rbac.go#L49-L79)) | `pods: get, patch` with `resourceNames` = this cluster's data pods ([`rbac.go:67-77`](../../internal/builder/rbac.go#L67-L77)), or no rule when the name list is empty | `pods: delete, get, list, patch, watch`, no `resourceNames` ([`clusterrole.yaml:50-59`](../../deploy/helm/valkey-operator/templates/clusterrole.yaml#L50-L59); marker [`valkey_controller.go:209`](../../internal/controller/valkey_controller.go#L209)) |
| RoleBinding `<cr>-sidecar` ([`rbac.go:140-160`](../../internal/builder/rbac.go#L140-L160)) | `roleRef` = that Role, subject = the `<cr>-sidecar` ServiceAccount | the same rule, so every rule of the referenced Role |

The Role is written before the RoleBinding in the same step, and a foreign Role stops the step
before the binding ([`valkey_controller.go:983-1004`](../../internal/controller/valkey_controller.go#L983-L1004)).
No other RBAC object is written. Every sidecar Role the repository ever built lies inside the
operator's `pods` rule, so an older operator image is covered too.

**The grant.** The ClusterRole grants `bind` and `escalate` on `roles` cluster-wide:
[`clusterrole.yaml:150-163`](../../deploy/helm/valkey-operator/templates/clusterrole.yaml#L150-L163)
(`bind` at `:155`, `escalate` at `:158`), generated from the marker
[`valkey_controller.go:217`](../../internal/controller/valkey_controller.go#L217) into
[`role.yaml:121-134`](../../config/rbac/role.yaml#L121-L134). Both verbs exempt the operator from
a check it passes anyway. `resourceNames` cannot narrow either: `escalate` is authorized with the
empty name of a create request, and `bind` with the referenced Role's name, which is
`<cr>-sidecar` per resource. The verbs can only be kept or dropped.

**The coverage check counts only rules held through RBAC.** The chart binds its ClusterRole to
the operator ServiceAccount unconditionally
([`clusterrolebinding.yaml`](../../deploy/helm/valkey-operator/templates/clusterrolebinding.yaml)),
so every chart install holds the rules through RBAC.

**The records.**
[privilege-footprint.md](../security/privilege-footprint.md) lines 47-49 state that without the
verbs the API server refuses the sidecar Role. That is false by the check above, and it names only
`escalate` while `bind` is equally unneeded. Gap H-2 (lines 156-160) asks to test and drop
`escalate` only. [ADR 0013](../adr/0013-operator-is-cluster-wide-privileged.md) D3 retains both
verbs "until the narrower configuration is actually tested"; D2, *Alternatives Considered* and
*Residual risks* are hedged correctly.

**Test coverage.** The integration manager runs as a `system:masters` user
([`suite_test.go:63-67`](../../test/integration/suite_test.go#L63-L67)), so every RBAC write is
admitted there regardless of the ClusterRole; envtest runs with RBAC authorization and offers
`Environment.AddUser` for a non-masters identity. The e2e tier installs with the chart, so every
e2e cluster writes the sidecar Role and RoleBinding under the chart ClusterRole. The drift guard
`TestHelmClusterRoleCoversGeneratedRole`
([`rbac_drift_test.go:160`](../../internal/controller/rbac_drift_test.go#L160)) asserts only
generated ⊆ chart, so a chart that keeps `escalate` while the marker drops it stays green.

**Impact.**

- *Holder of the operator's token, image or process:* with `escalate` it writes a Role with any
  namespaced rule (pods/exec, secrets create and update, roles and rolebindings) in any namespace,
  kube-system included; with `bind` it binds that Role, or any existing Role, to any subject,
  including a ServiceAccount it just created. Without the verbs the same chain only delegates the
  operator's own rules. The token stays cluster-admin equivalent through the channels ADR 0013 D1
  accepts (every Secret readable, workload create under any ServiceAccount, pod image patch).
- *Principal who may create `Valkey` resources:* no path to the verbs; the Role's rules are fixed
  in the builder and its names and the binding are derived from the CR and proven pods.
- *Maintainer changing the sidecar Role:* today `escalate` silently admits a sidecar rule the
  operator does not hold. Without it the write fails with 403 and the resource reports
  `ReconcileBlocked`, phase `Error`, until the ClusterRole gains the rule, the order
  [ADR 0014](../adr/0014-rbac-lives-in-three-places.md) already demands.
- *Installer auditing the chart:* the Kubernetes RBAC good-practices page lists both verbs as
  privilege-escalation risks.

Out of scope: `roles` and `rolebindings` `delete`, `update`, `patch` cluster-wide, and whether
`roles: delete` is needed at all. Narrowing the footprint to namespaces is T56.

## Required changes

### Independent of the open questions

1. Correct [privilege-footprint.md](../security/privilege-footprint.md) lines 47-55: the API
   server does not refuse the sidecar Role without the verbs, and `bind` is as unneeded as
   `escalate`.
2. In ticket 056, point the *Not verified* item on `escalate` and the H-2 note to this ticket.

### Depends on the answers

If Q1 is A (drop both verbs):

1. Remove `escalate;bind` from the marker at
   [`valkey_controller.go:217`](../../internal/controller/valkey_controller.go#L217), run
   `make manifests` for [`role.yaml`](../../config/rbac/role.yaml), and remove `bind` (`:155`) and
   `escalate` (`:158`) from
   [`clusterrole.yaml`](../../deploy/helm/valkey-operator/templates/clusterrole.yaml#L150-L163);
   extend the comment at `:149` to say why no escalation verb is needed. No chart value keeps the
   verbs (security posture, fixed fleet-wide). No migration step: RBAC re-checks only writes.
2. Integration test in `test/integration/`: with `testEnv.AddUser` a non-masters user bound to a
   ClusterRole parsed from the chart's rules block (needs its own copy of `parsePolicyRules` or a
   shared helper; the existing one sits in a `_test.go` file of `internal/controller`). As that
   user, create `BuildSidecarRole` (three names) and `BuildSidecarRoleBinding`, update the Role to
   five names, update the binding's labels: all succeed. Negative controls: a Role with
   `pods/exec: create` and a RoleBinding to an admin-written Role with `secrets: create` are both
   refused with `IsForbidden`. Mutations: adding `escalate` turns the first negative control red,
   adding `bind` the second; adding `create` to the builder's verbs turns the positive half red.
3. Unit assertions in [`rbac_drift_test.go`](../../internal/controller/rbac_drift_test.go): neither
   `role.yaml` nor the chart grants `escalate` or `bind` (mutation: re-add `escalate` to the chart
   only, must go red); every (group, resource, verb) of `BuildSidecarRole` is granted by a chart
   rule without `resourceNames` (mutation: add `create` to the builder, must go red).
4. [ADR 0013](../adr/0013-operator-is-cluster-wide-privileged.md): Status amendment; D2 and D3
   re-decided, superseded text marked in place (coordinate with T70, which also edits D3);
   *Alternatives Considered* entry "Drop `escalate` and `bind`" becomes the chosen path;
   *Residual risks* bullet closed; the D13 note naming H-1 and H-2 and the *Consequences* bullet
   ending "reach namespaced admin everywhere" adjusted; the index row in
   [`docs/adr/README.md`](../adr/README.md).
5. [privilege-footprint.md](../security/privilege-footprint.md): the `roles` row loses both verbs
   and the "privilege ceiling" consequence, the `rolebindings` row and the summary state delegation
   of the operator's own rules, and H-2 is removed as closed.
6. Ticket 056: its channel list states delegation of the operator's own rules instead of
   `escalate`/`bind`.
7. `make generate-all` leaves no diff; `make test-unit`, `make test-integration`, `make lint` and
   the full e2e suite on both single-node legs green.

If Q1 is B (keep both verbs): re-decide ADR 0013 D3 to "kept by choice" and rewrite H-2 to state
that the verbs are not needed and are kept anyway.

## Open questions

### Q1: Does the operator keep or drop `roles: escalate, bind` in its ClusterRole?

The operator holds every rule of the one Role it writes, so both verbs only exempt it from a check
it passes anyway, and `resourceNames` cannot scope them. ADR 0013 D3 already keeps them only
"until the narrower configuration is actually tested".

- **A - drop both, prove the subset in the integration and unit tiers (recommended).** Effort S;
  removes the Role-escalation channel of a token holder; a future sidecar rule needs the
  ClusterRole rule first, which the unit assertion catches before a release.
- **B - keep both, correct the records only.** Effort XS, no code; keeps a cluster-wide escalation
  grant with no function, and D3 keeps a grant it can no longer justify.

A is recommended: the verbs buy nothing today, the integration test is exactly the test D3 waits
for, and the "strict subset" claim becomes an invariant a test enforces. Dropping only one verb is
not sensible, since each alone reopens a channel at the same cost.

**Answer:** _open_

## Not verified

- No API server was asked; admission without the verbs rests on reading the v1.36.4 and v1.29.0
  source. The integration test measures it on 1.29, the e2e suite on the Kind version of CI.

## Related

- T56 - namespace-scoped operator mode; the answer here is the same in both shapes.
- T70 - also corrects ADR 0013 D3 (its sidecar verb list).
