---
id: T35
title: records of who the master is lag the real master after a handover (seen on wds18 after the v1.13.0 upgrade)
state: decided
severity: low         # kept 2026-09-27 at 84a39c2, its reason restated by mechanism (History); re-examined together with T67's decision on the roll's own failover, which now owns the outgoing master's write loss (History)
security: none
urgency: now          # rule 1 since 2026-09-27 at 84a39c2: ten tracked places state a master record the code does not keep (Work list, XS); was later (rule 4); back to later when all ten are corrected
effort: L             # was M (History 2026-09-27)
blocked-by: decision  # added 2026-09-27 at 84a39c2: decisions 1 and 3 carry re-decision proposals whose premises were re-checked, decision 8 is new and open
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
  merge of it), or in the controller-runtime module cache. *(2026-09-27: every location in this
  file was re-read at `84a39c2`.)*
- **inference** means neither.
- *(added 2026-09-27)* **docker** means measured on 2026-09-27 against the two pinned images
  `valkey/valkey:9.1.1` and `valkey/valkey:8.1.9` (setup under Verification, "Measurements of
  2026-09-27"); **Kind** means read in the pod logs of `TestE2E_SidecarFailoverDrainMaster` runs
  of 2026-09-26 on Valkey 9.1.1.

~~Logs and reads were taken between 20:55 and 21:13 UTC. The operator log is saved in the
session scratchpad as `wds18-op.log` (untracked).~~ *(corrected 2026-09-27 at 84a39c2: live reads
ran between 20:55 and 21:13 UTC, but what was saved ends earlier. The saved operator log
`wds18-op.log` holds 1994 lines from 20:44:34Z to 20:55:10Z, and the saved `wds18-events.json`
holds 1157 events from 20:00:00Z to 20:55:06Z; both files were written at 20:57 UTC. They sit in
the `/private/tmp` scratchpad of session `538d7ed7`, which does not survive a reboot, so every
excerpt this ticket relies on is quoted below with its reconcileID or line. The Sentinel logs,
the sidecar logs and every read after 20:55:10 were read live and not saved: those **run** facts
cannot be re-checked.)*

## Context: the upgrade itself went as intended

- **Upgrade (run).** Flux upgraded the HelmRelease to chart 1.13.0 at 20:44:22Z (Helm revision
  96). The operator pod started at 20:44:33Z, commit `ad81a47`, 0 restarts.
- **Cluster (run).**
  - 8 Valkey CRs, 3 replicas each, none with persistence.
  - 4 with Sentinel (3 Sentinels each), 4 with TLS.
  - Metrics and observers are enabled.
  - Chaos Mesh Schedule `valkey-chaos` runs `pod-kill`, mode `one`, every 5 min, on
    `app.kubernetes.io/managed-by=vko.gtrfc.com`.
  - *(added 2026-09-27, from the saved log)* The same operator also rolled the four production
    CRs `gitlab-valkey` (namespace `gitlab`), `gpt-valkey` (`gpt`), `harbor-valkey` (`harbor`) and
    `oauth2-valkey` (`iam`). **All four are Sentinel and TLS clusters**: each emitted
    `SentinelUpdateComplete`, and each logs served-certificate lines (89, 88, 73 and 86). Part A
    (non-Sentinel) does not reach them; parts B, C and the label lag of decision 8 do.
- **Rolls (run).**
  - Data tiers rolled between 20:45:06 and 20:45:52, Sentinel tiers by 20:47:13.
  - Exactly one `RollingUpdateComplete` per CR and one `SentinelUpdateComplete` per Sentinel
    CR. *(2026-09-27, saved log: 12 `RollingUpdateComplete` and 8 `SentinelUpdateComplete`, the
    eight `database-examples` CRs plus the four production CRs.)*
  - No Warning Event on any Valkey object. *(2026-09-27: 0 Warning events of kind Valkey in the
    saved log.)*
  - ~~Four operator errors, all 409 conflicts, retried by the next pass.~~ *(corrected
    2026-09-27 at 84a39c2: the saved log holds three `Reconciler error` lines, all "the object has
    been modified", on `valkey8-sentinal`, `valkey8-sentinal-tls` and `valkey9-sentinal`; a
    fourth, if there was one, came from a live read after 20:55:10 and is not verified.)*
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
  available)", 20:45:06 (saved log lines 134-135).
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
| after 20:45:43 | operator log | ~~**no line for `valkey9`** up to the save at ~21:12~~ *(corrected 2026-09-27 at 84a39c2: the saved log ends at 20:55:10Z, not at ~21:12. Up to there it holds no `valkey9` line after line 1145, and the saved events hold no `valkey9` pod or StatefulSet event from 20:45:44 to 20:55:06. The stretch to ~21:12 was a live read that was not saved and is not verified)* |
| 21:12:45 | CR | `status.masterPod: valkey9-1`; the real master is `valkey9-0` (INFO replication, ~20:57) |

*(Added 2026-09-27, saved log.)* The promotion of pod-0 ran in pass `73101166` (log lines
1136-1141: "Promoted pod-0 back to master", "Updating replica ConfigMap", two "Redirected replica
to pod-0", "Setting rolling update state"). The completion and the stale-label line ran in the
next pass, `ad9aea37` (lines 1142-1145: "All pods updated but rolling update state still
present, finalizing", "Multi-replica rolling update completed, topology restored", the
`RollingUpdateComplete` Event, and "Sole master-labeled pod does not report master role; the
label is stale" for pod `valkey9-1` with `knownMaster` `valkey9-0` and role `slave`). The log
silence alone proves nothing for a non-TLS cluster: a healthy non-TLS pass logs nothing at INFO,
and the served-certificate V(1) line exists only on TLS dials
([`served_certificate.go:60`](../../internal/health/served_certificate.go)). The evidence that no
pass ran is the status value at 21:12:45 together with the absence of any `valkey9` pod event in
the saved events.

**Mechanism (read).**

1. **The status names the labelled pod.** The completing pass writes the status after its
   steady-state check.
   - `currentMasterPod` ([`valkey_controller.go:2324-2342`](../../internal/controller/valkey_controller.go))
     answers with the pod that carries the `instanceRole=master` label, when exactly one does.
     It does so before the known-master record.
   - *(Added 2026-09-26, read.)* That is the **non-Sentinel arm** of `updateStatus` only
     ([`:2252`](../../internal/controller/valkey_controller.go)). The Sentinel arm writes
     `clusterState.MasterPod` ([`:2477`, `:2490`](../../internal/controller/valkey_controller.go)),
     the pod the health checker found answering `role:master`
     ([`checker.go:115`](../../internal/health/checker.go)), and never reads the label. One
     field, two meanings — which is why B's status was right while A's was wrong.
   - That is its documented rule (the comment at `:2300-2322`): the label is the `-rw`
     selector, so it names the pod that receives writes. *(2026-09-27: the comment starts at
     [`:2289`](../../internal/controller/valkey_controller.go), and its second line — "The HA
     path has its own answer (clusterState.MasterPod, from Sentinel)" — is false by reading, as
     is the last sentence of [ADR 0002 D11](../adr/0002-surface-a-blocked-reconcile-on-the-cr.md)
     (`:331-332`, "the master as Sentinel reports it"): `findMaster` asks every data pod for
     `INFO replication` and takes the one answering `role:master`, the most connected replicas
     on a tie ([`checker.go:218-258`](../../internal/health/checker.go)); no Sentinel is asked.
     Both corrections are XS and need no decision — Work list.)* *(Both landed 2026-09-27, History:
     the comment and D11 now name `findMaster` and say that no Sentinel is asked.)* *(2026-09-27
     at 84a39c2: landed in commit `bcc63c9`; ADR 0002 carries the Status line at `:51-59` and the
     struck text with its correction at `:342-346`, the comment reads correctly at
     `valkey_controller.go:2289-2291`.)*
   - At 20:45:43 that was still `valkey9-1`. The sidecar polls every 1 s and had not
     relabelled yet.
2. **The pass saw the stale label and asked for nothing.** The branch at
   [`steady_state_master.go:261-267`](../../internal/controller/steady_state_master.go) logs
   "the label is stale" and returns. The terminating branch is at `:249-253`, the INFO probe at
   `:255`.
3. **Nothing re-enters Reconcile afterwards.**
   - There is no Pod watch (`For` with `GenerationChangedPredicate` at
     [`valkey_controller.go:2987`](../../internal/controller/valkey_controller.go), the `Owns`
     list at `:2988-2996`, the Secret watch at `:2997-3000`), and a pod label patch does not
     change the StatefulSet.
   - The CR watch carries `GenerationChangedPredicate`, and the healthy path returns no
     requeue ([`:373-396`](../../internal/controller/valkey_controller.go); the 10 s requeue on
     `Error` or `Syncing` is at `:377-379`).
   - `valkey9-0` was already Ready at 20:45:41, so no later StatefulSet status change came.
   - The cache resync is controller-runtime's default of 10 h
     (`sigs.k8s.io/controller-runtime@v0.25.1/pkg/cache/cache.go:45`). `cmd/main.go` sets no
     `SyncPeriod`. *(2026-09-27: `cache.go:120-126` says the resync Update is filtered by
     `GenerationChangedPredicate`, so it arrives only through the `Owns` watches.)*
   - So the field stays wrong until the next StatefulSet event, spec change or resync.
   - *(Added 2026-09-27, read in the module cache.)* **Why the 10 s requeue of pass `73101166`
     produced no later pass.** `promotePod0AndRedirect` returns a 10 s requeue
     ([`rolling_update.go:4680`](../../internal/controller/rolling_update.go)), and its
     "Updating replica ConfigMap" write triggered an immediate `Owns(ConfigMap)` event.
     controller-runtime v0.25.1 runs its priority queue by default
     (`pkg/controller/controller.go:271-289`, `ptr.Deref(UsePriorityQueue, true)`), and there an
     immediate add to an item that is waiting makes it ready and drops the delay
     (`pkg/controller/priorityqueue/priorityqueue.go:255-258`); whichever add comes first, one
     pass results. That pass was `ad9aea37`, which returned no requeue, so nothing remained.

**A second instance (inference).**
- `valkey9-tls` logged the same line at 20:45:39: pod `valkey9-tls-2`, role `slave`.
- ~~Its next logged pass was 20:55:01, after a chaos kill of `valkey9-tls-1`, and its status now
  names `valkey9-tls-0` correctly.~~ *(corrected 2026-09-27 at 84a39c2: the saved log shows three
  `valkey9-tls` passes 5-8 s later, at 20:45:44 (reconcileID `7e97b2c2`, line 1146), 20:45:45
  (`5359f88d`, lines 1191-1194) and 20:45:47 (`04957bc7`, lines 1242-1245), after the 1 s
  relabel. Each carries served-certificate V(1) lines for peers `-0`, `-1` and `-2`. Those lines
  come from the health checker's TLS config builder
  ([`checker.go:382`](../../internal/health/checker.go)), which runs on every checker TLS dial,
  so they prove a checker dial to each pod and not by themselves that the pass reached the
  all-ready branch of `updateStatus` that calls `currentMasterPod`. Its status names
  `valkey9-tls-0` correctly now.)*
- ~~That the status named `valkey9-tls-2` in between follows from the same code path. It was
  not observed.~~ *(corrected 2026-09-27 at 84a39c2: the status most likely named the right
  master from about 20:45:44-47 on (inference). Only `valkey9` is the measured long-stale
  instance.)*

**Not this shape (run).**
- `valkey8` and `valkey8-tls` completed while their sole labelled pod was terminating ("Sole
  master-labeled pod is terminating; refusing to adopt a dying authority"; saved log line 910,
  `valkey8`, 20:45:31, pod `valkey8-1`, and line 1015, `valkey8-tls`, 20:45:36, pod
  `valkey8-tls-1`).
- They completed before the old master's replacement existed, so later StatefulSet events
  re-entered them. Their status is correct now.
- What they carried in between is not recorded.

**Also in the window (inference, from the table).** The `-rw` Service selected the replica
`valkey9-1` from the promotion of pod-0 until the relabel, at most about 1 s. That is inherent
in the label handover and outside this ticket.

**Origin (read).** `currentMasterPod` in this form came with `744b589` (2026-08-21, first
released in v1.11.0; `git tag --contains 744b589` starts at `v1.11.0`).

### B — the `known-master` annotation of `valkey8-sentinal-tls` names a replica

**What happened (run).**

- **After the roll: `-1` is master.** The roll's Sentinel failover made
  `valkey8-sentinal-tls-1` master. The operator log reads "New master verified with data",
  `newMaster: valkey8-sentinal-tls-1`, 20:45:46 (saved log line 1212). The annotation named
  `-1`. *(2026-09-27, saved log: the record was written at finalization, 20:46:42, pass
  `e87a7189`, line 1674 "Syncing sentinel with current master before finalization", through
  `persistKnownMaster`; the saved log has no "Could not persist" line.)*
- **20:50:00: chaos kills `-1`.** Chaos Mesh killed `-1`, and Sentinel failed over.
  - `sentinel-0`: `+try-failover` 20:50:00.453, `+promoted-slave valkey8-sentinal-tls-0`
    20:50:01.628.
  - `+switch-master` on `sentinel-1`/`-2` at 20:50:01.645/.647 and on `sentinel-0` at
    20:50:02.777.
  - *(2026-09-27: these Sentinel timings were read live; no Sentinel log of wds18 was saved,
    so they cannot be re-checked. Their shape matches the docker measurement and the Kind
    logs below.)*
  - The log shows no `+sdown`/`+odown` in front of `+try-failover`. That fits a forced
    `SENTINEL FAILOVER` from the drain handler on SIGTERM (`internal/sidecar/drain.go`)
    (inference). *(2026-09-27: supported by the saved events, which show "Killing: Stopping
    container sidecar" for `valkey8-sentinal-tls-1` at 20:50:00, so SIGTERM reached the drain
    handler; it asks `sentinel-0` first
    ([`drain.go:135-148`](../../internal/sidecar/drain.go)); and a forced `SENTINEL FAILOVER`
    logs `+try-failover` with no preceding `+odown` on both pins (docker).)*
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
  [`rolling_update.go:1045`](../../internal/controller/rolling_update.go)).
  - This write is deliberately best-effort. The comment at `:1034-1044` says why: Sentinel is
    the authority there, the annotation only pre-seeds a restarting Sentinel, and
    `checkSteadyStateSplitBrain` never runs for Sentinel clusters. *(corrected 2026-09-27 at
    84a39c2: the middle reason is false by reading. The record also feeds the replica
    ConfigMap's `replicaof` ([`configmap.go:160-167`](../../internal/builder/configmap.go)),
    which is built for Sentinel clusters too (`needsReplicaConfigMap`,
    [`valkey_controller.go:530-533`](../../internal/controller/valkey_controller.go)), and the
    Sentinel data init adopts it in Phase 2 and self-claims the master config when it names the
    booting pod — see "Where it is read". The comment at `rolling_update.go:1040-1041` states
    the same falsehood; its correction is in the Work list.)*
  - A Sentinel failover outside a roll writes nothing. The writers are `:1045` and
    `recordPromotedMaster` ([`:1108`](../../internal/controller/rolling_update.go); callers
    `:1606`, `:4641`, `steady_state_master.go:493`, `valkey_controller.go:2942`); none runs on
    a Sentinel failover outside a roll.
- **Where it is read.**
  - The Sentinel ConfigMap takes the annotation as its monitor target
    ([`sentinel.go:87-96`, `:103-108`](../../internal/builder/sentinel.go)). It is excluded
    from the config hash (`GenerateSentinelConfForHash`), so the stale value rolls nothing. The
    replica side is excluded the same way (`GenerateValkeyConfForHash`,
    [`configmap.go:50-56`](../../internal/builder/configmap.go)).
  - A data pod's init falls back to the replica ConfigMap's `replicaof` only when no
    Sentinel answers within 30 s
    ([`statefulset.go:318-326`](../../internal/builder/statefulset.go)). *(2026-09-27: the
    Phase 1 loop sleeps 1+2+4+8+8+8 = 31 s in total, and each unanswered Sentinel adds up to
    3 s of timeout per round, [`:288-316`](../../internal/builder/statefulset.go).)*
  - *(Added 2026-09-27, read.)* **Phase 2 has no guard.** It adopts the record with no role
    probe and, when the record names the booting pod itself, selects the master config
    ([`statefulset.go:318-333`](../../internal/builder/statefulset.go); the annotation holds
    the full FQDN, so `grep -q $MY_HOST` matches). No steady-state check runs on the Sentinel
    path ([`steady_state_master.go:154`](../../internal/controller/steady_state_master.go)).
- **A booting Sentinel corrects the target itself.** Its init checks `ROLE` of the configured
  master. When that is not `master`, it scans the data pods and rewrites the monitor line
  ([`sentinel.go:654-692`](../../internal/builder/sentinel.go); the scan takes the first pod,
  in ordinal order, that answers `ROLE` master). ~~ADR 0008 places the record below peer
  discovery on purpose ("a stale record can never displace", `:96`).~~ *(corrected 2026-09-27 at
  84a39c2: ADR 0008 `:96` belongs to D6 and D7, the non-Sentinel init ranking; D7's guards —
  not-self, a live `role:master` probe — and D9's pairing with `checkSteadyStateSplitBrain`
  apply to that init only. The Sentinel data init's Phase 2 has neither guard, so on the
  Sentinel path a stale record is harmless only while some Sentinel answers.)*

**Not observed:** no Sentinel pod of this cluster restarted after 20:50. They date from
20:46:43 to 20:47:00.

### C — the killed master's replacement booted as a second master for about 5–10 s

Same event as B, `valkey8-sentinal-tls`.

**What happened (run).**

- 20:50:00: the operator logged "Could not find master via INFO replication" (no master found
  among 3 pods), phase `Error` (saved log lines 1921-1922, pass `be15ae29`).
- 20:50:07: `internal/health/checker.go:247` logged "WARNING: Multiple masters detected
  (split-brain)", candidates `valkey8-sentinal-tls-0` and `valkey8-sentinal-tls-1` (lines
  1927-1932, pass `6baad56e`, phase `Syncing`, "Instance not healthy, requeuing").
