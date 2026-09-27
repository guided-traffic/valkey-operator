---
id: T18
title: "`Ready` keeps its pre-roll value for the whole rolling update — decided in ADR 0001 D4, re-decision request"
state: analysed       # "open"; deferred 2026-08-26 as a re-decision
severity: low
security: none
urgency: now          # rule 1 since 2026-09-27: the 'whole roll' sentences were false by code reading (item 1, landed 2026-09-27); the registry string at condition_registry.go:102 still is (item 2); back to icebox (rule 5) once it is precise (History)
effort: S             # reading 1 (recommended); reading 2 is L plus e2e. Was S–L until 2026-09-27
blocked-by: human
filed-from: T6d analysis (option P6), 2026-08-25
opened: 2026-08-25
decided:
done:
---

# T18 - `Ready` keeps its pre-roll value for the whole rolling update — decided in ADR 0001 D4, re-decision request

**Severity: low, and it is not an uncovered defect. Status: open, found 2026-08-25
while analysing T6d (option P6). Corrected before filing: the first draft called this
an uncovered gap; ADR 0001 D4 decides it explicitly and is quoted below.**

~~Two~~ **Five** `reconcileWorkload` exits return before `updateStatus`:

- ~~[`valkey_controller.go:320-322`](../../internal/controller/valkey_controller.go#L320-L322)~~ *(corrected 2026-09-27: [`:336-339`](../../internal/controller/valkey_controller.go#L336-L339))* — `rollingResult.Error`
- ~~[`valkey_controller.go:324-326`](../../internal/controller/valkey_controller.go#L324-L326)~~ *(corrected 2026-09-27: [`:340-342`](../../internal/controller/valkey_controller.go#L340-L342))* — `rollingResult.NeedsRequeue`

> **Corrected 2026-08-26 — this item undercounts, and the correction makes reading 2
> bigger, not smaller.** Three further exits skip `updateStatus`: the terminal
> (`done == true`) returns of `handlePostRollingUpdateChecks`, propagated at
> ~~[`:343-345`](../../internal/controller/valkey_controller.go#L343-L345)~~ — the Sentinel roll
> error (~~[`:396`](../../internal/controller/valkey_controller.go#L396)~~), the Sentinel roll
> requeue (~~[`:399`](../../internal/controller/valkey_controller.go#L399)~~) and the no-master
> recovery (~~[`:417`](../../internal/controller/valkey_controller.go#L417)~~,
> ~~[`:419`](../../internal/controller/valkey_controller.go#L419)~~).
> *(corrected 2026-09-27, at `4a7543e`: propagated at [`:363-366`](../../internal/controller/valkey_controller.go#L363-L366); the Sentinel
> roll error [`:473-481`](../../internal/controller/valkey_controller.go#L473-L481), the Sentinel roll requeue [`:482-484`](../../internal/controller/valkey_controller.go#L482-L484),
> the no-master recovery [`:422-428`](../../internal/controller/valkey_controller.go#L422-L428). A sixth terminal return, outside any roll,
> is the steady-state split-brain check's at [`:440-443`](../../internal/controller/valkey_controller.go#L440-L443).)*
>
> The Decision below says those exits "were already changed for exactly this reason". That
> is true only of the **non-terminal** (`done == false`) result, which is now carried to the
> end of the pass. The terminal ones still return early. **Consequence: a full Sentinel-tier
> roll also freezes `Ready`** — so reading 2 is a larger change than this item already warns,
> and `updateStatus` is only reached at
> ~~[`:349`](../../internal/controller/valkey_controller.go#L349)~~ *(corrected 2026-09-27: [`:369`](../../internal/controller/valkey_controller.go#L369))*.

`NeedsRequeue` is set on essentially every pass of an active roll, so `updateStatus`
never runs *(corrected 2026-09-27: runs on two kinds of pass, see "Fact, re-verified" below)* and the `Ready` condition keeps whatever it held before the roll started —
normally `True / HAClusterReady` — while `updatePhase` writes `Rolling Update i/n`. So a
cluster reports Ready while its pods are being deleted one by one. `masterPod` and
`observerReady` freeze for the same reason and the same duration.

**This is decided behaviour, not an oversight.** ADR 0001 D4
(~~`0001:84-91`~~ *(corrected 2026-09-27: [`0001:88-107`](../adr/0001-continue-reconciling-past-a-rejected-write.md), D4 and its clarification)*) closes with:
*"The rolling-update exits of `reconcileWorkload` still own their own returns: a pass
with a rolling update in flight — blocked or not — returns before `updateStatus` and
writes its phase itself."* Changing it means amending ADR 0001 D4 in place with the
superseded sentence marked, per the CLAUDE.md ADR rules — not writing a new decision.

The *same argument* ADR 0026 D5 made for the `DeferredRequeueAfter` case — *"the pass
must not end on it, or everything below stays suspended … and the status write"* — is
what would be extended to the remaining early exit. The two later exits
(`handlePostRollingUpdateChecks`, the deferred recheck) were already changed for exactly
this reason and carry comments saying so, so the precedent for extending it exists.

**Two readings, and the decision is which one is intended:**

1. `Ready` means *the last steady state was healthy* — then this is correct and the
   only work is a doc sentence (and it merges into T6d P1).
2. `Ready` means *the cluster is serving now* — then a roll must write
   `Ready=False` with a rolling-update reason, which is a behaviour change for any
   consumer treating `Ready` as a gate. Verified: nothing in this repo, and nothing in
   the wds18 Flux setup, currently gates on it (see T6d).

**Proposed fix (not decided):** whichever reading is chosen, state it in the
`ConditionTypeReady` doc comment T6d P1 introduces, so the two are decided together
rather than twice. Note the scope if reading 2 wins: the exits that skip `Ready` skip
`masterPod`, `readyReplicas` and `observerReady` too, so "recompute `Ready` somewhere
every exit reaches" is really "reach `persistStatus` on every exit" — a larger change
than one write site, and one that has to respect ADR 0002 D8 (steady state costs no
status write), whose `ReadyReplicas`/`ObserverReady` guards are dead until T6a A1
lands.

> **Half of that last clause is stale, corrected 2026-08-26.** T6a A1 **landed** in
> `75b3c92`. `ObserverReady` is now assigned inside `persistStatus` on the far side of the
> capture (~~[`:2386-2393`](../../internal/controller/valkey_controller.go#L2386-L2393)~~ *(corrected 2026-09-27:
> [`:2553-2563`](../../internal/controller/valkey_controller.go#L2553-L2563))*) and
> compared by `statusUnchanged` at
> ~~[`:2422`](../../internal/controller/valkey_controller.go#L2422)~~ *(corrected 2026-09-27: [`:2591`](../../internal/controller/valkey_controller.go#L2591))* — that guard is live.
> **`ReadyReplicas` still carries the defect**, by the fix commit's own admission and
> recorded as an accepted residual in ADR 0002 ~~`:403`~~ *(corrected 2026-09-27: `0002:477-510`)*.
> So this item is the place where that
> remainder would be closed, and reading 2 subsumes it. *(Superseded 2026-09-27: the
> `readyReplicas` remainder has its own ticket,
> [059](059-status-readyreplicas-is-compared-against-itself.md), whose option A closes it
> independently of the reading chosen here; reading 2 no longer needs to carry it.)*
>
> The documentation dependency is also discharged: the `ConditionTypeReady` doc comment this
> item wanted to decide together with T6d P1 exists at
> [`api/v1/valkey_types.go:42-44`](../../api/v1/valkey_types.go#L42-L44). **The coupling is
> therefore one-sided now — the doc exists, the decision does not.** This item is blocked on
> a human picking a reading, nothing else.

## Fact, re-verified 2026-09-27

**Verified** (by reading at `4a7543e`):

- The exits above, at the corrected lines. `updateStatus` is reached only at
  [`valkey_controller.go:369`](../../internal/controller/valkey_controller.go#L369).
- **"For the whole roll" is not precise.** Two kinds of pass reach `updateStatus` while a roll is
  in flight, so `Ready`, `masterPod`, `readyReplicas` and `observerReady` are recomputed there:
  - a pass whose wait has outlived its bound: `terminationWait`, `recreationWait` and
    `availabilityWait` return `DeferredRequeueAfter`
    ([`rolling_update.go:2074`](../../internal/controller/rolling_update.go#L2074), [`:2185`](../../internal/controller/rolling_update.go#L2185), [`:2267`](../../internal/controller/rolling_update.go#L2267)), which
    `reconcileWorkload` does not return on ([`valkey_controller.go:355`](../../internal/controller/valkey_controller.go#L355); the Sentinel
    tier's at [`:485`](../../internal/controller/valkey_controller.go#L485)). ADR 0026 already says so
    ([`0026:629-633`](../adr/0026-a-pod-being-deleted-is-not-available.md): "keep their pre-roll
    values only for the budget rather than for the whole stall");
  - the pass in which the data roll pauses: `pauseRollingUpdate` returns an empty result
    ([`rolling_update.go:2643`](../../internal/controller/rolling_update.go#L2643)), so the pass continues to the Sentinel roll and,
    ~~unless that ends it~~ *(corrected 2026-09-27, review: unless a post-update check ends it -
    the Sentinel roll ([`valkey_controller.go:473-484`](../../internal/controller/valkey_controller.go#L473-L484)), the no-master
    recovery (`:422-428`) or the split-brain check (`:440-443`))*, to `updateStatus`
    ([023](023-pauserollingupdate-records-no-pause.md)).

  So ADR 0001 ([`:7-9`](../adr/0001-continue-reconciling-past-a-rejected-write.md#status),
  [`:97-100`](../adr/0001-continue-reconciling-past-a-rejected-write.md)), ADR 0002
  (`0002:521`), the `ConditionTypeReady` doc comment
  ([`api/v1/valkey_types.go:42-44`](../../api/v1/valkey_types.go#L42-L44)) and
  [`docs/operations/status.md:17`](../operations/status.md#ready) ~~contradict~~ *(contradicted,
  until work list item 1 on 2026-09-27; all five are made precise, History)* ADR 0026 `:629-633`,
  and the code sides with ADR 0026. Work list item 1. The registry string at
  `condition_registry.go:102` still says "for the whole roll" (item 2).
- **Nothing gates on the Valkey `Ready` condition.** The `kubectl wait --for=condition=Ready` calls
  in [`.github/workflows/release.yml`](../../.github/workflows/release.yml) wait on nodes
  (`:168`), a probe pod (`:262`) and cert-manager pods (`:337`); the e2e readers of a `Ready`
  condition read a pod's ([`test/e2e/rolling_update_test.go:717`](../../test/e2e/rolling_update_test.go#L717))
  and a Certificate's ([`test/e2e/tls_test.go:182`](../../test/e2e/tls_test.go#L182)); nothing under
  `deploy/` or `hack/` reads it. The operator itself writes it only in `updateStandaloneStatus`
  and `updateHAStatus` ([`valkey_controller.go:2242-2519`](../../internal/controller/valkey_controller.go#L2242-L2519)).
- **The registry gap and its guard.** The `Ready` row carries the `declaredGap` naming T18
  ([`condition_registry.go:102`](../../internal/controller/condition_registry.go#L102); the
  package comment at [`:17`](../../internal/controller/condition_registry.go#L17)), and
  `TestConditionRegistryGapsAreTraceable`
  ([`condition_registry_test.go:200-210`](../../internal/controller/condition_registry_test.go#L200-L210))
  demands a `T\d+` in every `declaredGap`, which the no-ticket-citation rule of ADR 0034 now
  forbids for new text ([040](040-tracked-files-cite-work-items-instead-of-adrs.md)). Reading 1
  removes the gap and with it this conflict for the row. T18 is cited outside `docs/tickets/` at
  `condition_registry.go:17` and `:102`, `CLAUDE.md:568` and ADR 0027 `:201`, `:252`, `:332`.

**Not verified:**

- Nothing was run; the two mid-roll recompute cases are traced by reading, not measured.
- The wds18 Flux claim of 2026-08-26 (no `healthChecks` on a `Kustomization` managing a Valkey
  CR) lives in another repository and was not re-checked.
- Whether a kstatus-style reader (Flux, Argo CD) would treat this CR's `Ready=True` mid-roll as
  "rolled out". No such consumer is known; it is the one argument for reading 2.

## Options

One decision: what `Ready` means during a roll. It is a re-decision of ADR 0001 D4.

- **Reading 1 — `Ready` is the verdict of the last pass that reached `updateStatus`
  (recommended).** Amend ADR 0001 D4 in place to decided (dated), mark the "deliberately left
  open" sentence ([`0001:103-106`](../adr/0001-continue-reconciling-past-a-rejected-write.md))
  superseded, and name the two mid-roll recompute cases. Update ADR 0002 D5a and Residual risks
  (`0002:521-526`), ADR 0027 `:201`, `:252`, `:332`, remove the `declaredGap` from the `Ready`
  row and the gap sentence at `condition_registry.go:17`, and `CLAUDE.md:568`. *(Added
  2026-09-27, review:)* the row's `clearSite` string ("recompute it every pass",
  `condition_registry.go:99`) is not true either — a pass that ends on a rolling-update exit does
  not recompute it — and is made precise in the same change. Cost S: no
  behaviour change, unit and lint only (`TestConditionRegistryLevelsHaveOneEvaluator` still sees
  one evaluator). Leaves: `Ready=True` beside `Rolling Update i/n` on a healthy roll.
- **Reading 2 — `Ready=False` with a rolling-update reason while a roll is in flight, by reaching
  `persistStatus` on every exit.** Six exits (above) would have to reach the status write while
  the roll keeps its phase authority, because `updateStatus` would otherwise overwrite
  `Rolling Update i/n` with `OK`, `Syncing` or `Provisioning` (the phase writes at `:2238-2274` and
  `:2464-2515`), and every roll pass would cost a
  status write that ADR 0002 D8 has to accept. Cost L plus e2e (a roll asserting the condition
  flip on both topologies). Leaves nothing open on the meaning, and closes the `masterPod` and
  `observerReady` freeze too.
- **Reading 2 by a second writer — the roll writes `Ready=False` next to its own phase.** Cheaper
  than reaching `persistStatus` (M), but `Ready` becomes a level with two evaluators, which ADR
  0027 allows only with an `ownershipRule` in the registry, and the roll's write would be
  suppressed on a blocked pass like its phase write. `masterPod` and `observerReady` stay frozen.

Reading 1 is marked because the stale value that would hurt, a stuck roll, is already recomputed
after its budget (ADR 0026 D5, D11; ADR 0010 D16, D17), because the failover-aware roll keeps a
healthy cluster serving while `Ready=True` is shown, and because nothing reads the condition
today (Verified above). Reading 2 fights the roll's phase authority at every exit for a consumer
that does not exist; if a kstatus-style consumer appears, it is the reading to revisit.

## Work list

1. **XS, no decision needed** *(added 2026-09-27)*: make the "whole roll" sentences precise,
   since they are false for the two recompute cases above: ADR 0001 `:9` and `:99-100` (struck
   and corrected in place, dated), ADR 0002 `:521` (same), the `ConditionTypeReady` doc comment
   `api/v1/valkey_types.go:42-44`, and `docs/operations/status.md:17`. The registry string at
   `condition_registry.go:102` says the same and is left to item 2: rewriting it touches a T18
   citation that `TestConditionRegistryGapsAreTraceable` requires. Does not close this ticket.
   *(Review 2026-09-27:)* the corrections name the pause pass as reaching the status write
   "unless a post-update check ends it", not "unless the Sentinel roll ends it": the no-master
   recovery and the split-brain check can end it too. **Done 2026-09-27**, all five places, in
   the "post-update check" wording.
2. **Waits on the decision**: reading 1 as listed under Options, then `git grep` T18 outside
   `docs/tickets/` and archive; or reading 2.

## Decision

**Deferred 2026-08-26 as a re-decision, and half of it is now documented.** No code changed:
this is ADR 0001 D4 operating as written, so closing it means amending that ADR in place, not
shipping a fix.

What did land is the part that was actually missing — the *price* of D4's second half is now
stated rather than implied. ADR 0001 D4 carries a clarification saying that those exits leave
`Ready`, `masterPod`, `readyReplicas` and `observerReady` at their pre-roll values ~~for the
whole roll~~ *(corrected 2026-09-27, work list item 1: the clarification now says "on every
pass that ends on a rolling-update exit" and names the two kinds of pass that recompute them)*,
and explicitly leaves open which reading of `Ready` is intended.
`vkov1.ConditionTypeReady`'s doc comment and the ~~README row~~ *(corrected 2026-09-27: since
`4a7543e` the text lives in [`docs/operations/status.md:17`](../operations/status.md#ready))* say the same, and
`conditionRegistry` carries it as a declared gap naming T18. So the behaviour is discoverable
from three places instead of zero, which is what made it look like a defect.

The open question is unchanged and is a product decision, not a code one: does `Ready` mean
"the last steady state was healthy" or "the cluster is serving now"? Reading 1 needs nothing
further. Reading 2 is bigger than one write site — it means reaching `persistStatus` on every
exit, while respecting ADR 0002 D8. Also verified while deferring: nothing gates on the
condition today, in this repo or in the wds18 Flux setup (no `Kustomization` managing a
Valkey CR carries `healthChecks`), so neither reading currently breaks a consumer.

## Verification

- Item 1: `git grep -n -i "whole roll\|whole rolling update\|whole duration of a roll" -- ':!docs/tickets'`
  finds, for `Ready`, only struck text and the registry string; `make lint` is green. *(Run
  2026-09-27 after the fix: for `Ready` it finds the struck text in ADR 0001 `:9`, `:112` and
  ADR 0002 `:535` and the registry string at `condition_registry.go:102`; the other hits - ADR
  0011 `:255`, 0023 `:264`, 0024 `:72` and three e2e comments - are about other things.
  `make lint` was not run.)*
- Reading 1: ADR 0001 D4 records the decided reading with its date and the superseded sentence
  marked; the `Ready` registry row carries no `declaredGap`; `make test-unit` and `make lint` are
  green; `git grep -n 'T18' -- ':!docs/tickets'` is empty.
- Reading 2: an e2e roll on both topologies observes `Ready=False` with the rolling-update reason
  and `True` after completion; the phase still reads `Rolling Update i/n` during the roll.

## History

- 2026-09-27: work list item 1 landed, file by file (read in `git diff` of the working tree):
  - [ADR 0001](../adr/0001-continue-reconciling-past-a-rejected-write.md): "for the whole roll"
    in Status (`:9`) and "for the whole duration of a roll" under D4 (`:112`) struck and
    corrected in place ("on every pass that ends on a rolling-update exit", naming the pass past
    a wait bound and the pause pass, "unless a post-update check ends that pass"); a dated
    "Amended 2026-09-27 (correction, no decision changes)" Status line; D4 itself unchanged.
  - [ADR 0002](../adr/0002-surface-a-blocked-reconcile-on-the-cr.md): the Residual-risks bullet
    (`:535` now) struck and corrected the same way; a dated Status line that also carries the
    T35 correction of D11.
  - [`api/v1/valkey_types.go`](../../api/v1/valkey_types.go), the `ConditionTypeReady` doc
    comment (`:42-46` now): "a pass that ends on a rolling-update exit returns before
    updateStatus …, so during a roll Ready keeps the value of the last pass that reached
    updateStatus (ADR 0001 D4). A pass whose wait has outlived its bound (ADR 0026 D5, D11) and
    the pass in which a data roll pauses do reach it, unless a post-update check ends the pass."
  - [`docs/operations/status.md:17`](../operations/status.md#ready): "keeps the value of the last
    status computation", both recompute cases named, a dated corrected marker.

  The registry string (`condition_registry.go:102`) is untouched, as planned (item 2). The doc
  comment grew by two lines, so `api/v1/valkey_types.go` references after `:44` in the tickets
  are two lower than the working tree now. The implementer asked for `make manifests` because
  an `api/v1` comment changed; a grep shows the `ConditionTypeReady` comment in neither
  `config/crd/bases/` nor the chart CRD (it documents a const, not a field), so no generated
  diff is expected - **not run**. **Urgency not recomputed in this pass** (the orchestrating run
  left every urgency but one to the owner). The frontmatter says it returns to `icebox` once
  item 1 lands, but read strictly rule 1 still matches: the registry string at
  `condition_registry.go:102` ("Ready keeps its pre-roll value for the whole roll") is itself a
  false statement in a tracked file, left to item 2 only because rewriting that line touches the
  `T18:` prefix `TestConditionRegistryGapsAreTraceable` requires (ticket 040, decision 2). So
  `now` holds until that string is made precise - keeping the prefix, or under 040's decision 2 -
  and `icebox` (rule 5) after it. **Not verified:** `make lint` was not run.
- 2026-09-27: adversarial review of the enrichment. Re-read at `4a7543e`: the exits
  `valkey_controller.go:336-342`, `:355`, `:363-366`, `:369`, `:422-428`, `:440-443`,
  `:473-485`; `rolling_update.go:2074`, `:2185`, `:2267`, `:2643`; ADR 0001 `:9`, `:88-107`,
  ADR 0002 `:521`, ADR 0026 `:629-633`, `valkey_types.go:42-44`, `status.md:17`,
  `condition_registry.go:17`, `:102`, `condition_registry_test.go:200-210` hold. Two
  precisions: the pause pass reaches `updateStatus` unless any post-update check ends it, not
  only the Sentinel roll (struck and corrected in place; the same wording belongs in the
  item 1 edits), and reading 1 also has to fix the registry row's `clearSite` string. Reading 1
  stays marked; work list item 1 is confirmed as XS with no decision. **Verified:** by reading.
  **Not verified:** nothing was run.
- 2026-09-27: enriched - re-verified at `4a7543e` and corrected every stale location in place
  (exits, ADR 0001 range, README row now `status.md:17`, the ADR 0002 residual now carried by
  059); added Fact, Options, Work list and Verification. Found that "for the whole roll" is false
  for a pass past a wait bound and for the pass that pauses. Urgency `icebox` -> `now` by rule 1
  (those sentences are false by code reading; back to `icebox` under rule 5 once work list item 1
  lands); effort `S–L` -> `S` (the recommended reading 1). **Verified:** by reading and grep at
  `4a7543e`. **Not verified:** nothing was run; the wds18 claim was not re-checked.
- 2026-09-27 - extracted verbatim from the collection ticket (now [archive/039-findings-from-the-1-11-0-fleet-rollout.md](archive/039-findings-from-the-1-11-0-fleet-rollout.md)) into its own file when the tickets were numbered. Frontmatter filled from the final board row (board archive of that file, groomed 2026-09-26) and from the section text.
