---
id: T12
title: no acknowledged write and no dataset is lost across the rolling update's master handover
state: decided        # Q1-Q6 decided 2026-09-28; ADR 0037 (Q1-Q5) and ADR 0038 (Q6) written the same day
severity: high        # acknowledged writes lost on every Sentinel roll; the only dataset deleted in the refusal shape with phase OK
security: none        # durability and data integrity; no principal gains a verb or an object
urgency: now          # rule 1: tracked texts call every multi-replica roll lossless and describe sync and dataset gates the code does not have
effort: L             # three code changes on one handover path, one e2e writer harness, ADR amendments; fencing as a refusal ADR
blocked-by:
filed-from: T4 analysis, 2026-08-24
opened: 2026-08-24
decided: 2026-09-28
done:
---

# T12 - no acknowledged write and no dataset is lost across the rolling update's master handover

**Scope.** A Sentinel data-tier roll hands the master over once: it forces a Sentinel failover,
re-points pods at the new master, checks the new master and deletes the former one. Each step can
lose writes acknowledged to a client or the dataset of the only pod holding it, and tracked texts
describe guards the code does not have. The parts, in handover order:

- the roll's forced Sentinel failover, and the second failover the dying master can trigger;
- the three "no sync in progress" checks that read a replica-only field from the master;
- `forceReplicaConnections` re-pointing data holders at an empty master;
- the delete of the former master behind a gate that refuses on no key count;
- write fencing (`min-replicas-to-write`) as an opt-in CRD field.

## Current state

### The handover path (shared)

O is the outgoing master, X the pod the roll takes as new master. Line numbers without a file are
in [rolling_update.go](../../internal/controller/rolling_update.go). Measurements ran in docker on
`valkey/valkey:9.1.1` and `8.1.9` with the operator's settings unless noted.

