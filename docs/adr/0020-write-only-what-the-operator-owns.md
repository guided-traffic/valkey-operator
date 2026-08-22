# ADR 0020: Write Only What the Operator Can Prove It Owns, and Grant Only to a Subject It Owns

## Status

Accepted. Date: 2026-08-21.

**Amended 2026-08-22 (NA61):** the guard also binds `reconcileStatefulSet`,
`reconcileSentinelStatefulSet` and `reconcileObserverDeployment`, `cleanupObserverDeployment`
gained the ADR 0006 delete guard, and D8 makes every other StatefulSet consumer treat a
foreign object as absent.

**Amended 2026-08-22 (NA62):** the guard binds **every** managed kind. The remaining five —
Service, ConfigMap, NetworkPolicy, ServiceMonitor, Certificate — are guarded, the two
`unstructured` reconcilers no longer stamp this CR's ownerReference onto an object they did
not verify, `replicaConfigMaster` treats a foreign ConfigMap as absent under D8, and the last
three name-only deletes in the ADR 0006 residual list carry the provenance proof and the UID
precondition. **D7 is superseded in full** and marked in place.

**Amended 2026-08-22 (NA63):** the rule reaches **pods**, whose controller is the
StatefulSet rather than the CR, through the two-hop proof of the new D9. Three doors are
closed: the sidecar Role no longer grants `patch` on a pod this cluster's StatefulSet did not
create, `clearDrainStamps` no longer patches one, and the rolling update no longer reads,
counts or **deletes** one — its six pod deletes now carry the ADR 0006 UID precondition.
`listMasterLabeledPods`, `replicaConfigMaster`, `recreatedAfter`, `checkAndRecoverNoMaster`
and `sentinelRolloutComplete` all treat an unproven pod as absent.

**Note 2026-08-22 (restore):** the residual-risk list gains the backup-restore analysis — a
namespace restore recreates every child with a stale controller-reference UID, the guards
refuse it by design, and adoption gated on Velero's restore labels was considered and
rejected. No decision changes; the supported restore path is documented in
[`SECURITY_ARCHITECTURE.md`](../../SECURITY_ARCHITECTURE.md) section 7.

Implemented on branch `feat/support-pdb`, in the same change as this ADR. Fourteen reconcile
paths carry the guard — `reconcileObserverServiceAccount`, `reconcileSidecarServiceAccount`,
`reconcileSidecarRole`, `reconcileSidecarRoleBinding`, `reconcileStatefulSet`,
`reconcileSentinelStatefulSet`, `reconcileObserverDeployment`, `reconcileService`,
`reconcileConfigMap`, `reconcileReplicaConfigMap`, `reconcileSentinelConfigMap`,
`reconcileNetworkPolicy`, `reconcileServiceMonitor` and `reconcileCertificate`, all in
[`internal/controller/valkey_controller.go`](../../internal/controller/valkey_controller.go) —
with the shared refusal and guarded-delete machinery in
[`internal/controller/foreign_object.go`](../../internal/controller/foreign_object.go).

This ADR closes every open residual of
[ADR 0006](0006-delete-only-what-the-operator-owns.md) that named an unguarded delete: the
delete-and-recreate in `reconcileSidecarRoleBinding`, the observer Deployment half of
`cleanupObserverDeployment` (2026-08-22, NA61), and `cleanupMetricsService`,
`cleanupServiceMonitor` and the NetworkPolicy half of `cleanupObserverDeployment`
(2026-08-22, NA62). All of them now prove ownership with `IsControlledBy` and send
`client.Preconditions{UID: …}`.

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

Five concrete failures made that gap worth closing. All five were read from the code on this
branch; none is a report of an object observed being damaged on a cluster.

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

