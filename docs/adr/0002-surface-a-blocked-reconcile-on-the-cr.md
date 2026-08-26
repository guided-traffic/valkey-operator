# ADR 0002: Surface a Blocked Reconcile on the CR

## Status

Accepted. Date: 2026-08-21.

Implemented on branch `feat/support-pdb` as `a700600`, `12a1267`, `cf62c55` and
`744b589` — the last carries D7's log-and-continue, D10 and D11. Not yet released, verified
in this repository: no tag contains those commits and no branch other than
`feat/support-pdb` does. Guarded by
[`internal/controller/reconcile_blocked_test.go`](../../internal/controller/reconcile_blocked_test.go),
[`status_phase_test.go`](../../internal/controller/status_phase_test.go),
[`condition_generation_test.go`](../../internal/controller/condition_generation_test.go)
and the e2e `TestE2E_AdmissionRejection_ReconcileBlockedCondition`
([`test/e2e/admission_recovery_test.go`](../../test/e2e/admission_recovery_test.go)). Those
files were read and their assertions match the rules below; no suite was run for this ADR.

Amended 2026-08-26: D5 gains an implementation correction and D5a; D10 gains an amendment
and D10a. `observerReady` never satisfied D5 and was moved into `persistStatus`; the
`SidecarUpdatePending` clear gained a second site at the completion branch; the `Ready`
contract is now declared on `vkov1.ConditionTypeReady` in `api/v1`. Implemented and verified
in this repository: `make test-unit`, `make lint` and `make cyclo` are green, and each new
regression test was confirmed to fail against the pre-fix code. No e2e or integration suite
was run for this amendment.

Amended 2026-08-22: D7 keeps its rule for the conditions this ADR is about, and gains an
exception. `setStatusCondition` is now a logging wrapper around `writeStatusCondition`,
which returns the error and retries a conflict against a freshly read CR; a caller whose
condition is a one-shot record with no later pass to recompute it uses that function
directly and decides for itself
([ADR 0010](0010-every-rolling-update-wait-is-bounded.md) D15).

## Context

During the 2026-08-19 infra-d incident (context in
[ADR 0001](0001-continue-reconciling-past-a-rejected-write.md)) the CR showed
`PHASE=Provisioning`/`Error` and no condition naming the admission rejection. A
user could not tell "my webhook is down" from "my storage is broken" without
reading operator logs. Both the phase values and the absence of the condition were read
off that cluster during the incident: measured on a cluster, not reproducible from this
repository.

Once [ADR 0001](0001-continue-reconciling-past-a-rejected-write.md) made the pass
continue past a rejected write, a second problem surfaced: while
`reconcileResources` failed but the data plane was healthy, every pass wrote the
phase twice with opposite values — `updateStandaloneStatus`/`updateHAStatus`
computed `OK`, then the final `updatePhase` overwrote it with `Error`.
`statusUnchanged` could not suppress the first write because the previous pass's
final value was already `Error`. Watchers — Lens, `kubectl get -w`, monitoring
keyed on `status.phase` — saw OK↔Error oscillation on every blocked pass. The double write
is verified by reading the pre-fix `Reconcile` (`12a1267^`), where the phase write sat
after `reconcileWorkload` and behind an early `return` on its error; the oscillation
follows from it and was not captured from a watch stream.

A rejected CR write is therefore not an error path to log and retry. It is an
ordinary runtime state that must be legible on the object itself.

## Decision

**D1 — A rejected write is reported as the `ReconcileBlocked` condition.** When a
managed write is rejected, `setReconcileBlockedCondition`
([`internal/controller/reconcile_blocked.go`](../../internal/controller/reconcile_blocked.go))
sets `ReconcileBlocked=True` with the rejecting webhook named in the message, and
flips it to `False`/`ReconcileSucceeded` on the first clean pass. `status.conditions`
already exists on the CRD, so this needs no schema change.

