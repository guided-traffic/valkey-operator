---
id: T75
title: The Sentinel failover's reset-and-retrigger cycle has no cap, and maxReconnectResets restarts instead of capping
state: analysed       # facts re-read at 84a39c2, two docker scenarios on both pinned images (P run twice, S once), every open decision carries a marked option (History 2026-09-27)
severity: medium      # each cycle repeats a Sentinel reset of every Sentinel (T62: measured to leave the whole tier unable to fail over when the named master is unreachable) and a SENTINEL FAILOVER, with no end and no report but a default-off 30 min alert. Below high: the one measured multi-replica driver was removed by ADR 0025 D9 (released in v1.13.0) and the remaining drivers found need a concurrent failure (by reading)
security: none        # no principal and no guarantee of the security model; an availability and convergence defect of the rolling update
urgency: next         # rule 3, derived top-down: rule 1 checked and not matched - no unreleased feature, and the comments at rolling_update.go:56-58, :135-137, :3142-3144 and sentinel_failover_test.go:1279-1281 are false by reading of the operator code, not measured (the same derivation as T73, filed from the same host; T18, T23 and T62 read rule 1 more loosely, History); rule 2 not matched; rule 3 matched: severity medium and the trigger live - released since v1.0.0 and reachable without any option (Fact)
effort: M             # the recommended option B: a counter in the state's own write, a hold branch that reports a level, a level condition with its conditionRegistry row, the no-replica branch counted per failover, unit tests with revert checks, the full e2e on both legs, ADR 0010 D18, docs/operations status and rolling-update pages with the resume procedure, the README annotation and condition tables
blocked-by: decision  # D1 below; the comment corrections and the ADR 0010 precision are not blocked
filed-from: T62 Work list 8c (ADR 0010 Residual risks, "Not filed"), re-verification of 2026-09-27
opened: 2026-09-27
decided:              # not decided
done:                 # not done
---

# T75 - The Sentinel failover's reset-and-retrigger cycle has no cap, and maxReconnectResets restarts instead of capping

Filed on 2026-09-27 from [T62](062-resetsentinelstate-falls-back-to-sentinel-reset.md) (Work
list 8c, Cross-ticket findings) during the re-verification at `84a39c2`. ADR 0010 records the
finding as a residual risk and ends it with "Not filed"
([0010:775-784](../adr/0010-every-rolling-update-wait-is-bounded.md)); the measurement that
first showed it is in [ADR 0025](../adr/0025-a-split-brain-warning-means-one-that-did-not-resolve-itself.md)
D9 (`:195-206`). Every location below is read at `84a39c2`; `rolling_update.go` is
`internal/controller/rolling_update.go`.

## Fact

