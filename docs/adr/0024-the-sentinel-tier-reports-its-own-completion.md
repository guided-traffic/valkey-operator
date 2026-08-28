# ADR 0024: The Sentinel tier reports its own completion

## Status

Accepted, amended 2026-08-25 (D3, see D8). Date: 2026-08-23.

Implemented: the `SentinelUpdatePending` condition, the `SentinelUpdateComplete`
event, the `Sentinel Rolling Update i/n` phase, the reworded
`RollingUpdateComplete` message, and the clear on a CR whose Sentinel was
disabled mid-roll. Amended 2026-08-25: convergence additionally excludes a
Sentinel pod that is being deleted (D8).

## Context

The rolling update runs in two tiers, and the ordering is structural:
`reconcileWorkload` drives the data-tier rolling update first, and the Sentinel
check (`checkAndHandleSentinelRollingUpdate`) runs only in a pass where the data
tier neither errored nor requeued. Both emission sites of the
`RollingUpdateComplete` event — `finalizeRollingUpdate` and
`verifyTopologyRestored` — belong to the data tier, and both clear the
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
  not change.
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
- **D6 — Disabling is not completing.** A CR whose Sentinel is disabled while
  the condition stands gets it cleared (reason `SentinelDisabled`) on the
  non-Sentinel path of `handlePostRollingUpdateChecks`, with no completion
  event. The clear is presence-guarded, like `clearSidecarUpdatePending`, so no
  condition is ever created on a CR that never carried one.

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
  costs no API call; the progress steps cost one status update each.
- A cluster with the condition True and a Sentinel pod that never becomes Ready
  requeues with `Sentinel Rolling Update i/n` standing indefinitely. That is the
  same unbounded quorum/readiness wait the Sentinel roll always had — this ADR
  makes it visible, it does not bound it. ADR 0010's bounded-wait rule covers
  the data-tier state machine; extending it to the Sentinel tier remains open.

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

## References

- [`internal/controller/rolling_update.go`](../../internal/controller/rolling_update.go) —
  `checkAndHandleSentinelRollingUpdate`, `finishSentinelRollingUpdate`,
  `recordSentinelUpdateProgress`, `sentinelUpdatePending`, `finalizeRollingUpdate`
- [`internal/controller/valkey_controller.go`](../../internal/controller/valkey_controller.go) —
  `handlePostRollingUpdateChecks`, `clearSentinelUpdatePending`,
  `writeStatusCondition`, `sentinelRolloutComplete`
- [`api/v1/valkey_types.go`](../../api/v1/valkey_types.go) —
  `ConditionTypeSentinelUpdatePending`, `ValkeyPhaseSentinelRollingUpdate`
- [`test/e2e/rolling_update_test.go`](../../test/e2e/rolling_update_test.go) —
  `TestE2E_RollingUpdate_HA`, subtest "Sentinel tier reports its own completion"
- Sibling ADRs: [0007](0007-failover-aware-rolling-update.md) (the data-tier
  roll), [0010](0010-every-rolling-update-wait-is-bounded.md) (why the state
  annotation must end at the data tier),
  [0020](0020-write-only-what-the-operator-owns.md) (foreign objects are
  absent), [0021](0021-per-resource-metrics-and-the-alert-that-was-missing.md)
  (conditions export as metrics),
  [0026](0026-a-pod-being-deleted-is-not-available.md) (D8: a terminating
  Sentinel is not converged)
