# ADR 0023: A StatefulSet whose volumeClaimTemplates no longer match the spec is refused, not rewritten

## Status

Accepted. Date: 2026-08-23.

Amended 2026-08-26: D4 gains D4a. The Sentinel call site may report a claim conflict and
may no longer clear one — the unconditional clear in the guard's `default:` arm erased
the data tier's report on every pass of every Sentinel cluster that had a conflict.
D4's "costs nothing" is marked superseded in place. Implemented and unit-verified; not
run against a cluster.

Implemented: `VolumeClaimTemplatesConflict` in the builder, `guardVolumeClaimTemplates`
in both StatefulSet reconcilers, the `RecreateRequired` `ReconcileBlocked` reason, the
`StorageSpecNotApplied` condition, and the two Warning Event reasons.

Verified in a Kind cluster on 2026-08-23 (Kubernetes 1.36), on a **non-Sentinel**
three-replica cluster — the topology on which the D4a defect is unreachable, which is why
that verification passed while the defect stood: a running three-replica
cluster that gains `spec.persistence` is refused — `ReconcileBlocked=True` and
`StorageSpecNotApplied=True`, both with reason `RecreateRequired`, the StatefulSet
untouched and no PersistentVolumeClaim created. The apiserver premises behind the
guard are pinned in envtest (`test/integration/volumeclaim_conflict_test.go`).

Not implemented, deliberately: the operator never performs the migration itself, and
since the measurements in D6 it does not name a procedure either — it states the cost
and refuses to write.

Discovered while verifying, not fixed here: the orphan-delete recovery this ADR was
drafted to recommend wedges the statefulset-controller, and clearing that wedge cost
the dataset through `detectAndResolveSplitBrain`. Both are recorded under D6 with
their reproductions and carried in the residual risks.

## Context

`spec.persistence` decides the shape of the data StatefulSet in two places at once
([`internal/builder/statefulset.go`](../../internal/builder/statefulset.go)): the
`volumeClaimTemplates` exist only when persistence is enabled, an `emptyDir` volume
named `data` is added to the pod template only when it is disabled, and the container
mounts `data` either way. The two halves are alternatives for the same name.

`reconcileStatefulSet` copies `Spec.Replicas`, `Spec.Template` and `Labels` onto the
live object and **never** `Spec.VolumeClaimTemplates` — which is correct, because a
StatefulSet's `spec` is immutable outside a whitelist that does not contain them. What
it means is that a `spec.persistence` change on an existing cluster is not drift that
the next pass converges. There is no next pass that can.

The 1.11.0 fleet rollout on 2026-08-22 surfaced the first of three shapes. A cluster
whose StatefulSet was created without persistence had gained `spec.persistence` in Git
four months earlier under an operator version that never rendered it. 1.11.0 rendered
it correctly, and every pass since submits a pod template mounting `data` against a
live object where no such volume can exist. The API server rejects it —
`spec.template.spec.containers[0].volumeMounts[1].name: Not found: "data"` — the CR
sits at phase `Error` under the catch-all `WriteFailed` reason with the raw API error
as its message, and no Event names what to do. It stayed that way for four months
without anyone acting.

The other two shapes were found by probing a real API server (envtest, kube-apiserver
1.29.0) in every direction of the toggle. They are worse than the first, because they
are quiet:

| Spec change | What the API server does |
|---|---|
| `enabled` false → true | Rejects the update on every pass. Loud, blocked, actionable once named. |
| `enabled` true → false | **Accepts it.** The template gains the `emptyDir` while the untouched claims stay on the object, both under the name `data`. No error ever. |
| `size` / `storageClass` while enabled | Nothing at all: `StatefulSetHasChanged` does not compare claims, and neither hash covers size or class, so the operator never writes. |

The accepted one is the reason this ADR refuses writes rather than only reporting
them. After a disable the stored StatefulSet holds a pod template and a claim
disagreeing about the same volume name, and the statefulset-controller resolves that
in the claim's favour (`updateStorage`, upstream — read from the controller source,
not run here): pods keep being generated with the PVC while the operator's own config
says persistence is off. Nothing in the cluster reports it.

Two more probe results constrain any fix. Every direct write to `volumeClaimTemplates`
— add, clear, resize, reclass — fails with the same field-level `Forbidden` cause on
`spec`, HTTP 422, with `apierrors.IsForbidden` returning **false**; the error names
none of the fields involved, so a guard cannot be built by parsing rejections. And
orphan-deleting the StatefulSet and recreating it with different claims is accepted,
which is what makes the documented manual recovery in
[ADR 0020](0020-write-only-what-the-operator-owns.md) D1 work at all.

