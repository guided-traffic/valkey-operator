---
id: T74
title: No condition reports a Sentinel that has no monitor, or knows too few peers or replicas
state: analysed       # facts read and the rebuild window measured on both pinned images; both decisions carry a recommended option
severity: medium      # a tier that cannot fail over stays invisible until a master failure makes it an outage; the defect hides the states, it does not produce them
security: hardening   # the states arise with no principal; a report only detects, it opens or closes no path
threat: "no attack path is needed for the defect; a report would additionally surface a Sentinel whose monitor was removed or redirected by anyone who can send Sentinel commands (any client on port 26379/36379 with spec.sentinel.disableAuth true, or a holder of the cluster password)"
urgency: now          # rule 1: the SentinelPeersStale all-clear message states agreement the code does not measure; recompute (rule 3 gives next) once Required change 1 lands
effort: M             # typed reply error, per-Sentinel health fields, one evaluator with a debounce, a new condition with registry row, tests, ADR 0022, docs and one alert
blocked-by: decision  # Q1 and Q2; Required changes 1 and 2 need none
filed-from: T62
opened: 2026-09-27
decided:
done:
---

# T74 - No condition reports a Sentinel that has no monitor, or knows too few peers or replicas

## Current state

The health pass reads the Sentinels only when every Valkey and Sentinel pod is Ready
([`valkey_controller.go:2458-2462`](../../internal/controller/valkey_controller.go#L2458-L2462))
and a master was found ([`checker.go:108-138`](../../internal/health/checker.go#L108-L138)).
`observeSentinels` ([`checker.go:295-345`](../../internal/health/checker.go#L295-L345)) sends
`SENTINEL MASTER <monitor>` to every Sentinel and keeps only `num-other-sentinels` and whether
`flags` is exactly `master`. The only condition written from it, `SentinelPeersStale`
([ADR 0022](../adr/0022-sentinel-identity-is-pinned-to-the-pod.md) D4, D5), looks for a **surplus**
of peers (`staleSentinelPods`,
[`valkey_controller.go:2415-2424`](../../internal/controller/valkey_controller.go#L2415-L2424)).
Nothing looks for a deficit:

- **No monitor.** Such a Sentinel answers `ERR No such master with that name`. `Client.exec`
  turns every RESP error into a plain error
  ([`client.go:479-481`](../../internal/valkeyclient/client.go#L479-L481)), so the checker cannot
  tell it from a dropped connection and skips the pod as "not responding"
  ([`checker.go:330-334`](../../internal/health/checker.go#L330-L334)).
- **Too few peers.** A Sentinel knowing fewer than `replicas - 1` others is never selected.
- **Too few replicas.** `num-slaves` is parsed into `SentinelMasterInfo.NumSlaves`
  ([`client.go:37-52`](../../internal/valkeyclient/client.go#L37-L52)) and never carried into
  `health.ClusterState`.

Further facts the change depends on:

- **False all-clear.** `recordSentinelPeerDrift` writes `False`/`SentinelPeersConsistent`,
  "Every Sentinel knows %d other Sentinels, as expected"
  ([`valkey_controller.go:2372-2381`](../../internal/controller/valkey_controller.go#L2372-L2381)),
  also when a Sentinel knows 0 peers or has no monitor. The same overstatement is in the type doc
  comment ([`valkey_types.go:117-119`](../../api/v1/valkey_types.go#L117-L119)) and ADR 0022 D5.
- **Unread field.** `ClusterState.SentinelMonitoring`
  ([`checker.go:41-42`](../../internal/health/checker.go#L41-L42), set from `monitoring()`) is read
  by no production code; only health-package tests assert through `monitoring()`.
- **The pod stays Ready.** The Sentinel readiness probe is `PING`
  ([`sentinel.go:420-446`](../../internal/builder/sentinel.go#L420-L446)); a monitor-less Sentinel
  answers `PONG`, so the operator never replaces it.
- **No alert, no metric.** None of the eight shipped alerts
  ([`prometheusrule.yaml`](../../deploy/helm/valkey-operator/templates/prometheusrule.yaml)) reads
  Sentinel state. Every condition is already exported as `vko_valkey_status_condition`
  ([`collector.go:186-192`](../../internal/metrics/collector.go#L186-L192)), so a new condition is
  alertable with no collector change; ADR 0021 forbids a per-Sentinel gauge.
- **Sentinel never forgets a replica.** `num-slaves` counts dead entries too, and replicas announce
  by hostname, so the count below `spec.replicas - 1` while the master lists every replica means
  the Sentinel does not read that master, was just reset, or has not run its next INFO (at most
  10 s) after a scale-up.

**Legitimate transients** (measured in docker on `valkey/valkey:9.1.1` and `8.1.9`, no TLS):

- REMOVE + MONITOR, or a replaced Sentinel: 0 peers for about 1 to 2.3 s; 0 replicas for at most
  0.11 s when auth is already set, about 0.6 s longer when `auth-pass` is set late (it is the
  eighth command of `resetSentinelState`).
- `SENTINEL RESET` (the documented `SentinelPeersStale` remedy): 0 replicas for 9.4 to 9.9 s,
  bounded by upstream's 10 s INFO period.
- The completing pass of a data-tier-only roll (for example `spec.resources`) runs
  `resetSentinelState` ([`rolling_update.go:1049`](../../internal/controller/rolling_update.go#L1049))
  and then the health pass milliseconds later, and schedules no follow-up
  ([`valkey_controller.go:391-396`](../../internal/controller/valkey_controller.go#L391-L396)). An
  image-change roll ends that pass on the Sentinel roll and does not read the window. A replaced
  Sentinel is not read inside its window (readiness starts after 5 s).

**Durable states** (measured in docker, both images, T62 scenarios): no monitor (`ERR No such
master`, `PING` answers `PONG`, never recovers); reset toward an unreachable master (all Sentinels
`s_down,master`, 0 replicas, 0 peers, failover answers `NOGOODSLAVE`); monitor pointed at a
replica (0 replicas, 2 peers).

**Impact.** Sentinel-enabled clusters only. A tier that cannot fail over shows `Ready=True`,
`phase=OK` and `SentinelPeersStale=False` with a message claiming agreement. Two monitor-less
Sentinels of three lose the quorum. On a cluster without persistence a failover that cannot
happen may end in an empty master flushing its replicas (T36 route, not measured).

## Required changes

### Independent of the open questions

1. **Correct the all-clear text.** The `False` message at
   [`valkey_controller.go:2379-2380`](../../internal/controller/valkey_controller.go#L2379-L2380)
   becomes "No Sentinel knows more than %d other Sentinels"; the doc comment at
   [`valkey_types.go:117-119`](../../api/v1/valkey_types.go#L117-L119) and ADR 0022 D5 say it
   clears once no Sentinel knows more than `replicas - 1` others. Unit test in
   [`sentinel_peer_drift_test.go`](../../internal/controller/sentinel_peer_drift_test.go) asserts
   the message with one Sentinel at 0 peers, and fails with the old message.
2. **Delete the unread field** (in the same change as Q1's implementation, because the tests need
   the new fields): `ClusterState.SentinelMonitoring`, `sentinelObservation.agreeing`,
   `monitoring()`; move the assertions in `checker_live_test.go`, `checker_paths_test.go`,
   `checker_test.go` and `valkey_controller_test.go` onto the per-Sentinel fields.

### Depends on the answers

3. A typed reply error in `valkeyclient` (RESP text kept in `Error()`); only
   `No such master with that name` counts as monitor-missing, a transport failure or any other
   reply error (for example an auth refusal) stays "did not answer".
4. `NumSlaves` and the monitor-missing set carried in `ClusterState`; one evaluator in
   `updateHAStatus` next to `recordSentinelPeerDrift`, with the debounce of Q2 and a 5 min recheck
   while True (as ADR 0022 D7).
5. The `ConditionType` and reasons in `api/v1` and a `conditionRegistry` row (level, one
   evaluator, not presence-guarded, the debounce declared in its prose).
6. Alert `ValkeySentinelMonitorDegraded` in the shipped PrometheusRule: condition-series
   expression like `ValkeyReconcileBlocked`, `for: 15m`, severity warning as `ValkeyReplicasMissing`.
7. Documents: ADR 0022 amended (a decision for the condition, one for the debounce, Residual risks:
   no report when the master is not found or not every pod is Ready, a wrong address with
   `spec.replicas: 1`, no address comparison, a restart restarts the clock); README condition table;
   `docs/operations/status.md` and `docs/operations/monitoring.md`; one sentence in the Sentinel
   section of `CLAUDE.md`.
8. Tests:
   - Unit (fake Sentinel plumbing, `NewValkeyClientFn`, `fakeValkeyServer(t)`): the `No such
     master` reply counts as monitor-missing, a refused connection and an auth refusal do not;
     0 peers raises its reason; replicas short raise it only with `AllSynced`; no Sentinel
     answering writes nothing over a standing `True`. Each fails with its clause removed.
   - Unit, debounce: a deficit seen once writes no `True` and requests a recheck within the bound;
     past the bound it writes `True`; no deficit writes `False` and drops the tracker entry.
   - `TestConditionRegistryCoversEveryConditionType` red without the row.
   - E2E on both single-node legs: a data-tier-only roll on a Sentinel cluster never shows `True`;
     a new e2e runs `SENTINEL REMOVE <monitor>` on one Sentinel pod, expects `True`/
     `SentinelMonitorMissing` naming that pod within bound plus recheck, deletes the pod and
     expects `False`.
   - `make test-unit`, `make test-integration`, `make lint`, `make cyclo`, `make generate-all`
     with no diff.

## Open questions

### Q1: Are the deficits reported on the existing `SentinelPeersStale` or on a new condition?

The health pass already receives per Sentinel the error or the reply with `flags`, `num-slaves`
and `num-other-sentinels`. Either way the change sends no new command, repairs nothing (ADR 0022
D6) and leaves `Ready` and `phase` alone.

- **A - widen `SentinelPeersStale`:** new reasons for missing monitor, peers and replicas. Cost S.
  One condition then carries contradicting remedies (`SENTINEL RESET` fixes a surplus but is a
  no-op for a missing monitor), "stale" describes an empty table, and the meaning of a released
  condition changes under existing alerts.
- **B - a new level condition `SentinelMonitorDegraded` (recommended):** `True` when an answering
  Sentinel has no monitor (`SentinelMonitorMissing`), knows fewer than `replicas - 1` peers
  (`SentinelPeersMissing`) or fewer than `spec.replicas - 1` replicas while `AllSynced`
  (`SentinelReplicasMissing`); one reason per pass in that order, the message naming every pod;
  `False`/`SentinelMonitorsComplete` otherwise; nothing written when no Sentinel answered. Cost M.
  Every Sentinel CR gains the condition as `False` after the upgrade (status write, no roll).

B keeps one remedy per condition and covers exactly the states in which a tier cannot fail over,
read from the reply the pass already gets. The name may be changed.

**Answer:** _open_

### Q2: How does the condition stay silent through a legitimate rebuild?

A routine data-tier-only roll reads the Sentinels inside the rebuild window and schedules no
follow-up, and a manual `SENTINEL RESET` shows up to 10 s of 0 replicas to any pass. Without a
debounce the condition goes `True` at the end of every such roll and stays until the next event
or the 10 h resync.

- **A - withhold until the deficit outlived 90 s, clock in memory (recommended):** the first pass
  that sees a deficit stores a first-seen time in a per-CR tracker (namespace and name, the
  `nudgeTracker` shape) and calls `requestRecheck(90s)`; a later pass still seeing it writes
  `True`; a clean pass forgets it and writes `False`. 90 s matches `sentinelAwarenessTimeout` and
  is about nine times the longest measured window. An operator restart restarts the clock.
- **B - `True` at first sight:** either a transitional reason turned into a persisted one after
  the bound (the ADR 0025 shape), or plain `True` with the debounce left to the alert's `for:`
  (the `TLSMaterialStale` precedent, cost S). The clock survives a restart, but the condition goes
  `True` on every data-tier-only roll and every manual RESET, so Lens and user alerts on
  `status="True"` fire on routine work.

A: the states worth reporting last until someone acts, so a report one bound late loses nothing,
while B shows a `True` on exactly the pass that is certain to see the transient.

**Answer:** _open_

## Not verified

- The rebuild window on Kubernetes with TLS, a dial per command and CoreDNS; settled by the new
  e2e of Required change 8.
- Whether the completing pass of a data-tier-only roll actually reads a Sentinel inside the
  window on a real cluster (by reading only); settled by the same e2e.
- How often the durable states occur on the fleet; settled by inspecting operator logs of a
  stalled finalization or a timed-out failover.

## Related

- [T62](062-resetsentinelstate-falls-back-to-sentinel-reset.md) - its reset paths produce the
  states; neither ticket blocks the other, and T62 does not remove the need for Q2 (manual RESET,
  scale-up).
- [T36](036-non-persistent-master-restarts-empty.md) - a tier that cannot fail over is one route
  into its data-loss mechanism.
