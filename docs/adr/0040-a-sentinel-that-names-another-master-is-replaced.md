# ADR 0040: A Sentinel that keeps naming another master than its tier is replaced

## Status

Accepted. Date: 2026-11-01.

Implemented: the per-Sentinel master address in the health pass (D1), the poll of a healthy
Sentinel cluster (D2), the `SentinelMasterDiverged` condition (D3), the replacement and its gates
(D4, D5) and its Event (D6).

Verified in the unit tier (`internal/controller/sentinel_master_divergence_test.go`, the
`SentinelMasters` and `MasterCandidates` tests in `internal/health/checker_live_test.go`): every
gate of D4 and the clock refuse or allow the delete as stated, and 11 of 11 mutations of them
(each gate, the bound, the restart of the clock, the live look) are killed by the test named for
it. On a local Kind cluster (Valkey 9 line, the operator image built from this change) the e2e
`TestE2E_SentinelNamingAnotherMasterIsReplaced` (`test/e2e/sentinel_master_divergence_test.go`)
passed, and so did `TestE2E_RollingUpdate_HA`, `TestE2E_RollingUpdate_HA_WritesDuringHandover`,
`TestE2E_PodMetadataChangeRollsBothTiers` (its clean-roll subtest asserts no Warning Event),
`TestE2E_SentinelStaleMaster`, `TestE2E_SentinelPeerTableSurvivesPodReplacement` and
`TestE2E_TLS_HAClusterWithSentinel`; across those six the operator logged no replacement and no
refusal. Not run: the full suites, the Valkey 8 line, the multi-node leg. What the new e2e does
and does not prove is under Residual risks.

## Context

Measured on `wds18-k8s-main`, `database-examples/valkey9-sentinal-tls` (Valkey 9.0, TLS, three
data pods, three Sentinels), on 2026-11-01:

- Node `k8s-main-lambda` was not Ready until 00:17:42 UTC; the master, data pod 2, ran there. At
  00:05:47 the Sentinels failed over to data pod 0 at config epoch 1566. No rolling update was
  involved: no Sentinel logged a `-monitor`/`+monitor` after it.
- Sentinel 2, on the same node and in TILT for most of that window, logged at 00:06:21
  `+config-update-from` and `+switch-master … -2 … -0`, and at 00:06:40
  `Failed to resolve hostname 'valkey9-sentinal-tls-0.valkey9-sentinal-tls-headless…'`.
- Seven hours later `SENTINEL MASTER` answered: Sentinels 0 and 1 name data pod 0, config epoch
  1566, flags `master`; Sentinel 2 names data pod 2, **config epoch 1566**, flags
  `s_down,master`, `role-reported slave`. Its config file still read `config-epoch 0`.
- Read in the Valkey source (`src/sentinel.c`, branches `9.0` and `unstable`): the hello handler
  assigns `primary->config_epoch = primary_config_epoch` and only then calls
  `sentinelResetPrimaryAndChangeAddress`, whose return value it ignores; that function returns
  `C_ERR` when `createSentinelAddr` cannot resolve the hostname. Every later hello carries epoch
  1566, which is not greater than 1566, so the switch is never retried. The Sentinel recovers on a
  higher epoch, which takes another failover, or on a restart, which reloads the file.
- Also read there: `SENTINEL is-master-down-by-addr` looks the master up by address, so the stuck
  Sentinel neither confirms the real master's failure nor votes on its failover. The tier looked
  like three Sentinels and failed over like two, with no margin left. A client that asks it for
  the master receives a replica.
- Nothing in this repository reported it. `observeSentinels` counted the Sentinels whose flags
  were exactly `master` against a majority and never compared addresses, so two of three passed;
  the CR read `Ready=True`, phase `OK`. The observer did see it (`sentinelQuorumFailure`), and
  that reaches the CR only as `status.observerReady: false`.

The same state needs no DNS failure: any Sentinel that names a wrong address at the same config
epoch as its peers stays there. A fresh monitor starts at epoch 0 (measured: the config file
after `+monitor`), and `resetSentinelState` removes and re-adds the monitor on every Sentinel.

## Decision

**D1 — The health pass keeps each Sentinel's whole reply.** `observeSentinels` stores the
`SENTINEL MASTER` answer per pod in `ClusterState.SentinelMasters`, and `CheckCluster` counts the
data pods answering role master in `ClusterState.MasterCandidates`. No connection is added.

