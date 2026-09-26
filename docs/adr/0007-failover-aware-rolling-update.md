# ADR 0007: Failover-Aware Rolling Update Against the Persisted Template

## Status

Accepted. Date: 2026-08-21.

The strategy itself predates this ADR set; the template-source and freshness-guard
decisions below landed on branch `feat/support-pdb`.

Amended 2026-08-27: **D2 undercounted its own inputs.** It named four sts-derived values and
the persisted container list; `tlsMaterialHashFromSts`, added by
[ADR 0030](0030-rotating-certificates-rotate-the-instances-that-cannot-reload-them.md) D4, was
never registered here. The count is corrected in place and the rule it states is unchanged —
that input always did come from the persisted template, which is why a blocked StatefulSet
write cannot turn a certificate rotation into a pod-delete loop.

Amended 2026-09-26: **D9's second half counted the delete of an outdated pod among the
sites that ask `available()`, and it no longer is one.**
[ADR 0026](0026-a-pod-being-deleted-is-not-available.md) D11 (ticket T32): an outdated pod
is replaced whether it is available or not, and only a terminating one is waited on. The wait
it removes dates from the first rolling update and was justified as "was recently replaced",
which no outdated pod ever is — and after a spec fix the replacement that never came up is
exactly the outdated pod that wait held on, with nothing to end it but a human
`kubectl delete pod`. On the Sentinel tier the quorum guard now charges only a delete that
spends a vote. The superseded sentence is struck through in place. D10 gains a short passage
separating its sync waits from the availability wait that sits in front of them in the same
two functions and expires differently.

Amended 2026-09-26 (ticket T31, [ADR 0032](0032-generated-pods-run-rootless.md) D3): **D6 and
D7 no longer decide a single pod that runs as root.** The rootless posture is the change D7
warned about — it moves the pod-spec hash of every pod — and the image-only test would have
got it wrong both ways: on the Helm path the new sidecar image would have made it
"sidecar-only" and deferred it, and on kustomize, where the sidecar does not move, a
non-persistent pod would have been deleted with its data. A root pod is decided by
`singlePodDeferral` now — on persistence first, then on whether the Valkey image, the TLS
material record or the config hash changed; D6 and D7 keep their rule for rootless pods. The
superseded sentences are marked in place.

Corrected 2026-09-26, found by the adversarial review of the T32 implementation: **D10 said
the Sentinel path has always verified the new master's key count in `verifyNewMasterReady`.
It never has.** The function reads `DBSIZE`, logs it and refuses only when the count is
unreadable — true since the read was introduced in commit `5214d56`. The claim is struck
through in place; the gap is not closed by T32 and is recorded under Residual risks.

Guards, per decision:

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
  The root half, as amended 2026-09-26: `TestSinglePodDeferral` (every row of the three-way
  rule, TLS and configuration drift included),
  `TestSinglePodDeferral_ReadsPersistenceOffThePersistedStatefulSet`,
  `TestHandleStandaloneRollingUpdate_ReplacesAPersistentRootPod`,
  `TestCheckAndHandleRollingUpdate_DefersANonPersistentRootPodAndReportsIt` and
  `TestCheckAndHandleRollingUpdate_NoPodSecurityConditionWithoutADeferral` in
  [`pod_security_migration_test.go`](../../internal/controller/pod_security_migration_test.go).
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
* D7 for a rootless pod and the first half of D9 — no dedicated test. Verified by reading
  `isSidecarOnlyChange`, `buildPodContainers`, `ProbeCommand` and `HealthServer`; the sticky
  half of D9 is pinned by `TestHealthServer_ReadyzReady`
  ([`internal/sidecar/health_test.go`](../../internal/sidecar/health_test.go)). D7 for a root
  pod is the D6 root half above.
