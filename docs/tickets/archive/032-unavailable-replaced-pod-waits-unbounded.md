---
id: T32
title: a replaced pod that exists but never becomes available stalls the roll with no bound and no condition
state: done
severity: medium
security: none
urgency: next
effort: L
filed-from: T31 analysis, 2026-09-25
opened: 2026-09-26
decided: 2026-09-26
done: 2026-09-26
---

Found while analysing [T31](031-generated-pods-run-as-root.md) and filed on Hans's
decision of 2026-09-26 as a prerequisite of T31's release: T31 is the first change that rolls
every cluster of a fleet automatically at the operator upgrade — both tiers — so a roll that can
hang silently stops being a single-cluster nuisance. **Analysed, decided and implemented
2026-09-26** on `feat/rootless`, with an adversarial review round before release; the
Verification section records every run and the "Implementation notes" what the review changed.
The Fact and Options sections are the analysis as filed, labelled **read** (in the tree at
`9925539`, or in the upstream sources named).

## Fact

### The data tier: seven waits, none bounded

`waitForUnavailablePod` ([`rolling_update.go:2061-2069`](../../../internal/controller/rolling_update.go))
handles a pod that exists but is not available. A terminating pod goes to `terminationWait`,
bounded by `podTerminationOverrun` ([ADR 0026](../../adr/0026-a-pod-being-deleted-is-not-available.md)
D5). Every other pod gets a log line and a requeue after `rollingUpdateRequeueDelay` — no
`ensureWaitBound`, no condition, no phase update. One more site waits without even calling it.

| # | Site | Line | Waits on | Pod is |
|---|---|---|---|---|
| 1 | `standaloneWait`, before the delete | `:3408` | the only pod of a `replicas: 1` cluster | outdated |
| 2 | `standaloneWait`, after | `:3422` | the same pod | current |
| 3 | `replaceNextReplica` | `:2136-2139` | `candidates[0]`, the youngest outdated replica | outdated |
| 4 | `verifyReplacedReplicasSynced` | `:2232-2235` | a replaced replica; returns before the sync-wait bound is armed (comment `:2220-2231`) | current |
| 5 | `waitForReplicasReady` | `:2453-2456` | every replica before the master failover; its bound covers "available but not replicating", not "never available" | current (and an available but outdated second master, which `:2453` also waits on) |
| 6 | `replaceRemainingPods`, loop | `:2656-2659` | the remaining outdated pods, the former master included | outdated |
| 7 | `replaceRemainingPods`, fall-through | `:2691-2692` | nothing it names — no pod, no log line | current |

Row 7 was found in the analysis. On the Sentinel path in `failover-triggered` or
`replacing-master`, a current pod that is not Ready keeps `countUpdatedPods` below the total
(it counts `reachable()`), so `handleRollingUpdate` dispatches to `handlePostFailover` →
`handleNewMasterFound` → `replaceRemainingPods`, whose loop finds no outdated pod and falls
through to "Should not reach here, but requeue to be safe" on every pass.

Row 2 is almost unreachable, and the standalone stall looks different for that reason:
`handleStandaloneRollingUpdate` never writes the rolling-update state annotation (no
`setRollingUpdateState` in `:3364-3439`), and `checkAndHandleRollingUpdate` returns early when
no pod is outdated and no state is recorded (`:254-276`). A current single pod that never
starts is therefore reported by `updateStandaloneStatus` as `Provisioning`, the ordinary status
path. The unbounded wait begins only after a spec fix, at row 1.

What the pod is decides what can help:

- **Outdated (rows 1, 3, 6).** After a spec fix the stuck pod is outdated and the youngest (it
  was created last), so it is `candidates[0]`. The operator never deletes it, because every
  delete site requires `available()` (ADR 0026 D1). Only a human `kubectl delete pod` moves the
  roll on.
- **Current (rows 2, 4, 5, 7).** No delete helps: a pod on the current template comes back
  identical. Only the observation can be bounded.

**Origin of the wait** (read, `91ca86d`, 2026-02-17, "feat: Rolling Updates"): on the loop over
outdated pods, `// If pod exists but isn't ready yet (was recently replaced), wait for it.` A
replaced pod matches the template by construction and is never outdated, so the stated reason
applies to no pod this wait has ever acted on. ADR 0026 later renamed the check to
`available()` and routed its terminating half to `terminationWait`; the readiness half is the
original check, unchanged.

### The Sentinel tier: the same class in three places

The ticket as filed excluded the Sentinel tier because "the Sentinel roll has no per-pod
availability wait". That is literally true and misses the equivalent:

