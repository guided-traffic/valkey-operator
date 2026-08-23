# Security Architecture

How the Valkey operator is trusted, what it is allowed to do, where credentials
live, and what the isolation it provides does **not** cover.

Every permission below was read out of the manifests and the code, not out of
intent: the generated ClusterRole ([`config/rbac/role.yaml`](config/rbac/role.yaml)),
the chart ClusterRole
([`deploy/helm/valkey-operator/templates/clusterrole.yaml`](deploy/helm/valkey-operator/templates/clusterrole.yaml)),
the kubebuilder markers that generate the first
([`internal/controller/valkey_controller.go:167-184`](internal/controller/valkey_controller.go)),
and the per-instance Role builder
([`internal/builder/rbac.go`](internal/builder/rbac.go)). Where a statement is
**not** verified against this repository, it says so.

Related: [README.md](README.md) (user-facing reference) and
[docs/adr/](docs/adr/README.md), which holds the decisions behind this document —
[ADR 0013](docs/adr/0013-operator-is-cluster-wide-privileged.md) (the privilege model),
[ADR 0014](docs/adr/0014-rbac-lives-in-three-places.md) (how the rules stay in sync),
[ADR 0016](docs/adr/0016-authentication-and-tls-posture.md) (auth and TLS) and
[ADR 0006](docs/adr/0006-delete-only-what-the-operator-owns.md) (the delete guards).
A `DEVELOPER.md` does not exist yet.

---

## 1. Roles and trust boundaries

| Principal | Identity | Scope | Trusted with |
|---|---|---|---|
| **Operator manager** | ServiceAccount `<release>` in the release namespace, bound by a **ClusterRoleBinding** ([`clusterrolebinding.yaml`](deploy/helm/valkey-operator/templates/clusterrolebinding.yaml)) | **Cluster-wide, all namespaces** | Every rule in section 4. Reads every Secret in the cluster, writes RBAC, deletes pods and Secrets |
| **Pre-upgrade hook** | ServiceAccount `<release>-upgrade`, cluster-wide, created and deleted per `helm upgrade` ([`pre-upgrade-rbac.yaml`](deploy/helm/valkey-operator/templates/pre-upgrade-rbac.yaml)) | Cluster-wide, lifetime of the hook Job | `valkeys` get/list/patch/update and `customresourcedefinitions` get/list/patch/update |
| **Sidecar** | ServiceAccount `<cr-name>-sidecar`, one per Valkey CR ([`BuildSidecarServiceAccount`](internal/builder/rbac.go)) | **This cluster's own data pods**, by name: `pods` patch with `resourceNames` (section 4.2) | Patching `instanceRole` on its own pod and the drain stamp on a peer pod |
| **Observer** | Its **own** ServiceAccount `<cr-name>-observer`, bound to no Role, with `automountServiceAccountToken: false` ([`BuildObserverServiceAccount`](internal/builder/observer.go)) | None — no Role, and no token mounted | Nothing — it makes no Kubernetes API call at all (verified: no `client-go` import in `internal/observer` or `cmd/observer`) |
| **Valkey pods** | Same `<cr-name>-sidecar` ServiceAccount — the whole pod, so the `valkey`, `sidecar` and `exporter` containers all carry the token ([`statefulset.go:547`](internal/builder/statefulset.go)) | Same | The `valkey` process itself needs no API access; only the sidecar container uses the token |
| **Sentinel pods** | The namespace `default` ServiceAccount ([`sentinel.go:368`](internal/builder/sentinel.go)) | Whatever `default` is bound to (nothing, in a stock cluster) | Nothing — Sentinel pods carry no labeler sidecar |
| **CR author** | Any principal with `create valkeys` in a namespace | That namespace | Chooses images, the auth Secret name, the TLS mode — see section 3 for what that buys them |

```
        cluster scope                          namespace scope
  ┌───────────────────────────┐        ┌──────────────────────────────────┐
  │  ClusterRole              │        │  Role <cr>-sidecar               │
  │  valkey-operator-role     │        │  pods: patch                     │
  │                           │        │  resourceNames: <cr>-0 … <cr>-N  │
  └───────────┬───────────────┘        └──────────────┬───────────────────┘
              │ ClusterRoleBinding                    │ RoleBinding
              ▼                                       ▼
  ┌───────────────────────────┐        ┌──────────────────────────────────┐
  │  SA <release>             │        │  SA <cr>-sidecar                 │
  │  operator Deployment      │        │  └── valkey pod                  │
  │  (release namespace)      │        │      (valkey+sidecar+exporter)   │
  └───────────┬───────────────┘        │                                  │
              │                        │  SA <cr>-observer: no Role,      │
              │                        │  no token (observer Deployment)  │
              │                        │  (sentinel pods use `default`)   │
              │                        └──────────────┬───────────────────┘
              │ creates + owns                        │
              ▼                                       │ patches
  StatefulSets, Deployments, Services, ConfigMaps,    │  metadata.labels
  SA/Role/RoleBinding, NetworkPolicies, PDBs,         │  metadata.annotations
  cert-manager Certificates, ServiceMonitors  ────────┘  of its own cluster's pods
              │
              │ reads                       ┌──────────────────────────┐
              ├────────────────────────────►│ auth Secret (user-owned) │
              │                             └──────────────────────────┘
              │ reads + DELETES             ┌──────────────────────────┐
              └────────────────────────────►│ any Secret, any namespace│
                                            └──────────────────────────┘
              │
              │ TCP: INFO / REPLICAOF / WAIT / SENTINEL, authenticated with
              ▼ the cluster password, TLS when spec.tls.enabled
        Valkey and Sentinel pods
```

