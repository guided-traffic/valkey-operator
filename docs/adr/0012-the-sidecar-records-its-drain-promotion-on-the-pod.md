# ADR 0012: The Sidecar Records Its Drain Promotion on the Pod, Not on the CR

## Status

Accepted. Date: 2026-08-21.

The stamp and the `findSyncedReplica` fix are implemented on branch `feat/support-pdb` —
both in commit `cc7e034` — and covered by
[`internal/sidecar/drain_test.go`](../../internal/sidecar/drain_test.go). Revert-verified by
the implementing agent: a process claim with no artifact in this repository. The
labeler/drain ordering is **pre-existing** — `labeler.Run` already precedes the drain
handler's `Handle` on `main`, checkable with `git show main:internal/sidecar/run.go` — and
is now pinned by a regression test, `TestRunSidecar_LabelsThePodThenDrains` in
[`internal/sidecar/run_test.go`](../../internal/sidecar/run_test.go), reachable through the
`runSidecar` seam this change extracted.

Amended 2026-08-21: three of the four open items are closed — D8 step 1 (the Role
now grants `patch` only), D9 (the outgoing master is demoted, guarded by
`TestPromoteAndRedirect_DemotesTheOutgoingMaster`) and the dead `NewLabeler`
(deleted).

Amended 2026-08-21 (second amendment): **D8 is complete.** Step 2 shipped — the observer
runs under its own Role-less ServiceAccount `<cr-name>-observer` with
`automountServiceAccountToken: false` — and so did step 3: the sidecar Role grants
`patch` on named pods only. Step 3 is **wider than the original wording**: the name list
is the union of the pods `spec.replicas` asks for and the pods that currently exist, not
`spec.replicas` alone. D8 records why. Nothing in this ADR is open any more; the residual
risks that remain are named in Residual risks and are consequences of the design, not
unfinished work.

Amended 2026-08-21 (third amendment): the residual this ADR filed against its own D8
step 2 — that the observer ServiceAccount is reconciled by name and a pre-existing one
under that name is relabelled rather than refused — is **closed** by
[ADR 0020](0020-write-only-what-the-operator-owns.md). Its bounded-consequence
reasoning held for the observer and did **not** transfer to the sidecar, which is the
finding that made ADR 0020 necessary; the bullet below is corrected in place.

Amended 2026-08-22: **D10 is new, and it fixes a defect this ADR never considered.** The
record and the ordering were reasoned about carefully; that the drain might not run at all
was not. The kubelet SIGTERMs both containers in one batch and a Valkey with `save ""` exits
within milliseconds, so the drain lost the race often enough to be seen in CI -- observed in
[run 32470830344](https://github.com/guided-traffic/valkey-operator/actions/runs/32470830344),
where no surviving pod was master for seven seconds after the kill, the returning pod-0
self-claimed the role with an empty dataset, and both replicas full-resynced their only copy
away. A preStop hook on the Valkey container now waits for the drain. Guarded by
`TestDrainSignal_OnlyWhereTheDrainPerformsAFailover`,
`TestDrainPreStop_ExpiresInsideTheGracePeriod`, `TestDrainPreStop_ShellLoopIsBounded` and
`TestDrainPreStop_IsPartOfThePodSpecHash`
([`internal/builder/drain_signal_test.go`](../../internal/builder/drain_signal_test.go)) plus
`TestSignalDrainComplete_EveryExitPathReleasesValkey` and
`TestSignalDrainComplete_MissingMountIsNotAFailure`
([`internal/sidecar/drain_signal_test.go`](../../internal/sidecar/drain_signal_test.go)). The
end-to-end proof that the drain promotes at all is
`TestE2E_NoSentinel_MasterKill_NoSplitBrain`, which since the same date asserts the promotion
directly instead of inferring it from the resulting topology.

## Context

Every Valkey data pod runs a sidecar container. On SIGTERM of a **master** pod — any node
drain or eviction — its drain handler promotes a replica so writes keep flowing. That
promotion is real and it is the operator's business, because the operator's split-brain
resolution demotes toward its own record
([ADR 0011](0011-evidence-based-steady-state-split-brain-resolution.md)) and a demotion is a
`REPLICAOF` that discards a dataset.

The sidecar could not record it. `BuildSidecarRole` grants `pods get/list/patch` and **no
access to the `Valkey` CR at all**, so the operator's known-master annotation kept naming
the drained pod — and the operator then demoted the pod that had taken every write during
the drain.

The earlier conclusion, that the gap "cannot be closed from the operator side alone", was
wrong in an instructive way: it needed only a place the sidecar **already** had permission
to write. That conclusion was recorded in a working note outside this repository and is kept
here because the reasoning, not the note, is what matters.

## Decision

**D1 — The sidecar has no CR access, and that stays.** Granting every data pod's sidecar
write access to the `Valkey` CR would put the operator's own source of truth inside the pod
trust domain — a sidecar that could write the CR could redirect the whole cluster's
replication topology from inside a compromised pod.

**D2 — The promoter records the promotion, on the pod it promoted.** `stampPromotion`
([`internal/sidecar/drain.go`](../../internal/sidecar/drain.go)) patches
`vko.gtrfc.com/drain-promoted-at` (RFC3339 UTC,
[`internal/common/annotations.go`](../../internal/common/annotations.go)) onto the promoted
pod. Recording happens on the side that **knows** a promotion occurred, not on the side that
has to infer it — turning an inference problem into a record, at no new grant.

**D3 — The stamp is written immediately after the promotion and before any other
best-effort step, and it is itself best-effort.** Stamping first means a partially-completed
drain still leaves the evidence behind. A lost stamp is a **degradation** — the operator
falls back to the structural rule, to the recorded pod's own answer, or refuses — never a
**corruption**, so it must not be allowed to fail the drain.

**D4 — No stamp when a peer already answers `role:master`.** During a rolling update the
operator promotes a replica and then deletes the old master **without demoting it**, so the
drain runs on a pod that is still master while the topology already has a new one. Promoting
again would stamp a third pod nobody promoted.

**D5 — `findSyncedReplica` queries every reachable peer before "no master" may be
concluded, and this must not be optimised into an early return.** The failure it closes is
the operator manufacturing the evidence it consumes: on every rolling update of a
non-Sentinel cluster with three or more replicas, the deleted master's sidecar runs its
ordinary failover, `isSyncedReplica` rejects the operator's fresh master (a master is not a
*synced replica*), so it walks on to the next healthy replica and sends it
`REPLICAOF NO ONE`. Two damages, one of them never committed:

