---
id: T34
title: e2e fixtures wait on controller state after deleting a pod
state: analysed
severity: low         # no failure observed at the open sites; two subtests cannot fail as named
security: none        # no guard involved is a security control
urgency: now          # rule 1: tracked test comments at sidecar_test.go:487, :523-524 and sentinel_stale_master_test.go:130 are false
effort: M             # about 15-35 lines of test code; Kind runs, revert checks, a mutation and the close edits dominate
blocked-by: decision  # Q1 only; the comment fix and site 1's effect read need no decision
filed-from: T31, section "Drain-test finding", and ADR 0017 D50
opened: 2026-09-26
decided:
done:
---

# T34 - e2e fixtures wait on controller state after deleting a pod

## Current state

After a pod `Delete`, every wait the e2e fixtures use can be answered by the pod that is still
terminating:

- kubelet keeps a terminating pod `Ready` for its whole termination
  ([ADR 0026](../adr/0026-a-pod-being-deleted-is-not-available.md)).
- The StatefulSet's `readyReplicas` counts terminating pods (upstream
  `stateful_set_control.go:378`, v1.36.4). `waitForStatefulSetReady`
  ([`e2e_test.go:147-163`](../../test/e2e/e2e_test.go)) compares `ReadyReplicas` alone (`:160`).
- `waitForPodReady` ([`e2e_test.go:285-305`](../../test/e2e/e2e_test.go)) reads the pod by name,
  with no UID and no `deletionTimestamp` check. `getPod`, `valkeyExec` and `valkeyExecQuick` also
  work by name; `valkeyExecQuick` returns an empty string on any exec error (`:520-542`).
- A phase wait for `OK` is met by the status written before the delete.
- The identity wait exists: `waitForPodRecreated`
  ([`e2e_test.go:307-340`](../../test/e2e/e2e_test.go)) waits for the name under a new UID, Ready.
  Five delete sites use it. ADR 0017 D50 requires identity (image, UID or `deletionTimestamp`)
  wherever "which pod" is part of the assertion. A `deletionTimestamp`, once set, cannot be unset,
  so a pod of that name without one is the replacement.

Two sites violate D50:

**Site 1 - `TestE2E_SidecarDrainReplica`** ([`sidecar_test.go`](../../test/e2e/sidecar_test.go)).
The master of a fresh cluster is `sc-repdr-0` by construction; the test deletes replica
`sc-repdr-1` at `:485`.

- `:488-489` (StatefulSet 3/3, phase `OK`) are met by the terminating replica; the comment at
  `:487` ("Wait for cluster to recover (all 3 pods ready).") is false. The subtest ends 0.10-0.32 s
  after its start in 17 of 17 recorded legs.
- `:492-494` "delete replica does not trigger master failover" compares `findMasterPod`
  ([`rolling_update_test.go:733-750`](../../test/e2e/rolling_update_test.go)) with
  `initialMaster`. `findMasterPod` asks ordinal 0 first, and an old master keeps answering
  `role:master` for 10.3-10.5 s after a Sentinel failover (measured on 9.1.1 and 8.1.9), so this
  check cannot fail. It fails ADR 0017 D10 in substance.
- `:505` `waitForPodLabel(replicaPod, instanceRole, replica)` is met by the old pod's own label.
- `:520` `waitForConnectedReplicas` and the read at `:525-530` can be met by the old replica
  (vacuous in 2 of 17 legs).
- The comment at `:523-524` names `valkeyExecAllowError`; `:526` calls `valkeyExecQuick`.
- `:534` `waitForEndpointPodCount(-r, 2)` is sound: the EndpointSlice controller publishes a
  terminating pod not-ready, and the `-r` Service does not publish not-ready addresses.

**Site 2 - `TestE2E_SentinelStaleMaster`**
([`sentinel_stale_master_test.go`](../../test/e2e/sentinel_stale_master_test.go)). The test
deletes all three data and all three Sentinel pods (`:125-128`).

