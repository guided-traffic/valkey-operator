# ADR 0019: Reconcile Concurrency, and the Cost of a Stuck Pass

## Status

Accepted. Date: 2026-08-21.

Implemented on branch `feat/support-pdb`. Both halves ship together: the worker count
(`--max-concurrent-reconciles`, default 4) and the concurrent master probe in
`findMaster`.

Verified in this repository: the shared-state audit of D3 (read), the wiring
(`internal/controller/ratelimiter_test.go`, `cmd/main_test.go`), the parallel probe
(`internal/health/checker_parallel_test.go`) and the fleet decoupling itself
(`test/integration/reconcile_concurrency_test.go`, envtest). Both new guards were
mutation-checked: with the worker count back at 1 the integration test fails after
8.1 s, and with the probes back in a sequential loop the timing test fails at 1.51 s
against its 900 ms bound.

**Not verified:** nothing here was reproduced on a real cluster. The original
measurement that opened this item was taken in envtest as well. No fleet of many CRs
was run at any worker count, and the API-server load of four concurrent passes was
not measured.

## Context

`SetupWithManager` passed controller options that set a `RateLimiter` and nothing else
([`internal/controller/ratelimiter.go`](../../internal/controller/ratelimiter.go)), so
`MaxConcurrentReconciles` stayed at controller-runtime's default of **1**. Every
`Valkey` CR in the cluster was reconciled by that single worker, one after another.

A pass is not cheap. It dials the pods of its cluster over RESP with a 5 s
dial/read/write timeout each
([`internal/valkeyclient/client.go`](../../internal/valkeyclient/client.go)), and
`findMaster` walked the ordinals **sequentially**, so a cluster whose pods were
`Running` but not answering cost up to `replicas x 5 s` in that one function alone,
before the rolling-update and steady-state paths add their own probes.

That is a fleet-wide coupling, and it was measured: while a single 5-replica CR was in
such a pass, a *different* CR created in a parallel envtest case got no reconcile at
all for 15-25 s and its own wait timed out. Outside envtest the same shape is produced
by an ordinary node drain, a NetworkPolicy mistake or a hung Valkey — none of which is
a defect of the *other* clusters that inherit the latency.

Two properties made this worth deciding rather than patching:

* The queue already serialises per key. controller-runtime never runs two passes for
  the same object, at any worker count, so raising the count does not weaken any
  per-CR invariant — *provided* nothing in the reconciler keeps state that assumes
  fleet-wide serialisation. That had to be checked, not assumed.
* Raising the worker count alone treats the symptom. With N workers and N stuck
  clusters the coupling returns unchanged; only making a stuck pass cheaper changes
  the shape.

## Decision

**D1 — The operator reconciles several Valkey resources at once.**
`reconcileControllerOptions(maxConcurrent int)` sets `MaxConcurrentReconciles`, and
`SetupWithManager` passes `ValkeyReconciler.MaxConcurrentReconciles`. A value of zero
or less means "not configured" and falls back to `DefaultMaxConcurrentReconciles` = **4**,
so every reconciler built without the field — the integration suite, every test helper —
gets the decoupling rather than the single worker.

**D2 — The worker count is a flag, and the chart exposes it.**
`--max-concurrent-reconciles` (default `DefaultMaxConcurrentReconciles`) in
[`cmd/main.go`](../../cmd/main.go), rendered from `.Values.maxConcurrentReconciles` in
[`deploy/helm/valkey-operator/templates/deployment.yaml`](../../deploy/helm/valkey-operator/templates/deployment.yaml).
The number that is right depends on fleet size and API-server budget, which the
operator cannot know.

**D3 — Concurrency is only permitted because no reconciler state is fleet-wide.**
This is a standing constraint on new code, not a one-time audit. What holds today, all
read in this tree:

* `nudgeTracker` is mutex-guarded and every key carries the namespace and the CR name
  — the nudge grace period keyed by StatefulSet, the rolling-update wait bounds keyed
  by `waitBoundKey(namespace, name, bound)`.
* The blocked-pass marker rides on the `context`, not on the reconciler
  ([`internal/controller/reconcile_blocked.go`](../../internal/controller/reconcile_blocked.go)),
  so it is per pass and per CR.
* There is no package-level mutable state in `internal/`.
* Every managed object's name contains the CR name — including the NetworkPolicy,
  where `spec.networkPolicy.namePrefix` is a *prefix* in front of it — so two CRs
  never write the same object.

Anything that breaks one of these bullets has to be fixed, not worked around by
lowering the worker count.

**D4 — The master probe is concurrent, and its result may not depend on arrival order.**
`findMaster` probes all ordinals at once and collects into a slice **indexed by
ordinal**, never appended in completion order. Multiple masters are still arbitrated by
`connected_slaves`, now with `sort.SliceStable` plus that ordinal order, so equal slave
counts resolve to the **lowest ordinal** deterministically. The previous `sort.Slice`
was unstable: with two masters reporting the same count the winner was already
unspecified, and completion-order collection would have made it depend on which pod
answered first — a master authority that flips between passes for no observable reason
([ADR 0008](0008-known-master-annotation-is-the-recorded-authority.md)).

**D5 — A pod the API server does not report as `Running` is still never dialled.**
The readiness gate moved into `probeMasterRole` unchanged. Concurrency does not become
a licence to dial more.

