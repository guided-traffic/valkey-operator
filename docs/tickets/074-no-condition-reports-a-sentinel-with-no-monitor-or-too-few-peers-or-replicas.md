---
id: T74
title: No condition reports a Sentinel that has no monitor, or knows too few peers or replicas
state: analysed       # facts re-read at 84a39c2, the rebuild window measured on both pinned images (docker, 2026-09-27, with and without auth, REMOVE + MONITOR and SENTINEL RESET), adversarially reviewed the same day, both open decisions carry a marked option (History 2026-09-27)
severity: medium      # if never fixed: a Sentinel tier that cannot fail over (measured states E, A/K and C of T62) stays invisible - Ready, phase, every condition and every shipped alert read other signals - until a master failure turns it into an outage, and on a cluster without persistence possibly into a lost dataset (T36 route, not measured); kept below high because the defect only hides the states, it does not produce them
security: hardening   # derived, not in doubt: the states arise with no principal at all (T62's reset paths, a botched manual reset under ADR 0022 D6); the security relation is detection only - the path by which a principal can empty or remove a monitor is spec.sentinel.disableAuth, documented and opt-in (docs/security/secrets-and-tls.md:139-142, H-8), and a report opens or closes no path, so this is not boundary and no embargo applies
threat: "no attack path today, and none is needed for the defect; a report would additionally surface a Sentinel whose monitor was removed or pointed elsewhere by anyone who can send Sentinel commands - every client that reaches port 26379/36379 while spec.sentinel.disableAuth is true, or a holder of the cluster password - which today leaves the tier unable to fail over with nothing on the CR, in the metrics or in the alerts"
urgency: now          # rule 1, second clause, by the house reading used in T62 and T77 (a statement in a tracked file shown false by reading the code against measured states): valkey_controller.go:2379-2380 writes "Every Sentinel knows N other Sentinels, as expected" into the status of every Sentinel CR on any measured pass in which no Sentinel knows MORE than N - including a pass in which a Sentinel knows 0 (measured window after every reset, Fact) or has no monitor at all (T62 scenario E); the api/v1 doc comment :117-119 says the condition "clears itself once the tables agree with the replica count", which it also does when they do not. The operator itself was not run against those states, so this is false by reading of the operator code against measured Sentinel replies, not measured: T62, T73 and T77 take that as rule 1 (T75 records T18 and T23 doing the same), while T71, T75 and T78 read rule 1 strictly (measured only), and under the strict reading rule 1 does not match and rule 3 (severity medium, trigger live - the states are reachable through released code) gives next. Once the decision-free text correction (Work list item 1) lands, recompute: rule 3 gives next under either reading
effort: M             # option D1-B with D2-A: a typed reply error in valkeyclient, per-Sentinel fields in the health observation, one evaluator with an in-memory first-seen clock and a recheck, a new ConditionType with a registry row, unit tests with revert checks, one new e2e and one assertion in an existing one, ADR 0022 amended, README table, status.md, monitoring.md and one PrometheusRule alert
blocked-by: decision  # D1 and D2, below; Work list items 1 and 2 need none
filed-from: T62 (ticket 062, Work list item 8b, the Fact bullet "An emptied or missing monitor is not reported" and the Impact bullet "Invisible") during the re-verification of 2026-09-27 at 84a39c2
opened: 2026-09-27
decided:              # not recorded - no decision yet
done:                 # not recorded - not done
---

# T74 - No condition reports a Sentinel that has no monitor, or knows too few peers or replicas

Filed on 2026-09-27 from [ticket 062](062-resetsentinelstate-falls-back-to-sentinel-reset.md)
(`resetSentinelState` and its RESET fallback), whose re-verification at `84a39c2` found the gap,
recorded it as its Work list item 8b and named it a different mechanism from T62's own: the
health pass and a condition that owes a `conditionRegistry` row under
[ADR 0027](../adr/0027-conditions-are-levels-edges-or-history.md). Everything ticket 062 and the
run records of that re-verification held about the finding is moved here; ticket 062 keeps a
pointer. The run records lived in an ephemeral scratchpad; what is used from them is written
below.

## Fact

