---
id: T23
title: a stalled or cycling rolling update, the unverified Sentinel reset and the unreported Sentinel deficit
state: analysed       # every part read and verified; every question carries a recommended option
severity: medium      # measured: a tier-wide unverified reset leaves Sentinel unable to fail over, the cycle repeats it, nothing reports either; the pause alone is low
security: hardening   # the states arise with no principal; the Sentinel report only detects
threat: "no attack path is needed for the defects; a Sentinel report would additionally surface a monitor removed or redirected by anyone who can send Sentinel commands (any client on port 26379/36379 with spec.sentinel.disableAuth true, or a holder of the cluster password)"
urgency: now          # rule 1: test lines, comments, a condition message and ADR 0022 D6 state a halt, a cooldown, a cap, an agreement and a never-RESET rule the code breaks
effort: L             # a return value, a gate, a capped counter, two level conditions, alerts, ADR 0010 and ADR 0022 amendments, full e2e on both legs
blocked-by: decision  # Q1-Q8; the independent changes are not blocked
filed-from: T15 D4
opened: 2026-08-26
decided:
done:
---

# T23 - a stalled or cycling rolling update, the unverified Sentinel reset and the unreported Sentinel deficit

**Scope.** A data-tier rolling update can stop without saying so (the sync-timeout pause) or
retry without end (the Sentinel failover cycle); on the Sentinel path both lean on
`resetSentinelState`, which points every Sentinel at an unverified address and can leave the tier
unable to fail over, and nothing reports a Sentinel left with no monitor or too few peers or
replicas. The parts share the holding-pass return value, ADR 0010, ADR 0022, the condition and
alert set, and one full e2e run.

- **Sync-timeout pause:** clears the roll state and returns an empty result.
- **Sentinel reset:** falls back to `SENTINEL RESET` and resets toward any address.
- **Failover cycle:** resets and retriggers without a cap; `maxReconnectResets` restarts.
- **Sentinel deficit report:** no condition sees a missing monitor, peers or replicas.

## Current state

### Shared facts

