---
id: T35
title: records of who the master is lag the real master after a handover (seen on wds18 after the v1.13.0 upgrade)
state: decided
severity: low         # label and routing lag; the write loss of the roll's own failover is T67's
security: none
urgency: now          # rule 1: ten tracked places state a master record the code does not keep
effort: L
blocked-by: decision  # Q1 and Q2 are re-decision proposals, Q3 is open
filed-from: check of the v1.13.0 upgrade on wds18-k8s-main, namespace database-examples, 2026-09-26
opened: 2026-09-26
decided: 2026-09-27
done:
---

# T35 - Records of who the master is lag the real master after a handover

## Current state

Four records say who the master is, each on its own clock:

| Record | Writer | Moves |
|---|---|---|
| `instanceRole` pod label (`-rw`/`-r` selector) | the pod's sidecar labeler, 1 s poll of `INFO replication`; on Sentinel clusters confirmed by `SENTINEL MASTER` `ip` ([`labeler.go:135-145`](../../internal/sidecar/labeler.go), [`:351-363`](../../internal/sidecar/labeler.go)) | at most 1 s after the role change; on Sentinel clusters only after the failover leader's `+switch-master` |
| `status.masterPod` | `updateStatus`: from the label on the non-Sentinel arm ([`valkey_controller.go:2252`, `:2324-2342`](../../internal/controller/valkey_controller.go)), from `findMaster`'s `INFO` answer on the Sentinel arm ([`:2477`, `:2490`](../../internal/controller/valkey_controller.go)) | at the next pass |
| `vko.gtrfc.com/known-master` | the operator on its own promotions; best-effort at Sentinel roll finalization ([`rolling_update.go:1034-1048`](../../internal/controller/rolling_update.go)) | never on a Sentinel failover outside a roll |
| replica ConfigMap `replicaof`, Sentinel ConfigMap monitor line | the operator, from the annotation, at the start of a pass ([`valkey_controller.go:552-560`](../../internal/controller/valkey_controller.go)) | with the annotation; excluded from the config hash |

