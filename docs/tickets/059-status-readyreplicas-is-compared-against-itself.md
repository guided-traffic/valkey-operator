---
id: T59
title: status.readyReplicas is compared against itself, so it reaches the CR only when another status field changes with it
state: analysed       # 2026-09-27 at 84a39c2: every fact re-verified by reading, both former Not-verified points settled by reading, options costed with a marked best; was filed
severity: low         # by reading the count cannot go stale while only the operator writes status, because the Ready condition fixes it in every branch; a third-party status write is never corrected, and a Ready reason, message or branch change reopens it (was: no stale value was constructed by reading; the masking holds on strings nothing tests)
security: none        # only the operator ClusterRole grants valkeys/status; a principal who can forge the count can forge every status field
urgency: now          # rule 1 as this repository applies it (false by reading, the 018/023/029/047 precedent): ADR 0002:522-527 says both blocked-pass tests assert the count, and the HA one does not, and :519-521 lets a phase-message change reopen the defect (work list item 2); back to later (rule 4, cheap known fix) once item 2 lands. Was later (rule 4)
effort: S             # A-prime: one capture moved, two callee signatures, one regression test, the ADR 0002 amendment, one developer-page paragraph, two code comments and one CLAUDE.md sentence (was: A, a two-line move)
blocked-by: decision  # ADR 0002 accepts the masking as a residual risk; taking the fix is judging it too fragile to keep
filed-from: the documentation restructure of 2026-09-27 (review of ADR 0002 D5 and its residual risks)
opened: 2026-09-27
decided:              # not recorded - no decision yet
done:                 # not recorded - not done
---

# T59 - status.readyReplicas is compared against itself, so it reaches the CR only when another status field changes with it

Filed on 2026-09-27 out of the review passes of the documentation restructure. A reviewer of
[docs/developer/reconcile-loop.md](../developer/reconcile-loop.md) found that the reason
[ADR 0002](../adr/0002-surface-a-blocked-reconcile-on-the-cr.md) gave for leaving
`readyReplicas` where it is ("every branch's phase message is a function of the ready count")
was imprecise. The ADR was corrected the same day: its Status now has an "Amended again
2026-09-27" paragraph, the D5 amendment is struck and restated, and Residual risks has a new
entry. The open decision in that correction is carried here. Everything below was read in the
working tree of `feat/rootless` on 2026-09-27 (`HEAD` = `f5c6886`; `git diff --stat HEAD` shows
no change under `internal/controller/` except `volumeclaim_conflict.go`, so the cited status
code is the committed code). No make target and no cluster was run for this ticket.
~~*(corrected 2026-09-27: re-read at `4a7543e` on `chore/maintenance-2026-09-27`. Between
`f5c6886` and `4a7543e` nothing under `internal/controller/` changed except an Event message
string in `volumeclaim_conflict.go`, and every location below still holds.)*~~
*(corrected 2026-09-27 at 84a39c2: every location is re-read at `84a39c2` on
`chore/maintenance-2026-09-27` and the links below point there. `bcc63c9` changed
`valkey_controller.go` (a comment line at `:2290-2291` moves every location from there on by
one), `api/v1/valkey_types.go` (the printer column moved from `:1131` to `:1133`) and ADR 0002
(its line numbers moved by 10 to 19). Two ranges the earlier note called holding were inexact
even at `4a7543e`: the second ADR 0002 residual-risk entry ended at `:515`, not `:510`, and the
`reconcile-loop.md` paragraph ended at `:164`, not `:162` (`git show 4a7543e:<file>`).)*

## Fact

