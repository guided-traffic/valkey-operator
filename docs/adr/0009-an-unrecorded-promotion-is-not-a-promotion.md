# ADR 0009: A Promotion the Operator Could Not Record Is Not a Completed Promotion

## Status

Accepted. Date: 2026-08-21. Overturns the earlier "best-effort known-master publish"
tradeoff, which was re-confirmed as standing on 2026-08-20 and reversed the same week.
That review sequence has no artifact in this repository; the code either side of it does —
`30588bd~1` is the last commit whose `persistKnownMaster` logs a `V(1)` line and returns
nothing.

Implemented on branch `feat/support-pdb` in `30588bd` (manual failover, topology
restoration) and `744b589` (no-master recovery); no release tag contains either commit.
Verified by reading every write site D8 names and by
[`internal/controller/manual_failover_known_master_test.go`](../../internal/controller/manual_failover_known_master_test.go)
and [`known_master_authority_test.go`](../../internal/controller/known_master_authority_test.go).
Not reproduced against a cluster.

## Context

`vko.gtrfc.com/known-master` began life as a hint: a record of the last promotion the
operator observed, consumed by exactly one reader — `GenerateSentinelConf`
([`internal/builder/sentinel.go`](../../internal/builder/sentinel.go)), which pre-seeds the
`sentinel monitor` line so a Sentinel restarting after a failover starts on the right
master. Nothing read it as a data-plane authority, and the write was best-effort:
`persistKnownMaster` logged a `V(1)` line on failure and returned nothing. (The
`KnownMasterPublishFailed` Event belongs to the replica-ConfigMap republish, not to the
annotation write.) A lost write cost a log line. Both ends of that history are in git:
`73f6efe` introduced the annotation with the Sentinel reader as its only consumer, and
`30588bd~1` is the last commit that still discards the write error.

Two changes turned it into a data-plane authority in the same unreleased batch, both of
them in commit `2357946`:

* the steady-state split-brain check **demotes toward** it
  ([ADR 0011](0011-evidence-based-steady-state-split-brain-resolution.md)), and a
  demotion is a `REPLICAOF` that discards the demoted dataset;
* the init script **boots a pod as master from** it
  ([ADR 0008](0008-known-master-annotation-is-the-recorded-authority.md) D8).

Its write paths were still the best-effort writes they had been when nothing read them.
An adversarial review of that batch found three separate defects — at three different
code sites — that were all this one thing: the annotation had become authoritative while
its writers had not. The review is not in this repository; its fixes are (`30588bd` for
the manual failover and the topology restoration, `744b589` for the no-master recovery).

## Decision

**D1 — The invariant.**

> **On the non-Sentinel paths, every write that records a promotion is part of the
> promotion.** It retries where retrying can help, and on failure it fails the pass rather
> than letting the promotion stand unrecorded.

The scope is not a hedge: the Sentinel path has a different master authority and is
deliberately exempt — see D8.

**No future change may relax any of the bound sites back to `_ = r.Update(ctx, v)`.**

**D2 — Manual failover: promote-then-persist, but never delete the old master with the
promotion unrecorded.** `handleManualFailover` runs `promoteAndRedirect` first, then
`persistManualFailoverState` writes four annotations in one `Update` under a bounded
`retry.RetryOnConflict`: `promoted-pod`, `rolling-update-state`, `known-master` and
`manual-failover-started` — the bound of the state this write enters
([ADR 0010](0010-every-rolling-update-wait-is-bounded.md) D6). The bound is armed once by
`armManualFailoverBound`, before the first attempt, so a conflict retry re-applies the same
deadline instead of handing the state a fresh budget per attempt. If the write still does
not land, **the pass fails and the old master is not deleted**. Losing that
Update leaves the cluster failed over with state `""`; the next pass feeds
`knownMaster = ""` into the resolver, and with two replicas the 0–0 connected-slaves tie
demotes the promoted pod — the data loss this whole family of decisions exists to prevent.

**D3 — Retry conflicts only, and hand the caller back a current object.** The retry
covers `apierrors.IsConflict`; non-conflict errors return immediately, because refetching
cannot cure an admission rejection or a lost permission — it only delays the failure.
Inside the retry the CR is re-Get, the same four annotations re-applied, the Update
retried, and the fresh object `DeepCopyInto`d back over the caller's `v`. That copy is not
cosmetic: everything downstream in the same pass (`reconcileReplicaConfigMap`, the master
delete, `updatePhase`, `recordEvent`) keeps operating on `v`, and a stale object there
would write back over the annotations that were just persisted.

**D4 — Topology restoration: move the annotation only after the promotion actually
succeeded.** `promotePod0AndRedirect` writes `known-master` back to pod-0 immediately
after `REPLICAOF NO ONE` succeeds, and republishes the replica ConfigMap in the same step.
A failed promotion therefore never leaves the annotation pointing at a non-master, and on
the abandoned-restoration path the annotation still names the promoted replica — which is
what keeps the resolver correct there.

