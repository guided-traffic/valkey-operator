---
id: T79
title: the Sentinel roll deletes the former master behind verifyNewMasterReady, which reads the new master's key count and refuses on nothing, and three tracked texts still describe that gate
state: analysed       # the delete path and its gate read end to end at 84a39c2, the Valkey inputs of the gate measured in docker on both pins (2026-09-27), both open decisions carry a marked option
severity: high        # impact if never fixed: in the shape where the promoted master is empty and the outgoing pod still holds the dataset, the delete takes the only copy - a persistent pod reboots, asks Sentinel, becomes the empty master's replica and full-syncs its volume away (T73 M3) - and the roll completes with phase OK and nothing in it, as in ADR 0028 Context. The trigger is narrow and by reading (Impact)
security: none        # no principal involved: the operator's own delete breaks the dataset rule of ADR 0028, a data-integrity rule and not a trust boundary; the same class as T36, T67, T69 and T73. Not live: no page under docs/security/ states the gate as a guarantee (grep of docs/security for DBSIZE, key count and verifyNewMasterReady, 2026-09-27: no match)
urgency: now          # rule 1, second clause, even under the strict reading (re-derived in the adversarial review, 2026-09-27; was next): ADR 0007:523-526 is a statement about tracked text - "Three code comments still describe the check as present", quoting "has actual data (DBSIZE > 0)" - and a reproducible `git grep -n "has actual data\|critical safety check" 84a39c2 -- . ':!docs/tickets'` shows no code comment carries it (hits: ADR 0007 :388, :393, :525 and the corrected comment rolling_update.go:3364), which is a measurement of the tracked text the sentence is about, not a reading of code logic. The other false texts (ADR 0007:392-393, ADR 0026:787-789, rolling_update.go:3381 and :4026-4027, sentinel_failover_test.go:1063 and :1071-1072) are false by reading; under the repository reading (018, 023, 059, 073) they would match rule 1 as well. Back to next (rule 3: severity high, trigger live in released code since 5214d56, 2026-02-18, v1.0.0 and every later tag, on every Sentinel cluster during a data-tier roll) once Work list 2 and 4 land. The owner may rule that a grep of tracked text is no measurement; then rule 3 gives next
effort: M             # the recommended D1-A (one veto reusing demotionRefusalReason and dbSizeReader) and D2-C (a hold with a bounded, reported observation under its own condition) with unit tests and revert checks, the text corrections (XS), and an ADR 0007 amendment touching ADR 0028 D3, D4 and D8 and ADR 0026 Residual risks
blocked-by: decision  # D1 and D2, below
filed-from: T73 (Work list item 4 and its History sweep note), re-verification of 2026-09-27 at 84a39c2
opened: 2026-09-27
decided:              # not recorded - no decision yet
done:                 # not recorded - not done
---

# T79 - the Sentinel roll deletes the former master behind a gate that asks for no key count

Filed on 2026-09-27 from [T73](073-forcereplicaconnections-re-points-a-data-holder-at-an-empty-master.md)
(Work list item 4, "Filing, still open", and the sweep note in its History), during the
re-verification of every open ticket at `84a39c2`. Until this file, the finding was open only as a
residual risk in [ADR 0007](../adr/0007-failover-aware-rolling-update.md) (`:517-526`) and
[ADR 0026](../adr/0026-a-pod-being-deleted-is-not-available.md) (`:787-792`), and as an "open,
pre-existing" note in the archived
[archive/032](archive/032-unavailable-replaced-pod-waits-unbounded.md) (`:468-470`, `:482-483`),
which is history and not an owner. Every location below was read at `84a39c2`; only
`docs/tickets/` differs in the working tree, so the working-tree lines are the `84a39c2` lines.

## Fact

