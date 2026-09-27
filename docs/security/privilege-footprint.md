# Privilege footprint

What the operator's ClusterRole, the per-instance sidecar Role and the pre-upgrade hook may
do, and what each rule costs if the component holding it is compromised. Who holds each grant
is [trust boundaries](trust-boundaries.md); how the operator's own pods run is
[operator pod posture](operator-pod-posture.md). The decisions behind it are
[ADR 0013](../adr/0013-operator-is-cluster-wide-privileged.md) (the privilege model) and [ADR 0014](../adr/0014-rbac-lives-in-three-places.md) (how the rules stay in sync).

Every permission below was read out of the manifests and the code, not out of
intent: the generated ClusterRole ([`config/rbac/role.yaml`](../../config/rbac/role.yaml)),
the chart ClusterRole
([`deploy/helm/valkey-operator/templates/clusterrole.yaml`](../../deploy/helm/valkey-operator/templates/clusterrole.yaml)),
the kubebuilder markers that generate the first
([`internal/controller/valkey_controller.go:201-218`](../../internal/controller/valkey_controller.go)),
and the per-instance Role builder
([`internal/builder/rbac.go`](../../internal/builder/rbac.go)). Where a statement is
**not** verified against this repository, it says so.

## The operator ClusterRole

Bound cluster-wide. Rules exactly as generated; the "so what" column is the
consequence for a compromised or misbehaving operator.

| API group | Resources | Verbs | Consequence |
|---|---|---|---|
| `vko.gtrfc.com` | `valkeys` | get, list, watch, **create**, update, patch, **delete** | Can delete any Valkey CR — and with it, by ownerReference GC, the whole cluster it describes. `create` is not needed by any reconcile path; it comes from the default marker set |
| `vko.gtrfc.com` | `valkeys/status`, `valkeys/finalizers` | get/update/patch, update | Status authority; finalizer updates |
| `""` | `configmaps`, `serviceaccounts`, `services` | get, list, watch, create, update, patch, delete | Can rewrite or delete **any** ConfigMap, ServiceAccount or Service in the cluster, not only its own. Deleting a foreign ServiceAccount invalidates its tokens |
| `""` | `pods` | get, list, watch, **delete**, patch | Can delete any pod in the cluster. This is the rolling-update primitive; it is not scoped to owned pods |
| `""` | `secrets` | get, list, watch, **delete** | **Reads every Secret in the cluster** (the heaviest confidentiality exposure) and can destroy any of them. `delete` exists for one caller: the legacy `<name>-sentinel-tls` cleanup on the `unifiedCertificate` migration. That caller no longer deletes on the name alone — it requires either a Certificate this Valkey controls issuing into that name, or cert-manager's `cert-manager.io/certificate-name` annotation, plus `type: kubernetes.io/tls`, plus a UID precondition ([ADR 0006](../adr/0006-delete-only-what-the-operator-owns.md)). The *grant* is still cluster-wide, so a compromised operator is unaffected by that guard |
| `""` + `events.k8s.io` | `events` | create, patch | Can write Events anywhere. Both groups are listed because the operator records through `events.k8s.io/v1` while older tooling still reads the core group ([ADR 0014](../adr/0014-rbac-lives-in-three-places.md)) |
| `apps` | `deployments`, `statefulsets` | get, list, watch, create, update, patch, delete | Can replace the pod template — hence the image, hence the code — of **any** Deployment or StatefulSet in the cluster |
| `cert-manager.io` | `certificates` | full CRUD | Can request certificates from any Issuer/ClusterIssuer the namespace can reference, and delete existing ones. The legacy-Sentinel cleanup deletes only Certificates this Valkey controls by ownerReference ([ADR 0006](../adr/0006-delete-only-what-the-operator-owns.md)); no other path deletes a Certificate |
| `monitoring.coreos.com` | `servicemonitors` | full CRUD | Scrape configuration; used only when `spec.metrics.serviceMonitor.enabled` |
| `networking.k8s.io` | `networkpolicies` | full CRUD | **Can delete any NetworkPolicy in the cluster**, including policies that protect unrelated workloads |
| `policy` | `poddisruptionbudgets` | full CRUD | Availability guarantees of any workload can be removed or tightened |
| `rbac.authorization.k8s.io` | `roles` | get, list, watch, create, update, patch, delete, **escalate**, **bind** | **The privilege ceiling.** `escalate` lifts the rule that a principal may only grant permissions it holds, so the operator can write a Role containing *any* namespaced permission, in *any* namespace |
| `rbac.authorization.k8s.io` | `rolebindings` | get, list, watch, create, update, patch, delete | Together with the row above and `serviceaccounts` create: **create SA → write Role → bind → use.** A compromised operator is equivalent to namespaced admin in every namespace |
| `coordination.k8s.io` | `leases` | full CRUD | **Chart only**, not in the generated role — leader election (`--leader-elect`, off unless `leaderElection.enabled`). Legal drift: the drift guard checks containment, not equality ([`rbac_drift_test.go`](../../internal/controller/rbac_drift_test.go)) |