## Decision

**D1 — `volumeClaimTemplates` are compared before the drift check, and only on the
fields the builder decides.** `VolumeClaimTemplatesConflict` compares the claim names,
the storage request through `Quantity.Cmp`, the storage class nil-aware, and the
access modes as a set. **Labels are deliberately excluded**: the builder stamps the
common label set on the claim, that set carries the Valkey version taken from
`spec.image`, and the live claim is frozen at creation time — a comparison including
labels would report a conflict on the first image bump of every persistent cluster and
never stop. The API server also defaults fields on the stored claim that the builder
never sets (`volumeMode`, `status`), which is the second reason this is a whitelist
and not a `DeepEqual`.

**D2 — A structural conflict fails the step; a parameter conflict does not.** A
different *set* of claims means persistence was toggled, and both directions are
unwritable — one rejected, one accepted and wrong. The step returns
`errRecreateRequired`, so the pass is blocked and nothing is submitted. A *same* set
with different values means only the storage parameters are stuck: the pod template
update is legal, unrelated to the claims, and carries the replica count with it.
Holding it would wedge an atomic apply that changes size and image together — the
exact failure [ADR 0015](0015-one-crd-validated-by-schema-only.md) D4 rejects — for a
difference no write will ever settle. It is reported and the pass continues.

**D3 — The refusal ranks between a foreign object and an admission rejection.**
`reconcileBlockedReason` maps `errRecreateRequired` to `RecreateRequired`, below
`ForeignObject` and above `AdmissionWebhookDenied`. The ordering is
[ADR 0020](0020-write-only-what-the-operator-owns.md) D5's argument applied twice: an
admission gate reopens by itself while this clears only when a human acts, so it
outranks the gate; a foreign object means nothing under that name is ours at all,
which has to be said first. The ordering below `ForeignObject` is also structural —
the provenance guard returns before this check runs, so a foreign StatefulSet is never
diagnosed as a storage conflict.

**D4 — Both StatefulSet reconcilers call the same guard.** Sentinel keeps its state on
an `emptyDir` and its builder writes no claims, so the call compares empty against
empty ~~and costs nothing~~. It is there because `reconcileSentinelStatefulSet` shares
no code with the data one: a future Sentinel storage feature would otherwise
reintroduce the trap in the half nobody thought to guard.

> **Amended 2026-08-26 by D4a.** "Costs nothing" was false, and the way it was false is
> the defect D4a exists to fix. The call is kept; what it may do is narrowed.

**D4a — Either tier may report a claim conflict; only the data tier may clear one.**
Added 2026-08-26.

`StorageSpecNotApplied` is one condition with two evaluators, and the Sentinel one runs
second — `{name: "StatefulSet"}` is ordered before `{name: "Sentinel resources"}` in
[`resourceReconcileSteps`](../../internal/controller/valkey_controller.go), and
`runReconcileSteps` continues past a failing step by design (ADR 0001). The Sentinel
guard therefore always landed in the `default:` arm and cleared what the data tier had
just reported, and `writeStatusCondition` re-`Get`s the CR, so the presence guard did
not stop it either: the condition flipped `True` → `False` on **every pass**, two status
writes and two `LastTransitionTime` moves per reconcile, for as long as the conflict
stood.

The consequence was worst in the shape D2 leaves unblocked. On a parameter conflict the
guard returns `nil`, nothing sets `ReconcileBlocked` and the phase stays `OK` — so the
condition is the only durable statement the CR makes about its storage, which is exactly
what D5 built it for, and it said the opposite of the truth.

The rule going forward: `guardVolumeClaimTemplates` takes a `mayClear` argument, the data
call site passes `true` and the Sentinel one `false`. **Reporting is not narrowed** — a
Sentinel StatefulSet that somehow carries a claim still refuses and still reports under
its own Event reason, which is what D4 is for. Clearing is narrowed to the tier whose
builder writes the claims, because a tier that compares empty against empty has proven
nothing about the tier that does not.

The registry row records this as an ownership rule rather than as one evaluator
([ADR 0027](0027-conditions-are-levels-edges-or-history.md) D1): two sites still compute
a value and the last writer still owns `Reason` and `Message`, so claiming a single
evaluator would be a statement the code contradicts.

