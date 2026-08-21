# ADR 0018: Metrics — an Opt-In Exporter Sidecar, and the Operator's Own Endpoint

## Status

Accepted. Date: 2026-08-21.

The exporter, the metrics Service and the ServiceMonitor are implemented. The operator's own
`--metrics-bind-address` handling is **open**: the flag is parsed and never applied.

Everything in this ADR was **verified by reading** the builders, `cmd/main.go`, the chart
`deployment.yaml` and the controller-runtime version pinned in `go.mod`. Nothing here was
reproduced against a cluster, and no scrape, dashboard or endpoint response was measured.

## Context

Two metrics surfaces are in scope here, and they are easy to confuse:

* the **Valkey** metrics of each managed cluster, which users want in Prometheus;
* the **operator's own** controller-runtime metrics endpoint.

A third one exists and is out of scope for this ADR: the optional Observer deployment
(`spec.observer.enabled`) serves its own Prometheus registry on `/metrics`, on
`ObserverHealthPort` 8084 ([`internal/observer/server.go`](../../internal/observer/server.go),
[`internal/builder/observer.go`](../../internal/builder/observer.go)). It is reconciled next to
`reconcileMetrics` inside the same `reconcileMonitoringResources` step, so the name of that
function covers both.

For the first, the constraint is that a monitoring feature must never be able to take the data
path down, and that most clusters do not have the Prometheus-Operator CRDs installed. For the
second, the constraint turned out to be a defect: controller-runtime defaults
`Metrics.BindAddress` to `:8080`, which happens to equal both the flag default and the chart's
argument — so a flag that reaches nothing looks like it works.

## Decision

**D1 — Metrics are opt-in, as an exporter sidecar on every Valkey pod.**
`spec.metrics.enabled` appends a container built by `buildExporterContainer`
([`internal/builder/statefulset.go`](../../internal/builder/statefulset.go)), default
`oliver006/redis_exporter`, serving `/metrics` on `spec.metrics.port` (default 9121, named
port `metrics`). It connects to **`localhost`** — the TLS port when TLS is on — and reads the
password from the Secret as `REDIS_PASSWORD` **only when `v.IsAuthEnabled()`**; with auth off
no credential reaches the sidecar at all. A per-pod sidecar over localhost needs no network
exposure of the data port and no credential of its own.

Under TLS it is not a bare skip-verify: the exporter sets
`REDIS_EXPORTER_SKIP_TLS_VERIFICATION` **and** mounts the TLS volume, passing
`REDIS_EXPORTER_TLS_CA_CERT_FILE` plus `..._TLS_CLIENT_CERT_FILE` and `..._TLS_CLIENT_KEY_FILE`.
Verification is skipped for a mechanical reason — the name it dials, `localhost`, is not on the
server certificate — not because server identity is deemed irrelevant; the residual of an
unverified server is bounded **precisely because** the connection never leaves the pod's network
namespace. The client certificate is presented regardless, which is what keeps the sidecar
working when the server enables `tls-auth-clients`.

**D2 — The exporter carries no readiness probe.** A readiness probe on the sidecar makes the
whole pod unready when the exporter fails, which removes the pod from the `-rw` and `-r`
Services — **a monitoring failure would take the data path down with it.**

**D3 — The metrics Service carries a marker label, and the ServiceMonitor's selector adds
that marker to the shared labels, so only the metrics Service matches.**
`BuildMetricsService` creates `<name>-metrics` with `vko.gtrfc.com/metrics=true`. The selector
both sides share is `metricsServiceSelector`
([`internal/builder/service.go`](../../internal/builder/service.go)):
`common.SelectorLabels(v, ComponentValkey)` — `app.kubernetes.io/instance`,
`app.kubernetes.io/managed-by`, `app.kubernetes.io/component` — **plus** the marker, which is
the discriminator of the four. The shared labels alone would also match the `-rw` and `-r`
Services and have Prometheus scrape the **data ports**. The dedicated Service is controlled by
`spec.metrics.service.enabled` (default true; an enabled ServiceMonitor forces it on, since a
ServiceMonitor needs a Service to scrape — `IsMetricsServiceEnabled`,
[`api/v1/valkey_types.go`](../../api/v1/valkey_types.go)).

