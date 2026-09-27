---
id: T56
title: the operator has no namespace-scoped mode
state: analysed       # facts verified, options weighed
severity: medium      # the operator token is cluster-admin equivalent, with every Secret of the cluster in its cache
security: hardening
threat: "A compromise of the operator's ServiceAccount token, image or process may today read and delete every Secret, patch the image of any running pod, create workloads under any ServiceAccount and write itself any namespaced permission in every namespace, kube-system included; an opt-in namespace-scoped mode would confine that to the listed namespaces and what their ServiceAccounts hold."
urgency: icebox       # rule 5: hardening that needs a compromise, no decided fix, reopens ADR 0013 D1
effort: M             # proof in the integration tier, the unchanged ClusterRole bound per namespace; L with a full e2e leg in the mode
blocked-by: decision  # Q1
filed-from: T31, section "Further security measures - not in this change, each open" (archive/031)
opened: 2026-09-27
decided:
done:
---

# T56 - the operator has no namespace-scoped mode

## Current state

**RBAC.** The chart binds one ClusterRole
([`clusterrole.yaml:7-188`](../../deploy/helm/valkey-operator/templates/clusterrole.yaml)) through
one ClusterRoleBinding
([`clusterrolebinding.yaml:1-14`](../../deploy/helm/valkey-operator/templates/clusterrolebinding.yaml)).
Every rule is on a namespaced resource (no `nonResourceURLs`), so every rule could take effect per
namespace if the same ClusterRole were bound by RoleBindings. The token is mounted in the operator
pod on purpose (`automountServiceAccountToken: true`,
[`_helpers.tpl:84-87`](../../deploy/helm/valkey-operator/templates/_helpers.tpl)). Four channels
make it cluster-admin equivalent, in every namespace:

1. `secrets: delete, get, list, watch` ([`clusterrole.yaml:64-72`](../../deploy/helm/valkey-operator/templates/clusterrole.yaml)).
2. `deployments`, `statefulsets: create, update, patch` ([`clusterrole.yaml:73-85`](../../deploy/helm/valkey-operator/templates/clusterrole.yaml)): creating a workload runs code under any ServiceAccount of that namespace.
3. `pods: patch` ([`clusterrole.yaml:50-59`](../../deploy/helm/valkey-operator/templates/clusterrole.yaml)): a pod update may change container images, so the token runs code in any running pod, kube-system and flux-system included.
4. `roles: bind, create, escalate` and `rolebindings: create` ([`clusterrole.yaml:150-175`](../../deploy/helm/valkey-operator/templates/clusterrole.yaml)): it writes itself any namespaced permission (ADR 0013 D2).

`configmaps`, `services` create/update/patch ([`clusterrole.yaml:36-49`](../../deploy/helm/valkey-operator/templates/clusterrole.yaml))
add tampering in any namespace. Narrowing only the Secret rule or the Secret cache therefore takes
no Secret out of reach; only moving every rule out of the cluster-wide binding narrows the token.
RBAC has no label selector, so a label-filtered cache narrows memory, never the grant.

**Cache.** [`cmd/main.go:102-110`](../../cmd/main.go) builds the manager with no cache options, so
every informer is cluster-wide. The controller watches `Valkey` (with `GenerationChangedPredicate`),
owns nine namespaced kinds and watches every `Secret`
([`valkey_controller.go:2984-3002`](../../internal/controller/valkey_controller.go)). The Secret
watch is the rotation and password-change trigger: the healthy pass returns no requeue
([`valkey_controller.go:390-396`](../../internal/controller/valkey_controller.go)) and the default
resync is 10 h. Every Secret read names `v.Namespace`, so a cache restricted to the CR's namespace
serves every read unchanged. pprof is off, so no memory-only exposure path exists; a smaller cache
alone protects against nothing the token does not already open.

**Other facts the mode depends on.**

