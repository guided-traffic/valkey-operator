# ADR 0020: Write Only What the Operator Can Prove It Owns, and Grant Only to a Subject It Owns

## Status

Accepted. Date: 2026-08-21.

Implemented on branch `feat/support-pdb`, in the same change as this ADR. Four reconcile
paths carry the guard — `reconcileObserverServiceAccount`, `reconcileSidecarServiceAccount`,
`reconcileSidecarRole` and `reconcileSidecarRoleBinding`, all in
[`internal/controller/valkey_controller.go`](../../internal/controller/valkey_controller.go) —
with the shared refusal machinery in
[`internal/controller/foreign_object.go`](../../internal/controller/foreign_object.go).

This ADR closes one open residual of
[ADR 0006](0006-delete-only-what-the-operator-owns.md): the delete-and-recreate in
`reconcileSidecarRoleBinding` now refuses a foreign binding and sends a UID precondition
when it recreates its own.

**Deliberately out of scope**, and tracked separately: every other managed kind is still
written by generated name with no ownership check. The two that matter most are filed in
`local_valkey_operator_admission_gap.md` as NA61 (the data and Sentinel StatefulSets) and
NA62 (`reconcileServiceMonitor` and `reconcileCertificate` stamp the CR ownerReference onto
an object they never verified, which makes the garbage collector delete a foreign object when
the CR goes). D7 says why they were not taken here.

## Context

Every object this operator manages is named from the CR name, and there is no admission
webhook to constrain that name ([ADR 0015](0015-one-crd-validated-by-schema-only.md)). A CR
called `foo` therefore claims `foo-sidecar` for its sidecar ServiceAccount, Role and
RoleBinding (`SidecarServiceAccountName`,
[`internal/builder/rbac.go`](../../internal/builder/rbac.go)) and `foo-observer` for the
observer ServiceAccount and Deployment
([`internal/builder/observer.go`](../../internal/builder/observer.go)). Whoever may
`create valkeys` in a namespace picks that name.

[ADR 0006](0006-delete-only-what-the-operator-owns.md) closed the destructive half of this:
no delete by generated name without a provenance proof. Its D1 binds **deletions**. Its
Context already records that the *update* path was the mirror image — "Get → HasChanged →
Update silently *adopted* a foreign budget" — but the fix generalised no further than
PodDisruptionBudgets. On the reconcile write path `reconcilePodDisruptionBudget`
([`internal/controller/pdb.go`](../../internal/controller/pdb.go)) is the only managed kind
that checks `metav1.IsControlledBy` before writing.

Three concrete failures made that gap worth closing now. All three were read from the code on
this branch; none is a report of an object observed being damaged on a cluster.

* **The grant follows the name, not the object.** `BuildSidecarRoleBinding` writes
  `Subjects[0] = {Kind: ServiceAccount, Name: SidecarServiceAccountName(v), Namespace: …}` —
  a plain name reference with no UID — and `reconcileSidecarRoleBinding` never reads the
  ServiceAccount. So the binding grants `pods: [patch]` on this cluster's data pods to
  **whatever identity holds that name**, whether the operator created it, overwrote a foreign
  one, or no such object exists at all. This is the finding that reframes the item: refusing
  to write the ServiceAccount closes nothing, because the ServiceAccount write was never the
  channel. Only refusing the *binding* fails closed.

  What the grant buys is not cosmetic. `patch` on a pod reaches the `instanceRole` label the
  `-rw` and `-r` Services select on
  ([`internal/builder/service.go`](../../internal/builder/service.go)) and the
  `vko.gtrfc.com/drain-promoted-at` stamp the steady-state resolver consumes as promotion
  evidence ([ADR 0011](0011-evidence-based-steady-state-split-brain-resolution.md),
  [ADR 0012](0012-the-sidecar-records-its-drain-promotion-on-the-pod.md)). And the token is
  mounted: the data PodSpec sets `ServiceAccountName: SidecarServiceAccountName(v)` and never
  sets `AutomountServiceAccountToken`
  ([`internal/builder/statefulset.go`](../../internal/builder/statefulset.go)), unset meaning
  mount — the single production occurrence of that field in the tree is
  `internal/builder/observer.go`, on the observer.

