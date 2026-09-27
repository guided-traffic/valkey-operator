---
id: T12
title: No write fencing — a master with zero replicas keeps accepting writes; `min-replicas-to-write` as an opt-in field
state: analysed       # "open - analysed 2026-08-24, not started"
severity: medium
security: none
urgency: icebox
effort: L
blocked-by: product
filed-from: T4 analysis, 2026-08-24
opened: 2026-08-24
decided:
done:
---

# T12 - No write fencing — a master with zero replicas keeps accepting writes; `min-replicas-to-write` as an opt-in field

**Severity: medium (data durability, opt-in feature request). Status: open — analysed
2026-08-24, not started. Found while analysing T4. Severity argument strengthened
2026-08-26: [ADR 0028](../adr/0028-a-demotion-may-not-discard-the-only-dataset.md) D3
deliberately *lengthens* the two-master window — a refused demotion keeps both masters
until the state bound expires — and names the missing fencing as the price it pays.**

## What is missing

`grep -rn "min-replicas" internal/ api/` returns nothing. `generateValkeyConf`
([`internal/builder/configmap.go:62-137`](../../internal/builder/configmap.go)) has no
hook for it and there is no `spec.config` / `extraConfig` escape hatch on the CRD, so a
user cannot set it either. **Every master this operator builds accepts writes with zero
connected replicas.**

That is what makes the T4/T11 split-brain window expensive: both masters accumulate
writes, and the repair (`REPLICAOF` on the loser) discards one side's. With
`min-replicas-to-write 1` the diverging side would collect nothing to lose in the first
place.

## Why this is not a one-line opt-in

Measured 2026-08-24 against the repo's own pinned image, `valkey/valkey:9.1.1`
([`test/testimages/images.go:40`](../../test/testimages/images.go)), via
`docker run valkey-server --min-replicas-to-write 1 --min-replicas-max-lag 10`
with zero replicas attached:

| Command | Result under the gate |
|---|---|
| `SET` / `MSET` / `DEL` / `EXPIRE` | `NOREPLICAS Not enough good replicas to write.` |
| `WAIT`, `REPLICAOF`, `PUBLISH`, `CONFIG SET`, `CLIENT KILL` | **not gated** |
| `DBSIZE`, `PING`, `GET`, `INFO` | fine |

So the gate hits client writes and exactly one operator write — and misses every gate
the operator currently relies on. Six consequences, each verified:

**1. A master with zero replicas is a designed, load-bearing state here, not an edge
case.** The init script only accepts a peer as master when `connected_slaves > 0`
([`statefulset.go:428-436`](../../internal/builder/statefulset.go)), and
[`statefulset.go:450-465`](../../internal/builder/statefulset.go) exists purely to cover
"the promoted pod is the only master and has no replicas attached yet". The rolling
update says the same at
[`rolling_update.go:2757-2760`](../../internal/controller/rolling_update.go). Durations:
the manual-failover window is bounded only by `GetSyncTimeout` (**default 5 min**), and
the Sentinel path tolerates a zero-replica master through `handleMasterWithNoReplicas`
([`rolling_update.go:1940-1985`](../../internal/controller/rolling_update.go)) for
`replicaReconnectTimeout` = 90 s × `maxReconnectResets` = 2, i.e. **~3-5 min**, with
`SentinelParallelSyncs = 1` lengthening it. Under the gate, every client write in those
windows is refused.

**2. Standalone would be a permanent total write outage.** `replicationConfig` is the
only topology-conditional block
([`configmap.go:109-111`](../../internal/builder/configmap.go)); everything else is
emitted for every mode. A `replicas: 1` cluster has zero replicas forever. And the
operator would not notice: `verifyValkeyConnectivity` is PING-only
([`valkey_controller.go:2698-2707`](../../internal/controller/valkey_controller.go)),
both probes are PING
([`statefulset.go:750-772`](../../internal/builder/statefulset.go)), and `CheckCluster`
reasons from `ConnectedSlaves` / `MasterSyncInProgress`
([`health/checker.go:90-141`](../../internal/health/checker.go)). Phase would read `OK`
on a cluster that accepts nothing.

**3. `replicas: 2` in steady state with one pod down is the same silent outage.** No
rolling update needed. `readyReplicas=1`, PING succeeds, the `instanceRole=master` label
is untouched, so the `-rw` Service keeps routing to a master that refuses every write —
and both Kubernetes and the CR call it healthy.

