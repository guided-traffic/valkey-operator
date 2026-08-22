# ADR 0021: Export Per-Resource State as Metrics, Because the Only Alertable Signal Could Not Name a Resource

## Status

Accepted. Date: 2026-08-21.

Implemented in the same change as this ADR: the collector in
[`internal/metrics/collector.go`](../../internal/metrics/collector.go), its registration in
[`cmd/main.go`](../../cmd/main.go), and three new chart templates —
[`service.yaml`](../../deploy/helm/valkey-operator/templates/service.yaml),
[`servicemonitor.yaml`](../../deploy/helm/valkey-operator/templates/servicemonitor.yaml) and
[`prometheusrule.yaml`](../../deploy/helm/valkey-operator/templates/prometheusrule.yaml), all
opt-in.

This ADR amends [ADR 0018](0018-metrics-and-the-exporter-sidecar.md) in place: its residual
risk described the endpoint payload as controller-runtime metrics carrying no CR contents,
which is no longer true.

**Not verified:** no alert has fired against a real Prometheus. The PromQL in the shipped rule
was reasoned against the metric definitions and against the incident below; it was not
evaluated by a Prometheus server, and no scrape of the endpoint was measured.

## Context

On a production cluster a `Valkey` resource sat in `status.phase: Error` for four months and
nothing reported it. The operator was doing everything right: the write it attempted was
rejected by the API server on every pass, it kept the pass alive
([ADR 0001](0001-continue-reconciling-past-a-rejected-write.md)), it recorded the failure on
the resource ([ADR 0002](0002-surface-a-blocked-reconcile-on-the-cr.md)), and it retried
forever. The failure was visible in `kubectl get valkey` the whole time. No human looked, and
nothing could have told them to.

Three findings, all read from this repository:

* **The only alertable signal cannot name a resource.** ADR 0001 D5 calls
  `controller_runtime_reconcile_errors_total` "the operator's only alertable signal", and it is
  right — a rejected write returns its error, so the counter does increment. Its label set is
  `{controller}` (controller-runtime `pkg/internal/controller/metrics`). It can say the Valkey
  controller is failing. It cannot say **which** of twelve resources, in which namespace, since
  when.

* **Nothing scraped it either.** The chart shipped eight templates and none of them was a
  Service, a ServiceMonitor, a PodMonitor or a PrometheusRule. `podAnnotations` defaulted to
  `{}`, so there were no `prometheus.io/scrape` annotations either. The endpoint has always
  listened — controller-runtime defaults `BindAddress` to `:8080` and the Deployment declares
  the container port — but nothing in the shipped artefacts pointed a scraper at it.

* **The state that would have caught it was already in `status`.** The stuck resource carried
  `phase: Error`, `operatorVersion` frozen at a release two minor versions behind the running
  operator, and a `Ready` condition whose `observedGeneration` was `1` while
  `metadata.generation` was `2`. The last of those is the sharpest statement a controller can
  make about itself — *I never finished evaluating this spec* — and it was sitting in the API
  with no way out to a monitoring system.

## Decision

**D1 — The operator exports one set of series per Valkey resource, labelled with namespace and
name.** The metric families are:

| Metric | Type | Labels beyond `namespace`, `name` | What it answers |
|---|---|---|---|
| `vko_valkey_status_phase` | gauge, always 1 | `phase` | which phase, right now |
| `vko_valkey_status_condition` | gauge, always 1 | `condition`, `status`, `reason` | which condition holds, and why |
| `vko_valkey_status_ready_replicas` | gauge | — | ready data pods last observed |
| `vko_valkey_spec_replicas` | gauge | — | data pods requested |
| `vko_valkey_metadata_generation` | gauge | — | the spec version the API server holds |
| `vko_valkey_status_observed_generation` | gauge | — | the newest generation any condition reports |
| `vko_valkey_operator_version_info` | gauge, always 1 | `version` | which operator last wrote status |
| `vko_operator_build_info` | gauge, always 1 | `version`, `commit` (no instance labels) | which operator is running |
| `vko_valkey_collector_success` | gauge | none | whether the last scrape could read at all |

The `vko_` prefix keeps them clear of controller-runtime's built-ins and of the
`valkey_observer_*` families the separate observer process exports on its own port
([ADR 0018](0018-metrics-and-the-exporter-sidecar.md)).

**D2 — The generation pair is the point, not an extra.** `vko_valkey_metadata_generation` minus
`vko_valkey_status_observed_generation` is a controller-independent statement that a spec was
accepted and never converged. It is the one expression that would have fired for the incident
on day one, before anyone knew what was wrong with it, and it stays true for any future failure
of the same shape.

