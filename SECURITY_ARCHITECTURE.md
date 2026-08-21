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
| **Sidecar** | ServiceAccount `<cr-name>-sidecar`, one per Valkey CR ([`BuildSidecarServiceAccount`](internal/builder/rbac.go)) | **One namespace**, `pods` patch on *all* pods in it | Patching `instanceRole` on its own pod and the drain stamp on a peer pod |
| **Observer** | The **same** `<cr-name>-sidecar` ServiceAccount ([`internal/builder/observer.go:113`](internal/builder/observer.go)) | Same namespaced grant | Nothing — it makes no Kubernetes API call at all (verified: no `client-go` import in `internal/observer` or `cmd/observer`) |
| **Valkey pods** | Same `<cr-name>-sidecar` ServiceAccount — the whole pod, so the `valkey`, `sidecar` and `exporter` containers all carry the token ([`statefulset.go:547`](internal/builder/statefulset.go)) | Same | The `valkey` process itself needs no API access; only the sidecar container uses the token |
| **Sentinel pods** | The namespace `default` ServiceAccount ([`sentinel.go:368`](internal/builder/sentinel.go)) | Whatever `default` is bound to (nothing, in a stock cluster) | Nothing — Sentinel pods carry no labeler sidecar |
| **CR author** | Any principal with `create valkeys` in a namespace | That namespace | Chooses images, the auth Secret name, the TLS mode — see section 3 for what that buys them |

```
        cluster scope                          namespace scope
  ┌───────────────────────────┐        ┌──────────────────────────────────┐
  │  ClusterRole              │        │  Role <cr>-sidecar               │
  │  valkey-operator-role     │        │  pods: get,list,patch            │
  └───────────┬───────────────┘        └──────────────┬───────────────────┘
              │ ClusterRoleBinding                    │ RoleBinding
              ▼                                       ▼
  ┌───────────────────────────┐        ┌──────────────────────────────────┐
  │  SA <release>             │        │  SA <cr>-sidecar                 │
  │  operator Deployment      │        │  ├── valkey pod                  │
  │  (release namespace)      │        │  │   (valkey+sidecar+exporter)   │
  └───────────┬───────────────┘        │  └── observer Deployment         │
              │                        │  (sentinel pods use `default`)   │
              │                        └──────────────┬───────────────────┘
              │ creates + owns                        │
              ▼                                       │ patches
  StatefulSets, Deployments, Services, ConfigMaps,    │  metadata.labels
  SA/Role/RoleBinding, NetworkPolicies, PDBs,         │  metadata.annotations
  cert-manager Certificates, ServiceMonitors  ────────┘  of pods in the namespace
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
   namespace can therefore influence a topology decision — see section 8.

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
| observer | same env var | [`internal/builder/observer.go:222`](internal/builder/observer.go) |
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
  PVCs.
- Each Valkey CR gets **its own** ServiceAccount, Role and RoleBinding
  (`<cr-name>-sidecar`), so one cluster's sidecar credential is not another
  cluster's credential — the blast radius of a stolen sidecar token is one
  namespace, not the fleet.
- `spec.networkPolicy.enabled` writes ingress-only NetworkPolicies
  ([`internal/builder/networkpolicy.go`](internal/builder/networkpolicy.go)):
  the data port accepts traffic from Valkey pods, Sentinel pods, observer pods and
  **the operator namespace** (matched by `kubernetes.io/metadata.name`); the
  sidecar health port and the exporter port are open to everyone, because kubelet
  probes come from the node and Prometheus is not locatable from the CR.
- The PDB cleanup never deletes a budget it does not own (ownerReference check)
  and sends a **UID delete precondition** so a name reused between the read and
  the delete is not destroyed ([`internal/controller/pdb.go`](internal/controller/pdb.go)).

**What does not hold — read this before treating a namespace as a tenant boundary.**

- **The NetworkPolicies are ingress-only.** No egress rule is written, so a
  compromised Valkey pod may open connections anywhere, including to the API
  server.
- **The Role is namespace-wide, not pod-wide.** `<cr-name>-sidecar` grants
  `pods: patch` on **every pod in the namespace** with no `resourceNames`, so
  cluster A's sidecar can patch cluster B's pods — and the label it patches is
  the one the `-rw` Service selects on, while the annotation it patches steers a
  topology decision. The unused `get`/`list` were dropped (D8 step 1); the
  remaining narrowing steps and their ordering are
  [ADR 0012](docs/adr/0012-the-sidecar-records-its-drain-promotion-on-the-pod.md) D8.
- **The workload pods have no securityContext at all.** No `runAsNonRoot`, no
  `readOnlyRootFilesystem`, no `capabilities: drop [ALL]`, no
  `seccompProfile` — verified by the absence of any `SecurityContext` in
  `internal/builder`. The operator's *own* Deployment sets all four
  ([`deployment.yaml`](deploy/helm/valkey-operator/templates/deployment.yaml)); the
  clusters it creates inherit whatever the namespace's Pod Security admission
  level allows. A restricted-PSA namespace will reject these pods outright.
- **`automountServiceAccountToken` is never disabled**, so the sidecar token is
  mounted into the `valkey` container as well, not only into the sidecar.
- **A CR author picks the image.** `spec.image` and `spec.metrics.image` are
  arbitrary strings with no registry allowlist, and the pods run with the
  namespace's default security posture.
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
grant permissions it does not itself hold — but it does hold `pods: get,list,patch`,
so the narrower alternative (dropping `escalate` and keeping the sidecar Role a
subset of the operator's own grants) is worth testing. **Not verified:** whether
the current sidecar Role can in fact be created without `escalate` on this
Kubernetes version.

### 4.2 The per-instance sidecar Role

```yaml
# internal/builder/rbac.go — BuildSidecarRole
rules:
  - apiGroups: [""]
    resources: ["pods"]
    verbs: ["patch"]     # no resourceNames