- A handler returning `NeedsRequeue` ends the pass before the status write
  ([`valkey_controller.go:340-342`](../../internal/controller/valkey_controller.go#L340-L342)).
  Only an `Error` or `Syncing` phase requeues (`:377-380`); with a generation-gated CR watch and
  no Pod watch (`:2987-3000`) a pass without requeue re-dispatches only on an owned-object event,
  a referenced Secret change, a spec change, an operator restart or the 10 h resync.
- `DeferredRequeueAfter: rollingUpdateRequeueDelay` (10 s,
  [`rolling_update.go:203`](../../internal/controller/rolling_update.go#L203)) makes a pass a
  holding pass: Sentinel roll skipped, status write run (ADR 0010 D16/D17, ADR 0026 D5).
- None of the eight shipped alerts
  ([`prometheusrule.yaml:32-170`](../../deploy/helm/valkey-operator/templates/prometheusrule.yaml))
  keys on `RollingUpdatePaused` or on Sentinel state. Every condition is exported as
  `vko_valkey_status_condition` ([`collector.go:186-192`](../../internal/metrics/collector.go#L186-L192)),
  so a new condition is alertable without collector change; ADR 0021 forbids a per-Sentinel gauge.
- Both readiness probes are `PING` ([`statefulset.go:847-857`](../../internal/builder/statefulset.go#L847-L857),
  [`sentinel.go:420-446`](../../internal/builder/sentinel.go#L420-L446)): a replica with
  `master_link_status:down` and a Sentinel with no monitor answer `PONG` and stay Ready.
- Sentinel has no retrigger cooldown: a forced `SENTINEL FAILOVER` is refused only with `-INPROG`
  (per Sentinel) or `-NOGOODSLAVE`; three back to back answered `OK`.
- Measurements: docker, `valkey/valkey:9.1.1` and `8.1.9` identical, one master, two replicas,
  three Sentinels in the operator's config, no TLS, no auth, "unreachable" = `docker pause`.

### Sync-timeout pause

`pauseRollingUpdate` ([`rolling_update.go:2619-2647`](../../internal/controller/rolling_update.go#L2619-L2647))
sets `RollingUpdatePaused=True/SyncTimeout`, writes phase `Error`, emits a Warning, calls
`clearRollingUpdateState` (`:2637`; [`:3423-3497`](../../internal/controller/rolling_update.go#L3423-L3497):
in-memory bounds, `PodTerminationStalled`/`PodRecreationStalled`, the state and nine bound
annotations, the drain stamps) and returns an empty result (`:2646`).

- It breaks ADR 0010 D4 ([`0010:240-245`](../adr/0010-every-rolling-update-wait-is-bounded.md)),
  CLAUDE.md master-authority rule 3 and ADR 0026 `:616-619`: expiry must hand over to a bounded
  state. Only the condition records the pause; the next pass arms a fresh `syncTimeout` and pauses
  again with another Warning. The condition stays `True` until convergence (`:312`) or completion.
- The pausing pass is not a holding pass (`valkey_controller.go:336-342`, `:363`): the Sentinel
  roll runs (`:465-468`, the ADR 0026 D11 exception); without Sentinel the no-master recovery and
  the steady-state check run on the cleared state (`:421-443`); `updateStatus` overwrites `Error`.
- **Resume gap.** Without Sentinel `updateStandaloneStatus` reports `OK` once every pod is Ready
  (`:2235-2260`), so no requeue (`:392-396`): minutes where pods churn, hours in a quiet
  namespace. With Sentinel the pass requeues through `Syncing` (`:2475-2487`) while any replica
  does not report itself synced: `AllSynced` counts replicas by their own replication answer
  ([ADR 0037](../adr/0037-the-master-handover-loses-no-acknowledged-write-and-no-dataset.md) D2),
  so a replica in full sync or still connecting keeps the requeue. Zero WAIT acknowledgements
  with every replica synced read `OK` (`:2488-2500`) and leave the gap.
  `ValkeyPhaseNotOK`, `ValkeyReplicasMissing` and `ValkeySpecNotObserved` stay silent.
- Two test lines promise a halt:
  [`rolling_update_test.go:3898`](../../internal/controller/rolling_update_test.go#L3898),
  [`failover_sync_gate_test.go:132`](../../internal/controller/failover_sync_gate_test.go#L132)
  (their `NeedsRequeue == false` assertions are right); [`status.md:83`](../operations/status.md#error)
  lists "a paused roll" under `Error` without saying it lasts one status write.
- `clearSyncWaitTimestamp` ([`:2664-2674`](../../internal/controller/rolling_update.go#L2664-L2674))
  discards its write error, the only discarded object write in non-test code: the stored
  annotation survives, is read first (`:1220-1222`) and not re-armed (`:1179-1181`), so the next
  sync wait can pause early (safe direction).
- Every pause precedes a promotion or a delete: nine sites, `verifyReplacedReplicasSynced`
  (`:2575`, `:2592`) and seven callers of `waitOrPauseForReplicaSync`, before the failover
  (`:2702-2708`) or `promoteAndRedirect` (`:4034`); state `replacing-replicas` or empty. After the
  first pause the state stays empty (`:2424`, `:2457-2460`) and every wait returns `NeedsRequeue`,
  so the pausing pass is the only one of a paused roll reaching the no-master recovery.
- A non-empty state switches off `checkAndRecoverNoMaster` (`valkey_controller.go:2884-2888`) and
  `checkSteadyStateSplitBrain` ([`steady_state_master.go:158-163`](../../internal/controller/steady_state_master.go#L158-L163)).
  A replica phase with no master (a stale record, T35, then the real master lost at an ordinal
  other than 0) ends in a pause, whose pass promotes pod-0 without a dataset comparison (T35).
- A pause on an up-to-date replica with the remaining ordinals absent or not Ready ends in the
  converged early return (`:296-313`, the T15 fixture shape): nothing supervises that replica.

Impact: low. A roll can sit half-done for hours at phase `OK` with no alert; on Sentinel each
pause releases the Sentinel roll once. No data is lost.

### Sentinel reset

`resetSentinelState` ([`rolling_update.go:3617`](../../internal/controller/rolling_update.go#L3617))
sends every Sentinel `SENTINEL REMOVE`, `MONITOR <name> <addr> <port> <quorum>`, five `SET`s and
the `auth-pass` (`:3666-3693`); a failed REMOVE is followed by `SENTINEL RESET` (`:3669`) and no
MONITOR. An empty address falls back to pod-0 (`:3628-3632`). It never checks the pod, returns
nothing, and every caller carries on. Each command dials anew
([`client.go:429-453`](../../internal/valkeyclient/client.go#L429-L453)), so a REMOVE failing at
dial can be followed by a RESET that succeeds.

| Site | Function | Runs when | Address |
|---|---|---|---|
| `:958` | `checkFinalizationTopology` | finalization stalled (2 min), `masterCount != 1` | pod-0 |
| `:1007` | `syncSentinelWithMaster` | stalled, INFO on the master just failed | that pod |
| `:1049` | `syncSentinelWithMaster` | **final pass of every Sentinel data roll**; stalled, a partial replica count passes | the master, role not re-checked |
| `:3161` | `handleMasterWithNoReplicas` | available new-image master, no replica after 90 s | that master |
| `:3308` | `handleNoMasterFound` | failover timed out (30 s), no available new-image master | first pod the scan marks master (INFO or label, terminating included), else pod-0 |

- The completion hold (ADR 0026 D4) applies only while not stalled (`:912`); past the stall
  `:1049` can name a terminating master. ADR 0026 D4 calls a reset through a dying master
  unrecoverable; it is reachable at `:3308`.
- [ADR 0022](../adr/0022-sentinel-identity-is-pinned-to-the-pod.md) D6 and
  [`CLAUDE.md:743`](../../CLAUDE.md) say the operator never issues `SENTINEL RESET`.
- `TestResetSentinelState_FallsBackToResetWhenRemoveFails`
  ([`sentinel_failover_test.go:1470-1486`](../../internal/controller/sentinel_failover_test.go#L1470-L1486))
  pins the fallback, and its comment claims RESET reverts to pod-0 (it keeps the address).
- The per-master `SET resolve-hostnames`/`announce-hostnames` (`:3685-3686`) always fail: global
  options, already in the config ([`sentinel.go:162-163`](../../internal/builder/sentinel.go#L162-L163)).
- False comments: a Sentinel cooldown at `:140-142`, `:3068-3070`, `:3527-3529`, `:3614-3615` and
  [`client.go:249-251`](../../internal/valkeyclient/client.go#L249-L251); REMOVE + MONITOR called
  "a SENTINEL RESET" at `:151`, `:3545`, `rolling_update_bounds_test.go:760`.
- No command re-points a monitor and keeps its tables. A running Sentinel persists a switched
  address; a restarted pod takes it from the known-master annotation (`:1045`, ADR 0008). The
  finalization reset exists for a Sentinel monitoring a stale address.
- Rebuild window: about 2 s with every replica connected; at `:3161` and a stalled `:1049` until
  the missing replicas connect, and `:3161` drops the replica list Sentinel held.
- A data pod boots as master when a Sentinel names it
  ([`statefulset.go:288-332`](../../internal/builder/statefulset.go#L288-L332)); an `s_down`
  Sentinel still names its address.

| Measured | Result |
|---|---|
| Replies | `REMOVE nosuch`: `ERR No such master with that name`; `RESET nosuch`: `0`; per-master `SET resolve-hostnames`: `ERR Unknown option`; `MONITOR` of an existing name: `ERR Duplicate master name.`, unchanged |
| A: REMOVE + MONITOR on all three, master unreachable | all `s_down`, 0 replicas, 0 peers, no failover; forced failover `NOGOODSLAVE` |
| B / C: address back as master / as replica of a promoted pod | recover to 2/2 within 20 s / stay on it, 0 replicas, 90 s and later |
| D: RESET on one Sentinel only | the other two fail over and heal it within 30 s |
| K: code's shape (RESET on one, REMOVE + MONITOR on two) | all `s_down` 0/0, `NOGOODSLAVE`; recover after unpause |
| E: MONITOR fails after a successful REMOVE | never re-added: later REMOVE `No such master`, RESET `0`, MONITOR skipped |

Impact: medium. A tier reset toward an unreachable master cannot fail over until that address
answers as master; toward a returning replica it stays on it; a Sentinel can lose its monitor for
good (E). Without persistence this can end in an empty master flushing its replicas (T35, not
measured). No pod template changes.

### Failover cycle

- **Loop A, no new master.** `handlePostFailover`
  ([`:3071-3114`](../../internal/controller/rolling_update.go#L3071-L3114)) finds no available
  current pod answering `role:master`; `handleNoMasterFound`
  ([`:3287-3326`](../../internal/controller/rolling_update.go#L3287-L3326)) waits until the
  trigger's stamp is 30 s old **and** no pass has found a master for 30 s (`noMasterTimedOut`, an
  in-memory absence clock), resets
  every Sentinel (`:3308`), rewrites the failover timestamp and enters `stateFailoverReset`;
  `handleFailoverRetrigger` ([`:849-887`](../../internal/controller/rolling_update.go#L849-L887))
  waits 20 s and up to 90 s for Sentinel to know its replicas, then `setFailoverTriggered` and
  `triggerSentinelFailover` (`:3700-3740`; a refusal by all is only logged). About 65 s per cycle,
  up to 2.5 min, and the retrigger skips the first trigger's sync gates (`:2710-2719`).
  Those skipped gates are the route to an empty promotion that
  [ADR 0037](../adr/0037-the-master-handover-loses-no-acknowledged-write-and-no-dataset.md) D4 and
  D5 only hold (its Residual risks, 2026-09-28): the coordinated first trigger cannot promote a
  replica behind the master, the forced retrigger can. The repair `MasterHandoverStalled` names
  for `DatasetWouldBeDiscarded` runs through this retrigger: once Sentinel is pointed at the
  outgoing pod, the resolver demotes the empty new master, which starts a full sync from it, and
  the retrigger fires 20 s after the reset (up to 90 s while Sentinel has not rediscovered the
  replicas) without asking whether that sync is done — so a dataset whose full sync takes longer
  can still be promoted away and lost (read from the code and sentinel.c 9.1.1, not driven; ADR
  0037 *Residual risks*). Closing the route — the retrigger asking
  `waitForReplicasReady` and `WAIT` first, or a coordinated attempt before the forced one — is
  this ticket's, with the cycle questions below.
- **Loop B, a new master with no connected replica.** `handleMasterWithNoReplicas`
  ([`:3145-3187`](../../internal/controller/rolling_update.go#L3145-L3187)) after 90 s sends
  `REPLICAOF` to every other reachable pod (`:3159`) and resets every Sentinel (`:3161`) before it
  checks `maxReconnectResets` (2, `:138`). At the cap it clears the count (`:3170`) without
  rewriting the timestamp, `verifyNewMasterReady` requeues (`:3349-3353`), and the next pass resets
  at once: three resets and three rounds of `REPLICAOF` per round, repeated.
- Nothing caps either loop (`syncTimeout` does not bound the failover; the pause cannot serve,
  because it clears the state). Exits: an available current master (`:3119`), every pod current
  and reachable (`:718-723`), a spec change (`:833-845`), a human removing the annotation. Nothing
  reports it: phase `Rolling Update k/n`, no status write, `Ready` stale, no Event after the first.
- Comments claim a cap: [`:56-58`](../../internal/controller/rolling_update.go#L56-L58),
  `:135-137`, `:3142-3144`, `:3019-3022`,
  [`sentinel_failover_test.go:1279-1281`](../../internal/controller/sentinel_failover_test.go#L1279-L1281);
  `:789-792` is accurate. `:90-94`, `:1369-1373`, `:2721-2725` call the cycle the `NOGOODSLAVE`
  recovery. The test pins only the cap pass.
- Measured: `replica-priority 0` on every replica, or no replica at all, makes every forced
  failover answer `NOGOODSLAVE`, again 20 s later (the operator never sets the priority).
- Drivers (by reading): an old master unreachable on a NotReady node is marked master by its label
  and `:3308` resets the tier toward it, which is measured scenario A and sustains the cycle until
  the node returns; also a current master that is not `available()`, or replicas that cannot
  connect (loop B). ADR 0025 D9 records ten triggers in 9.5 min on a driver since removed.

Impact: medium. The roll never completes and never says so; loop A resets the tier toward an
unverified address about once a minute with an ungated failover (the route to an empty
promotion ADR 0037 records); loop B re-opens ADR 0025 D9's 90 s split-brain window on every timestamp rewrite.

### Sentinel deficit report

`observeSentinels` ([`checker.go:295-345`](../../internal/health/checker.go#L295-L345)) runs only
when every pod is Ready and a master was found
([`valkey_controller.go:2458-2462`](../../internal/controller/valkey_controller.go#L2458-L2462)),
sends `SENTINEL MASTER` and keeps `num-other-sentinels` and whether `flags` is `master`.
`SentinelPeersStale` (ADR 0022 D4, D5) looks only for a surplus (`staleSentinelPods`, `:2415-2424`).

- **No monitor:** `ERR No such master` becomes a plain error
  ([`client.go:479-481`](../../internal/valkeyclient/client.go#L479-L481)) and the pod is skipped
  as "not responding" ([`checker.go:330-334`](../../internal/health/checker.go#L330-L334)).
- **Too few peers** is never selected. **Too few replicas:** `NumSlaves`
  ([`client.go:37-52`](../../internal/valkeyclient/client.go#L37-L52)) never reaches
  `ClusterState`; it counts dead entries, so a short count while the master lists every replica
  means not reading that master, a fresh reset, or a scale-up within 10 s.
- **False all-clear:** "Every Sentinel knows %d other Sentinels, as expected"
  ([`valkey_controller.go:2372-2381`](../../internal/controller/valkey_controller.go#L2372-L2381))
  is written with 0 peers or no monitor too; the same in
  [`valkey_types.go:117-119`](../../api/v1/valkey_types.go#L117-L119) and ADR 0022 D5.
- `ClusterState.SentinelMonitoring` ([`checker.go:41-42`](../../internal/health/checker.go#L41-L42))
  is read by no production code.
- Transients (measured): REMOVE + MONITOR or a new Sentinel, 0 peers for 1 to 2.3 s and 0 replicas
  up to 0.11 s (0.6 s more when `auth-pass` comes late); `SENTINEL RESET`, 0 replicas for 9.4 to
  9.9 s. The completing pass of a data-tier-only roll resets at `:1049` and reads the Sentinels
  milliseconds later with no follow-up (`:391-396`). Durable states: scenarios A, C, E above.

Impact: medium, Sentinel clusters. A tier that cannot fail over shows `Ready=True`, `phase=OK`
and an all-clear claiming agreement; two monitor-less Sentinels of three lose the quorum.

## Required changes

### Shared

Independent of the open questions:

1. **One comment pass**, no behaviour change: the two pause test lines say the pause returns
   without a requeue and a later pass re-dispatches on a fresh budget (optionally `status.md:83`);
   the cooldown comments corrected; "SENTINEL RESET" reworded to REMOVE + MONITOR; the cap comments
   say the count caps resets within one round, the cap pass clears it without rewriting the
   timestamp and the next round starts at once. Checks: `git grep -n -i 'spec change' -- 'internal/**/*.go'`
   finds no pause test promising a halt; `grep -n -i cooldown` on `rolling_update.go` finds only
   `:3665` or corrected text; `make lint`.
2. **ADR 0010, one change:** D7 or D8 covers a bound's clear write as well as its arming write
   (D10, `0010:288-295`, covers bounded states only); Residual risks (`:775-784`) state the
   immediate restart after the cap and lose "Not filed." (ADR 0034). With answers: D4 (Q1), D18 (Q5).
3. **ADR 0022, one change:** Context (`:76-79`) says one reset Sentinel is healed by intact peers,
   a tier reset as a whole stays `s_down` 0/0 until the address answers as master, and a reset
   toward a replica stays on it; D5 says the all-clear means no Sentinel knows more than
   `replicas - 1` others; Residual risks: a monitor lost after REMOVE and a failed MONITOR stays
   missing until the next call. With answers: D6 (Q3, Q4), the report decisions (Q7, Q8).
   `CLAUDE.md:743` stays true under every kept option.

Depending on the answers:

4. The `RollingUpdateResult` doc comment
   ([`rolling_update.go:168-183`](../../internal/controller/rolling_update.go#L168-L183)) and
   `valkey_controller.go:343-354` name every setter of `DeferredRequeueAfter` (Q1, Q5).
5. Conditions and alerts in one change: each new condition (Q5, Q7) with its `conditionRegistry`
   row, README condition table and `docs/operations/status.md`; the new rules (Q2, Q7) in
   `prometheusrule.yaml` and the count in [`monitoring.md:63`](../operations/monitoring.md); a
   local `helm template` (T43).
6. Verification: `make test-unit`, `make test-integration`, `make lint`, `make cyclo`,
   `make generate-all` with no diff, one full e2e on `single-node-valkey9` and `single-node-valkey8`.
7. At close: replace the five T23 citations (ADR 0002 `:316`, ADR 0010 `:812`, ADR 0024 `:531`,
   ADR 0026 `:771`, `rolling_update.go:2616`) with the deciding ADR, extract per ADR 0034, archive.

### Sync-timeout pause

- **Independent:** `clearSyncWaitTimestamp` returns its error; its callers (`:2601`, `:2813`)
  return `RollingUpdateResult{Error: err}`. Unit test with an interceptor rejecting the removal of
  `annotationSyncWaitStarted` (inverse of `rejectAnnotationArming`,
  [`rolling_update_bounds_test.go:719-730`](../../internal/controller/rolling_update_bounds_test.go#L719-L730)),
  revert-checked.
- **(Q1 = D)** `pauseRollingUpdate` returns `DeferredRequeueAfter` (comment `:2641-2645`). Tests,
  revert-checked: positive `DeferredRequeueAfter`; the Sentinel roll skipped (pattern of
  `TestReconcileWorkload_DataAvailabilityStallHoldsTheSentinelRoll`); without Sentinel
  `checkAndRecoverNoMaster` still reached; the existing pause tests (`rolling_update_test.go:3875`,
  `failover_sync_gate_test.go:119`) assert it. Documents: every statement that the pausing pass
  runs the Sentinel roll (ADR 0001, 0010, 0024, 0026, 0027, CLAUDE.md, README.md, `status.md`,
  `valkey_controller.go:405-410`, `api/v1/valkey_types.go:165-168`), found with
  `grep -n 'empty result\|pass that pauses\|in which a data roll pauses\|pass where a data roll\|returns no requeue\|pauseRollingUpdate' docs/adr/*.md CLAUDE.md README.md docs/operations/*.md internal/controller/valkey_controller.go api/v1/valkey_types.go`.
- **(Q1 = C) additionally:** the pause calls `clearSyncWaitTimestamp` instead; rewrite the "pause
  clears the state" sentences (`api/v1/valkey_types.go:58-59`, `rolling_update.go:2609-2613`,
  `:2634-2636`, `status.md:21`, `rolling-updates.md:18`, ADR 0010 `:801-802`, ADR 0024 `:530`,
  ADR 0026 `:617`); amend ADR 0024 and 0026 with the no-master consequence; test state and stamps kept.
- **(Q2 = A)** `ValkeyRollingUpdatePaused`, recorded in ADR 0021 and
  [`status.md#rollingupdatepaused`](../operations/status.md#rollingupdatepaused).

### Sentinel reset

- **Independent:** delete the RESET fallback (`:3668-3672`, keep the log at `:3667`), so a failed
  REMOVE goes on to MONITOR (a duplicate is skipped at `:3676-3679`, a missing monitor re-added);
  delete `valkeyclient.SentinelReset` (`client.go:249-257`) and its tests (`client_test.go:685`,
  `exec_test.go:467`); rewrite the fallback test to REMOVE error, then MONITOR and SETs,
  revert-checked, without its false comment. Check: `git grep -n "SentinelReset(" -- internal cmd`
  is empty.
- **Independent:** delete the two per-master SETs and their expectations
  (`sentinel_failover_test.go:1458-1459`); ADR 0025 `:219` says the scan may take a label-only
  master or pod-0.
- **(Q3 = B)** The gate, the calls at `:958` and `:1007` deleted, ADR 0022 D6 = "REMOVE + MONITOR,
  only toward a verified master, never `SENTINEL RESET`", the gate below `persistKnownMaster`.
  Unit tests, failing with the gate removed or a call restored: named pod terminating, not Ready,
  `role:slave`, not answering, one of two scanned masters at `:3308` (no command); verified pod
  (commands sent); stalled finalization sends nothing. Re-read `rolling_update_test.go:2849`,
  `:2901`, `:4625`.
- **(Q4 = skip)** `SENTINEL MASTER` per Sentinel first. Unit tests: all agree, no REMOVE; one
  disagrees, only it is reset; an error, it is reset; each fails with the check removed.

### Failover cycle

- **(Q5, any option)** Episode counter annotation (example `vko.gtrfc.com/failover-attempts`)
  written by `setFailoverTriggered` in the same update as state and timestamp (ADR 0010 D10, D14),
  removed by `clearRollingUpdateState`, in the README annotation table. Level condition (example
  `SentinelFailoverStalled`, ADR 0027): True past the cap, cleared in `handleNewMasterFound` with a
  connected replica and in `clearRollingUpdateState`, presence-guarded; past the cap the pass
  returns `DeferredRequeueAfter`. No Event.
- **(Q5 = B)** Past the cap no reset and no `stateFailoverReset`; the state stays
  `failover-triggered` and each pass scans for a master. Loop B's count lives for the episode,
  checked before `:3159` and `:3161`, no clear at `:3170`. Resume procedure in
  `docs/operations/rolling-updates.md` (`SENTINEL FAILOVER` to one Sentinel, or remove state and
  counter); `:90-94`, `:1369-1373`, `:2721-2725` describe the capped cycle.
- Tests: N + 1 cycles with every Sentinel answering `NOGOODSLAVE` assert count and condition, fail
  with the counter write removed; a failing CR update shows counter and state are one write. Under
  B: past the cap no `FAILOVER`, `REMOVE` or `MONITOR`; at loop B's cap no `REPLICAOF`; a master
  appearing during the hold continues the roll; each fails with the cap check removed (loop B also
  with the clear at `:3170` restored). An e2e is optional (no deterministic fixture; ADR 0017).

### Sentinel deficit report

- **Independent:** the all-clear message (`valkey_controller.go:2379-2380`) becomes "No Sentinel
  knows more than %d other Sentinels", doc comment matching; a unit test in
  [`sentinel_peer_drift_test.go`](../../internal/controller/sentinel_peer_drift_test.go) with one
  Sentinel at 0 peers fails with the old message.
- **Independent, landing with Q7:** delete `SentinelMonitoring`, `sentinelObservation.agreeing`
  and `monitoring()`, moving the assertions (`checker_live_test.go`, `checker_paths_test.go`,
  `checker_test.go`, `valkey_controller_test.go`) onto per-Sentinel fields.
- **(Q7, Q8)** A typed reply error in `valkeyclient` where only `No such master with that name`
  means monitor-missing; `NumSlaves` and the monitor-missing set in `ClusterState`; one evaluator
  in `updateHAStatus` beside `recordSentinelPeerDrift` with Q8's debounce and a 5 min recheck
  while True; condition, reasons and registry row (level, one evaluator); alert
  `ValkeySentinelMonitorDegraded` shaped like `ValkeyReconcileBlocked`, `for: 15m`, warning; ADR
  0022 Residual risks (no report without a master or with a pod not Ready, no address comparison,
  a restart restarts the clock); one sentence in CLAUDE.md's Sentinel section.
- Tests (`NewValkeyClientFn`, `fakeValkeyServer(t)`), each failing with its clause removed:
  `No such master` counts, a refused connection and an auth refusal do not; 0 peers raises; short
  replicas raise only with `AllSynced`; no answer writes nothing over `True`; a deficit seen once
  writes no `True` and requests a recheck, past the bound `True`, none `False`. E2E on both legs:
  a data-tier-only roll never shows `True`; `SENTINEL REMOVE <monitor>` on one pod gives
  `True`/`SentinelMonitorMissing` naming it, deleting the pod gives `False`.

## Open questions

### Q1: What should a sync-timeout pause leave behind, and what should the pausing pass return? (pause)

Today it clears the whole roll state and returns an empty result: the Sentinel roll is released
and a pass reading `OK` schedules no recheck. Both options return `DeferredRequeueAfter`; they
differ in whether the state is cleared. Neither touches a pod template or a delete.

- **D - keep the clear, hold and requeue (recommended).** The no-master recovery keeps running in
  the pausing pass. ADR 0010 D4 and CLAUDE.md rule 3 name the pause as the one expiry that clears
  the state, depending on that recovery (T35). One Warning per `syncTimeout`; the phase alternates.
  S in code, M in documents.
- **C - keep the state, clear only the sync wait.** D4 holds as written; drain stamps, stall edges
  and the ADR 0032 D4 repair gate are kept. But the kept state switches off the no-master recovery
  while paused, so a replica phase with no master stays unwritable until a human acts. M.

D: C's gains are narrow latent issues and its cost is a write outage; every pause precedes the
promotion and the recheck runs `resolveSplitBrain` (`:3909`). D is strongest once T35 lands.

**Answer:** _open_

### Q2: Should the chart alert on `RollingUpdatePaused`? (pause)

The condition stays `True` from the first pause until convergence and is the one stable signal;
the phase alternates. An install with the rule set enabled gets one more alert.

- **A - `ValkeyRollingUpdatePaused` (recommended):** the condition series `== 1`, guarded by
  `vko_valkey_collector_success`, `for: 30m`, warning, shaped like `ValkeyReconcileBlocked`. XS.
  It also fires on a roll slower than 30 min after one pause: a `syncTimeout` too short.
- **B - no rule, document the query.** XS; every fleet user writes the same rule.

A: ADR 0021 alerts on an accepted spec that never converged, and a paused roll is that state in
the one shape the generation pair cannot see.

**Answer:** _open_

### Q3: Which master may `resetSentinelState` point every Sentinel at? (reset)

Today any address, or pod-0, unchecked; a reset toward an unreachable master or a returning
replica leaves the tier unable to fail over. No caller's state or bound changes either way.

- **A - no gate.** XS. The empty-table state stays reachable at `:958`, `:1007`, `:3308` and a
  stalled `:1049`, where the master is least trustworthy.
- **B - gate on a verified master, delete the two stall resets (recommended).** The named pod must
  be `available()` and answer `role:master` now, else log and return; delete `:958` (never
  verifiable) and `:1007` (INFO just failed); at `:3308` also the only scanned master; at `:3161`
  no uniqueness check (two masters is the designed post-failover state). S; one INFO per roll.

B: a skipped unverified reset loses nothing (measured), and it needs no state. A test depending on
a stalled finalization resetting Sentinel would be the reason to re-weigh A.

**Answer:** _open_

### Q4: Does a verified reset blank every Sentinel, or only those that disagree? (reset, builds on Q3 = B)

The loop blinds the whole tier for the rebuild window at the end of every Sentinel roll and at
`:3161`; peers heal one reset Sentinel, not a whole tier. `:3308` stays unconditional.

- **Keep the loop, record the window** in ADR 0022. XS; every roll keeps blanking the tier.
- **Skip each Sentinel naming the verified master (recommended):** `ip` equal, `flags` exactly
  `master`, and at a non-stalled `:1049` `num-slaves` at least the expected count; an error counts
  as disagreeing. One round trip per Sentinel.

Skip: it keeps exactly the stale-address case the reset exists for. It does not replace Q8
(manual RESET, scale-up).

**Answer:** _open_

### Q5: What does the roll do once its failover has cycled N times without a usable new master? (cycle)

Today it retries forever; the per-step bounds, the first trigger and ADR 0025 D9 stay, and ADR
0010 D4 forbids clearing the state on expiry.

- **A - report only.** S; the resets and failovers go on.
- **B - cap and hold (recommended).** After N + 1 failovers stop resetting and retriggering, keep
  the state, keep watching for a master (Sentinel's own failover or a human); loop B at most
  `maxReconnectResets` rounds per episode. M; a late-clearing cause needs Sentinel or a human.
- **C - back off** with a doubling wait up to a ceiling (for example 30 min). M; unbounded, slower.

B: each cycle costs a tier-wide reset and an ungated failover, the known drivers do not clear by
retrying, and stop-acting-keep-observing is ADR 0010 D16/D17's shape; C's advantage narrows
further under Q3 = B.

**Answer:** _open_

### Q6: How many retriggers N before the cap? (cycle, only if Q5 = B or C)

A few retries cover a Sentinel still rediscovering its replicas; no recovering failover has been
recorded needing more. With N = 3 the hold begins about 3 min after the first trigger, at most 8.

- **N = 3 (recommended):** covers the transient with margin, bounds the hold to minutes.
- **A larger N (for example 5):** more room, proportionally more resets and failovers.

**Answer:** _open_

### Q7: Are the Sentinel deficits reported on `SentinelPeersStale` or on a new condition? (report)

The health pass already has, per Sentinel, the error or `flags`, `num-slaves` and
`num-other-sentinels`; no new command, no repair, `Ready` and `phase` unchanged.

- **A - widen `SentinelPeersStale`.** S; one condition with contradicting remedies (RESET fixes a
  surplus, not a missing monitor) and a changed meaning under existing alerts.
- **B - new level `SentinelMonitorDegraded` (recommended):** reasons `SentinelMonitorMissing`,
  `SentinelPeersMissing`, `SentinelReplicasMissing` (only while `AllSynced`), one per pass in that
  order, naming every pod; `False`/`SentinelMonitorsComplete`; nothing when no Sentinel answered.
  M; every Sentinel CR gains it as `False` (status write, no roll).

B: one remedy per condition, covering exactly the states in which a tier cannot fail over.

**Answer:** _open_

### Q8: How does the deficit report stay silent through a legitimate rebuild? (report)

A data-tier-only roll reads the Sentinels inside the window with no follow-up, and a manual RESET
shows 10 s of 0 replicas; undebounced, `True` lasts until the next event or the 10 h resync.

- **A - withhold until 90 s old, clock in memory (recommended):** a per-CR tracker (the
  `nudgeTracker` shape) and `requestRecheck(90s)`; `True` if still seen, `False` and forget if
  clean. About nine times the longest window; a restart restarts the clock.
- **B - `True` at first sight**, persisted after a bound or debounced by the alert's `for:` (S).
  Survives a restart, but `True` appears on every such roll and manual RESET.

A: the states worth reporting last until someone acts, so one bound late loses nothing.

**Answer:** _open_

## Not verified

- Nothing ran on Kind or in `go test`: the resume gap and both loops are traced by reading; the
  Sentinel side is measured in docker only (no TLS, auth, CoreDNS, termination instead of pause).
- Whether `:1049` completes in production (a fleet observation of peer tables 4/3/2 suggests not),
  and whether a data-tier-only roll's last pass reads a Sentinel inside the window on a cluster.
- Whether anything depends on the routine all-Sentinel wipe, and that Sentinel's `ip` is the pod
  FQDN in Kubernetes (Q4).
- The sidecar labeler trusting the first Sentinel (`labeler.go:135-141`) under a split `:958`
  reset; a forced failover during `-INPROG`; client impact per retrigger.
- Whether the pause loses a sole master's drain stamp for adoption, lets the no-master recovery
  promote an empty replacement at pod-0, or opens the ADR 0032 D4 repair gate under a roll (Q1 = D).
- Whether `ValkeyPhaseNotOK` ever completes its 30 min for a paused or cycling roll.

## Related

- T15: origin of the pause finding and its fixture shape.
- T18: `Ready` stays stale while a roll holds or cycles; the deferred requeue lets the status write run.
- T34: lists the reset callers and the `config_epoch` restart a reset causes.
- T35: stale master records make Q1 = C a regression; `persistKnownMaster` stays above the gate.
- T35: no dataset-aware no-master recovery; a tier that cannot fail over is a route into it.
- T40: rewrites the T23 citations outside `docs/tickets/` at this ticket's close.
- T50: a changed auth Secret pauses the roll; under Q3 = B a master on the old password fails the gate.
- T43: no CI gate renders the chart (Q2, Q7).
- [T12](archive/012-the-master-handover-loses-no-acknowledged-write-and-no-dataset.md) (done): closed the Sentinel slow-sync gap of the pause and vetoed `forceReplicaConnections` on the dataset; the retrigger it left ungated, and its repair routes through, is this ticket's.