* **(2026-08-22, NA62) The two `unstructured` reconcilers handed a foreign object to the
  garbage collector.** `reconcileServiceMonitor` and `reconcileCertificate` built an
  ownerReference with `Controller: true` and `BlockOwnerDeletion: true` and wrote it onto
  `current` — `current.SetOwnerReferences(desired.GetOwnerReferences())` — without ever
  calling `IsControlledBy`. Deleting the CR then garbage-collects an object the operator
  never created; for a Certificate that ends somebody else's issuance and renewal, and the
  Secret it manages goes with it. This is the harm
  [ADR 0006](0006-delete-only-what-the-operator-owns.md) D1 forbids, arriving through a door
  D1 does not watch, because the operator issues no `Delete` at all.

  The stamp is the spectacular half, not the whole. The same branch writes
  `current.Object["spec"] = desired.Object["spec"]`, which for a Certificate replaces
  `issuerRef`, `dnsNames` and `secretName`
  ([`internal/builder/certificate.go`](../../internal/builder/certificate.go) sets
  `secretName: <cr>-tls`) — after which cert-manager maintains this cluster's Secret and
  abandons theirs, without waiting for any CR deletion. The four typed reconcilers do **not**
  share the ownerReference half: they call `SetControllerReference` on `desired` only and
  never touch `current.OwnerReferences`. They still overwrite a foreign object's
  `spec.selector`, `Data` or policy rules, which is why they are bound here too.

* **(2026-08-22, NA63) Pods were reachable through three separate doors, and the one that
  was filed is the most expensive to use.** The filing named the network commands —
  `checkAndRecoverNoMaster` probing `<cr>-0..N-1` and `checkSteadyStateSplitBrain` demoting
  label-selected pods with `REPLICAOF`. Those go through `podAddress`
  ([`internal/health/checker.go`](../../internal/health/checker.go)), so reaching a foreign
  pod needs this cluster's label set, a per-pod record under the headless Service **and** the
  CR's password. The two doors nobody had filed need only the label set:

  * `clearDrainStamps`
    ([`internal/controller/steady_state_master.go`](../../internal/controller/steady_state_master.go))
    listed by selector labels and **patched** every match — no network, no password, no DNS.
  * `listDataPodNames` fed `SidecarRolePodNames`, whose result is the `resourceNames` of the
    sidecar Role ([`internal/builder/rbac.go`](../../internal/builder/rbac.go)). A pod
    carrying the labels therefore entered the grant, and this cluster's sidecar token got
    `patch` on somebody else's pod. That is D3 mirrored: there the grant followed the name of
    the *subject*, here it followed the name of the *object*.

  And the destructive one: the rolling update reads pods by generated name and deletes them.
  The NA61 StatefulSet guard does not cover it, because it proves the wrong object — a
  StatefulSet can be provably ours while a pod under `<cr>-N` was created by somebody else,
  and a foreign pod differs from our persisted template by construction, so the very next
  step classifies it as outdated and schedules it for deletion.

## Decision

**D1 — No write onto a generated name the operator cannot prove it owns.** Provenance is
`metav1.IsControlledBy(obj, v)`, exactly as [ADR 0006](0006-delete-only-what-the-operator-owns.md)
D2 defines it for deletes; a label is not a proof. ~~The rule binds the seven paths listed
under Status. It does **not** yet bind every managed kind — see D7.~~ *(Superseded
2026-08-22, NA62.)* **The rule binds every managed kind**, on the fourteen paths listed under
Status. There is no managed object family left that the operator writes on the strength of a
name alone.

Two corollaries the NA62 amendment adds, both of which the two `unstructured` reconcilers
violated:

* **An ownerReference is a write like any other, and the most consequential one.** The
  operator never stamps `Controller: true` onto an object it did not verify. Everything else
  a refused write leaves undone is recoverable by a human; a garbage-collected Certificate
  and its Secret are not.
* **The refusal is checked once, before the change detection.** Both `unstructured`
  reconcilers only wrote when they saw drift, so a foreign object identical to the desired
  one was silently left alone and one that differed was taken over — the guard must not
  inherit that accident.

The strictness was re-decided for the StatefulSets (2026-08-22) and held: no second proof
channel.

An object that *lost* its controller reference — a CR deleted with
`--cascade=orphan` and recreated, a backup restore that changed the CR UID, a hand edit —
is refused like any other foreign object, visibly, with a downtime-free recovery
(`kubectl delete sts <name> --cascade=orphan` keeps the pods; the operator recreates the
StatefulSet and the statefulset-controller re-adopts the label-matching orphans — upstream
behaviour, asserted from the API contract). An operator *upgrade* is not such a loss: the
CR object and its UID survive the upgrade untouched, and every release ever built stamped
the reference on create — `reconcileStatefulSet` and `reconcileConfigMap` since `b0081d9`,
the Sentinel StatefulSet and `reconcileReplicaConfigMap` since `88b721b`, the observer
Deployment since `c6f97e2`, `reconcileService` since `b0081d9`, `reconcileNetworkPolicy`
since `6aa85f1`, `reconcileCertificate` since `aa259fc` and `reconcileServiceMonitor` since
`28b6830`, the last two with `Controller: true` in that very first commit, which is what
`IsControlledBy` requires — so no operator-created object is refused by the new guards.

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

