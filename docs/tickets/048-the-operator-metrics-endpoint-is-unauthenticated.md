---
id: T48
title: the operator metrics endpoint serves the fleet inventory without authentication
state: analysed       # was filed; 2026-09-27 at 84a39c2: every load-bearing fact verified at HEAD or at a pinned upstream source, both decisions carry a coherent option set; what stays unverified (Helm 4, the production CNI) changes neither recommendation
severity: low         # disclosure of names and health, no write path; ~~moving or disabling the endpoint, or~~ a NetworkPolicy bounds it today, or disabling the endpoint outside the chart (corrected 2026-09-27: the chart cannot move or disable it) (qualified 2026-09-27 at 84a39c2: the NetworkPolicy only where the CNI enforces one; outside the chart only a Helm post-renderer patch setting =0 lasts, unchecked by the chart, because a hand edit of the Deployment args is reverted by the next helm upgrade)
security: hardening
threat: "would additionally cover any client that can route to the operator pod and reads :8080/metrics: today it gets, without credentials, the vko_valkey_* series that name every Valkey resource by namespace and name, with its health"
urgency: later        # rule 4, re-derived top-down 2026-09-27 at 84a39c2: rule 1 does not match (the one false tracked statement, H-13 "from the chart", is fixed in bcc63c9; the remaining "move" wording is imprecise, not measured-false), rules 2 and 3 do not match (no release gate, severity low), rule 4 matches (decision 1's off switch and the wording fixes are cheap known fixes); the filter alone would be icebox (rule 5, reopens ADR 0018 D10)
effort: S             # was M; 2026-09-27 at 84a39c2: with the recommended options (decision 1 off switch, decision 2 A) the work is a chart value, doc and ADR amendments and hand-run helm template checks; M to L if decision 2 takes B (module, SecureServing, certificate, two templates, ServiceMonitor credential, Kind check)
blocked-by: decision  # decision 1 (chart value) and decision 2 (ADR 0018 D10), see Options; the wording fixes in the work list are not blocked
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

**Verified** (read 2026-09-27, re-read at `84a39c2`):

- The manager builds its metrics server from a bind address and nothing else:
  `Metrics: metricsserver.Options{BindAddress: f.metricsAddr}` ([`cmd/main.go`](../../cmd/main.go)
  line 105). A grep for `FilterProvider` and `WithAuthenticationAndAuthorization` over `cmd`,
  `internal`, `deploy` and `config` finds nothing. *(Re-run 2026-09-27 at `84a39c2`, with
  `SecureServing` added and `api` and `test` included:
  `grep -rn 'FilterProvider\|WithAuthenticationAndAuthorization\|SecureServing' cmd internal deploy config api test`
  exits 1.)*
- The chart binds it on `:8080` (`--metrics-bind-address=:8080`,
  [`deployment.yaml`](../../deploy/helm/valkey-operator/templates/deployment.yaml) line 37) and
  declares the container port (line 57) whether or not its metrics Service is rendered.
- No ClusterRole in `deploy` or `config` grants `tokenreviews` or `subjectaccessreviews` (same
  grep, no match), which the filter needs
  ([ADR 0018](../adr/0018-metrics-and-the-exporter-sidecar.md) D10). *(Re-run at `84a39c2`:
  `grep -rn 'tokenreviews\|subjectaccessreviews' cmd internal deploy config` exits 1. The filter's
  own doc comment names both `create` rules for the operator and `get` on the non-resource URL
  `/metrics` for the scraper, `pkg/metrics/filters/filters.go` lines 41–48 in controller-runtime
  v0.25.1.)*
- The payload names every Valkey resource by namespace and name
  ([ADR 0021](../adr/0021-per-resource-metrics-and-the-alert-that-was-missing.md)). *(Re-read at
  `84a39c2`: the label names `namespace` and `name` are defined at
  [`collector.go`](../../internal/metrics/collector.go) lines 50–51 and carried by every
  per-resource descriptor, lines 95–120; the collector lists every Valkey resource from the
  manager cache with no namespace option.)*

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
  ([`rbac_drift_test.go`](../../internal/controller/rbac_drift_test.go) lines 113–115). A rule
  rendered only while an option is on needs a template of its own.
- The chart's ServiceMonitor scrapes `port: metrics`, `path: /metrics` with no scheme, TLS or
  authorization field
  ([`servicemonitor.yaml`](../../deploy/helm/valkey-operator/templates/servicemonitor.yaml)
  lines 26–32).

*Added 2026-09-27 (re-verification at `84a39c2`):*

