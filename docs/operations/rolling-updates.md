# Rolling updates

What `spec.rollingUpdate.syncTimeout` bounds during a rolling update, what happens
at each wait when it expires, and how a Sentinel cluster hands its master over. The field
and its default are in the
[`spec.rollingUpdate`](../../README.md#specrollingupdate) table; the conditions named here
are explained in [status.md](status.md). The decisions are
[ADR 0007](../adr/0007-failover-aware-rolling-update.md) (the failover-aware roll),
[ADR 0010](../adr/0010-every-rolling-update-wait-is-bounded.md) (every wait is bounded) and
[ADR 0037](../adr/0037-the-master-handover-loses-no-acknowledged-write-and-no-dataset.md)
(the master handover).

## What `syncTimeout` bounds

`syncTimeout` bounds the two points in a rolling update where the operator waits
for a full dataset transfer, one wait that has nothing to do with a transfer —
on a pod that does not come up at all — and, on a Sentinel cluster, the hold in front
of the delete of the outgoing master. It does something different at each:

| Wait | On timeout |
|------|------------|
| A replaced replica syncing from the master, before the next pod is replaced | The wait ends and is **reported** (`RollingUpdatePaused` condition, phase `Error` only until the next status write, normally later in the same pass *(corrected 2026-09-27: this said "for that pass")*). It is a report, not a halt: the pass clears the rolling-update state, so a later pass that still finds outdated pods starts the state machine again on a fresh `syncTimeout` budget, and the phase returns to `OK` as soon as the cluster is Ready. A spec change restarts the roll from the beginning. |
| The former master (pod-0) syncing back after the failover, before it is promoted again | The restoration is **abandoned**: the promoted replica stays master and the update finishes (`TopologyRestored=False`). |
| A pod the roll waits on that exists, is not being deleted and does not become available — an unpullable image, a container that crashes at boot, a request no node can schedule, a data volume the [`check-data-writable` pre-flight](persistence.md#the-data-writable-pre-flight) refuses | The wait **continues** and is **reported** (`PodAvailabilityStalled`, naming the pod; its message and what retracts it are in [status.md](status.md#podavailabilitystalled)). The budget is measured from the pod's own clock — when its `Ready` condition last turned false, or its creation if it has never been Ready (no `Ready` condition yet, or only the `Ready=False` kubelet stamps at its first status sync of the pod) — so neither an operator restart nor the moment a long-`Pending` pod is finally scheduled resets it. Nothing is deleted for it: a pod on the current spec comes back identical. Past the budget the reconcile pass stops ending on the wait, so the status write runs again; on a Sentinel cluster a holding data tier holds the Sentinel roll too, because both tiers run `spec.image` ([ADR 0026](../adr/0026-a-pod-being-deleted-is-not-available.md) D11). |
| On a Sentinel cluster, the handover checks on the new master, the replicas on the current spec and the pod about to be deleted, before the outgoing master is deleted | The delete stays **held** and is **reported** (`MasterHandoverStalled`, one Warning Event). No timeout releases the delete and the roll is not paused: the rolling-update state is kept, the reconcile pass stops ending on the wait so the status write runs again, and the Sentinel roll waits for the data tier. The checks, the reasons and the repair are [below](#the-delete-of-the-outgoing-master-is-held-never-timed-out). |

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
replacement's sync, [the handover checks](#the-delete-of-the-outgoing-master-is-held-never-timed-out)
before the former master is replaced, and the Sentinel quorum guard for a delete that spends a vote. Replacing a
Sentinel that is not Ready spends none, so it goes ahead even when the quorum is already lost — after a spec
fix with two of three Sentinels stuck on the broken spec, it is the only way back to
a quorum, and the terminating-pod gate still takes those deletes one after the
other. The pod that never came up on the broken spec is outdated once the
spec is fixed, so the operator replaces it by itself, no `kubectl delete pod`
needed. The budget is shared: raising `syncTimeout` for a slow sync also delays this
report.

## The master handover on a Sentinel cluster

A Sentinel cluster's roll hands the master over once, when every replica runs the
current spec: it waits until every replica answers that it holds the dataset and a
`WAIT` on the master has been acknowledged, asks Sentinel for a failover, waits for the
promoted pod and then deletes the former master.

**The first failover it asks for is coordinated** — `SENTINEL FAILOVER <name>
COORDINATED`. Sentinel then holds an election, selects the replica and has the
outgoing master hand over itself: it pauses writes, waits until the replica has caught
up, becomes that replica's replica at once and hands over. A client whose write was
paused is disconnected without a reply, so it was never told the write succeeded, and
nothing the outgoing master acknowledged is lost (upstream behaviour read from the
Valkey 9.1.1 source and measured in docker, [ADR 0037](../adr/0037-the-master-handover-loses-no-acknowledged-write-and-no-dataset.md)
D1).

**Where that is not possible the failover is forced**, the command every roll sent
before: Sentinel promotes the replica and tells the outgoing master nothing, so it keeps
acknowledging writes through the `-rw` Service until Sentinel converts it — about 16 s
after the promotion, measured in docker — and its resync from the new master then
discards them. The pre-roll dataset survives either way. The forced command remains in
three cases:

- **A Sentinel that refuses the option.** A Sentinel before Valkey 9.0 does not know it
  (`ERR wrong number of arguments`), and a Sentinel whose master reports no failover
  state answers `NOGOODPRIMARY`. The operator asks that Sentinel for the forced failover
  at once, and every Sentinel after it in the same pass. Every roll of a Valkey 8
  cluster is therefore forced, and so is the roll that moves `spec.image` from Valkey 8
  to 9: the Sentinel tier runs `spec.image` too and rolls after the data tier, so the
  data tier's failover is asked of Sentinels still on Valkey 8. The first coordinated
  roll is the one after it. Any other refusal — a failover already in progress, no
  eligible replica — or an unreachable Sentinel is a failed attempt, and the next
  Sentinel is asked the same command.
- **A lost election.** A coordinated failover whose Sentinel does not win the election
  promotes nothing; Sentinel gives up after 10 s. The operator's 30 s retry then resets
  Sentinel and asks again, forced, so a failover that keeps losing its election cannot
  loop. That costs the roll about 55 s. Dead entries in a Sentinel's peer table count
  toward the majority the election needs — one more reason to clear a standing
  [`SentinelPeersStale`](status.md#sentinelpeersstale).
- **A master deleted outside a roll** — an eviction, a node drain — is failed over by
  its sidecar with the forced command: a write pause of up to 60 s would use up most of
  the pod's 75 s grace period.

The operator log names the command each failover used: `Sentinel failover triggered
successfully` carries `failoverMode` (`coordinated` or `forced`) and, after a fallback,
`fallbackReason` with the Sentinel's reply.

**What the coordinated failover costs is a block instead of a loss.** In its stall
shape — the selected replica not online when Sentinel acts, or never catching up — the
outgoing master holds every write for up to Sentinel's `failover-timeout` (60 s) and
then aborts: clients see a hang and a disconnect, not an error. The abort promotes nothing,
so the roll then falls back to its forced retrigger, and that failover loses its own window's
writes like any forced one (read from the code, not measured). The
checks in front of the trigger make the replica online and caught up, so the stall needs
the cluster to change between those checks and the trigger. On Kubernetes the
coordinated failover is measured on a single-node Kind cluster with three Sentinels and no
TLS: no acknowledged write lost on Valkey 9, where the forced failover lost about ten seconds
of writes; TLS replication, a lost election and the stall are not verified
([ADR 0037](../adr/0037-the-master-handover-loses-no-acknowledged-write-and-no-dataset.md)
*Residual risks*).

## The delete of the outgoing master is held, never timed out

**Before the former master is deleted, the roll asks each pod the question it can
answer.** On a Sentinel cluster only; the delete waits until all four hold:

- **The new master** — the first available pod on the current spec that answers
  `role:master` — has at least as many replicas attached as there are other pods on the
  current spec that exist and are not terminating, and at least one, and its key count
  can be read. While no such pod answers master yet, the roll is still waiting for the
  failover above, not holding the delete.
- **Each of those other pods** answers as a replica, with its link to the master up and
  no sync in progress. The replica is asked, not the master: a master counts a replica
  as attached from its sync request on, while it still holds nothing.
- **The delete discards no dataset.** When the new master holds no keys, the pod about
  to be deleted must hold none either, and a count that cannot be read holds the delete
  ([ADR 0028](../adr/0028-a-demotion-may-not-discard-the-only-dataset.md)).
- **The pod about to be deleted no longer answers `role:master`**; a pod that does not
  answer passes. After a forced failover that is the seconds until Sentinel has
  converted it; deleting it while it still answers master would let its sidecar ask
  Sentinel for a second failover (measured once, in docker).

While one of them fails the delete waits, re-checked every 10 s. Past `syncTimeout`,
counted from the first held pass, the wait is reported as
[`MasterHandoverStalled`](status.md#masterhandoverstalled) with one Warning Event, and
**the hold continues**: deleting that pod is the one step of the roll that cannot be
undone, so no timeout releases it and the roll is not paused. The rolling-update state
is kept, the reconcile pass stops ending on the wait so the status write runs again, and
the Sentinel roll waits for the data tier
([ADR 0026](../adr/0026-a-pod-being-deleted-is-not-available.md) D11). The reason names
the check that fails:

| Reason | What ends the hold |
|---|---|
| `ReplicaNotSynced` | Every replica on the current spec completes its sync from the new master and is attached to it; the delete then goes through by itself. A dataset whose full sync takes longer than `syncTimeout` is reported before it is done — raise `syncTimeout` for it. |
| `DatasetWouldBeDiscarded` | The new master holds no keys while the pod about to be deleted may hold the only dataset — the refusal shape of [ADR 0028](../adr/0028-a-demotion-may-not-discard-the-only-dataset.md). **To try to keep that dataset**, point Sentinel at the outgoing pod (below): the operator may then demote the empty new master, which resyncs from the outgoing pod, and the roll hands over again — through its forced retrigger, which does **not** wait for that resync. A dataset whose full sync takes longer than the retrigger's wait (20 s, up to 90 s while Sentinel has not rediscovered the replicas) can still be lost: Sentinel may promote the still-empty pod and convert the outgoing pod onto it. **A write that reaches the new master first also ends the hold, and discards the outgoing pod's dataset.** |
| `FormerMasterStillMaster` | Sentinel normally converts a former master within seconds of the failover. Check with `SENTINEL REPLICAS <name>` that every Sentinel still lists it; the delete goes through once it answers as a replica. |

**Pointing Sentinel at the outgoing pod** means, on every Sentinel pod, the sequence the
operator itself sends when it re-points Sentinel (`resetSentinelState`), with the
outgoing pod as the master:

```bash
# example: <name> is the Valkey resource, <pod> the outgoing pod, on every Sentinel pod
SENTINEL REMOVE <name>
SENTINEL MONITOR <name> <pod>.<name>-headless.<namespace>.svc.cluster.local <port> <quorum>
SENTINEL SET <name> down-after-milliseconds 5000
SENTINEL SET <name> failover-timeout 60000
SENTINEL SET <name> parallel-syncs 1
SENTINEL SET <name> auth-pass <password>        # only with spec.auth
```

`<port>` is `6379`, or `16379` with TLS; `<quorum>` is `spec.sentinel.replicas / 2 + 1`,
rounded down (2 for three Sentinels).
`SENTINEL REMOVE` drops every setting Sentinel held for that master, which is why the
`SET` lines follow — without `auth-pass` a Sentinel cannot reach a password-protected
master. The roll then hands over again through its forced retrigger, because its state
still records the first failover, so the writes the outgoing pod acknowledges during
that failover are lost. The whole repair is read from the code — the split-brain
resolver, the roll and the reset — and has not been driven on a cluster.

The roll of a cluster without Sentinel has no such hold: it refuses before its
promotion instead, when the candidate holds no keys and the master holds some
([ADR 0007](../adr/0007-failover-aware-rolling-update.md) D10), and deletes its outgoing
master right after the promotion.
