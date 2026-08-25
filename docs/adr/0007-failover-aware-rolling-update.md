# ADR 0007: Failover-Aware Rolling Update Against the Persisted Template

## Status

Accepted. Date: 2026-08-21.

The strategy itself predates this ADR set; the template-source and freshness-guard
decisions below landed on branch `feat/support-pdb`. Guards, per decision:

* D2, D3 — the five tests of
  [`internal/controller/rolling_update_blocked_write_test.go`](../../internal/controller/rolling_update_blocked_write_test.go)
  (`NoUpdateWhenImageWriteBlocked`, `NoUpdateWhenConfigWriteBlocked`,
  `StartsOnceTemplatePersisted`, `CollectPodStates_IgnoresUnpersistedImageChange`,
  `Reconcile_BlockedStatefulSetWriteDoesNotDeletePods`). All five are on the
  template-source rule; none of them reaches D4.
* D4 — `TestHandlePostManualFailover_WaitsWhenOnlyTheConfigHashChanged` and
  `TestHandlePostManualFailover_GuardVerdicts` in
  [`internal/controller/rolling_update_bounds_test.go`](../../internal/controller/rolling_update_bounds_test.go).
* D5 — `TestPromotePod0AndRedirect_DoesNotAdvanceWhenTheRecordFails` and
  `TestPromotePod0AndRedirect_AdvancesWhenTheRecordSucceeds` in
  [`internal/controller/known_master_authority_test.go`](../../internal/controller/known_master_authority_test.go).
* D6 — [`internal/controller/sidecar_pending_condition_test.go`](../../internal/controller/sidecar_pending_condition_test.go)
  and the `isSidecarOnlyChange` cases in
  [`internal/controller/rolling_update_test.go`](../../internal/controller/rolling_update_test.go).
* D8 — `TestDetectAndResolveSplitBrain_PrefersPromotedPodDuringFailover` in
  [`internal/controller/manual_failover_known_master_test.go`](../../internal/controller/manual_failover_known_master_test.go).
* D10 — the ten tests of
  [`internal/controller/failover_sync_gate_test.go`](../../internal/controller/failover_sync_gate_test.go),
  including the positive controls (an established replica passes; a promotion whose two key
  counts agree passes; an empty master still rolls) and the bound. Mutation-checked on
  2026-08-22, each reversal restored and `sha256`-verified: reverting the predicate to the
  sync flag alone and dropping the zero-acknowledgement branch turns four of them red
  (`BlocksAReplicaWhoseLinkIsDown`, `BlocksAReplicaThatAnswersMaster`,
  `PausesTheUpdateOnceTheBoundExpired`,
  `WaitForWriteSync_RefusesToFailOverWithoutASingleAcknowledgement`), each with "Expected
  value not to be nil"; dropping the empty-candidate branch turns
  `VerifyPromotionCandidateHoldsData_RefusesAnEmptyCandidate` red on its own.
* D1 — the multi-replica cases of the rolling-update unit suite, plus
  `TestE2E_RollingUpdate_MultiReplicaNoSentinel` and `TestE2E_RollingUpdate_HA_NoDataLoss`
  ([`test/e2e/rolling_update_test.go`](../../test/e2e/rolling_update_test.go)). Those two
  exist in this tree; whether they pass in CI is not checkable from the repository.
* D7 and D9 — no dedicated test. Verified by reading `isSidecarOnlyChange`,
  `buildPodContainers`, `ProbeCommand` and `HealthServer`; the sticky half of D9 is
  pinned by `TestHealthServer_ReadyzReady`
  ([`internal/sidecar/health_test.go`](../../internal/sidecar/health_test.go)).

Amended 2026-08-22: **D10 is new.** D1 and D9 both say the failover waits on replication
state, and the code asked only `master_sync_in_progress`, which a replica that has not
started syncing answers with 0 -- so a pod holding nothing could pass the last gate before
a promotion whose next step deletes the outgoing master. Found by reading, while
investigating a CI failure whose promoted master served an empty dataset; that failure is
**not** explained by this defect (the WAIT gate did report an acknowledging replica) and
stays open.

## Context

The data StatefulSet uses `updateStrategy: OnDelete` and
`podManagementPolicy: Parallel`, so pod replacement is the operator's job, not the
StatefulSet controller's. That is deliberate: a naive `RollingUpdate` would restart the
master without a controlled failover, taking writes down and risking data loss.

