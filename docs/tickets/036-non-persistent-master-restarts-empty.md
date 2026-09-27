---
id: T36
title: a non-persistent master that crash-restarts comes back empty and flushes its replicas
state: analysed       # was filed; at 84a39c2 every variant is measured on the Valkey side (docker, both pins) and every decision is costed with a marked option; the Kind reproduction verifies the Kubernetes half before any implementation (History 2026-09-27)
severity: high        # was an estimate; the Valkey side is measured on both pins for all three variants and for persistent mode rdb, and upstream documents the Sentinel case; the Kubernetes chain is not reproduced
security: none
urgency: next         # severity >= medium and the trigger is reachable in released code (rule 3); rule 1 checked and not matched (History 2026-09-27)
effort: L             # was M; the Sentinel guard S-M, the non-Sentinel recovery and guard M+S, the replaced-pod init check, the e2e on both Valkey lines and the Kind reproduction (History 2026-09-27)
blocked-by: decision  # the mitigation; the Sentinel half lands with or after T35 change 2 (Options, decision 1)
filed-from: T35       # decision 7 of the T35 refinement, 2026-09-27
opened: 2026-09-27
decided:
done:
---

Filed as its own ticket on 2026-09-27 (T35 decision 7). Hans's rule of the same day: every
finding is a file, new or appended to the ticket of its family; a board row was never
sufficient. This one is new because it differs from T35 in mechanism (a container restart,
not a stale record) and in severity.

Each claim carries a label: **read** means read in the tree at `f5c6886`; **inference** means
derived from documented Kubernetes or Valkey behaviour and not measured here. Nothing in this
ticket was measured on a cluster yet. *(2026-09-27: **measured (docker)** marks what was run in
plain docker against both pinned images, `valkey/valkey:9.1.1` and `8.1.9` — the Valkey side of
the chain, not Kubernetes; the scripts are in the session scratchpad, untracked. Line
references re-read at `4a7543e`.)* *(2026-09-27 at `84a39c2`: every line reference re-read at
`84a39c2`; the scratchpad is not durable, so the command and the result of every measurement
are recorded in this file, in "Measurements (docker)" below. **documented** marks behaviour
read in upstream documentation or source at a named URL, tag and line. The title names the
first variant found; the ticket now covers the whole family — an empty master, a master with a
stale snapshot, the post-roll deadlock, persistent mode `rdb`, and a replaced master pod.)*

## Fact

**The chain (read, with two inferences).**

