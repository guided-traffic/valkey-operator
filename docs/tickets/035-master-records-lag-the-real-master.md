---
id: T35
title: records of who the master is lag the real master after a handover (seen on wds18 after the v1.13.0 upgrade)
state: decided
severity: low
security: none
urgency: later
effort: M
filed-from: check of the v1.13.0 upgrade on wds18-k8s-main, namespace database-examples, 2026-09-26
opened: 2026-09-26
decided: 2026-09-27
done:
---

Filed as its own file on Hans's explicit request (2026-09-26), although it is below the filing
bar: severity low, security none. By the template it would be a board row only. History
records this. *(2026-09-27: that clause of the template is withdrawn on Hans's instruction —
every finding gets a file, new or appended to its family ticket; the board is retired. See
History.)*

Each claim carries a label:

- **run** means observed on the cluster or in a log of it. The time of the read is given where
  it matters.
- **read** means read in the tree at `f5c6886` (the `v1.13.0` code, commit `ad81a47` being the
  merge of it), or in the controller-runtime module cache.
- **inference** means neither.

Logs and reads were taken between 20:55 and 21:13 UTC. The operator log is saved in the
session scratchpad as `wds18-op.log` (untracked).

## Context: the upgrade itself went as intended

- **Upgrade (run).** Flux upgraded the HelmRelease to chart 1.13.0 at 20:44:22Z (Helm revision
  96). The operator pod started at 20:44:33Z, commit `ad81a47`, 0 restarts.
- **Cluster (run).**
  - 8 Valkey CRs, 3 replicas each, none with persistence.
  - 4 with Sentinel (3 Sentinels each), 4 with TLS.
  - Metrics and observers are enabled.
  - Chaos Mesh Schedule `valkey-chaos` runs `pod-kill`, mode `one`, every 5 min, on
    `app.kubernetes.io/managed-by=vko.gtrfc.com`.
- **Rolls (run).**
  - Data tiers rolled between 20:45:06 and 20:45:52, Sentinel tiers by 20:47:13.
  - Exactly one `RollingUpdateComplete` per CR and one `SentinelUpdateComplete` per Sentinel
    CR.
  - No Warning Event on any Valkey object.
  - Four operator errors, all 409 conflicts, retried by the next pass.
- **Posture (run).**
  - Pod users:
    - every data and Sentinel pod runs as uid/gid/fsGroup 999;
    - every observer runs as 65532.
  - Container fields: every container has `RuntimeDefault`, `allowPrivilegeEscalation: false`,
    a read-only root, `drop: [ALL]` and `privileged: false`.
  - A server-side dry-run of `pod-security.kubernetes.io/enforce=restricted` on the namespace
    returned no warning.
  - No pod differs from its template hash.
  - No template carries `fix-data-ownership`. None is persistent, so no second roll was due.
- **T32's path live (run).** The roll replaced the unavailable `valkey9-2` instead of waiting
  for it: "Deleting replica pod valkey9-2 for rolling update (youngest-first; the pod was not
  available)", 20:45:06.
- **Replication (run, ~20:57).** Every cluster had one master, two replicas with
  `master_link_status:up` and one `master_replid`. The labels matched the real roles.
  `DBSIZE` was 0 everywhere, so data preservation cannot be judged on these examples.

The three observations below are not caused by v1.13.0. Every mechanism involved predates it.

## Fact

### A — `status.masterPod` of `valkey9` names a replica, and nothing corrects it

**What happened (run).**

| Time (UTC) | Source | Event |
|---|---|---|
| 20:45:33.9 | operator Event | `ManualFailover`: promoted `valkey9-1` to temporary master, deleting old master `valkey9-0` |
| 20:45:34 | sidecar `valkey9-1` | labeler `replica → master` |
| 20:45:35 / 20:45:41 | pod | new `valkey9-0` created / Ready |
| 20:45:41 | operator log | "Configured pod-0 as replica of promoted pod", state `restoring-topology` |
| 20:45:43 | operator log | "Promoted pod-0 back to master", both replicas redirected, `verifying-topology`, "Multi-replica rolling update completed, topology restored" |
| 20:45:43 | operator log, same pass | "Sole master-labeled pod does not report master role; the label is stale", pod `valkey9-1`, role `slave` |
| 20:45:43 | sidecar `valkey9-1` | labeler `master → replica` |
| 20:45:44 | sidecar `valkey9-0` | labeler `replica → master` |
| after 20:45:43 | operator log | **no line for `valkey9`** up to the save at ~21:12 |
| 21:12:45 | CR | `status.masterPod: valkey9-1`; the real master is `valkey9-0` (INFO replication, ~20:57) |

**Mechanism (read).**

1. **The status names the labelled pod.** The completing pass writes the status after its
   steady-state check.
   - `currentMasterPod` ([`valkey_controller.go:2323-2341`](../../internal/controller/valkey_controller.go))
     answers with the pod that carries the `instanceRole=master` label, when exactly one does.
     It does so before the known-master record.
   - *(Added 2026-09-26, read.)* That is the **non-Sentinel arm** of `updateStatus` only
     ([`:2252`](../../internal/controller/valkey_controller.go)). The Sentinel arm writes
     `clusterState.MasterPod` ([`:2476`, `:2489`](../../internal/controller/valkey_controller.go)),
     the pod the health checker found answering `role:master`
     ([`checker.go:115`](../../internal/health/checker.go)), and never reads the label. One
     field, two meanings — which is why B's status was right while A's was wrong.
   - That is its documented rule (the comment at `:2300-2322`): the label is the `-rw`
     selector, so it names the pod that receives writes.
   - At 20:45:43 that was still `valkey9-1`. The sidecar polls every 1 s and had not
     relabelled yet.
2. **The pass saw the stale label and asked for nothing.** The branch at
   [`steady_state_master.go:261-267`](../../internal/controller/steady_state_master.go) logs
   "the label is stale" and returns.
3. **Nothing re-enters Reconcile afterwards.**
   - There is no Pod watch (the `Owns` list, `valkey_controller.go:2987-2994`), and a pod label
     patch does not change the StatefulSet.
   - The CR watch carries `GenerationChangedPredicate`, and the healthy path returns no
     requeue (`:375-392`).
   - `valkey9-0` was already Ready at 20:45:41, so no later StatefulSet status change came.
   - The cache resync is controller-runtime's default of 10 h
     (`sigs.k8s.io/controller-runtime@v0.25.1/pkg/cache/cache.go:45`). `cmd/main.go` sets no
     `SyncPeriod`.
   - So the field stays wrong until the next StatefulSet event, spec change or resync.

**A second instance (inference).**
- `valkey9-tls` logged the same line at 20:45:39: pod `valkey9-tls-2`, role `slave`.
- Its next logged pass was 20:55:01, after a chaos kill of `valkey9-tls-1`, and its status now
  names `valkey9-tls-0` correctly.
- That the status named `valkey9-tls-2` in between follows from the same code path. It was
  not observed.

