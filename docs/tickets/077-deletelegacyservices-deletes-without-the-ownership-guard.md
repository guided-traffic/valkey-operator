---
id: T77
title: deleteLegacyServices deletes without the controller proof and the UID precondition that ADR 0006 requires
state: analysed       # facts re-read at 84a39c2, the security class derived per case below, both options costed and one marked (History 2026-09-27)
severity: low         # if never fixed: a Service is deleted only when it carries this CR's UID in an ownerReference; outside the upgrade from v1.0.0/v1.0.1 that needs a principal who may already write, and so get rid of, that Service (Impact)
security: hardening   # derived, not in doubt: a principal who may create valkeys cannot put this CR's UID onto a Service it does not own, and a principal who can gains nothing the garbage collector does not already give it (Impact, cases 1 and 2); the mechanism is already published in tracked files (ADR 0006:32, :336-341, docs/adr/README.md:71, docs/security/isolation-and-tenancy.md:163-164), so no embargo would apply
threat: "no attack path today: a principal who may create Valkey CRs cannot make the operator delete a Service it does not own, because the delete needs this CR's server-assigned UID in an ownerReference on <cr> or <cr>-read, which only a principal who may create, update or patch that Service can put there, and that principal can already have the garbage collector delete it; the fix would additionally close the read-then-delete race on the cache (ADR 0006 D8) and the non-controller ownerReference case (ADR 0006 D2)"
urgency: now          # rule 1, second clause: ADR 0006 D1 (docs/adr/0006-delete-only-what-the-operator-owns.md:103-104) states that three pre-existing delete sites "do not yet satisfy" D1, and at 84a39c2 one does (every other r.Delete in internal/controller sends a UID precondition, grep below); the correction is decision-free (work list item 1), and once it lands the urgency is recomputed to later by rule 4 (option A is a cheap known fix, no product call)
effort: S             # option A: about ten lines in one function, three unit fixtures gain Controller: true, three new unit tests with a revert check, and the ADR 0006 / ADR index / security page updates in the same change
blocked-by: decision  # the one decision below (keep the cleanup behind deleteIfOwned, or remove it); work list item 1 needs none
filed-from: T42 (ticket 042, "deleteLegacyServices" under Fact and work list item 4) during the re-verification of 2026-09-27 at 84a39c2
opened: 2026-09-27
decided:              # not recorded - no decision yet
done:                 # not recorded - not done
---

# T77 - deleteLegacyServices deletes without the controller proof and the UID precondition that ADR 0006 requires

Filed on 2026-09-27 from ticket 042 (the static-analysis test net for the standing constraints),
whose re-verification at `84a39c2` found the gap and recorded it as its work list item 4 without
a file. Everything ticket 042 and its run records held about the finding is moved here; ticket
042 ~~is to keep~~ keeps only a pointer for its allowlist entry. ~~At filing, 042 still records the finding
as unfiled (its work list item 4 and the Seeding measurement); that pointer is written in 042,
not here (work list item 4).~~ *(Sweep 2026-09-27: 042's host update wrote that pointer, in its
Work list item 4, the Seeding measurement, Fact, Decision 1 A and "Explicitly out of scope".)*

## Fact