**The two boundaries that matter.**

1. **Operator ↔ workload.** The operator is the only component that can change
   the cluster topology (`REPLICAOF`), and it is cluster-wide. A compromised
   operator is a cluster-wide compromise (section 4).
2. **Sidecar ↔ operator.** The sidecar cannot write the CR — deliberately
   ([ADR 0012](docs/adr/0012-the-sidecar-records-its-drain-promotion-on-the-pod.md)) — so it
   reports a promotion it performed by patching a **pod annotation**
   (`vko.gtrfc.com/drain-promoted-at`, [`internal/common/annotations.go`](internal/common/annotations.go)).
   The operator consumes that annotation as evidence and may issue a destructive
   `REPLICAOF` on the strength of it. Everything that can patch a pod in the
   namespace can therefore influence a topology decision — see section 9.

---

## 2. Data and secret flow

### The cluster password

`spec.auth.secretName` names a Secret the **user** creates and owns; the operator
never generates one. Authentication is on only when that field is set
(`IsAuthEnabled`, [`api/v1/valkey_types.go:726`](api/v1/valkey_types.go)) — a CR
without `spec.auth` runs an **unauthenticated** Valkey, and the generated config
sets `protected-mode no`
([`internal/builder/configmap.go:70`](internal/builder/configmap.go)), so the only
remaining barrier is the network.

| Consumer | How it receives the password | Reference |
|---|---|---|
| `valkey` container | env `VALKEY_PASSWORD` from `secretKeyRef`, expanded into `--requirepass` / `--masterauth` by a shell wrapper | [`statefulset.go:658,696`](internal/builder/statefulset.go) |
| init container | same env var, used for the `-a` flag of its discovery probes | [`statefulset.go:321,506`](internal/builder/statefulset.go) |
| `sidecar` container | same env var | [`statefulset.go:768`](internal/builder/statefulset.go) |
| `exporter` sidecar | env `REDIS_PASSWORD` from the same `secretKeyRef` | [`statefulset.go:883`](internal/builder/statefulset.go) |
| observer | same env var | [`internal/builder/observer.go:246`](internal/builder/observer.go) |
| **operator** | reads the Secret through the API and holds the plaintext in memory for the duration of a call | [`readValkeyPassword`, `valkey_controller.go:143`](internal/controller/valkey_controller.go) |

Consequences worth naming: the password is visible in every one of those
containers' environments (`kubectl exec ... env`, and in the pod spec as a
reference, not a value), and the `--requirepass "$VALKEY_PASSWORD"` form means the
**expanded password appears in the `valkey-server` process arguments** inside the
container, so any process in that pod can read it from `/proc`. Both are the
standard Redis/Valkey deployment pattern; neither is a defect, but neither is a
secret store either.

### TLS material

Two mutually exclusive sources ([`TLSSpec`](api/v1/valkey_types.go)):

- `spec.tls.secretName` — a Secret the user provides (`tls.crt`, `tls.key`, `ca.crt`).
- `spec.tls.certManager` — the operator creates a **cert-manager `Certificate`**
  (`unstructured`, no typed dependency) and cert-manager issues the Secret. The
  operator never holds a private key; it mounts the Secret into the pods and reads
  it to build its own client TLS config.

Server-side settings the operator renders
([`configmap.go:80-93`](internal/builder/configmap.go)):

| Directive | Value | Meaning |
|---|---|---|
| `port 0` | when `tls.enabled` and not `allowUnencrypted` | plaintext port closed |
| `tls-port 16379` | when TLS is on | Sentinel uses 36379 |
| `tls-replication yes` | always under TLS | replication traffic is encrypted |
| `tls-auth-clients optional` | always | **client certificates are accepted, never required** — TLS gives confidentiality, not client authentication. The password is the only client authentication |
| `protected-mode no` | always | see above |

`spec.tls.allowUnencrypted: true` keeps 6379 open next to 16379, and
`spec.sentinel.allowUnencrypted` does the same for 26379 — both default `false`
and both are a deliberate downgrade for clients that cannot do TLS yet.
`spec.sentinel.disableAuth: true` removes `requirepass` from **Sentinel** while
keeping `sentinel auth-pass` toward the data nodes: anyone who can reach port
26379/36379 can then read the topology and issue Sentinel commands without a
password.

### Where credentials are *not*

- No credential is written into a ConfigMap, and the Sentinel path is the case
  worth knowing: `sentinel.conf` needs `requirepass` and `sentinel auth-pass`
  *inside the file*, so the ConfigMap carries the literal placeholder
  `%VALKEY_PASSWORD%` and the `init-sentinel-config` init container substitutes it
  from the env var into a writable copy on an `emptyDir`
  ([`internal/builder/sentinel.go:180-188,673`](internal/builder/sentinel.go)).
  The consequence to be aware of: the **rendered** file inside the Sentinel pod
  does contain the plaintext password, on an `emptyDir` that lives and dies with
  the pod, and Sentinel rewrites that file at runtime. The ConfigMap object in etcd
  never holds it. The Valkey config needs no placeholder at all —
  `GenerateValkeyConf` renders no password and `valkey-server` gets it through
  `--requirepass "$VALKEY_PASSWORD"`.
