# ADR 0027: Every Condition Is a Level, an Edge or History, and a Test Says Which

## Status

Accepted. Date: 2026-08-26.

Amended 2026-08-26, same day: **D1 and D2 gain the ownership rule** as the second legal
answer for a level with more than one evaluator, and **D4's two declared gaps are closed**
(ADR 0023 D4a, ADR 0002 D10b). `conditionOwnership` gains an `ownershipRule` field and the
registry gains `TestConditionRegistryOwnershipRulesAreEarned`. `make test-unit`, `make lint`
and `make cyclo` green; no e2e or integration suite was run.

Amended 2026-09-26 (T32, T31): **two rows added, and D1 names a third level hazard.**
`PodAvailabilityStalled` ([ADR 0026](0026-a-pod-being-deleted-is-not-available.md) D11) is the
second level with an ownership rule, partitioned by reason rather than reserved to one tier,
and its retraction reads the tier's pods instead of the pass's silence.
`PodSecurityUpdatePending` ([ADR 0032](0032-generated-pods-run-rootless.md) D3) is a level
with one evaluator. Verified 2026-09-26: `make test-unit`, `make lint`, `make cyclo` and
`make test-integration` green; locally on Kind (control plane + 3 workers, Kubernetes v1.36.1),
not in CI, on both Valkey lines
`TestE2E_RollingUpdate_UnavailableReplacementIsReportedAndReplaced` saw the data-tier evaluator
raise `True/ValkeyPodNotAvailable` and retract to `False/PodAvailable`, and no Sentinel pod was
replaced during the 30 s it watched the stall — the gate the ownership rule rests on.
`TestE2E_FleetUpgrade` (`make test-e2e-fleet-upgrade E2E_UPGRADE_FROM=1.12.8`, same Kind
cluster, not a CI job) saw `PodSecurityUpdatePending` raised `True/PodRunsAsRoot` on the
non-persistent single pod the upgrade deliberately leaves running; its retraction and the
Sentinel evaluator of `PodAvailabilityStalled` are verified in the unit tier only.

Implemented as [`internal/controller/condition_registry.go`](../../internal/controller/condition_registry.go)
and guarded by [`condition_registry_test.go`](../../internal/controller/condition_registry_test.go).
The registry is read by nothing in the reconcile path; its only consumer is that test.
Verified in this repository: `make test-unit`, `make lint` and `make cyclo` are green. The
membership guard was confirmed to fail against a registry with a row removed. No e2e or
integration suite was run for the registry — it has no runtime behaviour to exercise; the
2026-09-26 runs above exercised two conditions, not the registry.

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

* `RollingUpdatePaused` is set by `pauseRollingUpdate`, which is reachable on ~~every
  topology~~ **two of the three** — the `default:` arm (`replicas <= 1` without Sentinel)
  has no pause site in its call graph — and ~~cleared only by `finalizeRollingUpdate`, which
  only the Sentinel dispatcher calls~~. A non-Sentinel multi-replica cluster that hits
  `syncTimeout` therefore carried the condition for the life of the cluster, and the obvious
  fix is a trap: `pauseRollingUpdate` calls `clearRollingUpdateState` itself, so a clear
  placed there would erase the report in the same pass that made it.
* `StorageSpecNotApplied` is written by two independent evaluators — the data StatefulSet
  and the Sentinel StatefulSet both call `guardVolumeClaimTemplates`, whose `default` arm
  ~~clears unconditionally~~ cleared unconditionally. Step order is fixed and the Sentinel
  StatefulSet has no `volumeClaimTemplates` at all, so on a Sentinel cluster with a
  data-tier claim conflict the pass reported the conflict and then cleared it. Last writer
  wins, and the last writer is the one that never has anything to say.

