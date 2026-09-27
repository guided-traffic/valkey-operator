---
id: T12
title: no acknowledged write and no dataset is lost across the rolling update's master handover
state: analysed       # every part read and measured, options costed, no option chosen
severity: high        # acknowledged writes lost on every Sentinel roll; the only dataset deleted in the refusal shape with phase OK
security: none        # durability and data integrity; no principal gains a verb or an object
urgency: now          # rule 1: tracked texts call every multi-replica roll lossless and describe sync and dataset gates the code does not have
effort: L             # three code changes on one handover path, one e2e writer harness, ADR amendments; fencing as a refusal ADR
blocked-by: decision  # Q1-Q5; Q6 is a product call and waits for Q1
filed-from: T4 analysis, 2026-08-24
opened: 2026-08-24
decided:
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

- **Trigger.** `handleMasterFailover` (`:2695-2758`) waits for the replicas, sends `WAIT`, stamps
  `setFailoverTriggered` (`:2743`), triggers (`:2751`), requeues after 15 s. The retrigger
  `handleFailoverRetrigger` (`:849-887`, trigger `:882`) skips the replica and write-sync gates
  (`:2710-2719`). Both use `triggerSentinelFailover` (`:3700-3740`): each Sentinel in ordinal order,
  plain `SENTINEL FAILOVER <name>` ([client.go:231-238](../../internal/valkeyclient/client.go#L231-L238)).
- **No-replica branch.** `handleMasterWithNoReplicas` (`:3145`) tolerates a zero-replica X for
  about 270 s (90 s `replicaReconnectTimeout`, re-armed twice); past 90 s (`:3150`) it calls
  `forceReplicaConnections` (`:3159`) and `resetSentinelState` (`:3161`).
- **Delete.** `handleNewMasterFound` (`:3129`) hands over to `replaceRemainingPods`
  (`:2984-3051`): `verifyNewMasterReady` (Sentinel only, `:3012-3017`), the ADR 0026 D5 gate,
  `replacing-master` (`:3029`), delete of O (`:3034`), at the 15 s requeue or earlier on a watch
  event. `finalizeRollingUpdate` (`:891`, `:946-989`) reads no key count.
- **The gate.** `verifyNewMasterReady` (`:3331-3397`) takes the first current `available()` pod
  answering `role:master` as X and refuses only on `connected_slaves == 0` (`:3350`), an
  unreadable TLS config or `DBSIZE` of X (`:3368-3379`), or no non-terminating candidate. Its
  `master_sync_in_progress` term (`:3355`) never fires. On any readable count, zero included, it
  logs "New master verified with data" (`:3381`); it never reads O's count; its other waits are
  unbounded plain requeues.
- **The ADR 0028 refusal shape.** The resolver runs before every dispatch (`:714-715`) with
  Sentinel's master pointer as authority, except while `ownFailoverInFlight` (`:797-822`), the
  negation of the predicate gating `:3159`. `demotionRefusalReason` (`:1713-1736`, counts via
  `dbSizeReader` `:1681-1693`) refuses when the authority holds zero keys and the rogue some, or a
  count is unreadable; the refusal lives only on the pod slice `handlePostFailover` discards
  (`:3071`). X is returned as `masterIdx` (`:1478`), O keeps `isMaster` (`:1659-1663`). ADR 0028 D8
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

**Second failover.** Nothing asks O its role before the delete (the comment at `:3034` says "now a
replica", true only after `+convert-to-slave`). The delete SIGTERMs O's sidecar, whose drain handler
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
| `verifyNewMasterReady` `:3355` | "all replicas synced", "no sync in progress" | `connected_slaves > 0` |

The replica-side answer exists: `replicationNotEstablishedReason` (`:4467-4476`: role, link `up`, no
transfer), applied by `verifyReplacedReplicasSynced` (`:2536-2603`, bounded by
`spec.rollingUpdate.syncTimeout`, pause on expiry) and asked by the sidecar's `isSyncedReplica`
([drain.go:351-353](../../internal/sidecar/drain.go#L351-L353)). ADR 0007 D10 requires it before a
promotion; nothing asks it before the delete. Its "still syncing" message (`:4472-4474`) is
unreachable. Eight unit fixtures and one e2e subtest pin impossible replies.

**Impact.** On a Sentinel roll with `replicas >= 3` O is deleted while the other replica is in
`wait_bgsave` or loading; a second fault then loses the writes since the promotion (non-persistent:
the dataset). After a full sync the status reads `OK`, `Ready=True/HAClusterReady` while a replica
is empty; a sync restarting forever never fires `ValkeyPhaseNotOK`; `replica_sync` never goes red.

### Re-pointing pods at the new master

`forceReplicaConnections` (`:3192`; callers `:983` and `:3159`) sends `REPLICAOF X` to every existing
Ready pod except X, terminating ones included; it asks no role, reads no key count, and skips a
failed send. At `:3159` it performs exactly the demotion of O the resolver refused, and re-points
every replica holding O's copy; likewise when a count was unreadable, when O was not counted master,
or when the 90 s boundary falls between `:797` and `:3150`. Only "authority is O" does not reach
it. Measured: a 500-key master and its replica re-pointed at an empty X are empty by +7 s; O with
the operator's `rdb` or `aof` lines ([configmap.go:187-246](../../internal/builder/configmap.go#L187-L246))
re-pointed and restarted boots master with `DBSIZE` 0 in four of four runs (the sync replaces the
files). Comments, test texts and ADR 0028 Residual risks `:284` call the path safe.

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
- **Cost when opted in:** non-Sentinel `promoteAndRedirect` (`:4188-4248`) refuses writes
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

### Shared, or in one change across parts

- **E2E writer harness** (independent; needs T34's reply classification): during a Sentinel roll,
  write through `-rw`, count acknowledged-but-missing and refused writes separately, log X's `INFO`
  at the delete, detect a second failover (sidecar log "sentinel failover triggered" on the deleted
  pod, two `+switch-master`). Run on both legs before any fix; Q1 and Q6 build their e2e on it.
- **`verifyNewMasterReady`, one change:** the log line `:3381` drops "with data" and logs the count
  as a field (independent); returns the pod it verified; takes the replica-side check (Q2) and the
  dataset veto (Q4).
- **Pass-level refusal-shape test** (independent; run before any fix to prove it reproduces): two
  masters, Sentinel names the empty one, stamp older than 90 s; the holder gets no `REPLICAOF` (Q3)
  and, once X has a replica, is not deleted (Q4). One fake per pod with a key count (fleet helper
  `split_brain_dataset_test.go:51`).
- **ADR 0028, one amendment (Q3, Q4):** D1/D4 name the second `REPLICAOF` site and the delete; D8
  and ADR 0025 `:215-217` say "subject to the dataset veto"; D3's "the divergence is bounded" no
  longer holds on the Sentinel path. Independent: mark Residual risks `:284` in place as not holding
  at `handleMasterWithNoReplicas`.
- **ADR 0007, ADR 0026 (independent):** ADR 0007 `:517-519`, ADR 0026 `:475-476`, `:790-791` state
  the gate as role, available and one attached replica; ADR 0007 Residual risks `:523-526` names the
  texts of this ticket instead of three comments; fix ADR 0007 `:392-393`, ADR 0026 `:787-789`.
  Depending on Q2/Q4: ADR 0007 D10 covers the delete, ADR 0026 D11's *Replacement* argument
  (`:473-477`, `:791-792`) is amended, both residual risks close. No ADR edit cites a ticket.
- **Comments (independent):** `:2980-2983`, `:3011` (the gate as it is); `:2983`, `:3003`, `:3365`
  cite ADR 0007 Residual risks instead of T32; `:4026-4027` (the Sentinel path reads one count, the
  manual path two); `:3189-3191`, `:3137`, `:3157`, `:975` (every Ready pod except X);
  `:3143-3144`, `:3166-3167` and `sentinel_failover_test.go:1279-1281`, `:1300-1301` (the gate
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
- **(Q1 = A):** a coordinated call in `valkeyclient` beside `SentinelFailover`, which the drain
  handler keeps ([drain.go:27-32](../../internal/sidecar/drain.go#L27-L32)). The first trigger
  (`:2751`) sends `COORDINATED`; on the Valkey 8 `ERR wrong number of arguments` or `-NOGOODPRIMARY`
  it asks the same Sentinel with the forced command; `-INPROG`/`-NOGOODSLAVE` stay a failed attempt.
  The retrigger (`:882`) stays forced, so a failover without a majority falls back through the 30 s
  `failoverRetryTimeout`. New ADR amending ADR 0025 D9 and ADR 0007, index row, the fifteen places
  reworded again. Unit (RESP fake) per reply, plus forced retrigger and drain handler, with revert
  checks ([ADR 0017](../adr/0017-test-and-ci-policy.md)); e2e: 0 lost on Valkey 9, non-zero with the
  argument reverted, Valkey 8 logs its count and the fallback, zero Warning Events on a clean roll.
- **(Q1 = C):** amend ADR 0025 D9 with the sizes and the Kind measurement as revisit trigger.

### The sync checks

- **Independent:** rewrite the five replica fixtures with link `up` and sync `true`
  (`rolling_update_test.go:3792-3798`, `:4014-4020`,
  [topology_restore_stall_test.go:143-150](../../internal/controller/topology_restore_stall_test.go#L143-L150),
  [drain_test.go:305-311, :700-706](../../internal/sidecar/drain_test.go#L700-L706)) to link `down`
  with sync `1`. Replace "Wait for TLS replication sync"
  ([tls_test.go:1301-1311](../../test/e2e/tls_test.go#L1301-L1311), cannot fail) with a
  replica-side TLS wait via `valkeyTLSExecAllowError`; run on both lines.
- **(Q2 = A):** move the predicate of `replicationNotEstablishedReason` onto `ReplicationInfo`,
  build it and `isSyncedReplica` on that, drop the message at `:4473`. `CheckCluster` counts full
  answers from the replies `findMaster` already collects; `verifyNewMasterReady` calls
  `verifyReplacedReplicasSynced` with the same pods; the observer asks each data pod. Rewrite the
  three master fixtures (`sentinel_failover_test.go:1001-1011`,
  [checker_live_test.go:293-298](../../internal/health/checker_live_test.go#L293-L298),
  [checks_endpoint_test.go:73-79](../../internal/observer/checks_endpoint_test.go#L73-L79), fake node
  `fake_endpoint_test.go:301-307`) replica-side. Tests: X at `connected_slaves:1` with the other
  replica link `down` refuses the delete, arms the sync-wait bound, pauses past `syncTimeout`;
  `CheckCluster` and `checkReplicaSync` fail on that shape; reverting each site turns it red.
  Update [package-map.md:80](../developer/package-map.md) and the README `Syncing` row if needed.
- **(Q2 = D):** A's gate and observer items; C at `CheckCluster`. **(Q2 = C):** delete the three
  terms and master rows; the CRD description (`api/v1`, `make manifests`, `README.md:433`) says an
  attached count.
- In every case the only non-test user of `MasterSyncInProgress` is the parser (and the predicate
  under A).

### Re-pointing and the delete

- **(Q3 = B):** the veto inside `forceReplicaConnections`, reusing `dbSizeReader` and
  `demotionRefusalReason`, covering both callers. Unit: X with keys or both empty re-point all; X
  empty with a holder, a non-Ready holder, or an unreadable count re-point none (fail with the veto
  removed; per-target and targets-only mutations fail the mixed and non-Ready cases). Give
  `TestHandleMasterWithNoReplicas_ForcesReconnectAndResetsSentinelOnTimeout` key counts.
- **Independent:** rename `TestVerifyNewMasterReady_AcceptsAMasterWithReplicasAndData`
  ([sentinel_failover_test.go:1063](../../internal/controller/sentinel_failover_test.go#L1063)) and
  its message to what it pins, X's count being read; add a `DBSIZE 0` test pinning today's
  acceptance, the refusal test under Q4 = A.
- **(Q4 = A):** in `replaceRemainingPods`, after the gate and before the D5 gate,
  `demotionRefusalReason(dbSizeReader(ctx, v), X, O)` unchanged. X with keys or both empty: deleted;
  X empty and O with keys or unreadable, X unreadable: kept.
- **(Q5 = C):** the condition in `api/v1`, its `conditionRegistry` row, README condition row,
  [status.md](../operations/status.md), [rolling-updates.md](../operations/rolling-updates.md). Test
  past `syncTimeout`: state kept, condition True, no `RollingUpdatePaused`, no delete,
  `DeferredRequeueAfter` without `NeedsRequeue`, Sentinel roll held, one Warning Event; O dropping
  to 0 keys gives the delete and the presence-guarded clear.

### Write fencing

- **Independent:** the `WAIT` fixture above gets the real `ERR WAIT cannot be used with replica
  instances. ...` reply and a comment that any error reply blocks the promotion; assertion unchanged.
- **(Q6 = refuse):** new ADR "the operator does not offer `min-replicas-to-write` as a CRD field",
  index row, measurements and option 1's prerequisites as Alternatives, stating it does not protect
  against T35's flush; re-open trigger: a user asks, runs `replicas >= 3` and Valkey 9 with Q1 = A.
  If Q1 = B, one ADR with that fence. Rewrite this ticket's citations (ADR 0025 `:443-444`, ADR 0028
  `:123`, `:230`, [pod_termination_test.go:257](../../internal/controller/pod_termination_test.go);
  ADR 0026 `:206-208` may cite the ADR) until `git grep -n 'T12\|012-the-master-handover' -- ':!docs/tickets'`
  is empty.
- **(Q6 = build):** `spec.writeFencing` (name open), default off:
  `enabled: false # default`, `minReplicas: 1 # example`, `maxLagSeconds: 10 # example, >= 1`.
  Render both directives in the shared tail only when enabled (ADR 0005 D1); refuse
  `minReplicas > replicas - 2` and `maxLagSeconds < 1` without rendering (Q7), covering Sentinel
  with `replicas: 1`; parse and surface `slaveN` `state`/`lag` and `min_slaves_good_slaves`;
  document the refused-write cost at the field (ADR 0005 D8 shape); a divergence-free promotion for
  paths Q1 = A does not cover (`FAILOVER TO <host> <port>` exists on both pins) or that refusal in
  writing; gate e2e writes on `waitForConnectedReplicas` (after T34). Unit: absent and disabled
  byte-identical, enabled renders both bodies, invalid shapes render nothing, parser reads the new
  fields. E2E both legs: a fenced 3-replica cluster rolls under the writer harness with no
  acknowledged write lost.

## Open questions

### Q1: How should the roll's own Sentinel failover stop the outgoing master from acknowledging writes that are later discarded? (forced failover)

Today O acknowledges writes for about 16 s after the promotion, all lost. No option changes crash
failovers, the non-Sentinel path, the drain handler, pod templates or the CRD. Decide before Q6.

- **A - `COORDINATED` on Valkey 9, forced as fallback (recommended).** M. 0 lost in eight runs, and
  the second failover vanishes on Valkey 9 because O is a replica at once. A stalled `FAILOVER TO`
  can block writes on O for up to the remaining 60 s `failover-timeout` (refused, not lost; not
  measured); Valkey 8 keeps today's loss.
- **B - runtime `CONFIG SET min-replicas-to-write 1` on O before each trigger.** M. Both lines,
  still about 500 lost, the second failover untouched, a write to clear wherever O stays master;
  Sentinel's `CONFIG REWRITE` persists it, so a later crash failover can promote a pod refusing
  every write. Shares one ADR with Q6's refusal.
- **C - keep the loss and record its size.** S. Nothing changes at runtime.

A: 0 lost by one argument on a command already sent, no state to clear, and its fallbacks are
exactly today's command; B can follow for the Valkey 8 residual.

**Answer:** _open_

### Q2: Which signal answers "every replica holds the dataset" at `CheckCluster`, the observer and `verifyNewMasterReady`? (sync checks)

All three count attached replicas on the master, empty ones included. Not touched: the ADR 0007 D10
promotion gates, the `replicas: 2` shape, the dataset veto (Q4).

- **A - the replicas' full replication answer at all three sites (recommended).** M. The roll waits
  for the resync before deleting O; `Syncing` and `Ready=False` during every replica full sync.
- **D - A at the roll gate and the observer, C at `CheckCluster`.** Protects the delete; `Ready`
  does not flap on an eviction but keeps reading ready about an empty replica.
- **C - delete the terms, correct the records.** S, no behaviour change.

A: guards the one irreversible step with existing, tested code, and `Ready` reports the data plane
as ADR 0002 D5a defines it, at no extra dial.

**Answer:** _open_

### Q3: What does `forceReplicaConnections` do when X holds no keys and a pod it would re-point holds some? (re-pointing)

Decides whether ADR 0028's dataset rule binds this second `REPLICAOF` site. An X with keys still
re-points everyone; a refused shape holds as two visible masters (`MultipleMasters`,
`SplitBrainDetected` after 90 s), as ADR 0028 D3 and D8 accept.

- **A - veto per target.** XS-S. An empty target still attaches, the gate passes, and the spared
  holder is deleted one pass later.
- **B - veto the whole call (recommended).** S. X empty or unreadable: count every other existing
  pod, non-Ready included; any holder or unreadable count sends nothing and logs once who holds
  what. The no-replica branch then cycles every 90 s, and `replaceRemainingPods` waits for a replica
  no longer sent.

B: A's progress is the failure, since the empty pod it re-points unlocks the delete of the holder it
spared. Attaching by another route is Q4.

**Answer:** _open_

### Q4: What, beyond an attached replica, must hold before O is deleted on the Sentinel path? (delete)

`:3034` is the last point where a data-holding O can be kept; the veto needs O's count only when X
is empty.

- **A - veto at this delete (recommended).** S. Refuse when X is empty and O holds keys or a count
  is unreadable. The two masters of ADR 0028 D3 then last unbounded on this path, both behind `-rw`,
  until Sentinel or a human resolves them or X gains keys; a non-Ready O is held while X is empty.
- **B - veto at every roll delete** (`replaceNextReplica`, `deleteNextPendingPod` too). M. A master
  `DBSIZE` before every replica delete, for pods whose keys the next sync discards anyway.

A: protects exactly the pod at risk, at the site ADR 0028 D8 names, independent of Q5.

**Answer:** _open_

### Q5: What does a refused delete report and hand over to? (delete)

ADR 0010 bounds every roll wait and forbids handing expiry to a cleared state. Before the bound a
plain requeue, past it `DeferredRequeueAfter` (as `terminationWait`, `:2056-2076`), clocked by the
sync-wait bound (`ensureSyncWaitTimestamp`), which Q2 = A arms in the same gate. The held state is
`failover-triggered`, whose other arm calls `forceReplicaConnections`: a hold is only as safe as
Q3 = B.

- **A - pause like the manual path** (`waitOrPauseForReplicaSync`). XS. Clears the state against
  ADR 0010, releases the Sentinel roll, repeats a Warning and phase `Error` every `syncTimeout` (T23).
- **B - `RollingUpdatePaused` with a new reason.** S. The condition then means both "cleared and
  retrying" and "held, never retrying".
- **C - its own edge condition (recommended)**, for example `DatasetDeleteRefused` naming both pods
  and counts, set past `syncTimeout` with one Warning Event, cleared presence-guarded where the
  veto lets the delete through and in `clearRollingUpdateState`. S.

C: one meaning per condition, like the three `...Stalled` conditions, for one registry row more
than B.

**Answer:** _open_

### Q6: Does the operator offer `min-replicas-to-write` as an opt-in CRD field, or refuse it in an ADR? (write fencing)

A fence stops the replica-less side of a split from taking writes, but refuses writes at every
failover with full-resyncing replicas and turns degraded states into unexplained write outages.
Decided after Q1. A generic `spec.extraConfig` is no option (it would expose `replicaof`, `save`,
TLS).

- **Option 1 - build `spec.writeFencing`.** L. Protects refused-demotion windows and steady-state
  splits when opted in; at least 5 s of refused writes per controlled failover on Valkey 8 Sentinel
  clusters and on the non-Sentinel path under load.
- **Option 2 - refuse it in an ADR (recommended).** S. Nothing changes at runtime.

Option 2: nobody has asked, option 1 is L with a recurring cost, and beyond Q1 = A it adds only the
refused-demotion windows, bounded by ADR 0010 and reported as `MultipleMasters`. If Q1 = C, option 1
is the only protection of the ADR 0025 D9 window and this has to be redone.

**Answer:** _open_

### Q7: Only if Q6 = build: is `minReplicas <= replicas - 2` / `maxLagSeconds >= 1` enforced at admission or at runtime? (write fencing)

CEL is available ([valkey_types.go:541-542](../../api/v1/valkey_types.go)) and the CRD ships with
the chart.

- **Admission (CEL):** invalid specs never arrive, but a later scale-down below `minReplicas + 2` is
  refused too, against the `podDisruptionBudget.maxUnavailable` precedent
  ([valkey_types.go:775-779](../../api/v1/valkey_types.go)).
- **Runtime:** nothing rendered; a `WriteFencingNotApplied` condition and a Warning Event report it.

No option is recommended.

**Answer:** _open_

## Not verified

- Every part on Kubernetes (write rate, `-rw` lag, pooled connections, whether the delete pass lands
  inside a resync): the writer harness settles it.
- The second failover on Kubernetes and 8.1.9, and whether the sidecar's role read beats the Valkey
  container's SIGTERM (no `preStop` on Sentinel clusters,
  [statefulset.go:746-749](../../internal/builder/statefulset.go#L746-L749)).
- For Q1 = A: TLS replication, two Sentinels, no majority, a stalled `FAILOVER TO`.
- What a replica in `wait_bgsave` serves, and what Sentinel promotes if X fails then.
- Under Q2 = A, whether the sync-wait annotation is clear on entering `replaceRemainingPods`.
- The re-pointing and delete paths end to end (read only); how often X is empty while O holds data;
  persistence on a PVC (docker only).
- The `status.phase` of a held pass (`updateHAStatus`,
  [valkey_controller.go:2427](../../internal/controller/valkey_controller.go#L2427)); whether the
  non-Sentinel `replaceRemainingPods` can delete a holder behind an empty master.
- Fencing: survivor lag measured once on 9.1.1; whether the diverging side in ADR 0028's windows is
  the replica-less one.

## Related

- T34: the e2e reply check the writer harness and Q6 option 1 need.
- T40: item (b), the non-Sentinel counterpart of the lossless-text rewording.
- T35: shortens how long `-rw` routes to O; its flag-aware `-rw` fence was left here, no option takes it up.
- T23: `resetSentinelState` at `:3161` resets toward X without asking for keys; Q1 = A's fallback runs through it.
- T23: the uncapped reset-and-retrigger cycle, a route to an empty X; edits the same comment lines.
- T35: a non-persistent promoted pod restarting empty; fencing does not protect against its flush.
- T35: the probe passing on `LOADING`.
- T23: rests on `AllSynced` counting a mid-sync replica; Q5 option A inherits its missing pause record.
- T18: its option B assumes `True/HAClusterReady` between replacements, false under Q2 = A.
- T40: counts this ticket's citations outside `docs/tickets/` and the three T32 citations.
- Origin: [archive/039](archive/039-findings-from-the-1-11-0-fleet-rollout.md) (T4/T11 analysis).
