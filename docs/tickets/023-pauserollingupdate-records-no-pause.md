---
id: T23
title: "`pauseRollingUpdate` records no pause — it clears the state and re-arms a fresh budget"
state: analysed       # was filed; every load-bearing claim re-verified at 84a39c2 by reading, git and two docker measurements, options re-weighed (History 2026-09-27)
severity: low         # low as a defect; the "medium as a documentation lie" half landed in bcc63c9 (work list item 1)
security: none
urgency: now          # rule 1 as this repository applies it (false by code reading, as in 018, 044, 062): two test lines promise a halt (work list item 3); was icebox. Then later (rule 4, work list item 4), then icebox (rule 5, D1 re-decides ADR 0010 D4)
effort: M             # the recommended option D of D1: S in code, M in documents; C is M. Was L until 2026-09-27
blocked-by: adr-0010  # D1 amends ADR 0010 D4 under option D; work list items 3 and 4 are not blocked
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
[`:2637`](../../internal/controller/rolling_update.go#L2637))*), which deletes
`annotationSyncWaitStarted` and drops the in-memory wait bound. Nothing on the CR then records
that a pause happened except the `RollingUpdatePaused` condition, and the next dispatching pass
re-arms a **fresh** `syncTimeout` budget, waits it out and pauses again — re-emitting the
Warning Event each cycle. On a 5 min `syncTimeout` at a 10 s requeue that is a repeating cycle,
not a halt. *(corrected 2026-09-27: only where something requeues the pass that pauses. On a
multi-replica cluster without Sentinel nothing does, and the next dispatch waits for an event;
see Fact.)* *(added 2026-09-27 at 84a39c2: nor on a Sentinel cluster whose pause is a slow full
sync or zero WAIT acknowledgements, where the phase reads `OK` too; the replica count behind it
measured, the phase read, see Fact.)*

Two consequences:

* **It contradicts ADR 0010's own rule** that expiry hands over to another bounded state and
  *never* to a cleared rolling-update state — which is exactly what this does.
* **Four tracked sentences promise a halt.** T15 D4 corrects them in text; this item is the
  version where they become true at the mechanism: a `statePaused` state that both dispatchers
  hold until the generation changes. ~~*(corrected 2026-09-27: they were corrected; at `4a7543e`
  no tracked sentence promises a halt — `status.md:21`, `rolling-updates.md:18`,
  `api/v1/valkey_types.go:53-63` and `rolling_update.go:2603-2616` describe a report and a retry.
  `statePaused` is option B below, no longer the item.)*~~ *(corrected 2026-09-27 at 84a39c2: the
  four were corrected and describe a report and a retry —
  [`status.md:21`](../operations/status.md#rollingupdatepaused),
  [`rolling-updates.md:18`](../operations/rolling-updates.md),
  [`api/v1/valkey_types.go:55-62`](../../api/v1/valkey_types.go#L55-L62) and
  [`rolling_update.go:2605-2618`](../../internal/controller/rolling_update.go#L2605-L2618) — but two
  tracked test lines still promise the halt: the comment
  [`rolling_update_test.go:3898`](../../internal/controller/rolling_update_test.go#L3898)
  ("Paused: no requeue (operator waits for spec change).") and the assertion message
  [`failover_sync_gate_test.go:132`](../../internal/controller/failover_sync_gate_test.go#L132)
  ("a paused update waits for a new spec change, not a requeue"). The assertion next to each
  (`NeedsRequeue == false`) is right; the text is not. Work list item 3. `statePaused` is no
  longer an option, History.)*

**The re-decision it needs.** Is a state that ends only at a spec change *bounded* in ADR 0010's
sense, or is it the unbounded wait ~~D3~~ refuses? *(corrected 2026-09-27: D3 is the Phase 1
give-up rule; the refusal of unbounded waits is the ADR's title rule, and the rule the pause
breaks is D4, [`0010:240-245`](../adr/0010-every-rolling-update-wait-is-bounded.md).)* That question is why this is its own item and not
part of T15: answering it either way is an ADR 0010 amendment, and coupling it to a status
lifecycle fix makes both unreviewable. *(added 2026-09-27 at 84a39c2: the question as posed
belongs to the `statePaused` option, which was removed as not sensible; the open question is
decision D1 under Options, and each of its two options changes ADR 0010 text.)*

~~**Not verified:** the ~29-`False`-passes-per-`True` figure quoted in the T15 analysis is derived
from the requeue delay and the default `syncTimeout`, not observed on a cluster.~~
*(corrected 2026-09-27 at 84a39c2: the figure has no source. It is not in the T15 section of
[archive/039](archive/039-findings-from-the-1-11-0-fleet-rollout.md) (`:4724-5519`);
`git log --all -S'~29' --oneline` finds only `4a7543e`, and in that commit only this ticket's own
line. It may come from the untracked pre-numbering collection file, which git cannot show. It
also describes no current behaviour: since ADR 0002 D10b `RollingUpdatePaused` stays `True`
through every retry and is cleared only at the converged early return
([`rolling_update.go:312`](../../internal/controller/rolling_update.go#L312)) and at completion
([`:382`](../../internal/controller/rolling_update.go#L382)). Dropped.)*

## Fact, re-verified 2026-09-27 at 84a39c2

**Verified** (by reading at `84a39c2`, unless a measurement is named):

- `pauseRollingUpdate` ([`rolling_update.go:2619-2647`](../../internal/controller/rolling_update.go#L2619-L2647)) sets
  `RollingUpdatePaused=True/SyncTimeout` ([`:2623-2627`](../../internal/controller/rolling_update.go#L2623-L2627)), writes phase `Error`
  ([`:2629-2630`](../../internal/controller/rolling_update.go#L2629-L2630)), emits the Warning ([`:2632`](../../internal/controller/rolling_update.go#L2632)), calls
  `clearRollingUpdateState` ([`:2637`](../../internal/controller/rolling_update.go#L2637)) and returns an empty result
  ([`:2646`](../../internal/controller/rolling_update.go#L2646)). `clearRollingUpdateState` ([`:3423-3497`](../../internal/controller/rolling_update.go#L3423-L3497)) drops the
  in-memory bounds ([`:3431`](../../internal/controller/rolling_update.go#L3431)), clears `PodTerminationStalled` and
  `PodRecreationStalled` ([`:3439-3442`](../../internal/controller/rolling_update.go#L3439-L3442)), deletes the state annotation and every bound annotation
  ([`:3461-3470`](../../internal/controller/rolling_update.go#L3461-L3470)) and clears the drain-promotion stamps
  ([`:3495`](../../internal/controller/rolling_update.go#L3495)). The stamp clear sits after the early return at
  [`:3457-3460`](../../internal/controller/rolling_update.go#L3457-L3460), so a pause that finds none of the ten annotations clears no
  stamps. ADR 0010 D4 ([`0010:240-245`](../adr/0010-every-rolling-update-wait-is-bounded.md))
  forbids clearing the state on expiry and asks every abandon path to name a successor state;
  ADR 0026 says the same of the pause ([`0026:616-619`](../adr/0026-a-pod-being-deleted-is-not-available.md)).
  The Warning Event is a separate object, not part of the CR.
- **Every pause sits before a promotion or a delete**, so D4's hazard (two masters and no caller
  of `detectAndResolveSplitBrain`) is not reached from it: `verifyReplacedReplicasSynced`
  ([`:2575`](../../internal/controller/rolling_update.go#L2575), [`:2592`](../../internal/controller/rolling_update.go#L2592)) runs before `replaceNextReplica` deletes a
  replica (called at [`:2424`](../../internal/controller/rolling_update.go#L2424)); `waitOrPauseForReplicaSync` ([`:2819-2827`](../../internal/controller/rolling_update.go#L2819-L2827))
  is reached from `waitForReplicasReady` ([`:2801`](../../internal/controller/rolling_update.go#L2801), [`:2807`](../../internal/controller/rolling_update.go#L2807)), the
  zero-acknowledgement branch of `waitForWriteSync` ([`:2959`](../../internal/controller/rolling_update.go#L2959)) and
  `verifyPromotionCandidateHoldsData` ([`:2850`](../../internal/controller/rolling_update.go#L2850), [`:2862`](../../internal/controller/rolling_update.go#L2862),
  [`:2876`](../../internal/controller/rolling_update.go#L2876), [`:2882`](../../internal/controller/rolling_update.go#L2882)); on the Sentinel path
  after the guard that stops once a failover is triggered ([`:2702-2708`](../../internal/controller/rolling_update.go#L2702-L2708), then
  [`:2711`](../../internal/controller/rolling_update.go#L2711), [`:2717`](../../internal/controller/rolling_update.go#L2717)), on the path without Sentinel in
  `handleManualFailover` before `promoteAndRedirect` ([`:4008`](../../internal/controller/rolling_update.go#L4008), [`:4013`](../../internal/controller/rolling_update.go#L4013),
  [`:4030`](../../internal/controller/rolling_update.go#L4030), then [`:4034`](../../internal/controller/rolling_update.go#L4034)). At every one of them the state is `replacing-replicas` (set at
  [`:2457-2460`](../../internal/controller/rolling_update.go#L2457-L2460)) or empty. Every failover state is routed away before a pause
  site ([`:741-748`](../../internal/controller/rolling_update.go#L741-L748), [`:3949-3957`](../../internal/controller/rolling_update.go#L3949-L3957)). The standalone handler reaches
  no pause site: the nine pause sites (two direct `pauseRollingUpdate` calls, `:2575` and `:2592`,
  and seven callers of `waitOrPauseForReplicaSync`, whose own call is `:2822`) sit under `handleRollingUpdate` ([`:751`](../../internal/controller/rolling_update.go#L751),
  [`:756`](../../internal/controller/rolling_update.go#L756)) and `handleMultiReplicaRollingUpdate` ([`:3960`](../../internal/controller/rolling_update.go#L3960),
  [`:3966`](../../internal/controller/rolling_update.go#L3966)). *(Added 2026-09-27 at 84a39c2:)* **after
  the first pause of a roll the state stays empty for every later sync wait**, not only in the
  pausing pass: `replaceNextReplica` sets `replacing-replicas` only after
  `verifyReplacedReplicasSynced` has passed ([`:2424`](../../internal/controller/rolling_update.go#L2424), then
  [`:2457-2460`](../../internal/controller/rolling_update.go#L2457-L2460)). It stays harmless because every wait returns
  `NeedsRequeue`, and `reconcileWorkload` ends the pass on it before the post-update checks
  ([`valkey_controller.go:340-342`](../../internal/controller/valkey_controller.go#L340-L342)); so in a paused roll the
  pausing pass is the only pass that reaches the no-master recovery and the steady-state
  split-brain check. Not traced: a topology switch in the middle of a roll that leaves a state
  of the other topology.
- *(Added 2026-09-27 at 84a39c2:)* **An outdated pod does not remain at every pause.** The T15
  analysis ([archive/039 `:4928-4940`](archive/039-findings-from-the-1-11-0-fleet-rollout.md))
  recorded a fixture shape, measured in a scratch tree then: `verifyReplacedReplicasSynced`
  skips every outdated pod, so it pauses on an up-to-date replica, and when the remaining
  ordinals are absent or not Ready the next pass finds no outdated pod and no state and takes
  the converged early return ([`rolling_update.go:296-313`](../../internal/controller/rolling_update.go#L296-L313)). No roll
  supervises the unsynced replica after that. `countUpdatedPods` counts only pods that are
  current and reachable ([`:1981-1989`](../../internal/controller/rolling_update.go#L1981-L1989)), so with the state kept (option C
  of D1) the next pass would dispatch into `replaceNextReplica` and verify the sync again. Not
  re-run at `84a39c2`; whether the shape occurs in production is not known.
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
  [`valkey_controller.go:2606-2629`](../../internal/controller/valkey_controller.go#L2606-L2629); `updateStatus`
  ([`:2181`](../../internal/controller/valkey_controller.go#L2181)) re-reads the CR and
  `persistStatus` ([`:2548-2571`](../../internal/controller/valkey_controller.go#L2548-L2571)) writes the difference). Work list item 1.
  *(Added 2026-09-27 at 84a39c2:)* [`status.md:83`](../operations/status.md#error) lists "a paused
  roll" among what phase `Error` covers and gives no duration. That is imprecise rather than
  false: the pause does write and persist `Error`, briefly. An optional clarification rides with
  work list item 3.
- **Where the pause does not cycle.** ~~On a Sentinel cluster the unsynced replica makes
  `updateHAStatus` report `Syncing` (`:2475`), which requeues in 10 s (`:377-379`), or the
  released Sentinel roll requeues itself.~~ *(corrected 2026-09-27 at 84a39c2, measured: on a
  Sentinel cluster the pausing pass requeues through `Syncing`
  ([`valkey_controller.go:2475-2487`](../../internal/controller/valkey_controller.go#L2475-L2487), requeue at
  [`:377-380`](../../internal/controller/valkey_controller.go#L377-L380)) only when the unsynced replica is missing from the
  master's replica list: `AllSynced` is `!MasterSyncInProgress && connected_slaves == replicas-1`
  ([`internal/health/checker.go:129-130`](../../internal/health/checker.go#L129-L130)), and the master counts a replica in
  `connected_slaves` while that replica is in the middle of a full sync and reports
  `master_link_status:down` (measurement 2 below). So an unreachable master, an auth failure
  (the [authentication.md:110-118](../operations/authentication.md) case) or a replica pointed
  elsewhere requeue; a slow full sync — the "dataset that needs longer than `syncTimeout`" case —
  and a pause on zero WAIT acknowledgements with every replica connected read `OK`
  ([`:2488-2500`](../../internal/controller/valkey_controller.go#L2488-L2500)) and have the same resume gap as a cluster without
  Sentinel, unless the Sentinel roll released in that pass requeues. The resume gap on Sentinel
  is traced by reading, not reproduced.)* *(Precised 2026-09-27, final pass, from
  [T69](069-three-sync-checks-read-a-replica-field-from-the-master.md):)* the
  `!MasterSyncInProgress` term decides nothing here: `CheckCluster` reads it from the master's
  `INFO`, and Valkey writes `master_sync_in_progress` only on a replica, so on a master it is
  always `false` and `AllSynced` is the `connected_slaves` count alone (T69 Fact, upstream source
  and docker on both pins; T69's M1 also measured the transfer and the load this ticket's
  measurement 2 left out, with the replica counted throughout, 6-8 s at 1.5 M keys). How T69's options change
  this bullet: under its recommended **A**, `AllSynced` counts only replicas that answer the full
  replication answer (role, `master_link_status:up`), so a slow full sync reads `Syncing` and
  requeues every 10 s, which closes the Sentinel resume gap for that case without any D1 option
  of this ticket; the pause on zero WAIT acknowledgements with every replica's link up still reads
  `OK` and keeps the gap. Under T69's **B** (`state=online` in the master's `slaveN` lines) the
  same holds except for the 0.8-1.6 s between `online` and the replica's end of load. Under its
  runner-up **D** and under **C**, `CheckCluster` is unchanged and this bullet stands as written.
  Read, not measured on the operator. On a multi-replica
  cluster without Sentinel, `updateStandaloneStatus` reports `OK` once every pod is Ready and
  reachable ([`:2235-2260`](../../internal/controller/valkey_controller.go#L2235-L2260)); the readiness probe is a `PING`
  ([`statefulset.go:847-857`](../../internal/builder/statefulset.go#L847-L857),
  [`ProbeCommand`](../../internal/builder/statefulset.go#L1515)), which an unsynced replica passes
  *(measured 2026-09-27, measurement 1 below)*;
  and the pass returns the zero `deferredRequeue` ([`:392-396`](../../internal/controller/valkey_controller.go#L392-L396)). The CR watch is
  generation-gated and there is no Pod watch ([`:2987-2996`](../../internal/controller/valkey_controller.go#L2987-L2996)), so the roll
  dispatches again only on an event of an owned object, a spec change, an operator restart or
  the 10 h cache resync *(review 2026-09-27: or a change of a Secret the CR references, the one
  non-owned watch, [`valkey_controller.go:2997-3000`](../../internal/controller/valkey_controller.go#L2997-L3000))*, with phase `OK` and `RollingUpdatePaused=True` in between.
  *(Added 2026-09-27 at 84a39c2:)* the 10 h is controller-runtime v0.25.1's
  `defaultSyncPeriod = 10 * time.Hour` (`pkg/cache/cache.go:45` in the module cache,
  `go.mod:16`), and the operator sets no `SyncPeriod`. `Owns(&appsv1.StatefulSet{})` has no
  predicate, so a status update of the data StatefulSet (a pod readiness flip changes
  `status.readyReplicas`) re-enters the reconcile: the gap is short where pods churn, such as
  database-examples with its pod-kill schedule, and can be hours in a quiet namespace.
- **A comment ~~says~~ *(said, until work list item 1 on 2026-09-27; fixed, History)* the pause
  ends the pass.** [`rolling_update.go:2641-2645`](../../internal/controller/rolling_update.go#L2641-L2645):
  "That ends THIS pass without a wait". It ends the data roll's work for the pass; the pass goes
  on (above), as the doc comment of `handlePostRollingUpdateChecks`
  ([`valkey_controller.go:399-410`](../../internal/controller/valkey_controller.go#L399-L410)) and ADR 0010 `:800-812` say. Work list item 1.
  *(Verified 2026-09-27 at 84a39c2: the comment at `:2641-2645` now reads "That ends the data
  roll's work for this pass, not the pass", `git show bcc63c9`.)*
- **Appendix, same family (found 2026-09-27):** `clearSyncWaitTimestamp`
  ([`rolling_update.go:2664-2674`](../../internal/controller/rolling_update.go#L2664-L2674)) discards its write
  error ([`:2673`](../../internal/controller/rolling_update.go#L2673)) after it has
  forgotten the in-memory bound. If that write fails, the stored annotation survives, and
  `waitBoundExceeded` reads the annotation first
  ([`:1220-1222`](../../internal/controller/rolling_update.go#L1220-L1222)), so ~~the next sync wait of
  the same roll starts expired and pauses early~~ *(corrected 2026-09-27 at 84a39c2: the next sync
  wait of the same roll starts with the time since the previous wait was armed already spent,
  because `ensureWaitBound` does not re-arm an annotation that is present
  ([`:1179-1181`](../../internal/controller/rolling_update.go#L1179-L1181)); it pauses early only once that exceeds
  `syncTimeout`. The discarded clear gets no retry: at `:2601` the delete of the next replica
  follows in the same pass; at `:2813` the in-memory object already lacks the key, so a later arm
  in the same pass (`:2959`) overwrites the stored stamp with a fresh one)*. The fail direction is safe (a pause, never a
  promotion). Fixing it means returning the error to its callers
  ([`:2601`](../../internal/controller/rolling_update.go#L2601),
  [`:2813`](../../internal/controller/rolling_update.go#L2813), and the pause under option C); ~~not
  decided, and not part of work list item 1~~ *(corrected 2026-09-27 at 84a39c2: it needs no
  decision and is work list item 4; the log-only alternative leaves the defect, History)*.
  `grep -rn '_ = .*\.Update(ctx\|_ = .*\.Patch(ctx' internal cmd --include='*.go' | grep -v _test.go`
  finds `:2673` as the only discarded object write left in non-test code; its sibling
  `clearRecreationWait` logs the same failure ([`:2205-2209`](../../internal/controller/rolling_update.go#L2205-L2209)).
- T23 is cited outside `docs/tickets/` at ADR 0002 `:316`, ADR 0010 `:812`, ADR 0024 `:531`,
  ADR 0026 `:771` and [`rolling_update.go:2616`](../../internal/controller/rolling_update.go#L2616)
  (`git grep -n 'T23\b' -- ':!docs/tickets'`, exactly these five).
- *(Added 2026-09-27 at 84a39c2:)* **Keeping the state through a pause switches two recovery
  paths off.** `checkAndRecoverNoMaster` returns early on a non-empty state
  ([`valkey_controller.go:2884-2888`](../../internal/controller/valkey_controller.go#L2884-L2888)), and so does
  `checkSteadyStateSplitBrain` ([`steady_state_master.go:158-163`](../../internal/controller/steady_state_master.go#L158-L163)).
  Today the pausing pass on a cluster without Sentinel runs both, with the state already cleared
  ([`valkey_controller.go:421-443`](../../internal/controller/valkey_controller.go#L421-L443)), once per pause — not during the
  waits, which end on `NeedsRequeue`. With no master, the multi-replica dispatcher calls
  `replaceNextReplica` before its `masterIdx < 0` wait
  ([`rolling_update.go:3960-3972`](../../internal/controller/rolling_update.go#L3960-L3972)), so a replica phase with no master ends
  in `verifyReplacedReplicasSynced` pauses, and today the pausing pass promotes pod-0. How such a
  phase arises, by reading the init script
  ([`statefulset.go:490-538`](../../internal/builder/statefulset.go#L490-L538)): a single non-graceful kill of the
  recorded master does not produce it, because a pod whose replica config names itself
  re-claims master (`SELF_IS_KNOWN_MASTER`, `:504-508`). It needs a stale known-master record —
  the [T35](035-master-records-lag-the-real-master.md) family, observed on wds18 database-examples
  after v1.13.0, or an unrecorded drain promotion during the roll — followed by the loss of the
  real master at an ordinal other than 0: Phase 2 ignores the stale record because it answers
  `role=slave` (`:509-517`), and Phase 3 gives the replica config to ordinal != 0 (`:531-537`).
- *(Added 2026-09-27 at 84a39c2:)* **The pause clears two edges without evidence.**
  `PodTerminationStalled` and `PodRecreationStalled` are cleared at every pause
  ([`rolling_update.go:3439-3442`](../../internal/controller/rolling_update.go#L3439-L3442)); the registry lists
  `clearRollingUpdateState` as a clear site of both
  ([`condition_registry.go:165`](../../internal/controller/condition_registry.go#L165),
  [`:192`](../../internal/controller/condition_registry.go#L192)), while ADR 0027 asks an edge's clear site to prove its
  precondition is gone. Reachable in principle (a stalled missing candidate plus a replaced
  replica that loses sync); not traced.
- *(Added 2026-09-27 at 84a39c2:)* **No shipped alert fires for a paused roll** that reads `OK`
  (every pause without Sentinel; on Sentinel the slow-sync and zero-acknowledgement pauses). The
  chart's PrometheusRule ([`prometheusrule.yaml:32-170`](../../deploy/helm/valkey-operator/templates/prometheusrule.yaml))
  ships eight rules, none on `RollingUpdatePaused`: `ValkeyPhaseNotOK` does not fire (phase
  `OK`), `ValkeyReplicasMissing` does not (the unsynced replica is Ready, measurement 1),
  `ValkeySpecNotObserved` does not (every `updateStatus` writes `Ready` at `v.Generation`, and
  the collector takes the newest `observedGeneration`,
  [`collector.go:208-225`](../../internal/metrics/collector.go#L208-L225)). The series
  `vko_valkey_status_condition{condition="RollingUpdatePaused"}` is exported
  ([`collector.go:186-192`](../../internal/metrics/collector.go#L186-L192)); no rule uses it.
- *(Added 2026-09-27 at 84a39c2:)* A realistic production trigger is documented:
  [authentication.md:110-118](../operations/authentication.md) — pointing `spec.auth.secretName`
  at a Secret with a different password leaves the first replaced replica unable to sync, and
  the roll pauses. During the `wait_bgsave` phase of a full sync the replica reports
  `master_sync_in_progress:0` and `master_link_status:down` (measurement 2), so
  `replicationNotEstablishedReason` ([`rolling_update.go:4467-4476`](../../internal/controller/rolling_update.go#L4467-L4476)) reports
  "replication not established on <pod> (role=slave, linkStatus=down)" rather than "<pod> is still
  syncing from its master": the pause message
  of a slow sync can read like a broken link. Informative only. *(Precised 2026-09-27, final
  pass:)* [T69](069-three-sync-checks-read-a-replica-field-from-the-master.md) explains why: a
  replica's `master_link_status:up` and `master_sync_in_progress:1` are read from one state
  variable and never occur together, so the "still syncing" branch of
  `replicationNotEstablishedReason` is unreachable and every pause on a replica mid-sync names
  the link.

**Measurements** (2026-09-27, docker, both pinned lines, every container and network removed):

1. Does an unsynced replica pass the `PING` readiness probe?
   `docker run -d --rm --name vko-verify-t23-<tag> <img> valkey-server --replicaof 10.255.255.1 6379 --repl-timeout 5`,
   3 s later `docker exec … valkey-cli ping` and `valkey-cli info replication`, for
   `valkey/valkey:9.1.1` and `valkey/valkey:8.1.9`. Both: `PONG`, exit 0; `role:slave`,
   `master_host:10.255.255.1`, `master_link_status:down`, `master_sync_in_progress:0`. Run by the
   auditor and repeated by a second reviewer with the same result.
2. Does the master count a replica that is in the middle of a full sync? A user network per
   image; master `valkey-server --repl-diskless-sync yes --repl-diskless-sync-delay 20`, replica
   `valkey-server --replicaof <master> 6379`, 4 s later `info replication` on both and `ping` on
   the replica. Both lines: master `connected_slaves:1`,
   `slave0:…,state=wait_bgsave,offset=0,lag=0,type=replica`; replica `PONG` exit 0, `role:slave`,
   `master_link_status:down`, `master_sync_in_progress:0`. Only `wait_bgsave` was measured; the
   `send_bulk` and RDB-load phases were not.

**Cross-ticket** (verified by reading the tickets at `84a39c2`):

- [T18](018-ready-keeps-its-pre-roll-value-during-a-rolling-update.md) cited `rolling_update.go:2643`
  for the pause's empty result at `84a39c2` (now `:2646`, re-anchored in its own 2026-09-27 edit) and relies on "the pass that pauses reaches
  `updateStatus` unless a post-update check ends it". That stays true under both D1 options,
  because a `DeferredRequeueAfter` also continues to `updateStatus`; only the reason (in T18 and
  in ADR 0001 `:118`) changes from "returns an empty result" to "returns a
  `DeferredRequeueAfter`". *(Precised 2026-09-27, cross-ticket: a `DeferredRequeueAfter` also
  sets `dataTierHolding`
  ([`valkey_controller.go:363`](../../internal/controller/valkey_controller.go#L363)), so under
  both options the pause pass skips the Sentinel roll
  ([`:465-468`](../../internal/controller/valkey_controller.go#L465-L468)); the "unless a
  post-update check ends it" sentences at ADR 0002 `:538-539`, `api/v1/valkey_types.go:45-46`
  and `docs/operations/status.md:17` stay true. T18's note, which had named option C and asked
  to revisit all four places, was corrected to this reading the same day.)*
- [T35](035-master-records-lag-the-real-master.md) (decided 2026-09-27, its changes not built):
  its stale master records are the realistic precondition of the replica phase with no master that makes option C of D1 a regression. The
  ADR 0010 amendment under option D should name that dependency, so a later move to C does not
  silently drop the pausing pass's no-master recovery.
- [T36](036-non-persistent-master-restarts-empty.md) measured (docker, both pins) that
  `checkAndRecoverNoMaster` promotes pod-0 with no dataset comparison and can lose the writes
  since a roll; its recommended fix is a data-aware no-master recovery. That recovery is the exit
  option D keeps in the pausing pass and option C closes, so T36's fix changes the weight of D1
  (see Why D beats C).
- [T40](040-tracked-files-cite-work-items-instead-of-adrs.md) counts 5 T23 occurrences outside
  `docs/tickets/`, which matches `84a39c2` (its ADR 0002 location is `:316`). They are rewritten
  once, at this ticket's close, not twice.
- [T51](051-a-changed-cluster-password-reaches-no-running-pod.md): ~~its option B ("the roll stalls
  midway ... pauses the roll") is a T23 pause.~~ *(corrected 2026-09-27, cross-ticket: 051's
  re-verification removed its option B; the pause now lives in its Fact, the reference-change
  rotation path - a changed `spec.auth.secretName` or key with a different password rolls, and
  the first replaced replica cannot sync, so the roll pauses after `syncTimeout` - which is a
  T23 pause, as is the same path in `docs/operations/authentication.md:109-116`.)* Under D1 option C or D it repeats on a 10 s recheck
  on every topology; today, without Sentinel, it waits for an event with phase `OK`.
- [T69](069-three-sync-checks-read-a-replica-field-from-the-master.md) *(added 2026-09-27, final
  pass)*: the `MasterSyncInProgress` term of `AllSynced` is always `false` on a master, so the
  Sentinel resume-gap analysis above rests on the `connected_slaves` count alone. T69's
  recommended A makes a slow full sync read `Syncing` with a 10 s requeue and so closes the
  Sentinel slow-sync resume gap on its own; the zero-acknowledgement pause and every pause without
  Sentinel keep the gap, so D1 is still needed. Under T69 A the Impact sentence "no shipped alert
  fires ... on Sentinel, a pause on a slow full sync" no longer holds for that case
  (`ValkeyPhaseNotOK` can fire on a `Syncing` phase that lasts 30 min). T69's C or its runner-up D
  change nothing here.
- [archive/039](archive/039-findings-from-the-1-11-0-fleet-rollout.md): its T15 section does not
  contain the "~29 False passes per True" figure (dropped above).

**Not verified:**

- Nothing was run on Kind and no `go test` ran. The resume gap without Sentinel and on the
  Sentinel slow-sync pause is traced by reading, not reproduced.
- Whether the drain-stamp clear at a pause can drop a stamp a later pass needs. The call is on
  the pause path ([`:3495`](../../internal/controller/rolling_update.go#L3495)), and the comment above it
  ([`:3484-3489`](../../internal/controller/rolling_update.go#L3484-L3489)) names the hazard of clearing a fresh drain's stamp early. A
  master drained during a sync wait is plausible (its replicas then report the link down, which
  is what the wait waits on), but no sequence was traced to a wrong demotion.
  *(Refined 2026-09-27 at 84a39c2, by reading:)* the roll's resolver runs at the head of every
  dispatch pass, the pausing pass included (`resolveSplitBrain`,
  [`rolling_update.go:3909`](../../internal/controller/rolling_update.go#L3909) without Sentinel, `:715` with Sentinel;
  stamp rule [`:1444-1460`](../../internal/controller/rolling_update.go#L1444-L1460)), so a stamp on one of two masters is
  consumed before the pause clears it. The open case is a sole stamped master: in the same pass
  `checkSteadyStateSplitBrain` runs after the clear
  ([`valkey_controller.go:440`](../../internal/controller/valkey_controller.go#L440); `adoptUnrecordedPromotion`,
  [`steady_state_master.go:173-175`](../../internal/controller/steady_state_master.go#L173-L175)) with only the structural and the
  recorded-yielded rule left ([`:288-303`](../../internal/controller/steady_state_master.go#L288-L303)) — the ordering the
  comment warns against. Softened further: `listMasterLabeledPods` reads the pod from the cache
  ([`:195-210`](../../internal/controller/steady_state_master.go#L195-L210)), which may still carry the stamp right after
  the patch, so whether the stamp is visible in that pass is non-deterministic. What would
  settle it: a unit test that stamps a sole master, pauses, and asserts that the same pass
  records it.
- *(Added 2026-09-27, review:)* what option C leaves standing that `clearRollingUpdateState`
  clears today: it forgets every in-memory wait bound, not only the sync wait's
  ([`:3431`](../../internal/controller/rolling_update.go#L3431)), clears `PodTerminationStalled`
  and `PodRecreationStalled` (`:3439-3442`) and deletes nine further annotations
  (`:3461-3470`). At the pause sites the state is `replacing-replicas` or empty, and the
  recreation wait clears its own annotation (`clearRecreationWait`, `:2194-2210`), so none of them should be armed
  there; that was read, not traced per site. Under C the drain stamps are cleared at the roll's
  completion instead, which is where the comment at `:3475-3494` says they are spent; the cost
  of a stamp that outlives its drain (named there) was not traced for the longer window.
- *(Added 2026-09-27 at 84a39c2:)* **The ADR 0032 D4 ordering gate opens at a pause.**
  `dataOwnershipRepairNeeded` keeps the ownership repair while it is carried and a roll is
  recorded ([`pod_security_migration.go:65-67`](../../internal/controller/pod_security_migration.go#L65-L67)); the pause
  empties the state for the rest of the roll's waits, so if every ordinal then holds a pod that
  is ours, rootless and Ready while the template still carries the repair, the next
  `reconcileStatefulSet` removes the repair under a running roll. Reachable only with the repair
  still carried and every pod rootless but outdated for another reason (the migration release
  plus a concurrent spec change). **Disputed:** one reviewer reads the consequence as extra
  restarts on persistent tiers; by reading, the pods replaced before the removal would be
  replaced again, which the second roll of ADR 0032 D2 does anyway, and the pods not yet replaced
  get the final template in one step, so whether the total number of restarts grows is not
  established. No data loss by reading. Not traced; a unit test over
  `dataOwnershipRepairNeeded` after a pause would settle the reachability. Today and option D
  keep it open; option C closes it.
- The masterless replica phase of the recovery bullet above is read from the init script and
  the dispatcher, not traced end to end. *(Added 2026-09-27 at 84a39c2, review:)* nor is which
  pod the no-master recovery then promotes: it always takes pod-0
  ([`valkey_controller.go:2925`](../../internal/controller/valkey_controller.go#L2925)) with no dataset comparison (T36), and
  replicas are replaced youngest-first ([`rolling_update.go:2412-2435`](../../internal/controller/rolling_update.go#L2412-L2435)),
  so whether pod-0 can be the unsynced replacement at that moment — on a non-persistent tier an
  empty pod whose promotion flushes the others — was not traced. Whether the database-examples Chaos Mesh schedule kills
  with grace period 0 is not verified; the argument does not rest on it (T35 is the source of
  stale records).
- Whether `ValkeyPhaseNotOK` ever fires on a paused roll under D1 option C or D: the phase label
  changes every cycle, so a rule evaluation that lands in the short window of the pausing pass
  drops the series and resets its `for: 30m`. Probabilistic, not measured (ADR 0010
  Consequences leaves the same question open).

## Impact

Low. The durable signals are `RollingUpdatePaused=True` and a Warning per pause; no alert keys on
either, and the phase reads `OK` or `Syncing` after the pause pass. On a cluster without Sentinel
a paused roll can stay half-done for hours with phase `OK`. On a Sentinel cluster every pause
releases the Sentinel roll once (ADR 0026 D11, the known exception).
*(Added 2026-09-27 at 84a39c2:)* stronger than stated: none of the eight shipped alert rules
fires for a paused roll that reads `OK`, which covers every pause without Sentinel and, on
Sentinel, a pause on a slow full sync or on zero WAIT acknowledgements (the replica count
measured, the rest read, Fact). How long
the roll sits depends on the next event: short in a namespace with pod churn, up to the 10 h
resync in a quiet one. Nothing is lost: every pause precedes a delete or a promotion. A paused
roll delays a TLS-rotation roll too, but `TLSMaterialStale` and its 72 h alert already cover a
roll that never happens inside the 30-day cert-manager window, so no security class applies.

## Options

Two decisions, D1 and D2, presented one at a time. The third question this analysis found (what
`clearSyncWaitTimestamp` does with its write error) has one sensible answer and is work list
item 4, not a decision.

### D1 — What a sync-timeout pause leaves behind, and what the pausing pass returns

**Mechanism today.** `pauseRollingUpdate`
([`rolling_update.go:2619-2647`](../../internal/controller/rolling_update.go#L2619-L2647)) writes
`RollingUpdatePaused=True/SyncTimeout`, phase `Error` and a Warning, then calls
`clearRollingUpdateState` ([`:2637`](../../internal/controller/rolling_update.go#L2637)): every in-memory
bound forgotten, the two stall edges cleared, the state annotation and nine others deleted, the
drain stamps cleared. It returns an empty `RollingUpdateResult`
([`:2646`](../../internal/controller/rolling_update.go#L2646)). `reconcileWorkload`
([`valkey_controller.go:336-396`](../../internal/controller/valkey_controller.go#L336-L396)) therefore neither ends the pass nor
counts the data tier as holding ([`:363`](../../internal/controller/valkey_controller.go#L363)), and three things follow in
that pass: the Sentinel roll runs (the ADR 0026 D11 exception,
[`:465-468`](../../internal/controller/valkey_controller.go#L465-L468)); on a cluster without Sentinel, whose state is now
empty, the no-master recovery and the steady-state split-brain check run
([`:421-443`](../../internal/controller/valkey_controller.go#L421-L443)); `updateStatus` overwrites the `Error` phase. Only an
`Error` or `Syncing` phase requeues ([`:377-380`](../../internal/controller/valkey_controller.go#L377-L380)); a pause that reads
`OK` returns no requeue ([`:396`](../../internal/controller/valkey_controller.go#L396)) and the roll re-dispatches on the next
event, arming a fresh `syncTimeout`.

**What the choice changes:** whether the pause keeps the state annotation, the drain stamps and
the two stall edges, and whether the pass returns empty or with a 10 s `DeferredRequeueAfter`
(`rollingUpdateRequeueDelay`, [`:203`](../../internal/controller/rolling_update.go#L203)). **What it does not change:** where
the nine pause sites sit (all before a delete or a promotion), the fresh budget per retry, the
Warning per pause, where `RollingUpdatePaused` is cleared (`:312`, `:382`), any pod template (so
the release that carries either option rolls nothing and stays upgrade-neutral apart from CRs
paused at upgrade time), and any delete. Neither option has a security effect.

- **C — Keep the state, clear only the sync wait, hold and requeue (runner-up).** In
  `pauseRollingUpdate` replace `clearRollingUpdateState` with `clearSyncWaitTimestamp`
  ([`:2664-2674`](../../internal/controller/rolling_update.go#L2664-L2674)), which alone gives the next wait a fresh budget, and
  return `RollingUpdateResult{DeferredRequeueAfter: rollingUpdateRequeueDelay}`.
  `clearStaleRollingUpdateState` leaves `replacing-replicas` alone
  ([`:833-844`](../../internal/controller/rolling_update.go#L833-L844), exemption at `:834`), so ADR 0010 D4 and CLAUDE.md
  master-authority rule 3 hold as written. It gains four narrow things over D: the drain stamps
  survive for the roll's resolver (the open case of Not verified); the two stall edges are no
  longer cleared without evidence (ADR 0027); the ADR 0032 D4 repair gate stays held; and in the
  T15 fixture shape the roll keeps verifying the unsynced replica instead of ending its
  supervision. **Its cost is a regression:** the kept state keeps `checkAndRecoverNoMaster` and
  `checkSteadyStateSplitBrain` switched off for as long as the roll stays paused (Fact). The state
  is the same one the roll carries during its waits before the first pause, so C creates no new
  combination of state and pass; it closes the one window in which a paused roll without
  Sentinel reaches the no-master recovery today, so a replica phase with no master would stay
  without a writable master until a human acts. It depends on work list item 4 — with the error
  still discarded, a failed clear leaves the expired annotation and every pass pauses and warns
  again. Cost M: about ten lines; unit tests that a pause keeps the state annotation and the
  stamps, returns a positive `DeferredRequeueAfter`, and that the pausing pass skips the
  Sentinel roll (the pattern of `TestReconcileWorkload_DataAvailabilityStallHoldsTheSentinelRoll`,
  [`pod_availability_test.go:585`](../../internal/controller/pod_availability_test.go#L585)), each with a revert check;
  documents: the "pause clears the state" sentences
  ([`api/v1/valkey_types.go:58-59`](../../api/v1/valkey_types.go#L58-L59), `rolling_update.go:2609-2613` and `:2634-2636`,
  `status.md:21`, `rolling-updates.md:18`, ADR 0010 `:801-802`, ADR 0024 `:530`, ADR 0026
  `:617`), every statement of the D11 exception (list under D), the `RollingUpdateResult` doc
  comment ([`rolling_update.go:168-183`](../../internal/controller/rolling_update.go#L168-L183)) and
  [`valkey_controller.go:343-354`](../../internal/controller/valkey_controller.go#L343-L354), and an amendment of ADR 0024 and
  ADR 0026 recording the no-master consequence.
- **D — Keep the clear, hold and requeue (recommended).** Leave `clearRollingUpdateState` in
  `pauseRollingUpdate` and change only its return value to
  `RollingUpdateResult{DeferredRequeueAfter: rollingUpdateRequeueDelay}`. The pausing pass then
  holds the Sentinel roll ([`valkey_controller.go:363`](../../internal/controller/valkey_controller.go#L363),
  [`:465-468`](../../internal/controller/valkey_controller.go#L465-L468)) and ends on a 10 s recheck on every topology
  ([`:355`](../../internal/controller/valkey_controller.go#L355), [`:396`](../../internal/controller/valkey_controller.go#L396)), and on a cluster without
  Sentinel it still runs the no-master recovery and the steady-state check. The sentences that
  say the pause clears the state stay true. Consequences: a paused roll emits one
  `RollingUpdatePaused` Warning per `syncTimeout` indefinitely on every topology (today, without
  Sentinel, one per event-triggered re-dispatch; Events of the same reason and message aggregate,
  so this is noise, not load); without Sentinel the phase stops sitting at `OK` and alternates
  between `Rolling Update i/n (syncing)` ([`rolling_update.go:2588`](../../internal/controller/rolling_update.go#L2588)) and a
  brief `Error` or `OK`; the drain stamps are still cleared just before that pass's steady-state
  check; the two stall edges are still cleared without evidence; the ADR 0032 D4 gate stays open
  at a pause. **It reopens ADR 0010 D4 and CLAUDE.md master-authority rule 3**, which it amends to
  name the sync-timeout pause as the one expiry that clears the state. The reason: D4's hazard is
  no dispatch and no split-brain caller once the state is gone, with masters left over from a
  half-finished failover. Every pause precedes the promotion, so there is no such failover; while
  an outdated pod exists the 10 s recheck guarantees a dispatch whose head runs
  `resolveSplitBrain` ([`rolling_update.go:3909`](../../internal/controller/rolling_update.go#L3909)); and once none exists
  (the T15 fixture shape) two masters are owned by the ADR 0011 steady-state check without
  Sentinel and by Sentinel with it. The amendment must not claim that an outdated pod always
  remains, and should name its dependency on the pausing pass's no-master recovery (T35). Cost:
  S in code (one returned value; unit tests that the pausing pass returns a positive
  `DeferredRequeueAfter`, skips the Sentinel roll, and on a cluster without Sentinel still
  reaches `checkAndRecoverNoMaster`, each with a revert check; the two existing pause tests,
  [`rolling_update_test.go:3875`](../../internal/controller/rolling_update_test.go#L3875) and
  [`failover_sync_gate_test.go:119`](../../internal/controller/failover_sync_gate_test.go#L119), assert only
  `NeedsRequeue == false` and no error, so they need an assertion on `DeferredRequeueAfter`
  before they can fail against a changed body). M in documents: the D4 and rule 3 amendment; the
  `RollingUpdateResult` doc comment ([`rolling_update.go:168-183`](../../internal/controller/rolling_update.go#L168-L183)), which
  defines `DeferredRequeueAfter` as set "by a wait whose bound has expired", and
  [`valkey_controller.go:343-354`](../../internal/controller/valkey_controller.go#L343-L354), both of which must name the pause
  as a setter; the pause's return comment ([`rolling_update.go:2641-2645`](../../internal/controller/rolling_update.go#L2641-L2645));
  and every statement of the D11 exception, found with
  `grep -n 'empty result\|pass that pauses\|in which a data roll pauses\|pass where a data roll\|returns no requeue\|pauseRollingUpdate' docs/adr/*.md CLAUDE.md README.md docs/operations/*.md internal/controller/valkey_controller.go api/v1/valkey_types.go`:
  [`api/v1/valkey_types.go:165-168`](../../api/v1/valkey_types.go#L165-L168),
  [`valkey_controller.go:405-410`](../../internal/controller/valkey_controller.go#L405-L410), ADR 0001 `:118`, ADR 0010 `:67`,
  `:450-455`, `:800-812`, `:864`, ADR 0024 `:28`, `:92`, `:234-236`, `:527-537`, ADR 0026 `:116`,
  `:561-564`, `:593`, `:767-778`, `:892`, `:909`, ADR 0027 `:129-133`, `:146`, `README.md:532`,
  `status.md:49` and `:53`, `CLAUDE.md:488-489` and `:610-611`.

**Why D beats C.** Both close the same two recorded gaps with the same `DeferredRequeueAfter`:
no requeue for a pause that reads `OK` ([`valkey_controller.go:396`](../../internal/controller/valkey_controller.go#L396)) and
the Sentinel roll released in the pausing pass ([`:363`](../../internal/controller/valkey_controller.go#L363)). They differ in
what the pause clears. C's four gains are each a narrow latent issue whose worst outcome is a
stale report, a missing completion supervision or restarts, and the stamp hazard is traced only
by reading and largely covered by the resolver running first in the same pass. C's cost is a
regression from today: in a paused roll without Sentinel, a replica phase with no master (T35's
stale records followed by the loss of a master at an ordinal other than 0) loses its only
automatic exit, `checkAndRecoverNoMaster` in the pausing pass
([`:421-429`](../../internal/controller/valkey_controller.go#L421-L429), gated on an empty state at
[`:2884-2888`](../../internal/controller/valkey_controller.go#L2884-L2888)). A write outage outweighs the four. One
qualification: that exit is the no-master recovery [T36](036-non-persistent-master-restarts-empty.md)
measured as not dataset-aware — it promotes the hardcoded pod-0 and redirects every other pod to
it with no dataset comparison
([`valkey_controller.go:2905-2965`](../../internal/controller/valkey_controller.go#L2905-L2965)) — so where pod-0 is the
unsynced replacement of a non-persistent tier, the recovery D keeps could discard the dataset
that C would leave held behind a write outage (Not verified). D keeps today's behaviour there and
C removes it; making the recovery dataset-aware is T36's recommended fix, not this ticket's, and
the D-over-C argument is strongest once it lands. D's price is
a written exception in ADR 0010 D4 and rule 3, and D4's hazard was checked against all nine
pause sites and does not arise at any of them. D is also the smaller change and does not depend
on work list item 4. Checkable: a unit test that a pausing pass without Sentinel and with no
master reaches `checkAndRecoverNoMaster` passes under D and fails under C. If the owner values
rule 3 as written above that recovery, C is the fallback, with the no-master consequence
recorded in the amendment of ADR 0024 and ADR 0026.

### D2 — Should the chart's PrometheusRule alert on `RollingUpdatePaused`?

**Mechanism today.** The collector exports every condition as
`vko_valkey_status_condition{condition,status,reason}`
([`collector.go:186-192`](../../internal/metrics/collector.go#L186-L192)). The chart's PrometheusRule is off by default
(`prometheusRule.enabled`, [ADR 0021](../adr/0021-per-resource-metrics-and-the-alert-that-was-missing.md) D7)
and ships eight rules ([`prometheusrule.yaml:32-170`](../../deploy/helm/valkey-operator/templates/prometheusrule.yaml)),
none on `RollingUpdatePaused`, and none of them fires for a pause that reads `OK` (Fact). Under
either D1 option the phase alternates, so `ValkeyPhaseNotOK`'s per-phase series breaks every
cycle and whether its `for: 30m` ever completes is not established. `RollingUpdatePaused` is an
edge that stays `True` from the first pause until convergence or completion (`:312`, `:382`;
[`condition_registry.go:196-206`](../../internal/controller/condition_registry.go#L196-L206)), so it is the one stable signal.
**What the choice changes:** a rule, or documentation of the series. It changes no operator
behaviour and, with the rule set off by default, no default install; an install that already
set `prometheusRule.enabled=true` gets a ninth alert on chart upgrade (precedent:
`ValkeyTLSMaterialStale`, ADR 0030 `:16`, `:577`).

- **A — Ship a ninth rule, `ValkeyRollingUpdatePaused` (recommended).**
  `max by (namespace, name) (vko_valkey_status_condition{condition="RollingUpdatePaused",status="True"}) == 1`
  guarded by `and on () max(vko_valkey_collector_success) == 1`, `for: 30m`, severity `warning`
  — the shape of `ValkeyReconcileBlocked` ([`prometheusrule.yaml:51-57`](../../deploy/helm/valkey-operator/templates/prometheusrule.yaml)).
  Record it in ADR 0021, in [`monitoring.md:63`](../operations/monitoring.md) (which says
  "eight alerts") and at [`status.md#rollingupdatepaused`](../operations/status.md#rollingupdatepaused).
  Cost XS. Nothing renders the chart in CI today
  ([T58](058-no-ci-gate-renders-the-chart.md)), so the rule is checked by reading or a local
  `helm template`. Consequence: it also fires on a roll that paused once and then took more than
  30 minutes to finish, because the condition stays `True` until completion; that is still a roll
  whose `syncTimeout` is too short for its dataset, which an operator can act on.
- **B — Ship no rule; document the series and the query** in
  [`status.md#rollingupdatepaused`](../operations/status.md#rollingupdatepaused) and
  [`monitoring.md`](../operations/monitoring.md) (runner-up). Cost XS, documentation only. The
  default rule set stays blind to a paused roll that reads `OK`, and every fleet user writes the
  same rule.

**Why A beats B.** ADR 0021 ships alerts exactly for "a spec was accepted and never converged",
and a paused roll is that state in the one shape the generation pair cannot see, because the
pause and every later `updateStatus` stamp the current generation. At the same XS cost, A closes
the gap for every install that enables the rule set, B only for those who read the page.
Checkable: with the rule, a CR carrying `RollingUpdatePaused=True` for 30 minutes produces a
firing alert; without it, no shipped expression matches that series.

## Work list

1. **XS, no decision needed** *(added 2026-09-27)*: correct the comment at
   [`rolling_update.go:2641-2645`](../../internal/controller/rolling_update.go#L2641-L2645) so it says the empty result ends the data
   roll's work for this pass, not the pass: `reconcileWorkload` goes on to the Sentinel roll (the
   ADR 0026 D11 exception) and, unless ~~that~~ *(review 2026-09-27: a post-update check - the
   Sentinel roll, the no-master recovery or the split-brain check -)* ends the pass, to
   `updateStatus`. Comment only; does not close this ticket. *(Added 2026-09-27, review:)* in the
   same change, make [`rolling-updates.md:18`](../operations/rolling-updates.md) precise: "phase
   `Error` for that pass" becomes "phase `Error` only until the next status write, normally later
   in the same pass" (doc
   only, true today and under every option until the option lands). **Done 2026-09-27**, both
   halves (in `bcc63c9`, verified at `84a39c2`).
2. **Waits on decision D1**: the chosen option's code, tests and documents as listed under
   Options. Under the recommended D this includes the ADR 0010 D4 and CLAUDE.md rule 3
   amendment, dated, naming the T35 dependency.
3. **XS, no decision needed** *(added 2026-09-27 at 84a39c2)*: correct the two test lines that
   promise a halt — the comment at
   [`rolling_update_test.go:3898`](../../internal/controller/rolling_update_test.go#L3898) and the assertion message at
   [`failover_sync_gate_test.go:132`](../../internal/controller/failover_sync_gate_test.go#L132). New wording: the pause
   returns without a requeue, and a later pass re-dispatches on a fresh budget (true today; the
   D1 change rewrites them again when it lands). Optional in the same change: qualify "a paused
   roll" at [`status.md:83`](../operations/status.md#error) the way `rolling-updates.md:18` was
   qualified. Closes the rule-1 urgency.
4. **S, no decision needed** *(added 2026-09-27 at 84a39c2)*: `clearSyncWaitTimestamp`
   ([`rolling_update.go:2664-2674`](../../internal/controller/rolling_update.go#L2664-L2674)) returns its write error, and its two
   callers ([`:2601`](../../internal/controller/rolling_update.go#L2601), [`:2813`](../../internal/controller/rolling_update.go#L2813)) return
   `RollingUpdateResult{Error: err}`, so the next pass retries the clear before any delete or
   failover. A unit test with an interceptor that rejects the **removal** of
   `annotationSyncWaitStarted` — the inverse of `rejectAnnotationArming`
   ([`rolling_update_bounds_test.go:719-730`](../../internal/controller/rolling_update_bounds_test.go#L719-L730)), which rejects
   an Update that carries the annotation and lets a removal through — with a revert check. Extend
   ADR 0010 (D7 or D8, dated) so the rule covers a bound's clear write as well as its arming
   write; D10's "never inherits a leftover stamp"
   ([`0010:288-295`](../adr/0010-every-rolling-update-wait-is-bounded.md)) names the same defect for
   a bounded state, and the sync wait is not a state, so D10 does not cover it as written. Cost of the change: a failing CR write delays the next delete or failover by one
   backoff and writes phase `Error` "Rolling update error" for that pass
   ([`valkey_controller.go:336-338`](../../internal/controller/valkey_controller.go#L336-L338)). Whether
   `clearRecreationWait` should follow is the same question for another wait and out of scope.
5. **Waits on decision D2**: the rule, the ADR 0021 amendment, `monitoring.md` and `status.md`
   text (option A), or the documentation only (option B).
6. **At close**, whichever D1 option lands: replace the five T23 citations (ADR 0002 `:316`,
   ADR 0010 `:812`, ADR 0024 `:531`, ADR 0026 `:771`, `rolling_update.go:2616`) with the ADR that
   carries the decision, `git grep` T23 outside `docs/tickets/`, extract per ADR 0034, archive.

## Decision

Not decided.

## Verification

- Item 1: the comment no longer says the pass ends, `rolling-updates.md:18` no longer says the
  phase is `Error` for the whole pass; `make lint` is green. *(2026-09-27 after the fix: both
  read as required, History; `make lint` was not run.)* *(2026-09-27 at 84a39c2: still not run;
  the change is comments only, and `.golangci.yml:6-18` enables no line-length or comment-format
  linter; `misspell` finds nothing by reading.)*
- Item 3: `git grep -n -i 'spec change' -- 'internal/**/*.go'` no longer finds a pause test
  promising a halt; `make lint`.
- Item 4: the new unit test fails against today's body (revert check) and passes with the
  change; `make test-unit`, `make lint`, `make cyclo`.
- D1 option D: unit tests that the pausing pass returns a positive `DeferredRequeueAfter`, skips
  the Sentinel roll, and on a cluster without Sentinel reaches `checkAndRecoverNoMaster`; each
  fails against today's body except the last, which fails under option C. The grep of the D11
  exception above returns no statement that the pausing pass runs the Sentinel roll. ADR 0010 D4
  names the exception with its date. `make test-unit`, `make lint`, `make cyclo`. A Kind
  reproduction of a pause without Sentinel, if one can be built; otherwise the resume gap stays
  recorded as not measured.
- D1 option C: unit tests show that a pause keeps the state annotation and the drain stamps,
  returns a positive `DeferredRequeueAfter`, and that on a Sentinel cluster the pass that pauses
  does not run the Sentinel roll; each fails against today's body. Same targets.
- D2 option A: the rule renders (`helm template` locally, no CI gate, T58) and its expression
  matches the ValkeyReconcileBlocked shape; `monitoring.md` names nine alerts.

## History

- 2026-09-27: re-verified at 84a39c2. **Checked** by reading at `84a39c2`: `pauseRollingUpdate`
  and every pause site, `clearRollingUpdateState`, `clearSyncWaitTimestamp`, `ensureWaitBound`
  and `waitBoundExceeded`, `replaceNextReplica`, both dispatchers, `countUpdatedPods`,
  `reconcileWorkload`, `handlePostRollingUpdateChecks`, `updateHAStatus`, `checkAndRecoverNoMaster`,
  `checkSteadyStateSplitBrain`, `SetupWithManager`, `health.CheckCluster`, the init script's
  master discovery, `dataOwnershipRepairNeeded`, the condition registry, the collector and the
  eight PrometheusRule rules; ADR 0010 D4, D7, D8, ADR 0026 `:616-619`, archive/039's T15 section;
  git history of `bcc63c9` and `4a7543e`. **Measured** (docker, both pinned lines, commands in
  Fact): an unsynced replica passes `PING`; a replica in the `wait_bgsave` phase of a full sync
  is counted in the master's `connected_slaves` while it reports `master_link_status:down`.
  **Found false or outdated, corrected in place:** "at 4a7543e no tracked sentence promises a
  halt" (two test lines still do); the "~29 False passes per True" figure (no source, and no
  current behaviour — dropped); the appendix's "starts expired" (starts with part of its budget
  spent); "on a Sentinel cluster the unsynced replica makes `updateHAStatus` report `Syncing`"
  (only when the replica is missing from the master's list; a slow full sync reads `OK`,
  measured); the earlier History drift note "references before `:2640` are unchanged, after it
  two lower" (`bcc63c9` shifted `rolling_update.go` by +1 after `:419` and by +3 after `:2640`,
  and `valkey_controller.go` by +1 from `:2288`; the note stays as written, append-only);
  "not decided" for the clear-error fix (it needs no decision). **Locations re-read at 84a39c2**
  and fixed in the links (pure drift): `rolling_update.go` throughout, `valkey_controller.go`
  `:2606-2629`, `:2987-3000`, `:399-410`, ADR 0002 `:316`, ADR 0026 `:616-619`,
  `rolling_update.go:2616`. **Added:** the state stays empty for every sync wait after the first
  pause; an outdated pod does not remain at every pause (T15 fixture); keeping the state switches
  the no-master recovery and the steady-state check off, and the masterless replica phase needs a
  stale record (T35); the pause clears two stall edges without evidence; no shipped alert fires
  for a pause that reads `OK`; the 10 h resync source and the StatefulSet-status re-entry; the
  authentication.md trigger; the ADR 0032 D4 gate opening (disputed consequence, Not verified);
  cross-ticket notes on T18, T35, T40, T51 and archive/039. **Options:** rewritten as decisions
  D1 and D2. Removed: **A** (accept the cycle, change no code, amend D4) — dominated by D, which
  carries the same D4 amendment and for one returned value also closes the resume gap and the
  D11 exception; **B** (`statePaused` held until the generation changes) — turns a retry that
  heals itself into a halt only a human ends: a dataset that needs longer than `syncTimeout`
  halts instead of succeeding on the next budget, fixing the content of a new auth Secret does
  not change the generation (reverting `spec.auth.secretName` would), and in the Flux fleet
  every resume costs a commit to the CR — out of proportion for a low item; the log-only variant
  of the clear-error fix — leaves the defect, because the stored annotation outranks the
  in-memory copy. **Recommendation changed from C to the new D:** C keeps the state and so closes
  the pausing pass's no-master recovery, a regression C's text did not record; D closes the same
  two gaps with one returned value and removes no recovery path. C stays as runner-up with its
  four narrow gains (stamps, stall edges, ADR 0032 D4 gate, T15-shape supervision). The earlier
  citation of ADR 0010 D7/D8 as requiring a failed clear to fail the pass was a miscitation: D7
  and D8 name the arming write, and D7's remedy there is to log and fall back to memory; the
  argument for work list item 4 is the annotation's precedence alone. **Added** decision D2
  (an alert on `RollingUpdatePaused`, A recommended) and work list items 3 (test lines), 4
  (clear error), 5 (D2) and 6 (close). **Frontmatter:** `state` `filed` -> `analysed` (every
  load-bearing claim re-verified, the option set complete, the unverified rest named);
  `urgency` `icebox` -> `now` by rule 1 as this repository applies it to statements false by
  code reading (018, 044, 062, and this ticket's own earlier History): the two test lines. A
  strict reading of "measured-false" would give `later` by rule 4; one reviewer argued for that,
  and the repository convention decided. It falls to `later` (rule 4, item 4) once item 3 lands
  and to `icebox` (rule 5) once item 4 lands too. `severity` stays low, its "medium as a
  documentation lie" comment removed (that half landed in `bcc63c9`); `effort` stays M (D's
  documents dominate); `blocked-by` stays `adr-0010` (D amends D4). **Not verified:** nothing
  was run on Kind, no `go test`, no `make` target; the drain-stamp case, the masterless phase end
  to end, the ADR 0032 D4 consequence and the T15 shape in production (Fact, Not verified).
  **Review of this entry, same day:** measurement 2 re-run on both pins with the same result
  (`connected_slaves:1`, `state=wait_bgsave`, replica `PONG`, `master_link_status:down`,
  `master_sync_in_progress:0`; containers and networks removed). Fixed directly, as text of this
  entry: the pause-site count "eleven" (nine: `:2575`, `:2592` and the seven callers of
  `waitOrPauseForReplicaSync`); the init-script lines (`:504-508`, `:509-517`, `:531-537`); the
  quoted `replicationNotEstablishedReason` message; "measured" for the Sentinel slow-sync gap
  narrowed to the replica count (the phase is read). Added: the qualification that the no-master
  recovery option D keeps is not dataset-aware (T36, measured there), with a Not verified item
  and a T36 cross-ticket note; T35's state; D10 as a related rule for work list item 4.
  Cross-ticket: in the consistency pass of the same day, the T18 note was precised (under C and D
  the pause pass is a holding pass through `dataTierHolding`, so only ADR 0001 `:118`'s reason
  changes), and T18's own note, which still named option C and asked to revisit four places, was
  corrected to agree; the T51 note named an option B that 051's re-verification removed and now
  names 051's reference-change rotation path, which is the pause; T35's stale records and T36's
  recovery are cited consistently from both sides.
  Final pass: the Sentinel resume-gap analysis ("Where the pause does not cycle") and the pause
  message bullet now carry what [T69](069-three-sync-checks-read-a-replica-field-from-the-master.md)
  found - `master_sync_in_progress` is never emitted by a master, so `AllSynced`
  (`checker.go:130`, re-read at `84a39c2`) is the `connected_slaves` count alone, and the
  replica-side "still syncing" branch is unreachable - with how T69's options change the analysis
  (A closes the Sentinel slow-sync resume gap by itself, B nearly, C and D not), and Cross-ticket
  gained a T69 bullet. Frontmatter, D1 and D2 unchanged: the gap without Sentinel and the
  zero-acknowledgement pause on Sentinel stay open under every T69 option.
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
