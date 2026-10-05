---
id: T18
title: "what the CR status reports and how it is written: `Ready` during a roll, `readyReplicas` compared against itself, and the status write on a cached base"
state: analysed
severity: low         # no data-plane effect; status fields go stale or land one pass late, nothing is lost
security: none
urgency: now          # rule 1: ADR 0001, ADR 0002 and the condition registry state what the code contradicts (Ready and readyReplicas parts)
effort: M             # independent corrections S; Q1 A + Q2 A-prime + Q3 R1 together M; Q1 B adds L plus e2e
blocked-by: human     # Q1 re-decides ADR 0001 D4; Q2 and Q3 are ordinary decisions; the independent items are not blocked
filed-from: T6d analysis (option P6), 2026-08-25
opened: 2026-08-25
decided:
done:
---

# T18 - what the CR status reports and how it is written: `Ready` during a roll, `readyReplicas` compared against itself, and the status write on a cached base

**Scope.** Every pass that reaches `updateStatus` recomputes the CR status on a refreshed copy of
the CR and hands it to one write, `persistStatus`, which writes only on a difference. Three
defects sit in that path: which passes reach it, what the difference is computed against, and
which resourceVersion the write carries. All three amend ADR 0002 and the "The status write"
section of [reconcile-loop.md](../developer/reconcile-loop.md); two change the same lines
directly after the refresh.

- **`Ready` during a roll** - a pass that ends on a rolling-update exit never reaches
  `updateStatus`, so `Ready` and several other fields keep the last computed value.
- **`readyReplicas` compared against itself** - the count is assigned before the `prevStatus`
  capture, so it reaches the CR only when another status field changes with it.
- **The status write on a cached base** - the refresh reads the cache, so a CR write the cache
  has not delivered fails the pass with a 409.

## Current state

