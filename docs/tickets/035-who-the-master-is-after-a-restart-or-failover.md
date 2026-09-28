---
id: T35
title: who the master is after a restart or failover - lagging master records, an empty restarted master, probes that pass on a loading server
state: analysed       # the master-record part is decided; the restart guards and the probe gate are analysed only
severity: high        # a restarted non-persistent master discards the dataset on every pod (measured in docker)
security: none
urgency: now          # tracked comments and pages state a master record and a probe authentication the code does not have
effort: L             # records change M, restart guards and data-aware recovery M+S+S, sidecar gate M, Kind reproduction, e2e on both Valkey lines
blocked-by: decision  # Q1-Q10; the items marked independent are not blocked
filed-from: check of the v1.13.0 upgrade on wds18-k8s-main, namespace database-examples, 2026-09-26
opened: 2026-09-26
decided: 2026-09-27   # the master-record part only
done:
---

# T35 - who the master is after a restart or failover - lagging master records, an empty restarted master, probes that pass on a loading server

**Scope.** After a failover, a roll or a container restart, several places decide or record which
pod is the master, and each can be wrong for a while or for good: the records the operator and the
sidecar keep, the boot decision of a data pod's init and of a restarted `valkey-server`, and the
readiness verdict that routes clients. The package is one theme because the fixes share one boot
rule, one Sentinel query in the generated shell, one Kind reproduction and one e2e setup.

- Master records lag the real master (labels, `status.masterPod`, `known-master`, ConfigMaps).
- A master that crash-restarts or is replaced comes back empty or stale and flushes its replicas.
- The exec probes pass on any server reply, so a loading or busy data pod is Ready.

## Current state

### Master records lag the real master

Four records say who the master is, each on its own clock:

| Record | Writer | Moves |
|---|---|---|
| `instanceRole` pod label (`-rw`/`-r` selector) | the pod's sidecar labeler, 1 s poll of `INFO replication`; on Sentinel clusters confirmed by `SENTINEL MASTER` `ip` ([`labeler.go:135-145`](../../internal/sidecar/labeler.go), [`:351-363`](../../internal/sidecar/labeler.go)) | at most 1 s after the role change; on Sentinel clusters only after the failover leader's `+switch-master` |
| `status.masterPod` | `updateStatus`: from the label on the non-Sentinel arm ([`valkey_controller.go:2252`, `:2324-2342`](../../internal/controller/valkey_controller.go)), from `findMaster`'s `INFO` answer on the Sentinel arm ([`:2477`, `:2490`](../../internal/controller/valkey_controller.go)) | at the next pass |
| `vko.gtrfc.com/known-master` | the operator on its own promotions; best-effort at Sentinel roll finalization ([`rolling_update.go:1034-1048`](../../internal/controller/rolling_update.go)) | never on a Sentinel failover outside a roll |
| replica ConfigMap `replicaof`, Sentinel ConfigMap monitor line | the operator, from the annotation, at the start of a pass ([`valkey_controller.go:552-560`](../../internal/controller/valkey_controller.go)) | with the annotation; excluded from the config hash |

A relabel alone triggers no pass: there is no Pod watch
([`valkey_controller.go:2984-3002`](../../internal/controller/valkey_controller.go)), a healthy pass
returns no requeue ([`:373-396`](../../internal/controller/valkey_controller.go)), and the cache
resync is the controller-runtime default of 10 h. Role changes without a pod death (the relabel that
closes a topology restoration, a manual `SENTINEL FAILOVER` or `REPLICAOF`, the labeler's
cross-check flip, a Sentinel failover of a stalled master) leave every record to the resync.

- **A - `status.masterPod` names a replica after a non-Sentinel roll.** The completing pass finds
  the sole master label stale ([`steady_state_master.go:261-267`](../../internal/controller/steady_state_master.go);
  terminating branch `:249-253`), logs and returns; `currentMasterPod` then writes the labelled pod,
  not yet relabelled, and nothing re-enters. Measured on wds18: `valkey9` showed the replica
  `valkey9-1` for more than 27 minutes. The field means "the pod `-rw` selects" without Sentinel and
  "the pod answering `role:master`" with it. The operator does not read it; people using the MASTER
  column in Lens or `kubectl get valkey` act on a replica.
- **B - `known-master` names a replica after a Sentinel failover outside a roll.** Measured on wds18
  (`valkey8-sentinal-tls`): after `-1` was killed and `-0` promoted, annotation and monitor line
  still named `-1`. The Sentinel init validates its target by `ROLE` and a pod scan
  ([`sentinel.go:654-692`](../../internal/builder/sentinel.go)); the data init's Phase 2 does not:
  after about 31 s without a Sentinel answer it adopts `replicaof`, and if that names the booting pod
  it takes the master config ([`statefulset.go:318-333`](../../internal/builder/statefulset.go)). An
  empty replacement of the recorded pod then becomes a second master, both labelled master (each
  labeler trusts its local role without Sentinel, [`labeler.go:144`](../../internal/sidecar/labeler.go)),
  and its writes are discarded when Sentinel returns. Lossy and unlikely: the whole Sentinel tier
  must be silent.
