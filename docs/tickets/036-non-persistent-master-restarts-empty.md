---
id: T36
title: a non-persistent master that crash-restarts comes back empty and flushes its replicas
state: filed          # from reading, the Valkey side measured in docker 2026-09-27; the Kind reproduction is the next step
severity: high        # estimate - the replica flush is measured in docker, the Kubernetes chain is not
security: none
urgency: next         # severity >= medium and the trigger is reachable in released code (rule 3)
effort: L             # was M; the recommended start guard covers both topologies (History 2026-09-27)
blocked-by: decision  # the mitigation, after the reproduction
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
references re-read at `4a7543e`.)*

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
   replica config), an `emptyDir` that outlives a container restart.
2. A `valkey-server` container the kubelet restarts therefore starts with the configuration of
   its first start — the **master** config if it was master — and, on a non-persistent
   cluster, with **no dataset** (`save ""`, [ADR 0012 D10](../adr/0012-the-sidecar-records-its-drain-promotion-on-the-pod.md)).
   Same pod, same address, same hostname. *(corrected 2026-09-27, narrower: it starts with the
   config its **init** wrote, not with its current role, and "no dataset" holds only for a pod
   that never full-synced as a replica — a replica's full sync leaves `dump.rdb` in `/data`
   (`rdb-del-sync-files` is `no`), and a restarted `valkey-server` loads it despite `save ""`,
   **measured (docker)**. Three variants, in the table below.)*
3. **With Sentinel:** `down-after-milliseconds` is 5000 ([`sentinel.go:47`](../../internal/builder/sentinel.go)).
   The first restart of a crashed container is immediate and an empty Valkey boots in well under
   a second (**inference**), so Sentinel never marks the master `s_down`, performs no failover,
   and keeps the same address as master. *(2026-09-27: the restarted container answered
   `DBSIZE` within about 1 s of `docker start`, **measured (docker)**; the kubelet's restart
   latency is not measured, and if it reaches 5 s Sentinel fails over first and this step does
   not happen.)*
4. **Without Sentinel:** nothing arbitrates. `checkSteadyStateSplitBrain` sees one labelled
   master and `checkAndRecoverNoMaster` sees a master answering; neither reports anything.
5. The replicas lose their link, reconnect, and offer their old replication id; the restarted
   process has a new one and answers `FULLRESYNC` from an empty dataset. The replicas flush
   and load nothing (**inference**: upstream documents this under "Safety of replication when
   master has persistence turned off" and recommends that such a master not restart
   automatically — which a StatefulSet pod cannot arrange). *(2026-09-27, **measured
   (docker)**, both pins: a replica holding 500 keys kept them for about 5 s after the empty
   master came back — the `repl-diskless-sync-delay 5` of
   [`configmap.go:179`](../../internal/builder/configmap.go) — and held 0 at about 6 s, with a
   new `master_replid` and "Flushing old data" in its log. No guard in Valkey 8.1.9 or 9.1.1.)*

**The three master variants** *(added 2026-09-27)*. What a restarted master boots as depends on
how it got the role:

| Variant | How it got the role | Config on restart | `/data` on restart | Outcome |
|---|---|---|---|---|
| booted with the master config | ordinal-0 fallback on a fresh cluster ([`statefulset.go:342-349`](../../internal/builder/statefulset.go), [`:531-537`](../../internal/builder/statefulset.go)); Sentinel named the booting pod ([`:331-333`](../../internal/builder/statefulset.go), T35 part C); the known-master self-claim ([`:506-507`](../../internal/builder/statefulset.go), [`:528-530`](../../internal/builder/statefulset.go)) | master | empty | the chain as written; step 5 **measured (docker)** |
| promoted by Sentinel | any Sentinel failover, the roll's `SENTINEL FAILOVER` included | master: Sentinel sends `CONFIG REWRITE`, which drops the `replicaof` line from the promoted pod's config file, **measured (docker)**, both pins | the `dump.rdb` of its last full sync | a master with a stale snapshot; the replicas full-resync to it and lose the writes since that sync (**inference**) |
| promoted with `REPLICAOF NO ONE` by the operator or the drain ([`rolling_update.go:4199`](../../internal/controller/rolling_update.go), [`:4607`](../../internal/controller/rolling_update.go), [`valkey_controller.go:2948`](../../internal/controller/valkey_controller.go), [`drain.go:172`](../../internal/sidecar/drain.go); no `CONFIG REWRITE` anywhere in `internal/`) | every non-Sentinel roll (pod-0 via `promotePod0AndRedirect`), a drain, the no-master recovery | replica of the address its init wrote, often its own current replica | stale `dump.rdb` | a different failure: two pods replicating from each other and no master; unmeasured, may keep the data (**inference**). *(Review 2026-09-27, read: if no pod then answers `role:master` and all answer, `checkAndRecoverNoMaster` promotes pod-0 and redirects the others to it ([`valkey_controller.go:2904-2963`](../../internal/controller/valkey_controller.go)); after a non-Sentinel roll pod-0 is the restarted pod itself, so whether the data survives turns on whether pod-0 took a full sync from its peer before that promotion — unmeasured.)* |