- No credential is written into the CR status or into an Event.
- The operator logs no password; it logs pod names, addresses and roles.

---

## 3. Isolation and tenancy

**What holds.**

- Every generated object carries an ownerReference to its CR, so deleting the CR
  removes the whole cluster and nothing survives except user-owned Secrets and
  PVCs. Turning `spec.persistence.enabled` off leaves them behind too: it needs
  the manual StatefulSet migration in
  [ADR 0023](docs/adr/0023-volume-claim-templates-are-immutable.md), and the
  operator never deletes a PVC — so the RDB/AOF data of a cluster that is no
  longer persistent stays on disk until someone removes the claims by hand.
- Each Valkey CR gets **its own** ServiceAccount, Role and RoleBinding
  (`<cr-name>-sidecar`), and the Role names the pods it may patch, so the blast
  radius of a stolen sidecar token is **one cluster** — not the namespace, and not
  the fleet (section 4.2).
- The observer runs under `<cr-name>-observer`, which is bound to no Role and
  mounts no token at all
  ([ADR 0012](docs/adr/0012-the-sidecar-records-its-drain-promotion-on-the-pod.md) D8 step 2).
- `spec.networkPolicy.enabled` writes ingress-only NetworkPolicies
  ([`internal/builder/networkpolicy.go`](internal/builder/networkpolicy.go)):
  the data port accepts traffic from Valkey pods, Sentinel pods, observer pods and
  **the operator namespace** (matched by `kubernetes.io/metadata.name`); the
  sidecar health port and the exporter port are open to everyone, because kubelet
  probes come from the node and Prometheus is not locatable from the CR.
- The PDB cleanup never deletes a budget it does not own (ownerReference check)
  and sends a **UID delete precondition** so a name reused between the read and
  the delete is not destroyed ([`internal/controller/pdb.go`](internal/controller/pdb.go)).
- The operator **refuses to write** the observer ServiceAccount and the sidecar
  ServiceAccount, Role and RoleBinding when a generated name is held by an object
  it does not control, and it never grants the sidecar Role to a subject it does
  not own. A collision leaves that CR unwritable and visibly blocked instead of
  handing `pods: patch` to a stranger
  ([ADR 0020](docs/adr/0020-write-only-what-the-operator-owns.md),
  [`internal/controller/foreign_object.go`](internal/controller/foreign_object.go)).
- The same refusal covers the **data and Sentinel StatefulSets and the observer
  Deployment**, and a foreign StatefulSet is treated as **absent** by every other
  consumer: it is not nudged, no rolling update deletes its pods, and its replica
  counts never enter the CR status (ADR 0020 D8). The observer Deployment cleanup
  deletes only what the CR provably owns, with a UID precondition.

**What does not hold — read this before treating a namespace as a tenant boundary.**

- **The NetworkPolicies are ingress-only.** No egress rule is written, so a
  compromised Valkey pod may open connections anywhere, including to the API
  server.
- **The sidecar can patch any metadata on its own cluster's pods.** The grant is
  no longer namespace-wide — `resourceNames` limits it to `<cr-name>-0 …
  <cr-name>-N` (section 4.2) — but within that list it is unrestricted: a
  compromised sidecar can set `instanceRole=master` on any pod of *its* cluster
  and can forge the drain stamp the operator consumes as promotion evidence.
  Nothing narrower is expressible: those are the writes the sidecar exists to
  make ([ADR 0012](docs/adr/0012-the-sidecar-records-its-drain-promotion-on-the-pod.md) D8).
- **The workload pods have no securityContext at all.** No `runAsNonRoot`, no
  `readOnlyRootFilesystem`, no `capabilities: drop [ALL]`, no
  `seccompProfile` — verified by the absence of any `SecurityContext` in
  `internal/builder`. The operator's *own* Deployment sets all four
  ([`deployment.yaml`](deploy/helm/valkey-operator/templates/deployment.yaml)); the
  clusters it creates inherit whatever the namespace's Pod Security admission
  level allows. A restricted-PSA namespace will reject these pods outright.
- **The data pod mounts the sidecar token into every container.**
  `automountServiceAccountToken` is disabled on the observer pod and nowhere else,
  so the `valkey` and `exporter` containers carry the sidecar token too. It is a
  pod-level field, so splitting it per container is not expressible in Kubernetes;
  a separate ServiceAccount per container would need a separate pod.
- **A CR author picks the image.** `spec.image` and `spec.metrics.image` are
  arbitrary strings with no registry allowlist, and the pods run with the
  namespace's default security posture.