**D2 — Classification is message-based, not typed.** `isAdmissionRejection` matches
`failed calling webhook` (fail-closed webhook unreachable — the incident's shape),
`admission webhook ... denied the request` (explicit denial), and as a fallback an
internal error mentioning the admission chain. `apierrors.IsInternalError` alone is
not sufficient and is not used alone: callers wrap errors
(`fmt.Errorf("sentinel statefulset: %w", err)`), and an explicit denial arrives as
`Forbidden`, not as an internal error. Message matching also survives `errors.Join`,
whose concatenated text ORs the admission shapes across all joined errors.

**D3 — A blocked pass has exactly one phase authority, and it writes last.**
`Reconcile` marks the pass with `withBlockedPass` when `reconcileResources` fails.
While blocked, `updatePhase` drops every intermediate write — `updateStatus`'s, every
`updatePhase` call on the rolling-update paths (progress, syncing, the paused-error
phase, the two failover phases), the Sentinel-RU error phase, the no-master recovery
phase — and the final `writePhase` at the end of `Reconcile` bypasses the
suppression and is the pass's single phase write: `ValkeyPhaseError` plus the joined
step errors, written **after** `updateStatus` so it cannot be overwritten.

**D4 — The blocked flag rides on the context, never on the reconciler.**
`withBlockedPass`/`passIsBlocked` store it under `blockedPassKey{}` in the pass
`context.Context`. With `MaxConcurrentReconciles > 1` a reconciler field would leak
one CR's blocked state onto another CR's concurrent pass and suppress a legitimate
phase write.

**D5 — Suppression covers the phase only.** `persistStatus` restores the previous
phase and message while blocked, but `readyReplicas`, `masterPod`, `observerReady`
and the `Ready` condition keep updating. A rejected managed write says nothing about
the running data plane; freezing the whole status would hide real cluster state
behind an unrelated admission failure.

Amended 2026-08-26: **the rule is unchanged and was not implemented for two of the four
fields.** `statusUnchanged` compares `readyReplicas` and `observerReady` correctly, but
`updateStatus` assigned both in its prologue — *before* `updateStandaloneStatus` and
`updateHAStatus` captured the `prevStatus` those comparisons run against. Each field was
therefore compared against itself, and `persistStatus` skipped the write. `masterPod`,
`OperatorVersion` and the conditions were unaffected because they are set after the
capture; the NOTE in `updateStatus` documented that exact hazard for `OperatorVersion` and
nobody applied it to its two neighbours.

Consequence, measured read-only on a live fleet (2026-08-25, 12 CRs): `observerReady` was
wrong on **six of the eight** observer-enabled clusters, in both directions. On one CR the
sequence is exact — a status write at `07:05:07Z` sampled the observer three seconds before
it became `Available` at `07:05:10Z`, and the field still read `false` fourteen minutes
later. Three seconds of real lag became a permanently wrong value, because a field with no
proxy in phase, message or conditions has no passenger seat: nothing else changes when only
it changes. `readyReplicas` carries the identical defect and is masked rather than fixed —
every branch's phase message is a function of the ready count, so it always rides along.
That masking is a property of the current message strings, not an invariant.

The fix keeps D5's wording and moves the assignment: `observerReady` is now computed in
`persistStatus`, next to `v.Status.OperatorVersion`, which is the side of the capture this
ADR always meant. Guarded by
`TestUpdateStatus_ObserverReadyTransitionIsPersistedOnItsOwn` and
`TestUpdateStatus_DisablingTheObserverClearsAStoredVerdict`
([`internal/controller/valkey_controller_test.go`](../../internal/controller/valkey_controller_test.go));
both were confirmed to fail against the pre-fix assignment order before being kept.
`readyReplicas` is deliberately left where it is — see Residual risks.

**D5a — `Ready` is the data-plane verdict, and its disagreement with the phase is the
design.** Added 2026-08-26. D5 kept the condition updating without ever saying what it
means, and the pair `Ready=True` beside `phase=Error` was then filed as a contradiction off
a live cluster. It is not: `status.phase` carries two meanings — the data-plane verdict and
whether the operator can converge the spec — and D3 hands the field to the second one while
blocked. `Ready` carries only the first. The contract now lives on
`vkov1.ConditionTypeReady`, which moved out of `internal/controller` (where it was an
unexported string constant, and therefore the one condition every CR carries with no
declared type and no entry in any table built from `api/v1`) into
[`api/v1/valkey_types.go`](../../api/v1/valkey_types.go). It is stated in the README
condition table and pinned by `TestUpdateStatus_KeepsNonPhaseFieldsWhileBlocked` and
`TestUpdateHAStatus_KeepsReadyTrueWhileBlocked` — the second because no test at any tier
reached the `HAClusterReady` shape the finding was actually reported on.

The alternative of reconciling the two surfaces rather than documenting them was weighed
and refused; see Alternatives.

**D6 — A blocked pass writes its phase even when the workload half also failed.**
`Reconcile` keeps the workload result instead of returning on it, writes the Error
phase, and then returns `errors.Join(resourceErr, workloadErr)`. An early
`if workloadErr != nil { return }` in front of the phase write is forbidden — it
left the phase at the previous pass's value while `ReconcileBlocked=True` and
dropped the workload error entirely. That is the field sample
`phase=Provisioning blocked=True` observed under a sustained `CREATE configmaps`
block — measured on a cluster, not reproducible from this repository: the write was
skipped, not silently failing.

**D7 — A failed status write never ends the pass.** The empty-phase branch in
`Reconcile` logs and continues, so `reconcileResources`, `setReconcileBlockedCondition`
and `reconcileWorkload` all run even when the CR **status** subresource is itself
blocked. Otherwise a webhook guarding `valkeys/status`, or lost RBAC on it, leaves a
brand-new CR with an empty phase and no condition — invisible for exactly the failure
class this condition exists to surface. Likewise, `setStatusCondition` logs its
failure and still returns no error: a condition is a report about the pass, never a
reason to fail it, and the write is self-healing because the next pass recomputes it.
Amended 2026-08-22: that justification is the *reason* for the rule, not decoration,
and it only holds while a next pass recomputes the condition. `setStatusCondition` now
wraps `writeStatusCondition`, which returns the error and retries a conflict against a
freshly read CR — the read goes through the manager cache, so a caller that just wrote
the CR itself sees its own pre-update version. A condition with exactly one writer per
rolling update has no self-healing pass and is written through `writeStatusCondition`
directly ([ADR 0010](0010-every-rolling-update-wait-is-bounded.md) D15). Every condition
this ADR is about — `ReconcileBlocked`, `SidecarUpdatePending`, `RollingUpdatePaused` —
keeps the swallowing rule unchanged.

**D8 — Steady state costs no status write.** `setStatusCondition` returns without
issuing `Status().Update` when `meta.SetStatusCondition` reports no change, and
`setReconcileBlockedCondition` skips identical writes in both the set and the cleared
branch. A healthy CR reconciles every few seconds and a blocked one on the rate
limiter's exponential backoff (5 ms doubling, capped at `reconcileRetryMaxDelay` =
30 s) — a blocked pass returns the joined error and discards the workload result, so
it never takes the 10 s requeue. An unconditional update is pure API churn at exactly
the moment (a cluster-wide admission outage) when the API server can least absorb it.