## Consequences

* Four passes can now write to the API server at the same time. The client is not the
  limit — controller-runtime v0.24.1 sets `rest.Config.QPS = -1` and leaves throttling
  to API Priority and Fairness — and the work queue's own token bucket
  (`reconcileRetryQPS` 10, `reconcileRetryBurst` 100) is unchanged and shared, so a
  fleet failing at once still cannot saturate the client.
* Log lines from different CRs now interleave. Each carries its `reconcileID`, so a
  pass is still reconstructable, but reading the log as a single sequence no longer
  works.
* `findMaster` opens up to `replicas` connections at once instead of one at a time.
  For the cluster sizes this operator manages that is single digits per pass.
* The decoupling is a bar, not a guarantee: four simultaneously stuck clusters restore
  the original behaviour for everything behind them. D4 lowers the height of the bar
  by a factor of `replicas` for the master probe, but the rolling-update and
  steady-state probe loops are still sequential.
* Two operator replicas would now interleave more work — which is why leader election
  stays the supported deployment shape (`leaderElection.enabled: true` in the chart).

## Alternatives Considered

**Leave the worker count at 1 and only make passes cheaper.** Rejected: it keeps the
coupling in principle. Every future probe added to a pass would reopen it, and there
are 42 dial sites across `internal/controller` and `internal/health`.

**Raise the worker count and stop there.** Rejected as half the fix, for the reason in
D4's context: `replicas x 5 s` per stuck cluster is what makes the fleet coupling
expensive, and it costs one `sync.WaitGroup` to remove most of it.

**Parallelise all 42 dial sites.** Rejected. Most of them sit on the failover-critical
rolling-update paths, where ordering is the invariant being protected
([ADR 0007](0007-failover-aware-rolling-update.md),
[ADR 0011](0011-evidence-based-steady-state-split-brain-resolution.md)). `findMaster`
is the one loop that is a pure fan-out with no ordering meaning — and even it needed
an explicit determinism rule (D4).

**A deadline on the whole pass (`context.WithTimeout` around `Reconcile`).** Rejected.
It would cut a pass mid-failover, which is exactly what the bounded-wait design exists
to avoid ([ADR 0010](0010-every-rolling-update-wait-is-bounded.md)). It would also not
do what it promises: `valkeyclient.Client` takes no `context` and its timeouts are set
on the connection, so an in-flight dial would not be interrupted by a cancelled pass.

**Default the flag to 1 so upgrades change nothing.** Rejected. It is upgrade-neutral
in the letter of [ADR 0005](0005-upgrade-neutral-defaults-and-anti-affinity.md) and
useless in its spirit: the defect stays for everyone who never reads the flag. The
distinction that decided it — ADR 0005 governs **CRD fields that change a running data
plane**; the worker count changes neither a pod spec nor a config file, and for an
installation with a single Valkey CR it changes nothing at all.

## Residual risks

* **Not measured on a cluster.** Every number in this ADR comes from envtest or from
  reading the code. What four concurrent passes cost a real API server at fleet scale
  is unknown.
* **The bar is finite.** N stuck clusters still starve the fleet. Nothing detects or
  reports that condition today; a stuck cluster is visible per CR, not as a queue
  metric.
* **Only `findMaster` is parallel.** `verifyValkeyConnectivity`
  ([`internal/controller/valkey_controller.go`](../../internal/controller/valkey_controller.go)),
  `checkSentinel` and the rolling-update probe loops remain sequential, and each can
  still cost multiples of the 5 s timeout in one pass.
* **D3 is a claim about today's tree.** It is enforced by review, not by a test. There
  is no check that fails when someone adds a map to `ValkeyReconciler` keyed by
  something other than a CR.
* **The integration test proves latency, not correctness under concurrency.** It shows
  a second CR being served while a first is stuck. It does not exercise two passes
  racing on the same object — the queue makes that impossible for one key, and no test
  asserts that property directly.

## References

* [`internal/controller/ratelimiter.go`](../../internal/controller/ratelimiter.go) — `DefaultMaxConcurrentReconciles`, `reconcileControllerOptions`
* [`internal/controller/valkey_controller.go`](../../internal/controller/valkey_controller.go) — `MaxConcurrentReconciles`, `SetupWithManager`
* [`internal/health/checker.go`](../../internal/health/checker.go) — `findMaster`, `probeMasterRole`
* [`cmd/main.go`](../../cmd/main.go) — `--max-concurrent-reconciles`
* [`deploy/helm/valkey-operator/values.yaml`](../../deploy/helm/valkey-operator/values.yaml) — `maxConcurrentReconciles`
* [`test/integration/reconcile_concurrency_test.go`](../../test/integration/reconcile_concurrency_test.go), [`internal/health/checker_parallel_test.go`](../../internal/health/checker_parallel_test.go)
* [ADR 0001](0001-continue-reconciling-past-a-rejected-write.md) — the work queue's rate limiter, and why a pass continues past a rejected write
* [ADR 0005](0005-upgrade-neutral-defaults-and-anti-affinity.md) — upgrade-neutral defaults, and the boundary this decision argues against
* [ADR 0008](0008-known-master-annotation-is-the-recorded-authority.md) — why a deterministic master answer matters
* [ADR 0017](0017-test-and-ci-policy.md) — the tier rules the two new tests follow