- **Every managed object name is derived from the CR name, and the pod door is
  still open.** There is no admission webhook constraining CR names
  ([ADR 0015](docs/adr/0015-one-crd-validated-by-schema-only.md)), so whoever may
  `create valkeys` in a namespace chooses the names of that CR's derived objects.
  Since the NA62 amendment of
  [ADR 0020](docs/adr/0020-write-only-what-the-operator-owns.md) **every managed
  object family is guarded on both sides**: fourteen reconcile paths refuse to
  write an object the CR does not control, and every delete except
  `deleteLegacyServices` proves ownership and sends a UID precondition
  ([ADR 0006](docs/adr/0006-delete-only-what-the-operator-owns.md)). In particular
  the operator no longer stamps its controller ownerReference onto a ServiceMonitor
  or a cert-manager Certificate it did not verify, so a CR deletion can no longer
  garbage-collect a foreign object it adopted by name.

  Pods are covered too, since the NA63 amendment: a pod's controller is its
  StatefulSet rather than the CR, so the proof runs `pod -> StatefulSet -> CR`
  (ADR 0020 D9). That closed three unequal doors — the sidecar Role granting
  `patch` on a foreign pod, an annotation Patch onto one, and the rolling update
  reading, counting and **deleting** one. Only the first of those needed anything
  beyond the label set.

  Two gaps remain, both stated rather than fixed. **The guards protect only
  forward:** an object a *previous* release already stamped passes the ownership
  check, and nothing in the object distinguishes it from a genuine child — the same
  Update replaced its labels and wrote the operator-version annotation. Look before
  upgrading; there is no detection. And **upstream adoption bounds the pod guard:**
  the statefulset-controller adopts an orphan pod that matches its selector and
  stamps its own controller reference, so a pod built to carry a cluster's label set
  and left without a controller becomes genuinely that cluster's by Kubernetes' own
  rules. The guard closes collisions and strays, not a deliberate mimic.
- **Namespace is not a trust boundary for the operator itself.** It watches and
  writes everywhere.

---

## 4. Privilege footprint

### 4.1 The operator ClusterRole

Bound cluster-wide. Rules exactly as generated; the "so what" column is the
consequence for a compromised or misbehaving operator.

| API group | Resources | Verbs | Consequence |
|---|---|---|---|
| `vko.gtrfc.com` | `valkeys` | get, list, watch, **create**, update, patch, **delete** | Can delete any Valkey CR — and with it, by ownerReference GC, the whole cluster it describes. `create` is not needed by any reconcile path; it comes from the default marker set |
| `vko.gtrfc.com` | `valkeys/status`, `valkeys/finalizers` | get/update/patch, update | Status authority; finalizer updates |
| `""` | `configmaps`, `serviceaccounts`, `services` | get, list, watch, create, update, patch, delete | Can rewrite or delete **any** ConfigMap, ServiceAccount or Service in the cluster, not only its own. Deleting a foreign ServiceAccount invalidates its tokens |
| `""` | `pods` | get, list, watch, **delete**, patch | Can delete any pod in the cluster. This is the rolling-update primitive; it is not scoped to owned pods |
| `""` | `secrets` | get, list, watch, **delete** | **Reads every Secret in the cluster** (the heaviest confidentiality exposure) and can destroy any of them. `delete` exists for one caller: the legacy `<name>-sentinel-tls` cleanup on the `unifiedCertificate` migration. That caller no longer deletes on the name alone — it requires either a Certificate this Valkey controls issuing into that name, or cert-manager's `cert-manager.io/certificate-name` annotation, plus `type: kubernetes.io/tls`, plus a UID precondition ([ADR 0006](docs/adr/0006-delete-only-what-the-operator-owns.md)). The *grant* is still cluster-wide, so a compromised operator is unaffected by that guard |
| `""` + `events.k8s.io` | `events` | create, patch | Can write Events anywhere. Both groups are listed because the operator records through `events.k8s.io/v1` while older tooling still reads the core group ([ADR 0014](docs/adr/0014-rbac-lives-in-three-places.md)) |
| `apps` | `deployments`, `statefulsets` | get, list, watch, create, update, patch, delete | Can replace the pod template — hence the image, hence the code — of **any** Deployment or StatefulSet in the cluster |
| `cert-manager.io` | `certificates` | full CRUD | Can request certificates from any Issuer/ClusterIssuer the namespace can reference, and delete existing ones. The legacy-Sentinel cleanup deletes only Certificates this Valkey controls by ownerReference ([ADR 0006](docs/adr/0006-delete-only-what-the-operator-owns.md)); no other path deletes a Certificate |
| `monitoring.coreos.com` | `servicemonitors` | full CRUD | Scrape configuration; used only when `spec.metrics.serviceMonitor.enabled` |
| `networking.k8s.io` | `networkpolicies` | full CRUD | **Can delete any NetworkPolicy in the cluster**, including policies that protect unrelated workloads |
| `policy` | `poddisruptionbudgets` | full CRUD | Availability guarantees of any workload can be removed or tightened |
| `rbac.authorization.k8s.io` | `roles` | get, list, watch, create, update, patch, delete, **escalate**, **bind** | **The privilege ceiling.** `escalate` lifts the rule that a principal may only grant permissions it holds, so the operator can write a Role containing *any* namespaced permission, in *any* namespace |
| `rbac.authorization.k8s.io` | `rolebindings` | get, list, watch, create, update, patch, delete | Together with the row above and `serviceaccounts` create: **create SA → write Role → bind → use.** A compromised operator is equivalent to namespaced admin in every namespace |
| `coordination.k8s.io` | `leases` | full CRUD | **Chart only**, not in the generated role — leader election (`--leader-elect`, off unless `leaderElection.enabled`). Legal drift: the drift guard checks containment, not equality ([`rbac_drift_test.go`](internal/controller/rbac_drift_test.go)) |