```

What the sidecar actually calls: **`Pods(ns).Patch` and nothing else.** Verified
by grep over `internal/sidecar` and `cmd/sidecar` — the only clientset call site is
`patchMetadata` ([`internal/sidecar/labeler.go`](internal/sidecar/labeler.go)),
used by `PatchLabel` (own pod, `instanceRole`) and `PatchAnnotation` (the peer pod
the drain handler promoted). The grant matches that exactly since the unused
`get`/`list` were dropped; `TestBuildSidecarRole` pins the verb set, and the
operator rewrites the Role on every reconcile, so existing clusters narrow on
their next pass. The observer, which shares this ServiceAccount, calls nothing.

Two writes reach the operator's decisions through this grant:

- `instanceRole=master|replica|draining` — the label the `-rw` / `-r` Services
  select on, i.e. **where client writes go**.
- `vko.gtrfc.com/drain-promoted-at` — the drain stamp the operator accepts as
  evidence that a promotion it did not perform was legitimate, and on which it
  will demote other masters (`REPLICAOF`, destructive).

Narrowing further to `resourceNames` limited to the cluster's own pods — which
is what removes the cross-cluster patch — is
[ADR 0012](docs/adr/0012-the-sidecar-records-its-drain-promotion-on-the-pod.md) D8
steps 2 and 3, still open.

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

## 7. How to report a vulnerability

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

## 8. Residual risks — hardening checklist

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
- [ ] **Narrow the sidecar Role to `patch` with `resourceNames`
      ([ADR 0012](docs/adr/0012-the-sidecar-records-its-drain-promotion-on-the-pod.md) D8).** Dropping the unused
      `list` verb is the precondition — `resourceNames` and `list` are
      incompatible. Matters more since the drain stamp became evidence for a
      destructive `REPLICAOF`.
- [ ] **Give the workload pods a securityContext.** `runAsNonRoot`,
      `readOnlyRootFilesystem` where the data path allows it, `drop: [ALL]`,
      `seccompProfile: RuntimeDefault` — the operator already runs that way itself.
      Without it the clusters cannot be admitted into a `restricted` PSA namespace.
- [ ] **Set `automountServiceAccountToken: false` where the token is not needed**,
      and consider a separate ServiceAccount for the observer, which calls no API
      at all.
- [ ] **Add egress NetworkPolicies.** Today's policies are ingress-only, so a
      compromised data pod can talk to anything, the API server included.
- [ ] **Require client certificates where the deployment can.**
      `tls-auth-clients optional` means TLS authenticates the server only.
- [ ] **Do not leave `spec.sentinel.disableAuth` or either `allowUnencrypted` on
      after the migration that needed them.**
- [ ] **Treat the operator metrics endpoint as public unless moved or disabled.**
      By default it binds `:8080` in plain HTTP with no authentication filter.
      `--metrics-bind-address` is applied since the ADR 0018 D8 fix, so the
      endpoint can be moved or switched off (`=0`) from the chart; wherever it
      binds it stays unauthenticated — the filter is a separate trade
      ([ADR 0018](docs/adr/0018-metrics-and-the-exporter-sidecar.md) D9/D10).
- [ ] **Restrict who may `create valkeys`.** A CR author chooses the image the
      cluster runs and the name every generated object gets, and generated names
      collide with existing objects by design. [ADR 0006](docs/adr/0006-delete-only-what-the-operator-owns.md)
      closed the one path where such a collision was destructive; the general
      property — an attacker-chosen CR name drives every generated name — remains,
      so any new delete-by-generated-name needs the same provenance discipline.
- [ ] **Disable the pre-upgrade hook (`preUpgradeHook.enabled: false`) unless a
      migration needs it**, or accept a cluster-wide CRD write grant during every
      upgrade.
