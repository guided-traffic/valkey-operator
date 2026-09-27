---
id: T12
title: No write fencing — a master with zero replicas keeps accepting writes; `min-replicas-to-write` as an opt-in field
state: analysed       # "open - analysed 2026-08-24, not started"; re-verified 2026-09-27 at 84a39c2, still no option chosen
severity: medium      # kept 2026-09-27: the severity-high finding of the re-verification (the roll's own Sentinel failover loses acknowledged writes) is another mechanism, filed as T67 with severity high there; this rating never rested on it
security: none
urgency: next         # was icebox; rule 3 matches before rule 5 (History 2026-09-27); re-derived at 84a39c2, unchanged
effort: S             # was L; the recommended path (Decision 1 option 2 plus the XS fixture) is S; L applies only if option 1 is chosen (History 2026-09-27)
blocked-by: product
filed-from: T4 analysis, 2026-08-24
opened: 2026-08-24
decided:
done:
---

# T12 - No write fencing — a master with zero replicas keeps accepting writes; `min-replicas-to-write` as an opt-in field

**Severity: medium (data durability, opt-in feature request). Status: open — analysed
2026-08-24, not started. Found while analysing T4. Severity argument strengthened
2026-08-26: [ADR 0028](../adr/0028-a-demotion-may-not-discard-the-only-dataset.md) D3
deliberately *lengthens* the two-master window — a refused demotion keeps both masters
until the state bound expires — and names the missing fencing as the price it pays.**

**Re-verified 2026-09-27 at `84a39c2`.** The premise holds: no directive, no field and no code
exist. Measurements in docker on both pinned Valkey lines overturned several load-bearing claims
of the analysis below, each corrected in place. The re-verification also found a larger loss of
acknowledged writes that is **not this ticket's mechanism**: the operator's own Sentinel failover
during every Sentinel roll. It is filed as
[T67](067-the-rolls-own-sentinel-failover-loses-acknowledged-writes.md) (severity high), which
holds its measurements and options and is decided before Decision 1 here (Options).

## What is missing

`grep -rn "min-replicas" internal/ api/` returns nothing. `generateValkeyConf`
([`internal/builder/configmap.go:62-138`](../../internal/builder/configmap.go)) has no
hook for it and there is no `spec.config` / `extraConfig` escape hatch on the CRD, so a
user cannot set it either. **Every master this operator builds accepts writes with zero
connected replicas.**