A relabel alone triggers no pass: there is no Pod watch
([`valkey_controller.go:2984-3002`](../../internal/controller/valkey_controller.go)), a healthy
pass returns no requeue ([`:373-396`](../../internal/controller/valkey_controller.go)), and the
cache resync is the controller-runtime default of 10 h. Role changes without a pod death (the
relabel that closes a topology restoration, a manual `SENTINEL FAILOVER` or `REPLICAOF`, the
labeler's cross-check flip, a Sentinel failover of a stalled master) leave every record to the
resync.

**A - `status.masterPod` names a replica after a non-Sentinel roll.** The completing pass finds
the sole master label stale ([`steady_state_master.go:261-267`](../../internal/controller/steady_state_master.go);
terminating branch `:249-253`), logs and returns; `currentMasterPod` then writes the labelled pod,
which the sidecar has not relabelled yet, and nothing re-enters. Measured on wds18: `valkey9`
showed the replica `valkey9-1` for more than 27 minutes. The field also means "the pod `-rw`
selects" without Sentinel and "the pod answering `role:master`" with Sentinel. The operator does
not read it; people using the MASTER column in Lens or `kubectl get valkey` act on a replica.

**B - the `known-master` record names a replica after a Sentinel failover outside a roll.**
Measured on wds18 (`valkey8-sentinal-tls`): after `-1` was killed and `-0` promoted, the
annotation and the monitor line still named `-1`. The Sentinel init validates its target by
`ROLE` and a pod scan ([`sentinel.go:654-692`](../../internal/builder/sentinel.go)); the data
init's Phase 2 does not: after about 31 s without a Sentinel answer it adopts `replicaof`, and if
that names the booting pod it takes the master config
([`statefulset.go:318-333`](../../internal/builder/statefulset.go)). An empty replacement of the
recorded pod then becomes a second master, both labelled master (each labeler trusts its local
role without Sentinel, [`labeler.go:144`](../../internal/sidecar/labeler.go)), and its writes are
discarded when Sentinel returns. Lossy and unlikely: the whole Sentinel tier must be silent.

**C - a killed master's replacement boots as a second, empty master for about 5-10 s.** The data
init takes the first Sentinel answer to `get-master-addr-by-name`
([`statefulset.go:288-316`](../../internal/builder/statefulset.go)) and boots as master when it
names itself (`:331-333`). Before `+promoted-slave` every Sentinel names the dead master. On
wds18 the force-deleted master's replacement ran its init inside that window; the health checker
logged "Multiple masters detected" until Sentinel converted it. The readiness probe
(`InitialDelaySeconds: 5`, [`statefulset.go:847-858`](../../internal/builder/statefulset.go))
kept it out of `-rw`; a client discovering the master through Sentinel or using the headless name
could have written to it. `MultipleMasters` stayed `False`: its only evaluator is the
rolling-update resolver ([`condition_registry.go:139-150`](../../internal/controller/condition_registry.go)).
A replacement answering before `down-after` (5 s) without a forced failover is never failed over,
and the replicas full-resync from it (inference; rare, the drain almost always forces the failover).

**L - on Sentinel clusters the labels trail every failover.** The leader's `SENTINEL MASTER` `ip`
keeps the old master until its own `+switch-master`, when every replica's link is up or at
`failover-timeout` (60 s); its `get-master-addr-by-name` switches 7-14 ms after `+promoted-slave`.
`sentinel-0`, which the labeler asks first, leads every roll- and drain-forced failover. Measured
lag: 1.07-1.11 s (docker, both pins), 1.15 s (wds18), 5.38 s with a full resync (Kind). After a
drain `-rw` is empty for that time; on a roll it routes to the outgoing master, whose writes are
discarded at its conversion (inference). Every release rolls the four production Sentinel CRs.

**False tracked statements (rule 1).**

- The Sentinel-path record is said to follow a Sentinel failover or only seed a restarting
  Sentinel: [`rolling_update.go:1040-1041`](../../internal/controller/rolling_update.go),
  [`configmap.go:141-146`](../../internal/builder/configmap.go),
  [`sentinel.go:24-28`](../../internal/builder/sentinel.go), the init shell comment at
  [`statefulset.go:318-320`](../../internal/builder/statefulset.go) (false);
  [`configmap.go:42-44`](../../internal/builder/configmap.go),
  [`sentinel.go:77-79`](../../internal/builder/sentinel.go) (misleading).
- `status.masterPod` is said to be the live master, next to the non-Sentinel-only
  `TopologyRestored`: [`valkey_types.go:81-82`](../../api/v1/valkey_types.go),
  [`docs/operations/status.md:25`](../operations/status.md#topologyrestored),
  [ADR 0010:389](../adr/0010-every-rolling-update-wait-is-bounded.md),
  [`condition_registry.go:254`](../../internal/controller/condition_registry.go).

## Required changes

### Independent of the open questions

- **Rule-1 corrections (XS).** Sentinel path: the record moves only with operator promotions and
  roll finalization, so after a failover outside a roll it names the previous master, and Phase 2
  self-claims on it. `status.masterPod`: after a non-Sentinel roll it can name the previous master
  until the next event; ADR 0010 gets a Status line. The shell comment is in the hashed PodSpec
  and rides the release's sidecar-image roll. Check: `git grep -nE "pre-seeds a restarting sentinel|post-failover (master|state)|after a (successful )?sentinel failover|live answer|Read .status.masterPod. for the master"`
  outside `docs/tickets` returns only corrected text.
- **Change 1 (M), one change:**
  - **A2 (decision 1).** `adoptUnrecordedPromotion` records the proven-stale pod on `passState`
    ([`foreign_object.go:89-146`](../../internal/controller/foreign_object.go)); `currentMasterPod`
    skips the label rule for it and falls to the known-master record, so the field means "the
    master" on both topologies. Amend ADR 0002 D11. Unit: record's pod when the label was proven
    stale in the pass, labelled pod otherwise.
  - **B2 (decision 2).** `CheckCluster`'s Sentinel round
    ([`checker.go:325-341`](../../internal/health/checker.go)) keeps each Sentinel's master name.
    When a majority of answering Sentinels and the INFO master name ordinal P and the record (or its
    pod-0 default) names another, call `persistKnownMaster(P)`
    ([`rolling_update.go:1071-1089`](../../internal/controller/rolling_update.go)) after
    `persistStatus` (before it, the `Update` would discard in-memory conditions), log on failure,
    and `requestRecheck` so the next pass republishes both ConfigMaps. Sentinel path only. Amend
    ADR 0008 D3. Unit: writes P on agreement; nothing on split Sentinels, disagreeing INFO or P
    outside the ordinals; never without Sentinel.
  - **A4 (decision 5).** A Pod watch next to the Secret watch: Update events with a changed
    `instanceRole` value only, mapped to the CR by the `vko.gtrfc.com/cluster` label and namespace
    (`findValkeyForPod`, `instanceRoleChanged`). The Pod informer and `pods: watch` already exist.
    Unit: predicate and map function. Integration: a label patch enqueues a reconcile, observed
    through a counting wrapper (envtest runs no kubelet, so no status write happens). Amend
    ADR 0011 D21, and add one clause to each of the thirteen "no Pod watch" sentences found by
    `grep -rnE "Pod watch|no Pod *$" internal docs CLAUDE.md DEVELOPER.md` (the ADR 0011 heading
    at `:417` stays).
  - **`RWServiceMisrouted` (decision 6).** A new level next to `RWServiceEmpty`
    ([`rw_service_report.go:37`](../../internal/controller/rw_service_report.go)): registry row,
    `ConditionType`, README row, `docs/operations/status.md` section, `docs/developer/package-map.md`
    row, one sentence in ADR 0012 D12; presence-guarded. Sentinel arm: judged after `CheckCluster`
    in the all-ready case, any master-labelled pod other than `clusterState.MasterPod`, untouched
    without a `clusterState`. Non-Sentinel arm: the A2 verdict. Unit: set, cleared, `RWServiceEmpty`
    untouched.
  - **Proof (Kind):** after `TestE2E_RollingUpdate_MultiReplicaNoSentinel`, `status.masterPod`
    equals the INFO master; on a Sentinel cluster with its master deleted, annotation, monitor line
    and `replicaof` name the new master within one recheck after `Ready`; full suite on both legs.
- **Labeler poll (decision 4: nothing).** The 1 s poll stays; reopen only if the observer's
  write-test failure rate over rolls shows the handover window matters.
- **Closing:** the decisions into ADRs (0002 D11, 0008 D3, 0011 D21, 0012 D12, the Q2 boot rule),
  the operator-visible consequence into `docs/operations/status.md` and the README.

### Depends on the answers

- **Q1, if A1 stays:** both proving branches call `requestRecheck(ctx, rollingUpdateRequeueDelay)`;
  amend ADR 0011 D12; unit tests for the stale and the terminating branch.
- **Q2, under C4 (M):** one ADR shared with T36; the generated shell in the Sentinel data init
  ([`statefulset.go:288-333`](../../internal/builder/statefulset.go)); a builder unit test and an
  ADR 0017 D19 exec harness for that branch (none exists); an image-tools docker test during a
  forced failover held in the pre-promotion window and during a drain-less death; a Kind e2e on
  both Valkey lines: `TestE2E_SidecarFailoverDrainMaster`
  ([`sidecar_test.go:223`](../../test/e2e/sidecar_test.go)) with the master deleted through
  client-go with `GracePeriodSeconds: 0` (`kubectl --grace-period=0` without `--force` becomes 1),
  the replacement named by UID, its init and sidecar logs captured (needs a pod-log helper), no
  "Multiple masters" logged.
- **Q3, under L4 (XS):** the sidecar querier sends `get-master-addr-by-name` and treats a null or
  empty answer as "did not answer". Unit tests for the switch and the null answer; Kind: the
  promoted pod labelled master within one poll of `+promoted-slave`.

## Open questions

### Q1: Does A1's 10 s recheck stay, now that A4 (the Pod watch) ships in the same change?

Decision 1 chose A1 + A2 before A4 was decided. A4 delivers the settling relabel as an event,
sooner than A1's 10 s. A1 then only matters for a label that never settles (a sidecar alive but
not relabelling), where it re-probes `INFO` every 10 s per CR without end, the polling ADR 0011
D12 refuses; `RWServiceMisrouted` already reports that case.

- **Keep A1 + A2.** A second trigger for a missed watch event; XS plus an ADR 0011 D12 amendment;
  an unbounded poll when a label never settles.
- **Drop A1, ship A2 + A4 (recommended).** A relist re-delivers a missed update, so A1 guards
  nothing A4 misses; less code, and ADR 0011 D12 stays as written.

The current decision (A1 + A2) stands until re-decided.

**Answer:** _open_

### Q2: How does an empty replacement data pod on a Sentinel cluster avoid booting as master?

The current decision (reproduce, then C1 + C2, C3 as fallback) assumed one lagging Sentinel;
before `+promoted-slave` every Sentinel names the dead master, so C1 + C2 close nothing measured.

- **C4 (recommended).** A pod with an empty `/data` that Sentinel names as master does not take
  the master config while `SENTINEL MASTER` reports `num-slaves > 0`; it re-asks until another pod
  is named and boots as its replica; past a bound (above `down-after` plus a promotion, at most
  `failover-timeout` 60 s) it behaves as today. Cost M; the replacement boots 1-2 s (forced) or
  6-8 s (drain-less) later; a whole data tier replaced under surviving Sentinels waits for a
  failover or the bound.
- **C2'.** Decide nothing while any Sentinel reports `failover_in_progress`. Cost M; covers only
  the forced shape (only the leader carries the flag), and a slow failover outlasts Phase 1's 31 s
  unless the bound is raised.

C4 alone covers the forced and the drain-less shape, and is the same rule as T36's start guard,
so one ADR serves both.

**Answer:** _open_

### Q3: Which Sentinel answer does the labeler's cross-check read?

The labeler reads `SENTINEL MASTER` `ip` from `sentinel-0` first, which trails every forced
failover by the leader's `RECONF_REPLICAS` phase (1.1 s to 5.4 s measured, up to 60 s).

- **L4 (recommended).** Read `get-master-addr-by-name`, which the leader switches at
  `+promoted-slave`; the local-master precondition stays. A null answer must count as "did not
  answer", or a real master is labelled `replica`. Cost XS, rides the sidecar-image roll.
- **Nothing.** The lag stays on every Sentinel handover, including every release roll.

L4 removes that window for one command, with no rule change and no extra roll.

**Answer:** _open_

## Not verified

- Whether the empty `-1` of C was labelled `master` or received writes (logs lost); the Q2 e2e
  settles the boot behaviour.
- Behaviour on persistent clusters; not looked at.

## Related

- [T36](036-non-persistent-master-restarts-empty.md) - empty master after a crash-restart; shares the Q2 boot-rule ADR.
- [T67](067-the-rolls-own-sentinel-failover-loses-acknowledged-writes.md) - owns the outgoing master's write loss; L4 only shortens routing.
- [T59](059-status-readyreplicas-is-compared-against-itself.md) - overlaps B2 textually in `updateHAStatus`; either may land first.
- [T33](033-integration-tests-read-the-cache-after-a-write.md) - A4's integration test must not read the cache right after its patch.
- [T34](034-e2e-fixtures-wait-on-controller-state-after-a-pod-delete.md) - the Q2 e2e names the replacement by UID.
