# ADR 0026: A pod being deleted is not available

## Status

Accepted. Date: 2026-08-25.

Amended 2026-08-27: **adopting a pod as the master authority is a site that spends it, and
it was missing from the rule.** Measured in CI (single-node-valkey9): a chaos delete took
the recorded master in the same second the roll deleted the outgoing one, the dying pod's
drain stamped the peer that was itself already terminating, both split-brain resolvers
would adopt the stamp — the pod answers `role:master` for its whole termination, which is
this ADR's own founding measurement — and the fleet consolidated toward a pod that returned
on an empty volume: every pod ended at `dbsize=0`. The fix is the rule, applied: the roll
resolver selects its authority (stamp, named authority, connected-slaves tie-break) only
among non-terminating masters and refuses when none is left; the steady-state evidence scan
skips a terminating stamped pod; and `confirmedMasterAuthority` refuses a recorded master
whose Pod object provably carries a `DeletionTimestamp` — positive evidence only, an
unreadable Pod object refuses nothing. Demotion of a terminating master stays allowed: that
is the `reachable()` carve-out, and it is the *direction* that was wrong, not the contact.
This closes the measured half of T13.

Second amendment the same day, out of the same CI window: **the delete gate's last look
bypasses the cache.** The gate is check-then-act over pod states collected at the top of the
pass, and the cache those states come from is allowed to lag — a chaos delete landing in
between left the gate looking at a quiet tier while a pod was already terminating, measured
as two data pods terminating at once. `holdDeleteWhileTerminating` now re-reads the tier
through an uncached `APIReader` immediately before permitting a delete (a handful of GETs
per pod replacement, not per pass). A live look that cannot be performed changes nothing:
the cached answer was already "quiet", and a gate must not manufacture a hold out of
ignorance. The window is narrowed to the single read-to-delete gap, not closed — nothing
short of an API-server-side transaction could close it. The residue fired on the very next
run: both writers deleted in the same second, each after a read that truthfully showed a
quiet tier. That is the accepted physics of two uncoordinated writers, and the e2e sampler's
attribution now carries it — an overlap containing the test's own victim whose other pod
began terminating within `simultaneousDeleteWindow` (3 s) of the injection is the test's,
while a delete past that window is a genuine violation, because the operator's live
pre-delete read had the injected termination in front of it and a next-pass delete is at
least a requeue interval away.

Amended 2026-09-26 (T32): **D11 is new — an outdated pod is replaced, not waited for, and every
remaining wait on a pod that exists but never becomes available is a bounded observation on the
pod's own clock.** Found while analysing T31, the first change that rolls every cluster of a
fleet at the operator upgrade: a replacement that could not start — an unpullable image, a crash
at boot, a request no node can schedule — held the roll of its tier with no bound and no
condition naming it, froze the status surface, and after a spec fix never moved on by itself,
because every delete site asked `available()` and the stuck pod was exactly the one the roll
replaces next. The Sentinel roll had the same class in its quorum wait and its completion hold.
D1 is reworded for the three delete sites of an outdated pod; D5 loses the Sentinel roll from
what the stall shape buys back, because a holding data tier now holds it, for all three stall
conditions; D5's statement that the standalone gate goes through `standaloneWait` and its claim
to be the only bounded wait outside `ensureWaitBound` are superseded; D6 and D8 are extended for
the Sentinel tier's target selection and quorum cost. Every superseded sentence is struck
through in place. [ADR 0010](0010-every-rolling-update-wait-is-bounded.md) D17 states the same
bound from the bounded-wait side, and [ADR 0024](0024-the-sentinel-tier-reports-its-own-completion.md)
records the bounded completion hold. D11 was revised the same day, before it was committed, after
an adversarial review of the implementation, and is stated as it landed: the Sentinel quorum
guard applies only to a delete that spends a vote (D8), `PodAvailabilityStalled` is retracted on
evidence only, a `Ready=False` stamped at kubelet's first status sync is not a transition, the
Sentinel scan names the longest-unavailable current pod (D6), and the Sentinel-after-data ordering
has one known exception, a paused data roll.

Implemented: the `available()` / `reachable()` split on `podState` with the per-site answers of
D1–D4, the delete gate of D5, the tier definition of D7, the bounded stall observation and the
`PodTerminationStalled` condition of D5, and the Sentinel counters of D6. Unit coverage in
[`internal/controller/pod_termination_test.go`](../../internal/controller/pod_termination_test.go),
field coverage in [`test/e2e/pod_termination_test.go`](../../test/e2e/pod_termination_test.go).

Implemented for D11 on branch `feat/rootless`, not yet released: the delete rule at the three
sites, `availabilityWait` on `podState.notReadySince`, the Sentinel target selection and quorum
cost with `sentinelWait`, the data-tier hold of the Sentinel roll, and the
`PodAvailabilityStalled` level with its two evaluators and its registry row. Unit coverage in
[`internal/controller/pod_availability_test.go`](../../internal/controller/pod_availability_test.go):
the clock, including a `Ready=False` stamped at the first status sync
(`TestPodNotReadySince_FirstSyncIsNotATransition`), `availabilityWait` inside and past its
budget, the waits on current pods, both evaluators and their ownership rule in both directions,
the retraction on evidence only (`TestReportAvailabilityStall_RetractsOnlyOnEvidence`,
`TestSentinelRollingUpdate_TerminationPriorityDoesNotRetractTheReport`), the Sentinel target
selection, the replacement of a non-voting Sentinel with the quorum already lost
(`TestSentinelRollingUpdate_ReplacesANonVotingPodWhenQuorumIsAlreadyLost`), the
longest-unavailable Sentinel (`TestSentinelRollingUpdate_ReportsTheLongestUnavailablePod`), quorum
wait and completion hold, the Sentinel `DeferredRequeueAfter` reaching the pass result, the
data-tier hold and the class exit. The tests that asserted superseded behaviour were rewritten to
the new rule rather than deleted — the wait on an outdated pod in
[`sentinel_failover_test.go`](../../internal/controller/sentinel_failover_test.go) and
[`rolling_update_test.go`](../../internal/controller/rolling_update_test.go), the Sentinel roll
released by a data stall in
[`pod_termination_test.go`](../../internal/controller/pod_termination_test.go) — and the two
Sentinel quorum-wait tests kept their assertion and moved their unready pod onto the current
spec ([ADR 0017](0017-test-and-ci-policy.md) D18). Verified 2026-09-26: `make test-unit`,
`make test-integration`, `make lint` and `make cyclo` green, and the mutation checks of the T32
guards, run together with T31's ([ADR 0032](0032-generated-pods-run-rootless.md)), 36 in all,
were every one killed. Field coverage for the data tier is
`TestE2E_RollingUpdate_UnavailableReplacementIsReportedAndReplaced` in
[`test/e2e/pod_availability_test.go`](../../test/e2e/pod_availability_test.go). It passed on
2026-09-26 on Kind (control plane and three workers, Kubernetes v1.36.1) against valkey 9.1.1:
`PodAvailabilityStalled=True/ValkeyPodNotAvailable` named the stuck replica about 64 s after the
image change with `syncTimeout` 60 s, the Sentinel pod UIDs stayed unchanged over the 30 s hold
window, and after the image was put back the operator replaced the stuck pod itself — phase `OK`,
condition `False/PodAvailable`, 100 keys on every replica. It then passed inside both full local
suites on that cluster, `make test-e2e E2E_VALKEY_LINE=9` (51/51, 588 s) and
`make test-e2e E2E_VALKEY_LINE=8` (51/51, 536 s), and was re-run green on the final image against
valkey 9.1.1. All of these ran locally; the branch has not been through the pipeline, so no CI leg
has run it yet. The Sentinel-tier half is unit-only by design: the image is shared with the data tier, whose stall
now holds the Sentinel roll, and `SentinelSpec` has exactly `enabled`,
`replicas`, `podLabels`, `podAnnotations`, `allowUnencrypted` and `disableAuth` — no field that
could stop a Sentinel pod from starting on its own.

