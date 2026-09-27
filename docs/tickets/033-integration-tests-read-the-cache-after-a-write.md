---
id: T33
title: integration tests read through the manager cache right after a write
state: analysed       # re-verified 2026-09-27 at 84a39c2: still open as described, no fix commit since a8e8931; D1-D4 open
severity: medium      # a flaky required check blocks every merge and Renovate automerge (ruleset 23985346, renovate.json:15-18); the lost failover phase is a status label and does not raise it
security: none        # every vacuous refusal assertion has a unit-tier guard (internal/controller/foreign_object_test.go:63, :132, :344, :424, :627, :648)
urgency: next         # rule 3: rule 1 does not match (no unreleased feature, feat/rootless shipped in v1.13.0; the false statements found were in this ticket), rule 2 does not (CI green on ad81a47, 7017676, 84a39c2); severity medium and the trigger is live
effort: M             # B is XS, but the per-site write-order edits, the markers with positive controls, one mutation per set and W1 with two unit tests add up to M
blocked-by: decision  # D1-D4 in Options; the no-decision items of the Work list are not blocked
filed-from: T31, section "CI red on e2ce8bb", and the integration-race audit of 2026-09-26
opened: 2026-09-26
decided:              # not recorded - no decision yet
done:
---

Filed as a board row on 2026-09-26 out of the CI-red analysis of `e2ce8bb`
([T31](archive/031-generated-pods-run-as-root.md), section "CI red on `e2ce8bb`"). This file was
written on the same day. Every file:line below was re-read against the tree at `a8e8931`
(`feat/rootless`, clean). *(Re-read 2026-09-27 at `4a7543e`: `git diff --stat a8e8931 4a7543e`
over `test/integration/` and the cited controller files is empty except for
`internal/controller/volumeclaim_conflict.go`, where `4a7543e` rewrote the message string of
`warnRecreateRequired` and added one line at `:144`; every other test and controller cite holds.
Two cites moved (`volumeclaim_conflict.go:183`, ADR 0017 `:1111-1115`) and one length note was
wrong from the start (`:1183`); all three are corrected in place. An earlier wording of this
note, written the same day, called the controller diff empty; corrected by the adversarial
review of 2026-09-27.)* *(Re-read 2026-09-27 at `84a39c2`: `bcc63c9` added one comment line at
`valkey_controller.go:2290-2291` and one at `:3050`, so every `valkey_controller.go` cite of this
file between `:2291` and `:3047` is one line higher; those links are updated in place. Cites
before `:2290` hold. In `test/integration/` the only change since `a8e8931` is a comment rewrite
in `foreign_object_test.go:170-172` (`bcc63c9`, same line count), so every test cite holds.)*
Each claim is labelled by how it was verified
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
`valkey_controller.go`, which does differ from `main`, outside the lines named.)* *(Updated
2026-09-27 at `84a39c2`: `feat/rootless` was merged into `main` at `ad81a47` and released as
v1.13.0; v1.13.1 is `7017676` (read, `git rev-list -n1 v1.13.0` / `v1.13.1`). "The branch" in
this file therefore means the pre-merge `feat/rootless`; every site below is on `main` and in a
released version. No commit since `a8e8931` fixes any of them.)*

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
     - Every intermediate phase write in between is suppressed: `updatePhase` (`:2606-2611`)
       and `persistStatus` (`:2549-2552`).
     - The stored phase therefore stays at its previous value until `:295`. For a new CR that
       value is `Provisioning`, written at `:256-260`.
     - The informer delivers CR versions in resourceVersion order. A cached CR can therefore
       show the condition without `Error`, but never the reverse. (Audit; this is standard
       informer semantics and was not re-read in client-go.) *(2026-09-27: still not verified.
       The Kubernetes API concepts page cannot settle it: its source
       (`kubernetes/website`, `content/en/docs/reference/using-api/api-concepts.md`, sections
       "Efficient detection of changes" and "Semantics for watch") promises a change stream
       "without missing any events" but states no per-object delivery order. What would settle
       it is the apiserver watch cache and the client-go reflector source at v0.37.1.)*
     - **Which writers a negative read rules out decides whether a completed-pass marker is
       needed** *(added 2026-09-27, read)*. The resource steps run in the order ConfigMaps
       (`:553-554`), Services (`:556`), sidecar RBAC (`:557`), StatefulSet (`:558`), monitoring
       (`:562`), TLS material (`:572`) (`resourceReconcileSteps`, `:551-573`); then the
       condition write (`:276`); then `reconcileWorkload` (`:284`), which runs the nudge
       (`:332`) and `updateStatus` → `persistStatus` (the `Ready` condition, `:2241-2283` for
       standalone); then the `Error` phase (`:295`). A write that comes earlier in this order
       than the positive state the test polled is ruled out by an uncached read after that
       poll; a write that comes later is not, whatever the client.