- The `-1` of that moment was the replacement created at 20:50:00. *(2026-09-27, saved events:
  PodChaos `valkey-chaos-djqbk` Applied, the Killing events of `-1`'s exporter, sidecar and
  valkey containers, and StatefulSet `SuccessfulCreate` of `valkey8-sentinal-tls-1`, all at
  20:50:00; at 20:50:01 four `Created` and four `Started` events — the init container and the
  three main containers — and `valkey/valkey:8.1` pulled twice. So the init started and
  finished inside 20:50:01.x, before `+promoted-slave` at 20:50:01.628 or at most before the
  non-leaders switched at 01.645/.647. The object vanished in the same second as the kill,
  which fits a force delete: Chaos Mesh `pod-kill` defaults to a grace period of 0, Verification.)*
- 20:50:11.746: `sentinel-2` logged `+convert-to-slave valkey8-sentinal-tls-1`.
- ~~20:50:12: `Ready=True/HAClusterReady`.~~ *(corrected 2026-09-27 at 84a39c2: no pass ran at
  20:50:12. The saved log shows `valkey8-sentinal-tls` passes at 20:50:00 (`be15ae29`, `Error`),
  20:50:07 (`6baad56e`, `Syncing`, 10 s requeue), 20:50:17 (`8414efb1`, no "requeuing" line, so
  healthy) and 20:50:18 (`8e3f9628`). `Ready=True` was most likely written at 20:50:17; the 20:50:12
  probably conflated it with `+convert-to-slave` at 20:50:11.746. Only the condition's
  `lastTransitionTime` would confirm it, and it was not saved.)*
- The direction was right: the empty pod was converted, and `-0` kept its dataset.
- **The replacement's init and sidecar logs are lost.** Chaos killed that `-1` again at
  21:10:00.

**Mechanism (inference, resting on read code).**

- The data init asks the Sentinels one after another, and the first answer wins
  ([`statefulset.go:288-316`](../../internal/builder/statefulset.go); the command is
  `SENTINEL get-master-addr-by-name` at `:297`, `break 2` at `:308`). If the answer names
  the pod itself, it boots with the master config (`:331-333`).
- The 21:10 replacement ran its init within 1 s of its creation (run: created 21:10:00, init
  21:10:01; a live read, not saved).
- ~~At 20:50:01, `sentinel-0` had not switched yet (its `+switch-master` came at 20:50:02.777).
  So `sentinel-0` still named `-1`, and the fresh, empty `-1` booted as master.~~ *(corrected
  2026-09-27 at 84a39c2: false for the command the init uses. For `get-master-addr-by-name`
  the failover leader switches **first**: Sentinel answers with
  `sentinelGetCurrentPrimaryAddress`, which returns the promoted replica once the failover state
  reaches `RECONF_REPLICAS`, and that state is entered together with `+promoted-slave`
  (valkey 9.1.1 `src/sentinel.c:1657-1668`, `:3905-3918`, `:2608-2622`; 8.1.9 `:1638-1650`,
  `:3854-3867`). Before `+promoted-slave` **every** Sentinel names the dead master. Measured
  (docker, both pins): the leader's answer switched 7-14 ms after `+promoted-slave`, about
  45-90 ms before the non-leaders', which switched 59-98 ms after `+promoted-slave`. The measured shape is therefore the pre-promotion window, 20:50:00.453
  to 20:50:01.628, in which every Sentinel named `-1`, and the replacement's init ran inside it
  (events above). The leader's 1.1 s lag exists only for `+switch-master` and for the `ip` field
  of `SENTINEL MASTER` — decision 8.)*

**Why `-rw` was probably safe (inference).**
- ~~The sidecar labeler of a Sentinel cluster labels `master` only when Sentinel agrees
  ([`labeler.go:89-92`](../../internal/sidecar/labeler.go)). By 20:50:02.8 every Sentinel
  named `-0`.~~
- ~~So `-1` was probably never labelled master, and `-rw` never selected it. The sidecar log
  that would prove this is lost.~~
- *(corrected 2026-09-27 at 84a39c2: the conclusion holds, for a different reason.)* **The
  cross-check most likely confirmed `-1`.** It is at
  [`labeler.go:135-145`](../../internal/sidecar/labeler.go) and reads `SENTINEL MASTER` `ip`
  from the first Sentinel that answers ([`labeler.go:351-363`](../../internal/sidecar/labeler.go)),
  in the order `sentinel-0,1,2` ([`statefulset.go:1168-1188`](../../internal/builder/statefulset.go)).
  That field is `ri->addr` (valkey 9.1.1 `sentinel.c:3324-3325`), which on the leader
  `sentinel-0` kept `-1` until its `+switch-master` at 20:50:02.777. `myFQDN` is the pod name plus
  the headless FQDN ([`run.go:97`](../../internal/sidecar/run.go)), the same string Sentinel
  stores. So from its first poll at about 20:50:01 until 02.777 the empty `-1` passed its own
  cross-check and was most likely labelled `master`, and the promoted `-0` stayed labelled
  `replica` for the same time.
- **What kept `-rw` off the empty pod is readiness.** `BuildRWService` sets no
  `PublishNotReadyAddresses` ([`service.go:171-185`](../../internal/builder/service.go); only the
  headless Services do, `:154`, `:237`), and the valkey container's readiness probe has
  `InitialDelaySeconds: 5`, `PeriodSeconds: 5`
  ([`statefulset.go:847-858`](../../internal/builder/statefulset.go)). The replacement could not
  be Ready before about 20:50:06, and by then `sentinel-0` named `-0`, so the next poll after
  02.777 had relabelled `-1` `replica`. **`-rw` was most likely empty**, not misrouted, from
  20:50:00 until `-0`'s first poll after 02.777, at most about 20:50:03.8. The sidecar logs that
  would prove this are lost.

**The CR does not show it (read).**
- `MultipleMasters` stayed `False` (lastTransitionTime 20:45:46).
- The condition's one evaluator is the rolling-update resolver
  ([`condition_registry.go:139-150`](../../internal/controller/condition_registry.go); the only
  writers are in [`split_brain_report.go:86-133`](../../internal/controller/split_brain_report.go),
  reached from `resolveSplitBrain` and `resolveSplitBrainUnlessFailingOver`). The health checker
  only logs ([`checker.go:247`](../../internal/health/checker.go)).
- The window was far below the 90 s Warning threshold of ADR 0025 either way.

**Hypothesis, not observed.**
- A client that discovers the master through ~~`sentinel-0` before 20:50:02.777~~ *(corrected
  2026-09-27: any Sentinel before `+promoted-slave` at 20:50:01.628, or a non-leader before
  01.645, or through the `publishNotReadyAddresses` headless name of `-1`)* could have connected
  to the empty `-1` and written there.
- `+convert-to-slave` discards such writes.
- Nothing but the observer writes to these examples, and the observer log of this cluster is
  lost with its pod (replaced at 20:55:00).

**Origin (read).** The Sentinel query block of the data init (`statefulset.go:280-350`) is
unchanged between `v1.12.8` and `v1.13.0`: `git diff -U0` has no hunk there, only at
`:214` and `:622` *(2026-09-27: and from `:830` on)*.

**Verified:**

- **run:** every timeline row above, from the operator log, the Sentinel logs, the sidecar
  logs of `valkey9-0`/`-1`, the pod timestamps, the CR status and annotations, and the
  Sentinel ConfigMap. *(2026-09-27: only the rows up to 20:55:10 are backed by saved evidence,
  quoted above; the Sentinel and sidecar logs and the reads after 20:55:10 were live and cannot
  be re-checked.)*
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

**Re-verified 2026-09-27 (read at `4a7543e`):** every code location in Fact and Options — the
ones that moved are corrected in place; nothing changed in the cited mechanisms since
`f5c6886` (`git diff f5c6886 4a7543e` touches only comments in `rbac.go` and `labeler.go` among
the cited files); controller-runtime is still `v0.25.1` with `defaultSyncPeriod = 10 * time.Hour`
at `pkg/cache/cache.go:45`; `managerOptions` sets no `SyncPeriod`
([`cmd/main.go:102-110`](../../cmd/main.go)); the "Pod watch" premise sentences, by grep.
**Newly found by reading:** ADR 0002 D11's last sentence and the comment at
`valkey_controller.go:2290` ~~misstate~~ *(misstated, until the XS corrections of 2026-09-27)* the
Sentinel arm's source (Fact A, mechanism 1).
**Not verified 2026-09-27:** every **run** fact — the wds18 logs live in an untracked scratchpad
file and were not re-read.

**Re-verified 2026-09-27 at `84a39c2`.**

- **Verified, read:** no code of change 1 or change 2 exists (the stale and terminating
  branches at `steady_state_master.go:245-270` return with no `requestRecheck`; no
  `RWServiceMisrouted`, `instanceRoleChanged` or `findValkeyForPod` in `internal` or `api`;
  `SetupWithManager` at `valkey_controller.go:2984-3002` has `Owns` plus the Secret watch only);
  the two XS corrections are commit `bcc63c9`; since `f5c6886` the cited mechanisms changed only
  in comments. Every location above was re-read; the ones that moved are fixed in their links.
- **Verified, saved evidence re-read:** the passes and events quoted above, from `wds18-op.log`
  and `wds18-events.json` (coverage in the header).
- **Verified, docker and upstream source:** the Sentinel command semantics behind part C and
  decision 8 (Verification, "Measurements of 2026-09-27").
- **Verified, Kind logs:** the leader's `SENTINEL MASTER` `ip` lag of 1.084 s and 5.377 s after a
  drain-forced failover (Verification).
- **Not verified:** the Sentinel timings at 20:50 and every other live-only **run** fact; whether
  `-1` was labelled master at 20:50 (sidecar log lost); the time `Ready` turned `True`; the
  version of Chaos Mesh on wds18 (its grace-period default was read at `master`).

## Impact

- **A: a status field is wrong, for up to the resync period.**
  - Lens and `kubectl get valkey` show the wrong master: 27+ min measured on `valkey9`, up to
    10 h by the resync default.
  - Nothing in the operator reads `status.masterPod` (read: no reader outside assignments;
    2026-09-27: `git grep MasterPod` over `internal`, `cmd` and `api`, tests excluded, finds only
    `statusUnchanged`, [`valkey_controller.go:2586`](../../internal/controller/valkey_controller.go)).
    Anyone who reads the MASTER column to act (connect, debug, fail over by hand) acts on a
    replica.
  - It happens after any non-Sentinel roll whose last StatefulSet event precedes the sidecar
    relabel, which is the ordinary case when pod-0 is Ready before the restore completes.
  - *(Added 2026-09-27.)* Four tracked places tell the reader that `status.masterPod` is the
    live master ("the live answer" / "Read `status.masterPod` for the master"), each next to the
    non-Sentinel-only `TopologyRestored`, which is exactly `valkey9`'s class — Work list.
  - *(Added 2026-09-27.)* It does not reach the four production CRs, which are Sentinel
    clusters (Context).
