# ADR 0037: The master handover loses no acknowledged write and no dataset

## Status

Accepted. Date: 2026-09-28.

Implemented: nothing yet. Every rule below is decided and outstanding. The e2e writer harness
(*Consequences*) runs first, on both Valkey lines, before any of D1–D7 lands, so the loss this
record measured in docker is measured on Kubernetes once and the fix is measured against it. The
order of the work after that: D1; then D2 and D3 together with D6 and D7 (one gate, one hold);
then D4 and D5; D8 rides every step.

Amends in place: [ADR 0025](0025-a-split-brain-warning-means-one-that-did-not-resolve-itself.md)
D9 (the roll's own failover is coordinated where Sentinel supports it), [ADR 0007](0007-failover-aware-rolling-update.md)
D1 and D10 (the failover command; the replica-side predicate binds the delete),
[ADR 0028](0028-a-demotion-may-not-discard-the-only-dataset.md) D1, D3, D4 and D8 (the veto
binds `forceReplicaConnections` and the delete; the divergence on the Sentinel path is held, not
bounded) and [ADR 0026](0026-a-pod-being-deleted-is-not-available.md) D11 (the *Replacement*
argument's gate). [ADR 0038](0038-the-operator-does-not-offer-min-replicas-to-write.md) is the
companion refusal: no write fence.

## Context

A Sentinel data-tier roll hands the master over once. With every replica on the new template it
forces a Sentinel failover, waits for the promoted pod (X) to have a replica, and deletes the
former master (O). Each step could lose writes a client had already been acknowledged, or the
dataset of the only pod holding it, and fifteen tracked texts called the roll lossless. Measured
in docker on `valkey/valkey:9.1.1` and `8.1.9` with the operator's settings, 2026-08 to 2026-09:

- **The failover was forced.** A plain `SENTINEL FAILOVER <name>` sets `SRI_FORCE_FAILOVER`: no
  election, a `REPLICAOF NO ONE` to X, and O is told nothing
  ([sentinel.c 9.1.1:3923-3960](https://github.com/valkey-io/valkey/blob/9.1.1/src/sentinel.c#L3923-L3960)).
  O keeps its master label and the `-rw` endpoint and acknowledges writes until Sentinel has seen
  it report `master` for 8 s (`4 × publish_period`) while listed as a replica and sends
  `+convert-to-slave` ([:2630-2641](https://github.com/valkey-io/valkey/blob/9.1.1/src/sentinel.c#L2630-L2641));
  then O full-resyncs from X and discards them. Two replicas, one Sentinel, 500-600 writes/s:
  9133-11249 of 13271 acknowledged writes lost on 9.1.1 (four runs), 8633 and 9510 on 8.1.9;
  `+convert-to-slave` 16.3-16.4 s after `+promoted-slave`. The operator's `WAIT` before the
  trigger covers only writes before it. `SENTINEL FAILOVER <name> COORDINATED` (Valkey 9.0 and
  later; 8.1.9 answers `ERR wrong number of arguments`): 0 lost in eight runs, O writable for
  0.62-1.4 s, O `role:slave` at once.
- **The second failover.** Nothing asked O its role before the delete. On the forced path the
  delete SIGTERMs O's sidecar while O still answers `master`, and its drain handler sends the
  same forced command: Sentinel's master is X by then, so X is failed over to a third replica
  (measured once: `OK`, the other replica promoted 1.14 s later).
- **The sync checks read a replica-only field on the master.** `master_sync_in_progress` exists
  only on a replica ([server.c 9.1.1:6506-6525](https://github.com/valkey-io/valkey/blob/9.1.1/src/server.c#L6506-L6525));
  on a master `parseReplicationInfo` leaves it `false`, and `connected_slaves` counts a replica
  from its sync request, while it holds nothing (6-8 s empty on 1.5 M keys; after a forced
  failover under writes the other replica full-resyncs, counted from +0.7 s, link up at +8 s).
  `CheckCluster` (`AllSynced`, phase `Syncing`), the observer's `checkReplicaSync` and the delete
  gate `verifyNewMasterReady` all asked the master. The replica-side answer existed and was
  already the rule in front of every promotion ([ADR 0007](0007-failover-aware-rolling-update.md)
  D10, `replicationNotEstablishedReason`: role replica, `master_link_status:up`, no sync in
  progress); nothing asked it before the delete.
- **The delete gate accepted an empty new master.** `verifyNewMasterReady` read X's `DBSIZE`,
  logged it and refused only an unreadable count; O's count was never read. In the refusal
  shape of [ADR 0028](0028-a-demotion-may-not-discard-the-only-dataset.md) — X empty and
  Sentinel's authority, O holding the dataset and still answering `master` — any attach to X let
  the gate pass and O was deleted: non-persistent, its memory gone; persistent, it rebooted as
  X's replica and full-synced `/data` away. The roll ended `RollingUpdateComplete`, phase `OK`.
- **`forceReplicaConnections` re-pointed holders at an empty master.** After 90 s without a
  replica on X (`handleMasterWithNoReplicas`) it sent `REPLICAOF X` to every reachable pod, O
  included, asking no role and reading no count: a 500-key master and its replica were empty by
  +7 s; O with the operator's `rdb` or `aof` lines, re-pointed and restarted, booted master with
  `DBSIZE` 0 in four of four runs. It performed exactly the demotion the resolver had refused.

Three routes flush O in that refusal shape, and only one is the operator's. Sentinel itself
converts O 8 s after `+switch-master` while it still lists O and X looks sane, which no veto
reaches; the operator's `REPLICAOF` is live once Sentinel has forgotten O (after the reset that
follows the same branch: `SENTINEL REMOVE` + `MONITOR X`, and Sentinel learns replicas from X's
`INFO` alone); and the delete. Meanwhile the labeler trusts Sentinel over the local role
([`labeler.go`](../../internal/sidecar/labeler.go)), so O labels itself `replica`: `-rw`
selects the empty X alone, `-r` selects O, and every client write lands on X. The shape is
narrow — a forced failover promotes the replica with the highest offset
(`compareReplicasForPromotion`), so an empty one only when it is the only eligible replica or
all are empty, or when a promoted non-persistent pod restarts empty — and inside it the veto of
[ADR 0028](0028-a-demotion-may-not-discard-the-only-dataset.md) D1 is zero-against-some: the
first write on X lifts it.

## Decision

**D1 — The roll's own Sentinel failover is coordinated where Sentinel supports it, and forced
otherwise.** The first trigger (`handleMasterFailover`) sends `SENTINEL FAILOVER <name>
COORDINATED`. Sentinel then holds an election, selects the replica and sends the master one
transaction — `CLIENT PAUSE <rest of failover-timeout> WRITE` and `FAILOVER TO <X> <port>
TIMEOUT <same>` ([sentinel.c 9.1.1:4762-4800](https://github.com/valkey-io/valkey/blob/9.1.1/src/sentinel.c#L4762-L4800)):
O blocks writes, waits for X's ack offset to reach its own, becomes X's replica and hands over
with `PSYNC FAILOVER` ([replication.c 9.1.1:5596-5760](https://github.com/valkey-io/valkey/blob/9.1.1/src/replication.c#L5596-L5760));
the paused clients are disconnected unacknowledged, never answered. Fallbacks, each exactly
today's command: on `ERR wrong number of arguments` (a Sentinel before Valkey 9.0) or
`-NOGOODPRIMARY` (the master reports no `master_failover_state`) the same Sentinel is asked with
the forced command at once; `-INPROG` and `-NOGOODSLAVE` stay a failed attempt; an `OK` whose
election is lost (`-failover-abort-not-elected` after 10 s, no majority of the Sentinel table)
promotes nothing and falls back through `failoverRetryTimeout` (30 s), reset and the forced
retrigger. The retrigger (`handleFailoverRetrigger`) and the sidecar's drain handler stay
forced: the first because a lost election would otherwise loop, the second because it runs
inside a 75 s grace period a 60 s stall would consume. The `WAIT` gate before the trigger stays:
it keeps the pause short and remains the gate for the forced fallback. The upgrade roll from
Valkey 8 to 9 is itself forced — the Sentinel tier shares `spec.image` and rolls after the data
tier — so the first lossless roll is the next one.

**D2 — "Synced" is the replica's full replication answer at every site that says it.** The
predicate of `replicationNotEstablishedReason` — role not master, `master_link_status` `up`, no
sync in progress — moves onto `valkeyclient.ReplicationInfo`, and `isSyncedReplica` (sidecar),
`CheckCluster` (`AllSynced`, `ReadyReplicas`) and the observer's `checkReplicaSync` are built on
it. `CheckCluster` evaluates it on the replies `findMaster` already collects from every Running
pod, keeping them indexed by ordinal instead of discarding the non-master ones, at no extra
dial; the observer asks each data pod, one `INFO` per replica per cycle. The master's
`master_sync_in_progress` is read nowhere but the parser. `Ready` therefore reports
`False`/`ReplicationSyncing` and the phase `Syncing` during every replica full sync, after an
eviction or a restart too, which is what [ADR 0002](0002-surface-a-blocked-reconcile-on-the-cr.md)
D5a and the `Ready` text in [status.md](../operations/status.md) already state ("replicating";
a replica in full sync is not, and `-r` routes reads to it because the probe asks `PING` only).
No opt-in ([ADR 0005](0005-upgrade-neutral-defaults-and-anti-affinity.md) D1): this corrects a
status defect, it adds no feature.

**D3 — The delete of the outgoing master waits until the dataset is on every current replica
and every current replica is on the new master.** `verifyNewMasterReady` returns the master it
verified and asks the D2 predicate of every other pod on the current template that exists and is
not terminating, **and** requires X's `connected_slaves` to be at least their number. Neither
alone proves the handover: the count on X proves the attachment (a replica chained through O
passes the predicate, Sentinel reconfigures with `parallel-syncs 1`), the predicate proves the
dataset (`connected_slaves` counts a replica from its sync request). `master_host` is not
compared: the init script writes two FQDN forms. The gate arms the sync-wait bound
(`ensureSyncWaitTimestamp`); past `spec.rollingUpdate.syncTimeout` it holds as D6 says, never
through `pauseRollingUpdate`. A tier of two is unchanged: after the conversion O is X's only
replica, no other pod is asked, and the refusal on `connected_slaves == 0` stays.

**D4 — `forceReplicaConnections` sends nothing while the new master holds no keys and any other
pod holds some.** The veto of [ADR 0028](0028-a-demotion-may-not-discard-the-only-dataset.md)
D1 binds this second `REPLICAOF` site, for the whole call and both callers
(`handleMasterWithNoReplicas`, `checkFinalizationTopology`): with X empty or unreadable, every
other existing pod is counted, non-Ready ones included, and any holder or unreadable count means
no `REPLICAOF` at all, logged once with who holds what, no Event (ADR 0028 D6). Fail-closed as
ADR 0028 D3. A per-target veto lost because an empty target attaching to X satisfies every gate
and unlocks the holder's delete, and because an X with a replica would defeat any write fence.
The Sentinel reset that follows the call runs as before. X then stays replica-less: the gate
refuses on `connected_slaves == 0` as today, the no-replica branch cycles, `MultipleMasters` is
reported at every boundary pass and `SplitBrainDetected` after 90 s (ADR 0025).

**D5 — An outdated pod is deleted only when its deletion discards no dataset and it no longer
answers master.** In `replaceRemainingPods`, after the D3 gate and before the delete gate of
[ADR 0026](0026-a-pod-being-deleted-is-not-available.md) D5, two preconditions on the pod about
to be deleted — whichever outdated pod the loop reached — on both paths: `demotionRefusalReason(X,
pod)` unchanged (X with keys or both empty, deleted; X empty and the pod holding keys or
unreadable, or X unreadable, held), and the pod does not answer `master` (a replica, or no
answer). Both as AND: the veto carries the dataset reason and holds an unreadable pod, the role
holds a full X for the seconds until Sentinel has converted O on the forced path (~16 s plus one
requeue), which is where the drain handler's second failover used to find a master to act on.
On the non-Sentinel path X is the pod flagged `isMaster` on the current template;
`verifyPromotionCandidateHoldsData` refused before the promotion, so the veto there catches only
a master that lost its data since. On the Sentinel path the delete no longer ends the refusal
shape of ADR 0028 D8; it holds it, bounded by D6.

**D6 — A held handover is reported as `MasterHandoverStalled` and never hands its state away.**
The gate holds in `failover-triggered` for three reasons — a current replica not synced (D3),
the dataset veto and the role (D5) — clocked by the sync-wait bound. Before
`spec.rollingUpdate.syncTimeout` (default 5 min) a plain requeue. Past it: the condition, an
edge with one evaluator, reasons `ReplicaNotSynced`, `DatasetWouldBeDiscarded` and
`FormerMasterStillMaster`, the message naming X, the held pod and the counts or the sync
reason, and the repair — point Sentinel at O (`SENTINEL REMOVE`, `SENTINEL MONITOR O`), after
which the resolver may demote the empty X (ADR 0028 D1), X syncs from O, the roll finds O
outdated and hands over under D1; one Warning Event at the set; from then `DeferredRequeueAfter`
in the shape of `terminationWait` (ADR 0026 D5): the state is kept, the Sentinel roll stays held
(`dataTierHolding`), the pass continues to the no-master recovery, the steady-state split-brain
check and the status write. Cleared presence-guarded where the delete goes through and in
`clearRollingUpdateState`; a row in `conditionRegistry`
([ADR 0027](0027-conditions-are-levels-edges-or-history.md)). The pause of
`pauseRollingUpdate` is not used here: it clears the state, which
[ADR 0010](0010-every-rolling-update-wait-is-bounded.md) forbids as the target of an expiry, and
releases the Sentinel roll onto the spec the data tier is stuck on. The Warning, unlike the
silent `...Stalled` siblings: this hold replaces a pause at this gate and every pause emits one,
and a dataset veto shows nowhere but here.

**D7 — The phase of a held pass is the health verdict, without a second override.** The held
pass computes the status like every stalled pass; under D2 that reads `Syncing` and
`Ready=False`/`ReplicationSyncing` in every hold shape, because the second master is not a
synced replica. The one phase override is `ReconcileBlocked`
([ADR 0002](0002-surface-a-blocked-reconcile-on-the-cr.md) D3); a second would amend that
record for a shape `MultipleMasters`, `SplitBrainDetected` and `MasterHandoverStalled` carry.
The condition exports as `vko_valkey_status_condition{condition="MasterHandoverStalled"}`
([ADR 0021](0021-per-resource-metrics-and-the-alert-that-was-missing.md)); whether the chart
alerts on it is decided with the alert on `RollingUpdatePaused`.

**D8 — The tracked texts say what the roll loses.** Every place that calls a multi-replica roll
lossless says instead: the pre-roll dataset survives; on a Sentinel cluster whose Sentinels
cannot run a coordinated failover (before Valkey 9.0, the 8 to 9 upgrade roll included) and on
any roll whose coordinated failover fell back to forced, the writes the outgoing master
acknowledges during the roll's failover are lost. `git grep -n -i 'lossless\|without data
loss\|no data is lost\|loses no data\|zero data loss'` over `README.md`, `CLAUDE.md`,
`docs/adr/`, `docs/operations/` and `test/` finds no unqualified claim. The `verifyNewMasterReady`
log line drops "with data" and logs the count as a field.

## Consequences

- **An e2e writer harness is the prerequisite, and the measurement.** During a Sentinel roll it
  writes through `-rw`, counts acknowledged-but-missing and refused writes separately, logs X's
  `INFO` at the delete and detects a second failover (the sidecar's "sentinel failover
  triggered" on the deleted pod, two `+switch-master`). It runs on both legs before any rule
  lands, and the D1 e2e asserts 0 lost on Valkey 9, non-zero with the argument reverted, the
  count and the fallback logged on Valkey 8, and zero Warning Events on a clean roll
  ([ADR 0017](0017-test-and-ci-policy.md) revert checks). It needs the e2e reply classification
  that `valkey-cli --raw SET` exiting 0 under a refusal does not give.
- **D1 trades loss for a block.** In the stall shape (X not `online` at the trigger, or never
  catching up) O blocks writes for up to `failover-timeout` (60 s) and then aborts; clients see
  a hang and a disconnect, not an error and not a lost write. The gates in front of the trigger
  make X online and caught up, so the stall needs the world to change between `WAIT` and the
  trigger. A lost election costs a roll about 40 s. The roll depends on
  [ADR 0022](0022-sentinel-identity-is-pinned-to-the-pod.md)'s clean Sentinel table for the
  first time: dead entries count towards the majority the election needs, and a stale table
  falls back to forced. Valkey 8 keeps today's loss.
- **D2 makes `Ready` flap.** Every replica full sync — after an eviction, a restart, a Sentinel
  reconfiguration — reads `Syncing` and `Ready=False` for its duration, minutes on a large
  dataset. The chart's `ValkeyPhaseNotOK` alert has `for: 30m`, so only a sync longer than that
  fires, which today never fires. Waits on `OK` after a replica replacement take seconds longer.
  Eight unit fixtures and one e2e subtest pin replies a master never gives and are rewritten
  replica-side.
- **D4 to D6 hold instead of acting.** In the refusal shape O keeps the dataset behind `-r`, X
  stays empty behind `-rw`, the Sentinel roll waits, and nothing ends it but a human, the repair
  in the message, or the first write on X — the veto is zero-against-some, and
  [ADR 0038](0038-the-operator-does-not-offer-min-replicas-to-write.md) refuses the fence that
  would keep X empty. A non-Ready O is held while X is empty. The D3 wait on the forced path
  adds up to one requeue per roll.
- **Two ADR rules are amended in place** (*Status*), one row joins the condition registry, the
  README condition row and [status.md](../operations/status.md) gain `MasterHandoverStalled`
  with its repair, [rolling-updates.md](../operations/rolling-updates.md) the hold, and
  [upgrading.md](../operations/upgrading.md) the 8 to 9 roll and the 60 s block.

## Alternatives Considered

**The failover (D1).** *A runtime `CONFIG SET min-replicas-to-write 1` on O before each
trigger* — both lines, but still about 500 lost (the fence acts only after `max-lag`), the
second failover untouched, and Sentinel's `CONFIG REWRITE` in `sentinelKillClients` persists
the directive into O's running config, so a later crash failover can promote a pod that refuses
every write: a new state to clear and a new failure class for a 5 % gain. *Keep the loss and
record its size* — every TLS rotation, upgrade and metrics change would lose 70-85 % of a 16 s
write window at phase `OK`, and the fifteen texts would be rewritten instead of made true.
*`FAILOVER TO <X>` from the operator straight to O, on both lines* — O then reports `role:slave`,
Sentinel treats a master reporting replica as unreachable, marks it down after 5 s and runs its
own failover with its own selection, and converts an X reporting master back as soon as O looks
sane again: the non-Sentinel promotion fought against Sentinel, timing unverified.

**The sync signal (D2).** *The replica-side answer at the gate and the observer only, the
master's count in `CheckCluster`* — protects the delete and keeps `Ready` quiet by reading
`True` about an empty replica. *Delete the three dead terms and correct the records* — no
behaviour change, the delete stays unguarded.

**The re-pointing (D4).** *A veto per target* — an empty target still attaches, X gains a
replica, the gate passes, only D5 stands before the holder's delete, and an X with a replica
satisfies any write fence.

**The delete (D5).** *The veto at every roll delete* (`replaceNextReplica`,
`deleteNextPendingPod` too) — a master `DBSIZE` before every replica delete, and no shape in
which deleting a replica discards the only dataset: a replica holding data behind an empty
master is flushed by it at the next link, and only a promotion saves it.

**The hold (D6).** *Pause like the manual path* (`waitOrPauseForReplicaSync`) — clears the
state against ADR 0010, releases the Sentinel roll, and repeats: with O still `master` the
resolver refuses, O keeps `isMaster`, `replaceNextReplica` skips it, `waitForReplicasReady`
blocks, a Warning and phase `Error` every `syncTimeout`. *`RollingUpdatePaused` with a new
reason* — the condition would then mean both "cleared and retrying" and "held, never retrying",
while what a pause leaves behind is itself being re-decided. *A phase override to `Error` for
the hold* — see D7.

## Residual risks

- **Not verified on Kubernetes:** the coordinated failover at all — the address chain
  (`replica-announce-ip` is the pod FQDN, Sentinel announces hostnames, `findReplica` compares
  that string and the listening port; consistent by code), TLS replication, three Sentinels with
  an election, two Sentinels, no majority, a stalled `FAILOVER TO`. The docker runs are the only
  measurement; the writer harness settles it.
- **Sentinel's own conversion is a route no veto reaches.** It acts 8-16 s after an empty
  promotion whenever Sentinel still lists O and X looks sane; D4 and D5 stop the operator's
  flush, which is the only one left once Sentinel has forgotten O. The fix for the common route
  lies before this record: never promote an empty X. D1's coordinated first trigger cannot (X
  must reach O's offset); the forced retrigger can, and it skips the sync gates the first
  trigger has (`handleFailoverRetrigger`). Gating it is a decision this record does not take.
- **The veto holds until the first write on X.** One cycle on a cluster with traffic, until a
  human on an idle one. Accepted with ADR 0038.
- **A chained replica passes D2** (link up to O while O is X's replica). D3's count on X covers
  the delete; `CheckCluster` and the observer do not ask the attachment target.
- **Whether the third replica resyncs partially after a coordinated failover** is read from
  PSYNC2 (O paused before the handover and X caught up), not measured; D3 needs the check for a
  mid-roll eviction or restart regardless. **How long real full syncs take** on a fleet, so how
  long `Ready` reads `ReplicationSyncing`, is unknown.
- **The repair path of D6** (Sentinel pointed at O, X demoted, roll resumed) is read along the
  resolver, the roll and D1, not driven. **The second failover on Kubernetes** is not measured;
  D5's role precondition is right regardless, it only makes the comment at the delete true.
- **`DBSIZE` counts db0 only**, as in ADR 0028.

## References

* [`internal/controller/rolling_update.go`](../../internal/controller/rolling_update.go) —
  `handleMasterFailover`, `handleFailoverRetrigger`, `triggerSentinelFailover`,
  `handlePostFailover`, `handleNewMasterFound`, `handleMasterWithNoReplicas`,
  `forceReplicaConnections`, `replaceRemainingPods`, `verifyNewMasterReady`,
  `replicationNotEstablishedReason`, `demotionRefusalReason`, `dbSizeReader`, `terminationWait`,
  `pauseRollingUpdate`, `clearRollingUpdateState`
* [`internal/valkeyclient/client.go`](../../internal/valkeyclient/client.go) — `SentinelFailover`,
  `ReplicationInfo`, `parseReplicationInfo`
* [`internal/health/checker.go`](../../internal/health/checker.go) — `CheckCluster`, `findMaster`
* [`internal/observer/checks.go`](../../internal/observer/checks.go) — `checkReplicaSync`
* [`internal/sidecar/drain.go`](../../internal/sidecar/drain.go) — the forced drain failover,
  `isSyncedReplica`; [`internal/sidecar/labeler.go`](../../internal/sidecar/labeler.go) — the
  Sentinel cross-check
* [`internal/controller/condition_registry.go`](../../internal/controller/condition_registry.go)
* Upstream, Valkey 9.1.1: [`sentinel.c`](https://github.com/valkey-io/valkey/blob/9.1.1/src/sentinel.c)
  (`SENTINEL FAILOVER`, `sentinelFailoverTo`, `sentinelFailoverWaitStart`,
  `sentinelSelectReplica`, `compareReplicasForPromotion`, the `+convert-to-slave` rule),
  [`replication.c`](https://github.com/valkey-io/valkey/blob/9.1.1/src/replication.c)
  (`failoverCommand`, `updateFailoverStatus`, `findReplica`)
* [ADR 0002](0002-surface-a-blocked-reconcile-on-the-cr.md) D3, D5a — the one phase override, `Ready` as the data-plane verdict
* [ADR 0005](0005-upgrade-neutral-defaults-and-anti-affinity.md) D1 — why D2 is no opt-in
* [ADR 0007](0007-failover-aware-rolling-update.md) D1, D3, D10 — the sequence, the fail direction, the predicate D2 generalises
* [ADR 0010](0010-every-rolling-update-wait-is-bounded.md) — why D6 holds and never clears
* [ADR 0017](0017-test-and-ci-policy.md) — the revert checks and the e2e tier the harness lives in
* [ADR 0021](0021-per-resource-metrics-and-the-alert-that-was-missing.md) — the condition export
* [ADR 0022](0022-sentinel-identity-is-pinned-to-the-pod.md) — the table the election counts
* [ADR 0025](0025-a-split-brain-warning-means-one-that-did-not-resolve-itself.md) D7, D9 — zero Warnings on a clean roll; the window D1 closes
* [ADR 0026](0026-a-pod-being-deleted-is-not-available.md) D5, D11 — the delete gate and the hold shape D6 copies
* [ADR 0027](0027-conditions-are-levels-edges-or-history.md) — the registry row of D6
* [ADR 0028](0028-a-demotion-may-not-discard-the-only-dataset.md) D1, D3, D4, D6, D8 — the veto D4 and D5 extend
* [ADR 0038](0038-the-operator-does-not-offer-min-replicas-to-write.md) — the fence this record does without
