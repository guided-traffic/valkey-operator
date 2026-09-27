---
id: T36
title: a non-persistent master that crash-restarts comes back empty and flushes its replicas
state: analysed       # every variant measured on the Valkey side, every decision costed; the Kind reproduction precedes the implementation
severity: high        # the whole dataset or every write since a sync is lost on every pod; measured in docker, not on Kubernetes
security: none
urgency: next         # severity >= medium and the trigger is reachable in released code (rule 3)
effort: L             # Sentinel guard S-M, non-Sentinel recovery and guard M+S, the replaced-pod init check, e2e on both Valkey lines, the Kind reproduction
blocked-by: decision  # the mitigation; the Sentinel half lands with or after T35 change 2
filed-from: T35
opened: 2026-09-27
decided:
done:
---

# T36 - a non-persistent master that crash-restarts comes back empty and flushes its replicas

## Current state

**The mechanism.** The data init writes the chosen config once into a writable `emptyDir`
([`statefulset.go:333`, `:336`, `:346-348`](../../internal/builder/statefulset.go) with Sentinel,
[`:524-536`](../../internal/builder/statefulset.go) without; volume
[`:233-238`](../../internal/builder/statefulset.go), [`:411-416`](../../internal/builder/statefulset.go)).
Init containers do not re-run on a container restart and an `emptyDir` survives it (documented
Kubernetes behaviour). A restarted `valkey-server` (PID 1,
[`statefulset.go:822-833`](../../internal/builder/statefulset.go)) therefore boots on the config
of its first start, on the same address, with whatever `/data` holds. The replicas' partial
resync is refused, and after `repl-diskless-sync-delay 5`
([`configmap.go:179`](../../internal/builder/configmap.go)) they full-resync from it and drop
what they held. Valkey 8.1.9 and 9.1.1 have no guard against this (measured in docker; upstream
valkey-doc `topics/replication.md:43-61` documents it, the Sentinel case included).

What the restarted master holds depends on how it got the role:

