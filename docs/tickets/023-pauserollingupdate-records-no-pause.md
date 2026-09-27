---
id: T23
title: "`pauseRollingUpdate` records no pause — it clears the state and re-arms a fresh budget"
state: filed
severity: low         # "low as a defect, medium as a documentation lie"
security: none
urgency: icebox       # rule 5 since 2026-09-27: the false pause comment (item 1) landed, the fix waits on an ADR 0010 D4 re-decision
effort: M             # the recommended option C; A is S, B is L. Was L (the statePaused option) until 2026-09-27
blocked-by: adr-0010
filed-from: T15 D4
opened: 2026-08-26
decided:
done:
---

# T23 - `pauseRollingUpdate` records no pause — it clears the state and re-arms a fresh budget

**Severity: low as a defect, medium as a documentation lie. Status: open, filed 2026-08-26 by
T15 D4. Effort: L (~80 LOC plus an ADR 0010 re-decision).** *(corrected 2026-09-27: the
documentation half is gone, see below, so only "low as a defect" remains; the effort depends on
the option, see Options.)*

`pauseRollingUpdate` calls `clearRollingUpdateState`
(~~[`rolling_update.go:2073`](../../internal/controller/rolling_update.go#L2073)~~ *(corrected 2026-09-27:
[`:2636`](../../internal/controller/rolling_update.go#L2636))*), which deletes
`annotationSyncWaitStarted` and drops the in-memory wait bound. Nothing on the CR then records
that a pause happened except the `RollingUpdatePaused` condition, and the next dispatching pass
re-arms a **fresh** `syncTimeout` budget, waits it out and pauses again — re-emitting the
Warning Event each cycle. On a 5 min `syncTimeout` at a 10 s requeue that is a repeating cycle,
not a halt. *(corrected 2026-09-27: only where something requeues the pass that pauses. On a
multi-replica cluster without Sentinel nothing does, and the next dispatch waits for an event;
see Fact.)*

Two consequences:

* **It contradicts ADR 0010's own rule** that expiry hands over to another bounded state and
  *never* to a cleared rolling-update state — which is exactly what this does.
* **Four tracked sentences promise a halt.** T15 D4 corrects them in text; this item is the
  version where they become true at the mechanism: a `statePaused` state that both dispatchers
  hold until the generation changes. *(corrected 2026-09-27: they were corrected; at `4a7543e` no
  tracked sentence promises a halt — [`status.md:21`](../operations/status.md#rollingupdatepaused),
  [`rolling-updates.md:18`](../operations/rolling-updates.md),
  [`api/v1/valkey_types.go:53-63`](../../api/v1/valkey_types.go#L53-L63) and
  [`rolling_update.go:2603-2616`](../../internal/controller/rolling_update.go#L2603-L2616) describe a report and a retry. `statePaused` is
  option B below, no longer the item.)*

**The re-decision it needs.** Is a state that ends only at a spec change *bounded* in ADR 0010's
sense, or is it the unbounded wait ~~D3~~ refuses? *(corrected 2026-09-27: D3 is the Phase 1
give-up rule; the refusal of unbounded waits is the ADR's title rule, and the rule the pause
breaks is D4, [`0010:240-245`](../adr/0010-every-rolling-update-wait-is-bounded.md).)* That question is why this is its own item and not
part of T15: answering it either way is an ADR 0010 amendment, and coupling it to a status
lifecycle fix makes both unreviewable.

**Not verified:** the ~29-`False`-passes-per-`True` figure quoted in the T15 analysis is derived
from the requeue delay and the default `syncTimeout`, not observed on a cluster.

## Fact, re-verified 2026-09-27

**Verified** (by reading at `4a7543e`):

- `pauseRollingUpdate` ([`rolling_update.go:2618-2644`](../../internal/controller/rolling_update.go#L2618-L2644)) sets
  `RollingUpdatePaused=True/SyncTimeout`, writes phase `Error`, emits the Warning, calls
  `clearRollingUpdateState` ([`:2636`](../../internal/controller/rolling_update.go#L2636)) and returns an empty result
  ([`:2643`](../../internal/controller/rolling_update.go#L2643)). `clearRollingUpdateState` ([`:3420-3493`](../../internal/controller/rolling_update.go#L3420-L3493)) drops the
  in-memory bounds, deletes the state annotation and every bound annotation
  ([`:3457-3466`](../../internal/controller/rolling_update.go#L3457-L3466)) and clears the drain-promotion stamps
  ([`:3492`](../../internal/controller/rolling_update.go#L3492)). ADR 0010 D4 ([`0010:240-245`](../adr/0010-every-rolling-update-wait-is-bounded.md))
  forbids clearing the state on expiry and asks every abandon path to name a successor state;
  ADR 0026 says the same of the pause ([`0026:618`](../adr/0026-a-pod-being-deleted-is-not-available.md)).
- **Every pause sits before a promotion or a delete**, so D4's hazard (two masters and no caller
  of `detectAndResolveSplitBrain`) is not reached from it: `verifyReplacedReplicasSynced`
  ([`:2574`](../../internal/controller/rolling_update.go#L2574), [`:2591`](../../internal/controller/rolling_update.go#L2591)) runs before `replaceNextReplica` deletes a
  replica ([`:2423`](../../internal/controller/rolling_update.go#L2423)); `waitOrPauseForReplicaSync` ([`:2816-2825`](../../internal/controller/rolling_update.go#L2816-L2825))
  is reached from `waitForReplicasReady` ([`:2798`](../../internal/controller/rolling_update.go#L2798), [`:2804`](../../internal/controller/rolling_update.go#L2804)), the
  zero-acknowledgement branch of `waitForWriteSync` ([`:2956`](../../internal/controller/rolling_update.go#L2956)) and
  `verifyPromotionCandidateHoldsData` ([`:2847-2879`](../../internal/controller/rolling_update.go#L2847-L2879)); on the Sentinel path
  after the guard that stops once a failover is triggered ([`:2702-2706`](../../internal/controller/rolling_update.go#L2702-L2706), then
  [`:2708`](../../internal/controller/rolling_update.go#L2708), [`:2714`](../../internal/controller/rolling_update.go#L2714)), on the path without Sentinel in
  `handleManualFailover` before `promoteAndRedirect` ([`:4005`](../../internal/controller/rolling_update.go#L4005), [`:4010`](../../internal/controller/rolling_update.go#L4010),
  [`:4027`](../../internal/controller/rolling_update.go#L4027)). At every one of them the state is `replacing-replicas` (set at
  [`:2455-2458`](../../internal/controller/rolling_update.go#L2455-L2458)) or empty.
- **The pause's phase lasts one write.** The empty result is neither an error nor a requeue
  ([`valkey_controller.go:336-342`](../../internal/controller/valkey_controller.go#L336-L342)), so the pass goes on to the Sentinel roll and,
  ~~unless that ends it~~ *(corrected 2026-09-27, review: unless a post-update check ends it -
  the Sentinel roll, the no-master recovery at `:422-428` or the split-brain check at
  `:440-443`)*, to `updateStatus`, which recomputes phase and message over the pause's
  `Error` in the same pass. The durable report is the condition and the Event.
  *(Added 2026-09-27, review:)* so
  [`rolling-updates.md:18`](../operations/rolling-updates.md) ("phase `Error` for that pass") is
  imprecise *(made precise 2026-09-27, work list item 1, History)*: the pass writes `Error` and, unless a post-update check ends it, overwrites it with
  the computed phase before it ends (`updatePhase` writes at once,
  [`valkey_controller.go:2605-2620`](../../internal/controller/valkey_controller.go#L2605-L2620); `updateStatus` re-reads the CR and
  `persistStatus` writes the difference). Work list item 1.
- **Where the pause does not cycle.** On a Sentinel cluster the unsynced replica makes
  `updateHAStatus` report `Syncing` ([`:2475`](../../internal/controller/valkey_controller.go#L2475)), which requeues in 10 s
  ([`:377-379`](../../internal/controller/valkey_controller.go#L377-L379)), or the released Sentinel roll requeues itself. On a multi-replica
  cluster without Sentinel, `updateStandaloneStatus` reports `OK` once every pod is Ready and
  reachable ([`:2235-2260`](../../internal/controller/valkey_controller.go#L2235-L2260)); the readiness probe is a `PING`
  ([`statefulset.go:847-857`](../../internal/builder/statefulset.go#L847-L857),
  [`ProbeCommand`](../../internal/builder/statefulset.go#L1515)), which an unsynced replica passes;
  and the pass returns the zero `deferredRequeue` ([`:392-396`](../../internal/controller/valkey_controller.go#L392-L396)). The CR watch is
  generation-gated and there is no Pod watch ([`:2986-2993`](../../internal/controller/valkey_controller.go#L2986-L2993)), so the roll
  dispatches again only on an event of an owned object, a spec change, an operator restart or
  the 10 h cache resync *(review 2026-09-27: or a change of a Secret the CR references, the one
  non-owned watch, [`valkey_controller.go:2996-2999`](../../internal/controller/valkey_controller.go#L2996-L2999))*, with phase `OK` and `RollingUpdatePaused=True` in between.
- **A comment ~~says~~ *(said, until work list item 1 on 2026-09-27; fixed, History)* the pause
  ends the pass.** [`rolling_update.go:2640-2642`](../../internal/controller/rolling_update.go#L2640-L2642):
  "That ends THIS pass without a wait". It ends the data roll's work for the pass; the pass goes
  on (above), as the doc comment of `handlePostRollingUpdateChecks`
  ([`valkey_controller.go:406-410`](../../internal/controller/valkey_controller.go#L406-L410)) and ADR 0010 `:800-812` say. Work list item 1.
- **Appendix, same family (found 2026-09-27):** `clearSyncWaitTimestamp` discards its write
  error ([`rolling_update.go:2670`](../../internal/controller/rolling_update.go#L2670)) after it has
  forgotten the in-memory bound. If that write fails, the stored annotation survives, and
  `waitBoundExceeded` reads the annotation first
  ([`:1218-1221`](../../internal/controller/rolling_update.go#L1218-L1221)), so the next sync wait of
  the same roll starts expired and pauses early. The fail direction is safe (a pause, never a
  promotion). Fixing it means returning the error to its callers
  ([`:2600`](../../internal/controller/rolling_update.go#L2600),
  [`:2810`](../../internal/controller/rolling_update.go#L2810), and the pause under option C); not
  decided, and not part of work list item 1.
- T23 is cited outside `docs/tickets/` at ADR 0002 `:306`, ADR 0010 `:812`, ADR 0024 `:531`,
  ADR 0026 `:771` and [`rolling_update.go:2615`](../../internal/controller/rolling_update.go#L2615).

**Not verified:**

- Nothing was run. The resume gap without Sentinel is traced by reading, not reproduced on Kind.
- Whether the drain-stamp clear at a pause can drop a stamp a later pass needs. The call is on
  the pause path ([`:3492`](../../internal/controller/rolling_update.go#L3492)), and the comment above it
  ([`:3481-3486`](../../internal/controller/rolling_update.go#L3481-L3486)) names the hazard of clearing a fresh drain's stamp early. A
  master drained during a sync wait is plausible (its replicas then report the link down, which
  is what the wait waits on), but no sequence was traced to a wrong demotion.
- *(Added 2026-09-27, review:)* what option C leaves standing that `clearRollingUpdateState`
  clears today: it forgets every in-memory wait bound, not only the sync wait's
  ([`:3428`](../../internal/controller/rolling_update.go#L3428)), clears `PodTerminationStalled`
  and `PodRecreationStalled` (`:3436-3439`) and deletes nine further annotations
  (`:3457-3466`). At the pause sites the state is `replacing-replicas` or empty, and the
  recreation wait clears its own annotation (`:2200-2203`), so none of them should be armed
  there; that was read, not traced per site. Under C the drain stamps are cleared at the roll's
  completion instead, which is where the comment at `:3472-3491` says they are spent; the cost
  of a stamp that outlives its drain (named there) was not traced for the longer window.

## Impact

Low. The durable signals are `RollingUpdatePaused=True` and a Warning per pause; no alert keys on
either, and the phase reads `OK` or `Syncing` after the pause pass. On a cluster without Sentinel
a paused roll can stay half-done for hours with phase `OK`. On a Sentinel cluster every pause
releases the Sentinel roll once (ADR 0026 D11, the known exception).

## Options

One decision. It re-decides ADR 0010 D4, which CLAUDE.md carries as rule 3 of the master
authority.

- **A — Accept the cycle and name the pause as an exception in D4.** Justified by the position of
  every pause site (Fact). Cost S, no code: the D4 amendment, the five citations rewritten to
  cite ADR 0010 D4. Leaves an exception in a rule whose text asks every future abandon path to
  name a successor, the resume gap without Sentinel (which the amendment would then have to
  state), the D11 exception, and the drain-stamp clear at each pause.
- **B — A `statePaused` both dispatchers hold until the generation changes.** Cost L: both
  dispatchers, ADR 0010, 0024 and 0026 amended, docs, unit tests and a Kind e2e. Turns a slow but
  progressing sync (a dataset that needs longer than `syncTimeout`) into a halt only a spec change
  ends, and answers D4's question with "a human-ended state is bounded".
- **C — Keep the retry, stop clearing (recommended).** In `pauseRollingUpdate` replace
  `clearRollingUpdateState` ([`:2636`](../../internal/controller/rolling_update.go#L2636)) with `clearSyncWaitTimestamp`
  ([`:2661-2671`](../../internal/controller/rolling_update.go#L2661-L2671)), which alone gives the next wait a fresh budget, and return
  `RollingUpdateResult{DeferredRequeueAfter: rollingUpdateRequeueDelay}` instead of the empty
  result. Then the state annotation survives, and `clearStaleRollingUpdateState`
  ([`:832-843`](../../internal/controller/rolling_update.go#L832-L843)) leaves `replacing-replicas` alone, so D4 holds without an
  exception. `reconcileWorkload` counts the data tier as holding
  ([`valkey_controller.go:363`](../../internal/controller/valkey_controller.go#L363)), so the Sentinel roll stays held (the D11 exception
  closes), and the pass ends on a 10 s recheck on every topology (the resume gap closes). A pause
  no longer clears drain stamps. One implementation detail: the clear has to fail the pause on a
  write error, as `clearRollingUpdateState` does, not discard it as `clearSyncWaitTimestamp` does
  today ([`:2670`](../../internal/controller/rolling_update.go#L2670)), or a failed clear leaves the
  expired annotation behind and the next wait starts already expired (ADR 0010 D7, D8). Cost M:
  about ten lines; the pause tests
  (`TestVerifyReplacedReplicasSynced_TimeoutPausesUpdate`,
  [`rolling_update_test.go:3875`](../../internal/controller/rolling_update_test.go#L3875);
  `TestWaitForReplicasReady_PausesTheUpdateOnceTheBoundExpired`,
  [`failover_sync_gate_test.go:119`](../../internal/controller/failover_sync_gate_test.go#L119))
  with revert checks; the sentences that say the pause clears the state
  (`api/v1/valkey_types.go:56-57`, `status.md:21`, `rolling-updates.md:18`, ADR 0024 `:530`,
  ADR 0026 `:618`) and every statement of the D11 exception (ADR 0026, ADR 0010 `:800-812`,
  `valkey_controller.go:406-410`, `README.md:532`, `status.md:49` and `:53`, `CLAUDE.md:488-489` and
  `:610-611`). Leaves one Warning per `syncTimeout` cycle, as today, and D4's broader question
  (is a wait that re-arms forever bounded?) where it is today: reported, not answered.

C is marked because it makes D4 hold instead of writing an exception into it, closes two
recorded gaps (the resume gap without Sentinel, the D11 exception) with one change local to
`pauseRollingUpdate`, and keeps the self-healing retry that B gives up. A is the fallback if the
documentation churn is not worth a low-severity item; its amendment then has to state the resume
gap.

## Work list

1. **XS, no decision needed** *(added 2026-09-27)*: correct the comment at
   [`rolling_update.go:2640-2642`](../../internal/controller/rolling_update.go#L2640-L2642) so it says the empty result ends the data
   roll's work for this pass, not the pass: `reconcileWorkload` goes on to the Sentinel roll (the
   ADR 0026 D11 exception) and, unless ~~that~~ *(review 2026-09-27: a post-update check - the
   Sentinel roll, the no-master recovery or the split-brain check -)* ends the pass, to
   `updateStatus`. Comment only; does not close this ticket. *(Added 2026-09-27, review:)* in the
   same change, make [`rolling-updates.md:18`](../operations/rolling-updates.md) precise: "phase
   `Error` for that pass" becomes "phase `Error` only until the next status write, normally later
   in the same pass" (doc
   only, true today and under every option until the option lands). **Done 2026-09-27**, both
   halves.
2. **Waits on the decision**: the option's code, tests and documents as listed; then replace the
   five T23 citations with ADR 0010 D4, `git grep` T23 outside `docs/tickets/`, archive.

## Decision

None yet.

## Verification

- Item 1: the comment no longer says the pass ends, `rolling-updates.md:18` no longer says the
  phase is `Error` for the whole pass; `make lint` is green. *(2026-09-27 after the fix: both
  read as required, History; `make lint` was not run.)*
- C: unit tests show that a pause keeps the state annotation and the drain stamps, returns a
  positive `DeferredRequeueAfter`, and that on a Sentinel cluster the pass that pauses does not run
  the Sentinel roll; each fails against today's body. `make test-unit`, `make lint`, `make cyclo`.
  A Kind reproduction of a pause without Sentinel, if one can be built; otherwise the resume gap
  stays recorded as not measured.
- A: ADR 0010 D4 names the exception with its date and states the resume gap.

## History

- 2026-09-27: urgency `now` -> `icebox` (rule 5): item 1, the only rule-1 statement, landed; what is left waits on the ADR 0010 D4 re-decision. Applied as the History entry below derived it.
- 2026-09-27: work list item 1 landed, file by file (read in `git diff` of the working tree):
  - [`internal/controller/rolling_update.go`](../../internal/controller/rolling_update.go), the
    return comment in `pauseRollingUpdate` (`:2640-2644` now, three lines became five): "That
    ends the data roll's work for this pass, not the pass: reconcileWorkload goes on to the
    post-update checks, the Sentinel roll included (ADR 0026 D11), and to updateStatus unless
    one of them ends the pass. Nor does it end the roll: any later pass that finds an outdated
    pod dispatches again on a fresh budget". The `(T23)` citation in the function's doc comment
    (`:2615`) is untouched; it belongs to work item 2 and ticket 040.
  - [`docs/operations/rolling-updates.md:18`](../operations/rolling-updates.md): "phase `Error`
    for that pass" became "phase `Error` only until the next status write, normally later in the
    same pass", with a dated corrected marker.

  Line drift: the comment grew by two lines, so every `rolling_update.go` reference above from
  `:2643` on (`:2643`, `:2661-2671`, `:2670`, `:2702-2714`, `:2798-2825`, `:2847-2956`,
  `:3420-3493`, `:4005-4027`) is two lower than the working tree now; the references before
  `:2640` are unchanged. The appendix finding (`clearSyncWaitTimestamp`), item 2 and the
  decision are untouched. **Urgency not recomputed in this pass** (the orchestrating run left
  every urgency but one to the owner): by the frontmatter's own derivation it falls from `now`
  to `icebox` (rule 5), because item 1 was the only rule-1 statement and the fix waits on an
  ADR 0010 re-decision. **Not verified:** `make lint` was not run.
- 2026-09-27: adversarial review of the enrichment. Re-read at `4a7543e`: `rolling_update.go:2618-2644`,
  `:2636`, `:2640-2643`, `:2670`, `:1218-1221`, `:832-843`, `:2423`, `:2455-2458`, the pause call
  sites, `:3420-3493`; `valkey_controller.go:336-342`, `:363`, `:377-379`, `:406-410`,
  `:2235-2260`, `:2475`, `:2986-2999`; ADR 0010 `:240-245`, `:800-812`; the five T23 citations
  hold. Changes: the pause pass reaches `updateStatus` unless any post-update check ends it
  (struck and corrected in place, also in the work-list comment); the referenced-Secret watch
  added to the re-dispatch triggers; `rolling-updates.md:18` named as imprecise and added to work
  list item 1 (doc only); option C's unexamined leftovers added to Not verified. Option C stays
  marked. **Verified:** by reading. **Not verified:** nothing was run.
- 2026-09-27: enriched - re-verified at `4a7543e`, corrected the stale location and the halt and
  D3 claims in place, added Fact, Impact, Options (C recommended), Work list and Verification.
  Found that without Sentinel the pass that pauses ends with no requeue, so the roll waits for an
  event rather than cycling (traced by reading). Urgency `later` -> `now` by rule 1 (the comment
  at `rolling_update.go:2640-2642` is false by code reading; back to `icebox` under rule 5 once
  work list item 1 lands, since no fix is decided and the re-decision is a human call); effort
  `L` -> `M` (the recommended option C). **Verified:** by reading and grep at `4a7543e`.
  **Not verified:** nothing was run; the drain-stamp consequence is not traced.
- 2026-09-27 - extracted verbatim from the collection ticket (now [archive/039-findings-from-the-1-11-0-fleet-rollout.md](archive/039-findings-from-the-1-11-0-fleet-rollout.md)) into its own file when the tickets were numbered. Frontmatter filled from the final board row (board archive of that file, groomed 2026-09-26) and from the section text.
