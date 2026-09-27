---
id: T59
title: status.readyReplicas is compared against itself, so it reaches the CR only when another status field changes with it
state: filed
severity: low         # no stale value was constructed by reading; the masking holds on strings nothing tests
security: none
urgency: later        # rule 4: option A is a cheap known fix; no false statement stands, severity below medium
effort: S             # A: a two-line move, one unit test, the ADR 0002 amendment and one developer-page paragraph
blocked-by: decision  # ADR 0002 accepts the masking as a residual risk; taking the fix is judging it too fragile to keep
filed-from: the documentation restructure of 2026-09-27 (review of ADR 0002 D5 and its residual risks)
opened: 2026-09-27
decided:              # not recorded - no decision yet
done:                 # not recorded - not done
---

# T59 - status.readyReplicas is compared against itself, so it reaches the CR only when another status field changes with it

Filed on 2026-09-27 out of the review passes of the documentation restructure. A reviewer of
[docs/developer/reconcile-loop.md](../developer/reconcile-loop.md) found that the reason
[ADR 0002](../adr/0002-surface-a-blocked-reconcile-on-the-cr.md) gave for leaving
`readyReplicas` where it is ("every branch's phase message is a function of the ready count")
was imprecise. The ADR was corrected the same day: its Status now has an "Amended again
2026-09-27" paragraph, the D5 amendment is struck and restated, and Residual risks has a new
entry. The open decision in that correction is carried here. Everything below was read in the
working tree of `feat/rootless` on 2026-09-27 (`HEAD` = `f5c6886`; `git diff --stat HEAD` shows
no change under `internal/controller/` except `volumeclaim_conflict.go`, so the cited status
code is the committed code). No make target and no cluster was run for this ticket.

## Fact

