---
id: T36
title: a non-persistent master that crash-restarts comes back empty and flushes its replicas
state: filed          # from reading; the Kind reproduction is the first step
severity: high        # estimate until measured - a replicated dataset lost to one container restart
security: none
urgency: next         # severity >= medium and the trigger is reachable in released code (rule 3)
effort: M
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
ticket was measured on a cluster yet.

## Fact

**The chain (read, with two inferences).**

1. Init containers run once per pod, never on a container restart (Kubernetes semantics,
   **inference** in the sense that it is not this repository's code). The data init writes the
   configuration it chose into the pod's writable config volume
   ([`statefulset.go:332`](../../internal/builder/statefulset.go), `cp` of the master or the
   replica config), an `emptyDir` that outlives a container restart.
2. A `valkey-server` container the kubelet restarts therefore starts with the configuration of
   its first start — the **master** config if it was master — and, on a non-persistent
   cluster, with **no dataset** (`save ""`, [ADR 0012 D10](../adr/0012-the-sidecar-records-its-drain-promotion-on-the-pod.md)).
   Same pod, same address, same hostname.
3. **With Sentinel:** `down-after-milliseconds` is 5000 ([`sentinel.go:47`](../../internal/builder/sentinel.go)).
   The first restart of a crashed container is immediate and an empty Valkey boots in well under
   a second (**inference**), so Sentinel never marks the master `s_down`, performs no failover,
   and keeps the same address as master.
4. **Without Sentinel:** nothing arbitrates. `checkSteadyStateSplitBrain` sees one labelled
   master and `checkAndRecoverNoMaster` sees a master answering; neither reports anything.
5. The replicas lose their link, reconnect, and offer their old replication id; the restarted
   process has a new one and answers `FULLRESYNC` from an empty dataset. The replicas flush
   and load nothing (**inference**: upstream documents this under "Safety of replication when
   master has persistence turned off" and recommends that such a master not restart
   automatically — which a StatefulSet pod cannot arrange).

**Three operator-side facts that make the chain reachable (read).**

- **The liveness probe restarts a stalled master.** The Valkey container carries an exec
  liveness probe (`ProbeCommand(v)`, period 10 s, timeout 5 s, failure threshold 5,
  [`statefulset.go:859-869`](../../internal/builder/statefulset.go)): a master that does not
  answer `PING` for about 50 s is killed and restarted on the operator's instruction. On a
  liveness kill the kubelet runs the container's `preStop` hook (**inference**), which waits
  for the drain marker ([`statefulset.go:758`](../../internal/builder/statefulset.go)) that
  only a terminating sidecar writes — the sidecar is not terminating, so the hook waits its
  60 s bound first. Not measured.
- **`maxmemory` is never set; the memory limit is the backstop.** The generated config carries
  `maxmemory-policy noeviction` and no `maxmemory` ([`configmap.go:129`](../../internal/builder/configmap.go)),
  and the CRD has no field for it. A growing dataset ends at `spec.resources.limits.memory`,
  the kernel kills the container (`OOMKilled`), the kubelet restarts it — empty.
- **Non-persistent is the default.** `spec.persistence.enabled` defaults to `false`
  ([`api/v1/valkey_types.go`](../../api/v1/valkey_types.go), `PersistenceSpec`). On wds18 all
  eight example clusters are non-persistent (T35, Context).

**Verified (read):** the init's `cp` into the writable config volume; `save ""` for
non-persistent clusters as ADR 0012 states it; the Sentinel `down-after` constant; the two
operator checks that see nothing; the probe parameters and the `preStop` hook on the Valkey
container; the absence of `maxmemory` in the builder and the CRD; the persistence default.

**Not verified:** every step marked inference; the whole chain end to end; whether Valkey 8
or 9 has any guard against an empty master full-resyncing replicas that hold data (none is
known); the `preStop` behaviour on a liveness kill; the boot time of an empty container in
Kind; whether the observer's write test would catch the flush.

## Impact

- On a non-persistent multi-replica cluster, **one container restart of the master discards the
  dataset on every pod**, while two healthy replicas held it a second earlier. The replication
  the user configured three pods for does not protect against this event.
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
applies. Each is listed with the question it must answer.

- **A start guard in the Valkey container.** A wrapper before `valkey-server` that recognises
  "this is a restart of a pod that booted as master and holds no dataset" — a marker in the
  writable config volume written on the first start, plus no `dump.rdb`/`appendonly*` in
  `/data` — and then does not start as master. What it starts as is the open question: as a
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
- **A README sentence** naming the hazard and recommending persistence for any dataset that
  must survive a master restart — owed in every case, as the honest minimum, and no substitute
  for a mechanism the operator itself triggers.

## Decision

**Open.** First step, decided with the filing: reproduce on Kind before costing anything.

## Verification

- [ ] **Reproduction (first).** A 3-replica non-persistent cluster on Kind, with and without
  Sentinel. Write keys until `DBSIZE` is stable on the replicas; `kill -9` the `valkey-server`
  process inside the master container (a crash, no `preStop`); read `DBSIZE` on both replicas
  and `INFO replication` on all three every second for a minute. Record: the restart time of
  the container, whether Sentinel logged `+sdown`, the replicas' `master_replid` before and
  after, and the final `DBSIZE`. Then the same with the liveness path: block the master (a
  `DEBUG SLEEP 70`) and watch the kubelet restart it. That measures the severity this
  ticket estimates.
- [ ] **The mitigation**, once chosen, with the revert check ADR 0017 asks for, and the same
  reproduction turned into an e2e that asserts the replicas keep their `DBSIZE` across a
  master container restart.
- [ ] The README sentence.

## History

- 2026-09-27 — renamed to `036-non-persistent-master-restarts-empty.md` (was `local_T36-non-persistent-master-restarts-empty.md`) when the tickets were numbered.
- 2026-09-27 — **filed** as its own ticket from T35 decision 7, out of the reading done while
  costing T35 option C3 (the "fresh pod" heuristic raised the question what a *restarted*
  container boots as). Severity high is an estimate from reading, marked so in the
  frontmatter; urgency `next` by derivation rule 3 (severity ≥ medium and the trigger
  reachable in released code). Hans's proposal on the same day had been a board row with the
  file created when work starts; his answer retired the board and made the file the record.