**Mechanism.** On a Sentinel cluster the status step calls `CheckCluster` only in the branch of
`updateHAStatus` where every Valkey and every Sentinel pod is Ready
([`valkey_controller.go:2458-2462`](../../internal/controller/valkey_controller.go#L2458-L2462)),
and `CheckCluster` reaches the Sentinels only after it found a master by `INFO replication` and
read that master's replication info
([`checker.go:108-125`](../../internal/health/checker.go#L108-L125), the Sentinel branch
[`:133-138`](../../internal/health/checker.go#L133-L138)). `observeSentinels`
([`checker.go:295-345`](../../internal/health/checker.go#L295-L345)) then sends
`SENTINEL MASTER <monitor>` to every Sentinel ordinal and keeps two things from each reply:
`num-other-sentinels` into the peer map, and whether `flags` is exactly `master` into a counter
([`:336-341`](../../internal/health/checker.go#L336-L341)). A Sentinel whose command fails, for
any reason, is logged at V(1) as "Sentinel not responding" and left out
([`:330-334`](../../internal/health/checker.go#L330-L334)). The only condition written from that
observation is `SentinelPeersStale`
([ADR 0022](../adr/0022-sentinel-identity-is-pinned-to-the-pod.md) D4, D5), and it looks only for
a **surplus**: `staleSentinelPods` returns a pod when `known > SentinelPeersExpected`
([`valkey_controller.go:2415-2424`](../../internal/controller/valkey_controller.go#L2415-L2424)).
Nothing looks for a **deficit**.

**Verified (read at `84a39c2`):**

- **Three states go unreported.** (1) A Sentinel with **no monitor** for this cluster answers
  `SENTINEL MASTER` with `ERR No such master with that name` (measured on both pinned images,
  T62 scenario E, commands below). `Client.exec` turns every RESP `-` reply into a plain `fmt`
  error, `valkey error: ...`
  ([`client.go:479-481`](../../internal/valkeyclient/client.go#L479-L481)), so the checker cannot
  tell "answered that it has no monitor" from "did not answer", and drops the pod either way. (2)
  A Sentinel that knows **fewer peers** than `replicas - 1` is kept in the map and never
  selected. (3) A Sentinel that knows **fewer replicas** than the data tier has: `num-slaves` is
  parsed into `SentinelMasterInfo.NumSlaves`
  ([`client.go:37-52`](../../internal/valkeyclient/client.go#L37-L52),
  [`:647`](../../internal/valkeyclient/client.go#L647)) and never carried into
  `health.ClusterState`
  ([`checker.go:24-54`](../../internal/health/checker.go#L24-L54)).
- **`SentinelPeersStale` writes an all-clear that states agreement.** With at least one Sentinel
  in the map and none above the expected count, `recordSentinelPeerDrift` writes `False`, reason
  `SentinelPeersConsistent`, message "Every Sentinel knows %d other Sentinels, as expected"
  ([`valkey_controller.go:2372-2381`](../../internal/controller/valkey_controller.go#L2372-L2381)).
  On a pass where one Sentinel knows 0 peers, or where one has no monitor and the others
  answered, that message is false. The doc comment of the type says it "clears itself once the
  tables agree with the replica count"
  ([`valkey_types.go:117-119`](../../api/v1/valkey_types.go#L117-L119)); it also clears when they
  do not. ADR 0022 D5 (`:110-113`) says "False when they all agree with the replica count", which
  is incomplete in the same way. This is an instance of the third level hazard of ADR 0027 D1
  (`:103-105`, extended 2026-09-26): the silence of a Sentinel that answered with an error is read
  as part of the all-clear.
- **`SentinelMonitoring` has no reader.** `ClusterState.SentinelMonitoring`
  ([`checker.go:41-42`](../../internal/health/checker.go#L41-L42), set at
  [`:135`](../../internal/health/checker.go#L135) from `monitoring()`,
  [`:290-292`](../../internal/health/checker.go#L290-L292), a majority answering `flags` exactly
  `master`) is read by no production code (`git grep -n SentinelMonitoring -- '*.go'`: outside
  tests only `checker.go:41`, `:42`, `:135`). Its doc comment ("true when sentinel instances
  agree on the master") overstates it: no address is compared. The health-package tests assert
  `observeSentinels` through `monitoring()`
  ([`checker_live_test.go:515`](../../internal/health/checker_live_test.go#L515), `:616`,
  [`checker_paths_test.go:533`](../../internal/health/checker_paths_test.go#L533), `:549`,
  `:598`, `:636`, `:659`), so removing it moves those assertions.
- **The Sentinel pod stays Ready in every such state.** The readiness probe is `PING`
  ([`sentinel.go:420-446`](../../internal/builder/sentinel.go#L420-L446)), and a monitor-less
  Sentinel answers `PONG` (T62 scenario E).
- **The one existing `num-slaves` reader looks at one Sentinel.** `isSentinelAwareOfReplicas`
  ([`rolling_update.go:3564-3605`](../../internal/controller/rolling_update.go#L3564-L3605)) is
  the pre-failover wait of the next roll, bounded by `sentinelAwarenessTimeout` = 90 s
  ([`:128-133`](../../internal/controller/rolling_update.go#L128-L133)). It returns on the first
  Sentinel that answers and skips one whose command fails, so a monitor-less Sentinel-0 is
  skipped there too. It reports nothing.
- **No alert and no metric reads Sentinel state.** The shipped PrometheusRule has eight alerts
  (`grep -c "alert:"`), none on Sentinel state; the one `sentinel` hit (`:138`) is TLS text
  ([`prometheusrule.yaml`](../../deploy/helm/valkey-operator/templates/prometheusrule.yaml)).
  Every status condition is already exported as `vko_valkey_status_condition{condition, status,
  reason}` by the collect-time collector
  ([`collector.go:186-192`](../../internal/metrics/collector.go#L186-L192),
  [ADR 0021](../adr/0021-per-resource-metrics-and-the-alert-that-was-missing.md)), so a new
  condition is alertable with no collector change. ADR 0021 forbids a gauge written from a
  reconcile pass, so a per-Sentinel metric is not available.
- **The health pass reads the Sentinels on the very pass that reset them - when that pass rolls
  no Sentinel.** `resetSentinelState` sends REMOVE + MONITOR to every Sentinel on the final pass
  of every Sentinel data roll
  ([`rolling_update.go:1049`](../../internal/controller/rolling_update.go#L1049), loop
  [`:3649-3696`](../../internal/controller/rolling_update.go#L3649-L3696); T62 Fact). That pass
  returns `Completed`, not a requeue
  ([`rolling_update.go:330-335`](../../internal/controller/rolling_update.go#L330-L335);
  `finishDataRoll` says so at [`:391-393`](../../internal/controller/rolling_update.go#L391-L393):
  "the Sentinel roll and the status write still run now"), so `reconcileWorkload` goes on to
  the Sentinel roll and then to `updateStatus`
  ([`valkey_controller.go:335-369`](../../internal/controller/valkey_controller.go#L335-L369)).
  Which of the two decides the pass (by reading, not run): **(a)** when no Sentinel pod is
  outdated - a data-tier-only change such as `spec.resources`, which reaches data pods only
  (Sentinel containers take `GetSentinelResources`,
  [`sentinel.go:398-401`](../../internal/builder/sentinel.go#L398-L401)) - the Sentinel roll
  returns nothing, and with every pod Ready the pass reaches `observeSentinels` milliseconds
  after the last MONITOR; **(b)** when the Sentinel pods are outdated too - every `spec.image`
  change, since the tiers share it, and the Sentinel StatefulSet template is written each pass
  by the resource step ([`valkey_controller.go:1447`](../../internal/controller/valkey_controller.go#L1447))
  with no roll gate found - the same pass deletes the first Sentinel pod and returns a requeue
  ([`rolling_update.go:5099-5102`](../../internal/controller/rolling_update.go#L5099-L5102)),
  `handlePostRollingUpdateChecks` ends the pass
  ([`valkey_controller.go:482-484`](../../internal/controller/valkey_controller.go#L482-L484),
  [`:413-416`](../../internal/controller/valkey_controller.go#L413-L416),
  [`:363-366`](../../internal/controller/valkey_controller.go#L363-L366)), and no status write
  runs until the Sentinel roll has finished, long after the reset's window. A
  healthy pass then returns no requeue
  ([`valkey_controller.go:391-396`](../../internal/controller/valkey_controller.go#L391-L396)):
  the next measurement comes from an owned-object event or the 10 h cache resync (ADR 0022 D7
  and its Residual risks `:203-207`). The only poll today is `SentinelPeersStale`'s 5 min
  recheck, and only while it is True
  ([`valkey_controller.go:2352`](../../internal/controller/valkey_controller.go#L2352),
  [`:2397`](../../internal/controller/valkey_controller.go#L2397)).
- **A replaced Sentinel is not read inside its window.** `CheckCluster` runs only with every
  Sentinel pod Ready (above), and the Sentinel readiness probe starts after
  `InitialDelaySeconds: 5` ([`sentinel.go:537-540`](../../internal/builder/sentinel.go#L537-L540)),
  while a replaced Sentinel knows 0 peers for under 1 s in docker (M3 below). By reading plus
  the docker timing; the Kubernetes timing of a replaced Sentinel is not measured.
- **Sentinel never forgets a replica either.** `num-slaves` counts known replica entries,
  including dead ones (measured, M4 below), and replicas announce by hostname
  (`replica-announce-ip $MY_HOST`,
  [`statefulset.go:355`](../../internal/builder/statefulset.go#L355)), so the count neither drops
  when a data pod dies nor grows when one is replaced. A count below `spec.replicas - 1` on a
  pass where the master's INFO lists every replica therefore means either that the Sentinel is
  not reading that master - it monitors another address (T62 scenario C, measured
  `num-slaves=0`), or it was reset and has not yet run its first INFO after the reset (at once
  after REMOVE + MONITOR, only at the next 10 s period after `SENTINEL RESET`, M7 below) - or
  that its view is one INFO period old: a replica was added by a scale-up and the Sentinel's
  next periodic INFO, at most 10 s away (9.1.1 `sentinel.c:86`, `:3051`), has not run yet (by
  reading, not measured).
- **Provenance.** `recordSentinelPeerDrift` came with the pinned identity in `1b1f6ed`
  (2026-08-23), released in v1.12.0 (`git tag --contains 1b1f6ed`).

**Measured 2026-09-27** (docker, `valkey/valkey:9.1.1` and `valkey/valkey:8.1.9`; one master and
two replicas, `valkey-server --port 6379 --save '' --appendonly no`, replicas with
`--replicaof <m> 6379 --replica-announce-ip <own container name>`; Sentinels started from a file
in the operator's line order: `port 26379`, `dir /data`, `sentinel monitor mymaster <m> 6379 2`,
`sentinel down-after-milliseconds mymaster 5000`, `sentinel failover-timeout mymaster 60000`,
`sentinel parallel-syncs mymaster 1`, `sentinel resolve-hostnames yes`,
`sentinel announce-hostnames yes`; values read with `valkey-cli -p 26379 SENTINEL MASTER mymaster`;
25 s settle time before each scenario; containers `vko-file-074-*`, `vko-file-074f-*`,
`vko-file-074r-*`, `vko-file-074d-*`, and for M5 to M7 `vko-file-074x-*` with Sentinel files
kept under `/tmp/s` in the container and 20 s between M5, M6 and M7, all removed afterwards,
checked with `docker ps -a` and `docker network ls`). No TLS, docker DNS instead of CoreDNS; no
auth in M1 to M4. Upstream source for M6 and M7 read at the pinned tags
(<https://raw.githubusercontent.com/valkey-io/valkey/9.1.1/src/sentinel.c>,
<https://raw.githubusercontent.com/valkey-io/valkey/8.1.9/src/sentinel.c>).

| Scenario | Commands | Result |
|---|---|---|
| M1: the `:1049` shape, healthy master, all three Sentinels reset | per Sentinel in turn via `docker exec`: `SENTINEL REMOVE mymaster`, `SENTINEL MONITOR mymaster <m> 6379 2`, `SENTINEL SET mymaster down-after-milliseconds 5000`, `... failover-timeout 60000`, `... parallel-syncs 1` (the loop took 1.09 s on 9.1.1, 1.1 s on 8.1.9); read all three about every 0.75 s from the end of the loop | both images: +0.1 s all three `flags=master num-slaves=2 num-other-sentinels=0`; +1.5 to +1.6 s `num-other-sentinels` 1, 1, 2; +2.3 s all three 2/2, stable to +21.8 s (9.1.1; the 8.1.9 read stopped at +3.8 s, still 2/2) |
| M2: one Sentinel, read from inside its container | `docker exec <s1> sh -c '...'`: REMOVE, MONITOR, then `SENTINEL MASTER` every 0.1 s, time from `date +%s.%N` | 9.1.1: +0.00 s `flags=master,disconnected num-slaves=0 num-other-sentinels=0`; +0.11 s `flags=master num-slaves=2` peers 0; +1.01 s peers 1; +1.13 s peers 2. 8.1.9: the same, peers 1 at +1.16 s and 2 at +1.27 s |
| M3: a Sentinel pod replaced (fresh config, same pinned `sentinel myid`) | three Sentinels with `sentinel myid 1111...`, `2222...`, `3333...` (40 hex digits); `docker rm -f <s3>`; start `<s3>` again with a fresh file and the same id, read it every 0.1 s from process start | 9.1.1: +0.11 s `num-slaves=2`, peers 0; +0.87 s peers 1; +0.98 s peers 2. 8.1.9: +0.11 s 2/0; +0.64 s peers 1; +0.85 s peers 2. `<s1>` still knows 2 peers afterwards (the pinned id switches the address, ADR 0022 D1) |
| M4: a replica disappears | one Sentinel, quorum 1; `docker rm -f <r2>`; read at +10 s and +40 s, and `SENTINEL REPLICAS mymaster` | both images: `num-slaves=2` at both reads; `SENTINEL REPLICAS` lists `<r2>:6379` with `flags=s_down,slave` |
| M5: the full `resetSentinelState` command order with auth (adversarial review) | fresh cluster as above plus `--requirepass pw --masterauth pw` on every data node and `requirepass pw`, `sentinel auth-pass mymaster pw` in every Sentinel file; in `<s1>` via one `sh -c`: REMOVE, MONITOR, SET `down-after-milliseconds`, `failover-timeout`, `parallel-syncs`, `resolve-hostnames yes`, `announce-hostnames yes`, `auth-pass pw` (the order of [`rolling_update.go:3666-3692`](../../internal/controller/rolling_update.go#L3666-L3692)), then `SENTINEL MASTER` every 0.1 s | both images: the two hostname SETs answer `ERR Unknown option or number of arguments`; +0.01 s `num-slaves=0` peers 0 (9.1.1 `flags=master,disconnected`, 8.1.9 `flags=master`); +0.12 s `num-slaves=2`; peers 2 at +1.03 s (9.1.1) and +1.12 s (8.1.9) - the same as M2 |
| M6: as M5, `auth-pass` SET 0.5 s late | `<s2>`: REMOVE, MONITOR, SET `down-after-milliseconds`, `sleep 0.5`, SET `auth-pass pw`, then read every 0.1 s from the auth SET | both images: `flags=master,disconnected num-slaves=0` until +0.35 s, `flags=master num-slaves=2` at +0.69 s (9.1.1) and +0.60 s (8.1.9); peers already 2 at the first read. The link without auth learns no replica; the SET drops the links (9.1.1 `sentinel.c:4336-4343`, `dropInstanceConnections` at `:4341`; 8.1.9 `:4258-4265`) and the first INFO follows the reconnect because `info_refresh` is still 0 |
| M7: `SENTINEL RESET` on one Sentinel, healthy master (the documented `SentinelPeersStale` remedy, and T62's fallback) | `<s3>`: `SENTINEL RESET mymaster` (answers `1`), then read every 0.1 s | 9.1.1: +0.01 s `master,disconnected` 0/0; +0.12 s `master`, `num-slaves=0` peers 1; +0.93 s peers 2; `num-slaves=2` only at +9.85 s. 8.1.9: peers 1 at +0.70 s, 2 at +0.82 s, `num-slaves=2` at +9.42 s. Upstream: `sentinelResetPrimary` (9.1.1 `sentinel.c:1528-1558`, 8.1.9 `:1509-1539`) closes the links but leaves `info_refresh` set, so the next INFO waits for the 10 s period (`:3051`, 8.1.9 `:3007`, period `:86`); MONITOR creates a fresh instance with `info_refresh = 0` (9.1.1 `:1343`, 8.1.9 `:1327`), whose first INFO goes out on the first link |

From T62 (measured 2026-09-27, both images, commands in ticket 062 under Fact, Measured):
scenario E, a MONITOR that fails after a successful REMOVE, leaves `SENTINEL MASTER` answering
`ERR No such master with that name` and `PING` answering `PONG`, and the `resetSentinelState`
shape never re-adds the monitor; scenarios A and K, REMOVE + MONITOR toward an unreachable
master on every Sentinel, leave all three at `flags=s_down,master num-slaves=0
num-other-sentinels=0` with a forced failover answering `NOGOODSLAVE`; scenario C, the address
back as a replica of a promoted pod, leaves all three at `num-slaves=0 num-other-sentinels=2`
naming the replica at +30 s and +90 s.

So a legitimate REMOVE + MONITOR or a replaced Sentinel shows **0 peers for about 1 to 2.3 s**
and **0 replicas only until its first INFO, within 0.11 s** when every replica is connected and
the auth password is already set (upstream sends the first INFO on the first link, 9.1.1
`sentinel.c:3051`, T62), and `flags=master,disconnected` in its first instant. With auth the
replica half lasts until the `auth-pass` SET, the eighth command of the loop, plus about 0.6 s
(M6). A legitimate **`SENTINEL RESET` shows 0 replicas for up to the 10 s INFO period**
(M7: 9.4 to 9.9 s in docker; [archive/039:4061-4062](archive/039-findings-from-the-1-11-0-fleet-rollout.md)
measured 3 to 9 s on Kind with 9.1.1, and `:565-567` "back within ~9 s per pod", also on Kind,
both after RESET). A deficit lasts longer only when the master itself lists fewer replicas (T62:
`:3161`, a stalled `:1049`), and then the health pass sees `AllSynced` false.

**Not verified:**

- **The Kubernetes timings of REMOVE + MONITOR.** The measurements ran in docker with no TLS;
  the operator dials each command anew with TLS and AUTH ([`client.go:429-452`](../../internal/valkeyclient/client.go#L429-L452)),
  so the seven commands before `auth-pass` take seven dials (M6 shows why that gap counts), and
  names resolve through CoreDNS. The only Kubernetes timings on record are archive 039's, and
  they are of `SENTINEL RESET` (M7's mechanism). That the completing pass of a data-tier-only
  roll actually reads a Sentinel inside its window, and that an image-change roll does not
  (Fact), are by reading, not observed.
- **`flags` of a Sentinel that monitors a replica** (T62 scenario C recorded the counts, not the
  flags). The count criterion below does not depend on it.
- **Whether `ip` in `SENTINEL MASTER` equals the FQDN the health pass dials** in Kubernetes (T62
  D2 records the same gap). No option below compares addresses.
- **How often the states occur on the fleet.** The operator log of a stalled finalization or a
  timed-out failover has not been inspected for them; T62's archive 039 observation (`:1049`
  perhaps not completing in production) is unresolved there.

## Impact

Sentinel-enabled clusters only. Nothing on the CR, in `vko_valkey_*` or in the shipped alerts
distinguishes a tier that can fail over from one that cannot:

- **A Sentinel with no monitor** (T62 scenario E: a MONITOR that failed after a successful
  REMOVE, today reachable through `resetSentinelState` whenever the named address does not
  resolve, such as the pod-0 fallback while pod-0 is recreated; or a manual `SENTINEL REMOVE`)
  casts no vote and never recovers on its own; its pod stays Ready and on the current template,
  so the operator never replaces it. With two of three in that state the tier has lost its
  quorum. Today: absent from the peer map, `SentinelPeersStale=False` with the message that every
  Sentinel knows the expected count.
- **A tier reset toward an unreachable master** (T62 scenarios A, K) cannot fail over until the
  address answers as a master again. Mostly not measured by the health pass at all, because
  `findMaster` fails first and the phase already reads `Error`; reported only in the rarer case
  where the operator reaches the master and the Sentinels do not.
- **A tier pointed at a replica** (T62: the `:958` pick of pod-0 "possibly a replica"; scenario
  C) knows 0 replicas, so a failover it starts answers `NOGOODSLAVE`, while the data plane is
  healthy and `Ready=True`, `phase=OK`.
- **On a cluster without persistence** a failover that cannot happen can end in an empty master
  flushing its replicas (T62, Not verified; the mechanism of
  [T36](036-non-persistent-master-restarts-empty.md)).
- **The false all-clear** misleads whoever reads `SentinelPeersStale` as "the Sentinel tables are
  fine", which its message states.
- **Why a report needs a debounce.** The completing pass of every data-tier-only roll on a
  Sentinel cluster reads the Sentinels inside the rebuild window (Fact, case (a)) and schedules
  no follow-up, so a naive level would go True at the end of every such roll and stay True until
  the next event or the 10 h resync. An image-change roll does not reach the health pass inside
  the window (case (b)). Two more legitimate transients, by reading: a manual `SENTINEL RESET`
  (the documented `SentinelPeersStale` remedy, up to 10 s of 0 replicas, M7) read by any pass,
  including `SentinelPeersStale`'s own 5 min recheck; and a data scale-up, until each Sentinel's
  next periodic INFO (at most 10 s).

**Security relation (the threat line, expanded).** No principal is needed: the states come from
the operator's own reset paths (T62) or from a manual reset (ADR 0022 D6). The additional
coverage a report gives is detection of a principal who sends Sentinel commands: with
`spec.sentinel.disableAuth: true` any client that reaches port 26379/36379 may `SENTINEL REMOVE`
the monitor or `SENTINEL MONITOR` it elsewhere (documented, `docs/security/secrets-and-tls.md:139-142`,
hardening item H-8); without it, a holder of the cluster password may. Live whenever such a
cluster exists; the report would close nothing, only make the result visible. Hence
`hardening`, not `boundary`.

**No roll.** No option touches a pod template or sends a new Sentinel command; the change reaches
the fleet with the operator upgrade as one more status condition, written on the first pass
like every level ([ADR 0005](../adr/0005-upgrade-neutral-defaults-and-anti-affinity.md) D1
untouched).

## Options

Two decisions. The text correction of `SentinelPeersStale` (Work list item 1) and the removal of
the unread `SentinelMonitoring` (item 2) need none.

### D1 - Which Sentinel states are reported, and on which condition?

**Mechanism.** Today the health pass already receives, per Sentinel, either an error or a
`SENTINEL MASTER` reply with `flags`, `num-slaves` and `num-other-sentinels`; it keeps the peer
count and the flag, discards the replica count, and cannot tell an error reply from a dropped
connection (Fact). `SentinelPeersStale` reports a surplus of peers, which Sentinel never
forgets, so it needs no debounce and its remedy is a `SENTINEL RESET` while the master is
healthy. The choice decides which deficits become visible on the CR, and so, through
`vko_valkey_status_condition`, alertable. It does not change any Sentinel, send a new command,
repair anything (ADR 0022 D6 stands: repair is a human's or the next Sentinel roll's, and T62's
work on `resetSentinelState`), or change `Ready` or `phase`. It does not decide the debounce
(D2).

- **A - widen `SentinelPeersStale` to deficits.** New reasons on the existing type for a missing
  monitor, missing peers and missing replicas. Cost S (no new type, no new registry row; the
  typed reply error and the per-Sentinel fields are needed either way). Consequences: one
  condition carries two remedies that contradict each other - `SENTINEL RESET` is the documented
  remedy for a surplus (message at `valkey_controller.go:2405-2407`,
  `docs/operations/status.md` `SentinelPeersStale`) and a measured no-op for a Sentinel with no
  monitor (T62 scenario E: RESET answers `0`); the name says "stale" for a table that is empty;
  the surplus half must stay undebounced and the deficit half must be debounced, so the
  evaluator splits by reason anyway; and a released condition changes meaning under anyone who
  already alerts on `SentinelPeersStale=True` (v1.12.0 on).
- **B - a new level condition next to it (recommended).** Proposed name
  `SentinelMonitorDegraded` (the owner may rename it), `True` when at least one Sentinel that
  answered (1) replied `ERR No such master with that name` (reason `SentinelMonitorMissing`), (2)
  knows fewer than `replicas - 1` other Sentinels (`SentinelPeersMissing`), or (3) knows fewer
  than `spec.replicas - 1` replicas on a pass where the health pass saw `AllSynced`
  (`SentinelReplicasMissing`); one reason per pass in that order, the message naming every pod
  and what it lacks; `False` with reason `SentinelMonitorsComplete` otherwise. A pass in which no
  Sentinel answered at all writes nothing (the ADR 0022 D5 rule, and ADR 0027 D1's third
  hazard). Clause (3) is gated on `AllSynced` because a master that lists fewer replicas makes
  every Sentinel know fewer, legitimately (Fact). Needs: a typed reply error in `valkeyclient`
  (the RESP `-` text kept in `Error()`, so every caller's log is unchanged) so the one reply
  `No such master with that name` (9.1.1 `sentinel.c:3740`, 8.1.9 `:3689`) is a measurement and
  a transport failure is not - any other reply error, such as an auth refusal from `authenticate`
  ([`client.go:410`](../../internal/valkeyclient/client.go#L410)), stays "did not answer";
  `NumSlaves` and the monitor-missing set carried in `ClusterState`; one evaluator in `updateHAStatus` next to `recordSentinelPeerDrift`; a
  `conditionRegistry` row (level, one evaluator, clear site the evaluator, not
  presence-guarded, like `SentinelPeersStale`); a 5 min recheck while True, as ADR 0022 D7; one
  alert `ValkeySentinelMonitorDegraded` in the shipped PrometheusRule (default off; the
  condition-series expression of `ValkeyReconcileBlocked`,
  [`prometheusrule.yaml:51-59`](../../deploy/helm/valkey-operator/templates/prometheusrule.yaml#L51-L59),
  which is `critical`; `for: 15m` and severity warning as `ValkeyReplicasMissing`, `:87-96`). Cost M. Consequences: every
  Sentinel CR gains the condition as `False` on the first pass after the upgrade (a status
  write, no roll); one more condition in the README table. What it does not cover: a cluster
  that never reaches the all-Ready branch or whose master is not found (ADR 0022 Residual risks
  `:199-202`, unchanged); a tier pointed at a wrong address when `spec.replicas` is 1, where the
  expected replica count is 0; a Sentinel that names an address other than the master the
  health pass found (no address comparison, Not verified above).

**B is marked.** The three clauses are exactly the measured states in which a Sentinel cannot
vote or the tier cannot fail over (T62 E; A and K where measured; C), each read off the reply
the health pass already receives, so the ADR 0022 D4 principle - no extra connection per pass -
holds. A separate type keeps each condition with one remedy. B beats A, the runner-up, because
A saves only an API constant and a registry row, while it writes a remedy into the message that
fixes none of the three deficit states - a measured no-op for the headline case E (RESET answers
`0`), a Sentinel pointed at a replica keeps that address through a RESET (ADR 0022 Context `:71-72`
measured that RESET keeps the current address; toward a replica it was not measured), and a RESET toward an unreachable master is the amnesia of A and K - and
changes the meaning of a released condition. A's one real advantage, that an existing user
alert on `SentinelPeersStale=True` would fire on the deficits with no change on the user's
side, does not survive the same sentence: that alert was written for a surplus with a RESET
remedy.

### D2 - How does the level stay silent through a legitimate rebuild?

**Mechanism.** Every legitimate REMOVE + MONITOR and every replaced Sentinel shows a deficit for
about 1 to 2.3 s (peers) and at most 0.11 s (replicas, with every replica connected and auth
already set), a late `auth-pass` about 0.6 s more, and a `SENTINEL RESET` up to 10 s of 0
replicas, measured on both images (M1-M3, M5-M7). The one pass guaranteed to look is the
completing pass of a data-tier-only Sentinel data roll, milliseconds after the `:1049` reset,
and it schedules no follow-up (Fact, case (a)); a manual RESET and a data scale-up can be read
by any pass. A debounce therefore needs a clock and a pass that comes back after it. The choice decides what the CR shows in the
window and whether the clock survives an operator restart. It does not change D1's criteria,
the Sentinel roll, or any wait.

- **A - withhold until the deficit outlived a bound; clock in memory, return by recheck
  (recommended).** The first pass that sees a deficit stores a first-seen time in a per-CR
  tracker keyed by namespace and name (the `nudgeTracker` shape,
  [ADR 0019](../adr/0019-reconcile-concurrency-and-the-cost-of-a-stuck-pass.md) - no fleet-wide
  state), calls `requestRecheck(bound)`, and leaves the condition as it is; a later pass that
  still sees a deficit past the bound writes `True`; a pass that sees none forgets the time and
  writes `False`. Bound 90 s, the operator's own bound for "Sentinel has learned the replicas"
  (`sentinelAwarenessTimeout`, `rolling_update.go:128-133`) and the 90 s family of ADR 0025 D3:
  about nine times the longest measured legitimate window (M7, a RESET's 0 replicas for 9.4 to
  9.9 s, bounded by upstream's 10 s INFO period) and about forty times the longest REMOVE +
  MONITOR window, leaving margin for the unmeasured TLS and CoreDNS timings; 30 s would also
  clear every measured window, with three times the RESET window as margin, and was not
  preferred, because detection speed buys nothing for states that last until someone acts.
  Cost: the tracker entry, one recheck, a test clock. Consequences: neither a routine roll nor a
  manual RESET writes `True`; an operator restart restarts the clock (the report comes one bound later,
  never early); a deficit that heals between two passes and recurs restarts it. The withholding
  is declared in the registry row's prose, so the next reader does not mistake it for an
  evaluator that did not measure.
- **B - the ADR 0025 shape.** `True` at first sight with a transitional reason, a persisted reason
  once `LastTransitionTime` is older than the bound, a recheck at the bound, the alert keyed on
  the persisted reason, `LastTransitionTime` declared load-bearing as for `MultipleMasters`.
  Cost about the same, no tracker. Its simplest variant drops the persisted reason: `True` at
  first sight, a recheck while `True`, and the debounce left to the shipped alert's `for:` - the
  `TLSMaterialStale` precedent, `True` for every ordinary rotation roll with a 72 h alert
  (`docs/operations/status.md` `TLSMaterialStale`, `prometheusrule.yaml:124-132`); cost S.
  Consequences, both variants: the clock survives a restart; but the condition goes `True` on
  the completing pass of every data-tier-only roll on a Sentinel cluster and on a pass that
  lands in a manual RESET, and a user alert or a Lens view keyed on `status="True"` fires on
  routine work.

**A is marked.** The states worth reporting are durable - scenario E lasts until a Sentinel pod
is replaced, A and K until the address answers as a master - so a report one bound late, or two
after a restart, loses nothing. B's advantage, a restart-proof clock, matters where the bound
gates an action (ADR 0025's Warning Event, ADR 0010's waits); here it gates only a report, and
B pays for it with a `True` on the one pass that is certain to see the transient, which is the
noise the debounce exists to prevent. The `TLSMaterialStale` precedent does not carry over:
there the `True` describes, for the whole roll, pods that really do run the old material; here
the transient lasts at most about 10 s and the next pass comes a recheck interval later, so the
`True` would go on stating an incapacity that had already ended.

## Decision

Not decided.

## Work list

1. **XS, no decision needed (text):** make `SentinelPeersStale`'s all-clear say what it measured:
   the `False` message at
   [`valkey_controller.go:2379-2380`](../../internal/controller/valkey_controller.go#L2379-L2380)
   becomes "No Sentinel knows more than %d other Sentinels"; the doc comment at
   [`valkey_types.go:117-119`](../../api/v1/valkey_types.go#L117-L119) says it clears once no
   Sentinel knows more than `replicas - 1` others; ADR 0022 D5 (`:110-113`) gains the same
   precision, dated, as no decision change. A unit test in
   [`sentinel_peer_drift_test.go`](../../internal/controller/sentinel_peer_drift_test.go)
   asserts the message on a pass with one Sentinel at 0 peers. After it lands, recompute the
   urgency (frontmatter).
2. **XS, no decision needed (code), in the same change as D1:** delete the unread
   `ClusterState.SentinelMonitoring`, `sentinelObservation.agreeing` and `monitoring()`; move the
   health-package assertions that read `monitoring()` (Fact) onto the per-Sentinel fields D1
   adds, together with the test sites that read or set the field itself
   ([`checker_live_test.go:258`](../../internal/health/checker_live_test.go#L258), `:644`;
   [`checker_paths_test.go:334`](../../internal/health/checker_paths_test.go#L334);
   [`checker_test.go:81`](../../internal/health/checker_test.go#L81), `:488-491`;
   [`valkey_controller_test.go:66`](../../internal/controller/valkey_controller_test.go#L66),
   `:1598`; `git grep -n SentinelMonitoring -- '*.go'` at `84a39c2`). Kept out of item 1
   because those tests need the new fields to assert against.
3. **Waits on D1 and D2:** the typed reply error, the `ClusterState` fields, the evaluator, the
   tracker and recheck, the `ConditionType` and its reasons in `api/v1`, the `conditionRegistry`
   row, the alert, as costed under Options.
4. **Waits on D1 and D2 (documents, same change):** ADR 0022 amended in place (a new D9 for the
   condition and a D10 for the debounce, with Status line and Residual risks: the not-covered
   cases of D1-B, and the restart that restarts the clock); the README condition table row;
   `docs/operations/status.md` gains a section and the `SentinelPeersStale` section loses nothing;
   `docs/operations/monitoring.md` lists the alert; the Sentinel section of `CLAUDE.md` names the
   new condition next to `SentinelPeersStale` (owner's file, one sentence); no ticket citation
   outside `docs/tickets/` (ADR 0034).
5. **Measurement, informs nothing in D1 or D2 but closes the Not verified items:** the new e2e
   of Verification is the first Kubernetes observation of a monitor-less Sentinel (also T62's
   open "Kubernetes behaviour of the measured states", for state E only).
6. Close ([ADR 0034](../adr/0034-tickets-are-work-lists-that-get-archived.md)): the rule is in
   ADR 0022, the operator-facing text in `status.md` and `monitoring.md`; `git grep` `T74` and
   `074-` outside `docs/tickets/`, then move to `archive/`.

## Cross-ticket

- [T62](062-resetsentinelstate-falls-back-to-sentinel-reset.md) produces the states and decides
  how often: its decision-free fallback fix re-adds a lost monitor on the next verified reset,
  its D1-B gate removes the unverified resets, its D2 recommendation resets only Sentinels that
  disagree - one that answers `SENTINEL MASTER` with an error among them. None of that makes a
  state visible between rolls, so neither ticket blocks the other. Under T62's D2
  recommendation a clean roll's completing pass resets no Sentinel, which removes the one
  guaranteed reading of the window (Fact, case (a)) but not the need for D2 here: a manual
  `SENTINEL RESET` (M7), a data scale-up and the reset of a Sentinel that disagreed still show a
  deficit to whatever pass reads them. (A replaced Sentinel, M3, is not read inside its window,
  Fact; this corrects the filing's own sentence.)
- [T36](036-non-persistent-master-restarts-empty.md): the no-failover state is one route into its
  mechanism on a cluster without persistence (T62, Not verified).
- The two sibling findings of T62's re-verification (its Work list items 8a and 8c) are filed
  separately; they change no fact here.

## Verification

- Item 1: `grep -n "as expected" internal/controller/valkey_controller.go` is empty; the new
  unit test fails with the old message restored; `make test-unit`, `make lint`.
- Item 2: `git grep -n "SentinelMonitoring\|monitoring()" -- '*.go'` is empty; `make test-unit`.
- D1-B: unit tests over the existing fake Sentinel plumbing (`NewValkeyClientFn`, the router of
  [`checker_live_test.go`](../../internal/health/checker_live_test.go),
  `fakeValkeyServer(t)`): an error reply `ERR No such master with that name` counts as
  monitor-missing, and neither a refused connection nor another reply error (an auth refusal)
  does; 0 peers, and replicas short with
  `AllSynced`, each raise their reason; replicas short without `AllSynced` do not; no Sentinel
  answering writes nothing over a standing `True`. Each fails with its clause removed (mutation
  and revert check, [ADR 0017](../adr/0017-test-and-ci-policy.md)).
  `TestConditionRegistryCoversEveryConditionType` green with the row, red without it.
- D2-A: a deficit seen once writes no `True` and requests a recheck no longer than the bound; the
  same deficit past the bound writes `True`; no deficit writes `False` and drops the tracker
  entry; the first test fails with the first-seen gate removed.
- E2E, both legs (`single-node-valkey9`, `single-node-valkey8`): a Sentinel cluster rolled by a
  data-tier-only change (for example `spec.resources`; an image change ends the completing pass
  on the Sentinel roll and would not exercise the window, Fact) asserts the condition is never
  `True` at completion and after bound plus recheck (a clean roll stays silent); a new e2e runs `SENTINEL REMOVE <monitor>` on one
  Sentinel pod, expects `True` with reason `SentinelMonitorMissing` naming that pod within bound
  + recheck + slack, deletes that Sentinel pod and expects `False`.
- `make test-unit`, `make test-integration`, `make lint`, `make cyclo`, `make generate-all` with
  no diff.

## History

- 2026-09-27: filed from ticket 062 (Work list item 8b, the Fact bullet "An emptied or missing
  monitor is not reported", the Impact bullet "Invisible" and the Cross-ticket bullet "Adjacent,
  needs its own file (Work list 8b)") during the re-verification at 84a39c2. **Moved:** the
  finding and its reasoning from 062 and from that re-verification's auditor, facts-skeptic and
  design-skeptic records (the unread `SentinelMonitoring`, the V(1) "not responding" skip, the
  `PING` probe, the eight alerts, the design skeptic's point that this is a different mechanism
  owing an ADR 0027 row and that any level needs a debounce); T62's scenarios A, C, E and K,
  summarised with their results and pointing to 062 for the commands. **Re-verified now, by
  reading at `84a39c2`:** `observeSentinels`, `CheckCluster`'s early returns, the all-Ready
  branch, `recordSentinelPeerDrift` and `staleSentinelPods`, the registry row of
  `SentinelPeersStale`, `SentinelMasterInfo` and its parser, the RESP error path of
  `readFullResponse`, `isSentinelAwareOfReplicas`, the probe, the PrometheusRule, the collector's
  condition series, and the completing pass reaching `updateStatus`; all of the moved facts hold
  (062's `checker.go:328-333` for the skip is `:330-334`). **New in this filing:** the `False`
  message of `SentinelPeersStale` and the `valkey_types.go` doc comment state agreement where the
  code only knows the absence of a surplus (hence urgency `now` by rule 1, recomputed after item
  1); the RESP error path makes an error reply indistinguishable from a dropped connection;
  `NumSlaves` is parsed and discarded; the health pass reads the Sentinels on the pass that
  reset them and schedules no follow-up, which is why the debounce needs a recheck and not only a
  clock; every condition is already a metric series. **Measured** (docker, both pinned images,
  M1 to M4, commands and results under Fact): the rebuild window after a reset of all three
  Sentinels and of one, a replaced Sentinel with a pinned id, and a dead replica's entry.
  **Corrected:** the finding's wording "every legitimate reset briefly shows 0 peers and 0
  replicas" holds for peers (about 1 to 2.3 s) but for replicas only until the first INFO, within
  0.11 s, when every replica is connected; `flags` reads `master,disconnected` in the first
  instant. **Options:** D1 (A widen `SentinelPeersStale`, B a new level, B marked) and D2 (A
  in-memory first-seen plus recheck with a 90 s bound, B the ADR 0025 transitional/persisted
  shape, A marked). **Not kept:** reporting through `Ready` or `phase` (ADR 0002 D5: `Ready` is
  the data plane, which serves while Sentinel cannot fail over); a per-Sentinel gauge (ADR 0021
  forbids a gauge written from a reconcile pass); an Event only (Events expire, and ADR 0025 D7
  promises no Warning on a clean roll); a second read inside the same pass after a short sleep
  (holds a reconcile worker, ADR 0019, and a few seconds do not cover the longer windows); a
  fourth clause comparing the Sentinel's address with the found master (FQDN equality not
  measured in Kubernetes, and the count clause already catches scenario C). **Not verified:**
  the Kubernetes timings, the flags of a Sentinel monitoring a replica, the address comparison,
  the frequency on the fleet. No make target, `go test`, Kind cluster or kubectl was run; every
  `vko-file-074*` container and network was removed.
  **Adversarial review, same day, at `84a39c2`:** every load-bearing line re-read (checker,
  `recordSentinelPeerDrift`, `readFullResponse`, the registry row, the probe, the PrometheusRule,
  the completion path through `finishDataRoll`, `handlePostRollingUpdateChecks` and the Sentinel
  roll) and the run records of 062's re-verification searched for this finding. **Found wrong
  and corrected in place:** the claim that the completing pass of every Sentinel data roll reads
  the Sentinels - true only for a data-tier-only roll; on an image change the same pass deletes
  the first Sentinel pod and ends before the status write (Fact, cases (a) and (b), by reading);
  the claim that a replaced Sentinel (M3) still shows the window to the health pass - its
  readiness starts after 5 s, past the measured window; the filing's correction of the finding
  ("0 replicas only until the first INFO, within 0.11 s") holds for REMOVE + MONITOR with auth
  already set, not for `SENTINEL RESET` (up to 10 s, M7, which also explains archive 039's 3 to 9 s
  on Kind that the filing had not carried) nor for a late `auth-pass` (M6); the 90 s bound's
  "forty times the longest measured window" (about nine times, M7); "severity warning, the
  pattern of `ValkeyReconcileBlocked`" (that alert is critical); D1-B's typed error made every
  reply error a measurement (now only `No such master with that name`); Work list item 2 missed
  the test sites that read or set `SentinelMonitoring` itself. **Measured** (docker, both
  pinned images, M5 to M7, containers `vko-file-074x-*` and their networks removed, checked with
  `docker ps -a` and `docker network ls`): the loop's command order with auth, a late `auth-pass`,
  and a RESET; upstream `sentinel.c` read at both tags for the mechanism. **Options:** D1-B's
  justification gains that the surplus remedy fixes none of the deficit states, and A's argument
  (existing alerts fire unchanged) was weighed and does not hold; D2-B gains its simplest variant
  (debounce in the alert's `for:`, the `TLSMaterialStale` precedent), weighed and not marked;
  both marks stand. **Not kept:** suppressing the evaluation only on a pass that ran
  `resetSentinelState` itself (no clock) - it misses a manual RESET, whose 10 s window is the
  longest measured, and a scale-up. **Urgency** kept at `now`, with the strict reading of rule 1
  (T71, T75, T78: `next`) recorded in the frontmatter. No make target, `go test`, Kind cluster or
  kubectl was run.
