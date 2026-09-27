---
id: T82
title: the operator is granted roles escalate and bind, which no Role or RoleBinding it writes needs
state: analysed       # every load-bearing fact re-read at 84a39c2 and in the Kubernetes v1.36.4 and v1.29.0 source, both options costed and one marked (History 2026-09-27)
severity: low         # if never fixed: nothing breaks; the operator token keeps one of its four cluster-admin-equivalent channels (T56), and the three others stay whatever this ticket decides (Impact)
security: hardening   # derived, not in doubt: no principal reaches the verbs through the CR API (Impact, case 2), and a holder of the operator token is already cluster-admin equivalent through channels ADR 0013 D1 accepts (case 1); the gap is published as H-2 in docs/security/privilege-footprint.md, so no embargo applies
threat: "would additionally cover a compromise of the operator's ServiceAccount token, image or process: today roles escalate lets it write a Role with any namespaced permission (pods/exec, secrets create and update, roles and rolebindings with any verb) in any namespace, kube-system included, and roles bind lets it bind any existing Role to any subject; without them it could only hand out rules it already holds. The other channels ADR 0013 D1 accepts (every Secret readable, workload create under any ServiceAccount, pod image patch) stay, so the token remains cluster-admin equivalent after the fix"
urgency: later        # strict reading of rule 1 ("measured-false"), applied: privilege-footprint.md:47-49 states that the API server refuses the sidecar Role without the verbs, which is false by reading the Kubernetes source against the chart but not measured, so rule 1 does not match; no release gate (rule 2); severity low (rule 3); option A is a cheap known fix, effort S (rule 4). Under the other reading, false by code reading counts, rule 1 matches that sentence and the urgency is now, until work list item 1 lands
effort: S             # option A: two verbs out of one marker and one chart rule, role.yaml regenerated, one integration test with an impersonated user, two unit assertions, ADR 0013 and the privilege-footprint page in the same change
blocked-by: decision  # option A re-decides ADR 0013 D3 (see Options); work list item 1 needs no decision
filed-from: T56 (ticket 056, the Not verified item on escalate and the Related tickets note that gap H-2 has no ticket) and gap H-2 of docs/security/privilege-footprint.md, during the re-verification of 2026-09-27 at 84a39c2
opened: 2026-09-27
decided:              # not recorded - no decision yet
done:                 # not recorded - not done
---

# T82 - the operator is granted roles escalate and bind, which no Role or RoleBinding it writes needs

Filed on 2026-09-27 from ticket 056 (no namespace-scoped operator mode), whose re-verification at
`84a39c2` answered by reading the question that gap
[H-2](../security/privilege-footprint.md#h-2) and
[ADR 0013](../adr/0013-operator-is-cluster-wide-privileged.md) D3 leave open, recorded the answer
under its *Not verified* list, and noted that no ticket carried H-2. Everything about the finding
belongs here. The mechanism and the answer are H-2's alone: they are the same for today's
cluster-wide binding and for 056's namespace mode, so 056 is to keep only a pointer (work list
item 6); until that item lands, 056 still carries its own copy of the answer.

## Fact

**Mechanism.** The API server guards every write of a Role and of a RoleBinding with an
escalation check. A Role create or update is admitted when the writer is in `system:masters`,
**or** is authorized for the verb `escalate` on `roles`, **or** already holds every rule the Role
contains, at the Role's scope. A RoleBinding create or update is admitted when the writer is in
`system:masters`, **or** is authorized for `bind` on the referenced Role, **or** already holds
every rule of the referenced Role. The first two branches short-circuit the third; the third is
a plain coverage test of the writer's own RBAC rules against the new ones, and a writer rule
without `resourceNames` covers a rule that lists them. So `escalate` and `bind` matter only for a
Role whose rules the writer does not hold itself.

The operator writes exactly one Role and one RoleBinding per Valkey resource, and their rules are
a strict subset of what the operator's ClusterRole grants unrestricted and cluster-wide:

| What the operator writes | Its rules | What the operator holds |
|---|---|---|
| Role `<cr>-sidecar` ([`rbac.go:49-79`](../../internal/builder/rbac.go#L49-L79)) | `pods: get, patch` with `resourceNames` = this cluster's data pods ([`rbac.go:67-77`](../../internal/builder/rbac.go#L67-L77)), or no rule at all when the name list is empty ([`rbac.go:59-65`](../../internal/builder/rbac.go#L59-L65)) | `pods: delete, get, list, patch, watch`, no `resourceNames` ([`clusterrole.yaml:50-59`](../../deploy/helm/valkey-operator/templates/clusterrole.yaml#L50-L59); marker [`valkey_controller.go:209`](../../internal/controller/valkey_controller.go#L209)) |
| RoleBinding `<cr>-sidecar` ([`rbac.go:140-160`](../../internal/builder/rbac.go#L140-L160)) | `roleRef` = that Role, subject = the `<cr>-sidecar` ServiceAccount | the same rule, so it holds every rule of the referenced Role |

Nothing else in the operator writes an RBAC object: `grep -rn 'rbacv1\.\(Role\|RoleBinding\|ClusterRole\)' internal cmd`
outside `_test.go` finds only the two builders, the two `current :=` reads in the reconciler
([`valkey_controller.go:1116`](../../internal/controller/valkey_controller.go#L1116),
[`:1158`](../../internal/controller/valkey_controller.go#L1158)) and the two `Owns` watches
([`valkey_controller.go:2993-2994`](../../internal/controller/valkey_controller.go#L2993-L2994)).
The write sites are the Role create and update
([`valkey_controller.go:1120`](../../internal/controller/valkey_controller.go#L1120),
[`:1143`](../../internal/controller/valkey_controller.go#L1143)) and the RoleBinding create,
recreate and update
([`valkey_controller.go:1162`](../../internal/controller/valkey_controller.go#L1162),
[`:1204`](../../internal/controller/valkey_controller.go#L1204),
[`:1218`](../../internal/controller/valkey_controller.go#L1218)). The Role is written before the
RoleBinding in the same step, and a Role the operator does not control stops the step before the
binding ([`valkey_controller.go:983-1004`](../../internal/controller/valkey_controller.go#L983-L1004),
Role at `:992`, binding at `:1003`). Without `bind`, the RoleBinding write therefore always finds
the referenced Role present and covered.

Yet the ClusterRole grants both verbs on `roles`, cluster-wide:
[`clusterrole.yaml:150-163`](../../deploy/helm/valkey-operator/templates/clusterrole.yaml#L150-L163)
(`bind` at `:155`, `escalate` at `:158`), generated from the marker
[`valkey_controller.go:217`](../../internal/controller/valkey_controller.go#L217) into
[`role.yaml:121-134`](../../config/rbac/role.yaml#L121-L134). Because the escalate branch
short-circuits, the API server does consult the verb on every sidecar Role write today; the
coverage test it skips would pass.

**What the tracked records say.**

- [privilege-footprint.md](../security/privilege-footprint.md) lines 47-49: "`escalate` and
  `bind` are not gratuitous: without them the API server refuses to let the operator create the
  `<cr-name>-sidecar` Role, since a principal may not grant permissions it does not itself hold"
  - stated as current fact, then qualified in the same paragraph ("but it does hold ... so the
  narrower alternative (dropping `escalate`) is worth testing", lines 49-53) and marked
  **Not verified** (lines 54-55). By the reading above the first sentence is false: the API server
  admits the Role without either verb. It also names only `escalate`; `bind` is equally unneeded.
- H-2 itself ([privilege-footprint.md](../security/privilege-footprint.md) lines 156-160):
  "Test whether the sidecar Role can be created without `escalate` ... and if so, drop the verb."
  Singular; the ADR asks for both.
- [ADR 0013](../adr/0013-operator-is-cluster-wide-privileged.md) D2 (lines 151-161) names the
  chain "create SA -> write Role (with `escalate`) -> bind -> use" as namespaced admin everywhere;
  D3 (lines 163-171) retains both verbs "until the narrower configuration is actually tested",
  calls the refusal "an observation from when the grant was added, not reproduced", and says it
  "may no longer apply"; *Alternatives Considered* (lines 432-435) lists dropping both as
  "explicitly untested"; *Residual risks* (lines 505-509) says reducing it "requires verifying the
  subset claim and dropping both `escalate` and `bind`". These are hedged correctly and are not
  false. D3's verb list for the sidecar Role (`get,list,patch`) is wrong, but that correction is
  item (d) of [T70](070-tracked-comments-and-adrs-state-what-the-code-contradicts.md), not this
  ticket's.
- **History of the grant.** Both verbs arrived in `ce97f1b` (2026-02-28, "feat: reliable valkey
  service (#9)", a squash of eleven commits). The same commit added `patch` to the operator's own
  `pods` rule, and its sidecar Role granted `pods: get, list, patch` without `resourceNames`,
  which the operator's `pods: delete, get, list, patch, watch` covered. Every later sidecar Role
  stayed inside that rule, which has not changed since `ce97f1b` (`git log -G'resources=pods,'` on
  the controller): `patch` alone from `44a974a` (2026-08-21), `resourceNames` from `fa50b89`
  (2026-08-21), `get, patch` from `e32f0d2` (2026-08-27) (`git log -G'Verbs:' --
  internal/builder/rbac.go`, re-read in the review of 2026-09-27). No committed state of the
  repository ever had a sidecar Role outside the operator's grant; the refusal ADR 0013 D3 records
  may have been seen on an intermediate commit of that squash (one is titled "fix: operator
  permission") before `pods: patch` was added. That is an inference, not verified.

**Why `resourceNames` cannot narrow the two verbs.** `escalate` is authorized with the request's
object name ([`escalation_check.go:85`](https://github.com/kubernetes/kubernetes/blob/v1.36.4/pkg/registry/rbac/escalation_check.go#L85),
`Name: requestInfo.Name`), and a create request carries none; the RBAC page states "You cannot
restrict **deletecollection** or top-level **create** requests by resource name". `bind` is
authorized with the referenced Role's name
([`escalation_check.go:128-133`](https://github.com/kubernetes/kubernetes/blob/v1.36.4/pkg/registry/rbac/escalation_check.go#L128-L133)),
which would work for a fixed name, but the operator's Role names are `<cr>-sidecar`, one per
Valkey resource, and `resourceNames` match exact names only. Neither verb can be narrowed to
"the sidecar Roles"; they can only be kept or dropped.

**Verified** (2026-09-27, by reading at `84a39c2`):

- The builder, the reconciler write sites and order, the marker, the chart rule and the
  generated role, at the lines above.
- The Kubernetes escalation logic, read in the v1.36.4 source from the Go module cache
  (`k8s.io/kubernetes@v1.36.4`, `k8s.io/component-helpers@v0.37.1`):
  Role create and update short-circuit on `EscalationAllowed || RoleEscalationAuthorized`
  ([`role/policybased/storage.go:67-77`](https://github.com/kubernetes/kubernetes/blob/v1.36.4/pkg/registry/rbac/role/policybased/storage.go#L67-L77),
  update at `:80-101`) and otherwise run `ConfirmNoEscalationInternal`; RoleBinding create and
  update short-circuit on `EscalationAllowed` and then `BindingAuthorized`, and otherwise fetch the
  referenced Role's rules with `GetRoleReferenceRules` and run `ConfirmNoEscalation`
  ([`rolebinding/policybased/storage.go:70-100`](https://github.com/kubernetes/kubernetes/blob/v1.36.4/pkg/registry/rbac/rolebinding/policybased/storage.go#L70-L100),
  update at `:102-144`); `EscalationAllowed` is membership in `system:masters`
  ([`escalation_check.go:32-48`](https://github.com/kubernetes/kubernetes/blob/v1.36.4/pkg/registry/rbac/escalation_check.go#L32-L48));
  `ConfirmNoEscalation` compares the writer's resolved rules with `Covers`
  ([`validation/rule.go:53-69`](https://github.com/kubernetes/kubernetes/blob/v1.36.4/pkg/registry/rbac/validation/rule.go#L53-L69)),
  and `ruleCovers` treats an owner rule without `resourceNames` as covering any name list
  ([`policy_comparator.go:164-170`](https://github.com/kubernetes/component-helpers/blob/v0.37.1/auth/rbac/validation/policy_comparator.go#L164-L170)).
  The rule resolver is built on the RBAC REST storage registries, not an informer
  ([`rest/storage_rbac.go:101-106`](https://github.com/kubernetes/kubernetes/blob/v1.36.4/pkg/registry/rbac/rest/storage_rbac.go#L101-L106)).
  It resolves RBAC bindings only, so the coverage test counts only rules the operator holds
  through RBAC; the chart binds its ClusterRole to the operator ServiceAccount unconditionally
  (no `if` in [`clusterrole.yaml`](../../deploy/helm/valkey-operator/templates/clusterrole.yaml) or
  [`clusterrolebinding.yaml`](../../deploy/helm/valkey-operator/templates/clusterrolebinding.yaml);
  `serviceAccount.create: false` only changes which ServiceAccount the binding names).
  `BindingAuthorized` asks for `bind` on `roles` for a `roleRef` of kind Role and on
  `clusterroles` for kind ClusterRole
  ([`escalation_check.go:123-133`](https://github.com/kubernetes/kubernetes/blob/v1.36.4/pkg/registry/rbac/escalation_check.go#L123-L133)).
- The same logic in Kubernetes v1.29.0, envtest's version and the README floor, read in the
  v1.29.0 tag on GitHub on 2026-09-27 (review of this ticket): the Role storage short-circuits on
  `EscalationAllowed || RoleEscalationAuthorized` in Create and Update and otherwise calls
  `ConfirmNoEscalationInternal`; the RoleBinding storage checks `EscalationAllowed`, then
  `BindingAuthorized`, then `GetRoleReferenceRules` and `ConfirmNoEscalation`
  (`pkg/registry/rbac/role/policybased/storage.go`,
  `pkg/registry/rbac/rolebinding/policybased/storage.go`); `ruleCovers` in
  `k8s.io/component-helpers` v0.29.0 `auth/rbac/validation/policy_comparator.go` carries the same
  `resourceNames` branch as v0.37.1.
- The documented rule, the Kubernetes RBAC page, section "Privilege escalation prevention and
  bootstrapping" (https://kubernetes.io/docs/reference/access-authn-authz/rbac/, fetched from the
  kubernetes/website source 2026-09-27): a role may be created or updated if "You already have
  all the permissions contained in the role, at the same scope as the object being modified" or
  the writer may `escalate`; a binding if the writer holds "all the permissions contained in the
  referenced role (at the same scope as the role binding) *or*" may `bind` the referenced role.
- **No tier exercises the operator's real grant except e2e.** The integration suite runs its
  manager on the config `envtest.Environment.Start` returns
  ([`suite_test.go:63-67`](../../test/integration/suite_test.go#L63-L67),
  [`:88`](../../test/integration/suite_test.go#L88)), whose user is in `system:masters`
  (controller-runtime v0.25.1 `pkg/envtest/server.go:315`), so `EscalationAllowed` admits every
  RBAC write there regardless of the ClusterRole. envtest does start the API server with
  `--authorization-mode=RBAC` (controller-runtime v0.25.1
  `pkg/internal/testing/controlplane/apiserver.go:339`) and offers `Environment.AddUser`
  (`pkg/envtest/server.go:378`), so a non-masters identity can be tested there. The e2e tier
  installs the operator with the chart
  ([`Makefile:218-223`](../../Makefile#L218-L223), CI
  [`release.yml:353`](../../.github/workflows/release.yml#L353), values in
  [`test/e2e/helm-values.yaml`](../../test/e2e/helm-values.yaml) touch no RBAC), so every e2e
  cluster writes its sidecar Role and RoleBinding under the chart ClusterRole.
- The drift guard `TestHelmClusterRoleCoversGeneratedRole`
  ([`rbac_drift_test.go:160`](../../internal/controller/rbac_drift_test.go#L160)) asserts
  generated ⊆ chart only, so a chart that keeps `escalate` while the marker drops it stays green
  (legal drift, [ADR 0014](../adr/0014-rbac-lives-in-three-places.md)).
- No docker measurement was taken: no claim here concerns Valkey runtime behaviour.

**Not verified:**

- No API server was asked. That the sidecar Role and RoleBinding are admitted without `escalate`
  and `bind` rests on reading the v1.36.4 and v1.29.0 source (envtest's version,
  [`Makefile:5`](../../Makefile#L5), and the README floor); the versions between them were not
  read. The integration test of option A measures it on 1.29, the e2e suite on the Kind version
  of the legs.
- Whether `GetRoleReferenceRules` can be served from the API server's watch cache and so miss a
  Role created a moment earlier. It does not decide anything: a miss fails the step and the pass
  is retried.
- Why the verbs were added (the squash history above is an inference).
- Which policy engines or scanners in the fleet flag `escalate`/`bind` (case 4 below).

## Impact

Nothing breaks either way. The grant matters only to a holder of the operator's identity.

1. **A holder of the operator's ServiceAccount token, image or process** (the principal
   ADR 0013 D1 names). Live today, not dormant: with `escalate` it writes a Role carrying any
   namespaced rule in any namespace, and with `bind` (on `roles`) it binds that Role, or any Role
   somebody else wrote, to any subject, its own ServiceAccount or one it just created
   (`serviceaccounts: create`,
   [`clusterrole.yaml:36-49`](../../deploy/helm/valkey-operator/templates/clusterrole.yaml#L36-L49)).
   `bind` on `roles` does not reach a RoleBinding to a ClusterRole such as `admin` (that check
   asks for `bind` on `clusterroles`, Fact); `escalate` makes the difference irrelevant, since the
   token writes an equivalent Role instead.
   That is ADR 0013 D2's "create SA -> write Role -> bind -> use". Without the two verbs the same
   chain yields only the operator's own rules, handed to another subject: delegation, no new
   permission. What stays, and is why the class is hardening: the token reads every Secret, can
   create a Deployment or StatefulSet under any ServiceAccount of any namespace, and can patch
   the image of any running pod (T56, Fact, "four channels"), so it keeps reaching the rights of
   every ServiceAccount in the cluster. The fix removes one channel, not the cluster-admin
   equivalence.
2. **A principal who may create `Valkey` resources.** No path to the verbs: the Role's rules are
   fixed in the builder (`pods: get, patch`), its names come from the CR name and from pods the
   StatefulSet proven ours created ([`rbac.go:97-121`](../../internal/builder/rbac.go#L97-L121),
   filtered per ADR 0020), and the RoleBinding's `roleRef` and subject are derived from the CR
   name. The CR API cannot make the operator write an escalated Role. Unaffected either way.
3. **A maintainer changing the sidecar Role.** Today `escalate` hides a mistake: a sidecar rule
   the operator does not hold (for example `pods: create`) is admitted silently and widens the
   sidecar beyond the operator. Without the verbs the write fails with 403, the step fails, and
   the Valkey resource reports `ReconcileBlocked` and phase `Error` until the ClusterRole gains the
   rule - which is the order [ADR 0014](../adr/0014-rbac-lives-in-three-places.md) already demands
   for any new rule. Option A adds a unit assertion so this fails in `make test-unit` rather than
   on a cluster.
4. **An installer auditing the chart.** `escalate` and `bind` are what RBAC review guidance and
   admission policies look for first; the Kubernetes RBAC good-practices page lists both under
   "Kubernetes RBAC - privilege escalation risks" ("The exception to this is the `escalate`
   verb"; `bind` "allows for the bypass of Kubernetes in-built protections against privilege
   escalation", https://kubernetes.io/docs/concepts/security/rbac-good-practices/, fetched
   2026-09-27). Which scanners the fleet runs was not checked.

Not covered by this ticket and unchanged by it: `roles` and `rolebindings` `delete`, `update` and
`patch` cluster-wide still let a token holder remove or rewrite (within its own rules) any Role or
RoleBinding of any namespace, a tampering and denial-of-service channel, and whether the operator
needs `roles: delete` at all (no call site was found by `grep`; the garbage collector removes the
Role with the CR) was not analysed. Narrowing the whole footprint to namespaces is T56.

## Options

**The decision:** keep or drop `roles: escalate, bind` in the operator's ClusterRole.

The mechanism, once more in prose: the API server lets a writer create a Role only if the writer
already holds every rule in it, unless the writer may `escalate`; and create a RoleBinding only if
the writer holds every rule of the referenced Role, unless the writer may `bind` it. The operator
holds every rule of the one Role it writes, so both verbs are an exemption from a check the
operator passes anyway. `resourceNames` cannot scope either verb to the sidecar Roles (Fact), so
there is no middle ground between keeping and dropping.

**A - drop both verbs, and prove the subset in the integration and unit tiers (recommended).**
Remove `escalate;bind` from the marker at
[`valkey_controller.go:217`](../../internal/controller/valkey_controller.go#L217), regenerate
[`role.yaml`](../../config/rbac/role.yaml) with `make manifests`, and remove `bind` and `escalate`
from [`clusterrole.yaml:155,158`](../../deploy/helm/valkey-operator/templates/clusterrole.yaml#L150-L163).
Add an integration test that impersonates a non-masters user bound to the chart's ClusterRole and
writes the builder's Role and RoleBinding (Verification), and two unit assertions: no rule of
either file grants `escalate` or `bind`, and every rule of `BuildSidecarRole` is covered by an
unrestricted rule of the chart ClusterRole. No chart value keeps the verbs: this narrows a
security posture, which is fixed fleet-wide with no opt-in (ADR 0005 D1 governs features).
Upgrade: Helm updates the ClusterRole in place; existing sidecar Roles and RoleBindings are not
re-checked (RBAC checks only writes), and the next write of each is covered. An older operator
image running under the narrowed ClusterRole, during the rollout or after an image-only rollback,
is covered as well: every sidecar Role the repository ever built lies inside the operator's
`pods` rule, unchanged since `ce97f1b` (Fact, history of the grant). A rollback to an older chart
restores the verbs. No migration step. Amends ADR 0013 D2, D3, *Alternatives* and
*Residual risks*, and closes H-2.
Cost: effort S. Consequence: case 3 above - a future sidecar rule needs the ClusterRole rule
first, or that Valkey resource is blocked until it has it; the unit assertion catches that before
a release.

**B - keep both verbs, correct the records only.** Rewrite privilege-footprint.md lines 47-55 and
H-2 to say the verbs are not needed by reading and are kept anyway, and amend ADR 0013 D3 to
retain them by choice rather than "until tested". Cost: XS, no code. Consequence: case 1 keeps
the Role-escalation channel; a hidden widening of the sidecar Role (case 3) stays silent; and the
stated reason for keeping them disappears the moment the records are corrected, so D3 would keep
a grant it cannot justify.

**Why A beats B:** the verbs buy nothing today - every write they exempt passes the check they
skip - so B keeps a cluster-wide privilege-escalation grant for no function, and its only saving
is an S-sized change. ADR 0013 D3 already names the condition for dropping them ("until the
narrower configuration is actually tested"), and A's integration test is that test, so A carries
out an existing decision rather than making a new one. A also turns the "strict subset" sentence
that three documents repeat from prose into an invariant a test enforces. The strongest argument
for B is that the coverage test counts only rules held through RBAC (Fact), so an installation
that grants the operator its `pods` rights through another authorizer (a webhook, a managed
cluster's IAM integration) instead of RBAC would be refused after A where it is admitted today.
It does not hold for this operator: the chart renders its ClusterRole and ClusterRoleBinding
without a condition, so every chart install holds the rules through RBAC, and an install without
the chart has taken over the whole footprint and sees the refusal as `ReconcileBlocked` (case 3
above). B would be the choice
only if the owner wants no RBAC change before T56 decides the namespace mode, and nothing in T56
depends on the verbs (Fact: the answer is the same in both shapes).

**Considered and not added:**

- *Drop only `escalate`, or only `bind`.* Each half reopens a channel on its own: `bind` alone
  still binds any existing Role in any namespace (a wide one written by someone else) to any
  subject, and `escalate` alone still rewrites any existing Role already bound to a subject the
  token can act as. The cost of dropping both is the same as dropping one.
- *Scope the verbs with `resourceNames`.* Not expressible (Fact): `escalate` is authorized with
  the empty name of a create, and the Role names are per resource.
- *A chart-shipped ClusterRole that the operator only binds per Valkey resource, with `bind`
  restricted to that one name.* The ClusterRole cannot carry per-cluster pod names, so every
  sidecar would get `get, patch` on every pod of its namespace, undoing ADR 0012 D8 step 3
  (named pods only); and it is not needed, since the operator already holds the rules.
- *A chart-shipped Role per namespace.* The chart does not know the namespaces at install time;
  that is T56's namespace mode.

## Decision

Not decided.

## Work list

1. *(decision-free, on either option)* Correct
   [privilege-footprint.md](../security/privilege-footprint.md) lines 47-55 in place (strike, date,
   correction): the API server does not refuse the sidecar Role without the verbs by the upstream
   rule read against the chart, and `bind` is as unneeded as `escalate`. Under the reading of rule
   1 that counts code reading, this item is the one that makes the urgency `now`.
2. *(A)* Remove `escalate;bind` from the marker at
   [`valkey_controller.go:217`](../../internal/controller/valkey_controller.go#L217), run
   `make manifests` so [`role.yaml:121-134`](../../config/rbac/role.yaml#L121-L134) follows, and
   remove `bind` (`:155`) and `escalate` (`:158`) from
   [`clusterrole.yaml`](../../deploy/helm/valkey-operator/templates/clusterrole.yaml#L150-L163);
   extend the comment at `:149` to say why no escalation verb is needed.
3. *(A)* Integration test in `test/integration/` (Verification, first bullet), and in
   [`rbac_drift_test.go`](../../internal/controller/rbac_drift_test.go) the two unit assertions.
4. *(A)* Records, in the same change: [ADR 0013](../adr/0013-operator-is-cluster-wide-privileged.md)
   Status amendment with the date; D2 and D3 re-decided with the superseded text marked in place
   (D3's paragraph is also the target of T70 item (d); whichever lands second rebases onto the
   other); the *Alternatives Considered* entry "Drop `escalate` and `bind`" becomes the chosen
   path; the *Residual risks* bullet closed; the D13 closing note (line 338, "the two gaps that
   define the trust model are H-1 and H-2") and the *Consequences* bullet that ends "reach
   namespaced admin everywhere" (line 357) re-read against the narrowed grant and marked in place;
   the ADR index row for 0013 ([`docs/adr/README.md:95`](../adr/README.md)) and, when D3 is
   superseded rather than amended, the list of superseded rules above the index
   ([`docs/adr/README.md:43-45`](../adr/README.md)). In
   [privilege-footprint.md](../security/privilege-footprint.md): the `roles` row (line 37) loses
   the two verbs and the "privilege ceiling" consequence, the `rolebindings` row (line 38) and the
   summary (lines 41-45) state delegation of the operator's own rules instead of namespaced admin,
   and H-2 (lines 154-160) is removed as a closed gap under ADR 0036. `git grep -n -w 'escalate'`
   over `docs/security/` and `docs/adr/` then finds only struck or dated text.
5. *(B instead of 2-4)* ADR 0013 D3 re-decided to "kept by choice", H-2 rewritten to state the
   reading and the choice.
6. Ticket 056: its *Not verified* item on `escalate` and its *Related tickets* note on H-2 point
   to this ticket; on close, if A landed, its `threat:` line drops "roles escalate/bind" from the
   channels, and its Fact's channel (4) (`roles: bind, create, escalate` and `rolebindings:
   create`, 056 line 131) and the sentence naming `escalate`/`bind` among the channels (056 line
   332) state delegation of the operator's own rules instead.

## Verification

- **Integration (A; the API server decides the outcome, so this tier, ADR 0017 D2).** With
  `testEnv.AddUser` a user outside `system:masters`, a ClusterRole created from the rules block of
  [`clusterrole.yaml`](../../deploy/helm/valkey-operator/templates/clusterrole.yaml) (parsed from
  the file, as the drift guard does; its parser, `parsePolicyRules`, sits in a `_test.go` file of
  `internal/controller` and cannot be imported from `test/integration`, so the test needs its own
  copy or a shared helper), and a ClusterRoleBinding to it. As that user: create
  `BuildSidecarRole` (three names) and `BuildSidecarRoleBinding` in a fresh namespace, update the
  Role to five names, update the RoleBinding's labels - all succeed. Negative controls as the same
  user: a Role with `pods/exec: create` is refused with `IsForbidden`; a RoleBinding to a Role
  with `secrets: create` that the admin client wrote beforehand is refused with `IsForbidden`.
  - Mutation check 1: add `escalate` to the ClusterRole the test creates - the first negative
    control must go red (the Role is admitted). Proves the test sees `escalate`.
  - Mutation check 2: add `bind` instead - the second negative control must go red.
  - Revert check: make `BuildSidecarRole` add `create` to its verbs - the positive half must go
    red with 403. Proves the test measures coverage, not a masters bypass.
- **Unit (A).** In [`rbac_drift_test.go`](../../internal/controller/rbac_drift_test.go): neither
  `role.yaml` nor the chart grants `escalate` or `bind` on any resource - mutation: re-add
  `escalate` to the chart only, which `TestHelmClusterRoleCoversGeneratedRole` alone would pass,
  must go red; and every (group, resource, verb) of `BuildSidecarRole` is granted by a chart rule
  without `resourceNames` - revert: add `create` to the builder's verbs, must go red.
- **e2e (A), no new test.** The full suite on both single-node legs, green on the narrowed chart:
  every e2e cluster writes a sidecar Role and RoleBinding under the chart ClusterRole (Fact), so
  this is the measurement on a real API server of the Kind version in CI.
- `make generate-all` leaves no diff; `make test-unit`, `make test-integration`, `make lint`
  green.
- Work list item 1: `grep -n 'refuses to' docs/security/privilege-footprint.md` finds the
  sentence only struck.

## History

- 2026-09-27, adversarial review at 84a39c2: every cited line of the builder, the reconciler,
  the marker, the chart rule, `role.yaml`, the drift guard, the integration suite, the Makefile
  and `release.yml` re-read and found as cited; the v1.36.4 and component-helpers v0.37.1 source
  re-read from the module cache, the two quoted Kubernetes pages re-fetched and the quotes found.
  Added: the v1.29.0 escalation code, read on GitHub, which moves envtest's version and the README
  floor from *Not verified* to *Verified*; that `bind` on `roles` does not reach a ClusterRole
  (`escalation_check.go:123-133`) and that the coverage test counts only RBAC-held rules, with the
  unconditional chart binding that makes that harmless; the history of every sidecar Role verb
  set (`44a974a`, `fa50b89`, `e32f0d2`) under an unchanged operator `pods` rule, which makes
  option A safe for an older image under the narrowed chart; the strongest argument for B
  (the operator's `pods` rights held through another authorizer than RBAC) and why it does not hold; in work list item
  4 the ADR 0013 D13 note (line 338), the Consequences bullet (line 357) and the superseded-rules
  line of the ADR index (lines 43-45), which H-2's close also touches; in item 6 056's channel (4)
  and line 332; in the integration test, that `parsePolicyRules` is not importable from
  `test/integration`. The introduction no longer says the finding "is moved" out of 056, which
  this filing did not edit. Class, severity, effort, urgency and the recommendation unchanged:
  hardening matches a file name without the embargo prefix, and A survives the argument above.
- 2026-09-27: filed from ticket 056 (its *Not verified* item on whether the sidecar Role needs
  `escalate`, and its *Related tickets* note that gap H-2 had no ticket) and gap H-2 of
  docs/security/privilege-footprint.md during the re-verification at 84a39c2; the builder, the
  reconciler write order, the marker, the chart rule, the generated role, the drift guard, the
  integration suite identity and the e2e install path re-read at 84a39c2; the escalation check
  read in the Kubernetes v1.36.4 and component-helpers v0.37.1 source and on the RBAC page, which
  answers the H-2 question by reading (neither verb is needed; `resourceNames` cannot narrow
  either); `git log -S escalate` traced the grant to `ce97f1b`, which already gave the operator
  every rule of the then sidecar Role; no API server was asked and no docker measurement taken (no
  Valkey behaviour involved). Security hardening (the token stays cluster-admin equivalent
  through the channels ADR 0013 D1 accepts; the CR API reaches neither verb), severity low,
  effort S, urgency `later` by rule 4 under the strict reading of rule 1, `now` under the reading
  that counts code reading (the false sentence at privilege-footprint.md:47-49, work list item 1).
  Options A (drop both, recommended) and B (keep, correct the records); four alternatives
  considered and not added. Ticket 056 is not edited by this filing; its pointer is work list
  item 6.