**D9 — Every condition carries `ObservedGeneration`, read from the refreshed
object.** `setStatusCondition` stamps `ObservedGeneration: v.Generation` on every
condition written through it — `ReconcileBlocked`, `SidecarUpdatePending`,
`RollingUpdatePaused` and `TopologyRestored` — taking the generation from the object
it refreshed inside the function (since 2026-08-22 the refresh and the stamp sit in
`writeStatusCondition`, which `setStatusCondition` wraps; the rule is unchanged). kstatus-style tooling reads a missing
`observedGeneration` as generation 0, i.e. as permanently stale, so an unstamped
condition is ignored by every consumer that checks freshness. The
skip-if-unchanged guard of D8 **includes** `ObservedGeneration` (added to the message
check, never substituted for it), so a cluster that stays blocked across a spec edit
re-reports the same reason for the new generation instead of appearing not to have
evaluated it.

**D10 — A condition that reports deferred work must be clearable from the converged
state.** `clearSidecarUpdatePending` is called from `checkAndHandleRollingUpdate`
immediately before the `needsRollingUpdate == false && state == ""` early return —
the only site that provably knows every pod matches the live template. Its previous
only caller sat at the end of `handleStandaloneRollingUpdate` (`744b589^`, verified by
reading), unreachable once the deferred update actually applied, so
`SidecarUpdatePending=True` stayed set forever and a converged cluster was
indistinguishable from one that never applied the update.

Amended 2026-08-26: **there are two such sites, and neither subsumes the other.** "The only
site" above is superseded: it was wrong about the pass that *completes* a roll. That pass got
past the early return by definition — a pod needed updating, or state was recorded — so it
never reached the clear, and the completion branch cleared only the rolling-update state
annotation. The completing pass schedules no follow-up either: the healthy path returns a
zero requeue, the CR watch is `GenerationChangedPredicate`-gated, there is no Pod watch and
no `SyncPeriod` override, so the next *guaranteed* pass is the controller-runtime cache
resync of an owned object.