> **Both closed 2026-08-26**, and the strikethroughs above mark what stopped being true.
> `RollingUpdatePaused` is now cleared from the two sites in `checkAndHandleRollingUpdate`
> that every dispatch target reaches, the converged one gated on tier convergence, and the
> unguarded `False` write inside `finalizeRollingUpdate` is deleted
> ([ADR 0002](0002-surface-a-blocked-reconcile-on-the-cr.md) D10b).
> `StorageSpecNotApplied` keeps both evaluators and gains an ownership rule: either tier may
> report a conflict, only the data tier may clear one
> ([ADR 0023](0023-volume-claim-templates-are-immutable.md) D4a). The paragraph is left
> standing because it is the argument for the registry existing at all — these two were
> found by writing a table, not by an incident, and that is still the point.

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
| **level** | re-measured from live state on every pass that reaches its evaluator | exactly one evaluator — or several plus a declared **ownership rule** naming which one decides; no clear site, because it corrects itself |
| **edge** | records that something happened, or that work is deferred | a clear at a site that *proves* the precondition is gone, and a presence guard |
| **history** | a verdict about a completed operation | nothing — and it must **never** gain a clear |

The hazards are different per kind, which is why one rule for "conditions" was never
enough. A level's hazard is an evaluator that is not reached, or two evaluators racing to
be last — *extended 2026-09-26:* or an evaluator that is reached on a pass that did not
measure, and reads the silence as the all-clear. `PodAvailabilityStalled` was first written
that way and caught in review before release: every pass that stopped at another wait first
retracted it, and the next pass raised it again (below). An edge's hazard is the missing
clear. History's hazard is the opposite: a well-meaning cleanup that discards the record.

**Amended 2026-08-26: the ownership rule is the second legal answer for a level.** The
`evaluators` docstring described this escape from the day the registry was written and
nothing implemented it, so the only way to keep a two-evaluator level in the table was to
declare a gap. `StorageSpecNotApplied` is the first row that earned it: both StatefulSet
reconcilers genuinely evaluate the condition and both may raise it, and pretending
otherwise by recounting them as one authoritative evaluator would state something the code
contradicts — both sites still call `reportStorageSpecNotApplied`, and the last writer still
owns `Reason` and `Message`. What removes the race is not the count but the rule: only the
data tier may clear (ADR 0023 D4a). A rule that names no site, or that sits on a row with a
single evaluator, fails `TestConditionRegistryOwnershipRulesAreEarned` — the same
traceability the ticket reference gives a declared gap.

*Added 2026-09-26:* the second row is `PodAvailabilityStalled`
([ADR 0026](0026-a-pod-being-deleted-is-not-available.md) D11), and its rule has a different
shape. Nothing is reserved to one tier; the condition is partitioned by reason instead. The
data tier's roll reports `ValkeyPodNotAvailable`, the Sentinel tier's roll
`SentinelPodNotAvailable`, and each evaluator (`reportAvailabilityStall`, called on every
non-error result from `checkAndHandleRollingUpdate` and `checkAndHandleSentinelRollingUpdate`)
raises with its own reason and retracts only a standing True carrying it. What keeps the two
from contending is again the step order plus one gate: the data tier evaluates first, and a
data tier that raises also holds the pass (`DeferredRequeueAfter`), which skips the Sentinel
roll — so the two never both raise in one pass. The gate has one known gap: a paused data roll
(`pauseRollingUpdate`) returns an empty result, so the pass that pauses does enter the
Sentinel roll. A pause carries no stall, so the data evaluator can at most retract in that
pass.

**The retraction is taken on evidence, never on silence.** A result without a stall says only
that this pass did not end on an expired availability wait; it may have stopped at a
terminating pod, at the no-replicas wait after a failover or at a fresher replacement still
inside its budget, none of which looked at the pod the report names. A standing True is
therefore retracted only when `expiredUnavailablePod` finds no pod of the tier — the ordinal
range of its StatefulSet, proven ours — that exists, is not being deleted, is not Ready and has
been not-Ready longer than `syncTimeout` by its own clock. Cache reads only, and only on a pass
that finds its own True standing and carries no stall.

