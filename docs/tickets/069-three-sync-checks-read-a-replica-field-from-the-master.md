---
id: T69
title: Three "no sync in progress" checks read master_sync_in_progress from a master's INFO, where Valkey never emits it, so none of them can fire
state: analysed       # facts read and measured on both Valkey pins, options costed, no decision
severity: medium      # the gate before the former master's delete in a Sentinel roll passes during a replica's full resync; a loss needs a second fault
security: none        # no trust boundary and no hostile principal: a data-safety gate and a status report
urgency: now          # rule 1: ADRs, CRD description, README and code comments state a check the code does not have; next once work list item 1 lands
effort: M             # option A: one shared predicate, three sites, eight unit fixtures, one e2e guard, ADR amendments
blocked-by: decision  # Q1
filed-from: T12
opened: 2026-09-27
decided:
done:
---

# T69 - Three "no sync in progress" checks read master_sync_in_progress from a master's INFO, where Valkey never emits it, so none of them can fire

## Current state

**What Valkey reports.** `master_sync_in_progress` appears in `INFO replication` only on a
replica, as `repl_state == REPL_STATE_TRANSFER`
([server.c 9.1.1:6506-6525](https://github.com/valkey-io/valkey/blob/9.1.1/src/server.c#L6506-L6525),
[8.1.9:6039-6058](https://github.com/valkey-io/valkey/blob/8.1.9/src/server.c#L6039-L6058)). On a
master the line is absent and `parseReplicationInfo`
([`client.go:592-593`](../../internal/valkeyclient/client.go#L592-L593)) leaves
`MasterSyncInProgress` at `false`. A master counts a replica in `connected_slaves` from the moment
it asks to sync (`wait_bgsave`, `send_bulk`, `online`;
[server.c 9.1.1:6567, :6577-6603](https://github.com/valkey-io/valkey/blob/9.1.1/src/server.c#L6567-L6603)).
On a replica, `master_link_status:up` and `master_sync_in_progress:1` derive from the same
`repl_state` and never occur together.

Measured in docker on `valkey/valkey:9.1.1` and `8.1.9` (1.5 M keys of 100 B, diskless sync with
delay 5, the operator's settings):

- No `role:master` reply ever contained `master_sync_in_progress`.
- A fresh replica: the master reports the final `connected_slaves` for 6-8 s while the replica
  holds nothing; the master's `slaveN` line says `state=online` 0.8-1.6 s before the replica's
  link is up.
- A `SENTINEL FAILOVER` under writes: the non-promoted replica full-resyncs; from about 0.7 s the
  new master reports `connected_slaves:1` (`wait_bgsave` until about +6 s, then `online`), the
  replica's link is up only at about +8 s. Without writes the resync is partial.
- The replica answers `LOADING` to `PING` with exit code 0 during the load, so the pod stays
  Ready (owned by T76).

**The three sites** all ask the master:

| Site | Intended (comment or contract) | Actually checks |
|---|---|---|
| `CheckCluster` `AllSynced` [`checker.go:130`](../../internal/health/checker.go#L130), consumed at [`valkey_controller.go:2475-2488`](../../internal/controller/valkey_controller.go#L2475-L2488); Sentinel clusters only | "all replicas have completed sync" ([`checker.go:32`, `:38`](../../internal/health/checker.go#L38)); README phase `Syncing` | `connected_slaves == spec.replicas - 1` |
| observer `checkReplicaSync` [`checks.go:103`](../../internal/observer/checks.go#L103) (`replica_sync`) | "connected and synced" ([`checks.go:91`](../../internal/observer/checks.go#L91)); CRD `replicaSyncFailure` "bulk sync is in progress" ([`valkey_types.go:918`](../../api/v1/valkey_types.go#L918), both CRDs, `README.md:433`) | `connected_slaves >= replicas - 1` |
| `verifyNewMasterReady` [`rolling_update.go:3355`](../../internal/controller/rolling_update.go#L3355), the gate before the former master's delete in `replaceRemainingPods` ([`:3011-3017`](../../internal/controller/rolling_update.go#L3011-L3017)) | "all replicas synced", "no sync in progress" ([`:2980-2982`](../../internal/controller/rolling_update.go#L2980-L2982), `:3011`, ADR 0007 `:517-519`, ADR 0026 `:475-476`, `:790-791`) | role master and `connected_slaves > 0` |

If the observer's `discoverMaster` returns a replica, `connected_slaves:0` fails the check before
the sync term is reached, so the term is never decisive there either.

**The replica-side answer exists.** `replicationNotEstablishedReason`
([`rolling_update.go:4467-4476`](../../internal/controller/rolling_update.go#L4467-L4476)) asks a
replica for role, `master_link_status:up` and no transfer; `verifyReplacedReplicasSynced`
([`:2536-2603`](../../internal/controller/rolling_update.go#L2536-L2603)) applies it to every
current, existing, non-master pod, waits on an unavailable one, and is bounded by
`spec.rollingUpdate.syncTimeout` with a pause on expiry. The sidecar's `isSyncedReplica`
([`drain.go:351-353`](../../internal/sidecar/drain.go#L351-L353)) asks the same. ADR 0007 D10 makes
it the rule before a promotion; nothing asks it after the promotion, before the delete. The e2e
helper `replicationEstablished` ([`e2e_test.go:466-485`](../../test/e2e/e2e_test.go#L466-L485))
already reads replica-side and documents the fact. Because the link term is asked first, the
message "is still syncing from its master" (`rolling_update.go:4472-4474`) is unreachable; a slow
sync reads "replication not established ... linkStatus=down".

**Tests that pin impossible replies:**

- Master reply with `master_sync_in_progress:1`: `TestVerifyNewMasterReady_RejectsAMasterStillSyncing`
  ([`sentinel_failover_test.go:1001-1011`](../../internal/controller/sentinel_failover_test.go#L1001-L1011)),
  row "a full sync in progress is not synced" of `TestCheckCluster_ReplicaAccounting`
  ([`checker_live_test.go:293-298`](../../internal/health/checker_live_test.go#L293-L298)), row
  "full resync still running" of `TestCheckReplicaSync`
  ([`checks_endpoint_test.go:73-79`](../../internal/observer/checks_endpoint_test.go#L73-L79); the
  fake node [`fake_endpoint_test.go:301-307`](../../internal/observer/fake_endpoint_test.go#L301-L307)
  writes the field into masters too).
- Replica reply with link `up` and sync `true`: `rolling_update_test.go:3792-3798`, `:4014-4020`,
  [`topology_restore_stall_test.go:143-150`](../../internal/controller/topology_restore_stall_test.go#L143-L150),
  [`drain_test.go:305-311`, `:700-706`](../../internal/sidecar/drain_test.go#L700-L706).
- e2e: subtest "Wait for TLS replication sync"
  ([`tls_test.go:1301-1311`](../../test/e2e/tls_test.go#L1301-L1311)) waits for
  `connected_slaves:2`, already established at `:1266`, and for the absent master flag, so it
  cannot fail.

**Impact:**

- **Sentinel roll, `spec.replicas >= 3`:** the former master is deleted while the other current
  replica is still in `wait_bgsave` or loading. For the resync the tier holds one current copy;
  if the new master fails in that window, its writes since the promotion are lost, and during the
  load no complete copy exists (on a non-persistent tier, the dataset). Needs a second fault; the
  window grows with the dataset. The `replicas: 2` shape is T12's and not changed here.
- **Status of a Sentinel cluster:** after any replica full sync, phase `OK` and
  `Ready=True/HAClusterReady` while that replica holds nothing; `Syncing` appears only while fewer
  replicas are attached. A replica that restarts its sync over and over reads `OK` indefinitely,
  so the chart's `ValkeyPhaseNotOK` alert (30 min, default off) never fires.
- **Observer:** `replica_sync` never goes red on a bulk sync, against its CRD description.
- **Records:** ADR 0007 and ADR 0026 (including D11's *Replacement* argument, `:473-477`) state a
  gate the code does not have.

## Required changes

### Independent of the open questions

1. Correct in place, without citing this ticket: ADR 0007 `:517-519`, ADR 0026 `:475-476` and
   `:790-791` (the gate is role, available and at least one attached replica); the comments at
   `checker.go:32`, `:38`, `checks.go:91`, `rolling_update.go:2980-2982`, `:3011`.
2. Rewrite the five impossible replica fixtures to the state Valkey sends during a transfer,
   `master_link_status:down` with `master_sync_in_progress:1`.
3. Replace the body of "Wait for TLS replication sync" with a replica-side wait over TLS on both
   replicas (`master_link_status:up` via `valkeyTLSExecAllowError`); the plain-port helpers
   `waitForConnectedReplicas`/`waitForReplicaSynced` do not work on this TLS-only cluster. Run the
   TLS e2e on both Valkey lines.

### Depends on the answers

Under A:

- Move the predicate of `replicationNotEstablishedReason` into `valkeyclient` (a method on
  `ReplicationInfo`); build `replicationNotEstablishedReason` and `isSyncedReplica` on it. Remove
  the unreachable message at `rolling_update.go:4473` or mark it unreachable.
- `CheckCluster`: keep every pod's reply that `findMaster` already collects; `AllSynced` and
  `ReadyReplicas` count replies with the full answer. No additional dial.
- `verifyNewMasterReady`: after finding the new master, call `verifyReplacedReplicasSynced` with
  the same pods (skips the outgoing pod, bounded by `syncTimeout`, pauses on expiry).
- Observer `checkReplicaSync`: ask each data pod; `replicas - 1` must give the full answer.
- Rewrite the three master-fixture tests on replica-side fixtures; drop `syncInProgress` from the
  observer fake node for masters.
- Amend ADR 0007 D10 to cover the delete after the promotion; restate the item 1 lines; update
  [package-map.md:80](../developer/package-map.md) and the README `Syncing` row if their wording
  changes.
- Tests: `verifyNewMasterReady` with the new master at `connected_slaves:1` and the other current
  replica `link down` refuses the delete and arms the sync-wait bound, past `syncTimeout` it
  pauses; `CheckCluster` with one replica `link down` gives `AllSynced=false`; `checkReplicaSync`
  of the same shape fails. Revert check: putting each site back to the master-side term turns its
  test red. Full e2e on both lines.

Under D: the `verifyNewMasterReady` and observer items of A; C at `CheckCluster`.

Under C: delete the three terms and the three impossible master rows; rewrite the CRD description
(`api/v1`, `make manifests`, `README.md:433`) to say an attached count.

In every case: `grep -rn 'MasterSyncInProgress' internal/ --include='*.go' | grep -v _test.go`
names only the parser (and the shared predicate under A); `make test-unit`, `make lint`,
`make cyclo`, `make test-integration`.

## Open questions

### Q1: Which signal answers "every replica holds the dataset" at the three sites?

Today each site counts attached replicas on the master, which includes replicas that have not
loaded anything. The choice does not touch the promotion gates of ADR 0007 D10, the
`replicas: 2` shape, or the `DBSIZE` gap of `verifyNewMasterReady`.

- **A - ask the replicas with the full replication answer at all three sites (recommended).**
  Cost M. A Sentinel roll waits for the redirected replica's resync before deleting the former
  master; the status shows `Syncing` and `Ready=False` during every replica full sync.
- **D - A at the roll gate and the observer, `CheckCluster` corrected only (C).** Protects the
  delete, leaves the status alone; `Ready` does not flap on an eviction or drain, but keeps
  reading "ready" about an empty replica and `ValkeyPhaseNotOK` never fires for a sync that never
  completes.
- **C - delete the terms and correct the records.** Cost S, no behaviour change; the delete keeps
  passing during the resync and the status keeps reading `OK`.

A is recommended: it guards the one irreversible step with code that already exists and is
tested, and `Ready` and the phase then report what the data plane holds, as ADR 0002 D5a defines
`Ready`, at no extra dial.

**Answer:** _open_

## Not verified

- On Kubernetes, whether the post-failover pass reaches `verifyNewMasterReady` inside the resync
  (10 s requeue against a dataset-dependent resync); a Kind Sentinel roll with a writer and a
  large dataset, logging the new master's `INFO` at the delete, would settle it.
- Which dataset a redirected replica serves in `wait_bgsave` (by reading its previous one, since
  `repl-diskless-load` stays `disabled`); a measurement would settle it.
- What Sentinel promotes if the new master fails while the redirected replica is in `wait_bgsave`
  or loading.
- That the observer's `replica_read_test` goes red during a full sync (by reading yes; not run).
- Under A, whether the sync-wait annotation is clear when `replaceRemainingPods` is entered, and
  what the pass after a pause there does with the former master; trace it during implementation.

## Related

- [T12](012-no-write-fencing-min-replicas-to-write-as-an-opt-in-field.md): source; owns the `replicas: 2` shape.
- [T23](023-pauserollingupdate-records-no-pause.md): its Sentinel resume-gap analysis rests on `AllSynced` counting a mid-sync replica; changes under A.
- [T18](018-ready-keeps-its-pre-roll-value-during-a-rolling-update.md): its option B assumes `True/HAClusterReady` between replacements; under A that pass reads `False/ReplicationSyncing`.
- [T76](076-the-exec-probes-pass-on-any-server-reply.md): owns the probe passing on `LOADING`.
