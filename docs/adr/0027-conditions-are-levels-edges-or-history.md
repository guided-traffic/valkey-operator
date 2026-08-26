# ADR 0027: Every Condition Is a Level, an Edge or History, and a Test Says Which

## Status

Accepted. Date: 2026-08-26.

Implemented as [`internal/controller/condition_registry.go`](../../internal/controller/condition_registry.go)
and guarded by [`condition_registry_test.go`](../../internal/controller/condition_registry_test.go).
The registry is read by nothing in the reconcile path; its only consumer is that test.
Verified in this repository: `make test-unit`, `make lint` and `make cyclo` are green. The
membership guard was confirmed to fail against a registry with a row removed. No e2e or
integration suite was run for this ADR — there is no runtime behaviour to exercise.

## Context

One failure shape has now been fixed four times, at four different sites, by four
different changes:

| Condition | The clear that was missing | Fixed by |
|---|---|---|
| `SidecarUpdatePending` | its only caller sat on a path that is not entered once the deferred update applies | [ADR 0002](0002-surface-a-blocked-reconcile-on-the-cr.md) D10 |
| `SentinelUpdatePending` | disabling Sentinel mid-roll skips the check that would clear it, forever | [ADR 0024](0024-the-sentinel-tier-reports-its-own-completion.md) D6 |
| `PodTerminationStalled` | the stall report had no clear at the boundary that ends the stall | [ADR 0026](0026-a-pod-being-deleted-is-not-available.md) D5 |
| `SidecarUpdatePending` again | the pass that *completes* a roll got past the D10 site by definition | ADR 0002 D10, amended 2026-08-26 |

The shape is always the same: **the clear sits behind the very code path whose absence
caused the staleness.** Each fix was correct and each was found by an incident or a fleet
audit rather than by looking.

Writing the full inventory down for the first time — eleven condition types, in the course
of analysing a stale-status ticket — immediately produced two more instances that no
incident had surfaced:

* `RollingUpdatePaused` is set by `pauseRollingUpdate`, which is reachable on every
  topology, and cleared only by `finalizeRollingUpdate`, which only the Sentinel dispatcher
  calls. A non-Sentinel cluster that hits `syncTimeout` therefore carries the condition for
  the life of the cluster, and the obvious fix is a trap: `pauseRollingUpdate` calls
  `clearRollingUpdateState` itself, so a clear placed there would erase the report in the
  same pass that made it.
* `StorageSpecNotApplied` is written by two independent evaluators — the data StatefulSet
  and the Sentinel StatefulSet both call `guardVolumeClaimTemplates`, whose `default` arm
  clears unconditionally. Step order is fixed and the Sentinel StatefulSet has no
  `volumeClaimTemplates` at all, so on a Sentinel cluster with a data-tier claim conflict
  the pass reports the conflict and then clears it. Last writer wins, and the last writer
  is the one that never has anything to say.

Two defects found by writing a table, against four found by incidents, is the argument. A
convention that has been missed this often is not a convention.

The inventory also exposed an asymmetry that made every previous table wrong by
construction: `Ready` — the one condition every CR carries — was an unexported string
constant in `internal/controller` and not a `ConditionType` in `api/v1` at all. Any
enumeration built from the API package silently omitted it.

## Decision

**D1 — Every condition is classified as a level, an edge or history, and the classification
decides what it owes.**

| Kind | Meaning | Owes |
|---|---|---|
| **level** | re-measured from live state on every pass that reaches its evaluator | exactly one evaluator; no clear site, because it corrects itself |
| **edge** | records that something happened, or that work is deferred | a clear at a site that *proves* the precondition is gone, and a presence guard |
| **history** | a verdict about a completed operation | nothing — and it must **never** gain a clear |

The hazards are different per kind, which is why one rule for "conditions" was never
enough. A level's hazard is an evaluator that is not reached, or two evaluators racing to
be last. An edge's hazard is the missing clear. History's hazard is the opposite: a
well-meaning cleanup that discards the record.

