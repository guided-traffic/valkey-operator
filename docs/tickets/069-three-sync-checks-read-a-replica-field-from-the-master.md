---
id: T69
title: Three "no sync in progress" checks read master_sync_in_progress from a master's INFO, where Valkey never emits it, so none of them can fire
state: analysed       # facts re-read at 84a39c2, the upstream source read at both pinned tags, the master-side and replica-side answers measured in docker on both pins (2026-09-27); options costed with a marked best, no decision yet
severity: medium      # the gate in front of the one irreversible step of a Sentinel roll, the delete of the former master, passes while the only other current replica is still in a full resync (measured shape, both pins); a loss needs a second fault, the new master dying inside that window, so not high. The status half (Ready=True, phase OK while a replica holds no dataset) and the observer half are low on their own
security: none        # no trust boundary and no hostile principal: a data-safety gate and a status report that cannot fire; data-loss tickets of this repository carry none (T12, T36, T62)
urgency: now          # rule 1, second clause (measured-false statements in tracked files): ADR 0007:517-519 and ADR 0026:475-476 and :790-791 say verifyNewMasterReady requires "no sync in progress"; api/v1/valkey_types.go:918, both generated CRDs and README.md:433 say replicaSyncFailure fires while "bulk sync is in progress"; the code comments at checker.go:32 and :38, checks.go:91 and rolling_update.go:2980-2982 and :3011 promise the same - all contradicted by upstream source and by measurement on both pins. The first clause does not match: every site is released (5214d56, c6f97e2, 88b721b). Recompute to next (rule 3: severity medium, trigger live on every Sentinel roll whose redirected replica full-resyncs) once work list item 1 lands
effort: M             # recommended option A: three sites (the roll gate reuses verifyReplacedReplicasSynced), one shared predicate, eight unit fixtures that pin an impossible reply rewritten, one e2e guard, the ADR 0007 D10 amendment, the ADR 0026 lines, the code comments of Work list item 1; option C alone is S
blocked-by: decision  # which signal answers "every replica is synced" at the three sites (Options)
filed-from: T12 (its Work list "file as tickets of their own"), during the re-verification of 2026-09-27 at 84a39c2
opened: 2026-09-27
decided:              # not recorded - no decision yet
done:                 # not recorded - not done
---

# T69 - Three "no sync in progress" checks read master_sync_in_progress from a master's INFO, where Valkey never emits it, so none of them can fire

Filed on 2026-09-27 from [T12](012-no-write-fencing-min-replicas-to-write-as-an-opt-in-field.md),
whose re-verification at `84a39c2` found the fact while reading `CheckCluster` and recorded it as
outside T12's mechanism. Everything T12 and its run recorded about it is moved here; every
location below is re-read at `84a39c2` (the working tree changes nothing under `internal/`,
`api/`, `test/` or `docs/adr/`), and the measurements below were taken for this file.

## Fact

