---
id: T73
title: forceReplicaConnections re-points every other pod at the new master with no role or dataset check, in the same pass in which the ADR 0028 guard refused that demotion
state: analysed       # the call path is read end to end at 84a39c2, the Valkey side is measured on both pinned images (docker, 2026-09-27), and the one open decision carries a marked option
severity: high        # impact if never fixed: every copy of the dataset outside the promoted pod is replaced by that pod's dataset, and in the refusal shape that is an empty one; the cluster then ends phase OK with nothing in it, as in ADR 0028 Context. Persistence does not protect: measured in docker on both pins for rdb and aof with the operator's config lines (M3, 2026-09-27) - the replaced dataset is what is on disk afterwards. The trigger is narrow and by reading (Impact)
security: none        # no principal involved: the operator's own REPLICAOF breaks ADR 0028 D1, a data-integrity rule, not a trust boundary; same class as T36, T62, T67 and T69. Not live: D1 is not a guarantee of the security model (no page under docs/security/ states it). Not in doubt between hardening and boundary: no principal gains a verb or an object through it, and a client that can empty the promoted pod can already flush the dataset directly
urgency: now          # rule 1, second clause, as this repository applies it: ADR 0028:284 ("The Sentinel path is protected by D1"), the comments at rolling_update.go:3143-3144 and :3166-3167 and the test text at sentinel_failover_test.go:1279-1281 and :1301 (the continuation is safe) are false - the REPLICAOF :3159 sends after the refusal is read in the code, and what that REPLICAOF does to the data holder is measured in docker on both pins (M1, M3), the same basis as T67; false-by-reading alone was taken as rule 1 in 018, 023, 059 and 075. Read strictly (the operator path itself is not measured) rule 1 does not match and rule 3 gives next: severity high, trigger live in released code since 2926077 (2026-02-28, v1.1.0 and every later tag) on every Sentinel cluster during a data-tier roll. The repository reading is applied, as in T75; the owner may rule the strict one. Back to next (rule 3) once Work list 2 and 3 land. Rule 2 does not match
effort: S             # the recommended option: one veto in forceReplicaConnections reusing demotionRefusalReason and dbSizeReader, unit tests with revert checks, the adjusted test at sentinel_failover_test.go:1246, comment and ADR 0028 corrections (the comment lines shared with T75 edited once); no e2e can make Sentinel promote an empty pod deterministically (Verification)
blocked-by: decision  # D1, below
filed-from: T62 (Work list 8a and its Cross-ticket "Adjacent" bullet), found by the design skeptic of the T62 re-verification of 2026-09-27
opened: 2026-09-27
decided:              # not recorded - no decision yet
done:                 # not recorded - not done
---

# T73 - forceReplicaConnections re-points every other pod at the new master with no role or dataset check, in the same pass in which the ADR 0028 guard refused that demotion

Filed on 2026-09-27 from [T62](062-resetsentinelstate-falls-back-to-sentinel-reset.md) (Work
list 8a, Cross-ticket "Adjacent"), where the design skeptic of the T62 re-verification found it
by reading at `84a39c2`. This file is now the record of the finding; T62's Work list 8a and its
Cross-ticket bullet ~~are to point here (at this review they still read "needs its own file")~~
point here *(checked in the sweep of 2026-09-27)*. Every
location below was re-read at `84a39c2` (only `docs/tickets/` differs in the working tree, so
the working-tree lines are the `84a39c2` lines).

## Fact