**Not this shape (run).**
- `valkey8` and `valkey8-tls` completed while their sole labelled pod was terminating ("Sole
  master-labeled pod is terminating; refusing to adopt a dying authority").
- They completed before the old master's replacement existed, so later StatefulSet events
  re-entered them. Their status is correct now.
- What they carried in between is not recorded.

**Also in the window (inference, from the table).** The `-rw` Service selected the replica
`valkey9-1` from the promotion of pod-0 until the relabel, at most about 1 s. That is inherent
in the label handover and outside this ticket.

**Origin (read).** `currentMasterPod` in this form came with `744b589` (2026-08-21, first
released in v1.11.0).

### B — the `known-master` annotation of `valkey8-sentinal-tls` names a replica

**What happened (run).**

- **After the roll: `-1` is master.** The roll's Sentinel failover made
  `valkey8-sentinal-tls-1` master. The operator log reads "New master verified with data",
  `newMaster: valkey8-sentinal-tls-1`, 20:45:46. The annotation named `-1`.
- **20:50:00: chaos kills `-1`.** Chaos Mesh killed `-1`, and Sentinel failed over.
  - `sentinel-0`: `+try-failover` 20:50:00.453, `+promoted-slave valkey8-sentinal-tls-0`
    20:50:01.628.
  - `+switch-master` on `sentinel-1`/`-2` at 20:50:01.645/.647 and on `sentinel-0` at
    20:50:02.777.
  - The log shows no `+sdown`/`+odown` in front of `+try-failover`. That fits a forced
    `SENTINEL FAILOVER` from the drain handler on SIGTERM (`internal/sidecar/drain.go`)
    (inference).
- **Afterwards: the annotation and the Sentinel ConfigMap still name `-1`.** The annotation
  still reads `valkey8-sentinal-tls-1` (read ~21:00). The monitor line of the
  `valkey8-sentinal-tls-sentinel-config` ConfigMap reads
  `sentinel monitor valkey8-sentinal-tls valkey8-sentinal-tls-1.… 16379 2`. The real master
  is `-0`, which the CR status reports correctly.

**Mechanism (read).**

- **Only operator promotions write the annotation.** On the Sentinel path,
  `syncSentinelWithMaster` persists the master at roll finalization
  ([ADR 0008](../adr/0008-known-master-annotation-is-the-recorded-authority.md) D3,
  `persistKnownMaster` at
  [`rolling_update.go:1044`](../../internal/controller/rolling_update.go)).
  - This write is deliberately best-effort. The comment at `:1033-1043` says why: Sentinel is
    the authority there, the annotation only pre-seeds a restarting Sentinel, and
    `checkSteadyStateSplitBrain` never runs for Sentinel clusters.
  - A Sentinel failover outside a roll writes nothing.
- **Where it is read.**
  - The Sentinel ConfigMap takes the annotation as its monitor target
    ([`sentinel.go:103-107`](../../internal/builder/sentinel.go)). It is excluded from the
    config hash (`GenerateSentinelConfForHash`), so the stale value rolls nothing.
  - A data pod's init falls back to the replica ConfigMap's `replicaof` only when no
    Sentinel answers within 30 s
    ([`statefulset.go:318-326`](../../internal/builder/statefulset.go)).
- **A booting Sentinel corrects the target itself.** Its init checks `ROLE` of the configured
  master. When that is not `master`, it scans the data pods and rewrites the monitor line
  ([`sentinel.go:654-692`](../../internal/builder/sentinel.go)). ADR 0008 places the record
  below peer discovery on purpose ("a stale record can never displace", `:96`).

**Not observed:** no Sentinel pod of this cluster restarted after 20:50. They date from
20:46:43 to 20:47:00.

### C — the killed master's replacement booted as a second master for about 5–10 s

Same event as B, `valkey8-sentinal-tls`.

**What happened (run).**

- 20:50:00: the operator logged "Could not find master via INFO replication" (no master found
  among 3 pods), phase `Error`.
- 20:50:07: `internal/health/checker.go:247` logged "WARNING: Multiple masters detected
  (split-brain)", candidates `valkey8-sentinal-tls-0` and `valkey8-sentinal-tls-1`.
- The `-1` of that moment was the replacement created at 20:50:00.
- 20:50:11.746: `sentinel-2` logged `+convert-to-slave valkey8-sentinal-tls-1`.
- 20:50:12: `Ready=True/HAClusterReady`.
- The direction was right: the empty pod was converted, and `-0` kept its dataset.
- **The replacement's init and sidecar logs are lost.** Chaos killed that `-1` again at
  21:10:00.

**Mechanism (inference, resting on read code).**

- The data init asks the Sentinels one after another, and the first answer wins
  ([`statefulset.go:288-316`](../../internal/builder/statefulset.go)). If the answer names
  the pod itself, it boots with the master config (`:330-333`).
- The 21:10 replacement ran its init within 1 s of its creation (run: created 21:10:00, init
  21:10:01).
- At 20:50:01, `sentinel-0` had not switched yet (its `+switch-master` came at 20:50:02.777).
  So `sentinel-0` still named `-1`, and the fresh, empty `-1` booted as master.

**Why `-rw` was probably safe (inference).**
- The sidecar labeler of a Sentinel cluster labels `master` only when Sentinel agrees
  ([`labeler.go:89-92`](../../internal/sidecar/labeler.go)). By 20:50:02.8 every Sentinel
  named `-0`.
- So `-1` was probably never labelled master, and `-rw` never selected it. The sidecar log
  that would prove this is lost.

**The CR does not show it (read).**
- `MultipleMasters` stayed `False` (lastTransitionTime 20:45:46).
- The condition's one evaluator is the rolling-update resolver (`condition_registry.go:140`,
  `split_brain_report.go`). The health checker only logs.
- The window was far below the 90 s Warning threshold of ADR 0025 either way.

**Hypothesis, not observed.**
- A client that discovers the master through `sentinel-0` before 20:50:02.777 could have
  connected to the empty `-1` and written there.
- `+convert-to-slave` discards such writes.
- Nothing but the observer writes to these examples, and the observer log of this cluster is
  lost with its pod (replaced at 20:55:00).

**Origin (read).** The Sentinel query block of the data init (`statefulset.go:280-350`) is
unchanged between `v1.12.8` and `v1.13.0`: `git diff -U0` has no hunk there, only at
`:214` and `:622`.

**Verified:**

- **run:** every timeline row above, from the operator log, the Sentinel logs, the sidecar
  logs of `valkey9-0`/`-1`, the pod timestamps, the CR status and annotations, and the
  Sentinel ConfigMap.
- **read:**
  - `currentMasterPod` and its documented rule;
  - the stale-label branch;
  - the watches, the predicate and the healthy-path return;
  - the 10 h resync default;
  - the known-master writers and readers on the Sentinel path;
  - both init scripts;
  - the labeler's Sentinel cross-check;
  - the single `MultipleMasters` evaluator;
  - the unchanged init between v1.12.8 and v1.13.0.

**Not verified:**

- The `status.masterPod` values of `valkey9-tls`, `valkey8` and `valkey8-tls` between their
  completion and their next pass.
- What the fresh `-1` of C did at boot: which Sentinel its init asked, what it got, and
  whether its sidecar ever labelled it master. The logs are lost.
- Whether any client wrote to the empty `-1` of C.
- Whether the stale annotation of B ever reaches a data pod through the fallback. That needs
  every Sentinel silent for 30 s.
- Whether a new Sentinel under chaos corrects B's monitor line as the init code says. No
  Sentinel restarted there.
- Behaviour on persistent clusters and on the production namespaces. They were not looked
  at.

## Impact