That is what makes the T4/T11 split-brain window expensive: both masters accumulate
writes, and the repair (`REPLICAOF` on the loser) discards one side's. ~~With
`min-replicas-to-write 1` the diverging side would collect nothing to lose in the first
place.~~ *(corrected 2026-09-27 at `84a39c2`: with `min-replicas-to-write 1` the side of a split
that holds no good replica stops taking writes. A replica counts as good while it is `ONLINE` and
has ACKed within `min-replicas-max-lag` seconds
([replication.c 9.1.1:4923-4946](https://github.com/valkey-io/valkey/blob/9.1.1/src/replication.c#L4923-L4946)),
and the count is refreshed when a replica disconnects, goes online or is accepted for a partial
resync, and once a second
([:952](https://github.com/valkey-io/valkey/blob/9.1.1/src/replication.c#L952),
[:1334](https://github.com/valkey-io/valkey/blob/9.1.1/src/replication.c#L1334),
[:1597](https://github.com/valkey-io/valkey/blob/9.1.1/src/replication.c#L1597),
[:5439](https://github.com/valkey-io/valkey/blob/9.1.1/src/replication.c#L5439)). A side whose
replica link drops collects nothing more; a side whose link stalls without a disconnect collects
up to `max-lag` seconds plus one cron tick of writes. Read in source, not measured. The fence does
nothing for the side that keeps a replica, and it does not stop a replica from full-syncing an
empty restarted master: `REPLICAOF` and `SYNC` are not gated, which is T36.)*

## Why this is not a one-line opt-in

Measured 2026-08-24 against the repo's own pinned image, `valkey/valkey:9.1.1`
([`test/testimages/images.go:40`](../../test/testimages/images.go)), via
`docker run valkey-server --min-replicas-to-write 1 --min-replicas-max-lag 10`
with zero replicas attached:

| Command | Result under the gate |
|---|---|
| `SET` / `MSET` / `DEL` / `EXPIRE` | `NOREPLICAS Not enough good replicas to write.` |
| `WAIT`, `REPLICAOF`, `PUBLISH`, `CONFIG SET`, `CLIENT KILL` | **not gated** |
| `DBSIZE`, `PING`, `GET`, `INFO` | fine |

*(Re-measured 2026-09-27 at `84a39c2` on `valkey/valkey:9.1.1` and `8.1.9`, every row: `SET`,
`MSET`, `DEL`, `EXPIRE`, `INCR` and `FLUSHALL` answer `NOREPLICAS Not enough good replicas to
write.`; `WAIT 1 100` answers `0`; `REPLICAOF NO ONE` answers `OK`; `PUBLISH` `0`; `CONFIG SET`
`OK`; `CLIENT KILL ID` / `TYPE` `0`; `DBSIZE`, `PING`, `GET`, `TTL`, `SELECT` and `INFO` are
unaffected. The gate applies to commands flagged as write commands only
([server.c 9.1.1:4541-4542](https://github.com/valkey-io/valkey/blob/9.1.1/src/server.c#L4541-L4542)).
That `PUBLISH` passes is load-bearing for any fence: Sentinel publishes its hello messages on the
monitored master, so a fenced master keeps Sentinel discovery working.)*

So the gate hits client writes and exactly one operator write — and misses every gate
the operator currently relies on. Six consequences, each verified:

**1. A master with zero replicas is a designed, load-bearing state here, not an edge
case.** The init script only accepts a peer as master when `connected_slaves > 0`
([`statefulset.go:457-488`](../../internal/builder/statefulset.go), the test at `:471`), and
~~`statefulset.go:490-519` exists purely to cover "the promoted pod is the only master and has
no replicas attached yet". The rolling update says the same at `rolling_update.go:4036-4040`.
Durations: the manual-failover window is bounded only by `GetSyncTimeout` (**default 5 min**,
`valkey_types.go:1448-1453`), and the Sentinel path tolerates a zero-replica master through
`handleMasterWithNoReplicas` (`rolling_update.go:3142`) for `replicaReconnectTimeout` = 90 s ×
`maxReconnectResets` = 2 (`:149`, `:138`), i.e. **~3-5 min**, with `SentinelParallelSyncs = 1`
(`sentinel.go:53`) lengthening it. Under the gate, every client write in those windows is
refused.~~ *(corrected 2026-09-27 at `84a39c2`:
[`statefulset.go:490-519`](../../internal/builder/statefulset.go) covers the case its comment
([`:490-496`](../../internal/builder/statefulset.go)) names, "the promoted pod ... has no replicas
attached yet", and [`rolling_update.go:4039-4043`](../../internal/controller/rolling_update.go)
says the same, but both hold only for `replicas: 2`. On `replicas >= 3`, `promoteAndRedirect`
([`rolling_update.go:4188-4248`](../../internal/controller/rolling_update.go)) demotes the
outgoing master and redirects every other replica to the promoted pod before the delete
(the loop at `:4234-4245` skips only the old master and the promoted pod), so the promoted pod
has connected replicas: in docker, idle, two were connected and the first fenced `SET` was
accepted at +0.23 s (9.1.1) and +0.25 s (8.1.9) after a partial resync. On `replicas: 2` the
demoted old master stays attached until its delete, and the promoted pod has zero replicas only
from that delete until the replacement joins. The 5 min of `GetSyncTimeout`
([`valkey_types.go:1448-1455`](../../api/v1/valkey_types.go)) is the stall bound of the
manual-failover state (`isManualFailoverStalled`,
[`rolling_update.go:1320-1322`](../../internal/controller/rolling_update.go)), not a zero-replica
duration. What a fenced manual failover on `replicas >= 3` costs is the resync of the redirected
pods: 0.23-0.25 s idle, and 5.1-5.7 s under a client write load, because the redirected pods then
full-resync behind `repl-diskless-sync-delay 5`
([`configmap.go:179`](../../internal/builder/configmap.go)), plus the dataset transfer
(Measurements). The Sentinel path does tolerate a zero-replica master through
`handleMasterWithNoReplicas`
([`rolling_update.go:3145`](../../internal/controller/rolling_update.go)):
`replicaReconnectTimeout` = 90 s and `maxReconnectResets` = 2
([`:149`, `:138`](../../internal/controller/rolling_update.go)) re-arm the timestamp twice
(`incrementReconnectResetCount`, `:3244-3257`), so about 270 s pass before the proceed branch
(`:3163`). `SentinelParallelSyncs = 1` ([`sentinel.go:53`](../../internal/builder/sentinel.go))
does not lengthen it for `minReplicas: 1`: Sentinel reconfigures the first non-promoted replica at
once and holds back only the second and later ones
([sentinel.c 9.1.1:5283](https://github.com/valkey-io/valkey/blob/9.1.1/src/sentinel.c#L5283);
docker, both pins: `+slave-reconf-sent` 46-82 ms after `+promoted-slave`). Under the gate every
client write is refused while the master holds no good replica.)*

**2. Standalone would be a permanent total write outage.** `replicationConfig` is the
only topology-conditional block
([`configmap.go:109-111`](../../internal/builder/configmap.go)); everything else is
emitted for every mode. A `replicas: 1` cluster has zero replicas forever. And the
operator would not notice: `verifyValkeyConnectivity` is PING-only
(~~[`valkey_controller.go:2698-2707`](../../internal/controller/valkey_controller.go)~~ *(corrected 2026-09-27: [`valkey_controller.go:2970-2981`](../../internal/controller/valkey_controller.go))*),
both probes are PING
(~~[`statefulset.go:750-772`](../../internal/builder/statefulset.go)~~ *(corrected 2026-09-27: [`statefulset.go:847-870`](../../internal/builder/statefulset.go), readiness `:847-858`, liveness `:859-870`)*), and `CheckCluster`
reasons from `ConnectedSlaves` / `MasterSyncInProgress`
([`health/checker.go:90-141`](../../internal/health/checker.go)). Phase would read `OK`
on a cluster that accepts nothing. *(Added 2026-09-27 at `84a39c2`: a `replicas: 1` CR with
`sentinel.enabled: true` is also accepted by the API and does receive `replicationConfig`, see
Placement under Design constraints. Its `MasterSyncInProgress` term reads a master's `INFO`, where
Valkey never emits that field; filed as
[T69](069-three-sync-checks-read-a-replica-field-from-the-master.md).)*

**3. `replicas: 2` in steady state with one pod down is the same silent outage.** No
rolling update needed. `readyReplicas=1`, PING succeeds, the `instanceRole=master` label
is untouched, so the `-rw` Service keeps routing to a master that refuses every write —
and both Kubernetes and the CR call it healthy.

**4. The operator writes to Valkey in exactly one place, and it would go red.**
`writeHealthKey` (~~[`internal/observer/checks.go:108-115`](../../internal/observer/checks.go)~~ *(corrected 2026-09-27: [`internal/observer/checks.go:109-116`](../../internal/observer/checks.go))*)
issues `SELECT <db>` + `SET vko:health … EX 10` against the master. `ExecMulti`
propagates the `-NOREPLICAS` reply as an error
([`valkeyclient/client.go:479-481`](../../internal/valkeyclient/client.go)), `write_test`
fails, `read_test` is force-failed
(~~[`observer/observer.go:290-296`](../../internal/observer/observer.go)~~ *(corrected 2026-09-27: [`observer/observer.go:292-300`](../../internal/observer/observer.go))*),
`replica_read_test` is skipped, `writeTestFailure` defaults to true
(~~[`valkey_types.go:1075-1078`](../../api/v1/valkey_types.go)~~ *(corrected 2026-09-27: the field at [`valkey_types.go:908-911`](../../api/v1/valkey_types.go), the default in `UnreadyWhenDefault` at [`:1457-1463`](../../api/v1/valkey_types.go))*) → `/readyz` 503 → the
observer pod flips NotReady (probe `PeriodSeconds: 2, FailureThreshold: 1`,
[`builder/observer.go:95-105`](../../internal/builder/observer.go)) → `status.observerReady=false`.
The observer is itself opt-in and off by default
(~~[`IsObserverEnabled`](../../api/v1/valkey_types.go#L1007)~~ *(corrected 2026-09-27: [`IsObserverEnabled`](../../api/v1/valkey_types.go#L1363))*), ~~but the two features attract
the same user, so the combination has to be handled, not hoped away.~~ *(corrected 2026-09-27 at
`84a39c2`: the combination needs no interlock. The observer's `replica_sync` check
([`observer/observer.go:278-284`](../../internal/observer/observer.go) →
[`observer/checks.go:92-107`](../../internal/observer/checks.go)) already fails whenever
`connected_slaves < replicas - 1`, for every cluster with more than one replica, and its
`replicaSyncFailure` flag defaults to true
([`valkey_types.go:918-921`](../../api/v1/valkey_types.go),
[`builder/observer.go:203`](../../internal/builder/observer.go),
[`cmd/observer/observer.go:84`](../../cmd/observer/observer.go)), so every zero-replica window
already makes the observer unready without fencing. Under fencing `write_test` fails only when
the master really refuses writes, for example with a connected but lagging replica, and that is
a true report.)*

**5. `min-replicas-max-lag` is invisible to this operator.**
`min_slaves_good_slaves` **is** in `INFO replication` *(corrected 2026-09-27: only while `min-replicas-to-write` is set — absent without the directive on 9.1.1 and 8.1.9, measured in docker)*, but `parseReplicationInfo`
([`valkeyclient/client.go:566-596`](../../internal/valkeyclient/client.go)) does not parse
it and `ReplicationInfo` has no field for it. A lagging replica reports
`connected_slaves:1`, `master_link_status:up`, `master_sync_in_progress:0` — every gate
in the operator green (`waitForReplicasReady`, `verifyReplacedReplicasSynced`,
`replicationNotEstablishedReason`, `CheckCluster`, the sidecar `isSyncedReplica`) —
while the master refuses writes. ~~On a 3-replica cluster that happens routinely: replacing
one replica forks the master and the survivor lags.~~ *(corrected 2026-09-27 at `84a39c2`: a
replica's full sync does not make the surviving replica lag. Lag is the time since the replica's
last `REPLCONF ACK`
([replication.c 9.1.1:4933-4935](https://github.com/valkey-io/valkey/blob/9.1.1/src/replication.c#L4933-L4935)),
and the master keeps processing ACKs during the fork and the diskless transfer. Measured once, on
9.1.1 only, with a 522 MB dataset while a fresh replica full-synced: the surviving replica stayed
`online` at lag 0-1 s, `min_slaves_good_slaves` never fell below 1, and 0 of 11 `SET`s were
refused. A lag past `max-lag` needs a stalled replica or link, not a sibling's resync. The
per-replica `slaveN:...,state=online,...,lag=0` fields are in `INFO replication` with or without
the directive (measured on both pins), and `parseReplicationInfo` does not read them either.)*
**Nothing the operator logs or exposes would name the cause.**

**6. Enabling it is itself a rolling update that walks through its own worst window.**
The directive would enter both config bodies, both of which feed `ComputeConfigHash`
([`configmap.go:293-305`](../../internal/builder/configmap.go)) — only
`AnnotationKnownMaster` is excluded. So the CR edit triggers a full failover-aware
rolling update, ~~and the failover step lands on a fresh master with zero replicas that
now carries the new setting. The enablement produces the longest outage of the feature's
lifetime.~~ *(corrected 2026-09-27 at `84a39c2`: and its failover step opens the same window as
every later roll, not a longer one. The promoted pod carries the new setting; on `replicas >= 3`
it has redirected replicas (consequence 1), but when they cannot partial-resync it refuses writes
until one has full-synced. Measured: 5.7-6.9 s in nine fenced forced Sentinel failovers and
5.1-5.7 s in the `promoteAndRedirect` shape under load, on both pins, because the old master's
stream (client writes, Sentinel hellos) continues after the promotion ("Requested offset for
second ID was 1369, but I can reply up to 1227") and `repl-diskless-sync-delay 5`
([`configmap.go:179`](../../internal/builder/configmap.go)) delays the full sync. It is not
unconditional: one 8.1.9 run started without the 12 s settle period resynced partially and took
writes at +0.45 s (Measurements). The enabling
roll protects, if anything, less than a later one, because its outgoing master still runs the
unfenced template. With `SENTINEL FAILOVER <name> COORDINATED` on Valkey 9 every replica resynced
partially and a fenced new master took writes at +0.62 s (Measurements), which is
[T67](067-the-rolls-own-sentinel-failover-loses-acknowledged-writes.md)'s recommended option.)*

**Bonus finding: an existing test points at the wrong tripwire.**
`TestHandleMasterFailover_DoesNotFailOverWhenWriteSyncFails`
(~~[`sentinel_failover_test.go:574-597`](../../internal/controller/sentinel_failover_test.go)~~ *(corrected 2026-09-27: [`sentinel_failover_test.go:574-599`](../../internal/controller/sentinel_failover_test.go), the mock at `:579-581`)*)
mocks a `NOREPLICAS` reply to `WAIT`. Measured: **`WAIT` is not gated** — it returns `0`
with no error. And `waitForWriteSync` returns nil early when `numReplicas == 0`
(~~[`rolling_update.go:1774-1777`](../../internal/controller/rolling_update.go)~~ *(corrected 2026-09-27: [`rolling_update.go:2933-2936`](../../internal/controller/rolling_update.go))*), so it is
a no-op in exactly the window the gate bites. The test is still a valid test of the
operator's own refusal path; it is just not evidence about this feature. *(Added 2026-09-27 at
`84a39c2`: an error `WAIT` really returns, measured on a replica on both pins, is `ERR WAIT cannot
be used with replica instances. Please also note that if a replica is configured to be writable
(which is not the default) writes to replicas are just local and are not propagated.`)*

## E2E impact

`valkeyExec` (~~[`test/e2e/e2e_test.go:230-265`](../../test/e2e/e2e_test.go)~~ *(corrected 2026-09-27: [`test/e2e/e2e_test.go:225-268`](../../test/e2e/e2e_test.go))*) retries only
on a non-zero exit. Measured: `valkey-cli --raw SET` under the gate prints the error to
**stdout and exits 0**, so `valkeyMSET`'s `require.Equal(t, "OK", resp)`
(~~[`e2e_test.go:268-281`](../../test/e2e/e2e_test.go)~~ *(corrected 2026-09-27: [`e2e_test.go:270-282`](../../test/e2e/e2e_test.go))*) **aborts** the test with a string
comparison. ~~~15 call sites would be affected, all of them writing right after a
failover or a roll.~~ *(corrected 2026-09-27 at `84a39c2`: a scripted count of calls whose
arguments carry a write-verb literal or forward `args...` finds 61 write call sites across 16
files, through six helpers: `valkeyExec` 23, `valkeyTLSExec` 21, `valkeyMSET` 12,
`valkeyExecAllowError` 2, `valkeyTLSExecAllowError` 2, `valkeyExecQuick` 1. The count was scripted
by one reviewer and not re-run by a second; [T68](068-the-e2e-exec-helpers-do-not-check-the-valkey-reply.md)
recounted 60 with a different script, and its 15 discarding sites agree. 15 of them discard the reply, and those are the silent
ones: [`tls_test.go:496-499`](../../test/e2e/tls_test.go), `:830`, `:836`, `:842`, `:1284`,
`:1288`, `:1295`, `:1298`, [`tls_rotation_test.go:131`](../../test/e2e/tls_rotation_test.go),
[`sidecar_test.go:481`](../../test/e2e/sidecar_test.go),
[`rolling_update_test.go:291`](../../test/e2e/rolling_update_test.go) and
[`fleet_upgrade_test.go:768`](../../test/e2e/fleet_upgrade_test.go), each spot-checked by a second
reviewer. The rest assert the reply, so an error reply fails there with its text visible.)*
Two need naming because they would fail *misleadingly*:
[`sentinel_stale_master_test.go:204-206`](../../test/e2e/sentinel_stale_master_test.go)
asserts a write to the Sentinel-reported master is not a READONLY error — it would now
fail with NOREPLICAS, a message pointing at the wrong diagnosis; ~~and
[`standalone_test.go:396`](../../test/e2e/standalone_test.go) would still pass, for the
wrong reason.~~ *(corrected 2026-09-27 at `84a39c2`: the subtest at
[`standalone_test.go:395-398`](../../test/e2e/standalone_test.go) asserts nothing, it only logs
the reply; and a replica answers `READONLY` before the gate applies, because
`checkGoodReplicasStatus` returns true whenever `primary_host` is set
([replication.c 9.1.1:4942](https://github.com/valkey-io/valkey/blob/9.1.1/src/replication.c#L4942)),
so it would see `READONLY`, the right reason.)*

~~Any implementation therefore has to make `valkeyExec` treat a `-`-prefixed stdout reply
as an error, independently of the feature.~~ *(corrected 2026-09-27 at `84a39c2`: a `-` prefix
identifies nothing. `--raw` prints error replies without the `-` (stdout reads `NOREPLICAS Not
enough good replicas to write.`), and integer replies such as `TTL` print `-1` or `-2`, so the rule
would miss every error and misfire on those. Measured on both pins, `valkey-cli -e` makes an
error reply exit 1 with the text on stderr, while `OK`, `TTL -1`, `PUBLISH` and an empty `GET`
still exit 0. Adding `-e` is not a one-line change: `valkeyExec` treats any non-zero exit as a
transient kubectl failure, makes five attempts with 4, 6, 8 and 10 s of sleep between them (28 s
plus the five exec runs) and then fails with `kubectl exec failed`
([`e2e_test.go:230-268`](../../test/e2e/e2e_test.go); `valkeyTLSExec` makes three attempts 2 s
apart, [`tls_test.go:48-88`](../../test/e2e/tls_test.go)), so the helper has to
classify a `valkey-cli` error reply, for example by its stderr, before the retry loop; and at
least ten call sites run `valkeyExec` / `valkeyTLSExec` inside `require.Eventually` polls
(`standalone_test.go:333`, `:343`, `:371`, `:407`, `sidecar_test.go:264`, `tls_test.go:1219`,
`:1302`, `:1317`, `:1352`, `:1373`) that today tolerate a transient error reply such as `LOADING`
and would need the `AllowError` variant. The item is independent of fencing — a `READONLY`,
`LOADING` or `OOM` reply fails the same way today — and is filed as
[T68](068-the-e2e-exec-helpers-do-not-check-the-valkey-reply.md), which re-measured it and
recommends plain `-e` in the two strict helpers.)*

## Design constraints this leaves

- **ADR 0005 D1**: new CRD features default to off; an operator upgrade changes nothing.
  So: absent block and `enabled: false` both render no directive.
- **ADR 0015**: schema validation only — no webhook, ~~no CEL anywhere in the repo
  (verified: `x-kubernetes-validations` appears in zero generated CRDs). A cross-field
  rule like "only with `replicas >= 3`" **cannot** be enforced at admission. It has to be
  a runtime refusal with a condition and an Event, in the shape ADR 0023 and ADR 0002
  already use for a spec the operator accepts and declines to apply. (corrected 2026-09-27:
  false since ADR 0033, which amended ADR 0015 on 2026-09-26: `SeccompProfileSpec` carries two
  `XValidation` rules and the generated CRD carries `x-kubernetes-validations`. "`replicas >= 3`
  when fencing is on" can be a CEL rule on `ValkeySpec`, refused at admission; a runtime refusal
  with a condition stays only as the defence for an operator running against an older CRD.)~~
  *(corrected 2026-09-27 at `84a39c2`: CEL exists since
  [ADR 0033](../adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md),
  which amended ADR 0015 on 2026-09-26: `SeccompProfileSpec` carries two `XValidation` rules
  ([`valkey_types.go:541-542`](../../api/v1/valkey_types.go)) and the generated CRD carries
  `x-kubernetes-validations`
  ([`config/crd/bases/vko.gtrfc.com_valkeys.yaml:553`](../../config/crd/bases/vko.gtrfc.com_valkeys.yaml),
  [`deploy/helm/valkey-operator/templates/crd.yaml:555`](../../deploy/helm/valkey-operator/templates/crd.yaml)).
  A fencing field needs two rules, not "`replicas >= 3`": `minReplicas <= replicas - 2` while
  enabled, so one replica can be replaced without an outage (for `minReplicas: 1` that is
  `replicas >= 3`, and it also covers `sentinel.enabled` with `replicas: 1`, which placement does
  not exclude), and `maxLagSeconds >= 1`, because `min-replicas-max-lag 0` switches the gate off
  silently ([replication.c 9.1.1:4943](https://github.com/valkey-io/valkey/blob/9.1.1/src/replication.c#L4943);
  the Valkey default is 10, config.c 9.1.1:3430). A runtime fallback is not needed against CRD
  skew: the CRD ships as the chart template
  [`crd.yaml`](../../deploy/helm/valkey-operator/templates/crd.yaml) and is upgraded with the
  operator, and an older CRD prunes the unknown field before the operator reads it. But an
  admission rule on `ValkeySpec` also refuses every later scale-down below `minReplicas + 2`, for
  example a GitOps change of `replicas` from 3 to 2, and the repository has a stated precedent
  against refusing that: `podDisruptionBudget.maxUnavailable` is warned about and honoured
  "rather than rejecting a later scale-down"
  ([`valkey_types.go:775-779`](../../api/v1/valkey_types.go)). Admission refusal against a runtime
  refusal with a condition is therefore a design choice inside option 1, not a settled
  constraint.)*
- **CRD shape**: ~~7 of 10~~ *(corrected 2026-09-27: 7 of 11)* optional blocks are `+optional` pointer-to-struct with
  `Enabled bool` + `+kubebuilder:default=false`, read through
  `v.Spec.X != nil && v.Spec.X.Enabled` (~~[`valkey_types.go:675-740`](../../api/v1/valkey_types.go)~~ *(corrected 2026-09-27: `ValkeySpec` at [`valkey_types.go:1026`](../../api/v1/valkey_types.go); `auth`, `rollingUpdate`, `antiAffinity` and `podSecurity` carry no `Enabled`)*).
  `antiAffinity` is the exception, an enum defaulted to `off` (ADR 0005 D2/D3).
- **Placement**: the shared tail of `replicationConfig`
  ([`configmap.go:174-181`](../../internal/builder/configmap.go)) ~~is the only placement
  that reaches replicas, because on both HA paths the init container copies the **master**
  ConfigMap onto a replica and merely appends `replicaof` (`statefulset.go:335-339` and
  `:523-527`). It is also already gated on `IsSentinelEnabled() || IsMultiReplicaWithoutSentinel()`,
  which excludes standalone for free — hazard 2 disappears by placement alone.~~ *(corrected
  2026-09-27 at `84a39c2`: is the right placement, but not because only it reaches replicas: the
  init container copies the master body onto a replica on both HA paths
  ([`statefulset.go:333-349`](../../internal/builder/statefulset.go),
  [`:522-537`](../../internal/builder/statefulset.go)), so any line of the master body reaches
  them. The tail is right because it is in both bodies — the replica body is still copied by the
  ordinal fallback (`statefulset.go:348`, `:536`) — and because it is gated on
  `IsSentinelEnabled() || IsMultiReplicaWithoutSentinel()`
  ([`configmap.go:108-111`](../../internal/builder/configmap.go)). That gate excludes only
  `replicas: 1` without Sentinel. `IsSentinelEnabled` does not look at `replicas`
  ([`valkey_types.go:1159-1161`](../../api/v1/valkey_types.go)) and `replicas` carries only
  `Minimum=1` ([`:1027-1030`](../../api/v1/valkey_types.go)), so a CR with `sentinel.enabled: true`
  and `replicas: 1` is accepted, receives `replicationConfig` and would be fenced permanently.
  Hazard 2 therefore needs the rule above as well.)*
- **Sentinel config is untouched**: `generateSentinelConf`
  ([`sentinel.go:99-194`](../../internal/builder/sentinel.go)) never calls
  `replicationConfig`.

## Proposed shape (not decided)

`spec.writeFencing` (name open), `+optional` pointer, `enabled: false` by default:

```yaml
spec:
  writeFencing:
    enabled: false      # default; absent block renders no directive
    minReplicas: 1      # example; min-replicas-to-write
    maxLagSeconds: 10   # example; min-replicas-max-lag, must be >= 1 (0 disables the gate)
```

Hard prerequisites the implementation owes, in the same change:

1. ~~**Refuse below `replicas: 3`** at runtime (corrected 2026-09-27: at admission, by a CEL rule
   — see the ADR 0015 constraint above — with the runtime refusal as the fallback) — a condition
   (`WriteFencingNotApplied`) plus a Warning Event, never a silent no-op, and never a
   rendered directive. On `replicas: 2` every replica replacement is a write outage, and
   the manual failover is a 5-minute one.~~ *(corrected 2026-09-27 at `84a39c2`: **Refuse
   `minReplicas > replicas - 2` and `maxLagSeconds < 1`**, and never render the directive then:
   either at admission by a CEL rule, or at runtime with a condition (`WriteFencingNotApplied`)
   and a Warning Event, never a silent no-op; which one is the choice named under Design
   constraints, ADR 0015. On `replicas: 2` a replica replacement is a write outage from the
   delete until the replacement is online, and the manual failover leaves the promoted pod
   without a replica from the old master's delete until it returns.)*
2. ~~**Parse `min_slaves_good_slaves`** into `ReplicationInfo` and surface it, or the
   operator stays structurally blind to the only signal that explains a refusal
   (hazard 5). This is the piece with the widest blast radius and it is useful on its own
   (corrected 2026-09-27: it is not useful on its own: the field is absent from
   `INFO replication` unless the directive is set, measured in docker on 9.1.1 and 8.1.9).~~
   *(corrected 2026-09-27 at `84a39c2`: **Parse the per-replica `slaveN` `state` and `lag`
   fields** of `INFO replication`, present with or without the directive (measured on both pins),
   and `min_slaves_good_slaves`, present only while fenced
   ([server.c 9.1.1:6571-6572](https://github.com/valkey-io/valkey/blob/9.1.1/src/server.c#L6571-L6572)),
   into `ReplicationInfo` and surface them. Without them the operator stays blind to the signal
   that explains a refusal (hazard 5). This is the piece with the widest blast radius.)*
3. ~~**Refuse the combination with `spec.observer.enabled` unless `minReplicas` is
   satisfiable**, or the observer turns every legitimate failover window into
   `observerReady=false`.~~ *(corrected 2026-09-27 at `84a39c2`: dropped; its premise is false.
   The observer's `replica_sync` check already reports every zero-replica window, and under
   fencing `write_test` reports only real refusals, consequence 4.)*
4. ~~**Fix `valkeyExec`** to treat a `-`-prefixed stdout reply as an error, and gate the
   e2e writes on `waitForConnectedReplicas` where they are not already.~~ *(corrected 2026-09-27
   at `84a39c2`: the `-` rule identifies nothing (E2E impact). The reply check with
   `valkey-cli -e` is independent of fencing and is filed as
   [T68](068-the-e2e-exec-helpers-do-not-check-the-valkey-reply.md); this implementation
   depends on it having landed. What stays here: gate the e2e writes on
   `waitForConnectedReplicas` where they are not already.)*
5. ~~**Document the enablement outage** at the CRD field, in the shape ADR 0005 D8 uses
   for hard anti-affinity: the person who sets it reads the consequence where they set it.~~
   *(corrected 2026-09-27 at `84a39c2`: **Document the failover refusal and the degraded-state
   outage** at the CRD field, in the shape ADR 0005 D8 uses for hard anti-affinity: every
   controlled failover whose redirected replicas full-resync refuses writes on the promoted pod
   for at least 5 s plus the dataset transfer (consequence 6), and fewer than `minReplicas` good
   replicas — two of three pods down, one stalled replica — is a write outage. The enabling roll
   is not worse than later ones.)*
6. *(added 2026-09-27 at `84a39c2`)* **A divergence-free promotion, or the refusal accepted in
   writing.** Valkey ships the mechanism: `SENTINEL FAILOVER <name> COORDINATED` on Valkey 9
   ([T67](067-the-rolls-own-sentinel-failover-loses-acknowledged-writes.md) option A), and the server-side `FAILOVER TO <host> <port>`, which exists on both pins
   (`COMMAND DOCS FAILOVER` on 8.1.9: "Starts a coordinated failover from a server to one of its
   replicas", since 6.2.0; on a master without a replica it answers `ERR FAILOVER requires
   connected replicas.`). `FAILOVER TO` for the non-Sentinel `promoteAndRedirect` path is not
   measured. Driving `FAILOVER TO` by hand under a Valkey 8 Sentinel is not a sensible variant:
   that Sentinel would see its master report `role:slave` outside a failover it knows about,
   unmeasured ground for ADR 0022 and ADR 0025.

Tests per ADR 0017. Unit: absent block and `enabled: false` render byte-identical config
(the upgrade-neutrality guard); enabled renders both directives in the shared tail of
both config bodies; `replicas: 2` renders nothing and sets the condition;
`parseReplicationInfo` reads `min_slaves_good_slaves`. ~~E2E: a 3-replica cluster with the
feature on survives a full rolling update with every write acknowledged — which is the
test that would actually prove the prerequisite list is complete, and the one most likely
to fail first.~~ *(corrected 2026-09-27 at `84a39c2`: the unit rows also cover
`minReplicas > replicas - 2`, `maxLagSeconds: 0`, `sentinel.enabled` with `replicas: 1`, and the
`slaveN` lag fields. E2E: a 3-replica cluster with the feature on runs a writer through a full
rolling update and asserts that **no acknowledged write is lost**, counting refused writes. "Every
write acknowledged" fails by design on the forced failover path (consequence 6); with the
coordinated failover on Valkey 9, fenced on every node, 0 acknowledged writes were lost and the
new master took writes at +0.62 s in docker.)*

## Verified / not verified *(added 2026-09-27)*

**Verified 2026-09-27.** *Read at `4a7543e`:* every location above, corrected in place where it
moved; `min-replicas`, `min_slaves` and `MinReplicas` appear nowhere in `internal/`, `api/`,
`cmd/` or `deploy/`; the gate at [`configmap.go:109-111`](../../internal/builder/configmap.go)
still excludes standalone; `WriteTestFailure` still defaults to true. *Measured in docker on
`valkey/valkey:9.1.1` and `8.1.9` with `--min-replicas-to-write 1` and no replica:* `SET` answers
`NOREPLICAS`, `WAIT 1 100` answers `0`, `valkey-cli --raw SET` prints the error on stdout and
exits 0 (9.1.1); `min_slaves_good_slaves` is in `INFO replication` only while the directive is
set. The 2026-08-24 measurement is thereby re-run on both pins for these four facts.

~~**Not verified 2026-09-27:** the other rows of the 2026-08-24 table (`REPLICAOF`, `PUBLISH`,
`CONFIG SET`, `CLIENT KILL`, `DBSIZE`, `GET`, `INFO`), the count of 39 e2e write sites, and
anything on Kubernetes.~~ *(corrected 2026-09-27 at `84a39c2`: the table rows and the write-site
count are now verified, see below; Kubernetes stays unverified.)*

**Verified 2026-09-27 at `84a39c2`** (an audit and two independent reviews, each point re-read at
this commit):

- *Read:* `grep -rn 'min-replicas\|min_slaves\|MinReplicas\|min_replicas\|minReplicas'` over
  `internal/ api/ cmd/ deploy/ config/ test/` returns nothing; `ValkeySpec`
  ([`valkey_types.go:1026`](../../api/v1/valkey_types.go)) has no config escape hatch
  (`grep -rni extraConfig api/ internal/` is empty); every location in this file, corrected in
  place where it moved; the six consequences, with the corrections above; `ExecMulti` returns an
  error reply as a Go error ([`client.go:333-358`](../../internal/valkeyclient/client.go), the
  `-` branch at `:479-481`); the operator's only data write is the observer health key
  (`grep -rn '"SET"\|"MSET"\|"DEL"'` over non-test `internal/` and `cmd/` finds only
  [`observer/checks.go:114`](../../internal/observer/checks.go), plus `SENTINEL SET`, which is no
  data write); the four citations of this ticket outside `docs/tickets/` (Options, Decision 1).
- *Upstream source at tag 9.1.1:* the gate
  ([server.c 9.1.1:4541-4542](https://github.com/valkey-io/valkey/blob/9.1.1/src/server.c#L4541-L4542)),
  the good-replica rule and `max-lag 0` switching it off
  ([replication.c 9.1.1:4923-4946](https://github.com/valkey-io/valkey/blob/9.1.1/src/replication.c#L4923-L4946)),
  a `CONFIG SET` of either setting recomputing the count at once through `updateGoodReplicas`
  ([config.c 9.1.1:2617-2619, :3429-3430](https://github.com/valkey-io/valkey/blob/9.1.1/src/config.c#L2617-L2619)),
  `min_slaves_good_slaves` emitted only when both settings are non-zero, on every role
  ([server.c 9.1.1:6568-6572](https://github.com/valkey-io/valkey/blob/9.1.1/src/server.c#L6568-L6572)),
  `master_sync_in_progress` emitted only on a replica
  ([server.c 9.1.1:6506-6525](https://github.com/valkey-io/valkey/blob/9.1.1/src/server.c#L6506-L6525)),
  Sentinel's `REPLICAOF` transaction carrying `CONFIG REWRITE`
  ([sentinel.c 9.1.1:4883-4913](https://github.com/valkey-io/valkey/blob/9.1.1/src/sentinel.c#L4883-L4913)),
  `parallel-syncs` holding back only the second and later replicas
  ([sentinel.c 9.1.1:5283](https://github.com/valkey-io/valkey/blob/9.1.1/src/sentinel.c#L5283)),
  and `SENTINEL FAILOVER <name> [COORDINATED]`
  ([sentinel.c 9.1.1:3920-3962](https://github.com/valkey-io/valkey/blob/9.1.1/src/sentinel.c#L3920-L3962)),
  which the 8.1.9 `sentinel.c` does not contain (`grep -ci coordinated` = 0; 8 at tag 9.0.0).
- *Measured in docker on both pins:* see [Measurements](#measurements-2026-09-27-at-84a39c2).

**Not verified at `84a39c2`, and what would settle it:**

- Anything on Kubernetes. A Kind e2e with a writer on the `-rw` Service through a Sentinel roll
  would settle the fenced failover windows of consequence 6 there. The size of the roll's own
  failover loss on Kubernetes, and the Kind subtest that measures it, belong to
  [T67](067-the-rolls-own-sentinel-failover-loses-acknowledged-writes.md).
- The survivor-lag measurement of consequence 5 ran once, on 9.1.1 only; a rerun on 8.1.9 would
  settle it for the second line.
- The 61 e2e write sites were counted by one reviewer's script; the 15 discarding sites were
  spot-checked by a second. *(2026-09-27:
  [T68](068-the-e2e-exec-helpers-do-not-check-the-valkey-reply.md) recounted the total as 60 with
  a different script; the 15 discarding sites agree.)*
- The coordinated failover, on which Decision 1's re-open trigger rests: its open points (TLS
  replication, small and majority-less Sentinel tiers, a stalled `FAILOVER TO`, the post-failover
  handler) are carried by [T67](067-the-rolls-own-sentinel-failover-loses-acknowledged-writes.md).
- Whether, in ADR 0028's refused-demotion windows, the diverging side is the replica-less one
  (the side a fence would stop). Not measured.

## Measurements 2026-09-27 at `84a39c2`

Every run used `docker run --rm` on the local `valkey/valkey:9.1.1` and `valkey/valkey:8.1.9`
with containers and networks named `vko-verify-012-*`, all removed afterwards. The scripts lived
in the session scratchpad and are not kept; each method is described here so it can be rebuilt.

| What | Method | Result |
|---|---|---|
| Gate per command, zero replicas | `valkey-server --min-replicas-to-write 1 --min-replicas-max-lag 10`, then `valkey-cli --raw <cmd>` per command | both pins: see the re-measurement note under the 2026-08-24 table; `min_slaves_good_slaves:0` present |
| `valkey-cli` on an error reply | `valkey-cli [-e] --raw SET k v` under the gate, `TTL k`, `GET nokey`, `LPUSH` on a string key, `PUBLISH c x` | without `-e`: rc 0, stdout `NOREPLICAS Not enough good replicas to write.` (no `-`), stderr empty; with `-e`: rc 1, stdout empty, error on stderr; `TTL` prints `-1`/`-2` with rc 0; `WRONGTYPE` rc 1; `PUBLISH` rc 0; both pins |
| `WAIT` error text | `valkey-server --replicaof 127.0.0.1 1`, then `WAIT 1 100` | both pins: `ERR WAIT cannot be used with replica instances. ...` |
| Fenced manual failover, idle | m, r1, r2 on one network, all `--min-replicas-to-write 1 --min-replicas-max-lag 10 --repl-diskless-sync yes --repl-diskless-sync-delay 5`; `REPLICAOF NO ONE` on r1, then m and r2 `REPLICAOF r1`, `SET` on r1 in a loop | first accepted `SET` +0.23 s (9.1.1), +0.25 s (8.1.9); `connected_slaves:2`, two good; r2 `Successful partial resynchronization` |
| Same under client write load | as above, with a `valkey-cli SET` loop inside m and `WAIT 2 1000` before the three `REPLICAOF` calls | first `SET` on the promoted pod +5.69 s, 27 refused (9.1.1); +5.08 s, 24 refused (8.1.9); m and r2 log `Full resync from primary` |
| Survivor during another replica's full sync | `--enable-debug-command yes`, `DEBUG POPULATE 2000000` with 200-byte values (522.55 MB `used_memory`), fenced m with two replicas; one replica killed and replaced by a fresh one; `SET` and `INFO` polled every ~0.7 s | 9.1.1 only, one run: survivor `online` at lag 0-1 s, `min_slaves_good_slaves >= 1`, 0 of 11 `SET`s refused; the new replica online after 7.5 s |
| Fenced forced Sentinel failover | m, r1, r2 fenced plus one `valkey-sentinel` (`resolve-hostnames yes`, `announce-hostnames yes`, quorum 1, `down-after-milliseconds 5000`, `failover-timeout 60000`, `parallel-syncs 1`); after two good replicas and a 12 s settle, `WAIT 2 1000`, `SENTINEL FAILOVER mm`, `SET` on the promoted pod every 0.1 s | first accepted write +5.71 to +6.53 s (9.1.1, five runs, a second reviewer +6.55 s), +6.11 / +6.93 / +6.21 s (8.1.9, three runs after the settle; one run without the settle +0.45 s, partial resync); primary log `Partial resynchronization not accepted: Requested offset for second ID was 1369, but I can reply up to 1227`, then `Delay next BGSAVE for diskless SYNC` |
| The fenced Sentinel failover with `SENTINEL FAILOVER mm COORDINATED` | the fenced topology of the row above with `--save '' --appendonly no`; a writer loop inside m (~600 `SET load:<i>`/s) logs every acknowledged index; `WAIT 2 1000`; `SENTINEL FAILOVER mm COORDINATED`; after 15 s every acknowledged index is checked with `EXISTS load:<i>` on the new master | 9.1.1, fenced on every node: 0 of 2047 acknowledged writes lost, first accepted write on the new master +0.62 s; m, r1 and r2 each logged an accepted partial resynchronization. 8.1.9: `ERR wrong number of arguments for 'sentinel\|failover' command` |
| e2e write call sites | a script over `test/e2e/*.go` counting calls of the six helpers whose arguments carry a write-verb literal or forward `args...`, statement-position calls counted as discarding the reply | 61 sites in 16 files, 15 discarding (11 in `tls_test.go`); `grep -c 'valkeyExec(t\|valkeyTLSExec(t'` over `test/e2e/*.go` = 183 calls, reads included |

*(2026-09-27, filed out:)* the rows this ticket's Decision 1 does not rest on moved with their
findings. The acknowledged-write loss of the forced Sentinel failover, unfenced, fenced on every
node and fenced at runtime on the outgoing master only, and the unfenced coordinated runs with one
and with three Sentinels, are in
[T67](067-the-rolls-own-sentinel-failover-loses-acknowledged-writes.md)'s Measurements. The loss in
the `promoteAndRedirect` promote-to-demote gap without a fence (1 acknowledged write on 9.1.1 and
2 on 8.1.9 at about 250 writes/s) is item (b) of
[T70](070-tracked-comments-and-adrs-state-what-the-code-contradicts.md); a fence would not help
there, because the other replica is still attached during that gap.

## Options

### Order with T67

[T67](067-the-rolls-own-sentinel-failover-loses-acknowledged-writes.md), the roll's own Sentinel
failover losing acknowledged writes, is decided before Decision 1. Decision 1's cost argument
depends on which failover path the roll uses: on the forced path an opted-in cluster paid at least
5 s of refused writes at every controlled failover measured on a settled cluster (consequence 6);
with T67's recommended option A, the coordinated failover on Valkey 9, it pays about a second.
The measurements and options of that decision (formerly Decision 2 here) live in T67 only.

### Decision 1 — offer `min-replicas-to-write` as an opt-in CRD field, or refuse it in an ADR

**Mechanism.** Today `generateValkeyConf`
([`configmap.go:62-138`](../../internal/builder/configmap.go)) writes no `min-replicas` directive,
and the CRD has no field or escape hatch for one, so every master accepts writes whether or not it
has a replica (Valkey's default `min-replicas-to-write 0`). In a split both sides take writes and
the repair (`REPLICAOF` on the loser) discards the loser's; ADR 0028 D3 lengthens that window
whenever it refuses a demotion. The directive makes a master refuse every write command with
`NOREPLICAS` while fewer than N replicas are `ONLINE` and have ACKed within `max-lag` seconds;
`WAIT`, `REPLICAOF`, `PUBLISH`, `CONFIG SET`, `CLIENT KILL` and reads pass (measured, both pins).
Placed in the shared tail of `replicationConfig`
([`configmap.go:174-181`](../../internal/builder/configmap.go)), it enters both config bodies and
the config hash ([`:293-305`](../../internal/builder/configmap.go)), so turning it on rolls that
cluster. **What the choice changes** on a cluster that opts in: the side of a split without a good
replica stops taking writes; and, measured, the promoted pod refuses writes at every controlled
failover whose redirected replicas full-resync — at least 5 s plus the dataset transfer on the
forced Sentinel path and on `promoteAndRedirect` under load. Replacing one replica at
`replicas >= 3` refuses nothing (measured once, 9.1.1). **What it does not change:** no fleet CR
is affected until it opts in (ADR 0005 D1), no split is resolved, and a replica still full-syncs
an empty restarted master (`REPLICAOF` and `SYNC` are not gated; T36).

1. **Build `spec.writeFencing`.** L, plus the divergence-free promotion for the paths T67's option A
   does not cover. An opt-in block (`enabled`, default false; `minReplicas`; `maxLagSeconds`)
   rendered into the `replicationConfig` tail, with the corrected prerequisites of "Proposed
   shape": the rule `minReplicas <= replicas - 2` and `maxLagSeconds >= 1`, at admission (CEL,
   which also refuses later scale-downs, against the PDB precedent at
   [`valkey_types.go:775-779`](../../api/v1/valkey_types.go)) or at runtime with a condition;
   parsing the `slaveN` lag fields and `min_slaves_good_slaves`; the failover refusal documented at
   the field; a divergence-free promotion or the refusal accepted; an e2e that counts
   acknowledged-but-lost and refused writes through a full roll. It depends on the e2e reply check,
   [T68](068-the-e2e-exec-helpers-do-not-check-the-valkey-reply.md). Nothing rolls on upgrade; enabling it rolls that cluster once. Consequences:
   every later failover on Valkey 8 Sentinel clusters, and on the non-Sentinel path under client
   write load (idle it took writes at +0.23 s), refuses writes for at least 5 s plus the transfer
   whenever the redirected replicas full-resync; a cluster with fewer than `minReplicas` good replicas (two
   of three pods down, a stalled replica) has a write outage the operator cannot explain until the
   lag fields are parsed. It protects ADR 0028's refused-demotion windows and steady-state splits
   on the clusters that opt in.
2. **Refuse the field in an ADR** **(recommended)**. S. A new ADR, "the operator does not offer
   `min-replicas-to-write` as a CRD field", carrying the measurements of this ticket and the
   corrected prerequisite list of option 1 as its Alternatives, so a re-opening starts from the
   design. It states that fencing would not protect against T36's replica flush. Its re-open
   trigger: a user asks for it, runs `replicas >= 3`, and runs Valkey 9 with the coordinated
   failover of [T67](067-the-rolls-own-sentinel-failover-loses-acknowledged-writes.md) option A, the condition under which option 1's recurring failover cost mostly
   disappears on the Sentinel path. Then [ADR 0025](../adr/0025-a-split-brain-warning-means-one-that-did-not-resolve-itself.md)
   `:443-444` (the T12 label and the path link into `docs/tickets/`),
   [ADR 0028](../adr/0028-a-demotion-may-not-discard-the-only-dataset.md) `:123`, `:230` and the
   comment at [`pod_termination_test.go:257`](../../internal/controller/pod_termination_test.go)
   cite the new ADR instead of this ticket (ADR 0034 D7);
   [ADR 0026](../adr/0026-a-pod-being-deleted-is-not-available.md) `:206-208`, which names "no
   write fencing on either side" without a citation, may reference it. ADR family: if T67
   were decided as the runtime fence (its option B), the refusal must live in the same ADR as that
   fence, or one ADR says the operator does not set `min-replicas-to-write` and another says it
   does. The ticket is archived after the XS fixture has landed (the findings filed out of it moved on
   2026-09-27, Work list). Nothing rolls and nothing changes at runtime; the divergence in ADR 0028's
   windows stays accepted, now as a recorded decision.

**Why option 2.** Nobody has asked for the field: the 2026-08-24 and 2026-08-26 looks chose no
option, and the History records no requester. Option 1 is L, and an opted-in cluster pays a
recurring, measured cost: at least 5 s of refused writes per controlled failover on Valkey 8
Sentinel clusters (6.1-6.9 s in the three runs on a settled cluster) and on the non-Sentinel path
under load (5.1-5.7 s), plus
write outages in degraded states that nothing reports yet. What it would protect beyond
T67's option A is ADR 0028's refused-demotion windows, which are bounded (ADR 0010) and reported
(`MultipleMasters`), and steady-state splits (ADR 0011), on those clusters only. Option 1 would
win if a user asked for the field while the fleet runs Valkey 9 with the coordinated failover,
which is exactly the re-open trigger the ADR records. If T67 goes to its option C, option 1
becomes the only protection of the ADR 0025 D9 window for clusters that opt in, and this
comparison has to be redone.

Not proposed: a generic `spec.extraConfig` escape hatch. It would let a user set the directive,
but also override every directive the operator manages (`replicaof`, `save`, TLS). That is a
much larger product call than this one.

## Decision

**Current: not decided** *(2026-09-27)* — a product call between the Options of Decision 1;
the roll's own failover loss, decided before it, is [T67](067-the-rolls-own-sentinel-failover-loses-acknowledged-writes.md)'s decision and not decided either. The dated
entries below are the analysis notes of the two earlier looks, not decisions.

- 2026-08-24: raised as its own item at the user's request, after the T4 discussion
  surfaced the missing fencing. Analysis above; **no option chosen and no work started.**
  Recorded explicitly: this is not the one-line opt-in it looks like — the operator's
  own design tolerates multi-minute zero-replica windows on purpose, so the feature is
  the prerequisite list, not the directive.
- 2026-08-26: **still no option chosen.** Re-verified: the Decision section genuinely records
  no choice, so there is nothing to implement yet — **this item is blocked on a product call,
  not on engineering effort**, and it should not be picked up as "the next ticket" until that
  call is made. `grep -rn 'min-replicas' internal/ api/` still returns nothing.

  Two ADRs now name the absence of this feature as the price of an accepted risk —
  [ADR 0028](../adr/0028-a-demotion-may-not-discard-the-only-dataset.md) ~~`:189-191`~~ *(corrected 2026-09-27: `:123`, `:230`)* and
  [ADR 0025](../adr/0025-a-split-brain-warning-means-one-that-did-not-resolve-itself.md)
  ~~`:243-248`~~ *(corrected 2026-09-27: `:439-444`)* — which strengthens the case for deciding it, and changes nothing about it being
  a decision rather than a task.

  **The tradeoff to put in front of whoever decides:** a naive enablement is a net
  regression, not a partial win. The operator deliberately produces zero-replica windows
  during its own rolling update, so `min-replicas-to-write` set without the prerequisite list
  turns a *durability* risk into an *availability* outage on every roll — and the e2e that
  would catch that is the one most likely to fail first.

  Reference drift corrected 2026-08-26 (this item was written before `2051a34`/`75b3c92`):
  `handleMasterWithNoReplicas` `1940-1985` →
  [`rolling_update.go:2505`](../../internal/controller/rolling_update.go#L2505); the
  `waitForWriteSync` early return `1774-1777` →
  [`:2321-2324`](../../internal/controller/rolling_update.go#L2321-L2324); `writeTestFailure`
  `valkey_types.go:1075-1078` → [`:647`](../../api/v1/valkey_types.go#L647) with the default
  at `:1157-1160`; `IsObserverEnabled` `:1007` → [`:1091-1093`](../../api/v1/valkey_types.go#L1091-L1093);
  `observer/checks.go:108-115` → `:113`. Two counts were also wrong: the "~15 e2e write call
  sites" is **39** across 13 files, and both sites named as misleading
  (`sentinel_stale_master_test.go:204`, `standalone_test.go:396`) use `valkeyExecAllowError`,
  not `valkeyExec` — so they are not the hazard this item claimed.

  *(Re-corrected 2026-09-27 at `4a7543e`, locations re-read at `84a39c2`: `handleMasterWithNoReplicas`
  [`:3145`](../../internal/controller/rolling_update.go); the early return
  [`:2933-2936`](../../internal/controller/rolling_update.go); `writeTestFailure`
  [`valkey_types.go:908-911`](../../api/v1/valkey_types.go) with its default in
  `UnreadyWhenDefault` [`:1457-1463`](../../api/v1/valkey_types.go); `IsObserverEnabled`
  [`:1363`](../../api/v1/valkey_types.go); `writeHealthKey`
  [`observer/checks.go:110`](../../internal/observer/checks.go). The 39 write sites were not
  re-counted.)* *(2026-09-27 at `84a39c2`: recounted with a script as 61 in 16 files, E2E
  impact.)*

## Verification

- Decision 1 option 2: the refusal ADR exists and is in the ADR index;
  `git grep -n 'T12\|012-no-write' -- ':!docs/tickets'` returns nothing (ADR 0025 `:443-444`,
  ADR 0028 `:123`, `:230` and `pod_termination_test.go:257` rewritten); the fixture item below has
  landed.
- Decision 1 option 1: the unit and e2e rows of "Proposed shape", with the e2e asserting that no
  acknowledged write is lost through a full roll, refused writes counted, on both Valkey legs.
- The XS fixture item: `make test-unit` green with the new reply text.

## Work list *(added 2026-09-27)*

- **XS, needs no decision, can land today:** the fixture of
  `TestHandleMasterFailover_DoesNotFailOverWhenWriteSyncFails`
  ([`sentinel_failover_test.go:579-581`](../../internal/controller/sentinel_failover_test.go))
  answers `WAIT` with `NOREPLICAS`, which `WAIT` never returns (measured). Give it an error text
  `WAIT` does return — `ERR WAIT cannot be used with replica instances. ...`, measured on a
  replica on both pins — and one comment line saying any error reply blocks the promotion. The
  test keeps its assertion.
- **Done 2026-09-27: filed as tickets of their own** (outside this ticket's mechanism; each
  finding is a file, README filing rule):
  - Filed as [T67](067-the-rolls-own-sentinel-failover-loses-acknowledged-writes.md): the
    roll's own Sentinel failover loses the writes the outgoing master acknowledges after the
    promotion (formerly the separate finding and Decision 2 here, with its Kind e2e subtest).
  - Filed as [T68](068-the-e2e-exec-helpers-do-not-check-the-valkey-reply.md): the e2e helpers
    `valkeyExec` and `valkeyTLSExec` do not check the Valkey reply; option 1 here depends on it.
  - Filed as [T69](069-three-sync-checks-read-a-replica-field-from-the-master.md): three sync
    checks read `master_sync_in_progress`, a replica-only field, from a master's `INFO`
    (`CheckCluster`, the observer's `replica_sync`, `verifyNewMasterReady`).
  - Filed as [T70](070-tracked-comments-and-adrs-state-what-the-code-contradicts.md), items (a)
    and (b): the "no replicas attached yet" comments that hold only for `replicas: 2`, and the
    claim that `waitForWriteSync` drains the outgoing master of writes.
- **Waits on Decision 1:** option 2 (the ADR, the three ADR citations, the test comment, the
  archive) or option 1 (everything under "Proposed shape").
- **Done by this edit:** the in-place corrections of 2026-09-27 at `84a39c2` (History).

## Related tickets

- **T40** counts this ticket's four citations outside `docs/tickets/` (ADR 0025 `:443-444` with
  the path link at `:444`, ADR 0028 `:123`, `:230`, `pod_termination_test.go:257`). Decision 1
  option 2 retires them, so T40's per-label and path counts change in the same work.
- **T36**: fencing does not protect against its replica flush — `REPLICAOF` and `SYNC` are not
  gated (measured) — and the refusal ADR must not claim otherwise. T36's fact that the `emptyDir`
  config outlives a container restart is also why T67's option B fence can strand.
- **T62**: `handleMasterWithNoReplicas` calls `resetSentinelState`
  ([`rolling_update.go:3161`](../../internal/controller/rolling_update.go)), T62's subject. T62
  changes the reset, not the 90 s × 2 bound cited in consequence 1.
- **T34**: ~~edits the same e2e helpers as the reply check~~ *(corrected 2026-09-27,
  consistency pass: its recommended option E edits the readiness waits in the same file,
  `test/e2e/e2e_test.go`, not `valkeyExec`)*; different mechanism. *(2026-09-27: the reply check
  moved to T68, which now carries this relation.)*
- **T35**: its decision 8 (recommended L4, the labeler's cross-check reads
  `get-master-addr-by-name`) shortens how long `-rw` keeps routing to the outgoing master after
  the promotion - the leader's `SENTINEL MASTER` `ip` lags for its whole `RECONF_REPLICAS` phase,
  1.07 s to 5.38 s measured there; it does not stop that master from acknowledging writes, which
  is [T67](067-the-rolls-own-sentinel-failover-loses-acknowledged-writes.md). T35 names this ticket as the owner of that write loss; since 2026-09-27 the owner is
  T67. T35 left its flag-aware `-rw` fence (L5, empty `-rw` for the whole failover) to this
  ticket's fencing question; no option here takes it up, and T67's option A (0 acknowledged
  writes lost, measured) removes its reason on Valkey 9. The "no replicas attached yet" comment
  finding, for which T35 was named a candidate family, is filed in T70 instead.
- **[T67](067-the-rolls-own-sentinel-failover-loses-acknowledged-writes.md)**: decided before Decision 1 (Options). If it goes to its option C, option 1 here
  becomes the only protection of the ADR 0025 D9 window for clusters that opt in; if it goes to
  its option B, the refusal ADR of option 2 here and B's fence belong in one ADR.
- **[T68](068-the-e2e-exec-helpers-do-not-check-the-valkey-reply.md)**: the e2e reply check; option 1 here depends on it having landed.
- **[T69](069-three-sync-checks-read-a-replica-field-from-the-master.md)**: the replica-only `master_sync_in_progress` read by `CheckCluster` (consequence 2).
- **[T70](070-tracked-comments-and-adrs-state-what-the-code-contradicts.md)**: items (a) and (b) correct the two code comments this ticket found false.
- **Origin**: raised out of T4/T11 in
  [archive/039](archive/039-findings-from-the-1-11-0-fleet-rollout.md); its board row there is
  history and stays as is.

## History

- 2026-09-27: re-verified at `84a39c2` (an audit, a facts review and a design review; contested
  points re-read by the editor, including `SENTINEL FAILOVER ... COORDINATED` in sentinel.c at
  tags 9.1.1 and 8.1.9). **Checked:** the premise (no directive, field or code; grep over
  `internal/ api/ cmd/ deploy/ config/ test/` empty), every location (seven claims had drifted by
  one to three lines and were fixed in the links: the `rolling_update.go` comment `:4039-4043`,
  `handleMasterWithNoReplicas` `:3145`, `verifyValkeyConnectivity` `:2970-2981`, the observer
  field and default `:908-911` / `:1457-1463`, `IsObserverEnabled` `:1363`, the early return
  `:2933-2936`, the `XValidation` markers `:541-542` and `ValkeySpec` `:1026`; also
  `configmap.go:62-138`, `:174-181`, `sentinel.go:99-194`), and every claim against code, upstream
  source and docker. **Found false or outdated, corrected in place:** the promoted pod has zero
  replicas only on `replicas: 2`; the 5 min `GetSyncTimeout` is a stall bound, not a zero-replica
  window; `parallel-syncs 1` does not lengthen the window for `minReplicas: 1`; placement does not
  exclude `sentinel.enabled` with `replicas: 1`; the observer interlock is unnecessary; a
  sibling's full sync does not make the survivor lag; the enabling roll is not worse than later
  ones; `standalone_test.go:396` asserts nothing and would see `READONLY`; the `-` prefix rule
  detects nothing (`valkey-cli -e` does, with retry and `Eventually` caveats); the CEL rule is
  `minReplicas <= replicas - 2` plus `maxLagSeconds >= 1`, the runtime fallback defends no chart
  skew, and admission refusal also refuses scale-downs against the PDB precedent; the placement
  reasoning was inverted; "~15" e2e write sites are 61 in 16 files (39 in the 2026-08-26 count);
  "the diverging side would collect nothing" is bounded by `max-lag`; the e2e "every write
  acknowledged" fails by design on the forced path; the earlier "not verified" table rows are now
  verified. Nested strike-throughs of pure location values inside the corrected passages were
  collapsed into one strike each. **Measured** (docker, both pins, section Measurements): the full
  gate table; `valkey-cli` exit codes; the `WAIT` error text; fenced manual failover idle and
  under load; the survivor during a full sync (9.1.1 once); fenced forced Sentinel failovers;
  acknowledged-write loss of the forced, runtime-fenced and coordinated Sentinel failovers; the
  `promoteAndRedirect` gap. **New:** the roll's own Sentinel failover loses about 10000
  acknowledged writes per failover in docker (ADR 0025 D9's window), recorded as a separate
  finding to be filed as its own ticket (filing rule), with Decision 2; three further findings to
  file (Work list). **Options:** Decision 1 keeps option 1 (with corrected prerequisites, the
  observer interlock dropped, a divergence-free promotion added) and option 2, still
  **recommended**, its justification changed: it no longer rests on "every full sync of a roll"
  or on "the enablement produces the longest outage", which were false, nor on a universal
  refusal of at least 5 s per failover, which the coordinated failover removes on Valkey 9
  Sentinel clusters; it rests on no requester, L effort, the refusal that remains on Valkey 8
  Sentinel and the non-Sentinel path, degraded-state outages, and a re-open trigger naming Valkey 9
  with the coordinated failover. **Removed:** option 3 "leave it in icebox" (no action, no
  record) — not an actual choice, it reaches option 2's outcome without the ADR that ADR 0034
  requires and leaves ADR 0025's path link to break on archive; option 4 "fence the split at the
  `SplitBrainDetected` edge" (runtime `CONFIG SET` on every master after 90 s) — rests on two false
  premises: on Sentinel clusters the setting does not die with the process, because Sentinel's
  `REPLICAOF` transaction carries `CONFIG REWRITE` into the writable `emptyDir` config, and
  `MultipleMasters` is evaluated only inside a roll (callers `rolling_update.go:715`, `:3909`;
  ADR 0025 Residual risks), so a fence set in an abandoned roll is never lifted and a steady-state
  split is never seen; its one advantage, no roll cost, is delivered for the largest measured
  window by Decision 2. **Decision 2 options** (labels of the audit in brackets): 2a coordinated
  failover with the forced fallback (the design review's addition, **recommended**), 2b runtime
  fence on the outgoing master (the audit's option A, recommended by the audit; demoted because
  2a lost 0 writes where 2b lost about 500, with no state to strand), 2c record the loss (the
  audit's B). Removed: the audit's option C "2b behind an opt-in CRD field" — a CRD surface for a
  roll-internal repair that helps nobody by default, while ADR 0025 D9 itself changed the roll
  fleet-wide without a field. **Frontmatter:** effort L → S, because the recommended path is
  Decision 1 option 2 (S) plus the XS fixture, and the reply-check item moves to its own ticket;
  L applies only if option 1 is chosen. Severity stays medium: the audit proposed high on the
  strength of the Decision 2 loss, which both reviews place in its own ticket (severity high
  there, estimate). Urgency stays `next`, re-derived: rule 1 does not match (the measured-false
  statements in this file are corrected by this edit, the false code comments go to their own
  tickets, and the fixture's canned reply is test input, not a statement), rule 2 does not match,
  rule 3 matches (severity medium, trigger live: ADR 0028 D3's refused demotions and steady-state
  splits in released code). State stays `analysed`, security `none` (durability, no hostile
  principal), `blocked-by: product`. **Review of this edit** (same day, locations and upstream
  lines re-read at `84a39c2` and tag 9.1.1): the one nested correction left (the probe range)
  collapsed; the `valkeyExec` backoff corrected from "2-10 s, about 40 s" to 4, 6, 8 and 10 s
  (28 s of sleep); the `resetSentinelState` call is at `:3161`, the "drained of writes" comment
  at `:4216-4217`; the "at least 5 s" refusal qualified where it read as universal (idle
  `promoteAndRedirect` took writes at +0.23 s, one unsettled 8.1.9 Sentinel run at +0.45 s);
  ADR 0025's "read, not measured" qualifies the 90 s bound, not the loss; the coordinated runs
  counted as seven; added, read in source, that at a coordinated promotion Sentinel kills the
  clients of both pods and sends `CLIENT UNPAUSE`.
  Cross-ticket: in the consistency pass of the same day, Related tickets gained the T35 decision 8
  relation (L4 shortens how long `-rw` routes to the outgoing master, Decision 2 here stops it
  acknowledging; they compose, and T35 now names Decision 2 as the owner of the write loss), and
  the claim that T34 edits the same e2e helpers was corrected in both places (T34's option E edits
  the readiness waits in `e2e_test.go`, not `valkeyExec`; T34 now carries the reverse note).
  Filed: the separate finding and Decision 2 (the roll's own Sentinel failover loses acknowledged
  writes) as [T67](067-the-rolls-own-sentinel-failover-loses-acknowledged-writes.md), with the
  Measurements rows of the forced, runtime-fenced and unfenced coordinated failovers and the
  Kubernetes and coordinated-failover points of "Not verified"; this ticket keeps Decision 1, an
  "Order with T67" section in place of "Order of the two decisions", and the one coordinated run
  fenced on every node that Decision 1's re-open trigger rests on. Filed: the e2e reply check as
  [T68](068-the-e2e-exec-helpers-do-not-check-the-valkey-reply.md), linked from E2E impact and
  prerequisite 4, with T68's recount of 60 write sites noted. Filed: the three reads of
  `master_sync_in_progress` from a master as
  [T69](069-three-sync-checks-read-a-replica-field-from-the-master.md). Filed: the "no replicas
  attached yet" and "drained of writes" comment findings as items (a) and (b) of
  [T70](070-tracked-comments-and-adrs-state-what-the-code-contradicts.md), which also holds the
  `promoteAndRedirect` gap measurement removed from the table here. The Work list items that asked
  for these filings are marked done with pointers, and Related tickets gained T67 to T70 (T35's
  write-loss relation now points at T67). Frontmatter: only the severity comment changed, because
  it named the separate finding as still to be filed; severity medium, urgency `next` and effort S
  never rested on the moved findings and stay.
  Final pass: checked that E2E impact (its closing correction) and prerequisite 4 both link
  [T68](068-the-e2e-exec-helpers-do-not-check-the-valkey-reply.md), where the e2e reply check now
  lives (T68 recommends its option B, plain `-e` in the two strict helpers, as E2E impact states);
  both already did, and E2E impact's scripted count of 61 write sites now also names T68's recount
  of 60, as Not verified already did. Frontmatter unchanged.
- 2026-09-27: adversarial review of the enrichment — about half of the corrected locations
  re-read at `4a7543e`, all held but the probe range (now `:847-870`); `7 of 11` recounted
  (eleven pointer blocks in `ValkeySpec`, seven with `Enabled`). Added option 4, a runtime fence
  at the `SplitBrainDetected` edge, which the options had missed; option 2 stays recommended,
  its justification narrowed accordingly, and its archive step now waits for the independent
  `valkeyExec` item. Urgency `next` kept: the derivation is the table's, and the flag for Hans
  below stands. No frontmatter change.
- 2026-09-27: enriched - locations re-read at `4a7543e` and corrected in place; two claims
  corrected: CEL exists since ADR 0033, so prerequisite 1 can be an admission rule, and
  `min_slaves_good_slaves` is not useful alone, because it is absent without the directive
  (measured in docker on both pins); Options added (build, refuse in an ADR, leave), with the
  refusal ADR recommended; a work list separating one XS fixture fix. **Urgency icebox → next:**
  rule 3 matches before rule 5. Severity is medium, and the trigger is live in released code:
  every two-master window, which ADR 0028 D3 deliberately lengthens, lets both sides take writes
  that the repair then discards. What is next is the product call, not the build.
  `blocked-by: product` stays. The earlier triage kept `icebox`, reasoning that the feature is
  opt-in and the risk accepted; the table has no such exception, so Hans may overrule it
  explicitly.
- 2026-09-27 - extracted verbatim from the collection ticket (now [archive/039-findings-from-the-1-11-0-fleet-rollout.md](archive/039-findings-from-the-1-11-0-fleet-rollout.md)) into its own file when the tickets were numbered. Frontmatter filled from the final board row (board archive of that file, groomed 2026-09-26) and from the section text.