* D9's second half, as amended 2026-09-26 — one pair per delete site, the replace and the
  terminating wait: `TestReplaceNextReplica_ReplacesACandidateThatIsNotReady` /
  `_WaitsForACandidateThatIsTerminating` and
  `TestReplaceRemainingPods_ReplacesAnOutdatedPodThatIsNotReady` /
  `_WaitsForAnOutdatedPodThatIsTerminating` in
  [`sentinel_failover_test.go`](../../internal/controller/sentinel_failover_test.go),
  `TestHandleStandaloneRollingUpdate_ReplacesAnOutdatedPodThatIsNotReady` in
  [`rolling_update_test.go`](../../internal/controller/rolling_update_test.go) with
  `TestHandleStandaloneRollingUpdate_DoesNotReDeleteATerminatingPod` in
  [`pod_termination_test.go`](../../internal/controller/pod_termination_test.go); on the
  Sentinel tier `TestSentinelRollingUpdate_ReplacesTheUnavailableOutdatedSentinelFirst` and
  `TestSentinelRollingUpdate_ReplacesANonVotingPodWhenQuorumIsAlreadyLost` in
  [`pod_availability_test.go`](../../internal/controller/pod_availability_test.go). The
  replace tests name their mutation in their comments. The T32 implementation run of
  2026-09-26 reports `make test-unit` green and 36 mutation checks over the T32 and T31
  guards, all killed; the per-test list is not recorded here, and this document did not
  re-run them. End to end,
  `TestE2E_RollingUpdate_UnavailableReplacementIsReportedAndReplaced`
  ([`test/e2e/pod_availability_test.go`](../../test/e2e/pod_availability_test.go)) passed on
  2026-09-26 on Kind (control plane + 3 workers, Kubernetes v1.36.1, Valkey 9.1.1): after a
  Sentinel cluster's image was put back from an unpullable one, the operator replaced the
  stuck replica itself and every replica held the 100 written keys. The full suite and the
  Valkey 8 leg had not finished at the time of writing.

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

**D2 — Every "desired" input comes from the live StatefulSet, never from the CR.** Five
are named values: `valkeyImageFromSts(sts)`, `sidecarImageFromSts(sts)`,
`configHashFromSts(sts)` (the `vko.gtrfc.com/config-hash` annotation on the persisted pod
template), `podSpecHashFromSts(sts)` and `tlsMaterialHashFromSts(sts)` — the last added by
[ADR 0030](0030-rotating-certificates-rotate-the-instances-that-cannot-reload-them.md) D4
and, until 2026-08-27, never registered here; it reads the `VKO_TLS_MATERIAL_HASH` env of
the persisted template's carrier container, with the superseded
`vko.gtrfc.com/tls-material-hash` annotation as a fallback
([ADR 0031](0031-a-record-the-operator-trusts-lives-in-pod-spec.md)). The sixth is the
persisted template's own container list, `currentSts.Spec.Template.Spec.Containers`, which
every `podNeedsUpdate` call site passes through to `podSpecHashChanged` as the fallback for
a pod carrying no `vko.gtrfc.com/pod-spec-hash` annotation — same source, and precisely the
input the first residual risk below leans on. `v.Spec.Image` and `builder.ComputeConfigHash(v)` are read
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
with the same sts-derived inputs used by `checkAndHandleRollingUpdate` and
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
applied** *(for a pod that runs rootless; a root pod is decided by `singlePodDeferral` since
2026-09-26, see the amendment below)*. `handleStandaloneRollingUpdate` detects a change
affecting exclusively the sidecar image on a true standalone (`isSidecarOnlyChange`), sets
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

*Amended 2026-09-26* ([ADR 0032](0032-generated-pods-run-rootless.md) D3, ticket T31): a
single pod that runs without `runAsNonRoot` — every pod an operator before the rootless
release built — is no longer decided by `isSidecarOnlyChange` but by `singlePodDeferral`
([`pod_security_migration.go`](../../internal/controller/pod_security_migration.go)), on
whether its StatefulSet keeps a volume:

* persistent: replaced at once, sidecar drift or not, with the ownership repair running on
  its way up — one restart, the data kept on the volume;
* not persistent, and the Valkey image, the TLS material record and the config hash all
  unchanged: deferred until the pod is recreated for another reason (a delete, an eviction,
  a node loss), reported as `PodSecurityUpdatePending=True/PodRunsAsRoot` naming the pod. The
  deferral holds every change the pod-spec hash carries together with the posture, because
  the two cannot be told apart; a sidecar drift is deferred with it and still reported as
  `SidecarUpdatePending`;
* not persistent, and the Valkey image, the TLS material record or the config hash changed:
  replaced. None of the three moves on an operator upgrade alone — the image and the
  configuration are the CR author's, and a certificate rotation is the single-pod data loss
  [ADR 0030](0030-rotating-certificates-rotate-the-instances-that-cannot-reload-them.md)
  already accepted.

The images, hashes and persistence it compares all come from the persisted StatefulSet, per
D2 — persistence from its `volumeClaimTemplates`, not from `spec.persistence`, because a
persistence toggle the operator refused to write
([ADR 0023](0023-volume-claim-templates-are-immutable.md)) would otherwise read as persistent
and delete the only pod together with its `emptyDir`. A rootless single pod goes through
`isSidecarOnlyChange` exactly as described above.