- **A: a status field is wrong, for up to the resync period.**
  - Lens and `kubectl get valkey` show the wrong master: 27+ min measured on `valkey9`, up to
    10 h by the resync default.
  - Nothing in the operator reads `status.masterPod` (read: no reader outside assignments).
    Anyone who reads the MASTER column to act (connect, debug, fail over by hand) acts on a
    replica.
  - It happens after any non-Sentinel roll whose last StatefulSet event precedes the sidecar
    relabel, which is the ordinary case when pod-0 is Ready before the restore completes.
- **B: a record names a replica.** It is cosmetic under the documented design. The
  annotation and the Sentinel monitor line name a replica, the consumers validate before use,
  and the fallback path is reached only when no Sentinel answers for 30 s.
- **C: a second, empty master answers for about 5–10 s** after a master is killed, until
  Sentinel converts it.
  - Measured once, direction correct, no data at risk on these examples.
  - The residual risk is a Sentinel-discovering client that writes to the empty pod inside
    the switch window (hypothesis). The operator's CR shows none of it.
- **Security: none.** No principal gains anything, and no guard is weakened.

## Options

*Written 2026-09-26 on Hans's explicit request ("refine this ticket with solution
approaches"), although the template allows an `## Options` section only above the filing
bar. It is the same exception as the filing itself, and History records it.*

Every claim below is **read** in the tree at `f5c6886` unless labelled **run** (the wds18
log of the Fact section) or **inference**.

### The shape of the problem: six records, three writers, three clocks

| Record | Writer | Reader | Moves |
|---|---|---|---|
| `instanceRole` pod label | the pod's own sidecar labeler, a 1 s poll of `INFO replication` that patches on change ([`labeler.go:100-148`](../../internal/sidecar/labeler.go)); the drain handler writes `draining` on the dying master ([`drain.go:121`](../../internal/sidecar/drain.go)) | the `-rw`/`-r` selectors; `listMasterLabeledPods`; `currentMasterPod` rule 1 | ≤ 1 s after the role change, if the sidecar is alive |
| `status.masterPod` | the operator, in `updateStatus`: **from the label** on the non-Sentinel arm ([`valkey_controller.go:2252`](../../internal/controller/valkey_controller.go)), **from INFO** on the Sentinel arm ([`:2476`, `:2489`](../../internal/controller/valkey_controller.go)) | humans, Lens, `kubectl get valkey`; nothing in the operator | at the next pass — which after a completed roll may be the 10 h resync (A) |
| `vko.gtrfc.com/known-master` (CR annotation) | the operator, on the promotions it performs (ADR 0008 D3); best-effort at Sentinel roll finalization ([`rolling_update.go:1039-1047`](../../internal/controller/rolling_update.go)) | replica ConfigMap `replicaof`; Sentinel ConfigMap monitor line ([`sentinel.go:103-107`](../../internal/builder/sentinel.go)); the non-Sentinel resolvers | on the next operator promotion; never on a Sentinel failover outside a roll (B) |
| Sentinel's master table | Sentinel | labeler cross-check ([`labeler.go:135-142`](../../internal/sidecar/labeler.go)); data init Phase 1; drain; the roll's authority (`getSentinelMasterPodName`) | 1–3 s per failover, per Sentinel (**run**: the leader switched 1.1 s after the other two) |
| replica ConfigMap `replicaof` | the operator, from the annotation | data init Phase 2: the record on non-Sentinel clusters, the fallback after 30 s of Sentinel silence otherwise ([`statefulset.go:322`](../../internal/builder/statefulset.go)) | with the annotation |
| Sentinel ConfigMap monitor line | the operator, from the annotation | Sentinel init, validated by `ROLE` and a pod scan ([`sentinel.go:672-692`](../../internal/builder/sentinel.go)) | with the annotation |

Two consequences the Fact section did not draw:

- **`status.masterPod` has two meanings.** On the non-Sentinel arm it is "the pod the `-rw`
  selector names" — the documented rule of `currentMasterPod`. On the Sentinel arm it is
  "the pod that answers `role:master`", and the label is never read. That is why B's status
  was right while A's was wrong, and it is a precision defect in its own right: a field a
  human reads must not change its meaning with `spec.sentinel.enabled`.
- **The operator learns of a role change only through a side effect.** A pod's death moves
  the StatefulSet status and `Owns(StatefulSet)` — no predicate — re-enters the pass. A
  relabel alone moves nothing the operator watches. Four role changes have no side effect:
  the relabel that closes a topology restoration (A), a human's `SENTINEL FAILOVER`, a
  human's `REPLICAOF`, and the labeler's cross-check flip on a Sentinel cluster. All four
  leave `status.masterPod` to the resync.

### The rule the options serve

> A role change reaches the operator as an event, and every record the operator owns is
> rewritten in the pass that follows. Where the operator causes the change, it records it in
> the same pass. Where a pod decides its role at boot, it decides on a settled majority,
> never on the first voice.

Each option is one instance of that rule, costed on its own. None of them makes the
operator a writer of the `instanceRole` label (ADR 0012 D1, D12); the one that would is
listed under "not proposed", with its price.

### A — `status.masterPod` after a non-Sentinel roll

**A1 — the pass that proves the label in flux asks for the pass that sees it settled.**
The filing's proposal, now checked. `adoptUnrecordedPromotion` has two branches that
*prove* the sole label mid-transition — "terminating"
([`steady_state_master.go:250`](../../internal/controller/steady_state_master.go)) and "the
label is stale" ([`:264`](../../internal/controller/steady_state_master.go)) — and both
return without a recheck. Each calls `requestRecheck(ctx, d)`.

- **The recheck is honoured on this path.** `applyRecheck` folds it into the result at
  [`valkey_controller.go:310`](../../internal/controller/valkey_controller.go) on the
  error-free return, and `recordSentinelPeerDrift`
  ([`:2396`](../../internal/controller/valkey_controller.go)) already requests one from the
  same status arm. A blocked pass drops it and returns an error instead, which is its own
  retry. The filing's open question is closed.
- **`d` must exceed the sidecar poll** (1 s), nothing more. `rollingUpdateRequeueDelay`
  (10 s, [`rolling_update.go:203`](../../internal/controller/rolling_update.go)) does; a
  dedicated constant of about 3 s makes the status converge sooner. ADR 0011 D14's 15 s is
  not owed here: it protects a *demotion verdict* from a label set that has not caught up,
  and this recheck only re-reads the label and rewrites a status field.
- Closes: A's observed shape on every multi-replica non-Sentinel cluster — the only topology
  the check runs on ([`:154`](../../internal/controller/steady_state_master.go)).
- Leaves: nothing on that topology while the sidecar lives. A dead sidecar never relabels,
  so nothing settles; that is A3's case.
- Touches: ADR 0011 D12's last sentence ("a merely stale role label schedules nothing,
  because the operator does not repatch labels it does not own",
  [`:210-216`](../adr/0011-evidence-based-steady-state-split-brain-resolution.md)) and the
  Alternatives entry "Requeue uniformly … on a stale label the operator has no fix to poll
  for" ([`:421`](../adr/0011-evidence-based-steady-state-split-brain-resolution.md)). Both
  are right about the label and blind to the status field: the operator has no fix for the
  label, and it has a record of its own that it derived from the label. The recheck polls
  for that record. One amended sentence, the rejection kept for what it was about.
- Cost: XS. Two calls, two unit tests, one ADR sentence; one extra pass per handover.

