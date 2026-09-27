---
id: T12
title: No write fencing — a master with zero replicas keeps accepting writes; `min-replicas-to-write` as an opt-in field
state: analysed       # "open - analysed 2026-08-24, not started"
severity: medium
security: none
urgency: next         # was icebox; rule 3 matches before rule 5 (History 2026-09-27)
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
(~~[`statefulset.go:428-436`](../../internal/builder/statefulset.go)~~ *(corrected 2026-09-27: [`statefulset.go:457-488`](../../internal/builder/statefulset.go), the test at `:471`)*), and
~~[`statefulset.go:450-465`](../../internal/builder/statefulset.go)~~ *(corrected 2026-09-27: [`:490-519`](../../internal/builder/statefulset.go))* exists purely to cover
"the promoted pod is the only master and has no replicas attached yet". The rolling
update says the same at
~~[`rolling_update.go:2757-2760`](../../internal/controller/rolling_update.go)~~ *(corrected 2026-09-27: [`rolling_update.go:4036-4040`](../../internal/controller/rolling_update.go))*. Durations:
the manual-failover window is bounded only by `GetSyncTimeout` (**default 5 min**, [`valkey_types.go:1448-1453`](../../api/v1/valkey_types.go)), and
the Sentinel path tolerates a zero-replica master through `handleMasterWithNoReplicas`
(~~[`rolling_update.go:1940-1985`](../../internal/controller/rolling_update.go)~~ *(corrected 2026-09-27: [`rolling_update.go:3142`](../../internal/controller/rolling_update.go))*) for
`replicaReconnectTimeout` = 90 s × `maxReconnectResets` = 2 *(corrected 2026-09-27: [`:149`, `:138`](../../internal/controller/rolling_update.go))*, i.e. **~3-5 min**, with
`SentinelParallelSyncs = 1` ([`sentinel.go:53`](../../internal/builder/sentinel.go)) lengthening it. Under the gate, every client write in those
windows is refused.

**2. Standalone would be a permanent total write outage.** `replicationConfig` is the
only topology-conditional block
([`configmap.go:109-111`](../../internal/builder/configmap.go)); everything else is
emitted for every mode. A `replicas: 1` cluster has zero replicas forever. And the
operator would not notice: `verifyValkeyConnectivity` is PING-only
(~~[`valkey_controller.go:2698-2707`](../../internal/controller/valkey_controller.go)~~ *(corrected 2026-09-27: [`valkey_controller.go:2969-2980`](../../internal/controller/valkey_controller.go))*),
both probes are PING
(~~[`statefulset.go:750-772`](../../internal/builder/statefulset.go)~~ *(corrected 2026-09-27: ~~`statefulset.go:845-869`~~ *(review 2026-09-27:* [`statefulset.go:847-870`](../../internal/builder/statefulset.go)*, readiness `:847-858`, liveness `:859-870`)*)*), and `CheckCluster`
reasons from `ConnectedSlaves` / `MasterSyncInProgress`
([`health/checker.go:90-141`](../../internal/health/checker.go)). Phase would read `OK`
on a cluster that accepts nothing.

**3. `replicas: 2` in steady state with one pod down is the same silent outage.** No
rolling update needed. `readyReplicas=1`, PING succeeds, the `instanceRole=master` label
is untouched, so the `-rw` Service keeps routing to a master that refuses every write —
and both Kubernetes and the CR call it healthy.