Two properties of the surrounding system shape the rest of this ADR:

* [ADR 0001](0001-continue-reconciling-past-a-rejected-write.md) makes the pass continue
  when a sub-resource write is rejected, so `reconcileWorkload` runs **even when the
  StatefulSet `Update` did not land**. Every rolling-update decision therefore has to be
  safe against "my own write did not take effect".
* A single standalone pod without persistence has no failover target, so restarting it
  loses in-memory data. That is physically unavoidable — which makes it a decision about
  when *not* to restart.

The concrete defect that forced the template-source rule: the old code took "desired"
from two places — image and config hash from the CR, sidecar image and pod-spec hash
from the live StatefulSet. With the StatefulSet write rejected, `podNeedsUpdate` was
true against a template that was never written. The operator deleted the pod, the
statefulset-controller recreated it from the still-old template, the pod came back
outdated, and the cycle repeated every `rollingUpdateRequeueDelay` (10 s) for as long as
the admission gate stayed closed. The loop itself was observed on a cluster during the
admission-webhook incident and is not reproducible from this repository; what the tree
holds is the fix and its guard — `TestReconcile_BlockedStatefulSetWriteDoesNotDeletePods`
drives three passes against a rejected StatefulSet `Update` and asserts the pod survives
every one of them.

## Decision

**D1 — The rolling-update sequence is fixed.** Replace replica pods one by one; verify
each new pod joins the cluster and is seen by the other instances; wait for replication
sync to complete; after **all** replicas are migrated, initiate a controlled leader
failover; verify the failover succeeded; then replace the last pod, the former master.
`dispatchMultiReplicaState` reaches the failover step only once `replaceNextReplica`
finds no replica left needing an update, so the count is `spec.replicas - 1`: two on a
three-replica cluster, four on a five-replica one (`spec.replicas` carries `Minimum=1`
and no maximum). Failing over only once every replica already runs the new spec
guarantees the promotion target is up to date and synced, so the failover cannot promote
a pod that would then have to full-resync. Replacing the master last means the pod
holding the authoritative dataset is disturbed exactly once, at the end, when a synced
successor already exists.

**D2 — Every "desired" input comes from the live StatefulSet, never from the CR.** Four
are named values: `valkeyImageFromSts(sts)`, `sidecarImageFromSts(sts)`,
`configHashFromSts(sts)` (the `vko.gtrfc.com/config-hash` annotation on the persisted pod
template) and `podSpecHashFromSts(sts)`. The fifth is the persisted template's own
container list, `currentSts.Spec.Template.Spec.Containers`, which every `podNeedsUpdate`
call site passes through to `podSpecHashChanged` as the fallback for a pod carrying no
`vko.gtrfc.com/pod-spec-hash` annotation — same source, and precisely the input the first
residual risk below leans on. `v.Spec.Image` and `builder.ComputeConfigHash(v)` are read
at no rolling-update decision site — not in `checkAndHandleRollingUpdate`,
`collectPodStates`, `handleStandaloneRollingUpdate` or `handlePostManualFailover`. In one
line:

> **A rolling update compares pods against the template the statefulset-controller will
> actually recreate them from.**

Nothing else can be a correct comparison target, because nothing else is what a
recreated pod gets.

**D3 — An empty desired value means "cannot tell" and degrades toward not replacing
pods.** `valkeyImageFromSts` and `configHashFromSts` return the empty string when the
container or annotation is absent, and every comparison treats that as "skip the check".
An absent container or missing annotation is missing information, not evidence of drift,
and a malformed or partially-written template must never be able to trigger a deletion.

**D4 — The pre-`REPLICAOF` freshness guard compares the full pod template.** The new-pod
guard in `handlePostManualFailover` calls `podNeedsUpdate` against the live StatefulSet
with the same five sts-derived inputs used by `checkAndHandleRollingUpdate` and
`collectPodStates`, rather than comparing the Valkey image alone. The old image-only
guard was skipped entirely when the image was empty, so a config-hash-only rolling
update passed it trivially and `REPLICAOF` was sent to the old, about-to-die master —
with only the `DeletionTimestamp` check left, which misses a stale cache read. The
replaced loop is in the diff of commit `30588bd`, so that claim is checkable here.