**D2 — A healthy Sentinel cluster is measured every 5 minutes at the latest.** Every pass that
reaches `CheckCluster` on a Sentinel cluster requests a recheck after
`sentinelStateRecheckInterval` (5 min). Sentinel state changes no Kubernetes object: a failover
across a network partition, or one run by hand, starts no pass, and the next measurement was the
10 h cache resync.

**D3 — `SentinelMasterDiverged` is a level with one evaluator.** `recordSentinelMasterDivergence`
sets it `True`, reason `SentinelNamesAnotherMaster`, when an answering Sentinel names an address
other than `ClusterState.MasterAddress`, naming each such pod and the address it names, sorted;
`False`, reason `SentinelMastersAgree`, when every answering Sentinel names it. A pass in which no
Sentinel answered writes nothing (ADR 0022 D5). While it is `True` the cluster is rechecked every
30 s (`sentinelMasterDivergenceRecheckInterval`).

**D4 — The pod of a Sentinel that kept naming another master for 2 minutes is replaced, under
gates.** `handleSentinelMasterDivergence` acts on a diverged Sentinel once it has been seen
diverged on every measurement for `sentinelMasterDivergenceBound` (2 min), and only when all hold:

- exactly one data pod answers master (`MasterCandidates == 1`);
- no data-tier rolling-update state is recorded — the roll owns the Sentinel monitors and the
  failover;
- the target's own flags do not carry `failover_in_progress` — a failover leader names the old
  master until the replicas are reconfigured;
- a strict majority of the configured Sentinels names that master with flags exactly `master`;
- the Sentinel StatefulSet and the pod are proven ours (ADR 0020), no Sentinel pod is outdated
  (the roll replaces those), the quorum guard `sentinelDeleteKeepsVotes` holds with the target
  counted as a vote, and no Sentinel pod is terminating in the cached sweep or in the live look
  right before the delete (ADR 0026 D5).

One pod per pass. The clock is in memory (`sentinelDivergenceTracker`): it starts at the first
measurement that sees the divergence, is dropped on a measurement that does not, and restarts at
every attempt, whatever the attempt achieved.

**D5 — The replacement is a pod delete, never a Sentinel command.** The new pod's init container
confirms the master by `ROLE` and scans the data pods otherwise (`buildSentinelInitCommand`), the
Sentinel boots at config epoch 0, and the first hello of a peer gives it the epoch and the address
the majority names. It converges to the majority, not to the operator's reading. Its identity is
pinned to the ordinal, so the peers switch the address instead of adding a voter (ADR 0022 D1).

**D6 — Each attempt emits one `SentinelMasterDiverged` Warning** naming the Sentinel, the address
it named and the master, and whether the delete succeeded. A clean rolling update emits none: the
roll-state gate keeps the replacement out of it.

## Consequences

- Every healthy Sentinel `Valkey` is reconciled every 5 minutes: a full pass, with the cached
  reads of every owned object, one `INFO replication` per data pod and one `SENTINEL MASTER` per
  Sentinel. Before, it was reconciled on owned-object events and the 10 h resync only.
- Every Sentinel `Valkey` gains the condition, as `False` on its first measured pass: one status
  write, no roll, one more `vko_valkey_status_condition` series (ADR 0021).
- A replacement costs that Sentinel for its restart. It had no vote for the real master before,
  so the failover margin does not shrink; the phase reads `Provisioning` until the pod is Ready.
- The repair is slow by design: up to 5 minutes until the first measurement when nothing else
  triggers a pass, then the 2-minute bound and up to 30 s to the next recheck.