* **a forged stamp**, which the operator then reads as evidence of a drain promotion nobody
  performed. That state never existed in a committed tree — the stamp and this fix landed
  together in `cc7e034`, and on `main` `findSyncedReplica` returns at the first synced
  replica and writes no stamp at all (`git show main:internal/sidecar/drain.go`, verified by
  reading); and
* **a `REPLICAOF` fight** — older and pre-existing, present on `main` — because
  `reconfigureReplicas` then points *every* remaining peer at that pod, so the outgoing
  master demoted the incoming one. It left no visible symptom because nothing consumed the
  outcome until the steady-state check arrived, on this same unreleased branch.

Returning at the first synced replica would hand back a promotion target while a master sits
further down the list, reopening exactly this. A genuine node drain has no master among its
peers and is unaffected.

**D6 — The labeler exits before the drain handler runs.** In
[`internal/sidecar/run.go`](../../internal/sidecar/run.go), `labeler.Run` returns on the
cancelled context and only then is the injected drain runner's `Handle` called
(`drain.Handle` inside `runSidecar`; the value is the `drainHandler` that `Run` builds), so
the `instanceRole=draining` label the drain handler patches is never re-patched back to
`master`.
**Exactly one pod carries the master label for the whole drain window** — which is what makes
that window observable to a reconcile pass, and therefore what makes adoption at
`len(labeled) == 1` possible at all. **The sidecar shutdown order is load-bearing for
operator-side split-brain resolution and must not be rearranged for unrelated reasons.**
The ordering itself predates this change; what this change adds is the `runSidecar` seam and
the regression test that pins the order, so a rearrangement now fails a test instead of
silently costing the adoption its precondition.

**D7 — The stamp is security-relevant input, not telemetry.** The operator will issue
`REPLICAOF` against the pods that do **not** carry it. That reclassifies the sidecar's
`pods: patch` grant: least privilege must be re-evaluated whenever an annotation becomes an
authority input.

**D8 — The sidecar Role is to be narrowed, in a fixed order.** `BuildSidecarRole`
([`internal/builder/rbac.go`](../../internal/builder/rbac.go)) grants namespace-wide
`pods: [patch]` with no `resourceNames`, per Valkey CR — exactly the one verb the sidecar
calls: `internal/sidecar` and `cmd/sidecar` make exactly **one** Kubernetes API call,
`clientset.CoreV1().Pods(namespace).Patch` in `patchMetadata`, reached from `PatchLabel`
(own pod, `instanceRole`) and `PatchAnnotation` (a peer pod, the drain stamp). Verified by
grep over both packages, not assumed. The order is:

