# ADR 0025: A Warning named split-brain means one that did not resolve itself

## Status

Accepted. Date: 2026-08-24.

Implemented: the `MultipleMasters` condition and the `splitBrainWarnAfter` bound
([`internal/controller/split_brain_report.go`](../../internal/controller/split_brain_report.go)),
the `DeletionTimestamp` guard on the role-label fallback (`labelClaimsMaster`,
[`internal/controller/rolling_update.go`](../../internal/controller/rolling_update.go)),
`SplitBrainResolved` as a Normal Event, the single report in
`verifyTopologyRestored`, and a default `fakeEventRecorder` on every test
reconciler.

Amends [ADR 0011](0011-evidence-based-steady-state-split-brain-resolution.md) D20 in place: the
shared `demoteRogueMaster` helper now emits `SplitBrainResolved` as Normal, which
the steady-state path inherits.

Open: nothing clears `MultipleMasters` outside a rolling update — see
[Residual risks](#residual-risks).

## Context

The 1.11.0 fleet rollout on wds18-k8s-main (2026-08-22 21:33 UTC) rolled eleven
Valkey clusters. All eleven succeeded: one master each, `master_link_status:up`
on every replica, byte-identical offsets, no timeout, no refused promotion. Six
of them emitted a Warning Event named `SplitBrainDetected` — "2 pods report
master role" — within about one second of the operator's *own* controlled
promotion.

A Warning named split-brain during a planned update trains operators to ignore
the one Warning that must never be ignored. Three separate facts produced it.

**Two masters during a controlled failover are the design, not a race.**
[ADR 0008](0008-known-master-annotation-is-the-recorded-authority.md) says it verbatim: the
promoted pod has taken `REPLICAOF NO ONE` and the outgoing master answers until
it terminates. There are ten such windows across the two topologies — with
Sentinel, the gap between Sentinel promoting the replica and reconfiguring the
old master, a half-completed failover in `failover-reset`, the Terminating
ex-master in `replacing-master`, a drain-handler failover; without Sentinel, the
in-pass promote-then-demote gap, a failed best-effort demotion, the Terminating
ex-master through the label fallback, a self-elected returning pod-0, and both
topology-restoration phases. In every one of them except two, an authority name
is available and is itself one of the reported masters.

**The report fired before the operator knew anything.** The Warning sat inside
`detectAndResolveSplitBrain`, four lines after the master count and *before* the
authority was consulted. It could not tell a designed window from an undesigned
one, because at that point it had not looked.

**A Terminating ex-master was manufactured back into a master by its own stale
label.** `collectPodStates` trusted `vko.gtrfc.com/instanceRole` whenever
`GetReplicationInfo` failed, and nothing clears that label at delete time: the
sidecar labeler polls on its own clock and the kubelet gives no ordering between
the two SIGTERMs ([ADR 0012](0012-the-sidecar-records-its-drain-promotion-on-the-pod.md)). So the
operator demoted the outgoing master exactly as intended, deleted it, the pod
stopped answering, and the label resurrected it as a second master.
`demoteRogueMaster` then refused it — a not-Ready pod cannot be demoted — while
the caller cleared `isMaster` locally anyway, so the Warning re-fired every pass
and no `SplitBrainResolved` ever closed it.

Two amplifiers rode on top. `SplitBrainResolved` was itself typed Warning
although it reports a repair that *succeeded*, so fixing only the detection site
would have left a Warning storm. And `verifyTopologyRestored` double-reported one
fact: `rogueCount > 0` and "more than one master" are the same predicate, so
`TopologyRestoreIncomplete` always arrived with `SplitBrainDetected` — two to
three Warnings per pass, every 10 s (`rollingUpdateRequeueDelay`), for up to
`finalizationStallTimeout` = 2 min, about 36 emissions for one incomplete
restore.

The event recorder does not damp any of this. `Recorder` is
`k8s.io/client-go/tools/events.EventRecorder` (`mgr.GetEventRecorder`,
[`cmd/main.go`](../../cmd/main.go)). Unlike the legacy `record` broadcaster it has no spam
filter and no rate limiter — only an isomorphic series cache keyed on
`(eventType, action, reason, reportingController, reportingInstance, regarding,
related)`. `recordEvent` passes `action == reason` and `related == nil`, so the
effective key is `(type, reason, CR)`; repeats within 6 minutes become a series
count, and **the note is not part of the key**, so only the first message
survives.

None of it was asserted anywhere. `SplitBrainDetected` and
`TopologyRestoreIncomplete` had zero assertions in any tier, and this was
mechanically guaranteed: `newTestReconciler` never set `Recorder`, and
`recordEvent` returns early on a nil recorder — so a test that did not opt in
could neither observe an Event nor fail on a new one.

## Decision

**D1. A Warning named split-brain means a split brain that nobody resolved.**
Not "the operator is performing a failover". The level — more than one pod
answering master — is carried by the `MultipleMasters` status condition; the
`SplitBrainDetected` Warning Event is the edge where that level outlived
`splitBrainWarnAfter`.

**D2. `detectAndResolveSplitBrain` reports nothing.** It is the resolution path
([ADR 0007](0007-failover-aware-rolling-update.md) D8, [ADR 0008](0008-known-master-annotation-is-the-recorded-authority.md)
D10/D11) and stays free of Events and status writes. The report belongs to
`resolveSplitBrain`, which wraps it and runs after the authority has been
applied. Resolution behaviour is unchanged by this ADR, and
`TestDetectAndResolveSplitBrain_*` standing unchanged is the proof.

**D3. `splitBrainWarnAfter` is 90 s, and it is chosen against durations, not
taste.** It is above every duration a legitimately outgoing master can occupy —
the data pod's 75 s `terminationGracePeriodSeconds` and the 60 s drain `preStop`
hook ([`internal/builder/statefulset.go`](../../internal/builder/statefulset.go)) — and below
`finalizationStallTimeout` = 2 min, so the operator cannot abandon a topology
restoration with rogue masters still present without the Warning having fired
first. Both ends are asserted by
`TestSplitBrainWarnAfterIsBoundedByTheDurationsItMustOutlive`. It joins the
existing 90 s family (`replicaReconnectTimeout`, `sentinelAwarenessTimeout`).

**D4. The bound has two copies, and neither is an annotation.** The durable copy
is the `LastTransitionTime` of `MultipleMasters`, a write the reporting pass
performs anyway; the in-memory copy is a `nudgeTracker` entry under
`boundMultipleMasters`. The condition wins when it stands, so a restarted
operator does not hand an unresolved split brain a fresh 90 s of silence; the
tracker answers whenever the status write never landed. That is the
[ADR 0010](0010-every-rolling-update-wait-is-bounded.md) D7/D8 discipline — a bound that can
silently fail to arm is not a bound — reached without a fourth annotation on the
CR.

**D5. The condition's *reason* is the memory of whether the Warning already
fired.** `MultipleMastersTransitional` is inside the bound;
`MultipleMastersPersisted` is past it, and the transition between them is the one
moment `SplitBrainDetected` is emitted. `meta.SetStatusCondition` keeps
`LastTransitionTime` while the status stays True, so changing the reason does not
reset the deadline it is measured from.

**D6. An unreachable pod that is being deleted is evidence of nothing.** The
`instanceRole` label may only be believed for a pod without a
`DeletionTimestamp` (`labelClaimsMaster`). An INFO-confirmed master still counts,
terminating or not — the guard drops the heuristic, never the answer, because a
master that still serves INFO still holds writes. This is
[ADR 0011](0011-evidence-based-steady-state-split-brain-resolution.md) D6 ("silence is not
evidence") applied to the rolling-update regime, and it is a correctness fix:
before it, the resolver resolved against a master that no longer existed and
`masterIdx` was dragged to the highest-ordinal master.

**D7. One fact, one Event; and a clean rolling update emits no Warning at all.**
`verifyTopologyRestored` owns `TopologyRestoreIncomplete` for the
more-than-one-master predicate and therefore calls the bare resolver, not
`resolveSplitBrain`. `SplitBrainResolved` is Normal — it reports a repair that
succeeded. A clean rolling update reads `RollingUpdate` ×n →
`FailoverTriggered` / `ManualFailover` → `RollingUpdateComplete` →
`SentinelUpdateComplete`, all Normal, on both topologies, and that is asserted by
an e2e subtest per topology rather than left as a property nobody checks.

**D8. Every test reconciler records Events.** `newTestReconciler` installs a
`fakeEventRecorder` by default, so a newly added Event can fail an assertion
instead of being dropped on a nil recorder. The recorder is mutex-guarded because
`findMaster` probes pods concurrently ([ADR 0019](0019-reconcile-concurrency-and-the-cost-of-a-stuck-pass.md)).

## Consequences

- **Up to 90 seconds in which a genuine split brain raises no Warning Event.**
  This is the price and it is the whole point. It is bounded on three sides: the
  resolver still demotes on every pass, `SplitBrainUnresolved` still reports a
  failed repair immediately ([ADR 0011](0011-evidence-based-steady-state-split-brain-resolution.md)),
  and `MultipleMasters` is True from the first pass. Only the Warning waits.
- **A status write per transition**, on a path that previously wrote none. Bounded
  by `writeStatusCondition`, which skips the update when the stored condition
  already matches in every field, so a steady double-master window writes once
  and then reads.
- **The condition is written at the resolver's call sites, never from inside it.**
  `writeStatusCondition` re-`Get`s the CR into `v`, which would drop unpersisted
  annotation edits if it ran deeper in the state machine. That constrains where
  `resolveSplitBrain` may be called; a future call site has to answer the same
  question.
- **"Who has two masters right now" is fleet-queryable for the first time.** The
  condition auto-exports as `vko_valkey_status_condition{condition="MultipleMasters"}`
  ([ADR 0021](0021-per-resource-metrics-and-the-alert-that-was-missing.md)), which closes a gap
  [ADR 0011](0011-evidence-based-steady-state-split-brain-resolution.md) admits in its own
  Consequences: "the check writes no status and sets no condition".
- **A default event recorder in unit tests changes what an existing test can
  see.** No existing assertion changed, but a test that reconciles now allocates
  and retains Events for the duration of the test.
- The `MultipleMasters` message names the pods and the authority, because the
  events API freezes the note of the first occurrence of a series — "2 pods
  report master role" is what an operator would have been left with for six
  minutes regardless of what happened next.

## Alternatives Considered

**Retype at the emission site: Normal when an authority names one of the
masters.** Move the report below the authority resolution and emit Normal
`MultipleMastersExpected` when `knownMaster` is among the masters. One function,
no new state. **Rejected on a verified fact:** in the Sentinel path the authority
comes from a live `SENTINEL MASTER` reply on *every* pass including
`replacing-replicas`, so a genuinely self-elected rogue during the replica phase
would be downgraded to Normal — and [ADR 0011](0011-evidence-based-steady-state-split-brain-resolution.md)
D2 skips the steady-state checker entirely with Sentinel enabled, so nothing else
would ever raise it. It also leaves `SplitBrainResolved` and the
`verifyTopologyRestored` double-report untouched.

**Gate on the rolling-update state.** Expected iff the state is one of
`failover-triggered`, `failover-reset`, `replacing-master`, `manual-failover`,
`restoring-topology`, `verifying-topology`, *and* the authority is among the
masters, *and* the count is exactly 2. Tighter than the previous option:
`replacing-replicas` and the empty state stay Warning, and those are the two
unexplained shapes. **Rejected** because it keys on the switch
[ADR 0008](0008-known-master-annotation-is-the-recorded-authority.md) D11 declares "the single
place that decision is made" — every future rolling-update state would have to
answer a second question there — and because it fixes only the reporting, leaving
the Terminating ex-master as a false input to the resolver.

**The `DeletionTimestamp` guard alone (D6 without the bound).** It removes the
biggest single contributor to the storm as a correctness improvement. **Rejected
as a half:** the genuinely designed windows remain — Sentinel promoting before it
demotes, and a failed best-effort demotion where the old master keeps answering
`INFO` for up to 60 s under the drain `preStop` hook.

**The bound alone (D1–D5 without D6).** **Rejected as the other half:** it leaves
the resolver resolving against a master that no longer exists, and `masterIdx`
dragged to the highest-ordinal master.

**Arming the bound with an annotation through `ensureWaitBound`.** Rejected: it
writes the CR from a path that has no other reason to, and the condition already
carries a persisted timestamp that is exactly the deadline. **Arming it in
process memory only** was rejected for the opposite reason — an operator restart
during a genuine split brain would restart the silence.

## Residual risks

- **Nothing clears `MultipleMasters` outside a rolling update.** The condition is
  written only by `resolveSplitBrain`, which runs only while a rolling update is
  in flight (`checkAndHandleRollingUpdate` returns early otherwise, the same
  dormancy [ADR 0011](0011-evidence-based-steady-state-split-brain-resolution.md) D1 describes).
  A rolling update that is abandoned with rogue masters still present therefore
  leaves the condition standing at True/`MultipleMastersPersisted`, and an
  administrator who then repairs the split brain by hand does not clear it; the
  next rolling update does. Accepted rather than papered over: writing False
  because nobody measured would be a worse answer than a stale True, and the
  Warning, `TopologyRestoreIncomplete` and `TopologyRestored=False` all survive
  independently. Closing it properly means a steady-state master count for
  Sentinel clusters, which is the gap
  [ADR 0011](0011-evidence-based-steady-state-split-brain-resolution.md) D2 already names.
- **A permanently failing status write re-warns.** If the condition never lands,
  the reason cannot remember that the Warning fired, so the in-memory bound
  re-emits on every pass. The events API series cache collapses those into one
  object with a count for 6 minutes, so the blast radius is a rising count, not a
  storm of objects. Not verified against a real API server; derived from the
  recorder's key, which is verified in code.
- **Two masters still cost data.** No master this operator builds refuses writes
  without a replica (`min-replicas-to-write` is set nowhere and has no CRD escape
  hatch), so both sides of a split accumulate writes that the repair then
  discards. This ADR changes what a Warning promises; it does not change what a
  divergence costs. Tracked separately as T12 in
  [`docs/tickets/local_neue_baustellen.md`](../tickets/local_neue_baustellen.md).
- **The e2e assertion is an absence.** "No Warning Event on the CR" fails on a
  genuinely degraded run as well as on a regression of this ADR, which is
  intended — but on a resource-starved CI node a legitimately slow topology
  restore would surface here as a `TopologyRestoreIncomplete` failure rather than
  as the timeout it is. The abandon path has its own test
  (`TestE2E_RollingUpdate_TopologyRestoreAbandoned`), so the happy-path tests are
  not expected to reach it.
- **The ten designed windows are enumerated from a code read, not measured.** Six
  of them were observed on wds18 on 2026-08-22; the rest are derived from the
  state machine.

## References

- [`internal/controller/split_brain_report.go`](../../internal/controller/split_brain_report.go) —
  `resolveSplitBrain`, `reportMultipleMasters`, `clearMultipleMasters`,
  `splitBrainWarnAfter`, `boundMultipleMasters`.
- [`internal/controller/rolling_update.go`](../../internal/controller/rolling_update.go) —
  `detectAndResolveSplitBrain` (reports nothing), `labelClaimsMaster`,
  `collectPodStates`, `demoteRogueMaster`, `verifyTopologyRestored`,
  `forgetWaitBounds`.
- [`api/v1/valkey_types.go`](../../api/v1/valkey_types.go) —
  `ConditionTypeMultipleMasters`, `ReasonMultipleMastersTransitional`,
  `ReasonMultipleMastersPersisted`, `ReasonSingleMaster`.
- [`internal/controller/split_brain_report_test.go`](../../internal/controller/split_brain_report_test.go) —
  the unit tier for every decision above.
- [`test/e2e/rolling_update_test.go`](../../test/e2e/rolling_update_test.go),
  [`test/e2e/pdb_test.go`](../../test/e2e/pdb_test.go) — the per-topology
  "raised no Warning" subtests and `requireNoWarningEvents`.
- [ADR 0007](0007-failover-aware-rolling-update.md) D8 and
  [ADR 0008](0008-known-master-annotation-is-the-recorded-authority.md) D10/D11 — the resolution
  behaviour this ADR does not touch.
- [ADR 0010](0010-every-rolling-update-wait-is-bounded.md) D7/D8 — the two-copy discipline for a
  bound.
- [ADR 0011](0011-evidence-based-steady-state-split-brain-resolution.md) D6, D20 — silence is not
  evidence; the shared demotion helper, amended here.
- [ADR 0026](0026-a-pod-being-deleted-is-not-available.md) D2, D5 — the carve-out that keeps
  `demoteRogueMaster` reachable for a terminating master, and the reason the new termination
  waits report through a condition and never through an Event.
- [ADR 0012](0012-the-sidecar-records-its-drain-promotion-on-the-pod.md) — why the role label
  outlives the pod.
- [ADR 0021](0021-per-resource-metrics-and-the-alert-that-was-missing.md) — how the condition
  becomes a metric.
- [`docs/tickets/local_neue_baustellen.md`](../tickets/local_neue_baustellen.md) — T4, the
  finding and the option analysis behind this ADR; T5, the matching
  `DeletionTimestamp` guard in the replace-candidate selection.