**Mechanism.** `updateStatus` assigns `v.Status.ReadyReplicas` from the data StatefulSet at
[`valkey_controller.go:2213-2214`](../../internal/controller/valkey_controller.go#L2213-L2214).
That happens *before* `updateStandaloneStatus`
([`:2228`](../../internal/controller/valkey_controller.go#L2228)) and `updateHAStatus`
([`:2450`](../../internal/controller/valkey_controller.go#L2450)) capture `prevStatus`.
`statusUnchanged` compares `prev.ReadyReplicas` with `curr.ReadyReplicas`
([`:2582`](../../internal/controller/valkey_controller.go#L2582)), but both hold the same value,
so that comparison can never fail. If nothing else differs, `persistStatus`
([`:2547`](../../internal/controller/valkey_controller.go#L2547)) skips the write. The count
reaches the stored CR only when some other field forces the write.

**Verified:**

- **The order above**, and that `observerReady` and `operatorVersion` are assigned inside
  `persistStatus` after the capture
  ([`:2553-2563`](../../internal/controller/valkey_controller.go#L2553-L2563)). That is where
  ADR 0002 D5 moved `observerReady` on 2026-08-26, when the same defect had left it wrong on six
  of the eight observer-enabled clusters of a live fleet (ADR 0002 D5, amendment of
  2026-08-26). `readyReplicas` was left behind by decision (same ADR, Residual risks).
- **On a blocked pass the phase message carries nothing.** `persistStatus` puts
  `prevStatus.Phase` and `prevStatus.Message` back when `passIsBlocked`
  ([`reconcile_blocked.go:171`](../../internal/controller/reconcile_blocked.go#L171)) is true
  ([`:2548-2551`](../../internal/controller/valkey_controller.go#L2548-L2551)), and it does so
  before `statusUnchanged` runs. The fields left to force a write are the conditions,
  `masterPod`, `operatorVersion` and `observerReady`. Of those, only the `Ready` condition moves
  with the count.
- **Which string moves with the count, branch by branch.** Standalone
  ([`:2234-2284`](../../internal/controller/valkey_controller.go#L2234-L2284)):
  - All ready (count = `spec.replicas`): the phase message is `All replicas are ready` or
    `Instance unreachable: …`. The `Ready` reason is `AllReplicasReady` or
    `ConnectivityCheckFailed`. No count appears.
  - Partly ready: the phase message and the `Ready` message both name the count.
  - None ready (count 0): no count appears.

  HA ([`:2456-2525`](../../internal/controller/valkey_controller.go#L2456-L2525)):
  - All ready: the OK phase message names the count. The Error and Syncing messages do not;
    Syncing names the health check's synced counts, not the StatefulSet's ready count. The
    `Ready` message never names the StatefulSet count.
  - Partly ready: both name it.
  - None ready: neither does.

  So a changed count changes the branch, which changes the `Ready` reason or status, or it
  stays in a partly-ready branch, which changes the `Ready` message. Inside an all-ready branch
  the count can change only together with `spec.replicas`. That bumps the generation, and
  `meta.SetStatusCondition` copies the new `ObservedGeneration` onto an existing condition
  (`k8s.io/apimachinery@v0.37.0/pkg/api/meta/conditions.go:62-63`, read in the module cache).
- **The stored value has three consumers:**
  - the `Ready` printer column
    ([`valkey_types.go:1131`](../../api/v1/valkey_types.go#L1131));
  - the gauge `vko_valkey_status_ready_replicas`
    ([`collector.go:195-196`](../../internal/metrics/collector.go#L195-L196));
  - the alert `ValkeyReplicasMissing`, which fires on `spec - ready > 0` held for `15m`
    ([`prometheusrule.yaml:87-94`](../../deploy/helm/valkey-operator/templates/prometheusrule.yaml)).
- **No test isolates the count.** The two blocked-pass tests in
  [`status_phase_test.go`](../../internal/controller/status_phase_test.go):
  - `TestUpdateStatus_KeepsNonPhaseFieldsWhileBlocked` (`:205`) marks the StatefulSet ready
    after the first pass, so the count and the `Ready` condition change together.
  - `TestUpdateHAStatus_KeepsReadyTrueWhileBlocked` (`:243`) does not assert `readyReplicas` at
    all.

**Not verified:**

- **That the count can never go stale today.** No case was found by reading, but nothing was
  measured and no test was written.
- **Whether the stored status can hold a count that disagrees with an otherwise converged
  status.** Such a status could come from an earlier operator version or from a write by
  someone other than the operator. Today's code would never correct it, because no compared
  field would change. Not examined.
- **How this interacts with [018](018-ready-keeps-its-pre-roll-value-during-a-rolling-update.md).**
  The mechanism there is different: during a roll, `updateStatus` is not reached at all, so the
  count keeps its pre-roll value whatever the assignment order is. This ticket does not change
  that.

## Impact

No operator hits this today, as far as reading shows. The coupling breaks in two cases: when a
phase message or a `Ready` string stops naming the count, or when a branch is added whose
strings do not change with the count. From then on the stored `readyReplicas` stays at the value
of the last write that some other field caused, and nothing reports it. The printer column, the
gauge and `ValkeyReplicasMissing` all read the stored value:

- a value stuck too high keeps the alert silent on a cluster that is short of pods;
- a value stuck too low fires the alert on a cluster that is complete.

`observerReady` showed this exact defect class on a live fleet. Its three seconds of real lag
became a value that stayed wrong (ADR 0002 D5).

## Options

**A — Move the assignment after the capture (recommended).** Delete the assignment at
`:2214` and set `v.Status.ReadyReplicas = readyReplicas` in `updateStandaloneStatus` and
`updateHAStatus`, right after their `prevStatus` capture (or in `persistStatus`, next to
`observerReady`, with the count as a parameter). Add a unit test that fails against today's
order. The test seeds a stored status whose `readyReplicas` disagrees with the StatefulSet while
every other field is already at its converged value, runs `updateStatus` once on a blocked pass
and once on an unblocked pass, and asserts the write both times. Then amend ADR 0002: strike the
two Residual-risks entries on `readyReplicas` and the D5 sentence as superseded, and add a dated
Status entry. Also update the persistStatus paragraph of
[reconcile-loop.md](../developer/reconcile-loop.md#the-status-write), which states the
masking. Cost: two lines of code, one test, the ADR amendment and one paragraph.

**B — Keep the masking and test it.** Write a table test that changes only the count in each
branch of both functions and asserts that the stored status changes. It turns red when a string
stops carrying the count. Cost: one test and no code. But it pins message strings, a new branch
that is missing from the table escapes it, and the count still depends on those strings.

**C — Leave it as is.** ADR 0002 Residual risks accepts this today. It costs nothing now, and
nothing tests the risk.

**A is marked best.** It removes the dependency instead of pinning it, and it costs about what
B costs. It gives the field the shape that ADR 0002 D5 already gave `observerReady`, and that
`CLAUDE.md` states for status fields: the assignment goes on the far side of the `prevStatus`
capture. Its regression test can fail against today's order (the seeded-stale case). B can only
fail when a string changes. B beats C only because it makes the fragility visible.

## Decision

None yet.

## Verification

- The new unit test fails against today's assignment order and passes after the move (the
  revert check of [ADR 0017](../adr/0017-test-and-ci-policy.md)).
- `make test-unit`, `make lint` and `make cyclo` are green.
- ADR 0002: the D5 sentence and both Residual-risks entries on `readyReplicas` are marked
  superseded in place, and Status is amended with the date. The ADR's row in
  [docs/adr/README.md](../adr/README.md) changes in the same change if its State moves.
- [reconcile-loop.md](../developer/reconcile-loop.md) no longer says that `readyReplicas` is
  assigned before the capture.
- `git grep -n 'readyReplicas' -- ':!docs/tickets'` finds no other statement of the masking.

## History

- 2026-09-27: filed out of the documentation restructure. The imprecise argument in ADR 0002 was
  corrected in that ADR the same day. This ticket carries the decision the correction left
  open: whether to take the fix that the ADR names and has not taken.