- Leader election is on by default ([`values.yaml:86-87`](../../deploy/helm/valkey-operator/values.yaml)); the release namespace needs `leases` and core `events: create, patch` (the lock records through the core-group recorder). `OperatorNamespace` is used only as a NetworkPolicy peer, never for a cached read.
- With `DefaultNamespaces`, an informer is synced only when every per-namespace informer is. A listed namespace whose RoleBinding is gone (namespace deleted or recreated after the last `helm upgrade`) answers 403; a running operator keeps serving the others, but after any restart the 2 min cache sync timeout expires and the manager exits: a crashloop that stops every listed namespace. This is the per-namespace form of ADR 0014 D8.
- A listed namespace must exist at install and upgrade (NamespaceLifecycle admission).
- The CRD ships unconditionally in `templates/crd.yaml`, so the mode is one release per cluster serving a list, and the installer still needs cluster-scoped rights.
- The pre-upgrade hook is a cluster-scoped ClusterRole on `valkeys` and `customresourcedefinitions` ([`pre-upgrade-rbac.yaml:29-50`](../../deploy/helm/valkey-operator/templates/pre-upgrade-rbac.yaml)), because `migrate.Run` lists every Valkey ([`migrate.go:54-55`](../../cmd/migrate/migrate.go)); it is already optional (`preUpgradeHook.enabled`, default `true`, gap H-3).
- The metrics collector lists Valkey from the manager cache ([`collector.go:161`](../../internal/metrics/collector.go)); under a namespace list an unlisted CR produces no `vko_valkey_*` series and `ValkeySpecNotObserved` cannot fire for it. The operator sets no finalizer, so such a CR still deletes and its objects are garbage-collected.
- The RBAC drift guard `TestHelmClusterRoleCoversGeneratedRole` ([`rbac_drift_test.go:160`](../../internal/controller/rbac_drift_test.go)) reads the literal rules block of `clusterrole.yaml`; a RoleBinding to the unchanged ClusterRole needs no second rules copy and no new guard.
- envtest runs kube-apiserver with the RBAC authorizer and the suite does not override it ([`suite_test.go:63-65`](../../test/integration/suite_test.go)), so the mode is testable without Kind.
- The e2e fixture `createNamespace` deletes and recreates its namespace and deletes it after a passing test ([`e2e_test.go:101-133`](../../test/e2e/e2e_test.go)); a RoleBinding the chart rendered there dies with it.

**Impact.** On every install, whoever obtains the operator's token or runs code in its process
holds cluster-admin-equivalent power. Live only after a compromise, hence hardening. Multi-tenant
clusters cannot confine the operator to their tenants' namespaces. No multi-tenant user is named;
the production fleet has one owner.

## Required changes

**Depends on Q1 (only under B):**