**Mechanism.** Every pass of a CR that is not being deleted runs the step "Services"
([`valkey_controller.go:556`](../../internal/controller/valkey_controller.go#L556); a pass on a
CR with a `deletionTimestamp` returns before the resource steps,
[`:238-242`](../../internal/controller/valkey_controller.go#L238-L242)), and
`reconcileServices` ends it with the step "legacy Service cleanup", which has no `when:` gate
([`valkey_controller.go:813`](../../internal/controller/valkey_controller.go#L813);
`runReconcileSteps` runs a step without `when` unconditionally,
[`:517-528`](../../internal/controller/valkey_controller.go#L517-L528)). `deleteLegacyServices`
([`valkey_controller.go:936-962`](../../internal/controller/valkey_controller.go#L936-L962))
reads the Services named `<cr>` and `<cr>-read` in the CR's namespace
([`:938-944`](../../internal/controller/valkey_controller.go#L938-L944)), and for each one that
exists walks **all** its ownerReferences; the first whose `UID` equals the CR's UID leads to
`r.Delete(ctx, svc)` **without** `client.Preconditions`, a NotFound on the delete counting as
success ([`:951-958`](../../internal/controller/valkey_controller.go#L951-L958), the delete at
`:955`). It differs from the rule in two ways:

1. **Any ownerReference, not the controller one.** ADR 0006 D2 makes the controller
   ownerReference (`metav1.IsControlledBy`) the provenance proof; the loop accepts a
   non-controller reference with the CR's UID as well.
2. **No UID precondition.** ADR 0006 D1 and D8 require the delete to carry the UID of the object
   the decision was made on. The `Get` at `:944` reads the manager cache (the controller
   `Owns(&corev1.Service{})`,
   [`valkey_controller.go:2991`](../../internal/controller/valkey_controller.go#L2991), and
   `cmd/main.go` configures no cache exception), so the delete by name can land on a different
   object that took the name after the cache last saw the old one.

The shared helper that does both is `deleteIfOwned`
([`foreign_object.go:182-203`](../../internal/controller/foreign_object.go#L182-L203)):
`IsControlledBy`, then `Delete` with `client.Preconditions{UID: &uid}`, a Conflict read as
"already replaced", logged and not failed. Four cleanups already use it
([`valkey_controller.go:641`](../../internal/controller/valkey_controller.go#L641), `:722`,
`:2130`, `:2154`).

**Verified (read at `84a39c2` unless stated):**

- **It is the only unguarded delete.** `grep -n "r.Delete(" internal/controller/*.go` over the
  non-test files gives seven sites; six carry `client.Preconditions{UID: …}`
  ([`foreign_object.go:193`](../../internal/controller/foreign_object.go#L193), `:307`,
  [`pdb.go:265`](../../internal/controller/pdb.go#L265),
  [`valkey_controller.go:1188`](../../internal/controller/valkey_controller.go#L1188), `:1657`,
  `:1716`, `:2097`), and `:955` does not.
- **Its age.** `git blame -L 934,962` attributes all 29 lines to `ce97f1b` ("feat: reliable
  valkey service (#9)", 2026-02-28); the first tag containing it is `v1.1.0` (2026-02-28).
- **What it was written for: one name, not two.** `v1.0.0` (2026-02-18) and `v1.0.1`
  (2026-02-20), both ancestors of `ce97f1b` and superseded by `v1.1.0` on 2026-02-28, created a
  client Service named `<cr>` (`ClientServiceName` returns `v.Name`; `reconcileClientService`
  is called from the pass, `git show v1.0.1:internal/controller/valkey_controller.go`, line
  199), ClusterIP with the selector `SelectorLabels(v, ComponentValkey)`
  (`git show v1.0.1:internal/builder/service.go`), through `controllerutil.SetControllerReference`,
  so a genuine legacy `<cr>` Service carries the **controller** ownerReference (with
  `blockOwnerDeletion`). **No operator build ever created `<cr>-read`.** `BuildReadService`
  existed in the builder of both releases, but nothing outside its own unit test ever called it:
  `git log -S "BuildReadService(" HEAD` finds only `88b721b` (adds the builder and its test in
  `internal/builder/sentinel_test.go`) and `ce97f1b` (removes both); with `--all` it adds only
  `411248f` on the unmerged `origin/feat/kubernetes-services`, which removes them as well; and
  `git grep BuildReadService v1.0.0` / `v1.0.1` over non-test files finds only
  `internal/builder/service.go`. The name
  appears in those releases only as a certificate DNS name
  (`git show v1.0.1:internal/builder/certificate.go`, line 88). So the `<cr>-read` half of the
  step has never had a genuine object to delete; the code comment calling it the "old read
  service" (`valkey_controller.go:935`, `:940`) reads as if one had existed.
- **No released operator ever wrote a CR's ownerReference onto a Service that already existed.**
  The `v1.0.0`/`v1.0.1` `reconcileService` update path wrote only `Spec.Ports`, `Spec.Selector`
  and `Labels` (`git show v1.0.0:` and `v1.0.1:internal/controller/valkey_controller.go`,
  `reconcileService`). A scan of the `reconcileService` body in all 104 tags finds no
  `OwnerReferences`/`SetOwnerReferences` in any of them, and after `v1.0.1` no tag's
  `internal/controller` calls `BuildClientService`, `BuildReadService`, `ClientServiceName` or
  `ReadServiceName` (loop over `git tag` with `git show <tag>:…` and `git grep <tag>`, run at
  filing review, 2026-09-27).
- **No current builder produces either name.** The generated Service names at `84a39c2` are
  `<cr>-headless`, `<cr>-sentinel-headless`
  ([`labels.go:148-153`](../../internal/common/labels.go#L148-L153)), `<cr>-all`, `<cr>-rw`,
  `<cr>-r` and `<cr>-metrics`
  ([`service.go:17-34`](../../internal/builder/service.go#L17-L34)). ADR 0035 records the same
  (`0035:87-88`: "The operator does not create that Service: it deletes it as a legacy name").
- **No current write stamps this CR's ownerReference onto a Service it does not control.**
  `reconcileService` refuses a Service it does not control before any update
  ([`valkey_controller.go:1253-1258`](../../internal/controller/valkey_controller.go#L1253-L1258)).
  The two places that set ownerReferences on an existing object, `reconcileServiceMonitor` and
  `reconcileCertificate` (`:699`, `:1928`), sit behind `IsControlledBy` (`:686`, `:1908`) and
  are not Services.
- **The operator may delete Services cluster-wide.** The chart ClusterRole grants
  `create, delete, get, list, patch, update, watch` on `configmaps`, `services`,
  `serviceaccounts`
  ([`clusterrole.yaml:38-49`](../../deploy/helm/valkey-operator/templates/clusterrole.yaml#L38-L49)).
- **The gap is documented in tracked files, correctly:** ADR 0006 Status
  (`0006:32-34`, "One item stays open"), D1 (`0006:104-106`), Residual risks (`0006:336-341`),
  the ADR index row (`docs/adr/README.md:71`) and
  [`isolation-and-tenancy.md:163-164`](../security/isolation-and-tenancy.md) ("every delete
  except `deleteLegacyServices` proves ownership and sends a UID precondition").
- **One of those statements is false at `84a39c2`.** ADR 0006 D1 (`0006:103-104`): "Three
  pre-existing sites do not yet satisfy it and are tracked under Residual risks". The ADR's own
  Residual risks mark two of the three closed (`reconcileSidecarRoleBinding`, "Closed
  2026-08-21"; the name-only cleanups, "Closed 2026-08-22", `0006:314-335`), and the grep above
  shows one unguarded delete left. The Status (`0006:32`) already says one; D1 was not
  updated.
- **The tests that pin today's behaviour.** Unit:
  `TestReconcile_DeletesLegacyClientService` and `TestReconcile_DeletesLegacyReadService`
  ([`valkey_controller_test.go:534`](../../internal/controller/valkey_controller_test.go#L534),
  `:567`) and `TestDeleteLegacyServices_PropagatesDeleteError`
  ([`resource_reconcile_test.go:935`](../../internal/controller/resource_reconcile_test.go#L935))
  build the legacy Service with an ownerReference that has **no** `Controller: true`, so they
  pass only because of deviation 1; `TestReconcile_DoesNotDeleteUnownedLegacyService`
  (`valkey_controller_test.go:600`, no ownerReference at all),
  `TestDeleteLegacyServices_PropagatesGetError` and
  `TestDeleteLegacyServices_SkipsServiceOwnedByAnotherInstance`
  (`resource_reconcile_test.go:917`, `:959`, another UID) do not depend on it. No test asserts a
  precondition on this delete. Integration
  ([`sidecar_services_test.go:366-373`](../../test/integration/sidecar_services_test.go#L366-L373))
  and e2e `TestE2E_LegacyServiceCleanup`
  ([`sidecar_test.go:590-598`](../../test/e2e/sidecar_test.go#L590-L598)) build the ownerReference
  with `Controller: true` and `BlockOwnerDeletion: true`, the shape `v1.0.x` wrote.
- **No test pins the precondition of `deleteIfOwned` either**, the helper option A routes this
  delete through. The unit tier asserts a UID precondition at four places, and none of them
  reaches `deleteIfOwned`: the RoleBinding recreate
  ([`foreign_object_test.go:239-242`](../../internal/controller/foreign_object_test.go#L239-L242),
  the delete at `valkey_controller.go:1188`), `deleteOwnedPod` (`foreign_object_test.go:1124`),
  the PDB cleanup (`pdb_test.go:778-782`) and the legacy Sentinel Certificate and Secret
  (`certificate_reconcile_test.go:815-819`, deletes at `valkey_controller.go:1657`, `:1716`).
  `TestDeleteIfOwned_ToleratesAReplacementUnderTheName` (`foreign_object_test.go:898-911`)
  returns a Conflict from an interceptor whatever options arrive, so it stays green without the
  precondition. The controller-runtime fake client does not enforce a UID precondition at all:
  `sigs.k8s.io/controller-runtime@v0.25.1/pkg/client/fake/client.go:721-733` checks only
  `Preconditions.ResourceVersion`. So only an interceptor that captures the delete options can
  pin it, and dropping `client.Preconditions` from `foreign_object.go:193` would, by reading,
  leave the unit tier green today for all four `deleteIfOwned` cleanups (not run).
- **Carried from ticket 042's run (reproduced there by two independent `go/ast` scans at
  `84a39c2`, the source of the scan was not kept):** the pair `deleteLegacyServices`/`Delete`
  (`valkey_controller.go:955`) is one of the 19 unguarded `(function, verb)` pairs under the net's
  rule as first written and one of the 8 allowlist entries left under its Decision 1 option A.
- **No ticket carried it before this one.** `grep -rln deleteLegacyServices docs/tickets` at
  the time of filing: 042, 033 (its B1 is a cache read in the integration test of this function,
  not the provenance gap) and archive/037 (records it as the open item only).

**Not verified:**

- Nothing was run against a cluster (no Kind, no kubectl, by the rules of this run), and no make
  target or `go test` was run. No container was started: no Valkey behaviour is claimed here.
- That the API server assigns `metadata.uid` on create and ignores a client-supplied one is
  upstream behaviour relied on here and not measured in this repository. Upstream documents
  that every object gets a distinct UID over the cluster's lifetime
  (<https://kubernetes.io/docs/concepts/overview/working-with-objects/names/#uids>).
- The garbage-collector behaviour Impact case 2 rests on is read from the upstream
  documentation, not measured:
  <https://kubernetes.io/docs/concepts/overview/working-with-objects/owners-dependents/>, "the
  owner reference is treated as absent, and the dependent is subject to deletion once all owners
  are verified absent" (fetched 2026-09-27).
- The length of the cache window of deviation 2 (the informer's propagation lag) was not
  measured.
- Whether any installation still runs `v1.0.0` or `v1.0.1` and would upgrade directly to a
  current release is not knowable from this repository. The repository states no minimum
  release a direct upgrade is supported from; [upgrading.md](../operations/upgrading.md)
  defines the supported mechanism (`helm upgrade`), not a source version, and the fleet-upgrade
  e2e starts from 1.12.8.

## Impact

Nothing breaks on a cluster that never ran `v1.0.0` or `v1.0.1`: there the step reads two names
per pass and finds nothing it may delete. Per case:

1. **A principal who may create `valkeys` in a namespace, and nothing on Services** — verb
   `create` (and `delete`) on a `Valkey` whose name equals a foreign Service `S` in that
   namespace, object `S`. **No path.** The operator deletes `S` only when `S` carries this CR's
   UID in an ownerReference. The UID is assigned by the API server when the CR is created (Not
   verified, above), no released operator ever wrote a CR's ownerReference onto an existing
   Service (Verified), and today's writes refuse a Service they do not control. Another CR's
   Service does not qualify either: it carries that CR's UID. Live today, and closed.
2. **A principal who may create, update or patch a Service `S`** (and read the CR's UID, which
   `get valkeys` or any owned object's ownerReferences shows) — verb `update`/`patch` on `S`,
   adding an ownerReference with the CR's UID; the operator deletes `S` on its next pass.
   **No escalation.** The same write with an ownerReference to an owner that does not exist
   makes the garbage collector delete `S` without the operator (upstream, Not verified above),
   and the same principal can rewrite `S`'s selector and ports, which takes the Service out of
   service just as well. Dormant as a security matter.
3. **The read-then-delete race, no hostile principal** — only on a cluster upgraded directly
   from `v1.0.0`/`v1.0.1`, whose genuine legacy `<cr>` Service the first pass deletes (there is
   no genuine `<cr>-read`, Fact). If a third party creates a Service under exactly `<cr>`
   before the cache has seen the delete,
   and a pass runs in that window, the cached old object still matches and the delete by name
   removes the new one. A hostile principal gains nothing from it: to plant the replacement it
   needs `create services`, and the Service lost is its own. Dormant, a window of the informer's
   lag, not measured.
4. **A deliberate non-controller ownerReference** — a user who ties the lifetime of their own
   Service `<cr>` or `<cr>-read` to the CR with a non-controller ownerReference sees it deleted
   on the next pass instead of when the CR goes. A surprise, not a security matter; dormant.

In every case the operator reports nothing on the CR (no Event, no condition), which is today's
behaviour and not changed by either option. The residual cost of the gap is that it is the one
documented exception to ADR 0006 and ADR 0020 D1, and the one allowlist entry ticket 042's net
would have to carry for a production path.

## Options

**The decision: what becomes of the legacy Service cleanup.**

**Mechanism today.** The step runs on every pass of every CR, reads `<cr>` and `<cr>-read`
from the cache and deletes either one when any of its ownerReferences carries the CR's UID,
by name. Its only productive run is the first pass after a direct upgrade from `v1.0.0` or
`v1.0.1` (current for ten days in February 2026), and only for `<cr>`, whose Service carries the
controller ownerReference; the `<cr>-read` read has never found a genuine object (Fact). The
choice changes whether that
delete is proven and preconditioned (A) or whether it exists at all (B). It does not change
what the operator does with any current Service, the ClusterRole (other cleanups still need
`delete services`), or the absence of an Event.

- **A. Route the delete through `deleteIfOwned` (recommended).** Replace the loop at
  `:951-960` with `r.deleteIfOwned(ctx, v, svc, "legacy Service")` for each name that exists.
  Cost: XS in code (the function shrinks), plus `Controller: true` on the three unit fixtures
  named under Fact, three new unit tests (the first to pin the precondition `deleteIfOwned`
  sends, Fact) and the doc updates (work list). Consequences: both
  deviations close — a non-controller ownerReference no longer triggers the delete (case 4 is
  left to the garbage collector at CR deletion, which is what the user asked for), and the
  delete carries the UID precondition, a Conflict read as "replaced" (case 3). Genuine legacy
  Services are still deleted, because `v1.0.x` wrote the controller reference (Verified); the
  integration and e2e fixtures already use that shape and stay valid. A foreign Service under
  `<cr>` now produces one Info log line per pass ("Skipping deletion: the name is held by an
  object this Valkey does not control"), where today's loop is silent; `cleanupMetricsService`
  already logs the same line for a foreign `<cr>-metrics`, and `<cr>` is a name an unrelated
  Service plausibly carries, so the line is accepted as the price of one guard shared by every
  cleanup. Ticket 042's net sees `deleteIfOwned` as a guard, so its allowlist loses this entry.
- **B. Remove the step, the function and its tests.** Cost: S, net negative code: the step at
  `:813`, the function, six unit tests, the integration subtest and `TestE2E_LegacyServiceCleanup`
  go, and ADR 0006, ADR 0035 (`0035:87-88` becomes half false) and
  `docs/developer/reconcile-loop.md:89` are rewritten. Consequences: the only unguarded delete
  disappears rather than being guarded, two cached reads per pass per CR go, and a future
  builder that names a Service `<cr>` or `<cr>-read` can no longer be deleted by this step on
  every pass (a trap under both today's code and A, which the new Service's own tests would
  catch at once). The price is a **product call this repository has never made**: that a
  direct upgrade from `v1.0.0`/`v1.0.1` is unsupported or left with one stale Service, `<cr>`
  (no `<cr>-read` ever existed). By reading, a leftover `<cr>` selects every data pod like
  `<cr>-all` (the selector is unchanged, `SelectorLabels` at `v1.0.1` and at `84a39c2`) and
  goes with the CR through its controller reference, so the harm is small, but the
  upgrade-source floor is a decision for the whole release policy, not for a hardening ticket:
  the repository fixes the upgrade mechanism (ADR 0014 D8, `helm upgrade`) and no source
  release.

**Why A over B.** The case for B is stronger than it first looks: half of the step (`<cr>-read`)
has been dead since it was written, the other half serves two releases that were current for
ten days and are thirteen minor lines behind `v1.13.1`, and A adds an Info line per pass for a
foreign Service named like the CR that today's loop does not print. The mark stays on A all
the same. Both remove the exception to ADR 0006 for the only cases that exist (1 to 4); A does
it without deciding which old releases a direct upgrade may start from, at a cost below B's, and
keeps the one migration path that ever had an object, for two cached reads per pass. B's extra
gains (the future-name trap, those two reads, the silence) are real but small, and B becomes the
obvious cleanup once a minimum upgrade source is decided elsewhere; at that point this step is
dead code either way.

**Not kept:**

- Adding only the UID precondition and keeping the any-ownerReference scan. Same cost as A,
  leaves deviation 1 and a second hand-written copy of what `deleteIfOwned` already does.
- A with `<cr>-read` dropped from the name list. It never had a genuine object, but dropping it
  saves one cached read per pass and turns a hardening fix into a behaviour change with churn in
  all three test tiers (`valkey_controller_test.go:567`, the integration and e2e legacy
  subtests) and in the ADR 0006 Residual risk text; whoever takes B removes it anyway.

## Decision

Not decided.

## Work list

1. *(no decision; docs only; discharges urgency rule 1)* ADR 0006 D1
   (`docs/adr/0006-delete-only-what-the-operator-owns.md:103-106`): mark "Three pre-existing
   sites do not yet satisfy it" in place and state that two of the three were closed on
   2026-08-21 and 2026-08-22 (as the Residual risks already say) and that
   `deleteLegacyServices` is the one left. Then recompute this ticket's urgency (rule 4:
   `later`) with a History entry.
2. *(waits on the decision)* Under A: `deleteLegacyServices` calls `deleteIfOwned` per existing
   name; the three unit fixtures (`valkey_controller_test.go:534`, `:567`,
   `resource_reconcile_test.go:935`) gain `Controller: true`; new unit tests: the delete carries
   `Preconditions.UID` equal to the Service's UID (capture through an interceptor, as
   [`foreign_object_test.go:239-240`](../../internal/controller/foreign_object_test.go#L239-L240)
   does; the fake client ignores a UID precondition, Fact), a Service with this CR's UID in a
   **non-controller** ownerReference is not deleted, and a Conflict on the delete returns nil;
   the function comment and the `:940` comment stop calling `<cr>-read` an "old read service"
   (none was ever created). Under B: the removals listed in option B, plus one unit test in
   `foreign_object_test.go` that captures the options `deleteIfOwned` sends, because under B
   nothing else would pin its precondition for the four cleanups that keep using it (Fact).
3. *(same change as item 2)* ADR 0006: an amendment in Status (dated), the D1 sentence of
   item 1 and the Residual risk at `0006:336-341` marked closed in place; the index row
   `docs/adr/README.md:71` and its State;
   [`isolation-and-tenancy.md:163-164`](../security/isolation-and-tenancy.md) ("every delete
   except `deleteLegacyServices`" becomes "every delete"). Under B additionally ADR 0035
   `0035:87-88` and `docs/developer/reconcile-loop.md:89`.
4. *(cross-ticket, no decision)* Ticket 042: ~~its work list item 4 and its Seeding measurement
   still say the finding is unfiled; they are replaced by a pointer to this ticket (an edit of
   042, not of this file).~~ *(done 2026-09-27 by 042's host update, checked in the sweep)* If
   042's test 3 lands first, its allowlist line
   `deleteLegacyServices`/`Delete` is removed in the change of item 2; if this lands first, 042's
   seeded allowlist starts at 7 entries. Ticket 033's B1 mutation ("`deleteLegacyServices`
   returns nil without deleting") is rechecked against the new body; under B, B1 goes with the
   integration subtest. Ticket 070 collects the other tracked statements the code contradicts;
   the ADR 0006 D1 sentence stays here, because item 3 edits the same section in the same
   change.
5. Close ([ADR 0034](../adr/0034-tickets-are-work-lists-that-get-archived.md)): the rule is
   already ADR 0006 D1, D2 and D8, so the extraction is item 3. Then
   `git grep -nwE 'T77|077'` outside `docs/tickets/`, and the move to `archive/`.

## Verification

- `make test-unit` green with the new tests. **Revert check (ADR 0017):** restore the old loop
  at `:951-960` with the new tests in place; the precondition test, the non-controller test and
  the Conflict test (the old loop returns the Conflict as an error) go red, the fixture tests
  stay green (they use the controller shape now). Mutation: drop `client.Preconditions` from
  `deleteIfOwned` (`foreign_object.go:193`); the new precondition test goes red. It is the only
  one that does: no existing test pins that precondition (Fact), so today this mutation survives
  the unit tier, by reading.
- `make test-integration` (`TestSidecarServicesRouting_Integration`, its legacy subtest) and
  the e2e `TestE2E_LegacyServiceCleanup` on both single-node legs stay green under A: they
  prove a genuine legacy Service is still deleted.
- `grep -n "r.Delete(" internal/controller/*.go` over non-test files: every site carries
  `client.Preconditions` (under A) or `:955` is gone (under B).
- `make lint`, `make cyclo`.
- Work list item 1: ADR 0006 `:103-106` no longer states an unguarded site that the grep does
  not find.

## History

- 2026-09-27: filed from ticket 042 (Fact entry "`deleteLegacyServices`", the Seeding
  measurement pair, Why item 3, the Not verified entry on its security class, work list item 4)
  and from that ticket's run records of the same day (verify, facts and design), during the
  re-verification at `84a39c2`. **Moved here:** the mechanism (`valkey_controller.go:936-962`,
  run every pass from `:813`, the loop at `:951-955` without `Preconditions`), its age
  (`ce97f1b`, 2026-02-28), the tracked documents naming it (ADR 0006 `:32`, `:336`,
  `docs/adr/README.md:71`, `isolation-and-tenancy.md:163-164`), the scan result that makes it one
  of 042's unguarded pairs, and 042's reasoned but unmeasured note that each harm path needs a
  principal who could already get rid of the Service, pointing to `hardening`. **Re-verified
  now, by reading at `84a39c2`:** all of that holds; additionally the step list (`:556`,
  `:517-528`), the cache-backed read (`Owns(&corev1.Service{})` at `:2991`), the seven
  `r.Delete` sites of which only `:955` lacks a precondition, the `v1.0.0`/`v1.0.1` legacy names,
  selectors and controller ownerReferences (one name, not two: corrected by the review below),
  the fact that no released `reconcileService` wrote
  ownerReferences onto an existing Service, today's Service names, the ClusterRole grant, and
  which tests depend on the non-controller shape (three unit fixtures) and which already use the
  controller shape (integration, e2e). **Security class derived per case: `hardening`**, not in
  doubt (Impact, cases 1 and 2), so the file is tracked and not embargoed; 042's
  "points to `hardening`" is confirmed, with the garbage-collector argument sharpened from
  "a principal who can delete the Service" to "a principal who can write it" (a dangling
  ownerReference suffices, upstream documentation, not measured). **New in this filing:** ADR
  0006 D1 (`0006:103-104`) still says three pre-existing sites do not satisfy D1, which the
  ADR's own Residual risks and the grep show false for two of them; that is rule 1 of the
  urgency table, hence `now`, and a decision-free work item. **Measured:** nothing; no cluster,
  no container (no Valkey behaviour involved), no make target. Options written: A (route
  through `deleteIfOwned`, recommended) and B (remove the step, needs an upgrade-source product
  call); the precondition-only variant not kept. **Adversarial review of the filing, the same
  day at `84a39c2`:** the load-bearing facts re-read and holding (the loop, the seven delete
  sites, the cache-backed read, the ClusterRole, the ADR lines, the fixtures, the upstream
  garbage-collector sentence re-fetched); one fact was wrong and is corrected under Fact: no
  operator build ever created `<cr>-read` (`BuildReadService` had no caller outside its unit
  test), so only `<cr>` is a genuine legacy Service, which narrows Impact case 3 and option B to
  one name; the claim that no released operator wrote an ownerReference onto an existing
  Service now rests on a scan of all 104 tags, not two; the Verification mutation claimed an
  existing `deleteIfOwned` precondition test in `foreign_object_test.go`, which does not exist
  (the fake client ignores a UID precondition), so the new test is the first to pin it and
  option B carries one of its own; the pass that skips a CR being deleted, the misleading code
  comment, the not-kept variant without `<cr>-read`, the 042 pointer still to be written and the
  sibling ticket 070 added. Security class, severity, urgency (rule 1), effort and the mark on
  A re-derived and unchanged.
  Sweep: 042 now carries the pointer to this file (Work list item 4, Seeding measurement, Fact,
  Decision 1 A, "Explicitly out of scope"), so the opening note and the first half of Work list item
  4 are struck as done; the rest of item 4 (the allowlist ordering, the 033 B1 recheck) stays open.
  Frontmatter unchanged.