**The honest summary of that table:** the operator is not a namespaced workload
manager with a few extra rights. Between `roles/escalate` + `rolebindings` +
`serviceaccounts` and `secrets: get,list`, it is a cluster-wide privileged
component. Treat access to its ServiceAccount token, its image and its CR API the
way you treat access to a cluster-admin credential.

`escalate` and `bind` are not gratuitous: without them the API server refuses to
let the operator create the `<cr-name>-sidecar` Role, since a principal may not
grant permissions it does not itself hold — but it does hold ~~`pods: patch`~~ `pods: get`
and `pods: patch` cluster-wide, and the sidecar Role is now a strict subset of that
(~~`patch`~~ `get` and `patch` on named pods) *(corrected 2026-09-27: the Role grants both
verbs, see [the per-instance sidecar Role](#the-per-instance-sidecar-role))*, so the narrower
alternative (dropping `escalate`) is worth testing.
**Not verified:** whether the sidecar Role can in fact be created without
`escalate` on this Kubernetes version.

## The per-instance sidecar Role

```yaml
# internal/builder/rbac.go — BuildSidecarRole
rules:
  - apiGroups: [""]
    resources: ["pods"]
    verbs: ["get", "patch"]
    resourceNames: ["<cr-name>-0", "<cr-name>-1", "<cr-name>-2"]   # example: replicas 3
```

What the sidecar actually calls: **`Pods(ns).Patch` and `Pods(ns).Get`, nothing
else.** Verified by grep over `internal/sidecar` and `cmd/sidecar` — the clientset
call sites are `patchMetadata` ([`internal/sidecar/labeler.go`](../../internal/sidecar/labeler.go)),
used by `PatchLabel` (own pod, `instanceRole`) and `PatchAnnotation` (the peer pod
the drain handler promoted), and `IsTerminating` (same file), the drain handler
reading whether a promotion candidate carries a `DeletionTimestamp` before it
forwards the drain window to it (added 2026-08-27, ADR 0028 D5a — promoting a
terminating peer was measured wiping the fleet). `get` on the same named pods
reveals pod specs of this cluster only; the Secrets those pods use are mounted,
never inlined, so the read exposes no credential material. ~~The grant matches that exactly: one verb, and only the
pods of this cluster.~~ The grant matches that exactly: two verbs, `get` and `patch`, and only
the pods of this cluster *(corrected 2026-09-27: this said "one verb", but `BuildSidecarRole`
grants `[get, patch]` and `TestBuildSidecarRole` asserts exactly that pair — the YAML above was
right)*. `TestBuildSidecarRole` pins verb set and name list together,
and the operator rewrites the Role on every reconcile, so existing clusters narrow
on their next pass with no migration step.

**How the name list is built** ([`SidecarRolePodNames`](../../internal/builder/rbac.go)):
the union of the pods `spec.replicas` asks for and the pods that currently carry the
cluster's data-pod labels. The desired half covers scale-up — the `sidecar RBAC`
reconcile step runs before the `StatefulSet` step, so pod N is granted before it is
created. The live half covers scale-down: a pod being removed keeps its grant until
it is actually gone, because its drain handler still sets `instanceRole=draining` on
itself to leave the `-rw` Service before failing over. Two safety properties are
pinned by tests: an empty list would match *every* pod in Kubernetes RBAC, so a
cluster with no pods gets no rule at all; and names coming from the label selector
are filtered to the `<cr-name>-<ordinal>` form, so a pod created with this cluster's
labels under a foreign name cannot widen the grant.

Two writes reach the operator's decisions through this grant:

- `instanceRole=master|replica|draining` — the label the `-rw` / `-r` Services
  select on, i.e. **where client writes go**.
- `vko.gtrfc.com/drain-promoted-at` — the drain stamp the operator accepts as
  evidence that a promotion it did not perform was legitimate, and on which it
  will demote other masters (`REPLICAOF`, destructive).

Both writes are therefore confined to the cluster the sidecar belongs to. What the
grant does **not** stop: a compromised sidecar lying about its *own* cluster — it can
label any of its own pods master, and it can forge its own drain stamp. That is
inherent to the mechanism, not a gap
([ADR 0012](../adr/0012-the-sidecar-records-its-drain-promotion-on-the-pod.md) D8,
Residual risks).

The observer no longer shares this ServiceAccount: it runs under `<cr-name>-observer`,
which is bound to no Role, and its pod sets `automountServiceAccountToken: false`, so
it mounts no token to steal.

**Since 2026-08-27 the grant reaches one container, not the whole data pod.** The pod
still runs as `<cr-name>-sidecar` — a pod has one identity — but it sets
`automountServiceAccountToken: false` and hands the token to the `sidecar` container
through a projected volume it declares itself. `valkey-server`, every init container
and the third-party `redis_exporter` now hold no token. Sentinel pods, which
never call the API, set the same flag and declare no projection. This is D8 step 4 of
[ADR 0012](../adr/0012-the-sidecar-records-its-drain-promotion-on-the-pod.md);
[isolation and tenancy](isolation-and-tenancy.md#what-does-not-hold) lists what the grant still permits the sidecar itself.

## The pre-upgrade hook

`<release>-upgrade` gets `valkeys: get,list,patch,update` and
`customresourcedefinitions: get,list,patch,update`, cluster-wide, for the duration
of the Job. `patch`/`update` on CRDs is a **cluster-wide schema-change grant**: a
compromised hook image could rewrite the schema or the conversion strategy of any
CRD in the cluster. It is disabled with `preUpgradeHook.enabled: false`, at the
cost of the field-default migration it performs
([`cmd/migrate`](../../cmd/migrate/migrate.go)). Its pod carries the operator's pod posture
([operator pod posture](operator-pod-posture.md)); it mounts its token because it talks to the API server.

## What this does not cover

<a id="h-1"></a>

### H-1: Scope the operator away from `secrets: get,list` on everything

It needs
the auth Secret and the TLS Secret of the namespaces it serves, not the
cluster's Secrets. Since 2026-08-26 the TLS Secret is read on **every pass** of
every TLS cluster, for the material fingerprint, so a filtered cache has one
more consumer to satisfy. Options: a namespaced Role per watched namespace, or a
cache filtered by label with the ClusterRole narrowed to match. Cost: the
operator stops being install-and-forget for new namespaces. The cluster-wide scope is the
decision of [ADR 0013](../adr/0013-operator-is-cluster-wide-privileged.md) D1.

<a id="h-2"></a>

### H-2: Re-examine `roles: escalate` + `rolebindings` + `serviceaccounts: create`

That triple is namespaced admin everywhere. Test whether the sidecar Role can
be created without `escalate` now that it is a strict subset of the
operator's own pod grant ([the operator ClusterRole](#the-operator-clusterrole)) — and if so, drop the verb.

<a id="h-3"></a>

### H-3: Disable the pre-upgrade hook (`preUpgradeHook.enabled: false`) unless a migration needs it

Or accept a cluster-wide CRD write grant during every
upgrade.

What that grant permits: [the pre-upgrade hook](#the-pre-upgrade-hook). The hook's own code
uses none of the CRD half *(verified 2026-09-27 against the chart and the code)*: `migrate.Run`
([`cmd/migrate/migrate.go`](../../cmd/migrate/migrate.go)) lists and patches `Valkey` objects
and nothing else, and no Go file under `cmd/` or `internal/` imports `apiextensions`. The
comment on the rule in
[`pre-upgrade-rbac.yaml`](../../deploy/helm/valkey-operator/templates/pre-upgrade-rbac.yaml),
"Update the CRD to the latest schema before migrating existing CRs", describes a step the hook
never takes, so `customresourcedefinitions: get,list,patch,update` is granted and unused.
