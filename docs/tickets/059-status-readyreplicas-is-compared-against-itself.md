---
id: T59
title: status.readyReplicas is compared against itself, so it reaches the CR only when another status field changes with it
state: analysed       # every fact verified by reading, options costed with a marked best
severity: low         # the Ready condition carries the count on every operator write; a third-party status write is never corrected
security: none        # only the operator ClusterRole grants valkeys/status; a principal who can forge the count can forge every status field
urgency: now          # rule 1: two sentences in ADR 0002 are false by reading (independent item 1); later (rule 4) once they are corrected
effort: S             # one capture moved, two callee signatures, one regression test, ADR 0002 amendment and a few doc and comment edits
blocked-by: decision  # ADR 0002 accepts the masking as a residual risk; taking the fix is judging it too fragile to keep
filed-from: the documentation restructure (review of ADR 0002 D5 and its residual risks)
opened: 2026-09-27
decided:
done:
---

# T59 - status.readyReplicas is compared against itself, so it reaches the CR only when another status field changes with it

## Current state

**Mechanism.** `updateStatus` re-reads the CR
([`valkey_controller.go:2204-2206`](../../internal/controller/valkey_controller.go#L2204-L2206))
and assigns `v.Status.ReadyReplicas` from the data StatefulSet at
[`:2213-2214`](../../internal/controller/valkey_controller.go#L2213-L2214), before
`updateStandaloneStatus` ([`:2228`](../../internal/controller/valkey_controller.go#L2228)) and
`updateHAStatus` ([`:2451`](../../internal/controller/valkey_controller.go#L2451)) each capture
`prevStatus`. So `statusUnchanged` compares the count with itself
([`:2583`](../../internal/controller/valkey_controller.go#L2583)), and if nothing else differs
`persistStatus` ([`:2548`](../../internal/controller/valkey_controller.go#L2548)) skips the write
([`:2566-2568`](../../internal/controller/valkey_controller.go#L2566-L2568)). `observerReady` and
`operatorVersion` are assigned inside `persistStatus` after the capture
([`:2554-2564`](../../internal/controller/valkey_controller.go#L2554-L2564)); ADR 0002 D5 moved
`observerReady` there after the same defect left it wrong on a live fleet, and left
`readyReplicas` behind as an accepted residual risk
([`0002:491-529`](../adr/0002-surface-a-blocked-reconcile-on-the-cr.md)). The only guard of the
window between the refresh `Get` and the captures is the NOTE comment at
[`:2208-2211`](../../internal/controller/valkey_controller.go#L2208-L2211). Nothing else writes
`v.Status` in that window.

**Why the count still reaches the CR today (the masking).** The `Ready` condition, written by
the same `persistStatus` call, fixes the count in every branch:

- Standalone ([`:2234-2284`](../../internal/controller/valkey_controller.go#L2234-L2284)) and HA
  ([`:2457-2526`](../../internal/controller/valkey_controller.go#L2457-L2526)): all-ready reasons
  imply count = `spec.replicas` of that generation, partly-ready `Ready` messages name the count
  (`:2271`, `:2512-2513`), none-ready implies 0. `spec.replicas` has `Minimum=1`
  ([`valkey_types.go:480`](../../api/v1/valkey_types.go#L480)), so the branches are disjoint.
- Inside an all-ready branch the count changes only with `spec.replicas`, which bumps the
  generation, and `meta.SetStatusCondition` copies the new `ObservedGeneration` onto the
  condition.
- On a blocked pass `persistStatus` restores the previous phase and message
  ([`:2549-2552`](../../internal/controller/valkey_controller.go#L2549-L2552)); the `Ready`
  condition alone still suffices. The phase message is never the only field that moves with
  the count.
- The only status writers are `persistStatus`, `writePhase` (`:2628`) and
  `writeStatusCondition` (`:2709`); the latter two send the stored `Ready` and count back
  unchanged, and no other code sets `ConditionTypeReady`.

So while only the operator writes status, the count cannot go stale. The first pass of a new
release always writes, because `operatorVersion` changes (`:2555`).

**Where it breaks.** A count written by someone other than the operator is overwritten in memory
at `:2214` before the capture, compares equal, and stands until another compared field changes.
The CR watch is generation-gated
([`:2987`](../../internal/controller/valkey_controller.go#L2987)), so a status-only edit starts no
pass. The masking also breaks if a `Ready` reason is shared by two branches with different
counts, a partly-ready `Ready` message stops naming the count, or a new branch's `Ready`
condition does not fix the count. No test pins any of this:
`TestUpdateStatus_KeepsNonPhaseFieldsWhileBlocked` (`status_phase_test.go:205`) and
`TestReconcile_BlockedPassDoesNotFlapPhase` (`:84`) move the count together with `Ready`,
`TestUpdateHAStatus_KeepsReadyTrueWhileBlocked` (`:243`) asserts no count, and
`TestStatusUnchanged_DetectsChanges` (`valkey_controller_test.go:854`) tests the helper, not the
order.

**Impact.** The stored count feeds the `Ready` printer column
([`valkey_types.go:1133`](../../api/v1/valkey_types.go#L1133)), the gauge
`vko_valkey_status_ready_replicas`
([`collector.go:195-196`](../../internal/metrics/collector.go#L195-L196)) and the alert
`ValkeyReplicasMissing` (`spec - ready > 0` for 15m,
[`prometheusrule.yaml:87-94`](../../deploy/helm/valkey-operator/templates/prometheusrule.yaml)).
A value stuck too high keeps the alert silent on a cluster short of pods; stuck too low it fires
on a complete cluster. No operator decision reads the stored count. ADR 0026
([`0026:628-633`](../adr/0026-a-pod-being-deleted-is-not-available.md)) promises that the alert
sees a data-tier stall; in that pass the count reaches the CR only through the masking.

**ADR 0002 states two false sentences.** [`0002:522-527`](../adr/0002-surface-a-blocked-reconcile-on-the-cr.md)
says both blocked-pass tests assert the count; there are three tests, and the HA one asserts no
count (the conclusion, no test isolates the count, holds). `:519-521` implies a phase-message
change can reopen the defect; it cannot.

## Required changes

### Independent of the open questions

1. ADR 0002: strike `:522-527` and `:519-521` in place and restate them (three blocked-pass
   tests and what each asserts; only the `Ready` condition's encoding carries the count), with a
   dated correction note in its Status.

### Depends on the answers

2. The chosen option's code change, regression test and documentation edits, as listed under
   Q1. Code comments and tests cite ADR 0002 D5, never T59 (ADR 0034).
3. Close: the decision is in ADR 0002, `reconcile-loop.md` carries the subsystem knowledge, the
   ticket moves to `archive/` (under C: the residual-risk edit, then `dropped`).

**Regression test (A-prime and A).** Seed a stored status whose `readyReplicas` disagrees with the
StatefulSet while every other field is converged (`reconcileFor`, then `c.Status().Update` with
only the count changed, pattern at
[`status_phase_test.go:213-216`](../../internal/controller/status_phase_test.go#L213-L216)), run
`updateStatus` on a blocked and an unblocked context, assert the write both times, one case per
topology. It must fail on today's order and pass after the fix (ADR 0017 revert check).

**Verification.** `make test-unit`, `make lint`, `make cyclo` green; ADR 0002 D5 passage
(`:140-147`), D5 sentence (`:156`) and both residual-risk entries (`:491-501`, `:502-529`) marked
superseded in place, Status amended, the index row updated if the State moves;
`git grep -n 'readyReplicas' -- ':!docs/tickets'` finds no unmarked statement of the masking.

## Open questions

### Q1: Take the fix, and in which shape, or refuse it?

The count is correct today only because the `Ready` condition encodes it, which nothing tests,
and a third-party count is never corrected. Both fixes are behaviour-neutral for the fleet: they
write exactly when today's operator writes for every status only the operator wrote, with no
extra write, no roll and nothing on upgrade.

- **A-prime - move the capture (recommended).** Take `prevStatus := v.Status.DeepCopy()` once in
  `updateStatus` directly after the refresh `Get` (`:2204-2206`), pass it to both callees in
  place of their captures at `:2228` and `:2451`; `:2214` stays. Cost S: two callee signatures
  and the call sites `:2218`, `:2222`; the regression test; ADR 0002 amended (`:43-49`,
  `:140-147`, `:156`, `:491-529` superseded, noting the fix is a moved capture, not the moved
  assignment the ADR named); `reconcile-loop.md` `:149-150` and `:158-164`; the NOTE at
  `:2208-2211`; the `persistStatus` doc comment rationale (`:2539-2547`); `CLAUDE.md:579`
  generalised from "next to `OperatorVersion`" to "after the capture". Check: exactly one
  `prevStatus := v.Status.DeepCopy` in `valkey_controller.go`, directly after the refresh `Get`.
- **A - move the assignment.** Delete `:2214`, set the count directly after the captures at
  `:2228` and `:2451`. Cost S, the smallest diff: the regression test, the same ADR 0002 places,
  `reconcile-loop.md`, the NOTE; `persistStatus` rationale and `CLAUDE.md:579` stay true. Leaves
  the prologue window guarded only by the NOTE comment, which was already missed once.
- **C - refuse the fix.** Keep ADR 0002's residual risk, replace its Not-verified sentence
  (`0002:521-522`) with the encoding argument, record the refusal, drop this ticket. Cost XS. The
  count keeps resting on untested `Ready` strings, and a third-party count stays wrong.

A-prime because the defect happened twice in the same place, a prologue assignment between the
refresh `Get` and a capture in another function; A-prime removes that window so a third field
cannot repeat it, while A removes one field and keeps a comment as the only guard.

**Answer:** _open_

## Not verified

- Whether any tool in the production fleet (for example a backup restore) writes the
  `valkeys/status` subresource; settled by an audit of its writers on the fleet API servers.
- That the regression test fails today and passes after the fix rests on reading; running it
  both ways settles it.

## Related

- [T18](018-ready-keeps-its-pre-roll-value-during-a-rolling-update.md) - passes that skip
  `updateStatus` during a roll keep the pre-roll count; independent, no option here changes them.
- [T35](035-master-records-lag-the-real-master.md) - decision B2 adds a write after
  `persistStatus` in the tail of `updateHAStatus`; textual overlap only.
- [T78](078-the-status-write-conflicts-with-the-operators-own-earlier-write.md) - `persistStatus`
  409 on a cached refresh; its options keep the refresh `Get` in place and compose with A-prime
  and A in either order.
- [T40](040-tracked-files-cite-work-items-instead-of-adrs.md) - comments cite ADRs, not tickets.