**The rule holds only while the data step runs first**, which nothing pinned before this
change and which swapping two lines silently inverts.
`TestResourceReconcileSteps_StatefulSetBeforeSentinelResources` is that pin.

**Residual risks of D4a, stated rather than argued away:**

* **A Sentinel-tier report has no clear owner.** If the Sentinel builder ever writes
  claims, a conflict it reports is retracted by the data tier's clear on any pass where
  the Sentinel step does not run — `reconcileSentinelResources` is a fail-fast chain, so
  a failing Sentinel ConfigMap or headless Service skips the guard entirely. The clean
  answer there is a second condition type, and it is deliberately not paid for a tier
  that cannot conflict through any operator-written path today.
* **A zero-evaluator pass leaves the last value standing.** `reconcileStatefulSet`
  returns before the guard on a `Get` error and on a foreign StatefulSet, so on a
  Sentinel CR whose data StatefulSet is foreign neither tier evaluates and a `True`
  stands indefinitely. That is the level hazard ADR 0027 names, and it is unchanged by
  this decision — non-Sentinel clusters have always behaved this way.
* **Unit-verified only.** The tests are three unit tests: a full `reconcileResources`
  pass on a Sentinel CR with a data-tier conflict (which is what pins the cross-step
  rule), the single-step twin on `reconcileSentinelStatefulSet`, and the order pin. The
  2026-08-23 Kind verification recorded in Status above ran on a **non-Sentinel**
  cluster, which is precisely why this survived it; no cluster run covers D4a.

**D5 — The fact is durable on the CR, not only in Events.** `StorageSpecNotApplied`
answers "is the storage the spec asks for the storage that runs" and is True for both
conflict shapes, with the reason distinguishing them. Events expire; this is what a
fleet-wide query and [ADR 0021](0021-per-resource-metrics-and-the-alert-that-was-missing.md)
D1's `vko_valkey_status_condition` series read. It needs no recheck cadence, unlike
`SentinelPeersStale`: a claim conflict appears only when the spec changes and
disappears only when the spec changes back or the StatefulSet is replaced, and both of
those wake a pass on their own — a generation bump or the `Owns(&appsv1.StatefulSet{})`
watch. The condition is cleared only when it exists, so clusters that never had a
conflict carry no condition rather than a permanent False one.

**D6 — The operator states the cost and does not name a procedure it cannot stand
behind.** An earlier draft of this ADR had the Event carry the orphan-delete recovery
[ADR 0020](0020-write-only-what-the-operator-owns.md) D1 documents. Running that
recovery end to end for the first time (2026-08-23, Kind, Kubernetes 1.36) showed it
does not work in the enabling direction, twice over — see the two measurements below.
Until those are fixed the Event says that the change needs a hand-recreated
StatefulSet, that **the operator does not carry the dataset across it**, and that
reverting `spec.persistence` clears the block for free. The procedure and its cost
live in the README, where they can be stated at length and kept honest.

**Measurement 0 — the recovery works, losslessly, in the disabling direction.** The
pods survive the orphan delete, the operator recreates the StatefulSet without claim
templates, the statefulset-controller re-adopts, and the failover-aware rolling
update replaces every pod with an `emptyDir`-backed one. End state: three new pods,
`phase=OK`, `DBSIZE=1` with the seeded key on all three. It works because
`storageMatches` has nothing to check when there are no claim templates, so the
adopted pods are never candidates for the update that fails below. The old claims
stay bound and unused. This is why D6 is written per direction rather than as one
refusal.

**Measurement 1 — the orphan-delete recovery wedges the statefulset-controller in
the enabling direction.**
The pods survive and the recreated StatefulSet does adopt them, so ADR 0020 D1's
adoption assumption holds. What follows does not: the controller then tries to attach
the new claim to each adopted pod, and a pod spec is immutable —
`Pod "probe-0" is invalid: spec: Forbidden: pod updates may not change fields other
than spec.containers[*].image, ...`, with the rejected diff naming
`+ "ClaimName": "data-probe-0"`. The sync fails on the lowest mismatching ordinal and
returns, so **no missing pod is created either**. Measured: 31 `FailedUpdate` events
over five minutes and a cluster left at 2/3 pods, the third having been deleted by
this operator's own rolling update and now uncreatable. Deleting the adopted pods by
hand, lowest ordinal first, does clear it.