**Three operator-side facts that make the chain reachable (read).**

- **The liveness probe restarts a stalled master.** The Valkey container carries an exec
  liveness probe (`ProbeCommand(v)`, period 10 s, timeout 5 s, failure threshold 5,
  [`statefulset.go:859-869`](../../internal/builder/statefulset.go)): a master that does not
  answer `PING` for about 50 s is killed and restarted on the operator's instruction. On a
  liveness kill the kubelet runs the container's `preStop` hook (**inference**), which waits
  for the drain marker ([`statefulset.go:758`](../../internal/builder/statefulset.go)) that
  only a terminating sidecar writes — the sidecar is not terminating, so the hook waits its
  60 s bound first. Not measured. *(corrected 2026-09-27: the hook exists on multi-replica
  non-Sentinel pods only — `drainPreStop` returns nil otherwise,
  [`statefulset.go:746-749`](../../internal/builder/statefulset.go). On a Sentinel cluster a
  stalled master is failed over after the 5 s `down-after`, long before the liveness kill
  (15 s initial delay plus 5 × 10 s), and the restarted pod meets Sentinels that have moved
  on and convert it: the liveness trigger is probably harmless there (**inference**). It is
  live on non-Sentinel clusters.)*
- **`maxmemory` is never set; the memory limit is the backstop.** The generated config carries
  `maxmemory-policy noeviction` and no `maxmemory` ([`configmap.go:129`](../../internal/builder/configmap.go)),
  and the CRD has no field for it. A growing dataset ends at `spec.resources.limits.memory`,
  the kernel kills the container (`OOMKilled`), the kubelet restarts it — empty.
- **Non-persistent is the default.** `spec.persistence.enabled` defaults to `false`
  ([`api/v1/valkey_types.go:991-994`](../../api/v1/valkey_types.go), `PersistenceSpec`). On wds18 all
  eight example clusters are non-persistent (T35, Context).

**Verified (read):** the init's `cp` into the writable config volume; `save ""` for
non-persistent clusters as ADR 0012 states it; the Sentinel `down-after` constant; the two
operator checks that see nothing; the probe parameters and the `preStop` hook on the Valkey
container; the absence of `maxmemory` in the builder and the CRD; the persistence default.

**Not verified:** every step marked inference; the whole chain end to end; whether Valkey 8
or 9 has any guard against an empty master full-resyncing replicas that hold data (none is
known); the `preStop` behaviour on a liveness kill; the boot time of an empty container in
Kind; whether the observer's write test would catch the flush.

**Verified 2026-09-27.** *Read at `4a7543e`:* every line reference above; `checkSteadyStateSplitBrain`
([`steady_state_master.go:151-156`](../../internal/controller/steady_state_master.go)) and
`checkAndRecoverNoMaster` ([`valkey_controller.go:2880`](../../internal/controller/valkey_controller.go))
both run on multi-replica non-Sentinel clusters only
([`valkey_controller.go:421-422`](../../internal/controller/valkey_controller.go)); `valkey-server`
is the container's PID 1 in both command shapes (`exec`,
[`statefulset.go:821-832`](../../internal/builder/statefulset.go)); `/data` is an `emptyDir`
without persistence ([`:578-586`](../../internal/builder/statefulset.go)) and the working
directory ([`:842`](../../internal/builder/statefulset.go)); no `maxmemory`,
`enable-debug-command` or `master-reboot-down-after-period` anywhere in `internal/`, `api/` or
`cmd/`. *Measured (docker), both pins:* the flush of step 5; `dump.rdb` kept after a full
sync and loaded on restart; Sentinel's `CONFIG REWRITE` on promotion;
`sentinel master-reboot-down-after-period` accepted in a config file and by `SENTINEL SET`
(a made-up directive is a fatal config error); `kill -9 1` from inside the container leaves
`valkey-server` running; `DEBUG` refused (`enable-debug-command no`); `CLIENT PAUSE … ALL`
accepted.

