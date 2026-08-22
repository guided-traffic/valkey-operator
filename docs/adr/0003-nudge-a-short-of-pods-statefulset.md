# ADR 0003: Nudge a Short-of-Pods StatefulSet

## Status

Accepted. Date: 2026-08-21.

Implemented on branch `feat/support-pdb` as `2d1a133`, `396e46c`, `dedbf45`, `06a4a9f`
and `e6ba971`, not yet released — verified in this repository: no tag contains those
commits and no branch other than `feat/support-pdb` does. Verified end to end on a Kind
cluster: after a blocked-pod-creation window, all three data pods were recreated
**9.02 s** after the webhook was removed, against **5 min 29 s** in the incident that
motivated it. Both numbers were measured on a cluster and are not reproducible here —
`TestE2E_AdmissionRejection_StatefulSetNudgeRecovery`
([`test/e2e/admission_recovery_test.go`](../../test/e2e/admission_recovery_test.go))
asserts the 60 s `admissionRecoveryDeadline` and logs the elapsed time; it does not
pin either figure.

## Context

Both StatefulSets this operator manages use `updateStrategy: OnDelete` and
`podManagementPolicy: Parallel` — the data set in
[`internal/builder/statefulset.go`](../../internal/builder/statefulset.go), Sentinel in
[`internal/builder/sentinel.go`](../../internal/builder/sentinel.go) — so pod
creation is entirely the statefulset-controller's job. The operator's only lever over
that controller is writing the StatefulSet object.

In the 2026-08-19 infra-d incident the statefulset-controller's creates were rejected
by a fail-closed Kyverno webhook whose backend was temporarily gone. Nothing then woke
it except its own `ItemExponentialFailureRateLimiter`. Every incident figure and
observation below was taken off the affected cluster and is not reproducible from this
repository:

* the StatefulSet object was not written during the window (`managedFields` shows the
  last operator spec write an hour earlier), so no informer event came from the object;
* zero pods existed, so there were no pod events either;
* Flux reconciled the owning Kustomization every minute, but the `Valkey` CR was
  unchanged, so nothing propagated down.

Measured wait: 13:52:58 → 13:58:27 = **5 min 29 s**, matching the default limiter
after 16 consecutive failures (5 ms · 2¹⁶ = 5 min 28 s). Net effect: ~7 min with zero
Valkey data pods, of which 5 min were pure kube-controller retry delay after the cause
was already gone. The whole SSO login path of the cluster was down for that window.

The first implementation of the fix landed **inert**, and that shaped most of the
decisions below. That much is readable from this repository: `2d1a133` added the nudge
with neither a requeue of its own nor a call position ahead of the rolling-update
checks; `396e46c` added the requeue, `dedbf45` moved the call ahead of those checks and
narrowed the suppression, and `06a4a9f` removed the rest of it. The evidence that it
was inert in a running cluster is a measurement taken on one, not reproducible here:
the data StatefulSet sat at `status.replicas=0 / spec.replicas=3` for 5 min 03 s across
98 samples with `metadata.resourceVersion` constant at 26322 — zero operator writes,
the nudge annotation never set, zero "Nudged StatefulSet" log lines.

## Decision

**D1 — A short-of-pods StatefulSet is nudged with a metadata annotation.** When a
managed StatefulSet reports `status.replicas < spec.replicas` for longer than
`nudgeGracePeriod` (10 s), the operator patches
`vko.gtrfc.com/nudge: <RFC3339>` (`builder.AnnotationNudge`) as a JSON merge patch on
the object metadata. Writing it changes the `resourceVersion`, which the
statefulset-controller's informer turns into an immediate sync instead of waiting out
its backoff. Code: [`internal/controller/nudge.go`](../../internal/controller/nudge.go)
(`nudgeShortStatefulSets`, `nudgeStatefulSet`, the in-memory `nudgeTracker`), helpers in
[`internal/builder/annotations.go`](../../internal/builder/annotations.go).

