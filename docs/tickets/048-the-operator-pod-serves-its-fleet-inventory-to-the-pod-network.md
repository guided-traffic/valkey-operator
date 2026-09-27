---
id: T48
title: the operator pod serves its fleet inventory on :8080 to the whole pod network
state: analysed       # facts verified, every decision carries a coherent option set
severity: low         # disclosure of names and health, no Secret material, no write path
security: hardening
threat: "would additionally cover any client that can route to the operator pod, outside a configured peer list, and reads :8080/metrics: today it gets, without credentials, the vko_valkey_* series that name every Valkey resource by namespace and name, with its health; :8081 (answers only 'ok') and the operator's egress are deliberately not covered"
urgency: later        # rule 4: the off switch, the default-off policy template and the wording fixes are cheap known fixes
effort: S             # with the recommended options; M if the enforcement check needs a local-only gate, M to L if Q4 takes B
blocked-by: decision  # Q1 to Q4; the wording fixes are not blocked
filed-from: T31, section "Further security measures — not in this change, each open" (archive/031)
opened: 2026-09-27
decided:
done:
---

# T48 - the operator pod serves its fleet inventory on :8080 to the whole pod network

**Scope.** The operator pod serves the per-resource fleet inventory
([ADR 0021](../adr/0021-per-resource-metrics-and-the-alert-that-was-missing.md)) on `:8080`
without authentication, the chart offers no way to switch it off, and the chart ships no
NetworkPolicy that limits who reaches it. Both bounds share the named port `metrics`, the same
three render refusals and the same documentation, so they are decided and built as one package.
The operator-facing statements of the gap are
[H-13](../security/operator-pod-posture.md) and [H-14](../security/operator-pod-posture.md#h-14).

- **Metrics endpoint** - the unauthenticated `:8080` listener, the missing chart value to switch
  it off, and whether scrapes should be authenticated.
- **Operator-namespace NetworkPolicy** - no chart template restricts ingress to the operator pod.

## Current state

**Shared facts.**

- The payload names every Valkey resource by namespace and name: the labels are defined at
  [`collector.go`](../../internal/metrics/collector.go) lines 50–51 and carried by every
  per-resource descriptor (lines 95–120); the collector lists all Valkey resources cluster-wide.
- The operator pod declares `:8080` (named port `metrics`) and `:8081` (named port `health`)
  ([`deployment.yaml`](../../deploy/helm/valkey-operator/templates/deployment.yaml) lines 55–58
  and 60, bound at lines 37–38). `:8081` carries only `healthz.Ping`
  ([`cmd/main.go`](../../cmd/main.go) lines 188–195) and answers `ok` (with `?verbose` the check
  names); no pprof listener exists. The disclosure that matters is `:8080`.
- [`values.yaml`](../../deploy/helm/valkey-operator/values.yaml) `metrics:` block (lines
  112–145) configures only the Service, the ServiceMonitor and the PrometheusRule; its comment at
  lines 106–111 describes the endpoint.

**Impact.** Live on every default install: any pod, and anything else routable to the pod
network, reads the inventory; nothing can be written through the endpoint. An installer can write
a NetworkPolicy by hand, the chart offers none, and on a CNI that does not enforce NetworkPolicy
no policy changes anything.

### Metrics endpoint

- The manager builds its metrics server from a bind address and nothing else:
  `Metrics: metricsserver.Options{BindAddress: f.metricsAddr}` ([`cmd/main.go`](../../cmd/main.go)
  line 105). No `FilterProvider`, `WithAuthenticationAndAuthorization` or `SecureServing` exists
  in the repository, and no ClusterRole grants `tokenreviews` or `subjectaccessreviews`.
- `--metrics-bind-address` defaults to `:8080` ([`cmd/main.go`](../../cmd/main.go) line 69). The
  chart hard-codes `--metrics-bind-address=:8080` (`deployment.yaml` line 37) and always declares
  the `metrics` port; `values.yaml` has no key for it. The kustomize manifest under
  `config/manager` serves the default `:8080` too.
- Only `=0` (controller-runtime creates no server) or a loopback bind narrows who reaches the
  endpoint; a different port narrows nothing, because any port listening on `0.0.0.0` in a
  container is reachable from the network (`k8s.io/api` v0.37.1 `core/v1/types.go` lines
  3163–3165). A loopback bind stays reachable through `kubectl port-forward`.
- On a chart install, a hand edit of the Deployment `args` is reverted by the next
  `helm upgrade` (three-way patch with overwrite, `args` is an atomic list, and the args change
  with every release through `--operator-image`). The only durable out-of-chart route is a
  post-renderer patch, which addresses the positional `args` by index and is invisible to every
  render refusal of the chart.
- With the endpoint off, the chart's `ValkeyMetricsAbsent` alert
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

### Operator-namespace NetworkPolicy

- The chart renders no NetworkPolicy
  ([`templates/`](../../deploy/helm/valkey-operator/templates/); `helm template rel
  deploy/helm/valkey-operator --namespace vko` renders none).
- Nothing in this repository connects to the operator pod except kubelet: no webhook server
  (`cmd/main.go` lines 102–110), no webhook configuration, no conversion webhook. An
  ingress-only policy on the operator pod cannot cut reconciling; the users who connect are the
  scrapers of `:8080`.
- The operator's egress is the API server, DNS and the Valkey and Sentinel ports 6379, 16379,
  26379, 36379; its only dialer is
  [`internal/valkeyclient/client.go`](../../internal/valkeyclient/client.go) lines 398–401.
- A compromised operator defeats any policy on its own pod: the chart ClusterRole grants
  `networkpolicies` full CRUD and `deployments`/`statefulsets` create/update/patch cluster-wide
  ([`clusterrole.yaml`](../../deploy/helm/valkey-operator/templates/clusterrole.yaml) lines
  73–85, 97–109). Egress filtering therefore protects nothing and is not an option.
- The chart selector labels
  ([`_helpers.tpl`](../../deploy/helm/valkey-operator/templates/_helpers.tpl) lines 46–49) are
  also on the pre-upgrade hook pod
  ([`pre-upgrade-job.yaml`](../../deploy/helm/valkey-operator/templates/pre-upgrade-job.yaml)
  lines 19–20), which declares no `ports` and only lists and patches Valkey CRs.
- The workload NetworkPolicies the operator writes admit the whole operator namespace on the data
  and Sentinel ports ([`networkpolicy.go`](../../internal/builder/networkpolicy.go) lines 77–88,
  197–207; [ADR 0013](../adr/0013-operator-is-cluster-wide-privileged.md) D7), and their exporter
  port is open to every source (lines 129–143). Both are out of scope here.
- NetworkPolicy semantics that shape the policy: an ingress-isolated pod always admits its
  resident node and reply traffic; hostNetwork behaviour is undefined and CNI-specific; an
  ingress rule with an empty or missing `from` admits every source; named ports are allowed
  (`k8s.io/api` v0.37.1 `networking/v1/types.go` lines 122–125, 164–166).
- CI e2e runs kind v0.33.0 with kindnet, which enforces NetworkPolicy through
  kube-network-policies (nfqueue, fail-open by default, admits root-owned node traffic). CI loads
  no `nfnetlink_queue` (`release.yml` lines 102–115). No test checks that a NetworkPolicy is
  enforced, and no e2e reads `:8080` or `:8081`.

## Required changes

**Shared, in one change across both parts**

1. One render-refusal helper in `_helpers.tpl` for the three metrics values
   `metrics.service.enabled`, `metrics.serviceMonitor.enabled` and
   `metrics.prometheusRule.enabled`: it fails the render when the endpoint is off (Q1 = 1D) or
   when the policy is on with an empty peer list (Q2 = A).
2. The policy's `metrics` rule is omitted when the endpoint is off (Q1 = 1D and Q2 = A).
3. One documentation pass naming both new values (Q1, Q2): README
   [Helm chart values](../../README.md#helm-chart-values) rows, H-13 and H-14, the `values.yaml`
   comment lines 106–111, [monitoring.md](../operations/monitoring.md) lines 82–84, ADR 0021 lines
   155–157, ADR 0018 D9 and its Consequences bullet "no chart value changes that" (lines 148–150,
   false under either value), and
   [ADR 0033](../adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md)
   lines 714–716 (lists the policy as open).
4. Render tests, hand-run `helm template` until T43 gates the chart, then added to its matrix:
   default values render byte-identical to today with no policy; each value on renders what its
   part below names; every refusal case fails the render, one case each. Revert check: removing
   one refusal lets its case render.

**Metrics endpoint**

- Independent: rewrite the "move" wording in the places listed under Current state: a move
  narrows reach only when it binds loopback; a different port narrows nothing.
- Independent: H-13 states that a hand edit of the args is reverted by the next `helm upgrade`
  and only a post-renderer patch lasts, unchecked by the chart. If Q1 lands first, rewrite the
  sentence around the new value instead.
- (Q1 = 1D) The value in `values.yaml`; `deployment.yaml` line 37 and lines 55–58: off renders
  `--metrics-bind-address=0` and drops the `metrics` port; on renders the current literal
  byte-identically.
- (Q4 = A) Amend ADR 0018 D10 to "declined; the default install stays public per D9; the opt-in
  bounds are the operator-namespace policy and the endpoint off switch; reopen when an install
  on a CNI that does not enforce NetworkPolicy must keep scraping with the inventory closed, and
  then take B with an issued certificate". Adjust the H-13 sentence.
- (Q4 = B) The work list in the option below, plus a unit test next to
  `TestManagerOptions_MetricsBindAddress` ([`cmd/main_test.go`](../../cmd/main_test.go) line 295)
  that pins the filter on and off (removing the `FilterProvider` line turns it red), and on Kind:
  an unauthenticated request is refused with the option on and answered with it off, a token
  bound to the scraper ClusterRole is answered, and the chart ServiceMonitor scrape succeeds over
  https. Close also amends the [ADR 0013](../adr/0013-operator-is-cluster-wide-privileged.md)
  footprint and
  [the operator ClusterRole table](../security/privilege-footprint.md#the-operator-clusterrole).

**Operator-namespace NetworkPolicy (Q2 = A)**

- `templates/networkpolicy.yaml`, default off, one NetworkPolicy in the release namespace:
  `podSelector` = the chart selector labels (no `component` exclusion: a `podLabels` entry could
  take the operator pod out of its own policy), `policyTypes: [Ingress]`.
- Rule on named port `metrics` with `from` = a user-supplied `NetworkPolicyPeer` list (`toYaml`,
  so pod selectors, namespace selectors and `ipBlock` fit). An empty list omits the rule, never
  `from: []`.
- Rule on named port `health` with no `from`, so the liveness of the single reconciler does not
  depend on how a CNI implements the node allowance.
- Values named so they read as the operator pod's policy, for example
  `operatorNetworkPolicy.enabled` (default `false`) and `operatorNetworkPolicy.metricsFrom`
  (default `[]`).
- The template leaves the Deployment pod template untouched, so enabling it restarts nothing;
  the render test asserts the Deployment byte-identical either way and exactly one policy with
  the value on.
- H-14 ([`operator-pod-posture.md`](../security/operator-pod-posture.md) lines 100–105) states
  that egress is deliberately not filtered (the RBAC reason); H-13's mitigation sentence (lines
  97–98) names the value.
- ADR: amend ADR 0013 D7 or write a new ADR (plus index line) recording the default-off policy,
  the refusal of egress filtering (RBAC reason), the refusal of a default-on policy (ADR 0018 D9
  calls the open endpoint posture), and the robustness reason for open health ports.
- (Q3) The enforcement check: on an enforcing CNI, a non-hostNetwork pod outside the peer list
  is refused on `:8080`, a listed pod is answered, with an empty list no pod reaches `:8080`, an
  e2e Valkey resource reaches `OK`, and `helm upgrade` completes its pre-upgrade hook.

## Open questions

Take Q1 first; it is small and independent. Q3 follows Q2. Take Q4 last, because its
recommendation rests on the bounds Q1 and Q2 offer.

### Q1: Should the chart offer a value for the operator's own metrics endpoint, and in which shape? (metrics endpoint)

The chart always serves the endpoint on `:8080`; a chart user can switch it off only through a
post-renderer patch that the chart's render refusals cannot see, so `=0` next to an enabled
ServiceMonitor renders cleanly, scrapes nothing and fires `ValkeyMetricsAbsent` forever.

- **1A - no value, record the refusal.** Amend ADR 0018 D9 to say the chart always serves the
  endpoint and a NetworkPolicy is the restriction. Cost XS; the off switch stays an
  index-coupled post-renderer patch outside the canonical install path.
- **1B - a free-form address (for example `metrics.bindAddress`, default `":8080"`).** `"0"`
  switches off, any other value sets the container port; off and loopback refuse all three
  metrics values. Cost S: host:port parsing (IPv6 included) that must keep the container port,
  the Service `targetPort` and the refusals in step; its extras over 1D are a port choice, which
  narrows nothing, and a loopback mode nobody asks for.
- **1D - an off switch, two states (for example `metrics.endpoint.enabled`, default `true`)
  (recommended).** On renders the current literal byte-identically (upgrade-neutral, ADR 0005
  D1); off renders `=0`, drops the port and refuses the three metrics values. Cost XS–S.
  Recommended because it makes the one mitigation with a security effect reachable on the
  canonical install path with plain equality checks, without 1B's parsing or 1A's unchecked
  patch. Name and shape (boolean or two-value enum) are open.

**Answer:** _open_

### Q2: Should the chart offer the operator pod's NetworkPolicy as a template, or only document an example? (operator-namespace NetworkPolicy)

The operator pod needs no ingress besides kubelet, so a policy restricting `:8080` to listed
scrapers costs reconciling nothing. The question is who writes and maintains the selector.

- **A - ingress-only chart template, default off (recommended).** As in Required changes. Cost
  S; the selector is the chart's own and the render refuses combinations that would break
  scraping.
- **C - documented example under `docs/operations/`, no template.** Cost XS. The copied selector
  depends on `nameOverride` and the release name and fails open silently when it stops matching;
  nothing refuses a scraper without a peer, and no render checks the snippet.

A is recommended because it closes H-14's `:8080` exposure from the chart with a selector that
cannot drift from the Deployment, for one template, two values and three checks more than C.

**Answer:** _open_

### Q3: Where is enforcement of the policy proven? (operator-namespace NetworkPolicy)

A green test without a refused request cannot tell "enforced" from "vacuous", and whether kindnet
enforces inside CI's Docker-in-Docker runners is unknown. E needs Q2 = A.

- **L - one local Kind check by hand** (`make kind-create`), recorded in this ticket. Cost XS; a
  one-time measurement, so a later change that gives the operator pod an ingress need (a webhook
  server) merges green.
- **E - CI (recommended):** the value on in `test/e2e/helm-values.yaml` with a test-pod label as
  peer, plus one e2e subtest with a negative control (outside pod refused, listed pod answered).
  Cost S. Every leg then runs the suite and the fleet-upgrade e2e under the policy. If CI does not
  enforce, the negative half needs a gate that no CI leg can set and runs only locally (effort
  toward M); the positive half still runs in CI.

E is recommended because it re-proves both halves on every PR and catches a new ingress need that
no other tier sees; its first CI run answers the one thing L would establish.

**Answer:** _open_

### Q4: Should the operator authenticate scrapes of its metrics endpoint (reopen ADR 0018 D10)? (metrics endpoint)

controller-runtime v0.25.1 can wrap `/metrics` with `WithAuthenticationAndAuthorization`
(TokenReview plus SubjectAccessReview, 401/403, cached), also on plain HTTP, where the scraper's
token then travels in clear. It imports `k8s.io/apiserver`, which `go.mod` does not have.

- **A - decline the filter, record it in ADR 0018 D10 (recommended).** Cost XS. The default
  install stays unauthenticated, as ADR 0018 D9 records as posture. Recommended because the
  NetworkPolicy (Q2) and the off switch (Q1) cover the same read path at far lower cost; B adds
  only a scraped endpoint on a non-enforcing CNI. If Q2 = C, revisit.
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
- Whether the production CNI enforces NetworkPolicy, how it treats node-originated and
  hostNetwork traffic and named ports, and whether the operator is installed through a Flux
  HelmRelease with drift detection. `kubectl get helmrelease -A` and a NetworkPolicy deny test
  settle the first and the last.
- Whether kindnet enforces inside CI's Docker-in-Docker runners (nfqueue, fail-open default):
  settled by the first CI run of Q3's negative control.
- Which `:8080` readers are not pods (an API-server pod-proxy read, a hostNetwork scraper) and so
  need an `ipBlock` peer: inferred from the documented semantics, not measured.
- For Q4 = B only: a Prometheus scrape against the self-signed certificate and an `h2`-only
  server, and whether the go1.27 HTTP/2 mitigations make an explicit HTTP/2 opt-out moot.

## Related

- [T43](043-static-checks-and-ci-gates-miss-tagged-tests-standing-constraints-and-chart.md) - the chart render gate that takes over the
  `helm template` cases.
- T40 - lists this ticket as the work behind H-13.
