---
id: T79
title: the Sentinel roll deletes the former master behind verifyNewMasterReady, which reads the new master's key count and refuses on nothing, and three tracked texts still describe that gate
state: analysed       # delete path and gate read, the Valkey inputs of the gate measured on both pins
severity: high        # an empty promoted master plus a data-holding outgoing pod ends in the delete of the only dataset, with phase OK
security: none        # the operator's own delete breaks a data-integrity rule (ADR 0028), no principal involved
urgency: now          # ADR 0007 Residual risks states tracked text that is false; next once the text fixes land
effort: M             # one veto, one reported hold under its own condition, text fixes and ADR amendments
blocked-by: decision  # Q1 and Q2
filed-from: T73
opened: 2026-09-27
decided:
done:
---

# T79 - the Sentinel roll deletes the former master behind a gate that asks for no key count

## Current state

**The delete.** On a Sentinel cluster the data-tier roll replaces the replicas, triggers a Sentinel
failover, and then deletes the outgoing master O in `replaceRemainingPods`
([rolling_update.go:2984](../../internal/controller/rolling_update.go#L2984)). Per outdated pod it
waits for a gone or terminating pod, asks `verifyNewMasterReady` (Sentinel path only,
[rolling_update.go:3012-3017](../../internal/controller/rolling_update.go#L3012-L3017)), passes the
ADR 0026 D5 gate (`:3023-3026`), sets `replacing-master` (`:3029`) and deletes
([rolling_update.go:3034](../../internal/controller/rolling_update.go#L3034)).

**The gate.** `verifyNewMasterReady`
([rolling_update.go:3331](../../internal/controller/rolling_update.go#L3331)) takes the first
current, `available()` pod answering `role:master` (X) and refuses only when X has
`connected_slaves == 0` (`:3350`), the TLS config or X's `DBSIZE` is unreadable (`:3368-3379`),
the only candidate is terminating (`:3387-3392`) or no candidate exists (`:3395-3396`). Its
`master_sync_in_progress` check (`:3355`) never fires on a master (T69). On any readable count,
**zero included**, it logs "New master verified with data" and returns verified (`:3381-3383`). It
never reads O's count. Its waits other than the terminating one are unbounded plain requeues.

**What the delete does.** A non-persistent O loses its memory. A persistent O reboots, its init
container asks Sentinel first ([statefulset.go:288-339](../../internal/builder/statefulset.go#L288-L339)),
boots as X's replica and full-syncs its `/data` away (measured in T73 for `rdb` and `aof`).
`finalizeRollingUpdate` (`:891`, `checkFinalizationTopology` `:946-989`) asks no key count either:
an empty master with every replica attached completes the roll with `RollingUpdateComplete`.

**When the gate matters.** If Sentinel already converted O into X's replica (5 to 20 s after the
failover, measured in T73), O already holds what X holds and no delete-time check helps. The count
of O is meaningful only while O still answers master (the ADR 0028 refusal shape) or its link to X
is down before the flush (up to 4 to 7 s).

**The refusal shape.** The resolver runs before every dispatch
([rolling_update.go:714-715](../../internal/controller/rolling_update.go#L714-L715), except while
`ownFailoverInFlight`, `:797-800`). When it refuses to demote a data-holding O toward an empty X, it
returns X as `masterIdx` ([rolling_update.go:1478](../../internal/controller/rolling_update.go#L1478))
and O keeps `isMaster` (`:1659-1663`). Once X has one connected replica by any route,
`handleNewMasterFound` hands over to `replaceRemainingPods`
([rolling_update.go:3129](../../internal/controller/rolling_update.go#L3129)), the gate passes on
`DBSIZE 0`, and O, the pod the refusal protected, is deleted. ADR 0028 D1/D4 cover a demotion, not
a delete; ADR 0028 D8 (`0028:198`, `:206-210`) records this delete as the end of the refusal on the
Sentinel path.

**Measured** (docker, `valkey/valkey:9.1.1` and `8.1.9`, three plain servers, 500 keys on O, X and a
replica R empty, `REPLICAOF X` on R): from +1 s X answers `role:master`, `connected_slaves:1`, no
`master_sync_in_progress` line, `DBSIZE 0`, while O answers `role:master`, `DBSIZE 500`. Every
refusal branch of the gate is false from +1 s on.

**Routes to an empty X:** the retrigger after a 30 s failover timeout (`handleFailoverRetrigger`,
[rolling_update.go:849-887](../../internal/controller/rolling_update.go#L849-L887)), which skips
the `waitForReplicasReady`/`waitForWriteSync` gates of the first trigger (`:2710-2719`), and a
promoted non-persistent pod restarting empty (T36).

**Tracked texts that describe the gate as present:**

1. The log line [rolling_update.go:3381](../../internal/controller/rolling_update.go#L3381), "New
   master verified with data", written with `dbsize=0` as well.
2. The comment [rolling_update.go:4026-4027](../../internal/controller/rolling_update.go#L4026-L4027)
   says the Sentinel path "reads the same counts": the manual path reads two (outgoing master and
   candidate, `verifyPromotionCandidateHoldsData`, `:2843-2890`), the Sentinel path one.
3. `TestVerifyNewMasterReady_AcceptsAMasterWithReplicasAndData`
   ([sentinel_failover_test.go:1063](../../internal/controller/sentinel_failover_test.go#L1063)),
   message "the keyspace of the promoted pod is what has to be non-empty" (`:1071-1072`). Its
   fixture answers every `DBSIZE` with 4711; no test feeds a 0.
4. ADR 0007 Residual risks (`:523-526`) says three code comments still describe the check as
   present; none does (the header `:2979-2983` and inline comment `:3361-3366` already say the count
   is not refused). ADR 0007 `:392-393` and ADR 0026 Residual risks `:787-789` quote the old comment.
5. Comments at `:2983`, `:3003` and `:3365` cite the archived T32 ticket as the record of the gap.

**Impact.** Sentinel clusters only, during a data-tier roll. In the refusal shape any attach to X (a
restarting pod, Sentinel reconfiguring a replica, `forceReplicaConnections`, T73) lets the gate
pass and O is deleted: the dataset is gone, the roll reports `RollingUpdateComplete`, phase `OK`.
The only trace is the info log "verified with data ... dbsize 0". Not affected: clusters without
Sentinel (gate not called), rolls whose X holds keys, pod templates (nothing rolls).

## Required changes

### Independent of the open questions

- Log line `:3381`: drop "with data", log the count as a field.
- Comment `:4026-4027`: the Sentinel path reads one count, the new master's.
- Rename the test at `sentinel_failover_test.go:1063` and rewrite its message: it pins that X's
  `DBSIZE` is read, not that it is non-empty. Add a test that feeds `DBSIZE 0` and pins today's
  acceptance (becomes the refusal test under Q1-A).
- Comments `:2983`, `:3003`, `:3365`: cite ADR 0007 Residual risks instead of T32 (ADR 0034).
- ADR 0007 `:523-526`: name the texts above instead of the three comments; fix ADR 0007 `:392-393`
  and ADR 0026 `:787-789`. No ticket citation in any ADR edit.

### Depends on the answers

- The veto of Q1 and the hold of Q2 in `replaceRemainingPods`; `verifyNewMasterReady` returns the
  pod it verified.
- ADR: a new D in ADR 0007 or an amendment of ADR 0028 D4 naming the delete as a guarded site;
  amend ADR 0028 D8 (`:198`, `:206-210`) and D3 ("the divergence is bounded" no longer holds on the
  Sentinel path); amend ADR 0026 D11's *Replacement* argument (`:791-792`); close the ADR 0007 and
  ADR 0026 residual risks.
- Under Q2-C: the condition in `api/v1`, its `conditionRegistry` row, a README condition-table row,
  a section in [status.md](../operations/status.md), and [rolling-updates.md](../operations/rolling-updates.md).

### Tests

- `replaceRemainingPods` with one fake server per pod (fleet helper in
  `internal/controller/split_brain_dataset_test.go:51`): X with keys -> deleted; both empty ->
  deleted; X empty and O with keys -> not deleted; X empty and O unreadable -> not deleted; X
  unreadable -> not deleted. Cases 3 and 4 fail with the veto removed (revert check).
- Hold (Q2-C): driven past `syncTimeout`, the state stays set, the new condition is True,
  `RollingUpdatePaused` is not written, no pod deleted, result `DeferredRequeueAfter` without
  `NeedsRequeue`, the Sentinel roll does not run, one Warning Event. A second test drops O's count
  to 0 and asserts the delete and the presence-guarded clear.
- A pass-level test of the refusal shape (two masters, Sentinel names the empty one, X with one
  replica): the data holder survives `handleRollingUpdate`. Run it before the fix to prove it
  reproduces.
- `make test-unit`, `make lint`, `make cyclo`; full e2e on `single-node-valkey9` and
  `single-node-valkey8`, because the delete runs on every Sentinel roll.

## Open questions

### Q1: What, beyond an attached replica, must hold before the former master is deleted on the Sentinel path?

The delete at `:3034` is the last point where the operator can keep a data-holding O. The gate
already reads X's count; a dataset veto needs O's count only when X is empty.

- **A - veto at this delete (recommended).** After `verifyNewMasterReady`, call
  `demotionRefusalReason(dbSizeReader(ctx, v), X, O)` (`:1713-1736`, `:1681-1693`, reused unchanged)
  before the D5 gate. Refuse when X is empty and O holds keys, or a count is unreadable; both empty
  passes. Cost S: one extra `DBSIZE` on X per attempt, one on O only when X is empty. Consequence:
  the two visible masters of ADR 0028 D3 then last without a bound on this path, both behind the
  `-rw` Service, until Sentinel or a human resolves them or X gains keys; an outdated O that is not
  Ready (unreadable count) is held while X is empty.
- **B - veto at every roll delete** (`replaceNextReplica`, `replaceRemainingPods`,
  `deleteNextPendingPod`). Cost M: a master `DBSIZE` before every replica delete of every roll.
  The extra sites only delete pods answering `slave`, whose keys the next sync discards anyway.

A protects exactly the pod the finding is about, at the site ADR 0028 D8 names, and does not depend
on Q2: with the state cleared, the refusal-shape pass still reaches `replaceRemainingPods`.

**Answer:** _open_

### Q2: What does a refused delete report and hand over to?

ADR 0010 requires every roll wait to be bounded and forbids handing expiry over to a cleared
rolling-update state; ADR 0026 D5 never resumes a refusal on a clock and bounds only the reported
observation. Before the bound a refusal is a plain requeue; past it the result is
`DeferredRequeueAfter` (as in `terminationWait`, `:2056-2076`), so status and steady-state checks run
and the Sentinel roll stays held. The clock is the sync-wait bound (`ensureSyncWaitTimestamp`),
which must be armed here.

- **A - pause like the manual path** (`waitOrPauseForReplicaSync`). Cost XS. Clears the state
  against ADR 0010, releases the Sentinel roll on every pausing pass, and repeats a Warning and
  phase `Error` every `syncTimeout` for the same refusal (T23).
- **B - hold under `RollingUpdatePaused` with a new reason.** Cost S. The condition then means both
  "cleared and retrying" (as documented in `api/v1/valkey_types.go:55-68` and `status.md`) and "held,
  never retrying on its own".
- **C - hold under its own edge condition (recommended)** (for example `DatasetDeleteRefused`,
  message naming both pods and counts), set past `syncTimeout` with one Warning Event, cleared
  presence-guarded where the veto lets the delete through and in `clearRollingUpdateState`. Cost S.
  The hold ends when O empties, a human decides which dataset is real, or X gains keys.

C keeps each condition to one meaning, matches `PodTerminationStalled`, `PodRecreationStalled` and
`PodAvailabilityStalled`, and costs one constant and one registry row more than B. The held state
is `failover-triggered`, whose other arm calls `forceReplicaConnections` (`:3150-3159`), so the
hold is only as safe as T73's veto of that call.

**Answer:** _open_

## Not verified

- The operator path end to end: only read; the pass-level unit test above settles it.
- How often X is empty while O still holds data: no route was produced on a cluster.
- Which `status.phase` a held pass ends on: what `CheckCluster` answers with two masters
  (`updateHAStatus`, [valkey_controller.go:2427](../../internal/controller/valkey_controller.go#L2427))
  was not read.
- Whether the non-Sentinel `replaceRemainingPods` (skips the gate, relies on
  `verifyPromotionCandidateHoldsData`) can delete a data holder behind an empty master.

## Related

- T73: `forceReplicaConnections` re-points O at the empty X one pass earlier; its veto and this one
  compose, neither closes the other's gap.
- T69: the `master_sync_in_progress` half of the gate cannot refuse on a master.
- T75: the uncapped reset-and-retrigger cycle; its retrigger is a route to an empty X.
- T36: a non-persistent promoted pod restarting empty is a route to an empty X.
- T23: Q2-A would inherit its missing pause record.
- T67: writes O accepts after the promotion; its appendix B is the same delete seen from the other
  side, untouched by Q1-A.
- T40: the three T32 citations.
