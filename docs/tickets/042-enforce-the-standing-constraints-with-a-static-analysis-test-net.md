# Ticket: Enforce the three standing constraints with a static-analysis test net

Ticket 042, formerly S1 (`local_standing_constraints_enforcement.md`); renamed on 2026-09-27 when
the tickets were numbered.

> **Status: OPEN, nothing built. Verified 2026-08-26 on `HEAD` = `1c309d8`; re-verified
> 2026-09-27 on `HEAD` = `4a7543e`, corrections in place below. ~~One decision is open
> ([Options](#options)); tests 1 and 2 need none ([Work list](#work-list)).~~
> *(corrected 2026-09-27 at `84a39c2`: two decisions are open ([Options](#options)): Decision 1
> for test 3, and Decision 2 for the reconciler-field half of test 1. Test 2 and the
> package-variable half of test 1 need none ([Work list](#work-list)).)* Urgency: `later`
> *(re-derived 2026-09-27, see History; the board row had `icebox`)*. Effort: M.** ~~Index:
> [`archive/039-findings-from-the-1-11-0-fleet-rollout.md`](archive/039-findings-from-the-1-11-0-fleet-rollout.md) (archived 2026-09-27, no longer maintained). Keep this line current — update it
> in the same change that touches this ticket.~~ *(corrected 2026-09-27 at `84a39c2`: there is no
> index any more; this blockquote is the ticket's state,
> [README](README.md#there-is-no-index-table-and-no-board).)*
>
> *(Re-verified 2026-09-27 at `84a39c2`: still open as described, nothing built. State
> **analysed** in substance: every load-bearing claim is verified at `84a39c2` or marked not
> verified, the seeding numbers are reproduced by two independent scans, and the option sets are
> complete. Severity `low` (nothing breaks if the net is never built; it catches future
> regressions that review catches today). Security `hardening`, threat: "no attack path today;
> would additionally catch at unit-test time a future reconcile path that writes or deletes an
> object under a generated name without a provenance proof (ADR 0020 D1, ADR 0006), the door
> through which a principal who may create `Valkey` CRs overwrites or garbage-collects a foreign
> object by naming it, and a future package variable or reconciler field shared across CRs
> (ADR 0019 D3)". Urgency `later` by rule 4, effort M, blocked by the decisions (Decision 1,
> Decision 2). The ticket keeps its frontmatter-less form: the tickets
> [README](README.md#there-is-no-index-table-and-no-board) (`README.md:115-117`) names 042 as a
> file whose state is this blockquote, so a YAML block needs that README paragraph changed in the
> same change.)*
>
> `internal/controller/standing_constraints_test.go` **does not exist**, and no equivalent
> exists: the only other `go/ast`-based test in the tree is
> [`condition_registry_test.go:3-6`](../../internal/controller/condition_registry_test.go#L3-L6),
> which enforces a different rule (it parses `api/v1/valkey_types.go`, nothing in
> `internal/controller`). The precedent this ticket cites,
> [`rbac_drift_test.go`](../../internal/controller/rbac_drift_test.go), is there.
>
> **All three constraints still hold today — this is preventive work, not a repair:**
> *(precised 2026-09-27 at `84a39c2`: constraint 3 with the one documented exception,
> `deleteLegacyServices`, see [Why](#why), carried by
> [T77](077-deletelegacyservices-deletes-without-the-ownership-guard.md).)*
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
>   *(Added 2026-09-27 at `84a39c2`: none of the six is ever assigned, index-assigned or appended
>   to after its declaration, tests included. The one cross-CR state in the tree is not a package
>   variable but a reconciler field, `nudges nudgeTracker`, which test 1 as designed does not see;
>   that is Decision 2.)*
> * **Constraint 2** (no metric written from a reconcile pass): no `prometheus`/`promauto`
>   import anywhere in `internal/controller` or `internal/sidecar`. *(Added 2026-09-27 at
>   `84a39c2`: nor in `internal/builder`, `internal/common`, `internal/health` or
>   `internal/valkeyclient`, the other packages a reconcile pass executes; `client_golang` is
>   imported only by `internal/metrics` and `internal/observer`.)*
> * **Constraint 3** (a new managed kind inherits the ownership guard): ~~**64**~~ non-test
>   client-verb / `SetControllerReference` call sites against ~~**76**~~ guard-identifier
>   references *(corrected 2026-09-27: 71 sites — 65 client-verb, `Status().Update` and
>   `SetControllerReference` calls plus 6 calls of `writeWorkload`, which since 2026-09-26
>   carries every StatefulSet and Deployment write — against 82 guard-identifier references)*.
>   *(Precised 2026-09-27 at `84a39c2`: the 65 are 49 `r.Create`/`Update`/`Patch`/`Delete`
>   calls, 3 `r.Status().Update` calls and 13 `controllerutil.SetControllerReference` calls. The
>   82 is a text grep that also counts 9 mentions in comments; the code holds 73 guard-identifier
>   references, 67 of them inside function bodies and 6 the declarations of the guard functions.
>   The count decides nothing.)*
>   This is the real work in the ticket; tests 1 and 2 are a few hours together.
>
> **Effort: M (1–2 days).** Zero production code changes. It slips to L only if allowlist
> seeding for constraint 3 exceeds ~15 entries, which this ticket already makes a
> stop-and-discuss point rather than something to pad through. ~~*(2026-09-27: seeded by the
> rule as written it comes to 19 pairs, so the stop point triggers; the structural exemption
> recommended under [Options](#options) brings it to 4.)*~~ *(corrected 2026-09-27 at `84a39c2`:
> seeded by the rule as written it comes to 19 unguarded pairs; with the design corrections that
> need no decision (`deleteOwnedPod` counted as a verb, not a guard) it comes to 22, and the
> structural exemption recommended under Decision 1 brings it to 8, below the stop point. See
> [Fact](#fact-re-verified-2026-09-27-head--84a39c2).)*
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
> assessment — every claim above is a static read of the tree. *(2026-09-27 at `84a39c2`: still
> not run; the counts come from throwaway `go/ast` scans outside the repository, see
> [Fact](#fact-re-verified-2026-09-27-head--84a39c2).)*

## Why

Three load-bearing rules in this repo are **standing constraints on future code**, not
enforced invariants. Today they hold through discipline and review only:

1. **No fleet-wide reconciler state** (ADR 0019 D3). Reconcile concurrency is 4; it is only
   safe because no state in `internal/controller` is shared across CRs — the `nudgeTracker`
   keys carry namespace and CR name, the blocked-pass marker rides on the context, and there
   is no package-level mutable state. One future package-level map breaks this silently.
   *(Added 2026-09-27 at `84a39c2`: so does a future map field on the reconciler. The
   `nudgeTracker` is itself a reconciler field,
   [`valkey_controller.go:112`](../../internal/controller/valkey_controller.go#L112), a
   mutex-guarded `map[types.NamespacedName]time.Time`
   ([`nudge.go:55-58`](../../internal/controller/nudge.go#L55-L58)) shared by all four workers
   ([`ratelimiter.go:60`](../../internal/controller/ratelimiter.go#L60), chart
   `maxConcurrentReconciles: 4`), and safe only because every key carries namespace and name
   (`waitBoundKey`, [`rolling_update.go:1152-1154`](../../internal/controller/rolling_update.go#L1152-L1154)).)*
2. **No metric written from a reconcile pass** (ADR 0021). The operator's `vko_valkey_*`
   series come from a collect-time collector over the manager cache
   (`internal/metrics/collector.go`), which is what makes deleted resources stop producing
   series with zero bookkeeping. One future `prometheus.NewGaugeVec(...).Set(...)` inside a
   ~~reconcile step reintroduces the deletion-bookkeeping problem and the stale-series bug.~~
   *(corrected 2026-09-27 at `84a39c2`: reconcile step introduces the deletion-bookkeeping
   problem [ADR 0021](../adr/0021-per-resource-metrics-and-the-alert-that-was-missing.md) D3
   rejected (`0021:89-94`, and `0021:184-186` under Alternatives). No reconcile-written gauge
   ever shipped: before ADR 0021 there were no per-resource series at all (`0021:23-54`), so
   there was no stale-series bug to bring back.)*
3. **A new managed kind inherits the ownership guard, not an exemption** (ADR 0020 D1/D2,
   ADR 0006). ~~Every write and delete on a generated name goes through
   `metav1.IsControlledBy` / `deleteIfOwned` / `podIsOurs` today.~~ *(corrected 2026-09-27 at
   `84a39c2`: every write and delete on a generated name goes through `metav1.IsControlledBy` /
   `deleteIfOwned` / `podIsOurs` today except one, `deleteLegacyServices`
   ([`valkey_controller.go:936-962`](../../internal/controller/valkey_controller.go#L936-L962)),
   which deletes after an ownerReference UID scan with no `IsControlledBy` and no UID
   precondition. It is the documented open item of ADR 0006 (`0006:32`, `:336`), and
   `docs/adr/README.md:71` and `docs/security/isolation-and-tenancy.md:163-164` name it as the
   exception. Its fix is
   [T77](077-deletelegacyservices-deletes-without-the-ownership-guard.md).)* Nothing stops a future
   `reconcileNewThing` from calling `r.Update` on the strength of a name.

The RBAC triple has exactly this kind of net (`rbac_drift_test.go` asserts generated ⊆ chart
and names the missing triple; ADR 0014 explains why a test beats a convention). These three
constraints have no equivalent. This ticket adds one.

## What to build

One new file, `internal/controller/standing_constraints_test.go`, containing three ordinary
Go tests that parse the package source with `go/parser` + `go/ast` (no new dependencies —
stdlib only, same style of "test as lint" as `rbac_drift_test.go`). They run under
`make test-unit` and therefore in CI with no pipeline change. *(Verified 2026-09-27 at
`84a39c2`: the `Unit Tests` job ([`release.yml:589`](../../.github/workflows/release.yml#L589))
runs `make test-unit-coverage` (`:609`), which is `go test ./...` without build tags
([`Makefile:114-118`](../../Makefile#L114-L118)). `Unit Tests` is one of the twelve required
checks of [ADR 0017](../adr/0017-test-and-ci-policy.md) D47 (`0017:535`); a live read of
`gh api repos/guided-traffic/valkey-operator/rules/branches/main` on 2026-09-27 lists it, enforced
by a repository ruleset (the classic branch-protection endpoint answers 404). So the net gates
merges. The file carries no build tag, so `make lint` and `make vet` cover it; ticket 043 does
not apply.)*

### Test 1: `TestNoPackageLevelMutableState`

Parse every non-test file in `internal/controller`. Fail on any package-level `var` whose
declared or inferred type is mutable (map, slice, chan, pointer, or a struct type — anything
a concurrent reconcile pass could observe another pass through). *(Added 2026-09-27: ADR 0019
D3 states the rule for all of `internal/` (`0019:80`), and the reconciler calls into
`internal/builder`, `internal/health` and `internal/valkeyclient`, so scan every package under
`internal/`; the one extra allowlist entry is `internal/builder/tls_material.go:41`. With every
existing `var` allowlisted by name, "any package-level `var` not on the allowlist fails" is
enough and needs no type inference from syntax. `errors.New` returns a pointer, so the type rule
as written would flag the sentinels and lean on the allowlist anyway.)* *(Added 2026-09-27 at
`84a39c2`: locate the tree with `repoRoot`
([`rbac_drift_test.go:63`](../../internal/controller/rbac_drift_test.go#L63)). Whether the test
also pins the fields of `ValkeyReconciler` and `nudgeTracker` is Decision 2
([Options](#options)).)*

Allowlist, checked by variable name and asserted immutable-by-use:

- sentinel errors created with `errors.New` (e.g. `errForeignObject`) — written once at init,
  only ever compared;
- any true constant-like `var` that cannot be `const` (e.g. a precomputed table), listed
  explicitly with a one-line justification string in the test.

*(Added 2026-09-27 at `84a39c2`: "asserted immutable-by-use" is part of the work, and the
earlier work list dropped it. The test fails on any assignment, index assignment, `++`/`--` or
`append` whose target is an allowlisted variable, anywhere in `internal/`, tests included. Today
there is none.)*

The failure message must name the variable, the file and the rule
("package-level mutable state breaks MaxConcurrentReconciles > 1 — see ADR 0019 D3;
carry the state per-CR (keyed by namespace/name) or per-pass (on the context) instead").

Known accepted state to seed the allowlist from the current tree: check first whether any
exists at all — ~~as of 2026-08-22 the expectation is that the list is empty apart from
sentinel errors.~~ *(corrected 2026-09-27 at `84a39c2`: the list has six entries, four
`errors.New` sentinels (`errForeignObject`, `errSeccompProfileNotAllowed`,
`errUserNamespacesDropped`, `errRecreateRequired`) and two tables (`conditionRegistry`,
`tlsMaterialKeys`), none of them written after its declaration.)* If the scan finds something not
on that list, that is a finding of this
ticket, not something to silently allowlist: surface it to Hans before proceeding.

### Test 2: `TestNoMetricsWrittenFromReconcile`

~~Parse the imports of every non-test file in `internal/controller` (and `internal/sidecar`,
which has the same no-CR-access shape and must not grow a registry either). Fail if any file
imports `github.com/prometheus/client_golang/prometheus` or
`.../prometheus/promauto`.~~ *(corrected 2026-09-27 at `84a39c2`: that scope is wrong in both
directions, and the correction needs no decision. It misses `internal/health`, which runs in
every pass (`getInstanceChecker` returns `health.NewChecker`,
[`valkey_controller.go:116-121`](../../internal/controller/valkey_controller.go#L116-L121);
`findMaster` is in `internal/health/checker.go`), and `internal/builder` and `internal/common`.
It misses a gauge that `internal/metrics` exports and the controller sets, because a method call
on an imported value needs no `prometheus` import. And it applies to `internal/sidecar` a rule no
ADR states: the sidecar is a separate process whose series die with its pod, so ADR 0021 D3's
deletion-bookkeeping argument does not reach it. The test therefore scans the reconciler's import
closure under `internal/`, computed by the test from the import declarations of
`internal/controller` so that a new package is covered without editing the test (today:
`controller`, `builder`, `common`, `health`, `valkeyclient`), and fails on any import with the
prefix `github.com/prometheus/client_golang/`, on `sigs.k8s.io/controller-runtime/pkg/metrics`,
and on the module's `internal/metrics`. It does not scan `internal/sidecar` or
`internal/observer`. Nothing violates it today: outside the closure, `client_golang` is imported
by `internal/metrics/collector.go:18` and `internal/observer/{metrics.go:6, observer.go:12,
server.go:8}`, and `controller-runtime/pkg/metrics` or `internal/metrics` by `cmd/main.go`,
`internal/metrics/collector.go:21` and `test/integration` only.)*

~~Metric registration and emission belong in `internal/metrics` only.~~ *(corrected 2026-09-27 at
`84a39c2`: false for the tree — `internal/observer` registers and sets its own gauges in the
separate observer process ([`metrics.go:10-18`](../../internal/observer/metrics.go#L10-L18),
`prometheus.MustRegister` at [`observer.go:185`](../../internal/observer/observer.go#L185)),
which ADR 0021 acknowledges (`0021:73`). In the operator process, metric registration and
emission belong in `internal/metrics/collector.go` only.)* The failure message
points at ADR 0021 D3 and states the alternative: "add the series to the collect-time collector
in internal/metrics/collector.go; never write a gauge from a reconcile pass".

The import ban is a syntactic approximation of ADR 0021 D3, not its exact scope: D3 forbids
per-resource gauges written during reconcile, and the ban also forbids process-global
instrumentation in a shared package, for example a label-free command-latency histogram in
`internal/valkeyclient`, which the sidecar and the observer import as well. The test's doc comment
says so.

No allowlist. If a legitimate need ever arises, widening the test *is* the review hook.

### Test 3: `TestEveryWriteAndDeleteIsGuardedOrAllowlisted`

The ambitious one, and the one that pays for the ticket. Scope it precisely:

- Walk every function in `internal/controller` (non-test files). Collect every call site of
  the reconciler's client verbs: `r.Create`, `r.Update`, `r.Patch`, `r.Delete`, and the
  `controllerutil.SetControllerReference` stamp (a write like any other — it decides what the
  garbage collector takes with the CR, ~~ADR 0020 NA62~~ *(corrected 2026-09-27 at `84a39c2`:
  ADR 0020 D1, the corollary at `0020:209`; `NA62` is an archive/037 label that ticket 040 is
  removing from tracked files, and the test's messages and comments cite ADRs only, ADR 0034)*).
  *(Added 2026-09-27: also
  `r.Status().Update` / `r.Status().Patch`, and `r.writeWorkload`
  ([`pod_hardening.go:54`](../../internal/controller/pod_hardening.go#L54)), through which every
  StatefulSet and Deployment write goes since 2026-09-26 —
  [`valkey_controller.go:1303`](../../internal/controller/valkey_controller.go#L1303), `:1372`,
  `:1473`, `:1525`, `:2048`, `:2075`. Without it a new unguarded caller of `writeWorkload`
  escapes the net.)* *(Added 2026-09-27 at `84a39c2`: the list is still incomplete. The verbs are
  the full method sets of controller-runtime v0.25.1 `client.Writer` (`Apply`, `Create`,
  `Delete`, `Update`, `Patch`, `DeleteAllOf`, `pkg/client/interfaces.go:65-85`) and
  `SubResourceWriter` (`Create`, `Update`, `Patch`, `Apply`, `:146-162`, reached through
  `Status()` or `SubResource(...)`), matched on **any** receiver, not only `r`, plus the two
  wrappers `writeWorkload` and `deleteOwnedPod`. Today all 52 client-writer calls are on `r` (49
  `r.Create`/`Update`/`Patch`/`Delete`, 3 `r.Status().Update`), and there is no `Apply`,
  `DeleteAllOf` or `SubResource` call and no call on `r.Client` or `r.APIReader`, so the wider
  match has zero false positives; revive's `receiver-naming` rule only keeps one receiver name
  per type, it does not pin it to `r`, and a free helper taking a `client.Client` would escape a
  match on `r`.)*
- For each call site, require **either** that the enclosing function's body (directly, not
  transitively) references at least one guard identifier —
  `IsControlledBy`, `deleteIfOwned`, ~~`deleteOwnedPod`,~~ `podIsOurs`, `podUnderNameIsOurs`,
  `ownedDataStatefulSet`, `filterOwnedPods` — **or** that the `(function, verb)` pair is on
  the explicit allowlist. *(Added 2026-09-27: all seven exist,
  [`foreign_object.go:182`](../../internal/controller/foreign_object.go#L182), `:226`, `:238`,
  `:264`, `:283`, `:305`; add `legacySentinelSecretIsOurs`
  ([`valkey_controller.go:1733`](../../internal/controller/valkey_controller.go#L1733)), the
  proof `deleteLegacySentinelSecret` uses, or that guarded delete shows as unguarded.)*
  *(corrected 2026-09-27 at `84a39c2`: `deleteOwnedPod`
  ([`foreign_object.go:305-313`](../../internal/controller/foreign_object.go#L305-L313)) is not a
  guard. It proves nothing: it sends only the UID precondition, and its doc comment says it
  "deletes a pod the caller has already proven". It is a verb wrapper, like `writeWorkload`, and
  moves to the verbs above. Listed as a guard it had two effects: its calls were never checked
  (they are not `r.Delete`), and every other verb in a function that calls it counted as guarded.
  The second masks nothing today — moving it changes no other pair's status — but the first lets
  every future pod delete through it pass the net, including one that `Get`s a pod by name and
  deletes it with no `podIsOurs`. This matches the documented split, `DEVELOPER.md:423-424`:
  prove with `podIsOurs`, delete with `deleteOwnedPod`. The walk treats the bodies of function
  literals as part of the enclosing function, both for guard references and for Decision 1's CR
  identifiers.)*
- The allowlist is a `map[string]string` in the test: key `"funcName/verb"`, value a
  one-line justification. Legitimate entries fall into known families:
  - writes to the CR itself and its status (`updateStatus`, `updatePhase`,
    `setStatusCondition`, annotation writes on `v` — the CR is the owner, guards do not
    apply); *(corrected 2026-09-27: under the direct-body rule the pairs are the callees that
    hold the write — `persistStatus`, `writePhase`, `writeStatusCondition`
    ([`valkey_controller.go:2570`](../../internal/controller/valkey_controller.go#L2570), `:2628`,
    `:2709`) — not their callers `updateStatus`, `updatePhase`, `setStatusCondition`
    (`:2181`, `:2606`, `:2666`); how to exempt them is the open decision under
    [Options](#options))*
  - pod writes/deletes in functions whose *caller* proved provenance and passed the proven
    object in (e.g. ~~`deleteOwnedPod` itself;~~ the rolling-update helpers that receive an
    already-filtered pod) — the justification names the proving caller; *(corrected 2026-09-27
    at `84a39c2`: with `deleteOwnedPod` a verb, this family is its own `Delete` plus the four
    callers that reference no proof in their own body — `replaceNextReplica`
    ([`rolling_update.go:2488`](../../internal/controller/rolling_update.go#L2488)),
    `replaceRemainingPods` (`:3034`), `deleteNextPendingPod` (`:3994`) and `handleManualFailover`
    (`:4078`) — whose pods all come from `collectPodStates`, which filters with `podIsOurs`
    ([`:1931`](../../internal/controller/rolling_update.go#L1931), commented "The choke point for
    four of the six pod deletes"). A justification names the proving function and guard ("pod
    taken from collectPodStates, which filters with podIsOurs"), never a line number: this
    re-verification found 15 line numbers moved by one comment-only commit)*
  - Event emission if it surfaces as a client verb.
- Seed the allowlist from the current tree. Every seeded entry must be justifiable from the
  guard architecture (ADR 0020 D8's one-reporter rule, the caller-proves pattern of
  `podIsOurs`). An entry that cannot be justified is, again, a finding — surface it.

The point is not proving correctness (a guard on the wrong object still passes). The point
is the same as the RBAC drift test: **a new unguarded write cannot land without either
adding the guard or adding a visible, justified allowlist line** — and that diff line is
what review catches. State this explicitly in the test's doc comment so nobody mistakes it
for a soundness proof. *(Added 2026-09-27 at `84a39c2`: the doc comment also names the concrete
shape of that gap — `IsControlledBy` on a StatefulSet and `ownedDataStatefulSet` prove the
StatefulSet, not a pod, so a pod write next to them passes on the wrong object. Measured on the
tree before `995f186` (the ADR 0020 D9 fix), see
[Fact](#fact-re-verified-2026-09-27-head--84a39c2).)*

Direct-body-only matching (no call-graph transitivity) is a deliberate simplification: it
produces some allowlist entries for helper functions, and each of those entries documents a
caller-proves relationship that is currently implicit. That is a feature. If the allowlist
comes out longer than ~15 entries, stop and reconsider the matching depth with Hans before
padding it.

## Constraints on the implementation

- Stdlib only (`go/parser`, `go/ast`, `go/token`). No new module requirements. *(2026-09-27 at
  `84a39c2`: holds for Decision 1 A and B; the `go/types` variants are the removed option D1-D,
  see History.)*
- Cyclomatic complexity < 15 per function — the AST walk wants small named helpers anyway.
  *(Added 2026-09-27 at `84a39c2`: no gate checks this for the new file. `make cyclo` ignores
  `_test.go` ([`Makefile:90`](../../Makefile#L90), ADR 0017 D35 at `0017:647`), and golangci-lint
  enables no complexity linter (`.golangci.yml:7-18`). It is a review item.)*
- No `testing.Short()` gate (repo rule, CLAUDE.md).
- Failure messages must name file, function/variable, and the ADR that carries the rule —
  modelled on `rbac_drift_test.go`, which names the missing triple. *(Added 2026-09-27 at
  `84a39c2`: they cite ADRs only — ADR 0019 D3, ADR 0020 D1/D9, ADR 0006, ADR 0021 D3 — never
  `NA62`, `NA63`, a T-label or this ticket (ADR 0034).)*
- English, like everything else.

## ADR updates (same change, per CLAUDE.md)

- **ADR 0019**: the "standing constraint, not a one-time audit" sentence in D3 gains a
  pointer: the constraint is now backed by `TestNoPackageLevelMutableState`. Status note
  with date.
- **ADR 0021**: same for the no-gauge-from-reconcile rule and
  `TestNoMetricsWrittenFromReconcile`. *(Added 2026-09-27 at `84a39c2`: including the sentence
  that the import ban approximates D3 and reaches process-global metrics in shared packages.)*
- **ADR 0020**: same for D1/D2 inheritance and `TestEveryWriteAndDeleteIsGuardedOrAllowlisted`,
  including one honest sentence on what the test does **not** prove (guard-on-wrong-object,
  transitivity). Residual-risks entry for the allowlist mechanism: an unjustified allowlist
  line is the new way to defeat the net, and review of that line is the defence. *(Added
  2026-09-27 at `84a39c2`: the guard-on-wrong-object sentence carries the measured example, the
  pre-`995f186` Sentinel pod delete that passed on `IsControlledBy` of the Sentinel StatefulSet.
  Sequence the edit after ticket 040 rewrites the `NA62`/`NA63` labels in ADR 0020 (040 work list
  item 4, "rewrite ADR 0020 before T42 touches it", which itself waits on 040's Decision 1), and
  coordinate with ticket 060, which also
  amends ADR 0020 (the D9 section); the contents do not conflict.)*
- ~~`CLAUDE.md`: one line under the reconcile-concurrency and metrics sections each, pointing
  at the tests, so the next agent finds the net before re-deriving the rule.~~
  *(corrected 2026-09-27: since
  [ADR 0035](../adr/0035-the-readme-advertises-the-reference-lives-under-docs.md) contributor
  knowledge lives in `docs/developer/` and `DEVELOPER.md`. The pointers go into the table of
  convention-guarding tests,
  [`docs/developer/testing.md:48-53`](../developer/testing.md), and into the managed-object
  checklist, [`DEVELOPER.md:410-430`](../../DEVELOPER.md).)* *(Added 2026-09-27 at `84a39c2`:
  the managed-object pointer states that `deleteOwnedPod` counts as a verb, not a guard. Under
  Decision 2 B a third pointer goes into the operator-flag checklist, `DEVELOPER.md:432-437`: a
  flag handed to the reconciler in `newReconciler` needs a line in the reconciler-field
  allowlist, or that step goes red with no pointer.)*

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
   *(Added 2026-09-27 at `84a39c2`: the `r.Update` must write a non-CR object, for example a
   ConfigMap built in the function; under Decision 1 A a write of its `*vkov1.Valkey` parameter
   passes silently.)*
6. ~~`make lint` and `make cyclo` green.~~ *(corrected 2026-09-27 at `84a39c2`: `make lint` green;
   `make cyclo` green as well, but it ignores `_test.go` (`Makefile:90`), so it proves nothing
   about the new file, whose complexity is a review item.)*
7. Report the seeded allowlist and any findings from seeding to Hans before finishing.

*(Added 2026-09-27 at `84a39c2`, for the parts the re-verification added:)*

8. **Mutation 5**: in a new function that holds no guard, call
   `controllerutil.SetControllerReference(v, obj, r.Scheme)` → test 3 fails. It proves that the
   CR exemption reads the controlled (second) argument, not any argument.
9. **Mutation 6**: `Get` a pod by name and pass it to `r.deleteOwnedPod` in a function with no
   `podIsOurs` → test 3 fails. Under the guard list as first written it passes.
10. **Mutation 7**: `_ = prometheus.NewRegistry()` inside a function in `internal/health`, and
    separately an import of `internal/metrics` from `internal/controller` used in a function body
    → test 2 fails for each. Revert.
11. **Mutation 8** *(only under Decision 2 B)*: add a map field to `ValkeyReconciler` → test 1
    fails and names the field and ADR 0019 D3. Revert.

## Explicitly out of scope

- Enforcing `findMaster`'s ordinal-indexed collection (ADR 0019's second half) — semantic,
  not lintable at this level.
- Verifying guards act on the *right* object — that stays with unit tests and review.
- The chart/RBAC net — exists (`rbac_drift_test.go`).
- Any change to reconcile behaviour. This ticket adds tests and doc pointers only.
- *(Added 2026-09-27 at `84a39c2`:)* fixing `deleteLegacyServices`. That is
  [T77](077-deletelegacyservices-deletes-without-the-ownership-guard.md) (filed from work list
  item 4).

## Seeding measurement (2026-09-27, `HEAD` = `4a7543e`; locations re-read at `84a39c2`)

**Verified.** The measurement comes from a throwaway `go/ast` scan outside the repository (its
source is not kept). It applies this ticket's own rule to the non-test files of
`internal/controller`: the verbs above, including `writeWorkload` and `Status().Update`, and a
guard counted when its identifier appears anywhere in the enclosing function body. The result is
**71 call sites in 66 `(function, verb)` pairs, 19 of them unguarded**:

- **14 pairs write the CR itself.** 11 are annotation writes in
  [`rolling_update.go`](../../internal/controller/rolling_update.go): `persistKnownMaster`
  `:1080`, `ensureWaitBound` `:1183`, `clearRecreationWait` `:2205`, `clearSyncWaitTimestamp`
  `:2673`, `incrementReconnectResetCount` `:3257`, `clearReconnectResetCount` `:3269`,
  `setRollingUpdateState` `:3418`, `clearRollingUpdateState` `:3471`, `setFailoverTriggered`
  `:3515`, `setFailoverTimestamp` `:3524`, and `writeManualFailoverState` `:4146` and `:4161`.
  The other 3 are status writes in
  [`valkey_controller.go`](../../internal/controller/valkey_controller.go): `persistStatus`
  `:2570`, `writePhase` `:2628` and `writeStatusCondition` `:2709`. Each of the 14 passes either
  the parameter `v *vkov1.Valkey` or `fresh := &vkov1.Valkey{}` (`rolling_update.go:4156-4161`,
  inside a `retry.RetryOnConflict` function literal).
- **2 pairs are `writeWorkload`'s own `Create` and `Update`**
  ([`pod_hardening.go:59`](../../internal/controller/pod_hardening.go#L59), `:61`). Its callers
  prove ownership.
- **1 pair is `deleteOwnedPod`** ([`foreign_object.go:307`](../../internal/controller/foreign_object.go#L307)),
  ~~which is itself the guard; its callers prove the pod with `podIsOurs`.~~ *(corrected
  2026-09-27 at `84a39c2`: which is not a guard but the delete wrapper, and whose callers mostly
  do not prove the pod in their own body. Of its six call sites only
  `handleStandaloneRollingUpdate` (`rolling_update.go:3816`, `podIsOurs` at `:3781`) does;
  `dispatchSentinelRollingUpdate` (`:5099`) references only `IsControlledBy` on the Sentinel
  StatefulSet, its pod proof sitting in `scanSentinelPods` (`:4991`); and `replaceNextReplica`
  `:2488`, `replaceRemainingPods` `:3034`, `deleteNextPendingPod` `:3994` and
  `handleManualFailover` `:4078` reference no proof and take the pod from `collectPodStates`
  (`podIsOurs` at `:1931`). The pods are proven transitively, not by the callers.)*
- **1 pair is `deleteLegacySentinelSecret`**
  ([`valkey_controller.go:1716`](../../internal/controller/valkey_controller.go#L1716)). It is
  guarded by `legacySentinelSecretIsOurs`, which is not in the guard list; adding it removes the
  pair.
- **1 pair is `deleteLegacyServices`**
  ([`valkey_controller.go:936-962`](../../internal/controller/valkey_controller.go#L936-L962)).
  It references no guard identifier, so the pair is unguarded and stays on the allowlist until
  the delete is fixed. The gap itself, the known open item of
  [ADR 0006](../adr/0006-delete-only-what-the-operator-owns.md), is
  [T77](077-deletelegacyservices-deletes-without-the-ownership-guard.md) *(filed 2026-09-27 from
  work list item 4)*.

Mutation 3 works as designed *(on three pairs, not one, see the DoD)*: `reconcileConfigMap` holds exactly one guard reference,
`IsControlledBy` at [`valkey_controller.go:843`](../../internal/controller/valkey_controller.go#L843).
Test 2 finds nothing today: `client_golang/prometheus` is imported only by
`internal/metrics/collector.go` and `internal/observer/{observer,metrics,server}.go`.

**Not verified:** `make test-unit`, `make lint` and `make cyclo` were not run. The scan matches
guards by identifier, as this ticket's rule does, so its counts are that rule's result, not a
soundness claim.

## Fact (re-verified 2026-09-27, `HEAD` = `84a39c2`)

**Verified** (static read and throwaway scans, each reproduced independently by a second scan
written from scratch; the scan programs lived in the session scratchpad outside the repository
and are not kept; `git status` was clean before and after):

- **Nothing is built.** `internal/controller/standing_constraints_test.go` does not exist, and
  `git grep` for the three test names outside `docs/tickets/` exits 1.
- **Test 1 baseline.** `git grep -nE '^var ' -- 'internal/*.go' ':!*_test.go'` and a top-level
  `var` scan both find exactly six: `builder/tls_material.go:41`,
  `controller/condition_registry.go:92`, `foreign_object.go:71`, `pod_hardening.go:22`, `:46`,
  `volumeclaim_conflict.go:46`. A grep for assignment, index assignment or `append` on any of
  them over `internal/`, tests included, finds only the declarations.
- **The reconciler struct.** `ValkeyReconciler`
  ([`valkey_controller.go:75-113`](../../internal/controller/valkey_controller.go#L75-L113)) has
  12 fields: `Client` (embedded), `Scheme`, `InstanceChecker`, `Recorder`, `OperatorImage`,
  `OperatorNamespace`, `OperatorVersion`, `APIReader`, `NewValkeyClientFn`,
  `MaxConcurrentReconciles`, `AllowedSeccompLocalhostProfiles`, `nudges`. Four were added in the
  38 days before 2026-09-26 (`git log -G`, first appearance): `nudges` `2d1a133` (2026-08-19),
  `MaxConcurrentReconciles` `ec5036f` (2026-08-21), `APIReader` `e32f0d2` (2026-08-27),
  `AllowedSeccompLocalhostProfiles` `b13377e` (2026-09-26). The flag fields are set once in
  `newReconciler` ([`cmd/main.go:113-125`](../../cmd/main.go#L113-L125)). `health.Checker` and
  `valkeyclient.Client` hold no mutable state today; the per-pass `passState`
  (`foreign_object.go:89-92`) carries a mutex but rides on the context, not on the reconciler.
- **The internal import graph** (per-package `git grep` of `valkey-operator/internal/` imports,
  non-test files): `controller` → `builder`, `common`, `health`, `valkeyclient`; `health` →
  `builder`, `common`, `valkeyclient`; `builder` → `common`; `sidecar` → `common`, `tlsmaterial`,
  `valkeyclient`; `observer` → `tlsmaterial`, `valkeyclient`; `metrics` → none. No package of the
  reconciler's closure imports `client_golang`, `controller-runtime/pkg/metrics` or
  `internal/metrics`.
- **Test 3 seeding under the rule as written:** `sites=71 pairs=66 unguarded=19`, exactly the
  pairs listed under [Seeding measurement](#seeding-measurement-2026-09-27-head--4a7543e-locations-re-read-at-84a39c2).
  The 71 sites are 49 `r.Create`/`Update`/`Patch`/`Delete` (`git grep -hoE` counts 25
  `r.Update`, 14 `r.Create`, 8 `r.Delete`, 2 `r.Patch`), 3 `r.Status().Update`, 13
  `controllerutil.SetControllerReference` (`pdb.go:176` and 12 in `valkey_controller.go`, every
  one written `(v, desired, r.Scheme)`) and 6 `writeWorkload`.
- **Test 3 seeding under the corrected rule** (`deleteOwnedPod` a verb, `legacySentinelSecretIsOurs`
  a guard, the full `Writer`/`SubResourceWriter` method set on any receiver):
  `sites=77 pairs=72 unguarded=22`. The six added sites are exactly the `deleteOwnedPod` calls;
  there is no `Apply`, `DeleteAllOf`, `SubResource` or non-`r` write. The 22 are the 14 CR writes
  plus 8: `writeWorkload`/`Create`, `writeWorkload`/`Update`, `deleteOwnedPod`/`Delete`,
  `deleteLegacyServices`/`Delete`, and `replaceNextReplica`, `replaceRemainingPods`,
  `deleteNextPendingPod` and `handleManualFailover`, each calling `deleteOwnedPod`. Under
  Decision 1 A the 8 are the allowlist.
- **Would the net have caught what ADR 0020 D9 fixed?** Both scans applied the rule as written to
  the non-test `internal/controller` files of `995f186^` (`git show 995f186^:<file>` into the
  scratchpad): `sites=72 pairs=70 unguarded=20`. It flags 5 of the 6 unproven pod deletes
  (`replaceNextReplica` `rolling_update.go:1357`, `replaceRemainingPods` `:1725`,
  `handleStandaloneRollingUpdate` `:2409`, `deleteNextPendingPod` `:2579`, `handleManualFailover`
  `:2643`, lines at that revision) and the unproven pod write `clearDrainStamps`/`Patch`
  (`steady_state_master.go:680`). It passes the sixth delete,
  `checkAndHandleSentinelRollingUpdate`'s `r.Delete(ctx, firstOutdatedPod)` (`:3370`), because the
  function referenced `IsControlledBy` on the Sentinel StatefulSet (`:3324`) — a guard on the
  wrong object.
- **The `go/types` alternative, measured.** Type-checking the non-test files of
  `internal/controller` with the stdlib `importer.ForCompiler(fset, "source", nil)` took 2m59.9s
  wall (334 s user; go1.27.1 darwin/arm64), and 3m20.7s on the second run (346 s user, 508 s
  sys). `golang.org/x/tools/go/packages` v0.49.0 (`packages.Load` with `NeedTypes`,
  `NeedTypesInfo`, `NeedSyntax`, from a scratch module, offline) took 823, 798 and 813 ms with a
  warm build cache and no package errors. `golang.org/x/tools` is absent from `go.mod`, present in
  `go.sum:137-138` at v0.49.0, and in the module graph (`GOPROXY=off go mod graph`: required by
  `golang.org/x/text@v0.42.0`, `controller-runtime@v0.25.1`, `apiextensions-apiserver@v0.37.1`,
  `kube-openapi`); [ADR 0014](../adr/0014-rbac-lives-in-three-places.md) D12 (`0014:165`) makes a
  test-only import a direct `go.mod` require.
- **`deleteLegacyServices`**
  ([`valkey_controller.go:936-962`](../../internal/controller/valkey_controller.go#L936-L962))
  holds the one unguarded non-CR delete of the tree; its facts, security class and fix are in
  [T77](077-deletelegacyservices-deletes-without-the-ownership-guard.md). For this ticket it is
  one allowlist line, `deleteLegacyServices`/`Delete`.
- **Close-step grep collision.** `git grep -nwE 'T42|042|S1' -- ':!docs/tickets'` returns only
  `internal/controller/pod_termination_test.go:408`, which cites the unrelated archive/039 `S1`
  label (the `countUpdatedPods` regression, `archive/039:2159`); it is not this ticket and is not
  edited at close.
- **The board row** (`archive/039:7826`, ICEBOX section) reads Sev `—`, Sec `—`, Eff `L`, no
  reason; the board was last groomed on 2026-09-26 (`archive/039:7746`), a day before the 19-pair
  measurement. The row's `L` has no recorded basis.
- No Valkey behaviour is claimed here, so no container was started.

**Not verified:**

- `make test-unit`, `make lint`, `make cyclo` (not run in this re-verification either, by the
  run's rules).
- The `go/packages` timing (about 0.8 s) was measured in one session only (three runs, 823, 798
  and 813 ms) and not reproduced independently; no option depends on it.
- *(2026-09-27: the security class of the `deleteLegacyServices` gap, parked here as unverified,
  is derived in [T77](077-deletelegacyservices-deletes-without-the-ownership-guard.md).)*

## Impact

Nothing breaks today. Without the net, the next package-level map, reconciler map field,
reconcile-written gauge or unguarded write onto a generated name lands with every test green, and
review is the only catch — the failure mode ADR 0014 and ADR 0027 were written after. The
security relation is `hardening`: no attack path exists today; the net would additionally catch
the provenance door of ADR 0020 D1 and ADR 0006 before a principal who may create `Valkey` CRs
could use it.

## Options

Two decisions, taken one at a time; both are test-design choices, neither changes production
code. Test 2 and the package-variable half of test 1 wait on neither.

### Decision 1 — how test 3 treats a write on the CR itself

**Mechanism.** 14 of the write pairs in `internal/controller` write the reconciled `Valkey` CR,
not a generated object: eleven annotation writes `r.Update(ctx, v)` in `rolling_update.go`
(`persistKnownMaster` [`:1080`](../../internal/controller/rolling_update.go#L1080) through
`writeManualFailoverState` `:4146` and `:4161`, listed under
[Seeding measurement](#seeding-measurement-2026-09-27-head--4a7543e-locations-re-read-at-84a39c2)),
the last on `fresh := &vkov1.Valkey{}` declared inside a `retry.RetryOnConflict` function literal
(`:4156`); and three `r.Status().Update(ctx, v)` in `valkey_controller.go`, `persistStatus`
[`:2570`](../../internal/controller/valkey_controller.go#L2570), `writePhase` `:2628`,
`writeStatusCondition` `:2709`. None references a guard and none needs one: the CR is the owner,
and ADR 0020 D1 concerns objects under generated names. Under the direct-body rule each is an
unguarded pair, so with the corrections above the scan reports 22 unguarded pairs, these 14 plus
8. The choice decides whether the 14 leave the allowlist through a structural rule or are listed
one by one. It changes nothing for writes onto non-CR objects and nothing in production code.

- **A. Exempt a CR self-write structurally. (recommended)** A call is a CR self-write when its
  object argument is an identifier the enclosing function (function literals included) declares
  as a `*vkov1.Valkey` parameter or assigns from `&vkov1.Valkey{}`. The object argument is pinned
  per verb: the argument after `ctx` for `Create`/`Update`/`Patch`/`Delete` and the
  `Status()`/`SubResource()` writers, the **second** (controlled) argument of
  `SetControllerReference(owner, controlled, scheme)`, never any argument — all 13
  `SetControllerReference` calls pass `v` first, so an any-argument exemption would exempt every
  ownerReference stamp, the write ADR 0020 D1 says decides what the garbage collector takes.
  `Apply` takes an apply configuration, not an object, so the exemption never matches it (the safe
  direction). Cost: S, about 30 lines of stdlib `go/ast` reading `fd.Type.Params`, the
  assignments of the body and a per-verb argument table. Consequences: it matches all 14 CR writes
  and nothing else, leaving 8 allowlist lines, each with a distinct justification a reviewer can
  check — `writeWorkload` ×2 (its three callers prove ownership with `IsControlledBy`),
  `deleteOwnedPod`/`Delete` (the wrapper itself), `deleteLegacyServices`/`Delete` (the ADR 0006
  open item, [T77](077-deletelegacyservices-deletes-without-the-ownership-guard.md); the line
  goes when T77 lands, under either of its options, leaving 7), and the four `deleteOwnedPod` callers fed by `collectPodStates`. A CR written
  through any other variable shape shows up as unguarded, the safe direction. The hole: a second
  `*vkov1.Valkey` parameter, or a `&vkov1.Valkey{}` read under another CR's name, would be
  exempted; no such function exists and the operator writes no other `Valkey`. Mutations 5 and 4
  prove the argument pinning and the non-CR case.
- **B. Allowlist every unguarded pair, as the ticket was first written.** No structural rule in
  the test; every new CR writer is a visible line. Cost: XS test code, but 22 allowlist entries
  (19 under the rule as first written), 14 of them carrying the identical justification "the CR is
  the owner", and each new annotation helper adds another (11 exist). Consequences: it crosses
  the ~15 stop point — which is the ticket's own device for reconsidering the depth with Hans, not
  a cost in itself — and it needs no type-shaped rule that could itself be wrong. But the
  repeated line teaches review to wave "CR is the owner" through, and that line, copied onto a
  non-CR write, is exactly how the net is defeated.

**Why A.** Checkable against the tree: the `84a39c2` scans show it matches exactly the 14 CR
writes and leaves 8 lines, each naming a distinct caller-proves relation or a known residual,
under the stop point. It beats B because B's 14 identical lines are the one justification that
lets a non-CR write through when copied, and the list grows with every annotation helper; A's
failure mode, an unmatched shape, is a visible unguarded pair.

### Decision 2 — whether test 1 also pins the fields of the reconciler

**Mechanism.** ADR 0019 D3 (`0019:70-86`) permits four concurrent reconciles only while no
reconciler state is fleet-wide. The one cross-CR state in the tree is not a package variable but
the reconciler field `nudges nudgeTracker`
([`valkey_controller.go:112`](../../internal/controller/valkey_controller.go#L112)), a
mutex-guarded map keyed by `types.NamespacedName`
([`nudge.go:55-58`](../../internal/controller/nudge.go#L55-L58)), safe because every key carries
namespace and name (`waitBoundKey`, `rolling_update.go:1152-1154`). Test 1 as designed scans only
package-level `var`s — six in all of `internal/`, none mutable. A new map field on
`ValkeyReconciler`, or a second map in `nudgeTracker`, keyed by pod name would be fleet-wide state
test 1 does not see. The choice widens what test 1 checks; it does not change the package-variable
half and adds no production code.

- **A. Package-level variables only, as written.** Cost: none beyond test 1. Consequences: no
  friction for new operator flags. The place where the only existing cross-CR state lives stays a
  review convention; a field on one struct is a visible diff, but so is a package variable, which
  is test 1's whole premise.
- **B. Also pin the field sets of `ValkeyReconciler` (12 fields) and `nudgeTracker` (`mu`,
  `first`) by name through `reflect`, each with a one-line justification in the test.
  (recommended)** A new field fails until listed; the message cites ADR 0019 D3 and says to key
  state by namespace/name or carry it on the context. Cost: XS, about 25 lines of stdlib
  `reflect`, plus one allowlist line per new reconciler field (four in the 38 days before
  2026-09-26), plus a step in the operator-flag checklist of `DEVELOPER.md:432-437` (three of the
  four recent fields were flags handed over in `newReconciler`). Consequences: it proves that a
  field was reviewed, not that its keys are per-CR; it cannot see state behind the interface and
  func fields (`Client`, `InstanceChecker`, `Recorder`, `NewValkeyClientFn`). The key shape stays
  a review question.

**Why B.** The only cross-CR state in the tree is a reconciler field while all six package
variables are immutable, so B covers the place where the next violation would most plausibly be
added, and its failure message carries ADR 0019 D3 to an author who does not know the rule. It
beats A because A tests only the package variables, which hold no mutable state today, and leaves
the one struct that holds cross-CR state to convention; the cost is one reviewed line per field
at a measured rate of four fields in 38 days.

## Decision

Not yet decided.

## Work list

1. **[XS, no decision] Test 2 and the package-variable half of test 1**, in
   `internal/controller/standing_constraints_test.go`:
   - Test 1 scans every package under `internal/`, because ADR 0019 D3 states the rule for all of
     `internal/` (`0019:80`), and fails on any package-level `var` not on the named allowlist.
     The allowlist has 6 entries: `conditionRegistry`, `errForeignObject`,
     `errSeccompProfileNotAllowed`, `errUserNamespacesDropped`, `errRecreateRequired` and
     `tlsMaterialKeys`. *(Added 2026-09-27 at `84a39c2`: plus the immutable-by-use check — any
     assignment, index assignment, `++`/`--` or `append` targeting an allowlisted variable
     anywhere in `internal/` fails; the tree is located with `repoRoot`.)*
   - Test 2 is written ~~as designed~~ *(corrected 2026-09-27 at `84a39c2`: with the corrected
     scope — the reconciler's import closure computed from `internal/controller`'s imports, the
     `client_golang/` prefix, `controller-runtime/pkg/metrics` and `internal/metrics` forbidden,
     sidecar and observer not scanned — and the approximation stated in its doc comment)*.
   - Mutations 1, 2 and 7.
   - Status notes go on ADR 0019 D3 and ADR 0021 D3, and two rows into
     `docs/developer/testing.md:48-53`.
2. *(waits on Decision 2)* The reconciler-field pin of test 1 under B, mutation 8, and the step in
   the operator-flag checklist, `DEVELOPER.md:432-437`. Under A nothing is added.
3. *(waits on Decision 1)* Test 3, with its seeded allowlist and mutations 3 to 6. It includes
   the 2026-09-27 corrections to its design above, which need no decision: `writeWorkload`,
   `Status().Update` and `Status().Patch` among the verbs, and `legacySentinelSecretIsOurs`
   among the guards. *(Added 2026-09-27 at `84a39c2`, also decision-free: `deleteOwnedPod` moved
   from the guards to the verbs; the full `Writer`/`SubResourceWriter` method set on any receiver;
   function-literal bodies walked as part of their function; allowlist justifications name the
   proving function, never a line; messages cite ADRs only.)* Then the ADR 0020 status note and
   residual risk (with the pre-`995f186` example), sequenced after ticket 040 work list item 4 and
   coordinated with ticket 060, and the pointer in `DEVELOPER.md:410-430` stating that
   `deleteOwnedPod` counts as a verb.
4. ~~*(no decision; it is a filing, not an XS code item)* A ticket for `deleteLegacyServices`, the
   ADR 0006 open item.~~ *(done 2026-09-27: filed as
   [T77](077-deletelegacyservices-deletes-without-the-ownership-guard.md), which carries the facts,
   the security class and the ticket 033 B1 recheck.)* What stays here: if test 3 lands before
   T77, its seeded allowlist holds `deleteLegacyServices`/`Delete`, and T77's fix removes that
   line; if T77 lands first, the allowlist starts at 7. Report the seeded allowlist to Hans (DoD
   step 7).
5. Close ([ADR 0034](../adr/0034-tickets-are-work-lists-that-get-archived.md)): the rule is
   already in ADRs 0019, 0020 and 0021, and the extraction is their status notes plus the ~~two~~
   developer pointers *(2026-09-27 at `84a39c2`: two, three under Decision 2 B)*. Then
   `git grep -nwE 'T42|042|S1'` outside `docs/tickets/` *(the one expected hit,
   `internal/controller/pod_termination_test.go:408`, is the unrelated archive/039 `S1` label and
   is not edited by this ticket; ticket 040 work item 8 may already have rewritten it, in which
   case the grep returns nothing)*, and move the file to `archive/`.

## History

- 2026-09-27: re-verified at `84a39c2`. Checked every claim of the ticket against the tree;
  the seeding numbers were reproduced by two independent `go/ast` scans (71 sites, 66 pairs, 19
  unguarded as written; 77, 72 and 22 under the corrected rule), and locations were re-read at
  `84a39c2` (the comment-only commit `bcc63c9` moved 15 line numbers in `rolling_update.go` and
  `valkey_controller.go`; `condition_registry_test.go:2-6` is `:3-6`), fixed in the links.
  **Found false or outdated, corrected in place:** `deleteOwnedPod` listed as a guard (it only
  sends the UID precondition; moved to the verbs) and "its callers prove the pod with
  `podIsOurs`" (true for one of six); the verb list (missing `Apply`, `DeleteAllOf`, the
  `SubResourceWriter` and receivers other than `r`); test 2's scope (misses `internal/health`,
  `builder`, `common` and the `internal/metrics` bypass, and binds the sidecar with a rule no ADR
  states — corrected as decision-free work); "metric registration belongs in `internal/metrics`
  only" (the observer registers its own); "every write and delete ... is guarded today" (except
  `deleteLegacyServices`); "reintroduces ... the stale-series bug" (none ever shipped); "`make
  cyclo` green" as proof for a `_test.go` file; "82 guard-identifier references" (a text count,
  73 in code); "structural exemption brings it to 4" (8); "tests 1 and 2 need no decision" (test
  1's field half is Decision 2); the stale Index sentence. The test-3 citation of `NA62` becomes
  ADR 0020 D1. **Measured:** the pre-`995f186` tree (5 of 6 unproven pod deletes and
  `clearDrainStamps`/`Patch` flagged, the Sentinel delete passed on a guard of the wrong object);
  the stdlib source importer at 2m59.9s and 3m20.7s, `go/packages` at about 0.8 s. **Options:**
  rewritten as two decisions. Decision 1 keeps A (recommended) and B (runner-up), unchanged in
  recommendation, with A now walking function literals and pinned per verb, and 8 allowlist
  lines instead of 4. Removed from Decision 1: **C** (exempt CR writers by function name) — B with
  the list moved, a duplicate; **D** (`go/types`) — the stdlib importer costs about 3 minutes per
  unit run, `go/packages` needs a new direct `golang.org/x/tools` require (ADR 0014 D12), and on
  today's tree the syntactic rule already matches 14 of 14 CR writes with zero false positives,
  so it is disproportionate for a low-severity review hook. New Decision 2 (test 1 pins the
  reconciler fields): A (package variables only) and B (pin by `reflect`, recommended). Not kept
  for Decision 2: a mutable-kind heuristic over the fields (fuzzy — a pointer or interface hides a
  map, and it still misses a cache behind an interface; more code than B for a weaker hook) and a
  check that every reconciler map is keyed by `types.NamespacedName` (proves nothing: `waitBoundKey`
  already builds a synthetic `NamespacedName`, so `NamespacedName{Name: ordinal}` passes while
  being fleet-wide, and it misses non-map state). Not kept as a decision: test 2's scope — its
  option "`internal/controller` and `internal/sidecar`, exact paths" is a defect (misses
  `internal/health`, enforces an ADR-less rule), and "the closure plus `internal/sidecar` with a new
  ADR 0021 rule" is speculative scope no incident or ADR reasoning motivates, which would also
  have to be reopened for the own-exporter future ADR 0030 records; the closure scope is therefore
  a correction, not a choice. The earlier History entry below that attributes the board row's `L`
  to the 19-entry allowlist has no support: the row gives no reason and predates the measurement
  (`archive/039:7746`, `:7826`); it stays as written, append-only. **Frontmatter-equivalent in the
  Status blockquote:** state `analysed` in substance (was open/filed: facts verified, options
  complete), severity `low` and security `hardening` with a threat line (never set before),
  urgency `later` by rule 4 (unchanged: rules 1-3 do not match — no tracked file outside the
  tickets states something false about these constraints — and the decisions are test-design
  choices, not product calls), effort M (unchanged), blocked by Decisions 1 and 2. No YAML block
  was added, because `README.md:115-117` names 042 as frontmatter-less; adding one needs that
  paragraph changed in the same change. Cross-ticket: ADR 0020 edits wait on ticket 040 item 4
  (itself waiting on 040's Decision 1) and are coordinated with ticket 060; ticket 040 work item 8
  may rewrite the close grep's one false positive (`pod_termination_test.go:408`); ticket 033's B1
  is not a duplicate of the unfiled `deleteLegacyServices` ticket; ticket 043 does not apply (no
  build tag). A review of this re-verification fixed two drifted cites the auditor carried
  (ADR 0020 corollary `:208` is `:209`, the board's grooming line `archive/039:7745` is `:7746`),
  un-struck the earlier Constraint 3 correction (its 71 and 82 hold; the new figures are a
  precision, not a second correction) and dropped the drifting `033` line numbers in favour of
  naming B1. Filed: the `deleteLegacyServices` gap (work list item 4) as
  [T77](077-deletelegacyservices-deletes-without-the-ownership-guard.md) (low, hardening, effort S,
  analysed, tracked); its facts, the parked unverified note on its security class and the ticket
  033 B1 recheck moved there, and this ticket keeps only the allowlist line
  `deleteLegacyServices`/`Delete` with pointers under Status, Why, Seeding measurement, Fact, Not
  verified, Decision 1 A, Explicitly out of scope and the work list. Filed: nothing in this
  ticket's state, severity, security class, urgency, effort or decisions rested on the finding, so
  none changes; Decision 1 A notes that its 8 allowlist lines become 7 once T77 lands.
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
