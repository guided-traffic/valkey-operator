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
[ADR 0016](docs/adr/0016-authentication-and-tls-posture.md) (auth and TLS),
[ADR 0006](docs/adr/0006-delete-only-what-the-operator-owns.md) (the delete guards),
[ADR 0032](docs/adr/0032-generated-pods-run-rootless.md) (the workload pod posture) and
[ADR 0033](docs/adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md)
(the seccomp choice, the opt-in user namespace and the operator's own pod posture).
A `DEVELOPER.md` does not exist yet.

---

## 1. Roles and trust boundaries

| Principal | Identity | Scope | Trusted with |
|---|---|---|---|
| **Operator manager** | ServiceAccount `<release>` in the release namespace, bound by a **ClusterRoleBinding** ([`clusterrolebinding.yaml`](deploy/helm/valkey-operator/templates/clusterrolebinding.yaml)) | **Cluster-wide, all namespaces** | Every rule in section 4. Reads every Secret in the cluster, writes RBAC, deletes pods and Secrets |
| **Pre-upgrade hook** | ServiceAccount `<release>-upgrade`, cluster-wide, created and deleted per `helm upgrade` ([`pre-upgrade-rbac.yaml`](deploy/helm/valkey-operator/templates/pre-upgrade-rbac.yaml)) | Cluster-wide, lifetime of the hook Job | `valkeys` get/list/patch/update and `customresourcedefinitions` get/list/patch/update |
| **Sidecar** | ServiceAccount `<cr-name>-sidecar`, one per Valkey CR ([`BuildSidecarServiceAccount`](internal/builder/rbac.go)) | **This cluster's own data pods**, by name: `pods` patch with `resourceNames` (section 4.2) | Patching `instanceRole` on its own pod and the drain stamp on a peer pod |
| **Observer** | Its **own** ServiceAccount `<cr-name>-observer`, bound to no Role, with `automountServiceAccountToken: false` ([`BuildObserverServiceAccount`](internal/builder/observer.go)) | None — no Role, and no token mounted | Nothing — it makes no Kubernetes API call at all (verified: no `client-go` import in `internal/observer` or `cmd/observer`) |
| **Valkey pods** | The pod runs as `<cr-name>-sidecar`, but the token reaches the **`sidecar` container only**: `automountServiceAccountToken: false` plus a projected volume mounted into that one container ([`sidecarTokenVolume`](internal/builder/statefulset.go)) | Same as the sidecar row, for that container | `valkey`, `exporter` and every init container hold no Kubernetes credential (the cluster password is section 2); the migration-only root repair (section 4.5) holds neither — no token and no environment |
| **Sentinel pods** | The namespace `default` ServiceAccount, with `automountServiceAccountToken: false` ([`sentinel.go`](internal/builder/sentinel.go)) | None — no token mounted | Nothing — Sentinel pods carry no labeler sidecar and never call the Kubernetes API |
| **CR author** | Any principal with `create valkeys` in a namespace | That namespace | Chooses images, the auth Secret name, the TLS mode, and since 2026-09-26 the pods' seccomp profile ~~among those on the nodes~~ among the `Localhost` profiles the operator's allow-list names, none by default *(amended 2026-09-26, ADR 0033 D9)*, and whether they run in a user namespace — see section 3 for what that buys them |

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
container, so any process in that container can read it from `/proc` — not one in
another container of the pod, because no generated pod shares its process namespace
(section 4.5). Both are the
standard Redis/Valkey deployment pattern; neither is a defect, but neither is a
secret store either.

### TLS material

Two mutually exclusive sources ([`TLSSpec`](api/v1/valkey_types.go)):

- `spec.tls.secretName` — a Secret the user provides (`tls.crt`, `tls.key`, `ca.crt`).
- `spec.tls.certManager` — the operator creates a **cert-manager `Certificate`**
  (`unstructured`, no typed dependency) and cert-manager issues the Secret. The
  operator never writes a private key and never persists one of its own. It does **hold**
  them: the manager cache backs an unfiltered Secret informer, so every watched Secret —
  `tls.key` and every cluster password included — is resident in operator memory for the
  process lifetime. That is not new with the fingerprint, and it is what the `secrets` scope
  item on the hardening checklist is about.

**Two different consumers read that Secret, and they read different parts of it.**

| Consumer | Reads | Why |
|---|---|---|
| the reconciler's and the health checker's own client config | `ca.crt` only | they verify the server and present **no client certificate**, so a rotation never breaks them |
| the material fingerprint (`ComputeTLSMaterialHash`) | `ca.crt`, `tls.crt`, `tls.key` | it has to notice that the *content* changed, which is what triggers the roll |

The fingerprint is a 32-bit FNV-1a digest, carried as the `VKO_TLS_MATERIAL_HASH`
environment variable of the sidecar container on the data tier and the sentinel
container on the Sentinel tier, on both StatefulSet pod templates, and therefore
**readable by anyone with `get pods` or `get statefulsets`**. It is derived from a
private key, which is high-entropy and not guessable, so the digest confirms nothing
an attacker does not already hold.

It sits in the pod **spec** rather than in pod metadata since 2026-08-27, because
metadata is patchable by anything holding the sidecar token and spec is not — and the
cheap attack was never forging the value but *deleting* it: both consumers skip a pod
that carries no record, so one merge patch setting the key to `null` switched the roll
off. The superseded `vko.gtrfc.com/tls-material-hash` annotation is still read for pods
written before that date and is never written again
([ADR 0031](docs/adr/0031-a-record-the-operator-trusts-lives-in-pod-spec.md)).

The same construction over the **password** would be a brute-forceable oracle, and
[ADR 0030](docs/adr/0030-rotating-certificates-rotate-the-instances-that-cannot-reload-them.md)
D11 refuses it there — **at any digest strength**, because what makes the TLS case safe is that
nobody enumerates 2048-bit RSA keys, not that FNV-1a is narrow. A wider hash of a password is a
marginally slower oracle, not a safe one.

**It is a change detector, not an integrity control, and must not be read as one.**
FNV-1a is non-cryptographic and 32 bits wide, and `tls.key` is hashed last, so trailing
bytes appended after the PEM block — which every PEM parser ignores — let a chosen digest
be hit by search. Anyone who can **write** the TLS Secret can therefore replace the
material while keeping the fingerprint identical, and neither the rolling update nor
`TLSMaterialStale` would notice. That principal can already replace the cluster's TLS
identity outright, so this buys evasion of the report rather than new access — but the
report must not be presented as evidence that the material is unchanged.

**Decided 2026-08-27: this stays.** A wide cryptographic digest would remove the collision and
therefore the silence, and it was still not taken, because of what remains afterwards: the
attacker loses the silent swap and gains one **indistinguishable from a legitimate rotation** —
same fleet roll, same condition transition, no observer for whom the two differ. What would
raise the ceiling is a trust anchor outside the Secret, not a better hash over it, and nothing
here has one. ADR 0030 D11 carries the reasoning and the two counter-arguments that do not
hold.

**The record used to be writable from inside a data pod, and is not any more.** The sidecar
Role grants `pods: patch` on this cluster's data pods. Until 2026-08-27 the fingerprint was a
pod annotation and every container of the data pod mounted the token, so `valkey-server` and
the third-party exporter could delete or overwrite it and suppress both the roll and the
staleness report. Two changes closed it, and either alone would have left a hole:

- **The token reaches one container.** `automountServiceAccountToken: false` on the data pod
  plus a projected volume mounted into the `sidecar` container
  ([ADR 0012](docs/adr/0012-the-sidecar-records-its-drain-promotion-on-the-pod.md) D8 step 4).
  `valkey-server`, both init containers and the exporter now hold no credential — but the
  sidecar must keep the grant, so this does not close the record.
- **The record left pod metadata.** It is an env var of that container's spec, and env is not
  one of the fields the API server lets a pod update change, so the patch is refused for every
  principal — the compromised sidecar included, and the operator too
  ([ADR 0031](docs/adr/0031-a-record-the-operator-trusts-lives-in-pod-spec.md)).

Two corrections to what this section used to say. It called the annotation the *third*
forgeable field of that grant. Section 3 now enumerates them instead of counting: nine rows,
eight of them still live. And it treated forgery as the attack: **deletion was cheaper**, and
no digest strength would have touched it.

**Who reloads and who is replaced.** A process that parsed a certificate at startup
keeps presenting it until it exits — measured on a live fleet, it killed the sidecar
labeler, the Sentinel cross-check and the drain promotion of
[ADR 0012](docs/adr/0012-the-sidecar-records-its-drain-promotion-on-the-pod.md) on
every TLS cluster whose pods outlived a rotation, silently. The rule that follows is
in section 6 and in ADR 0030: **a long-lived process this repository owns re-reads its
material; every other process is replaced by a rolling update the rotation triggers.**

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
- Every pod template the operator renders is rootless since 2026-09-26 — uid 999 on data and
  Sentinel pods, no capability, `no_new_privs`, ~~`RuntimeDefault` seccomp~~ a seccomp filter
  that is `RuntimeDefault` unless `spec.podSecurity.seccompProfile` names a `Localhost` profile,
  and never `Unconfined` *(amended 2026-09-26,
  [ADR 0033](docs/adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md)
  D1)*, a read-only root filesystem, `privileged: false` stated and no Service links — and the
  unit tier checks every rendered template against Pod Security `restricted` — the whole
  matrix without the `spec.podSecurity` opt-ins, and the three templates of one cluster with
  both (`make test-unit`, green 2026-09-26; section 4.5,
  [ADR 0032](docs/adr/0032-generated-pods-run-rootless.md) D1). A user namespace
  (`hostUsers: false`) is opt-in per Valkey resource and off by default (section 4.5). **On a
  node the rootless posture is verified locally only** *(subject named 2026-09-26: the sentence
  said "it" right after the user namespace, which it did not cover)* — Kind with containerd,
  2026-09-26, not in CI (section 9) — ~~and the ADR 0033 additions not at all yet: their e2e has
  not completed~~ *(superseded 2026-09-26)*, and so are the ADR 0033 additions on the generated
  pods, the user namespace among them: `TestE2E_PodHardening_UserNamespacesLocalhostSeccompAndDigest`
  ran on Kind (Kubernetes 1.36.1, containerd 2.3.1, runc 1.4.2, Linux 6.10) and found the `valkey`
  container of each of the three data pods and the `sentinel` container of each of the three
  Sentinel pods of an opted-in cluster in a user namespace of their own (`uid_map` is not the
  identity map) under a seccomp filter, one data pod's `valkey` container still at uid 999 with an
  empty bounding set and `no_new_privs`, and the dataset written before the move intact through
  the idmapped mount (section 4.5). The chart's opt-ins for the operator's own pods (section 4.4)
  were not run on a node. ~~That run predates the `Localhost` allow-list and the CEL path rule
  (ADR 0033 D9 and amended D1, section 3); the allow-list's own e2e subtest has not run.~~
  *(Superseded 2026-09-26.)* ~~The final run of 2026-09-26~~ The last two runs of 2026-09-26
  *(amended 2026-09-26: a later run followed the one this named)* — each on one image built from
  the code with the allow-list and the CEL path rule (ADR 0033 D9 and amended D1, section 3), the
  later one also with ADR 0025 D9's own clock, same Kind setup — ~~was green on both Valkey
  lines~~ ran this test green on both Valkey lines, the allow-list's own refusal subtest included;
  the later run's one failure was another test's (section 9). The
  rootless posture does not hold for a pod an earlier operator built until the migration replaces it, nor for the one root
  init container of a pod created while the migration repair was on the template, until the
  second roll of the migration replaces that pod too (below).
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
- **The sidecar can patch any metadata on its own cluster's pods, and `pods: patch`
  is wider than metadata.** The grant is no longer namespace-wide —
  `resourceNames` limits it to `<cr-name>-0 … <cr-name>-N` (section 4.2) — and
  since 2026-08-27 only the **sidecar container** holds the token that carries it
  ([ADR 0012](docs/adr/0012-the-sidecar-records-its-drain-promotion-on-the-pod.md) D8
  step 4). Within that list it is still unrestricted. What a compromised sidecar can
  rewrite, enumerated rather than sampled — this list used to say "the third field"
  and stop at two:

  | Field | What it buys |
  |---|---|
  | `instanceRole` label | the `-rw` and `-r` Services select on it; setting `master` diverts client writes |
  | `vko.gtrfc.com/drain-promoted-at` | the operator consumes it as promotion evidence ([ADR 0012](docs/adr/0012-the-sidecar-records-its-drain-promotion-on-the-pod.md) D6, [ADR 0028](docs/adr/0028-a-demotion-may-not-discard-the-only-dataset.md)) |
  | `vko.gtrfc.com/config-hash` | suppresses the rolling update for a config change |
  | `vko.gtrfc.com/pod-spec-hash` | suppresses the rolling update for a pod-spec change |
  | ~~`vko.gtrfc.com/tls-material-hash`~~ | it did suppress the certificate-rotation roll **and** the `TLSMaterialStale` report; the record moved into pod spec on 2026-08-27 and the annotation is now inert ([ADR 0031](docs/adr/0031-a-record-the-operator-trusts-lives-in-pod-spec.md)) |
  | any selector label | deleting one detaches the pod from its StatefulSet controllerRef; `podIsOurs` then reads false |
  | `metadata.ownerReferences` | not in apimachinery's immutable ObjectMeta set — that set is exactly `name`, `namespace`, `uid`, `creationTimestamp`, `deletionTimestamp`, `deletionGracePeriodSeconds` |
  | `metadata.finalizers` | same; a foreign finalizer keeps the pod from ever being deleted |
  | `spec.containers[*].image` | one of the five entries the API server allows a pod update to change |

  Nine rows, of which the struck-through one is no longer reachable: eight are live.
  For every hash still in that table the **deletion** is cheaper than the forgery,
  because both consumers carry a presence guard (`recorded != "" && recorded !=
  desired`), so setting the key to `null` makes the pod unmeasured rather than
  mismatched. That is why the TLS fingerprint's answer was to leave pod metadata and
  not to get a stronger digest, and it is the argument for moving `config-hash` and
  `pod-spec-hash` next ([ADR 0031](docs/adr/0031-a-record-the-operator-trusts-lives-in-pod-spec.md)
  D6). Nothing narrower is expressible for the label and the drain stamp: those
  are the writes the sidecar exists to make
  ([ADR 0012](docs/adr/0012-the-sidecar-records-its-drain-promotion-on-the-pod.md) D8).
- ~~**The workload pods have no securityContext at all.** No `runAsNonRoot`, no
  `readOnlyRootFilesystem`, no `capabilities: drop [ALL]`, no
  `seccompProfile` — verified by the absence of any `SecurityContext` in
  `internal/builder`. The operator's *own* Deployment sets all four
  ([`deployment.yaml`](deploy/helm/valkey-operator/templates/deployment.yaml)); the
  clusters it creates inherit whatever the namespace's Pod Security admission
  level allows. A restricted-PSA namespace will reject these pods outright.~~
  **Superseded 2026-09-26 by [ADR 0032](docs/adr/0032-generated-pods-run-rootless.md) D1:
  every pod template the operator renders is rootless, with no option (section 4.5).** The struck list was
  also short by one: it named four controls and omitted `allowPrivilegeEscalation: false`,
  the fifth the operator's own Deployment sets
  ([ADR 0013](docs/adr/0013-operator-is-cluster-wide-privileged.md) D8) and every generated
  container now carries. What still does not hold is the **migration window**: a pod an
  earlier operator built keeps running its Valkey-image containers as uid 0 with the
  runtime's default capabilities until the roll replaces it; a non-persistent
  `spec.replicas: 1` pod and a pod too old to carry a `pod-spec-hash` annotation are not
  replaced by the roll at all and stay root until they are deleted for another reason — a
  container restart keeps the pod spec; and every data pod created while the template
  carries the ownership repair keeps that root init container in its spec ~~until the pod is
  next replaced, which after the upgrade is every persistent data pod the migration roll
  created~~ until the second roll replaces it, which starts once the repair has left the
  template (corrected 2026-09-26, [ADR 0032](docs/adr/0032-generated-pods-run-rootless.md)
  D2; section 4.5).
- ~~**The data pod mounts the sidecar token into every container.**
  `automountServiceAccountToken` is disabled on the observer pod and nowhere else,
  so the `valkey` and `exporter` containers carry the sidecar token too. It is a
  pod-level field, so splitting it per container is not expressible in Kubernetes;
  a separate ServiceAccount per container would need a separate pod.~~
  **No longer true, and the last sentence never was.** Since 2026-08-27 the data pod
  sets `automountServiceAccountToken: false` and projects the token into the
  `sidecar` container alone; the Sentinel pod sets the flag and projects nothing.
  The split needs no second ServiceAccount and no second pod — a hand-declared
  `projected` volume with a `serviceAccountToken` source, mounted into one
  container, has been GA since Kubernetes 1.20
  ([ADR 0012](docs/adr/0012-the-sidecar-records-its-drain-promotion-on-the-pod.md) D8
  step 4).
- **A CR author picks the image.** `spec.image` and `spec.metrics.image` are
  arbitrary strings with no registry allowlist, and ~~the pods run with the
  namespace's default security posture~~ (superseded 2026-09-26 by
  [ADR 0032](docs/adr/0032-generated-pods-run-rootless.md) D1) whatever user the image
  declares, its containers run as uid 999 with no capability, `no_new_privs` and
  ~~`RuntimeDefault` seccomp~~ the seccomp filter of `spec.podSecurity.seccompProfile`
  *(amended 2026-09-26, ADR 0033 D1)* (section 4.5). The choice no longer buys root inside the
  container; it still buys arbitrary code next to the cluster password and the dataset —
  and in a pod created while the migration repair was on the template, until the second roll
  replaces it, `spec.image` is also the image of the one root init container. A digest-pinned
  `spec.image` is deployable since 2026-09-26 (it used to produce an invalid
  `app.kubernetes.io/version` label, ADR 0033 D5), so a CR author *can* pin what runs; nothing
  makes them.
