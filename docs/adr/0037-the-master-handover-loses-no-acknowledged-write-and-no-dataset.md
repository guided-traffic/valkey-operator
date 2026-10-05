# ADR 0037: The master handover loses no acknowledged write and no dataset

## Status

Accepted. Date: 2026-09-28.

Implemented 2026-09-28, every rule, in one change. The writer harness ran first against the code
before it, and that run is the Kubernetes measurement in *Context*.

- **D1**: `valkeyclient.SentinelFailoverCoordinated`; `triggerSentinelFailover(ctx, v,
  coordinated)` — coordinated from `handleMasterFailover`, forced from `handleFailoverRetrigger`;
  the sidecar's drain handler is unchanged (its interface has only `SentinelFailover`). Error
  replies are typed (`valkeyclient.ReplyError`). The fallback (`coordinatedFallbackReason`) takes
  `NOGOODPRIMARY` and an `ERR` whose text says the option is unknown (`wrong number of arguments`,
  a Valkey 8 Sentinel; `Unknown failover option`, a Valkey 9 one) — not `ERR` on its code alone,
  which also answers a monitor name the Sentinel does not know and would send a tier that can
  coordinate into the forced failover. After a fallback the rest of the pass is forced; every
  other failure is a failed attempt, the next Sentinel asked in the same mode. The success line
  logs `failoverMode` and `fallbackReason`.
- **D2**: `ReplicationInfo.NotEstablishedReason`; `replicationNotEstablishedReason` prefixes the
  pod and lost the unreachable "still syncing" message; `CheckCluster` counts `ReadyReplicas`
  from the replies `findMaster` returns by ordinal and no longer dials the master a second time;
  the observer's `checkReplicaSync` asks every data pod; `isSyncedReplica` uses the predicate.
- **D3, D5, D6** (`internal/controller/master_handover.go`): `gateOutgoingPodDelete`,
  `verifyNewMasterReady`, `replicasNotOnNewMaster`, `datasetRefusal`, `stillMasterRefusal`,
  `holdHandover`, `endHandoverHold`;
  `ConditionTypeMasterHandoverStalled` with `ReasonReplicaNotSynced`,
  `ReasonDatasetWouldBeDiscarded`, `ReasonFormerMasterStillMaster` and, on the clear,
  `ReasonMasterHandoverNotHeld`; the Event reason `MasterHandoverStalled`; a `conditionRegistry`
  row. D5 runs on the Sentinel path only (corrected in D5).
- **D4**: `replicaOfRefusal` at the head of `forceReplicaConnections`, key counts memoized per
  pass (`keyCountsOnce`).
- **From the review of the change** (each amended in place above): the hold has its own bound,
  `vko.gtrfc.com/handover-hold-started` (D3); the dataset veto is the reason reported first
  (D6); and the post-failover handler resets Sentinel only after no pass has found a master for
  `failoverRetryTimeout`, besides the stamp (`noMasterTimedOut`, D6).
- **D7**: nothing to build — the held pass returns `DeferredRequeueAfter` and the status write
  computes the phase (precised in D7).
- **D8**: the fifteen texts reworded — `CLAUDE.md` among them, for the owner's review — and the
  log line reads "New master verified" with the count as a field. The grep over `README.md`,
  `CLAUDE.md`, `docs/adr/`, `docs/operations/` and `test/` finds the claim only struck through in
  place, quoted in this record, or in the places the decision left out (ADR 0012 D9, ADR 0016,
  ADR 0023, `persistence.md`).

Verified 2026-09-28. On Kind (the setup of the *Context* measurement), with the operator built
from this change: the harness lost 0 acknowledged writes on Valkey 9.1.1 in every run (0 of
22 772, 16 973, 19 192 and 31 388; coordinated, one `+switch-master`, no drain failover), and 701,
2 493, 2 357, 360 and 2 291 on Valkey 8.1.9 (the forced fallback logged with the Sentinel's
reply). In one 9.1.1 run 289 writes were answered `READONLY` — sent to O after its handover and
before its labeler moved the `-rw` endpoint — refused, not lost. Revert check: with the first
trigger forced again, Valkey 9.1.1 lost 2 446 of 20 650 and the harness failed on the loss and on
the missing coordinated log line. The full e2e suite passed on both lines, 53 tests each, the
multi-node anti-affinity test skipped as on every single-node leg, and ran again on the final code
after the review fixes: 53 of 53 on Valkey 9; 52 of 53 on Valkey 8, the harness failing on its own
log assertion — the forced command right after the fallback met `NOGOODSLAVE` on every Sentinel
and the retrigger went through without a fallback line of its own — which now checks the fallback
and the forced success separately and passed in two reruns. The multi-node leg did not run
locally. `make test-unit`, `make lint`, `make cyclo`, `make test-integration`, `make gosec`,
`make vuln` green, `make generate-all` without a diff. 25 of 25 unit-tier mutations of the new code
killed, one or more per rule. The refusal-shape unit test failed against the code before the
change (a `REPLICAOF` to the holder and its delete) and passes on it. What is not verified is in
*Residual risks*.

