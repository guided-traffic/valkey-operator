# Examples

Complete `Valkey` manifests for the common shapes, each with what it is for. Every field is
in the [CRD reference](../../README.md#crd-reference); the page for each subject explains
what the fields do. The resources each manifest makes the operator create, and how they are
named, are in the [naming conventions](../../README.md#naming-conventions).

## Standalone — Minimal

The simplest deployment: a single Valkey pod with no persistence, no TLS, no auth.
Its manifest is the standalone instance of the [fast start](../../README.md#fast-start).

## Standalone — With Persistence

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

The modes and what changing storage later does: [persistence.md](persistence.md).

## Standalone — With TLS (cert-manager)

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

Which port serves what once TLS is on: [tls.md](tls.md#when-tls-is-enabled).

## Standalone — With TLS + Dual Port

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

More on the mode: [tls.md](tls.md#dual-port-mode-allowunencrypted).

## Standalone — With TLS (User-Provided Secret)

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

## HA — 3 Replicas with Sentinel

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

## HA — Full Production Setup (TLS + Persistence + Labels)

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
                                 # See "Unified TLS Certificate" in tls.md.
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

## HA — With Cluster Observer

Deploy a diagnostic observer alongside the cluster. What it checks, what it creates and the endpoints it serves: [observer.md](observer.md).

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

## With Metrics (Prometheus Exporter)

Attach a metrics exporter sidecar to every Valkey pod. What the exporter does, what enabling it creates and what it rolls: [monitoring.md](monitoring.md#the-exporter-sidecar).

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
    # image: oliver006/redis_exporter:v1.92.1@sha256:7fbc93d30f0f91eed1b2fe6968a956259cc5d260a984dd9071f1d1e9c2692ecd  # optional; default shown
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

## HA — With Authentication

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

## HA — With Authentication (Sentinel Unauthenticated)

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
