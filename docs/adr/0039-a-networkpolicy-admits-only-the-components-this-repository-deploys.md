# ADR 0039: A NetworkPolicy this repository ships admits only the components this repository deploys

## Status

Accepted. Date: 2026-09-29.

Implemented 2026-09-29 for the policies the operator generates: D1, D2, D3, D5 and D6. The
generated policies admit the data and Sentinel ports from the deployed components only, open no
rule for the sidecar health, exporter and observer ports, and admit the operator as its pod
(`--operator-pod-selector` and `POD_NAMESPACE`, set by the chart and by `config/manager/`). Open:
D4's question whether the chart renders a policy for the operator pod at all — the chart ships
none today.

Amends in place: [ADR 0013](0013-operator-is-cluster-wide-privileged.md) D7 (the any-source rules
and the namespace-wide operator peer) and [ADR 0018](0018-metrics-and-the-exporter-sidecar.md) D6
(the exporter port is no longer opened) and its rejected alternative "Always open port 9121".

## Context

A NetworkPolicy is an administrator's instrument. An administrator who isolates workloads has a
plan for who may talk to whom in the cluster, and a policy a third-party chart or operator ships
either fits that plan or defeats it: many charts ship policies that, once enabled, still admit a
whole namespace or every source on some port, and an administrator who wants tight isolation can
then use none of them. NetworkPolicies are additive — a pod selected by several policies admits
the union of their rules (upstream `networking/v1` API semantics) — so a broad rule in a shipped
policy cannot be narrowed by the administrator's own policy, while a narrow shipped policy can
always be widened by one.

What this repository shipped before this record was implemented (read in the code, 2026-09-29):

- **The generated policies** (`internal/builder/networkpolicy.go`, written when
  `spec.networkPolicy.enabled`, default `false`) are ingress-only, one each for the data pods,
  the Sentinel pods and the observer.
  - The data port (6379, 16379 under TLS) admits the cluster's own data, Sentinel and observer
    pods, and **every pod of the operator's namespace** (`namespaceSelector` on
    `kubernetes.io/metadata.name`, no pod selector, `BuildValkeyNetworkPolicy`). The Sentinel
    port (26379, 36379) admits the same peers (`BuildSentinelNetworkPolicy`).
  - Three ports admit **every source**, a rule with no `from`: the sidecar health port 8082,
    the exporter port `spec.metrics.port` (default 9121) while metrics are enabled, and the
    observer port 8084, which serves its health endpoints and its own `/metrics`
    (`BuildObserverNetworkPolicy`). The reasons recorded in ADR 0013 D7: kubelet probes come
    from the node, and Prometheus is not locatable from the CR.
  - No rule admits an application client, so on an enforcing CNI a client reaches the data and
    Sentinel ports only through a policy the administrator adds — except a client that runs in
    the operator's namespace. No page says so (ADR 0035 D3 records the missing operations page).
  - The operator is identified by namespace alone, from `POD_NAMESPACE` (`cmd/main.go`), which
    the chart sets through the downward API and the kustomize manifest in `config/manager/`
    does not set at all — there the operator peer is missing.
- **The chart** (`deploy/helm/valkey-operator`) ships no NetworkPolicy; `values.yaml` tells the
  installer to write one for the operator's metrics endpoint.

Upstream API semantics (read in `k8s.io/api` `networking/v1/types.go`): traffic from the pod's own
node is admitted whatever the policy says, so kubelet probes need no rule. How a given CNI
implements that is CNI-specific and not verified here.

## Decision

**D1 — A NetworkPolicy this repository ships admits only traffic between the components this
repository deploys.** That holds for every policy the operator generates for a `Valkey` resource
and for every policy the chart renders. The components are the data pods (the Valkey container,
its sidecar and, with metrics, its exporter), the Sentinel pods, the observer and the operator
pod. No shipped rule admits anything else: no application client, no scraper, no ingress
controller, no namespace as a whole, no rule without a `from`, no `ipBlock`.

**D2 — Every rule names the source component and the port that component uses, and nothing
wider.** A peer is a pod selector on the component's selector labels in the resource's namespace.
The operator is admitted as the operator pod — its namespace **and** a pod selector that matches
the operator pod alone, not the pre-upgrade hook and not any other pod of that namespace —
never as a namespace. A port no deployed component connects to gets no rule: the exporter port,
which only an external scraper reads; the sidecar health port and the observer port, which only
kubelet reads, and kubelet's traffic comes from the node and needs none.

