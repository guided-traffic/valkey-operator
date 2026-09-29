# NetworkPolicies

What the NetworkPolicies of `spec.networkPolicy` admit, what they leave to you, and how to admit
your applications and your Prometheus. The two fields and their defaults are in the
[`spec.networkPolicy`](../../README.md#specnetworkpolicy) table, the policy names in the README
[naming conventions](../../README.md#naming-conventions); the rule behind them is
[ADR 0039](../adr/0039-a-networkpolicy-admits-only-the-components-this-repository-deploys.md).

## What the policies admit

With `spec.networkPolicy.enabled: true` the operator writes one ingress policy for the data pods,
and one each for the Sentinel pods and the observer pod when those are enabled. Each one admits
traffic between the parts of the cluster the operator deploys, and nothing else:

| Policy selects | Port | Admitted from |
|---|---|---|
| The data pods | `6379`, and `16379` with `spec.tls.enabled` | the data pods of the same resource; its Sentinel pods and its observer pod, when enabled; the operator pod |
| The Sentinel pods | `26379`, and `36379` with `spec.tls.enabled` | the Sentinel pods and the data pods of the same resource; its observer pod, when enabled; the operator pod |
| The observer pod | — | nothing |

Not admitted, on an enforcing network plugin:

- **Your application clients**, on the data and Sentinel ports.
- **A scraper**, on the exporter port (`spec.metrics.port`, default `9121`) and on the observer's
  port `8084`.
- The sidecar health port `8082` and the observer port `8084` get no rule for kubelet either:
  its probes come from the pod's node, which a NetworkPolicy admits by definition. How a network
  plugin implements that exemption is its own; the Sentinel's TCP probe on `26379` (without TLS
  and auth) already depended on it before, since no rule ever admitted the node there.

Everything in that list is yours to admit, with a policy of your own (below). NetworkPolicies
add up — a pod selected by several policies accepts the union of their rules — so your policy
widens the generated one without touching it, and the operator never overwrites it.

The policies are **ingress only**: nothing restricts where the pods connect to.

### How the operator pod is recognised

The operator connects to the data and Sentinel ports for its health checks, so both policies
admit it — as a pod, not as a namespace: the operator's namespace **and** the labels that select
the operator pod alone. The chart passes both (`POD_NAMESPACE` and `--operator-pod-selector`)
and labels the operator pod `app.kubernetes.io/component: operator`, which the chart's
pre-upgrade hook pod does not carry. The chart refuses to render a `podLabels` or
`preUpgradeHook.podLabels` value that sets that key.

An operator started without either half — a hand-written manifest that sets neither — writes no
operator rule and logs `POD_NAMESPACE or --operator-pod-selector is unset` at startup. Where the
policies are enforced, it then cannot reach the pods it manages.
`config/manager/manager.yaml` in the repository sets both.

## Admitting your applications

A client in namespace `apps` labelled `app: shop` that talks to the `Valkey` resource `cache` in
namespace `db`, with Sentinel:

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: cache-clients
  namespace: db
spec:
  podSelector:
    matchLabels:
      app.kubernetes.io/instance: cache            # the resource name
      app.kubernetes.io/managed-by: vko.gtrfc.com
  policyTypes: [Ingress]
  ingress:
    - from:
        - namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: apps
          podSelector:
            matchLabels:
              app: shop
      ports:
        - port: 6379                               # 16379 with TLS
        - port: 26379                              # 36379 with TLS; Sentinel-aware clients only
```

`namespaceSelector` and `podSelector` in the **same** list entry admit pods that match both;
as two entries they would admit the whole `apps` namespace.

## Admitting a scraper

The exporter port of the data pods and the observer's `/metrics` on `8084`, from a Prometheus in
namespace `monitoring` labelled `app.kubernetes.io/name: prometheus`:

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: cache-scrape
  namespace: db
spec:
  podSelector:
    matchLabels:
      app.kubernetes.io/instance: cache
      app.kubernetes.io/managed-by: vko.gtrfc.com
      app.kubernetes.io/component: valkey
  policyTypes: [Ingress]
  ingress:
    - from:
        - namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: monitoring
          podSelector:
            matchLabels:
              app.kubernetes.io/name: prometheus
      ports:
        - port: 9121                               # spec.metrics.port
---
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: cache-observer-scrape
  namespace: db
spec:
  podSelector:
    matchLabels:
      app.kubernetes.io/component: observer
      vko.gtrfc.com/cluster: cache
  policyTypes: [Ingress]
  ingress:
    - from:
        - namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: monitoring
          podSelector:
            matchLabels:
              app.kubernetes.io/name: prometheus
      ports:
        - port: 8084
```

[monitoring.md](monitoring.md) has the rest of the scrape setup.

## Turning the policies off, and renaming them

Setting `spec.networkPolicy.enabled: false` deletes the policies the resource owns on the next
reconcile; so does turning Sentinel or the observer off for that component's policy, and so does
a changed `namePrefix` for the policies under the old names. A policy the resource does not own
— one you wrote under the same name included — is never deleted.

A policy under one of the generated names that the resource does not own is not overwritten
either: the operator records a `NetworkPolicyNotOwned` Warning Event, and for the data or
Sentinel policy reports `ReconcileBlocked` with reason `ForeignObject`, because the isolation the
resource asks for is then not in place. The observer's policy only records the Event.

## Upgrading

Earlier operator releases wrote wider rules: the sidecar health port, the
exporter port and the observer port admitted every source, and the data and Sentinel ports
admitted every pod of the operator's namespace. The upgrade rewrites existing policies on the
first reconcile. **A scraper that reached the exporter or the observer through the old rule
loses that access** until you admit it as above; so does anything else in the operator's
namespace that talked to the data or Sentinel ports. [upgrading.md](upgrading.md) lists it with
the rest of that release.

## What this does not cover

- **Enforcement is the network plugin's.** A plugin that does not enforce NetworkPolicy ignores
  every rule here, the ones you add included. Nothing in the operator checks.
- **A label is not an identity.** Any pod created in the resource's namespace with a component's
  labels is admitted as that component, and any pod in the operator's namespace with the operator
  pod's labels as the operator. Creating such a pod takes `create pods` in that namespace.
- **No egress rule is written** ([isolation and tenancy](../security/isolation-and-tenancy.md)).