1. drop `get` and `list`, keep `patch` — **done 2026-08-21**; the exact verb set is pinned
   by `TestBuildSidecarRole`, and the operator rewrites the Role on every reconcile, so
   existing clusters narrow on the next pass;
2. give the observer its own ServiceAccount with no Role at all, **and** set
   `automountServiceAccountToken: false` on its pod spec — **done 2026-08-21**. The
   ServiceAccount is `<cr-name>-observer` (`BuildObserverServiceAccount`,
   [`internal/builder/observer.go`](../../internal/builder/observer.go)); nothing binds a
   Role to it, and `ObserverDeploymentHasChanged` compares both the ServiceAccount name and
   the automount flag, so an observer created before this change is rolled onto the new
   identity instead of keeping the sidecar token forever;
3. then `verbs: [patch]` with `resourceNames` — **done 2026-08-21**, and the list is
   **the union of the desired and the existing pods**, not `["<sts>-0" … "<sts>-N-1"]` as
   this step originally read. The superseded wording covered scale-up and broke scale-down:
   with the list derived from `spec.replicas` alone, scaling 5 -> 3 revokes the grant of
   pods 3 and 4 while they are still terminating, and the departing master needs exactly
   that grant to set `instanceRole=draining` on itself before it fails over — denying it
   keeps writes flowing into a dying master. `BuildSidecarRole` therefore takes the live
   pod names and `SidecarRolePodNames` unions them with the desired ordinals
   ([`internal/builder/rbac.go`](../../internal/builder/rbac.go)); a name leaves the grant
   when its pod is gone, not when the spec stops asking for it.

Dropping `list` was a **hard precondition** for step 3: `resourceNames` is incompatible with
`list` in Kubernetes RBAC.

Three properties of step 3 hold it together, each with its own guard:

* **Scale-up ordering.** `reconcileResources` runs the `sidecar RBAC` step before the
  `StatefulSet` step ([`internal/controller/valkey_controller.go`](../../internal/controller/valkey_controller.go),
  `resourceReconcileSteps`), so pod N is named before the write that creates it. Pinned by
  `TestResourceReconcileSteps_RBACBeforeStatefulSet`.
* **An empty name list is not an empty grant.** In Kubernetes RBAC `resourceNames: []`
  matches *every* object, so a cluster with no pod to patch gets **no rule at all** rather
  than a namespace-wide one (`TestBuildSidecarRole_NoPodsYieldsNoRuleRatherThanAnOpenOne`).
* **A label does not widen the grant.** The live names come from a label selector, and
  labels are set by whoever creates the pod, so `SidecarRolePodNames` keeps only names of
  the exact form `<sts>-<canonical ordinal>`
  (`TestSidecarRolePodNames_IgnoresNamesThatAreNotThisStatefulSets`).

The pod List is part of the step: if it fails, `reconcileSidecarRole` fails rather than
narrowing the Role on incomplete information — the Role already in the cluster is the wider
one, and leaving it is the safe direction.

**D9 — The outgoing master is demoted, strictly after the promotion succeeds and before the
redirect loop, and the demotion is best-effort.** Implemented 2026-08-21 in the
**operator's** `promoteAndRedirect`
([`internal/controller/rolling_update.go`](../../internal/controller/rolling_update.go)),
whose redirect loop still skips `masterIdx` — the demotion is its own step before that loop,
`REPLICAOF <promotedHost>` against the outgoing master, log-and-continue on failure. Guarded
by `TestPromoteAndRedirect_DemotesTheOutgoingMaster`. Not to be conflated with the sidecar's
own `reconfigureReplicas`, which skips by address (`addr == newMasterAddr`) and has no index
to skip. The ordering is load-bearing: ordered *before* the promotion, the demotion would
demote the only master in the cluster with nothing promoted to replace it. Ordered after,
the worst outcome — promotion succeeded but the ConfigMap republish or the delete then
fails — leaves a demoted old master and a promoted new one, which is the intended end state
anyway. It loses no data: the operator's `waitForWriteSync` (`WAIT`, same file, called
immediately before the promotion) drained the pod of writes and it is deleted seconds later.