**The honest summary of that table:** the operator is not a namespaced workload
manager with a few extra rights. Between `roles/escalate` + `rolebindings` +
`serviceaccounts` and `secrets: get,list`, it is a cluster-wide privileged
component. Treat access to its ServiceAccount token, its image and its CR API the
way you treat access to a cluster-admin credential.

`escalate` and `bind` are not gratuitous: without them the API server refuses to
let the operator create the `<cr-name>-sidecar` Role, since a principal may not
grant permissions it does not itself hold — but it does hold `pods: patch`
cluster-wide, and the sidecar Role is now a strict subset of that (`patch` on named
pods), so the narrower alternative (dropping `escalate`) is worth testing.
**Not verified:** whether the sidecar Role can in fact be created without
`escalate` on this Kubernetes version.

### 4.2 The per-instance sidecar Role

```yaml
# internal/builder/rbac.go — BuildSidecarRole
rules:
  - apiGroups: [""]
    resources: ["pods"]
    verbs: ["patch"]
    resourceNames: ["<cr-name>-0", "<cr-name>-1", "<cr-name>-2"]   # example: replicas 3
```

What the sidecar actually calls: **`Pods(ns).Patch` and nothing else.** Verified
by grep over `internal/sidecar` and `cmd/sidecar` — the only clientset call site is
`patchMetadata` ([`internal/sidecar/labeler.go`](internal/sidecar/labeler.go)),
used by `PatchLabel` (own pod, `instanceRole`) and `PatchAnnotation` (the peer pod
the drain handler promoted). The grant matches that exactly: one verb, and only the
pods of this cluster. `TestBuildSidecarRole` pins verb set and name list together,
and the operator rewrites the Role on every reconcile, so existing clusters narrow
on their next pass with no migration step.

**How the name list is built** ([`SidecarRolePodNames`](internal/builder/rbac.go)):
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
([ADR 0012](docs/adr/0012-the-sidecar-records-its-drain-promotion-on-the-pod.md) D8,
Residual risks).

The observer no longer shares this ServiceAccount: it runs under `<cr-name>-observer`,
which is bound to no Role, and its pod sets `automountServiceAccountToken: false`, so
it mounts no token to steal.

### 4.3 The pre-upgrade hook

`<release>-upgrade` gets `valkeys: get,list,patch,update` and
`customresourcedefinitions: get,list,patch,update`, cluster-wide, for the duration
of the Job. `patch`/`update` on CRDs is a **cluster-wide schema-change grant**: a
compromised hook image could rewrite the schema or the conversion strategy of any
CRD in the cluster. It is disabled with `preUpgradeHook.enabled: false`, at the
cost of the field-default migration it performs
([`cmd/migrate`](cmd/migrate/migrate.go)).

### 4.4 Operator process posture

From [`deployment.yaml`](deploy/helm/valkey-operator/templates/deployment.yaml),
verified: `runAsNonRoot: true`, `seccompProfile: RuntimeDefault`,
`allowPrivilegeEscalation: false`, `readOnlyRootFilesystem: true`,
`capabilities: drop [ALL]`, `terminationGracePeriodSeconds: 10`. Ports 8080
(metrics) and 8081 (health), both plain HTTP and unauthenticated.

**What `:8080` discloses.** Besides controller-runtime's own counters it serves one
set of `vko_valkey_*` series per `Valkey` resource
([`internal/metrics/collector.go`](internal/metrics/collector.go),
[ADR 0021](docs/adr/0021-per-resource-metrics-and-the-alert-that-was-missing.md)):
namespace, resource name, phase, condition types with their reasons, replica
counts, and the operator version that last wrote status. That is an inventory of
the fleet and its health. It carries **no Secret material and no spec contents** —
no password, no image, no host, no TLS material.

The chart can render an optional `<release>-metrics` Service and ServiceMonitor for
this endpoint (`metrics.service.enabled`, `metrics.serviceMonitor.enabled`, both
default `false`). Neither changes reachability: the container port is declared with
or without them, so anything that can route to the operator pod already reads
`:8080`. What they add is a stable name and a scrape target.

---

## 5. Validation story

There is **no admission webhook** in this project — no `ValidatingWebhookConfiguration`,
no `MutatingWebhookConfiguration`, nothing under `config/webhook`. Everything that
validates a `Valkey` object is CRD schema validation generated from the kubebuilder
markers in [`api/v1/valkey_types.go`](api/v1/valkey_types.go): enums
(`certManager.issuer.kind` ∈ {Issuer, ClusterIssuer}, `observer.logLevel`),
defaults (`auth.secretPasswordKey: password`, `podDisruptionBudget.enabled: false`,
`tls.enabled: false`), types and required fields.

What that means in practice:

- **Cross-field rules are not enforced at admission.** `spec.tls.secretName` and
  `spec.tls.certManager` are documented as mutually exclusive; nothing rejects a
  CR that sets both. The reconciler resolves it, the API server does not.
- **A rejected CR write is a first-class runtime state, not an error path.** A
  third-party fail-closed webhook (Kyverno, OPA) that rejects the operator's writes
  is surfaced on the CR as the `ReconcileBlocked` condition with the rejecting
  webhook named in the message — the whole reason this ticket family exists.
- **The operator validates the data plane, not the input.** Its checks are about
  reachability, replication role and sync state; it trusts the CR.

---

## 6. Rotation and change propagation