- ~~**`writePhase` widens the window. It is latent and heals itself.**~~ **`writePhase` can
  lose or delay a phase write. It heals itself in a blocked pass.** *(corrected 2026-09-27 at
  `84a39c2`: outside a blocked pass a lost phase write does not heal in the same way, see the
  `Failover in progress` bullet below, and in the one CI log read the blocked-pass `Error`
  write landed without delay.)*
  - `writePhase` (`:2615-2629`) is a cached `Get` (`:2617`) followed by `Status().Update`
    (`:2628`), with no retry. Its error is discarded at `:295`.
  - `writeStatusCondition` (`:2689-2716`) retries the same shape with `retry.RetryOnConflict`
    (`:2691`), and its doc comment (`:2679-2685`) names the reason: a stale cached read gets a
    409 on the operator's own earlier write.
  - `writePhase` meets exactly this 409 whenever its cached `Get` runs before the informer has
    delivered the condition write (read; how often was not measured).
  - *(Added 2026-09-27, read in code.)* A second trigger exists in code: in a blocked pass
    `persistStatus` still writes whenever a non-phase field changed (the blocked branch
    `:2549-2552` restores only phase and message; the write is `:2570`), for example a first
    `Ready` condition or a change of `ObserverReady` or `ReadyReplicas`, and `writePhase`'s
    cached `Get` then runs shortly after that write. It needs `persistStatus` to reach its
    write, which a new CR usually does not in pass 1: `updateStatus` returns early through
    the suppressed `updatePhase` when the StatefulSet is not yet in the cache or is foreign
    (`:2188-2201`). **Measured against it** (read, CI job log of run `108655236971` on
    `84a39c2`, the green `Integration Tests (envtest)` run, read-only with the owner's
    authenticated `gh run view 36331871661 --job 108655236971 --log`): for `foreign-sa-test`
    (A1 site `foreign_object_test.go:75-78`) the second blocked pass `a8703a13` logs
    `Instance not healthy, requeuing` with `"phase": "Error"` (the log line of
    `valkey_controller.go:376-377`, which reads the phase `updateStatus` leaves, and in a
    blocked pass that is the stored phase restored by `:2549-2552`) before it reaches its own
    `:295`; the same holds for `vct-enable-test` (A1 site `volumeclaim_conflict_test.go:230-233`,
    pass `d6a7c411` after the blocked pass `1a2801ac`). In both cases the first blocked pass's
    `Error` write had landed. An audit claim of 2026-09-27 that the `Error` write of a blocked
    pass "most likely" gets a 409 is therefore **refuted for that run**; the 409 in a blocked
    pass is possible, and its rate is unmeasured, because the discarded error at `:295` is
    never logged.
  - The pass still returns `resourceErr` (`:297`), so the rate limiter re-enters it and the next
    pass writes `Error`. In a blocked pass the result is a delay, not a lost write. The limiter
    is the operator's own (`newReconcileRateLimiter`,
    [`ratelimiter.go:71-79`](../../internal/controller/ratelimiter.go)): 5 ms doubling per
    consecutive failure, capped at 30 s (read). The per-item backoff is reset by a successful
    pass, so a CR that enters the blocked state from a healthy one loses about 5 ms, not up to
    30 s *(added 2026-09-27, read)*.
  - *(Corrected 2026-09-26: the range read `:2614-2627` and the return `:296`; the function ends
    at `:2628` and the return is at `:297`. "This 409 is exactly what `writePhase` meets" read as
    if every blocked pass met it; it needs the cache lag.)* *(2026-09-27: those lines are
    `:2615-2629` and `:297` at `84a39c2`, and the cache lag is no longer the only trigger in
    code, see above.)*
  - **Outside a blocked pass a lost phase write does not heal in the same way** *(added
    2026-09-27, read in code, rate not measured)*. Both writers of the phase
    `Failover in progress` call `updatePhase` right after an `r.Update` of the CR metadata and
    discard its error: [`rolling_update.go:2743-2746`](../../internal/controller/rolling_update.go)
    (`setFailoverTriggered`, `:3508-3516`, ends in `r.Update(ctx, v)`), and `:4045-4062`
    (`persistManualFailoverState`, then `updatePhase` at `:4062`). `grep ValkeyPhaseFailover
    internal/controller` finds no other non-test writer. `writePhase`'s cached `Get` at `:2617`
    runs moments after that `Update` and overwrites the caller's object, whose write answer held
    the new resourceVersion, with whatever the cache holds; if the cache has not delivered the
    `Update`, the `Status().Update` at `:2628` gets a 409 and the phase write is dropped. The
    Sentinel path then returns `RequeueAfter` 15 s (`rolling_update.go:2756`). The phase that
    [CLAUDE.md](../../CLAUDE.md) (section Status) requires to be visible during a failover is
    therefore shown only when the cache happened to be current. How often that is was not
    measured.
  - **409s on the CR status are observed in a green run** *(added 2026-09-27, read)*: the same
    CI log of run `108655236971` holds 4 `Reconciler error` lines whose whole error is
    `Operation cannot be fulfilled on valkeys.vko.gtrfc.com ...: the object has been modified`
    (`aa-test` 16:08:34Z, `foreign-cm-test` 16:08:37Z, `obs-disabled-test` 16:08:41Z,
    `sc-ha-svc` 16:08:44Z), none joined with a resource error. The only CR status writers are
    `valkey_controller.go:2570`, `:2628` and `:2709` (read, `grep Status().Update`), and only
    `:2709` retries. The error text names no site, and `updateStatus`'s error is returned
    unchanged (`:369-370`), so whether these came from `updatePhase` → `writePhase`
    (`:2190`/`:2200` → `:2628`) or from `persistStatus` (`:2204` → `:2570`) is **not
    attributed**. [T35](035-master-records-lag-the-real-master.md) (section "Context: the upgrade
    itself went as intended", bullet Rolls) records "Four
    operator errors, all 409 conflicts" on the wds18 fleet at `ad81a47`, equally
    unattributed. *(Precised 2026-09-27, when the `persistStatus` finding was filed as
    [T78](078-the-status-write-conflicts-with-the-operators-own-earlier-write.md): T78 attributes
    all four to `persistStatus` (`:2570`) by elimination over the saved log of that run (read),
    so none of them came from `writePhase`. T35 has since corrected its wds18 count to three;
    T78 attributes two of them to `setRollingUpdateState`'s metadata `Update`
    (`rolling_update.go:3418`) and the third to `:2570`, again none to `writePhase`.)*
- **testify `Eventually` checks at once.** testify v1.12.1 `Eventually` runs the condition once
  immediately, before the first tick (`assert/assertions.go:2023-2024`, module cache).
  - A positive poll is therefore not delayed.
  - A negative poll (`IsNotFound`) is satisfied by a cache that has not yet seen the object.
  - The condition runs in its own goroutine (`go checkCond()`, `:2024`). A `require` call
    inside it calls `FailNow` off the test goroutine *(added 2026-09-27, read; see Options D4)*.
- **Why the fix pattern works: a write's answer is the stored object.** controller-runtime
  v0.25.1 wraps every typed client in `targetZeroingDecoder`
  (`pkg/client/apiutil/apimachinery.go:210`, `:224-240`, module cache). It zeroes the target and
  then decodes the API server's answer into it. The object passed to `Create`/`Update`/`Patch`
  therefore holds the stored object afterwards, server defaulting included, whatever the
  content type.
- **An uncached test client is cheap** *(added 2026-09-27, read in the module cache)*. envtest's
  `rest.Config` sets QPS 1000 and Burst 2000 (controller-runtime v0.25.1
  `pkg/envtest/server.go:309-313`), against client-go's default of 5 qps and burst 10
  (`k8s.io/client-go@v0.37.1` `rest/config.go:48-49`), so a `client.New` client polling at
  50-250 ms is not throttled. `retry.DefaultRetry` is 5 steps of 10 ms, factor 1.0, jitter 0.1
  (`util/retry/util.go:28-33`).

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
  - ~~The job log needs authentication and was not read. The check run's one public annotation
    says only "Process completed with exit code 2" (read, `check-runs/108379517605/annotations`).
    That CI failed on this test comes from T31 ("Found on the way" and section "CI red on
    `e2ce8bb`"), which does not say how it established that. So CI going red on `e2ce8bb` is
    read, and its attribution to this race is **not verified** here.~~ *(corrected 2026-09-27
    at `84a39c2`: the job log was read with the owner's authenticated, read-only
    `gh run view 36232951766 --repo guided-traffic/valkey-operator --job 108379517605 --log`.
    Its log lines 1505-1524 (09:33:31Z) name `TestPodSecurity_TemplatesSurviveAPIServerDefaulting_Integration`,
    subtests `data_StatefulSet_with_the_ownership_repair` and `observer_Deployment`, both with
    `Error Trace: .../pod_security_test.go:49` and `StatefulSet.apps "posture-it-repair" not
    found` / `Deployment.apps "posture-it-observer" not found`: a cached `Get` right after a
    `Create`. These three `--- FAIL` lines are the only ones, against 94 `--- PASS`. The
    check run's one public annotation still says only "Process completed with exit code 2".
    The attribution of the CI red to this race is therefore **read**, not T31's attribution.)*
  - *(Added 2026-09-27, read on the public GitHub API: `success` on `ad81a47` (v1.13.0, run
    `108480068656`, 2026-09-26T20:03:34Z), `7017676` (v1.13.1, `108538705101`,
    2026-09-27T03:06:43Z) and `84a39c2` (`108655236971`, 2026-09-27T16:09:03Z).)*
- **Read. The same patterns in [`pod_hardening_test.go`](../../test/integration/pod_hardening_test.go):**
  - the `Patch` answer (`:102-105`);
  - `Create` answers (`:184-201`);
  - `requirePhaseError`, which polls the phase (`:204-220`), used at `:148` and `:254`;
  - `apiReader` for "never created" (`:256-258`).
  - The audit's highest-risk site, which was then `pod_hardening_test.go:144-147`, is `:148`
    today. It was fixed before `b13377e` and is **not** carried over below. The file was first
    committed in `b13377e` (read, `git log --diff-filter=A`), after the audit ran (12:49-13:09
    local versus a commit time of 16:26), so the unfixed form never reached a commit.

### Confirmed sites, by what they can do (each re-read at `a8e8931`, again at `84a39c2`)

**Class A: can go red (a real flake source).**

| # | Site | Shape | Flake scenario | Severity |
|---|---|---|---|---|
| A1 | [`foreign_object_test.go:75-78`](../../test/integration/foreign_object_test.go), `:223-226`; [`volumeclaim_conflict_test.go:230-233`](../../test/integration/volumeclaim_conflict_test.go) | One unpolled `Get` asserts `phase == Error` after a poll on `ReconcileBlocked` (and `StorageSpecNotApplied`, which the StatefulSet step writes even earlier, [`volumeclaim_conflict.go`](../../internal/controller/volumeclaim_conflict.go) ~~`:182`~~ *(corrected 2026-09-27: `:183`; `4a7543e` added a line at `:144`)*) | A 250 ms tick (all three polls use 250 ms; `claimGuardInterval`, `volumeclaim_conflict_test.go:48`) lands between the condition write (`:276`) and `writePhase` (`:295`), or its 409-delayed retry pass. The CR still reads its previous phase (`Provisioning` in envtest, where no pod ever runs), and the assertion fails. | real, low rate (audit estimate from the pass structure, not measured; in the one CI log read on 2026-09-27 the `Error` write landed in the first blocked pass at two of the three sites, see Mechanism) |
| A2 | [`tls_material_test.go:293-295`](../../test/integration/tls_material_test.go) | A cached `Get` of a pod the test created at `:272`, with only three `Patch` round trips in between | In the full suite the Pod informer already runs, because every pass lists pods (`listDataPodNames`, `valkey_controller.go:1061`). If it has not yet delivered the ADD, the `Get` returns NotFound. The assertion itself is stable. | real, full suite only. The audit claimed a larger window when the test runs alone under `-run`, and the verifier **refuted** that: the first cached `Get` of a new type blocks until the informer has synced, and its initial LIST holds the pod (controller-runtime `pkg/cache/internal/informers.go:322-331`, read). |
| A3 | [`observer_test.go:159-164`](../../test/integration/observer_test.go) | A single cached `Get` and a spec `Update`, with no retry, right after the observer Deployment appears | The same pass writes the CR status after creating the Deployment. Either `persistStatus` changes `ObserverReady` from nil to `&false` (`valkey_controller.go:2559-2564`) and calls `Status().Update` (`:2570`), or `updatePhase` writes "Waiting for StatefulSet creation" (`:2190`). A `Get` that runs before that write, or misses it in the cache, carries the older resourceVersion, the `Update` gets a 409 and `require.NoError` fails. The first half needs no cache lag at all: it is write order. | real, low rate |

**Class B: never a false red, but a regression can pass (vacuous on some runs, B2 on every
run).** *(Corrected 2026-09-26: the heading said "cannot go red". That holds only for B2. B1 and
B3 do go red on a regression whenever the cache has already caught up, so they are
probabilistic guards, which is what [ADR 0017](../adr/0017-test-and-ci-policy.md) D9 rejects,
not guards that can never fail.)*

| # | Site | How a regression can pass | Guard that does exist |
|---|---|---|---|
| B1 | [`sidecar_services_test.go:413-419`](../../test/integration/sidecar_services_test.go), `:421-427` | Both polls check `IsNotFound`, and `Eventually` checks immediately, so a cache that has not yet seen the test's own `Create` (`:392`, `:409`) satisfies them. A no-op `deleteLegacyServices` would pass whenever that happens, and goes red after 10 s when the cache already holds the first legacy Service. | unit, positive: `TestReconcile_DeletesLegacyClientService` and `TestReconcile_DeletesLegacyReadService`, [`valkey_controller_test.go:534`](../../internal/controller/valkey_controller_test.go), `:567`; error paths and the foreign-owner skip: [`resource_reconcile_test.go:915`](../../internal/controller/resource_reconcile_test.go) ff. *(Corrected 2026-09-26: only the second group was named, and none of those three tests asserts that an owned legacy Service is deleted.)* |
| B2 | [`foreign_object_test.go:236-237`](../../test/integration/foreign_object_test.go) | The assertion runs as soon as the poll sees `ReconcileBlocked`, which the first pass writes. The nudge cannot land until `nudgeGracePeriod` = 10 s after a pass first observed the StatefulSet short ([`nudge.go:26`](../../internal/controller/nudge.go), `:229`). With the ownership guard (`nudge.go:211`) removed, the test therefore stays green unless the condition poll alone takes more than 10 s of its 30 s budget. This is vacuous **by construction**, not by cache timing. *(Measured 2026-09-27, read in the CI log of run `108655236971`: `foreign-sts-test`'s first pass logs at 16:08:35.878, its blocked passes end at 35.9124, 35.9191, 35.9420, 35.9647, 36.0075 and 36.0931 (gaps of about 7, 23, 23, 43 and 86 ms, the 5 ms doubling plus pass time), and the recovery subtest creates the StatefulSet at 36.2516, so the whole first subtest, this assertion included, ran within about 0.4 s of the first pass.)* | unit: `TestNudgeShortStatefulSets_DoesNotNudgeAForeignStatefulSet`, [`foreign_object_test.go:424`](../../internal/controller/foreign_object_test.go) (it simulates the elapsed grace with `pastGrace`) |
| B3 | [`observer_test.go:123-128`](../../test/integration/observer_test.go), `:87-94`; [`foreign_object_test.go:82-85`](../../test/integration/foreign_object_test.go), `:88-93`, `:311-326`, and *(added 2026-09-27)* `:162-167`, `:230-235`, `:369-375`; [`tls_material_test.go:182-184`](../../test/integration/tls_material_test.go); [`volumeclaim_conflict_test.go:150-169`](../../test/integration/volumeclaim_conflict_test.go), `:356-362`; [`integration_test.go:99-105`](../../test/integration/integration_test.go) | Each assertion ("X is absent", "not `Error`", "foreign object untouched") runs before the step or write it rules out has necessarily happened, or through another type's informer. Examples: the StatefulSet appears before the monitoring and TLS-material steps (`resourceReconcileSteps`, `valkey_controller.go:551-574`), and before `ReconcileBlocked` and the phase are written. `integration_test.go:99-105` was **found in this re-check and was not in the audit**: it checks that the `-all`/`-r` Services are absent through the cache (`assert.Error` on a cached `Get`). *(Added 2026-09-27: `foreign_object_test.go:162-167` reads the foreign observer ServiceAccount through the SA informer after a Deployment poll, `:230-235` the foreign StatefulSet's template, labels and ownerReferences through the StatefulSet informer after the CR condition poll, and `:369-375` the foreign ConfigMap through the ConfigMap informer after the CR poll; each is the shape of `:88-93`.)* | unit tier, for the two foreign-object sets: `TestReconcileSidecarRBAC_WritesNoGrantWhenTheServiceAccountIsForeign` and `TestReconcileMetricsService_ForeignServiceDoesNotFailThePass`, [`internal/controller/foreign_object_test.go:132`](../../internal/controller/foreign_object_test.go), `:627` (read). *(Corrected 2026-09-26: cited the span `:63`-`:990`, which the file outruns, up to ~~`:1152`~~ *(corrected 2026-09-27: `:1183`, the length at `a8e8931` as at `4a7543e`)*.)* For the three foreign-object reads added 2026-09-27: `internal/controller/foreign_object_test.go:63`, `:344`, `:648` (read). For the other sets (observer disabled, `TLSMaterialStale` on a non-TLS cluster, the storage conditions, the standalone Service set) the unit guard was not traced. Which of the named unit tests carry a recorded mutation check was not traced either. |
| B4 *(added 2026-09-27)* | [`observer_test.go:177-183`](../../test/integration/observer_test.go) | Polls `err != nil` on the operator-created observer ServiceAccount after disabling the observer; `Eventually` checks immediately, so a cache that never delivered the SA to this subtest satisfies it, the B1 shape. The rate is negligible: the Deployment-deletion poll before it takes at least one pass. | not traced |
| B5 *(added 2026-09-27)* | [`sidecar_services_test.go:466-481`](../../test/integration/sidecar_services_test.go) | Checks through the cache that the standalone `-all`/`-r` Services are absent, after `time.Sleep(2 * time.Second)`: the set of `integration_test.go:99-105` with a sleep in place of a completed-pass marker. | not traced |

**Class C: latent (holds today only on timing, or because no operator write happens in the gap).**

| # | Site | Holds because | Would break when |
|---|---|---|---|
| C1 | [`foreign_object_test.go:107-109`](../../test/integration/foreign_object_test.go) | The ServiceAccount is read after the RoleBinding poll. The operator creates them in the order SA → Role → RB (`reconcileSidecarRBAC`, `valkey_controller.go:983-1003`), in separate informers. | The SA ADD lags the RB by more than two round trips plus a tick offset (verifier: negligible rate) |
| C2 | [`integration_test.go:108-125`](../../test/integration/integration_test.go), `:196-200`, `:266-341`, `:423-428`, `:514-519` | These unpolled `Get`s of the SA, Role, RB, ConfigMaps, Services and Sentinel objects follow a polled StatefulSet that was created several round trips later (`valkey_controller.go:551-574`, `:1379-1396`) | One informer lags another by more than that gap |
| C3 | [`sidecar_services_test.go:298-321`](../../test/integration/sidecar_services_test.go), `:430-439` | Role and RB are read after the SA poll, and `-r`/`-all` after the `-rw` poll (`reconcileServices`, `valkey_controller.go:806-815`). In a run of the whole test, the StatefulSet poll at `:209-213` precedes `:298-321`, and the StatefulSet step runs after sidecar RBAC (`valkey_controller.go:557-558`), so only informer lag remains there. Run alone under a subtest filter, only the Service polls (`:64-67`) precede it, and it holds because pass 1 usually finishes before a 250 ms tick (the audit's "direct race under `-run`" was **overstated**, per the verifier). | Pass 1 straddles the tick (subtest filter), or one informer lags another (full run) |
| C4 | [`reconcile_concurrency_test.go:142-154`](../../test/integration/reconcile_concurrency_test.go) | `Status().Update` is called on the first cached StatefulSet, with no retry. The operator rewrites the StatefulSet only on drift (`valkey_controller.go:1366-1373`) or through the nudge after 10 s, and envtest runs no StatefulSet controller. | A future second-pass write to that StatefulSet, or a CI run slow enough to reach the nudge |
| C5 | `writePhase`, `valkey_controller.go:2615-2629` with `:295` | See Mechanism. Production code, ~~and the next pass heals it~~ *(corrected 2026-09-27: in a blocked pass the next pass heals it)*. | ~~It already widens A1's window today~~ *(corrected 2026-09-27: it can widen A1's window, which the one CI log read did not show; outside a blocked pass it can drop the `Failover in progress` phase, Mechanism)* |

*(Count, 2026-09-27: 26 ranges in the tables as written on 2026-09-26, plus the five added
2026-09-27 (three in B3, B4, B5): 31 cited ranges in the same 7 test files.)*

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
- ~~Reads where the operator's decision proves that the shared cache held the object: the
  `ForeignObject` sites, and `foreign_object_test.go:163`.~~ *(corrected 2026-09-27 at
  `84a39c2`: these reads cannot produce a false red, because the operator's refusal proves its
  cache held the object; but a regression that writes onto the foreign object before reporting
  passes whenever that type's informer lags the one the test polled. By this file's own
  classification they are class B, and `foreign_object_test.go:162-167` (the `:163` read),
  `:230-235` and `:369-375` are now in the B3 row.)*
- All scraping in `metrics_test.go`, which is polled. *(2026-09-27: polled, but its wait
  conditions call `require` inside testify's condition goroutine; see Options D4.)*
- *(Added 2026-09-27.)* `tls_material_test.go:213-217`, a `require.Never` for 3 s through the
  cache on a StatefulSet that must never be created: practically sound. A regression that
  creates it early in the window reaches the cache within milliseconds and is caught at the
  next tick; only a create in the last milliseconds of the window slips through the cache, and
  a create after 3 s passes under any client (a bounded negative, write order). The condition
  touches no `t`.

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
  operator's own rate limiter (`ratelimiter.go:71-79`), the positive unit guards of B1, and
  `pdb_uid_precondition_test.go:46` building an uncached client already;
- run: 13 + 3 green runs of `make test-integration`.

**Not verified:**
- No site in classes A–C was reproduced. Every rate is the audit's estimate from the pass
  structure.
- The 2-of-4 reproduction and the 6/6 + 3/3 after the fix are taken from T31, not re-run.
- ~~The CI job log of `e2ce8bb` was not read (it needs authentication), so the name of the test
  that failed in CI comes from T31, and T31 does not record how it established that. That CI's
  red was this race is therefore an attribution, not a reading.~~ *(corrected 2026-09-27: read;
  see the reproduced instance above.)*
- The claim that informers deliver in resourceVersion order is standard behaviour and was not
  re-read in client-go.
- ~~Branch protection was not read (it needs authentication). That `Integration Tests (envtest)`
  is required rests on [ADR 0017](../adr/0017-test-and-ci-policy.md) D47.~~ *(corrected
  2026-09-27: read. Classic branch protection on `main` answers 404 "Branch not protected"
  (`gh api repos/guided-traffic/valkey-operator/branches/main/protection/required_status_checks`);
  the requirement lives in repository ruleset `23985346`, whose `required_status_checks` rule
  lists the twelve contexts of ADR 0017 D47, `Integration Tests (envtest)` among them
  (`gh api repos/guided-traffic/valkey-operator/rules/branches/main`, re-read 2026-09-27).)*
- Which of the unit-tier guards named in class B carry a recorded mutation check was not traced.

**Verified 2026-09-27 at `4a7543e` (read, no test run):**
- every cite re-opened; only the two marked above moved (plus the `:1183` length note, which was wrong at `a8e8931` already). `go.mod` still pins controller-runtime
  v0.25.1 and testify v1.12.1 (`7017676` moved only `k8s.io/*` to v0.37.1), so the decoder and
  `Eventually` sources cited above still apply;
- `apiReader` is still used once (`pod_hardening_test.go:257`); 57 `require`/`assert.Eventually`
  lines *(precised 2026-09-27 at `84a39c2`: 56 calls plus the comment at
  `volumeclaim_conflict_test.go:546`; the tier also has one `require.Never`,
  `tls_material_test.go:213`, and 10 lines using `wait.PollUntilContextTimeout`)*; no
  `MatchingFields`/`IndexField` anywhere in `test/integration`;
- ~~`make lint` does not see these files: every file in `test/integration` is
  `//go:build integration`, and `Makefile:77-85` and `.golangci.yml` pass no build tags
  ([T43](043-lint-and-vet-skip-every-build-tagged-test-file.md));~~ *(corrected 2026-09-27 at
  `84a39c2`: every file in `test/integration` is `//go:build integration`, and `Makefile:77-85`
  and `.golangci.yml` pass no build tags, so `go vet` and golangci-lint never load these files;
  the `gofmt -l .` line at `Makefile:84` does walk them and lists an unformatted one, but it
  exits 0 and nothing tests its output, so `make lint` cannot fail on them. Measured: a
  `//go:build integration` scratch file `bad_test.go` holding `func  F( ) {  }`, then
  `gofmt -l .` printed `bad_test.go` and `exit=0` (local go1.26.5; `go.mod` asks for 1.27.1);
  [T43](043-lint-and-vet-skip-every-build-tagged-test-file.md) `:90-94` records the same.)*
- [`docs/developer/testing.md:69-71`](../developer/testing.md) says `k8sClient` reads the cache —
  true today, and the one doc sentence Option B makes false *(2026-09-27: B also makes the code
  comment at `suite_test.go:47-50` false and the comment at `pod_hardening_test.go:256`
  redundant; `git grep -n "k8sClient\|apiReader" -- '*.md' ':!docs/tickets'` finds only
  `docs/developer/testing.md:69-70`)*;
- the strings the XS item in the Work list polls for: `errForeignObject`
  ([`foreign_object.go:71`](../../internal/controller/foreign_object.go), wrapped at `:76-77`),
  `errRecreateRequired` ([`volumeclaim_conflict.go:46-47`](../../internal/controller/volumeclaim_conflict.go),
  wrapped at `:53-54`); the phase message is `Failed to reconcile resources: ` plus
  `compactErrorMessage` of the pass error (`valkey_controller.go:295-296`,
  [`reconcile_blocked.go:88-93`](../../internal/controller/reconcile_blocked.go));
- `git grep -nw T33` outside `docs/tickets/`: no hit, so closing needs no citation cleanup.

**Not verified 2026-09-27:** no test was run; whether client-go v0.37.1 changed anything about
informer delivery was not read; every rate stays the audit's estimate.

**Verified 2026-09-27 at `84a39c2` (read and measured, no test run, no `make` target):**
- every cite of this file re-opened; the `valkey_controller.go` cites after `:2290` moved by one
  line (`bcc63c9`) and are updated in place; `git diff --stat a8e8931 HEAD -- test/integration`
  shows only the comment rewrite in `foreign_object_test.go:170-172`;
- the CI job logs of `e2ce8bb` (red) and `84a39c2` (green), read-only with the owner's
  authenticated `gh`; the required-checks ruleset; the CI conclusions on `ad81a47`, `7017676`
  and `84a39c2` (all above);
- `renovate.json:15` `"platformAutomerge": true` and `:18` `"ignoreTests": false`, with the
  Renovate docs (<https://docs.renovatebot.com/configuration-options/>, section
  `automergeType`: "if you have no tests but still want Renovate to automerge, you need to add
  `ignoreTests: true`"), so Renovate's automerge waits for passing checks (read; GitHub's
  native auto-merge semantics were not fetched);
- the five cache-dependent reads the inventory had missed, and the `require.Never` that is
  practically sound (tables above);
- the `gofmt -l` exit code (measured, above);
- in the module cache: envtest's QPS/Burst, client-go's defaults and `retry.DefaultRetry`
  (Mechanism), testify's condition goroutine (`assert/assertions.go:2011-2038`).

**Not verified 2026-09-27 at `84a39c2`:** every rate (the `writePhase` 409 in a blocked pass,
the lost `Failover in progress` phase, the class-A flakes); ~~which status writer produced the 4
bare 409s of run `108655236971`~~ *(attributed by elimination to `persistStatus` in
[T78](078-the-status-write-conflicts-with-the-operators-own-earlier-write.md), 2026-09-27)*; the resourceVersion-order delivery (see Mechanism for what
would settle it); the 13 + 3 local green runs of 2026-09-26 (re-running needs `make`, which
this pass did not run). No Valkey behaviour is claimed in this file, so no docker measurement
was taken.

## Impact

- **The required check can go red for nothing.** `Integration Tests (envtest)` is one of the
  twelve required contexts (ADR 0017 D47, read). ADR 0017's Consequences say it outright: a
  flaky required check blocks every merge, and the policy answer is to fix or quarantine it,
  never to drop the context (~~`0017:1096-1100`~~ *(corrected 2026-09-27: `:1111-1115`; ADR 0017
  grew in `f5c6886` and `4a7543e`)*). ~~The same red also holds Renovate's automerge
  (**hypothesis**: automerge waits on the required checks).~~ *(corrected 2026-09-27: read, not
  a hypothesis. The context is required by ruleset `23985346`, and `renovate.json:15-18` sets
  `platformAutomerge: true` with `ignoreTests: false`, so a red required check holds Renovate's
  automerge; see Verified at `84a39c2`.)* The class has
  shown that it can do this: the race reproduced locally on `e2ce8bb` (T31, 2 of 4 runs), and
  CI's integration check was red on the same commit ~~(read; which test failed there is T31's
  attribution, see Not verified)~~ *(corrected 2026-09-27: the job log names this test and
  `pod_security_test.go:49`, read)*. Classes A1–A3 are the known remaining ways it can happen
  again.
- **Some assertions do not guard what they name.** Class B assertions cover ADR 0020
  provenance, the legacy-Service cleanup, the observer toggle, the TLS evaluator's
  upgrade-neutrality, the ADR 0023 storage conditions and the standalone Service set. As
  written, they cannot catch a regression on at least some runs (B2 on every run). This violates
  [ADR 0017](../adr/0017-test-and-ci-policy.md) D10 (an assertion that still holds with the
  guard deleted is not coverage) for B2 outright, and D9 (a probabilistic guard is not a
  guard) for B1 and B3 *(2026-09-27: and B4, B5)*. Whether each B3 set lacks a positive control
  (D11) was not checked set by set.
- **Security: none.** The security-relevant guards behind class B are the ADR 0020 refusals,
  and each of them also has a unit-tier guard (read: the foreign-object tests named in the
  B2 and B3 rows, `internal/controller/foreign_object_test.go:63`, `:132`, `:344`, `:424`,
  `:627`, `:648`, re-read 2026-09-27). The integration assertion is therefore a second layer
  that does not work, not the only one. The other class-B guards are not security guards. No
  principal gains anything from a flaky or vacuous test.
- **The pattern recurs.** The reproduced instance was a test new on this branch (added in
  `bb6c78f`, read with `git log`). Nothing in the suite stops the next `Get` after a `Create`;
  the re-read of 2026-09-27 found five more cache-dependent reads that the 2026-09-26 inventory
  had missed.
- **A production phase label can be lost** *(added 2026-09-27, read in code, rate not
  measured)*. `writePhase`'s unretried 409 can drop the `Failover in progress` phase written
  right after a metadata update of the CR (Mechanism). This is a stale status label: the
  `Ready` condition and the failover itself are unaffected, and it does not raise this
  ticket's severity.

## Options

The filing bar allows an Options section: the severity is medium and the trigger is live. The
mechanism was reproduced locally on one site (now fixed), CI's one red on this class is read
from its job log, and every remaining site runs in a required check on every push. No remaining
site has been reproduced; "live" rests on that reachability, not on a reproduction.

Four open decisions. D1 comes first because it decides how A2, B1, B3–B5 and C1–C3 are
written; D2 and D3 are independent of it and of each other; D4 is an ADR amendment and comes
last. The write-order fixes of A1, A3, C4 and C3 `:298-321` need none of them (Work list).

### D1 — how the cache-lag half is removed

**Mechanism.** Every test read goes through the manager's informer cache:
[`suite_test.go:129`](../../test/integration/suite_test.go) sets `k8sClient = mgr.GetClient()`,
and the reconciler shares that client and cache (`:98-99`). Writes go to the API server. A read
right after a write, or a read of type Y after a poll on type X, therefore depends on informer
timing: that is A2, B1, B3–B5 and C1–C3, 25 of the 31 cited ranges (the other six, A1, A3, B2
and C4, are write order). The choice changes only how test
reads are served. It does not change the operator, its cache, its RBAC or any production
behaviour, and it rolls nothing. It does not touch the write-order half: A1, A3, B2, C4, the B3
sets whose ruled-out write comes after the polled positive (Work list), and C3
`sidecar_services_test.go:298-321` under a subtest filter, where `reconcileSidecarRBAC`
creates SA, Role and RoleBinding in three calls (`valkey_controller.go:984`, `:992`, `:1003`)
after the Services the parent polls; those need their per-site fix under every option.

- **A — fix each site inside the cached design.** A2 takes the pod from the last `Patch` answer
  or reads it through `apiReader`; B1 and B4 poll `apiReader` for NotFound; B3 and B5 first wait
  for a positive state and then read the negative through `apiReader`; C1–C3 turn each unpolled
  read into a poll. *Cost:* about 25 edits in 7 test files, on top of the write-order edits both
  options share. *Consequence:* it protects only the
  listed sites. The list was incomplete once already (five reads found on 2026-09-27), and the
  one reproduced instance was a newly written test, so the next `Get` after a `Create` repeats
  the class unless a separate guard is built.
- **B — make every test read uncached (recommended).** `k8sClient` becomes
  `client.New(testEnv.Config, client.Options{Scheme: scheme.Scheme})`, and `apiReader` merges
  into it. The suite already runs such a client (`pdb_uid_precondition_test.go:46`, read).
  *Cost:* XS for the client; the comment at `suite_test.go:47-50`, the sentence at
  `docs/developer/testing.md:69-71` and the now redundant `apiReader` use at
  `pod_hardening_test.go:256-257` are rewritten. *Consequence:* test reads stop acting as a
  barrier ("the test saw it, so the operator's informer has it"). The operator may then act on
  an older cache than the test sees, which is how it runs in production, where nobody shares
  its cache. No test was found that relies on that barrier (read: no test passes `k8sClient`
  into operator code; its only non-read uses are the test's own writes; no
  `MatchingFields`/`IndexField`/unstructured read in `test/integration`), but that nothing else
  depends on it is a **hypothesis** until the suite runs. B makes C3 `:298-321` under a subtest
  filter slightly worse, not better: an uncached SA poll hits sooner, which widens the
  SA-to-Role window; it stays in the write-order list.

**Recommended: B.** Checkable: one assignment removes the cache-lag half for every present and
future read; the suite already runs such a client (`pdb_uid_precondition_test.go:46`); envtest's
QPS 1000 / Burst 2000 (`pkg/envtest/server.go:309-313`) means uncached polling is not
throttled; and no test uses cache indexes or unstructured reads. B beats A because A's own
inventory missed five reads in one day and cannot cover the next test, while B needs no
enumeration. B also covers the three foreign-object "untouched" reads added to B3, because in
the regression they target the forbidden write comes in a resource step, before the positive
the test polled (Mechanism, write order).

### D2 — B2, the nudge assertion that cannot fail within its subtest

**Mechanism.** [`foreign_object_test.go:236-237`](../../test/integration/foreign_object_test.go)
asserts that the foreign StatefulSet carries no nudge annotation, right after the first pass's
`ReconcileBlocked` poll. A nudge can only land `nudgeGracePeriod` = 10 s after a pass first
observed the StatefulSet short ([`nudge.go:26`](../../internal/controller/nudge.go),
`:228-231`), and the subtest ends within about 0.4 s of the first pass (measured in CI, B2
row). With the ownership guard at `nudge.go:211` removed the assertion still passes: it is
vacuous by construction, which [ADR 0017](../adr/0017-test-and-ci-policy.md) D10
(`0017:349-351`) says is not coverage. The deterministic guard is the unit test
`TestNudgeShortStatefulSets_DoesNotNudgeAForeignStatefulSet`
([`internal/controller/foreign_object_test.go:424`](../../internal/controller/foreign_object_test.go),
with `pastGrace`). But the nudge guard is a refusal guard (ADR 0020 D8,
[`0020:403-415`](../adr/0020-write-only-what-the-operator-owns.md), names `nudgeStatefulSet`),
and ADR 0017 D12 (`0017:377-382`) requires every refusal guard to be tested at four layers, one
of them a real-API-server test; this assertion is the only envtest line that claims that layer,
and ADR 0020's Residual risks say the guards were "exercised against envtest" (`0020:720-722`).
The choice changes only the integration test file and the ADR text; it changes neither the
guard nor the unit test, and no production behaviour.

- **Delete the assertion and record the unit-only coverage as a named D12 exception
  (recommended).** Delete `:236-237`, name the unit test in a comment, record its mutation check
  (`nudge.go:211` removed, the unit test fails; ADR 0017 D7), and at close add one sentence to
  ADR 0020 (D8 or its Residual risks) that the nudge guard is pinned at the unit tier only, as a
  named exception to ADR 0017 D12. *Cost:* XS plus the ADR sentence. *Consequence:* the
  integration file stops claiming coverage it does not give, and the D12 gap becomes a
  documented exception instead of a silent one.
- **Wait past the nudge.** Keep the assertion behind a wait of `nudgeGracePeriod` plus the next
  pass. That pass is error-driven, so the operator's backoff decides when it comes
  (`newReconcileRateLimiter`, `ratelimiter.go:71-79`: 5 ms doubling, 30 s cap), not the 5 s
  `nudgeRequeueInterval` (`nudge.go:39`). With only rate-limited passes the first pass after
  the 10 s grace is the twelfth, at about 10.2 s (5 ms · (2^11 − 1)); watch-driven passes raise
  the failure count without adding time and push it later, up to the 30 s cap after the grace
  (**hypothesis**: computed, not measured). *Cost:* 10-40 s per run of a required check whose
  whole suite takes about 25 s, with no `t.Parallel` anywhere in `test/integration` (read).
  *Consequence:* it satisfies D12 literally, with timing still set by backoff.

**Recommended: delete, with the named D12 exception.** It beats the wait because D12's own
rationale for the real-API-server layer (the fake client writes no UIDs and runs no garbage
collection, `0017:377-382`) does not apply to a patch that `IsControlledBy` refuses before any
API call, so the real API server adds nothing to this guard's semantics, while the wait roughly
doubles a required check. It is still a decision, not policy-settled work: deleting the only
envtest line of a refusal guard departs from D12, and that departure needs the owner's
agreement and an ADR sentence.

### D3 — does `writePhase` retry a conflict

**Mechanism.** `writePhase`
([`valkey_controller.go:2615-2629`](../../internal/controller/valkey_controller.go)) re-reads the
CR through the cache (`:2617`) into the caller's object and writes the status with that
resourceVersion (`:2628`), with no retry. In a blocked pass its error is discarded at `:295`
and the next pass heals it; in the one CI log read the `Error` write landed in the first
blocked pass at two A1 sites (Mechanism). Outside a blocked pass, the two `Failover in progress`
writers (`rolling_update.go:2746`, `:4062`) call it right after an `r.Update` of the CR and
discard the error, so a 409 there drops the phase until some later write (Mechanism, read in
code, rate not measured); and non-blocked callers that return the error (`:2190`, `:2200`)
fail the pass on a self-inflicted 409~~, which is one possible source of the 4 bare 409s of the
green CI run (unattributed)~~ *(corrected 2026-09-27: [T78](078-the-status-write-conflicts-with-the-operators-own-earlier-write.md)
attributes all four to `persistStatus` by elimination, so none of them is an observation of this
path; the self-inflicted 409 here stays read in code, rate not measured, and W1's recommendation
never rested on those four)*. The neighbour `writeStatusCondition` (`:2689-2716`) documents
exactly this 409 (`:2679-2685`) and retries it (`:2691`), and ADR 0002 D7 (amended 2026-08-22,
[`0002:186-199`](../adr/0002-surface-a-blocked-reconcile-on-the-cr.md)) records that shape for
conditions. The choice touches only the conflict path of the phase write: it rolls nothing,
changes no pod template or hash, leaves non-conflict errors alone (so ADR 0002 D7's
webhook-rejection behaviour and `TestReconcile_InitialPhaseWriteFailureDoesNotAbortPass`, which
injects a non-conflict error, are untouched), does not close A1's window (the condition and the
phase stay two writes), and does not touch `persistStatus`, whose 409 needs a different fix
([T78](078-the-status-write-conflicts-with-the-operators-own-earlier-write.md)).

- **W1 — `retry.RetryOnConflict(retry.DefaultRetry, ...)` around the `Get`, compare and
  `Update` (recommended).** The shape `writeStatusCondition` uses. *Cost:* XS-S: two unit tests
  through `interceptor.Funcs.SubResourceUpdate`, the harness
  `internal/controller/status_phase_test.go:35` already uses: one injects a single 409 and
  asserts the phase lands, one pins the failover case (an `r.Update` of the CR followed by
  `updatePhase` lands the phase); a D7 mutation removes the retry and both fail.
  *Consequence:* the phase of a blocked pass lands in that pass, the `Failover in progress`
  phase lands within `DefaultRetry`'s 5 × 10 ms budget whenever the cache catches up by then,
  and the non-blocked callers stop failing the pass on their own 409. ADR 0002 (D3 or D7) gets
  one sentence that `writePhase` retries a conflict against a freshly read CR. Known residual:
  the cached `Get` still replaces the caller's in-memory object with the cached copy, so a
  caller that just wrote the CR holds an older object when the skip branch (`:2621-2623`)
  returns before any `Update`; no traced caller is harmed by it (after `:2746` the pass only
  triggers the Sentinel failover and returns, `rolling_update.go:2750-2756`), ~~and it belongs
  with the `persistStatus` finding (Adjacent findings)~~ *(corrected 2026-09-27: it stays here;
  [T78](078-the-status-write-conflicts-with-the-operators-own-earlier-write.md) takes the
  `persistStatus` half of the clobber (`:2204`) and leaves the `writePhase` half (`:2617`) to
  this decision. If T78's recommended R1, the refresh read through the `APIReader`, is taken,
  reading `writePhase`'s `Get` through the same reader is a variant this decision may weigh
  then; not costed here)*.
- **W3 — write phase and message with a status merge patch against the caller's object, with
  no cached `Get` and no resourceVersion.** *Cost:* S. *Consequence:* it never produces a
  self-inflicted 409, and the patch answer decodes the stored object into the caller's
  object, which also removes the clobber W1 leaves. But it gives one of the three CR status
  writers a second write shape (no `Status().Patch` exists anywhere in `internal/controller`
  today, read) and drops optimistic concurrency for the phase, against the shape ADR 0002 D7
  records.
- **W2 — leave it.** *Cost:* none. *Consequence:* in a blocked pass the `Error` phase lands in
  the pass or one pass later (about 5 ms when the CR was healthy before); the
  `Failover in progress` phase stays lost whenever the cache lags the preceding `Update`, and
  the non-blocked callers keep failing passes on their own 409.

**Recommended: W1.** Checkable: it is the shape the code already documents as correct at
`valkey_controller.go:2679-2691` and ADR 0002 D7 records, it touches only the 409 path, and the
test harness exists (`status_phase_test.go:35`). It beats W3 on consistency with
`writeStatusCondition` and with ADR 0002 D7, at a smaller change. It beats W2 because W2's
premise that the effect is cosmetic does not hold: the `Failover in progress` phase, which
CLAUDE.md requires to be visible, can be dropped outright (read in code, rate not measured).
*(The earlier justification that the blocked-pass 409 is structural and "almost by
construction" is refuted by the CI log and is not used.)*

### D4 — does ADR 0017 D25 extend to the integration tier

**Mechanism.** ADR 0017 D25 ([`0017:462-471`](../adr/0017-test-and-ci-policy.md)) says new
waits, and every wait touched by a change, use `wait.PollUntilContextTimeout` and never
`require.Eventually`, "whose condition goroutine can outlive the test and touch a finished
`*testing.T`". Its title and its open-item count are e2e-only, and the ADR index row
([`docs/adr/README.md:109`](../adr/README.md)) lists its conversion as open. The integration
tier holds 56 `require`/`assert.Eventually` calls and one `require.Never`
(`tls_material_test.go:213`), next to 10 `PollUntilContextTimeout` lines written by choice
(`awaitClaimGuard`, `volumeclaim_conflict_test.go:543-548`, which cites D25; `awaitTLSRotation`;
`requirePhaseError`; the `pod_hardening_test.go` polls). One file puts `require` inside a
condition goroutine: `scrapeMetrics(t)`
([`metrics_test.go:21-31`](../../test/integration/metrics_test.go), `require.NoError` at `:25`
and `:30`, `require.Equal` at `:28`, a plain `http.Get` with no timeout at `:24`) is called in
the condition of the waits at `:37-39`, `:94-105`, `:114-124` and `:131-134`; no other
integration file uses `t` inside a wait condition (read). In testify v1.12.1 a `require` failure
there marks the test failed and runs `runtime.Goexit` on the condition goroutine only, nothing
is sent on the result channel, and `Eventually` fails at its 15-30 s timer
(`assert/assertions.go:2011-2038`): a correct red, delayed. The `go doc testing.T.FailNow` text
says it "must be called from the goroutine running the test". D25's panic case, a `require` on
a finished `*testing.T`, needs a scrape that outlives the wait budget, which the timeout-less
`http.Get` against an in-process loopback server makes possible but rare. Neither produces a
false pass. The choice changes ADR 0017's text and the wait style of the touched tests, no
production code.

- **a — extend D25 to the integration tier for new waits and every wait this ticket's fix
  touches.** *Cost:* part of the per-site work, plus an ADR 0017 amendment and the index row.
  *Consequence:* codifies what the newest helpers already do; `metrics_test.go` keeps the one
  place in the tier with the shape D25 forbids, because this fix does not touch it.
- **a′ — a, plus converting the four `metrics_test.go` waits whose condition calls `require`
  (recommended).** *Cost:* a plus XS (four waits in one file). *Consequence:* the tier has no
  wait condition left that calls `FailNow` off the test goroutine; the remaining untouched
  `Eventually` calls stay an open conversion item, as D25 already tolerates for e2e.

**Recommended: a′.** It removes the only verified instance in the tier of the shape D25 forbids,
whose present effect is a red delayed by 15-30 s and whose rare effect is a panic on a finished
`*testing.T`, at XS extra cost, and nothing more. It beats a because a leaves that instance in
place for no saving worth the name. It needs an ADR 0017 amendment, so it is the owner's call.

## Decision

**Not decided.** Hans has not decided D1–D4 yet *(checked 2026-09-27 at `84a39c2`)*. The
recommendations live in Options: D1 B, D2 delete with the named D12 exception, D3 W1, D4 a′.
~~**Open.** Hans has not decided yet. *(Checked 2026-09-27: still not decided. The recommendation
below is D1–D4 of Options, in that order.)*~~

~~**Recommendation: B, plus A's per-site fixes for the write-order half, plus W1. B2 is resolved
by deleting the assertion in favour of the unit guard.**~~
- ~~**Why B.** The class was reproduced on a test new on this branch. A per-site list does not
  stop the next one, and one line does.~~
- ~~**Why the per-site fixes are still needed.** They are the half that B cannot reach: the
  operator's write order is not a cache property.~~
- ~~**Why W1.** The function sits next to its own documented counter-example (`writeStatusCondition`,
  `:2678-2684`).~~
- ~~**Why delete B2's assertion.** Waiting past the nudge buys a slow, backoff-timed negative
  check for a guard the unit tier already pins deterministically.~~

~~**Also recommended, and not a current rule: convert touched waits to
`PollUntilContextTimeout`.** Every wait the fix touches would use
`wait.PollUntilContextTimeout` with an explicit interval and budget and log the last observed
value. ADR 0017 D25 states this rule for e2e only (read: "E2E waits poll…"). The two newest
integration helpers already follow it by choice (`awaitClaimGuard`,
`volumeclaim_conflict_test.go:543-548`, which cites D25; `requirePhaseError`). Extending it to
the integration tier, which has 57 lines with `require.Eventually`/`assert.Eventually` (read,
`grep`), is part of the recommendation.~~

*(corrected 2026-09-27 at `84a39c2`: the recommendation text above is superseded by the
per-decision analysis in Options, which states each recommendation with its justification. D2's
recommendation now carries the named ADR 0017 D12 exception, D3's justification no longer rests
on the neighbour alone, and D4's recommendation is a′ with the count precised to 56 calls; see
History.)*

## Work list

*(Added 2026-09-27, updated 2026-09-27 at `84a39c2`.)* The items marked "no decision" are the
same edit under every option, because write order is not a cache property. Each is XS and closes
nothing on its own.

- [ ] **XS, no decision — A1: poll the phase instead of reading it once**, with
  `requirePhaseError` ([`pod_hardening_test.go:204-220`](../../test/integration/pod_hardening_test.go),
  same package):
  - [`foreign_object_test.go:75-78`](../../test/integration/foreign_object_test.go) →
    `requirePhaseError(t, types.NamespacedName{Name: crName, Namespace: "default"}, "sidecar ServiceAccount")`
    (the local `key` there is the SA key, not the CR key);
  - `foreign_object_test.go:223-226` → `requirePhaseError(t, key, "does not control")`;
  - [`volumeclaim_conflict_test.go`](../../test/integration/volumeclaim_conflict_test.go):
    insert `requirePhaseError(t, key, "volumeClaimTemplates are immutable")` before `:230` and
    delete the assertion at `:232-233`; keep the `Get` at `:230-231`, which `:235` reads.
  - Proof: the A1 mechanism line under Verification.
- [ ] **XS, no decision — A3 and C4: read and write inside one poll**, the pattern of
  `pod_hardening_test.go:150-158`:
  - [`observer_test.go:159-164`](../../test/integration/observer_test.go): `Get`, set
    `Spec.Observer.Enabled = false`, `Update`, retried until the `Update` lands (needs the
    `context` and `k8s.io/apimachinery/pkg/util/wait` imports);
  - [`reconcile_concurrency_test.go:148-154`](../../test/integration/reconcile_concurrency_test.go):
    fresh `Get` of the StatefulSet, the six status fields, `Status().Update`, retried; the
    `slowProbes.arm()` at `:144` stays in front (the file already imports `context` and needs
    `wait`).
  - Proof: the grep line under Verification; no deterministic mutation for these two is known.
- [ ] **XS, no decision — C3 `:298-321`: poll the Role and the RoleBinding** (or read them
  after a StatefulSet poll), because they are created after the polled SA
  (`valkey_controller.go:984`, `:992`, `:1003`); needed under A and B alike.
- [ ] **S, no decision — the completed-pass marker for the B3 sets whose ruled-out write comes
  after the polled positive.** Wait for the `Ready` condition with `observedGeneration` equal to
  `metadata.generation` (`updateStatus` sets it after every resource step, `valkey_controller.go:2241-2283`
  standalone, `updateHAStatus` for Sentinel, and `persistStatus` writes it on the first pass that
  reaches it) before `observer_test.go:123-128` and `:87-94`, `tls_material_test.go:182-184`,
  `foreign_object_test.go:311-326`, the `ReconcileBlocked` and phase halves of
  `volumeclaim_conflict_test.go:150-169` (`:163-169`) and `volumeclaim_conflict_test.go:356-362`;
  B5 (`sidecar_services_test.go:466-481`) replaces its `time.Sleep` with the same marker. The
  marker precedes the `Error` phase write of a blocked pass (`persistStatus` runs inside
  `reconcileWorkload`, before `:295`), so it proves a `ReconcileBlocked` assertion but not a
  "phase is not `Error`" assertion alone; every set above that asserts the phase also asserts
  `ReconcileBlocked`, which catches the same regression, and that is to be stated in the test.
  Under B the other B3 sets need no marker, because the writer they rule out runs in a resource
  step before the polled positive: `foreign_object_test.go:82-93`, `:162-167` (the observer SA
  is written before the polled Deployment, `valkey_controller.go:729-735`), `:230-235` (the
  template, label and ownerReference writers are in the StatefulSet step, before `:276`; the
  nudge is a workload-pass writer at `:332` and is B2), `:369-375`, the `StorageSpecNotApplied`
  half of `volumeclaim_conflict_test.go:150-169` (`:158-161`), and `integration_test.go:99-105`.
  Under A they get a positive wait plus an `apiReader` read. Every set gets its positive control
  (ADR 0017 D11).
- [x] **XS, no decision — file the `persistStatus` 409 as its own ticket** — filed as
  [T78](078-the-status-write-conflicts-with-the-operators-own-earlier-write.md) on 2026-09-27.
- [ ] **Waits on D1:** `suite_test.go:129-130` plus `pod_hardening_test.go:256-257` (B), or the
  per-site reads (A); then A2, B1, B4, C1, C2 and C3 `:430-439`.
- [ ] **Waits on D2:** B2 (`foreign_object_test.go:236-237`).
- [ ] **Waits on D3:** W1 (`valkey_controller.go:2615-2629`) with its two unit tests.
- [ ] **Waits on D4:** the wait style of every touched wait, and under a′ the four
  `metrics_test.go` waits.
- [ ] **Close ([ADR 0034](../adr/0034-tickets-are-work-lists-that-get-archived.md)):** the
  decisions taken go into ADR 0017 as a new D (integration reads come from the API server, a
  write-order read polls for the completed pass) and, under a′ or a, the D25 amendment, with
  its Status date, and the ADR index row ([`docs/adr/README.md:109`](../adr/README.md)) if its
  State text changes; under B rewrite [`docs/developer/testing.md:69-71`](../developer/testing.md)
  and the comment at `suite_test.go:47-50`; under W1 one sentence in ADR 0002 that `writePhase`
  retries a conflict; under D2's recommendation the unit-only sentence in ADR 0020 and the named
  D12 exception; `git grep -nw T33` outside `docs/tickets/` (no hit on 2026-09-27 at
  `84a39c2`); then `git mv` to [archive/](archive/).

## Verification

Done when every line holds, with the command and date recorded here:

- [ ] **A1, by mechanism.** ~~In a scratch copy, add a delay (for example 500 ms) in front of the
  `writePhase` call at `valkey_controller.go:295`. The unfixed test at each A1 site then fails,
  and the fixed test passes. The delay never lands in the tree.~~ *(corrected 2026-09-27 at
  `84a39c2`: a scratch copy contradicts [ADR 0017](../adr/0017-test-and-ci-policy.md) D13
  (`0017:384-390`), which runs mutations against the live tree with a byte-exact backup and a
  `sha256`-checked restore, and its rejected alternative "Mutate a copy of the tree instead of
  the live tree" (`0017:1205-1208`). The rule decides it; that alternative's stated reason,
  "loses the real build and test wiring", does not obviously apply to a full clone.)* Inject a
  delay (for example 500 ms) in front of the `writePhase` call at `valkey_controller.go:295` as
  a D13 mutation on the live tree, with the `sha256`-checked restore. The unfixed test at each
  A1 site then fails and the fixed test passes; record the failing output.
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
  - a standalone cluster gets the `-all`/`-r` Services (`integration_test.go:99-105`, and B5
    `sidecar_services_test.go:466-481`).
  *(The last three added 2026-09-26: B3 had nine ranges, and the list covered six of them.)*
  - *(Added 2026-09-27:)* the foreign observer ServiceAccount is written onto
    (`foreign_object_test.go:162-167`);
  - the foreign StatefulSet's template, labels or ownerReferences are written
    (`foreign_object_test.go:230-235`);
  - the foreign ConfigMap is written (`foreign_object_test.go:369-375`).

  Each mutation fails its fixed test. Each set also carries a positive control (D11).
- [ ] **B4.** Mutation: `cleanupObserverServiceAccount` deletes nothing. The fixed test fails on
  every run.
- [ ] **B2.** The assertion is gone, and its comment names
  `TestNudgeShortStatefulSets_DoesNotNudgeAForeignStatefulSet`. That unit test fails with
  `nudge.go:211` removed (mutation, recorded). ADR 0020 names the unit-only coverage (if D2 goes
  as recommended).
- [ ] **W1 (if taken).** A unit test injects one 409 and asserts that the phase lands; a second
  asserts that `updatePhase` after an `r.Update` of the CR lands the `Failover` phase. Both fail
  with the retry removed.
- [ ] **D4 (if a′).** No wait condition in `test/integration` calls a `require` or `assert`
  function (grep).
- [ ] `make test-integration` green locally in at least 10 consecutive runs, `make lint`,
  `make cyclo`, and `Integration Tests (envtest)` green in CI on the fix commit. ~~*(Precised
  2026-09-27: `make lint` checks only the formatting of these files — `gofmt -l .` at
  `Makefile:84` ignores build tags, `go vet` and golangci-lint skip every
  `//go:build integration` file until [T43](043-lint-and-vet-skip-every-build-tagged-test-file.md)
  lands; `make cyclo` ignores `_test.go`, so it matters for W1 only.)*~~ *(corrected 2026-09-27
  at `84a39c2`: `make lint` lists unformatted integration files but cannot fail on them — its
  `gofmt -l .` line at `Makefile:84` exits 0 while listing (measured, Verified at `84a39c2`),
  and `go vet` and golangci-lint never load build-tagged files — until
  [T43](043-lint-and-vet-skip-every-build-tagged-test-file.md) lands; if T43's fix makes the
  `gofmt` line fail, this becomes a real formatting gate. `make cyclo` ignores `_test.go`, so
  it matters for W1 only.)* A streak does
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
  T34 links back (~~`:956-958` in its 2026-09-27 re-verification~~ *(corrected 2026-09-27,
  consistency pass: its Cross-ticket section, T33 bullet, since its re-verification)*; `:624-626`
  at `84a39c2`). If D4
  goes to a or a′, the two tiers share one wait rule; T34 is
  not otherwise affected.
- **The `persistStatus` 409 is [T78](078-the-status-write-conflicts-with-the-operators-own-earlier-write.md)**
  *(filed 2026-09-27; this bullet held the finding until then)*: `updateStatus` refreshes the CR
  from the cache ([`valkey_controller.go:2204`](../../internal/controller/valkey_controller.go))
  and `persistStatus` writes with that resourceVersion and no retry (`:2570`), so a CR write the
  cache has not delivered fails the pass with 409. T78 holds the attribution of the CI and wds18
  409s, why `RetryOnConflict` is the wrong fix there (the reason it is not in D3), its options
  and its relation to T59's A-prime; the `writePhase` half of the in-memory clobber (`:2617`)
  stays with D3.
- **[T35](035-master-records-lag-the-real-master.md) ~~cites "T33 row A2"~~** ~~(`:449-451`)~~
  *(corrected 2026-09-27, sweep: T35 no longer cites this file for it; its Decision 5 mechanism
  states the claim itself, pointing at `steady_state_master.go:195-200`)* for the
  claim that every pass lists pods through the cached client, pointing at
  `steady_state_master.go:195-199`, where this file points at `listDataPodNames`
  (`valkey_controller.go:1061-1070`). Both lists exist and the claims agree. Option B changes
  only test reads, so T35's Pod-informer premise is untouched. T35 (section "Context: the upgrade
  itself went as intended", bullet Rolls) also records four
  409 conflicts on the wds18 fleet at `ad81a47`~~, which is relevant to D3 and to the
  `persistStatus` finding above~~ *(corrected 2026-09-27: T35 now counts three, and
  [T78](078-the-status-write-conflicts-with-the-operators-own-earlier-write.md) attributes two to
  `setRollingUpdateState`'s metadata `Update` and one to `persistStatus`, none to `writePhase`, so
  they are T78's evidence and not D3's)*.
- **[T31](archive/031-generated-pods-run-as-root.md)** holds the reproduction numbers
  (`archive/031:999-1006`: 2 of 4 red, 6/6 + 3/3 green) consistently. T31 does not say how it
  attributed the CI red to this test; the job log read on 2026-09-27 now confirms the
  attribution.

## History

- 2026-09-27: re-verified at `84a39c2` against an audit, a facts review and a design review,
  with the conflicts between them settled by reading the code and the CI logs. Checked: every
  cite; the currency (no fix commit since `a8e8931`; `feat/rootless` merged at `ad81a47`,
  v1.13.0; `Integration Tests (envtest)` green on `ad81a47`, `7017676` and `84a39c2`).
  **Location drift** (fixed in the links): the `valkey_controller.go` cites after `:2290` moved
  by one line in `bcc63c9` (`updatePhase` `:2606-2611`, the blocked branch of `persistStatus`
  `:2549-2552`, `ObserverReady` `:2559-2564`, `Status().Update` `:2570`, `writePhase`
  `:2615-2629`, `writeStatusCondition` `:2689-2716` with its doc comment `:2679-2685`);
  `ratelimiter.go:71-80` precised to `:71-79`, where the function ends. **Found false or
  outdated, corrected in place:** the `make lint` wording in Verified at `4a7543e` and in
  Verification (it lists unformatted integration files but cannot fail: `gofmt -l .` exits 0,
  measured on a scratch `//go:build integration` file); the A1 verification method "in a
  scratch copy", which contradicts ADR 0017 D13; the "Checked and safe" entry for the
  `ForeignObject` reads and `foreign_object_test.go:163`, which are class B; the "job log not
  read" statements and the Renovate automerge hypothesis (both now read); the "Branch
  protection was not read" bullet (ruleset `23985346` read); the 57 `Eventually` lines precised
  to 56 calls plus a comment, and the missing `require.Never`; the branch framing (the work is
  on `main` and released). **Refuted in review, not written as fact:** the audit's claim that
  the `Error` write of a blocked pass "most likely" gets a 409 because of `persistStatus`'s own
  write; the CI log of run `108655236971` shows the `Error` phase stored after the first blocked
  pass at `foreign-sa-test` and `vct-enable-test`. **Read in the CI logs (owner's authenticated
  `gh`, read-only):** the `e2ce8bb` red names this test at `pod_security_test.go:49` (3 FAIL,
  94 PASS); 4 bare CR-status 409s in the green run on `84a39c2`, unattributed; B2's first
  subtest finishing within about 0.4 s of the first pass, with backoff gaps of about 7, 23, 23,
  43 and 86 ms. **New facts added:** five cache-dependent reads missing from the inventory (three
  foreign-object reads into B3, B4 `observer_test.go:177-183`, B5
  `sidecar_services_test.go:466-481`; now 31 ranges in 7 files) and `tls_material_test.go:213-217`
  recorded as practically sound; the writer-order rule for completed-pass markers; the lost
  `Failover in progress` phase at `rolling_update.go:2746` and `:4062` (read in code, rate not
  measured); the `persistStatus` 409 finding (Adjacent findings, to be filed as its own ticket;
  not filed here); `require` inside the `metrics_test.go` wait conditions; envtest's QPS/Burst
  and `retry.DefaultRetry`; the C14 ordering claim cannot be settled from the Kubernetes docs
  page. No docker measurement: the file makes no Valkey behaviour claim. **Options rewritten**
  as four decision subsections, mechanism first, and the recommendation removed from the
  Decision section, which now reads "Not decided". Removed options: D1 **C** (quarantine the
  class-A tests) — disproportionate to an XS A1 fix, removes the real-API-server layer ADR 0017
  D12 requires for the ADR 0020 and ADR 0023 refusal guards (the recovery tests
  `foreign_object_test.go:96-119`, `:240-260` and `volumeclaim_conflict_test.go:253-283`), plus
  the TLS-carrier and observer-toggle tests of A2 and A3, leaves every vacuous class-B assertion
  in place, and fixes nothing; D4 "convert all
  57 lines" — disproportionate, no defect is known in the calls beyond the four `metrics_test.go`
  waits, effort grows toward L; D4 "leave D25 e2e-only" — D25's stated reason (testify's
  goroutine-based `Eventually`) is identical in both tiers and ADR 0017 records no deliberate
  e2e scoping, so keeping a rule scoped by title alone is no argued choice. Considered and not
  added: D2 "keep the assertion and label it a forward assertion" (ADR 0017 rejected keeping
  vacuous assertions, `0017:1180-1182`; D9's forward assertions are probabilistic ones); D2
  "shorten `nudgeGracePeriod` in the suite" (envtest runs no StatefulSet controller, so every
  StatefulSet in the suite would be nudged, and those patches would race the unretried
  `Status().Update` of C4). Added options: D3 **W3** (status merge patch, runner-up) and D4
  **a′**. Recommendation changes: D1 stays B, with its scope corrected (C3 `:298-321` stays a
  write-order site, and B makes it slightly worse); D2 changes from "delete" to "delete and
  record the unit-only coverage as a named ADR 0017 D12 exception in ADR 0020", because D12
  requires a real-API-server layer for every refusal guard and this was its only envtest line,
  and D2 stays a decision, not policy-settled work; D3 stays W1, but its justification no
  longer claims the blocked-pass 409 is structural (refuted) and rests on the lost
  `Failover in progress` phase and consistency with `writeStatusCondition`; D4 changes from a to
  a′, which also converts the four `metrics_test.go` waits, with the hazard stated as a
  delayed red and a rare panic, not a false pass. Work list: C3 `:298-321` and the
  completed-pass markers added as no-decision items, the marker scoped to the sets whose
  ruled-out write follows the polled positive, filing the `persistStatus` ticket added, the
  Close step extended (suite comment, ADR 0002 under W1, ADR 0020 under D2). **Frontmatter
  unchanged:** state `analysed` (nothing decided); severity `medium` (a flaky required check
  blocks every merge and Renovate automerge; the lost failover phase is a status label and does
  not raise it); security `none` (every vacuous refusal assertion has a unit guard, re-read);
  urgency `next` by rule 3 (rule 1: no unreleased feature, the false statements were in this
  ticket and are corrected here; rule 2: CI green on `ad81a47`, `7017676`, `84a39c2`, nothing
  gates a release; rule 3: medium and live); effort `M` (B is XS, but the per-site edits, the
  markers with positive controls, one mutation per set and W1 with two unit tests add up to
  M); blocked-by `decision`. The frontmatter values are unchanged and now carry their reasons as
  comments, as in the skeleton. **Review of this edit, same day:** it re-read the cited code
  (`writePhase`, `persistStatus`, `writeStatusCondition`, the `Failover in progress` writers at
  `rolling_update.go:2743-2746` and `:4062`, `reconcileObserver` inside the monitoring step at
  `valkey_controller.go:580`, `metrics_test.go:21-39`, testify's `Eventually`, envtest's
  QPS/Burst, `retry.DefaultRetry`) and the saved CI log of run `108655236971`: the first
  `foreign-sa-test` pass `fb101655` creates the objects and the next pass `a8703a13` already
  logs phase `Error`; `foreign-sts-test` and the 4 bare 409 lines as stated. It then struck in
  place two corrections the edit had made silently (the `writePhase` bullet's old lead, "widens
  the window. It is latent and heals itself", and the C5 row), restored the 2026-09-26
  correction note on the `writePhase` lines that the edit had deleted, corrected D1's count (the
  cache-lag half is 25 of the 31 ranges, not all 31; option A's cost with it), added the
  recovery-test ranges and the A2/A3 tests to the reason option C was removed, fixed the drifted
  cross-ticket cites (T43 `:90-94`, T34 `:956-958`, T35 `:449-451`) and named the fleet of
  T35's four 409s as wds18.
  Cross-ticket: in the consistency pass of the same day, the T34 back-link cite was replaced by
  the section name, and the `persistStatus` 409 finding gained its relation to T59's recommended
  A-prime (the capture anchors on the same refresh `Get` the fix would move; T59 records the same
  dependency in its Work list).
  Filed: the `persistStatus` 409 finding as
  [T78](078-the-status-write-conflicts-with-the-operators-own-earlier-write.md) (severity low,
  security none, effort S, state analysed); its Adjacent findings bullet is now a pointer to T78,
  and its Work list item is ticked as filed. Filed: T78's attribution of the 4 bare CI 409s to
  `persistStatus` and of the wds18 409s (three, two of them `setRollingUpdateState` metadata
  writes) was carried into Mechanism, Not verified and the T35 bullet as marked corrections, and
  D3's mechanism no longer names those 409s as a possible `writePhase` source; W1's residual
  in-memory clobber at `:2617` stays with D3, because T78 takes only the `persistStatus` half, and
  D3 notes T78's R1 as a variant to weigh. The earlier "whichever lands second re-anchors the
  capture" note left with the moved bullet; T78 records that it holds only for a fix that moves
  or repeats the refresh `Get`. Frontmatter unchanged: no severity, urgency or effort reason and
  no recommendation of this ticket rested on the moved finding.
  Sweep: The two citations of T35 by line (`:47` twice, `:449-451`) pointed at lines that moved;
  they now name T35's section "Context: the upgrade itself went as intended" (bullet Rolls), and the
  Adjacent-findings bullet records that T35 no longer cites this file's row A2 but states the
  cached-client claim itself in its Decision 5 mechanism (`steady_state_master.go:195-200`).
  Frontmatter, options and work list unchanged.
- 2026-09-27: adversarial review of the enrichment - spot-checked the new cites (the error
  strings, `writePhase`, `writeStatusCondition`, `requirePhaseError`, `suite_test.go:129-130`,
  `Makefile:84`, the three A1 sites, the A3/C4 sites and their imports, `go.mod`, the 57
  `Eventually` lines): all hold. Corrected the intro note, which called the controller diff since
  `a8e8931` empty although `4a7543e` changed `volumeclaim_conflict.go`. The two XS no-decision
  items and the D1-D4 recommendations confirmed.
- 2026-09-27: enriched - re-read at `4a7543e` (two moved cites corrected: `volumeclaim_conflict.go:183`,
  ADR 0017 `:1111-1115`; the `:1152` length note), Options ordered into four decisions D1–D4 with
  one recommendation each, a Work list splitting two XS no-decision items (A1; A3 and C4) from the
  decision-bound work, and the `make lint` scope precised. State, severity, urgency (`next`, rule
  3), effort `M` and `blocked-by` unchanged.
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
