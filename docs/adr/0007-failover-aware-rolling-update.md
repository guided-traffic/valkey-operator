# ADR 0007: Failover-Aware Rolling Update Against the Persisted Template

## Status

Accepted. Date: 2026-08-21.

Amended 2026-09-28: **D1's controlled failover is coordinated where Sentinel supports it, and D10's predicate binds the delete of the outgoing master.** [ADR 0037](0037-the-master-handover-loses-no-acknowledged-write-and-no-dataset.md) D1 sends `SENTINEL FAILOVER <name> COORDINATED` on the first trigger, the forced command as the fallback; its D3 and D5 put the replica-side predicate, a count on the new master, the key-count veto and the role in front of the delete in `replaceRemainingPods`, held past `syncTimeout` rather than paused (D6). The residual risk below that named the missing key check is closed by decision ~~and open until built~~ *(and implemented 2026-09-28: `triggerSentinelFailover` with the coordinated option on the first trigger, and the handover gate `gateOutgoingPodDelete` in [`master_handover.go`](../../internal/controller/master_handover.go); unit-tested in [`coordinated_failover_test.go`](../../internal/controller/coordinated_failover_test.go), [`master_handover_test.go`](../../internal/controller/master_handover_test.go) and [`sentinel_failover_test.go`](../../internal/controller/sentinel_failover_test.go); no run of the built code on Kubernetes is recorded here)*. Marked in place.

Amended 2026-09-29 ([ADR 0018](0018-metrics-and-the-exporter-sidecar.md) D11): **D6 and D7 no
longer decide a rootless single pod whose exporter image or environment differs from the
template.** That release moves the exporter image and its environment together with the sidecar
image — the change D7 warned about and the 2026-09-28 residual under D6 named — and the
image-only test deferred the exporter fix as "sidecar-only" on every single-replica cluster with
metrics. Such a pod is decided by `singlePodDeferral` the way a root pod is, and reported as
`PodSecurityUpdatePending=True/ExporterOutdated` when it is held. The residual's sentence that
the deferral "cannot be tightened" is marked in place.

Amended 2026-09-29: **D11 is new — a Sentinel cluster with one data pod is rolled as a single
pod — and D6 and D7 are amended with it.** D1's sequence needs a replica to promote, and the
dispatch sent every Sentinel cluster to it whatever its size: with one data pod the roll asked
Sentinel for a failover about every 15 s, Sentinel refused each one, and neither the data pod nor
any Sentinel pod behind it ever took a change (*Context*). `rollDataTier` now routes that topology
to `handleStandaloneRollingUpdate`, so D6 and its amendments decide it as they decide a single pod
without Sentinel. The adversarial review of that change found that a sidecar-only deferral held
certificate rotations and configuration changes on every rootless single pod, and on this route
let the tiers diverge; Hans decided that a rotated TLS record or a changed config hash is never a
sidecar-only delta (D6, amended), with a guard that makes a release changing the rendered
configuration a decision (D7, amended). The review's other findings are closed in D11 itself:
a deferral interrupting a recorded replacement is settled before it releases the Sentinel roll, a
failover state left by a scale-down is restated rather than dropped, a replacement somebody else
started is recorded, and a tier of one under a CR asking for more waits instead of asking
Sentinel for a failover. D1 and D6 are marked in place. Implemented; unit-tested and
mutation-checked (the D11 guard below). The e2e `TestE2E_RollingUpdate_SentinelSingleDataPod`
([`sentinel_single_pod_test.go`](../../test/e2e/sentinel_single_pod_test.go)) passed three times
in a row before the review fixes, three times after them and once on the final code, on
2026-09-29 on a local Kind
cluster (control plane + 3 workers, Kubernetes v1.36.1, an operator image built from this tree):
the only data pod replaced from Valkey 8 to 9 with no `FailoverTriggered`, its saved key kept,
Sentinel naming it as master again, every replaced Sentinel pod created after
`RollingUpdateComplete`, no Warning Event. It ran alone, not in a full suite and not in CI; the
configuration and rotation paths of the D6 amendment are unit-tested only.

Amended 2026-10-05: **D2 gains a seventh input, the pod metadata record, and D6 and D7 a third
record outside the pod-spec hash.** `spec.podLabels`, `spec.podAnnotations` and their
`spec.sentinel` counterparts reach only the template metadata, and the statefulset-controller
applies template metadata to no running pod under `OnDelete`. A change therefore rewrote both
templates, moved both controller revisions and replaced no pod: measured on 2026-10-05 on
wds18-k8s-main, where a label added to eight clusters reached none of the 21 data pods
chaos-mesh had not happened to kill, a label-keyed scrape stayed down, and
`KubeStatefulSetUpdateNotRolledOut` fired on every data StatefulSet with a pod left on the old
revision. The record (`VKO_POD_METADATA_HASH`, D2) makes the change ride the failover-aware
roll. The data tier keeps the presence rule; the Sentinel tier does not, so the release that
introduces the record rolls every Sentinel tier once (decided by Hans the same day;
[ADR 0005](0005-upgrade-neutral-defaults-and-anti-affinity.md) D11). The record is carried in
the pod spec as [ADR 0031](0031-a-record-the-operator-trusts-lives-in-pod-spec.md) D1 requires,
not in a template annotation. Implemented; unit-tested, 11 of 11 mutations of the new code
killed. The e2e `TestE2E_PodMetadataChangeRollsBothTiers`
([`pod_metadata_test.go`](../../test/e2e/pod_metadata_test.go)) — add a label to both tiers,
then remove it — passed twice on 2026-10-05 on a local Kind cluster (control plane + 3 workers,
an operator image built from this tree, Valkey 9): every data and Sentinel pod replaced and
carrying the label, then none carrying it, every pod on `updateRevision`, both completion Events,
no Warning Event; during the second run the record was read on the `sidecar` and `sentinel`
containers of the persisted templates. A third time inside one full e2e suite on the same image
(Valkey 9, 56 of 56 tests green); not on Valkey 8, not in CI, and the fleet upgrade that adds the
Sentinel roll has not run.

