---
id: T60
title: ownership guard gaps - a refused pod-name collision is not reported through ReconcileBlocked, and the legacy Service cleanup deletes without the guard
state: in-progress    # Q1 shipped (B, 2026-09-28: reportPodNameCollision); Q2 open
severity: low         # both refusals and deletes are narrow; the reports and one delete path are weaker than documented
security: hardening
threat: "no attack path: the legacy Service delete needs this CR's server-assigned UID in an ownerReference, which only a principal who may write that Service can place and who can already have the garbage collector delete it (the pod-name-collision report is now on ReconcileBlocked, Q1 shipped)"
urgency: now          # ADR 0006 D1 states a false site count; the correction is decision-free, after it later
effort: S             # Q1 shipped; the recommended option of Q2 is S, the ADR 0006 correction XS
blocked-by: decision  # Q2 only
filed-from: orchestrator verification during the ticket enrichment
opened: 2026-09-27
decided:
done:
---

# T60 - ownership guard gaps - a refused pod-name collision is not reported through ReconcileBlocked, and the legacy Service cleanup deletes without the guard

**Scope.** [ADR 0020](../adr/0020-write-only-what-the-operator-owns.md) and
[ADR 0006](../adr/0006-delete-only-what-the-operator-owns.md) require every write and delete onto
a generated name to prove control first, and every failing refusal to be reported with its own
`ReconcileBlocked` reason. Two places deviate: one refusal that protects correctly but reports
weaker than documented, and one delete that skips the guard. Both carry false ADR sentences that
can be corrected without a decision.

- **Pod-name collision report**: the rolling update's refusal of a foreign pod under a generated
  pod name never reaches `ReconcileBlocked`, and ADR 0020 misstates who gets the `PodNotOwned`
  Event.
- **Legacy Service cleanup**: `deleteLegacyServices` deletes without `IsControlledBy` and without
  the UID precondition, and ADR 0006 D1 miscounts the unguarded sites.

## Current state

### Pod-name collision report — resolved (2026-09-28)

Shipped under Q1 = B. A pod at a generated ordinal name the StatefulSet did not create is now
reported through `ReconcileBlocked=True/ForeignObject` by the `reportPodNameCollision` resource
step, so the critical `ValkeyReconcileBlocked` alert sees it; the roll holds on the pod rather
than failing the pass (ADR 0020 D9, ADR 0002 D13). The `PodNotOwned` Event still fires only for
a data-pod-labelled pod — the condition, not the Event, is the complete report.

### Legacy Service cleanup