Three consequences follow, and none is a race. One condition carries one reason, so a stall
raised over the other tier's standing report replaces it: a data-tier stall over a Sentinel
report, or — only in the pass that pauses a data roll — a Sentinel stall over a data report
whose pod is still down. A Sentinel report is not re-measured while the data tier rolls,
because its evaluator is not reached then — the first level hazard named above, here a direct
consequence of the Sentinel tier rolling after the data tier
([ADR 0024](0024-the-sentinel-tier-reports-its-own-completion.md) D1): the report stands
until the Sentinel roll is entered again. And a Sentinel report the data tier overwrote comes
back on the first pass that enters the Sentinel roll again, if the Sentinel pod is still down
on the current spec, because that pod's own clock has long expired by then. Disabling Sentinel
moves the Sentinel evaluator to the class exit in `runSentinelRollingUpdate`, next to
`clearSentinelUpdatePending`, which a cluster without Sentinel reaches on every pass the data
tier does not end (a data-tier hold does not skip it), and it retracts on the same evidence:
no code path in `internal/controller` deletes a StatefulSet, so a leftover Sentinel pod that is
still down keeps the report True until it turns Ready or is removed by hand.

*Added 2026-09-26:* `PodSecurityUpdatePending` ([ADR 0032](0032-generated-pods-run-rootless.md)
D3) is a level with one evaluator, although its sibling deferral `SidecarUpdatePending` is an
edge. The deferral is decided in `handleStandaloneRollingUpdate`, which the dispatch enters
only when it finds an outdated pod or a roll still recorded in the state annotation; once the
pod is replaced, the dispatch returns at "no rolling update needed" before it — the exact shape
of the first row of the Context table. So the evaluator, `reportPodSecurityUpdatePending`,
sits one frame up in `checkAndHandleRollingUpdate` next to the data tier's
`reportAvailabilityStall`, and on every non-error result raises
`True/PodRunsAsRoot` when the result names a deferred pod and otherwise writes
`False/PodSecurityUpdateApplied` over a standing True only. Here the silence is a measurement,
which is what separates it from the hazard above: the handler that names the pod is reached on
every pass on which the pod is outdated, and no wait stands in front of that decision.

**A rule is not a proof.** Nothing verifies that the code obeys the rule the row states,
exactly as nothing verifies the `evaluators` count. What the registry buys here is that the
next author adding a third call site reads the rule next to the failure message instead of
discovering it.

**D2 — The classification is declared in a registry, and a test enforces what it can.**
`conditionRegistry` names, per type: the owner site, the kind, the number of evaluators, the
ownership rule when there is more than one, the clear site, whether that clear is
presence-guarded, and any field of the stored condition that something reads as *data*
rather than as a report. The tests assert that every `ConditionType` declared in `api/v1`
appears exactly once, that every edge has a presence-guarded clear, that every level has one
evaluator **or an ownership rule**, that an ownership rule is only claimed where there is
more than one evaluator, and that no history row has a clear site.

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
the decision.** ~~`RollingUpdatePaused` and `StorageSpecNotApplied` are in the registry
today with their gaps declared, because the fixes are decisions that have not been taken.~~
Both were fixed on 2026-08-26 and their gaps are gone; the one gap left is `Ready`/T18,
which is a re-decision request rather than a defect (ADR 0001 D4 decides the current
behaviour). A gap with no `T<number>` reference fails the test: otherwise the escape hatch
becomes a way to silence the guard, and a suppressed invariant is indistinguishable from a
broken one with a comment on it.

The two gaps this ADR shipped with are the evidence for the mechanism rather than a
footnote: both were closed within a day of being written down, one by narrowing an
evaluator (ADR 0023 D4a) and one by moving a clear up one frame and deleting an unguarded
write (ADR 0002 D10b). Neither had an incident behind it.

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
because it is history. An abstraction that leaks on two rows of its own table — two of
eleven when this ADR was written — is not one.

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
* ~~Two known defects are now declared in code rather than living only in a ticket. That is
  more honest and slightly worse-looking: `conditionRegistry` reads as a list of two
  admissions. Both are traceable to their items.~~ **Both were fixed on 2026-08-26**, within
  a day of being written down. What remains declared is `Ready`/T18, which is an open
  re-decision rather than a defect. The mechanism worked in the direction it was built for:
  the declaration was a to-do with a test behind it, not a permanent excuse.