Open, and named as such: the steady-state master authority reads no `DeletionTimestamp` at all
(D10 and *Residual risks*); this ADR binds the rolling update only. For D11: a Sentinel pod that
is missing rather than unavailable still waits unbounded; a single pod that never starts is not
reported; a paused data roll releases the Sentinel roll in the pass that pauses; and a tier of
one or two Sentinels can never replace a Ready outdated Sentinel — pre-existing, reached by T31's
fleet-wide Sentinel roll, and **not decided** (all four in *Residual risks*).

## Context

`isPodReady` ([`rolling_update.go`](../../internal/controller/rolling_update.go)) reads the
`PodReady` condition and nothing else, and until this change no caller paired it with
`DeletionTimestamp`. That was believed to be a window of a second or two.

**Measured 2026-08-24 on kind, Kubernetes 1.36.1.** Two pods built in the shape of this
operator's workloads were deleted, then polled once a second for `deletionTimestamp` next to the
`PodReady` condition:

| Shape | grace | Result |
|---|---|---|
| `valkey:9.1.1`, exec readiness probe (`valkey-cli ping`), `preStop: sleep 60` | 75 s | `deletionTimestamp` set at 0 s; `Ready=True` for all 61 s until the object disappeared |
| `valkey:9.1.1`, exec readiness probe, `trap '' TERM` | 30 s | `deletionTimestamp` set at 0 s; `Ready=True` for all 30 s until the object disappeared |

**kubelet does not flip `PodReady` for a terminating pod.** While the readiness probe keeps
passing, the pod is Ready right up to the moment it is gone — which is why the Kubernetes
endpoints controller carries its own `DeletionTimestamp` check instead of relying on readiness.
The window is the whole termination, however long that is.

How long that is, in this operator: a replica delete releases the drain `preStop` hook in about
a second on every topology, because the hook is a wait loop with a 60 s **cap**
([`statefulset.go`](../../internal/builder/statefulset.go)) whose marker a `defer` releases on
every exit path of `Handle`, and `Handle` returns immediately for a non-master
([`drain.go`](../../internal/sidecar/drain.go)). 60 s is approachable only by a master whose
drain failover is still running, and the full grace period (75 s data, 30 s Sentinel) by a pod
that is slow or wedged on shutdown.

Three decisions of the rolling update read the stale Ready and spent a pod on it:

* **The promotion.** `findPromotionCandidate` accepted any Ready, updated pod. On the
  non-Sentinel manual-failover path the promoted pod takes `REPLICAOF NO ONE`, is recorded as
  `known-master`, and the outgoing master is deleted seconds later.
  `verifyPromotionCandidateHoldsData` passes, because a dying pod does hold the data right up to
  the moment it stops. Without persistence the dataset is then gone. The Sentinel path had the
  mirror-image hole in `verifyNewMasterReady`, the gate before the old master's delete.
* **The Sentinel quorum guard.** `readyCount` counted a terminating Sentinel, so with three
  Sentinels and quorum two, `readyCount-1 = 2 >= 2` authorised deleting a second one — one live
  Sentinel of three and no quorum for a failover for the union of both termination windows.
  [ADR 0004](0004-opt-in-poddisruptionbudgets.md) derives the Sentinel PDB from exactly this
  quorum, and [ADR 0022](0022-sentinel-identity-is-pinned-to-the-pod.md) measured what a Sentinel
  tier that cannot reach a majority costs: no promotion at all.
* **The redundancy gate of the data tier.** `verifyReplacedReplicasSynced` exists to stop the
  operator deleting the next candidate while a replaced one is still catching up — its own
  comment says so — and a replaced replica being deleted for an unrelated reason (chaos,
  eviction, node drain) passed it: `Ready=True`, `master_link_status:up`, until the process
  stops. Two replicas down at once, which is the invariant
  [ADR 0007](0007-failover-aware-rolling-update.md) D1 exists for.

The visible symptom that started this was none of those. It was a duplicate log line: nearly
every CR logged "Deleting replica pod X" twice in the same second under two reconcileIDs,
because pass A's delete triggers a watch event that requeues instantly and pass B sees the pod
still present, still Ready, still on the old template. That repeat delete is a verified API
no-op — `rest.BeforeDelete` takes the `DeletionTimestamp != nil` branch and, with no
`GracePeriodSeconds`, returns "graceful deletion is pending, do nothing" — so the cosmetic
finding was carrying three safety findings.

The obvious one-line fix — folding `DeletionTimestamp` into readiness itself — must not be made,
and not for cosmetic reasons. `demoteRogueMaster` refuses a not-Ready pod. The terminating
outgoing master still answers `INFO` as master and is therefore still `isMaster`, deliberately
([ADR 0025](0025-a-split-brain-warning-means-one-that-did-not-resolve-itself.md): the guard
drops the heuristic, never the answer). Under a blanket change the demotion would be refused and
the outgoing master would keep accepting writes as a master for the rest of its termination —
up to the 60 s cap — with no write fencing on either side. Trading a `REPLICAOF` that works
today for a divergence window is the wrong direction.

## Decision

**D1 — `available()` is the default answer, and it is a rename rather than a list.**
`podState.ready` is now `readyCondition`, and two accessors decide what a site is asking:

```go
func (ps podState) available() bool { return ps.readyCondition && !ps.terminating }
func (ps podState) reachable() bool { return ps.readyCondition }
```

`terminating` is set in `collectPodStates` from `pod.DeletionTimestamp != nil`. ~~Every site that
**spends** a pod — deletes it, promotes it, counts it toward a quorum or a completion — calls
`available()`.~~ *(superseded 2026-09-26 for the delete of an outdated pod, see D11)* Every site
that **spends** a pod — promotes it, counts it toward a quorum or a completion, or deletes it for
any reason other than replacing an outdated pod — calls `available()`. Replacing an outdated pod
asks only whether that pod is being deleted: the standalone delete in
`handleStandaloneRollingUpdate`, `replaceNextReplica`, `replaceRemainingPods`, and on the
Sentinel tier the target selection of D8 (D11). The rename is the point: it turned all thirteen
readers into compile errors, so each was decided once and recorded, and new code that reaches for
the obvious name gets the safe answer. Stating the rule as an enumeration of sites had been wrong
three times before this; thirteen readers to one exception makes the enumeration an enumeration of
the wrong half.

**D2 — `reachable()` is the carve-out, and it is about the question, not about a file.** A
terminating pod that still answers `INFO` still holds writes, so the commands that repair the
topology must still be sent to it. Four sites ask this question:
`demoteRogueMaster`, the `numReplicas` count of `waitForWriteSync`, `forceReplicaConnections`,
and the redirect loop of `promoteAndRedirect`. `demoteRogueMaster` is the load-bearing one —
refusing it is the divergence window described in *Context*. The `podState` built in
[`steady_state_master.go`](../../internal/controller/steady_state_master.go) also asks only this
question, but it fills `terminating` anyway, so `available()` is never structurally false at a
construction site.

**D3 — excluding a pod must tighten a gate, never relax one.** `waitForWriteSync` counts
terminating replicas because excluding them lowers `numReplicas`, and at zero the function skips
`WAIT` entirely — the exclusion would remove the gate in front of a promotion instead of
sharpening it.

