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
| `observerReady` | `bool` | Whether the observer deployment is ready (only set when `observer.enabled: true`) |
| `phase` | `string` | Current lifecycle phase |
| `message` | `string` | Human-readable status description |
| `conditions` | `[]Condition` | Standard Kubernetes conditions |

#### Phase Values

| Phase | Description |
|-------|-------------|
| `OK` | Cluster is healthy |
| `Provisioning` | Initial setup in progress |
| `Syncing` | Replication sync in progress |
| `Rolling Update X/Y` | Rolling update progress |
| `Failover in progress` | Sentinel-triggered leader switch |
| `Error` | Error state (see `message` for details) |

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
```

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