**D2 — The classification is declared in a registry, and a test enforces what it can.**
`conditionRegistry` names, per type: the owner site, the kind, the number of evaluators, the
clear site, whether that clear is presence-guarded, and any field of the stored condition
that something reads as *data* rather than as a report. The tests assert that every
`ConditionType` declared in `api/v1` appears exactly once, that every edge has a
presence-guarded clear, that every level has one evaluator, and that no history row has a
clear site.

This is the [ADR 0014](0014-rbac-lives-in-three-places.md) idiom: RBAC drift is caught by a
test rather than by a convention because the convention had been missed. Same reasoning,
same shape.

**D3 — The registry is parsed out of `api/v1`, not reflected over.** `ConditionType` is a
type **alias** for `string`, so there is no distinct runtime type to enumerate and no
reflection that could tell a condition constant from any other string constant. The test
parses `api/v1/valkey_types.go` with `go/parser` and matches on the declared type, not on
the identifier prefix — so a constant someone names `ConditionTypeSomething` without giving
it the type is not silently accepted as covered.

**D4 — A declared gap suppresses the invariants, and must name the ticket item that owns
the decision.** `RollingUpdatePaused` and `StorageSpecNotApplied` are in the registry today
with their gaps declared, because the fixes are decisions that have not been taken. A gap
with no `T<number>` reference fails the test: otherwise the escape hatch becomes a way to
silence the guard, and a suppressed invariant is indistinguishable from a broken one with a
comment on it.

**D5 — `Ready` is a `ConditionType` in `api/v1` like every other condition.** It moved out
of `internal/controller`, which is what makes D3's enumeration complete, and it carries the
contract that distinguishes it from `status.phase`
([ADR 0002](0002-surface-a-blocked-reconcile-on-the-cr.md) D5a).

**D6 — The registry changes no behaviour, and must not start to.** Nothing in the reconcile
path reads it. It is a declaration checked by a test, not a dispatch table: a registry the
reconciler consulted would become a second authority beside each producer, which is what D7
rejects.

**D7 — There is no central condition-GC pass.** The producer stays the one reporter. A
function that walked every condition type and flipped the stale ones would contradict
[ADR 0002](0002-surface-a-blocked-reconcile-on-the-cr.md)'s one-reporter design,
[ADR 0025](0025-a-split-brain-warning-means-one-that-did-not-resolve-itself.md)'s rule that
the split-brain condition is written at the resolver's call sites and never inside it, and
[ADR 0026](0026-a-pod-being-deleted-is-not-available.md) D5's rule that the gate sits at
the delete and never at a function head. It would also have to hard-exclude the first two
entries of its own table — `MultipleMasters`, because a flip resets the
`splitBrainWarnAfter` deadline read from its `LastTransitionTime`, and `TopologyRestored`,
because it is history. An abstraction that leaks on two of eleven rows is not one.

**D8 — No condition is ever deleted.** `meta.RemoveStatusCondition` is called nowhere in
the tree, and nothing here changes that. The only lifecycle is True↔False, which is why the
presence guard is the entire upgrade-neutrality story: `meta.SetStatusCondition` **adds** an
absent condition and reports a change, so an unguarded clear writes the condition onto every
CR in the fleet on the first upgraded pass
([ADR 0005](0005-upgrade-neutral-defaults-and-anti-affinity.md) D10).

## Consequences

* A new condition type costs a registry row, or the unit tier goes red. That is the point,
  and it is also the cost: the row has to be written by whoever adds the condition, at the
  moment they still know which kind it is.
* The table has to be kept current, which is the same discipline the ADRs already demand
  and the same way it can rot. The membership half is enforced; see Residual risks for the
  half that is not.
* Two known defects are now declared in code rather than living only in a ticket. That is
  more honest and slightly worse-looking: `conditionRegistry` reads as a list of two
  admissions. Both are traceable to their items.
* The four write styles the inventory found (in-memory batched via `persistStatus`;
  `setStatusCondition`; `writeStatusCondition` with the caller consuming `(changed, err)`;
  the presence-guarded clear wrapper) are **not** consolidated. Each is deliberate and
  ADR-backed, and a fifth style is the thing to avoid, not a fourth-to-one refactor.
* `Ready` moving into `api/v1` adds an exported string constant to the API package. No
  schema change, no CRD regeneration — it is a constant, not a field.