**A2 — rule 1 yields to rule 2 in the pass that proved it stale.** `adoptUnrecordedPromotion`
already holds the answer that makes the label stale (`info.Role != master`,
[`:261-267`](../../internal/controller/steady_state_master.go)). It records the pod name on
the pass state (`passState`,
[`foreign_object.go:89-130`](../../internal/controller/foreign_object.go) — the carrier the
recheck already rides), and `currentMasterPod`
([`valkey_controller.go:2323-2341`](../../internal/controller/valkey_controller.go)) skips
rule 1 when its one labelled pod is that name, falling to the annotation, which after
`promotePod0AndRedirect` names pod-0 (ADR 0008 D3). The status is then right in the pass
that completes the roll, and A1's recheck confirms the relabel.

- Closes: A1's shape, one pass earlier (**run**: at 20:45:43 instead of no earlier than
  20:45:53).
- Leaves: a label and a record that are both stale on the same pod. The function returns
  before probing when they agree
  ([`:237`](../../internal/controller/steady_state_master.go)), so no verdict exists. That
  needs a dead sidecar — A3.
- Semantic consequence, to be decided with it: for the ≤ 1 s of flux the non-Sentinel arm
  then reports *the master* rather than *the pod the `-rw` selector names* — which is what
  the Sentinel arm has always reported. One meaning for the field on both topologies; the
  routing lag becomes a condition's business (A3), not the field's.
- Cost: XS on top of A1. One field, three lines, two tests; the `currentMasterPod` comment
  rewritten.

**A3 — a sole master label on a pod that is not master is reported.** Today it is one log
line while `Ready` reads `True`, the phase `OK`, `status.masterPod` the replica, and every
write to `-rw` is answered `READONLY`. The `RWServiceEmpty` level
([`rw_service_report.go:37`](../../internal/controller/rw_service_report.go); one evaluator,
settled clusters only, presence-guarded) reports the *empty* selection; a *misrouted* one is
the same outage with one endpoint. Mechanism: a second `True` reason on that level
(`LabeledPodIsNotMaster`), judged from an answer the pass already holds — A2's pass-state
verdict on the non-Sentinel arm, `clusterState.MasterPod` against the labelled pod on the
Sentinel arm — so no new connection. The brief `True` inside every handover's ≤ 1 s is the
flicker the level already accepts for the empty case (its own comment, and ADR 0025's
`MultipleMasters` precedent); if that flicker is unwanted, ADR 0025's deadline pattern
applies, with the condition's `LastTransitionTime` as the clock and A1's recheck as the
observer.

- Closes: the invisible outage of a dead or stuck sidecar on the master — the T21 shape
  with one live label instead of none.
- Touches: ADR 0012 D12
  ([`:407`](../adr/0012-the-sidecar-records-its-drain-promotion-on-the-pod.md), which
  describes the level), the README condition row, and the registry row
  ([`condition_registry.go:236-244`](../../internal/controller/condition_registry.go)) —
  the same row with a new reason, or a new level if "Empty" may not mean "misrouted"; ADR
  0027 decides that by the name, not by the mechanism.
- Cost: S. Not needed for the measured incident; it is the piece that makes the class
  visible on the CR.

**A4 — a Pod watch on `instanceRole` changes.** `Watches(&corev1.Pod{},
EnqueueRequestsFromMapFunc(findValkeyForPod), WithPredicates(instanceRoleChanged))` next to
the Secret watch ([`valkey_controller.go:2996`](../../internal/controller/valkey_controller.go)):
Update events only, fired when the label's value differs between the old and the new object,
mapped to the CR by the `vko.gtrfc.com/cluster` label
([`labels.go:24-25`](../../internal/common/labels.go)) and the namespace. The Pod informer
already runs cluster-wide — every pass lists pods through the cached client
([`steady_state_master.go:195-199`](../../internal/controller/steady_state_master.go), T33
row A2), and `managerOptions` restricts nothing
([`cmd/main.go:102-110`](../../cmd/main.go)) — so the watch adds an event handler and no
memory. Every relabel then re-enters the pass inside the API round trip.

- Closes: the class. A converges within one poll plus one pass on both topologies; B2 gets
  its trigger for the failovers that kill no pod; the four side-effect-free role changes
  above reach the status.
