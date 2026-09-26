# ADR 0010: Every Rolling-Update Wait Is Bounded and Has a Named Exit

## Status

Accepted. Date: 2026-08-21.

Amended 2026-08-26: D15 gains a clarification — "one-shot verdict" means the condition is
**history**, and the type comment that claimed liveness was corrected. No rule changed and
no code changed; the two consequences of the historical reading (the freeze on class exit,
and what a clear would destroy) are recorded under D15 as accepted rather than fixed.

Amended 2026-08-27: **D16 is new — the wait for a pod that was deleted and never recreated
is a bounded observation.** The three recreation waits (`replaceNextReplica`,
`replaceRemainingPods`, the standalone loop) were the last unbounded waits in the rolling
update *(they were not — corrected 2026-09-26: the availability waits of D17 were unbounded
too, and further requeues of another class still are, see Residual risks)*, found by the T10
wedge: an immutable-field sync error stops the StatefulSet
controller from creating the pod, the pass ended on the wait forever, and with it the status
write, the steady-state split-brain check and the Sentinel roll *(superseded 2026-09-26 for the
last of the three: the stall no longer buys the Sentinel roll back — a holding data tier holds
it, see D16 and [ADR 0026](0026-a-pod-being-deleted-is-not-available.md) D11)*. `recreationWait` now rides
the D7/D8 `ensureWaitBound` infrastructure (there is no pod object to read a timestamp
from), and past `podRecreationOverrun` = 2 min the pass stops ending on the wait —
`DeferredRequeueAfter` plus the `PodRecreationStalled` condition, the ADR 0026 D5 shape
applied to the absent pod instead of the terminating one. The wait itself is unchanged (the
operator cannot create the pod; only its controller can), the bound is cleared per episode
on the exists path so one roll's sequential waits each own their budget, and no Event is
emitted (ADR 0025 D7). Also 2026-08-27: the `RollingUpdateComplete` Event now states the end
state each of the three completion exits actually reached, and the verify-incomplete exit
emits the completion marker it used to omit — the message drift D3/D5 owned is fixed (T17).