- **Only `=0` or a loopback bind narrows who reaches the endpoint; a different port narrows
  nothing.** The Kubernetes API contract says so for container ports: "Not specifying a port
  here DOES NOT prevent that port from being exposed. Any port which is listening on the default
  "0.0.0.0" address inside a container will be accessible from the network" (`k8s.io/api`
  v0.37.1 `core/v1/types.go` lines 3163–3165, module cache). controller-runtime creates no server
  at all for `0` (`server.go` lines 120–121). A loopback bind is still reachable through
  `kubectl port-forward`, which needs `create` on `pods/portforward`: containerd v2.1.4 dials
  `localhost:<port>` inside the pod's network namespace
  (`internal/cri/server/sandbox_portforward_linux.go` line 73, tcp6 fallback at line 76, fetched
  from raw.githubusercontent.com). Read, not measured on a cluster; CRI-O was not checked.
- **A hand edit of the Deployment `args` is reverted by the next `helm upgrade`.** Three
  independent reasons, each read, none measured. (1) Helm v3.19.0 builds the upgrade patch as a
  three-way strategic merge of old manifest, new manifest and live object with `overwrite` true
  (`pkg/kube/client.go` line 699, in `createPatch` from line 640). (2) `Container.Args` is
  `+listType=atomic` (`k8s.io/api` v0.37.1 `core/v1/types.go` line 3154), so a differing list is
  replaced as a whole. (3) The chart's `args` change on every release anyway, because
  `--operator-image` ([`deployment.yaml`](../../deploy/helm/valkey-operator/templates/deployment.yaml)
  line 39) renders the image tag, which defaults to `.Chart.AppVersion`
  ([`_helpers.tpl`](../../deploy/helm/valkey-operator/templates/_helpers.tpl) line 69). A Flux
  HelmRelease with `spec.driftDetection.mode: enabled` "will attempt to correct the drift" even
  between upgrades (https://fluxcd.io/flux/components/helm/helmreleases/).
- **The one durable out-of-chart route is a post-renderer.** `helm upgrade --post-renderer`, or a
  Flux HelmRelease `spec.postRenderers` kustomize patch, whose patches "are applied in the order
  given, and persisted by Helm to the manifest" (same Flux page). Such a patch addresses the
  positional literal `args` (lines 36–46) by index, keeps the `metrics` container port, and is
  invisible to every render refusal of the chart: a post-rendered `=0` next to
  `metrics.serviceMonitor.enabled` renders cleanly and scrapes nothing.
- **With the endpoint off or loopback-bound, the chart's own alert fires forever.**
  `ValkeyMetricsAbsent` is `absent(vko_valkey_collector_success)`
  ([`prometheusrule.yaml`](../../deploy/helm/valkey-operator/templates/prometheusrule.yaml)
  lines 161–162), and the header of that template guards every other rule on the same series.
  The chart Service is rendered when `metrics.service.enabled` or `metrics.serviceMonitor.enabled`
  is on ([`service.yaml`](../../deploy/helm/valkey-operator/templates/service.yaml) line 1) and
  targets the named port `metrics` (line 27).
- **The drift guard's block boundary.** `readRulesBlock` ends the rules block at the first line
  that is not blank and does not start with a space, `-` or `#`
  ([`rbac_drift_test.go`](../../internal/controller/rbac_drift_test.go) lines 104–111). An
  indented `{{` trips the guard (lines 113–115). A column-0 `{{` ends the block instead, so every
  rule after it leaves the comparison: the test, which checks generated ⊆ chart only
  (lines 160–191), goes red with a misleading "missing triple" when the generated role needs
  one of those rules, and says nothing when the rule exists in the chart alone. A separate
  template is therefore the right shape for any conditional grant.
- **The filter's operator grant needs no new rule.** The built-in ClusterRole
  `system:auth-delegator` is exactly `create` on `tokenreviews` and `subjectaccessreviews`
  (kubernetes v1.37.1 `plugin/pkg/auth/authorizer/rbac/bootstrappolicy/policy.go` lines 475–480,
  fetched from raw.githubusercontent.com). A conditional ClusterRoleBinding to it, in a template
  of its own, grants what the filter needs without touching `clusterrole.yaml`. It still widens
  the operator's footprint by those two verbs, and `TestHelmClusterRoleCoversGeneratedRole` reads
  only `templates/clusterrole.yaml` (`chartRolePath`, `rbac_drift_test.go` line 35), so no test
  would notice a stale footprint entry for it.
- **`SecureServing` details that decide option B's shape** (controller-runtime v0.25.1
  `server.go`, module cache): the fallback certificate is self-signed for `localhost` and
  `127.0.0.1` only (line 316) and generated in memory, so `readOnlyRootFilesystem: true`
  ([`_helpers.tpl`](../../deploy/helm/valkey-operator/templates/_helpers.tpl) line 125) does not
  block it, while a scraper dialing the pod IP must skip verification; an issued certificate is
  read from `CertDir`, default `os.TempDir()/k8s-metrics-server/serving-certs` (lines 169–170),
  so it needs a Secret volume; the TLS config advertises only ALPN `h2` (lines 278–280), which
  keeps HTTP/2 on. The filter caches authentication for 1 min, allowed decisions for 5 min and
  denials for 30 s, and answers 401 to an unauthenticated request (`filters.go` lines 64, 83–84,
  109–112).