**D2 — The annotation lives on StatefulSet object metadata, never on
`spec.template`.** It must stay invisible to `StatefulSetHasChanged`,
`SentinelStatefulSetHasChanged`, `podTemplateChanged`, `OperatorVersionChanged` and
`ComputePodSpecHash`. On the pod template it would flip the pod-spec hash and start a
failover-aware rolling update every 20 s on exactly the cluster that is already short of
pods — the operator restarting the cluster because it tried to unstick it. The five are
not equally exposed, and the guarantee is pinned accordingly.
`TestNudgeAnnotation_DoesNotTriggerDriftDetection`
([`internal/builder/annotations_test.go`](../../internal/builder/annotations_test.go))
asserts the three that read the live object: `StatefulSetHasChanged`,
`SentinelStatefulSetHasChanged` and `OperatorVersionChanged` — the last of which does
read object-metadata annotations, which is precisely where the nudge lives. The other
two the test never calls; they are safe by construction rather than by assertion:
`podTemplateChanged` is reached only through the two `HasChanged` functions and
compares `spec.template` alone, and `ComputePodSpecHash` takes the CR plus the operator
image and never sees a StatefulSet at all.
`reconcileStatefulSet` correspondingly assigns only `Spec.Replicas`, `Spec.Template`
and `Labels` onto the live object, so an operator write never erases the annotation —
which matters because the annotation value *is* the rate-limit state.

**D3 — The annotation value doubles as the rate limit.** It is re-bumped only when
older than `builder.NudgeInterval` (20 s), via `builder.NudgeDue` / `builder.NudgePatch`.
No CRD field and no extra in-cluster state.

**D4 — `NudgeDue` fails open.** An absent, unparsable or future timestamp reads as due.
A corrupted or hand-edited value can delay a nudge by at most one interval and can never
disable the recovery permanently. Clock skew produces an extra nudge rather than a
suppressed one — the accepted direction of error.

**D5 — The nudge has its own requeue clock, independent of the CR phase.**
`reconcileWorkload` requeues after `nudgeRequeueInterval` (5 s) whenever
`nudgeShortStatefulSets` reports a StatefulSet short of pods, regardless of
`status.phase`. The phase-based requeue (10 s for `Error`/`Syncing`) is **not** the
nudge's clock: a cluster whose pod creates are rejected reports `Provisioning`, which is
not in that set — and nothing else re-enters `Reconcile` in that state, because the CR
watch is `GenerationChangedPredicate`-gated, the StatefulSet is not written so `Owns()`
fires nothing, and with zero pods there are no pod events. Since the nudge needs two
passes (the first only records the observation), the grace period was unreachable by
construction. **Any future recovery mechanism that needs more than one pass must carry
its own wakeup source rather than inherit one.**

**D6 — `nudgeRequeueInterval` (5 s) stays strictly shorter than `nudgeGracePeriod`
(10 s), and `grace + interval <= 30 s`.** The first bump must land one requeue *after*
the short state is first observed; equal values would put the second pass exactly on the
boundary and make the first bump a race against clock resolution. The 30 s ceiling is
T1's recovery target ("pods return within 30 s of the cause disappearing"), which comes
from the admission-gap ticket and is not in this repository; its in-repo trace is the
`admissionRecoveryDeadline` doc comment in
[`test/e2e/admission_recovery_test.go`](../../test/e2e/admission_recovery_test.go),
which derives the same 30 s from `nudgeGracePeriod` plus `NudgeInterval` and then adds
scheduling headroom to reach its 60 s assertion. A 30 s re-bump interval, as first
sketched, would consume the entire budget.

**D7 — The nudge is called first in `reconcileWorkload`, before both rolling-update
checks, and its result is read last.** `shortOfPods := r.nudgeShortStatefulSets(...)`
precedes every call in the function — only the `logger` binding sits above it — and
nothing that can return early may be inserted before it. The
`shortOfPods` value is read at the very end, after every rolling-update return, so the
rolling update keeps requeue authority and the 5 s clock can never preempt the 10 s
`rollingUpdateRequeueDelay`. Both halves are load-bearing and both are pinned
(`TestReconcileWorkload_NudgesDataStatefulSetDuringSentinelQuorumWait`,
`TestReconcileWorkload_RollingUpdateKeepsRequeueAuthority`).

**D8 — No rolling-update suppression, for either StatefulSet. The discriminator is
duration, not state.** Every rolling-update delete site requeues and then blocks on the
pod it deleted coming back, so a blocked recreation *during* a rolling update is
precisely when the nudge is the only lever — and precisely when the original design
switched it off. Three sites say so literally, with a "waiting for the pod to be
recreated" branch: `replaceNextReplica`, `replaceRemainingPods` and
`handleStandaloneRollingUpdate`. `deleteNextPendingPod` has no such branch — it skips a
pod that does not exist and falls through to a bare terminal requeue — and the Sentinel
path skips a missing pod so that its quorum guard blocks the next delete. Same net
effect, different shape; the argument rests on the requeue, not on the log line. The
`vko.gtrfc.com/rolling-update-state` annotation is a phase marker, not a liveness
marker: it is set before the delete and cannot be cleared while the pod
is missing, so keying suppression on it suppressed the nudge for the entire duration of
the stall. `nudgeGracePeriod` already measures the only thing that separates an
intentional delete from a stuck one.

