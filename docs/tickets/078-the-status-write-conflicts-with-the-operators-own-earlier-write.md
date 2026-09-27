---
id: T78
title: persistStatus writes with the resourceVersion of a cached refresh, so a CR write the cache has not delivered fails the pass with a 409
state: analysed       # 2026-09-27 at 84a39c2: mechanism re-read, all four bare CR 409s of the green CI run and one of the three wds18 409s attributed to this site by elimination, options costed with a marked best
severity: low         # nothing is lost by reading: every field persistStatus writes is a level the next pass (5 ms later) recomputes; the cost is a spurious ERROR "Reconciler error", a reconcile_errors_total increment and one extra full pass per occurrence
security: none        # the 409 fails closed (a stale status is refused, not written); a principal who can write the CR can already change its spec, and no shipped guarantee rests on the log line
urgency: later        # rule 4 (cheap known fix: R1, S). Rule 1 does not match: pre-existing since b0081d9 (2026-02-17, every release from v1.0.0), and no tracked statement was measured false (the ADR 0002 D7 title and the refresh comment at valkey_controller.go:2203 are misleading by reading, not measured false; Fact, Adjacent findings). Rule 2 does not: CI is green on 84a39c2 with these 409s and nothing gates a release. Rule 3 does not: severity low
effort: S             # R1: one read switched to the APIReader with a nil fallback, the APIReader doc comment, one unit test with two cases and two mutations, the ADR 0002 amendment, one reconcile-loop.md paragraph
blocked-by: decision  # Decision 1 in Options; the no-decision items of the Work list are not blocked
filed-from: T33 (section Adjacent findings, Work list item "file the persistStatus 409 as its own ticket") and T59 (Work list item 3), during the re-verification of 2026-09-27 at 84a39c2
opened: 2026-09-27
decided:              # not recorded - no decision yet
done:                 # not recorded - not done
---

# T78 - persistStatus writes with the resourceVersion of a cached refresh, so a CR write the cache has not delivered fails the pass with a 409

Filed on 2026-09-27 from [033](033-integration-tests-read-the-cache-after-a-write.md) (section
Adjacent findings) and [059](059-status-readyreplicas-is-compared-against-itself.md) (Work list
item 3), where the finding was recorded as a hypothesis and not filed. This file is now its
record. It is its own file and not an appendix under the filing rule: 033's D3 has the same
mechanism but decides `writePhase`, where a conflict retry is the right fix and here the wrong
one (Fact, "The 409 is protective"), and 059 edits the same function for a different mechanism
and a different decision. Everything below was read at `84a39c2` on `chore/maintenance-2026-09-27`
(`git diff --stat 84a39c2 -- internal/ cmd/ api/` is empty, so the cited code is the committed
code). Labels as in 033: **run** means executed, **read** means read in the tree, the module
cache or a saved log, **hypothesis** means neither.

## Fact

### Mechanism (read)