Every pass of a CR not being deleted ends the step "Services" with the ungated step "legacy Service
cleanup" ([`valkey_controller.go:813`](../../internal/controller/valkey_controller.go#L813)).
`deleteLegacyServices`
([`valkey_controller.go:936-962`](../../internal/controller/valkey_controller.go#L936-L962)) reads
`<cr>` and `<cr>-read` from the manager cache, walks **all** ownerReferences of each, and on the
first carrying the CR's UID calls `r.Delete(ctx, svc)` by name, NotFound counting as success
(`:955`). Two deviations from ADR 0006:

1. **Any ownerReference, not the controller one** (D2 requires `metav1.IsControlledBy`).
2. **No UID precondition** (D1, D8). The read is cache-backed (`Owns(&corev1.Service{})`,
   [`valkey_controller.go:2991`](../../internal/controller/valkey_controller.go#L2991)), so the
   delete by name can hit an object that took the name after the cache last saw the old one.

`deleteIfOwned`
([`foreign_object.go:182-203`](../../internal/controller/foreign_object.go#L182-L203)) does both
(`IsControlledBy`, then `Delete` with `client.Preconditions{UID}`, a Conflict logged as "already
replaced") and serves four other cleanups (`valkey_controller.go:641`, `:722`, `:2130`, `:2154`).
`:955` is the only one of the seven `r.Delete` sites in `internal/controller` without a UID
precondition.

What the step is for: only `v1.0.0` and `v1.0.1` created a Service `<cr>` (ClusterIP, selector
`SelectorLabels(v, ComponentValkey)`, **controller** ownerReference). No build ever created
`<cr>-read`; the comments at `valkey_controller.go:935` and `:940` calling it the "old read
service" are wrong. No released operator wrote a CR ownerReference onto an existing Service, no
current builder produces either name, and `reconcileService` refuses a Service it does not control
([`valkey_controller.go:1253-1258`](../../internal/controller/valkey_controller.go#L1253-L1258)).
The operator may delete Services cluster-wide
([`clusterrole.yaml:38-49`](../../deploy/helm/valkey-operator/templates/clusterrole.yaml#L38-L49)).

**Documentation.** The gap is stated correctly in ADR 0006 Status (`0006:32-34`), Residual risks
(`0006:336-341`), the index row (`docs/adr/README.md:71`) and
[`isolation-and-tenancy.md:163-164`](../security/isolation-and-tenancy.md). ADR 0006 D1
(`0006:103-104`) is false: it names three pre-existing unguarded sites, the ADR's own Residual
risks mark two closed, and only this one is left.

**Tests.** `TestReconcile_DeletesLegacyClientService`, `TestReconcile_DeletesLegacyReadService`
([`valkey_controller_test.go:534`](../../internal/controller/valkey_controller_test.go#L534),
`:567`) and `TestDeleteLegacyServices_PropagatesDeleteError`
([`resource_reconcile_test.go:935`](../../internal/controller/resource_reconcile_test.go#L935))
build the ownerReference without `Controller: true` and pass only because of deviation 1.
Integration
([`sidecar_services_test.go:366-373`](../../test/integration/sidecar_services_test.go#L366-L373))
and `TestE2E_LegacyServiceCleanup`
([`sidecar_test.go:590-598`](../../test/e2e/sidecar_test.go#L590-L598)) already use the
controller shape `v1.0.x` wrote. No test pins the UID precondition of `deleteIfOwned`: the
controller-runtime fake client ignores it, and
`TestDeleteIfOwned_ToleratesAReplacementUnderTheName` returns a Conflict whatever options arrive.
Only an interceptor capturing the delete options can pin it (as
[`foreign_object_test.go:239-242`](../../internal/controller/foreign_object_test.go#L239-L242)
does for the RoleBinding).

**Impact, per case** (the operator reports none of them on the CR):

- May create `valkeys`, nothing on Services: no path; a foreign Service never carries this CR's
  server-assigned UID.
- May write a Service: adding the CR's UID gets it deleted, but a dangling ownerReference gets the
  garbage collector to do the same. No escalation.
- Read-then-delete race: only after a direct upgrade from `v1.0.x`; a Service created under `<cr>`
  inside the cache lag is deleted instead of the legacy one, and its creator is the one who loses
  it.
- Deliberate non-controller ownerReference on a user Service `<cr>`/`<cr>-read`: deleted on the
  next pass instead of with the CR. A surprise, not a security matter.

## Required changes

### Pod-name collision report — shipped (Q1 = B, 2026-09-28)

Done: `reportPodNameCollision` resource step; ADR 0020 D9 (hold + report), its "One reporter"
paragraph and Status amendment; ADR 0002 D13; `docs/developer/reconcile-loop.md` step table;
`docs/operations/status.md` `ForeignObject`; `docs/security/isolation-and-tenancy.md`; the
entry-scan comment reworded to the hold. Tests as listed under Q1.

### Legacy Service cleanup

- ADR 0006 D1 (`docs/adr/0006-delete-only-what-the-operator-owns.md:103-106`): state that one
  pre-existing site, `deleteLegacyServices`, does not yet satisfy D1. Decision-free, do it with Q2.
- Q2 = A: `deleteLegacyServices` calls `r.deleteIfOwned(ctx, v, svc, "legacy Service")` per
  existing name, replacing the loop at `:951-960`; the comments stop calling `<cr>-read` an old
  read Service. The three unit fixtures gain `Controller: true`. New unit tests: the delete carries
  `Preconditions.UID` equal to the Service UID (interceptor capture); a Service with this CR's UID
  in a non-controller ownerReference is not deleted; a Conflict on the delete returns nil.
- Q2 = B: remove the step at `:813`, the function, its six unit tests, the integration subtest and
  `TestE2E_LegacyServiceCleanup`; update ADR 0035 (`0035:87-88`) and
  `docs/developer/reconcile-loop.md:89`. One unit test in `foreign_object_test.go` capturing the
  options `deleteIfOwned` sends, so its precondition stays pinned for the four remaining cleanups.
- Either answer: ADR 0006 Status amendment, D1 and the Residual risk at `0006:336-341` closed, the
  index row `docs/adr/README.md:71` and its State, and `isolation-and-tenancy.md:163-164` reading
  "every delete" without the exception.

### Verification

- `make test-unit`, `make lint`, `make cyclo` green.
- Q1 = B: each new `Reconcile` test fails with the step removed.
- Q2: revert check - restore the old loop with the new tests in place, and the precondition,
  non-controller and Conflict tests go red (A); mutation - drop `client.Preconditions` from
  `foreign_object.go:193`, and the new precondition test goes red. Integration and
  `TestE2E_LegacyServiceCleanup` stay green under A. Every `r.Delete` site in
  `internal/controller` carries a UID precondition (A), or `:955` is gone (B).

## Open questions

### Q1: Is a pod-name collision the rolling update refuses reported through `ReconcileBlocked`? (pod-name collision report)

Today it reaches the CR only as phase `Error` with the pod in `status.message`. The choice decides
whether it also carries the condition and fires the critical alert; it does not change the refusal,
the retry cadence of a data-pod collision, the single `PodNotOwned` emitter or the status freeze
(T18).

- **A - Accept and record.** D5 names the pod door as the one failing refusal reported by phase and
  message only. Cost XS, no code. `ValkeyReconcileBlocked` stays blind to pod collisions, and a
  collision on a pass another step blocks leaves no trace.
- **B - Report it through the one evaluator (recommended).** A report-only resource step, placed
  last next to "TLS material"
  ([`valkey_controller.go:563-572`](../../internal/controller/valkey_controller.go#L563-L572)). Per
  tier (data always, Sentinel when `spec.sentinel.enabled`) it `Get`s the StatefulSet, skips one
  that is absent or foreign (reported by their own steps), `Get`s each pod in
  `[0, *Spec.Replicas)` and returns `foreignObjectError("Pod", name)` for a pod `podIsOurs`
  refuses. A read error other than NotFound is returned, so `:276` cannot clear a standing
  `ForeignObject` on a pass that measured nothing (ADR 0027). No Event (the condition message names
  the pod). The error joins `resourceErr`, so `:276` writes `True/ForeignObject` and the next clean
  pass clears it; the registry stays at one evaluator. The walk mirrors `scanTierTLSMaterial`
  ([`tls_material.go:317-360`](../../internal/controller/tls_material.go#L317-L360)), on cached
  reads; the rolling update keeps its own refusal at the delete sites. Cost S. Consequences:
  - The phase message reads `Failed to reconcile resources: …` instead of `Rolling update error: …`.
  - With the rule enabled the critical alert fires after 15 minutes; its summary "The operator
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
    `Error`, the pod surviving, and the clear to `False` after the pod is deleted.
  - Docs in the same change: ADR 0020 D9's "One reporter" paragraph (`0020:486-499`), its
    fail-direction table (`:472-476`, a row for the step), the Consequences bullet (`:589-594`),
    the Residual-risks entry (`:800-805`, closed); ADR 0002 D13 (`0002:355-360`) worded so that
    `ReconcileBlocked` also covers a held pod name the roll refuses; a row in the step table of
    [`docs/developer/reconcile-loop.md`](../developer/reconcile-loop.md) (`:84-96`). `status.md:37`
    becomes accurate as written.

B is recommended because it is the only option that brings the one failing refusal outside
`resourceErr` to `ReconcileBlocked` and the critical alert, through the existing single evaluator,
at S; A saves one size step at the price of a permanent D5 exception and a blind alert.

**Answer:** B (Hans, 2026-09-28), **shipped**. `reportPodNameCollision` is the last resource
step: per applicable tier it reads the ordinal range, treats a foreign or absent StatefulSet as
absent, and returns `foreignObjectError("Pod", name)` for the first pod `podIsOurs` refuses, so
the one evaluator writes `ReconcileBlocked=True/ForeignObject` and the critical alert sees it; a
read error other than NotFound is returned so a standing report is not cleared on silence
(ADR 0027). ADR 0020 D9 and its "One reporter" paragraph, ADR 0002 D13 and the
`docs/developer/reconcile-loop.md` step table are amended. Tests:
`TestReconcile_ReportsADataPodNameCollisionThroughReconcileBlocked`,
`TestReconcile_ReportsASentinelPodNameCollisionThroughReconcileBlocked`,
`TestReportPodNameCollision_SkipsTheSentinelTierWhenDisabled`,
`TestReportPodNameCollision_TreatsAForeignStatefulSetAsAbsent` (removing the step leaves them
red on the condition). The embargoed security finding that depended on this step as its report
landed in the same change.

### Q2: Is the legacy Service cleanup routed through `deleteIfOwned` or removed? (legacy Service cleanup)

The step runs every pass but has a genuine object to delete only on the first pass after a direct
upgrade from `v1.0.0`/`v1.0.1`, and only for `<cr>`. The repository states no minimum release a
direct upgrade may start from.

- **A - route through `deleteIfOwned` (recommended).** Both deviations close, genuine legacy
  Services are still deleted. Cost S. A foreign Service named `<cr>` produces one Info log line per
  pass, as `cleanupMetricsService` already does for `<cr>-metrics`.
- **B - remove the step, the function and its tests.** Net negative code, two cached reads per
  pass gone. Costs a product call: a direct upgrade from `v1.0.x` is unsupported or keeps one stale
  `<cr>` Service (it selects every data pod like `<cr>-all` and goes with the CR).

A is recommended because it closes the only ADR 0006 exception without deciding an upgrade-source
floor, a release-policy decision; once such a floor is decided, B is the obvious follow-up.

**Answer:** _open_

## Not verified

- Nothing was run for the collision report: no `Reconcile`-level test, no Kind reproduction.
- Q1 = B: the length of the orphan-delete re-adoption window was not measured; that the existing
  `Reconcile` tests stay green (their pods are owned via `ownedByTestSts`) and that Chaos Mesh pod
  kills produce no false positive are read, not run.
- The length of the cache window behind the legacy Service race: not measured.
- The garbage-collector behaviour for a dangling ownerReference: read from upstream documentation,
  not measured.
- Whether any installation still runs `v1.0.x` and upgrades directly: not knowable from the
  repository.

## Related

- T18: status fields stay frozen while the rolling update returns its error.
- T34: its mutation B1 ("`deleteLegacyServices` returns nil without deleting") is rechecked against
  the new body; under Q2 = B it goes with the integration subtest.
- T40: also edits ADR 0020; whichever change lands later re-reads its line numbers.
- T43: also edits ADR 0020; its static-analysis net carries `deleteLegacyServices`/`Delete` as an
  allowlist entry, removed when Q2 lands.
- T40: collects other tracked statements the code contradicts, including a stale ADR 0020
  description in `docs/security/isolation-and-tenancy.md`; the ADR 0006 D1 sentence stays here
  because the same section is edited.