**Mechanism, loop A (no new master).** Every Sentinel-enabled cluster rolls its data tier
through `handleRollingUpdate` (dispatch at
[`rolling_update.go:321-323`](../../internal/controller/rolling_update.go#L321-L323)). Its failover moves between two states and nothing counts how often:

1. `stateFailoverTriggered`: `handlePostFailover`
   ([`rolling_update.go:3071-3114`](../../internal/controller/rolling_update.go#L3071-L3114))
   scans for a pod that is on the current template, `available()` and answers `role:master`.
   If none does, `handleNoMasterFound`
   ([`rolling_update.go:3287-3326`](../../internal/controller/rolling_update.go#L3287-L3326))
   requeues until `failoverRetryTimeout` (30 s,
   [`:143`](../../internal/controller/rolling_update.go#L143)) has passed since the failover
   timestamp (`:3290`), then resets every Sentinel (`resetSentinelState`, `:3308`, toward the
   first pod the scan marked master, else pod-0, `:3628-3632`), clears the Sentinel-awareness
   bound (`:3312`), rewrites the failover timestamp (`:3315`) and enters `stateFailoverReset`
   (`:3321`).
2. `stateFailoverReset`: `handleFailoverRetrigger`
   ([`rolling_update.go:849-887`](../../internal/controller/rolling_update.go#L849-L887)) waits
   `failoverResetMinWait` (20 s, `:155`, checked at `:855`), then waits for Sentinel to know the
   expected replicas (`spec.replicas - 1`, `:864`), bounded by a fresh `sentinelAwarenessTimeout`
   (90 s, `:133`; `:865-873`), then calls `setFailoverTriggered` (`:878`, state and timestamp in
   one update, ADR 0010 D14) and `triggerSentinelFailover` (`:882`), which tries each Sentinel
   ordinal until one accepts
   ([`:3700-3740`](../../internal/controller/rolling_update.go#L3700-L3740)); a refusal by every
   Sentinel is only logged (`:883`). Back to 1.

Each step is bounded; the cycle is not. A cycle takes about 50 s (30 s + 20 s + requeue
granularity) and up to about two and a half minutes (30 s + 20 s + 90 s) when the awareness wait
runs out, which it does whenever the reset left the Sentinels knowing fewer replicas than
expected (the self-sustaining driver below). The exits are: an available current pod answering
`role:master` (`handleNewMasterFound`, `:3119`); every pod on the current template and
`reachable()`, so `updatedCount == totalPods` sends the pass to `finalizeRollingUpdate`
(`:718-723`) - which needs the outdated old master to be deleted by someone else and recreated
on the current template (by reading); a spec change that leaves no pod replaced, so
`clearStaleRollingUpdateState` drops the state (`:833-845`, called at `:735`); or a human
removing the state annotation. There is no counter, no episode deadline, no condition and no
Event: the first trigger records the Normal Event `FailoverTriggered` (`:2748`);
`handleNoMasterFound` and the retrigger record none and only log at the default level ("Failover
timed out, resetting sentinel state and scheduling retry", `:3295`; "Retriggering sentinel
failover after reset", `:876`).

**Mechanism, loop B (a new master with no connected replica).** When the new master answers but
reports `connected_slaves:0`, `handleMasterWithNoReplicas`
([`rolling_update.go:3145-3187`](../../internal/controller/rolling_update.go#L3145-L3187))
waits until `replicaReconnectTimeout` (90 s, `:149`) has passed since the failover timestamp,
then sends `REPLICAOF` to every other reachable pod (`forceReplicaConnections`, `:3159`) and
resets every Sentinel (`:3161`), **before** it looks at the counter. Below `maxReconnectResets`
(2, [`:138`](../../internal/controller/rolling_update.go#L138)) it increments the count and
rewrites the failover timestamp in one update (`incrementReconnectResetCount`, `:3178`,
`:3244-3258`). At the cap it clears the count (`:3170`, `clearReconnectResetCount`,
`:3261-3270`, which writes only the count) and tail-calls `replaceRemainingPods` (`:3173`), whose
`verifyNewMasterReady` requeues while `connected_slaves` is 0 (`:3349-3353`). The failover
timestamp is not rewritten on the cap pass: the only writers are `incrementReconnectResetCount`
(`:3249`), `setFailoverTriggered` (`:3514`) and `setFailoverTimestamp` (`:3523`). So the next
pass, one `rollingUpdateRequeueDelay` (10 s, `:203`) later, finds the count at 0 and the stamp
already older than 90 s, and resets at once. One round is three resets and three rounds of
`REPLICAOF` at about 0 s, 90 s and 180 s, the next round starts about 10 s later, and it repeats
for as long as the new master has no connected replica.

**Verified (read at `84a39c2`):**

- **Nothing bounds loop A.** No counter is read or written on the path `handleNoMasterFound` ->
  `handleFailoverRetrigger` -> `setFailoverTriggered`; the annotations `clearRollingUpdateState`
  removes (`:3447-3470`) are the complete list of roll state, and none of them counts loop A's
  attempts or records an episode start (the one count among them, `reconnect-reset-count`,
  counts loop B's resets within one round, below). `spec.rollingUpdate.syncTimeout`
  (`GetSyncTimeout`, `api/v1/valkey_types.go:1450-1455`) bounds replica sync and pod
  availability, not the failover.
- **`maxReconnectResets` does not bound loop B either, and never bounds loop A.** It is read
  only in `handleMasterWithNoReplicas` (`:3163`); loop A does not pass through it. It counts
  consecutive resets within one round and the cap pass clears it (above). Clear-at-cap is as
  old as the counter (`2926077`, 2026-02-28, "fix: e2e tests", first in v1.1.0; the cap branch
  of that commit already reset and sent `REPLICAOF` before the check).
- **The comments say the opposite.** `annotationReconnectResetCount`
  ([`:56-58`](../../internal/controller/rolling_update.go#L56-L58)): "Used to break the infinite
  loop that occurs when replicas never reconnect via sentinel alone." `maxReconnectResets`
  ([`:135-137`](../../internal/controller/rolling_update.go#L135-L137)): "After this many resets
  we send direct REPLICAOF commands and proceed with the rolling update regardless."
  `handleMasterWithNoReplicas`
  ([`:3142-3144`](../../internal/controller/rolling_update.go#L3142-L3144)): "After
  maxReconnectResets attempts the function proceeds with the rolling update regardless,
  breaking the infinite retry loop." The test comment
  ([`sentinel_failover_test.go:1279-1281`](../../internal/controller/sentinel_failover_test.go#L1279-L1281)):
  "After maxReconnectResets the operator stops resetting and hands over to the rolling update".
  The comment in `replaceRemainingPods` (`:3019-3022`) says a head gate "would ... reopen the
  infinite retry loop it exists to break". The test itself only pins the cap pass (the count is
  cleared, `REPLICAOF` is still sent, the old master is not deleted,
  `sentinel_failover_test.go:1282-1306`); no test drives the pass after it. The accurate
  statement already exists in the doc comment of `resolveSplitBrainUnlessFailingOver`
  (`:789-792`: "with no overall cap while the promoted master has no connected replica, because
  the pass that reaches maxReconnectResets clears the count"), in ADR 0010 (`:778-781`) and in
  ADR 0025 (`:237-241`, `:372-374`); none of the three says when the count starts over.
- **The cycle was built as the recovery path for `NOGOODSLAVE`.** `annotationSentinelAwarenessStarted`
  (`:90-94`) and `isSentinelAwarenessStalled` (`:1369-1373`) say that past the awareness bound
  the roll triggers anyway and "the existing NOGOODSLAVE retry cycle will handle recovery"; the
  first trigger's comment (`:2721-2725`) says a trigger before Sentinel knows the replicas
  "results in NOGOODSLAVE, wasting ~75s on the retry cycle". The transient case it was written
  for is a Sentinel that has not yet rediscovered the replicas after a reset; that retries did
  recover in CI is inferred from the provenance (`a6a1a19`, "fix: e2e tests"), and how many
  retries they needed is not recorded.
- **What each cycle does.** Loop A: one `resetSentinelState` over every Sentinel (REMOVE +
  MONITOR + SETs, toward an address the operator does not verify, T62) and one
  `SENTINEL FAILOVER` - which, unlike the first trigger, is sent without `waitForReplicasReady`
  or `waitForWriteSync` (`:2710-2719` run only on the first trigger; T73 reads this as a route
  to an empty promotion). Loop B: one `forceReplicaConnections` (a `REPLICAOF` to every
  reachable pod other than the new master, with no role or dataset check, T73) and one
  `resetSentinelState` per reset.
- **Sentinel does not stop the retrigger.** Carried from T62 (read at the pinned tags and
  measured, both images): a forced `SENTINEL FAILOVER` is refused only with `-INPROG` or
  `-NOGOODSLAVE` (valkey 9.1.1 `src/sentinel.c:3940-3947`, 8.1.9 `:3875-3881`); three forced
  failovers 12 to 20 s apart all answered `OK` (T62 scenario F5). `-INPROG` is per Sentinel and
  `triggerSentinelFailover` moves to the next ordinal on any error (by reading).
- **A self-sustaining driver on multi-replica clusters exists by reading, on measured Sentinel
  behaviour.** Once the cycle runs for another reason (the first failover produced no available
  current master within 30 s) and the old master then becomes unreachable while its pod stays (a
  NotReady node), the scan at `handleNoMasterFound` marks it master by its label when INFO fails
  ([`:1951-1961`](../../internal/controller/rolling_update.go#L1951-L1961)), `:3308` resets
  every Sentinel toward it, and T62 measured that state on both images: every Sentinel at
  `s_down` with 0 replicas and 0 peers, a forced `SENTINEL FAILOVER` answering
  `NOGOODSLAVE No suitable replica to promote` (T62 scenarios A, A2, K), recovering within 20 s
  only once the address answers as a master again (scenario B). So every retrigger fails and the
  next timeout resets the tier toward the same address, for as long as the pod does not answer;
  when the node returns, the next reset toward the answering address lets the cycle complete.
  An old master that is unreachable already at the first trigger does not start it: a forced
  failover does not need the master (by reading).
- **Any persistent `NOGOODSLAVE` drives loop A.** Measured 2026-09-27 (below): with
  `replica-priority 0` on every replica, `SENTINEL FAILOVER` answers
  `NOGOODSLAVE No suitable replica to promote` on two Sentinels in a row, on both pinned images.
  The operator never sets `replica-priority` (`git grep -n "replica-priority\|slave-priority"`
  in `api/`, `internal/`, `cmd/` and `deploy/` finds nothing), no CRD field passes Valkey
  configuration through, and a runtime `CONFIG SET` on a replica does not survive that replica's
  replacement, which the roll performs before its failover (by reading); so this is a fixture and
  not a production trigger. It shows that the operator's cycle has nothing but the retry for a
  refusal that does not clear.
- **The phase does not change during the cycle, and neither does the rest of the status.**
  `handleRollingUpdate` writes `Rolling Update <updated>/<total>` on every pass before the
  dispatch (`:726-727`), and neither handler writes another phase (the first trigger writes
  `Failover in progress`, `:2746`, and the next pass overwrites it). Every handler of the cycle
  returns `NeedsRequeue`, so the pass ends before the status write
  ([`valkey_controller.go:340-342`](../../internal/controller/valkey_controller.go#L340-L342))
  and `Ready` keeps the value of the last pass that reached it (T18's shape); a failed CR write
  in the cycle writes phase `Error` instead (`valkey_controller.go:336-338`). The shipped
  `ValkeyPhaseNotOK` alert (`for: 30m`,
  `deploy/helm/valkey-operator/templates/prometheusrule.yaml:73-85`) would therefore fire after
  30 minutes of a stable phase string, but the PrometheusRule is off by default
  (`values.yaml:137-143`, `enabled: false`). No condition in `api/v1/valkey_types.go:24-260`
  reports a failover that does not complete.
- **Provenance.** `stateFailoverReset` and the retrigger came in `a6a1a19` (2026-02-18, "fix: e2e
  tests", first in v1.0.0); `maxReconnectResets` in `2926077` (v1.1.0). ADR 0025 D9's guard,
  which removed the measured driver, is `b13377e` (2026-09-26, first in v1.13.0).

**Measured.**

- *Recorded in ADR 0025 D9 (`:195-206`), not re-run:* Kind, 2026-09-26, Valkey 8, the
  observer-enabled Sentinel cluster `hard`, before D9: the observer turned unready during the
  failover, a pass one second after the trigger demoted the promoted replica, Sentinel timed
  out, and the cycle ran ten triggers, ten demotions of the promoted replica `hard-1` with
  `hard-0` as the authority and nine timeouts, from the first trigger until the test's
  ten-minute wait gave up about nine and a half minutes later (about 57 s per cycle). On
  Valkey 9 the same cluster saw one trigger, one benign demotion of the outgoing master, and
  completed. On the first version of D9 (before its clock) the recorded runs logged four
  triggers (one per run of the test), zero demotions and zero timeouts for `hard` (ADR 0025,
  `:396-403`); the rerun with D9's clock recorded no such counts (`:410-416`), and no e2e
  reproduces the window deterministically (`:416-419`). The operator log itself was not re-read
  for this ticket.
- *2026-09-27, docker, this ticket, scenario P (`replica-priority 0`):* `valkey/valkey:9.1.1` and
  `valkey/valkey:8.1.9`, identical results. A docker network `vko-file-075-<tag>`, one master
  (`valkey-server --port 6379 --save '' --appendonly no`), two replicas (the same plus
  `--replicaof <m> 6379`), three Sentinels from a file with `port 26379`,
  `sentinel monitor mymaster <m> 6379 2`, `down-after-milliseconds mymaster 5000`,
  `failover-timeout mymaster 60000`, `parallel-syncs mymaster 1`, `resolve-hostnames yes`,
  `announce-hostnames yes` (down-after and failover-timeout as the operator generates them,
  `internal/builder/sentinel.go:157-158`); after 15 s `SENTINEL MASTER` on s1 showed
  `flags master`, `num-slaves 2`, `num-other-sentinels 2`. Then `CONFIG SET replica-priority 0`
  on both replicas (`OK`), 12 s later `SENTINEL REPLICAS mymaster` on s1 showed
  `slave-priority 0` for both, `SENTINEL FAILOVER mymaster` on s1 answered `NOGOODSLAVE No
  suitable replica to promote`, the master still answered `ROLE` `master` 5 s later, and the same
  command on s2 answered `NOGOODSLAVE` again. After `CONFIG SET replica-priority 100` on both and
  12 s, `SENTINEL FAILOVER` on s3 answered `OK`; 10 s later the old master still answered
  `master`, and the completion of that failover was not followed. Run once by the filing pass
  and once more by its review, same steps (containers `vko-file-075-rv9-*`, `vko-file-075-rv8-*`),
  same results on both images.
- *2026-09-27, docker, review of this ticket, scenario S (no replica):* same images, same
  Sentinel file, one master and no replica (network `vko-file-075r-<tag>`). After 12 s
  `SENTINEL MASTER` on s1 showed `flags master`, `num-slaves 0`, `num-other-sentinels 2`;
  `SENTINEL FAILOVER mymaster` on s1, s2 and s3 each answered `NOGOODSLAVE No suitable replica to
  promote`, the same three commands 20 s later answered the same, and the master still answered
  `ROLE` `master`. Identical on both images.
- Every container and network of both scenarios was removed (`docker ps -a`, `docker network ls`
  show none with the prefix). The setup differs from Kubernetes: no TLS, no auth, no operator.
  The scripts lived in the ephemeral scratchpad; every step is listed above.

**Not verified:**

- **Loop A or loop B driven by the operator after D9.** No Kind or e2e run drove either loop
  on the current code; the self-sustaining driver (an unreachable old master whose pod stays)
  and loop B's restart are by reading, with the Sentinel side measured.
  No unit test drives the pass after the cap.
- **Other drivers**, each by reading only: a promoted current pod that answers `role:master` but
  is not `available()` (its readiness fails) or whose INFO the operator cannot read (TLS or
  password drift), so `handlePostFailover` never takes it, and the retrigger then asks Sentinel
  to fail over away from it, possibly back to the outdated old master; a new master whose
  replicas cannot connect (loop B).
- **Client impact per cycle.** That each accepted retrigger is a role change clients see as a
  write interruption is by reading; the measured run logged ten promotions and ten demotions of
  `hard-1` but no client measurement.
- **Whether the phase string really stays constant** long enough for `ValkeyPhaseNotOK`:
  `countUpdatedPods` counts `reachable()` current pods (`:1981-1989`), so a pod that flaps
  changes the label, and a failed CR write sets phase `Error`, each restarting the alert's
  `for:` clock. Not measured.

## Impact

Every Sentinel-enabled cluster during a data-tier rolling update once the roll's own failover
does not produce an available current master (loop A) or produces one that never gets a
connected replica (loop B); every driver found needs a concurrent failure (by reading), apart
from the `replica-priority 0` fixture (Fact).

- **The roll never completes and never says so.** The phase stays `Rolling Update k/n`, the
  status write does not run, no condition and no Warning Event is written; the only signals are
  the default-level log lines and, 30 minutes in, the default-off `ValkeyPhaseNotOK` alert.
- **Every cycle repeats the operations T62 measured as hazardous.** Loop A resets every Sentinel
  toward an unverified address once per cycle, about once a minute: toward an unreachable master
  that is the tier-wide `NOGOODSLAVE` state (T62 A2, K), which the next cycle re-creates before
  it can heal. Loop B adds a `REPLICAOF` to every other reachable pod three times per round (T73:
  that can re-point a data holder at an empty master, by reading, with the Valkey side
  measured). T62's recommended gate (its D1, option B) would skip the unverified resets inside
  the cycle but would not cap the cycle.
- **Every accepted retrigger is a new failover, without the first trigger's gates.** In the
  measured run that was ten promotions and ten demotions of the same replica in nine and a half
  minutes. D9 removed that driver; the loop that repeated it is unchanged, and each retrigger is
  one more draw of T73's route to an empty promotion.
- **Loop B keeps re-opening ADR 0025 D9's window.** Each timestamp rewrite re-opens the 90 s in
  which the split-brain resolver only reports; ADR 0025 (`:237-243`) records that the resolver
  still runs once per window. Not a new effect, but it repeats without end.
- **Documentation:** a contributor who reads the comments at `:56-58`, `:135-137`, `:3142-3144`
  or `sentinel_failover_test.go:1279-1281` concludes that the no-replica branch is capped; it
  is not.
- **No roll.** No option touches a pod template, so nothing here rolls the fleet
  ([ADR 0005](../adr/0005-upgrade-neutral-defaults-and-anti-affinity.md) D1).

## Options

One decision. The comment corrections and the ADR 0010 precision need none (Work list).

### D1 - What does the roll do once its Sentinel failover has cycled N times without a usable new master?

**Mechanism.** Today a failover episode (from the first `setFailoverTriggered` in
`handleMasterFailover` until a current, available master with a connected replica lets
`replaceRemainingPods` proceed, or until the state is cleared) retries without limit: loop A
resets every Sentinel and retriggers about once a minute, loop B resets and sends `REPLICAOF`
three times per round of about 190 s, and nothing records how many times either ran. The choice
decides what happens after the Nth cycle of one episode. It does not change the per-step bounds
(30 s, 20 s, the 90 s awareness wait, the 90 s reconnect wait), the first trigger, the
`verifyNewMasterReady` gate in front of the old master's delete, which Sentinels are reset and
toward what (T62 D1 and D2), ADR 0025 D9's window, or any pod template. ADR 0010 D4 constrains
every option: an expiry may not clear `vko.gtrfc.com/rolling-update-state`
([0010:240-245](../adr/0010-every-rolling-update-wait-is-bounded.md)).

Common to A, B and C: the episode's cycle count is a new CR annotation (name an example,
`vko.gtrfc.com/failover-attempts`), incremented by `setFailoverTriggered` in the same update as
the state and the timestamp (ADR 0010 D10, D14), removed by `clearRollingUpdateState`, and
listed in the README's annotation table (`README.md:163`). A level condition (name an example,
`SentinelFailoverStalled`) with its `conditionRegistry` row
([ADR 0027](../adr/0027-conditions-are-levels-edges-or-history.md)) is True while the episode is
past its cap, and is cleared where the precondition is proven gone: `handleNewMasterFound` when
it proceeds with a connected replica, and `clearRollingUpdateState`, each behind a presence
guard. Because every pass of the cycle ends before the status write today, the report needs the
bounded-observation shape of ADR 0010 D16 and D17 and ADR 0026 D5: past the cap the pass returns
`DeferredRequeueAfter` instead of ending on the wait, so the status write runs (on a Sentinel
cluster the status write alone; a holding data tier holds the Sentinel roll, ADR 0010 D16 as
amended), with that shape's known residual risk of a phase that alternates between
`Rolling Update k/n` and what `updateStatus` computes (ADR 0010 `:591-598`). No Event: every
bounded observation in the repository reports a level and no Event (ADR 0010 D16, D17, ADR 0026
D5, ADR 0025 D7), and nothing here argues for an exception.

- **A - report only.** Count the cycles and report the level past N; the cycle continues
  unchanged. Cost S (the counter, the condition and its row, the deferred requeue, unit tests).
  Consequences: the stuck roll becomes visible after N cycles; every cycle keeps resetting every
  Sentinel and retriggering, and loop B keeps restarting, without end.
- **B - cap and hold (recommended).** Once the episode has sent N + 1 `SENTINEL FAILOVER` (the
  first trigger and N retriggers; proposal N = 3) and the last has timed out,
  `handleNoMasterFound` neither resets (`:3308`) nor enters `stateFailoverReset`: the state stays
  `failover-triggered` with its old stamp, every pass still runs `handlePostFailover`'s scan and
  continues the roll the moment an available current pod answers `role:master` - Sentinel's own
  automatic failover, or a human `SENTINEL FAILOVER <name>` on one Sentinel. The hold begins
  about three minutes after the first trigger (three cycles of about 50 s plus the last 30 s
  timeout) and at most about eight (three cycles of about 140 s plus 30 s). Loop B's count lives for the
  episode instead of one round: the cap check moves in front of `forceReplicaConnections` and
  `resetSentinelState`, so a pass at the cap sends neither and waits in `verifyNewMasterReady`
  (merely not clearing the count at `:3170` would still reset on every pass, because the check
  sits after the two calls today); an episode then sends at most `maxReconnectResets` resets and
  rounds of `REPLICAOF` instead of three per round. Both holds report the level. The state is
  kept (D4), so the split-brain resolver keeps running on every pass outside D9's window. The
  resume procedure goes to `docs/operations/rolling-updates.md`: send `SENTINEL FAILOVER` to one
  Sentinel once the cause is fixed, or remove the state annotation and the counter so the next
  pass starts a fresh episode. A count, not an episode deadline: a cycle lasts between about
  50 s and two and a half minutes, and a deadline would be one more armed stamp under ADR 0010
  D10, while the count rides in the state's own write. ADR 0010 gains D18 (the cycle is capped,
  and its expiry is a bounded observation, as D16 and D17) and the residual risk closes. Cost M.
  Consequences: a cause that clears only after the hold began needs a human or Sentinel's own
  failover (below); no new wait that is not reported; no roll.
- **C - back off.** Past N cycles, keep cycling but double the wait before each retrigger up to
  a ceiling (for example 30 minutes), and report the level. Cost M (the counter, a backoff
  computed from it, the condition, tests). Consequences: a cause that clears late heals without a
  human; the number of Sentinel resets and failovers stays unbounded, only slower, and each of
  them keeps the costs of Impact.

**B is marked.** Three reasons, each checkable. (1) Every extra cycle costs something measured or
read: the reset at `:3308` is the one T62 measured to leave the tier unable to fail over toward
an unreachable master (A2, K), each retrigger skips the first trigger's replica and `WAIT` gates
(T73's route), and the one measured multi-replica cycle was ten failovers each undone. (2) The
drivers found do not clear by retrying: a Sentinel with no replica to promote refuses every
forced failover, again 20 s later (measured, scenario S), `replica-priority 0` refuses every one
(scenario P), and the measured pre-D9 cycle never recovered in ten cycles; the transient case
the cycle was written for, a Sentinel still rediscovering its replicas (`:90-94`, `:1369-1373`, `:2721-2725`), is
covered by N retries. What the record does not show is how many retries a recovering failover
needed: the counted failovers of cluster `hard` completed on their first trigger (ADR 0025), and
no other cluster's retries were counted, which is why N = 3 is a proposal the owner confirms
with the decision. (3) It is the shape the repository already chose for waits nobody can end
automatically: ADR 0010 D16 and D17 and ADR 0026 D5 stop acting and keep observing, report a
level, and never clear the state; Sentinel's own failover stays the self-heal because the hold
sends Sentinel nothing.

C, the runner-up, has one real case: the self-sustaining driver clears when the NotReady node
returns, and if that happens after the hold began, today's cycle and C's next retrigger complete
the roll while B holds it until a human resumes, because the returned old master is healthy and
Sentinel sees no reason to fail over (by reading). That case needs a compounding failure (the
first failover already failed for another reason), and it narrows further once T62's gate lands:
the reset toward an unreachable, unverified master is then skipped, Sentinel keeps its tables,
and its own automatic failover (quorum 2, `down-after-milliseconds` 5000,
`internal/builder/sentinel.go:156-157`; delayed by up to twice `failover-timeout` after an
earlier attempt, T62's reading of 9.1.1 `sentinel.c:4966-4978`) promotes a current replica that
B's scan takes (by reading, not measured). Against it, C keeps an unbounded number of ungated
retriggers and tier-wide resets, and on a refusal that does not clear (scenario P) it retries
forever, only slower. The mark survives. A is the cheapest and fixes only visibility.

## Decision

Not decided.

## Work list

1. **XS, no decision needed (comments):** correct
   [`rolling_update.go:56-58`](../../internal/controller/rolling_update.go#L56-L58),
   [`:135-137`](../../internal/controller/rolling_update.go#L135-L137),
   [`:3142-3144`](../../internal/controller/rolling_update.go#L3142-L3144) and
   [`sentinel_failover_test.go:1279-1281`](../../internal/controller/sentinel_failover_test.go#L1279-L1281)
   to what the code does today: the count caps consecutive resets within one round, the pass
   that reaches it clears it without rewriting the failover timestamp, and the next pass starts
   the next round at once (the wording of `:789-792`). Rewritten again by the D1 change.
2. **XS, no decision needed (ADR text):** ADR 0010 Residual risks
   ([0010:775-784](../adr/0010-every-rolling-update-wait-is-bounded.md)), dated and marked as no
   decision change: the restart after the cap is immediate, because the cap pass does not rewrite
   the failover timestamp, and one round is three resets; replace "Not filed." with
   a dated "Filed 2026-09-27." and no ticket id (ADR 0034).
3. **Waits on D1:** the counter in `setFailoverTriggered`, loop B's per-episode count with the cap
   check moved in front of `:3159` and `:3161`, the hold (B) or the backoff (C) with the deferred
   requeue, the condition and its `conditionRegistry` row, unit tests with revert checks; ADR 0010
   D18 and the residual risk closed; the annotation in `README.md:163`; the condition in the
   README condition table (`README.md:506-522`) and in `docs/operations/status.md`; the resume
   procedure in `docs/operations/rolling-updates.md`; the comments of item 1 and `:90-94`,
   `:1369-1373`, `:2721-2725` ("the existing NOGOODSLAVE retry cycle will handle recovery")
   updated to the capped cycle.
4. **Optional, informs the e2e (not needed for D1):** an e2e fixture that makes every retrigger
   answer `NOGOODSLAVE`. The candidate, `CONFIG SET replica-priority 0` on every current replica
   after it is replaced and before the roll triggers its failover (scenario P), races the roll's
   own trigger, which is not assessed; no deterministic fixture is known. It asserts the cap and
   the condition, not a completed roll. ADR 0017 decides whether it may be an e2e.
5. **Done 2026-09-27, no decision needed:** moved out of this ticket; it is not this ticket's
   mechanism and is recorded in the ticket that owns it.
6. Close ([ADR 0034](../adr/0034-tickets-are-work-lists-that-get-archived.md)): the rule into
   ADR 0010 D18, the resume procedure into `docs/operations/rolling-updates.md`, the
   contributor-facing sentence into `docs/developer/` if the rolling-update page describes the
   failover states; `git grep` `T75` and `075-` outside `docs/tickets/`, then move to
   `archive/`.

## Cross-ticket findings

- [T62](062-resetsentinelstate-falls-back-to-sentinel-reset.md): the host. Its D1 gate changes
  what each cycle's reset does, not how many cycles run; under its option B the reset at `:3308`
  is skipped unless the named pod is verified, which removes the Sentinel wipe from loop A's
  self-sustaining driver above, and the retrigger then meets intact Sentinel tables. T62 does not
  change the cap, and this ticket does not change the gate. T62's Work list item 8
  ([062:579-590](062-resetsentinelstate-falls-back-to-sentinel-reset.md)) records all three of
  its filings as done and links T73 (8a), T74 (8b) and this ticket (8c), and its Cross-ticket
  findings carry one pointer each (`062:621-632`).
- [T73](073-forcereplicaconnections-re-points-a-data-holder-at-an-empty-master.md) (T62 Work
  list 8a): `forceReplicaConnections`, called once per loop B reset, re-points data holders with
  no role or dataset check, and the ungated retrigger of loop A is one of its two routes to an
  empty promotion. B bounds how often both run per episode; T73's veto decides what each call
  may do.
- [T74](074-no-condition-reports-a-sentinel-with-no-monitor-or-too-few-peers-or-replicas.md) (T62
  Work list 8b): the tier-wide empty-table state of the self-sustaining driver is one of the
  states it would report; whether its evaluator runs on the passes of the cycle, which end
  before the status write, is T74's to check.
- [T18](018-ready-keeps-its-pre-roll-value-during-a-rolling-update.md): every pass of the cycle
  ends before the status write, so `Ready` keeps its pre-roll value for as long as the cycle
  runs; B's deferred requeue lets the status write run during the hold.
- [T23](023-pauserollingupdate-records-no-pause.md): `pauseRollingUpdate` clears the state, so
  the next pass re-dispatches with a fresh budget; reusing it as the cap would restart the
  episode with a fresh count (History, removed options).

## Verification

- Item 1: `grep -n "breaking the infinite retry loop\|stops resetting\|Used to break" internal/controller/*.go`
  is empty, or finds only corrected text; `make lint`.
- Item 2: ADR 0010 `:775-784` no longer says "Not filed" and states the immediate restart and
  with a date.
- D1 (any option): a unit test that drives `handleNoMasterFound` and `handleFailoverRetrigger`
  through N + 1 cycles with every Sentinel answering `NOGOODSLAVE` and asserts the count and the
  condition; it fails with the counter write removed from `setFailoverTriggered`, and a test with
  a failing CR update shows the counter and the state are one write (ADR 0010 D14). The
  `conditionRegistry` guard is green with the new row and red without it. A test that the pass
  past the cap returns `DeferredRequeueAfter` and not `NeedsRequeue`.
- D1 option B: past the cap a pass sends no `SENTINEL FAILOVER`, no `SENTINEL REMOVE` and no
  `SENTINEL MONITOR` and leaves the state `failover-triggered`; a pass at loop B's cap with a
  failover timestamp older than 90 s sends no `REPLICAOF` and no `SENTINEL REMOVE`; a current,
  available master that appears during the hold continues the roll and clears the condition.
  Each test fails with the cap check removed,
  and the loop B test fails with the clear at `:3170` restored (revert checks, ADR 0017).
  `make test-unit`, `make lint`, `make cyclo`; the full e2e suite green on `single-node-valkey9`
  and `single-node-valkey8`, because the counter sits in `setFailoverTriggered`, which every
  Sentinel data roll passes.

## History

- 2026-09-27: filed from T62 Work list 8c during the re-verification at 84a39c2; the finding had
  been recorded only as an ADR 0010 residual risk ("Not filed") and as one line in T62.
  **Carried here** (T62 keeps its pointer lines): T62's Cross-ticket line and Work list item 8c;
  T62's measured Sentinel facts the cycle depends on (F5, no cooldown refuses a forced failover;
  A, A2, K, B, the tier-wide `NOGOODSLAVE` state and its recovery; the per-Sentinel `-INPROG`
  reading), cited by scenario name, the commands and results staying in T62; ADR 0025 D9's
  measured cycle. **Re-verified now (reading):** both loops step by step (`:849-887`,
  `:3071-3187`, `:3287-3326`), every writer of the failover timestamp (`:3249`, `:3514`,
  `:3523`), that `maxReconnectResets` is read only at `:3163`, that no other counter or deadline
  bounds the episode (`clearRollingUpdateState` `:3447-3470`, `GetSyncTimeout`), the absence of
  any condition, Event or phase for the cycle, the alert and its default-off switch, the tests
  that pin the cap pass and none that drives the pass after it, the provenance (`a6a1a19`,
  v1.0.0; `2926077`, v1.1.0; `b13377e`, v1.13.0). **Precised:** ADR 0010's residual risk and
  ADR 0025 say the count starts over after the cap and not when; the restart is immediate,
  because the cap pass does not rewrite the timestamp (Work list item 2); four code and test
  comments say the loop is broken (Work list item 1). **Measured:** docker, both pinned images,
  scenario P: `replica-priority 0` on every replica makes a forced `SENTINEL FAILOVER` answer
  `NOGOODSLAVE` on two Sentinels in a row (Fact, Measured). **Removed options:** "pause the roll
  after N cycles" through `pauseRollingUpdate` - it clears the state, which ADR 0010 D4 forbids
  on expiry, and the next pass re-dispatches through `handleMasterFailover` and starts a fresh
  episode with a fresh count (T23), so it caps nothing and adds a Warning per restart; "an
  episode deadline instead of a count" - kept only as a sentence under B, because it is the same
  cap with one more armed stamp. **Adversarial review, same day:** scenario P re-run on both
  images, same results; scenario S measured on both images: with no replica to promote, every
  forced failover answered `NOGOODSLAVE`, again 20 s later. **Found** a further finding, which
  goes into its own file (Work list item 5) *(reduced before commit, 2026-09-27)*. **Corrected:** the cycle's upper length is about two and a half
  minutes, not two (30 + 20 + 90 s), so B's hold starts at about three to eight minutes, not
  three to six; the exit through `finalizeRollingUpdate` was missing; "none of them is an attempt
  count" overlooked `reconnect-reset-count`; `Ready` does not report the data plane during the
  cycle, because every pass ends before the status write (T18); the ADR 0025 zero counts are from
  the runs on D9's first version, before its clock; B's hold was placed in `stateFailoverReset`,
  after the reset it is meant to spare, and now sits in `handleNoMasterFound` before `:3308`; B
  ended its pass on the wait with a phase message and a Warning Event, against the
  bounded-observation shape its own reason (3) cites, and now returns `DeferredRequeueAfter`,
  reports the level and emits no Event; the self-sustaining driver needs the cycle to run
  already, because a forced failover does not need the master; T62 Work list 8a is filed as T73
  and 8b as T74. **B's mark re-argued** against C's one real case (a NotReady node returning
  after the hold began) and kept. **Frontmatter:** `security: none` (no principal, no security
  guarantee); `severity: medium`; `urgency: next` by rule 3, top-down - rule 1 reads
  "measured-false" and the comments are false by reading only, the derivation T73 used for the
  same shape from the same host (the filing pass had `now` by the looser reading of T18, T23 and
  T62); `effort: M`; `blocked-by: decision`. **Not verified:** either loop driven by the operator
  after D9, the other drivers, the client impact per
  cycle, the constancy of the phase label; no make target, `go test`, Kind cluster or kubectl was
  run; every docker container and network of this ticket was removed.
  Sweep: T62's Work list item 8c and its Cross-ticket line now point here, so the Cross-ticket note
  that they did not is struck. Work list item 5, an unfiled finding *(its title reduced before
  commit)*, is still open and now names its owner (the next filing
  run, which Hans starts). Frontmatter unchanged.
  Final pass: Work list item 5 is done - moved out of this ticket - and the passages of this
  ticket that described it were removed or reduced before commit (Fact, Impact,
  Options, Work list, Verification, Not verified, the severity and urgency comments, and three
  sentences of this History entry, each marked in place). Severity stays `medium`, urgency stays
  `next` by rule 3, and the mark on D1's option B is unchanged. The struck Cross-ticket note on
  T62 and its sweep remark were replaced by the verified statement: T62's Work list item 8
  (`062:579-590`) records all three filings as done and links T73, T74 and this ticket, and its
  Cross-ticket findings carry one pointer each (`062:621-632`). Verified at `84a39c2` by reading;
  nothing was run.