`observed_generation` is the **maximum** across conditions, not the value of one designated
condition. Conditions are written by different code paths, and a blocked resource carries a
fresh `ReconcileBlocked` next to a `Ready` left over from the previous generation. The maximum
answers the question the metric is for — the newest spec the operator evaluated at all — while
any single condition would report a resource as stale merely because one condition was not
rewritten.

**D3 — The collector reads the cache at scrape time; it is not a set of gauges written during
reconcile.** Series are rebuilt from the manager's informer cache on every scrape, so a deleted
resource stops producing series with no deletion bookkeeping, and a changed phase leaves no
stale predecessor behind. A `GaugeVec` written from the reconciler would need a delete path for
every resource that disappears — including the ones that disappear while the operator is not
running — and getting that wrong keeps an alert firing for a cluster that no longer exists.

The read costs no API request: the manager's cache is already populated by the `For(&Valkey{})`
watch, and `valkeys get;list;watch` is already granted, so **this adds no RBAC**.

**D4 — A scrape that could not read publishes `vko_valkey_collector_success 0` and no resource
series at all.** Emitting an empty fleet would be read by every alert as "nothing is broken",
which is the worst possible answer from a collector that could not look. Every rule in the
shipped PrometheusRule is guarded on this series, so a failing collector makes the alerts stop
evaluating rather than silently clear, and two rules watch the guard itself
(`ValkeyMetricsCollectorFailing`, `ValkeyMetricsAbsent`).

**D5 — Condition series are deduplicated by type, and the collector is a checked collector.**
`status.conditions` is a plain list that only convention keeps unique; two entries of the same
type would emit two identical label sets and make the registry fail the **entire** gather,
taking every other resource's series down with it. The first entry per type wins. Announcing
every descriptor in `Describe` makes the registry catch a future `Collect` that emits something
undeclared.

**D6 — Staleness is decided against the running operator, not against the fleet majority.**
`vko_operator_build_info` publishes the running version, so
`vko_valkey_operator_version_info unless on (version) vko_operator_build_info` is an exact join:
any resource whose recorded version is not the running one. This is the only rule that catches a
resource the operator quietly stopped touching while its phase still reads `OK` — the earlier
formulation, "differs from what most of the fleet reports", was a guess that needed `topk` over
a `count by (version)` and would have been wrong during a rollout.

**D7 — Every chart value added here defaults to `false`, so a chart upgrade creates nothing.**
[ADR 0005](0005-upgrade-neutral-defaults-and-anti-affinity.md) D1 is scoped to CRD features and
their API-server-applied defaults, so it does not literally bind a chart-shipped Service. Its
purpose does: `helm upgrade` must not create an object the administrator did not ask for. The
chart's own default-on values (`serviceAccount.create`, `leaderElection.enabled`,
`preUpgradeHook.enabled`) are all things whose absence breaks the install or the upgrade path;
none of these three is.

**D8 — Requesting a ServiceMonitor renders the Service.** A ServiceMonitor selects Services, so
`serviceMonitor.enabled: true` with `service.enabled: false` would install cleanly and scrape
nothing. The template renders the Service for either flag, mirroring `IsMetricsServiceEnabled`
in [`api/v1/valkey_types.go`](../../api/v1/valkey_types.go), where a CR-level ServiceMonitor
forces its Service on the same way. The Service carries the marker label
`vko.gtrfc.com/metrics=true` and the ServiceMonitor selects on it, which is the same pattern the
per-CR metrics Service already uses.

**D9 — Every shipped alert aggregates the scrape-target labels away with `max by (...)`.** With
`replicaCount > 1` both operator pods serve identical per-resource series — the non-leader keeps
its cache warm — and without the aggregation each alert would fire once per pod.

**D10 — The thresholds are not chart values.** Seven rules with a duration each is seven knobs
that would need documenting, defaulting and testing, for a rule set that any operator with an
opinion will replace wholesale. The values comment says so, and `prometheusRule.enabled: false`
is the supported way to opt out.

## Consequences

* **The unauthenticated endpoint now discloses the fleet inventory.** Namespace, resource name,
  phase, condition reasons, replica counts and operator version, for every Valkey resource in
  the cluster. No Secret material, no spec contents — no password, no image, no host, no TLS
  material. The chart Service does not change reachability: the container port is declared with
  or without it and anything that can route to the operator pod already reads `:8080`. This is
  recorded as a residual risk here, in ADR 0018 and in
  [`SECURITY_ARCHITECTURE.md`](../../SECURITY_ARCHITECTURE.md), and the mitigations are the ones
  ADR 0018 already names: a NetworkPolicy for the operator namespace, moving the endpoint with
  `--metrics-bind-address`, or the D10 authentication filter.