- **Trigger.** `handleMasterFailover` (`:2802-2865`) waits for the replicas, sends `WAIT`, stamps
  `setFailoverTriggered` (`:2850`), triggers (`:2858`), requeues after 15 s. The retrigger
  `handleFailoverRetrigger` (`:956-994`, trigger `:989`) skips the replica and write-sync gates
  (`:2817-2826`). Both use `triggerSentinelFailover` (`:3807-3847`): each Sentinel in ordinal order,
  plain `SENTINEL FAILOVER <name>` ([client.go:231-238](../../internal/valkeyclient/client.go#L231-L238)).
- **No-replica branch.** `handleMasterWithNoReplicas` (`:3252`) tolerates a zero-replica X for
  about 270 s (90 s `replicaReconnectTimeout`, re-armed twice); past 90 s (`:3257`) it calls
  `forceReplicaConnections` (`:3266`) and `resetSentinelState` (`:3268`).
- **Delete.** `handleNewMasterFound` (`:3236`) hands over to `replaceRemainingPods`
  (`:3091-3158`): `verifyNewMasterReady` (Sentinel only, `:3119-3124`), the ADR 0026 D5 gate,
  `replacing-master` (`:3136`), delete of O (`:3141`), at the 15 s requeue or earlier on a watch
  event. `finalizeRollingUpdate` (`:998`, `:1053-1096`) reads no key count.
- **The gate.** `verifyNewMasterReady` (`:3438-3504`) takes the first current `available()` pod
  answering `role:master` as X and refuses only on `connected_slaves == 0` (`:3457`), an
  unreadable TLS config or `DBSIZE` of X (`:3475-3486`), or no non-terminating candidate. Its
  `master_sync_in_progress` term (`:3462`) never fires. On any readable count, zero included, it
  logs "New master verified with data" (`:3488`); it never reads O's count; its other waits are
  unbounded plain requeues.
- **The ADR 0028 refusal shape.** The resolver runs before every dispatch (`:821-822`) with
  Sentinel's master pointer as authority, except while `ownFailoverInFlight` (`:904-929`), the
  negation of the predicate gating `:3266`. `demotionRefusalReason` (`:1820-1843`, counts via
  `dbSizeReader` `:1788-1800`) refuses when the authority holds zero keys and the rogue some, or a
  count is unreadable; the refusal lives only on the pod slice `handlePostFailover` discards
  (`:3178`). X is returned as `masterIdx` (`:1585`), O keeps `isMaster` (`:1766-1770`). ADR 0028 D8
  (`:198`, `:206-210`) records the delete as the end of the refusal on the Sentinel path.
- **Routes to an empty X:** the retrigger (Sentinel selects without reading a key count), and a
  promoted non-persistent pod restarting empty
  ([statefulset.go:288-339](../../internal/builder/statefulset.go#L288-L339), T35).

### The roll's forced Sentinel failover

A plain `SENTINEL FAILOVER` is **forced**: a replica gets `REPLICAOF NO ONE`, O is not told
([sentinel.c 9.1.1:3957-3960](https://github.com/valkey-io/valkey/blob/9.1.1/src/sentinel.c#L3957-L3960)).
O acknowledges writes until Sentinel converts it after 8 s of reporting master while listed as
replica ([:2630-2641](https://github.com/valkey-io/valkey/blob/9.1.1/src/sentinel.c#L2630-L2641));
they are discarded on resync, and `WAIT` covers only earlier ones. O keeps its master label and the
`-rw` endpoint until its labeler's Sentinel
([labeler.go:135-145](../../internal/sidecar/labeler.go#L135-L145)) sees `+switch-master`; pooled
connections stay until the conversion.

**Second failover.** Nothing asks O its role before the delete (the comment at `:3126` says "now a replica", true only after `+convert-to-slave`). The delete SIGTERMs O's sidecar, whose drain handler
([drain.go:98-148](../../internal/sidecar/drain.go#L98-L148)) sends the same forced command if O
still answers master, failing over the pod just promoted. By timing (promotion at about +1 s,
conversion about 16.4 s later, delete pass at 15 s or earlier) this is the expected order.

| Trigger (two replicas, one Sentinel, 500-600 writes/s) | Acknowledged writes lost | Timing |
|---|---|---|
| Forced | 9.1.1: 9133-11249 of 13271 (four runs); 8.1.9: 8633, 9510 | `+convert-to-slave` 16.3-16.4 s after `+promoted-slave` |
| Forced, `min-replicas-to-write 1` on every node | 556-596 | |
| Forced, runtime `CONFIG SET` fence on O first | 512-542 | writable at +0.42-0.50 s |
| `SENTINEL FAILOVER <name> COORDINATED` | 9.1.1: 0 in eight runs (one Sentinel; three at quorum 2); 8.1.9: `ERR wrong number of arguments for 'sentinel\|failover' command`, no failover | writable at +0.62-1.4 s; O `role:slave` at once |
| Second forced failover 14 s after the first (9.1.1, once) | | `OK`; the other replica promoted 1.14 s later |

**Impact.** Every client writing to a Sentinel cluster while its data tier rolls: image or config
changes, pod-spec-changing upgrades, metrics or anti-affinity, every TLS rotation roll
([ADR 0030](../adr/0030-rotating-certificates-rotate-the-instances-that-cannot-reload-them.md) D4).
Each roll fails over once ([ADR 0007](../adr/0007-failover-aware-rolling-update.md) D1), forced.
Clients see `OK`, `Ready` stays `True`, the only Event is Normal `FailoverTriggered`; the pre-roll
dataset survives. [ADR 0025](../adr/0025-a-split-brain-warning-means-one-that-did-not-resolve-itself.md)
D9 accepts the loss (`:285-295`) without a size. No e2e writes during a roll. Fifteen tracked places
claim a multi-replica roll loses nothing (listed under Required changes).

### The sync checks

`master_sync_in_progress` exists only on a replica
([server.c 9.1.1:6506-6525](https://github.com/valkey-io/valkey/blob/9.1.1/src/server.c#L6506-L6525),
[8.1.9:6039-6058](https://github.com/valkey-io/valkey/blob/8.1.9/src/server.c#L6039-L6058)); on a
master `parseReplicationInfo` ([client.go:592-593](../../internal/valkeyclient/client.go#L592-L593))
leaves it `false`, and `connected_slaves` counts a replica from its sync request. Measured (1.5 M
keys): no master reply carried the field; a fresh replica is counted 6-8 s while empty; after a
forced failover under writes the other replica full-resyncs, counted from +0.7 s, link up at +8 s.
A loading replica answers `LOADING` to `PING` with exit 0 and stays Ready (T35).

| Site | Stated contract | Actually checks |
|---|---|---|
| `CheckCluster` `AllSynced` [checker.go:130](../../internal/health/checker.go#L130), used at [valkey_controller.go:2475-2488](../../internal/controller/valkey_controller.go#L2475-L2488), Sentinel only | "all replicas have completed sync"; phase `Syncing` | `connected_slaves == replicas - 1` |
| observer `checkReplicaSync` [checks.go:103](../../internal/observer/checks.go#L103) | CRD `replicaSyncFailure` "bulk sync is in progress" ([valkey_types.go:918](../../api/v1/valkey_types.go#L918)) | `connected_slaves >= replicas - 1` |
| `verifyNewMasterReady` `:3462` | "all replicas synced", "no sync in progress" | `connected_slaves > 0` |

The replica-side answer exists: `replicationNotEstablishedReason` (`:4574-4583`: role, link `up`, no
transfer), applied by `verifyReplacedReplicasSynced` (`:2643-2710`, bounded by
`spec.rollingUpdate.syncTimeout`, pause on expiry) and asked by the sidecar's `isSyncedReplica`
([drain.go:351-353](../../internal/sidecar/drain.go#L351-L353)). ADR 0007 D10 requires it before a
promotion; nothing asks it before the delete. Its "still syncing" message (`:4579-4581`) is
unreachable. Eight unit fixtures and one e2e subtest pin impossible replies.

**Impact.** On a Sentinel roll with `replicas >= 3` O is deleted while the other replica is in
`wait_bgsave` or loading; a second fault then loses the writes since the promotion (non-persistent:
the dataset). After a full sync the status reads `OK`, `Ready=True/HAClusterReady` while a replica
is empty; a sync restarting forever never fires `ValkeyPhaseNotOK`; `replica_sync` never goes red.

### Re-pointing pods at the new master

`forceReplicaConnections` (`:3299`; callers `:1090` and `:3266`) sends `REPLICAOF X` to every existing
Ready pod except X, terminating ones included; it asks no role, reads no key count, and skips a
failed send. At `:3266` it performs exactly the demotion of O the resolver refused, and re-points
every replica holding O's copy; likewise when a count was unreadable, when O was not counted master,
or when the 90 s boundary falls between `:904` and `:3257`. Only "authority is O" does not reach
it. Measured: a 500-key master and its replica re-pointed at an empty X are empty by +7 s; O with
the operator's `rdb` or `aof` lines ([configmap.go:187-246](../../internal/builder/configmap.go#L187-L246))
re-pointed and restarted boots master with `DBSIZE` 0 in four of four runs (the sync replaces the
files). Comments, test texts and ADR 0028 Residual risks `:284` call the path safe.

Three routes flush O in the refusal shape, and only one is the operator's. Sentinel lists O as a
replica of X after `+switch-master` and sends it `REPLICAOF X` once O has reported `master` for
8 s while X looks sane (`+convert-to-slave`,
[sentinel.c 9.1.1:2630-2641](https://github.com/valkey-io/valkey/blob/9.1.1/src/sentinel.c#L2630-L2641);
measured 16 s), which no veto reaches. The operator's `REPLICAOF` at `:3266` is live once
Sentinel no longer knows O: after the reset at `:3268` (`REMOVE`+`MONITOR X`, Sentinel learns
replicas from X's `INFO` alone), or with O unreachable to Sentinel. The delete of O is the
third. Meanwhile the labeler trusts Sentinel over the local role
([labeler.go:133-141](../../internal/sidecar/labeler.go#L133-L141)): O labels itself `replica`,
`-rw` selects the empty X alone and `-r` selects O, so every client write lands on X. The shape
itself is narrow: a forced failover promotes the replica with the highest offset
(`compareReplicasForPromotion`), so an empty one only when it is the only eligible replica or
all are empty; the other route is T35's promoted non-persistent pod restarting empty.

### The delete of the former master

Once Sentinel has converted O (5-20 s after the failover) O holds what X holds; O's count matters
while O still answers master (the refusal shape) or its link is down before the flush (up to
4-7 s). In the refusal shape any attach to X (a restarting pod, Sentinel reconfiguring a replica,
`forceReplicaConnections`) lets the gate pass on `DBSIZE 0` and O is deleted: non-persistent, its
memory is gone; persistent, it reboots as X's replica and full-syncs `/data` away. The roll ends
`RollingUpdateComplete`, phase `OK`, traced only by the log "verified with data ... dbsize 0".
Measured (500 keys on O, empty X with one replica): from +1 s every refusal branch of the gate is
false. ADR 0028 D1/D4 cover a demotion, not a delete.

### Write fencing

`generateValkeyConf` ([configmap.go:62-138](../../internal/builder/configmap.go)) writes no
`min-replicas` directive and the CRD has no field, so in a split both masters take writes and the
repair discards the loser's. [ADR 0028](../adr/0028-a-demotion-may-not-discard-the-only-dataset.md)
D3 lengthens that window on every refused demotion and names the missing fence as the price.

- **Semantics** (`min-replicas-to-write 1`, zero replicas): writes (`SET`, `DEL`, `EXPIRE`, `INCR`,
  `FLUSHALL`, ...) answer `NOREPLICAS`; `WAIT` (answers `0`), `REPLICAOF`, `PUBLISH`, `CONFIG SET`,
  `CLIENT KILL` and reads are not gated. A stalled link lets `max-lag` plus one cron tick of writes
  through (upstream source); `min-replicas-max-lag 0` switches the gate off silently. No help for the
  side that keeps a replica, nor against a replica full-syncing an empty master (T35).
- **Placement:** the shared tail of `replicationConfig` (`configmap.go:174-181`, gated at
  `:108-111`); both bodies feed `ComputeConfigHash`, so enabling rolls once.
- **Cost when opted in:** non-Sentinel `promoteAndRedirect` (`:4295-4355`) refuses writes
  0.23-0.25 s idle, 5.1-5.7 s plus the transfer under load; a forced Sentinel failover 5.7-6.9 s
  (nine runs); `replicas: 2` until the replacement joins. `sentinel.enabled` with `replicas: 1` is
  accepted ([valkey_types.go:1159-1161](../../api/v1/valkey_types.go)) and would be fenced for good
  behind PING-only checks reading `OK`. One pod down of two, two of three, or one stalled replica
  become write outages behind a healthy `-rw`, unexplained because `parseReplicationInfo` reads
  neither `slaveN` `state`/`lag` nor `min_slaves_good_slaves` (a sibling's 522 MB sync refused 0 of
  11 `SET`s on the survivor). The observer's `/readyz` fails only on a real refusal;
  `handleMasterWithNoReplicas` refuses every write for its 270 s.
- **Fixture:** `TestHandleMasterFailover_DoesNotFailOverWhenWriteSyncFails`
  ([sentinel_failover_test.go:579-581](../../internal/controller/sentinel_failover_test.go)) mocks
  `NOREPLICAS` for `WAIT`, which `WAIT` never returns; on a replica it returns `ERR WAIT cannot be
  used with replica instances. Please also note that if a replica is configured to be writable
  (which is not the default) writes to replicas are just local and are not propagated.`
- **E2E:** `valkey-cli --raw SET` under the gate exits 0 (T34);
  [sentinel_stale_master_test.go:204-206](../../test/e2e/sentinel_stale_master_test.go) would report
  `NOREPLICAS` with a misleading not-`READONLY` diagnosis.

## Required changes

**Order of the work** (also in ADR 0037's Status; the implementation happens in a session that
has only these files). Nothing of D1-D7 lands before step 0 ran on both legs.

0. The e2e writer harness (below), run on `single-node-valkey9` and `single-node-valkey8` against
   today's code; its counts go into the ADR 0037 Context as the Kubernetes measurement.
1. ADR 0037 D1 (Q1), with the fifteen rewordings (D8) and `upgrading.md`.
2. D2 and D3 (Q2) together with D6 and D7 (Q5): one gate, one hold, one registry row; the
   `Syncing`/`Ready` change and the fixtures.
3. D4 (Q3) and D5 (Q4), with the refusal-shape unit test and the comments.
4. ADR 0038 D4: the `WAIT` fixture and the e2e reply check.

Every step: `make test-unit`, `make lint`, `make cyclo`, `make test-integration`, full e2e on both
legs, the revert checks of ADR 0017, and the same change updates ADR 0037's Status ("Implemented:"
per rule) and its index row, the pages under *Documentation* below, and this ticket's
`state:`/`done:`. Cyclomatic complexity stays under 15; `verifyNewMasterReady` and
`replaceRemainingPods` are near it and split before they grow.

**Documentation that moves with the code:** [package-map.md:80](../developer/package-map.md#L80)
(the checker's contract), [reconcile-loop.md](../developer/reconcile-loop.md) ("The workload
pass" for the handover, "The status write" and the requeue table for phase `Syncing`;
DEVELOPER.md names none of the handover functions), [rolling-updates.md](../operations/rolling-updates.md)
("The former master is never force-promoted" gains the coordinated handover, the hold and the
repair), [status.md](../operations/status.md) (a `MasterHandoverStalled`
section, the `Ready`/`Syncing` meaning), [upgrading.md](../operations/upgrading.md) (the 8 -> 9
roll is forced, the 60 s block), [monitoring.md](../operations/monitoring.md) (`ValkeyPhaseNotOK`
covers a long sync; the condition series), the README condition row. CLAUDE.md needs Hans: the
"Rolling Update Strategy" step 4 ("controlled leader failover") and the "A Warning named
split-brain" paragraph ("Writes that reach the old master after the promotion are lost") gain the
ADR 0037 pointer and the forced-fallback scope, the ADR 0025 D9 line the coordinated note.

### Shared, or in one change across parts

- **E2E writer harness** (independent; needs T34's reply classification): during a Sentinel roll,
  write through `-rw`, count acknowledged-but-missing and refused writes separately, log X's `INFO`
  at the delete, detect a second failover (sidecar log "sentinel failover triggered" on the deleted
  pod, two `+switch-master`). Run on both legs before any fix; Q1 builds its e2e on it.
- **`verifyNewMasterReady`, one change:** the log line `:3488` drops "with data" and logs the count
  as a field (independent); returns the pod it verified; takes the replica-side check (Q2) and the
  dataset veto (Q4).
- **Pass-level refusal-shape test** (independent; run before any fix to prove it reproduces): two
  masters, Sentinel names the empty one, stamp older than 90 s; the holder gets no `REPLICAOF` (Q3)
  and, once X has a replica, is not deleted (Q4). One fake per pod with a key count (fleet helper
  `split_brain_dataset_test.go:51`).
- **ADRs (done 2026-09-28):** ADR 0037 and ADR 0038 written; ADR 0025 D9, ADR 0007 D1 and D10,
  ADR 0028 D1, D3, D4 and D8, ADR 0026 D11 amended in place, their residual risks on the gate
  closed by decision and marked open until built, the gate descriptions no longer carry the dead
  sync term, index rows added. The pre-existing T32 labels in the ADR 0007 and ADR 0026 residual
  risks are T40's sweep. Every code change updates the Status of ADR 0037 and its index row.
- **Comments (independent):** `:3087-3090`, `:3118` (the gate as it is); `:3090`, `:3110`, `:3472`
  cite ADR 0007 Residual risks instead of T32; `:4133-4134` (the Sentinel path reads one count, the
  manual path two); `:3296-3298`, `:3244`, `:3264`, `:1082` (every Ready pod except X);
  `:3250-3251`, `:3273-3274` and `sentinel_failover_test.go:1279-1281`, `:1300-1301` (the gate
  checks replication, not the dataset; T23 edits the same lines, one change); `checker.go:32`,
  `:38`, `checks.go:91`.
- Every code change: `make test-unit`, `make lint`, `make cyclo`, `make test-integration`; full e2e
  on `single-node-valkey9` and `single-node-valkey8`.

### The forced failover

- **Independent, XS, now:** reword to "the pre-roll dataset survives; on a Sentinel cluster the
  writes the outgoing master acknowledges during the roll's failover are lost (ADR 0025 D9)", no
  Kubernetes size: [README.md:46](../../README.md#L46), [CLAUDE.md:1068](../../CLAUDE.md#L1068)
  (needs Hans), [ADR 0005:357](../adr/0005-upgrade-neutral-defaults-and-anti-affinity.md#L357),
  [ADR 0018:100, :179](../adr/0018-metrics-and-the-exporter-sidecar.md#L100),
  [ADR 0032:366](../adr/0032-generated-pods-run-rootless.md#L366),
  [anti-affinity.md:16, :39](../operations/anti-affinity.md#L16),
  [monitoring.md:31](../operations/monitoring.md#L31), [upgrading.md:52, :161](../operations/upgrading.md#L52),
  [tls_rotation_test.go:219](../../test/e2e/tls_rotation_test.go#L219),
  [fleet_upgrade_test.go:22-23](../../test/e2e/fleet_upgrade_test.go#L22-L23),
  [rolling_update_test.go:489-490](../../test/e2e/rolling_update_test.go#L489-L490),
  [images.go:76-78](../../test/testimages/images.go#L76-L78). Out of scope: `persistence.md`,
  ADR 0023, ADR 0012 D9 (T40 item (b)), ADR 0016 `:261`, `rotation-and-change-propagation.md:47`.
  Check: `git grep -n -i 'lossless\|without data loss\|no data is lost\|loses no data\|zero data
  loss'` over `README.md`, `CLAUDE.md`, `docs/adr/`, `docs/operations/`, `test/` finds no such claim.
- **Q1 = A (decided 2026-09-28):** a coordinated call in `valkeyclient` beside
  `SentinelFailover`, which the retrigger (`:989`) and the drain handler
  ([drain.go:27-32](../../internal/sidecar/drain.go#L27-L32)) keep forced. The first trigger
  (`:2858`) sends `SENTINEL FAILOVER <name> COORDINATED`; on `ERR wrong number of arguments`
  (a Valkey 8 Sentinel: 8.1.9 rejects the fourth argument, 9.0.0 knows it) or `-NOGOODPRIMARY`
  it asks the same Sentinel with the forced command; `-INPROG`/`-NOGOODSLAVE` stay a failed
  attempt. The second fallback is the existing one: an `OK` whose election is lost
  (`-failover-abort-not-elected` after 10 s, no majority of the Sentinel table
  ([sentinel.c 9.1.1:5090-5110](https://github.com/valkey-io/valkey/blob/9.1.1/src/sentinel.c#L5090-L5110)))
  promotes nothing, `failoverRetryTimeout` (30 s) resets, and the retrigger fires forced,
  about 40 s later than today. On the master the coordinated path
  ([sentinel.c 9.1.1:4762-4800](https://github.com/valkey-io/valkey/blob/9.1.1/src/sentinel.c#L4762-L4800),
  [replication.c 9.1.1:5596-5760](https://github.com/valkey-io/valkey/blob/9.1.1/src/replication.c#L5596-L5760))
  sends `CLIENT PAUSE <rest of failover-timeout> WRITE` and `FAILOVER TO <X> <port> TIMEOUT
  <same>` in one transaction: O blocks writes, waits for X's ack offset, becomes X's replica,
  and the paused clients are disconnected unacknowledged. The stall shape (X not `online`,
  or never catching up) blocks writes for up to `failover-timeout` (60 s), then both sides
  abort; nothing is lost. The address chain holds by code: `replica-announce-ip` is the pod
  FQDN ([statefulset.go:355](../../internal/builder/statefulset.go#L355)), Sentinel announces
  hostnames ([sentinel.go:163](../../internal/builder/sentinel.go#L163)), `findReplica`
  compares that string and the listening port. The upgrade roll 8 -> 9 itself is forced: the
  Sentinel tier shares `spec.image` and rolls after the data tier, so the first lossless roll
  is the next one. [ADR 0037](../adr/0037-the-master-handover-loses-no-acknowledged-write-and-no-dataset.md)
  D1 (written 2026-09-28, amending ADR 0025 D9 and ADR 0007 D1); the fifteen places reworded
  again; `upgrading.md` names the 8 -> 9 roll and the 60 s block. Implementation notes: the
  fallback sits inside `triggerSentinelFailover`'s per-Sentinel loop, so the Sentinel that
  refused the option is the one asked forced; `valkeyclient.Client` gains
  `SentinelFailoverCoordinated(name)` beside `SentinelFailover`, and the sidecar's
  `ValkeyCommander` interface ([drain.go:27-32](../../internal/sidecar/drain.go#L27-L32)) is not
  widened. The client wraps every error (`sentinel failover %s on %s: %w`), so the classification
  reads the reply text: `ERR wrong number of arguments for 'sentinel|failover' command` (8.1.9,
  measured), `NOGOODPRIMARY Primary does not support FAILOVER command`, `INPROG Failover already
  in progress`, `NOGOODSLAVE No suitable replica to promote`, `Unknown failover option specified`
  (a 9.x Sentinel given a wrong option; treated as the 8.x reply) — all from
  [sentinel.c 9.1.1:3923-3960](https://github.com/valkey-io/valkey/blob/9.1.1/src/sentinel.c#L3923-L3960);
  match on the leading token, the client may or may not keep the `-`. The trigger logs
  `failoverMode` (`coordinated`/`forced`) and `fallbackReason` with those stable keys, which the
  e2e on Valkey 8 asserts on; a second `COORDINATED` attempt is never made in the same pass. Unit (RESP fake)
  per reply, plus forced retrigger and drain handler, with revert checks
  ([ADR 0017](../adr/0017-test-and-ci-policy.md)); e2e: 0 lost on Valkey 9, non-zero with the
  argument reverted, Valkey 8 logs its count and the fallback, zero Warning Events on a clean
  roll.

### The sync checks

- **Independent:** rewrite the five replica fixtures with link `up` and sync `true`
  (`rolling_update_test.go:3792-3798`, `:4014-4020`,
  [topology_restore_stall_test.go:143-150](../../internal/controller/topology_restore_stall_test.go#L143-L150),
  [drain_test.go:305-311, :700-706](../../internal/sidecar/drain_test.go#L700-L706)) to link `down`
  with sync `1`. Replace "Wait for TLS replication sync"
  ([tls_test.go:1301-1311](../../test/e2e/tls_test.go#L1301-L1311), cannot fail) with a
  replica-side TLS wait via `valkeyTLSExecAllowError`; run on both lines.
- **Q2 = A (decided 2026-09-28):** move the predicate of `replicationNotEstablishedReason`
  onto `ReplicationInfo` — one method, `NotEstablishedReason() string`, `""` when the pod is a
  replica with `master_link_status` `up` and no sync in progress, the reason otherwise;
  `replicationNotEstablishedReason(podName, info)` keeps its signature and prefixes the pod name,
  `isSyncedReplica` is `info.NotEstablishedReason() == ""` — and drop the message at `:4580`.
  `findMaster` hands `CheckCluster` every reply indexed by ordinal (the `Checker` API keeps its
  shape; `ClusterState` may carry the per-pod reasons for the status message);
  `ReadyReplicas` is the count of non-master pods with an empty reason, `AllSynced` is
  `ReadyReplicas == TotalReplicas`, the `Syncing` message stays "Replication syncing: %d/%d
  replicas ready".
  `CheckCluster` evaluates it on the replies `findMaster` already collects
  ([checker.go:174-199](../../internal/health/checker.go#L174-L199) keeps master candidates
  only; keep every reply, indexed by ordinal), no extra dial, and `ReadyReplicas` counts the
  pods that pass. The observer asks each data pod, one `INFO` per replica per cycle. At the
  gate, `verifyNewMasterReady` asks the predicate of every other pod on the current template
  that exists and is not terminating **and** requires X's `connected_slaves` at least equal to
  their number: the count on X proves the attachment, the predicate the dataset, neither alone
  (a replica chained through O passes the predicate, `parallel-syncs 1`; `master_host` is not
  compared, the init script writes two FQDN forms,
  [statefulset.go:285](../../internal/builder/statefulset.go#L285),
  [:445](../../internal/builder/statefulset.go#L445)). The gate arms the sync-wait bound; on
  expiry it hands over as Q5 decides, never to `pauseRollingUpdate` (`:2726-2760`), which clears
  the state under `failover-triggered` against ADR 0010 and releases the Sentinel roll.
  `replicas: 2` is unchanged: O is X's only replica after the conversion, no other pod is asked,
  the `connected_slaves == 0` refusal stays. Rewrite the three master fixtures
  (`sentinel_failover_test.go:1001-1011`,
  [checker_live_test.go:293-298](../../internal/health/checker_live_test.go#L293-L298),
  [checks_endpoint_test.go:73-79](../../internal/observer/checks_endpoint_test.go#L73-L79), fake
  node `fake_endpoint_test.go:301-307`) replica-side. Tests: X at `connected_slaves:1` with the
  other replica link `down` refuses the delete and arms the sync-wait bound; X at
  `connected_slaves:0` with the other replica link `up` (chained) refuses; `CheckCluster` and
  `checkReplicaSync` fail on a Ready replica in full sync; reverting each site turns it red.
  Update [package-map.md:80](../developer/package-map.md); the README `Syncing` row and the
  `Ready` text in [status.md](../operations/status.md) already read right;
  [monitoring.md](../operations/monitoring.md) gains that `ValkeyPhaseNotOK` (`for: 30m`) now
  covers a sync longer than that.
- The only non-test user of `MasterSyncInProgress` is the parser and the predicate.

### Re-pointing and the delete

- **Q3 = B (decided 2026-09-28):** the veto inside `forceReplicaConnections`, reusing
  `dbSizeReader` and `demotionRefusalReason` unchanged, covering both callers. X empty or
  unreadable: count every other existing pod, non-Ready included; any holder or unreadable
  count sends nothing and logs once who holds what; no Event (ADR 0028 D6). The reset at
  `:3268` still runs after a vetoed call (T23 decides the reset). Unit: X with keys or both
  empty re-point all; X empty with a holder, a non-Ready holder, or an unreadable count
  re-point none (fail with the veto removed; per-target and targets-only mutations fail the
  mixed and non-Ready cases). Give
  `TestHandleMasterWithNoReplicas_ForcesReconnectAndResetsSentinelOnTimeout` key counts.
- **Independent:** rename `TestVerifyNewMasterReady_AcceptsAMasterWithReplicasAndData`
  ([sentinel_failover_test.go:1063](../../internal/controller/sentinel_failover_test.go#L1063)) and
  its message to what it pins, X's count being read; add a `DBSIZE 0` test pinning today's
  acceptance, the refusal test under Q4 = A.
- **Q4 = A (decided 2026-09-28):** in `replaceRemainingPods`, after the gate and before the D5
  gate, two preconditions on the pod about to be deleted, whichever outdated pod the loop
  reached, on both paths (the gate is Sentinel-only, the delete is not; on the non-Sentinel path
  the current master is the pod flagged `isMaster` on the current template, where
  `verifyPromotionCandidateHoldsData` refused before the promotion and the veto catches only a
  master that lost its data since, T35). First `demotionRefusalReason(dbSizeReader(ctx, v), X,
  pod)` unchanged: X with keys or both empty, deleted; X empty and the pod with keys or
  unreadable, X unreadable, held. X is the pod `verifyNewMasterReady` returned on the Sentinel
  path and the pod flagged `isMaster` on the current template otherwise; no such pod is
  "X unreadable", held (fail closed, ADR 0028 D3). The role is read with
  `checker.GetReplicationInfo(pod)`: `role:master` holds (`FormerMasterStillMaster`); an error
  is "no answer" and passes the role check, and the veto's `DBSIZE` then decides. Second the role: the pod is deleted only when it does not
  answer `master` (a replica, or no answer); on Valkey 9 with Q1 = A that costs nothing, on the
  forced path it holds the delete until Sentinel has converted O (~16 s plus one requeue) and
  the drain handler's second failover has no master to act on, and the comment at `:3126`
  becomes true. Both as AND: the veto carries the dataset reason and holds on an unreadable O,
  the role holds a full X for the first seconds. What the hold reports and hands over to is Q5.
  ADR 0028 D8 is amended: on the Sentinel path the delete no longer ends the refusal, it holds
  it, bounded by Q5. Cost of the hold: O behind `-r` with the dataset, X empty behind `-rw`,
  until a human or the first write on X (Q3); a non-Ready O is held while X is empty.
- **Q5 = C (decided 2026-09-28):** `MasterHandoverStalled` in `api/v1`, an edge with one
  evaluator (the gate) and reasons `ReplicaNotSynced`, `DatasetWouldBeDiscarded`,
  `FormerMasterStillMaster`; the message names X, the held pod and the counts or the sync
  reason, and the repair: point Sentinel at O (`SENTINEL REMOVE`/`MONITOR O`), after which the
  resolver may demote the empty X (ADR 0028 D1), X syncs from O, the roll finds O outdated and
  hands over coordinated. Set once the gate's wait outlived `syncTimeout`, with one Warning
  Event; from then `DeferredRequeueAfter` as `terminationWait` (`:2163-2183`): state kept,
  Sentinel roll held (`dataTierHolding`), the pass continues to the split-brain check and the
  status write. Phase without override: health-derived, `Syncing` and
  `Ready=False/ReplicationSyncing` in every hold shape under Q2 = A (the second master is not a
  synced replica); a second phase override beside `ReconcileBlocked` would be an ADR 0002
  amendment for a shape `MultipleMasters`, `SplitBrainDetected` and this condition carry.
  Cleared presence-guarded where the delete goes through and in `clearRollingUpdateState`;
  `conditionRegistry` row (`kind: conditionEdge, evaluators: 1, clearSite: "clearMasterHandoverStalled,
  from the delete that goes through in replaceRemainingPods and from clearRollingUpdateState",
  presenceGuarded: true`), constants `ConditionTypeMasterHandoverStalled` and the three reasons
  in `api/v1`, the Event reason `MasterHandoverStalled`, the requeue
  `DeferredRequeueAfter: rollingUpdateRequeueDelay`; the bound is the existing pair
  `ensureSyncWaitTimestamp`/`isSyncWaitTimedOut`, and the gate clears it
  (`clearSyncWaitTimestamp`) in the pass the delete goes through — `verifyReplacedReplicasSynced`
  clears it at its own end, so the gate must arm its own (see *Not verified*). README condition
  row, [status.md](../operations/status.md) with the repair,
  [rolling-updates.md](../operations/rolling-updates.md). The condition exports as
  `vko_valkey_status_condition{condition="MasterHandoverStalled"}` (ADR 0021); a chart alert
  row follows T23 Q2. Test past `syncTimeout`: state kept, condition True with the reason of
  the cause, no `RollingUpdatePaused`, no delete, `DeferredRequeueAfter` without
  `NeedsRequeue`, Sentinel roll held, one Warning Event; O dropping to 0 keys, O answering
  replica, or the replica syncing gives the delete and the presence-guarded clear.

### Write fencing

- **Independent:** the `WAIT` fixture above gets the real `ERR WAIT cannot be used with replica
  instances. ...` reply and a comment that any error reply blocks the promotion; assertion unchanged.
- **Q6 = refuse (decided 2026-09-28):**
  [ADR 0038](../adr/0038-the-operator-does-not-offer-min-replicas-to-write.md) (written the
  same day) carries the measurements, what the fence protects and what it does not, option 1's
  design with the admission-or-runtime question as the alternative, and the three re-open
  triggers. The ADR citations of this ticket are rewritten; the one left is the comment at
  [pod_termination_test.go:257](../../internal/controller/pod_termination_test.go#L257), which
  cites ADR 0038 instead, until `git grep -n 'T12\|012-the-master-handover' -- ':!docs/tickets'`
  is empty.

## Open questions

### Q1: How should the roll's own Sentinel failover stop the outgoing master from acknowledging writes that are later discarded? (forced failover)

Today O acknowledges writes for about 16 s after the promotion, all lost. No option changes
crash failovers, the non-Sentinel path, the drain handler, pod templates or the CRD.

- **A - `COORDINATED` on Valkey 9, forced as fallback (chosen).** M. 0 lost in eight runs, and
  the second failover vanishes on Valkey 9 because O is a replica at once. The stall shape
  blocks writes on O for up to `failover-timeout` (60 s; `CLIENT PAUSE WRITE` blocks, it does
  not refuse), then aborts; a lost election costs the roll about 40 s through the existing
  retry; Valkey 8 Sentinels, the 8 -> 9 upgrade roll included, keep today's loss.
- **B - runtime `CONFIG SET min-replicas-to-write 1` on O before each trigger.** M. Both lines,
  still about 500 lost, the second failover untouched, a write to clear wherever O stays master;
  Sentinel's `CONFIG REWRITE` persists it, so a later crash failover can promote a pod refusing
  every write.
- **C - keep the loss and record its size.** S. Nothing changes at runtime.
- **D - `FAILOVER TO <X>` from the operator straight to O, on both lines.** Rejected: O then
  reports `role:slave`, Sentinel treats a master reporting replica as unreachable, marks it down
  after 5 s and runs its own failover with its own selection, and converts an X reporting master
  back as soon as O looks sane again. The non-Sentinel promotion fought against Sentinel, timing
  unverified.

**Answer:** A, 2026-09-28. 0 lost by one argument on a command already sent, no state to clear,
and every fallback is exactly today's command: the error replies fall back at once, an `OK`
whose election is lost falls back through `failoverRetryTimeout`. The price is 60 s of blocked
writes in the stall shape instead of lost ones, and a roll that depends on ADR 0022's clean
Sentinel table for the first time. Design and verification under Required changes.

### Q2: Which signal answers "every replica holds the dataset" at `CheckCluster`, the observer and `verifyNewMasterReady`? (sync checks)

All three count attached replicas on the master, empty ones included. Not touched: the ADR 0007 D10
promotion gates, the `replicas: 2` shape, the dataset veto (Q4).

- **A - the replicas' full replication answer at all three sites (chosen).** M. The roll waits
  for the resync before deleting O; `Syncing` and `Ready=False/ReplicationSyncing` during every
  replica full sync, after an eviction or a restart too, minutes on a large dataset;
  `ValkeyPhaseNotOK` (`for: 30m`) fires only on a sync longer than that, which today never
  fires.
- **D - A at the roll gate and the observer, C at `CheckCluster`.** Protects the delete and keeps
  `Ready` quiet by reading `True` about an empty replica.
- **C - delete the terms, correct the records.** S, no behaviour change, the delete stays
  unguarded.

**Answer:** A, 2026-09-28. One predicate, `replicationNotEstablishedReason`, at every site that
says "synced", and it is the one ADR 0007 D10 already prescribes; the gate in front of the one
irreversible step gets the bounded wait the promotion gates have, at no extra dial; `Ready`
becomes what [status.md](../operations/status.md) already states ("replicating": a replica in
full sync is not, and `-r` routes reads to it because the probe asks `PING` only), and the CRD
description of `replicaSyncFailure` becomes true. No opt-in (ADR 0005 D1): a status defect, not a
feature. Q2 decides the predicate; what the gate's wait hands over to on expiry is Q5, and the
gate lands with it. Design under Required changes.

### Q3: What does `forceReplicaConnections` do when X holds no keys and a pod it would re-point holds some? (re-pointing)

Decides whether ADR 0028's dataset rule binds this second `REPLICAOF` site. An X with keys still
re-points everyone; a refused shape holds as two visible masters (`MultipleMasters`,
`SplitBrainDetected` after 90 s), as ADR 0028 D3 and D8 accept.

- **A - veto per target.** XS-S. An empty target still attaches, X gains a replica, the gate
  passes on `connected_slaves >= 1` and under Q2 = A on "synced" to an empty master; only Q4
  stands before the holder's delete, and an X with a replica satisfies any write fence (Q6).
- **B - veto the whole call (chosen).** S. X empty or unreadable: count every other existing
  pod, non-Ready included; any holder or unreadable count sends nothing and logs once who holds
  what. X stays replica-less: the gate refuses on `connected_slaves == 0` as today, without Q4;
  the no-replica branch cycles every 90 s (T23's cap), `MultipleMasters` at every boundary
  pass, `SplitBrainDetected` after 90 s.

**Answer:** B, 2026-09-28. The same rule as ADR 0028 D1 at the second `REPLICAOF` site,
fail-closed as D3; D1/D4 name the site, D3's "the divergence is bounded" no longer holds on the
Sentinel path. Its honest size: B stops the flush by the operator, which is the only one left
once Sentinel has forgotten O; the common route runs through Sentinel's own conversion 16 s after
an empty promotion, and the fix for that lies before Q3: Q1 = A's coordinated first trigger
cannot promote an empty X, the forced retrigger can, and its missing gates are T23's (its
"T12's route"). The veto holds until the first `-rw` write reaches X, one cycle on a cluster with
traffic, until a human on an idle one; its complement is a write fence on a replica-less master
(Q6), which B composes with and A would defeat.

### Q4: What, beyond an attached replica, must hold before O is deleted on the Sentinel path? (delete)

`:3141` is the last point where a data-holding O can be kept; the veto needs O's count only when X
is empty.

- **A - veto at this delete (chosen).** S. Refuse when X is empty and the pod about to be deleted
  holds keys or a count is unreadable, and while that pod still answers `master`. The two
  masters of ADR 0028 D3 then last unbounded on this path, X alone behind `-rw` and O behind
  `-r` (the labeler trusts Sentinel), until Sentinel or a human resolves them or the first write
  on X lifts the veto; a non-Ready O is held while X is empty.
- **B - veto at every roll delete** (`replaceNextReplica`, `deleteNextPendingPod` too). M. A master
  `DBSIZE` before every replica delete, and no shape in which deleting a replica discards the
  only dataset: a replica holding data behind an empty master is flushed by it at the next link,
  and only a promotion saves it, not a skipped delete.

**Answer:** A, 2026-09-28. The veto guards the pod at risk at the site ADR 0028 D8 names, for
whichever outdated pod the loop reached (B's concern without B's cost), on both paths for free;
the role precondition at the same delete removes the forced path's second failover and makes the
comment at `:3126` true. Q4 decides the predicates; the hold's report and hand-over are Q5.

### Q5: What does a refused delete report and hand over to? (delete)

ADR 0010 bounds every roll wait and forbids handing expiry to a cleared state. Since Q2 and Q4
the gate holds for three reasons (a replica not synced, the dataset veto, the outgoing pod still
`master`), all in `failover-triggered`, clocked by the sync-wait bound
(`ensureSyncWaitTimestamp`, `syncTimeout`, default 5 min). Before the bound a plain requeue.
The code has two shapes past a bound: the pause (`pauseRollingUpdate`, `:2726-2760`: state
cleared, Sentinel roll released, repeats every `syncTimeout`) and the hold (`terminationWait`,
`recreationWait`, `availabilityWait`: state kept, `DeferredRequeueAfter`, a condition, the pass
continues). A hold is only as safe as Q3 = B, since the state's other arm calls
`forceReplicaConnections`.

- **A - pause like the manual path** (`waitOrPauseForReplicaSync`). XS. Clears the state against
  ADR 0010, releases the Sentinel roll onto the spec the data tier is stuck on, and repeats: with O
  still `master` the resolver refuses, O keeps `isMaster`, `replaceNextReplica` skips it,
  `waitForReplicasReady` blocks, a Warning and phase `Error` every `syncTimeout` (T23).
- **B - `RollingUpdatePaused` with a new reason.** S. The condition then means both "cleared and
  retrying" and "held, never retrying", while T23 Q1/Q2 re-decide what a pause leaves behind and
  whether the chart alerts on it.
- **C - its own edge condition (chosen).** S. One condition for the handover hold, a reason per
  cause.

**Answer:** C, 2026-09-28. `MasterHandoverStalled`, in the `...Stalled` family, the
`terminationWait` shape: edge, one evaluator, three reasons, `DeferredRequeueAfter` past
`syncTimeout`, state and Sentinel roll held, one Warning at the set (this hold replaces a pause
at this gate and every pause emits one; the three `...Stalled` are silent because their subject
shows in `kubectl get pods`, a dataset veto shows nowhere else), phase health-derived without an
override, cleared at the delete that goes through and in `clearRollingUpdateState`, the repair
in the message and in `status.md`. A precedent for T23 Q1: a hold that keeps its state. Design
under Required changes.

### Q6: Does the operator offer `min-replicas-to-write` as an opt-in CRD field, or refuse it in an ADR? (write fencing)

A fence stops the replica-less side of a split from taking writes, but refuses writes at every
failover with full-resyncing replicas and turns degraded states into unexplained write outages.
A generic `spec.extraConfig` is no option (it would expose `replicaof`, `save`, TLS). What the
fence still protects after Q1-Q5: the Valkey 8 forced-failover residual on a tier of three or
more (16 s to about 1-2 s, since Sentinel re-points the third replica within seconds and O then
refuses; a 2-pod tier cannot be fenced under `minReplicas <= replicas - 2`); the Q3/Q4 hold,
where a replica-less X refuses writes, stays empty and keeps the veto standing until a human
instead of one cycle; the steady-state splits of ADR 0028 D3. What it costs when opted in: at
least 5 s of refused writes per controlled failover on Valkey 8 Sentinel clusters and on the
non-Sentinel path under load, an unmeasured few seconds of `NOREPLICAS` after a coordinated
failover (X has no good replica until O and R attach with lag under `max-lag`), degraded states
as write outages the status cannot explain without parsing `slaveN` lag and
`min_slaves_good_slaves`, a stalled link leaking `max-lag` plus one cron tick, `max-lag 0`
switching off silently. No protection against T35's flush (a full sync is not a write) nor for
the side that keeps a replica.

- **Option 1 - build `spec.writeFencing`.** L. CRD field, admission or runtime refusal, render,
  parser, condition, observer, docs, e2e under the writer harness on both legs, plus the
  recurring cost above for every cluster that opts in.
- **Option 2 - refuse it in an ADR (chosen).** S. Nothing changes at runtime.

**Answer:** Option 2, 2026-09-28. Q1 = A removes the loss on Valkey 9; what remains for a fence
is a residual on a line the upgrade path leaves and that no cluster keeps past its first roll
under a Valkey 9 Sentinel, a narrow shape Q5 now reports with its repair and in which Q3 = B
keeps the operator itself from the flush, and ADR 0028's accepted windows. Against that,
seconds of `NOREPLICAS` on every failover for every opted-in cluster and an L nobody asked for;
ADR 0005 D1 makes a feature opt-in, built and kept it has to be regardless. ADR 0038 records the
measurements, the limits, option 1's design (the admission-or-runtime question of the former Q7
included) and three re-open triggers: a user on Valkey 8 with `replicas >= 3` who cannot move
to 9; `MasterHandoverStalled/DatasetWouldBeDiscarded` seen in the field; a non-Sentinel split
diverging beyond ADR 0028's bound. Q7 (admission or runtime) is closed with it and lives in
that ADR's alternative.

## Not verified

- Every part on Kubernetes (write rate, `-rw` lag, pooled connections, whether the delete pass lands
  inside a resync): the writer harness settles it.
- The second failover on Kubernetes and 8.1.9, and whether the sidecar's role read beats the Valkey
  container's SIGTERM (no `preStop` on Sentinel clusters,
  [statefulset.go:746-749](../../internal/builder/statefulset.go#L746-L749)).
- Q1 = A on Kubernetes: the address chain, TLS replication, three Sentinels with an election,
  two Sentinels, no majority, a stalled `FAILOVER TO`; the docker runs are the only measurement.
- What a replica in `wait_bgsave` serves, and what Sentinel promotes if X fails then.
- With Q2 = A, whether the sync-wait annotation is clear on entering `replaceRemainingPods`;
  how long real full syncs take on a fleet, so how long `Ready` reads `ReplicationSyncing`;
  whether the third replica resyncs partially after a coordinated failover (read from PSYNC2:
  O paused before the handover and X caught up; the gate needs the check for a mid-roll
  eviction or restart regardless).
- The re-pointing and delete paths end to end (read only); how often X is empty while O holds data;
  persistence on a PVC (docker only).
- The `status.phase` of a held pass is health-derived by design (Q5): `Syncing` in every hold
  shape under Q2 = A, read off `updateHAStatus`
  ([valkey_controller.go:2427](../../internal/controller/valkey_controller.go#L2427)), not
  measured. The repair path of `MasterHandoverStalled` (Sentinel pointed at O, X demoted, roll
  resumed) is read along the resolver, the roll and Q1 = A, not driven. Whether the non-Sentinel
  `replaceRemainingPods` could delete a holder behind an empty master is not measured; with
  Q4 = A the veto runs on that path too.
- Fencing: survivor lag measured once on 9.1.1; whether the diverging side in ADR 0028's windows is
  the replica-less one; how long `NOREPLICAS` lasts after a coordinated failover.

## Related

- T34: the e2e reply check the writer harness and Q6 option 1 need.
- T40: item (b), the non-Sentinel counterpart of the lossless-text rewording.
- T35: shortens how long `-rw` routes to O; its flag-aware `-rw` fence was left here, no option takes it up.
- T23: `resetSentinelState` at `:3268` resets toward X without asking for keys; Q1 = A's fallback runs through it.
- T23: the uncapped reset-and-retrigger cycle, a route to an empty X; edits the same comment lines.
- T35: a non-persistent promoted pod restarting empty; fencing does not protect against its flush.
- T35: the probe passing on `LOADING`.
- T23: rests on `AllSynced` counting a mid-sync replica; its Q1 (what a pause leaves behind) has
  this ticket's Q5 hold as a precedent, its Q2 decides the alert row for `MasterHandoverStalled`.
- T18: its option B assumes `True/HAClusterReady` between replacements, false with Q2 = A.
- T40: counts this ticket's citations outside `docs/tickets/` and the three T32 citations.
- Origin: [archive/039](archive/039-findings-from-the-1-11-0-fleet-rollout.md) (T4/T11 analysis).