**4. The operator writes to Valkey in exactly one place, and it would go red.**
`writeHealthKey` (~~[`internal/observer/checks.go:108-115`](../../internal/observer/checks.go)~~ *(corrected 2026-09-27: [`internal/observer/checks.go:109-116`](../../internal/observer/checks.go))*)
issues `SELECT <db>` + `SET vko:health … EX 10` against the master. `ExecMulti`
propagates the `-NOREPLICAS` reply as an error
([`valkeyclient/client.go:479-481`](../../internal/valkeyclient/client.go)), `write_test`
fails, `read_test` is force-failed
(~~[`observer/observer.go:290-296`](../../internal/observer/observer.go)~~ *(corrected 2026-09-27: [`observer/observer.go:292-300`](../../internal/observer/observer.go))*),
`replica_read_test` is skipped, `writeTestFailure` defaults to true
(~~[`valkey_types.go:1075-1078`](../../api/v1/valkey_types.go)~~ *(corrected 2026-09-27: the field at [`valkey_types.go:906-909`](../../api/v1/valkey_types.go), the default in `UnreadyWhenDefault` at [`:1456-1461`](../../api/v1/valkey_types.go))*) → `/readyz` 503 → the
observer pod flips NotReady (probe `PeriodSeconds: 2, FailureThreshold: 1`,
[`builder/observer.go:95-105`](../../internal/builder/observer.go)) → `status.observerReady=false`.
The observer is itself opt-in and off by default
(~~[`IsObserverEnabled`](../../api/v1/valkey_types.go#L1007)~~ *(corrected 2026-09-27: [`IsObserverEnabled`](../../api/v1/valkey_types.go#L1361))*), but the two features attract
the same user, so the combination has to be handled, not hoped away.

**5. `min-replicas-max-lag` is invisible to this operator.**
`min_slaves_good_slaves` **is** in `INFO replication` *(corrected 2026-09-27: only while `min-replicas-to-write` is set — absent without the directive on 9.1.1 and 8.1.9, measured in docker)*, but `parseReplicationInfo`
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
(~~[`sentinel_failover_test.go:574-597`](../../internal/controller/sentinel_failover_test.go)~~ *(corrected 2026-09-27: [`sentinel_failover_test.go:574-599`](../../internal/controller/sentinel_failover_test.go), the mock at `:579-581`)*)
mocks a `NOREPLICAS` reply to `WAIT`. Measured: **`WAIT` is not gated** — it returns `0`
with no error. And `waitForWriteSync` returns nil early when `numReplicas == 0`
(~~[`rolling_update.go:1774-1777`](../../internal/controller/rolling_update.go)~~ *(corrected 2026-09-27: [`rolling_update.go:2930-2933`](../../internal/controller/rolling_update.go))*), so it is
a no-op in exactly the window the gate bites. The test is still a valid test of the
operator's own refusal path; it is just not evidence about this feature.

## E2E impact

`valkeyExec` (~~[`test/e2e/e2e_test.go:230-265`](../../test/e2e/e2e_test.go)~~ *(corrected 2026-09-27: [`test/e2e/e2e_test.go:225-268`](../../test/e2e/e2e_test.go))*) retries only
on a non-zero exit. Measured: `valkey-cli --raw SET` under the gate prints the error to
**stdout and exits 0**, so `valkeyMSET`'s `require.Equal(t, "OK", resp)`
(~~[`e2e_test.go:268-281`](../../test/e2e/e2e_test.go)~~ *(corrected 2026-09-27: [`e2e_test.go:270-282`](../../test/e2e/e2e_test.go))*) **aborts** the test with a string
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
- **ADR 0015**: schema validation only — no webhook, ~~no CEL anywhere in the repo
  (verified: `x-kubernetes-validations` appears in zero generated CRDs). A cross-field
  rule like "only with `replicas >= 3`" **cannot** be enforced at admission. It has to be
  a runtime refusal with a condition and an Event, in the shape ADR 0023 and ADR 0002
  already use for a spec the operator accepts and declines to apply.~~ *(corrected 2026-09-27: false since
  [ADR 0033](../adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md),
  which amended ADR 0015 on 2026-09-26: `SeccompProfileSpec` carries two `XValidation` rules
  ([`valkey_types.go:539-540`](../../api/v1/valkey_types.go)) and the generated CRD carries
  `x-kubernetes-validations`. "`replicas >= 3` when fencing is on" can be a CEL rule on
  `ValkeySpec` ([`:1024`](../../api/v1/valkey_types.go)), refused at admission; a runtime
  refusal with a condition stays only as the defence for an operator running against an older
  CRD.)*
- **CRD shape**: ~~7 of 10~~ *(corrected 2026-09-27: 7 of 11)* optional blocks are `+optional` pointer-to-struct with
  `Enabled bool` + `+kubebuilder:default=false`, read through
  `v.Spec.X != nil && v.Spec.X.Enabled` (~~[`valkey_types.go:675-740`](../../api/v1/valkey_types.go)~~ *(corrected 2026-09-27: `ValkeySpec` at [`valkey_types.go:1024`](../../api/v1/valkey_types.go); `auth`, `rollingUpdate`, `antiAffinity` and `podSecurity` carry no `Enabled`)*).
  `antiAffinity` is the exception, an enum defaulted to `off` (ADR 0005 D2/D3).
- **Placement**: the shared tail of `replicationConfig`
  ([`configmap.go:175-181`](../../internal/builder/configmap.go)) is the only placement
  that reaches replicas, because on both HA paths the init container copies the **master**
  ConfigMap onto a replica and merely appends `replicaof`
  (~~[`statefulset.go:289-300`](../../internal/builder/statefulset.go) and
  [`:482-487`](../../internal/builder/statefulset.go)~~ *(corrected 2026-09-27: [`statefulset.go:335-339`](../../internal/builder/statefulset.go) and [`:523-527`](../../internal/builder/statefulset.go))*). It is also already gated on
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

1. **Refuse below `replicas: 3`** at runtime *(corrected 2026-09-27: at admission, by a CEL rule — see the ADR 0015 constraint above — with the runtime refusal as the fallback)* — a condition
   (`WriteFencingNotApplied`) plus a Warning Event, never a silent no-op, and never a
   rendered directive. On `replicas: 2` every replica replacement is a write outage, and
   the manual failover is a 5-minute one.
2. **Parse `min_slaves_good_slaves`** into `ReplicationInfo` and surface it, or the
   operator stays structurally blind to the only signal that explains a refusal
   (hazard 5). This is the piece with the widest blast radius ~~and it is useful on its own~~
   *(corrected 2026-09-27: it is not useful on its own: the field is absent from `INFO replication` unless the
   directive is set, measured in docker on 9.1.1 and 8.1.9)*.
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

## Verified / not verified *(added 2026-09-27)*

**Verified 2026-09-27.** *Read at `4a7543e`:* every location above, corrected in place where it
moved; `min-replicas`, `min_slaves` and `MinReplicas` appear nowhere in `internal/`, `api/`,
`cmd/` or `deploy/`; the gate at [`configmap.go:109-111`](../../internal/builder/configmap.go)
still excludes standalone; `WriteTestFailure` still defaults to true. *Measured in docker on
`valkey/valkey:9.1.1` and `8.1.9` with `--min-replicas-to-write 1` and no replica:* `SET` answers
`NOREPLICAS`, `WAIT 1 100` answers `0`, `valkey-cli --raw SET` prints the error on stdout and
exits 0 (9.1.1); `min_slaves_good_slaves` is in `INFO replication` only while the directive is
set. The 2026-08-24 measurement is thereby re-run on both pins for these four facts.

**Not verified 2026-09-27:** the other rows of the 2026-08-24 table (`REPLICAOF`, `PUBLISH`,
`CONFIG SET`, `CLIENT KILL`, `DBSIZE`, `GET`, `INFO`), the count of 39 e2e write sites, and
anything on Kubernetes.

## Options *(added 2026-09-27)*

One decision: build the feature, or refuse it. *(Review 2026-09-27: build it as option 1 or
option 4, or refuse it.)* Nothing else in this ticket waits on another
decision.

1. **Build `spec.writeFencing` with the five prerequisites.** L. A CRD field defaulting off
   (ADR 0005 D1); `replicas >= 3` as a CEL rule on `ValkeySpec`, cheaper since ADR 0033 than
   the runtime-only refusal this ticket assumed; the observer interlock; the `valkeyExec`
   hardening; the CRD doc of the enablement outage; and the e2e of a full roll with every write
   acknowledged. `min_slaves_good_slaves` shrinks to a diagnostic for fenced clusters, because
   it exists only there (measured). Rolls nothing on upgrade; enabling it rolls that cluster
   (hazard 6). Leaves: every zero-replica window the operator opens on purpose becomes a
   client-write outage for whoever opts in — the manual failover, bounded by `syncTimeout`
   (default 5 min, [`valkey_types.go:1448-1453`](../../api/v1/valkey_types.go)); the Sentinel
   no-replica tolerance (90 s × 2, [`rolling_update.go:138`, `:149`](../../internal/controller/rolling_update.go));
   and every full sync of a roll. What it protects is the two-master windows that ADR 0011,
   0025 and 0028 already bound and report.
2. **Refuse it in an ADR** **(recommended)**. S. A new ADR, "the operator does not fence writes",
   carrying the measurements and this prerequisite list as its Alternatives, so a later
   re-opening starts from the design. Its re-open trigger is a user who asks for fencing,
   runs `replicas >= 3` and accepts the roll outages. Then:
   - [ADR 0025](../adr/0025-a-split-brain-warning-means-one-that-did-not-resolve-itself.md) `:442-444`
     and [ADR 0028](../adr/0028-a-demotion-may-not-discard-the-only-dataset.md) `:123`, `:230`
     cite that ADR instead of this ticket (ADR 0034 D7);
   - so does the comment at [`pod_termination_test.go:257`](../../internal/controller/pod_termination_test.go);
   - the ticket is archived — *(review 2026-09-27)* after the independent `valkeyExec` item of
     the Work list has landed or moved to a ticket of its own, since an archived ticket holds
     no open work.

   Rolls nothing. The divergence cost stays where ADR 0025 and 0028 already accept it, now
   stated as a decision instead of an open item.
3. **Leave it in icebox.** Zero cost now. The outcome is the same as 2 but unrecorded. Two ADRs
   keep naming an open ticket as the price of their accepted risk. ADR 0025 keeps a link into
   `docs/tickets/` that breaks when this file is archived.
4. *(added in review 2026-09-27)* **Fence only the split, at runtime.** M. At the edge where
   `MultipleMasters` outlives `splitBrainWarnAfter` = 90 s and `SplitBrainDetected` fires
   ([`split_brain_report.go:35`, `:77-78`](../../internal/controller/split_brain_report.go)),
   the operator sends `CONFIG SET min-replicas-to-write 1` to every pod answering master, and
   `CONFIG SET min-replicas-to-write 0` where the level clears. It does not need to know which
   side is right: the side without a replica stops taking writes, which is the side a refused
   ADR 0028 demotion leaves diverging in the common shape. `CONFIG SET` is not gated
   (measured 2026-08-24, not re-measured), nothing rewrites the config file, so the setting dies
   with the process. No CRD field, no config hash, no roll, and no cost on controlled failovers,
   which resolve inside the 90 s (the bound sits above the 75 s grace period, ADR 0025). Leaves:
   the first 90 s of every split unfenced; a new write onto Valkey in the split-brain path
   that ADR 0025 and ADR 0028 govern, both of which it amends; a fence stranded on a master if the operator stops before the clear, harmless while that
   master has a replica and a write outage when it has none; both sides fenced when neither
   holds a replica. The premise that the diverging side is the replica-less one is **not
   measured**. Not recommended now: it is a new write in the master-authority path that the six
   rules in CLAUDE.md guard, it needs its own ADR, and nobody has asked for it. It is
   the shape the refusal ADR of option 2 names as the one a re-opening starts from, ahead of
   option 1.

Not proposed: a generic `spec.extraConfig` escape hatch. It would let a user set the directive,
but also override every directive the operator manages (`replicaof`, `save`, TLS). That is a
much larger product call than this one.

Why 2: this ticket's own analysis shows that a naive enablement is a net regression, and that
the feature is the prerequisite list, not the directive. The gain goes only to users who opt in
(ADR 0005 D1), and only inside windows the operator already bounds and reports. The cost lands
on every roll of those clusters. ADR 0034 says a decision lives in an ADR and a ticket is a work
list. A refusal ADR makes the accepted price durable and removes the ADRs' citations of this
ticket. Option 3 reaches the same outcome without recording it. *(Review 2026-09-27:)* option 4
removes the roll cost that sinks option 1, so the refusal is no longer "the price is every roll";
it rests on option 4's unmeasured premise and on it adding a write to the master-authority path.
The ADR must say so, or a reader takes option 1's cost as the reason for all of them.

## Decision

**Current: not decided** *(2026-09-27)* — a product call between the Options above; the dated
entries below are the analysis notes of the two earlier looks, not decisions.

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
  [ADR 0028](../adr/0028-a-demotion-may-not-discard-the-only-dataset.md) ~~`:189-191`~~ *(corrected 2026-09-27: `:123`, `:230`)* and
  [ADR 0025](../adr/0025-a-split-brain-warning-means-one-that-did-not-resolve-itself.md)
  ~~`:243-248`~~ *(corrected 2026-09-27: `:439-444`)* — which strengthens the case for deciding it, and changes nothing about it being
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

  *(Re-corrected 2026-09-27 at `4a7543e`: `handleMasterWithNoReplicas`
  [`:3142`](../../internal/controller/rolling_update.go); the early return
  [`:2930-2933`](../../internal/controller/rolling_update.go); `writeTestFailure`
  [`valkey_types.go:906-909`](../../api/v1/valkey_types.go) with its default in
  `UnreadyWhenDefault` [`:1456-1461`](../../api/v1/valkey_types.go); `IsObserverEnabled`
  [`:1361`](../../api/v1/valkey_types.go); `writeHealthKey`
  [`observer/checks.go:110`](../../internal/observer/checks.go). The 39 write sites were not
  re-counted.)*

## Work list *(added 2026-09-27)*

- **XS, needs no decision, can land today:** the fixture of
  `TestHandleMasterFailover_DoesNotFailOverWhenWriteSyncFails`
  ([`sentinel_failover_test.go:579-581`](../../internal/controller/sentinel_failover_test.go))
  answers `WAIT` with `NOREPLICAS`, which `WAIT` never returns (measured). Give it a neutral
  error text and one comment line saying any error reply blocks the promotion. The test keeps
  its assertion.
- **Independent of the decision, S, needs a full e2e run:** `valkeyExec` treats a `-`-prefixed
  stdout reply as an error (prerequisite 4). A `READONLY`, `LOADING` or `OOM` reply fails the
  same misleading way without fencing.
- **Waits on the decision:** option 2 (the ADR, the three ADR citations, the test comment,
  the archive) or option 1 (everything under "Proposed shape").

## History

- 2026-09-27: adversarial review of the enrichment — about half of the corrected locations
  re-read at `4a7543e`, all held but the probe range (now `:847-870`); `7 of 11` recounted
  (eleven pointer blocks in `ValkeySpec`, seven with `Enabled`). Added option 4, a runtime fence
  at the `SplitBrainDetected` edge, which the options had missed; option 2 stays recommended,
  its justification narrowed accordingly, and its archive step now waits for the independent
  `valkeyExec` item. Urgency `next` kept: the derivation is the table's, and the flag for Hans
  below stands. No frontmatter change.
- 2026-09-27: enriched - locations re-read at `4a7543e` and corrected in place; two claims
  corrected: CEL exists since ADR 0033, so prerequisite 1 can be an admission rule, and
  `min_slaves_good_slaves` is not useful alone, because it is absent without the directive
  (measured in docker on both pins); Options added (build, refuse in an ADR, leave), with the
  refusal ADR recommended; a work list separating one XS fixture fix. **Urgency icebox → next:**
  rule 3 matches before rule 5. Severity is medium, and the trigger is live in released code:
  every two-master window, which ADR 0028 D3 deliberately lengthens, lets both sides take writes
  that the repair then discards. What is next is the product call, not the build.
  `blocked-by: product` stays. The earlier triage kept `icebox`, reasoning that the feature is
  opt-in and the risk accepted; the table has no such exception, so Hans may overrule it
  explicitly.
- 2026-09-27 - extracted verbatim from the collection ticket (now [archive/039-findings-from-the-1-11-0-fleet-rollout.md](archive/039-findings-from-the-1-11-0-fleet-rollout.md)) into its own file when the tickets were numbered. Frontmatter filled from the final board row (board archive of that file, groomed 2026-09-26) and from the section text.
