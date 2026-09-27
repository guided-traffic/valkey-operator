---
id: T60
title: ADR 0020 D9 claims a ReconcileBlocked report that the rolling update's pod refusal never writes
state: analysed       # was filed; every fact re-verified at 84a39c2 on 2026-09-27, the open mechanism item (retry cadence) settled by reading, options complete
severity: low         # the refusal protects correctly; only its report on the CR is weaker than documented
security: hardening
threat: "no attack path: a pod whose generated name is held by a foreign pod is refused as ADR 0020 D9 intends; what is missing is the ReconcileBlocked/ForeignObject report, and with it the critical ValkeyReconcileBlocked alert, which would additionally flag such a collision - on installs that render the chart's PrometheusRule at all (metrics.prometheusRule.enabled, default false)"
urgency: now          # rule 1, re-derived 2026-09-27 at 84a39c2: ADR 0020 :11-12, :497-498 and :803-804 say a colliding Sentinel pod gets no Event, false by code reading for one carrying the data-pod selector labels (work list item 2); then later (rule 4, option B is a costed, cheap known fix). Was later (rule 4) after item 1 landed
effort: S             # the ADR corrections are XS; the recommended option B is S
blocked-by: decision  # which option, below
filed-from: orchestrator verification during the ticket enrichment of 2026-09-27
opened: 2026-09-27
decided:              # not recorded - no decision yet
done:                 # not recorded - not done
---

# T60 - ADR 0020 D9 claims a ReconcileBlocked report that the rolling update's pod refusal never writes

## Fact

[ADR 0020](../adr/0020-write-only-what-the-operator-owns.md) D9 decides that the rolling update
refuses a pod whose generated name (`<cr>-N`, `<cr>-sentinel-N`) is held by a pod its StatefulSet
did not create, and fails instead of deleting it. It then ~~says~~ *(said, until work list item 1
on 2026-09-27; struck and corrected in place, History)*, in the D9 paragraph "One reporter, and
it is not the rolling update" (`0020:486-499`, the struck sentence at `:489`): "The rolling
update emits no Event of its own — its refusal already reaches the CR as
`ReconcileBlocked/ForeignObject` with the pod name in the message". The Consequences bullet at
`0020:589-594` (struck text at `:590-591`) ~~repeats~~ *(repeated)* it: such a cluster "now
reports `ReconcileBlocked/ForeignObject` and phase `Error`". The phase half is true. The
condition half is not. *(2026-09-27: both places now say phase `Error` and `status.message`
only, and name the open question; the code is unchanged at `84a39c2`, so the mechanism below
still holds.)*

**Mechanism** (read at `84a39c2`).

