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

**Four items are open:** narrowing the sidecar Role, the two-master termination window, the
dead `NewLabeler`, and the token the observer mounts from the shared ServiceAccount. See
Residual risks.

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
`pods: [get, list, patch]` with no `resourceNames`, per Valkey CR, while `internal/sidecar`
and `cmd/sidecar` make exactly **one** Kubernetes API call —
`clientset.CoreV1().Pods(namespace).Patch` in `patchMetadata`, reached from `PatchLabel`
(own pod, `instanceRole`) and `PatchAnnotation` (a peer pod, the drain stamp). `get` and
`list` are granted and never used; verified by grep over both packages, not assumed. The
recommended order is:

1. drop `get` and `list`, keep `patch` — one line, no ordering concern;
2. give the observer its own ServiceAccount with no Role at all, **and** set
   `automountServiceAccountToken: false` on its pod spec
   ([`internal/builder/observer.go`](../../internal/builder/observer.go)). No builder in this
   repo sets that field — grep over `internal/builder` returns nothing — so without it the
   observer keeps mounting a token even once its ServiceAccount grants nothing;
3. then `verbs: [patch]` with `resourceNames: ["<sts>-0" … "<sts>-N-1"]`.

Dropping `list` is a **hard precondition** for step 3: `resourceNames` is incompatible with
`list` in Kubernetes RBAC. Step 3 also needs a scale-up ordering guarantee — the name list
derives from `spec.replicas`, so a scale-up must reconcile the Role *before* the new pod's
sidecar starts patching, or that sidecar 403s until the next pass. The operator already
writes the Role on every reconcile, so this is one builder change plus an ordering test, not
a new mechanism.

**D9 — If the outgoing master is ever demoted, the demotion is ordered strictly after the
promotion succeeds and before the redirect loop, and it is best-effort.** The redirect loop
in question is the **operator's**: `promoteAndRedirect`
([`internal/controller/rolling_update.go`](../../internal/controller/rolling_update.go))
skips `masterIdx`, so it never demotes. Not to be conflated with the sidecar's own
`reconfigureReplicas`, which skips by address (`addr == newMasterAddr`) and has no index to
skip. Ordered *before* the promotion, such a demotion would demote the only master in the
cluster with nothing promoted to replace it. Ordered after, the worst outcome — promotion
succeeded but the ConfigMap republish or the delete then fails — leaves a demoted old master
and a promoted new one, which is the intended end state anyway. It loses no data: the operator's
`waitForWriteSync` (`WAIT`, same file, called immediately before the promotion) drained the
pod of writes and it is deleted seconds later.

## Consequences

* **The operator must infer, from indirect evidence, promotions it did not perform**, and
  some of them are unprovable and end in a refusal
  ([ADR 0011](0011-evidence-based-steady-state-split-brain-resolution.md) D11).
* **The stamp lives on the Pod object**, so it does not survive a delete-recreate: a promoted
  pod that loses its node before any pass reads the stamp comes back without it.
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

* **The sidecar Role is still `get, list, patch`, namespace-wide (open).** D8 is the plan,
  not the state of the tree.
* **The two-master termination window is an accepted state (open).** Between the promotion
  and the kubelet stopping `valkey-server`, two pods answer `role:master`. Not damaging today:
  the outgoing sidecar patches its own label to `draining` first, so the `-rw` Service is not
  split; the steady-state check returns early because a rolling-update state annotation is
  set; and since D5 nothing acts on the window destructively. What remains is that **the
  operator produces, on every rolling update, the exact state its own steady-state check
  calls a split brain** — and the outgoing sidecar's `waitForRoleChange` polls until
  `valkey-server` refuses connections rather than until a role change it will never observe
  (bounded by the drain timeout — `--failover-timeout` / `SIDECAR_FAILOVER_TIMEOUT`, default
  60 s — inside the data StatefulSet's 75 s termination grace period). Verified by reading;
  not reproduced against a cluster.
* **`NewLabeler` is dead code and duplicates the live wiring (open).** Its two call sites are
  both in `labeler_test.go`. The live path in `run.go` re-implements the same four steps —
  role detector, pod patcher, `Labeler` construction, Sentinel cross-check — and the two
  cross-check blocks agree today. **That agreement is the hazard**: two copies of one wiring,
  one unreachable, both plausible-looking, and only the reachable one has consequences if
  they drift. A change made in the wrong copy is invisible in production *and* invisible in
  the tests, because the tests exercise the other one. The decision is to delete it, not to
  cover it — covering the happy path of a constructor nobody calls is coverage theatre.
  Verified that nothing needs replacing: both deleted tests assert error strings that are
  already asserted directly elsewhere, and in their wrapped form on the live path.
* **The observer shares the sidecar ServiceAccount and mounts a token it never uses
  (open).** Closing it needs both halves of step 2 in D8: its own Role-less ServiceAccount
  *and* `automountServiceAccountToken: false`.

## References

* [`internal/sidecar/drain.go`](../../internal/sidecar/drain.go) — `Handle`, `findSyncedReplica`, `isSyncedReplica`, `reconfigureReplicas`, `stampPromotion`, `waitForRoleChange`
* [`internal/sidecar/run.go`](../../internal/sidecar/run.go) — the labeler/drain shutdown ordering, the drain timeout
* [`internal/sidecar/labeler.go`](../../internal/sidecar/labeler.go) — `patchMetadata`, `PatchLabel`, `PatchAnnotation`, `NewLabeler`
* [`internal/builder/rbac.go`](../../internal/builder/rbac.go) — `BuildSidecarRole`, `BuildSidecarServiceAccount`
* [`internal/controller/rolling_update.go`](../../internal/controller/rolling_update.go) — operator-side `promoteAndRedirect`, `waitForWriteSync` (D9)
* [`internal/common/annotations.go`](../../internal/common/annotations.go) — `AnnotationDrainPromotedAt`
* [ADR 0011](0011-evidence-based-steady-state-split-brain-resolution.md) — how the stamp is consumed, cleared and outranked
* [ADR 0013](0013-operator-is-cluster-wide-privileged.md) — the surrounding privilege model