**D7 — The sidecar image must remain the only pod-spec delta an operator upgrade
introduces for single-replica pods.** The D6 deferral compares **images only** *(for a
rootless pod; a root pod is decided by `singlePodDeferral` since 2026-09-26)*, so the
no-delete guarantee holds exactly while that is true. Any future change that alters a
single-replica pod spec beyond the sidecar image breaks the guarantee and must be treated
as a data-loss change. The counter-case stands, verified by reading `isSidecarOnlyChange`
and `buildPodContainers`: enabling metrics adds the `exporter` container, which changes
the pod-spec hash while the sidecar image stays current, so `isSidecarOnlyChange` returns
false and the pod really is restarted *(for a rootless pod or a persistent root one since
2026-09-26; a non-persistent root pod defers it, see the amendment below)*. No test drives
that combination — the `isSidecarOnlyChange` cases cover the function, not the metrics
path — and it is not reproduced against a cluster.

*Amended 2026-09-26* ([ADR 0032](0032-generated-pods-run-rootless.md) D3): the rootless
posture is the first such change, and it was treated as one — the D6 amendment above. The
rule is unchanged and still load-bearing, because the image-only test decides every rootless
pod, and after the migration that is every pod the operator builds: the next release that
changes a single-replica pod spec beyond the sidecar image deletes a non-persistent rootless
pod with its data unless it gets its own decision the way ADR 0032 D3 did. The metrics
counter-case now holds for a rootless pod and for a persistent root one; on a non-persistent
root pod enabling metrics is a pod-spec-hash change and waits with the posture under
`PodSecurityUpdatePending`, whose message says that every other pending change of the pod
spec applies on the next restart. Verified by reading `singlePodDeferral` and
`ComputeConfigHash`, which metrics does not enter.

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
so a pod the operator itself has just deleted answers Ready until it is gone. ~~Every site
that deletes, promotes or counts a pod therefore asks `podState.available()` rather than
the Ready condition~~ *(superseded 2026-09-26 for the delete of an outdated pod, see
below)*, and the four sites that only need to talk to a pod ask `reachable()`.
The full rule, the carve-out and the delete gate:
[ADR 0026](0026-a-pod-being-deleted-is-not-available.md).

*Amended 2026-09-26* ([ADR 0026](0026-a-pod-being-deleted-is-not-available.md) D11): every
site that promotes a pod or counts it toward a quorum or a completion asks `available()` —
except `countUpdatedPods`, which deliberately keeps `reachable()` (ADR 0026 D4) — and so does
every delete except the replacement of an outdated pod. **An outdated pod is replaced
whether it is available or not; only a terminating one is waited on**, through
`terminationWait`, and the tier's delete gate still applies. The three sites are the
standalone delete in `handleStandaloneRollingUpdate`, `replaceNextReplica` and
`replaceRemainingPods`. On the Sentinel tier an outdated pod that is neither available nor
terminating is the delete target — the lowest such ordinal, ahead of the lowest outdated
one — and the quorum guard applies only to a delete that spends a vote
(`cost > 0 && readyCount-cost < quorum`): a target that is not available holds no vote and
is replaced even when the quorum is already lost — two of three Sentinels stuck on a broken
spec leave `readyCount` at 1, and a guard that still compared that against the quorum
refused forever. The delete gate serialises those deletes. The delete spends nothing the
roll was not about to spend: masters are never replica candidates, `replaceRemainingPods`
deletes the former master only behind `verifyNewMasterReady` (a replication gate, not a key
count — see Residual risks), the PVC survives a pod delete, a replica re-syncs from its
master, and a single pod without persistence loses nothing the same roll would not take
from it the moment it turned Ready. `deleteNextPendingPod` (a leftover outdated
second master) keeps `available()`, and a pod on the current template is never deleted — for
that pod only the wait is bounded, not lifted.

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
synced destroys the dataset and cannot be undone. *Added 2026-09-26:* the wait in front of
that question — a pod that exists, is not being deleted and is not available, so nothing
can be asked yet — is a different wait with the same budget and a different expiry. The
sync bound is armed only once a replica answers, so that wait runs on the pod's own
not-Ready clock, and past `syncTimeout` it holds and reports `PodAvailabilityStalled`
instead of pausing ([ADR 0026](0026-a-pod-being-deleted-is-not-available.md) D11). It never
promotes either: the pass continues, the roll does not.

**The last look is the key count** (`verifyPromotionCandidateHoldsData`): a candidate that
holds no keys while the outgoing master holds some does not get promoted. ~~The Sentinel path
has verified exactly this since it was written and calls it a critical safety check
(`verifyNewMasterReady`), but it runs *after* the failover, which is early enough there
because the old master is only deleted afterwards;~~ *(wrong since it was written, corrected
2026-09-26: `verifyNewMasterReady` reads the new master's `DBSIZE`, logs it and refuses only
when it is unreadable — it never reads the outgoing master's count and compares nothing. Its
comment calls that a critical safety check; the check does not exist. See Residual risks.)*
On the manual path the delete follows the promotion within seconds, so the check has to come
before it. An empty master returns early
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
  The root deferral of the D6 amendment is wider: a non-persistent root single pod holds
  every pod-spec-hash change with the posture — a resources change or enabling metrics
  included — until it is recreated for another reason or an administrator deletes it, and
  `PodSecurityUpdatePending` is the only signal. A persistent root single pod pays one
  restart at the operator upgrade instead: downtime, not data loss.
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