**D5 — Rolling-update state advances only after the action it describes succeeded.**
`promotePod0AndRedirect` returns `RollingUpdateResult{NeedsRequeue: true}` **before**
calling `setRollingUpdateState` on each of its three failure exits: a TLS config error, a
failed `REPLICAOF NO ONE`, and a failed `recordPromotedMaster`. Recording
`verifying-topology` while pod-0 is still a replica would make the next pass verify a
topology that was never established. The third exit needs more than staying put, because
there the promotion already happened: it calls `rollbackPod0Promotion` to hand pod-0 back
to the previously recorded master, closing the window in which the cluster carries a
master the annotation does not name — the rule that owns it is
[ADR 0009](0009-an-unrecorded-promotion-is-not-a-promotion.md) D5.

**D6 — A sidecar-only delta on a single-replica non-Sentinel cluster is deferred, never
applied.** `handleStandaloneRollingUpdate` detects a change affecting exclusively the
sidecar image on a true standalone (`isSidecarOnlyChange`), sets
`SidecarUpdatePending=True`, and leaves the pod running the old sidecar image. Restarting
it would trade in-memory data for a sidecar bump. **Documentation must state that
consequence and not the opposite** — an earlier draft of that README section claimed the
pod "is restarted and its in-memory data is lost", which would have had an admin schedule
downtime for nothing while never learning the real behaviour. That draft was corrected
before it was committed, so the wrong sentence is development history and is not
recoverable from this repository; only the correction is, in the message of commit
`a0ac61f`. The committed README states the deferral ("A single-replica cluster without
Sentinel is not restarted for this"). Do not read the same phrase in the committed metrics
note as the defect: there the pod really is restarted, which is D7's counter-case.

**D7 — The sidecar image must remain the only pod-spec delta an operator upgrade
introduces for single-replica pods.** The D6 deferral compares **images only**, so the
no-delete guarantee holds exactly while that is true. Any future change that alters a
single-replica pod spec beyond the sidecar image breaks the guarantee and must be treated
as a data-loss change. The counter-case stands, verified by reading `isSidecarOnlyChange`
and `buildPodContainers`: enabling metrics adds the `exporter` container, which changes
the pod-spec hash while the sidecar image stays current, so `isSidecarOnlyChange` returns
false and the pod really is restarted. No test drives that combination — the
`isSidecarOnlyChange` cases cover the function, not the metrics path — and it is not
reproduced against a cluster.

**D8 — During an in-flight manual failover the split-brain resolver is told which pod
was promoted.** `handleMultiReplicaRollingUpdate` passes `annotationPromotedPod` to
`detectAndResolveSplitBrain` for the `manualFailover` and `replacingMaster` states.
Inside a rolling update the operator *knows* which pod it promoted, so there is no reason
to guess — and the "most connected slaves" fallback ties at zero in a shrunken cluster,
picks the lowest ordinal (the old master that was just deleted) and demotes the promoted
pod, destroying the data it holds. Any new rolling-update state that promotes must thread
the promoted pod through the same way.

**D9 — Readiness reflects server liveness, not replication health.** A pod with a broken
replication link stays Ready: the readiness probe is a plain PING against a config with
`replica-serve-stale-data yes`, and the sidecar `/readyz` is sticky once a role has been
observed. Consequence to hold on to: **readiness can never be used as a proxy for
replication health anywhere in the operator** — the rolling update waits on sync state,
not on readiness.

*Second half, added 2026-08-25:* readiness is not a proxy for **being spendable** either.
kubelet keeps `PodReady=True` for the whole termination of a pod whose probe still passes,
so a pod the operator itself has just deleted answers Ready until it is gone. Every site
that deletes, promotes or counts a pod therefore asks `podState.available()` rather than
the Ready condition, and the four sites that only need to talk to a pod ask `reachable()`.
The full rule, the carve-out and the delete gate:
[ADR 0026](0026-a-pod-being-deleted-is-not-available.md).

**D10 — Before a promotion, "synced" is the full replication answer, and the wait for it
is bounded.** `waitForReplicasReady` and `verifyReplacedReplicasSynced` ask
`replicationNotEstablishedReason`: role must not be master, `master_link_status` must be
`up`, and no full sync may be running. The three are one answer. A link in
CONNECT/CONNECTING reports `master_sync_in_progress:0` while no byte has moved, so the
sync flag alone accepts a replica that never started -- and the promotion is followed
immediately by the delete of the outgoing master, which is the last copy of the data.
Phase 1 has asked the full question since it was written (`pod0SyncWaitReason`) and the
sidecar always has (`isSyncedReplica`); the gate where the answer decides a promotion did
not, which is the asymmetry this decision removes.

**Zero WAIT acknowledgements is not a partial result.** A cascaded chain acknowledges
through the intermediate node, so `acked < numReplicas` with `acked >= 1` is accepted;
`acked == 0` means no replica confirmed the master offset at all and the failover does not
proceed on it.

**Both waits are bounded by `spec.rollingUpdate.syncTimeout`** and pause the rolling
update on expiry ([ADR 0010](0010-every-rolling-update-wait-is-bounded.md)). The
direction is deliberate: a rolling update that stops half-done keeps a serviceable
cluster and resumes on the next spec change, while a promotion onto a replica that never
synced destroys the dataset and cannot be undone.

**The last look is the key count** (`verifyPromotionCandidateHoldsData`): a candidate that
holds no keys while the outgoing master holds some does not get promoted. The Sentinel path
has verified exactly this since it was written and calls it a critical safety check
(`verifyNewMasterReady`), but it runs *after* the failover, which is early enough there
because the old master is only deleted afterwards; on the manual path the delete follows the
promotion within seconds, so the check has to come before it. An empty master returns early
-- a cluster that holds no data yet must still be able to roll -- and an unreadable count
waits rather than assuming a yes (D3). The two counts are also logged on the way through,
because after the delete of the outgoing master nothing can be asked about what the
promotion was based on.

## Consequences

* Behaviour change on the normal path from D2 is free: the operator watches
  `Owns(&appsv1.StatefulSet{})`, so a successful `Update` in `reconcileStatefulSet`
  enqueues the reconcile that then sees the new template. The rollout starts in the pass
  the write triggers instead of the pass that wrote it — one watch event of added latency.
* While a StatefulSet write is blocked, the pods simply stay put. There is no churn
  symptom left to diagnose, so diagnosability comes entirely from the CR:
  `ReconcileBlocked=True` with reason `AdmissionWebhookDenied` and phase `Error`
  ([ADR 0002](0002-surface-a-blocked-reconcile-on-the-cr.md)).
* A genuinely empty container image in the persisted template silently disables the image
  check for that cluster (D3).
* D4 widens the stall surface: a config-only or resources-only rolling update whose pod-0
  `Delete` never took effect now stalls in the guard. That stall is the guard working;
  the missing bound was the defect, and **this** stall is bounded — the guard is the
  `podNeedsUpdate` branch of `handlePostManualFailover`, one of the six wait branches
  [ADR 0010](0010-every-rolling-update-wait-is-bounded.md) D6 expires into Phase 2. The
  next bullet is a different stall, in a different function, and that one is not covered
  there.
* A permanently failing promotion keeps Phase 1 requeueing (D5) at
  `rollingUpdateRequeueDelay` (10 s), **with no deadline — this one is still open.** The
  Phase 1 bound of [ADR 0010](0010-every-rolling-update-wait-is-bounded.md) D2 does not
  reach it: the bound is armed and evaluated only inside
  `waitOrAbandonTopologyRestoration`, and `handleTopologyRestoration` calls that on the
  sync-wait branch alone (`pod0SyncWaitReason(...) != ""`). All three failure exits of
  `promotePod0AndRedirect` — TLS config error, failed `REPLICAOF NO ONE`, failed
  `recordPromotedMaster` — return a bare requeue instead (the record exit rolls the
  promotion back first, per D5, but the requeue it returns is just as unbounded), and a
  pod-0 that is reachable and already a synced replica keeps `pod0SyncWaitReason` empty on
  every following pass. ADR 0010 does not track it either; it claims Phase 1's sync wait
  (D2) and the six `handlePostManualFailover` branches (D6), and its own open list names
  two other unconverted bounds (`ensureSentinelAwarenessTimestamp`,
  `ensureSyncWaitTimestamp`). Scope, per exit: the TLS exit is unreachable
  from the sync-wait branch, because `Checker.GetReplicationInfo` builds the same config
  from the same Secret and would have failed first and routed the pass into the bounded
  branch — but the self-loop recovery branch calls `promotePod0AndRedirect` without any
  sync-wait check, so there all three exits loop. Verified by reading the branches; not
  reproduced against a cluster, and no test in
  [`internal/controller/rolling_update_bounds_test.go`](../../internal/controller/rolling_update_bounds_test.go)
  covers a repeatedly failing promotion — the only `promotePod0AndRedirect` case there is
  the success path.
* The deferred sidecar update (D6) is a silent divergence between desired and running
  sidecar until the condition is noticed, which is why the condition must be clearable
  from the converged state ([ADR 0002](0002-surface-a-blocked-reconcile-on-the-cr.md) D10).
* Serving stale data from a disconnected replica is the accepted trade of D9: the `-r`
  Service keeps such a pod in rotation. Any future desire to fail readiness on a broken
  master link changes the availability profile of the read Service.
* The whole failover, known-master, topology-restoration and split-brain machinery exists
  to make D1 safe. Because the operator deletes pods directly rather than evicting them,
  a PodDisruptionBudget never constrains it
  ([ADR 0004](0004-opt-in-poddisruptionbudgets.md) D12).

## Alternatives Considered

### Naive StatefulSet `RollingUpdate` ordering

Rejected: it restarts the master without a controlled failover, taking writes down and
risking data loss.

### Keep the split desired-source and gate the rolling update on the write having succeeded

Not chosen. Unifying the source removes the failure mode without adding a new gate, and
pod-spec-only changes were already self-protecting for exactly this reason.

### Treat an empty desired value as a mismatch

Rejected: it would delete pods on the basis of an unreadable template.

### Keep the image-only freshness guard and lean on `DeletionTimestamp`

Rejected: it misses stale cache reads and config-hash-only updates. Reverting to it to
avoid the wider stall surface would trade a visible stall for a wrong `REPLICAOF` target.

### Write a bespoke hash comparison inside the freshness guard

Rejected in favour of reusing `podNeedsUpdate`, so no comparison logic is duplicated.

### Restart the standalone pod for a sidecar-only delta

Rejected for the data loss on an unreplicated standalone.

### Let the connected-slaves heuristic decide during a rolling update

Rejected: it is the mechanism that destroyed a promoted pod's data (observed on a cluster,
not reproducible from this repository). The tie itself is verified by reading
`detectAndResolveSplitBrain`, and the guard against it is
`TestDetectAndResolveSplitBrain_PrefersPromotedPodDuringFailover`.

