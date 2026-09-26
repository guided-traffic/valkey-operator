# ADR 0024: The Sentinel tier reports its own completion

## Status

Accepted, amended 2026-08-25 (D3, see D8) and 2026-09-26 (D1, D5, D6 and the
Consequences, see D9; D9's quorum guard for a tier of one or two Sentinels, see
D10). Date: 2026-08-23.

Implemented: the `SentinelUpdatePending` condition, the `SentinelUpdateComplete`
event, the `Sentinel Rolling Update i/n` phase, the reworded
`RollingUpdateComplete` message, and the clear on a CR whose Sentinel was
disabled mid-roll. Amended 2026-08-25: convergence additionally excludes a
Sentinel pod that is being deleted (D8).

Amended 2026-09-26 (T32; the rule's primary home is
[ADR 0026](0026-a-pod-being-deleted-is-not-available.md) D11): **the completion
hold is bounded.** A replacement Sentinel that exists and never becomes available
is reported as `PodAvailabilityStalled=True` (reason `SentinelPodNotAvailable`)
once it has been down longer than `spec.rollingUpdate.syncTimeout`, and the pass
continues to the status write instead of ending on the wait; the Sentinel
result's `DeferredRequeueAfter` is applied rather than dropped. The quorum wait
takes the same route, and the quorum guard applies only to a delete that spends
a vote, so after a spec fix a Sentinel stuck on the broken spec is replaced even
when the quorum is already lost. The report is retracted on evidence only. A
terminating Sentinel pod in either wait goes through `terminationWait`, and
`PodTerminationStalled` is cleared by those waits and at the Sentinel
completion. A holding data tier now holds the Sentinel roll, which restores
D1's ordering with one known exception, the pass in which a data roll pauses
(D9). Implemented. The hold of the Sentinel roll passed its e2e on 2026-09-26;
the Sentinel-tier waits have unit coverage only, for the reason under *Residual
risks*. *(Superseded 2026-09-26 by D10; until that decision, the same day, this
read: "Open, awaiting a decision: a tier of one or two Sentinels can never
replace a Ready outdated pod (pre-existing, Residual risks).")*

Decided 2026-09-26 by Hans (D10): **a tier of one or two Sentinels rolls
serially** — one Sentinel at a time, and only while every other one is
available (`sentinelDeleteKeepsVotes`). Implemented; unit-tested and
mutation-checked. The e2e `TestE2E_RollingUpdate_TwoSentinelsRollSerially` is
written and ~~has not been run~~ *(green 2026-09-26, locally on Kind and not in
CI, on both Valkey lines — in an earlier run and inside both full suites on
the final image of the branch)* (*Residual risks*).

## Context

The rolling update runs in two tiers, and the ordering is structural:
`reconcileWorkload` drives the data-tier rolling update first, and the Sentinel
check (`checkAndHandleSentinelRollingUpdate`) runs only in a pass where the data
tier neither errored nor requeued — nor, since 2026-09-26, held a wait past its
bound (D9). Both emission sites of the `RollingUpdateComplete` event —
`finalizeRollingUpdate` and `verifyTopologyRestored` — belong to the data tier,
and both clear the
rolling-update state annotation in the same breath. That clearing is load-bearing:
the absence of the state annotation is what means "no data-tier update in
flight" (ADR 0010 — once it is gone, nothing calls `detectAndResolveSplitBrain`).

The Sentinel roll itself was stateless and silent. Each pass compared the
Sentinel pods against the persisted template (`sentinelPodNeedsUpdate`), deleted
at most one outdated pod under a quorum guard, and requeued; when no pod was
outdated it returned an empty result — the same answer every healthy
steady-state pass produces, indistinguishable from "a roll just finished". No
event, no condition, no phase write.

The concrete failure: during the 1.11.0 fleet rollout on wds18-k8s-main
(2026-08-22), every sentinel-enabled cluster emitted `RollingUpdateComplete` and
cleared its state **before any Sentinel pod was replaced**; Sentinel rolling
continued for up to ~90 s afterwards. The post-rollout audit could not prove
Sentinel-tier completion from the log at all — only from live cluster state.
Anything sequencing on "Completed" (a human, a pipeline) acts too early. The
status surface was wrong during that window too: while Sentinel pods roll, the
pass ends before `updateStatus`, so the phase kept showing the last data-tier
value — and on a Sentinel-only spec change (Sentinel podLabels, resources) it
showed `OK` for the whole roll, violating the status contract ("OK when
healthy, otherwise the current task").

One trap constrained the design: a second convergence predicate already exists.
`sentinelRolloutComplete` (gating the legacy-certificate cleanup) is
revision-based, while the roll driver is image/hash-based. The two can disagree —
a template change covered by no hash bumps the controller revision but rolls
nothing — so a completion marker on the revision predicate could wait forever
for pods the driver will never replace.

## Decision

- **D1 — Completion is reported per tier.** `RollingUpdateComplete` means the
  data tier: all data pods run the persisted template. The Sentinel tier emits
  its own `SentinelUpdateComplete` event when its roll finishes. The data-tier
  event message names its scope and points at the Sentinel tier; its timing does
  not change. *(Amended 2026-09-26 by D9: the ordering that message points at —
  "any outdated Sentinel pods are rolled next" — had an exception from
  2026-08-25, when a data-tier wait past its bound began to continue the pass
  into the Sentinel roll. A holding data tier now holds the Sentinel roll, which
  restores the ordering except in the pass in which a data roll pauses — see
  D9.)*
- **D2 — The `SentinelUpdatePending` condition is the level, and its previous
  value is the memory.** `checkAndHandleSentinelRollingUpdate` sets it to True
  (reason `SentinelPodsOutdated`, message carrying the progress count) whenever
  a Sentinel pod needs replacing, before acting. The completion pass — no
  outdated pod, and the condition standing True — flips it to False (reason
  `Completed`) and emits the event. There is no annotation and no new state
  machine behind it; a CR that never rolled never carries the condition, so a
  healthy steady-state pass writes nothing and the upgrade is fleet-neutral.
- **D3 — Convergence means every pod is current AND Ready, judged by the
  driver's own predicate.** *(Amended 2026-08-25 by D8: "Ready" is now
  "available", i.e. Ready and not being deleted.)* The completion check counts pods that exist, pass
  `sentinelPodNeedsUpdate` and are Ready (`updatedReadyCount == replicas`).
  "No outdated pod" alone is not completion — after the last delete the
  replacement is still booting, which is exactly the too-early edge this ADR
  removes. A completion marker must never use a different predicate than the
  roll driver (the `sentinelRolloutComplete` trap above).
- **D4 — The event is emitted exactly once per roll, gated on the flip
  landing.** `writeStatusCondition` reports whether the status write actually
  changed the stored condition; only a landed True→False flip emits. A stale
  cache can delay the edge by one pass, never double-emit it, because a status
  update from a stale copy cannot land. A failed flip requeues: with every pod
  Ready, nothing else re-triggers the pass.
- **D5 — The phase names the Sentinel roll while it runs.** While the roll is in
  flight the phase reads `Sentinel Rolling Update i/n` (i = pods current and
  Ready), written through `updatePhase` alongside the condition. The first
  converged pass falls through to `updateStatus`, which restores the normal
  phase. This covers both the image-bump tail (previously a stale
  `Rolling Update N/N`) and Sentinel-only rolls (previously `OK` throughout).
  *(Amended 2026-09-26 by D9: a pass whose wait has outlived its bound falls
  through to `updateStatus` as well, so while a Sentinel stall is reported the
  phase alternates between `Sentinel Rolling Update i/n` and that function's
  verdict — see Consequences.)*
- **D6 — Disabling is not completing.** A CR whose Sentinel is disabled while
  the condition stands gets it cleared (reason `SentinelDisabled`) on the
  non-Sentinel path of `handlePostRollingUpdateChecks`, with no completion
  event. The clear is presence-guarded, like `clearSidecarUpdatePending`, so no
  condition is ever created on a CR that never carried one. *(Amended
  2026-09-26 by D9: that path now lives in `runSentinelRollingUpdate`, the
  Sentinel half of `handlePostRollingUpdateChecks`, and retracts a standing
  Sentinel report of `PodAvailabilityStalled` in the same place — never writing
  onto a CR that carries none, never touching a data-tier report, and, like
  every retraction of it, only on evidence (D9). No code path deletes the
  Sentinel StatefulSet when Sentinel is disabled, so a Sentinel pod it left down
  past the budget keeps the report standing until that pod is Ready,
  terminating or gone — read, not run.)*

- **D8 — Amendment, 2026-08-25: a Sentinel pod that is being deleted is not
  converged either.** kubelet keeps `PodReady=True` for the whole termination of
  a pod whose readiness probe still passes, so the Ready predicate of D3 counted
  a Sentinel that was on its way out — the same too-early edge D3 exists to
  remove, reached through a different input. `scanSentinelPods` now feeds both
  the quorum guard and `updatedReadyCount` from one availability predicate
  (Ready **and** no `DeletionTimestamp`), so the marker cannot fire over a pod
  that is not running. The price is stated rather than hidden: the completion
  marker now also waits out a Sentinel termination the roll did not cause — a
  chaos kill, an eviction, a node drain — and anything sequencing on
  `SentinelUpdateComplete` sees it that much later. The counterpart of the
  change, and the full rule, is
  [ADR 0026](0026-a-pod-being-deleted-is-not-available.md) D6.

- **D9 — Amendment, 2026-09-26: the Sentinel waits on a pod that exists are
  bounded observations, and a holding data tier holds the Sentinel roll.** The
  rule is [ADR 0026](0026-a-pod-being-deleted-is-not-available.md) D11 (T32);
  this is what it changes for the tier this ADR is about.

  *The completion hold is bounded.* `finishSentinelRollingUpdate` takes the pod
  scan, and while `updatedReadyCount < replicas` it waits through
  `sentinelWait` — as does the quorum wait in `dispatchSentinelRollingUpdate`,
  which is now reached only for a target that holds a vote (below).
  `sentinelWait` routes by what the scan can name:
  - a terminating Sentinel pod: `terminationWait`, tier `sentinel`
    (ADR 0026 D5). Both waits used to requeue on one with no bound — the hold
    passes no delete gate, and with three Sentinels a terminating pod made the
    quorum guard refuse a healthy target before the gate behind it was reached;
    past `podTerminationOverrun` they now report `PodTerminationStalled`.
  - a pod on the current spec that exists and is not available:
    `availabilityWait`, measured on the pod's own not-Ready clock
    (`podNotReadySince`): the Ready condition's `lastTransitionTime`, or the
    `creationTimestamp` when there is none or when kubelet stamped it at its
    first status sync — no later than `firstSyncSlack` (5 s) after
    `status.startTime` (`stampedAtFirstSync`). Such a pod has never been Ready,
    and without that rule a pod Pending longer than the budget had its clock
    reset to the moment it was scheduled. Of several such pods the scan names
    the one with the oldest clock, not the lowest ordinal: a pod that just came
    back on the same broken spec must not hide one that has been down for hours
    behind a fresh budget. Within `spec.rollingUpdate.syncTimeout` it is the
    plain requeue; past it, `DeferredRequeueAfter` and
    `PodAvailabilityStalled=True` with reason `SentinelPodNotAvailable`, naming
    the pod and the instant it stopped being Ready. The wait is not lifted — a
    pod on the current spec comes back identical when deleted — only the pass
    stops ending on it.
  - anything else: the plain requeue, unbounded — a missing pod, and the quorum
    wait of a tier of one or two Sentinels, where every pod is available and
    nothing can be named (*Residual risks*). *(Amended 2026-09-26 by D10: the
    second case is gone. A small tier whose pods are all available passes the
    guard, so its quorum wait is reached only with a pod down, and that pod is
    named by the two arms above like on any other tier unless it is missing;
    the missing pod is what is left here.)*

  `SentinelUpdatePending` stays True and no `SentinelUpdateComplete` is emitted
  over a stall: D3's predicate is unchanged, and a stalled roll is not a
  finished one. The condition has one evaluator per tier:
  `checkAndHandleSentinelRollingUpdate` is now a thin wrapper around
  `dispatchSentinelRollingUpdate` that calls `reportAvailabilityStall` on every
  non-error result, and it retracts only a Sentinel report — False, reason
  `PodAvailable`, written only over a standing True, and only on evidence:
  `expiredUnavailablePod` must find no Sentinel pod (the StatefulSet's ordinal
  range, ours) that exists, is not terminating, is not Ready and has been
  not-Ready longer than `syncTimeout`. A pass that stopped at another wait first
  did not measure the stuck pod — `sentinelWait` routes a terminating Sentinel
  ahead of it — and retracting on that silence made the condition flap for as
  long as one stall lasted. Class exit is D6's.

  *A stalled pass continues, and its recheck is kept.* `runSentinelRollingUpdate`
  returns the Sentinel result's `DeferredRequeueAfter` as the pass's recheck,
  merged with the split-brain check's by `soonerRequeue`. It used to be dropped,
  so a Sentinel-tier `PodTerminationStalled` from the delete gate continued the
  pass but scheduled no recheck of its own. On a Sentinel cluster what
  continuing buys back is the status write alone: the no-master recovery and
  the steady-state split-brain check run only on non-Sentinel topologies.
  `PodTerminationStalled` is cleared by `sentinelWait` whenever no Sentinel pod
  is terminating, and by the completion in `finishSentinelRollingUpdate`: the
  hold passes no delete gate, so without those two a stall reported on the last
  replaced Sentinel would stand on a converged tier.

  *The Sentinel tier rolls after the data tier again, with one known
  exception.* From 2026-08-25 (ADR 0026 D5) a data-tier wait past its bound
  returned `DeferredRequeueAfter`, which continues the pass — and the first
  thing the rest of the pass did was the Sentinel roll. That exception to D1 was dormant
  while the only such stalls were environmental (a pod wedged terminating on a
  NotReady node, a StatefulSet controller that cannot create a pod). Bounding
  the availability wait would have made it the common case, and `spec.image`
  is shared by both tiers — both Sentinel containers are built from it
  ([`sentinel.go`](../../internal/builder/sentinel.go)): a replica stuck on a
  bad image would release the Sentinel roll onto the same image, take a
  healthy Sentinel down with it and spend the spare vote. `reconcileWorkload`
  now passes `rollingResult.DeferredRequeueAfter > 0` into
  `handlePostRollingUpdateChecks`, and `runSentinelRollingUpdate` skips the
  Sentinel roll for that pass — for all three stall conditions
  (`PodTerminationStalled`, `PodRecreationStalled`, `PodAvailabilityStalled`).
  The exception left is the pause: `pauseRollingUpdate` returns an empty result
  — no requeue, no deferral — so the pass in which a data roll pauses runs the
  Sentinel roll (*Residual risks*). What keeps the two evaluators of
  `PodAvailabilityStalled` from contending within a pass is narrower than the
  ordering and has no exception: a data stall result always carries
  `DeferredRequeueAfter`, so a pass that reports a data stall never runs the
  Sentinel roll. Across passes they share one condition (*Residual risks*).

  Target selection moved with it and is ADR 0026 D11's, not this ADR's: an
  outdated Sentinel that is neither available nor terminating is replaced
  first, and the quorum guard applies only to a delete that spends a vote —
  `cost > 0 && readyCount-cost < quorum`, where `sentinelScan.deleteTarget`
  charges the target one vote only when it is available. A non-voting target is
  therefore replaced even when the quorum is already lost: two of three
  Sentinels stuck on the broken spec after the fix leave `readyCount` at 1, and
  a guard charged against that refused forever. The delete gate behind the
  guard still refuses each such delete while any Sentinel pod is terminating.
  After a spec fix a stuck Sentinel roll moves on by itself — up to a Ready
  outdated pod of a tier of one or two Sentinels, which it never replaces
  (*Residual risks*). *(Amended 2026-09-26 by D10: the guard is now
  `cost > 0 && !sentinelDeleteKeepsVotes(readyCount, cost, quorum, total)`,
  which is the expression above for three or more Sentinels and `total-1` in
  place of `quorum` for one or two, so such a tier replaces its Ready outdated
  pods too, one at a time.)*

- **D10 — Decision, 2026-09-26: a tier of one or two Sentinels rolls
  serially.** `spec.sentinel.replicas` has `Minimum=1`, and at one and two the
  quorum `replicas/2+1` equals the replica count: such a tier has no spare vote,
  so no delete of a Ready pod could pass the guard — `readyCount-cost < quorum`
  held for every one — and the roll refused forever with a frozen status
  (*Residual risks*, as it read
  before this decision). The guard is now `sentinelDeleteKeepsVotes(readyCount,
  cost, quorum, total)`:
  - three or more Sentinels (`quorum < total`): `readyCount-cost >= quorum`,
    unchanged.
  - one or two Sentinels (`quorum == total`): `readyCount-cost >= total-1` —
    one Sentinel at a time, and only while every other one is available. A
    single Sentinel is replaced whenever it is available; of two, the second is
    deleted only once the first one's replacement is available.

  Everything else about the roll stays as D9 has it: the guard applies only to
  a delete that spends a vote, so a non-voting target (cost 0) is exempt as
  before; the delete gate behind the guard still refuses every delete while any
  Sentinel pod is terminating; and the quorum wait, now reached on a small tier
  only while one of its pods is down, routes through `sentinelWait`, which names
  that pod unless it is missing.

  The reason is what such a tier is. Sized at one or two, it tolerates no
  Sentinel outage in the first place — a failover needs the whole tier
  (`SentinelQuorumFor`, and the `sentinel monitor` quorum built from it), and
  the CRD field's own documentation says two Sentinels tolerate no outage at
  all. A serial roll costs automatic failover for as long as one Sentinel is
  being replaced, which is what any single Sentinel failure costs that tier
  anyway. Refusing bought nothing in exchange: no Sentinel change reached a
  Ready Sentinel of such a tier unless something other than the operator
  deleted it — no image change, no certificate rotation
  ([ADR 0030](0030-rotating-certificates-rotate-the-instances-that-cannot-reload-them.md)
  treats `valkey-sentinel` as pinning its material until the pod is replaced),
  and not the rootless posture
  ([ADR 0032](0032-generated-pods-run-rootless.md)), whose release rolls every
  Sentinel tier once at the operator upgrade.

## Consequences

- Two completion events now exist, and consumers must pick the right one. A
  pipeline that keeps sequencing on `RollingUpdateComplete` behaves exactly as
  before this ADR — too early on sentinel-enabled clusters. The reworded event
  message is the only pointer such a consumer gets.
- The condition auto-exports as a `vko_valkey_status_condition` series
  (ADR 0021), so "which clusters are mid-Sentinel-roll" is a fleet-wide metric
  with no collector change — read at collect time, not written from a reconcile
  pass.
- The Sentinel roll now writes status (condition + phase) where it previously
  wrote nothing. The writes self-skip when unchanged, so a wait pass still
  costs no API call; the progress steps cost one status update each. *(Amended
  2026-09-26 by D9: that holds inside the wait budget; a pass with a reported
  stall costs two, see below.)*
- *(Superseded 2026-09-26 by D9 for a Sentinel pod that exists; stated here as
  it read.)* A cluster with the condition True and a Sentinel pod that never
  becomes Ready requeues with `Sentinel Rolling Update i/n` standing
  indefinitely. That is the same unbounded quorum/readiness wait the Sentinel
  roll always had — this ADR makes it visible, it does not bound it. ADR 0010's
  bounded-wait rule covers the data-tier state machine; extending it to the
  Sentinel tier remains open. **Current rule:** past
  `spec.rollingUpdate.syncTimeout` the pod is named by
  `PodAvailabilityStalled=True/SentinelPodNotAvailable` and the pass continues
  to the status write; the wait itself still lasts until the pod comes up or
  the spec is fixed. Unbounded and unnamed still: a Sentinel pod that is
  *missing*, and the quorum wait of a tier of one or two Sentinels (*Residual
  risks*). *(Amended 2026-09-26 by D10: the small tier's quorum wait is no
  longer in that list — only the missing pod is.)*
- The Sentinel update waits for the data tier in every pass the data tier
  holds (D9), including a data pod wedged terminating on a NotReady node, which
  no spec caused. Nothing urgent waits — the old Sentinels keep running — but a
  consumer of `SentinelUpdateComplete` sees it that much later, the same kind of
  price D8 states.
- While a Sentinel stall is reported, each pass writes the phase twice:
  `Sentinel Rolling Update i/n` from `recordSentinelUpdateProgress`, then the
  verdict of `updateStatus` — `Provisioning` while a Sentinel is not Ready
  (`updateHAStatus`). The phase alternates and every stalled pass costs two
  status updates for as long as the stall lasts; the data-tier stalls already
  behave the same way. The `PodAvailabilityStalled` message names an instant
  rather than a running duration, so the condition itself is written once per
  stall.
- A tier of one or two Sentinels now has no automatic failover while each of
  its Sentinels is replaced (D10): a master lost in that window cannot be
  failed over before the replacement is available. Every such tier pays it once
  at the operator upgrade that ships ADR 0032, and again on every change that
  rolls the Sentinel tier — `spec.image`, which both tiers share, a Sentinel
  pod-spec change, a certificate rotation under TLS — changes the operator
  never delivered to it before.
- The operator now does to a two-Sentinel tier what that tier's
  PodDisruptionBudget refuses a node drain. With `spec.podDisruptionBudget`
  enabled, `minAvailable` is the quorum, 2, and the Eviction API refuses every
  eviction; the roll deletes (`deleteOwnedPod`) rather than evicts, so the
  budget does not hold it — the same as on the data tier. The difference from a
  drain is that the roll takes one Sentinel and waits for its replacement.
- The quorum wait of a small tier becomes a named observation. Before D10 it
  was reached even with every pod available, and then had nothing to name;
  now it is reached only while a pod is down, so a replacement that exists and
  never becomes available is reported as
  `PodAvailabilityStalled=True/SentinelPodNotAvailable` past `syncTimeout`,
  like on any other tier.

## Alternatives Considered

- **Move `RollingUpdateComplete` behind the Sentinel tier.** Passes are
  stateless, so "a roll was in flight" would have to be persisted past
  `clearRollingUpdateState` — either the data-tier state machine survives into
  the Sentinel tier, breaking the ADR 0010 invariant that the annotation's
  absence means no update in flight, or a second marker is introduced anyway,
  at which point this option contains the chosen one plus a semantic break: a
  Sentinel-only roll has no data-tier update and would emit a data-named event
  or nothing. Rejected.
- **Annotation-based edge, event only.** A progress annotation set on the first
  delete, cleared at convergence with the event. Honest, but it adds an
  annotation lifecycle to reason about (crash windows, Sentinel disabled
  mid-roll, foreign StatefulSet) and an event is an edge only — missable, not
  queryable afterwards, no metric. It duplicates memory the status can carry.
  Rejected: the chosen design is this option plus the level signal at
  essentially the same complexity.
- **Reuse `sentinelRolloutComplete` as the completion predicate.** Rejected per
  D3: revision-based and driver-based convergence can disagree, and a marker
  that waits for pods the driver will never replace never fires.
- **Keep releasing the Sentinel roll from a holding data tier** (ADR 0026 D5 as
  decided 2026-08-25), or release it for every stall except
  `PodAvailabilityStalled`. Rejected 2026-09-26 (D9): the first costs a healthy
  Sentinel and the spare vote whenever the data tier is stuck on a bad
  `spec.image`, which both tiers share; the second gives
  `DeferredRequeueAfter` two meanings for the sake of keeping the Sentinel
  update moving during a NotReady-node stall, which is not urgent.
- **Leave the Sentinel waits unbounded** and let a runbook cover a stuck
  Sentinel roll. Rejected 2026-09-26: the rootless change (ADR 0032) rolls the
  Sentinel tier of every cluster in a fleet at the operator upgrade, and a
  stuck roll there would freeze the status surface without naming the pod.
- **Keep refusing to roll a tier of one or two Sentinels, but report it** — a
  named, bounded refusal in place of the plain requeue, so the status is no
  longer frozen. Rejected 2026-09-26 (D10): it makes the refusal visible and
  converges nothing. Such a tier would still never receive a Sentinel image
  change, a rotated certificate or the rootless posture, and every one of those
  would stand reported on it for good — protecting a failover capacity the tier
  does not have during any single Sentinel failure either.
- **Leave it open** — the guard as it was, the unbounded, unnamed requeue.
  Rejected 2026-09-26 (D10): the ADR 0032 release rolls every Sentinel tier
  once, so every cluster with one or two Sentinels would sit at
  `Sentinel Rolling Update 0/n` with `SentinelUpdatePending=True` from the
  upgrade on, the pass ending before the status write, until someone deleted
  the pods by hand.

## Residual risks

- A crash after the last pod delete but before the True write loses the
  completion event for that roll: the condition stays consistent (never True),
  only the edge is missed. Accepted — the next spec change produces the next
  roll and marker.
- A foreign Sentinel StatefulSet is treated as absent (ADR 0020), so a roll
  interrupted by an ownership collision leaves the condition True until a
  legitimate StatefulSet converges. `reconcileSentinelStatefulSet` reports the
  collision; this path stays quiet by design.
- The event-ordering assertion (SentinelUpdateComplete not before
  RollingUpdateComplete) is verified by the extended `TestE2E_RollingUpdate_HA`;
  the unit tier verifies the condition lifecycle and the exactly-once emission
  against the fake client. Not verified: behaviour under a lagging informer
  cache — the exactly-once argument in D4 is reasoned from the resourceVersion
  precondition, not reproduced in a test.
- **The Sentinel-tier waits of D9 have unit coverage only; there is no e2e for
  them.** The reason: a Sentinel pod that never becomes available has no
  Sentinel-only way in through the CR. `spec.image` is shared with the data
  tier, whose stall now holds the Sentinel roll, and `SentinelSpec` has exactly
  `enabled`, `replicas`, `podLabels`, `podAnnotations`, `allowUnencrypted` and
  `disableAuth` — no field that could make a Sentinel pod unschedulable or
  unable to start on its own. The unit tier covers the unavailable outdated
  Sentinel as the target, a non-voting target replaced with the quorum already
  lost (`TestSentinelRollingUpdate_ReplacesANonVotingPodWhenQuorumIsAlreadyLost`),
  the quorum wait and the completion hold past the budget, the pod with the
  oldest clock named (`TestSentinelRollingUpdate_ReportsTheLongestUnavailablePod`),
  the first-sync clock (`TestPodNotReadySince_FirstSyncIsNotATransition`), a
  terminating pod in the hold, the retraction on evidence only — driven through
  the shared evaluator on a data report
  (`TestReportAvailabilityStall_RetractsOnlyOnEvidence`) and on the Sentinel
  tier when a terminating Sentinel takes priority in `sentinelWait`
  (`TestSentinelRollingUpdate_TerminationPriorityDoesNotRetractTheReport`) —
  the applied `DeferredRequeueAfter`, the hold of the Sentinel roll under a
  data availability stall and a data termination stall, both evaluators
  leaving the other tier's report alone, and the class-exit retraction
  ([`pod_availability_test.go`](../../internal/controller/pod_availability_test.go),
  `TestReconcileWorkload_StalledTerminationHoldsTheSentinelRoll` in
  [`pod_termination_test.go`](../../internal/controller/pod_termination_test.go)).
  The hold itself has an e2e:
  `TestE2E_RollingUpdate_UnavailableReplacementIsReportedAndReplaced` asserts
  the Sentinel pods' UIDs unchanged against the set taken before the image
  change, every 5 s across a 30 s window during the data stall and once more
  after the spec fix. It passed on 2026-09-26 on Kind (control plane and three
  workers, Kubernetes v1.36.1, containerd) as part of both full suites, run
  locally: `make test-e2e E2E_VALKEY_LINE=9` 51/51 and `E2E_VALKEY_LINE=8`
  51/51, each re-run green on the final image. Not in CI — ~~the branch has not
  been through the pipeline~~ *(corrected 2026-09-26: the branch was pushed as
  `e2ce8bb`, where two gate jobs failed, ADR 0017 D49; what that run's E2E legs
  reported is not recorded in this repository)*. *(Clarified 2026-09-26: these runs predate the two
  decisions taken after the T31/T32 commit `bb6c78f` — D10 here and the second
  roll of a migrated persistent data tier,
  [ADR 0032](0032-generated-pods-run-rootless.md) D2 — which changed
  `rolling_update.go`, changed `TestE2E_FleetUpgrade` and added
  `TestE2E_RollingUpdate_TwoSentinelsRollSerially`. ~~The suite has not been
  re-run since, and neither of those two e2e tests has run at all.~~)*
  *(Updated 2026-09-26: re-run on one operator image built from the final code
  of the branch — Kind, Kubernetes 1.36.1, containerd 2.3.1, runc 1.4.2, Linux
  6.10: both full suites green, 53/53 on Valkey 9 and 53/53 on Valkey 8, this
  e2e and `TestE2E_RollingUpdate_TwoSentinelsRollSerially` included, and
  `TestE2E_FleetUpgrade` green from 1.12.8. Locally, not in CI.)*
  **The argument covers the Sentinel-only fields, not the shared ones**, and
  one shared field does reach a Sentinel pod alone (read, not run):
  `spec.antiAffinity.mode: hard` is part of the Sentinel pod-spec hash and
  repels every Sentinel pod of the CR, so with `spec.sentinel.replicas` above
  the number of topology domains while the data tier fits into them (or has
  fewer than two replicas and therefore no term), switching to hard leaves a
  replacement Sentinel Pending on a converged data tier. An e2e along that
  line would fit the multi-node leg; it has not been written.
- **A Sentinel pod that is missing still waits unbounded.** The scan skips a
  NotFound pod, so the quorum wait and the completion hold have nothing to
  name and fall back to the plain requeue — T10's class, which ADR 0010 D16
  bounds for the data tier only. Open, not filed.
- **Closed 2026-09-26 by D10: a tier of one or two Sentinels could never
  replace a Ready outdated Sentinel.** Such a tier now rolls serially; what D10 leaves
  open is the next item. The finding as it was recorded, until the decision the
  same day, under the heading *Open, awaiting a decision*:
  `spec.sentinel.replicas` has
  `Minimum=1`, and with one or two Sentinels the quorum `replicas/2+1` equals
  the replica count, so the delete of every Ready pod fails
  `readyCount-cost < quorum`. The quorum wait then has nothing to name — no pod
  is terminating and none is unavailable — so `sentinelWait` falls through to
  the plain requeue: unbounded, the pass ends before the status write every
  time, and the status stands at `Sentinel Rolling Update i/n` with
  `SentinelUpdatePending=True` until something other than the operator deletes
  the pods. Pre-existing — the guard read `readyCount-1 < quorum` before D9 —
  and D9 leaves it as it was; what changes is the reach, because the rootless
  change ([ADR 0032](0032-generated-pods-run-rootless.md)) rolls every Sentinel
  tier once at the operator upgrade, so every such cluster hits it then. Every
  Sentinel tier on wds18-k8s-main has three Sentinels (checked read-only on
  2026-09-26). A non-voting target is not in this class: D9 replaces it even
  with the quorum lost. Read, not reproduced; no decision is taken here.
- **D10's serial roll is unit-verified; its e2e ~~has not been run~~ is green,
  locally and not in CI** *(updated 2026-09-26)*.
  `TestSentinelRollingUpdate_SmallTiersRollSerially` drives
  `checkAndHandleSentinelRollingUpdate` over one outdated Sentinel (replaced),
  two outdated Ready Sentinels (exactly one replaced) and two Sentinels of
  which the replacement is still booting (the other kept);
  `TestSentinelDeleteKeepsVotes` pins the arithmetic for three, two and one
  Sentinels. Mutation-checked on 2026-09-26 in `make test-unit` on a copy of
  the working tree: the old guard, `readyCount-cost >= quorum` for every size,
  replaces nothing and fails the first two rows of the first test and the
  second test, and nothing else; reverted, both pass.
  `TestE2E_RollingUpdate_TwoSentinelsRollSerially`
  ([`pod_availability_test.go`](../../test/e2e/pod_availability_test.go)) — two
  Sentinels, an image change, both Sentinel UIDs replaced,
  `SentinelUpdatePending=False`, phase `OK`, a key written before read back
  after — is written and ~~**not run yet**~~ green on Kind on both Valkey lines
  (2026-09-26: an earlier run, then inside both full suites on the final image
  of the branch); ~~the operator-log read of the two deletes that would show
  their order was not recorded, so the order still rests on the unit test~~
  *(read 2026-09-26 from the final run's operator log: per leg exactly two
  `Deleting sentinel pod for rolling update` lines, `two-sen-sentinel-0` then
  `two-sen-sentinel-1`, eight seconds apart in separate reconciles, on
  Valkey 9 and on Valkey 8. The log does not print the replacement's
  readiness, so that the second delete waited for it rests on
  `sentinelDeleteKeepsVotes`, read; the e2e itself still asserts no order)*. A
  tier of one Sentinel has no e2e.
  Not verified by any tier: that a two-Sentinel tier still fails over after its
  serial roll. The argument is ADR 0022 — a replacement keeps its `sentinel
  myid`, so the peer table a leader's majority is computed over does not grow
  with each replaced Sentinel — read, not run; the e2e kills no master after
  the roll. D10 has no reach on wds18-k8s-main, whose Sentinel tiers all have
  three Sentinels (checked read-only 2026-09-26).
- **The pass in which a data roll pauses runs the Sentinel roll.**
  `pauseRollingUpdate` returns an empty result, so `reconcileWorkload` passes
  no hold and the Sentinel roll runs in that pass, onto the spec the data tier
  paused on. The pause clears the rolling-update state, and the next pass
  waits out a fresh `syncTimeout` before pausing again (T23), so each pause
  releases one Sentinel pass, which deletes at most one pod. The reach is
  narrower than the one D9 closed: a pause is an available pod whose
  replication could not be confirmed within `syncTimeout`, not a pod that
  never comes up. Recorded in `handlePostRollingUpdateChecks`, not fixed; read,
  not run.
- **One condition, two tiers: a data report overwrites a standing Sentinel
  report.** `PodAvailabilityStalled` has one reason per tier but one slot. A
  Sentinel stall still standing when a later data roll stalls is replaced by
  the data report, and the Sentinel roll is held for as long as the data tier
  holds, so for that stretch the stuck Sentinel is named nowhere. The Sentinel
  report returns on the first Sentinel pass after the data tier finishes,
  immediately past the budget, because the stuck pod's clock is already old.
  Accepted.

## References

- [`internal/controller/rolling_update.go`](../../internal/controller/rolling_update.go) —
  `checkAndHandleSentinelRollingUpdate`, `dispatchSentinelRollingUpdate`,
  `finishSentinelRollingUpdate`, `sentinelWait`, `sentinelScan` (`observe`,
  `deleteTarget`), `recordSentinelUpdateProgress`, `sentinelUpdatePending`,
  `finalizeRollingUpdate`; D9: `availabilityWait`, `podNotReadySince`,
  `stampedAtFirstSync`, `firstSyncSlack`, `reportAvailabilityStall`,
  `expiredUnavailablePod`, `terminationWait`, `pauseRollingUpdate` (the
  exception to the hold); D10: `sentinelDeleteKeepsVotes` and its call in
  `dispatchSentinelRollingUpdate`
- [`internal/controller/valkey_controller.go`](../../internal/controller/valkey_controller.go) —
  `handlePostRollingUpdateChecks`, `runSentinelRollingUpdate`,
  `clearSentinelUpdatePending`, `writeStatusCondition`,
  `sentinelRolloutComplete`; D9: `reconcileWorkload`, `soonerRequeue`,
  `updateHAStatus`
- [`internal/controller/condition_registry.go`](../../internal/controller/condition_registry.go) —
  the `PodAvailabilityStalled` row (a level with two evaluators and an
  ownership rule) and the `PodTerminationStalled` clear sites
- [`internal/builder/sentinel.go`](../../internal/builder/sentinel.go) — both
  Sentinel containers take `spec.image`; `ComputeSentinelPodSpecHash`; D10:
  `SentinelQuorumFor` and the `sentinel monitor` quorum built from it
- D10: [`internal/builder/pdb.go`](../../internal/builder/pdb.go) —
  `BuildSentinelPodDisruptionBudget` (`minAvailable` is the quorum);
  [`internal/controller/foreign_object.go`](../../internal/controller/foreign_object.go) —
  `deleteOwnedPod` (a delete, not an eviction)
- [`api/v1/valkey_types.go`](../../api/v1/valkey_types.go) —
  `ConditionTypeSentinelUpdatePending`, `ValkeyPhaseSentinelRollingUpdate`;
  D9: `ConditionTypePodAvailabilityStalled`, `ReasonSentinelPodNotAvailable`,
  `ReasonPodAvailable`, `SentinelSpec` (its `Replicas` has `Minimum=1`)
- [`test/e2e/rolling_update_test.go`](../../test/e2e/rolling_update_test.go) —
  `TestE2E_RollingUpdate_HA`, subtest "Sentinel tier reports its own completion"
- D9: [`internal/controller/pod_availability_test.go`](../../internal/controller/pod_availability_test.go),
  [`internal/controller/pod_termination_test.go`](../../internal/controller/pod_termination_test.go)
  (`TestReconcileWorkload_StalledTerminationHoldsTheSentinelRoll`),
  [`test/e2e/pod_availability_test.go`](../../test/e2e/pod_availability_test.go)
  (`TestE2E_RollingUpdate_UnavailableReplacementIsReportedAndReplaced`,
  subtest "The Sentinel roll is held while the data tier holds")
- D10: [`internal/controller/pod_availability_test.go`](../../internal/controller/pod_availability_test.go)
  (`TestSentinelRollingUpdate_SmallTiersRollSerially`,
  `TestSentinelDeleteKeepsVotes`),
  [`test/e2e/pod_availability_test.go`](../../test/e2e/pod_availability_test.go)
  (`TestE2E_RollingUpdate_TwoSentinelsRollSerially`, ~~not run yet~~ green
  locally on both Valkey lines, 2026-09-26)
- Sibling ADRs: [0007](0007-failover-aware-rolling-update.md) (the data-tier
  roll), [0010](0010-every-rolling-update-wait-is-bounded.md) (why the state
  annotation must end at the data tier; D16 bounds the missing-pod wait on the
  data tier, D17 the availability wait),
  [0020](0020-write-only-what-the-operator-owns.md) (foreign objects are
  absent), [0021](0021-per-resource-metrics-and-the-alert-that-was-missing.md)
  (conditions export as metrics),
  [0022](0022-sentinel-identity-is-pinned-to-the-pod.md) (a replaced Sentinel
  keeps its identity, which D10's serial roll relies on),
  [0026](0026-a-pod-being-deleted-is-not-available.md) (D6: a terminating
  Sentinel is not converged, the counterpart of D8 here; D5: `terminationWait`; D11: the availability wait,
  the Sentinel target selection and the hold of the Sentinel roll — the primary
  home of D9), [0027](0027-conditions-are-levels-edges-or-history.md) (a level
  with two evaluators owes an ownership rule),
  [0030](0030-rotating-certificates-rotate-the-instances-that-cannot-reload-them.md)
  (a rotation reaches `valkey-sentinel` only through a roll — D10),
  [0032](0032-generated-pods-run-rootless.md) (the release that rolls every
  Sentinel tier of a fleet once)
