# Pod anti-affinity

How `spec.antiAffinity` spreads the data and Sentinel pods of a cluster across nodes or
zones, and what each mode costs. The two fields and their defaults are in the
[`spec.antiAffinity`](../../README.md#specantiaffinity) table; why the feature is off by
default is [ADR 0005](../adr/0005-upgrade-neutral-defaults-and-anti-affinity.md).

## Modes

Anti-affinity is **opt-in**: omitting the block (or `mode: off`, the default)
renders no term at all, so upgrading the operator never changes how existing
clusters are scheduled. The flip side is that without an opt-in all pods of a
cluster may land on one node — a single drain then takes the whole data plane
down at once. **Multi-replica clusters should set `mode: soft` (or `hard`)**;
enabling it on a running cluster triggers one failover-aware rolling update. On a
multi-replica cluster the pre-roll dataset survives it; on a Sentinel cluster whose
Sentinels cannot run a coordinated failover (before Valkey 9.0, the 8 to 9 upgrade roll
included) and on any roll whose coordinated failover fell back to forced, the writes the
outgoing master acknowledges during the roll's failover are lost
([the master handover](rolling-updates.md#the-master-handover-on-a-sentinel-cluster)).
*(corrected 2026-09-28: this said "lossless for multi-replica clusters")*

- **`off`** (default) renders nothing. Scheduling is exactly what it was before
  the operator supported anti-affinity.
- **`soft`** renders `preferredDuringSchedulingIgnoredDuringExecution`
  with weight `100`, the strongest preference the scheduler weighs against its other
  priorities. Under node pressure pods may still be co-located, so the spread is a
  best effort, not a guarantee.
- **`hard`** renders `requiredDuringSchedulingIgnoredDuringExecution`. The spread is
  guaranteed, with two consequences worth knowing before enabling it: with fewer
  schedulable spread domains than replicas the surplus pods stay `Pending` (which
  also wedges the next rolling update), and during a node drain an evicted pod stays
  `Pending` until a domain without a pod of the same StatefulSet becomes schedulable.
  That is degraded but correct — the alternative is silently re-co-locating the pods.
- Each StatefulSet **repels only its own kind**, selected by
  `app.kubernetes.io/instance` + `app.kubernetes.io/managed-by` +
  `app.kubernetes.io/component`. Data and Sentinel pods may therefore share a node,
  and a second Valkey CR in the same namespace is unaffected.
- **StatefulSets with fewer than 2 replicas get no term** (data at
  `spec.replicas: 1`, Sentinel at `spec.sentinel.replicas: 1`): a singleton has no
  peer to repel, and injecting an empty term would change the pod-spec hash and
  restart the pod for nothing.
- Changing `mode` or `topologyKey` changes the pod-spec hash and therefore triggers
  the operator's failover-aware rolling update — on a multi-replica cluster the pre-roll
  dataset survives it, and the writes of its failover are lost where that failover is
  forced, as above. *(corrected 2026-09-28: this said "lossless for a multi-replica
  cluster")*

## Examples

```yaml
apiVersion: vko.gtrfc.com/v1
kind: Valkey
metadata:
  name: ha-valkey
spec:
  replicas: 3
  image: valkey/valkey:8.0
  sentinel:
    enabled: true
    replicas: 3
  antiAffinity:
    mode: soft                            # default: off (no term; opt in with soft or hard)
    topologyKey: kubernetes.io/hostname   # default: kubernetes.io/hostname
```

Spreading across availability zones instead of nodes is a `topologyKey` change:

```yaml
  antiAffinity:
    mode: hard
    topologyKey: topology.kubernetes.io/zone
```
