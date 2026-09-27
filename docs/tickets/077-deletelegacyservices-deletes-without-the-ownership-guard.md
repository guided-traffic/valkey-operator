---
id: T77
title: deleteLegacyServices deletes without the controller proof and the UID precondition that ADR 0006 requires
state: analysed       # facts read, security class derived per case, both options costed
severity: low         # a Service is deleted only when it carries this CR's UID in an ownerReference
security: hardening   # no principal gains anything the garbage collector does not already give it (Impact); the mechanism is already public in tracked files
threat: "no attack path today: the delete needs this CR's server-assigned UID in an ownerReference on <cr> or <cr>-read, which only a principal who may write that Service can put there, and that principal can already have the garbage collector delete it; the fix additionally closes the read-then-delete race on the cache (ADR 0006 D8) and the non-controller ownerReference case (ADR 0006 D2)"
urgency: now          # ADR 0006 D1 states a false fact (three unguarded sites, one exists); after that decision-free correction it becomes later
effort: S             # option A: about ten lines in one function, three fixtures, three new unit tests, the doc updates
blocked-by: decision  # Q1; the ADR 0006 D1 correction needs none
filed-from: T42
opened: 2026-09-27
decided:
done:
---

# T77 - deleteLegacyServices deletes without the controller proof and the UID precondition that ADR 0006 requires

## Current state