- **`k8s.io/apiserver` would ride the existing Renovate group.** `k8s-go-modules` matches
  `/^k8s.io//` ([`renovate.json`](../../renovate.json) lines 256–261).
- **prometheus-operator's credential fields.** `bearerTokenSecret` is "Deprecated: use
  authorization instead", and the `arbitraryFSAccessThroughSMs` documentation warns that with a
  `bearerTokenFile` "a malicious target can get access to the Prometheus service account's token"
  (https://prometheus-operator.dev/docs/api-reference/api/). *(Review 2026-09-27: both fields
  carry "Deprecated: use `authorization` instead." in the prometheus-operator source,
  `pkg/apis/monitoring/v1/types.go` line 673 for `bearerTokenFile` and `http_config.go` line 101
  for `bearerTokenSecret`, read on the `main` branch, a moving ref; the version a production
  Prometheus runs was not checked.)*

**Not verified:**

- ~~Whether controller-runtime's filter, in the version `go.mod` pins, works on a plain-HTTP
  metrics server or needs `SecureServing: true`. That decides whether every scrape
  configuration has to move to HTTPS as well as to a bearer token.~~ *(answered 2026-09-27 by
  reading the module source, above: it works on plain HTTP. `SecureServing` decides only
  whether the token travels in clear. Nothing was run.)*