- A `True` that persists means a gate held, and the operator log says which ("Not replacing a
  Sentinel that names another master", with its reason).

## Alternatives Considered

**Report only**, as `SentinelPeersStale` does (ADR 0022 D6). It leaves the tier without its
spare vote until a human acts, and the observer had reported exactly this for seven hours without
anybody acting.

**`SENTINEL REMOVE` + `SENTINEL MONITOR` on the diverged Sentinel.** Fast, no pod churn, and
`resetSentinelState` already has the sequence. Lost because `SENTINEL MONITOR` resolves the
hostname and refuses one it cannot resolve ("Invalid IP address or hostname specified", read in
`src/sentinel.c`): in the DNS failure that produces this state the removal succeeds and the
monitor is not re-added. A Sentinel without a monitor answers `No such master`, which the health
pass reads as "not responding", so nothing would report or retry it. A pod that cannot resolve at
boot fails visibly and recovers on its own.

**`SENTINEL RESET`.** Keeps the address the Sentinel already has (ADR 0022 Context), which is
the wrong one, and ADR 0022 D6 keeps the operator from issuing it.

**A failover, to raise the epoch.** Moves the data-plane master to repair a Sentinel.

**A liveness probe on the Sentinel container.** A container restart keeps the `emptyDir` config;
whether that file carries the new epoch with the old address depends on whether Sentinel flushed
it after the failed switch. On wds18 it had not, and any later flush makes the restart useless. A
probe on "my master reports slave" also fires in every legitimate failover window.

**Announcing IPs instead of hostnames.** Takes DNS out of the hello path, and gives up the
hostname posture of the generated configuration (`resolve-hostnames`, `announce-hostnames`), which
the observer checks for (`checkSentinelMasterHostname`).

**An upstream fix.** Taking the epoch only after a successful address change is the real fix of
the cause. It is outside this repository and helps no released image; this record does not depend
on it and does not prevent it.

**Detection on events alone, without D2.** Misses every failover that changes no Kubernetes
object.

## Residual risks

- **A majority stuck on the same wrong address is not repaired**, only reported. It needs two of
  three Sentinels to fail the same way.
- **Only the branch of `updateHAStatus` where every pod is Ready measures**, as for ADR 0022: a
  cluster stuck in `Provisioning` or `Error` is not measured.
- **The address comparison assumes `announce-hostnames`.** `generateSentinelConf` sets it, and
  the reply's `ip` was the pod FQDN on wds18 (one TLS cluster, measured). A Sentinel
  answering with an IP would be reported as diverged, and replaced only if a majority still
  answers with the FQDN.
- **An operator restart restarts every clock**, delaying a replacement by one bound.
- **A replacement during an ongoing DNS outage** leaves the new pod failing to start until DNS
  returns. Not measured.
- **The `podIsOurs` check on the target is not separately tested**: the Sentinel scan before it
  already proves every ordinal, so a mutation of it is not killed. It stays as the proof on the
  object that is deleted.
- **The upstream failure itself was not reproduced.** The e2e builds the equal-epoch state by
  command on a fresh cluster (every Sentinel at epoch 0, one pointed at a replica), checks that
  its peers do not correct it for 20 s, starts a pass by annotating the Sentinel StatefulSet —
  D2's poll is covered by the unit tier, not by the e2e — and then asserts the condition, the
  replacement, every Sentinel naming the master, the cleared condition, the Event, an unchanged
  master and a replicated write. It passed once on Kind, Valkey 9 line, without TLS; the
  comparison of TLS addresses (`host:16379` on both sides) is verified by reading the code and the
  live wds18 reply, not by an e2e that holds a TLS Sentinel diverged.

## References

- [`internal/controller/sentinel_master_divergence.go`](../../internal/controller/sentinel_master_divergence.go) — `handleSentinelMasterDivergence`, `recordSentinelMasterDivergence`, `divergenceReplacementRefusal`, `replaceDivergedSentinel`, `divergedSentinelPod`, `sentinelDivergenceTracker`, the three intervals
- [`internal/health/checker.go`](../../internal/health/checker.go) — `ClusterState.SentinelMasters`, `ClusterState.MasterCandidates`, `observeSentinels`
- [`internal/controller/valkey_controller.go`](../../internal/controller/valkey_controller.go) — `updateHAStatus`, the call site
- [`internal/controller/rolling_update.go`](../../internal/controller/rolling_update.go) — `scanSentinelPods`, `sentinelDeleteKeepsVotes`, `liveTerminatingPod`, the gates reused
- [`internal/builder/sentinel.go`](../../internal/builder/sentinel.go) — `buildSentinelInitCommand`, which validates the master a replaced Sentinel boots on
- [`api/v1/valkey_types.go`](../../api/v1/valkey_types.go) — `ConditionTypeSentinelMasterDiverged`
- [`internal/controller/sentinel_master_divergence_test.go`](../../internal/controller/sentinel_master_divergence_test.go), [`test/e2e/sentinel_master_divergence_test.go`](../../test/e2e/sentinel_master_divergence_test.go)
- [ADR 0022](0022-sentinel-identity-is-pinned-to-the-pod.md) — the pinned identity a replacement keeps, and the never-RESET rule
- [ADR 0026](0026-a-pod-being-deleted-is-not-available.md) — the delete gate
- [ADR 0020](0020-write-only-what-the-operator-owns.md) — the provenance proof on the pod
- [ADR 0024](0024-the-sentinel-tier-reports-its-own-completion.md) — the quorum guard of the Sentinel roll
- [ADR 0027](0027-conditions-are-levels-edges-or-history.md) — the registry row of the condition
