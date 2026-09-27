---
id: T49
title: no NetworkPolicy guards the operator namespace
state: analysed       # every fact the decisions rest on is verified; enforcement in CI and production is what Q2 measures
severity: low         # disclosure of :8080 alone (fleet inventory, names and health, no Secret material, no write path)
security: hardening
threat: "would additionally cover any pod on the pod network, outside a configured peer list, that reads the operator pod's :8080 (the per-resource fleet inventory, ADR 0021); :8081 (answers only 'ok') and the operator's egress are deliberately not covered"
urgency: later        # rule 4: a default-off chart template is a cheap known fix
effort: S             # moves toward M if Docker-in-Docker Kind does not enforce and the Q2 subtest needs a gate
blocked-by: decision  # Q1, then Q2
filed-from: T31, section "Further security measures — not in this change, each open" (archive/031)
opened: 2026-09-27
decided:
done:
---

# T49 - no NetworkPolicy guards the operator namespace

The operator-facing statement of the gap is [H-14](../security/operator-pod-posture.md#h-14).

## Current state

- The chart renders no NetworkPolicy
  ([`templates/`](../../deploy/helm/valkey-operator/templates/); `helm template rel
  deploy/helm/valkey-operator --namespace vko` renders none).
- The operator pod declares `:8080` (metrics, the `vko_valkey_*` fleet inventory of
  [ADR 0021](../adr/0021-per-resource-metrics-and-the-alert-that-was-missing.md)) and `:8081`
  (health) ([`deployment.yaml`](../../deploy/helm/valkey-operator/templates/deployment.yaml)
  lines 57 and 60, bound at lines 37–38). `:8081` carries only `healthz.Ping`
  ([`cmd/main.go`](../../cmd/main.go) lines 188–195) and answers `ok` (with `?verbose` the check
  names); no pprof listener exists. The disclosure that matters is `:8080`.
- Nothing in this repository connects to the operator pod except kubelet: no webhook server
  (`cmd/main.go` lines 102–110), no webhook configuration, no conversion webhook. An
  ingress-only policy on the operator pod cannot cut reconciling. The users who connect are the
  scrapers of `:8080`.
- The operator's egress is the API server, DNS and the Valkey and Sentinel ports 6379, 16379,
  26379, 36379; its only dialer is
  [`internal/valkeyclient/client.go`](../../internal/valkeyclient/client.go) lines 398–401.
- The chart selector labels
  ([`_helpers.tpl`](../../deploy/helm/valkey-operator/templates/_helpers.tpl) lines 46–49) are
  also on the pre-upgrade hook pod
  ([`pre-upgrade-job.yaml`](../../deploy/helm/valkey-operator/templates/pre-upgrade-job.yaml)
  lines 19–20), which declares no `ports` and only lists and patches Valkey CRs.
- The workload NetworkPolicies the operator writes admit the whole operator namespace on the data
  and Sentinel ports ([`networkpolicy.go`](../../internal/builder/networkpolicy.go) lines 77–88,
  197–207; [ADR 0013](../adr/0013-operator-is-cluster-wide-privileged.md) D7). Their exporter
  port is open to every source (lines 129–143). Both are out of scope here.
- NetworkPolicy semantics that shape the policy: an ingress-isolated pod always admits its
  resident node and reply traffic; hostNetwork behaviour is undefined and CNI-specific; an
  ingress rule with an empty or missing `from` admits every source; named ports are allowed
  (`k8s.io/api` v0.37.1 `networking/v1/types.go` lines 122–125, 164–166).
- A compromised operator defeats any policy on its own pod: the chart ClusterRole grants
  `networkpolicies` full CRUD and `deployments`/`statefulsets` create/update/patch cluster-wide
  ([`clusterrole.yaml`](../../deploy/helm/valkey-operator/templates/clusterrole.yaml) lines
  73–85, 97–109). Egress filtering therefore protects nothing and is not an option.
- CI e2e runs kind v0.33.0 with kindnet, which enforces NetworkPolicy through
  kube-network-policies (nfqueue, fail-open by default, admits root-owned node traffic). CI loads
  no `nfnetlink_queue` (`release.yml` lines 102–115). No test checks that a NetworkPolicy is
  enforced, and no e2e reads `:8080` or `:8081`.

**Impact:** on every install, any pod on the pod network reads the fleet inventory on `:8080`.
An installer can write such a policy by hand; the chart does not offer one. On a CNI that does not
enforce NetworkPolicy no policy changes anything.

## Required changes

**Depends on Q1 (for A):**

- `templates/networkpolicy.yaml`, default off, one NetworkPolicy in the release namespace:
  `podSelector` = the chart selector labels (no `component` exclusion: a `podLabels` entry could
  take the operator pod out of its own policy), `policyTypes: [Ingress]`.
- Rule on named port `metrics` with `from` = a user-supplied `NetworkPolicyPeer` list (`toYaml`,
  so pod selectors, namespace selectors and `ipBlock` fit). **An empty list omits the rule**,
  never `from: []`. The rule is also omitted if [T48](048-the-operator-metrics-endpoint-is-unauthenticated.md)
  Q1 turns the endpoint off.
- Rule on named port `health` with no `from`, so the liveness of the single reconciler does not
  depend on how a CNI implements the node allowance.
- Render refusals in `_helpers.tpl`: an empty peer list together with `metrics.service.enabled`,
  `metrics.serviceMonitor.enabled` or `metrics.prometheusRule.enabled` fails the render (the same
  three values T48 refuses with the endpoint off).
- Values, named so they read as the operator pod's policy, for example
  `operatorNetworkPolicy.enabled` (default `false`) and `operatorNetworkPolicy.metricsFrom`
  (default `[]`); tell T48 the name.
- The template leaves the Deployment pod template untouched, so enabling it restarts nothing.
- README [Helm chart values](../../README.md#helm-chart-values) rows.
- H-14 in [`operator-pod-posture.md`](../security/operator-pod-posture.md) lines 100–105: name
  the value and state that egress is deliberately not filtered (the RBAC reason). H-13's
  mitigation sentence (lines 97–98) names the value.
- ADR: amend ADR 0013 D7 or write a new ADR (plus index line) recording the default-off policy,
  the refusal of egress filtering (RBAC reason), the refusal of a default-on policy (ADR 0018 D9
  calls the open endpoint posture), and the robustness reason for open health ports.
- Update [ADR 0018](../adr/0018-metrics-and-the-exporter-sidecar.md) Consequences lines 148–150
  (becomes false) and
  [ADR 0033](../adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md)
  lines 714–716 (lists this as open); ADR 0021 lines 155–157, `values.yaml` lines 106–111 and
  [monitoring.md](../operations/monitoring.md) lines 82–84 name the value.
- Render test: no policy by default, exactly one with the value on, the three refusals, the
  rendered Deployment byte-identical either way; add the cases to
  [T58](058-no-ci-gate-renders-the-chart.md)'s matrix.

**Depends on Q2:** the enforcement check. On an enforcing CNI: a non-hostNetwork pod outside the
peer list is refused on `:8080`, a listed pod is answered, with an empty list no pod reaches
`:8080`, an e2e Valkey resource reaches `OK`, and `helm upgrade` completes its pre-upgrade hook.

## Open questions

### Q1: Should the chart offer the operator pod's NetworkPolicy as a template, or only document an example?

The operator pod needs no ingress besides kubelet, so a policy restricting `:8080` to listed
scrapers costs reconciling nothing. The question is who writes and maintains the selector.

- **A - ingress-only chart template, default off (recommended).** As in Required changes. Cost S;
  the selector is the chart's own and the render refuses combinations that would break scraping.
- **C - documented example under `docs/operations/`, no template.** Cost XS. The copied selector
  depends on `nameOverride` and the release name and fails open silently when it stops matching;
  nothing refuses a scraper without a peer, and no render checks the snippet.

A is recommended because it closes H-14's `:8080` exposure from the chart with a selector that
cannot drift from the Deployment, for one template, two values and three checks more than C.

**Answer:** _open_

### Q2: Where is enforcement of the policy proven?

A green test without a refused request cannot tell "enforced" from "vacuous", and whether kindnet
enforces inside CI's Docker-in-Docker runners is unknown.

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

## Not verified

- Whether kindnet enforces inside CI's Docker-in-Docker runners (nfqueue, fail-open default):
  settled by the first CI run of Q2's negative control.
- How the production CNI treats node-originated and hostNetwork traffic to an ingress-isolated
  pod, and named ports: not visible from the repository.
- Which `:8080` readers are not pods (an API-server pod-proxy read, a hostNetwork scraper) and so
  need an `ipBlock` peer: inferred from the documented semantics, not measured.

## Related

- [T48](048-the-operator-metrics-endpoint-is-unauthenticated.md) - authentication and on/off of
  `:8080`; shares the named port and the three render refusals.
- [T58](058-no-ci-gate-renders-the-chart.md) - the render gate that should carry this ticket's
  render cases.