* A level may now have more than one evaluator, which is a real loosening. The
  compensating rule is that the loosening has to be *stated* — an ownership rule naming
  which site decides — and stating one on a single-evaluator row fails its own test.
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

Rejected as insufficient rather than wrong. ~~Both fixes are still owed and are tracked as
their own items~~ — both landed on 2026-08-26 (ADR 0023 D4a, ADR 0002 D10b); what this ADR
adds is the reason the *next* one gets found by a test instead of by a fleet audit. Per-site
fixes have a perfect record of being correct and a perfect record of not preventing the next
instance. The fixes themselves are the evidence for that: neither would have been written
without the table, and one of them needed a new registry field to be expressible at all.

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
* **`evaluators` is a hand-maintained count, and so is `ownershipRule`.** Nothing verifies
  either against the code. A second evaluator added without touching the registry is exactly
  the `StorageSpecNotApplied` defect, and the registry would not catch its recurrence; a rule
  that says the data tier clears while the code clears from both is equally invisible here.
  The tests that do enforce it are the three unit tests ADR 0023 D4a names, one of which
  exists only to pin the step order the rule rests on. For `PodAvailabilityStalled` they are
  `TestCheckAndHandleRollingUpdate_NeverRetractsASentinelReport` and
  `TestCheckAndHandleSentinelRollingUpdate_NeverRetractsADataReport`, one per direction;
  `TestReconcileWorkload_DataAvailabilityStallHoldsTheSentinelRoll` for the gate the rule
  rests on; `TestReportAvailabilityStall_RetractsOnlyOnEvidence` and
  `TestSentinelRollingUpdate_TerminationPriorityDoesNotRetractTheReport` for the evidence
  rule; and `TestHandlePostRollingUpdateChecks_SentinelDisabledRetractsTheSentinelReport` with
  `_SentinelDisabledLeavesOtherCRsAlone` for the class exit — all in
  [`pod_availability_test.go`](../../internal/controller/pod_availability_test.go), green in
  `make test-unit` on 2026-09-26. Two parts of the rule are traced by reading only: no test
  pins the pause gap in the gate, and the class-exit fixture has no Sentinel StatefulSet, so
  it covers only a cluster with no leftover Sentinel pod.
* **The third level hazard is invisible to the guard** (D1, extended 2026-09-26). A row whose
  evaluator is reached on every pass passes every test whether that evaluator measures or
  reads the pass's silence as the all-clear. `PodAvailabilityStalled` was caught by review,
  not by the registry, and the next instance will be too.
* ~~**The two declared gaps are open defects, not accepted designs.**~~ **Both were fixed on
  2026-08-26** — `RollingUpdatePaused` by ADR 0002 D10b, `StorageSpecNotApplied` by ADR 0023
  D4a. The one row still carrying a `declaredGap` is `Ready`/T18, and that one genuinely is
  an accepted design pending a re-decision (ADR 0001 D4), not a defect.
* **A row can pass every test and still be wrong about the fleet.** Neither fix was verified
  against a cluster: both are unit-verified, and one of the `RollingUpdatePaused` probes
  guards a shape that was reproduced in a fixture and never observed in production.
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
* [ADR 0023](0023-volume-claim-templates-are-immutable.md) D4a, [ADR 0026](0026-a-pod-being-deleted-is-not-available.md) D11 — the two levels that carry an ownership rule
* [ADR 0024](0024-the-sentinel-tier-reports-its-own-completion.md) D1 — the tier order `PodAvailabilityStalled`'s rule rests on, and whose pause gap it inherits
* [ADR 0032](0032-generated-pods-run-rootless.md) D3 — `PodSecurityUpdatePending`, and why its evaluator sits one frame above the deferral
* [`internal/controller/pod_availability_test.go`](../../internal/controller/pod_availability_test.go), [`pod_security_migration_test.go`](../../internal/controller/pod_security_migration_test.go) — the unit rules behind the two 2026-09-26 rows
* [ADR 0025](0025-a-split-brain-warning-means-one-that-did-not-resolve-itself.md) — why `MultipleMasters` must not be touched
* [ADR 0010](0010-every-rolling-update-wait-is-bounded.md) D15 — why `TopologyRestored` is history