- **A CR author also picks the seccomp profile, ~~among the ones on the nodes~~ among the
  `Localhost` profiles the operator's allow-list names — none by default** *(re-decided
  2026-09-26,
  [ADR 0033](docs/adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md)
  D9)*. Since 2026-09-26
  `spec.podSecurity.seccompProfile` is `RuntimeDefault` or `Localhost` with ~~any path~~ a relative
  path below the kubelet's seccomp directory
  ([ADR 0033](docs/adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md)
  D1). `Unconfined` is refused by the CRD, but a `Localhost` profile is only as strict as the
  file it names: an allow-by-default profile installed on a node — the e2e fixture is one —
  is weaker than `RuntimeDefault` and still passes Pod Security `restricted`, which accepts
  every `Localhost` profile (read in `k8s.io/pod-security-admission` v0.37.0,
  `check_seccompProfile_restricted.go`). The path cannot leave that directory: the API server
  refuses an absolute path or a `..` element in every pod template (read in upstream
  `validateSeccompProfileField`, Kubernetes v1.36.4; not tested here) — ~~the CRD itself checks
  only that the path is non-empty~~ *(superseded 2026-09-26: a second CEL rule on
  `SeccompProfileSpec` now refuses both at CR admission, section 5)*. ~~The profile set on the
  nodes is therefore part of what a CR author may choose from (section 9).~~ *(Superseded
  2026-09-26 by the allow-list below; so is the stance that the operator has no allow-list and an
  admission policy has to narrow the choice.)*

  **The operator enforces a default-deny allow-list of `Localhost` profiles** (ADR 0033 D9).
  `--allowed-seccomp-localhost-profiles` — chart value
  `valkeyPodSecurity.allowedSeccompLocalhostProfiles`, `[]` # default — lists the profiles, as
  paths relative to the kubelet's seccomp directory, a Valkey resource may name; the chart
  renders the flag only for a non-empty list, and `profileList` trims the comma-separated
  entries and drops blanks ([`cmd/main.go`](cmd/main.go)). `RuntimeDefault` needs no entry and
  is always allowed. A `Localhost` path matching no entry exactly — every one while the list is
  empty — is refused ~~before any of the three workloads is written~~ at the write of each of the
  three workloads *(gate moved 2026-09-26 from the head of each workload step)*, on create and on
  update alike: `reconcileStatefulSet` returns `errSeccompProfileNotAllowed` ~~ahead of the data
  StatefulSet write~~ right before it writes — on create after the TLS material record, on update
  after the ownership proof, the claim guard (`guardVolumeClaimTemplates`), the TLS material
  record and the repair decision, and before the drift detection — and is the one reporter
  (`ReconcileBlocked=True/SeccompProfileNotAllowed`, phase
  `Error`, the message naming the profile and the chart value; ranked below `RecreateRequired`
  and above `UserNamespacesUnsupported` in `reconcileBlockedReason`), while the Sentinel
  StatefulSet and observer Deployment steps withhold their writes silently at the same point —
  after their own ownership proof and, on the Sentinel StatefulSet, its claim guard and TLS
  material record (`seccompProfileAllowed` in [`pod_hardening.go`](internal/controller/pod_hardening.go), called
  from [`valkey_controller.go`](internal/controller/valkey_controller.go)). The
  chart refuses at render an empty entry, one starting with `/`, one containing `,` or one with a
  `..` element (`valkey-operator.allowedSeccompLocalhostProfiles` in
  [`_helpers.tpl`](deploy/helm/valkey-operator/templates/_helpers.tpl)). Documenting the risk
  only was decided first and reversed the same day; removing `Localhost` would have taken the
  option from clusters that manage their own profiles (section 4.5).

  - *What it defends against.* A CR author naming a profile nobody chose for Valkey — a
    permissive test profile left on a node, one installed for another workload, or any path at
    all on a cluster whose administrator never considered `Localhost`: the default refuses every
    one. What CR authors choose from becomes a decision of whoever configures the operator
    instead of a side effect of what lies on the nodes.
  - *What it does not defend against.* The list holds **names**; the operator cannot see the
    files. It trusts that a listed path holds, on every node a pod can land on, a profile at
    least as strict as intended — whoever can write the kubelet's seccomp directory on a node, or
    the tool that manages it, decides what a listed name enforces, and a listed file that differs
    between nodes or is loosened later goes unnoticed. `RuntimeDefault` is whatever the node's
    runtime ships. The list is one per operator, not per namespace: every CR author may pick any
    listed profile. It binds only what the operator writes; a principal who may create pods or
    StatefulSets in the namespace directly is not bound by it, and an admission policy on pods
    stays the control for those.
  - *What a refusal costs.* The whole workload write is held, not only the profile: until an
    administrator lists the profile or the spec names another, an image change or a certificate
    rotation of that cluster does not reach its templates either (~~the TLS fingerprint is stamped
    after the check in `reconcileStatefulSet`~~ *(superseded 2026-09-26)* the TLS fingerprint is
    stamped before the check since the gate moved, onto a template the refusal then does not
    write — read, not tested), and the running pods keep
    their template. The rest of the pass still runs — `runReconcileSteps` joins the step errors
    and carries on — so ConfigMaps, Services, sidecar RBAC, certificates, PodDisruptionBudgets,
    NetworkPolicies and the metrics objects are written as usual, and a rotated certificate is
    still reported by `TLSMaterialStale`, which compares the pods against the Secret (read, not
    tested). Removing a profile from the list freezes the clusters that name it the same
    way; it does not take the profile off their running pods.
  - *What is still reported while a profile is refused* *(added 2026-09-26, when the gate moved:
    at the head of each workload step a refusal had hidden a name collision, frozen the
    `StorageSpecNotApplied` level and skipped the TLS material record — closed the same day)*.
    Everything a workload
    step proves or measures before its write. A data or Sentinel StatefulSet under the generated
    name that this Valkey does not control is reported as `ForeignObject` with its Warning Event,
    and outranks the refusal in `reconcileBlockedReason`; a foreign observer Deployment still
    gets its Warning Event. The claim guard re-measures `StorageSpecNotApplied` on every pass, and
    a `RecreateRequired` conflict ends the data step before the gate and outranks the refusal as
    well. The TLS material record is armed before the gate, so on a fresh TLS cluster whose
    Secret cert-manager has not issued yet the step ends at the record (ADR 0030 D12) and the
    refusal is reported once the Secret exists. Because the gate sits before the drift
    detection, a template that already carries a profile the list no longer holds is reported on
    every pass, not only when something else would be written. Verified by
    `TestSeccompProfileNotAllowed_GateSitsAtTheWrite` (a foreign StatefulSet is reported as
    `ForeignObject`; a live template the shrunk list no longer holds is reported without drift
    and not written); the claim-guard and TLS-record orderings are read from
    `reconcileStatefulSet`, not tested row by row.

  Verified: `TestSeccompProfileAllowed`, `TestSeccompProfileNotAllowed_NoWorkloadIsWritten`
  (create and update, all three workloads) and `TestProfileList` (`make test-unit`, green
  2026-09-26 before the gate moved; ~~the full unit tier on the final code is not claimed~~
  *(amended 2026-09-26)* the full unit tier with the gate at the write was green in a clean copy,
  `make test-unit-coverage`, on the code before ADR 0025 D9 gained its own clock; its rerun on
  the final code has no result yet),
  `TestSeccompProfileNotAllowed_GateSitsAtTheWrite` (added with the move; 7 of 7
  mutations of the D9 code killed, the gate-position mutations included); the chart's rendering
  and refusals with `helm template` by hand (2026-09-26). ~~No
  recorded run yet: `TestPodSecurity_LocalhostProfileAllowList_Integration` (the green
  `make test-integration` of 2026-09-26 predates it) and the e2e subtest "a Localhost profile
  the operator does not allow is refused and reported".~~ *(Superseded 2026-09-26.)*
  `TestPodSecurity_LocalhostProfileAllowList_Integration` was green in repeated envtest runs
  (Kubernetes 1.29 API server), and the e2e subtest "a Localhost profile the operator does not
  allow is refused and reported" — `ReconcileBlocked=True/SeccompProfileNotAllowed` through the
  real chart, no StatefulSet created — was green on every run of the final image (section 9).
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
    verbs: ["get", "patch"]
    resourceNames: ["<cr-name>-0", "<cr-name>-1", "<cr-name>-2"]   # example: replicas 3
