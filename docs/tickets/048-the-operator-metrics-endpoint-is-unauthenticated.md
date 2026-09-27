---
id: T48
title: the operator metrics endpoint serves the fleet inventory without authentication
state: filed
severity: low         # disclosure of names and health, no write path; ~~moving or disabling the endpoint, or~~ a NetworkPolicy bounds it today, or disabling the endpoint outside the chart (corrected 2026-09-27: the chart cannot move or disable it)
security: hardening
threat: "would additionally cover any client that can route to the operator pod and reads :8080/metrics: today it gets, without credentials, the vko_valkey_* series that name every Valkey resource by namespace and name, with its health"
urgency: later        # rule 4 since 2026-09-27: the H-13 correction landed; decision 1 option 1B (a chart bind-address value) is a cheap known fix; the filter alone would be icebox (rule 5, reopens ADR 0018 D10)
effort: M             # filter, conditional grant, chart value, scrape configuration, tests; the H-13 correction alone is XS, the chart value XS-S
blocked-by: decision  # decision 1 (chart value) and ADR 0018 D10 (decision 2), see Options; the H-13 correction is not blocked
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

*Added 2026-09-27 (enrichment, at `4a7543e`):*

- **The chart has no value for the address.** The flag is registered with default `:8080`
  ([`cmd/main.go`](../../cmd/main.go) line 69). The chart's `args`
  ([`deployment.yaml`](../../deploy/helm/valkey-operator/templates/deployment.yaml) lines 36–46)
  pass it as a literal and take no extra arguments, and
  [`values.yaml`](../../deploy/helm/valkey-operator/values.yaml) has no key for it: its
  `metrics:` block (lines 112–145) configures only the Service, the ServiceMonitor and the
  PrometheusRule. The kustomize manifest under `config/manager` passes `--leader-elect` only,
  so it serves the flag default `:8080` as well.
- **controller-runtime v0.25.1** ([`go.mod`](../../go.mod) line 16) **applies a filter on plain
  HTTP too.** Read in the module cache: `pkg/metrics/server/server.go` builds the filter from
  `FilterProvider` at lines 133–137 and wraps the `/metrics` handler at lines 224–231 whatever
  `SecureServing` says, and `createListener` (line 274) branches only on TLS. Without
  `SecureServing` a scraper's bearer token therefore travels in clear. With it and no
  certificate in `CertDir`, the server falls back to a self-signed certificate (lines 311–325).
- **The filter adds a module.** `pkg/metrics/filters/filters.go` imports `k8s.io/apiserver`
  (lines 27–30) and says so in its own doc comment (line 50). Neither [`go.mod`](../../go.mod) nor
  `go.sum` has an entry for `k8s.io/apiserver` today.
- **A conditional grant cannot live in `clusterrole.yaml`'s rules block.** `readRulesBlock`
  fails the drift test on any `{{` inside that block
  ([`rbac_drift_test.go`](../../internal/controller/rbac_drift_test.go) lines 112–114). A rule
  rendered only while an option is on needs a template of its own.
- The chart's ServiceMonitor scrapes `port: metrics`, `path: /metrics` with no scheme, TLS or
  authorization field
  ([`servicemonitor.yaml`](../../deploy/helm/valkey-operator/templates/servicemonitor.yaml)
  lines 26–32).

**Not verified:**

- ~~Whether controller-runtime's filter, in the version `go.mod` pins, works on a plain-HTTP
  metrics server or needs `SecureServing: true`. That decides whether every scrape
  configuration has to move to HTTPS as well as to a bearer token.~~ *(answered 2026-09-27 by
  reading the module source, above: it works on plain HTTP. `SecureServing` decides only
  whether the token travels in clear. Nothing was run.)*
- How the chart's optional ServiceMonitor would have to change (bearer token, TLS config).
  *(2026-09-27: today it carries none of these fields, above. How a Prometheus handles the
  self-signed fallback certificate was not tried.)*
- *(added 2026-09-27)* What the next `helm upgrade` does to a Deployment argument an operator
  changed outside the chart.

### Appendix 2026-09-27: H-13 promises a chart setting that does not exist

**Verified** (found by the triage, confirmed by the orchestrator and re-read for this entry,
2026-09-27):

