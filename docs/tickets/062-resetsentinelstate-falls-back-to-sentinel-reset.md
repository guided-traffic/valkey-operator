---
id: T62
title: resetSentinelState falls back to SENTINEL RESET, which ADR 0022 D6 and CLAUDE.md say the operator never issues
state: analysed       # was filed; the open question is measured on both pinned images (docker, 2026-09-27), the upstream replies are read at both tags, and every open decision carries a marked option (History 2026-09-27)
severity: medium      # was an estimate; now measured: an unverified REMOVE + MONITOR on every Sentinel (the loop) leaves the whole tier unable to fail over, silently, until the named address answers as a master, and pointed at a replica if it comes back as one; on a cluster without persistence it can go on to lose the dataset (by reading plus upstream documentation, not measured); kept below high because the triggers are narrow (a 2 min finalization stall, a 30 s failover timeout with the scanned master unreachable, a master death inside a rebuild window)
security: none
urgency: now          # rule 1, second clause: the comment at sentinel_failover_test.go:1467-1468 repeats a claim ADR 0022 Context measured false; the cooldown comments at rolling_update.go:140-142 and :3614-3615 are measured false for the delay after a completed failover (2026-09-27); ADR 0022 D6 (:116) and CLAUDE.md:743 state an unconditional rule that rolling_update.go:3669 breaks. The first clause does not match: the fallback is released (b13c83f, 2026-02-18)
effort: S             # upper end of S: the recommended D1 option B (the gate, deleting the :958 and :1007 calls), the recommended D2 agreement check, the decision-free fallback fix and comment corrections, unit tests with revert checks, the full e2e on both legs; A alone is XS
blocked-by: decision  # D1 and D2, below; CLAUDE.md:743 stays true as written under every kept option (was: "CLAUDE.md edits also need Hans", which applied only under the removed option C)
filed-from: ticket enrichment of 2026-09-27 (review of the security-hardening family)
opened: 2026-09-27
decided:              # not recorded - no decision yet
done:                 # not recorded - not done
---

# T62 - resetSentinelState falls back to SENTINEL RESET, which ADR 0022 D6 and CLAUDE.md say the operator never issues

Filed on 2026-09-27. The orchestrator of the ticket enrichment found it by reading at
`4a7543e`; ~~this file re-read every location in the working tree of
`chore/maintenance-2026-09-27` on the same day. The only change to `rolling_update.go` in that
working tree above the cited lines is a comment that grew by two lines at `:2640`, so the
working-tree lines below are the `4a7543e` lines plus two from `:2643` on (the `4a7543e` line is
given where it differs). No make target, container or cluster was run for this file.~~
*(corrected 2026-09-27 at 84a39c2: `bcc63c9` has two growing hunks in `rolling_update.go` above
the cited lines, +1 at `:419` (the `podNeedsUpdate` comment) and +2 at `:2637`, so from `:422`
on the lines are the `4a7543e` lines plus one and from `:2643` on plus three; every
working-tree line this file first gave was one short (`git diff 4a7543e 84a39c2 --
internal/controller/rolling_update.go`). Every location below is re-read at `84a39c2`. The
re-verification ran docker measurements against both pinned Valkey images (Measurements, below);
no make target, `go test`, Kind cluster or kubectl was run.)*

The title names the RESET fallback. The measured hazard is wider: the unverified
REMOVE + MONITOR on every Sentinel, which the fallback is one branch of (Impact). The file name
and title are kept so the id stays findable.

## Fact