Amended 2026-09-26: **D17 is new — the wait on a pod that exists, is not being deleted and is
not available is a bounded observation on the pod's own clock, and an outdated pod is not
waited on at all.** Filed as T32 out of the T31 analysis and made a prerequisite of T31's
release, because T31 is the first change that rolls every multi-replica data tier and every
Sentinel tier at the operator upgrade ([ADR 0032](0032-generated-pods-run-rootless.md)): a
replacement that never came up — an unpullable image, a crash at boot, a request no node can
schedule — held the roll with a plain requeue at every site that asks for an available
pod, with no bound, no condition and no pod named, and the pass ended on it before the status
write. Outdated pods are now replaced rather than waited for
([ADR 0026](0026-a-pod-being-deleted-is-not-available.md) D11, the primary home of the
decision), which removes the waits a spec fix could never end; what remains waits on a pod a
delete would not help, is bounded by `spec.rollingUpdate.syncTimeout` measured on the pod's
own clock, and reports `PodAvailabilityStalled`. **D16 is amended with it:** a holding
data tier no longer releases the Sentinel roll, for all three stall conditions, so the Sentinel
roll left the list of what the stall shape buys back (marked in place above and in D16). The
residual-risk entry on "a pod-0 that does not match the template for any other reason" is
closed. Implemented on branch `feat/rootless`, not yet released. Guarded by the unit tests in
[`internal/controller/pod_availability_test.go`](../../internal/controller/pod_availability_test.go);
by the delete-site tests rewritten to the new rule, `TestReplaceNextReplica_ReplacesACandidateThatIsNotReady`
and `TestReplaceRemainingPods_ReplacesAnOutdatedPodThatIsNotReady` in
[`sentinel_failover_test.go`](../../internal/controller/sentinel_failover_test.go) and
`TestHandleStandaloneRollingUpdate_ReplacesAnOutdatedPodThatIsNotReady` in
[`rolling_update_test.go`](../../internal/controller/rolling_update_test.go); by
`TestReconcileWorkload_StalledTerminationHoldsTheSentinelRoll` in
[`pod_termination_test.go`](../../internal/controller/pod_termination_test.go), which replaced
the test asserting the superseded release; and, end-to-end for the data tier on a 3+3 Sentinel
cluster, by `TestE2E_RollingUpdate_UnavailableReplacementIsReportedAndReplaced` in
[`test/e2e/pod_availability_test.go`](../../test/e2e/pod_availability_test.go). An adversarial
review the same day, before anything was committed, changed the implementation in four places
and narrowed one claim; the superseded wording never left the working tree, so D16's amendment,
D17 and the Residual risks state the reviewed rule directly. The Sentinel quorum guard applies
only to a delete that spends a vote; the report is retracted on evidence, never on a pass that
stopped at another wait; a Ready=False kubelet stamped at its first sync is not a transition, so
it does not restart the clock; the Sentinel scan names the pod down longest, not the lowest
ordinal. The narrowed claim: the ordering "the Sentinel tier rolls after the data tier" has one
known exception, the pass in which a data roll pauses. The four changes are guarded by
`TestSentinelRollingUpdate_ReplacesANonVotingPodWhenQuorumIsAlreadyLost`,
`TestReportAvailabilityStall_RetractsOnlyOnEvidence`,
`TestSentinelRollingUpdate_TerminationPriorityDoesNotRetractTheReport`,
`TestPodNotReadySince_FirstSyncIsNotATransition` and
`TestSentinelRollingUpdate_ReportsTheLongestUnavailablePod`, all in `pod_availability_test.go`.
The review also surfaced one pre-existing unbounded Sentinel wait, ~~**open and awaiting a
decision**~~ *(decided 2026-09-26, see the next amendment)* (a tier of one or two Sentinels,
see Residual risks). Verified 2026-09-26: `make test-unit` green,
with 36 mutation checks across the T32 and T31 guards all killed; `make test-integration`,
`make lint` and `make cyclo` green; the e2e above green on Kind (control plane + 3 workers,
Kubernetes v1.36.1, Valkey 9.1.1) — `PodAvailabilityStalled=True/ValkeyPodNotAvailable` named
the stuck replica about 64 s after the image change with `syncTimeout` 60 s, no Sentinel pod UID
changed over the 30 s hold window, and after the image was put back the operator replaced the
stuck pod itself, the phase returned to `OK`, the condition read `False/PodAvailable` and 100 keys
were on every replica. The full e2e suite ran the same day, locally on that Kind cluster and
not in CI (~~the branch has not been through the pipeline~~ *(corrected 2026-09-26: the branch
was pushed as `e2ce8bb`, where two gate jobs failed — [ADR 0017](0017-test-and-ci-policy.md) D49;
what that run's E2E legs reported is not recorded in this repository)*): `make test-e2e E2E_VALKEY_LINE=9`
51/51 green and `make test-e2e E2E_VALKEY_LINE=8` 51/51 green on the final image, both
including this e2e, which was also re-run green on the final image against Valkey 9.1.1.
*(Clarified 2026-09-26: these runs predate the two decisions taken after the D17 commit — the
serial roll of the next amendment and the second roll of a migrated persistent data tier,
[ADR 0032](0032-generated-pods-run-rootless.md) D2 — which changed `rolling_update.go`,
changed `TestE2E_FleetUpgrade` and added `TestE2E_RollingUpdate_TwoSentinelsRollSerially`.
~~Neither of those two e2e tests has been run, so these runs say nothing about that code.~~)*
*(Updated 2026-09-26: that code has run since, locally on Kind and not in CI, on one operator
image built from ~~the final code of the branch~~ the code before D14's single failover write
and [ADR 0025](0025-a-split-brain-warning-means-one-that-did-not-resolve-itself.md) D9's clock
*(corrected 2026-09-26: both came later the same day, see the D14 amendment below)* — Kubernetes
1.36.1, containerd 2.3.1, runc 1.4.2, Linux 6.10: both full suites green, 53/53 on Valkey 9 and
53/53 on Valkey 8, this e2e and `TestE2E_RollingUpdate_TwoSentinelsRollSerially` included, and
`TestE2E_FleetUpgrade` green from 1.12.8.)* *(Run again 2026-09-26 on one image built from the
code with both, same Kind stack: `TestE2E_FleetUpgrade` green from 1.12.8, the full suite 53/53
on Valkey 8 and 52/53 on Valkey 9, this e2e and the two-Sentinel e2e green on both lines. The
one failure was `TestE2E_SidecarFailoverDrainMaster`, a fixture that waited on controller state
after deleting a pod; fixed afterwards and green 8 of 8 on Valkey 9
([ADR 0017](0017-test-and-ci-policy.md) D50).)*

Amended 2026-09-26, after D17 was committed: **the Sentinel wait the review left open is
decided — a tier of one or two Sentinels rolls serially
([ADR 0024](0024-the-sentinel-tier-reports-its-own-completion.md) D10, the home of the
decision).** Its quorum equals its size, so the guard refused every delete that spends a vote
and the roll requeued forever with nothing named. The guard is now `sentinelDeleteKeepsVotes`:
unchanged for three or more Sentinels, and on a tier of one or two it admits a delete that
leaves `total-1` voters — one Sentinel at a time, only while every other one is available.
What is left of that wait is the class this ADR already bounds or records: an unavailable
current Sentinel through `availabilityWait` (D17), a terminating one through `terminationWait`,
a missing one as the plain requeue of the Residual risks. The residual entry is closed in
place, and D17's quote of the guard is corrected. Implemented on branch `feat/rootless`, not
yet released. Guarded by `TestSentinelRollingUpdate_SmallTiersRollSerially` and
`TestSentinelDeleteKeepsVotes` in
[`pod_availability_test.go`](../../internal/controller/pod_availability_test.go), and
end-to-end by `TestE2E_RollingUpdate_TwoSentinelsRollSerially` in
[`test/e2e/pod_availability_test.go`](../../test/e2e/pod_availability_test.go) — that a
two-Sentinel tier rolls, completes and keeps the data, not the one-at-a-time order, which only
the unit test asserts — which ~~**has not been run yet**~~ *(green 2026-09-26, locally on Kind
and not in CI, on both Valkey lines: in an earlier run and inside both full suites on ~~the final
image of the branch~~ the image before D14's single write *(corrected 2026-09-26)*, and again
inside both full suites on the image with it)*. Verified 2026-09-26: `make test-unit` green on
the working tree, both unit tests included.

Amended 2026-09-26, later the same day: **D14 — an arming write belongs in the same update as
the state it bounds.** The two sites that enter `stateFailoverTriggered`,
`handleMasterFailover` and `handleFailoverRetrigger`, wrote the state and then, in a second
update, the failover timestamp; they now write both in one (`setFailoverTriggered`). Recorded
at D14. Guarded by `TestHandleRollingUpdate_ArmsTheFailoverStateWithItsTimestamp` in
[`split_brain_failover_test.go`](../../internal/controller/split_brain_failover_test.go) (the
mutation that splits the write again is killed), and by
`TestHandleMasterFailover_SurfacesTheTimestampWriteFailure` and
`TestHandleFailoverRetrigger_SurfacesTheTimestampWriteFailure` in
[`sentinel_failover_test.go`](../../internal/controller/sentinel_failover_test.go), adapted to
the single write. *(Scope, read 2026-09-26, not run: the arming test drives only
`handleMasterFailover`, and the mutation it kills splits `setFailoverTriggered` itself. Two
writes put back at `handleFailoverRetrigger` alone would fail none of the three tests, nor any
other `handleFailoverRetrigger` test: the
adapted retrigger test fails from the first write, which leaves the reset state and its old
stamp standing under either shape.)* *(Closed the same day:
`TestHandleFailoverRetrigger_ArmsTheFailoverStateWithAFreshTimestamp` requires the first write
that names `failover-triggered` at the retrigger to carry a stamp other than the reset's; the
mutation that puts the two writes back at `handleFailoverRetrigger` alone is killed.)* No e2e injects the failed write; the image with the change
ran the suites above. Implemented on branch `feat/rootless`, not yet released.

Implemented on branch `feat/support-pdb`, not yet released — no tag contains this
branch's HEAD and none of the files named below exist on `origin/main`. Guarded by
[`internal/controller/rolling_update_bounds_test.go`](../../internal/controller/rolling_update_bounds_test.go),
[`topology_restore_stall_test.go`](../../internal/controller/topology_restore_stall_test.go)
and, end-to-end for D1–D3, `TestE2E_RollingUpdate_TopologyRestoreAbandoned` in
[`test/e2e/topology_abandon_test.go`](../../test/e2e/topology_abandon_test.go).
Every bound was revert-verified during development; that run leaves no artifact in this
repository and is not reproducible from it.

Amended 2026-08-21: the last two unconverted bounds,
`ensureSentinelAwarenessTimestamp` and `ensureSyncWaitTimestamp`, now go through
`ensureWaitBound` / `waitBoundExceeded` (D14 closed for both). Their reset sites
(`incrementReconnectResetCount`, `clearSentinelAwarenessTimestamp`,
`clearSyncWaitTimestamp`) drop the in-memory copy alongside the annotation, and
`forgetWaitBounds` covers the end-of-update and CR-deletion paths. Guarded by
`TestSentinelAwarenessBound_*`, `TestSyncWaitBound_HoldsWhenArmingWriteFails` and
`TestClearSyncWaitTimestamp_ForgetsTheBound` in
[`rolling_update_bounds_test.go`](../../internal/controller/rolling_update_bounds_test.go).

Amended 2026-08-22: D3 gains an ordering. `TopologyRestored` was written *after* the
state transition and its write error was swallowed, so a single rejected status update
lost the verdict permanently — the writer never runs again. It is now written **before**
the transition, and a conflict fails the pass instead (D15). Found by
`TestE2E_RollingUpdate_TopologyRestoreAbandoned` failing on a condition that never
appeared while the abandon itself had happened
([run 32577672028](https://github.com/guided-traffic/valkey-operator/actions/runs/32577672028)).
Guarded by `TestAbandonTopologyRestoration_ConflictHoldsPhase1`,
`TestAbandonTopologyRestoration_ConflictRetriedThenRecorded`,
`TestAbandonTopologyRestoration_PermanentFailureStillEscapes` and
`TestPromotePod0AndRedirect_ConflictHoldsPhase1` in
[`topology_restore_stall_test.go`](../../internal/controller/topology_restore_stall_test.go).

Amended 2026-08-22: **D13 is restated.** The gates in front of the promotion now share the
`syncTimeout` budget and can pause the rolling update, so `verifyReplacedReplicasSynced` is no
longer its only consumer ([ADR 0007](0007-failover-aware-rolling-update.md) D10). The property
D13 protects is unchanged: none of them runs while the restore phases hold the state.

## Context

The non-Sentinel rolling update is a state machine that waits: for a replaced pod to come
back, for a promoted replica to attract replicas, for pod-0 to re-sync, for every replica
to reconnect. Each wait was a bare `NeedsRequeue`, and the outer loop offered no escape:
`clearStaleRollingUpdateState` runs only on the `replacedCount == 0` branch, and during
topology restoration every pod is updated by definition, so dispatch never reaches it.

The consequences of an unbounded wait are worse here than a stuck update, because
`reconcileWorkload` returns on `NeedsRequeue`
([`internal/controller/valkey_controller.go:299`](../../internal/controller/valkey_controller.go))
**before** `handlePostRollingUpdateChecks` and `updateStatus`. A parked state machine
therefore also parks:

* the steady-state split-brain check
  ([ADR 0011](0011-evidence-based-steady-state-split-brain-resolution.md)),
* the status write, so the phase freezes at `Rolling Update N/M`,
* the `TopologyRestored` condition, which is never written at all.

The causes are entirely routine: a PVC that cannot bind, `ImagePullBackOff` on the new
tag, a fail-closed admission webhook rejecting the pod CREATE, the operator killed between
a promote and a delete, a rejected pod `Delete` that leaves the state annotation already
written.

## Decision

**D1 — Topology restoration runs in two explicit, separately-bounded phases.** Phase 1
(`stateRestoringTopology`, `handleTopologyRestoration`) waits for pod-0 to sync back from
the promoted replica and then promotes it again. Phase 2 (`stateVerifyingTopology`,
`verifyTopologyRestored`) confirms every replica reconnected. A stalled sync and a stalled
reconnection are different problems with different safe outcomes, so they get different
budgets and different exits.

**D2 — Phase 1 is bounded by `spec.rollingUpdate.syncTimeout` (default 5 m) in its own
annotation `vko.gtrfc.com/topology-restore-started`.** `syncTimeout` is the right budget
because it is the same wait — a replaced pod pulling a full dataset — and it is the only
candidate a user can raise when the dataset does not fit the default. It gets its **own**
annotation: reusing `annotationSyncWaitStarted` would inherit a stamp the replica-replacement
phase leaves behind on every early return, and reusing the finalization stamp would let a
long Phase 1 eat Phase 2's budget.

**D3 — On Phase 1 timeout, give up the topology, never the data. Pod-0 is never
force-promoted.** An unsynced pod-0 forced to master comes up empty and discards the
promoted replica's writes. `abandonTopologyRestoration` records the
`TopologyRestoreAbandoned` Warning, sets `TopologyRestored=False` as the durable record,
arms the Phase 2 bound and enters `stateVerifyingTopology` — **in that order**, amended
2026-08-22: the record precedes the state transition, because the transition is what makes
the record unwritable (D15). The superseded order wrote the condition last and swallowed
its error. It writes no phase itself: the
phase returns to `OK` a pass or more later, through `updateStatus`, once Phase 2 reports
`Completed` — because the cluster **is** healthy. The supported end state of a failed
restoration is "finished, single master, not pod-0", not "stuck".

**D4 — A bounded phase escapes *into* the next bounded phase, never around the state
machine. Clearing `vko.gtrfc.com/rolling-update-state` on expiry is forbidden.** Once the
state annotation is gone, `checkAndHandleRollingUpdate` early-returns whenever no pod needs
an update, and `detectAndResolveSplitBrain` has no caller outside the rolling update.
Phase 2 is the last pass that can consolidate the masters a half-finished failover leaves
behind. Every future abandon path must name a successor state rather than resetting.

**D5 — Phase 2 is bounded by `finalizationStallTimeout` (2 m, own annotation) on *every*
branch, not only the rogue-master one.** A permanently failing `collectPodStates` or pod
lookup completes the update unverified and records `TopologyVerifyIncomplete`. A bound is
only a bound if every exit from the wait is covered — before this, a failing pod lookup
requeued forever (the shape still on `origin/main`, where only the rogue-master branch
consults `isFinalizationStalled`), one function over from the defect Phase 1 had just
fixed, which would have made the Phase 1 hand-over land in a second infinite loop.

**D6 — Every manual-failover wait branch is bounded and expires into Phase 2.** All six
*wait* branches of `handlePostManualFailover` (pod-0 `IsNotFound`, `DeletionTimestamp != nil`,
`podNeedsUpdate`, `!isPodReady`, `buildTLSConfig` failure, failed `REPLICAOF`) return
`waitOrAbandonManualFailover`. Six is the count of the waits, not of the function's exits:
a missing `vko.gtrfc.com/promoted-pod` clears the state and a non-`NotFound` `Get` error on
pod-0 returns an error, and neither of those waits, so neither takes a bound.
Budget: `v.GetSyncTimeout()`, armed as `vko.gtrfc.com/manual-failover-started` inside
`persistManualFailoverState`. On expiry the handler calls `abandonTopologyRestoration` —
reusing it rather than adding a second abandon path buys the `TopologyRestored=False`
condition, the Phase 2 hand-over and the Phase 2 budget arming for free.

**D7 — Bounds are dual-stored, and an arming error is never discarded.** `ensureWaitBound`
records a first-seen entry in the in-memory `nudgeTracker` **and logs** the annotation
`Update` error; `waitBoundExceeded` prefers the annotation (survives an operator restart)
and falls back to the in-memory stamp (survives a failing API server).

> **A bound that can silently fail to arm is not a bound.**

The concrete failure it closes: `_ = r.Update(ctx, v)` on the arming write means that under
persistent conflicts or an admission gate on the CR, the annotation never persists, the
stall check stays `false` forever, and the phase requeues indefinitely — the exact stall the
bound exists to break.

**D8 — An annotation that could not be persisted is deleted from the in-memory object.**
Leaving it there makes every later pass re-arm it with a fresh timestamp, `waitBoundExceeded`
reads that instead of the tracker, and the deadline is never reached — the same defect one
indirection deeper.

**D9 — Wait-bound tracker keys are namespaced with `/`.** The data StatefulSet is named
after the CR, so without a separator a wait-bound key would be identical to that
StatefulSet's nudge key in the shared tracker. `/` cannot appear in a Kubernetes object
name, which makes the separation total rather than probabilistic.

**D10 — Every bounded state arms its own bound on entry, in the same `Update` as the state
write, and never inherits a leftover stamp.** `armWaitBound(v, annotation, bound)` is the
shared body; `armTopologyRestoreBound` is called immediately before
`setRollingUpdateState(stateRestoringTopology)`, and `armFinalizationBound` at both
`stateVerifyingTopology` entries (`abandonTopologyRestoration` and `promotePod0AndRedirect`).
A rolling update that died mid-phase leaves its stamp behind, and the next update — if it
starts with at least one pod already matching the new template — dispatches straight into
that phase against an hours-old timestamp: immediate abandonment with no attempt at all.

**D11 — A deadline is computed once, before the first write attempt.**
`armManualFailoverBound` stamps before `persistManualFailoverState`'s conflict retry, so the
retry re-applies the *same* deadline. Arming inside the retry loop would grant a new full
budget per attempt, silently turning a bounded wait back into an unbounded one.

**D12 — Bounds are also armed defensively at the wait site, not only at state entry.**
`waitOrAbandonManualFailover` calls `ensureWaitBound` before evaluating the budget, so a
state written by an older operator version, or one whose arming write never landed, still
acquires a bound on the first pass that observes it — otherwise the fix would not reach
exactly the clusters already stuck.

**D13 — The shared `syncTimeout` budget is two-sided only during the replica replacement
and the failover step.** `dispatchMultiReplicaState` routes `restoring-topology` and
`verifying-topology` **before** `replaceNextReplica` and before the failover step, so no
consumer that can call `pauseRollingUpdate` runs during the restore phases. A short
`syncTimeout` chosen to make Phase 1 abandon quickly therefore cannot trip
`RollingUpdatePaused` from inside the restore phases.

~~`verifyReplacedReplicasSynced` is the only consumer that can call
`pauseRollingUpdate`~~ (superseded 2026-08-22): the pre-promotion gates share the same
budget through `waitOrPauseForReplicaSync` — `waitForReplicasReady`, the zero-acknowledgement
branch of `waitForWriteSync`, and `verifyPromotionCandidateHoldsData`
([ADR 0007](0007-failover-aware-rolling-update.md) D10). They sit in the failover step, which
the restore states never reach, so the property above is unchanged; what changed is that four
consumers now share one annotation rather than one.

**D14 — An arming write whose error is discarded is a defect, not a duplication to fold.**
Of five inline RFC3339 stall checks, three are readability hygiene — their stamp is written
by `setFailoverTimestamp`, which returns the `Update` error, and every caller checks it
*(since 2026-09-26 the two trigger sites write it through `setFailoverTriggered`, see the
amendment below; `incrementReconnectResetCount` writes it too, in the update that carries the
reset count, and its caller checks the error — read)*. The
other two (`isSentinelAwarenessStalled`, `isSyncWaitTimedOut`) rest on an arming write whose
error is discarded, are live unbounded stalls and are tracked as defects. Filing a live
unbounded-requeue defect as a readability cleanup means it ships whenever nobody gets round
to the cleanup.
*(Amended 2026-09-26.)* A checked error was not enough for the failover stamp: both sites that
enter `stateFailoverTriggered` wrote the state and then, in a second update, the timestamp. When
the second write failed, the state stood without its stamp, and `annotationTimestampExceeded`
reads a missing stamp as never expired — `isFailoverTimedOut` and `isReplicaReconnectTimedOut`
never fired (read in code; no run reached it). *(Narrowed 2026-09-26, read: that holds at
`handleMasterFailover`, the first trigger, where no stamp stands. At `handleFailoverRetrigger`
the stamp `handleNoMasterFound` wrote for the reset stood instead, at least `failoverResetMinWait`
= 20 s old, so there the bounds fired early rather than never — the pre-change
`TestHandleFailoverRetrigger_SurfacesTheTimestampWriteFailure` asserted exactly that old stamp
standing.)* `setFailoverTriggered` now writes both in one
update (`TestHandleRollingUpdate_ArmsTheFailoverStateWithItsTimestamp`; the mutation that splits
the write again is killed). **An arming write belongs in the same update as the state it bounds.**
The same missing stamp would also have held open the window of
[ADR 0025](0025-a-split-brain-warning-means-one-that-did-not-resolve-itself.md) D9, which since
carries its own clock and treats a missing stamp as no window.
**Not applied everywhere yet:** `handleNoMasterFound` still writes the stamp and then
`stateFailoverReset` in two updates. The stamp goes first, so a failed second write leaves
`failover-triggered` standing with a fresh stamp *(or `replacing-master`, the other state
`handlePostFailover` serves and that therefore reaches it; added 2026-09-26)* — ~~bounded,
because~~ *(corrected 2026-09-26: not unbounded the way a missing stamp is, because the fresh
stamp expires and)* the next pass past
`failoverRetryTimeout` tries again, but each such failure in `failover-triggered` re-opens ADR
0025 D9's window by 90 s
(read; `TestHandleNoMasterFound_SurfacesTheStateWriteFailure` pins the state standing, not the
stamp).

**D15 — A one-shot verdict is written before the transition that ends its last writer, and
a conflict fails the pass.** `TopologyRestored` has exactly two writers,
`abandonTopologyRestoration` and `promotePod0AndRedirect`, and each enters
`stateVerifyingTopology` in the same pass; no path returns to Phase 1. The condition is
therefore not a steady-state report that the next pass recomputes — the reasoning
`setStatusCondition` documents for `ReconcileBlocked` and `SidecarUpdatePending` does not
transfer — and swallowing its write loses the verdict for the life of the cluster. Both
writers call `recordTopologyRestoredCondition` first and return
`RollingUpdateResult{Error}` when it reports a conflict; the state annotation stays where
it is, the CR is still stalled, and the next pass writes the verdict. This is
[ADR 0009](0009-an-unrecorded-promotion-is-not-a-promotion.md) applied to the abandon: an
abandon the operator could not record is not a completed abandon.

Clarified 2026-08-26: **"one-shot verdict" means the condition is history, and its own type
comment used to say otherwise.** The rule below is unchanged; what was missing is the
consequence for a *reader*. Because nothing outside a rolling update writes
`TopologyRestored`, a steady-state adoption ([ADR 0011](0011-evidence-based-steady-state-split-brain-resolution.md))
or a no-master recovery moves the master and leaves the condition where it stood — so a
`True` reading "pod-0 was promoted back to master" can sit next to a non-pod-0 master
indefinitely. Measured read-only on a live fleet (2026-08-25): a chaos pod-kill promoted
pod-1 two minutes after a roll ended, and three days later the CR still carried
`TopologyRestored=True` naming pod-0, with `status.masterPod`, the `known-master` annotation
and the labels all correctly on pod-1.

That is this rule working, not a defect — ADR 0011's Context already books it as an accepted
trace-gap, and README's condition table already said "the **last** rolling update". The
defect was that
[`api/v1/valkey_types.go`](../../api/v1/valkey_types.go) claimed the condition was "the only
durable record that the topology differs from the canonical one", a *live* claim no writer
supports, and that the `currentMasterPod` doc comment read the same way. Both now state the
direction explicitly: **`status.masterPod` is the live answer ([ADR 0002](0002-surface-a-blocked-reconcile-on-the-cr.md)
D11); this condition answers what the last update did.** A fleet audit read the two together
and filed the pair as a bug, which is what a documentation defect in this area costs.

Two consequences of the historical reading are recorded rather than fixed. A CR that leaves
the multi-replica-non-Sentinel class — `spec.sentinel.enabled: true`, or a scale to one
replica — reaches no writer again, so the verdict freezes for the life of the cluster with
no path back; under the historical reading that is correct, and the presence-guarded
class-exit clear that `SentinelUpdatePending` gets is deliberately **not** applied here,
because ADR 0002 D10 is scoped to deferred *work* and extending it to a completed *verdict*
is a different decision. And any clear that were added would have to carry the prior verdict
forward: overwriting a standing `False`/`RestoreTimeout` discards the only durable record of
an abandon, which is exactly what this rule exists to protect.

Two limits are part of the rule. **Only a conflict is handed back.** Anything that repeats
identically on every pass — a withdrawn RBAC on the `valkeys/status` subresource is the
realistic one — is logged and the state machine advances without the record, because
returning it would pin the update in `stateRestoringTopology` forever and trade the lost
condition for the unbounded wait D2–D4 exists to remove. And **the write reads the CR
again on every attempt** (`writeStatusCondition`, `retry.RetryOnConflict`): the read goes
through the manager cache, so a writer that just updated the CR itself reads back the
version from before its own write and is rejected with a 409 that has no competing writer
behind it. That is the failure CI hit; putting the record before the state write removes
the preceding update as well, so the retry is the second line of defence, not the first.

**D16 — The wait for a deleted pod that is never recreated is a bounded observation.**
Added 2026-08-27 (T10). The three sites that wait for the StatefulSet controller to recreate
a pod the roll deleted — `replaceNextReplica`, `replaceRemainingPods` and the standalone
loop — returned a bare requeue with no bound: a wedge that makes the pod uncreatable (the
measured one is an immutable-field sync error on the lowest mismatching ordinal) ended the
pass on the wait forever, freezing the status surface and suspending the steady-state
split-brain check for as long as the wedge lasted. `recreationWait` bounds the observation,
not the wait: within `podRecreationOverrun` = 2 min the plain requeue is unchanged; past it
the pass returns `DeferredRequeueAfter` and reports `PodRecreationStalled` naming the pod
and where to look (`FailedCreate`/`FailedUpdate` on the StatefulSet). There is no pod object
to read a deadline from, so the bound rides the D7/D8 annotation-plus-tracker
infrastructure and is cleared per episode on the exists path — a single roll waits for
several pods in sequence, and a timestamp surviving the first episode would spend the
budget of all later ones. The exit is deliberately not a state transition: the operator
cannot create the pod, so there is no other bounded state to hand over to — the D1 rule is
satisfied by the observation being bounded while the wait itself remains until the pod
exists or a human clears the wedge. No Event (ADR 0025 D7); this is the ADR 0026 D5 shape
applied to the absent pod instead of the terminating one.

Amended 2026-09-26 (T32): **the stall shape no longer buys back the Sentinel roll.** D16 took
the ADR 0026 D5 shape whole, and D5 listed the Sentinel roll among what `DeferredRequeueAfter`
hands back to the pass; that item is superseded by
[ADR 0026](0026-a-pod-being-deleted-is-not-available.md) D11. `reconcileWorkload` passes
`rollingResult.DeferredRequeueAfter > 0` to `handlePostRollingUpdateChecks`, and
`runSentinelRollingUpdate` skips the Sentinel roll for that pass — for every data-tier stall,
since `terminationWait`, `recreationWait` and D17's `availabilityWait` are the only producers of
`DeferredRequeueAfter`. What the stall buys back is the status write, the no-master recovery
(`checkAndRecoverNoMaster`) and the steady-state split-brain check; the last two self-gate on
`IsMultiReplicaWithoutSentinel`, so on a Sentinel cluster it is the status write alone. The
reason is D17's common case: `spec.image` is shared by both tiers, so a Sentinel roll released
by a data replica stuck on a bad image takes a healthy Sentinel onto the same image, and the
quorum guard stops only once the spare vote is spent *(on a tier of one or two Sentinels, which
has none, after the first delete — the serial roll of ADR 0024 D10, 2026-09-26)*. Before D17 a
replica stuck on a bad image never released the Sentinel roll, because that wait was unbounded;
bounding it without this hold would have made the bad-image case worse on every Sentinel
cluster. The ordering
[ADR 0024](0024-the-sentinel-tier-reports-its-own-completion.md) builds on — the Sentinel tier
rolls after the data tier — holds again for every data-tier stall, **but not without
exception**: `pauseRollingUpdate` returns an empty `RollingUpdateResult`, no `NeedsRequeue` and
no `DeferredRequeueAfter`, so the pass in which a data roll pauses is not holding and runs the
Sentinel roll (Residual risks).

**D17 — The wait on a pod that exists, is not being deleted and is not available is a bounded
observation on the pod's own clock; an outdated pod is not waited on at all.** Added
2026-09-26 (T32). A replacement that never comes up — an unpullable image, a container that
crashes at boot, a request no node can schedule — was waited for with a plain requeue at every
site that asks for an available pod: no bound, no condition, no pod named, and the frozen tail
of the pass the Context describes — and, unlike D16's wedge, produced by an ordinary spec
mistake. Two halves:

* **An outdated pod is replaced, not waited for.** The three delete sites —
  `replaceNextReplica`, `replaceRemainingPods` and the standalone handler — delete an outdated
  pod unless it is terminating (then `terminationWait`, ADR 0026 D5); readiness is no longer
  asked. The standalone handler still defers what `singlePodDeferral` defers instead of
  deleting — a sidecar-only change, and the move off root on a pod without persistence unless
  its image, config or TLS material changed as well
  ([ADR 0032](0032-generated-pods-run-rootless.md) D3). The Sentinel roll takes an outdated
  Sentinel that is neither available nor terminating ahead of `firstOutdatedPod`, charges the target a vote only when it is available
  (`sentinelScan.deleteTarget`), and applies the quorum guard only to a delete that spends one
  (~~`cost > 0 && scan.readyCount-cost < quorum`~~ in `dispatchSentinelRollingUpdate`;
  *corrected 2026-09-26: the guard is `cost > 0 && !sentinelDeleteKeepsVotes(...)`, the same
  `readyCount-cost >= quorum` for three or more Sentinels and a serial delete on a tier of one
  or two, [ADR 0024](0024-the-sentinel-tier-reports-its-own-completion.md) D10*). With the
  quorum already lost — two of three Sentinels stuck on the broken spec after the fix,
  `readyCount` 1 — replacing a non-voting pod is the only way the quorum comes back, and a guard
  charged against `readyCount` alone refused it forever; the delete gate (ADR 0026 D5) still
  refuses the next delete while a Sentinel is terminating. After a spec fix the pod stuck on
  the broken spec is exactly the one these sites now take, so the roll moves on by itself. The
  rule and what it leaves unchanged live in ADR 0026 D11; it belongs here too because it removes
  the waits no bound could have fixed — bounding the observation of a wait that only a human
  `kubectl delete` ends still leaves the roll stuck.
* **Every remaining wait on such a pod goes through `availabilityWait`.** What remains is a
  pod on the current template, or a master, which a delete would not help:
  `verifyReplacedReplicasSynced` and `waitForReplicasReady` through `waitForUnavailablePod`
  (terminating → `terminationWait`, otherwise `availabilityWait`); the `replaceRemainingPods`
  fall-through on `firstUnavailableExisting`, where the dispatch lands while a current pod keeps
  `countUpdatedPods` below the total; the standalone wait on the current pod (`standaloneWait`),
  reached only while rolling-update state is recorded — the standalone handler writes none, so a
  current single pod that never starts takes the converged early return in
  `dispatchDataRollingUpdate` and shows as phase `Provisioning`, not as this condition;
  and the Sentinel quorum wait and completion hold through `sentinelWait`. Within
  `spec.rollingUpdate.syncTimeout` (`GetSyncTimeout()`, default 5 m) the plain requeue is
  unchanged. Past it the pass returns `DeferredRequeueAfter`, and the tier's evaluator —
  `checkAndHandleRollingUpdate` for the data tier, `checkAndHandleSentinelRollingUpdate` for
  the Sentinel tier, both through `reportAvailabilityStall` — reports `PodAvailabilityStalled`
  (`ValkeyPodNotAvailable` / `SentinelPodNotAvailable`) naming the pod and the start of its
  clock. On the Sentinel tier the named pod is the current unavailable one with the **oldest**
  clock, not the lowest ordinal (`sentinelScan.observe`): a pod that just came back on the same
  broken spec must not hide one that has been down for hours behind a fresh budget.
  `availabilityWait` itself writes nothing.

The clock is the pod's own (`podNotReadySince`): `PodReady.lastTransitionTime` while Ready is
not True; `creationTimestamp` while the pod has no Ready condition yet, the time is zero, or the
not-True Ready is the one kubelet stamped at its first status sync of the pod
(`stampedAtFirstSync`: `lastTransitionTime` no later than `status.startTime` plus
`firstSyncSlack` = 5 s); zero while the pod is Ready, and a zero clock is the plain requeue. The
first-sync case is not a transition away from Ready — such a pod was never Ready, so it has been
unavailable since it was created — and reading it as one reset the clock of a pod that sat
Pending longer than the budget to the moment it was scheduled, retracting a standing report for
a pod that never came up. A real transition back from Ready still restarts the clock (Residual
risks). Like ADR 0026 D5 and unlike D16, this is **not** an `ensureWaitBound` member: kubelet
(or, for `creationTimestamp`, the API server) writes the start time onto the pod, so nothing is
armed and nothing can fail to arm (D7/D8), no CR write is spent, an operator restart loses
nothing, and the deadline is per pod — which D16 had to buy with per-episode clears, because one
roll waits on several pods in sequence. The budget is `syncTimeout` because D6
already spends it on the same wait (the `!isPodReady` branch on pod-0) and the gates of D13
spend it on the same pod once it answers, so a user who raised it for a slow full sync raised
this one with it. D17 shares the value, not an annotation, and never calls
`pauseRollingUpdate`: the D13 property is untouched, and no call site is reachable from the
restore phases, which `dispatchMultiReplicaState` routes first.

As in D16, the exit is not a state transition and the state annotation stays (D4): a pod on the
current template comes back identical when deleted, so there is nothing to hand over to, and
this ADR's rule is met by the observation being bounded while the wait itself remains. What the stall
buys back is D16's list as amended the same day — a holding data tier holds the Sentinel roll.
The Sentinel roll's own `DeferredRequeueAfter`, which `handlePostRollingUpdateChecks` used to
drop, is now applied, merged with the steady-state check's recheck by `soonerRequeue` (the
sooner wins). The condition is a **level**, not an edge like its two siblings
([ADR 0027](0027-conditions-are-levels-edges-or-history.md)): nothing the roll does proves the
stall is over, so each tier evaluates it on every non-error pass that reaches its roll. A tier
retracts only its own reason — `False`/`PodAvailable`, written only over a standing `True` of
that tier — and **only on evidence**: `expiredUnavailablePod` finds no pod of the tier (the
ordinal range of its StatefulSet, ours only, cache reads) that exists, is not terminating, is
not Ready and has been not-Ready longer than `syncTimeout`. A pass whose result carries no stall
is not that evidence: one that stopped at another wait first — a terminating pod, a fresher
replacement on its own budget — never looked at the reported pod, and retracting on its silence
made the condition flap for as long as one stall lasted. The two tiers share one slot: a data
report raised over a standing Sentinel report replaces it, and the first Sentinel pass after the
data tier finishes that waits on the stuck pod reports it again at once, because that pod's clock
is already old — accepted, traced by reading (ADR 0026 D11). The Sentinel report is also retracted
on class exit in `runSentinelRollingUpdate`, through the same evidence check — so, traced by
reading, it stands while a Sentinel pod left behind is still down past the budget, because the
operator does not delete the Sentinel StatefulSet of a CR whose Sentinel is disabled. The message
names the instant, never a running duration, so a stall that lasts a day costs one condition
write. No Event (ADR 0025 D7).

## Consequences

* The original topology is never restored on the abandon path, and the CR carries
  `TopologyRestored=False` until the next successful restoration. **Operators must accept a
  master on a non-zero ordinal as normal**
  ([ADR 0008](0008-known-master-annotation-is-the-recorded-authority.md) D14).
* An update can complete without having verified that every replica reconnected;
  `TopologyVerifyIncomplete` is the only trace.
* A genuinely slow reconnection can be declared stalled after 2 minutes.
* Phase 2 must handle an abandoned, non-canonical topology as input, not only a successfully
  restored one — and it must be given the right split-brain authority, which is why
  [ADR 0008](0008-known-master-annotation-is-the-recorded-authority.md) D11 forbids passing
  the promoted-pod annotation there.
* One more annotation and one more state transition to reason about per bound.
* The bound of an *inherited* state starts at first observation, not at real state entry, so
  an upgraded operator grants such a CR a full fresh budget (D12).
* If the dispatch order of D13 ever changed, the two `syncTimeout` consumers would contend
  and a short timeout would pause the update instead of abandoning the restore.
* Any future escape hatch aimed at Phase 2 inherits the obligation to arm the Phase 2 bound
  at its new entry point.
* Any future bound armed inside a retrying writer must follow D11's order: stamp first, then
  retry the write with that stamp.
* A status subresource the operator may no longer write costs the `TopologyRestored`
  verdict outright (D15). The bound wins over the record, and the only trace left is the
  `TopologyRestoreAbandoned` Event plus an operator-log line.
* The `TopologyRestoreAbandoned` Event is recorded before the condition, so every retried
  abandon pass emits it again. Event aggregation turns that into a series count rather than
  duplicate objects, and `waitForValkeyEvent` in the e2e suite reads it either way.
* The Event reason does not distinguish a failover stall from a Phase 1 sync timeout —
  operators read the message text, not the reason. A distinct reason string
  (`MasterNeverReturned`) is a one-line change if it is ever wanted.
* An outdated pod that is not available is deleted at once rather than waited for (D17) —
  the same roll replaces it either way, so what changes is only when.
* A stuck replacement is reported only once `syncTimeout` has passed on its own clock: 5 min by
  default, and longer wherever a user raised the budget for a slow full sync — the two cannot be
  tuned apart. A current single pod is not reported at all (D17). `ValkeyPhaseNotOK` is the
  fallback after 30 min, but whether it survives the alternating phase of the next bullet is not
  checked.
* On a Sentinel cluster every data-tier stall — terminating, absent or unavailable — now also
  holds the Sentinel update (D16 as amended). Not urgent: the outdated Sentinels keep running.
  A NotReady-node termination stall or a T10 wedge therefore delays the Sentinel tier for as
  long as it lasts.
* During a reported stall of any of the three kinds, the pass writes the phase twice —
  `Rolling Update i/n` before dispatch, then `updateStatus`'s `Provisioning` while a pod is not
  Ready — so the phase alternates for as long as the stall lasts. Inherited from D16 and ADR
  0026 D5, not introduced by D17; traced by reading, not measured. `vko_valkey_status_phase`
  carries one series per resource labelled with the current phase, so a scrape between the two
  writes replaces the `Provisioning` series; whether that resets the `for: 30m` of
  `ValkeyPhaseNotOK` has not been checked (ADR 0026 residual risks). The three stall conditions
  are the stable signal.

## Alternatives Considered

### Leave the waits unbounded

The pre-fix behaviour, still readable on `origin/main`: `restoring-topology` on the CR
indefinitely, the update never finalized, and the whole tail of the reconcile pass —
including the steady-state split-brain check — skipped for the length of the stall.

### Force-promote pod-0 after the Phase 1 timeout

Rejected: an unsynced pod-0 comes up empty and discards the promoted replica's writes.

### Clear the rolling-update state on expiry

Named and rejected outright, twice (Phase 1 and manual failover). It strands a two-master
cluster with no consolidation pass left.

### Expire the manual-failover wait into `pauseRollingUpdate`

Louder and cheaper (`RollingUpdatePaused=True`), but it consolidates no masters and leaves a
CR that a GitOps loop will not clear.

### A distinct abandon path for the manual-failover expiry

Rejected in favour of reusing `abandonTopologyRestoration`: three behaviours that would then
have to be kept in sync.

### Reuse `annotationSyncWaitStarted` or the finalization stamp for Phase 1

Rejected: the first is left behind by the replica-replacement phase on every early return,
the second belongs to Phase 2 and sharing would let a long Phase 1 eat its budget.

### A fixed constant instead of `syncTimeout`

Rejected: not user-raisable, and the wait it bounds is exactly the one a large dataset
lengthens.

### Treat N consecutive arming failures as "stalled"

Considered, not taken — the dual store is simpler and does not need a new counter.

### A durable store for first-seen timestamps

Rejected: the operator has none, and the annotation already covers the restart case whenever
it can be written at all.

### Reject timestamps predating the current state transition, instead of re-arming

A comparison-based alternative to D10. Re-arming was preferred because it costs no extra API
call — the stamp rides the state `Update` that was happening anyway.

### Rely on `clearStaleRollingUpdateState` alone

Rejected: it only covers the `replacedCount == 0` branch, which is precisely the branch a
stalled restoration never takes.

### Separate trackers for nudges and wait bounds

Not taken: sharing `r.nudges` made the fix free, and D9's separator makes collision
impossible.

### Retry the `TopologyRestored` write until it lands, whatever the error

Rejected: it re-creates an unbounded wait through the back door. A status subresource the
operator may no longer write would hold the CR in `stateRestoringTopology` for as long as
the permission is missing — the exact stall D2–D4 removes, keyed on a different failure.
D15 hands back only the conflict, which by definition another pass can clear.

### Keep the write last and simply return its error

Rejected: by then `stateVerifyingTopology` is persisted, and no pass re-enters Phase 1.
Failing the pass would requeue into Phase 2, which does not write the verdict — the record
stays lost and only the log gets louder. The ordering is what makes the retry meaningful.

### Read the CR through an uncached `APIReader` before the status write

Considered. It removes the stale-cache conflict at its source rather than retrying past it,
but it means a live API read on every condition write and a new field on the reconciler.
Not taken: writing the record before the pass's own first update already removes the
self-inflicted conflict, and `retry.RetryOnConflict` covers a genuine competing writer.

### Fold all five inline stall checks into `waitBoundExceeded` as one mechanical cleanup

Rejected once the discarded error in two of the five was noticed — see D14.

### Bound the availability wait through `ensureWaitBound`

Rejected for D17: the pod carries a clock nothing has to arm. An annotation would bring back
the D7/D8 arming failure for a wait that has no need of it, would be first-seen-wins per CR
rather than per pod, and would need D16's per-episode clears, because one roll waits on several
pods in sequence.

### A fixed constant for D17, like `podTerminationOverrun` and `podRecreationOverrun`

Rejected: not user-raisable, and D6 already bounds the same wait on pod-0 with `syncTimeout` —
two budgets for one wait, picked by whichever state the roll happens to be in, would be
arbitrary.

### Bound the wait on an outdated pod instead of replacing it

Rejected; recorded with the other options in ADR 0026 D11. After a spec fix the stuck pod is
outdated, and a bounded observation of it still ends only when a human deletes the pod.

### Let a data-tier stall release the Sentinel roll, as ADR 0026 D5 stood

Rejected (D16 as amended): the tiers share `spec.image`, so the released roll takes a healthy
Sentinel onto the image the data tier is stuck on, and the quorum guard stops only after the
spare vote is gone *(on a tier of one or two Sentinels, after the first delete — ADR 0024 D10,
2026-09-26)*. Holding the Sentinel roll for `PodAvailabilityStalled` alone was rejected
too — it would give `DeferredRequeueAfter` two meanings depending on which wait produced it.

## Residual risks

* **(Closed 2026-08-21) `ensureSentinelAwarenessTimestamp` and `ensureSyncWaitTimestamp` were
  the two bounds never converted.** Both wrote their annotation with `_ = r.Update(ctx, v)`
  and kept no second copy, so with CR writes failing persistently their stall checks answered
  `false` forever — the Sentinel rolling update parked before ever sending
  `SENTINEL FAILOVER`, and `verifyReplacedReplicasSynced` requeued without ever reaching
  `pauseRollingUpdate`. Both now go through `ensureWaitBound` / `waitBoundExceeded`, and the
  conversion carried the obligation this entry named: the tracker is first-seen-wins, so
  every site that clears the annotation also forgets the in-memory copy
  (`incrementReconnectResetCount` and `clearSentinelAwarenessTimestamp` for the Sentinel
  bound, `clearSyncWaitTimestamp` for the sync bound) — without those `forget` calls a stale
  entry pre-expires the next attempt's budget, D10's defect one layer down. Still not
  reproduced against a cluster; the guards are unit tests
  (`TestSentinelAwarenessBound_HoldsWhenArmingWriteFails`,
  `TestSentinelAwarenessBound_ResetRebaselines`,
  `TestSyncWaitBound_HoldsWhenArmingWriteFails`,
  `TestClearSyncWaitTimestamp_ForgetsTheBound`).
* **The in-memory half of a bound is per operator process**, so a restart before the
  annotation ever lands restarts the budget. Deliberate.
* **(Closed 2026-09-26) The manual-failover hand-over does not bound the outer loop.** After Phase 2 consolidates
  and completes, the state clears; a pod-0 that then *exists* but does not match the live
  template — stuck `Terminating`, or otherwise not replaced — keeps `needsRollingUpdate` true,
  so the next pass re-enters at `replaceNextReplica` and requeues with no bound, and the "tail
  of the pass is skipped" consequence returns for that case.
  *(Narrowed 2026-08-25.* The stuck-`Terminating` half of this item is now covered:
  [ADR 0026](0026-a-pod-being-deleted-is-not-available.md) D5 routes every wait on a
  terminating pod — the delete gate and the `!available()` waits alike — through
  `terminationWait` *(all but one: the Sentinel completion hold did not, until
  [ADR 0026](0026-a-pod-being-deleted-is-not-available.md) D11 routed it through `sentinelWait`
  on 2026-09-26)*, which after `podTerminationOverrun` = 2 min stops setting `NeedsRequeue`
  and reports `PodTerminationStalled` instead, so the tail of the pass runs again. The
  *refusal* is still not resumed, deliberately. ~~What remains open here is a pod-0 that does
  not match the template for any **other** reason, which still requeues unbounded.~~ *(Closed
  2026-09-26, see the end of this entry.)*
  ADR 0026 D5 is also the one bounded rolling-update wait that does **not** go through
  `ensureWaitBound` *(one of two since 2026-09-26: D17's availability wait is the second, for
  the same reason — it reads a clock the pod carries)*: it measures the pod's own `metadata.deletionTimestamp`, a timestamp the
  API server writes, so the D7/D8 failure this ADR is about — an arming write that fails
  silently — cannot occur there. The reasoning is recorded in that ADR rather than here.) A pod-0 that was never created is
  **not** that case: `checkAndHandleRollingUpdate` skips absent pods when deciding whether an
  update is needed, so with the state already cleared it returns before any dispatch, the pass
  runs on through `handlePostRollingUpdateChecks` and `updateStatus`, and the short-StatefulSet
  nudge owns the requeue ([ADR 0003](0003-nudge-a-short-of-pods-statefulset.md)). Bounding the
  outer loop is explicitly a different item.
  *(Closed 2026-09-26 by D17 and [ADR 0026](0026-a-pod-being-deleted-is-not-available.md)
  D11.* The unbounded requeue left here was the `!available()` wait on `candidates[0]` in
  `replaceNextReplica`: an outdated pod-0 that is not the master is a replica candidate, and
  the roll waited for it to become available with a plain requeue. An outdated pod that is not
  terminating is now deleted rather than waited for, and every wait left on that path is
  bounded — a terminating pod by ADR 0026 D5, an absent one by D16, an unavailable pod on the
  current template by D17, a replaced replica that answers but does not sync by the sync-wait
  bound (D13). The closure covers the wait this entry named, not every requeue of the rolling
  update: an outdated pod-0 that still counts as the master is not a candidate and reaches the
  failover step, whose unbounded requeues are the next entry.)
* **Unbounded requeues of another class remain.** A master or a promotion step that does not
  answer still ends the pass with a plain requeue and no bound: "No master detected during
  rolling update" (`handleRollingUpdate` and `dispatchMultiReplicaState`), and "No ready
  updated replica available for promotion" and a failing `promoteAndRedirect` in
  `handleManualFailover`. T32's analysis lists further sites of the same class —
  `waitForWriteSync` on a `WAIT` or TLS error, the waits inside `verifyNewMasterReady`, the
  `deleteNextPendingPod` fall-through — which were not re-read for this entry. None is filed.
  D17 bounds the waits on a pod that does not come up, not on a pod that does not answer.
* **The Sentinel failover's reset-and-retrigger cycle has no cap** *(added 2026-09-26, read)*.
  Each step is bounded — `failover-triggered` hands over to `failover-reset` after
  `failoverRetryTimeout` (30 s), `handleFailoverRetrigger` fires again after
  `failoverResetMinWait` (20 s) — but nothing counts the cycles; ~~`maxReconnectResets` caps only
  the no-replica branch~~ *(corrected 2026-09-26, read: `maxReconnectResets` counts only the
  no-replica branch's resets, and caps no branch overall — the pass that reaches it clears the
  count, so a new master that still has no connected replica starts it over)*. Before [ADR 0025](0025-a-split-brain-warning-means-one-that-did-not-resolve-itself.md)
  D9 a Kind run went through ten triggers in about nine and a half minutes and was still cycling
  when the test's ten-minute wait gave up. D9 removed the cause that drove that run; the loop
  itself is unchanged. Not filed.
* **A missing Sentinel pod still waits unbounded.** The Sentinel quorum wait and completion
  hold route through `sentinelWait`, which falls back to the plain requeue when the scan names
  neither a terminating pod nor an unavailable current one — and a pod the StatefulSet
  controller never recreates is skipped by `scanSentinelPods`, so it is named by neither. That
  is D16's class on the Sentinel tier; D17 bounds only a pod that exists.
* **A pod whose readiness flaps restarts D17's clock** on every transition back from Ready, so
  a pod that turns Ready at least once within every `syncTimeout` is never reported. Read from
  `podNotReadySince`, not measured. The first-sync rule has a margin of its own:
  `stampedAtFirstSync` assumes kubelet stamps the Ready condition within `firstSyncSlack` = 5 s
  of `status.startTime`, and a first sync later than that reads as a transition and restarts the
  clock at scheduling — the pre-review behaviour, for that pod only. The margin rests on the
  code's reading of kubelet, not on a measurement; the e2e cannot tell the two clocks apart,
  because an unpullable image does not delay scheduling. That kubelet keeps `lastTransitionTime`
  across a kubelet restart is read from the upstream status manager, not measured on a node
  either.
* **A paused data roll releases the Sentinel roll in the pass that pauses.** The hold of D16 as
  amended keys on the data result's `DeferredRequeueAfter`, and `pauseRollingUpdate` clears the
  state annotation and returns an empty `RollingUpdateResult`, so `reconcileWorkload` passes
  `dataTierHolding` false and `runSentinelRollingUpdate` runs the Sentinel roll in that pass.
  The next pass that finds an outdated data pod dispatches the data roll again on a fresh
  `syncTimeout` and ends on its waits, so the release recurs once per pause rather than holding
  the Sentinel roll open — traced by reading, not measured. A pause is reached only through the
  D13 consumers, and on a Sentinel cluster each of them runs only once the replicas it waits on
  are available — `verifyReplacedReplicasSynced` and `waitForReplicasReady` check that first, and
  the zero-acknowledgement branch of `waitForWriteSync` runs after `waitForReplicasReady`, and
  `verifyPromotionCandidateHoldsData` is on the non-Sentinel path only; a replica that never
  comes up goes to `availabilityWait` before the sync-wait bound is armed, so the bad-image case
  the hold exists for does not take this path. Recorded, not fixed; the pause's shape is T23's
  item.
* **(Closed 2026-09-26 by [ADR 0024](0024-the-sentinel-tier-reports-its-own-completion.md)
  D10) A tier of one or two Sentinels never replaced a Ready outdated Sentinel.** The quorum is
  `replicas/2+1`, which equals `replicas` for one and two, so the guard refused every delete
  that spends a vote even with every Sentinel Ready; `sentinelWait` then found neither a
  terminating pod nor an unavailable current one and returned the plain requeue. The roll
  requeued with no bound and no pod named, and every pass ended on it before `updateStatus`,
  with the phase standing at `Sentinel Rolling Update i/n`. Pre-existing — the guard's
  arithmetic predates T32, which relaxed it only for a non-voting target — and admitted by the
  CRD (`SentinelSpec.Replicas` has `Minimum=1`; its doc comment advises three or more). T31
  makes it reachable at the operator upgrade, because that release rolls every Sentinel tier
  once ([ADR 0032](0032-generated-pods-run-rootless.md)); the wds18 fleet has only
  three-Sentinel tiers (checked read-only 2026-09-26). ~~No decision has been taken, and none is
  recorded here.~~
  *(Decided 2026-09-26: such a tier rolls serially.* `sentinelDeleteKeepsVotes` keeps
  `readyCount-cost >= quorum` wherever `quorum < total`, and where the quorum equals the size it
  requires `readyCount-cost >= total-1` — one Sentinel at a time, and only while every other one
  is available. The delete gate still applies, and a non-voting target still costs nothing. The
  price is automatic failover for the seconds one Sentinel restarts, which is what any single
  Sentinel failure already costs a tier sized to tolerate none; without it, no Sentinel change —
  an image, a certificate rotation ([ADR 0030](0030-rotating-certificates-rotate-the-instances-that-cannot-reload-them.md)),
  the rootless posture — ever reached such a tier. The wait that remains is refused only while
  another Sentinel is not available, and routes like every other Sentinel wait: a terminating pod
  through `terminationWait`, an unavailable current one through `availabilityWait`, a missing one
  to the plain requeue of "A missing Sentinel pod still waits unbounded" above. The rule and its
  reasoning live in ADR 0024 D10.
  Guarded at unit level by `TestSentinelRollingUpdate_SmallTiersRollSerially` and
  `TestSentinelDeleteKeepsVotes`; `TestE2E_RollingUpdate_TwoSentinelsRollSerially` exists and
  ~~has not been run~~ is green locally on both Valkey lines, 2026-09-26, without asserting the
  serial order.)
* **The Sentinel half of D17 has no e2e.** The image is shared with the data tier, whose stall
  holds the Sentinel roll (D16 as amended), and `SentinelSpec` carries no field that could make
  only a Sentinel pod fail to start (`enabled`, `replicas`, `podLabels`, `podAnnotations`,
  `allowUnencrypted`, `disableAuth`). The target selection, the quorum cost and both bounded
  Sentinel waits are verified at unit level only. The hold is the one Sentinel-side behaviour
  the data-tier e2e covers: no Sentinel pod UID changed over its 30 s hold window.
* **No e2e forces a pod-0 that never returns.** The Phase 1 escape is covered end-to-end by
  `TestE2E_RollingUpdate_TopologyRestoreAbandoned`
  ([`test/e2e/topology_abandon_test.go`](../../test/e2e/topology_abandon_test.go)), which
  reaches it through a jammed replication link rather than an absent pod-0 — the state machine
  cannot enter Phase 1 without pod-0 coming back at all. The manual-failover escape (D6) is the
  one verified at unit level only.

## References

* [`internal/controller/rolling_update.go`](../../internal/controller/rolling_update.go) — `handleTopologyRestoration`, `verifyTopologyRestored`, `abandonTopologyRestoration`, `waitOrAbandonManualFailover`, `ensureWaitBound`, `waitBoundExceeded`, `armWaitBound`, `armTopologyRestoreBound`, `armFinalizationBound`, `armManualFailoverBound`, `waitBoundKey`, `forgetWaitBounds`, `finalizationStallTimeout`; D16: `recreationWait`, `clearRecreationWait`, `podRecreationOverrun`; D17: `podNotReadySince`, `stampedAtFirstSync`, `firstSyncSlack`, `availabilityWait`, `unavailablePod`, `reportAvailabilityStall`, `expiredUnavailablePod`, `waitForUnavailablePod`, `standaloneWait`, `firstUnavailableExisting`, `sentinelScan` (`observe`, `deleteTarget`), the quorum guard in `dispatchSentinelRollingUpdate` and `sentinelDeleteKeepsVotes` (the serial roll of ADR 0024 D10), `sentinelWait`, `finishSentinelRollingUpdate`, and the evaluator wrappers `checkAndHandleRollingUpdate` / `checkAndHandleSentinelRollingUpdate`
* [`internal/controller/valkey_controller.go`](../../internal/controller/valkey_controller.go) — `reconcileWorkload`, `handlePostRollingUpdateChecks`, `runSentinelRollingUpdate`, `soonerRequeue` (the data-tier hold of D16 as amended); `pauseRollingUpdate` in `rolling_update.go` is its known exception
* [`internal/controller/condition_registry.go`](../../internal/controller/condition_registry.go) — the `PodAvailabilityStalled` row (level, two evaluators, ownership rule)
* [`api/v1/valkey_types.go`](../../api/v1/valkey_types.go) — `GetSyncTimeout()`, `RollingUpdateSpec.SyncTimeout`, `ConditionTypePodAvailabilityStalled` and its three reasons
* [`internal/controller/pod_availability_test.go`](../../internal/controller/pod_availability_test.go), [`test/e2e/pod_availability_test.go`](../../test/e2e/pod_availability_test.go) — D17's guards
* [ADR 0026](0026-a-pod-being-deleted-is-not-available.md) — D5, the stall shape D16 and D17 apply; D11, the primary home of D17 and of the amendment to D16
* [ADR 0024](0024-the-sentinel-tier-reports-its-own-completion.md) — the tier ordering the D16 amendment restores, except in the pass in which a data roll pauses; D10, the serial roll of a tier of one or two Sentinels, the one decision D17's review left open
* [ADR 0027](0027-conditions-are-levels-edges-or-history.md) — why `PodAvailabilityStalled` is a level with two evaluators
* [ADR 0007](0007-failover-aware-rolling-update.md) — the sequence whose waits these are
* [ADR 0009](0009-an-unrecorded-promotion-is-not-a-promotion.md) — the one wait that is deliberately *not* bounded, and why
* [ADR 0011](0011-evidence-based-steady-state-split-brain-resolution.md) — what runs after the state clears, and why the hand-over target matters
