---
id: T48
title: the operator metrics endpoint serves the fleet inventory without authentication
state: filed
severity: low         # disclosure of names and health, no write path; moving or disabling the endpoint, or a NetworkPolicy, bounds it today
security: hardening
threat: "would additionally cover any client that can route to the operator pod and reads :8080/metrics: today it gets, without credentials, the vko_valkey_* series that name every Valkey resource by namespace and name, with its health"
urgency: icebox       # rule 5: the fix reopens ADR 0018 D10, a recorded trade
effort: M             # filter, conditional grant, chart value, scrape configuration, tests
blocked-by: decision  # ADR 0018 D10, see Options
filed-from: T31, section "Further security measures — not in this change, each open" (archive/031)
opened: 2026-09-27
decided:              # not recorded - no decision yet
done:                 # not recorded - not done
---

Filed on 2026-09-27 from the first row of the table
["Further security measures — not in this change, each open"](archive/031-generated-pods-run-as-root.md#further-security-measures--not-in-this-change-each-open)
in the archived ticket 031, which is `done` and tracks none of its rows. The operator-facing
statement of the gap is [H-13](../security/operator-pod-posture.md#h-13).

## Fact

**Verified** (read 2026-09-27):

- The manager builds its metrics server from a bind address and nothing else:
  `Metrics: metricsserver.Options{BindAddress: f.metricsAddr}` ([`cmd/main.go`](../../cmd/main.go)
  line 105). A grep for `FilterProvider` and `WithAuthenticationAndAuthorization` over `cmd`,
  `internal`, `deploy` and `config` finds nothing.
- The chart binds it on `:8080` (`--metrics-bind-address=:8080`,
  [`deployment.yaml`](../../deploy/helm/valkey-operator/templates/deployment.yaml) line 37) and
  declares the container port (line 57) whether or not its metrics Service is rendered.
- No ClusterRole in `deploy` or `config` grants `tokenreviews` or `subjectaccessreviews` (same
  grep, no match), which the filter needs
  ([ADR 0018](../adr/0018-metrics-and-the-exporter-sidecar.md) D10).
- The payload names every Valkey resource by namespace and name
  ([ADR 0021](../adr/0021-per-resource-metrics-and-the-alert-that-was-missing.md)).

**Not verified:**

- Whether controller-runtime's filter, in the version `go.mod` pins, works on a plain-HTTP
  metrics server or needs `SecureServing: true`. That decides whether every scrape
  configuration has to move to HTTPS as well as to a bearer token.
- How the chart's optional ServiceMonitor would have to change (bearer token, TLS config).

## Impact

Live on every default install. Any pod, and anything else routable to the pod network, reads
the inventory; nothing can be written through the endpoint. Today an operator bounds it by
moving or disabling the endpoint (`--metrics-bind-address`, `=0` disables it,
[ADR 0018](../adr/0018-metrics-and-the-exporter-sidecar.md) D8) or by writing a NetworkPolicy
for the operator namespace; the chart ships none
([ticket 049](049-no-networkpolicy-guards-the-operator-namespace.md)).

## Options

The decision is whether to reopen [ADR 0018](../adr/0018-metrics-and-the-exporter-sidecar.md)
D10, which records the filter as "a separate, deliberate trade, not free hardening".

- **A — keep D9 and D10.** The endpoint stays unauthenticated wherever it binds, and the
  documentation keeps telling operators to treat it as public. Costs nothing; leaves the
  inventory to network controls alone.
- **B — the filter behind a chart value, default off (best).** A value (name to be decided)
  sets `FilterProvider` and renders a `create` rule on `tokenreviews` and
  `subjectaccessreviews` only while it is on, plus a ClusterRole a scraper can be bound to for
  `get` on the `/metrics` non-resource URL. Existing scrapers keep working until an operator
  opts in — the default-off the chart already applies to its metrics Service, ServiceMonitor
  and PrometheusRule. A conditional grant cannot come from a kubebuilder marker, because the
  generated role is unconditional; it lives in the chart only, which
  `TestHelmClusterRoleCoversGeneratedRole` permits (generated ⊆ chart,
  [ADR 0014](../adr/0014-rbac-lives-in-three-places.md)).
- **C — the filter on by default.** Closes the gap on every install and breaks every existing
  scrape configuration on the upgrade that ships it.

B is marked because it is the only option that closes the gap for whoever wants it without an
upgrade breaking anyone else's monitoring. Its cost is D10's own: while it is on, the operator
holds two more `create` verbs, widening the footprint of
[ADR 0013](../adr/0013-operator-is-cluster-wide-privileged.md).

## Decision

None yet.

## Verification

- A unit test in `cmd`, next to `TestManagerOptions_MetricsBindAddress`, pins that the filter
  is set when the option is on and absent when it is off.
- On Kind: an unauthenticated request to `:8080/metrics` is refused with the option on and
  answered with it off; a request with a token bound to the scraper ClusterRole is answered.
- Revert check: removing the `FilterProvider` line turns the first test red.

## History

- 2026-09-27 — filed from the row "Authenticated operator metrics endpoint (controller-runtime
  `WithAuthenticationAndAuthorization`)" of archive/031. Gap
  [H-13](../security/operator-pod-posture.md#h-13) states what is missing.