**Mechanism.** `updateStatus` re-reads the CR
([`valkey_controller.go:2204-2206`](../../internal/controller/valkey_controller.go#L2204-L2206))
and assigns `v.Status.ReadyReplicas` from the data StatefulSet at
[`valkey_controller.go:2213-2214`](../../internal/controller/valkey_controller.go#L2213-L2214).
That happens *before* `updateStandaloneStatus`
([`:2228`](../../internal/controller/valkey_controller.go#L2228)) and `updateHAStatus`
([`:2451`](../../internal/controller/valkey_controller.go#L2451)) capture `prevStatus`.
`statusUnchanged` compares `prev.ReadyReplicas` with `curr.ReadyReplicas`
([`:2583`](../../internal/controller/valkey_controller.go#L2583)), but both hold the same value,
so that comparison can never fail. If nothing else differs, `persistStatus`
([`:2548`](../../internal/controller/valkey_controller.go#L2548)) skips the write
([`:2566-2568`](../../internal/controller/valkey_controller.go#L2566-L2568)). The count
reaches the stored CR only when some other field forces the write
([`:2570`](../../internal/controller/valkey_controller.go#L2570)).

**Verified:**

- **The order above**, and that `observerReady` and `operatorVersion` are assigned inside
  `persistStatus` after the capture
  ([`:2554-2564`](../../internal/controller/valkey_controller.go#L2554-L2564)). That is where
  ADR 0002 D5 moved `observerReady` on 2026-08-26, when the same defect had left it wrong on six
  of the eight observer-enabled clusters of a live fleet (ADR 0002 D5, amendment of
  2026-08-26, [`0002:125-139`](../adr/0002-surface-a-blocked-reconcile-on-the-cr.md#decision)).
  `readyReplicas` was left behind by decision (same ADR, `:156` and Residual risks
  `:491-529`).
- **Nothing else touches `v.Status` in the window** *(added 2026-09-27 at 84a39c2)*. Between the
  refresh `Get` at `:2204` and the captures at `:2228` and `:2451`, the only write to `v.Status`
  is `:2214`; the Sentinel StatefulSet read at `:2438-2440` writes into `sentinelSts`, not `v`.
  Between the capture and `statusUnchanged`, `reportRWServiceEndpoints`
  ([`rw_service_report.go:26-28`](../../internal/controller/rw_service_report.go#L26-L28)) and
  `recordSentinelPeerDrift` (`:2367-2410`) mutate conditions in place, `currentMasterPod`
  (`:2324-2342`) only reads, and nothing re-reads `v`. Non-test readers of
  `Status.ReadyReplicas` are `:2177` (`deploy.Status`), `:2213-2214`, `:2439`
  (`sentinelSts.Status`) and [`collector.go:196`](../../internal/metrics/collector.go#L196)
  (`grep -rn 'Status\.ReadyReplicas' internal/ cmd/ api/ | grep -v _test.go`); no operator
  decision reads the stored count.
- **Origin** *(added 2026-09-27 at 84a39c2)*. The dead comparison exists since `4b904f8`
  (2026-02-20, "fix: respect failing connections from operator to valkey & sentinal (#7)"),
  which introduced `prevStatus` and `statusUnchanged` after the existing assignment
  (`git show 4b904f8:internal/controller/valkey_controller.go`: assignment `:597`, captures
  `:611` and `:699`, compare `:789`). Before it the status was written unconditionally
  (`git show 4b904f8^:...`: assignment `:505`, unconditional `Status().Update` at `:559`), so
  nothing was masked. `git tag --contains 4b904f8` starts at `v1.0.1`: every release from
  v1.0.1 on carries it, v1.0.0 does not.
- **On a blocked pass the phase message carries nothing.** `persistStatus` puts
  `prevStatus.Phase` and `prevStatus.Message` back when `passIsBlocked`
  ([`reconcile_blocked.go:171`](../../internal/controller/reconcile_blocked.go#L171)) is true
  ([`:2549-2552`](../../internal/controller/valkey_controller.go#L2549-L2552)), and it does so
  before `statusUnchanged` runs. The fields left to force a write are the conditions,
  `masterPod`, `operatorVersion` and `observerReady`. Of those, only the `Ready` condition moves
  with the count: `RWServiceEmpty` is judged only when every pod is ready
  ([`rw_service_report.go:38`](../../internal/controller/rw_service_report.go#L38)),
  `SentinelPeersStale` does not depend on the count, `masterPod` is set only in all-ready
  branches (`:2252`, `:2477`, `:2490`), and `operatorVersion` (`:2555`) and `observerReady`
  (`:2559-2564`) are independent of it.
- **Which string moves with the count, branch by branch.** Standalone
  ([`:2234-2284`](../../internal/controller/valkey_controller.go#L2234-L2284)):
  - All ready (count = `spec.replicas`): the phase message is `All replicas are ready` or
    `Instance unreachable: …`. The `Ready` reason is `AllReplicasReady` or
    `ConnectivityCheckFailed`. No count appears.
  - Partly ready: the phase message (`:2264`) and the `Ready` message (`:2271`, reason
    `ReplicasNotReady` at `:2270`) both name the count.
  - None ready (count 0): no count appears.

  HA ([`:2457-2526`](../../internal/controller/valkey_controller.go#L2457-L2526)):
  - All ready: the OK phase message names the count (`:2491-2492`). The Error and Syncing
    messages do not; Syncing names the health check's synced counts, not the StatefulSet's
    ready count (`:2478-2486`). The `Ready` message never names the StatefulSet count.
  - Partly ready: both name it (phase `:2504-2505`, `Ready` reason `HAClusterProvisioning` at
    `:2511` and message at `:2512-2513`), also when the Valkey count is 0 and only Sentinels
    are ready.
  - None ready: neither does (`:2523-2524`).

  So a changed count changes the branch, which changes the `Ready` reason or status, or it
  stays in a partly-ready branch, which changes the `Ready` message. Inside an all-ready branch
  the count can change only together with `spec.replicas`. That bumps the generation, and
  `meta.SetStatusCondition` copies the new `ObservedGeneration` onto an existing condition
  (~~`k8s.io/apimachinery@v0.37.0/pkg/api/meta/conditions.go:62-63`, read in the module cache~~
  *(corrected 2026-09-27: [`go.mod:12`](../../go.mod#L12) pins v0.37.1 since `7017676`; the same
  lines 62-63 of `k8s.io/apimachinery@v0.37.1/pkg/api/meta/conditions.go`, read in the module
  cache)*).
- **The count cannot go stale while only the operator writes status** *(added 2026-09-27 at
  84a39c2, by reading; this was the first Not-verified point)*. The encoding argument:
  - The only non-test writers of the Valkey status are `persistStatus`
    ([`:2570`](../../internal/controller/valkey_controller.go#L2570)), `writePhase` (`:2628`,
    re-`Get` at `:2617`) and `writeStatusCondition` (`:2709`, re-`Get` at `:2693`); the latter
    two send the stored `Ready` condition and the stored count back unchanged, and a
    cache-stale re-`Get` ends in a 409, not in an overwrite. No other code sets
    `ConditionTypeReady`: its non-test hits are the nine sites in the two switches and
    [`condition_registry.go:94`](../../internal/controller/condition_registry.go#L94), and
    there is no `RemoveStatusCondition` or `Status.Conditions =` reassignment. So the stored
    `Ready` condition and the stored count always come from the same `persistStatus` write.
  - `spec.replicas` has `Minimum=1`
    ([`valkey_types.go:480`](../../api/v1/valkey_types.go#L480)), so the branches are
    disjoint. In every branch the `Ready` condition (reason, message, `ObservedGeneration`)
    fixes the count: all-ready reasons imply count = `spec.replicas` of that generation,
    partly-ready messages name it (a scale-down with count > `spec.replicas` lands there too),
    none-ready implies 0.
  - Hence a pass whose computed `Ready` equals the stored one computes the stored count, and a
    pass with a different count always changes `Ready` and writes. A cache-stale refresh `Get`
    changes nothing, because the cached status is itself one consistent pair.
  - The phase message is never the only field that moves with the count: the one
    count-naming message that is not in a partly-ready branch, the HA OK message, changes its
    count only with `spec.replicas`, which moves `ObservedGeneration` too. The `Ready`
    condition alone is sufficient on every pass, blocked or not.
- **A count from an earlier release is overwritten; a count written by a third party is not**
  *(added 2026-09-27 at 84a39c2, by reading; this was the second Not-verified point)*.
  `persistStatus` sets `OperatorVersion` after the capture
  ([`:2555`](../../internal/controller/valkey_controller.go#L2555)) from `main.version`
  ([`cmd/main.go:30`](../../cmd/main.go#L30), wired at `:121`), which release images stamp
  through `-X main.version=${BUILD_NUMBER:-dev}`
  ([`Containerfile:31`](../../Containerfile#L31); `BUILD_NUMBER` set at
  [`build.yml:83`](../../.github/workflows/build.yml#L83)). The first pass of a new release that
  reaches `updateStatus` therefore always writes, and that write carries the fresh count; a
  `dev` build does not force it. A status write by someone else is different: the refreshed `v`
  carries the forged count, `:2214` overwrites it before the capture, `statusUnchanged` sees no
  difference and nothing writes, so the forged value stands until another compared field
  changes. Who can write it: only the operator ClusterRole grants `valkeys/status`
  ([`clusterrole.yaml:21-27`](../../deploy/helm/valkey-operator/templates/clusterrole.yaml#L21-L27),
  [`config/rbac/role.yaml:156`](../../config/rbac/role.yaml#L156)); no `aggregate-to-*` role
  exists under `deploy/` or `config/`, and the pre-upgrade hook role grants the main resource
  only. The CR watch is generation-gated
  ([`valkey_controller.go:2987`](../../internal/controller/valkey_controller.go#L2987); `:306`
  is only a comment that mentions it), so a status-only edit starts no pass; the next pass
  comes from a StatefulSet or Deployment event (`:2988-2989`), a requeue or a resync.
- **The stored value has three consumers:**
  - the `Ready` printer column
    ([`valkey_types.go:1133`](../../api/v1/valkey_types.go#L1133));
  - the gauge `vko_valkey_status_ready_replicas`
    ([`collector.go:195-196`](../../internal/metrics/collector.go#L195-L196));
  - the alert `ValkeyReplicasMissing`, which fires on `spec - ready > 0` held for `15m`
    ([`prometheusrule.yaml:87-94`](../../deploy/helm/valkey-operator/templates/prometheusrule.yaml)).
- **No test isolates the count.** ~~The two blocked-pass tests in
  [`status_phase_test.go`](../../internal/controller/status_phase_test.go):~~
  *(corrected 2026-09-27 at 84a39c2: there are three blocked-pass tests in
  [`status_phase_test.go`](../../internal/controller/status_phase_test.go), not two; the third
  is below:)*
  - `TestUpdateStatus_KeepsNonPhaseFieldsWhileBlocked` (`:205`) marks the StatefulSet ready
    after the first pass (`:211`), so the count (`:223`) and the `Ready` condition (`:236`)
    change together.
  - `TestUpdateHAStatus_KeepsReadyTrueWhileBlocked` (`:243`) does not assert `readyReplicas` at
    all (`:243-270`).
  - `TestReconcile_BlockedPassDoesNotFlapPhase` (`:84`) marks the StatefulSet ready (`:93`)
    after a first pass left it unready, so the count moves 0 -> 3 together with `Ready`, and it
    asserts the count at `:113`.
  - `TestStatusUnchanged_DetectsChanges`
    ([`valkey_controller_test.go:854`](../../internal/controller/valkey_controller_test.go#L854),
    `readyReplicas` case at `:876-879`) tests the helper, not the order. No test calls
    `updateStandaloneStatus`, `updateHAStatus` or `persistStatus` directly (grep over
    `*_test.go` in `internal/` and `test/`).
- **The doc comment of `statusUnchanged` ~~lists~~ *(listed, until work list item 1 on
  2026-09-27; fixed, History)* one field too few** (added 2026-09-27).
  [`valkey_controller.go:2573-2575`](../../internal/controller/valkey_controller.go#L2573-L2575)
  said it returns true "if phase, message, readyReplicas, masterPod, operatorVersion, and
  conditions are all equal"; the function also compares `observerReady`
  ([`:2592`](../../internal/controller/valkey_controller.go#L2592)), so with those six equal and
  `observerReady` changed it returns false. ~~The comment predates the ADR 0002 D5 move of
  `observerReady` and was not updated with it.~~ *(corrected 2026-09-27, same day: the
  comparison came in `c6f97e2` (2026-03-20, the observer feature), which added the
  `ObserverReady` check to `statusUnchanged` without touching its doc comment
  (`git log -S'prev.ObserverReady'`); the ADR 0002 D5 move of the assignment on 2026-08-26 is
  unrelated.)* Work list item 1, committed in `bcc63c9`: the comment now names the seven fields
  the function compares (`:2576-2599`).
- **Some mid-roll passes reach `updateStatus`** *(added 2026-09-27 at 84a39c2; this
  replaces the third Not-verified point, struck below)*. An error or `NeedsRequeue` from
  `checkAndHandleRollingUpdate` returns before `updateStatus`
  ([`:336-342`](../../internal/controller/valkey_controller.go#L336-L342)). A pass that carries
  `DeferredRequeueAfter` (`:355`, a wait past its bound) continues, and `pauseRollingUpdate`
  returns an empty result
  ([`rolling_update.go:2646`](../../internal/controller/rolling_update.go#L2646)); both reach
  `updateStatus` (`:369`) unless `handlePostRollingUpdateChecks` returns done (`:363-366`). On a
  Sentinel cluster a holding data tier makes `runSentinelRollingUpdate` return not-done
  (`:465-467`), so the stall pass reaches `updateStatus` there as well. ADR 0001:110-118 and
  ADR 0002:535-545, as corrected in `bcc63c9`, say the same.

**Not verified:**

- ~~**That the count can never go stale today.** No case was found by reading, but nothing was
  measured and no test was written.~~ *(corrected 2026-09-27 at 84a39c2: settled by reading,
  see "The count cannot go stale while only the operator writes status" under Verified. Still
  not verified: nothing was measured, no test was run, and the fleet's stored counts were not
  read.)*
- ~~**Whether the stored status can hold a count that disagrees with an otherwise converged
  status.** Such a status could come from an earlier operator version or from a write by
  someone other than the operator. Today's code would never correct it, because no compared
  field would change. Not examined.~~ *(corrected 2026-09-27 at 84a39c2: examined by reading.
  A count from an earlier release is corrected by the first pass of a new release, because
  `operatorVersion` changes; a third-party write is never corrected until another compared
  field changes; see Verified.)*
- ~~**How this interacts with [018](018-ready-keeps-its-pre-roll-value-during-a-rolling-update.md).**
  The mechanism there is different: during a roll, `updateStatus` is not reached at all, so the
  count keeps its pre-roll value whatever the assignment order is. This ticket does not change
  that.~~ *(corrected 2026-09-27 at 84a39c2: `updateStatus` is skipped on every pass that ends on
  a rolling-update exit, but a pass past a wait bound and the pass in which the data roll pauses
  reach it and recompute the count; see Verified. The mechanism of
  [018](018-ready-keeps-its-pre-roll-value-during-a-rolling-update.md) is still different, and
  no option here changes the passes that skip `updateStatus`.)*
- **Whether any tool in the production fleet writes the Valkey status subresource**, for
  example a backup restore. Only the RBAC in this repository was read; a cluster-admin
  principal or a restore controller is outside it. What would settle it: an audit of the
  `valkeys/status` writers on the fleet's API servers, which this run may not do.
- **The seeded-stale test's behaviour.** That it fails at `84a39c2` and passes after the fix
  rests on reading; running it both ways is what decides it.

## Impact

No operator hits this today, as far as reading shows. ~~The coupling breaks in two cases: when a
phase message or a `Ready` string stops naming the count, or when a branch is added whose
strings do not change with the count.~~ *(corrected 2026-09-27 at 84a39c2: a phase message
change alone reopens nothing, because the phase message is never the only field that moves
with the count, and in the all-ready and none-ready branches no `Ready` string names the count;
the reason and `ObservedGeneration` imply it. The coupling breaks in three cases: a `Ready`
reason shared by two branches with different counts, a partly-ready `Ready` message that stops
naming the count, or a new branch whose `Ready` condition does not fix the count. It is also
broken today for one input: a count written to the status by someone other than the
operator.)* From then on the stored `readyReplicas` stays at the value
of the last write that some other field caused, and nothing reports it. The printer column, the
gauge and `ValkeyReplicasMissing` all read the stored value:

- a value stuck too high keeps the alert silent on a cluster that is short of pods;
- a value stuck too low fires the alert on a cluster that is complete.

*(added 2026-09-27 at 84a39c2)* ADR 0026
([`0026:628-633`](../adr/0026-a-pod-being-deleted-is-not-available.md)) promises that past the
availability budget the status write runs again and `ValkeyReplicasMissing` can see a data-tier
stall. In that pass the count reaches the CR only through the masking: the `Ready` condition
moves to `ReplicasNotReady` or `HAClusterProvisioning` and carries the write.

`observerReady` showed this exact defect class on a live fleet. Its three seconds of real lag
became a value that stayed wrong (ADR 0002 D5).

## Options

### Decision 1: take the fix, and in which shape, or refuse it

**What the code does today.** `updateStatus` re-reads the CR at
[`valkey_controller.go:2204`](../../internal/controller/valkey_controller.go#L2204) and writes
the data StatefulSet's ready count into `v.Status.ReadyReplicas` at `:2214`, in its prologue.
Each callee then takes its own `prevStatus` copy (`:2228` standalone, `:2451` HA), so `prev` and
`curr` carry the same count and the comparison at `:2583` is dead. The count still reaches the
CR on every pass that changes it, because the `Ready` condition, written by the same
`persistStatus` call, fixes the count in every branch (Fact, encoding argument). That masking
is a property of the `Ready` reasons, messages and `ObservedGeneration`, and no test pins it
(Fact, "No test isolates the count"). A count written by a third party is never corrected
until another field changes. The prologue gap between the refresh `Get` and the callee captures
is where both instances of this defect lived: `readyReplicas` since `4b904f8` (2026-02-20), and
`observerReady` from `c6f97e2` (2026-03-20) until ADR 0002 D5 moved it on 2026-08-26. The only
guard against a third instance is the NOTE comment at
[`:2208-2211`](../../internal/controller/valkey_controller.go#L2208-L2211).

**What the choice changes.** Whether the count is compared against the stored value, whether a
third-party count is corrected on the next pass that reaches `updateStatus`, and whether ADR
0002 D5's accepted residual risk
([`0002:491-529`](../adr/0002-surface-a-blocked-reconcile-on-the-cr.md)) stays accepted.

**What it does not change.** The passes that skip `updateStatus` on a rolling-update exit
(ADR 0001 D4, [018](018-ready-keeps-its-pre-roll-value-during-a-rolling-update.md)); any
`Ready`, phase or message string; any pod, CRD, RBAC or chart. By the encoding argument, a
fixed operator writes exactly when today's operator writes for every status only the operator
wrote: no extra status writes, no roll, nothing on upgrade beyond the write the
`operatorVersion` change already forces (`:2555`). Every option below is therefore
behaviour-neutral for the fleet as it runs today.

**The regression test, shared by A-prime and A.** It seeds a stored status whose
`readyReplicas` disagrees with the StatefulSet while every other field is converged (first a
`reconcileFor`, then `c.Status().Update` with only the count changed, the pattern at
[`status_phase_test.go:213-216`](../../internal/controller/status_phase_test.go#L213-L216)),
runs `updateStatus` once on a blocked and once on an unblocked context, and asserts the write
both times, with at least one case per topology because each option touches one site per
topology. By reading it fails at `84a39c2` (`:2214` overwrites the seeded value before the
capture, `statusUnchanged` returns true at `:2566`) and passes after either fix. Its comment
cites ADR 0002 D5, never T59 (ADR 0034; [040](040-tracked-files-cite-work-items-instead-of-adrs.md)).

**A-prime - move the capture, not the field (recommended).** Take
`prevStatus := v.Status.DeepCopy()` once in `updateStatus`, directly after the refresh `Get` at
`:2204-2206` and before `:2213`, and pass it to `updateStandaloneStatus` and `updateHAStatus` in
place of their own captures at `:2228` and `:2451`. `:2214` stays where it is and is now a
change like any other; `persistStatus`, `operatorVersion` and `observerReady` stay as they are.
- Cost S: one capture added, two removed, two callee signatures gain a
  `*vkov1.ValkeyStatus` parameter and the call sites `:2218` and `:2222` change (no test calls
  the callees directly); the regression test; the ADR 0002 amendment, which marks
  `0002:43-49` (the Status paragraph), `:140-147` (the D5 amendment's masking passage),
  `:156` (the D5 sentence), `:491-501` and `:502-529` (Residual risks) superseded in place and
  records that the fix taken is a different mechanism from the one the ADR named (a moved
  capture, not a moved assignment) and why; `reconcile-loop.md`
  [`:149-150`](../developer/reconcile-loop.md#the-status-write) ("Both capture `prevStatus`
  before they change anything themselves, but after `updateStatus` has already set
  `readyReplicas`", false afterwards) and `:158-164` (the masking); the NOTE at `:2208-2211`,
  rewritten to say the capture sits directly after the read so every later assignment is a
  change; the `persistStatus` doc comment
  [`:2531-2547`](../../internal/controller/valkey_controller.go#L2531-L2547), whose rationale for
  `observerReady` (`:2539-2547`, "a value assigned before the caller captured prevStatus")
  stops being the operative reason and is rewritten, not only trimmed; and `CLAUDE.md:579`
  ("next to `OperatorVersion`"), generalised to "after the capture".
- Consequences: the count is compared against the stored value; a third-party count
  self-corrects on the next pass that reaches `updateStatus`; any future prologue assignment is
  compared correctly without anyone reading the NOTE, because anything assigned after the
  `Get` is after the capture and anything assigned before it is overwritten by the `Get`.
  `reportRWServiceEndpoints` still runs between the capture and `persistStatus`, as its
  contract requires ([`rw_service_report.go:26-28`](../../internal/controller/rw_service_report.go#L26-L28)),
  and the "(T7)" comments at `:2230-2231` and `:2453-2454` stay true; if the change rewrites
  them, the T-label goes under 040's rule. The guard tests of D5,
  `TestUpdateStatus_ObserverReadyTransitionIsPersistedOnItsOwn` and
  `TestUpdateStatus_DisablingTheObserverClearsAStoredVerdict`, are unaffected.
  [035](035-master-records-lag-the-real-master.md) decision 2 (B2) writes after `persistStatus`
  and its placement rule (no write between capture and `persistStatus`) still holds with the
  window starting at `:2204`.

**A - move the assignment to the far side of the capture.** Delete `:2214` and set
`v.Status.ReadyReplicas = readyReplicas` directly after the captures at `:2228` and `:2451`
(variant: in `persistStatus` next to `observerReady`, which needs the count as a new
parameter).
- Cost S, and the smallest diff of the fixes: -1/+2 lines with no signature change in the first
  variant; the regression test; the same ADR 0002 places as A-prime (without the "different
  mechanism" note, because this is the fix the ADR names at `0002:500-501` and `:528-529`);
  `reconcile-loop.md:149-150` and `:158-164`; the NOTE at `:2208-2211`. The `persistStatus`
  rationale at `:2539-2547` and `CLAUDE.md:579` stay true as written.
- Consequences: the count is compared against the stored value and a third-party count
  self-corrects, as under A-prime. It reproduces the D5 precedent that `CLAUDE.md:579` states.
  It removes one occupant of the prologue window and leaves the window, guarded only by the
  NOTE at `:2208-2211`, which was already missed once (for `observerReady`).

**C - refuse the fix and record the refusal in ADR 0002.** Keep D5's accepted residual risk,
replace its Not-verified sentence (`0002:521-522`) with today's encoding argument (the `Ready`
condition fixes the count in every branch; a third-party write is never corrected), record the
refusal, and drop this ticket.
- Cost XS: one ADR 0002 residual-risk edit, then `state: dropped`.
- Consequences: the count's persistence keeps resting on the `Ready` reasons, messages and
  `ObservedGeneration`, which nothing tests; every future edit of a `Ready` string or branch
  silently carries the risk; a third-party count stays wrong until another field changes.

**Considered and not listed** *(added 2026-09-27 at 84a39c2)*: deleting the dead
`ReadyReplicas` comparison from `statusUnchanged` only makes the dependence on `Ready`
explicit and fixes nothing; moving `operatorVersion` and `observerReady` out of `persistStatus`
together with A-prime is scope nobody needs, it touches code that works; "a new ADR or an
amendment of ADR 0002" has one answer (amend in place, the re-decision rule of `CLAUDE.md`), so
it is not a choice.

**A-prime is marked best.** It and A cost the same class (S) and have the same effect on the
fleet: both write exactly when today's operator writes for every status only the operator
wrote, so neither rolls a pod, adds a status write or changes anything on upgrade. A-prime
beats A, the runner-up, because the defect has occurred twice in the same place - a prologue
assignment between the refresh `Get` at `:2204` and a capture in a different function - and
A-prime removes that window, so a third field cannot repeat it, while A removes one field and
leaves the window guarded by a comment that was already missed once. What A has for it is
real: it is the fix ADR 0002 names, it is the smaller diff, and it leaves the `persistStatus`
rationale and `CLAUDE.md:579` true; A-prime pays for that with two signatures and three more
documentation edits, all S. Both beat C because the fix is behaviour-neutral and S, and the
ADR's own condition for taking it ("if the coupling is ever judged too fragile to keep",
`0002:500-501`) is met by the finding that one condition's encoding alone carries the count,
untested, and that a third-party count is never corrected. Checkable: the regression test
fails at `84a39c2` and passes after the change; under A-prime,
`grep -n 'prevStatus := v.Status.DeepCopy' internal/controller/valkey_controller.go` finds
exactly one hit, directly after the refresh `Get` in `updateStatus`;
`TestStatusUnchanged_DetectsChanges` and the observer guard tests stay green, because a
converged status still compares equal.

## Work list

1. **XS, no decision needed** *(added 2026-09-27)*: correct the `statusUnchanged` doc comment at
   [`valkey_controller.go:2573-2575`](../../internal/controller/valkey_controller.go#L2573-L2575)
   to name `observerReady` among the compared fields. Comment only; it does not close this
   ticket. **Done 2026-09-27**, committed in `bcc63c9` ("docs: correct comments and records that
   the code contradicts").
2. **XS, no decision needed, rule 1** *(added 2026-09-27 at 84a39c2)*: ADR 0002
   [`:522-527`](../adr/0002-surface-a-blocked-reconcile-on-the-cr.md) says the two blocked-pass
   status tests "mark the StatefulSets ready after the first pass and assert the `Ready`
   condition along with the count, so neither isolates the count". That is false by reading:
   `TestUpdateHAStatus_KeepsReadyTrueWhileBlocked`
   ([`status_phase_test.go:243-270`](../../internal/controller/status_phase_test.go#L243-L270))
   asserts no count, and a third blocked-pass test, `TestReconcile_BlockedPassDoesNotFlapPhase`
   (`:84`, count at `:113`), is not named. The conclusion (no test isolates the count) holds.
   In the same entry, `:519-521` says a change "not only to a phase message" can reopen the
   defect, which implies that a phase-message change can; by the encoding argument (Fact) it
   cannot, because the phase message is never the only field that moves with the count.
   Strike both sentences in place and restate them, with a dated correction note in ADR 0002's
   Status, as the ADR rules require. Needed whatever Decision 1 becomes: under A-prime or A the
   entry is marked superseded, under C it is kept, and in both cases a reader must not find the
   false sentence stated as current. Outside `docs/tickets/`, so not done in this run.
3. **Waits on Decision 1**: the chosen option's code change, the regression test and the
   documentation edits, exactly as listed under that option. The code comments and the test
   cite ADR 0002 D5, never T59 (ADR 0034;
   [040](040-tracked-files-cite-work-items-instead-of-adrs.md)).
   [035](035-master-records-lag-the-real-master.md) decision 2 (B2, decided) adds a write after
   `persistStatus` on the Sentinel status path; both changes edit the tail of `updateHAStatus`
   (`:2451-2528`), so the overlap is textual only, and which lands first is optional.
   ~~*(Added 2026-09-27, cross-ticket from
   [033](033-integration-tests-read-the-cache-after-a-write.md), Adjacent findings: `persistStatus`
   writes with the resourceVersion of the cached refresh at `:2204` and no retry, so a status write
   earlier in the pass can fail it with 409 - a hypothesis by reading, to be filed as its own
   ticket. Its fix re-derives the status on a fresh object, which moves or repeats the refresh
   `Get` that A-prime anchors the capture to; whichever of the two lands second re-anchors the
   capture after the last refresh. Under A the capture sites stay where they are and the same
   re-check applies.)*~~ *(corrected 2026-09-27: filed as
   [T78](078-the-status-write-conflicts-with-the-operators-own-earlier-write.md), which holds the
   finding that `persistStatus` writes with the resourceVersion of the cached refresh at `:2204`
   and fails the pass with a 409 when the cache has not yet delivered an earlier CR write, the CI
   409 attribution and its options. Its recommended R1 keeps the refresh `Get` where it is and
   changes only its reader, so A-prime and A compose with every T78 option in either order and
   nothing needs re-anchoring; the re-anchor note above held only for a fix that moves or repeats
   that `Get`, and none of T78's options does. No dependency, sequencing only.)*
4. **Closing, after item 3** (ADR 0034): the decision is extracted into ADR 0002 (item 3 does
   that), `reconcile-loop.md` carries the subsystem knowledge, then the ticket moves to
   `archive/`. Under C the extraction is the residual-risk edit, then `dropped` and the move.

## Decision

None yet.

## Verification

- Item 1 *(added 2026-09-27)*: the `statusUnchanged` doc comment names the seven fields the
  function compares (`:2576-2599`); `make lint` is green. *(2026-09-27 after the fix: the
  comment names phase, message, readyReplicas, masterPod, operatorVersion, observerReady and
  conditions, which are the seven fields the function compares, read against its body.
  `make lint` was not run.)* *(2026-09-27 at 84a39c2: the comment is in `bcc63c9`, read with
  `git show bcc63c9 -- internal/controller/valkey_controller.go`, hunk
  `@@ -2570,8 +2571,8 @@`.)*
- Item 2: ADR 0002 no longer states that both blocked-pass tests assert the count; the
  corrected text names the three tests and what each asserts, and the ADR's Status carries the
  dated correction.
- The new unit test fails against today's order and passes after the fix (the revert check of
  [ADR 0017](../adr/0017-test-and-ci-policy.md)), on a blocked and an unblocked pass, for both
  topologies.
- `make test-unit`, `make lint` and `make cyclo` are green.
- ADR 0002: the D5 amendment passage (`:140-147`), the D5 sentence (`:156`) and both
  Residual-risks entries on `readyReplicas` (`:491-501`, `:502-529`) are marked superseded in
  place, and Status is amended with the date. The ADR's row in
  [docs/adr/README.md](../adr/README.md) changes in the same change if its State moves.
- [reconcile-loop.md](../developer/reconcile-loop.md) no longer says that `readyReplicas` is
  assigned before the capture, and (A-prime) no longer says that both callees capture
  `prevStatus`.
- ~~`git grep -n 'readyReplicas' -- ':!docs/tickets'` finds no other statement of the masking.~~
  *(corrected 2026-09-27 at 84a39c2: a superseded rule is marked in place, not deleted, so the
  masking text stays in ADR 0002. The check is: `git grep -n 'readyReplicas' -- ':!docs/tickets'`
  finds no statement of the masking that is not struck or marked superseded.)*
- A-prime only: `grep -n 'prevStatus := v.Status.DeepCopy' internal/controller/valkey_controller.go`
  returns one line, directly after the refresh `Get` in `updateStatus`, and `CLAUDE.md` no
  longer names `OperatorVersion` as the only valid place for a status assignment.

## History

- 2026-09-27: re-verified at 84a39c2 (auditor, facts skeptic and design skeptic; the disputed
  points checked again by reading at `84a39c2`). **Checked:** the mechanism, every cited
  location, the three status writers, every `ConditionTypeReady` site, the readers of
  `Status.ReadyReplicas`, the apimachinery pin, the RBAC for `valkeys/status`, the CR watch,
  the rolling-update exits before `updateStatus`, the blocked-pass tests, the origin of the
  dead comparison, ADR 0002's residual-risk text and `reconcile-loop.md`. **Locations re-read
  at 84a39c2** and fixed in the links: every `valkey_controller.go` location from `:2290` on is
  one line lower than the body said (`bcc63c9` added a comment line), the printer column moved
  to `valkey_types.go:1133`, ADR 0002 to `:43-49`, `:156`, `:491-529`, `reconcile-loop.md` to
  `:149-164`, the `persistStatus` doc comment to `:2531-2547`. **Found false or outdated,
  corrected in place:** the intro note that every location still held (and two ranges were
  inexact even at `4a7543e`); "The two blocked-pass tests" (there are three,
  `TestReconcile_BlockedPassDoesNotFlapPhase` at `status_phase_test.go:84`); the Not-verified
  point on 018 ("during a roll, `updateStatus` is not reached at all": a pass past a wait bound
  and the pass that pauses reach it, `valkey_controller.go:355`, `:363-369`,
  `rolling_update.go:2646`); the Impact sentence that a phase-message change reopens the
  defect (only the `Ready` condition's encoding matters); the Verification grep, which would
  have demanded deleting superseded ADR text; the earlier History entry's "working tree"
  (item 1 is committed in `bcc63c9`). **Settled by reading, promoted to Verified:** the count
  cannot go stale while only the operator writes status (encoding argument), a count from an
  earlier release is overwritten on the first pass of a new release (`operatorVersion`), a
  third-party count is never corrected. **New facts:** origin `4b904f8` (2026-02-20), in every
  release from v1.0.1 on, v1.0.0 wrote status unconditionally; the generation-gated watch is at
  `:2987` (an auditor draft cited the comment at `:307`, not copied); ADR 0026's promise that
  `ValkeyReplicasMissing` sees a data-tier stall rests on the masking in that pass; ADR 0002
  `:522-527` and `:519-521` are false by reading (work list item 2). **Measured:** nothing; no docker
  measurement was needed (no claim depends on Valkey behaviour) and no test was run. Commands
  used: `git log -S'prevStatus := v.Status.DeepCopy()'`, `git log -S'prev.ObserverReady'`,
  `git show 4b904f8:` and `4b904f8^:` of `valkey_controller.go`, `git tag --contains 4b904f8`,
  `git show bcc63c9 -- internal/controller/valkey_controller.go`, the greps named in Fact, and
  `conditions.go:62-64` of `k8s.io/apimachinery@v0.37.1` in the module cache. **Options:**
  rewritten as one decision with mechanism first. Added **A-prime** (move the capture to
  directly after the refresh `Get`), now **recommended**: it costs the same class as A and
  closes the window in which the defect happened twice, where A leaves it guarded by a comment.
  **Recommendation changed from A to A-prime** for that reason; A is the runner-up. A's text
  gained the missing ADR 0002 place `:140-147` and lost the claim that A needs a new
  `persistStatus` parameter (only its variant does). **Removed: B** ("keep the masking and pin
  it with a table test over every branch"): dominated once the fix is known to be
  behaviour-neutral - it costs about the same, pins strings instead of removing the dependency,
  lets a new branch escape the table and leaves a third-party count uncorrected. **Reframed: C**
  from "leave it as is" (not an actual choice) to "refuse the fix and record the refusal and the
  encoding argument in ADR 0002, then drop the ticket". **Cross-ticket:** 018 is independent in
  both directions (the masking also holds on the roll passes that reach `updateStatus`); 018:111
  names 059's "option A" as closing the `readyReplicas` remainder, which A-prime does equally;
  018:376-378 cites `059:117-120` for the struck 018 bullet, which this edit moved to
  059:230-237 (018 is not changed from here).
  035 B2 is unaffected, overlap textual only. **Review of this entry, same day:** the refresh
  `Get` is at `valkey_controller.go:2204` (`:2203` is its comment), fixed in every link; the
  ADR 0002 roll correction is `:535-545`, not `:539-545`; the three options considered and not
  listed are recorded under Options. 040: the fix cites ADR 0002 D5. **Frontmatter:**
  `state` filed -> analysed (every fact verified by reading, options costed and marked);
  `severity` stays low with a new comment (the count cannot go stale by reading; third-party
  writes and `Ready` changes reopen it); `urgency` later -> now by rule 1 as this repository
  applies it to a statement false by reading (the 018, 023, 029 and 047 precedent, and this
  ticket's own earlier entry below), for ADR 0002 `:519-527`, back to later (rule 4) once work
  list item 2 lands. **Disputed:** the facts skeptic left it to the owner whether a statement
  false by reading, not measured, counts under rule 1; if it does not, `later` by rule 4 stands.
  `effort` stays S (comment rewritten for A-prime); `security` none and `blocked-by` decision
  unchanged.
  Cross-ticket: in the consistency pass of the same day, Work list item 3 gained the dependency on
  033's unfiled `persistStatus` 409 finding (its fix moves or repeats the refresh `Get` that
  A-prime anchors on; 033 records the same), and 018's cite of the moved 018 bullet was corrected
  on 018's side. Filed: the `persistStatus` 409 finding this ticket parked in Work
  list item 3 is now [T78](078-the-status-write-conflicts-with-the-operators-own-earlier-write.md)
  (severity low, security none, effort S, state analysed); item 3 points there instead of carrying
  the hypothesis, and its re-anchor note is struck, because T78's options keep the refresh `Get`
  at `:2204` in place and compose with A-prime and A in either order. Nothing in this ticket's
  frontmatter, options or recommendation rested on the moved finding, so none of it changes.
- 2026-09-27: urgency `now` -> `later` (rule 4): item 1, the only rule-1 statement, landed; option A is a cheap known fix. Applied as the History entry below derived it.
- 2026-09-27: work list item 1 landed, one file (read in `git diff` of the working tree):
  [`internal/controller/valkey_controller.go`](../../internal/controller/valkey_controller.go),
  the `statusUnchanged` doc comment (two lines, rewrapped in place) reads "It returns true if
  phase, message, readyReplicas, masterPod, operatorVersion, observerReady and conditions are
  all equal". Comment only. Line drift from other comment edits of the same change: the
  `currentMasterPod` comment gained a line at `:2290`, so every reference above from `:2290` on
  (`:2450`, `:2456-2525`, `:2530-2563`, `:2572-2597`) is one lower than the working tree now -
  the doc comment itself sits at `:2573-2575`; `:2208-2228` are unchanged. Item 2 and the
  decision are untouched. **Urgency not recomputed in this pass**
  (the orchestrating run left every urgency but one to the owner): by the frontmatter's own
  derivation it falls from `now` to `later` (rule 4), because item 1 was the only rule-1
  statement. **Not verified:** `make lint` was not run.
- 2026-09-27: adversarial review of the enrichment. Re-read at `4a7543e`: `go.mod:12`
  (v0.37.1), `conditions.go:62-63` in the v0.37.1 module, `valkey_controller.go:2208-2214`,
  `:2530-2546`, `:2572-2597`, the grep for `Status.ReadyReplicas` (`:2177`, `:2213-2214`,
  `:2438`), ADR 0002 `:146` and `reconcile-loop.md:149-162` hold. One claim was wrong and is
  struck and corrected in place: the doc comment's omission dates from `c6f97e2`, not from the
  D5 move. Option A stays marked; the work list item 1 is confirmed as XS with no decision.
  **Verified:** by reading and `git log -S`. **Not verified:** nothing was run.
- 2026-09-27: enriched - re-verified every location at `4a7543e` (the apimachinery pin corrected
  to v0.37.1), added the ADR 0002, developer-page and comment locations to option A, and split
  out an XS no-decision item: the `statusUnchanged` doc comment omits `observerReady`. Urgency
  `later` -> `now` by rule 1, because that comment is false by code reading; it returns to
  `later` (rule 4) once item 1 lands. **Verified:** by reading at `4a7543e` and
  `git diff f5c6886 4a7543e`. **Not verified:** nothing was run.
- 2026-09-27: filed out of the documentation restructure. The imprecise argument in ADR 0002 was
  corrected in that ADR the same day. This ticket carries the decision the correction left
  open: whether to take the fix that the ADR names and has not taken.