**D4 — `countUpdatedPods` keeps counting terminating pods; the completion hold lives in
`finalizeRollingUpdate`, Sentinel path only.** Its result is the dispatch predicate
`updatedCount == totalPods`, and on the Sentinel path `handleRollingUpdate` has no state switch
under that branch. Excluding a terminating pod there does not delay the completion — it drops
the pass back into the post-failover state machine with a failover timestamp that is minutes
old, straight into the timed-out branch, which either resets Sentinel through a dying master
(the unrecoverable direction per ADR 0022) or triggers a real `SENTINEL FAILOVER` on a
fully-updated healthy cluster. The hold therefore sits inside `finalizeRollingUpdate`, before
`checkFinalizationTopology` so that Sentinel is never pointed at a dying master, and bounded by
`finalizationStallTimeout` = 2 min. It is **not** applied on the non-Sentinel path:
`finalizeMultiReplicaRollingUpdate` has no bound of its own, so holding there would create the
unbounded class D5 exists to close. Accepted there: `RollingUpdateComplete` can fire seconds
early over a pod that is terminating, which is what happens today.

**D5 — the operator never deletes a pod of a tier while any pod of that tier is terminating; the
refusal is never resumed, the observation of it is bounded.**

The gate sits immediately in front of each `deleteOwnedPod`, never at a function head. A head
gate would discard the work those functions do above their delete: `replaceNextReplica` clears
the sync-wait bound through `verifyReplacedReplicasSynced` and returns the `nil` both dispatchers
read as "advance"; `replaceRemainingPods` is tail-called by `handleMasterWithNoReplicas` after it
has cleared the reconnect counter. The gated deletes are `replaceNextReplica`,
`replaceRemainingPods`, `deleteNextPendingPod` and the Sentinel delete (after its quorum guard).
~~The standalone handler is a single-pod tier where the gate and the boot wait are the same
branch, and both go through `standaloneWait`.~~ *(superseded 2026-09-26, see D11)* The
standalone handler is a single-pod tier: its gate is the pod's own `DeletionTimestamp`, checked
in front of the delete and routed to `terminationWait`, and `standaloneWait` is left with the
wait on a current pod, which D11 shows is reachable only on a tier larger than one pod.

Every wait on a terminating pod — the gate and the `!available()` waits alike — goes through
`terminationWait`, which reports it in one of two shapes:

* inside the budget: a requeue that ends the pass. This is the normal case, and it costs a clean
  roll nothing: it replaces one no-op re-delete with one no-op wait at the same cadence, one API
  call cheaper, for the second a replica actually takes.
* past it: `DeferredRequeueAfter` plus the `PodTerminationStalled` condition (reason
  `PodStuckTerminating`, message naming the pod). **The delete is still refused** — deleting a
  second pod because the first is wedged is the failure the gate exists to prevent, and stopping
  half-done keeps a serviceable cluster (ADR 0007). What expiry buys back is the tail of the
  reconcile pass, which a requeue return skips: ~~the Sentinel roll,~~ *(superseded
  2026-09-26: a holding data tier holds the Sentinel roll, see D11)* `checkAndRecoverNoMaster`,
  `checkSteadyStateSplitBrain` — per [ADR 0011](0011-evidence-based-steady-state-split-brain-resolution.md)
  D1 the only thing that re-detects a split brain outside a rolling update — and the status
  write. On a NotReady node the `DeletionTimestamp` never clears, so that blackout would
  otherwise be permanent. The rolling-update state annotation stays, nothing is deleted, and the
  roll resumes on its own once the pod is gone, because the StatefulSet watch fires.

*(Corrected 2026-09-26.)* "Every wait" was not true of the Sentinel completion hold, which
waited on a terminating Sentinel with a plain requeue until D11 routed it through `sentinelWait`
and hence `terminationWait`. It holds now.

The condition clears from the delete gate, which every delete site calls on every pass — and
also from `clearRollingUpdateState`, because there is exactly one shape with no later gate: a
stall on the **last** pod the roll replaces, where the pod returns, everything is current, and
the pass finalizes without passing a gate again. Without that second clear the condition would
stand forever on a healthy cluster. *Extended 2026-09-26 (D11):* the Sentinel tier has the same
shape on its completion hold, which passes no delete gate at all, so it clears in two more
places — `sentinelWait`, whenever the tier has nothing terminating, and the completion in
`finishSentinelRollingUpdate`.

