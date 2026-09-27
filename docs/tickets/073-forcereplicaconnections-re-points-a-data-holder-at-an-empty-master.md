---
id: T73
title: forceReplicaConnections re-points every other pod at the new master with no role or dataset check, in the same pass in which the ADR 0028 guard refused that demotion
state: analysed       # call path read end to end, Valkey side measured on both pinned images, one open decision with a marked option
severity: high        # every copy of the dataset outside the promoted pod is replaced by its dataset, empty in the refusal shape, and the cluster ends phase OK; persistence does not protect
security: none        # the operator's own REPLICAOF breaks a data-integrity rule (ADR 0028 D1); no principal gains a verb or an object
urgency: now          # rule 1: ADR 0028 and code and test comments call this path safe, which the measured Valkey behaviour contradicts; next once the comment and ADR corrections land
effort: S             # one veto in forceReplicaConnections reusing existing helpers, unit tests, comment and ADR corrections
blocked-by: decision  # Q1
filed-from: T62
opened: 2026-09-27
decided:
done:
---

# T73 - forceReplicaConnections re-points every other pod at the new master with no role or dataset check, in the same pass in which the ADR 0028 guard refused that demotion

## Current state

**The function.** `forceReplicaConnections`
([`rolling_update.go:3192`](../../internal/controller/rolling_update.go#L3192)) sends
`REPLICAOF <named master> <port>` to every pod that exists and is Ready, terminating pods
included, except the named master (skip at
[`:3210`](../../internal/controller/rolling_update.go#L3210)). It asks no pod its role and reads
no key count. A failed `REPLICAOF` is logged and skipped. It has two callers, `:983`
(`checkFinalizationTopology`) and [`:3159`](../../internal/controller/rolling_update.go#L3159)
(`handleMasterWithNoReplicas`).

**The path to the discard at `:3159`** (one pass of `handleRollingUpdate`, Sentinel clusters only):

1. `collectPodStates` marks a pod `isMaster` on `INFO role:master` or, if `INFO` fails, on its label
   ([`rolling_update.go:1951-1962`](../../internal/controller/rolling_update.go#L1951-L1962)).
2. The resolver runs with Sentinel's master pointer as the authority
   ([`rolling_update.go:714-715`](../../internal/controller/rolling_update.go#L714-L715)); it is
   skipped only while `ownFailoverInFlight` is true (`:797-822`), the negation of the
   `isReplicaReconnectTimedOut` predicate that gates `:3159` (`:3150`). So past 90 s it has run.
3. `demoteRogues` asks `demotionRefusalReason`
   ([`rolling_update.go:1713-1736`](../../internal/controller/rolling_update.go#L1713-L1736)):
   an authority with zero keys while the rogue holds some, or an unreadable count, is a refusal.
   The refusal is logged and kept only on the local pod slice.
4. `handlePostFailover` discards that slice (`_ []podState`,
   [`rolling_update.go:3071`](../../internal/controller/rolling_update.go#L3071)), re-scans, and
   takes the first available current-template pod answering `role:master` as the new master X.
5. X with `connected_slaves:0`, 90 s past the failover timestamp: `forceReplicaConnections` at
   `:3159` re-points the refused old master O and every replica holding its copy at X; then
   `resetSentinelState` toward X (`:3161`).

**Cases that reach `:3159`:**

| Case | Guard in the pass | Outcome |
|---|---|---|
| O counted master, authority X, X empty, O holds keys | refuses | `REPLICAOF X` on O anyway |
| a key count unreadable | refuses (fail-closed) | `REPLICAOF X` on O anyway (if the TLS config cannot be built, nothing is sent) |
| authority is O | demotes X toward O | `:3159` not reached |
| O not counted master (`INFO` failed, label not master) but Ready | not run (one master) | `REPLICAOF X` on O |
| the 90 s boundary falls between `:797` and `:3150` | suppressed | `REPLICAOF X` on O |
| a replica holding a copy of O's data | covers masters only | the copy is discarded |

**What seals the loss.** On the next pass, or in the same pass when the reset count is spent
(`:3163-3174`), `replaceRemainingPods` asks `verifyNewMasterReady`
([`rolling_update.go:3331-3397`](../../internal/controller/rolling_update.go#L3331-L3397)), which
requires only a connected replica and a readable `DBSIZE`, then deletes the outgoing pod
([`:3034`](../../internal/controller/rolling_update.go#L3034)).

**How X can be empty.** The failover retrigger `handleFailoverRetrigger`
([`rolling_update.go:849-887`](../../internal/controller/rolling_update.go#L849-L887)) does not
wait for synced replicas, and Sentinel selects a replica without reading a key count. A promoted
pod without persistence that restarts boots as an empty master
([`statefulset.go:288-332`](../../internal/builder/statefulset.go#L288-L332)). The `:3159`
precondition (no replica attached to X for 90 s) is itself the case in which Sentinel did not
demote O.

**Measured (docker, `valkey/valkey:9.1.1` and `8.1.9`):**

- M1, the `:3159` shape: O master with 500 keys, R its replica, X an empty master. `REPLICAOF X`
  on O and R answers `OK`; X has `connected_slaves:2` at +1 s; O and R hold 0 keys by +7 s. O
  logs "Flushing old data", "keys loaded: 0".
- M2: Sentinel (`SENTINEL FAILOVER`) promotes an empty replica X and converts the old master O
  into its replica within 20 s; O ends with 0 keys.
- M3, persistence: O with the operator's `rdb` or `aof` lines
  ([`configmap.go:187-246`](../../internal/builder/configmap.go#L187-L246)) re-pointed at an empty
  X, then restarted without `replicaof`: O boots master with `DBSIZE` 0 in all four runs. The full
  sync replaces the files on disk as well as the memory.

**Records that say the opposite:** ADR 0028 Residual risks (`:284`, "The Sentinel path is
protected by D1"); the comments at
[`rolling_update.go:3143-3144`](../../internal/controller/rolling_update.go#L3143-L3144) and
`:3166-3167` ("so this is safe"); the test texts at
[`sentinel_failover_test.go:1279-1281`](../../internal/controller/sentinel_failover_test.go#L1279-L1281)
and `:1301`; the "non-master pod" wording at `:3189-3190`, `:3137`, `:3157` and `:975`, which the
code does not check. ADR 0025 (`:215-217`) and ADR 0028 D8 (`:206-209`) describe the send
without saying it bypasses D1.
`TestHandleMasterWithNoReplicas_ForcesReconnectAndResetsSentinelOnTimeout`
([`sentinel_failover_test.go:1246-1277`](../../internal/controller/sentinel_failover_test.go#L1246-L1277))
pins today's behaviour with no key count in play.

**Impact.** Sentinel clusters during a data-tier roll, no-replica branch past 90 s. When the
resolver refuses to demote O toward an empty X, `:3159` does exactly that demotion and re-points
every replica copy too; the next delete completes the roll and the cluster reads phase `OK`
with no data. The only trace is an info log line ("Refusing to demote ..." then "Sent REPLICAOF
to pod"). Not affected: clusters without Sentinel, rolls whose new master gets a replica within
90 s, a new master that holds the data (there the call repairs, as intended). No change here
touches a pod template, so nothing rolls.

## Required changes

**Independent of the open question:**

- Correct the doc comment of `forceReplicaConnections` (`:3189-3191`) and the "non-master"
  wording at `:3137`, `:3157` and `:975`: it re-points every Ready pod except the named master.
- Correct `:3143-3144` and `:3166-3167` and the test texts at `sentinel_failover_test.go:1279-1281`
  and `:1300-1301`: `verifyNewMasterReady` gates on replication, not on the dataset. T75 edits the
  same lines; one change corrects both halves.
- ADR 0028 Residual risks (`:284`): mark the "protected by D1" statement in place as not holding
  at `handleMasterWithNoReplicas`.

**Depends on the answer to Q1 (option B):**

- Veto in `forceReplicaConnections`, reusing `dbSizeReader` (`:1681-1693`) and
  `demotionRefusalReason` (`:1713-1736`). It covers both callers.
- ADR 0028 D1/D4 name the second `REPLICAOF` site; ADR 0028 D8 (`:206-209`) and ADR 0025
  (`:215-217`) say "subject to the dataset veto"; the comments corrected above state the veto.
- Unit tests with one fake server per pod carrying a key count (fleet helper in
  `split_brain_dataset_test.go:51`): named master with keys - all re-pointed; both empty - all
  re-pointed; empty master and one data-holding target - none; empty master and a data holder
  that is not Ready - none; an unreadable count - none. The last three fail with the veto
  removed; a per-target-skip mutation fails the mixed case; a targets-only mutation fails the
  not-Ready case.
- A pass-level test: two masters, Sentinel names the empty one, failover stamp older than 90 s;
  after `handleRollingUpdate` the data holder received no `REPLICAOF`. Run it before the fix to
  prove it reproduces.
- Give `TestHandleMasterWithNoReplicas_ForcesReconnectAndResetsSentinelOnTimeout` key counts.
- `make test-unit`, `make lint`, `make cyclo`; full e2e on `single-node-valkey9` and
  `single-node-valkey8` (the benign case must still re-point). No e2e can make Sentinel promote
  an empty pod deterministically.

## Open questions

### Q1: What does `forceReplicaConnections` do when the master it names holds no keys and a pod it would re-point holds some?

Today it re-points everyone. The choice decides whether ADR 0028's dataset rule also binds this
second `REPLICAOF` site. Either way the benign case (named master holds keys) keeps re-pointing
everyone, and a refused shape holds as two visible, writable masters (`MultipleMasters`,
`SplitBrainDetected` after 90 s), the trade ADR 0028 D3 and D8 already accepted.

- **A - veto per target:** skip each target `demotionRefusalReason` refuses, re-point the rest.
  Cost XS-S. An empty target (a restarted replica, the usual company of an empty promotion) still
  attaches, `verifyNewMasterReady` passes, and the protected data holder is deleted one pass later.
- **B - veto the whole call (recommended):** read the named master's `DBSIZE` once; if it holds
  keys, proceed as today. If it holds none or is unreadable, count every other existing pod,
  including non-Ready ones (the outgoing pod is deleted whatever its readiness); if any holds keys
  or cannot be counted, send no `REPLICAOF` this pass and log once which pod holds what. Cost S;
  the no-replica branch then keeps cycling every 90 s, and after `maxReconnectResets`
  `replaceRemainingPods` waits for a replica the operator no longer sends. An empty cluster with
  one pod not answering waits until it answers.

B is recommended because A's progress is the failure: the empty pod it re-points unlocks the
delete of the data holder it just spared. B keeps the topology where the resolver left it, costs
nothing in the common case, and matches ADR 0028 D3 (an unreadable count is a refusal). B does not
close the delete when a pod attaches to X by another route; that is T79.

**Answer:** _open_

## Not verified

- The operator path end to end: read only, no `go test` or Kind run; the pass-level unit test
  above settles it.
- How often X is empty in practice: the retrigger and non-persistent restart routes are by
  reading; neither was produced.
- The tiebreak variant (X and O both at `connected_slaves:0`, no Sentinel answering,
  `mostConnectedMaster` `:1622-1638`): read, not produced.
- Persistence on a PVC-backed pod: M3 measures it in docker only.

## Related

- [T79](079-the-sentinel-roll-deletes-the-former-master-with-no-key-count-gate.md) - the delete behind `verifyNewMasterReady` one pass later; B here vetoes the `REPLICAOF`, T79 the delete; neither closes the other.
- [T62](062-resetsentinelstate-falls-back-to-sentinel-reset.md) - `:3161` resets Sentinel toward the same X; its gate does not ask for keys.
- [T75](075-the-sentinel-failover-reset-and-retrigger-cycle-has-no-cap.md) - caps how often the no-replica cycle calls this function; edits the same comment lines.
- [T69](069-three-sync-checks-read-a-replica-field-from-the-master.md) - the sync half of `verifyNewMasterReady` cannot refuse.
- [T36](036-non-persistent-master-restarts-empty.md) - a non-persistent promoted pod restarting empty is one route to an empty X.
- [T12](012-no-write-fencing-min-replicas-to-write-as-an-opt-in-field.md) - writes O accepts while two masters stand are lost by any demotion of O.