**D3 — Access from outside the deployed components is the administrator's to grant.**
Application clients of the data and Sentinel ports, a Prometheus scraping the exporter, the
observer or the operator's own metrics endpoint, and anything else that is not a deployed
component are admitted by a NetworkPolicy the administrator writes; policies being additive, it
adds to the shipped ones without touching them. The documentation names the ports a client and a
scraper use and shows such a policy as an example; neither the CRD nor the chart takes a peer
list for it.

**D4 — Shipped policies stay opt-in.** Isolation is the administrator's decision:
`spec.networkPolicy.enabled` stays default `false`, and a policy the chart renders is off by
default. Whether the chart renders one for the operator pod at all is not decided here; if it
does, it follows D1 and D2 — with no deployed component connecting to the operator pod, such a
policy admits nothing but the node.

**D5 — The rules reach every cluster that has its policies enabled, with the operator upgrade.**
A shipped rule wider than D1 and D2 allow is a defect of the isolation the resource asked for,
not a feature, so there is no switch to keep the old rules
([ADR 0005](0005-upgrade-neutral-defaults-and-anti-affinity.md) D1 governs features, not the
repair of a defect). An existing policy is rewritten on the first pass: the removed rules are an
`Ingress` difference, which `NetworkPolicyHasChanged` compares.

**D6 — A generated policy the spec no longer asks for is deleted.** Every pass lists the
NetworkPolicies in the resource's namespace and deletes each one this `Valkey` controls whose
name the spec no longer produces: all of them once `spec.networkPolicy.enabled` is off, the
Sentinel or observer policy once that component is off, and the policies under the old names
after `spec.networkPolicy.namePrefix` changes. Until this record the step ran only while the
policies were enabled, so turning them off left the last written policies in place, isolating
the pods with rules the operator no longer updated — and D5 could never reach a cluster that
turned its policies off and on again. The controller reference is the proof, never a name or a
label, and the delete carries the UID precondition
([ADR 0006](0006-delete-only-what-the-operator-owns.md) D8, D9); a policy another object
controls is never touched.

## Consequences

- **A scraper that reached the exporter or the observer through the old any-source rule loses
  that access at the upgrade**, on every cluster with `spec.networkPolicy.enabled`, until the
  administrator admits it. The release notes and `docs/operations/upgrading.md` say so; the
  operations page for `spec.networkPolicy` that ADR 0035 D3 still misses states what the
  generated policies admit, that clients and scrapers are not among them, and gives the example
  of D3.
- **Clients are unaffected by the change and are now documented**: they were never admitted
  except from the operator's namespace, which loses that accidental access with D2.
- **The operator needs a pod selector it can put into the policies.** The chart labels the
  operator pod `app.kubernetes.io/component: operator` — the pre-upgrade hook pod carries
  `pre-upgrade-hook` — and passes the selector labels plus that one as
  `--operator-pod-selector`, next to `POD_NAMESPACE` from the downward API; the render fails when
  `podLabels` or `preUpgradeHook.podLabels` sets the component key, which would take the operator
  out of its own peer or put the hook into it. `config/manager/manager.yaml` passes the same
  label, flag and variable. The operator writes no operator peer when either half is missing and
  logs that at startup; the peer carries both selectors in one entry.
- **Turning the policies off deletes them** (D6), so the first pass after the flag goes off
  lifts the isolation instead of freezing it.
- **Nothing proves enforcement in CI.** The Kind clusters of CI run kindnet, reported to fail open
  without the netfilter queue module (not re-verified here); no e2e test asserts that a
  NetworkPolicy blocks anything. A test of the rules needs an enforcing CNI.
- The unit tests pin the absence of any rule outside the data and Sentinel ports, the absence
  of a rule without a `from` and of a namespace-only peer, the operator peer's two selectors,
  and that half an operator identity writes no operator peer
  ([`networkpolicy_test.go`](../../internal/builder/networkpolicy_test.go)); the controller tests
  pin the rewrite of a policy with the old rules and the deletions of D6.

