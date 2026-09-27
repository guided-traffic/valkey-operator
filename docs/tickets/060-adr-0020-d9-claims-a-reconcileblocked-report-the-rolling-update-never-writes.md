---
id: T60
title: ADR 0020 D9 claims a ReconcileBlocked report that the rolling update's pod refusal never writes
state: analysed       # every fact verified, options complete
severity: low         # the refusal protects correctly; only its report on the CR is weaker than documented
security: hardening
threat: "no attack path: a pod whose generated name is held by a foreign pod is refused as ADR 0020 D9 intends; what is missing is the ReconcileBlocked/ForeignObject report and with it the critical ValkeyReconcileBlocked alert, on installs that render the chart's PrometheusRule (metrics.prometheusRule.enabled, default false)"
urgency: now          # rule 1: ADR 0020 states a false Event rule (required change 1); later (rule 4) once that lands
effort: S             # the ADR and comment corrections are XS; the recommended option B is S
blocked-by: decision  # Q1
filed-from: orchestrator verification during the ticket enrichment
opened: 2026-09-27
decided:
done:
---

# T60 - ADR 0020 D9 claims a ReconcileBlocked report that the rolling update's pod refusal never writes

## Current state

[ADR 0020](../adr/0020-write-only-what-the-operator-owns.md) D9 refuses a pod whose generated
name (`<cr>-N`, `<cr>-sentinel-N`, N below the StatefulSet's replicas) is held by a pod its
StatefulSet did not create, and fails the step instead of deleting it. D5 (`0020:332`) says every
refusal is reported with its own `ReconcileBlocked` reason. The pod door does not do that; ADR
0020 records this as an open Residual risk ("The rolling update's pod refusal carries no
condition", `0020:800-805`).

**Why the condition is never written**

- `ReconcileBlocked` has one evaluator, `setReconcileBlockedCondition`
  ([`reconcile_blocked.go:118`](../../internal/controller/reconcile_blocked.go#L118)), called once
  per pass at [`valkey_controller.go:276`](../../internal/controller/valkey_controller.go#L276)
  with `resourceErr`, the joined error of `reconcileResources` (`:275`). The registry row declares
  one evaluator and the same call as the clear site
  ([`condition_registry.go:104-110`](../../internal/controller/condition_registry.go#L104-L110)).
- The rolling update runs later, in `reconcileWorkload` (`:284`). Its refusals return
  `foreignObjectError("Pod", …)`
  ([`foreign_object.go:76-78`](../../internal/controller/foreign_object.go#L76-L78)) at
  [`rolling_update.go:282`](../../internal/controller/rolling_update.go#L282),
  [`:1935`](../../internal/controller/rolling_update.go#L1935) (`collectPodStates`),
  [`:3782`](../../internal/controller/rolling_update.go#L3782),
  [`:4289`](../../internal/controller/rolling_update.go#L4289) (data tier) and
  [`:4992`](../../internal/controller/rolling_update.go#L4992) (Sentinel tier, `scanSentinelPods`).
  `verifyTopologyRestored` (`:4728-4752`) swallows a `collectPodStates` error, but only after
  `:3881` has already refused in the same pass, so it is not a live route.
- The data-tier error writes phase `Error`, `Rolling update error: …`
  ([`valkey_controller.go:336-339`](../../internal/controller/valkey_controller.go#L336-L339));
  the Sentinel-tier error writes `Sentinel rolling update error: …`
  ([`:473-481`](../../internal/controller/valkey_controller.go#L473-L481)). Neither touches
  `ReconcileBlocked`, and both return before `updateStatus` (`:369`), so the status fields it
  writes stay frozen (T18).
- No resource step fails on a pod collision: `reconcileSidecarRole`
  ([`valkey_controller.go:1096`](../../internal/controller/valkey_controller.go#L1096)) emits
  `PodNotOwned` (`:1106-1110`) and carries on. So `ReconcileBlocked` is absent or
  `False/ReconcileSucceeded`
  ([`reconcile_blocked.go:121-132`](../../internal/controller/reconcile_blocked.go#L121-L132)).
- On a pass another resource step blocks, `updatePhase` is suppressed
  ([`valkey_controller.go:2606-2611`](../../internal/controller/valkey_controller.go#L2606-L2611))
  and the one phase write names only the resource error, so the collision is not on the CR at all.
- The data tier is scanned on every pass (`checkAndHandleRollingUpdate` is unconditional,
  `valkey_controller.go:335`); the Sentinel tier is not scanned on a pass where the data roll
  errors, requeues or holds (`:336-342`, `:465-468`), nor with Sentinel disabled (`:455-464`).
- The refusal is retried by the rate limiter
  ([`ratelimiter.go:71-79`](../../internal/controller/ratelimiter.go#L71-L79)): 5 ms doubling,
  capped at 30 s after about 41 s.
- The rolling update is the only door on the fail-the-step side of D9's table (`0020:472-476`)
  whose refusal is not in `resourceErr`.

**The `PodNotOwned` Event is decided by labels, not by name.** `listDataPodNames` lists by the
data-pod selector labels `app.kubernetes.io/instance`, `app.kubernetes.io/managed-by`,
`app.kubernetes.io/component=valkey`
([`valkey_controller.go:1065-1068`](../../internal/controller/valkey_controller.go#L1065-L1068),
[`labels.go:115-121`](../../internal/common/labels.go#L115-L121)) and refuses every listed pod
the data StatefulSet does not control (`filterOwnedPods`,
[`foreign_object.go:264-275`](../../internal/controller/foreign_object.go#L264-L275)). The Event
fires for every foreign pod carrying those labels, a Sentinel-named one included, and never for a
pod without them. It does not fire on a pass where the sidecar ServiceAccount step fails or the
ServiceAccount is foreign (`valkey_controller.go:983-992`), or the pod List fails (`:1096-1100`).
ADR 0020 says instead that "a colliding Sentinel pod, or one without the data-pod selector labels,
gets no Event" (Status `0020:11-12`, D9 `:497-498`, Residual risks `:803-804`), which is false for
a Sentinel-named pod carrying the labels. D5's list of Event reasons (`0020:337-341`) omits
`PodNotOwned`.

**Comment drift.** The comment at
[`rolling_update.go:279-280`](../../internal/controller/rolling_update.go#L279-L280) says the
refusal leaves the roll state in place "so its bounded waits keep being driven". Every refused
pass returns at `:282` before any wait is evaluated, so the waits stay armed and none is driven;
ADR 0020 D9 (`0020:482-483`) says "stay armed", which is accurate.

**Tests.** `TestCollectPodStates_RefusesAForeignPod`,
`TestCheckAndHandleRollingUpdate_RefusesAForeignPod` and
`TestCheckAndHandleSentinelRollingUpdate_RefusesAForeignPod`
([`foreign_object_test.go:1050-1103`](../../internal/controller/foreign_object_test.go#L1050-L1103))
assert the returned error; the last two also assert the pod survives. No test runs `Reconcile`
with a foreign pod at an in-range ordinal, and `test/integration/foreign_object_test.go` has no Pod
case.

**Impact**

- Operators: a pod-name collision never fires the critical `ValkeyReconcileBlocked` alert
  ([`prometheusrule.yaml:51-59`](../../deploy/helm/valkey-operator/templates/prometheusrule.yaml#L51-L59),
  `for: 15m`); it shows as phase `Error` and, after 30 minutes, the `ValkeyPhaseNotOK` warning
  (`:73-79`). Both alerts exist only with `metrics.prometheusRule.enabled`
  ([`values.yaml:143`](../../deploy/helm/valkey-operator/values.yaml#L143), default false).
- A colliding pod without the data-pod labels gets no Event; on a pass another step blocks, the
  collision leaves no trace on the CR.
- Contributors read in D5 a rule the pod door does not satisfy.
- The refusal itself works: no foreign pod is deleted, commanded or counted.

## Required changes

### Independent of the open questions

1. ADR 0020, Status (`0020:11-12`), D9 (`:497-498`) and Residual risks (`:803-804`): replace the
   Event sentence with "a colliding pod without the data-pod selector labels (`instance`,
   `managed-by`, `component=valkey`) gets no Event, whatever its name, and none gets one on a pass
   where the sidecar ServiceAccount step fails or the ServiceAccount is foreign". Add `PodNotOwned`
   to D5's list of Event reasons (`:337-341`). Check: `git grep -n "Sentinel pod, or" -- docs/adr`
   finds no current statement, and `PodNotOwned` has a hit in D5.
2. Reword the comment at `rolling_update.go:279-280` to say the waits stay armed.

### Depends on the answers

- Option A: the D5 exception sentence in ADR 0020 (`0020:332-345`), the Residual-risks entry
  (`:800-805`) turned into the recorded answer, and a half-sentence in
  [`status.md:37`](../operations/status.md#reconcileblocked) that `ForeignObject` does not cover a
  pod under a generated pod name.
- Option B: the step, tests and ADR edits described under Q1.

## Open questions

### Q1: Is a pod-name collision the rolling update refuses reported through `ReconcileBlocked`?

Today it reaches the CR only as phase `Error` with the pod in `status.message`. The choice decides
whether it also carries the condition and fires the critical alert; it does not change the refusal
itself, the retry cadence of a data-pod collision, the single `PodNotOwned` emitter or the status
freeze (T18).

- **A - Accept and record.** D5 names the pod door as the one failing refusal reported by phase and
  message only. Cost XS, no code. `ValkeyReconcileBlocked` stays blind to pod collisions, and a
  collision on a pass another step blocks leaves no trace.
- **B - Report it through the one evaluator (recommended).** A report-only resource step, placed
  last next to "TLS material"
  ([`valkey_controller.go:563-572`](../../internal/controller/valkey_controller.go#L563-L572)).
  Per tier (data always, Sentinel when `spec.sentinel.enabled`) it `Get`s the StatefulSet, skips
  one that is absent or foreign (reported by their own steps), `Get`s each pod in
  `[0, *Spec.Replicas)` and returns `foreignObjectError("Pod", name)` for a pod `podIsOurs`
  refuses. A read error other than NotFound is returned, so `:276` cannot clear a standing
  `ForeignObject` on a pass that measured nothing (ADR 0027). No Event (the condition message names
  the pod). The error joins `resourceErr`, so `:276` writes `True/ForeignObject` and the next clean
  pass clears it; the registry stays at one evaluator. The walk mirrors `scanTierTLSMaterial`
  ([`tls_material.go:317-360`](../../internal/controller/tls_material.go#L317-L360)), on cached
  reads. The rolling update keeps its own refusal at the delete sites. Cost S. Consequences:
  - The phase message reads `Failed to reconcile resources: …` instead of `Rolling update error: …`.
  - With the rule enabled, the critical alert fires after 15 minutes; its summary "The operator
    cannot write a managed resource" (`prometheusrule.yaml:61`) is loose for a pod (optional
    rewording).
  - A Sentinel-pod collision during a data roll turns the phase from `Rolling Update i/n` into
    `Error` and replaces the roll's 10 s requeue with rate-limiter pacing; the roll still
    progresses and its waits are wall-clock bounded.
  - Short transient `ForeignObject` windows: the orphan-delete re-adoption, and a CR recreated
    while its old pods still terminate (up to 75 s data, 30 s Sentinel).
  - Upgrade-neutral: presence-guarded condition, nothing rolls, no CR annotation; one cached `Get`
    per pod per pass.
  - Tests through `Reconcile` (`reconcileFor`, `status_phase_test.go`), one with a foreign pod at
    `<cr>-0` and one at `<cr>-sentinel-0`: each asserts `True/ForeignObject` naming the pod, phase
    `Error`, the pod surviving, and the clear to `False` after the pod is deleted; each fails with
    the step removed. `make test-unit`, `make lint`, `make cyclo`.
  - Docs in the same change: ADR 0020 D9's "One reporter" paragraph (`0020:486-499`), its
    fail-direction table (`:472-476`, a row for the step), the Consequences bullet (`:589-594`),
    the Residual-risks entry (`:800-805`, closed); ADR 0002 D13 (`0002:355-360`) worded so that
    `ReconcileBlocked` also covers a held pod name the roll refuses; a row in the step table of
    [`docs/developer/reconcile-loop.md`](../developer/reconcile-loop.md) (`:84-96`). `status.md:37`
    becomes accurate as written.

B is the only option that makes the one failing refusal outside `resourceErr` reach
`ReconcileBlocked`, fires the critical alert for a pod collision and keeps the collision visible on
a pass another step blocks, through the existing single evaluator and at S. A saves one size step
at the price of a permanent D5 exception and a blind critical alert.

**Answer:** _open_

## Not verified

- Nothing was run: no `Reconcile`-level test, no Kind reproduction of a collision.
- Option B: the length of the orphan-delete re-adoption window was not measured; that the existing
  `Reconcile` tests stay green (their pods are owned by the test StatefulSet via `ownedByTestSts`)
  and that Chaos Mesh pod kills produce no false positive are read, not run.

## Related

- T18: status fields stay frozen while the rolling update returns its error.
- T40, T42: also edit ADR 0020; whichever change lands later re-reads its line numbers.
- T70: corrects a stale ADR 0020 description in `docs/security/isolation-and-tenancy.md`.