The budget is `podTerminationOverrun` = 2 min, measured from the pod's own
`metadata.deletionTimestamp`. **That field is not the instant of the delete**: the API server
sets it to `now + gracePeriodSeconds`
([`k8s.io/apiserver@v0.35.0`, `pkg/registry/rest/delete.go:162`](https://github.com/kubernetes/apiserver)),
i.e. to the moment the graceful deletion is due. `time.Since` of it is therefore the *overrun*,
zero at the deadline, and correct per tier without this code knowing any grace period — the 75 s
of a data pod and the 30 s of a Sentinel pod are already inside the zero point.

This deliberately does **not** go through `ensureWaitBound` like the five waits around it.
[ADR 0010](0010-every-rolling-update-wait-is-bounded.md) D7/D8 requires that discipline because
those waits measure something the operator itself started and have no timestamp but the one they
write — a write that can fail indefinitely, leaving the bound unarmed and the phase requeueing
forever. This wait measures an API object with a server-written start time: nothing is armed, so
nothing can fail to arm, it survives an operator restart with no annotation, it costs no CR
write, and the deadline is per pod instead of first-seen-wins per CR. ~~It is the one bounded
rolling-update wait that is not an `ensureWaitBound` member, and this paragraph is why.~~
*(superseded 2026-09-26: D11's `availabilityWait` is the second, reading the pod's own not-Ready
clock for the same reason)* The two are the bounded rolling-update waits that are not
`ensureWaitBound` members, and this paragraph is why.

**No Event on any of it.** ADR 0025 D7 promises a clean rolling update emits zero Warning
Events, `requireNoWarningEvents` fails on any Warning of any reason regarding the CR, and two
e2e subtests assert it. The stall reports through the log, the phase string and the condition.

**D6 — one predicate feeds both Sentinel counters, and it is availability.** The per-pod scan of
`checkAndHandleSentinelRollingUpdate` is extracted into `scanSentinelPods`, returning
`{readyCount, updatedReadyCount, firstOutdatedPod, terminating}`. A terminating Sentinel counts
for neither counter: not for `readyCount`, so it cannot hold up a quorum it is about to leave;
and not for `updatedReadyCount`, so the `SentinelUpdatePending` flip and the
`SentinelUpdateComplete` Event no longer fire while a Sentinel pod is on its way out. The second
half changes a marker [ADR 0024](0024-the-sentinel-tier-reports-its-own-completion.md) made
load-bearing for external sequencing — the price is that the completion marker now also waits out
a Sentinel termination unrelated to the roll, which is stated in ADR 0024 as well.

`firstOutdatedPod` keeps selecting a terminating outdated pod. The gate waits on it rather than
skipping it, mirroring the data tier's rule that `sortReplicaCandidates` is termination-blind and
the terminating pod stays `candidates[0]`. Skipping it would hand position 0 to the next live
pod and delete that instead, which is the regression this whole ADR is about, one layer up.

*Extended 2026-09-26 (D11).* The scan is called from `dispatchSentinelRollingUpdate`, the body
behind the `checkAndHandleSentinelRollingUpdate` evaluator, and carries two more fields:
`unavailableOutdatedPod`, the first outdated pod that is neither available nor terminating, and
`unavailableCurrent`, the pod on the desired spec in the same state with the **oldest** not-Ready
clock — not the lowest ordinal, so a pod that just came back on the same broken spec with a fresh
budget cannot hide one that has been down for hours
(`TestSentinelRollingUpdate_ReportsTheLongestUnavailablePod`). `sentinelScan.observe` puts every
existing pod into exactly one of four arms — terminating, Ready, outdated and unavailable,
current and unavailable — so the predicate behind both counters is unchanged.
`unavailableOutdatedPod` never holds a terminating pod, so the rule of the previous paragraph
stands: a terminating outdated pod is still waited on, because the
gate refuses every delete of the tier while it terminates, whichever pod is the target, and the
pod D8 now takes ahead of `firstOutdatedPod` is by construction not a live one. The data tier's
counterpart is `podState.notReadySince`, filled from `podNotReadySince` by `collectPodStates`
and by `standaloneWait`, and read as a field for the reason `terminating` is: fixtures build
`podState`s with `pod == nil` (*Residual risks*).

**D7 — "the tier" is the ordinal range `[0, *sts.Spec.Replicas)`, never a label-selector List.**
`collectPodStates` and `scanSentinelPods` both walk that range already. A selector would also
return the surplus ordinals a concurrent scale-down is draining, so a 5-to-3 scale-down applied
together with an image bump would hold every delete for the whole drain of pods the roll never
touched.

**D8 — the Sentinel invariant is the quorum guard, not "one at a time".** A Sentinel pod that is
already **gone** (NotFound, not terminating) is skipped by the scan, which lowers `readyCount`
and advances `firstOutdatedPod`; at five Sentinels with quorum three the arithmetic then permits
deleting the next one while the previous replacement is still booting, and the gate cannot see it
because the `DeletionTimestamp` is gone by then. That is accepted: it is the same quorum ADR 0004
derives the Sentinel PDB from. The doc comment that claimed "one at a time" is corrected in the
same change. For the three-replica tiers the fleet runs there is no observable difference.

*Extended 2026-09-26 (D11).* The guard charges the target a vote only when the target holds
one, and applies only to a delete that spends one. `sentinelScan.deleteTarget` takes an outdated
pod that is neither available nor terminating ahead of `firstOutdatedPod`, and returns a cost of
1 only for a target that is Ready and not terminating; the guard in
`dispatchSentinelRollingUpdate` reads `cost > 0 && readyCount - cost < quorum`. That states the
invariant exactly — no delete takes the available count below `quorum`, and a delete that costs
nothing does not lower it at all. The old guard took `firstOutdatedPod` and charged it
`readyCount - 1` whether it held a vote or not: after a spec fix on a three-Sentinel tier the
stuck pod is outdated and costs no vote, but it was selected only when it had the lowest ordinal
and was charged a vote it did not have even then — `2 - 1` is below quorum either way, so the
roll held until a human deleted the pod. Charging the right cost is not enough once the quorum
is already lost: with two of three Sentinels stuck on the broken spec `readyCount` is 1, and a
guard that also judged free deletes refused this one forever (`1 - 0 < 2`), although replacing a
non-voting pod is the only way the quorum comes back
(`TestSentinelRollingUpdate_ReplacesANonVotingPodWhenQuorumIsAlreadyLost`). The D5 delete gate
still serialises those deletes: while one Sentinel terminates, no other is deleted, whatever it
costs. A zero-cost delete never lowers the available count, so it does not widen the
five-Sentinel hole above: the next pass charges the next healthy target in full, exactly as the
old arithmetic charged it while the unavailable pod was still there, and the booting replacement
is `unavailableCurrent`, never a target. What the gate does not serialise is the boot: once a
deleted non-voting pod is gone, the next non-voting outdated pod can be deleted while the first
replacement is still starting. That spends nothing, since neither holds a vote. Traced by
reading. On a tier of one or two Sentinels `quorum` equals `replicas`, so the guard refuses every
delete that spends a vote — an open item, not a decision (*Residual risks*).

**D9 — the manual-failover old-master delete is exempt, and the exemption is written down.** The
pod deleted at the end of `handleManualFailover` is `pods[masterIdx]`, the master the function
has just failed over from, so the exemption condition — the pod being deleted is itself the
master — holds structurally at that site. The promotion has already happened, so the two-down
risk was accepted a few lines earlier; and if the best-effort demotion inside `promoteAndRedirect`
failed, holding the delete would extend a genuine two-master state toward `splitBrainWarnAfter`
= 90 s, the edge ADR 0025's Warning is defined on. Verified while writing the test: on that path
`waitForReplicasReady` runs first and refuses every *other* terminating pod of the tier, so the
gate would only ever have fired on the master anyway. The exemption is insurance, not a hot path.

**D10 — this ADR binds the rolling update. The steady-state master authority is out of scope and
is a separate open item.** `steady_state_master.go` reads no `DeletionTimestamp`:
`listMasterLabeledPods` filters on label and ownership only, and an ungracefully killed master
keeps its `instanceRole=master` label. `checkAndRecoverNoMaster` / `probeForAnyMaster` can send
`REPLICAOF NO ONE` to a dying pod. Whether that is a defect depends on persistence and on what
the pod comes back holding — an analysis this ADR does not contain.

**D11 — An outdated pod is replaced, not waited for; every remaining wait on a pod that exists,
is not being deleted and is not available is a bounded observation on the pod's own clock.**
Added 2026-09-26 (T32).

`waitForUnavailablePod` split a pod that is not available into a terminating one, bounded by D5,
and everything else, which got a log line and a plain requeue: no bound, no condition, no pod
named. Seven data-tier sites waited that way — the standalone loop before and after its delete,
`replaceNextReplica`, `verifyReplacedReplicasSynced`, `waitForReplicasReady`, and the loop and
the fall-through of `replaceRemainingPods` — and the Sentinel roll had the same class in its
quorum wait and its completion hold. For an outdated pod the wait was not only unbounded but
self-defeating: after a spec fix the replacement that never came up on the broken spec is
outdated and the youngest, so it is `candidates[0]`, and only a human `kubectl delete pod` moved
the roll on. The wait's one recorded reason (`91ca86d`, the first rolling update: "was recently
replaced") applies to no outdated pod, because a replaced pod matches the template by
construction.

**Replacement.** Deleting an outdated pod asks only whether it is being deleted — then
`terminationWait` — and whether any pod of its tier is (the D5 gate). Readiness is no longer
asked at the three delete sites: the standalone delete in `handleStandaloneRollingUpdate`,
`replaceNextReplica` and `replaceRemainingPods`. The delete spends nothing the roll was not about
to spend: replica candidates are never masters (`sortReplicaCandidates` filters `isMaster`),
`replaceRemainingPods` deletes the former master only behind `verifyNewMasterReady`, which asks
for a current, available master with replicas attached and no sync in progress — it reads that
master's `DBSIZE` but does not refuse on it, a pre-existing gap D11 does not close (*Residual
risks*) — the PVC survives a pod delete, a replica re-syncs from its master, and a single pod
without persistence loses nothing the same roll would not take from it the moment it turned
Ready. It is the policy of the upstream StatefulSet update loop for a `Parallel` StatefulSet,
which both of this operator's are (`k8s.io/kubernetes@v1.36.4`,
`pkg/controller/statefulset/stateful_set_control.go`, read: an outdated pod that is not
terminating is deleted without an availability check, and availability is waited for only on a
pod that is updated or already terminating). Upstream is not uniform about it: under the default
`OrderedReady` policy `processReplica` stops at the first pod that is not Running and Ready
before the update loop is reached, and the `MaxUnavailableStatefulSet` path — beta and off by
default in v1.36 — refuses the delete while `maxUnavailable` pods are already unavailable.
**Unchanged:** promotion, quorum and completion count `available()`; `deleteNextPendingPod`,
which deletes a leftover outdated second master, keeps `available()`; a pod on the current
template is never deleted. `replaceNextReplica`'s Normal `RollingUpdate` Event adds "the pod was
not available" when it was not, and all three sites log it.

