# Ticket: Enforce the three standing constraints with a static-analysis test net

Ticket 042, formerly S1 (`local_standing_constraints_enforcement.md`); renamed on 2026-09-27 when
the tickets were numbered.

> **Status: OPEN, nothing built. Verified 2026-08-26 on `HEAD` = `1c309d8`; re-verified
> 2026-09-27 on `HEAD` = `4a7543e`, corrections in place below. One decision is open
> ([Options](#options)); tests 1 and 2 need none ([Work list](#work-list)). Urgency: `later`
> *(re-derived 2026-09-27, see History; the board row had `icebox`)*. Effort: M.** Index:
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
>   as this ticket predicts**. Non-test files in `internal/controller` have ~~three~~
>   *(corrected 2026-09-27: five — ADR 0033 added two sentinel errors,
>   [`pod_hardening.go:22`](../../internal/controller/pod_hardening.go#L22) and
>   [`:46`](../../internal/controller/pod_hardening.go#L46))*
>   package-level `var`s and no unexpected mutable state:
>   ~~[`condition_registry.go:84`](../../internal/controller/condition_registry.go#L84)~~
>   [`condition_registry.go:92`](../../internal/controller/condition_registry.go#L92)
>   *(line corrected 2026-09-27)* (a
>   constant-like table), [`foreign_object.go:71`](../../internal/controller/foreign_object.go#L71)
>   and [`volumeclaim_conflict.go:46`](../../internal/controller/volumeclaim_conflict.go#L46)
>   (two sentinel errors). Seeding the allowlist is therefore cheap and surfaces **no
>   findings** — the stop-and-discuss point this ticket builds in is very likely a no-op.
>   *(Added 2026-09-27: [ADR 0019](../adr/0019-reconcile-concurrency-and-the-cost-of-a-stuck-pass.md)
>   D3 states the rule for all of `internal/` (`0019:80`); outside `internal/controller` the one
>   package-level `var` is the table
>   [`internal/builder/tls_material.go:41`](../../internal/builder/tls_material.go#L41).)*
> * **Constraint 2** (no metric written from a reconcile pass): no `prometheus`/`promauto`
>   import anywhere in `internal/controller` or `internal/sidecar`.
> * **Constraint 3** (a new managed kind inherits the ownership guard): ~~**64**~~ non-test
>   client-verb / `SetControllerReference` call sites against ~~**76**~~ guard-identifier
>   references *(corrected 2026-09-27: 71 sites — 65 client-verb, `Status().Update` and
>   `SetControllerReference` calls plus 6 calls of `writeWorkload`, which since 2026-09-26
>   carries every StatefulSet and Deployment write — against 82 guard-identifier references)*.
>   This is the real work in the ticket; tests 1 and 2 are a few hours together.
>
> **Effort: M (1–2 days).** Zero production code changes. It slips to L only if allowlist
> seeding for constraint 3 exceeds ~15 entries, which this ticket already makes a
> stop-and-discuss point rather than something to pad through. *(2026-09-27: seeded by the
> rule as written it comes to 19 pairs, so the stop point triggers; the structural exemption
> recommended under [Options](#options) brings it to 4.)*
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
a concurrent reconcile pass could observe another pass through). *(Added 2026-09-27: ADR 0019
D3 states the rule for all of `internal/` (`0019:80`), and the reconciler calls into
`internal/builder`, `internal/health` and `internal/valkeyclient`, so scan every package under
`internal/`; the one extra allowlist entry is `internal/builder/tls_material.go:41`. With every
existing `var` allowlisted by name, "any package-level `var` not on the allowlist fails" is
enough and needs no type inference from syntax. `errors.New` returns a pointer, so the type rule
as written would flag the sentinels and lean on the allowlist anyway.)*

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
  garbage collector takes with the CR, ADR 0020 NA62). *(Added 2026-09-27: also
  `r.Status().Update` / `r.Status().Patch`, and `r.writeWorkload`
  ([`pod_hardening.go:54`](../../internal/controller/pod_hardening.go#L54)), through which every
  StatefulSet and Deployment write goes since 2026-09-26 —
  [`valkey_controller.go:1303`](../../internal/controller/valkey_controller.go#L1303), `:1372`,
  `:1473`, `:1525`, `:2048`, `:2075`. Without it a new unguarded caller of `writeWorkload`
  escapes the net.)*
- For each call site, require **either** that the enclosing function's body (directly, not
  transitively) references at least one guard identifier —
  `IsControlledBy`, `deleteIfOwned`, `deleteOwnedPod`, `podIsOurs`, `podUnderNameIsOurs`,
  `ownedDataStatefulSet`, `filterOwnedPods` — **or** that the `(function, verb)` pair is on
  the explicit allowlist. *(Added 2026-09-27: all seven exist,
  [`foreign_object.go:182`](../../internal/controller/foreign_object.go#L182), `:226`, `:238`,
  `:264`, `:283`, `:305`; add `legacySentinelSecretIsOurs`
  ([`valkey_controller.go:1733`](../../internal/controller/valkey_controller.go#L1733)), the
  proof `deleteLegacySentinelSecret` uses, or that guarded delete shows as unguarded.)*
- The allowlist is a `map[string]string` in the test: key `"funcName/verb"`, value a
  one-line justification. Legitimate entries fall into known families:
  - writes to the CR itself and its status (`updateStatus`, `updatePhase`,
    `setStatusCondition`, annotation writes on `v` — the CR is the owner, guards do not
    apply); *(corrected 2026-09-27: under the direct-body rule the pairs are the callees that
    hold the write — `persistStatus`, `writePhase`, `writeStatusCondition`
    ([`valkey_controller.go:2569`](../../internal/controller/valkey_controller.go#L2569), `:2627`,
    `:2708`) — not their callers `updateStatus`, `updatePhase`, `setStatusCondition`
    (`:2181`, `:2605`, `:2665`); how to exempt them is the open decision under
    [Options](#options))*
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
- ~~`CLAUDE.md`: one line under the reconcile-concurrency and metrics sections each, pointing
  at the tests, so the next agent finds the net before re-deriving the rule.~~
  *(corrected 2026-09-27: since
  [ADR 0035](../adr/0035-the-readme-advertises-the-reference-lives-under-docs.md) contributor
  knowledge lives in `docs/developer/` and `DEVELOPER.md`. The pointers go into the table of
  convention-guarding tests,
  [`docs/developer/testing.md:48-53`](../developer/testing.md), and into the managed-object
  checklist, [`DEVELOPER.md:410-430`](../../DEVELOPER.md).)*

## Verification (Definition of Done)

Mutation checks, per the repo's revert-check policy (ADR 0017):

1. `make test-unit` green on the unmodified tree with the new tests active.
2. **Mutation 1**: add `var passCounter = map[string]int{}` at package level in
   `internal/controller` → `TestNoPackageLevelMutableState` fails and names it. Revert.
3. **Mutation 2**: add a `prometheus` import to `valkey_controller.go` →
   `TestNoMetricsWrittenFromReconcile` fails. Revert. *(Added 2026-09-27: an unused import does
   not compile, so the mutation needs a use inside a function body, e.g.
   `_ = prometheus.NewRegistry()`; a package-level use would trip test 1 as well.)*
4. **Mutation 3**: comment out the `IsControlledBy` check in one guarded reconcile function
   (e.g. `reconcileConfigMap`) → `TestEveryWriteAndDeleteIsGuardedOrAllowlisted` fails for
   ~~exactly that function/verb pair~~ *(corrected 2026-09-27: every verb of that function, for
   `reconcileConfigMap` the three pairs `SetControllerReference`, `Create` and `Update`, since the
   rule asks per `(function, verb)` pair and the function holds one guard)*. Revert.
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

## Seeding measurement (2026-09-27, `HEAD` = `4a7543e`)

**Verified.** The measurement comes from a throwaway `go/ast` scan outside the repository (its
source is not kept). It applies this ticket's own rule to the non-test files of
`internal/controller`: the verbs above, including `writeWorkload` and `Status().Update`, and a
guard counted when its identifier appears anywhere in the enclosing function body. The result is
**71 call sites in 66 `(function, verb)` pairs, 19 of them unguarded**:

- **14 pairs write the CR itself.** 11 are annotation writes in
  [`rolling_update.go`](../../internal/controller/rolling_update.go): `persistKnownMaster`
  `:1079`, `ensureWaitBound` `:1182`, `clearRecreationWait` `:2204`, `clearSyncWaitTimestamp`
  `:2670`, `incrementReconnectResetCount` `:3254`, `clearReconnectResetCount` `:3266`,
  `setRollingUpdateState` `:3415`, `clearRollingUpdateState` `:3468`, `setFailoverTriggered`
  `:3512`, `setFailoverTimestamp` `:3521`, and `writeManualFailoverState` `:4143` and `:4158`.
  The other 3 are status writes in
  [`valkey_controller.go`](../../internal/controller/valkey_controller.go): `persistStatus`
  `:2569`, `writePhase` `:2627` and `writeStatusCondition` `:2708`. Each of the 14 passes either
  the parameter `v *vkov1.Valkey` or `fresh := &vkov1.Valkey{}` (`rolling_update.go:4153-4158`).
- **2 pairs are `writeWorkload`'s own `Create` and `Update`**
  ([`pod_hardening.go:59`](../../internal/controller/pod_hardening.go#L59), `:61`). Its callers
  prove ownership.
- **1 pair is `deleteOwnedPod`** ([`foreign_object.go:307`](../../internal/controller/foreign_object.go#L307)),
  which is itself the guard; its callers prove the pod with `podIsOurs`.
- **1 pair is `deleteLegacySentinelSecret`**
  ([`valkey_controller.go:1716`](../../internal/controller/valkey_controller.go#L1716)). It is
  guarded by `legacySentinelSecretIsOurs`, which is not in the guard list; adding it removes the
  pair.
- **1 pair is `deleteLegacyServices`**
  ([`valkey_controller.go:936-962`](../../internal/controller/valkey_controller.go#L936-L962)).
  It scans ownerReferences by UID, with no `IsControlledBy` and no UID precondition. This is the
  known open item of [ADR 0006](../adr/0006-delete-only-what-the-operator-owns.md) (`0006:32`,
  `:336`), and **no ticket carries it**. The filing rule wants a file for it; this enrichment did
  not create one.

Mutation 3 works as designed *(on three pairs, not one, see the DoD)*: `reconcileConfigMap` holds exactly one guard reference,
`IsControlledBy` at [`valkey_controller.go:843`](../../internal/controller/valkey_controller.go#L843).
Test 2 finds nothing today: `client_golang/prometheus` is imported only by
`internal/metrics/collector.go` and `internal/observer/{observer,metrics,server}.go`.

**Not verified:** `make test-unit`, `make lint` and `make cyclo` were not run. The scan matches
guards by identifier, as this ticket's rule does, so its counts are that rule's result, not a
soundness claim.

## Options

One decision, and it has to be taken before test 3 is written. Tests 1 and 2 do not wait on it.

### Decision 1 — how test 3 treats a write on the CR itself

- **A. Exempt it structurally.** A call whose object argument is an identifier the enclosing
  function declares as a parameter of type `*vkov1.Valkey`, or assigns from `&vkov1.Valkey{}`,
  counts as a CR self-write. `go/ast` reads that from `fd.Type.Params` and the assignment, so the
  test stays stdlib-only. **(recommended)** The allowlist drops to 4 entries (`writeWorkload`
  ×2, `deleteOwnedPod`, `deleteLegacyServices`), below the ~15 stop point, and each entry
  documents a real caller-proves relation or a known residual. The rule matches all 14 CR writes
  in the tree. A CR written through a differently typed variable would show up as unguarded,
  which is the safe direction. The operator never writes another `Valkey`, so the exemption
  hides no write. *(Review 2026-09-27: "object argument" has to be pinned per verb, or A opens
  a hole. It is the argument after `ctx` for `Create`, `Update`, `Patch`, `Delete` and
  `Status().Update`/`Patch`, and the **second** argument (the controlled object) of
  `SetControllerReference(owner, controlled, scheme)`. All 13 `SetControllerReference` calls pass
  `v` as the **first** argument, so a test that exempts a call when *any* argument is the CR
  would exempt every ownerReference stamp, the write ADR 0020 D1 calls the one that decides what
  the garbage collector takes. Mutation 3 catches it only if it expects every pair of
  `reconcileConfigMap`: removing its one guard (`valkey_controller.go:843`) leaves three verbs
  unguarded, `SetControllerReference` `:823`, `Create` `:831` and `Update` `:857`, not one, and
  the `SetControllerReference` pair is the one an any-argument exemption would drop.)*
- **B. Allowlist every pair, as the ticket was written.** 19 entries, 14 of them with the same
  justification ("the CR is the owner"). That passes the ~15 stop point, and a line repeated 14
  times weakens the review hook the ticket exists for.
- **C. Exempt the CR writers by function name.** This is B with the list moved: every new
  annotation helper still needs a line, and nothing is gained.
- **D. Type-check with `go/types` instead of reading syntax.** Exact, but it needs the package's
  imports: either `golang.org/x/tools/go/packages` (not in `go.mod`, so a new module
  requirement, against the stdlib constraint) or the stdlib source importer, which type-checks
  controller-runtime and client-go from source on every `make test-unit` (runtime not measured).

## Decision

Not yet decided.

## Work list

1. **[XS, no decision] Tests 1 and 2**, the first two tests of
   `internal/controller/standing_constraints_test.go`:
   - Test 1 scans every package under `internal/`, because ADR 0019 D3 states the rule for all of
     `internal/` (`0019:80`), and fails on any package-level `var` not on the named allowlist.
     The allowlist has 6 entries: `conditionRegistry`, `errForeignObject`,
     `errSeccompProfileNotAllowed`, `errUserNamespacesDropped`, `errRecreateRequired` and
     `tlsMaterialKeys`.
   - Test 2 is written as designed.
   - Status notes go on ADR 0019 D3 and ADR 0021 D3, and two rows into
     `docs/developer/testing.md:48-53`.
2. *(waits on Decision 1)* Test 3, with its seeded allowlist and mutations 3 and 4. It includes
   the 2026-09-27 corrections to its design above, which need no decision: `writeWorkload`,
   `Status().Update` and `Status().Patch` among the verbs, and `legacySentinelSecretIsOurs`
   among the guards. Then the ADR 0020 status note and residual risk, and the pointer in
   `DEVELOPER.md:410-430`.
3. *(no decision; it is a filing, not an XS code item)* A ticket for `deleteLegacyServices`, the
   ADR 0006 open item. Report it to Hans together with the seeded allowlist (DoD step 7).
4. Close ([ADR 0034](../adr/0034-tickets-are-work-lists-that-get-archived.md)): the rule is
   already in ADRs 0019, 0020 and 0021, and the extraction is their status notes plus the two
   developer pointers. Then `git grep -nwE 'T42|042|S1'` outside `docs/tickets/`, and move the
   file to `archive/`.

## History

- 2026-09-27: adversarial review of the enrichment below. The ADR 0019 D3 rule sits at
  `0019:80`, not `:79` (three places fixed before commit). Decision 1 A gains a note pinning
  "object argument" per verb: every `SetControllerReference` passes the CR as its first
  argument, so an exemption keyed on any argument would exempt every ownerReference stamp. The
  DoD's mutation 3 is corrected in place: it fails three pairs of `reconcileConfigMap`, not one.
  Recommendation, urgency and effort unchanged.
- 2026-09-27: enriched - the status block is re-verified on `4a7543e` and corrected in place:
  five package-level vars (was three), 71 call sites against 82 guard references (was 64 and
  76). The test 3 design gains `writeWorkload`, `Status().Update` and
  `legacySentinelSecretIsOurs`. The seeding measurement is added (19 unguarded pairs), with
  Options for the one open decision, a work list marking tests 1 and 2 as XS with no decision,
  and this History. The `CLAUDE.md` pointer is replaced by `docs/developer/testing.md` and
  `DEVELOPER.md` (ADR 0035). **Urgency re-derived: `later`, was `icebox`** on the board row
  ([archive/039](archive/039-findings-from-the-1-11-0-fleet-rollout.md), ICEBOX section). Rule 4
  matches: the complete stdlib-only design is a cheap known fix, and the open decision is how
  deep the matching goes, not a product call. **Effort stays M**; the board row's L assumed the
  19-entry allowlist that Decision 1 A avoids.
- 2026-09-27 - renamed from `local_standing_constraints_enforcement.md` (S1) to ticket 042 when
  the tickets were numbered (recorded under the title).