**Measurement 2 — clearing the wedge lost the dataset.** The ordering is forced: the
controller wedges at the lowest mismatching ordinal, so pod-0 goes first, and pod-0
is where a cluster's master usually is. Its sidecar drained and promoted the last
surviving replica, which held the data. Pod-0 then came back on a **fresh, empty**
claim, still named by `vko.gtrfc.com/known-master`, and reported master — and
`detectAndResolveSplitBrain` picks the recorded name unconditionally
([`internal/controller/rolling_update.go`](../../internal/controller/rolling_update.go)),
with the connected-slaves tiebreak reached only when the recorded name matches none
of the masters. It logged `realMaster=probe-0, roguePod=probe-2` and demoted the pod
holding the data. End state: a healthy-looking cluster, three bound claims,
`phase=OK`, `DBSIZE=0` everywhere. The promotion path has
`verifyPromotionCandidateHoldsData` for exactly this
([ADR 0011](0011-evidence-based-steady-state-split-brain-resolution.md)); this path
has no equivalent. It is a defect in its own right ~~and is not fixed here~~ — this ADR
only refuses to send anyone into it.

> **Corrected 2026-08-26.** "Not fixed" stopped being true on 2026-08-26:
> [ADR 0028](0028-a-demotion-may-not-discard-the-only-dataset.md) is the fix. The
> rolling-update resolver no longer trusts the recorded authority unconditionally — an
> authority holding zero keys while the rogue holds some ends that demotion, and an
> unreadable count is a refusal rather than a demotion. The refusal this ADR makes is
> unchanged; only the sentence claiming nobody had addressed the demotion is.

**D7 — The Event never promises that recreating changes existing storage, because it
does not.** Claims are named `data-<statefulset>-<ordinal>` and are reused by name, so
a recreated StatefulSet binds the ones that already exist and only a claim created
afterwards — a scale-out — follows the new template. Growing a volume is an edit on
each PersistentVolumeClaim and needs a StorageClass with `allowVolumeExpansion`;
changing the class means moving the data.

**D8 — The ConfigMap keeps converging while a conflict stands.** The ConfigMap step
runs before the StatefulSet step and, per
[ADR 0001](0001-continue-reconciling-past-a-rejected-write.md), keeps succeeding when a
later one fails. So the `save`/`appendonly` directives follow the spec immediately
while the volumes do not, and a pod that restarts for any other reason boots the new
persistence config against the old volume layout. This is accepted rather than
engineered away: holding it would mean rendering the data ConfigMap from live cluster
state instead of from the spec, which puts a cluster read into the config hash the
whole rolling update is keyed on. The mixed state costs consistency between the dump
settings and the volume, never the dataset — a restarted pod rejoins as a replica and
resyncs from the master.

## Consequences

- Clusters that toggled persistence **off** at some point and reconcile cleanly today
  become blocked on upgrade, with phase `Error`. That is the honest report of a state
  that was always broken and never said so, but it is an upgrade-visible change and
  belongs in the release notes.
- The already-blocked shape changes its `ReconcileBlocked` reason from `WriteFailed`
  to `RecreateRequired`. Anything filtering or silencing on the old reason sees a
  transition.
- While a structural conflict stands **every** StatefulSet write is held, not only the
  storage one: replica scaling, image changes and label changes all ride the same
  `Update`. The Event says so.
- One more condition type, and therefore one more `vko_valkey_status_condition` series
  per resource (ADR 0021).
- No chart change is needed for alerting: `ValkeyReconcileBlocked` matches any reason
  and groups by it. `ValkeySpecNotObserved` will **not** fire for a blocked CR — the
  condition is written with the current `observedGeneration`, so the generation pair
  stays closed. The two named alerts are the only metric signals, and the whole
  PrometheusRule ships default off.
- `TestReconcile_UpdatesConfigMapOnSpecChange` was inverted rather than deleted: it
  toggled persistence on an existing cluster and asserted the pass succeeded. It now
  asserts D8 — the ConfigMap converges *and* the pass reports the conflict.

## Alternatives Considered

