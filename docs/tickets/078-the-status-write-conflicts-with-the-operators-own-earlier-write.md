---
id: T78
title: persistStatus writes with the resourceVersion of a cached refresh, so a CR write the cache has not delivered fails the pass with a 409
state: analysed
severity: low         # nothing is lost: every field persistStatus writes is a level the next pass recomputes
security: none        # the 409 fails closed; a stale status is refused, never written
urgency: later        # rule 4, cheap known fix (R1, S)
effort: S             # R1: one read, one doc comment, one unit test, an ADR 0002 amendment, one docs paragraph
blocked-by: decision  # Q1; the items independent of it are not blocked
filed-from: T33 and T59
opened: 2026-09-27
decided:
done:
---

# T78 - persistStatus writes with the resourceVersion of a cached refresh, so a CR write the cache has not delivered fails the pass with a 409

## Current state

**The refresh.** `updateStatus`
([`valkey_controller.go:2181`](../../internal/controller/valkey_controller.go#L2181)) reads the
data StatefulSet; if it is missing or foreign it returns through `updatePhase` -> `writePhase`
([`valkey_controller.go:2189-2201`](../../internal/controller/valkey_controller.go#L2189-L2201)).
Otherwise it "refreshes" the CR with `r.Get` into the caller's `v`
([`valkey_controller.go:2203-2206`](../../internal/controller/valkey_controller.go#L2203-L2206),
comment "Refresh the Valkey object to avoid conflicts."). `r.Get` is the cache-backed manager
client ([`main.go:115`](../../cmd/main.go#L115); no type is excluded from the cache), so the read
replaces the caller's object in place, resourceVersion included, with the cached copy.

**The write.** `updateStandaloneStatus` and `updateHAStatus` compute the status in memory and end
in `persistStatus`
([`valkey_controller.go:2548-2571`](../../internal/controller/valkey_controller.go#L2548-L2571)),
which calls `r.Status().Update(ctx, v)` at
[`valkey_controller.go:2570`](../../internal/controller/valkey_controller.go#L2570) with the
resourceVersion of the refresh and no retry. Nothing writes the CR between the refresh and the
write. Of the three CR status writers (`:2570`, `writePhase` at `:2628`, `writeStatusCondition` at
`:2709`), only `writeStatusCondition` retries
([`valkey_controller.go:2679-2691`](../../internal/controller/valkey_controller.go#L2679-L2691)).

**The trigger** is any CR write the cache has not delivered when the refresh runs:

1. **A write earlier in the same pass.** Every operator write decodes the stored object into `v`,
   so `v` is current afterwards; the refresh then replaces it with the older cached copy. Writers
   that can run before the refresh: the empty-phase `Provisioning` write
   ([`valkey_controller.go:256-260`](../../internal/controller/valkey_controller.go#L256-L260)),
   `setReconcileBlockedCondition`
   ([`reconcile_blocked.go:118-148`](../../internal/controller/reconcile_blocked.go#L118-L148)),
   every `setStatusCondition` and `writeStatusCondition` caller (among them the
   `SentinelUpdatePending` clear at the end of a Sentinel roll,
   [`rolling_update.go:5212-5224`](../../internal/controller/rolling_update.go#L5212-L5224)), and
   the rolling-update annotation writes on `v`. Several log nothing on success.
2. **A write of the previous pass.** The pass starts from a cached copy
   ([`valkey_controller.go:225-226`](../../internal/controller/valkey_controller.go#L225-L226));
   a pass that starts before the informer delivered the previous status write refreshes into the
   same stale copy.

**On the 409** the error travels unchanged to `Reconcile`
([`valkey_controller.go:300-301`](../../internal/controller/valkey_controller.go#L300-L301)).
controller-runtime re-queues rate-limited (5 ms, doubling, capped at 30 s,
[`ratelimiter.go`](../../internal/controller/ratelimiter.go#L16)), increments
`controller_runtime_reconcile_errors_total` and logs `ERROR Reconciler error`.

**The 409 is protective.** A status `Update` replaces the whole status; a write from a copy that
predates a condition write would revert that condition. Therefore:

- `RetryOnConflict` around the existing call, or a fresh read that re-sends the status computed
  from the stale copy, reverts the condition the 409 protected.
- `status.conditions` has no `+listType=map` marker
  ([`valkey_types.go:1124-1126`](../../api/v1/valkey_types.go#L1124-L1126)), so a merge patch or
  server-side apply replaces the whole list as well.
- An `Update` without a resourceVersion is refused as invalid for a custom resource (the fake
  client refuses it with a conflict).

A fix must give the write a current base, or re-apply the computed fields onto one.

**An uncached reader already exists.** `APIReader`
([`valkey_controller.go:84-92`](../../internal/controller/valkey_controller.go#L84-L92), wired at
[`main.go:116`](../../cmd/main.go#L116)) is documented for one class of read, the delete gate's
live look (ADR 0026 D5, `liveTerminatingPod` in
[`rolling_update.go:2111-2141`](../../internal/controller/rolling_update.go#L2111-L2141)); it is
nil in unit tests that do not wire it. A GET through it is a consistent read, costs one API
request with no client-side throttle (`QPS = -1`), and needs no RBAC change (the chart grants
`get` on `valkeys`).

**Observed.** In one green CI run of `Integration Tests (envtest)`, four bare CR 409s, all
attributed to `:2570` by elimination. In the wds18 fleet upgrade log, one of three 409s is
`:2570`: the pass that landed the `SentinelUpdatePending=False` clear (form 1); one of eight
Sentinel roll completions in that log ended this way. The other two wds18 409s came from
`setRollingUpdateState`'s metadata `Update`
([`rolling_update.go:3418`](../../internal/controller/rolling_update.go#L3418)), a different
write that this ticket does not change.

**Impact.**

- Each occurrence is one `ERROR Reconciler error` line, one increment of the error counter
  (indistinguishable from a real write failure, and the signal
  [ADR 0001](../adr/0001-continue-reconciling-past-a-rejected-write.md) D5 calls alertable) and
  one extra full pass with a health check against every pod. No shipped alert reads the counter.
- The status lands one pass late; nothing is lost.
- A blocked pass returns the 409 joined with its resource error; the user-visible messages are
  built from the resource error alone.
- After the refresh `v` holds the stale copy. Nothing downstream reads it today except
  `valkey.Status.Phase`; [T35](035-master-records-lag-the-real-master.md)'s decided B2 will write
  the `known-master` annotation from `v` after `persistStatus` and inherit the stale base when the
  status write was skipped.

**Two records are misleading.** The refresh comment at
[`valkey_controller.go:2203`](../../internal/controller/valkey_controller.go#L2203) holds only when
the cache is newer than `v`; in form 1 the cached read causes the conflict.
[ADR 0002](../adr/0002-surface-a-blocked-reconcile-on-the-cr.md) D7's title "A failed status
write never ends the pass" is wider than its body, which scopes the rule to the empty-phase write
and `setStatusCondition`; `persistStatus` does return its error.

## Required changes

### Independent of the open questions

- [docs/developer/reconcile-loop.md](../developer/reconcile-loop.md), section "The status write":
  state that the write carries the resourceVersion of the refresh, that a conflict fails the pass,
  and why the 409 must not be retried on the same object.

### Depends on the answers

Under R1:

- `updateStatus`: read the refresh through `r.APIReader.Get`, falling back to `r.Get` when the
  reader is nil (the `liveTerminatingPod` precedent,
  [`rolling_update.go:2118`](../../internal/controller/rolling_update.go#L2118)); rewrite the
  refresh comment to say what the read is for.
- `APIReader` doc comment
  ([`valkey_controller.go:84-92`](../../internal/controller/valkey_controller.go#L84-L92)): name
  this second class of read.
- ADR 0002: amend with the rule that the status write's base is read from the API server, why, and
  the scope of D7. Code comments and tests cite ADR 0002, never this ticket.
- reconcile-loop.md: the paragraph above, rewritten for R1.
- Unit test in `internal/controller/status_phase_test.go`, next to the interceptor harness
  ([`status_phase_test.go:35`](../../internal/controller/status_phase_test.go#L35)): `Client`
  wraps the fake with an `interceptor.Funcs.Get` that returns a pre-write snapshot of the CR;
  `APIReader` is the plain fake.
  - Case 1 (same pass): a `ReconcileBlocked` clear is written, then `updateStatus` runs with a
    status change pending; no error, the write lands, `ReconcileBlocked` is still `False`.
  - Case 2 (previous pass): the snapshot predates an earlier `persistStatus` write; no error.
  - A nil `APIReader` keeps today's behaviour (one assertion).
- Mutations (ADR 0017): (a) switch the refresh back to `r.Get`: both cases fail with a conflict;
  (b) keep `r.Get` and, on a conflict at `:2570`, copy the resourceVersion of an `APIReader` read
  onto `v` and write again: case 1 fails on a reverted `ReconcileBlocked`. Revert both, tests pass.
- `make test-unit`, `make lint`, `make cyclo` green.

Under R2: the owned-field retry in `persistStatus`, two unit tests, one mutation, the ADR 0002
amendment, the reconcile-loop.md paragraph.

Under R3: an ADR 0002 residual-risk entry recording the self-inflicted refusal and D7's scope, and
the refresh comment corrected in the same change.

## Open questions

### Q1: How does the status write get a current base?

Today the status is computed on a cached copy and written with its resourceVersion, so the
operator's own recent writes make it fail with 409. The fix cannot simply retry, because the 409
is what keeps a condition written moments earlier from being reverted. A genuine third-party
conflict stays a refusal under R1 and R3.

- **R1 - read the refresh through the `APIReader` (recommended).** The computation starts from the
  stored object, so neither form of the trigger can conflict; a remaining conflict is genuine.
  Cost S; one uncached GET per pass that reaches `updateStatus`. `Ready`'s `ObservedGeneration`
  may name a generation this pass's resource steps did not apply for one pass, as the cached read
  already can.
- **R2 - on conflict, re-read and re-apply the fields the computation owns.** No extra read on the
  common path, and a genuine conflict resolves in the same pass. Cost M; the owned-field list is a
  second registry every status field must join (T35 adds `RWServiceMisrouted`), and a missed field
  is silently dropped. The retry reads the same lagging cache.
- **R3 - leave it.** No code; every self-inflicted conflict keeps costing an ERROR line, a counter
  increment and a full extra pass.

R1 removes the cause instead of retrying past it, needs no field list and cannot miss a field; a
pass reaching `updateStatus` already dials every pod, so one GET is small. No ordering alternative
exists, because form 2 is a write of the previous pass.

**Answer:** _open_

## Not verified

- The rate on a live fleet over a steady-state period; one fleet log and one CI run were read.
- How fast the informer delivers a same-pass write; matters only for R2's retry window.

## Related

- [T33](033-integration-tests-read-the-cache-after-a-write.md) - same mechanism in `writePhase`
  (its D3), where a retry is the right fix; R1's reader is an option there too.
- [T59](059-status-readyreplicas-is-compared-against-itself.md) - A-prime moves the `prevStatus`
  capture next to the same refresh `Get`; composes with every option in either order.
- [T35](035-master-records-lag-the-real-master.md) - B2 writes after `persistStatus` and gets a
  current base under R1.
