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
update, found by the T10 wedge: an immutable-field sync error stops the StatefulSet
controller from creating the pod, the pass ended on the wait forever, and with it the status
write, the steady-state split-brain check and the Sentinel roll. `recreationWait` now rides
the D7/D8 `ensureWaitBound` infrastructure (there is no pod object to read a timestamp
from), and past `podRecreationOverrun` = 2 min the pass stops ending on the wait —
`DeferredRequeueAfter` plus the `PodRecreationStalled` condition, the ADR 0026 D5 shape
applied to the absent pod instead of the terminating one. The wait itself is unchanged (the
operator cannot create the pod; only its controller can), the bound is cleared per episode
on the exists path so one roll's sequential waits each own their budget, and no Event is
emitted (ADR 0025 D7). Also 2026-08-27: the `RollingUpdateComplete` Event now states the end
state each of the three completion exits actually reached, and the verify-incomplete exit
emits the completion marker it used to omit — the message drift D3/D5 owned is fixed (T17).

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
by `setFailoverTimestamp`, which returns the `Update` error, and every caller checks it. The
other two (`isSentinelAwarenessStalled`, `isSyncWaitTimedOut`) rest on an arming write whose
error is discarded, are live unbounded stalls and are tracked as defects. Filing a live
unbounded-requeue defect as a readability cleanup means it ships whenever nobody gets round
to the cleanup.

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
* **The manual-failover hand-over does not bound the outer loop.** After Phase 2 consolidates
  and completes, the state clears; a pod-0 that then *exists* but does not match the live
  template — stuck `Terminating`, or otherwise not replaced — keeps `needsRollingUpdate` true,
  so the next pass re-enters at `replaceNextReplica` and requeues with no bound, and the "tail
  of the pass is skipped" consequence returns for that case.
  *(Narrowed 2026-08-25.* The stuck-`Terminating` half of this item is now covered:
  [ADR 0026](0026-a-pod-being-deleted-is-not-available.md) D5 routes every wait on a
  terminating pod — the delete gate and the `!available()` waits alike — through
  `terminationWait`, which after `podTerminationOverrun` = 2 min stops setting `NeedsRequeue`
  and reports `PodTerminationStalled` instead, so the tail of the pass runs again. The
  *refusal* is still not resumed, deliberately. What remains open here is a pod-0 that does not
  match the template for any **other** reason, which still requeues unbounded.
  ADR 0026 D5 is also the one bounded rolling-update wait that does **not** go through
  `ensureWaitBound`: it measures the pod's own `metadata.deletionTimestamp`, a timestamp the
  API server writes, so the D7/D8 failure this ADR is about — an arming write that fails
  silently — cannot occur there. The reasoning is recorded in that ADR rather than here.) A pod-0 that was never created is
  **not** that case: `checkAndHandleRollingUpdate` skips absent pods when deciding whether an
  update is needed, so with the state already cleared it returns before any dispatch, the pass
  runs on through `handlePostRollingUpdateChecks` and `updateStatus`, and the short-StatefulSet
  nudge owns the requeue ([ADR 0003](0003-nudge-a-short-of-pods-statefulset.md)). Bounding the
  outer loop is explicitly a different item.
* **No e2e forces a pod-0 that never returns.** The Phase 1 escape is covered end-to-end by
  `TestE2E_RollingUpdate_TopologyRestoreAbandoned`
  ([`test/e2e/topology_abandon_test.go`](../../test/e2e/topology_abandon_test.go)), which
  reaches it through a jammed replication link rather than an absent pod-0 — the state machine
  cannot enter Phase 1 without pod-0 coming back at all. The manual-failover escape (D6) is the
  one verified at unit level only.

## References

* [`internal/controller/rolling_update.go`](../../internal/controller/rolling_update.go) — `handleTopologyRestoration`, `verifyTopologyRestored`, `abandonTopologyRestoration`, `waitOrAbandonManualFailover`, `ensureWaitBound`, `waitBoundExceeded`, `armWaitBound`, `armTopologyRestoreBound`, `armFinalizationBound`, `armManualFailoverBound`, `waitBoundKey`, `forgetWaitBounds`, `finalizationStallTimeout`
* [`api/v1/valkey_types.go`](../../api/v1/valkey_types.go) — `GetSyncTimeout()`
* [ADR 0007](0007-failover-aware-rolling-update.md) — the sequence whose waits these are
* [ADR 0009](0009-an-unrecorded-promotion-is-not-a-promotion.md) — the one wait that is deliberately *not* bounded, and why
* [ADR 0011](0011-evidence-based-steady-state-split-brain-resolution.md) — what runs after the state clears, and why the hand-over target matters