1. Init containers run once per pod, never on a container restart (Kubernetes semantics,
   **inference** in the sense that it is not this repository's code). The data init writes the
   configuration it chose into the pod's writable config volume
   (~~[`statefulset.go:332`](../../internal/builder/statefulset.go)~~ *(corrected 2026-09-27:
   the `cp` is at [`statefulset.go:333`, `:336`, `:346-348`](../../internal/builder/statefulset.go)
   on Sentinel clusters and [`:524-536`](../../internal/builder/statefulset.go) without; the
   volume is the `emptyDir` at [`:233-238`](../../internal/builder/statefulset.go) and
   [`:411-416`](../../internal/builder/statefulset.go))*, `cp` of the master or the
   replica config), an `emptyDir` that outlives a container restart. *(2026-09-27 at
   `84a39c2`: **documented**, no longer an inference. Init containers re-run only when the pod
   sandbox restarts, or when all containers have terminated and their completion record was
   garbage-collected ([init containers](https://kubernetes.io/docs/concepts/workloads/pods/init-containers/),
   website `content/en/docs/concepts/workloads/pods/init-containers.md:350-364`); a single
   container restart never re-runs them. "The data in an emptyDir volume is safe across
   container crashes" ([volumes](https://kubernetes.io/docs/concepts/storage/volumes/#emptydir),
   `volumes.md:170-171`). The sidecar keeps running while only the Valkey container restarts
   ([`run.go:111-122`](../../internal/sidecar/run.go)).)*
2. A `valkey-server` container the kubelet restarts therefore starts with the configuration of
   its first start — the **master** config if it was master — and, on a non-persistent
   cluster, with **no dataset** (`save ""`, [ADR 0012 D10](../adr/0012-the-sidecar-records-its-drain-promotion-on-the-pod.md)).
   Same pod, same address, same hostname. *(corrected 2026-09-27, narrower: it starts with the
   config its **init** wrote, not with its current role, and "no dataset" holds only for a pod
   that never full-synced as a replica — a replica's full sync leaves `dump.rdb` in `/data`
   (`rdb-del-sync-files` is `no`), and a restarted `valkey-server` loads it despite `save ""`,
   **measured (docker)**. Three variants, in the table below.)* *(2026-09-27 at `84a39c2`:
   re-measured, exp2 and exp3 below; `rdb-del-sync-files` defaults to `no` and
   `repl-diskless-load` to `disabled` (valkey `src/config.c` 9.1.1:3279, :3368), and
   [`configmap.go`](../../internal/builder/configmap.go) sets neither. Setting
   `rdb-del-sync-files yes` would turn every stale-snapshot variant into the empty one, which is
   worse, so it is not an option.)*
3. **With Sentinel:** `down-after-milliseconds` is 5000 ([`sentinel.go:47`](../../internal/builder/sentinel.go)).
   The first restart of a crashed container is immediate and an empty Valkey boots in well under
   a second (**inference**), so Sentinel never marks the master `s_down`, performs no failover,
   and keeps the same address as master. *(2026-09-27: the restarted container answered
   `DBSIZE` within about 1 s of `docker start`, **measured (docker)**; the kubelet's restart
   latency is not measured, and if it reaches 5 s Sentinel fails over first and this step does
   not happen.)* *(2026-09-27 at `84a39c2`: the immediate first restart is **documented**
   ("Initial crash: Kubernetes attempts an immediate restart",
   [pod lifecycle](https://kubernetes.io/docs/concepts/workloads/pods/pod-lifecycle/),
   `pod-lifecycle.md:202-210`), and so is its limit: a container that crashes again within
   10 minutes of running is restarted after a back-off of 10 s, 20 s, … up to 300 s
   (`pod-lifecycle.md:386-389`), which exceeds `down-after`, so on a Sentinel cluster the chain
   applies to the first crash after 10 minutes of healthy running. That holds for a default
   kubelet only: the same page documents the alpha gate `ReduceDefaultCrashLoopBackOffDecay`
   (initial delay 1 s) and `KubeletCrashLoopBackOffMax` with a `maxContainerRestartPeriod`
   below 10 s (`pod-lifecycle.md:549-575`), under which a repeated crash also restarts inside
   `down-after`. With a 1000 ms `master-reboot-down-after-period` and without it, Sentinel
   logged only `+reboot`, never `+sdown`, for a restart of this kind, exp1 and exp4.)*
4. **Without Sentinel:** nothing arbitrates. `checkSteadyStateSplitBrain` sees one labelled
   master and `checkAndRecoverNoMaster` sees a master answering; neither reports anything.
   *(2026-09-27 at `84a39c2`: holds for a master that booted with the master config —
   [`steady_state_master.go:151-181`](../../internal/controller/steady_state_master.go) adopts
   at most an unrecorded promotion, and [`valkey_controller.go:2905`](../../internal/controller/valkey_controller.go)
   returns on `hasMaster`. The `REPLICAOF NO ONE` variant does reach the no-master recovery,
   with its own loss, third row of the table below.)*
5. The replicas lose their link, reconnect, and offer their old replication id; the restarted
   process has a new one and answers `FULLRESYNC` from an empty dataset. The replicas flush
   and load nothing (**inference**: upstream documents this under "Safety of replication when
   master has persistence turned off" and recommends that such a master not restart
   automatically — which a StatefulSet pod cannot arrange). *(2026-09-27, **measured
   (docker)**, both pins: a replica holding 500 keys kept them for about 5 s after the empty
   master came back — the `repl-diskless-sync-delay 5` of
   [`configmap.go:179`](../../internal/builder/configmap.go) — and held 0 at about 6 s, with a
   new `master_replid` and "Flushing old data" in its log. No guard in Valkey 8.1.9 or 9.1.1.)*
   *(2026-09-27 at `84a39c2`: re-measured, exp1 below. The upstream page is valkey-doc
   [`topics/replication.md:43-61`](https://github.com/valkey-io/valkey-doc/blob/main/topics/replication.md),
   and it names the Sentinel case explicitly at `:58-59`: "the primary can restart fast enough
   for Sentinel to not detect a failure".)*

**The three master variants** *(added 2026-09-27)*. What a restarted master boots as depends on
how it got the role:

| Variant | How it got the role | Config on restart | `/data` on restart | Outcome |
|---|---|---|---|---|
| booted with the master config | ordinal-0 fallback on a fresh cluster ([`statefulset.go:342-349`](../../internal/builder/statefulset.go), [`:531-537`](../../internal/builder/statefulset.go)); Sentinel named the booting pod ([`:331-333`](../../internal/builder/statefulset.go), T35 part C); the known-master self-claim ([`:506-507`](../../internal/builder/statefulset.go), [`:528-530`](../../internal/builder/statefulset.go)) | master | empty | the chain as written; step 5 **measured (docker)**: the whole dataset is lost on every pod (exp1) |
| promoted by Sentinel | any Sentinel failover, the roll's `SENTINEL FAILOVER` included | master: Sentinel sends `CONFIG REWRITE`, which drops the `replicaof` line from the promoted pod's config file, **measured (docker)**, both pins | the `dump.rdb` of its last full sync | a master with a stale snapshot; the replicas full-resync to it and lose the writes since that sync (**inference**) *(2026-09-27 at `84a39c2`: **measured (docker)**, both pins, exp3 — the replica logged "Trying a partial resynchronization", then "Full resync from primary", and went from 1000 to 500 keys; a full resync, not a partial one. Sentinel's `CONFIG REWRITE` with `SLAVEOF` is valkey `src/sentinel.c` 9.1.1:4912.)* |
| promoted with `REPLICAOF NO ONE` by the operator or the drain ([`rolling_update.go:4202`](../../internal/controller/rolling_update.go), [`:4610`](../../internal/controller/rolling_update.go), [`valkey_controller.go:2949`](../../internal/controller/valkey_controller.go), [`drain.go:172`](../../internal/sidecar/drain.go); no `CONFIG REWRITE` anywhere in `internal/`) | every non-Sentinel roll (pod-0 via `promotePod0AndRedirect`), a drain, the no-master recovery | replica of the address its init wrote, often its own current replica | stale `dump.rdb` | ~~a different failure: two pods replicating from each other and no master; unmeasured, may keep the data (**inference**). *(Review 2026-09-27, read: if no pod then answers `role:master` and all answer, `checkAndRecoverNoMaster` promotes pod-0 and redirects the others to it ([`valkey_controller.go:2904-2963`](../../internal/controller/valkey_controller.go)); after a non-Sentinel roll pod-0 is the restarted pod itself, so whether the data survives turns on whether pod-0 took a full sync from its peer before that promotion — unmeasured.)*~~ *(corrected 2026-09-27 at `84a39c2`: **measured (docker)**, both pins, exp2/exp2b, re-measured independently on 9.1.1: it does **not** keep the data. The restarted pod boots as a replica of its own replica; both sides refuse `PSYNC` with "-NOMASTERLINK Can't SYNC while not connected with my master" (valkey `src/replication.c` 9.1.1:1119, 8.1.9:1084), so no pod is master and the `-rw` Service has no endpoint. `checkAndRecoverNoMaster` ([`valkey_controller.go:2881-2968`](../../internal/controller/valkey_controller.go)) then finds every pod reachable and none master, records and promotes the hardcoded `<sts>-0` ([`:2925`](../../internal/controller/valkey_controller.go), [`:2942`](../../internal/controller/valkey_controller.go), [`:2949`](../../internal/controller/valkey_controller.go)) and redirects the rest ([`:2954-2965`](../../internal/controller/valkey_controller.go)) with no dataset comparison. After a non-Sentinel roll the restarted master **is** pod-0, holding the snapshot of its full sync during the roll: the emulated recovery left all three pods at 500 of 1000 keys — every write since the roll is lost. When the restarted master is a drain-promoted non-zero ordinal, pod-0 is a synced replica and the recovery keeps the data (read, from the same measured mechanics). A roll whose topology restoration was abandoned ([`abandonTopologyRestoration`, `rolling_update.go:4511`](../../internal/controller/rolling_update.go), reached through [`:4484-4489`](../../internal/controller/rolling_update.go)) leaves the roll's promoted pod as master, promoted at [`:4202`](../../internal/controller/rolling_update.go) with `replicaof <old master>` still in its config; its restart reaches the same deadlock, and the recovery then promotes pod-0, the pod whose failure to sync caused the abandonment, so the loss can exceed "the writes since the roll" — read, not measured.)* |

**Persistent clusters in mode `rdb`** *(added 2026-09-27 at `84a39c2`, **measured (docker)**)*.
A persistent master reloads its own files on a restart. In mode `rdb` — the CRD default mode
([`api/v1/valkey_types.go:999`](../../api/v1/valkey_types.go)), with the save points
`900 1`, `300 10`, `60 10000` ([`configmap.go:206-208`](../../internal/builder/configmap.go)) —
that is the last save, so after a process death without a shutdown save the restarted master
starts behind its replicas, their partial resync is refused, and they roll back to it: exp6,
both pins, 1000 to 500 keys, up to 15 minutes of writes at a low write rate. A **graceful** stop
does not lose anything: on `SIGTERM` the master logs "Saving the final RDB snapshot before
exiting", reloads 1000 keys, and the replica logs "Successful partial resynchronization with
primary" (exp6term, both pins). So in mode `rdb` the rollback follows an OOM kill, a crash, or
a hang that ignores `SIGTERM` until the grace period ends in `SIGKILL`; a liveness kill of a
process whose event loop still runs saves and loses nothing. In mode `aof` or `both`
(`appendfsync everysec`, [`configmap.go:228-231`](../../internal/builder/configmap.go)) the
reload is behind by about the fsync interval — not measured. A `Persistence` block that
skipped CRD defaulting, with an empty mode, renders `save ""` and `appendonly no`
(the two `else` branches of `persistenceConfig`, [`configmap.go:216-221`](../../internal/builder/configmap.go)
and [`:237-242`](../../internal/builder/configmap.go)); through the API server the
mode is always defaulted to `rdb`.

**Three operator-side facts that make the chain reachable (read).**

- **The liveness probe restarts a stalled master.** The Valkey container carries an exec
  liveness probe (`ProbeCommand(v)`, period 10 s, timeout 5 s, failure threshold 5,
  [`statefulset.go:859-870`](../../internal/builder/statefulset.go)): a master that does not
  answer `PING` for about 50 s is killed and restarted on the operator's instruction *(precised
  2026-09-27, sweep: "does not answer" means no reply at all - a master answering an error reply,
  `-LOADING`, `-BUSY` or `NOAUTH`, passes the probe, measured in docker on both pins in
  [T76](076-the-exec-probes-pass-on-any-server-reply.md))*. On a
  liveness kill the kubelet runs the container's `preStop` hook (**inference**), which waits
  for the drain marker ([`statefulset.go:750-758`](../../internal/builder/statefulset.go)) that
  only a terminating sidecar writes — the sidecar is not terminating, so the hook waits its
  60 s bound first. Not measured. *(corrected 2026-09-27: the hook exists on multi-replica
  non-Sentinel pods only — `drainPreStop` returns nil otherwise,
  [`statefulset.go:746-749`](../../internal/builder/statefulset.go). On a Sentinel cluster a
  stalled master is failed over after the 5 s `down-after`, long before the liveness kill
  ~~(15 s initial delay plus 5 × 10 s)~~, and the restarted pod meets Sentinels that have moved
  on and convert it: the liveness trigger is probably harmless there (**inference**). It is
  live on non-Sentinel clusters.)* *(corrected 2026-09-27 at `84a39c2`: the budget is not
  15 s plus 5 × 10 s, because the first probe is one of the five failures. The kubelet runs the
  probe on a per-worker ticker not aligned to the container start and skips ticks until
  `InitialDelaySeconds` has passed (kubernetes v1.36.1 `pkg/kubelet/prober/worker.go:160-169`,
  `:330-331`), so after a container start the first probe falls 15-25 s in and the fifth
  consecutive failure 55-65 s in; a start guard's bound must stay below 55 s. For a stall in a
  running container the initial delay does not apply and each failed probe can take its 5 s
  timeout: the kill comes roughly 40-60 s after the stall begins, which the "about 50 s"
  above approximates. The `preStop` on a liveness kill is **documented**: the hook "is called
  immediately before a container is terminated due to an API request or management event such
  as a liveness/startup probe failure"
  ([container lifecycle hooks](https://kubernetes.io/docs/concepts/containers/container-lifecycle-hooks/)).
  The marker is written only by `signalDrainComplete`
  ([`drain.go:332-349`](../../internal/sidecar/drain.go)), inside the drain handler that runs
  after `SIGTERM` cancels the sidecar's context ([`run.go:111-122`](../../internal/sidecar/run.go));
  the bound is `drainPreStopTimeoutSeconds = 60` ([`statefulset.go:59`](../../internal/builder/statefulset.go)).)*
- **`maxmemory` is never set; the memory limit is the backstop.** The generated config carries
  `maxmemory-policy noeviction` and no `maxmemory` ([`configmap.go:129`](../../internal/builder/configmap.go)),
  and the CRD has no field for it. A growing dataset ends at `spec.resources.limits.memory`,
  the kernel kills the container (`OOMKilled`), the kubelet restarts it — empty.
  *(2026-09-27 at `84a39c2`: re-read, holds — [`configmap.go:126-135`](../../internal/builder/configmap.go),
  no `maxmemory` anywhere in `internal/`, `api/` or `cmd/`. For this ticket the bullet says only
  that an OOM kill is a reachable trigger of the restart. Whether and how the operator sets
  `maxmemory` is its own decision, filed as
  [T71](071-maxmemory-is-never-set-so-an-oom-kill-is-the-only-memory-bound.md), which carries
  the analysis and its docker measurements.)*
- **Non-persistent is the default.** `spec.persistence.enabled` defaults to `false`
  ([`api/v1/valkey_types.go:993-996`](../../api/v1/valkey_types.go), `PersistenceSpec`;
  `IsPersistenceEnabled` returns `false` for a nil block,
  [`:1337-1339`](../../api/v1/valkey_types.go)). On wds18 all eight example clusters are
  non-persistent (T35, Context). *(2026-09-27 at `84a39c2`: not verified in this pass — relayed
  from T35, observed 2026-09-26; `kubectl get valkey -A -o jsonpath` over
  `spec.persistence.enabled` would check it. The persistence and topology of the production
  namespaces are not verified either.)*

**What else the restart touches** *(added 2026-09-27 at `84a39c2`, read unless marked)*.

- **The data pod's probes detect only "nothing listens".** Readiness and liveness are both
  `valkey-cli … ping` ([`statefulset.go:847-870`](../../internal/builder/statefulset.go),
  `ProbeCommand` at [`:1515-1545`](../../internal/builder/statefulset.go)). `valkey-cli ping`
  exits 0 on any server reply, error replies included, and 1 only when it cannot connect —
  **measured (docker)**, both pins (Measurements, probe exit codes). So a pod that holds before
  `valkey-server` starts is not Ready; a pod booted as a replica whose link is down is Ready;
  a persistent master answering `-LOADING` is Ready (~~inferred for `-LOADING`, not measured~~
  *(corrected 2026-09-27, sweep: measured in docker on both pins by [T76](076-the-exec-probes-pass-on-any-server-reply.md), after a restart
  and during a full-sync load)*).
- **A pod whose local Valkey is unreachable keeps its last `instanceRole` label**: the labeler
  returns before patching when `DetectRole` fails ([`labeler.go:119-127`](../../internal/sidecar/labeler.go)).
  It leaves the `-rw` endpoints only because it is not Ready — the `-rw` and `-r` Services do
  not publish not-ready addresses, only the two headless Services do
  ([`service.go:154`](../../internal/builder/service.go), [`:237`](../../internal/builder/service.go)).
  The `-r` Service selects `instanceRole=replica` ([`service.go:204-206`](../../internal/builder/service.go)),
  and `replica-serve-stale-data yes` ([`configmap.go:176`](../../internal/builder/configmap.go))
  lets a replica with a broken link serve its dataset to readers.
- **The CR records the no-master window of the `REPLICAOF NO ONE` variant only**: the recovery
  sets phase `Error` with "No master detected, recovering by promoting pod-0"
  ([`valkey_controller.go:2913-2916`](../../internal/controller/valkey_controller.go)) and the
  `Error` phase requeues after 10 s ([`:377-380`](../../internal/controller/valkey_controller.go)).
  Nothing records the flush itself in any variant.
- **The observer cannot see a flush.** It writes one key with a 10 s TTL to the master and
  reads it back on every pod ([`internal/observer/checks.go:110-159`](../../internal/observer/checks.go))
  and checks connected replicas and sync state ([`:92-107`](../../internal/observer/checks.go));
  it counts no keys, so a flush shows at most as a transient sync failure.
- **Sentinel's `master-reboot-down-after-period` cannot catch an empty restart.** The run-id
  change seen in `INFO` sets `SRI_PRIMARY_REBOOT` (valkey `src/sentinel.c` 9.1.1:2462-2470);
  the first `PONG` clears it (`:2734`), while `-LOADING` and `-MASTERDOWN` count as available
  without clearing it (`:2729-2731`); `s_down` on reboot needs the flag to outlive the period
  (`:4485-4486`); `INFO` runs every 10 s (`:86`). Upstream `sentinel.conf` 9.1.1:353-361
  defines the directive as the time Sentinel "is willing to accept a -LOADING response after a
  primary has been rebooted". It therefore fails over only a master still loading a large
  dataset when an `INFO` arrives — a subset of the stale-snapshot variant, never the empty one.
  **Measured (docker)**, exp4.
- **One config hash covers both tiers of a Sentinel cluster.** `ComputeConfigHash` folds the
  Valkey configs and `GenerateSentinelConfForHash` into one FNV hash
  ([`configmap.go:293-305`](../../internal/builder/configmap.go)), stamped on the data pod
  template ([`statefulset.go:153`](../../internal/builder/statefulset.go)) and the Sentinel pod
  template ([`sentinel.go:234`](../../internal/builder/sentinel.go)); the Sentinel roll compares
  it ([`rolling_update.go:4864-4865`](../../internal/controller/rolling_update.go)). Any config
  line on a Sentinel cluster rolls both tiers. A change to the Valkey container command changes
  only the data tier's pod-spec hash (`ComputePodSpecHash`, FNV over the whole built PodSpec,
  [`statefulset.go:1228-1239`](../../internal/builder/statefulset.go)).
- **ADR 0008 already records half of this as open.** Its residual risk "The mirror case of D8"
  ([ADR 0008](../adr/0008-known-master-annotation-is-the-recorded-authority.md), Residual risks)
  has two halves: a recorded master replaced by a new pod self-claims and boots empty ("the pod
  cannot tell 'I am empty because I am new' from 'I am empty because the cluster is'"), and
  `checkAndRecoverNoMaster` "promotes pod-0 unconditionally when it finds no master at all …
  Open". [ADR 0028](../adr/0028-a-demotion-may-not-discard-the-only-dataset.md) D1-D4 guard
  only the rolling-update resolver's demotions, not this promotion.
- **The replaced-pod half is the same Valkey mechanism with a different trigger.** A
  non-persistent recorded master that is recreated rather than restarted (deleted without a
  drain, evicted, node lost) gets a new `emptyDir`: its init finds no master with connected
  replicas in Phase 1 ([`statefulset.go:452-488`](../../internal/builder/statefulset.go)),
  reads its own name as the known master in Phase 2 ([`:505-508`](../../internal/builder/statefulset.go))
  and boots empty as master ([`:528-530`](../../internal/builder/statefulset.go)); the replicas
  full-resync to it. Every marker-based guard in the Options below covers a container restart
  inside a living pod only — a new pod has a new `emptyDir` and no marker — so none of them is
  the fix for that half (decision 6 is).

**Verified (read):** the init's `cp` into the writable config volume; `save ""` for
non-persistent clusters as ADR 0012 states it; the Sentinel `down-after` constant; the two
operator checks that see nothing; the probe parameters and the `preStop` hook on the Valkey
container; the absence of `maxmemory` in the builder and the CRD; the persistence default.

**Not verified:** every step marked inference; the whole chain end to end; ~~whether Valkey 8
or 9 has any guard against an empty master full-resyncing replicas that hold data (none is
known)~~ *(answered 2026-09-27: none, measured (docker), step 5)*; ~~the `preStop` behaviour on
a liveness kill~~ *(answered 2026-09-27 at `84a39c2`: documented, it runs)*; the boot time of
an empty container in Kind; ~~whether the observer's write test would catch the flush~~
*(answered 2026-09-27 at `84a39c2`, by reading: it cannot, it counts no keys)*.

**Verified 2026-09-27.** *Read at `4a7543e`:* every line reference above; `checkSteadyStateSplitBrain`
([`steady_state_master.go:151-156`](../../internal/controller/steady_state_master.go)) and
`checkAndRecoverNoMaster` ([`valkey_controller.go:2881`](../../internal/controller/valkey_controller.go))
both run on multi-replica non-Sentinel clusters only
([`valkey_controller.go:421-422`](../../internal/controller/valkey_controller.go)); `valkey-server`
is the container's PID 1 in both command shapes (`exec`,
[`statefulset.go:822-833`](../../internal/builder/statefulset.go)); `/data` is an `emptyDir`
without persistence ([`:579-587`](../../internal/builder/statefulset.go)) and the working
directory ([`:842`](../../internal/builder/statefulset.go)); no `maxmemory`,
`enable-debug-command` or `master-reboot-down-after-period` anywhere in `internal/`, `api/` or
`cmd/`. *Measured (docker), both pins:* the flush of step 5; `dump.rdb` kept after a full
sync and loaded on restart; Sentinel's `CONFIG REWRITE` on promotion;
`sentinel master-reboot-down-after-period` accepted in a config file and by `SENTINEL SET`
(a made-up directive is a fatal config error); `kill -9 1` from inside the container leaves
`valkey-server` running; `DEBUG` refused (`enable-debug-command no`); `CLIENT PAUSE … ALL`
accepted.

**Not verified 2026-09-27:** the chain on Kubernetes end to end; the kubelet's first-restart
latency; ~~the promoted-after-boot outcome on a non-Sentinel cluster~~ *(answered 2026-09-27 at
`84a39c2`: the `NOMASTERLINK` deadlock and, after the pod-0 recovery, the loss of the writes
since the roll, measured (docker), exp2)*; ~~whether the replicas of a Sentinel-promoted master
full-resync or partially resync from its reloaded snapshot~~ *(answered 2026-09-27 at
`84a39c2`: full resync, measured (docker), exp3)*; ~~what `master-reboot-down-after-period`
does after a reboot and how fast~~ *(answered 2026-09-27 at `84a39c2`: it fails over only a
master still answering `-LOADING`; measured (docker), exp4, and read in `sentinel.c`)*.

**Verified 2026-09-27 at `84a39c2`.** *Read:* the defect is untouched since `4a7543e` —
`git diff 4a7543e 84a39c2 --stat` over the builder, the controller and the API shows only line
shifts in `valkey_types.go`, `rolling_update.go` (+3 before the promotion sites) and
`valkey_controller.go` (+1 before `checkAndRecoverNoMaster`); the data inits, the container
command, the Sentinel config and the no-master recovery are unchanged; only the docs sentence
landed, in `bcc63c9`. Every link in this file re-read at `84a39c2`. The gate is
[`valkey_controller.go:421`](../../internal/controller/valkey_controller.go) (`if
v.IsMultiReplicaWithoutSentinel()`) with the call on `:422`, as the ticket said at `4a7543e`.
The rest is recorded in place above and in the measurements below. *Documented:* the init
re-run rule, the `emptyDir` crash rule, the immediate first restart and its back-off, the
`preStop` on a liveness kill (links in Fact). *Measured (docker):* exp1-exp6, exp6term, the
probe exit codes, and the PID 1, `DEBUG` and `CLIENT PAUSE` checks, below.

**Not verified 2026-09-27 at `84a39c2`, and what would verify it:** the whole chain on
Kubernetes; the kubelet's first-restart latency on Kind; how long after a post-roll pod-0
restart `checkAndRecoverNoMaster` runs — by reading 5-20 s, because a container restart flips
the pod's readiness, which changes the owned StatefulSet's status, and `Owns(&appsv1.StatefulSet{})`
carries no predicate ([`valkey_controller.go:2987-2988`](../../internal/controller/valkey_controller.go)),
plus the 10 s requeue of a not-healthy phase; the hold guard with three Sentinels and quorum 2
(exp5 ran one Sentinel with quorum 1); the `-LOADING` duration ~~and probe outcome~~ during a large
persistent reload *(the probe outcome, exit 0, is measured in [T76](076-the-exec-probes-pass-on-any-server-reply.md); sweep 2026-09-27)*; the AOF loss window; the abandoned-restoration edge; the wds18 persistence
settings. All but the last belong to the Kind reproduction (Verification).

### Measurements (docker), 2026-09-27

All on a user-defined docker network, containers named `vko-verify-036-*` (auditor) and
`vko-verify-036s-*` / `vko-verify-036e-*` (skeptic and editor), every one removed afterwards
(`docker ps -a` and `docker network ls` filtered on `vko-verify-036` printed nothing). Images
`valkey/valkey:9.1.1` and `valkey/valkey:8.1.9`. `COMMON` below is
`--save "" --appendonly no --repl-diskless-sync yes --repl-diskless-sync-delay 5`, the
non-persistent replication settings the operator renders. Writes are
`for i in $(seq 1 500); do echo "SET k$i v$i"; done | valkey-cli`.

| Id | What | Command (condensed, exact flags) | Result |
|---|---|---|---|
| exp1 | an empty master restarts; its replica | master `valkey-server $COMMON`; replica `valkey-server $COMMON --replicaof <master> 6379`; 500 keys; `docker kill` + `docker start` the master; replica `DBSIZE` and `master_replid` every second | 9.1.1: replica 500 until t+6 s, 0 at t+7 s, `master_replid` 8194104c to 1607f3c0, log "Full resync from primary … Flushing old data"; master `DBSIZE` 0 from t+1 s. 8.1.9: 500 until t+4 s, 0 at t+6 s, "PRIMARY <-> REPLICA sync: Flushing old data" |
| exp2 / exp2b | the end of a non-Sentinel roll | b promoted (`replicaof no one`) holding 500 keys, c its replica; a (pod-0) started with `--replicaof b` and full-synced (`dump.rdb` in `/data`); restoration as `promotePod0AndRedirect` does it: `replicaof no one` on a, `replicaof a 6379` on b and c; 500 more keys on a; `docker kill` + `docker start` a; roles, links, `DBSIZE`, replid and offset every 2-3 s; then the recovery emulated (`replicaof no one` on a, `replicaof a` on b and c) | both pins: after the restart a is `role:slave` (link down, 500 keys, replid 85ad72b7, offset 0), b and c `role:slave` (link down, 1000 keys, replid cad27df4, offset 16581); a and b log "-NOMASTERLINK Can't SYNC while not connected with my master" every second; no master for the 16 s window. After the emulated pod-0 recovery all three hold 500. Independent re-run on 9.1.1: the same shape (a 500 keys, replid c27f0912, offset 0; b and c 1000, replid f2ade96a, offset 16567; after the recovery 500 on all three) |
| exp3 | a Sentinel-promoted master with a stale snapshot | y started from an init-once config file in `/data` with `replicaof x` and full-synced 500 keys; on y `replicaof no one` + `config rewrite` (what Sentinel sends); x `replicaof y`; 500 more keys on y; `docker kill` + `docker start` y | both pins: 0 `replicaof` lines in y's config after the rewrite; y restarts as master with 500 keys; x logs "Trying a partial resynchronization", then "Full resync from primary", and drops from 1000 to 500 |
| exp4 | `sentinel master-reboot-down-after-period` against an empty restart | master and replica as exp1; one `valkey-sentinel` with `sentinel monitor mm <master> 6379 1`, `down-after-milliseconds mm 5000`, `failover-timeout mm 60000`, `master-reboot-down-after-period mm <period>`; 500 keys; `docker kill` + `docker start` the master; replica `DBSIZE`/role and Sentinel's master every 2 s | period 1000 ms, both pins: only `+reboot`, about 8 s after the restart, no `+sdown`, no failover; the replica flushed to 0 at about t+7-9 s. Period 1 ms, 9.1.1: the replica flushed to 0 by t+6 s, then `+sdown`, `+odown`, `+switch-master` at about t+8 s promoted that empty replica |
| exp5 | a shell prototype of decision 1's hold guard | master command: config in `/data/v.conf`, marker `/data/.started`; if the marker exists and the config has no `replicaof`, loop `timeout 3 valkey-cli -h <sentinel> -p 26379 SENTINEL get-master-addr-by-name mm \| head -1` (once a second, at most 40) until it names another host, then append `replicaof <named> 6379`; `touch` the marker; `exec valkey-server /data/v.conf`. Replica; one Sentinel, quorum 1, down-after 5000; 500 keys; `docker kill` + `docker start` the master | both pins: the guard held 6 s ("sentinel names <self>, holding" to "names 172.x, booting as its replica after 6s"); `+sdown`, then `+switch-master` to the replica; the replica kept 500 keys throughout; the restarted pod came up as its replica and re-synced 500 |
| exp6 | persistent mode `rdb`, `SIGKILL` | master `valkey-server --save "900 1" --save "300 10" --save "60 10000" --dir /data --appendonly no --repl-diskless-sync yes --repl-diskless-sync-delay 5`; replica `--save "900 1" --appendonly no --replicaof <master> 6379`; 500 keys, `SAVE`, 500 more; `docker kill` + `docker start` the master | both pins: the master reloads 500; the replica logs "Trying a partial resynchronization", then "Full resync from primary", and drops from 1000 to 500 |
| exp6term | the same, `SIGTERM` | as exp6, but `docker stop -t 20` instead of `docker kill` (and `docker kill` again as the control) | `stop`, 9.1.1 and 8.1.9: master logs "Saving the final RDB snapshot before exiting", "DB saved on disk", reloads 1000; the replica logs "Successful partial resynchronization with primary" and keeps 1000. `kill`, 9.1.1: replica 1000 until t+6 s, 500 at t+8 s, "Full resync from primary", "Flushing old data" |
| probe exit codes | what the data pod's probe sees | `docker run -d --rm valkey/valkey:9.1.1 valkey-server --save "" --replicaof 10.255.255.1 6379 --replica-serve-stale-data no`; `valkey-cli ping; echo exit=$?`; `valkey-cli -p 1 ping; echo exit=$?` (also run on 8.1.9, and `valkey-cli -a wrong ping`) | `MASTERDOWN Link with MASTER is down …`, `exit=0`; "Could not connect … Connection refused", `exit=1`; a wrong password gets an AUTH error, prints `PONG` and exits 0 |
| PID 1, `DEBUG`, `CLIENT PAUSE` | the reproduction's triggers | `docker run -d valkey/valkey:9.1.1 valkey-server --save ""`; `docker exec … sh -c 'kill -9 1'`; `valkey-cli debug sleep 0`; `valkey-cli config get enable-debug-command`; `valkey-cli client pause 3000 ALL`; `timeout 5 valkey-cli ping` | `kill` exits 0 and the container keeps running, `PING` answers `PONG`; "ERR DEBUG command not allowed"; `enable-debug-command no` (default `PROTECTED_ACTION_ALLOWED_NO`, `src/config.c` 9.1.1:3376); `CLIENT PAUSE` returns `OK` and the next `PING` returns after 3 s |

## Impact

- On a non-persistent multi-replica cluster, **one container restart of the master discards the
  dataset on every pod**, while two healthy replicas held it a second earlier. The replication
  the user configured three pods for does not protect against this event. *(narrowed
  2026-09-27, by the variants table: the whole dataset for a master that booted with the
  master config; the writes since its last full sync for a Sentinel-promoted one; ~~a
  different, unmeasured failure for one promoted with `REPLICAOF NO ONE` — by reading, the
  master of every non-Sentinel cluster after its first roll.~~)* *(corrected 2026-09-27 at
  `84a39c2`, all measured (docker): for one promoted with `REPLICAOF NO ONE` — by reading, the
  master of every non-Sentinel cluster after its first roll, unless the roll's restoration was
  abandoned — a write outage with no master until `checkAndRecoverNoMaster` runs, then the loss
  of every write since the roll, because the recovery promotes the restarted pod-0 (exp2).)*
- The triggers are routine: a memory limit reached on a cache with `noeviction`, a master
  blocked past the liveness budget, any crash of `valkey-server`. Two of the three are this
  operator's own configuration.
- ~~The CR shows nothing: `Ready` stays `True`, the phase `OK`, `status.masterPod` the same pod.~~
  *(corrected 2026-09-27 at `84a39c2`: nothing on the CR records the flush in any variant. The
  `REPLICAOF NO ONE` variant shows a transient phase `Error`, "No master detected, recovering by
  promoting pod-0" ([`valkey_controller.go:2913-2916`](../../internal/controller/valkey_controller.go)),
  and then `OK` over the rolled-back dataset. For the other variants nothing lasting is written,
  by reading; the transient readiness dip of the restarted pod is not measured.)*
- ~~Persistent clusters are not affected in this shape: the restarted master reloads its RDB or
  AOF. Whether the replicas full-resync from it (a restart-time snapshot, writes since the
  last save lost) is the ordinary persistence trade-off, not this ticket.~~ *(corrected
  2026-09-27 at `84a39c2`: persistent clusters in mode `rdb`, the default mode, are affected in
  the same shape. After an OOM kill, a crash or a hang ended by `SIGKILL`, the restarted master
  reloads its last save and every replica full-resyncs back to it — measured (docker), exp6,
  1000 to 500 keys, up to 15 minutes of writes at a low write rate. It is not the ordinary
  persistence trade-off: the replicas held the newer data, and it is the replication that
  discards it. A graceful stop saves first and loses nothing (exp6term). Mode `aof` or `both`
  bounds the loss to about the fsync interval, not measured.)*
- *(added 2026-09-27 at `84a39c2`)* A non-persistent recorded master that is **replaced** on a
  non-Sentinel tier (a hard node failure, so no drain) boots empty and the replicas flush — the
  open half of ADR 0008's "mirror case of D8", by reading; not measured in this ticket.
- Security: none. *(2026-09-27 at `84a39c2`: the triggers a client can cause — `SHUTDOWN`,
  `CLIENT PAUSE`, an OOM write burst — need the cluster password where auth is on, and whoever
  holds it can `FLUSHALL` directly; without auth anyone can. No trust boundary is crossed.)*

## Options

Six decisions, each presented on its own. Decisions 1 and 2 choose the mechanism per topology;
3 is part of 2; 4 sets the scope of 1 and 2; 5 is part of 1; 6 is the replaced-pod half. The
Kind reproduction (Verification) runs before any of them is implemented. Standing rules every
option below keeps unless it says otherwise: record before promote and no escape on a failed
record ([ADR 0009](../adr/0009-an-unrecorded-promotion-is-not-a-promotion.md) D6, D7),
provenance ([ADR 0020](../adr/0020-write-only-what-the-operator-owns.md)), bounded waits
([ADR 0010](../adr/0010-every-rolling-update-wait-is-bounded.md)), the image-tool list
(`RequiredImageTools`, [`image_requirements.go`](../../internal/builder/image_requirements.go) —
`sh`, `valkey-cli`, `timeout`, `sleep`, `grep`, `head` are already in it, so no option below
adds a tool, provided the marker is written by shell redirection: exp5 used `touch`, which is
not in the list), and the test policy ([ADR 0017](../adr/0017-test-and-ci-policy.md): an e2e on
both Valkey lines with its revert check).

### Decision 1 — Sentinel tier: what stops a restarted master from serving its replicas an older dataset?

**Mechanism today.** The Valkey container runs `valkey-server` on the config the data init
wrote once into the writable `emptyDir` ([`statefulset.go:333`](../../internal/builder/statefulset.go),
[`:336-339`](../../internal/builder/statefulset.go), [`:345-349`](../../internal/builder/statefulset.go);
volume [`:233-238`](../../internal/builder/statefulset.go); command
[`:822-833`](../../internal/builder/statefulset.go)). A container restart re-reads that file and
`/data` and comes back on the same address. The first restart after 10 minutes of healthy
running is immediate, an empty Valkey answers within about 1 s, Sentinel's `down-after` is
5 s ([`sentinel.go:47`](../../internal/builder/sentinel.go), rendered at
[`:157`](../../internal/builder/sentinel.go)), so no failover happens; the replicas' partial
resync is refused and after `repl-diskless-sync-delay 5` they flush (exp1: everything lost) or
roll back (exp3: the writes since the last full sync lost).

**What the choice changes:** what the Valkey container does on a restart inside a living pod,
before `valkey-server` starts. **What it does not change:** the init container, a pod's first
start, a replaced pod (decision 6), the Sentinel config or the shared config hash (so the
Sentinel tier does not roll), the operator's reconcile. Either option rolls the data tier of
every Sentinel cluster in decision 4's scope once, through the pod-spec hash
([`statefulset.go:1228-1239`](../../internal/builder/statefulset.go)); the roll is
failover-aware and lossless for more than one data pod. A tier with one data pod has no peer
to protect and so gets no guard rendered at all (decision 4): a rendered guard changes its pod
spec, and the roll that follows replaces its only pod, which discards a non-persistent dataset
for no protection. `needsInitContainer` is true for a Sentinel tier of any size
([`statefulset.go:645-647`](../../internal/builder/statefulset.go)), so the render condition
cannot simply be the volume's.

- **Hold guard (recommended).** A wrapper in the container command keeps a marker in the
  writable config `emptyDir`. On a restart — marker present, config without `replicaof` — it
  asks the Sentinels with the same query as the data init. While they name this pod, it does
  not start `valkey-server`: nothing listens, the probe exits 1, the pod is not Ready and
  leaves the `-rw` endpoints although the labeler keeps its old `master` label, and Sentinel
  fails over after the 5 s `down-after`. Once another pod is named, the wrapper appends
  `replicaof <named> <port>` and `exec`s. What it does when no failover comes is decision 5.
  *Cost:* S-M — `sh -c` for every rendered tier, auth or not, at
  [`statefulset.go:822-833`](../../internal/builder/statefulset.go); the Sentinel auth
  condition mirrored (`IsAuthEnabled() && !IsSentinelAuthDisabled()`,
  [`:265-268`](../../internal/builder/statefulset.go)) and the Sentinel TLS flags (`--tls
  --cacert` only, [`:254-258`](../../internal/builder/statefulset.go)); a builder unit test;
  `make test-image-tools`; the e2e; an ADR for the rule. *Consequences:* from then on every
  master container restart is a Sentinel failover, and the restarted pod rejoins as a replica
  with a full sync; measured outage about 6 s (exp5). It must ask Sentinel the same question
  as the data init after T35 decision 3's change to it, not today's first-answer loop at
  [`statefulset.go:294-316`](../../internal/builder/statefulset.go), so it shares one
  function with that change and lands with or after T35 change 2. It creates decision 5.
  *(Read in [T35](035-master-records-lag-the-real-master.md) at `84a39c2`: decision 3 as
  taken is C1 + C2 — C1, the majority of the answering Sentinels; C2, a Sentinel with a
  failover in progress is not a settled voice — but T35 proposes a re-decision to **C4**, "an
  empty pod named master while Sentinel knows replicas waits for a peer", in the Sentinel data init,
  keyed on an empty `/data` and on `num-slaves > 0` from `SENTINEL MASTER` — because C1 + C2
  as specified close nothing it measured, and its change 2 now waits for that re-decision.
  T35 records C4 and this guard as compatible, not identical, to be one shared shell function
  with one condition set and one ADR ("an empty data pod does not take the master role while
  Sentinel knows a replica", with its bound), decided once for both tickets. Which query the
  guard shares therefore follows T35's re-decision, not C1 + C2 as a given. Where the two
  differ and the shared ADR has to settle it: this guard keys on a restart marker and covers
  a stale snapshot as well as an empty `/data`, and decision 4 recommends it on persistent
  tiers too, while C4 keys on an empty `/data` and leaves persistent pods with data alone;
  C4's `num-slaves > 0` answers the fresh-cluster case decision 5 answers with a peer
  `DBSIZE`.)*
- **Boot-as-replica guard.** On a restart — marker present, config without `replicaof` — the
  wrapper appends `replicaof <a peer>` and starts at once. The peers are replicas with a
  broken link and refuse it with `NOMASTERLINK` (exp2), so nobody flushes; Sentinel sees its
  master reporting `role:slave` and fails over by its role-mismatch rule. *Cost:* S — one shell
  branch, no Sentinel query in the container; the same snippet as decision 2's guard, so one
  code path serves both topologies. *Consequences:* the role-mismatch rule needs
  `down_after + 2 × info_period` = 25 s after the `INFO` that shows `role:slave`
  (`sentinel.c` 9.1.1:4482-4484, `INFO` every 10 s at `:86`), a write outage of about 25-35 s
  per master container restart, read from source and not measured. The pod answers `PING`, so
  it is Ready, labelled `replica`, selected by `-r`, and serves its empty or stale dataset to
  readers until the failover and its own full sync (`replica-serve-stale-data yes`). It never
  trips the liveness probe, so it needs no decision 5.

**Why the hold guard.** Checkable in exp5 against exp1: across the same restart the replica went
from 500 to 500 keys with the guard and from 500 to 0 without it, on both pins, with an outage
of about 6 s. It beats the boot-as-replica guard on each restart twice over: about 20-30 s
less write outage, because it does not wait for Sentinel's 25 s role-mismatch timer, and no
stale or empty reads through `-r`, because a pod that does not listen is not Ready. What the
runner-up saves — no Sentinel query in the container, no decision 5 — is small: the query is
the one T35 decision 3 rewrites for the init anyway, and decision 5's recommended form reuses
the runner-up's branch as its fallback.

### Decision 2 — non-Sentinel tier: what stops a restarted master from wiping or rolling back its replicas?

**Mechanism today.** No arbiter exists. A pod-0 that booted with the master config (ordinal
fallback [`statefulset.go:531-537`](../../internal/builder/statefulset.go) or self-claim
[`:528-530`](../../internal/builder/statefulset.go)) restarts empty as master and the replicas
flush (exp1); `checkSteadyStateSplitBrain` sees one labelled master
([`steady_state_master.go:151-181`](../../internal/controller/steady_state_master.go)) and the
recovery sees a master ([`valkey_controller.go:2905`](../../internal/controller/valkey_controller.go)).
Every non-Sentinel roll ends with pod-0 promoted by `REPLICAOF NO ONE`
([`rolling_update.go:4610`](../../internal/controller/rolling_update.go), in
`promotePod0AndRedirect` at [`:4587`](../../internal/controller/rolling_update.go)) after it
full-synced from the roll's promoted pod, with `replicaof <that pod>` still in its config; a
drain promotion ([`drain.go:172`](../../internal/sidecar/drain.go)) leaves the same shape on a
non-zero ordinal. A restart boots it as a replica of its own replica, the `NOMASTERLINK`
deadlock follows (exp2), and `checkAndRecoverNoMaster`
([`valkey_controller.go:2881-2968`](../../internal/controller/valkey_controller.go)) records and
promotes the hardcoded `<sts>-0` ([`:2925`](../../internal/controller/valkey_controller.go)),
losing every write since the roll when pod-0 is the restarted pod. The recovery refuses while
any pod is unreachable ([`:2905`](../../internal/controller/valkey_controller.go)) and is
suppressed during a roll ([`:2886-2888`](../../internal/controller/valkey_controller.go)).

**What the choice changes:** which pod the no-master recovery promotes, and what a restarted
would-be master boots as. **What it does not change:** record before promote (ADR 0009 D6), the
unbounded recovery (D7), provenance (the recovery proves the StatefulSet first, ADR 0020 D9),
the `unreachable > 0` refusal, the suppression during a roll.

- **(a) data-aware no-master recovery plus (b) a boot-as-replica guard (recommended).**
  (a): `checkAndRecoverNoMaster` promotes the available pod holding the newest data instead of
  pod-0 (which pod, decision 3), records it first, and refuses on an unreadable answer
  (ADR 0028 D3's rule applied to a promotion). (b): on a restart — marker present, config
  without `replicaof` — the Valkey container appends `replicaof <a peer>`, so it can never
  serve as an empty or stale master; the peers refuse it with `NOMASTERLINK`, nobody flushes,
  and (a) promotes the pod with the data. *Cost:* M for (a) — the selection, unit tests with
  `fakeValkeyServer`, an e2e that restarts pod-0 after a roll; S for (b), the same shell branch
  as decision 1's runner-up; one ADR that extends ADR 0028 from the demotion to the recovery
  promotion and amends ADR 0008's residual (its recovery half). *Consequences:* (a) is
  operator-only and rolls nothing; (b) rolls every multi-replica non-Sentinel data tier in
  decision 4's scope once, a lossless failover-aware roll. Writes fail from the restart until
  a pass runs the recovery — by reading 5-20 s through the StatefulSet status watch and the
  10 s requeue, without depending on T35 decision 5's Pod watch, which may shorten it; not
  measured. Until the recovery redirects it and its full sync lands, the guarded pod is Ready,
  labelled `replica` and serves its empty or stale dataset through `-r`
  (`replica-serve-stale-data yes`) — still strictly better than today, where every pod ends
  with that dataset. Inside a roll the recovery is suppressed, so a guarded restart there
  waits for the roll's own bounds.
- **(a) alone.** *Cost:* M. *Consequences:* removes the measured loss for the post-roll master
  — by reading the master of every non-Sentinel cluster after its first roll — and rolls
  nothing; leaves the booted-as-master variant, so a cluster with no roll and no master-pod
  deletion since its creation still loses everything on a master container restart (exp1).

**Why (a) plus (b).** Checkable in the measured deadlock (exp2b): the restarted pod-0 reports
500 keys, the old replid and offset 0; pod-1 and pod-2 report 1000 keys, the current replid and
offset 16581. A data-aware choice promotes pod-1 or pod-2 and loses nothing; today's pod-0
choice loses 500. It beats (a) alone by also covering fresh clusters, at S and one lossless
roll. Of the ways to cover the booted-as-master variant it is the only one that keeps every pod
reachable, so it needs no exception to the `unreachable > 0` refusal. Land (a) first: it rolls
nothing and alone covers the post-roll state.

*(Added 2026-09-27, consistency pass.)* **Related decisions elsewhere.**
[T23](023-pauserollingupdate-records-no-pause.md)'s recommended D1 option D keeps the
no-master recovery (`checkAndRecoverNoMaster`) running in the pass in which a data roll pauses,
and 023 weighs D over C partly on that recovery being the safe exit; (a) here is what makes that
exit dataset-aware, so this decision changes the weight of T23's D1, as 023 records.
[T62](062-resetsentinelstate-falls-back-to-sentinel-reset.md) records a Sentinel-side route into
this ticket's mechanism - its measured tier-wide empty-table state after a reset toward an
unreachable master - on a cluster without persistence (not verified there); it is the Sentinel
tier's concern, not this non-Sentinel decision's.

### Decision 3 — which pod the data-aware recovery promotes (only if decision 2 takes (a))

**Mechanism today.** None: the recovery names `<sts>-0`
([`valkey_controller.go:2925`](../../internal/controller/valkey_controller.go)).
`ReplicationInfo` parses role, connected replicas, master host and port, link status and the
sync flag, and no offset or replid ([`client.go:26-33`](../../internal/valkeyclient/client.go));
a `DBSIZE` reader exists (`dbSizeReader`, [`rolling_update.go:1678`](../../internal/controller/rolling_update.go)).
ADR 0028 decided for the demotion that a pod holding zero keys never displaces one holding
some (D1) and that an unreadable count is a refusal (D3). **What the choice changes:** the
ranking only. **What it does not change:** record before promote, the refusal on an
unreachable pod.

- **Highest `master_repl_offset`, with ADR 0028 D1's zero-key veto and D3's fail-closed, ties
  to the lowest ordinal (recommended).** *Cost:* parsing `master_repl_offset` into
  `ReplicationInfo`, one `DBSIZE` per candidate for the veto, unit tests. *Consequences:*
  ranks by replication progress, which is what the loss is measured in; the veto keeps a
  divergent history with a high offset and no keys from winning.
- **Highest `DBSIZE`, with D3's fail-closed.** *Cost:* the smallest — `dbSizeReader` exists,
  no new parsing. *Consequences:* a key count is not progress: a workload that deletes keys
  ranks a stale snapshot holding more keys above the current dataset.

**Why the offset.** In exp2b the offsets separate the restarted pod (0) from the pods with the
data (16581) by construction of replication, while the key counts would do so only because
that workload never deleted; the veto keeps what ADR 0028 already decided. It beats `DBSIZE`
alone at the cost of one parsed field.

### Decision 4 — which data tiers render the guards

**Mechanism today.** The writable config `emptyDir` and its init exist on every tier for which
`needsInitContainer` holds — every Sentinel tier and every multi-replica non-Sentinel tier
([`statefulset.go:645-647`](../../internal/builder/statefulset.go)) — persistent or not, so
the marker signal works on all of them. A persistent `rdb` master rolls its replicas back
after a `SIGKILL`-type exit (exp6), not after a graceful stop (exp6term). Sentinel counts
`-LOADING` as available (`sentinel.c` 9.1.1:2729-2731), so a persistent master (either mode)
reloading a large dataset is never failed over and writes fail for the whole load; the probe
exits 0 on `-LOADING` (~~inferred from the measured exit codes~~ *(measured by [T76](076-the-exec-probes-pass-on-any-server-reply.md),
sweep 2026-09-27)*), so that pod stays Ready and in `-rw` — the Service membership read, not
measured. A Sentinel tier with one data pod also has the volume, and a
rendered guard there changes its pod spec and replaces its only pod, a lost dataset when it
is non-persistent, while it has no peer to protect (decision 1); every option below therefore
renders nothing when `spec.replicas` is 1. **What the choice changes:** which tiers roll once
on the release, and whose master container restarts become failovers or recoveries. **What it
does not change:** the mechanism of decisions 1 and 2.

- **Every multi-replica tier with the writable config volume, `spec.replicas > 1`
  (recommended).** *Cost:* none beyond decisions 1 and 2 — for more than one data pod
  `needsInitContainer` always holds, so the render condition is `spec.replicas > 1` alone and
  reads no persistence field. *Consequences:* rolls every multi-replica data tier once, persistent
  `aof`/`both` tiers included; every master container restart on those tiers becomes a
  failover (Sentinel) or a recovery (non-Sentinel), which also replaces the reload-time write
  outage with the roughly 6 s failover and removes the AOF fsync-window loss. The guard's
  signal (marker present, no `replicaof`) cannot tell a saved shutdown from a `SIGKILL`, so a
  graceful master container restart on a persistent tier, lossless today, becomes a failover
  too — in practice a liveness kill of a process that still handles `SIGTERM`, which on a
  Sentinel tier Sentinel has already failed over during the stall.
- **Non-persistent tiers and persistent tiers in mode `rdb`.** *Cost:* the render condition
  reads `spec.persistence.mode` as well, and the unit and e2e matrix gains a mode dimension.
  *Consequences:* covers every loss measured in this ticket; saves one lossless roll of the
  multi-replica `aof`/`both` tiers and leaves their reload outage and fsync-window loss as
  they are. A mode change already rolls through the config hash, so the mode-dependent
  condition adds no extra roll.
- **Non-persistent tiers only.** *Cost:* the render condition reads
  `spec.persistence.enabled`. *Consequences:* the smallest roll; covers the default
  configuration; a persistent `rdb` cluster keeps rolling every replica back to its last save
  after an OOM kill or a crash of its master — a measured loss on the persistent setup a user
  gets by setting only `enabled: true`.

**Why every multi-replica tier.** The `rdb` rollback is measured (exp6), which rules out
"non-persistent only". Between the other two, the `rdb`-scoped condition saves one lossless,
failover-aware roll of the `aof`/`both` tiers, and pays for it with a mode branch, a test
dimension and an ADR rule per mode, while leaving those tiers with a reload outage the guard
would turn into a 6 s failover. The benefit on `aof` tiers is read from source, not measured;
if the Kind reproduction shows the `-LOADING` window to be negligible, the runner-up becomes the
cheaper equal and should be taken instead.

### Decision 5 — what decision 1's hold guard does when no failover comes (only if decision 1 takes the hold)

**Mechanism.** Sentinel fails over only with a quorum and a replica it can select, so without
them it keeps naming the held pod. The liveness probe kills a container that does not answer
`PING` 55-65 s after its start ([`statefulset.go:859-870`](../../internal/builder/statefulset.go));
the kubelet restarts it with back-off (10 s, 20 s … 300 s) and the guard runs again, because the
marker survives in the `emptyDir`. **What the choice changes:** whether the pod ever starts as
master while a peer may hold data, and whether it crash-loops. **What it does not change:** a
pod's first start, decision 2's guard (which never holds), Sentinel.

- **Ask the peers first, then hold, then fall back to a replica (recommended).** Before
  holding, ask every peer `DBSIZE`: when every peer answers 0, start at once as today (nothing
  to protect). Otherwise hold while Sentinel names this pod; after a bound below 55 s, append
  `replicaof <a peer>` and start — the decision 1 runner-up's branch. *Cost:* one `DBSIZE` per
  peer with its own flag set: the Valkey TLS port 16379 with `--tls --cacert` (enough, because
  `tls-auth-clients optional`, [`configmap.go:93`](../../internal/builder/configmap.go)) and the
  password whenever auth is on, even with `spec.sentinel.disableAuth` — the Sentinel-cluster
  init builds only Sentinel flags ([`statefulset.go:254-268`](../../internal/builder/statefulset.go)),
  the peer flags exist only in the non-Sentinel init ([`:425-433`](../../internal/builder/statefulset.go)).
  *Consequences:* never crash-loops; never starts an empty or stale master over a peer that
  may hold data (an unreadable or unreachable peer counts as "may hold data", ADR 0028 D3's
  direction); after the bound the pod is Ready as a replica and serves stale reads through
  `-r`, and Sentinel's role-mismatch rule fails over by itself once it has a quorum. With no
  quorum the tier has no master and no CR condition says so, unless one is added (ADR 0027
  row).
- **Hold, then fall back to a replica, no peer query; no peer, start at once.** *Cost:* the
  fallback branch only. *Consequences:* as above, but an all-empty tier waits the bound plus
  the 25-35 s role-mismatch failover for nothing, and a tier whose Sentinels know no replica
  (replicas that never connected to this master are not in its `INFO`) cannot fail over at
  all: it sits with no master and a Ready replica pod — read, not measured.
- **Hold only while a peer may hold data, then exit non-zero.** *Cost:* the peer query as in
  the recommended option. *Consequences:* the pod stays not Ready, so no stale reads; the
  outage is visible as `CrashLoopBackOff`, and once quorum returns the pod rejoins only after
  its current back-off, up to 300 s.

**Why ask, hold, fall back.** It is the only form that is right in all three boundary cases:
an all-empty tier starts at once (the second option waits, the third too if a peer is
unreachable); a no-quorum tier whose replicas hold data keeps them without crash-looping (the
third option crash-loops); a tier whose Sentinels know no replica starts as master when the
peers are empty rather than sitting masterless (the second option). It beats the second
option by one `DBSIZE` per peer with a flag set of its own. Two points stay open inside the
recommended form, neither a separate choice: if T35 re-decides its decision 3 to C4, C4's
`num-slaves > 0` from `SENTINEL MASTER` is a second signal for the fresh-cluster case, and the
shared ADR of decision 1 settles one condition set for both; and appending
`replica-serve-stale-data no` to the fallback config would make the fallen-back pod refuse data
reads with `MASTERDOWN` while its probe still exits 0 (measured (docker), probe exit codes), so
`-r` would serve no stale data — but the line stays in that container's config for its whole
life, so it is not part of the recommendation without a Kind run.

### Decision 6 — a replaced (not restarted) recorded master on a non-persistent non-Sentinel tier

**Mechanism today.** A recreated pod has a new `emptyDir` and no marker, so no guard above
fires. Its init finds no master with connected replicas in Phase 1
([`statefulset.go:452-488`](../../internal/builder/statefulset.go)), reads its own name as the
known master in Phase 2 ([`:505-508`](../../internal/builder/statefulset.go)), boots empty as
master ([`:528-530`](../../internal/builder/statefulset.go)), and the replicas full-resync to
it. ADR 0008 records this as open ("there is no cheap guard"). A drain ([ADR 0012](../adr/0012-the-sidecar-records-its-drain-promotion-on-the-pod.md)
D10) prevents it for an ordinary pod delete; a hard node failure runs no drain. On a Sentinel
tier the replaced pod asks Sentinel, which has normally failed over; a replacement answering
before `down-after` without a forced failover is named master by Sentinel and boots empty, the
same chain through a pod replacement — T35 Impact C, inference from upstream code, not
measured, and T35 decision 3's proposed C4 is its fix, not this decision. **What the choice
changes:** the init's two master branches on non-persistent non-Sentinel tiers. **What it does
not change:** a persistent tier (its volume keeps the data), the unreachable-peer case (ADR
0008's first residual stays as accepted), Phase 1.

- **A peer-data check in the init's master branches (recommended).** On a non-persistent tier
  the Phase 2 self-claim and the Phase 3 ordinal-0 fallback boot as a replica of a peer when
  any peer answers `DBSIZE > 0`, which reaches decision 2's deadlock, where (a) promotes the
  pod with the data. *Cost:* a few shell lines in a loop that already queries every peer with
  the peer flags ([`statefulset.go:425-433`](../../internal/builder/statefulset.go)); a
  builder unit test; an e2e that deletes the recorded master with `--grace-period=0`; the ADR
  0008 residual superseded. *Consequences:* lands in the same roll as decision 2's guard,
  because the init is in the pod spec; depends on decision 2 (a) for the promotion that
  follows, without which pod-0 is promoted as today; applies ADR 0028 D1's evidence rule at
  boot.
- **Accept the residual.** Record in ADR 0008 that a hard-failed recorded master on a
  non-persistent non-Sentinel tier discards the dataset, and say so in
  [docs/operations/persistence.md](../operations/persistence.md). *Cost:* XS. *Consequences:*
  a documented data-loss path on node failure stays open.

**Why the peer check.** ADR 0008's reason for leaving it open — the pod cannot tell "new" from
"the cluster is empty" — is answered by asking the peers, which this init already does in
Phase 1; the check costs a few lines in that loop and closes a documented loss with the same
evidence rule as ADR 0028 D1. Accepting it is cheaper only by the few lines.

## Decision

**Not decided.** The six decisions under Options are open; the recommended option of each is
marked there. The Kind reproduction (Verification) runs before any implementation. *(The
earlier text of this section, superseded on 2026-09-27, is in History.)*

## Work list *(added 2026-09-27)*

- **XS, needs no decision, can land today:** the hazard sentence in
  [docs/operations/persistence.md](../operations/persistence.md) (last Verification item).
  **Done 2026-09-27** (History).
- **XS, needs no decision** *(added 2026-09-27 at `84a39c2`)*: the closing advice of that
  section ([docs/operations/persistence.md:24-25](../operations/persistence.md)), "Enable
  persistence for any dataset that must survive a restart of the master's container", is
  incomplete: with mode `rdb`, the default mode, an OOM kill or a crash of the master still
  rolls every replica back to the last save (exp6); add that, and that mode `aof` or `both`
  bounds the loss to about the fsync interval. Cites no ticket (ADR 0034 D7). Outside
  `docs/tickets/`, so not done in the 2026-09-27 ticket run.
- **Needs no decision** *(added 2026-09-27 at `84a39c2`)*: file the missing `maxmemory` as its
  own ticket. **Done 2026-09-27: filed as
  [T71](071-maxmemory-is-never-set-so-an-oom-kill-is-the-only-memory-bound.md)**, and this
  ticket's `maxmemory` Fact bullet points at it. ~~T52's option C still cites this ticket's bullet;
  repointing it at T71 is T52's own edit, not done here.~~ *(Sweep 2026-09-27: done in T52 the
  same day; its option C note now points at T71.)*
- **Decided with the filing, needs no further decision:** the Kind reproduction, in the plan
  rewritten under Verification.
- **Waits on the decisions:** the mitigation per decision 1-6, the ADRs (the restart-guard
  rule; ADR 0028 extended to the recovery promotion; ADR 0008's residual amended or
  superseded), the unit tests and the e2e.
- **Closing (ADR 0034):** the decisions into ADRs; the operator-visible consequence into
  [docs/operations/persistence.md](../operations/persistence.md); the guard and the recovery
  into `docs/developer/`; then `state: done`, a "what shipped" History line, `git grep` for
  T36 and this file name, and the move to `archive/`.

## Verification

- [ ] **Reproduction (first).** A 3-replica non-persistent cluster on Kind, with and without
  Sentinel. Write keys until `DBSIZE` is stable on the replicas; ~~`kill -9` the `valkey-server`
  process inside the master container~~ *(corrected 2026-09-27: that does nothing —
  `valkey-server` is the container's PID 1 and a SIGKILL sent to it from inside its own PID
  namespace is dropped, measured (docker) for both command shapes. Kill it from the Kind node
  by host PID, `crictl inspect` → `.info.pid`, or run `SHUTDOWN NOSAVE` for a clean exit the
  kubelet restarts the same way)* (a crash, no `preStop`); read `DBSIZE` on both replicas
  and `INFO replication` on all three every second for a minute. Record: the restart time of
  the container, whether Sentinel logged `+sdown`, the replicas' `master_replid` before and
  after, and the final `DBSIZE`. Then the same with the liveness path: block the master (a
  ~~`DEBUG SLEEP 70`~~ *(corrected 2026-09-27: `DEBUG` is refused — `enable-debug-command no`
  is the image default and the generated config sets nothing, measured (docker), T57; use
  `CLIENT PAUSE 70000 ALL`, which stalls `PING` and is accepted, measured (docker))*) and watch
  the kubelet restart it. That measures the severity this ticket estimates. *(Added
  2026-09-27: run both master variants — a fresh cluster before its first roll, and one after
  a roll or a Sentinel failover — and record `/data/dump.rdb` and the config file's `replicaof`
  line before the kill; add an OOM run (low memory limit, write until `OOMKilled`) as the
  realistic trigger; ~~on the Sentinel cluster, a second pass with
  `sentinel master-reboot-down-after-period` set.~~ The Sentinel master kill shares the session
  with T35 decision 3.)* *(corrected 2026-09-27 at `84a39c2`: the reboot-directive pass is
  dropped — by source it cannot fail over a master that answers `PONG`, and exp4 measured no
  failover at 1000 ms on both pins. Prefer `kubectl exec … valkey-cli SHUTDOWN NOSAVE NOW` as
  the crash trigger: it needs no node access, so the later e2e can use it on CI's Kind — the
  operator renames no command and sets no `shutdown-*` option, and `restartPolicy: Always`
  restarts after any exit; `NOW` skips the wait for replicas. Add: the kubelet's first-restart
  latency; the time from a post-roll pod-0 restart to `checkAndRecoverNoMaster` (by reading
  5-20 s), with and without T35 decision 5's Pod watch; the abandoned-restoration edge if it
  can be forced; decision 1's hold guard with three Sentinels and quorum 2; a persistent `rdb`
  run with `SHUTDOWN NOSAVE` or an OOM kill — a plain `SHUTDOWN`, a `SIGTERM` or a liveness kill
  saves first and shows no loss (exp6term); a large persistent reload, to measure the
  `-LOADING` window and the probe outcome during it (decision 4; the probe outcome, exit 0, is
  already measured in docker on both pins by [T76](076-the-exec-probes-pass-on-any-server-reply.md),
  so the Kind run only confirms it on a cluster); a hard deletion of the
  recorded master with `--grace-period=0` on a non-Sentinel tier (decision 6).)*
- [ ] **The mitigation**, once chosen, with the revert check ADR 0017 asks for, and the same
  reproduction turned into an e2e that asserts the replicas keep their `DBSIZE` across a
  master container restart.
- [x] The ~~README~~ sentence *(corrected 2026-09-27: in
  [docs/operations/persistence.md](../operations/persistence.md), a new section after
  "Persistence Modes"; **XS, needs no decision** — the Options section owes it in every case)*:
  without persistence a restart of the master's `valkey-server` container can bring it back
  with no data or with the snapshot of its last full sync, and the replicas can resynchronize
  from it and drop what they held; enable persistence for any dataset that must survive a
  master container restart. Cites no ticket (ADR 0034 D7). *(Done 2026-09-27: the section "Without
  persistence, a restarted master can empty its replicas" sits between the modes table and
  "Changing storage on an existing cluster"; it cites no ticket, checked by grep over the added
  lines.)* *(2026-09-27 at `84a39c2`: committed in `bcc63c9`, `git log -S "a restarted master
  can empty its replicas"`; its closing advice is incomplete for mode `rdb`, Work list.)*

## History

- 2026-09-27: re-verified at `84a39c2` (an audit, a facts review and a design review, reconciled
  by the editor). **Checked:** every claim and link against the code at `84a39c2`; upstream
  Valkey 9.1.1/8.1.9 `sentinel.c`, `replication.c`, `config.c` and `sentinel.conf`, valkey-doc
  `replication.md`, the Kubernetes pages on init containers, volumes, pod lifecycle and
  container lifecycle hooks, and kubelet `prober/worker.go` at v1.36.1; ADR 0008's residuals,
  ADR 0009 D6/D7, ADR 0028 D1-D4. The defect is open and its code untouched since `4a7543e`;
  locations re-read at `84a39c2` and fixed in the links (`rolling_update.go:4199`/`:4607` to
  `:4202`/`:4610`, `valkey_controller.go:2948` to `:2949` and `:2880` to `:2881`,
  `statefulset.go:821-832` to `:822-833`, `:578-586` to `:579-587`, `:758` to `:750-758`,
  `:859-869` to `:859-870`, `valkey_types.go:991-994` to `:993-996`); the gate
  `valkey_controller.go:421-422` holds (an audit claim that it had moved to `:420-421` was
  refuted). **Found false or outdated, corrected in place:** the `REPLICAOF NO ONE` variant
  does not "maybe keep the data" — it deadlocks with `NOMASTERLINK` and the pod-0 recovery
  loses every write since the roll; "persistent clusters are not affected" is false for mode
  `rdb` after a `SIGKILL`-type exit (a graceful stop is lossless); "the CR shows nothing" is
  false for the `REPLICAOF NO ONE` variant (transient phase `Error`); the liveness budget is
  55-65 s after a start, not 15 s + 5 × 10 s; `master-reboot-down-after-period` rests on a false
  premise (the reboot flag clears on the first `PONG`); a config line rolls both tiers of a
  Sentinel cluster, not only one (one shared config hash) — this made the old table's roll
  scope wrong for the reboot directive ("every Sentinel tier once", `sentinel.go:93`) and for
  `maxmemory` ("every data tier"). Four inferences became documented upstream behaviour (init
  re-run, `emptyDir` across crashes, the immediate first restart with its back-off, `preStop`
  on a liveness kill); the Sentinel-promoted loss became measured. New facts: the probe exit
  codes, the labeler keeping its label, the observer's blindness, ADR 0008's two-half residual,
  the abandoned-restoration edge, the replaced-pod half, the back-off limit and its node knobs.
  The wds18 persistence claim is relayed from T35 and not verified. **Measured:** exp1-exp6,
  exp6term, the probe exit codes, PID 1/`DEBUG`/`CLIENT PAUSE`, commands and results under
  "Measurements (docker)"; every container removed. **Options rewritten** as six decisions,
  replacing the candidate list, the "Weighed" table and its justification paragraph.
  **Removed options:** (1) "`sentinel master-reboot-down-after-period`" — one Sentinel config
  line; false premise, it fails over only a master still answering `-LOADING`, measured no
  failover at 1000 ms and a failover after the flush at 1 ms; (2) "`maxmemory` derived from the
  memory limit" — removes only the OOM trigger and is a capacity decision of its own, to be
  filed as its own ticket (Work list); (3) "the docs sentence only" — already landed in
  `bcc63c9`, no longer a choice, and refusing a measured S-M fix for a high-severity loss is
  disproportionate; (4) "start guard, non-Sentinel half" (hold plus an operator path that
  promotes while the recorded master is unreachable) — would reopen the `unreachable > 0`
  refusal, since a held restart and a partitioned live master look alike from outside, and
  the data-aware recovery reaches the same end with every pod reachable, at M+S instead of L;
  (5) "the liveness probe" change — removes only the rarest trigger and the only recovery from
  a real hang, and with decision 2 a liveness kill no longer loses data; (6) "refuse the
  non-Sentinel topology in an ADR" (considered in the audit) — the post-roll loss runs through
  the operator's own pod-0 promotion; (7) "start as master after the bound" (the review's
  "starts anyway") — reintroduces the loss decision 1 prevents, delayed; (8) "always exit
  non-zero after the bound" — dominated, it crash-loops a single-pod or all-empty tier for
  nothing. **Added options:** the boot-as-replica guard (decisions 1 and 2), the data-aware
  recovery (decision 2), the ranking (decision 3), the scope choices (decision 4), the bound
  variants (decision 5), the replaced-pod check (decision 6). **Recommendation changes:**
  decision 1 keeps the hold guard, with its justification rewritten (it also avoids stale
  reads through `-r`, and it is what creates decision 5); the non-Sentinel half, which had no
  recommendation ("follows the reproduction"), now recommends the data-aware recovery plus the
  boot-as-replica guard; decision 4 recommends every multi-replica tier (`spec.replicas > 1`)
  against the audit's "non-persistent plus `rdb`", because Sentinel counts `-LOADING` as
  available (`sentinel.c` 9.1.1:2729-2731) and the `rdb` scoping saves only one lossless roll;
  decision 5 recommends asking the peers, holding, then falling back to a replica, against the
  audit's "exit non-zero" (crash loop) and the design review's "fall back without a peer
  query" (masterless when Sentinel knows no replica, read). **Superseded Decision text**, moved
  here verbatim: "**Open.** First step, decided with the filing: reproduce on Kind before
  costing anything. *(2026-09-27: costed above without the reproduction, on Hans's request;
  still not decided. The reproduction runs in one Kind session with T35 decision 3.)*"
  **Cross-ticket:** T35 decision 3 changes the Sentinel query decision 1's guard must share, so
  the Sentinel half lands with or after T35 change 2; T35 at `84a39c2` proposes re-deciding it
  from C1 + C2 to C4 (an empty pod named master waits while Sentinel knows a replica), records
  C4 and this guard as one shared function and one ADR, and its Impact C (a replacement
  answering before `down-after`) is the Sentinel counterpart of decision 6; T35 decision 5's Pod watch may
  shorten the non-Sentinel no-master window but is not needed for it; T35's finding text
  ("comes back empty and stays the master") holds only for the booted-as-master variant, and
  its C3 premise (no `dump.rdb` on a non-persistent pod) holds for fresh replacement pods, not
  for a restart inside a living pod; T12's `min-replicas-to-write` would not prevent the flush
  (connecting is what flushes the replica), no dependency; T57's `DEBUG` finding re-measured;
  T52's option C cites the removed `maxmemory` option (Work list). **Frontmatter:** `state`
  filed → analysed (every variant measured on the Valkey side, every decision costed with a
  marked option; the Kind reproduction verifies the Kubernetes half and precedes the
  implementation, not the analysis — the audit had proposed keeping `filed`); the `severity`
  comment no longer says estimate for the Valkey side; `effort` stays L with a new reason;
  `blocked-by` stays `decision`, its comment names the T35 ordering. **Urgency** stays `next`
  by rule 3; rule 1 was checked: this ticket's own false statements are corrected in this
  entry, and [docs/operations/persistence.md:24-25](../operations/persistence.md) is incomplete
  for mode `rdb`, not measured-false (the design review noted that reading it as false would
  give `now` until the docs fix lands); `docs/operations/examples.md:15` ("Data survives pod
  restarts via a PersistentVolumeClaim") holds for a graceful restart and is incomplete for an
  OOM kill. Security stays `none`. **Review of this entry's edit (same day, read at
  `84a39c2`):** spot-checked every correction and every recommended option's premise against
  the code; fixed the empty-mode render location (`configmap.go:189-194` is the
  persistence-disabled branch, the empty-mode branches are `:216-221` and `:237-242`); added the
  missing cost that a guard rendered on a Sentinel tier with one data pod rolls, and so
  discards, its only non-persistent pod — decision 4's condition became `spec.replicas > 1`;
  added T35's proposed C4 re-decision and its Impact C to decisions 1, 5 and 6, which the edit
  had not carried; recorded that exp5's `touch` is not in `RequiredImageTools`; recorded the
  design review's `replica-serve-stale-data no` refinement under decision 5.
  Cross-ticket: in the consistency pass of the same day, a note under decision 2 records that
  T23's recommended D keeps the no-master recovery running in the pausing pass, which (a) here
  makes dataset-aware, and that T62 records a Sentinel-side route into this mechanism; T52's
  option C, which cited the removed `maxmemory` option, now points at this ticket's `maxmemory`
  Fact bullet until the new ticket is filed (Work list item still open for the filing), and T35's
  crash-restart finding carries this ticket's variants. Filed: the missing `maxmemory` (removed
  option 2 above and the Work list item) as
  [T71](071-maxmemory-is-never-set-so-an-oom-kill-is-the-only-memory-bound.md), medium, security
  none, effort M, state analysed; the Fact bullet now keeps only what this ticket needs (no
  `maxmemory`, so an OOM kill at the memory limit is a reachable restart trigger) and points at
  T71, and the Work list item is marked done. No frontmatter change: the severity, urgency and
  every decision rest on the restart mechanism, not on how the operator bounds memory, and the
  OOM trigger stands as long as T71 is open. T52's option C still cites this ticket's bullet and
  is T52's edit.
  Sweep: The probe outcome on `-LOADING` was recorded as inferred in Fact, Decision 4 and Not
  verified; T76 measured it in docker on both pins (after a restart and during a full-sync load), so
  those places now point there, and the liveness sentence ("does not answer `PING` for about 50 s")
  is precised to no reply at all, since an error reply passes (T76 Work list item 5, carried out
  here). Frontmatter, options and recommendations unchanged: they rest on the restart mechanism, not
  on the probe outcome. The Work list note that T52 option C still cited this ticket instead of T71
  is struck, because T52 now points at T71.
  Final pass: re-checked the four places that record the probe outcome on `-LOADING` (the
  liveness bullet, the readiness bullet, Not verified and Decision 4) against T76's current text
  and measurement table - `valkey-cli ping` prints `LOADING Valkey is loading the dataset in
  memory` and exits 0 after a restart (at 1 s and at about 60 s) and while a replica loads the
  RDB of a full sync, on valkey/valkey 9.1.1 and 8.1.9 - and all four already point there; the
  one place still asking a run to measure the probe outcome, the Kind reproduction in the Work
  list, now says it is measured in docker by T76 and the run only confirms it on a cluster.
  Frontmatter unchanged.
- 2026-09-27: the XS docs item landed, one file (read in `git diff` of the working tree):
  [`docs/operations/persistence.md`](../operations/persistence.md), a new section "Without
  persistence, a restarted master can empty its replicas" after the modes table. It says the
  default is `spec.persistence.enabled: false` and that such a cluster saves nothing (`save ""`,
  `appendonly no`); names the triggers (an `OOMKilled` at the memory limit, a crash, a
  liveness-probe kill); says, with "can", that the master may come back empty or with the
  snapshot of its last full sync and that its replicas can then resynchronize from it and drop
  what they held; recommends persistence for any dataset that must survive a restart of the
  master's container; and states that this comes from reading and a plain-docker run against
  the two pinned images, not from a Kubernetes reproduction. No ticket is cited. The
  reproduction, the mitigation decision and the rest of the work list are untouched; severity
  stays an estimate, urgency `next` (rule 3) unchanged. Its two code facts re-read for this
  entry: `+kubebuilder:default=false` on `PersistenceSpec.Enabled`
  ([`valkey_types.go:995-996`](../../api/v1/valkey_types.go)) and `save ""` / `appendonly no`
  from `persistenceConfig` ([`configmap.go:189-194`](../../internal/builder/configmap.go)).
  **Not verified:** everything the section says about the restart itself, as before (docker, not
  Kubernetes).
- 2026-09-27: adversarial review of the enrichment — about half of the new line references
  re-read at `4a7543e`, all held. Added in place: the no-master recovery promotes pod-0 in the
  `REPLICAOF NO ONE` variant (read); the start guard's missing bound for a tier where Sentinel
  cannot fail over, and its roll scope if rendered only without persistence. The
  recommendation stands. No frontmatter change.
- 2026-09-27: enriched - line references re-read at `4a7543e` and corrected in place; the
  Valkey side measured in docker on both pins (replica flush about 5 s after the empty master
  returns, `dump.rdb` reloaded, Sentinel's `CONFIG REWRITE`, the reboot directive accepted, the
  two reproduction steps that could not work); the three master variants; Options costed, the
  start guard's Sentinel half recommended; a work list with the XS docs sentence. **Effort
  M → L**: the recommended guard covers both topologies, and its non-Sentinel half needs a new
  operator promotion path. Urgency stays `next` (rule 3: severity high, trigger reachable in
  released code).
- 2026-09-27 — renamed to `036-non-persistent-master-restarts-empty.md` (was `local_T36-non-persistent-master-restarts-empty.md`) when the tickets were numbered.
- 2026-09-27 — **filed** as its own ticket from T35 decision 7, out of the reading done while
  costing T35 option C3 (the "fresh pod" heuristic raised the question what a *restarted*
  container boots as). Severity high is an estimate from reading, marked so in the
  frontmatter; urgency `next` by derivation rule 3 (severity ≥ medium and the trigger
  reachable in released code). Hans's proposal on the same day had been a board row with the
  file created when work starts; his answer retired the board and made the file the record.