The 2026-08-22 amendment sorts the three new paths by the same test. The **data
StatefulSet** refusal fails its step — without it there is no data plane at all. The
**Sentinel StatefulSet** refusal fails its step — an HA cluster without its Sentinels has
no failover authority. The **observer Deployment** refusal returns `nil` with the D6
recheck, exactly like the observer ServiceAccount: the observer is diagnostic, the CR does
its job without it, and `status.observerReady` records the degradation.

The NA62 amendment sorts the last five, and two of them are decided by their *caller* rather
than by their reconciler, because one function serves several object families:

| Path | Direction | Why |
|---|---|---|
| `reconcileCertificate` | fails the step | The data StatefulSet mounts the TLS Secret by name, so a foreign Certificate under `<cr>-tls` means either no Secret (pods never start) or one with foreign SANs (clients fail the verify). |
| `reconcileConfigMap`, `reconcileReplicaConfigMap`, `reconcileSentinelConfigMap` | fail the step | The pods mount these by name. The refusal cannot undo that a stranger's file is what Valkey started with; it stops the operator from also overwriting their data. |
| `reconcileService` | fails the step | `-rw` is how clients reach the master. A cluster whose write endpoint the operator will not maintain is not usable. |
| `reconcileService` **for `<cr>-metrics`** | returns `nil` + recheck | Decided in `reconcileMetricsService`, which downgrades `errForeignObject`. Scraping is observability; the Event is still emitted. |
| `reconcileServiceMonitor` | returns `nil` + recheck | Same reason, same surface. |
| `reconcileNetworkPolicy` | fails the step | See below — the one place D2 is read wider than the data plane. |
| `reconcileNetworkPolicy` **for the observer policy** | returns `nil` + recheck | Decided in `reconcileNetworkPolicies`, matching every other observer path. |

The NetworkPolicy direction is the one that does not follow from "can the CR still serve
reads and writes": it can. It fails anyway, because a CR reporting `OK` while the policy it
names belongs to another object is a **security statement that is not true**, and
`spec.networkPolicy.enabled` is the user asking for that statement. The D2 question is
therefore "can the CR do the job it was asked to do", and isolation is part of the job when
it was requested. The observer's own policy is exempt for the same reason its Deployment is:
the component mounts no token, no RoleBinding names it, and the CR does its job without it.

Deciding two of these in the caller rather than in the reconciler is deliberate.
`reconcileService` serves six callers and `reconcileNetworkPolicy` three; putting the fail
direction inside them would make it depend on which builder produced `desired`, which is
exactly the kind of implicit rule this ADR exists to remove. The caller that knows why it is
writing is the one that decides what a refusal costs.

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
also emits its own Warning Event reason — `ObserverServiceAccountNotOwned`,
`SidecarServiceAccountNotOwned`, `SidecarRoleNotOwned`, `SidecarRoleBindingNotOwned`,
`StatefulSetNotOwned`, `SentinelStatefulSetNotOwned`, `ObserverDeploymentNotOwned` and, since
NA62, `ServiceNotOwned`, `ConfigMapNotOwned`, `NetworkPolicyNotOwned`,
`ServiceMonitorNotOwned` and `CertificateNotOwned` — so the colliding name is findable
without reading operator logs ([ADR 0002](0002-surface-a-blocked-reconcile-on-the-cr.md)).
One reason per *kind*, not per call site: the three ConfigMap reconcilers and the six Service
callers share theirs, and the colliding name is in the message.

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