* **The metadata write erased annotations rather than merging them.** Both ServiceAccount
  reconcilers assigned `current.Annotations = desired.Annotations`, and `desired` carries at
  most the operator-version key (`ApplyOperatorVersion`,
  [`internal/builder/annotations.go`](../../internal/builder/annotations.go)). Every other
  annotation on the target was dropped — `eks.amazonaws.com/role-arn`,
  `iam.gke.io/gcp-service-account`, `kubernetes.io/enforce-mountable-secrets`. Those two
  functions were the **only** two `current.Annotations = …` assignments in
  `internal/controller`; the twelve other reconcilers assign labels and merge the version
  annotation. This was a deviation from the repo convention, not the convention.

* **Nothing brought the operator back.** A colliding object carries no ownerReference to the
  CR, so the `Owns(&corev1.ServiceAccount{})` registration in `SetupWithManager` never maps
  its deletion to a reconcile request, and `predicate.GenerationChangedPredicate` on the CR
  watch drops the operator's own status writes. Without an explicit requeue an administrator
  who removes the collision sees nothing happen — and restarts the operator to force a pass.

## Decision

**D1 — No write onto a generated name the operator cannot prove it owns.** Provenance is
`metav1.IsControlledBy(obj, v)`, exactly as [ADR 0006](0006-delete-only-what-the-operator-owns.md)
D2 defines it for deletes; a label is not a proof. The rule binds the four paths listed under
Status. It does **not** yet bind every managed kind — see D7.

The wording avoids the word *adoption* on purpose: throughout
[ADR 0011](0011-evidence-based-steady-state-split-brain-resolution.md) and `CLAUDE.md` that
word means master adoption, and `MasterAdoptionRefused` is a live Event reason.

**D2 — A refusal that costs the CR nothing does not fail the pass; a refusal that leaves the
CR unusable does.** The two ServiceAccounts sit on opposite sides of this line, and the split
is the decision, not an inconsistency:

* The **observer** ServiceAccount refusal returns `nil`, so the same pass still writes the
  observer Deployment. The pod names the ServiceAccount by string either way, so the refusal
  never changes which identity it runs under — and that identity grants it nothing:
  `AutomountServiceAccountToken: ptr.To(false)` in the Deployment pod template (enforced on
  the update path too, through `observerIdentityChanged`), no RoleBinding names it, and
  `internal/observer` imports no Kubernetes client at all. Refusing the Deployment as well
  would convert a name collision into an outage of the diagnostic component and buy nothing.
* The **sidecar** refusal fails its step. Without the binding the sidecar cannot patch, so no
  pod ever carries `instanceRole` — the label is set only by
  [`internal/sidecar/labeler.go`](../../internal/sidecar/labeler.go) and
  [`internal/sidecar/drain.go`](../../internal/sidecar/drain.go), never by the pod template,
  whose `common.BaseLabels` excludes it by construction — and the `-rw` Service therefore
  selects no pod at all. That cluster is not writable, and the CR must not report `OK`.

This is deliberately the opposite of the PodDisruptionBudget choice
([ADR 0004](0004-opt-in-poddisruptionbudgets.md) D10), where a budget that is not written
silently protects nothing and the pass continues. The distinguishing question is not
"was something not written" but "can the CR still do its job".

**D3 — A refusal reaches the RoleBinding through the ServiceAccount and the Role, not through
the RoleBinding's own provenance.** `reconcileSidecarRBAC` takes a `(bool, error)` verdict
from `reconcileSidecarServiceAccount` and from `reconcileSidecarRole` and writes the binding
only when both are owned.

Both directions are load-bearing, and neither is covered by checking the binding itself. In
the ordinary collision only the *ServiceAccount* pre-exists; the binding does not, so the
operator would create it fresh and hand the grant away with no foreign binding anywhere in
sight. The Role is the mirror image: `RoleRef` names the Role, so binding our ServiceAccount
to a Role the operator did not write grants the sidecar whatever that Role happens to carry.