**D10 — The Valkey process outlives the start of its own drain, and the sidecar decides
when it may exit.** Everything above assumes the drain handler can talk to the local
Valkey: `Handle` reads its own role from it before anything else, and `isSyncedReplica`
(D5) only accepts a peer whose `master_link_status` is still `up`, which stops being true
the moment the master they replicate from dies. The kubelet gives no ordering between the
SIGTERM it sends the sidecar and the one it sends Valkey, and a non-persistent Valkey
(`save ""`) exits in milliseconds -- inside the handler's own `PatchLabel` call. The drain
then either cannot read its role or finds no promotable peer, and it fails **open**: no
promotion, no stamp, no Event, and one log line in a container that is about to be deleted.

The fix is a `preStop` hook on the **Valkey** container that waits for
`/var/run/vko/drain-complete`, written by the drain handler on every exit path
(`internal/common/drain.go` carries both constants, for the reason D2 gives for the
annotation). Three properties make it a bound rather than a stall: the hook gives up after
60 s, which is inside the 75 s `terminationGracePeriodSeconds` and leaves Valkey room to
shut down; the marker write is a `defer` at the top of `Handle`, so it also covers the
panic path -- a crashing sidecar must not hold Valkey hostage; and the sidecar treats a
**missing mount** as "not this cluster", so the two sides cannot drift apart into a hook
waiting for a file nobody can write.

**Scope: multi-replica without Sentinel only.** That is where the drain performs the
failover itself and where losing it costs a dataset. A Sentinel cluster hits the same first
step -- `DetectRole` bails, so `SENTINEL FAILOVER` is never sent -- but Sentinel's own
`down-after-milliseconds` timer then performs the failover: slower, not lossy. A standalone
pod has nothing to fail over to. Neither pays a preStop on every deletion.

**Native sidecar containers do not solve this** and must not be mistaken for a simpler
version of it. Init containers with `restartPolicy: Always` are terminated *after* the
regular containers, so Valkey would be guaranteed to be gone before the drain starts. That
converts the race into a certainty.

## Consequences

* **The operator must infer, from indirect evidence, promotions it did not perform**, and
  some of them are unprovable and end in a refusal
  ([ADR 0011](0011-evidence-based-steady-state-split-brain-resolution.md) D11).
* **The stamp lives on the Pod object**, so it does not survive a delete-recreate: a promoted
  pod that loses its node before any pass reads the stamp comes back without it.
* **Deleting a data pod of a non-Sentinel multi-replica cluster now waits for the drain**
  (D10). A replica costs one poll tick, a master costs what its failover costs, and a pod
  whose sidecar cannot write the marker costs the full 60 s. Every step of a rolling update
  pays it, because a rolling update is a sequence of pod deletions.
* **Two more moving parts in the pod spec** (D10): a shared `emptyDir` and a shell loop in a
  lifecycle hook. The shell is not a new assumption -- the master-discovery init container
  already runs `sh -c` with `sleep` in the same image.
* **The sidecar runs the operator image** (`operatorImage`,
  [`internal/builder/statefulset.go`](../../internal/builder/statefulset.go)), so an operator
  upgrade leaves a pod writing no stamp until **that pod** has been rolled onto the new
  image — the window is per-pod, not the upgrade itself. A multi-replica cluster closes it
  with the ordinary rolling update; at `replicas: 1` the sidecar-only change is deferred to
  the next natural pod restart with no bound (`SidecarUpdatePending`,
  [`internal/controller/valkey_controller.go`](../../internal/controller/valkey_controller.go)),
  which is bounded in practice only because `checkSteadyStateSplitBrain` skips
  single-replica clusters. For that window the evidence model runs on its other two rules.
* **Everything that can patch a pod in the namespace can influence a topology decision the
  operator will act on destructively.** Cluster A's sidecar can patch cluster B's pods: move
  the `instanceRole` label (redirecting client writes) or forge a drain stamp (steering a
  `REPLICAOF` that discards a dataset). Cross-cluster interference inside a shared namespace
  is currently possible **by design**, and the residual grows with every operator decision
  that reads pod metadata as authority.
* **The observer runs under the same ServiceAccount and makes no API call at all** —
  verified by the absence of any `client-go` import in `internal/observer` and
  `cmd/observer`. An observer pod compromise therefore yields a token with namespace-wide
  `pods get,list,patch` despite the observer needing no API access whatsoever.