The label set is not the only bound: `BuildServiceMonitor` also pins
`namespaceSelector.matchNames` to the CR's own namespace, so a Service carrying the marker in
**another** namespace is out of reach whatever its labels say. The marker discriminates inside
the namespace; the namespace pin discriminates across them.

**D4 — The ServiceMonitor is `unstructured`, with no typed Prometheus-Operator dependency.**
`BuildServiceMonitor`
([`internal/builder/servicemonitor.go`](../../internal/builder/servicemonitor.go)) produces an
`unstructured.Unstructured` of `monitoring.coreos.com/v1`, gated behind
`spec.metrics.serviceMonitor.enabled` (default false) and **skipped gracefully when the CRD is
absent**. Importing the Prometheus-Operator Go types would couple the operator to their API
module and release cadence for a resource most clusters do not use. Same pattern as the
cert-manager `Certificate` ([ADR 0016](0016-authentication-and-tls-posture.md) D5), and the
expected pattern for any future optional third-party CRD.

**D5 — `reconcileMetrics` is create-or-cleanup**, wired through
`reconcileMonitoringResources`, so disabling the feature removes the resources rather than
orphaning them ([ADR 0004](0004-opt-in-poddisruptionbudgets.md) D6 has the same shape).

**D6 — The NetworkPolicy opens the exporter port only when metrics are enabled.** The policy
surface tracks the feature set rather than being permanently widened for a feature most
clusters do not enable.

**D7 — Enabling metrics migrates through the failover-aware rolling update, losslessly.** The
sidecar changes the pod-spec hash, so the existing machinery migrates the pods and **no
persistence is required**. Routing every pod-spec change through the same rolling update means
new features do not each need their own migration story
([ADR 0007](0007-failover-aware-rolling-update.md)).

**D8 — A parsed flag must be applied.** `bindOperatorFlags` declares
`--metrics-bind-address` (default `:8080`) but `managerOptions` builds `ctrl.Options` with no
`Metrics` field, so the value reaches nothing. The fix is
`Metrics: metricsserver.Options{BindAddress: f.metricsAddr}` plus a test that a **non-default**
value reaches the options struct — the default coincidence is exactly what hides the bug.

**D9 — Until then, treat the operator's metrics endpoint as public.** It serves `:8080`
(metrics) and `:8081` (health), both plain HTTP, no authentication filter, no `SecureServing`.
The unmovable half is the **metrics** address alone: there is **no supported way to move it or
turn it off**, because `--metrics-bind-address=0`, controller-runtime's documented way to
disable the metrics server, is silently ignored. The health address is wired —
`managerOptions` sets `HealthProbeBindAddress: f.probeAddr`
([`cmd/main.go`](../../cmd/main.go)) — so `--health-probe-bind-address`, `=0` included, does
what it says. Documenting the metrics endpoint as public prevents a network design built on a
setting that does nothing.

**D10 — Adding `FilterProvider: filters.WithAuthenticationAndAuthorization` is a separate,
deliberate trade, not free hardening.** It requires a `TokenReview`/`SubjectAccessReview` grant
on the ClusterRole ([ADR 0013](0013-operator-is-cluster-wide-privileged.md)).

## Consequences

* Every data pod carries an extra container and its resource requests, and — **only when auth
  is enabled** — the cluster password is present in the exporter container's environment, under
  `REDIS_PASSWORD` rather than the `VALKEY_PASSWORD` the other containers use
  ([ADR 0016](0016-authentication-and-tls-posture.md) D2).
* **A broken exporter is invisible to Kubernetes readiness** and must be detected through the
  absence of metrics instead (D2).