**Bounded observation.** A pod on the current template — or a master — that exists, is not
being deleted and is not available is still waited for, because deleting it brings it back
identical; only the observation is bounded. `availabilityWait` is the D5 shape applied to the pod
that does not come up instead of the pod that does not go away. Its budget is
`spec.rollingUpdate.syncTimeout` (default 5 min), which [ADR 0010](0010-every-rolling-update-wait-is-bounded.md)
D6 already spends on the same wait — the `!isPodReady` branch on pod-0 after a manual failover —
and the D13 gates spend on the same pod once it is available but not replicating, so a user who
raised it for a slow full sync raised this one with it. Its clock is the pod's own,
`podState.notReadySince` from `podNotReadySince`: zero while the pod is Ready; the Ready
condition's `lastTransitionTime` while the condition is not True and that time marks a
transition away from Ready; and the `creationTimestamp` while the pod has not been Ready since it
was created — no Ready condition yet, a zero time, or a `Ready=False` that kubelet stamped at its
first status sync of the pod. `stampedAtFirstSync` recognises the last case as a
`lastTransitionTime` no later than `status.startTime` plus `firstSyncSlack` (5 s), because
kubelet's first sync stamps both in one `updateStatusInternal` call. Without it a pod that sat
Pending for longer than the budget — a node still being provisioned — had its clock reset to the
moment it was scheduled, and a standing report was retracted for a pod that was never available
(`TestPodNotReadySince_FirstSyncIsNotATransition`). It is a clock nothing has to arm, not one
nothing can move — a real flip away from Ready restarts it, as it should: after the first sync
kubelet moves that time only when the condition's status changes, and after a kubelet restart
takes the previous status from the API object (`k8s.io/kubernetes@v1.36.4`,
`pkg/kubelet/status/status_manager.go`, `updateStatusInternal` and `updateLastTransitionTime`,
read). Inside the budget, or on a zero clock, the result is the plain requeue these sites always
returned; past it, `DeferredRequeueAfter` with the pod named in the unexported
`RollingUpdateResult.availabilityStall`. The wait sites are the non-terminating branch of
`waitForUnavailablePod` and everything that reaches it: `standaloneWait` on a current pod;
`verifyReplacedReplicasSynced` (which returns before the sync-wait bound is armed, so that bound
cannot cover this pod); `waitForReplicasReady`; and the `replaceRemainingPods` fall-through,
which used to requeue with no pod named and now waits on `firstUnavailableExisting`.
`standaloneWait` is reachable only while a refused StatefulSet write leaves the live tier larger
than `spec.replicas: 1` (`TestHandleStandaloneRollingUpdate_BoundsTheWaitOnACurrentPod`): the
standalone handler records no rolling-update state, so a single current pod takes the "no
rolling update needed" return of `dispatchDataRollingUpdate` before any wait (*Residual risks*).
`waitForReplicasReady` is split: `!available()` goes to this wait,
while an available but outdated pod — a second master `replaceNextReplica` does not take —
keeps the plain requeue. A stall returns through its caller like any other result, so
`replaceNextReplica` still does not delete the next replica. No annotation, no in-memory
tracker, no CR write: per pod and restart-proof, the D5 argument unchanged.

**The Sentinel tier is in scope.** D8, extended, makes its recovery after a spec fix possible
through target selection and quorum cost, and one routing helper bounds its waits. The two Sentinel
waits that are not the delete gate — the quorum wait and the completion hold in
`finishSentinelRollingUpdate` — share `sentinelWait`: a terminating pod goes to
`terminationWait`, a current pod that exists and is not available — the one with the oldest
clock (D6) — goes to `availabilityWait`, since it is what a quorum wait is really waiting for
while the target it refuses is a healthy outdated pod; anything else gets the plain requeue.
`runSentinelRollingUpdate` applies the Sentinel result's `DeferredRequeueAfter`, merged with the
steady-state split-brain check's pending result by `soonerRequeue` (the sooner wins), where
`handlePostRollingUpdateChecks` used to drop it and a stalled Sentinel wait scheduled no recheck
of its own.

**A holding data tier holds the Sentinel roll, for all three stall conditions.**
`reconcileWorkload` passes `rollingResult.DeferredRequeueAfter > 0` to
`handlePostRollingUpdateChecks`, and `runSentinelRollingUpdate` skips the Sentinel roll for that
pass — whether the data tier holds on a pod that does not go away (`PodTerminationStalled`), one
that is not recreated (`PodRecreationStalled`, ADR 0010 D16) or one that does not come up
(`PodAvailabilityStalled`). The tiers share `spec.image`: a Sentinel roll released by a data
stall deletes a healthy Sentinel, which returns on the spec the data tier is stuck on and sticks
too, and the quorum guard stops at 2/3 with the spare vote spent — one more Sentinel loss then
leaves no automatic failover. The release was dormant while the only two stalls were rare and
environmental; bounding the availability wait would have made it the common case, on every bad
image. [ADR 0024](0024-the-sentinel-tier-reports-its-own-completion.md) D1 — the Sentinel tier
rolls after the data tier — holds again, with one known exception: `pauseRollingUpdate` returns
an empty result, neither a requeue nor a `DeferredRequeueAfter`, so the pass that pauses the data
roll is not holding and runs the Sentinel roll (*Residual risks*). What the stall shape buys
back is the status write, `checkAndRecoverNoMaster` and `checkSteadyStateSplitBrain`; the latter
two are gated on `IsMultiReplicaWithoutSentinel` — the first at its call site in
`handlePostRollingUpdateChecks`, the second inside itself — so on a Sentinel cluster it is the
status write alone.

**Reporting: `PodAvailabilityStalled`, a level with two evaluators and no Event.** True reasons
`ValkeyPodNotAvailable` and `SentinelPodNotAvailable` — the tier is in the reason because two
evaluators write the condition — and the False reason `PodAvailable`, written only over a
standing True of the same tier and never onto a CR that did not carry it. `availabilityWait`
writes nothing; the evaluators are `checkAndHandleRollingUpdate`, now a thin wrapper around
`dispatchDataRollingUpdate`, and `checkAndHandleSentinelRollingUpdate`, a wrapper around
`dispatchSentinelRollingUpdate`, each calling `reportAvailabilityStall` on every non-error
result; an error result did not measure the tier and leaves the condition as it is. The
evaluation sits in the wrapper because the dispatch measures at the gocyclo ceiling, and because
the wrapper is the one frame every pass goes through whichever dispatch target ran — a level
retracted only by the site that raised it goes stale the moment that site stops being reached.
A pass without a stall is not a measurement either, so the retraction takes evidence:
`reportAvailabilityStall` writes `False/PodAvailable` over the tier's own standing True only when
`expiredUnavailablePod` finds no pod of the tier — the ordinal range of its StatefulSet (D7), a
StatefulSet or pod that is not ours counting as absent (ADR 0020), read from the cache — that
exists, is not terminating, is not Ready and has been not-Ready longer than `syncTimeout` by its
own clock. A pass that stopped at another wait first — a terminating pod, the no-replicas wait
after a failover, a fresher replacement with its own budget — never looked at the pod the report
names, and retracting on its silence made the condition flap for as long as one stall lasted
(`TestReportAvailabilityStall_RetractsOnlyOnEvidence`,
`TestSentinelRollingUpdate_TerminationPriorityDoesNotRetractTheReport`). The ownership rule,
registered in `conditionRegistry` ([ADR 0027](0027-conditions-are-levels-edges-or-history.md)):
each tier reports and retracts only its own reason; the data tier evaluates first, and under the
hold above the Sentinel roll runs only in a pass the data tier neither ended nor held, so the two
never contend within a pass — except in the pass that pauses a data roll (*Residual risks*). The
class exit — Sentinel disabled — evaluates a Sentinel report in `runSentinelRollingUpdate`, next
to `clearSentinelUpdatePending`, and retracts it on the same evidence: no code path in
`internal/controller` deletes a StatefulSet, so a leftover Sentinel pod that is still down keeps
the report True until it turns Ready or is removed by hand. The message names the pod and the
start of its clock — the instant it stopped being Ready, or its creation — never a running
duration, so a stall costs one condition write and not one per pass. No Event for the stall, as
for both siblings (ADR 0025 D7); the condition is alertable through `vko_valkey_status_condition`
([ADR 0021](0021-per-resource-metrics-and-the-alert-that-was-missing.md)), and no PrometheusRule
alert was added for it.