Amends in place: [ADR 0025](0025-a-split-brain-warning-means-one-that-did-not-resolve-itself.md)
D9 (the roll's own failover is coordinated where Sentinel supports it), [ADR 0007](0007-failover-aware-rolling-update.md)
D1 and D10 (the failover command; the replica-side predicate binds the delete),
[ADR 0028](0028-a-demotion-may-not-discard-the-only-dataset.md) D1, D3, D4 and D8 (the veto
binds `forceReplicaConnections` and the delete; the divergence on the Sentinel path is held, not
bounded) and [ADR 0026](0026-a-pod-being-deleted-is-not-available.md) D11 (the *Replacement*
argument's gate). [ADR 0038](0038-the-operator-does-not-offer-min-replicas-to-write.md) is the
companion refusal: no write fence.

## Context

A Sentinel data-tier roll hands the master over once *(a roll with a replica: the single data
pod of a Sentinel cluster is replaced without a handover,
[ADR 0007](0007-failover-aware-rolling-update.md) D11)*. With every replica on the new template it
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

**Measured on Kubernetes** (2026-09-28, before any rule here was built: the operator from `main`
at `dfe74bf`, single-node Kind v0.32.0 with its default node image, three data pods and three
Sentinels, no TLS, a data roll started by a CPU-request change; the writer harness of
*Consequences*, one `valkey-cli` connection per write through the `-rw` Service, about 200 writes
per second): Valkey 9.1.1 lost 2245 of 20425 and 2060 of 19722 acknowledged writes in two runs,
Valkey 8.1.9 lost 2352 of 20624 and 864 of 32841, each run one contiguous block of writes between
the trigger and the delete, no write acknowledged before the trigger lost, one `+switch-master`
per run. The second failover did not occur on Kind: in all four runs the outgoing master's drain
handler read `role: replica` at its SIGTERM, so Sentinel had converted O before the delete pass.

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
promotes nothing and falls back through `failoverRetryTimeout` (30 s, measured since 2026-09-28
from the first pass that found no master, see D6), reset and the forced
retrigger. The retrigger (`handleFailoverRetrigger`) and the sidecar's drain handler stay
forced: the first because a lost election would otherwise loop, the second because it runs
inside a 75 s grace period a 60 s stall would consume. The `WAIT` gate before the trigger stays:
it keeps the pause short and remains the gate for the forced fallback. The upgrade roll from
Valkey 8 to 9 is itself forced — the Sentinel tier shares `spec.image` and rolls after the data
tier — so the first roll that loses no acknowledged write is the next one.

**D2 — "Synced" is the replica's full replication answer at every site that says it.** The
predicate of `replicationNotEstablishedReason` — role not master, `master_link_status` `up`, no
sync in progress — moves onto `valkeyclient.ReplicationInfo`, and `isSyncedReplica` (sidecar),
`CheckCluster` (`AllSynced`, `ReadyReplicas`) and the observer's `checkReplicaSync` are built on
it. `CheckCluster` evaluates it on the replies `findMaster` already collects from every Running
pod, keeping them indexed by ordinal instead of discarding the non-master ones, at no extra
dial; the observer asks each data pod, one `INFO` per replica per cycle. The master's
`master_sync_in_progress` is read nowhere but the parser. `Ready` therefore reports
`False`/`ReplicationSyncing` and the phase `Syncing` during every replica full sync, after an
eviction or a restart too, *(precised 2026-09-28, read in the code as built: on a Sentinel
cluster — `CheckCluster` is called by `updateHAStatus` alone; the status of a cluster without
Sentinel asks each pod `PING` and never reads replication)* which is what [ADR 0002](0002-surface-a-blocked-reconcile-on-the-cr.md)
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
compared: the init script writes two FQDN forms. The gate arms ~~the sync-wait bound
(`ensureSyncWaitTimestamp`)~~ *(corrected 2026-09-28, found in review: its own bound,
`vko.gtrfc.com/handover-hold-started` with an in-memory copy — `verifyReplacedReplicasSynced`
clears the sync-wait bound whenever every replaced replica is synced, and a pass that finds no
replica left to replace and then reaches the gate re-armed the hold on every pass, so it was
never reported)*; past `spec.rollingUpdate.syncTimeout` it holds as D6 says, never
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
to be deleted — whichever outdated pod the loop reached — ~~on both paths~~ *(corrected
2026-09-28: `replaceRemainingPods` is reached only on the Sentinel path — its callers are
`handleRollingUpdate`, `handleNewMasterFound` and `handleMasterWithNoReplicas`, all behind the
Sentinel dispatch — so the preconditions run there alone; the non-Sentinel roll deletes its master
in `handleManualFailover`, after `verifyPromotionCandidateHoldsData`, and its remaining pods in
`deleteNextPendingPod`, and neither carries this veto, which is what the rejected veto at every
roll delete would have added)*: `demotionRefusalReason(X,
pod)` unchanged (X with keys or both empty, deleted; X empty and the pod holding keys or
unreadable, or X unreadable, held), and the pod does not answer `master` (a replica, or no
answer). Both as AND: the veto carries the dataset reason and holds an unreadable pod, the role
holds a full X for the seconds until Sentinel has converted O on the forced path (~16 s plus one
requeue), which is where the drain handler's second failover used to find a master to act on.
~~On the non-Sentinel path X is the pod flagged `isMaster` on the current template;
`verifyPromotionCandidateHoldsData` refused before the promotion, so the veto there catches only
a master that lost its data since.~~ *(Void, corrected 2026-09-28: no non-Sentinel pass reaches
this delete, see above.)* On the Sentinel path the delete no longer ends the refusal
shape of ADR 0028 D8; it holds it, bounded by D6.

**D6 — A held handover is reported as `MasterHandoverStalled` and never hands its state away.**
The gate holds in `failover-triggered` for three reasons — a current replica not synced (D3),
the dataset veto and the role (D5) — clocked by ~~the sync-wait bound~~ its own bound (see D3,
corrected 2026-09-28). Every check holds; the order decides only the reason reported, and the
dataset veto is reported first: in the shape D4 leaves X has no replica, so D3 refuses as well,
and reporting that alone told the reader to wait for a sync that never comes *(found in review,
2026-09-28)*. A hold keeps the roll in `failover-triggered` with the trigger's stamp long expired,
so the post-failover handler no longer times out on that stamp alone: a pass that finds no
master starts an absence clock, and Sentinel is reset only once no pass has found a master for
`failoverRetryTimeout` — before, one pass in which X was not Ready or its `INFO` timed out
reset Sentinel and forced a failover of a healthy X *(`noMasterTimedOut`, found in review,
2026-09-28)*. Before
`spec.rollingUpdate.syncTimeout` (default 5 min) a plain requeue. Past it: the condition, an
edge with one evaluator, reasons `ReplicaNotSynced`, `DatasetWouldBeDiscarded` and
`FormerMasterStillMaster`, the message naming X, the held pod and the counts or the sync
reason, and the repair — point Sentinel at O (`SENTINEL REMOVE`, `SENTINEL MONITOR O`, and the
`auth-pass` and timing settings `SENTINEL REMOVE` dropped, on every Sentinel), after
which the resolver may demote the empty X (ADR 0028 D1), X syncs from O, ~~the roll finds O
outdated and hands over under D1~~ *(corrected 2026-09-28, read in the code as built, not driven:
the roll then finds no current pod answering master, times the failover out and resets Sentinel
onto O, and hands over through the retrigger — which D1 keeps forced, so the repaired handover
loses the writes O acknowledges in its window on every Valkey line)*; one Warning Event at the set; from then `DeferredRequeueAfter`
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
synced replica. *(Precised 2026-09-28, read in the code as built, not measured: in every hold
shape in which the outgoing pod still answers master or a current replica is not synced. A hold
on an unreadable key count, or on the new master's attachment count alone, can leave every pod
a synced replica, and the phase then reads `OK`; `MasterHandoverStalled` carries those. And a
held pass writes the rolling-update phase before the status write replaces it, twice per
requeue, as every `DeferredRequeueAfter` hold before it.)* The one phase override is `ReconcileBlocked`
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
  a hang and a disconnect, not an error ~~and not a lost write~~ *(corrected 2026-09-28, read in
  sentinel.c 9.1.1 and the code, not measured: the abort promotes nothing, so the roll falls back
  through the reset to the forced retrigger, which loses its own window's writes; and when that
  forced promotion lands before O's pause has run out, the writes the pause held are among them)*.
  The gates in front of the trigger
  make X online and caught up, so the stall needs the world to change between `WAIT` and the
  trigger. A lost election costs a roll about ~~40 s~~ 55 s *(the absence clock of D6 adds
  one requeue; read from the code)*. The roll depends on
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

- ~~**Not verified on Kubernetes:** the coordinated failover at all~~ *(narrowed 2026-09-28: it ran
  on single-node Kind with three Sentinels, no TLS, and lost no acknowledged write — so the
  address chain and an election among three Sentinels are verified there.)* Not verified on
  Kubernetes: TLS replication under a coordinated failover, two Sentinels, a lost election, a
  stalled `FAILOVER TO`, a multi-node cluster, a fleet.
- **Sentinel's own conversion is a route no veto reaches.** It acts 8-16 s after an empty
  promotion whenever Sentinel still lists O and X looks sane; D4 and D5 stop the operator's
  flush, which is the only one left once Sentinel has forgotten O. The fix for the common route
  lies before this record: never promote an empty X. D1's coordinated first trigger cannot (X
  must reach O's offset); the forced retrigger can, and it skips the sync gates the first
  trigger has (`handleFailoverRetrigger`). Gating it is a decision this record does not take.
- **The veto holds until the first write on X.** One cycle on a cluster with traffic, until a
  human on an idle one. Accepted with ADR 0038.
- **A chained replica passes D2** (link up to O while O is X's replica). ~~D3's count on X covers
  the delete~~ *(corrected 2026-09-28, read in the code as built: D3 counts the other pods on the
  current template, and O, being outdated, is not among them — but once O is X's replica, X counts
  O, so a replica chained through a converted O passes the predicate **and** the count, and O is
  deleted under it. The count covers a replica chained through an O that still answers master,
  which D5's role precondition holds anyway. What the gap costs: the chained replica loses its
  upstream at the delete and keeps its dataset until Sentinel, which reconfigures the replicas in
  the same failover, or the returning O re-attaches it; no acknowledged write is lost, since the
  chained replica is not the master. Closing it means comparing each replica's attachment target,
  which D3 rejected because the init script writes two FQDN forms. Not measured.)*
  `CheckCluster` and the observer do not ask the attachment target.
- **Whether the third replica resyncs partially after a coordinated failover** is read from
  PSYNC2 (O paused before the handover and X caught up), not measured; D3 needs the check for a
  mid-roll eviction or restart regardless. **How long real full syncs take** on a fleet, so how
  long `Ready` reads `ReplicationSyncing`, is unknown.
- **The repair path of D6** (Sentinel pointed at O, X demoted, roll resumed) is read along the
  resolver, the roll and D1, not driven; it hands over through the forced retrigger (corrected in
  D6). **And it can still lose the dataset** *(found in review, 2026-09-28)*: the retrigger does
  not wait for X's resync from O — it fires once `failoverResetMinWait` (20 s) has passed and
  Sentinel reports the replicas, capped at 90 s — and it asks no replica whether it holds the
  data. If X's full sync is not done by then, Sentinel can promote the still-empty X and convert
  O onto it. The repair therefore keeps the dataset only when its full sync is shorter than that
  wait, and the texts say so. Gating the retrigger is the decision this record leaves open
  (*Sentinel's own conversion* above). **The second failover on Kubernetes** did not occur in four runs on Kind, all four with
  O already converted at its SIGTERM; D5's role precondition is right regardless, it only makes
  the comment at the delete true.
- **The harness bounds each write** (`timeout 10 valkey-cli -t 2`): one run without the bound
  had a single write hang for the rest of a roll, right after the outgoing master's delete, on a
  connection that never completed. A cut-off write counts as not acknowledged, which is what the
  client saw; the cause of the hang is not established.
- **`DBSIZE` counts db0 only**, as in ADR 0028.

## References

* [`internal/controller/rolling_update.go`](../../internal/controller/rolling_update.go) —
  `handleMasterFailover`, `handleFailoverRetrigger`, `triggerSentinelFailover`,
  `handlePostFailover`, `handleNewMasterFound`, `handleMasterWithNoReplicas`,
  `forceReplicaConnections`, `replaceRemainingPods`, `coordinatedFallbackReason`,
  `replicationNotEstablishedReason`, `demotionRefusalReason`, `dbSizeReader`, `terminationWait`,
  `pauseRollingUpdate`, `clearRollingUpdateState`
* [`internal/controller/master_handover.go`](../../internal/controller/master_handover.go) —
  `gateOutgoingPodDelete`, `verifyNewMasterReady`, `replicasNotOnNewMaster`, `datasetRefusal`,
  `stillMasterRefusal`, `holdHandover`, `endHandoverHold`, `reportMasterHandoverStalled`,
  `replicaOfRefusal`
* [`internal/valkeyclient/client.go`](../../internal/valkeyclient/client.go) — `SentinelFailover`,
  `SentinelFailoverCoordinated`, `ReplyError`, `ReplicationInfo.NotEstablishedReason`,
  `parseReplicationInfo`
* [`test/e2e/handover_writes_test.go`](../../test/e2e/handover_writes_test.go) — the writer harness,
  `TestE2E_RollingUpdate_HA_WritesDuringHandover`
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