**Automate the migration: orphan-delete the StatefulSet and let the next pass recreate
it.** Rejected, and the reason is when it would run rather than what it does. Every
silently drifted cluster in a fleet meets the condition at once, so the migration
fires on upgrade day with no user action at that moment, on up to
`maxConcurrentReconciles` clusters simultaneously — PodDisruptionBudgets constrain
only the Eviction API and the operator deletes, and a fleet-wide throttle is exactly
the shared reconciler state
[ADR 0019](0019-reconcile-concurrency-and-the-cost-of-a-stuck-pass.md) D3 forbids. It
also violates [ADR 0005](0005-upgrade-neutral-defaults-and-anti-affinity.md)'s rule
that an upgrade changes nothing about an existing cluster's behaviour. Three further
gaps priced it: `reconcileStatefulSet` has no `DeletionTimestamp` branch, so during
orphan finalization it would submit the doomed update onto the terminating object; the
re-adoption wait has no action that forces it, so its only honest
[ADR 0010](0010-every-rolling-update-wait-is-bounded.md) successor state is the blocked
condition this ADR defines — the automation structurally contains the refusal as its
failure mode; and during the ownership gap the CR would speak through
`foreignObjectError` and `PodNotOwned` Events whose documented meaning it contradicts.

The measurements in D6 settle it beyond those arguments. The automation would have
performed exactly the sequence that wedges, unattended, on every affected cluster at
once — and the wedge is not self-clearing, so it would have needed the same manual
pod deletions, whose measured cost was the dataset. An automation that reliably
destroys data is not a variant worth gating; it is the reason the refusal is the
whole feature.

**The same automation behind an explicit opt-in.** Held in reserve rather than
rejected. If the D5 condition shows conflicts living unmigrated across a fleet instead
of being resolved through D6, that is the evidence the automation is worth its gates.
It must be a **spec field**, never an annotation: the CR watch is generation-gated, so
an annotation edit on a healthy CR is invisible until the manager's 10 h cache resync,
and every `vko.gtrfc.com` annotation today is operator-written state. A Git-resident
approval is also standing policy rather than a one-shot, and the field name has to be
honest about that.

**A CEL transition rule making `spec.persistence` immutable.** ADR 0015 D3 names CEL as
a permitted enforcement channel and D2's no-webhook decision is untouched by it, so
this was the one option that would reject the edit at apply time, in front of the human
making it — which is a real advantage over a condition that reaches only someone who
looks. Rejected because CEL can compare only the old spec against the new one, and the
invariant is the new spec against the **live StatefulSet**. The proxy is wrong in both
directions: it rejects safe edits — a size change before the StatefulSet exists, any
edit after a migration already cleared the conflict — and it deadlocks D6, whose first
step *is* that spec edit. With the rule in place the only remaining migration is
deleting and recreating the CR, which takes every child object with it and is a full
outage. ADR 0015 D4's GitOps argument applies here and worse: there is no legal edit
ordering at all. A softer rule forbidding only the disable direction inherits all of
this for exactly the direction it guards, and splits one defect family across two
enforcement surfaces that drift independently of the builder.

**Report the conflict without refusing the write.** This is ADR 0020's already-rejected
"treat it as absent without refusing" shape: the pass would end error-free and report
forever with no condition naming the cause, and in the disable direction the doomed
write is the one the API server accepts.

## Residual risks

- **There is no verified lossless migration for the enabling direction.** D6's
  measurements 1 and 2 close the question the earlier draft left open, in the worst
  way: the documented recovery wedges, and forcing past the wedge cost the dataset.
  What ships here is the refusal plus an honest cost statement. Whether a lossless
  path exists — moving the master to the highest ordinal before the recreate, so that
  the pod deleted first is a replica and the pod holding the data is deleted last —
  is plausible from the mechanism and **was not tested**. The disabling direction is
  measured to work (measurement 0), so the asymmetry is real and not an artefact of
  how the run was driven.
- **The split-brain finding is untriaged beyond this scenario.** `detectAndResolveSplitBrain`
  trusting the recorded name over the data is reachable whenever a recorded master
  returns empty while a sidecar drain promoted someone else and a rolling update is in
  flight. A normal roll does not produce it — the operator records its own promotion
  first (ADR 0009) — but this migration does, and nothing establishes that it is the
  only path. ~~It needs its own ADR and its own fix.~~ **Discharged 2026-08-26 by
  [ADR 0028](0028-a-demotion-may-not-discard-the-only-dataset.md)**, which is that ADR
  and that fix: a demotion may not discard the only dataset, and inside a roll only the
  drain stamp and the dataset discriminate.
- **The re-adoption assumption itself held.** ADR 0020 D1 records adoption as asserted
  from the API contract and never run; it now has been, and pods orphaned by
  `--cascade=orphan` were adopted by the recreated StatefulSet with its new UID. That
  half of the documentation is confirmed. It is what happens *after* adoption that
  breaks.
