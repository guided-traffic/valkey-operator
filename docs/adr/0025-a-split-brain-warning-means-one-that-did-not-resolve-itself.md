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

Amended 2026-09-26 (`feat/rootless`, measured before it was decided): **D9 — while a Sentinel
failover the roll itself requested is in flight, the double master is reported and not
resolved.** Implemented in `resolveSplitBrainUnlessFailingOver`
([`rolling_update.go`](../../internal/controller/rolling_update.go)); unit-tested
(`TestHandleRollingUpdate_DoesNotDemoteTheReplicaSentinelIsPromoting`, with a positive control in
`failover-reset`; the mutation that calls `resolveSplitBrain` unconditionally is killed). The bug
predates this amendment and was on `main`; decided by Hans on 2026-09-26. Run end to end the same
day, locally on Kind and not in CI, on one operator image built from ~~the final code~~ the first
version of D9, before its clock *(corrected 2026-09-26, the amendment below)*: both full
e2e suites green (53/53 on Valkey 9, 53/53 on Valkey 8) and two further Valkey 8 runs of the test
that found it green; the operator log of those runs shows no demotion during the roll's failover
(*Residual risks*).

Amended again 2026-09-26: **D9 carries its own clock** (`ownFailoverInFlight`: 90 s from the
failover timestamp), and the two trigger sites write the state and that timestamp in one update
(`setFailoverTriggered`, [ADR 0010](0010-every-rolling-update-wait-is-bounded.md) D14). The unit
test gained a row past the window and a row without a timestamp; three mutations — no guard, no
clock, no timestamp check — are killed, and `TestHandleRollingUpdate_ArmsTheFailoverStateWithItsTimestamp`
kills the split write. Run on one image built from that code, same Kind stack, not in CI: the
fleet-upgrade e2e from 1.12.8 green, the full suite 53/53 on Valkey 8 and 52/53 on Valkey 9, and
two further Valkey 8 runs of the hardening and restricted-namespace e2e green. The one failure,
`TestE2E_SidecarFailoverDrainMaster`, is a fixture defect outside D9: it deletes a master of a
cluster with no roll in flight, and the operator log of that cluster shows no operator action
between its creation and its deletion *(that it is outside D9 is read from the test, which
changes no spec; that the fixture caused it is a diagnosis from the test code and its timing,
not traced in the failed run — ADR 0017 D50; noted 2026-09-26)*. Fixed afterwards, green 8 of 8
on Valkey 9 ([ADR 0017](0017-test-and-ci-policy.md) D50). No trigger or demotion count is
recorded from that run's log. The clock closes the residual risk of the unbounded
`verifyNewMasterReady` wait holding the resolver off *(and opens a narrower one, noted
2026-09-26: Sentinel can still be reconfiguring replicas when the clock runs out — Residual
risks)*.

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
`TestDetectAndResolveSplitBrain_*` standing unchanged is the proof. *(Narrowed 2026-09-26 by D9:
`detectAndResolveSplitBrain` itself is still unchanged, but the Sentinel rolling update no longer
calls it in `failover-triggered`, so in that one state this ADR does change whether the resolver
runs *(within 90 s of the failover timestamp, since D9's clock, the same day)*.)*

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

**D9. While the roll's own Sentinel failover is in flight, the double master is reported, not
resolved.** *(Added 2026-09-26.)* In `stateFailoverTriggered` *(and, since the same day, only
while its failover timestamp is younger than 90 s — the clock below)* the Sentinel rolling update calls
`reportMultipleMasters` and skips `detectAndResolveSplitBrain`. Sentinel promotes its candidate
first and moves its master pointer only at `+switch-master`, so for that window the promoted
replica answers master while the authority the resolver reads (`getSentinelMasterPodName`) still
names the old one — resolving then demotes exactly the replica the operator asked Sentinel to
promote. **Measured** on Kind (2026-09-26, Valkey 8, a Sentinel cluster with the observer): the
observer turned unready during the failover, its Deployment status re-entered the pass one second
after the trigger (`Owns(&appsv1.Deployment{})`; the trigger is inferred from the timing and the
correlation — only the two observer-enabled clusters of the run saw it — not traced), the resolver
demoted the promotion, Sentinel timed out, and the reset-and-retrigger cycle ~~ran for more than ten
minutes; on Valkey 9 the same cluster won the race after eleven cycles~~ *(corrected 2026-09-26
against the operator log of that run: on Valkey 8 it ran ten cycles — ten triggers, ten demotions
of the promoted replica `hard-1` with `hard-0` as the authority, nine timeouts — from the first
trigger until the test's ten-minute wait gave up, about nine and a half minutes later. On Valkey 9
the same cluster saw one trigger and one demotion two seconds after it, of the outgoing master
`hard-0` with `hard-1` as the authority — that pass landed after Sentinel had moved its pointer —
and the failover completed with no timeout. The eleven triggers, eleven demotions and nine
timeouts under Residual risks are both legs together)*. ~~The window stays bounded:
a failover that does not complete within `failoverRetryTimeout` is handed to
`stateFailoverReset`, which reconfigures Sentinel to the old master, and the next pass resolves as
before.~~ *(Corrected 2026-09-26, read in `handlePostFailover`, not measured: that sentence covers
only the branch in which no new-image master is found. In the double master of this decision the
promoted replica answers master, and once it is available `handlePostFailover` takes it as the
new master. With a connected replica, `replaceRemainingPods` sets `replacing-master` — where the
resolver runs again — right before it deletes the old master, once `verifyNewMasterReady` passes
and no pod of the tier is terminating. With no connected replica, `replicaReconnectTimeout` (90 s
from the failover timestamp) sends a best-effort `REPLICAOF` of the new master to every other
reachable pod, the old master included (`handleMasterWithNoReplicas`, `forceReplicaConnections`).
With no new-image master, `failoverRetryTimeout` (30 s) hands the failover to
`stateFailoverReset`, which resets Sentinel onto the pod answering master, and the next pass
resolves as before. One wait of the connected-replica branch has no bound — Residual risks.)*
*(Amended again 2026-09-26: **the window carries its own clock.**)* It is
`ownFailoverInFlight`: `stateFailoverTriggered` **and** a failover timestamp younger than
`replicaReconnectTimeout` (90 s). Past that, resolution resumes in the same state — Sentinel moves
its pointer within seconds of the promotion or gives the failover up, so the authority is settled
either way *(the operator configures Sentinel's `failover-timeout` as 60 s,
`SentinelFailoverTimeout`, below the clock — read; that Sentinel has switched or aborted by then
is Sentinel's own behaviour at that timeout, not measured here)* *(qualified 2026-09-26, read
in Valkey's `src/sentinel.c` on `unstable` at `dca022f`, not in the 8.x or 9.x release sources:
`failover-timeout` bounds two phases separately — the wait for the promoted replica to
acknowledge (`sentinelFailoverWaitPromotion`) and the reconfiguration of the other replicas,
which ends only when each reports its link to the new master up or the timeout passes
(`sentinelFailoverDetectEnd`) — and `+switch-master` comes after the second. "Within seconds"
is the case of replicas that resynchronise within seconds, and 60 s below 90 s does not by
itself settle the authority by the clock — Residual risks)* — which bounds the window
independently of the post-failover handler and closes the
unbounded `verifyNewMasterReady` branch below. A timeout of the no-replica branch rewrites the
timestamp ~~(at most `maxReconnectResets` times)~~ *(corrected 2026-09-26, read:
`maxReconnectResets` times in a row; the pass that reaches the cap clears the count, so a new
master that still has no connected replica starts it over, with no overall cap. Each such
rewrite runs in a pass whose resolver read the same stamp milliseconds earlier —
`resolveSplitBrainUnlessFailingOver` runs before the post-failover handler — so, short of a pass
landing on the 90 s boundary, the resolver has run once before the window re-opens)*, re-opening the
window by 90 s each; so does the
no-master timeout when its second write fails (Residual risks), and each retrigger from
`failover-reset` is a new failover with a window of its own. A state without
a timestamp — written by an earlier operator, or left by a failed second write before
`setFailoverTriggered` wrote both in one update ([ADR 0010](0010-every-rolling-update-wait-is-bounded.md)
D14) — is no window and resolves as before. ~~Unit rows for all four cases;~~ *(corrected
2026-09-26: four unit rows — just armed (no demotion), `failover-reset` (a demotion, the
positive control), past the window and without a timestamp (a demotion each); no row rewrites
the timestamp)* the three mutations (no guard, no clock, no timestamp check) are killed.
D1 still holds — the level is True from the first pass, and the Warning waits for
`splitBrainWarnAfter`.

## Consequences

- **Up to 90 seconds in which a genuine split brain raises no Warning Event.**
  This is the price and it is the whole point. It is bounded on three sides: the
  resolver still demotes on every pass *(every pass outside the roll's own Sentinel failover
  since 2026-09-26, D9)*, `SplitBrainUnresolved` still reports a
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

- **(D9) For up to ~~`failoverRetryTimeout`~~ `replicaReconnectTimeout` (90 s) two pods may
  accept writes during the roll's own Sentinel failover** *(corrected 2026-09-26 with D9's bound:
  30 s is the branch without a new-image master; with one that has no connected replica the
  forced `REPLICAOF` comes at 90 s; read, not measured; ~~the branch with a connected replica has
  one wait without a bound, Residual risks~~ *(amended again 2026-09-26: D9's own clock hands
  every branch back to the resolver at 90 s from the failover timestamp, the connected-replica
  branch included; a rewrite of that timestamp re-opens it, D9)*)*, both labelled master, with nothing
  demoting either. That window is the
  one every Sentinel failover has; before D9 the operator shortened it by demoting the promoted
  replica, which is what broke the failover. Writes that reach the old master after the promotion
  are lost when Sentinel reconfigures it — as they were before, for whichever pod was demoted.

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

*(Added 2026-09-26, for D9.)*

- **Name the promotion candidate as the authority during the failover.** The operator does not
  choose it — Sentinel's leader does — so the operator would have to guess, which is the rule-3
  shape ADR 0011 refuses.
- **A grace period after the trigger instead of the state.** A time bound would have to be
  longer than Sentinel's failover to be safe ~~and shorter than `failoverRetryTimeout` to add
  anything~~ *(struck 2026-09-26, see the amendment at the end of this item)*; the state already
  carries exactly that window and is bounded by the timeout
  *(by the timeouts of each branch, D9 as corrected 2026-09-26)*. *(Amended again 2026-09-26:
  a time bound is now part of D9, on top of the state rather than instead of it. The state still
  names the window; the 90 s clock only caps it, because one branch's exit has no bound. So the
  bound is not shorter than `failoverRetryTimeout`, and the "to add anything" half above was
  wrong — what it adds is the cap on the branch that has none. The "longer than Sentinel's
  failover" half still holds~~: 90 s is above the 60 s `failover-timeout` the operator configures~~
  *(struck 2026-09-26: as a requirement it holds, but 90 s is not shown to meet it by being above
  60 s — Sentinel applies `failover-timeout` to its promotion wait and its replica
  reconfiguration separately, D9 as qualified, Residual risks)*.)*
- **Stop re-entering the pass on observer Deployment status.** It removes the one trigger that
  was measured, not the race: any other event inside the window — a StatefulSet status change, a
  requeue — lands in the same place.

## Residual risks

- ~~**(D9) One branch of the window has an unbounded wait, by reading**~~ *(closed 2026-09-26, the
  same day: D9 now carries its own 90 s clock, so the wait below no longer holds the resolver
  off; the wait itself is unchanged and pre-existing)*. *(added 2026-09-26)*. With
  a new-image master that has a connected replica, the state leaves `failover-triggered` only
  once `verifyNewMasterReady` passes, and that function's plain requeues — the new
  master's sync flag, an unreadable `DBSIZE`, a TLS configuration that cannot be built — carry no
  bound (its own comment calls its final requeue "this function's unbounded requeue"). While one
  of them holds and the old master still answers master, nothing in the operator resolves the
  double master; Sentinel reconfiguring the old master is what ends it. Read in code; no run has reached it, and
  whether it is reachable with a Sentinel that has already reconfigured a replica was not traced.
- **(D9) A rewrite of the failover timestamp re-opens the window.** *(Added 2026-09-26, read.)*
  The no-replica timeout does so by design, ~~at most `maxReconnectResets` times~~ *(corrected
  2026-09-26, read: `maxReconnectResets` times in a row, and the pass that reaches the cap clears
  the count, so it can start over while the new master has no connected replica; the resolver
  runs earlier in the pass that rewrites, D9)*, in the pass that
  also sends the forced `REPLICAOF`. The no-master timeout, `handleNoMasterFound`, writes the
  timestamp and then `failover-reset` in two updates; when the second fails, `failover-triggered`
  stands with a fresh timestamp and the resolver stays withheld for another 90 s, once per such
  failure *(in a pass that came from `failover-triggered`; `handlePostFailover` also serves
  `replacing-master`, where the state that stands is not D9's — added 2026-09-26)*.
  `TestHandleNoMasterFound_SurfacesTheStateWriteFailure` pins the state standing, not the
  timestamp. The single-write rule of [ADR 0010](0010-every-rolling-update-wait-is-bounded.md) D14
  has not been applied there.
- **(D9) The clock can run out while Sentinel is still reconfiguring.** *(Added 2026-09-26, read
  in Valkey's `src/sentinel.c` on `unstable` at `dca022f`, not in the 8.x or 9.x release sources;
  not measured.)* Sentinel moves its pointer (`+switch-master`) only once every reachable replica
  reports its link to the promoted one up, or `failover-timeout` has passed since the promotion
  was acknowledged — and the acknowledgement has a `failover-timeout` of its own. With the 60 s the
  operator configures, a promotion acknowledged late or a replica that resynchronises slowly can
  keep the pointer on the old master past 90 s from the trigger. From then on the resolver runs
  again and can demote the promoted replica, the pre-D9 behaviour, confined to that tail. No run
  has reached it.
- **(D9) The e2e evidence is a rerun, not a dedicated test.** The measurement that found it is
  `TestE2E_PodHardening_UserNamespacesLocalhostSeccompAndDigest` on Valkey 8; ~~with D9 the
  suites and that test are rerun (result recorded in the T31 ticket)~~ *(rerun 2026-09-26, locally
  on Kind — Kubernetes 1.36.1, containerd 2.3.1, runc 1.4.2, Linux 6.10 — on one operator image
  built from ~~the final code~~ the first version of D9, before its clock *(corrected
  2026-09-26)*, D9 included: both full suites green, 53/53 on Valkey 9 and 53/53 on
  Valkey 8, and two further Valkey 8 runs of that test ~~alone~~ outside the suites, together
  with `TestE2E_PodSecurity_RestrictedNamespace` (corrected 2026-09-26), green. The operator log of those runs
  shows, for its observer-enabled Sentinel cluster `hard`, four Sentinel failover triggers — one
  per run of the test — zero demotions and zero failover timeouts; the run before D9 logged eleven
  triggers, eleven demotions and nine timeouts, and its Valkey 8 leg was red. Not in CI.)*
  *(Split per leg and checked against the operator log of both runs, 2026-09-26: the eleven,
  eleven and nine are ten, ten and nine on Valkey 8 plus one trigger and one benign demotion of
  the outgoing master on Valkey 9 — D9. In ~~the final run~~ the run before the clock the only other demotion during a
  Sentinel roll, on `rl-ha` of `TestE2E_PodSecurity_RestrictedNamespace` (Valkey 9 leg), fell in
  `replacing-master`, after the new master had been verified, and demoted `rl-ha-2` in favour of
  Sentinel's master `rl-ha-1` — outside D9's state; no resolver demotion (`Demoting rogue master
  to replica`) in that log falls in `failover-triggered`.)* *(Rerun again 2026-09-26 on one
  image with D9's clock and the single failover write, same Kind stack: the full suite 53/53 on
  Valkey 8 and 52/53 on Valkey 9 — the one failure `TestE2E_SidecarFailoverDrainMaster`, a
  fixture defect outside D9 (Status) — and two further Valkey 8 runs of that test with
  `TestE2E_PodSecurity_RestrictedNamespace` green. No trigger, demotion or timeout count is
  recorded from that run's log, so the zero demotions above stand for the run before the
  clock only.)* No e2e
  reproduces the window deterministically — the trigger was an observer readiness flip, inferred,
  not traced — so the rerun shows the cycle gone on the runs made, not that the window was
  reached on each of them.
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
  `forgetWaitBounds`; D9: `resolveSplitBrainUnlessFailingOver`, called from
  `handleRollingUpdate`, its clock `ownFailoverInFlight`, and `setFailoverTriggered`.
- [`internal/controller/split_brain_failover_test.go`](../../internal/controller/split_brain_failover_test.go) —
  D9's unit test, `TestHandleRollingUpdate_DoesNotDemoteTheReplicaSentinelIsPromoting`, and
  `TestHandleRollingUpdate_ArmsTheFailoverStateWithItsTimestamp`.
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
  behaviour this ADR does not touch *(except D9's one state, where the Sentinel path does not
  call it; added 2026-09-26)*.
- [ADR 0028](0028-a-demotion-may-not-discard-the-only-dataset.md) D1, D8 — the dataset veto and
  the bounded states the resolver runs in; D9 withholds the resolver in `failover-triggered` on
  the Sentinel path *(for 90 s from the failover timestamp since D9's clock, 2026-09-26)*, so no
  resolver rule, the veto included, runs there.
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
