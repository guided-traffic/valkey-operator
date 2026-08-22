# ADR 0001: Continue Reconciling Past a Rejected Sub-Resource Write

## Status

Accepted. Date: 2026-08-21.

Implemented on branch `feat/support-pdb` (51 commits ahead of `main` at the time
of writing; not yet released). Verified by
[`internal/controller/reconcile_steps_test.go`](../../internal/controller/reconcile_steps_test.go)
and the e2e `TestE2E_AdmissionRejection_ReconcileContinuesPastRejectedWrite`.

## Context

On 2026-08-19 a worker-node drain on cluster infra-d evicted the single-replica
`kyverno-admission-controller` together with all three data pods of an
`oauth2-valkey` cluster. For about 90 s `kyverno-svc` had no endpoints, and the
Kyverno mutating webhook `mutate.kyverno.svc-fail` (`failurePolicy: Fail`,
matching `pods`, `deployments`, `statefulsets`) rejected every matching create
cluster-wide. Every figure and object name in this paragraph was observed on that
cluster during the incident and is not reproducible from this repository; the only
in-tree record is prose — the header comment of
[`test/e2e/admission_recovery_test.go`](../../test/e2e/admission_recovery_test.go),
whose fixture webhook rebuilds the *shape* of the rejection rather than replaying
the incident.

The operator wrote the Sentinel StatefulSet part-way through its reconcile — after
the data StatefulSet, which is written before it — and returned that error
immediately. Everything after it — NetworkPolicies, monitoring, the health and
rolling-update handling, and `updateStatus` — was skipped for the whole rejection
window. The CR stopped reflecting the data plane while a single unrelated write
was blocked, so an operator user could not tell "my pods are gone" from "the
operator stopped looking".

Two further properties made the stall worse than the webhook gap itself:

* controller-runtime's default per-item rate limiter caps the exponential backoff
  at **1000 s**. The exponent advances once per failed pass and the delay between
  passes *is* the backoff, so the wait before the next look grows to roughly the
  length of the outage — about 41 s away after 41 s of failure, 5.5 min after
  5.5 min, the ceiling after some 22 min.
* Nothing else wakes the operator while it waits. CR status writes are filtered by
  `GenerationChangedPredicate`, there is no Pod watch, and a *rejected* write does
  not mutate the object, so `Owns()` fires no event either.

A rejected write is therefore not an exceptional error path in this operator. It
is an ordinary runtime state that can last minutes, and the operator is the only
component that notices when it ends.

## Decision

**D1 — `reconcileResources` is a declarative step list; every applicable step
runs.** It is a `[]reconcileStep{name, when, run}` executed by `runReconcileSteps`
([`internal/controller/valkey_controller.go`](../../internal/controller/valkey_controller.go)).
Each failure is wrapped with its step name (`"StatefulSet: ..."`) and all failures
are returned as one `errors.Join`. The same helper replaces the abort-on-first-error
chains inside `reconcileServices`, `reconcileMonitoringResources` and
`reconcileMetrics`. The `when` predicate replaces the previous `if` chains, which
also keeps the function under the repo's cyclomatic-complexity limit of 15.

The relative order of the pre-existing steps is unchanged, and it is dependency
order by convention. `PodDisruptionBudgets` is the one entry without a counterpart
on `main` — it arrives on this branch with the PDB feature (commit `604cd91`,
[ADR 0004](0004-opt-in-poddisruptionbudgets.md)) and is inserted after the Sentinel
step:

```
ConfigMap → replica ConfigMap → TLS Certificates → Services → sidecar RBAC
→ StatefulSet → Sentinel resources → PodDisruptionBudgets → NetworkPolicies
→ monitoring (Observer, metrics Service, ServiceMonitor)
```

**D2 — Steps reference the objects of earlier steps by name only.** This is the
precondition that makes continuing safe: no step reads back an object a failed
predecessor was supposed to create, so a rejected write degrades one object
instead of corrupting the rest of the pass. A future step that needs a *live read*
of an object an earlier step writes breaks this argument and must gate itself.

**D3 — The data-plane half of the pass runs either way.** `Reconcile` no longer
returns on a resource error. Rolling update, post-rolling checks, the StatefulSet
nudge ([ADR 0003](0003-nudge-a-short-of-pods-statefulset.md)), `updateStatus` and
the requeue live in `reconcileWorkload` and run whether or not `reconcileResources`
succeeded; the joined error is returned afterwards.

**D4 — A failed `reconcileResources` never skips `updateStatus`.** Status is
observation, not a side effect of a successful write. `readyReplicas`, `masterPod`
and the conditions keep being written while the operator cannot write its managed
objects; only the phase is overridden afterwards (see
[ADR 0002](0002-surface-a-blocked-reconcile-on-the-cr.md)). The rolling-update exits
of `reconcileWorkload` still own their own returns: a pass with a rolling update in
flight — blocked or not — returns before `updateStatus` and writes its phase
itself.