"Pod-0" in D4 and D5 is what the call sites pass, not what the signature guarantees: the
function takes its target as `masterPodName`, and both branches of
`handleTopologyRestoration` pass `<sts>-0`. A third call site naming anything else would
have to re-establish D5's argument for itself.

**D5 — And roll the promotion back when the record fails afterwards.**
`promotePod0AndRedirect` captures the previously recorded master before promoting, and on
a failed `recordPromotedMaster` calls `rollbackPod0Promotion` to hand pod-0 back to that
master. Stopping the advance to Phase 2 was necessary but left pod-0 master, unrecorded,
for up to the Phase 1 budget (`spec.rollingUpdate.syncTimeout`, default 5 m) while the
`-rw` Service sent it writes that Phase 2 then discarded. The rollback collapses that
window to zero and **loses nothing**, because Phase 1 only promotes pod-0 once it has
fully synced from the promoted replica — making it a replica of that same pod again
discards no data it did not already have. Where nothing else was recorded, or the record
already names pod-0 (the annotation write landed and only the ConfigMap republish
failed), there is nothing to hand back to and pod-0 stays promoted, with a log line saying
so. A rollback that itself fails is logged only.

The sync argument covers one of the two entries into `promotePod0AndRedirect`. The other is
the self-loop branch, taken when `annotationPromotedPod` already names pod-0, which skips
`pod0SyncWaitReason` entirely — and there the conclusion holds for the second reason
instead: `persistManualFailoverState` wrote the same host into `promoted-pod` and
`known-master`, so `previousMasterHost` equals pod-0's own host and `rollbackPod0Promotion`
returns on its `previousMasterHost == pod0Host` guard without touching anything.

**D6 — No-master recovery records *before* it promotes.** `checkAndRecoverNoMaster` calls
`recordPromotedMaster` first and returns that error instead of swallowing it;
`handlePostRollingUpdateChecks` sets phase `Error` with `No-master recovery failed: <err>`
and requeues after 10 s for as long as the write keeps failing. This function never gets a
second chance: the next pass finds a master and short-circuits on `hasMaster`, so the
annotation would name some other pod **permanently**, feeding both the init-script
self-claim and the steady-state authority — and the first demotion would go the wrong way.
Recording first makes the failure recoverable: nothing has been promoted, the returned
error retries the whole recovery next pass, and naming a pod that is still a replica is
harmless meanwhile, because the steady-state check demotes nothing until the named pod
itself reports `role:master`.

**D7 — No escape timeout that eventually promotes without a record.** The no-master
recovery is deliberately unbounded. A bound would reintroduce exactly the unrecorded
promotion the ordering exists to prevent. This is the same shape as the unbounded
manual-failover waits with the **opposite** verdict: those were an accidental stall worth
bounding ([ADR 0010](0010-every-rolling-update-wait-is-bounded.md)), this is a deliberate
trade that carries a phase and a message.

**D8 — The four bound sites, the one exemption, and any future addition.** Bound:
`persistManualFailoverState`, `recordPromotedMaster` inside `promotePod0AndRedirect`,
`rollbackPod0Promotion`, and `checkAndRecoverNoMaster`. A new site that ends a promotion
inherits the invariant, and one already has: `adoptMaster`
([`internal/controller/steady_state_master.go`](../../internal/controller/steady_state_master.go),
[ADR 0011](0011-evidence-based-steady-state-split-brain-resolution.md)) records a promotion
the operator did not perform, returns the `recordPromotedMaster` error, and its callers
consolidate nothing without it.

Not a funnel, either: `persistKnownMaster` is the single writer of the annotation on every
path except `persistManualFailoverState`, which sets it inline in the one `Update` its
conflict retry repeats. The invariant is therefore policed per site, not at one chokepoint.

Exempt: `syncSentinelWithMaster`. `persistKnownMaster` returns its error there as
everywhere, but that caller logs it and continues instead of failing the pass. Sentinel is
its own master authority on that path, the annotation only pre-seeds a restarting
Sentinel's `sentinel monitor` line, and `checkSteadyStateSplitBrain` — the consumer that
turned the annotation into a destructive authority — never runs for Sentinel clusters. So
the promotion stands recorded nowhere without failing the pass, and that is the decision.
It lapses the moment a Sentinel-path consumer reads the annotation as authority.

## Consequences

* **Promotion paths now fail a reconcile pass on an API write error rather than
  proceeding.** That is the intended trade: a failed pass retries, a lost record does not.
* **A multi-replica non-Sentinel cluster with no master is not recovered while the CR
  cannot be written.** Every pod reports `role:replica`, the `-rw` Service has no
  endpoint, every write fails — and it stays that way for as long as the CR write path is
  broken (a fail-closed admission webhook on the CR, lost `valkeys` RBAC, a permanently
  conflicting writer). Reads keep working through the `-r` Service.
* That freeze must be findable in the field, which is the whole reason it is documented:
  `kubectl get valkey <name>` shows phase `Error`, and `kubectl describe` (or `-o yaml`)
  carries `.status.message` = `No-master recovery failed: <err>`. There is no Message
  print column on the CRD, so the plain `get` shows the phase but never the message.
