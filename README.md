# Valkey Operator

A Kubernetes operator for deploying and managing production-grade [Valkey](https://valkey.io/) instances — standalone or highly available with Sentinel.

[![Go](https://img.shields.io/badge/Go-1.26-blue.svg)](https://go.dev/)
[![Kubernetes](https://img.shields.io/badge/Kubernetes-1.29+-blue.svg)](https://kubernetes.io/)
[![License](https://img.shields.io/badge/license-Apache%202.0-green.svg)](LICENSE)

## Features

- **Standalone & HA modes** — single-node or multi-node with automatic Sentinel deployment
- **TLS encryption** — full TLS for Valkey, replication, and Sentinel via cert-manager or user-provided Secrets
- **Dual-port mode** — optional `allowUnencrypted` flag keeps plaintext ports open alongside TLS for gradual migration
- **Persistence** — RDB, AOF, or both with configurable PVCs
- **Authentication** — password from Kubernetes Secret
- **Observability** — CRD status visible in `kubectl` and Lens, Kubernetes Events
- **Controlled rolling updates** — replica-first rollout with replication sync verification and automatic failover
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
| `podLabels` | `map[string]string` | — | Additional labels for Valkey pods |
| `podAnnotations` | `map[string]string` | — | Additional annotations for Valkey pods |
| `resources` | `ResourceRequirements` | — | CPU/memory requests and limits |

### `spec.sentinel`

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | `bool` | `false` | Enable Sentinel HA mode |
| `replicas` | `int32` | `3` | Number of Sentinel instances |
| `allowUnencrypted` | `bool` | `false` | Keep plaintext Sentinel port (`26379`) open alongside TLS port (`36379`). Only effective when `spec.tls.enabled: true`. |
| `podLabels` | `map[string]string` | — | Additional labels for Sentinel pods |
| `podAnnotations` | `map[string]string` | — | Additional annotations for Sentinel pods |

### `spec.tls`

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | `bool` | `false` | Enable TLS encryption |
| `allowUnencrypted` | `bool` | `false` | Keep plaintext Valkey port (`6379`) open alongside TLS port (`16379`). Replication always uses TLS. |
| `certManager` | `CertManagerSpec` | — | cert-manager integration (mutually exclusive with `secretName`) |
| `secretName` | `string` | — | Name of existing TLS Secret (must contain `tls.crt`, `tls.key`, `ca.crt`) |

### `spec.tls.certManager`

| Field | Type | Description |
|-------|------|-------------|
| `issuer.kind` | `string` | `Issuer` or `ClusterIssuer` |
| `issuer.name` | `string` | Name of the issuer resource |
| `issuer.group` | `string` | API group (default: `cert-manager.io`) |
| `extraDnsNames` | `[]string` | Additional DNS names for the certificate |

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
make e2e-local               # Full E2E: create Kind cluster → deploy → test → cleanup
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
│  └─────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────┘
```

---

## License

[Apache License 2.0](LICENSE)