**D5 — A failing pass returns its error, never a hand-picked `RequeueAfter`.**
Shaping retry cadence is the rate limiter's job. Swallowing the error would give a
flat 5 s retry but drop the pass out of `controller_runtime_reconcile_errors_total`
and out of controller-runtime's error log — trading the operator's only alertable
signal for a marginal latency gain.

**D6 — The per-item retry backoff is capped at 30 s.** `newReconcileRateLimiter`
([`internal/controller/ratelimiter.go`](../../internal/controller/ratelimiter.go))
restores client-go's classic `DefaultTypedControllerRateLimiter` shape —
`MaxOf(per-item exponential, overall token bucket)` with the bucket at 10 qps /
burst 100 — and caps the exponential at `reconcileRetryMaxDelay = 30 s` from
`reconcileRetryBaseDelay = 5 ms`. The bucket is *added*, not kept: with
controller-runtime v0.24.1 (`go.mod`) the default rate limiter is the bare per-item
exponential limiter (5 ms → 1000 s, no overall bucket) whenever the priority queue
is used, and the priority queue is on unless `UsePriorityQueue` is set to false,
which `SetupWithManager` does not do. 5 ms doubling reaches the cap only after 13
consecutive failures (~41 s of continuous rejection), so it is still a genuine
backoff.

**D7 — Every reconcile exit keeps a retry clock, and only one.** No path may return
`ctrl.Result{}, true, nil`. A path that already carries its own requeue keeps it and
is not converted for symmetry — the no-master recovery branch of
`handlePostRollingUpdateChecks` keeps `RequeueAfter: 10 s` with an in-code comment
saying why, because giving it the rate limiter as well would hand one path two
competing clocks.

**D8 — Status messages stay single-line.** The joined `reconcileResources` error
passes through `compactErrorMessage`
([`internal/controller/reconcile_blocked.go:62`](../../internal/controller/reconcile_blocked.go))
on both surfaces that carry it — the `ReconcileBlocked` condition and the
blocked-pass phase write — which folds `errors.Join`'s newlines into `"; "`. The
remaining status writes format a single error with `%v` and are not compacted; they
are single-line today only because `errors.Join` is used at exactly two sites and
neither feeds them. `kubectl get`/`describe` and Lens are the surfaces this exists
to serve.

**D9 — Managed-object writes assign only owned fields.** `reconcileStatefulSet`
assigns `Spec.Replicas`, `Spec.Template`, `Labels` and the operator-version
annotation onto the live object, and `ApplyOperatorVersion` merges into the existing
annotation map. This is what lets operator-written state on the StatefulSet — notably
the nudge annotation — survive an operator-driven update.