- **C - a killed master's replacement boots as a second, empty master for about 5-10 s.** The data
  init takes the first Sentinel answer to `get-master-addr-by-name`
  ([`statefulset.go:288-316`](../../internal/builder/statefulset.go)) and boots as master when it
  names itself (`:331-333`); before `+promoted-slave` every Sentinel names the dead master. On wds18
  the force-deleted master's replacement ran its init inside that window and the health checker
  logged "Multiple masters detected" until Sentinel converted it. The readiness probe kept it out of
  `-rw`; a client discovering the master through Sentinel or the headless name could have written to
  it. `MultipleMasters` stayed `False`: its only evaluator is the rolling-update resolver
  ([`condition_registry.go:139-150`](../../internal/controller/condition_registry.go)). A replacement
  answering before `down-after` (5 s) without a forced failover is never failed over, and the
  replicas full-resync from it (inference; rare, the drain almost always forces the failover).
- **L - on Sentinel clusters the labels trail every failover.** The leader's `SENTINEL MASTER` `ip`
  keeps the old master until its own `+switch-master` (every replica link up, or `failover-timeout`
  60 s); its `get-master-addr-by-name` switches 7-14 ms after `+promoted-slave`. `sentinel-0`, which
  the labeler asks first, leads every roll- and drain-forced failover. Measured lag: 1.07-1.11 s
  (docker, both pins), 1.15 s (wds18), 5.38 s with a full resync (Kind). After a drain `-rw` is empty
  for that time; on a roll it routes to the outgoing master, whose writes are discarded at its
  conversion (inference). Every release rolls the four production Sentinel CRs.

