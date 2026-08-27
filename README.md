# Valkey Operator

A Kubernetes operator for deploying and managing production-grade [Valkey](https://valkey.io/) instances — standalone or highly available with Sentinel.

[![Testing](https://github.com/guided-traffic/valkey-operator/actions/workflows/release.yml/badge.svg)](https://github.com/guided-traffic/valkey-operator/actions/workflows/release.yml)
[![Coverage](https://img.shields.io/endpoint?url=https://raw.githubusercontent.com/guided-traffic/valkey-operator/main/.github/badges/coverage.json)](https://github.com/guided-traffic/valkey-operator)
[![Go Report Card](https://goreportcard.com/badge/github.com/guided-traffic/valkey-operator)](https://goreportcard.com/report/github.com/guided-traffic/valkey-operator)

## Features

- **Standalone & HA modes** — single-node or multi-node with automatic Sentinel deployment
- **TLS encryption** — full TLS for Valkey, replication, and Sentinel via cert-manager or user-provided Secrets
- **Dual-port mode** — optional `allowUnencrypted` flag keeps plaintext ports open alongside TLS for gradual migration
- **Persistence** — RDB, AOF, or both with configurable PVCs
- **Authentication** — password from Kubernetes Secret
- **Observability** — CRD status visible in `kubectl` and Lens, Kubernetes Events
- **Controlled rolling updates** — replica-first rollout with replication sync verification and automatic failover
- **Cluster Observer** — optional diagnostic deployment that continuously verifies cluster health (master reachable, replication sync, write/read tests, Sentinel quorum) and exposes Prometheus metrics
- **Metrics exporter** — optional per-pod Prometheus exporter sidecar with a dedicated Service and Prometheus-Operator `ServiceMonitor`; enabling it on a running cluster migrates through the failover-aware rolling update without data loss
- **Disruption budgets** — optional PodDisruptionBudgets that keep a node drain from evicting all data pods or the Sentinel quorum at once
- **Pod anti-affinity** — opt-in spreading of data and Sentinel pods across nodes: `mode: soft` (scheduler preference) or `mode: hard` (guaranteed spread); off by default so upgrades change nothing
- **Network policies** — optional firewall rules for Valkey and Sentinel traffic
- **Helm deployment** — install the operator with a single `helm install`

## Documentation

| Document | What it covers |
|---|---|
| [SECURITY_ARCHITECTURE.md](SECURITY_ARCHITECTURE.md) | Trust boundaries, every RBAC rule the operator and the per-instance sidecar hold and what each one permits, where the password and the TLS material live, what the isolation does **not** cover, and the hardening checklist |
| [docs/adr/](docs/adr/README.md) | Architecture Decision Records — what was decided, why, what was rejected and what it costs, for the reconcile model, the rolling update, the master authority and split-brain resolution, the privilege model, and the test/CI policy |
| [CRD Reference](#crd-reference) (below) | Every `spec` field, its default and its effect |
| [Valkey documentation](https://valkey.io/topics/) | Upstream server, replication and Sentinel behaviour |
| [cert-manager](https://cert-manager.io/docs/) | Issuers referenced by `spec.tls.certManager` |

Read the security document before granting anyone `create valkeys`: the operator
holds a cluster-wide grant that includes reading every Secret and writing RBAC.

## Quick Start

### Prerequisites

- Kubernetes cluster (v1.29+)
- Helm 3
- [cert-manager](https://cert-manager.io/) (only if using TLS with automatic certificate management)

### Install the Operator

```bash
helm install valkey-operator deploy/helm/valkey-operator \
  --namespace valkey-operator-system \
  --create-namespace
```

### Upgrade the Operator

`helm upgrade` with the chart is the supported path. The CRD, the operator
ClusterRole and the Deployment all live in the chart's `templates/`, so one
command carries schema, permissions and image forward together:

```bash
helm upgrade valkey-operator deploy/helm/valkey-operator \
  --namespace valkey-operator-system
```

Updating the operator image on its own — `kubectl set image`, or a bumped tag
applied against an older chart — leaves the CRD and the ClusterRole behind and is
not a supported upgrade path.

<details>
<summary>What the upgrade does, how to verify it, rollback and uninstall</summary>

**Before the new operator starts.** The chart runs a `pre-upgrade` hook Job
(`valkey-operator-pre-upgrade`) that executes `manager migrate` and writes the
current field defaults into existing `Valkey` CRs, so the new operator never
reconciles a CR that predates its defaults. It is enabled by default; skip it
with `--set preUpgradeHook.enabled=false`.

**What it does to running clusters.** The sidecar that runs in every Valkey pod
uses the operator image (`--operator-image`, set by the chart from
`image.repository:tag`), so an upgrade that changes the operator tag changes the
managed pod spec. Every multi-replica cluster is then migrated once through the
failover-aware rolling update — replicas first, then a controlled failover, then
the former master — without data loss.

**A single-replica cluster without Sentinel is not restarted for this.** A
sidecar-only delta on the only pod has no failover target, so the operator
deliberately does not apply it: it sets the `SidecarUpdatePending` condition on the
`Valkey` CR and leaves the pod running the **old** sidecar image. There is no
downtime and nothing to schedule — but there is also no automatic convergence: the
pod keeps the old sidecar until something restarts it, which means a manual
`kubectl delete pod`, an eviction, or a spec change that alters the pod template
(a new `spec.image`, for example). Force it when you want it — but the restart is
not free on the only pod of the cluster: it has no failover target, so an instance
without `persistence.enabled` comes back empty (with persistence it reloads its
RDB/AOF). This is the same exception as the metrics note further down.

```bash
kubectl get valkey <name> -o jsonpath='{range .status.conditions[?(@.type=="SidecarUpdatePending")]}{.status}{"\n"}{end}'
kubectl delete pod <name>-0        # the deferred sidecar update applies on recreation
```

The condition clears itself once the deferred update has applied: the operator clears it
in the one place that provably knows every pod matches the live template — the pass where
no pod needs an update and no rolling-update state is recorded
([ADR 0002](docs/adr/0002-surface-a-blocked-reconcile-on-the-cr.md) D10). To confirm the
running sidecar directly rather than through the condition:

```bash
kubectl get pod <name>-0 -o jsonpath='{range .spec.containers[*]}{.name}={.image}{"\n"}{end}'
```

**Verify.**

```bash
kubectl -n valkey-operator-system rollout status deployment/valkey-operator
kubectl get valkey -A
```

Every instance returns to `PHASE=OK` with `READY` equal to `REPLICAS`. `Rolling
Update` means the migration above is still running; `kubectl describe valkey
<name>` shows the current step and any `ReconcileBlocked` condition.

**Upgrading from the released chart repository** instead of a checked-out tree:

```bash
helm repo add valkey-operator https://guided-traffic.github.io/valkey-operator/
helm repo update
helm upgrade valkey-operator valkey-operator/valkey-operator \
  --namespace valkey-operator-system
```

**Rollback.**

```bash
helm rollback valkey-operator --namespace valkey-operator-system
```

The CRD is part of the release, so a rollback restores the previous CRD schema as
well. Spec fields that only the newer schema knows are pruned from existing CRs by
the API server, so roll back before adopting new fields, or re-apply them after
upgrading again.

**Uninstall.**

```bash
kubectl delete valkey --all --all-namespaces   # do this knowingly, see below
helm uninstall valkey-operator --namespace valkey-operator-system
```

The CRD is a normal chart template with no `helm.sh/resource-policy: keep`, so
`helm uninstall` deletes it — and deleting a CRD removes every `Valkey` CR with
it, which garbage-collects the StatefulSets, Services and ConfigMaps those CRs
own. PersistentVolumeClaims created for `spec.persistence` are **not** removed
(the StatefulSets set no PVC retention policy): the data stays on disk and is
reattached when a cluster of the same name is created again.

</details>

### Deploy a Standalone Valkey Instance

```yaml
apiVersion: vko.gtrfc.com/v1
kind: Valkey
metadata:
  name: my-valkey
spec:
  replicas: 1
  image: valkey/valkey:8.0
```

```bash
kubectl apply -f my-valkey.yaml
kubectl get valkey
```

```
NAME        REPLICAS   READY   PHASE   MASTER          AGE
my-valkey   1          1       OK      my-valkey-0     2m
```

---

## Examples

### Standalone — Minimal

The simplest deployment: a single Valkey pod with no persistence, no TLS, no auth.

```yaml
apiVersion: vko.gtrfc.com/v1
kind: Valkey
metadata:
  name: minimal
spec:
  replicas: 1
  image: valkey/valkey:8.0
```

### Standalone — With Persistence

Data survives pod restarts via a PersistentVolumeClaim.

```yaml
apiVersion: vko.gtrfc.com/v1
kind: Valkey
metadata:
  name: persistent
spec:
  replicas: 1
  image: valkey/valkey:8.0
  persistence:
    enabled: true
    mode: rdb          # rdb | aof | both
    size: 5Gi
    storageClass: ""   # empty = default StorageClass
  resources:
    requests:
      cpu: 250m
      memory: 256Mi
    limits:
      cpu: 500m
      memory: 512Mi
```

### Standalone — With TLS (cert-manager)

All traffic is encrypted. The operator creates a cert-manager `Certificate` resource automatically.

**Prerequisite:** cert-manager must be installed and a `ClusterIssuer` (or `Issuer`) must exist.

```yaml
apiVersion: vko.gtrfc.com/v1
kind: Valkey
metadata:
  name: tls-standalone
spec:
  replicas: 1
  image: valkey/valkey:8.0
  tls:
    enabled: true
    certManager:
      issuer:
        kind: ClusterIssuer
        name: my-ca-issuer
```

> **Note:** When TLS is enabled, the plaintext port (`6379`) is disabled by default. Valkey listens on TLS port `16379`. Set `spec.tls.allowUnencrypted: true` to keep port `6379` open alongside `16379` (dual-port mode).

### Standalone — With TLS + Dual Port

Keep the plaintext port open while TLS is active — useful for migration or clients that do not support TLS.

```yaml
apiVersion: vko.gtrfc.com/v1
kind: Valkey
metadata:
  name: tls-dualport
spec:
  replicas: 1
  image: valkey/valkey:8.0
  tls:
    enabled: true
    allowUnencrypted: true    # Valkey listens on both 6379 (plain) and 16379 (TLS)
    certManager:
      issuer:
        kind: ClusterIssuer
        name: my-ca-issuer
```

> **Security note:** `allowUnencrypted` defaults to `false`. Enable it only when you need temporary plaintext access; disable it once all clients are migrated to TLS.

### Standalone — With TLS (User-Provided Secret)

If you manage certificates yourself, provide a Secret with `tls.crt`, `tls.key`, and `ca.crt`:

```yaml
apiVersion: vko.gtrfc.com/v1
kind: Valkey
metadata:
  name: tls-manual
spec:
  replicas: 1
  image: valkey/valkey:8.0
  tls:
    enabled: true
    secretName: my-valkey-tls-secret
```

### HA — 3 Replicas with Sentinel

A production-ready HA setup: 3 Valkey nodes (1 master + 2 replicas) with 3 Sentinel instances for automatic failover.

```yaml
apiVersion: vko.gtrfc.com/v1
kind: Valkey
metadata:
  name: ha-cluster
spec:
  replicas: 3
  image: valkey/valkey:8.0
  sentinel:
    enabled: true
    replicas: 3
  persistence:
    enabled: true
    mode: rdb
    size: 10Gi
  resources:
    requests:
      cpu: 250m
      memory: 256Mi
    limits:
      cpu: "1"
      memory: 1Gi
```

The operator creates:

| Resource | Name | Count |
|----------|------|-------|
| StatefulSet | `ha-cluster` | 3 Valkey pods |
| StatefulSet | `ha-cluster-sentinel` | 3 Sentinel pods |
| ConfigMap | `ha-cluster-config` | Master config |
| ConfigMap | `ha-cluster-replica-config` | Replica config (with `replicaof`) |
| ConfigMap | `ha-cluster-sentinel-config` | Sentinel config |
| Service | `ha-cluster` | Client-facing (ClusterIP) |
| Service | `ha-cluster-headless` | Valkey DNS (headless) |
| Service | `ha-cluster-sentinel-headless` | Sentinel DNS (headless) |

### HA — Full Production Setup (TLS + Persistence + Labels)

The most comprehensive configuration with TLS, persistence, custom labels, and resource limits.

```yaml
apiVersion: vko.gtrfc.com/v1
kind: Valkey
metadata:
  name: production
spec:
  replicas: 3
  image: valkey/valkey:8.0
  sentinel:
    enabled: true
    replicas: 3
    podLabels:
      app: sentinel
      team: platform
    podAnnotations:
      prometheus.io/scrape: "true"
  tls:
    enabled: true
    # unifiedCertificate: true   # Recommended for go-redis Sentinel mode and
                                 # other clients that share a tls.Config across
                                 # Sentinel discovery and master connection.
                                 # See "Unified TLS Certificate" in TLS Details.
    certManager:
      issuer:
        kind: ClusterIssuer
        name: production-ca
      extraDnsNames:
        - valkey.example.com
  persistence:
    enabled: true
    mode: both          # RDB + AOF for maximum durability
    size: 20Gi
    storageClass: fast-ssd
  podLabels:
    app: valkey
    team: platform
    environment: production
  podAnnotations:
    prometheus.io/scrape: "true"
    prometheus.io/port: "9121"
  resources:
    requests:
      cpu: 500m
      memory: 512Mi
    limits:
      cpu: "2"
      memory: 2Gi
```

### HA — With Cluster Observer

Deploy a diagnostic observer alongside the cluster. The observer continuously runs health checks (PING, write/read tests, replication sync, Sentinel quorum) and exposes results via readiness probe and Prometheus metrics on port `8084`.

```yaml
apiVersion: vko.gtrfc.com/v1
kind: Valkey
metadata:
  name: observed-cluster
spec:
  replicas: 3
  image: valkey/valkey:8.0
  sentinel:
    enabled: true
    replicas: 3
  observer:
    enabled: true
    db: 15              # Valkey DB for health key (default: 15)
    logLevel: info      # Log verbosity: debug, info, warn, error (default: info)
    # mtls:             # Optional: enable mTLS for observer connections (both default to false)
    #   valkey: true    # Send client cert to Valkey pods
    #   sentinel: true  # Send client cert to Sentinel pods
    resources:
      requests:
        cpu: 50m
        memory: 64Mi
      limits:
        memory: 128Mi
```

The observer creates:

| Resource | Name | Description |
|----------|------|-------------|
| Deployment | `observed-cluster-observer` | 1 observer pod (same image as operator) |
| NetworkPolicy | `observed-cluster-observer` | Allows health probe ingress on port 8084 (if `networkPolicy.enabled`) |

Health endpoints:

| Endpoint | Description |
|----------|-------------|
| `GET /readyz` | 200 if all checks pass, 503 otherwise (JSON body with per-check details) |
| `GET /healthz` | Always 200 (liveness) |
| `GET /metrics` | Prometheus metrics |

### With Metrics (Prometheus Exporter)

Attach a metrics exporter sidecar to every Valkey pod. The exporter (`oliver006/redis_exporter` by default) connects to the local Valkey instance and serves `/metrics` on port `9121`. TLS and authentication are handled automatically — the exporter reuses the pod's mounted certificates and the auth Secret.

```yaml
apiVersion: vko.gtrfc.com/v1
kind: Valkey
metadata:
  name: monitored
spec:
  replicas: 3
  image: valkey/valkey:8.0
  sentinel:
    enabled: true
    replicas: 3
  metrics:
    enabled: true
    # image: oliver006/redis_exporter:v1.66.0  # optional; default shown
    # port: 9121                               # optional; exporter /metrics port
    resources:
      requests:
        cpu: 50m
        memory: 32Mi
      limits:
        memory: 64Mi
    service:
      enabled: true          # dedicated <name>-metrics Service (default: true)
    serviceMonitor:
      enabled: true          # requires the Prometheus-Operator CRDs
      interval: 30s
      labels:
        release: prometheus  # match your Prometheus serviceMonitorSelector
```

Enabling metrics creates:

| Resource | Name | Description |
|----------|------|-------------|
| Container | `exporter` | Exporter sidecar added to each Valkey pod (no readiness probe, so it never affects pod routing) |
| Service | `monitored-metrics` | ClusterIP Service exposing the `metrics` port across all Valkey pods, marked with `vko.gtrfc.com/metrics: "true"` |
| ServiceMonitor | `monitored-metrics` | Prometheus-Operator scrape target selecting the metrics Service (only when `serviceMonitor.enabled: true`) |

The `ServiceMonitor` is managed as an unstructured object, so the operator has **no build-time dependency** on the Prometheus-Operator. If the `monitoring.coreos.com` CRDs are not installed, the operator logs a message and skips the `ServiceMonitor` rather than failing.

> **Lossless migration:** turning `metrics.enabled` on (or off) changes the pod template, which the operator rolls out through its normal failover-aware rolling update — replicas are replaced one by one and the leader is failed over, so **no data is lost even without persistence**. The only exception is a single standalone pod (`replicas: 1`) without persistence: it has no failover target, so adding the sidecar restarts it and its in-memory data is lost.

### HA — With Authentication

Protect your cluster with a password stored in a Kubernetes Secret.

```bash
kubectl create secret generic valkey-auth --from-literal=password=my-strong-password
```

```yaml
apiVersion: vko.gtrfc.com/v1
kind: Valkey
metadata:
  name: auth-cluster
spec:
  replicas: 3
  image: valkey/valkey:8.0
  sentinel:
    enabled: true
    replicas: 3
  auth:
    secretName: valkey-auth
    secretPasswordKey: password
```

### HA — With Authentication (Sentinel Unauthenticated)

Valkey requires a password, but Sentinel accepts client connections without authentication. This is useful when Sentinel discovery clients (e.g., application frameworks) do not support Sentinel AUTH.

Sentinel still uses `auth-pass` internally to connect to password-protected Valkey nodes.

```yaml
apiVersion: vko.gtrfc.com/v1
kind: Valkey
metadata:
  name: auth-nosentinel-auth
spec:
  replicas: 3
  image: valkey/valkey:8.0
  sentinel:
    enabled: true
    replicas: 3
    disableAuth: true     # Sentinel accepts unauthenticated client connections
  auth:
    secretName: valkey-auth
    secretPasswordKey: password
```

> **Security note:** `disableAuth` only affects Sentinel — Valkey itself always requires the configured password. Consider enabling TLS and/or `networkPolicy` to restrict Sentinel access when using this option.

---

## CRD Reference

### `spec`

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `replicas` | `int32` | `1` | Number of Valkey instances |
| `image` | `string` | *(required)* | Valkey container image (e.g., `valkey/valkey:8.0`) |
| `sentinel` | `SentinelSpec` | — | Sentinel HA configuration |
| `auth` | `AuthSpec` | — | Authentication configuration |
| `tls` | `TLSSpec` | — | TLS encryption configuration |
| `metrics` | `MetricsSpec` | — | Metrics exporter configuration |
| `networkPolicy` | `NetworkPolicySpec` | — | NetworkPolicy configuration |
| `persistence` | `PersistenceSpec` | — | Data persistence configuration |
| `observer` | `ObserverSpec` | — | Cluster observer configuration |
| `podDisruptionBudget` | `PodDisruptionBudgetSpec` | — | PodDisruptionBudgets for the data and Sentinel StatefulSets |
| `antiAffinity` | `AntiAffinitySpec` | *(off)* | Opt-in pod anti-affinity for the data and Sentinel StatefulSets |
| `rollingUpdate` | `RollingUpdateSpec` | — | Rolling update timing |
| `podLabels` | `map[string]string` | — | Additional labels for Valkey pods |
| `podAnnotations` | `map[string]string` | — | Additional annotations for Valkey pods |
| `resources` | `ResourceRequirements` | — | CPU/memory requests and limits |

### `spec.sentinel`

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | `bool` | `false` | Enable Sentinel HA mode |
| `replicas` | `int32` | `3` | Number of Sentinel instances |
| `allowUnencrypted` | `bool` | `false` | Keep plaintext Sentinel port (`26379`) open alongside TLS port (`36379`). Only effective when `spec.tls.enabled: true`. |
| `disableAuth` | `bool` | `false` | Disable password authentication for Sentinel client connections. Sentinel still uses `auth-pass` to connect to Valkey nodes. Only effective when `spec.auth` is configured. |
| `podLabels` | `map[string]string` | — | Additional labels for Sentinel pods |
| `podAnnotations` | `map[string]string` | — | Additional annotations for Sentinel pods |

### `spec.tls`

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | `bool` | `false` | Enable TLS encryption |
| `allowUnencrypted` | `bool` | `false` | Keep plaintext Valkey port (`6379`) open alongside TLS port (`16379`). Replication always uses TLS. |
| `unifiedCertificate` | `bool` | `false` | Make Valkey and Sentinel share one TLS Secret covering both sets of hostnames. Under cert-manager, one `Certificate` is issued instead of two; under a user-provided Secret, the flag is informational. See [Unified TLS Certificate](#unified-tls-certificate-valkey--sentinel). |
| `certManager` | `CertManagerSpec` | — | cert-manager integration (mutually exclusive with `secretName`) |
| `secretName` | `string` | — | Name of existing TLS Secret (must contain `tls.crt`, `tls.key`, `ca.crt`) |

### `spec.tls.certManager`

| Field | Type | Description |
|-------|------|-------------|
| `issuer.kind` | `string` | `Issuer` or `ClusterIssuer` |
| `issuer.name` | `string` | Name of the issuer resource |
| `issuer.group` | `string` | API group (default: `cert-manager.io`) |
| `extraDnsNames` | `[]string` | Additional DNS names for the certificate |

### `spec.metrics`

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | `bool` | `false` | Add a Prometheus exporter sidecar to each Valkey pod |
| `image` | `string` | `oliver006/redis_exporter:v1.66.0` | Exporter container image |
| `port` | `int32` | `9121` | Container/Service port serving `/metrics` (named `metrics`) |
| `resources` | `ResourceRequirements` | — | CPU/memory requests and limits for the exporter container |
| `extraArgs` | `[]string` | — | Additional command-line arguments passed to the exporter (e.g. `["--check-keys=*"]`) |
| `service` | `MetricsServiceSpec` | — | Dedicated metrics Service configuration |
| `serviceMonitor` | `ServiceMonitorSpec` | — | Prometheus-Operator ServiceMonitor configuration |

### `spec.metrics.service`

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | `bool` | `true` | Create the dedicated `<name>-metrics` Service. An enabled `serviceMonitor` forces this on regardless. |
| `labels` | `map[string]string` | — | Additional labels applied to the metrics Service |

### `spec.metrics.serviceMonitor`

Requires the Prometheus-Operator CRDs (`monitoring.coreos.com`) to be installed. When they are absent, the operator skips the ServiceMonitor instead of failing.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | `bool` | `false` | Create a ServiceMonitor selecting the metrics Service |
| `interval` | `string` | `30s` | Scrape interval |
| `scrapeTimeout` | `string` | — | Per-scrape timeout (empty = Prometheus default) |
| `labels` | `map[string]string` | — | Additional labels, commonly used to match a Prometheus instance's `serviceMonitorSelector` (e.g. `release: prometheus`) |

### `spec.observer`

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | `bool` | `false` | Deploy a diagnostic observer alongside the cluster |
| `db` | `int` | `15` | Valkey database index (0–15) used for the health check key |
| `logLevel` | `string` | `info` | Log verbosity: `debug`, `info`, `warn`, `error`. At `debug`, stack traces are included for all errors. At `info` and above, stack traces are suppressed. |
| `mtls` | `ObserverMTLSSpec` | — | Controls whether the observer sends a client certificate to Valkey and/or Sentinel. Only effective when `spec.tls.enabled: true`. |
| `resources` | `ResourceRequirements` | 50m/64Mi request, 128Mi limit | CPU/memory for the observer container |
| `unreadyWhen` | `ObserverUnreadyWhenSpec` | all `true` | Per-check control over whether a failure causes the observer to report unReady. Failures are always logged regardless of this setting. |

### `spec.observer.unreadyWhen`

Each field controls whether the corresponding check failure flips the observer to unReady.
When a field is `false`, failures are still logged but do not affect the ready state.
Omitting a field is equivalent to `true`.

| Field | Default | Check description |
|-------|---------|-------------------|
| `masterUnreachable` | `true` | PING to the current master fails |
| `writeTestFailure` | `true` | Health key cannot be written to the master |
| `readTestFailure` | `true` | Health key cannot be read back from the master |
| `replicaSyncFailure` | `true` | A replica is disconnected or bulk sync is in progress (_replicas > 1 only_) |
| `replicaReadTestFailure` | `true` | A replica returns stale or missing health key data (_replicas > 1 only_) |
| `sentinelUnreachable` | `true` | One or more Sentinel instances do not respond to PING (_sentinel only_) |
| `sentinelQuorumFailure` | `true` | Sentinels disagree on the current master address (_sentinel only_) |
| `sentinelMasterDown` | `true` | Sentinel reports `s_down` or `o_down` flags on the master (_sentinel only_) |
| `sentinelMasterHostnameInvalid` | `true` | Sentinel reports a bare IP instead of a DNS hostname for the master (_sentinel only_) |
| `sentinelReplicaHostnamesInvalid` | `true` | Sentinel reports bare IPs for one or more replicas (_sentinel only_) |

**Minimal operation mode** — observer signals unReady only when the master itself is unavailable;
replica lag and Sentinel issues are logged but tolerated:

```yaml
spec:
  observer:
    enabled: true
    unreadyWhen:
      replicaSyncFailure: false
      replicaReadTestFailure: false
      sentinelUnreachable: false
      sentinelQuorumFailure: false
      sentinelMasterDown: false
      sentinelMasterHostnameInvalid: false
      sentinelReplicaHostnamesInvalid: false
```

### `spec.observer.mtls`

When `spec.tls.enabled: true`, the observer always verifies the server's certificate. These flags additionally enable **mutual TLS (mTLS)** by sending a client certificate. When neither flag is set, no certificate secret is mounted into the observer pod.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `valkey` | `bool` | `false` | Send client certificate to Valkey pods (mTLS). When `false`, the observer uses server-only TLS. |
| `sentinel` | `bool` | `false` | Send client certificate to Sentinel pods (mTLS). When `false`, the observer uses server-only TLS. |

> **Note:** The TLS secret is only mounted into the observer pod when at least one of `mtls.valkey` or `mtls.sentinel` is `true`. If both are `false` (the default), the observer connects using TLS without a client certificate and no volume mount is created.

### `spec.podDisruptionBudget`

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | `bool` | `false` | Create a PDB for the data StatefulSet and, with Sentinel enabled, a quorum-preserving PDB for the Sentinel StatefulSet |
| `maxUnavailable` | `int32` | `1` | Data pods that may be disrupted voluntarily at the same time. Applies to the data StatefulSet only |

The budgets are named after the StatefulSets they cover — `<name>` for the data
pods and `<name>-sentinel` for Sentinel — live in the CR's namespace and are owned
by the CR, so deleting the CR removes them.

The ownerReference is also what the operator goes by: **a PodDisruptionBudget it
does not own is never deleted and never adopted**, even under exactly those names.
A hand-written budget for the same pods therefore survives every reconcile — the
operator leaves it untouched and records a `PodDisruptionBudgetNotOwned` Warning
Event on the CR instead. That Event is only recorded while
`spec.podDisruptionBudget.enabled` is `true`: a CR that never opted in leaves a
foreign budget alone silently, without a permanent warning stream. It **is**
recorded while the feature is on but not applicable to that StatefulSet (fewer
than two replicas, or Sentinel disabled): the name is taken, so scaling back up
would silently produce no budget at all. While such a budget exists,
`spec.podDisruptionBudget` has no effect for that StatefulSet — and
the operator suppresses the two content warnings below for it, because they would
describe values that never reached an object. Delete or rename the foreign budget
to hand the name over to the operator.

Both budgets are **opt-in**. The operator creates none unless the block is present
and `enabled: true` — a budget created next to a user-managed one would cover the
same pods twice, and the Eviction API refuses every eviction in that case.

What the budgets do and do not cover:

- The **data PDB** uses `maxUnavailable` (default `1`), so a node drain takes one
  data pod at a time instead of the whole StatefulSet. Setting `maxUnavailable`
  to `spec.replicas` or higher removes the protection; the operator honours it and
  warns rather than rejecting a later scale-down. The warning is a log line plus a
  `PodDisruptionBudgetTooPermissive` Event on the CR, emitted on every reconcile
  while the condition holds — so scaling `spec.replicas` down into it (`5` -> `2`
  with `maxUnavailable: 2`) is reported too, even though the PDB object itself
  never changes.
- The **Sentinel PDB** uses `minAvailable = floor(spec.sentinel.replicas / 2) + 1`
  — the failover quorum. It is **computed, never configurable**: a settable value
  could silently break the guarantee that a drain cannot take the Sentinel majority.
  With `spec.sentinel.replicas: 2` the quorum equals the replica count, so **no
  voluntary disruption is permitted at all** — `kubectl drain` on a node hosting a
  Sentinel pod never finishes until the CR is scaled or the PDB removed. The formula
  stays (a smaller `minAvailable` would let a drain take automatic failover), and the
  operator makes the consequence visible: a log line plus a
  `SentinelPodDisruptionBudgetBlocksDrains` Event on the CR, emitted on every reconcile
  while the condition holds — including after scaling `spec.sentinel.replicas` `3` ->
  `2`, where the quorum stays `2` and the PDB object itself never changes. Use an odd
  Sentinel count of 3 or more; an even count is not HA in the first place.
- **StatefulSets with fewer than 2 replicas get no PDB**, even with `enabled: true`
  (data at `spec.replicas: 1`, Sentinel at `spec.sentinel.replicas: 1`). With one pod
  `maxUnavailable: 1` would permit evicting the only pod and `minAvailable: 1` would
  block `kubectl drain` forever — fake safety for an instance that is not HA either
  way. Scaling below 2 deletes an existing PDB; scaling back up recreates it.
- Budgets gate **voluntary** disruptions only (drain, cluster autoscaler, eviction
  API). Node failures, `kubectl delete pod` and the operator's own failover-aware
  rolling update are unaffected — the operator deletes pods directly.

```yaml
apiVersion: vko.gtrfc.com/v1
kind: Valkey
metadata:
  name: ha-valkey
spec:
  replicas: 3
  image: valkey/valkey:8.0
  sentinel:
    enabled: true
    replicas: 3
  podDisruptionBudget:
    enabled: true          # default: false
    maxUnavailable: 1      # default: 1 (data StatefulSet only)
```

The operator needs `policy/poddisruptionbudgets` RBAC for this; the Helm chart
ships it unconditionally, so no permission change is needed to turn the feature on.

### `spec.antiAffinity`

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `mode` | `string` | `off` | `off` (no term), `soft` (scheduler preference) or `hard` (guaranteed spread) |
| `topologyKey` | `string` | `kubernetes.io/hostname` | Node label whose values define the spread domains |

Anti-affinity is **opt-in**: omitting the block (or `mode: off`, the default)
renders no term at all, so upgrading the operator never changes how existing
clusters are scheduled. The flip side is that without an opt-in all pods of a
cluster may land on one node — a single drain then takes the whole data plane
down at once. **Multi-replica clusters should set `mode: soft` (or `hard`)**;
enabling it on a running cluster triggers one failover-aware rolling update
(lossless for multi-replica clusters).

- **`off`** (default) renders nothing. Scheduling is exactly what it was before
  the operator supported anti-affinity.
- **`soft`** renders `preferredDuringSchedulingIgnoredDuringExecution`
  with weight `100`, the strongest preference the scheduler weighs against its other
  priorities. Under node pressure pods may still be co-located, so the spread is a
  best effort, not a guarantee.
- **`hard`** renders `requiredDuringSchedulingIgnoredDuringExecution`. The spread is
  guaranteed, with two consequences worth knowing before enabling it: with fewer
  schedulable spread domains than replicas the surplus pods stay `Pending` (which
  also wedges the next rolling update), and during a node drain an evicted pod stays
  `Pending` until a domain without a pod of the same StatefulSet becomes schedulable.
  That is degraded but correct — the alternative is silently re-co-locating the pods.
- Each StatefulSet **repels only its own kind**, selected by
  `app.kubernetes.io/instance` + `app.kubernetes.io/managed-by` +
  `app.kubernetes.io/component`. Data and Sentinel pods may therefore share a node,
  and a second Valkey CR in the same namespace is unaffected.
- **StatefulSets with fewer than 2 replicas get no term** (data at
  `spec.replicas: 1`, Sentinel at `spec.sentinel.replicas: 1`): a singleton has no
  peer to repel, and injecting an empty term would change the pod-spec hash and
  restart the pod for nothing.
- Changing `mode` or `topologyKey` changes the pod-spec hash and therefore triggers
  the operator's failover-aware rolling update — lossless for a multi-replica
  cluster.

```yaml
apiVersion: vko.gtrfc.com/v1
kind: Valkey
metadata:
  name: ha-valkey
spec:
  replicas: 3
  image: valkey/valkey:8.0
  sentinel:
    enabled: true
    replicas: 3
  antiAffinity:
    mode: soft                            # default: off (no term; opt in with soft or hard)
    topologyKey: kubernetes.io/hostname   # default: kubernetes.io/hostname
```

Spreading across availability zones instead of nodes is a `topologyKey` change:

```yaml
  antiAffinity:
    mode: hard
    topologyKey: topology.kubernetes.io/zone
```

### `spec.persistence`

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | `bool` | `false` | Enable persistent storage |
| `mode` | `string` | `rdb` | Persistence mode: `rdb`, `aof`, or `both` |
| `storageClass` | `string` | `""` | StorageClass name (empty = default) |
| `size` | `Quantity` | `1Gi` | Requested storage size |

`mode` is a config-file setting and propagates like any other one — it changes the
config hash and rides the failover-aware rolling update. **`enabled`,
`storageClass` and `size` are read when the StatefulSet is created and never
again.** A StatefulSet's `volumeClaimTemplates` are immutable, the API server
rejects every update that touches them, and the operator never writes them — so
changing storage on an existing cluster is not drift that a later pass converges
([ADR 0023](docs/adr/0023-volume-claim-templates-are-immutable.md)).

The operator reports the difference instead of submitting a write that cannot fix
it — enabling persistence on an existing StatefulSet is rejected by the API server,
and disabling it is *accepted*, which is worse: the pod template gains an
`emptyDir` while the live claims stay on the object.

| Change on an existing cluster | What the operator does |
|---|---|
| `enabled` toggled in either direction | Writes **nothing** to that StatefulSet — replica, image and label changes are held together with the storage change. `StorageSpecNotApplied=True` with reason `RecreateRequired`, `ReconcileBlocked=True` with the same reason, and a `StatefulSetRecreateRequired` Warning Event on every pass. |
| `size` or `storageClass` changed while `enabled: true` | Applies every other change normally; only the storage stays as it is. `StorageSpecNotApplied=True` with reason `VolumeClaimTemplatesImmutable` and a `StatefulSetRecreateRequired` Warning Event naming the difference. The reconcile is **not** blocked. |

Both shapes use the same Event reason — `StatefulSetRecreateRequired` for the data
StatefulSet, `SentinelStatefulSetRecreateRequired` for the Sentinel one, which
carries no `volumeClaimTemplates` today and therefore never conflicts — and the
message says which shape it is. Because the Sentinel tier never conflicts, it is
also never allowed to *resolve* `StorageSpecNotApplied`: either tier may report a
conflict, only the data tier may clear one. A tier that compares empty against
empty has proven nothing about the tier that holds the claims. While a `RecreateRequired` conflict stands the
**ConfigMap keeps converging** — the `save`/`appendonly` directives follow the spec
even though the volumes do not, so a pod that restarts for any other reason boots
the new persistence config against the old volume layout. That costs consistency
between the dump settings and the volume, never the dataset: the pod rejoins as a
replica and resyncs from the master.

**The migration works in one direction and not the other.** Both were walked on a
running three-replica cluster (2026-08-23, Kind, Kubernetes 1.36); the results are
not symmetric and the difference decides whether you can do this at all.

Either way the first step is the same, and `--cascade=orphan` is not optional:

```bash
kubectl delete statefulset <name> -n <namespace> --cascade=orphan
```

> **Never omit `--cascade=orphan`.** Without it the delete takes every pod at once,
> and a cluster whose data is only in memory loses it on the spot.

**Turning persistence off: verified, lossless.** The pods survive the delete, the
operator recreates the StatefulSet without claim templates, the
statefulset-controller re-adopts the pods, and the failover-aware rolling update
replaces them one by one with `emptyDir`-backed ones. Measured end state: three new
pods, `phase: OK`, and the dataset intact on all three. The old
PersistentVolumeClaims stay behind, still bound (see below).

**Turning persistence on: do not do this on a cluster whose data matters.** The
same first step wedges. Re-adoption itself works, but the statefulset-controller
then tries to attach the new claim to each adopted pod, which pod immutability
forbids — `Pod "<name>-0" is invalid: spec: Forbidden: pod updates may not change
fields other than ...`. The sync fails on the lowest such ordinal and returns, so no
missing pod is created either: a cluster the operator had already started rolling
stays short of pods indefinitely. Deleting the adopted pods by hand, lowest ordinal
first, does clear the wedge — and in the measured run the dataset did **not**
survive that step: the empty replacement of ordinal 0 is still the recorded master,
and the split-brain resolver demoted the drain-promoted pod that held the data. **That
second half is fixed** since [ADR 0028](docs/adr/0028-a-demotion-may-not-discard-the-only-dataset.md):
the operator no longer demotes a master holding keys toward a recorded one holding
none, so the split brain stays visible instead of costing the dataset. The wedge
itself is not fixed. Treat enabling persistence on an existing cluster
as "stand up a new cluster and restore into it", and back up first
(`valkey-cli --rdb`, or `BGSAVE` plus a copy out of the pod). Both findings, with
their reproductions, are in
[ADR 0023](docs/adr/0023-volume-claim-templates-are-immutable.md).

Reverting `spec.persistence` to what the StatefulSet was created with clears the
block at once, touches no pod and costs nothing — the right move whenever the change
was not deliberate.

**Recreating does not resize or reclass the volumes that already exist.** The
claims are named `data-<name>-<ordinal>` and are reused by name, so a recreated
StatefulSet binds the same PersistentVolumeClaims it had before — only claims
created *later*, when a scale-out adds a new ordinal, follow the new `size` or
`storageClass`. Growing a volume is an edit on each PersistentVolumeClaim and needs
a StorageClass with `allowVolumeExpansion: true`; changing the class means moving
the data.

**Turning persistence off leaves the data on disk.** The operator never deletes a
PersistentVolumeClaim and sets no `persistentVolumeClaimRetentionPolicy`, so the
old claims and their RDB/AOF files outlive the migration. They are reattached if a
persistent cluster of the same name is created again, and removed only by hand.

### `spec.rollingUpdate`

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `syncTimeout` | `Duration` | `5m` | How long the operator waits for a replaced pod to finish replication sync before it stops waiting |

`syncTimeout` bounds the two points in a rolling update where the operator waits
for a full dataset transfer, and it does something different at each:

| Wait | On timeout |
|------|------------|
| A replaced replica syncing from the master, before the next pod is replaced | The wait ends and is **reported** (`RollingUpdatePaused` condition, phase `Error` for that pass). It is a report, not a halt: the pass clears the rolling-update state, so a later pass that still finds outdated pods starts the state machine again on a fresh `syncTimeout` budget, and the phase returns to `OK` as soon as the cluster is Ready. A spec change restarts the roll from the beginning. |
| The former master (pod-0) syncing back after the failover, before it is promoted again | The restoration is **abandoned**: the promoted replica stays master and the update finishes (`TopologyRestored=False`). |

The second case never force-promotes pod-0. An unsynced pod-0 would come up as an
empty master and discard every write the promoted replica accepted since the
failover, so the operator gives up the canonical topology rather than the data.
The cluster stays fully usable — the `-rw`/`-r` Services select the master by
label, not by ordinal. Raise `syncTimeout` for large datasets whose initial sync
does not fit in five minutes.

### `spec.auth`

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `secretName` | `string` | — | Kubernetes Secret name containing the password |
| `secretPasswordKey` | `string` | `password` | Key within the Secret |

### `status`

| Field | Type | Description |
|-------|------|-------------|
| `readyReplicas` | `int32` | Number of ready Valkey instances |
| `masterPod` | `string` | Name of the current master pod |
| `observerReady` | `bool` | Whether the observer Deployment has a ready replica (only set when `observer.enabled: true`). The observer's readiness is its own last cluster health verdict, so this is a health signal, not a rollout signal. A Deployment holding the generated name that this `Valkey` does not control reads as **not** ready. |
| `phase` | `string` | Current lifecycle phase |
| `message` | `string` | Human-readable status description |
| `conditions` | `[]Condition` | Standard Kubernetes conditions |

#### Condition Types

| Type | Meaning when `True` |
|------|---------------------|
| `Ready` | The data plane is serving: the instances the spec asks for are running, reachable and, on a multi-replica cluster, replicating. Every `Valkey` carries it. **It answers a different question than `status.phase`, and on a blocked cluster the two disagree by design.** `phase` carries both the data-plane verdict *and* whether the operator can converge the spec, and while a managed write is being refused the second meaning wins the field and reports `Error` — a spec the operator cannot apply has to be visible. `Ready` carries only the first and keeps reporting the truth about the running cluster, because a rejected write says nothing about it. So `Ready=True` next to `phase=Error` means: your cluster is serving, and the operator cannot write something — read `status.message` and `ReconcileBlocked` to find out what. During a rolling update the condition keeps its pre-roll value, because a pass with a roll in flight writes its own phase and returns before the status computation ([ADR 0001](docs/adr/0001-continue-reconciling-past-a-rejected-write.md) D4). |
| `RollingUpdatePaused` | A rolling update stopped waiting because a replaced pod did not sync within `spec.rollingUpdate.syncTimeout`. **It reports an expired wait, not a halted operator:** the pause clears the rolling-update state, so a later pass that still finds outdated pods dispatches again on a fresh budget and sets the condition again; a spec change restarts the roll from the beginning rather than resuming it. It goes `False` with reason `Completed` when a roll finishes and with reason `Converged` when there is no roll left to run — the usual cause being a spec put back to what the pods already run. Both clears sit in `checkAndHandleRollingUpdate`, so every topology reaches them; before that the only clear was on the Sentinel path. Written only on a cluster that paused — clusters that never did carry no such condition. See [ADR 0002](docs/adr/0002-surface-a-blocked-reconcile-on-the-cr.md) D10. |
| `TopologyRestored` | The last data-tier rolling update of a multi-replica non-Sentinel cluster handed the master role back to pod-0. `False` means the operator gave up waiting for pod-0 and left the promoted replica as master — the cluster is healthy, its master is just not pod-0. **This is a verdict about that update, not a live statement about the topology now.** Nothing outside a rolling update writes it, so a later steady-state adoption (a drain that promoted another pod, for instance) moves the master without touching the condition, and a `True` can sit next to a non-pod-0 master indefinitely. **Read `status.masterPod` for the master; read this for what the last update did.** It is never written on a Sentinel-enabled or single-replica cluster, and never cleared when a cluster becomes one. See [ADR 0010](docs/adr/0010-every-rolling-update-wait-is-bounded.md) D15. |
| `SidecarUpdatePending` | A single-replica cluster's pod carries an outdated sidecar image. The operator does not restart the only pod for a sidecar-only change; the update applies on the next pod restart. The message names the pod and the `spec.replicas` the deferral was decided on. It clears itself at either of the two sites that prove every pod matches the live StatefulSet template: the pass that finds nothing to update, and the pass that completes a rolling update. A cluster whose `spec.replicas` is above 1 never gains it — the sidecar image is part of the pod-spec hash, so a sidecar change rides the ordinary rolling update. Note the condition follows `spec.replicas`, not the number of running pods: those two can differ while a StatefulSet write is being refused (see [`spec.persistence`](#specpersistence)), which is why the message states the replica count it decided on. See [ADR 0002](docs/adr/0002-surface-a-blocked-reconcile-on-the-cr.md) D10 and D10a. |
| `ReconcileBlocked` | A managed resource could not be written, or the operator refused to write it. The reason says which, because they end differently: `AdmissionWebhookDenied` (a cluster-side admission gate rejected the write, or a fail-closed webhook could not be called — clears itself once the gate reopens), `ForeignObject` (one of the generated names is held by an object this `Valkey` does not control — clears when someone deletes or renames it), `RecreateRequired` (a StatefulSet whose immutable `volumeClaimTemplates` no longer match `spec.persistence` — clears when the StatefulSet is recreated or the spec is put back, see [`spec.persistence`](#specpersistence)), `WriteFailed` (any other write failure: RBAC, quota, conflict, API server unreachable). When one pass produces several, the reason reported is the one that needs a human: `ForeignObject` before `RecreateRequired` before `AdmissionWebhookDenied`. |
| `StorageSpecNotApplied` | The storage `spec.persistence` asks for is not the storage the cluster runs on, because a StatefulSet's `volumeClaimTemplates` are immutable. Reason `RecreateRequired` means the operator writes nothing to that StatefulSet at all until it is recreated (`ReconcileBlocked` carries the same reason); reason `VolumeClaimTemplatesImmutable` means only `size`/`storageClass`/access modes are stuck while every other change still applies. The message names the difference. It goes `False` with reason `StorageSpecApplied` once the live claims match again, and is written only on a cluster that had a conflict — clusters that never had one carry no such condition. **On a Sentinel-enabled cluster both StatefulSet reconcilers evaluate it, and only the data one may resolve it:** a tier whose `volumeClaimTemplates` are empty by construction can never prove that the storage the spec asks for is the storage that runs. See [`spec.persistence`](#specpersistence) and [ADR 0023](docs/adr/0023-volume-claim-templates-are-immutable.md) D4a. |
| `SentinelPeersStale` | At least one Sentinel knows more other Sentinels than the cluster has. Sentinel never forgets a peer it has seen, and the majority a failover leader needs is computed over that whole table — so the surplus is failover capacity that is already gone, not a display issue. The message names each pod and its count. Clear it with `SENTINEL RESET <cluster-name>` on one Sentinel at a time **while the master is healthy** (a reset with the master unreachable leaves that Sentinel knowing nothing and unable to rediscover), or leave it to the next Sentinel roll. Only written while all Valkey and Sentinel pods are Ready; a pass where no Sentinel answers leaves the previous value. A cluster reporting `True` is re-checked every 5 minutes, so a reset shows up as a cleared condition within the maintenance window rather than at the next cache resync. See [ADR 0022](docs/adr/0022-sentinel-identity-is-pinned-to-the-pod.md). |
| `SentinelUpdatePending` | The Sentinel tier is being rolled: at least one Sentinel pod runs an outdated spec, or a replacement pod is not Ready yet. The `RollingUpdateComplete` event covers the **data tier only** and fires before the first Sentinel pod is replaced — the update as a whole is finished when this condition goes `False` with reason `Completed`, which is also the moment the `SentinelUpdateComplete` event is emitted. Reason `SentinelDisabled` means Sentinel was disabled while the condition stood (no completion event in that case). Written only on a cluster whose Sentinel tier actually rolled; clusters that never rolled carry no such condition. See [ADR 0024](docs/adr/0024-the-sentinel-tier-reports-its-own-completion.md). |
| `PodTerminationStalled` | A pod of the tier being rolled has been `Terminating` for more than two minutes past its own graceful deletion deadline, and the rolling update of that tier is holding: the operator never deletes a pod of a tier while another pod of that tier is on its way out. **The condition does not lift the hold and nothing resumes it** — deleting a second pod because the first is wedged is what the hold prevents. What it marks is that the operator stopped ending the reconcile pass on the wait, so the Sentinel roll, the no-master recovery, the steady-state split-brain check and the status write run again while the stall lasts. The message names the pod and how far past its deadline it is. Look at that pod: a `NodeNotReady` node, a stuck finalizer and a container ignoring SIGTERM are the usual causes. It clears itself with reason `PodTerminationCleared` once the pod is gone, and the roll continues on its own. No Event accompanies it. See [ADR 0026](docs/adr/0026-a-pod-being-deleted-is-not-available.md). |
| `TLSMaterialStale` | At least one pod is still running the TLS material from before the last certificate rotation. **This is True for the length of every ordinary rotation roll and is not urgent**: cert-manager renews 30 days before expiry and the previous certificate keeps working for those 30 days, so the pods have a month of slack and the shipped alert waits three days before firing. The message names the pods and their tier. It clears itself with reason `TLSMaterialCurrent` once every measured pod carries the current fingerprint. What it exists to catch is the roll that **never starts** — the operator missed the Secret event, cannot write the StatefulSet, or is blocked for an unrelated reason — because no other signal fires in that case and the pods then keep the old certificate until it expires. Only written on TLS clusters, and only measured for pods that already carry the fingerprint (`VKO_TLS_MATERIAL_HASH` on the sidecar container, or the superseded `vko.gtrfc.com/tls-material-hash` annotation on pods from before 2026-08-27); pods created by an older operator are unmeasured, not stale. See [Certificate rotation](#certificate-rotation). |
| `MultipleMasters` | More than one data pod answered that it is the master while a rolling update was in flight. This is **not by itself a fault**: every controlled failover has a window in which the promoted pod and the outgoing one both answer master, and the operator closes it on the same pass. The reason tells the two apart — `MultipleMastersTransitional` is inside the 90 s bound and carries no Event, `MultipleMastersPersisted` is past it and is the moment the `SplitBrainDetected` **Warning** event fires. The message names the pods and the authority. It goes `False` with reason `SingleMaster` on the first pass that sees at most one master; a rolling update abandoned with rogue masters still present leaves it `True` until the next one, and so does a resolution the operator **refused** because demoting would have discarded the only dataset ([ADR 0028](docs/adr/0028-a-demotion-may-not-discard-the-only-dataset.md)) — a refusal carries no Event of its own, so a `MultipleMasters` that outlives the 90 s bound is how it surfaces. Written only during a rolling update — clusters that never saw two masters carry no such condition. See [ADR 0025](docs/adr/0025-a-split-brain-warning-means-one-that-did-not-resolve-itself.md). |

#### Phase Values

| Phase | Description |
|-------|-------------|
| `OK` | Cluster is healthy |
| `Provisioning` | Initial setup in progress |
| `Syncing` | Replication sync in progress |
| `Rolling Update X/Y` | Data-tier rolling update progress |
| `Sentinel Rolling Update X/Y` | Sentinel-tier rolling update progress (runs after the data tier, or alone on Sentinel-only spec changes) |
| `Failover in progress` | Sentinel-triggered leader switch |
| `Error` | Error state (see `message` for details). **Covers two different things:** a data plane the operator cannot verify (an unreachable instance, a failed cluster health check, a paused roll), and a healthy cluster whose spec the operator cannot converge because a managed write is being refused. Read `message` and the `ReconcileBlocked` condition to tell them apart — and read the `Ready` condition for the data plane, which stays `True` in the second case on purpose. |

---

## Common Labels

All managed resources carry a consistent set of labels:

```yaml
app.kubernetes.io/component: valkey | sentinel
app.kubernetes.io/instance: <cr-name>
app.kubernetes.io/managed-by: vko.gtrfc.com
app.kubernetes.io/name: valkey
app.kubernetes.io/version: <image-tag>
vko.gtrfc.com/cluster: <cr-name>
```

Pod-level labels additionally include:

```yaml
vko.gtrfc.com/instanceName: <pod-name>
vko.gtrfc.com/instanceRole: master | replica
```

---

## TLS Details

When TLS is enabled (`spec.tls.enabled: true`):

- The plaintext port `6379` is disabled (`port 0`) — set `spec.tls.allowUnencrypted: true` to keep it open (dual-port mode)
- Valkey listens on TLS port `16379`
- Sentinel listens on TLS port `36379` (= 26379 + 10000, following Valkey's `+10000` convention)
- All replication traffic is encrypted (`tls-replication yes`) regardless of `allowUnencrypted`
- Probes use `valkey-cli --tls` with the mounted certificates

### Certificate rotation

**The operator replaces the pods whose TLS material they cannot reload, and only those.**

A Kubernetes Secret volume is rewritten in place when cert-manager rotates the certificate
it holds. A process that parsed the old bytes at startup keeps using them until it exits —
so on a cluster whose pods outlive a rotation, those processes eventually present an expired
certificate and are rejected, silently, with valid material sitting in the mount.

Which processes can pick up new material on their own:

| Process | Reloads? | Why |
|---|---|---|
| init containers | yes | they shell out to `valkey-cli` per invocation and read the files fresh |
| the operator sidecar | yes | re-reads its material per command |
| the cluster observer | yes | same, and it runs alone in its Deployment |
| `valkey-server` | not verified | treated as pinning |
| `valkey-sentinel` | not verified | treated as pinning |
| `oliver006/redis_exporter` | no | third-party, long-lived, not the operator's to change |

The restart unit is the pod, not the container, so one non-reloading process spends the
whole pod's exemption. Both StatefulSets therefore carry a fingerprint of their TLS Secret
in the pod template — the `VKO_TLS_MATERIAL_HASH` environment variable of the `sidecar`
container on the data tier and of the `sentinel` container on the Sentinel tier — and a
rotation changes it, which the
**normal failover-aware rolling update** then acts on — the same controlled, one-pod-at-a-time
replacement any other spec change gets. **The observer Deployment carries no fingerprint and is
never restarted for a rotation.**

The trigger is the rotation, not the expiry. cert-manager renews 30 days before expiry and
the previous certificate stays valid for those 30 days, so the roll has a month of slack and
nothing is time-critical; several clusters rotating in the same window simply queue behind
`--max-concurrent-reconciles`. The `TLSMaterialStale` condition and the `ValkeyTLSMaterialStale`
alert cover the one case the slack does not: a roll that never starts.

> **Known gap, measured 2026-08-27: a TLS cluster that has never been changed since it was
> created is not covered.** The operator creates the cert-manager `Certificate` and the
> StatefulSet in the same pass, so the first pod template is written before cert-manager has
> issued and carries no fingerprint — and the pods are built from that template. They are then
> *unmeasured* rather than stale, so a rotation moves the template and replaces nothing, and
> `TLSMaterialStale` stays `False`. It resolves the first time anything else replaces the pods,
> which an operator upgrade does for the data tier. Until it is fixed, force one roll on a new
> TLS cluster — any spec change does — or delete its data pods once
> `kubectl get sts <name> -o yaml` shows `VKO_TLS_MATERIAL_HASH` on the `sidecar` container.

**Upgrading to an operator version that has this mechanism rolls nothing.** A pod that does not
carry the fingerprint is never restarted for it, so existing pods adopt the
fingerprint the next time they are replaced for another reason, and only rotations after that
roll them.

**On a single-replica cluster the roll is a restart of the only pod**, exactly like any other
change to the pod spec or the generated config — brief downtime, and **data loss if
`spec.persistence` is off**. That is unchanged from how this operator has always treated a
standalone instance; it is called out here because a certificate rotation is the first thing
that triggers it without anyone editing the CR. Turn on `spec.persistence` for standalone
instances whose dataset matters.

### Port Summary

| Component | No TLS | TLS only | TLS + `allowUnencrypted` |
|-----------|--------|----------|--------------------------|
| Valkey | `6379` | `16379` | `16379` + `6379` |
| Sentinel | `26379` | `36379` | `36379` + `26379` |
| Metrics exporter | `9121` | `9121` | `9121` |

The metrics exporter always serves plaintext HTTP on `9121` (configurable via `spec.metrics.port`); it connects to the local Valkey over the TLS port internally when TLS is enabled.

### Dual-Port Mode (`allowUnencrypted`)

Set `spec.tls.allowUnencrypted: true` and/or `spec.sentinel.allowUnencrypted: true` to keep the corresponding plaintext port open alongside the TLS port. This is useful for:

- **Gradual TLS rollout** — migrate clients one by one without downtime
- **Mixed environments** — some workloads use TLS, others cannot
- **Debugging** — plaintext access with simple tools during development

When `allowUnencrypted` is true, the existing services expose an additional port alongside the TLS port:

| Service | TLS port | Plain port (added) |
|---------|----------|--------------------|
| `<name>-rw` | `16379` (`valkey`) | `6379` (`valkey-plain`) |
| `<name>-all` | `16379` (`valkey`) | `6379` (`valkey-plain`) |
| `<name>-r` | `16379` (`valkey`) | `6379` (`valkey-plain`) |
| `<name>-sentinel-headless` | `36379` (`sentinel`) | `26379` (`sentinel-plain`) |

No new services are created — the same service names are used for both TLS and plaintext access.

> **Note on Sentinel discovery:** When a client connects to Sentinel on the plaintext port (`26379`) and calls `SENTINEL get-master-addr-by-name`, Sentinel always returns the TLS port (`16379`). This is by design — use the unencrypted Valkey services directly if the client cannot handle TLS data connections.

**Connecting to a TLS-enabled instance from within the cluster:**

```bash
valkey-cli --tls \
  --cert /tls/tls.crt \
  --key /tls/tls.key \
  --cacert /tls/ca.crt \
  -h my-valkey -p 16379 PING
```

### Unified TLS Certificate (Valkey + Sentinel)

By default the operator issues **two** `Certificate` resources when cert-manager
is enabled together with Sentinel:

| Certificate | Secret | Covers |
|-------------|--------|--------|
| `<name>-tls` | `<name>-tls` | Valkey pod / service hostnames |
| `<name>-sentinel-tls` | `<name>-sentinel-tls` | Sentinel pod / headless hostnames |

Some Sentinel-aware clients (e.g. **`go-redis`**) reuse the same `tls.Config` for
both the Sentinel discovery connection and the subsequent master connection.
That client validates the Valkey master certificate against the Sentinel
hostname (or vice versa) and fails with an error like:

```
x509: certificate is valid for oauth2-valkey-0.oauth2-valkey-headless.iam..., 
not oauth2-valkey-sentinel-2.oauth2-valkey-sentinel-headless.iam...
```

To fix this, set `spec.tls.unifiedCertificate: true`. With cert-manager, the
operator then issues a **single** `Certificate` whose SAN list covers both
Valkey and Sentinel hostnames, and both StatefulSets mount the same Secret.
With a user-provided Secret, the flag is informational — the same Secret is
already mounted by both StatefulSets.

```yaml
apiVersion: vko.gtrfc.com/v1
kind: Valkey
metadata:
  name: oauth2-valkey
spec:
  replicas: 3
  sentinel:
    enabled: true
    replicas: 3
  tls:
    enabled: true
    unifiedCertificate: true
    certManager:
      issuer:
        kind: ClusterIssuer
        name: cluster-ca
```

Resulting layout:

| Certificate | Secret | Covers |
|-------------|--------|--------|
| `<name>-tls` | `<name>-tls` | Valkey **and** Sentinel hostnames |

**Migration of an existing cluster** is automatic and safe:

1. The operator updates `<name>-tls` so its SAN list now also includes the
   Sentinel hostnames (cert-manager re-issues the Secret in place).
2. The Sentinel `StatefulSet` spec is patched to mount `<name>-tls` instead
   of `<name>-sentinel-tls`, triggering a rolling restart of the Sentinel
   pods onto the shared Secret.
3. Once every Sentinel pod runs against `<name>-tls`, the operator deletes
   the legacy `<name>-sentinel-tls` `Certificate` and `Secret`.

The deletion in step 3 is gated on the StatefulSet already referencing the
unified Secret, so a pod restart between steps cannot land on a missing
volume. The migration completes in at most two reconcile passes.

---

## Persistence Modes

| Mode | Description |
|------|-------------|
| `rdb` | Point-in-time snapshots (`save 900 1`, `save 300 10`, `save 60 10000`) |
| `aof` | Append-only file with `appendfsync everysec` |
| `both` | RDB + AOF combined for maximum durability |

---

## Development

### Prerequisites

- Go 1.26+
- Docker
- [Kind](https://kind.sigs.k8s.io/) (for local E2E testing)
- [cert-manager](https://cert-manager.io/) (for TLS E2E tests)

### Build

```bash
make build        # Build operator binary
make docker-build # Build container image
```

### Test

```bash
make test-unit               # Unit tests
make test-unit-coverage      # Unit tests with coverage
make test-integration        # Integration tests (envtest)
make test-e2e                # E2E tests (requires running cluster)
make test-e2e E2E_RUN='TestE2E_PodDisruptionBudget'  # E2E tests filtered by name
make e2e-local               # Full E2E: create Kind cluster (control-plane + 3 workers) → deploy → test → cleanup
make lint                    # Linting (golangci-lint + go vet)
make gosec                   # Security scan
make vuln                    # Vulnerability check
make cyclo                   # Cyclomatic complexity check
```

### Run Locally

```bash
make run  # Run the operator against the current kubeconfig
```

---

## Helm Chart Values

The operator itself is configured via Helm values:

```yaml
replicaCount: 1

image:
  repository: guidedtraffic/valkey-operator
  pullPolicy: IfNotPresent
  tag: ""            # defaults to Chart appVersion

resources:
  limits:
    cpu: 500m
    memory: 128Mi
  requests:
    cpu: 10m
    memory: 64Mi

leaderElection:
  enabled: true      # required for HA operator deployment

maxConcurrentReconciles: 4   # default; how many Valkey resources are reconciled at once

metrics:                       # the OPERATOR's own endpoint, not spec.metrics on a CR
  service:
    enabled: false             # default; ClusterIP Service in front of :8080
    port: 8080                 # default
    labels: {}                 # default; extra labels on the Service
  serviceMonitor:
    enabled: false             # default; needs the Prometheus-Operator CRDs
    interval: 30s              # default
    scrapeTimeout: ""          # default; left to Prometheus when empty
    labels: {}                 # default; must match your serviceMonitorSelector
  prometheusRule:
    enabled: false             # default; ships the alert rules below
    labels: {}                 # default; must match your ruleSelector
```

`maxConcurrentReconciles` bounds how far one unhealthy cluster can slow down the rest:
a reconcile pass dials the pods of its cluster with a 5 s timeout each, so with a single
worker a cluster whose pods stopped answering delays every other Valkey resource in the
fleet. Passes for the *same* resource stay serialised at any value. Raise it for large
fleets, lower it to reduce concurrent API-server load
([ADR 0019](docs/adr/0019-reconcile-concurrency-and-the-cost-of-a-stuck-pass.md)).

### Operator metrics and alerting

`metrics.*` above configures the **operator's own** endpoint. It is unrelated to
`spec.metrics` on a `Valkey` resource, which adds a Prometheus exporter sidecar to that
resource's pods.

The operator always serves `:8080/metrics`. Besides controller-runtime's counters it
publishes one set of `vko_valkey_*` series per `Valkey` resource, labelled with namespace
and name — so an alert can say *which* resource is not converging.
`controller_runtime_reconcile_errors_total` only carries the controller name and cannot
([ADR 0021](docs/adr/0021-per-resource-metrics-and-the-alert-that-was-missing.md)).

| Metric | Labels | Meaning |
|---|---|---|
| `vko_valkey_status_phase` | `namespace`, `name`, `phase` | Always `1`; one series per resource carrying its current phase |
| `vko_valkey_status_condition` | `namespace`, `name`, `condition`, `status`, `reason` | Always `1`; one series per status condition |
| `vko_valkey_status_ready_replicas` | `namespace`, `name` | Ready data pods the operator last observed |
| `vko_valkey_spec_replicas` | `namespace`, `name` | Data pods requested by `spec.replicas` |
| `vko_valkey_metadata_generation` | `namespace`, `name` | The spec version the API server holds |
| `vko_valkey_status_observed_generation` | `namespace`, `name` | Newest generation any condition reports as observed |
| `vko_valkey_operator_version_info` | `namespace`, `name`, `version` | Always `1`; the operator that last wrote status |
| `vko_operator_build_info` | `version`, `commit` | Always `1`; the operator that is running |
| `vko_valkey_collector_success` | — | `1` when the last scrape could list the resources, `0` when it could not |

The pair to watch is `vko_valkey_metadata_generation` against
`vko_valkey_status_observed_generation`: a gap means a spec change was accepted by the API
server and never converged — for example a field that is immutable on an already created
object. That is the shape of failure that can sit unnoticed for months, because the pods stay
up and only the spec change is stuck.

`prometheusRule.enabled: true` ships eight alerts over these series — `ValkeySpecNotObserved`,
`ValkeyReconcileBlocked`, `ValkeyPhaseNotOK`, `ValkeyReplicasMissing`,
`ValkeyOperatorVersionStale`, `ValkeyTLSMaterialStale`, `ValkeyMetricsCollectorFailing` and
`ValkeyMetricsAbsent`. Every rule is guarded on `vko_valkey_collector_success`, so a collector
that cannot read reports "unknown" instead of "healthy". Thresholds are not exposed as values;
replace the rule if they do not fit.

`ValkeyTLSMaterialStale` is the odd one out on timing: its `for:` is **72 hours**, not minutes,
because the roll it watches is deliberately not time-critical (see
[Certificate rotation](#certificate-rotation)). A short threshold would page on every normal
rotation.

Turning `serviceMonitor.enabled` on renders the Service as well — a ServiceMonitor selects
Services, and one without the other scrapes nothing.

> **Security note:** the endpoint is plain HTTP with no authentication filter wherever it
> binds, and the per-resource series make it an inventory of every `Valkey` resource in the
> cluster and its health. It carries no Secret material and no spec contents. Adding the
> Service does **not** change reachability — the container port is declared either way, so
> anything that can route to the operator pod already reads `:8080`. Restrict it with a
> NetworkPolicy in the operator namespace, move it with `--metrics-bind-address`, or switch it
> off with `--metrics-bind-address=0`
> ([SECURITY_ARCHITECTURE.md](SECURITY_ARCHITECTURE.md)).

---

## Architecture

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

---

## License

[Apache License 2.0](LICENSE)
