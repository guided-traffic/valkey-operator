---
id: T48
title: the operator metrics endpoint serves the fleet inventory without authentication
state: analysed       # facts verified, both decisions carry a coherent option set
severity: low         # disclosure of names and health, no write path; a NetworkPolicy bounds it where the CNI enforces one
security: hardening
threat: "would additionally cover any client that can route to the operator pod and reads :8080/metrics: today it gets, without credentials, the vko_valkey_* series that name every Valkey resource by namespace and name, with its health"
urgency: later        # rule 4: the off switch and the wording fixes are cheap known fixes
effort: S             # with the recommended options; M to L if Q2 takes B
blocked-by: decision  # Q1 and Q2; the wording fixes are not blocked
filed-from: T31, section "Further security measures — not in this change, each open" (archive/031)
opened: 2026-09-27
decided:
done:
---

# T48 - the operator metrics endpoint serves the fleet inventory without authentication

## Current state

- The manager builds its metrics server from a bind address and nothing else:
  `Metrics: metricsserver.Options{BindAddress: f.metricsAddr}` ([`cmd/main.go`](../../cmd/main.go)
  line 105). No `FilterProvider`, `WithAuthenticationAndAuthorization` or `SecureServing` exists
  in the repository, and no ClusterRole grants `tokenreviews` or `subjectaccessreviews`.
- The payload names every Valkey resource by namespace and name: the labels are defined at
  [`collector.go`](../../internal/metrics/collector.go) lines 50–51 and carried by every
  per-resource descriptor (lines 95–120); the collector lists all Valkey resources cluster-wide.
- The flag `--metrics-bind-address` defaults to `:8080` ([`cmd/main.go`](../../cmd/main.go)
  line 69). The chart hard-codes `--metrics-bind-address=:8080`
  ([`deployment.yaml`](../../deploy/helm/valkey-operator/templates/deployment.yaml) line 37) and
  always declares the named container port `metrics` (lines 55–58).
  [`values.yaml`](../../deploy/helm/valkey-operator/values.yaml) has no key for it; its
  `metrics:` block (lines 112–145) configures only the Service, the ServiceMonitor and the
  PrometheusRule. The kustomize manifest under `config/manager` serves the default `:8080` too.
- Only `=0` (controller-runtime creates no server) or a loopback bind narrows who reaches the
  endpoint; a different port narrows nothing, because any port listening on `0.0.0.0` in a
  container is reachable from the network (`k8s.io/api` v0.37.1 `core/v1/types.go` lines
  3163–3165). A loopback bind stays reachable through `kubectl port-forward`.
- On a chart install, a hand edit of the Deployment `args` is reverted by the next
  `helm upgrade` (three-way patch with overwrite, `args` is an atomic list, and the args change
  with every release through `--operator-image`). The only durable out-of-chart route is a
  post-renderer patch, which addresses the positional `args` by index and is invisible to every
  render refusal of the chart.
- With the endpoint off, the chart's own `ValkeyMetricsAbsent` alert
  (`absent(vko_valkey_collector_success)`,
  [`prometheusrule.yaml`](../../deploy/helm/valkey-operator/templates/prometheusrule.yaml)
  lines 161–162) fires forever, and the chart Service
  ([`service.yaml`](../../deploy/helm/valkey-operator/templates/service.yaml) line 27) and
  ServiceMonitor
  ([`servicemonitor.yaml`](../../deploy/helm/valkey-operator/templates/servicemonitor.yaml)
  lines 26–32, no scheme, TLS or authorization field) point at nothing.
- Docs present a move of the endpoint as a restriction, which it is not unless it binds
  loopback: H-13 heading and body
  ([`operator-pod-posture.md`](../security/operator-pod-posture.md) lines 81, 87–94),
  [ADR 0018](../adr/0018-metrics-and-the-exporter-sidecar.md) D9 heading (line 116),
  [ADR 0021](../adr/0021-per-resource-metrics-and-the-alert-that-was-missing.md) lines 155–157,
  [`monitoring.md`](../operations/monitoring.md) lines 83–84 and the `values.yaml` comment lines
  110–111. H-13 says disabling means "changing the Deployment's arguments outside the chart"
  without saying that a hand edit does not survive an upgrade.