| Change | Propagates? | Mechanism |
|---|---|---|
| `spec.image`, resources, probes, config | Yes | Pod-spec hash / config hash on the pod template, failover-aware rolling update ([`ComputePodSpecHash`](internal/builder/statefulset.go), `ComputeConfigHash`) |
| cert-manager certificate renewal | Partially | The Secret content changes and the mount follows it; **whether the running `valkey-server` reloads the new material is not verified in this repo** |
| `spec.tls.secretName` (different Secret) | Yes | The name is part of the pod spec, so the hash changes and the pods roll |
| **Password change inside the auth Secret** | **No** | See below |
| `spec.auth.secretName` (different Secret) | Yes | Same reason as the TLS Secret name |

**The password rotation gap, stated precisely.** The Secret is watched
([`findValkeyForSecret`, `valkey_controller.go:1919`](internal/controller/valkey_controller.go))
and a change does enqueue a reconcile — but the password reaches the pods as an
`env.valueFrom.secretKeyRef`, which Kubernetes resolves **once, at pod start**, and
the pod-spec hash covers the *reference*, not the value. So after `kubectl edit
secret`:

1. Running pods keep serving with the **old** password; no rolling update is
   triggered.
2. The operator re-reads the Secret on its next pass and starts authenticating
   with the **new** password — against pods that still expect the old one. Its
   health checks and any `REPLICAOF` it needs to send begin to fail.
3. The cluster converges only when every pod is restarted manually.

Rotating a password today therefore means: change the Secret, then roll the pods
yourself (replicas first, master last), accepting that a cluster without
persistence loses in-memory data if it has no failover target. Automatic
propagation without data loss is an open product wish
(`.github/idea.md`), not an implemented feature.

---

## 7. Backup and restore

Restore is the one legitimate operation that makes the operator's own objects
look foreign to it, so it gets its own section. The ownership guards
([ADR 0020](docs/adr/0020-write-only-what-the-operator-owns.md),
[ADR 0006](docs/adr/0006-delete-only-what-the-operator-owns.md)) prove provenance
through the controller ownerReference's **UID**, and a restore is precisely the
operation that changes UIDs.

### What a naive full-namespace restore does

A backup tool that restores the CR *and* the operator-managed children (Velero
restores objects with their backed-up `ownerReferences` but necessarily new UIDs)
produces this sequence:

1. The restored Valkey CR has a **new UID**. Every restored child still carries a
   controller ownerReference pointing at the **old** UID.
2. Every guard refuses correctly — these objects are genuinely not controlled by
   the live CR. The pass reports `ReconcileBlocked` with reason `ForeignObject`
   plus one Warning Event per colliding kind
   ([`internal/controller/foreign_object.go`](internal/controller/foreign_object.go)),
   and rechecks every 30 seconds.