**D9 — Shortness is measured by created pods (`status.replicas`), not by readiness.**
A pod that exists but is not ready is the kubelet's business and no nudge helps there.
This is also what bounds nudge noise on the healthy path: a recreated pod clears the
short state within seconds of creation, long before readiness, while a bump needs the
short state to survive 10 s across two passes — so a healthy recreation never reaches a
bump and the mechanism writes nothing at all in the common case.

**D10 — Every non-bumping path still reports "short"; unknown is not recovered.**
`nudgeStatefulSet` returns true inside the grace period, inside the rate limit, on a
failed patch, and on any Get error other than `IsNotFound`. Only `IsNotFound` and
`status.replicas >= desired` return false and forget the observation. In `Provisioning`
the nudge requeue is the only wakeup source, so treating a transient read error as "no
longer short" would both reset the grace period and end the requeue chain — parking
exactly the stall the nudge exists to break.

**D11 — Nudge failures are logged and swallowed.** A nudge is a best-effort accelerator
against a third-party controller's backoff; failing the pass on it would convert an
optional acceleration into an outage of the whole reconcile.

**D12 — Grace-period observations are forgotten at exactly three sites**, symmetric for
both StatefulSets: `nudgeStatefulSet` on a NotFound Get, `nudgeStatefulSet` when
`status.replicas >= desired`, and `forgetNudges` on CR deletion. The per-pass
`forget(dataKey)` that ran on every rolling-update pass is gone (added in `2d1a133`,
removed in `06a4a9f`) — it made the old suppression total rather than merely delayed,
because `observe` always returned `now` and the grace period could never be crossed.

**D13 — The nudge is unbounded.** For a StatefulSet that stays short indefinitely
(quota exhausted, missing PVC, permanently broken webhook) it repeats every 20 s
forever, and the 5 s poll runs at a flat cadence with no backoff. The operator cannot
distinguish "stuck forever" from "stuck until the webhook comes back", and giving up
would reintroduce the multi-minute tail the mechanism exists to remove.

## Consequences

* No RBAC or manifest change was needed: `patch` on `apps/statefulsets` was already in
  both the generated role and the Helm ClusterRole.
* A CR that stays short of pods reconciles every 5 s for as long as that lasts
  (before `396e46c`: not at all). Bounded per affected CR, stops the moment
  `status.replicas` reaches `spec.replicas`. The 5 s cadence is the bound, not a probe
  timeout: while a StatefulSet is short of pods `updateStatus` takes a `Provisioning`
  branch and performs no Valkey probe at all — `verifyValkeyConnectivity` runs only under
  `readyReplicas == spec.replicas`, which a short pass cannot reach. The per-pass cost is a
  cached List plus a few Gets.
* The operator may nudge a StatefulSet whose pods it deleted on purpose. Cost: one
  metadata patch and one statefulset-controller sync. Worst case, with a pod ignoring
  SIGTERM for its full termination grace period, at one bump per `NudgeInterval` after
  the grace period: four patches for a data pod (75 s,
  [`internal/builder/statefulset.go`](../../internal/builder/statefulset.go)) and two
  for a Sentinel pod (30 s,
  [`internal/builder/sentinel.go`](../../internal/builder/sentinel.go)). Under
  `OnDelete` + `Parallel`, creating a missing ordinal is unconditional and ordinal
  naming makes a duplicate impossible, so a nudge can only accelerate a recreation the
  statefulset-controller already owes.
* Losing the in-memory `nudgeTracker` on operator restart is harmless — it delays the
  first bump by one grace period.
* Any new early return in the rolling-update path sits *after* the nudge and therefore
  cannot make it dormant again; any reordering of the tail of `reconcileWorkload` must
  keep the rolling-update return above the `shortOfPods` read.
* A grace-period observation now carries across the rolling-update boundary instead of
  restarting the clock — a deliberate side effect of D12, strictly better.
* Nudge malfunctions are visible only in logs, not in CR status. The requeue return
  value is the only in-band signal, which is why D10 has to keep it truthful.

## Alternatives Considered

### Wait for the statefulset-controller's own backoff

The status quo that produced the 5-minute tail. Rejected.

### Bump something inside the pod template to force a resync

Would work as a wakeup, and would trigger a failover-aware rolling update on every
nudge. Rejected — see D2.

### A new CRD field or in-cluster object to track nudges

Unnecessary: the annotation value carries the rate-limit state itself.

### Key the nudge on `status.readyReplicas`