- [`operator-pod-posture.md`](../security/operator-pod-posture.md) lines 87–89 (gap H-13):
  "`--metrics-bind-address` is applied since the ADR 0018 D8 fix, so the endpoint can be moved
  or switched off (`=0`) from the chart". **This ~~is~~ *(was, until work list item 1 on
  2026-09-27; "from the chart" is struck and corrected in place, History)* false.** `deployment.yaml` line 37 hard-codes
  `--metrics-bind-address=:8080`, and `values.yaml` has no key for it (above). The chart
  hard-coded the flag when the sentence was written as well: it was line 40 of `deployment.yaml`
  in `3c78c33` (2026-08-21), the commit that added the sentence to the former
  `SECURITY_ARCHITECTURE.md`. `4a7543e` then moved it verbatim into this page.
- The following are **misleading, not false**, because they name the manager flag and do not
  claim a chart value. [`installation.md`](../operations/installation.md) documents Helm as the
  install path, so for a chart user each of them describes a setting the chart does not offer:
  - [`monitoring.md`](../operations/monitoring.md) lines 83–84: "move it with
    `--metrics-bind-address`, or switch it off with `--metrics-bind-address=0`".
  - [`values.yaml`](../../deploy/helm/valkey-operator/values.yaml) lines 110–111: "or bind the
    endpoint elsewhere".
  - [ADR 0021](../adr/0021-per-resource-metrics-and-the-alert-that-was-missing.md) lines
    155–157: "moving the endpoint with `--metrics-bind-address`".
- `values.yaml` line 94 ("set on the Deployment; =0 switches it off") and
  [ADR 0018](../adr/0018-metrics-and-the-exporter-sidecar.md) D8/D9 are true as written.

**Not verified:** Only `=0`, or an address that other pods cannot route to, narrows who
reaches the endpoint. A different port does not. This is general networking, not measured
here.

## Impact

Live on every default install. Any pod, and anything else routable to the pod network, reads
the inventory; nothing can be written through the endpoint. Today an operator bounds it ~~by
moving or disabling the endpoint (`--metrics-bind-address`, `=0` disables it,
[ADR 0018](../adr/0018-metrics-and-the-exporter-sidecar.md) D8) or~~ by writing a NetworkPolicy
for the operator namespace; the chart ships none
([ticket 049](049-no-networkpolicy-guards-the-operator-namespace.md)). *(corrected 2026-09-27:
on a chart install the endpoint can be disabled with `=0` only by changing the Deployment's
arguments outside the chart, see the appendix. Moving it to another port does not bound who
reaches it.)*

*(added 2026-09-27)* H-13 ~~sends~~ *(sent, until work list item 1 on 2026-09-27)* an operator
who wants to close the endpoint to a chart setting that does not exist. That is the false statement of the appendix (rule 1), and its
correction is the XS slice in the work list.

## Options

The H-13 correction needs no decision (work list). Two decisions are open. **Take decision 1
first**: it is small and independent, and it touches the same `deployment.yaml` and
`values.yaml` lines as [ticket 049](049-no-networkpolicy-guards-the-operator-namespace.md).
**Take decision 2 after ticket 049's policy is decided**, because that policy closes the same
read path more cheaply.

### Decision 1: a chart value for the bind address

- **1A — no value.** The XS correction states that the chart cannot move or disable the
  endpoint, and operators change the Deployment outside the chart. Costs nothing. The
  mitigation ADR 0018 D9 names ("unless it is moved or disabled") then stays out of reach on
  the chart, which [`rbac_drift_test.go`](../../internal/controller/rbac_drift_test.go) line 7
  calls the canonical install path. That repeats, one layer up, the defect ADR 0018 D8 fixed in
  the binary.