False tracked statements: the Sentinel-path record is said to follow a Sentinel failover or only
seed a restarting Sentinel at [`rolling_update.go:1040-1041`](../../internal/controller/rolling_update.go),
[`configmap.go:141-146`](../../internal/builder/configmap.go),
[`sentinel.go:24-28`](../../internal/builder/sentinel.go) and the init shell comment at
[`statefulset.go:318-320`](../../internal/builder/statefulset.go) (false), and at
[`configmap.go:42-44`](../../internal/builder/configmap.go),
[`sentinel.go:77-79`](../../internal/builder/sentinel.go) (misleading). `status.masterPod` is said to
be the live master at [`valkey_types.go:81-82`](../../api/v1/valkey_types.go),
[`docs/operations/status.md:25`](../operations/status.md#topologyrestored),
[ADR 0010:389](../adr/0010-every-rolling-update-wait-is-bounded.md) and
[`condition_registry.go:254`](../../internal/controller/condition_registry.go).

### A restarted or replaced master comes back empty or stale

The data init writes the chosen config once into a writable `emptyDir`
([`statefulset.go:333`, `:336`, `:346-348`](../../internal/builder/statefulset.go) with Sentinel,
[`:524-536`](../../internal/builder/statefulset.go) without; volume
[`:233-238`](../../internal/builder/statefulset.go), [`:411-416`](../../internal/builder/statefulset.go)).
Init containers do not re-run on a container restart and an `emptyDir` survives it, so a restarted
`valkey-server` (PID 1, [`statefulset.go:822-833`](../../internal/builder/statefulset.go)) boots on
the config of its first start, on the same address, with whatever `/data` holds. The replicas'
partial resync is refused and after `repl-diskless-sync-delay 5`
([`configmap.go:179`](../../internal/builder/configmap.go)) they full-resync from it. Valkey 8.1.9
and 9.1.1 have no guard (measured in docker; upstream valkey-doc `topics/replication.md:43-61`
documents it, the Sentinel case included).

| How it got the role | Config on restart | `/data` on restart | Outcome (docker, both pins) |
|---|---|---|---|
| booted with the master config: ordinal-0 fallback ([`statefulset.go:342-349`](../../internal/builder/statefulset.go), [`:531-537`](../../internal/builder/statefulset.go)), Sentinel named the booting pod ([`:331-333`](../../internal/builder/statefulset.go)), known-master self-claim ([`:506-507`](../../internal/builder/statefulset.go), [`:528-530`](../../internal/builder/statefulset.go)) | master | empty (`save ""`) | every pod flushes to 0 keys about 5-7 s after the restart |
| promoted by Sentinel (any failover, the roll's included) | master: Sentinel's `CONFIG REWRITE` drops `replicaof` | `dump.rdb` of its last full sync (`rdb-del-sync-files no`) | replicas full-resync to the snapshot; writes since that sync lost (1000 to 500 keys) |
| promoted with `REPLICAOF NO ONE` ([`rolling_update.go:4202`](../../internal/controller/rolling_update.go), [`:4610`](../../internal/controller/rolling_update.go), [`valkey_controller.go:2949`](../../internal/controller/valkey_controller.go), [`drain.go:172`](../../internal/sidecar/drain.go)); every non-Sentinel roll ends this way for pod-0 | replica of the address its init wrote, often its own current replica | stale `dump.rdb` | no master: both sides refuse `PSYNC` with `NOMASTERLINK`, `-rw` has no endpoint; `checkAndRecoverNoMaster` promotes the hardcoded `<sts>-0` ([`valkey_controller.go:2925`](../../internal/controller/valkey_controller.go)) with no dataset comparison; after a roll that is the restarted pod (500 of 1000 on all three) |

- **Persistent tiers in mode `rdb`** (the CRD default mode, [`valkey_types.go:999`](../../api/v1/valkey_types.go);
  save points [`configmap.go:206-208`](../../internal/builder/configmap.go)) roll every replica back
  to the last save after a `SIGKILL`-type exit (1000 to 500 keys). A graceful stop saves first;
  `aof`/`both` bounds the loss to about the fsync interval.
- **A replaced master** on a non-persistent non-Sentinel tier (node failure, no drain) gets a new
  `emptyDir`, reads its own name as known master ([`statefulset.go:505-508`](../../internal/builder/statefulset.go))
  and boots empty as master; the replicas flush. ADR 0008's residual "the mirror case of D8" records
  this and the pod-0 recovery as open. Its Sentinel counterpart is item C above.
- **Why nothing catches it.** Sentinel `down-after-milliseconds` is 5000
  ([`sentinel.go:47`](../../internal/builder/sentinel.go)); the first restart after 10 minutes of
  healthy running is immediate and an empty Valkey answers within about 1 s, so no failover.
  `master-reboot-down-after-period` fails over only a master still answering `-LOADING` (measured: at
  1000 ms only `+reboot`). Without Sentinel, `checkSteadyStateSplitBrain` sees one master
  ([`steady_state_master.go:151-181`](../../internal/controller/steady_state_master.go)) and
  `checkAndRecoverNoMaster` returns on `hasMaster` ([`valkey_controller.go:2905`](../../internal/controller/valkey_controller.go)).
  The probes pass on any reply (next part). The observer counts no keys
  ([`checks.go:92-159`](../../internal/observer/checks.go)); nothing on the CR records the flush; the
  `REPLICAOF NO ONE` variant shows only a transient phase `Error`
  ([`valkey_controller.go:2913-2916`](../../internal/controller/valkey_controller.go)).
- **The triggers are routine.** No `maxmemory` is set ([`configmap.go:126-135`](../../internal/builder/configmap.go)),
  so an OOM kill restarts the master; liveness kills a master that gives no reply for about 40-60 s;
  any crash. Non-persistent is the default ([`valkey_types.go:993-996`](../../api/v1/valkey_types.go)).
- **Impact.** On any multi-replica cluster one master container restart discards data on every pod,
  although the replicas held it a second earlier.

### The exec probes pass on any server reply

Every exec probe is one `valkey-cli ... ping`, and the kubelet reads only its exit code. Without
`-e`, `valkey-cli` exits 1 only when it cannot connect; an error reply to `AUTH` or `PING` is printed
and the process exits 0. The verdict is "a server answered within `timeoutSeconds`".

- Data tier: `ProbeCommand` ([`statefulset.go:1512-1545`](../../internal/builder/statefulset.go))
  builds four forms (auth/TLS combinations), none with `--no-auth-warning` or `-e`. It is readiness
  (delay 5 s, period 5 s, timeout 3 s, threshold 3) and liveness (delay 15 s, period 10 s, timeout
  5 s, threshold 5) of the `valkey` container ([`statefulset.go:847-870`](../../internal/builder/statefulset.go)).
  No startup probe.
- Sentinel tier: `SentinelProbeCommand` ([`sentinel.go:412-459`](../../internal/builder/sentinel.go))
  builds the same shape on 26379/36379 when TLS or auth is on, otherwise a `tcpSocket` probe.
- The sidecar's readiness is `GET /readyz` (delay 3 s, period 3 s, timeout 2 s, threshold 3,
  [`statefulset.go:1008-1020`](../../internal/builder/statefulset.go)), 200 once `SetReady` was
  called ([`health.go:36-38`](../../internal/sidecar/health.go), [`:60-68`](../../internal/sidecar/health.go));
  the only caller is the labeler poll after its first successful `DetectRole`, one
  `INFO replication` with the start-time password ([`labeler.go:124-133`](../../internal/sidecar/labeler.go),
  [`:190-204`](../../internal/sidecar/labeler.go)). Nothing resets it: latched for the container's life.
- Tests assert command strings only; image-tools asserts stdout `PONG` for the no-auth, no-TLS data
  probe ([`restricted_runtime_test.go:104`](../../test/imagetools/restricted_runtime_test.go)). No
  test pins an exit code.

Measured (docker, `valkey/valkey:9.1.1` and `8.1.9`, the exact generated strings, no TTY):

| Server state | Probe prints | Exit |
|---|---|---|
| healthy, right password | `PONG` | 0 |
| wrong or empty password, or `requirepass` and no `-a` | `AUTH failed: WRONGPASS ...` / `NOAUTH ...` | 0 |
| loading after a restart or a full sync | `LOADING Valkey is loading the dataset in memory` | 0 |
| script past `busy-reply-threshold` | `BUSY Valkey is busy running a script ...` | 0 |
| nothing listening, or TLS CA mismatch | `Could not connect ...` | 1 |
| process stopped (`SIGSTOP`) | no answer within the timeout | none |

During a load `INFO replication` answers normally and `INFO persistence` shows `loading:1`; during a
script `INFO replication` answers `-BUSY`. With `-e` the probe exits 1 on every error reply, a wrong
password included. The option C command (Q10) exits 0 on `PONG` and a wrong password, 1 on
`-LOADING`, `-BUSY` and no connection.

- **Impact.** A persistent data pod whose `valkey` container restarts, and any replica loading a
  full sync, answers `-LOADING` for the load yet is Ready: in the `-rw`, `-r` and `-all` Services
  ([`service.go:171-224`](../../internal/builder/service.go)), counted healthy by the data PDB and by
  `available()` ([`rolling_update.go:1899`](../../internal/controller/rolling_update.go)). `-BUSY`
  does the same.
- Liveness is right and stays: failing it on `-LOADING`, `-BUSY` or an auth error would kill long
  loads, discard script work and turn a password withdrawal into a crash loop.
- False statements: [`sentinel.go:429`](../../internal/builder/sentinel.go) says the probe "must
  authenticate"; [`authentication.md:51-52`](../operations/authentication.md) says the replacement
  becomes Ready because its readiness probe authenticates. It becomes Ready because the exec probe
  passes on any reply and the sidecar authenticates once with the same Secret value.
- Cosmetic: the auth forms lack `--no-auth-warning`, so every run writes the `-a` warning to stderr.
- **Rollout constraint.** `ComputePodSpecHash` covers the whole `PodSpec`
  ([`statefulset.go:1228-1238`](../../internal/builder/statefulset.go)). On the Helm path a new
  readiness command adds no roll (every release rolls multi-replica data tiers for the sidecar
  image). For a single data pod, `singlePodDeferral`
  ([`pod_security_migration.go:128-150`](../../internal/controller/pod_security_migration.go))
  decides by `isSidecarOnlyChange`, which compares images only
  ([`rolling_update.go:3841-3866`](../../internal/controller/rolling_update.go)): on kustomize or a
  floating tag the pod is replaced at once, a non-persistent one with its dataset, the ADR 0007 D7
  case (traced by reading).

## Required changes

Standing rules for every change here: record before promote (ADR 0009 D6, D7), provenance
(ADR 0020), bounded waits (ADR 0010), no new tool in `RequiredImageTools` (write a marker by shell
redirection, not `touch`), and an e2e on both Valkey lines with its revert check (ADR 0017).

### Shared across parts

- **Corrections (independent, XS each).**
  - Sentinel-path record: it moves only with operator promotions and roll finalization, so after a
    failover outside a roll it names the previous master, and Phase 2 self-claims on it.
    `status.masterPod`: after a non-Sentinel roll it can name the previous master until the next
    event; ADR 0010 gets a Status line. The shell comment is in the hashed PodSpec and rides the
    release's sidecar-image roll. Check: `git grep -nE "pre-seeds a restarting sentinel|post-failover (master|state)|after a (successful )?sentinel failover|live answer|Read .status.masterPod. for the master"`
    outside `docs/tickets` returns only corrected text.
  - Rewrite the comment at [`sentinel.go:429`](../../internal/builder/sentinel.go): the probe passes
    the password so a healthy Sentinel answers `PONG`; its verdict does not depend on it. Check:
    `git grep -n "must authenticate" -- internal/builder/sentinel.go` returns nothing.
  - Correct [`authentication.md:51-52`](../operations/authentication.md) to name both gates (T50 owns
    that page's rewrite; do it once, in whichever change lands first).
  - Record in [ADR 0007](../adr/0007-failover-aware-rolling-update.md) D9, with the measurement: both
    exec probes fail only on no connection or no answer; liveness keeps this on purpose; the sidecar
    gate latches at the first answered `INFO replication`, which a loading server gives.
  - [docs/operations/persistence.md:24-25](../operations/persistence.md): "enable persistence" is
    incomplete; mode `rdb` still rolls every replica back to the last save after an OOM kill or
    crash, `aof`/`both` bounds the loss to about the fsync interval. Cite no ticket.
- **One Kind reproduction session (independent).** 3 non-persistent replicas, with and without
  Sentinel. Crash trigger `kubectl exec ... valkey-cli SHUTDOWN NOSAVE NOW`, stall trigger
  `CLIENT PAUSE 70000 ALL` (`DEBUG` is refused, `kill -9 1` in the container does nothing); a
  force-deleted master (client-go, `GracePeriodSeconds: 0`; `kubectl --grace-period=0` without
  `--force` becomes 1). Record restart latency, Sentinel `+sdown`, the replicas' `master_replid` and
  final `DBSIZE`, the replacement's init and sidecar logs; run a fresh cluster, a post-roll one, an
  OOM kill, a persistent `rdb` tier and a large persistent reload (`-LOADING` duration, Q7, Q10).
- **One boot-rule ADR and one shell function (Q2, Q4, Q8).** "An empty data pod does not take the
  master role while Sentinel knows a replica", with its bound, serves the replacement's init (C4) and
  the restart wrapper (hold guard). Both use one Sentinel query function and one condition set in
  the generated shell of the Sentinel data init ([`statefulset.go:288-333`](../../internal/builder/statefulset.go);
  auth and TLS flags at [`:254-268`](../../internal/builder/statefulset.go)); the restart guard lands
  with or after C4. ADR 0028 extends from the demotion to the recovery promotion (Q5, Q6); ADR 0008's
  residual is amended or superseded (Q9).

### Master records

- **Records change (decided, M, one change):**
  - **A2.** `adoptUnrecordedPromotion` records the proven-stale pod on `passState`
    ([`foreign_object.go:89-146`](../../internal/controller/foreign_object.go)); `currentMasterPod`
    skips the label rule for it and falls to the known-master record, so the field means "the master"
    on both topologies. Amend ADR 0002 D11. Unit: record's pod when the label was proven stale in the
    pass, labelled pod otherwise.
  - **B2.** `CheckCluster`'s Sentinel round ([`checker.go:325-341`](../../internal/health/checker.go))
    keeps each Sentinel's master name. When a majority of answering Sentinels and the INFO master name
    ordinal P and the record (or its pod-0 default) names another, call `persistKnownMaster(P)`
    ([`rolling_update.go:1071-1089`](../../internal/controller/rolling_update.go)) after
    `persistStatus` (before it, the `Update` would discard in-memory conditions), log on failure, and
    `requestRecheck` so the next pass republishes both ConfigMaps. Sentinel path only. Amend ADR 0008
    D3. Unit: writes P on agreement; nothing on split Sentinels, disagreeing INFO or P outside the
    ordinals; never without Sentinel.
  - **A4.** A Pod watch next to the Secret watch: Update events with a changed `instanceRole` only,
    mapped to the CR by the `vko.gtrfc.com/cluster` label and namespace (`findValkeyForPod`,
    `instanceRoleChanged`). The Pod informer and `pods: watch` exist. Unit: predicate and map
    function. Integration: a label patch enqueues a reconcile, observed through a counting wrapper
    (envtest runs no kubelet). Amend ADR 0011 D21 and add one clause to each of the thirteen "no Pod
    watch" sentences found by `grep -rnE "Pod watch|no Pod *$" internal docs CLAUDE.md DEVELOPER.md`
    (the ADR 0011 heading at `:417` stays).
  - **`RWServiceMisrouted`.** A new level next to `RWServiceEmpty`
    ([`rw_service_report.go:37`](../../internal/controller/rw_service_report.go)): registry row,
    `ConditionType`, README row, `docs/operations/status.md` section, `docs/developer/package-map.md`
    row, one sentence in ADR 0012 D12; presence-guarded. Sentinel arm: judged after `CheckCluster` in
    the all-ready case, any master-labelled pod other than `clusterState.MasterPod`, untouched without
    a `clusterState`. Non-Sentinel arm: the A2 verdict. Unit: set, cleared, `RWServiceEmpty` untouched.
  - **Proof (Kind):** after `TestE2E_RollingUpdate_MultiReplicaNoSentinel`, `status.masterPod` equals
    the INFO master; on a Sentinel cluster with its master deleted, annotation, monitor line and
    `replicaof` name the new master within one recheck after `Ready`; full suite on both legs.
- **Labeler poll (decided: nothing).** The 1 s poll stays; reopen only if the observer's write-test
  failure rate over rolls shows the handover window matters.
- **(Q1) if A1 stays:** both proving branches call `requestRecheck(ctx, rollingUpdateRequeueDelay)`;
  amend ADR 0011 D12; unit tests for the stale and the terminating branch.
- **(Q2) under C4 (M):** the shared shell function above; a builder unit test and an ADR 0017 D19
  exec harness for that branch (none exists); an image-tools docker test during a forced failover
  held in the pre-promotion window and during a drain-less death; a Kind e2e on both lines:
  `TestE2E_SidecarFailoverDrainMaster` ([`sidecar_test.go:223`](../../test/e2e/sidecar_test.go)) with
  the master deleted through client-go with `GracePeriodSeconds: 0`, the replacement named by UID, its
  logs captured (needs a pod-log helper), no "Multiple masters" logged.
- **(Q3) under L4 (XS):** the sidecar querier sends `get-master-addr-by-name` and treats a null or
  empty answer as "did not answer". Unit tests for the switch and the null answer; Kind: the promoted
  pod labelled master within one poll of `+promoted-slave`.

### Restarted or replaced master

- **(Q4-Q9)** The mitigation as answered, with unit tests (builder, `fakeValkeyServer`) and an e2e
  asserting the replicas keep their `DBSIZE` across a master container restart; for Q9 an e2e deleting
  the recorded master with `--grace-period=0`.
- On close: the operator consequence into [docs/operations/persistence.md](../operations/persistence.md),
  the guard and the recovery into `docs/developer/`.

### Exec probes

- **(Q10) under D:**
  1. In the labeler poll ([`labeler.go:120-160`](../../internal/sidecar/labeler.go)) detect loading
     (`loading:1` or `-LOADING`) and `-BUSY`, close the gate ([`health.go`](../../internal/sidecar/health.go))
     on either, reopen on the next normal answer; unchanged on an auth error or a lost connection;
     closed before the first answer, as today.
  2. Amend ADR 0007 D9 (sticky only across auth errors and lost connections), the sidecar row of
     [`architecture.md:86`](../developer/architecture.md) and the operations pages on readiness.
     `ProbeCommand` and the pod spec stay unchanged.
  3. Unit: 503 before any answer; a normal answer opens; `-LOADING`/`loading:1` closes; `-BUSY`
     closes; the next normal answer reopens; an auth error and a refused connection leave it open.
     Revert check: the latch-only poll fails the loading and busy rows. Mutations: auth error as not
     ready, lost connection as not ready, dropped loading check, each fails its row.
  4. Unit: `ComputePodSpecHash` for a fixed `Valkey` is unchanged.
  5. E2E (both lines): a replica running an endless `EVAL` past `busy-reply-threshold` leaves the
     ready endpoints of `-r` (`readyEndpointPodNames`) within 15 s, returns after `SCRIPT KILL`, its
     restart count unchanged. The load case stays at the unit tier.
- **Closing:** the decisions into ADRs (0002 D11, 0008 D3, 0011 D21, 0012 D12, 0007 D9, the boot-rule
  ADR), the operator-visible consequences into `docs/operations/status.md`,
  `docs/operations/persistence.md` and the README.

## Open questions

### Q1: Does the 10 s recheck (A1) stay, now that the Pod watch (A4) ships in the same change? (master records)

The recorded decision is A1 (a recheck from both proving branches) plus A2, taken before A4 was
decided. A4 delivers the settling relabel as an event, sooner than 10 s; A1 then only matters for a
label that never settles, where it re-probes `INFO` every 10 s per CR without end, the polling ADR
0011 D12 refuses, and `RWServiceMisrouted` already reports that case.

- **Keep A1 + A2.** A second trigger for a missed watch event; XS plus an ADR 0011 D12 amendment; an
  unbounded poll when a label never settles.
- **Drop A1, ship A2 + A4 (recommended).** A relist re-delivers a missed update, so A1 guards nothing
  A4 misses; less code, and ADR 0011 D12 stays as written.

The recorded decision stands until re-decided.

**Answer:** _open_

### Q2: How does an empty replacement data pod on a Sentinel cluster avoid booting as master? (master records)

The recorded decision (reproduce, then C1 + C2, C3 as fallback) assumed one lagging Sentinel;
before `+promoted-slave` every Sentinel names the dead master, so C1 + C2 close nothing measured.

- **C4 (recommended).** A pod with an empty `/data` that Sentinel names as master does not take the
  master config while `SENTINEL MASTER` reports `num-slaves > 0`; it re-asks until another pod is
  named and boots as its replica; past a bound (above `down-after` plus a promotion, at most
  `failover-timeout` 60 s) it behaves as today. Cost M; the replacement boots 1-2 s (forced) or 6-8 s
  (drain-less) later; a whole data tier replaced under surviving Sentinels waits for a failover or the
  bound.
- **C2'.** Decide nothing while any Sentinel reports `failover_in_progress`. Cost M; covers only the
  forced shape (only the leader carries the flag), and a slow failover outlasts Phase 1's 31 s unless
  the bound is raised.

C4 alone covers the forced and the drain-less shape and is the same rule as the restart guard (Q4),
so one ADR serves both.

**Answer:** _open_

### Q3: Which Sentinel answer does the labeler's cross-check read? (master records)

The labeler reads `SENTINEL MASTER` `ip` from `sentinel-0` first, which trails every forced failover
by the leader's `RECONF_REPLICAS` phase (1.1 s to 5.4 s measured, up to 60 s).

- **L4 (recommended).** Read `get-master-addr-by-name`, which the leader switches at
  `+promoted-slave`; the local-master precondition stays. A null answer must count as "did not
  answer", or a real master is labelled `replica`. Cost XS, rides the sidecar-image roll.
- **Nothing.** The lag stays on every Sentinel handover, every release roll included.

L4 removes that window for one command, with no rule change and no extra roll.

**Answer:** _open_

### Q4: On a Sentinel tier, what stops a restarted master from serving its replicas an older dataset? (restarted master)

The fix is a wrapper in the Valkey container command using a marker in the config `emptyDir`
(marker present, config without `replicaof` = restart of a master). It rolls the data tier once
through the pod-spec hash, lossless for more than one data pod; the Sentinel tier does not roll.

- **Hold guard (recommended):** while Sentinel names this pod, do not start `valkey-server`; the pod
  is not Ready, Sentinel fails over after 5 s, then append `replicaof <named>` and `exec`. Cost S-M;
  outage about 6 s; needs Q8.
- **Boot-as-replica guard:** append `replicaof <a peer>` and start at once; peers refuse with
  `NOMASTERLINK`, Sentinel's role-mismatch rule fails over after about 25-35 s. Cost S; the pod is
  Ready as `replica` and serves empty or stale reads through `-r` meanwhile.

Measured in docker the hold guard kept 500 of 500 keys with a 6 s outage, against 0 without it, and
avoids the role-mismatch wait and stale reads. It shares the shell function of Q2's C4 and lands with
or after it.

**Answer:** _open_

### Q5: On a non-Sentinel tier, what stops a restarted master from wiping or rolling back its replicas? (restarted master)

Nothing arbitrates today; the post-roll variant ends in the `NOMASTERLINK` deadlock and the recovery
promotes pod-0, the restarted pod. The recovery keeps its `unreachable > 0` refusal
([`valkey_controller.go:2905`](../../internal/controller/valkey_controller.go)) and its suppression
during a roll ([`:2886-2888`](../../internal/controller/valkey_controller.go)).

- **(a) data-aware recovery plus (b) boot-as-replica guard (recommended):** (a)
  `checkAndRecoverNoMaster` promotes the available pod with the newest data (Q6), recording it first
  and refusing on an unreadable answer; (b) a restarted master boots as a replica of a peer, reaching
  the deadlock (a) resolves. Cost M + S; (a) rolls nothing, (b) rolls every multi-replica non-Sentinel
  data tier once. Writes fail until the recovery runs (by reading 5-20 s).
- **(a) alone:** cost M, rolls nothing; covers the post-roll state, but a cluster that never rolled
  still loses everything (booted-as-master variant).

In the measured deadlock pod-0 had 500 keys at offset 0 and its peers 1000 at offset 16581, so a
data-aware choice loses nothing; (b) also covers fresh clusters. Land (a) first.

**Answer:** _open_

### Q6: Which pod does the data-aware recovery promote? (restarted master; only if Q5 takes (a))

`ReplicationInfo` has no offset or replid ([`client.go:26-33`](../../internal/valkeyclient/client.go));
`dbSizeReader` exists ([`rolling_update.go:1678`](../../internal/controller/rolling_update.go)).

- **Highest `master_repl_offset`, ADR 0028 D1 zero-key veto, D3 fail-closed, tie to lowest ordinal
  (recommended):** one more parsed field, one `DBSIZE` per candidate.
- **Highest `DBSIZE`, fail-closed:** smallest change; a workload that deletes keys ranks a stale
  snapshot above the current dataset.

The offset measures replication progress, which is what the loss is measured in; the veto keeps what
ADR 0028 decided.

**Answer:** _open_

### Q7: Which data tiers render the restart guards? (restarted master)

Every tier with `spec.replicas > 1` has the config `emptyDir` (`needsInitContainer`,
[`statefulset.go:645-647`](../../internal/builder/statefulset.go)). A single-pod tier renders no
guard: it has no peer, and the roll would discard its only non-persistent pod.

- **Every multi-replica tier (recommended):** no persistence branch; rolls every multi-replica data
  tier once; also turns the `-LOADING` reload outage of persistent tiers into a 6 s failover (Sentinel
  counts `-LOADING` as available). A graceful master restart on a persistent tier becomes a failover.
- **Non-persistent and persistent `rdb` tiers:** covers every measured loss; saves one lossless roll
  of `aof`/`both` tiers at the cost of a mode branch and a test dimension.
- **Non-persistent only:** smallest roll; leaves the measured `rdb` rollback.

Simplest condition, and it removes the reload write outage; if the Kind run shows the `-LOADING`
window negligible, take the `rdb`-scoped option instead.

**Answer:** _open_

### Q8: What does the hold guard do when no failover comes? (restarted master; only if Q4 takes the hold)

Without a quorum or a selectable replica Sentinel keeps naming the held pod; liveness kills it 55-65 s
after start and the guard runs again with back-off. The bound must stay below 55 s.

- **Ask the peers, hold, then fall back to a replica (recommended):** every peer `DBSIZE` 0 -> start
  at once; otherwise hold, and after the bound append `replicaof <a peer>`. Cost: a peer query with its
  own flags (TLS port 16379, `--tls --cacert`, password whenever auth is on;
  [`statefulset.go:425-433`](../../internal/builder/statefulset.go) has them for the non-Sentinel init
  only). Never crash-loops; an unreachable peer counts as "may hold data".
- **Hold, then fall back, no peer query:** an all-empty tier waits for nothing; a tier whose Sentinels
  know no replica sits masterless.
- **Hold while a peer may hold data, then exit non-zero:** no stale reads, but `CrashLoopBackOff` and
  up to 300 s to rejoin.

The first is the only option correct in all three boundary cases (all-empty, no quorum with data,
Sentinels knowing no replica).

**Answer:** _open_

### Q9: What happens to a replaced (not restarted) recorded master on a non-persistent non-Sentinel tier? (restarted master)

A new pod has a new `emptyDir` and no marker, so no restart guard fires; ADR 0008 records it as open.
A drain prevents it for an ordinary delete; a hard node failure runs none. The Sentinel counterpart
is Q2.

- **Peer-data check in the init's master branches (recommended):** Phase 2 self-claim and Phase 3
  ordinal-0 fallback boot as a replica when any peer answers `DBSIZE > 0`; Q5 (a) then promotes the
  pod with the data. A few shell lines in the existing peer loop
  ([`statefulset.go:425-433`](../../internal/builder/statefulset.go)); lands in the same roll as Q5 (b).
- **Accept the residual:** record it in ADR 0008 and docs/operations/persistence.md; cost XS; a
  documented data-loss path on node failure stays open.

The peer check answers ADR 0008's "new or cluster empty" question by asking the peers, which the init
already does in Phase 1.

**Answer:** _open_

### Q10: What takes a loading or busy data pod out of Ready? (exec probes)

Neither readiness gate sees a load, and neither sees `-BUSY` once the sidecar has latched. Liveness,
the Sentinel tier and ADR 0007 D9's rule that readiness is not replication health stay unchanged under
every option. Independent of Q4-Q9, which do not touch readiness.

- **A - keep both gates, document them.** XS (the shared corrections only). A loading or busy pod
  stays routed to and counted healthy, now as a documented property.
- **C - a readiness command of its own.** `sh -c 'out=$(valkey-cli --no-auth-warning -a
  "$VALKEY_PASSWORD" ping 2>/dev/null) || exit 1; case "$out" in PONG*|NOAUTH*|WRONGPASS*) exit 0;;
  esac; exit 1'` (today's TLS arguments, no `-a` without auth); liveness stays. M: measured end to
  end, no restart window, password-independent; but a pod-spec change needing an ADR 0007 D7 treatment
  for the single pod (re-deciding ADR 0007 D6). A loading pod whose probe holds a rejected password
  still counts as ready.
- **D - the sidecar gate closes on `-LOADING` and `-BUSY` (recommended).** M: ships in the sidecar
  image, no pod-spec change, no roll of its own, no D7 treatment; auth behaviour unchanged. Costs: a
  stalled poll freezes the gate, and at a `valkey` container restart a few seconds may pass before the
  gate closes; designed, not measured end to end.

D fixes the defect where the server is already read every second and cannot become the ADR 0007 D7
data-loss change on an operator upgrade. If a D7 treatment lands first for another reason, C becomes
the better option (S, fully measured).

**Answer:** _open_

## Not verified

- Whether the empty `-1` of item C was labelled `master` or received writes (logs lost); the Q2 e2e settles it.
- The master-record behaviour on persistent clusters.
- The restart chain on Kubernetes end to end and the kubelet's first-restart latency (Kind session).
- The time from a post-roll pod-0 restart to `checkAndRecoverNoMaster`; by reading 5-20 s.
- The hold guard with three Sentinels and quorum 2 (docker ran one Sentinel, quorum 1).
- The `-LOADING` duration of a large persistent reload and the AOF loss window (decide Q7 and the D window of Q10).
- A roll whose topology restoration was abandoned (`abandonTopologyRestoration`, [`rolling_update.go:4511`](../../internal/controller/rolling_update.go)) may lose more than the writes since the roll; read, not measured.
- The persistence settings of the wds18 and production clusters (`kubectl get valkey -A` over `spec.persistence.enabled`).
- That a loading or busy pod stays in the `-r` EndpointSlice and that the PDB admits an eviction meanwhile (Kind run with a large dataset).
- Under Q10 D, how long a pod stays Ready after the sidecar sees `loading:1` (the D e2e or a timed Kind run).
- That a Sentinel never answers `PING` with `-LOADING` or `-BUSY` (inferred: no dataset, no scripts; settled by reading `sentinel.c`).
- The single-pod replacement on a probe-only pod-spec delta (traced by reading; matters only for Q10 C; a unit test of `singlePodDeferral` settles it).

## Related

- [T12](archive/012-the-master-handover-loses-no-acknowledged-write-and-no-dataset.md) (done) - the roll's own failover is coordinated on Valkey 9 Sentinels and loses no acknowledged write there (ADR 0037 D1); L4 only shortens routing.
- [T18](018-cr-status-reporting-and-the-status-write.md) - overlaps B2 textually in `updateHAStatus`; either may land first.
- [T34](034-test-fixtures-pass-on-evidence-that-does-not-prove-the-assertion.md) - A4's integration test must not read the cache right after its patch.
- [T34](034-test-fixtures-pass-on-evidence-that-does-not-prove-the-assertion.md) - the Q2 and Q9 e2e name the replacement by UID.
- T52 - `maxmemory` is never set, which makes an OOM kill a reachable restart trigger.
- T23 - a Sentinel-side route into the restart mechanism after a reset.
- T23 - its D1 option D relies on the no-master recovery Q5 (a) makes data-aware; states the readiness probe is a `PING` an unsynced replica passes, true under every Q10 option.
- T50 - password rotation; relies on the probes passing on `NOAUTH` and owns the `authentication.md` rewrite.
- T12 - the same `valkey-cli` exit-0-on-error behaviour for `--raw SET`; `-e` is the switch.
