---
id: T60
title: ADR 0020 D9 claims a ReconcileBlocked report that the rolling update's pod refusal never writes
state: filed
severity: low         # the refusal protects correctly; only its report on the CR is weaker than documented
security: hardening
threat: "no attack path: a pod whose generated name is held by a foreign pod is refused as ADR 0020 D9 intends; what is missing is the ReconcileBlocked/ForeignObject report, and with it the critical ValkeyReconcileBlocked alert, which would additionally flag such a collision"
urgency: later        # rule 4 since 2026-09-27: the ADR 0020 correction landed; option B is a costed, cheap known fix
effort: S             # the ADR correction is XS; the recommended option B is S
blocked-by: decision  # which option, below
filed-from: orchestrator verification during the ticket enrichment of 2026-09-27
opened: 2026-09-27
decided:              # not recorded - no decision yet
done:                 # not recorded - not done
---

# T60 - ADR 0020 D9 claims a ReconcileBlocked report that the rolling update's pod refusal never writes

## Fact

[ADR 0020](../adr/0020-write-only-what-the-operator-owns.md) D9 decides that the rolling update
refuses a pod whose generated name (`<cr>-N`, `<cr>-sentinel-N`) is held by a pod its StatefulSet
did not create, and fails instead of deleting it. It then ~~says~~ *(said, until work list item 1
on 2026-09-27; struck and corrected in place, History)*, at `0020:475-479`: "The rolling
update emits no Event of its own — its refusal already reaches the CR as
`ReconcileBlocked/ForeignObject` with the pod name in the message". The Consequences bullet at
`0020:569-573` ~~repeats~~ *(repeated)* it: such a cluster "now reports `ReconcileBlocked/ForeignObject` and phase
`Error`". The phase half is true. The condition half is not. *(2026-09-27: both places now say
phase `Error` and `status.message` only, and name the open question; the code is unchanged, so
the mechanism below still holds.)*

**Mechanism.**

