---
id: T12
title: No write fencing — a master with zero replicas keeps accepting writes; `min-replicas-to-write` as an opt-in field
state: analysed       # no option chosen
severity: medium      # data durability in two-master windows; opt-in feature request
security: none
urgency: next         # rule 3: severity medium, trigger live in released code (ADR 0028 D3 refused demotions, steady-state splits)
effort: S             # the recommended path (refusal ADR plus the XS fixture); option 1 would be L
blocked-by: product
filed-from: T4 analysis, 2026-08-24
opened: 2026-08-24
decided:
done:
---

# T12 - No write fencing — a master with zero replicas keeps accepting writes; `min-replicas-to-write` as an opt-in field

## Current state

No master built by this operator is fenced. `generateValkeyConf`
([`configmap.go:62-138`](../../internal/builder/configmap.go)) writes no `min-replicas` directive,
and the CRD has no field and no config escape hatch for one, so every master accepts writes with
zero replicas (Valkey default `min-replicas-to-write 0`). In a split both masters take writes, and
the repair (`REPLICAOF` on the loser) discards the loser's.
[ADR 0028](../adr/0028-a-demotion-may-not-discard-the-only-dataset.md) D3 makes that window
longer whenever it refuses a demotion, and names the missing fencing as the price.
[ADR 0025](../adr/0025-a-split-brain-warning-means-one-that-did-not-resolve-itself.md) names it too.

**What the directive does** (measured in docker, `valkey/valkey:9.1.1` and `8.1.9`, zero replicas):

| Commands | Under `min-replicas-to-write 1` |
|---|---|
| `SET`, `MSET`, `DEL`, `EXPIRE`, `INCR`, `FLUSHALL` | `NOREPLICAS Not enough good replicas to write.` |
| `WAIT` (answers `0`), `REPLICAOF`, `PUBLISH`, `CONFIG SET`, `CLIENT KILL` | not gated |
| `DBSIZE`, `PING`, `GET`, `TTL`, `SELECT`, `INFO` | unaffected |

- A replica counts as good while `ONLINE` and ACKed within `min-replicas-max-lag` seconds. A side
  whose replica link drops takes nothing more; a stalled link without a disconnect lets up to
  `max-lag` seconds plus one cron tick of writes through (read in upstream source).
  `min-replicas-max-lag 0` switches the gate off silently.
- `PUBLISH` passing keeps Sentinel hello messages and discovery working on a fenced master.
- The fence does nothing for the side of a split that keeps a replica, and does not stop a replica
  from full-syncing an empty restarted master (`REPLICAOF` and `SYNC` are not gated; T36).
- Where it would go: the shared tail of `replicationConfig`
  ([`configmap.go:174-181`](../../internal/builder/configmap.go)), which is in both config bodies
  and gated on `IsSentinelEnabled() || IsMultiReplicaWithoutSentinel()`
  ([`:108-111`](../../internal/builder/configmap.go)). Both bodies feed `ComputeConfigHash`
  ([`:293-305`](../../internal/builder/configmap.go)), so enabling it rolls the cluster once.
  `generateSentinelConf` ([`sentinel.go:99-194`](../../internal/builder/sentinel.go)) is untouched.

**What an opted-in cluster would pay** (all measured in docker on both pins unless noted):

1. **Controlled failovers.** On `replicas >= 3`, `promoteAndRedirect`
   ([`rolling_update.go:4188-4248`](../../internal/controller/rolling_update.go)) redirects the
   other replicas before the delete: idle, the first fenced `SET` is accepted at +0.23-0.25 s;
   under client write load the redirected pods full-resync behind `repl-diskless-sync-delay 5`
   ([`configmap.go:179`](../../internal/builder/configmap.go)) and writes are refused for
   5.1-5.7 s plus the dataset transfer. A forced Sentinel failover on a settled cluster refused
   writes for 5.7-6.9 s (nine runs). `SENTINEL FAILOVER <name> COORDINATED` on Valkey 9 (fenced on
   every node) took writes at +0.62 s and lost 0 of 2047 acknowledged writes; Valkey 8 has no
   `COORDINATED`. On `replicas: 2` the promoted pod has zero replicas from the old master's delete
   until the replacement joins.
2. **`sentinel.enabled: true` with `replicas: 1`** is accepted by the API (`IsSentinelEnabled`
   ignores `replicas`, [`valkey_types.go:1159-1161`](../../api/v1/valkey_types.go)), receives
   `replicationConfig` and would be fenced permanently. The operator would not notice:
   `verifyValkeyConnectivity`
   ([`valkey_controller.go:2970-2981`](../../internal/controller/valkey_controller.go)) and both
   probes ([`statefulset.go:847-870`](../../internal/builder/statefulset.go)) are PING-only, so
   phase reads `OK` on a cluster that accepts nothing. `replicas: 1` without Sentinel is excluded
   by the gate.
