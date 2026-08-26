# ADR 0026: A pod being deleted is not available

## Status

Accepted. Date: 2026-08-25.

Implemented: the `available()` / `reachable()` split on `podState` with the per-site answers of
D1–D4, the delete gate of D5, the tier definition of D7, the bounded stall observation and the
`PodTerminationStalled` condition of D5, and the Sentinel counters of D6. Unit coverage in
[`internal/controller/pod_termination_test.go`](../../internal/controller/pod_termination_test.go),
field coverage in [`test/e2e/pod_termination_test.go`](../../test/e2e/pod_termination_test.go).

Open, and named as such: the steady-state master authority reads no `DeletionTimestamp` at all
(D10 and *Residual risks*); this ADR binds the rolling update only.

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

`terminating` is set in `collectPodStates` from `pod.DeletionTimestamp != nil`. Every site that
**spends** a pod — deletes it, promotes it, counts it toward a quorum or a completion — calls
`available()`. The rename is the point: it turned all thirteen readers into compile errors, so
each was decided once and recorded, and new code that reaches for the obvious name gets the safe
answer. Stating the rule as an enumeration of sites had been wrong three times before this;
thirteen readers to one exception makes the enumeration an enumeration of the wrong half.

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
The standalone handler is a single-pod tier where the gate and the boot wait are the same
branch, and both go through `standaloneWait`.

Every wait on a terminating pod — the gate and the `!available()` waits alike — goes through
`terminationWait`, which reports it in one of two shapes:

* inside the budget: a requeue that ends the pass. This is the normal case, and it costs a clean
  roll nothing: it replaces one no-op re-delete with one no-op wait at the same cadence, one API
  call cheaper, for the second a replica actually takes.
* past it: `DeferredRequeueAfter` plus the `PodTerminationStalled` condition (reason
  `PodStuckTerminating`, message naming the pod). **The delete is still refused** — deleting a
  second pod because the first is wedged is the failure the gate exists to prevent, and stopping
  half-done keeps a serviceable cluster (ADR 0007). What expiry buys back is the tail of the
  reconcile pass, which a requeue return skips: the Sentinel roll, `checkAndRecoverNoMaster`,
  `checkSteadyStateSplitBrain` — per [ADR 0011](0011-evidence-based-steady-state-split-brain-resolution.md)
  D1 the only thing that re-detects a split brain outside a rolling update — and the status
  write. On a NotReady node the `DeletionTimestamp` never clears, so that blackout would
  otherwise be permanent. The rolling-update state annotation stays, nothing is deleted, and the
  roll resumes on its own once the pod is gone, because the StatefulSet watch fires.

The condition clears from the delete gate, which every delete site calls on every pass — and
also from `clearRollingUpdateState`, because there is exactly one shape with no later gate: a
stall on the **last** pod the roll replaces, where the pod returns, everything is current, and
the pass finalizes without passing a gate again. Without that second clear the condition would
stand forever on a healthy cluster.

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
write, and the deadline is per pod instead of first-seen-wins per CR. It is the one bounded
rolling-update wait that is not an `ensureWaitBound` member, and this paragraph is why.

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

## Residual risks

* **Clock skew.** `time.Since(deletionTimestamp)` compares the operator's clock against the API
  server's. Both are in-cluster and normally NTP-synced, and a two-minute budget absorbs seconds
  of skew, but a badly skewed operator would report a stall early or late. Not measured.
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
  never as `ps.pod.DeletionTimestamp`, because fixtures routinely pass `pod: nil`.
* **The 60 s figure in *Context* was wrong once.** The first measurement used an unconditional
  `preStop: sleep 60` and concluded every replacement had a 60 s terminating-Ready window. The
  operator's hook is a wait loop with a 60 s cap that a replica releases in about a second. What
  survived the correction is the load-bearing half: kubelet keeps `PodReady=True` for the whole
  termination, whatever its length.

## References

* [`internal/controller/rolling_update.go`](../../internal/controller/rolling_update.go) —
  `podState`, `available`, `reachable`, `firstTerminatingPod`, `terminationWait`,
  `holdDeleteWhileTerminating`, `waitForUnavailablePod`, `standaloneWait`,
  `clearPodTerminationStalled`, `scanSentinelPods`, `podTerminationOverrun`
* [`internal/controller/steady_state_master.go`](../../internal/controller/steady_state_master.go) — the D2 construction site
* [`internal/controller/valkey_controller.go`](../../internal/controller/valkey_controller.go) — the `DeferredRequeueAfter` branch of `reconcileWorkload`
* [`api/v1/valkey_types.go`](../../api/v1/valkey_types.go) — `ConditionTypePodTerminationStalled`
* [`internal/controller/pod_termination_test.go`](../../internal/controller/pod_termination_test.go) — the unit rules
* [`test/e2e/pod_termination_test.go`](../../test/e2e/pod_termination_test.go) — the field rule
* [ADR 0004](0004-opt-in-poddisruptionbudgets.md) — the Sentinel quorum this reuses
* [ADR 0007](0007-failover-aware-rolling-update.md) — the rolling update and its D9 on what readiness may mean
* [ADR 0010](0010-every-rolling-update-wait-is-bounded.md) — every wait is bounded; D5 is a deliberate exception to its *mechanism*, not to its rule
* [ADR 0011](0011-evidence-based-steady-state-split-brain-resolution.md) — the steady-state check a stalled pass used to suspend
* [ADR 0022](0022-sentinel-identity-is-pinned-to-the-pod.md) — what a Sentinel tier without a majority costs
* [ADR 0024](0024-the-sentinel-tier-reports-its-own-completion.md) — the completion marker D6 changes
* [ADR 0025](0025-a-split-brain-warning-means-one-that-did-not-resolve-itself.md) — the carve-out and the zero-Warning promise