**Impact.** Live on every default install: any pod, and anything else routable to the pod
network, reads the inventory; nothing can be written through the endpoint. Today the only
bound is a NetworkPolicy for the operator namespace written by the administrator (the chart
ships none, T49), effective only where the CNI enforces it.

## Required changes

**Independent of the open questions**

1. Rewrite the "move" wording in the places listed above: a move narrows reach only when it
   binds loopback; a different port narrows nothing.
2. H-13: add that a hand edit of the args is reverted by the next `helm upgrade` and only a
   post-renderer patch lasts, unchecked by the chart. If Q1 lands first, rewrite the sentence
   around the new value instead.

**Depends on the answers**

- Q1 = 1D: the value; `deployment.yaml` line 37 and lines 55–58 (off renders
  `--metrics-bind-address=0` and drops the `metrics` port); render refusals in `_helpers.tpl`
  for off with `metrics.service.enabled`, `metrics.serviceMonitor.enabled` or
  `metrics.prometheusRule.enabled`; `values.yaml`; one row in the README
  [Helm chart values](../../README.md#helm-chart-values) table; rewrite H-13, monitoring.md, the
  `values.yaml` comment, ADR 0018 D9 and its Consequences bullet "no chart value changes that"
  (lines 148–150) and ADR 0021 lines 155–157 around the value. Tell T49 the value name so its
  `:8080` ingress rule and render refusal follow it.
- Q2 = A: amend ADR 0018 D10 to "declined; the default install stays public per D9; the opt-in
  bounds are the T49 policy and the Q1 off switch; reopen when an install on a CNI that does
  not enforce NetworkPolicy must keep scraping with the inventory closed, and then take B with
  an issued certificate". Adjust the H-13 sentence.
- Q2 = B: see the option below for the full work list.

**Tests**

- Q1 = 1D, hand-run `helm template` until T58 gates the chart: default values render
  byte-identical to today; off renders `--metrics-bind-address=0` and no `metrics` port; off
  together with each of the three metrics values fails the render, one case each. Revert check:
  removing one refusal lets its case render.
- Q2 = B: a unit test next to `TestManagerOptions_MetricsBindAddress`
  ([`cmd/main_test.go`](../../cmd/main_test.go) line 295) pins the filter on and off (removing
  the `FilterProvider` line turns it red); on Kind, an unauthenticated request is refused with
  the option on and answered with it off, a token bound to the scraper ClusterRole is answered,
  and the chart ServiceMonitor scrape succeeds over https.

**Close:** amend ADR 0018 (D10, and D9 with its Consequences bullet under Q1) and ADR 0021
lines 155–157; under B also the [ADR 0013](../adr/0013-operator-is-cluster-wide-privileged.md)
footprint and
[the operator ClusterRole table](../security/privilege-footprint.md#the-operator-clusterrole).

## Open questions

Take Q1 first; it is small and independent. Take Q2 together with or after T49, because its
recommendation rests on the bounds T49 and Q1 offer.

### Q1: Should the chart offer a value for the operator's own metrics endpoint, and in which shape?

The chart always serves the endpoint on `:8080`; a chart user can switch it off only through a
post-renderer patch that the chart's render refusals cannot see, so `=0` next to an enabled
ServiceMonitor renders cleanly, scrapes nothing and fires `ValkeyMetricsAbsent` forever.

- **1A - no value, record the refusal.** Amend ADR 0018 D9 to say the chart always serves the
  endpoint and a NetworkPolicy is the restriction. Cost XS; the off switch stays an
  index-coupled post-renderer patch outside the canonical install path.
- **1B - a free-form address (for example `metrics.bindAddress`, default `":8080"`).** `"0"`
  switches off, any other value sets the container port; off refuses all three metrics values,
  loopback refuses them as well. Cost S: host:port parsing (IPv6 included) that must keep the
  container port, the Service `targetPort` and the refusals in step; its extras over 1D are a
  port choice, which narrows nothing, and a loopback mode nobody asks for.
- **1D - an off switch, two states (for example `metrics.endpoint.enabled`, default `true`)
  (recommended).** On renders the current literal byte-identically (upgrade-neutral, ADR 0005
  D1); off renders `=0`, drops the port and refuses the three metrics values. Cost XS–S.
  Recommended because it makes the one mitigation with a security effect reachable on the
  canonical install path with plain equality checks, without 1B's parsing or 1A's unchecked
  patch. Name and shape (boolean or two-value enum) are open.

**Answer:** _open_

### Q2: Should the operator authenticate scrapes of its metrics endpoint (reopen ADR 0018 D10)?

controller-runtime v0.25.1 can wrap `/metrics` with `WithAuthenticationAndAuthorization`
(TokenReview plus SubjectAccessReview, 401/403, cached), also on plain HTTP, where the scraper's
token then travels in clear. It imports `k8s.io/apiserver`, which `go.mod` does not have.

- **A - decline the filter, record it in ADR 0018 D10 (recommended).** Cost XS. The default
  install stays unauthenticated, as ADR 0018 D9 records as posture. Recommended because the T49
  NetworkPolicy and the Q1 off switch cover the same read path at far lower cost; B adds only a
  scraped endpoint on a non-enforcing CNI. If T49 is dropped, revisit.
- **B - the filter as an opt-in chart value and flag, default off, with `SecureServing`.**
  Cost M to L:
  - add `k8s.io/apiserver` (rides the Renovate `k8s-go-modules` group); a flag setting
    `FilterProvider`, `SecureServing` and `TLSOpts`, including an HTTP/2 decision (the default
    TLS config advertises only ALPN `h2`);
  - a conditional ClusterRoleBinding to `system:auth-delegator` (exactly `create` on
    `tokenreviews` and `subjectaccessreviews`) in its own template, which keeps
    `clusterrole.yaml` and the drift test untouched but adds two verbs to the footprint that no
    test checks;
  - a scraper ClusterRole (`get` on `/metrics`) bound to a Prometheus ServiceAccount the chart
    cannot know;
  - ServiceMonitor `scheme: https`, a `tlsConfig` and an `authorization` credential from a
    Secret (`bearerTokenFile` and `bearerTokenSecret` are deprecated);
  - a certificate: the fallback is self-signed for `localhost`/`127.0.0.1` only, so a scraper
    must skip verification and presents its token to whoever answers at the pod IP; only an
    issued certificate mounted at `CertDir` (a cert-manager dependency the chart does not have)
    avoids that.

**Answer:** _open_

## Not verified

- Helm 4 (CI pins `v4.3.0`): whether server-side apply also reverts a hand edit of the args, and
  its post-renderer interface. A `helm upgrade` after `kubectl edit` on Kind settles it.
- Whether the production CNI enforces NetworkPolicy, and whether the operator is installed
  through a Flux HelmRelease with drift detection. `kubectl get helmrelease -A` and a
  NetworkPolicy deny test settle it.
- For B only: a Prometheus scrape against the self-signed certificate and an `h2`-only server,
  and whether the go1.27 HTTP/2 mitigations make an explicit HTTP/2 opt-out moot. The Kind check
  under Tests settles the first.

## Related

- [T49](049-no-networkpolicy-guards-the-operator-namespace.md) - the NetworkPolicy that bounds
  the endpoint; its `:8080` rule and refusal must follow Q1.
- [T58](058-no-ci-gate-renders-the-chart.md) - the chart render gate that takes over the
  `helm template` cases.
- T40 - lists this ticket as the work behind H-13.
