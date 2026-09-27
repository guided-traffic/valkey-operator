# Ticket: Enforce the three standing constraints with a static-analysis test net

Ticket 042, formerly S1 (`local_standing_constraints_enforcement.md`); renamed on 2026-09-27 when
the tickets were numbered.

> **Status: OPEN, nothing built. Verified 2026-08-26 on `HEAD` = `1c309d8`.** Index:
> [`archive/039-findings-from-the-1-11-0-fleet-rollout.md`](archive/039-findings-from-the-1-11-0-fleet-rollout.md) (archived 2026-09-27, no longer maintained). Keep this line current — update it
> in the same change that touches this ticket.
>
> `internal/controller/standing_constraints_test.go` **does not exist**, and no equivalent
> exists: the only other `go/ast`-based test in the tree is
> [`condition_registry_test.go:2-6`](../../internal/controller/condition_registry_test.go#L2-L6),
> which enforces a different rule. The precedent this ticket cites,
> [`rbac_drift_test.go`](../../internal/controller/rbac_drift_test.go), is there.
>
> **All three constraints still hold today — this is preventive work, not a repair:**
>
> * **Constraint 1** (no fleet-wide reconciler state): the baseline scan comes out **exactly
>   as this ticket predicts**. Non-test files in `internal/controller` have three
>   package-level `var`s and no unexpected mutable state:
>   [`condition_registry.go:84`](../../internal/controller/condition_registry.go#L84) (a
>   constant-like table), [`foreign_object.go:71`](../../internal/controller/foreign_object.go#L71)
>   and [`volumeclaim_conflict.go:46`](../../internal/controller/volumeclaim_conflict.go#L46)
>   (two sentinel errors). Seeding the allowlist is therefore cheap and surfaces **no
>   findings** — the stop-and-discuss point this ticket builds in is very likely a no-op.
> * **Constraint 2** (no metric written from a reconcile pass): no `prometheus`/`promauto`
>   import anywhere in `internal/controller` or `internal/sidecar`.
> * **Constraint 3** (a new managed kind inherits the ownership guard): **64** non-test
>   client-verb / `SetControllerReference` call sites against **76** guard-identifier
>   references. This is the real work in the ticket; tests 1 and 2 are a few hours together.
>
> **Effort: M (1–2 days).** Zero production code changes. It slips to L only if allowlist
> seeding for constraint 3 exceeds ~15 entries, which this ticket already makes a
> stop-and-discuss point rather than something to pad through.
>
> **Why it is worth doing even though nothing is broken:** constraint 1 is the safety
> argument for `--max-concurrent-reconciles=4`. One future package-level map breaks
> fleet-wide isolation with no test going red and no symptom until two CRs interleave in
> production. Both existing precedents (ADR 0014, ADR 0027) were written *after* their
> convention had already been broken; this is the same net *before*. It also turns "a new
> managed kind inherits the guard, not an exemption" into a diff line review can catch,
> which is the precondition for adding managed kinds without a per-kind provenance audit.
>
> **Not verified:** `make test-unit` / `make lint` / `make cyclo` were not run for this
> assessment — every claim above is a static read of the tree.

## Why

Three load-bearing rules in this repo are **standing constraints on future code**, not
enforced invariants. Today they hold through discipline and review only:

1. **No fleet-wide reconciler state** (ADR 0019 D3). Reconcile concurrency is 4; it is only
   safe because no state in `internal/controller` is shared across CRs — the `nudgeTracker`
   keys carry namespace and CR name, the blocked-pass marker rides on the context, and there
   is no package-level mutable state. One future package-level map breaks this silently.
2. **No metric written from a reconcile pass** (ADR 0021). The operator's `vko_valkey_*`
   series come from a collect-time collector over the manager cache
   (`internal/metrics/collector.go`), which is what makes deleted resources stop producing
   series with zero bookkeeping. One future `prometheus.NewGaugeVec(...).Set(...)` inside a
   reconcile step reintroduces the deletion-bookkeeping problem and the stale-series bug.
3. **A new managed kind inherits the ownership guard, not an exemption** (ADR 0020 D1/D2,
   ADR 0006). Every write and delete on a generated name goes through
   `metav1.IsControlledBy` / `deleteIfOwned` / `podIsOurs` today. Nothing stops a future
   `reconcileNewThing` from calling `r.Update` on the strength of a name.

The RBAC triple has exactly this kind of net (`rbac_drift_test.go` asserts generated ⊆ chart
and names the missing triple; ADR 0014 explains why a test beats a convention). These three
constraints have no equivalent. This ticket adds one.

## What to build

One new file, `internal/controller/standing_constraints_test.go`, containing three ordinary
Go tests that parse the package source with `go/parser` + `go/ast` (no new dependencies —
stdlib only, same style of "test as lint" as `rbac_drift_test.go`). They run under
`make test-unit` and therefore in CI with no pipeline change.

### Test 1: `TestNoPackageLevelMutableState`

Parse every non-test file in `internal/controller`. Fail on any package-level `var` whose
declared or inferred type is mutable (map, slice, chan, pointer, or a struct type — anything
a concurrent reconcile pass could observe another pass through).

Allowlist, checked by variable name and asserted immutable-by-use:

- sentinel errors created with `errors.New` (e.g. `errForeignObject`) — written once at init,
  only ever compared;
- any true constant-like `var` that cannot be `const` (e.g. a precomputed table), listed
  explicitly with a one-line justification string in the test.

The failure message must name the variable, the file and the rule
("package-level mutable state breaks MaxConcurrentReconciles > 1 — see ADR 0019 D3;
carry the state per-CR (keyed by namespace/name) or per-pass (on the context) instead").

Known accepted state to seed the allowlist from the current tree: check first whether any
exists at all — as of 2026-08-22 the expectation is that the list is empty apart from
sentinel errors. If the scan finds something not on that list, that is a finding of this
ticket, not something to silently allowlist: surface it to Hans before proceeding.

### Test 2: `TestNoMetricsWrittenFromReconcile`

Parse the imports of every non-test file in `internal/controller` (and `internal/sidecar`,
which has the same no-CR-access shape and must not grow a registry either). Fail if any file
imports `github.com/prometheus/client_golang/prometheus` or
`.../prometheus/promauto`.

Metric registration and emission belong in `internal/metrics` only. The failure message
points at ADR 0021 and states the alternative: "add the series to the collect-time collector
in internal/metrics/collector.go; never write a gauge from a reconcile pass".

No allowlist. If a legitimate need ever arises, widening the test *is* the review hook.

### Test 3: `TestEveryWriteAndDeleteIsGuardedOrAllowlisted`

The ambitious one, and the one that pays for the ticket. Scope it precisely:

- Walk every function in `internal/controller` (non-test files). Collect every call site of
  the reconciler's client verbs: `r.Create`, `r.Update`, `r.Patch`, `r.Delete`, and the
  `controllerutil.SetControllerReference` stamp (a write like any other — it decides what the
  garbage collector takes with the CR, ADR 0020 NA62).
- For each call site, require **either** that the enclosing function's body (directly, not
  transitively) references at least one guard identifier —
  `IsControlledBy`, `deleteIfOwned`, `deleteOwnedPod`, `podIsOurs`, `podUnderNameIsOurs`,
  `ownedDataStatefulSet`, `filterOwnedPods` — **or** that the `(function, verb)` pair is on
  the explicit allowlist.
- The allowlist is a `map[string]string` in the test: key `"funcName/verb"`, value a
  one-line justification. Legitimate entries fall into known families:
  - writes to the CR itself and its status (`updateStatus`, `updatePhase`,
    `setStatusCondition`, annotation writes on `v` — the CR is the owner, guards do not
    apply);
  - pod writes/deletes in functions whose *caller* proved provenance and passed the proven
    object in (e.g. `deleteOwnedPod` itself; the rolling-update helpers that receive an
    already-filtered pod) — the justification names the proving caller;
  - Event emission if it surfaces as a client verb.
- Seed the allowlist from the current tree. Every seeded entry must be justifiable from the
  guard architecture (ADR 0020 D8's one-reporter rule, the caller-proves pattern of
  `podIsOurs`). An entry that cannot be justified is, again, a finding — surface it.

The point is not proving correctness (a guard on the wrong object still passes). The point
is the same as the RBAC drift test: **a new unguarded write cannot land without either
adding the guard or adding a visible, justified allowlist line** — and that diff line is
what review catches. State this explicitly in the test's doc comment so nobody mistakes it
for a soundness proof.

Direct-body-only matching (no call-graph transitivity) is a deliberate simplification: it
produces some allowlist entries for helper functions, and each of those entries documents a
caller-proves relationship that is currently implicit. That is a feature. If the allowlist
comes out longer than ~15 entries, stop and reconsider the matching depth with Hans before
padding it.

## Constraints on the implementation

- Stdlib only (`go/parser`, `go/ast`, `go/token`). No new module requirements.
- Cyclomatic complexity < 15 per function — the AST walk wants small named helpers anyway.
- No `testing.Short()` gate (repo rule, CLAUDE.md).
- Failure messages must name file, function/variable, and the ADR that carries the rule —
  modelled on `rbac_drift_test.go`, which names the missing triple.
- English, like everything else.

## ADR updates (same change, per CLAUDE.md)

- **ADR 0019**: the "standing constraint, not a one-time audit" sentence in D3 gains a
  pointer: the constraint is now backed by `TestNoPackageLevelMutableState`. Status note
  with date.
- **ADR 0021**: same for the no-gauge-from-reconcile rule and
  `TestNoMetricsWrittenFromReconcile`.
- **ADR 0020**: same for D1/D2 inheritance and `TestEveryWriteAndDeleteIsGuardedOrAllowlisted`,
  including one honest sentence on what the test does **not** prove (guard-on-wrong-object,
  transitivity). Residual-risks entry for the allowlist mechanism: an unjustified allowlist
  line is the new way to defeat the net, and review of that line is the defence.
- `CLAUDE.md`: one line under the reconcile-concurrency and metrics sections each, pointing
  at the tests, so the next agent finds the net before re-deriving the rule.

## Verification (Definition of Done)

Mutation checks, per the repo's revert-check policy (ADR 0017):

1. `make test-unit` green on the unmodified tree with the new tests active.
2. **Mutation 1**: add `var passCounter = map[string]int{}` at package level in
   `internal/controller` → `TestNoPackageLevelMutableState` fails and names it. Revert.
3. **Mutation 2**: add a `prometheus` import to `valkey_controller.go` →
   `TestNoMetricsWrittenFromReconcile` fails. Revert.
4. **Mutation 3**: comment out the `IsControlledBy` check in one guarded reconcile function
   (e.g. `reconcileConfigMap`) → `TestEveryWriteAndDeleteIsGuardedOrAllowlisted` fails for
   exactly that function/verb pair. Revert.
5. **Mutation 4**: add a fictional `reconcileWidget` with a bare `r.Update` → same test
   fails; adding an allowlist line makes it pass (proving the review-hook mechanism). Revert.
6. `make lint` and `make cyclo` green.
7. Report the seeded allowlist and any findings from seeding to Hans before finishing.

## Explicitly out of scope

- Enforcing `findMaster`'s ordinal-indexed collection (ADR 0019's second half) — semantic,
  not lintable at this level.
- Verifying guards act on the *right* object — that stays with unit tests and review.
- The chart/RBAC net — exists (`rbac_drift_test.go`).
- Any change to reconcile behaviour. This ticket adds tests and doc pointers only.