- `ReconcileBlocked` has one evaluator, `setReconcileBlockedCondition`
  ([`reconcile_blocked.go:118`](../../internal/controller/reconcile_blocked.go#L118)), and one
  production caller,
  [`valkey_controller.go:276`](../../internal/controller/valkey_controller.go#L276), which passes
  `resourceErr`: the joined error of `reconcileResources` (`:275`). The registry row declares
  exactly that: a level, one evaluator, cleared by the same call
  ([`condition_registry.go:104-110`](../../internal/controller/condition_registry.go#L104-L110)).
  The blocked-pass marker is set from the same `resourceErr`, before the workload
  ([`valkey_controller.go:278-282`](../../internal/controller/valkey_controller.go#L278-L282)).
- The rolling update is not a resource step. It runs in `reconcileWorkload` (`:284`), and its pod
  refusals return `foreignObjectError("Pod", …)`
  ([`foreign_object.go:76-78`](../../internal/controller/foreign_object.go#L76-L78)) ~~as
  `RollingUpdateResult.Error`~~ *(corrected 2026-09-27 at 84a39c2: every site surfaces it as
  `RollingUpdateResult.Error` except one of the four callers of `collectPodStates`,
  `verifyTopologyRestored`, which does not, see the bullet after the list)*: data tier at
  [`rolling_update.go:282`](../../internal/controller/rolling_update.go#L282)
  (`dispatchDataRollingUpdate`), [`:1935`](../../internal/controller/rolling_update.go#L1935)
  (`collectPodStates`, which feeds `:706` `handleRollingUpdate`, `:3088` `handlePostFailover`,
  `:3881` `handleMultiReplicaRollingUpdate` and `:4728` `verifyTopologyRestored`),
  [`:3782`](../../internal/controller/rolling_update.go#L3782) (`handleStandaloneRollingUpdate`) and
  [`:4289`](../../internal/controller/rolling_update.go#L4289) (`handlePostManualFailover`);
  Sentinel tier at [`:4992`](../../internal/controller/rolling_update.go#L4992)
  (`scanSentinelPods`, returned at `:5061-5063`).
- *(added 2026-09-27 at 84a39c2)* `verifyTopologyRestored`
  ([`rolling_update.go:4728-4752`](../../internal/controller/rolling_update.go#L4728-L4752))
  turns any `collectPodStates` error, a foreign pod included, into `NeedsRequeue` every 10 s,
  arms the finalization clock, and past `finalizationStallTimeout` completes the roll unverified
  with a `TopologyVerifyIncomplete` Warning. That path is shadowed, not a live fifth route:
  `verifyTopologyRestored` and `handlePostManualFailover` are reached only from
  `handleMultiReplicaRollingUpdate` (`verifyTopologyRestored` at `:3925` and `:3956`,
  `handlePostManualFailover` at `:3921` and `:3950`), after its own
  `collectPodStates` at `:3881` has refused or proved every ordinal in the same pass and returned
  `{Error: err}` on a refusal (`:3881-3884`); `handlePostFailover` likewise only after `:706`.
  A foreign pod reaches the swallowing read only if it appears in the cache between the two reads
  of one pass, and the next pass refuses at `:282` or `:3881` with a phase write.
- The data tier is measured on every pass that reaches the rolling update with an owned data
  StatefulSet: `checkAndHandleRollingUpdate` is called unconditionally
  ([`valkey_controller.go:335`](../../internal/controller/valkey_controller.go#L335)), the
  dispatch loop walks the ordinals until the first outdated pod
  ([`rolling_update.go:264-290`](../../internal/controller/rolling_update.go#L264-L290), `continue`
  on a missing pod, `break` at `:286-289`), and every handler behind it walks all ordinals again
  first. The Sentinel tier is not: `runSentinelRollingUpdate` is not reached on a pass where the
  data roll errors (`:336-339`), requeues (`:340-342`) or holds (`:465-468`), nor when Sentinel is
  disabled (`:455-464`).
- The data-tier error reaches
  [`valkey_controller.go:336-339`](../../internal/controller/valkey_controller.go#L336-L339):
  `updatePhase(Error, "Rolling update error: …")` and a return. The Sentinel-tier error reaches
  [`:473-481`](../../internal/controller/valkey_controller.go#L473-L481):
  `updatePhase(Error, "Sentinel rolling update error: …")`. Neither touches `ReconcileBlocked`.
  Both exits return before `updateStatus` (`:369`), so `Ready`, `status.readyReplicas`,
  `masterPod` and `observerReady` stay frozen while a collision stands (T18's scope).
- A pod collision does not fail `reconcileResources`: `reconcileSidecarRole`
  ([`valkey_controller.go:1096`](../../internal/controller/valkey_controller.go#L1096)) emits the
  `PodNotOwned` Warning for the pods it refuses (`:1106-1110`, the comment at `:1102-1105`) and
  carries on without an error. The other pod reads treat a foreign pod as absent or skip it
  (`pod_security_migration.go:73`, `valkey_controller.go:1830`, `:2865`, `tls_material.go:347`).
  So `resourceErr` is nil, and `:276` keeps or writes `ReconcileBlocked=False` (or writes
  nothing on a CR that never carried it,
  [`reconcile_blocked.go:121-132`](../../internal/controller/reconcile_blocked.go#L121-L132)).

What the CR shows for a pod collision: phase `Error`, `status.message` naming the pod, and
`ReconcileBlocked` absent or `False/ReconcileSucceeded`. On a pass another resource step blocks,
`updatePhase` is suppressed ([`valkey_controller.go:2606-2611`](../../internal/controller/valkey_controller.go#L2606-L2611))
and the one phase write names only the resource error (`:295-296`), so the collision is not on
the CR at all.

**Verified** (by reading at `4a7543e`, re-read at `84a39c2` on 2026-09-27):

- Everything above.
- The claim was false when it was written. Both sentences came in `995f186` (2026-08-22), and at
  that commit the evaluator was already called only with `resourceErr`
  (`git show 995f186:internal/controller/valkey_controller.go`: `:258`
  `resourceErr := r.reconcileResources`, `:259` the evaluator call) and the rolling-update
  error path already wrote only the phase (`:320`).
- No test asserts the CR-level report. `TestCollectPodStates_RefusesAForeignPod`,
  `TestCheckAndHandleRollingUpdate_RefusesAForeignPod` and
  `TestCheckAndHandleSentinelRollingUpdate_RefusesAForeignPod`
  ([`foreign_object_test.go:1050-1103`](../../internal/controller/foreign_object_test.go#L1050-L1103))
  ~~assert the returned error and that the pod survives~~ *(corrected 2026-09-27 at 84a39c2: all
  three assert the returned error; only the second and the third also assert that the pod
  survives, `TestCollectPodStates_RefusesAForeignPod` (`:1050-1059`) asserts only
  `ErrorIs(errForeignObject)`)*. `grep -rn 'foreignPod(' internal/ test/` finds no test that runs
  `Reconcile` with a foreign pod at an in-range ordinal, and `test/integration/foreign_object_test.go`
  has no Pod case.
- The alert contract follows the condition: `ValkeyReconcileBlocked` (critical, `for: 15m`) keys
  on it
  ([`prometheusrule.yaml:51-59`](../../deploy/helm/valkey-operator/templates/prometheusrule.yaml#L51-L59)),
  so it does not fire for a pod collision; `ValkeyPhaseNotOK` (warning, `for: 30m`, `:73-79`)
  does. The PrometheusRule is off by default
  ([`values.yaml:143`](../../deploy/helm/valkey-operator/values.yaml#L143),
  `metrics.prometheusRule.enabled: false`).
- ~~The Event half of the ADR paragraph holds for data pods only (corrected 2026-09-27: at
  most for data pods; which foreign data pods `listDataPodNames` returns was not enumerated
  here). `reconcileSidecarRole` takes its list from `listDataPodNames`
  ([`valkey_controller.go:1061-1083`](../../internal/controller/valkey_controller.go#L1061-L1083)),
  which lists data pods; no other `warnForeignObject` call names a pod. A collision on a Sentinel
  pod name, refused at `rolling_update.go:4989`, therefore gets no Event and no condition.~~
  *(corrected 2026-09-27 at 84a39c2: the `PodNotOwned` Event is decided by labels, not by name.
  `listDataPodNames` lists by `SelectorLabels(v, ComponentValkey)`
  ([`valkey_controller.go:1065-1068`](../../internal/controller/valkey_controller.go#L1065-L1068)),
  the three labels `app.kubernetes.io/instance`, `app.kubernetes.io/managed-by` and
  `app.kubernetes.io/component=valkey`
  ([`labels.go:115-121`](../../internal/common/labels.go#L115-L121)), and refuses every listed pod
  its data StatefulSet does not control (`filterOwnedPods`,
  [`foreign_object.go:264-275`](../../internal/controller/foreign_object.go#L264-L275)); when the
  data StatefulSet is absent or foreign, `ownedDataStatefulSet` returns nil
  (`foreign_object.go:238-256`) and every labelled pod is refused and warned about. So the Event
  fires for every foreign pod carrying those three labels, whatever its name, inside or outside
  the ordinal range, a Sentinel pod name included, and never for a pod under a generated name
  without them. It fires only on a pass where `reconcileSidecarRole` runs:
  `reconcileSidecarRBAC` returns before it when the sidecar ServiceAccount step errors or the
  ServiceAccount is foreign (`valkey_controller.go:983-992`), and a failed pod List or
  StatefulSet read fails the step before the Event loop (`:1096-1100`). `reasonPodNotOwned` has
  one emission, `valkey_controller.go:1107` (the constant at `foreign_object.go:48`). The repo's
  own Sentinel refusal fixture has the labelled shape: `foreignPod` carries
  `PodLabels(v, ComponentValkey, …)` (`foreign_object_test.go:921-938`) and is used under the
  Sentinel name at `:1085`, but that test never runs `reconcileSidecarRole`. The condition half
  holds: a collision on a Sentinel pod name, refused at `rolling_update.go:4992`, gets no
  condition.)*
- *(added 2026-09-27 at 84a39c2)* ADR 0020 still carries a sentence this finding makes false for
  one case: "a colliding Sentinel pod, or one without the data-pod selector labels, gets no
  Event" (Status `0020:11-12`, Residual risks `:803-804`; D9 `:497-498` words it "a colliding
  Sentinel pod, or a pod under a data-pod name without those labels"). A Sentinel-named foreign
  pod carrying the data-pod selector labels does get `PodNotOwned`. Work list item 2 corrects it.
- *(added 2026-09-27 at 84a39c2)* ADR 0020 D5's list of per-family Warning Event reasons
  (`0020:337-341`) ends at `CertificateNotOwned` and omits `PodNotOwned`, which exists since
  `995f186` (`git log -S reasonPodNotOwned -- internal/controller/foreign_object.go`). An
  incomplete list, not a false statement; work list item 2 adds it.
- *(added 2026-09-27 at 84a39c2)* D5 is not honoured by every door, and the ticket's former
  Options justification said it was. ADR 0020 D2 (`0020:251-279`) puts five refusals on the
  "costs the CR nothing" side: they return nil with `requestRecheck` and never reach
  `ReconcileBlocked` — the metrics Service
  ([`valkey_controller.go:605-613`](../../internal/controller/valkey_controller.go#L605-L613)),
  the ServiceMonitor (`:680-692`), the observer ServiceAccount (`:775-782`), the observer
  NetworkPolicy (`:1972-1974`) and the observer Deployment (`:2057-2064`). What holds is
  narrower: D9's fail-direction table (`0020:472-476`) puts the rolling update on the "refuse and
  fail the step" side, and it is the only failing-direction door whose refusal is not in
  `resourceErr`, so the only failing refusal without the condition.
- ~~How fast the refusal is re-checked: it returns an error, so the controller-runtime rate
  limiter drives it, not the 30 s recheck D6 describes for resource-step refusals
  ([`foreign_object.go:61`](../../internal/controller/foreign_object.go#L61)). Not examined
  further.~~ *(corrected 2026-09-27 at 84a39c2: verified by reading, moved here from Not verified.
  The error leaves `Reconcile` without `applyRecheck` (`valkey_controller.go:299-301`, the
  Sentinel error via `:480` and `:365`). controller-runtime v0.25.1 (`go.mod:16`) re-adds a
  non-terminal error rate-limited, `pkg/internal/controller/controller.go:486-490`
  (`c.Queue.AddWithOpts(priorityqueue.AddOpts{RateLimited: true, …}, req)`), logs
  "Reconciler error" and increments the reconcile-errors metric (`:491-497`); read from the local
  module cache. The limiter is `newReconcileRateLimiter`
  ([`ratelimiter.go:71-79`](../../internal/controller/ratelimiter.go#L71-L79)): per-item
  exponential backoff from 5 ms (`:16`), capped at `reconcileRetryMaxDelay` = 30 s (`:38`),
  reached after about 13 consecutive failures, about 41 s (`:35-37`). After that the cadence is
  the 30 s of D6's `foreignObjectRecheckInterval`
  ([`foreign_object.go:61`](../../internal/controller/foreign_object.go#L61)).)*

**Not verified:**

- Nothing was run: no unit test through `Reconcile`, no Kind reproduction of a collision. No
  Valkey behaviour is involved, so no docker measurement was taken.
- [`docs/operations/status.md:37`](../operations/status.md#reconcileblocked) describes
  `ForeignObject` as "one of the generated names is held by an object this `Valkey` does not
  control". A reader will take pod names to be covered; the page does not say so explicitly, so
  it reads as imprecise rather than false. Not changed here. Under option B it becomes accurate
  as written; under option A it needs the half-sentence.
- The length of the orphan-delete re-adoption window (option B's consequences) was not measured.
- That option B's step leaves the existing `Reconcile`-level unit tests unblocked: their pod
  fixtures (`createSentinelPod`, `rolling_update_test.go:2198-2219`, via `ownedByTestSts`) are
  owned by the test StatefulSet, read but not run.

**Cross-ticket** *(added 2026-09-27 at 84a39c2; filed the same day)*: the stale sentence in
`docs/security/isolation-and-tenancy.md:192-193`, which still describes ADR 0020 as it read
before its `bcc63c9` correction, is item (i) of
[T70](070-tracked-comments-and-adrs-state-what-the-code-contradicts.md) (severity low, security
hardening, effort S, state analysed, urgency now), the ticket that collects the decision-free
corrections of tracked comments, ADR sentences and pages the 2026-09-27 re-verification found
false or stale.

## Impact

- **Contributors** ~~read~~ *(read, until work list item 1 on 2026-09-27)* in ADR 0020 that the pod
  door satisfies D5 ("A refusal is reported with its own ReconcileBlocked reason", `0020:332`).
  It does not, so D5 has an ~~unrecorded~~ exception *(recorded since 2026-09-27 as a Residual
  risk of ADR 0020, "The rolling update's pod refusal carries no condition", `0020:800-805`,
  with the question left open)*: the one failing-direction door without the condition (Fact).
- **Operators**: a pod-name collision does not fire the critical `ValkeyReconcileBlocked` alert.
  It shows only as phase `Error` and, after 30 minutes, the `ValkeyPhaseNotOK` warning; both
  alerts exist only with `metrics.prometheusRule.enabled` (default false); ~~a Sentinel-pod
  collision also has no Event~~ *(corrected 2026-09-27 at 84a39c2: a colliding pod without the
  data-pod selector labels has no Event, whatever its name; a Sentinel-named one carrying them
  does, Fact)*. On a pass another resource step blocks, the collision is not on the CR at all.
  While the collision stands, the status fields `updateStatus` writes stay frozen (T18). The
  refusal itself works: no foreign pod is deleted, commanded or counted.
- **Security**: hardening. Detection of a collision is weaker than documented; nothing is
  permitted that D9 refuses.
- **Other edits of ADR 0020** *(added 2026-09-27, consistency pass)*: ticket
  [040](040-tracked-files-cite-work-items-instead-of-adrs.md) rewrites the 37 `NA` label lines
  of ADR 0020 (its work item 4) and ticket
  [042](042-enforce-the-standing-constraints-with-a-static-analysis-test-net.md) adds a Status
  note and a residual risk to it after 040's rewrite. Work list item 2 and option B's amendments
  touch the D5 list, the D9 section, its table, Consequences and Residual risks of the same ADR;
  the contents do not conflict, and whichever edit lands later re-reads the ADR's line numbers.

## Options

Work list items 1 and 2 correct ADR 0020 under every option. One decision is open.

### D1 - How is a pod-name collision the rolling update refuses reported on the CR?

**Mechanism.** Today a pod holding a generated name (`<cr>-N`, `<cr>-sentinel-N`, N below the
StatefulSet's replicas) that its StatefulSet did not create is refused inside
`reconcileWorkload` ([`valkey_controller.go:284`](../../internal/controller/valkey_controller.go#L284))
at the five sites listed under Fact. The data-tier refusal writes phase `Error`,
`Rolling update error: …`, and returns (`:336-339`); the Sentinel-tier refusal writes
`Sentinel rolling update error: …` and returns (`:473-481`). `ReconcileBlocked` has one
evaluator, called once per pass at `:276` with the joined error of `reconcileResources` (`:275`),
and no resource step fails on a pod collision, so the condition is absent or `False` and the
critical `ValkeyReconcileBlocked` alert
([`prometheusrule.yaml:51-59`](../../deploy/helm/valkey-operator/templates/prometheusrule.yaml#L51-L59))
stays silent. The refusal is retried by the rate limiter, 5 ms doubling to a 30 s cap.

The choice changes only which surface carries the collision: the condition plus the alert, or
phase and message alone. It does not change the refusal itself (no foreign pod is deleted,
commanded or counted), the fail-the-step direction of the D9 table, the retry cadence of the
data-pod case (both paths return an error), the single `PodNotOwned` emitter, or the status
freeze on the rolling update's error exit (T18).

**A - Accept and record.** On top of work list items 1 and 2, state in place at D5
(`0020:332-345`) that the rolling update's pod door is the one failing-direction refusal reported
by phase and `status.message` only, and add a half-sentence to
[`status.md:37`](../operations/status.md#reconcileblocked) saying that `ForeignObject` does not
cover a pod under a generated pod name. The Residual-risks entry (`0020:800-805`) already
exists; its open question becomes the recorded answer. No code, nothing rolls.

- Cost XS: two sentences in ADR 0020, one half-sentence in `status.md`.
- Consequences: D5 permanently does not hold for the one door on the fail-the-step side of D9's
  table. With the rule enabled, `ValkeyReconcileBlocked` never fires for a pod collision;
  `ValkeyPhaseNotOK` (warning) fires after 30 minutes. A colliding pod without the data-pod
  selector labels gets no Event. A collision on a pass that another resource step blocks leaves
  no trace on the CR.

**B - Report it through the one evaluator (recommended).** Add a report-only resource step,
placed last next to "TLS material"
([`valkey_controller.go:563-572`](../../internal/controller/valkey_controller.go#L563-L572)),
that proves every pod at an ordinal of the data StatefulSet, and of the Sentinel StatefulSet when
`spec.sentinel.enabled` (the gate `runSentinelRollingUpdate` uses, `:455-464`). Per tier it
`Get`s the StatefulSet, skips one that is absent or foreign (`reconcileStatefulSet` at `:1321`
and the Sentinel step at `:1487` report those), then `Get`s each pod in `[0, *Spec.Replicas)` and
returns `foreignObjectError("Pod", name)` for a pod `podIsOurs` refuses — the rolling update's
own predicate, so a terminating foreign pod counts too. A read error other than NotFound is
**returned**, as every write step does (`reconcileSidecarRole` fails on a failed List,
`:1096-1100`): swallowing it would let `:276` clear a standing `ForeignObject` on a pass that
measured nothing (`reconcile_blocked.go:121-132`), the flap ADR 0027 forbids; the cost is a
`WriteFailed` reason for a rare failed cache read, which `status.md:37` already documents. The
step emits no Event: the condition message names the pod, and `PodNotOwned` stays with
`reconcileSidecarRole` (one reporter, ADR 0020 D9). Its error joins `resourceErr` (the step
wrapper, `valkey_controller.go:517-528`), so `:276` writes `True/ForeignObject` with the pod name
(ranked first, `reconcile_blocked.go:70`), the blocked pass writes one `Error` phase
(`valkey_controller.go:286-297`), and the next clean pass clears the condition at the same call.
The rolling update keeps its own refusal at the delete sites, where the object acted on is proven
(ADR 0006). The registry does not change: one evaluator, one clear site (ADR 0027,
`condition_registry_test.go:147-160`). The walk is the one `scanTierTLSMaterial` already runs
([`tls_material.go:317-360`](../../internal/controller/tls_material.go#L317-L360)), on cached
reads; that step is a precedent for the placement and the walk, not for failing —
`reportTLSMaterialStale` never returns an error (`tls_material.go:161-172`), while this step
deliberately does, to reach the one evaluator.

- Cost S. One step function well under the cyclomatic limit of 15, wired in the resource-step
  list. Unit tests through `Reconcile` (the `reconcileFor` helper, `status_phase_test.go`), one
  for a foreign pod at `<cr>-0` and one at `<cr>-sentinel-0`, each asserting
  `True/ForeignObject` naming the pod, phase `Error`, the pod surviving, and the clear to `False`
  after the pod is deleted, each failing with the step removed (ADR 0017 mutation check). In the
  same change: ADR 0020 D9's "One reporter" paragraph (`0020:486-499`), its fail-direction table
  (`:472-476`, a row for the new step), the Consequences bullet (`:589-594`) and the
  Residual-risks entry (`:800-805`, closed); ADR 0002 D13 (`0002:355-360`, "ReconcileBlocked
  covers only writes the operator itself performs"), amended in wording because the step reports
  a held pod name when no operator write is pending — the reason to record: `ForeignObject`
  already means "a generated name is held by an object this `Valkey` does not control", and the
  cause reported is the operator's own roll refusal, not the statefulset-controller's rejected
  create, which stays outside; and a row in the step table of
  [`docs/developer/reconcile-loop.md`](../developer/reconcile-loop.md) (`:84-96`).
  `status.md:37` becomes accurate as written. This amends ADR 0020 and ADR 0002 and reopens no
  decision.
- Consequences:
  - The phase message of a collision reads `Failed to reconcile resources: …` instead of
    `Rolling update error: …` (`updatePhase` is suppressed on a blocked pass,
    `valkey_controller.go:2606-2611`). No test and no tracked doc outside the ADR 0020 `:491`
    quote depends on the old text (`grep -rn 'Rolling update error' internal test cmd docs`).
  - With the PrometheusRule enabled, the critical alert fires after 15 minutes. Its summary, "The
    operator cannot write a managed resource" (`prometheusrule.yaml:61`), is loose for a pod the
    operator does not write; optional wording, not needed for correctness.
  - A Sentinel-pod collision is reported, by condition and message.
  - On a pass where the data tier rolls (`NeedsRequeue`, `valkey_controller.go:340-342`), a
    Sentinel-pod collision turns the phase from `Rolling Update i/n` into `Error` for the whole
    data roll, because the blocked pass owns the field (ADR 0002 D5); today that pass never
    reaches the Sentinel scan. The same pass drops the roll's 10 s requeue
    (`rollingUpdateRequeueDelay`, `rolling_update.go:203`), because a blocked pass returns
    `ctrl.Result{}` with the error (`valkey_controller.go:293-297`): the rate limiter paces it, a
    burst of fast passes and then 30 s, plus `Owns(StatefulSet)` events. The roll still progresses
    (the workload runs on a blocked pass), and its waits are wall-clock bounded (ADR 0010), so an
    expiry is seen up to about 20 s later. Every blocked pass already pays this (ADR 0001 D6); it
    is new only for this case. A data-pod collision is paced by the rate limiter today already.
  - Transient `ForeignObject` windows, all far below the alert's 15 minutes: the orphan-delete
    re-adoption (seconds, not measured; today the roll already refuses there with a phase write,
    ADR 0023 `:307-309`), and a CR recreated under the same name while its old pods still
    terminate, up to 75 s for data pods
    ([`statefulset.go:618`](../../internal/builder/statefulset.go#L618)) and 30 s for Sentinel
    pods ([`sentinel.go:390`](../../internal/builder/sentinel.go#L390)); the StatefulSet door
    already reports `ForeignObject` in that second case (`valkey_controller.go:1321`).
  - The Chaos Mesh pod-kill schedule produces no false positive: the pod the StatefulSet
    recreates carries its controller reference from creation (`podIsOurs` is `IsControlledBy`,
    `foreign_object.go:226-228`), which every roll in the fleet already depends on. By reading,
    not measured.
  - Upgrade-neutral: the condition is presence-guarded (`reconcile_blocked.go:121-125`), so it
    appears only on a CR that has a collision, and such a CR shows phase `Error` today already.
    Nothing rolls, no CR annotation is needed (Flux-safe). One extra cached `Get` per pod per
    pass, on top of the TLS walk on TLS clusters.
  - It does not unfreeze status during a collision: the rolling update still returns its error
    before `updateStatus` (T18).

**Recommendation: B.** It is the only option under which the one failing-direction refusal that
never reaches `ReconcileBlocked` does, and it does so through the existing single evaluator —
checkable: the registry row (`condition_registry.go:104-110`) stays at `evaluators: 1` and no ADR
decision is reopened. Its shape is proven here: the "TLS material" step sits among the resource
steps precisely because workload paths return early during a roll and a level measured there
goes stale, and its per-tier `podIsOurs` walk is the walk B needs. It is the only option that
makes `ValkeyReconcileBlocked` fire for a pod collision, names a Sentinel-pod collision on the
CR, and keeps the collision visible on a pass another step blocks (the condition message names
every joined step error, while the one phase write names only `resourceErr`, `:295-296`), at S on
cached reads, rolling nothing and needing no CR annotation. It beats A, the runner-up, because
A buys a one-size saving (XS against S) with a permanent exception for the one fail-direction
door, a blind critical alert, and no trace on a pass another step blocks.

## Work list

1. **XS, no decision needed**: correct ADR 0020 in place to what the code does today, true under
   every option. At `0020:489` strike "its refusal already reaches the CR as
   `ReconcileBlocked/ForeignObject` with the pod name in the message" and state that it reaches
   the CR as phase `Error` with the pod in `status.message` only, that `ReconcileBlocked` is
   evaluated from `reconcileResources` alone, and that the `PodNotOwned` Event covers data pods
   only. At `0020:590-591` strike "`ReconcileBlocked/ForeignObject` and" with a dated correction.
   Add a Status line "Amended 2026-09-27 (correction, no decision changes)" saying that whether
   the pod door is reported through `ReconcileBlocked`, as D5 asks, is open. No ticket citation in
   the ADR (ADR 0034). Does not close this ticket. **Done 2026-09-27, committed in `bcc63c9`**
   ("docs: correct comments and records that the code contradicts", +35 lines in ADR 0020), and
   more than the item asked: a Residual-risks entry records the gap too (`0020:800-805`,
   History). That entry is neutral between the options; under option A it is the entry A asks
   for, minus the D5 exception sentence and the `status.md:37` half-sentence.
2. **XS, no decision needed** *(added 2026-09-27 at 84a39c2)*: correct ADR 0020's Event wording
   in place, true under every option. In Status (`0020:11-12`), D9 (`:497-498`) and Residual risks
   (`:803-804`), "a colliding Sentinel pod, or one without the data-pod selector labels, gets no
   Event" (D9: "or a pod under a data-pod name without those labels") becomes "a colliding pod without the data-pod selector labels (`instance`,
   `managed-by`, `component=valkey`) gets no Event, whatever its name, and none gets one on a pass
   where the sidecar ServiceAccount step fails or the ServiceAccount is foreign", with a dated
   correction. Add `PodNotOwned` to D5's list of per-family Warning Event reasons (`:337-341`).
   Outside `docs/tickets/`, so not done in the re-verification run of 2026-09-27.
2a. **XS, no decision needed** *(added 2026-09-27, consistency pass; read at `84a39c2`)*: the
   comment above the refusal at
   [`rolling_update.go:279-280`](../../internal/controller/rolling_update.go#L279-L280) says the
   failure leaves the rolling-update state in place "so its bounded waits keep being driven". While
   the refusal repeats, every pass returns at `:282` before any wait of the roll is evaluated, so
   the waits stay armed and none is driven; ADR 0020 D9 (`0020:482-483`) says "stay armed", which
   is accurate. Reword the comment to match the ADR. True under every option.
3. **Waits on the decision**: the chosen option's work as listed under Options.

## Decision

None yet.

## Verification

- Item 1: `git grep -n "already reaches the CR as" -- docs/adr` finds only struck text, and ADR
  0020's Status carries the dated correction. *(Run 2026-09-27 after the fix: one hit, ADR 0020
  `:489`, inside struck text; the Status line "Amended again 2026-09-27 (correction, no decision
  changes)" is at `:7-16`. Done. Re-run at `84a39c2`: the same one hit; the change is committed
  in `bcc63c9`.)*
- Item 2: `git grep -n "Sentinel pod, or" -- docs/adr` (three hits at `84a39c2`: `0020:12`,
  `:498`, `:804`) finds only struck text, and
  `grep -n PodNotOwned docs/adr/0020-write-only-what-the-operator-owns.md` has a hit in D5.
- B: a unit test through `Reconcile` with a foreign pod at `<cr>-0` sees
  `ReconcileBlocked=True/ForeignObject` naming the pod and phase `Error`, and the pod survives;
  after the foreign pod is deleted the next pass clears the condition to `False`. The same test
  with a foreign pod at `<cr>-sentinel-0`. Both fail with the step removed. `make test-unit`,
  `make lint`, `make cyclo`.
- A: ADR 0020 D5 names the exception and `status.md:37` carries the half-sentence.

## History

- 2026-09-27: re-verified at `84a39c2`. **Checked**, by reading and against the auditor's report
  and two skeptic reviews: the one evaluator and its caller (`valkey_controller.go:275-276`), the
  registry row, the five `foreignObjectError("Pod", …)` sites and every `collectPodStates` caller,
  both phase-only error exits, `reconcileSidecarRole` and `listDataPodNames`, the alert rules and
  the chart default, `995f186` for the origin, `bcc63c9` for item 1, and the retry path through
  controller-runtime v0.25.1 (local module cache, `pkg/internal/controller/controller.go:486-497`)
  and `ratelimiter.go`. **Measured** (commands, not Valkey): `grep -n foreignObjectError
  internal/controller/*.go | grep -v _test` gives `rolling_update.go:282, 1935, 3782, 4289,
  4992`, against `282, 1934, 3779, 4286, 4989` at `4a7543e`; `grep -n 'collectPodStates('` gives
  callers `:706, :3088, :3881, :4728`; the only non-test caller of `setReconcileBlockedCondition`
  is `:276`; `git grep -n 'already reaches the CR as' -- docs/adr` gives one struck hit at
  `0020:489`; `sed -n 499,500p` and `sed -n 589,596p` of ADR 0020 put the D9 paragraph at
  `:486-499` (`:500` is blank) and the Consequences bullet at `:589-594`. No docker measurement:
  no Valkey behaviour is involved. **Locations re-read** at `84a39c2` and fixed in place (the
  `bcc63c9` comment lines shifted `rolling_update.go` by +1 and +3; ADR 0020 D5 moved to `:332`,
  the prometheusrule range widened to `:51-59` to take in the severity label, the `PodNotOwned` loop to `:1106-1110`). **Found false or
  outdated**, struck and corrected in place: the Event-half bullet, whose earlier "not
  enumerated" correction was itself outdated — `PodNotOwned` is label-driven, so a Sentinel-named
  foreign pod carrying the data-pod labels gets it and an unlabelled pod under any generated name
  does not, and the Event also waits on the sidecar ServiceAccount step; the implicit claim that
  every `collectPodStates` refusal surfaces as `RollingUpdateResult.Error` (`verifyTopologyRestored`
  swallows it, on a path shadowed by `:3881` in the same pass); `TestCollectPodStates_RefusesAForeignPod`
  asserts only the error; the Not-verified retry-cadence item, now verified (rate limiter, 30 s
  cap after about 41 s, equal to D6's recheck); the Impact line on Sentinel-pod Events. **New
  facts**: item 1 is committed in `bcc63c9`, not only a working-tree diff; ADR 0020's remaining
  sentence "a colliding Sentinel pod … gets no Event" is false for a labelled Sentinel-named pod
  (the repo's own fixture shape) and D5's Event list omits `PodNotOwned` — both put on the new
  decision-free work list item 2; the stale sentence in `isolation-and-tenancy.md:192-193`,
  recorded as a cross-ticket finding outside T60's scope. **Options**: rewritten as one decision
  (D1) with its mechanism. Removed: **B'** (a second writer: the rolling update sets
  `ReconcileBlocked` itself, or the evaluator moves after `reconcileWorkload`) — the Sentinel tier
  is not re-measured on every pass (its scan is skipped whenever the data roll errors, requeues or
  holds, `valkey_controller.go:336-342`, `:465-468`), so a level fed from the workload would be
  cleared on passes that never looked (ADR 0027); the first variant also flaps against `:276` and
  needs an `ownershipRule`; the ticket's former argument from
  `TestReconcile_InitialPhaseWriteFailureStillReportsReconcileBlocked` did not hold, the test
  asserts only the end state (`status_phase_test.go:330-352`), and the auditor's replacement
  argument that the data tier is not re-measured either was refuted (it is, every pass).
  **C** (emit `PodNotOwned` from the rolling update) — the condition and the alert stay blind,
  D5 still does not hold, and it adds a second Event reporter against ADR 0020 D9's one-reporter
  rule; its only gain, an Event for an unlabelled Sentinel-named collision, is covered better by
  B's condition message. **Recommendation unchanged (B)**, justification replaced: the former
  "D5 is a standing rule that every other door honours" is false (five D2 refusals return nil and
  never set the condition); B is recommended because the pod door is the only fail-direction
  refusal outside `resourceErr`. B's text gained: read errors are returned, not swallowed (an
  auditor proposal to log and drop them would have let `:276` clear a standing report); the ADR
  0002 D13 wording amendment and the D9 table row; the Sentinel-collision-during-data-roll phase
  change and pacing cost; the CR-recreate transient window (75 s / 30 s); the reconcile-loop step
  table row; the Chaos Mesh and Flux checks; the line on an embargoed ticket (removed since) and
  the T18 freeze.
  **Frontmatter**: `state` filed -> analysed (every fact re-verified, the open mechanism item
  settled, the options complete); the threat line adds that the alert exists only with
  `metrics.prometheusRule.enabled` (default false, `values.yaml:143`); severity low, security
  hardening, effort S and blocked-by decision unchanged. **Urgency `later` -> `now`, rule 1**
  (set by the review pass of this entry; the editing pass had kept `later`). ADR 0020's
  remaining sentence "a colliding Sentinel pod, or one without the data-pod selector labels, gets
  no Event" (`0020:11-12`, `:497-498`, `:803-804`) is false by code reading for a determinable
  case: a Sentinel-named foreign pod carrying the data-pod selector labels gets `PodNotOwned`, and
  the repo's own fixture has that shape. This run applies rule 1 to statements false by code
  reading (T18: "false by code or source reading"; T29: "rule 1 by reading, per the
  018/044/045/062 precedent"), and this ticket was itself filed at `now` by rule 1 on a statement
  verified by reading. The counter-reading was weighed and not taken: that the sentence states its
  premise correctly ("lists only pods carrying the data-pod selector labels") and is imprecise by
  omission. Its conclusion names every colliding Sentinel pod, which is what is false. `now`
  covers work list item 2 alone (XS, no decision); when it lands, rule 1 no longer matches and a
  top-down re-derivation gives `later` (rule 4: option B is a costed, cheap known fix). **Disputed,
  owner to settle**: if the owner reads rule 1's "measured-false" as excluding statements verified
  only by reading, urgency is `later` now. **Not verified**: nothing was run; the re-adoption
  window length and the existing tests under B's step are read, not measured.
  Cross-ticket: in the consistency pass of the same day, the line on an embargoed ticket was cut
  back to the words the embargo permits (the old clause is not kept), a bullet records that 040
  and 042 also edit ADR 0020, and Work list item 2a was added: the comment at `rolling_update.go:279-280` says the
  waits "keep being driven" where ADR 0020 D9 says, accurately, that they stay armed (read at
  `84a39c2`).
  Filed: the cross-ticket finding on `isolation-and-tenancy.md:192-193` moved to
  [T70](070-tracked-comments-and-adrs-state-what-the-code-contradicts.md) as its item (i), and
  the Fact section keeps only a pointer to it; nothing in this ticket's frontmatter, options or
  recommendation rested on that finding, so nothing else changed.
  Sweep: The Impact bullet on an embargoed ticket was removed before commit, and this entry's two
  mentions of it no longer carry its id. Frontmatter, options and recommendation unchanged.
- 2026-09-27: urgency `now` -> `later` (rule 4, top-down): the ADR 0020 correction, the only rule-1 statement, landed; option B is a costed, cheap known fix. Applied as the History entry below derived it.
- 2026-09-27: work list item 1 landed, one file (read in `git diff` of the working tree):
  [ADR 0020](../adr/0020-write-only-what-the-operator-owns.md).
  - D9, its paragraph "One reporter, and it is not the rolling update" (`:486-499` now): "its refusal already reaches the CR as `ReconcileBlocked/ForeignObject`
    with the pod name in the message" struck and corrected in place - phase `Error` with the pod
    in `status.message` (`Rolling update error: …`, `Sentinel rolling update error: …`), on a
    pass the resource step also blocks not even there, `ReconcileBlocked` evaluated from
    `reconcileResources` alone so the `ValkeyReconcileBlocked` alert does not fire, never true
    since the paragraph was written, and no Event for a colliding Sentinel pod; "one Event series
    per pass" became "at most one".
  - The Consequences bullet (`:589-591` now): "`ReconcileBlocked/ForeignObject` and" struck.
  - Status: a dated "Amended again 2026-09-27 (correction, no decision changes)" line at the top,
    newest first, stating that the reporting question is open.
  - Residual risks: a new entry, "The rolling update's pod refusal carries no condition", with
    the question open and no option taken.

  Neither option is decided in the ADR text. The decision (B recommended) and item 2 are
  untouched. **Urgency not recomputed in this pass** (the orchestrating run left every urgency
  but one to the owner): item 1 was the only rule-1 statement, so a top-down re-derivation gives
  `later` (rule 4: option B is a costed, cheap known fix). **Not verified:** nothing was run.
- 2026-09-27: adversarial review of the filing. Re-read at `4a7543e`: `valkey_controller.go:276`,
  `:336-339`, `:473-481`, `reconcile_blocked.go:118-131`, `foreign_object.go:61`, `:76-78`, the
  five `foreignObjectError("Pod", …)` sites, ADR 0020 `:321`, `:475-479`, `:570` and
  `prometheusrule.yaml:51-58` (severity `critical`) and `:73-79` hold. Two changes: the
  Event-half bullet read as if the `PodNotOwned` Event covered every foreign data pod, which
  was not enumerated (narrowed to "at most for data pods", struck and corrected in place). The bullet
  that referred to an embargoed ticket was **removed, not struck**, under the
  [embargo rule](README.md#an-open-security-finding-is-embargoed); striking would have kept it.
  The file was untracked, so no commit carried it. Recommendation B stands. **Verified:** by reading. **Not
  verified:** nothing was run.
- 2026-09-27: filed during the ticket enrichment. The orchestrator verified by reading that ADR
  0020 D9 and its Consequences bullet claim a `ReconcileBlocked/ForeignObject` report that the
  rolling update never writes; this file re-verified it at `4a7543e`, dated the claim to
  `995f186`, and added the Sentinel-pod Event gap. Urgency `now` by rule 1. **Verified:** by
  reading, grep and `git show`. **Not verified:** nothing was run.