**Not verified 2026-09-27:** the chain on Kubernetes end to end; the kubelet's first-restart
latency; the promoted-after-boot outcome on a non-Sentinel cluster; whether the replicas of a
Sentinel-promoted master full-resync or partially resync from its reloaded snapshot; what
`master-reboot-down-after-period` does after a reboot and how fast.

## Impact

- On a non-persistent multi-replica cluster, **one container restart of the master discards the
  dataset on every pod**, while two healthy replicas held it a second earlier. The replication
  the user configured three pods for does not protect against this event. *(narrowed
  2026-09-27, by the variants table: the whole dataset for a master that booted with the
  master config; the writes since its last full sync for a Sentinel-promoted one; a
  different, unmeasured failure for one promoted with `REPLICAOF NO ONE` — by reading, the
  master of every non-Sentinel cluster after its first roll.)*
- The triggers are routine: a memory limit reached on a cache with `noeviction`, a master
  blocked past the liveness budget, any crash of `valkey-server`. Two of the three are this
  operator's own configuration.
- The CR shows nothing: `Ready` stays `True`, the phase `OK`, `status.masterPod` the same pod.
- Persistent clusters are not affected in this shape: the restarted master reloads its RDB or
  AOF. Whether the replicas full-resync from it (a restart-time snapshot, writes since the
  last save lost) is the ordinary persistence trade-off, not this ticket.
- Security: none.

## Options

Candidates, **not costed** — the reproduction below comes first and decides which of them
applies. Each is listed with the question it must answer. *(2026-09-27: costed in the table
below from reading and the docker measurements, on Hans's request to weigh the options; the
Kind reproduction still decides the scope.)*

- **A start guard in the Valkey container.** A wrapper before `valkey-server` that recognises
  "this is a restart of a pod that booted as master and holds no dataset" — a marker in the
  writable config volume written on the first start, ~~plus no `dump.rdb`/`appendonly*` in
  `/data`~~ *(corrected 2026-09-27: not a usable signal — a pod that ever full-synced as a
  replica holds a stale `dump.rdb` and reloads it, measured (docker), and a stale snapshot is a
  loss too; the signal is the marker plus "the config it would boot with has no `replicaof`")*
  — and then does not start as master. What it starts as is the open question: as a
  replica of a peer that answers `role:master` (there is none: the peers are its replicas);
  exiting non-zero so the pod goes not-Ready and Sentinel fails over after 5 s (Sentinel
  clusters only; without Sentinel `checkAndRecoverNoMaster` would have to refuse to promote
  the empty pod — [ADR 0028](../adr/0028-a-demotion-may-not-discard-the-only-dataset.md)'s
  rule applied to a promotion); or as a replica with a `replicaof` no peer answers, serving
  nothing until Sentinel or the operator points it somewhere.
- **`maxmemory` derived from the memory limit** (a fraction of `spec.resources.limits.memory`,
  policy stays `noeviction` or becomes configurable). Removes the OOM trigger, not the others.
  Changes the config hash of every cluster with a limit: a roll on the release.
- **The liveness probe.** Whether a `PING` that stalls 50 s should restart the process at all
  on a non-persistent master, or only report; a restart that loses the dataset is the worse
  outcome of the two.
- **A ~~README~~ sentence** *(corrected 2026-09-27: in
  [docs/operations/persistence.md](../operations/persistence.md) — since
  [ADR 0035](../adr/0035-the-readme-advertises-the-reference-lives-under-docs.md) the README
  explains nothing)* naming the hazard and recommending persistence for any dataset that
  must survive a master restart — owed in every case, as the honest minimum, and no substitute
  for a mechanism the operator itself triggers.
- *(added 2026-09-27)* **`sentinel master-reboot-down-after-period`**, Sentinel clusters only:
  Sentinel treats a master whose run id changed as down for the period and fails over
  (**inference**, upstream). Accepted by both pinned images, **measured (docker)**.

**Weighed (2026-09-27).** One decision: the mitigation. Triggers are OOM, crash and liveness;
variants as in the Fact table.