**4. The operator writes to Valkey in exactly one place, and it would go red.**
`writeHealthKey` ([`internal/observer/checks.go:108-115`](../../internal/observer/checks.go))
issues `SELECT <db>` + `SET vko:health … EX 10` against the master. `ExecMulti`
propagates the `-NOREPLICAS` reply as an error
([`valkeyclient/client.go:479-481`](../../internal/valkeyclient/client.go)), `write_test`
fails, `read_test` is force-failed
([`observer/observer.go:290-296`](../../internal/observer/observer.go)),
`replica_read_test` is skipped, `writeTestFailure` defaults to true
([`valkey_types.go:1075-1078`](../../api/v1/valkey_types.go)) → `/readyz` 503 → the
observer pod flips NotReady (probe `PeriodSeconds: 2, FailureThreshold: 1`,
[`builder/observer.go:95-105`](../../internal/builder/observer.go)) → `status.observerReady=false`.
The observer is itself opt-in and off by default
([`IsObserverEnabled`](../../api/v1/valkey_types.go#L1007)), but the two features attract
the same user, so the combination has to be handled, not hoped away.

**5. `min-replicas-max-lag` is invisible to this operator.**
`min_slaves_good_slaves` **is** in `INFO replication`, but `parseReplicationInfo`
([`valkeyclient/client.go:566-596`](../../internal/valkeyclient/client.go)) does not parse
it and `ReplicationInfo` has no field for it. A lagging replica reports
`connected_slaves:1`, `master_link_status:up`, `master_sync_in_progress:0` — every gate
in the operator green (`waitForReplicasReady`, `verifyReplacedReplicasSynced`,
`replicationNotEstablishedReason`, `CheckCluster`, the sidecar `isSyncedReplica`) —
while the master refuses writes. On a 3-replica cluster that happens routinely: replacing
one replica forks the master and the survivor lags. **Nothing the operator logs or
exposes would name the cause.**

**6. Enabling it is itself a rolling update that walks through its own worst window.**
The directive would enter both config bodies, both of which feed `ComputeConfigHash`
([`configmap.go:293-305`](../../internal/builder/configmap.go)) — only
`AnnotationKnownMaster` is excluded. So the CR edit triggers a full failover-aware
rolling update, and the failover step lands on a fresh master with zero replicas that
now carries the new setting. The enablement produces the longest outage of the feature's
lifetime.

**Bonus finding: an existing test points at the wrong tripwire.**
`TestHandleMasterFailover_DoesNotFailOverWhenWriteSyncFails`
([`sentinel_failover_test.go:574-597`](../../internal/controller/sentinel_failover_test.go))
mocks a `NOREPLICAS` reply to `WAIT`. Measured: **`WAIT` is not gated** — it returns `0`
with no error. And `waitForWriteSync` returns nil early when `numReplicas == 0`
([`rolling_update.go:1774-1777`](../../internal/controller/rolling_update.go)), so it is
a no-op in exactly the window the gate bites. The test is still a valid test of the
operator's own refusal path; it is just not evidence about this feature.

## E2E impact

`valkeyExec` ([`test/e2e/e2e_test.go:230-265`](../../test/e2e/e2e_test.go)) retries only
on a non-zero exit. Measured: `valkey-cli --raw SET` under the gate prints the error to
**stdout and exits 0**, so `valkeyMSET`'s `require.Equal(t, "OK", resp)`
([`e2e_test.go:268-281`](../../test/e2e/e2e_test.go)) **aborts** the test with a string
comparison. ~15 call sites would be affected, all of them writing right after a
failover or a roll. Two need naming because they would fail *misleadingly*:
[`sentinel_stale_master_test.go:204-206`](../../test/e2e/sentinel_stale_master_test.go)
asserts a write to the Sentinel-reported master is not a READONLY error — it would now
fail with NOREPLICAS, a message pointing at the wrong diagnosis; and
[`standalone_test.go:396`](../../test/e2e/standalone_test.go) would still pass, for the
wrong reason.

Any implementation therefore has to make `valkeyExec` treat a `-`-prefixed stdout reply
as an error, independently of the feature.

## Design constraints this leaves

- **ADR 0005 D1**: new CRD features default to off; an operator upgrade changes nothing.
  So: absent block and `enabled: false` both render no directive.
- **ADR 0015**: schema validation only — no webhook, no CEL anywhere in the repo
  (verified: `x-kubernetes-validations` appears in zero generated CRDs). A cross-field
  rule like "only with `replicas >= 3`" **cannot** be enforced at admission. It has to be
  a runtime refusal with a condition and an Event, in the shape ADR 0023 and ADR 0002
  already use for a spec the operator accepts and declines to apply.
- **CRD shape**: 7 of 10 optional blocks are `+optional` pointer-to-struct with
  `Enabled bool` + `+kubebuilder:default=false`, read through
  `v.Spec.X != nil && v.Spec.X.Enabled` ([`valkey_types.go:675-740`](../../api/v1/valkey_types.go)).
  `antiAffinity` is the exception, an enum defaulted to `off` (ADR 0005 D2/D3).
- **Placement**: the shared tail of `replicationConfig`
  ([`configmap.go:175-181`](../../internal/builder/configmap.go)) is the only placement
  that reaches replicas, because on both HA paths the init container copies the **master**
  ConfigMap onto a replica and merely appends `replicaof`
  ([`statefulset.go:289-300`](../../internal/builder/statefulset.go) and
  [`:482-487`](../../internal/builder/statefulset.go)). It is also already gated on
  `IsSentinelEnabled() || IsMultiReplicaWithoutSentinel()`, which excludes standalone for
  free — hazard 2 disappears by placement alone.
- **Sentinel config is untouched**: `generateSentinelConf`
  ([`sentinel.go:100-195`](../../internal/builder/sentinel.go)) never calls
  `replicationConfig`.

## Proposed shape (not decided)

`spec.writeFencing` (name open), `+optional` pointer, `enabled: false` by default:

```yaml
spec:
  writeFencing:
    enabled: false      # default; absent block renders no directive
    minReplicas: 1      # min-replicas-to-write
    maxLagSeconds: 10   # min-replicas-max-lag
```

Hard prerequisites the implementation owes, in the same change:

1. **Refuse below `replicas: 3`** at runtime — a condition
   (`WriteFencingNotApplied`) plus a Warning Event, never a silent no-op, and never a
   rendered directive. On `replicas: 2` every replica replacement is a write outage, and
   the manual failover is a 5-minute one.
2. **Parse `min_slaves_good_slaves`** into `ReplicationInfo` and surface it, or the
   operator stays structurally blind to the only signal that explains a refusal
   (hazard 5). This is the piece with the widest blast radius and it is useful on its own.
3. **Refuse the combination with `spec.observer.enabled` unless `minReplicas` is
   satisfiable**, or the observer turns every legitimate failover window into
   `observerReady=false`.
4. **Fix `valkeyExec`** to treat a `-`-prefixed stdout reply as an error, and gate the
   e2e writes on `waitForConnectedReplicas` where they are not already.
5. **Document the enablement outage** at the CRD field, in the shape ADR 0005 D8 uses
   for hard anti-affinity: the person who sets it reads the consequence where they set it.

Tests per ADR 0017. Unit: absent block and `enabled: false` render byte-identical config
(the upgrade-neutrality guard); enabled renders both directives in the shared tail of
both config bodies; `replicas: 2` renders nothing and sets the condition;
`parseReplicationInfo` reads `min_slaves_good_slaves`. E2E: a 3-replica cluster with the
feature on survives a full rolling update with every write acknowledged — which is the
test that would actually prove the prerequisite list is complete, and the one most likely
to fail first.

## Decision

- 2026-08-24: raised as its own item at the user's request, after the T4 discussion
  surfaced the missing fencing. Analysis above; **no option chosen and no work started.**
  Recorded explicitly: this is not the one-line opt-in it looks like — the operator's
  own design tolerates multi-minute zero-replica windows on purpose, so the feature is
  the prerequisite list, not the directive.
- 2026-08-26: **still no option chosen.** Re-verified: the Decision section genuinely records
  no choice, so there is nothing to implement yet — **this item is blocked on a product call,
  not on engineering effort**, and it should not be picked up as "the next ticket" until that
  call is made. `grep -rn 'min-replicas' internal/ api/` still returns nothing.

  Two ADRs now name the absence of this feature as the price of an accepted risk —
  [ADR 0028](../adr/0028-a-demotion-may-not-discard-the-only-dataset.md) `:189-191` and
  [ADR 0025](../adr/0025-a-split-brain-warning-means-one-that-did-not-resolve-itself.md)
  `:243-248` — which strengthens the case for deciding it, and changes nothing about it being
  a decision rather than a task.

  **The tradeoff to put in front of whoever decides:** a naive enablement is a net
  regression, not a partial win. The operator deliberately produces zero-replica windows
  during its own rolling update, so `min-replicas-to-write` set without the prerequisite list
  turns a *durability* risk into an *availability* outage on every roll — and the e2e that
  would catch that is the one most likely to fail first.

  Reference drift corrected 2026-08-26 (this item was written before `2051a34`/`75b3c92`):
  `handleMasterWithNoReplicas` `1940-1985` →
  [`rolling_update.go:2505`](../../internal/controller/rolling_update.go#L2505); the
  `waitForWriteSync` early return `1774-1777` →
  [`:2321-2324`](../../internal/controller/rolling_update.go#L2321-L2324); `writeTestFailure`
  `valkey_types.go:1075-1078` → [`:647`](../../api/v1/valkey_types.go#L647) with the default
  at `:1157-1160`; `IsObserverEnabled` `:1007` → [`:1091-1093`](../../api/v1/valkey_types.go#L1091-L1093);
  `observer/checks.go:108-115` → `:113`. Two counts were also wrong: the "~15 e2e write call
  sites" is **39** across 13 files, and both sites named as misleading
  (`sentinel_stale_master_test.go:204`, `standalone_test.go:396`) use `valkeyExecAllowError`,
  not `valkeyExec` — so they are not the hazard this item claimed.

## History

- 2026-09-27 - extracted verbatim from the collection ticket (now [archive/039-findings-from-the-1-11-0-fleet-rollout.md](archive/039-findings-from-the-1-11-0-fleet-rollout.md)) into its own file when the tickets were numbered. Frontmatter filled from the final board row (board archive of that file, groomed 2026-09-26) and from the section text.
