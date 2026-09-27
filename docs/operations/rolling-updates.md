# Rolling updates

What `spec.rollingUpdate.syncTimeout` bounds during a rolling update, and what happens
at each wait when it expires. The field and its default are in the
[`spec.rollingUpdate`](../../README.md#specrollingupdate) table; the conditions named here
are explained in [status.md](status.md). The decisions are
[ADR 0007](../adr/0007-failover-aware-rolling-update.md) (the failover-aware roll) and
[ADR 0010](../adr/0010-every-rolling-update-wait-is-bounded.md) (every wait is bounded).

## What `syncTimeout` bounds

`syncTimeout` bounds the two points in a rolling update where the operator waits
for a full dataset transfer, and one wait that has nothing to do with a transfer —
on a pod that does not come up at all. It does something different at each:

| Wait | On timeout |
|------|------------|
| A replaced replica syncing from the master, before the next pod is replaced | The wait ends and is **reported** (`RollingUpdatePaused` condition, phase `Error` for that pass). It is a report, not a halt: the pass clears the rolling-update state, so a later pass that still finds outdated pods starts the state machine again on a fresh `syncTimeout` budget, and the phase returns to `OK` as soon as the cluster is Ready. A spec change restarts the roll from the beginning. |
| The former master (pod-0) syncing back after the failover, before it is promoted again | The restoration is **abandoned**: the promoted replica stays master and the update finishes (`TopologyRestored=False`). |
| A pod the roll waits on that exists, is not being deleted and does not become available — an unpullable image, a container that crashes at boot, a request no node can schedule, a data volume the [`check-data-writable` pre-flight](persistence.md#the-data-writable-pre-flight) refuses | The wait **continues** and is **reported** (`PodAvailabilityStalled`, naming the pod; its message and what retracts it are in [status.md](status.md#podavailabilitystalled)). The budget is measured from the pod's own clock — when its `Ready` condition last turned false, or its creation if it has never been Ready (no `Ready` condition yet, or only the `Ready=False` kubelet stamps at its first status sync of the pod) — so neither an operator restart nor the moment a long-`Pending` pod is finally scheduled resets it. Nothing is deleted for it: a pod on the current spec comes back identical. Past the budget the reconcile pass stops ending on the wait, so the status write runs again; on a Sentinel cluster a holding data tier holds the Sentinel roll too, because both tiers run `spec.image` ([ADR 0026](../adr/0026-a-pod-being-deleted-is-not-available.md) D11). |

## The former master is never force-promoted

The second case never force-promotes pod-0. An unsynced pod-0 would come up as an
empty master and discard every write the promoted replica accepted since the
failover, so the operator gives up the canonical topology rather than the data.
The cluster stays fully usable — the `-rw`/`-r` Services select the master by
label, not by ordinal. Raise `syncTimeout` for large datasets whose initial sync
does not fit in five minutes.

## A pod that never comes up

The third case never lifts the wait, and it does not have to for a spec fix to take
effect: before replacing an **outdated** pod the roll asks only whether a pod of that
tier is terminating, and never waits for the outdated pod itself to become available
(the one exception, unchanged, is a leftover outdated second master, replaced only
while it is available). The waits on the *other* pods are unchanged: the previous
replacement's sync, the check on the new master before the former master is
replaced, and the Sentinel quorum guard for a delete that spends a vote. Replacing a
Sentinel that is not Ready spends none, so it goes ahead even when the quorum is already lost — after a spec
fix with two of three Sentinels stuck on the broken spec, it is the only way back to
a quorum, and the terminating-pod gate still takes those deletes one after the
other. The pod that never came up on the broken spec is outdated once the
spec is fixed, so the operator replaces it by itself, no `kubectl delete pod`
needed. The budget is shared: raising `syncTimeout` for a slow sync also delays this
report.