The strategy itself predates this ADR set; the template-source and freshness-guard
decisions below landed on branch `feat/support-pdb`.

Amended 2026-08-27: **D2 undercounted its own inputs.** It named four sts-derived values and
the persisted container list; `tlsMaterialHashFromSts`, added by
[ADR 0030](0030-rotating-certificates-rotate-the-instances-that-cannot-reload-them.md) D4, was
never registered here. The count is corrected in place and the rule it states is unchanged —
that input always did come from the persisted template, which is why a blocked StatefulSet
write cannot turn a certificate rotation into a pod-delete loop.

Amended 2026-09-28: **D2's image input is every container and init container of the
template, not the valkey and sidecar images alone.** `podImagesDrifted` maps the template's
`Containers` and `InitContainers` by name and reports the first pod container whose image
differs; `podOutdated` ORs it in and `sentinelPodNeedsUpdate` uses it. Before this the data
tier compared the valkey and sidecar images only, so an image written onto the `exporter`
or an init container was never rolled away. The rule is stated in D2 below and its
single-pod residual next to D6.

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

Updated 2026-09-26, no decision of this ADR changed: D8 gains a pointer to its Sentinel-path
counterpart, [ADR 0025](0025-a-split-brain-warning-means-one-that-did-not-resolve-itself.md) D9
(no split-brain resolution while the roll's own Sentinel failover is in flight); the residual
risk on a Sentinel tier of one or two is closed in place by
[ADR 0024](0024-the-sentinel-tier-reports-its-own-completion.md) D10; the D9 guards record their
final e2e runs.

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
  stuck replica itself and every replica held the 100 written keys. ~~The full suite and the
  Valkey 8 leg had not finished at the time of writing.~~ *(Updated 2026-09-26: it passed inside
  both full local suites the same day, 51/51 on Valkey 9 and on Valkey 8, and again inside both
  full suites on one operator image built from the final code of the branch, 53/53 on each line —
  Kind, Kubernetes 1.36.1, containerd 2.3.1, runc 1.4.2, Linux 6.10; locally, not in CI.)*
* D11 — the twelve tests of
  [`sentinel_single_pod_roll_test.go`](../../internal/controller/sentinel_single_pod_roll_test.go):
  the replacement without a failover (an image change, and a persistent pod that runs as root),
  a configuration change behind a stale sidecar replaced, a held non-persistent root pod
  reported, a leftover failover state restated and settled, a replacement somebody else started
  recorded, the pass-by-pass hold of the Sentinel roll up to the completion, a deferral that
  releases it and one that interrupts a recorded replacement and does not, a tier of one under a
  CR asking for more, a scale-down the StatefulSet does not carry yet, and the route predicate.
  The D6 and D7 amendments of 2026-09-29: `TestSinglePodDeferral_ARotationOrAConfigChangeIsNotSidecarOnly`
  ([`pod_security_migration_test.go`](../../internal/controller/pod_security_migration_test.go))
  and `TestComputeConfigHash_PinnedForTheSinglePodRule`
  ([`configmap_test.go`](../../internal/builder/configmap_test.go)). Mutation-checked on
  2026-09-29, each mutation restored and `cmp`-verified: the old dispatch turns four red (it keeps
  the pod, records `failover-triggered` and emits `FailoverTriggered` — the first step of the
  loop, now reproduced in a unit test); dropping the `RollingUpdateComplete` Event, the
  StatefulSet count of the route predicate, the settle, the restate, the early record, the
  tier-of-one guard, the TLS or the configuration term of `sidecarOnlyDelta`, or letting a
  deferral hold, each turns its own test red, and a changed rendered configuration turns the
  pinned hashes red.

Amended 2026-08-22: **D10 is new.** D1 and D9 both say the failover waits on replication
state, and the code asked only `master_sync_in_progress`, which a replica that has not
started syncing answers with 0 -- so a pod holding nothing could pass the last gate before
a promotion whose next step deletes the outgoing master. Found by reading, while
investigating a CI failure whose promoted master served an empty dataset; that failure is
**not** explained by this defect (the WAIT gate did report an acknowledging replica) and
stays open.

Amended 2026-09-27: document references follow the documentation layout of ADR 0035 and ADR 0036; no rule changed.

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

The failure that forced D11: a Sentinel cluster with one data pod — the default shape, since
`spec.replicas` defaults to 1 and nothing refuses `sentinel.enabled` beside it — went to the
failover roll like every Sentinel cluster. `replaceNextReplica` found no candidate, and
`handleMasterFailover` passed every gate vacuously: `waitForReplicasReady` had no replica to
ask, and `waitForWriteSync` returns at zero replicas. It recorded `failover-triggered`, emitted
`FailoverTriggered` and asked every Sentinel for a failover, which each refused (`NOGOODSLAVE`,
nothing to promote); on the next pass `clearStaleRollingUpdateState` discarded the state because
no pod counted as replaced, and the same step ran again, about every 15 s. Every pass ended on
that requeue, before the Sentinel roll and before the status write. The sidecar image is the
operator image, so every operator upgrade entered the loop, and so did every change of the
image, the configuration, the pod spec or the TLS material: the data pod and every Sentinel pod
kept their old spec — the root posture of earlier releases
([ADR 0032](0032-generated-pods-run-rootless.md)) and rotated-away TLS material
([ADR 0030](0030-rotating-certificates-rotate-the-instances-that-cannot-reload-them.md))
included — with the status frozen and no condition saying why, until a human deleted the pod or
scaled the cluster. Found by reading; the first step of the loop is reproduced by a unit test
under the old dispatch (D11's guard), the loop itself was not run against a cluster.

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
successor already exists. *(Amended 2026-09-29: a Sentinel cluster with one data pod has no replica to migrate or promote and is not rolled by this sequence; it is rolled as a single pod, D11.)* *(Amended 2026-09-28: the controlled failover is `SENTINEL FAILOVER <name> COORDINATED` where the Sentinel supports it and the forced command otherwise — [ADR 0037](0037-the-master-handover-loses-no-acknowledged-write-and-no-dataset.md) D1.)* *(Implemented 2026-09-28: the Sentinel roll's first trigger, `handleMasterFailover`, sends the coordinated command; a Sentinel that refuses the option itself — `NOGOODPRIMARY`, or the `ERR` a Sentinel before Valkey 9.0 answers — is asked the forced command at once, and so is every later Sentinel of that pass (`coordinatedFallbackReason`). The retrigger after a failover that did not complete, `handleFailoverRetrigger`, and the sidecar's drain failover stay forced. The non-Sentinel roll's promotion is unchanged.)*

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

Amended 2026-09-28: **the image input is every container and init container of the
template, not the valkey and sidecar images alone.** `podImageChanged` still answers the
narrow valkey/sidecar question the single-pod deferral asks
(`singlePodDeferral`, `isSidecarOnlyChange`, D6), but whether a pod is outdated is decided
by `podImagesDrifted(pod, &sts.Spec.Template.Spec)`: it maps the template's `Containers`
and `InitContainers` by name and reports the first pod container whose image differs,
skipping a name the template does not carry. `podOutdated` ORs it in, and
`sentinelPodNeedsUpdate` uses it for the Sentinel tier. Before this, the data tier compared
the valkey and sidecar images only, so an image written onto the `exporter` container or an
init container — which a holder of `pods: patch`, or of the `<cr>-sidecar` token that may
patch this cluster's data pods, can do — differed from the template and was never rolled
away. A pod the statefulset-controller built carries the template's images exactly, so an
honest fleet rolls nothing here and an operator upgrade onto this rule replaces no pod of an
unswapped cluster.

*Residual, accepted:* comparing the pod's actual image against the template extends the
same incompatibility D2 already has for the valkey and sidecar images (`podImageChanged`) to
every other container and init container. A mutating admission webhook that rewrites a
container image at pod **create** but not in the StatefulSet template — an air-gapped
registry mirror, say — makes that container drift from the template on every recreated pod,
which reads here as permanently outdated and rolls the pod forever. Before this change the
exporter and init images entered the decision only through the pod-spec-hash annotation,
which such a webhook does not touch, so the exporter specifically was immune; it no longer is.
This is the same failure the valkey and sidecar comparison already had, made uniform rather
than newly introduced, and it is accepted, not fixed: a pod-image-mutating webhook that leaves
the template untouched is unsupported. Closing it would mean reading the pod image back with
mirror-aware tolerance, which the operator has no registry map to do.

*Amended 2026-10-05:* **the seventh input is the pod metadata record**,
`podMetadataHashFromSts(sts)`. It is a digest of the pod labels and annotations the CR author
gives the tier (`ComputePodMetadataHash` over `spec.podLabels` and `spec.podAnnotations`, or
the `spec.sentinel` pair — the user maps, never the merged ones, so an operator-owned label a
release changes does not move it), carried as `VKO_POD_METADATA_HASH` on the tier's carrier
container — the sidecar, the sentinel container — and stamped onto the built StatefulSet in
`reconcileStatefulSet` and `reconcileSentinelStatefulSet`, so the pod-spec hash never moves with
it ([ADR 0031](0031-a-record-the-operator-trusts-lives-in-pod-spec.md) D1–D3). Both tiers stamp
it on every write, empty maps included: an empty desired value means "cannot tell" (D3), so a
record written only for non-empty maps would make the removal of the last entry roll nothing.
`podOutdated` ORs `podMetadataHashChanged` in; `sentinelPodNeedsUpdate` asks
`sentinelPodMetadataOutdated`.

**The two tiers read a pod without the record differently.** The data tier keeps the presence
rule of the TLS record: such a pod is not outdated for it, because otherwise the upgrade that
introduces the record would replace the only pod of a non-persistent single-replica cluster
together with its dataset (D6, D7); every multi-replica data pod takes the record in the roll
its new sidecar image causes anyway
([ADR 0005](0005-upgrade-neutral-defaults-and-anti-affinity.md) D11). The Sentinel tier drops
it: a Sentinel pod without the record is outdated. That tier rolls on an operator upgrade only
when its pod spec or configuration changes (ADR 0005 D11), so with the presence rule every
Sentinel StatefulSet would run on a revision no pod carries from the upgrade on —
`KubeStatefulSetUpdateNotRolledOut` on each of them, the symptom the record exists to end — and
a `spec.sentinel` metadata change would roll none of the pods from before the record. The tier
holds no dataset, so the one roll this costs replaces nothing that carries data. Decided by
Hans, 2026-10-05.

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

**D6 — A sidecar-only delta on a single-replica ~~non-Sentinel~~ cluster is deferred, never
applied** *(for a pod that runs rootless; a root pod is decided by `singlePodDeferral` since
2026-09-26, see the amendment below)* *(with or without Sentinel since 2026-09-29: a Sentinel
cluster with one data pod reaches `handleStandaloneRollingUpdate` too, D11)*. `handleStandaloneRollingUpdate` detects a change
affecting exclusively the sidecar image on a true standalone (`isSidecarOnlyChange`), sets
`SidecarUpdatePending=True`, and leaves the pod running the old sidecar image. Restarting
it would trade in-memory data for a sidecar bump. **Documentation must state that
consequence and not the opposite** — an earlier draft of that README section claimed the
pod "is restarted and its in-memory data is lost", which would have had an admin schedule
downtime for nothing while never learning the real behaviour. That draft was corrected
before it was committed, so the wrong sentence is development history and is not
recoverable from this repository; only the correction is, in the message of commit
`a0ac61f`. [`docs/operations/upgrading.md`](../operations/upgrading.md#a-single-replica-cluster)
(ADR 0035; this record wrote the committed README here) states the deferral ("A
single-replica cluster — with or without Sentinel — is not restarted for this", reworded
2026-09-29 for D11). Do not read the same
phrase in the committed metrics note
([`docs/operations/monitoring.md`](../operations/monitoring.md#enabling-metrics-on-a-running-cluster))
as the defect: there the pod really is restarted, which is D7's counter-case.

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

*Residual (2026-09-28), the single-pod cost of the widened image comparison above:* on a
non-persistent single-replica ~~non-Sentinel~~ cluster *(with or without Sentinel since
2026-09-29, D11)* the deferral still reads the valkey and
sidecar images only. An image written onto that pod's `exporter` or an init container while
the sidecar image also differs reads to `isSidecarOnlyChange` as sidecar-only and is
deferred with it — reported as `SidecarUpdatePending`, not replaced. ~~The deferral cannot be
tightened to notice it: a release that bumps `DefaultMetricsExporterImage` moves the sidecar
and the exporter image together, and replacing a single non-persistent pod for that would
discard its dataset, which D7 forbids.~~ *(Tightened 2026-09-29 for the exporter,
[ADR 0018](0018-metrics-and-the-exporter-sidecar.md) D11: a differing exporter image or
environment is decided by persistence, as a root pod is — a persistent pod is replaced, a
non-persistent one is held and reported as `PodSecurityUpdatePending=True/ExporterOutdated`
instead of `SidecarUpdatePending` alone, so the dataset is still never discarded. An image
written onto an init container is still read as sidecar-only.)* The pod-spec-hash record that would tell a swap from
an upgrade is writable by the same principal (`vko.gtrfc.com/pod-spec-hash`). On every other
topology — multi-replica or persistent — the swap is replaced by the ordinary
failover-aware roll.

*Amended 2026-09-29* (Hans, on the adversarial review of D11): **a sidecar-only delta is one the
records can tell apart.** `isSidecarOnlyChange` compares the Valkey and sidecar images only, so
a rootless single pod running the sidecar of an earlier operator — which every operator upgrade
leaves on it — deferred every certificate rotation and every configuration change with the
sidecar, for as long as the pod lived: the rotated-away key stayed in use
([ADR 0030](0030-rotating-certificates-rotate-the-instances-that-cannot-reload-them.md)), TLS or
auth switched on stayed off, and beside Sentinel the Sentinel tier took a configuration the data
pod did not serve (D11). `sidecarOnlyDelta` adds the two records outside the pod-spec hash: a pod
whose TLS material record or config hash differs from the template is replaced — without
persistence with its dataset, the cost a rotation (ADR 0030) and a configuration change of the
CR author always carried, and the rule a root pod already had. A change of the pod spec alone
still waits with the sidecar (D7). Holding the configuration change and, beside Sentinel, the
Sentinel roll with it was the alternative and lost: a requested TLS or auth switch that silently
does not arrive is the worse failure. The images, hashes and persistence still come from the
persisted StatefulSet (D2).

*Amended 2026-10-05:* **the pod metadata record (D2) is the third record a sidecar-only delta
requires unchanged.** `sidecarOnlyDelta` asks `podMetadataHashChanged` beside the TLS record and
the config hash, and `singlePodReplaceable` replaces on it: a label or annotation change of the
CR author replaces a single pod whose sidecar is outdated — without persistence with its
dataset, the cost a configuration change already carries — and so it does a pod held for a
security repair. A label selectors and policies key on has to reach the pod, and on a single
pod the stale sidecar every operator upgrade leaves would otherwise hold it for the pod's
lifetime. A pod built before the record carries none and is not measured (D2), so the upgrade
that introduces the record defers exactly what it deferred before.

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

*Amended 2026-09-29:* the rendered configuration is under the same rule. A changed config hash
replaces a single pod whatever its sidecar does (D6, amended 2026-09-29), so a release that
changes what `ComputeConfigHash` renders restarts every non-persistent single data pod at the
operator upgrade, with its dataset. `TestComputeConfigHash_PinnedForTheSinglePodRule`
([`configmap_test.go`](../../internal/builder/configmap_test.go)) pins the hash of four shapes and
fails such a release first; the decision it asks for is this rule's. It sees only what the four
shapes render. The TLS record needs no guard: it is Secret content, and no release moves it.

*Amended 2026-10-05:* the pod metadata recipe is under the same rule. A changed record replaces
a single pod whatever its sidecar does (D6, amended 2026-10-05), so a release that changes what
`ComputePodMetadataHash` computes for the same maps restarts every non-persistent single data
pod at the upgrade, with its dataset. `TestComputePodMetadataHash_Pinned`
([`pod_metadata_test.go`](../../internal/builder/pod_metadata_test.go)) pins three shapes and
fails such a release first.

**D8 — During an in-flight manual failover the split-brain resolver is told which pod
was promoted.** `handleMultiReplicaRollingUpdate` passes `annotationPromotedPod` to
`detectAndResolveSplitBrain` for the `manualFailover` and `replacingMaster` states.
Inside a rolling update the operator *knows* which pod it promoted, so there is no reason
to guess — and the "most connected slaves" fallback ties at zero in a shrunken cluster,
picks the lowest ordinal (the old master that was just deleted) and demotes the promoted
pod, destroying the data it holds. Any new rolling-update state that promotes must thread
the promoted pod through the same way.

*Pointer added 2026-09-26:* the Sentinel path has no promoted-pod annotation to thread — Sentinel's
leader, not the operator, picks the candidate — and resolves against Sentinel's live master
(`getSentinelMasterPodName`). During the roll's own Sentinel failover that authority still names
the old master while the candidate already answers master, so in `failover-triggered` the
resolver is not called at all: the double master is reported, not resolved
(`resolveSplitBrainUnlessFailingOver`,
[ADR 0025](0025-a-split-brain-warning-means-one-that-did-not-resolve-itself.md) D9). That is the
Sentinel-path counterpart of this decision, found on Kind on 2026-09-26 as a failover the resolver
kept undoing.

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
refused forever. *(On a tier of one or two Sentinels, whose quorum equals its size, a paid
delete is refused unless `readyCount-cost >= total-1` since 2026-09-26 —
`sentinelDeleteKeepsVotes`, [ADR 0024](0024-the-sentinel-tier-reports-its-own-completion.md)
D10.)* The delete gate serialises those deletes. The delete spends nothing the
roll was not about to spend: masters are never replica candidates, `replaceRemainingPods`
deletes the former master only behind `verifyNewMasterReady` ~~(a replication gate, not a key
count — see Residual risks)~~ *(since 2026-09-28 the handover gate of
[ADR 0037](0037-the-master-handover-loses-no-acknowledged-write-and-no-dataset.md) D3 and D5,
`gateOutgoingPodDelete`: the replica-side predicate on every current replica, a count on the new
master, the key-count veto and the role; the Sentinel path only — Residual risks)*, the PVC survives a pod delete, a replica re-syncs from its
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
*(Amended 2026-09-28: [ADR 0037](0037-the-master-handover-loses-no-acknowledged-write-and-no-dataset.md) D3 and D5 put the same replica-side predicate, a count on the
new master, the key-count veto and the role in front of that delete, held past `syncTimeout` as
`MasterHandoverStalled` rather than paused — ~~decided, not built~~ implemented 2026-09-28 in
`gateOutgoingPodDelete`, on the Sentinel path, where `replaceRemainingPods` is the only caller.
The predicate itself moved onto `valkeyclient.ReplicationInfo.NotEstablishedReason`, which
`replicationNotEstablishedReason` wraps with the pod's name, so every gate above asks the same
answer as the health check, the observer and the sidecar drain — ADR 0037 D2.)*
On the manual path the delete follows the promotion within seconds, so the check has to come
before it. An empty master returns early
-- a cluster that holds no data yet must still be able to roll -- and an unreadable count
waits rather than assuming a yes (D3). The two counts are also logged on the way through,
because after the delete of the outgoing master nothing can be asked about what the
promotion was based on.

**D11 — A Sentinel cluster with one data pod is rolled as a single pod.** When the CR and the
persisted data StatefulSet both ask for one data pod (`singleDataPodBehindSentinel`),
`rollDataTier` hands the data-tier roll to `handleStandaloneRollingUpdate`, not to D1's
sequence: there is no replica to promote, so no failover precedes the delete. Every single-pod
rule applies as it does without Sentinel — D6 and its amendments (a sidecar-only delta deferred
under `SidecarUpdatePending`; a pod that runs as root or carries an outdated exporter replaced
when persistent, held and reported as `PodSecurityUpdatePending` when not), the terminating
gate, and the bounded waits of [ADR 0026](0026-a-pod-being-deleted-is-not-available.md) D11.

The route keeps one roll state, `replacing-replicas`, meaning "a replacement of the only pod may
be in flight" (`handleSentinelSinglePodRollingUpdate`). While it stands every pass re-enters the
dispatch — without a state the dispatch skips a missing pod and a current one and sees no roll —
and the handler's waits hold the Sentinel roll (ADR 0026 D11):

* it is recorded before the pod is deleted, and before the terminating check, so a replacement
  somebody else started — an eviction, a manual delete of the outdated pod — is waited out like
  the operator's own (`recordSinglePodReplacement`);
* a state the failover roll left — a scale-down to one pod in the middle of that roll — is
  restated as `replacing-replicas`, never dropped, since the only pod may then be missing or
  booting (`restateAsSinglePodReplacement`). `replacing-replicas` is also the one state
  `clearStaleRollingUpdateState` never discards, so a route that flips back to D1's sequence in
  the middle of a replacement starts it normally;
* a deferral while the state stands — an operator upgrade moved the sidecar while a replacement
  was on its way — is settled first: the pod is waited on until it is available, the deferral's
  report carried along, and then the state is cleared (`settleDeferredReplacement`);
* the completion emits `RollingUpdateComplete`
  ([ADR 0024](0024-the-sentinel-tier-reports-its-own-completion.md) D1), `finishDataRoll` clears
  the state, and the Sentinel roll starts in the same pass.

A deferral with no replacement recorded holds nothing: the handler returns neither a requeue nor
a `DeferredRequeueAfter`, so the Sentinel roll runs in the same pass beside the held data pod and
no `RollingUpdateComplete` fires, because the data tier did not roll. That is safe because of
what a deferral can hold. The pod is up — a deferral that interrupts a replacement is settled
first. And it holds the sidecar image with whatever the pod-spec hash carries (D7), never the
Valkey image, the TLS material or the configuration: a new image replaces a held repair
(`singlePodReplaceable`), and a rotated TLS record or a changed config hash is never a
sidecar-only delta (D6, amended with this decision). So no Sentinel is taken onto a data spec that
does not come up, nor onto a protocol — a TLS port, a password — the data pod does not serve.
It is also what lets the Sentinel pods beside a held root data pod become rootless.

Both counts decide the route, because either one alone sends a scale in flight down the wrong
path: a scale-down the StatefulSet does not carry yet (CR 1, StatefulSet 3) would reach a handler
that deletes the master without a failover, and a scale-up it does not carry yet (CR 3,
StatefulSet 1) would restart the only pod before the replicas it can fail over to exist. That
scale-up stays on D1's sequence, which holds a tier of one — the pass continuing, with a
`DeferredRequeueAfter` — instead of asking Sentinel for a failover it can only refuse; it lasts as
long as the StatefulSet does not carry the scale-up, one pass on a stale cache, or as long as a
refused StatefulSet write, which its own step reports.

While the pod restarts, Sentinel marks the master `o_down`, aborts its own failover for want of a
replica (`-failover-abort-no-good-slave`) and keeps naming the pod to
`get-master-addr-by-name`; within 15 s of the pod's return it reports `flags master` again —
measured on docker with `valkey/valkey:9.1.1` and `8.1.9`, three Sentinels with the operator's
settings, no operator, TLS, auth or persistence. The replacement's init container asks Sentinel
for the master, gets its own hostname (`announce-hostnames yes`) and boots as master
(`init-config-selector` in [`statefulset.go`](../../internal/builder/statefulset.go)) — by reading.

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
* D11 charges a Sentinel cluster with one data pod what a single pod without Sentinel already
  pays: every applied change is one restart of its only master — downtime until the replacement
  is Ready, Sentinel reporting the master down meanwhile — and, without persistence, the dataset
  on a replacement. What is deferred without Sentinel is deferred here, reported by the same
  conditions: a sidecar-only delta, and without persistence the rootless posture and the exporter
  update. The first pass after the operator upgrade that carries D11 takes what the loop had
  held by these rules: the data pod is replaced when its Valkey image, TLS material or
  configuration changed meanwhile, or when it is persistent and still runs as root; a
  non-persistent root pod is held and named; anything else waits with the upgrade's sidecar. The
  Sentinel tier rolls in every case.
* The D6 amendment of 2026-09-29 makes a certificate rotation and a configuration change replace
  every single data pod, with or without Sentinel, even while it runs an earlier operator's
  sidecar — without persistence with its dataset, where the image-only test used to hold both
  for the pod's lifetime. A release that changes the rendered configuration now restarts every
  non-persistent single pod at the upgrade; the pinned hashes of D7's amendment make that a
  decision the release has to take.
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

### Keep the failover roll for a Sentinel cluster with one data pod (D11)

Three variants, decided against on 2026-09-29:

* **Refuse the topology by a CEL rule** (`replicas >= 2` while Sentinel is enabled). Cheap, but
  CEL judges writes only: stored CRs keep looping, and since the rule reads two fields it sits on
  `spec`, so every spec edit of such a CR is refused until the same edit fixes the topology. It
  also refuses the default shape and a legitimate setup — one pod behind a Sentinel endpoint,
  for example a development cluster using the client configuration of production.
* **Stop retrying and report** a level condition, the Sentinel roll held. The loop ends and is
  visible, but every operator upgrade becomes a manual pod delete per cluster, which bypasses
  D6's deferrals, and the root posture and stale TLS material stay until somebody acts.
* **Surge a second data pod**, fail over to it, replace the first and scale back. The only
  variant without downtime and without data loss on a pod without persistence, but a new state
  machine with failure modes of its own — a surge pod held Pending by a ResourceQuota or `hard`
  anti-affinity, a claim left behind, a StatefulSet replica count that differs from the CR — for
  a guarantee a single pod without Sentinel does not have either.

### Hold a configuration change behind the stale sidecar (D6, amended 2026-09-29)

Keep the image-only sidecar test for the configuration, and beside Sentinel hold the Sentinel
roll while the data pod's config hash differs, so the tiers cannot diverge. Upgrade-neutral with
no new restart, and lost: TLS or auth switched on for a single-pod cluster after any operator
upgrade stays off — on both tiers beside Sentinel — until somebody deletes the pod, reported only
as an outdated sidecar, and a pending configuration change would hold the Sentinel tier's own
changes as well.

### Pod metadata: fold it into `vko.gtrfc.com/pod-spec-hash` (D2, amended 2026-10-05)

No new record, the user maps digested together with the built spec. Lost: on a single data pod a
pod-spec change cannot be told apart from the sidecar bump and waits with it (D7), and every
single pod carries a deferred sidecar after its first operator upgrade, so a label change would
wait on those clusters until something else replaced the pod.

### Pod metadata: compare the pod's labels and annotations with the template directly

No record at all. Lost: it sees an added or changed entry but never a removed one, because a pod
carries labels from others too (the sidecar's `instanceRole`, the statefulset-controller's, a
human's); and a mutating admission policy that rewrites a label value at pod create would make
every pod outdated forever.

### Pod metadata: roll on `controller-revision-hash != status.updateRevision`

Lost: it rolls on every template delta, including the ones the operator defers on purpose (D6,
the retired repair's timing), and it makes the revision the driver, which
[ADR 0024](0024-the-sentinel-tier-reports-its-own-completion.md) D3 keeps out of the decision.

### Pod metadata: patch the template metadata onto the running pods

No restart. Lost: the pods keep the old revision, so the revision gap and its alert never close
and `sentinelRolloutComplete` never completes; and it is a pod-metadata write the operator does
not make today ([ADR 0020](0020-write-only-what-the-operator-owns.md)).

### Pod metadata: carry the record in a pod template annotation

The first design of this amendment. Lost to [ADR 0031](0031-a-record-the-operator-trusts-lives-in-pod-spec.md)
D1: the sidecar may patch its own pod's metadata, and with the data tier's presence rule a pod
whose record was deleted is unmeasured, so it would opt out of every later metadata roll.

### Pod metadata: the presence rule on the Sentinel tier too

No extra roll at the upgrade. Lost (Hans, 2026-10-05): every Sentinel StatefulSet runs on a
revision no pod carries from that upgrade on, the alert fires on each, and a `spec.sentinel`
metadata change rolls none of the pods from before the record until chance replaces them.

### Pod metadata: no presence rule on either tier

Lost: the upgrade that introduces the record replaces the only pod of every non-persistent
single-replica cluster, with its dataset.

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
* ~~**The Sentinel path deletes the former master with no key-count gate.**~~ **Closed
  2026-09-28: the handover gate asks the key counts before that delete**
  ([ADR 0037](0037-the-master-handover-loses-no-acknowledged-write-and-no-dataset.md) D3, D5, D6;
  `gateOutgoingPodDelete`). Every other pod on the current template that exists and is not
  terminating must answer as a synced replica and the new master must count at least that many
  attached replicas, at least one; the new master's `DBSIZE` must be readable; the ADR 0028 veto
  (`demotionRefusalReason`) must let the delete through — a new master holding keys, or both
  empty; and the pod about to be deleted must not answer `role:master` (no answer passes). A
  refusal requeues inside `spec.rollingUpdate.syncTimeout` and is reported as
  `MasterHandoverStalled` past it, the rolling-update state kept. Unit-tested
  (`TestReplaceRemainingPods_DeletesTheOutgoingPodOnlyWhenNothingIsLost`,
  `TestHandleRollingUpdate_TheRefusalShapeKeepsTheDataset`); the full e2e suite and the writer harness of ADR 0037 ran green on the built code on Kind, both Valkey lines, the refusal shape itself not driven on a cluster. As recorded until then: Before the `replaceRemainingPods` delete, `verifyNewMasterReady` requires a current, available master
  with at least one connected replica (its "no sync in progress" term reads
  `master_sync_in_progress`, a field a master never reports, and never fires), and reads its
  `DBSIZE` — but compares it with nothing and never reads the outgoing master's count, so a
  Sentinel failover that promoted an empty replica passes it. `verifyPromotionCandidateHoldsData`
  exists on the manual path only. Pre-existing since commit `5214d56` (2026-02-18), found by
  reading during the T32 review, not fixed by T32, not reproduced against a cluster. ~~Three
  code comments still describe the check as present~~ *(corrected 2026-09-28: the header of
  `replaceRemainingPods` and the inline comment in `verifyNewMasterReady` say since 2026-09-26
  that the count is logged and not enforced; the comment above the pre-promotion check on the
  manual path still says the Sentinel path "reads the same counts", where it reads one)* *(all
  three rewritten 2026-09-28 with the gate: the manual-path comment now says the Sentinel path
  reads the new master's count before its delete, and the outgoing pod's only when the new master
  is empty)*.
  *(Closed by decision 2026-09-28, [ADR 0037](0037-the-master-handover-loses-no-acknowledged-write-and-no-dataset.md) D3 and D5: the replica-side predicate, a count
  on the new master, the key-count veto and the role in front of that delete; ~~open until
  built~~ built the same day, above.)*
* ~~**A Sentinel tier of one or two Sentinels never replaces a Ready outdated Sentinel — open,
  awaiting a decision.**~~ **Closed 2026-09-26: such a tier rolls serially**
  ([ADR 0024](0024-the-sentinel-tier-reports-its-own-completion.md) D10, `sentinelDeleteKeepsVotes`;
  unit-tested, and `TestE2E_RollingUpdate_TwoSentinelsRollSerially` green locally on both Valkey
  lines). As recorded until then: `quorum = replicas/2 + 1` equals `replicas` for both sizes, so the
  D9 guard refuses every delete that spends a vote (`readyCount` is at most `replicas`), and
  on a healthy tier every pass ends on the plain requeue of `sentinelWait` before the status
  write, without a bound. `spec.sentinel.replicas` carries `Minimum=1`, so both sizes are
  admitted. Pre-existing and
  unchanged by T32; T31 moves the pod-spec hash of every Sentinel tier
  ([ADR 0032](0032-generated-pods-run-rootless.md)), so such a cluster hits it at the
  operator upgrade. wds18 runs only three-Sentinel tiers (checked read-only on 2026-09-26);
  other clusters were not checked. Verified by reading `dispatchSentinelRollingUpdate`,
  `sentinelWait` and `runSentinelRollingUpdate`; not reproduced against a cluster.
* **D11 rests on Sentinel's behaviour while the only data pod restarts**, measured without the
  operator and without TLS or auth (Decision). The route itself is unit-tested and covered end to
  end by `TestE2E_RollingUpdate_SentinelSingleDataPod`, whose runs are recorded in Status.
* **`RollingUpdateComplete` is emitted before `finishDataRoll` clears the state** on D11's
  route, so a clear that fails emits it once more on the next pass. `finalizeRollingUpdate` has
  the same shape on the failover roll. Accepted: a duplicate Normal Event.
* **A tier of one under a CR that asks for more reports nothing of its own** while it waits (D11):
  the refused StatefulSet write that can keep it there is reported by the StatefulSet step, and a
  stale cache lasts a pass. The pod keeps its old spec meanwhile.
* **A Sentinel cluster scaled down to one data pod while its master was a higher ordinal** was
  not traced — the known-master record and the Sentinel monitor address. D1's sequence deletes
  pod-0 in that window as well, since it is an outdated replica there, so D11 does not add the
  case.

* **The introducing release leaves a revision gap the presence rule does not close (D2, amended
  2026-10-05).** On the kustomize or floating-tag path the data tier does not roll on an operator
  upgrade (ADR 0005 D11), and a deferred single data pod is not replaced: both keep a revision
  their template no longer has, and the alert fires on those StatefulSets until the pods are
  replaced. A `spec.podLabels` change does not reach such a pod either, because it carries no
  record, and nothing reports that beyond the standing `SidecarUpdatePending`.
* **Template metadata the operator owns is still covered by no record.** A release that changes a
  base label or a template annotation outside the hashes moves the revision and rolls nothing.
  Today the only such label that changes, `app.kubernetes.io/version`, moves with `spec.image`,
  which rolls anyway.
* **The record makes the CR's metadata reach every pod; it compares no live label.** What a
  pod's own sidecar writes onto its pod after it started is not undone by this mechanism
  ([isolation and tenancy](../security/isolation-and-tenancy.md#what-does-not-hold)).

## References

* [`internal/controller/rolling_update.go`](../../internal/controller/rolling_update.go) — for D11 `rollDataTier`, `singleDataPodBehindSentinel`, `handleSentinelSinglePodRollingUpdate`, `restateAsSinglePodReplacement`, `settleDeferredReplacement`, `recordSinglePodReplacement`, the tier-of-one guard in `handleRollingUpdate`; `checkAndHandleRollingUpdate`, `collectPodStates`, `handleStandaloneRollingUpdate`, `handleMultiReplicaRollingUpdate`, `handlePostManualFailover`, `promotePod0AndRedirect`, `isSidecarOnlyChange`, `podNeedsUpdate`, `replaceNextReplica`, `replaceRemainingPods`, `availabilityWait`, `dispatchSentinelRollingUpdate`, `sentinelScan.deleteTarget`, `sentinelWait`; for the 2026-09-28 amendment `handleMasterFailover`, `triggerSentinelFailover`, `coordinatedFallbackReason`, `handleFailoverRetrigger`, `replicationNotEstablishedReason`; for the 2026-10-05 amendment `podOutdated`, `podMetadataHashChanged`, `podMetadataHashFromSts`, `sentinelPodNeedsUpdate`, `sentinelPodMetadataOutdated`
* [`internal/controller/master_handover.go`](../../internal/controller/master_handover.go) — `gateOutgoingPodDelete`, `verifyNewMasterReady` (moved here from `rolling_update.go` on 2026-09-28), `replicasNotOnNewMaster`, `datasetRefusal`, `holdHandover`
* [`internal/valkeyclient/client.go`](../../internal/valkeyclient/client.go) — `ReplicationInfo.NotEstablishedReason` (D10's predicate), `SentinelFailoverCoordinated`
* [`internal/controller/valkey_controller.go`](../../internal/controller/valkey_controller.go) — `runSentinelRollingUpdate` (the Sentinel-tier residual risk); `reconcileStatefulSet` and `reconcileSentinelStatefulSet` stamp the pod metadata record (D2 as amended 2026-10-05)
* [`internal/controller/pod_security_migration.go`](../../internal/controller/pod_security_migration.go) — `singlePodDeferral`, `reportPodSecurityUpdatePending` (D6, D7 as amended 2026-09-26); `sidecarOnlyDelta` (D6 as amended 2026-09-29 and 2026-10-05), `singlePodReplaceable` (D6 as amended 2026-10-05)
* [`internal/builder/configmap_test.go`](../../internal/builder/configmap_test.go) — `TestComputeConfigHash_PinnedForTheSinglePodRule` (D7 as amended 2026-09-29)
* [`internal/builder/statefulset.go`](../../internal/builder/statefulset.go) — `ComputePodSpecHash`, the readiness probe
* [`internal/builder/pod_metadata.go`](../../internal/builder/pod_metadata.go) — `ComputePodMetadataHash`, `StampPodMetadataHash`, `RecordedPodMetadataHash` (D2 as amended 2026-10-05); `TestComputePodMetadataHash_Pinned` in its test (D7 as amended 2026-10-05)
* [`internal/controller/pod_metadata_test.go`](../../internal/controller/pod_metadata_test.go) and [`test/e2e/pod_metadata_test.go`](../../test/e2e/pod_metadata_test.go) — the pod metadata record's unit guard and e2e
* [`internal/builder/configmap.go`](../../internal/builder/configmap.go) — `replica-serve-stale-data yes`
* [ADR 0001](0001-continue-reconciling-past-a-rejected-write.md) — why the rolling update must survive its own rejected write
* [ADR 0008](0008-known-master-annotation-is-the-recorded-authority.md) — how the promotion decision reaches the pods
* [ADR 0009](0009-an-unrecorded-promotion-is-not-a-promotion.md) — why a promotion may not proceed unrecorded
* [ADR 0010](0010-every-rolling-update-wait-is-bounded.md) — the bounds on every wait this sequence introduces
* [ADR 0026](0026-a-pod-being-deleted-is-not-available.md) — D1 on what a spending site asks of a pod, D11 on the replacement of an outdated pod and the availability wait (D9, D10)
* [ADR 0032](0032-generated-pods-run-rootless.md) — D3, the single-pod rule for a pod that runs as root (D6, D7)
* [ADR 0024](0024-the-sentinel-tier-reports-its-own-completion.md) — D10, the serial roll of a tier of one or two Sentinels (the closed residual risk); D1, the data-tier completion marker D11's route emits
* [`internal/controller/sentinel_single_pod_roll_test.go`](../../internal/controller/sentinel_single_pod_roll_test.go) — D11's unit guard
* [ADR 0025](0025-a-split-brain-warning-means-one-that-did-not-resolve-itself.md) — D9, the Sentinel-path counterpart of D8: no resolution while the roll's own Sentinel failover is in flight
* [ADR 0037](0037-the-master-handover-loses-no-acknowledged-write-and-no-dataset.md) — D1, the coordinated failover of D1 here; D2, the predicate of D10 at every site that says "synced"; D3, D5 and D6, the handover gate in front of the former master's delete (the closed residual risk)
