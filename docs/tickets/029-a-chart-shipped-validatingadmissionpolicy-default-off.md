---
id: T29
title: a chart-shipped ValidatingAdmissionPolicy, default off
state: analysed       # facts and options complete, open checks named
severity: low
security: hardening
threat: "would additionally cover every pod field a holder of the `<cr>-sidecar` token can rewrite beyond the instanceRole label and the drain stamp - ownerReferences, finalizers, any other label or annotation including config-hash and pod-spec-hash, container images, init-container images (effective only when the pod sandbox is recreated), activeDeadlineSeconds and toleration additions - and could tie an instanceRole write to the pod the token was issued for; RBAC can express none of it"
urgency: now          # rule 1: ADR 0031 and isolation-and-tenancy.md hold statements false by reading (Required changes 1-3); icebox once corrected
effort: M
blocked-by: decision  # Q1, and the new ADR it produces
filed-from: T25
opened: 2026-08-27
decided:
done:
---

# T29 - a chart-shipped ValidatingAdmissionPolicy, default off

## Current state

**The grant.** Each cluster's sidecar Role grants `get` and `patch` on that cluster's data pods,
narrowed only by `resourceNames` ([`rbac.go:67-77`](../../internal/builder/rbac.go#L67-L77),
names from [`rbac.go:97-121`](../../internal/builder/rbac.go#L97-L121)), bound to the
ServiceAccount `<cr>-sidecar` ([`rbac.go:18-19`](../../internal/builder/rbac.go#L18-L19)). RBAC
has no field-level rules, and `resourceNames` is already in use, so nothing narrower is
expressible in RBAC. The operator's own cluster-wide `pods: patch`
([`clusterrole.yaml:50-59`](../../deploy/helm/valkey-operator/templates/clusterrole.yaml#L50-L59))
is out of scope; the operator is trusted.

**What the sidecar legitimately writes**, each as a single-key JSON merge patch on pod metadata
([`labeler.go:255-281`](../../internal/sidecar/labeler.go#L255-L281)):

- the label `vko.gtrfc.com/instanceRole` on its own pod only
  ([`labeler.go:154`](../../internal/sidecar/labeler.go#L154),
  [`drain.go:120-121`](../../internal/sidecar/drain.go#L120-L121)); the value is any role string
  `INFO` reports ([`labeler.go:203`](../../internal/sidecar/labeler.go#L203)), so a policy
  compares keys, not values;
- the annotation `vko.gtrfc.com/drain-promoted-at` on a peer only
  ([`drain.go:217-218`](../../internal/sidecar/drain.go#L217-L218); the peer list excludes the pod
  itself, [`drain.go:415-422`](../../internal/sidecar/drain.go#L415-L422)).

Nothing else. The released v1.12.8 sidecar uses the same patch shape.

**What the token can additionally write.** Every label and annotation (including `config-hash`
and `pod-spec-hash`), `ownerReferences`, `finalizers`, and four update-mutable spec fields:
`containers[*].image`, `initContainers[*].image`, `activeDeadlineSeconds` (set or lowered) and
toleration additions. Per lever:

- A swapped `valkey` or `sidecar` image restarts that container on the new image at once
  (kubelet); the operator replaces the pod only on its next data-tier pass via `podImageChanged`
  ([`rolling_update.go:485-500`](../../internal/controller/rolling_update.go#L485-L500)), except
  where [ADR 0007](../adr/0007-failover-aware-rolling-update.md) D6 defers a sidecar-only change.
- A swapped init-container image is latent until the pod sandbox is recreated (no generated init
  container is a native sidecar).
- A finalizer keeps the pod from being deleted, reported only as `PodTerminationStalled` after
  2 min ([`rolling_update.go:120`](../../internal/controller/rolling_update.go#L120)).
- Deleting `pod-spec-hash` reduces `podSpecHashChanged` to a resources comparison, deleting
  `config-hash` makes `podAnnotationHashChanged` report no change
  ([`rolling_update.go:504-524`](../../internal/controller/rolling_update.go#L504-L524)): the roll
  is switched off.
- `activeDeadlineSeconds` kills the pod; the StatefulSet recreates it from the template (the
  Role's missing `delete` does not stop this).
- `ownerReferences` edits detach or reattach the pod.
- `instanceRole: master` on a replica routes a share of `-rw` writes to it; they fail `READONLY`
  (`replica-read-only yes`, [`configmap.go:177`](../../internal/builder/configmap.go#L177)).
  `MultipleMasters` counts pods that answered master, not labels
  ([`split_brain_report.go:57-74`](../../internal/controller/split_brain_report.go#L57-L74)). The
  same relabel is reachable over the data plane: `REPLICAOF NO ONE` on a peer, whose honest
  labeler then labels it `master`.

**Impact.** Principal: whoever authenticates as `<cr>-sidecar`; among generated containers only
the sidecar holds that token, so the principal is dormant until a sidecar is compromised. Verb:
`patch`. Target: the data pods of that cluster. The sidecar container already holds the cluster
password ([`statefulset.go:946-959`](../../internal/builder/statefulset.go#L946-L959)) and, under
TLS, the keypair ([`:971-978`](../../internal/builder/statefulset.go#L971-L978)). What the pod
levers add is control-plane effects and code execution in other containers: the password gives
no such route (on valkey 9.1.1 and 8.1.9 `MODULE LOAD`, `DEBUG` and `CONFIG SET dir` are closed;
`EVAL` runs).

**Why a VAP.** It is the only chart-shippable in-cluster control that reaches these fields.
The in-tree admission plugin `OwnerReferencesPermissionEnforcement` (not in the default set)
closes `ownerReferences` for a principal without `delete`, but it is an API-server flag and
covers one field. The sidecar's token is kubelet-projected and pod-bound
([`statefulset.go:711-716`](../../internal/builder/statefulset.go#L711-L716)), so the API server
puts `authentication.kubernetes.io/pod-name` and `pod-uid` into `request.userInfo.extra` (1.29
and 1.33 source); a VAP can tell which pod a request comes from, RBAC cannot. Impersonation
(`--as`) carries no such extra. Data pods run as `<cr>-sidecar`
([`statefulset.go:602`](../../internal/builder/statefulset.go#L602)) and
`spec.serviceAccountName` is not update-mutable, so it is an anchor the token holder cannot
rewrite.

**Constraints.**

- Nothing ships a policy today: no template under `deploy/helm/valkey-operator/templates/`, no
  value in `values.yaml`. [ADR 0031 `:188-196`](../adr/0031-a-record-the-operator-trusts-lives-in-pod-spec.md#L188-L196)
  records the VAP as "Filed, not taken here".
- [ADR 0015](../adr/0015-one-crd-validated-by-schema-only.md) D2 refuses admission webhooks that
  validate a `Valkey` object ([`0015:56-65`](../adr/0015-one-crd-validated-by-schema-only.md#L56-L65));
  its reason, a third-party webhook backend outage, is in its Context (`:31-44`) and rejected
  alternative (`:191-195`). A VAP runs in the API server and has no backend, and a policy on pods
  is outside D2's letter, but the heading "No admission webhook" reads as a general refusal.
- VAP is GA (`admissionregistration.k8s.io/v1`) from Kubernetes 1.30, beta and off by default in
  1.28-1.29. The README declares a 1.29 floor; envtest is 1.29.0 and cannot serve the v1 kind;
  CI Kind is 1.33.4.
- No ADR states a default-off rule for chart values in general. The precedent is
  [ADR 0021 D7](../adr/0021-per-resource-metrics-and-the-alert-that-was-missing.md#L121-L127),
  which binds only its own values; the new ADR must state its own default-off decision.
- VAP failure semantics: `failurePolicy` defaults to `Fail` and covers CEL evaluation errors; a
  match condition that errors under `Fail` rejects the request; `objectSelector` matches if the
  old or the new object matches. CEL has `request.namespace`, not a bare `namespace`.
- Other writers of these pods: the statefulset-controller and the garbage collector patch
  `ownerReferences`; administrators label and annotate.
- A policy object is no input of `ComputePodSpecHash` or the config hash: enabling it rolls
  nothing.

## Required changes

### Independent of the open questions

These correct tracked statements that are false or incomplete today.

1. [`isolation-and-tenancy.md`](../security/isolation-and-tenancy.md#what-does-not-hold): `:83`
   calls its table "enumerated rather than sampled"; make that true by adding rows for
   `activeDeadlineSeconds` (pod kill, the StatefulSet recreates it), `initContainers[*].image`
   (latent until sandbox recreation) and toleration additions. Row `:96`: a swapped container
   image runs at once. Row `:88`: writes to a mislabeled replica fail `READONLY`. `:105`: "nothing
   narrower is expressible in RBAC". Name `OwnerReferencesPermissionEnforcement` in the
   `ownerReferences` row.
2. [ADR 0031](../adr/0031-a-record-the-operator-trusts-lives-in-pod-spec.md#L188-L196):
   `:192-193` says D2 of ADR 0015 has a stated reason; it has none, the reason is ADR 0015's
   Context and rejected alternative. `:190-191`: "the only chart-shippable control". Residual
   risk `:209-213`: add the three spec fields.
3. [`privilege-footprint.md:97`](../security/privilege-footprint.md) counts "Two writes" that
   reach operator decisions; the hash annotations do too. Point at the `isolation-and-tenancy.md`
   table instead of counting.

### Depends on the answers

4. Q1 either way: ADR 0031 `:196` states the decision, and an ADR plus its
   [index](../adr/README.md) row records it. Under A: a new ADR (own default-off decision, ADR
   0021 D7 as precedent; rejected alternatives: a documented recipe, on by default with a 1.30
   floor, removing the sidecar's `pods: patch` because it reopens
   [ADR 0012](../adr/0012-the-sidecar-records-its-drain-promotion-on-the-pod.md), a
   `.Capabilities` gate; the condition for flipping the default, for example a release run on the
   production fleet with no refused legitimate sidecar write) and a Status note on ADR 0015 that
   D2 does not reach an in-process policy on pods. Under C: an ADR recording the refusal, and the
   security page keeps the rows as accepted residual risk.
5. Under Q1 = A: policy and binding templates under `deploy/helm/valkey-operator/templates/`, a
   default-off value in `values.yaml`, the README
   [Helm chart values](../../README.md#helm-chart-values) row, and the rows at
   `isolation-and-tenancy.md:94-96` naming the policy as the opt-in control. Rules:
   - gate on the value alone, as [`servicemonitor.yaml:1-7`](../../deploy/helm/valkey-operator/templates/servicemonitor.yaml#L1-L7)
     does; no `.Capabilities` gate, because plain `helm template` reports the v1 kind as absent
     and would render the control to nothing;
   - `failurePolicy: Fail`, binding `validationActions: [Deny]`;
   - `matchConstraints` on `pods` `UPDATE` only (`pods/status` not named); `objectSelector` on
     `app.kubernetes.io/managed-by: vko.gtrfc.com` and `app.kubernetes.io/component: valkey`;
   - `matchConditions` that cannot error (guard every map access with `in`), so a CEL error can
     refuse only the sidecar's own writes;
   - principal anchored on the stored pod: `request.userInfo.username == 'system:serviceaccount:'
     + request.namespace + ':' + oldObject.spec.serviceAccountName`;
   - compare named surfaces (labels without `instanceRole`, annotations without
     `drain-promoted-at`, `ownerReferences`, `finalizers`, `spec`), ignore `managedFields`; the
     `instanceRole` rule follows Q2.
6. Under Q1 = A, tests: the value on in `test/e2e/helm-values.yaml`, so every drain and roll e2e
   is a positive control; a negative Kind e2e with a pod-bound token (for example
   `kubectl create token <cr>-sidecar --bound-object-kind Pod --bound-object-name <cr>-0`), not
   `--as`: patches of `ownerReferences`, `finalizers`, a container or init-container image,
   `activeDeadlineSeconds`, a toleration or `config-hash` are refused with the policy's message,
   and under Q2 = A-prime so is `instanceRole` on another pod; operator, statefulset-controller
   and administrator patches pass; the create response and `status.typeChecking` show no cost or
   type error. `helm template` renders nothing with the value off and policy plus binding with it
   on. Kind, not envtest, because envtest 1.29 cannot serve the v1 kind.

## Open questions

### Q1: May the chart ship a default-off ValidatingAdmissionPolicy for the sidecar's pod writes?

The policy would stop the pod levers above before they take effect, where RBAC cannot. It
changes no RBAC, no pod template and no data-plane right; existing installs are unchanged, and
only installs that enable it on Kubernetes 1.30 or later are protected. The deciding fact is
not in the repository: which Kubernetes versions the production fleet runs, whether its API
servers enable `OwnerReferencesPermissionEnforcement`, and whether you would enable the value
there. If the fleet is below 1.30 or you would not enable it, C wins.

- **A - admit it (recommended):** a new ADR, a Status note on ADR 0015, a template pair, a value
  and a negative Kind e2e (effort M). Every future sidecar write needs a policy change; a CEL
  defect fails closed on the `instanceRole` write where the value is on, which the e2e with the
  value on catches before a release.
- **C - refuse and accept the gap:** an ADR records the refusal, the security page keeps the rows
  as residual risk (effort S). Every install keeps image swaps that run at once, finalizer
  pinning, roll suppression by hash deletion, the `activeDeadlineSeconds` kill and
  `ownerReferences` edits.

A is recommended because it is the only chart-shippable control for these levers, the password
gives none of them, it runs in the API server so the webhook outage behind ADR 0015 cannot recur,
and it costs a non-opting install nothing.

**Answer:** _open_

### Q2: Should the policy tie an `instanceRole` write to the pod the token was issued for?

Only relevant if Q1 = A. Both options use the same template and allow the principal to change
only the `instanceRole` label and the `drain-promoted-at` annotation; they differ in which pod
may receive the label.

- **A - allow-list only:** the key may change on any pod of the cluster. A token holder can still
  label any pod `master`, although no legitimate path does.
- **A-prime - allow-list plus pod binding (recommended):** `instanceRole` may change only when
  the `pod-uid` extra equals `oldObject.metadata.uid` and the `pod-name` extra equals
  `oldObject.metadata.name`; a request without the claim may not change it. Adds two negative e2e
  cases that need a pod-bound token. If the claim were missing on a live cluster, every label
  write would be refused and every drain and roll e2e would fail.

A-prime is recommended: two CEL comparisons give per-pod identity that A and RBAC lack, and it
matches the code, which labels only its own pod. It does not close the `-rw` diversion for a
password holder, who can promote a peer over the data plane; it is defence in depth.

**Answer:** _open_

## Not verified

- That the CEL fits the VAP cost budget; creating the policy on Kind 1.33.4 and reading the
  create response and `status.typeChecking` settles it.
- That the pod-bound token carries the `pod-name` and `pod-uid` extras on a live API server; the
  Kind e2e settles it.
- Which server-set metadata differs between `object` and `oldObject` on a label-only merge patch
  (1.30-1.36); the e2e settles it.
- That a fleet still on the v1.12.8 sidecar passes the policy (same patch shape by reading).

## Related

- T58: its chart render gate adds a policy row, rendered without `--api-versions`, once this
  lands.