- `ReconcileBlocked` has one evaluator, `setReconcileBlockedCondition`
  ([`reconcile_blocked.go:118`](../../internal/controller/reconcile_blocked.go#L118)), and one
  production caller,
  [`valkey_controller.go:276`](../../internal/controller/valkey_controller.go#L276), which passes
  `resourceErr`: the joined error of `reconcileResources` (`:275`). The registry row declares
  exactly that: a level, one evaluator, cleared by the same call
  ([`condition_registry.go:104-110`](../../internal/controller/condition_registry.go#L104-L110)).
- The rolling update is not a resource step. It runs in `reconcileWorkload` (`:284`), and its pod
  refusals return `foreignObjectError("Pod", …)`
  ([`foreign_object.go:76-78`](../../internal/controller/foreign_object.go#L76-L78)) as
  `RollingUpdateResult.Error`: data tier at
  [`rolling_update.go:282`](../../internal/controller/rolling_update.go#L282)
  (`dispatchDataRollingUpdate`), [`:1934`](../../internal/controller/rolling_update.go#L1934)
  (`collectPodStates`, which feeds `:705`, `:3085`, `:3878`, `:4725`),
  [`:3779`](../../internal/controller/rolling_update.go#L3779) (`handleStandaloneRollingUpdate`) and
  [`:4286`](../../internal/controller/rolling_update.go#L4286) (`handlePostManualFailover`);
  Sentinel tier at [`:4989`](../../internal/controller/rolling_update.go#L4989)
  (`scanSentinelPods`, returned at `:5058-5060`).
- The data-tier error reaches
  [`valkey_controller.go:336-339`](../../internal/controller/valkey_controller.go#L336-L339):
  `updatePhase(Error, "Rolling update error: …")` and a return. The Sentinel-tier error reaches
  [`:473-481`](../../internal/controller/valkey_controller.go#L473-L481):
  `updatePhase(Error, "Sentinel rolling update error: …")`. Neither touches `ReconcileBlocked`.
- A pod collision does not fail `reconcileResources`: `reconcileSidecarRole`
  ([`valkey_controller.go:1096`](../../internal/controller/valkey_controller.go#L1096)) emits the
  `PodNotOwned` Warning for the data pods it refuses (`:1102-1109`) and carries on without an
  error. So `resourceErr` is nil, and `:276` keeps or writes `ReconcileBlocked=False` (or writes
  nothing on a CR that never carried it,
  [`reconcile_blocked.go:121-131`](../../internal/controller/reconcile_blocked.go#L121-L131)).

What the CR shows for a pod collision: phase `Error`, `status.message` naming the pod, and
`ReconcileBlocked` absent or `False/ReconcileSucceeded`.

**Verified** (by reading at `4a7543e`):

- Everything above.
- The claim was false when it was written. Both sentences came in `995f186` (2026-08-22), and at
  that commit the evaluator was already called only with `resourceErr`
  (`git show 995f186:internal/controller/valkey_controller.go`, `:259`) and the rolling-update
  error path already wrote only the phase (`:319-321`).
- No test asserts the CR-level report. `TestCollectPodStates_RefusesAForeignPod`,
  `TestCheckAndHandleRollingUpdate_RefusesAForeignPod` and
  `TestCheckAndHandleSentinelRollingUpdate_RefusesAForeignPod`
  ([`foreign_object_test.go:1050-1103`](../../internal/controller/foreign_object_test.go#L1050-L1103))
  assert the returned error and that the pod survives.
- The alert contract follows the condition: `ValkeyReconcileBlocked` (critical, `for: 15m`) keys
  on it
  ([`prometheusrule.yaml:51-57`](../../deploy/helm/valkey-operator/templates/prometheusrule.yaml#L51-L57)),
  so it does not fire for a pod collision; `ValkeyPhaseNotOK` (warning, `for: 30m`, `:73-79`)
  does. The PrometheusRule is off by default.
- The Event half of the ADR paragraph holds ~~for data pods only~~ *(corrected 2026-09-27: at most
  for data pods; which foreign data pods `listDataPodNames` returns was not enumerated here)*. `reconcileSidecarRole` takes its
  list from `listDataPodNames`
  ([`valkey_controller.go:1061-1083`](../../internal/controller/valkey_controller.go#L1061-L1083)),
  which lists data pods; no other `warnForeignObject` call names a pod. A collision on a Sentinel
  pod name, refused at `rolling_update.go:4989`, therefore gets no Event and no condition.

**Not verified:**

- Nothing was run: no unit test through `Reconcile`, no Kind reproduction of a collision.
- [`docs/operations/status.md:37`](../operations/status.md#reconcileblocked) describes
  `ForeignObject` as "one of the generated names is held by an object this `Valkey` does not
  control". A reader will take pod names to be covered; the page does not say so explicitly, so
  it reads as imprecise rather than false. Not changed here.
- How fast the refusal is re-checked: it returns an error, so the controller-runtime rate limiter
  drives it, not the 30 s recheck D6 describes for resource-step refusals
  ([`foreign_object.go:61`](../../internal/controller/foreign_object.go#L61)). Not examined further.

## Impact

- **Contributors** ~~read~~ *(read, until work list item 1 on 2026-09-27)* in ADR 0020 that the pod
  door satisfies D5 ("A refusal is reported with its own ReconcileBlocked reason", `0020:321`). It
  does not, so D5 has an ~~unrecorded~~ exception *(recorded since 2026-09-27 as a Residual risk
  of ADR 0020, "The rolling update's pod refusal carries no condition", with the question left
  open)*.
- **Operators**: a pod-name collision does not fire the critical `ValkeyReconcileBlocked` alert.
  It shows only as phase `Error` and, after 30 minutes, the `ValkeyPhaseNotOK` warning; a
  Sentinel-pod collision also has no Event. The refusal itself works: no foreign pod is deleted,
  commanded or counted.
- **Security**: hardening. Detection of a collision is weaker than documented; nothing is
  permitted that D9 refuses.

## Options

Work list item 1 corrects the ADR under every option. The decision is what the operator should
report for a pod collision.

- **A — Accept and record.** On top of item 1, record in ADR 0020 Residual risks that the pod door
  is reported by phase and message only, state it as an exception to D5 in place, and add a
  half-sentence to `status.md:37`. Cost XS, no code. Leaves D5 not holding for one door, the
  critical alert blind to pod collisions, and Sentinel-pod collisions without an Event.
- **B — Report it through the one evaluator (recommended).** Add a resource step that proves every
  pod at an ordinal of the data and the Sentinel StatefulSet, by `Get` per ordinal the way
  `dispatchDataRollingUpdate` does
  ([`rolling_update.go:264-283`](../../internal/controller/rolling_update.go#L264-L283)), skips a
  StatefulSet that is absent or foreign (its own step reports that), and returns
  `foreignObjectError("Pod", name)` for a pod `podIsOurs` refuses. The error joins `resourceErr`,
  so `:276` writes `ReconcileBlocked=True/ForeignObject` with the pod name (`reconcileBlockedReason`
  ranks it first), the blocked pass makes its one `Error` phase write the authority, and the next
  clean pass clears the condition at the same call. The rolling update keeps its own refusal at
  the delete sites, where the object acted on is proven (ADR 0006). Cost S: one step on cached
  reads, a unit test through `Reconcile` that asserts the condition and its clear and fails with
  the step removed (ADR 0017), and ADR 0020 D9 and the Consequences bullet amended. The registry
  does not change: still one evaluator and one clear site (ADR 0027). What it changes: the phase
  message of a collision reads `Failed to reconcile resources: …` instead of
  `Rolling update error: …`, the alert fires after 15 minutes, and a Sentinel-pod collision is
  reported. It also reports the seconds between an orphaning StatefulSet delete and the
  re-adoption of its pods (the orphan-delete recovery ADR 0020 D1 names) as `ForeignObject`,
  well below the alert's 15 minutes; today the rolling update already refuses in that window,
  with a phase write only (not measured either way).
- **B' — A second writer (variant, loses to B).** Either the rolling update sets
  `ReconcileBlocked` itself, or the evaluator moves after `reconcileWorkload` and takes the
  workload's `errForeignObject` too. The first makes the condition flap: `:276` runs first in
  every pass and clears it (`reconcile_blocked.go:121-131`) before the roll sets it again, two
  status writes per pass, and ADR 0027 would need a second evaluator with an `ownershipRule`. The
  second keeps one evaluator but moves it against the blocked-pass marker and the phase writes
  that `TestReconcile_InitialPhaseWriteFailureStillReportsReconcileBlocked`
  ([`status_phase_test.go:330`](../../internal/controller/status_phase_test.go#L330)) and ADR 0002
  rest on: a larger blast radius than B for the same report.
- **C — Emit `PodNotOwned` from the rolling update.** Cost XS to S. It does not make the
  condition claim true (item 1 is still needed), adds a second Event reporter for data pods against
  the one-reporter rule of D8 and D9, and does not reach the alert. It would cover the Sentinel-pod
  Event. ADR 0025 D7 (no Warning on a clean roll) is not touched either way, because a collision is
  not a clean roll.

B is marked because D5 is a standing rule that every other door honours, and B satisfies it
through the existing single evaluator with no registry change, which makes the alert contract hold
as well. A is the fallback if one more step on every pass is judged too much for a rare collision.
C repairs the least.

## Work list

1. **XS, no decision needed**: correct ADR 0020 in place to what the code does today, true under
   every option. At `0020:477-479` strike "its refusal already reaches the CR as
   `ReconcileBlocked/ForeignObject` with the pod name in the message" and state that it reaches
   the CR as phase `Error` with the pod in `status.message` only, that `ReconcileBlocked` is
   evaluated from `reconcileResources` alone, and that the `PodNotOwned` Event covers data pods
   only. At `0020:570-571` strike "`ReconcileBlocked/ForeignObject` and" with a dated correction.
   Add a Status line "Amended 2026-09-27 (correction, no decision changes)" saying that whether
   the pod door is reported through `ReconcileBlocked`, as D5 asks, is open. No ticket citation in
   the ADR (ADR 0034). Does not close this ticket. **Done 2026-09-27**, and more than the item
   asked: a Residual-risks entry records the gap too (History). That entry is neutral between the
   options; under option A it is the entry A asks for, minus the D5 exception sentence and the
   `status.md:37` half-sentence.
2. **Waits on the decision**: the option's work as listed under Options.

## Decision

None yet.

## Verification

- Item 1: `git grep -n "already reaches the CR as" -- docs/adr` finds only struck text, and ADR
  0020's Status carries the dated correction. *(Run 2026-09-27 after the fix: one hit, ADR 0020
  `:489`, inside struck text; the Status line "Amended again 2026-09-27 (correction, no decision
  changes)" is at `:7-16`. Done.)*
- B: a unit test through `Reconcile` with a foreign pod at `<cr>-0` sees
  `ReconcileBlocked=True/ForeignObject` naming the pod and phase `Error`, and the pod survives;
  after the foreign pod is deleted the next pass clears the condition to `False`. The same test
  with a foreign pod at `<cr>-sentinel-0`. Both fail with the step removed. `make test-unit`,
  `make lint`, `make cyclo`.
- A: ADR 0020 Residual risks carries the entry and D5 names the exception.

## History

- 2026-09-27: urgency `now` -> `later` (rule 4, top-down): the ADR 0020 correction, the only rule-1 statement, landed; option B is a costed, cheap known fix. Applied as the History entry below derived it.
- 2026-09-27: work list item 1 landed, one file (read in `git diff` of the working tree):
  [ADR 0020](../adr/0020-write-only-what-the-operator-owns.md).
  - D9, its paragraph "One reporter, and it is not the rolling update" (`:486-499` now): "its refusal already reaches the CR as `ReconcileBlocked/ForeignObject`
    with the pod name in the message" struck and corrected in place - phase `Error` with the pod
    in `status.message` (`Rolling update error: …`, `Sentinel rolling update error: …`), on a
    pass the resource step also blocks not even there, `ReconcileBlocked` evaluated from
    `reconcileResources` alone so the `ValkeyReconcileBlocked` alert does not fire, never true
    since the paragraph was written, and no Event for a colliding Sentinel pod; "one Event series
    per pass" became "at most one".
  - The Consequences bullet (`:589-591` now): "`ReconcileBlocked/ForeignObject` and" struck.
  - Status: a dated "Amended again 2026-09-27 (correction, no decision changes)" line at the top,
    newest first, stating that the reporting question is open.
  - Residual risks: a new entry, "The rolling update's pod refusal carries no condition", with
    the question open and no option taken.

  Neither option is decided in the ADR text. The decision (B recommended) and item 2 are
  untouched. **Urgency not recomputed in this pass** (the orchestrating run left every urgency
  but one to the owner): item 1 was the only rule-1 statement, so a top-down re-derivation gives
  `later` (rule 4: option B is a costed, cheap known fix). **Not verified:** nothing was run.
- 2026-09-27: adversarial review of the filing. Re-read at `4a7543e`: `valkey_controller.go:276`,
  `:336-339`, `:473-481`, `reconcile_blocked.go:118-131`, `foreign_object.go:61`, `:76-78`, the
  five `foreignObjectError("Pod", …)` sites, ADR 0020 `:321`, `:475-479`, `:570` and
  `prometheusrule.yaml:51-58` (severity `critical`) and `:73-79` hold. Two changes: the
  Event-half bullet read as if the `PodNotOwned` Event covered every foreign data pod, which
  was not enumerated (narrowed to "at most for data pods", struck and corrected in place). The bullet
  that referred to an embargoed ticket was **removed, not struck**, under the
  [embargo rule](README.md#an-open-security-finding-is-embargoed); striking would have kept it.
  The file was untracked, so no commit carried it. Recommendation B stands. **Verified:** by reading. **Not
  verified:** nothing was run.
- 2026-09-27: filed during the ticket enrichment. The orchestrator verified by reading that ADR
  0020 D9 and its Consequences bullet claim a `ReconcileBlocked/ForeignObject` report that the
  rolling update never writes; this file re-verified it at `4a7543e`, dated the claim to
  `995f186`, and added the Sentinel-pod Event gap. Urgency `now` by rule 1. **Verified:** by
  reading, grep and `git show`. **Not verified:** nothing was run.