| Variant | Config on restart | `/data` on restart | Outcome (measured in docker, both pins) |
|---|---|---|---|
| booted with the master config: ordinal-0 fallback ([`statefulset.go:342-349`](../../internal/builder/statefulset.go), [`:531-537`](../../internal/builder/statefulset.go)), Sentinel named the booting pod ([`:331-333`](../../internal/builder/statefulset.go)), known-master self-claim ([`:506-507`](../../internal/builder/statefulset.go), [`:528-530`](../../internal/builder/statefulset.go)) | master | empty (`save ""`) | every pod flushes to 0 keys about 5-7 s after the restart |
| promoted by Sentinel (any failover, the roll's included) | master: Sentinel's `CONFIG REWRITE` drops `replicaof` | `dump.rdb` of its last full sync (`rdb-del-sync-files no`) | replicas full-resync to the snapshot; writes since that sync lost (1000 to 500 keys) |
| promoted with `REPLICAOF NO ONE` ([`rolling_update.go:4202`](../../internal/controller/rolling_update.go), [`:4610`](../../internal/controller/rolling_update.go), [`valkey_controller.go:2949`](../../internal/controller/valkey_controller.go), [`drain.go:172`](../../internal/sidecar/drain.go)); every non-Sentinel roll ends this way for pod-0 | replica of the address its init wrote, often its own current replica | stale `dump.rdb` | no master: both sides refuse `PSYNC` with `NOMASTERLINK`, `-rw` has no endpoint; `checkAndRecoverNoMaster` then promotes the hardcoded `<sts>-0` ([`valkey_controller.go:2925`](../../internal/controller/valkey_controller.go)) with no dataset comparison; after a roll that is the restarted pod, so every write since the roll is lost (500 of 1000 on all three) |

**Persistent tiers in mode `rdb`** (the CRD default mode,
[`valkey_types.go:999`](../../api/v1/valkey_types.go); save points
[`configmap.go:206-208`](../../internal/builder/configmap.go)) are affected the same way after a
`SIGKILL`-type exit: the master reloads its last save and the replicas roll back to it (1000 to
500 keys). A graceful stop saves first and loses nothing. Mode `aof`/`both` bounds the loss to
about the fsync interval.

**A replaced master** on a non-persistent non-Sentinel tier (node failure, no drain) gets a new
`emptyDir`, reads its own name as known master
([`statefulset.go:505-508`](../../internal/builder/statefulset.go)) and boots empty as master;
the replicas flush. ADR 0008's residual "the mirror case of D8" records this and the pod-0
recovery as open.

**Why nothing catches it.**

- Sentinel: `down-after-milliseconds` is 5000 ([`sentinel.go:47`](../../internal/builder/sentinel.go));
  the first restart after 10 minutes of healthy running is immediate and an empty Valkey
  answers within about 1 s, so no failover. `master-reboot-down-after-period` cannot help: it
  fails over only a master still answering `-LOADING` (measured: at 1000 ms only `+reboot`).
- Non-Sentinel: `checkSteadyStateSplitBrain` sees one master
  ([`steady_state_master.go:151-181`](../../internal/controller/steady_state_master.go)) and
  `checkAndRecoverNoMaster` returns on `hasMaster` ([`valkey_controller.go:2905`](../../internal/controller/valkey_controller.go)).
- The probes are `valkey-cli ping` ([`statefulset.go:847-870`](../../internal/builder/statefulset.go)),
  which exits 0 on any reply, errors and `-LOADING` included.
- The observer counts no keys ([`checks.go:92-159`](../../internal/observer/checks.go)); nothing
  on the CR records the flush. The `REPLICAOF NO ONE` variant shows only a transient phase
  `Error` ([`valkey_controller.go:2913-2916`](../../internal/controller/valkey_controller.go)).

**The triggers are routine.** No `maxmemory` is set ([`configmap.go:126-135`](../../internal/builder/configmap.go),
T71), so an OOM kill at the memory limit restarts the master; the liveness probe (period 10 s,
timeout 5 s, threshold 5, [`statefulset.go:859-870`](../../internal/builder/statefulset.go))
kills a master that gives no reply for about 40-60 s; any crash. Non-persistent is the default
([`valkey_types.go:993-996`](../../api/v1/valkey_types.go)).

**Impact.** Users of any multi-replica cluster: one master container restart discards data on
every pod, although the replicas held it a second earlier (amounts per variant above).

## Required changes

Standing rules for every change: record before promote (ADR 0009 D6, D7), provenance (ADR
0020), bounded waits (ADR 0010), no new tool in `RequiredImageTools` (write the marker by shell
redirection, not `touch`), and an e2e on both Valkey lines with its revert check (ADR 0017).

### Independent of the open questions

- Kind reproduction first (3 non-persistent replicas, with and without Sentinel; one session
  with T35 decision 3), settling the items under Not verified. Crash trigger
  `kubectl exec ... valkey-cli SHUTDOWN NOSAVE NOW`, stall trigger `CLIENT PAUSE 70000 ALL`
  (`DEBUG` is refused, `kill -9 1` inside the container does nothing). Record restart latency,
  Sentinel `+sdown`, the replicas' `master_replid` and final `DBSIZE`; run a fresh cluster, a
  post-roll one, an OOM kill and a persistent `rdb` tier.
- [docs/operations/persistence.md:24-25](../operations/persistence.md): the advice "enable
  persistence" is incomplete; add that mode `rdb` still rolls every replica back to the last
  save after an OOM kill or crash, and that `aof`/`both` bounds the loss to about the fsync
  interval. Cite no ticket.

### Depends on the answers

- The mitigation per Q1-Q6, with unit tests (builder, `fakeValkeyServer`) and an e2e asserting
  the replicas keep their `DBSIZE` across a master container restart.
- ADRs: the restart-guard rule (shared with T35 C4: "an empty data pod does not take the master
  role while Sentinel knows a replica", with its bound); ADR 0028 extended from the demotion to
  the recovery promotion; ADR 0008's residual amended or superseded.
- On close: operator consequence into [docs/operations/persistence.md](../operations/persistence.md),
  the guard and the recovery into `docs/developer/`.

## Open questions

### Q1: On a Sentinel tier, what stops a restarted master from serving its replicas an older dataset?

The fix is a wrapper in the Valkey container command that uses a marker in the config
`emptyDir` (marker present, config without `replicaof` = restart of a master). It rolls the data
tier once through the pod-spec hash, lossless for more than one data pod; the Sentinel tier does
not roll.

- **Hold guard (recommended):** while Sentinel names this pod, do not start `valkey-server`;
  the pod is not Ready, Sentinel fails over after 5 s, then append `replicaof <named>` and
  `exec`. Cost S-M (auth and TLS flags of the Sentinel query at
  [`statefulset.go:254-268`](../../internal/builder/statefulset.go)); outage about 6 s; needs Q5.
- **Boot-as-replica guard:** append `replicaof <a peer>` and start at once; peers refuse with
  `NOMASTERLINK`, Sentinel's role-mismatch rule fails over after about 25-35 s. Cost S; the pod
  is Ready as `replica` and serves empty or stale reads through `-r` meanwhile.

Hold guard: measured in docker it kept 500 of 500 keys with a 6 s outage, against 0 without it;
it avoids the 25 s role-mismatch wait and stale reads. It must share one shell function and one
condition set with T35's data-init query (C4), so it lands with or after T35 change 2.

**Answer:** _open_

### Q2: On a non-Sentinel tier, what stops a restarted master from wiping or rolling back its replicas?

Nothing arbitrates today; the post-roll variant ends in the `NOMASTERLINK` deadlock and the
recovery promotes pod-0 (the restarted pod). The recovery keeps its `unreachable > 0` refusal
([`valkey_controller.go:2905`](../../internal/controller/valkey_controller.go)) and its
suppression during a roll ([`:2886-2888`](../../internal/controller/valkey_controller.go)).

- **(a) data-aware recovery plus (b) boot-as-replica guard (recommended):** (a)
  `checkAndRecoverNoMaster` promotes the available pod with the newest data (Q3), recording it
  first and refusing on an unreadable answer; (b) a restarted master boots as a replica of a
  peer, reaching the deadlock that (a) resolves. Cost M + S; (a) rolls nothing, (b) rolls every
  multi-replica non-Sentinel data tier once. Writes fail until the recovery runs (by reading
  5-20 s).
- **(a) alone:** cost M, rolls nothing; covers the post-roll state but not a cluster that never
  rolled, which still loses everything (booted-as-master variant).

(a) plus (b): in the measured deadlock pod-0 had 500 keys at offset 0 and its peers 1000 at
offset 16581, so a data-aware choice loses nothing; (b) also covers fresh clusters while keeping
every pod reachable. Land (a) first.

**Answer:** _open_

### Q3: Which pod does the data-aware recovery promote (only if Q2 takes (a))?

`ReplicationInfo` has no offset or replid ([`client.go:26-33`](../../internal/valkeyclient/client.go));
`dbSizeReader` exists ([`rolling_update.go:1678`](../../internal/controller/rolling_update.go)).

- **Highest `master_repl_offset`, ADR 0028 D1 zero-key veto, D3 fail-closed, tie to lowest
  ordinal (recommended):** parse one more field, one `DBSIZE` per candidate.
- **Highest `DBSIZE`, fail-closed:** smallest change; a workload that deletes keys ranks a stale
  snapshot above the current dataset.

The offset measures replication progress, which is what the loss is measured in; the veto keeps
what ADR 0028 decided.

**Answer:** _open_

### Q4: Which data tiers render the guards?

Every tier with `spec.replicas > 1` has the config `emptyDir` (`needsInitContainer`,
[`statefulset.go:645-647`](../../internal/builder/statefulset.go)). A single-pod tier renders no
guard in any option: it has no peer, and the roll would discard its only non-persistent pod.

- **Every multi-replica tier, `spec.replicas > 1` (recommended):** no persistence branch; rolls
  every multi-replica data tier once; also turns the `-LOADING` reload outage of persistent
  tiers into a 6 s failover. A graceful master restart on a persistent tier becomes a failover too.
- **Non-persistent and persistent `rdb` tiers:** covers every measured loss; saves one lossless
  roll of `aof`/`both` tiers at the cost of a mode branch and a test dimension.
- **Non-persistent only:** smallest roll; leaves the measured `rdb` rollback.

Every multi-replica tier: simplest condition, and Sentinel counts `-LOADING` as available, so a
large persistent reload is a write outage the guard removes. If the Kind run shows the `-LOADING`
window to be negligible, take the `rdb`-scoped option instead.

**Answer:** _open_

### Q5: What does the hold guard do when no failover comes (only if Q1 takes the hold)?

Without a quorum or a selectable replica Sentinel keeps naming the held pod; the liveness probe
kills it 55-65 s after start and the guard runs again with back-off. The bound must stay below
55 s.

- **Ask the peers, hold, then fall back to a replica (recommended):** every peer `DBSIZE` 0 ->
  start at once; otherwise hold, and after the bound append `replicaof <a peer>`. Cost: a peer
  query with its own flags (TLS port 16379, `--tls --cacert`, password whenever auth is on,
  [`statefulset.go:425-433`](../../internal/builder/statefulset.go) has them for the non-Sentinel
  init only). Never crash-loops; an unreachable peer counts as "may hold data".
- **Hold, then fall back, no peer query:** an all-empty tier waits for nothing; a tier whose
  Sentinels know no replica sits masterless.
- **Hold while a peer may hold data, then exit non-zero:** no stale reads, but `CrashLoopBackOff`
  and up to 300 s to rejoin.

Ask-hold-fall back is the only option correct in all three boundary cases (all-empty, no quorum
with data, Sentinels knowing no replica).

**Answer:** _open_

### Q6: What happens to a replaced (not restarted) recorded master on a non-persistent non-Sentinel tier?

A new pod has a new `emptyDir` and no marker, so no guard fires; ADR 0008 records it as open. A
drain prevents it for an ordinary delete; a hard node failure runs none. The Sentinel counterpart
is T35's C4.

- **Peer-data check in the init's master branches (recommended):** Phase 2 self-claim and Phase 3
  ordinal-0 fallback boot as a replica when any peer answers `DBSIZE > 0`; Q2 (a) then promotes
  the pod with the data. A few shell lines in the existing peer loop
  ([`statefulset.go:425-433`](../../internal/builder/statefulset.go)); lands in the same roll as
  Q2 (b); e2e deletes the recorded master with `--grace-period=0`.
- **Accept the residual:** record it in ADR 0008 and docs/operations/persistence.md; cost XS; a
  documented data-loss path on node failure stays open.

The peer check answers ADR 0008's "new or cluster empty" question by asking the peers, which the
init already does in Phase 1, at the cost of a few lines.

**Answer:** _open_

## Not verified

- The chain on Kubernetes end to end and the kubelet's first-restart latency (Kind reproduction).
- The time from a post-roll pod-0 restart to `checkAndRecoverNoMaster`; by reading 5-20 s.
- The hold guard with three Sentinels and quorum 2 (docker ran one Sentinel, quorum 1).
- The `-LOADING` duration of a large persistent reload and the AOF loss window (decides Q4).
- A roll whose topology restoration was abandoned (`abandonTopologyRestoration`,
  [`rolling_update.go:4511`](../../internal/controller/rolling_update.go)) may lose more than the
  writes since the roll; read, not measured.
- The persistence settings of the wds18 and production clusters (`kubectl get valkey -A` over
  `spec.persistence.enabled`).

## Related

- T35 - its decision 3 (C4) defines the Sentinel query Q1 and Q5 share; its Impact C is Q6's Sentinel counterpart.
- T71 - `maxmemory` is never set, which makes an OOM kill a reachable trigger.
- T76 - the probes pass on any server reply, `-LOADING` included.
- T23 - its D1 option D relies on the no-master recovery that Q2 (a) makes data-aware.
- T62 - a Sentinel-side route into this mechanism after a reset.