## Consequences

* A pod stuck `Terminating` now stalls the roll of its tier where it did not before. That is the
  intended direction, and it is the expensive half of the decision: the alternative is deleting a
  second pod because the first is wedged.
* The stall is visible only after `podTerminationOverrun`. Inside the budget the pass ends on the
  wait, so the phase string is the only signal for up to two minutes past a pod's graceful
  deadline. That is deliberate — reporting a one-second wait as a condition would make the
  condition meaningless.
* The Sentinel completion marker is later than it was: it waits out any Sentinel termination,
  including one the roll did not cause (D6, ADR 0024).
* Four `!available()` waits that used to be bounded through the sync-wait budget — the probe of a
  terminating pod failed, the budget expired, `pauseRollingUpdate` ended it — are now bounded
  through `terminationWait` instead. The exit is different on purpose: `pauseRollingUpdate` emits
  a Warning **and** clears the rolling-update state, which ADR 0010 D2–D4 forbids as a handover,
  and ADR 0025 D7 forbids as an Event.
* One more accessor pair to remember. `readyCondition` is deliberately awkward to read directly.
* An outdated pod that is not available is deleted at once instead of waited for (D11). A
  freshly unready outdated replica — a probe blip, a container restart — is restarted by the
  roll immediately; the same roll replaces it either way, and the Event says the pod was not
  available.
* A pod that never comes up is named on the CR — on any topology but a single pod (*Residual
  risks*) — after `syncTimeout` (default 5 min) measured on its own clock, from the instant it
  stopped being Ready or from its creation if it never was; not after the 2 min of
  `podTerminationOverrun`, and not relative to the delete. Measured once
  on Kind: about 64 s after the image change with `syncTimeout` 60 s (Status). Inside the budget
  the phase string is the only signal, as under D5. From then on the status write runs again, so
  `Ready`, `readyReplicas` and `masterPod` keep their pre-roll values only for the budget rather
  than for the whole stall, and `ValkeyReplicasMissing` (`for: 15m`), which reads
  `status.readyReplicas`, can see a data-tier stall. Traced by reading.
* A user who lowered `syncTimeout` gets the condition on a slow image pull, a slow boot or a slow
  schedule — a pod that was never Ready counts from its creation. It retracts to
  `False/PodAvailable` on the first pass that reaches the tier's evaluator once no pod of the tier
  is still unavailable past the budget, and nothing is deleted on its account: the budget bounds
  the observation, not the wait.
* On a Sentinel cluster every data-tier stall now holds the Sentinel update (D11), including a
  termination stall on a NotReady node that has nothing to do with the image. That is not
  urgent — the old Sentinels keep running and keep their quorum — but it changes what
  `PodTerminationStalled` and `PodRecreationStalled` leave running on that topology, and their
  API type comments say so.
* One condition carries one reason for two tiers, and that is accepted. A data-tier stall raised
  over a standing Sentinel report replaces it, and a Sentinel report is not re-measured while the
  data tier rolls, because the Sentinel evaluator is not reached then; a standing one stands until
  the Sentinel roll is entered again (ADR 0027 records both). An overwritten Sentinel report is
  not lost for a budget: the stuck Sentinel's clock is already old, so the first Sentinel pass
  after the data tier finishes that waits on it reports it again at once. Traced by reading.
* `PodAvailabilityStalled` is a level where its two siblings are edges: nothing the roll does
  proves a pod came up, so each tier re-measures it on every pass that reaches its roll, and
  retracts it only on a measurement of its own (`expiredUnavailablePod`) — a pass that ended at
  another wait first measured nothing about the named pod. Its message is stable across passes,
  where `terminationWait`'s carries the overrun rounded to seconds and rewrites
  `PodTerminationStalled` on every stalled pass; that inherited cost is unchanged.

## Alternatives Considered

**Fold `DeletionTimestamp` into `isPodReady`.** One line, and it would have refused the demotion
of the outgoing master — the divergence window in *Context*. Rejected.

**Deduplicate the delete only.** Fixes the log line and leaves the promotion, the Sentinel quorum
and the completion marker exactly as they were. It also does not deduplicate the Events: the
Event series cache keys on `(eventType, action, reason, …)` and not on the note, so every
`Normal/RollingUpdate` Event on one CR already collapses into one object with a rising
`Series.Count`. Rejected.

**Sentinel tier only.** The highest safety per line of any option, and a strict subset of what
landed rather than a competitor.

**Keep the rule as a list of the sites that change.** That formulation had been wrong three
times: it first missed `verifyReplacedReplicasSynced`, then the three shapes a re-review found,
then `handlePostFailover`. With thirteen readers and one exception, the list was of the wrong
half. Rejected in favour of the rename (D1).

**Bound the termination wait with `ensureWaitBound`, like every other rolling-update wait.**
Consistent with the family, and it was the original decision. It costs two CR writes per pod
replacement (arm and clear), a tenth annotation in `clearRollingUpdateState`, and it is
first-seen-wins per CR rather than per pod — so a bound left armed through a slow pod boot would
report the *next* termination as stalled immediately. The pod's own `deletionTimestamp` answers
the same question exactly, per pod, with no write and no arming failure mode. Rejected, with the
reasoning recorded in D5 because it is a deliberate exception to ADR 0010's mechanism.

**A named `terminationWaitTimeout` that resumes the delete.** Buys a resume for a case that must
not resume. Rejected; what landed bounds the *observation* instead.

**Replace an unavailable outdated pod only once it has outlived `syncTimeout` on its own clock
(T32 Q1 B).** Keeps `available()` as the delete rule with one named, bounded exception. In every
scenario of T32 it behaves like D11, because the stuck pod's clock has long expired by the time
the spec is fixed; it differs only for a freshly unready outdated pod, which it would wait on for
up to 5 min for a replacement that comes anyway — at the price of one more concept. Rejected.

**Never replace; bound and report only (T32 Q1 C).** No rule change, and the message tells a
human to delete the pod. Enough for T31's known failure — a current pod whose init container
recovers once the storage is fixed — and for no spec fix, where the stuck pod is outdated and
only a delete moves the roll. Rejected.

**Replace only a pod that has never been available.** The framing T32 was filed with. Not
observable through the API: kubelet keeps no history, and a list of waiting reasons is an
enumeration that misses "OOM on the old template, the user raises the limit". Dropped.

**Leave the Sentinel tier to a separate ticket, or to T31's runbook (T32 Q2).** A second ADR and
e2e pass for code three functions away from the data-tier fix, or a stuck Sentinel roll with a
frozen status and no pod name during the first fleet-wide Sentinel roll. Rejected.

**Hold the Sentinel roll only on `PodAvailabilityStalled` (T32 Q3 (b)).** The smallest change to
decided behaviour: the two existing stalls keep releasing it. It gives a data-tier
`DeferredRequeueAfter` two meanings depending on which condition set it, and ADR 0024 D1 keeps
an exception for two stall conditions on top of the paused-roll one found later (*Residual
risks*). Rejected.

**Leave D5 unchanged (T32 Q3 (c)).** Every data stall keeps releasing the Sentinel roll. With
the availability wait bounded, the bad-image case on a Sentinel cluster costs a Sentinel and the
spare vote. Rejected.

