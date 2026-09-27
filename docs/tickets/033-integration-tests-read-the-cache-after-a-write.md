---
id: T33
title: integration tests read through the manager cache right after a write
state: analysed       # nothing decided yet
severity: medium      # a flaky required check blocks every merge and Renovate automerge; the lost failover phase is a status label and does not raise it
security: none        # every vacuous refusal assertion also has a unit-tier guard
urgency: next         # rule 3: severity medium and the trigger is live
effort: M             # per-site write-order edits, markers with positive controls, one mutation per set, W1 with two unit tests
blocked-by: decision  # Q1-Q4; the independent items are not blocked
filed-from: T31, section "CI red on e2ce8bb", and the integration-race audit
opened: 2026-09-26
decided:
done:
---

# T33 - integration tests read through the manager cache right after a write

## Current state

### Mechanism

- `k8sClient = mgr.GetClient()` ([`suite_test.go:129`](../../test/integration/suite_test.go))
  serves `Get`/`List` from the manager's informer cache and sends writes to the API server; the
  reconciler under test shares that client and cache (`:98-99`). `apiReader = mgr.GetAPIReader()`
  (`:130`, comment `:47-50`) is used once, at
  [`pod_hardening_test.go:257`](../../test/integration/pod_hardening_test.go).
- **Cache lag.** Every type has its own informer. A `Get` right after a write can miss it, and
  seeing object X says nothing about object Y of another type.
- **Write order.** Even an uncached read can precede the operator's write. One pass runs, in
  [`valkey_controller.go`](../../internal/controller/valkey_controller.go): the resource steps
  (`resourceReconcileSteps`, `:551-573`: ConfigMaps, Services, sidecar RBAC, StatefulSet,
  monitoring, TLS material); the `ReconcileBlocked` condition (`:276`); `reconcileWorkload`
  (`:284`: nudge `:332`, `updateStatus` -> `persistStatus` with the `Ready` condition,
  `:2241-2283`); the `Error` phase (`:295`). In a blocked pass every intermediate phase write is
  suppressed (`updatePhase` `:2606-2611`, `persistStatus` `:2549-2552`), so the phase stays at
  its previous value (`Provisioning` for a new CR, `:256-260`) until `:295`. A negative read after
  a poll rules out only writes that come earlier in this order than the polled state.
- testify v1.12.1 `Eventually` runs the condition once immediately, in its own goroutine
  (`assert/assertions.go:2023-2024`). A negative poll (`IsNotFound`) is therefore satisfied by a
  cache that has not yet seen the object, and a `require` inside the condition calls `FailNow`
  off the test goroutine.
- The object passed to `Create`/`Update`/`Patch` holds the stored object afterwards
  (controller-runtime v0.25.1 `targetZeroingDecoder`, `pkg/client/apiutil/apimachinery.go:224-240`).
  The one reproduced instance (a cached `Get` after `Create`, red in CI with
  `pod_security_test.go:49 ... not found` and in 2 of 4 local runs) is fixed this way
  ([`pod_security_test.go:43-50`](../../test/integration/pod_security_test.go));
  `pod_hardening_test.go` uses the same patterns (`:102-105`, `:184-201`, `requirePhaseError`
  `:204-220`, `apiReader` `:256-258`).

### `writePhase` drops a conflicting phase write

`writePhase` (`valkey_controller.go:2615-2629`) does a cached `Get` (`:2617`) into the caller's
object and a `Status().Update` (`:2628`) with no retry; `writeStatusCondition` (`:2689-2716`)
retries the same 409 with `retry.RetryOnConflict` (`:2691`, reason at `:2679-2685`).

- Blocked pass: the error is discarded at `:295` and the next pass writes `Error`
  ([`ratelimiter.go:71-79`](../../internal/controller/ratelimiter.go): 5 ms doubling, 30 s cap).
  In the green CI run read, `Error` landed in the first blocked pass at two A1 sites.
- `Failover in progress`: both writers call `updatePhase` right after an `r.Update` of the CR and
  discard its error ([`rolling_update.go:2743-2746`](../../internal/controller/rolling_update.go),
  `:4045-4062`). If the cache lags that `Update`, the phase is dropped.
- Callers that return the error (`valkey_controller.go:2190`, `:2200`) fail the pass on the
  operator's own 409.

### Affected test sites

**Class A - can go red.**