- Costs, each named:
  - **Nine tracked sentences state "there is no Pod watch" as a premise**:
    [ADR 0001:46](../adr/0001-continue-reconciling-past-a-rejected-write.md),
    [ADR 0002:214](../adr/0002-surface-a-blocked-reconcile-on-the-cr.md),
    [ADR 0011 D13 and D21](../adr/0011-evidence-based-steady-state-split-brain-resolution.md),
    [ADR 0031:185](../adr/0031-a-record-the-operator-trusts-lives-in-pod-spec.md),
    [ADR 0032:316](../adr/0032-generated-pods-run-rootless.md), `CLAUDE.md:834`,
    `SECURITY_ARCHITECTURE.md:1010` (since 2026-09-27
    [`docs/security/rootless-migration.md:69`](../security/rootless-migration.md#fix-data-ownership-the-one-root-process)), [T31:565](archive/031-generated-pods-run-as-root.md) and
    this ticket's Fact. D21 rejected the watch "as unnecessary once the evidence became
    durable" ([`:417`](../adr/0011-evidence-based-steady-state-split-brain-resolution.md)).
    That reasoning stands — A4 adds no evidence and changes no evidence rule — and it never
    weighed the timeliness of the operator's own records, so the amendment adds a
    requirement rather than overturning one. The other eight need one clause each. That
    sweep is most of the effort.
  - **Pass volume**: one pass per relabel. `Owns(StatefulSet)` carries no predicate, so every
    readiness flip already re-enters, and every role change that kills a pod comes with one;
    the wds18 chaos schedule adds about two relabels per kill. ADR 0019 D3 is untouched: no
    fleet-wide state.
  - **Security, `hardening` class, not `boundary`**: the map function trusts a label, so
    whoever can patch pod labels in a namespace can enqueue passes for a CR of that
    namespace — and the sidecar's own `resourceNames`-bound `patch` grant
    ([`rbac.go:72`](../../internal/builder/rbac.go)) already can, on the very label the
    predicate watches. The pass it triggers writes nothing it would not write anyway: every
    write is ownership-proven (ADR 0020), the work queue deduplicates per CR, the rate
    limiter backs off. ADR 0031:185 calls the watch "one this operator deliberately does not
    have" inside an argument about a `pods/status` grant; the clause carries no reason of its
    own, and no grant is proposed here.
  - **No loop**: the operator never writes `instanceRole` (ADR 0012 D1), so the watch cannot
    feed itself. Under L2 each drain-handler write costs one pass that reads converged
    labels and writes nothing.
- The alternative with the same reach, a manager `SyncPeriod` of a few minutes, is rejected:
  every CR, every period, for nothing; still up to a period stale; and it resyncs every
  watched type, not pods.
- Cost: S in code (one map function, one predicate, two unit tests), M in documents.

### B — the `known-master` record on Sentinel clusters

**B1 — one sentence in ADR 0008.** The filing's recommendation. D3
([`:61`](../adr/0008-known-master-annotation-is-the-recorded-authority.md)) gains: on
Sentinel clusters the record moves only with an operator promotion, so after a Sentinel
failover outside a roll it names a replica until the next roll — harmless because every
reader validates: the Sentinel init probes `ROLE` and scans, and the data init reaches the
record only after 30 s of Sentinel silence.

- Cost: zero code. It leaves the monitor line and the `replicaof` fallback naming a replica,
  and in the one situation the fallback fires — every Sentinel silent for 30 s — the new pod
  is chained onto a replica. Valkey accepts that and Sentinel repairs it on return: wrong,
  not lossy.

**B2 — the record is refreshed from two answers the pass already holds.** The Sentinel
status arm holds the INFO master (`clusterState.MasterPod`,
[`checker.go:115`](../../internal/health/checker.go)) and asks every Sentinel
`SENTINEL MASTER` for the peer-table check
([`checker.go:325-341`](../../internal/health/checker.go)), keeping only the peer counts and
an `agreeing` count today. It keeps the master name each Sentinel reports as well, and the
rule is: when a majority of the answering Sentinels names pod P, the INFO master is P, P is
one of the StatefulSet's ordinals, and the annotation — or, unrecorded, the pod-0 default
both ConfigMaps fall back to — names another pod, the pass calls `persistKnownMaster(P)`
([`rolling_update.go:1070-1088`](../../internal/controller/rolling_update.go)): the
existing writer, a no-op when unchanged, a plain `Update` otherwise, the in-memory value
restored on failure; a conflict is returned and the next pass retries, and on this path the
error is logged, never returned (Sentinel stays the authority). Two independent sources must
agree, so a single lagging Sentinel (C's shape) cannot move the record.

- **Placement is load-bearing.** `Update` decodes the server's response into `v`, status
  included, so a write between the `prevStatus` capture and `persistStatus` would discard
  the conditions the pass set in memory (`RWServiceEmpty`, `SentinelPeersStale`) for that
  pass. `CheckCluster` returns the agreed name as data ([`valkey_controller.go:2460`](../../internal/controller/valkey_controller.go)); the write runs **after**
  `persistStatus`, on the resourceVersion that write returned. Every existing
  `persistKnownMaster` caller runs before the capture; this would be the first inside
  `updateStatus`, which is why the order is stated here and not left to the implementation.

- **Sentinel path only, and explicitly so.** On non-Sentinel clusters the annotation is the
  demotion authority and moves only on evidence (ADR 0011 D4–D7). On Sentinel clusters it is
  never one — `checkSteadyStateSplitBrain` skips them
  ([`:154`](../../internal/controller/steady_state_master.go)) and the roll reads Sentinel
  (ADR 0008 D10) — so the refresh can cause no `REPLICAOF`. Its only readers are the two
  ConfigMap fallbacks, which become right instead of wrong.
- Trigger: the pass after the failover. A failover that kills the master already gets it
  (**run**: phase `Error` at 20:50:00, then the 10 s requeue of
  [`valkey_controller.go:378`](../../internal/controller/valkey_controller.go) until `Ready`
  at 20:50:12); a manual `SENTINEL FAILOVER` does not, unless A4 exists.
- Cost: S. One CR update plus two ConfigMap updates per Sentinel failover; both ConfigMaps
  exclude the address from their hash (`GenerateSentinelConfForHash`, ADR 0008 D2), so no
  pod rolls. ADR 0008 D3 gains the steady-state writer; the comment at
  [`rolling_update.go:1039-1043`](../../internal/controller/rolling_update.go) stays true.

### C — the replacement pod's boot decision

The mechanism is in the Fact section: Phase 1 asks the Sentinels in ordinal order and the
first non-error reply wins (`break 2`,
[`statefulset.go:308`](../../internal/builder/statefulset.go)); a reply naming the pod
itself selects the master config ([`:332`](../../internal/builder/statefulset.go)). One
lagging Sentinel is enough, and the leader lagged 1.1 s (**run**).

**C1 — a name needs a majority.** The loop asks every Sentinel before deciding, counts the
names, and accepts one carried by at least `floor(n/2)+1` answering Sentinels — the same
quorum the monitor line uses (`SentinelQuorumFor`,
[`sentinel.go:60`](../../internal/builder/sentinel.go)). No majority → the loop's existing
retry (backoff, 30 s bound), then Phase 2 and 3 as today.

- Closes: the measured shape, one Sentinel behind two.
- Leaves: the window before `+promoted-slave`, in which every Sentinel still names the dead
  master (**run**: 1.2 s after `+try-failover`; a replacement created at :00 with its init
  at :01 can land inside it). Then a unanimous "you are the master" reaches a fresh, empty
  pod — the same double master.

**C2 — a Sentinel that is mid-failover is not a settled voice.** `SENTINEL MASTER <name>`
carries the address *and* the `flags` in one reply; the Go side already parses both
([`client.go:37-49`](../../internal/valkeyclient/client.go),
[`:631-652`](../../internal/valkeyclient/client.go)) and the observer already reads
`s_down`/`o_down` from that field
([`observer/checks.go:203`](../../internal/observer/checks.go)); the shell side needs an
`awk` over the flat key/value reply. A reply whose flags contain `failover_in_progress` is
not counted, and the loop retries. Upstream Sentinel sets that flag on the leader from
`+try-failover` until `+switch-master` (**inference**: the upstream `sentinel.c` as
remembered, not in this tree — verified on Kind by reading the flag during a failover). In
the measured run the leader would have answered "-1, failover_in_progress" at 20:50:01, and
the init would have waited for the 20:50:02.8 switch.

- `s_down`/`o_down` must **not** block a reply that names the booting pod itself: on a cold
  start Sentinel names pod-0 from the monitor line and flags it down because pod-0 is the
  pod that is booting. A cold start can still meet a `failover_in_progress` window (Sentinel
  tries and aborts a failover with no good replica, every `failover-timeout`); the cost is a
  few seconds, once, bounded by the 30 s loop — to measure on Kind with the reproduction.
- Closes: C1's residual window, from Sentinel's own state rather than from a timer.

**C3 — a fresh pod that Sentinel names as master waits for a second opinion.** The filing's
own candidate. When the majority names the pod itself and `/data` holds no `dump.rdb` or
`appendonly*` — always, on a non-persistent cluster — the init re-asks for a bounded window
(5 s, say) and takes the master config only if the answer stays unanimous through it.

- Closes: the same window as C2, without parsing flags.
- Cost: every replacement of a master on a non-persistent Sentinel cluster boots 5 s later,
  and every cold start pays 5 s on pod-0. A timer where C2 reads state — the fallback if the
  reproduction shows C2's flag does not cover the window.

**The same first-voice shape exists at two more sites**, for the record. The labeler's
cross-check `GetMasterAddress` ([`labeler.go:350`](../../internal/sidecar/labeler.go)) takes
the first answering Sentinel — a lagging one keeps the promoted pod labelled `replica` for
one more poll, direction `-rw` empty, safe. The roll's `getSentinelMasterPodName`
([`rolling_update.go:1740`](../../internal/controller/rolling_update.go)) does too, which
ADR 0025 D9's window guard already accounts for. C1's counting, written once in Go for the
sidecar, serves the first; the roll site is not proposed to change.

Whether `MultipleMasters` should be measured on Sentinel clusters outside a roll — the
health checker saw C and only logged
([`checker.go:247`](../../internal/health/checker.go)) — stays the separate ADR 0025
question the Fact section names.

### L — the label handover itself

The label is the `-rw` selector, so its lag is the write outage of every handover. The lag
is the labeler's poll: `--poll-interval=1s`
([`statefulset.go:916`](../../internal/builder/statefulset.go), pinned by
[`statefulset_test.go:782`](../../internal/builder/statefulset_test.go)), one
`INFO replication` per second per pod, one `SENTINEL MASTER` per poll on the master of a
Sentinel cluster, a patch only on change. What it costs per handover shape (all **read**):

| Handover | Who moves the label | Window on `-rw` |
|---|---|---|
| roll, non-Sentinel (`promoteAndRedirect`: `REPLICAOF NO ONE`, then the outgoing master demoted, ADR 0012 D9) | both pods' labelers, each on its next poll | ≤ 1 s with the outgoing master still selected, or both pods, or neither |
| drain, non-Sentinel | the dying master writes `draining` synchronously ([`drain.go:121`](../../internal/sidecar/drain.go)); the promoted peer's labeler on its next poll | ≤ 1 s empty — writes fail fast; direction safe |
| Sentinel failover | the promoted pod's labeler plus cross-check; the old master's labeler after `+convert-to-slave` | ≤ 1 s empty, then ≤ 1 s with the old master still selected when it is alive (a manual failover) |

**L1 — a shorter poll.** `--poll-interval=250ms` (or 500 ms). The mean lag drops from
500 ms to 125 ms and the bound from 1 s to 250 ms, on every shape above. Cost: four loopback
`INFO replication` per second per pod; on a TLS cluster each is a fresh dial and handshake
by design (ADR 0030 D2), plus four `SENTINEL MASTER` per second from the master of a
Sentinel cluster — expected negligible against what `valkey-server` serves, to measure once
on Kind before the default moves. It changes the pod-spec hash: one data-tier roll on the
release, which [ADR 0005 D11](../adr/0005-upgrade-neutral-defaults-and-anti-affinity.md)
already names as the baseline; Sentinel pods carry no labeler. ADR 0011 D14 keeps its margin
(15 s above the poll).

- **Multi-replica templates only; a standalone keeps `1s`.** A single pod has no handover,
  so its poll speed buys nothing — and its template must not move for this:
  `singlePodDeferral` ([`pod_security_migration.go:128-152`](../../internal/controller/pod_security_migration.go))
  defers a rootless single pod only for an image-only sidecar drift (`isSidecarOnlyChange`
  compares images, never args, [`rolling_update.go:3842`](../../internal/controller/rolling_update.go)).
  On a release the sidecar image moves with the args and the pod is deferred as today; on
  kustomize or a floating tag the image does not move, an args-only drift is not
  "sidecar-only", and the only pod of a non-persistent cluster would be replaced with its
  dataset — the ADR 0007 D7 shape its own comment names. Leaving the standalone template
  untouched closes that before it opens.
- **Log noise.** The labeler logs "failed to detect role" on every poll while Valkey is
  not up (boot, termination): four lines per second instead of one in those windows.
  Accept, or log once per state change — a small sidecar change on the same path.
- **A measuring instrument exists** for what the lag costs: the observer's write test
  (`writeTestOK`, [`observer/metrics.go:36`](../../internal/observer/metrics.go), 2 s
  cadence), whose failure rate over rolls is the handover write outage as a client sees it.
  Coarse per event, sound as a rate.

**L2 — the drain handler labels the peer it promoted.** In `manualFailover`, after
`REPLICAOF NO ONE` and `stampPromotion`
([`drain.go:172-179`](../../internal/sidecar/drain.go)): `PatchLabel(peer, instanceRole,
master)`, best-effort like the stamp. The grant exists — the stamp already patches the peer,
and the sidecar Role carries `get, patch` on every pod of the StatefulSet
([`rbac.go:72`](../../internal/builder/rbac.go), ADR 0012 D8 step 3). The peer's own labeler
writes the same value on its next poll; both derive it from `ROLE`, so they cannot disagree.
Non-Sentinel only, the drain's scope (ADR 0012 D10). It closes the drain's empty window to
the API round trip. Touches ADR 0012 D6 and D12 by one clause each ("each pod's sidecar
does" gains "and the drain handler of the pod that promoted it"); the operator still writes
nothing.

**L3 — event-driven on Sentinel clusters**, a subscription to `+switch-master` on
Sentinel's pub/sub. Not proposed: a subscriber with reconnect and per-dial TLS material, for
the 125 ms that L1 leaves.

### Not proposed, with its price

**The operator stamps the labels of the pods it promotes and demotes in a roll.** In
`promoteAndRedirect`, right after `REPLICAOF NO ONE` and after the demotion of the outgoing
master, both pods ownership-proven, non-Sentinel only. It would close the roll's window to
the API round trip and is safe by construction: it derives from the same `ROLE` the sidecar
reads, and the operator never promotes a terminating pod (`available()`, ADR 0026), so it
never touches a `draining` label (ADR 0012 D6). It is nevertheless a re-decision of ADR 0012
D12, which was written as a rule — "writing the label from the controller would reintroduce
the second writer D1 exists to prevent" — out of T7, against a *repair* writer that never
existed. It is also the only option that can *sequence* the labels with the commands — the
outgoing master labelled `replica` before its `REPLICAOF`, the promoted pod `master` right
after its `REPLICAOF NO ONE` — which no sidecar-side change can do, so a planned handover's
window would fall to the API round trip. L1 takes three quarters of the gain with no rule
change. If the remaining 250 ms of a planned handover ever matter — measured as the
observer's write-test failure rate over rolls, above — this is the option, and it needs D12
rewritten as "the operator repairs no label" rather than "writes none".

### Recommended bundle, in order

*Superseded 2026-09-27 by the Decision table below: decisions 1, 2, 5 and 6 form one change
(A1 + A2, B2, A4, `RWServiceMisrouted`), 3 follows the Kind reproduction, 4 is nothing. Kept
as the recommendation that was put to Hans, decision by decision.*

1. **A1 + A2 + B2** — the measured incident, closed at the record. Effort S; one ADR
   sentence each in 0011 and 0008. A2 gives `status.masterPod` one meaning on the way.
2. **C1 + C2, after the Kind reproduction the filing already demands** — on the data init,
   with C1's counting reused by the labeler's cross-check. The reproduction decides whether
   C3 is needed and whether C's severity stays `low`.
3. **L1, measured once on a TLS cluster, and L2 with it** — the write outage of every
   handover cut by three quarters, the drain's to a round trip.
4. **A4** — the structural closure, once the document sweep is affordable. It is what makes
   step 1 cover the role changes that kill no pod.
5. **A3** — the class made visible on the CR; the only item that adds a status surface.

## Decision

Seven decisions, taken one at a time on Hans's rule of 2026-09-26; this table is the one
current decision of the ticket. A row moves to *decided* with its date and the chosen option;
the weighing that carried the mark is in Options.

| # | Decision | Chosen | Date |
|---|---|---|---|
| 1 | `status.masterPod` after a non-Sentinel roll | **A1 + A2** — recheck in the two proving branches, `rollingUpdateRequeueDelay` reused (no new constant), and rule 1 yields to rule 2 in the pass that proved the label stale; one meaning for the field on both topologies | 2026-09-27 |
| 2 | the `known-master` record on Sentinel clusters | **B2** — refreshed when a majority of the answering Sentinels and INFO name the same ordinal and the record (or its pod-0 default) names another; Sentinel path only; the write after `persistStatus`, logged on failure | 2026-09-27 |
| 3 | the replacement pod's boot decision | **reproduce first, then C1 + C2**, C3 only as fallback — the reproduction on `TestE2E_SidecarFailoverDrainMaster` (already a Sentinel cluster whose master is deleted) plus a `--grace-period=0` run, replacement named by UID, its init and sidecar logs captured (a log helper is needed); C1 as *majority of the answering Sentinels*, so one answering Sentinel is today's behaviour and a degraded tier does not wait 30 s | 2026-09-27 |
| 4 | the label handover | **nothing** — the 1 s poll stays; L1, L2 and the operator-side stamping stay documented in Options with the observer's write-test rate as the instrument that would reopen this | 2026-09-27 |
| 5 | a Pod watch on `instanceRole` changes | **A4 now**, in the same change as decisions 1 and 2 — Update events on a changed `instanceRole` value only, mapped by the cluster label; ADR 0011 D21 amended (its reasoning stands, timeliness added as the requirement it never weighed), the other eight "no Pod watch" sentences given one clause each; the full e2e suite on both legs is the proof for the extra passes inside a roll | 2026-09-27 |
| 6 | a misrouted `-rw` selection reported on the CR | **A3 as a new level `RWServiceMisrouted`** — same evaluator as `RWServiceEmpty` (`reportRWServiceEndpoints`), judged from the A2 verdict on the non-Sentinel arm and `clusterState.MasterPod` against the labelled pod on the Sentinel arm; own registry row, README row, ADR 0012 D12 sentence; presence-guarded like its sibling | 2026-09-27 |
| 7 | the crash-restart adjacent finding | **its own ticket, [T36](036-non-persistent-master-restarts-empty.md)** — no board row: Hans rejected the board outright ("a board entry was never sufficient"); a finding is a new ticket or an appendix to the existing ticket of its family, and this one differs from T35 in mechanism and severity, so it is new. The Kind reproduction first, as proposed; severity high is an estimate until then | 2026-09-27 |

## Verification

Per option, each with the revert check ADR 0017 asks for: the named test fails without the
change.

- [ ] **A1.** Unit: a pass whose sole labelled pod answers `role:slave` returns a requeue of
  the chosen delay, and so does a pass whose sole labelled pod is terminating. Revert:
  without the two `requestRecheck` calls both fail. Kind: after the roll of
  `TestE2E_RollingUpdate_MultiReplicaNoSentinel`, `status.masterPod` equals the
  `INFO replication` master within one recheck — the fixture already reads that master by
  INFO (`findMasterPod`,
  [`rolling_update_test.go:195-209`](../../test/e2e/rolling_update_test.go)); the status
  comparison is the new assertion.
- [ ] **A2.** Unit: the completing pass writes the annotation's pod into `status.masterPod`
  when the sole label was proven stale in the same pass, and the labelled pod when nothing
  was proven. Revert: without the pass-state field the first fails.
- [ ] **A3** *(decision 6: a new level)*. Unit: a settled cluster with one labelled pod
  that is not the INFO master reports `RWServiceMisrouted=True/LabeledPodIsNotMaster`, and
  clears it on the next settled pass whose label matches; `RWServiceEmpty` is untouched by
  the same fixture. Its own registry row; the ADR 0027 guard stays green; the README row
  exists; ADR 0012 D12 names both levels.
- [ ] **A4.** Unit: the predicate fires on a changed `instanceRole` value only — not on a
  status-only pod update, not on a pod without the cluster label — and the map function
  returns the CR named by the pod's namespace and cluster label. Integration (envtest): a
  label patch on a data pod enqueues a reconcile of its CR, visible through the status write
  it produces. Sweep: `grep -rn "no Pod watch" docs CLAUDE.md DEVELOPER.md`
  returns only sentences that say when the watch was added. *(File list amended 2026-09-27:
  `SECURITY_ARCHITECTURE.md` is now `docs/security/`, which `docs` covers, and `DEVELOPER.md`
  is new.)*
- [ ] **B1.** The ADR 0008 D3 sentence.
- [ ] **B2.** Unit: with a majority of Sentinels and INFO naming P and the annotation naming
  Q, the pass writes P; with the Sentinels split, with INFO disagreeing, or with P outside
  the ordinals, it writes nothing; on a non-Sentinel cluster it never runs. Revert: without
  the refresh the first fails. Kind: a Sentinel cluster in the shape of
  `TestE2E_HAClusterWithSentinel`, master deleted, `+switch-master` seen; then the
  annotation and the monitor line of `<name>-sentinel-config` name the new master within
  one pass after `Ready`.
- [ ] **C, reproduction first** — the filing's item, sharpened. On Kind, a Sentinel cluster;
  the master deleted **with** its grace period (the drain forces the failover, as on wds18)
  and, in a second run, with `--grace-period=0` (Sentinel's own 5 s `down-after` path). The
  replacement is named by UID (`waitForPodRecreated`, ADR 0017 D50), and its
  `init-config-selector` log and its sidecar log are captured before anything else touches
  it. Recorded: which Sentinel answered, what it said, whether the init chose the master
  config, and whether the sidecar ever labelled the pod master. That decides C1/C2/C3 and
  whether C's severity stays `low`.
- [ ] **C1 + C2.** The init script is generated Go text, so the unit is in `internal/builder`:
  the generated script asks every Sentinel, counts, and skips a reply carrying
  `failover_in_progress`. `make test-image-tools` runs the script's commands under the
  restricted posture; `awk` has its line in `RequiredImageTools` or gets one (ADR 0017).
  Kind: the reproduction above, re-run, with the replacement booting as a replica of the
  promoted pod on every run and the health checker logging no "Multiple masters".
- [ ] **L1.** Measured first, on a TLS Sentinel cluster on Kind: sidecar and `valkey-server`
  CPU at 1 s against 250 ms over ten minutes. Then the constant, the builder test, and an
  e2e assertion that the `-rw` EndpointSlice follows a drain within the new bound
  (`readyEndpointPodNames`).
- [ ] **L2.** Unit (sidecar): after a successful promotion the handler patches the peer's
  label to `master`, after the stamp and before the redirect, and a failed patch does not
  fail the drain. Revert: without the call the first fails. Kind:
  `TestE2E_SidecarFailoverDrainMaster` with the `-rw` EndpointSlice read within the round
  trip of the promotion.

## Adjacent findings

Not in scope, not filed.

- **Chaos Mesh kills more than one pod per tick.** Pods were recreated in threes at 20:50 and
  20:55 and in twos at 21:10, and the operator deleted nothing after 20:48 (run).
  - The Schedule logged `Failed to update lastScheduleTime` conflicts and `Forbid spawning new
    job … still running`.
  - Several jobs per tick are likely. That was not checked. It is outside this repository.
- **`valkey9-tls` runs `valkey/valkey:8.0`**, per its spec (run).
- **A transient `FailedMount` at 20:55:02.** Pod `valkey9-tls-1` could not mount the
  projected token `sidecar-api-access`: "the UID in the bound object reference … does not
  match". The kubelet was fetching a token for the pod object that had been deleted and
  recreated under the same name. The pod came up Ready with 0 restarts (run).
- **Observer readiness 503s.** They occur only in the roll window (20:45–20:47) and at the
  chaos ticks. The observer reports replica sync and read-test failures there, which is its
  job (run).

- **A non-persistent master that crash-restarts comes back empty and stays the master**
  (read, with an inference; not measured; found 2026-09-26 while costing C3). Init
  containers run once per pod, so a `valkey-server` container the kubelet restarts boots
  from the config the init wrote into the pod's writable config volume — the master config,
  if it was master — with no dataset and on the same address. Sentinel sees the same master
  back inside `down-after-milliseconds` (5 s,
  [`sentinel.go:47`](../../internal/builder/sentinel.go)) and has no reason to fail over; a
  non-Sentinel cluster has no arbiter at all. The replicas then full-resync from an empty
  master. Upstream documents the shape under "Safety of replication when master has
  persistence turned off" and recommends that such a master not restart automatically.
  None of the options above touches it. On a non-persistent cluster it is a dataset lost to
  a container crash the replicas survived — above this ticket's severity. **Filed as
  [T36](036-non-persistent-master-restarts-empty.md) on 2026-09-27** (decision 7).
- **`status.masterPod` means two things**, one per topology — Fact A and Options, "six
  records". A precision defect of the field itself; A2 is the option that fixes it.
- **Three sites take the first Sentinel that answers** — Options C, last paragraph.

## History

- 2026-09-27 — `SECURITY_ARCHITECTURE.md` was split into `docs/security/` by the documentation
  restructure: the "no Pod watch" premise at its line 1010 now also names its new place,
  `docs/security/rootless-migration.md:69`, and the A4 sweep command no longer names the
  deleted file. No finding changed.
- 2026-09-27 — renamed to `035-master-records-lag-the-real-master.md` (was `local_T35-master-records-lag-the-real-master.md`) when the tickets were numbered.
- 2026-09-27 — **decision 7 taken, and two rules from Hans recorded verbatim in substance.**
  - **Decision 7: the crash-restart finding gets its own ticket, [T36](036-non-persistent-master-restarts-empty.md).**
    Hans's answer to the board-row proposal, in his words: the board is badly implemented and
    annoying — delete it; there is either a new ticket or the finding is appended to an existing
    one; an entry in the board was never sufficient. Consequences applied the same day: the
    board (`local_BOARD.md`) archived verbatim into `local_neue_baustellen.md` (its RELEASE
    narrative records cluster operations that exist nowhere else) and deleted; the template's
    "board row only" clause replaced by "every finding is a file, new or appended";
    `## Options` allowed wherever a decision is open. T36 is a new file, not an appendix to
    T35, because its mechanism (a container restart, not a record) and its severity differ.
  - **Rule (global, 2026-09-26, applied here from decision 1 on):** decisions are presented one
    at a time; each with options researched against the code and sensible in the project's
    context, weighed against each other, the best one marked and its mark justified; recorded
    in `~/.claude/CLAUDE.md` and the memory `decisions-one-at-a-time-with-marked-best-option`.
  - **State `analysed` → `decided`**, `decided: 2026-09-27`, effort S → M (decisions 1, 2, 5,
    6 are one change with a nine-document sweep), `blocked-by` removed. Urgency stays `later`
    by rule 4 (a decided fix, severity low).
  - **The seven decisions, in one place:** 1 A1 + A2 · 2 B2 · 3 reproduce first, then C1 + C2 ·
    4 nothing · 5 A4 now · 6 new level `RWServiceMisrouted` · 7 own ticket T36. Six of seven
    followed the marked option; decision 4 (nothing) and decision 6 (a new level rather than a
    reason on `RWServiceEmpty`) did not, each with its reason in its own entry below.
- 2026-09-27 — **decision 6 taken: A3 as a new level `RWServiceMisrouted`** (Hans; the
  recommendation was a second reason on `RWServiceEmpty`, not taken for the name — a level
  called "Empty" must not be True with one endpoint). Weighed and not taken: later with a
  field trigger, never. The precedent for the flicker (`MultipleMasters`, ADR 0025) and for
  one evaluator serving two conditions (both arms of `reportRWServiceEndpoints`) carries over.
- 2026-09-27 — **decision 5 taken: A4 now** (Hans). Weighed and not taken: A4 later with a
  fleet observation as the trigger (defers a gap proven from the code), never (a 5-minute
  requeue on healthy Sentinel clusters would be polling where an event exists). Read for
  it: the pod template carries no `instanceRole` (no caller of `PodLabels` in the builder),
  so the sidecar's first patch is the first event; `pods: watch` is already granted in both
  RBAC sources; the operator's own pod patches are annotations and never fire the predicate.
- 2026-09-27 — **decision 4 taken: nothing, the 1 s poll stays** (Hans). Weighed and not
  taken: L1 + L2 (recommended: 250 ms poll on multi-replica templates plus the drain
  handler labelling the promoted peer), L1 alone, and operator-side stamping in
  `promoteAndRedirect` (a re-decision of ADR 0012 D12). Read for it: `isSidecarOnlyChange`
  compares images only, so L1 would have had to leave the standalone template untouched.
- 2026-09-27 — **decision 3 taken: reproduce first, then C1 + C2, C3 as fallback** (Hans).
  Weighed and not taken: C1 now and C2 later (same fixture written blind, two changes at
  one site), C1 + C2 without the reproduction (C2 rests on an inference whose failure mode
  is inert, but its proof needs the logs anyway), accepting the ~10 s double master (a
  self-made write-loss window). `awk` is already in `RequiredImageTools`
  (`image_requirements.go:44`), so C2 adds no tool.
- 2026-09-27 — **decision 2 taken: B2** (Hans). Weighed and not taken: B1 (zero code, a
  record and two ConfigMaps documented as wrong), B3 (a second Sentinel round before
  `updateStatus`: doubles the Sentinel traffic of every pass for a once-per-failover write).
  Two facts read for it: `persistKnownMaster` is a plain `Update` without retry, and an
  `Update` decodes the server response into `v` status included, so the write must follow
  `persistStatus`.
- 2026-09-27 — **decision 1 taken: A1 + A2** (Hans, on the single-decision presentation).
  Weighed and not taken: A1 alone (leaves one recheck period of a value the pass knew was
  wrong, and the two meanings of the field), A4 now (solves a different problem, this one a
  second later; nine-document sweep; unverified effect of extra passes on a running roll),
  `SyncPeriod` (blanket resync). Decisions 2–7 open.
- 2026-09-26 — **refined with options on Hans's explicit request** ("refine this ticket with
  solution approaches"), although the template allows `## Options` only above the filing bar
  — the same exception as the filing, recorded here. The filing's per-part proposals (A
  recheck, B one sentence, C reproduce first) are carried into Options as A1, B1 and C1–C3,
  each now checked against the code; the filing's open question on A1 ("check first that the
  recheck is honoured") is answered: it is, at `valkey_controller.go:310`. New in the
  refinement, all read at `f5c6886`: the two meanings of `status.masterPod` (Fact A,
  Options), the four role changes without a side effect, A2–A4, B2, C2, the L options on the
  label handover, the option deliberately not proposed with its price, and the crash-restart
  adjacent finding. Decision stays open with a recommended bundle; state unchanged
  (`analysed`).

- 2026-09-26 — **filed and analysed** from the check of the v1.13.0 upgrade on wds18,
  `database-examples`.
  - Written as its own file on explicit request, although it is below the filing bar
    (severity low, security none), where the template allows a board row only.
  - Urgency `later`:
    - rules 1 and 2 do not match: the observations are pre-existing and gate nothing;
    - rule 3 does not match: severity low;
    - rule 4 matches: A has a cheap known fix.
  - Effort `S` covers A and B. C may grow once reproduced.