## Residual risks

* **Clock skew.** `time.Since(deletionTimestamp)` compares the operator's clock against the API
  server's. Both are in-cluster and normally NTP-synced, and a two-minute budget absorbs seconds
  of skew, but a badly skewed operator would report a stall early or late. Not measured.
  D11's clock adds a third party: kubelet stamps the Ready condition's `lastTransitionTime` with
  `metav1.Now()` on the node (`updateLastTransitionTime`, read), so that skew is between the
  operator and the node — except for a pod that has not been Ready since its creation, whose
  `creationTimestamp` is the API server's. The first-sync rule compares two times the same kubelet
  wrote in one call, so skew does not enter it.
* **That kubelet keeps `lastTransitionTime` across a kubelet restart is read, not measured.**
  `updateStatusInternal` falls back to the API object's status when its cache is empty, and
  `updateLastTransitionTime` keeps the old time while the status is unchanged
  (`k8s.io/kubernetes@v1.36.4`). Nobody restarted a kubelet under a stuck pod to watch it. If
  it did reset, the report would come late, never early.
* **The first-sync rule is read, not measured, and `firstSyncSlack` is a margin, not a measured
  distance.** That kubelet's first status sync stamps the Ready condition and `status.startTime`
  together is read in `updateStatusInternal` (`k8s.io/kubernetes@v1.36.4`: the
  `updateLastTransitionTime` calls, then `StartTime` set to `metav1.Now()` when none exists). The
  failure direction is early, not late: a pod whose Ready turned False within 5 s of its start
  after having been True would be dated from its creation.
* **A missing Sentinel pod still waits unbounded.** `scanSentinelPods` skips a NotFound pod, so
  neither `unavailableCurrent` nor `terminating` names it, and `sentinelWait` gives the quorum
  wait and the completion hold the plain requeue — T10's class
  ([ADR 0010](0010-every-rolling-update-wait-is-bounded.md) D16) on the Sentinel tier. D11
  bounds only a pod that exists.
* **A Sentinel tier of one or two Sentinels can never replace a Ready outdated Sentinel. Open,
  awaiting Hans's decision; nothing is decided here.** `quorum = replicas/2 + 1` equals `replicas`
  for one and two Sentinels (the CRD minimum is one), so a Ready target costs a vote and
  `readyCount - 1 < quorum` holds on every pass. The quorum wait then has nothing to name — no
  terminating pod, no unavailable current pod — and `sentinelWait` gives the plain requeue: the
  roll requeues unbounded, the pass ends on it and the status write never runs. Pre-existing,
  not caused by D11 — only an unavailable outdated Sentinel, which costs nothing, gets through.
  It stops being dormant with T31: [ADR 0032](0032-generated-pods-run-rootless.md) rolls every
  Sentinel tier once at the operator upgrade, so every such cluster reaches it then. The wds18
  fleet runs only three-Sentinel tiers, checked read-only on 2026-09-26.
* **A paused data roll releases the Sentinel roll in the pass that pauses.** `pauseRollingUpdate`
  returns an empty `RollingUpdateResult` — no requeue, no `DeferredRequeueAfter` — so
  `reconcileWorkload` passes the data tier as not holding and the Sentinel roll runs in that pass,
  and may delete one Sentinel if the quorum guard permits. The next pass dispatches the data roll
  again on a fresh budget (T23), and while it waits the Sentinel roll is not reached, so the
  exception recurs once per pause. The shape the hold exists for is the bad image, and every
  pause site sits behind an `available()` check of a data pod on the new spec, so the new image
  did start — which makes a Sentinel returning on a spec that cannot start unlikely here, not
  impossible. The same pass is the one in which the ownership rule's "never contend within a
  pass" does not follow from the hold: a Sentinel report written there replaces a data report
  still standing on evidence, until a later data pass that waits on that pod raises it again.
  Accepted; traced by reading, no test drives it.
* **A single pod that never starts is not reported.** `handleStandaloneRollingUpdate` writes no
  rolling-update state, so once the only pod of a `spec.replicas: 1` cluster has been replaced it
  is current, and `dispatchDataRollingUpdate` takes its "no rolling update needed" return before
  any wait: `standaloneWait` is not reached, `PodAvailabilityStalled` is not raised, and the pod
  shows only as phase `Provisioning` through `updateStandaloneStatus`. After a spec fix the pod is
  outdated and D11's replacement rule deletes it, so the roll is not stuck; only the report is
  missing. Pre-existing in shape, and the scope limit
  [ADR 0032](0032-generated-pods-run-rootless.md) D7 states. Traced by reading.
* **`verifyNewMasterReady` reads the new master's `DBSIZE` and does not refuse on it.** Its
  comment calls it the check that an empty replica was not promoted while the old master had
  data; the code logs the count and returns verified on any successful read, so the gates in
  front of the former master's delete in `replaceRemainingPods` are role, attached replicas and
  no sync in progress. Pre-existing and not fixed by T32; it concerns D11 because D11's
  *Replacement* argument leans on that gate, and D11's comment in `replaceRemainingPods` was
  corrected not to claim a key check.
* **The `waitForReplicasReady` split is behaviour-neutral, and therefore not mutation-guarded.**
  An available pod carries no not-Ready clock, so routing an available outdated pod into
  `availabilityWait` returns the same plain requeue through the zero-clock branch; the second
  half of `TestWaitForReplicasReady_SplitsUnavailableFromOutdated` passes with or without the
  split. It exists so the log names the right wait and the second master is kept out of the
  availability wait by construction rather than by a zero timestamp.
* **The phase alternates during a reported stall.** The multi-replica paths write
  `Rolling Update i/n` before dispatch and the Sentinel roll writes `Sentinel Rolling Update i/n`
  before it acts, and the status write the stall buys back writes its own
  verdict — `Provisioning` while a pod is not Ready — so every stalled pass makes two status
  writes and the phase alternates for as long as the stall lasts. Inherited: both sibling stalls
  already do it, and D11's stable message avoids only the condition rewrite. Traced by reading,
  not measured. One consequence is not verified either way: `vko_valkey_status_phase` carries
  exactly one series per resource, labelled with the current phase, so a scrape that lands
  between the two writes replaces the `Provisioning` series, and whether that resets the
  `for: 30m` of `ValkeyPhaseNotOK` — the alert the no-new-alert decision leaned on — has not been
  checked. `PodAvailabilityStalled` through `vko_valkey_status_condition` is the stable signal.
* **`deleteNextPendingPod` keeps `available()`, and its fall-through stays an unbounded plain
  requeue.** A leftover outdated second master that never becomes available is skipped, and the
  function requeues with nothing named. Not D11's class — a master is not a replaceable replica —
  and left with the other unbounded requeues of that kind T32 lists as adjacent findings.
* **Two data replicas can still be down at once.** `sortReplicaCandidates` orders youngest-first
  and `verifyReplacedReplicasSynced` checks only replaced pods, so an outdated replica that is
  unavailable but not `candidates[0]` does not stop the delete of a healthy candidate.
  Pre-existing; D11 neither causes nor fixes it — the unavailable pod it deletes is
  `candidates[0]` and already down.
* **The D11 e2e has run only locally, not on a CI leg.** `TestE2E_RollingUpdate_UnavailableReplacementIsReportedAndReplaced`
  drives the data tier onto an unpullable image and back on a 3+3 Sentinel cluster with
  `syncTimeout` 60 s. It passed on 2026-09-26 on Kind with a control plane and three workers
  (Kubernetes v1.36.1) against valkey 9.1.1 (Status), then inside both full local suites on that
  cluster — the Valkey 9 and the Valkey 8 line, 51/51 each — and once more on the final image
  against valkey 9.1.1. Neither single-node CI leg has run it, because the branch has not been
  through the pipeline, and it has not run on a single-node cluster at all. Until it has, these
  local runs on one multi-node Kind cluster are the field evidence for the data-tier half —
  including that the Sentinel UIDs stay unchanged under the hold.