| Option | Covers | Cost | Rolls on the release | Leaves open |
|---|---|---|---|---|
| **Start guard, Sentinel half** **(recommended)** — on a restart (marker present) of a non-persistent pod whose config has no `replicaof`, ask the Sentinels as the init does ([`statefulset.go:288-316`](../../internal/builder/statefulset.go)); while they name this pod, do not start — nothing answers on the address, so the 5 s `down-after` fails over to a replica that still holds the data; once a peer is named, append `replicaof` and `exec` | all three triggers; the empty and the stale-snapshot variant | S–M: generated shell in the container command ([`:821-832`](../../internal/builder/statefulset.go)), a builder unit test, `RequiredImageTools` if a tool is new, `make test-image-tools`, an e2e on both Valkey lines | every Sentinel-enabled data tier once (pod-spec hash) *(review 2026-09-27: every non-persistent one only, if the guard is rendered only without persistence)* | the wait must end below the liveness budget (15 s + 5 × 10 s) or exit non-zero and re-ask on the next start. *(Review 2026-09-27:)* a tier where Sentinel cannot fail over — no replica eligible yet (a fresh cluster whose replicas never connected), no Sentinel quorum, a single data pod — keeps naming this pod forever, so an unbounded guard turns a data-loss risk into a permanent outage; it needs a bound after which it starts anyway (today's behaviour), and that bound is part of the design the reproduction informs |
| **Start guard, non-Sentinel half** | all three triggers; the booted-as-master variant | L: the guard cannot self-claim, and nothing then promotes — `checkAndRecoverNoMaster` does nothing while a pod is unreachable ([`valkey_controller.go:2904`](../../internal/controller/valkey_controller.go)) and would promote pod-0 otherwise — so it needs an operator path that promotes the replica holding the data and records it (ADR 0008, 0009, 0028 bind it) | every multi-replica non-Sentinel data tier once | the promoted-after-boot variant, which the reproduction must measure first |
| **`master-reboot-down-after-period`** | all three triggers, both variants — only if it fails over before the flush | XS: one line next to [`sentinel.go:157-159`](../../internal/builder/sentinel.go) | every Sentinel tier once (the line is in the Sentinel config hash, [`:93`](../../internal/builder/sentinel.go)) | races the flush, about 5 s after the restart (measured (docker)); Sentinel learns a new run id from `INFO`, every 10 s by upstream default (**inference**), so it likely fires too late unless `repl-diskless-sync-delay` rises, which slows every full sync. Nothing for non-Sentinel |
| **`maxmemory` from the memory limit** | OOM only | S–M: a fraction of `spec.resources.limits.memory` rendered into [`configmap.go:126-135`](../../internal/builder/configmap.go) | every data tier with a memory limit (config hash) | crash and liveness; turns an OOM kill into refused writes under `noeviction` — a behaviour change a cache user may want anyway, combinable with any row |
| **Liveness change** (no liveness probe on non-persistent multi-replica non-Sentinel data pods) | liveness only, the one trigger live on non-Sentinel only | S | every non-Sentinel multi-replica data tier once | OOM and crash; a hung master is never restarted |
| **The docs sentence only** | nothing | XS | nothing | the mechanism |

Why the start guard, Sentinel half first: it is the only option that removes the mechanism —
a restarted master answering with a dataset older than its replicas' — instead of one of its
three triggers, and it covers the empty and the stale-snapshot variant alike. It waits on the
arbiter the tier already has, and the replicas keep their data exactly as long as no empty
master answers them (the flush needs the empty master to answer, measured (docker)), so it does
not race the 5 s delay the way `master-reboot-down-after-period` does. `maxmemory` and the
liveness change each remove one trigger of three. The non-Sentinel half follows the
reproduction: it needs a new promotion path (L), and whether its common variant loses data at
all is unmeasured. `master-reboot-down-after-period` is run in the same Kind session; if it
fails over before the flush on both Valkey lines, it replaces the Sentinel half at XS.

## Decision

**Open.** First step, decided with the filing: reproduce on Kind before costing anything.
*(2026-09-27: costed above without the reproduction, on Hans's request; still not decided.
The reproduction runs in one Kind session with T35 decision 3.)*

## Work list *(added 2026-09-27)*

- **XS, needs no decision, can land today:** the hazard sentence in
  [docs/operations/persistence.md](../operations/persistence.md) (last Verification item).
  **Done 2026-09-27** (History).
- **Waits on the Kind reproduction, then on the decision:** the mitigation, its ADR, its tests.

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
  realistic trigger; on the Sentinel cluster, a second pass with
  `sentinel master-reboot-down-after-period` set. The Sentinel master kill shares the session
  with T35 decision 3.)*
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
  lines.)*

## History

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