**Mechanism.** `forceReplicaConnections`
([`rolling_update.go:3192`](../../internal/controller/rolling_update.go#L3192)) walks the pod
slice it is handed and sends `REPLICAOF <named master FQDN> <port>` to every pod that exists and
is Ready, terminating pods included, except the named master
([`rolling_update.go:3206-3220`](../../internal/controller/rolling_update.go#L3206-L3220), the
skip at [`:3210`](../../internal/controller/rolling_update.go#L3210):
`ps.name == masterPodName || !ps.exists || !ps.reachable()`). It asks no pod its role and reads
no key count, neither on the target nor on the named master. It is best-effort: a failed
`REPLICAOF` is logged and the loop moves on (`:3215-3219`). Its doc comment says "every ready
non-master pod" (`:3189-3190`); the code has no non-master condition. `REPLICAOF` makes the
target full-sync from the named master, which replaces the target's whole dataset with the
master's (measured, below).

**The path through `:3159`, traced (read at `84a39c2`).** One pass of `handleRollingUpdate`
(Sentinel clusters only, `:701`):

1. `collectPodStates` scans every data pod; a pod is `isMaster` on an `INFO role:master` answer,
   terminating or not, or on its label when `INFO` failed
   ([`rolling_update.go:1951-1962`](../../internal/controller/rolling_update.go#L1951-L1962)).
2. The resolver runs before any dispatch:
   `resolveSplitBrainUnlessFailingOver(ctx, v, pods, masterIdx, sentinelMaster)`
   ([`rolling_update.go:714-715`](../../internal/controller/rolling_update.go#L714-L715)), with
   Sentinel's master pointer (`getSentinelMasterPodName`, `:1741-1786`, the first Sentinel that
   answers) as the authority. It skips resolution only while `ownFailoverInFlight` is true
   (`:797-800`), which is `state == failover-triggered` **and** `!isReplicaReconnectTimedOut(v)`
   (`:817-822`).
3. `detectAndResolveSplitBrain` (`:1415`) picks the real master among the non-terminating
   masters - drain stamp, then the named authority, then the most connected slaves
   (`:1437-1471`) - and `demoteRogues` (`:1654-1676`) asks `demotionRefusalReason`
   ([`rolling_update.go:1713-1736`](../../internal/controller/rolling_update.go#L1713-L1736))
   before every `REPLICAOF`: an authority holding zero keys while the rogue holds some, or any
   unreadable count, is a refusal. **This is the ADR 0028 guard, and it runs in this pass**:
   `forceReplicaConnections` at `:3159` fires only when `isReplicaReconnectTimedOut` is true
   (`:3150`), the same predicate whose negation keeps the resolver out, so past the 90 s clock
   the resolver has already run. A refusal logs "Refusing to demote a rogue master: the demotion
   would discard the only dataset" (`:1661`) and leaves `isMaster` set on the local slice
   (`:1663`, `:1674`, ADR 0028 D8). Nothing else records it.
4. With state `failover-triggered` or `replacing-master` the pass goes to `handlePostFailover`
   (`:746-747`), which **discards the resolver's slice** (its parameter is `_ []podState`,
   [`rolling_update.go:3071`](../../internal/controller/rolling_update.go#L3071)) and scans again
   (`freshPods`, `:3088`). The first pod on the current template that is `available()` and
   answers `role:master` is taken as the new master X (`:3099-3110`).
5. `handleNewMasterFound`: X with `connected_slaves == 0` goes to `handleMasterWithNoReplicas`
   (`:3122-3123`). Past `replicaReconnectTimeout` = 90 s from the failover timestamp (`:149`,
   `:3539-3541`) that function calls
   [`forceReplicaConnections(ctx, v, ps.name, allPods)`](../../internal/controller/rolling_update.go#L3159)
   at `:3159` with the fresh scan, then `resetSentinelState` toward X (`:3161`).
6. So the old master O, which step 3 refused to demote toward X, gets `REPLICAOF X` later in the
   same pass - after a phase write, a StatefulSet read and a second `INFO` scan of every pod (not
   timed) - together with every replica that holds a copy of O's dataset.

**The state in which the discard happens.** All of the following in one pass:

- Sentinel enabled, a data-tier roll with state `failover-triggered` (or `replacing-master`) and
  a failover timestamp older than 90 s. Entering `replacing-master` does not rewrite the stamp
  (it is written only by `setFailoverTriggered`, `incrementReconnectResetCount` and
  `setFailoverTimestamp`, `:3514`, `:3249`, `:3523`), so in that state `:3159` fires on the first
  pass past 90 s from the last trigger or reset whose promoted master shows no connected replica.
- X: on the current template, Ready, not terminating, answering `role:master` with
  `connected_slaves:0`, first by ordinal among such pods.
- O: an existing Ready pod other than X (terminating allowed) that holds keys X does not hold.
  In the ADR 0028 refusal shape X holds zero keys and O holds some.

**Whether the ADR 0028 guard runs before it, per case (read):**

| Case | Guard runs in the pass? | Outcome at `:3159` |
|---|---|---|
| O counted master in step 1, authority is X (Sentinel's pointer names X, or the tiebreak picks X), X empty, O not empty | yes, and it refuses | the refusal is logged, then `REPLICAOF X` on O discards the dataset |
| as above, a key count unreadable (ADR 0028 D3) | yes, and it refuses fail-closed | `REPLICAOF X` on O is sent anyway, with nothing known about X's dataset. If the TLS config cannot be built, both `dbSizeReader` (`:1682-1686`) and `forceReplicaConnections` (`:3195-3199`) give up, so that sub-case sends nothing |
| authority is O (Sentinel still names O, or O has more connected slaves) | yes, and it demotes X toward O (O holds keys, no refusal) | X answers `role:slave` in step 4, no new master is found, `:3159` is not reached |
| O not counted master in step 1 (its `INFO` failed and its label does not claim master) but Ready | no - one master, no split brain | `REPLICAOF X` on O |
| the 90 s boundary falls between `:797` and `:3150` of the same pass | no - suppressed by `ownFailoverInFlight` | `REPLICAOF X` on O; the window is the time between the two clock reads |
| O is a replica holding a copy (a replica of the old master, never re-pointed by Sentinel) | not applicable - the guard covers only masters | `REPLICAOF X` discards that copy too; with O's own copy gone as well, nothing is left |

**What seals the loss on the next pass (read).** After `:3159` the re-pointed pods attach to X
(measured: `connected_slaves:2` one second after the `REPLICAOF`). The next pass takes
`handleNewMasterFound` to `replaceRemainingPods` (`:3129`), whose gate before deleting the
outgoing pod is `verifyNewMasterReady`
([`rolling_update.go:3331-3397`](../../internal/controller/rolling_update.go#L3331-L3397)): it
requires a connected replica and no `master_sync_in_progress` on X (a replica-side field that
Valkey never emits in a master's `INFO`, so that half cannot refuse,
[T69](069-three-sync-checks-read-a-replica-field-from-the-master.md)),
reads X's `DBSIZE`, logs it and refuses only when it is unreadable (`:3355-3383`, comment
`:3361-3366`). The outgoing pod is deleted
([`rolling_update.go:3034`](../../internal/controller/rolling_update.go#L3034)). When the reset
count is already spent, the same pass can get there: `:3163-3174` tail-calls
`replaceRemainingPods` right after the `REPLICAOF` and the Sentinel reset, and its
`verifyNewMasterReady` passes as soon as one re-pointed pod has attached (by reading; M1 shows the
attach within one second, the Sentinel reset in between is not timed). That gate is
[ADR 0007](../adr/0007-failover-aware-rolling-update.md) Residual risks (`:517-526`, "The
Sentinel path deletes the former master with no key-count gate", also ADR 0026 Residual risks
`:787-792`); at `:3159` the data is already gone before it is asked.

**How X can be the empty one (read, narrow).** The first trigger of the roll's failover waits
for every replica to be ready and synced and for a `WAIT` on the master
(`handleMasterFailover`, `waitForReplicasReady` and `waitForWriteSync`,
[`rolling_update.go:2710-2719`](../../internal/controller/rolling_update.go#L2710-L2719)). The
retrigger after a 30 s failover timeout does not: `handleFailoverRetrigger`
([`rolling_update.go:849-887`](../../internal/controller/rolling_update.go#L849-L887)) checks
only the elapsed time and Sentinel's replica count before `SENTINEL FAILOVER`, and proceeds
without the count once the awareness wait has stalled (`:865-869`). Sentinel selects a
replica by `replica-priority`, then replication offset, then run id, and drops a replica
disconnected longer than `down-after-milliseconds * 10` plus the SDOWN time
(<https://valkey.io/topics/sentinel/>, "Replica selection and priority", fetched 2026-09-27); it
never reads a key count, and it promotes an empty replica when that is the one it selects
(measured, below). A second route is a promoted pod without persistence that restarts before its
replicas attach: its init container asks Sentinel, which names the pod itself, and it boots as
an empty master ([`statefulset.go:288-332`](../../internal/builder/statefulset.go#L288-L332),
[T36](036-non-persistent-master-restarts-empty.md)'s mechanism).

**Why O can still answer master at 90 s (read plus measured).** A healthy Sentinel tier converts
the old master itself: measured below, O went from master to replica of the promoted pod
between 5 s and 20 s after a forced failover, by +20 s in every run on both images, and its
dataset went with it.
The `:3159` precondition - no replica connected to X for 90 s - is itself evidence that Sentinel
did not carry out its reconfiguration, so the case in which O is still master at `:3159` is the
case in which Sentinel is not demoting it. When Sentinel does, the loss in the empty-promotion
shape is Sentinel's own and is the ADR 0007 residual risk, not this call.

**Verified (read at `84a39c2`):**

- The call path, the skip condition and the lost refusal, steps 1-6 above.
- The predicate pairing: `ownFailoverInFlight` (`:817-822`) and the `:3150` branch read the same
  `annotationFailoverTimestamp` against the same `replicaReconnectTimeout`; the comment of
  `resolveSplitBrainUnlessFailingOver` says so (`:789-793`).
- `forceReplicaConnections` has two callers, `:983` and `:3159`
  (`git grep -n forceReplicaConnections -- internal`). At `:983` (`checkFinalizationTopology`,
  stalled finalization) the pass has counted exactly one master (`:950-951`, `:970-983`); a
  rogue the resolver refused to demote keeps `isMaster`, so that pass counts two and takes the
  `:951` branch instead. `:983` therefore cannot follow a refusal of the same pass. It can still
  re-point a data-holding pod the scan did not count as master (`INFO` failed, label not
  master) at a master holding fewer keys - not reproduced, and not the refusal shape.
- Provenance: `forceReplicaConnections` and its call in `handleMasterWithNoReplicas` came in
  `2926077` (2026-02-28, "fix: e2e tests"; `git log -S`), contained in `v1.1.0` and every later
  tag; the ADR 0028 guard came in `2051a34` (2026-08-26) and did not touch this function.
- ADR 0025 (`:215-217`, "a best-effort `REPLICAOF` of the new master to every other reachable
  pod, the old master included") and ADR 0028 D8 (`:206-209`, corrected 2026-09-26, "forces every
  other reachable pod onto the new master") both record what this branch sends; neither says
  that it bypasses ADR 0028 D1. ADR 0028 Residual risks (`:284`) says "The Sentinel path
  is protected by D1" - false at `:3159` by this reading.
- Comments that state the opposite: `handleMasterWithNoReplicas`, its doc comment
  ([`rolling_update.go:3143-3144`](../../internal/controller/rolling_update.go#L3143-L3144),
  "verifyNewMasterReady will still gate the old-master deletion until replication is confirmed")
  and inline (`:3166-3167`, "verifyNewMasterReady will block the final deletion until
  replication is confirmed, so this is safe"), and the unit test
  `TestHandleMasterWithNoReplicas_ProceedsAfterTheLastResetButStillGatesTheDelete`, its doc
  comment ([`sentinel_failover_test.go:1279-1281`](../../internal/controller/sentinel_failover_test.go#L1279-L1281),
  "verifyNewMasterReady still holds the deletion back, which is what makes proceeding safe") and
  its assertion message ([`:1301`](../../internal/controller/sentinel_failover_test.go#L1301),
  "proceeding is only safe because the master verification still blocks the delete"). The
  verification gates on replication, not on the dataset. Comments that describe the function as
  re-pointing only non-master pods, which the code does not check: its doc comment (`:3189-3190`,
  "every ready non-master pod"), the header of `handleMasterWithNoReplicas` (`:3137`, "all
  non-master pods"), the inline comment before the call (`:3157`, "every non-master pod") and the
  comment above the `:983` call (`:975`, "all non-master pods"). [T75](075-the-sentinel-failover-reset-and-retrigger-cycle-has-no-cap.md)
  corrects `:3142-3144` and `sentinel_failover_test.go:1279-1281` for another false half (the
  loop is not broken), so both tickets edit the same lines.
- `TestHandleMasterWithNoReplicas_ForcesReconnectAndResetsSentinelOnTimeout`
  ([`sentinel_failover_test.go:1246-1277`](../../internal/controller/sentinel_failover_test.go#L1246-L1277))
  pins the current behaviour: `REPLICAOF` to every pod except the promoted master (assert
  `:1261-1263`), with no key count in play. ADR 0028's unit tests
  (`split_brain_dataset_test.go:189-265`) and its e2e
  (`test/e2e/split_brain_dataset_test.go:52`, a cluster without Sentinel) exercise the resolver
  only.
- The log line of the discard is "Sent REPLICAOF to pod" at the default level (`:3218`); no
  Event and no condition names it.

**Measured 2026-09-27** (docker, `valkey/valkey:9.1.1` and `valkey/valkey:8.1.9`; scripts in the
session scratchpad, which is not durable, so the commands and results are recorded here;
containers `vko-file-073-*` and `vko-file-073s-*` at filing, `vko-file-073r-*`, `vko-file-073rs-*`
and `vko-file-073p-*` in the adversarial review's reruns and M3, all removed afterwards,
`docker ps -a` and `docker network ls` show none). In M1 and M2 every server runs
`valkey-server --port 6379 --save '' --appendonly no` on one docker network; M3's persistent pod
runs the operator's persistence lines (below).

| Scenario | Commands | Result (both images unless stated) |
|---|---|---|
| M1: the `:3159` shape | X a separate empty master, O a master, R `REPLICAOF O 6379`; then 500 keys written to O with `SET k$i v$i` and 9 s for R's initial sync (diskless sync delay); then, as `forceReplicaConnections` does, `REPLICAOF X 6379` on O and on R; `DBSIZE` and `master_link_status` read at +1, +4, +7, +10 s | before: O `master`/500, R `slave`/500, X `master`/0 with `connected_slaves:0`. Both `REPLICAOF` answer `OK`; X has `connected_slaves:2` at +1 s, where O and R still hold 500 with the link down; both are at 0 with the link up by +7 s in every run (filing run: 500 at +4 s and 0 at +7 s on both images; review rerun: R 0 at +4 s and O 0 at +7 s on 9.1.1, both 0 at +4 s on 8.1.9) - which read first sees 0 depends on where X's 5 s diskless sync delay ends. O's log: "Flushing old data" and "keys loaded: 0" (9.1.1), "Full resync from primary", "Flushing old data", "keys loaded: 0" (8.1.9) |
| M2: Sentinel promotes an empty replica and converts the old master itself | O master with 500 keys; X `--replicaof O`; R `--replicaof O --replica-priority 0` (not eligible); three Sentinels from a file with `port 26379`, `sentinel monitor mymaster O 6379 2`, `down-after-milliseconds 5000`, `failover-timeout 60000`, `parallel-syncs 1`, `resolve-hostnames yes`, `announce-hostnames yes`; 12 s; X made empty while still a replica (`CONFIG SET replica-read-only no`, `FLUSHALL`); `SENTINEL FAILOVER mymaster` on s1; roles and `DBSIZE` at +2 ... +45 s | `FAILOVER` answers `OK`; `+selected-slave` X, `+switch-master` to X within about 7 s (9.1.1) and 2 s (8.1.9) of it (rerun: 6.4 s and 2.2 s). X is `master`/0 from +2 s. O is still `master`/500 at +5 s, is `slave` at +10 s (8.1.9, filing run) or +20 s (9.1.1; both images in the rerun), and `slave`/0 by +20 s on both in both runs. R: `slave`/0 by +10 s on 9.1.1; `slave`/500 to +45 s on 8.1.9 (both runs) |
| M3: a persistent O (review, 2026-09-27) | X an empty master as in M1; O `valkey-server --port 6379 --repl-diskless-sync yes --repl-diskless-sync-delay 5 --dir /data` plus, for `rdb`, `--save '900 1' --save '300 10' --save '60 10000' --dbfilename dump.rdb --appendonly no` (then `SAVE`), and for `aof`, `--save '' --appendonly yes --appendfilename appendonly.aof --appendfsync everysec` (the lines of `persistenceConfig`, [`configmap.go:187-246`](../../internal/builder/configmap.go#L187-L246)); 500 keys; `REPLICAOF X 6379` on O; 10 s; then `docker restart` of O, whose command line carries no `replicaof`, so it boots a master from its own disk | `rdb`: `dump.rdb` 5379 bytes before, 172 bytes after the sync; `aof`: base `appendonly.aof.1.base.rdb` plus a 16307-byte incr file before, a new `appendonly.aof.2.base.rdb` (172 bytes) and an empty incr file after. After the restart O is `master` with `DBSIZE` 0 in all four runs (two images, two modes); its log: "DB loaded from disk" (`rdb`) or "DB loaded from base file appendonly.aof.2.base.rdb" (`aof`), "keys loaded: 0". The full sync replaces the files on disk as well as the memory |

M2's R on 8.1.9 is an artefact of the construction: X's emptiness is a local write on a replica
and never entered the replication stream, so R's partial resync from X keeps its copy; a pod that
comes back empty (the routes above) has no such shared history. M2 is not the `:3159` path - the
operator ran nothing - it shows the two Valkey facts the path rests on: Sentinel promotes an
empty pod, and a demotion toward it discards the demoted dataset, whoever sends it.

**Not verified:**

- **The operator path end to end.** No Kind run, no `go test`, no make target. The chain is
  read; the Valkey behaviour at each step is measured (M1, M2, M3). A unit test with a fake server
  per pod would reproduce it without a cluster (Verification); none was written or run.
- **How often X is empty in practice.** The two routes (the unguarded retrigger, a promoted pod
  without persistence restarting) are by reading; neither was produced. Whether Sentinel's
  replica selection can pick a restarted replica with offset 0 over synced ones depends on
  which replicas are eligible at that moment (upstream documentation, not measured).
- **The tiebreak variant**: X and O both at `connected_slaves:0` with X on the lower ordinal and
  no Sentinel answering (`mostConnectedMaster`, `:1622-1638`). Read, not produced.
- **Persistence on a cluster.** M3 measures the Valkey side in docker (the files in `/data` are
  replaced by the sync); that a PVC-backed pod behaves the same is by reading, not run.
- **Writes O accepts after the promotion.** While O answers master it keeps taking writes (no
  write fencing, [T12](012-no-write-fencing-min-replicas-to-write-as-an-opt-in-field.md)); any
  demotion of O loses them, Sentinel's included. That loss is T12's, not this ticket's, and
  whether a client reaches O in that window depends on the `-rw` Service label (not examined).

## Impact

Sentinel-enabled clusters only, during a data-tier rolling update, on the no-replica branch of
the post-failover handler past 90 s:

- **The refusal case.** When the resolver refuses to demote O toward an empty X, `:3159` does
  exactly that demotion in the same pass, and re-points every replica holding a copy as well.
  Every pod then holds X's empty dataset (M1). The next pass finds X with connected replicas,
  `verifyNewMasterReady` passes on an empty master, and the outgoing pod is deleted; the roll
  completes and the cluster reads phase `OK` with no data - the end state ADR 0028 Context
  measured on the non-Sentinel path, reached here past the guard that was written to prevent it.
- **The fail-closed case.** An unreadable key count is a refusal under ADR 0028 D3 because the
  destructive direction needs positive justification; `:3159` sends the `REPLICAOF` with no
  count at all.
- **The unguarded cases.** O not counted master, the 90 s boundary between two clock reads, and
  replicas holding the only remaining copies are re-pointed without any guard in the pass.
- **Silent.** The only trace is an info log line ("Refusing to demote ..." followed by "Sent
  REPLICAOF to pod"). `MultipleMasters` flips back to False once O answers `slave`, and
  `SplitBrainDetected` fires only if the double master outlived 90 s by that pass.
- **Documentation.** ADR 0028 Residual risks (`:284`) tells a reader that D1 protects the
  Sentinel path; a contributor who trusts it will not look at `:3159`. Two code comments and two
  test texts call the continuation safe (Fact, Verified).
- **Not affected:** clusters without Sentinel (`forceReplicaConnections` has no caller on that
  path); a roll whose promoted master has a connected replica before 90 s (the common case);
  a promoted master that holds the data (the `REPLICAOF` then repairs, which is what it was
  written for). No option below touches a pod template, so none rolls the fleet
  ([ADR 0005](../adr/0005-upgrade-neutral-defaults-and-anti-affinity.md) D1 untouched).

## Options

### D1 - What does `forceReplicaConnections` do when the master it names holds no keys and a pod it would re-point holds some?

**Mechanism.** Today the function re-points every Ready pod except the named master, with no
role and no key count (`:3206-3212`), and the pass that calls it at `:3159` has thrown away the
resolver's refusal (`:3071`, `:3088`). The choice decides whether the dataset rule of
[ADR 0028](../adr/0028-a-demotion-may-not-discard-the-only-dataset.md) D1, D3 and D4 - "whatever
chose the authority" - also binds this second `REPLICAOF` site. It does not change the benign
case the function was written for (the named master holds keys, replicas are slow to follow
Sentinel), where it keeps re-pointing everyone. It does not change the `resetSentinelState` call
at `:3161` (T62's D1), the reset counter and its missing overall cap (ADR 0010 Residual risks,
[T75](075-the-sentinel-failover-reset-and-retrigger-cycle-has-no-cap.md)), or
`verifyNewMasterReady`
([T79](079-the-sentinel-roll-deletes-the-former-master-with-no-key-count-gate.md), Cross-ticket).
Under every option below the refused shape holds as two masters that both accept writes, with
`MultipleMasters` True and `SplitBrainDetected` after 90 s, which is the trade ADR 0028 D3 and D8 already accepted for the
resolver ("two masters a human can see beat one dataset silently discarded"). Both options reuse
`dbSizeReader` (`:1681-1693`) and `demotionRefusalReason` (`:1713-1736`) as they are.

- **A - veto per target.** Before each `REPLICAOF`, call
  `demotionRefusalReason(keys, namedMaster, target)`; skip a refused target, re-point the rest.
  Cost XS-S: one `DBSIZE` on the named master per target (the helper reads it first and stops
  when it holds keys), unit tests with revert checks. Consequences: a data-holding pod is never
  re-pointed at an empty master here. **It leaves the loss one pass away** whenever one target is
  itself empty - a restarted replica, the typical company of an empty promotion: that target
  attaches, X shows `connected_slaves:1`, the next pass's `verifyNewMasterReady` passes, and the
  outgoing pod that A just protected is deleted (`:3013-3034`). A saves the dataset only when
  every other pod holds it.
- **B - veto the whole call (recommended).** Read the named master's `DBSIZE` once. If it holds
  keys, proceed exactly as today. If it holds none, or the count is unreadable, read every other
  pod that exists - every target, and also an existing pod the loop skips because it is not
  Ready (`:3210`), since the outgoing pod is deleted whatever its readiness (ADR 0026 D11) and a
  persistent one comes back from its volume as X's replica; if any of them holds keys or cannot
  be counted, send no `REPLICAOF` at all this pass and log once which pod holds what. Cost S: the
  same helpers, one `DBSIZE` per pass in the common case and one per pod only in the empty case;
  unit tests for five shapes (named master holds keys: all re-pointed; both empty: all
  re-pointed; empty master and one data holder: nobody re-pointed; empty master and a data
  holder that is not Ready: nobody re-pointed; unreadable count: nobody re-pointed), each of the
  last three failing with the veto removed; `TestHandleMasterWithNoReplicas_ForcesReconnectAndResetsSentinelOnTimeout`
  gets key counts on its fake servers. Consequences: in the refused shape nothing attaches to X
  through this function, so the operator no longer unlocks the delete itself; the no-replica
  branch keeps cycling every 90 s as it does today, and after `maxReconnectResets`
  `replaceRemainingPods` waits in `verifyNewMasterReady` for a replica that the operator no
  longer sends (that wait is the unbounded plain requeue ADR 0025 records). **B does not close
  the delete:** a pod that attaches to X by another route - a data pod that restarts and whose
  init container asks Sentinel, which names X, or Sentinel reconfiguring a replica itself -
  still passes `verifyNewMasterReady`, and the outgoing data holder is then deleted; that is the
  ADR 0007 key-count gap, filed as
  [T79](079-the-sentinel-roll-deletes-the-former-master-with-no-key-count-gate.md), not this call.
  An empty cluster with one pod not answering waits until that pod answers, the fail-closed cost of ADR 0028 D3. Both callers are
  covered, because the veto sits in the function: at `:983` a stalled finalization with an empty
  single master and a data-holding non-master pod re-points nothing either.

**B is marked.** The case for A: it keeps the repair for every pod that has nothing to lose, so
in a mixed shape a replica still reaches X and the roll moves on, where B holds the whole tier in
the no-replica cycle. That progress is the failure: the empty pod A re-points is what lets
`verifyNewMasterReady` pass, and the next delete takes the data holder A has just spared
(`:3013-3034`) - A protects O at `:3159` and hands the same dataset to the delete one pass later
through the one empty pod an empty promotion usually comes with. B keeps the topology where the
resolver left it, which is the point of the refusal, and the cost of holding (two visible
masters, the 90 s cycle) is the one ADR 0028 D3 and D8 already accepted. The extra cost over A is
one conditional loop in the empty case and nothing in the common case, and B matches the fail
direction D3 set: an unreadable count is a refusal, not a demotion. The mark survives the
runner-up's best argument.

**Considered and not kept.**

- *Skip targets that answer `role:master`, leaving masters to the resolver.* It spares O and
  still re-points O's replicas, which both discards their copies and unlocks the delete of O on
  the next pass; the dataset is lost one pass later.
- *Carry the resolver's refusal into `handlePostFailover` and skip the refused pods.* It covers
  only the first case of the table; the fail-open cases (O not counted master, the boundary,
  replica copies) stay, and it couples the post-failover handler to the resolver's slice, which
  `handlePostFailover` discards on purpose to read fresh roles (`:3074-3075`).
- *Delete the `:3159` call.* It removes the operator's only repair for the benign case it was
  written for (`2926077`, the CI stall where Sentinel never re-pointed the replicas); with it
  gone, that case waits in `verifyNewMasterReady` without a bound. Trading a common stall for a
  narrow loss is out of proportion when B removes the operator's own discard and keeps the
  repair.

## Decision

Not decided.

## Work list

1. **Waits on D1:** the veto in `forceReplicaConnections` (recommended B), its unit tests with
   revert checks, and the rewrite of the assertions at
   [`sentinel_failover_test.go:1246-1277`](../../internal/controller/sentinel_failover_test.go#L1246-L1277).
2. **XS, no decision needed (comments):** correct the doc comment of `forceReplicaConnections`
   (`:3189-3191`, "every ready non-master pod": it re-points every Ready pod except the named
   master, and under D1 what it refuses), and the same "non-master" wording at `:3137`, `:3157`
   and `:975`; correct "will still gate the old-master deletion" at `:3143-3144`, "so this is
   safe" at `:3163-3167`, and the test texts at `sentinel_failover_test.go:1279-1281` and
   `:1300-1301` (the verification gates on replication, not on the dataset). With B landed, state
   the dataset veto instead. `:3142-3144` and `sentinel_failover_test.go:1279-1281` are also
   T75's Work list item (the loop is not broken): whichever ticket lands first corrects both
   halves, and the other records it.
3. **XS, no decision needed (ADR text):** ADR 0028 Residual risks (`:284`), dated and marked in
   place: D1 did not protect the Sentinel path at `handleMasterWithNoReplicas`, whose
   `REPLICAOF` ran after a refusal of the same pass; under D1-B of this ticket, amend D1/D4 to
   name the second site and add a References line. ADR 0028 D8's correction (`:206-209`) and
   ADR 0025 (`:215-217`) gain "subject to the dataset veto" once B lands. No ticket citation in
   either ADR ([ADR 0034](../adr/0034-tickets-are-work-lists-that-get-archived.md)).
4. **Filed as
   [T79](079-the-sentinel-roll-deletes-the-former-master-with-no-key-count-gate.md)**
   *(final pass 2026-09-27)*: the ADR 0007 residual risk "The Sentinel path deletes the former
   master with no key-count gate" (`:517-526`, also ADR 0026 Residual risks `:787-792`). It is this ticket's family - the same empty
   promotion, the destructive step one pass later, and B's own remaining route (Options) - with a
   separate decision (what gates the delete, and what bounded state a refused delete hands over
   to). The texts that still describe that gate as present are T79's Work list, not this one.
5. Close ([ADR 0034](../adr/0034-tickets-are-work-lists-that-get-archived.md)): the rule into
   ADR 0028 (item 3), the contributor-facing sentence of CLAUDE.md "The non-Sentinel master
   authority, in six rules", rule 6, only if it changes; `git grep` `T73` and `073-` outside
   `docs/tickets/`, then move to `archive/`.

## Cross-ticket findings

- [T62](062-resetsentinelstate-falls-back-to-sentinel-reset.md): `:3161` resets Sentinel toward
  the same X right after `:3159`. T62's recommended D1-B gate verifies that X is available and
  answers `role:master`; it does not ask for keys, so under T62 alone Sentinel is still pointed
  at an empty X, and a data pod that restarts then asks Sentinel and boots as X's replica. B here
  and T62's gate compose; neither closes the other's gap. T62's D1 text says it does not change
  `forceReplicaConnections`; this ticket is where that function is decided.
- [T36](036-non-persistent-master-restarts-empty.md): a promoted pod without persistence that
  restarts empty is one route into the empty-X shape (Fact).
- [T12](012-no-write-fencing-min-replicas-to-write-as-an-opt-in-field.md): the writes O accepts
  while two masters stand are lost by any demotion of O; B keeps both masters writable longer in
  the refused shape, which is ADR 0028 D3's accepted cost.
- [T34](034-e2e-fixtures-wait-on-controller-state-after-a-pod-delete.md) lists the callers of
  `resetSentinelState`; nothing here changes them.
- [T75](075-the-sentinel-failover-reset-and-retrigger-cycle-has-no-cap.md): the uncapped
  no-replica cycle calls `forceReplicaConnections` once per round; its recommended B moves the
  cap check in front of that call and `resetSentinelState`. B here and T75's cap compose: the cap
  bounds how often the call runs, the veto decides what one call may do. Both tickets correct
  `rolling_update.go:3142-3144` and `sentinel_failover_test.go:1279-1281` (Work list 2). T75's
  Cross-ticket bullet ~~names this finding "T62 Work list 8a"; its History records it as T73~~
  *(sweep 2026-09-27)* links this file.
- [T79](079-the-sentinel-roll-deletes-the-former-master-with-no-key-count-gate.md):
  the delete of the outgoing pod behind `verifyNewMasterReady`, one pass after `:3159` in the
  same shape. B here vetoes the `REPLICAOF`; T79's recommended D1-A vetoes that delete. Both reuse `demotionRefusalReason` and
  `dbSizeReader`, and neither closes the other's gap.
- [T69](069-three-sync-checks-read-a-replica-field-from-the-master.md): the sync half of
  `verifyNewMasterReady` cannot refuse on a master's `INFO`, so the gate that the loss passes on
  the next pass is attached replicas plus a readable `DBSIZE`. T69's recommended A (ask the
  replicas) would still pass on a replica fully synced from an empty X.

## Verification

- D1-B: unit tests on `forceReplicaConnections` and `handleMasterWithNoReplicas` with one fake
  server per pod carrying a key count (the fleet helper of
  `internal/controller/split_brain_dataset_test.go:51`): named master with keys - every other
  pod gets `REPLICAOF`; both empty - every other pod gets it; named master empty and one target
  with keys - no pod gets it; named master empty and a data holder that is not Ready - no pod
  gets it; a count unreadable - no pod gets it. Each of the last three fails with the veto
  removed (revert check, [ADR 0017](../adr/0017-test-and-ci-policy.md)); a mutation that turns
  the refusal into a per-target skip fails the test with one empty and one data-holding target,
  and one that counts only the targets fails the not-Ready case.
- A pass-level unit test of the refusal case: two masters, Sentinel names the empty one, the
  failover stamp older than 90 s; after `handleRollingUpdate` the data-holding pod received no
  `REPLICAOF` from either site. By reading it fails at `84a39c2` (the `:3159` send); it has not
  been run - run it before the fix to prove it reproduces.
- `make test-unit`, `make lint`, `make cyclo` (`forceReplicaConnections` stays under 15).
- The full e2e suite on `single-node-valkey9` and `single-node-valkey8`, because the function
  runs in the no-replica branch of Sentinel rolls (the benign case must still re-point).
  No e2e can make Sentinel promote an empty pod deterministically; the unit tier carries the
  refusal.
- Items 2 and 3: `grep -rn "so this is safe\|only safe because\|makes proceeding safe\|still gate the old-master\|non-master pod" internal/controller/`
  finds corrected text only; `grep -n "protected by D1" docs/adr/0028-*` finds struck or dated
  text.

## History

- 2026-09-27: filed from T62 (Work list 8a and its Cross-ticket "Adjacent" bullet, which
  carried the finding in one paragraph) during the re-verification at 84a39c2; the source was
  the design skeptic of that run (`rolling_update.go:3190-3213`, `:3159`, ADR 0025 `~:215-217`),
  which also noted that a uniqueness gate on T62's `:3161` would skip when the resolver refused
  (kept in T62). **Moved here:** the mechanism, the `:3159` / `:3192-3213` / `:3210` locations,
  the ADR 0028 and ADR 0025 references, "inference by reading, not measured". **Re-verified now
  (read at `84a39c2`):** the whole call path from the resolver (`:714-715`, `:795-822`,
  `:1415-1479`, `:1654-1736`) through `handlePostFailover` (`:3071-3114`) to `:3159` and on to
  `replaceRemainingPods` and `verifyNewMasterReady`; the guard **does** run before `:3159` in the
  same pass (same clock predicate), and its verdict is lost because `handlePostFailover` ignores
  the resolver's slice and re-scans - the host's wording ("that pod can be an old master the
  split-brain resolver just refused to demote") holds, and is sharpened: not only a refused old
  master, but the fail-closed refusal, an uncounted master, the clock boundary and data-holding
  replicas reach the same `REPLICAOF`; `:983`, the second caller, cannot follow a refusal of the
  same pass; provenance `2926077` and `2051a34`; the tests that pin the behaviour; ADR 0028
  `:284` and the comments and test texts that the reading contradicts; the unguarded retrigger at `:849-887`
  as a route to an empty promotion. **Measured (docker, both pinned images, commands and results
  under Fact):** M1, the `:3159` shape (both data holders at 0 keys within 7 s of the
  `REPLICAOF`); M2, Sentinel promoting an empty replica and converting the old master to it
  within 20 s. **Found not as stated:** nothing in the host paragraph was false; its "If the
  refusal was because the new master holds no keys" is one of several shapes (table under Fact).
  **Options** are new in this file: A (per-target veto), B (whole-call veto, recommended), three
  considered and not kept. **Frontmatter derived:** severity high (loss of every copy, silent),
  security none, effort S; the filing draft derived urgency next (rule 3, rule 1 read strictly).
  **Adversarial review, same day, before the file was committed:** every load-bearing location
  re-read at `84a39c2` and held. Changed: urgency **next -> now** (rule 1 as this repository
  applies it - the precedent of T67, docker emulation of the operator's own command sequence,
  and of 018, 023, 059 and 075, false by reading; the strict reading and its result next are
  kept in the frontmatter comment; back to next once Work list 2 and 3 land); the title and
  step 6 said "milliseconds" after the refusal, not timed - now "in the same pass"; M1's "11 s"
  was 9 s after the writes; M1 and M2 rerun on both pins (same outcome, second-level timings
  differ, both recorded); **M3 new**: persistence measured not to protect (`rdb` and `aof`, both
  pins), moved out of Not verified; the Work list 4 grep was stale (067, 069 and this file also
  match, none owns the gap; ADR 0026 and archive/032 also record it); two more "safe" texts
  (`:3143-3144`, `sentinel_failover_test.go:1279-1281`) and four "non-master" comments added,
  with the overlap with T75 on two of them; the same-pass delete when the reset count is spent,
  and T69's sync half, added to the sealing paragraph; B now also counts an existing pod that is
  not Ready (the outgoing pod is deleted whatever its readiness) and states that it does not
  close the delete by other attach routes (Work list 4); the runner-up argument is written out
  and the mark kept; T75 and T69 added under Cross-ticket; the pass-level test is marked not
  run. **Not verified:** the operator path on a cluster, how often X is empty, the tiebreak
  variant, persistence on a PVC. No make target, `go test`, Kind cluster or kubectl was run;
  every docker container and network created was removed.
  Sweep: T62 and T75 now link this file, so the opening note and the T75 Cross-ticket bullet no
  longer say otherwise. Work list item 4, the unfiled ADR 0007 residual risk "The Sentinel path
  deletes the former master with no key-count gate", is still open and now names its owner (the next
  filing run, which Hans starts; the sweep edits open tickets only) and its form (its own file with
  the next free number, because its decision is separate, together with the three stale code
  comments the ADR names). Frontmatter unchanged. Final pass: Work list item 4 now points to
  [T79](079-the-sentinel-roll-deletes-the-former-master-with-no-key-count-gate.md), filed today,
  instead of parking the finding for a filing run, and the two in-text "Work list 4" references (D1
  Mechanism, B's consequences) name T79; its parked claim that the three code comments ADR 0007
  names still describe the gate is dropped, because T79 found them rewritten in `bb6c78f`
  (checked: `git grep "has actual data\|critical safety check" 84a39c2 -- . ':!docs/tickets'`
  finds ADR 0007 `:388`, `:393`, `:525` and the corrected comment `rolling_update.go:3364` only),
  and a T79 bullet is added under Cross-ticket (its recommended D1-A read in T79 at `:309`).
  Frontmatter unchanged.