- **1B — `metrics.bindAddress`, default `":8080"` (name to be decided) (recommended).**
  - The default renders the same `args` as today, so upgrading changes nothing
    ([ADR 0005](../adr/0005-upgrade-neutral-defaults-and-anti-affinity.md) D1).
  - `"0"` renders `=0` and drops the `metrics` container port (deployment.yaml lines 56–58).
    With `metrics.service.enabled` or `metrics.serviceMonitor.enabled` on, it fails the render,
    because a Service with no target port scrapes nothing. That is the same reasoning as the
    header of [`service.yaml`](../../deploy/helm/valkey-operator/templates/service.yaml),
    lines 1–8.
  - Any other value must end in `:<port>`, and the container port follows it.
  - Cost XS–S: deployment.yaml line 37 and lines 56–58, `values.yaml`, one row in the README
    [Helm chart values](../../README.md#helm-chart-values) table (README lines 569–577), and
    the H-13 and monitoring.md text. It is verified by hand with `helm template`, because no CI
    gate renders the chart today ([ticket 058](058-no-ci-gate-renders-the-chart.md)).
- **1C — a generic `extraArgs` list.** The most flexible option. The flags are `fs.StringVar`
  ([`cmd/main.go`](../../cmd/main.go) lines 69–84), and Go's `flag` package keeps the last value
  of a flag given twice (standard-library behaviour, not run here). An `extraArgs` entry would
  therefore silently override a value the chart validated at render time, such as
  `--allowed-seccomp-localhost-profiles` (`_helpers.tpl` lines 136–143, gap H-15). After that,
  every flag is chart surface that no check covers.

1B is marked because it makes ADR 0018 D9's documented mitigation reachable on the chart at
XS–S cost, and its default renders the same Deployment. 1C loses on the silent override.

### Decision 2: reopen ADR 0018 D10, the filter

The decision is whether to reopen [ADR 0018](../adr/0018-metrics-and-the-exporter-sidecar.md)
D10, which records the filter as "a separate, deliberate trade, not free hardening".

- **A — keep D9 and D10 (recommended, until ticket 049 has shipped or been decided).** The
  endpoint stays unauthenticated wherever it binds, and the documentation keeps telling
  operators to treat it as public. Costs nothing; leaves the inventory to network controls
  alone.
- **B — the filter behind a chart value, default off.** ~~(best)~~ A value (name to be decided)
  sets `FilterProvider` and renders a `create` rule on `tokenreviews` and
  `subjectaccessreviews` only while it is on, plus a ClusterRole a scraper can be bound to for
  `get` on the `/metrics` non-resource URL. Existing scrapers keep working until an operator
  opts in — the default-off the chart already applies to its metrics Service, ServiceMonitor
  and PrometheusRule. A conditional grant cannot come from a kubebuilder marker, because the
  generated role is unconditional; it lives in the chart only, which
  `TestHelmClusterRoleCoversGeneratedRole` permits (generated ⊆ chart,
  [ADR 0014](../adr/0014-rbac-lives-in-three-places.md)) ~~.~~ *(corrected 2026-09-27: only in a
  template of its own. A `{{ if }}` inside `clusterrole.yaml`'s rules block turns that test red,
  `rbac_drift_test.go` lines 112–114.)* *(added 2026-09-27)* It also adds the
  `k8s.io/apiserver` module. Without `SecureServing` it sends the scraper's token in clear, so
  in practice it needs `SecureServing` as well. That means a self-signed or issued certificate,
  and a ServiceMonitor with `scheme: https`, a TLS config and an authorization field.
- **C — the filter on by default.** Closes the gap on every install and breaks every existing
  scrape configuration on the upgrade that ships it.

~~B is marked because it is the only option that closes the gap for whoever wants it without an
upgrade breaking anyone else's monitoring. Its cost is D10's own: while it is on, the operator
holds two more `create` verbs, widening the footprint of
[ADR 0013](../adr/0013-operator-is-cluster-wide-privileged.md).~~ *(superseded as the
recommendation 2026-09-27)*

A is marked for now. Ticket 049's option A closes the same read path at S cost. It adds no
module, needs no scraper migration and no new `create` verbs on the operator's ClusterRole
([ADR 0013](../adr/0013-operator-is-cluster-wide-privileged.md)). B's realistic shape is
larger than filed: a new module, `SecureServing`, and a TLS scrape. Revisit B when an
installation needs the gap closed on a CNI that does not enforce NetworkPolicy, where ticket
049 cannot help. If B is taken, take it with `SecureServing` on.

## Work list

**Not waiting on a decision (XS, can be done today):**

1. **H-13 correction (rule 1).** Strike "from the chart" in
   [`operator-pod-posture.md`](../security/operator-pod-posture.md) lines 88–89 and state what
   holds: the chart passes `:8080` unconditionally and has no value for it. In the same change,
   optionally: qualify [`monitoring.md`](../operations/monitoring.md) lines 83–84 and the
   `values.yaml` comment lines 110–111, both misleading rather than false (a comment only, no
   render change). ADR 0021 lines 155–157 stay, because they name the flag. Doing this does not
   close the ticket. **Done 2026-09-27** for H-13; the optional `monitoring.md` and
   `values.yaml` qualifications were not taken and stay open as misleading, not false.

**Waiting on decision 1:** the value, the `deployment.yaml` wiring, the README row, a
`helm template` check (default render identical to HEAD; `"0"` renders `=0` and no `metrics`
port; `"0"` with `serviceMonitor.enabled` fails), and rewriting H-13 and monitoring.md around
the value.

**Waiting on decision 2 (after ticket 049):** add `k8s.io/apiserver`; add a flag that sets
`FilterProvider`, and `SecureServing`; add a unit test next to
`TestManagerOptions_MetricsBindAddress` ([`cmd/main_test.go`](../../cmd/main_test.go) line 295);
add a separate template with the conditional `tokenreviews`/`subjectaccessreviews` rule and the
scraper ClusterRole; move the ServiceMonitor to https with a bearer token; run the Kind check
below.

**Close (ADR 0034):** amend ADR 0018 (D10, and D9 if 1B lands). If B lands, also the ADR 0013
footprint and [the operator ClusterRole table](../security/privilege-footprint.md#the-operator-clusterrole).
Then the README values rows, monitoring.md and H-13. `git grep -n 'T48\|048-the-operator'`
outside `docs/tickets/` (none today), then move to `archive/`.

## Decision

None yet.

## Verification

- *(added 2026-09-27, XS slice)* `grep -n "from the chart" docs/security/operator-pod-posture.md`
  no longer finds the claim unstruck. *(Run 2026-09-27 after the fix: one hit, `:89`, the
  struck text and its correction. Done.)*
- *(added 2026-09-27, decision 1)* `helm template` with default values is byte-identical to the
  render at HEAD. `metrics.bindAddress=0` renders `--metrics-bind-address=0` and no `metrics`
  container port. `0` together with `metrics.serviceMonitor.enabled=true` fails the render.
- A unit test in `cmd`, next to `TestManagerOptions_MetricsBindAddress`, pins that the filter
  is set when the option is on and absent when it is off.
- On Kind: an unauthenticated request to `:8080/metrics` is refused with the option on and
  answered with it off; a request with a token bound to the scraper ClusterRole is answered.
- Revert check: removing the `FilterProvider` line turns the first test red.

## History

- 2026-09-27: urgency `now` -> `later` (rule 4, top-down): the H-13 correction, the only rule-1 statement, landed, and decision 1's option 1B is a cheap known fix. Applied as the History entry below derived it.
- 2026-09-27 — work list item 1 (the H-13 correction) landed, one file (read in `git diff` of
  the working tree): [`operator-pod-posture.md`](../security/operator-pod-posture.md) H-13,
  "from the chart" struck and corrected in place - the flag exists on the binary
  (`cmd/main.go:69`), `deployment.yaml:37` passes `--metrics-bind-address=:8080`
  unconditionally, `values.yaml` has no value for it, so on a chart install moving or disabling
  the endpoint means changing the Deployment's arguments outside the chart. `monitoring.md`
  and the `values.yaml` comment were not touched (the optional half of the item). Decisions 1
  and 2 are untouched. **Urgency not recomputed in this pass** (the orchestrating run left every
  urgency but one to the owner): the frontmatter names rule 1 for the H-13 slice only, and with
  it landed no false statement is left, so a top-down re-derivation gives `later` (rule 4:
  decision 1's option 1B is a cheap known fix) rather than the `icebox` the comment names for
  the filter alone. **Not verified:** nothing was rendered or run.
- 2026-09-27 — enriched - appended the H-13 false statement (XS slice, rule 1); answered the
  plain-HTTP question from controller-runtime v0.25.1; added the `k8s.io/apiserver`, drift-guard
  and ServiceMonitor facts; split Options into decision 1 (chart value, 1B recommended) and
  decision 2 (filter; recommendation moved from B to A until ticket 049); added a work list.
- 2026-09-27 — urgency `icebox` → `now`: rule 1 now matches first, because H-13 states a chart
  setting that does not exist (the appendix). The filter part alone would still be `icebox`
  (rule 5). Severity comment corrected in place (the chart cannot move or disable the endpoint).
- 2026-09-27 — filed from the row "Authenticated operator metrics endpoint (controller-runtime
  `WithAuthenticationAndAuthorization`)" of archive/031. Gap
  [H-13](../security/operator-pod-posture.md#h-13) states what is missing.