Would nudge on ordinary readiness lag and make the noise bound unprovable. Rejected.

### Rely on the existing phase-based 10 s requeue

WP1's stated assumption — from the admission-gap ticket, which is not in this
repository — and false for `Provisioning`, the phase of the exact failure mode the
nudge targets. The falsification is in-repo: the phase-based requeue at the end of
`reconcileWorkload` covers `Error` and `Syncing` only. This is why the first
implementation was inert.

### Suppress the nudge while a rolling update is in progress

The original design. Rejected after two rounds, both in this repository's history:
`dedbf45` narrowed it to the data StatefulSet only, `06a4a9f` removed it entirely. The
state set that fixes the problem is the empty set — suppressing in the delete-states
(`replacing-replicas`, `replacing-master`, `manual-failover`) reproduces the defect
unfixed: those are the states in which the operator deleted a pod and then blocks on
that exact pod coming back. It is also the half `waitForReplicasReady`
([`internal/controller/rolling_update.go`](../../internal/controller/rolling_update.go))
belongs to: it is unreachable in the four states of the inverse set below — the Sentinel
path routes `failover-reset` and `failover-triggered` away before `handleMasterFailover`,
which returns early on both anyway, and `dispatchMultiReplicaState` routes
`restoring-topology` and `verifying-topology` away before `handleManualFailover`. It
therefore runs under `replacing-replicas`/empty state, where it requeues on a not-ready
replica with no elapsed-time bound of its own. The literal inverse
(`failover-triggered`, `failover-reset`, `restoring-topology`, `verifying-topology`) is
worse still: there an absent pod is unintentional — nothing in those states deleted it —
so the nudge is the only thing that recovers it. Two of the four do end on their own,
`restoring-topology` on `spec.rollingUpdate.syncTimeout` and `verifying-topology` on
`finalizationStallTimeout` (2 m), but those bounds end the phase by abandoning the
topology, not by producing the missing pod.

### Plumb `nudgeRequeueInterval` through the rolling-update early returns

No benefit: both quorum paths already return 10 s, shorter than the 20 s nudge interval,
so a nudge could not become due any sooner. It would only give one path two competing
clocks.

### Cap the nudge retries or back the poll off

Rejected: it trades the recovery guarantee for idle cost that is already bounded and
small.

### Fail closed on an unparsable nudge timestamp

Rejected: one bad annotation value would disable the recovery permanently.

## Residual risks

* **Perpetual low-rate API churn on a permanently broken cluster** — one metadata patch
  per 20 s plus a 5 s reconcile poll per affected CR. Named and accepted (D13).
* **Under a sustained admission block the effective nudge cadence stretches from 5 s
  toward the 30 s backoff cap**, because the blocked path returns the error and
  controller-runtime ignores the discarded `ctrl.Result`. The documented recovery bound
  is therefore ~30 s + nudge interval, not 5 s + interval. Recorded so a slow
  post-recovery nudge is not read as a regression.
* **"Short of pods implies the pass requeues" excludes the StatefulSet-absent case.**
  A NotFound reports not-short and forgets the observation, and the `Provisioning` path
  then ends with no requeue; recovery rides on the controller-runtime `Owns()` watch. If
  that watch were ever removed or filtered, this path would stall silently.
* **A blocked pod creation is not visible on the CR as such.** It shows up as a
  short-of-pods StatefulSet, not as `ReconcileBlocked` — see
  [ADR 0002](0002-surface-a-blocked-reconcile-on-the-cr.md) D13.
* Any future code that folds all StatefulSet annotations into a drift comparison or a
  hash breaks D2's safety argument silently.

## References

* [`internal/controller/nudge.go`](../../internal/controller/nudge.go) — `nudgeShortStatefulSets`, `nudgeStatefulSet`, `nudgeTracker`, `forgetNudges`, `nudgeGracePeriod`, `nudgeRequeueInterval`
* [`internal/builder/annotations.go`](../../internal/builder/annotations.go) — `AnnotationNudge`, `NudgeInterval`, `NudgeDue`, `NudgePatch`
* [ADR 0001](0001-continue-reconciling-past-a-rejected-write.md) — why the nudge is reached at all on a blocked pass
* [ADR 0002](0002-surface-a-blocked-reconcile-on-the-cr.md) — the complementary signal, and what it deliberately does not cover
* [ADR 0007](0007-failover-aware-rolling-update.md) — `OnDelete` + `Parallel` on the data StatefulSet, so pod creation is the statefulset-controller's job. It does not discuss the nudge; why nudging across a self-inflicted delete is safe is D8 above and the `nudgeShortStatefulSets` doc comment