## Alternatives Considered

### A single `reconcileConditions` GC pass in `Reconcile`

Rejected, and it is the option this ADR exists to close. See D7: it contradicts three
existing ADRs on where a condition may be written, and it must exclude `MultipleMasters`
and `TopologyRestored` — the two rows where getting it wrong is destructive rather than
cosmetic. The attraction is real (one site, no per-producer discipline) and it is exactly
the attraction that makes it dangerous.

### `pruneStaleConditions` inside the batched status pass

Rejected. It would cost zero extra API calls, since `statusUnchanged` already diffs the
condition slice and `persistStatus` already writes once — genuinely the cheapest design.
But it inherits `updateStatus`'s reachability: `reconcileWorkload` returns before it on the
rolling-update exits ([ADR 0001](0001-continue-reconciling-past-a-rejected-write.md) D4), so
a cluster whose roll requeues indefinitely — the exact shape the stale-condition findings
came from — would never GC. A cleanup that is skipped in the situation it exists for is not
one.

### Fix the two found defects and skip the registry

Rejected as insufficient rather than wrong. Both fixes are still owed and are tracked as
their own items; what this ADR adds is the reason the *next* one gets found by a test
instead of by a fleet audit. Per-site fixes have a perfect record of being correct and a
perfect record of not preventing the next instance.

### Enforce the classification, not just the membership

Considered and not attempted. A test cannot tell that a level was declared as history: both
compile, both pass, and the only way to know is to read the writers. Encoding the semantics
would mean encoding each condition's precondition, which is D7's central-GC design wearing
a test's clothes.

## Residual risks

* **A wrongly classified row still passes.** The guard catches a missing row, a missing
  clear, an unguarded clear, racing evaluators and a history row that grew a clear. It
  cannot catch a row that names the wrong kind or a `clearSite` string that describes a
  function which no longer clears anything — the field is prose, not a symbol reference, so
  a rename leaves it silently stale. That half stays a review question, which is stated here
  rather than implied.
* **`evaluators` is a hand-maintained count.** Nothing verifies it against the code. A
  second evaluator added without touching the registry is exactly the
  `StorageSpecNotApplied` defect, and the registry would not catch its recurrence.
* **The two declared gaps are open defects, not accepted designs.** `RollingUpdatePaused`
  standing forever on a non-Sentinel cluster is user-visible and wrong; the
  `StorageSpecNotApplied` race means a Sentinel cluster can report `False` while its data
  tier has an unappliable storage spec. Both are latent on the fleet that was inspected —
  no cluster currently carries either — but latent is not fixed.
* **`MultipleMasters` standing stale outside a rolling update is accepted** by
  [ADR 0025](0025-a-split-brain-warning-means-one-that-did-not-resolve-itself.md)'s own
  Residual risks, and the registry deliberately does not flag it. Anything that later
  "cleans up" that row breaks the split-brain deadline.
* **Not verified:** the test parses one file at a fixed repo-relative path. If the condition
  constants are ever split across files, the parser silently covers less than it claims —
  it fails loudly only when it finds *no* constants at all, not when it finds fewer.

## References

* [`internal/controller/condition_registry.go`](../../internal/controller/condition_registry.go) — the registry
* [`internal/controller/condition_registry_test.go`](../../internal/controller/condition_registry_test.go) — the guard
* [`api/v1/valkey_types.go`](../../api/v1/valkey_types.go) — the condition types, and `ConditionTypeReady`'s contract
* [ADR 0002](0002-surface-a-blocked-reconcile-on-the-cr.md) — D7–D10 on how conditions are written, D5a on `Ready`
* [ADR 0014](0014-rbac-lives-in-three-places.md) — the same idiom for RBAC drift
* [ADR 0024](0024-the-sentinel-tier-reports-its-own-completion.md) D6, [ADR 0026](0026-a-pod-being-deleted-is-not-available.md) D5 — two of the four historical instances
* [ADR 0025](0025-a-split-brain-warning-means-one-that-did-not-resolve-itself.md) — why `MultipleMasters` must not be touched
* [ADR 0010](0010-every-rolling-update-wait-is-bounded.md) D15 — why `TopologyRestored` is history
