# TLS

What changes when a `Valkey` resource turns TLS on, how certificate rotation reaches the
pods, which port serves what, and the two TLS variants: dual-port mode and the unified
certificate. The fields themselves — `spec.tls`, `spec.tls.certManager`,
`spec.sentinel.allowUnencrypted` — are in the [CRD reference](../../README.md#spectls);
mutual TLS for the observer is [observer.md](observer.md#mutual-tls-mtls). Where the TLS
material lives and who can read it is
[docs/security/secrets-and-tls.md](../security/secrets-and-tls.md). The decisions are
[ADR 0016](../adr/0016-authentication-and-tls-posture.md) (the TLS posture) and
[ADR 0030](../adr/0030-rotating-certificates-rotate-the-instances-that-cannot-reload-them.md)
(rotation), with the fingerprint's place in the pod spec from
[ADR 0031](../adr/0031-a-record-the-operator-trusts-lives-in-pod-spec.md).

## When TLS is enabled

When TLS is enabled (`spec.tls.enabled: true`):

- The plaintext port `6379` is disabled (`port 0`) — set `spec.tls.allowUnencrypted: true` to keep it open (dual-port mode)
- Valkey listens on TLS port `16379`
- Sentinel listens on TLS port `36379` (= 26379 + 10000, following Valkey's `+10000` convention)
- All replication traffic is encrypted (`tls-replication yes`) regardless of `allowUnencrypted`
- Probes use `valkey-cli --tls` with the mounted certificates

## Certificate rotation

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

The two "not verified" rows have a measuring instrument: on every health pass the operator
compares the certificate each pod actually serves against the `tls.crt` in its Secret, and
logs a mismatch (`pod serves a TLS certificate that differs from the one in its Secret`).
During a rotation roll that line is expected; outside one it means a roll is not happening.
The matching case logs at verbosity 1, so whether `valkey-server` reloads on its own can be
measured by raising the operator log level across a rotation window. Report-only — it never
fails a handshake and never triggers a roll.

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
`--max-concurrent-reconciles`. The [`TLSMaterialStale`](status.md#tlsmaterialstale) condition
and the `ValkeyTLSMaterialStale` alert ([monitoring.md](monitoring.md#operator-metrics-and-alerting))
cover the one case the slack does not: a roll that never starts.

**A fresh TLS cluster is covered from birth.** The operator refuses to create a StatefulSet
whose TLS material it cannot fingerprint yet, so the StatefulSet appears a few seconds after
the CR — once cert-manager has issued — and every pod carries the fingerprint from its first
start. (Before 2026-08-27 the StatefulSet was created alongside the `Certificate`, its first
pods carried no fingerprint, and a rotation never rolled a cluster that had not been changed
since creation.) The one operational consequence: with a user-provided `spec.tls.secretName`,
the Secret has to exist — until it does, no data plane is provisioned and the CR stays in
`Provisioning` with "Waiting for StatefulSet creation".

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

## Port Summary

| Component | No TLS | TLS only | TLS + `allowUnencrypted` |
|-----------|--------|----------|--------------------------|
| Valkey | `6379` | `16379` | `16379` + `6379` |
| Sentinel | `26379` | `36379` | `36379` + `26379` |
| Metrics exporter | `9121` | `9121` | `9121` |

The metrics exporter always serves plaintext HTTP on `9121` (configurable via `spec.metrics.port`); it connects to the local Valkey over the TLS port internally when TLS is enabled.

## Dual-Port Mode (`allowUnencrypted`)

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

## Unified TLS Certificate (Valkey + Sentinel)

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
  image: valkey/valkey:8.0   # example; spec.image is required - this line was missing before 2026-09-27
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
