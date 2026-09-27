# Trust boundaries

Who acts in an installation of the Valkey operator, which identity each one runs under,
what each is trusted with, and the two boundaries that matter. What every grant permits,
rule by rule, is [privilege footprint](privilege-footprint.md); where the cluster password
and the TLS material go is [secrets and TLS](secrets-and-tls.md).

## Principals and what each is trusted with

| Principal | Identity | Scope | Trusted with |
|---|---|---|---|
| **Operator manager** | ServiceAccount `<release>` in the release namespace, bound by a **ClusterRoleBinding** ([`clusterrolebinding.yaml`](../../deploy/helm/valkey-operator/templates/clusterrolebinding.yaml)) | **Cluster-wide, all namespaces** | Every rule in [the privilege footprint](privilege-footprint.md). Reads every Secret in the cluster, writes RBAC, deletes pods and Secrets |
| **Pre-upgrade hook** | ServiceAccount `<release>-upgrade`, cluster-wide, created and deleted per `helm upgrade` ([`pre-upgrade-rbac.yaml`](../../deploy/helm/valkey-operator/templates/pre-upgrade-rbac.yaml)) | Cluster-wide, lifetime of the hook Job | `valkeys` get/list/patch/update and `customresourcedefinitions` get/list/patch/update |
| **Sidecar** | ServiceAccount `<cr-name>-sidecar`, one per Valkey CR ([`BuildSidecarServiceAccount`](../../internal/builder/rbac.go)) | **This cluster's own data pods**, by name: `pods` ~~patch~~ get and patch with `resourceNames` ([the per-instance sidecar Role](privilege-footprint.md#the-per-instance-sidecar-role)) | Patching `instanceRole` on its own pod and the drain stamp on a peer pod, and reading whether that peer is terminating *(corrected 2026-09-27: the Role grants `get` as well as `patch`, `BuildSidecarRole`)* |
| **Observer** | Its **own** ServiceAccount `<cr-name>-observer`, bound to no Role, with `automountServiceAccountToken: false` ([`BuildObserverServiceAccount`](../../internal/builder/observer.go)) | None — no Role, and no token mounted | Nothing — it makes no Kubernetes API call at all (verified: no `client-go` import in `internal/observer` or `cmd/observer`) |
| **Valkey pods** | The pod runs as `<cr-name>-sidecar`, but the token reaches the **`sidecar` container only**: `automountServiceAccountToken: false` plus a projected volume mounted into that one container ([`sidecarTokenVolume`](../../internal/builder/statefulset.go)) | Same as the sidecar row, for that container | `valkey`, `exporter` and every init container hold no Kubernetes credential (the cluster password is [secrets and TLS](secrets-and-tls.md#the-cluster-password)); the migration-only root repair ([rootless migration](rootless-migration.md#fix-data-ownership-the-one-root-process)) holds neither — no token and no environment |
| **Sentinel pods** | The namespace `default` ServiceAccount, with `automountServiceAccountToken: false` ([`sentinel.go`](../../internal/builder/sentinel.go)) | None — no token mounted | Nothing — Sentinel pods carry no labeler sidecar and never call the Kubernetes API |
| **CR author** | Any principal with `create valkeys` in a namespace | That namespace | Chooses images, the auth Secret name, the TLS mode, and since 2026-09-26 the pods' seccomp profile ~~among those on the nodes~~ among the `Localhost` profiles the operator's allow-list names, none by default *(amended 2026-09-26, ADR 0033 D9)*, and whether they run in a user namespace — see [isolation and tenancy](isolation-and-tenancy.md#what-does-not-hold) for what that buys them |

```
        cluster scope                          namespace scope
  ┌───────────────────────────┐        ┌──────────────────────────────────┐
  │  ClusterRole              │        │  Role <cr>-sidecar               │
  │  valkey-operator-role     │        │  pods: get, patch                │
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

## The two boundaries that matter

1. **Operator ↔ workload.** The operator is the only component that can change
   the cluster topology (`REPLICAOF`), and it is cluster-wide. A compromised
   operator is a cluster-wide compromise ([privilege footprint](privilege-footprint.md)).
2. **Sidecar ↔ operator.** The sidecar cannot write the CR — deliberately
   ([ADR 0012](../adr/0012-the-sidecar-records-its-drain-promotion-on-the-pod.md)) — so it
   reports a promotion it performed by patching a **pod annotation**
   (`vko.gtrfc.com/drain-promoted-at`, [`internal/common/annotations.go`](../../internal/common/annotations.go)).
   The operator consumes that annotation as evidence and may issue a destructive
   `REPLICAOF` on the strength of it. Everything that can patch a pod in the
   namespace can therefore influence a topology decision — see [what does not hold](isolation-and-tenancy.md#what-does-not-hold).

## What this does not cover

The table lists the identities the chart and the operator create, plus the CR author. It
does not model principals the operator does not create — a cluster administrator, a node,
whoever may write the auth or TLS Secret, or whoever may create or patch pods in a namespace
directly. The last two appear where their mechanism is: [TLS material](secrets-and-tls.md#tls-material)
for the Secret writer, [what does not hold](isolation-and-tenancy.md#what-does-not-hold) and
[the `Localhost` allow-list](seccomp-profiles.md#the-localhost-allow-list) for a direct pod
writer. The open gaps of each boundary are stated on the page of the mechanism that has them,
not here.
