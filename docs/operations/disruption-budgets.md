# PodDisruptionBudgets

What the opt-in budgets of `spec.podDisruptionBudget` protect against, what they do not,
and how the operator treats a budget it did not create. The two fields and their defaults
are in the [`spec.podDisruptionBudget`](../../README.md#specpoddisruptionbudget) table;
the decision is [ADR 0004](../adr/0004-opt-in-poddisruptionbudgets.md).

## Names and ownership

The budgets are named after the StatefulSets they cover — `<name>` for the data
pods and `<name>-sentinel` for Sentinel — live in the CR's namespace and are owned
by the CR, so deleting the CR removes them.

The ownerReference is also what the operator goes by: **a PodDisruptionBudget it
does not own is never deleted and never adopted**, even under exactly those names.
A hand-written budget for the same pods therefore survives every reconcile — the
operator leaves it untouched and records a `PodDisruptionBudgetNotOwned` Warning
Event on the CR instead. That Event is only recorded while
`spec.podDisruptionBudget.enabled` is `true`: a CR that never opted in leaves a
foreign budget alone silently, without a permanent warning stream. It **is**
recorded while the feature is on but not applicable to that StatefulSet (fewer
than two replicas, or Sentinel disabled): the name is taken, so scaling back up
would silently produce no budget at all. While such a budget exists,
`spec.podDisruptionBudget` has no effect for that StatefulSet — and
the operator suppresses the two content warnings below for it, because they would
describe values that never reached an object. Delete or rename the foreign budget
to hand the name over to the operator.

Both budgets are **opt-in**. The operator creates none unless the block is present
and `enabled: true` — a budget created next to a user-managed one would cover the
same pods twice, and the Eviction API refuses every eviction in that case.

## What the budgets cover

What the budgets do and do not cover:

- The **data PDB** uses `maxUnavailable` (default `1`), so a node drain takes one
  data pod at a time instead of the whole StatefulSet. Setting `maxUnavailable`
  to `spec.replicas` or higher removes the protection; the operator honours it and
  warns rather than rejecting a later scale-down. The warning is a log line plus a
  `PodDisruptionBudgetTooPermissive` Event on the CR, emitted on every reconcile
  while the condition holds — so scaling `spec.replicas` down into it (`5` -> `2`
  with `maxUnavailable: 2`) is reported too, even though the PDB object itself
  never changes.
- The **Sentinel PDB** uses `minAvailable = floor(spec.sentinel.replicas / 2) + 1`
  — the failover quorum. It is **computed, never configurable**: a settable value
  could silently break the guarantee that a drain cannot take the Sentinel majority.
  With `spec.sentinel.replicas: 2` the quorum equals the replica count, so **no
  voluntary disruption is permitted at all** — `kubectl drain` on a node hosting a
  Sentinel pod never finishes until the CR is scaled or the PDB removed. The formula
  stays (a smaller `minAvailable` would let a drain take automatic failover), and the
  operator makes the consequence visible: a log line plus a
  `SentinelPodDisruptionBudgetBlocksDrains` Event on the CR, emitted on every reconcile
  while the condition holds — including after scaling `spec.sentinel.replicas` `3` ->
  `2`, where the quorum stays `2` and the PDB object itself never changes. Use an odd
  Sentinel count of 3 or more; an even count is not HA in the first place.
- **StatefulSets with fewer than 2 replicas get no PDB**, even with `enabled: true`
  (data at `spec.replicas: 1`, Sentinel at `spec.sentinel.replicas: 1`). With one pod
  `maxUnavailable: 1` would permit evicting the only pod and `minAvailable: 1` would
  block `kubectl drain` forever — fake safety for an instance that is not HA either
  way. Scaling below 2 deletes an existing PDB; scaling back up recreates it.
- Budgets gate **voluntary** disruptions only (drain, cluster autoscaler, eviction
  API). Node failures, `kubectl delete pod` and the operator's own failover-aware
  rolling update are unaffected — the operator deletes pods directly.

## Example

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
  podDisruptionBudget:
    enabled: true          # default: false
    maxUnavailable: 1      # default: 1 (data StatefulSet only)
```

The operator needs `policy/poddisruptionbudgets` RBAC for this; the Helm chart
ships it unconditionally, so no permission change is needed to turn the feature on.