* Anything that creates an additional Service for the cluster **must not** carry the marker
  label unless it should be scraped — and the reverse holds too: a Service carrying the marker
  but not the three shared selector labels is not scraped either, and neither is one outside the
  CR's namespace, which the `namespaceSelector` excludes before any label is compared (D3).
* No compile-time checking of the ServiceMonitor field names (D4); correctness rests on tests.
* Enabling metrics changes the NetworkPolicy **as well as** the pod spec, and both must be
  reconciled together.
* A single standalone pod (`replicas: 1`, no persistence) has no failover target, so adding the
  sidecar restarts it and loses in-memory data. Physically unavoidable, not a design gap — and
  the guarantee in D7 holds only as long as new features change the pod spec rather than
  mutating pods in place.
* **Anything that can reach the operator pod reads its metrics, and no chart value changes
  that.** NetworkPolicy is the only available control today, and none is written for the
  operator namespace.

## Alternatives Considered

### A central exporter Deployment scraping all pods

Rejected: it would need network access to every data port and its own credentials, where the
sidecar needs neither.

### A readiness probe on the exporter's `/metrics`

Rejected: it couples data-plane availability to the monitoring sidecar.

### Select the ServiceMonitor on the shared `app.kubernetes.io/*` labels

Rejected: it matches the `-rw` and `-r` Services too, so Prometheus would scrape the data
ports.

### A typed dependency on the Prometheus-Operator API module

Rejected for dependency weight and version coupling, and because the operator must install and
run identically whether or not those CRDs are present.

### Always open port 9121 in the NetworkPolicy

Rejected as unnecessary exposure.

### Require persistence before enabling metrics

Rejected: unnecessary for multi-replica clusters, which migrate losslessly.

### Leave `--metrics-bind-address` unwired

The status quo, hidden by the default coincidence. Rejected: an operator configured to bind the
endpoint elsewhere, or to disable it, keeps listening on `:8080` regardless — **an operator
believed to be closed is open.**

### Wire the flag *and* the authentication filter in one change

Rejected as bundling: the filter adds RBAC surface and is a separate decision (D10). Wiring the
flag alone is the recommended minimum.

## Residual risks

* **`managerOptions` still has no `Metrics` field (open).** Verified by reading `cmd/main.go`
  and the controller-runtime default for the version pinned in `go.mod`
  (`sigs.k8s.io/controller-runtime v0.24.1`, `DefaultBindAddress = ":8080"`); **not reproduced
  against a cluster.**
* **The operator metrics endpoint is unauthenticated.** Risk named per case rather than as a
  blanket: the payload is standard controller-runtime and workqueue metrics — **no Secret
  material and no CR contents** — so this is exposure of operational metadata, not of
  credentials.
* The health endpoint (`:8081`) is likewise plain HTTP and unauthenticated; it serves
  `healthz.Ping` only.

## References

* [`internal/builder/statefulset.go`](../../internal/builder/statefulset.go) — `buildExporterContainer`, `buildPodContainers`
* [`internal/builder/service.go`](../../internal/builder/service.go) — `BuildMetricsService`, `MetricsServiceLabel`
* [`internal/builder/servicemonitor.go`](../../internal/builder/servicemonitor.go) — `BuildServiceMonitor`
* [`internal/builder/networkpolicy.go`](../../internal/builder/networkpolicy.go) — the exporter-port ingress rule
* [`internal/controller/valkey_controller.go`](../../internal/controller/valkey_controller.go) — `reconcileMetrics`, `reconcileMonitoringResources`
* [`cmd/main.go`](../../cmd/main.go) — `bindOperatorFlags`, `managerOptions`
* [ADR 0007](0007-failover-aware-rolling-update.md) — the migration path enabling metrics rides on
* [ADR 0013](0013-operator-is-cluster-wide-privileged.md) — the operator's exposure surface
* [ADR 0016](0016-authentication-and-tls-posture.md) — the `unstructured` third-party CRD pattern, and the password's reach