**Mechanism.** On a Sentinel cluster the data-tier roll replaces the replicas, triggers a Sentinel
failover, and then deletes the outgoing master in `replaceRemainingPods`
([rolling_update.go:2984](../../internal/controller/rolling_update.go#L2984)). For every outdated
pod it first waits for a pod that is gone or terminating, and then, on the Sentinel path only,
asks `verifyNewMasterReady`
([rolling_update.go:3012-3017](../../internal/controller/rolling_update.go#L3012-L3017)). Then comes
the ADR 0026 D5 gate on another terminating pod (`:3023-3026`), then the state is set to
`replacing-master` (`:3029`), and then the pod is deleted
([rolling_update.go:3034](../../internal/controller/rolling_update.go#L3034)). Readiness of the
pod being deleted is not asked (ADR 0026 D11).

`verifyNewMasterReady` ([rolling_update.go:3331](../../internal/controller/rolling_update.go#L3331))
takes the first pod in slice order that is on the current template, `available()` and answers
`role:master` (`:3334-3348`). It then refuses only in these cases:

| Check | Where | What it refuses |
|---|---|---|
| `connected_slaves == 0` | [rolling_update.go:3350](../../internal/controller/rolling_update.go#L3350) | a new master with no attached replica |
| `master_sync_in_progress` | [rolling_update.go:3355](../../internal/controller/rolling_update.go#L3355) | nothing on a master: Valkey does not emit this replica-side field in a master's `INFO` ([T69](069-three-sync-checks-read-a-replica-field-from-the-master.md); measured again below) |
| TLS config not buildable | `:3368-3372` | the whole check (wait) |
| `DBSIZE` unreadable | [rolling_update.go:3374-3379](../../internal/controller/rolling_update.go#L3374-L3379) | the whole check (wait) |
| the only candidate is terminating | [rolling_update.go:3387-3392](../../internal/controller/rolling_update.go#L3387-L3392) | the whole check, through `terminationWait` (ADR 0026 D5, bounded observation) |
| no current, available pod answers `role:master` | [rolling_update.go:3395-3396](../../internal/controller/rolling_update.go#L3395-L3396) | the whole check (wait) |

On any readable count, **zero included**, it logs "New master verified with data" and returns
verified ([rolling_update.go:3381-3383](../../internal/controller/rolling_update.go#L3381-L3383)).
It never reads the key count of the pod about to be deleted and compares nothing. Each of its
waits except the terminating-candidate one is a plain requeue of `rollingUpdateRequeueDelay`
with no bound (listed as unbounded in archive/032 `:451-456`; ADR 0010 Residual risks
`:767-774`).

**What the delete does to the dataset (read, with the Valkey side measured in T73).** A non-persistent
outgoing pod loses its memory with the delete. A persistent one is recreated by the StatefulSet.
Its init container asks Sentinel first (`SENTINEL get-master-addr-by-name`,
[statefulset.go:288-316](../../internal/builder/statefulset.go#L288-L316), up to 30 s, then the
replica ConfigMap, `:318-327`). Sentinel names the new master, so the pod boots as its replica
(the replica branch, [statefulset.go:329-339](../../internal/builder/statefulset.go#L329-L339)). The full sync then replaces the files in
`/data` as well as the memory: this was measured in T73 M3 for `rdb` and `aof` on both pins. In
both cases, what the pod held before the delete is gone once the new master holds nothing.

**The completion path asks no key count either (read).** After the delete, the next passes reach
`finalizeRollingUpdate` ([rolling_update.go:891](../../internal/controller/rolling_update.go#L891)).
On the Sentinel path it holds while a data pod terminates, and then runs
`checkFinalizationTopology` (`:946-989`): exactly one master and every replica attached, then a
Sentinel sync. After that it emits the Normal `RollingUpdateComplete` Event and clears the state
(`:927-932`). An empty master with all replicas attached passes this check. When finalization
has stalled, it also calls `forceReplicaConnections` (`:983`, T73).

**The gate cannot fire where Sentinel converts the old master itself (measured in T73, read here).**
With a healthy Sentinel tier, the old master O becomes a replica of the promoted pod X within 5 to 20 s
of the failover, and full-syncs X's dataset (T73 M2, both pins). By the time `verifyNewMasterReady`
runs, O then holds what X holds. The delete-time count of O is therefore meaningful only while O
is **not** X's synced replica:

- O still answers master. This is the ADR 0028 refusal shape, in which Sentinel is not demoting O.
- O's link to X is down and it has not flushed yet. T73 M1 measured O still holding its keys
  with the link down for up to +4 to +7 s after the `REPLICAOF`, while X already counted it as
  connected at +1 s.

**Relation to ADR 0028** ([ADR 0028](../adr/0028-a-demotion-may-not-discard-the-only-dataset.md)).
D1 refuses a demotion (`REPLICAOF`) that would discard the only dataset, and D4 applies that
"whatever chose the authority". A delete of the only data holder discards the same dataset by
another verb, and the rule does not cover it (read). The refusal lasts only until a replica attaches
to X. On a Sentinel roll whose resolver refused to demote O toward an empty X (the resolver runs
before every dispatch, [rolling_update.go:714-715](../../internal/controller/rolling_update.go#L714-L715),
except while `ownFailoverInFlight` holds - `failover-triggered` and a failover timestamp younger
than 90 s, `:797-800`, `:817-822` - when it only reports), O keeps `isMaster` (D8). A later pass
reaches `handleNewMasterFound`: once X has one connected replica, by any route, it goes to
`replaceRemainingPods`
([rolling_update.go:3129](../../internal/controller/rolling_update.go#L3129)).
`verifyNewMasterReady` passes, and O, the pod the refusal protected, is deleted at `:3034`.
ADR 0028 D8 records exactly this as the end of the refusal on that path, twice: its bound-table
row "Sentinel path | Sentinel reconfigures the returning pod itself | resolved without the
operator" (`0028:198`) is the delete followed by the full sync from the empty X, and its
correction of 2026-09-26 (`0028:206-210`) says the state "leaves for `replacing-master` [...] once
`verifyNewMasterReady` passes", which is this delete.

**Relation to T73.** T73 is the second destructive site in the same shape. `forceReplicaConnections`
at `:3159` re-points O at X one pass *before* this delete. T73's recommended D1-B vetoes that call.
T73 then states that its B "does not close the delete": a pod that attaches to X by another route
still passes `verifyNewMasterReady`, and the outgoing data holder is deleted. That remaining route
is this ticket. The two vetoes compose, and neither closes the other's gap.

**How the promoted master can be empty (read, from T73 and T36).** There are three routes:

- The retrigger after a 30 s failover timeout (`handleFailoverRetrigger`,
  [rolling_update.go:849-887](../../internal/controller/rolling_update.go#L849-L887)). It sends
  `SENTINEL FAILOVER` without the `waitForReplicasReady` and `waitForWriteSync` gates of the first
  trigger (`:2710-2719`), and Sentinel selects a replica by priority, offset and run id, never by
  key count (T73 Fact, measured in T73 M2).
- A promoted pod without persistence that restarts before its replicas attach. It boots as an
  empty master because Sentinel names it
  ([T36](036-non-persistent-master-restarts-empty.md)).
- The tiebreak variant, which T73 records as not produced.

**Three tracked texts still describe the gate as present at `84a39c2`** (read):

1. The log line [rolling_update.go:3381](../../internal/controller/rolling_update.go#L3381),
   "New master verified with data". It is written for every readable count, `dbsize=0` included,
   so on the loss path it is the one trace, and it says the opposite of what happened.
2. The comment in `handleManualFailover`,
   [rolling_update.go:4026-4027](../../internal/controller/rolling_update.go#L4026-L4027): "The
   Sentinel path reads the same counts after its failover (verifyNewMasterReady) but only logs
   them". The counts of the manual path are two, the outgoing master's and the candidate's
   (`verifyPromotionCandidateHoldsData`, `:2843-2890`). The Sentinel path reads one, the new
   master's, and never the outgoing pod's.
3. The unit test `TestVerifyNewMasterReady_AcceptsAMasterWithReplicasAndData`
   ([sentinel_failover_test.go:1063](../../internal/controller/sentinel_failover_test.go#L1063)),
   whose assertion message is "the keyspace of the promoted pod is what has to be non-empty"
   ([sentinel_failover_test.go:1071-1072](../../internal/controller/sentinel_failover_test.go#L1071-L1072)).
   Its fixture answers every `DBSIZE` with 4711 (`clusterAnswer`, `:242-243`). The code accepts
   0 as well, and no test gives the function a 0, so no test pins either direction.

**Found not as stated: the three comments ADR 0007 names were already corrected.** ADR 0007
Residual risks (`:523-526`) says that "Three code comments still describe the check as present".
It names the header of `replaceRemainingPods` ("has actual data (DBSIZE > 0)"), the inline comment in
`verifyNewMasterReady`, and the comment above the pre-promotion check in `handleManualFailover`.
All three were rewritten in `bb6c78f` (2026-09-26, "feat: run every generated pod rootless and
bound waits on unavailable pods"; first tag `v1.13.0`). That is the same commit that added the ADR
sentence (`git log -S` for both). At HEAD:

- The header ([rolling_update.go:2979-2983](../../internal/controller/rolling_update.go#L2979-L2983))
  says "It reads that master's DBSIZE but does not refuse on it".
- The inline comment ([rolling_update.go:3361-3366](../../internal/controller/rolling_update.go#L3361-L3366))
  says "is NOT refused here, although this comment used to call it a critical safety check".
- The `handleManualFailover` comment is item 2 above: corrected to "only logs them", with the
  wrong "same counts" left in.

`git grep "has actual data\|critical safety check" 84a39c2 -- . ':!docs/tickets'` finds only ADR
0007 (`:388`, struck; `:393`; `:525`) and the corrected comment at `:3364`. The ADR texts that
still quote the old comments are stale the same way:

- ADR 0007 D10 correction `:392-393`: "Its comment calls that a critical safety check".
- ADR 0026 Residual risks `:787-789`: "Its comment calls it the check that an empty replica was not
  promoted while the old master had data".
- archive/032 `:468-469`. It is history and is not corrected.

**Adjacent texts owned elsewhere (read, not this ticket's to fix alone):**

- `:3010-3011` "has all replicas synced" and the "no sync in progress" of the header
  (`:2980-2981`) and ADR 0007 `:519`. These belong to [T69](069-three-sync-checks-read-a-replica-field-from-the-master.md).
- `:3143-3144` and `:3166-3167`, which call the continuation safe because `verifyNewMasterReady`
  gates the delete. These belong to [T73](073-forcereplicaconnections-re-points-a-data-holder-at-an-empty-master.md)
  and [T75](075-the-sentinel-failover-reset-and-retrigger-cycle-has-no-cap.md).
- The ticket citations "recorded with T32" / "recorded in the T32 ticket" at `:2983`, `:3003` and
  `:3365`. They point at an archived ticket as the record of an open gap. Citation rewriting in
  general is [T40](040-tracked-files-cite-work-items-instead-of-adrs.md)'s. These three lines are
  rewritten in the same edit as this ticket's comment work (Work list 3), citing ADR 0007.

**Verified (read at `84a39c2`):**

- The delete path and its order, `:2984-3037`. The gate's refusals and the unconditional
  `return true` on a readable count, `:3331-3383`. The completion path, `:891-989`. The resolver
  running before dispatch, `:714-715`, and its skip while `ownFailoverInFlight`, `:797-800`. The
  hand-off `:3129` to `replaceRemainingPods` once X has a connected replica. The init container's
  Sentinel-first master lookup, `statefulset.go:288-339`.
- After a refused demotion the resolver returns the authority as `masterIdx`
  ([rolling_update.go:1478](../../internal/controller/rolling_update.go#L1478)) and the vetoed
  rogue keeps `isMaster` (`:1659-1663`); `handleMasterFailover` returns nil for a master that is
  not outdated (`:2698-2700`). So with the rolling-update state cleared, a pass in the refusal
  shape does not reach `replaceNextReplica` with O (O answers master) and goes on to
  `replaceRemainingPods` (`:750-768`). This decides the D2 argument below.
- The data roll's result steers the Sentinel roll: `NeedsRequeue` ends the pass
  ([valkey_controller.go:340-342](../../internal/controller/valkey_controller.go#L340-L342)),
  `DeferredRequeueAfter` continues it but holds the Sentinel roll (`:363`, `:465-467`), and a pause
  (no requeue at all) releases the Sentinel roll in the pass that pauses (`:405-410`, the known
  ADR 0026 D11 exception).
- `RollingUpdatePaused` is documented as an expired wait whose pause clears the rolling-update
  state and dispatches again on a fresh budget
  ([api/v1/valkey_types.go:55-68](../../api/v1/valkey_types.go#L55-L68),
  [status.md](../operations/status.md#rollingupdatepaused)). The three ADR 0026 holds with a
  bounded observation each have a condition of their own (`PodTerminationStalled`,
  `PodRecreationStalled`, `PodAvailabilityStalled`, `condition_registry.go:158-180`). The
  registry test asserts the evaluator count only for levels
  ([condition_registry_test.go:147-161](../../internal/controller/condition_registry_test.go#L147-L161))
  and a count above one only with an ownership rule (`:167-178`); an edge must have a
  presence-guarded clear site (`:120-136`).
- The `-rw` Service selects `instanceRole=master`
  ([service.go:167-168](../../internal/builder/service.go#L167-L168)), a label the sidecar sets
  from its own pod's role (`cmd/sidecar/sidecar.go:3`), so while O and X both answer master
  both are behind it (read, not measured).
- `sortReplicaCandidates` ([rolling_update.go:2508-2514](../../internal/controller/rolling_update.go#L2508-L2514))
  filters on `needsUpdate && !isMaster` only, and `replaceNextReplica` (from `:2418`) deletes its
  first candidate with no dataset check. This decides D2 below.
- Provenance: the `DBSIZE` read came in `5214d56` (2026-02-18, "fix:
  TestE2E_RollingUpdate_HA_NoDataLoss", contained in `v1.0.0` and every later tag). It already
  logged and returned verified on any count, although its comment called it "a critical safety
  check" (`git show 5214d56`). The comment corrections came in `bb6c78f` (above).
- `verifyPromotionCandidateHoldsData` (`:2843`) and `demotionRefusalReason` (`:1713-1736`, with
  `dbSizeReader` at `:1681-1693`) exist and are reusable unchanged. The first is called only from
  `handleManualFailover` (`:4030`), the non-Sentinel path.
- `RollingUpdatePaused` is an edge with one evaluator and its clear sites one frame above the
  dispatch ([condition_registry.go:196-206](../../internal/controller/condition_registry.go#L196-L206)).
  `pauseRollingUpdate` sets it and clears the rolling-update state
  ([rolling_update.go:2619-2647](../../internal/controller/rolling_update.go#L2619-L2647)).
- No page under `docs/operations/` claims a key-count gate. `rolling-updates.md:38` says only "the
  check on the new master before the former master is replaced".

**Measured 2026-09-27** (docker, `valkey/valkey:9.1.1` and `valkey/valkey:8.1.9`). The script sat
in the session scratchpad, which is not durable, so the commands and results are recorded here.
Containers `vko-last-079-v9-{o,x,r}` and `vko-last-079-v8-{o,x,r}` ran on networks
`vko-last-079-v9` and `vko-last-079-v8`. All were removed afterwards, and `docker ps -a` and
`docker network ls` filtered on `vko-last-079` show none.

| Scenario | Commands | Result (both images unless stated) |
|---|---|---|
| M1: the inputs of `verifyNewMasterReady` in the refusal shape | three servers `valkey-server --port 6379 --save '' --appendonly no` on one network; 500 keys written to O with `SET k$i v$i`; X and R empty; `REPLICAOF X 6379` on R (a pod attaching to the empty master by any route); X's `INFO replication` (role, `connected_slaves`, `master_sync_in_progress`) and `DBSIZE`, O's role and `DBSIZE`, R's `master_link_status` and `DBSIZE` read at +1, +4 and +8 s | X: `role:master`, `connected_slaves:1` from +1 s (R's link still `down` at +1 s on both, and at +4 s on 9.1.1), no `master_sync_in_progress` line in any read, `DBSIZE` 0 and readable. O: `role:master`, `DBSIZE` 500 throughout. R: link `up` by +8 s (9.1.1) and +4 s (8.1.9), `DBSIZE` 0 |

With these answers, every refusal branch of `verifyNewMasterReady` is false from +1 s on (by
reading `:3350-3379`), so it returns verified while the pod the delete takes, O, holds all 500
keys. A veto that reads O's count at the delete would see 500 against 0. M1 is not the operator
path, because no operator code ran. It shows the Valkey answers that the gate receives in that
shape.

**Not verified:**

- **The operator path end to end.** No Kind run, no `go test`, no make target. The chain is read.
  The Valkey answers at the gate are measured (M1), and the disk replacement after the reboot was
  measured in T73 M3. A unit test with one fake server per pod would reproduce it without a
  cluster (Verification). None was written or run.
- **How often the promoted master is empty while the outgoing pod still holds data.** The routes
  are read (T73, T36), and none was produced on a cluster. In the common shape, Sentinel converts
  O within 20 s (T73 M2), and then no delete-time gate can help.
- ~~**Whether a second setting site of `RollingUpdatePaused` (D2-B) changes its registry row.**~~
  *(Answered by reading in the adversarial review, 2026-09-27: the evaluator count is asserted for
  levels only, see Verified; and D2-C, now marked, adds a row of its own instead.)*
- **What `status.phase` shows during the hold.** A pass with a roll in flight writes
  "Rolling Update i/n" (`rolling_update.go:726`) and, when it continues past a bound, the status
  computation writes its own phase (`updateHAStatus`,
  [valkey_controller.go:2427](../../internal/controller/valkey_controller.go#L2427)). What
  `CheckCluster` answers with two masters, and therefore which phase a held pass ends on, was not
  read.
- **The non-Sentinel `replaceRemainingPods`** skips `verifyNewMasterReady` (`:3012`) and relies on
  `verifyPromotionCandidateHoldsData` before its promotion. Whether a data holder can reach that
  delete behind an empty master was not examined.

## Impact

This affects Sentinel-enabled clusters only, during a data-tier rolling update, at the delete of
the outgoing pod in `replaceRemainingPods`.

- **The refusal shape, then any attach.** The resolver refuses to demote O (it holds keys) toward
  an empty X. Later a pod attaches to X: a data pod restarting and asking Sentinel, Sentinel
  reconfiguring a replica, or `forceReplicaConnections` re-pointing an empty pod (T73). X shows
  one connected replica, the gate passes on `DBSIZE 0`, and O is deleted. If O is non-persistent,
  the dataset is gone with the pod. If O is persistent, it reboots as X's replica and full-syncs
  its volume empty (T73 M3). Finalization then finds one master with every replica attached and
  reports `RollingUpdateComplete`: phase `OK`, no data. This is the end state ADR 0028 Context
  measured on the non-Sentinel path, reached here one pass after the guard written to prevent it.
- **O mid-conversion.** O's link to X is down and it has not flushed yet (T73 M1: up to +4 to +7
  s). The delete takes keys that replication was about to discard anyway, so there is no extra
  loss. A delete-time veto would hold only for those seconds.
- **O already converted by Sentinel (the common empty-promotion shape).** The loss happened at
  Sentinel's reconfiguration before any gate ran, and the delete discards nothing more. No option
  of D1-A or D1-B changes that. The prevention side is D1 option D (considered, not kept).
- **Silent.** The only trace is the info log "New master verified with data ... dbsize 0". When O
  answered master, `MultipleMasters` flips back to False once O is gone. `SplitBrainDetected`
  fires only if the double master outlived 90 s first.
- **Documentation.** A reader of ADR 0007 `:523-526` looks for three stale comments that were fixed
  in the same commit, and finds none. A reader of the log line, the `handleManualFailover` comment
  or the test name concludes that the key count is checked.
- **Not affected:** clusters without Sentinel (the gate is not called, `:3012`); rolls whose
  promoted master holds keys (the veto below refuses nothing there and costs one `DBSIZE` on X
  per delete attempt with `demotionRefusalReason` unchanged, none if the count already read is
  passed in); every
  pod template (no option touches one, so nothing rolls,
  [ADR 0005](../adr/0005-upgrade-neutral-defaults-and-anti-affinity.md) D1 untouched).

## Options

### D1 - What gates the delete of the former master on the Sentinel path, beyond an attached replica?

**Mechanism.** The delete at `:3034` is the last point at which the operator can keep a
data-holding outgoing pod. What that pod holds at that moment is exactly what the delete can
discard. `verifyNewMasterReady` already reads the new master's count (`:3374`). A dataset veto
needs one more read, and only when that count is zero. The choice decides whether the rule of
ADR 0028 D1, D3 and D4 also binds a delete. It does not change the promotion, Sentinel's
reconfiguration of O (the loss in the common shape), or `forceReplicaConnections` (T73's D1).

- **A - veto at this delete (recommended).** In `replaceRemainingPods`, on the Sentinel branch,
  after `verifyNewMasterReady` passes, call `demotionRefusalReason(dbSizeReader(ctx, v), X, ps)`
  for the pod about to be deleted, before the D5 gate and the `replacing-master` write
  (`:3023-3029`). `verifyNewMasterReady` returns the pod it verified. The pod is refused when X
  holds no keys and `ps` holds some, or when any count is unreadable (ADR 0028 D3). Both empty
  passes, so an empty cluster still rolls.
  - **Cost S.** With `demotionRefusalReason` unchanged, one more `DBSIZE` on X per delete attempt
    (it reads the authority first, `:1718`) and one on `ps` only when X is empty; the two helpers
    unchanged; unit tests with revert checks.
  - **Consequences.** The refusal shape of ADR 0028 stays two visible masters (D3's accepted
    trade) instead of ending in a delete. On this path that trade is no longer bounded: ADR 0028
    D3 calls the divergence "bounded" and D8 names this delete as the end of the Sentinel-path
    refusal, so under A the double master lasts until Sentinel or a human converts or empties the
    outgoing pod, or X gains keys. Both pods sit behind the `-rw` Service while both answer
    master (read, Verified), so the divergence can grow through it. Two masters a human can see
    is the trade `demoteRogues` already states
    ([rolling_update.go:1643-1648](../../internal/controller/rolling_update.go#L1643-L1648)); the
    ADR text must say that it holds without a bound here (Work list 4). An outdated `ps` that is
    not Ready has an unreadable
    count, so when X is empty the replacement of ADR 0026 D11 is refused for it. That is the
    fail-closed cost of D3: an empty cluster whose outgoing pod crashloops after a broken spec
    holds until the pod answers or a human deletes it. For a persistent pod this is right,
    because its volume may hold what `DBSIZE` cannot read.
- **B - veto at every roll delete.** One helper in front of every data-tier roll delete
  (`replaceNextReplica`, `replaceRemainingPods`, `deleteNextPendingPod`) refuses a delete of a pod
  holding keys while the current master holds none.
  - **Cost M.** One `DBSIZE` on the master before every replica delete of every roll on every
    topology, plus tests at each of the three sites and both arms of `replaceRemainingPods`. The
    standalone delete has no second pod to compare, and ADR 0007 D6/D7 and ADR 0032 D3 already
    decide it.
  - **Consequences.** It also covers a data holder that `replaceNextReplica` deletes as an
    ordinary replica: its candidates are every outdated pod that is not `isMaster` (`:2511`), with
    no dataset check. A data holder reaches that site only while it answers `slave`, that is, as
    some master's replica, and then the next full sync replaces its keys whether it is deleted or
    not (the "O mid-conversion" case of Impact; T73 M1, M2). In phase 1 of a Sentinel roll, a
    replica holding keys next to an empty master resyncs to empty before its delete (T73 M2). So
    the extra reach protects, by reading, only keys that replication is already discarding, and
    none of it was produced.

**A is marked.** The best case for B is that one helper closes the delete of a data holder at
every roll site, so no future route to a destructive delete can bypass it. It does not survive
the reading above: every extra site B covers deletes a pod that answers `slave`, whose keys the
next sync discards anyway, so B buys no dataset A leaves exposed and taxes every replica delete of
every roll on every topology with a master `DBSIZE`. A asks exactly the pod whose delete this
finding is about, reads one extra count per final delete and a second one only when X is empty,
and sits at the site ADR 0028 D8 names as the end of the refusal on the Sentinel path. A does not
depend on D2: with the state cleared, the refusal-shape pass still reaches `replaceRemainingPods`
(Verified) and meets the veto again.

**Considered and not kept.**

- *C - compare the new master's count with a count recorded before the failover* (for example
  written next to `waitForWriteSync`). It detects the loss Sentinel's reconfiguration already
  caused, but it prevents nothing. In that shape O holds zero by the time of the delete (T73 M2),
  so a refusal protects no data. It would hold a roll on a cluster whose data is already gone, and
  on one whose clients flushed deliberately during the roll. Detection of a lost dataset is a
  different question from a gate.
- *D - prevent the empty promotion instead:* run `waitForReplicasReady`, `waitForWriteSync` and a
  candidate key check before every `SENTINEL FAILOVER`, the retrigger at `:849-887` included. It
  narrows only the retrigger route. A promoted pod that restarts empty (T36) and an attach after
  the T73 refusal still reach this delete. It is a separate decision about the retrigger, which
  T73 (Fact) and T75 (Fact) describe but no ticket owns. See Work list 5.
- *Accept and document.* ADR 0007 already records the gap. Leaving it lets ADR 0028's refusal be
  undone by the next pass on the Sentinel path, and the fix costs S.

### D2 - What does a refused delete hand over to?

**Mechanism.** [ADR 0010](../adr/0010-every-rolling-update-wait-is-bounded.md) requires every
rolling-update wait to be bounded, and requires expiry to hand over to another bounded state,
never to a cleared rolling-update state. ADR 0026 D5 is the precedent for a refusal that is never
resumed on a clock: its terminating-pod gate keeps refusing, and only the *observation* is
bounded and reported. `verifyPromotionCandidateHoldsData`, the manual-path twin, hands a refusal
to `waitOrPauseForReplicaSync`, which pauses after `syncTimeout`. Under D1-A the veto itself does
not depend on this choice: with the state cleared, a refusal-shape pass still reaches
`replaceRemainingPods` and is refused again (Verified). D2 decides what the operator reports and
what the rest of the pass does while the delete is refused. The shared part of B and C: before
the bound the refusal is a plain requeue like the other waits of this function; past it the
result is `DeferredRequeueAfter`, as in `terminationWait`
([rolling_update.go:2056-2076](../../internal/controller/rolling_update.go#L2056-L2076)), so the
status write and the steady-state checks run again and the Sentinel roll stays held (Verified).
The clock is the sync-wait bound (`ensureSyncWaitTimestamp`, `:2655-2657`). By reading it is not
armed when the failover starts: its last arm site before the trigger is `waitForWriteSync`
(`:2959`), and every pass that gets past it has first passed the clear at the end of
`waitForReplicasReady` (`:2811-2813`); nothing in the post-failover states arms it.

- **A - pause, as the manual path does.** Call `waitOrPauseForReplicaSync`.
  - **Cost XS.**
  - **Consequences.** After `syncTimeout`, `pauseRollingUpdate` writes
    `RollingUpdatePaused=True/SyncTimeout`, phase `Error` and a Warning Event, and clears the
    rolling-update state (`:2634-2639`), which is the hand-over to a cleared state that ADR 0010
    forbids. The pause returns no requeue, so every pausing pass also runs the Sentinel roll
    (`valkey_controller.go:405-410`, the ADR 0026 D11 exception): the Sentinel tier moves onto the
    spec the data tier cannot finish and spends its spare vote while two data masters stand. The
    next pass starts on a fresh budget and pauses again, so the Warning, the phase `Error` and the
    Sentinel release repeat every `syncTimeout`, with nothing on the CR saying that the same
    delete is refused each time ([T23](023-pauserollingupdate-records-no-pause.md)). With no state
    and O answering `slave`, `replaceNextReplica` deletes O as an ordinary replica with no dataset
    check (`:2511`); by reading, that loses only keys the next sync discards (D1-B).
- **B - hold under `RollingUpdatePaused` with a new reason.** Keep the rolling-update state and
  refuse on every pass. Past `syncTimeout`, set `RollingUpdatePaused=True` with a new reason (for
  example `DatasetDeleteRefused`, the message naming both pods and both counts) and emit one
  Warning Event on the edge.
  - **Cost S.** One reason constant, one setter without the state clear, the registry row's
    `evaluators` and clear-site text, unit tests, and rewriting the condition's documented meaning
    in `api/v1/valkey_types.go:55-68` and `status.md`.
  - **Consequences.** The condition then carries two meanings: an expired sync wait whose pause
    cleared the state and dispatches again on a fresh budget (as documented today), and a
    refused delete with the state kept, which never dispatches again on its own. A reader of the
    condition type can no longer tell whether the roll will retry.
- **C - hold under its own condition (recommended).** The same hold as B, reported as a new edge
  condition (for example `DatasetDeleteRefused`, reason naming the shape, message naming both
  pods and both counts), set past `syncTimeout` with one Warning Event on the transition and
  cleared, presence-guarded, where the veto next lets the delete through and at
  `clearRollingUpdateState`, as `PodTerminationStalled` is cleared from its delete gate
  (`condition_registry.go:158-167`).
  - **Cost S.** A condition-type constant and its reasons in `api/v1` (a string, no CRD schema
    change), a registry row (ADR 0027; the edge test demands the presence-guarded clear), a
    README condition-table row (the table holding `README.md:509` and `:517`), a `status.md`
    section, and unit tests.
    The collector exports every condition generically
    ([collector.go:185-192](../../internal/metrics/collector.go#L185-L192)); no shipped alert
    watches `RollingUpdatePaused` or this one (the condition-based alerts are `ReconcileBlocked`
    and `TLSMaterialStale`, `prometheusrule.yaml:51-54`, `:124-127`).
  - **Consequences.** The hold ends in one of three ways. The data holder empties (Sentinel or a
    human converts it, or replication runs), and then the delete loses nothing. A human decides
    which dataset is real, for example by making O the master through Sentinel or flushing it
    deliberately. Or X gains keys. A spec change does not end it by itself: it outdates every
    pod, `clearStaleRollingUpdateState` clears the state (`:833-842`), and the new roll meets the
    same veto at its final delete if the shape persists (read). The held state is
    `failover-triggered`, whose other arm, `handleMasterWithNoReplicas`, calls
    `forceReplicaConnections` once X loses its replica (`:3150-3159`), so the hold is only as safe
    as T73's veto of that call. The phase during the hold is not the report (Not verified). An
    Event where ADR 0028 D6 sends none for a demotion refusal is deliberate: D6 relies on
    `MultipleMasters` and `SplitBrainDetected`, and a delete hold can outlive `syncTimeout` with no
    double master (an outdated pod whose count cannot be read).

**C is marked.** A is the cheapest and mirrors the manual path, and the veto protects the dataset
under A too, so the best case for A is that nothing else is needed. It does not survive: A hands
over to a cleared state against ADR 0010, releases the Sentinel roll on every pausing pass, and
repeats a Warning and phase `Error` every `syncTimeout` for a refusal that has not changed. The
manual path can afford A because its refusal comes *before* the promotion, when nothing
destructive is pending and the Sentinel tier is not involved. B and C share the ADR 0026 D5 shape
the codebase already uses: the destructive step is never taken on a clock, and the report is what
is bounded. The best case for B is one fewer condition type. It does not survive either:
`RollingUpdatePaused` is documented, in the API type and on the status page, as a wait that
cleared the state and retries on a fresh budget, and every other hold with a bounded observation
(`PodTerminationStalled`, `PodRecreationStalled`, `PodAvailabilityStalled`) has a condition of its
own. C costs one constant and one registry row more than B and keeps each condition to one
meaning.

## Decision

Not decided.

## Work list

1. **Waits on D1 and D2:** the veto in `replaceRemainingPods` (recommended D1-A), with
   `verifyNewMasterReady` returning the verified pod, and the hold with its bounded, reported
   observation under its own condition (recommended D2-C). Add unit tests with revert checks
   (Verification).
2. **XS, no decision needed (texts that describe the gate as present):**
   - Correct the log message at `:3381` so it does not claim data. For example "New master
     verified", with the count as a field; under D1-A, a separate line for a refusal.
   - Correct "the same counts" at `:4026-4027`: the Sentinel path reads one count, the new
     master's.
   - Rename `TestVerifyNewMasterReady_AcceptsAMasterWithReplicasAndData` and rewrite its message
     at `sentinel_failover_test.go:1071-1072`: it pins that the promoted pod's `DBSIZE` is read,
     not that it is non-empty.
   - Add a test that gives the function `DBSIZE 0` and pins today's acceptance. Under D1-A it
     becomes the refusal test.
3. **XS, same edit as item 2:** rewrite "recorded with T32" / "recorded in the T32 ticket" at
   `:2983`, `:3003` and `:3365` to cite ADR 0007 Residual risks, and record it in
   [T40](040-tracked-files-cite-work-items-instead-of-adrs.md)'s count. Comments that name no
   ticket are required from 2026-09-27 on ([ADR 0034](../adr/0034-tickets-are-work-lists-that-get-archived.md)).
4. **XS, no decision needed (ADR text), dated and marked in place:**
   - ADR 0007 Residual risks `:523-526`: the three comments it names were corrected in `bb6c78f`.
     Name the three texts of item 2 instead, until they land.
   - ADR 0007 D10 correction `:392-393` and ADR 0026 Residual risks `:787-789`: the comment no
     longer calls it a check.
   - Under D1-A and D2-C, add a new D to ADR 0007, or amend ADR 0028 D4, to name the delete as a
     second guarded site. Amend ADR 0028 D8's table row "Sentinel path" (`:198`) and its
     correction of 2026-09-26 (`:206-210`, "once `verifyNewMasterReady` passes"), and ADR 0028
     D3's "the divergence is bounded", which no longer holds on the Sentinel path (D1-A
     Consequences). Amend ADR 0026 D11's *Replacement* argument, which leans on this gate
     (ADR 0026 `:791-792`). Close the ADR 0007 and ADR 0026 residual risks in place.
   - Cite no ticket in any of these ADR edits (ADR 0034).
5. **Filing, open (owned by the next filing pass; this run may create only this file):** the
   retrigger in `handleFailoverRetrigger` (`:849-887`) sends `SENTINEL FAILOVER` without the gates
   of the first trigger. T73 (Fact) and T75 (Fact) describe it as a route to an empty promotion,
   and no ticket carries it as a finding with its own decision (D1 option D). It is either a new
   file or an appendix to T75, whose cycle it belongs to.
6. **Pointer to update (the next sweep; this run modifies no other file):** T73 Work list item 4
   still reads "Filing, still open" and should link this file.
7. Close (ADR 0034): the rule into ADR 0007 / ADR 0028 (item 4); the operator-facing hold into
   [docs/operations/status.md](../operations/status.md) (a section for the new condition),
   the README condition table and [rolling-updates.md](../operations/rolling-updates.md); the
   condition's row into `conditionRegistry` (CLAUDE.md "Every condition is a level, an edge or
   history"); the contributor-facing sentence into
   CLAUDE.md "The non-Sentinel master authority, in six rules", rule 6, only if it changes;
   `git grep` `T79` and `079-` outside `docs/tickets/`; then move to `archive/`.

## Cross-ticket findings

- [T73](073-forcereplicaconnections-re-points-a-data-holder-at-an-empty-master.md): the
  `REPLICAOF` at `:3159`, one pass before this delete in the same shape. T73's B vetoes that call.
  This ticket vetoes the delete it leaves open. Both reuse `demotionRefusalReason` and
  `dbSizeReader`, and neither closes the other's gap.
- [T69](069-three-sync-checks-read-a-replica-field-from-the-master.md): the `master_sync_in_progress`
  half of `verifyNewMasterReady` cannot refuse on a master (M1 here again: no such line in X's
  `INFO`, both pins). T69's recommended option asks the replicas. A replica fully synced from an
  empty X still passes it, so T69 does not close this gap either.
- [T75](075-the-sentinel-failover-reset-and-retrigger-cycle-has-no-cap.md): the uncapped
  reset-and-retrigger cycle. Its retrigger is the first empty-promotion route (Work list 5), and
  its `:3163-3174` tail call reaches this delete in the same pass once the reset count is spent.
- [T36](036-non-persistent-master-restarts-empty.md): a promoted pod without persistence that
  restarts empty is the second route.
- [T23](023-pauserollingupdate-records-no-pause.md): D2-A would inherit its missing pause record.
  D2-C keeps the state and records the hold on the CR under its own condition.
- [T67](067-the-rolls-own-sentinel-failover-loses-acknowledged-writes.md): the writes O accepts
  after the promotion are lost by any conversion or delete of O. That loss is T67's, not this
  ticket's. T67 appendix B is the same delete at `:3034` seen from the other side: nothing asks
  whether the outgoing pod still answers master before it is deleted, and its sidecar drain then
  forces a second failover. D1-A refuses only when X is empty, so it does not touch appendix B,
  and appendix B's fix does not touch this gap.
- [T40](040-tracked-files-cite-work-items-instead-of-adrs.md): the three T32 citations of Work
  list 3.

## Verification

- D1-A: unit tests on `replaceRemainingPods` with one fake server per pod carrying a key count
  (the fleet helper of `internal/controller/split_brain_dataset_test.go:51`):
  - new master with keys: the outgoing pod is deleted;
  - both empty: deleted;
  - new master empty and the outgoing pod with keys: not deleted;
  - the outgoing pod's count unreadable while the new master is empty: not deleted;
  - the new master's count unreadable: not deleted (today's behaviour, kept).

  The third and fourth fail with the veto removed (revert check,
  [ADR 0017](../adr/0017-test-and-ci-policy.md)). A mutation that reads the new master's count
  but skips the outgoing pod's also fails the third.
- D2-C: a unit test that drives the refusal past `syncTimeout` and asserts:
  - the rolling-update state is still set;
  - the new condition is `True` and `RollingUpdatePaused` is not written;
  - no pod was deleted;
  - the result carries `DeferredRequeueAfter` and no `NeedsRequeue`, and the Sentinel roll does not
    run in that pass;
  - one Warning Event, not one per pass.

  With the state clear put back (mutation), the test fails on the state assertion; with the
  plain requeue put back, on the result assertion. A second test lets the outgoing pod's count
  drop to 0 and asserts the delete and the presence-guarded clear. The registry guard
  (`make test-unit`) goes red until the row exists.
- A pass-level unit test of the refusal shape: two masters, Sentinel names the empty one, X has
  one connected replica. After `handleRollingUpdate` the data-holding pod still exists. By
  reading, it fails at `84a39c2` (the delete at `:3034`). It has not been run: run it before the
  fix to prove it reproduces.
- Item 2: the `DBSIZE 0` test exists. It passes at `84a39c2`, pinning the acceptance, and is turned
  into the refusal test by D1-A.
  `grep -rn "verified with data\|same counts\|what has to be non-empty\|AcceptsAMasterWithReplicasAndData" internal/`
  finds nothing.
- Items 3 and 4: `git grep -n "T32" -- internal/controller/rolling_update.go` finds none of the three
  lines. `grep -n "still describe the check as present\|Its comment calls" docs/adr/0007-*.md docs/adr/0026-*.md`
  finds only struck or dated text.
- `make test-unit`, `make lint`, `make cyclo` (`replaceRemainingPods` and `verifyNewMasterReady` stay
  under 15). Then the full e2e suite on `single-node-valkey9` and `single-node-valkey8`, because the
  delete runs on every Sentinel roll and the common case must still complete. No e2e can make
  Sentinel promote an empty pod deterministically, so the unit tier carries the refusal.

## History

- 2026-09-27 (adversarial review, at `84a39c2`, working tree identical outside `docs/tickets/`):
  every load-bearing line re-read; the cited lines hold except where corrected below.
  - **Urgency next -> now:** ADR 0007 `:523-526` is a statement about tracked text, and the
    `git grep` that falsifies it is a measurement of that text, so rule 1 matches even under the
    strict reading (frontmatter). Back to next once Work list 2 and 4 land.
  - **D2 re-marked, B -> C.** B's "no extra information over C" was wrong: `RollingUpdatePaused`
    is documented in `api/v1/valkey_types.go:55-68` and `status.md` as a wait that cleared the
    state and retries on a fresh budget, which a kept-state hold contradicts, and every other
    hold with a bounded observation has its own condition. B's text for the shared hold also said
    "phase `Error`", which a pass that continues past its bound does not keep (the status write
    computes its own phase), and did not say that past the bound the result must be
    `DeferredRequeueAfter`, the ADR 0026 D5 shape it claimed; both corrected under the D2 mechanism.
  - **The D1 and D2 marks re-argued.** Both used to rest on "`replaceNextReplica` deletes the same
    pod after a cleared state". By reading, after a refused demotion the resolver returns the
    authority as `masterIdx` (`:1478`), O keeps `isMaster`, and the pass reaches
    `replaceRemainingPods` and the veto again; `replaceNextReplica` takes O only while it answers
    `slave`, when the next sync discards its keys anyway. D1-A now stands without D2, and D2-A
    loses on the ADR 0010 hand-over, the Sentinel roll it releases on every pausing pass
    (`valkey_controller.go:405-410`) and the repeated Warning.
  - **Added:** D1-A's cost that the Sentinel-path refusal is no longer bounded (ADR 0028 D3, D8)
    and that both masters sit behind the `-rw` Service (read); ADR 0028 D8's correction
    `:206-210` and ADR 0026 `:791-792` to Work list 4; the resolver's `ownFailoverInFlight`
    skip; the two missing refusal rows of `verifyNewMasterReady` (`:3387-3396`); the D2 hold's
    dependency on T73's veto (`:3150-3159`); T67 appendix B under Cross-ticket; the Verified items
    on the resolver's `masterIdx`, the Sentinel-roll steering, the condition documentation and the
    registry test; the phase question under Not verified.
  - **Corrected:** the init container's replica branch is `statefulset.go:329-339`, not
    `:328-332`; D1-A costs one more `DBSIZE` on X per delete attempt with the helper unchanged, not
    none; D1-B's "four sites"; the registry Not-verified item is answered by reading.
  - **Not verified, unchanged:** the operator path on a cluster, how often the shape occurs, the
    non-Sentinel delete. No make target, `go test`, Kind cluster, kubectl or docker was run in
    this review.
- 2026-09-27: filed from T73 (Work list item 4 and its History sweep note, which named the owner and
  the form: its own file, together with "the three code comments the ADR names") during the
  re-verification at 84a39c2. The sources were ADR 0007 Residual risks `:517-526`, ADR 0026
  Residual risks `:787-792` and archive/032 `:468-470` and `:482-483`.
  - **Moved here:** the mechanism (the gate reads the new master's `DBSIZE`, compares nothing and
    never reads the outgoing pod's), provenance `5214d56`, and the relation to ADR 0028 and T73.
  - **Re-verified now (read at `84a39c2`):** the delete path `:2984-3037`, the gate `:3331-3383`,
    the completion path `:891-989`, the resolver hand-off `:714-715` and `:3129`, the init
    container's Sentinel-first lookup, `sortReplicaCandidates` without a dataset check, and the
    `RollingUpdatePaused` registry row.
  - **Measured (docker, both pins):** M1, the Valkey answers the gate receives in the refusal
    shape: an empty X with `connected_slaves:1` from +1 s and no `master_sync_in_progress` line,
    next to O holding 500 keys as master.
  - **Found not as stated:** the three comments ADR 0007 `:523-526` names were corrected in
    `bb6c78f`, the commit that added that sentence. The three texts that still describe the gate
    at HEAD are the log line `:3381`, "the same counts" at `:4026-4027`, and the test name and
    message at `sentinel_failover_test.go:1063` and `:1071-1072`. ADR 0007 `:392-393` and ADR 0026
    `:787-789` are stale in the same way.
  - **New in this file:** the case analysis (the gate helps only while O is not X's synced
    replica), D1 (A recommended, B, and C and D considered) and D2 (B recommended).
  - **Frontmatter derived:** severity high, security none, effort M, and urgency next by rule 3
    under the strict reading of rule 1. The repository reading gives now; both are in the
    frontmatter comment.
  - **Not verified:** the operator path on a cluster, how often the shape occurs, the registry row
    under D2-B, and the non-Sentinel delete. No make target, `go test`, Kind cluster or kubectl was
    run; every docker container and network created was removed. T73 Work list item 4 still reads
    "Filing, still open", because this run may create only this file (Work list 6).