Every pass of a CR that is not being deleted ends the step "Services" with the ungated step
"legacy Service cleanup"
([`valkey_controller.go:813`](../../internal/controller/valkey_controller.go#L813)).
`deleteLegacyServices`
([`valkey_controller.go:936-962`](../../internal/controller/valkey_controller.go#L936-L962))
reads the Services `<cr>` and `<cr>-read` from the manager cache, walks **all** ownerReferences of
each, and on the first one carrying the CR's UID calls `r.Delete(ctx, svc)` by name, NotFound
counting as success (the delete at `:955`). Two deviations from ADR 0006:

1. **Any ownerReference, not the controller one** (D2 requires `metav1.IsControlledBy`).
2. **No UID precondition** (D1, D8). The read is cache-backed (`Owns(&corev1.Service{})`,
   [`valkey_controller.go:2991`](../../internal/controller/valkey_controller.go#L2991)), so the
   delete by name can hit an object that took the name after the cache last saw the old one.

`deleteIfOwned`
([`foreign_object.go:182-203`](../../internal/controller/foreign_object.go#L182-L203)) does both
correctly (`IsControlledBy`, then `Delete` with `client.Preconditions{UID}`, a Conflict logged as
"already replaced") and is used by four other cleanups (`valkey_controller.go:641`, `:722`,
`:2130`, `:2154`). `:955` is the only one of the seven `r.Delete` sites in `internal/controller`
without a UID precondition.

What the step is for: only `v1.0.0` and `v1.0.1` created a Service `<cr>` (ClusterIP, selector
`SelectorLabels(v, ComponentValkey)`, **controller** ownerReference). No operator build ever
created `<cr>-read`; the comments at `valkey_controller.go:935` and `:940` calling it the "old read
service" are wrong. No released operator wrote a CR ownerReference onto an existing Service, no
current builder produces either name, and `reconcileService` refuses a Service it does not control
([`valkey_controller.go:1253-1258`](../../internal/controller/valkey_controller.go#L1253-L1258)).
The operator may delete Services cluster-wide
([`clusterrole.yaml:38-49`](../../deploy/helm/valkey-operator/templates/clusterrole.yaml#L38-L49)).

Documentation: the gap is stated correctly in ADR 0006 Status (`0006:32-34`), Residual risks
(`0006:336-341`), the index row (`docs/adr/README.md:71`) and
[`isolation-and-tenancy.md:163-164`](../security/isolation-and-tenancy.md). ADR 0006 D1
(`0006:103-104`) is false: it says three pre-existing sites do not satisfy D1; the ADR's own
Residual risks mark two of them closed, and only this one is left.

Tests: `TestReconcile_DeletesLegacyClientService`, `TestReconcile_DeletesLegacyReadService`
([`valkey_controller_test.go:534`](../../internal/controller/valkey_controller_test.go#L534),
`:567`) and `TestDeleteLegacyServices_PropagatesDeleteError`
([`resource_reconcile_test.go:935`](../../internal/controller/resource_reconcile_test.go#L935))
build the ownerReference without `Controller: true`, so they pass only because of deviation 1.
Integration
([`sidecar_services_test.go:366-373`](../../test/integration/sidecar_services_test.go#L366-L373))
and `TestE2E_LegacyServiceCleanup`
([`sidecar_test.go:590-598`](../../test/e2e/sidecar_test.go#L590-L598)) already use the controller
shape `v1.0.x` wrote. No test pins the UID precondition of `deleteIfOwned`: the controller-runtime
fake client ignores a UID precondition, and
`TestDeleteIfOwned_ToleratesAReplacementUnderTheName` returns a Conflict whatever options arrive.
Only an interceptor that captures the delete options can pin it (as
[`foreign_object_test.go:239-242`](../../internal/controller/foreign_object_test.go#L239-L242)
does for the RoleBinding).

Impact, per case (the operator reports none of them on the CR):

- **May create `valkeys`, nothing on Services:** no path; a foreign Service never carries this
  CR's server-assigned UID.
- **May write a Service:** adding the CR's UID gets it deleted, but a dangling ownerReference gets
  the garbage collector to do the same. No escalation.
- **Read-then-delete race:** only after a direct upgrade from `v1.0.x`; a Service created under
  `<cr>` inside the cache lag is deleted instead of the legacy one. The one who loses it is the
  one who created it.
- **Deliberate non-controller ownerReference** on a user Service `<cr>`/`<cr>-read`: deleted on
  the next pass instead of with the CR. A surprise, not a security matter.

## Required changes

### Independent of the open questions

- ADR 0006 D1 (`docs/adr/0006-delete-only-what-the-operator-owns.md:103-106`): state that one
  pre-existing site, `deleteLegacyServices`, does not yet satisfy D1. Then set this ticket's
  urgency to `later`.

### Depends on the answers

Under A:
- `deleteLegacyServices` calls `r.deleteIfOwned(ctx, v, svc, "legacy Service")` per existing name,
  replacing the loop at `:951-960`; the comments stop calling `<cr>-read` an old read Service.
- The three unit fixtures above gain `Controller: true`.
- New unit tests: the delete carries `Preconditions.UID` equal to the Service UID (interceptor
  capture); a Service with this CR's UID in a non-controller ownerReference is not deleted; a
  Conflict on the delete returns nil.

Under B:
- Remove the step at `:813`, the function, its six unit tests, the integration subtest and
  `TestE2E_LegacyServiceCleanup`; update ADR 0035 (`0035:87-88`) and
  `docs/developer/reconcile-loop.md:89`.
- One unit test in `foreign_object_test.go` capturing the options `deleteIfOwned` sends, so its
  precondition stays pinned for the four remaining cleanups.

Both: ADR 0006 Status amendment, D1 and the Residual risk at `0006:336-341` closed, the index row
`docs/adr/README.md:71` and its State, and `isolation-and-tenancy.md:163-164` reading "every
delete" without the exception.

Verification: `make test-unit` green; revert check - restore the old loop with the new tests in
place, the precondition, non-controller and Conflict tests go red; mutation - drop
`client.Preconditions` from `foreign_object.go:193`, the new precondition test goes red.
Integration and `TestE2E_LegacyServiceCleanup` stay green under A. Every `r.Delete` site in
`internal/controller` carries a UID precondition (A) or `:955` is gone (B). `make lint`,
`make cyclo`.

## Open questions

### Q1: Is the legacy Service cleanup routed through deleteIfOwned or removed?

The step runs on every pass but has a genuine object to delete only on the first pass after a
direct upgrade from `v1.0.0`/`v1.0.1`, and only for `<cr>`. The repository states no minimum
release a direct upgrade may start from.

- **A - route through `deleteIfOwned`** (recommended): both deviations close, genuine legacy
  Services are still deleted. Cost S. A foreign Service named `<cr>` produces one Info log line
  per pass, as `cleanupMetricsService` already does for `<cr>-metrics`.
- **B - remove the step, the function and its tests**: net negative code, two cached reads per
  pass gone. Costs a product call: a direct upgrade from `v1.0.x` is unsupported or keeps one
  stale `<cr>` Service (it selects every data pod like `<cr>-all` and goes with the CR).

A closes the only ADR 0006 exception without deciding an upgrade-source floor, which is a
release-policy decision and not one for a hardening ticket. Once such a floor is decided, B is the
obvious follow-up.

**Answer:** _open_

## Not verified

- The length of the cache window behind the race: not measured.
- The garbage-collector behaviour for a dangling ownerReference: read from upstream
  documentation, not measured.
- Whether any installation still runs `v1.0.x` and upgrades directly: not knowable from the
  repository.

## Related

- T42: its static-analysis net carries `deleteLegacyServices`/`Delete` as an allowlist entry,
  removed when this lands.
- T33: its mutation B1 ("`deleteLegacyServices` returns nil without deleting") is rechecked
  against the new body; under B it goes with the integration subtest.
- T70: collects the other tracked statements the code contradicts; the ADR 0006 D1 sentence stays
  here because the same section is edited.
