# Cluster observer

The optional diagnostic Deployment of `spec.observer`: what it checks, what it creates,
the endpoints it serves, which failures make it report unready, and when it presents a
client certificate. The fields and their defaults are in the
[`spec.observer`](../../README.md#specobserver) tables; a complete manifest is the
[observer example](examples.md#ha--with-cluster-observer). Its readiness is reported on
the `Valkey` resource as `status.observerReady`.

The observer continuously runs health checks (PING, write/read tests, replication sync, Sentinel quorum) and exposes results via readiness probe and Prometheus metrics on port `8084`.

## What it creates

With `spec.observer.enabled`, the operator (not the observer) creates a Deployment with one
observer pod running the operator image. The pod runs under a ServiceAccount of its own,
bound to no Role and with no token mounted, because the observer makes no Kubernetes API
call ([trust boundaries](../security/trust-boundaries.md)). With `spec.networkPolicy.enabled`
the operator also creates a NetworkPolicy for the observer pod that admits nothing: kubelet's
probes on `8084` come from the node, and a Prometheus scraping `/metrics` there needs a policy
of your own ([network-policy.md](network-policy.md#admitting-a-scraper)). *(corrected
2026-09-29: the policy admitted port `8084` from any source until this release.)* Their names,
including the `spec.networkPolicy.namePrefix` prefix on the NetworkPolicy, are in the README
[naming conventions](../../README.md#naming-conventions).

*(corrected 2026-09-27: the table moved here from the old README listed the Deployment and
the NetworkPolicy only and left out the observer ServiceAccount; the names now live only in
the README naming conventions.)*

## Health endpoints

| Endpoint | Description |
|----------|-------------|
| `GET /readyz` | 200 if all checks pass, 503 otherwise (JSON body with per-check details) |
| `GET /healthz` | Always 200 (liveness) |
| `GET /metrics` | Prometheus metrics |

## Which failures make it unready (`unreadyWhen`)

Each field controls whether the corresponding check failure flips the observer to unReady.
When a field is `false`, failures are still logged but do not affect the ready state.
Omitting a field is equivalent to `true`. The check behind each field is in the
[`spec.observer.unreadyWhen`](../../README.md#specobserverunreadywhen) table.

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

## Mutual TLS (`mtls`)

When `spec.tls.enabled: true`, the observer always verifies the server's certificate. These flags additionally enable **mutual TLS (mTLS)** by sending a client certificate. When neither flag is set, no certificate secret is mounted into the observer pod.

> **Note:** The TLS secret is only mounted into the observer pod when at least one of `mtls.valkey` or `mtls.sentinel` is `true`. If both are `false` (the default), the observer connects using TLS without a client certificate and no volume mount is created.