- **B: a record names a replica.** ~~It is cosmetic under the documented design. The
  annotation and the Sentinel monitor line name a replica, the consumers validate before use,
  and the fallback path is reached only when no Sentinel answers for 30 s.~~ *(corrected
  2026-09-27 at 84a39c2: not every consumer validates. The Sentinel init validates by `ROLE` and a
  scan; the data init's Phase 2 does not. When every Sentinel stays silent for more than 31 s
  while the pod the stale record names is replaced — any later replacement of that pod, not a
  likelier one — the empty pod self-claims the master config. With every Sentinel unreachable,
  each labeler trusts its local role ([`labeler.go:144`](../../internal/sidecar/labeler.go)), so
  once the empty pod is Ready `-rw` carries two master endpoints, and whatever the empty one
  accepted is discarded when the Sentinels return and convert it. Lossy, not cosmetic, and
  unlikely: it needs the whole Sentinel tier silent for over 31 s.)*
- **C: a second, empty master answers for about 5–10 s** after a master is killed, until
  Sentinel converts it.
  - Measured once, direction correct, no data at risk on these examples.
  - The residual risk is a Sentinel-discovering client that writes to the empty pod inside
    the switch window (hypothesis) *(2026-09-27: or a client that uses the pod's headless name;
    `-rw` did not select it, because it was not Ready, Fact C)*. The operator's CR shows none of
    it.
  - *(Added 2026-09-27, inference from upstream code, not measured.)* A master replaced **without**
    a forced failover and answering again before `down-after` (5 s,
    [`sentinel.go:47`](../../internal/builder/sentinel.go)) is never failed over: Sentinel
    re-resolves the hostname on reconnect (valkey 9.1.1 `sentinel.c:2352-2362`), finds a master
    at the new address, and the replicas full-resync from the empty pod — a dataset loss, T36's
    chain through a pod replacement. It needs a death the drain does not act on, which is rare:
    on Sentinel clusters the valkey container has no drain preStop
    ([`statefulset.go:746-749`](../../internal/builder/statefulset.go)) and the drain handler does
    nothing when its first `DetectRole` already fails
    ([`drain.go:105-109`](../../internal/sidecar/drain.go)), but the kubelet still gives every
    container at least 2 s of SIGTERM and valkey-server acts on SIGTERM only in `serverCron`
    (valkey 9.1.1 `src/server.c:7009`, the signal handler only sets `shutdown_asap`; `:1541`,
    `serverCron` acts on it; read 2026-09-27), and a master with lagging replicas then waits up
    to `shutdown-timeout` before it exits (`:4738-4739`), so the drain almost always forces the
    failover first (inference).
- *(Added 2026-09-27.)* **L: after every Sentinel failover the labels trail the promotion by the
  leader's whole `RECONF_REPLICAS` phase**, not by at most one poll. The labeler reads the
  leader's `SENTINEL MASTER` `ip`, which moves only at the leader's `+switch-master` (decision 8).
  Measured: 1.07-1.11 s (docker, empty data), 1.15 s (run, 20:50), 1.084 s and 5.377 s (Kind,
  partial and full resync); bounded by `failover-timeout`, 60 s. After a drain `-rw` is empty for
  that time (6 s measured on Kind). On a roll the outgoing master is alive and Ready and keeps
  confirming itself against the leader, so `-rw` keeps routing writes to it for that time, and
  those writes are discarded at its conversion (inference from code). Every release rolls the four
  production Sentinel CRs. *(Added 2026-09-27, cross-ticket: T34 measured the same `ip` lag at
  1.0 s on Kind with the old master deleted and 3.5-5.8 s in docker with the old master alive and
  a replica to reconfigure - longer than any docker run here, not explained. The loss of the
  outgoing master's acknowledged writes during the roll's own forced failover is its own ticket,
  [T67](067-the-rolls-own-sentinel-failover-loses-acknowledged-writes.md) (formerly T12's
  separate finding and Decision 2; 8633 to 11249 writes per forced failover in docker, its
  option A, `SENTINEL FAILOVER <name> COORDINATED` on Valkey 9, recommended); that stops the
  acknowledging, decision 8 below only shortens the routing, and the two compose.)*
- **Security: none.** No principal gains anything, and no guard is weakened.

## Options

*Rewritten 2026-09-27 at `84a39c2` as the current analysis, one subsection per decision. The
original options of 2026-09-26 and the recommended bundle are in History with the reason each
left this section; the reopen paths of decision 4 (L1, L2, the operator-side stamping) stay
documented under decision 4, as its row requires. Every claim is **read** at `84a39c2` unless labelled **run**, **docker**,
**Kind** or **inference**.*

### The shape of the problem: six records, three writers, three clocks

