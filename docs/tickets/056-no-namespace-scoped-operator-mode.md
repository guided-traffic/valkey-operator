---
id: T56
title: the operator has no namespace-scoped mode
state: analysed       # was filed; every load-bearing fact re-verified at 84a39c2 and the options re-weighed (History 2026-09-27)
severity: medium      # the operator stays equivalent to cluster-admin, with every Secret of the cluster in its memory
security: hardening
threat: "would additionally cover a compromise of the operator's ServiceAccount token, image or process: today it may read and delete every Secret, patch the image of any running pod, create Deployments and StatefulSets under any ServiceAccount, and write itself any namespaced permission (roles escalate/bind) in every namespace, kube-system included, which makes it cluster-admin equivalent; an opt-in namespace-scoped mode would confine that to the listed namespaces and what their ServiceAccounts hold"  # widened 2026-09-27 at 84a39c2; was: "... it may get, list, watch and delete every Secret in the cluster and holds every Secret it watches in its informer cache; a namespace-scoped mode would confine that to the namespaces it serves"
urgency: icebox       # rule 5 since 2026-09-27, re-derived at 84a39c2: no rule-1 statement left in this family (H-1 / ADR 0013 corrected in bcc63c9), no release gate, hardening needs a compromise, no decided fix; the mode reopens ADR 0013 D1
effort: M             # was L; M assumes the proof sits in the integration tier and the chart keeps one ClusterRole bound per namespace by RoleBinding (Options); L if the owner wants a full-suite e2e leg in the mode. The H-1/ADR 0013 correction alone was XS and is done
blocked-by: decision  # ADR 0013 D1, see Options
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
  ([`clusterrole.yaml:64-72`](../../deploy/helm/valkey-operator/templates/clusterrole.yaml)),
  bound cluster-wide ([`clusterrolebinding.yaml:1-14`](../../deploy/helm/valkey-operator/templates/clusterrolebinding.yaml)).
- The manager restricts its cache neither by namespace nor per object: a grep for
  `DefaultNamespaces` and `ByObject` in [`cmd/main.go`](../../cmd/main.go) finds nothing, and
  the Secret informer runs cluster-wide with no filter
  ([ADR 0016](../adr/0016-authentication-and-tls-posture.md) D2). *(Re-checked 2026-09-27 at
  84a39c2: [`cmd/main.go:102-110`](../../cmd/main.go) sets only `Scheme`, `Metrics`,
  `HealthProbeBindAddress`, `LeaderElection` and `LeaderElectionID`; `grep -rn
  "DefaultNamespaces\|ByObject\|cache.Options\|client.CacheOptions" --include='*.go' cmd internal
  | grep -v _test.go` prints nothing, exit 1.)*
- [ADR 0013](../adr/0013-operator-is-cluster-wide-privileged.md) D1 treats the operator as
  equivalent to a cluster-admin credential and states that a namespace does not confine it; its
  *Alternatives Considered* record "a namespaced Role per watched namespace, or a cache filtered
  by label with the ClusterRole narrowed to match", with the cost that the operator stops being
  install-and-forget for new namespaces. *(2026-09-27: the second half cannot be built, see the
  appendix.)* *(2026-09-27, later: the ADR now strikes the second half in place and records one
  option only, work list item 1, History.)* *(Locations at 84a39c2: D1 at ADR 0013 `:145-149`,
  the alternative at `:422-430`, the Status line at `:125-130`.)*
