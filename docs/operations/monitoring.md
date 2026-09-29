# Monitoring

Two unrelated metrics surfaces: the **exporter sidecar** that `spec.metrics` adds to the
pods of one `Valkey` resource, and the **operator's own endpoint**, configured by the
chart's `metrics.*` values, which reports on every `Valkey` resource in the cluster. The
fields and values are in the [`spec.metrics`](../../README.md#specmetrics) and
[Helm chart values](../../README.md#helm-chart-values) tables. The cluster observer, which
also serves metrics, is [observer.md](observer.md). The decisions are
[ADR 0018](../adr/0018-metrics-and-the-exporter-sidecar.md) (the exporter) and
[ADR 0021](../adr/0021-per-resource-metrics-and-the-alert-that-was-missing.md) (the
operator's per-resource metrics).

## The exporter sidecar

The exporter (`oliver006/redis_exporter` by default) connects to the local Valkey instance and serves `/metrics` on port `9121`. The operator wires the connection: under TLS the exporter uses the pod's mounted certificates, and with `spec.auth` it gets the cluster password from the auth Secret. *(corrected 2026-09-29: this said "TLS and authentication are handled automatically", which read as if the exporter's own port were protected. It is not.)*

The exporter's port is **plain HTTP without authentication**: whatever it serves, it serves to
anything that reaches the port. The operator therefore switches off the two routes it does not
use, on every cluster — `/scrape`, which connects to a target named in the request with the
exporter's credentials, and the export of key values (`REDIS_EXPORTER_DISABLE_SCRAPE_ENDPOINT`,
`REDIS_EXPORTER_DISABLE_EXPORTING_KEY_VALUES`). A key you configure through
`spec.metrics.extraArgs` (`--check-single-keys`, `--check-keys`) is exported with its name and
size, not its value. An image you set through `spec.metrics.image` must be v1.83.0 or later for
the first switch to take effect; see the `image` row of the
[`spec.metrics`](../../README.md#specmetrics) table.

With `spec.networkPolicy.enabled`, the generated policy admits no scraper on the exporter port:
admit your Prometheus with a policy of your own, as in
[network-policy.md](network-policy.md#admitting-a-scraper).

Enabling metrics (the [metrics example](examples.md#with-metrics-prometheus-exporter)) adds
the exporter container to every data pod. It has no readiness probe, so it never affects pod
routing. It also creates a ClusterIP Service exposing the `metrics` port across all data pods,
unless `spec.metrics.service.enabled` is `false`; an enabled ServiceMonitor forces that
Service on. The Service carries the marker label `vko.gtrfc.com/metrics: "true"`, so the
ServiceMonitor selects only it. A Prometheus-Operator `ServiceMonitor` scraping that Service is
created only with `spec.metrics.serviceMonitor.enabled: true`. The names of the container, the
Service and the ServiceMonitor are in the README
[naming conventions](../../README.md#naming-conventions).

The `ServiceMonitor` is managed as an unstructured object, so the operator has **no build-time dependency** on the Prometheus-Operator. If the `monitoring.coreos.com` CRDs are not installed, the operator logs a message and skips the `ServiceMonitor` rather than failing.

### Enabling metrics on a running cluster

> **Migration through the rolling update:** turning `metrics.enabled` on (or off) changes the pod template, which the operator rolls out through its normal failover-aware rolling update — replicas are replaced one by one and the leader is failed over, so **the pre-roll dataset survives even without persistence**. On a Sentinel cluster whose Sentinels cannot run a coordinated failover (before Valkey 9.0, the 8 to 9 upgrade roll included) and on any roll whose coordinated failover fell back to forced, the writes the outgoing master acknowledges during the roll's failover are lost ([the master handover](rolling-updates.md#the-master-handover-on-a-sentinel-cluster)). *(corrected 2026-09-28: this said "lossless" and "no data is lost")* The only exception is a single standalone pod (`replicas: 1`) without persistence: it has no failover target, so adding the sidecar restarts it and its in-memory data is lost.

## Operator metrics and alerting

`metrics.*` in the chart values configures the **operator's own** endpoint. It is unrelated to
`spec.metrics` on a `Valkey` resource, which adds a Prometheus exporter sidecar to that
resource's pods.

The operator always serves `:8080/metrics`. Besides controller-runtime's counters it
publishes one set of `vko_valkey_*` series per `Valkey` resource, labelled with namespace
and name — so an alert can say *which* resource is not converging.
`controller_runtime_reconcile_errors_total` only carries the controller name and cannot
([ADR 0021](../adr/0021-per-resource-metrics-and-the-alert-that-was-missing.md)).

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

`ValkeyPhaseNotOK` fires once a resource has held one phase other than `OK` for 30
minutes. On a Sentinel cluster that includes a replica full sync — after an eviction, a
restart, a Sentinel reconfiguration — which reads phase `Syncing` for its whole length,
because a replica counts as synced only on its own answer
([`Ready`](status.md#ready)): a full sync longer than 30 minutes fires it.

`ValkeyTLSMaterialStale` is the odd one out on timing: its `for:` is **72 hours**, not minutes,
because the roll it watches is deliberately not time-critical (see
[Certificate rotation](tls.md#certificate-rotation)). A short threshold would page on every normal
rotation.

No shipped rule watches [`MasterHandoverStalled`](status.md#masterhandoverstalled), the
hold in front of the delete of a Sentinel cluster's outgoing master. It is exported like
every condition, as
`vko_valkey_status_condition{condition="MasterHandoverStalled",status="True"}` with the
reason as a label, for a rule of your own; whether the chart ships one is not decided
([ADR 0037](../adr/0037-the-master-handover-loses-no-acknowledged-write-and-no-dataset.md)
D7).

Turning `serviceMonitor.enabled` on renders the Service as well — a ServiceMonitor selects
Services, and one without the other scrapes nothing.

> **Security note:** the endpoint is plain HTTP with no authentication filter wherever it
> binds, and the per-resource series make it an inventory of every `Valkey` resource in the
> cluster and its health. What it does and does not disclose, and why adding the Service
> changes nothing about who can read it, is
> [what `:8080` discloses](../security/operator-pod-posture.md#what-8080-discloses). Restrict it with a
> NetworkPolicy in the operator namespace, move it with `--metrics-bind-address`, or switch it
> off with `--metrics-bind-address=0`
> ([H-13](../security/operator-pod-posture.md#h-13)).