- **Quorum wait** ([`:4614-4618`](../../../internal/controller/rolling_update.go)):
  `readyCount-1 < quorum` → `NeedsRequeue`, no bound. With a replaced Sentinel pod that stays
  unavailable (current), the next target is a healthy outdated pod whose delete would break
  quorum. The guard refuses correctly, and forever, and says nothing about the pod the roll is
  actually waiting for.
- **Completion hold** (`:4677-4680`): `SentinelUpdatePending=True` and
  `updatedReady < total` → `NeedsRequeue`, no bound. `handlePostRollingUpdateChecks` returns
  `done` on it ([`valkey_controller.go:408-410`](../../../internal/controller/valkey_controller.go)),
  so the pass ends before `updateStatus`. The same hold waits on a *terminating* Sentinel pod
  without going through `terminationWait`, contrary to ADR 0026 D5's statement that every wait
  on a terminating pod does.
- **No recovery after a spec fix.** Every Sentinel pod is then outdated, `firstOutdatedPod`
  (`:4547`) is the lowest ordinal — a healthy pod — and the guard charges the target against
  the quorum as `readyCount-1` even when the target is not in `readyCount`. The stuck pod,
  which costs no vote, is never selected. With three Sentinels the roll holds until a human
  deletes it.
- `handlePostRollingUpdateChecks` reads only `NeedsRequeue` of the Sentinel result (`:408`) and
  drops its `DeferredRequeueAfter`. A Sentinel-tier `PodTerminationStalled` pass therefore
  continues but schedules no recheck; the resolving event (the pod gone) changes the StatefulSet
  status and the StatefulSet watch re-enters, so what is lost is the cadence, not the recovery.

### What the CR says during the stall today

The filed text said "nothing on the CR says the roll stopped". That is too strong:

- The multi-replica paths write the phase `Rolling Update i/n` on every pass before dispatch
  ([`rolling_update.go:603-604`](../../../internal/controller/rolling_update.go), `:3531-3533`), so
  the phase is not `OK` and `ValkeyPhaseNotOK`
  ([`prometheusrule.yaml:73-84`](../../../deploy/helm/valkey-operator/templates/prometheusrule.yaml),
  `for: 30m`) fires after half an hour. The Sentinel tier shows `Sentinel Rolling Update i/n`
  and `SentinelUpdatePending=True` the same way.
- Missing are the pod's name, the reason, a bound and the rest of the pass. `updateStatus` never
  runs, so `Ready`, `readyReplicas` and `masterPod` keep their pre-roll values (the T18
  mechanism), and `ValkeyReplicasMissing` cannot fire because its series is the frozen
  `status.readyReplicas` ([`collector.go:195-196`](../../../internal/metrics/collector.go)).
- `ReconcileBlocked` is unaffected: it reports write refusals, evaluated in the resource step of
  every pass.
- A standalone cluster after a spec fix keeps whatever `updateStatus` last wrote, typically
  `Provisioning`.

### Found while planning: a data-tier stall releases the Sentinel roll

`reconcileWorkload` ends the pass only on `NeedsRequeue`
([`valkey_controller.go:334-336`](../../../internal/controller/valkey_controller.go)).
`DeferredRequeueAfter`, the stall shape of ADR 0026 D5 and ADR 0010 D16, continues the pass,
and the first thing the rest of the pass does is the Sentinel roll (`:397-410`). ADR 0026 D5
lists the Sentinel roll explicitly among what the stall shape buys back
([`0026:183`](../../adr/0026-a-pod-being-deleted-is-not-available.md)); ADR 0024 D1 and the comment
at `valkey_controller.go:347` ("only when no Valkey rolling update is active") say the Sentinel
tier rolls after the data tier.

The contradiction was dormant because the two existing stalls are rare and environmental — a
NotReady node, a wedged StatefulSet controller. This ticket's stall is the common one and is
often caused by the spec, and **`spec.image` is shared by both tiers**: a data replica sticks,
the budget expires, the Sentinel roll deletes a healthy sentinel-0, which returns on the broken
image and sticks too, and the quorum guard stops at 2/3 — the spare vote is gone, and one more
Sentinel loss (a chaos kill, a node) leaves no automatic failover. Today the Sentinel roll never
runs during a data roll, because the wait is unbounded, so only one replica breaks. **Bounding
the wait without addressing this makes the bad-image case worse on every Sentinel cluster**,
and T31's roll changes both tiers.

### Precedent and building blocks (read)