- **`updateStorage` was read, not run.** That the claim wins over a same-named template
  volume in the generated pod comes from the upstream controller source. The envtest
  probe could only show that the API server stores both.
- **The probe ran against kube-apiserver 1.29.0 only** — the repo's declared floor.
  Nothing pins that a later version keeps accepting the disable direction; if it starts
  rejecting it, that shape simply becomes as loud as the other one.
- **D8's mixed state has no expiry.** A cluster can sit with a persistence-enabled
  ConfigMap and emptyDir volumes indefinitely, and on a cluster with frequent pod
  churn every restart moves one more pod onto the new config. It is reported, not
  bounded.
- **Nothing enforces that a conflict is ever resolved.** The condition makes it
  visible; the alert is default off; the CR keeps running.
- **The guard does not cover other immutable StatefulSet fields.** `serviceName`,
  `selector` and `podManagementPolicy` are constants derived from the CR name, and no
  `persistentVolumeClaimRetentionPolicy` is set anywhere in the tree, so
  `volumeClaimTemplates` are today the only immutable field that varies with the spec.
  A future builder change to any of the others recreates this trap outside the guard.

## References

- [`internal/builder/volumeclaim_conflict.go`](../../internal/builder/volumeclaim_conflict.go) — `VolumeClaimTemplatesConflict`, `VolumeClaimConflictKind`
- [`internal/builder/statefulset.go`](../../internal/builder/statefulset.go) — `BuildStatefulSet`, `buildVolumeClaimTemplates`, `StatefulSetHasChanged`
- [`internal/controller/volumeclaim_conflict.go`](../../internal/controller/volumeclaim_conflict.go) — `guardVolumeClaimTemplates`, `errRecreateRequired`, the Events and the condition
- [`internal/controller/valkey_controller.go`](../../internal/controller/valkey_controller.go) — `reconcileStatefulSet`, `reconcileSentinelStatefulSet`
- [`internal/controller/reconcile_blocked.go`](../../internal/controller/reconcile_blocked.go) — `reconcileBlockedReason`
- [`api/v1/valkey_types.go`](../../api/v1/valkey_types.go) — `ConditionTypeStorageSpecNotApplied`, `ReasonRecreateRequired`
- [ADR 0020](0020-write-only-what-the-operator-owns.md) — the provenance guard this runs behind, the fail-direction test, and the manual recovery D6 names
- [ADR 0006](0006-delete-only-what-the-operator-owns.md) — D14, why the PersistentVolumeClaims survive everything
- [ADR 0001](0001-continue-reconciling-past-a-rejected-write.md) — why the ConfigMap converges anyway (D8)
- [ADR 0015](0015-one-crd-validated-by-schema-only.md) — the CEL channel this declined to use, and D4's GitOps argument
- [ADR 0021](0021-per-resource-metrics-and-the-alert-that-was-missing.md) — why a condition is enough to make this alertable
- [`internal/builder/volumeclaim_conflict_test.go`](../../internal/builder/volumeclaim_conflict_test.go) — the comparison, including the image-bump case that pins D1's label exclusion
- [`internal/controller/volumeclaim_conflict_test.go`](../../internal/controller/volumeclaim_conflict_test.go) — the refusal, both directions, and the condition lifecycle
- [`test/integration/volumeclaim_conflict_test.go`](../../test/integration/volumeclaim_conflict_test.go) — the apiserver premises, and the negative case an untouched persistent cluster must produce
- [`test/e2e/persistence_migration_test.go`](../../test/e2e/persistence_migration_test.go) — the refusal on a running cluster, and why it stops before the migration
- [ADR 0017](0017-test-and-ci-policy.md) — the tiers this is verified in
- [ADR 0027](0027-conditions-are-levels-edges-or-history.md) — the registry row D4a produced, and the ownership rule that makes two evaluators legal
- [ADR 0028](0028-a-demotion-may-not-discard-the-only-dataset.md) — the demotion defect D6 discovered and left open, now fixed
- [`internal/controller/condition_registry.go`](../../internal/controller/condition_registry.go) — the `StorageSpecNotApplied` row and its `ownershipRule`
- [`internal/controller/reconcile_steps_test.go`](../../internal/controller/reconcile_steps_test.go) — `TestResourceReconcileSteps_StatefulSetBeforeSentinelResources`, the step order D4a depends on