## Alternatives Considered

**Keep the any-source rules for the health, exporter and observer ports** (ADR 0013 D7 as it
stood). Rejected: an any-source rule admits every pod of the cluster to that port, which is the
breadth that makes shipped policies unusable for an administrator who isolates, and no deployed
component needs it — kubelet comes from the node.

**A peer list for scrapers in the CRD or the chart values** (for example `metricsFrom`).
Rejected: it duplicates NetworkPolicy's own API inside ours, invites a broad default, and the
administrator who knows where the scraper runs can write that policy directly, additively.

**Admit the operator by its namespace** (as today). Rejected: every pod of that namespace —
including anything another team runs there — reaches the data and Sentinel ports.

**Ship policies on by default.** Rejected: isolation is the administrator's decision, and a
default-on policy blocks every client of a cluster whose administrator has no plan for it yet.

## Residual risks

- **Egress is not decided here.** The shipped policies stay ingress-only (open gap H-10 of
  [isolation-and-tenancy.md](../security/isolation-and-tenancy.md#h-10)). An egress policy for
  the deployed components would also have to admit DNS and, for the sidecar, the Kubernetes API
  server — neither a deployed component — so it needs its own decision.
- **Node traffic is admitted by the API definition, not verified per CNI.** A CNI that does not
  exempt the node would fail the sidecar's and the observer's HTTP probes once their rules are
  gone; the Sentinel's TCP probe on 26379 already relies on that exemption today.
- **A label is not provenance.** Any pod created in the resource's namespace with a component's
  selector labels is admitted as that component; that needs `create pods` in that namespace, a
  principal the rest of the threat model already treats as able to reach the data plane.
- **The operator pod selector depends on the install path setting it.** An install that neither
  labels the operator pod nor passes the selector leaves the operator without a peer; the
  operator logs it at startup and nothing else reports it.
- **Not verified:** which CNIs in the fleet enforce NetworkPolicy; whether any reader of the
  exporter or observer ports other than a scraper exists in a deployment.

## References

* [`internal/builder/networkpolicy.go`](../../internal/builder/networkpolicy.go) —
  `BuildValkeyNetworkPolicy`, `BuildSentinelNetworkPolicy`, `BuildObserverNetworkPolicy`,
  `NetworkPolicyHasChanged`
* [`internal/controller/valkey_controller.go`](../../internal/controller/valkey_controller.go) —
  `reconcileNetworkPolicies`, `reconcileNetworkPolicy`
* [`internal/controller/valkey_controller.go`](../../internal/controller/valkey_controller.go) —
  `cleanupNetworkPolicies` (D6)
* [`cmd/main.go`](../../cmd/main.go) — `POD_NAMESPACE`, `--operator-pod-selector`
* [`deploy/helm/valkey-operator/templates/_helpers.tpl`](../../deploy/helm/valkey-operator/templates/_helpers.tpl) —
  `valkey-operator.operatorComponentLabel`, `valkey-operator.operatorPodSelector`
* [`config/manager/manager.yaml`](../../config/manager/manager.yaml) — the kustomize install path
* [docs/operations/network-policy.md](../operations/network-policy.md) — what the policies admit,
  and the example of D3
* [`deploy/helm/valkey-operator/templates/deployment.yaml`](../../deploy/helm/valkey-operator/templates/deployment.yaml),
  [`values.yaml`](../../deploy/helm/valkey-operator/values.yaml)
* [ADR 0005](0005-upgrade-neutral-defaults-and-anti-affinity.md) D1 — features opt-in, defect
  repairs fleet-wide
* [ADR 0013](0013-operator-is-cluster-wide-privileged.md) D7 — the rule this record amends
* [ADR 0018](0018-metrics-and-the-exporter-sidecar.md) D6 — the exporter port rule this record
  amends
* [ADR 0020](0020-write-only-what-the-operator-owns.md) D2 — a foreign policy under a generated
  name fails the step
* [ADR 0035](0035-the-readme-advertises-the-reference-lives-under-docs.md) D3 — the missing
  operations page for `spec.networkPolicy`
* [docs/security/isolation-and-tenancy.md](../security/isolation-and-tenancy.md) — what the
  policies hold today, H-10