- **Upstream StatefulSet controller, default path** (`k8s.io/kubernetes@v1.36.4`,
  `pkg/controller/statefulset/stateful_set_control.go:713-731`): an outdated pod that is not
  terminating is deleted without an availability check; availability is waited for only on an
  updated pod. The `MaxUnavailableStatefulSet` path (`:697`, `:772`; Beta, default off since
  1.35 per `pkg/features/kube_features.go:1672-1675`) counts every unavailable pod and blocks —
  the shape this operator has. The data StatefulSet here is `OnDelete` + `Parallel`, so that
  loop never runs for it; the comparison is about the policy.
- **Kubelet clock:** `PodReady.lastTransitionTime` moves only when the condition's status
  changes, and after a kubelet restart the previous status is taken from the API object
  (`pkg/kubelet/status/status_manager.go:849-889`, `:1025-1037`). It is a clock nothing has to
  arm.
- **ADR 0010 already names this case** in its residual risks
  ([`0010:430`](../../adr/0010-every-rolling-update-wait-is-bounded.md)): "a pod-0 that does not
  match the template for any **other** reason, which still requeues unbounded."
- **Masters are never replica candidates.** `collectPodStates` counts a pod as master by its
  INFO answer or, when INFO fails, by its `instanceRole` label (`rolling_update.go:1776-1787`),
  and the sidecar labeler leaves the label untouched when it cannot read the role
  ([`labeler.go:124-128`](../../../internal/sidecar/labeler.go)).

### Inherited behaviour the plan carries over

- During a reported stall the pass writes the phase twice: `Rolling Update i/n` before
  dispatch, then `updateStatus`'s own verdict — `Provisioning` while a pod is not Ready
  (`valkey_controller.go:2167-2177`, `:2406-2418`). Two status writes per pass and an
  alternating phase for as long as the stall lasts. Already true for `PodTerminationStalled` and
  `PodRecreationStalled`.
- `terminationWait`'s message carries the overrun rounded to seconds
  (`rolling_update.go:1889-1891`), so a `PodTerminationStalled` pass rewrites the condition on
  every pass.

**Verified:** read at `9925539` — every site and line above, both status arms, the Prometheus
rule and the collector series it reads, the labeler's error path, commit `91ca86d`, and the
upstream sources (module cache, `k8s.io/kubernetes@v1.36.4`).
**Not verified:** nothing reproduced, and no test drives any of these stalls. The phase
alternation and the release of the Sentinel roll are traced by reading only. That kubelet keeps
`lastTransitionTime` across a restart is read from source, not measured on a node.

## Impact

Any replaced pod that cannot start: an unpullable image tag, a container OOM-killed at boot, a
broken config, an unschedulable resource request; after T31 also the pre-flight
`check-data-writable` refusing unrepairable data — NFS with `root_squash` is the known case. The
data plane is not harmed: the stuck pod is a replica, the only pod or a Sentinel, and nothing is
promoted onto it. What breaks is observability and progress: the status surface freezes, the CR
never names the pod, and after a spec fix the roll never resumes by itself. With the bound alone
and nothing else, a bad image would additionally cost a Sentinel and the spare vote (see above).

## Options

### Q1 — what the roll does with an outdated pod that is not available and not terminating

