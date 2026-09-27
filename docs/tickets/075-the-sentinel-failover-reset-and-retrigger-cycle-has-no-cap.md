---
id: T75
title: The Sentinel failover's reset-and-retrigger cycle has no cap, and maxReconnectResets restarts instead of capping
state: analysed       # both loops read step by step, the Sentinel side measured on both pinned images
severity: medium      # each cycle repeats a tier-wide Sentinel reset and a failover without end or report; the known drivers need a concurrent failure
security: none        # no principal and no security guarantee; an availability and convergence defect of the rolling update
urgency: next         # rule 3: severity medium, trigger live and reachable without any option
effort: M             # counter, hold, level condition, tests, ADR 0010 D18, operations docs
blocked-by: decision  # Q1; the comment and ADR 0010 corrections are not blocked
filed-from: T62
opened: 2026-09-27
decided:
done:
---

# T75 - The Sentinel failover's reset-and-retrigger cycle has no cap, and maxReconnectResets restarts instead of capping

## Current state

Every Sentinel-enabled data-tier roll goes through `handleRollingUpdate`
([`rolling_update.go:321-323`](../../internal/controller/rolling_update.go#L321-L323); all
locations below are in `internal/controller/rolling_update.go` unless named). Its failover can
cycle without limit in two loops.

**Loop A - no new master.**

1. `stateFailoverTriggered`: `handlePostFailover`
   ([`:3071-3114`](../../internal/controller/rolling_update.go#L3071-L3114)) looks for a pod on
   the current template that is `available()` and answers `role:master`. If none does,
   `handleNoMasterFound` ([`:3287-3326`](../../internal/controller/rolling_update.go#L3287-L3326))
   waits `failoverRetryTimeout` (30 s, `:143`), then resets every Sentinel (`resetSentinelState`,
   `:3308`, toward the first pod the scan marked master, else pod-0), clears the awareness bound,
   rewrites the failover timestamp and enters `stateFailoverReset`.
2. `stateFailoverReset`: `handleFailoverRetrigger`
   ([`:849-887`](../../internal/controller/rolling_update.go#L849-L887)) waits
   `failoverResetMinWait` (20 s), then for Sentinel to know `spec.replicas - 1` replicas (bounded
   by a fresh 90 s `sentinelAwarenessTimeout`), then calls `setFailoverTriggered` and
   `triggerSentinelFailover` (`:3700-3740`), which tries each Sentinel until one accepts; a refusal
   by all is only logged. Back to 1.

One cycle takes about 50 s, up to about 2.5 min when the awareness wait runs out. The retrigger
runs without the first trigger's `waitForReplicasReady` and `waitForWriteSync` gates
(`:2710-2719`).

**Loop B - a new master with no connected replica.** `handleMasterWithNoReplicas`
([`:3145-3187`](../../internal/controller/rolling_update.go#L3145-L3187)) waits 90 s
(`replicaReconnectTimeout`) since the failover timestamp, then sends `REPLICAOF` to every other
reachable pod (`forceReplicaConnections`, `:3159`) and resets every Sentinel (`:3161`), and only
then checks `maxReconnectResets` (2, `:138`). Below the cap it increments the count and rewrites
the timestamp in one update (`incrementReconnectResetCount`, `:3244-3258`). At the cap it clears
the count (`:3170`) without rewriting the timestamp and calls `replaceRemainingPods`, whose
`verifyNewMasterReady` requeues while `connected_slaves` is 0 (`:3349-3353`). The next pass, 10 s
later, finds count 0 and a stale timestamp and resets at once. One round is three resets and three
rounds of `REPLICAOF` at about 0 s, 90 s and 180 s, repeated for as long as the master has no
connected replica.

**Nothing caps either loop.** No counter or episode deadline exists for loop A (the roll state
removed by `clearRollingUpdateState`, `:3447-3470`, has none); `maxReconnectResets` is read only
at `:3163` and counts within one round. `spec.rollingUpdate.syncTimeout` does not bound the
failover. The exits are: an available current master (`handleNewMasterFound`, `:3119`); every pod
current and reachable (`finalizeRollingUpdate`, `:718-723`); a spec change that drops the state
(`clearStaleRollingUpdateState`, `:833-845`); or a human removing the state annotation.

**Nothing reports it.** The phase stays `Rolling Update k/n` (`:726-727`); every handler returns
`NeedsRequeue`, so the pass ends before the status write
([`valkey_controller.go:340-342`](../../internal/controller/valkey_controller.go#L340-L342)) and
`Ready` keeps its last value. No condition, no Event after the first `FailoverTriggered`; only
default-level log lines. The `ValkeyPhaseNotOK` alert (`for: 30m`) ships in a PrometheusRule that
is off by default.

**The comments claim a cap that does not exist:**
[`:56-58`](../../internal/controller/rolling_update.go#L56-L58) ("Used to break the infinite
loop"), [`:135-137`](../../internal/controller/rolling_update.go#L135-L137) ("proceed with the
rolling update regardless"),
[`:3142-3144`](../../internal/controller/rolling_update.go#L3142-L3144) ("breaking the infinite
retry loop"),
[`sentinel_failover_test.go:1279-1281`](../../internal/controller/sentinel_failover_test.go#L1279-L1281)
("stops resetting"), and `:3019-3022` in `replaceRemainingPods`. The accurate wording is the doc
comment at `:789-792`. The test pins only the cap pass; no test drives the pass after it.
`:90-94`, `:1369-1373` and `:2721-2725` describe the cycle as the recovery path for `NOGOODSLAVE`.

**Sentinel does not stop the retrigger.** A forced `SENTINEL FAILOVER` is refused only with
`-INPROG` (per Sentinel; the operator moves to the next one) or `-NOGOODSLAVE`; three forced
failovers 12 to 20 s apart all answered `OK` (T62). Measured on `valkey/valkey:9.1.1` and `8.1.9`,
identical results, three Sentinels with the operator's `down-after-milliseconds` and
`failover-timeout`:

- `replica-priority 0` on every replica: `SENTINEL FAILOVER` answers `NOGOODSLAVE No suitable
  replica to promote` on each Sentinel; after `replica-priority 100` it answers `OK`. The operator
  never sets `replica-priority`, so this is a fixture, not a production trigger.
- No replica at all: every forced failover answers `NOGOODSLAVE`, again 20 s later.

**Drivers (by reading).** Once the cycle runs, an old master that becomes unreachable while its
pod stays (a NotReady node) is marked master by its label, and `:3308` resets every Sentinel
toward it; T62 measured that state as every Sentinel at `s_down` with 0 replicas and 0 peers,
refusing every failover with `NOGOODSLAVE`, so the cycle sustains itself until the node returns.
Other possible drivers: a promoted current pod that answers `role:master` but is not `available()`
or whose INFO is unreadable; a new master whose replicas cannot connect (loop B). ADR 0025 D9
records a measured run, on a driver D9 removed, of ten triggers and ten demotions of the promoted
replica in about 9.5 min without recovery.

**Impact.** Every Sentinel-enabled cluster whose roll failover yields no usable current master:

- The roll never completes and never says so.
- Loop A resets the whole Sentinel tier toward an unverified address about once a minute and
  sends a new, ungated failover each cycle (T73's route to an empty promotion); loop B sends
  `REPLICAOF` to every other reachable pod three times per round (T73).
- Loop B re-opens ADR 0025 D9's 90 s report-only split-brain window on every timestamp rewrite.
- No option touches a pod template, so nothing here rolls the fleet.

## Required changes

**Independent of the open questions**

1. Correct the comments at `:56-58`, `:135-137`, `:3142-3144` and
   `sentinel_failover_test.go:1279-1281` to what the code does: the count caps consecutive resets
   within one round, the cap pass clears it without rewriting the timestamp, and the next round
   starts at once (the wording of `:789-792`).
2. [ADR 0010](../adr/0010-every-rolling-update-wait-is-bounded.md) Residual risks (`:775-784`):
   state that the restart after the cap is immediate and one round is three resets; replace
   "Not filed." without a ticket id (ADR 0034).

**Depends on the answers**

3. An episode counter annotation (example `vko.gtrfc.com/failover-attempts`), incremented by
   `setFailoverTriggered` in the same update as state and timestamp (ADR 0010 D10, D14), removed by
   `clearRollingUpdateState`, listed in the README annotation table.
4. A level condition (example `SentinelFailoverStalled`) with its `conditionRegistry` row (ADR
   0027): True past the cap, cleared in `handleNewMasterFound` when it proceeds with a connected
   replica and in `clearRollingUpdateState`, behind a presence guard. Past the cap the pass returns
   `DeferredRequeueAfter` so the status write runs (ADR 0010 D16/D17, ADR 0026 D5). No Event.
5. Option B: past the cap `handleNoMasterFound` neither resets nor enters `stateFailoverReset`;
   the state stays `failover-triggered` and every pass keeps scanning for a current master. Loop B's
   count lives for the episode and its check moves in front of `:3159` and `:3161`, with no clear
   at `:3170`.
6. ADR 0010 D18 (the cycle is capped; its expiry is a bounded observation) and the residual risk
   closed; the condition in the README condition table and `docs/operations/status.md`; the resume
   procedure in `docs/operations/rolling-updates.md` (send `SENTINEL FAILOVER` to one Sentinel, or
   remove the state annotation and the counter); the comments of item 1 and `:90-94`,
   `:1369-1373`, `:2721-2725` updated to the capped cycle.

**Tests**

- Unit: drive `handleNoMasterFound` and `handleFailoverRetrigger` through N + 1 cycles with every
  Sentinel answering `NOGOODSLAVE`; assert count and condition; fails with the counter write
  removed from `setFailoverTriggered`. A failing CR update shows counter and state are one write.
  The pass past the cap returns `DeferredRequeueAfter`. The `conditionRegistry` guard is red
  without the new row.
- Option B: past the cap no `SENTINEL FAILOVER`, `REMOVE` or `MONITOR` and the state stays; at
  loop B's cap with a stale timestamp no `REPLICAOF` and no `REMOVE`; a master appearing during
  the hold continues the roll and clears the condition. Revert checks: each fails with the cap
  check removed, the loop B test also with the clear at `:3170` restored.
- `make test-unit`, `make lint`, `make cyclo`; the full e2e suite on `single-node-valkey9` and
  `single-node-valkey8`, because every Sentinel data roll passes `setFailoverTriggered`.
- Optional e2e with every retrigger answering `NOGOODSLAVE` (`replica-priority 0` on each
  replaced replica); it races the roll's own trigger and no deterministic fixture is known.
  ADR 0017 decides whether it may be an e2e.

## Open questions

### Q1: What does the roll do once its Sentinel failover has cycled N times without a usable new master?

Today it retries forever: loop A resets every Sentinel and retriggers about once a minute, loop B
resets and sends `REPLICAOF` three times per round. The per-step bounds, the first trigger, the
`verifyNewMasterReady` gate and ADR 0025 D9 stay as they are, and ADR 0010 D4 forbids clearing the
roll state on expiry.

- **A - report only.** Count and report the level past N; the cycle continues. Cost S; the stuck
  roll becomes visible, the resets and failovers go on without end.
- **B - cap and hold (recommended).** After N + 1 failovers the operator stops resetting and
  retriggering, keeps the state and keeps watching for a current master (Sentinel's own failover or
  a human `SENTINEL FAILOVER`); loop B sends at most `maxReconnectResets` rounds per episode. Cost
  M; a cause that clears only after the hold needs Sentinel's own failover or a human.
- **C - back off.** Keep cycling past N with a doubling wait up to a ceiling (for example 30
  min), report the level. Cost M; heals a late-clearing cause without a human, but resets and
  failovers stay unbounded, only slower.

B because every extra cycle costs a tier-wide reset and an ungated failover, the drivers found do
not clear by retrying (measured `NOGOODSLAVE` stays), and stop-acting-keep-observing is the shape
ADR 0010 D16/D17 and ADR 0026 D5 already use. C's one advantage, a NotReady node returning after
the hold began, needs a compounding failure and narrows further once T62's gate skips the reset
toward an unreachable master.

**Answer:** _open_

### Q2: How many retriggers N does an episode get before the cap (option B or C)?

The cycle exists for a Sentinel still rediscovering its replicas after a reset, which a few
retries cover; how many retries a recovering failover needed has never been recorded. With N = 3
the hold begins about 3 min after the first trigger, at most about 8 min.

- **N = 3 (recommended).** Covers the transient case with margin and bounds the hold to minutes.
- **A larger N (for example 5).** More room for a slow recovery, proportionally more resets and
  failovers before the cap.

**Answer:** _open_

## Not verified

- Neither loop has been driven by the operator on the current code (Kind or e2e); the drivers and
  loop B's immediate restart are by reading, the Sentinel side measured.
- Client impact per accepted retrigger (a role change, so a write interruption) is by reading.
- Whether the phase string stays constant long enough for `ValkeyPhaseNotOK`: a flapping pod
  changes `k/n` and a failed CR write sets `Error`, each restarting the alert's clock.

## Related

- [T62](062-resetsentinelstate-falls-back-to-sentinel-reset.md): its gate decides what each
  cycle's reset does, not how many cycles run.
- [T73](073-forcereplicaconnections-re-points-a-data-holder-at-an-empty-master.md): what each
  `forceReplicaConnections` and ungated retrigger may do; the cap bounds how often.
- [T74](074-no-condition-reports-a-sentinel-with-no-monitor-or-too-few-peers-or-replicas.md):
  would report the empty-table Sentinel state of the self-sustaining driver.
- [T18](018-ready-keeps-its-pre-roll-value-during-a-rolling-update.md): `Ready` stays stale while
  the cycle runs; the deferred requeue lets the status write run.
- [T23](023-pauserollingupdate-records-no-pause.md): `pauseRollingUpdate` clears the state, so it
  cannot serve as the cap.
