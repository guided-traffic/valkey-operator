---
id: T29
title: a chart-shipped ValidatingAdmissionPolicy, default off
state: analysed       # was filed; facts, options and the open checks complete at 84a39c2 (History 2026-09-27)
severity: low
security: hardening
threat: "would additionally cover every pod field a holder of the `<cr>-sidecar` token can rewrite beyond the instanceRole label and the drain stamp - ownerReferences, finalizers, any other label or annotation including config-hash and pod-spec-hash, container images, init-container images (effective only when the pod sandbox is recreated), activeDeadlineSeconds and toleration additions - and could tie an instanceRole write to the pod the token was issued for; RBAC can express none of it"  # old value in History, 2026-09-27
urgency: now          # rule 1 by reading, per the 018/044/045/062 precedent: ADR 0031:192-193 and isolation-and-tenancy.md:83 are false by reading, :88, :96 and :105 incomplete (Work list 1, 2); back to icebox (rule 5) once corrected. Was icebox
effort: M
blocked-by: decision  # was adr-0015; ADR 0015 D2 is scoped to what validates a Valkey object, the blocker is Decision 1 and the new ADR it produces
filed-from: T25
opened: 2026-08-27
decided:
done:
---

# T29 - a chart-shipped ValidatingAdmissionPolicy, default off

