# Architecture

What runs where, and which Kubernetes objects one `Valkey` resource turns into. The order in
which a reconcile pass writes those objects is [reconcile-loop.md](reconcile-loop.md); which
file builds each of them is [package-map.md](package-map.md); the exact generated names are the
[naming conventions](../../README.md#naming-conventions) in README.md.

## The managed resources

```
┌─────────────────────────────────────────────────────────────────┐
│ Kubernetes Cluster                                              │
│                                                                 │
│  ┌──────────────────┐     watches      ┌────────────────────┐  │
│  │ Valkey Operator   │ ◄──────────────► │ Valkey CRD         │  │
│  │ (Deployment)      │                  │ (vko.gtrfc.com/v1) │  │
│  └────────┬─────────┘                  └────────────────────┘  │
│           │ creates/manages                                     │
│           ▼                                                     │
│  ┌─────────────────────────────────────────────────────────┐   │
│  │ Managed Resources                                        │   │
│  │                                                          │   │
│  │  ┌─────────────┐  ┌────────────┐  ┌────────────────┐   │   │
│  │  │ StatefulSet  │  │ ConfigMaps │  │ Services       │   │   │
│  │  │ (Valkey)     │  │ (master,   │  │ (headless,     │   │   │
│  │  │              │  │  replica)  │  │  client)       │   │   │
│  │  └─────────────┘  └────────────┘  └────────────────┘   │   │
│  │                                                          │   │
│  │  ┌─────────────┐  ┌────────────┐  ┌────────────────┐   │   │
│  │  │ StatefulSet  │  │ ConfigMap  │  │ Service        │   │   │
│  │  │ (Sentinel)   │  │ (sentinel) │  │ (sentinel-     │   │   │
│  │  │              │  │            │  │  headless)     │   │   │
│  │  └─────────────┘  └────────────┘  └────────────────┘   │   │
│  │                                                          │   │
│  │  ┌─────────────┐  ┌────────────────────────────────┐   │   │
│  │  │ Certificate  │  │ Certificate (Sentinel)         │   │   │
│  │  │ (Valkey TLS) │  │ (if sentinel + TLS enabled)    │   │   │
│  │  └─────────────┘  └────────────────────────────────┘   │   │
│  │                                                          │   │
│  │  ┌─────────────────────────────────────────────────┐    │   │
│  │  │ Deployment (Observer)                            │    │   │
│  │  │ (if observer.enabled — health checks + metrics)  │    │   │
│  │  └─────────────────────────────────────────────────┘    │   │
│  └─────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────┘
```

## What the diagram does not show

The diagram predates several object families and one TLS option. Verified against
[`internal/controller/valkey_controller.go`](../../internal/controller/valkey_controller.go)
(`resourceReconcileSteps` and the functions it calls) on 2026-09-27:

- **The client Services are more than one.** Beside the headless Service there is always a
  `-rw` Service selecting the pod labelled master, and with more than one replica a `-r` and
  an `-all` Service; `reconcileServices` also deletes Services of an older naming scheme. With
  metrics enabled a metrics Service joins them, unless `spec.metrics.service.enabled` is false
  and no ServiceMonitor asks for it.
- **The Sentinel Certificate is conditional twice.** Certificates exist only when cert-manager
  issues them (`spec.tls.certManager`); with a user-provided Secret the operator creates none.
  The second one is written only with Sentinel enabled **and** `spec.tls.unifiedCertificate`
  off; with it on, `reconcileLegacySentinelCertificateCleanup` removes a Sentinel Certificate
  and Secret an earlier configuration left behind, once every Sentinel pod has moved to the
  shared Secret.
- **The sidecar's RBAC.** Every cluster gets its own sidecar ServiceAccount, Role and
  RoleBinding (`reconcileSidecarRBAC`), written before the data StatefulSet.
- **The observer has a ServiceAccount of its own**, with no Role and no RoleBinding.
- **Opt-in objects:** the two PodDisruptionBudgets (`spec.podDisruptionBudget`), the Valkey,
  Sentinel and observer NetworkPolicies (`spec.networkPolicy`), and a Prometheus-Operator
  ServiceMonitor (`spec.metrics.serviceMonitor`), skipped with a log line when its CRD is not
  installed.
- **Every one of them carries a controller reference to the `Valkey`**, which is what the
  operator checks before it writes or deletes one
  ([ADR 0020](../adr/0020-write-only-what-the-operator-owns.md),
  [ADR 0006](../adr/0006-delete-only-what-the-operator-owns.md)); deleting the CR leaves the
  cleanup to the garbage collector.

## One binary, four processes

Everything the operator runs is the `manager` binary from [`cmd/main.go`](../../cmd/main.go),
in one of four modes chosen by its first argument:

| Mode | Runs as | What it does |
|---|---|---|
| operator (no subcommand) | the chart's Deployment | The controller-runtime manager with the `Valkey` reconciler and the metrics collector |
| `sidecar` | a container in every data pod | Labels its pod `instanceRole=master` or `replica`, answers `/readyz` and `/healthz`, and on SIGTERM runs the drain handler, which fails over when its pod is the master ([`internal/sidecar/`](../../internal/sidecar/)) |
| `observer` | the observer Deployment, one per `Valkey` with `spec.observer.enabled` | Polls the cluster from outside — master discovery, replication, a write and read, Sentinel quorum — and serves `/readyz`, `/healthz` and `/metrics` ([`internal/observer/`](../../internal/observer/)) |
| `migrate` | the chart's pre-upgrade hook Job | Writes missing field defaults into every existing `Valkey` and exits ([`cmd/migrate/`](../../cmd/migrate/)) |

The sidecar and the observer therefore run the operator's image (`--operator-image`), while
the Valkey and Sentinel containers run `spec.image`.

## What runs in a pod

**A data pod** (the Valkey StatefulSet) runs the `valkey` container, the `sidecar` container
and, with `spec.metrics.enabled`, the `exporter` container. Its init containers are
`check-data-writable` when persistence is on, `fix-data-ownership` only while a persistent
StatefulSet migrates from pods that ran as root
([ADR 0032](../adr/0032-generated-pods-run-rootless.md) D2), and `init-config-selector` when
the cluster has Sentinel or more than one replica — it chooses the master or replica
configuration the pod boots with.

**A Sentinel pod** runs the `sentinel` container on the same `spec.image`, after the
`init-sentinel-config` init container has written its configuration and pinned its identity
([ADR 0022](../adr/0022-sentinel-identity-is-pinned-to-the-pod.md)).

Both StatefulSets use `updateStrategy: OnDelete` and `podManagementPolicy: Parallel`: a pod is
replaced when the operator deletes it, never by the StatefulSet controller
([ADR 0007](../adr/0007-failover-aware-rolling-update.md)).