1. [`cmd/main.go`](../../cmd/main.go): a namespace-list flag that sets `cache.Options.DefaultNamespaces`; empty keeps the cluster-wide cache.
2. Chart: a value (default empty) passed to the flag; `clusterrole.yaml` unchanged and always rendered; the ClusterRoleBinding only while the list is empty; otherwise one RoleBinding per listed namespace to the same ClusterRole, plus a Role and RoleBinding in the release namespace for `leases` and core `events: create, patch` unless that namespace is listed (not the full ClusterRole, which would expose the release namespace Secrets, Helm release Secrets included).
3. Integration test (envtest, RBAC authorizer): with the operator ServiceAccount bound only by RoleBindings, a SubjectAccessReview in an unlisted namespace denies `secrets get`, `pods patch`, `deployments create`, `roles create`; in a listed namespace the impersonated client creates the sidecar Role and RoleBinding; a manager with `DefaultNamespaces` does not reconcile a CR in an unlisted namespace.
4. A chart render with the list empty is byte-identical in RBAC to today.
5. `docs/operations/installation.md`: listed namespaces must exist before install and upgrade; a recreated namespace needs its RoleBinding back before the next operator restart; adding a namespace is a values change; the hook stays cluster-scoped unless disabled.
6. Optional e2e leg in the mode: the fixture must stop recreating listed namespaces or create the RoleBinding itself, and ADR 0017 D31 (three legs) is amended.
7. Docs and ADRs: ADR 0013 D1 (the opt-in exception) and its alternative; ADR 0014 (binding switch, D8 crashloop in per-namespace form); ADR 0016 D2 and D5 (Secret watch no longer always cluster-wide); H-1 in [`privilege-footprint.md`](../security/privilege-footprint.md#h-1) including its "one more consumer" sentence; [`trust-boundaries.md:12`](../security/trust-boundaries.md); [`README.md:187-189`](../../README.md) and the values table.
8. On a real install: `kubectl auth can-i --list --as=system:serviceaccount:<release-ns>:<sa> -n kube-system` shows no operator rule.

**Depends on Q1 (under A):** record the rejection of "a namespaced Role per watched namespace" in ADR 0013 with its reason, restate H-1 as an accepted residual risk, drop the ticket.

**Depends on Q2:** 2A: the README values text and `installation.md` describe what an unlisted CR looks like. 2B: the status path described in Q2.

## Open questions

### Q1: Should ADR 0013 D1 be reopened with an opt-in namespace-scoped mode?

The operator is cluster-admin equivalent today (Current state). An opt-in list would confine it to
the listed namespaces; the default install, the installer's rights, the hook and every Valkey pod
stay as they are (the list reaches no pod builder, so nothing rolls).

- **A - keep D1 and record the refusal.** Cost XS. The operator stays namespaced-admin everywhere; after a compromise a kube-system or flux-system pod image patch or workload makes it cluster-admin.
- **B - opt-in namespace list, the unchanged ClusterRole bound per listed namespace (recommended).** Cost M (Required changes). It confines the token to the listed namespaces plus what their ServiceAccounts hold; in this fleet gitlab, harbor and iam would be listed and their Secrets stay in reach, but kube-system, flux-system and cert-manager leave it. Costs: a lost RoleBinding crashloops the operator for every listed namespace on its next restart; a new namespace needs a values change; switching an existing install makes the old pod see 403 until the rollout (ADR 0014 D8 shape); a CR in an unlisted namespace is not served at all (no recovery, no split-brain resolution, frozen annotations), which matters for database-examples under Chaos Mesh.

B is the only option that changes what a compromised token can do, and it costs existing installs
nothing. A wins if no cluster the owner runs would set a list. The deciding question: would the
production fleet set the list (gitlab, gpt, harbor, iam, database-examples and every other
namespace holding a Valkey CR)?

**Answer:** _open_

### Q2 (only under B): How is a Valkey CR in an unlisted namespace reported?

Under B such a CR never reaches `Reconcile`: empty status (blank Phase column), no metrics, no
alert, no recovery, clean deletion.

- **2A - report nothing, document the boundary (recommended).** Cost XS documentation. A CR in the wrong namespace is silent; only the blank Phase column shows it.
- **2B - a cluster-wide Valkey watch that writes a condition.** Cost M: a Valkey informer outside the list, a mandatory namespace gate at the `Reconcile` entry, a new `conditionRegistry` row (ADR 0027), collector filtering, and a cluster-wide `valkeys: get, list, watch` plus `valkeys/status: patch, update` ClusterRoleBinding, which lets a token holder read every Valkey spec, forge status in excluded namespaces and widens the T48 inventory back to the whole cluster.

2A: the list is the administrator's own statement of scope, and whoever sets it also chooses where
CRs live; it writes nothing outside the list. Its price must be written into the documentation.

**Answer:** _open_

## Not verified

- The restart crashloop is derived from reading controller-runtime v0.25.1; an envtest manager with `DefaultNamespaces` over two namespaces, one without its RoleBinding, and a restart settles it.
- Which ServiceAccounts in gitlab, gpt, harbor, iam and database-examples hold cluster-wide rights (bounds what B confines to); needs cluster read access.
- Whether the fleet's `cluster-ca` is a CA ClusterIssuer whose key Secret sits in the `cert-manager` namespace.
- Whether Flux helm-controller drift detection is on (would restore a lost RoleBinding), and whether Flux reports an unserved CR with no status as ready.
- The memory cost of caching every Secret of the cluster.

## Related

- T29 - a chart-shipped ValidatingAdmissionPolicy; confining the operator by VAP would share its blocker and needs VAP GA 1.30 above the 1.29 floor.
- T47 - its options narrow or retire the hook ClusterRole.
- T48 - under B the unauthenticated metrics inventory covers only listed namespaces; 2B widens it back.
- T58 - the namespace list is a non-default render path its chart check should cover.
- T82 - `escalate`/`bind` not needed; if it lands, channel 4 and the threat line change.