- Since 2026-08-26 the TLS Secret is read on every pass of every TLS cluster, for the material
  fingerprint ([H-1](../security/privilege-footprint.md#h-1)), ~~so a filtered cache has one more
  consumer to satisfy.~~ *(corrected 2026-09-27 at 84a39c2: the read holds —
  [`tls_material.go:94-102`](../../internal/controller/tls_material.go) calls `tlsMaterialHash`,
  which does a cache-backed `r.Get` at [`tls_material.go:121-131`](../../internal/controller/tls_material.go)
  — but it puts no constraint on a namespace-restricted cache. The sentence was written for the
  label-filter option, which no longer exists. Every Secret read names `v.Namespace`
  ([`tls_material.go:121-123`](../../internal/controller/tls_material.go),
  [`valkey_controller.go:181-185`](../../internal/controller/valkey_controller.go)), and `api/v1`
  has no Secret namespace field, so a cache restricted to the CR's namespace serves every read
  automatically. H-1 carries the same sentence; it is left until the close.)*

*Added 2026-09-27 (enrichment, at `4a7543e`):*

- **Every rule of the chart ClusterRole is on a namespaced resource**
  (`clusterrole.yaml` lines 8–188), with no `nonResourceURLs`. The resources are `valkeys`
  with status and finalizers, `configmaps`, `services`, `serviceaccounts`, `pods`, `secrets`,
  `deployments`, `statefulsets`, `events`, `networkpolicies`, `poddisruptionbudgets`,
  `certificates`, `servicemonitors`, `roles` (with `bind` and `escalate`, lines 150–163),
  `rolebindings` and `leases`. Every rule can therefore move into a Role.
  *(Re-checked 2026-09-27 at 84a39c2: holds. It also means every rule takes effect per namespace
  when the unchanged ClusterRole is bound by a RoleBinding, see below.)*
- The controller watches `Valkey`, owns nine namespaced kinds, and watches every `Secret`
  ([`valkey_controller.go:2984-3002`](../../internal/controller/valkey_controller.go): `For` at
  `:2987`, the nine `Owns` at `:2988-2996`, `Watches(Secret)` at `:2997-3000`).
  That Secret watch is the cluster-wide informer that holds every Secret in memory.
  The cache-backed pod Lists ([`valkey_controller.go:1064`](../../internal/controller/valkey_controller.go),
  [`steady_state_master.go:196`](../../internal/controller/steady_state_master.go) and `:732`)
  add a cluster-wide Pod informer.
- ServiceMonitor CRD absence is detected by `meta.IsNoMatchError` (`valkey_controller.go`
  lines 665 and 714), which is RESTMapper discovery and needs no RBAC rule.
- ~~The pre-upgrade hook's grant stays cluster-scoped in any mode.~~ It is a ClusterRole with
  `valkeys: get, list, patch, update` and `customresourcedefinitions: get, list, patch, update`
  ([`pre-upgrade-rbac.yaml:29-50`](../../deploy/helm/valkey-operator/templates/pre-upgrade-rbac.yaml)),
  bounded in time, not scope ([ADR 0013](../adr/0013-operator-is-cluster-wide-privileged.md) D10).
  *(corrected 2026-09-27 at 84a39c2: the hook is cluster-scoped whenever it is rendered, because
  `migrate.Run` lists every Valkey with no namespace
  ([`migrate.go:54-55`](../../cmd/migrate/migrate.go)), but it is already optional today: the
  whole file, ClusterRole included, sits under `{{- if .Values.preUpgradeHook.enabled }}`
  ([`pre-upgrade-rbac.yaml:1`](../../deploy/helm/valkey-operator/templates/pre-upgrade-rbac.yaml)),
  default `true` ([`values.yaml:151-153`](../../deploy/helm/valkey-operator/values.yaml)), and gap
  [H-3](../security/privilege-footprint.md#h-3) already recommends turning it off unless a
  migration needs it. An installation in the mode that wants no cluster-scoped operator grant can
  switch it off at the cost of the field-default migration; T47 is not a precondition.)*
- **The drift guard sees one file.** `rbac_drift_test.go` compares `config/rbac/role.yaml` with
  the rules block of `clusterrole.yaml` only
  ([`rbac_drift_test.go:34-35`](../../internal/controller/rbac_drift_test.go)), and fails
  on any `{{` inside that block ([`rbac_drift_test.go:113-115`](../../internal/controller/rbac_drift_test.go)).
  ~~A Role rendered per namespace needs a guard of its own.~~ *(corrected 2026-09-27 at 84a39c2:
  no second rules copy is needed. A RoleBinding may reference a ClusterRole and then grants its
  rules in the RoleBinding's namespace only ("a RoleBinding can reference a ClusterRole and bind
  that ClusterRole to the namespace of the RoleBinding",
  https://kubernetes.io/docs/reference/access-authn-authz/rbac/, fetched 2026-09-27). The mode
  can keep `clusterrole.yaml` literal and unchanged and switch only the binding, so the existing
  guard, `TestHelmClusterRoleCoversGeneratedRole`
  ([`rbac_drift_test.go:160`](../../internal/controller/rbac_drift_test.go)), keeps covering it.)*
- The operator's `pods: list` is cluster-wide (`clusterrole.yaml` lines 50–59). A pod spec
  names every Secret it mounts or references, so a token holder learns the name of every Secret
  a pod uses from pods alone. Secrets no pod references, such as Helm release Secrets, are not
  named this way. *(2026-09-27 at 84a39c2: true as a statement about names, but it bounds
  nothing: no Secret is out of the token's reach, see the next block.)*

*Added 2026-09-27 (re-verification at 84a39c2):*

- **The token reaches much more than Secrets, through four channels, in every namespace.**
  The chart mounts the token on purpose: the operator pod includes the
  `valkey-operator.podHardening` helper
  ([`deployment.yaml:30`](../../deploy/helm/valkey-operator/templates/deployment.yaml)), which
  states `automountServiceAccountToken: true`
  ([`_helpers.tpl:84-87`](../../deploy/helm/valkey-operator/templates/_helpers.tpl), "Both pods
  talk to the API server, so the token stays mounted"), so a process compromise includes the
  token. The ClusterRole grants, bound
  cluster-wide: (1) `secrets: delete, get, list, watch`
  ([`clusterrole.yaml:64-72`](../../deploy/helm/valkey-operator/templates/clusterrole.yaml));
  (2) `deployments`, `statefulsets: create, update, patch, ...`
  ([`clusterrole.yaml:73-85`](../../deploy/helm/valkey-operator/templates/clusterrole.yaml)),
  and "granting permission to create workloads also implicitly grants the API access levels of
  any service account in that namespace"
  (https://kubernetes.io/docs/concepts/security/rbac-good-practices/, "Workload creation");
  (3) `pods: patch` ([`clusterrole.yaml:50-59`](../../deploy/helm/valkey-operator/templates/clusterrole.yaml)),
  and a pod update may change `spec.containers[*].image` and `spec.initContainers[*].image`
  (Kubernetes v1.36.0 `pkg/apis/core/validation/validation.go:5691-5697`,
  `updatablePodSpecFields`, enforced by `ValidatePodUpdate` at `:5701` and `:5840`, message
  "pod updates may not change fields other than `spec.containers[*].image`,
  `spec.initContainers[*].image`, ...", fetched 2026-09-27), so the token can run code in any
  running pod under that pod's ServiceAccount, kube-system and flux-system pods included, without
  creating a workload; (4) `roles: bind, create, escalate` and `rolebindings: create`
  ([`clusterrole.yaml:150-175`](../../deploy/helm/valkey-operator/templates/clusterrole.yaml)),
  with which it writes itself any namespaced permission
  ([ADR 0013](../adr/0013-operator-is-cluster-wide-privileged.md) D2, `:151-156`); that the
  operator, by reading, needs neither `escalate` nor `bind` for the sidecar Role and RoleBinding
  it writes is gap H-2, filed as
  [T82](082-the-operator-is-granted-roles-escalate-and-bind-it-does-not-need.md). The
  `configmaps`, `services` create/update/patch rule
  ([`clusterrole.yaml:36-49`](../../deploy/helm/valkey-operator/templates/clusterrole.yaml)) adds
  tampering in any namespace (a kube-system ConfigMap, for example). **Consequence:** narrowing
  only the Secret rule or the Secret cache takes no Secret out of reach; only moving every rule
  out of the cluster-wide binding narrows the token.
- **The Secret reads.** Six cache-backed read sites:
  [`valkey_controller.go:131`](../../internal/controller/valkey_controller.go) (`buildTLSConfig`),
  `:181` (`readValkeyPassword`), `:1701` (`deleteLegacySentinelSecret`),
  [`tls_material.go:122`](../../internal/controller/tls_material.go),
  [`checker.go:79`](../../internal/health/checker.go) and `:356`; `valkey_controller.go:2998`
  is the watch, not a read. The Checker is built on `r.Client`
  ([`valkey_controller.go:120`](../../internal/controller/valkey_controller.go)). The uncached
  `APIReader` ([`valkey_controller.go:84-92`](../../internal/controller/valkey_controller.go),
  wired at [`main.go:116`](../../cmd/main.go)) serves only the delete gate
  ([`rolling_update.go:2118-2138`](../../internal/controller/rolling_update.go)).
- **The Secret watch is the rotation trigger.** The healthy pass returns no requeue
  ([`valkey_controller.go:390-396`](../../internal/controller/valkey_controller.go), "the healthy
  path still returns no requeue"), the `For(Valkey)` watch carries `GenerationChangedPredicate`
  (`:2987`), and the default resync is 10 h (controller-runtime v0.25.1 `pkg/cache/cache.go:45`,
  `defaultSyncPeriod`). Without the Secret watch a rotation or a password change would wait for an
  unrelated owned-object event or that resync.
- **No memory-only exposure path exists today.** pprof is off: `managerOptions` sets no
  `PprofBindAddress` ([`main.go:102-110`](../../cmd/main.go)), and "" disables it
  (controller-runtime v0.25.1 `pkg/manager/manager.go:248-253`). The token is mounted in the same
  pod as the cache. A cache that holds fewer Secrets therefore protects against nothing the token
  does not already open.
- **Leader election in a restricted mode.** `leaderElection.enabled` defaults to `true`
  ([`values.yaml:86-87`](../../deploy/helm/valkey-operator/values.yaml)); the lease lives in the
  pod's namespace, and the resource lock records its events through the core-group recorder
  (`recorderProvider.GetEventRecorderFor`, controller-runtime v0.25.1
  `pkg/leaderelection/leader_election.go:130`, with an upstream TODO to move to the new events
  API), while the reconciler itself records through `events.k8s.io`
  ([`main.go:118`](../../cmd/main.go)). The release namespace therefore needs `leases` and core
  `events: create, patch` unless it is itself a listed namespace. A refused event is logged, not
  fatal. `OperatorNamespace` is used only as a NetworkPolicy peer
  ([`valkey_controller.go:1952`](../../internal/controller/valkey_controller.go), `:1959`), never
  for a cached read.
- **One missing grant stops the whole operator at its next restart** (by reading, not measured).
  With `DefaultNamespaces`, each informer is a `multiNamespaceInformer` whose `HasSynced` is true
  only when every per-namespace informer has synced (controller-runtime v0.25.1
  `pkg/cache/multi_namespace_cache.go:472-479`). A listed namespace whose grant is missing (the
  namespace was deleted, or deleted and recreated, after the last `helm upgrade`, which deletes
  its RoleBinding) answers list/watch with 403 and never syncs. A running operator keeps serving
  the other namespaces and logs 403s; after any restart the controller waits `CacheSyncTimeout`
  (default 2 min, `pkg/controller/controller.go:267-268`), fails with "failed to wait for ...
  caches to sync" (`pkg/internal/controller/controller.go:366-384`) and the manager exits: a
  crashloop that stops reconciliation for every listed namespace. It is the per-namespace form of
  the crashloop [ADR 0014](../adr/0014-rbac-lives-in-three-places.md) D8 accepts (`:135-144`).
- **A listed namespace must exist when the chart is installed or upgraded**: the
  NamespaceLifecycle admission plugin refuses objects in a namespace that does not exist
  (https://kubernetes.io/docs/reference/access-authn-authz/admission-controllers/#namespacelifecycle).
- **The CRD ships unconditionally** in `templates/crd.yaml` (no `if`, ADR 0014 D9 `:146-149`),
  so a namespace-scoped mode is one release per cluster serving a list, not one release per
  tenant, and the chart installer still needs cluster-scoped rights.
- **An unlisted CR is invisible to the operator's own metrics.** The collector lists Valkey from
  the manager cache ([`main.go:183`](../../cmd/main.go),
  [`collector.go:161`](../../internal/metrics/collector.go)), so under `DefaultNamespaces` an
  unlisted CR produces no `vko_valkey_*` series and `ValkeySpecNotObserved`
  ([`prometheusrule.yaml:32`](../../deploy/helm/valkey-operator/templates/prometheusrule.yaml))
  cannot fire for it. The operator sets no finalizer (`grep -rn 'AddFinalizer\|\.Finalizers'
  --include='*.go' internal cmd | grep -v _test.go` is empty), so such a CR still deletes and its
  owned objects are garbage-collected.
- **The mode is testable in the integration tier.** envtest starts kube-apiserver with
  `--authorization-mode=RBAC` by default (controller-runtime v0.25.1
  `pkg/internal/testing/controlplane/apiserver.go:303`, `:339`), and the suite does not override
  it ([`suite_test.go:63-65`](../../test/integration/suite_test.go) sets only
  `CRDDirectoryPaths`). A client impersonating a ServiceAccount bound only by RoleBindings, and a
  manager built with `DefaultNamespaces`, can therefore be tested without Kind.
- **The e2e fixture destroys pre-created namespaces.** `createNamespace` deletes an existing
  namespace, waits until it is gone and recreates it, and the cleanup deletes it after a passing
  test ([`e2e_test.go:101-133`](../../test/e2e/e2e_test.go)); the suite carries 57 distinct quoted
  `e2e-*` strings, nearly all of them namespace names (`grep -rhoE '"e2e-[a-z0-9-]+"' test/e2e |
  sort -u | wc -l`; a few, such as `"e2e-term-"`, are prefixes or other names). A RoleBinding the chart
  rendered into such a namespace dies with it. The suite installs through Helm
  ([`Makefile:218`](../../Makefile)).
- **The absent cert-manager CRD is answered** (was *Not verified* below):
  `reconcileCertificate` returns every error but NotFound unchanged
  ([`valkey_controller.go:1884-1891`](../../internal/controller/valkey_controller.go)), so
  NoKindMatch fails the pass (ADR 0016 D5, `:81-85`); the only `IsNoMatchError` sites are `:665`
  and `:714`. The Certificate is `unstructured`, which the default client never caches
  (controller-runtime v0.25.1 `pkg/client/client.go:92-95`, `:311-313`), so the read is a live GET
  under the namespaced `certificates` rule. It has no bearing on the mode.
- **Cross-family finding, filed as T70.** ADR 0013 D3 states the sidecar Role verbs wider than
  [`rbac.go:67-76`](../../internal/builder/rbac.go) grants them; the correction is item (d) of
  [T70](070-tracked-comments-and-adrs-state-what-the-code-contradicts.md), which collects the
  decision-free record corrections of the 2026-09-27 re-verification. No decision of this ticket
  rests on it.

**Not verified:**

- ~~Which cluster-scoped reads remain in a namespace-scoped mode (how the operator detects the
  ServiceMonitor and cert-manager CRDs was not read).~~ *(answered 2026-09-27, above:
  ServiceMonitor by discovery, every chart rule namespaced, the hook's CRD grant cluster-scoped
  whenever the hook is rendered; the part left open by the enrichment, how an absent cert-manager
  CRD is handled, answered at 84a39c2 in the Verified block above: it fails the pass, and it has
  no bearing on the mode.)*
- ~~Whether a namespaced Role still needs `escalate` to create the sidecar Role — the question
  ADR 0013 D3 leaves open.~~ *(corrected 2026-09-27 at 84a39c2: the question is not specific to
  the mode; it is gap [H-2](../security/privilege-footprint.md#h-2)'s, and its answer is the same
  for a ClusterRole and for a ClusterRole bound by RoleBinding: by reading, neither `escalate` nor
  `bind` is needed in either shape, because the operator already holds every rule of the sidecar
  Role. Filed as
  [T82](082-the-operator-is-granted-roles-escalate-and-bind-it-does-not-need.md), which carries
  the documented rule, the Kubernetes source reading, the options and the integration test that
  decides it. No decision of this ticket rests on the answer.)*
- The crashloop above is derived from reading controller-runtime v0.25.1; it was not measured.
  An envtest manager with `DefaultNamespaces` over two namespaces, one of them without the
  RoleBinding, and a restart settles it.
- Which ServiceAccounts in the production namespaces (gitlab, gpt, harbor, iam,
  database-examples) hold cluster-wide rights; that bounds what the mode confines to. Needs read
  access to the cluster.
- Whether the fleet's `cluster-ca` issuer is a CA ClusterIssuer, whose key Secret sits in
  cert-manager's cluster resource namespace (default `cert-manager`,
  https://cert-manager.io/docs/configuration/).
- Whether Flux helm-controller drift detection is enabled in the fleet (it would restore a
  RoleBinding lost with a recreated namespace without a values change), and whether Flux's health
  check reports an unserved CR with no status as ready.
- The memory cost of caching every Secret of the cluster, Helm release Secrets included.

**Related tickets** *(added 2026-09-27 at 84a39c2)*: T29 — options G and 2C, considered and not added (History),
would share its blocker, ~~the ADR 0015 D2 re-decision~~ *(corrected 2026-09-27, consistency
pass: 029's re-verification found ADR 0015 D2 scoped to what validates a `Valkey` object; its
blocker is now its own Decision 1, a product decision and a new ADR, with a clarifying Status note
on ADR 0015)* and ValidatingAdmissionPolicy GA at 1.30
against the README floor of 1.29. T47 — its options narrow or retire the hook ClusterRole
(`pre-upgrade-rbac.yaml`), so what this ticket says of the hook follows whatever T47 decides.
T48 — under B the collector reads the restricted cache
([`collector.go:161`](../../internal/metrics/collector.go)), so the unauthenticated inventory
T48 describes covers only the listed namespaces; 2B would widen it back. T58 — the namespace
list is a non-default render path its chart check should cover (058 already names T56); under
B the RBAC drift guard itself needs no rendered chart. T40 — names 056 as the ticket holding
H-1's work, which stays correct. T70 — carries the ADR 0013 D3 verb correction found here, as its
item (d) ([070](070-tracked-comments-and-adrs-state-what-the-code-contradicts.md)). T82 —
gap H-2 (`escalate`/`bind`), filed 2026-09-27 from this ticket's *Not verified* item
([082](082-the-operator-is-granted-roles-escalate-and-bind-it-does-not-need.md)); if its
option A lands, this ticket's `threat:` line, Fact channel (4) and the Decision 1 mechanism
sentence naming `escalate`/`bind` change with it (082 work list item 6).

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
  (rule 1), and the correction is the XS slice in the work list. *(Re-checked 2026-09-27 at
  84a39c2: `rbac/v1/types.go:49-76`, fields at `:54`, `:60`, `:64`, `:68`, `:75`. The
  correction is committed in bcc63c9.)*

## Impact

~~Every install: whoever obtains the operator's token or runs code in its process reads every
Secret in the cluster.~~ *(corrected 2026-09-27 at 84a39c2: this understated the grant. Every
install: whoever obtains the operator's token or runs code in its process reads and deletes every
Secret, runs code in any running pod by patching its image, creates workloads under any
ServiceAccount, and writes itself any namespaced permission, in every namespace, kube-system and
flux-system included; that is cluster-admin equivalent (Fact). Live today only after a compromise
of the token, image or process, which is why the class is hardening.)* Multi-tenant clusters
cannot confine the operator to its tenants' namespaces. No multi-tenant user is named; the
production fleet has one owner.

*(added 2026-09-27)* H-1 and ADR 0013 ~~describe~~ *(described, until work list item 1 on
2026-09-27)* an option as able to narrow the grant when it cannot (appendix). An administrator who picks it gets a smaller cache and an unchanged token.

## Options

The H-1/ADR 0013 correction needed no decision and is done (work list). **Decision 1 comes
first.** Decision 2 exists only if decision 1 is B.

### Decision 1: whether to reopen ADR 0013 D1 with an opt-in namespace-scoped mode

**Mechanism today.** [`cmd/main.go:102-110`](../../cmd/main.go) builds the manager with no cache
options, so every informer is cluster-wide, the Secret watch
([`valkey_controller.go:2997-3000`](../../internal/controller/valkey_controller.go)) included.
The chart binds one ClusterRole
([`clusterrole.yaml:7-188`](../../deploy/helm/valkey-operator/templates/clusterrole.yaml)) through
one ClusterRoleBinding
([`clusterrolebinding.yaml:1-14`](../../deploy/helm/valkey-operator/templates/clusterrolebinding.yaml)).
Every rule is on a namespaced resource, and through Secrets, workload create, pod image patch and
`escalate`/`bind` the token is cluster-admin equivalent (Fact).

**What the choice changes.** Whether an installation can confine all of that to a list of
namespaces. **What it does not change:** the default install (an empty list is today's install);
the installer's cluster-scoped rights (the CRD in `templates/`, ADR 0014 D9); the pre-upgrade
hook, which stays a cluster-wide ClusterRole whenever `preUpgradeHook.enabled` is true
([`migrate.go:54-55`](../../cmd/migrate/migrate.go)); what the operator does for a served CR; and
any Valkey pod: the list reaches only cache options and RBAC templates, no builder, so enabling
it rolls no data, Sentinel or observer pod.

- **A — keep D1 and record the refusal.** ADR 0013's alternative "A namespaced Role per watched
  namespace" becomes a dated rejection with its reason, H-1 is restated as an accepted residual
  risk, and the ticket is dropped. Cost: XS (one ADR 0013 edit with a Status line, H-1 reworded).
  Consequence: the operator stays namespaced-admin in every namespace; after a compromise, a
  kube-system or flux-system pod image patch, or a workload there, turns it into cluster-admin,
  and cert-manager's cluster resource namespace stays readable.
- **B — an opt-in namespace list, the unchanged ClusterRole bound per namespace
  (recommended).** A chart value lists namespaces (default empty). The operator gets a flag that
  sets controller-runtime `cache.Options.DefaultNamespaces`
  (v0.25.1 `pkg/cache/cache.go:139-150`). While the list is empty the chart renders the
  ClusterRoleBinding as today; while it is set it renders no ClusterRoleBinding but one
  RoleBinding per listed namespace that references the **same, unchanged** ClusterRole, plus a
  small Role and RoleBinding in the release namespace for `leases` and core `events: create,
  patch` (unless the release namespace is listed). The release namespace is deliberately not
  given the full ClusterRole, which would hand the operator the Secrets there, the chart's own
  Helm release Secrets included. `clusterrole.yaml` keeps its literal rules block, so the drift
  guard, ADR 0014 D2 to D4 and the operator ClusterRole table stay valid; an unbound ClusterRole
  grants nothing. Cost: M — flag and cache options (S), the binding switch and the release
  namespace Role (S), an envtest integration test under the RBAC authorizer (S-M), the ADR and
  documentation amendments (S); L only if a full-suite e2e leg in the mode is also wanted, which
  needs a fixture change (Fact) and amends ADR 0017 D31, which enumerates three legs, not only
  D47. Consequences:
  - It confines the token to the listed namespaces **plus whatever the ServiceAccounts in them
    hold**, because workload create and pod image patch run code under any ServiceAccount of a
    listed namespace. It does not make the operator low-privilege: in this fleet gitlab, harbor
    and iam would be listed and their Secrets stay in reach, and a Valkey CR placed next to the
    application it serves keeps that application's Secrets in reach. What leaves reach is every
    unlisted namespace — kube-system, flux-system, cert-manager.
  - A namespace added later needs a values change; in the production fleet that is a
    HelmRelease edit in the same Git repository that adds the CR, and it restarts only the
    operator pod. Each listed namespace must exist at install and upgrade (NamespaceLifecycle).
  - A listed namespace that loses its RoleBinding (deleted, or deleted and recreated, after the
    last `helm upgrade`) makes every later operator restart crashloop after the 2 min cache sync timeout, which
    stops reconciliation in every listed namespace (Fact, by reading). In a Flux fleet that
    orders namespaces and HelmReleases, this is the operational risk of B.
  - Switching an existing install into the mode: `helm upgrade` removes the
    ClusterRoleBinding while the old operator pod still runs, so until the rollout replaces it
    its cluster-wide reflectors see 403 and a pass in flight can fail. That is ADR 0014 D8's
    accepted failure shape, and the roll states it may leave are bounded (ADR 0010).
  - A CR in an unlisted or de-listed namespace is not served at all: its rolling-update state
    and known-master annotation freeze, and its pods get no no-master recovery and no
    split-brain resolution. Leaving database-examples off the list, where Chaos Mesh kills a pod
    every 5 minutes, would leave those clusters with no operator recovery (decision 2 covers how
    that is reported).
  - One release per cluster (the CRD is in `templates/`). The hook stays cluster-scoped unless
    `preUpgradeHook.enabled: false` (H-3, possible today) or T47 retires it.
  - Reopens ADR 0013 D1 deliberately, for installations that set the list; D1 stays the default.

**B is recommended** because it is the only option that changes what a compromised operator
token, image or process can do, and it costs existing installs nothing: the default list is
empty and the list reaches no pod builder, so no Valkey pod rolls. Checkable: in the integration
tier, a SubjectAccessReview for the operator ServiceAccount in an unlisted namespace denies
`secrets get`, `pods patch` and `deployments create`; on a real install `kubectl auth can-i
--list --as=system:serviceaccount:<release-ns>:<sa> -n kube-system` shows no operator rule. The
concrete gain even for a single-owner fleet is that kube-system and flux-system, where a pod image
patch or a workload makes the operator cluster-admin today, leave its reach. Binding the unchanged
ClusterRole per namespace keeps it at one copy of the rules and no new guard. **A is the
runner-up** and wins if no cluster the owner runs would set a list: B's value exists only on
clusters that set it, and B adds the crashloop risk above to every such cluster. **The question
that decides between A and B:** would the production fleet set the list (gitlab, gpt, harbor,
iam, database-examples, and every other namespace that holds a Valkey CR)?

### Decision 2 (only under B): how a CR in an unlisted namespace is reported

**Mechanism under B.** With `DefaultNamespaces` set, the `For(Valkey)` informer
([`valkey_controller.go:2987`](../../internal/controller/valkey_controller.go)) and every other
informer see only listed namespaces, so an unlisted CR never reaches `Reconcile`. Its status stays
empty (a blank Phase column), it produces no `vko_valkey_*` series, `ValkeySpecNotObserved` cannot
fire for it, its pods run with no operator action, and it still deletes cleanly (Fact).
**What the choice changes:** whether the operator says anything about such a CR. **What it does
not change:** that the CR is not served.

- **2A — report nothing, document the boundary (recommended).** The chart values text,
  `docs/operations/installation.md` and the README values table say which namespaces are served
  and what an unlisted CR looks like: empty status, no metrics, no alert, no recovery. Cost: XS
  documentation. Consequence: a CR put in the wrong namespace is silent; only the blank Phase
  column shows it.
- **2B — a cluster-wide Valkey watch that writes a condition.** A Valkey informer outside the
  list (`ByObject` with `cache.AllNamespaces` for Valkey), a mandatory namespace gate at the
  `Reconcile` entry (without it, a pass for an unlisted CR would read owned objects from a cache
  that does not hold that namespace and fail), a new `conditionRegistry` row
  ([ADR 0027](../adr/0027-conditions-are-levels-edges-or-history.md)), collector filtering, and a
  cluster-wide `valkeys: get, list, watch` plus `valkeys/status: patch, update` grant. The grant
  reaches no Secret, workload or RBAC object, but it is a ClusterRoleBinding again — the property
  an auditor checks with `kubectl get clusterrolebinding` — lets a token holder forge the status
  of CRs in excluded namespaces and read every Valkey spec, and widens the unauthenticated
  collector inventory (T48) back to the whole cluster. Cost: M.

**2A is recommended** because the list is the administrator's own statement of scope, and
whoever sets it also chooses where CRs live, as in the single-owner production fleet: 2A writes
nothing outside the list and costs documentation only. **2B is the runner-up**: it buys
visibility on the CR at the price of a new condition, an informer outside the list, status writes
into excluded namespaces and a cluster-wide binding, all for a mode with no named user. 2A's price
has to be written into the documentation, not left implicit.

## Work list

**Not waiting on a decision:**

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
   line in ADR 0013 (History). *(2026-09-27 at 84a39c2: committed in bcc63c9, not a
   working-tree diff; the Verification grep passes.)*
2. ~~**Outside this family:** file the ADR 0013 D3 verb misstatement as its own ticket.~~ **Done
   2026-09-27: filed as T70**, item (d)
   ([070](070-tracked-comments-and-adrs-state-what-the-code-contradicts.md)), the decision-free
   record corrections of the re-verification.

**Waiting on decision 1 (B):**

1. `cmd/main.go`: a namespace-list flag that sets `cache.Options.DefaultNamespaces`; empty keeps
   today's cluster-wide cache.
2. The chart: the value (default empty); the ClusterRole unchanged and always rendered; the
   ClusterRoleBinding only while the list is empty; one RoleBinding per listed namespace to the
   ClusterRole; a Role and RoleBinding in the release namespace for `leases` and core
   `events: create, patch` unless that namespace is listed; the flag passed from the value.
3. ~~A drift guard for the Role shape in `rbac_drift_test.go`.~~ *(corrected 2026-09-27 at
   84a39c2: not needed; the ClusterRole's rules block stays literal and the existing guard keeps
   covering it. The small release-namespace Role holds only `leases` and `events`.)*
4. ~~An e2e leg in the mode. Matrix legs are not required by name, so branch protection does not
   change ([ADR 0017](../adr/0017-test-and-ci-policy.md) D47).~~ *(corrected 2026-09-27 at
   84a39c2: under [ADR 0017](../adr/0017-test-and-ci-policy.md) D2 (`:225-233`) the API server
   decides this outcome, so the proof is an envtest integration test: the RBAC authorizer, a
   manager with `DefaultNamespaces`, an impersonated client bound only by RoleBindings. An e2e
   leg is optional; if kept it amends D31 (`:504-510`, three legs) as well as needing no
   branch-protection change (D47), and the fixture has to stop deleting and recreating the
   namespaces it lists, or create the RoleBinding itself (Fact).)*
5. Documentation of the operational rules in `docs/operations/installation.md`: listed
   namespaces must exist before install and upgrade, a recreated namespace needs the RoleBinding
   back before the next operator restart (the crashloop), adding a namespace is a values change,
   and the hook stays cluster-scoped unless disabled.

**Waiting on decision 2:** the README values text and `installation.md` (2A), or the status path
(2B).

**Close (ADR 0034):** amend ADR 0013 D1 (the opt-in exception) and its alternative; ADR 0014
(a binding switch, D2 to D4 unchanged, D8's crashloop in its per-namespace form); ADR 0016 D2
(`:45-52`) and D5 (`:78-81`), which say the Secret watch is cluster-wide with no filter; ADR 0017
D31 only if an e2e leg is added. Then H-1 (including its "one more consumer" sentence),
[`trust-boundaries.md:12`](../security/trust-boundaries.md) ("Cluster-wide, all namespaces"),
[`README.md:187-189`](../../README.md) and the README values table, and
`docs/operations/installation.md`. The
[operator ClusterRole table](../security/privilege-footprint.md#the-operator-clusterrole) stays
valid. `git grep -n 'T56\b\|056-no-namespace' -- ':!docs/tickets'` (none at 84a39c2), then
move to `archive/`. Under A, the close is the ADR 0013 rejection and H-1's rewording, then
`dropped` and the move.

## Decision

None yet.

## Verification

- *(added 2026-09-27, XS slice)* `grep -n "narrowed to match" docs/security/privilege-footprint.md
  docs/adr/0013-operator-is-cluster-wide-privileged.md` finds the phrase only struck through.
  *(Run 2026-09-27 after the fix: `privilege-footprint.md:147` and ADR 0013 `:424` inside struck
  text, and ADR 0013 `:426` inside the correction that says no ClusterRole can be narrowed to
  match. Done.)* *(Re-run 2026-09-27 at 84a39c2, same three hits; the edits are in bcc63c9.)*
- In the mode, integration tier (envtest, RBAC authorizer): with the operator ServiceAccount
  bound only by RoleBindings, a SubjectAccessReview in an unlisted namespace denies `secrets
  get`, `pods patch`, `deployments create` and `roles create`; in a listed namespace the
  impersonated client creates the sidecar Role and RoleBinding; a manager with
  `DefaultNamespaces` does not reconcile a Valkey CR in an unlisted namespace. The same test
  without `escalate`/`bind` answers H-2 (the test itself is
  [T82](082-the-operator-is-granted-roles-escalate-and-bind-it-does-not-need.md)'s Verification).
- On a real install in the mode: `kubectl auth can-i --list
  --as=system:serviceaccount:<release-ns>:<sa> -n kube-system` shows no operator rule.
- Every chart render with the list empty is byte-identical in RBAC to today's (the ClusterRole
  unchanged, the ClusterRoleBinding present).
- A Valkey resource in an unlisted namespace is not reconciled; how that is reported is decision 2.

## History

- 2026-09-27 — re-verified at 84a39c2 (auditor, facts skeptic and design skeptic, disputed points
  re-read by this stage). **Checked:** every Fact bullet against the code, the chart and the
  pinned controller-runtime v0.25.1 and `k8s.io/api` v0.37.1 sources; the Kubernetes RBAC page
  (RoleBinding may reference a ClusterRole; the same-scope escalation rule) and
  `ValidatePodUpdate` at v1.36.0 (image is mutable) fetched; no docker measurement, because no
  claim concerns Valkey runtime behaviour; nothing was run. **Outdated or false, corrected in
  place:** the History claim that work list item 1 sat in a working-tree diff (committed in
  bcc63c9); "a filtered cache has one more consumer" (no constraint on a namespace cache);
  "the hook's grant stays cluster-scoped in any mode" (the hook is optional today,
  `preUpgradeHook.enabled`, H-3); "a Role rendered per namespace needs a guard of its own" and
  work list item 3 (a RoleBinding to the unchanged ClusterRole needs no second copy); option D's
  "only Secrets that no pod references drop out of reach" (false: escalate/bind, workload create
  and pod image patch reach every Secret); decision 2B's reason "costs exactly the cluster-scoped
  write B removes" (overstated: the grant reaches no Secret, workload or RBAC object, the cost is
  code, a ClusterRoleBinding again and the T48 inventory widened); the Impact line (understated
  the grant); work list item 4 (the proof belongs in the integration tier, ADR 0017 D2; an e2e
  leg amends D31). Not-verified item 1 answered (cert-manager CRD absence fails the pass, no
  bearing); item 2 re-scoped to H-2. **Location drift fixed in the links:** watch list
  `:2984-3002`, `pre-upgrade-rbac.yaml:29-50`, `rbac_drift_test.go:113-115`; locations re-read at
  84a39c2. **Added facts:** the four channels of the token and pod image patch; six Secret read
  sites (the auditor counted seven); the missing steady-state requeue; pprof off; leader
  election's core events; the per-namespace crashloop (by reading, not measured);
  NamespaceLifecycle; the unconditional CRD; the collector and finalizer behaviour; envtest's
  RBAC authorizer; the e2e fixture that recreates namespaces; the ADR 0013 D3 verb misstatement,
  recorded as a cross-family finding that needs its own H-2 ticket (not appended here, which
  would make rule 1 match). **Options removed:** C (label-filtered Secret cache, ClusterRole
  unchanged) — narrows no grant, `PolicyRule` has no selector, and the remaining cluster-wide
  channels reach every Secret; memory only, and no memory-only exposure path exists; it would
  also force every CR author to label the auth Secret. D (cluster-wide `secrets get` only, no
  informer, uncached reads) — rests on the false premise above, moves six read sites, and
  removes the rotation trigger, which reopens ADR 0030. **Options considered and not added:**
  E (make the list mandatory) — every upgrade without a list stops serving the fleet, against
  ADR 0005 D1 and ADR 0014 D8's single upgrade path; ADR 0013 D1 is a decided scope, not a
  posture defect. F (metadata-only Secret watch plus uncached reads) — same memory-only class as
  C and D. G (a chart-shipped ValidatingAdmissionPolicy confining the operator's writes) —
  admission never sees reads, it needs VAP GA 1.30 above the README floor of 1.29, and it
  reopens ADR 0015 D2 (T29's blocker). 2C (a VAP refusing CRs in unlisted namespaces) — same
  T29 blocker, disproportionate for an opt-in mode with no named user. The auditor's added
  decision 3 (where the Role rules live, options 3a one rules file via `.Files.Get`, 3b a second
  literal copy with its own test, 3c a rendered-chart guard duplicating T58) was not adopted: its
  premise, that a per-namespace grant needs its own rules copy, is false. **Recommendation:** B
  still recommended, now as the unchanged ClusterRole bound per namespace, justified by
  kube-system and flux-system leaving reach (not by cert-manager's key Secret, whose issuer type
  is unverified); runner-up A, with the deciding question stated. 2A still recommended.
  **Frontmatter:** `state` filed -> analysed (facts verified, options coherent); `threat` widened
  to the four channels; `effort` L -> M (no second rules copy, the proof in the integration
  tier; L if a full e2e leg is wanted); `urgency` icebox re-derived by rule 5, comment updated;
  `severity`, `security`, `blocked-by` unchanged. **Review of this entry's edit:** the auditor's
  "the chart sets no `automountServiceAccountToken`" was false — the `podHardening` helper states
  `automountServiceAccountToken: true` (`_helpers.tpl:87`); the conclusion, the token is mounted,
  holds and the Fact bullet now says so. The escalation-rule quote is now verbatim from the
  kubernetes/website source; the `ValidatePodUpdate` citation carries `validation.go:5691-5697`;
  the crashloop's cause no longer names "listed without a `helm upgrade`" or "listed out of
  order", which cannot remove a RoleBinding the same release renders; a nested correction in
  *Not verified* was flattened; ADR 0014 D8 is `:135-144`; the 57 e2e strings are not all
  namespace names; a *Related tickets* paragraph (T29, T47, T48, T58, T40, H-2) was added.
  Cross-ticket: in the consistency pass of the same day, the T29 blocker in Related tickets was
  corrected (029's blocker is its Decision 1 and a new ADR, with a Status note on ADR 0015, not an
  ADR 0015 D2 re-decision), and 047 corrected its reading of this ticket's hook lines.
  Filed: the ADR 0013 D3 verb misstatement (the sidecar Role verbs), parked in Fact and Work list
  item 2, moved to [T70](070-tracked-comments-and-adrs-state-what-the-code-contradicts.md) item (d)
  (severity low, security hardening, effort S, state analysed, urgency now); the Fact bullet is a
  pointer, Related tickets names T70, and Work list item 2 is done. No frontmatter field of this
  ticket rested on the finding, which was kept out of this family on purpose, so severity, urgency
  and effort are unchanged.
  Sweep: Related tickets: the open filing of gap H-2 (`escalate`/`bind`), which no ticket carries,
  now names its owner (the next filing run, which Hans starts). Frontmatter unchanged.
  Final pass: gap H-2 is now filed as the tracked
  [T82](082-the-operator-is-granted-roles-escalate-and-bind-it-does-not-need.md) (security
  hardening, severity low, effort S, state analysed, blocked by a decision), so the H-2 notes
  point to it, as its work list item 6 asks: the *Not verified* item on `escalate` keeps its
  one-sentence answer and drops the RBAC-page quote and the untested-verbs note, which T82
  carries; the *Related tickets* note that no ticket carried H-2 and its sweep parenthetical are
  replaced by a T82 entry naming what changes here if its option A lands; Fact channel (4) and the
  Verification line on H-2 gained a pointer. Checked against T82's current text (its Fact cites
  the RBAC page section, its Verification holds the impersonated-user integration test); nothing
  was run. No decision, option or frontmatter field of this ticket changed.
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