**Mechanism.** `resetSentinelState`
([`rolling_update.go:3617`](../../internal/controller/rolling_update.go#L3617)) walks every
Sentinel ordinal and, per Sentinel, sends `SENTINEL REMOVE <name>`, then
`SENTINEL MONITOR <name> <masterAddr> <port> <quorum>`, then five `SENTINEL SET`s and the
`auth-pass` ([`:3666-3693`](../../internal/controller/rolling_update.go#L3666-L3693)). When the
REMOVE returns an error, it sends `SENTINEL RESET <name>` instead and moves on to the next
Sentinel without a MONITOR
([`:3666-3673`](../../internal/controller/rolling_update.go#L3666-L3673), the RESET call at
`:3669`). An empty `masterAddr` falls back to pod-0
([`:3628-3632`](../../internal/controller/rolling_update.go#L3628-L3632), `builder.MasterAddress`,
[`configmap.go:33-36`](../../internal/builder/configmap.go#L33-L36)). The function never checks
the pod it names. It is best-effort: it returns nothing, and every caller carries on whatever
happened.

**Verified (read at `84a39c2`):**

- **Five callers, three of them on paths where the master may be unreachable or unverified:**
  - [`:958`](../../internal/controller/rolling_update.go#L958), `checkFinalizationTopology`: the
    finalization is stalled (`finalizationStallTimeout` = 2 min, `:126`) and the pass counts
    zero or several masters (`masterCount != 1`, `:951`); the reset runs with `""`, so every
    Sentinel is pointed at pod-0. Because the branch runs only when the count is not one, pod-0
    is there either one of two or more pods answering master, or possibly a replica.
  - [`:1007`](../../internal/controller/rolling_update.go#L1007), `syncSentinelWithMaster`: the
    finalization is stalled and `GetReplicationInfo` on the pod identified as master has just
    failed; the reset runs anyway with that pod's address.
  - [`:1049`](../../internal/controller/rolling_update.go#L1049), `syncSentinelWithMaster`: the
    master answered and its replicas are connected (or the stall let a partial count through,
    `:1016-1027`); the address is that master's. **This call runs on the final pass of every
    Sentinel data roll**, not on a rare path: `finalizeRollingUpdate` enters
    `checkFinalizationTopology` whenever a roll state is recorded (`:898`), and
    `replaceNextReplica` records `stateReplacingReplicas` on the first replacement
    (`:2457-2461`). `syncSentinelWithMaster` never re-checks the role (`:1011-1049`), and past
    the stall the completion hold (next bullet) no longer keeps a terminating master out, so on
    a stalled finalization `:1049` can name a terminating master that still answers INFO.
  - [`:3161`](../../internal/controller/rolling_update.go#L3161), `handleMasterWithNoReplicas`:
    a new-image master that is `available()` (`:3099-3108`, since `360cb03`) answered
    `role:master` with no connected replica past `replicaReconnectTimeout` (90 s from the
    failover timestamp, `:149`, `:3539-3541`); `forceReplicaConnections` runs first (`:3159`),
    on the same pod scan.
  - [`:3308`](../../internal/controller/rolling_update.go#L3308), `handleNoMasterFound`: the
    failover timed out (`failoverRetryTimeout` = 30 s, `:143`, `:3530-3531`) and no available
    new-image master answered; the address is the first pod the pass's scan marked as master,
    else pod-0 (`:3300-3308`). The scan marks a pod master on an INFO `role:master` answer, a
    terminating pod included (`:1952-1957`), or on its label when INFO failed (`:1958-1961`),
    so the pick is by ordinal among possibly several.
- **The completion hold does not protect the stalled calls.** `finalizeRollingUpdate` holds the
  completion while a data pod terminates so that Sentinel is never pointed at a dying master
  (ADR 0026 D4), but only while the finalization is not stalled
  ([`:912`](../../internal/controller/rolling_update.go#L912),
  `!r.isFinalizationStalled(v)`). Past the 2 min stall the calls at `:958`, `:1007` and `:1049`
  run.
- **The rule the code breaks.** [ADR 0022](../adr/0022-sentinel-identity-is-pinned-to-the-pod.md)
  D6 (`:116`): "The operator never issues `SENTINEL RESET` on its own." [`CLAUDE.md:743`](../../CLAUDE.md):
  "The operator never issues `SENTINEL RESET` itself." Neither is restricted. Only the ADR's
  Status (`:17-19`) narrows it: "the operator never issues `SENTINEL RESET` to clean a peer
  table". D6 (`:118-119`) sets two conditions for the manual reset: "one Sentinel at a time with
  the master verified healthy".
- **Why the rule exists.** ADR 0022 Context (`:71-79`) measured on both pinned images that a
  `SENTINEL RESET` issued while the master is unreachable leaves the Sentinel at
  `num-other-sentinels=0` and `num-slaves=0` "with no way back: peer and replica discovery both
  run through the master". ~~ADR 0026 D4 (`:248-260`) reads the timed-out branch of the Sentinel
  roll - `handleNoMasterFound`, which calls `resetSentinelState` at `:3307` - as one that
  "resets Sentinel through a dying master (the unrecoverable direction per ADR 0022)".~~
  *(corrected 2026-09-27 at 84a39c2: ADR 0026 D4 (`:250-256`) gives the timed-out branch two
  arms - it "either resets Sentinel through a dying master ... or triggers a real
  `SENTINEL FAILOVER`" - and the analysis D4 came from
  ([archive/039:2172-2181](archive/039-findings-from-the-1-11-0-fleet-rollout.md)) maps the reset
  arm to `handleMasterWithNoReplicas` and the FAILOVER arm to `handleNoMasterFound`. The same
  commit as D4, `360cb03` (2026-08-25), added the `available()` filter at `:3100`, so at
  `84a39c2` `handleMasterWithNoReplicas` can no longer receive a terminating pod; the timed-out
  path that still resets toward a dying pod is `handleNoMasterFound` (`:3308`), whose scan takes
  a terminating pod that answers `role:master`. The auditor and the design skeptic of the
  re-verification read D4 as meaning `:3161`; the facts skeptic showed the filter; both are
  right about their half.)* So an ADR already names a reset routed through a dying master as
  the unrecoverable direction, and at `84a39c2` that reset is reachable through `:3308`.
- **Each command dials anew.** `Client.exec` opens a connection per command, sets a deadline
  (5 s default, `:160`), authenticates, writes, reads and closes it
  ([`client.go:429-453`](../../internal/valkeyclient/client.go#L429-L453)); a RESP `-` reply
  becomes a plain `fmt` error (`:480-482`), with no typed reply error. So a REMOVE that fails at
  dial or write can be followed by a RESET that succeeds on the next dial, and a REMOVE whose
  reply is lost after the server executed it counts as failed.
- **A unit test pins the fallback, under a false comment.**
  `TestResetSentinelState_FallsBackToResetWhenRemoveFails`
  ([`sentinel_failover_test.go:1470-1486`](../../internal/controller/sentinel_failover_test.go#L1470-L1486))
  asserts `SENTINEL REMOVE` then `SENTINEL RESET` and no MONITOR. It injects
  "No such master with that name" (`:1475`), which is exactly the case in which upstream RESET
  matches nothing (measured, below), so it pins a RESET that changes nothing plus the missing
  re-add of the monitor (assert message `:1485`). Its comment (`:1467-1469`) says "a plain
  RESET would revert sentinel to the pod-0 address from its config file" - the claim ADR 0022
  Context measured false (RESET keeps the current master address, also after a failover). The
  function's own doc comment said the same until 2026-09-27 and was corrected that day in
  `bcc63c9` (text only, now `:3607-3610`); the test comment was not.
- ~~**The corrected doc comment discharges an ADR residual risk.** ADR 0022 Residual risks
  (`:204-205`) says the wrong comment above `resetSentinelState` "was left in place by this
  change and is corrected in the change that next touches that function", and Context
  (`:68-70`) says the comment "claims the opposite and is wrong". Both describe a comment that
  no longer exists. Work list item 1.~~ *(corrected 2026-09-27 at 84a39c2: true at `4a7543e`;
  `bcc63c9` struck both sentences in place - Context `:75-76`, Residual risks `:210-212`
  "Closed 2026-09-27" - and added the Status line `:21-24`. Work list item 1 is done.)*
- **An emptied or missing monitor is not reported.** `SentinelPeersStale` is True only for a
  Sentinel that knows *more* peers than expected (`staleSentinelPods`,
  [`valkey_controller.go:2415-2424`](../../internal/controller/valkey_controller.go#L2415-L2424),
  `known > SentinelPeersExpected`). A Sentinel that knows none, or has no monitor, raises
  nothing. The Sentinel readiness probe is `PING`
  ([`sentinel.go:420-446`](../../internal/builder/sentinel.go#L420-L446)), so such a pod stays
  Ready. The missing report is its own finding, filed as
  [T74](074-no-condition-reports-a-sentinel-with-no-monitor-or-too-few-peers-or-replicas.md),
  which holds the health-checker reading, the shipped alerts and the debounce measurements; this
  ticket keeps only the fact that the states it produces are silent.
- **Comments call the REMOVE + MONITOR a "SENTINEL RESET".** `failoverResetMinWait` (`:151`),
  `hasMinWaitElapsed` (`:3545`) and
  [`rolling_update_bounds_test.go:760`](../../internal/controller/rolling_update_bounds_test.go#L760)
  speak of "a SENTINEL RESET" where the code sends REMOVE + MONITOR; `:130`, `:979`, `:3275` and
  `:3558` name it correctly.
- **Comments state a Sentinel cooldown that does not refuse the operator's retrigger.**
  `failoverRetryTimeout` (`:140-142`: "sentinel refuses a failover due to its internal cooldown
  (failover-timeout)"), `handlePostFailover` (`:3068-3070`), `isFailoverTimedOut`
  (`:3527-3529`) and `resetSentinelState` (`:3614-3615`: "have cooldowns that prevent another
  failover"), and the doc comment of `valkeyclient.SentinelReset`
  ([`client.go:249-251`](../../internal/valkeyclient/client.go#L249-L251): "clears the
  sentinel's internal failover cooldown state"), which Work list item 4 deletes. Upstream, a forced `SENTINEL FAILOVER` is refused only with `-INPROG` or
  `-NOGOODSLAVE` (valkey 9.1.1 `sentinel.c:3940-3947`, 8.1.9 `:3875-3881`); the
  2 x failover-timeout delay (9.1.1 `:4966-4978`) gates only the automatic
  `sentinelStartFailoverIfNeeded`, and RESET/REMOVE zero it (9.1.1 `:1545`, 8.1.9 `:1526`).
  Measured false for the delay after a completed failover (F5, below). `-INPROG` is per
  Sentinel - only the Sentinel running the attempt carries `SRI_FAILOVER_IN_PROGRESS` - and
  `triggerSentinelFailover` moves to the next ordinal on any error
  ([`:3711-3733`](../../internal/controller/rolling_update.go#L3711-L3733)), so by reading not
  even one attempt in progress refuses the retrigger; the INPROG case was not measured. The
  comment at `:3665` ("clears ... cooldowns") is accurate: REMOVE drops the instance and its
  automatic-failover delay with it.
- **Two of the five SETs always fail.** `SENTINEL SET <name> resolve-hostnames yes` and
  `... announce-hostnames yes` (`:3685-3686`) are rejected on both pinned images (measured,
  below): the options are global, not per master, and already in the generated config
  ([`sentinel.go:162-163`](../../internal/builder/sentinel.go#L162-L163)). The errors are
  discarded (`_ =`). `TestResetSentinelState_ReconfiguresEverySentinelAroundTheGivenMaster`
  pins them ([`sentinel_failover_test.go:1458-1459`](../../internal/controller/sentinel_failover_test.go#L1458-L1459)).
- **No command re-points a monitor and keeps its tables.** The `SENTINEL` subcommands at both
  tags are ckquorum, config, debug, failover, flushconfig, get-master-addr-by-name, help,
  info-cache, is-master-down-by-addr, master, masters, monitor, myid, pending-scripts, remove,
  replicas, reset, sentinels, set, simulate-failure; `sentinelSetCommand` (9.1.1 `:4254ff`,
  8.1.9 `:4176ff`) accepts no address. The internal re-point
  `sentinelResetPrimaryAndChangeAddress` (9.1.1 `:1589`) is reached only by a hello with a newer
  config epoch (`:2861`) and at the end of a failover (`:5331`), and it ends in
  `sentinelFlushConfig` (`:1642`), so a running Sentinel persists a switched address itself.
- **A verified reset opens a rebuild window.** After REMOVE + MONITOR a Sentinel knows replicas
  only from the master's INFO and peers only from their hellos. The first INFO goes out on the
  first link, not after the 10 s period (`info_refresh == 0`, 9.1.1 `sentinel.c:3051`; periods
  `:86`, `:89`), and hellos every 2 s, so with every replica connected the window is about 2 s
  ([archive/039:423-424](archive/039-findings-from-the-1-11-0-fleet-rollout.md) observed peers
  back in ~2 s and replicas within ~10 s as an upper bound). At `:3161` the master has
  `ConnectedSlaves == 0` by definition (`:3122-3124`), and on a stalled `:1049` a partial count
  is let through, so there the Sentinels learn only the replicas the master's INFO lists and the
  gap lasts until the missing replicas connect - the condition that already failed for 90 s or
  2 min. At `:3161` the reset also drops the replica list Sentinel holds after its own
  failover (inference by reading; the comment at `:978-981` describes the same loss for
  cascaded replicas).
- **No stale replica entries accrue from pod replacement.** Replicas announce themselves by
  hostname (`replica-announce-ip $MY_HOST`,
  [`statefulset.go:355`](../../internal/builder/statefulset.go#L355), `:543`), and the Sentinel
  `myid` is pinned (ADR 0022), so no new ghost peer accrues either. Existing ghost peers are left
  to an operator or the next Sentinel roll (ADR 0022 D6), not to this reset. By reading.
- **A data pod boots as master when a Sentinel names it.** The data init container asks the
  Sentinels `get-master-addr-by-name` first and takes the answer
  ([`statefulset.go:288-316`](../../internal/builder/statefulset.go#L288-L316)); if it names the
  pod's own FQDN, the pod starts with the master config (`:329-332`). A Sentinel that is
  `s_down` still names the address it monitors.
- **The headless Service publishes not-ready addresses**
  ([`service.go:154`](../../internal/builder/service.go#L154)), so a pod FQDN resolves while the
  pod has an IP; it does not for a missing or IP-less pod, such as pod-0 while it is being
  recreated.
- **Provenance** (`git log -S`). The reset began as a plain `SENTINEL RESET` in `730008e`
  (2026-02-18, "fix: TestE2E_RollingUpdate_HA_Idempotent"). The same day `b13c83f` ("fix: e2e
  tests") made it REMOVE + MONITOR with RESET as the fallback, and added the verified
  finalization reset with a recorded reason: "After a failover the master may have changed
  (e.g. pod-0 → pod-2), but sentinel's initial config file still points to pod-0. Without this,
  the next rolling update finds sentinel monitoring a stale address with num-slaves=0 and
  failover becomes impossible." `f42f450` (same day) gated that reset on every replica being
  connected: "The sentinel reset (REMOVE + MONITOR) is destructive and can temporarily disrupt
  replica connections. We must ensure the cluster is fully stable ... before performing the
  reset." `c906950` (2026-02-28, "Fix/reboot loop (#12)") refactored it into
  `syncSentinelWithMaster`, removed that comment and added the stall branches: the blind pod-0
  reset (`:958`), the reset after a failed INFO (`:1007`) and the partial-count pass into
  `:1049`. The recorded premise of `b13c83f` concerns a restarted Sentinel reading its config
  file; a running Sentinel persists the switched address itself (above), and a restarted pod
  gets the address from the known-master annotation (`persistKnownMaster`, `:1045`,
  [ADR 0008](../adr/0008-known-master-annotation-is-the-recorded-authority.md)).
- `valkeyclient.SentinelReset`'s only production caller is `rolling_update.go:3669`
  (`git grep -n "SentinelReset(" -- internal cmd`: `client.go:252`, and the tests
  `client_test.go:685`, `exec_test.go:467`).

**Measured 2026-09-27** (docker, `valkey/valkey:9.1.1` and `valkey/valkey:8.1.9`, identical
results on both unless stated). Topology: a docker network, one master, two replicas
(`valkey-server --port 6379 --save '' --appendonly no`, replicas with `--replicaof <m> 6379`),
three Sentinels started from a file written in the operator's line order:
`port 26379`, `sentinel monitor mymaster <m> 6379 2`, `down-after-milliseconds mymaster 5000`,
`failover-timeout mymaster 60000`, `parallel-syncs mymaster 1`, `resolve-hostnames yes`,
`announce-hostnames yes`. "Unreachable" is `docker pause` of the master; values are read with
`valkey-cli -p 26379 SENTINEL MASTER mymaster`. Containers `vko-verify-062-*`,
`vko-verify-062k-*`, `vko-verify-062skeptic-*`, `vko-verify-062c-*`, all removed afterwards
(`docker ps -a` and `docker network ls` show none). The setup differs from Kubernetes: pause
instead of pod termination, no TLS, no auth.

| Scenario | Commands | Result |
|---|---|---|
| Replies | `SENTINEL REMOVE nosuch`; `SENTINEL RESET nosuch`; `SENTINEL SET mymaster resolve-hostnames yes`; `... announce-hostnames yes`; `SENTINEL MONITOR probe <unresolvable host> 6379 2` | `ERR No such master with that name`; `0`; `ERR Unknown option or number of arguments for SENTINEL SET 'resolve-hostnames'`; the same for `announce-hostnames`; `ERR Invalid IP address or hostname specified` (9.1.1 `sentinel.c:3988-3992`) |
| A: all three reset, master unreachable | pause m; on each Sentinel `SENTINEL REMOVE mymaster`, `SENTINEL MONITOR mymaster <m> 6379 2`, `SENTINEL SET mymaster down-after-milliseconds 5000`; read at +10 s, +30 s, +60 s | all three `num-slaves=0 num-other-sentinels=0`, `flags=s_down,master`; replicas stay `slave`, no failover |
| A2: forced failover after A | fresh cluster, as A, +30 s; `SENTINEL FAILOVER mymaster` | `NOGOODSLAVE No suitable replica to promote` |
| B: address back as master | after A, unpause m; read at +20 s | all three `num-slaves=2 num-other-sentinels=2` |
| C: address back as a replica | pause m; REMOVE + MONITOR on all three; r1 `REPLICAOF NO ONE`; r2 `REPLICAOF r1 6379`; unpause m; m `REPLICAOF r1 6379`; read at +30 s, +90 s | all three `num-slaves=0 num-other-sentinels=2`, still naming m; r1 is master. The Sentinels relearn their peers but never find the real master or its replicas |
| D: RESET on one Sentinel only | fresh; pause m; s1 `SENTINEL RESET mymaster` (answers `1`); read at +30 s; unpause; +30 s | the two untouched Sentinels fail over (r2 master); at +30 s all three, s1 included, `flags=master num-slaves=2 num-other-sentinels=2` naming r2; after unpause `num-slaves=3` |
| K: the code's shape when the fallback fires | fresh; pause m; s1 `SENTINEL RESET mymaster` (`1`); s2, s3 REMOVE + MONITOR + SETs (`OK`); read at +40 s; forced FAILOVER on s2; unpause; +25 s | +40 s all three `flags=s_down,master num-slaves=0 num-other-sentinels=0`, replicas `slave`; FAILOVER `NOGOODSLAVE`; after unpause all three back at 2/2 |
| E: MONITOR fails after a successful REMOVE | fresh; `docker stop` m; s1 `SENTINEL REMOVE mymaster` (`OK`); `SENTINEL MONITOR mymaster <m> 6379 2`; `SENTINEL MASTER`; `docker start` m; s1 REMOVE; RESET; `SENTINEL MASTER`; `PING` | MONITOR `ERR Invalid IP address or hostname specified`; `SENTINEL MASTER` `ERR No such master with that name`; after the master is back REMOVE `ERR No such master with that name`, RESET `0`, `SENTINEL MASTER` still the error, `PING` `PONG` - the `resetSentinelState` shape never re-adds the monitor |
| F5: forced failover back to back | healthy; s1 `SENTINEL FAILOVER mymaster`; after the address switch + 12 s again; +20 s again | `OK`, `OK` (the master moved again), `OK` - no cooldown refusal |
| Duplicate | one Sentinel with `sentinel monitor mymaster 127.0.0.1 6379 2`; `SENTINEL MONITOR mymaster 127.0.0.1 6380 2`; read the port | `ERR Duplicate master name.`, port still 6379 (9.1.1 `sentinel.c:1304-1307`, `:1711-1713`) |

Upstream source read at the pinned tags
(<https://raw.githubusercontent.com/valkey-io/valkey/9.1.1/src/sentinel.c>,
<https://raw.githubusercontent.com/valkey-io/valkey/8.1.9/src/sentinel.c>): REMOVE and the
name lookup 9.1.1 `:3735-3742`, `:4007-4015` (8.1.9 `:3689`); RESET replies the count from
`sentinelResetPrimariesByPattern` (9.1.1 `:3900-3903`, `:1562-1580`; 8.1.9 `:3849-3852`);
MONITOR 9.1.1 `:3968-4001`; REMOVE and MONITOR flush the config (9.1.1 `:4015`, `:4000`).

**Not verified:**

- ~~**Open question: whether REMOVE + MONITOR with the master unreachable leaves the Sentinel in
  the same empty-table state.** ... What happens once the named address answers again - as a
  master, or as a replica of a pod promoted meanwhile - was not measured. If this holds, the
  hazard is not limited to the RESET fallback.~~ *(answered 2026-09-27 at 84a39c2, measured
  on both images, scenarios A, A2, B, C above: it holds, every Sentinel the loop reaches ends
  with empty tables and the tier cannot fail over; it recovers within 20 s once the address
  answers as a master, and stays pointed at the address with 0 replicas known when it comes
  back as a replica of a promoted pod.)*
- ~~**When the RESET fallback actually fires.** ... From memory of upstream `sentinel.c`, not
  re-read for this file.~~ *(corrected 2026-09-27 at 84a39c2: now read at both tags and
  measured on both images - REMOVE of an unknown name answers "No such master with that name"
  and RESET of it answers 0. The fallback changes a table only after a transport-level REMOVE
  failure followed by a successful RESET; after a REMOVE whose reply was lost although it
  executed, RESET matches nothing and the MONITOR is skipped, so that Sentinel is left with no
  monitor (scenario E).)*
- ~~Whether any Valkey 8 or 9 command re-points a monitor or clears the failover cooldown
  without dropping the tables (option D). Not researched.~~ *(corrected 2026-09-27 at
  84a39c2: read at both tags, none does, and the forced failover has no cooldown to clear;
  Verified, above.)*
- ~~Nothing was run: no unit test, no docker measurement, no Kind run.~~ *(corrected
  2026-09-27: docker measurements ran, above; still no unit test and no Kind run.)*
- **The Kubernetes behaviour of the measured states** - pod termination instead of pause, TLS,
  auth. A Kind run of a Sentinel data roll with the master killed inside the stall would
  settle it.
- **Whether the `:1049` reset completes in production.** The archived T8 execution record
  (2026-08-26, gitlab-valkey on wds18, TLS, operator v1.11.1, whose `finalizeRollingUpdate`
  has the same `IsSentinelEnabled() && currentState != ""` branch, v1.11.1
  `rolling_update.go:587`, `:662`, and the same REMOVE/RESET code, `:2404-2407`) measured the
  Sentinel peer tables at 4/3/2 before and after a full failover-aware data roll that ended
  `OK` ([archive/039:4012-4027](archive/039-findings-from-the-1-11-0-fleet-rollout.md)). A
  successful REMOVE + MONITOR on all three Sentinels would have dropped the ghost peers to
  2/2/2, because hellos carry only the sender's own identity and the ghosts belong to replaced
  pods. So either the call did not run in that roll or its commands failed; the archive's own
  explanation ("a data-tier roll does not restart the Sentinel pods") misses this reset. The
  operator log is not in the repository, and the failure logs of `resetSentinelState` are V(1)
  (`:3667`, `:3670`, `:3677`) while "Sentinel reconfigured successfully" is at the default level
  (`:3695`). An e2e or Kind operator log after a Sentinel data roll, showing that line three
  times or not, would settle it.
- **Data loss on a cluster without persistence** (inference by reading plus upstream
  documentation, not measured). After every Sentinel is reset toward an unreachable master (A2)
  no failover happens. When that master pod comes back empty, its init container asks the
  Sentinels, which still name its address, so it starts as master with no data (Verified,
  above); per scenario B the Sentinels then recover onto it, and the replicas full-resync from
  the empty master. Upstream documents the failure
  (<https://valkey.io/topics/replication/>, "Safety of replication when primary has
  persistence turned off"). With intact tables Sentinel fails over to a replica that holds the
  data before the pod is back. Production has at least one memory-only Sentinel cluster,
  gitlab-valkey ([archive/039:3835-3837](archive/039-findings-from-the-1-11-0-fleet-rollout.md)).
  This is a new route into the mechanism of [T36](036-non-persistent-master-restarts-empty.md).
- **The sidecar labeler following a wrongly pointed tier.** The labeler trusts the first
  Sentinel that answers (`internal/sidecar/labeler.go:135-141`, `:351-363`), so a tier pointed at
  one of two masters by `:958` could move the master label of the other one to replica.
  Inference by reading, not measured.
- **DNS for a terminating pod and CoreDNS negative caching**, which decide how often a MONITOR
  at a pod FQDN answers "Invalid IP address or hostname specified" in Kubernetes.
- **A forced failover while another Sentinel holds `-INPROG`** (the per-Sentinel reading above).
- **Whether anything depends on the routine all-Sentinel wipe at `:1049`** beyond the recorded
  reason of `b13c83f` (decision D2).
- **A container restart keeps the monitor-less or emptied state** (by reading: the Sentinel
  runs on a writable copy, `sentinel.go:531-534`; the init container copies the ConfigMap only
  at pod start, `:628-630`; REMOVE and MONITOR flush the config; init containers do not rerun on
  a container restart). Not measured.

## Impact

Sentinel-enabled clusters only, during a data-tier rolling update: on a failover that times out
(`:3308`), a finalization that stalls for 2 min (`:958`, `:1007`, `:1049` stalled), a new
master with no replica after 90 s (`:3161`), and - through the rebuild window - at the end of
every Sentinel data roll (`:1049`). The first three are the paths on which the master is most
likely to be unreachable, terminating or ambiguous.

- ~~**Verified half:** a Sentinel whose REMOVE fails and whose RESET succeeds while the master is
  unreachable ends with empty peer and replica tables, by ADR 0022's measurement of RESET. It
  cannot then lead or vote a failover it would have been needed for.~~ *(corrected 2026-09-27
  at 84a39c2: on its own, a RESET of one Sentinel whose peers keep their tables does not block
  the failover - the peers promote a replica and heal the reset Sentinel within 30 s through
  their hellos (scenario D). In the code the peers do not keep their tables: the same loop
  sends them REMOVE + MONITOR toward the same address, and in that shape all three end at
  `s_down`, 0/0, and a forced failover answers `NOGOODSLAVE` (scenario K). The fallback is
  harmful through the loop it sits in, not on its own. When REMOVE failed with a reply error,
  the RESET is a no-op (measured); a Sentinel left without a monitor is never repaired by this
  function (scenario E).)*
- ~~**If the open question holds:** every Sentinel the loop reaches ends that way, on every call
  that names an unreachable master; the tier cannot fail over until the named address answers as
  a master again or the Sentinel pods are replaced (the init container rewrites the config on
  the pod's `emptyDir`; a container restart keeps Sentinel's rewritten config - inference, not
  measured).~~ *(corrected 2026-09-27 at 84a39c2, measured: every Sentinel the loop reaches
  ends at 0/0 and `s_down` on a call that names an unreachable master, and the tier cannot fail
  over (`NOGOODSLAVE`) until the named address answers as a master again (then within 20 s) or
  the Sentinel pods are replaced. If the address comes back as a replica of a pod promoted
  meanwhile, the tier stays pointed at that replica, knowing 0 replicas, 90 s and more later.
  That a container restart keeps the rewritten config is still by reading, Not verified.)*
- **On a cluster without persistence** the no-failover state can end in an empty master that
  flushes its replicas (Not verified, inference plus upstream documentation; [T36](036-non-persistent-master-restarts-empty.md)'s mechanism).
- **A Sentinel can lose its monitor for good.** A MONITOR that fails after a successful REMOVE
  (a name that does not resolve, such as the pod-0 fallback while pod-0 is recreated) leaves it
  with no monitor; every later call answers REMOVE with "No such master", RESET with 0, and skips
  the MONITOR (scenario E). It still answers `PING`, so the pod stays Ready, and the operator
  never replaces a Sentinel pod that is on the current template. Only a pod replacement
  (a Sentinel roll) repairs it. The unit test pins this behaviour.
- **Even a verified reset blanks every Sentinel at once**, at the end of every Sentinel data
  roll; a master death inside the rebuild window (about 2 s with all replicas connected, until
  the missing replicas connect at `:3161` and on a stalled `:1049`) leaves the tier in the A2
  state.
- **Invisible:** no condition reports a Sentinel that knows too few peers or replicas or has no
  monitor, and `Ready`, `phase` and the alerts read other signals
  ([T74](074-no-condition-reports-a-sentinel-with-no-monitor-or-too-few-peers-or-replicas.md)).
- **Documentation:** a contributor who reads ADR 0022 D6 or `CLAUDE.md` concludes that no code
  path issues `SENTINEL RESET`; one does, and a unit test pins it. Five comments state a
  Sentinel cooldown that does not refuse the operator's retrigger.
- **No roll.** Nothing in this ticket touches a pod template, so no option rolls the fleet or
  endangers a single non-persistent pod through a roll, and upgrade neutrality
  ([ADR 0005](../adr/0005-upgrade-neutral-defaults-and-anti-affinity.md) D1) is untouched; every
  change reaches the fleet with the operator upgrade as runtime Sentinel commands only.

## Options

Two decisions. The removal of the RESET fallback itself (a failed REMOVE goes on to the MONITOR)
needs no decision - every kept option includes it and its alternatives were removed (History) -
and is in the Work list.

### D1 - Which master may `resetSentinelState` point every Sentinel at?

**Mechanism.** Today the function takes any address its caller passes, or pod-0 for `""`
(`:3628-3632`), and sends REMOVE + MONITOR to every Sentinel without checking that pod
(`:3653-3696`). The five callers are listed under Fact. Measured: a reset toward an unreachable
master rebuilds nothing until that address answers as a master (A, A2, B), and toward a pod that
comes back as a replica it rebuilds the wrong tier (C). The choice decides whether the operator
writes Sentinel's master authority without verifying it. It does not change the callers' state
transitions or the bounds of [ADR 0010](../adr/0010-every-rolling-update-wait-is-bounded.md),
because the function returns nothing and every caller proceeds whatever it did (`:958`, `:1007`,
`:1049` return nil; `:3161` goes on to the reset count and `replaceRemainingPods`, `:3163-3180`;
`:3308` goes on to `clearSentinelAwarenessTimestamp`, `setFailoverTimestamp` and
`stateFailoverReset`, `:3312-3323`). It does not change `persistKnownMaster` (`:1045`) or
`forceReplicaConnections` (`:983`, `:3159`). It does not decide how many Sentinels a verified
reset touches (D2).

- **A - no gate.** Only the decision-free fallback fix and comment corrections (Work list) land;
  every caller keeps resetting toward whatever it names. Cost XS. Consequences: ADR 0022 D6 and
  `CLAUDE.md:743` become true as written, and nothing changes on the routine path. The measured
  tier-wide empty-table state stays reachable at `:958`, `:1007`, `:3308` and on a stalled
  `:1049` - exactly the paths on which the master is least trustworthy - together with a reset
  toward one of two masters chosen by ordinal.
- **B - gate on a verified master, and delete the two stall resets (recommended).** Before the
  loop, resolve the pod the address names and require that it is `available()` (Ready and not
  being deleted, [ADR 0026](../adr/0026-a-pod-being-deleted-is-not-available.md) D1) and answers
  `role:master` to `INFO replication` now; otherwise log and return without a command. Per site:
  - `:958` is deleted: it runs only when the pass counted zero or several masters, so it names a
    pod the operator picked by ordinal among masters, or a replica, and could never be verified
    as the single master.
  - `:1007` is deleted: it runs only when INFO on that master has just failed. A gate that
    re-asked INFO would let a flaky first answer pass on the second; deleting it matches `:958`,
    and the stalled finalization then completes without a Sentinel write, leaving Sentinel's own
    tracking intact.
  - `:1049`: uniqueness already holds by construction (`masterCount == 1`, `:951`); the gate adds
    `available()` and a fresh `role:master`, so a stalled finalization skips a terminating master.
  - `:3308`: the gate also requires that the named pod is the only pod the pass's scan marks
    master (`countMasters(freshPods) == 1`), so the operator never chooses among masters for
    Sentinel. When it skips, Sentinel keeps its tables and can fail over an unreachable master
    itself, and the retrigger after `failoverResetMinWait` is refused by no cooldown (F5), only
    by `-NOGOODSLAVE` or an attempt in progress on that one Sentinel, which the trigger loop
    moves past (`:3711-3733`).
  - `:3161`: two pods answering master is the designed post-failover state (the promoted pod and
    the outgoing one until it is demoted,
    [ADR 0025](../adr/0025-a-split-brain-warning-means-one-that-did-not-resolve-itself.md)), and
    the scan precedes `forceReplicaConnections`, so a uniqueness condition would skip the very
    case the site exists for. The gate therefore asks only `available()` and `role:master` here:
    the pod is the new-image master that Sentinel itself promoted after the roll's
    `SENTINEL FAILOVER`, so the operator is not choosing (by reading: only Sentinel promotes a
    new-image pod during the roll; not measured). (Evaluating uniqueness after
    `forceReplicaConnections` was considered and not taken: that REPLICAOF takes effect
    asynchronously, and it is itself a gap of its own, filed as
    [T73](073-forcereplicaconnections-re-points-a-data-holder-at-an-empty-master.md).)

  Amend ADR 0022 D6: the operator's own reset is REMOVE + MONITOR, sent only toward a verified
  master, and never `SENTINEL RESET`; this also makes ADR 0025 `:219` ("resets Sentinel onto the
  pod answering master") true as written. Cost S: the gate (about fifteen lines, under the
  cyclomatic limit), the two call deletions, unit tests with revert checks (ADR 0017) for a
  named pod that is terminating, not Ready, answers `role:slave`, does not answer, is one of two
  scanned masters at `:3308` (no command sent), and one that verifies (commands sent), plus
  tests that a stalled finalization sends no Sentinel command; the stalled-finalization tests
  (`rolling_update_test.go:2849`, `:2901`, `:4625`) are re-read for assertions on the deleted
  resets (not checked here); the full e2e suite on both legs, because the gate sits on `:1049`,
  the path every Sentinel data roll takes (the `:3308` retry runs only when a failover takes
  more than 30 s, which was not measured in CI). Consequences: no new wait and no new state; one
  extra INFO on the final pass of every Sentinel roll; no roll. What it leaves: the rebuild
  window of a verified reset (D2), a MONITOR that fails at a name that does not resolve (rare
  once the named pod is verified, because its FQDN resolves while it has an IP; DNS for a
  terminating pod not verified), and the uncapped reset-and-retrigger cycle (ADR 0010 Residual
  risks `:775-784`, unchanged; filed as
  [T75](075-the-sentinel-failover-reset-and-retrigger-cycle-has-no-cap.md)).

**B is marked.** Checkable in three ways. (1) Skipping an unverified reset loses nothing the
reset could gain: measured on both images, a reset toward an unreachable master rebuilds nothing
until that address answers as a master (A2, B), and toward a replica it rebuilds the wrong tier
(C). (2) It needs no new state and no wait - the function returns nothing and every caller
proceeds - so ADR 0010 is untouched, and it keeps the operator from choosing among masters for
Sentinel, in the spirit of ADR 0011 and ADR 0028. (3) It restores what the authors had already
decided: `f42f450` gated this reset as "destructive" and `c906950` removed the gate for CI
flakiness, and archive 039 recorded the measured rule "Any reset path must gate on a reachable
master reporting `flags=master`" (`:418-424`). B beats A, the runner-up, because A saves only the
gate and one INFO per roll and keeps the measured tier-wide failure on exactly the paths on which
the master is least trustworthy. If the full e2e run shows that a later test depends on a stalled
finalization having reset Sentinel, that is the evidence to re-weigh A, and it must be recorded
here.

### D2 - Does a verified reset still blank every Sentinel, or only the Sentinels that disagree?

**Mechanism.** Even with D1's gate, `resetSentinelState` sends REMOVE + MONITOR to every Sentinel
within milliseconds (`:3653-3696`), so every Sentinel is blind at once for the rebuild window
(Fact: about 2 s with every replica connected; at `:3161` and on a stalled `:1049` until the
missing replicas connect). `:1049` opens that window at the end of every Sentinel data roll. The
recorded reason for the finalization reset (`b13c83f`) is a Sentinel that monitors a stale
address; a running Sentinel persists a switched address itself, and a restarted one reads the
known-master annotation (Fact, Provenance). Measured: intact peers heal a reset Sentinel (D),
and a tier reset as a whole does not heal (A2, K). The choice applies at `:1049` and `:3161`.
At `:3308` the reset stays unconditional under both options: there it aborts a Sentinel attempt
in progress before the retrigger, which is its point (by reading, what that buys is avoiding two
concurrent attempts, not avoiding a refusal). At `:3161` there is no attempt left to abort: the
new master exists, and Sentinel's `failover-timeout` (60 s,
[`sentinel.go:50`](../../internal/builder/sentinel.go#L50)) has run out by the 90 s clock
(by reading, not measured). It does not change D1's gate, the callers' flow or the
decision-free fallback fix.

- **Keep the loop and record the window.** The ADR 0022 amendment under D1 states the window,
  its trigger (a master death within it) and its recovery (the address answering as master),
  with the 2026-09-27 measurements. Cost XS (ADR text). Consequences: every Sentinel roll keeps
  blanking the tier; at `:3161` the reset keeps dropping the replica list Sentinel holds after
  its own failover; the next roll keeps waiting in `isSentinelAwareOfReplicas`
  (`:3564`, `sentinelAwarenessTimeout`, comment `:130`).
- **Skip each Sentinel that already names the verified master (recommended).** Read
  `SENTINEL MASTER` first and leave a Sentinel alone if it answers, its `ip` is the verified
  address, its `flags` are exactly `master`, and - at a non-stalled `:1049` only, where a
  rebuild can only add replicas - its `num-slaves` is at least the expected count; at `:3161`
  there is no `num-slaves` clause, because the master's INFO cannot supply more replicas than it
  has. A Sentinel whose `SENTINEL MASTER` errors (no monitor, scenario E) counts as disagreeing
  and gets REMOVE + MONITOR, which with the decision-free fix re-adds its monitor. The peer count
  is not a criterion: ghost peers are left to an operator or the next Sentinel roll (ADR 0022
  D6). Cost: one `SENTINEL MASTER` round trip per Sentinel on existing plumbing
  (`SentinelMasterInfo` with `IP`, `Flags`, `NumSlaves`,
  [`client.go:37-52`](../../internal/valkeyclient/client.go#L37-L52); already read by
  `getSentinelMasterPodName`, `:1738`, and `isSentinelAwareOfReplicas`, `:3564`), a caller flag, unit
  tests with revert checks; the e2e run is the one B already requires. Consequences: a routine
  roll no longer blanks the tier; at `:3161` a Sentinel that followed its own failover keeps
  its tables (in the normal case every Sentinel is skipped there); the Sentinels that are reset
  restart their `config_epoch` at 0 and take the peers' epoch from their hellos naming the same
  address (by reading; [T34](034-e2e-fixtures-wait-on-controller-state-after-a-pod-delete.md)
  already warns that a test must not assume the epoch only grows). That the `ip` field holds the
  FQDN is by reading (`replica-announce-ip` is the FQDN, `announce-hostnames yes`) and matches
  the measurement output, which shows container hostnames there; the comparison in Kubernetes
  is not measured.

**Skip-if-agreeing is marked.** It keeps exactly the case the finalization reset was written for
(`b13c83f`: a Sentinel on a stale address) and drops only the wipe of Sentinels that already
agree, which `f42f450` called destructive and which nothing else is recorded to need: replicas
announce by hostname, so pod replacement leaves no stale replica entries, and ghost peers are D6's
business (Fact). It removes the window where it is longest (`:3161`, a stalled `:1049`) and
removes the tier-wide blanking on every roll, for one read per Sentinel in the same function and
under the same e2e run as D1. Keeping the loop, the runner-up, costs only ADR text and its window
is small on the common path (about 2 s at a non-stalled `:1049`), but its original justification
- that the reason for the unconditional reset is not recorded - was false, and it leaves the
longer windows at `:3161` and on a stalled `:1049`, whose consequence on a memory-only cluster
can be the dataset (Not verified, T36 route). If the archive 039 observation holds - `:1049` not
completing in production - either option changes less on the live fleet than it appears; that
does not change the ranking.

## Decision

Not decided.

## Work list

1. **XS, no decision needed (docs):** ADR 0022 describes a comment that no longer exists. In
   Context (`:68-70`) strike "the comment in `rolling_update.go` above `resetSentinelState` claims
   the opposite and is wrong" with a dated correction (the comment was corrected on 2026-09-27
   and now states the measured behaviour); in Residual risks (`:204-205`) mark the entry "The
   wrong comment above `resetSentinelState` was left in place …" as discharged on 2026-09-27; add
   a dated Status line ("Corrected 2026-09-27, no decision changes"). No ticket citation in the
   ADR (ADR 0034). Does not close this ticket. Not done in the filing pass. *(Done 2026-09-27 in
   `bcc63c9`: Status `:21-24`, Context `:75-76`, Residual risks `:210-212`; see History.)*
2. **XS, no decision needed (test comment):** correct the comment of
   `TestResetSentinelState_FallsBackToResetWhenRemoveFails`
   ([`sentinel_failover_test.go:1467-1469`](../../internal/controller/sentinel_failover_test.go#L1467-L1469)):
   REMOVE + MONITOR sets the address the caller names, and a plain RESET keeps the address each
   Sentinel holds (ADR 0022 Context). True under every option; with item 4 the test itself is
   rewritten in the same change.
3. **Measurement:** ~~in docker against both pinned images ... Settles the open question under
   Not verified.~~ *(Done 2026-09-27 at 84a39c2, on both images, with more scenarios than
   planned: Fact, Measured.)*
4. **XS, no decision needed (code):** remove the RESET fallback; a failed REMOVE goes on to the
   MONITOR. Keep the log at `:3667`, delete `:3668-3672` (the comment, the RESET block and the
   `continue`). If a monitor exists, the MONITOR answers "Duplicate master name." and leaves it
   unchanged, and the existing MONITOR-failure branch (`:3676-3679`, pinned by
   `TestResetSentinelState_SkipsTheParametersWhenTheMonitorAddFails`) skips the SETs; if it is
   missing, it is re-added - no error string is parsed. Rewrite
   `TestResetSentinelState_FallsBackToResetWhenRemoveFails` to assert REMOVE-error, then MONITOR
   and the SETs, with a revert check (ADR 0017). Delete `valkeyclient.SentinelReset`
   (`client.go:249-257`) and its tests (`client_test.go:685`, `exec_test.go:467`); the manual
   reset of ADR 0022 D6 is a `valkey-cli` step. This makes ADR 0022 D6 and `CLAUDE.md:743` true
   as written; ADR 0022 Residual risks gains the line that a monitor lost after a successful
   REMOVE and a failed MONITOR is missing until the next call. Independent of D1 and D2.
5. **XS, no decision needed (comments):** correct the cooldown premise at `:140-142`,
   `:3068-3070`, `:3527-3529` and `:3614-3615` (Fact); reword "SENTINEL RESET" to REMOVE +
   MONITOR at `:151`, `:3545` and `rolling_update_bounds_test.go:760`.
6. **XS, no decision needed (code):** delete the two per-master SETs that always fail
   (`:3685-3686`) and their expectations (`sentinel_failover_test.go:1458-1459`); the options are
   global and already in the config, so no Sentinel behaviour changes.
7. **XS, no decision needed (ADR text):** ADR 0022 Context (`:76-79`, "no way back ... no other
   channel to rebuild from"), dated and marked as no decision change: a single Sentinel reset
   while the master was unreachable was healed within 30 s by peers that kept their tables
   (scenario D); this does not apply to `resetSentinelState`'s own fallback, whose peers receive
   REMOVE + MONITOR in the same loop (scenario K); every Sentinel reset stays at `s_down`, 0/0,
   `NOGOODSLAVE` until the address answers as a master, and a reset toward a replica stays
   pointed at it (A2, B, C). ADR 0025 `:219` ("resets Sentinel onto the pod answering master")
   is imprecise at `84a39c2` (the scan also takes a label-only master or pod-0); correct it with
   the D1 amendment, or now as a dated precision if D1 stays open.
8. **Filing:** ~~three findings of the 2026-09-27 re-verification belong in files of their own
   (the filing rule, "every finding is a file"), each with the next free number~~ *(done
   2026-09-27: all three filed)*: (a) filed as
   [T73](073-forcereplicaconnections-re-points-a-data-holder-at-an-empty-master.md) -
   `forceReplicaConnections` re-points every Ready pod but the named master, including a data
   holder the ADR 0028 guard just protected, at a master that may be empty; (b) filed as
   [T74](074-no-condition-reports-a-sentinel-with-no-monitor-or-too-few-peers-or-replicas.md) -
   no condition reports a Sentinel with no monitor, or with too few peers or replicas; (c) filed
   as [T75](075-the-sentinel-failover-reset-and-retrigger-cycle-has-no-cap.md) - the Sentinel
   failover reset-and-retrigger cycle that ADR 0010 Residual risks (`:775-784`) records as "Not
   filed" has no cap.
9. **Measurement (informs D2, not needed for D1):** the operator log of a Sentinel data roll on
   an e2e or Kind cluster - "Sentinel reconfigured successfully" three times at finalization, or
   not, with V(1) enabled to see a failure - settles whether `:1049` completes (Not verified).
10. **Waits on the decision:** D1's and D2's code and tests, and the ADR 0022 D6 amendment, as
    listed under Options; `CLAUDE.md:743` stays true as written under every kept option.
11. Close ([ADR 0034](../adr/0034-tickets-are-work-lists-that-get-archived.md)): the rule into ADR
    0022 D6 (amended in place) with its residual risks, the contributor-facing sentence in
    `CLAUDE.md` only if it changes; `git grep` `T62` and `062-` outside `docs/tickets/`, then move
    to `archive/`.

## Cross-ticket findings

- [T51](051-a-changed-cluster-password-reaches-no-running-pod.md): `resetSentinelState`'s runtime
  `SENTINEL SET auth-pass` (`:3692`, password read at `:3647`). Under D1-B, a master that still
  requires the old password fails the gate's INFO (the client reads the new Secret value), so no
  `auth-pass` switch happens at that site - part of T51's open question whether a failover retry
  during the manual procedure switches Sentinel `auth-pass` early. T51's Sentinel half must not
  reuse `resetSentinelState`, which blanks every table (measured); it sends `SENTINEL SET
  auth-pass` directly. T51 already records that it does not treat this function as its plumbing (T51 `:323-326`;
  its note that T62 cites the function at `:3616` is outdated, this file cites `:3617` since the
  2026-09-27 re-verification). *(2026-09-27, consistency pass: 051 corrected that note and now
  records this gate's effect on its Fact bullet about the failover retry.)*
- [T35](035-master-records-lag-the-real-master.md) relies on `persistKnownMaster` at roll
  finalization (`:1045`). Every kept option leaves that call where it is, before
  `resetSentinelState` and on the verified path; a change that moved the gate above `:1045` would
  affect T35.
- [T36](036-non-persistent-master-restarts-empty.md): the tier-wide empty-table state is a new
  route into its mechanism on a cluster without persistence (Not verified, above).
- [T34](034-e2e-fixtures-wait-on-controller-state-after-a-pod-delete.md) lists the five callers
  (`:958`, `:1007`, `:1049`, `:3161`, `:3308`) and the `config_epoch` restart; under D1-B two of
  those calls are deleted, and under D2's recommendation fewer Sentinels restart their epoch.
- [T73](073-forcereplicaconnections-re-points-a-data-holder-at-an-empty-master.md) (Work list
  8a): `forceReplicaConnections`, called at `:3159` right before this ticket's `:3161` reset,
  sends REPLICAOF with no role or dataset check after the ADR 0028 guard of the same pass. D1-B's
  `:3161` gate does not depend on it, and T62 does not change it.
- [T74](074-no-condition-reports-a-sentinel-with-no-monitor-or-too-few-peers-or-replicas.md)
  (Work list 8b): no condition reports the monitor-less and emptied Sentinel states this ticket
  measured (E, A, K, C). Its debounce exists because every legitimate reset, including the
  `:1049` reset of every Sentinel data roll, briefly shows 0 peers and 0 replicas; under D2's
  recommendation fewer Sentinels go through that window.
- [T75](075-the-sentinel-failover-reset-and-retrigger-cycle-has-no-cap.md) (Work list 8c): the
  Sentinel failover reset-and-retrigger cycle has no cap; each cycle repeats the `:3308` reset
  this ticket gates. T62 does not change the cap.

## Verification

- Item 1: `git grep -n "claims the opposite and is wrong\|was left in place by this change" -- docs/adr`
  finds only struck text, and ADR 0022 Status carries the dated line. *(Holds at `84a39c2`: the
  hits are `:75` and `:210`, both struck.)*
- Item 2: ~~`grep -n "revert sentinel to the pod-0 address" internal/controller/*_test.go` is
  empty;~~ *(corrected 2026-09-27 at 84a39c2: that grep is already empty while the false comment
  is present, because the phrase breaks across `:1467` and `:1468`)*
  `grep -n "pod-0 address from its config file\|would revert sentinel" internal/controller/*_test.go`
  is empty (today it finds `:1467` and `:1468`); `make lint` is green.
- Item 4: `git grep -n "SentinelReset(" -- internal cmd` finds nothing; the rewritten test fails
  with the RESET fallback or the `continue` restored; `make test-unit`, `make lint`.
- Item 5: `grep -n -i "cooldown" internal/controller/rolling_update.go` finds only `:3665` or a
  corrected statement; `grep -n "SENTINEL RESET" internal/controller/rolling_update.go
  internal/controller/rolling_update_bounds_test.go` finds only the doc comment `:3608` (which
  describes RESET correctly).
- Item 6: `grep -n '"resolve-hostnames"\|"announce-hostnames"' internal/controller/rolling_update.go`
  is empty; `make test-unit`.
- D1-B: the unit tests above, each failing with the gate removed or a deleted call restored;
  `make test-unit`, `make lint`, `make cyclo`; the full e2e suite green on both legs
  (`single-node-valkey9`, `single-node-valkey8`); ADR 0022 D6 states the gated rule, and
  `git grep -n "SENTINEL RESET"` outside `docs/tickets/` finds no statement that contradicts it.
- D2 (recommended option): a unit test in which every Sentinel already names the verified master
  sends no REMOVE, one in which one disagrees resets that one only, and one in which
  `SENTINEL MASTER` errors resets that one; each fails with the check removed.

## History

- 2026-09-27: re-verified at 84a39c2. **Checked:** every location of the ticket, re-read at
  `84a39c2` (the Fact, Options and Work list links are updated in place; every working-tree line
  the ticket gave was one short, because the line model missed the +1 hunk of `bcc63c9` at
  `:419` - struck and corrected); `git diff 4a7543e 84a39c2`, `git blame`, `git log -S` on the
  reset's history; the upstream `sentinel.c` of 9.1.1 and 8.1.9; the data init container, the
  headless Service, the health checker, the labeler, the PrometheusRule and archive 039.
  **Measured** (docker, both pinned images, commands and results under Fact, Measured):
  scenarios A, A2, B, C, D, E, F5, K, the error replies and Duplicate. The inputs were an
  auditor report and a facts and a design skeptic; the skeptics re-measured the replies, A2 and
  K independently with the same results. **Found false or outdated:** the line model (above);
  "the verified half" of Impact as a standalone mechanism (a single reset Sentinel is healed by
  intact peers, D; in the code's loop it holds, K); the Fact bullet on ADR 0022 describing a
  gone comment (done in `bcc63c9`); the attribution of ADR 0026 D4's dying-master arm (D4's
  source meant `handleMasterWithNoReplicas`; since `360cb03` that arm cannot receive a
  terminating pod, so at `84a39c2` the live dying-master reset is `:3308` - the auditor's and the
  facts skeptic's readings were each right about one half); the Verification grep for item 2
  (vacuous); option B's cost "a skipped reset leaves Sentinel's failover cooldown in place, so
  the retrigger may be refused again" (no cooldown refuses a forced failover, read and measured);
  B's residual window "one round trip" (it is the rebuild window); B's premise that `:1049` and
  `:3160` always pass (a stalled `:1049` can name a terminating master; `:3161` can skip under a
  uniqueness rule); "the failover-retry path runs in every Sentinel roll test" (it runs only
  after a 30 s failover timeout; `:1049` is the path every roll takes); option D's open
  feasibility (no command exists); B's justification "the condition ADR 0022 D6 sets" (D6 sets
  two, B adopts the verified-master half). The open question is **answered** (it holds), the
  "from memory" upstream claim is verified. **New facts:** `:1049` runs on every Sentinel roll;
  `:958` runs only with zero or several masters; the monitor-less state (E); the cooldown
  comments are false; two SETs always fail; no table-preserving command exists; the rebuild
  window and why it is longer at `:3161` and on a stalled `:1049`; `SentinelMonitoring` has no
  reader; provenance `730008e`, `b13c83f`, `f42f450`, `c906950` (the auditor's attribution of the
  finalization reset to `c906950` was corrected by both skeptics: it came in `b13c83f` with a
  recorded reason); the archive 039 T8 observation (Not verified); the data-loss route on a
  cluster without persistence (inference, Not verified); the facts skeptic's line `:3684-3685`
  for the two SETs was checked and is `:3685-3686`. **Options:** Options rewritten as D1 and D2.
  D1 keeps B, amended: per-site gate (uniqueness at `:3308`, by construction at `:1049`, not at
  `:3161` where two masters are the designed state), and the `:958` and `:1007` calls deleted
  instead of gated. A stays as the runner-up (without the "skip that Sentinel" branch, which
  moved into the decision-free fallback fix). **Removed:** C (narrow ADR 0022 D6 and `CLAUDE.md`
  and accept) - it accepts a failure now measured on both pinned lines, silently, and contradicts
  ADR 0026 D4 without reopening it; D (commands that keep the tables) - the premise is false, no
  subcommand or SET option re-points a monitor and no cooldown needs clearing; E (delete the
  three unverified calls, no gate; proposed in this pass) - dominated by B, it leaves a stalled
  `:1049` able to name a terminating master; "keep the RESET fallback" (proposed as the status
  quo of a fallback decision) - it breaks ADR 0022 D6, is a no-op after a reply error, blanks one
  table after a transport failure and never re-adds a missing monitor; "skip that Sentinel on a
  failed REMOVE" (the old A/B text) - dominated at the same cost by going on to the MONITOR,
  which re-adds a lost monitor, so the fallback question has one sensible answer and became Work
  list item 4 instead of a decision; "reset one Sentinel per pass" (proposed in this pass for the
  window) - it needs new persisted state and a new ADR 0010 bound for a window of seconds, out
  of proportion. **Recommendation changes:** D1 unchanged in substance (B), amended as above. D2
  is new; the auditor recommended "keep the loop and record the window", resting on the claim
  that the reason for the unconditional reset is not recorded; the design skeptic refuted that
  (`b13c83f` records it, `f42f450` called the reset destructive) and showed the window is not
  seconds at `:3161` and on a stalled `:1049`, so "skip each Sentinel that already names the
  verified master" is marked and keeping the loop is the runner-up (the facts skeptic's point
  that the window is about 2 s at a non-stalled `:1049` is recorded; it makes the runner-up cheap
  on the common path and does not change the ranking; the auditor's rarity argument from the
Chaos Mesh schedule was not used, because that schedule kills pods only in the example namespace
`database-examples`, not in production, and could not be checked in this run). **Work list:** item 3 done; items 4-9
  added (the decision-free fallback fix, comment corrections, the two SETs, ADR 0022/0025 text,
  three findings to file, the archive 039 measurement). **Frontmatter:** `state` filed ->
  analysed (open question measured, every decision has a marked option); `severity` stays medium,
  now measured rather than estimated, with the data-loss escalation named as not measured;
  `urgency` stays now by rule 1, carried by the test comment, with the cooldown comments as a
  second measured-false statement for the delay after a completed failover (the INPROG case is
  not measured) and rule 1's first clause checked and not matched (released since `b13c83f`);
  `effort` stays S, at its upper end; `blocked-by` stays decision, and "CLAUDE.md edits also need
  Hans" is dropped because it applied only under the removed option C. Title and file name kept.
  **Not verified:** the Kubernetes behaviour of the measured states, `:1049` in production, the
  data-loss route, the labeler inference, DNS for a terminating pod, the INPROG case, whether
  anything depends on the routine wipe. No make target, `go test` or cluster was run; every
  docker container and network was removed.
  Cross-ticket: in the consistency pass of the same day, the T51 note records that 051 corrected
  its `:3616` cite and now carries this ticket's gate effect on its failover-retry Fact bullet;
  036 now names this ticket's empty-table route into its mechanism.
  Filed: Work list item 8 is done - 8a as
  [T73](073-forcereplicaconnections-re-points-a-data-holder-at-an-empty-master.md) (high,
  security none), 8b as
  [T74](074-no-condition-reports-a-sentinel-with-no-monitor-or-too-few-peers-or-replicas.md)
  (medium, hardening) and 8c as
  [T75](075-the-sentinel-failover-reset-and-retrigger-cycle-has-no-cap.md) (medium, security
  none), all tracked; the three Cross-ticket "needs its own file" bullets became pointers, D1-B's
  notes on the `:3161` REPLICAOF and on the uncapped cycle link T73 and T75, and the Fact bullet
  on the missing report was cut to the fact this ticket's severity needs (the states are silent,
  the pod stays Ready), the health-checker and alert reading now living in T74. Frontmatter
  unchanged: severity, urgency, effort and the D1 and D2 recommendations rest on the measured
  reset hazard, not on the three moved findings.
- 2026-09-27: work list item 1 landed in the text-vs-code review of the maintenance branch: ADR
  0022 Context and Residual risks strike the two sentences about the wrong comment in place, and
  the Status carries a dated "Corrected 2026-09-27 (no decision changes)" line. Item 1 does not
  close this ticket. **Verified:** by reading the corrected comment above `resetSentinelState`.
  **Not verified:** nothing was run.
- 2026-09-27: filed from the ticket enrichment of that day (the review of the security-hardening
  family). The orchestrator found the fallback by reading at `4a7543e`; this file re-read the
  five callers, the fallback, `Client.exec`, the unit test, `staleSentinelPods`, ADR 0022 Status,
  Context, D6 and Residual risks, ADR 0026 D4 and the ADR 0010 residual risk in the working tree,
  and added: the stall gate on the completion hold (`:911`), that ADR 0026 D4 already reads the
  REMOVE + MONITOR path as the unrecoverable direction, the per-command dial, the false test
  comment, that an emptied table is not reported, and the two comments that call REMOVE +
  MONITOR a RESET. Severity `medium` is an estimate (frontmatter). Urgency `now` by rule 1:
  `CLAUDE.md:743` and ADR 0022 D6 state an unconditional rule that `rolling_update.go:3668`
  breaks, and the test comment repeats a claim ADR 0022 measured false. **Verified:** by reading.
  **Not verified:** nothing was run; the REMOVE + MONITOR hazard and the upstream REMOVE error
  are open (Not verified).