**Mechanism.** Valkey writes `master_sync_in_progress` into `INFO replication` only inside the
`if (server.primary_host)` block, that is only on a replica, where it is
`server.repl_state == REPL_STATE_TRANSFER`
([server.c 9.1.1:6506-6525](https://github.com/valkey-io/valkey/blob/9.1.1/src/server.c#L6506-L6525);
the same block at [server.c 8.1.9:6039-6058](https://github.com/valkey-io/valkey/blob/8.1.9/src/server.c#L6039-L6058)).
A master's reply has no such line, and `parseReplicationInfo`
([`client.go:592-593`](../../internal/valkeyclient/client.go#L592-L593)) leaves
`ReplicationInfo.MasterSyncInProgress` at its zero value `false`. On the master side
`connected_slaves` is `listLength(server.replicas)`
([server.c 9.1.1:6567](https://github.com/valkey-io/valkey/blob/9.1.1/src/server.c#L6567),
[8.1.9:6091](https://github.com/valkey-io/valkey/blob/8.1.9/src/server.c#L6091)): a replica is
counted from the moment it asks for a sync, in `wait_bgsave`, `send_bulk` and `online` alike
(the per-replica `slaveN` line carries that state,
[server.c 9.1.1:5834-5843, :6577-6603](https://github.com/valkey-io/valkey/blob/9.1.1/src/server.c#L6577-L6603)).
So a check that reads a master's `INFO` for "no sync in progress" is always satisfied, and a
check that pairs it with `connected_slaves` is satisfied by a replica that holds nothing yet.

Three production sites do exactly that:

| Site | What it reads | What it was meant to check (its own comment or contract) | What it checks |
|---|---|---|---|
| `CheckCluster` [`checker.go:130`](../../internal/health/checker.go#L130) (`AllSynced`), consumed at [`valkey_controller.go:2475-2488`](../../internal/controller/valkey_controller.go#L2475-L2488) | the master found by `findMaster` (role master, [`checker.go:194-199`](../../internal/health/checker.go#L194-L199)) | "true when all replicas have completed sync with the master" ([`checker.go:38`](../../internal/health/checker.go#L38)); `ReadyReplicas` "ready and synced" (`:32`); README: phase `Syncing` = "Replication sync in progress" ([`README.md:530`](../../README.md)) | `connected_slaves == spec.replicas - 1` only |
| observer `checkReplicaSync` [`checks.go:103`](../../internal/observer/checks.go#L103), run as `replica_sync` ([`observer.go:280`](../../internal/observer/observer.go#L280)) | the address `discoverMaster` returns ([`checks.go:17-27`](../../internal/observer/checks.go#L17-L27)) | "all replicas are connected and synced" ([`checks.go:91`](../../internal/observer/checks.go#L91)); CRD `replicaSyncFailure`: "a replica is disconnected or bulk sync is in progress" ([`valkey_types.go:918`](../../api/v1/valkey_types.go#L918), [`README.md:433`](../../README.md)) | `connected_slaves >= replicas - 1` only |
| `verifyNewMasterReady` [`rolling_update.go:3355`](../../internal/controller/rolling_update.go#L3355), the gate in front of the former master's delete in `replaceRemainingPods` ([`:3011-3017`](../../internal/controller/rolling_update.go#L3011-L3017)) | a pod answering `Role == master` (`:3348`) | "has all replicas synced" (`:3011`); "no sync in progress" ([`:2980-2982`](../../internal/controller/rolling_update.go#L2980-L2982), ADR 0007 `:517-519`, ADR 0026 `:475-476`, `:790-791`) | `connected_slaves > 0` only (`:3350`) |

On the observer, `discoverMaster` can return a replica (a stale Sentinel answer, or the pod-0
fallback of [`checks.go:76-83`](../../internal/observer/checks.go#L76-L83)), whose reply does carry
the field; but a replica in this operator has no replicas of its own, so `connected_slaves:0`
fails the check at `:99-101` before `:103` is reached. The term is therefore never decisive at
any of the three sites — T12's "always false" holds literally for the first and the third. (By
reading: a chained topology, a replica with replicas of its own that `discoverMaster` returns, was
not examined; with Sentinel enabled `discoverMaster` has no pod-0 fallback, it returns the error of
`discoverMasterViaSentinel`, [`checks.go:18-19`](../../internal/observer/checks.go#L18-L19).)

**The replica-side term is never decisive either.** Both replica lines are read from one variable:
`master_link_status` is `up` exactly when `server.repl_state == REPL_STATE_CONNECTED`, and
`master_sync_in_progress` is `1` exactly when it is `REPL_STATE_TRANSFER`
([server.c 9.1.1:6523, :6525](https://github.com/valkey-io/valkey/blob/9.1.1/src/server.c#L6523-L6525);
8.1.9:6056, :6058). A reply with the link up and a sync in progress does not exist, so wherever the
link term is asked first, the sync term decides nothing: `replicationNotEstablishedReason` returns
at `master_link_status != "up"` before it reaches `:4472`, so its message "is still syncing from its
master" ([`rolling_update.go:4472-4474`](../../internal/controller/rolling_update.go#L4472-L4474)) is
unreachable - which is why T23 found the pause of a slow sync reading "replication not
established ... linkStatus=down"; `isSyncedReplica` ([`drain.go:353`](../../internal/sidecar/drain.go#L353))
is the same conjunction. This costs no behaviour (the link term is the stricter of the two), but
five more unit fixtures pin the impossible replica state `MasterLinkStatus: "up"` with
`MasterSyncInProgress: true`: `TestVerifyReplacedReplicasSynced_SyncInProgress`
([`rolling_update_test.go:3792-3798`](../../internal/controller/rolling_update_test.go#L3792-L3798)),
`TestReplaceNextReplica_WaitsForSyncBeforeDeleting`
([`rolling_update_test.go:4014-4020`](../../internal/controller/rolling_update_test.go#L4014-L4020)),
the case "pod-0 stuck mid-sync" of `TestHandleTopologyRestoration_AbandonsAfterSyncTimeout`
([`topology_restore_stall_test.go:143-150`](../../internal/controller/topology_restore_stall_test.go#L143-L150)),
`TestDrainHandler_NoSyncedReplicaFound`
([`drain_test.go:305-311`](../../internal/sidecar/drain_test.go#L305-L311)) and the row "sync in
progress" of `TestIsSyncedReplica` ([`drain_test.go:700-706`](../../internal/sidecar/drain_test.go#L700-L706));
the parser test `TestParseReplicationInfo_SyncInProgress`
([`client_test.go:88-98`](../../internal/valkeyclient/client_test.go#L88-L98)) parses the same
impossible reply, which is harmless for a parser test. By reading, the five are the tests that
would fail if the sync term were removed from `replicationNotEstablishedReason` or
`isSyncedReplica` (not run), so they would report a regression for a change that alters nothing
Valkey can send. (`TestHandleMasterFailover_WaitsWhileAReplicaIsStillSyncing`,
[`sentinel_failover_test.go:545`](../../internal/controller/sentinel_failover_test.go#L545), sets
the flag with no link status; the link term decides it, so it is not in this list.) No test
asserts the unreachable message (`grep -rn 'still syncing' internal/` finds only
`rolling_update.go:4473` and a comment in `rolling_update_test.go:4011`). Same field, same decision
(the shared predicate of option A), so it is an appendix here and not a ticket of its own.

**The replica-side answer already exists.** `replicationNotEstablishedReason`
([`rolling_update.go:4467-4476`](../../internal/controller/rolling_update.go#L4467-L4476)) asks a
replica for role, `master_link_status:up` and no transfer, and is used by
`verifyReplacedReplicasSynced` (`:2581`), `waitForReplicasReady` (`:2804`) and
`pod0SyncWaitReason` (`:4448`); the sidecar asks the same in `isSyncedReplica`
([`drain.go:351-353`](../../internal/sidecar/drain.go#L351-L353)); ADR 0007 D10
([`0007`:358-367](../adr/0007-failover-aware-rolling-update.md)) makes it the rule before a
promotion. It is not asked after the promotion, before the delete of the former master. And the
e2e tier already knows the fact: `replicationEstablished`
([`e2e_test.go:466-485`](../../test/e2e/e2e_test.go#L466-L485), commit `fb0557d`, 2026-08-22) says
`master_sync_in_progress` "is a field of a REPLICA INFO and never appears in a master response,
so the guard that used to stand here could not fail". That commit fixed the e2e helpers and left
the three production sites, and one e2e guard in the same shape
([`tls_test.go:1302-1310`](../../test/e2e/tls_test.go#L1302-L1310)), untouched.

**Three unit tests pin the dead term with a fixture Valkey never produces** (a master reply with
`master_sync_in_progress:1`): `TestVerifyNewMasterReady_RejectsAMasterStillSyncing`
([`sentinel_failover_test.go:1001-1011`](../../internal/controller/sentinel_failover_test.go#L1001-L1011)),
the row "a full sync in progress is not synced" of `TestCheckCluster_ReplicaAccounting`
([`checker_live_test.go:293-298`](../../internal/health/checker_live_test.go#L293-L298)), and the
row "full resync still running" of `TestCheckReplicaSync`
([`checks_endpoint_test.go:73-79`](../../internal/observer/checks_endpoint_test.go#L73-L79), fixture
builder [`fake_endpoint_test.go:301-307`](../../internal/observer/fake_endpoint_test.go#L301-L307),
which writes `master_sync_in_progress` into every node's reply, masters included). They assert the
code against its own fixture and prove nothing about Valkey (not run for this ticket).

**Verified:**

- *By reading at `84a39c2`:* the three sites and their consumers above; the only other readers of
  the field (`rolling_update.go:4472`, `drain.go:353`) read a replica's reply; `CheckCluster` runs
  only for Sentinel-enabled clusters ([`valkey_controller.go:2216-2218`](../../internal/controller/valkey_controller.go#L2216-L2218),
  `:2461`); `findMaster` dials every data pod already and keeps only the masters
  ([`checker.go:218-258`](../../internal/health/checker.go#L218-L258)); `checkReplicaRead` dials
  every data pod already ([`checks.go:135-159`](../../internal/observer/checks.go#L135-L159));
  `rollingUpdateRequeueDelay` is 10 s ([`rolling_update.go:203`](../../internal/controller/rolling_update.go#L203));
  ADR 0010's residual risks list the requeues inside `verifyNewMasterReady` as unbounded
  ([`0010`:766-773](../adr/0010-every-rolling-update-wait-is-bounded.md)).
- *By reading upstream source at tags 9.1.1 and 8.1.9* (`raw.githubusercontent.com/valkey-io/valkey/<tag>/src/server.c`):
  the lines cited under Mechanism, and the two replica lines derived from the one `repl_state`
  (9.1.1:6523, :6525; 8.1.9:6056, :6058).
- *By reading at `84a39c2`, added in the review of 2026-09-27:* `collectPodStates` asks every data
  pod `GetReplicationInfo` on every roll pass and sets `isMaster` from the answer
  ([`rolling_update.go:1947-1961`](../../internal/controller/rolling_update.go#L1947-L1961));
  `verifyReplacedReplicasSynced` ([`:2536-2603`](../../internal/controller/rolling_update.go#L2536-L2603))
  skips `needsUpdate`, `isMaster` and missing pods, waits on an unavailable current pod, asks
  `replicationNotEstablishedReason`, arms the sync-wait bound and pauses on its expiry; the e2e
  helpers `waitForConnectedReplicas` and `waitForReplicaSynced` run `valkeyExecQuick`, which calls
  `valkey-cli` without `--tls` ([`e2e_test.go:523-540`](../../test/e2e/e2e_test.go#L523-L540)); the
  TLS test already waits for `replicationEstablished` over TLS before its writes
  ([`tls_test.go:1266`](../../test/e2e/tls_test.go#L1266), helper
  [`:1618-1626`](../../test/e2e/tls_test.go#L1618-L1626)); the chart's `ValkeyPhaseNotOK` alert
  fires on a phase other than `OK` held for 30 min
  ([`prometheusrule.yaml:73-77`](../../deploy/helm/valkey-operator/templates/prometheusrule.yaml#L73-L77)).
- *By git:* origins `88b721b` (2026-02-17, `checker.go:130`), `5214d56` (2026-02-18,
  `rolling_update.go:3355`), `c6f97e2` (2026-03-20, `checks.go:103`), `f6ddb63` (2026-03-21, the
  CRD description), `9f0f595` (2026-02-17, `tls_test.go:1309`); `fb0557d` touched only
  `test/e2e/`.
- *Measured in docker on both pins, 2026-09-27* (Measurements): no `role:master` reply in any poll
  of any run contained `master_sync_in_progress`; the master counts a replica in
  `connected_slaves` from `wait_bgsave` on; the master flips the replica to `state=online` before
  the replica has finished loading; after a `SENTINEL FAILOVER` under writes the non-promoted
  replica full-resyncs while the new master already reports `connected_slaves:1`; no replica reply
  in any poll of the five runs showed `master_link_status:up` together with
  `master_sync_in_progress:1` (the saved poll logs of the filing run, re-read by `grep -c` in the
  review: 0 in each).

**Not verified, and what would settle it:**

- Anything on Kubernetes. Whether `verifyNewMasterReady` actually runs inside the redirected
  replica's full resync depends on when the post-failover pass reaches it (requeues of 10 s)
  against the resync duration, which scales with the dataset (about 8 s for 1.5 M keys of 100 B
  in docker). A Kind run of a Sentinel roll with a writer and a dataset large enough that the
  resync outlasts one requeue, logging the new master's `INFO` at the moment of the delete, would
  settle it.
- Which dataset a replica in `wait_bgsave` still serves after a failover redirect (the fresh
  replica of M1 served `DBSIZE 0`; a redirected one keeps its previous dataset until the load,
  by upstream default `repl-diskless-load disabled` - read, not measured here).
- That the observer's `replica_read_test` goes red during a replica's full sync (by reading: an
  empty or stale replica fails the `GET` compare, a loading one returns an error; not run).
- Dual-channel replication (`dual-channel-replication-enabled`, default off) lists an extra
  `type=rdb-channel` entry. By reading it is counted: `connected_slaves` is
  `listLength(server.replicas)` and the `slaveN` loop walks the same list
  ([server.c 9.1.1:6567-6603](https://github.com/valkey-io/valkey/blob/9.1.1/src/server.c#L6567-L6603));
  not measured. The operator does not enable it: the data config writes only `repl-diskless-sync
  yes` and `repl-diskless-sync-delay 5` ([`configmap.go:178-179`](../../internal/builder/configmap.go#L178-L179)).
- Under option A, `verifyReplacedReplicasSynced` would run after the promotion as well: whether the
  sync-wait annotation is clear when `replaceRemainingPods` is entered, and what the pass after a
  pause there does with the former master (by then an outdated replica; T23: a pause clears the
  roll state and the next pass dispatches again on a fresh budget), were not traced.
- What Sentinel promotes, and what that pod then holds, if the new master fails while the
  redirected replica is in `wait_bgsave` or loading, was not examined (Impact).

### Measurements 2026-09-27 (docker, `valkey/valkey:9.1.1` and `valkey/valkey:8.1.9`)

**M1 - a fresh replica's full sync, seen from both sides.** Per tag:

```sh
docker network create vko-file-069-net-<tag>
docker run -d --rm --name vko-file-069-m-<tag> --network vko-file-069-net-<tag> valkey/valkey:<tag> \
  valkey-server --enable-debug-command yes --save "" --appendonly no
docker exec vko-file-069-m-<tag> valkey-cli DEBUG POPULATE 1500000 key 100
docker run -d --rm --name vko-file-069-r-<tag> --network vko-file-069-net-<tag> valkey/valkey:<tag> \
  valkey-server --replicaof vko-file-069-m-<tag> 6379 --save "" --appendonly no
# then about every 0.75 s: INFO replication on both, INFO persistence (loading:),
# PING with its exit code, and DBSIZE on the replica
```

`repl-diskless-sync yes`, `repl-diskless-sync-delay 5` (defaults, read with `CONFIG GET`; the same
two values the operator writes, [`configmap.go:178-179`](../../internal/builder/configmap.go#L178-L179)).
The `PING` exit code in the table is that of a second `PING` issued right after the one whose text
is shown; in the `LOADING` rows the `DBSIZE` that followed it still answered `LOADING`, so both
fell inside the load.

| Time after replica start | Master `INFO` | Replica `INFO` | Replica PING / DBSIZE |
|---|---|---|---|
| 9.1.1 +0.0 .. +5.4 s | `connected_slaves:1`, `slave0:...state=wait_bgsave,offset=0` | `master_link_status:down`, `master_sync_in_progress:0`, `loading:0` | `PONG`, exit 0 / `0` |
| 9.1.1 +6.2 s | `state=online,offset=0` | `link down`, `sync_in_progress:1`, `loading:1` | `LOADING Valkey is loading the dataset in memory`, **exit 0** |
| 9.1.1 +7.8 s | `state=online` | `master_link_status:up`, `sync_in_progress:0` | `PONG` / `1500000` |
| 8.1.9 +0.0 .. +5.3 s | `wait_bgsave` | `link down`, `sync_in_progress:0` | `PONG`, exit 0 / `0` |
| 8.1.9 +6.0 s | still `wait_bgsave` | `sync_in_progress:1` | `LOADING ...`, exit 0 |
| 8.1.9 +6.8 s | `state=online,offset=0` | `sync_in_progress:1`, `loading:1` | `LOADING ...`, exit 0 |
| 8.1.9 +7.6 s | `state=online` | `master_link_status:up` | `PONG` / `1500000` |

No master reply contained `master_sync_in_progress`. For 6-8 s the master reported the final
`connected_slaves` while the replica held nothing; `state=online` came 0.8-1.6 s before the
replica's link was up. The `PING` exit code 0 on a `LOADING` reply is the readiness probe's
behaviour ([`statefulset.go:1515-1545`](../../internal/builder/statefulset.go#L1515-L1545), no `-e`),
so the replica pod stays Ready throughout. That probe fact belongs to
[T76](076-the-exec-probes-pass-on-any-server-reply.md), which measured it on both pins, including
the load of a full sync; [T36](036-non-persistent-master-restarts-empty.md) ~~still records it as
inferred~~ *(since the sweep of 2026-09-27 points at T76's measurement)*. It is not this ticket's
mechanism.

**M2 - a controlled Sentinel failover, seen from the new master.** Per tag: a master `m` with
`DEBUG POPULATE 1500000 key 100` and two replicas `r1`, `r2` (as in M1, plus
`--replica-announce-ip <container name>`); once both are `state=online` and linked, a 12 s
settle; one `valkey-sentinel` with `port 26379`, `sentinel resolve-hostnames yes`,
`sentinel announce-hostnames yes`, `sentinel monitor mm <m> 6379 1`,
`down-after-milliseconds 5000`, `failover-timeout 60000`, `parallel-syncs 1`; after 8 s
`WAIT 2 1000` on `m`; in the writer runs
`valkey-benchmark -t set -n 100000000 -r 100000 -c 1 -P 1 -q` started inside `m` 2 s before;
then `SENTINEL FAILOVER mm` and `INFO replication` (plus `loading:`) of all three about every
0.7 s.

| Run | New master's view of the non-promoted replica | That replica's own answer |
|---|---|---|
| 9.1.1, no writer | `state=online` from +1.4 s | `master_link_status:up` throughout (partial resync) |
| 9.1.1, writer | `connected_slaves:0` at +0.0 s, then `connected_slaves:1`, `state=wait_bgsave` +0.7 .. +6.0 s, `state=online,offset=0` at +6.7 s | `link down` +1.4 .. +7.4 s, `sync_in_progress:1` +6.7 s, `loading:1` +7.4 s, `link up` +8.1 s (full resync) |
| 8.1.9, writer | `connected_slaves:0` at +0.0 s, then `connected_slaves:1`, `state=wait_bgsave` +0.7 .. +6.1 s, `state=online,offset=0` at +6.7 s | `link down` +0.7 .. +7.4 s, `sync_in_progress:1` +6.1 s, `loading:1` +6.7 .. +7.4 s, `link up` +8.1 s (full resync) |

In all three runs the old master kept answering `role:master` for about 12-19 s, was then
converted by Sentinel and full-resynced itself (listed by the new master as `wait_bgsave` for
about 5 s, `online` before its own load finished). T12's run recorded the same full resync of the
redirected replicas under writes. Every container and network named `vko-file-069-*` was removed
(`docker ps -a` and `docker network ls` with that filter return nothing).

## Impact

- **The former master's delete in a Sentinel roll.** With `spec.replicas` of 3 or more, the
  replica Sentinel did not promote is redirected to the new master and, under writes,
  full-resyncs (M2, both pins). From about 0.7 s after the failover command (in both writer runs
  the poll at +0.0 s still read `connected_slaves:0`) the new master reports
  `connected_slaves >= 1`, so
  `verifyNewMasterReady` passes, and `replaceRemainingPods` deletes the former master while the
  other current replica is in `wait_bgsave` or loading. For the resync's duration the tier holds
  one current copy, the new master. The redirected replica keeps its pre-redirect dataset until
  its load starts (upstream default `repl-diskless-load disabled`, which the operator does not
  change; read, not measured) and holds nothing usable during the load. If the new master fails
  inside that window, the writes it acknowledged since the promotion exist nowhere else, and in
  the load phase no complete copy exists at all; on a non-persistent tier that is the dataset
  (what Sentinel promotes in that state was not examined). It needs a second fault; the window
  grows with the dataset. With `replicas: 2` there is no other current replica: the only replica the new master counts is the pod about to be
  deleted, which is T12's consequence 1 and not changed by this ticket.
- **The CR status of a Sentinel cluster.** After any replica restart (roll, eviction, drain, a
  chaos kill) that ends in a full sync, `CheckCluster` reports `AllSynced`, so the status reads
  phase `OK`, `Ready=True/HAClusterReady`, "All Valkey and Sentinel instances are ready", while
  that replica holds nothing (M1: 6-8 s at 1.5 M keys; longer with the dataset). The pod is Ready
  as well, because the probe passes on `LOADING` (M1, T76). `Syncing` appears only while fewer
  replicas are connected. A replica that restarts its full sync over and over while the master
  keeps listing it (by reading, for example a replica output buffer limit hit during every
  transfer; not measured) therefore reads `OK` indefinitely, and the chart's `ValkeyPhaseNotOK`
  alert (the PrometheusRule is default off), which waits for 30 min of a phase other than `OK`,
  never sees it.
- **The observer's `replica_sync` check** never goes red on a bulk sync, against the CRD
  description users set `replicaSyncFailure` by. By reading, `replica_read_test` (default on)
  goes red during that window instead; a user who turned `replicaReadTestFailure` off and kept
  `replicaSyncFailure` on gets neither.
- **The records.** ADR 0007 and ADR 0026 state a gate the code does not have, and ADR 0026 D11's
  *Replacement* argument leans on it (`:473-477`).

## Options

**What the code does today.** Each site asks the master one question, "how many replicas are
attached, and is a sync running", and Valkey answers only the first half, counting a replica
from the moment it asks to sync. **What the choice decides** is which signal answers "every
replica holds the dataset" at the three sites. It does not change the promotion gates of
ADR 0007 D10 (they already ask the replicas), the `replicas: 2` shape, the unbounded requeues
ADR 0010 lists, or the `DBSIZE` gap of `verifyNewMasterReady` (ADR 0026 Residual risks).

- **A - ask the replicas, with the full replication answer. (recommended)** Move the predicate of
  `replicationNotEstablishedReason` into `valkeyclient` (a method on `ReplicationInfo`), so the
  controller, the health checker, the observer and the sidecar share one definition, and use it.
  Its decisive terms are role and `master_link_status:up`; the sync term may stay, it can never
  decide (Fact, "The replica-side term is never decisive either").
  - `CheckCluster`: `probeMasterRole` keeps every pod's reply, which `findMaster` already
    collects concurrently; `AllSynced` and `ReadyReplicas` count the replies with the full
    answer. No additional dial.
  - `verifyNewMasterReady`: after finding the new master, call the existing
    `verifyReplacedReplicasSynced` with the same `pods`. It already asks every current, existing,
    non-master pod for the full answer, skips the outgoing pod (`needsUpdate`; it resyncs itself,
    M2, and is the pod being deleted), waits on an unavailable current pod through the ADR 0026
    D11 bound, and is bounded by `spec.rollingUpdate.syncTimeout` through the sync-wait bound,
    pausing the roll on expiry as ADR 0007 D10 does - so the half adds no new wait code and no
    unbounded wait to ADR 0010's list. It costs one `INFO` per current replica on that pass,
    what `replaceNextReplica` already pays before every replica delete
    ([`rolling_update.go:2424`](../../internal/controller/rolling_update.go#L2424)).
  - observer `checkReplicaSync`: ask each data pod (it dials each in `checkReplicaRead` already);
    `replicas - 1` must give the full answer. The CRD description becomes true as written.

  Cost: M. A Sentinel roll now waits the redirected replica's resync (seconds, minutes on a large
  dataset) before deleting the former master; the status shows `Syncing` and `Ready=False` for
  the duration of every replica full sync on the passes that reach `updateHAStatus` (a pass that
  ends on a rolling-update exit keeps the last value, T18), with the 10 s requeue of that phase
  ([reconcile-loop.md:136](../developer/reconcile-loop.md)). ADR 0007 D10 is amended to cover the
  delete after the promotion.
- **B - read the per-replica state from the master.** Parse the `slaveN` lines and require
  `state=online` for `replicas - 1` entries. One dial per site. Measured weaker: the master sets
  `online` 0.8-1.6 s before the replica has loaded (M1, M2) - the "looser half" `fb0557d` already
  documents in the e2e helpers. Tightening it with `offset > 0` rests on when the replica's first
  `REPLCONF ACK` arrives, which is observed behaviour, not a documented contract. Loses to A: it
  rebuilds in production the gap the e2e tier was fixed for.
- **C - delete the three terms and correct the records.** Say what the code checks (an attached
  count); change the CRD description (`api/v1`, `make manifests`, both CRD files, README.md:433).
  Cost: S, no behaviour change. The former master's delete keeps passing during the redirected
  replica's resync, and the status keeps reading `OK`. Loses because the gate guards the one
  irreversible step of the roll, and the replica-side answer is already written and tested.
- **D - A at the roll gate and the observer, C at `CheckCluster`.** Runner-up. The case for it:
  it protects the irreversible step and leaves the status alone. Under A, `Ready` turns False for
  seconds on every replica full sync outside a roll - an eviction, a node drain, a chaos kill of a
  non-persistent replica - which a tool waiting on `Ready` (a Flux `wait: true` health check) sees
  as a transient failure, and T18 is an open re-decision of what `Ready` means during a roll, so
  changing its inputs now could be read as pre-empting it. It still loses to A: the flap is the
  true state (one copy fewer than the spec asks for), and ADR 0002 D5a already decided that `Ready`
  is the data-plane verdict, which T18 does not reopen for passes outside a roll; under D the
  status keeps saying "All Valkey and Sentinel instances are ready" about an empty replica, and a
  replica caught in a sync that never completes keeps the phase `OK`, so `ValkeyPhaseNotOK` never
  fires (Impact) - under A it fires after 30 min, the intended signal for a cluster that does not
  converge. A's `CheckCluster` half costs no dial - the replies are already on the wire and
  discarded.

A beats D on the one point where they differ: `Ready` and the phase then report what the data
plane holds, which is what ADR 0002 D5a says `Ready` is for, at no measurement cost.

## Decision

Not decided.

## Work list

1. **Needs no decision, XS, lands now (rule 1).** Correct in place, marked as corrections and
   without citing this ticket: ADR 0007 `:517-519` and ADR 0026 `:475-476` and `:790-791` (the
   gate is role, available and at least one attached replica; the sync term reads a master's
   `INFO` and cannot fire); the comments at `checker.go:32`, `:38`, `checks.go:91`,
   `rolling_update.go:2980-2982` and `:3011`. The CRD description and README.md:433 depend on the
   decision (true again under A and D, rewritten under C).
2. **Needs no decision, S, needs the TLS e2e on both lines:** the subtest "Wait for TLS
   replication sync" at `tls_test.go:1301-1311` waits for `connected_slaves:2`, which
   `waitForConnectedReplicasTLS` already established at `:1266` before the writes, and for the
   absence of `master_sync_in_progress:1` in the master's reply, which cannot fail. Replace its body
   with a replica-side wait over TLS on both replicas (`master_link_status:up` through
   `valkeyTLSExecAllowError`), which is what the next subtest, reading the data back from
   replica 1, needs. The `fb0557d` helpers `waitForConnectedReplicas` and `waitForReplicaSynced`
   are not usable here: they run `valkeyExecQuick` without `--tls` against a cluster whose plain
   port is closed (`tlsSpec()`), and no TLS variant of `waitForReplicaSynced` exists.
3. **Waits on the decision (A):** the shared predicate in `valkeyclient`, with
   `replicationNotEstablishedReason` and `isSyncedReplica` built on it; the three sites as
   described; the three unit tests above rewritten on replica-side fixtures (the impossible
   master fixtures removed, including the `syncInProgress` field of the observer's fake node for
   masters); the five impossible replica fixtures (`rolling_update_test.go:3792-3798`,
   `:4014-4020`, `topology_restore_stall_test.go:143-150`, `drain_test.go:305-311`, `:700-706`)
   rewritten to the state Valkey sends during a transfer, `master_link_status:down` with `master_sync_in_progress:1`, and the unreachable
   message at `rolling_update.go:4473` either removed or kept with a comment saying it cannot be
   reached (the fixture rewrite needs no decision and can land with item 1); ADR 0007 D10 amended and the ADR 0007 / ADR 0026 lines of item 1 restated;
   [package-map.md:80](../developer/package-map.md) if the `CheckCluster` wording changes; README
   `Syncing` row checked against the new behaviour.
4. **Waits on the decision (C):** delete the terms and the three tests' impossible rows, rewrite
   the CRD description, `make manifests`, README.md:433.

## Verification

- Unit: under A, a `verifyNewMasterReady` test where the new master reports `connected_slaves:1`
  and the other current replica answers `master_link_status:down` refuses the delete and arms the
  sync-wait bound; one past `syncTimeout` pauses; a `CheckCluster` test with every replica
  attached and one replica `link down` gives `AllSynced=false`; a `checkReplicaSync` test of the
  same shape fails. **Revert check (ADR 0017):** putting each site back to the master-side term
  turns each new test red.
- The eight impossible fixtures named under Fact (three master replies with the flag set, five
  replica replies with the link up and the flag set) no longer exist in that form, checked by
  reading the diff: a single `grep` cannot see them, because two are built across several lines
  (`checker_live_test.go:295-296`) or through a struct field (`syncInProgress` of the observer's
  fake node). `grep -rn -B1 -A1 'MasterLinkStatus: *"up"' internal/ --include='*_test.go' | grep
  'MasterSyncInProgress: *true'` returns nothing (it finds all five today).
- `grep -rn 'MasterSyncInProgress' internal/ --include='*.go' | grep -v _test.go` names only the
  shared predicate and the parser.
- e2e: the full suite on both Valkey lines (the Sentinel roll tests exercise the new wait); the
  TLS test of item 2 green on both lines. A Kind run of a Sentinel roll with a writer and a
  dataset whose resync outlasts one requeue shows the delete waiting for the replica's
  `master_link_status:up` (Not verified, above).
- `make test-unit`, `make lint`, `make cyclo`, `make test-integration`.

## Related tickets

- [T12](012-no-write-fencing-min-replicas-to-write-as-an-opt-in-field.md): the source; its
  consequence 1 is the `replicas: 2` shape this ticket leaves alone.
- [T23](023-pauserollingupdate-records-no-pause.md): its Sentinel resume-gap analysis rests on
  `AllSynced` counting a replica that is mid-sync. Under A (not under D, which leaves
  `CheckCluster` as it is), a slow full sync reads `Syncing` and requeues every 10 s, which changes that analysis; its
  measurement 2 covered `wait_bgsave` only, M1 here covers the transfer and the load, and the
  "replication not established" message it found is explained by the unreachable branch (Fact).
- [T18](018-ready-keeps-its-pre-roll-value-during-a-rolling-update.md): its option B describes the
  per-pass verdict between replacements as `True/HAClusterReady`; under A that pass reads
  `False/ReplicationSyncing` while a replaced replica is still in its full sync.
- [T76](076-the-exec-probes-pass-on-any-server-reply.md): owns the probe passing on `LOADING`,
  which M1 observed again.

## History

- 2026-09-27: filed from T12 (its Work list, "file as tickets of their own") during the
  re-verification at `84a39c2`. Moved from T12: the finding (the three sites, server.c
  9.1.1:6506-6525) and the upstream reading of its run. Re-verified now: every location at
  `84a39c2`; the upstream lines at both tags (server.c 9.1.1:6506-6525, :6567, :6577-6603; 8.1.9
  :6039-6058, :6091); one precision to T12 - on the observer the term is not literally always
  false (its address can be a replica), but never decisive, because `connected_slaves:0` fails
  first. Found now: the e2e comment of `fb0557d` (2026-08-22) already recorded the fact without
  touching the production sites; the vacuous e2e guard at `tls_test.go:1309`; three unit tests
  pinning a master fixture Valkey never produces; the measured-false statements in ADR 0007,
  ADR 0026, the CRD description, README.md:433 and five code comment locations. Measured in docker on both
  pins: M1 (fresh replica full sync, both sides) and M2 (controlled Sentinel failover, 9.1.1
  without and with a writer, 8.1.9 with a writer); the `PING` exit 0 on `LOADING` recorded for
  T36's family. Severity medium, security none, urgency now by rule 1. Adversarial review the
  same day, corrected in place: the `PING` fact belongs to T76, not T36; the new master read
  `connected_slaves:0` at the first poll, so "from the first poll" became "from about 0.7 s"; the
  redirected replica keeps its previous dataset until its load (read), so the Impact no longer
  says the tier loses the dataset for the whole window; ADR 0002 D5 became D5a; Work list item 2
  named plain-port helpers for a TLS-only cluster and now names a replica-side wait over TLS.
  Added: the replica-side sync term is never decisive either (`master_link_status` and
  `master_sync_in_progress` derive from the one `repl_state`, server.c 9.1.1:6523, :6525), so
  `rolling_update.go:4473` is unreachable and five more fixtures pin an impossible replica
  state; option A's roll-gate half reuses `verifyReplacedReplicasSynced`; the runner-up D argued
  on its own terms (the `Ready` flap, T18) and the mark kept on the `ValkeyPhaseNotOK` and
  ADR 0002 D5a grounds; the dual-channel count read in source; Related tickets. Severity,
  security, urgency and effort re-derived and unchanged.
  Sweep: The note that T36 still records the probe outcome on `LOADING` as inferred is struck: T36
  now points at T76's measurement. Frontmatter unchanged.