| # | Site | Why |
|---|---|---|
| A1 | [`foreign_object_test.go:75-78`](../../test/integration/foreign_object_test.go), `:223-226`; [`volumeclaim_conflict_test.go:230-233`](../../test/integration/volumeclaim_conflict_test.go) | One unpolled `Get` asserts `phase == Error` after a poll on `ReconcileBlocked` (or `StorageSpecNotApplied`, [`volumeclaim_conflict.go:183`](../../internal/controller/volumeclaim_conflict.go)). A 250 ms tick between `:276` and `:295` reads `Provisioning`. |
| A2 | [`tls_material_test.go:293-295`](../../test/integration/tls_material_test.go) | Cached `Get` of a pod created at `:272`. In the full suite the Pod informer runs (`listDataPodNames`, `valkey_controller.go:1061`) and may not have the ADD yet. |
| A3 | [`observer_test.go:159-164`](../../test/integration/observer_test.go) | Cached `Get` plus spec `Update`, no retry, right after the observer Deployment appears. The same pass writes the CR status (`:2559-2564` -> `:2570`, or `:2190`), so the `Update` can get a 409. Write order; no cache lag needed. |

**Class B - never a false red, but a regression can pass.**

| # | Site | Why | Unit guard |
|---|---|---|---|
| B1 | [`sidecar_services_test.go:413-419`](../../test/integration/sidecar_services_test.go), `:421-427` | `IsNotFound` polls pass on a cache that has not seen the test's own `Create` (`:392`, `:409`). | [`valkey_controller_test.go:534`](../../internal/controller/valkey_controller_test.go), `:567`; [`resource_reconcile_test.go:915`](../../internal/controller/resource_reconcile_test.go) ff. |
| B2 | [`foreign_object_test.go:236-237`](../../test/integration/foreign_object_test.go) | Asserts no nudge on the foreign StatefulSet. A nudge cannot land before `nudgeGracePeriod` = 10 s ([`nudge.go:26`](../../internal/controller/nudge.go), `:229`); the subtest ends about 0.4 s after the first pass (CI log). Vacuous by construction. | [`foreign_object_test.go:424`](../../internal/controller/foreign_object_test.go) |
| B3 | [`observer_test.go:87-94`](../../test/integration/observer_test.go), `:123-128`; [`foreign_object_test.go:82-85`](../../test/integration/foreign_object_test.go), `:88-93`, `:162-167`, `:230-235`, `:311-326`, `:369-375`; [`tls_material_test.go:182-184`](../../test/integration/tls_material_test.go); [`volumeclaim_conflict_test.go:150-169`](../../test/integration/volumeclaim_conflict_test.go), `:356-362`; [`integration_test.go:99-105`](../../test/integration/integration_test.go) | "Absent", "not `Error`" or "foreign object untouched" is asserted before the write it rules out has necessarily happened, or through another type's informer. | foreign-object sets: [`foreign_object_test.go:63`](../../internal/controller/foreign_object_test.go), `:132`, `:344`, `:627`, `:648`; others not traced |
| B4 | [`observer_test.go:177-183`](../../test/integration/observer_test.go) | `err != nil` poll on the observer ServiceAccount, the B1 shape; negligible rate. | not traced |
| B5 | [`sidecar_services_test.go:466-481`](../../test/integration/sidecar_services_test.go) | Standalone `-all`/`-r` absence after `time.Sleep(2 * time.Second)`. | not traced |

**Class C - latent, holds on timing.**

| # | Site | Holds because |
|---|---|---|
| C1 | [`foreign_object_test.go:107-109`](../../test/integration/foreign_object_test.go) | SA read after the RoleBinding poll; created SA -> Role -> RB (`valkey_controller.go:983-1003`). |
| C2 | [`integration_test.go:108-125`](../../test/integration/integration_test.go), `:196-200`, `:266-341`, `:423-428`, `:514-519` | Unpolled reads follow a polled StatefulSet created several round trips later. |
| C3 | [`sidecar_services_test.go:298-321`](../../test/integration/sidecar_services_test.go), `:430-439` | Role/RB after the SA poll, `-r`/`-all` after the `-rw` poll. Under a subtest filter `:298-321` is write order: SA, Role, RB are three calls (`valkey_controller.go:984`, `:992`, `:1003`). |
| C4 | [`reconcile_concurrency_test.go:142-154`](../../test/integration/reconcile_concurrency_test.go) | `Status().Update` on the first cached StatefulSet, no retry; the operator rewrites it only on drift (`valkey_controller.go:1366-1373`) or via the nudge after 10 s. |

Sound as written: write-answer reads, `pdb_uid_precondition_test.go`'s uncached client,
poll-guarded writes, a `Get` after polling the same object, `tls_material_test.go:213-217`.

### Impact