**D7 — ~~The other managed kinds keep their current behaviour until each is decided on its
own.~~** *(**Superseded in full on 2026-08-22 (NA62).** It was first superseded for the data
and Sentinel StatefulSets and the observer Deployment earlier the same day (NA61); the
remaining five kinds — Service, ConfigMap, NetworkPolicy, ServiceMonitor, Certificate — are
now bound by D1 as well, so nothing is left for this rule to govern. It is kept here because
its reasoning is what shaped the order the work was done in, and because the sentence it ends
on turned out to be wrong.)* Extending D1 to the data StatefulSet is
not a mechanical repeat: it is the most critical object in the system, and a guard there
refuses every StatefulSet that lost its controller ownerReference — a backup restore, a
migration, an older operator release — which turns an upgrade into an outage. That needs
its own upgrade analysis, and bundling it here would have made one change decide four fail
directions at once. NA62 needs a *different* fix shape ("do not stamp an ownerReference
onto an object you did not verify"), not this one.

The last sentence above did not survive contact with the code. NA62 was filed as needing a
different fix shape, and the ownerReference stamp *is* a distinct harm — but it is not the
only one on those two paths. The same branch overwrites the whole `spec`, which for a
Certificate repoints `secretName` and `issuerRef` and costs the other party their Secret
before any CR is deleted. A fix that only dropped the stamp would have left that open, so
the shape is D1 after all, and removing the stamp is what D1 refusing the write already does.

The upgrade question was checked for the objects this ADR does bind and came out clean:
`reconcileSidecarServiceAccount` carried `controllerutil.SetControllerReference` in its very
first commit (`73f6efe`), so every sidecar ServiceAccount the operator ever created has the
reference and no existing cluster is refused by the new guard. The 2026-08-22 amendment
repeated that check for the three newly bound paths — first commits `b0081d9`, `88b721b`
and `c6f97e2`, each already stamping the reference — with the same result; the analysis
D7 asked for is in D1.

**D8 — A StatefulSet the operator does not own is treated as absent by every consumer, and
the reconciler is the one reporter.** Refusing the Update alone would not have stopped the
operator from *acting on* the foreign object: the nudge patches an annotation onto it, the
rolling update deletes pods against its persisted template, and the status counts its
replicas as the CR's own. Each of those is a write to, or an action derived from, an object
that is not ours. The guarded consumers, each taking the branch its NotFound case already
has: `nudgeStatefulSet` ([`internal/controller/nudge.go`](../../internal/controller/nudge.go)),
`checkAndHandleRollingUpdate`, `handlePostFailover` and `checkAndHandleSentinelRollingUpdate`
([`internal/controller/rolling_update.go`](../../internal/controller/rolling_update.go)),
`sentinelRolloutComplete`, `updateStatus` and `updateHAStatus`
([`internal/controller/valkey_controller.go`](../../internal/controller/valkey_controller.go)).
Only `reconcileStatefulSet` and `reconcileSentinelStatefulSet` warn and emit the Event, so a
blocked CR produces one Event series per pass, not one per consumer.

**(Amended 2026-08-22, NA62)** The rule is about *any* managed object a second path reads or
writes, not only StatefulSets, and the second such consumer is the replica ConfigMap.
`replicaConfigMaster`
([`internal/controller/steady_state_master.go`](../../internal/controller/steady_state_master.go))
reads the **live** object — deliberately, because the CR annotation and the published file
diverge exactly where it matters — and returns its `replicaof` target as the published
master. That value feeds `checkSteadyStateSplitBrain`, whose resolver issues `REPLICAOF`
([ADR 0011](0011-evidence-based-steady-state-split-brain-resolution.md)). Without the guard a
stranger's ConfigMap under `<cr>-replica-config` would decide who this cluster demotes. It
now reports the published master as unknown, which is the answer the callers already handle
(`couldNotHaveSelfElected` compares an empty name unequal to everything, and the paths that
must fail closed on a destructive action rely on that). `reconcileReplicaConfigMap` stays the
one reporter.

A sweep of the remaining kinds found no third consumer: nothing reads back a Service, a
NetworkPolicy, a ServiceMonitor or a Certificate to derive a decision from it. The one
Certificate reader, `deleteLegacySentinelCertificate`, was already ownership-checked.

**D9 — A pod is proven two-hop, and the proof is the StatefulSet.** *(2026-08-22, NA63.)* A
pod is the only managed object whose controller is not the CR: the statefulset-controller
creates it and stamps itself. So the chain is `pod -> StatefulSet -> CR`, `podIsOurs(pod, sts)`
against a StatefulSet D1 has already proven, and every link compares a UID rather than a name
([`internal/controller/foreign_object.go`](../../internal/controller/foreign_object.go)).

The fail direction splits by door, on the same D2 question:

| Path | Direction |
|---|---|
| `listDataPodNames` (the sidecar grant), `listMasterLabeledPods`, `clearDrainStamps` | filter the pod out |
| `checkAndRecoverNoMaster`, `recreatedAfter`, `sentinelRolloutComplete`, `replicaConfigMaster` | treat as absent, which each already reads as "unknown" and fails closed on |
| the rolling update: `checkAndHandleRollingUpdate`, `collectPodStates`, `handleStandaloneRollingUpdate`, `handlePostManualFailover`, `checkAndHandleSentinelRollingUpdate` | refuse and fail the step |

The rolling update is the one that fails rather than filters, because treat-as-absent maps
onto its existing `exists=false, needsUpdate=true` branch and would leave it waiting for a pod
that can never appear, reporting "waiting for pod-N" while the truth is "a foreign object
holds that name". A collision clears only when a human acts, which is the same argument D5
makes for `ForeignObject` outranking a transient cause. Failing keeps the rolling-update state
annotation in place, so its bounded waits stay armed
([ADR 0010](0010-every-rolling-update-wait-is-bounded.md)).

**One reporter, and it is not the rolling update.** `reconcileSidecarRole` runs on every pass
and lists the data pods anyway, so it emits the `PodNotOwned` Warning for the whole family at
no extra read; every filtering path stays quiet. The rolling update emits no Event of its own —
its refusal already reaches the CR as `ReconcileBlocked/ForeignObject` with the pod name in the
message — so a collision produces one Event series per pass, the same rule D8 sets.

**Two things D9 deliberately does not do.** The `resourceNames` of the sidecar Role keep the
*desired* half, `<cr>-0..N-1` derived from `spec.replicas` before those pods exist; that half
is load-bearing for scale-up (D3, and the comment on `SidecarRolePodNames`), and a name under
which no pod of ours can ever exist is a name our own StatefulSet is blocked on anyway. And
the guard does not survive upstream adoption: the statefulset-controller adopts orphan pods
matching its selector and stamps its own controller reference, so a pod built to carry this
cluster's label set and left without a controller becomes genuinely ours by Kubernetes' own
rules. D9 closes collisions and strays, not a deliberate mimic. That adoption behaviour is
read from the API contract and reproduced nowhere in this repo.

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
* **(2026-08-22) A name collision on the StatefulSet names is now a hard, visible stop for
  the whole CR.** Before the amendment the Update was near-certainly rejected by the
  apiserver anyway — `spec.selector` is immutable and not among the written fields — but
  the operator kept nudging the foreign object and counting its pods, and in the one case
  where the selectors *did* match, the Update succeeded and replaced the foreign workload's
  pod template. Both are gone: the refusal is deliberate, reported with the colliding name,
  and recovery needs a human (delete or rename one of the two).
* **(2026-08-22) A StatefulSet that lost its controller reference out-of-band is refused
  after the operator upgrade** — orphan-delete plus CR recreate, a restore that changed the
  CR UID, a hand edit. That silent adoption was load-bearing for nobody the repo knows of,
  and the recovery is downtime-free (D1). An ordinary upgrade is unaffected; the
  from-previous-release upgrade e2e creates its fleet with the real prior image and the
  new operator must keep updating it.
* **(2026-08-22, NA62) The guard protects only forward.** An object a previous release
  already stamped now *passes* `IsControlledBy`, and the CR delete will still garbage-collect
  it. Nothing in the tree can tell such an object from a genuine child: the same Update
  branch that stamped the reference also wrote `current.SetLabels(desired.GetLabels())`,
  replacing the label map wholesale, and `ApplyOperatorVersion` stamped the annotation — so
  labels, controller reference and version annotation are all identical to a real one. This
  is documented and left, not detected; see Residual risks and the hardening checklist in
  [`SECURITY_ARCHITECTURE.md`](../../SECURITY_ARCHITECTURE.md).
* **(2026-08-22, NA62) Turning a feature off can no longer delete somebody else's object.**
  `spec.metrics.enabled`, `spec.metrics.serviceMonitor.enabled` and `spec.observer.enabled`
  each drove a name-only `Delete`. The cheapest of them needed one boolean in a CR the author
  already controls. All three now prove ownership and send the UID precondition, through the
  shared `deleteIfOwned`.
* **(2026-08-22, NA62) Test fixtures had to become honest.** Twenty-one existing tests
  staged objects built straight from `internal/builder` — which never sets the ownerReference,
  because the reconciler does — and were implicitly asserting behaviour that only held while
  nothing checked ownership. They now stamp it. Two of them asserted the *opposite* of the
  new rule ("a missing owner reference must be restored") and were rewritten: an object
  without a controller reference is foreign, and restoring one is precisely what D1 forbids.

* **(2026-08-22, NA63) A pod that exists only in the checker's imagination is no longer an
  answer.** `checkAndRecoverNoMaster` used to accept a probe reply for a pod name whether or
  not the API server had such a pod; now it reads the object first. That is stricter than the
  old behaviour by design — a no-master verdict is what promotes pod-0 with `REPLICAOF NO ONE`
  — and it surfaced as a unit test that had been asserting the recovery with no pods staged
  at all.
* **(2026-08-22, NA63) The rolling update stops on a colliding pod instead of deleting it.**
  A cluster whose `<cr>-N` is held by a foreign pod now reports `ReconcileBlocked/ForeignObject`
  and phase `Error` rather than quietly rolling. It could not have completed either way — the
  statefulset-controller cannot create its own pod under a taken name — but the failure is now
  the reported one rather than a rollout that never finishes.
* **(2026-08-22, NA63) Every pod fixture in the unit tests had to declare its parent.** The
  suite staged pods built straight from literals, and the fake client assigns no UID, so a
  StatefulSet the reconciler created under test had an empty one. Both halves are fixed
  centrally: fixtures point at a deterministic `<sts-name>-sts-uid`, and a Create interceptor
  stamps the same string on a StatefulSet the fake client creates. Without the second half the
  tests would have measured the fixture rather than the guard — the trap `newTestValkey`
  already documents for the CR's own UID.

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
Rejected in the original decision, not on the merits — see D7. The 2026-08-22 amendment is
exactly this end state.

**Auto-re-own a reference-less StatefulSet on structural evidence** (full operator label set
plus a selector equal to the generated one) — considered for the amendment, to let
orphan-recreate and restore heal without a human. Rejected: it opens a second proof channel
that D1, [ADR 0006](0006-delete-only-what-the-operator-owns.md) D2 and the PDB precedent all
explicitly closed ("a label is not a proof"), and a deliberately crafted mimic object would
be adopted with whatever the operator does not write — `volumeClaimTemplates` above all,
which are immutable and decide where the data lands. The legitimate population it would
serve is rare and has a visible, downtime-free manual recovery (D1).

**Treat a foreign StatefulSet as absent without refusing the write** — i.e. only D8, no D1.
Rejected: the pass would end error-free with no StatefulSet written, reporting Provisioning
forever with no condition naming the cause. The refusal is what makes the collision visible
and drives the retry.

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

**(NA62) Drop the ownerReference stamp and keep converging the spec**, the fix shape the
finding was filed with and the one D7 predicted. Rejected once the second half of the harm
was read: the spec write rewrites a foreign Certificate's `secretName` and `issuerRef`, so
cert-manager maintains this cluster's Secret and abandons theirs — no CR deletion needed. A
stamp-only fix leaves that open, costs a guard anyway, and publishes a second rule competing
with D1 for the same question.

**(NA62) Guard only the pair NA62 names, and file the typed three separately.** Rejected: the
argument for splitting was D7's, and D7's premise (a different fix shape) turned out to be
false. With the shape identical, the only per-kind work left is the fail direction, and
leaving three kinds unguarded would mean the tree carries three different answers to one
question. The typed three are not harmless either — a `spec.selector` is mutable, so a
foreign Service is taken over with no immutability backstop at all.

**(NA62) Detect objects a previous release already adopted** — by a missing operator label,
a missing version annotation, anything structural — and un-stamp or report them. Rejected as
impossible, not as undesirable: the stamping Update also replaced the label map and wrote the
version annotation, so no field distinguishes the two cases. A heuristic would have to guess,
and its false positive removes a genuine child's ownerReference, after which the next pass
refuses it and the CR goes down.

**(NA62) Give the metrics Service and the observer NetworkPolicy the same failing direction
as their siblings**, for one uniform rule per function. Rejected: it would put a CR into
`Error` because a name in the *monitoring* surface collided, which is the trade D2 already
refused for the observer. The cost is that two fail directions live in callers rather than in
the reconciler — stated in D2 so it reads as a decision and not as an inconsistency.

**(NA63) Prove a pod by its labels and its ordinal name** instead of by its parent — the pod
carries the full operator label set and its name matches `<cr>-N` for `N < spec.replicas`.
Rejected for the third time in this ADR, and for the same reason as the StatefulSet
auto-re-own: a label is not a proof, and here it would have been the only one. It also needs
no extra read, which is exactly what makes it tempting.

**(NA63) Prove a pod by an ownerReference naming a StatefulSet** without looking that
StatefulSet up. Rejected: the comparison would hang on the StatefulSet's *name*, which is what
the CR author chooses — ADR 0006 D2's mistake, rebuilt one level down. The test that pins this
stages a pod which is a perfectly valid child of a foreign StatefulSet under the generated
name.

**(NA63) Treat a foreign pod as absent inside the rolling update**, the way D8 treats a
foreign StatefulSet, reusing the `exists=false, needsUpdate=true` branch that its NotFound case
already has. Genuinely close, and it is the method D8 states. Rejected because the resulting
wait is honest about the wrong thing: the operator would report "waiting for pod-N to be
recreated" forever, while what happened is that a foreign object holds the name and no amount
of waiting fixes it.

**(NA63) Close only the two doors the finding named.** Rejected once the other two were read:
the network commands need labels, a DNS record and the password, while the `clearDrainStamps`
patch and the sidecar grant need only the label set — and the grant is the one that hands a
capability to a stranger rather than merely touching them.

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
* **(Closed 2026-08-22, NA62) The remaining managed kinds are still unguarded on the write
  path.** All five are bound by D1 now, and no managed object family is written on the
  strength of a name alone.
* **An object a previous release already adopted stays adopted, and cannot be found.** The
  guard is not retroactive, and no field separates a foreign object that was stamped from a
  genuine child — see the Consequences bullet for why. A cluster that ran an earlier release
  with a colliding ServiceMonitor or Certificate carries that stamp today, and deleting the
  CR will garbage-collect the object. The only remedy is to look before upgrading; it is on
  the hardening checklist in [`SECURITY_ARCHITECTURE.md`](../../SECURITY_ARCHITECTURE.md).
* **The garbage-collector behaviour was never reproduced.** That a `Controller: true` /
  `BlockOwnerDeletion: true` reference makes the CR deletion cascade to the referenced object
  is upstream behaviour asserted from the API contract. envtest starts no
  kube-controller-manager, so no tier in this repo runs a garbage collector, and no test
  here observes the deletion the finding is named after. What the tests do prove is the
  operator's half: the reference is never written onto an object it did not verify.
* **A namespace restore makes every child foreign, and the guards are meant to say so.**
  A backup tool that restores the CR together with the managed children (Velero restores
  objects with their backed-up `ownerReferences` but necessarily new UIDs) recreates every
  child with a controller reference to the *old* CR UID. Every guard then refuses — correctly,
  these objects are not controlled by the live CR — and the pass blocks with
  `ReconcileBlocked/ForeignObject` until the upstream garbage collector, which resolves owners
  by UID, removes the stale children and the operator rebuilds them. The convergence therefore
  rests on the same unreproduced garbage-collector contract as the bullet above. The supported
  path — restore only the CR, the auth Secret and the PVCs, and let the operator derive the
  rest — is documented in
  [`SECURITY_ARCHITECTURE.md`](../../SECURITY_ARCHITECTURE.md) section 7. **Adoption gated on
  Velero's `velero.io/backup-name` / `velero.io/restore-name` labels was considered and
  rejected**: a label is writable by anyone who can create the object, so honoring it would
  reopen the exact door D1 closed, with a Velero prefix instead of an instance label. The
  least-bad adoption evidence would be the stale controller reference itself (kind `Valkey`,
  same name and namespace, UID mismatch) — a shape no victim object legitimately carries —
  but it is still forgeable by the object's creator and buys only the removal of the
  delete-and-rebuild window, so it stays unbuilt until that window is shown to matter. No
  restore has been exercised against a real cluster from this repository.
* **The Secret door is out of scope and open by design.** The data StatefulSet mounts
  `ValkeyTLSSecretName(v)` by name, and with a user-provided Secret (`spec.tls.secretName`)
  naming any Secret in the namespace is the documented feature. Under cert-manager the name
  is derived (`<cr>-tls`), so a foreign Secret under it is mounted into this cluster's pods
  unverified. It is not a new door — a CR author can already name any Secret in the namespace
  through `spec.tls.secretName` and `spec.auth.secretName` — but it is not closed by this
  ADR either.
* **(Closed 2026-08-22, NA63) The pod door is untouched.** All three doors are guarded by D9,
  and the rolling update's pod deletes carry the UID precondition. What remains open is stated
  in D9 and repeated here: the `resourceNames` desired half, and upstream adoption of a
  label-matching orphan.
* ~~**The pod door is untouched (NA63).**~~ *(Superseded 2026-08-22 by D9; kept because its
  last two sentences are the bound that still applies to the pods D9 cannot prove either way,
  and because it names only two of the three doors that were actually there.)* The StatefulSet
  guards close every action derived
  *from the StatefulSet object*, but two steady-state paths derive pods from the CR alone:
  `checkAndRecoverNoMaster` probes `<cr>-0..N-1` by name and can promote pod-0, and
  `checkSteadyStateSplitBrain` acts on whatever pods carry the master label — both without
  verifying the pods' controller. The commands authenticate with the CR's credentials, so a
  foreign Valkey with its own password refuses them and the probe failure blocks the
  recovery path; a CR *without* `spec.auth` aimed at unauthenticated foreign pods has no
  such backstop. Filed as NA63 in `local_valkey_operator_admission_gap.md`.
* **The selector-immutability backstop was never reproduced.** The claim that a
  template-write onto a selector-mismatched StatefulSet is rejected by the apiserver is
  upstream behaviour read from the API contract. It stopped being load-bearing with this
  amendment — the guard refuses first — and stays unverified.
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
  sentinel error, the Event reasons, the per-pass recheck state, the warn helper and the
  shared `deleteIfOwned`.
* [`internal/controller/valkey_controller.go`](../../internal/controller/valkey_controller.go) —
  `reconcileObserverServiceAccount`, `reconcileSidecarRBAC`, `reconcileSidecarServiceAccount`,
  `reconcileSidecarRole`, `reconcileSidecarRoleBinding`, `reconcileStatefulSet`,
  `reconcileSentinelStatefulSet`, `reconcileObserverDeployment`, `reconcileService`,
  `reconcileConfigMap`, `reconcileReplicaConfigMap`, `reconcileSentinelConfigMap`,
  `reconcileNetworkPolicy`, `reconcileServiceMonitor`, `reconcileCertificate`,
  `cleanupObserverDeployment`, `cleanupMetricsService`, `cleanupServiceMonitor`, the two
  caller-side downgrades in `reconcileMetricsService` and `reconcileNetworkPolicies`, and the
  `applyRecheck` fold in `Reconcile`.
* [`internal/controller/nudge.go`](../../internal/controller/nudge.go),
  [`internal/controller/rolling_update.go`](../../internal/controller/rolling_update.go) and
  [`internal/controller/steady_state_master.go`](../../internal/controller/steady_state_master.go)
  (`replicaConfigMaster`) — the D8 treat-as-absent consumers.
* The D9 pod paths: `podIsOurs`, `ownedDataStatefulSet`, `podUnderNameIsOurs`,
  `filterOwnedPods` and `deleteOwnedPod` in
  [`internal/controller/foreign_object.go`](../../internal/controller/foreign_object.go);
  `listDataPodNames`, `probeForAnyMaster` and `sentinelRolloutComplete` in
  [`internal/controller/valkey_controller.go`](../../internal/controller/valkey_controller.go);
  `listMasterLabeledPods`, `clearDrainStamps` and `recreatedAfter` in
  [`internal/controller/steady_state_master.go`](../../internal/controller/steady_state_master.go);
  the five pod reads and six pod deletes in
  [`internal/controller/rolling_update.go`](../../internal/controller/rolling_update.go).
* [`internal/builder/rbac.go`](../../internal/builder/rbac.go) — `SidecarRolePodNames`, whose
  live half D9 filters and whose desired half it deliberately leaves alone.
* [`internal/health/checker.go`](../../internal/health/checker.go) — `podAddress`, which is
  why the network door needs a per-pod DNS record on top of the labels.
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