* A forged `instanceRole=master` label is **visible** (two labeled masters) but **nothing
  repairs it**. The labeler patches only on a *detected* role change — `poll` returns early
  on `role == l.lastRole` ([`internal/sidecar/labeler.go`](../../internal/sidecar/labeler.go)),
  so a label somebody else overwrote is never re-written until the pod's real role flips or
  the sidecar process restarts and `lastRole` resets. The operator does not repair it either:
  it never writes that label, and `demoteConfirmedRogues` skips a labeled pod whose probe
  reports a non-master role. A forged label therefore stays in the `-rw` Service selector
  (`common.MasterSelectorLabels`) for as long as those two conditions hold. A forged
  `vko.gtrfc.com/drain-promoted-at` is **different in kind**, because the operator accepts it
  as evidence and acts on it destructively — it issues `REPLICAOF` against the pods that do
  not carry it.

## Alternatives Considered

### Grant the sidecar `valkeys` update rights so it can write the known-master annotation

Rejected as a privilege escalation from every data pod. The pod-annotation channel achieves
the same recording with the grant the sidecar already had.

### Leave the operator to infer the promotion from replication state, or from the label alone

Both rejected — the forensic route was disproven by a measurement taken against live pods,
not reproducible from this repository, and the label cannot distinguish a drain promotion
from a self-election
([ADR 0011](0011-evidence-based-steady-state-split-brain-resolution.md)).

### Make the stamp mandatory

Rejected: it turns a metadata write failure into a failed drain.

### Return at the first synced peer in `findSyncedReplica`

Rejected: it misses a master later in the list — the shape that would forge the stamp.

### Leave the sidecar failover unconditional and filter forged stamps operator-side

Not chosen: the operator cannot tell a forged stamp from a real one.

### Keep the sidecar Role as-is, documented

Defensible only while nothing reads pod metadata as authority — which stopped being true when
the stamp landed on this branch. Nothing here has been released.

### Make `runSidecar` call `NewLabeler` instead of deleting it

Collapses the duplicate from the other side, but `NewLabeler` constructs its *own* detector
and patcher while `runSidecar` receives them injected and shares those same two objects with
the drain handler. Calling it would build a second pair or force a signature change, and it
costs the injection seam the sidecar tests rely on.

## Residual risks

* **The preStop is a bound, not a handshake in both directions (D10).** It releases Valkey
  when the marker appears *or* when 60 s pass, and it cannot tell those apart. A drain that
  is genuinely slower than the bound -- a very slow API server for the `PatchLabel`, DNS
  that takes seconds to resolve the peers -- still fails open exactly as before, just far
  more rarely. Making it a true handshake would mean failing the pod deletion, which trades
  a rare lost promotion for a pod that will not go away.
* **`isSyncedReplica` still rejects every peer whose master just died (D5, D10).** The
  predicate requires `master_link_status: up`, which is precisely false in the seconds after
  a master is lost. D10 removes the situation rather than the strictness: while Valkey is
  held open the peers still see their master, so the predicate answers usefully. It was
  considered and **not** relaxed, because loosening it means choosing a promotion target by
  replication offset instead of by a binary "is synced" -- a change to the path that discards
  a dataset when it is wrong, and one that D10 makes unnecessary for the observed failure.
  What stays uncovered: a Valkey that dies *despite* the hook, for instance by crashing
  rather than by SIGTERM.
* **A pod whose sidecar cannot write the marker is slow to delete, fleet-wide (D10).** A
  broken sidecar image, a volume that failed to mount, a sidecar OOM-killed before SIGTERM:
  each costs 60 s per pod deletion, and nothing surfaces the cause on the CR. The kubelet
  event for the expired hook is the only trace.
* **(Closed 2026-08-21) The sidecar Role is namespace-wide `patch`.** D8 is complete: the
  grant is `patch` on this cluster's own data pods by name, so a stolen sidecar token no
  longer reaches another cluster's pods — and therefore cannot forge the drain stamp the
  operator consumes as promotion evidence. What the grant still permits, by construction:
  patching **any metadata** on **this** cluster's pods, including its own `instanceRole`
  and its peers' drain stamps. Nothing narrower is possible — those are the writes the
  sidecar exists to make. A compromised sidecar can still lie about its own cluster.
* **A pod keeps its grant until it is gone, not until the spec drops it.** The union in
  `SidecarRolePodNames` is deliberate (D8 step 3). The cost: a pod that lingers — stuck
  terminating, or orphaned with the cluster's labels — keeps a `patch` grant on its own
  name for as long as it exists. That is strictly narrower than the previous
  namespace-wide grant and it is bounded by the pod's own lifetime.