3. **Degraded states.** `replicas: 2` with one pod down, two of three pods down, or one stalled
   replica: the `-rw` Service keeps routing to a master that refuses every write while Kubernetes
   and the CR report healthy.
4. **Invisible cause.** `parseReplicationInfo`
   ([`client.go:566-596`](../../internal/valkeyclient/client.go)) reads neither the per-replica
   `slaveN:...,state=...,lag=...` fields (present with or without the directive) nor
   `min_slaves_good_slaves` (present only while fenced). Every operator gate
   (`waitForReplicasReady`, `verifyReplacedReplicasSynced`, `replicationNotEstablishedReason`,
   `CheckCluster`, the sidecar `isSyncedReplica`) stays green on a lagging replica. Replacing one
   replica at `replicas >= 3` does not make the survivor lag: 0 of 11 `SET`s refused during a
   522 MB full sync of a sibling.
5. **Observer.** `writeHealthKey`
   ([`observer/checks.go:109-116`](../../internal/observer/checks.go)) is the operator's only data
   write; under the gate it fails and `/readyz` goes 503. No interlock is needed: `replica_sync`
   ([`observer/checks.go:92-107`](../../internal/observer/checks.go)) already fails in every
   zero-replica window, so `write_test` fails only on a real refusal.
6. **Handler tolerance.** `handleMasterWithNoReplicas`
   ([`rolling_update.go:3145`](../../internal/controller/rolling_update.go)) tolerates a
   zero-replica master for about 270 s (90 s `replicaReconnectTimeout`, re-armed twice by
   `maxReconnectResets`) before it proceeds; under the gate every client write in that time is
   refused.

**A test fixture answers with a reply `WAIT` never gives.**
`TestHandleMasterFailover_DoesNotFailOverWhenWriteSyncFails`
([`sentinel_failover_test.go:579-581`](../../internal/controller/sentinel_failover_test.go))
mocks `NOREPLICAS` for `WAIT`, which is not gated. An error `WAIT` does return (measured on a
replica, both pins): `ERR WAIT cannot be used with replica instances. Please also note that if a
replica is configured to be writable (which is not the default) writes to replicas are just local
and are not propagated.` The test remains a valid test of the refusal path.

**E2E.** `valkey-cli --raw SET` under the gate prints the error on stdout and exits 0, so the
helpers in [`e2e_test.go`](../../test/e2e/e2e_test.go) do not see it; that reply check is T68.
[`sentinel_stale_master_test.go:204-206`](../../test/e2e/sentinel_stale_master_test.go) asserts a
write to the Sentinel master is not `READONLY` and would fail with `NOREPLICAS`, a misleading
diagnosis.

## Required changes

### Independent of the open questions

- Fixture of `TestHandleMasterFailover_DoesNotFailOverWhenWriteSyncFails`: replace the
  `NOREPLICAS` reply with the `ERR WAIT cannot be used with replica instances. ...` text above,
  plus one comment line that any error reply blocks the promotion; the assertion stays.
  `make test-unit` green.

### Depends on the answers

**If Q1 = refuse (option 2):**

- New ADR "the operator does not offer `min-replicas-to-write` as a CRD field", with a line in the
  ADR index. It carries this ticket's measurements and option 1's prerequisite list as its
  Alternatives, states that fencing would not protect against T36's replica flush, and records the
  re-open trigger: a user asks for it, runs `replicas >= 3`, and runs Valkey 9 with the
  coordinated failover of T67 option A.
- If T67 is decided as its runtime fence (option B), this refusal goes into the same ADR as that
  fence.
- Rewrite the citations of this ticket to the new ADR: ADR 0025 `:443-444` (label and path link),
  ADR 0028 `:123` and `:230`,
  [`pod_termination_test.go:257`](../../internal/controller/pod_termination_test.go).
  ADR 0026 `:206-208` ("no write fencing on either side") may reference it.
- Done when `git grep -n 'T12\|012-no-write' -- ':!docs/tickets'` returns nothing and the fixture
  item has landed; then archive the ticket.

**If Q1 = build (option 1):** `spec.writeFencing` (name open), optional pointer block, default off:

```yaml
spec:
  writeFencing:
    enabled: false      # default; absent block renders no directive
    minReplicas: 1      # example; min-replicas-to-write
    maxLagSeconds: 10   # example; min-replicas-max-lag, must be >= 1
```

- Render both directives in the shared tail of `replicationConfig` only when enabled (ADR 0005 D1).
- Refuse `minReplicas > replicas - 2` and `maxLagSeconds < 1`, never rendering the directive then;
  where the refusal happens is Q2. The first rule also covers `sentinel.enabled` with `replicas: 1`.
- Parse the `slaveN` `state` and `lag` fields and `min_slaves_good_slaves` into `ReplicationInfo`
  and surface them.
- Document at the CRD field, in the shape of ADR 0005 D8: every failover whose redirected replicas
  full-resync refuses writes for at least 5 s plus the transfer, and fewer than `minReplicas` good
  replicas is a write outage.