Measured read-only on a live fleet (2026-08-25): four non-Sentinel clusters completed their
data roll within four seconds of each other on 2026-08-22, and the clear landed 1 s, 3 s,
6 min 9 s and **41 min 9 s** later — each time only because an unrelated pod-kill happened
to enqueue a pass. A fleet audit read the CRs inside that window and filed the 41-minute
lag as a permanent stall, which it was not; on a cluster nothing disturbs, the resync is
the real bound.

The clear is therefore also called from the `result.Completed` branch of
`checkAndHandleRollingUpdate`, **before** `clearRollingUpdateState` so a failing annotation
write cannot skip it. The convergence proof there is `updatedCount == totalPods`, which
`countUpdatedPods` evaluates as `!needsUpdate && reachable()` over the whole tier — not the
`Completed` flag itself, because two of the completion sites inside
`verifyTopologyRestored` are stalled completions that could not re-read the pods; both are
entered only through that same count, so the proof holds for them too. Guarded by
`TestCheckAndHandleRollingUpdate_CompletionClearsSidecarUpdatePending`
([`internal/controller/sidecar_pending_condition_test.go`](../../internal/controller/sidecar_pending_condition_test.go)),
confirmed to fail without the call.

Clearing from inside `clearRollingUpdateState` instead — one site covering all eight of its
callers — was considered and **rejected**: two of those callers clear state *mid-roll*
without proving convergence, so it would erase a `True` that is still accurate. The same
trap makes it the wrong site for `RollingUpdatePaused`, whose own clear gap is a separate
open item.

**D10a — The condition names the pod and the number the decision was made on.** Added
2026-08-26. The message was the fixed string "Standalone pod has an outdated sidecar image",
and a fleet audit read it on a three-replica cluster, where it looked like a contradiction
rather than the pre-v1.5.1 legacy value it was (before `3f0a1fe` the dispatch had no
topology guard, so every non-Sentinel cluster went through the standalone handler). The
mismatch is still reachable and is therefore not only a wording fix: the deferral guard
reads `v.Spec.Replicas <= 1` while the loop walks `*currentSts.Spec.Replicas`, and a refused
StatefulSet write ([ADR 0023](0023-volume-claim-templates-are-immutable.md)) holds the two
apart — so a cluster scaled down to one replica in that state can carry three running pods.
The message reports `spec.replicas` as the number the decision was made on, never as a
claim about how many pods exist, and `setSidecarUpdatePendingCondition` takes the pod name
as a parameter so a pending state without a named pod is unrepresentable.

**D11 — On the non-Sentinel path `status.masterPod` derives from the `-rw` Service
selector, never from the ordinal.** `currentMasterPod` answers in authority order: the
`instanceRole=master` label when exactly one pod carries it (that is the literal
selector of the `-rw` Service — not a guess at the master but the pod receiving
writes), then the known-master annotation whenever the label does not answer — zero or
several labeled pods, and equally a pod `List` that errors, which is logged and falls
through to the same record — then pod-0. `replicas <= 1` returns pod-0 without reading
anything. **Two labeled masters deliberately picks no winner** — that state belongs to
`checkSteadyStateSplitBrain`
([ADR 0011](0011-evidence-based-steady-state-split-brain-resolution.md)). With Sentinel
the field has a different source and `currentMasterPod` is never called: `updateHAStatus`
writes `clusterState.MasterPod` — the master as Sentinel reports it.

**D12 — The status reports a per-instance current task.** `OK` when the instance is
healthy, otherwise a short description of what the operator is doing
(`Rolling Update 2/3`, `Syncing`, `Failover in progress`), readable in Lens. This is
why the split-brain paths requeue rather than end the pass: ending it would skip
`updateStatus` and freeze the CR at its last verdict — usually `OK` — while the
operator loops on a real problem invisibly.

**D13 — `ReconcileBlocked` covers only writes the operator itself performs.**
Rejected **pod** creation never surfaces there: pod creation belongs to the
statefulset-controller under `updateStrategy: OnDelete`, and that failure mode is
covered by the nudge ([ADR 0003](0003-nudge-a-short-of-pods-statefulset.md)) instead.
Naming the boundary prevents the recurring expectation that the condition explains
every stall.

**D14 — Condition messages are truncated at 1024 runes, keeping the front.**
`truncateConditionMessage` appends a literal `...` to the cut, so a truncated message is
stored at 1027 runes; `conditionMessageLimit` bounds the copied error, not the field. The
webhook name is always at the front of the API server's message, so keeping the head
preserves the one datum the condition exists to convey while bounding object size.

## Consequences

* While blocked, the CR reports `Error` even when the data plane is perfectly
  healthy. The health verdict is still readable from the non-phase fields and
  returns on the first successful pass.