- The waits at `:131-136` are met by the old pods. The comment at `:130` ("Wait for all pods to
  come back.") is false, and the log line "All pods restarted and ready" at `:137` appeared
  0.34-0.62 s after the delete in 5 of 6 CI legs.
- The subtests at `:140-210` read by name. They read the replacements only because the old
  Sentinels stop answering within about a second of the delete, not because the fixture waits.

Sites checked and fine: `admission_recovery_test.go:237` (waits for `status.replicas == 0`),
`pod_termination_test.go:128` (observes termination on purpose, victim chosen by identity),
`topology_abandon_test.go:241` (gates on effects only the replacement can produce). The PDB
eviction waits at `pdb_test.go:102`, `:110` and `:274` are vacuous, but every attempt gates on
`DisruptionsAllowed > 0`, which excludes terminating pods; only their comments at `:100-101` and
`:272-273` promise a recovery the wait does not guarantee.

**Impact.**

- `E2E Tests` is a required check. A vacuous wait can turn it red for nothing and hold a PR until
  a rerun; this happened once, in `TestE2E_SidecarFailoverDrainMaster`, which now waits by UID.
  Sites 1 and 2 carry the same exposure with no failure observed in 17 legs.
- Site 1 does not guard what it names. The unit tier catches an outright removal of the replica
  role check at [`drain.go:114`](../../internal/sidecar/drain.go)
  (`TestDrainHandler_ReplicaExitsImmediately`), but no unit test runs a replica drain with
  `sentinelEnabled`, so a regression confined to the Sentinel path, a role misread against real
  Valkey, or a failover from another source after a replica delete passes both tiers.
- Site 2's stale-master assertions are protected by timing only.

## Required changes

### Independent of the open questions

- Comment fix: [`sidecar_test.go:523-524`](../../test/e2e/sidecar_test.go) names
  `valkeyExecQuick`; the reason (transient exec failure) stays.
- Site 1 effect read, replacing `:492-494`, lands with the Q1 fix:
  - Before the delete, read `config-epoch` from `SENTINEL MASTER` on all three Sentinels (reuse
    the key/value walk of `sentinelPeerCount`,
    [`sentinel_peer_table_test.go:134-149`](../../test/e2e/sentinel_peer_table_test.go)).
  - Directly after the identity wait and before any `waitForReplicaSynced(replicaPod)`
    ([`e2e_test.go:509-518`](../../test/e2e/e2e_test.go)): assert the epoch unchanged on all three
    (one read each, not a poll), then poll every ordinal, bounded, until exactly one pod answers
    `role:master` and it is `initialMaster`.
  - Why `config-epoch`: it rises from 0 to 1 on every Sentinel within 1.3 s of a completed
    failover (measured on both pinned lines), and nothing on site 1's path resets it
    (`resetSentinelState` runs only inside a roll). Placing it before the sync wait keeps a
    failover from failing on the wrong assertion (ADR 0017 D9, D10).
- Close ([ADR 0034](../adr/0034-tickets-are-work-lists-that-get-archived.md)): ADR 0017 D50 and
  its Status record the fixture fix (under E also that the shared waits refuse a terminating pod);
  drop "D50 (the fixture fix for two vacuous sites)" from
  [`docs/adr/README.md:109`](../adr/README.md); update `CLAUDE.md:282-284` ("two are vacuous");
  rewrite the T34 citations outside `docs/tickets/` to ADR 0017 D50 (shared with T40); then move
  this file to [archive/](archive/).

### Depends on the answers

- Under E: in [`e2e_test.go`](../../test/e2e/e2e_test.go), `waitForStatefulSetReady` keeps
  `ReadyReplicas == n` and additionally requires each pod `<name>-0` ... `<name>-(n-1)` to exist,
  be Ready and carry no `deletionTimestamp` (one pod List per poll); `waitForPodReady` returns false
  while the pod carries a `deletionTimestamp`. No edit at site 2.
- Under A: capture the UID before `sidecar_test.go:485` and call `waitForPodRecreated` before
  `:488`; capture six UIDs before `sentinel_stale_master_test.go:125` and call
  `waitForPodRecreated` six times before `:137`.
- Either way the comments at `sidecar_test.go:487`, `sentinel_stale_master_test.go:130` and the
  log line at `:137` become true.

### Tests that prove it

- On Kind, both Valkey lines:
  `make test-e2e E2E_RUN='TestE2E_SidecarDrainReplica|TestE2E_SentinelStaleMaster'`.
- Site 1: the delete subtest takes at least the replacement time in both single-node CI legs
  (durations recorded). Revert check: with the identity wait removed in a scratch copy, the
  subtest returns in under 1 s again.
- Site 1 mutation: in a scratch copy, issue `SENTINEL FAILOVER` after the identity wait with both
  replicas healthy, wait 2 s, run the effect read. The subtest must fail on the `config-epoch` or
  master-poll assertion, not on a sync or readiness wait. (Not directly after the delete: Sentinel
  clusters have no drain `preStop`, so the failover could pick the replica that is going away.)
- Site 2: "All pods restarted and ready" is logged only after six new pods; time from delete to
  that line recorded in both legs. Revert check as for site 1.
- Under E: full suite green on both Valkey lines on the fix commit (the `E2E Tests` legs), naming
  any test whose duration moved by more than the replacement time.
- `make lint` does not check e2e files until T43 lands; it proves nothing here.

## Open questions

### Q1: Is the fix made in the shared wait helpers or per site with UID waits?

Every fixture reaches for `waitForStatefulSetReady` or `waitForPodReady` after a delete, and both
are met by a terminating pod. The fix can make those two helpers refuse a terminating pod, or add
`waitForPodRecreated` at the two known sites only.

- **E - the shared waits refuse a terminating pod (recommended).** About 15 lines in
  `e2e_test.go`. Changes the meaning of all 120 calls in 22 files: a call that today returns while a
  pod of its range terminates now waits, at most the termination (75 s data, 30 s Sentinel, within
  the 5 min `testTimeout`). Fixes site 2 with no edit, makes the PDB comments true, and covers every
  future delete followed by the usual wait.
- **A - per-site UID waits at sites 1 and 2.** About 15 lines in two test files, no other test's
  timing moves. The shared waits stay vacuous after a delete, so every future delete relies on
  review against D50, and the PDB comments stay untrue.

Why E: the defect lives in the helpers, not the sites; every pre-existing delete site that was not
safe by construction used `waitForStatefulSetReady` and was vacuous, and the per-site rule was
already missed once after `waitForPodRecreated` existed. It is the same choice ADR 0026 made for
operator code: fix the accessor, not a list of sites. A is right only if a change of meaning across
120 calls is not acceptable; the full suites on both lines bound that risk under either option.

**Answer:** _open_

## Not verified

- Whether any of the 120 calls of the shared waits relies on returning while a pod terminates
  (matters for E); the full suite on both lines would show it.
- That the old Sentinels at site 2 stop answering within a second (one CI operator log); a Kind run
  logging Sentinel termination times next to the subtests at `:140-162` would settle it.
- Flake paths at site 1 (`getPod` at `:510` between removal and recreation; count and list read
  separately at `:534-535`) and site 2 (a read straddling the two pod generations at `:167`/`:173`):
  none observed in 17 legs.
- That a Sentinel-path replica-drain regression would fail over as fast as the master drain does:
  inferred, never run with a replica.
- `pod_termination_test.go:220-221` excludes the master from the victim choice by the
  `instanceRole` label, which lags a promotion; not observed, no work item until a Kind run logs the
  victim's `ROLE` next to the roll's failover time.

## Related

- T33 - the same class in the integration tier: a read a stale observer can satisfy.
- T35 - Sentinel master records lag the real master; source of the lag figures per command.
- T40 - the T34 citation lines outside `docs/tickets/` are on its list; whichever closes first
  rewrites them.
- T43 - `make lint` and `go vet` skip e2e files.
- T62 - `resetSentinelState` restarts `config-epoch` at 0; site 1 runs no roll, so its effect read
  is unaffected.
- T68 - edits `valkeyExec` in `e2e_test.go`, where E edits the readiness waits; one full e2e run can
  serve both.
- T70 - the ADR 0026 sentence about the drain `preStop` on every topology.