**D10 — Builders set every field the API server defaults.** A field left `nil` is
defaulted server-side and then reads as permanent drift against the builder output.
`buildSentinelPodSpec` sets `TerminationGracePeriodSeconds` explicitly
(`sentinelTerminationGrace = 30`, the Kubernetes default, deliberately *not* the data
path's 75 — Sentinel has no failover timeout to wait out on shutdown). Before this,
`SentinelStatefulSetHasChanged` reported drift on every pass: 460 `"Updating Sentinel
StatefulSet"` lines against 23 creates in one e2e log. Those two counts were taken
from a local e2e run's operator log, which is not committed here, so the ratio is not
reproducible from this repository. The mechanism behind it is checkable: on `main`,
`buildSentinelPodSpec` never assigns `TerminationGracePeriodSeconds`, and
`terminationGracePeriodEqual` returns `false` when exactly one side is `nil`.

## Consequences

* The error a pass returns is a joined multi-error, so every consumer must cope with
  joined text. The classification path (`reconcileBlockedReason` /
  `isAdmissionRejection`) needed no change precisely because its message matching ORs
  the admission shapes across the whole joined message; `setReconcileBlockedCondition`
  itself only gained the `compactErrorMessage` call of D8.
* A blocked pass costs up to three status writes instead of one: the
  `ReconcileBlocked` condition, `updateStatus`, and the final phase write.
  `setStatusCondition` issues its own `Status().Update`, so the condition is a write
  of its own — on the first blocked pass, and on the first pass after a generation
  change or a changed message; `setReconcileBlockedCondition` skips it while reason,
  message and observed generation are identical. The other two dedupe as well
  (`persistStatus` via `statusUnchanged`, `writePhase` on phase and message), so a
  cluster that stays blocked on the same rejection settles back to no status write
  per pass. Healthy passes are unchanged.
* Retry latency is bounded by `reconcileRetryMaxDelay` (30 s) rather than by a
  shorter hand-picked interval. Cadence measured against a fail-closed webhook that
  rejected `CREATE configmaps` for four minutes, passes extracted from the operator
  log: gaps of 1, 1, 1, 2, 6, 10, 20, 30, 30, 30 s — flat exactly at the cap.
  Recovery measured in the same run: `ReconcileBlocked=False` 13 s after the webhook
  was removed, where the old limiter's pending delay would have been 163.84 s
  (computed from 5 ms doubling, not observed). The gaps are wall clock between two
  passes read off 1 s-resolution log timestamps, not the raw backoff, so they track
  the doubling series (0.64, 1.28, 2.56, 5.12, 10.24, 20.48, 30) only approximately.
  That log is not committed here, so neither measurement is reproducible from this
  repository; the in-tree guards are the unit tests in
  [`internal/controller/ratelimiter_test.go`](../../internal/controller/ratelimiter_test.go).
* The two exits of `handlePostRollingUpdateChecks` are deliberately asymmetric; that
  only stays legible because the comment records the reason.
* Making the Sentinel grace period explicit changed `ComputeSentinelPodSpecHash`, so
  the first reconcile after that upgrade rolls the Sentinel pods once. They are
  stateless (config rebuilt by the init container) and rolled serially, so quorum
  holds.
* `golang.org/x/time` moved from indirect to direct in `go.mod` (already in the build
  graph via client-go; only the requirement line changed).

## Alternatives Considered

### Keep the abort-on-first-error chain

The shipped behaviour before this ADR, and exactly the failure mode above: one
rejection on one sub-resource stalls the whole data plane and freezes the CR status.

### Express continue-past-failure as further `if` chains

Functionally equivalent, but it would push `reconcileResources` past the repo's
cyclomatic-complexity threshold of 15. The step list is the cheaper shape.

### A dependency graph between steps

Rejected as over-engineered for a fixed, short list. The cost is that dependency
order is convention: nothing enforces it, so a reordering has to be reviewed.

### Swallow the error and return a flat `RequeueAfter: 5s`

Marginally faster retry, but the reconcile disappears from the error metric and the
controller-runtime error log. Rejected on observability grounds — see D5.

### Leave controller-runtime's 1000 s backoff default

Rejected: the wait grows to roughly the length of the outage, and everything hanging
off a pass inherits it — the `ReconcileBlocked` condition keeps naming a webhook that
is long healthy, the phase stays `Error`, and the nudge slows with it.

### Teach `terminationGracePeriodEqual` to treat `nil` as 30

Would have fixed the one observed drift. Rejected in favour of the builder-side rule
(D10), which generalises to any server-defaulted field.

## Residual risks

* **Foreign labels on managed objects are dropped.** Label handling is assignment
  (`current.Labels = desired.Labels`) at 11 managed-object sites including the PDB.
  Third-party labels (cost allocation, policy selectors) are silently removed on the
  next update. Changing it is a convention-wide change; an in-isolation deviation for
  one object would be worse than the uniform rule.
* **A CR deleted mid-pass survives further than it used to.** Since the pass continues
  past a failed status write, `reconcileResources` can create children carrying an
  ownerReference to a gone UID. Cost measured in what it can actually do: a handful of
  API writes that GC undoes within seconds, and the loop terminates — the next
  reconcile Gets NotFound at the top of `Reconcile`, forgets the nudge state and
  returns. That cost is read off the code path and Kubernetes' ownerReference GC
  semantics; the race was not reproduced against a cluster. **If this is ever fixed it
  must be one guard for the whole pass** (re-read the CR once after
  `reconcileResources` and return on NotFound), never a per-call-site `IsNotFound`,
  which would make the pass inconsistent rather than correct.
* Per-CR in-memory tracker state is dropped by `forgetNudges` on exactly the two
  `Reconcile` exits that never reach the nudge again (`IsNotFound`, `DeletionTimestamp`).
  Over-reach is the failure mode to watch, and
  `TestReconcile_KeepsNudgesOfOtherCRs` exists to fail if the map is cleared wholesale.

## References

* [`internal/controller/valkey_controller.go`](../../internal/controller/valkey_controller.go) — `reconcileStep`, `runReconcileSteps`, `reconcileResources`, `reconcileWorkload`
* [`internal/controller/ratelimiter.go`](../../internal/controller/ratelimiter.go) — `newReconcileRateLimiter`, `reconcileControllerOptions`
* [`internal/controller/reconcile_blocked.go`](../../internal/controller/reconcile_blocked.go) — `compactErrorMessage`
* [ADR 0002](0002-surface-a-blocked-reconcile-on-the-cr.md) — how the block reaches the CR
* [ADR 0003](0003-nudge-a-short-of-pods-statefulset.md) — the other half of the incident response
* [ADR 0015](0015-one-crd-validated-by-schema-only.md) — why a rejected write is a first-class runtime state here
* [ADR 0017](0017-test-and-ci-policy.md) — `reconcileControllerOptions()` exists so the wiring itself is unit-testable