**Severity: low. Status: open, filed 2026-08-27 out of T25, same reason as T28. Effort: M, ~~and
it needs an ADR 0015 re-decision before it needs code.~~** *(corrected 2026-09-27 at 84a39c2:
it needs a product decision, Decision 1 below, and a new ADR before it needs code. ADR 0015 D2
is scoped to what validates a `Valkey` object
([`0015:56-57`](../adr/0015-one-crd-validated-by-schema-only.md#L56-L57)), so ADR 0015 gets a
clarifying Status note - the explicit amendment
[ADR 0031 `:192-195`](../adr/0031-a-record-the-operator-trusts-lives-in-pod-spec.md#L192-L195)
asks for - and not a re-decision of its rule.)*

~~The only in-Kubernetes control that reaches the three fields nothing else can:~~
*(corrected 2026-09-27 at 84a39c2: the only chart-shippable in-cluster control that reaches the
fields nothing in RBAC can; the in-tree admission plugin `OwnerReferencesPermissionEnforcement`
also reaches `ownerReferences`, but it is an API-server flag, see Fact)*
`metadata.ownerReferences`, `metadata.finalizers` and `spec.containers[*].image` — all three
writable by anything holding `pods: patch`, ~~all three enumerated in `SECURITY_ARCHITECTURE.md`
section 3 (since 2026-09-27
[`docs/security/isolation-and-tenancy.md`, "What does not hold"](../security/isolation-and-tenancy.md#what-does-not-hold)),~~
*(corrected 2026-09-27: `SECURITY_ARCHITECTURE.md` is gone; all three are rows of the table in
[`isolation-and-tenancy.md`, "What does not hold"](../security/isolation-and-tenancy.md#what-does-not-hold),
`:94`, `:95` and `:96`)*
and none of them expressible as an RBAC restriction, because `resourceNames` is the only
object-level narrowing Kubernetes offers and it is already in use. *(Added 2026-09-27 at
84a39c2: the three are not the whole list. The same grant also writes
`spec.initContainers[*].image`, `spec.activeDeadlineSeconds` and toleration additions, which
the table omits; see Fact.)*

**The blocker is a decision, not the code.** [ADR 0015](../adr/0015-one-crd-validated-by-schema-only.md)
D2 refuses admission **webhooks**, and ~~its stated reason~~ *(corrected 2026-09-27: D2 itself
states no reason; the reason is ADR 0015's Context,
`0015:31-44`, and its rejected
alternative "A validating webhook enforcing cross-field rules and a registry allowlist",
`0015:191-195`)* is a measured outage of a third-party
webhook backend. A `ValidatingAdmissionPolicy` has no backend to lose, so D2's reasoning does
not transfer — but it must be **amended explicitly** rather than silently stretched, because
"we refuse admission control" is how the sentence currently reads to anyone who has not read
the reason. *(Added 2026-09-27: D2's own text,
`0015:56-65`, is scoped to
what validates a `Valkey` object, so a policy on pods is outside its letter; the heading "No
admission webhook" and the ADR title are what a reader takes as the rule.)* *(Added 2026-09-27
at 84a39c2: the failure mode is different, not absent. `failurePolicy` defaults to `Fail`, and
a CEL evaluation error or a match condition that errors rejects the request, so the policy must
be scoped and its match conditions must not be able to error; Work list 5.)*

VAP is GA from Kubernetes 1.30 and [`README.md`](../../README.md) declares a 1.29 floor, so it
is opt-in behind a chart value or a floor bump. ~~Default off either way, which is the
[ADR 0005](../adr/0005-upgrade-neutral-defaults-and-anti-affinity.md) rule for anything that
can reject a write an upgrade would otherwise have made.~~ *(corrected 2026-09-27 at 84a39c2:
no ADR states a rule "for anything that can reject a write". ADR 0005 D1 is scoped to CRD
features ([`0005:124-127`](../adr/0005-upgrade-neutral-defaults-and-anti-affinity.md#L124-L127)).
The chart-value precedent is
[ADR 0021 D7](../adr/0021-per-resource-metrics-and-the-alert-that-was-missing.md#L121-L127),
and it binds only the values that ADR added ("Every chart value added **here** defaults to
`false`"); it applies ADR 0005 D1's purpose, `helm upgrade` must not create an object the
administrator did not ask for. The ADR that admits this policy therefore has to state its own
default-off decision and cite ADR 0021 D7 as precedent.)*

## Fact (enriched 2026-09-27 at `4a7543e`, re-verified 2026-09-27 at `84a39c2`)

**Verified** (read at `HEAD` = `84a39c2` unless a source says otherwise):

- Nothing ships a policy today: `git grep -n -i ValidatingAdmissionPolicy -- ':!docs/tickets'`
  finds only prose, [ADR 0031](../adr/0031-a-record-the-operator-trusts-lives-in-pod-spec.md#L188-L196) `:190`
  ("Filed, not taken here", `:196`). `deploy/helm/valkey-operator/templates/` has no policy or
  binding template and `values.yaml` no such value.
- Who holds `pods: patch`: the per-cluster sidecar Role, `get` and `patch` on `pods` narrowed by
  `resourceNames` ([`rbac.go:67-77`](../../internal/builder/rbac.go#L67-L77), verbs `:74`,
  `resourceNames` `:75`; the names are `<sts>-<ordinal>` from
  [`rbac.go:97-121`](../../internal/builder/rbac.go#L97-L121)), bound to the
  ServiceAccount `<cr>-sidecar` ([`rbac.go:18-19`](../../internal/builder/rbac.go#L18-L19)); and the
  operator's ClusterRole, `pods` with `patch` cluster-wide
  ([`clusterrole.yaml:50-59`](../../deploy/helm/valkey-operator/templates/clusterrole.yaml#L50-L59)).
- What the sidecar legitimately writes, all as a single-key JSON merge patch on pod metadata
  ([`labeler.go:255-281`](../../internal/sidecar/labeler.go#L255-L281), `MergePatchType` at `:272`): the label
  `vko.gtrfc.com/instanceRole` on its own pod ([`labeler.go:154`](../../internal/sidecar/labeler.go#L154),
  and `draining` at [`drain.go:120-121`](../../internal/sidecar/drain.go#L120-L121)), and the
  annotation `vko.gtrfc.com/drain-promoted-at` on the pod it promotes
  ([`drain.go:217-218`](../../internal/sidecar/drain.go#L217-L218)). Nothing else. *(Added
  2026-09-27 at 84a39c2: every write site, by
  `grep -rn -e PatchLabel -e PatchAnnotation -e '\.Patch(' -e '\.Update(' -e '\.Create(' -e '\.Delete(' internal/sidecar cmd | grep -v _test.go`:
  those three plus the `Patch` inside `patchMetadata` and `cmd/migrate/migrate.go:86`, which is
  the pre-upgrade hook and not the sidecar. `instanceRole` is written only on the sidecar's own
  pod (`l.podName`, `d.podName`), the stamp only on a peer: `buildReplicaAddrs` skips the pod
  itself ([`drain.go:415-422`](../../internal/sidecar/drain.go#L415-L422)). The labeler writes any
  role string `INFO` reports beyond `master` and `slave` verbatim
  ([`labeler.go:203`](../../internal/sidecar/labeler.go#L203)), so a policy compares keys, not
  values. It patches only when the detected role differs from its cached one
  ([`labeler.go:148`](../../internal/sidecar/labeler.go#L148)), so a label forged onto a replica
  stays until that replica's role changes. The released v1.12.8 sidecar uses the same patch
  shape (`git show v1.12.8:internal/sidecar/labeler.go`, `:255-271`).)*
- The floor: [`README.md`](../../README.md) `:198` "Kubernetes cluster (v1.29+)";
  [`Chart.yaml`](../../deploy/helm/valkey-operator/Chart.yaml) has no `kubeVersion`; envtest is
  1.29.0 ([`Makefile:5`](../../Makefile#L5)), CI Kind 1.33.4
  ([`release.yml:23`](../../.github/workflows/release.yml#L23)).
- The chart's precedent for a default-off template whose API may be missing:
  [`servicemonitor.yaml:1-7`](../../deploy/helm/valkey-operator/templates/servicemonitor.yaml#L1-L7)
  is gated by its value alone, and a missing CRD is an install error, "which is why it is
  default-off". No template uses `.Capabilities` (`grep -rn Capabilities deploy/helm/`: no hit).
- *(Moved from Not verified 2026-09-27 at 84a39c2.)* VAP is alpha in 1.26-1.27, beta and off by
  default in 1.28-1.29, and GA (`admissionregistration.k8s.io/v1`, on) from 1.30:
  [feature-gate page](https://github.com/kubernetes/website/blob/main/content/en/docs/reference/command-line-tools-reference/feature-gates/ValidatingAdmissionPolicy.md)
  lines 8-20, and `feature-state state="stable" for_k8s_version="v1.30"` at line 12 of
  [validating-admission-policy.md](https://github.com/kubernetes/website/blob/main/content/en/docs/reference/access-authn-authz/validating-admission-policy.md).
  So envtest 1.29.0 cannot serve the v1 kind (read, not run).
- *(Added 2026-09-27 at 84a39c2.)* The pod fields an update may change are exactly five:
  `containers[*].image`, `initContainers[*].image`, `activeDeadlineSeconds` (set, or lowered,
  never removed), `tolerations` (additions only) and `terminationGracePeriodSeconds` (negative
  to 1 only) -
  [validation.go v1.33.4 `#L5263-L5269`](https://github.com/kubernetes/kubernetes/blob/v1.33.4/pkg/apis/core/validation/validation.go#L5263-L5269)
  and `#L5298-L5315`, the same list at
  [v1.36.0 `#L5691-L5697`](https://github.com/kubernetes/kubernetes/blob/v1.36.0/pkg/apis/core/validation/validation.go#L5691-L5697);
  v1.30.0 (`:5062-5069`) also listed `containers[*].resources` for the in-place resize alpha.
  `terminationGracePeriodSeconds` is out of reach, because the operator sets 75
  ([`statefulset.go:618`](../../internal/builder/statefulset.go#L618)). In metadata only `name`,
  `namespace`, `uid`, `creationTimestamp`, `deletionTimestamp` and
  `deletionGracePeriodSeconds` are immutable (apimachinery v0.37.1
  `pkg/api/validation/objectmeta.go:337-361`). The StatefulSet controller deletes a `Failed` pod
  and recreates it
  ([stateful_set_control.go v1.33.4 `#L378-L386`](https://github.com/kubernetes/kubernetes/blob/v1.33.4/pkg/controller/statefulset/stateful_set_control.go#L378-L386)),
  so `activeDeadlineSeconds` is a pod kill that the Role's missing `delete` verb does not stop,
  one-shot per patch because the recreated pod carries no deadline.
- *(Added 2026-09-27 at 84a39c2.)* A swapped image does not wait for the operator. By reading
  the kubelet, a changed image on a regular container restarts that container on the new image
  at once
  ([kuberuntime_manager.go v1.33.4 `#L1071`](https://github.com/kubernetes/kubernetes/blob/v1.33.4/pkg/kubelet/kuberuntime/kuberuntime_manager.go#L1071)),
  while init containers run only on the sandbox-creation path (same file, about `:916-970`;
  upstream [init-containers.md](https://github.com/kubernetes/website/blob/main/content/en/docs/concepts/workloads/pods/init-containers.md):
  altering an init container's image "does not restart the Pod"). No generated init container is
  a native sidecar (`grep -n RestartPolicy internal/builder/statefulset.go`: no hit), so a
  swapped init image is latent until the sandbox is recreated, and an `activeDeadlineSeconds`
  kill does not trigger it because the StatefulSet recreates the pod from the template.
- *(Added 2026-09-27 at 84a39c2.)* The operator replaces, after the fact, a pod whose `valkey` or
  `sidecar` image was swapped: the data-tier dispatch Gets every ordinal and asks `podOutdated`
  ([`rolling_update.go:285-288`](../../internal/controller/rolling_update.go#L285-L288),
  [`:445-449`](../../internal/controller/rolling_update.go#L445-L449)), which reaches
  `podImageChanged`
  ([`rolling_update.go:485-500`](../../internal/controller/rolling_update.go#L485-L500)), and
  the ordinary failover-aware roll replaces the pod - except where
  [ADR 0007](../adr/0007-failover-aware-rolling-update.md) D6 defers a sidecar-only change on a
  single-replica cluster without Sentinel - with the cost of any roll, the dataset of a single
  non-persistent data pod included. A deleted `pod-spec-hash` annotation
  makes `podSpecHashChanged` fall back to comparing resources only
  ([`rolling_update.go:515-524`](../../internal/controller/rolling_update.go#L515-L524)); a
  deleted `config-hash` makes `podAnnotationHashChanged` report no change (`:504-510`).
- *(Added 2026-09-27 at 84a39c2.)* The sidecar container already holds the cluster password
  (`VALKEY_PASSWORD` from the auth Secret,
  [`statefulset.go:946-959`](../../internal/builder/statefulset.go#L946-L959)) and, under TLS,
  the keypair mount ([`:971-978`](../../internal/builder/statefulset.go#L971-L978)), next to the
  only projected token ([`:961-969`](../../internal/builder/statefulset.go#L961-L969); the pod sets
  `automountServiceAccountToken: false` at [`:610`](../../internal/builder/statefulset.go#L610)).
  Measured 2026-09-27 (Verification, measurement 1): on both pinned images `MODULE LOAD`,
  `DEBUG` and `CONFIG SET dir` are closed; `EVAL` runs. The generated config sets none of these
  options (`grep -rn -i -e enable-module -e enable-debug -e enable-protected -e rename-command internal cmd`:
  no hit outside tests).
- *(Added 2026-09-27 at 84a39c2.)* The sidecar's token is a kubelet-projected
  `serviceAccountToken` ([`statefulset.go:711-716`](../../internal/builder/statefulset.go#L711-L716)),
  which the kubelet binds to the pod
  ([projected.go v1.33.4 `#L336-L341`](https://github.com/kubernetes/kubernetes/blob/v1.33.4/pkg/volume/projected/projected.go#L336-L341)).
  The authenticator then puts `authentication.kubernetes.io/pod-name` and `pod-uid` into
  `request.userInfo.extra`
  ([serviceaccount/util.go v1.33.4 `#L139-L145`](https://github.com/kubernetes/kubernetes/blob/v1.33.4/staging/src/k8s.io/apiserver/pkg/authentication/serviceaccount/util.go#L139-L145),
  [v1.29.0 `#L146-L147`](https://github.com/kubernetes/kubernetes/blob/v1.29.0/staging/src/k8s.io/apiserver/pkg/authentication/serviceaccount/util.go#L146-L147)).
  A VAP can therefore tell which data pod a sidecar request comes from; RBAC cannot.
  Impersonation (`--as`) carries no such extra. Data pods run as `<cr>-sidecar`
  ([`statefulset.go:602`](../../internal/builder/statefulset.go#L602)), and
  `spec.serviceAccountName` is not update-mutable, so it is an anchor a token holder cannot
  rewrite.
- *(Added 2026-09-27 at 84a39c2.)* `OwnerReferencesPermissionEnforcement`, an in-tree admission
  plugin not in the default set, lets only a principal with `delete` on an object change its
  `ownerReferences`
  ([admission-controllers.md](https://github.com/kubernetes/website/blob/main/content/en/docs/reference/access-authn-authz/admission-controllers.md)
  lines 648-656; the default list at lines 128-132 does not contain it). The sidecar Role has no
  `delete`, so where an API server enables the plugin the `ownerReferences` lever is already
  closed. It is an API-server flag; the chart cannot ship it.
- *(Added 2026-09-27 at 84a39c2.)* VAP semantics
  ([validating-admission-policy.md](https://github.com/kubernetes/website/blob/main/content/en/docs/reference/access-authn-authz/validating-admission-policy.md)
  lines 315-321, 333-347, 391-411; k8s.io/api v0.37.1 `admissionregistration/v1/types.go:654-661`):
  `failurePolicy` defaults to `Fail` and governs misconfiguration and CEL evaluation errors; a
  match condition that is false skips the policy, one that errors under `Fail` rejects the
  request; `objectSelector` matches if either the old or the new object matches. The CEL
  variables are `object`, `oldObject`, `request`, `params`, `namespaceObject`, `authorizer` and
  `variables` - there is no bare `namespace`.
- *(Added 2026-09-27 at 84a39c2.)* Upstream controllers patch these pods: the
  statefulset-controller adopts and releases pods by patching `ownerReferences`
  ([controller_ref_manager.go v1.33.4 `#L222-L245`](https://github.com/kubernetes/kubernetes/blob/v1.33.4/pkg/controller/controller_ref_manager.go#L222-L245)),
  and the garbage collector patches them when it orphans dependents
  ([garbagecollector.go v1.33.4 `#L668`](https://github.com/kubernetes/kubernetes/blob/v1.33.4/pkg/controller/garbagecollector/garbagecollector.go#L668)).
  It removes a pod's `foregroundDeletion` finalizer only after a foreground delete of that pod
  (`#L652-L653`). Read in source, not measured.
- *(Added 2026-09-27 at 84a39c2.)* A forged `master` label on a replica is not a silent
  diversion. Replicas run `replica-read-only yes`
  ([`configmap.go:177`](../../internal/builder/configmap.go#L177)), so writes the `-rw` Service
  routes to that pod fail with `READONLY`. The steady-state check works on the labeled set and
  adopts only a pod that confirms `role:master`
  ([`steady_state_master.go:103-130`](../../internal/controller/steady_state_master.go#L103-L130)).
  `MultipleMasters` counts pods that *answered* master
  ([`split_brain_report.go:57-74`](../../internal/controller/split_brain_report.go#L57-L74)), not
  labels, so a label forgery alone does not raise it.
- *(Added 2026-09-27 at 84a39c2.)* Two tracked statements are false by reading.
  [`isolation-and-tenancy.md:83`](../security/isolation-and-tenancy.md#what-does-not-hold)
  calls its table (`:86-96`) "enumerated rather than sampled", but it has no row for
  `spec.activeDeadlineSeconds`, `spec.initContainers[*].image` or toleration additions, and row
  `:96` itself says the image field is "one of the five entries"; `:105` "Nothing narrower is
  expressible" holds for RBAC only; row `:88` "diverts client writes" omits that those writes
  fail `READONLY`. [ADR 0031 `:192-193`](../adr/0031-a-record-the-operator-trusts-lives-in-pod-spec.md#L188-L196)
  says of ADR 0015 D2 "its stated reason is a measured outage", but D2 states none
  ([`0015:56-65`](../adr/0015-one-crd-validated-by-schema-only.md#L56-L65)). Two more passages
  of ADR 0031 are incomplete rather than false: `:190-191` calls the VAP "The only
  in-Kubernetes control that also reaches" `ownerReferences`, `finalizers` and the image, which
  holds for the three together but overstates for `ownerReferences`, which the plugin above
  also reaches; its residual risk `:209-213` lists the levers without the three spec fields.

**Not verified:**

- ~~That VAP is GA (`admissionregistration.k8s.io/v1`) from 1.30 and beta, off by default, before
  it: an upstream fact, not checked here.~~ *(corrected 2026-09-27 at 84a39c2: verified from
  the feature-gate page, moved to Verified.)*
- That the CEL of Decision 2 fits the VAP cost budget; nothing was written. An allow-list that
  compares whole maps or `object.spec == oldObject.spec` is the shape the static cost estimator
  can refuse at create time for unbounded pod lists, which would be an install error with the
  value on. What settles it: create the policy on a v1.30+ API server (Kind 1.33.4, as in CI)
  and read the create response and `status.typeChecking`.
- ~~That the garbage collector and the StatefulSet controller write `ownerReferences` and
  `finalizers` on these pods (the reason Decision 2 C loses): upstream behaviour, not measured.~~
  *(corrected 2026-09-27 at 84a39c2: the `ownerReferences` half is verified in upstream source,
  see Verified; the `finalizers` half is false except after a foreground delete of the pod
  itself.)*
- ~~That envtest 1.29 cannot serve a v1 VAP: inferred from the version, not run.~~ *(corrected
  2026-09-27 at 84a39c2: follows from the feature-gate stages, moved to Verified.)*
- That the sidecar's kubelet-projected token carries the `pod-name` and `pod-uid` extras on a
  live API server: read in upstream source (1.29.0 and 1.33.4), not measured. What settles it:
  the Kind e2e of Work list 6, which fails every drain and roll if the claim is missing.
- Which server-set pod metadata (`managedFields`, anything the update strategy rewrites) differs
  between `object` and `oldObject` on a label-only merge patch at 1.30-1.36. The e2e settles it.
- How soon after an image swap the operator's next data-tier pass runs. There is no Pod watch
  ([`valkey_controller.go:2986-3001`](../../internal/controller/valkey_controller.go#L2986-L3001))
  and the healthy path returns no requeue (`:393-396`); the trigger is inferred to be the owned
  StatefulSet's status change after the container restart (`Owns` at `:2988`). Not measured.
- The kubelet side of `activeDeadlineSeconds` (the pod goes `Failed` with `DeadlineExceeded`),
  and whether the drain `preStop` hook runs then: documented upstream, not measured.
- The Lua sandbox of `valkey-server` was not assessed; the measurement covers only `MODULE LOAD`,
  `DEBUG` and protected configs.
- Whether the production API servers enable `OwnerReferencesPermissionEnforcement`, and which
  Kubernetes versions the production fleet runs: not recorded anywhere in the repository (grep
  over `docs/adr` and `archive/039` finds only the Kind and envtest versions). This is the
  question for Hans that decides Decision 1.
- That a fleet still running the v1.12.8 sidecar passes the policy: same patch shape by
  reading, not run.
- What the operator reports on the CR for a forged `master` label on a replica. The labeled set
  then holds two pods, and the steady-state decision table
  ([`steady_state_master.go:116-130`](../../internal/controller/steady_state_master.go#L116-L130))
  either demotes toward the recorded master or refuses with a Warning Event; which branch fires,
  whether it repeats every pass while the label stays, and whether any condition carries it was
  not traced. `MultipleMasters` does not (Verified above).

## Impact

Principal: ~~a compromised **sidecar container** of cluster X, the only container holding the
`<cr>-sidecar` token~~ *(corrected 2026-09-27 at 84a39c2: whoever authenticates as the
ServiceAccount `<cr>-sidecar`; the policy keys on that identity, not on a container. Among the
containers the operator generates, only the sidecar container holds its token)*
([ADR 0012](../adr/0012-the-sidecar-records-its-drain-promotion-on-the-pod.md)
D8 step 4). Verb: `patch`. Object: the data pods of cluster X only (`resourceNames`). ~~Dormant
today: it needs a compromised sidecar first.~~ *(corrected 2026-09-27 at 84a39c2: for a
compromised sidecar container the principal is dormant, it needs that compromise first.)* The
operator's own cluster-wide `pods: patch` is out of scope; the operator is trusted.

*(Added 2026-09-27 at 84a39c2.)* What the pod-level levers add to what the sidecar container
already holds (the password with full rights, the TLS keypair): control-plane effects and code
execution in other containers, which the password does not give (measured: no `MODULE LOAD`,
`DEBUG` or `CONFIG SET dir` route; the Lua sandbox was not assessed, see Not verified). Per lever: a `valkey` or `sidecar` image swap runs at once
and lasts until the operator's next data-tier pass replaces the pod, except where ADR 0007 D6
defers a sidecar-only change; a finalizer keeps a pod
from ever being deleted, reported only as `PodTerminationStalled` after 2 min
([`rolling_update.go:120`](../../internal/controller/rolling_update.go#L120), ADR 0026); deleting
`config-hash` or `pod-spec-hash` switches the corresponding roll off; `activeDeadlineSeconds`
kills the pod, and the StatefulSet recreates it; `ownerReferences` detach or reattach the pod;
an `instanceRole` of `master` on any pod of the cluster routes a share of `-rw` writes to a pod
that refuses them. The last one is also reachable over the data plane: the password holder can
send `REPLICAOF NO ONE` to a peer, whose own honest labeler then labels it `master`.

## Options

Two decisions, in this order: 1 gates 2. *(Rewritten 2026-09-27 at 84a39c2; the former
Decision 3, the gating on a missing API, is not a real decision and is now a rule in Work
list 5. Removed options and the reasons are in History.)*

### Decision 1 — may the chart ship a ValidatingAdmissionPolicy at all?

**Mechanism.** Today each cluster's sidecar Role grants `get` and `patch` on that cluster's
data pods, narrowed only by `resourceNames`
([`rbac.go:67-77`](../../internal/builder/rbac.go#L67-L77)); RBAC has no field-level rules, so
a holder of the token can rewrite every label, annotation, `ownerReferences`, `finalizers` and
the four reachable update-mutable spec fields (Fact). A swapped image runs before any operator
pass can react (Fact). [ADR 0015](../adr/0015-one-crd-validated-by-schema-only.md)
D2 refuses webhooks that validate `Valkey` objects, and
[ADR 0031 `:188-196`](../adr/0031-a-record-the-operator-trusts-lives-in-pod-spec.md#L188-L196)
records the VAP as "Filed, not taken here". The choice changes only whether the chart carries a
cluster-scoped policy and binding behind a value. It changes no RBAC, no pod template (nothing
rolls, with the value on or off, because neither object is an input of `ComputePodSpecHash` or
the config hash), not the operator's cluster-wide grant and not the data-plane rights the
sidecar holds. Removing the sidecar's `pods: patch` altogether is not an option here: the
operator would have to write `instanceRole` and the drain stamp itself, which reopens
[ADR 0012](../adr/0012-the-sidecar-records-its-drain-promotion-on-the-pod.md) (the sidecar exists
because the drain promotion and the relabel after a failover cannot wait for a reconcile pass,
and the operator has no Pod watch), at effort L for a low hardening item; whichever ADR records
this decision names it as a rejected alternative.

- **A. A new ADR admits a chart-shipped, default-off VAP (`admissionregistration.k8s.io/v1`); ADR
  0015 gets a Status note that D2 does not reach an in-process policy on pods; ADR 0031 `:196`
  records it as taken. (recommended)** Cost M: the ADR (its own default-off decision, ADR 0021
  D7 as precedent, the rejected alternatives, and the condition under which the default would
  flip), the ADR 0015 note, ADR 0031 `:188-196` and `:209-213`, the
  [index](../adr/README.md) row, a policy and binding template pair, a value, the README
  [Helm chart values](../../README.md#helm-chart-values) row, the rows at
  `isolation-and-tenancy.md:88-96`, the value on in `test/e2e/helm-values.yaml`, and a negative
  Kind e2e. Consequences: nothing rolls even when enabled, and existing installs are unchanged;
  only installs that enable it on 1.30 or later are protected; every future sidecar write needs
  a policy change, and the e2e fails while it is missing (with the value on in the e2e values,
  every drain and roll e2e is a positive control); a CEL defect fails closed on the sidecar's
  `instanceRole` write and breaks `-rw` routing where the value is on - scoping (Work list 5)
  and the e2e keep that loud and before a release.
- **C. Refuse the VAP and accept the gap; an ADR records the refusal with its reason, and the
  security page keeps the rows as accepted residual risk.** Cost S: the ADR (a refusal to act gets
  its own ADR under the project rule, or an amendment of ADR 0031 where the item was filed),
  ADR 0031 `:196` rewritten from "Filed, not taken here" to the refusal, and the security-page
  wording - not "only the close". Consequences: every install keeps, for a holder of the token,
  image swaps that run at once, finalizer pinning,
  hash deletion that switches rolls off, the `activeDeadlineSeconds` kill and `ownerReferences`
  edits. The case for C: severity low, the value protects only where someone opts in, the
  principal already holds the password, and the chart's CEL is coupled to every future sidecar
  write for good.

**Why A:** it is the only chart-shippable control that stops the pod-level levers before they
take effect, and the password gives none of them (measured: no `MODULE LOAD`, `DEBUG` or
`CONFIG SET dir` route on either pinned image); RBAC cannot express them
([`rbac.go:75`](../../internal/builder/rbac.go#L75) already uses `resourceNames`), and the only
in-tree alternative, `OwnerReferencesPermissionEnforcement`, is an API-server flag covering one
field. It runs in the API server, so the webhook outage behind ADR 0015's Context cannot recur
through it; it is in no pod template, so it never rolls; and it is off by default, so an upgrade
changes nothing. It beats C because C closes nothing while A costs a non-opting install nothing;
C's coupling cost is guarded by the e2e once the value is on in `test/e2e/helm-values.yaml`.
**Checkable precondition:** A pays off only where it is enabled. If the production fleet runs
below Kubernetes 1.30, or Hans would not enable the value there, C wins. The fleet's versions are
not recorded in the repository; that is the one question to put to Hans with this decision.

### Decision 2 — whom the policy binds and what it refuses (after 1 = A)

**Mechanism.** The sidecar makes three writes, each a single-key merge patch
([`labeler.go:255-281`](../../internal/sidecar/labeler.go#L255-L281)): `instanceRole` on its own
pod ([`labeler.go:154`](../../internal/sidecar/labeler.go#L154),
[`drain.go:120-121`](../../internal/sidecar/drain.go#L120-L121)) and `drain-promoted-at` on a peer
([`drain.go:217-218`](../../internal/sidecar/drain.go#L217-L218), peer list without itself at
[`drain.go:415-422`](../../internal/sidecar/drain.go#L415-L422)). Every request carries the username
`system:serviceaccount:<ns>:<cr>-sidecar` and, from its pod-bound token, the `pod-name` and
`pod-uid` extras (Fact). The statefulset-controller, the garbage collector and administrators
patch the same pods. The choice decides which requests the policy refuses. It does not touch the
operator's ServiceAccount, the kubelet's `pods/status` writes (a subresource the rules do not
name), any Valkey behaviour, or the data-plane route to a relabel (`REPLICAOF NO ONE` on a peer,
then its honest labeler). Common to both options: the principal is anchored on the stored pod,
`request.userInfo.username == 'system:serviceaccount:' + request.namespace + ':' +
oldObject.spec.serviceAccountName` (the former CEL used a `namespace` variable that does not
exist, and the `vko.gtrfc.com/cluster` label, which is a sound anchor under the allow-list but
needs the policy to protect it), and the policy compares named surfaces - labels without
`instanceRole`, annotations without `drain-promoted-at`, `ownerReferences`, `finalizers`, `spec`
- rather than the whole object.

- **A. An allow-list: that principal may change only the `instanceRole` label and the
  `drain-promoted-at` annotation, on any pod of its cluster.** Cost M, within Decision 1 A. It
  covers every row of the table, and the three spec fields the table omits. Consequence: a token
  holder can still label any pod of its cluster `master` or stamp any pod, although no
  legitimate path does either.
- **A-prime. A, plus pod binding for `instanceRole`: that key may change only when
  `request.userInfo.extra['authentication.kubernetes.io/pod-uid'][0] == oldObject.metadata.uid`
  (and the `pod-name` extra equals `oldObject.metadata.name`); a request without the claim may not
  change it. Keys are compared, not values
  ([`labeler.go:203`](../../internal/sidecar/labeler.go#L203)). (recommended)** Cost: the same
  template and e2e as A, plus two negative cases that need a pod-bound token, since
  impersonation carries no claim. Consequences: the claim is read in upstream source, not
  measured on a cluster; if it were missing, every label write would be refused and every drain
  and roll e2e would fail - loud, and before a release. A future sidecar write of `instanceRole`
  on another pod needs a policy change.

**Why A-prime:** it ties an `instanceRole` write to the identity of the pod that issued it - a
token bound to pod X can relabel only pod X - a property A and every RBAC variant lack, for two
CEL comparisons against a claim the API server sets across the supported range (apiserver
v1.29.0 `util.go:146-147`, v1.33.4 `:139-145`), and it matches the code, which writes the label
only on its own pod. It anchors on the immutable `spec.serviceAccountName` and on the pod UID,
not on a label. It does not close the `-rw` diversion row for a holder of the password, who can
promote a peer over the data plane; it is defence in depth, not a closure. A is the runner-up and
an acceptable equal: it loses only the per-pod identity, and costs the same.

## Decision

Not yet decided.

## Work list

~~Nothing here is XS without a decision. The no-decision corrections were stale sentences in this
file, and they are done above.~~ *(corrected 2026-09-27 at 84a39c2: items 1 and 2 need no
decision; they correct tracked statements outside this directory that are false by reading.)*

1. *(no decision)* [`isolation-and-tenancy.md`, "What does not hold"](../security/isolation-and-tenancy.md#what-does-not-hold):
   add rows for `spec.activeDeadlineSeconds` (a pod kill; the StatefulSet recreates the pod),
   `spec.initContainers[*].image` (latent until the sandbox is recreated) and toleration
   additions; in the image row `:96` say that a swapped container image runs at once;
   in row `:88` say that writes routed to a mislabeled replica fail `READONLY`; change `:105` to
   "nothing narrower is expressible in RBAC"; that makes "enumerated rather than sampled" at `:83`
   true again. Mention `OwnerReferencesPermissionEnforcement` in the `ownerReferences` row.
2. *(no decision)* [ADR 0031](../adr/0031-a-record-the-operator-trusts-lives-in-pod-spec.md#L188-L196):
   correct `:192-193` (D2 states no reason; the reason is ADR 0015's Context and rejected
   alternative) and sharpen `:190-191` ("the only in-Kubernetes control" - the only chart-shippable one;
   the admission plugin also reaches `ownerReferences`), and add the three spec fields to the
   residual risk `:209-213`.
2a. *(no decision; added 2026-09-27, consistency pass, read at `84a39c2`)*
   [`privilege-footprint.md:97`](../security/privilege-footprint.md) says "Two writes reach the
   operator's decisions through this grant" and lists `instanceRole` and the drain stamp. The
   public table at `isolation-and-tenancy.md:86-98` shows more rows that reach operator decisions
   through the same `pods: patch` - at least `vko.gtrfc.com/config-hash` and
   `vko.gtrfc.com/pod-spec-hash`, which suppress a roll. Make the sentence point at that table
   instead of counting. XS, true under every option.
3. *(waits on Decision 1, any outcome)* ADR 0031 `:196` "Filed, not taken here" states the
   decision. Under A: a new ADR (its own default-off decision with ADR 0021 D7 as precedent; the
   rejected alternatives - a documented recipe, on by default with a 1.30 floor, removing the
   sidecar's `pods: patch`, a `.Capabilities` gate; and the condition for flipping the default,
   for example a release run on the production fleet, the Chaos Mesh namespace included, with no
   refused legitimate sidecar write), a Status note on ADR 0015, and the row in
   [`docs/adr/README.md`](../adr/README.md). Under C: the ADR that records the refusal and its row.
4. *(waits on Decisions 1 and 2)* The policy and binding templates under
   `deploy/helm/valkey-operator/templates/`, a default-off value in `values.yaml`, the row in the
   README Helm values reference, and the rows at `isolation-and-tenancy.md:94-96` naming the
   policy as the opt-in control.
5. *(no decision within 1 = A; rules for item 4)* Gate on the value alone, as
   `servicemonitor.yaml:1-7` does: enabling it where the v1 kind is missing is an install error
   that names the kind. `failurePolicy: Fail` (consistent with the fail-closed stance ADR 0015 D7 takes for third-party webhooks); binding
   `validationActions: [Deny]`; `matchConstraints` on `pods` `UPDATE` only (`pods/status` not
   named); `objectSelector` on `app.kubernetes.io/managed-by: vko.gtrfc.com` and
   `app.kubernetes.io/component: valkey`; `matchConditions` that cannot error (guard every map
   access with `in`), so a CEL error can refuse only the sidecar's own writes; the allow-list
   ignores `managedFields` and compares named surfaces (Decision 2).
6. *(waits on 1 = A)* The value on in `test/e2e/helm-values.yaml`, so every drain and roll e2e
   runs under the policy; a negative Kind e2e with a pod-bound token (for example
   `kubectl create token <cr>-sidecar --bound-object-kind Pod --bound-object-name <cr>-0`, not
   yet tried); `helm template` with the value off and on. Kind, because envtest 1.29 cannot serve
   the v1 kind and the positive half needs a running sidecar (ADR 0017). ~~T58's chart render gate
   would also cover the enabled render path.~~ *(corrected 2026-09-27 at 84a39c2: T58, state
   `filed`, has no policy row in its matrix, and a render never checks the CEL; if this lands
   first, T58 adds that row, which under item 5 renders without `--api-versions`.)* *(Precised
   2026-09-27, consistency pass: T58 is now `analysed` and its Related tickets name this row,
   rendered without `--api-versions`; its matrix still lists no policy row until one of the two
   lands.)*
7. Close ([ADR 0034](../adr/0034-tickets-are-work-lists-that-get-archived.md)): extraction into
   the ADR and the security page, `git grep -nwE 'T29|029'` outside `docs/tickets/`, then
   `archive/`.

## Verification

- Kind, policy enabled, ~~acting as `<cr>-sidecar`~~ *(corrected 2026-09-27 at 84a39c2: with a
  token bound to a data pod, not `--as` impersonation, which carries no `pod-name` extra and would
  be refused for the wrong reason under Decision 2 A-prime)*: a patch of `ownerReferences`,
  `finalizers`, a container or init-container image, `activeDeadlineSeconds`, a toleration or
  `config-hash` is refused with the policy's message; under A-prime so is an `instanceRole`
  patch on another pod. The labeler patch, the drain patch and every operator patch still pass,
  and so do the statefulset-controller's and an administrator's. The policy's create response
  and `status.typeChecking` show no cost or type error.
- `helm template` renders no policy with the value off, and the policy plus its binding with it
  on.
- Measurements taken for this ticket:
  1. 2026-09-27, `for img in valkey/valkey:9.1.1 valkey/valkey:8.1.9; do docker run -d --rm --name vko-verify-t029-ed-<n> $img; docker exec ... valkey-cli CONFIG GET enable-module-command | enable-debug-command | enable-protected-configs; CONFIG SET enable-module-command yes; CONFIG SET dir /tmp; EVAL "return 1" 0; docker rm -f ...; done`:
     on both images all three configs `no`; the first `CONFIG SET` fails "can't set immutable
     config", the second "can't set protected config"; `EVAL` returns 1. No container left
     (`docker ps -a --filter name=vko-verify-t029-ed`: 0). The same result was measured
     independently the same day by the audit and by its review.
  2. 2026-09-27, helm v3.21.3 on a scratch chart printing
     `.Capabilities.APIVersions.Has "admissionregistration.k8s.io/v1/ValidatingAdmissionPolicy"`
     and `Has "admissionregistration.k8s.io/v1"`: plain `helm template` gives false and true
     (KubeVersion v1.36.0); with `--api-versions admissionregistration.k8s.io/v1/ValidatingAdmissionPolicy`
     both true. Helm's default set holds group/versions only (helm v3.21.3
     `pkg/chartutil/capabilities.go:37-38, 115-123`). The group-level check is also true on 1.29,
     where the group serves webhook configurations. Measured by the audit and its review, not
     re-run in the editing pass.
  3. 2026-09-27, `curl -sSL https://raw.githubusercontent.com/kubernetes/kubernetes/<tag>/pkg/apis/core/validation/validation.go | grep -n -A6 'var updatablePodSpecFields'`
     for v1.30.0, v1.33.4 and v1.36.0, and the `serviceaccount/util.go` and feature-gate fetches
     cited in Fact.

## History

- 2026-09-27: re-verified at 84a39c2 against the code, the ADRs, upstream source at the pinned
  tags and two docker runs. Checked: every Fact location; the sidecar's write sites (grep over
  `internal/sidecar` and `cmd`); the ADR 0015, 0005, 0021 and 0031 passages; the security-page
  table; upstream pod-update validation, the StatefulSet recreate, kubelet image handling,
  bound-token claims, VAP semantics and `OwnerReferencesPermissionEnforcement`. Measured: Valkey
  `MODULE LOAD`/`DEBUG`/protected-config defaults on 9.1.1 and 8.1.9, and helm `.Capabilities`
  under `helm template` (Verification). Found false or outdated and corrected in place: the header's
  "ADR 0015 re-decision" (it is a product decision plus a new ADR and a Status note); "the only
  in-Kubernetes control" (the only chart-shippable one); the ADR 0005 attribution of the
  default-off rule (ADR 0021 D7 is the precedent and binds only its own values); "Rolls nothing,
  because it is default off" (it rolls nothing even when enabled, being in no pod template); C's
  "Costs only the close" (it needs an ADR); the Decision 2 A CEL's `namespace` variable (it is
  `request.namespace`); Decision 2 C's finalizer reason (only after a foreground delete); the
  Verification's `--as` impersonation; the Work list claim that every item needs a decision; the
  Work list claim that T58's gate covers the enabled path. Two Not verified items (VAP GA stages,
  envtest 1.29) moved to Verified. Locations re-read at 84a39c2: `rbac.go:69-74` is now
  `rbac.go:67-77`, drifted with `bcc63c9`; `drain.go:119-120` and `:216-217` were off by one from
  the start (`:119` is a comment line and `internal/sidecar` is unchanged since `4a7543e`), so the
  previous entry's "the `file:line` locations of Fact ... hold" was partly wrong even then; they
  are now `:120-121` and `:217-218`, and `labeler.go:255-262` is `:255-281`. New facts added: the
  three further update-mutable spec fields; the operator replacing a swapped `valkey` or
  `sidecar` image only after it ran; the kubelet's immediate
  container restart and the latent init image; the password and keypair already in the sidecar;
  the pod-bound token claims; VAP failure semantics; the forged-label effects
  (`READONLY`, `MultipleMasters` counts probed roles, not labels); the false-by-reading statements
  in ADR 0031 and `isolation-and-tenancy.md`. Two review claims were not taken: that the pod
  binding "closes" the `-rw` diversion row (the password holder can promote a peer over the data
  plane), and that a label forgery is reported as `MultipleMasters` (it is not,
  `split_brain_report.go:57-74`). Options removed:
  - Decision 1 B, a documented recipe only: untested, drifts from `rbac.go:18-19` and the two keys
    without anything going red; a VAP recipe is A without the test, and a recipe in a
    webhook-based policy engine puts the labeler's write behind the backend class whose outage is
    ADR 0015's Context.
  - Decision 1 D, a 1.30 floor with the policy on by default: the floor breaks every 1.29 install,
    an unmeasured fail-closed CEL would sit on every cluster's load-bearing `instanceRole` write
    after a plain `helm upgrade`, and it contradicts the ADR 0021 D7 precedent. Its earlier reason
    ("what ADR 0005 keeps off") was wrong, and "not a defect of the operator's own posture" was not
    taken either, because the narrowing of the sidecar's token (ADR 0012 D8 step 4) shipped
    fleet-wide with no toggle. The default-on idea is kept as the flip condition in A's ADR.
  - Decision 2 B, a deny-list of the three filed fields: leaves both hash annotations,
    `activeDeadlineSeconds`, init images and tolerations open and saves only a few CEL lines.
  - Decision 2 C, every principal except the operator: refuses the statefulset-controller's
    adoption and release patches, the garbage collector's orphaning patch and every
    administrator's `kubectl label`/`annotate`.
  - Decision 3 as a decision: A (gate on the value alone) is settled by the
    `servicemonitor.yaml:1-7` precedent and became a rule in Work list 5; B (`.Capabilities`)
    renders a security control silently to nothing, and the only discriminating check is false
    under plain `helm template` (measured); C (raise the floor) duplicated Decision 1 D; a new
    variant, value-gated plus `fail` when the kind is missing, was considered and not listed,
    because it breaks every offline render with the value on (measured `Has` false).
  - Considered and not listed: removing the sidecar's `pods: patch` (reopens ADR 0012, effort L);
    Decision 2's rule that the drain stamp may land only on another pod (cosmetic: a stamp is
    honoured only on a probe-confirmed master, and the password holder can promote a peer).
  Recommendations: Decision 1 stays A, with its justification rewritten (the former
  justification was false: `valkey` and `sidecar` swaps also execute at once) and a precondition
  named (the production fleet's Kubernetes versions, a question for Hans). Decision 2 changes
  from A to the
  new A-prime (pod binding for `instanceRole` on the pod UID), because it adds per-pod identity for
  two comparisons; A is the runner-up. Frontmatter: `state` filed -> analysed (facts and options
  complete, open checks named); `threat` was "would additionally cover metadata.ownerReferences,
  metadata.finalizers and spec.containers[*].image, writable by anything holding pods: patch and
  not expressible as an RBAC restriction", now names the token holder, the three further spec
  fields and the pod binding; `urgency` icebox -> now under rule 1 by reading, per the
  018/044/045/062 precedent, while ADR 0031 `:192-193` and `isolation-and-tenancy.md:83` stand
  false and `:88`, `:96` and `:105` incomplete (Work list 1 and 2, appended here as the family
  work items), back to
  icebox under rule 5 once corrected; `blocked-by` adr-0015 -> decision. `severity` low, `security`
  hardening and `effort` M unchanged. Review of this pass, same day: `labeler.go:255-277` widened
  to `:255-281` (the function ends there); the ADR 0031 "only in-Kubernetes control" passage
  re-read at `:190-191`, not `:189-190`, and classed as incomplete rather than false, as are
  `isolation-and-tenancy.md:88`, `:96` and `:105`; the util.go anchor aligned with its cited
  lines; the Lua caveat added to Impact; how a forged `master` label surfaces on the CR added to
  Not verified.
  - Cross-ticket: in the consistency pass of the same day, the T58 note of Work list item 6 was
    precised (T58 is `analysed` and its Related tickets name the policy row, rendered without
    `--api-versions`), and item 2a was added: `privilege-footprint.md:97` counts two writes that
    reach operator decisions while the public table at `isolation-and-tenancy.md:86-98` shows at
    least `config-hash` and `pod-spec-hash` as well (read at `84a39c2`); T56's statement of this
    ticket's blocker was corrected on its side.
  - Sweep: passages of Fact, Impact, Decision 1 (its mechanism and option C), Work list item 1
    and this entry were shortened before commit. The claim that the next data-tier pass replaces
    a pod with a swapped `valkey` or `sidecar` image is now bounded by the ADR 0007 D6 deferral
    of a sidecar-only change on a single-replica cluster without Sentinel, which it had omitted
    (read at `84a39c2`). No
    frontmatter field, option or recommendation rested on the removed clauses, so none changed.
- 2026-09-27: adversarial review of the enrichment below. One clause the enrichment added to
  Options was removed before commit under the
  [embargo rule](README.md#an-open-security-finding-is-embargoed); its wording is not repeated
  here. Spot-checked at `4a7543e`: the `file:line` locations of Fact, the ADR 0015 lines and
  `isolation-and-tenancy.md:94-96` hold. Recommendations, urgency and effort unchanged.
- 2026-09-27: enriched - Fact split into Verified / Not verified with `file:line` locations,
  Impact, Options (three ordered decisions, each with a recommendation), Decision (not yet
  decided), work list, Verification. Two stale sentences corrected in place: the
  `SECURITY_ARCHITECTURE.md` pointer, and where ADR 0015 states its reason. Frontmatter
  unchanged: urgency re-derived as `icebox` (rule 5: it needs a re-decision of ADR 0015 that
  nobody has accepted), effort M, blocked by `adr-0015`.
- 2026-09-27 - `SECURITY_ARCHITECTURE.md` was split into `docs/security/` by the documentation
  restructure; the section 3 pointer above now also names its new place. No finding changed.
- 2026-09-27 - extracted verbatim from the collection ticket (now [archive/039-findings-from-the-1-11-0-fleet-rollout.md](archive/039-findings-from-the-1-11-0-fleet-rollout.md)) into its own file when the tickets were numbered. Frontmatter filled from the final board row (board archive of that file, groomed 2026-09-26) and from the section text. One sentence of the section was left out under the embargo rule of [README.md](README.md#an-open-security-finding-is-embargoed); its wording is kept in an embargoed ticket file and returns when that embargo ends (corrected 2026-09-27: this entry first recorded the omission in other words, which are kept in the same file).