| | What | Cost |
|---|---|---|
| **A** (chosen) | Delete an outdated pod unless it is terminating (plus the ADR 0026 D5 tier gate); readiness is not asked. Upstream's default StatefulSet policy. | ADR 0026 D1 is reworded for the three delete sites. A freshly unready outdated pod is restarted at once instead of waited for — the same roll replaces it either way. |
| B | Replace it once it has been unavailable for longer than `syncTimeout` (the pod's own clock); `available()` stays the rule, with one named, bounded exception. | Same outcome as A in this ticket's scenarios, since the stuck pod's clock has long expired when the spec is fixed; a freshly unready outdated pod waits up to 5 min for a replacement that comes anyway; one more concept. |
| C | Never replace; bound and condition only, the message tells a human to delete the pod. | No rule change. Enough for T31's known failure (a current pod whose init container recovers once the storage is fixed), not for any spec fix. |
| dropped | Replace only a pod that has *never* been available — the filed framing. | Not observable through the API: kubelet keeps no history, and a list of waiting reasons is an enumeration that misses "OOM on the old template, the user raises the limit". |

### Q2 — the Sentinel tier

| | What | Cost |
|---|---|---|
| **Include** (chosen) | Target selection and quorum cost, a bounded quorum wait and completion hold, the Sentinel `DeferredRequeueAfter` applied. | Effort M → L; ADR 0024 and ADR 0026 D6/D8 are amended too. |
| T33 | A separate ticket; T31 blocked by it as well. | A second ADR and e2e pass for code three functions away from the data-tier fix. |
| Out | Adjacent finding only; T31's runbook handles a stuck Sentinel roll by hand. | A stuck Sentinel roll with a frozen status and no pod name during the first fleet-wide Sentinel roll. |

### Q3 — whether a holding data tier releases the Sentinel roll

| | What | Cost |
|---|---|---|
| **(a)** (chosen) | A holding data tier holds the Sentinel roll too, for all three stall conditions. | ADR 0026 D5 amended; a NotReady-node termination stall now also holds the Sentinel update, which is not urgent — the old Sentinels keep running. The README rows of the two sibling conditions change. |
| (b) | Only `PodAvailabilityStalled` holds the Sentinel roll. | Smallest change to decided behaviour, but two meanings of `DeferredRequeueAfter`. |
| (c) | ADR 0026 D5 unchanged. | The bad-image case on a Sentinel cluster costs a Sentinel and the spare vote. |

## Decision

Decided 2026-09-26 by Hans, one question at a time (Q1 A, Q2 include, Q3 (a)); D5 and D6 are
the defaults stated in the same round and not objected to.

- **D1 — Outdated pods are replaced, not waited for.** Deleting an outdated pod asks only
  whether it is being deleted (`terminationWait`) and whether any pod of its tier is (the ADR
  0026 D5 gate). Readiness is no longer asked at the three delete sites: the standalone delete,
  `replaceNextReplica`, `replaceRemainingPods`. The delete spends nothing: the roll replaces the
  pod anyway, the PVC survives a pod delete, a replica re-syncs from its master, masters are
  never candidates, and the one pod holding the only dataset — a single pod without persistence
  — is deleted by the same roll the moment it turns Ready. The only recorded reason for the wait
  (`91ca86d`) applies to no outdated pod. **Unchanged:** promotion, quorum and completion keep
  counting `available()`; `deleteNextPendingPod` (leftover second masters) keeps `available()`;
  a pod on the current template is never deleted. This is not T31's rejected M2 exception —
  that would have deleted a current pod.
- **D2 — Every remaining wait on a pod that exists, is not terminating and is not available is
  a bounded observation.** Budget: `spec.rollingUpdate.syncTimeout`, which ADR 0010 D6 already
  uses for the same wait (default 5 min, user-raisable). Clock: the pod's own —
  `PodReady.lastTransitionTime` while Ready is not True, `creationTimestamp` when the pod has no
  Ready condition yet (Pending). No annotation, no in-memory tracker, no CR write; per pod and
  restart-proof, the ADR 0026 D5 argument. Within the budget: the plain requeue of today. Past
  it: `DeferredRequeueAfter` and `PodAvailabilityStalled`. Applies to rows 2, 4, 5 and 7, and to
  the Sentinel waits of D3.
- **D3 — The Sentinel tier is in scope.** An outdated Sentinel pod that is not available and not
  terminating is the delete target ahead of `firstOutdatedPod`, and the quorum guard charges the
  target only when it is available — otherwise it is not in `readyCount` to begin with. The
  quorum wait and the completion hold route a current, non-terminating, unavailable pod through
  the D2 wait and a terminating one through `terminationWait`. `handlePostRollingUpdateChecks`
  applies the Sentinel result's `DeferredRequeueAfter` instead of dropping it.
- **D4 — A holding data tier holds the Sentinel roll, for all three stall conditions.** A
  data-tier `DeferredRequeueAfter` no longer releases `checkAndHandleSentinelRollingUpdate`. The
  stall shape buys back the status write, the no-master recovery and the steady-state
  split-brain check — on a Sentinel cluster the status write alone. Amends ADR 0026 D5; ADR 0024
  D1 holds without exception again. It changes `PodTerminationStalled` and
  `PodRecreationStalled` on Sentinel clusters: their Sentinel update now waits for the data tier.
- **D5 — Reporting.** New condition `PodAvailabilityStalled`, a **level**:
  - True reasons `ValkeyPodNotAvailable` and `SentinelPodNotAvailable` — the tier is in the
    reason because two evaluators write the condition — and False reason `PodAvailable`, written
    only over a standing True of the same tier, never onto a CR that did not carry it.
  - Two evaluators with an ownership rule: each tier's roll reports or retracts its own reason
    on every pass that reaches it. The data tier's runs first, and under D4 the Sentinel tier's
    runs only in a pass the data tier neither ended nor held, so the two never contend within a
    pass. Class exit (Sentinel disabled) retracts a Sentinel report, next to
    `clearSentinelUpdatePending`.
  - The message is stable across passes: it names the pod and the timestamp it has been
    unavailable since, never a running duration.
  - No Event for the stall, as for both siblings; the condition is alertable through
    `vko_valkey_status_condition` ([ADR 0021](../../adr/0021-per-resource-metrics-and-the-alert-that-was-missing.md)).
    A D1 delete of an unavailable replica keeps the existing Normal `RollingUpdate` Event, with a
    message saying the pod was not available. No new PrometheusRule alert: `ValkeyPhaseNotOK`
    already covers a stall after 30 minutes.
- **D6 — Recorded as amendments, not a new ADR** (the T10 precedent, ADR 0010 D16): ADR 0010
  gains D17, and D16's list of what the stall shape buys back loses the Sentinel roll; its
  residual-risk bullet at `0010:430` closes. ADR 0026 gains D11, with D1 reworded, D5's list
  amended and D6/D8 extended. ADR 0024 records the bounded completion hold and the terminating
  pod routed through `terminationWait`. ADR 0007 D9's second half follows D1. T31's planned ADR
  number 0032 stays free.

## Plan

Handover for the implementation, in this order. Every step lands in one change with its docs.

1. **API** — [`api/v1/valkey_types.go`](../../../api/v1/valkey_types.go): the condition type, the
   three reasons, a type comment in the style of its siblings. Amend the comments of
   `PodTerminationStalled` (`:148-162`) and `PodRecreationStalled` (`:164-173`), which list the
   Sentinel roll among what keeps running (D4). No CRD schema change: conditions are
   `[]metav1.Condition`.
2. **Data tier** — [`rolling_update.go`](../../../internal/controller/rolling_update.go):
   1. `podState` gains `notReadySince time.Time`, filled in `collectPodStates` and in
      `standaloneWait`'s `podState` from a helper: Ready condition present and not True with a
      non-zero `lastTransitionTime` → that time; no Ready condition, or a zero time →
      `creationTimestamp`; Ready True → zero. Read as a field, never through `ps.pod` — fixtures
      pass `pod: nil` (the ADR 0026 fixture trap).
   2. `availabilityWait(ctx, v, tier, pod, since, what)`: zero `since`, or within
      `v.GetSyncTimeout()` → `NeedsRequeue`; past → `DeferredRequeueAfter` plus an unexported
      `RollingUpdateResult` field naming the pod and the time. It writes no condition itself;
      the tier evaluator does (D5).
   3. `waitForUnavailablePod`: terminating → `terminationWait`, otherwise `availabilityWait`.
   4. D1 at the three delete sites: `!ps.available()` becomes `ps.terminating` →
      `terminationWait`; the standalone delete checks `pod.DeletionTimestamp`. Log whether the
      deleted pod was available, and let `replaceNextReplica`'s Event message say so.
   5. `waitForReplicasReady`: split the predicate — an available but outdated pod (a second
      master, not this ticket's class) keeps the plain requeue; `!available()` goes to
      `waitForUnavailablePod`.
   6. `replaceRemainingPods` fall-through: the first existing pod that is not available →
      `waitForUnavailablePod`; the plain requeue stays for the case that really should not
      happen.
   7. Data evaluator: `checkAndHandleRollingUpdate` becomes a thin wrapper around today's body,
      renamed, and reports or retracts the data-tier report on every non-error result. The body
      sits at the gocyclo ceiling (the reason `readyOne` exists, `:552-561`), so the evaluation
      must not go inside it.
3. **Sentinel tier** — `scanSentinelPods` gains the first unavailable outdated pod and the first
   unavailable current pod with its `notReadySince`. Target selection and quorum cost (D3). The
   quorum wait and the completion hold share one helper: terminating → `terminationWait` (tier
   `sentinel`), current and unavailable → `availabilityWait`, otherwise the plain requeue.
   `finishSentinelRollingUpdate` takes the scan instead of two counters. Sentinel evaluator: a
   wrapper around `checkAndHandleSentinelRollingUpdate`, the same shape as 2.7.
4. **Controller** — [`valkey_controller.go`](../../../internal/controller/valkey_controller.go):
   `reconcileWorkload` passes `rollingResult.DeferredRequeueAfter > 0` to
   `handlePostRollingUpdateChecks` (D4), which then skips the Sentinel roll, merges the Sentinel
   `DeferredRequeueAfter` with the split-brain check's pending result (the sooner wins), and on
   the non-Sentinel branch retracts a Sentinel report. Mind gocyclo; the other callers of the
   function are tests.
5. **Registry** — [`condition_registry.go`](../../../internal/controller/condition_registry.go):
   one row — level, `evaluators: 2`, the ownership rule of D5, clear site "each tier's
   evaluator on every pass that reaches it; the Sentinel report also on class exit",
   `presenceGuarded: true`.
6. **Docs, same change** — the ADR amendments of D6, each with its superseded sentence marked in
   place and a dated Status entry; [ADR 0027](../../adr/0027-conditions-are-levels-edges-or-history.md)
   if its text enumerates the levels that carry an ownership rule (so far only
   `StorageSpecNotApplied`); README's condition table — a new row, and the
   `PodTerminationStalled`/`PodRecreationStalled` rows (`README.md:959`, `:962`) lose the
   Sentinel roll from what keeps running; CLAUDE.md's "A pod being deleted is not available"
   (the delete rule and the list of what a stall buys back); DEVELOPER.md wherever it describes
   the rolling-update waits (**there is no DEVELOPER.md in this repository**, so nothing to
   amend; ADR 0026 D11, ADR 0010 D17, README and CLAUDE.md carry it); T31's Fact bullet on T32
   once this is done.

## Implementation notes (2026-09-26)

Implemented as planned, then reviewed adversarially (four lenses, every finding put to three
refuters, 56 agents). What the review changed beyond the Plan, each with its guard test:

- **The quorum guard charges only a delete that spends a vote** (`cost > 0 && readyCount-cost <
  quorum`). D3 as written refused a zero-cost delete once the quorum was already lost — two of
  three Sentinels stuck on a broken spec after a fix, readyCount 1 — and so never recovered.
  `TestSentinelRollingUpdate_ReplacesANonVotingPodWhenQuorumIsAlreadyLost`.
- **The condition is retracted on evidence only** (`expiredUnavailablePod`): a pass that stopped
  at another wait first — a terminating pod ahead in `sentinelWait`, the no-replicas wait after a
  failover — no longer writes `False` while the pod is still down. Before, it flapped on a fixed
  cycle in the ticket's own main scenario. `TestReportAvailabilityStall_RetractsOnlyOnEvidence`,
  `TestSentinelRollingUpdate_TerminationPriorityDoesNotRetractTheReport`.
- **The clock survives scheduling**: a `Ready=False` kubelet stamped at its first status sync
  (`stampedAtFirstSync`, within 5 s of `status.startTime`) means never Ready, so a pod Pending
  longer than the budget keeps its creation clock. `TestPodNotReadySince_FirstSyncIsNotATransition`.
- **The Sentinel scan names the longest-unavailable pod**, not the lowest ordinal.
  `TestSentinelRollingUpdate_ReportsTheLongestUnavailablePod`.
- Test hardening: the D4 tests no longer let the nudge supply the requeue they assert; the
  `sentinelWait` clear, `standaloneWait`'s clock and the `replaceRemainingPods` terminating
  check each got a test that fails without them; the e2e image revert retries on conflict.

Recorded, not changed: a paused data roll (`pauseRollingUpdate` returns no requeue) releases the
Sentinel roll in the pass that pauses — D4 names the three stall conditions, and the pause is not
one of them (ADR 0026 D11 residual risks). One condition for two tiers: a data report overwrites
a standing Sentinel report, which comes back on the first Sentinel pass after the data tier
finishes.

## Verification

Done when every line holds, with the command and date recorded here:

- [x] Unit, each guard with a mutation check (revert the line, the test fails):
  - the `notReadySince` helper: Ready False with a time, no Ready condition, Ready False with a
    zero time, Ready True;
  - `availabilityWait`: within the budget → `NeedsRequeue` and no stall; past it →
    `DeferredRequeueAfter` and a stall; zero `since` → `NeedsRequeue`;
  - D1: `replaceNextReplica` deletes an outdated, not-Ready, non-terminating `candidates[0]`
    (mutation: restore `!ps.available()`) and still waits on a terminating one; the same pair
    for `replaceRemainingPods` and the standalone handler. The ADR 0026 fixture trap applies:
    only the pod meant to be terminating carries a finalizer;
  - D2: `verifyReplacedReplicasSynced` and the `replaceRemainingPods` fall-through report past
    the budget; the data evaluator writes True naming the pod and retracts it to False once the
    pod is Ready; a CR that never stalled never gains the condition; an error result leaves it
    as it is;
  - D3: three Sentinels, all outdated, one not Ready → that one is deleted although
    `readyCount-1 < quorum` (mutation: select `firstOutdatedPod`); three Sentinels with the
    unready one current → the healthy target is still refused, so the guard is intact; the
    completion hold past the budget → `SentinelPodNotAvailable`; a terminating pod in the hold →
    `terminationWait`; the Sentinel `DeferredRequeueAfter` reaches the pass result;
  - D4: a data-tier `DeferredRequeueAfter` deletes no Sentinel pod in that pass (mutation: drop
    the flag); the data evaluator never retracts a Sentinel report; the class exit does;
  - existing tests that assert the superseded wait on an outdated unavailable pod are rewritten
    to the new rule, not deleted ([ADR 0017](../../adr/0017-test-and-ci-policy.md) D18).

  **Done 2026-09-26.** `make test-unit` green. 25 mutation checks against the T32 guards, each run
  as a full `make test-unit` in an isolated copy of the tree (scratchpad script
  `mutate.py`): all killed — the 16 of the Plan (the three D1 delete sites, the unbounded
  availability wait, the fall-through, target selection and quorum cost, the D4 flag, the Sentinel
  deferred requeue, the own-reason rule, the class exit, the `sentinelWait` termination routing and
  its completion clear, the clock, the data evaluator, the error guard) and 9 for the review fixes
  above. One guard is behaviour-neutral and therefore has no mutation check: the split in
  `waitForReplicasReady`, because an available pod carries no clock. Rewritten rather than
  deleted: `TestReplaceNextReplica_ReplacesACandidateThatIsNotReady`,
  `TestReplaceRemainingPods_ReplacesAnOutdatedPodThatIsNotReady`,
  `TestHandleStandaloneRollingUpdate_ReplacesAnOutdatedPodThatIsNotReady`,
  `TestReconcileWorkload_StalledTerminationHoldsTheSentinelRoll`, and the two Sentinel quorum
  tests, whose unready pod is now current.
- [x] E2E, one new test on a 3+3 Sentinel cluster, both e2e legs. `syncTimeout` around 60 s,
  two-sided like `topologyAbandonSyncTimeout` in
  [`topology_abandon_test.go`](../../../test/e2e/topology_abandon_test.go). Write keys; set
  `spec.image` to an unpullable reference → `PodAvailabilityStalled=True/ValkeyPodNotAvailable`
  naming the stuck replica within the budget plus a margin; the Sentinel pods' UIDs stay
  unchanged throughout (D4); put the image back → the operator replaces the stuck pod (its UID
  changes, the test deletes nothing), the phase returns to `OK`, the condition reads
  `False/PodAvailable`, and the keys are on every replica.

  `TestE2E_RollingUpdate_UnavailableReplacementIsReportedAndReplaced`, local Kind (control plane
  + 3 workers, Kubernetes v1.36.1), image `localhost:1/vko-e2e/unpullable:0`. **valkey 9.1.1,
  2026-09-26: passed** in the targeted run and again in the full suite (`make test-e2e
  E2E_VALKEY_LINE=9`, 51 tests, 588 s, all green): the condition named the stuck replica 64-66 s
  after the image change, the Sentinel UIDs held for 30 s, the operator replaced the stuck pod
  within 12 s of the image going back, 100 keys on every replica. **valkey 8.1.9, 2026-09-26: passed** in the full suite (`make test-e2e E2E_VALKEY_LINE=8`, 51
  tests, 536 s, all green; report after 64 s, replacement 7 s after the revert). Re-run on the
  final image against valkey 9.1.1 after the review fixes: passed (report after 62 s). Not run in
  CI — the branch has not been pushed through the pipeline yet.
- [x] Not an e2e, stated here and in ADR 0024: the Sentinel-tier half. The image is shared with
  the data tier, whose stall now holds the Sentinel roll (D4), and the CR has no Sentinel-only
  field that could make a Sentinel pod unschedulable or unable to start — `SentinelSpec` has
  exactly `enabled`, `replicas`, `podLabels`, `podAnnotations`, `allowUnencrypted` and
  `disableAuth`. Unit coverage only.
- [x] `make test-unit`, `make test-integration`, `make lint`, `make cyclo` green; the doc
  amendments of step 6 are in the same change. All four green on 2026-09-26 on the final tree;
  the ADRs (0026 D11, 0010 D17, 0024, 0007 D9, 0027), README and CLAUDE.md amended in the same
  commit.

## Adjacent findings

Not in scope and not filed; each was read at `9925539`.

- **The Sentinel quorum wait and completion hold wait unbounded on a *missing* Sentinel pod**
  (NotFound → skipped by the scan): T10's class (ADR 0010 D16) on the Sentinel tier. D3 bounds
  only an existing pod.
- **Two data replicas down at once.** `sortReplicaCandidates` orders youngest-first (`:2182-2203`)
  and `verifyReplacedReplicasSynced` checks only replaced pods, so an outdated replica that is
  unavailable but not `candidates[0]` does not stop the delete of a healthy candidate.
  Pre-existing; D1 neither causes nor fixes it.
- **A two-replica non-Sentinel cluster whose master is pod-1 cannot finish any later roll.**
  `findPromotionCandidate` (`:3776-3784`) skips pod-0 unconditionally, because the rest of the
  manual-failover state machine hardcodes pod-0 as the permanent master. With two replicas and
  the master on pod-1, pod-0 is the only other pod, so the candidate is `-1` and
  `handleManualFailover` requeues "No ready updated replica available for promotion" forever
  (`:3618-3622`), after pod-0 has already been replaced. A master on pod-1 is a supported end
  state — an abandoned topology restoration (ADR 0010 D3) or a drain promotion (ADR 0012)
  leaves one — so the trigger is reachable in released code. Traced by reading only; probably
  above the filing bar, and not T32's class.
- **Unbounded requeues of another class** — a master or a promotion step that does not answer,
  where ADR 0010's "every rolling-update wait is bounded" does not hold either:
  `waitForWriteSync` on a `WAIT` or TLS error (`:2598-2614`); "No master detected during rolling
  update" (`:639-642`, `:3569-3572`); `promoteAndRedirect` failing (`:3634-3637`); the waits
  inside `verifyNewMasterReady` (`:2982-3007`, `:3024-3025`); the `deleteNextPendingPod`
  fall-through (`:3599`).
- **A Sentinel tier with one or two Sentinels can never roll a Ready outdated Sentinel**
  (found in the 2026-09-26 review, verified by reading). Quorum is `replicas/2+1`, which equals
  the replica count at 1 and 2, so `readyCount-1 < quorum` refuses every delete that spends a
  vote, and `sentinelWait` has nothing to name — the plain requeue, unbounded, status frozen.
  Pre-existing for any Sentinel spec change and for TLS rotation (ADR 0030); T31 rolls every
  Sentinel tier once, so such a cluster would hit it at the upgrade. wds18 has only 3-Sentinel
  tiers (checked read-only 2026-09-26). **Decided by Hans 2026-09-26: such a tier rolls
  serially** — one Sentinel at a time, only while every other one is available
  (`sentinelDeleteKeepsVotes`, ADR 0024 D10). Unit-tested and mutation-checked
  (`TestSentinelRollingUpdate_SmallTiersRollSerially`, `TestSentinelDeleteKeepsVotes`), e2e
  `TestE2E_RollingUpdate_TwoSentinelsRollSerially`.
- **`verifyNewMasterReady` reads the new master's DBSIZE and does not refuse on it**, although its
  comment calls it a critical safety check. Pre-existing; T32's D1 comment in
  `replaceRemainingPods` used to claim a key check and was corrected.
- The two inherited behaviours under Fact — the alternating phase during a reported stall, and
  `terminationWait`'s message rewriting the condition on every pass. D2 inherits the first;
  D5's stable message avoids the second for the new condition only.

## History

- 2026-09-27 — **archived** as `archive/032-unavailable-replaced-pod-waits-unbounded.md` (was `local_T32-unavailable-replaced-pod-waits-unbounded.md`) when the tickets were numbered; state `done` unchanged.
- 2026-09-26 — **implemented and done** on `feat/rootless` together with T31, state `decided` →
  `done`. The review round changed four things beyond the Plan (Implementation notes) and found
  two gaps outside the decision, recorded under Adjacent findings: a Sentinel tier of one or two
  Sentinels can never roll a Ready outdated Sentinel (asked, and decided the same day: serial
  roll, ADR 0024 D10), and `verifyNewMasterReady` does not refuse on the DBSIZE it reads (open,
  pre-existing).
- 2026-09-26 — **analysed and decided** by Hans in a question round, one question at a time;
  state `filed` → `decided`, effort M → L (the Sentinel tier and the D4 amendment were added).
  Q1 A, Q2 include, Q3 (a). Q3 was not in the filed scope: it surfaced while planning the
  implementation, because a bound makes ADR 0026 D5's stall shape the common case. The four
  items of the filed "Scope for the analysis" are answered by D1 (replacement), D2 and D5 (bound,
  condition, registry row) and D5 (Event policy). Corrected in the Fact: the filed "Five call
  sites" table is now seven rows; "nothing on the CR says the roll stopped" was too strong;
  "the Sentinel roll has no per-pod availability wait" was true and missed the same class; the
  open question whether `Ready` or `ReconcileBlocked` say anything is answered by reading.
  Implementation deliberately not started, on Hans's instruction to document the ticket only.
- 2026-09-26 — filed from the T31 analysis; made a prerequisite of T31's release by Hans the
  same day.
