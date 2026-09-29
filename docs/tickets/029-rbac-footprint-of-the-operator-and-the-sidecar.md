---
id: T29
title: the RBAC footprint of the operator and the sidecar - a sidecar pod-write admission policy, roles escalate and bind, a namespace-scoped mode
state: analysed       # facts and options complete in every part, open checks named
severity: medium      # the operator token is cluster-admin equivalent, with every Secret of the cluster in its cache
security: hardening
threat: "A holder of the `<cr>-sidecar` token can rewrite pod fields RBAC cannot narrow (images, finalizers, ownerReferences, hash annotations, activeDeadlineSeconds, tolerations, instanceRole on any pod of its cluster), and a holder of the operator's token, image or process is cluster-admin equivalent in every namespace, including writing itself any namespaced permission through roles escalate and bind; each part narrows one of these."
urgency: now          # the admission-policy part: ADR 0031, isolation-and-tenancy.md and privilege-footprint.md hold statements false by reading
effort: L             # M for the admission policy, S for the verbs, M for the namespace mode
blocked-by: decision  # Q1, Q3, Q4
filed-from: T25
opened: 2026-08-27
decided:
done:
---

# T29 - the RBAC footprint of the operator and the sidecar

**Scope.** Two principals hold RBAC grants the chart and the builder write: the per-cluster
`<cr>-sidecar` ServiceAccount and the operator's own ServiceAccount. Each grant reaches further
than the code uses, and RBAC alone cannot narrow all of it; this ticket decides per grant what is
narrowed and how, and corrects the security records that misstate the grants today.

- the sidecar's pod-patch grant and a chart-shipped ValidatingAdmissionPolicy, default off;
- the operator's `roles: escalate, bind`, which no Role or RoleBinding it writes needs;
- an opt-in namespace-scoped operator mode.

## Current state

### Shared facts

- The chart binds one ClusterRole
  ([`clusterrole.yaml:7-188`](../../deploy/helm/valkey-operator/templates/clusterrole.yaml)) to
  the operator ServiceAccount through one unconditional ClusterRoleBinding
  ([`clusterrolebinding.yaml:1-14`](../../deploy/helm/valkey-operator/templates/clusterrolebinding.yaml)).
  Every rule is on a namespaced resource (no `nonResourceURLs`). The operator token is mounted on
  purpose ([`_helpers.tpl:84-87`](../../deploy/helm/valkey-operator/templates/_helpers.tpl)).