- ~~How the chart's optional ServiceMonitor would have to change (bearer token, TLS config).
  *(2026-09-27: today it carries none of these fields, above. How a Prometheus handles the
  self-signed fallback certificate was not tried.)*~~ *(answered in part 2026-09-27 at 84a39c2: the
  required fields are known by reading, above: `scheme: https`, a `tlsConfig` with
  `insecureSkipVerify` or a CA, and an `authorization` credential from a Secret. Still not
  tried: a Prometheus scrape against the self-signed certificate, and against a server that
  offers only ALPN `h2`. A Kind run with option B's shape would settle both.)*
- ~~*(added 2026-09-27)* What the next `helm upgrade` does to a Deployment argument an operator
  changed outside the chart.~~ *(answered in part 2026-09-27 at 84a39c2: for Helm 3 by reading,
  above: it reverts the edit. Still not verified: Helm 4 server-side apply, which CI
  pins at `v4.3.0` ([`release.yml`](../../.github/workflows/release.yml) line 316), and Helm 4's
  post-renderer interface. A `helm upgrade` after `kubectl edit` on Kind would measure it.)*
- *(added 2026-09-27 at 84a39c2)* Whether the production CNI enforces NetworkPolicy, whether the
  operator itself is installed through a Flux HelmRelease, and whether that HelmRelease enables
  drift detection. No cluster access was allowed; `kubectl get helmrelease -A` and a
  NetworkPolicy deny test would settle them.
- *(added 2026-09-27 at 84a39c2)* Whether the go1.27 `net/http` HTTP/2 mitigations make an
  explicit HTTP/2 opt-out moot for option B. The kubebuilder scaffold (master branch
  `testdata/project-v4/cmd/main.go`, read by the review with curl; master is a moving branch)
  disables HTTP/2 by default through `TLSOpts`, citing GHSA-qppj-fm5r-hxr3 and
  GHSA-4374-p667-p6c8, and defaults to a disabled bind with `SecureServing` and the filter on.
  That is a greenfield default, not evidence about a running fleet.

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
  `SECURITY_ARCHITECTURE.md`. `4a7543e` then moved it verbatim into this page. *(2026-09-27 at
  84a39c2: the correction is committed in `bcc63c9`; `grep -n 'from the chart'
  docs/security/operator-pod-posture.md` gives one hit, `:89`, the struck text. Its corrected
  sentence says moving or disabling "means changing the Deployment's arguments outside the
  chart", which omits that the next helm upgrade reverts such an edit (Fact) - work list.)*
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
- *(added 2026-09-27 at 84a39c2)* **A second imprecision in the same places: a move is presented
  as a restriction.** A move narrows reach only when it binds loopback; a different port narrows
  nothing (Fact). That affects H-13's heading ("unless moved or disabled",
  `operator-pod-posture.md` line 81) and body (lines 87–94), ADR 0018 D9's heading ("public unless
  it is moved or disabled", line 116), ADR 0021 lines 155–157, `monitoring.md` lines 83–84 and
  `values.yaml` lines 110–111. ADR 0018's Consequences bullet "no chart value changes that"
  (lines 148–150) is true today and becomes false once decision 1 adds a value.

~~**Not verified:** Only `=0`, or an address that other pods cannot route to, narrows who
reaches the endpoint. A different port does not. This is general networking, not measured
here.~~ *(corrected 2026-09-27 at 84a39c2: verified from the Kubernetes API contract, with the
port-forward qualification for a loopback bind, see Fact. Not measured on a cluster.)*

## Impact

Live on every default install. Any pod, and anything else routable to the pod network, reads
the inventory; nothing can be written through the endpoint. Today an operator bounds it ~~by
moving or disabling the endpoint (`--metrics-bind-address`, `=0` disables it,
[ADR 0018](../adr/0018-metrics-and-the-exporter-sidecar.md) D8) or~~ by writing a NetworkPolicy
for the operator namespace; the chart ships none
([ticket 049](049-no-networkpolicy-guards-the-operator-namespace.md)). *(corrected 2026-09-27:
on a chart install the endpoint can be disabled with `=0` only by changing the Deployment's
arguments outside the chart, see the appendix. Moving it to another port does not bound who
reaches it.)* *(added 2026-09-27 at 84a39c2: the NetworkPolicy bounds it only where the CNI
enforces NetworkPolicy, unverified for production. Of the ways to change the arguments outside
the chart, only a Helm post-renderer patch survives: a hand edit of the Deployment `args` is
reverted by the next `helm upgrade` (Fact), and no render check of the chart sees a post-renderer
patch.)*

*(added 2026-09-27)* H-13 ~~sends~~ *(sent, until work list item 1 on 2026-09-27)* an operator
who wants to close the endpoint to a chart setting that does not exist. That is the false statement of the appendix (rule 1), and its
correction is the XS slice in the work list.

## Options

The wording fixes need no decision (work list). Two decisions are open. **Take decision 1
first**: it is small and independent of decision 2. It couples to
[ticket 049](049-no-networkpolicy-guards-the-operator-namespace.md) through the port and the
mode, not through shared lines: ticket 049 adds its own template and values and does not edit
`deployment.yaml`, but its `:8080` ingress rule and its render refusal keyed on
`metrics.serviceMonitor.enabled` must follow decision 1 (no `:8080` rule while the endpoint is
off). Ticket 049's reverse note carried the same "same lines" wording and was corrected on its
side the same day. **Take decision 2 together with or after ticket 049**, because decision 2's
recommendation rests on the bounds ticket 049 and decision 1 offer.

### Decision 1: a chart value for the operator's own metrics endpoint

**Mechanism.** The binary registers `--metrics-bind-address` with default `:8080`
([`cmd/main.go`](../../cmd/main.go) line 69) and hands it to controller-runtime (line 105), which
creates no server for `0` and listens on any other value. The chart hard-codes
`--metrics-bind-address=:8080`
([`deployment.yaml`](../../deploy/helm/valkey-operator/templates/deployment.yaml) line 37) and
always declares the named port `metrics` (lines 55–58). The optional Service targets that port
([`service.yaml`](../../deploy/helm/valkey-operator/templates/service.yaml) line 27), the
optional ServiceMonitor scrapes it
([`servicemonitor.yaml`](../../deploy/helm/valkey-operator/templates/servicemonitor.yaml)
lines 26–32), and the optional PrometheusRule's `ValkeyMetricsAbsent` fires when the series is
missing ([`prometheusrule.yaml`](../../deploy/helm/valkey-operator/templates/prometheusrule.yaml)
lines 161–162). Of the bind modes only `0` and loopback change who reaches the endpoint; a port
change does not (Fact). A chart user reaches `0` today only through a post-renderer patch that
no render check sees; a hand edit is reverted by the next upgrade (Fact). The choice changes
which modes the chart can render and which render refusals tie them to the Service, the
ServiceMonitor and the PrometheusRule. It does not change the binary, the default render,
authentication (decision 2), or who reaches the endpoint while it listens on the pod network
(ticket 049).

- **1A — no value; record the refusal.** Amend ADR 0018 D9 and its Consequences bullet (lines
  148–150) to state that the chart always serves the endpoint on the pod network, that a
  NetworkPolicy (ticket 049) is the restriction, and that `=0` on a chart install is a
  post-renderer patch the chart does not check. Cost XS: the ADR amendment and the H-13 and
  monitoring.md wording. Consequence: the mitigation ADR 0018 D9 and ADR 0021 name stays outside
  the chart, which [`rbac_drift_test.go`](../../internal/controller/rbac_drift_test.go) line 7
  calls the canonical install path - one layer up, the defect ADR 0018 D8 fixed in the binary.
  The post-renderer patch addresses the positional `args` by index, so a chart that reorders its
  arguments silently patches the wrong one, and it keeps the `metrics` port and the ServiceMonitor
  and PrometheusRule that then scrape nothing or fire forever.
- **1B — a free-form address, `metrics.bindAddress` (name open), default `":8080"`.** The default
  renders the same `args` literal, so the upgrade changes nothing
  ([ADR 0005](../adr/0005-upgrade-neutral-defaults-and-anti-affinity.md) D1; by construction,
  not rendered). `"0"` renders `=0` and drops the `metrics` container port; any other value must
  end in `:<port>` and the container port follows it. *(corrected 2026-09-27 at 84a39c2: the
  refusal set as filed, `"0"` with `metrics.service.enabled` or `metrics.serviceMonitor.enabled`,
  is incomplete: `"0"` must also refuse `metrics.prometheusRule.enabled`, because
  `ValkeyMetricsAbsent` would fire forever, and a loopback address must refuse all three,
  because nothing outside the pod can scrape it.)* Cost S: host:port parsing and validation in
  `_helpers.tpl` (IPv6 included) to keep the container port, the Service `targetPort` and the
  refusals in step, deployment.yaml line 37 and lines 55–58, `values.yaml`, one row in the README
  [Helm chart values](../../README.md#helm-chart-values) table (README lines 569–577), the
  ADR 0018 D9 and Consequences amendment, H-13 and monitoring.md, and a hand-run `helm template`
  until [ticket 058](058-no-ci-gate-renders-the-chart.md) gates the chart. Consequence: its extra
  capability over 1D is a port choice, which narrows nothing, and a loopback bind, whose only
  consumer is `kubectl port-forward`; an address the parser gets wrong can leave the Service
  pointing at a closed port with no render error.
- **1D — an off switch, two states (for example `metrics.endpoint.enabled`, default `true`; name
  and shape, boolean or two-value enum, open) (recommended).** On renders
  `--metrics-bind-address=:8080` byte-identical to line 37, so the upgrade changes nothing
  (ADR 0005 D1; by construction, not rendered). Off renders `--metrics-bind-address=0`, drops
  the `metrics` container port, and fails the render while `metrics.service.enabled`,
  `metrics.serviceMonitor.enabled` or `metrics.prometheusRule.enabled` is on. Cost XS–S: one
  value, an equality check per refusal in `_helpers.tpl`, deployment.yaml line 37 and lines
  55–58, `values.yaml`, one README row, the ADR 0018 D9 heading and Consequences amendment
  (lines 116, 148–150), ADR 0021 lines 155–157, H-13 and monitoring.md, and three hand-run
  `helm template` cases (later ticket 058 cases). Consequence: no port choice and no loopback
  mode; a loopback mode can be added the day someone names the port-forward need.

*(1C, a generic `extraArgs` list, and two shapes considered on 2026-09-27 were removed as not
sensible; the reasons are in History.)*

**1D is marked** because it makes off, the mitigation ADR 0018 D9 names that has a security
effect for every install that does not scrape the operator, reachable on the canonical install
path at XS–S cost, with a default that renders the same literal as line 37 and refusals that are plain equality checks a `helm template` case
can pin. It beats the runner-up 1B because 1B's only additions are a port, which narrows nothing
(`k8s.io/api` `types.go` lines 3163–3165), and a loopback bind nobody in the repository asks for,
and it pays for them with address parsing that has to keep the container port, the Service
`targetPort` and the refusals in step. It beats 1A because 1A leaves `=0` to a post-renderer
patch that is index-coupled and invisible to the chart's refusals, so an administrator who
follows it next to `serviceMonitor.enabled` gets a clean render, no scrape and a permanent
`ValkeyMetricsAbsent`.

### Decision 2: reopen ADR 0018 D10, the authentication filter

**Mechanism.** No `FilterProvider` is set ([`cmd/main.go`](../../cmd/main.go) line 105), so every
client that can route to the pod reads the `vko_valkey_*` inventory without credentials. If a
filter is set, controller-runtime v0.25.1 wraps `/metrics` with it whether or not TLS is on
(Fact). `WithAuthenticationAndAuthorization` authenticates the bearer token by TokenReview with
anonymous access disabled, authorizes by SubjectAccessReview, answers 401 or 403, and caches its
answers (Fact). It imports `k8s.io/apiserver`, which `go.mod` does not have. The operator's
ServiceAccount then needs `create` on `tokenreviews` and `subjectaccessreviews` (exactly
`system:auth-delegator`), and the scraper needs `get` on `/metrics`. Without `SecureServing` the
scraper's token crosses the pod network in clear; with it and no mounted certificate, the server
presents a self-signed certificate for `localhost`/`127.0.0.1` only and advertises only ALPN
`h2` (Fact). The chart ServiceMonitor has no https, TLS or authorization field. The choice
changes whether any scrape needs a credential. It does not change who can route to the pod
(ticket 049), what the payload contains, or the health port `:8081`.

- **A — decline the filter; record it in ADR 0018 D10 (recommended).** Amend D10 from "a
  separate, deliberate trade" to: declined; the default install stays public per D9; the opt-in
  bounds are the ticket 049 policy and decision 1's off switch; reopen when an install on a CNI
  that does not enforce NetworkPolicy must keep scraping with the inventory closed, and then take
  B with an issued certificate, not with skip-verify. Cost XS: the ADR amendment and the H-13
  sentence. Consequence: on a default install the endpoint stays unauthenticated, as ADR 0018
  records as posture (D9, lines 116–121; residual risk, lines 197–207). The recommendation
  depends on ticket 049: if ticket 049 is dropped, only decision 1's off switch remains, and it
  gives up scraping, so decision 2 has to be revisited.
- **B — the filter as an opt-in (a chart value plus a flag, default off), with `SecureServing`
  on.** Existing scrapers keep working until an administrator opts in (ADR 0005 D1). Its shape,
  corrected against the source:
  - `SecureServing` is mandatory, or the scraper's token travels in clear. That opens an HTTP/2
    question: the default TLS config advertises only `h2`, and the kubebuilder scaffold disables
    HTTP/2 for the Rapid Reset and Stream Cancellation advisories, so B needs a `TLSOpts`
    decision (Not verified: whether go1.27 makes it moot, and how Prometheus negotiates).
  - The operator grant is a conditional ClusterRoleBinding to `system:auth-delegator` in its own
    template: no rule in `clusterrole.yaml`, no drift-test change, but two more `create` verbs in
    the footprint ([ADR 0013](../adr/0013-operator-is-cluster-wide-privileged.md), the
    [operator ClusterRole table](../security/privilege-footprint.md#the-operator-clusterrole)),
    an entry no test checks.
  - The scraper ClusterRole (`nonResourceURLs: ["/metrics"]`, `get`) has to be bound to a
    Prometheus ServiceAccount the chart cannot know: a value, or left to the administrator.
  - The ServiceMonitor needs `scheme: https`, a `tlsConfig` and an `authorization` credential from
    a Secret (`bearerTokenFile` is deprecated and is forbidden under
    `arbitraryFSAccessThroughSMs`), in practice a long-lived ServiceAccount token Secret the
    administrator creates.
  - With the self-signed fallback the scraper must set `insecureSkipVerify`, so it presents its
    token to whoever answers at the pod IP - the exposure prometheus-operator warns about. B then
    trades a metadata disclosure for a credential exposure to anyone who can impersonate the
    target; a token dedicated to `get /metrics` limits what that credential reads. Only an issued
    certificate (a cert-manager Certificate mounted at `CertDir`, an optional dependency the
    chart does not have) avoids it.
  - `k8s.io/apiserver` moves with the Renovate `k8s-go-modules` group.
  
  Cost M to L: the module, a flag and a unit test next to `TestManagerOptions_MetricsBindAddress`
  ([`cmd/main_test.go`](../../cmd/main_test.go) line 295), the `SecureServing` and `TLSOpts`
  wiring, the certificate path, two templates (the auth-delegator binding and the scraper
  ClusterRole), the ServiceMonitor fields, the ADR 0013 and footprint entries, and a Kind check.
  Consequence: each scrape costs a TokenReview and a SubjectAccessReview, bounded by the caches.

*(C, the filter on by default, was removed on 2026-09-27 as not sensible; the reason is in
History.)*

**A is marked** because two cheaper opt-in bounds cover the same read path: ticket 049's
default-off NetworkPolicy (effort S) and decision 1's off switch (XS–S). Neither adds a module,
a scraper migration or the two `create` verbs B adds to the ADR 0013 footprint. B's only extra
coverage is a scraped endpoint on a CNI that does not enforce NetworkPolicy, and its realistic
shape for that case adds a module, `SecureServing` with an HTTP/2 decision, a scraper binding the
chart cannot name, a credential Secret, and either skip-verify, which exposes the scraper's
token, or a cert-manager dependency - all for a low-severity metadata disclosure that ADR 0018
records as posture. A beats B on proportion as long as ticket 049's option A is taken, and the
reopen trigger recorded in D10 keeps B available the day a non-enforcing CNI needs it.

## Work list

**Not waiting on a decision (XS):**

1. **H-13 correction (rule 1).** Strike "from the chart" in
   [`operator-pod-posture.md`](../security/operator-pod-posture.md) lines 88–89 and state what
   holds: the chart passes `:8080` unconditionally and has no value for it. In the same change,
   optionally: qualify [`monitoring.md`](../operations/monitoring.md) lines 83–84 and the
   `values.yaml` comment lines 110–111, both misleading rather than false (a comment only, no
   render change). ADR 0021 lines 155–157 stay, because they name the flag. Doing this does not
   close the ticket. **Done 2026-09-27** for H-13 *(committed in `bcc63c9`, re-read
   2026-09-27 at 84a39c2)*; the optional `monitoring.md` and `values.yaml` qualifications were
   not taken and stay open as misleading, not false.
2. *(added 2026-09-27 at 84a39c2)* **The "move" wording.** Rewrite each place that presents a
   move as a restriction so that it says a move narrows reach only when it binds loopback and a
   different port narrows nothing (`k8s.io/api` v0.37.1 `core/v1/types.go` lines 3163–3165): H-13's
   heading and body ([`operator-pod-posture.md`](../security/operator-pod-posture.md) lines 81,
   87–94), ADR 0018 D9's heading (line 116), ADR 0021 lines 155–157,
   [`monitoring.md`](../operations/monitoring.md) lines 83–84 and the `values.yaml` comment lines
   110–111 (absorbs the optional half of item 1).
3. *(added 2026-09-27 at 84a39c2)* **The revert caveat.** H-13's corrected sentence ("changing
   the Deployment's arguments outside the chart") gets: a hand edit is reverted by the next
   `helm upgrade`, and only a post-renderer patch lasts, unchecked by the chart (Fact). If
   decision 1 lands first, this sentence is rewritten around the value instead.

**Waiting on decision 1 (1D):** the value, the `deployment.yaml` wiring (line 37, lines 55–58),
the three render refusals in `_helpers.tpl`, the README row, the `helm template` cases under
Verification, and rewriting H-13, monitoring.md, the `values.yaml` comment, ADR 0018 D9 and its
Consequences bullet (lines 148–150) and ADR 0021 lines 155–157 around the value. Tell ticket 049
the value name, so its `:8080` rule and refusal follow it; add the cases to ticket 058's gate
when that lands.

**Waiting on decision 2:** under A, the ADR 0018 D10 amendment with the reopen trigger and the
H-13 sentence. Under B: add `k8s.io/apiserver`; a flag that sets `FilterProvider`,
`SecureServing` and `TLSOpts`; a unit test next to `TestManagerOptions_MetricsBindAddress`
([`cmd/main_test.go`](../../cmd/main_test.go) line 295); the certificate path; a template with
the conditional ClusterRoleBinding to `system:auth-delegator` and one with the scraper
ClusterRole; the ServiceMonitor https, TLS and authorization fields; the ADR 0013 and
privilege-footprint entries; the Kind check below.

**Close (ADR 0034):** amend ADR 0018 (D10, and D9 with its Consequences bullet if decision 1
lands) and ADR 0021 lines 155–157. If B lands, also the ADR 0013 footprint and
[the operator ClusterRole table](../security/privilege-footprint.md#the-operator-clusterrole).
Then the README values rows, monitoring.md and H-13. `git grep -n 'T48\|048-the-operator'`
outside `docs/tickets/` (none, re-run 2026-09-27 at 84a39c2), then move to `archive/`.

## Decision

None yet.

## Verification

- *(added 2026-09-27, XS slice)* `grep -n "from the chart" docs/security/operator-pod-posture.md`
  no longer finds the claim unstruck. *(Run 2026-09-27 after the fix: one hit, `:89`, the
  struck text and its correction. Done. Re-run at 84a39c2: same hit, committed in `bcc63c9`.)*
- ~~*(added 2026-09-27, decision 1)* `helm template` with default values is byte-identical to the
  render at HEAD. `metrics.bindAddress=0` renders `--metrics-bind-address=0` and no `metrics`
  container port. `0` together with `metrics.serviceMonitor.enabled=true` fails the render.~~
  *(corrected 2026-09-27 at 84a39c2, for the recommended 1D:)* `helm template` with default
  values is byte-identical to the render at HEAD. The value off renders
  `--metrics-bind-address=0` and no `metrics` container port. Off together with
  `metrics.service.enabled`, `metrics.serviceMonitor.enabled` or `metrics.prometheusRule.enabled`
  fails the render, one case each. Revert check: removing one refusal lets its case render.
  These become cases of ticket 058's gate when it lands.
- Decision 2, option B only: a unit test in `cmd`, next to `TestManagerOptions_MetricsBindAddress`,
  pins that the filter is set when the option is on and absent when it is off.
- Decision 2, option B only, on Kind: an unauthenticated request to `:8080/metrics` is refused
  with the option on and answered with it off; a request with a token bound to the scraper
  ClusterRole is answered; the chart ServiceMonitor scrape succeeds over https (settles the
  self-signed and ALPN `h2` questions under Not verified).
- Decision 2, option B only, revert check: removing the `FilterProvider` line turns the unit
  test above red.

## History

- 2026-09-27 — re-verified at 84a39c2. **Checked:** every Fact claim against HEAD and the pinned
  upstream sources (controller-runtime v0.25.1 and `k8s.io/api` v0.37.1 in the module cache;
  kubernetes v1.37.1 `bootstrappolicy/policy.go`, containerd v2.1.4 port-forward and Helm v3.19.0
  `pkg/kube/client.go` fetched from raw.githubusercontent.com; the Flux HelmRelease and
  prometheus-operator API pages); the greps re-run (no filter, no `SecureServing`, no review
  grant, no `k8s.io/apiserver`, no citation of T48 outside `docs/tickets/`). No docker
  measurement: no claim concerns Valkey behaviour; no container was started, nothing was
  rendered or run on a cluster. Locations re-read at 84a39c2: the drift guard is at
  `rbac_drift_test.go` lines 113–115 (was cited as 112–114, fixed in the link); ADR 0021 lines
  155–157 hold (a review proposal to narrow them to 156–157 was wrong, 155 starts the sentence).
  **Found outdated or false:** the previous History entry said the H-13 correction was read "in
  `git diff` of the working tree"; it is committed in `bcc63c9`. The severity comment and Impact
  named "disabling the endpoint outside the chart" as a bound; a hand edit of the args is reverted
  by the next `helm upgrade` (three-way patch with overwrite, atomic `args`, `--operator-image`
  changing with every appVersion), and only a post-renderer patch lasts - qualified in place (review below).
  The appendix's Not-verified "only `=0` or an unroutable address narrows reach" moved to
  Verified from the API contract, with the port-forward qualification for loopback. The Options
  header said decision 1 touches the same `deployment.yaml` and `values.yaml` lines as ticket 049;
  ticket 049 does not edit `deployment.yaml`, and the coupling is the port and the mode. 1B's
  refusal set missed `metrics.prometheusRule.enabled` (`ValkeyMetricsAbsent`) and a loopback
  refusal. B's shape missed the `system:auth-delegator` binding, the HTTP/2 ALPN question, the
  skip-verify token exposure and the credential Secret. **New facts:** the "move" imprecision in
  six places and the ADR 0018 Consequences bullet that decision 1 falsifies (appendix, work list
  items 2 and 3). **Options, decision 1:** added 1D, a two-state off switch, and made it the
  recommendation instead of 1B, because 1B's extra port choice narrows nothing and its loopback
  mode has no consumer, while it needs address parsing; 1A reworded around the post-renderer
  route instead of "change the Deployment outside the chart". Removed: **1C** (a generic
  `extraArgs` list) - an entry is coupled to nothing (`=0` next to `serviceMonitor.enabled` renders
  cleanly, `:9090` leaves `service.yaml` line 27 on 8080) and a repeated flag silently overrides a
  validated one, such as `--allowed-seccomp-localhost-profiles`, which the chart checks at render
  time (`_helpers.tpl` lines 136–143, gap H-15) (go1.27.1 `flag.go`, `stringValue.Set` lines
  247–249, read); no other need for a generic flag surface is recorded; **a three-state enum
  `pod | loopback | off`** (proposed by the audit of this run, never in the ticket) - its loopback
  mode serves only `kubectl port-forward`, which nothing in the repository asks for, so it is
  speculative scope; **deriving the bind from the chart's metrics values** (`:8080` only while the
  Service, ServiceMonitor or PrometheusRule is on, considered in this run) - it silently removes
  the series from installs that scrape another way (a hand-written PodMonitor, `prometheus.io/*`
  annotations through the chart's `podAnnotations`, `deployment.yaml` line 15) and changes
  behaviour on upgrade against ADR 0005 D1. **Options, decision 2:** A stays recommended, its
  proposed D10 wording corrected (both bounds are opt-in, so it is "declined, the default stays
  public, reopen when ...", not "declined while bounded"); B rewritten with the corrected shape.
  Removed: **C** (the filter on by default) - it answers 401 to every existing scraper on the
  upgrade that ships it (`filters.go` lines 109–112, the ServiceMonitor sends no credential), and
  ADR 0018 D9 records the open endpoint as posture, not a defect, so the fleet-wide no-toggle rule
  does not apply; nothing found supports reopening D9 for a metadata-only disclosure. Accumulated
  review addenda inside Options replaced by the current text. **Frontmatter:** `state` filed ->
  analysed (facts verified, options coherent); `effort` M -> S (the recommended options are a chart
  value and wording; M to L only if decision 2 takes B); `urgency` stays `later`, re-derived
  top-down (rule 4); severity comment corrected (the hand edit is no bound); `blocked-by` comment
  updated. **Not verified:** Helm 4 server-side apply and post-renderer interface (CI pins
  v4.3.0), CRI-O port-forward, the production CNI's NetworkPolicy enforcement, whether the
  operator is installed through a Flux HelmRelease and with drift detection, Prometheus against
  the self-signed certificate and an `h2`-only server. **Review of this entry (same day):**
  location drift fixed (`server.go` `0` check at lines 120–121, not 118–121; ALPN `h2` at lines
  278–280, not 276–278); the severity comment and the Impact correction had been struck whole,
  earlier corrections included, and are restored, with the post-renderer and CNI qualifications
  added beside them instead, because changing the arguments outside the chart does disable the
  endpoint, only a hand edit of them does not last; the "Not verified" items answered by reading
  are marked "answered in part"; both prometheus-operator bearer-token fields confirmed
  deprecated in its source (`main` branch). Disputed and settled: the audit cited the Helm FAQ's
  replicas example for the upgrade revert, but that example is a `helm rollback`; the revert on
  `helm upgrade` rests on Helm v3.19.0 `pkg/kube/client.go` line 699, atomic `args` and
  `--operator-image`. The audit's "no durable way on a Helm-managed fleet" was refuted by the
  post-renderer route. Cross-ticket: ticket 049 had the mirror "same lines" wording and corrected
  it on its side; ticket 040 lists this ticket as the work behind H-13, consistent with no
  citation of T48 outside `docs/tickets/`; the filed-from row is row 1 of the archived ticket
  031's further-measures table, confirmed.
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
