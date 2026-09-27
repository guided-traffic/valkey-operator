---
id: T18
title: "`Ready` keeps its pre-roll value for the whole rolling update — decided in ADR 0001 D4, re-decision request"
state: analysed       # "open"; deferred 2026-08-26 as a re-decision
severity: low
security: none
urgency: icebox
effort: S–L           # as recorded on the board; depends entirely on which reading of Ready is chosen
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

- [`valkey_controller.go:320-322`](../../internal/controller/valkey_controller.go#L320-L322) — `rollingResult.Error`
- [`valkey_controller.go:324-326`](../../internal/controller/valkey_controller.go#L324-L326) — `rollingResult.NeedsRequeue`

> **Corrected 2026-08-26 — this item undercounts, and the correction makes reading 2
> bigger, not smaller.** Three further exits skip `updateStatus`: the terminal
> (`done == true`) returns of `handlePostRollingUpdateChecks`, propagated at
> [`:343-345`](../../internal/controller/valkey_controller.go#L343-L345) — the Sentinel roll
> error ([`:396`](../../internal/controller/valkey_controller.go#L396)), the Sentinel roll
> requeue ([`:399`](../../internal/controller/valkey_controller.go#L399)) and the no-master
> recovery ([`:417`](../../internal/controller/valkey_controller.go#L417),
> [`:419`](../../internal/controller/valkey_controller.go#L419)).
>
> The Decision below says those exits "were already changed for exactly this reason". That
> is true only of the **non-terminal** (`done == false`) result, which is now carried to the
> end of the pass. The terminal ones still return early. **Consequence: a full Sentinel-tier
> roll also freezes `Ready`** — so reading 2 is a larger change than this item already warns,
> and `updateStatus` is only reached at
> [`:349`](../../internal/controller/valkey_controller.go#L349).

`NeedsRequeue` is set on essentially every pass of an active roll, so `updateStatus`
never runs and the `Ready` condition keeps whatever it held before the roll started —
normally `True / HAClusterReady` — while `updatePhase` writes `Rolling Update i/n`. So a
cluster reports Ready while its pods are being deleted one by one. `masterPod` and
`observerReady` freeze for the same reason and the same duration.

**This is decided behaviour, not an oversight.** ADR 0001 D4
([`0001:84-91`](../adr/0001-continue-reconciling-past-a-rejected-write.md)) closes with:
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
> capture ([`:2386-2393`](../../internal/controller/valkey_controller.go#L2386-L2393)) and
> compared by `statusUnchanged` at
> [`:2422`](../../internal/controller/valkey_controller.go#L2422) — that guard is live.
> **`ReadyReplicas` still carries the defect**, by the fix commit's own admission and
> recorded as an accepted residual in ADR 0002 `:403`. So this item is the place where that
> remainder would be closed, and reading 2 subsumes it.
>
> The documentation dependency is also discharged: the `ConditionTypeReady` doc comment this
> item wanted to decide together with T6d P1 exists at
> [`api/v1/valkey_types.go:42-44`](../../api/v1/valkey_types.go#L42-L44). **The coupling is
> therefore one-sided now — the doc exists, the decision does not.** This item is blocked on
> a human picking a reading, nothing else.

## Decision

**Deferred 2026-08-26 as a re-decision, and half of it is now documented.** No code changed:
this is ADR 0001 D4 operating as written, so closing it means amending that ADR in place, not
shipping a fix.

What did land is the part that was actually missing — the *price* of D4's second half is now
stated rather than implied. ADR 0001 D4 carries a clarification saying that those exits leave
`Ready`, `masterPod`, `readyReplicas` and `observerReady` at their pre-roll values for the
whole roll, and explicitly leaves open which reading of `Ready` is intended.
`vkov1.ConditionTypeReady`'s doc comment and the README row say the same, and
`conditionRegistry` carries it as a declared gap naming T18. So the behaviour is discoverable
from three places instead of zero, which is what made it look like a defect.

The open question is unchanged and is a product decision, not a code one: does `Ready` mean
"the last steady state was healthy" or "the cluster is serving now"? Reading 1 needs nothing
further. Reading 2 is bigger than one write site — it means reaching `persistStatus` on every
exit, while respecting ADR 0002 D8. Also verified while deferring: nothing gates on the
condition today, in this repo or in the wds18 Flux setup (no `Kustomization` managing a
Valkey CR carries `healthChecks`), so neither reading currently breaks a consumer.

## History

- 2026-09-27 - extracted verbatim from the collection ticket (now [archive/039-findings-from-the-1-11-0-fleet-rollout.md](archive/039-findings-from-the-1-11-0-fleet-rollout.md)) into its own file when the tickets were numbered. Frontmatter filled from the final board row (board archive of that file, groomed 2026-09-26) and from the section text.