- A divergence-free promotion for the paths T67 option A does not cover (server-side
  `FAILOVER TO <host> <port>` exists on both pins), or the refusal accepted in writing.
- Gate the e2e writes on `waitForConnectedReplicas` where they are not already; requires T68 to
  have landed.
- Unit tests: absent block and `enabled: false` render byte-identical config; enabled renders both
  directives in both bodies; `minReplicas > replicas - 2`, `maxLagSeconds: 0` and
  `sentinel.enabled` with `replicas: 1` render nothing and are refused; the parser reads the
  `slaveN` lag fields and `min_slaves_good_slaves`.
- E2E, both Valkey legs: a 3-replica cluster with the feature on runs a writer through a full
  rolling update and asserts that no acknowledged write is lost, counting refused writes.

## Open questions

### Q1: Does the operator offer `min-replicas-to-write` as an opt-in CRD field, or refuse it in an ADR?

Fencing stops the replica-less side of a split from taking writes, but on an opted-in cluster it
refuses writes at every controlled failover whose replicas full-resync and turns degraded states
into write outages the operator cannot yet explain. T67 (the roll's own Sentinel failover losing
acknowledged writes) is decided first, because its choice sets the failover cost here.

- **Option 1 - build `spec.writeFencing`** (L): protects ADR 0028's refused-demotion windows and
  steady-state splits on clusters that opt in; costs at least 5 s of refused writes per controlled
  failover on Valkey 8 Sentinel clusters and on the non-Sentinel path under load, plus unexplained
  degraded-state outages until the lag fields are parsed.
- **Option 2 - refuse the field in an ADR (recommended)** (S): nothing changes at runtime; the
  divergence in ADR 0028's windows stays accepted as a recorded decision with a re-open trigger.
  Nobody has asked for the field, option 1 is L with a recurring measured cost, and what it adds
  beyond T67 option A (refused-demotion windows, bounded by ADR 0010 and reported as
  `MultipleMasters`) is narrow. If T67 goes to its option C, option 1 becomes the only protection
  of the ADR 0025 D9 window for opted-in clusters and this comparison has to be redone.

A generic `spec.extraConfig` escape hatch is not an option here: it would also let users override
`replicaof`, `save` and TLS, a much larger product call.

**Answer:** _open_

### Q2: Only if Q1 = build: is the `minReplicas <= replicas - 2` / `maxLagSeconds >= 1` rule enforced at admission or at runtime?

CEL is available (`SeccompProfileSpec` already carries `XValidation` rules,
[`valkey_types.go:541-542`](../../api/v1/valkey_types.go)); the CRD ships with the chart, so no
runtime fallback is needed against CRD skew.

- **Admission (CEL on `ValkeySpec`)**: invalid specs never reach the operator, but every later
  scale-down below `minReplicas + 2` (for example a GitOps change of `replicas` from 3 to 2) is
  refused too, against the precedent of `podDisruptionBudget.maxUnavailable`, which is honoured
  "rather than rejecting a later scale-down"
  ([`valkey_types.go:775-779`](../../api/v1/valkey_types.go)).
- **Runtime**: the spec is accepted, the directive is not rendered, and a `WriteFencingNotApplied`
  condition plus a Warning Event report it (the ADR 0023 / ADR 0002 shape); never a silent no-op.

The ticket records no recommendation.

**Answer:** _open_

## Not verified

- Anything on Kubernetes: a Kind e2e with a writer on the `-rw` Service through a Sentinel roll
  would settle the fenced failover windows there.
- The survivor-lag measurement (item 4) ran once, on 9.1.1 only; a rerun on 8.1.9 would settle it.
- Whether, in ADR 0028's refused-demotion windows, the diverging side is the replica-less one (the
  side a fence would stop).
- `FAILOVER TO` on the non-Sentinel `promoteAndRedirect` path; the open points of the coordinated
  Sentinel failover are carried by T67.

## Related

- [T67](067-the-rolls-own-sentinel-failover-loses-acknowledged-writes.md): decided before Q1; its
  option A sets the failover cost, its option B means one ADR for both, its option C reopens Q1.
- [T68](068-the-e2e-exec-helpers-do-not-check-the-valkey-reply.md): the e2e reply check; option 1
  depends on it.
- T36: fencing does not protect against its replica flush; the refusal ADR must not claim otherwise.
- [T69](069-three-sync-checks-read-a-replica-field-from-the-master.md): `CheckCluster` reads the
  replica-only `master_sync_in_progress` from a master.
- T40: counts this ticket's four citations outside `docs/tickets/`; option 2 retires them.
- T35: its flag-aware `-rw` fence (L5) was left to this ticket; no option here takes it up.
- Origin: [archive/039](archive/039-findings-from-the-1-11-0-fleet-rollout.md) (T4/T11 analysis).