* **Cardinality is bounded by resource count.** About eleven series per Valkey resource plus two
  global ones — roughly 130 series for a twelve-resource fleet. Nothing here is labelled with a
  pod name, an image tag or anything else unbounded.
* **A scrape now walks the whole resource list.** From the cache, in memory, on every scrape
  interval. For fleets this size that is not measurable; for a cluster with thousands of Valkey
  resources it would be worth revisiting.
* **The chart gained three templates and one values block**, all inert by default.
* **Two alerts fire about the monitoring rather than the fleet.** That is deliberate: without
  them, the guard in D4 would turn a broken collector into silence.

## Alternatives Considered

**Ship only the chart objects and alert on `controller_runtime_reconcile_errors_total`.**
Rejected. It needs no Go code, but the label set is `{controller}`: the alert can say the
controller is failing and never which resource, which is the exact gap that let the incident
run. Worse, on the release the fleet runs, the retry backoff is controller-runtime's default
(5 ms to 1000 s), so a permanently failing resource produces roughly one error every sixteen
minutes and a rate threshold would have to be written around that.

**Extend kube-state-metrics with a `CustomResourceStateMetrics` entry for `Valkey`.** Rejected
as the answer, kept as a legitimate deployment-side complement. It needs no operator code at all
and the cluster this incident happened on already runs kube-state-metrics configured that way
for Flux resources — so it would genuinely have caught this one. But it lives outside this
repository, every user would have to build it themselves from the CRD, and it cannot express
`vko_operator_build_info`, because the running operator's version is not in any resource.

**Write gauges from the reconciler.** Rejected per D3: it trades a scrape-time list for a
deletion-bookkeeping problem, and the failure mode of getting it wrong is an alert that never
clears.

**Put the phase in an info metric together with the name**
(`vko_valkey_info{phase=...}`). Rejected: the series identity would change on every phase
transition, so `for:` durations would reset and a resource flapping between two phases would
never hold an alert long enough to fire.

**Emit one series per phase and per condition status with 0/1 values**, as kube-state-metrics
does for pods. Rejected: it multiplies cardinality by the number of enum values for no gain
here, and the 0-valued series linger as a second thing to reason about. One series carrying the
current value as a label is enough, and it disappears when the value changes.

**Expose the alert thresholds as chart values.** Rejected per D10.

## Residual risks

* **No alert has been evaluated by a Prometheus.** The PromQL was written against the metric
  definitions and checked by hand against the incident; nothing in CI parses or evaluates it.
  A `promtool check rules` step over the rendered chart would close this and is not implemented.
* **The endpoint stays unauthenticated**, and now says more. See Consequences.
* **`vko_valkey_collector_success` is one global series, not one per informer.** A cache that is
  synced for Valkey resources but stale for something else still reports 1. The collector only
  reads Valkey resources, so this is accurate for what it claims — but it is not a general
  health signal for the operator.
* **The collector holds a `client.Reader`, not the manager.** If a future change scopes the
  manager cache to a namespace subset, the metrics silently narrow with it and no alert would
  report the resources that vanished from the export. Nothing guards against that today.
* **`ValkeyPhaseNotOK` fires on a legitimately long rolling update.** Thirty minutes is generous
  for the fleet this was written for; a large cluster with slow replication could exceed it and
  page for something that is working.
* **The `for: 1h` on `ValkeyOperatorVersionStale` is a guess.** It has to outlast a rolling
  operator upgrade across the fleet, which nothing here measures.

## References

* [`internal/metrics/collector.go`](../../internal/metrics/collector.go) — the collector, the
  metric descriptors and `Register`.
* [`internal/metrics/collector_test.go`](../../internal/metrics/collector_test.go) — including
  the blocked-resource fixture that mirrors the incident.
* [`cmd/main.go`](../../cmd/main.go) — registration against `mgr.GetCache()`.
* [`deploy/helm/valkey-operator/templates/`](../../deploy/helm/valkey-operator/templates/) —
  `service.yaml`, `servicemonitor.yaml`, `prometheusrule.yaml`.
* [ADR 0018](0018-metrics-and-the-exporter-sidecar.md) — the exporter sidecar and the endpoint
  decisions this ADR amends.
* [ADR 0001](0001-continue-reconciling-past-a-rejected-write.md) D5 — the sentence that named
  `controller_runtime_reconcile_errors_total` as the only alertable signal.
* [ADR 0002](0002-surface-a-blocked-reconcile-on-the-cr.md) — the `ReconcileBlocked` condition
  the `vko_valkey_status_condition` series exports.
* [ADR 0005](0005-upgrade-neutral-defaults-and-anti-affinity.md) D1 — the default-off rule D7
  reasons from.
