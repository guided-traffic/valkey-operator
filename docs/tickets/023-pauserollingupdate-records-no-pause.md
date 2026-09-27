---
id: T23
title: "`pauseRollingUpdate` records no pause — it clears the state and re-arms a fresh budget"
state: analysed       # every load-bearing claim verified, option set complete
severity: low         # nothing is lost: every pause precedes a delete or a promotion
security: none
urgency: now          # rule 1: two tracked test lines promise a halt the code does not perform
effort: M             # recommended Q1 option: S in code, M in documents
blocked-by: adr-0010  # Q1 amends ADR 0010 D4; the independent changes are not blocked
filed-from: T15 D4
opened: 2026-08-26
decided:
done:
---

# T23 - `pauseRollingUpdate` records no pause — it clears the state and re-arms a fresh budget

## Current state

**What the pause does.** `pauseRollingUpdate`
([`rolling_update.go:2619-2647`](../../internal/controller/rolling_update.go#L2619-L2647)) sets
`RollingUpdatePaused=True/SyncTimeout`, writes phase `Error`, emits a Warning, calls
`clearRollingUpdateState` ([`:2637`](../../internal/controller/rolling_update.go#L2637)) and returns
an empty `RollingUpdateResult` ([`:2646`](../../internal/controller/rolling_update.go#L2646)).
`clearRollingUpdateState` ([`:3423-3497`](../../internal/controller/rolling_update.go#L3423-L3497))
forgets every in-memory wait bound, clears `PodTerminationStalled` and `PodRecreationStalled`
(`:3439-3442`), deletes the state annotation and nine bound annotations (`:3461-3470`) and clears
the drain-promotion stamps (`:3495`, skipped by the early return at `:3457-3460` when none of the
ten annotations is present).

**Why that is a problem.**

- It breaks ADR 0010 D4 ([`0010:240-245`](../adr/0010-every-rolling-update-wait-is-bounded.md)) and
  CLAUDE.md master-authority rule 3: expiry must hand over to a bounded successor state, never to a
  cleared one. ADR 0026 (`0026:616-619`) says the same of the pause.
- Nothing records the pause except the condition. The next dispatching pass arms a fresh
  `syncTimeout`, waits it out and pauses again with another Warning. `RollingUpdatePaused` stays
  `True` until the converged early return (`:312`) or completion (`:382`).
- The pausing pass is not a holding pass. The empty result is neither an error nor a requeue
  ([`valkey_controller.go:336-342`](../../internal/controller/valkey_controller.go#L336-L342)) and does
  not set `dataTierHolding` (`:363`), so the Sentinel roll runs in that pass (`:465-468`, the ADR 0026
  D11 exception); without Sentinel the no-master recovery and the steady-state split-brain check run
  with the state already cleared (`:421-443`); `updateStatus` overwrites the `Error` phase.
- **Resume gap.** Only an `Error` or `Syncing` phase requeues (`:377-380`).
  - Without Sentinel (multi-replica), `updateStandaloneStatus` reports `OK` once every pod is Ready
    (`:2235-2260`). The readiness probe is a `PING`
    ([`statefulset.go:847-857`](../../internal/builder/statefulset.go#L847-L857)), and a replica pointed
    at an unreachable master answers `PONG`, exit 0, with `master_link_status:down` (measured with
    docker on 9.1.1 and 8.1.9). The pass returns no requeue (`:392-396`); with a generation-gated CR
    watch and no Pod watch (`:2987-3000`) the roll re-dispatches only on an owned-object event (data
    StatefulSet status included), a referenced Secret change, a spec change, an operator restart or
    the 10 h cache resync: short where pods churn, hours in a quiet namespace.
  - With Sentinel, the pausing pass requeues through `Syncing` (`:2475-2487`) only when the unsynced
    replica is missing from the master's replica list. `AllSynced`
    ([`health/checker.go:129-130`](../../internal/health/checker.go#L129-L130)) is effectively the
    `connected_slaves` count alone (T69), and a master counts a replica that is in the `wait_bgsave`
    phase of a full sync while that replica reports `master_link_status:down` (measured with docker on
    both pins; the `send_bulk` and load phases were not measured). So an unreachable master, an auth
    failure or a replica pointed elsewhere requeue; a slow full sync and a pause on zero WAIT
    acknowledgements read `OK` (`:2488-2500`) and have the same gap, unless the released Sentinel
    roll requeues.
- **No shipped alert fires** for a pause that reads `OK`: none of the eight rules
  ([`prometheusrule.yaml:32-170`](../../deploy/helm/valkey-operator/templates/prometheusrule.yaml))
  keys on `RollingUpdatePaused`, and `ValkeyPhaseNotOK`, `ValkeyReplicasMissing` and
  `ValkeySpecNotObserved` stay silent (phase `OK`, replica Ready, generation stamped by every
  `updateStatus`). The series is exported ([`collector.go:186-192`](../../internal/metrics/collector.go#L186-L192)).
- **Two test lines promise a halt**: the comment at
  [`rolling_update_test.go:3898`](../../internal/controller/rolling_update_test.go#L3898) ("Paused: no
  requeue (operator waits for spec change).") and the assertion message at
  [`failover_sync_gate_test.go:132`](../../internal/controller/failover_sync_gate_test.go#L132) ("a
  paused update waits for a new spec change, not a requeue"). The `NeedsRequeue == false` assertions
  next to them are right. [`status.md:83`](../operations/status.md#error) lists "a paused roll" under
  phase `Error` without saying it lasts only until the next status write.
- **`clearSyncWaitTimestamp` discards its write error**
  ([`rolling_update.go:2664-2674`](../../internal/controller/rolling_update.go#L2664-L2674), `:2673`)
  after forgetting the in-memory bound. The stored annotation survives, `waitBoundExceeded` reads it
  first (`:1220-1222`) and `ensureWaitBound` does not re-arm a present one (`:1179-1181`), so the
  next sync wait of the same roll starts with part of its budget spent and can pause early. The fail
  direction is safe (a pause, never a promotion). It is the only discarded object write in non-test
  code; its sibling `clearRecreationWait` logs the same failure (`:2205-2209`).

**Facts the options rest on.**

- Every pause sits before a promotion or a delete. The nine pause sites: `verifyReplacedReplicasSynced`
  (`:2575`, `:2592`, before `replaceNextReplica` deletes a replica) and seven callers of
  `waitOrPauseForReplicaSync` in `waitForReplicasReady`, `waitForWriteSync` (zero acknowledgements)
  and `verifyPromotionCandidateHoldsData`, all before the failover (Sentinel, after the guard at
  `:2702-2708`) or `promoteAndRedirect` (`:4034`). The state there is `replacing-replicas` or empty;
  failover states are routed away first (`:741-748`, `:3949-3957`). The standalone handler reaches none.
- After the first pause of a roll the state stays empty for every later sync wait, because
  `replaceNextReplica` sets `replacing-replicas` only after `verifyReplacedReplicasSynced` passes
  (`:2424`, then `:2457-2460`). Every wait returns `NeedsRequeue`, which ends the pass before the
  post-update checks (`valkey_controller.go:340-342`), so the pausing pass is the only pass of a
  paused roll that reaches the no-master recovery and the steady-state check.
- An outdated pod does not remain at every pause (the T15 fixture shape): a pause on an up-to-date
  replica with the remaining ordinals absent or not Ready leads to the converged early return
  (`rolling_update.go:296-313`), and nothing supervises the unsynced replica.
- A non-empty state switches off `checkAndRecoverNoMaster` (`valkey_controller.go:2884-2888`) and
  `checkSteadyStateSplitBrain` ([`steady_state_master.go:158-163`](../../internal/controller/steady_state_master.go#L158-L163)).
  With no master, the multi-replica dispatcher calls `replaceNextReplica` before its `masterIdx < 0`
  wait (`rolling_update.go:3960-3972`), so a replica phase with no master ends in a pause, and today
  the pausing pass promotes pod-0 (`valkey_controller.go:2925`, no dataset comparison, T36). Such a
  phase needs a stale known-master record (T35) followed by the loss of the real master at an ordinal
  other than 0 (init script, [`statefulset.go:490-538`](../../internal/builder/statefulset.go#L490-L538)).
- The pause clears `PodTerminationStalled` and `PodRecreationStalled` without evidence
  ([`condition_registry.go:165`](../../internal/controller/condition_registry.go#L165), `:192`), which
  ADR 0027 asks an edge's clear site not to do.

**Impact.** Low. A paused roll can sit half-done for hours with phase `OK` and no alert (every pause
without Sentinel; on Sentinel a slow full sync or zero WAIT acknowledgements), and on Sentinel each
pause releases the Sentinel roll once. No data is lost. A delayed TLS-rotation roll is already
covered by `TLSMaterialStale` and its 72 h alert.

## Required changes

**Independent of the open questions**

1. Reword the two test lines (`rolling_update_test.go:3898`, `failover_sync_gate_test.go:132`): the
   pause returns without a requeue and a later pass re-dispatches on a fresh budget. Optionally
   qualify "a paused roll" at `status.md:83`. Check: `git grep -n -i 'spec change' -- 'internal/**/*.go'`
   finds no pause test promising a halt; `make lint`.
2. `clearSyncWaitTimestamp` returns its write error; its callers (`rolling_update.go:2601`, `:2813`)
   return `RollingUpdateResult{Error: err}`, so the next pass retries the clear before any delete or
   failover. Cost: a failing write delays the next delete by one backoff and shows phase `Error` for
   that pass (`valkey_controller.go:336-338`).
   - Unit test with an interceptor that rejects the removal of `annotationSyncWaitStarted` (the
     inverse of `rejectAnnotationArming`,
     [`rolling_update_bounds_test.go:719-730`](../../internal/controller/rolling_update_bounds_test.go#L719-L730)),
     with a revert check; `make test-unit`, `make lint`, `make cyclo`.
   - Extend ADR 0010 D7 or D8 so the rule covers a bound's clear write as well as its arming write
     (D10, `0010:288-295`, covers bounded states only, and the sync wait is not one).
   - `clearRecreationWait` is out of scope.

**Depends on the answers**

3. Q1: the chosen option's code, tests and documents (listed under Q1).
4. Q2: the rule and its documentation, or the documentation only.
5. At close: replace the five T23 citations (ADR 0002 `:316`, ADR 0010 `:812`, ADR 0024 `:531`,
   ADR 0026 `:771`, `rolling_update.go:2616`) with the ADR that carries the decision, extract per
   ADR 0034, archive.

## Open questions

### Q1: What should a sync-timeout pause leave behind, and what should the pausing pass return?

Today the pause clears the whole roll state and returns an empty result, so the pass releases the
Sentinel roll and, if the phase reads `OK`, schedules no recheck. Both options below return
`RollingUpdateResult{DeferredRequeueAfter: rollingUpdateRequeueDelay}` (10 s, `rolling_update.go:203`),
which makes the pausing pass a holding pass (Sentinel roll skipped) with a recheck on every topology;
they differ in whether the state is cleared. Neither changes a pod template, a delete or security.

- **D - keep the clear, hold and requeue (recommended).** Change only the return value. The pausing
  pass without Sentinel still runs the no-master recovery and the steady-state check. Amends ADR 0010
  D4 and CLAUDE.md rule 3 to name the sync-timeout pause as the one expiry that clears the state, and
  names its dependency on the pausing pass's no-master recovery (T35); it must not claim that an
  outdated pod always remains. Consequences: one Warning per `syncTimeout` indefinitely; without
  Sentinel the phase alternates between `Rolling Update i/n (syncing)` and a brief `Error` or `OK`;
  drain stamps, stall edges and the ADR 0032 D4 repair gate behave as today. Cost S in code, M in
  documents.
- **C - keep the state, clear only the sync wait, hold and requeue.** Replace
  `clearRollingUpdateState` with `clearSyncWaitTimestamp` in the pause; `clearStaleRollingUpdateState`
  leaves `replacing-replicas` alone (`:833-844`), so ADR 0010 D4 holds as written. Gains: drain stamps
  survive, stall edges are not cleared without evidence, the ADR 0032 D4 repair gate stays held, and
  the roll keeps verifying the unsynced replica in the T15 fixture shape. Cost: a regression - the
  kept state switches off the no-master recovery for as long as the roll is paused, so a replica phase
  with no master stays without a writable master until a human acts. Needs required change 2 first,
  and an amendment of ADR 0024 and ADR 0026 recording the no-master consequence. Cost M.

Reason for D: both close the same two gaps with the same return value; C's gains are narrow latent
issues (a stale report, a missing supervision, extra restarts), while its cost is a write outage.
D4's hazard (masters left over from a half-finished failover with no split-brain caller) does not
arise at any of the nine pause sites, because every pause precedes the promotion and the 10 s recheck
guarantees a dispatch that runs `resolveSplitBrain` (`:3909`) while an outdated pod exists. One
qualification: the recovery D keeps promotes pod-0 without comparing datasets (T36); D is strongest
once T36's data-aware recovery lands.

Work for D:
- Code: the returned value in `pauseRollingUpdate`.
- Tests, each with a revert check: the pausing pass returns a positive `DeferredRequeueAfter`, skips
  the Sentinel roll (pattern of `TestReconcileWorkload_DataAvailabilityStallHoldsTheSentinelRoll`,
  [`pod_availability_test.go:585`](../../internal/controller/pod_availability_test.go#L585)), and
  without Sentinel still reaches `checkAndRecoverNoMaster` (fails under C). The existing pause tests
  ([`rolling_update_test.go:3875`](../../internal/controller/rolling_update_test.go#L3875),
  [`failover_sync_gate_test.go:119`](../../internal/controller/failover_sync_gate_test.go#L119)) need an
  assertion on `DeferredRequeueAfter`.
- Documents: ADR 0010 D4 and CLAUDE.md rule 3; the `RollingUpdateResult` doc comment
  ([`rolling_update.go:168-183`](../../internal/controller/rolling_update.go#L168-L183)) and
  `valkey_controller.go:343-354`, which must name the pause as a setter of `DeferredRequeueAfter`; the
  pause's return comment (`rolling_update.go:2641-2645`); every statement that the pausing pass runs
  the Sentinel roll (the D11 exception, in ADR 0001, 0010, 0024, 0026, 0027, CLAUDE.md, README.md,
  `status.md`, `valkey_controller.go:405-410`, `api/v1/valkey_types.go:165-168`), found with
  `grep -n 'empty result\|pass that pauses\|in which a data roll pauses\|pass where a data roll\|returns no requeue\|pauseRollingUpdate' docs/adr/*.md CLAUDE.md README.md docs/operations/*.md internal/controller/valkey_controller.go api/v1/valkey_types.go`.
- Under C additionally: the "pause clears the state" sentences (`api/v1/valkey_types.go:58-59`,
  `rolling_update.go:2609-2613`, `:2634-2636`, `status.md:21`, `rolling-updates.md:18`, ADR 0010
  `:801-802`, ADR 0024 `:530`, ADR 0026 `:617`) and a test that a pause keeps the state and stamps.

**Answer:** _open_

### Q2: Should the chart's PrometheusRule alert on `RollingUpdatePaused`?

No shipped rule fires for a pause that reads `OK`. `RollingUpdatePaused` stays `True` from the first
pause until convergence or completion, so it is the one stable signal; under either Q1 option the
phase alternates, and whether `ValkeyPhaseNotOK`'s `for: 30m` ever completes is not established. The
rule set is off by default (ADR 0021 D7); an install that enabled it gets one more alert on upgrade.

- **A - ship a ninth rule, `ValkeyRollingUpdatePaused` (recommended).**
  `max by (namespace, name) (vko_valkey_status_condition{condition="RollingUpdatePaused",status="True"}) == 1`
  guarded by `and on () max(vko_valkey_collector_success) == 1`, `for: 30m`, severity `warning`, in the
  shape of `ValkeyReconcileBlocked` (`prometheusrule.yaml:51-57`). Record it in ADR 0021,
  [`monitoring.md:63`](../operations/monitoring.md) ("eight alerts" becomes nine) and
  [`status.md#rollingupdatepaused`](../operations/status.md#rollingupdatepaused). Cost XS; no CI gate
  renders the chart (T58), so check with a local `helm template`. It also fires on a roll that paused
  once and then took more than 30 minutes, which still signals a `syncTimeout` too short for the
  dataset.
- **B - no rule; document the series and the query** in `status.md` and `monitoring.md`. Cost XS; the
  default rule set stays blind and every fleet user writes the same rule.

Reason for A: ADR 0021 ships alerts for "a spec was accepted and never converged", and a paused roll
is that state in the one shape the generation pair cannot see. Same cost as B, and it reaches every
install that enables the rule set.

**Answer:** _open_

## Not verified

- Nothing ran on Kind and no `go test` ran; the resume gap is traced by reading, not reproduced. A
  Kind reproduction of a pause without Sentinel would settle it.
- Whether a sole master's drain stamp, cleared by the pause before `checkSteadyStateSplitBrain` runs
  in the same pass (`valkey_controller.go:440`), is lost for adoption; the cache may still show it. A
  unit test that stamps a sole master, pauses and asserts the same pass records it would settle it.
- Whether the no-master recovery can promote an unsynced, empty replacement at pod-0 on a
  non-persistent tier (replicas are replaced youngest-first, `rolling_update.go:2412-2435`).
- Whether the ADR 0032 D4 repair gate, opened by the emptied state, can remove the repair under a
  running roll and whether that adds restarts; a unit test over `dataOwnershipRepairNeeded` after a
  pause would settle reachability. No data loss by reading. Open under D, closed under C.
- Whether the T15 fixture shape occurs in production; under C, whether any other bound that
  `clearRollingUpdateState` clears is armed at a pause site (read as none, not traced per site).

## Related

- T15: origin of this item (D4).
- T18: relies on the pausing pass reaching `updateStatus`; Q1 changes only the reason (ADR 0001 `:118`).
- T35: stale master records, the precondition that makes option C a regression.
- T36: the no-master recovery is not dataset-aware; its fix strengthens option D.
- T40: rewrites the T23 citations outside `docs/tickets/` once, at this ticket's close.
- T51: a changed `spec.auth.secretName` with a different password pauses the roll.
- T58: no CI gate renders the chart (Q2 option A).
- T69: its option A closes the Sentinel slow-sync gap; the other pauses still need Q1.