- `Integration Tests (envtest)` is a required check (ruleset `23985346`, ADR 0017 D47) that
  Renovate's automerge waits for (`renovate.json:15-18`): a flake blocks every merge. Nothing
  stops the next `Get` after a `Create`.
- Class B assertions (ADR 0020 provenance, legacy-Service cleanup, observer toggle, TLS evaluator,
  ADR 0023 storage conditions, standalone Services) violate ADR 0017 D10 (B2) and D9 (B1, B3-B5).
- The `Failover in progress` phase that CLAUDE.md (section Status) requires can be lost; `Ready`
  and the failover itself are unaffected.

## Required changes

### Independent of the open questions

- **A1:** replace the single read with `requirePhaseError`:
  `foreign_object_test.go:75-78` -> `requirePhaseError(t, types.NamespacedName{Name: crName, Namespace: "default"}, "sidecar ServiceAccount")`
  (the local `key` is the SA key); `:223-226` -> `requirePhaseError(t, key, "does not control")`;
  `volumeclaim_conflict_test.go`: insert `requirePhaseError(t, key, "volumeClaimTemplates are immutable")`
  before `:230`, delete `:232-233`, keep the `Get` at `:230-231` that `:235` reads.
- **A3 and C4:** read and write inside one poll (pattern `pod_hardening_test.go:150-158`):
  `observer_test.go:159-164` (`Get`, `Spec.Observer.Enabled = false`, `Update`, retried; add
  `context` and `wait` imports); `reconcile_concurrency_test.go:148-154` (fresh `Get`, the six
  status fields, `Status().Update`, retried; `slowProbes.arm()` at `:144` stays in front).
- **C3 `:298-321`:** poll the Role and the RoleBinding.
- **Completed-pass marker:** wait for the `Ready` condition with `observedGeneration ==
  metadata.generation` before `observer_test.go:87-94`, `:123-128`, `tls_material_test.go:182-184`,
  `foreign_object_test.go:311-326`, `volumeclaim_conflict_test.go:163-169`, `:356-362`; B5
  replaces its `time.Sleep` with it. The marker precedes the `Error` write, so each test states
  that its `ReconcileBlocked` assertion catches the regression a "not `Error`" check aims at.
  Every set gets a positive control (ADR 0017 D11).
- **Proof:** A1 by an ADR 0017 D13 mutation on the live tree (500 ms delay before `writePhase`
  at `:295`): unfixed fails, fixed passes. B1 (`deleteLegacyServices` a no-op), B4
  (`cleanupObserverServiceAccount` a no-op) and one mutation per B3 set fail the fixed test. A
  grep finds no read of a just-written object without a poll, the write answer or an uncached
  client. `make test-integration` green 10 times and in CI; a streak alone is no proof.

### Depends on the answers

- **Q1 = B:** `suite_test.go:129-130` becomes
  `client.New(testEnv.Config, client.Options{Scheme: scheme.Scheme})`, `apiReader` merges into
  it; rewrite `suite_test.go:47-50`, `docs/developer/testing.md:69-71`, drop the `apiReader` use
  at `pod_hardening_test.go:256-257`. The B3 sets not listed under the marker need nothing more.
  **Q1 = A:** per-site fixes for A2, B1, B4, C1, C2, C3 `:430-439`, and a positive wait plus an
  `apiReader` read for the remaining B3 sets.
- **Q2:** B2 per the answer; under the recommendation one sentence in ADR 0020 (D8 or Residual
  risks) naming the unit-only coverage as an ADR 0017 D12 exception.
- **Q3 = W1:** `retry.RetryOnConflict(retry.DefaultRetry, ...)` around `writePhase`'s `Get`,
  compare and `Update`; two unit tests via `interceptor.Funcs.SubResourceUpdate`
  (`internal/controller/status_phase_test.go:35`): one 409 then the phase lands, and
  `updatePhase` after an `r.Update` of the CR lands `Failover in progress`; both fail with the
  retry removed. One sentence in ADR 0002 (D3 or D7).
- **Q4:** the wait style of every touched wait; under a' the four `metrics_test.go` waits, proven
  by a grep that no wait condition calls `require` or `assert`. ADR 0017 D25 amendment and the
  index row ([`docs/adr/README.md:109`](../adr/README.md)).
- **Close:** a new ADR 0017 decision (integration reads come from the API server; a write-order
  read polls for the completed pass), then archive.

## Open questions

### Q1: How is the cache-lag half removed?

25 of the 31 cited ranges (A2, B1, B3-B5, C1-C3) depend on informer timing. The choice changes
only how test reads are served, no production behaviour; the write-order sites need their fix
either way.