* **`ReconcileBlocked` does not name this failure, and must not be read as if it did.**
  The condition has one non-test writer, fed by the outcome of `reconcileResources`
  ([ADR 0002](0002-surface-a-blocked-reconcile-on-the-cr.md)) — ConfigMaps, TLS
  Certificates, Services, sidecar RBAC, StatefulSets, Sentinel resources, PDBs,
  NetworkPolicies, monitoring. All of those are managed **child** objects; every CR
  `Update` lives on the rolling-update path instead. A webhook scoped to `valkeys`, lost
  `valkeys` RBAC or a permanently conflicting CR writer therefore leaves the child writes
  succeeding and `ReconcileBlocked` `False`/absent — the phase, the message and the
  operator log are the whole signal for exactly the failure class this ADR is about. Not
  verified against a cluster: whether the phase write itself survives depends on the
  status subresource staying writable, `valkeys/status` being a separate RBAC resource and
  a webhook's own scope deciding whether it intercepts it.
* Write availability of a no-master cluster is gated on a human repairing the CR write
  path. The freeze is indefinite **by design**.
* No error is swallowed and no state machine advances past an unrecorded promotion
  ([ADR 0001](0001-continue-reconciling-past-a-rejected-write.md)) — but a *partially*
  applied record is possible, which is why D5 and D6 argue each partial state benign and
  retried rather than claiming none exists. `recordPromotedMaster` is three non-atomic
  steps (`persistKnownMaster`, `clearDrainStamps`, `reconcileReplicaConfigMap`): a failure
  of the last one leaves the annotation persisted and the replica ConfigMap stale — the
  case D5 reads as "the record already names pod-0, there is nothing to hand back to". And
  on the topology path the promotion has already happened when the record fails, so a
  `rollbackPod0Promotion` that itself fails (best-effort, logged only) leaves pod-0 master
  and unrecorded until the Phase 1 bound abandons the restoration.

## Alternatives Considered

### Best-effort annotation writes with an Event on failure

Explicitly overturned. The pre-existing behaviour was weaker still — `persistKnownMaster`
logged a `V(1)` line and recorded no Event at all (git: `30588bd~1`) — and even the
evented variant is rejected. Once the annotation is read as authority, an unrecorded
promotion is indistinguishable from no promotion, and the operator then demotes toward a
stale record and discards the promoted dataset.

### `retry.OnError` over all error classes

Rejected: refetching cannot cure an admission rejection or a missing permission, so
retrying those only delays the failure. `TestHandleManualFailover_DoesNotRetryNonConflictErrors`
pins the narrow scope; it passes with and without the retry by design, precisely so the
codebase does not drift into retrying everything.

### Persist before promoting, in the manual failover

Would close the crash window completely. Rejected on 2026-08-20 as too much state-machine
rework for that margin — see the residual below. The date is review history with no
artifact in this repository. What makes the same ordering cheap in D6 and expensive here:
`checkAndRecoverNoMaster` records one annotation naming a pod it chose before it spoke to
any Valkey (`<sts>-0`, always) and enters no state, while `persistManualFailoverState` also
names the pod `findPromotionCandidate` picked and enters `stateManualFailover` — persisting
first there means entering that state for a promotion that may still fail.

### Leave the unrecorded pod-0 promotion standing and let Phase 2 clean up

The intermediate state of the fix round — development history, never committed: `30588bd`
added the non-advance and `rollbackPod0Promotion` in one change. Rejected: it leaves a real
write window whose contents Phase 2 then discards.

### Promote first and record best-effort in the no-master recovery

The pre-fix behaviour was weaker than the heading: `checkAndRecoverNoMaster` promoted
pod-0 and recorded nothing at all (git: `744b589~1`). A silent data-loss path, traded here
for a visible availability freeze.

### Bound the no-master recovery and promote on expiry

Rejected — see D7.

## Residual risks

* **An operator crash or total API outage in the instant between a successful promotion
  and a successful persist still leaves the window open.** Small, not zero, and named
  rather than closed: closing it needs persist-before-promote, which was weighed and
  declined.
* The no-master availability freeze (D6/D7) is the deliberate cost of this ADR. It is a
  trade, not a defect, and anyone shortening it must first say what happens to a promotion
  nobody recorded.

## References

* [`internal/controller/rolling_update.go`](../../internal/controller/rolling_update.go) — `handleManualFailover`, `persistManualFailoverState`, `promotePod0AndRedirect`, `recordPromotedMaster`, `rollbackPod0Promotion`
* [`internal/controller/valkey_controller.go`](../../internal/controller/valkey_controller.go) — `checkAndRecoverNoMaster`, `handlePostRollingUpdateChecks`
* [ADR 0008](0008-known-master-annotation-is-the-recorded-authority.md) — what the annotation is and who reads it
* [ADR 0010](0010-every-rolling-update-wait-is-bounded.md) — the opposite verdict on an accidental stall
* [ADR 0011](0011-evidence-based-steady-state-split-brain-resolution.md) — the consumer that made this invariant necessary