* **The Role is written from a cache-backed List.** A pod that exists but has not reached
  the operator's informer yet is not in the name list, so a sidecar starting in the same
  instant can see a 403 until the next pass writes the wider list. The labeler logs the
  failure and retries on its next poll without advancing `lastRole`
  ([`internal/sidecar/labeler.go`](../../internal/sidecar/labeler.go)), and the drain
  handler continues with the failover, so neither path loses work over it.
* **(Closed 2026-08-21) The two-master termination window.** `promoteAndRedirect` now demotes
  the outgoing master per D9, so no two pods answer `role:master` beyond the seconds the
  demotion itself takes. What D9 cannot cover: a demotion that fails is logged and the window
  returns for that one failover — accepted, because failing the failover over it would be
  worse. Verified by unit test; not reproduced against a cluster.
* **(Closed 2026-08-21) `NewLabeler` was dead code and is deleted**, together with its two
  tests. Verified that nothing needed replacing: both deleted tests asserted error strings
  that are already asserted directly elsewhere (`TestValkeyRoleDetector_TLSConfigError`,
  `TestNewKubernetesPodPatcher_OutsideClusterFails`), and in their wrapped form on the live
  path (`run_test.go`). The live wiring in `run.go` is now the only copy.
* **(Closed 2026-08-21) The observer shares the sidecar ServiceAccount and mounts a token
  it never uses.** Both halves of D8 step 2 shipped: `<cr-name>-observer`, bound to no Role,
  with `automountServiceAccountToken: false`. Migration costs one observer Deployment roll
  (stateless). What is **not** closed and is new with it: the observer ServiceAccount is
  reconciled by name, so a pre-existing ServiceAccount that happens to carry the derived
  name is adopted and relabelled rather than refused. The consequence is bounded — the
  observer mounts no token, so it gains no capability from a foreign ServiceAccount — and
  the *deletion* side is already guarded by an ownership check and a UID precondition
  ([ADR 0006](0006-delete-only-what-the-operator-owns.md)), so the operator never deletes a
  ServiceAccount it does not own.

  **(Closed 2026-08-21.)** [ADR 0020](0020-write-only-what-the-operator-owns.md) refuses
  the write and reports it. Two corrections to the paragraph above, both of which that
  ADR records: the damage was never "limited to labels" — the annotation map was assigned
  rather than merged, so every annotation on the target was erased — and the
  bounded-consequence argument is observer-specific. It does not carry to the sidecar
  ServiceAccount of the same shape: that token *is* mounted into every data pod, and
  `BuildSidecarRoleBinding` names the ServiceAccount by name without a UID, so a foreign
  object holding it was granted `pods: patch` on this cluster's own pods — the label the
  `-rw` Service selects on and the drain stamp this ADR's D6 has the operator consume as
  evidence.
* **The valkey and exporter containers still carry the sidecar token.** The grant is now
  per-pod-name, but it is mounted into every container of the data pod, not only into the
  sidecar. Closing that means a second ServiceAccount and a token-projection split that
  Kubernetes does not offer per container — it is a pod-level field. Accepted.

## References

* [`internal/sidecar/drain.go`](../../internal/sidecar/drain.go) — `Handle`, `findSyncedReplica`, `isSyncedReplica`, `reconfigureReplicas`, `stampPromotion`, `waitForRoleChange`
* [`internal/sidecar/run.go`](../../internal/sidecar/run.go) — the labeler/drain shutdown ordering, the drain timeout
* [`internal/sidecar/labeler.go`](../../internal/sidecar/labeler.go) — `patchMetadata`, `PatchLabel`, `PatchAnnotation`
* [`internal/builder/rbac.go`](../../internal/builder/rbac.go) — `BuildSidecarRole`, `BuildSidecarServiceAccount`, `SidecarRolePodNames` (D8 step 3)
* [`internal/builder/observer.go`](../../internal/builder/observer.go) — `BuildObserverServiceAccount`, the observer pod identity (D8 step 2)
* [`internal/controller/rolling_update.go`](../../internal/controller/rolling_update.go) — operator-side `promoteAndRedirect`, `waitForWriteSync` (D9)
* [`internal/common/annotations.go`](../../internal/common/annotations.go) — `AnnotationDrainPromotedAt`
* [ADR 0011](0011-evidence-based-steady-state-split-brain-resolution.md) — how the stamp is consumed, cleared and outranked
* [ADR 0013](0013-operator-is-cluster-wide-privileged.md) — the surrounding privilege model