**D4 — Operator-owned labels are assigned; annotations are merged.** The two ServiceAccount
reconcilers now write `current.Labels = desired.Labels` and then
`builder.ApplyOperatorVersion(current, …)`, which is what the other twelve reconcilers in the
package already do. The change-detection compares labels and the version annotation, not the
whole annotation map, so an annotation the operator does not own can neither be erased nor
re-trigger an update on every pass.

This is independent of D1 and survives it: an ownership guard cannot help an object the
operator legitimately owns and a second writer annotates. Assigning labels is left alone —
that is the repo-wide convention, recorded with its tradeoff in
[`internal/controller/pdb.go`](../../internal/controller/pdb.go), and changing it is a
convention-wide change that is out of scope here.

**D5 — A refusal is reported with its own ReconcileBlocked reason.** `vkov1.ReasonForeignObject`
is distinct from `ReasonWriteFailed` because nothing failed, and it **outranks**
`ReasonAdmissionWebhookDenied` when a pass produced both: an admission gate reopens on its own
and the next pass clears the condition, while a name collision clears only when a human acts.
Reporting the transient cause would hide the one that needs an operator. Each object family
also emits its own Warning Event reason (`ObserverServiceAccountNotOwned`,
`SidecarServiceAccountNotOwned`, `SidecarRoleNotOwned`, `SidecarRoleBindingNotOwned`) so the
colliding name is findable without reading operator logs
([ADR 0002](0002-surface-a-blocked-reconcile-on-the-cr.md)).

The Warning is **not gated**, unlike `warnPodDisruptionBudgetNotOwned`. That gate exists
because a hand-written PodDisruptionBudget under the StatefulSet name was the documented
workaround before the feature existed, so the warning had a large legitimate population to
stay quiet for ([ADR 0004](0004-opt-in-poddisruptionbudgets.md) D11). Nothing hand-writes a
`<cr-name>-sidecar` ServiceAccount as a workaround, so the population here is actual
collisions and every one of them is worth reporting; the recorder aggregates the repeats into
one Event series.

**D6 — Every refusal is re-checked without human intervention.** Both kinds recover on their
own once the colliding object is removed, and neither needs a CR edit or an operator restart:

* A refusal that **failed** its step is re-entered by the work queue, whose per-item
  exponential backoff is capped at `reconcileRetryMaxDelay` = 30 s
  ([`internal/controller/ratelimiter.go`](../../internal/controller/ratelimiter.go)). The pass
  is not a waiting loop: `reconcileResources` aggregates rather than aborts
  ([ADR 0001](0001-continue-reconciling-past-a-rejected-write.md)), so everything except the
  refused step keeps being reconciled meanwhile.
* A refusal that **did not** fail its step calls `requestRecheck(ctx, foreignObjectRecheckInterval)`.
  The request rides on a per-pass `passState` reached through the context — the same shape as
  the blocked-pass marker, and for the same reason: it stays per-pass and per-CR at
  `MaxConcurrentReconciles > 1`
  ([ADR 0019](0019-reconcile-concurrency-and-the-cost-of-a-stuck-pass.md) D3). `Reconcile`
  folds it into the returned `ctrl.Result` with `applyRecheck`, which never lengthens a
  requeue the pass already asked for, and only on the error-free path, because
  controller-runtime drives the retry from the error otherwise.

Both intervals are 30 s so the two kinds of refusal recover at the same cadence.

**D7 — The other managed kinds keep their current behaviour until each is decided on its own.**
Extending D1 to the data StatefulSet is not a mechanical repeat: it is the most critical object
in the system, and a guard there refuses every StatefulSet that lost its controller
ownerReference — a backup restore, a migration, an older operator release — which turns an
upgrade into an outage. That needs its own upgrade analysis, and bundling it here would have
made one change decide four fail directions at once. NA62 needs a *different* fix shape
("do not stamp an ownerReference onto an object you did not verify"), not this one.