| Record | Writer | Reader | Moves |
|---|---|---|---|
| `instanceRole` pod label | the pod's own sidecar labeler, a 1 s poll of `INFO replication` that patches on change ([`labeler.go:100-148`](../../internal/sidecar/labeler.go)); the drain handler writes `draining` on the dying master ([`drain.go:121`](../../internal/sidecar/drain.go)) | the `-rw`/`-r` selectors; `listMasterLabeledPods`; `currentMasterPod` rule 1 | ≤ 1 s after the role change on non-Sentinel clusters; on Sentinel clusters after the leader's `+switch-master` plus ≤ 1 s (row "Sentinel", decision 8) |
| `status.masterPod` | the operator, in `updateStatus`: **from the label** on the non-Sentinel arm ([`valkey_controller.go:2252`](../../internal/controller/valkey_controller.go)), **from INFO** on the Sentinel arm ([`:2477`, `:2490`](../../internal/controller/valkey_controller.go)) | humans, Lens, `kubectl get valkey`; nothing in the operator | at the next pass — which after a completed roll may be the 10 h resync (A) |
| `vko.gtrfc.com/known-master` (CR annotation) | the operator, on the promotions it performs (ADR 0008 D3); best-effort at Sentinel roll finalization ([`rolling_update.go:1034-1048`](../../internal/controller/rolling_update.go)) | replica ConfigMap `replicaof`; Sentinel ConfigMap monitor line ([`sentinel.go:103-108`](../../internal/builder/sentinel.go)); the non-Sentinel resolvers | on the next operator promotion; never on a Sentinel failover outside a roll (B) |
| Sentinel's master table | Sentinel | two commands that move at different times: `get-master-addr-by-name` — the data init Phase 1 ([`statefulset.go:297`](../../internal/builder/statefulset.go)); `SENTINEL MASTER` `ip` — the labeler cross-check ([`labeler.go:351-363`](../../internal/sidecar/labeler.go)), the health checker's Sentinel round ([`checker.go:325-341`](../../internal/health/checker.go), B2's source) and the roll's `getSentinelMasterPodName` ([`rolling_update.go:1741`](../../internal/controller/rolling_update.go)) | ~~1–3 s per failover, per Sentinel (**run**: the leader switched 1.1 s after the other two)~~ *(corrected 2026-09-27 at 84a39c2: before `+promoted-slave` every Sentinel names the dead master in both commands. `get-master-addr-by-name`: the leader switches at `+promoted-slave` (7-14 ms after it, docker), the non-leaders at their `+switch-master`, 59-98 ms after `+promoted-slave` (docker). `SENTINEL MASTER` `ip`: the non-leaders at their `+switch-master`, the leader only at its own `+switch-master`, when `RECONF_REPLICAS` ends — every replica's link up, or `failover-timeout` (60 s): 1.07-1.11 s docker, 1.15 s run, 1.08 s and 5.38 s Kind)* |
| replica ConfigMap `replicaof` | the operator, from the annotation | data init Phase 2: the record on non-Sentinel clusters, the unguarded fallback after 31 s of Sentinel silence otherwise ([`statefulset.go:318-333`](../../internal/builder/statefulset.go)) | with the annotation, at the start of a pass ([`valkey_controller.go:552-560`](../../internal/controller/valkey_controller.go)) |
| Sentinel ConfigMap monitor line | the operator, from the annotation | Sentinel init, validated by `ROLE` and a pod scan ([`sentinel.go:654-692`](../../internal/builder/sentinel.go)) | with the annotation, at the start of a pass |

Two consequences:

- **`status.masterPod` has two meanings.** On the non-Sentinel arm it is "the pod the `-rw`
  selector names" — the documented rule of `currentMasterPod`. On the Sentinel arm it is
  "the pod that answers `role:master`", and the label is never read. That is why B's status
  was right while A's was wrong, and it is a precision defect in its own right: a field a
  human reads must not change its meaning with `spec.sentinel.enabled`.
- **The operator learns of a role change only through a side effect.** A pod's death moves
  the StatefulSet status and `Owns(StatefulSet)` — no predicate — re-enters the pass. A
  relabel alone moves nothing the operator watches. Five role changes have no side effect:
  the relabel that closes a topology restoration (A), a human's `SENTINEL FAILOVER`, a
  human's `REPLICAOF`, the labeler's cross-check flip on a Sentinel cluster, and *(added
  2026-09-27, inference)* Sentinel's own failover of a master that stalls past `down-after`
  without dying (CPU starvation, for example). All five leave `status.masterPod` to the resync.

### The rule the options serve

> A role change reaches the operator as an event, and every record the operator owns is
> rewritten in the pass that follows. Where the operator causes the change, it records it in
> the same pass. ~~Where a pod decides its role at boot, it decides on a settled majority,
> never on the first voice.~~ *(corrected 2026-09-27 at 84a39c2: the first voice is not what
> failed — before `+promoted-slave` every voice names the dead master. The boot half reads:)*
> An empty pod does not take the master role while Sentinel still knows a replica that may
> hold the data.

None of the options makes the operator a writer of the `instanceRole` label (ADR 0012 D1,
D12).

### Decision 1 (re-decision proposed) — does A1's recheck stay, now that A4 ships in the same change?

**Mechanism.** Today the pass that proves the sole master label stale
([`steady_state_master.go:261-267`](../../internal/controller/steady_state_master.go)) or
terminating (`:249-253`) logs and returns; the completing pass then writes the labelled pod
into `status.masterPod` ([`valkey_controller.go:2252`, `:2324-2342`](../../internal/controller/valkey_controller.go)),
and no event re-enters the CR (Fact A, mechanism 3). Decision 1 (2026-09-27) chose **A1 + A2**:

- **A1**: both branches call `requestRecheck(ctx, rollingUpdateRequeueDelay)` (10 s,
  [`rolling_update.go:203`](../../internal/controller/rolling_update.go)), folded into the result
  by `applyRecheck` on the error-free return
  ([`valkey_controller.go:310`](../../internal/controller/valkey_controller.go),
  [`foreign_object.go:132-146`](../../internal/controller/foreign_object.go)); a blocked pass
  returns an error instead (`:286-297`), which is its own retry. `recordSentinelPeerDrift`
  already requests one from the same status arm (`:2397`). The delay only has to exceed the
  sidecar poll (1 s); ADR 0011 D14's 15 s is not owed here, because it protects a *demotion
  verdict* from a label set that has not caught up, and this recheck only re-reads the label and
  rewrites a status field.
- **A2**: `adoptUnrecordedPromotion` records the proven-stale pod name on the pass state
  (`passState`, [`foreign_object.go:89-146`](../../internal/controller/foreign_object.go),
  `requestRecheck` at `:126`, `applyRecheck` at `:138`), and `currentMasterPod` skips rule 1
  when its one labelled pod is that name, falling to the record, which after
  `promotePod0AndRedirect` names pod-0 (`recordPromotedMaster` runs before the redirect,
  [`rolling_update.go:4641`](../../internal/controller/rolling_update.go); saved log line 1145
  shows `knownMaster valkey9-0` in the completing pass). The status is then right in the pass
  that completes the roll (**run**: at 20:45:43, where A1 alone would give no earlier than
  20:45:53), and the field has one meaning on both topologies: for the ≤ 1 s of flux the
  non-Sentinel arm reports *the master* rather than *the pod the `-rw` selector names*, which is
  what the Sentinel arm has always reported; the routing lag becomes decision 6's business. A2 amends
  [ADR 0002 D11](../adr/0002-surface-a-blocked-reconcile-on-the-cr.md) (`:331-346`), which states
  the rule it changes. A2 leaves a label and a record that are both stale on the same pod
  (the function returns before probing when they agree, `:237`) — a dead sidecar, decision 6.

Decision 5 then put **A4** (a Pod watch on `instanceRole` changes) into the same change. A4
enqueues the CR on every change of the label's value, so the relabel that settles the label
triggers a pass within the API round trip — sooner than A1's 10 s — and a relabel that lands
while the completing pass runs re-queues the key. The terminating branch already gets an
`Owns(StatefulSet)` event when the pod goes. A missed event is not a real gap: after a watch
gap, a relist delivers the changed pod as an Update from the cached old object to the new one,
which the predicate compares, and a pod recreated inside the gap moves the StatefulSet status.
What A1 still adds under A4: when the label never settles, the stale branch runs on every pass
(`:236-267`) and re-requests, so the cluster is probed with `INFO` (`:255`) every 10 s per CR
with no end — the polling [ADR 0011 D12](../adr/0011-evidence-based-steady-state-split-brain-resolution.md)
(`:210-216`) and its Alternatives entry (`:421`, "on a stale label the operator has no fix to
poll for") refuse. That case needs a sidecar that is alive but not relabelling (for example
one whose patch is refused); a crashed sidecar makes the pod not Ready, so `-rw` drops it and
the case is `RWServiceEmpty`'s, not a misroute. Decision 6's `RWServiceMisrouted` reports the
misroute case. The choice does not change A2 or A4.

- **Keep A1 + A2 as decided.** A second trigger if a watch event were missed. Cost: XS, plus
  amending ADR 0011 D12's last sentence and its Alternatives entry. Consequence: a pass A4
  already delivers sooner, and with a label that never settles an `INFO` probe every 10 s per
  CR with no end.
- **Drop A1; ship A2 + A4 (recommended).** The completing pass writes the record's pod (A2),
  the relabel event re-enters the pass (A4), a label that never settles is reported (decision
  6), not polled. Cost: two calls, two unit tests and one ADR amendment fewer than decided.
  Consequence: ADR 0011 D12 stays as written.

**Why the mark.** A4, decided for the same change, delivers the settling relabel as an event
within the API round trip, strictly sooner than A1's 10 s, and A2 already puts the right value
into the completing pass. A1 is left with only the label that never settles, where it becomes
the unbounded poll ADR 0011 D12 was written to refuse, while decision 6 already reports that
case. The runner-up buys protection only against a lost informer update, which the relist
mechanism does not produce. This re-decides the owner's decision 1; it stands as taken until
Hans re-decides.

### Decision 2 (decided: B2) — the `known-master` record on Sentinel clusters

**Mechanism.** On the Sentinel path the record moves only with operator promotions and roll
finalization (Fact B). Its readers are the Sentinel ConfigMap monitor line, validated by the
Sentinel init, and the replica ConfigMap `replicaof`, which the Sentinel data init's Phase 2
adopts without a guard (Fact B, Impact B). The Sentinel status arm already holds the INFO master
(`clusterState.MasterPod`, [`checker.go:115`](../../internal/health/checker.go)) and asks every
Sentinel `SENTINEL MASTER` for the peer-table check
([`checker.go:325-341`](../../internal/health/checker.go); the reply carries `IP`, `NumSlaves`
and `Flags`, [`client.go:36-50`](../../internal/valkeyclient/client.go), parser `:631-652`),
keeping only the peer counts and an `agreeing` count today.

**B2, as decided.** The round keeps the master name each Sentinel reports. When a majority of
the answering Sentinels names pod P, the INFO master is P, P is one of the StatefulSet's
ordinals, and the annotation — or, unrecorded, the pod-0 default both ConfigMaps fall back to —
names another pod, the pass calls `persistKnownMaster(P)`
([`rolling_update.go:1071-1089`](../../internal/controller/rolling_update.go)): the existing
writer, a no-op when unchanged, a plain `Update` (`:1080`) otherwise, the in-memory value
restored on failure; on this path the error is logged, never returned (Sentinel stays the
authority). Two independent sources must agree, so the leader alone — whose `ip` trails for its
whole `RECONF_REPLICAS` phase — cannot move the record.

- **Placement is load-bearing.** `Update` decodes the server's response into `v`, status
  included, so a write between the `prevStatus` capture (`valkey_controller.go:2451`) and
  `persistStatus` (`:2528`) would discard the conditions the pass set in memory
  (`RWServiceEmpty`, `SentinelPeersStale`) for that pass. `CheckCluster` (`:2461`) returns the
  agreed name as data; the write runs **after** `persistStatus`, on the resourceVersion that
  write returned. Every existing `persistKnownMaster` caller runs before the capture.
- **Sentinel path only.** On non-Sentinel clusters the annotation is the demotion authority and
  moves only on evidence (ADR 0011 D4–D7). On Sentinel clusters it is never one —
  `checkSteadyStateSplitBrain` skips them
  ([`steady_state_master.go:154`](../../internal/controller/steady_state_master.go)) and the roll
  reads Sentinel (ADR 0008 D10) — so the refresh causes no `REPLICAOF`.
- *(Added 2026-09-27, needs no decision.)* **The refresh must be followed by the pass that
  republishes both ConfigMaps.** They are the record's only readers, and they are rebuilt only
  at the start of a pass ([`valkey_controller.go:552-560`](../../internal/controller/valkey_controller.go)).
  A CR annotation write is filtered by `GenerationChangedPredicate` (`:2987`), and a healthy HA
  pass returns no requeue (`:393-396`), so a refresh alone leaves both readers stale until the
  next event or the resync. After a successful write the pass calls `requestRecheck` with a short
  delay, as `finishDataRoll` does
  ([`rolling_update.go:394-396`](../../internal/controller/rolling_update.go)). That goes through
  `reconcileReplicaConfigMap` and the Sentinel ConfigMap step, which already carry the ADR 0020
  ownership proof and conflict handling; a direct republish from inside `updateStatus` would
  duplicate that write path for no gain.
- **Trigger:** the pass after the failover. A failover that kills the master already gets one
  (**run**: phase `Error` at 20:50:00, then the 10 s requeue of
  [`valkey_controller.go:377-379`](../../internal/controller/valkey_controller.go) until the
  healthy pass at ~~20:50:12~~ 20:50:17 *(corrected 2026-09-27, Fact C)*); a failover that kills
  no pod gets one only through A4.
- **Cost:** S. One CR update plus two ConfigMap updates per Sentinel failover outside a roll;
  both ConfigMaps exclude the address from their hash, so no pod rolls. ADR 0008 D3 gains the
  steady-state writer. Impact B's correction strengthens the choice: the record is lossy through
  Phase 2, not cosmetic.

### Decision 3 (re-decision proposed) — how does a replacement data pod on a Sentinel cluster decide to boot as master?

**Mechanism.** The Sentinel data init
([`statefulset.go:280-350`](../../internal/builder/statefulset.go)) asks the Sentinels in
ordinal order with `SENTINEL get-master-addr-by-name` (`:297`) and takes the first non-error
answer (`break 2`, `:308`). An answer naming the pod itself selects the master config
(`:331-333`); no answer for 31 s leads to Phase 2, the record (`:318-326`). Before
`+promoted-slave` every Sentinel names the dead master; from `+promoted-slave` on the leader
names the promoted replica (Fact C). Measured on both pins (docker): `+try-failover` to
`+promoted-slave` took 1.07 s (9.1.1) and 1.10 s (8.1.9); throughout, the non-leaders showed
flags `master` or `master,disconnected` and no failover flag; only the leader carried
`failover_in_progress`. On wds18 the force-deleted master's replacement ran its init at
20:50:01, before `+promoted-slave` at 01.628 (Fact C). A death the drain does not act on gives
the same shape with no failover flag anywhere until `down-after` (5 s), and worse: a
replacement that answers before `down-after` is never failed over (Impact C).

The choice changes whether an empty replacement that Sentinel names as master boots as master.
It does not change Phase 2 (the record, decision 2). Any change to the script changes the
pod-spec hash (`ComputePodSpecHash`, FNV over the whole built PodSpec,
[`statefulset.go:1223-1238`](../../internal/builder/statefulset.go)), so it rolls every Sentinel
data tier; on the Helm path that rides the roll every release already makes through the sidecar
image ([ADR 0005 D11](../adr/0005-upgrade-neutral-defaults-and-anti-affinity.md), `:297-301`).
Decision 3 as taken (2026-09-27: reproduce first, then C1 + C2, C3 as fallback) rests on the
false premise corrected in Fact C: C1 and C2 as specified close nothing of the measured shape,
and its planned `--grace-period=0` run does not reach Sentinel's `down-after` path
(Verification).

- **C4 — an empty pod named master while Sentinel knows replicas waits for a peer
  (recommended).** In Phase 1, a pod whose `/data` holds no dataset and that Sentinel names as
  master does not take the master config while the `SENTINEL MASTER` reply reports
  `num-slaves > 0`. It re-asks until a Sentinel names another pod, then boots as that pod's
  replica; past a bound, today's behaviour applies. The name comes from
  `get-master-addr-by-name`, which switches first on the leader; `SENTINEL MASTER` is read only
  for `num-slaves`, because its `ip` trails for the leader's whole `RECONF_REPLICAS` phase and
  would keep the pod waiting whenever only `sentinel-0` answers. While the pod waits nothing
  answers on its address, so Sentinel fails over on `down-after` in the drain-less shape.
  - Cost: M. Generated shell in the Sentinel init (`awk` is already declared,
    [`image_requirements.go:45`](../../internal/builder/image_requirements.go)); a builder unit
    test *(precised 2026-09-27, cross-ticket from T41: an ADR 0017 D19 exec harness for the
    Sentinel branch of the data init, which no test executes today)*; a docker test in the image-tools tier that runs the generated script against a Sentinel
    tier — which is new scope for that tier, since it does not run the config-writer scripts
    today (CLAUDE.md, the Valkey image section), to be checked against ADR 0017 — and that must
    hold the failover inside the ~1.1 s pre-promotion window to be deterministic (for example by
    delaying promotion); a force-delete e2e on both Valkey lines; one ADR.
  - Consequences: a replacement of a killed master boots about 1-2 s later (forced failover) or
    about 6-8 s later (drain-less, `down-after` plus promotion). Persistent pods with data are
    unaffected. **A cold start is unaffected only while the Sentinel tier is fresh**: Sentinel
    never forgets a replica ([ADR 0022](../adr/0022-sentinel-identity-is-pinned-to-the-pod.md)),
    so when the whole data tier is replaced while the Sentinels survive (all data pods on one
    lost node, for example, since anti-affinity is off by default), `num-slaves > 0` on every
    Sentinel, the empty named pod waits, and the tier either promotes an equally empty replica
    after `down-after` (about 5-10 s more, inference) or pays the bound. Harmless, but a cost. A
    tier that cannot fail over waits out the bound and then boots as today.
  - The bound is a design parameter. It must exceed `down-after` plus a promotion, and a
    failover can take up to `failover-timeout` (60 s,
    [`sentinel.go:50`](../../internal/builder/sentinel.go)); past it, a slow failover falls to
    today's behaviour. C2' carries the same parameter, so it does not separate the two.
  - Relation to [T36](036-non-persistent-master-restarts-empty.md): compatible with T36's
    recommended Sentinel-half start guard, not identical. T36's guard runs in the valkey
    container command on a restart, keys on a restart marker and a config with no `replicaof`,
    and has no `num-slaves` condition; C4 runs in the init, keys on an empty `/data`, and adds
    `num-slaves > 0` (which also answers T36's own open case of a fresh cluster whose replicas
    never connected). One shared shell function needs one condition set, and it belongs in one
    ADR ("an empty data pod does not take the master role while Sentinel knows a replica", with
    its bound), decided once for both tickets and cited by both.
- **C2' — a failover in progress anywhere stops the decision.** The init asks every Sentinel with
  `SENTINEL MASTER`; while any answer carries `failover_in_progress`, it decides nothing and
  retries the round; then it takes the address from `get-master-addr-by-name`. It covers the
  forced shape from Sentinel's own state: the roll trigger
  ([`rolling_update.go:3713-3736`](../../internal/controller/rolling_update.go)) and the drain
  ([`drain.go:135-148`](../../internal/sidecar/drain.go)) both force the failover through
  `sentinel-0` first, which then leads it, and the leader carries the flag from `+try-failover`
  (`sentinel.c` 9.1.1 `:4939-4948`) until `+switch-master` or an abort (`:5359-5370`). Cost: M,
  the same test set as C4. Consequences: the drain-less shape stays open, because no Sentinel
  shows any flag before `down-after`; and the leader keeps the flag up to `failover-timeout`,
  longer than Phase 1's 31 s, so unless the bound is raised a slow failover falls into Phase 2's
  unguarded self-claim on the stale record. *(Carried over from the C2 of 2026-09-26, still
  valid for C2':)* `s_down`/`o_down` must not block a reply that names the booting pod itself —
  on a cold start Sentinel names pod-0 from the monitor line and flags it down because pod-0 is
  the pod that is booting; and a cold start can still meet a `failover_in_progress` window
  (Sentinel tries and aborts a failover when no replica is good, then waits
  `failover-timeout` × 2 before the next try, `sentinel.c` 9.1.1 `:4969`), a cost of a few
  seconds, bounded by the retry loop (inference, not measured). The Go side already parses
  `flags` (`SentinelMasterInfo`, [`client.go:36-50`](../../internal/valkeyclient/client.go)); the
  shell side needs an `awk` over the flat key/value reply.

**Why the mark.** C4 is the only option that covers both the measured forced-failover shape (the
replacement started inside the ~1.2 s in which every Sentinel named it) and the drain-less shape,
where no Sentinel shows any flag and a fast replacement is never failed over at all. It costs
nothing on a settled tier, reads no flags, and is the same shape of rule T36 recommends for the
restart path, so one ADR serves both tickets. C2' is the runner-up: it reads a flag only the
leader carries, so it leaves the drain-less shape open. The drain-less shape is rare in
production (Impact C), so C4's lead rests on one mechanism for both shapes and for T36, not on
frequency. The reproduction decision 3 asked for becomes C4's test. This re-decides the owner's
decision 3; it stands as taken until Hans re-decides.

### Decision 4 (decided: nothing) — the labeler's poll

**Mechanism.** The labeler polls `INFO replication` every second
([`statefulset.go:916`](../../internal/builder/statefulset.go), pinned by
[`statefulset_test.go:782`](../../internal/builder/statefulset_test.go)) and patches on change.
The poll's contribution to every handover is at most 1 s, and that figure still holds. What does
not hold is the Sentinel row of the table the decision was weighed on:

| Handover | Who moves the label | Window on `-rw` |
|---|---|---|
| roll, non-Sentinel (`promoteAndRedirect`: `REPLICAOF NO ONE`, then the outgoing master demoted, ADR 0012 D9) | both pods' labelers, each on its next poll | ≤ 1 s with the outgoing master still selected, or both pods, or neither |
| drain, non-Sentinel | the dying master writes `draining` synchronously ([`drain.go:121`](../../internal/sidecar/drain.go)); the promoted peer's labeler on its next poll | ≤ 1 s empty — writes fail fast; direction safe |
| Sentinel failover | the promoted pod's labeler plus cross-check; the old master's labeler after `+convert-to-slave` | ~~≤ 1 s empty, then ≤ 1 s with the old master still selected when it is alive (a manual failover)~~ *(corrected 2026-09-27 at 84a39c2: the leader's `RECONF_REPLICAS` phase plus ≤ 1 s of poll after the promotion — 1.07-1.15 s measured with empty data or a partial resync, 5.38 s with a full resync (Kind), bounded by 60 s — empty after a drain, and with the old master still selected when it is alive (a roll or a manual failover); the poll part is decision 4, the leader part is decision 8)* |

Decision 4 took "nothing": the 1 s poll stays. The options weighed were L1 (a 250 ms poll on
multi-replica templates only — a standalone keeps `1s`, because `isSidecarOnlyChange` compares
images and never args,
[`rolling_update.go:3845`](../../internal/controller/rolling_update.go), so an args-only drift
would replace a single non-persistent pod with its dataset; `singlePodDeferral`,
[`pod_security_migration.go:128`](../../internal/controller/pod_security_migration.go)) and L2
(the non-Sentinel drain handler labels the peer it promoted,
[`drain.go:172-179`](../../internal/sidecar/drain.go), under the sidecar Role's `get, patch`,
[`rbac.go:73-74`](../../internal/builder/rbac.go)). Both remain valid as analysed and compose
with decision 8; the instrument that would reopen decision 4 is the observer's write test
(`writeTestOK`, [`observer/metrics.go:36`](../../internal/observer/metrics.go),
`--poll-interval=2s` at [`observer.go:161`](../../internal/builder/observer.go)), whose failure
rate over rolls is the handover write outage as a client sees it. Decision 4 stays closed on the
poll; the leader lag is the separate decision 8.

*Documented for reopening decision 4, as its row requires (restored by the review of 2026-09-27
from the options of 2026-09-26; not open options):*

- **L1 — a shorter poll**, `--poll-interval=250ms` (or 500 ms), multi-replica templates only.
  The mean poll lag drops from 500 ms to 125 ms and the bound from 1 s to 250 ms. Cost: four
  loopback `INFO replication` per second per pod, on a TLS cluster each a fresh dial and handshake
  by design (ADR 0030 D2), plus four `SENTINEL MASTER` per second from the master of a Sentinel
  cluster — expected negligible, to measure once on Kind before the default moves. It changes the
  pod-spec hash: one data-tier roll on the release, which ADR 0005 D11 already names as the
  baseline; Sentinel pods carry no labeler; ADR 0011 D14 keeps its 15 s margin above the poll.
  The standalone template keeps `1s` for the reason above. The labeler logs "failed to detect
  role" on every poll while Valkey is not up, so four lines per second instead of one in those
  windows — accept, or log once per state change.
- **L2 — the non-Sentinel drain handler labels the peer it promoted**: `PatchLabel(peer,
  instanceRole, master)` after `REPLICAOF NO ONE` and `stampPromotion`, best-effort like the
  stamp, under the existing `get, patch` grant (ADR 0012 D8 step 3). The peer's own labeler writes
  the same value on its next poll; both derive it from `ROLE`, so they cannot disagree.
  Non-Sentinel only, the drain's scope (ADR 0012 D10). It closes the drain's empty window to the
  API round trip; ADR 0012 D6 and D12 gain one clause each ("and the drain handler of the pod that
  promoted it"); under A4 each such write costs one pass that reads converged labels and writes
  nothing.
- **The operator stamps the labels of the pods it promotes and demotes in a roll** (in
  `promoteAndRedirect`, both pods ownership-proven, non-Sentinel only). Safe by construction: it
  derives from the same `ROLE` the sidecar reads, and the operator never promotes a terminating
  pod (`available()`, ADR 0026), so it never touches a `draining` label (ADR 0012 D6). It is the
  only option that can *sequence* the labels with the commands — the outgoing master labelled
  `replica` before its `REPLICAOF`, the promoted pod `master` right after its `REPLICAOF NO ONE` —
  so a planned handover's window would fall to the API round trip. It reopens ADR 0012 D12
  ("writing the label from the controller would reintroduce the second writer D1 exists to
  prevent"), which would have to be rewritten as "the operator repairs no label" rather than
  "writes none". The trigger that would justify it: the observer's write-test failure rate over
  rolls showing that the last 250 ms of a planned handover matter.
- **L3** (a subscription to `+switch-master` over Sentinel pub/sub) was never proposed: a
  subscriber with reconnect and per-dial TLS material for what L1 leaves.

### Decision 5 (decided: A4) — a Pod watch on `instanceRole` changes

**Spec.** `Watches(&corev1.Pod{}, EnqueueRequestsFromMapFunc(findValkeyForPod),
WithPredicates(instanceRoleChanged))` next to the Secret watch
([`valkey_controller.go:2997-3000`](../../internal/controller/valkey_controller.go)): Update
events only, fired when the label's value differs between the old and the new object, mapped to
the CR by the `vko.gtrfc.com/cluster` label
([`labels.go:24-25`](../../internal/common/labels.go), `BaseLabels` `:82-91`) and the namespace.
The Pod informer already runs cluster-wide — every pass lists pods through the cached client
([`steady_state_master.go:195-200`](../../internal/controller/steady_state_master.go)), and
`managerOptions` restricts nothing ([`cmd/main.go:102-110`](../../cmd/main.go)) — so the watch
adds an event handler and no memory. The pod template carries no `instanceRole` (`PodLabels`,
`labels.go:95`, has no production caller), so the sidecar's first patch is the first event.
`pods: get, list, watch, patch, delete` are granted in both RBAC sources
([`config/rbac/role.yaml:24-30`](../../config/rbac/role.yaml),
[`clusterrole.yaml:50-59`](../../deploy/helm/valkey-operator/templates/clusterrole.yaml)).

- **The document sweep.** Thirteen tracked places outside `docs/tickets/` state the no-Pod-watch
  premise, found at `84a39c2` by `grep -rnE "Pod watch|no Pod *$" internal docs CLAUDE.md
  DEVELOPER.md` (14 hits: these thirteen and the heading of the rejected alternative at ADR 0011
  `:417`, which stays):
  [`steady_state_master.go:668-669`](../../internal/controller/steady_state_master.go),
  [`rolling_update.go:355-356`](../../internal/controller/rolling_update.go),
  [`sidecar_pending_condition_test.go:109`](../../internal/controller/sidecar_pending_condition_test.go),
  [`steady_state_master_test.go:1301-1302`](../../internal/controller/steady_state_master_test.go),
  [`pod_security_migration_test.go:513-514`](../../internal/controller/pod_security_migration_test.go),
  [ADR 0001:58](../adr/0001-continue-reconciling-past-a-rejected-write.md),
  [ADR 0002:240](../adr/0002-surface-a-blocked-reconcile-on-the-cr.md),
  [ADR 0011:222 (D13) and :306 (D21)](../adr/0011-evidence-based-steady-state-split-brain-resolution.md),
  [ADR 0031:185](../adr/0031-a-record-the-operator-trusts-lives-in-pod-spec.md),
  [ADR 0032:316](../adr/0032-generated-pods-run-rootless.md), `CLAUDE.md:902` and
  [`docs/security/rootless-migration.md:55`](../security/rootless-migration.md). D21 rejected
  the watch "as unnecessary once the evidence became durable"; that reasoning stands — A4 adds no
  evidence and changes no evidence rule — and it never weighed the timeliness of the operator's
  own records, so D21's amendment adds a requirement rather than overturning one. The other
  twelve need one clause each. A line break elsewhere in the phrase can still hide one; each hit
  is read. The sentences that say the next *guaranteed* pass is the cache resync stay true: the
  watch fires on a change, it guarantees no pass.
- **Pass volume:** one pass per relabel. `Owns(StatefulSet)` carries no predicate, so every
  readiness flip already re-enters, and every role change that kills a pod comes with one; the
  wds18 chaos schedule adds about two relabels per kill. ADR 0019 D3 is untouched: no fleet-wide
  state. ADR 0031:185 calls it "a Pod watch this operator deliberately does not have" inside an
  argument about a `pods/status` grant; that clause carries no reason of its own, and no grant is
  proposed here.
- **Security, `hardening` class, not `boundary`:** the map function trusts a label, so whoever
  can patch pod labels in a namespace can enqueue passes for a CR of that namespace — and the
  sidecar's own `resourceNames`-bound `patch` grant
  ([`rbac.go:73-74`](../../internal/builder/rbac.go)) already can, on the very label the
  predicate watches. The pass it triggers writes nothing it would not write anyway: every write
  is ownership-proven (ADR 0020), the work queue deduplicates per CR, the rate limiter backs off.
- **No loop:** the operator never writes `instanceRole` (ADR 0012 D1), so the watch cannot feed
  itself.
- **Cost:** S in code (one map function, one predicate, two unit tests), M in documents.

### Decision 6 (decided: A3 as `RWServiceMisrouted`) — a misrouted `-rw` selection on the CR

**Spec.** A new level `RWServiceMisrouted` next to `RWServiceEmpty`
([`rw_service_report.go:37`](../../internal/controller/rw_service_report.go); registry
[`condition_registry.go:235-246`](../../internal/controller/condition_registry.go); the
`ConditionType` next to [`valkey_types.go:241-260`](../../api/v1/valkey_types.go)), with its own
registry row, the three documentation places a condition has
([`README.md:522`](../../README.md), [`docs/operations/status.md:71`](../operations/status.md#rwserviceempty),
[`docs/developer/package-map.md:49`](../developer/package-map.md)) and one sentence in
[ADR 0012 D12](../adr/0012-the-sidecar-records-its-drain-promotion-on-the-pod.md) (`:414`),
presence-guarded like its sibling. The brief `True` inside every handover is the flicker the
level accepts (ADR 0025's `MultipleMasters` precedent).

- *(corrected 2026-09-27 at 84a39c2, needs no decision.)* ~~Same evaluator as `RWServiceEmpty`
  (`reportRWServiceEndpoints`), judged from answers the pass already holds, so no new
  connection.~~ `reportRWServiceEndpoints` runs at
  [`valkey_controller.go:2455`](../../internal/controller/valkey_controller.go), **before**
  `CheckCluster` (`:2461`), and `clusterState` exists only in the all-ready case
  (`:2458-2501`). On the Sentinel arm the judgement therefore runs after `CheckCluster`, inside
  the all-ready case, and neither judges nor clears without a `clusterState`. It counts any
  master-labelled pod other than `clusterState.MasterPod`, not only a sole one: a stale extra
  label on a Sentinel cluster is the more plausible dead-sidecar shape. On the non-Sentinel arm
  the verdict exists only when label and record disagree
  ([`steady_state_master.go:236-242`](../../internal/controller/steady_state_master.go)) — the
  limit A2 leaves, stated in the ADR sentence.

### Decision 7 (decided) — the crash-restart adjacent finding

Its own ticket, [T36](036-non-persistent-master-restarts-empty.md). Nothing open here; the
relation to decision 3 is in its C4 option.

### Decision 8 (new, open) — which Sentinel answer the labeler's cross-check reads

**Mechanism.** The labeler confirms a local master only when Sentinel names the pod
([`labeler.go:135-145`](../../internal/sidecar/labeler.go)). It reads `SENTINEL MASTER` `ip`
from the first Sentinel that answers ([`labeler.go:351-363`](../../internal/sidecar/labeler.go);
`GetMasterAddress`'s only production caller is `labeler.go:138`), in the order `sentinel-0,1,2`.
That field is `ri->addr` (valkey 9.1.1 `sentinel.c:3324-3325`), which on the failover leader
stays the old master until its `+switch-master`, at the end of `RECONF_REPLICAS`: only when every
replica reports `master_link_status` up towards the promoted pod (`:2684-2689`), or at
`failover-timeout`. A replica that needs a full resync holds that open: `repl-diskless-sync-delay
5` alone adds 5 s ([`configmap.go:178-179`](../../internal/builder/configmap.go)), plus the
transfer. `sentinel-0` leads every roll- and drain-forced failover
([`rolling_update.go:3713-3736`](../../internal/controller/rolling_update.go),
[`drain.go:135-148`](../../internal/sidecar/drain.go)). Measured: 1.07-1.11 s (docker), 1.15 s
(run), 1.084 s and 5.377 s (Kind; in the second run the promoted `sc-drain-1` logged "Sentinel
disagrees, labeling as replica" every second from 13:58:29.96 to 13:58:34.97 and relabelled
master at 13:58:35.96). Consequences: after every such failover the promoted pod stays labelled
`replica`, and an old master that is still alive stays labelled `master`, for that phase plus the
poll; `-rw` is empty after a drain and routed to the outgoing master on a roll, whose writes are
discarded at its conversion (inference). The choice changes only which Sentinel command the
cross-check sends. It does not change the poll (decision 4), does not make the operator a label
writer (ADR 0012 D1, D12), and does not fence the outgoing master's writes (*precised
2026-09-27, cross-ticket: owned by [T67](067-the-rolls-own-sentinel-failover-loses-acknowledged-writes.md)
for the roll's own failover, recommended A, the coordinated failover, and decided before
T12's Decision 1, the `min-replicas-to-write` field, which is recommended refused*). The sidecar
change rides the sidecar-image roll every release already makes (ADR 0005 D11).

- **L4 — the cross-check reads `SENTINEL get-master-addr-by-name` (recommended).** The leader
  answers the promoted pod from `+promoted-slave` on (7-14 ms after it, docker), the non-leaders
  at their `+switch-master` (59-98 ms after `+promoted-slave`, docker), so the answer converges the moment the
  failover can no longer be aborted — the leader names the promoted replica only after that
  replica has reported role master. The local-master precondition
  ([`labeler.go:137`](../../internal/sidecar/labeler.go)) means L4 never confirms a pod before its
  own `INFO` says master. **Implementation requirement:** `get-master-addr-by-name` answers a null
  array for an unknown monitor where `SENTINEL MASTER` answers an error
  (valkey 9.1.1 `sentinel.c:3905-3918`); the querier must treat a null or empty answer as "this
  Sentinel did not answer" and try the next one, or `"" != myFQDN` would label a real master
  `replica` and empty `-rw`. Cost: XS, one command and parser in the sidecar querier, one unit
  test for the switch and one for the null answer. Consequences: no rule change and no extra
  roll; the pre-promotion window, in which every Sentinel names the dead master, remains (it is
  decision 3's).
- **Nothing.** Keep `SENTINEL MASTER` `ip`. No code. Consequence: the leader lag above remains on
  every Sentinel handover, including every release roll of the four production Sentinel CRs.

**Why the mark.** Measured (docker on both pins with empty data, Kind on 9.1.1), the leader's
`SENTINEL MASTER` `ip` trails its own `get-master-addr-by-name` by its whole `RECONF_REPLICAS`
phase — about 1.1 s with empty data or a partial resync, 5.4 s with a full one (Kind only),
bounded only by 60 s by reading — and `sentinel-0`, which the labeler asks first, leads
every failover the operator or the drain forces. L4 removes that window from every such handover
for one command in the sidecar, changes no rule and adds no roll. "Nothing" keeps the loss for no
saving. Part C gives no argument for L4: the empty replacement there was never Ready while it was
labelled master (Fact C).

Whether `MultipleMasters` should be measured on Sentinel clusters outside a roll — the health
checker saw C and only logged ([`checker.go:247`](../../internal/health/checker.go)) — stays the
separate ADR 0025 question the Fact section names.

## Decision

Eight decisions, taken one at a time on Hans's rule of 2026-09-26; this table is the one
current decision of the ticket. A row moves to *decided* with its date and the chosen option;
the weighing that carried the mark is in Options. *(2026-09-27 at 84a39c2: rows 1 and 3 carry a
re-decision proposal and row 8 is new; each stands as written until Hans decides.)*

| # | Decision | Chosen | Date |
|---|---|---|---|
| 1 | `status.masterPod` after a non-Sentinel roll | **A1 + A2** — recheck in the two proving branches, `rollingUpdateRequeueDelay` reused (no new constant), and rule 1 yields to rule 2 in the pass that proved the label stale; one meaning for the field on both topologies. *(2026-09-27 at 84a39c2: re-decision proposed — drop A1 under decision 5, Options decision 1.)* | 2026-09-27 |
| 2 | the `known-master` record on Sentinel clusters | **B2** — refreshed when a majority of the answering Sentinels and INFO name the same ordinal and the record (or its pod-0 default) names another; Sentinel path only; the write after `persistStatus`, logged on failure | 2026-09-27 |
| 3 | the replacement pod's boot decision | **reproduce first, then C1 + C2**, C3 only as fallback — the reproduction on `TestE2E_SidecarFailoverDrainMaster` (already a Sentinel cluster whose master is deleted) plus a `--grace-period=0` run, replacement named by UID, its init and sidecar logs captured (a log helper is needed); C1 as *majority of the answering Sentinels*, so one answering Sentinel is today's behaviour and a degraded tier does not wait 30 s. *(2026-09-27 at 84a39c2: its premise — one lagging Sentinel — is false, Fact C; re-decision proposed, C4, Options decision 3.)* | 2026-09-27 |
| 4 | the label handover | **nothing** — the 1 s poll stays; L1, L2 and the operator-side stamping stay documented in Options with the observer's write-test rate as the instrument that would reopen this. *(2026-09-27: the poll figure holds; the Sentinel leader lag it did not see is decision 8.)* | 2026-09-27 |
| 5 | a Pod watch on `instanceRole` changes | **A4 now**, in the same change as decisions 1 and 2 — Update events on a changed `instanceRole` value only, mapped by the cluster label; ADR 0011 D21 amended (its reasoning stands, timeliness added as the requirement it never weighed), the ~~other eight~~ *(corrected 2026-09-27: other twelve)* "no Pod watch" sentences given one clause each; the full e2e suite on both legs is the proof for the extra passes inside a roll | 2026-09-27 |
| 6 | a misrouted `-rw` selection reported on the CR | **A3 as a new level `RWServiceMisrouted`** — same evaluator as `RWServiceEmpty` (`reportRWServiceEndpoints`), judged from the A2 verdict on the non-Sentinel arm and `clusterState.MasterPod` against the labelled pod on the Sentinel arm; own registry row, README row, ADR 0012 D12 sentence; presence-guarded like its sibling. *(2026-09-27: the Sentinel-arm judgement runs after `CheckCluster`, Options decision 6.)* | 2026-09-27 |
| 7 | the crash-restart adjacent finding | **its own ticket, [T36](036-non-persistent-master-restarts-empty.md)** — no board row: Hans rejected the board outright ("a board entry was never sufficient"); a finding is a new ticket or an appendix to the existing ticket of its family, and this one differs from T35 in mechanism and severity, so it is new. The Kind reproduction first, as proposed; severity high is an estimate until then | 2026-09-27 |
| 8 | which Sentinel answer the labeler's cross-check reads | **not decided** — L4 recommended, Options decision 8 | — |

## Work list *(added 2026-09-27)*

~~No decision is open; every item below follows the table above.~~ *(corrected 2026-09-27 at
84a39c2: decisions 1 and 3 carry re-decision proposals and decision 8 is open; the items below
say which one they wait for.)*

- **XS, needs no decision, can land today** — the two false statements about the Sentinel
  arm's source (Fact A, mechanism 1), each corrected in place with a dated note:
  - [ADR 0002 D11](../adr/0002-surface-a-blocked-reconcile-on-the-cr.md) `:331-332`, "the
    master as Sentinel reports it" → the pod answering `role:master` to `INFO replication`,
    found by `findMaster`; no Sentinel is asked. The ADR's `Status` gets a dated line naming
    the correction (CLAUDE.md, ADRs must be kept current).
  - the comment at [`valkey_controller.go:2290`](../../internal/controller/valkey_controller.go),
    "(clusterState.MasterPod, from Sentinel)" → the same.
  - Urgency returns to `later` (rule 4) when both land. Decision 1 amends D11 again later
    (A2); this correction does not wait for it.
  - **Done 2026-09-27**, both, each with a dated note (History). *(2026-09-27: commit `bcc63c9`.)*
- **XS, needs no decision, rule 1 — ten tracked places state a master record the code does not
  keep** *(added 2026-09-27 at 84a39c2)*. Urgency stays `now` until all ten are corrected.
  - Six comments say the Sentinel-path record follows a Sentinel failover, or only seeds a
    restarting Sentinel. False, measured on wds18 (Fact B: record and monitor line on `-1` while
    `-0` was master) and by reading (Phase 2 self-claims on the record):
    [`rolling_update.go:1040-1041`](../../internal/controller/rolling_update.go) ("the annotation
    only pre-seeds a restarting sentinel"; its lead-in at `:1034-1038` names only Sentinel pods),
    [`configmap.go:141-146`](../../internal/builder/configmap.go) ("reflects the actual
    post-failover master"), [`sentinel.go:24-28`](../../internal/builder/sentinel.go) ("if a
    sentinel pod restarts after a failover, it reads the correct master") and the shell comment
    in the generated init at [`statefulset.go:318-320`](../../internal/builder/statefulset.go)
    ("so it reflects post-failover state") are false; [`configmap.go:42-44`](../../internal/builder/configmap.go)
    ("set after a sentinel failover") and [`sentinel.go:77-79`](../../internal/builder/sentinel.go)
    ("set by the operator after a successful sentinel failover") are misleading. The correct
    statement: on the Sentinel path the record moves only with operator promotions and
    Sentinel-roll finalization, so after a Sentinel failover outside a roll it names the previous
    master, and the data init's Phase 2 self-claims on it when every Sentinel is silent. When B2
    lands, the statement gains the steady-state refresh.
  - Four places say `status.masterPod` is the live master:
    [`valkey_types.go:81-82`](../../api/v1/valkey_types.go) ("status.masterPod is the live
    answer"), [`docs/operations/status.md:25`](../operations/status.md#topologyrestored) ("Read
    `status.masterPod` for the master"),
    [ADR 0010:389](../adr/0010-every-rolling-update-wait-is-bounded.md) and
    [`condition_registry.go:254`](../../internal/controller/condition_registry.go). Each sits
    next to `TopologyRestored`, which is non-Sentinel only — `valkey9`'s class, where the field
    named a replica for 27+ min (run). Each gets a dated caveat: after a non-Sentinel roll the
    field can name the previous master until the next event, up to the 10 h resync, until change
    1 lands; ADR 0010's `Status` gets the dated line.
  - The shell comment is part of the hashed PodSpec (`statefulset.go:1223-1238`), so correcting
    it rolls every Sentinel data tier. On the Helm path that rides the release's sidecar-image
    roll (ADR 0005 D11, `:297-301`) and adds no roll; it lands in the same XS change. The Go
    comments, the Go doc comment and the documents roll nothing.
- **Change 1 — decisions 1, 2, 5, 6, one change (M):** A2
  ([`steady_state_master.go:261-267`](../../internal/controller/steady_state_master.go),
  [`valkey_controller.go:2324-2342`](../../internal/controller/valkey_controller.go)); A1 in both
  branches (`:249-253`, `:261-267`) unless decision 1 is re-decided; B2 after `persistStatus`
  ([`valkey_controller.go:2461`, `:2528`](../../internal/controller/valkey_controller.go),
  [`checker.go:325-341`](../../internal/health/checker.go),
  [`rolling_update.go:1071`](../../internal/controller/rolling_update.go)) **with the
  `requestRecheck` that republishes both ConfigMaps** (Options decision 2, needs no decision);
  A4 next to the Secret watch
  ([`valkey_controller.go:2997-3000`](../../internal/controller/valkey_controller.go)); A3 as
  `RWServiceMisrouted` ([`rw_service_report.go:37`](../../internal/controller/rw_service_report.go),
  [`condition_registry.go:235`](../../internal/controller/condition_registry.go)), **evaluated
  after `CheckCluster` on the Sentinel arm** (Options decision 6, needs no decision); the
  thirteen-place "no Pod watch" sweep (Options decision 5) and the ADR amendments (0002 D11, 0008
  D3, 0011 D21 — and D12 only if A1 stays — 0012 D12); the full e2e suite on both legs. A1 + A2
  alone would be XS, but decision 5 binds them to A4 — shipping them first is a re-decision of 5,
  not an implementation choice. ~~[T59](059-status-readyreplicas-is-compared-against-itself.md)
  recommends landing its `prevStatus` move first, to keep B2's diff small.~~ *(corrected
  2026-09-27, cross-ticket: T59, re-verified the same day, records that B2 writes after
  `persistStatus` and its placement rule holds under T59's recommended A-prime; the two changes
  overlap only textually in the tail of `updateHAStatus`, so which lands first is optional.)*
- **Change 2 — decision 3 (M):** ~~the Kind reproduction, in one session with the
  [T36](036-non-persistent-master-restarts-empty.md) reproduction (both kill the master of a
  Sentinel cluster); then C1 + C2 in the Sentinel data init
  ([`statefulset.go:288-316`](../../internal/builder/statefulset.go)), a builder unit test,
  `make test-image-tools`, the e2e rerun.~~ *(corrected 2026-09-27 at 84a39c2: waits for the
  re-decision of decision 3. Under C4: one ADR shared with T36, the generated shell in the
  Sentinel data init ([`statefulset.go:288-333`](../../internal/builder/statefulset.go)), a
  builder unit test, the image-tools docker test and the force-delete e2e of Verification, in
  one session with the T36 reproduction. Under decision 3 as taken, its C1 + C2 close nothing
  measured.)*
- **Decision 8 (XS):** L4 in the sidecar querier with its two unit tests, once decided.
- **Evidence durability (done in this pass):** the reconcileIDs, lines and timestamps this
  ticket relies on are quoted above; the source files are ephemeral (Verification).
- **Closing under ADR 0034** needs, after the code: the decisions extracted into ADRs (0002 D11,
  0008 D3, 0011 D21, 0012 D12, the new boot-rule ADR shared with T36), the operator-visible
  consequence into `docs/operations/status.md` and the README condition row, then the move to
  `archive/`.

## Verification

Per option, each with the revert check ADR 0017 asks for: the named test fails without the
change.

- [x] **XS corrections** *(added 2026-09-27)*. `git grep -n "as Sentinel reports it\|MasterPod, from Sentinel"`
  returns only struck text; `make lint` stays green (a comment change); no ticket is cited in
  either file (ADR 0034 D7). *(Run 2026-09-27 after the fix: the grep returns only the struck
  text at ADR 0002 `:342`; no added line cites a ticket. `make lint` was not run.)* *(2026-09-27
  at 84a39c2: re-run, same result; the change is commit `bcc63c9`.)*
- [ ] **Rule-1 corrections** *(added 2026-09-27 at 84a39c2)*. `git grep -nE "pre-seeds a
  restarting sentinel|post-failover (master|state)|after a (successful )?sentinel failover|live
  answer|Read .status.masterPod. for the master"` outside `docs/tickets` returns only corrected
  text; no added line cites a ticket.
- [ ] **A1** *(only if decision 1 is not re-decided)*. Unit: a pass whose sole labelled pod
  answers `role:slave` returns a requeue of the chosen delay, and so does a pass whose sole
  labelled pod is terminating. Revert: without the two `requestRecheck` calls both fail. Kind:
  after the roll of `TestE2E_RollingUpdate_MultiReplicaNoSentinel`
  ([`rolling_update_test.go:97`](../../test/e2e/rolling_update_test.go)), `status.masterPod`
  equals the `INFO replication` master within one recheck — the fixture already reads that master
  by INFO (`findMasterPod`, defined at
  [`rolling_update_test.go:733`](../../test/e2e/rolling_update_test.go), called at `:195-209`);
  the status comparison is the new assertion. No e2e asserts `status.masterPod` today.
- [ ] **A2.** Unit: the completing pass writes the annotation's pod into `status.masterPod`
  when the sole label was proven stale in the same pass, and the labelled pod when nothing
  was proven. Revert: without the pass-state field the first fails. The same Kind assertion as
  A1 covers A2 + A4 when A1 is dropped.
- [ ] **A3** *(decision 6: a new level)*. Unit: a settled cluster with one labelled pod
  that is not the INFO master reports `RWServiceMisrouted=True/LabeledPodIsNotMaster`, and
  clears it on the next settled pass whose label matches; `RWServiceEmpty` is untouched by
  the same fixture; on the Sentinel arm a pass without `clusterState` neither sets nor clears it,
  and an extra master label next to the INFO master sets it. Its own registry row; the ADR 0027
  guard stays green; the README row, the `docs/operations/status.md` section and the
  `docs/developer/package-map.md` row exist; ADR 0012 D12 names both levels.
- [ ] **A4.** Unit: the predicate fires on a changed `instanceRole` value only — not on a
  status-only pod update, not on a pod without the cluster label — and the map function
  returns the CR named by the pod's namespace and cluster label. ~~Integration (envtest): a
  label patch on a data pod enqueues a reconcile of its CR, visible through the status write
  it produces.~~ *(corrected 2026-09-27 at 84a39c2: envtest runs no kubelet, so the data
  StatefulSet reports `readyReplicas` 0 and `updateStatus` takes the Provisioning branch
  (`valkey_controller.go:2262-2284`); even with `readyReplicas` faked, the all-ready branch needs
  `verifyValkeyConnectivity` to succeed (`:2235-2263`), so `currentMasterPod` never runs and
  nothing is written (`:2566-2568`). Integration: a label patch on a data pod enqueues a
  reconcile of its CR, observed through a counting wrapper around the reconciler or another
  observable that needs neither a kubelet nor Valkey, and not read through the manager cache
  right after the patch ([T33](033-integration-tests-read-the-cache-after-a-write.md)).)* Sweep:
  `grep -rnE "Pod watch|no Pod *$" internal docs CLAUDE.md DEVELOPER.md`, outside `docs/tickets`,
  each hit read, returns only sentences that say when the watch was added, plus the ADR 0011
  heading of the rejected alternative.
- [ ] **B2.** Unit: with a majority of Sentinels and INFO naming P and the annotation naming
  Q, the pass writes P and requests a recheck; with the Sentinels split, with INFO disagreeing,
  or with P outside the ordinals, it writes nothing; on a non-Sentinel cluster it never runs.
  Revert: without the refresh the first fails. Kind: a Sentinel cluster in the shape of
  `TestE2E_HAClusterWithSentinel` ([`standalone_test.go:121`](../../test/e2e/standalone_test.go)),
  master deleted, `+switch-master` seen; then the annotation **and** the monitor line of
  `<name>-sentinel-config` **and** the `replicaof` of the replica ConfigMap name the new master
  within one recheck after `Ready`.
- [ ] **C, reproduction** — the filing's item, sharpened. On Kind, a Sentinel cluster;
  ~~the master deleted **with** its grace period (the drain forces the failover, as on wds18)
  and, in a second run, with `--grace-period=0` (Sentinel's own 5 s `down-after` path).~~
  *(corrected 2026-09-27 at 84a39c2: a zero-grace delete does not skip the drain. The kubelet
  gives every container at least 2 s of SIGTERM (`minimumGracePeriodInSeconds = 2`, Kubernetes
  v1.36.1 `pkg/kubelet/kuberuntime/kuberuntime_manager.go:84`, applied at
  `kuberuntime_container.go:903-906`), so the drain still forces the failover, and `kubectl`
  turns `--grace-period=0` without `--force` into 1 (`staging/src/k8s.io/kubectl/pkg/cmd/delete/delete.go:192-196`).
  The runs are: a delete with `GracePeriodSeconds: 0` through client-go — the Chaos Mesh default
  and the wds18 shape: the object is gone at once and the replacement boots during the failover;
  a graceful delete as the control, whose margin is about 1 s, because on Sentinel clusters the
  valkey container has no drain preStop and the drain returns on connection refused
  (`drain.go:385-388`); the drain-less shape needs a death the drain does not act on.)* The
  replacement is named by UID (`waitForPodRecreated`,
  [`e2e_test.go:316`](../../test/e2e/e2e_test.go), ADR 0017 D50,
  [T34](034-e2e-fixtures-wait-on-controller-state-after-a-pod-delete.md)), and its `init-config-selector` log
  and its sidecar log are captured before anything else touches it; no pod-log helper exists in
  `test/e2e` yet. Recorded: which Sentinel answered, what it said, whether the init chose the
  master config, and whether the sidecar ever labelled the pod master.
  `TestE2E_SidecarFailoverDrainMaster` ([`sidecar_test.go:223`](../../test/e2e/sidecar_test.go),
  replicas 3, Sentinel 3, graceful `deletePod` at `:69-74`) is the fixture.
- [ ] **C1 + C2** *(decision 3 as taken; superseded if the re-decision takes C4)*. The init
  script is generated Go text, so the unit is in `internal/builder`: the generated script asks
  every Sentinel, counts, and skips a reply carrying `failover_in_progress`. ~~`make
  test-image-tools` runs the script's commands under the restricted posture;~~ *(corrected
  2026-09-27, cross-ticket from T41: `make test-image-tools` checks that the declared tools and
  shell constructs exist in the image and runs the pre-flight, repair, probe and drain hook,
  not the config-writer scripts; no test executes the Sentinel branch of the data init today -
  `init_script_exec_test.go` runs the non-Sentinel data init and
  `sentinel_init_script_exec_test.go` the Sentinel pod's own init - so ADR 0017 D19 requires a new
  exec harness, stub `valkey-cli` answering per Sentinel, for this branch, and D20's three proofs
  apply)* `awk` has its line
  in `RequiredImageTools` ([`image_requirements.go:45`](../../internal/builder/image_requirements.go)).
  Kind: the reproduction above, re-run, with the replacement booting as a replica of the
  promoted pod on every run and the health checker logging no "Multiple masters".
- [ ] **C4** *(if decision 3 is re-decided to C4)*. Unit (`internal/builder`): the generated
  script takes the name from `get-master-addr-by-name`, reads `num-slaves` from `SENTINEL MASTER`,
  and does not select the master config for an empty `/data` while `num-slaves > 0`. Image-tools
  docker test: the generated script against a docker Sentinel tier, once during a forced failover
  held inside the pre-promotion window and once during a drain-less death, the replacement
  booting as a replica both times; revert: with today's script the first boots as master. Kind:
  the zero-grace reproduction above on both Valkey lines, the health checker logging no
  "Multiple masters".
- [ ] **L4** *(decision 8)*. Unit (sidecar): the querier sends `get-master-addr-by-name`, returns
  the first non-null answer, and treats a null array or an empty answer as "did not answer" and
  asks the next Sentinel. Revert: with `SENTINEL MASTER` the switch test fails. Kind:
  `TestE2E_SidecarFailoverDrainMaster` with the promoted pod labelled master within one poll of
  `+promoted-slave` on the leader.

**Measurements of 2026-09-27** (the setup is recorded here because the script and its outputs
sit in an ephemeral scratchpad: `…/a462a903-…/scratchpad/work/t35/failover.sh`, `run-9.1.1.log`,
`run-8.1.9.log`).

- **Setup (docker).** One docker network; a master and two replicas (`valkey-server --save ""
  --appendonly no`, replicas with `--replicaof` and `--replica-announce-ip`); three Sentinels with
  `resolve-hostnames yes`, `announce-hostnames yes`, `down-after-milliseconds 5000`,
  `failover-timeout 60000`, quorum 2; a poller that asks every Sentinel `get-master-addr-by-name`
  and `SENTINEL MASTER` (`ip` and `flags`) every ~10 ms; then `docker kill` of the master and
  `SENTINEL FAILOVER` on `s0`. Container names start with `vko-verify-t35`; containers and network
  were removed afterwards (`docker ps -a`, `docker network ls` show none).
- **9.1.1:** `s0` `+try-failover` 17:18:53.309, `+promoted-slave` 54.380 (+1.071 s), `s1`/`s2`
  `+switch-master` 54.439/54.440, `s0` `+switch-master` 55.491 (+1.111 s after the promotion).
  `s0` `get-master-addr-by-name` switched at 54.394; `s0` `SENTINEL MASTER` `ip` stayed on the dead
  master with flags `master,failover_in_progress,force_failover` until 55.505. `s1`/`s2` named the
  dead master with flags `master` (transiently `disconnected`) until 54.44 and never showed a
  failover flag.
- **8.1.9:** `s0` `+try-failover` 17:19:36.324, `+promoted-slave` 37.422 (+1.098 s), `s1`/`s2`
  `+switch-master` 37.520, `s0` `+switch-master` 38.496 (+1.074 s). `s0` `get-master-addr-by-name`
  switched by 37.431; its `ip` kept the dead master with `failover_in_progress,force_failover`
  until 38.512.
- **9.1.1 re-run by an independent check (`vko-verify-t35s`):** `+try-failover` 18:01:20.996,
  `+promoted-slave` 22.127, `s0` `get` switched 22.134 (+7 ms), `s1`/`s2` 22.189-22.207, `s0`
  `+switch-master` 23.213 and its `ip` switched 23.225.
- **Kind (2026-09-26 logs, Valkey 9.1.1, `TestE2E_SidecarFailoverDrainMaster`, session
  `538d7ed7` scratchpad `drain-fixed-1/`, ephemeral).** Run 1: `sentinel-0` `+try-failover`
  13:57:59.311, `+promoted-slave` 58:00.437, non-leaders `+switch-master` 58:00.499, leader
  `+switch-master` 58:01.521 (+1.084 s), replica `sc-drain-2` "Successful partial
  resynchronization" 58:00.507. Run 2: `+try-failover` 13:58:28.832, `+promoted-slave` 29.866,
  non-leaders 29.967, `+slave-reconf-done` of `sc-drain-2` 35.192 after "Full resync from primary"
  at 34.179, leader `+switch-master` 35.243 (+5.377 s); the promoted `sc-drain-1` logged "local
  Valkey reports master but Sentinel disagrees, labeling as replica" (`sentinelMaster` the dead
  `sc-drain-0`) six times from 29.96 to 34.97 and "role changed replica → master" at 35.96.
- **Upstream source**, fetched from `raw.githubusercontent.com/valkey-io/valkey/<tag>/src/sentinel.c`:
  9.1.1 `:1657-1668` (8.1.9 `:1638-1650`) `sentinelGetCurrentPrimaryAddress`; 9.1.1 `:3905-3918`
  `get-master-addr-by-name` uses it and answers a null array for an unknown name; `:3324-3325`
  `SENTINEL MASTER` `ip` is `ri->addr`; `:2608-2629` `+promoted-slave` enters `RECONF_REPLICAS`;
  `:2684-2689` a replica is reconfigured only on link status up; `:4939-4948` the flag is set at
  `+try-failover`; `:5359-5370` cleared at `+switch-master` or abort; `:2352-2362` re-resolve of
  the hostname on reconnect. *(Added by the review of 2026-09-27:)* `sentinelFailoverDetectEnd`
  ends `RECONF_REPLICAS` on timeout once `failover_timeout` has elapsed ("+failover-end-for-timeout",
  9.1.1 `:5207` ff.), which is the 60 s bound; and `src/server.c` at 9.1.1: the SIGTERM handler only
  sets `shutdown_asap` (`:7009`), `serverCron` (`:1503`) acts on it (`:1541`), and a master with
  lagging replicas pauses writes and waits up to `shutdown-timeout` (`:4738-4739`).
- **Kubernetes and Chaos Mesh**, fetched: Kubernetes v1.36.1 `kuberuntime_manager.go:84`,
  `kuberuntime_container.go:903-906`, kubectl `delete.go:192-196`; Chaos Mesh at `master` (the
  wds18 version is unknown) `api/v1alpha1/podchaos_types.go:75-79` (`gracePeriod` default 0,
  "delete immediately") and `controllers/chaosimpl/podchaos/podkill/impl.go:50`.
- **controller-runtime v0.25.1**, module cache: `pkg/controller/controller.go:271-289`,
  `pkg/controller/priorityqueue/priorityqueue.go:255-258`, `pkg/cache/cache.go:45`, `:120-126`.

## Adjacent findings

~~Not in scope, not filed.~~ *(Corrected 2026-09-27, sweep: each bullet below is filed, part of
this ticket's options, outside this repository (Chaos Mesh), or observed behaviour that is not a
defect (the fleet's image choice, the transient mount, the observer's 503s); the last bullet is
analysed in [T75](075-the-sentinel-failover-reset-and-retrigger-cycle-has-no-cap.md) and waits
for its own file, owned by the next filing run.)*

- **Chaos Mesh kills more than one pod per tick.** Pods were recreated in threes at 20:50 and
  20:55 and in twos at 21:10, and the operator deleted nothing after 20:48 (run).
  - The Schedule logged `Failed to update lastScheduleTime` conflicts and `Forbid spawning new
    job … still running`.
  - ~~Several jobs per tick are likely. That was not checked.~~ *(corrected 2026-09-27: checked
    in the saved events — three PodChaos objects per tick. At 20:50: `djqbk` on
    `valkey8-sentinal-tls-1`, `jxw5x` on its observer, `mh2bk` on `valkey8-sentinal-1`. At 20:55:
    `pg557` on an observer, `s5nsf` on `valkey9-tls-1`, `xjm5v` on
    `valkey9-sentinal-tls-sentinel-0`.)* It is outside this repository.
- **`valkey9-tls` runs `valkey/valkey:8.0`**, per its spec (run; 2026-09-27: 17 saved log lines
  carry `desiredImage valkey/valkey:8.0`).
- **A transient `FailedMount` at 20:55:02.** Pod `valkey9-tls-1` could not mount the
  projected token `sidecar-api-access`: "the UID in the bound object reference … does not
  match". The kubelet was fetching a token for the pod object that had been deleted and
  recreated under the same name. The pod came up Ready with 0 restarts (run).
- **Observer readiness 503s.** They occur only in the roll window (20:45–20:47) and at the
  chaos ticks. The observer reports replica sync and read-test failures there, which is its
  job (run).

- **A non-persistent master that crash-restarts comes back empty and stays the master**
  (read, with an inference; not measured; found 2026-09-26 while costing C3). *(Precised
  2026-09-27, cross-ticket: T36 measured the shape per variant; "stays the master" holds for a
  master that booted with the master config. A master promoted by Sentinel restarts as master on
  the stale `dump.rdb` of its last full sync, and one promoted with `REPLICAOF NO ONE` boots as a
  replica of its own replica, both sides refuse with `NOMASTERLINK`, and the no-master recovery
  then promotes pod-0 - T36's variant table and exp2. The "no `dump.rdb` on a non-persistent
  cluster" premise of C3 holds for a fresh replacement pod, not for a restart inside a living
  pod.)* Init
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
  *(2026-09-27 at 84a39c2, for T36, inference from upstream code: Sentinel re-resolves a hostname
  on reconnect, so the same chain runs through a pod replacement that answers before
  `down-after` without a forced failover — Impact C; C4 and T36's start guard belong in one ADR,
  Options decision 3.)*
- **`status.masterPod` means two things**, one per topology — Fact A and Options, "six
  records". A precision defect of the field itself; A2 is the option that fixes it.
- **Three sites take the first Sentinel that answers** — Options, "six records". *(2026-09-27:
  the first voice is harmful only where the read field lags on the leader, which is the labeler's
  `ip` (decision 8) and the roll's `getSentinelMasterPodName`, which ADR 0025 D9's window guard
  already accounts for; the data init's `get-master-addr-by-name` leads.)*

## History

- 2026-09-27: re-verified at `84a39c2` by an audit, a facts review and a design review; every
  location re-read (the moved ones fixed in their links: `valkey_controller.go` `currentMasterPod`
  `:2324-2342`, the Sentinel arm `:2477`/`:2490`, the watches `:2987-3000`, the healthy path
  `:373-396`, `recordSentinelPeerDrift` `:2397`; `rolling_update.go` `persistKnownMaster` call
  `:1045`, its comment `:1034-1044`, the function `:1071-1089`, `isSidecarOnlyChange` `:3845`,
  `getSentinelMasterPodName` `:1741`; ADR 0002 D11 `:331-346`, ADR 0001 `:58`, ADR 0002 `:240`,
  ADR 0012 D12 `:414`; `valkey_types.go:241-260`; `condition_registry.go:235-246`;
  `image_requirements.go:45`; `client.go:36-50`; the labeler cross-check `:135-145`). No code of
  change 1 or change 2 exists; the XS corrections are commit `bcc63c9`.
  - **Checked:** the saved wds18 log and events (coverage 20:44:34-20:55:10Z and
    20:00:00-20:55:06Z, ephemeral, excerpts now quoted in Fact); upstream `sentinel.c` at 9.1.1
    and 8.1.9; Kubernetes v1.36.1 kubelet and kubectl; Chaos Mesh at `master`; controller-runtime
    v0.25.1; the Kind logs of `TestE2E_SidecarFailoverDrainMaster` of 2026-09-26.
  - **Measured (docker, both pins, and a re-run on 9.1.1):** a forced Sentinel failover with a
    dead master, each Sentinel's answer per command every ~10 ms (Verification, "Measurements").
  - **Found false or outdated, corrected in place:** the log was saved at 20:57 UTC and ends at
    20:55:10, not at ~21:12; three 409s in the saved log, not four (the fourth unverified);
    `valkey9-tls` had passes at 20:45:44/45/47, not a gap until 20:55:01; `Ready` most likely at
    20:50:17, not 20:50:12; the record of B written at 20:46:42; the code comment's "the
    annotation only pre-seeds a restarting sentinel" and the ticket's reliance on ADR 0008 `:96`
    (the Sentinel data init's Phase 2 self-claims on the record with no guard); Impact B's
    "cosmetic" (lossy through Phase 2, unlikely); Fact C's "sentinel-0 still named -1" (for
    `get-master-addr-by-name` the leader switches first; before `+promoted-slave` every Sentinel
    names the dead master; the replacement's init ran at 20:50:01 inside that window); Fact C's
    "Why `-rw` was probably safe" (the cross-check most likely confirmed the empty `-1`; the 5 s
    readiness delay and the absent `publishNotReadyAddresses` kept it out of `-rw`, which was
    most likely empty until ~20:50:03.8 — an audit claim that `-rw` selected the empty pod was
    refuted by the facts review and is not applied); the "Sentinel's master table" row and the
    L-table Sentinel row (the leader's `ip` lags for its whole `RECONF_REPLICAS` phase, measured
    1.07 s to 5.38 s, bounded by 60 s, not ≤ 1 s); the rule's "never on the first voice"; the
    "four role changes" gained a fifth by inference (a master stalled past `down-after`); decision 5's "other eight" (twelve); the envtest status-write
    observable for A4; the `--grace-period=0` reproduction (the kubelet floors it at 2 s and the
    drain still runs); A3's evaluator placement (before `CheckCluster`); B2's "the ConfigMaps
    become right" (they need a following pass); "Several jobs per tick ... not checked" (three
    per tick).
  - **Options restructured** into one subsection per decision, the accumulated "Review" and
    "corrected" addenda folded into the current text. **Removed options, with reasons:**
    B1 (one ADR 0008 sentence, "wrong, not lossy") — rests on a false premise, the Phase 2 fallback
    is lossy; C1 (a majority of the answering Sentinels) — before `+promoted-slave` all three name
    the dead master, and between the leader's switch and the others' the majority is wrong while
    the first voice is right; C2 as specified (a reply flagged `failover_in_progress` is not
    counted) — the non-leaders carry no flag (docker), so the remaining majority still names the
    booting pod; C3 (a 5 s unanimity timer on an empty pod) — dominated by C4, its 5 s is shorter
    than a drain-less failover and every cold start of pod-0 pays it; decision 3 as taken
    (reproduce first, then C1 + C2) — false premise, closes nothing measured; L3 (a
    `+switch-master` subscriber) — disproportionate, a subscriber with reconnect and per-dial TLS
    for what L1 leaves; operator-side label stamping in `promoteAndRedirect` as an open option — a
    re-decision of ADR 0012 D12 to save a sub-second window, disproportionate (it would reopen only
    if the observer's write-test rate showed the planned handover's last 250 ms matter); a manager
    `SyncPeriod` of a few minutes — every CR, every period, for nothing, still up to a period
    stale, and it resyncs every watched type, not pods; the "recommended bundle, in order" (1 A1 +
    A2 + B2, 2 C1 + C2 after the reproduction, 3 L1 + L2, 4 A4, 5 A3) — superseded by the decisions
    of 2026-09-27; L1 and L2 as open options — decided against in decision 4. Not taken into the ticket
    at all: bounding A1 by `RWServiceMisrouted`'s `lastTransitionTime` (speculative, a clock for a
    missed informer event a relist re-delivers); L5, a flag-aware cross-check that empties `-rw`
    for the whole failover (write fencing, T12's decision).
  - **Recommendations:** decision 1 — new proposal "drop A1, ship A2 + A4" (A4 delivers the
    settling relabel sooner; A1 is left with an unbounded poll ADR 0011 D12 refuses); decision 3 —
    new proposal C4 (the only option covering the measured pre-promotion window and the
    drain-less shape; runner-up C2', which reads a flag only the leader carries); decision 8 —
    new, L4 recommended over "nothing". Decisions 1 and 3 stand as taken until Hans re-decides.
  - **Needs no decision, added to the Work list:** the ten rule-1 corrections; B2's
    `requestRecheck` after a successful refresh; A3's Sentinel-arm placement after
    `CheckCluster`, counting any extra master label; A4's integration observable.
  - **Cross-ticket:** T36 — C4 and T36's Sentinel-half start guard are compatible, not
    identical, and belong in one ADR; a pod replacement answering before `down-after` runs T36's
    chain. T59 — land its `prevStatus` move before B2. T12 — the outgoing master's writes during
    a Sentinel handover are fencing, T12's decision; L4 only shortens how long `-rw` routes to it.
    T33 — A4's integration test must not read the cache right after its patch. T34 — the C
    reproduction names the replacement by UID. T18 — consistent (the ADR 0002 Status line of
    `bcc63c9` carries both corrections).
  - **Frontmatter:** urgency `later` → `now` by rule 1 (ten tracked places state a master record
    the code does not keep — six Sentinel-path comments, measured false on wds18 and false by
    reading, and four "status.masterPod is the live answer" sentences, measured false on
    `valkey9`); it returns to `later` only when all ten are corrected, the init-script comment
    included, which rides the release roll. `blocked-by: decision` added (decisions 1 and 3
    re-decision proposals, decision 8 open). Severity stays `low`, its reason now by mechanism
    rather than "production has no force deletes" (unverified): in production a force delete
    follows node loss, long after `down-after`, and a graceful delete is drained, so a replacement
    rarely boots inside a failover; the Phase 2 self-claim needs the whole Sentinel tier silent
    for over 31 s. Re-examine it with T12's fencing decision: the leader lag leaves `-rw` empty
    for up to 60 s after a drain (6 s measured) and routes to the outgoing master on a roll
    (inference). Effort stays `L`; security stays `none`; state stays `decided`.
  - **Review of this entry, the same day at `84a39c2`.** Every changed location spot-checked by
    reading (`valkey_controller.go`, `steady_state_master.go`, `rolling_update.go`, the Sentinel
    data init, the labeler, the drain handler, `service.go`, `configmap.go`, `sentinel.go`,
    `checker.go`, the four "live answer" places, the ADR lines, the 14-hit sweep grep); the saved
    log lines 910, 1015, 1142-1146, 1212, 1674, 1921 and 1927 and the log's coverage
    (20:44:34-20:55:10Z, 3 `Reconciler error` lines) re-read; `sentinel.c` at 9.1.1 and 8.1.9 and
    `server.c` at 9.1.1 re-fetched. All held except: the non-leader switch timing (the leader's
    answer led by about 45-90 ms, the non-leaders switched 59-98 ms after `+promoted-slave`; the
    text had mixed the two bases), and decision 8's "measured on both pins" for the 5.4 s figure
    (Kind on 9.1.1 only) — both fixed. Restored from the options of 2026-09-26, which the rewrite
    had dropped: the reopen paths decision 4's row promises to keep documented (L1's costs, L2,
    the operator-side stamping with its price), C2's cold-start caveat (now on C2'), A1's "D14's
    15 s is not owed", A2's measured gain and its semantic consequence, and A4's pass-volume and
    ADR 0031:185 notes. Added with sources: the `serverCron` handling of SIGTERM and
    `shutdown-timeout` behind Impact C's "the drain almost always acts first", and the
    `failover-timeout` end of `RECONF_REPLICAS`. No file outside `docs/tickets/` is modified.
  - Cross-ticket: in the consistency pass of the same day, Impact bullet L gained T34's lag
    figures and the pointer to T12's Decision 2 as owner of the outgoing master's write loss
    (decision 8's text precised the same way), the T59 sequencing sentence was corrected (T59
    records that which lands first is optional), the C1 + C2 work item's claim that `make
    test-image-tools` runs the script was corrected and the ADR 0017 D19 exec harness for the
    Sentinel branch of the data init was added to it and to C4's cost (from T41, verified: no
    test executes that branch), and the crash-restart finding was precised with T36's measured
    variants.
  - Filed: the loss of the outgoing master's acknowledged writes in the roll's own Sentinel
    failover, which this ticket cited as T12's separate finding and T12's Decision 2, is filed as
    [T67](067-the-rolls-own-sentinel-failover-loses-acknowledged-writes.md) (severity high,
    security none, effort M, state analysed; its options A, B and C are T12's 2a, 2b and 2c), and
    Impact bullet L and decision 8 now point to it instead of T12. Filed: the frontmatter
    severity comment now names T67's decision instead of T12's write-fencing decision, because
    the roll-side half of the re-examination this entry asks for above (the outgoing master
    routed by `-rw` on a roll) is T67's subject; severity stays `low`, since the write loss is
    T67's and what remains here is the label and routing lag that decision 8 shortens. Filed:
    the references to T12 earlier in this entry are left as written; for the roll's own failover
    they read as T67.
  - Sweep: Adjacent findings: the heading note "Not in scope, not filed." is struck and replaced by
    the current state of each bullet (filed, part of this ticket's options, outside this repository,
    or not a defect). Frontmatter unchanged.
  - Final pass: one bullet added under Adjacent findings earlier in this run, outside this
    ticket's mechanism and never committed, was removed before commit; it is recorded in the
    ticket that owns it. Frontmatter unchanged.
- 2026-09-27: urgency `now` -> `later` (rule 4): the two corrections, the only rule-1 statements, landed; changes 1 and 2 are decided. Applied as the History entry below derived it.
- 2026-09-27: the two XS corrections landed, file by file (read in `git diff` of the working
  tree):
  - [ADR 0002](../adr/0002-surface-a-blocked-reconcile-on-the-cr.md) D11 (`:342-346` now): "the
    master as Sentinel reports it" struck and corrected in place - the running data pod that
    answers `role:master` to `INFO replication`, found by `findMaster` probing every data pod,
    the most connected replicas when several answer, ties to the lowest ordinal (a stable sort,
    re-read at `checker.go:218` ff.), and no Sentinel is asked; a dated "Corrected 2026-09-27
    (no decision changes)" Status line, which also carries the T18 correction.
  - [`internal/controller/valkey_controller.go`](../../internal/controller/valkey_controller.go),
    the `currentMasterPod` doc comment (`:2290-2291` now): "(clusterState.MasterPod: the pod
    answering role:master to INFO replication, health.Checker.findMaster; no Sentinel is asked)".

  The comment grew by one line, so every `valkey_controller.go` reference in this file from
  `:2290` on is one lower than the working tree now (two lower from `:3049` on, after another
  comment edit of the same change); the references before `:2290` are unchanged. Decisions 1-7
  and changes 1 and 2 are untouched. **Urgency not recomputed in this pass** (the orchestrating
  run left every urgency but one to the owner): by the frontmatter's own derivation it returns
  from `now` to `later` (rule 4), because the two corrections were the only rule-1 statements.
  **Not verified:** `make lint` was not run.
- 2026-09-27: adversarial review of the enrichment — the new locations re-read at `4a7543e`
  (foreign_object, rbac, labeler, the three Go comments, ADR 0002/0011 lines, the condition's
  three doc places, `findMaster`, the e2e helpers, `managerOptions`), all held, and the false
  D11 sentence confirmed: `CheckCluster` takes `MasterPod` from `findMaster`
  ([`checker.go:108-115`](../../internal/health/checker.go)), and `updateHAStatus` copies it
  (`valkey_controller.go:2476`, `:2489`). Corrected in place: the sweep is thirteen places, not
  twelve (two wrapped test comments), with a grep that finds them; the XS correction of D11
  also gets a dated `Status` line. Effort stays L.
- 2026-09-27: enriched - locations re-read at `4a7543e` and corrected in place (the "no Pod
  watch" sweep grows from nine to twelve places, three Go comments included; A2 amends ADR 0002
  D11; a condition now has three doc places); a work list separating two XS corrections from
  changes 1 and 2; B1, L1 and L2 marked not chosen in Verification. **Urgency later → now**,
  rule 1: the last sentence of ADR 0002 D11 and the comment at `valkey_controller.go:2290` say
  the Sentinel arm's `status.masterPod` is Sentinel's answer, and the code takes it from
  `INFO replication` — false by reading the code the sentences describe, which is how a claim
  about code is measured; urgency falls back to `later` (rule 4) once the XS correction lands.
  **Effort M → L:** change 1 is M on its own (twelve-place sweep, four ADR amendments, the full
  e2e on both legs), change 2 another M with its Kind reproduction.
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