**The shared path.** `updateStatus`
([`valkey_controller.go:2181-2223`](../../internal/controller/valkey_controller.go#L2181-L2223))
reads the data StatefulSet (missing or foreign: `updatePhase` -> `writePhase`, `:2189-2201`),
refreshes the CR with `r.Get` into the caller's `v`
([`:2203-2206`](../../internal/controller/valkey_controller.go#L2203-L2206), comment "Refresh
the Valkey object to avoid conflicts."), assigns the count (`:2213-2214`), and hands over to
`updateStandaloneStatus` ([`:2226-2287`](../../internal/controller/valkey_controller.go#L2226-L2287))
or `updateHAStatus` ([`:2427-2529`](../../internal/controller/valkey_controller.go#L2427-L2529)).
Both capture `prevStatus` (`:2228`, `:2451`) and end in `persistStatus`
([`:2548-2571`](../../internal/controller/valkey_controller.go#L2548-L2571)), which restores the
previous phase and message on a blocked pass (`:2549-2552`), adds `observerReady` and
`operatorVersion` (`:2554-2564`; only writer of `operatorVersion`), skips the write when
`statusUnchanged` ([`:2583`](../../internal/controller/valkey_controller.go#L2583)) finds no
difference (`:2566-2568`), and otherwise calls `r.Status().Update(ctx, v)`
([`:2570`](../../internal/controller/valkey_controller.go#L2570)) with no retry. The CR status
writers are `persistStatus`, `writePhase` (`:2628`) and `writeStatusCondition` (`:2709`); only the
last retries (`:2679-2691`). The CR watch is generation-gated
([`:2987`](../../internal/controller/valkey_controller.go#L2987)), so a status-only edit starts no
pass.

### `Ready` during a roll

`reconcileWorkload` reaches `updateStatus` only at
[`:369`](../../internal/controller/valkey_controller.go#L369). Returning before it: the data roll
`Error` and `NeedsRequeue` ([`:336-342`](../../internal/controller/valkey_controller.go#L336-L342)),
and the terminal returns of `handlePostRollingUpdateChecks` (propagated at `:363-366`): Sentinel
roll error and requeue ([`:473-484`](../../internal/controller/valkey_controller.go#L473-L484)),
no-master recovery ([`:422-428`](../../internal/controller/valkey_controller.go#L422-L428)) and,
outside any roll, the steady-state split-brain check (`:440-443`). An ordinary roll pass of either
tier writes `Rolling Update i/n` and ends on one of these exits, so `Ready` (normally
`True/HAClusterReady`) keeps the value of the last pass that reached `updateStatus`. ADR 0001 D4
([`0001:100-126`](../adr/0001-continue-reconciling-past-a-rejected-write.md)) decides that the
roll exits own their returns and leaves open which reading of `Ready` is intended.

Two kinds of pass do reach `updateStatus` mid-roll: a wait past its bound (`terminationWait`,
`recreationWait`, `availabilityWait` return `DeferredRequeueAfter`,
[`rolling_update.go:2075`](../../internal/controller/rolling_update.go#L2075), `:2186`, `:2268`,
on which `reconcileWorkload` does not return, `valkey_controller.go:355`, `:485`; ADR 0026 D5,
D11; ADR 0010 D16, D17), and the pass in which the data roll pauses (`pauseRollingUpdate` returns
an empty result, [`rolling_update.go:2646`](../../internal/controller/rolling_update.go#L2646))
unless a post-update check ends it.

**Frozen on a pass that ends on a roll exit:** `Ready`, `readyReplicas`, `masterPod`,
`observerReady`, `operatorVersion`, the `SentinelPeersStale` level (`recordSentinelPeerDrift` runs
only in the all-Ready arm of `updateHAStatus`, `:2458-2462`), and the `Ready` condition's
`ObservedGeneration`, which feeds `vko_valkey_status_observed_generation`
([`collector.go:201-223`](../../internal/metrics/collector.go#L201-L223)). ADR 0001's
clarification (`0001:109-121`) names only the first four.

**Readers of `Ready`.** Nothing in this repository gates on it. Flux kstatus `status.Compute`
(fluxcd/cli-utils v1.3.0) finds no top-level `status.observedGeneration` and no
`Reconciling`/`Stalled` condition on this CRD, so it falls back to `checkReadyCondition`:
`Ready=True` is `Current`, anything else `InProgress`; a per-condition `observedGeneration` is not
read. kustomize-controller with `spec.wait` health-checks every applied object up to
`spec.timeout`, then marks the Kustomization `Ready=False/HealthCheckFailed`; a dependent is
`DependencyNotReady` meanwhile. In production, `oauth2-proxy-databases` (`k8s-flux-base`,
`apps/iam/oauth2-proxy/ks.yml`: `wait: true`, `interval: 1m`, `timeout: 5m`) applies Valkey
`oauth2-valkey` (3 replicas, 3 Sentinels, TLS), and `oauth2-proxy` depends on it. The other Valkey
Kustomizations set neither `wait` nor `healthChecks`.

**Impact:**

- `oauth2-proxy-databases` reads `Current` on a healthy roll. Once a stall or pause pass writes
  `Ready=False` it reads `InProgress` and fails after 5 min; if the roll resumes, the `False`
  stays frozen until the completing pass. For a Valkey spec change `wait: true` gives no rollout
  signal.
- Two default-off alerts can fire on a long healthy roll (traced by reading):
  `ValkeySpecNotObserved`
  ([`prometheusrule.yaml:32-50`](../../deploy/helm/valkey-operator/templates/prometheusrule.yaml#L32-L50),
  `for: 30m`) on a multi-replica non-TLS CR whose spec edit starts a roll in the same pass (TLS
  CRs restamp a condition through `reportTLSMaterialStale`); `ValkeyOperatorVersionStale`
  (`:104-122`, `for: 1h`) on an upgrade roll longer than an hour without a pause or stall pass.
  [`monitoring.md:57-61`](../operations/monitoring.md) says a generation gap means a spec change
  "never converged"; [`README.md:497`](../../README.md) says `operatorVersion` is the version that
  last reconciled the resource.

**Tracked statements the code contradicts:** ADR 0001 D4's rule sentence (`0001:104-107`) "a pass
with a rolling update in flight - blocked or not - returns before `updateStatus`" and the Status
line `0001:19` "D4 itself is unchanged"; ADR 0002 Residual risks
([`0002:540-544`](../adr/0002-surface-a-blocked-reconcile-on-the-cr.md)) "because a pass with a
roll in flight returns before `updateStatus`" and "come apart for the duration of a roll"; the
`Ready` registry row, whose `clearSite`
([`condition_registry.go:99`](../../internal/controller/condition_registry.go#L99)) says the arms
recompute it every pass while its `declaredGap` (`:102`) says "for the whole roll"; ADR 0002 D9
(`0002:218-220`) and the `setStatusCondition` doc comment (`valkey_controller.go:2640-2643`), which
say kstatus reads a condition without `ObservedGeneration` as generation 0 - kstatus reads only
the top-level `status.observedGeneration`, and D9's rule stands because
`vko_valkey_status_observed_generation` depends on the stamp.

**Registry gap.** The `Ready` row's `declaredGap` cites T18 (`condition_registry.go:102`, package
comment `:16-18`). `TestConditionRegistryGapsAreTraceable`
([`condition_registry_test.go:200-211`](../../internal/controller/condition_registry_test.go#L200-L211))
demands `T\d+` in every gap while ADR 0034 D7 forbids new ticket citations outside
`docs/tickets/`, so the string lands with Q1 (either reading deletes the gap) or with T40 decision
2 option A (the regexp becomes `ADR \d{4}`). T18 is cited outside `docs/tickets/` at
`condition_registry.go:17`, `:102`, `CLAUDE.md:568` and ADR 0027 `:201`, `:252`, `:332`. With the
gap removed, `TestConditionRegistryLevelsHaveOneEvaluator` (`condition_registry_test.go:149`)
passes on `evaluators: 1` (`condition_registry.go:98`).

### `readyReplicas` compared against itself

The count is assigned at
[`:2213-2214`](../../internal/controller/valkey_controller.go#L2213-L2214), before both
`prevStatus` captures, so `statusUnchanged` compares it with itself and, if nothing else differs,
the write is skipped. ADR 0002 D5 moved `observerReady` behind the capture after the same defect
left it wrong on a live fleet, and left `readyReplicas` as an accepted residual risk
(`0002:491-529`). The only guard of the window between the refresh and the captures is the NOTE
comment at `:2208-2211`.

**The masking.** The `Ready` condition, written by the same `persistStatus` call, fixes the count
in every branch: standalone (`:2234-2284`) and HA (`:2457-2526`) all-ready reasons imply count =
`spec.replicas`, partly-ready messages name it (`:2271`, `:2512-2513`), none-ready implies 0, and
`spec.replicas` has `Minimum=1` ([`valkey_types.go:480`](../../api/v1/valkey_types.go#L480)), so
the branches are disjoint. Inside an all-ready branch the count changes only with `spec.replicas`,
which bumps the generation that `meta.SetStatusCondition` copies onto the condition; on a blocked
pass `Ready` alone still suffices. `writePhase` and `writeStatusCondition` send the stored `Ready`
and count back unchanged, and no other code sets `ConditionTypeReady`. So while only the operator
writes status, the count cannot go stale.

**Where it breaks.** A count written by anyone else is overwritten in memory at `:2214` before the
capture, compares equal, and stands until another compared field changes. The masking also breaks
if a `Ready` reason is shared by two branches with different counts, a partly-ready message stops
naming the count, or a new branch's `Ready` does not fix it. No test pins this:
`TestUpdateStatus_KeepsNonPhaseFieldsWhileBlocked` (`status_phase_test.go:205`) and
`TestReconcile_BlockedPassDoesNotFlapPhase` (`:84`) move the count together with `Ready`,
`TestUpdateHAStatus_KeepsReadyTrueWhileBlocked` (`:243`) asserts no count, and
`TestStatusUnchanged_DetectsChanges` (`valkey_controller_test.go:854`) tests the helper, not the
order.

**Impact.** The stored count feeds the `Ready` printer column
([`valkey_types.go:1133`](../../api/v1/valkey_types.go#L1133)), the gauge
`vko_valkey_status_ready_replicas` (`collector.go:195-196`) and `ValkeyReplicasMissing`
(`spec - ready > 0` for 15m, `prometheusrule.yaml:87-94`): stuck too high it keeps the alert
silent on a cluster short of pods, stuck too low it fires on a complete one. No operator decision
reads it. ADR 0026 (`0026:628-633`) promises the alert sees a data-tier stall; in that pass the
count reaches the CR only through the masking.

**ADR 0002 states two false sentences.** `0002:522-527` says both blocked-pass tests assert the
count; there are three, and the HA one asserts none (the conclusion, no test isolates the count,
holds). `:519-521` implies a phase-message change can reopen the defect; it cannot.

### The status write on a cached base

`r.Get` is the cache-backed manager client ([`main.go:115`](../../cmd/main.go#L115); no type is
excluded from the cache), so the refresh replaces `v`, resourceVersion included, with the cached
copy, and `:2570` writes with it. Nothing writes the CR between the refresh and the write. The
trigger is any CR write the cache has not delivered when the refresh runs:

1. **A write earlier in the same pass.** Every operator write decodes the stored object into `v`;
   the refresh replaces it with the older cached copy. Writers before the refresh: the empty-phase
   `Provisioning` write (`:256-260`), `setReconcileBlockedCondition`
   ([`reconcile_blocked.go:118-148`](../../internal/controller/reconcile_blocked.go#L118-L148)),
   every `setStatusCondition` and `writeStatusCondition` caller (among them the
   `SentinelUpdatePending` clear at the end of a Sentinel roll,
   [`rolling_update.go:5212-5224`](../../internal/controller/rolling_update.go#L5212-L5224)), and
   the rolling-update annotation writes on `v`. Several log nothing on success.
2. **A write of the previous pass.** The pass starts from a cached copy (`:225-226`); a pass that
   starts before the informer delivered the previous status write refreshes into the same stale
   copy.

**On the 409** the error reaches `Reconcile` (`:300-301`); controller-runtime re-queues
rate-limited (5 ms doubling, capped at 30 s,
[`ratelimiter.go`](../../internal/controller/ratelimiter.go#L16)), increments
`controller_runtime_reconcile_errors_total` and logs `ERROR Reconciler error`.

**The 409 is protective.** A status `Update` replaces the whole status, so a write from a copy that
predates a condition write reverts it: `RetryOnConflict`, or a fresh read that re-sends the status
computed from the stale copy, reverts what the 409 protected; `status.conditions` has no
`+listType=map` marker ([`valkey_types.go:1124-1126`](../../api/v1/valkey_types.go#L1124-L1126)),
so a merge patch or server-side apply replaces the whole list too; an `Update` without a
resourceVersion is refused for a custom resource. A fix must give the write a current base, or
re-apply the computed fields onto one.

**An uncached reader exists.** `APIReader`
([`valkey_controller.go:84-92`](../../internal/controller/valkey_controller.go#L84-L92), wired at
`main.go:116`) is documented for the delete gate's live look (ADR 0026 D5, `liveTerminatingPod`,
[`rolling_update.go:2111-2141`](../../internal/controller/rolling_update.go#L2111-L2141)) and is
nil in unit tests that do not wire it. A GET through it is a consistent read, one API request with
no client-side throttle (`QPS = -1`), and needs no RBAC change (the chart grants `get` on
`valkeys`).

**Observed.** One green CI run of `Integration Tests (envtest)`: four bare CR 409s, all attributed
to `:2570` by elimination. The wds18 fleet upgrade log: one of three 409s is `:2570`, the pass that
landed the `SentinelUpdatePending=False` clear (form 1), one of eight Sentinel roll completions;
the other two came from `setRollingUpdateState`'s metadata `Update` (`rolling_update.go:3418`),
which this part does not change.

**Impact.** Each occurrence is one `ERROR Reconciler error` line, one error-counter increment
(indistinguishable from a real write failure, the signal ADR 0001 D5 calls alertable; no shipped
alert reads it) and one extra full pass with a health check against every pod. The status lands
one pass late; nothing is lost. A blocked pass returns the 409 joined with its resource error; the
user-visible messages use the resource error alone. After the refresh `v` holds the stale copy;
only `valkey.Status.Phase` is read from it today.

**Two misleading records.** The refresh comment at `:2203` holds only when the cache is newer than
`v`; in form 1 the cached read causes the conflict. ADR 0002 D7's title "A failed status write
never ends the pass" is wider than its body, which scopes the rule to the empty-phase write and
`setStatusCondition`; `persistStatus` returns its error.

## Required changes

### Shared, in one change

- **One ADR 0002 amendment** carries every part's edit (D5, D7, D9, the residual risks
  `0002:491-545`, and what Q1-Q3 decide), superseded rules marked in place, Status amended, the
  index row updated if the State moves.
- **One rewrite of [reconcile-loop.md](../developer/reconcile-loop.md), "The status write"**
  (`:149-150`, `:158-164`): the refresh and its reader, where `prevStatus` is captured, the
  resourceVersion the write carries, that a conflict fails the pass, and why the 409 must not be
  retried on the same object. The last three hold independent of Q3.
- **The refresh block** (`valkey_controller.go:2203-2214`, the NOTE and the refresh comment) is
  rewritten once for Q2 and Q3; their changes compose in either order.
- **Tests** go into `internal/controller/status_phase_test.go`, next to the interceptor harness
  ([`:35`](../../internal/controller/status_phase_test.go#L35)); code comments and tests cite ADR
  0001 or ADR 0002, never this ticket.
- **Proof:** `make test-unit`, `make lint`, `make cyclo` green; `git grep -n 'readyReplicas' --
  ':!docs/tickets'` finds no unmarked statement of the masking.

### Independent of the open questions

1. ADR 0001: correct the D4 rule sentence (`0001:104-107`) in place: a pass that ends on a
   rolling-update exit returns before `updateStatus`; a pass past a wait bound (ADR 0026 D5, D11;
   ADR 0010 D16, D17) and the pause pass continue to it. Correct the Status line `0001:19`. No rule
   changes. Same change: ADR 0002 `:540-544`.
2. `condition_registry.go:99`: `clearSite` says the arms recompute `Ready` on every pass that
   reaches `updateStatus`.
3. ADR 0002 D9 (`0002:218-220`) and the `setStatusCondition` doc comment (`:2640-2643`): kstatus
   reads only a top-level `status.observedGeneration`; the stamp stays for
   `vko_valkey_status_observed_generation`. Check: `git grep -n "generation 0" -- ':!docs/tickets'`
   finds no current statement.
4. ADR 0002 `:519-521` and `:522-527` superseded in place and restated: three blocked-pass tests
   and what each asserts; only the `Ready` condition's encoding carries the count.

### `Ready` during a roll (Q1)

**Under A:** ADR 0001's "deliberately left open" sentence (`0001:123-126`) replaced by the decided
reading, the frozen-value list (`:110-111`) completed with `operatorVersion`, `SentinelPeersStale`
and the `Ready` `ObservedGeneration`, the kstatus consequence named; ADR 0002 `0002:535-545` marked
decided; ADR 0027 `:201`, `:252`, `:332` left with no declared gap; the `Ready` `declaredGap`
(`condition_registry.go:102`) and the package-comment sentence (`:16-18`) removed;
[`status.md:17`](../operations/status.md#ready) says what a Flux Kustomization with `wait: true` or
`healthChecks` sees during a roll; `monitoring.md:57-61` and ADR 0021 Residual risks
(`0021:200-218`) say a spec-triggered roll of a non-TLS CR can keep the generation gap open and a
long upgrade roll keeps `operatorVersion` behind; `README.md:497` says `operatorVersion` is written
with the status, not on every reconcile; `CLAUDE.md:568` (needs Hans; coordinate with T40 work item
2). Proof: `git grep -n -w T18 -- ':!docs/tickets'` is empty.

**Under B:**

- Every roll exit of `reconcileWorkload` and of the Sentinel roll reaches `persistStatus`, with a
  per-pass context marker (the `passIsBlocked` pattern) that keeps the roll's phase and message.
- `updateStandaloneStatus` and `updateHAStatus` gain a roll-in-flight branch writing `Ready=False`
  with a rolling-update reason, placed before the health check in `updateHAStatus` (spares roll
  passes the `CheckCluster` probe, `:2457-2460`); without it `persistStatus` publishes a per-pass
  verdict that flips across replacements. If Q2 = C, the branch's `Ready` must fix the count or it
  breaks the masking.
- ADR 0001 D4 second half superseded in place; ADR 0002, the ADR 0027 row, the registry gap and the
  docs amended; the release notes name the change (ADR 0005 D10: upgrade neutrality covers
  `status`).
- Proof: an e2e roll on both topologies observes `Ready=False` with the reason, `True` after
  completion, and the phase `Rolling Update i/n` throughout.

### `readyReplicas` compared against itself (Q2)

- **Under A-prime:** `prevStatus := v.Status.DeepCopy()` once in `updateStatus` directly after the
  refresh, passed to both callees in place of their captures at `:2228` and `:2451` (two
  signatures, call sites `:2218`, `:2222`); `:2214` stays. The NOTE, the `persistStatus` doc
  comment rationale (`:2539-2547`) and `CLAUDE.md:579` (from "next to `OperatorVersion`" to "after
  the capture") rewritten. Check: exactly one `prevStatus := v.Status.DeepCopy` in
  `valkey_controller.go`, directly after the refresh.
- **Under A:** delete `:2214`, set the count directly after the captures at `:2228` and `:2451`;
  the NOTE updated; the `persistStatus` rationale and `CLAUDE.md:579` stay true.
- **Under A-prime or A:** ADR 0002 `:43-49`, `:140-147`, `:156` and `:491-529` superseded, naming
  which move was made. Regression test: seed a stored status whose `readyReplicas` disagrees with
  the StatefulSet while every other field is converged (`reconcileFor`, then `c.Status().Update`
  with only the count changed, pattern at
  [`status_phase_test.go:213-216`](../../internal/controller/status_phase_test.go#L213-L216)), run
  `updateStatus` on a blocked and an unblocked context, assert the write both times, one case per
  topology; it fails on today's order and passes after the fix (ADR 0017 revert check).
- **Under C:** ADR 0002 keeps the residual risk; its Not-verified sentence (`0002:521-522`) is
  replaced by the encoding argument and the refusal recorded.

### The status write on a cached base (Q3)

**Under R1:**

- `updateStatus` reads the refresh through `r.APIReader.Get`, falling back to `r.Get` when the
  reader is nil (the `liveTerminatingPod` precedent, `rolling_update.go:2118`); the refresh comment
  says what the read is for; the `APIReader` doc comment names this second class of read.
- ADR 0002: the rule that the status write's base is read from the API server, why, and D7's scope.
- Unit test: `Client` wraps the fake with an `interceptor.Funcs.Get` returning a pre-write snapshot
  of the CR; `APIReader` is the plain fake. Case 1 (same pass): a `ReconcileBlocked` clear is
  written, then `updateStatus` runs with a status change pending; no error, the write lands,
  `ReconcileBlocked` is still `False`. Case 2 (previous pass): the snapshot predates an earlier
  `persistStatus` write; no error. A nil `APIReader` keeps today's behaviour.
- Mutations (ADR 0017): (a) refresh back to `r.Get` - both cases fail with a conflict; (b) keep
  `r.Get` and, on a conflict at `:2570`, copy the resourceVersion of an `APIReader` read onto `v`
  and write again - case 1 fails on a reverted `ReconcileBlocked`. Revert both, tests pass.

**Under R2:** the owned-field retry in `persistStatus`, two unit tests, one mutation, the ADR 0002
amendment. **Under R3:** an ADR 0002 residual-risk entry recording the self-inflicted refusal and
D7's scope; the refresh comment corrected.

## Open questions

### Q1: Does `Ready` mean "the last computed data-plane verdict" or "the cluster is serving now, and not mid-roll"? (`Ready` during a roll)

Today `Ready` keeps the value of the last pass that reached `updateStatus`, so a healthy roll reads
`True` and Flux's `wait: true` on `oauth2-proxy-databases` reads `Current`. The stall surfacing of
ADR 0026 D5, D11 and ADR 0010 D16, D17 already recomputes `Ready` on a stuck roll either way.

- **A - decide the current behaviour (recommended).** Text and one registry row, no behaviour
  change, no e2e; cost S. `wait: true` stays vacuous for a Valkey spec change; the frozen fields and
  the two alert effects stay.
- **B - `Ready=False` with a rolling-update reason while either tier rolls.** Cost L plus e2e. The
  frozen fields become current and `wait: true` gains a rollout signal. `oauth2-proxy-databases`
  turns `InProgress` for every roll of `oauth2-valkey`, certificate-rotation and operator-upgrade
  rolls included, holds `oauth2-proxy`'s applies for the whole roll, and a roll longer than 5 min
  fails it with `HealthCheckFailed`. Every roll pass then reaches `persistStatus`, which raises the
  rate of the Q3 conflicts and, under R1, adds one GET per roll pass.

A changes nothing for the one production reader and breaks no standing constraint; the
failover-aware roll keeps `oauth2-valkey` serving and oauth2-proxy does not need the roll to finish.
If a GitOps consumer ever needs to gate on convergence, a top-level `status.observedGeneration` is
the carrier that does not redefine `Ready`.

**Answer:** _open_

### Q2: Take the `readyReplicas` fix, and in which shape, or refuse it? (`readyReplicas` compared against itself)

The count is correct today only because the `Ready` condition encodes it, which nothing tests, and
a third-party count is never corrected. Both fixes write exactly when today's operator writes for
every status only the operator wrote: no extra write, no roll, nothing on upgrade.

- **A-prime - move the capture (recommended).** One capture after the refresh, passed to both
  callees. Cost S.
- **A - move the assignment.** The smallest diff, cost S; leaves the window between the refresh and
  the captures guarded only by the NOTE comment, which was already missed once.
- **C - refuse the fix.** Cost XS. The count keeps resting on untested `Ready` strings, and a
  third-party count stays wrong.

A-prime, because the defect happened twice in the same place, a prologue assignment between the
refresh and a capture in another function; A-prime removes that window so a third field cannot
repeat it, while A removes one field and keeps a comment as the only guard.

**Answer:** _open_

### Q3: How does the status write get a current base? (the status write on a cached base)

The status is computed on a cached copy and written with its resourceVersion, so the operator's own
recent writes make it fail with 409. The fix cannot simply retry, because the 409 keeps a condition
written moments earlier from being reverted. A genuine third-party conflict stays a refusal under
R1 and R3.

- **R1 - read the refresh through the `APIReader` (recommended).** The computation starts from the
  stored object, so neither form of the trigger can conflict. Cost S; one uncached GET per pass that
  reaches `updateStatus`. `Ready`'s `ObservedGeneration` may name a generation this pass's resource
  steps did not apply for one pass, as the cached read already can.
- **R2 - on conflict, re-read and re-apply the fields the computation owns.** No extra read on the
  common path; a genuine conflict resolves in the same pass. Cost M; the owned-field list is a
  second registry every status field must join (T35 adds `RWServiceMisrouted`), a missed field is
  silently dropped, and the retry reads the same lagging cache.
- **R3 - leave it.** No code; every self-inflicted conflict keeps costing an ERROR line, a counter
  increment and a full extra pass.

R1 removes the cause instead of retrying past it, needs no field list and cannot miss a field; a
pass reaching `updateStatus` already dials every pod, so one GET is small. No ordering alternative
exists, because form 2 is a write of the previous pass.

**Answer:** _open_

## Not verified

- The mid-roll recompute cases and the two alert effects are traced by reading, not measured.
- The live wds18 cluster and its kustomize-controller version were not read; the Flux facts come
  from local clones and upstream source. Argo CD was not checked for a per-condition
  `observedGeneration` reader.
- How long a roll of `oauth2-valkey` takes; decides whether Q1 B fails `oauth2-proxy-databases`
  (5 min). A timed roll of a 3+3 Sentinel TLS cluster on Kind settles it.
- Whether `ValkeySpecNotObserved` fires on a healthy roll depends on whether the informer delivered
  the template write in the pass that applies the CR edit; an e2e reading
  `vko_valkey_status_observed_generation` during the replica phase of a non-TLS multi-replica roll
  settles it.
- Whether any tool in the production fleet (for example a backup restore) writes the
  `valkeys/status` subresource; an audit of its writers on the fleet API servers settles it.
- That the Q2 regression test fails today and passes after the fix rests on reading.
- The 409 rate on a live fleet over a steady-state period (one fleet log and one CI run were read),
  and how fast the informer delivers a same-pass write (matters only for R2).

## Related

- [T40](040-tracked-text-cites-tickets-and-states-what-the-code-contradicts.md) - decision 2 and work item 2 touch
  the same registry line, ADR 0027 D4 and `CLAUDE.md:568`.
- T23 - its pause change makes the pause pass a holding pass; only ADR 0001 `:118`'s reason
  ("because `pauseRollingUpdate` returns an empty result") becomes false, and T23 rewrites it.
- [T12](archive/012-the-master-handover-loses-no-acknowledged-write-and-no-dataset.md) (done) - `AllSynced` in `updateHAStatus` counts replicas by their own answer (ADR 0037 D2), so `True/HAClusterReady` no longer holds while a replica full-syncs; composes with Q1 B's roll-in-flight branch.
- [T35](035-who-the-master-is-after-a-restart-or-failover.md) - its decided B2 writes the `known-master`
  annotation from `v` after `persistStatus`; it inherits the stale base today and gets a current
  one under R1.
- [T34](034-test-fixtures-pass-on-evidence-that-does-not-prove-the-assertion.md) - the same cached-base mechanism in
  `writePhase` (its D3), where a retry is the right fix; R1's reader is an option there too.