* A pass that enters or changes the blocked state costs up to two status writes of its
  own (the `ReconcileBlocked` condition and the final phase); a persistently blocked
  pass with an unchanged error writes neither, because both are skip-guarded (D8, and
  `writePhase`'s own phase/message comparison). On top of that, `persistStatus` still
  writes whenever one of the non-phase fields D5 keeps updating actually changed.
  Truthfulness beats write economy here: `PHASE=OK` under a rejected write is the exact
  failure the condition exists to remove.
* `lastTransitionTime` reflects the real transition rather than the last
  observation, so it cannot be used as a heartbeat.
* One extra status write per **generation change** (not per pass) from D9.
* Multi-replica non-Sentinel clusters pay a cache-served pod List on every healthy
  status computation for D11 — the call sits in the all-replicas-ready branch of
  `updateStandaloneStatus`, before `persistStatus` decides whether to write, so an
  unhealthy pass pays nothing and a healthy pass pays even when the write is skipped.
  Sentinel and single-replica clusters pay nothing.
* Every function that participates in phase suppression must receive the pass
  context — a helper that fabricates its own context silently loses the flag.
* On a pass failing many steps, later steps' errors are cut from the CR by D14 and
  remain only in the log.
* Paths that must keep working during a blocked pass have to avoid API writes
  entirely. The split-brain demotion path is deliberately write-free for this reason
  ([ADR 0011](0011-evidence-based-steady-state-split-brain-resolution.md)).

## Alternatives Considered

### Leave the diagnosis to `status.phase` / `status.message`

The pre-change state, and the fourth finding of the incident write-up: a stall with no
machine-readable cause. Rejected. That write-up is a working note outside this repository,
so the finding is recorded here rather than cited.

### Add a new status field instead of a condition

Unnecessary — `status.conditions` already exists on the CRD
([`config/crd/bases/vko.gtrfc.com_valkeys.yaml`](../../config/crd/bases/vko.gtrfc.com_valkeys.yaml)),
so the condition costs no schema change. Verified by reading the generated CRD; `make
manifests` was not re-run for this ADR.

### Typed classification via `apierrors.IsInternalError` only

Rejected: misses wrapped errors and explicit `Forbidden` denials, which is most of the
real traffic.

### Let `updateStatus` own the phase

Would remove the second status write on a blocked pass, at the cost of reporting
`PHASE=OK` while every managed write is being rejected. Refused.

### Suppress the entire status write while blocked

Removes the flapping but goes blind: the CR freezes at its last verdict and the live
data-plane fields stop updating.

### Fix only `updateStatus`'s phase write

Would still flap against every phase write on the rolling-update paths. The suppression
has to be pass-wide.

### Stamp `ObservedGeneration` from the caller's copy

Rejected as inert on the path that matters most; see D9.

### Clear `SidecarUpdatePending` from `handlePostRollingUpdateChecks` or `updateStatus`

A workable alternative (b in the original analysis) that costs a per-pass condition
evaluation on the healthy path. D10's site was preferred because it is one of only two
places that already prove convergence; the completion branch is the other, and it was added
in 2026-08-26 rather than moving the evaluation onto the status path.

### Clear `SidecarUpdatePending` from inside `clearRollingUpdateState`

Considered in 2026-08-26 as the single site covering all eight callers, and rejected: two of
those callers (`clearStaleRollingUpdateState` and `pauseRollingUpdate`) clear state
*mid-roll*, where nothing proves convergence, so the clear would erase a `True` that is
still accurate. It is named here because it is the fix the next reader will propose, and
because it is also the trap that makes it the wrong site for `RollingUpdatePaused`.

### Make `Ready` follow the phase — `False`/`ReconcileBlocked` while blocked

Refused 2026-08-26, and it is a direct reversal of D5. It presents one verdict at the cost
of deleting the only field that tells an operator "your three-node HA cluster is serving"
during a block — the exact live case that raised the question, where the data plane was
verified healthy and the block was a StatefulSet the operator refused to touch. It would
also move `vko_valkey_status_observed_generation`, which is the maximum `observedGeneration`
across conditions, and therefore the `ValkeySpecNotObserved` alert
([ADR 0021](0021-per-resource-metrics-and-the-alert-that-was-missing.md) D2) — a second
consumer, not only a status field.

### Add a distinct phase value `Blocked` so `Error` means only a broken data plane

Weighed 2026-08-26 and **not taken now**, on cost rather than on principle. It is cheap:
`Status.Phase` carries no `+kubebuilder:validation:Enum` and already holds composed values
like `Rolling Update 2/3`, so a new value needs no CRD change, and `ValkeyPhaseNotOK` stays
armed because it matches `phase != "OK"`. What it costs is a one-time label transition on
`vko_valkey_status_phase`, which resets the `for: 30m` accumulation of an already-firing
alert and silently stops matching any user silence pinned to `phase="Error"` — the same
series-identity argument ADR 0021 uses against a different design. It also does not fix the
identical ambiguity on the four non-blocked `Error` phases. D5a documents the split instead;
if the surface is misread a third time, this is the option to revisit, and the structural
answer (splitting the two meanings into two fields with its own printer column) is the one
after that.

## Residual risks

* **Message-based classification can drift with upstream Kubernetes wording.**
  Accepted: a misclassification only degrades the condition reason to `WriteFailed`,
  it does not change behaviour. A unit table covers quota/conflict/plain errors as
  negatives so the matcher cannot over-classify.
* **`ObservedGeneration` can be over-fresh by one pass.** A spec edit landing between
  the caller's evaluation and the refresh `Get` makes the condition name a generation
  it did not evaluate. The doc comment states this weaker guarantee rather than
  claiming the stronger one; the next pass recomputes.
* **A replica-ConfigMap write rejected during an admission gap stays stale** until
  the block clears. Visible through the `KnownMasterPublishFailed` Event and the
  `ReconcileBlocked` condition; the operator cannot write through an admission block,
  so no guard would change the outcome.
* A user reading only `ReconcileBlocked` cannot see a blocked **pod** creation (D13);
  that path is visible as a short-of-pods StatefulSet plus the nudge.
* **`readyReplicas` still cannot trigger a status write on its own** (D5, amended).
  Deliberately not fixed with `observerReady`: the field is masked by the fact that every
  branch's phase message is a function of the ready count, so it rides along on every pass
  that changes it, and no case could be constructed by reading in which it goes stale. The
  accepted cost is that the masking is a property of the message strings — anyone who makes
  a phase message stop naming the count reopens the defect, and nothing tests for that.
  Moving the assignment next to `observerReady` closes it and is a two-line change if the
  coupling is ever judged too fragile to keep.
* **The `Ready` contract is documented, not enforced** (D5a). Nothing prevents a future
  writer from setting `Ready` off something that is not the data plane, and nothing prevents
  a consumer from reading it as "the operator is healthy". The condition registry
  ([ADR 0027](0027-conditions-are-levels-edges-or-history.md)) records that `Ready` is a
  level with one evaluator, which catches a second evaluator appearing but not a wrong one.
* **`Ready` keeps its pre-roll value for the whole rolling update**, because a pass with a
  roll in flight returns before `updateStatus`
  ([ADR 0001](0001-continue-reconciling-past-a-rejected-write.md) D4). That is decided
  behaviour, and D5a states it, but it means "Ready" and "serving right now" come apart for
  the duration of a roll — as do `masterPod`, `readyReplicas` and `observerReady`. Whether
  that is the intended reading of the condition is an open question, not a settled one.
* **Not verified for the 2026-08-26 amendments:** no e2e or integration suite was run. The
  unit tier, `make lint` and `make cyclo` were run and are green, and each new regression
  test was confirmed to fail against the pre-fix code. Every claim about live fleet state is
  a read-only `kubectl get` observation from 2026-08-25, not a controlled experiment.

## References

* [`internal/controller/reconcile_blocked.go`](../../internal/controller/reconcile_blocked.go) — `setReconcileBlockedCondition`, `isAdmissionRejection`, `withBlockedPass`, `passIsBlocked`, `compactErrorMessage`, `truncateConditionMessage`
* [`internal/controller/valkey_controller.go`](../../internal/controller/valkey_controller.go) — `setStatusCondition`, `persistStatus`, `writePhase`, `currentMasterPod`, `clearSidecarUpdatePending`
* [ADR 0001](0001-continue-reconciling-past-a-rejected-write.md) — why the pass reaches the status write at all
* [ADR 0003](0003-nudge-a-short-of-pods-statefulset.md) — the failure mode this condition deliberately does not cover
* [ADR 0011](0011-evidence-based-steady-state-split-brain-resolution.md) — why split-brain paths requeue instead of ending the pass
* [ADR 0015](0015-one-crd-validated-by-schema-only.md) — no admission webhook of our own; third-party ones are the environment