### A replication-aware readiness probe

Would remove disconnected replicas from the read Service. Not adopted; it would also make
readiness a second, partial source of truth about replication.

## Residual risks

* `podNeedsUpdate` skips the config-hash comparison when a pod lacks
  `vko.gtrfc.com/config-hash` (pods from older operator versions):
  `podAnnotationHashChanged` reports drift only for a pod that carries the annotation
  with a differing value. Deliberate — the same semantics as the rest of the rolling
  update — but it means the D4 freshness guard is inert for config-hash-only updates on
  such pods. It is not inert altogether: `podImageChanged` compares the Valkey and
  sidecar images without consulting any annotation, and `podSpecHashChanged` falls back
  to `containersResourceChanged` against the live template's containers when
  `vko.gtrfc.com/pod-spec-hash` is missing, so image and resource drift on those pods
  still hold the guard.
* D7 is load-bearing as a regression guard, not just documentation: the single-replica
  no-data-loss guarantee silently degrades the day another pod-spec field starts changing
  on upgrade.

## References

* [`internal/controller/rolling_update.go`](../../internal/controller/rolling_update.go) — `checkAndHandleRollingUpdate`, `collectPodStates`, `handleStandaloneRollingUpdate`, `handleMultiReplicaRollingUpdate`, `handlePostManualFailover`, `promotePod0AndRedirect`, `isSidecarOnlyChange`, `podNeedsUpdate`
* [`internal/builder/statefulset.go`](../../internal/builder/statefulset.go) — `ComputePodSpecHash`, the readiness probe
* [`internal/builder/configmap.go`](../../internal/builder/configmap.go) — `replica-serve-stale-data yes`
* [ADR 0001](0001-continue-reconciling-past-a-rejected-write.md) — why the rolling update must survive its own rejected write
* [ADR 0008](0008-known-master-annotation-is-the-recorded-authority.md) — how the promotion decision reaches the pods
* [ADR 0009](0009-an-unrecorded-promotion-is-not-a-promotion.md) — why a promotion may not proceed unrecorded
* [ADR 0010](0010-every-rolling-update-wait-is-bounded.md) — the bounds on every wait this sequence introduces