Rejected for the data loss on an unreplicated standalone. A persistent one would lose only
uptime, and D6 defers its sidecar bump all the same; only a root pod is replaced for having a
volume ([ADR 0032](0032-generated-pods-run-rootless.md) D3).

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
  on upgrade. Since 2026-09-26 that is true of rootless pods; a root pod is decided by
  `singlePodDeferral` (D6 amendment), which is the one change that was caught.
* **The Sentinel path deletes the former master with no key-count gate.** Before the
  `replaceRemainingPods` delete, `verifyNewMasterReady` requires a current, available master
  with at least one connected replica and no sync in progress, and reads its `DBSIZE` — but
  compares it with nothing and never reads the outgoing master's count, so a Sentinel
  failover that promoted an empty replica passes it. `verifyPromotionCandidateHoldsData`
  exists on the manual path only. Pre-existing since commit `5214d56` (2026-02-18), found by
  reading during the T32 review, not fixed by T32, not reproduced against a cluster. Three
  code comments still describe the check as present: the header of `replaceRemainingPods`
  ("has actual data (DBSIZE > 0)"), the inline comment in `verifyNewMasterReady`, and the
  comment above the pre-promotion check in `handleManualFailover`.
* **A Sentinel tier of one or two Sentinels never replaces a Ready outdated Sentinel — open,
  awaiting a decision.** `quorum = replicas/2 + 1` equals `replicas` for both sizes, so the
  D9 guard refuses every delete that spends a vote (`readyCount` is at most `replicas`), and
  on a healthy tier every pass ends on the plain requeue of `sentinelWait` before the status
  write, without a bound. `spec.sentinel.replicas` carries `Minimum=1`, so both sizes are
  admitted. Pre-existing and
  unchanged by T32; T31 moves the pod-spec hash of every Sentinel tier
  ([ADR 0032](0032-generated-pods-run-rootless.md)), so such a cluster hits it at the
  operator upgrade. wds18 runs only three-Sentinel tiers (checked read-only on 2026-09-26);
  other clusters were not checked. Verified by reading `dispatchSentinelRollingUpdate`,
  `sentinelWait` and `runSentinelRollingUpdate`; not reproduced against a cluster.

## References

* [`internal/controller/rolling_update.go`](../../internal/controller/rolling_update.go) — `checkAndHandleRollingUpdate`, `collectPodStates`, `handleStandaloneRollingUpdate`, `handleMultiReplicaRollingUpdate`, `handlePostManualFailover`, `promotePod0AndRedirect`, `isSidecarOnlyChange`, `podNeedsUpdate`, `replaceNextReplica`, `replaceRemainingPods`, `availabilityWait`, `verifyNewMasterReady`, `dispatchSentinelRollingUpdate`, `sentinelScan.deleteTarget`, `sentinelWait`
* [`internal/controller/valkey_controller.go`](../../internal/controller/valkey_controller.go) — `runSentinelRollingUpdate` (the Sentinel-tier residual risk)
* [`internal/controller/pod_security_migration.go`](../../internal/controller/pod_security_migration.go) — `singlePodDeferral`, `reportPodSecurityUpdatePending` (D6, D7 as amended 2026-09-26)
* [`internal/builder/statefulset.go`](../../internal/builder/statefulset.go) — `ComputePodSpecHash`, the readiness probe
* [`internal/builder/configmap.go`](../../internal/builder/configmap.go) — `replica-serve-stale-data yes`
* [ADR 0001](0001-continue-reconciling-past-a-rejected-write.md) — why the rolling update must survive its own rejected write
* [ADR 0008](0008-known-master-annotation-is-the-recorded-authority.md) — how the promotion decision reaches the pods
* [ADR 0009](0009-an-unrecorded-promotion-is-not-a-promotion.md) — why a promotion may not proceed unrecorded
* [ADR 0010](0010-every-rolling-update-wait-is-bounded.md) — the bounds on every wait this sequence introduces
* [ADR 0026](0026-a-pod-being-deleted-is-not-available.md) — D1 on what a spending site asks of a pod, D11 on the replacement of an outdated pod and the availability wait (D9, D10)
* [ADR 0032](0032-generated-pods-run-rootless.md) — D3, the single-pod rule for a pod that runs as root (D6, D7)