- Per Valkey resource the operator writes one Role and one RoleBinding, both `<cr>-sidecar`, and
  no other RBAC object. The Role ([`rbac.go:49-79`](../../internal/builder/rbac.go#L49-L79))
  grants `pods: get, patch` narrowed only by `resourceNames` = this cluster's data pods
  ([`rbac.go:67-77`](../../internal/builder/rbac.go#L67-L77), names from
  [`rbac.go:97-121`](../../internal/builder/rbac.go#L97-L121)), or no rule when the list is empty;
  the RoleBinding ([`rbac.go:140-160`](../../internal/builder/rbac.go#L140-L160)) binds it to the
  ServiceAccount `<cr>-sidecar` ([`rbac.go:18-19`](../../internal/builder/rbac.go#L18-L19)). A
  foreign Role stops the step before the binding
  ([`valkey_controller.go:983-1004`](../../internal/controller/valkey_controller.go#L983-L1004)).
- RBAC has no field-level rules and no label selector, and the sidecar Role already uses
  `resourceNames`: nothing narrower is expressible in RBAC for it.
- envtest (1.29.0) runs kube-apiserver with the RBAC authorizer
  ([`suite_test.go:63-67`](../../test/integration/suite_test.go#L63-L67)), but the manager runs as
  `system:masters`, so every RBAC write is admitted there; `Environment.AddUser` offers a
  non-masters identity. CI Kind is 1.33.4; the README declares a 1.29 floor.
- The drift guard `TestHelmClusterRoleCoversGeneratedRole`
  ([`rbac_drift_test.go:160`](../../internal/controller/rbac_drift_test.go#L160)) reads the literal
  rules block of `clusterrole.yaml` and asserts only generated ⊆ chart.

### The sidecar's pod-patch grant

**Legitimate writes**, each a single-key JSON merge patch on pod metadata
([`labeler.go:255-281`](../../internal/sidecar/labeler.go#L255-L281)), the same shape in the
released v1.12.8 sidecar: the label `vko.gtrfc.com/instanceRole` on its own pod only
([`labeler.go:154`](../../internal/sidecar/labeler.go#L154),
[`drain.go:120-121`](../../internal/sidecar/drain.go#L120-L121)), with any role string `INFO`
reports ([`labeler.go:203`](../../internal/sidecar/labeler.go#L203)), so a policy compares keys,
not values; and the annotation `vko.gtrfc.com/drain-promoted-at` on a peer only
([`drain.go:217-218`](../../internal/sidecar/drain.go#L217-L218); the peer list excludes the pod
itself, [`drain.go:415-422`](../../internal/sidecar/drain.go#L415-L422)).

**What the token can additionally write**: every label and annotation, `ownerReferences`,
`finalizers`, and four update-mutable spec fields.

- A swapped `valkey` or `sidecar` image restarts that container at once; the operator replaces the
  pod only on its next data-tier pass via `podImageChanged`
  ([`rolling_update.go:485-500`](../../internal/controller/rolling_update.go#L485-L500)), except
  where [ADR 0007](../adr/0007-failover-aware-rolling-update.md) D6 defers a sidecar-only change. A
  swapped init-container image is latent until the pod sandbox is recreated. Since 2026-09-28
  both tiers compare every container and init-container image against the persisted template
  (`podImagesDrifted`, [ADR 0007](../adr/0007-failover-aware-rolling-update.md) D2), so a swapped
  `exporter` or init-container image is rolled away by the next data-tier pass — except on a
  single-replica non-persistent cluster, where a swap that also moves the sidecar image is
  deferred with it (D6).
- A finalizer blocks deletion, reported only as `PodTerminationStalled` after 2 min
  ([`rolling_update.go:120`](../../internal/controller/rolling_update.go#L120)).
- Deleting `pod-spec-hash` reduces `podSpecHashChanged` to a resources comparison, deleting
  `config-hash` makes `podAnnotationHashChanged` report no change
  ([`rolling_update.go:504-524`](../../internal/controller/rolling_update.go#L504-L524)): the roll
  is switched off.
- `activeDeadlineSeconds` (set or lowered) kills the pod and the StatefulSet recreates it (the
  Role's missing `delete` does not stop this); toleration additions are accepted;
  `ownerReferences` edits detach or reattach the pod.
- `instanceRole: master` on a replica routes a share of `-rw` writes to it; they fail `READONLY`
  ([`configmap.go:177`](../../internal/builder/configmap.go#L177)). `MultipleMasters` counts pods
  that answered master, not labels
  ([`split_brain_report.go:57-74`](../../internal/controller/split_brain_report.go#L57-L74)). The
  same relabel is reachable over the data plane: `REPLICAOF NO ONE` on a peer, whose honest
  labeler then labels it `master`.

**Impact.** Only the sidecar holds the `<cr>-sidecar` token, so the principal is dormant until a
sidecar is compromised. That container already holds the cluster password
([`statefulset.go:946-959`](../../internal/builder/statefulset.go#L946-L959)) and, under TLS, the
keypair ([`:971-978`](../../internal/builder/statefulset.go#L971-L978)); the pod levers add
control-plane effects and code execution in other containers, which the password does not give
(on valkey 9.1.1 and 8.1.9 `MODULE LOAD`, `DEBUG` and `CONFIG SET dir` are closed; `EVAL` runs).

**Why a VAP.** It is the only chart-shippable in-cluster control that reaches these fields. The
in-tree plugin `OwnerReferencesPermissionEnforcement` (not in the default set) closes
`ownerReferences` for a principal without `delete`, but it is an API-server flag and covers one
field. The sidecar token is kubelet-projected and pod-bound
([`statefulset.go:711-716`](../../internal/builder/statefulset.go#L711-L716)), so the API server
puts `authentication.kubernetes.io/pod-name` and `pod-uid` into `request.userInfo.extra` (1.29 and
1.33 source): a VAP can tell which pod a request comes from; impersonation (`--as`) carries no such
extra. Data pods run as `<cr>-sidecar`
([`statefulset.go:602`](../../internal/builder/statefulset.go#L602)), and `spec.serviceAccountName`
is not update-mutable, an anchor the token holder cannot rewrite.

**Constraints.**

- Nothing ships a policy (no template, no value);
  [ADR 0031 `:188-196`](../adr/0031-a-record-the-operator-trusts-lives-in-pod-spec.md#L188-L196)
  records it as "Filed, not taken here".
- [ADR 0015](../adr/0015-one-crd-validated-by-schema-only.md) D2 refuses admission webhooks that
  validate a `Valkey` object ([`0015:56-65`](../adr/0015-one-crd-validated-by-schema-only.md#L56-L65));
  its reason, a third-party webhook backend outage, is in its Context (`:31-44`) and rejected
  alternative (`:191-195`). A VAP has no backend and a policy on pods is outside D2's letter, but
  the heading "No admission webhook" reads as a general refusal.
- VAP is GA (`admissionregistration.k8s.io/v1`) from 1.30, beta and off by default in 1.28-1.29;
  envtest 1.29.0 cannot serve the v1 kind.
- No ADR states a default-off rule for chart values in general;
  [ADR 0021 D7](../adr/0021-per-resource-metrics-and-the-alert-that-was-missing.md#L121-L127)
  binds only its own values.
- `failurePolicy` defaults to `Fail` and covers CEL evaluation errors; an erroring match condition
  under `Fail` rejects the request; `objectSelector` matches if the old or the new object matches;
  CEL has `request.namespace`, not a bare `namespace`.
- The statefulset-controller and the garbage collector patch `ownerReferences`; administrators
  label and annotate.
- A policy object is no input of `ComputePodSpecHash` or the config hash: enabling it rolls
  nothing.

### The operator's roles escalate and bind

**The API server check** (v1.36.4 and v1.29.0 source). A Role write is admitted when the writer is
in `system:masters`, may `escalate` on `roles`, or already holds every rule of the Role at its
scope; a RoleBinding write when it is in `system:masters`, may `bind` the referenced Role, or holds
every rule of that Role. A rule without `resourceNames` covers one that lists them, and only rules
held through RBAC count; every chart install holds them through the ClusterRoleBinding.

**What the operator holds and is granted.** `pods: delete, get, list, patch, watch` without
`resourceNames`
([`clusterrole.yaml:50-59`](../../deploy/helm/valkey-operator/templates/clusterrole.yaml#L50-L59),
marker [`valkey_controller.go:209`](../../internal/controller/valkey_controller.go#L209)) covers
every rule of the sidecar Role, of every version the repository ever built, and so of the
binding's `roleRef`. Yet the ClusterRole grants `bind` and `escalate` on `roles` cluster-wide
([`clusterrole.yaml:150-163`](../../deploy/helm/valkey-operator/templates/clusterrole.yaml#L150-L163),
`bind` at `:155`, `escalate` at `:158`), generated from the marker
[`valkey_controller.go:217`](../../internal/controller/valkey_controller.go#L217) into
[`role.yaml:121-134`](../../config/rbac/role.yaml#L121-L134): both exempt the operator from a check
it passes anyway. `resourceNames` cannot narrow them (`escalate` is authorized with the empty name
of a create, `bind` with the Role name `<cr>-sidecar` per resource); they can only be kept or
dropped.

**The records.** [privilege-footprint.md](../security/privilege-footprint.md) lines 47-49 state
that without the verbs the API server refuses the sidecar Role: false by the check above, and only
`escalate` is named. Gap H-2 (lines 156-160) asks to drop `escalate` only.
[ADR 0013](../adr/0013-operator-is-cluster-wide-privileged.md) D3 retains both "until the narrower
configuration is actually tested"; D2, *Alternatives Considered* and *Residual risks* are hedged
correctly. The drift guard stays green for a chart that keeps `escalate` while the marker drops
it. Every e2e cluster installs with the chart and writes the sidecar Role under its ClusterRole.

**Impact.** With `escalate` a token holder writes a Role with any namespaced rule (pods/exec,
secrets create, roles and rolebindings) in any namespace, kube-system included; with `bind` it
binds that or any Role to any subject. Without the verbs the chain only delegates the operator's
own rules. A principal who may create `Valkey` resources has no path to the verbs. For a
maintainer, `escalate` today silently admits a sidecar rule the operator does not hold; without it
the write fails with 403 and reports `ReconcileBlocked`, phase `Error`, the order
[ADR 0014](../adr/0014-rbac-lives-in-three-places.md) already demands. The Kubernetes RBAC
good-practices page lists both verbs as escalation risks. Out of scope: `roles` and `rolebindings`
`delete`, `update`, `patch`, and whether `roles: delete` is needed.

### A namespace-scoped operator mode

**Four channels** make the operator token cluster-admin equivalent in every namespace:
`secrets: delete, get, list, watch` ([`clusterrole.yaml:64-72`](../../deploy/helm/valkey-operator/templates/clusterrole.yaml));
`deployments`, `statefulsets: create, update, patch` ([`:73-85`](../../deploy/helm/valkey-operator/templates/clusterrole.yaml)),
running code under any ServiceAccount; `pods: patch`, running code in any pod by an image change,
kube-system and flux-system included; and `roles: bind, create, escalate` with
`rolebindings: create` ([`:150-175`](../../deploy/helm/valkey-operator/templates/clusterrole.yaml)),
writing itself any namespaced permission (ADR 0013 D2), a channel that shrinks to delegating its
own rules under Q3 = A. `configmaps`, `services` create/update/patch
([`:36-49`](../../deploy/helm/valkey-operator/templates/clusterrole.yaml)) add tampering. Narrowing
the Secret rule or cache alone takes no Secret out of reach, and a label-filtered cache narrows
memory, never the grant; only moving every rule out of the cluster-wide binding narrows the token.
Because every rule is namespaced, the unchanged ClusterRole can be bound per namespace by
RoleBindings, with no second rules copy for the drift guard.

**Cache.** [`cmd/main.go:102-110`](../../cmd/main.go) sets no cache options, so every informer is
cluster-wide. The controller watches `Valkey` (`GenerationChangedPredicate`), owns nine namespaced
kinds and watches every `Secret`
([`valkey_controller.go:2984-3002`](../../internal/controller/valkey_controller.go)), the rotation
and password-change trigger (the healthy pass returns no requeue,
[`:390-396`](../../internal/controller/valkey_controller.go); resync 10 h). Every Secret read names
`v.Namespace`, so a namespace-restricted cache serves every read unchanged. pprof is off; a smaller
cache alone protects against nothing the token does not already open.

**Facts the mode depends on.**

- Leader election is on by default ([`values.yaml:86-87`](../../deploy/helm/valkey-operator/values.yaml)); the release namespace needs `leases` and core `events: create, patch`. `OperatorNamespace` is only a NetworkPolicy peer.
- With `DefaultNamespaces` an informer is synced only when every per-namespace informer is. A listed namespace whose RoleBinding is gone (namespace recreated after the last `helm upgrade`) answers 403; a running operator keeps serving the others, but after a restart the 2 min cache sync timeout expires and the manager exits: a crashloop for every listed namespace, the per-namespace form of ADR 0014 D8.
- A listed namespace must exist at install and upgrade (NamespaceLifecycle admission).
- The CRD ships unconditionally (`templates/crd.yaml`): one release per cluster serving a list, and the installer still needs cluster-scoped rights.
- The pre-upgrade hook holds a ClusterRole on `valkeys` and `customresourcedefinitions` ([`pre-upgrade-rbac.yaml:29-50`](../../deploy/helm/valkey-operator/templates/pre-upgrade-rbac.yaml)) because `migrate.Run` lists every Valkey ([`migrate.go:54-55`](../../cmd/migrate/migrate.go)); it is optional (`preUpgradeHook.enabled`, default `true`, gap H-3).
- The metrics collector lists Valkey from the cache ([`collector.go:161`](../../internal/metrics/collector.go)): an unlisted CR has no `vko_valkey_*` series and `ValkeySpecNotObserved` cannot fire for it. No finalizer is set, so it still deletes cleanly.
- The e2e fixture `createNamespace` deletes and recreates its namespace and deletes it after a passing test ([`e2e_test.go:101-133`](../../test/e2e/e2e_test.go)); a RoleBinding the chart rendered there dies with it.
- Confining the operator by a VAP instead would also need VAP GA 1.30 above the 1.29 floor.

**Impact.** On every install, whoever obtains the operator's token or runs code in its process
holds cluster-admin-equivalent power, live only after a compromise. Multi-tenant clusters cannot
confine the operator; no multi-tenant user is named, the production fleet has one owner.

## Required changes

### Shared, independent of the open questions

1. [privilege-footprint.md](../security/privilege-footprint.md), one change: lines 47-55 - the API
   server does not refuse the sidecar Role without the verbs, and `bind` is as unneeded as
   `escalate`; `:97` counts "Two writes" reaching operator decisions, but the hash annotations do
   too - point at the `isolation-and-tenancy.md` table instead of counting.
2. [`isolation-and-tenancy.md`](../security/isolation-and-tenancy.md#what-does-not-hold): `:83`
   calls its table "enumerated rather than sampled"; add rows for `activeDeadlineSeconds`,
   `initContainers[*].image` and toleration additions. Row `:96`: a swapped image runs at once.
   Row `:88`: writes to a mislabeled replica fail `READONLY`. `:105`: "nothing narrower is
   expressible in RBAC". Name `OwnerReferencesPermissionEnforcement` in the `ownerReferences` row.
3. [ADR 0031](../adr/0031-a-record-the-operator-trusts-lives-in-pod-spec.md#L188-L196): `:192-193`
   says ADR 0015 D2 has a stated reason; it has none, the reason is its Context and rejected
   alternative. `:190-191`: "the only chart-shippable control". Residual risk `:209-213`: add the
   three spec fields.

### Shared, depending on the answers

4. (Q3, Q4) [ADR 0013](../adr/0013-operator-is-cluster-wide-privileged.md) is amended once for both
   answers: D1 (Q4: the opt-in exception, or the recorded rejection of "a namespaced Role per
   watched namespace"), D2 and D3 (Q3), superseded text marked in place, the D13 note naming H-1
   and H-2, the *Consequences* bullet ending "reach namespaced admin everywhere", *Alternatives
   Considered*, *Residual risks*, and the index row in [`docs/adr/README.md`](../adr/README.md).
5. (Q3 = A, Q4 = B) Both integration tests bind a non-masters identity to the chart ClusterRole's
   rules in envtest; build the rules parser once as a helper usable from `test/integration/`
   instead of copying `parsePolicyRules` out of `internal/controller`.

### Sidecar admission policy

6. (Q1) ADR 0031 `:196` states the decision and an ADR plus index row records it. Q1 = A: a new
   ADR with its own default-off decision (ADR 0021 D7 as precedent; rejected alternatives: a
   documented recipe, on by default with a 1.30 floor, removing the sidecar's `pods: patch`
   because it reopens [ADR 0012](../adr/0012-the-sidecar-records-its-drain-promotion-on-the-pod.md),
   a `.Capabilities` gate; the condition for flipping the default, for example a release run on
   the production fleet with no refused legitimate sidecar write) and a Status note on ADR 0015
   that D2 does not reach an in-process policy on pods. Q1 = C: an ADR recording the refusal, the
   security page keeps the rows as accepted residual risk.
7. (Q1 = A) Policy and binding templates, a default-off value in `values.yaml`, the README
   [Helm chart values](../../README.md#helm-chart-values) row, and `isolation-and-tenancy.md:94-96`
   naming the policy as the opt-in control. Rules:
   - gate on the value alone, as [`servicemonitor.yaml:1-7`](../../deploy/helm/valkey-operator/templates/servicemonitor.yaml#L1-L7)
     does; no `.Capabilities` gate (plain `helm template` reports the v1 kind as absent);
   - `failurePolicy: Fail`, binding `validationActions: [Deny]`;
   - `matchConstraints` on `pods` `UPDATE` only (`pods/status` not named); `objectSelector` on
     `app.kubernetes.io/managed-by: vko.gtrfc.com` and `app.kubernetes.io/component: valkey`;
   - `matchConditions` that cannot error (guard every map access with `in`);
   - principal anchored on the stored pod: `request.userInfo.username == 'system:serviceaccount:'
     + request.namespace + ':' + oldObject.spec.serviceAccountName`;
   - compare labels without `instanceRole`, annotations without `drain-promoted-at`,
     `ownerReferences`, `finalizers` and `spec`, ignore `managedFields`; the `instanceRole` rule
     follows Q2.
8. (Q1 = A) Tests: the value on in `test/e2e/helm-values.yaml`, so every drain and roll e2e is a
   positive control; a negative Kind e2e with a pod-bound token (`kubectl create token
   <cr>-sidecar --bound-object-kind Pod --bound-object-name <cr>-0`), not `--as`: patches of
   `ownerReferences`, `finalizers`, a container or init-container image, `activeDeadlineSeconds`,
   a toleration or `config-hash` are refused with the policy's message, under Q2 = A-prime also
   `instanceRole` on another pod; operator, statefulset-controller and administrator patches pass;
   the create response and `status.typeChecking` show no cost or type error. `helm template`
   renders nothing with the value off, policy plus binding with it on.

### Roles escalate and bind

9. (Q3 = A) Remove `escalate;bind` from the marker at
   [`valkey_controller.go:217`](../../internal/controller/valkey_controller.go#L217), run
   `make manifests`, remove `bind` (`:155`) and `escalate` (`:158`) from `clusterrole.yaml`, and
   say at `:149` why no escalation verb is needed. No chart value keeps them; no migration step,
   RBAC re-checks only writes.
10. (Q3 = A) Integration test: a non-masters user (`testEnv.AddUser`) bound to the chart rules
    creates `BuildSidecarRole` (three names) and `BuildSidecarRoleBinding`, updates the Role to five
    names and the binding's labels: all succeed. A Role with `pods/exec: create` and a RoleBinding
    to an admin-written Role with `secrets: create` are refused with `IsForbidden`. Mutations:
    adding `escalate` turns the first refusal red, `bind` the second, `create` in the builder's
    verbs the positive half.
11. (Q3 = A) Unit assertions in `rbac_drift_test.go`: neither `role.yaml` nor the chart grants
    `escalate` or `bind` (mutation: re-add `escalate` to the chart only); every
    (group, resource, verb) of `BuildSidecarRole` is granted by a chart rule without
    `resourceNames` (mutation: add `create` to the builder).
12. (Q3) privilege-footprint.md under A: the `roles` row loses both verbs and the "privilege
    ceiling" consequence, the `rolebindings` row and the summary state delegation of the operator's
    own rules, H-2 closed. Under B: D3 becomes "kept by choice" and H-2 states the verbs are not
    needed and are kept anyway.
13. (Q3 = A) `make generate-all` leaves no diff; `make test-unit`, `make test-integration`,
    `make lint` and the full e2e suite on both single-node legs green.

### Namespace-scoped mode

14. (Q4 = B) [`cmd/main.go`](../../cmd/main.go): a namespace-list flag setting
    `cache.Options.DefaultNamespaces`; empty keeps the cluster-wide cache.
15. (Q4 = B) Chart: a value (default empty) passed to the flag; `clusterrole.yaml` unchanged and
    always rendered; the ClusterRoleBinding only while the list is empty, otherwise one RoleBinding
    per listed namespace to the same ClusterRole, plus a Role and RoleBinding in the release
    namespace for `leases` and core `events: create, patch` unless it is listed (not the full
    ClusterRole, which would expose the release namespace Secrets, Helm release Secrets included).
    With the list empty the render is byte-identical in RBAC to today.
16. (Q4 = B) Integration test (RBAC authorizer): with the operator ServiceAccount bound only by
    RoleBindings, a SubjectAccessReview in an unlisted namespace denies `secrets get`,
    `pods patch`, `deployments create`, `roles create`; in a listed namespace the impersonated
    client creates the sidecar Role and RoleBinding; a manager with `DefaultNamespaces` does not
    reconcile a CR in an unlisted namespace.
17. (Q4 = B) `docs/operations/installation.md`: listed namespaces must exist before install and
    upgrade; a recreated namespace needs its RoleBinding back before the next operator restart;
    adding a namespace is a values change; the hook stays cluster-scoped unless disabled; what an
    unlisted CR looks like (Q5 = A) or the status path (Q5 = B).
18. (Q4 = B) Docs and ADRs beyond item 4: ADR 0014 (binding switch, D8 crashloop per namespace);
    ADR 0016 D2 and D5 (the Secret watch is no longer always cluster-wide); H-1 in
    [`privilege-footprint.md`](../security/privilege-footprint.md#h-1) with its "one more
    consumer" sentence; [`trust-boundaries.md:12`](../security/trust-boundaries.md);
    [`README.md:187-189`](../../README.md) and the values table. Under Q4 = A, H-1 is restated as
    an accepted residual risk.
19. (Q4 = B, optional) An e2e leg in the mode: the fixture stops recreating listed namespaces or
    creates the RoleBinding itself, and ADR 0017 D31 (three legs) is amended. On a real install,
    `kubectl auth can-i --list --as=system:serviceaccount:<release-ns>:<sa> -n kube-system` shows
    no operator rule.

## Open questions

### Q1: May the chart ship a default-off ValidatingAdmissionPolicy for the sidecar's pod writes? (sidecar admission policy)

The policy would stop the sidecar token's pod levers where RBAC cannot. It changes no RBAC, pod
template or data-plane right; only installs that enable it on Kubernetes 1.30 or later are
protected. The deciding fact is outside the repository: which Kubernetes versions the production
fleet runs, whether its API servers enable `OwnerReferencesPermissionEnforcement`, and whether you
would enable the value there. Below 1.30, or if you would not enable it, C wins.

- **A - admit it (recommended):** a new ADR, a Status note on ADR 0015, a template pair, a value
  and a negative Kind e2e (effort M). Every future sidecar write needs a policy change; a CEL
  defect fails closed on the `instanceRole` write where the value is on, which the e2e with the
  value on catches before a release.
- **C - refuse and accept the gap:** an ADR records the refusal, the security page keeps the rows
  as residual risk (effort S). Every install keeps immediate image swaps, finalizer pinning, roll
  suppression by hash deletion, the `activeDeadlineSeconds` kill and `ownerReferences` edits.

A is recommended: it is the only chart-shippable control for these levers, it runs in the API
server so the webhook outage behind ADR 0015 cannot recur, and a non-opting install pays nothing.

**Answer:** _open_

### Q2: Should the policy tie an `instanceRole` write to the pod the token was issued for? (sidecar admission policy)

Only if Q1 = A. Both options let the principal change only the `instanceRole` label and the
`drain-promoted-at` annotation; they differ in which pod may receive the label.

- **A - allow-list only:** the key may change on any pod of the cluster; a token holder can still
  label any pod `master`, although no legitimate path does.
- **A-prime - allow-list plus pod binding (recommended):** `instanceRole` may change only when the
  `pod-uid` and `pod-name` extras equal `oldObject.metadata.uid` and `.name`; a request without the
  claim may not change it. Adds two negative e2e cases with a pod-bound token. If the claim were
  missing on a live cluster, every label write and every drain and roll e2e would fail.

A-prime is recommended: two CEL comparisons give per-pod identity that A and RBAC lack and match
the code, which labels only its own pod. It is defence in depth; a password holder can still
promote a peer over the data plane.

**Answer:** _open_

### Q3: Does the operator keep or drop `roles: escalate, bind` in its ClusterRole? (roles escalate and bind)

The operator holds every rule of the one Role it writes, so both verbs only exempt it from a check
it passes anyway, and `resourceNames` cannot scope them. ADR 0013 D3 keeps them only "until the
narrower configuration is actually tested". The answer is the same with or without Q4 = B.

- **A - drop both, prove the subset in the integration and unit tiers (recommended).** Effort S;
  removes the Role-escalation channel; a future sidecar rule needs the ClusterRole rule first,
  which the unit assertion catches before a release.
- **B - keep both, correct the records only.** Effort XS; keeps a cluster-wide escalation grant
  with no function, which D3 can no longer justify.

A is recommended: the verbs buy nothing, the integration test is exactly the test D3 waits for, and
the "strict subset" claim becomes a tested invariant. Dropping only one verb is not sensible, since
each alone reopens a channel at the same cost.

**Answer:** _open_

### Q4: Should ADR 0013 D1 be reopened with an opt-in namespace-scoped mode? (namespace-scoped mode)

An opt-in namespace list would confine the operator token to the listed namespaces; the default
install, the installer's rights, the hook and every Valkey pod stay as they are (the list reaches
no pod builder, so nothing rolls).

- **A - keep D1 and record the refusal.** Cost XS. After a compromise a kube-system or
  flux-system pod image patch or workload still makes the token cluster-admin.
- **B - opt-in list, the unchanged ClusterRole bound per listed namespace (recommended).** Cost M.
  Confines the token to the listed namespaces plus what their ServiceAccounts hold; in this fleet
  gitlab, harbor and iam would be listed and their Secrets stay in reach, but kube-system,
  flux-system and cert-manager leave it. Costs: a lost RoleBinding crashloops the operator for
  every listed namespace on its next restart; a new namespace needs a values change; switching an
  existing install makes the old pod see 403 until the rollout; a CR in an unlisted namespace is
  not served at all (no recovery, no split-brain resolution, frozen annotations), which matters for
  database-examples under Chaos Mesh.

B is recommended as the only option that changes what a compromised token can do, at no cost to
existing installs; A wins if no cluster the owner runs would set a list. The deciding question:
would the production fleet set the list (gitlab, gpt, harbor, iam, database-examples and every
other namespace holding a Valkey CR)?

**Answer:** _open_

### Q5: How is a Valkey CR in an unlisted namespace reported? (namespace-scoped mode)

Only if Q4 = B. Such a CR never reaches `Reconcile`: empty status (blank Phase column), no metrics,
no alert, no recovery, clean deletion.

- **A - report nothing, document the boundary (recommended).** Cost XS documentation; only the
  blank Phase column shows it.
- **B - a cluster-wide Valkey watch that writes a condition.** Cost M: a Valkey informer outside
  the list, a namespace gate at the `Reconcile` entry, a `conditionRegistry` row (ADR 0027),
  collector filtering, and a cluster-wide `valkeys: get, list, watch` plus
  `valkeys/status: patch, update` binding, which lets a token holder read every Valkey spec, forge
  status in excluded namespaces and widens the metrics inventory back to the whole cluster.

A is recommended: the list is the administrator's own statement of scope, and it writes nothing
outside the list; its price must be documented.

**Answer:** _open_

## Not verified

- Admission of the sidecar Role and binding without `escalate` and `bind`: read from the v1.36.4 and v1.29.0 source, no API server asked (the Q3 integration test measures 1.29, the e2e suite CI's Kind).
- That the policy's CEL fits the VAP cost budget (create it on Kind 1.33.4, read the response and `status.typeChecking`).
- That the pod-bound sidecar token carries the `pod-name` and `pod-uid` extras on a live API server, and which server-set metadata differs between `object` and `oldObject` on a label-only merge patch (1.30-1.36); the Kind e2e settles both.
- That a fleet still on the v1.12.8 sidecar passes the policy (same patch shape by reading).
- The restart crashloop under `DefaultNamespaces`, read from controller-runtime v0.25.1; an envtest manager over two namespaces, one without its RoleBinding, and a restart settles it.
- Which ServiceAccounts in gitlab, gpt, harbor, iam and database-examples hold cluster-wide rights; whether `cluster-ca` is a CA ClusterIssuer with its key Secret in `cert-manager`; whether Flux helm-controller drift detection would restore a lost RoleBinding and reports an unserved CR as ready.
- The memory cost of caching every Secret of the cluster.

## Related

- T44 - its options narrow or retire the pre-upgrade hook ClusterRole.
- T48 - under Q4 = B the unauthenticated metrics inventory covers only listed namespaces; Q5 = B widens it back.
- T43 - its chart render gate gains a policy row (rendered without `--api-versions`) and the namespace-list render path.
- T40 - also corrects ADR 0013 D3 (its sidecar verb list); coordinate with item 4.