3. The Kubernetes garbage collector resolves owner references by UID, treats an
   owner that cannot be verified as absent, and **deletes the restored children**.
   *Upstream behaviour, read from the garbage-collection contract
   ([Kubernetes docs](https://kubernetes.io/docs/concepts/architecture/garbage-collection/));
   not reproduced in this repo — envtest runs no garbage collector (same caveat
   as in ADR 0020's residual risks).*
4. The operator recreates every child with correct ownerReferences on the new
   CR UID, and the guards pass again.

So a full restore **converges on its own**, but through a delete-and-rebuild
window in which the restored pods are removed and recreated. It is churn, not a
dead end.

### The supported restore path: restore state, not derived objects

Everything the operator creates is derived, deterministic state. Only three
things in a namespace are not derivable and are what a backup must carry:

| What | Why it must be in the backup | How it survives the guards |
|---|---|---|
| The **Valkey CR** | Carries the spec and the operator's recorded facts as metadata annotations: `vko.gtrfc.com/known-master` ([`internal/builder/sentinel.go`](internal/builder/sentinel.go), [ADR 0008](docs/adr/0008-known-master-annotation-is-the-recorded-authority.md)) and the rolling-update state family ([`internal/controller/rolling_update.go`](internal/controller/rolling_update.go)) | It *is* the owner; guards do not apply to it |
| The **auth Secret** (`spec.auth.secretName`) | User-provided; the operator only reads it and never stamps an ownerReference on it | No guard touches it — it was never operator-owned |
| The **PVCs** | The data. Created by the statefulset-controller from `volumeClaimTemplates` ([`internal/builder/statefulset.go`](internal/builder/statefulset.go)); the operator never writes or deletes a PVC and sets no `persistentVolumeClaimRetentionPolicy`, so they carry no ownerReference to anything | The rebuilt StatefulSet's pods rebind them by name |

Everything else — StatefulSets, Services, ConfigMaps, NetworkPolicies, the
sidecar ServiceAccount/Role/RoleBinding, observer Deployment, ServiceMonitor,
Certificates — should be **excluded from the restore**. The operator rebuilds
all of it from the CR on the first pass, with correct ownership. Under
cert-manager, the rebuilt Certificates make cert-manager reissue the TLS
Secrets; a user-provided TLS Secret (`spec.tls.secretName`) is user state and
belongs in the backup like the auth Secret.

A CR restored mid-rolling-update carries stale rolling-update annotations. That
is survivable by design: every rolling-update wait is bounded and expiry hands
over to another bounded state
([ADR 0010](docs/adr/0010-every-rolling-update-wait-is-bounded.md)), so a stale
state machine expires instead of wedging.

*Not verified: no restore of any kind has been exercised against a real cluster
from this repository. The convergence claim in step 3–4 above rests on the
upstream garbage-collector contract plus the operator behaviour the tests do
prove (refusal, recheck, rebuild-on-absence).*

### Why the operator does not honor `velero.io/restore-name` as adoption evidence

Velero labels every restored object with `velero.io/backup-name` and
`velero.io/restore-name`
([Velero restore reference](https://velero.io/docs/main/restore-reference/)).
It is tempting to treat these as proof that a foreign-looking object is a
restored child and adopt it. The operator deliberately does not: **a label is
writable by anyone who can create the object**, so a label-gated adoption path
would hand the exact capability ADR 0020 closed — a principal with `create` on
a kind in the namespace could stamp the two Velero labels on a colliding object
and have the operator adopt, overwrite and eventually garbage-collect it. The
adoption question and the alternative that was considered live in
[ADR 0020's residual risks](docs/adr/0020-write-only-what-the-operator-owns.md#residual-risks).

---

## 8. How to report a vulnerability

This repository has **no `SECURITY.md`** and no published contact address yet —
stated rather than invented.

Until one exists, report privately through **GitHub private vulnerability
reporting** on <https://github.com/guided-traffic/valkey-operator> (Security →
Report a vulnerability), or to the maintainer organisation
<https://github.com/guided-traffic>. Please do **not** open a public issue for a
finding that lets someone read a Secret, escalate RBAC, or destroy data. Include
the operator version (`app.kubernetes.io/version` on the operator pod), the chart
version, and whether TLS and auth were enabled.

---

## 9. Residual risks — hardening checklist

Ordered by what a compromise buys an attacker, not by effort. Every item is
verified against this repository; the ticket item, where one exists, holds the
analysis.

- [ ] **Scope the operator away from `secrets: get,list` on everything.** It needs
      the auth Secret and the TLS Secret of the namespaces it serves, not the
      cluster's Secrets. Options: a namespaced Role per watched namespace, or a
      cache filtered by label with the ClusterRole narrowed to match. Cost: the
      operator stops being install-and-forget for new namespaces.
- [ ] **Re-examine `roles: escalate` + `rolebindings` + `serviceaccounts: create`.**
      That triple is namespaced admin everywhere. Test whether the sidecar Role can
      be created without `escalate` now that it is a strict subset of the
      operator's own pod grant (section 4.1) — and if so, drop the verb.
- [x] **Gate the legacy Sentinel TLS Secret delete on provenance
      ([ADR 0006](docs/adr/0006-delete-only-what-the-operator-owns.md), done 2026-08-21).** It used
      to delete by name, with no ownerReference check and no UID precondition —
      the opposite of the rule the PDB cleanup enforces. The Secret now needs a
      Certificate this Valkey controls or cert-manager's provenance annotation,
      and the Certificate beside it needs the ownerReference; both deletes carry a
      UID precondition and every refusal records a Warning. This bounds what the
      *reconcile path* touches; narrowing the cluster-wide `secrets` grant itself
      is the separate item above.
- [x] **Narrow the sidecar Role to `patch` with `resourceNames`
      ([ADR 0012](docs/adr/0012-the-sidecar-records-its-drain-promotion-on-the-pod.md) D8, done
      2026-08-21).** The unused `get`/`list` went first — `resourceNames` and `list`
      are incompatible — and the grant now names this cluster's own data pods
      (section 4.2). That removes the cross-cluster write, which mattered because
      the drain stamp is evidence for a destructive `REPLICAOF`. What remains is
      inherent: the sidecar can still forge that evidence for its *own* cluster.
- [x] **Refuse the sidecar grant when the name is held by a foreign object
      ([ADR 0020](docs/adr/0020-write-only-what-the-operator-owns.md), done 2026-08-21).**
      `BuildSidecarRoleBinding` names its subject by name, without a UID, so the
      Role above was granted to whatever identity held `<cr-name>-sidecar` —
      created by the operator or not. The ServiceAccount, Role and RoleBinding now
      each require the controller ownerReference, and a refusal on any of them
      stops the binding, fails the pass and reports `ReconcileBlocked/ForeignObject`
      with the colliding name. The same change stopped both ServiceAccount
      reconcilers from erasing a target's annotations, and the observer refusal
      keeps the Deployment running because that identity grants it nothing.
- [x] **Refuse to write the data and Sentinel StatefulSets and the observer
      Deployment onto a foreign object
      ([ADR 0020](docs/adr/0020-write-only-what-the-operator-owns.md) D8, done 2026-08-22).**
      The data StatefulSet carries the bare CR name — the likeliest name for an
      accidental or aimed collision — and the Update installs the pod template
      into whatever holds it. Both StatefulSet writes now refuse and fail the
      pass; every other consumer treats a foreign StatefulSet as absent, so it is
      neither nudged nor rolled nor counted; the observer Deployment refuses
      without failing and its cleanup deletes only with provenance plus a UID
      precondition. Operator upgrades are unaffected: every release since the
      first commit stamps the controller reference on create.
- [x] **Guard the last five write paths and stop stamping an ownerReference onto
      an unverified object (NA62)**
      ([ADR 0020](docs/adr/0020-write-only-what-the-operator-owns.md) D1, D2, D8,
      done 2026-08-22). `reconcileServiceMonitor` and `reconcileCertificate` wrote
      this CR's controller ownerReference onto whatever object held the derived
      name, so the CR deletion garbage-collected it — and the same branch rewrote a
      foreign Certificate's `secretName` and `issuerRef`, which costs the other
      party their Secret without waiting for any deletion. Both refuse now, as do
      `reconcileService`, the three ConfigMap reconcilers and
      `reconcileNetworkPolicy`. `replicaConfigMaster` treats a foreign replica
      ConfigMap as absent, so a stranger's `replicaof` directive can no longer feed
      the master authority.
- [x] **Stop deleting by name when a feature flag is switched off (NA62)**
      ([ADR 0006](docs/adr/0006-delete-only-what-the-operator-owns.md) D2, D8, done
      2026-08-22). `cleanupMetricsService`, `cleanupServiceMonitor` and the
      NetworkPolicy half of `cleanupObserverDeployment` deleted whatever held the
      derived name; the trigger was one boolean in a CR its author controls. All
      three prove ownership and send the UID precondition now.
- [ ] **Before upgrading, look for objects an earlier release already adopted.**
      The NA62 guard is not retroactive. A ServiceMonitor or cert-manager
      Certificate that collided with a derived name under an earlier release
      carries this CR's controller ownerReference today, and deleting the CR will
      garbage-collect it. No field distinguishes such an object from a genuine
      child, so this cannot be automated: compare the ServiceMonitors and
      Certificates under `<cr>` names against what you expect the operator to have
      created, in every namespace that runs a Valkey.
- [x] **Verify pod provenance before touching, granting on, or deleting a pod
      (NA63)** ([ADR 0020](docs/adr/0020-write-only-what-the-operator-owns.md) D9,
      done 2026-08-22). Filed as the two steady-state command paths; the audit found
      three doors and the filed one was the most expensive to use, since it needs the
      label set, a per-pod headless DNS record and the CR password. The two cheaper
      ones needed only labels: `clearDrainStamps` **patched** every label-matching
      pod, and `listDataPodNames` put them into the `resourceNames` of the sidecar
      Role, handing this cluster's sidecar token `patch` on a stranger's pod. The
      destructive one was the rolling update, which reads pods by generated name and
      deletes them at six call sites — the NA61 StatefulSet guard did not cover it,
      because a StatefulSet can be provably ours while the pod under `<cr>-N` is not.
      All are guarded now, and the six deletes carry the UID precondition.
- [ ] **Give the workload pods a securityContext.** `runAsNonRoot`,
      `readOnlyRootFilesystem` where the data path allows it, `drop: [ALL]`,
      `seccompProfile: RuntimeDefault` — the operator already runs that way itself.
      Without it the clusters cannot be admitted into a `restricted` PSA namespace.
- [x] **Give the observer its own ServiceAccount and stop mounting its token**
      ([ADR 0012](docs/adr/0012-the-sidecar-records-its-drain-promotion-on-the-pod.md) D8 step 2,
      done 2026-08-21). `<cr-name>-observer` is bound to no Role and the pod sets
      `automountServiceAccountToken: false`.
- [ ] **Stop mounting the sidecar token into the `valkey` and `exporter`
      containers.** `automountServiceAccountToken` is a pod-level field, so the
      only way to give the sidecar a token the other containers do not have is to
      move it out of the pod — a design change, not a flag.
- [ ] **Add egress NetworkPolicies.** Today's policies are ingress-only, so a
      compromised data pod can talk to anything, the API server included.
- [ ] **Require client certificates where the deployment can.**
      `tls-auth-clients optional` means TLS authenticates the server only.
- [ ] **Do not leave `spec.sentinel.disableAuth` or either `allowUnencrypted` on
      after the migration that needed them.**
- [ ] **Treat the operator metrics endpoint as public unless moved or disabled,
      and know that it now names every Valkey resource.** By default it binds
      `:8080` in plain HTTP with no authentication filter, and since
      [ADR 0021](docs/adr/0021-per-resource-metrics-and-the-alert-that-was-missing.md)
      the payload is an inventory of the fleet and its health (section 4.4), not
      only controller-runtime counters. `--metrics-bind-address` is applied since
      the ADR 0018 D8 fix, so the endpoint can be moved or switched off (`=0`) from
      the chart; wherever it binds it stays unauthenticated — the filter is a
      separate trade ([ADR 0018](docs/adr/0018-metrics-and-the-exporter-sidecar.md)
      D9/D10). A NetworkPolicy for the operator namespace is the only control that
      works without changing either.
- [ ] **Restrict who may `create valkeys`.** A CR author chooses the image the
      cluster runs and the name every generated object gets, and generated names
      collide with existing objects by design.
      [ADR 0006](docs/adr/0006-delete-only-what-the-operator-owns.md) closed the
      deletes and [ADR 0020](docs/adr/0020-write-only-what-the-operator-owns.md)
      closed every write, so a collision is now refused and reported rather than
      acted on. What the guards do **not** undo is the image choice, the objects an
      earlier release already adopted, or the pod door (section 3). The CR-name
      grant stays the control that bounds all three, and any new write or delete by
      generated name needs the same provenance discipline.
- [ ] **Disable the pre-upgrade hook (`preUpgradeHook.enabled: false`) unless a
      migration needs it**, or accept a cluster-wide CRD write grant during every
      upgrade.