The upgrade question was checked for the objects this ADR does bind and came out clean:
`reconcileSidecarServiceAccount` carried `controllerutil.SetControllerReference` in its very
first commit (`73f6efe`), so every sidecar ServiceAccount the operator ever created has the
reference and no existing cluster is refused by the new guard.

## Consequences

* **A name collision is now a hard, visible stop for the sidecar.** A CR whose name aims at an
  existing `<cr-name>-sidecar` object never becomes writable, reports `Error` with a message
  naming the object, and stays that way until a human deletes the object or renames the CR.
  Before this change it came up and silently granted `pods: patch` to a stranger, which is
  worse — but it did come up, and this is a behaviour change for anyone who was relying on
  that.
* **Two ServiceAccount reconcilers no longer converge metadata fully.** An annotation the
  operator once set and later drops from `desired` now survives on the object. For these two
  the operator sets exactly one annotation, so the practical surface is nil.
* **Three functions changed signature** to `(bool, error)`, matching the PDB precedent.
  Callers must not ignore the verdict; the compiler enforces that at the two call sites.
* **The refusal costs one Event series and one requeue per affected CR per 30 s.** For an
  unaffected CR nothing changes: no extra read, no extra write, no requeue.
* **`Phase = Error` for the sidecar case is written by the existing blocked-pass authority**,
  so it does not flap against the phase computed from the data plane
  ([ADR 0002](0002-surface-a-blocked-reconcile-on-the-cr.md)). Nothing new writes the phase.

## Alternatives Considered

**Guard the observer ServiceAccount only, as the finding was originally filed.** Rejected: it
closes the cosmetic half of the newest bug while leaving the only place where a collision
transfers a real capability untouched, and it publishes a rule that reads as project-wide but
is implemented in one function.

**Guard both ServiceAccounts but not the Role and RoleBinding.** Rejected as the worst
combination: it pays for three guards and still fails open, because the binding names the
ServiceAccount rather than referencing the object, and in the ordinary collision the binding
does not exist yet and is created fresh.

**Refuse the sidecar ServiceAccount, the Role, the RoleBinding *and* the data StatefulSet.**
Rejected here, not on the merits — see D7. It is the more consistent end state and it is what
NA61 will decide.

**Refuse the observer Deployment as well, matching the PodDisruptionBudget precedent.**
Rejected: the pod names the ServiceAccount by string with or without the guard, so refusing
the Deployment does not change which identity it runs under; it only stops the observer. The
capability argument that makes the observer safe is verified, and `status.observerReady` has
no print column, so the degradation would be invisible in `kubectl get valkey`.

**Merge instead of refuse — keep adopting, but stop erasing metadata.** Rejected as the whole
answer: it leaves the operator writing onto objects it cannot prove it owns, which contradicts
[ADR 0006](0006-delete-only-what-the-operator-owns.md) D2 and D11 in spirit and would set a
second, competing precedent beside the PDB one. Taken as *part* of the answer, in D4.

**Return `nil` from the sidecar refusal and carry it on a new status condition instead.**
Rejected after tracing the phase machinery. `updateStatus` runs after `reconcileResources`
in the same pass and recomputes `status.phase` from ready replicas and connectivity — the pods
are up and reachable in this scenario, so it would write `OK` over anything the refusal had
set. Making it durable means teaching `updateStatus` to consult the new condition on both its
standalone and HA branches, i.e. building a second phase authority beside the existing one.
Returning the error reuses the machinery that already solves exactly this, including the
anti-flapping suppression.

**Amend ADR 0006 instead of writing this one.** Its D2 and D11 are already the rules a refusal
needs, and its residual list is where the RoleBinding item lived. Rejected on two counts: the
title would have to widen from "delete only" to "touch only", which means renaming the file
and rewriting 20 Markdown references across nine files plus eight references in Go comments
that point at *delete* sites; and D3's rule is a shape ADR 0006 does not have — it is about a
reference *inside* the object rather than about the object being written.

