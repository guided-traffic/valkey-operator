---
id: T33
title: integration tests read through the manager cache right after a write
state: analysed
severity: medium
security: none
urgency: next
effort: M
blocked-by: decision
filed-from: T31, section "CI red on e2ce8bb", and the integration-race audit of 2026-09-26
opened: 2026-09-26
decided:
done:
---

Filed as a board row on 2026-09-26 out of the CI-red analysis of `e2ce8bb`
([T31](archive/031-generated-pods-run-as-root.md), section "CI red on `e2ce8bb`"). This file was
written on the same day. Every file:line below was re-read against the tree at `a8e8931`
(`feat/rootless`, clean). Each claim is labelled by how it was verified
([ADR 0017](../adr/0017-test-and-ci-policy.md) D36): **run** means executed, **read** means
read in the tree, the module cache or a public API, and **hypothesis** means neither.
"Audit" refers to the integration-race audit of 2026-09-26: a find agent and an adversarial
verifier, recorded in the local session journal `wf_7f0cb77a-e5c/journal.jsonl` (not in the
repository). Carried over are the findings the verifier marked CONFIRMED and the defects the
verifier added itself (B2 and part of B3); each one was re-checked in the current tree.
*(Corrected 2026-09-26 in the adversarial re-check: this said "only findings the verifier
marked CONFIRMED", which left out the verifier's own additions.)*

All the remaining **test** sites are in files that are identical to `main` (read with
`git diff --stat e3e869d HEAD -- test/integration/`: only `pod_hardening_test.go`,
`pod_security_test.go` and `suite_test.go` differ). The production side, `writePhase` and the
write order of a blocked pass, is unchanged on the branch as well (read: `git diff e3e869d HEAD
-- internal/controller/valkey_controller.go` touches none of `writePhase`,
`setReconcileBlockedCondition`, `withBlockedPass`, `persistStatus`). The defect class is
therefore **pre-existing on `main`**. The only instances that `feat/rootless` introduced were in
`pod_security_test.go` and `pod_hardening_test.go`, and both are fixed. *(Corrected 2026-09-26:
the merge base was given as `9925539`, which is the stale local `main`; the merge base with
`origin/main` is `e3e869d`, merged at `e2ce8bb`, and between the two only `go.mod`/`go.sum`
changed. The sentence also said "all the remaining sites", but C5 lives in
`valkey_controller.go`, which does differ from `main`, outside the lines named.)*

## Fact

### Mechanism (read)

- The suite reads through the cache. `k8sClient = mgr.GetClient()`
  ([`suite_test.go:129`](../../test/integration/suite_test.go)) serves `Get`/`List` from the
  manager's informer cache and sends writes to the API server. The reconciler under test uses
  the same client (`:98-99`), so it shares that cache.
- `apiReader = mgr.GetAPIReader()` (`:130`, comment `:47-50`) was added on `feat/rootless`. It
  is used exactly once, at [`pod_hardening_test.go:257`](../../test/integration/pod_hardening_test.go).
- The race has two halves, and they need different fixes.
  1. **Cache lag.** A `Get` right after a write, whether the test's own write or the
     operator's, can miss that write. Every type has its own informer. Seeing object X in the
     cache therefore says nothing about object Y of another type, even when the operator
     created Y first.
  2. **Write order.** Even with an uncached read, the operator has not necessarily written the
     field yet.
     - In a blocked pass, the operator writes `ReconcileBlocked` first, as its own status update
       ([`valkey_controller.go:276`](../../internal/controller/valkey_controller.go) →
       `setStatusCondition` → `writeStatusCondition`).
     - Then `reconcileWorkload` runs (`:284`), and only after that the one `Error` phase write
       (`:295`).
     - Every intermediate phase write in between is suppressed: `updatePhase` (`:2605-2610`)
       and `persistStatus` (`:2548-2551`).
     - The stored phase therefore stays at its previous value until `:295`. For a new CR that
       value is `Provisioning`, written at `:256-260`.
     - The informer delivers CR versions in resourceVersion order. A cached CR can therefore
       show the condition without `Error`, but never the reverse. (Audit; this is standard
       informer semantics and was not re-read in client-go.)
- **`writePhase` widens the window. It is latent and heals itself.**
  - `writePhase` (`:2614-2628`) is a cached `Get` followed by `Status().Update`, with no retry.
    Its error is discarded at `:295`.
  - `writeStatusCondition` (`:2688-2715`) retries the same shape with `retry.RetryOnConflict`,
    and its doc comment (`:2678-2684`) names the reason: a stale cached read gets a 409 on the
    operator's own earlier write.
  - `writePhase` meets exactly this 409 whenever its cached `Get` runs before the informer has
    delivered the condition write (read; how often was not measured).
  - The pass still returns `resourceErr` (`:297`), so the rate limiter re-enters it and the next
    pass writes `Error`. The result is a delay, not a lost write. The limiter is the operator's
    own (`newReconcileRateLimiter`, [`ratelimiter.go:71-80`](../../internal/controller/ratelimiter.go)):
    5 ms doubling per consecutive failure, capped at 30 s (read).
  - *(Corrected 2026-09-26: the range read `:2614-2627` and the return `:296`; the function ends
    at `:2628` and the return is at `:297`. "This 409 is exactly what `writePhase` meets" read as
    if every blocked pass met it; it needs the cache lag.)*
- **testify `Eventually` checks at once.** testify v1.12.1 `Eventually` runs the condition once
  immediately, before the first tick (`assert/assertions.go:2023-2024`, module cache).
  - A positive poll is therefore not delayed.
  - A negative poll (`IsNotFound`) is satisfied by a cache that has not yet seen the object.
- **Why the fix pattern works: a write's answer is the stored object.** controller-runtime
  v0.25.1 wraps every typed client in `targetZeroingDecoder`
  (`pkg/client/apiutil/apimachinery.go:210`, `:224-240`, module cache). It zeroes the target and
  then decodes the API server's answer into it. The object passed to `Create`/`Update`/`Patch`
  therefore holds the stored object afterwards, server defaulting included, whatever the
  content type.

### The reproduced instance, fixed on `feat/rootless`

- **Run (recorded in T31, not re-run in this pass).**
  `TestPodSecurity_TemplatesSurviveAPIServerDefaulting_Integration` read its object back with
  `k8sClient.Get` directly after `k8sClient.Create`. At `e2ce8bb` the `Get` sat inside the
  `roundTrip` helper (read with `git show`).
  - `make test-integration` was red in 2 of 4 local runs on `e2ce8bb`.
  - The fix takes the stored object from the `Create` answer
    ([`pod_security_test.go:43-50`](../../test/integration/pod_security_test.go)).
  - After the fix: 6 of 6 local runs green, then 3 of 3 more.
- **Read (public GitHub API, 2026-09-26).** Check run `Integration Tests (envtest)`:
  - `failure` on `e2ce8bb` (completed 09:33:45Z);
  - `success` on `e6a9d7c` (17:15:04Z) and on `a8e8931` (17:39:20Z).
  - The job log needs authentication and was not read. The check run's one public annotation
    says only "Process completed with exit code 2" (read, `check-runs/108379517605/annotations`).
    That CI failed on this test comes from T31 ("Found on the way" and section "CI red on
    `e2ce8bb`"), which does not say how it established that. So CI going red on `e2ce8bb` is
    read, and its attribution to this race is **not verified** here.
- **Read. The same patterns in [`pod_hardening_test.go`](../../test/integration/pod_hardening_test.go):**
  - the `Patch` answer (`:102-105`);
  - `Create` answers (`:184-201`);
  - `requirePhaseError`, which polls the phase (`:204-220`), used at `:148` and `:254`;
  - `apiReader` for "never created" (`:256-258`).
  - The audit's highest-risk site, which was then `pod_hardening_test.go:144-147`, is `:148`
    today. It was fixed before `b13377e` and is **not** carried over below. The file was first
    committed in `b13377e` (read, `git log --diff-filter=A`), after the audit ran (12:49-13:09
    local versus a commit time of 16:26), so the unfixed form never reached a commit.

### Confirmed sites, by what they can do (each re-read at `a8e8931`)

**Class A: can go red (a real flake source).**

| # | Site | Shape | Flake scenario | Severity |
|---|---|---|---|---|
| A1 | [`foreign_object_test.go:75-78`](../../test/integration/foreign_object_test.go), `:223-226`; [`volumeclaim_conflict_test.go:230-233`](../../test/integration/volumeclaim_conflict_test.go) | One unpolled `Get` asserts `phase == Error` after a poll on `ReconcileBlocked` (and `StorageSpecNotApplied`, which the StatefulSet step writes even earlier, [`volumeclaim_conflict.go:182`](../../internal/controller/volumeclaim_conflict.go)) | A 250 ms tick (all three polls use 250 ms; `claimGuardInterval`, `volumeclaim_conflict_test.go:48`) lands between the condition write (`:276`) and `writePhase` (`:295`), or its 409-delayed retry pass. The CR still reads its previous phase (`Provisioning` in envtest, where no pod ever runs), and the assertion fails. | real, low rate (audit estimate from the pass structure, not measured) |
| A2 | [`tls_material_test.go:293-295`](../../test/integration/tls_material_test.go) | A cached `Get` of a pod the test created at `:272`, with only three `Patch` round trips in between | In the full suite the Pod informer already runs, because every pass lists pods (`listDataPodNames`, `valkey_controller.go:1061`). If it has not yet delivered the ADD, the `Get` returns NotFound. The assertion itself is stable. | real, full suite only. The audit claimed a larger window when the test runs alone under `-run`, and the verifier **refuted** that: the first cached `Get` of a new type blocks until the informer has synced, and its initial LIST holds the pod (controller-runtime `pkg/cache/internal/informers.go:322-331`, read). |
| A3 | [`observer_test.go:159-164`](../../test/integration/observer_test.go) | A single cached `Get` and a spec `Update`, with no retry, right after the observer Deployment appears | The same pass writes the CR status after creating the Deployment. Either `persistStatus` changes `ObserverReady` from nil to `&false` (`valkey_controller.go:2558-2563`) and calls `Status().Update` (`:2569`), or `updatePhase` writes "Waiting for StatefulSet creation" (`:2190`). A `Get` that runs before that write, or misses it in the cache, carries the older resourceVersion, the `Update` gets a 409 and `require.NoError` fails. The first half needs no cache lag at all: it is write order. | real, low rate |

**Class B: never a false red, but a regression can pass (vacuous on some runs, B2 on every
run).** *(Corrected 2026-09-26: the heading said "cannot go red". That holds only for B2. B1 and
B3 do go red on a regression whenever the cache has already caught up, so they are
probabilistic guards, which is what [ADR 0017](../adr/0017-test-and-ci-policy.md) D9 rejects,
not guards that can never fail.)*

| # | Site | How a regression can pass | Guard that does exist |
|---|---|---|---|
| B1 | [`sidecar_services_test.go:413-419`](../../test/integration/sidecar_services_test.go), `:421-427` | Both polls check `IsNotFound`, and `Eventually` checks immediately, so a cache that has not yet seen the test's own `Create` (`:392`, `:409`) satisfies them. A no-op `deleteLegacyServices` would pass whenever that happens, and goes red after 10 s when the cache already holds the first legacy Service. | unit, positive: `TestReconcile_DeletesLegacyClientService` and `TestReconcile_DeletesLegacyReadService`, [`valkey_controller_test.go:534`](../../internal/controller/valkey_controller_test.go), `:567`; error paths and the foreign-owner skip: [`resource_reconcile_test.go:915`](../../internal/controller/resource_reconcile_test.go) ff. *(Corrected 2026-09-26: only the second group was named, and none of those three tests asserts that an owned legacy Service is deleted.)* |
| B2 | [`foreign_object_test.go:236-237`](../../test/integration/foreign_object_test.go) | The assertion runs as soon as the poll sees `ReconcileBlocked`, which the first pass writes. The nudge cannot land until `nudgeGracePeriod` = 10 s after a pass first observed the StatefulSet short ([`nudge.go:26`](../../internal/controller/nudge.go), `:229`). With the ownership guard (`nudge.go:211`) removed, the test therefore stays green unless the condition poll alone takes more than 10 s of its 30 s budget. This is vacuous **by construction**, not by cache timing. | unit: `TestNudgeShortStatefulSets_DoesNotNudgeAForeignStatefulSet`, [`foreign_object_test.go:424`](../../internal/controller/foreign_object_test.go) (it simulates the elapsed grace with `pastGrace`) |
| B3 | [`observer_test.go:123-128`](../../test/integration/observer_test.go), `:87-94`; [`foreign_object_test.go:82-85`](../../test/integration/foreign_object_test.go), `:88-93`, `:311-326`; [`tls_material_test.go:182-184`](../../test/integration/tls_material_test.go); [`volumeclaim_conflict_test.go:150-169`](../../test/integration/volumeclaim_conflict_test.go), `:356-362`; [`integration_test.go:99-105`](../../test/integration/integration_test.go) | Each assertion ("X is absent", "not `Error`", "foreign object untouched") runs before the step or write it rules out has necessarily happened, or through another type's informer. Examples: the StatefulSet appears before the monitoring and TLS-material steps (`resourceReconcileSteps`, `valkey_controller.go:551-574`), and before `ReconcileBlocked` and the phase are written. `integration_test.go:99-105` was **found in this re-check and was not in the audit**: it checks that the `-all`/`-r` Services are absent through the cache (`assert.Error` on a cached `Get`). | unit tier, for the two foreign-object sets: `TestReconcileSidecarRBAC_WritesNoGrantWhenTheServiceAccountIsForeign` and `TestReconcileMetricsService_ForeignServiceDoesNotFailThePass`, [`internal/controller/foreign_object_test.go:132`](../../internal/controller/foreign_object_test.go), `:627` (read). *(Corrected 2026-09-26: cited the span `:63`-`:990`, which the file outruns, up to `:1152`.)* For the other sets (observer disabled, `TLSMaterialStale` on a non-TLS cluster, the storage conditions, the standalone Service set) the unit guard was not traced. Which of the named unit tests carry a recorded mutation check was not traced either. |

**Class C: latent (holds today only on timing, or because no operator write happens in the gap).**

| # | Site | Holds because | Would break when |
|---|---|---|---|
| C1 | [`foreign_object_test.go:107-109`](../../test/integration/foreign_object_test.go) | The ServiceAccount is read after the RoleBinding poll. The operator creates them in the order SA → Role → RB (`reconcileSidecarRBAC`, `valkey_controller.go:983-1003`), in separate informers. | The SA ADD lags the RB by more than two round trips plus a tick offset (verifier: negligible rate) |
| C2 | [`integration_test.go:108-125`](../../test/integration/integration_test.go), `:196-200`, `:266-341`, `:423-428`, `:514-519` | These unpolled `Get`s of the SA, Role, RB, ConfigMaps, Services and Sentinel objects follow a polled StatefulSet that was created several round trips later (`valkey_controller.go:551-574`, `:1379-1396`) | One informer lags another by more than that gap |
| C3 | [`sidecar_services_test.go:298-321`](../../test/integration/sidecar_services_test.go), `:430-439` | Role and RB are read after the SA poll, and `-r`/`-all` after the `-rw` poll (`reconcileServices`, `valkey_controller.go:806-815`). In a run of the whole test, the StatefulSet poll at `:209-213` precedes `:298-321`, and the StatefulSet step runs after sidecar RBAC (`valkey_controller.go:557-558`), so only informer lag remains there. Run alone under a subtest filter, only the Service polls (`:64-67`) precede it, and it holds because pass 1 usually finishes before a 250 ms tick (the audit's "direct race under `-run`" was **overstated**, per the verifier). | Pass 1 straddles the tick (subtest filter), or one informer lags another (full run) |
| C4 | [`reconcile_concurrency_test.go:142-154`](../../test/integration/reconcile_concurrency_test.go) | `Status().Update` is called on the first cached StatefulSet, with no retry. The operator rewrites the StatefulSet only on drift (`valkey_controller.go:1366-1373`) or through the nudge after 10 s, and envtest runs no StatefulSet controller. | A future second-pass write to that StatefulSet, or a CI run slow enough to reach the nudge |
| C5 | `writePhase`, `valkey_controller.go:2614-2628` with `:295` | See Mechanism. Production code, and the next pass heals it. | It already widens A1's window today |

**What the verifier refuted.** The audit said the comments at `observer_test.go:114` ("ensures
reconcile ran") and `foreign_object_test.go:305-306` were wrong about step order. The verifier
disagreed: both comments are literally true, because the pass has run. They only mislead about
whether the pass has *completed*.

**Checked and safe (audit, spot-checked by the verifier; not re-read line by line in this
pass).**
- Reads that take the object from the write's own answer (`pod_security_test.go`,
  `pod_hardening_test.go`, `createClaimGuardFixture`).
- The uncached client in `pdb_uid_precondition_test.go`.
- Poll-guarded writes (`affinity_test.go`, `pdb_test.go`, `tls_material_test.go:113-122`,
  `updateValkeyForClaimGuard`).
- A `Get` after polling the same object: the cache only moves forward.
- Reads where the operator's decision proves that the shared cache held the object: the
  `ForeignObject` sites, and `foreign_object_test.go:163`.
- All scraping in `metrics_test.go`, which is polled.

**Run, this pass.** `make test-integration` on `a8e8931`, 13 consecutive runs on this host
(2026-09-26, 20:23-20:29 local, 22-25 s each, `-count=1`): **13 of 13 green.** At the
current rate a green streak cannot tell the fixed tree from the unfixed one. That is why the
Verification section asks for a mechanism check and not a repetition count, the same limit
ADR 0017 records for D50.

**Run, adversarial re-check of this file.** `make test-integration` on `a8e8931`, 3 more
consecutive runs (2026-09-26, 20:34-20:36 local, 22-24 s test time each): **3 of 3 green**, 29
top-level tests passed, 0 failed, 0 skipped in each log. Same limit as above.

**Verified:**
- read at `a8e8931`: every file:line in the three tables and in Mechanism;
- read in the module cache: the testify, controller-runtime cache and decoder sources;
- read with `git diff`: the change history of the suite;
- read on the public GitHub API: the CI conclusions on `e2ce8bb`, `e6a9d7c` and `a8e8931`, and
  the one annotation of the red check run (no test name in it);
- read in the re-check: every file:line again (four line numbers corrected, see History), the
  operator's own rate limiter (`ratelimiter.go:71-80`), the positive unit guards of B1, and
  `pdb_uid_precondition_test.go:46` building an uncached client already;
- run: 13 + 3 green runs of `make test-integration`.

**Not verified:**
- No site in classes A–C was reproduced. Every rate is the audit's estimate from the pass
  structure.
- The 2-of-4 reproduction and the 6/6 + 3/3 after the fix are taken from T31, not re-run.
- The CI job log of `e2ce8bb` was not read (it needs authentication), so the name of the test
  that failed in CI comes from T31, and T31 does not record how it established that. That CI's
  red was this race is therefore an attribution, not a reading.
- The claim that informers deliver in resourceVersion order is standard behaviour and was not
  re-read in client-go.
- Branch protection was not read (it needs authentication). That `Integration Tests (envtest)`
  is required rests on [ADR 0017](../adr/0017-test-and-ci-policy.md) D47.
- Which of the unit-tier guards named in class B carry a recorded mutation check was not traced.

## Impact

- **The required check can go red for nothing.** `Integration Tests (envtest)` is one of the
  twelve required contexts (ADR 0017 D47, read). ADR 0017's Consequences say it outright: a
  flaky required check blocks every merge, and the policy answer is to fix or quarantine it,
  never to drop the context (`0017:1096-1100`). The same red also holds Renovate's automerge
  (**hypothesis**: automerge waits on the required checks). The class has
  shown that it can do this: the race reproduced locally on `e2ce8bb` (T31, 2 of 4 runs), and
  CI's integration check was red on the same commit (read; which test failed there is T31's
  attribution, see Not verified). Classes A1–A3 are the known remaining ways it can happen
  again.
- **Some assertions do not guard what they name.** Class B assertions cover ADR 0020
  provenance, the legacy-Service cleanup, the observer toggle, the TLS evaluator's
  upgrade-neutrality, the ADR 0023 storage conditions and the standalone Service set. As
  written, they cannot catch a regression on at least some runs (B2 on every run). This violates
  [ADR 0017](../adr/0017-test-and-ci-policy.md) D10 (an assertion that still holds with the
  guard deleted is not coverage) for B2 outright, and D9 (a probabilistic guard is not a
  guard) for B1 and B3. Whether each B3 set lacks a positive control (D11) was not checked set by
  set.
- **Security: none.** The security-relevant guards behind class B are the ADR 0020 refusals,
  and each of them also has a unit-tier guard (read: the foreign-object tests named in the
  B2 and B3 rows). The integration assertion is therefore a second layer that does not work, not the
  only one. The other class-B guards are not security guards. No principal gains anything from
  a flaky or vacuous test.
- **The pattern recurs.** The reproduced instance was a test new on this branch (added in
  `bb6c78f`, read with `git log`). Nothing in the suite stops the next `Get` after a `Create`.

## Options

The filing bar allows an Options section: the severity is medium and the trigger is live. The
mechanism was reproduced locally on one site (now fixed), and every remaining site runs in a
required check on every push. No remaining site has been reproduced; "live" rests on that
reachability, not on a reproduction.

| | What | Cost |
|---|---|---|
| **A** | Fix each site within the cached-client design. A1 → `requirePhaseError` (the suite is one package, so it is callable from every file as it is; moving it into a shared helper file is optional). A2 → take the pod from the last `Patch` answer, or read it through `apiReader`. A3, C4 → retry the `Get`+write inside a poll, as `pod_hardening_test.go:150-158` does. B1 → poll `apiReader` for NotFound. B3 → first wait for the positive state the completed pass writes (the phase or `Ready`), then read the negative through `apiReader`, and add a positive control where the set lacks one (D11). C1–C3 → turn each unpolled read into a poll. | 26 cited ranges in 7 test files, plus `writePhase` if W1 is taken. *(Corrected 2026-09-26: said "about 20 sites in 8 files".)* It protects only the sites on this list: the next `Get` after a `Create` repeats the class. |
| **B** | Make the suite's reads uncached. `k8sClient` becomes a client built from the envtest config (`client.New`), so every test read comes from the API server, and `apiReader` merges into it. The suite already builds one such client for a single test (`pdb_uid_precondition_test.go:46`, read). The write-order half (A1, A3, B2, B3, C4) still gets its per-site fix from A. | One line removes the cache-lag half for every present and future test: A2, B1, C1–C3, and every "never created" check. Tests no longer read what the operator reads. The audit found no test that relies on that (**read**: no `MatchingFields`/index use in `test/integration`). Whether anything else depends on it is a **hypothesis** until the suite runs. |
| **C** | Quarantine the class-A tests. ADR 0017 names this as the alternative to a fix. | It removes the only end-to-end coverage of the recovery without a restart (ADR 0020 for the two foreign-object tests, ADR 0023 for the claim guard), plus the TLS-carrier and observer-toggle tests of A2 and A3. Nothing is fixed, and the vacuous half stays. |

For every option there is a separate sub-question about production code:

| | `writePhase` (C5) | Cost |
|---|---|---|
| **W1** | `retry.RetryOnConflict` around the `Get`+`Update`, the same shape as `writeStatusCondition`. | Production change: a unit test with a conflict-injecting client and a mutation check (D7). It narrows A1's window but does not close it, because the condition and the phase stay two separate writes. |
| **W2** | Leave it. It heals itself: the next rate-limited pass writes `Error`. | None. A1's window stays one pass wider than it has to be. |

**B2 has no cheap positive fix.** Waiting past the nudge would take `nudgeGracePeriod` plus the
next pass. That pass is error-driven here, so the rate limiter's backoff decides when it comes,
not the 5 s `nudgeRequeueInterval` (`nudge.go:39`). The limiter is the operator's own
(`newReconcileRateLimiter`, `ratelimiter.go:71-80`, read): 5 ms doubling per consecutive
failure, capped at 30 s. With only rate-limited passes they start at about 5 ms · (2^k − 1), so
the first pass after the 10 s grace is the twelfth, at about 10.2 s plus the passes' own run
time. Watch-driven passes (the Owns events of the objects pass 1 creates) raise the failure
count without adding time and push that pass later, up to the 30 s cap after the grace
(**hypothesis**: computed from those parameters, not measured). The negative check would have
to wait at least 10 s per run, and how much longer the backoff decides. *(Corrected 2026-09-26:
this cited "the default controller rate limiter", which the operator replaces.)* The
alternative is to delete the assertion and name the unit test as the guard in a comment (D9:
the deterministic guard is the unit test).

## Decision

**Open.** Hans has not decided yet.

**Recommendation: B, plus A's per-site fixes for the write-order half, plus W1. B2 is resolved
by deleting the assertion in favour of the unit guard.**
- **Why B.** The class was reproduced on a test new on this branch. A per-site list does not
  stop the next one, and one line does.
- **Why the per-site fixes are still needed.** They are the half that B cannot reach: the
  operator's write order is not a cache property.
- **Why W1.** The function sits next to its own documented counter-example (`writeStatusCondition`,
  `:2678-2684`).
- **Why delete B2's assertion.** Waiting past the nudge buys a slow, backoff-timed negative
  check for a guard the unit tier already pins deterministically.

**Also recommended, and not a current rule: convert touched waits to
`PollUntilContextTimeout`.** Every wait the fix touches would use
`wait.PollUntilContextTimeout` with an explicit interval and budget and log the last observed
value. ADR 0017 D25 states this rule for e2e only (read: "E2E waits poll…"). The two newest
integration helpers already follow it by choice (`awaitClaimGuard`,
`volumeclaim_conflict_test.go:543-548`, which cites D25; `requirePhaseError`). Extending it to
the integration tier, which has 57 lines with `require.Eventually`/`assert.Eventually` (read,
`grep`), is part of the recommendation.

## Verification

Done when every line holds, with the command and date recorded here:

- [ ] **A1, by mechanism.** In a scratch copy, add a delay (for example 500 ms) in front of the
  `writePhase` call at `valkey_controller.go:295`. The unfixed test at each A1 site then fails,
  and the fixed test passes. The delay never lands in the tree.
- [ ] **A2, A3, C4.** Grep proof: no `k8sClient.Get` of an object created or written earlier in
  the same test is followed by an assertion or a write without either a poll or the write
  answer (or, under B, the uncached client).
- [ ] **B1.** Mutation: `deleteLegacyServices` returns nil without deleting. The fixed test
  fails on every run.
- [ ] **B3.** One mutation per negative set:
  - the observer is created while disabled;
  - `TLSMaterialStale` is written on a non-TLS cluster;
  - `StorageSpecNotApplied` is written with no conflict;
  - the metrics refusal fails the pass;
  - the sidecar grant is written onto a foreign ServiceAccount;
  - a RoleBinding names the observer ServiceAccount (`observer_test.go:87-94`);
  - a parameter-only claim conflict fails the pass (`volumeclaim_conflict_test.go:356-362`);
  - a standalone cluster gets the `-all`/`-r` Services (`integration_test.go:99-105`).
  *(The last three added 2026-09-26: B3 has nine ranges, and the list covered six of them.)*

  Each mutation fails its fixed test. Each set also carries a positive control (D11).
- [ ] **B2.** The assertion is gone, and its comment names
  `TestNudgeShortStatefulSets_DoesNotNudgeAForeignStatefulSet`. That unit test fails with
  `nudge.go:211` removed (mutation, recorded).
- [ ] **W1 (if taken).** A unit test injects one 409 and asserts that the phase lands. It fails
  with the retry removed.
- [ ] `make test-integration` green locally in at least 10 consecutive runs, `make lint`,
  `make cyclo`, and `Integration Tests (envtest)` green in CI on the fix commit. A streak does
  not prove a zero rate (13 + 3 runs were already green before the fix); the mutation lines
  above are the proof.

## Adjacent findings

- **Closed by reading in this pass: the verifier's "missed defect C".** The concern was that
  `writeWorkload` ([`pod_hardening.go:54-73`](../../internal/controller/pod_hardening.go)) detects
  a dropped `hostUsers` by reading the write answer (`spec.HostUsers == nil` after `Create`/`Update`,
  `:66`). The verifier confirmed that this holds for protobuf and left JSON open: a JSON decode
  into the existing object could keep a stale `&false`. controller-runtime v0.25.1 zeroes the
  decode target for every typed client, whatever the content type (`targetZeroingDecoder`,
  `apiutil/apimachinery.go:224-240`, read). The concern therefore does not arise on the pinned
  version. It would return only with an unstructured client or a controller-runtime that drops
  the zeroing.
- **The sibling in the e2e tier is
  [T34](034-e2e-fixtures-wait-on-controller-state-after-a-pod-delete.md)** (its own file
  since 2026-09-26; the link pointed at the board before). e2e fixtures wait on controller
  state after a pod delete. It has the same root: a read that a stale observer can satisfy.

## History

- 2026-09-27 — renamed to `033-integration-tests-read-the-cache-after-a-write.md` (was `local_T33-integration-tests-read-the-cache-after-a-write.md`) when the tickets were numbered.
- 2026-09-26 — **adversarial re-check** of this file and its board row, every file:line
  re-opened at `a8e8931`. State, severity, urgency and the open Decision unchanged; rule 3 still
  matches, with "live" now resting explicitly on reachability plus the one local reproduction.
  Corrected in place, each marked where it stands:
  - line numbers: the Provisioning write `:255-259` → `:256-260`, `writePhase` `:2614-2627` →
    `:2614-2628` (Mechanism and C5), the returned `resourceErr` `:296` → `:297`, B3's
    `foreign_object_test.go:90-93` → `:88-93` (the cached `Get` is at `:89`);
  - merge base `9925539` (stale local `main`) → `e3e869d` (`origin/main`), and "all remaining
    sites identical to `main`" narrowed to the test sites, C5 being in a file that differs;
  - "only CONFIRMED findings carried over" → plus the verifier's own additions (B2, part of B3);
  - class B was headed "cannot go red": only B2 is; B1 and B3 are probabilistic (D9);
  - B1's guard named only error-path tests; the positive guards are
    `TestReconcile_DeletesLegacyClientService`/`…ReadService`; B3's guard was the span
    `foreign_object_test.go:63-990`, now the two tests that guard its foreign-object sets, and
    the other B3 sets say their unit guard was not traced;
  - the B3 mutation list covered six of nine ranges; three mutations added;
  - B2's wait cost was derived from "the default controller rate limiter"; the operator has its
    own (5 ms doubling, 30 s cap), giving at least 10 s and at most about 40 s (hypothesis);
  - "reproduced in CI" → CI red read, attribution to this race is T31's (the check run's public
    annotation names no test);
  - A3's 409 needs no cache lag: a `Get` before the operator's status write is enough;
  - C3's `:298-321` is preceded by a StatefulSet poll in a full run, so only informer lag
    remains there;
  - effort basis: 26 cited ranges in 7 test files, not "about 20 in 8".
  - Confirmed by reading: every other site, the testify, informer and decoder sources, the
    three CI conclusions, the unit guard of B2 and C4's drift/nudge premise. Run: 3 more green
    `make test-integration` runs.
- 2026-09-26 — **file written and analysed**, state `filed` → `analysed`.
  - Every audit site was re-read at `a8e8931`~~, and the line numbers are current~~ *(four were
    not; corrected by the re-check above)*.
  - Not carried over: the audit's `pod_hardening_test.go:144-147` finding, fixed before
    `b13377e`.
  - Refuted parts recorded: the `-run` claim for A2, "direct race under `-run`" for C3, and the
    "comments wrong about step order" claim.
  - Added: `integration_test.go:99-105` (B3); missed defect C closed by reading.
  - **Urgency `later` → `next`.** The derivation rules, applied top-down:
    - Rule 1 does not match. The only instances in unreleased code on this branch are fixed, so
      the rest is pre-existing on `main`. No statement in tracked files is *measured* false.
    - Rule 2 does not match. The fix does not gate the release: CI is green on `e6a9d7c` and
      `a8e8931`.
    - **Rule 3 matches:** the severity is medium and the trigger is live. The mechanism was
      reproduced locally ~~and in CI~~ on a sibling site, and every remaining site runs in a
      required check on every push. *(Corrected 2026-09-26: CI going red on `e2ce8bb` is read,
      but which test failed there is T31's attribution, not a reading; the local reproduction
      is T31's 2 of 4 runs. No remaining site has been reproduced.)*
    - The board row had been placed in LATER without the derivation.
  - **Effort `S` → `M`:** ~~about 20 sites in 8 files~~ 26 cited ranges in 7 test files
    *(corrected 2026-09-26)*, one production-code sub-question, and a mutation check per
    negative set.
  - Blocked by the decision above.
- 2026-09-26 — filed as a board row from the CI-red analysis of `e2ce8bb` (T31) and the
  integration-race audit.