- **The refresh.** `updateStatus`
  ([`valkey_controller.go:2181`](../../internal/controller/valkey_controller.go#L2181)) reads the
  data StatefulSet ([`valkey_controller.go:2188`](../../internal/controller/valkey_controller.go#L2188));
  missing or foreign, it returns through `updatePhase` → `writePhase`
  ([`valkey_controller.go:2189-2201`](../../internal/controller/valkey_controller.go#L2189-L2201)).
  Otherwise it "refreshes" the CR
  ([`valkey_controller.go:2203-2206`](../../internal/controller/valkey_controller.go#L2203-L2206),
  comment "Refresh the Valkey object to avoid conflicts.", which holds only when the cache is
  newer than the caller's copy and produces the conflict when it is older) with `r.Get` into the
  caller's `v`.
  `r.Get` is the manager client ([`main.go:115`](../../cmd/main.go#L115), `mgr.GetClient()`;
  `managerOptions` at [`main.go:102-109`](../../cmd/main.go#L102-L109) disables the cache for no
  type), so the read is served from the informer cache and **replaces the caller's object in
  place**, resourceVersion included.
- **The write.** `updateStandaloneStatus` and `updateHAStatus` capture `prevStatus`
  ([`valkey_controller.go:2228`](../../internal/controller/valkey_controller.go#L2228),
  [`:2451`](../../internal/controller/valkey_controller.go#L2451)), compute the status in memory
  and end in `persistStatus`
  ([`valkey_controller.go:2548-2571`](../../internal/controller/valkey_controller.go#L2548-L2571)),
  which writes with `r.Status().Update(ctx, v)` at
  [`valkey_controller.go:2570`](../../internal/controller/valkey_controller.go#L2570): the
  resourceVersion of the refresh, no retry.
- **The three CR status writers** are `:2570` (`persistStatus`), `:2628` (`writePhase`, cached
  re-`Get` at `:2617`) and `:2709` (`writeStatusCondition`); only `:2709` retries
  (`retry.RetryOnConflict` at
  [`valkey_controller.go:2691`](../../internal/controller/valkey_controller.go#L2691); its doc
  comment [`valkey_controller.go:2679-2685`](../../internal/controller/valkey_controller.go#L2679-L2685)
  names exactly this 409: "a caller that updated the CR itself moments earlier reads back the
  version from before its own write"). `grep -rn 'Status()\.Update\|Status()\.Patch' internal cmd`
  without test files finds these three and nothing else.
- **Nothing writes the CR between the refresh and the write.** Between `:2204` and `:2570` every
  condition is set in memory (`meta.SetStatusCondition` at `:2241-2280`, `:2374-2402`,
  `:2468-2522`, and
  [`rw_service_report.go:50`](../../internal/controller/rw_service_report.go#L50), `:66`); there
  is no `r.Update`, `setStatusCondition` or `writeStatusCondition` call in that range.
- **The trigger is any CR write the cache has not delivered when `:2204` runs.** It comes in two
  forms, and the finding as first recorded ("something earlier in the pass wrote the CR") named
  only the first:
  1. **An earlier write of the same pass.** Every operator write of the CR decodes the stored
     object into the caller's `v` (controller-runtime v0.25.1 `targetZeroingDecoder`, read for
     033, Mechanism), so after it `v` is current. The refresh then replaces that current `v` with
     the older cached copy: the in-memory clobber, and the 409 follows. The writers that can run
     before `:2204` in a pass: the empty-phase `Provisioning` write
     ([`valkey_controller.go:256-260`](../../internal/controller/valkey_controller.go#L256-L260)),
     `setReconcileBlockedCondition` ([`valkey_controller.go:276`](../../internal/controller/valkey_controller.go#L276),
     [`reconcile_blocked.go:118-148`](../../internal/controller/reconcile_blocked.go#L118-L148)),
     every other `setStatusCondition` and `writeStatusCondition` caller (among them the
     `SentinelUpdatePending` clear at the end of a Sentinel roll,
     [`rolling_update.go:5212-5224`](../../internal/controller/rolling_update.go#L5212-L5224)),
     and the rolling-update annotation writes on `v`
     ([`rolling_update.go:1080`](../../internal/controller/rolling_update.go#L1080), `:1183`,
     `:2205`, `:2673`, `:3257`, `:3269`, `:3418`, `:3471`, `:3515`, `:3524`, `:4146`, `:4161`;
     `grep -n '\.Update(ctx' internal/controller/*.go` without test files, every non-CR target
     excluded by reading). Several of them log nothing on success (`setStatusCondition`,
     `ensureWaitBound` at `:1183`, `clearSyncWaitTimestamp` at `:2673`), so a pass whose log
     shows no write may still have written the CR.
  2. **A write of an earlier pass.** The pass starts from a cached copy
     ([`valkey_controller.go:225-226`](../../internal/controller/valkey_controller.go#L225-L226)),
     and a pass that starts before the informer delivered the previous pass's status write
     refreshes into the same stale copy.
- **What happens on the 409.** `updateStatus` returns the error unchanged, `reconcileWorkload`
  returns it ([`valkey_controller.go:369-370`](../../internal/controller/valkey_controller.go#L369-L370)),
  and `Reconcile` returns it
  ([`valkey_controller.go:300-301`](../../internal/controller/valkey_controller.go#L300-L301);
  in a blocked pass joined with the resource error at `:297`, after the one `Error` phase write
  at `:295`, which still runs). controller-runtime v0.25.1 then re-adds the request rate-limited,
  increments `controller_runtime_reconcile_errors_total` and
  `controller_runtime_reconcile_total{result="error"}`, and logs `ERROR Reconciler error`
  (`pkg/internal/controller/controller.go:486-497`, module cache). The rate limiter is the
  operator's own: 5 ms doubling per consecutive failure, capped at 30 s, reset by a successful
  pass ([`ratelimiter.go:16`](../../internal/controller/ratelimiter.go#L16),
  [`:38`](../../internal/controller/ratelimiter.go#L38),
  [`:71-79`](../../internal/controller/ratelimiter.go#L71-L79)). What the error skips is the pass
  tail's requeue choice (`:373-395`); the rate-limited retry re-enters sooner than any of those
  requeues.
- **The 409 is protective, and that decides the fix.** A status `Update` replaces the whole status,
  and a copy that predates a condition write carries the old condition, so a write that succeeded
  from that copy would revert the condition written moments earlier. Retrying the same object
  conflicts again; retrying with a fresh read but re-sending the status computed from the stale copy
  reverts the condition the 409 protected. `status.conditions` carries no `+listType=map` marker
  ([`valkey_types.go:1124-1126`](../../api/v1/valkey_types.go#L1124-L1126)), so a JSON merge patch
  or a server-side apply of the status would replace the whole list as well. An `Update` without a
  resourceVersion is no way out either: a custom resource refuses it as invalid ("must be specified
  for an update"; `customResourceStrategy.AllowUnconditionalUpdate` returns false,
  k8s.io/apiextensions-apiserver v0.37.1 `pkg/registry/customresource/strategy.go:268-271`, and the
  generic store's check, k8s.io/apiserver v0.37.1 `pkg/registry/generic/registry/store.go:734`,
  `:804`, module cache; the fake client refuses it with a conflict,
  `pkg/client/fake/client.go:1449-1517`, which lists no custom group). A fix therefore has to give
  the write a current base, or re-apply the pass's computed fields onto one; `RetryOnConflict`
  around the existing call is not a fix (033's D3 W1 is the right shape for `writePhase`, which
  writes only phase and message, and the wrong one here).
- **The clobber is harmless downstream today.** After `updateStatus` the pass reads only
  `valkey.Status.Phase` ([`valkey_controller.go:377-379`](../../internal/controller/valkey_controller.go#L377-L379)),
  and a blocked pass's `writePhase` re-reads the CR anyway (`:2617`). One decided, not yet
  implemented change will depend on `v` after `persistStatus`:
  [035](035-master-records-lag-the-real-master.md) decision 2 (B2) writes the `known-master`
  annotation after `persistStatus` on the Sentinel path. After a successful status write `v` is
  the write answer; after a skipped one (`:2566-2568`) it is the refreshed copy, so B2's `Update`
  inherits the stale base whenever the refresh returned one.
- **Origin.** The refresh and its comment date from `b0081d9` (2026-02-17, "feat: Core
  Reconciliation (Standalone)", comment at its `:211`), and `git tag --contains b0081d9` starts at
  `v1.0.0`: every release carries it.
- **An uncached reader exists on the reconciler.** `APIReader`
  ([`valkey_controller.go:84-92`](../../internal/controller/valkey_controller.go#L84-L92),
  wired at [`main.go:116`](../../cmd/main.go#L116)) is documented as existing "for exactly one
  class of read", the delete gate's live look (ADR 0026 D5,
  [`rolling_update.go:2111-2141`](../../internal/controller/rolling_update.go#L2111-L2141)), and
  is nil in unit tests that do not wire it; `newTestReconciler` wires it to the fake client
  ([`valkey_controller_test.go:98`](../../internal/controller/valkey_controller_test.go#L98)).
  controller-runtime v0.25.1 disables client-side rate limiting on the config `GetConfigOrDie`
  returns (`pkg/client/config/config.go:101-105`, `QPS = -1`, module cache; the manager is built
  from it, [`main.go:167`](../../cmd/main.go#L167)), so an uncached GET costs one API request and
  no client-side token. That GET carries no resourceVersion (`typedClient.Get`,
  `pkg/client/typed_client.go:189-201`, empty `GetOptions`), which the API server serves as
  "Most Recent", a consistent read that must include every write that has returned
  (kubernetes/website `content/en/docs/reference/using-api/api-concepts.md`, section "Semantics
  for get and list", read in a copy fetched on 2026-09-27). The chart already grants `get`
  on `valkeys` ([`clusterrole.yaml:10-16`](../../deploy/helm/valkey-operator/templates/clusterrole.yaml#L10-L16)).
  [ADR 0010](../adr/0010-every-rolling-update-wait-is-bounded.md) considered this reader for a
  condition write and did not take it
  ([`0010:675-680`](../adr/0010-every-rolling-update-wait-is-bounded.md#read-the-cr-through-an-uncached-apireader-before-the-status-write)):
  "a live API read on every condition write and a new field on the reconciler", when ordering
  already removed that conflict. The field has existed since ADR 0026 D5.

### Observed (read in two saved logs)

**CI, job `108655236971` of run `36331871661`** (`Integration Tests (envtest)` on `84a39c2`,
green; log read by the earlier run with the owner's authenticated, read-only
`gh run view 36331871661 --job 108655236971 --log`, saved in the session scratchpad, not in the
repository; the excerpts are quoted here). `grep 'the object has been modified'` finds four
`Reconciler error` lines whose whole error is
`Operation cannot be fulfilled on valkeys.vko.gtrfc.com "<name>": the object has been modified`,
none joined with a resource error, so all four passes were unblocked. Per pass, from every log
line carrying its reconcileID and the passes before it:

| CR | 409 pass | Log before the 409 | Attribution |
|---|---|---|---|
| `foreign-cm-test` | `90f9208c`, 16:08:37.2983 | `Creating StatefulSet` once, in pass `605a1483` (36.9436); five blocked passes (`6ffc8d15` … `3c26734f`, up to 37.1265) run the StatefulSet step without a second create, so the StatefulSet is in the cache; `90f9208c` logs `Creating ConfigMap` (37.2877, the test removed the foreign one) and then the bare 409 | **`:2570`, by elimination.** The StatefulSet is cached and owned, so `updateStatus` passes `:2188-2201`, and its only remaining CR write is `:2570`. The pass is the first unblocked one after six blocked ones (`605a1483` and the five after it), so by reading it cleared `ReconcileBlocked` (a same-pass write, form 1) before the refresh; that clear goes through `setStatusCondition`, which logs a failure, and none is logged. |
| `aa-test` | `7552fd79`, 16:08:34.0611 | create pass `95f302b3` (33.7639-33.9637); pass `c5792a8f` logs `Updating StatefulSet` at 34.0326 (by reading its reaction to the test's `antiAffinity.mode: soft` spec write, [`affinity_test.go:32-40`](../../test/integration/affinity_test.go#L32-L40): the second pass of the other three CRs updates no StatefulSet), so the StatefulSet is cached; `7552fd79` logs nothing before the error | **`:2570`, by elimination**, as above. Not a conflict with the test's own writes, by timing: the soft write landed before `c5792a8f` (which reacted to it), and the next one (`hard`) lands shortly before `dbcfd872` updates the StatefulSet again at 34.2911. Which operator write the cache had not delivered is not visible: form 2 (`c5792a8f`'s status write for the new generation) fits, and so does a same-pass write that logs nothing on success (form 1); hypothesis either way. |
| `obs-disabled-test` | `e16f7240`, 16:08:41.0029 | create pass `a5fc6900`, `Creating StatefulSet` at 40.9897, 13 ms earlier; `e16f7240` logs nothing else | **`:2570`, by elimination.** `e16f7240` ran the StatefulSet step (no resource error, so every step ran and succeeded) and logged neither `Creating StatefulSet` ([`valkey_controller.go:1302`](../../internal/controller/valkey_controller.go#L1302)) nor the TLS-record refusal ([`tls_material.go:107-109`](../../internal/controller/tls_material.go#L107-L109)), the two lines a cached NotFound at [`valkey_controller.go:1286`](../../internal/controller/valkey_controller.go#L1286) produces on its way to a create (the third way, a refused seccomp profile, is a resource error). So the StatefulSet `a5fc6900` created, with this CR as controller, was in the cache before `updateStatus` read the same cache, and `:2190`/`:2200` → `:2628` is excluded. Trigger most likely form 2 (the create pass's own phase writes, 13 ms earlier; hypothesis). |
| `sc-ha-svc` | `343f8e3c`, 16:08:44.7540 | create pass `62451f9d`, last line `Creating Sentinel StatefulSet` at 44.7254, 29 ms earlier; `343f8e3c` logs nothing else | **`:2570`, by elimination**, the same argument as `obs-disabled-test` (no `Creating StatefulSet` and no resource error in `343f8e3c`). Trigger most likely form 2 (hypothesis). |

The elimination rests on reading, not on an exhaustive trace. The only unwrapped CR writes whose
error reaches `Reconcile` are `:2570`, `writePhase` through `updateStatus`'s early returns (`:2190`,
`:2200`; every other `updatePhase` caller discards, logs or wraps its error, `grep -n
'updatePhase('`) and the rolling-update annotation writes; `persistKnownMaster` wraps its error
("persisting known master …",
[`rolling_update.go:1080-1087`](../../internal/controller/rolling_update.go#L1080-L1087)), the
`TopologyRestored` write logs `TopologyRestored write still conflicting` before it returns a
conflict and runs only in a roll's topology restore (`:4565-4580`), and the `SentinelUpdatePending`
clear turns a failure into a requeue (`:5217-5220`). Each annotation write needs a recorded roll
annotation or a roll in progress (for example `clearRollingUpdateState` writes only when one of ten
annotations is present,
[`rolling_update.go:3444-3459`](../../internal/controller/rolling_update.go#L3444-L3459)), and
`setRollingUpdateState` and `setFailoverTriggered` log `Setting rolling update state` before their
write ([`rolling_update.go:3412`](../../internal/controller/rolling_update.go#L3412), `:3509`).
envtest runs no pod, the four CRs carry no roll annotation, and no such line appears in those
passes. The four error texts were re-read in the saved log during the adversarial review of
2026-09-27: each is exactly `Operation cannot be fulfilled on valkeys.vko.gtrfc.com "<name>": the
object has been modified; please apply your changes to the latest version and try again`.

**wds18, the fleet upgrade to v1.13.0 (`ad81a47`) of 2026-09-26** (the operator log saved in the
scratchpad of session `538d7ed7` as `wds18-op.log`, the log
[035](035-master-records-lag-the-real-master.md) reads; ephemeral, excerpts quoted here;
`git diff ad81a47 84a39c2` changes only comment lines in `valkey_controller.go` and
`rolling_update.go`, so the line numbers below are those of `84a39c2` and the code is the one
that ran). Three `Reconciler error` lines, all "the object has been modified" (035 corrected its
count from four to three the same day):

- `valkey8-sentinal-tls`, pass `24818696`, 20:45:46: `Rolling update detected`,
  `New master verified with data`, `Setting rolling update state` `replacing-master`, then the
  409 (log lines 1202-1214).
- `valkey9-sentinal`, pass `0eed3b2a`, 20:45:49: `Failed to clear the recreation wait bound
  annotation` with the same 409 (logged and not returned,
  [`rolling_update.go:2205-2208`](../../internal/controller/rolling_update.go#L2205-L2208)), then
  `Setting rolling update state` `replacing-master`, then the returned 409 (lines 1289-1320).
- `valkey8-sentinal`, pass `ca350298`, 20:47:13: no line with its reconcileID before the error,
  but the line right before it (1911, same second) is the `SentinelUpdateComplete` Event for
  `valkey8-sentinal`. The passes before it (20:47:05-08) wait on the Sentinel roll's last pod;
  the next logged pass is at 20:50:02.

The first two are **not status writes**: the returned 409 comes from `setRollingUpdateState`'s
metadata `Update` ([`rolling_update.go:3418`](../../internal/controller/rolling_update.go#L3418),
called from `replaceRemainingPods` at
[`rolling_update.go:3029-3030`](../../internal/controller/rolling_update.go#L3029-L3030), which
returns it as the roll's error; `reconcileWorkload` writes the phase `Rolling update error: …`
through `updatePhase` at [`valkey_controller.go:337`](../../internal/controller/valkey_controller.go#L337)
and returns it); `Deleting remaining pod for rolling update`, the line after a successful state
write, is absent from both passes. That is the same stale-copy shape on a CR metadata write, a
different site and a different fix (Adjacent findings).

The third is **`:2570`, by elimination, and form 1 by reading**. The Event is emitted by
`finishSentinelRollingUpdate` only after `writeStatusCondition` landed the
`SentinelUpdatePending=False` clear and reported a change
([`rolling_update.go:5212-5224`](../../internal/controller/rolling_update.go#L5212-L5224)); the
function then returns an empty result, `runSentinelRollingUpdate` continues the pass
([`valkey_controller.go:472-485`](../../internal/controller/valkey_controller.go#L472-L485)),
`checkSteadyStateSplitBrain` returns at once on a Sentinel cluster
([`steady_state_master.go:154-156`](../../internal/controller/steady_state_master.go#L154-L156)),
and the pass reaches the refresh with a `v` that holds the clear, replaces it with a cached copy
that does not, and computes a phase other than the `Sentinel Rolling Update …` the roll left stored
(`recordSentinelUpdateProgress`, `rolling_update.go:5177-5179`): a write, and the 409. Had that
write succeeded it would have put `SentinelUpdatePending=True` back. The data roll of that CR had
completed at 20:46:47, and no `Setting rolling update state` line appears. Events carry no
reconcileID, so that the Event and the error belong to the same pass is inferred from the per-CR
serialisation and the shared second, not read. The same log holds eight `SentinelUpdateComplete`
Events (lines 1592-1911); only this one is followed by a 409, so the trigger needs the cache lag and
is not structural.

**Verified:**

- the mechanism above, every file:line at `84a39c2`, and the three status writers (grep);
- that nothing writes the CR between `:2204` and `:2570` (grep of the range and of
  `rw_service_report.go`);
- the error path and controller-runtime's handling of a returned error (module cache v0.25.1);
- the origin (`git log -S`, `git tag --contains`);
- all four bare 409s of the green CI run come from `:2570`, by elimination in the saved log and
  the code (`foreign-cm-test` and `aa-test` by the earlier run, `obs-disabled-test` and
  `sc-ha-svc` by the adversarial review of the same day, which re-read the passes);
- two of the three wds18 409s come from `rolling_update.go:3418`, not from a status writer, and
  the third from `:2570`, by elimination;
- that the 409 is protective (the whole-status `Update`, no list-map marker on conditions, and a
  custom resource refuses an `Update` without a resourceVersion);
- that the uncached read R1 uses is a consistent read, and that no RBAC change is needed for it;
- that a deterministic unit test is possible: the fake client refuses a stale resourceVersion
  (`pkg/client/fake/versioned_tracker.go:310-311`, module cache) and the interceptor harness
  exists ([`status_phase_test.go:35`](../../internal/controller/status_phase_test.go#L35)).

**Not verified:**

- which same-pass or earlier write the cache had not delivered in `aa-test`, `obs-disabled-test`
  and `sc-ha-svc` (form 1 or form 2), and that the `SentinelUpdateComplete` Event of line 1911
  belongs to pass `ca350298`;
- the rate on a live fleet: one fleet log, one upgrade, one of three 409s and one of eight
  Sentinel roll completions; nothing counted over a steady-state period;
- every elimination rests on the annotation guards, the error wrapping and the log lines of the
  other writers, not on a trace of every call path;
- that no CR can hit this 409 on every pass: by reading it cannot persist, because each failure
  doubles the delay and the cache catches up, and nothing was measured;
- that the informer could not deliver a same-pass write within the milliseconds before `:2204`
  (it did not in the five passes above; not measured);
- no test was written or run, no make target, no cluster; no docker measurement was taken,
  because nothing here is a claim about Valkey.

## Impact

- **A healthy fleet logs errors that are not failures.** Each occurrence is one
  `ERROR Reconciler error` line and one increment of `controller_runtime_reconcile_errors_total`,
  indistinguishable from a real write failure, plus one extra full pass, which re-runs every
  resource step and the health check against every data and Sentinel pod. No shipped alert reads
  the counter (every rule in
  [`prometheusrule.yaml`](../../deploy/helm/valkey-operator/templates/prometheusrule.yaml) reads
  `vko_valkey_*` series), so nothing shipped pages. An administrator who alerts on the counter
  itself, which [ADR 0001](../adr/0001-continue-reconciling-past-a-rejected-write.md) D5 calls
  the operator's only alertable signal and
  [ADR 0021](../adr/0021-per-resource-metrics-and-the-alert-that-was-missing.md) supplements with
  per-resource series, counts every occurrence as a failure; an operator reading the log cannot
  tell the two apart, and 035 had to account for three such lines. In the one fleet log read, one
  of eight Sentinel roll completions ended in this 409 (Observed).
- **The status lands one pass late.** Every field `persistStatus` writes (phase, message,
  `readyReplicas`, `masterPod`, `operatorVersion`, `observerReady`, the `Ready`,
  `RWServiceEmpty` and `SentinelPeersStale` conditions) is a level recomputed on every pass that
  reaches `updateStatus`, and the rate-limited retry comes 5 ms later. Nothing is lost by reading.
- **A blocked pass** returns the 409 joined with its resource error. The `ReconcileBlocked`
  message and the `Error` phase message are built from the resource error alone (`:276`, `:295`),
  so nothing user-visible changes.
- **The in-memory clobber** changes nothing downstream today (Fact); 035's B2, once implemented,
  writes from the refreshed copy when `persistStatus` skipped its write.
- **Security:** none. The 409 refuses a stale status and never writes one; the only other
  writers of the CR are principals who may update `valkeys` (users, GitOps controllers, the
  chart's pre-upgrade migrate hook, [`migrate.go:86`](../../cmd/migrate/migrate.go#L86)), whose
  write produces a genuine conflict that the next pass resolves, and who control the spec anyway.

## Options

### Decision 1: how the status write gets a current base

**What the code does today.** `updateStatus` replaces the caller's `v` with the cached copy at
`:2204`, the pass computes the status on it, and `persistStatus` writes it with that copy's
resourceVersion at `:2570`. Whenever the cache has not delivered a CR write of this pass or of
the previous one, the write is refused with 409 and the pass fails; the refusal is what keeps a
condition written moments earlier from being reverted.

**What the choice changes.** Whether the operator's own recent writes can make the status write
conflict; what happens to a conflict that remains; what `v` holds after the refresh.

**What it does not change.** No pod, template, hash, CRD, RBAC or chart changes, and nothing
rolls. A genuine conflict (a third party wrote the CR between the read and the write, a window
that includes the health check) stays a refusal that fails the pass under R1 and R3; R2 retries
it by re-applying the computed fields onto the third party's version. The `updatePhase` →
`writePhase` path at `:2190`/`:2200` → `:2628` is 033's D3 and is not touched; none of the
409s read here came from it (Observed).
The status computation, its strings and the `prevStatus` capture are not touched:
[059](059-status-readyreplicas-is-compared-against-itself.md)'s recommended A-prime moves the
capture to directly after this same refresh `Get` (before `:2213`) and composes with every option
below in either order, because R1 keeps the `Get` where it is and changes only its reader, R2
acts after the capture, and R3 changes nothing. The earlier note in 033 and 059 that "whichever
lands second re-anchors the capture" holds only for a fix that moves or repeats the refresh `Get`
before the computation, which no option below does.

- **R1 - read the refresh through the `APIReader` (recommended).** `:2204` reads with
  `r.APIReader.Get` into `v`, falling back to `r.Get` when the reader is nil (the
  `liveTerminatingPod` precedent, `rolling_update.go:2118`). The computation then starts from the
  stored object (a consistent read, Fact), so neither form of the trigger can produce a 409, the
  clobber becomes a refresh to a version at least as new as `v`, and a conflict that remains is a
  genuine one.
  - Cost S: one read and its nil fallback; the refresh comment rewritten to say what the read is
    for; the `APIReader` doc comment
    ([`valkey_controller.go:84-92`](../../internal/controller/valkey_controller.go#L84-L92)), which
    names one class of read, extended by this second one; an ADR 0002 amendment recording that
    the status write's base is read from the API server, why, and the class of read it adds (ADR
    0002 holds the status-write rules; ADR 0010's "considered" alternative concerns condition
    writes and stays as it is); the reconcile-loop.md paragraph (Work list); one unit test with two
    cases and two mutations (Verification). No RBAC, chart or CRD change.
  - Consequences: one uncached GET per pass that reaches `updateStatus`, with no client-side
    throttle (`QPS = -1`); a pass that reaches it is one that did not end on a rolling-update
    wait, so a healthy cluster pays it per watch event, and an unhealthy one per 10 s requeue
    (`:377-379`). `v.Generation` is the stored one, so the `Ready` condition's
    `ObservedGeneration` can name a generation the resource steps of this pass did not apply
    (a spec edit landing mid-pass); the cached refresh does the same whenever the cache already
    holds the edit, the over-claim lasts one pass, and the generation change queues that pass,
    as `setStatusCondition`'s doc comment already accepts for conditions
    ([`valkey_controller.go:2634-2640`](../../internal/controller/valkey_controller.go#L2634-L2640)).
    035's B2 gets a current base after a skipped write for free.
- **R2 - retry the write on a fresh object, re-applying the fields this computation owns.** On a
  conflict, `RetryOnConflict` re-reads the CR into a new object, copies onto it the fields the
  status computation owns (phase and message unless the pass is blocked, `readyReplicas`,
  `masterPod`, `operatorVersion`, `observerReady`, and the `Ready`, `RWServiceEmpty` and
  `SentinelPeersStale` conditions), compares the fresh status before and after, and writes; on
  success the result is copied back into `v` (the ADR 0009 D3 shape of
  `writeManualFailoverState`, [`rolling_update.go:4126-4167`](../../internal/controller/rolling_update.go#L4126-L4167)).
  - Cost M: the owned-field list, a compare per attempt, two unit tests and one mutation, the ADR
    0002 amendment. The list is a second place every in-memory status field has to be registered
    in, and it is about to grow: 035's decided decision 6 adds `RWServiceMisrouted` to the same
    in-memory evaluator (`reportRWServiceEndpoints`). A field left off the list is silently
    dropped from a retried write, the kind of omission the condition registry exists to catch.
    A delta of `prevStatus` against the computed status would avoid the list, and is not
    sensible: under today's order `readyReplicas` is assigned before the capture and never in
    the delta (059), and a delta against a stale base drops every verdict that equals the stale
    value.
  - Consequences: no extra read on the common path; the retry reads through the cache again,
    and when the cache is still behind after `retry.DefaultRetry`'s 5 × 10 ms the pass fails as
    today.
- **R3 - leave it.** Cost none, apart from an ADR 0002 residual-risk entry that records the
  self-inflicted refusal (Work list). Every self-inflicted conflict keeps costing an ERROR line, a
  counter increment and a full extra pass, and the operator log keeps mixing them with real
  write failures.

**Considered and not listed.** *Drop the refresh and write from `v` as the pass carries it:*
`v` is not current either, because the pass starts from a cached copy (`:225-226`) and every
`setStatusCondition` call re-reads the cache into `v` even when it writes nothing (`:2693`), as
does `writePhase` (`:2617`); it would also lose the one case the refresh serves today, a third
party's write the cache delivered after the pass started. *Write without a resourceVersion:* a
custom resource refuses the `Update` as invalid (Fact). *A status merge patch or a server-side
apply:* each replaces the whole conditions list and so reverts a condition written moments
earlier (Fact), which is the protection the 409 gives. *Re-run `updateStatus` with an uncached
read only after a 409:* it pays the health check twice per conflict to save one GET per pass,
and needs R1's read anyway. *Read uncached only in a pass that wrote the CR itself* (a flag on
`passState`): it saves the GET on most passes and misses form 2, the previous pass's write,
which fits three of the four CI occurrences (Observed). *Report a conflict from `persistStatus`
as a `RequeueAfter` instead of an error:* it hides the symptom and keeps the cause (the extra full
pass, the clobber), silences a genuine conflict along with the self-inflicted one, and is exactly
what [ADR 0001](../adr/0001-continue-reconciling-past-a-rejected-write.md) D5 rules out ("A
failing pass returns its error, never a hand-picked `RequeueAfter`"), so it would need that rule
re-decided for a log line; a fixed `RequeueAfter` would also lose the exponential spacing, and
`Result.Requeue`, which would keep it, is deprecated in controller-runtime v0.25.1
(`pkg/reconcile/reconcile.go:38-42`).

**R1 is marked best.** It removes the cause instead of retrying past it or hiding it: the operator's
own writes can no longer conflict with its status write, whichever pass made them, and the conflict
that remains is the genuine one the 409 exists for. The case for R2, the runner-up, is real: it adds
no read to a pass that does not conflict, and it resolves a genuine third-party conflict in the same
pass, where R1 still fails that pass. It does not win, because both of its gains are small against
its risk: a pass that reaches `updateStatus` with every pod ready already dials the pods for its
health check, so one consistent GET is not what a pass costs, and a genuine conflict inside the pass
window is rare and costs one rate-limited retry; while R2's owned-field list is a second registry
every in-memory status field must join, and the day one is missed the retried write drops it
silently, a correctness defect in the one path that exists to prevent a lost status. R2's retry also
reads the same cache that caused the conflict, so whether it fixes form 1 within `DefaultRetry`'s
roughly 50 ms depends on an informer lag nobody measured; R1 needs no list and cannot miss a field.
R1 is also smaller (S against M), and its cost is one uncached GET per pass that reaches
`updateStatus` against a reader the reconciler already has and a client with no client-side
throttle. ADR 0010's reason for not taking the same reader does not carry over: it weighed a live
read per condition write and a new reconciler field, when ordering already removed that conflict;
here it is one read per status pass, the field exists, and no ordering can help, because form 2 of
the trigger is a write of an earlier pass. It beats R3 because the cost is real and recurring (all
four bare 409s of one green CI run, one of eight Sentinel roll completions on wds18) and the fix is
S. Checkable: the unit test under Verification fails at `84a39c2` with a conflict and passes after
the change.

## Decision

Not decided.

## Work list

1. **XS, no decision needed:** [docs/developer/reconcile-loop.md](../developer/reconcile-loop.md),
   section "The status write" (`:141-174`), states that `updateStatus` "re-reads the CR" and that
   `persistStatus` writes the status, but not that the write carries the resourceVersion of that
   read, that a conflict fails the pass, or why the 409 must not be retried on the same object.
   Add those three sentences. Outside `docs/tickets/`, so not done in this run. Under R1 the
   paragraph is rewritten again by item 3.
2. **Done 2026-09-27, no decision needed:** moved out of this ticket and recorded in the ticket
   that owns it.
3. **Waits on Decision 1:** the chosen option's code change, its unit test, the ADR 0002
   amendment and the reconcile-loop.md paragraph, as listed under that option. Code comments and
   the test cite ADR 0002, never T78 ([ADR 0034](../adr/0034-tickets-are-work-lists-that-get-archived.md);
   [040](040-tracked-files-cite-work-items-instead-of-adrs.md)).
4. **Sequencing, no dependency:** 059's A-prime and this ticket edit neighbouring lines of
   `updateStatus`; either order works (Options, "What it does not change"). 033's D3 decides
   `writePhase` separately; if R1 is taken here, a `writePhase` that reads through the same
   reader is an option 033's D3 may want to weigh, and nothing here decides it. 035's B2 writes
   after `persistStatus`; under R1 it gets a current base after a skipped write.
5. **Closing** ([ADR 0034](../adr/0034-tickets-are-work-lists-that-get-archived.md)): the
   decision is extracted into ADR 0002 (item 3), reconcile-loop.md carries the subsystem
   knowledge, then the ticket moves to `archive/`. Under R3 the extraction is an ADR 0002
   residual-risk entry that records the refusal, then `dropped`.

## Adjacent findings

- **The rolling update's annotation writes use the same stale base.** On wds18 two passes failed
  with 409 on `setRollingUpdateState(replacing-master)`
  ([`rolling_update.go:3410-3418`](../../internal/controller/rolling_update.go#L3410-L3418)), one
  after `clearRecreationWait` had already hit the same 409 (Observed). Each writes CR metadata
  from the pass's cached `v` with no retry; the failed state write fails the roll step before the
  outgoing master's delete, writes the phase `Rolling update error: …` (`:337`), and the next pass
  repeats the step. Same shape, different write (metadata, not status), different consequence (a
  roll step and a transient `Error` phase) and a fix that belongs to the roll's own write rules
  (ADR 0009 D3 retries only the manual-failover write). Recorded here because it corrects the
  attribution 033 gave the wds18 409s; it is not this ticket's to decide, and none of this
  ticket's options changes those writes.
- **ADR 0002 D7's title is wider than its body.** "A failed status write never ends the pass"
  ([`0002:186-199`](../adr/0002-surface-a-blocked-reconcile-on-the-cr.md)); the body scopes the
  rule to the empty-phase write and `setStatusCondition`, and `persistStatus`'s failed write does
  return the error. Not counted as a false statement for the urgency: the rule protects that
  `reconcileResources` and `reconcileWorkload` run despite a failed status write, which holds,
  because `persistStatus` is the last step of the pass and the error skips only the requeue
  choice. The ADR 0002 amendment of item 3 should state that scope.
- **The refresh comment says the opposite of what the read does in form 1.** "Refresh the Valkey
  object to avoid conflicts." ([`valkey_controller.go:2203`](../../internal/controller/valkey_controller.go#L2203))
  is true only when the cache is newer than the caller's copy; after a same-pass write the cached
  read is what produces the conflict. Misleading by reading, not measured false, so it does not
  move the urgency. R1 rewrites it (item 3); under R3 it is corrected on its own in the same
  change as the ADR 0002 residual-risk entry.

## Verification

- **Unit test (R1),** in `internal/controller/status_phase_test.go` next to the interceptor
  harness (`:35`): the reconciler's `Client` wraps the fake client with an `interceptor.Funcs.Get`
  that returns, for the Valkey CR, a snapshot taken before a write; `APIReader` is the plain fake
  client. Case 1 (same pass): a `ReconcileBlocked` clear is written through the fake, then
  `updateStatus` runs with a status change pending; it returns no error, the status write lands,
  and `ReconcileBlocked` is still `False` afterwards. Case 2 (earlier pass): the snapshot predates
  a previous `persistStatus` write; `updateStatus` returns no error. At `84a39c2` both fail with a
  conflict (the fake refuses the stale resourceVersion, `versioned_tracker.go:310-311`), by
  reading.
- **Mutations** ([ADR 0017](../adr/0017-test-and-ci-policy.md)): (a) switch the refresh back to
  `r.Get`: both cases fail with a conflict; (b) the rejected "retry past it" shape: keep `r.Get`
  for the refresh and, on a conflict at `:2570`, copy the resourceVersion of an `APIReader` read
  onto `v` and write again: case 1 fails on a reverted `ReconcileBlocked` (the write lands with
  the stale conditions), which proves the test pins the protective half and not only the absence
  of an error. Revert both and the tests pass. A nil `APIReader` keeps today's behaviour (one
  assertion). *(Corrected in the adversarial review of 2026-09-27: the mutation first named here,
  clearing `v.ResourceVersion` before the write, cannot revert anything: the fake client refuses
  an empty resourceVersion for a custom resource with a conflict, and a real API server refuses it
  as invalid, Fact.)*
- `grep -rn 'APIReader\.' internal/controller --include='*.go' | grep -v _test` shows two
  readers, `liveTerminatingPod` and the refresh, and the field's doc comment names both classes of
  read.
- `make test-unit`, `make lint`, `make cyclo` green. Not a proof, but the expected effect: the
  bare CR 409s of `Integration Tests (envtest)` drop from four to none unless another site
  produces them (all four read here are `:2570`, Observed); a count is probabilistic and does not
  replace the mutation.

## History

- 2026-09-27: filed from [033](033-integration-tests-read-the-cache-after-a-write.md) (section
  Adjacent findings; Work list item "file the `persistStatus` 409 as its own ticket") and
  [059](059-status-readyreplicas-is-compared-against-itself.md) (Work list item 3, the cross-ticket
  note on the refresh `Get` A-prime anchors to) during the re-verification at `84a39c2`; state
  `analysed`. **Moved here:** the finding (the unretried cached refresh at `:2204` and the write at
  `:2570`), the three status writers and the one retry, the protective nature of the 409 and why
  `RetryOnConflict` is not the fix, the in-memory clobber as the second half of the shape "a status
  writer refreshes the caller's object from the cache" (the `writePhase` half stays with 033's D3),
  the 4 bare 409s of CI job `108655236971`, and the relation to 059's A-prime; the earlier run's raw
  audit, facts and design results for 033 and 059 were read and everything used from them is in this
  file. **Re-verified now, by reading at `84a39c2`:** every cite above; that the refresh is a cache
  read (`main.go:115`, no cache exclusion in `managerOptions`); that nothing writes the CR between
  `:2204` and `:2570`; the error path through controller-runtime v0.25.1 (`controller.go:486-497`)
  and the operator's rate limiter; the origin (`b0081d9`, every release from `v1.0.0`); the
  `APIReader` and its one documented class of read; the client-side rate limiting that
  `GetConfigOrDie` disables; the fake client's resourceVersion check and the interceptor harness.
  **Read in the saved logs:** the four CI passes with every line of their reconcileIDs and of the
  passes before them, and the three wds18 passes. **Corrected against the host tickets:** (1) the
  trigger is not only a write earlier in the same pass but any CR write the cache has not delivered,
  including the previous pass's; (2) the 4 bare 409s are no longer unattributed: all four come from
  `:2570` by elimination; (3) the finding is no longer a pure hypothesis: for `foreign-cm-test` the
  same-pass write is, by reading, the `ReconcileBlocked` clear of the first unblocked pass; (4) 033
  counted the wds18 409s as four and as relevant to this finding; 035 itself corrected the count to
  three, and two of those come from `setRollingUpdateState`'s metadata `Update`
  (`rolling_update.go:3418`), not from a status writer (recorded under Adjacent findings, to be
  filed), and the third comes from `:2570`; (5) "the fix is re-deriving the status on a fresh
  object" (033, 059) is one option (R2); an uncached read of the base (R1) avoids the conflict
  without re-deriving; (6) "whichever lands second re-anchors the capture" (033, 059) holds only for
  a fix that moves or repeats the refresh `Get`, which none of the options does. **Frontmatter
  derived:** severity low (nothing lost by reading), security none (the 409 fails closed), urgency
  later by rule 4 (rules 1-3 do not match, reasons in the comment), effort S (R1), blocked-by
  decision. **Measured:** nothing; no docker run, because no Valkey behaviour is claimed, and no
  test or make target was run. **Adversarial review, the same day, before the file was first
  committed:** re-read the four CI passes and the wds18 log and attributed `obs-disabled-test`
  (`e16f7240`) and `sc-ha-svc` (`343f8e3c`) to `:2570` (no `Creating StatefulSet` line and no
  resource error in the 409 pass, so the StatefulSet was cached), and `valkey8-sentinal`
  (`ca350298`) to `:2570` by elimination with the `SentinelUpdatePending` clear as its same-pass
  write (the `SentinelUpdateComplete` Event at line 1911; one of eight such Events in that log is
  followed by a 409); found that `aa-test`'s pass may equally be form 1, because several CR writes
  log nothing on success, and that the test's own spec writes are not the other party, by timing;
  added `:2673` and `:4161` to the pre-refresh writers; found that a custom resource refuses an
  `Update` without a resourceVersion (apiextensions-apiserver and apiserver v0.37.1, module cache),
  which corrected mutation (b) and the "Considered and not listed" entry; found that the uncached
  read is consistent (`typedClient.Get` sends no resourceVersion; the API concepts page) and needs
  no RBAC change; moved the former option R3 (report a conflict as a requeue) to "Considered and not
  listed" because ADR 0001 D5 rules it out and it keeps the cause, relabelled "leave it" from R4 to
  R3, and re-argued R1 against R2 as the runner-up, where the mark held; corrected the rate-limiter
  cite (`ratelimiter.go:16`, `:38`).
  Sweep: Work list item 2, the unfiled finding on the rolling update's annotation writes from a
  stale cached CR, is still open and now names its owner (the next filing run, which Hans starts)
  and its form (its own file: 023, 033 and 042 name the functions only as locations). Frontmatter
  unchanged.
  Final pass: Work list item 2 is marked done (moved out of this ticket), with no link, and the Adjacent finding's parked "to be filed" became a statement that the
  finding is not this ticket's to decide and that no option here changes those writes, with no
  pointer; frontmatter, options and decision unchanged.