**Rename the generated objects so they cannot collide** (a CR-UID suffix or a hash). Rejected:
the names are user-facing in the README naming tables, and a rename means every existing
cluster gets a new ServiceAccount, a new binding and a pod restart —
[ADR 0005](0005-upgrade-neutral-defaults-and-anti-affinity.md) rules that out for a change
that alters nothing a user asked for.

## Residual risks

* **Nothing here was reproduced on a real cluster.** The capability path, the erased
  annotations and the recovery were all read from the code and then exercised against envtest.
  No collision was aimed at a real workload, and no foreign identity was observed patching a
  pod.
* **The other managed kinds remain unguarded on the write path** — StatefulSet, Service,
  ConfigMap, NetworkPolicy, Deployment, ServiceMonitor and Certificate. NA61 and NA62 name the
  two with the sharpest consequences; the rest are unticketed. D7 says why, and this is the
  largest open item of this ADR.
* **The recovery test does not isolate its wake-up source.**
  `TestForeignSidecarServiceAccount_Integration` proves the operator finishes provisioning
  after the collision is removed, with no CR edit and no restart. It does not prove the work
  queue requeue is what woke it: the CR owns a StatefulSet, and an `Owns()` event from that
  object could in principle deliver the same pass. Separating the two means suppressing the
  other watches, which the suite has no seam for.
* **The `imagePullSecrets` channel is untouched.** The ServiceAccount admission plugin injects
  the ServiceAccount's `imagePullSecrets` into the pod regardless of
  `automountServiceAccountToken`, and no builder in this repo sets that field — so under a
  foreign observer ServiceAccount those secrets are the pod's only source. That is upstream
  behaviour, not verifiable from this repository, and the refusal neither opens nor closes it.
* **A pod still carries the ServiceAccount *name* as its identity** for a service mesh, SPIFFE
  or an admission policy, and that name is CR-derived whether or not the operator owns the
  object. The guard does not change it.
* **`errors.Is` over a joined pass error decides the ReconcileBlocked reason.** If a future
  step wraps a refusal in a way that breaks unwrapping, the reason silently degrades to
  `WriteFailed`. The condition still fires; only its reason would be wrong.
* **The `passState` mutex guards nothing today**, because `runReconcileSteps` is a sequential
  loop. It is there so that a future step which fans out does not race silently, which
  [ADR 0019](0019-reconcile-concurrency-and-the-cost-of-a-stuck-pass.md) D3 makes a standing
  constraint rather than a one-time audit.

## References

* [`internal/controller/foreign_object.go`](../../internal/controller/foreign_object.go) — the
  sentinel error, the Event reasons, the per-pass recheck state and the warn helper.
* [`internal/controller/valkey_controller.go`](../../internal/controller/valkey_controller.go) —
  `reconcileObserverServiceAccount`, `reconcileSidecarRBAC`, `reconcileSidecarServiceAccount`,
  `reconcileSidecarRole`, `reconcileSidecarRoleBinding`, and the `applyRecheck` fold in
  `Reconcile`.
* [`internal/controller/reconcile_blocked.go`](../../internal/controller/reconcile_blocked.go) —
  `reconcileBlockedReason` and its precedence.
* [`internal/builder/rbac.go`](../../internal/builder/rbac.go) — the name-based subject and
  `RoleRef` that make D3 necessary.
* [`internal/controller/foreign_object_test.go`](../../internal/controller/foreign_object_test.go)
  and [`test/integration/foreign_object_test.go`](../../test/integration/foreign_object_test.go).
* [ADR 0006](0006-delete-only-what-the-operator-owns.md) — the deletion half, and the residual
  this closes.
* [ADR 0004](0004-opt-in-poddisruptionbudgets.md) D10, D11 — the precedent D2 and D5 depart from.
* [ADR 0012](0012-the-sidecar-records-its-drain-promotion-on-the-pod.md) — what the sidecar
  grant is for, and the residual that named this gap.
* [ADR 0019](0019-reconcile-concurrency-and-the-cost-of-a-stuck-pass.md) D3 — why per-pass
  state rides on the context.