```

What the sidecar actually calls: **`Pods(ns).Patch` and `Pods(ns).Get`, nothing
else.** Verified by grep over `internal/sidecar` and `cmd/sidecar` — the clientset
call sites are `patchMetadata` ([`internal/sidecar/labeler.go`](internal/sidecar/labeler.go)),
used by `PatchLabel` (own pod, `instanceRole`) and `PatchAnnotation` (the peer pod
the drain handler promoted), and `IsTerminating` (same file), the drain handler
reading whether a promotion candidate carries a `DeletionTimestamp` before it
forwards the drain window to it (added 2026-08-27, ADR 0028 D5a — promoting a
terminating peer was measured wiping the fleet). `get` on the same named pods
reveals pod specs of this cluster only; the Secrets those pods use are mounted,
never inlined, so the read exposes no credential material. The grant matches that exactly: one verb, and only the
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

**Since 2026-08-27 the grant reaches one container, not the whole data pod.** The pod
still runs as `<cr-name>-sidecar` — a pod has one identity — but it sets
`automountServiceAccountToken: false` and hands the token to the `sidecar` container
through a projected volume it declares itself. `valkey-server`, every init container
and the third-party `redis_exporter` now hold no token. Sentinel pods, which
never call the API, set the same flag and declare no projection. This is D8 step 4 of
[ADR 0012](docs/adr/0012-the-sidecar-records-its-drain-promotion-on-the-pod.md);
section 3 lists what the grant still permits the sidecar itself.

### 4.3 The pre-upgrade hook

`<release>-upgrade` gets `valkeys: get,list,patch,update` and
`customresourcedefinitions: get,list,patch,update`, cluster-wide, for the duration
of the Job. `patch`/`update` on CRDs is a **cluster-wide schema-change grant**: a
compromised hook image could rewrite the schema or the conversion strategy of any
CRD in the cluster. It is disabled with `preUpgradeHook.enabled: false`, at the
cost of the field-default migration it performs
([`cmd/migrate`](cmd/migrate/migrate.go)). Its pod carries the operator's pod posture
(section 4.4); it mounts its token because it talks to the API server.

### 4.4 Operator process posture

The operator Deployment and the pre-upgrade hook Job render the same two helpers since
2026-09-26
([`_helpers.tpl`](deploy/helm/valkey-operator/templates/_helpers.tpl),
[ADR 0033](docs/adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md)
D6), verified in the templates:

| Level | Field | Value |
|---|---|---|
| pod (`valkey-operator.podHardening`) | `runAsNonRoot`, `runAsUser`, `runAsGroup`, `fsGroup` | `true`, 65532, 65532, 65532 — the distroless `nonroot` user, now named instead of inherited from the image |
| pod | `seccompProfile` | `podSecurity.seccompProfile`: `RuntimeDefault` # default, or `Localhost` with `localhostProfile`. Any other type, `Localhost` without a path, or a path without `Localhost` fails the render — `Unconfined` cannot be rendered. Since 2026-09-26 so does a `localhostProfile` that starts with `/` or has a `..` element (`..` inside a file name passes) — the path rule the CRD applies to `spec.podSecurity` (section 5), which for these two pods the API server would otherwise apply only when it validates the Deployment or hook Job (section 3) |
| pod | `hostUsers` | `false` only with `podSecurity.userNamespaces: true` # default `false` |
| pod | `enableServiceLinks` | `false` |
| pod | `automountServiceAccountToken` | `true`, stated: both pods call the API server |
| container (`valkey-operator.containerSecurityContext`) | `privileged`, `allowPrivilegeEscalation`, `readOnlyRootFilesystem`, `capabilities` | `false`, `false`, `true`, `drop: [ALL]` |
| image (`valkey-operator.image`) | `image.digest` | empty # default. Set to `sha256:<64 hex>` (checked at render time), it makes the reference `repository:tag@digest` for the operator, the hook **and** `--operator-image` / `OPERATOR_IMAGE`, so the sidecar and observer containers the operator generates run the pinned image too |

The Deployment keeps `terminationGracePeriodSeconds: 10`. Ports 8080 (metrics) and 8081
(health), both plain HTTP and unauthenticated. The chart's `podSecurity` values do not reach the
Valkey pods; those take `spec.podSecurity` per resource (section 4.5), and
`valkeyPodSecurity.allowedSeccompLocalhostProfiles` bounds which `Localhost` profile that field
may name (section 3; the operator's own `podSecurity.seccompProfile` is not checked against it).

Three consequences of the opt-ins, read from the templates and not run: `podSecurity.userNamespaces`
on a cluster whose nodes cannot honour it leaves the operator pod — and on `helm upgrade` first the
hook pod, which runs before the Deployment is updated — unable to start, and only that pod's own
status and events say why, since no Valkey resource can report on the operator; a `Localhost`
profile missing on the node does the same; and an API server
with `UserNamespacesSupport` off drops `hostUsers` from these two templates as silently as from
the operator's own writes — but the `UserNamespacesUnsupported` report (section 4.5) covers only
the workloads the operator writes, so for its own pod nothing says the namespace is missing.
Check with `kubectl get deploy -n <release-namespace> <chart-fullname> -o jsonpath='{.spec.template.spec.hostUsers}'`. The chart was
checked with `helm lint` and `helm template` by hand only (defaults, `image.digest`, userns,
`Localhost`, and each refused value — since 2026-09-26 including the allow-list's and the
operator's own `localhostProfile` path check: `/etc/op.json`, `../op.json` and `profiles/../op.json`
fail the render, `profiles/op.json` and `profiles/..op.json` render into the Deployment and the
hook Job). In CI
~~only the default values are rendered~~ the chart is rendered only by the e2e job's
`helm install` (`.github/workflows/release.yml`), with
[`test/e2e/helm-values.yaml`](test/e2e/helm-values.yaml) — image, resources, leader election
and, since 2026-09-26, a non-empty `valkeyPodSecurity.allowedSeccompLocalhostProfiles`, while
`podSecurity` and `image.digest` stay at their defaults *(amended 2026-09-26)*; **no CI
gate renders the digest, user-namespace or `Localhost` paths or checks a refusal** (section 9).

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

### 4.5 Workload pod posture

Since 2026-09-26 every pod template the operator renders is rootless, with no CRD field and no
opt-out — `spec.podSecurity`, added the same day, chooses the seccomp profile and a user namespace
and cannot turn the rootless posture off; the migration repair below is the one root container it
still adds
([ADR 0032](docs/adr/0032-generated-pods-run-rootless.md) D1, superseding
[ADR 0013](docs/adr/0013-operator-is-cluster-wide-privileged.md) D9). Before that no generated
pod set a `securityContext`, and because every container on the Valkey image sets `command:` —
which replaces the entrypoint that would have dropped to the `valkey` user — `valkey-server`,
`valkey-sentinel` and the init scripts ran as uid 0 under `Unconfined` seccomp; for
`valkey-server`, measured in Docker on both pinned lines, with fourteen capabilities
(`NET_RAW`, `DAC_OVERRIDE` and `SETUID` among them) and `NoNewPrivs: 0`.

[ADR 0033](docs/adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md)
(2026-09-26, same unreleased release) adds two per-resource choices under `spec.podSecurity` — the
seccomp profile and an opt-in user namespace — and states the rest of what a pod manifest can
express instead of leaving it to API defaults. Both helpers of
[`pod_security.go`](internal/builder/pod_security.go) end in `applyPodHardening`, so the fields
below reach every data, Sentinel and observer pod.

| Pod | Pod level | Every container and init container |
|---|---|---|
| data `<cr>-N` | `runAsNonRoot: true`, `runAsUser: 999`, `runAsGroup: 999`, `fsGroup: 999`, ~~`seccompProfile: RuntimeDefault`~~ `seccompProfile` = `GetSeccompProfile()` (`RuntimeDefault` # default, or the `Localhost` profile of `spec.podSecurity.seccompProfile`), `enableServiceLinks: false`, `hostUsers: false` only with `spec.podSecurity.userNamespaces: true` (unset # default) | `privileged: false` *(stated since 2026-09-26)*, `allowPrivilegeEscalation: false`, `readOnlyRootFilesystem: true`, `capabilities.drop: [ALL]` |
| Sentinel `<cr>-sentinel-N` | same as data | same |
| observer | `runAsNonRoot: true`, ~~`seccompProfile: RuntimeDefault` — no uid: the operator image's distroless `nonroot` user (65532) is numeric, so kubelet can verify it; no `fsGroup`: no data volume~~ *(superseded 2026-09-26, ADR 0033 D4)* `runAsUser`, `runAsGroup`, `fsGroup: 65532` — the operator image's numeric `nonroot` user (`OperatorUID`), pinned rather than inherited from whatever an image built from another base declares; the `fsGroup` makes the optional TLS Secret volume readable to that group — and the same `seccompProfile`, `enableServiceLinks` and `hostUsers` as data | same |

`hostNetwork`, `hostPID` and `hostIPC` are not set on any generated pod: they are plain booleans
whose zero value is `false`, the API has no way to carry an explicit `false`, and
`TestPodHardening_DefaultsOnEveryTemplate` fails if a builder ever sets one.
`automountServiceAccountToken` stays as section 4.2 describes: `false` on every generated pod,
the data pod projecting its token into the sidecar alone.

- **One walk, not per-container code.** `applyValkeyPodSecurity` and `applyObserverPodSecurity`
  ([`pod_security.go`](internal/builder/pod_security.go)) run last in each builder over the
  assembled `PodSpec`, so a container added later inherits the posture by being in the pod.
  The unit tier evaluates every rendered template of the topology × TLS × auth × metrics ×
  persistence matrix with the checks the API server's PodSecurity admission runs
  (`k8s.io/pod-security-admission`,
  [`pod_security_test.go`](internal/builder/pod_security_test.go)).
- **One uid per pod.** The sidecar and the exporter run as 999 too, not as their images'
  users (65532, 59000), so the data volume has one owner. No generated pod shares its process
  namespace, so the common uid does not let one container see, signal or trace another's
  processes, and the ServiceAccount token stays a mount of the sidecar container alone
  (section 4.2).
- **`fsGroup: 999` with `fsGroupChangePolicy` unset** (= `Always`): `OnRootMismatch` inspects
  only the volume root and would skip files a later root writer left beneath a correctly
  owned one.

**Seccomp: `RuntimeDefault` or `Localhost`, never `Unconfined`**
([ADR 0033](docs/adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md)
D1). `spec.podSecurity.seccompProfile` sets the pod-level profile of the data, Sentinel and
observer pods (`GetSeccompProfile`). Omitted, or `type: RuntimeDefault`, is the runtime's default
filter as before; `Localhost` takes `localhostProfile`, a path relative to the kubelet's seccomp
directory. The CRD enforces the enum and, by the first CEL rule this CRD carries, that the path is
set exactly for `Localhost` (envtest refuses `Unconfined`, a `Localhost` without or with an empty
path, and a path with `RuntimeDefault` — `make test-integration`, green 2026-09-26); since later
that day a second CEL rule refuses a path starting with `/` or holding a `..` element (envtest
rows added, ~~no recorded run yet~~ green in repeated runs on 2026-09-26, Kubernetes 1.29 API
server), and the operator writes a `Localhost` profile only when its
allow-list names it, which is empty by default (section 3, ADR 0033 D9). `Unconfined`
is refused because it is the one value that removes the filter: every syscall the kernel offers
becomes reachable from a compromised `valkey-server`, and one CR would take its namespace out of
`restricted`. No Valkey workload is known to need a syscall `RuntimeDefault` blocks, so the escape
hatch buys nothing. `Localhost` exists for clusters that manage their own profiles (the Security
Profiles Operator, say) — a fixed `RuntimeDefault` would have left them a mutating policy, which
the drift comparison would rewrite back on every pass. Changing the profile moves both pod-spec
hashes and rolls the tiers failover-aware; an explicit `RuntimeDefault` hashes like the omitted
field and rolls nothing (`TestPodHardening_OptInsMoveThePodSpecHashes`). Three limits:

- A `Localhost` profile is **node state the operator cannot see**. Missing on a node, it keeps a
  pod scheduled there from starting (~~the e2e asserts that — not yet run~~ measured on Kind
  2026-09-26, before the allow-list existed: a container of the pod waits, and the runtime's
  message names the file; since the allow-list only a listed profile gets that far, and the e2e
  values list the missing one on purpose — measured again with the allow-list in the final run of
  2026-09-26, green on both Valkey lines); a multi-replica or
  Sentinel roll holds on that pod and reports `PodAvailabilityStalled` after
  `spec.rollingUpdate.syncTimeout` (ADR 0026 D11), a single pod is not reported (ADR 0032 D7).
  Too strict, a container fails at a syscall. It must allow every generated container, the
  `chown` of `fix-data-ownership` included.
- It is **only as strict as the file it names**, and every CR author may name ~~any file in that
  directory~~ any file the operator's allow-list names — none by default *(amended 2026-09-26,
  ADR 0033 D9)* (section 3). Pod Security `restricted` does not tell a permissive `Localhost`
  profile from a strict one, and neither does the allow-list: it compares names, not content.
- It is set **at pod level only**. Container-level `seccompProfile` stays unset and uncompared
  (drift paragraph below).

**User namespaces: opt-in, per Valkey resource**
([ADR 0033](docs/adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md)
D2, D3). `spec.podSecurity.userNamespaces: true` sets `hostUsers: false` on the data, Sentinel and
observer pods (`applyPodHardening`); omitted or `false` leaves the field unset, so no existing
cluster changes. Toggling it moves both pod-spec hashes and rolls the tiers failover-aware
(ADR 0005 D7). Inside the user namespace the securityContext above is unchanged — uid 999,
`drop: [ALL]`, no privilege escalation — read from the builder; that it still yields an empty
bounding set and `no_new_privs` on a node is what the e2e checks, ~~and it has not run yet~~
*(measured 2026-09-26 on Kind, in one data pod's `valkey` container: uid 999, `CapEff` and
`CapBnd` 0, `NoNewPrivs 1`)*.

- *What it defends against.* A process that escapes its container — a runtime or kernel
  breakout — arrives on the node as an unprivileged uid from the pod's own mapped range, not as
  host uid 999. Without it, uid 999 inside is uid 999 on the node: the same uid for every data and
  Sentinel pod of every Valkey cluster scheduled there, and the owner of what they wrote to
  node-local volumes such as Kind's `hostPath` claims — so one escaped Valkey process would own
  the other clusters' files on that node (reasoned, not tested). A capability held inside the
  user namespace — the repair's `CAP_CHOWN` is the only one — is honoured by the kernel only
  for files whose owner maps into that namespace (Linux semantics, not measured here). That
  kubelet gives every pod a distinct, non-overlapping host range is **read in
  the upstream user-namespace documentation, not measured here**; the e2e checks only that
  `uid_map` is not the identity map ~~, and has not run yet~~ — measured 2026-09-26 on Kind for
  the `valkey` container of the three data pods and the `sentinel` container of the three
  Sentinel pods of the test cluster (not the sidecar, exporter or observer containers).
- *What it does not defend against.* Everything that stays inside the pod: the cluster password
  in the environment (section 2), the dataset, the sidecar's token (section 4.2), unrestricted
  egress (section 3), and every command an authenticated Valkey client can send. It narrows no
  syscall and closes no kernel bug reachable from inside the pod's own namespace. It is off
  unless a CR asks for it, and the chart's `podSecurity.userNamespaces` is a separate switch for
  the operator and hook pods only (section 4.4).
- *What it needs.* Kubernetes 1.33 (1.30 with the `UserNamespacesSupport` gate), containerd 2.0 or
  CRI-O 1.25, Linux 6.3 for idmapped `tmpfs`, and idmap support in the file system of every data
  volume, which NFS lacks — from the field's documentation and the ADR, not measured here beyond
  the e2e's Kind node ~~(not yet run)~~ (Kubernetes 1.36.1, containerd 2.3.1, runc 1.4.2,
  Linux 6.10, where it passed on 2026-09-26 — a `hostPath` volume, which says nothing about
  idmap support on other file systems).
- *When the API server drops the field* (D3). An API server with `UserNamespacesSupport` off —
  the default before Kubernetes 1.33 — drops `hostUsers` from a pod template **without an error**
  (measured in envtest on Kubernetes 1.29). Every create and update of the data StatefulSet, the
  Sentinel StatefulSet and the observer Deployment — every write that carries a pod template; the
  nudge is a metadata-only merge patch — goes through `writeWorkload`
  ([`pod_hardening.go`](internal/controller/pod_hardening.go)), which reads the stored template
  out of the write's answer; sent `false` and stored nothing fails the step with
  `errUserNamespacesDropped`, reported as `ReconcileBlocked=True/UserNamespacesUnsupported` and
  phase `Error`, the message naming the gate and both ways out. The write itself is not withheld
  — an image change or a TLS rotation in the same template still applies — so the pods run
  **without** a user namespace and the CR says so, while `Ready` keeps reporting the data plane
  (ADR 0002 D5). The next pass sees the same drift and writes again, so the report stands for as
  long as the cluster drops the field, paced by the rate limiter; in `reconcileBlockedReason` it
  ranks directly below `RecreateRequired` and above an admission rejection, because it too
  clears only when a human acts
  (gate on, or the field back to `false`). Report and release measured in envtest, `make
  test-integration` green 2026-09-26.
- *When a node cannot honour it.* An API server that keeps the field in front of a runtime or
  kernel without support is **not** detected before a pod fails to start: the roll holds on the
  first replacement and reports `PodAvailabilityStalled` after `spec.rollingUpdate.syncTimeout`
  (read from ADR 0026 D11, not measured with a user namespace); a single pod is not reported
  (ADR 0032 D7). Not measured: the migration repair inside a user
  namespace — uid 0 of the namespace re-owning root-written legacy files through an idmapped
  mount. The e2e moves a cluster that was rootless from birth, so no repair runs in it.

**Stated rather than defaulted**
([ADR 0033](docs/adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md)
D4).

- `privileged: false` on every container and init container, the repair included
  (`restrictedContainerSecurityContext`, `dataOwnershipRepairContainer`). It is the API default;
  stated so that the object reads complete and a scanner does not have to know the default, and
  compared by the drift check, so an out-of-band `privileged: true` on a template is converged
  back.
- `enableServiceLinks: false` on every generated pod. kubelet otherwise injects
  `<SERVICE>_SERVICE_HOST`, `<SERVICE>_SERVICE_PORT` and the Docker-link `<SERVICE>_PORT*`
  variables for every Service of the namespace that has a cluster IP into every container — an
  inventory of the namespace that no process here reads, and a name-collision surface for the
  variables they do read. The `KUBERNETES_SERVICE_*` and `KUBERNETES_PORT*` variables of the
  `kubernetes` Service in the `default` namespace are injected regardless (read in kubelet
  `getServiceEnvVarMap`, Kubernetes v1.36.4).

**Images by digest**
([ADR 0033](docs/adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md)
D5). Until 2026-09-26 a digest-pinned `spec.image` could not be deployed at all:
`ExtractVersionFromImage` returned `sha256:<64 hex>` as the `app.kubernetes.io/version` label, 71
characters with a colon, and the API server refuses such a label on every object carrying it. It
now returns the tag of `repo:tag@sha256:…`, an empty value for a digest-only reference, and never
the digest; every case of `TestExtractVersionFromImage` is checked with `IsValidLabelValue`. The
exporter default
`DefaultMetricsExporterImage` is pinned to the digest of the multi-arch image index behind
`v1.66.0`, read with `docker buildx imagetools inspect` on 2026-09-26, so a re-pushed tag cannot
change what runs next to the password. The sidecar and the observer run the operator image,
pinned when the chart's `image.digest` is set (section 4.4). Nothing requires a digest: a tag in
`spec.image` or `spec.metrics.image` is pulled by tag as before.

**Resources**
([ADR 0033](docs/adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md)
D7). `spec.sentinel.resources` goes to every container of the Sentinel pod, the init container
included; omitted means no requests and no limits, as before. The sidecar and the data pod's init
containers state none, and no container gets a default: a limit guessed too low is an OOM kill in
the process that holds the drain promotion (ADR 0012). A namespace with a cpu/memory
`ResourceQuota` therefore still refuses the data pods (section 9).

**What is deliberately not set**
([ADR 0033](docs/adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md)
D8). No AppArmor profile, on the generated pods or on the operator's: kubelet refuses a pod
that requests any AppArmor profile other than `Unconfined` on a node where AppArmor is not
enabled (`isRequired` in `pkg/security/apparmor/helpers.go` and the `Validate` refusal in
`validate.go`, read in upstream Kubernetes v1.36.4, not measured on a node), so an explicit
`RuntimeDefault` would keep every generated pod from starting on any node without AppArmor —
the SELinux-based distributions among them — while a runtime on an AppArmor node already applies
its default profile to a container that names none (runtime behaviour, neither read in source nor
measured here). No `seLinuxOptions`, no
`runtimeClassName`, no sysctls: each is cluster-specific, and nothing asked for them.

**What it changes, and what it does not.** A compromised process in a generated pod holds no
capability, cannot regain one through a setuid binary or file capabilities in its image
(`no_new_privs`), runs under a syscall filter, finds nothing writable outside its mounted
volumes, and — only where `spec.podSecurity.userNamespaces` is on — is an unprivileged uid on
the node. It still holds everything the container is given: the cluster password in its
environment (section 2), the dataset, and unrestricted egress (section 3).

**The drift comparison checks only what the operator sets.** `podSpecChanged`/`containerChanged`
(both StatefulSets) and `ObserverDeploymentHasChanged` compare the pod- and container-level
fields the builder sets, with subset semantics, so a field a mutating admission policy adds is
not a drift the operator rewrites on every pass (ADR 0032 D5). One field is deliberately not a
subset: a live template may not *add* a capability, so an out-of-band `NET_RAW` grant is
converged back. Everything the builder leaves unset is not compared at all — container-level
`runAsUser`, `runAsNonRoot` and `seccompProfile` included, which the restricted container
posture leaves to the pod level, and `fsGroupChangePolicy`. An out-of-band or admission-added
`runAsUser: 0` or `seccompProfile: Unconfined` on a container, or an `OnRootMismatch` that
narrows kubelet's `fsGroup` re-owning to the volume root, therefore stays on the template
until the operator writes it for another reason. Checked read-only on wds18 only (2026-09-26,
ADR 0032 residual risks): no Kyverno mutate rule there rewrites a StatefulSet template. A
namespace enforcing `restricted` refuses a pod carrying either regardless (section 9).

Since 2026-09-26 the comparison also covers `privileged` (subset, like the other container
fields) and `enableServiceLinks` (`containerSecurityContextChanged`, `podHardeningChanged`); the
pod-level profile was already compared by type and `localhostProfile` (`seccompProfileDiffers`),
so a `Localhost` path edited out of band is converged back. One more
field is deliberately **exact**: `hostUsers`. Opting out leaves the desired field unset, and a
subset comparison would never converge the persisted `false` back — the `capabilities.add`
argument, for a field whose unset value is the weaker one (ADR 0033 D2). Its cost, read from
`podHardeningChanged` and not tested: a mutating policy that adds `hostUsers: false` to the
StatefulSet or Deployment template of a CR that did not opt in is fought over, one template write
per pass; ask for it through `spec.podSecurity.userNamespaces` instead. A policy that mutates
Pods at creation does not touch the templates and is not fought over.

**How existing clusters move.** The posture is part of the built `PodSpec`, so both pod-spec
hashes change with the operator upgrade: every multi-replica data tier and every Sentinel tier
rolls once, failover-aware — the Sentinel tier included, which a plain upgrade otherwise never
rolls — and the observer Deployment is rewritten through its own `securityContext` comparison.
A persistent data tier rolls a second time to shed the repair below. A tier of one or two
Sentinels has no spare vote: the quorum guard as first committed refused every delete of an
available Sentinel there, so the posture — like every other Sentinel change — would never have
reached it. Since 2026-09-26
such a tier rolls one Sentinel at a time, each only while every other one is available, at the
cost of automatic failover for the seconds one Sentinel restarts
([ADR 0024](docs/adr/0024-the-sentinel-tier-reports-its-own-completion.md) D10,
`sentinelDeleteKeepsVotes`).
The ADR 0033 changes — `privileged: false`, `enableServiceLinks: false`, the observer's pinned
uid — ship in the same, still unreleased, release and ride these rolls; they add none of their
own, and the observer Deployment is rewritten once for all of them. A later change of
`spec.podSecurity` is an ordinary pod-spec change and rolls the tiers failover-aware.
Until a pod is replaced it runs as before; a container restart keeps the pod spec and so does
not apply the posture. A pod so old that it carries no `pod-spec-hash` annotation is not
recognised as outdated (`podSpecHashChanged` falls back to comparing resources) and keeps
running as root until it is deleted for another reason — and on a persistent tier it keeps the
repair below on the template for as long.

**The pre-flight `check-data-writable`** is the first init container of every persistent data
pod (only the repair below goes in front of it). It runs as uid 999 with shell builtins only and
fails the pod when `/data`, `/data/appendonlydir` or a regular file directly in either is not
writable, naming the fix in the termination message (`FallbackToLogsOnError`, so `kubectl
describe pod` shows it). It turns a silent failure into a loud one: on an RDB volume whose root
is `0755 root`, a uid-999 pod starts, answers reads — and then every `BGSAVE` fails and, with the
generated `stop-writes-on-bgsave-error yes`, every write returns `MISCONF` while the pod stays
Ready (measured, T31). Nested directories and non-regular files are not checked.

**`fix-data-ownership` is the one root process the operator still creates**, and only while the
migration runs (ADR 0032 D2, D4).

- *When.* Only in the data StatefulSet, only with persistence, and only while a data pod proven
  ours (`podIsOurs`) in the live StatefulSet's ordinal range runs without `runAsNonRoot: true` —
  a pod an earlier operator built — or the live template itself still lacks it, because at the
  first pass after the upgrade a pod may be missing *(the template half added 2026-09-26: it was
  in the code, not here)*. Once carried it stays until every ordinal holds a migrated
  pod — proven ours, rootless and Ready — and no data-tier roll is recorded; ~~past its
  pre-flight (exited 0, or the pod has been Ready)~~ *(tightened 2026-09-26: the removal starts
  the second roll, which must not overtake the first — ADR 0032 D4)*; a
  missing or foreign pod is not proof, which closes the race on the last pod of a tier
  (`dataOwnershipRepairNeeded`,
  [`pod_security_migration.go`](internal/controller/pod_security_migration.go)). No Sentinel,
  observer or non-persistent pod gets it: their volumes are fresh `emptyDir`s.
  *The ordering fix, 2026-09-26: both defects surfaced in fleet-upgrade runs on a node; the
  mechanism of the first was then read from the code (T31).* `reconcileStatefulSet` runs
  before the rolling update in the same pass. The first fleet-upgrade run with the second roll
  counted one `RollingUpdateComplete` per persistent tier instead of two: the repair had left the
  template on the last replacement's pre-flight, every pod turned outdated under the first roll,
  and `clearStaleRollingUpdateState` discarded that roll's state before it finalized — on the
  non-Sentinel path in the middle of the topology restoration. Hence Ready instead of "past its
  pre-flight", and the recorded-roll gate. The second run then stranded the repair: the pass that
  may remove it is the one after the completion, and a completing pass scheduled none (the CR
  watch is generation-gated, and there is no Pod watch). `finishDataRoll`
  ([`rolling_update.go`](internal/controller/rolling_update.go)) now asks for that pass
  (`requestRecheck`, 10 s) whenever the template still carries the repair. For this document the
  consequence is the length of the window: the template carries the root init container until
  the tier's first roll has **finalized** plus, normally, that one recheck — not merely until its
  last pod started — and any pod deleted in that span, a chaos kill included, is created with it.
  The recorded-roll state is a gate on *keeping* the repair, never evidence for adding
  it (`TestDataOwnershipRepairNeeded_StaysWhileARollIsRecorded`,
  `TestCompletedRoll_AsksForThePassThatRemovesTheRepair`; `make test-unit` green 2026-09-26).
- *What.* uid 0 and gid 0, `runAsNonRoot: false`, `capabilities: drop [ALL], add [CHOWN]`,
  `privileged: false` *(stated since 2026-09-26, ADR 0033 D4)*,
  `allowPrivilegeEscalation: false` (`no_new_privs`), `readOnlyRootFilesystem: true`, under the
  pod's ~~`RuntimeDefault` seccomp~~ seccomp profile — `RuntimeDefault`, or a `Localhost` one,
  which must then allow its `chown` *(amended 2026-09-26, ADR 0033 D1)* — and, where
  `spec.podSecurity.userNamespaces` is on, inside the pod's user namespace, where uid 0 is not
  root on the node. It runs `find /data ! -user 999 -exec chown -h 999:999 {} + ;
  exit 0` ([`dataOwnershipRepairScript`](internal/builder/pod_security.go)). It is
  best-effort — it always exits 0, and the pre-flight after it is the one gate — because a
  second run on a sandbox restart cannot enter a directory the first handed to 999 with `0700`
  (`lost+found` on an ext4 root) without the DAC override it lacks. `-h` re-owns a symlink
  itself, never its target. *(Corrected 2026-09-26: this used to quote the command from before
  the pre-release review, without `-h` and `exit 0`.)* It mounts only the data volume and
  receives no environment and no token. Its image is the `valkey` container's, i.e.
  `spec.image`.
- *Why.* kubelet applies `fsGroup` on some volume types and not on others: not on `hostPath`
  (Kind's local-path provisioner — asserted by the fleet-upgrade e2e, green in one local run
  on 2026-09-26, not a CI job), NFS or a CSI driver with `fsGroupPolicy: None`. There,
  `root:root 0644` files and a `0755`
  `appendonlydir` an earlier operator wrote stay unwritable for uid 999, and an AOF pod exits
  at start. `CAP_CHOWN` alone suffices because the legacy files are owned by the uid the
  repair runs as (measured, T31).
- *Why the template writes are not a roll.* `reconcileStatefulSet` inserts it into the *built*
  StatefulSet (`WithDataOwnershipRepair`) after `ComputePodSpecHash` ran, so the pod-spec hash
  never covers it: adding it and removing it are two writes of the StatefulSet and no pod
  replacement. Root therefore enters only a pod *created* while the template carries it — the
  replacements of the migration roll, and any pod deleted for another reason in that window, a
  chaos kill included — as one `find`. *(This bullet was headed "Why it never rolls a pod";
  superseded 2026-09-26 by the second roll below — the template writes still roll nothing, the
  pods that carry the repair afterwards do.)*
- ~~*What rolling nothing leaves behind.* A pod that received the container keeps it in its
  spec after the template drops it, until the pod is next replaced — after the upgrade that is
  every persistent data pod the migration roll created.~~ *(Superseded 2026-09-26 by
  [ADR 0032](docs/adr/0032-generated-pods-run-rootless.md) D2: the alternative "accept the
  repair in pod specs until their next replacement" lost.)*
- *The second roll.* A pod that received the container keeps it in its immutable spec after the
  template drops it, and that alone makes it outdated (`podCarriesRetiredRepair`, asked through
  `podOutdated` at every data-tier site — the dispatch loop, `collectPodStates`, the standalone
  handler and the manual-failover master check,
  [`rolling_update.go`](internal/controller/rolling_update.go)). The ordinary failover-aware
  roll replaces it: every persistent multi-replica data tier rolls twice at the upgrade, and a
  persistent single pod restarts twice — two short downtimes, data kept. The comparison, not the
  hash, starts that roll, and only once the repair has left the template, which it does only
  when every ordinal holds a migrated pod and the first roll has finalized (the ordering fix
  above); a pod missing during the second roll is no evidence,
  so the repair does not come back. Until the second roll replaces it, such a pod keeps two
  exposures: Kubernetes re-runs a pod's init containers whenever it gives the pod a new sandbox
  (a node reboot, say), so the repair runs again there as root with `CAP_CHOWN`, finding
  nothing left to re-own; and `spec.initContainers[*].image` stays writable by a pod update
  like the container images (section 3), so a stolen sidecar token can point that root
  container at an image of its choice for its next run. Neither was measured. No root process
  runs in a pod created after the template dropped the repair, so once a tier's second roll
  completes none of its pods carries a root container. Unit-tested
  (`TestReconcileStatefulSet_RepairComesAndGoesAndTheRetiredRepairRolls`,
  `TestPodCarriesRetiredRepair`,
  `TestHandleStandaloneRollingUpdate_ReplacesAPodCarryingTheRetiredRepair`; `make test-unit`
  green 2026-09-26). ~~**not yet run on a node** (section 9).~~ *(Corrected 2026-09-26.)* On a
  node it ran twice, and each run found one of the two ordering defects above; ~~**the run after
  both fixes is in progress and has not completed**~~ *(superseded 2026-09-26)* the run after both
  fixes, from 1.12.8 on Kind, was green, with exactly two `RollingUpdateComplete` per persistent
  tier and nothing rolling after the second roll, and green again on the final image of
  2026-09-26 — locally, not in CI (section 9).

The evidence is the pod's `securityContext`, which no pod update can change: no label or
annotation — nothing the sidecar token can patch (section 3) — can switch the repair on, and it
survives an operator restart. Detaching a pod by deleting a selector label does make it "not
provably ours" and so keeps a repair that is already carried on the template; that extends the
migration window, it cannot start one. The recorded-roll gate of the ordering fix has the same
shape: `vko.gtrfc.com/rolling-update-state` is an annotation on the **CR**, which the sidecar
token cannot write (its Role names pods only, section 4.2), and a principal that can patch the
CR and keeps the annotation set holds an already-carried repair on the template — it cannot add
one. That principal already picks `spec.image`, which is the repair's image (read from
`dataOwnershipRepairNeeded`; not tested as an attack). A template carrying the repair passes Pod Security `baseline` and fails `restricted`
(`TestPodSecurity_TheRepairIsBaselineButNotRestricted`), which is why the namespace label waits
for the migration (section 9).

~~Two ways the repair itself fails, and both hold the pod in its init phase until the roll
reports `PodAvailabilityStalled` naming it after `spec.rollingUpdate.syncTimeout` — before the
pre-flight gets to name the fix. The repair sets no `FallbackToLogsOnError`, so its refusal is
in `kubectl logs <pod> -c fix-data-ownership`, not in `kubectl describe pod`.~~ *(Corrected
2026-09-26: that described the repair before the pre-release review made it best-effort.)* The
repair exits 0 whatever `chown` refused, so a volume it could not re-own reaches the pre-flight,
which holds the pod in its init phase and names the fix in `kubectl describe pod`; the roll
reports `PodAvailabilityStalled` naming the pod after `spec.rollingUpdate.syncTimeout`
([ADR 0026](docs/adr/0026-a-pod-being-deleted-is-not-available.md) D11). What `chown` refused is
in `kubectl logs <pod> -c fix-data-ownership`.

- **NFS with `root_squash`** is the case no pod can repair. Root is squashed, so `chown` is
  refused; the fix is a server-side `chown -R 999:999` before the upgrade. Root-squash itself
  was not measured.
- **A symlink on the data volume** is why the repair passes `-h`. Measured in Docker on
  2026-09-26 (`valkey/valkey:9.1.1`, GNU coreutils 9.7) on the command without it: `chown`
  follows a symlink, so the root repair tried to re-own the link's *target* inside its own
  container rather than the link, and any `chown` failure — a dangling link, a target on the
  read-only root — made `find`, and with it the repair, exit 1. With `-h` it re-owns the link
  itself; no run of that variant against a symlink is recorded. Valkey writes no symlinks;
  placing one takes write access to the volume.

**Single-pod clusters decide by persistence** (ADR 0032 D3, `singlePodDeferral`). A persistent
`spec.replicas: 1` pod still running as root is replaced at the upgrade, the repair running on
its way up, and once more when the repair has left the template (the second roll above) —
~~one restart~~ two restarts since 2026-09-26, data kept. One exception, read from the code and
not tested: if the template's sidecar image moved between the two replacements (another
operator upgrade in that window), the second is a sidecar-only drift of a rootless pod, which
`isSidecarOnlyChange` defers to the pod's next restart under `SidecarUpdatePending`
(ADR 0007 D6) — and the pod keeps the repair until then. A non-persistent one is **not**
replaced unless its Valkey image changed as well, because replacing it would discard the
dataset: it keeps running as root until it is deleted for another reason (a node drain, say —
a container restart keeps the pod spec), and `PodSecurityUpdatePending=True` (reason
`PodRunsAsRoot`) names it. Deleting the pod applies the posture at once and discards the
dataset. The condition is a level with one evaluator, is written
`False/PodSecurityUpdateApplied` only over a standing True, and emits no Event.

---

## 5. Validation story

There is **no admission webhook** in this project — no `ValidatingWebhookConfiguration`,
no `MutatingWebhookConfiguration`, nothing under `config/webhook`. Everything that
validates a `Valkey` object is CRD schema validation generated from the kubebuilder
markers in [`api/v1/valkey_types.go`](api/v1/valkey_types.go): enums
(`certManager.issuer.kind` ∈ {Issuer, ClusterIssuer}, `observer.logLevel`,
`podSecurity.seccompProfile.type` ∈ {RuntimeDefault, Localhost}),
defaults (`auth.secretPasswordKey: password`, `podDisruptionBudget.enabled: false`,
`tls.enabled: false`, `podSecurity.seccompProfile.type: RuntimeDefault`,
`podSecurity.userNamespaces: false`), types and required fields — and, since 2026-09-26, ~~one
CEL rule~~ two CEL rules, both on `SeccompProfileSpec` (below).

What that means in practice:

- **Cross-field rules are not enforced at admission — with one exception.** `spec.tls.secretName` and
  `spec.tls.certManager` are documented as mutually exclusive; nothing rejects a
  CR that sets both. The reconciler resolves it, the API server does not. The exception is
  the seccomp profile: a CEL rule on `SeccompProfileSpec` requires a non-empty
  `localhostProfile` exactly when `type` is `Localhost`, and the enum refuses `Unconfined`
  ([ADR 0033](docs/adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md)
  D1; envtest, green 2026-09-26). ~~The rule does not check the path's shape: an absolute path
  or a `..` element passes the CRD and is refused by the API server only when the operator
  writes the workload (upstream `validateSeccompProfileField`), which surfaces as
  `ReconcileBlocked/WriteFailed` (`reconcileBlockedReason`) — both read, neither tested here.~~
  *(Superseded 2026-09-26.)* A second rule refuses a `localhostProfile` that starts with `/` or
  holds a `..` element (`(^|/)[.][.](/|$)`, so `..` inside a file name passes), at CR admission
  instead of at the workload write ([`valkey_types.go`](api/v1/valkey_types.go); envtest rows for
  an absolute path, a leading, inner and trailing `..` and dots inside a name — ~~no recorded run
  yet~~ green in repeated runs on 2026-09-26, Kubernetes 1.29 API server). Which `Localhost` profile may be named at all is **not** admission validation: the API
  server accepts any well-formed path, and the reconciler refuses one its allow-list does not
  name (`ReconcileBlocked/SeccompProfileNotAllowed`, section 3).
- **A rejected CR write is a first-class runtime state, not an error path.** A
  third-party fail-closed webhook (Kyverno, OPA) that rejects the operator's writes
  is surfaced on the CR as the `ReconcileBlocked` condition with the rejecting
  webhook named in the message — the whole reason this ticket family exists.
- **The operator validates the data plane, not the input.** Its checks are about
  reachability, replication role and sync state; it trusts the CR — with one exception since
  2026-09-26, the `Localhost` seccomp allow-list above.

---

## 6. Rotation and change propagation

| Change | Propagates? | Mechanism |
|---|---|---|
| `spec.image`, resources, probes, config | Yes | Pod-spec hash / config hash on the pod template, failover-aware rolling update ([`ComputePodSpecHash`](internal/builder/statefulset.go), `ComputeConfigHash`) |
| cert-manager certificate renewal | **Yes**, since 2026-08-26 | The Secret content changes, the mount follows it, and a fingerprint of that content (`VKO_TLS_MATERIAL_HASH` in the carrier container of both StatefulSet pod templates) makes the rotation ride the failover-aware rolling update — on a tier of one or two Sentinels only since 2026-09-26, whose quorum guard had refused every delete of an available Sentinel ([ADR 0024](docs/adr/0024-the-sentinel-tier-reports-its-own-completion.md) D10). Processes this repo owns re-read their material instead and are exempt. **Whether `valkey-server` itself reloads is still not verified** — it is treated as pinning so that nobody has to find out ([ADR 0030](docs/adr/0030-rotating-certificates-rotate-the-instances-that-cannot-reload-them.md)) |
| `spec.tls.secretName` (different Secret) | Yes | The name is part of the pod spec, so the hash changes and the pods roll |
| **Password change inside the auth Secret** | **No** | See below |
| `spec.auth.secretName` (different Secret) | Yes | Same reason as the TLS Secret name |
| Workload `securityContext` (the operator upgrade onto [ADR 0032](docs/adr/0032-generated-pods-run-rootless.md)) | Yes, once — twice on a persistent data tier since 2026-09-26 — **except a non-persistent single pod** | Part of the built `PodSpec`, so both pod-spec hashes move: every multi-replica data tier and every Sentinel tier rides one failover-aware roll — a tier of one or two Sentinels one at a time ([ADR 0024](docs/adr/0024-the-sentinel-tier-reports-its-own-completion.md) D10) — and the observer Deployment follows through its own `securityContext` comparison. A persistent data tier then rolls a second time, replacing the pods created while the template carried the root ownership repair (`podCarriesRetiredRepair`, ADR 0032 D2), and only after the first roll has finalized (ADR 0032 D4, the ordering fix of section 4.5); a persistent single pod is therefore replaced ~~once~~ twice. A non-persistent `spec.replicas: 1` pod stays root until it is deleted, under `PodSecurityUpdatePending`, and a pod without a `pod-spec-hash` annotation is not recognised as outdated (section 4.5). The ADR 0033 fields of the same release (`privileged: false`, `enableServiceLinks: false`, the observer uid) ride these rolls and add none |
| `spec.podSecurity` (`seccompProfile`, `userNamespaces`) | Yes | Part of the built `PodSpec`, so both pod-spec hashes move and the tiers roll failover-aware; the observer Deployment follows through `podSecurityContextChanged`/`podHardeningChanged`. An explicit `RuntimeDefault` hashes like the omitted field and rolls nothing. A `hostUsers: false` the API server drops is reported, not silently lost (`ReconcileBlocked/UserNamespacesUnsupported`, section 4.5) |

**The password rotation gap, stated precisely.** The Secret is watched
([`findValkeyForSecret`, `valkey_controller.go:2861`](internal/controller/valkey_controller.go),
whose predicate `secretConcernsValkey` matches the auth Secret **and** the TLS Secrets of
both tiers, unified and user-provided — until 2026-08-26 it matched auth Secrets only, so a
certificate rotation enqueued nothing at all)
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
      cluster's Secrets. Since 2026-08-26 the TLS Secret is read on **every pass** of
      every TLS cluster, for the material fingerprint, so a filtered cache has one
      more consumer to satisfy. Options: a namespaced Role per watched namespace, or a
      cache filtered by label with the ClusterRole narrowed to match. Cost: the
      operator stops being install-and-forget for new namespaces. Carried as an open
      follow-up of T31 (2026-09-26) in its general form, a namespace-scoped operator mode
      ([ADR 0013](docs/adr/0013-operator-is-cluster-wide-privileged.md)).
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
- [x] **Give the workload pods a securityContext**
      ([ADR 0032](docs/adr/0032-generated-pods-run-rootless.md) D1, implemented 2026-09-26).
      Every data, Sentinel and observer pod template carries the five controls the operator's own
      Deployment has, plus `runAsUser`/`runAsGroup`/`fsGroup: 999` on data and Sentinel
      pods, with no option to turn it off (section 4.5). Two corrections to what this item
      asked for: `readOnlyRootFilesystem` holds on every container, not only "where the data
      path allows it", because every path a process writes is a mounted volume; and the item
      named four controls, omitting `allowPrivilegeEscalation: false`, which is set too.
      **Verified on a node locally only** (Kind with containerd, not in CI) — see "Do not
      treat the rootless posture as proven" below.
- [x] **State the rest of the pod-manifest hardening, on the generated pods and on the
      operator's own**
      ([ADR 0033](docs/adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md),
      implemented 2026-09-26, unreleased). `privileged: false` on every container,
      `enableServiceLinks: false` on every pod, the observer pinned to uid/gid/fsGroup 65532,
      the seccomp profile selectable as `RuntimeDefault` or `Localhost` and never `Unconfined`,
      an opt-in user namespace with a report when the API server drops it, a digest-pinned
      `spec.image` deployable, the exporter default pinned by digest, and the operator and hook
      pods on the same posture with `image.digest` (sections 4.4, 4.5) — and, decided later
      the same day, a default-deny allow-list of `Localhost` profiles plus a CEL rule on the
      path's shape (D9 and amended D1, section 3). Unit, lint, cyclo and integration green
      2026-09-26, and 8 of 8 mutations of the ADR 0033 code killed; the chart's non-default
      paths rendered by hand
      only; ~~**the e2e has not completed**~~ *(superseded 2026-09-26)*
      `TestE2E_PodHardening_UserNamespacesLocalhostSeccompAndDigest` ran on Kind: the Valkey 9
      suite failed it once, on its own owner assertion for the `/data` volume root (Kind's
      `hostPath` root is root-owned `0777`, and a cluster this operator built never ran the
      repair); the test now compares that owner before and after the move and has passed on
      Valkey 8 and, rerun alone, on Valkey 9 (item below). All of this predates the allow-list
      and the CEL path rule: with them, `make test-unit` is green again (2026-09-26, before the
      allow-list gate moved to the write), ~~while
      their envtest and e2e subtest have no recorded run.~~ *(superseded 2026-09-26)* their
      envtest rows were green in repeated runs (Kubernetes 1.29 API server), 7 of 7 mutations of
      the D9 code were killed (the gate-position mutations of the later move included), and the
      ~~final~~ run on one image built from the ~~finished~~ code with the moved gate was green:
      both full suites 53/53, the fleet upgrade from 1.12.8, and two extra Valkey 8 runs of this
      test and `TestE2E_PodSecurity_RestrictedNamespace`, the allow-list refusal subtest green on
      every run (item below). *(Amended 2026-09-26: that run was not the last. The final run, on
      an image that adds ADR 0025 D9's own clock and the single failover write, was green for
      this test and the restricted-namespace test on both lines and in two more Valkey 8 runs;
      its one failure was `TestE2E_SidecarFailoverDrainMaster` on Valkey 9, diagnosed as a defect
      of that test's fixture — item below.)* The chart's own `localhostProfile` path check (section 4.4) was rendered by
      hand only. ~~Of the CI-parity gates on the final code, only `make generate-all` (no diff, in
      a clean copy with a freshly installed controller-gen v0.22.0) and `make test-release-tooling`
      have a result; lint, cyclo, gosec, vuln, the unit and integration coverage targets and the
      image-tools check on the final code are not claimed, and CI has not seen the change.~~
      *(Superseded 2026-09-26.)* The CI-parity gates ran in a clean copy of the code with the
      moved gate, before ADR 0025 D9 gained its own clock, and were all green:
      `make generate-all` (no diff with a freshly installed controller-gen v0.22.0), `make lint`
      (golangci-lint v2.14.0, 0 issues), `make cyclo`, `make gosec` (v2.29.0, 0 issues),
      `make vuln` (no vulnerabilities), the unit and integration coverage targets,
      `make test-image-tools` and `make test-release-tooling`. The code changed after that run —
      ADR 0025 D9's own clock, the single failover write, a sidecar e2e fixture — and the rerun on
      the final code has no result yet. CI has not seen the change.
- [ ] **Finish the rootless migration where it cannot finish itself**
      ([ADR 0032](docs/adr/0032-generated-pods-run-rootless.md) D3, D7, section 4.5). Before the
      upgrade, `chown -R 999:999` every Valkey volume on NFS exported with `root_squash`, on
      the server side — no pod can re-own it, and the first replacement never starts. After
      it, three kinds of pod still run as root: a non-persistent single pod
      (`PodSecurityUpdatePending` names it; deleting it discards its data), a pod so old that
      it carries no `pod-spec-hash` annotation (delete it), and the pods of a tier whose
      roll holds on a replacement that never became available (`PodAvailabilityStalled`).
- [ ] **Enforce Pod Security `restricted` on each namespace once it is migrated**
      ([ADR 0032](docs/adr/0032-generated-pods-run-rootless.md) D6). The posture is a
      property of what the operator renders; the label makes the API server refuse
      everything else, including a container-level `runAsUser: 0` or
      `seccompProfile: Unconfined` the subset drift comparison does not converge back
      (section 4.5). List the violators first with
      `kubectl label --dry-run=server --overwrite ns <ns> pod-security.kubernetes.io/enforce=restricted`,
      then label for real. ~~Expect the dry run to name the persistent data pods the migration
      roll created: their spec keeps the completed repair (section 4.5).~~ *(Superseded
      2026-09-26: a second roll replaces those pods,
      [ADR 0032](docs/adr/0032-generated-pods-run-rootless.md) D2.)* Once every persistent
      data tier has finished its second roll, the dry run should name no generated pod beyond
      the three kinds of the item above — derived from the unit matrix and
      `podCarriesRetiredRepair`, not measured with a dry run after a migration. A data pod it
      names for `fix-data-ownership` belongs to a tier whose second roll is still running or
      held (`PodAvailabilityStalled`), or is a persistent single pod whose second replacement
      was deferred as sidecar-only (`SidecarUpdatePending`, section 4.5). Such pods do not block the label — enforcement acts at
      admission and evicts no running pod, and their replacements come from the repair-free
      template — so the precondition is the template, not the pods: no data StatefulSet in
      the namespace may still carry `fix-data-ownership`. Not
      earlier: a data template carrying the migration repair is `baseline`, not
      `restricted`, so the pods the roll would create are refused at admission and the roll
      waits on an absent pod (`PodRecreationStalled`). The operator does not label
      namespaces. What the label does **not** police: which `Localhost` seccomp profile a pod
      names — `restricted` accepts every one (item below).
- [ ] **Do not treat the rootless posture as proven on your runtime.** As of 2026-09-26 it
      is guarded against the Pod Security checks in the unit tier (`make test-unit`, green
      2026-09-26) and against API-server defaulting in envtest ~~(no run recorded)~~
      (`make test-integration`, green 2026-09-26 — recorded in the T31 ticket; corrected the
      same day), and run
      under `--user 999:999 --read-only --cap-drop ALL --security-opt no-new-privileges`
      against both pinned Valkey lines in Docker
      (`make test-image-tools`, green 2026-09-26). **On a node it ran locally only, not in
      CI** — ~~the branch has not been through the pipeline~~ *(superseded 2026-09-26: the
      branch was pushed as `e2ce8bb`, whose pipeline failed `Generated Manifests Up To Date` and
      `Integration Tests (envtest)`, both since fixed, and whose E2E result this document does
      not record; `e2ce8bb` does not contain the ADR 0033 changes)*: on Kind (control plane + 3 workers,
      Kubernetes v1.36.1, containerd) on 2026-09-26, `make test-e2e` was green on both Valkey
      lines (51/51 each). That includes `TestE2E_PodSecurity_RestrictedNamespace`: the
      namespace refused an unrestricted pod (server-side dry run), every generated pod was
      admitted under `enforce=restricted` and became Ready, the `valkey` and `sentinel`
      containers showed `Uid 999`, `CapEff 0`, `CapBnd 0`, `NoNewPrivs 1`, and the sidecar's
      projected token was measured on the node at `0640`, owner and group 999 (kubelet
      rewrote the `0644` `DefaultMode` under `fsGroup`). The fleet-upgrade e2e (the migration
      itself) passed once, from released chart 1.12.8 with its amd64 image under emulation
      (the default start, 1.10.48, cannot run on the arm64 host used): every tier converged
      rootless, every persistent pod ran the repair before it left the template, the migrated
      persistent masters wrote and snapshotted without `MISCONF`, and the non-persistent single
      pod kept running as root under `PodSecurityUpdatePending`. It is still not a CI job.
      Both runs predate the two decisions taken later that day — the second roll that sheds
      the repair (ADR 0032 D2) and the serial roll of a tier of one or two Sentinels
      ([ADR 0024](docs/adr/0024-the-sentinel-tier-reports-its-own-completion.md) D10). Those
      are unit-tested (`make test-unit`, green 2026-09-26); ~~the fleet-upgrade e2e as changed
      for the second roll [...] and the new `TestE2E_RollingUpdate_TwoSentinelsRollSerially`
      have **not run**.~~ *(Superseded 2026-09-26: the changed fleet-upgrade e2e has since
      run.)* The fleet-upgrade e2e as changed for the second roll (it waits for the second
      roll, asserts that every entry of `/data` and `/data/appendonlydir` on the persistent
      volumes is owned by 999, and counts two data-tier rolls on a persistent multi-replica
      tier and one on a non-persistent one) has run twice, and each
      run found an ordering defect of the second roll — the second roll overtaking the first,
      then the repair stranded on the template after the first roll completed (section 4.5,
      ADR 0032 D4). Both are fixed and unit-tested; ~~**the rerun after both fixes, both full
      suites (which carry `TestE2E_RollingUpdate_TwoSentinelsRollSerially`) and the new
      `TestE2E_PodHardening_UserNamespacesLocalhostSeccompAndDigest` are running and have not
      completed** — none of them counts as run.~~ *(Superseded 2026-09-26: they have run, on
      Kind with Kubernetes 1.36.1, containerd 2.3.1, runc 1.4.2, Linux 6.10.)* The fleet-upgrade
      rerun after both fixes, from 1.12.8, was green, with exactly two `RollingUpdateComplete`
      per persistent tier and nothing rolling after the second roll. The full suite was 53/53 on
      Valkey 8 and 52/53 on Valkey 9, the one failure the hardening test's own `/data` owner
      assertion (item above; corrected, then green rerun alone on Valkey 9), and
      `TestE2E_RollingUpdate_TwoSentinelsRollSerially` was green on both lines in an earlier run.
      All of these predate the `Localhost` allow-list and the CEL path rule (ADR 0033 D9 and
      amended D1); ~~a
      rerun of the fleet upgrade and both full suites with them is in progress and does not
      count as run.~~ *(Superseded 2026-09-26.)* The rerun with them went red on Valkey 8, and not
      on anything this document covers: during the roll's own Sentinel failover the rolling
      update's split-brain resolver took Sentinel's pre-switch master as the authority and demoted
      the replica Sentinel was promoting, and the reset-and-retrigger cycle ~~ran for more than ten
      minutes~~ ran ten cycles, until the test's ten-minute wait gave up *(corrected 2026-09-26
      against the operator log, as ADR 0025 D9 records it)*, on the hardening test's
      observer-enabled cluster — a defect already on `main`, now fixed: in that window the double
      master is reported, not resolved
      ([ADR 0025](docs/adr/0025-a-split-brain-warning-means-one-that-did-not-resolve-itself.md)
      D9) — *(amended 2026-09-26)* for at most 90 s from the failover timestamp, which the
      operator now writes in the same update as the state
      ([ADR 0010](docs/adr/0010-every-rolling-update-wait-is-bounded.md) D14) and which the
      forced-`REPLICAOF` timeout rewrites, re-opening the window after a pass that ran the
      resolver, with no cap on how often (read, not measured); a state without a
      timestamp is no window, and past 90 s the resolver acts again in the same state (unit-tested,
      not measured). ~~The final run~~ The next run *(amended 2026-09-26: not the
      last)*, on one image built from the code with the allow-list, the CEL path
      rule, the moved allow-list gate and ADR 0025 D9 (Kind, Kubernetes 1.36.1, containerd 2.3.1,
      runc 1.4.2, Linux 6.10): the fleet upgrade from 1.12.8 green; the full suite 53/53 on
      Valkey 9 and 53/53 on Valkey 8, which carry `TestE2E_RollingUpdate_TwoSentinelsRollSerially`
      and `TestE2E_PodSecurity_RestrictedNamespace`; two extra Valkey 8 runs of
      `TestE2E_PodHardening_UserNamespacesLocalhostSeccompAndDigest` and
      `TestE2E_PodSecurity_RestrictedNamespace` green. In the operator log of that run the
      hardening test's cluster had four Sentinel failover triggers (one per run), no demotion and
      no failover timeout, against eleven, eleven and nine in the red run *(split per leg
      2026-09-26: ten, ten and nine on Valkey 8, one, one and none on Valkey 9)*.
      *(Added 2026-09-26.)* The final run, on one image built from that code plus D9's own clock
      and the single failover write, same Kind setup: the fleet upgrade from 1.12.8 green; the
      full suite 53/53 on Valkey 8 and 52/53 on Valkey 9; two extra Valkey 8 runs of the hardening
      and restricted-namespace tests green. The one failure, `TestE2E_SidecarFailoverDrainMaster`,
      is diagnosed as the test's own: its delete subtest passed in 0.38 s because
      every wait was already met by the terminating old master, which kubelet keeps Ready
      ([ADR 0026](docs/adr/0026-a-pod-being-deleted-is-not-available.md)), and "data survives
      failover" then picked the dying pod as the master, and its `DBSIZE`, sent by pod name, read 0
      from the empty replacement. That diagnosis is read
      from the test code and its timing and supported by a watcher on green runs; the red run's
      pod logs were lost with the CR, and its operator log shows no operator action between the
      cluster's creation and its deletion. The test now waits for the replacement by UID
      ([ADR 0017](docs/adr/0017-test-and-ci-policy.md) D50) and was green 8 of 8 alone on
      Valkey 9; five other sites of the same shape, not yet audited, are ticket T34.
      Trigger and demotion counts for the final run are not recorded here. Still locally, not in
      CI. Not covered at all: CRI-O's smaller default capability set, and
      OpenShift — **its `restricted-v2` SCC refuses a fixed `runAsUser: 999` outside the
      namespace's UID range**, so these pods are not admitted there; nothing in this
      repository targets OpenShift today.
- [ ] **Turn on user namespaces where every node can honour them**
      ([ADR 0033](docs/adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md)
      D2, D3). `spec.podSecurity.userNamespaces: true` per Valkey resource, and
      `podSecurity.userNamespaces: true` in the chart for the operator and hook pods — two
      separate switches, both off by default. Check first: Kubernetes 1.33 (or the
      `UserNamespacesSupport` gate), containerd 2.0 or CRI-O 1.25, Linux 6.3, and a data-volume
      file system with idmap support — not NFS. Turn it on for one cluster and watch it: an
      API server that drops the field shows `ReconcileBlocked=True/UserNamespacesUnsupported`
      and phase `Error` (the pods keep running without the namespace); a node that cannot
      honour it holds the roll on a replacement that never starts (`PodAvailabilityStalled`),
      and a single pod is not reported. For the operator and the hook no Valkey resource
      reports either case: a dropped field is silent, and a pod a node cannot start shows
      it only in its own status and events — on `helm upgrade` the hook runs first, so the
      upgrade fails there (read from the chart, not run). ~~Not yet measured on any
      node: the e2e that does so has not completed,~~ *(superseded 2026-09-26)* Measured on one
      node setup only, locally and not in CI: Kind with Kubernetes 1.36.1, containerd 2.3.1, runc
      1.4.2 and Linux 6.10, where
      `TestE2E_PodHardening_UserNamespacesLocalhostSeccompAndDigest` moved a persistent Sentinel
      cluster into a user namespace in one failover-aware roll, found the `valkey` container of
      each data pod and the `sentinel` container of each Sentinel pod in their own user namespace
      and the dataset intact through the idmapped mount
      (section 4.5); CRI-O is not covered, and the migration
      repair inside a user namespace is not covered at all (section 4.5). It bounds an escape
      from the container; it changes nothing an attacker can do inside the pod.
- [ ] ~~**Treat every `Localhost` seccomp profile on a node as selectable by every CR
      author**~~ **List only the `Localhost` seccomp profiles you would accept for every Valkey
      pod, and treat each listed file as trusted** *(re-decided 2026-09-26)*
      ([ADR 0033](docs/adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md)
      D1, D9, section 3). ~~Install on nodes that run Valkey pods only profiles you would accept on
      any of them, and never leave a permissive test profile there — the e2e fixture is
      allow-by-default with a short deny list, weaker than `RuntimeDefault`. Pod Security
      `restricted` does not distinguish them. If the choice must be narrower than "whatever
      is on the node", an admission policy — on `spec.podSecurity.seccompProfile` of the CR,
      or on the `localhostProfile` of the pods it generates — has to narrow it; the operator
      has no allow-list.~~ *(Superseded 2026-09-26: the operator enforces an allow-list.)*
      `valkeyPodSecurity.allowedSeccompLocalhostProfiles` (`--allowed-seccomp-localhost-profiles`)
      is empty by default, which refuses every `Localhost` profile — the operator then writes no
      workload of a Valkey resource naming one and reports
      `ReconcileBlocked=True/SeccompProfileNotAllowed`; `RuntimeDefault` is always allowed.
      Add a path only for a profile at least as strict as you accept for every Valkey pod in
      the cluster: the list is operator-wide, so every CR author may pick any entry, and
      neither the allow-list nor Pod Security `restricted` looks at what the file enforces.
      Keep every listed file identical on every node that runs Valkey pods and guard who may
      write the kubelet's seccomp directory there — that principal decides what a listed name
      means. Never list a permissive test profile outside a test cluster: the e2e lists its
      fixture, which is allow-by-default with a short deny list and weaker than
      `RuntimeDefault` ([`test/e2e/helm-values.yaml`](test/e2e/helm-values.yaml)). For pods and
      StatefulSets the operator does not write, an admission policy remains the control. Open
      follow-up (T31): a recommended `Localhost` profile
      for the generated containers, recorded for example with the Security Profiles Operator —
      none ships today, and a profile must allow the repair's `chown`.
- [ ] **Pin the operator image by digest.** Set `image.digest` (`sha256:<64 hex>`, checked at
      render time); the same reference then reaches `--operator-image`, so the sidecar and
      observer containers the operator generates are pinned too (section 4.4). Open
      follow-up: the release pipeline does not stamp the digest of the image it pushes into
      the chart, so the default stays empty and pinning is left to the installer.
- [ ] **Pin `spec.image` and `spec.metrics.image` by digest.** A digest in `spec.image` is
      deployable since 2026-09-26 (`repo:tag@sha256:…` keeps the tag as the version label; a
      digest-only reference yields an empty one). The exporter default is already pinned to
      `v1.66.0`'s index digest — open follow-up: Renovate does not track
      `DefaultMetricsExporterImage`, so that pin ages until someone moves it by hand.
- [ ] **Know that a cpu/memory `ResourceQuota` still refuses the data pods.** Set
      `spec.resources`, `spec.metrics.resources`, `spec.observer.resources` and, new,
      `spec.sentinel.resources` (every Sentinel container, the init container included).
      The sidecar and the data pod's init containers state no requests or limits and have no
      field; decided so on purpose (ADR 0033 D7: no guessed defaults, since an OOM-killed
      sidecar breaks the drain promotion), and open as a follow-up for quota namespaces.
- [x] **Give the observer its own ServiceAccount and stop mounting its token**
      ([ADR 0012](docs/adr/0012-the-sidecar-records-its-drain-promotion-on-the-pod.md) D8 step 2,
      done 2026-08-21). `<cr-name>-observer` is bound to no Role and the pod sets
      `automountServiceAccountToken: false`.
- [x] **Stop mounting the sidecar token into the `valkey` and `exporter`
      containers** ([ADR 0012](docs/adr/0012-the-sidecar-records-its-drain-promotion-on-the-pod.md)
      D8 step 4, done 2026-08-27). The row used to say this needed moving the token
      out of the pod. It did not: `automountServiceAccountToken: false` plus a
      hand-declared `projected` volume mounted into the `sidecar` container is the
      supported pattern, GA since Kubernetes 1.20. `valkey`, `exporter` and both init
      containers now carry no token; the Sentinel pod carries none at all. The sidecar
      container still holds the grant and must.
- [ ] **Add egress NetworkPolicies.** Today's policies are ingress-only, so a
      compromised data pod can talk to anything, the API server included.
- [ ] **Watch for a certificate roll that never starts.** A rotation is propagated by
      replacing the pods that cannot reload their material, and the previous
      certificate stays valid for the cert-manager overlap — 30 days at defaults — so
      the roll is never urgent. What that window does not cover is a roll that does not
      happen at all. The `TLSMaterialStale` condition reports it per cluster and the
      shipped `ValkeyTLSMaterialStale` alert fires after **72 h**; the chart's
      `PrometheusRule` is **default off**, so this entry stays unchecked until it is
      enabled. Read it as a liveness check on the roll, **not** as an integrity check on
      the material: the fingerprint it compares is forgeable by whoever can write
      the Secret, by collision against a 32-bit digest (see section 2). That is
      **accepted permanently** as of 2026-08-27 — a wide digest would remove the
      collision and leave a substitution indistinguishable from a legitimate
      rotation, which no observer can act on. See
      [ADR 0030](docs/adr/0030-rotating-certificates-rotate-the-instances-that-cannot-reload-them.md) D11.

- [ ] **Do not extend the TLS material fingerprint to low-entropy secrets — at any
      digest strength.** The `VKO_TLS_MATERIAL_HASH` record is a digest of Secret
      content, published on the pod template and readable with `get pods`. Over a
      private key that is harmless, because nobody can enumerate 2048-bit RSA keys;
      over the cluster password it would be a brute-forceable oracle, because an
      attacker holding the digest guesses candidates and hashes them. **The security
      parameter is the entropy of the input, not the width of the digest** — this row
      used to say "32-bit", which read as though SHA-256 would make the password case
      safe. It would not; it would only make the guessing marginally slower. Moving
      the carrier into the pod spec
      ([ADR 0031](docs/adr/0031-a-record-the-operator-trusts-lives-in-pod-spec.md))
      changed who can *write* it and nothing about who can read it. ADR 0030 D11
      bounds the exception to TLS material, and the password rotation gap in
      section 6 must not be closed by copying it.

- [ ] **Rotate the cluster password by hand until propagation exists.** Changing the auth
      Secret rolls nothing, and the operator starts authenticating with the new password
      against pods that still expect the old one (section 6). Open follow-up (T31, ADR 0030
      D11): propagation that does not publish a digest of the password.
- [ ] **Require client certificates where the deployment can.**
      `tls-auth-clients optional` means TLS authenticates the server only.
- [ ] **Give the probes, the sidecar, the exporter and the observer least-privilege Valkey
      ACL users.** Open follow-up (T31, ADR 0016): today every component authenticates with
      the one cluster password and full rights, so the exporter — a third-party image — holds
      the same authority as a client that may `FLUSHALL`.
- [ ] **Pin `enable-debug-command` and `enable-module-command` to `no` in the generated
      config.** Open follow-up (T31): the config builder renders neither
      ([`configmap.go`](internal/builder/configmap.go), verified by grep), so whatever the
      image defaults to applies. Believed `no` since Redis 7; **not re-checked for either
      pinned Valkey line**.
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
      works without changing either. Open follow-up (T31): controller-runtime's
      `WithAuthenticationAndAuthorization` filter — still the separate, unmade trade of D10,
      which needs a `TokenReview`/`SubjectAccessReview` grant.
- [ ] **Add a NetworkPolicy for the operator namespace.** The chart ships none, so nothing
      restricts who reaches the operator pod's `:8080` and `:8081`, or where it connects.
      Open follow-up (T31): a chart-rendered policy, default off — ingress to metrics and
      health only, egress to the API server and the Valkey ports.
- [ ] **Restrict who may `create valkeys`.** A CR author chooses the image the
      cluster runs and the name every generated object gets, and generated names
      collide with existing objects by design.
      [ADR 0006](docs/adr/0006-delete-only-what-the-operator-owns.md) closed the
      deletes and [ADR 0020](docs/adr/0020-write-only-what-the-operator-owns.md)
      closed every write, so a collision is now refused and reported rather than
      acted on. What the guards do **not** undo is the image choice, the objects an
      earlier release already adopted, or the pod door (section 3). The CR-name
      grant stays the control that bounds all three, and any new write or delete by
      generated name needs the same provenance discipline. Since 2026-09-26 a CR author
      also chooses the seccomp profile ~~among those on the nodes~~ among the `Localhost`
      profiles the operator's allow-list names, none by default *(amended 2026-09-26, ADR 0033
      D9)* (section 3, item above).
- [ ] **Disable the pre-upgrade hook (`preUpgradeHook.enabled: false`) unless a
      migration needs it**, or accept a cluster-wide CRD write grant during every
      upgrade.
- [ ] **Render the chart in CI.** The chart's refusals — a seccomp type other than
      `RuntimeDefault`/`Localhost`, a `Localhost` without a path or a path without it, an
      `image.digest` that is not `sha256:<64 hex>`, and, added later on 2026-09-26, an entry of
      `valkeyPodSecurity.allowedSeccompLocalhostProfiles` that is empty, starts with `/`, holds
      a `,` or a `..` element, as well as an operator/hook `podSecurity.seccompProfile.localhostProfile`
      that starts with `/` or has a `..` element — and the operator and hook pod posture
      were checked with `helm lint` and `helm template` by hand on 2026-09-26. In CI ~~only the
      default values are rendered~~ the chart is rendered only by the e2e job's `helm install`
      (`.github/workflows/release.yml`), with `test/e2e/helm-values.yaml` — image, resources,
      leader election and the non-empty allow-list; `podSecurity` and `image.digest` stay at
      their defaults *(amended 2026-09-26)*. Open follow-up (T31, ADR 0017): no CI gate renders
      the digest, user-namespace or `Localhost` paths or checks a refusal, so a template change
      that breaks one of them would not turn a PR red. On the default path, a change that drops
      a control Pod Security `restricted` requires would be caught by the new e2e subtest that
      dry-runs that label on the operator namespace — ~~which has not run yet~~ green on Kind
      2026-09-26, and on every run of the final image, locally, not in CI; one that drops
      `enableServiceLinks: false`, `privileged: false`, the read-only root filesystem or the
      pinned uid would not (read from the test, not run).