- **A - fix each site in the cached design:** about 25 edits in 7 files; protects only the
  listed sites, and the list has already proven incomplete.
- **B - make every test read uncached (recommended):** one assignment plus two comment/doc
  rewrites; test reads stop acting as a barrier for the operator's cache, as in production. No
  test is known to rely on that barrier (no cache indexes, no unstructured reads).

B covers every present and future read without enumeration; envtest's QPS 1000 / Burst 2000
(`pkg/envtest/server.go:309-313`) means uncached polling is not throttled.

**Answer:** _open_

### Q2: What happens to the B2 nudge assertion, which cannot fail?

It is the only envtest line for the nudge refusal guard (ADR 0020 D8), and ADR 0017 D12 asks for
a real-API-server layer for every refusal guard. The unit test at
`internal/controller/foreign_object_test.go:424` pins the guard deterministically.

- **Delete it and record the unit-only coverage as a named D12 exception (recommended):** XS
  plus one ADR 0020 sentence; the comment names the unit test and its mutation check
  (`nudge.go:211` removed, unit test fails).
- **Wait past the nudge:** keeps D12 literally; adds 10-40 s (computed from the backoff) to a
  required check whose whole suite takes about 25 s.

The guard refuses before any API call, so the real API server adds nothing to it, while the wait
roughly doubles the check.

**Answer:** _open_

### Q3: Does `writePhase` retry a conflict?

Its unretried 409 delays the `Error` phase in a blocked pass, can drop `Failover in progress`, and
fails non-blocked passes on the operator's own write. Only the conflict path changes; ADR 0002
D7's handling of non-conflict errors stays.

- **W1 - `RetryOnConflict` like `writeStatusCondition` (recommended):** XS-S with two unit
  tests; the cached `Get` still replaces the caller's object (no traced caller is harmed).
- **W3 - status merge patch without `Get` or resourceVersion:** S; no self-inflicted 409 and no
  clobber, but a second write shape and no optimistic concurrency, against ADR 0002 D7.
- **W2 - leave it:** no cost; the failover phase stays lost whenever the cache lags.

W1 is the shape the code and ADR 0002 D7 already document as correct, at the smallest change. If
T78's R1 (refresh through the `APIReader`) is taken, reading `writePhase`'s `Get` through the same
reader is a variant to weigh.

**Answer:** _open_

### Q4: Does ADR 0017 D25 (poll with `wait.PollUntilContextTimeout`, never `Eventually`) extend to the integration tier?

D25 is e2e-only by title. The integration tier has 56 `Eventually` calls, one `require.Never` and
10 `PollUntilContextTimeout` polls. `scrapeMetrics(t)`
([`metrics_test.go:21-31`](../../test/integration/metrics_test.go)) calls `require` inside the
wait conditions at `:37-39`, `:94-105`, `:114-124`, `:131-134`: a failure there is a red delayed
by 15-30 s, and rarely a panic on a finished `*testing.T`; never a false pass.

- **a - extend D25 to new waits and every wait this ticket touches:** part of the per-site work
  plus the ADR amendment; `metrics_test.go` keeps the forbidden shape.
- **a' - a, plus converting the four `metrics_test.go` waits (recommended):** XS more; no wait
  condition in the tier calls `FailNow` off the test goroutine.

a' removes the one verified instance of the shape D25 forbids at XS extra cost.

**Answer:** _open_

## Not verified

- Every rate (class-A flakes, the blocked-pass 409, the lost failover phase) is estimated from
  the pass structure; no remaining site was reproduced. The mutation checks settle the tests.
- That informers deliver CR versions in resourceVersion order; the apiserver watch cache and the
  client-go reflector source at v0.37.1 would settle it.
- That no test relies on the cache barrier under Q1 = B; running the suite settles it.
- The unit guards of the non-foreign-object B3 sets, B4 and B5, and whether the named unit guards
  carry a recorded mutation check.

## Related

- [T31](archive/031-generated-pods-run-as-root.md) - holds the reproduction of the fixed instance.
- [T34](034-e2e-fixtures-wait-on-controller-state-after-a-pod-delete.md) - the e2e sibling; under Q4 a or a' both tiers share one wait rule.
- [T35](035-master-records-lag-the-real-master.md) - relies on the operator's Pod informer, which Q1 = B does not touch.
- [T43](043-lint-and-vet-skip-every-build-tagged-test-file.md) - `make lint` does not check these files.
- [T78](078-the-status-write-conflicts-with-the-operators-own-earlier-write.md) - the `persistStatus` 409; the observed CI and wds18 409s belong there.