* **D8 is a real hole, accepted.** At five or more Sentinels the quorum arithmetic permits a
  second delete while a replacement is booting, and the gate cannot see a pod that is already
  gone. No fleet cluster runs more than three.
* **D4's non-Sentinel asymmetry.** `RollingUpdateComplete` can still fire over a terminating pod
  on the non-Sentinel path. Bounding `finalizeMultiReplicaRollingUpdate` is the follow-up if that
  matters.
* **D10 is not analysed.** The steady-state adoption path can still record a terminating pod as
  the master authority. A StatefulSet pod name is stable, so recording it *by name* still
  resolves to the pod that returns — whether that is right is the open item.
* **The e2e cannot order its own delete against the operator's, and has to attribute instead.**
  `TestE2E_RollingUpdate_NoSecondDeleteWhileAPodTerminates` injects a chaos delete on the very
  event that unblocks the roll -- a replaced pod becoming Ready -- so operator and test race for
  who deletes second, and the observed overlap alone says nothing about who caused it. Measured
  in CI on 2026-08-26: the operator deleted its next candidate at 08:58:05 into a quiet tier,
  logged `Waiting for the replaced pod to become available` on the very next pass, and the test
  deleted its victim 0.6 s later -- a correct hold reported as a violation. `metav1.Time` has
  second granularity, so the two deletions are not orderable from the objects either. The test
  therefore records which pod it deleted and what was already terminating at that instant, and
  excuses only that combination; a pod the operator puts on its way out *after* the injection is
  still a violation. **Waiting for a quiet tier before injecting is not the fix** -- the operator
  closes that window within the same second, so the injection would mostly not happen and the
  test would silently stop testing. The residual hole is one API round trip: an operator delete
  landing between the test's snapshot and its own delete is excused. That window is milliseconds
  against a roll that deletes roughly three times in ninety seconds.
* **The gate is not exercisable in envtest.** There is no kubelet, so a deleted pod never leaves
  `Terminating`. The unit fixtures build the state directly and the e2e is the only tier that
  sees a real one.
* **Fixture trap, for whoever writes the next test.** The fake client refuses an object with a
  `DeletionTimestamp` and no finalizer, and a pod that has one is undeletable — `deleteOwnedPod`
  on it is a silent no-op. A test that seeds every pod that way passes whether or not the gate
  exists. Only the pod meant to be terminating carries the finalizer; the pod whose survival is
  asserted must be genuinely deletable. Likewise `terminating` is read as a `podState` field and
  never as `ps.pod.DeletionTimestamp`, because fixtures routinely pass `pod: nil` — and since
  D11 the same holds for `notReadySince`, which is never re-derived from `ps.pod`. A `podState`
  fixture without a `notReadySince` has a zero clock and gets the plain requeue, so a test of the
  stall must set it explicitly.
* **The 60 s figure in *Context* was wrong once.** The first measurement used an unconditional
  `preStop: sleep 60` and concluded every replacement had a 60 s terminating-Ready window. The
  operator's hook is a wait loop with a 60 s cap that a replica releases in about a second. What
  survived the correction is the load-bearing half: kubelet keeps `PodReady=True` for the whole
  termination, whatever its length.

## References

* [`internal/controller/rolling_update.go`](../../internal/controller/rolling_update.go) —
  `podState`, `available`, `reachable`, `firstTerminatingPod`, `terminationWait`,
  `holdDeleteWhileTerminating`, `waitForUnavailablePod`, `standaloneWait`,
  `clearPodTerminationStalled`, `scanSentinelPods`, `podTerminationOverrun`; for D11
  `podNotReadySince`, `stampedAtFirstSync`, `firstSyncSlack`, `unavailablePod`,
  `availabilityWait`, `reportAvailabilityStall`, `expiredUnavailablePod`,
  `firstUnavailableExisting`, `notAvailableNote`, `dispatchDataRollingUpdate`,
  `sentinelScan.observe`, `sentinelScan.deleteTarget`, the `cost > 0` quorum guard in
  `dispatchSentinelRollingUpdate`, `sentinelWait`, `finishSentinelRollingUpdate`; for the
  residual risks `pauseRollingUpdate` (the known release of the Sentinel roll) and
  `verifyNewMasterReady` (the unrefused `DBSIZE`)
* [`internal/controller/steady_state_master.go`](../../internal/controller/steady_state_master.go) — the D2 construction site
* [`internal/controller/valkey_controller.go`](../../internal/controller/valkey_controller.go) — the `DeferredRequeueAfter` branch of `reconcileWorkload`; for D11 `handlePostRollingUpdateChecks`, `runSentinelRollingUpdate`, `soonerRequeue`
* [`internal/controller/condition_registry.go`](../../internal/controller/condition_registry.go) — the `PodAvailabilityStalled` row and its ownership rule, and the extended `PodTerminationStalled` clear site
* [`api/v1/valkey_types.go`](../../api/v1/valkey_types.go) — `ConditionTypePodTerminationStalled`; for D11 `ConditionTypePodAvailabilityStalled`, `ReasonValkeyPodNotAvailable`, `ReasonSentinelPodNotAvailable`, `ReasonPodAvailable`, `RollingUpdateSpec.SyncTimeout`
* [`internal/controller/pod_termination_test.go`](../../internal/controller/pod_termination_test.go) — the unit rules
* [`internal/controller/pod_availability_test.go`](../../internal/controller/pod_availability_test.go) — the D11 unit rules
* [`test/e2e/pod_termination_test.go`](../../test/e2e/pod_termination_test.go) — the field rule
* [`test/e2e/pod_availability_test.go`](../../test/e2e/pod_availability_test.go) — the D11 field rule for the data tier, green in both full local Kind suites (Valkey 9 and 8) on 2026-09-26, not yet on a CI leg
* [ADR 0004](0004-opt-in-poddisruptionbudgets.md) — the Sentinel quorum this reuses
* [ADR 0007](0007-failover-aware-rolling-update.md) — the rolling update and its D9 on what readiness may mean, whose second half follows D11
* [ADR 0010](0010-every-rolling-update-wait-is-bounded.md) — every wait is bounded; D5 and D11 are deliberate exceptions to its *mechanism*, not to its rule; D6 and D13 spend the budget D11 shares, D16 the third stall the D11 hold covers, D17 the same bound from that ADR's side
* [ADR 0011](0011-evidence-based-steady-state-split-brain-resolution.md) — the steady-state check a stalled pass used to suspend
* [ADR 0017](0017-test-and-ci-policy.md) — D18, why the Sentinel quorum-wait fixtures changed and their assertions did not
* [ADR 0021](0021-per-resource-metrics-and-the-alert-that-was-missing.md) — `vko_valkey_status_condition`, where `PodAvailabilityStalled` is alertable
* [ADR 0022](0022-sentinel-identity-is-pinned-to-the-pod.md) — what a Sentinel tier without a majority costs
* [ADR 0024](0024-the-sentinel-tier-reports-its-own-completion.md) — the completion marker D6 changes; its D1 ordering, which the D11 hold restores except in the pass that pauses a data roll
* [ADR 0025](0025-a-split-brain-warning-means-one-that-did-not-resolve-itself.md) — the carve-out and the zero-Warning promise
* [ADR 0027](0027-conditions-are-levels-edges-or-history.md) — a level with two evaluators and the ownership rule it owes
* [ADR 0032](0032-generated-pods-run-rootless.md) — T31, released only after D11; its fleet-wide roll of every Sentinel tier is what reaches the open one- and two-Sentinel residual
