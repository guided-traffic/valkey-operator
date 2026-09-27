# T42 - Enforce the standing constraints with a static-analysis test net

> **Status:** analysed, open. Severity low (nothing breaks without the net), security hardening
> (no attack path today), urgency later, effort M. Test 2 and the package-variable half of
> test 1 need no decision; test 3 waits on Q1, the reconciler-field pin on Q2.

## Current state

Three rules are standing constraints on future code, held today by review only. All three hold
on the current tree; this is preventive work, not a repair.

1. **No fleet-wide reconciler state** ([ADR 0019](../adr/0019-reconcile-concurrency-and-the-cost-of-a-stuck-pass.md)
   D3, stated for all of `internal/`). It is the safety argument for four concurrent reconciles.
   `internal/` holds six package-level `var`s, none assigned after its declaration:
   `conditionRegistry` ([`condition_registry.go:92`](../../internal/controller/condition_registry.go#L92)),
   `errForeignObject` ([`foreign_object.go:71`](../../internal/controller/foreign_object.go#L71)),
   `errSeccompProfileNotAllowed` and `errUserNamespacesDropped`
   ([`pod_hardening.go:22`](../../internal/controller/pod_hardening.go#L22), [`:46`](../../internal/controller/pod_hardening.go#L46)),
   `errRecreateRequired` ([`volumeclaim_conflict.go:46`](../../internal/controller/volumeclaim_conflict.go#L46))
   and `tlsMaterialKeys` ([`internal/builder/tls_material.go:41`](../../internal/builder/tls_material.go#L41)).
   The one cross-CR state is a reconciler field, `nudges nudgeTracker`
   ([`valkey_controller.go:112`](../../internal/controller/valkey_controller.go#L112)), a
   mutex-guarded `map[types.NamespacedName]time.Time` ([`nudge.go:55-58`](../../internal/controller/nudge.go#L55-L58)),
   safe only because every key carries namespace and name (`waitBoundKey`,
   [`rolling_update.go:1152-1154`](../../internal/controller/rolling_update.go#L1152-L1154)).
   `ValkeyReconciler` ([`valkey_controller.go:75-113`](../../internal/controller/valkey_controller.go#L75-L113))
   has 12 fields; the flag fields are set once in `newReconciler`
   ([`cmd/main.go:113-125`](../../cmd/main.go#L113-L125)). New fields are added regularly
   (four in the last weeks, three of them operator flags).
2. **No metric written from a reconcile pass** ([ADR 0021](../adr/0021-per-resource-metrics-and-the-alert-that-was-missing.md)
   D3). The `vko_valkey_*` series come from the collect-time collector in
   `internal/metrics/collector.go`. The reconciler's import closure under `internal/` is
   `controller`, `builder`, `common`, `health`, `valkeyclient`; none of them imports
   `client_golang`, `controller-runtime/pkg/metrics` or `internal/metrics`. `client_golang` is
   imported only by `internal/metrics` and `internal/observer` (the separate observer process
   registers its own gauges, [`observer/metrics.go:10-18`](../../internal/observer/metrics.go#L10-L18)).
3. **A new managed kind inherits the ownership guard** ([ADR 0020](../adr/0020-write-only-what-the-operator-owns.md)
   D1/D2, [ADR 0006](../adr/0006-delete-only-what-the-operator-owns.md)). Every write and delete
   on a generated name is guarded by `IsControlledBy`, `deleteIfOwned` or `podIsOurs`, except
   `deleteLegacyServices` ([`valkey_controller.go:936-962`](../../internal/controller/valkey_controller.go#L936-L962)),
   whose fix is T77. Nothing stops a future function from calling `r.Update` on the strength of
   a name.

`internal/controller/standing_constraints_test.go` does not exist and nothing equivalent does;
the only `go/ast` test, [`condition_registry_test.go`](../../internal/controller/condition_registry_test.go#L3-L6),
enforces a different rule. The precedent for a "test as lint" is
[`rbac_drift_test.go`](../../internal/controller/rbac_drift_test.go).

**Test 3 seeding, measured with a static `go/ast` scan** of the non-test files of
`internal/controller`, using the verb and guard lists under Required changes: 77 call sites in
72 `(function, verb)` pairs, 22 of them unguarded:

- 14 write the CR itself: 11 annotation writes `r.Update(ctx, v)` in
  [`rolling_update.go`](../../internal/controller/rolling_update.go) (`persistKnownMaster`,
  `ensureWaitBound`, `clearRecreationWait`, `clearSyncWaitTimestamp`,
  `incrementReconnectResetCount`, `clearReconnectResetCount`, `setRollingUpdateState`,
  `clearRollingUpdateState`, `setFailoverTriggered`, `setFailoverTimestamp`, and
  `writeManualFailoverState` twice, once on `fresh := &vkov1.Valkey{}` inside a
  `retry.RetryOnConflict` literal), and 3 `r.Status().Update(ctx, v)` in `persistStatus`,
  `writePhase` and `writeStatusCondition` ([`valkey_controller.go:2570`](../../internal/controller/valkey_controller.go#L2570), `:2628`, `:2709`).
- `writeWorkload`/`Create` and `writeWorkload`/`Update` ([`pod_hardening.go:59`](../../internal/controller/pod_hardening.go#L59), `:61`); its callers prove ownership with `IsControlledBy`.
- `deleteOwnedPod`/`Delete` ([`foreign_object.go:307`](../../internal/controller/foreign_object.go#L307)).
  It proves nothing; it sends only the UID precondition for a pod its caller has proven
  (`DEVELOPER.md:423-424`: prove with `podIsOurs`, delete with `deleteOwnedPod`).
- Four `deleteOwnedPod` callers with no proof in their own body: `replaceNextReplica`,
  `replaceRemainingPods`, `deleteNextPendingPod`, `handleManualFailover`. Their pods come from
  `collectPodStates`, which filters with `podIsOurs` ([`rolling_update.go:1931`](../../internal/controller/rolling_update.go#L1931)).
- `deleteLegacyServices`/`Delete` (T77).

All 13 `controllerutil.SetControllerReference` calls pass the CR first: `(v, desired, r.Scheme)`.
There is no `Apply`, `DeleteAllOf`, `SubResource` or non-`r` write today.

The rule does not prove a guard acts on the right object: applied to the tree before the ADR
0020 D9 fix, it flagged 5 of the 6 unproven pod deletes and an unproven pod `Patch`, but passed
the Sentinel pod delete because the function referenced `IsControlledBy` on the Sentinel
StatefulSet.

**Impact.** Without the net, the next package-level map, reconciler map field,
reconcile-written gauge or unguarded write onto a generated name lands with every test green.
For constraint 3 that is the door through which a principal who may create `Valkey` CRs
overwrites or garbage-collects a foreign object by naming it.

## Required changes

One new file, `internal/controller/standing_constraints_test.go`: stdlib `go/parser`, `go/ast`,
`go/token` only; locates the tree with `repoRoot` ([`rbac_drift_test.go:63`](../../internal/controller/rbac_drift_test.go#L63));
no build tag and no `testing.Short()`, so it runs in `make test-unit` and the required
`Unit Tests` check. Failure messages name file, function or variable, and the ADR; they cite
ADRs only. Keep functions under complexity 15 (a review item: `make cyclo` ignores `_test.go`).

### Independent of the open questions

- **Test 1 `TestNoPackageLevelMutableState`, package-variable half.** Scan every package under
  `internal/`; fail on any package-level `var` not on a named allowlist of the six above, each
  with a one-line justification. Fail on any assignment, index assignment, `++`/`--` or `append`
  targeting an allowlisted variable anywhere in `internal/`, tests included. Message: "package-level
  mutable state breaks MaxConcurrentReconciles > 1 - see ADR 0019 D3; carry the state per-CR
  (keyed by namespace/name) or per-pass (on the context) instead".
- **Test 2 `TestNoMetricsWrittenFromReconcile`.** Compute the reconciler's import closure under
  `internal/` from the imports of `internal/controller`; fail on any import with prefix
  `github.com/prometheus/client_golang/`, on `sigs.k8s.io/controller-runtime/pkg/metrics` and on
  the module's `internal/metrics`. Do not scan `internal/sidecar` or `internal/observer`. No
  allowlist. The doc comment states that the import ban approximates ADR 0021 D3 and also forbids
  process-global metrics in shared packages. Message: "add the series to the collect-time
  collector in internal/metrics/collector.go; never write a gauge from a reconcile pass".
- **Mutations** (revert each): (1) `var passCounter = map[string]int{}` in `internal/controller`
  fails test 1; (2) `_ = prometheus.NewRegistry()` inside a function in `valkey_controller.go`
  fails test 2; (7) the same in `internal/health`, and separately an `internal/metrics` import
  used in a function body of `internal/controller`, each fail test 2.
- Status notes on ADR 0019 D3 and ADR 0021 D3 (the latter with the approximation sentence); two
  rows in the convention-test table, [`docs/developer/testing.md:48-53`](../developer/testing.md).

### Depends on the answers

- **Test 3 `TestEveryWriteAndDeleteIsGuardedOrAllowlisted`** (Q1). Design fixed apart from Q1:
  - Verbs: the full `client.Writer` set (`Create`, `Update`, `Patch`, `Delete`, `Apply`,
    `DeleteAllOf`) and `SubResourceWriter` set via `Status()` or `SubResource(...)`, on any
    receiver; `controllerutil.SetControllerReference`; the wrappers `writeWorkload` and
    `deleteOwnedPod`.
  - Guards: `IsControlledBy`, `deleteIfOwned`, `podIsOurs`, `podUnderNameIsOurs`,
    `ownedDataStatefulSet`, `filterOwnedPods`, `legacySentinelSecretIsOurs`
    ([`foreign_object.go:182-305`](../../internal/controller/foreign_object.go#L182),
    [`valkey_controller.go:1733`](../../internal/controller/valkey_controller.go#L1733)).
  - A pair passes when the enclosing function body (function literals included, no call-graph
    transitivity) references a guard, or when `"funcName/verb"` is on a `map[string]string`
    allowlist whose value names the proving function and guard, never a line number.
  - If the seeded allowlist exceeds about 15 entries, stop and discuss the matching depth with
    Hans. Report the seeded allowlist to Hans before finishing.
  - The doc comment says it is a review hook, not a soundness proof, and names the gap: a guard
    on a StatefulSet passes a pod write next to it.
  - Mutations: (3) remove the `IsControlledBy` in `reconcileConfigMap`
    ([`valkey_controller.go:843`](../../internal/controller/valkey_controller.go#L843)): its
    `SetControllerReference`, `Create` and `Update` pairs fail; (4) a new `reconcileWidget` with
    a bare `r.Update` of a ConfigMap fails, and passes once allowlisted; (5) a guard-free
    function calling `SetControllerReference(v, obj, r.Scheme)` fails; (6) a pod `Get` by name
    passed to `r.deleteOwnedPod` without `podIsOurs` fails.
  - ADR 0020 status note on D1/D2 with what the test does not prove (wrong object, transitivity,
    with the Sentinel-delete example above) and a residual risk: an unjustified allowlist line
    defeats the net, review of that line is the defence. Do this after T40 has rewritten ADR
    0020, and coordinate with T60, which also amends ADR 0020.
  - Pointer in the managed-object checklist, [`DEVELOPER.md:410-430`](../../DEVELOPER.md),
    stating that `deleteOwnedPod` counts as a verb, not a guard.
- **Reconciler-field pin** (Q2, only under B): mutation (8) a map field added to
  `ValkeyReconciler` fails test 1 naming the field and ADR 0019 D3; a step in the operator-flag
  checklist, `DEVELOPER.md:432-437`.

**Close:** the extraction is the ADR status notes and the developer pointers. The close grep
`git grep -nwE 'T42|042|S1'` outside `docs/tickets/` may hit
`internal/controller/pod_termination_test.go:408`, an unrelated `S1` label; leave it.

**Out of scope:** `findMaster`'s ordinal-indexed collection (not lintable), whether a guard acts
on the right object, any production code change, the `deleteLegacyServices` fix (T77).

## Open questions

### Q1: Should test 3 exempt a write on the reconciled `Valkey` CR structurally, or list each one on the allowlist?

14 of the 22 unguarded pairs write the CR itself, which is the owner and needs no guard. The
choice decides whether they leave the allowlist through a rule or appear as 14 identical lines.
It changes nothing for writes onto other objects.

- **A. Structural exemption (recommended).** A call is exempt when its object argument is an
  identifier the enclosing function declares as a `*vkov1.Valkey` parameter or assigns from
  `&vkov1.Valkey{}`. The object argument is pinned per verb: the one after `ctx`, and for
  `SetControllerReference` the second (controlled) argument, never the owner. About 30 lines of
  `go/ast`. Leaves 8 allowlist lines (7 once T77 lands), each with a distinct justification.
  Hole: a second `*vkov1.Valkey` parameter would be exempted; none exists.
- **B. Allowlist every pair.** No rule in the test; 22 entries, 14 of them "the CR is the owner",
  growing with every annotation helper. Crosses the stop point of about 15.

A matches exactly the 14 CR writes on today's tree and fails safe (an unmatched shape shows as
unguarded). B's repeated line is the justification that, copied onto a non-CR write, defeats
the net.

**Answer:** _open_

### Q2: Should test 1 also pin the field sets of `ValkeyReconciler` and `nudgeTracker`?

The only cross-CR state in the tree is a reconciler field, while all six package variables are
immutable. A new map field keyed by pod name would be fleet-wide state that the package-variable
test does not see.

- **A. Package-level variables only.** No extra code, no friction for new operator flags; the
  reconciler struct stays a review convention.
- **B. Pin the fields by name through `reflect` (recommended).** The 12 fields of
  `ValkeyReconciler` and `mu`, `first` of `nudgeTracker`, each with a justification; a new field
  fails with a message citing ADR 0019 D3. About 25 lines, plus one allowlist line per new
  field and a checklist step for flags. Proves a field was reviewed, not that its keys are
  per-CR, and cannot see state behind the interface and func fields.

B covers the struct where the next violation would most plausibly be added, and its message
carries the rule to an author who does not know it.

**Answer:** _open_

## Not verified

- `make test-unit`, `make lint` and `make cyclo` were not run; the counts come from a static
  scan that matches guards by identifier. Implementing the tests settles it.

## Related

- T77: fixes `deleteLegacyServices`; its fix removes one test-3 allowlist line.
- T40: rewrites ADR 0020 first; the ADR 0020 note here follows it.
- T60: also amends ADR 0020 (D9); contents do not conflict.
