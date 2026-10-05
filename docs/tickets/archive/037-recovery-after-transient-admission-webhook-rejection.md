# Ticket: valkey-operator — recovery after transient admission-webhook rejection

> **Archived 2026-09-27** as ticket 037, renamed from `local_valkey_operator_admission_gap.md`
> when the tickets were numbered. Without the `local_` prefix the owner's global `local_*` ignore
> rule no longer matches this file, so the statements below that call it untracked or gitignored
> describe it before that day.

> **Status: SOURCE DOCUMENT, still present, still untracked. Reviewed 2026-08-26.** Index:
> [`039-findings-from-the-1-11-0-fleet-rollout.md`](039-findings-from-the-1-11-0-fleet-rollout.md). Keep this line current.
>
> This is the working note whose `NA1`–`NA63` findings became the ADRs in
> [`docs/adr/`](../../adr/README.md). [`040-tracked-files-cite-work-items-instead-of-adrs.md`](../040-tracked-files-cite-work-items-instead-of-adrs.md)
> was written on the assumption that this file was **about to be deleted**; five days on it
> is still here, 7544 lines. That is not an oversight to fix by deleting it in a hurry —
> **53 lines in tracked files still cite `NA61`/`NA62`/`NA63`, which are documented here at
> `:7041` and `:7090` and nowhere else.** Deleting this file before those citations are
> rewritten turns 53 resolvable references into 53 dangling ones.
>
> Order: rewrite the `NA61`–`NA63` citations as ADR references first (see
> `040-tracked-files-cite-work-items-instead-of-adrs.md`), then decide whether this file goes. Until then it is a
> dependency, not a leftover.

> **Keep this file current.** Update it as part of every fix or assignment that
> touches this ticket — in the same change, not afterwards. Concretely: move the
> item's status (open → DONE / still open), record what was actually verified and
> how (commands, measured numbers, file:line), state what was deliberately left
> out and why, and correct any claim in this ticket that the work disproved — a
> superseded statement gets rewritten, not left standing next to its correction.
> New findings that surface along the way become their own numbered item rather
> than a footnote. Separate verified from unverified explicitly; "could not check
> this" is an acceptable entry, a guess presented as fact is not.

Target repo: `github.com/guided-traffic/valkey-operator` (image
`guidedtraffic/valkey-operator:1.10.46`, API group `vko.gtrfc.com`).
Filed from `k8s-flux-base` because the incident was observed there. **That
applies to the original filing only** — at the time no clone of the operator repo
was available, so the "What happened" and "Findings" sections below were written
blind. Everything from "Verification against operator source" onwards, including
all work packages and every NA item, is checked against the source in this repo;
where the two disagree the later section wins.

## What happened (measured on infra-d, 2026-08-19)

A rolling worker-node update drained the node that hosted **both** the
single-replica `kyverno-admission-controller` **and** all three
`oauth2-valkey` data pods. For ~90 s `kyverno-svc` had no endpoints. The
Kyverno mutating webhook `mutate.kyverno.svc-fail` has
`failurePolicy: Fail` and matches `pods`, `deployments`, `statefulsets`,
`daemonsets`, `jobs`, `ingresses`, so during that window every create of
those kinds was rejected API-side.

Timeline (UTC, from cluster events and operator logs):

| Time | Event |
|---|---|
| 13:26:59 | valkey-operator restarts (moved by the previous drain round) |
| ~13:51 | drain evicts kyverno-admission-controller and `oauth2-valkey-0/1/2` together |
| 13:51:28 | operator reconcile fails: `sentinel statefulset: Internal error occurred: failed calling webhook "mutate.kyverno.svc-fail" ... no endpoints available for service "kyverno-svc"` |
| 13:51:36–13:52:58 | statefulset-controller `FailedCreate` for `oauth2-valkey-0/1/2` (x13 / x4 / x3), same webhook error; last attempt 13:52:58 |
| 13:53:28 | kyverno-admission-controller pod reaches `Ready` → `kyverno-svc` endpoint restored (pod was created 13:51:28, container started 13:53:14) |
| 13:53:28–13:58:27 | **nothing happens.** Operator reconciles every ~10 s, logs `Instance not healthy, requeuing`, CR phase `Provisioning`/`Error`. Data StatefulSet stays at `spec.replicas=3 / status.replicas=0` |
| 13:58:27 | statefulset-controller retries on its own → all three pods created |
| 13:59 | Valkey CR `PHASE=OK`, `READY=3`; oauth2-proxy readiness recovers |

**Net effect: ~7 min with zero Valkey data pods, of which 5 min were pure
kube-controller retry delay after the actual cause was already gone.**

Why nothing woke the statefulset-controller earlier — verified by elimination:

- The StatefulSet object was not written during the window.
  `managedFields` shows the last spec write by the operator (`manager`) at
  **12:59:42**, an hour before the incident; the only other entry is
  `kube-controller-manager` on the `status` subresource at 13:58:54, i.e.
  *after* the pods were created. So no informer event from the object itself.
- No pods of that StatefulSet existed, so no pod events either.
- Flux reconciles the owning Kustomization every 1 m, but the `Valkey` CR was
  unchanged, so no write propagated down to the StatefulSet.
- The operator reconciled continuously but only ever wrote the **sentinel**
  StatefulSet, never the data one.

The only remaining wakeup was the controller's own workqueue backoff timer.
The measured wait, 13:52:58 → 13:58:27 = **5 min 29 s**, matches the default
`ItemExponentialFailureRateLimiter` after 16 consecutive failures
(5 ms · 2^16 = 5 min 28 s) almost exactly. `podManagementPolicy: Parallel`
means one sync attempts all three pods, which is consistent with the 20
aggregated `FailedCreate` events over ~16 syncs.

During that window `oauth2-proxy` served `/ready` → HTTP 500 and logged
`lookup oauth2-valkey-0.oauth2-valkey-headless.iam.svc.cluster.local: no such host`,
i.e. the whole SSO login path of the cluster was down.

Nothing was repaired by hand. The system recovered on its own — this is not a
"stuck forever" bug, it is a slow-recovery and blast-radius bug.

## Findings for the operator

**F1 — reconcile aborts on the first sub-resource error.**
The sentinel-StatefulSet update is applied early in the reconcile and its
error is returned immediately (`error: "sentinel statefulset: ..."`). Every
later step — data StatefulSet, master discovery, status — is skipped for that
pass. One transient admission rejection on one sub-resource therefore stalls
the whole data plane, not just the sentinel part.

**F2 — no nudge when the data StatefulSet is short of pods. This is the whole
5-minute tail.**
While `status.replicas=0` against `spec.replicas=3`, the operator observed
the unhealthy instance and requeued every ~10 s, but never touched the
StatefulSet object. An untouched StatefulSet produces no informer event, and
with zero pods there are no pod events either, so the statefulset-controller
had nothing to react to except its own backoff timer — measured 5 min 29 s
from the last failed attempt, while the webhook had already been healthy for
5 min of that.

A no-op patch (annotation bump) on the StatefulSet whenever
`status.replicas < spec.replicas` persists for more than a few seconds would
force an immediate resync and cut the outage to roughly the webhook gap
itself (~90 s here).

The data StatefulSet uses `updateStrategy: OnDelete` and
`podManagementPolicy: Parallel`, so pod creation is entirely the
statefulset-controller's job — touching the object is the operator's only
lever, and it is not used.

**F3 — no PodDisruptionBudget for either StatefulSet.**
`kubectl get pdb -A` on the affected cluster listed no PDB for
`oauth2-valkey` or `oauth2-valkey-sentinel`. A single node drain was
therefore allowed to evict all three data pods at once. With a PDB
(`maxUnavailable: 1` on the data STS, quorum-preserving `minAvailable` on the
sentinel STS) the drain would have been serialized and the webhook gap would
have cost one pod, not all three.

**F4 — the rejection reason never reaches the CR.**
The CR showed `PHASE=Provisioning` / `Error` with no condition naming the
admission rejection. An operator user cannot tell "my webhook is down" from
"my storage is broken" without reading operator logs.

## Test scenarios to add

### T1 — transient admission rejection of pod creation (the incident)

1. Bring up a `Valkey` CR with `replicas: 3` (sentinel mode) and wait for
   `PHASE=OK`.
2. Install a `MutatingWebhookConfiguration` with `failurePolicy: Fail`,
   `rules` matching `CREATE pods`, pointing at a Service with **no**
   endpoints (or a `url` that refuses connections). Keep `timeoutSeconds`
   small so the test is fast.
3. Delete all three data pods at once (simulates the node drain that evicted
   them).
4. Assert: pod creation is rejected, the CR leaves `OK`, and the operator
   does **not** crash-loop or lose the CR state.
5. Delete the webhook configuration.
6. **Assert: within N seconds (target: <= 30 s, not the 5 min 29 s the
   statefulset-controller took on its own in the incident) all three pods exist again and
   the CR returns to `PHASE=OK` with the same master.**

Step 6 is the scenario's forward assertion. It is **not** a reliable regression
guard on its own: against an unfixed operator its outcome is a coin flip (see
NA13), because the statefulset-controller sometimes retries inside the deadline
by itself. The deterministic guard for the nudge is the unit test
`TestReconcileWorkload_RequeuesWhileShortOfPods`.

### T2 — the same rejection on the sentinel StatefulSet only

Same setup, but the webhook matches only `UPDATE statefulsets`. Assert that
the data-plane part of the reconcile still runs and the CR status reflects
the data plane correctly (guards F1).

### T3 — eviction / disruption budget

With a `Valkey` CR at `replicas: 3` on a multi-node kind cluster, issue
`kubectl drain` (or the Eviction API) against the node holding a quorum of
data pods. Assert that a PDB exists and that evicting more than one data pod
at a time is refused (guards F3). Also assert sentinel quorum is preserved.

### T4 — status surfaces the admission error

During T1's blocked window, assert the CR carries a condition whose message
contains the webhook name / rejection reason (guards F4).

## Verification against operator source

Added 2026-08-19 from the operator repo itself (`main` @ `37552a5`). The
report above was written blind from `k8s-flux-base`; this section supersedes
it where they disagree.

- **F2 confirmed.** `reconcileStatefulSet`
  (`internal/controller/valkey_controller.go:816`) writes the data
  StatefulSet only on spec drift (`StatefulSetHasChanged`); no write path is
  keyed on `status.replicas < spec.replicas`.
- **F1 corrected.** On current `main` the data StatefulSet is reconciled
  *before* the sentinel resources (`reconcileResources`, line 312 vs. 319).
  A sentinel-StatefulSet error therefore skips NetworkPolicies, monitoring,
  `updateStatus` and the health/rolling-update handling — not the
  data-StatefulSet write (which was a no-op in the incident anyway: no
  drift). WP3 below is scoped accordingly. The incident ran 1.10.46; the
  ordering there was not checked.
- **F3 confirmed.** No PDB code anywhere in the repo; also no
  `policy/v1` RBAC marker on the controller.
- **F4 refined.** Condition infrastructure exists (`setStatusCondition`,
  `valkey_controller.go:1552`, `ConditionType*` constants in
  `api/v1/valkey_types.go`), but the reconcile error paths only set
  `status.phase`/`status.message` via `updatePhase` — no condition is set.
- **T3 blocker.** The e2e kind cluster is single-node (`Makefile:108`); the
  drain test T3 needs a multi-node kind config first.

## Work packages

Ordered by value: WP1 alone removes the measured 5-minute outage tail. WP1
and WP2 share the webhook e2e harness and should land together or
back-to-back. WP4 is independent of the others and large enough to split
into its own ticket if needed.

### WP1 — StatefulSet nudge on missing pods (F2, T1) — IMPLEMENTED

**Status (2026-08-19, e2e verified 2026-08-19/20):** implemented and verified
including the T1 e2e — first executed in NA1 (which exposed and fixed a
dormancy defect that had made WP1 inert as shipped), green locally and in the
CI single-node e2e leg since. See "Implementation result" at the end of this
work package and NA1.

**Goal:** when the data StatefulSet reports
`status.replicas < spec.replicas` for longer than a short grace period, the
operator patches a nudge annotation onto the StatefulSet so the
statefulset-controller resyncs immediately instead of sitting out its
exponential backoff (measured 5 min 29 s in the incident).

Implementation notes:

- Patch (not Update) an annotation such as `vko.gtrfc.com/nudge: <RFC3339>`
  onto the StatefulSet object. The annotation value doubles as the
  rate-limit state: only re-bump when the stored timestamp is older than
  ~30 s. No new CRD field, no new controller state.
- Grace period before the first bump (order of 10–15 s / one requeue cycle)
  so normal pod churn does not trigger nudges.
- Must **not** fire while a rolling update is in progress — the operator
  deletes pods intentionally there (`updateStrategy: OnDelete`) and the
  short-of-pods state is expected.
- Must **not** trip `StatefulSetHasChanged` or
  `OperatorVersionChanged` drift detection (annotation-only patch; verify
  the compare functions in `internal/builder/` ignore it).
- Apply the same logic to the sentinel StatefulSet — same failure mode.

**Files:** `internal/controller/valkey_controller.go` (hook into the
status/health path that already observes readiness),
`internal/builder/annotations.go` + unit tests, new e2e test (T1).

**E2E harness (shared with WP2):** helper that installs a
`MutatingWebhookConfiguration` with `failurePolicy: Fail`, `rules` matching
`CREATE pods`, small `timeoutSeconds`, pointing at an unreachable `url`.
Scope it with a `namespaceSelector` to the test namespace — an unscoped
fail-closed webhook would also block kind system pods and unrelated tests.

**Done when:** T1 passes with recovery ≤ 30 s after webhook removal (step 6 is
the forward assertion, deterministic only *with* the nudge — the regression
guard against an unfixed operator is the unit test
`TestReconcileWorkload_RequeuesWhileShortOfPods`, see NA13); unit tests cover:
no nudge when healthy, no nudge during rolling update, rate limit honored.

**Implementation result (2026-08-19):**

- Code: new `internal/controller/nudge.go` (`nudgeShortStatefulSets`,
  `nudgeStatefulSet`, in-memory `nudgeTracker`), called from `reconcileWorkload`
  **before** the rolling-update checks since NA4 (`valkey_controller.go:268`).
  Helpers `AnnotationNudge`, `NudgeInterval`, `NudgeDue`, `NudgePatch` in
  `internal/builder/annotations.go`. Documented in the "Implementation
  reference" section below — **not** in `CLAUDE.md`, which never carried a
  "StatefulSet Nudge" section (claim corrected 2026-08-20 while doing NA15).
- **Timing deviation from the notes above:** grace period 10 s
  (`nudgeGracePeriod`), re-bump interval **20 s** (`builder.NudgeInterval`)
  instead of ~30 s. Reason: the ≤ 30 s recovery target requires
  grace + interval ≤ 30 s; a 30 s interval would land exactly on the limit.
- Merge patch on the StatefulSet **metadata** (not the pod template), so
  `StatefulSetHasChanged`, `SentinelStatefulSetHasChanged` and
  `OperatorVersionChanged` all ignore it — asserted by
  `TestNudgeAnnotation_DoesNotTriggerDriftDetection`, not assumed.
- Applied to the data **and** the Sentinel StatefulSet, unconditionally. The
  original design suppressed the nudge while `vko.gtrfc.com/rolling-update-state`
  was set; NA4 narrowed that to the data StatefulSet and NA15 removed it entirely.
  See NA15 for why the state annotation cannot serve as the guard.
- **No RBAC / manifest change needed:** `patch` on `apps/statefulsets` is
  already granted in `config/rbac/role.yaml` and the Helm ClusterRole.
- **Accepted tradeoff:** for a permanently stuck StatefulSet (quota, missing
  PVC) the nudge repeats every 20 s indefinitely — one small patch plus one
  statefulset-controller sync per 20 s per affected cluster. Bounding the
  retries would weaken the recovery guarantee.
- Verified: `make test-unit`, `make lint` (0 issues), `make cyclo`
  (< 15), `make gosec` (0 issues). Mutation check: disabling the nudge call
  fails 4 of the new unit tests.
- **T1 executed and green — but only after NA1.** As first recorded here the
  test (`test/e2e/admission_recovery_test.go`,
  `TestE2E_AdmissionRejection_StatefulSetNudgeRecovery`) had never been run —
  no Kind cluster on the machine it was written on. Its first isolated run
  then failed and exposed WP1 as inert (see NA1). With the NA1 fix it passes
  locally (129.63 s, recovery 9.02 s after webhook removal) and in the CI
  single-node leg (run 32359935550, 2026-08-20: 179.12 s, all six subtests
  incl. the two NA12 Event guards). Shape: blocks CREATE pods for 90 s (so
  the statefulset-controller backoff grows past the deadline), then asserts
  all three data pods return within 60 s of the webhook removal (not 30 s:
  scheduling headroom on a loaded Kind cluster; actual elapsed time is
  logged). The "same master" assertion from T1 step 6 was dropped — after all
  three pods are deleted the master identity is whatever Sentinel elects.
- **E2E harness for WP2 is ready:** `blockPodCreation` in the same file
  installs the namespace-scoped `failurePolicy: Fail` webhook pointing at a
  Service **with no endpoints** (reproduces the incident's
  "no endpoints available for service" message rather than a connection
  refusal) and returns an idempotent removal function.

### WP2 — surface admission rejection in the CR (F4, T4) — IMPLEMENTED

**Status (2026-08-19, e2e verified 2026-08-19/20):** implemented and verified
including the T4 e2e — first executed during the NA1/NA2 regression checks,
green in the CI single-node e2e leg since. See "Implementation result" at the
end of this work package.

**Goal:** a user can tell "my webhook is down" from "my storage is broken"
by looking at the CR.

Implementation notes:

- New condition type in `api/v1/valkey_types.go`:
  `ConditionTypeReconcileBlocked = "ReconcileBlocked"`.
- Set it (status `True`) in the `reconcileResources` error paths via the
  existing `setStatusCondition` helper. Reason `AdmissionWebhookDenied` when
  the error is an admission failure (`apierrors.IsInternalError` plus
  message match on `failed calling webhook`, and explicit webhook denials),
  reason `WriteFailed` otherwise; message carries the underlying error
  including the webhook name.
- Clear it (status `False`) after the first fully successful
  `reconcileResources` pass.
- `status.conditions` already exists on the CRD — no schema change, no
  `make manifests` diff expected beyond the constant.

**Files:** `api/v1/valkey_types.go`, `internal/controller/valkey_controller.go`,
unit tests; T4 is an assertion inside the T1 e2e scenario (during the
blocked window the condition names the webhook).

**Done when:** T4 assertion passes; condition transitions True→False across
the T1 window.

**Implementation result (2026-08-19):**

- API: `ConditionTypeReconcileBlocked` plus reasons `AdmissionWebhookDenied`,
  `WriteFailed`, `ReconcileSucceeded` in `api/v1/valkey_types.go`.
  `make manifests` produces **no diff** — `status.conditions` already existed.
- Code: new `internal/controller/reconcile_blocked.go`
  (`isAdmissionRejection`, `reconcileBlockedReason`,
  `truncateConditionMessage`, `setReconcileBlockedCondition`), called from
  `Reconcile` on both outcomes of `reconcileResources`
  (`valkey_controller.go:212`). Documented in `CLAUDE.md`
  ("ReconcileBlocked Condition").
- **Classification is message-based, not only typed** (deviation from the
  note above): callers wrap errors (`fmt.Errorf("sentinel statefulset: %w", err)`)
  and an explicit webhook denial arrives as `Forbidden`, not as an internal
  error, so `apierrors.IsInternalError` alone would miss both. Matched:
  `failed calling webhook` (fail-closed webhook unreachable — the incident's
  shape), `admission webhook ... denied the request` (explicit denial), and
  as a fallback an internal error mentioning `admission`. Unit table covers
  quota/conflict/plain errors as negatives.
- **No status write when nothing changes** — not in the ticket, but required:
  a healthy CR reconciles every few seconds and a blocked one every ~10 s, so
  an unconditional `Status().Update` would be pure API churn. Asserted via
  `resourceVersion` in `TestSetReconcileBlockedCondition_NoWrite*`.
- Message truncated at 1024 runes (`conditionMessageLimit`); the webhook name
  is always at the front of the API server's message.
- Verified: `make test-unit`, `make lint` (0 issues), `make cyclo` (< 15),
  `make gosec` (0 issues), `make manifests` (no diff). Mutation check:
  removing the two `setReconcileBlockedCondition` calls fails
  `TestReconcile_SetsReconcileBlockedOnAdmissionRejection` and
  `TestReconcile_ClearsReconcileBlockedAfterSuccess`.
- **T4 could not be an assertion inside T1 — corrected scope.** T1's webhook
  matches `CREATE pods`, and the operator never creates pods (that is the
  statefulset-controller's job under `updateStrategy: OnDelete`), so
  `reconcileResources` does not fail in the T1 window and `ReconcileBlocked`
  would never be set there. T4 is therefore its own e2e:
  `TestE2E_AdmissionRejection_ReconcileBlockedCondition` blocks
  `CREATE configmaps` — a write the operator does own, and the first one in
  `reconcileResources` — creates the CR under the block, asserts
  `ReconcileBlocked=True` / `AdmissionWebhookDenied` with the webhook name in
  the message, then asserts the flip to `False` / `ReconcileSucceeded` and
  `PHASE=OK` after removal.
- Harness generalized: `blockPodCreation` is now a wrapper around
  `blockCoreResourceCreation(t, ns, name, resources...)` in the same file;
  the webhook name is the shared constant `blackholeWebhookName`.
- **T4 e2e executed and green.** Initially recorded as not verified (no Kind
  cluster on the writing machine); first run green in the NA1/NA2 regression
  checks (14.57 s) and green in the CI single-node leg (run 32359935550,
  2026-08-20: 30.89 s, both subtests). The test waits for `kube-root-ca.crt`
  before installing the block so the root-CA publisher is not caught by the
  configmap webhook.
- **Consequence for WP3:** with `reconcileResources` still returning on the
  first error, the condition reports exactly one failing sub-resource. Once
  WP3 aggregates errors, `setReconcileBlockedCondition` should be fed the
  joined error and the reason picked if *any* joined error is an admission
  rejection.

### WP3 — continue reconcile past sub-resource errors (F1 corrected, T2) — IMPLEMENTED

**Status (2026-08-19, e2e verified 2026-08-19/20):** implemented and verified
including the T2 e2e — first executed during the NA1/NA2 regression checks,
green in the CI single-node e2e leg since. See "Implementation result" at the
end of this section.


**Goal:** one failing sub-resource write no longer aborts the rest of the
pass. Concretely on `main`: a sentinel-StatefulSet rejection must not skip
NetworkPolicies, monitoring, `updateStatus` and health handling.

Implementation notes:

- `reconcileResources` collects errors (`errors.Join`) and keeps going for
  steps that do not depend on the failed one. Creation-order dependencies
  (ConfigMaps before StatefulSets) still allow continuing — later steps
  reference earlier objects by name only.
- Phase/`ReconcileBlocked` (WP2) set once at the end from the aggregate,
  not per-step — removes today's per-step `updatePhase` calls.
- `updateStatus` runs even when sub-resource writes failed, so the CR
  keeps reflecting the data plane.
- Watch cyclomatic complexity (repo limit 15) — the restructure will need
  a small helper (step list + loop) rather than more `if` chains.

**Files:** `internal/controller/valkey_controller.go` + unit tests; e2e T2
(webhook matching only `UPDATE statefulsets`; assert data plane reconciles
and status stays truthful).

**Done when:** T2 passes; unit tests cover aggregate-error paths.

**Implementation result**

- `reconcileResources` is now a step list (`reconcileStep{name, when, run}`) run by
  `runReconcileSteps` (`internal/controller/valkey_controller.go`): every applicable
  step runs, failures are wrapped with the step name and returned as one
  `errors.Join`. The same helper replaced the abort-on-first-error chains inside
  `reconcileServices`, `reconcileMonitoringResources` and `reconcileMetrics`
  (new leaf funcs `reconcileMetricsService`, `reconcileMetricsServiceMonitor`).
  Step order is unchanged.
- `Reconcile` no longer returns on a resource error: the data-plane half moved into
  `reconcileWorkload` (rolling update, post-rolling checks, nudge, `updateStatus`,
  requeue) and runs either way. The joined error is returned afterwards so the
  rate limiter backs off.
- Phase is written **once, last** — the per-step `updatePhase` calls are gone. A
  blocked pass ends at `Error` + `"Failed to reconcile resources: <joined>"`, placed
  after `updateStatus` so it survives it. New `compactErrorMessage`
  (`reconcile_blocked.go`) folds `errors.Join`'s newlines into `"; "`, and the
  `ReconcileBlocked` message uses it too — a multi-line status message is unreadable
  in `kubectl`/Lens.
- **Tradeoff:** while blocked, `updateStatus` and the final phase write disagree, so
  a blocked pass costs two status writes per reconcile instead of one. Healthy passes
  are unchanged (no extra write). The alternative — letting `updateStatus` own the
  phase — would report `PHASE=OK` while a managed write is being rejected.
- `setReconcileBlockedCondition` needed no change: matching is on the message, and
  `errors.Join`'s concatenated text already ORs the admission shapes across all
  joined errors (the point raised under WP2).
- Unit tests: `internal/controller/reconcile_steps_test.go` — helper semantics
  (all steps run, predicates skip, joined error), `reconcileResources` continuing
  past a rejected StatefulSet (NetworkPolicies + metrics Service still created) and
  past a rejected ConfigMap (StatefulSet still written), both rejections surviving
  into one error, and a full `Reconcile` pass that still writes the StatefulSet and
  ends at `Error` with a single-line message naming the failing step.
- E2E T2: `TestE2E_AdmissionRejection_ReconcileContinuesPastRejectedWrite`
  (`test/e2e/admission_recovery_test.go`) — creates a healthy `replicas: 1` CR, blocks
  `UPDATE apps/v1 statefulsets`, then patches `replicas: 2` + `networkPolicy.enabled`
  in one go: the StatefulSet write is rejected while the NetworkPolicies behind it
  must still appear, the condition must name the `StatefulSet` step *and* the webhook,
  `status.readyReplicas` must still report the running pod, and the cluster must
  converge to 2/2 + `PHASE=OK` after the webhook is removed.
  Harness: `blockCoreResourceCreation` is now a wrapper around
  `blockResourceOperations(t, ns, name, group, version, operations, resources...)`;
  new helpers `patchValkeySpec` and `waitForNetworkPolicies`.
- Verified locally: `make lint` (0 issues), `make cyclo` (all < 15), `make gosec`
  (0 issues), `make test-unit`, `make test-integration` — all green.
  The T2 e2e, initially outstanding, first ran green in the NA1/NA2 regression
  checks (51.61 s) and passes in the CI single-node leg (run 32359935550,
  2026-08-20: 102.84 s, all four subtests).

### WP4 — PodDisruptionBudgets (F3, T3) — IMPLEMENTED

**Status (2026-08-19, branch `feat/support-pdb`):** implemented and **fully
verified — T3 executed and green on a real 4-node Kind cluster** — see
"Implementation result" at the end of this work package.

**Goal:** a single node drain can no longer evict all data pods (or a
sentinel quorum) at once.

Implementation notes:

- New CRD field `spec.podDisruptionBudget` (sketch below). Omitted block →
  no PDBs (backward compatible: if the operator auto-created PDBs, users
  who already manage their own would end up with two PDBs matching the same
  pods, and the Eviction API refuses eviction outright in that case).
- New builder `internal/builder/pdb.go`. **Both PDBs are mandatory scope of
  this WP — the sentinel PDB is not optional:**
  - data PDB: `maxUnavailable` from spec, default 1;
  - sentinel PDB: `minAvailable = floor(sentinelReplicas/2) + 1` (quorum),
    **computed, not configurable** — a configurable value would let a spec
    silently break the quorum guarantee.
- **Single-replica skip rule (decided 2026-08-19):** no PDB is created for a
  StatefulSet with fewer than 2 replicas, even when `enabled: true` —
  data PDB skipped at `spec.replicas == 1`, sentinel PDB skipped at
  `spec.sentinel.replicas < 2` (API minimum is 1, `valkey_types.go:65`).
  Rationale: with one pod, `maxUnavailable: 1` permits evicting the only
  pod (useless object) and `minAvailable: 1`/`maxUnavailable: 0` blocks
  `kubectl drain` forever (stalled node maintenance, fake safety — the
  instance is not HA either way). Skip is visible as a log line; optionally
  an Event. Deliberately **not** enforced as CRD/CEL validation: a rule like
  `replicas > 1 || !pdb.enabled` would reject scaling an existing CR with
  enabled PDB down to 1 and force two ordered edits.
- Controller: create-or-cleanup pattern like `reconcileMetrics`
  (create/update when enabled and replica count qualifies, delete when
  disabled or when the count drops below 2), owner references set.
- New RBAC marker: `// +kubebuilder:rbac:groups=policy,resources=poddisruptionbudgets,verbs=get;list;watch;create;update;patch;delete`
  → `make manifests` regenerates `config/rbac` and `config/crd`.
- Helm chart (`deploy/helm/valkey-operator/`): add `policy` /
  `poddisruptionbudgets` to `templates/clusterrole.yaml`, sync the CRD
  schema into `templates/crd.yaml`, and document the behavior in
  `values.yaml` with a short comment (two lines suffice), e.g.:
  `# Valkey CRs support spec.podDisruptionBudget (opt-in). PDBs are only`
  `# created for StatefulSets with >= 2 replicas; singletons are skipped.`
- E2E infra: extend the kind config in `Makefile:108` with worker nodes
  (control-plane + 2–3 workers) for T3. Check impact on existing e2e
  runtime/scheduling assumptions before enabling.

**Files:** `api/v1/valkey_types.go` (+ deepcopy regen),
`internal/builder/pdb.go` + tests, `internal/controller/valkey_controller.go`,
`config/` regen, `deploy/helm/valkey-operator/` (clusterrole, crd, values),
`Makefile`, e2e T3.

**Done when:** T3 passes on a multi-node kind cluster: both PDBs exist
(data **and** sentinel), evicting a second data pod while one is down is
refused, sentinel quorum preserved during drain. Unit tests additionally
assert: no PDB objects for `replicas: 1` (and sentinel `replicas: 1`), and
cleanup when scaling down below 2.

**Implementation result (2026-08-19):**

- Built as specified: `spec.podDisruptionBudget{enabled, maxUnavailable}`
  (`api/v1/valkey_types.go`), builders in `internal/builder/pdb.go`,
  create-or-cleanup in `internal/controller/pdb.go`, wired as the
  `PodDisruptionBudgets` reconcile step after `Sentinel resources`. RBAC marker
  added, `make generate-all` regenerated CRD + deepcopy + Helm CRD, `policy`
  rule added to the Helm ClusterRole, `values.yaml` comment added.
- **Quorum formula deduplicated instead of copied:** `SentinelQuorumFor(replicas)`
  now lives in `internal/builder/sentinel.go` and is used both by the Sentinel PDB
  and by the Sentinel config generation, which had the same `floor(n/2)+1`
  inline. One definition, so the PDB can never disagree with the running
  `sentinel monitor` quorum.
- **maxUnavailable >= replicas is honoured, not rejected** (a CEL rule would block
  a later scale-down), but the operator logs a warning on create/update: the budget
  then permits evicting every data pod at once.
- **Single-replica skip** is logged at V(1) and, when a PDB already exists, as a
  normal "Deleting PodDisruptionBudget" line — no per-reconcile noise for a
  standalone instance that opted in.
- **Owner references** are set, so `kubectl delete valkey` removes both budgets;
  `Owns(&policyv1.PodDisruptionBudget{})` added to `SetupWithManager`.
- **Kind config** (`Makefile:kind-create`) is now control-plane + 3 workers, written
  to `tmp/kind-config.yaml` instead of `/tmp` (project rule). 3 workers, not 2, so
  WP5's hard-mode spread case (3 replicas on 3 distinct nodes) is reproducible; its
  negative case cordons one worker.
- **CI kind config is unchanged** (`.github/workflows/release.yml` builds its own
  single-node DinD cluster, including a `docker exec ...-control-plane sysctl` step
  that assumes one node). T3 was therefore written node-count agnostic: it drives
  the **Eviction API** directly instead of `kubectl drain`, and a PDB is enforced per
  pod set, not per node — so it passes on both cluster shapes. A real drain test
  (and WP5's hard-mode assertions) still needs the CI config to grow workers.
- **Integration coverage (envtest, real API server):** `test/integration/pdb_test.go`
  — both budgets created with owner refs, the **CRD default** `maxUnavailable: 1`
  applied by the API server (the fake client in the unit tests never defaults),
  data budget removed on scale-to-1 while the sentinel budget survives, and both
  removed when disabled.
- **T3 executed and green.** `make kind-create` (the new control-plane + 3 workers
  config), operator installed via the Helm chart, then
  `go test -tags=e2e -run TestE2E_PodDisruptionBudget`:
  `TestE2E_PodDisruptionBudget_SerializesEvictions` 22.3 s (budget shapes and
  status, refused second **data** eviction, refused second **sentinel** eviction,
  cleanup after disabling) and `TestE2E_PodDisruptionBudget_SkippedForSingleReplica`
  44.1 s — both PASS. The eviction race feared while writing the test did not
  materialise: both refusals were observed within 0.1 s of the first eviction.
- Verified: `make lint` (0 issues), `make cyclo` (all < 15), `make gosec`
  (0 issues), `make test-unit`, `make test-integration`, `make generate-all`
  (no drift) — all green.
- **Unrelated pre-existing failure noticed:** `TestHandleTopologyRestoration_*`
  (2 tests) and `TestHandleMultiReplicaRollingUpdate_SplitBrainDemotedBeforeUpdate`
  fail when `internal/controller` is run **without** `-short`. Verified at clean
  `HEAD` (74460e9) in a separate worktree, so this predates WP4. `make test-unit`
  passes because it runs `-short`, which skips these three (among other
  Short-gated tests that do pass without it — the "exactly these three"
  originally claimed here was about the failures, not the skips). Tracked as
  **NA22** since 2026-08-20; re-verified identical on `main` @ `37552a5`.
  **Resolved 2026-08-20** — see NA22 below: all three repaired, every
  `testing.Short()` gate removed, `-short` dropped from the unit-test targets.

### WP5 — PodAntiAffinity for data and sentinel pods (T5) — IMPLEMENTED

**Goal:** the incident's enabling co-location (all three data pods on one
node) is prevented — best-effort by default (`soft`), guaranteed with
`mode: hard`. Complements WP4: anti-affinity prevents co-location up front,
the PDB serializes what eviction remains.

Greenfield, verified: no affinity handling exists anywhere in
`internal/builder/` or `api/v1/` today.

Implementation notes:

- New CRD field `spec.antiAffinity` (sketch below), applied to **both** the
  data and the sentinel StatefulSet pod templates. Each repels only its own
  kind via the existing component labels
  (`app.kubernetes.io/component` + `vko.gtrfc.com/cluster`).
- `mode: soft` renders `preferredDuringSchedulingIgnoredDuringExecution`
  (weight 100); `mode: hard` renders
  `requiredDuringSchedulingIgnoredDuringExecution`. `topologyKey` defaults to
  `kubernetes.io/hostname`. **Default history:** hard was decided 2026-08-19
  and revised to soft the same day (upgrade-wedge and e2e consequences below);
  **on 2026-08-20 the default was revised again to a new `off` mode (WP6,
  decided by Hans): an operator upgrade must not change the scheduling of
  existing clusters, so a term is only rendered after an explicit opt-in to
  soft or hard.** The original "no off switch" stance is thereby reversed —
  the weakest setting is now no constraint at all, and it is the default.
- **Single-replica skip rule**, same shape as WP4: no anti-affinity term is
  injected when the StatefulSet has fewer than 2 replicas — a singleton has
  no peer to repel, and skipping avoids a pointless template-hash change
  (and thus a restart) for standalone instances.
- **Soft consequences (originally written for default-soft; since WP6 they
  apply to the opt-in instead):** the term is a scheduler preference — it
  never blocks scheduling, so enabling it cannot wedge a cluster. The
  template-hash change triggers one failover-aware rolling update of the
  cluster that opts in (lossless). Soft does **not** guarantee spread: under
  node pressure the scheduler may still co-locate pods. The NA9 discussion
  (every operator release already rolls every multi-replica data StatefulSet
  via the sidecar image) is unchanged, but WP6 removes the one-time
  Sentinel roll a default term would have caused on upgrade — by default
  nothing is rendered, so nothing rolls.
- **Hard mode (opt-in) consequences, documented at the field:**
  - Fewer schedulable nodes than replicas → surplus pods `Pending`;
    enabling hard on a constrained cluster wedges its next rolling update.
  - During a node drain an evicted pod stays `Pending` until a node
    without a replica of the same cluster is schedulable — degraded but
    correct: the spread guarantee is preserved instead of silently
    re-co-locating.
- E2E: the off-default and soft assertions run on the existing single-node
  kind cluster; only the hard-mode assertions of T5 need the multi-node infra
  (T3 blocker).
  WP5 itself is therefore not blocked by the multi-node rebuild — only its
  hard-path test is.
- Rollout to existing healthy clusters is the standard failover-aware
  rolling update (lossless). The `replicas == 1` skip keeps standalone
  singletons untouched.
- Helm chart: sync CRD schema into `templates/crd.yaml`; extend the
  `values.yaml` comment from WP4 by one line noting default-soft
  anti-affinity for multi-replica clusters.

**Files:** `api/v1/valkey_types.go` (+ deepcopy regen),
`internal/builder/statefulset.go`, `internal/builder/sentinel.go` + tests,
`deploy/helm/valkey-operator/` (crd, values), e2e T5.

**Done when:** T5 passes (soft term by default, hard spread, Pending
negative case); unit tests cover soft/hard/topologyKey rendering,
component-scoped selectors, and the `replicas == 1` skip.

**Implementation result (2026-08-19):**

- Built as specified: `spec.antiAffinity{mode, topologyKey}`
  (`api/v1/valkey_types.go`), builder in `internal/builder/affinity.go`, wired
  into `buildPodSpec` (`internal/builder/statefulset.go`) and
  `buildSentinelPodSpec` (`internal/builder/sentinel.go`). No controller and no
  RBAC change — the term lives inside the pod template the operator already writes.
- **One builder for both components.** `BuildPodAntiAffinity(v, component)` takes
  the component and reuses `common.SelectorLabels`, i.e. exactly the label set the
  StatefulSet selector uses. The anti-affinity term can therefore never select a
  different pod set than the StatefulSet it sits in — no second, hand-written
  selector to drift.
- **Unknown mode falls back to the weakest setting.** The CEL enum makes
  `bogus` unreachable through the API server, but if validation is ever
  bypassed the weakest setting wins rather than a constraint that leaves pods
  `Pending`. Unit-tested. (As built on 2026-08-19 the fallback was soft;
  since WP6 the weakest setting — and therefore the fallback — is off.)
- **Rolling-update detection needed no change:** `podSpecChanged` does not compare
  `Affinity`, but the pod-spec hash annotation covers the whole `PodSpec`, so a
  mode or topologyKey change flips the hash, `podTemplateChanged` sees the changed
  annotation, and the existing failover-aware rolling update migrates the pods.
  Guarded by `TestComputePodSpecHash_ChangesWithAntiAffinityMode` and the sentinel
  equivalent.
- **Negative case without cordoning nodes.** T5's Pending case was specified as
  "3 replicas on 2 schedulable workers", which would mean cordoning a node — a
  cluster-wide side effect under `t.Parallel()` that would strand the pods of every
  other e2e test. It instead collapses the spread domains by pointing `topologyKey`
  at `kubernetes.io/os`, a label every node carries: one domain, three replicas,
  exactly one pod scheduled and two `Unschedulable`. Same assertion, node-count
  agnostic, so it also runs on CI's single-node DinD cluster.
- **Hard-spread test skips below 3 schedulable nodes**, counting only nodes that
  are Ready, uncordoned and free of a `NoSchedule`/`NoExecute` taint — without the
  taint check, multi-node Kind's tainted control-plane node would inflate the count
  and the test would fail instead of skipping on a 2-worker cluster.
- **Integration coverage (envtest, real API server):** `test/integration/affinity_test.go`
  — the **CRD defaults** `soft` / `kubernetes.io/hostname` applied by the API server
  (the fake client in the unit tests never defaults), both pod templates carrying the
  soft term, the switch to hard replacing the preference with the required term, and
  the term disappearing on scale-to-1.
- **T5 executed and green** on the control-plane + 3 workers Kind cluster from
  WP4 (`make kind-create`), operator installed via the Helm chart:
  `TestE2E_AntiAffinity_SoftByDefault` 16.1 s,
  `TestE2E_AntiAffinity_HardSpreadsAcrossNodes` 16.2 s (data pods and sentinel pods
  each on three distinct nodes — the multi-node rebuild from WP4 paid off here) and
  `TestE2E_AntiAffinity_HardLeavesSurplusPending` 2.1 s — all PASS.
  (WP6 later split `SoftByDefault` into `OffByDefault` + `SoftWhenRequested`;
  those two have not run on a cluster yet — see WP6.)
- Docs: `README.md` gets a feature bullet and a full `spec.antiAffinity` reference
  section (including the zone-spread variant); `CLAUDE.md` CRD example and the Helm
  `values.yaml` comment extended.
- Verified: `make lint` (0 issues), `make cyclo` (all < 15), `make gosec`
  (0 issues), `make test-unit`, `make test-integration`, `make generate-all`
  (no drift) — all green.
- **Resolved by NA1: the e2e failure seen here was not WP5's.** A full
  `make test-e2e` run on the same cluster (28 PASS / 6 FAIL) was aborted early.
  Five failures were explained immediately (`cert-manager-install` deliberately
  skipped, so `TestE2E_CertManagerReady` and the four TLS tests depending on it
  had to fail). The sixth, `TestE2E_AdmissionRejection_StatefulSetNudgeRecovery`,
  was then run in isolation as NA1 and root-caused to a WP1 operator defect
  (the nudge requeue was a dead code path in `Provisioning`) — unrelated to
  anti-affinity, since fixed and green. See NA1 for the full attribution.

### WP6 — antiAffinity mode `off` as the new default — IMPLEMENTED

**Status (2026-08-20):** implemented, unit- and integration-verified; the two
reworked e2e tests compile but have not run on a cluster.

**Decision (2026-08-20, Hans):** the anti-affinity block gains a third mode
`off`, and `off` becomes the default — an operator upgrade must not change the
scheduling behavior of existing clusters. This reverses WP5's "no off switch"
stance: the weakest setting is now no constraint at all.

**Named consequence, accepted:** the incident's enabling co-location
(all data pods on one node) is no longer prevented by default — WP5's
protection is opt-in now. Every cluster that wants the spread must set
`mode: soft` (or `hard`) explicitly; README and the CRD field docs therefore
recommend the opt-in for every multi-replica cluster, and enabling it later
costs one failover-aware rolling update (lossless for multi-replica clusters).
The default-path protections that remain from this ticket are the PDB (opt-in
as well) and the nudge (unconditional).

What changed:

- API (`api/v1/valkey_types.go`): `AntiAffinityModeOff`, enum `off;soft;hard`,
  CRD default `off` (controller-gen quotes it — YAML would otherwise read a
  bare `off` as boolean). `AntiAffinityMode()` returns off for a nil block,
  an empty mode and unknown values (the weakest-fallback rationale from WP5,
  re-pointed at off); new `IsAntiAffinityEnabled()`;
  `NeedsDataAntiAffinity`/`NeedsSentinelAntiAffinity` require an enabled mode
  on top of the replica minimum. Builder logic unchanged — the off filter
  lives entirely in the `Needs*` helpers.
- One deliberate wrinkle: a block present with only `topologyKey` set is
  still off (the API server defaults `mode: off` into it). Presence of the
  block does not mean opt-in; only `mode: soft|hard` does. Documented at the
  field.
- Tests reworked so the guarded property stays the same where it was about
  replicas or components (those fixtures now opt into soft explicitly), plus
  new guards: no-block → nil term, explicit off → nil term, unknown → nil
  term, and `ComputePodSpecHash` off→soft (opting in rolls the pods) and
  soft→hard both change the hash.
  E2E: `TestE2E_AntiAffinity_SoftByDefault` split into
  `TestE2E_AntiAffinity_OffByDefault` (no affinity on either pod template)
  and `TestE2E_AntiAffinity_SoftWhenRequested` (the old assertions behind an
  explicit `mode: soft`). Both still match the multi-node leg's
  `E2E_RUN=TestE2E_AntiAffinity` prefix; the CI grep guard names only the
  hard-spread test and is unaffected.
- Integration (`test/integration/affinity_test.go`): asserts the API server
  defaults `mode` to `off`, no template carries a term by default, opting
  into soft adds the preferred term to both templates, hard replaces it,
  scale-to-1 removes it.
- Docs: README feature bullet, `spec` table and `spec.antiAffinity` section
  (now with the explicit recommendation), Helm `values.yaml` comment,
  `CLAUDE.md` CRD example. `make generate-all` synced the CRD into
  `config/crd/bases` and the Helm chart.

Verified: `make test-unit`, `make test-integration` (including the reworked
`TestAntiAffinity_Integration`, 3.0 s), `make lint` (0 issues), `make cyclo`
(all < 15), `make gosec` (0 issues), `make generate-all` (no further drift),
e2e package compiles (`make test-e2e E2E_RUN='TestE2E_CompileCheckOnly_NoSuchTest'`).
The two reworked anti-affinity e2e tests, initially unrun on any cluster, are
green in the CI multi-node leg (run 32359935550, 2026-08-20:
`TestE2E_AntiAffinity_OffByDefault` 26.07 s,
`TestE2E_AntiAffinity_SoftWhenRequested` 26.06 s with both subtests); the
single-node leg runs the full suite and covers them as well.

### CR extension (WP2 + WP4 + WP5 combined)

Spec — WP4 and WP5 add fields, WP6 revises the antiAffinity default to off;
WP1/WP3 need no API change:

```yaml
apiVersion: vko.gtrfc.com/v1
kind: Valkey
metadata:
  name: test
spec:
  replicas: 3
  sentinel:
    enabled: true
    replicas: 3
  podDisruptionBudget:          # new, optional; omitted → no PDBs (today's behavior)
    enabled: true               # example
    maxUnavailable: 1           # default; applies to the data StatefulSet only
                                # sentinel PDB is always quorum-derived, not settable
  antiAffinity:                 # new, optional; omitted → off, no term (WP6)
    mode: soft                  # default off (upgrades change nothing);
                                # soft = scheduler preference, never blocks;
                                # hard = required spread, surplus pods stay Pending
    topologyKey: kubernetes.io/hostname   # default
                                # applies to data and sentinel pods, each repelling
                                # its own kind; skipped for single-replica StatefulSets
```

```go
// PodDisruptionBudgetSpec configures PodDisruptionBudgets for the Valkey
// data and Sentinel StatefulSets.
type PodDisruptionBudgetSpec struct {
	// Enabled creates a PDB for the data StatefulSet (maxUnavailable, default 1)
	// and, when Sentinel is enabled, a quorum-preserving PDB
	// (minAvailable = floor(replicas/2)+1) for the Sentinel StatefulSet.
	// StatefulSets with fewer than 2 replicas never get a PDB: it would
	// either be useless (maxUnavailable 1) or block node drains (minAvailable 1).
	// +kubebuilder:default=false
	Enabled bool `json:"enabled,omitempty"`

	// MaxUnavailable is the maximum number of data pods that may be
	// disrupted voluntarily at the same time.
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=1
	// +optional
	MaxUnavailable *int32 `json:"maxUnavailable,omitempty"`
}

// AntiAffinitySpec configures pod anti-affinity for the data and Sentinel
// StatefulSets. A term is rendered only for mode soft or hard AND 2 or more
// replicas; singletons are skipped (no peer to repel). Omitting the block —
// or mode off, the default since WP6 — renders nothing, so an operator
// upgrade never changes scheduling.
type AntiAffinitySpec struct {
	// Mode selects off (no term, default), soft
	// (preferredDuringSchedulingIgnoredDuringExecution)
	// or hard (requiredDuringSchedulingIgnoredDuringExecution).
	// Soft is a scheduler preference and never blocks scheduling; hard
	// leaves surplus pods Pending when there are fewer schedulable nodes
	// than replicas.
	// +kubebuilder:validation:Enum=off;soft;hard
	// +kubebuilder:default=off
	// +optional
	Mode string `json:"mode,omitempty"`

	// TopologyKey is the node label whose values define the spread domains.
	// +kubebuilder:default="kubernetes.io/hostname"
	// +optional
	TopologyKey string `json:"topologyKey,omitempty"`
}
```

Status — WP2 adds a condition type, no schema change
(`status.conditions` already exists):

```yaml
status:
  phase: Error
  conditions:
    - type: ReconcileBlocked          # new condition type
      status: "True"
      reason: AdmissionWebhookDenied  # or WriteFailed for non-admission errors
      message: >-
        sentinel statefulset: Internal error occurred: failed calling webhook
        "mutate.kyverno.svc-fail": no endpoints available for service "kyverno-svc"
      lastTransitionTime: "2026-08-19T13:51:28Z"
```

### T5 — anti-affinity spread

Default (`mode` omitted → off since WP6, runs on the single-node e2e
cluster): create a `Valkey` CR with `replicas: 3` and assert **no** affinity
on either pod template — an upgrade must not change scheduling. With
`mode: soft`: assert the pod templates of the data and sentinel StatefulSets
carry a `preferredDuringSchedulingIgnoredDuringExecution` anti-affinity term
on `kubernetes.io/hostname`. With `mode: hard` on a multi-node kind cluster
(same infra as T3): assert the `required...` term and that the three data
pods land on three distinct nodes; same for sentinel pods. Negative case
(documents the hard-mode tradeoff): hard, 3 replicas on 2 schedulable
workers → exactly one pod `Pending` (guards WP5). Node-spread is only
asserted in hard mode — soft is a preference and not deterministic.

## Note on the root cause — not the operator's fault

The enabling condition was on our side: `kyverno-admission-controller` runs
with **1 replica and no PDB** on that cluster, so it was evicted in the same
drain batch as everything else, and a `failurePolicy: Fail` webhook with no
endpoints rejects every matching create cluster-wide (15 Flux Kustomizations
failed reconciliation in the same window). Fixing Kyverno HA is a separate
change in `k8s-flux-base` (`apps/cluster-policies/kyverno/app/helmrelease.yml`).
`failurePolicy: Fail` is the correct, fail-closed setting and must stay — the
fix is HA for the webhook, never weakening the policy.

The operator work above shortens the outage and reduces the blast radius; it
does not remove the cause.

## Implementation reference

Design notes for the three shipped work packages. These were briefly kept in
`CLAUDE.md`; they are feature-ticket detail, not project-wide guidance, so they
live here instead.

### StatefulSet Nudge (short-of-pods recovery)

Pod creation for both StatefulSets is entirely the statefulset-controller's job
(`updateStrategy: OnDelete`, `podManagementPolicy: Parallel`). When its creates are
rejected — e.g. by a fail-closed admission webhook whose backend is temporarily gone —
it retries on an exponential workqueue backoff that reached **5 min 29 s** in the
2026-08-19 infra-d incident, long after the rejection cause was resolved. Nothing else
wakes it: the StatefulSet object is not written (no spec drift) and with zero pods there
are no pod events either.

The operator therefore bumps an annotation to force an immediate resync:

- Annotation `vko.gtrfc.com/nudge: <RFC3339>` (`builder.AnnotationNudge`), written as a
  **merge patch** on the StatefulSet *metadata* — not on the pod template. It is
  therefore invisible to `StatefulSetHasChanged` / `SentinelStatefulSetHasChanged` /
  `OperatorVersionChanged` and never triggers a rolling update.
- The stored timestamp doubles as rate-limit state: re-bumped only when older than
  `builder.NudgeInterval` (20 s). No CRD field, no in-cluster state.
- Grace period `nudgeGracePeriod` (10 s, in-memory `nudgeTracker`) before the first bump
  so normal pod churn is not nudged. Losing the map on restart is harmless.
- **No rolling-update suppression**, for either StatefulSet (NA15; the original
  design suppressed the data nudge while `vko.gtrfc.com/rolling-update-state` was
  set). Every rolling-update delete site requeues into a "waiting for pod to be
  recreated" branch, so a blocked recreation is exactly when the nudge is the only
  lever. What separates an intentional deletion from a stall is duration, which
  `nudgeGracePeriod` already measures; the state annotation is a phase marker that
  stays set for the whole phase, stall included.
- Applies to the data and the Sentinel StatefulSet; keyed on
  `status.replicas < spec.replicas` (created pods, not ready ones).
- Code: `internal/controller/nudge.go` (`nudgeShortStatefulSets`), called from
  `reconcileWorkload` **before** the rolling-update checks (NA4); helpers in
  `internal/builder/annotations.go`. The rolling update keeps requeue authority
  because `shortOfPods` is only read after every rolling-update return.
- E2E guard: `TestE2E_AdmissionRejection_StatefulSetNudgeRecovery`
  (`test/e2e/admission_recovery_test.go`) blocks CREATE pods with a namespace-scoped
  `failurePolicy: Fail` webhook, deletes all data pods, then asserts recovery within 60 s
  of removing the webhook.

### ReconcileBlocked Condition

`status.conditions[type=ReconcileBlocked]` tells a user *why* a CR is stuck without
reading operator logs — specifically it separates "a cluster-side admission gate
rejects my writes" from "the write itself failed".

- Set from the outcome of every `reconcileResources` pass in `Reconcile`
  (`valkey_controller.go`), via `setReconcileBlockedCondition`
  (`internal/controller/reconcile_blocked.go`).
- Reasons: `AdmissionWebhookDenied` when `isAdmissionRejection` matches the error
  (message contains `failed calling webhook`, or `admission webhook ... denied the
  request`, or an internal error mentioning the admission chain), `WriteFailed`
  otherwise, `ReconcileSucceeded` when cleared. Matching is on the message, not only
  the typed reason: callers wrap errors (`fmt.Errorf("sentinel statefulset: %w", err)`)
  and explicit denials arrive as `Forbidden`, not as an internal error.
- Message carries the underlying error (truncated at `conditionMessageLimit`, 1024
  runes) including the webhook name.
- **No status write when nothing changes** — neither on a healthy pass that was never
  blocked, nor on repeated identical failures. A blocked cluster reconciles every few
  seconds; rewriting the condition each time would be pure API churn.
- No CRD schema change: `status.conditions` already exists, so `make manifests`
  produces no diff.
- Note on scope: only writes the *operator* performs reach this condition. Pod
  creation is the statefulset-controller's job, so the 2026-08-19 incident's
  `CREATE pods` rejection never surfaced here — that failure mode is covered by the
  StatefulSet nudge above.
- E2E guard: `TestE2E_AdmissionRejection_ReconcileBlockedCondition`
  (`test/e2e/admission_recovery_test.go`) blocks `CREATE configmaps` with a
  namespace-scoped `failurePolicy: Fail` webhook and asserts the condition names it,
  then flips to `False` after removal.

### Aggregate Reconcile (no abort on the first failing sub-resource)

A rejected write on one managed object must not silence the rest of the pass. In the
2026-08-19 incident a single webhook rejection on the Sentinel StatefulSet skipped
NetworkPolicies, monitoring, `updateStatus` and the health/rolling-update handling
for as long as the rejection lasted.

- `reconcileResources` is a **step list** (`reconcileStep{name, when, run}`) executed by
  `runReconcileSteps` (`valkey_controller.go`): every applicable step runs, failures are
  collected and returned as one `errors.Join`, each wrapped with its step name
  (`"StatefulSet: ..."`). Same helper inside `reconcileServices`, `reconcileMonitoringResources`
  and `reconcileMetrics`, so a failing Service does not skip its siblings either.
  Steps only reference earlier objects by name, so continuing is safe.
- Step order is unchanged (ConfigMap → replica ConfigMap → TLS → Services → sidecar RBAC →
  StatefulSet → Sentinel → NetworkPolicies → monitoring); `when` replaces the old `if` chains,
  which also kept cyclomatic complexity down.
- `Reconcile` no longer returns on a resource error. The data-plane part moved to
  `reconcileWorkload` (rolling update, post-rolling checks, nudge, `updateStatus`, requeue)
  and runs either way. The joined error is returned afterwards so the controller-runtime
  rate limiter backs off instead of spinning on the 10 s requeue.
- **Phase is written once, last.** The per-step `updatePhase` calls are gone; a blocked pass
  ends with `Error` + `"Failed to reconcile resources: <joined>"`, written *after*
  `updateStatus` so it is not overwritten. `compactErrorMessage`
  (`internal/controller/reconcile_blocked.go`) folds `errors.Join`'s newlines into `"; "` —
  both the phase message and the `ReconcileBlocked` message must stay single-line for
  `kubectl`/Lens.
- **Literally once, since NA3.** `Reconcile` marks the context with `withBlockedPass` when
  `reconcileResources` fails; `updatePhase` then drops every intermediate write and
  `persistStatus` keeps the previous phase/message, so the final `writePhase` is the pass's
  only phase write. Non-phase status fields (`readyReplicas`, `masterPod`, conditions) keep
  updating. Before that fix the health phase and the Error phase alternated on every blocked
  pass, which watchers saw as flapping.
- Unit guards: `internal/controller/reconcile_steps_test.go` (all steps run despite a
  failure, joined error carries every rejection, data plane still reconciled while blocked).
- E2E guard: `TestE2E_AdmissionRejection_ReconcileContinuesPastRejectedWrite`
  (`test/e2e/admission_recovery_test.go`) blocks `UPDATE apps/v1 statefulsets`, scales the CR
  and enables NetworkPolicies in one patch, then asserts the NetworkPolicies appear anyway,
  the condition names the `StatefulSet` step, and `status.readyReplicas` still reports the
  running pod. `blockCoreResourceCreation` is now a wrapper around the generalized
  `blockResourceOperations(t, ns, name, group, version, operations, resources...)`.

### PodDisruptionBudgets

Voluntary disruptions (node drain, cluster autoscaler) go through the Eviction API,
which is the only place a `PodDisruptionBudget` can serialize them. Without a budget
a single drain takes every pod on the node — in the 2026-08-19 infra-d incident all
three data pods at once.

- Opt-in via `spec.podDisruptionBudget.enabled`. Deliberately **not** on by default:
  a second budget covering the same pods as a user-managed one makes the Eviction API
  refuse every eviction, so auto-creating PDBs would break clusters that already have them.
- Data budget: `maxUnavailable` (default 1, `spec.podDisruptionBudget.maxUnavailable`).
  Sentinel budget: `minAvailable = SentinelQuorumFor(replicas) = floor(replicas/2)+1`,
  computed and not settable — the same helper the Sentinel config uses for its
  `sentinel monitor` quorum (`internal/builder/sentinel.go`).
- **< 2 replicas → no budget**, even when enabled, for the data and the Sentinel
  StatefulSet independently. With one pod `maxUnavailable: 1` permits evicting the only
  pod and `minAvailable: 1` blocks `kubectl drain` forever. Scaling below 2 deletes an
  existing budget; scaling up recreates it. Not enforced as CEL: `replicas > 1 || !enabled`
  would reject scaling an existing CR down and force two ordered edits.
- The operator's own rolling update deletes pods directly (not via eviction), so a budget
  never blocks it — only external drains are serialized.
- Code: `internal/builder/pdb.go` (builders, `PodDisruptionBudgetHasChanged`),
  `internal/controller/pdb.go` (`reconcilePodDisruptionBudgets`, create-or-cleanup,
  owner refs, warning when `maxUnavailable >= replicas`).
- E2E guard: `test/e2e/pdb_test.go` — both budgets' shape and status, a refused second
  eviction for data pods and for Sentinel, cleanup after disabling, and no budget at all
  for a single-replica instance.

## Follow-up work from implementation review (2026-08-19)

Findings from the code review of branch `feat/support-pdb` against this ticket.
All code claims below were verified against the branch source (file:line given);
none of them were re-verified by running tests. Ordered by severity: NA1 blocks
the merge, NA2/NA3 should land with the branch or immediately after, the rest
are follow-ups.

### NA1 — Run T1 in isolation and attribute its failure — DONE (root cause found and fixed)

**Status (2026-08-19):** executed on the 4-node Kind cluster. T1 reproduced in
isolation, was root-caused to an operator defect, fixed, and now passes.
**WP1 was inert as shipped** — the nudge never fired, in the test or in
production. The fix is in `internal/controller/nudge.go` and
`internal/controller/valkey_controller.go`.

#### Baseline: T1 alone on a clean, idle cluster (before the fix)

Cluster: `make kind-create` (control-plane + 3 workers), all leftover e2e
namespaces deleted, operator rebuilt from `0c8b424` and restarted, T1 the only
test running.

```
--- FAIL: TestE2E_AdmissionRejection_StatefulSetNudgeRecovery (326.61s)
    --- PASS: .../pod_creation_stays_rejected_and_the_CR_leaves_OK   (4.01s)
    --- FAIL: .../operator_nudges_the_StatefulSet_instead_of_waiting (60.00s)
    --- FAIL: .../all_data_pods_return_shortly_after_the_webhook...  (60.01s)
    --- PASS: .../cluster_returns_to_OK                             (104.01s)
```

Attribution of the two full-run failures, which differ from each other:

- **"pod creation stays rejected" was a load artifact.** It takes 4.01 s in
  isolation and only failed in the full run, where 34 e2e namespaces shared one
  operator with the default `MaxConcurrentReconciles = 1`. No code defect.
- **"operator nudges the StatefulSet" is a real operator defect.** It fails
  deterministically in isolation, on an idle cluster, with a freshly built
  binary. And because no nudge ever happens, the recovery subtest fails too and
  the cluster only comes back on the statefulset-controller's own backoff — the
  incident behaviour WP1 was written to eliminate, reproduced in miniature
  (subtest 4 needed another 104 s).
- **Correction to the full-run picture recorded earlier in this ticket:
  subtest 3 did not fail in the full run — it PASSED there at 15.02 s**
  (`tmp/e2e-full.log`), on the same nudge-less binary and with zero
  `"Nudged StatefulSet"` lines in the log, i.e. purely on the
  statefulset-controller's own retry. Isolated, the same binary failed it at
  60.01 s. Two runs, same code, opposite outcomes: **subtest 3 is a coin flip
  against an unfixed operator**, because the residual wait after the block
  clears is roughly uniform in `[0, current backoff]` and the backoff sits at
  40–164 s when a 90 s hold ends. It is a valid check that the fixed behaviour
  is fast, but on its own it was never a reliable regression guard. See NA13.

#### Root cause: the operator goes dormant exactly where the nudge is needed

`reconcileWorkload` only requeues for two phases
(`internal/controller/valkey_controller.go:275`):

```go
if valkey.Status.Phase == vkov1.ValkeyPhaseError || valkey.Status.Phase == vkov1.ValkeyphaseSyncing {
    return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
}
```

A cluster whose pod creates are rejected reports **`Provisioning`**
(`updateHAStatus`: `"HA cluster provisioning: 0/3 valkey, 3/3 sentinel"`), which
is not in that set. Nothing else re-enters `Reconcile` in that state:

- the CR watch uses `GenerationChangedPredicate`, so status writes do not
  re-trigger;
- the data StatefulSet is not written (no spec drift), so `Owns()` produces no
  event;
- there are zero pods, so there are no pod events either.

On top of that the nudge needs **two** passes: the first only records the
observation for `nudgeGracePeriod`, and the bump happens on a later one. With no
requeue there is no later one — the grace period is unreachable by construction.

Measured evidence from the baseline run (polling the live objects every 3 s):

- data StatefulSet at `status.replicas=0 / spec.replicas=3` from 18:38:14 to
  18:43:17 — **5 min 03 s**, 98 consecutive samples;
- `metadata.resourceVersion` constant at `26322` across all 98 samples: the
  operator wrote the object **zero times**;
- `vko.gtrfc.com/nudge` **never set**, in any sample;
- `"Instance not healthy, requeuing"` appears **0 times** in the entire operator
  log, and `"Nudged StatefulSet"` **0 times** across the whole 3261-line log of
  the earlier full run covering 34 namespaces.

**This corrects a stated fact in WP1.** Its implementation note claims "the
reconciler requeues unhealthy instances every 10 s, so the grace period costs at
most one requeue cycle". That holds for `Error`/`Syncing` only. For the failure
mode WP1 targets the phase is `Provisioning` and the requeue never happens, so
the feature could not work — it was not a timing miss but a dead code path.

#### Fix

- `nudgeStatefulSet` and `nudgeShortStatefulSets`
  (`internal/controller/nudge.go`) now **return whether a StatefulSet is short of
  pods** — true on every non-bumping path too (inside the grace period, inside
  the rate limit, patch failed), because each of those still requires the caller
  to come back. A rolling update in progress returns false: it drives its own
  requeue and must not get a second clock.
- New `nudgeRequeueInterval` (5 s, `nudge.go`): `reconcileWorkload` requeues on
  that signal regardless of phase. Shorter than `nudgeGracePeriod` so the first
  bump lands one requeue after the short state is first observed, instead of
  sitting exactly on the boundary.
- No API change, no RBAC change, no new state.

#### Verification

Same cluster, operator rebuilt with the fix:

```
--- PASS: TestE2E_AdmissionRejection_StatefulSetNudgeRecovery (129.63s)
    --- PASS: .../pod_creation_stays_rejected_and_the_CR_leaves_OK   (4.01s)
    --- PASS: .../operator_nudges_the_StatefulSet_instead_of_waiting (10.01s)
    --- PASS: .../all_data_pods_return_shortly_after_the_webhook...   (9.02s)
    --- PASS: .../cluster_returns_to_OK                               (6.01s)
```

`All three data pods recreated 9.021880958s after the webhook was removed`
— against 5 min 29 s in the infra-d incident and a blown 60 s deadline in the
baseline above.

- Unit guards added (`internal/controller/nudge_test.go`): the short state is
  reported on the first observation (inside the grace period), a complete
  cluster is not, a short Sentinel StatefulSet alone still is, a rolling update
  reports false, and `TestReconcileWorkload_RequeuesWhileShortOfPods` asserts a
  positive `RequeueAfter` while the CR is in `Provisioning` — the exact
  regression.
- Mutation check: deleting the `if shortOfPods` block fails
  `TestReconcileWorkload_RequeuesWhileShortOfPods` with `"0s" is not positive`.
- `make test-unit`, `make test-integration`, `make lint` (0 issues),
  `make cyclo` (all < 15), `make gosec` (0 issues) — all green.
- No regression in the neighbouring guards: `TestE2E_AdmissionRejection_ReconcileBlockedCondition`,
  `TestE2E_AdmissionRejection_ReconcileContinuesPastRejectedWrite`,
  `TestE2E_PodDisruptionBudget_*` and `TestE2E_AntiAffinity_*` all PASS
  (7 tests, 56 s, run in parallel with each other).
- **One existing test needed a fixture fix, not an assertion change.**
  `TestReconcile_RollingUpdate_StandaloneNoRequeueWhenNoChange` seeded a ready
  pod but let the reconcile create the StatefulSet, which under the fake client
  (no statefulset-controller) stays at `status.replicas = 0` — i.e. "short of
  pods", so it now requeues. New helper `readyStatefulSetFor`
  (`internal/controller/rolling_update_test.go`) seeds a StatefulSet status
  consistent with the pods the test creates, so the assertion stays about
  rolling-update requeues.

#### Recovery target: drift resolved

Three numbers, each with a distinct role, now stated the same way in ticket and
test:

| Number | Role |
|---|---|
| ~30 s | design bound: `nudgeGracePeriod` (10 s) + `builder.NudgeInterval` (20 s) |
| 60 s | asserted `admissionRecoveryDeadline` — CI headroom for pod scheduling |
| 9.02 s | measured on an idle 4-node Kind cluster |

WP1's original "≤ 30 s" is the design bound and is met; the test keeps 60 s so a
loaded CI runner cannot flake it, and logs the actual elapsed time.

#### Consequence for the other items

- **NA2 is unaffected and still open.** T1's dormancy was not backoff decay:
  `reconcileResources` succeeded throughout, so no error was returned and no
  backoff accumulated. NA2's unbounded backoff is a separate path that applies
  when a managed write is being rejected.
- **NA4 was unaffected by this fix and has since been fixed separately.** The
  new requeue is computed *after* `checkAndHandleRollingUpdate` and
  `handlePostRollingUpdateChecks`, so at the time both early returns still
  skipped the nudge and its clock — NA1's fix removed the dormancy, not the
  ordering. NA4 moved the nudge to the top of `reconcileWorkload`; see NA4
  below.
- **Accepted cost of the fix:** a CR that stays short of pods now reconciles
  every 5 s for as long as that lasts (previously: not at all). For a
  permanently stuck StatefulSet — quota, missing PVC — that is a standing poll.
  It is bounded per affected CR, stops the moment `status.replicas` reaches
  `spec.replicas` (created, not ready), and the passes self-limit because
  `updateStatus` probes pods with a 5 s client timeout. Deliberately not
  backed off: the recovery guarantee is the point of WP1.

### NA2 — Cap the reconcile error backoff — DONE

**Status (2026-08-19):** fixed in `internal/controller/ratelimiter.go` (new) and
`SetupWithManager`. Measured on the 4-node Kind cluster.

#### The defect

`Reconcile` returns the joined resource error
(`internal/controller/valkey_controller.go`) and `SetupWithManager` configured no
rate limiter, so consecutive blocked passes backed off on controller-runtime's
default `ItemExponentialFailureRateLimiter`: 5 ms · 2^n capped at **1000 s**.

Nothing else rescues the operator while it waits: CR status writes are filtered
by `GenerationChangedPredicate`, there is no Pod watch, and a rejected write does
not mutate the object, so `Owns()` fires no event. Everything that hangs off a
pass inherits the delay — `ReconcileBlocked` keeps naming a webhook that is
already healthy, the phase stays `Error`, and the StatefulSet nudge (which only
runs inside a pass) slows with it.

**Severity restated after doing the arithmetic — the original "~16 min" framing
was misleading.** The exponent advances once per failed pass and the delay
between passes *is* the backoff, so reaching the 1000 s ceiling takes about
**22 min of continuous failure**, not minutes. The accurate and sharper
statement is that **the wait before the next look grows to roughly the length of
the outage**:

| continuous failure so far | next retry is |
|---|---|
| 41 s | 41 s away |
| 1.4 min | 82 s away |
| 5.5 min | 5.5 min away |
| 21.8 min | 16.7 min away (ceiling) |

So a short webhook gap was never the bad case; a long one — a broken policy, a
missing PVC, an exhausted quota — is, and the CR then reports a stale condition
for about as long as the outage lasted.

#### The fix

`newReconcileRateLimiter` (`internal/controller/ratelimiter.go`) keeps
controller-runtime's default shape — `MaxOf(per-item exponential, overall token
bucket)` with the bucket unchanged at 10 qps / burst 100 — and caps the
exponential at **30 s** (`reconcileRetryMaxDelay`) instead of 1000 s.
`reconcileControllerOptions()` wraps it so `SetupWithManager` can pass it via
`WithOptions`, and so the wiring is unit-testable. 30 s still means a real
backoff: 5 ms doubling reaches the cap only after 13 consecutive failures,
about 41 s of continuous rejection.

`golang.org/x/time` moves from indirect to direct in `go.mod` — the module was
already in the build graph via client-go; only the annotation changes.

Deliberately **not** done: swallowing the error and returning `RequeueAfter`
instead. That would give a flat 5 s retry but drop the reconcile out of
`controller_runtime_reconcile_errors_total` and out of controller-runtime's error
log, trading observability for a marginal latency gain.

#### Verification

**Measured cadence under a sustained block.** A `Valkey` CR created in a
namespace where a fail-closed webhook rejects `CREATE configmaps`, held for
4 minutes, reconcile passes extracted from the operator log:

```
gaps between consecutive passes (s): 1, 1, 1, 2, 6, 10, 20, 30, 30, 30
MAX GAP: 30.0
```

The curve climbs and then flattens exactly at the cap, at the step where the
default would have gone to 40.96 s and on to 81.92, 163.84, … (the early gaps
are coarse because the log timestamps have 1 s resolution).

**Measured staleness after the cause disappears.** Removing the webhook at the
end of that same 4-minute block:

```
ReconcileBlocked=False after 13s   (reason: ReconcileSucceeded)
```

13 s, inside the 30 s cap. Under the old limiter the pending delay after four
minutes of continuous failure is 163.84 s, so the CR could have kept reporting
`AdmissionWebhookDenied` for up to ~2.7 minutes after the webhook was healthy.
This is the second half of NA2's original done-when, met.

- Unit guards (`internal/controller/ratelimiter_test.go`): the cap is reached and
  never exceeded, the first failure still retries at 5 ms, retries do back off,
  `Forget` resets a recovered CR, backoff is per-item so one stuck CR cannot slow
  another, and `reconcileControllerOptions()` actually carries the capped limiter.
- **Mutation check, second attempt.** The first version of the test asserted the
  measured delay against `reconcileRetryMaxDelay` itself, so raising the constant
  back to 1000 s kept it green — a circular test that guarded nothing. Fixed by
  asserting against an independent policy ceiling `maxTolerableRetryDelay`
  (60 s); the mutation now fails with `"16m40s" is not less than or equal to
  "1m0s"` in both the limiter and the wiring test.
- `make test-unit`, `make test-integration`, `make lint` (0 issues),
  `make cyclo` (< 15), `make gosec` (0 issues), `make generate-all` (no drift).
- `go test ./internal/controller/ -count=1` (without `-short`) fails only the
  three tests WP4 already documented as pre-existing — no new failures.
- E2E regression check with NA1 + NA2 both applied — all green:
  `TestE2E_AdmissionRejection_ReconcileBlockedCondition` 14.57 s,
  `..._ReconcileContinuesPastRejectedWrite` 51.61 s,
  `..._StatefulSetNudgeRecovery` 126.62 s (all four subtests PASS; nudge 10.01 s,
  pod recovery 10.01 s).

**No e2e added, on purpose.** An e2e for this would hold the block, remove it and
assert the condition clears within N seconds. Against an unfixed operator the
residual wait is roughly uniform in `[0, current backoff]`, so with a 90 s hold
(backoff ≈ 82 s) and a 45 s window such a test would catch the regression only
about half the time — the same coin-flip defect NA13 describes for T1's
subtest 3. The deterministic guards are the unit tests plus the cadence
measurement above; the existing
`TestE2E_AdmissionRejection_ReconcileBlockedCondition` continues to cover the
functional flip.

### NA3 — Eliminate the phase flapping on blocked passes — DONE

**Status (2026-08-19):** fixed in `internal/controller/valkey_controller.go` and
`internal/controller/reconcile_blocked.go`. Unit-tested including a mutation
check; not exercised on a cluster.

#### The defect

While `reconcileResources` failed but the data plane was healthy (T2's exact
scenario), every pass wrote the phase twice with opposite values:
`updateStandaloneStatus`/`updateHAStatus` computed the health phase (**OK**),
then the final `updatePhase` overwrote it with **Error**. `statusUnchanged`
could not suppress the first write because the previous pass's final value was
Error. Watchers (Lens, `kubectl get -w`, monitoring on `status.phase`) saw
OK↔Error oscillation on every blocked pass. The ticket recorded "two status
writes disagree" but missed the visible flapping.

#### The fix — one phase authority per blocked pass

A blocked pass is marked on the **context** (`withBlockedPass`/`passIsBlocked`,
`reconcile_blocked.go`), set in `Reconcile` as soon as `reconcileResources`
returns an error. The flag rides on the context rather than on the reconciler so
it stays per-pass and per-CR with `MaxConcurrentReconciles > 1`.

Three consequences, which together make "at most one phase write" literal:

- `updatePhase` drops the write while blocked and delegates to the new
  `writePhase` otherwise. This covers **every** intermediate phase write in the
  pass, not just `updateStatus`'s — the rolling-update progress phases
  (`rolling_update.go`, 8 call sites), the Sentinel-RU error phase and the
  no-master recovery phase would have flapped against Error the same way.
- `persistStatus` (new, shared tail of `updateStandaloneStatus` and
  `updateHAStatus`) restores the previous phase and message before writing while
  blocked. Everything else — `readyReplicas`, `masterPod`, `observerReady`, the
  `Ready` condition — keeps updating: a rejected managed write says nothing about
  the running data plane. This is the middle ground the review asked for.
- The final `writePhase` bypasses the suppression and is the pass's single phase
  authority.

`Reconcile` was restructured so that write can no longer be skipped: it used to
`return` on a workload error *before* the phase write. Now the workload result is
kept and, when blocked, the Error phase is written and
`errors.Join(resourceErr, workloadErr)` returned, so a workload failure is
neither lost from the error nor able to leave the phase on the previous pass's
value.

#### The open datum from NA2 is settled: the write was skipped, not lost

The sample that read `phase=Provisioning blocked=True` during the sustained
`CREATE configmaps` block is explained by exactly that early return, not by a
silently failing status write. Reproduced as a mutation: restoring the old
`if workloadErr != nil { return }` in front of the phase write makes
`TestReconcile_BlockedPassWritesPhaseEvenWhenWorkloadFails` fail with

```
expected: "Error"
actual  : "Provisioning"
"Setting up Valkey resources" does not contain "Failed to reconcile resources:"
```

i.e. the field symptom, in a unit test. Any error path inside `reconcileWorkload`
(rolling-update check error, a failing `updateStatus`) produced it. Not
re-verified against a live cluster — the field sample itself was one observation.

#### Verification

- `internal/controller/status_phase_test.go` (new):
  `TestReconcile_BlockedPassDoesNotFlapPhase` records every status write of the CR
  through a `SubResourceUpdate` interceptor and asserts no OK phase is written and
  the phase does not change at all across a blocked pass, while `readyReplicas`
  still tracks the ready StatefulSet; `TestReconcile_BlockedPassRecoversToHealthPhase`
  asserts the health phase takes over again once the write succeeds;
  `TestReconcile_BlockedPassWritesPhaseEvenWhenWorkloadFails` covers the datum
  above; `TestUpdatePhase_SuppressedWhileBlocked` /
  `TestUpdatePhase_WritesWhenPassIsNotBlocked` /
  `TestUpdateStatus_KeepsNonPhaseFieldsWhileBlocked` pin the mechanism.
- **Mutation check.** Making `passIsBlocked` always return false fails
  `TestReconcile_BlockedPassDoesNotFlapPhase`, `TestUpdatePhase_SuppressedWhileBlocked`
  and `TestUpdateStatus_KeepsNonPhaseFieldsWhileBlocked` (each with
  `expected "Error", actual "OK"`); the separate `Reconcile` mutation above fails
  the fourth test. No test passes against the unfixed behaviour.
- `make test-unit`, `make test-integration`, `make lint` (0 issues), `make cyclo`
  (< 15), `make gosec` (0 issues).
- `go test ./internal/controller/ -count=1` (without `-short`) fails only the three
  tests WP4 documented as pre-existing — confirmed identical on the branch with the
  change stashed.
- No e2e added: the flap is a property of a single pass, and a poll-based e2e would
  have to sample faster than the pass to observe it. The unit test observes every
  write directly, which is strictly stronger.

### NA4 — Nudge is unreachable while a Sentinel rolling update waits on quorum — DONE

**Status (2026-08-19):** fixed in `internal/controller/valkey_controller.go`
(call order) and `internal/controller/nudge.go` (suppression scope).

#### The defect

`reconcileWorkload` reached `nudgeShortStatefulSets` only when neither rolling
update returned early. The Sentinel quorum guard
(`internal/controller/rolling_update.go`, `checkAndHandleSentinelRollingUpdate`)
returns `NeedsRequeue` while a deleted Sentinel pod is missing — so if pod
creation is blocked mid-Sentinel rolling update, the operator loops on the
quorum wait and **neither** StatefulSet is ever nudged. The 5 min 29 s
statefulset-controller tail returns for exactly that constellation.

Root cause as diagnosed during review, confirmed: the data rolling update
persists its state in an annotation (which nudge suppression keys on), the
Sentinel rolling update has no persisted state — its nudge suppression was
implicit control flow, i.e. the *position* of the call.

#### The fix

Two changes, both small; the second is what makes the first safe.

1. **The nudge runs first.** `shortOfPods := r.nudgeShortStatefulSets(...)` moved
   to the top of `reconcileWorkload`, before `checkAndHandleRollingUpdate` and
   `handlePostRollingUpdateChecks`. Position no longer decides who gets nudged.
2. **Suppression became per StatefulSet.** `nudgeShortStatefulSets` previously
   returned early for *both* StatefulSets when the data-RU annotation was set.
   NA4 left only the data StatefulSet suppressed by it:

   ```go
   short := false
   if r.getRollingUpdateState(v) == "" {
       short = r.nudgeStatefulSet(ctx, v, dataKey)
   } else {
       r.nudges.forget(dataKey)
   }
   if v.IsSentinelEnabled() && r.nudgeStatefulSet(ctx, v, sentinelKey) {
       short = true
   }
   ```

   **Superseded by NA15 (2026-08-20):** that remaining branch is gone; both
   StatefulSets are now nudged unconditionally. NA4's own argument turned out to
   apply to the data rolling update word for word — see NA15. The code block above
   is kept as the historical intermediate state, not as current code.

**The open suppression question, answered: no Sentinel-RU suppression at all.**
The ticket left it open; the argument that settles it is that the Sentinel
rolling update deletes one pod and then *waits for that exact pod to come back*.
Suppressing the nudge there suppresses it precisely where it is the only lever.
The nudge is harmless under OnDelete — `AnnotationNudge` lives on the
StatefulSet metadata, not the pod template, so it is invisible to
`SentinelStatefulSetHasChanged` and the resync it triggers recreates the pod
from the current template, which is the rolling update's own next step.

The same reasoning closed a mirror gap that fell out for free: a **data** rolling
update never deletes a Sentinel pod, so a short Sentinel StatefulSet during one
is a genuine stall — and the quorum it costs is what the data rolling update is
waiting on. Under the old blanket suppression that case was dormant too. Scope
widened by three lines, deliberately, and stated here rather than silently.

What NA4 did **not** do, and should have: apply the same argument to the data
rolling update, which also deletes one pod and then waits for that exact pod. The
half-measure left an unexplained asymmetry that NA15 removed.

Nudge noise during a *healthy* Sentinel rolling update is bounded by
construction: `status.replicas` counts created pods, not ready ones, so a
recreated pod clears the short state within seconds, while a bump needs the
short state to survive `nudgeGracePeriod` (10 s) across two passes 10 s apart.

#### Verification

- New unit tests (`internal/controller/nudge_test.go`):
  `TestReconcileWorkload_NudgesDataStatefulSetDuringSentinelQuorumWait` and
  `TestReconcileWorkload_NudgesSentinelStatefulSetWhileRecreationBlocked` build
  the NA4 constellation (no data RU; sentinel-2 deleted and not recreated; the
  two survivors ready and outdated → `readyCount-1 = 1 < quorum = 2`), assert the
  pass really ends in the quorum wait (`RequeueAfter == rollingUpdateRequeueDelay`)
  and that the respective StatefulSet is nudged anyway.
  `TestNudgeShortStatefulSets_NudgesSentinelDuringDataRollingUpdate` pinned the
  suppression scope (renamed to `..._NudgesBothDuringDataRollingUpdate` by NA15,
  which inverted its data-half assertion).
- Mutation checks, both run: moving the nudge call back below the rolling-update
  checks fails both new `reconcileWorkload` tests ("Should NOT be empty");
  restoring the blanket suppression failed the scope test (that second mutation
  check is obsolete since NA15 — there is no suppression left to restore; NA15
  runs its own).
- `make test-unit`, `make test-integration`, `make lint` (0 issues), `make cyclo`
  (all < 15), `make gosec` (0 issues) — all green. No existing test needed an
  assertion change; one message in
  `TestNudgeShortStatefulSets_NoRequeueSignalDuringRollingUpdate` was narrowed to
  say "data" because it is now a statement about the data StatefulSet only. (That
  test was rewritten by NA15 as `..._ReportsShortDuringRollingUpdate` plus
  `TestReconcileWorkload_RollingUpdateKeepsRequeueAuthority`.)
- No e2e added: the fix is a call-order property of a single pass, which the unit
  tests observe directly and completely. A poll-based e2e would only re-observe
  the WP1 recovery already measured in NA1.

#### Boundary: what NA4 does not cover

The nudge clock still depends on the pass returning *some* requeue. Both quorum
paths do (`rollingUpdateRequeueDelay`, 10 s — shorter than the 20 s
`builder.NudgeInterval`, so the 5 s `nudgeRequeueInterval` would buy nothing and
was deliberately not plumbed through the early returns). The Sentinel-RU **error**
path still returned `ctrl.Result{}, true` with no requeue and no error — that was
**NA5**, since fixed (below). Both Sentinel-RU exits now keep the clock running:
the wait through its requeue, the error through the rate limiter.

### NA5 — Pre-existing: Sentinel-RU error path stalls reconciliation — DONE

**Status (2026-08-19):** fixed in `internal/controller/valkey_controller.go`
(`handlePostRollingUpdateChecks`).

#### The defect

`handlePostRollingUpdateChecks` returned `ctrl.Result{}, true` on a Sentinel
rolling-update **error** — no returned error, no RequeueAfter. `Reconcile` then
ended without any requeue, and CR status writes do not re-trigger
(GenerationChangedPredicate), so reconciliation stalled until some owned-object
event happened to arrive. Verified unchanged from `main` — predates this branch;
the WP3 refactor preserved it.

The errors that reach this path are API failures inside
`checkAndHandleSentinelRollingUpdate` (`internal/controller/rolling_update.go`):
a non-NotFound Get on the Sentinel StatefulSet or on a Sentinel pod, or a failed
pod Delete — exactly the transient conditions a retry exists for.

#### The fix

`handlePostRollingUpdateChecks` returns `(ctrl.Result, bool, error)`; the
Sentinel-RU error path returns the error, and `reconcileWorkload` passes it
through (`return result, err`). The retry is then the controller-runtime rate
limiter — capped at 30 s by this branch's own backoff cap — which mirrors the
data rolling-update error path a few lines above.

The phase write stays: the error is visible on the CR *and* drives a retry.

**Not converted: the no-master recovery path.** It returns
`ctrl.Result{RequeueAfter: 10 * time.Second}, true, nil` and keeps its own retry
clock, so it was never part of the stall. Left as it was, with a comment saying
why, rather than folded in for symmetry.

#### Verification

- New unit test (`internal/controller/rolling_update_test.go`,
  `TestReconcileWorkload_RetriesAfterSentinelRollingUpdateError`): the NA4 quorum
  fixture with an interceptor that fails the Get of Sentinel pod 0 with a
  non-NotFound API error. Asserts `reconcileWorkload` returns an error naming
  that pod, that the CR phase is `Error`, and that `RequeueAfter` stays zero —
  the retry comes from the rate limiter, not from a second clock.
- Mutation check: returning `nil` instead of `sentinelResult.Error` from that
  path fails the new test.
- `make test-unit`, `make test-integration`, `make lint` (0 issues), `make cyclo`
  (all < 15), `make gosec` (0 issues) — all green; no existing test needed a
  change.
- No e2e added: the property is "this pass returns an error", which the unit test
  observes directly; an e2e would have to inject an API failure to see it.

### NA6 — maxUnavailable ≥ replicas warning misses the scale-down path — DONE

**Status (2026-08-19):** fixed in `internal/controller/pdb.go`
(`reconcileDataPodDisruptionBudget`, new `warnIfDataBudgetProtectsNothing`).

#### The defect

The warning fired only when the PDB was created or updated (`changed`,
`internal/controller/pdb.go:46`). Scaling `spec.replicas` down (e.g. 5 → 2 with
`maxUnavailable: 2`) leaves the PDB object byte-identical — same maxUnavailable,
same selector — so `PodDisruptionBudgetHasChanged` reports no drift, nothing is
written and no warning was emitted, though the budget now permits evicting every
data pod at once. The write-gated warning was silent for exactly the change that
removed the protection.

#### The fix

The condition is evaluated in `warnIfDataBudgetProtectsNothing`, called on every
pass in which the data budget applies, after the reconcile of the object rather
than inside its write branch. `reconcileDataPodDisruptionBudget` no longer looks
at whether anything was written.

The warning now has two channels: the log line as before, plus a Warning Event
`PodDisruptionBudgetTooPermissive` on the CR (reason constant
`reasonPodDisruptionBudgetTooPermissive`), which is what makes the misconfiguration
visible without reading operator logs. Repetition costs nothing on the Event side —
the recorder aggregates a repeated Event into one series — and the log side is
bounded by reconcile activity, since a healthy pass returns `ctrl.Result{}` with no
periodic requeue.

**Caveat, and it is NA12's:** the Event is written through
`k8s.io/client-go/tools/events`, i.e. `events.k8s.io/v1`, which the operator RBAC
does not grant. Until NA12 lands, this Event is rejected by the API server like
every other one and only the log line survives. The fix is still complete for NA6 —
the condition is evaluated on the right passes — but the more visible half of it
stays dormant until the RBAC rule is added.

**Removed on the way:** `reconcilePodDisruptionBudget` returned a `changed bool`
whose only consumer was the write-gated warning. Both call sites now discard it, so
the return value was dropped rather than left dead (unparam would flag it anyway).
NA7, if it wants a create/update-gated Sentinel warning, has to decide that on its
own terms instead of inheriting a leftover.

#### Verification

- New unit tests in `internal/controller/pdb_test.go`:
  - `TestReconcileDataPodDisruptionBudget_WarnsAfterScaleDownWithoutWrite` — the
    ticket case: `replicas 5, maxUnavailable 2` creates the PDB and does not warn,
    then `replicas 2` warns while a PDB write counter (interceptor on Create and
    Update of `policyv1.PodDisruptionBudget`) stays at zero.
  - `TestReconcileDataPodDisruptionBudget_WarnsOnEveryApplicablePass` — the create
    pass and the following no-op pass both warn.
  - `TestReconcileDataPodDisruptionBudget_NoWarningBelowReplicas` — a protecting
    budget stays silent.
  - Test infrastructure: `fakeEventRecorder` (implements
    `events.EventRecorder`, collects Events) and `pdbWriteCounter`.
- Mutation check: re-gating the warning on "the PDB was just created" fails both
  new warning tests (0 of 1 events after the scale-down, 1 of 2 across two passes).
- `make test-unit`, `make test-integration`, `make lint` (0 issues), `make cyclo`
  (all < 15), `make gosec` (0 issues) — all green; no existing test needed a change.
- Docs: the `maxUnavailable` CRD field doc (`api/v1/valkey_types.go`, regenerated
  into `config/crd/bases` and the Helm chart via `make manifests`) and the
  `spec.podDisruptionBudget` section of README.md now state that the warning is a
  log line plus an Event on every reconcile, and that the scale-down path is
  covered.
- No e2e added: the property is "this pass warns without writing", which the unit
  tests observe directly; an e2e could only re-observe the same condition through
  Events that NA12 currently discards.

### NA7 — Sentinel PDB at 2 replicas blocks all drains, silently at runtime — DONE

**Status (2026-08-20):** fixed in `internal/controller/pdb.go`
(`reconcileSentinelPodDisruptionBudget`, new `warnIfSentinelBudgetBlocksEveryDrain`).

#### The defect

With `sentinel.replicas: 2`, minAvailable = quorum = 2 = replicas: every
voluntary eviction is refused indefinitely — node drains hosting these pods
stall until manual intervention. Only a builder comment documented this
(`internal/builder/pdb.go`); at runtime nothing warned. Inconsistent with the
data budget, which warned for the "too loose" direction but not the
"fully blocking" one.

#### The fix

The formula is unchanged — a smaller minAvailable would let a drain take the
Sentinel majority and thereby automatic failover. Only visibility was added:
`warnIfSentinelBudgetBlocksEveryDrain` logs and records a Warning Event
`SentinelPodDisruptionBudgetBlocksDrains` (reason constant
`reasonSentinelPodDisruptionBudgetBlocksDrains`) while
`SentinelQuorumFor(replicas) >= replicas`, mirroring the data-side warning of NA6.
Below `MinPDBReplicas` the path is never reached, so in practice the condition is
exactly `spec.sentinel.replicas: 2`. The message names the remedy (odd count of 3
or more) instead of only the symptom.

**Deviation from the ticket, deliberate:** the ticket asked for a *create/update*
gated warning. That reproduces the NA6 defect one-to-one on the Sentinel side —
scaling `spec.sentinel.replicas` 3 -> 2 leaves the quorum at 2, so the PDB object
stays byte-identical and a write-gated warning would be silent for exactly the
change that turned the budget into a drain blocker. The warning therefore runs on
every pass in which the Sentinel budget applies, as NA6 does, and NA6's note about
not inheriting a leftover `changed bool` is honoured: nothing was reintroduced.

**Same NA12 caveat:** the Event goes through `events.k8s.io/v1`, which the operator
RBAC does not grant yet, so until NA12 lands only the log line survives.

#### Verification

- New unit tests in `internal/controller/pdb_test.go`:
  - `TestReconcileSentinelPodDisruptionBudget_WarnsWhenQuorumEqualsReplicas` — 2
    Sentinels: the PDB is still created with `minAvailable: 2` (formula unchanged)
    and a Warning Event names `minAvailable 2 equals spec.sentinel.replicas 2`.
  - `TestReconcileSentinelPodDisruptionBudget_WarnsAfterScaleDownWithoutWrite` —
    3 -> 2 Sentinels warns while the PDB write counter stays at zero.
  - `TestReconcileSentinelPodDisruptionBudget_WarnsOnEveryApplicablePass` — create
    pass and following no-op pass both warn.
  - `TestReconcileSentinelPodDisruptionBudget_NoWarningAtOddCount` — 3 and 5 stay
    silent.
  - Reuses the NA6 test infrastructure (`fakeEventRecorder`, `pdbWriteCounter`).
- Mutation check: gating the warning on "the PDB did not exist before this pass"
  fails `WarnsAfterScaleDownWithoutWrite` and `WarnsOnEveryApplicablePass`, i.e.
  the ticket's own wording is what the tests reject.
- `make test-unit`, `make test-integration`, `make lint` (0 issues), `make cyclo`
  (all < 15), `make gosec` (0 issues) — all green; no existing test needed a change.
- Docs: `spec.sentinel.replicas` and `spec.podDisruptionBudget.enabled` field docs
  (`api/v1/valkey_types.go`, regenerated into `config/crd/bases` and the Helm chart
  via `make manifests`), the PDB comment block in
  `deploy/helm/valkey-operator/values.yaml`, and the `spec.podDisruptionBudget`
  section of README.md now state the 2-replica consequence, the drain stall, and
  that the warning repeats on every reconcile including the scale-down path.
- No e2e added: the property is "this pass warns", which the unit tests observe
  directly; an e2e could only re-observe it through Events that NA12 discards.

### NA8 — CI has no multi-node e2e; T3 drain semantics and T5 hard are unguarded — DONE

**Status (2026-08-20):** fixed in `.github/workflows/release.yml` (the `e2e-tests`
job is now a two-leg matrix), `Makefile` (`E2E_RUN`) and
`test/e2e/affinity_test.go` (`requireThreeSchedulableNodes`).

#### The defect

`.github/workflows/release.yml` built a single-node DinD Kind cluster (including a
`docker exec ...-control-plane sysctl` step that assumed one node). The multi-node
behaviors — eviction serialization (T3) and hard-mode spread across nodes (T5) —
ran only on developer machines. Worse than untested: `TestE2E_AntiAffinity_HardSpreadsAcrossNodes`
skips below three schedulable nodes, so CI reported the missing coverage as green.

#### The fix

`e2e-tests` became a matrix with two legs. As first built they were serialized
with `max-parallel: 1` (shared-DinD-host assumption); commit `8ecc038` removed
that: each leg lands on its own ephemeral ARC runner pod, the distinct cluster
names keep them collision-free, and a new `e2e-gate` job named "E2E Tests"
aggregates both legs so the pre-existing required check keeps one stable name:

| Leg | Cluster | Scope |
|-----|---------|-------|
| `single-node` | control-plane only, cluster `valkey-operator-test` | full suite, unchanged |
| `multi-node` | control-plane + 3 workers, cluster `valkey-operator-test-multinode` | `E2E_RUN=TestE2E_AntiAffinity\|TestE2E_PodDisruptionBudget` |

Everything that assumed one node is now per-node: the kind config renders one
`- role: worker` line per `KIND_WORKERS`, the inotify sysctl step and the image
import/verification loop over `kind get nodes --name "${KIND_CLUSTER}"`. The image
import matters — the operator runs with `pullPolicy: Never`
(`test/e2e/helm-values.yaml`) and on a multi-node cluster its pod can land on any
worker.

**Three workers, not the ticket's "≥ 2":** Kind only removes the control-plane
`NoSchedule` taint on single-node clusters, and `schedulableNodeCount`
(`test/e2e/affinity_test.go`) correctly discounts tainted nodes. With 2 workers the
hard-spread test would still skip — i.e. the ticket's own lower bound would have
reproduced the defect it was written to fix.

**Skips do not count as coverage.** Two guards make that explicit:

- `E2E_REQUIRE_MULTI_NODE=true` (set only on the multi-node leg) turns the
  "fewer than 3 schedulable nodes" skip into `t.Fatalf`, so a cluster that came up
  smaller than requested fails instead of quietly passing.
- The workflow step greps the test output for
  `--- PASS: TestE2E_AntiAffinity_HardSpreadsAcrossNodes` and
  `--- PASS: TestE2E_PodDisruptionBudget_SerializesEvictions`; a renamed test that
  stops matching `E2E_RUN` fails the leg instead of leaving it vacuously green.
  The `Verify Kind cluster` step additionally asserts the node count equals
  `workers + 1` before any test runs.

**No real `kubectl drain` test, deliberate:** draining a worker evicts the pods of
every other e2e test scheduled on it, and the suite runs `t.Parallel()`. The
Eviction API — the mechanism `kubectl drain` itself uses and the only one a PDB can
gate — is asserted directly instead; the multi-node leg is what puts more than one
node under those assertions. Documented in the `test/e2e/pdb_test.go` header.

DEVELOPER.md (the ticket's alternative) does not exist in this repo, so the topology
is documented in `CLAUDE.md` ("E2E cluster topology") and README.md instead.

#### Verification

- `.github/workflows/release.yml` parses as YAML; every rewritten `run:` block
  passes `bash -n`.
- The kind-config generator was executed for both legs: `workers: 0` yields exactly
  `[control-plane]`, `workers: 3` yields `[control-plane, worker, worker, worker]`.
  A first version used `seq 1 "$KIND_WORKERS"`, which on BSD `seq` counts *down*
  and emitted two workers for the single-node leg — replaced by an explicit
  `while` loop.
- The `E2E_RUN` guard was simulated against a stub `make`: passes when both PASS
  lines are present, exits 1 when the hard-spread test is missing, and stays inert
  on the unfiltered single-node leg.
- `make test-e2e E2E_RUN='TestE2E_CompileCheckOnly_NoSuchTest'` compiles the
  `e2e`-tagged package (0 tests run) — the new `os` import and helper build.
- `make lint` (0 issues, gofmt clean) and `make test-unit` green. `make -n test-e2e`
  shows the unfiltered command is unchanged when `E2E_RUN` is empty.
- **Multi-node leg verified in CI (2026-08-20):** run 32359935550 (@ `519eaa4`)
  is green on both legs plus the `e2e-gate` aggregator. The multi-node leg
  finished in ~6 min with `TestE2E_AntiAffinity_HardSpreadsAcrossNodes` PASS
  (data and sentinel pods each on three distinct nodes) and both eviction
  refusals PASS on the first attempt — the resource risk originally noted here
  (4-container Kind cluster on the runner) did not materialize. The race in
  `assertSecondEvictionRefused` was closed by NA10 — the helper retries and no
  longer depends on a single observation of the refusal window.

### NA9 — Release note: default anti-affinity rolls every multi-replica cluster — DONE (closed without change, premise disproven)

**Original claim, now known to be wrong for the data StatefulSet:** WP5's
default-soft term changes the pod-spec hash of every multi-replica cluster, so
the first operator upgrade containing it was expected to trigger a
failover-aware rolling update per cluster that would not otherwise happen — "an
orchestrated mass-failover event, not a footnote".

**Verified against the source (2026-08-20):**

- Every Valkey data pod carries the operator image as its sidecar container:
  `buildSidecarContainer` sets `Image: operatorImage`
  (`internal/builder/statefulset.go:759-766`), and the Helm chart injects the
  operator's own tag there —
  `--operator-image={{ .Values.image.repository }}:{{ .Values.image.tag | default .Chart.AppVersion }}`
  plus the identical `OPERATOR_IMAGE` env
  (`deploy/helm/valkey-operator/templates/deployment.yaml:42,47-48`).
  `ComputePodSpecHash` covers the whole PodSpec including that sidecar, so
  **every** operator release already rolls every data StatefulSet with a
  controlled failover. WP5 adds no roll there; it rides along in the same pass.
- Sentinel pods carry no sidecar: `buildSentinelPodSpec`
  (`internal/builder/sentinel.go:367-380`) builds only the init container and
  `buildSentinelContainer`, both on `v.Spec.Image`, and
  `ComputeSentinelPodSpecHash` hashes exactly that. So the Sentinel StatefulSet
  is the one pod class that a plain operator upgrade does not roll, and WP5's
  term does roll it once for every cluster with `sentinel.replicas >= 2`.
- Install-path caveat, verified: `config/manager/manager.yaml` does not pass
  `--operator-image`, so on the kustomize path `OperatorImage` is empty and the
  sidecar falls back to the constant `ghcr.io/guided-traffic/valkey-operator:latest`
  (`internal/builder/statefulset.go:759-762`) — a static string that no upgrade
  changes. The same holds for a Helm install pinning a floating `image.tag`.
  In those setups the data roll would be new.

**Decision (2026-08-20, Hans):** the sidecar pinned to the operator version, and
therefore the fleet-wide data-pod rotation with one controlled failover per
multi-replica cluster on every operator release, is **intended behaviour**. Since
that rotation is the norm, the one-time Sentinel roll does not warrant a special
release-note entry either. NA9 is closed with no change to the release notes, the
release pipeline, or the docs. The existing README paragraph on the pod-spec hash
(`README.md:640-644`) stays as the only written mention.

**Fourth review addendum, settled with NA25 (closed 2026-08-20):** the NA20
init-script change also rolls multi-replica **non-Sentinel** clusters on the
kustomize/floating-tag path, where the sidecar constant never changes. Hans
extended this NA9 decision to cover it: the rotation is the norm on the
canonical install path, so it gets no release-note entry either; non-canonical
paths are the deviating admin's responsibility (NA24 doctrine).

**Not done, deliberately:** no `BREAKING CHANGE:` footer, no semantic-release
config change, no major version bump — all three were on the table and were
rejected with the premise.

### NA10 — Minor hygiene (bundle) — DONE

**Status (2026-08-20):** all four items closed — three fixed, one accepted with a
comment. Touched: `internal/controller/nudge.go`,
`internal/controller/valkey_controller.go`, `internal/controller/pdb.go`,
`internal/controller/nudge_test.go`, `test/e2e/pdb_test.go`.

**1. nudgeTracker leak on CR deletion — fixed.**
`forgetNudges(namespace, name)` (`internal/controller/nudge.go`) drops both keys
(data and `-sentinel`) via `common.StatefulSetName`, and `Reconcile` calls it on
exactly the two exits that never reach `nudgeStatefulSet` again: the `IsNotFound`
branch and the `DeletionTimestamp` branch (`valkey_controller.go`). The names are
derived from the same helper the nudge path uses, so the two cannot drift apart.

**2. `setStatusCondition` swallowed errors — fixed (logged, still swallowed).**
Both the refresh `Get` and the `Status().Update` now log via
`log.FromContext(ctx)`; a `NotFound` on the refresh stays silent because the CR
being gone is not an error worth a stack in the log. The errors are still not
returned: every caller is a void helper, a condition is a report about the pass
and never a reason to fail it, and the write is self-healing — the next pass
recomputes the condition from live state and rewrites it unless it already
matches (`setReconcileBlockedCondition` dedups against the freshly fetched CR).
The corrected framing versus the ticket text: the dedup does not *cause* the
retry, it only suppresses a *redundant* one; a dropped write leaves the stale
condition on the API server, so the next pass sees a mismatch and writes.

**3. PDB e2e eviction race — hardened.**
`assertSecondEvictionRefused` (`test/e2e/pdb_test.go`) now retries the whole
two-eviction sequence up to `evictionRaceAttempts` (3) and folds the eviction
request into the poll loop that observes the exhausted budget
(`evictWhenBudgetExhausted`), at `evictionPollInterval` = 100 ms instead of the
1 s of `waitForPodDisruptionBudget`. Two retryable outcomes are distinguished
from a defect and logged as such: the exhausted budget never observed within
`evictionRaceTimeout` (30 s), and the second eviction granted because the
replacement was Ready again. A budget that is genuinely not enforced loses all
three attempts and fails with a message naming the PDB.
Each attempt first waits for `disruptionsAllowed > 0` — found by the mutation run
below, not by reading: the disruption controller republishes the budget a moment
*after* the pods report Ready, so a retry that evicted on `ReadyReplicas == 3`
alone got a 429 on its own **first** eviction and reported the recovery lag as a
defect.

**4. `current.Labels = desired.Labels` on PDB update — accepted, commented.**
Kept as an assignment with a comment at the site (`internal/controller/pdb.go`,
in `reconcilePodDisruptionBudget`). It is the repo-wide convention: the same line
appears at 10 further managed-object sites in `valkey_controller.go`
(StatefulSets, Services, ConfigMap, NetworkPolicy, RBAC). A PDB whose foreign
labels survive while its StatefulSet's do not is worse than either rule applied
consistently, so changing it is a convention-wide change and deliberately out of
scope here.

#### Verification

- New unit tests in `internal/controller/nudge_test.go`:
  `TestReconcile_ForgetsNudgesWhenCRIsDeleted`,
  `TestReconcile_ForgetsNudgesWhenCRIsGone`,
  `TestReconcile_KeepsNudgesOfOtherCRs` (the over-reach guard: another CR's two
  entries must survive). Mutation check: removing the two `forgetNudges` calls
  fails all three; the third also fails if `forgetNudges` cleared the whole map.
- `make test-unit`, `make test-integration`, `make lint` (0 issues, gofmt clean),
  `make cyclo` (all < 15), `make gosec` (0 issues) — all green, no existing test
  needed a change.
- E2E, run against the local 4-node Kind cluster (control-plane + 3 workers,
  operator image `valkey-operator:test`):
  `make test-e2e E2E_RUN='TestE2E_PodDisruptionBudget'` → PASS in 36.5 s, both
  eviction subtests refused on attempt 1 (0.20 s / 0.10 s), no retry needed.
- Mutation check of the retry path itself: with the poll condition inverted to
  `DisruptionsAllowed != 1`, so the second eviction always fires while the budget
  is full, both subtests ran all 3 attempts, recovered the pod set between them
  and failed with `the second concurrent eviction was never refused in 3
  attempts` (94.9 s). The first version of that run failed differently — a 429 on
  the *first* eviction of attempt 2 — which is what produced fix 3's
  `disruptionsAllowed > 0` pre-wait.
- **Not verified:** the two new log lines in `setStatusCondition` are not
  asserted by a test — the reconciler has no log sink in the unit setup, and the
  observable behaviour (which condition is written) is unchanged and already
  covered by `reconcile_blocked_test.go`. The "never observed" retry branch of
  the e2e was not exercised by a real run either; only the "granted" branch was
  (the mutation forces that one).

### NA11 — Sentinel StatefulSet is rewritten on almost every reconcile (found while executing NA1) — DONE

**Status (2026-08-20):** fixed. `buildSentinelPodSpec`
(`internal/builder/sentinel.go`) now sets `TerminationGracePeriodSeconds`
explicitly to `30` — deliberately the Kubernetes default, not the data
StatefulSet's `75`: Sentinel has no failover timeout to wait out on shutdown,
and `30` is exactly what the API server stored all along, so runtime behavior
is unchanged and only the permanent nil-vs-30 drift disappears.

One-time upgrade cost, unavoidable: the explicit field changes
`ComputeSentinelPodSpecHash`, so on the first reconcile after an operator
upgrade the `vko.gtrfc.com/pod-spec-hash` annotation differs and the existing
rolling machinery replaces the Sentinel pods once. Sentinel pods are stateless
(config rebuilt by the init container) and rolled serially, so quorum holds.

Verification:

- New unit tests in `internal/builder/sentinel_test.go`:
  `TestSentinelStatefulSetHasChanged_NoDriftAfterAPIServerDefaulting` (the
  ticket's guard: desired spec vs an API-server-defaulted copy of itself
  reports no drift) and `TestBuildSentinelPodSpec_ExplicitTerminationGracePeriod`
  (pins the value to 30 so nobody "mirrors" the data path's 75 later without
  noticing the behavior change). Mutation check: removing the two builder lines
  fails both.
- `make test-unit`, `make test-integration`, `make lint` (0 issues, gofmt
  clean), `make cyclo` (all < 15), `make gosec` (0 issues) — all green, no
  existing test needed a change.
- E2E against the local 4-node Kind cluster, patched operator image:
  `TestE2E_HAClusterWithSentinel` → 1 create, **0** updates;
  `TestE2E_RollingUpdate_HA_Idempotent` (the worst case in the NA1 log: 83
  updates) → 1 create, **2** updates, each one immediately before its
  "Rolling update complete" line, i.e. the two legitimate image changes
  (update + revert). Update-per-create ratio across both runs: 2/2 = 1.
- The 14 `Server rejected event` lines in the same log are NA12, untouched
  here.

Original observation and root-cause analysis follow.

Not part of the review of the work packages — this surfaced from the operator
logs captured for NA1 and is unrelated to the nudge. It is an **observation with
an unfinished root cause**, deliberately recorded as such.

Verified:

- In the 3261-line operator log of the aborted full e2e run (34 namespaces):
  **460** `"Updating Sentinel StatefulSet"` lines against **23**
  `"Creating Sentinel StatefulSet"` — i.e. roughly 20 rewrites per Sentinel
  StatefulSet that exists, in every sentinel-enabled namespace
  (`e2e-rolling-idempotent` alone: 83).
- The data StatefulSet does **not** behave that way: 16 `"Updating StatefulSet"`
  against 39 creates.
- It reproduces on an idle cluster: in the isolated T1 baseline the Sentinel
  StatefulSet of a single CR was rewritten 13 times between 18:38:01 and
  18:38:12, then stopped — reconciles stop when events stop, which is the same
  dormancy NA1 describes, so the rate is bounded by how often the operator runs
  at all.
- The asymmetry is **not** in the comparison: `SentinelStatefulSetHasChanged`
  (`internal/builder/sentinel.go`) and `StatefulSetHasChanged`
  (`internal/builder/statefulset.go:1008`) are line-for-line identical and both
  delegate to `podTemplateChanged`. So the difference is in what the two builders
  produce versus what the API server stores.
- The obvious defaulting traps are already handled: `containerChanged`,
  `volumeSourceChanged` and friends compare selected fields, not whole structs,
  so `DefaultMode`, `terminationMessagePath` and similar do not leak in.

**Root cause — found and verified 2026-08-19:
`TerminationGracePeriodSeconds`.**

- `buildSentinelPodSpec` (`internal/builder/sentinel.go`) never sets it, so the
  desired Sentinel pod spec carries `nil`. The API server defaults the stored
  object to `30`.
- `terminationGracePeriodEqual` (`internal/builder/statefulset.go`) returns
  `false` when exactly one side is nil, so `podSpecChanged` is **permanently**
  true for the Sentinel StatefulSet and `SentinelStatefulSetHasChanged` reports
  drift on every single pass.
- The data StatefulSet escapes it because `buildPodSpec` sets the field
  explicitly (`internal/builder/statefulset.go:521`,
  `spec.TerminationGracePeriodSeconds = &terminationGrace`), which is exactly the
  asymmetry the log showed.
- **Pre-existing, not from this branch:** `git show main:internal/builder/sentinel.go`
  mentions the field only in a comment, same as HEAD.

Consequence: one wasted StatefulSet write per reconcile per Sentinel-enabled CR.
The write itself is a storage no-op — it sends `nil` and the API server
re-defaults to the same `30` — which is consistent with the `generation=1`
sample taken during a burst, though that single sample was not on its own
sufficient to prove it. It therefore does **not** churn pods: no
`resourceVersion` bump, no watch event, no rolling update. Cost is API traffic
and log noise, not availability.

Fix (small): set `TerminationGracePeriodSeconds` explicitly in
`buildSentinelPodSpec`, mirroring `buildPodSpec`. Guard with a builder unit test
that a desired Sentinel pod spec compared against an API-server-defaulted copy
of itself reports no drift.

Done when: the field is set, `SentinelStatefulSetHasChanged` returns false for an
unchanged CR against a defaulted live object, and the update-per-create ratio in
an e2e log is ~1.

### NA12 — Operator Events are silently discarded: RBAC misses `events.k8s.io` — DONE

**Status (2026-08-20):** fixed. The marker
`// +kubebuilder:rbac:groups=events.k8s.io,resources=events,verbs=create;patch`
now sits next to the core-group events marker in
`internal/controller/valkey_controller.go`; `make generate-all` merged both
groups into one rule in `config/rbac/role.yaml`, and the Helm ClusterRole
(`deploy/helm/valkey-operator/templates/clusterrole.yaml`) carries the same
combined rule. The core-group rule is kept deliberately for older API servers
and tooling that still read it.

Verification:

- Two new subtests in `TestE2E_AdmissionRejection_StatefulSetNudgeRecovery`
  (`test/e2e/admission_recovery_test.go`): one polls for a `StatefulSetNudged`
  Event with `Regarding` = the Valkey CR, one greps the operator log for
  `Server rejected event` + `forbidden`. The log check is scoped to the test's
  own time window (`kubectl logs --since-time`, new helper `getPodLogsSince` in
  `test/e2e/tls_test.go`) — an unscoped first version failed on pre-fix history
  in the long-lived operator pod's log — and to forbidden-rejections, because a
  namespace being torn down by a parallel test can reject an Event create
  without any RBAC involvement.
- Green run against the local Kind cluster after `helm upgrade`: all 6 subtests
  pass; `kubectl get events -n e2e-admission-nudge` shows the nudge Event on
  `valkey/nudge-test` — the first operator Event ever visible on a CR.
- Mutation check: with `events.k8s.io` stripped from the live ClusterRole,
  exactly the two new subtests fail (the Event poll times out at 60 s; the log
  check catches the fresh `StatefulSetNudged` create **and** patch denials in
  the window) while all pre-existing subtests still pass. RBAC restored via
  `helm upgrade`, final run green.
- No operator code path changed; the fix is RBAC only. `make lint` clean.

Original observation follows.

Verified while executing NA1, unrelated to the nudge. **Every Event the operator
records is rejected by the API server.**

- `config/rbac/role.yaml` grants events on the legacy core group only:
  `apiGroups: [""]`, `resources: [events]`, `verbs: [create, patch]`. The Helm
  ClusterRole (`deploy/helm/valkey-operator/templates/clusterrole.yaml`, the
  "Events for status reporting" rule) has the identical gap.
- `recordEvent` (`internal/controller/rolling_update.go`) calls
  `r.Recorder.Eventf`, and `Recorder` comes from `k8s.io/client-go/tools/events`
  — the newer events API, which writes `events.k8s.io/v1`. No rule covers that
  group, so every write is denied.
- The captured operator log contains **47** occurrences of
  `ERROR events Server rejected event (will not retry!)` with
  `events.k8s.io is forbidden: User ...`.

Impact: `kubectl describe valkey` shows no operator events at all. That includes
`StatefulSetNudged` (`internal/controller/nudge.go`), i.e. the one signal that
would tell an operator during an incident that the recovery mechanism is
working — the feature runs blind. It also silently voids every other
`recordEvent` call in the codebase. Nothing fails loudly, which is why it went
unnoticed: the errors only appear in the operator's own log.

Fix: add the marker
`// +kubebuilder:rbac:groups=events.k8s.io,resources=events,verbs=create;patch`
next to the existing events marker in `internal/controller/valkey_controller.go`,
run `make generate-all`, and add the same rule to the Helm ClusterRole
(`deploy/helm/valkey-operator/templates/clusterrole.yaml`). Keep the core-group
rule: older API servers and some tooling still read it.

Done when: an e2e asserts that a `StatefulSetNudged` (or any operator) Event is
visible on the CR, and the operator log is free of `Server rejected event`.

### NA13 — T1's own subtests are a race and a coin flip — DONE

**Status (2026-08-20):** fixed, both edits landed.

- Subtest 1 (`test/e2e/admission_recovery_test.go`, "pod creation stays rejected
  and the CR leaves OK") now **polls** for the phase to leave `OK` (2 s interval,
  60 s budget) via the dynamic client instead of doing one unpolled
  `getValkeyStatus` read after the StatefulSet poll. The last observed phase is
  logged and quoted in the failure message, so a genuine "stays OK" failure is
  still distinguishable from a status that was never written.
- The claim that the recovery step is the regression guard is gone from all three
  places that carried it: the test header of
  `TestE2E_AdmissionRejection_StatefulSetNudgeRecovery`, T1 step 6 above, and
  WP1's "Done when". All three now point at
  `TestReconcileWorkload_RequeuesWhileShortOfPods` as the deterministic guard and
  describe the e2e step as a forward assertion that is deterministic only with
  the nudge in place.
- The `admissionBlockHold` comment no longer claims the hold makes a
  backoff-driven pass impossible; it states the measured coin flip instead.

Verified: `make lint` and `make test-unit` green. The e2e itself was not
re-executed as part of this change (needs a Kind cluster).

Two test-quality defects in `test/e2e/admission_recovery_test.go`, both exposed
by comparing the full run against the isolated runs. Neither is an operator bug;
both make T1 report the wrong thing.

**Subtest 1 races the operator.** `"pod creation stays rejected and the CR leaves OK"`
polls until the StatefulSet reports `status.replicas == 0` and then does a
**single, unpolled** read of the CR phase before asserting it is not `OK`. The
poll exits on the very watch event the operator also has to process, so the test
is asserting on a status the operator has not necessarily written yet. It passes
in 4.01 s isolated and failed at the identical 4.01 s under 34-way parallelism.
Fix: poll for the phase to leave `OK` instead of reading it once.

**Subtest 3 is nondeterministic against an unfixed operator.** See the correction
in NA1: PASS at 15.02 s in the full run, FAIL at 60.01 s isolated, same binary,
no nudge in either. With the fix it is deterministic (9.02 s, bounded by
grace + nudge interval), so it is a fine *forward* assertion — but it must not be
described as the regression guard for the nudge. The deterministic guard is the
unit test `TestReconcileWorkload_RequeuesWhileShortOfPods`.

Done when: subtest 1 polls for the phase transition, and the ticket/test comments
no longer claim subtest 3 alone guards the nudge.

**Timeline note (2026-08-20):** the second review initially found both edits
unlanded; they landed the same day in commit `8beaccf`. Verified in code
during the third review: subtest 1 polls the phase transition and logs the
last observed phase (`test/e2e/admission_recovery_test.go:245-259`), and the
test header calls the recovery step a forward assertion, pointing at
`TestReconcileWorkload_RequeuesWhileShortOfPods` as the deterministic guard
(lines 183-191). Confirmed green in the CI single-node leg (run 32359935550:
subtest "pod creation stays rejected and the CR leaves OK" 4.01 s).

## Follow-up work from the second implementation review (2026-08-20)

Review of the full branch after NA1-NA12 landed, focused on what the first
review missed. All findings verified against the source at the given
file:line; `make test-unit` and `make lint` were re-run green as part of the
review. Ordered by severity: NA14 should block the merge, NA15 is the largest
remaining incident-window, NA16-NA18 are small. NA14, NA15, NA16, NA17 and
NA18 are DONE. NA19 and NA20 were found while doing NA15; both are DONE. NA21 was
found by the e2e written for NA20 and is DONE.

### NA14 — PDB cleanup deletes and adopts foreign PodDisruptionBudgets (no ownership check) — DONE

**Status (2026-08-20):** fixed in `internal/controller/pdb.go`
(`cleanupPodDisruptionBudget`, `reconcilePodDisruptionBudget`, new
`warnPodDisruptionBudgetNotOwned`).

#### The fix

Both paths are guarded with `metav1.IsControlledBy(pdb, v)`:

- `cleanupPodDisruptionBudget` deletes only budgets this Valkey controls. A
  same-named object without that ownerReference is left alone.
- `reconcilePodDisruptionBudget` returns before `HasChanged`/`Update` when the
  existing object is not controlled by the CR, so a foreign budget is never
  adopted, never repointed at the operator selector, and never stamped with the
  operator-version annotation. `spec.podDisruptionBudget` then has no effect for
  that StatefulSet, which the Event says explicitly.

Both refusals go through one helper: a log line plus a Warning Event
`PodDisruptionBudgetNotOwned` (reason constant
`reasonPodDisruptionBudgetNotOwned`), naming the budget and the consequence
("the operator only deletes budgets it created" / "spec.podDisruptionBudget
cannot take effect until that budget is deleted or renamed"). As with NA6/NA7 it
fires on every applicable pass rather than on writes: the collision is a property
of the cluster, not of a transition, and the recorder aggregates the repeat into
one Event series.

Ownership is checked by ownerReference, not by a managed-by label: the label is
something a copied manifest can carry, the controller ownerReference is not.

**Deliberately not in scope**, as the ticket's own scope note says:
`cleanupMetricsService`, `cleanupObserverDeployment` and `cleanupServiceMonitor`
keep the delete-by-name pattern. Their names are operator-suffixed
(`-metrics`, `-observer`) and not names a hand-written object would carry.

#### Verification

- New unit tests in `internal/controller/pdb_test.go`:
  - `TestCleanupPodDisruptionBudget_KeepsForeignBudget` — table over both foreign
    shapes (no ownerReference at all; controlled by another object). Two cleanup
    passes with `spec.podDisruptionBudget` absent leave both budgets byte-intact
    (minAvailable, selector, foreign labels, no operator annotation) and warn on
    every pass for both names.
  - `TestCleanupPodDisruptionBudget_DeletesOwnedBudget` — positive control with a
    real UID on the CR: an owned budget is still deleted when the feature is
    switched off. Without it a UID-comparing guard could pass every foreign-object
    test while breaking cleanup outright.
  - `TestReconcilePodDisruptionBudget_DoesNotAdoptForeignBudget` — enabling the
    feature next to same-named foreign budgets neither rewrites them nor claims
    ownership, and warns for both.
  - `TestReconcilePodDisruptionBudget_NoWriteToForeignBudget` — the refusal costs
    zero API writes (`pdbWriteCounter` from the NA6 infrastructure stays at 0).
  - `haWithPDB` now sets a UID, so every existing PDB test compares real
    ownerReferences instead of two empty strings.
- Mutation check (unit): removing both guards fails exactly the four new tests and
  nothing else.
- New e2e `TestE2E_PodDisruptionBudget_LeavesForeignBudgetAlone`
  (`test/e2e/pdb_test.go`): a hand-written PDB under the data-budget name, a CR
  with no `podDisruptionBudget` block, then the block enabled. Both halves are
  asserted against a real API server — the unit tests observe the guard through a
  fake client that writes no UIDs and runs no garbage collection. The enable half
  waits for the `PodDisruptionBudgetNotOwned` Event first, so the assertion cannot
  pass merely because no reconcile happened yet.
- Mutation check (e2e, run against the live Kind cluster with an operator image
  built from the unguarded code): the cleanup subtest fails on its first poll
  (the budget is already gone) and the enable subtest times out waiting for the
  Event. With the fix all three PDB e2e tests pass, including
  `SerializesEvictions/budgets_are_removed_when_disabled` — the owned-deletion
  path is unaffected.
- `make lint` (0 issues), `make cyclo` (all < 15), `make gosec` (0 issues),
  `make test-unit`, `make test-integration` — all green; no existing test needed a
  change.
- Docs: the `Enabled` field doc in `api/v1/valkey_types.go` (regenerated into
  `config/crd/bases` and the Helm CRD template via `make manifests`), the PDB
  comment block in `deploy/helm/valkey-operator/values.yaml`, and the
  `spec.podDisruptionBudget` section of README.md now state that the operator
  touches only budgets it owns and what happens to a foreign one.
- Note for a follow-up, not fixed here: the Event poll in the new e2e
  (`waitForValkeyEvent`) duplicates the inline poll in
  `test/e2e/admission_recovery_test.go` (the "nudge Event is visible" subtest,
  lines 278-293). Originally deferred because NA13 had open edits in that file;
  those landed (`8beaccf`), so the refactor is unblocked — still worth doing
  only opportunistically, next time either test is touched.

#### The defect (as found)

**The defect.** `cleanupPodDisruptionBudget`
(`internal/controller/pdb.go:187-202`) fetches the PDB **by name only**
(`<cr-name>` for data, `<cr-name>-sentinel` for Sentinel) and deletes it
unconditionally — no ownerReference check, no managed-by label check. The
`PodDisruptionBudgets` reconcile step has no `when` predicate
(`internal/controller/valkey_controller.go:401`), so the cleanup path runs on
**every pass of every CR whose `spec.podDisruptionBudget` is absent or
disabled** — which is every pre-existing CR after the operator upgrade.

**Why the collision is the expected configuration, not an edge case.** The
opt-in design's own rationale (`internal/controller/pdb.go:26-28`) is that
users already manage their own PDBs. The natural name for a hand-created PDB
covering the data pods is the StatefulSet name — `oauth2-valkey` — and
hand-creating exactly that PDB was the obvious F3 remediation before WP4
existed. Every such PDB is silently deleted, at the latest on the reconcile
after an operator restart. A user PDB carries no ownerReference, so its
deletion also triggers no `Owns()` event — it just disappears, and recreating
it lasts until the next pass.

**The mirror image in the update path.** `reconcilePodDisruptionBudget`
(`internal/controller/pdb.go:147-181`) adopts a same-named foreign PDB
silently when the user later sets `enabled: true`: Get → `HasChanged` →
Update overwrites budget fields and selector without ever checking who owns
the object.

**Fix.** Guard both paths with `metav1.IsControlledBy(pdb, v)`: cleanup
deletes only operator-owned budgets; the update path warns (log + Event) and
leaves a foreign object untouched instead of adopting it. Unit tests: a
foreign PDB named like the data budget survives cleanup passes; a foreign PDB
is not overwritten on enable; both warn.

**Scope note.** `cleanupMetricsService`, `cleanupObserverDeployment` and
`cleanupServiceMonitor` share the delete-by-name pattern (repo convention),
but their names are operator-suffixed (`-metrics`, `-observer`) and far less
collision-prone. Fixing them is a consistency follow-up, not part of NA14's
severity.

### NA15 — Data rolling update + blocked pod creation: the NA4 gap persists for the data StatefulSet — DONE

**Status (2026-08-20):** fixed in `internal/controller/nudge.go`. The data-RU
suppression branch is deleted; both StatefulSets are now nudged unconditionally.

#### The fix

`nudgeShortStatefulSets` lost its conditional entirely:

```go
short := r.nudgeStatefulSet(ctx, v, dataKey)
if v.IsSentinelEnabled() && r.nudgeStatefulSet(ctx, v, sentinelKey) {
    short = true
}
return short
```

**Why option (A) "delete the branch" and not (B) "narrow it to RU states in which
no pod is expected to be missing", which this ticket also offered:** (B) is
unimplementable as specified, because the RU state annotation is a **phase
marker, not a liveness marker**. It is set once before the delete and cleared
when the *phase* ends, not when the pod comes back — and the phase cannot end
while the pod is missing, because `replaceNextReplica` returns at
`if !ps.exists { ... }` (`internal/controller/rolling_update.go:1010-1012`) on
every pass. So suppressing in the delete-states
(`replacing-replicas`, `replacing-master`, `manual-failover`) suppresses the
nudge for the **entire duration of the stall** — the NA15 incident, unfixed. The
literal inverse reading (suppress in the states where no pod is expected missing:
`failover-triggered`, `failover-reset`, `restoring-topology`,
`verifying-topology`) is worse: those are the states where an absent pod is
*unintentional* and nothing else recovers it — `waitForReplicasReady`
(`rolling_update.go:1288-1292`) requires every non-master pod `ready &&
!needsUpdate`, so a missing pod blocks the state forever. The state set that
actually fixes NA15 is the empty set.

**The discriminator that does work is duration, and it already exists.**
`nudgeGracePeriod` (10 s) requires the short state to survive across passes, and
`status.replicas` counts *created* pods — so it recovers at pod creation, not at
Ready. A healthy recreation is back in ~1-3 s and never reaches the grace period;
in the healthy rolling update this change therefore writes nothing at all. The
nudge fires only when recreation is genuinely stuck, which is the one condition
every RU state is blocked on.

**Verified per state** (all seven, `rolling_update.go:38-46`): nowhere does the
data path depend on a pod staying gone for any window. All five data-pod delete
sites (`:1037`, `:1413`, `:2073`, `:2223`, `:2275`) are immediately followed by a
requeue whose next step waits for the recreation. In four states no delete
happens at all and suppression was **actively harmful**.

**The suppression was never total anyway.** `handleStandaloneRollingUpdate`
(`rolling_update.go:2032-2077`) deletes at `:2073` without ever calling
`setRollingUpdateState`, and `deleteNextPendingPod` (`:2216`) deletes with
whatever state it inherited, which can be `""`. The data nudge has been live
across those deletes since WP1 shipped, with no reported harm.

**`forget` semantics.** The deleted `else` branch called `r.nudges.forget(dataKey)`
on every RU pass, which is what made the suppression total rather than merely
delayed: `observe` always returned `now`, so the grace period could never be
crossed however long the RU ran. Both remaining `forget` sites are inside
`nudgeStatefulSet` (Get failure, `Status.Replicas >= desired`) plus `forgetNudges`
on CR deletion — identical to the Sentinel key, so the two StatefulSets are now
symmetric. Side effect, strictly better: a grace-period observation now carries
across the RU boundary instead of restarting the 10 s clock.

**Why nudging across a self-inflicted delete is safe** (each point verified, not
assumed):

- Under `OnDelete` + `Parallel` (`internal/builder/statefulset.go:120,125-127`)
  creating a missing ordinal is unconditional, so a nudge can only accelerate a
  recreation the statefulset-controller already owes. Ordinal naming makes a
  duplicate impossible.
- The desired pod template is written **before** every delete in the same pass:
  `reconcileResources` runs its StatefulSet step (`reconcileStatefulSet`,
  `valkey_controller.go:851-857`) before `reconcileWorkload`, and outdated pods
  are the RU's trigger *because* the template is already new.
- The annotation lives on StatefulSet **object** metadata
  (`builder.NudgePatch`, `internal/builder/annotations.go:90-93`), never on
  `spec.template`. `StatefulSetHasChanged` (`statefulset.go:1008-1015`) compares
  only `Spec.Replicas` and `Spec.Template`, and `ComputePodSpecHash` hashes a bare
  `corev1.PodSpec` with no ObjectMeta — no drift verdict, no hash change, no
  feedback loop. `reconcileStatefulSet` assigns only `Spec.Replicas`,
  `Spec.Template` and `Labels` onto the live-read object, so an operator write
  never drops the annotation either.
- Rate is capped at one patch per 20 s per StatefulSet by `NudgeDue`, whose state
  is the annotation itself. Worst case with a pod that ignores SIGTERM for the
  full 75 s grace: 4 patches for that pod.
- The rolling update keeps requeue authority: `reconcileWorkload` returns the RU
  result (`valkey_controller.go:276-278`) before it reads `shortOfPods` at the end
  of the function, so the 5 s nudge clock never preempts the 10 s RU wait.

#### Verification

- New/rewritten unit tests in `internal/controller/nudge_test.go`:
  - `TestReconcileWorkload_NudgesDataStatefulSetWhenRecreationBlocked` — the NA15
    constellation via the new `dataRecreationBlockedFixture` (data RU in progress,
    pod-2 deleted and not recreated, pod-0/pod-1 outdated and ready, Sentinel STS
    complete so the data STS is the only source of `short`). Asserts the pass really
    ends in the RU wait (`RequeueAfter == rollingUpdateRequeueDelay`) **and** that
    the data StatefulSet is nudged anyway.
  - `TestReconcileWorkload_RollingUpdateKeepsRequeueAuthority` — the other half:
    the nudge fires and the RU still owns the clock. This ordering
    (`:276` before `:304`) was load-bearing but untested before NA15; without the
    guard a later refactor hoisting the `shortOfPods` check would silently swap the
    RU's 10 s for the nudge's 5 s.
  - `TestNudgeShortStatefulSets_NudgesDataStatefulSetDuringRollingUpdate` (was
    `..._NoNudgeDuringRollingUpdate`) — inverted, and additionally asserts the
    tracker keeps accumulating, which is the half that dies with the `else` branch.
  - `TestNudgeShortStatefulSets_ReportsShortDuringRollingUpdate` (was
    `..._NoRequeueSignalDuringRollingUpdate`) — the short state is now reported.
  - `TestNudgeShortStatefulSets_NudgesBothDuringDataRollingUpdate` (was
    `..._NudgesSentinelDuringDataRollingUpdate`) — data-half assertion flipped, the
    Sentinel assertion is unchanged.
- Mutation check 1 (suppression restored in an isolated copy of the tree): exactly
  the five tests above fail, nothing else. Mutation check 2 (the `shortOfPods`
  return hoisted above the RU checks in `reconcileWorkload`): both
  `reconcileWorkload`-level tests fail on the requeue value.
- Blast radius measured before the change by removing the branch in a copy and
  running the full unit suite: exactly 3 tests / 4 assertions failed, all in
  `nudge_test.go`; every other package stayed green.
- `make test-unit`, `make test-integration`, `make lint` (0 issues), `make cyclo`
  (all < 15, `nudgeShortStatefulSets` drops from 5 to 3), `make gosec` (0 issues) —
  all green.
- No e2e added: the change is a single-pass property that the two
  `reconcileWorkload` tests observe directly and deterministically. The existing
  `TestE2E_AdmissionRejection_StatefulSetNudgeRecovery` is unaffected — it deletes
  data pods without an image change, so no RU state is ever set and the suppression
  was never on its path.
- Docs corrected in this file rather than in user-facing docs: `grep -i nudge`
  over `README.md`, `CLAUDE.md`, `deploy/`, `config/` and `api/` returns nothing,
  so no CRD/Helm/README statement was affected.

#### The defect (as found)

**The defect.** `nudgeShortStatefulSets` suppressed the **data** nudge while
`vko.gtrfc.com/rolling-update-state` was set (`internal/controller/nudge.go:126-129`
at the time). But the data rolling update deletes a pod and then waits for the
statefulset-controller to recreate it (`internal/controller/rolling_update.go:1011-1013`,
`updateStrategy: OnDelete`). If pod creation is admission-blocked in that window, the
recreation hangs on the statefulset-controller's exponential backoff after the webhook
heals — the 5 min 29 s tail, mid-rolling-update, with the nudge suppressed in exactly
that constellation. The RU's own 10 s requeue keeps the operator awake but never wakes
the statefulset-controller.

**Why NA4's own argument applies here unchanged.** NA4 removed the Sentinel-RU
suppression because "that path deletes one pod and then waits for that exact pod to
come back — suppressing the nudge there suppresses it precisely where it is the only
lever", and because the bump is harmless under OnDelete (metadata-only annotation,
invisible to drift detection, the resync recreates the pod from the current template —
the RU's own next step). Every word of that holds for the data RU. The remaining
suppression was unexplained asymmetry, not a documented decision.

**Weight.** NA9 established that every operator release rolls every multi-replica data
StatefulSet (sidecar image). Data RUs are therefore routine fleet-wide events, and
maintenance windows pair them with node drains — the incident's exact trigger for
webhook gaps.

#### Found while doing NA15, not fixed here

- **NA19 (new, below, now DONE):** an image or config change whose StatefulSet write is
  admission-blocked deletes pods that the statefulset-controller recreates from the
  still-old template. Pre-existing, independent of the nudge.
- **NA20 (new, below, now DONE):** a 2-replica non-Sentinel cluster can boot a
  returning pod-0 as an independent master via the init-container role election.
  Timing-independent, so not caused or widened by this change.

### NA16 — nudgeStatefulSet drops the requeue signal on a transient Get error — DONE

**Status (2026-08-20):** fixed in `internal/controller/nudge.go`
(`nudgeStatefulSet`).

#### The finding

Any Get error — not only NotFound — forgot the grace-period observation and
returned false. In `Provisioning` the nudge requeue is the only wakeup source
(the NA1 dormancy), so one transient error both reset the grace period and ended
the requeue chain until an unrelated event arrived. Cache-backed Gets practically
never fail, hence low severity — but the failure landed exactly on the path NA1
exists for.

#### The fix

The error branch is split on `apierrors.IsNotFound`:

- NotFound keeps the previous behavior — the StatefulSet is genuinely gone, so
  the observation is dropped and the caller is told nothing is short.
- Any other error keeps the observation and returns true. Unknown is not the same
  as recovered: the grace period does not restart, and the caller keeps
  requeueing, which is the only thing that can resolve the unknown. The error is
  logged, consistent with the rest of the function swallowing nudge failures.

Two unit tests in `internal/controller/nudge_test.go` pin both halves —
`TestNudgeShortStatefulSets_TransientGetErrorKeepsRequeue` (return value) and
`TestNudgeShortStatefulSets_TransientGetErrorKeepsObservation` (tracker state) —
using an interceptor that fails only the data StatefulSet read with a
non-NotFound error. `TestNudgeShortStatefulSets_MissingStatefulSetIsNoOp` remains
the guard for the NotFound path.

### NA17 — ReconcileBlocked and SidecarUpdatePending conditions never set ObservedGeneration — DONE

**Status (2026-08-20):** fixed in `internal/controller/valkey_controller.go`
(`setStatusCondition`) and `internal/controller/reconcile_blocked.go`
(`setReconcileBlockedCondition`).

#### The finding

`setStatusCondition` built its conditions without `ObservedGeneration`, while
every Ready condition sets it (`updateStandaloneStatus`/`updateHAStatus`).
Tooling that judges condition staleness by observedGeneration (kstatus-style)
read `ReconcileBlocked`, `SidecarUpdatePending` and `RollingUpdatePaused` as
generation 0 — never matching the live spec, therefore permanently stale.

#### The fix

Two parts, because the first alone would have been inert on the path that
matters most:

1. `setStatusCondition` now stamps `ObservedGeneration: v.Generation` from the
   **refreshed** object, not from the caller's copy. The refresh Get is what the
   condition describes, so a pass that started before a spec edit still writes a
   condition naming the generation it actually read.

2. `setReconcileBlockedCondition` compares `ObservedGeneration` in its
   skip-if-unchanged guard, in both branches. A cluster that stays blocked across
   a spec edit reports the same reason and message for the new generation; without
   the comparison the write was skipped and the condition kept naming the old
   generation — readable as "the new spec was never evaluated", the exact opposite
   of what happened. Same for the cleared branch: a new generation that reconciles
   cleanly must say so. Cost is one status write per generation change, not per
   pass; the per-pass suppression is untouched because the generation only moves
   on a spec edit.

Tests live in the new `internal/controller/condition_generation_test.go`. Six of
them fail without the fix (verified by stashing the two source files and
re-running):
`TestSetStatusCondition_CarriesObservedGeneration`,
`TestSetStatusCondition_UsesRefreshedGeneration` (stale caller copy vs. bumped
stored generation), `TestSetSidecarUpdatePendingCondition_CarriesObservedGeneration`
(set and cleared branch), `TestSetReconcileBlockedCondition_CarriesObservedGeneration`,
`..._RefreshesObservedGenerationOnNewSpec` and
`..._ClearedRefreshesObservedGenerationOnNewSpec`. Two more are regression guards
that pass either way and pin what the new comparison must **not** break:
`..._NoWriteWhenGenerationUnchanged` (no status write per pass) and
`..._MessageChangeStillWrites` (the generation check was added to the guard, not
substituted for the message check).

The fake client does not maintain `metadata.generation` on its own, so the tests
set and bump it explicitly through the `bumpGeneration` helper.

### NA18 — E2E webhook remover gives up after one failed delete — DONE

**Status (2026-08-20):** fixed in `test/e2e/admission_recovery_test.go`
(`blockResourceOperations`).

`blockResourceOperations` set `removed = true` **before** attempting the delete,
so a transient API error on the first removal made the deferred second call a
no-op and left the cluster-scoped `MutatingWebhookConfiguration` behind (inert —
its namespaceSelector points at a deleted namespace — but litter that the next
run of the same test has to clear).

The flag now moves below the delete: it is set only on success or `NotFound`, so
the error path returns with `removed` still false and the deferred call retries.
Test-only hygiene, no operator behavior change. `make lint` green.

## Found while implementing NA15 (2026-08-20)

Both surfaced while verifying that nudging across a self-inflicted pod delete is
safe. Neither is caused or worsened by NA15; both are pre-existing and were
deliberately left out of that change rather than folded into it.

### NA19 — A blocked StatefulSet write turns an image change into a pod-delete loop — DONE

`reconcileResources` deliberately does not abort the pass when one sub-resource
write fails (the aggregate-reconcile design, `valkey_controller.go:222-231`), so
`reconcileWorkload` runs even when the StatefulSet `Update` was rejected. In that
window `checkAndHandleRollingUpdate` uses a **split** notion of "desired":

| Input | Source | State when the STS write is blocked |
|---|---|---|
| `desiredImage` (`rolling_update.go:145`) | the CR | new |
| `desiredConfigHash` (`:147`) | the CR | new |
| `sidecarImg` (`:146`) | the live STS | old |
| `desiredPodSpecHash` (`:148`) | the live STS | old |

Pod-spec-only changes are therefore self-protecting — they read the StatefulSet
and see no drift. But an **image or config change with a rejected StatefulSet
write** makes `podNeedsUpdate` true against a template that was never written:
the operator deletes the pod, the statefulset-controller recreates it from the
still-old template, the pod comes back outdated, and the cycle repeats at the
10 s `rollingUpdateRequeueDelay`. Data loss is bounded by the RU's own
one-pod-at-a-time gating (each delete is behind `ps.ready` and the sync check),
but the cluster churns for as long as the admission gate is closed.

#### The fix (2026-08-20)

The split is gone: all four inputs now come from the live StatefulSet.
`internal/controller/rolling_update.go` gained two helpers next to the existing
`sidecarImageFromSts`/`podSpecHashFromSts` —

- `valkeyImageFromSts(sts)` — the `valkey` container image from the persisted
  pod template, empty when the container is absent.
- `configHashFromSts(sts)` — the `vko.gtrfc.com/config-hash` annotation from the
  persisted pod template, empty when absent.

and every rolling-update site was repointed at them:

| Site | Was | Now |
|---|---|---|
| `checkAndHandleRollingUpdate` | `v.Spec.Image`, `builder.ComputeConfigHash(v)` | `valkeyImageFromSts`, `configHashFromSts` |
| `collectPodStates` (multi-replica) | same | same |
| `handleStandaloneRollingUpdate` | same | same |
| `handlePostManualFailover` | `v.Spec.Image` | `valkeyImageFromSts` |

`handlePostManualFailover` took a `currentSts` parameter for this; both callers
already had it in scope. Its image comparison also gained an `!= ""` guard so an
absent Valkey container degrades to "cannot tell", matching how
`podImageChanged`/`podAnnotationHashChanged` already treat an empty desired
value. Empty everywhere means "skip the check", so the degradation is always
towards *not* replacing pods.

The invariant is now uniform and stateable in one line: **a rolling update
compares pods against the template the statefulset-controller will actually
recreate them from.** Nothing else can be a correct comparison target, because
nothing else is what a recreated pod gets.

**The behavior change on the normal path is free**, which is why the "needs its
own test matrix" concern shrank. The operator watches `Owns(&appsv1.StatefulSet{})`
(`valkey_controller.go:1770`), so the successful `Update` in `reconcileStatefulSet`
enqueues the very reconcile that then sees the new template. There is no added
latency beyond one watch event: previously the rollout started in the same pass
that wrote the template, now it starts in the pass the write triggers.

**Diagnosability, previously "not verified":** answered by the fix rather than
measured. While the write is rejected the CR carries `ReconcileBlocked=True`
with reason `AdmissionWebhookDenied` and phase `Error` (NA1/NA17), and the pods
now simply stay put — there is no longer a churn symptom to diagnose.

#### Verification

- New `internal/controller/rolling_update_blocked_write_test.go`:
  - `TestCheckAndHandleRollingUpdate_NoUpdateWhenImageWriteBlocked` — CR at 9.0,
    persisted template at 8.0, pod matching the template: no requeue, pod alive.
  - `TestCheckAndHandleRollingUpdate_NoUpdateWhenConfigWriteBlocked` — same for
    the config hash (auth enabled on the CR only). The fixture asserts up front
    that the two hashes actually differ, so the test cannot pass vacuously.
  - `TestCheckAndHandleRollingUpdate_StartsOnceTemplatePersisted` — positive
    control. Without it a helper that always reported "no drift" would satisfy
    every negative test while disabling rolling updates outright.
  - `TestCollectPodStates_IgnoresUnpersistedImageChange` — the multi-replica path
    reaches `podNeedsUpdate` through `collectPodStates`, not through the gate in
    `checkAndHandleRollingUpdate`, so it needs its own guard.
  - `TestReconcile_BlockedStatefulSetWriteDoesNotDeletePods` — the full pass, in
    the incident's shape: an interceptor rejects every `Update` of the data
    StatefulSet with `webhookUnreachableError()`, then three reconciles run (as
    the 10 s requeue would produce) and the pod must survive all three. This is
    the test that reproduces the *loop* rather than a single wrong decision.
  - Two helpers, `stsForValkey` (the template the operator would persist) and
    `podFromStsTemplate` (the pod the statefulset-controller would create from
    it, annotations included), keep "pod matches persisted template" a property
    of the fixture rather than a hand-copied constant.
- Mutation check: reverting the four call sites to `v.Spec.Image` /
  `builder.ComputeConfigHash(v)` fails exactly the four negative tests and leaves
  the positive control green — the expected signature, since the control asserts
  behavior both versions share.
- `make test-unit`, `make test-integration`, `make lint` (0 issues), `make cyclo`
  (all < 15), `make gosec` (0 issues) — all green. No existing test needed a
  change, including
  `TestCheckAndHandleRollingUpdate_StandaloneImageChange`, which drives a full
  Reconcile and therefore has the template persisted before the workload pass
  looks at it.

Not covered: no e2e was added. Reproducing this one needs a webhook that rejects
StatefulSet updates while leaving pods writable, which is a different blocking
shape than the existing `blockResourceOperations` helper installs. The unit-level
reconcile test covers the loop; an e2e would only add API-server fidelity to a
decision that reads no server state beyond the two objects the unit test
provides.

### NA20 — 2-replica non-Sentinel cluster can boot a returning pod-0 as an independent master — DONE

**Status (2026-08-20):** fixed in `internal/builder/statefulset.go` (non-Sentinel
init container) and `internal/controller/rolling_update.go`
(`handleManualFailover`, `promotePod0AndRedirect`).

#### The finding

The `init-config-selector` init container elects the pod's role from live cluster
state; if peers respond but none reports `role:master` with `connected_slaves > 0`,
it falls back to ordinal-based config and pod-0 boots as its own master. With
2 replicas and no Sentinel, `handleManualFailover` promotes pod-1 and deletes
pod-0, and the promoted pod-1 has zero connected slaves at that moment — so a
returning pod-0 can take the fallback branch and split the topology.

Timing-independent: it fires whether the pod comes back in 2 s or in 5 min, which
is why NA15 does not widen it. The Sentinel path is not affected
(`verifyNewMasterReady` gates the master delete on `ConnectedSlaves > 0`,
`rolling_update.go:1683-1692`), and neither is any cluster with 3+ replicas, where
the promoted master has a surviving slave.

**Verified:** the topology is reachable in a supported configuration.
`spec.replicas` carries only `+kubebuilder:validation:Minimum=1`
(`api/v1/valkey_types.go:577-581`) with no CEL rule tying it to Sentinel, and
`IsMultiReplicaWithoutSentinel()` (`:712-714`) is true for `replicas: 2` without
`spec.sentinel`. The fallback branch is in the init script at
`internal/builder/statefulset.go:415-449`: acceptance requires
`ROLE = master && SLAVES > 0`, and once any peer responds the discovery loop
breaks rather than waiting, so ordinal 0 gets the master config.

**Verified, and it bounds the severity:** the operator repairs this rather than
leaving it. `handlePostManualFailover` sends `REPLICAOF <promoted>` to pod-0 once
it is back and ready (`rolling_update.go:2403-2409`), then
`stateVerifyingTopology` runs `detectAndResolveSplitBrain` and retries `REPLICAOF`
on any remaining rogue master. So the split is a window, not a permanent state.
What is *not* repaired is data: writes that reached the independent pod-0 during
the window are discarded when it starts syncing from pod-1. The exposure requires
a client writing to pod-0 directly or through the `-rw` Service in that window.

Never measured, and no longer decision-relevant: how wide the window is in
practice, and whether the `-rw` Service actually routes to pod-0 while it
believes itself master (that depends on the `instanceRole` label being updated,
which the operator does on its own schedule). The measurement was originally
meant to decide between a documentation note and a real fix; the real fix was
built without it (below), so the open datum only bounds how bad the pre-fix
exposure was — not what to do about it.

#### The fix

The pod cannot decide this from peer state alone — a promoted master with zero
replicas and a rogue master with zero replicas look identical over `INFO
replication`. The operator does know: it performed the promotion. So the fix
carries the operator's answer to the pod through the channel that already exists
for the Sentinel path, the `replicaof` directive of the replica ConfigMap.

Controller (`internal/controller/rolling_update.go`):

- `handleManualFailover` records the promoted pod in `vko.gtrfc.com/known-master`
  in the same `Update` that already writes `promoted-pod` and the state
  annotation, then calls `reconcileReplicaConfigMap` **before** deleting the old
  master. Ordering is the whole point: a pod mounts the ConfigMap as it exists
  when the kubelet starts it, so a write that lands after the delete may or may
  not reach the recreated pod.
- The ConfigMap write is best-effort. A failure is logged and raised as a
  `KnownMasterPublishFailed` warning event, and the delete proceeds — blocking it
  would leave the CR in `manualFailover` with an undeleted old master and stall
  the rolling update indefinitely, which is worse than the window this fixes.
- `promotePod0AndRedirect` points the annotation back at pod-0 right after
  `REPLICAOF NO ONE` succeeds and republishes the ConfigMap. Without that reset
  the replica config would keep naming a pod that is being demoted in the same
  pass, and the next replica restart would replicate from it.

Init container (`internal/builder/statefulset.go`, non-Sentinel branch): a new
Phase 2 between peer discovery and the ordinal fallback reads the `replicaof`
target out of the replica config mount and adopts it — but only when it is not
this pod itself and only when that peer answers `INFO replication` with
`role:master`. Both guards matter:

- The self guard is what makes the default configuration a no-op. Outside a
  failover the replica config names pod-0, so pod-0 sees itself, skips, and takes
  the master config exactly as before.
- The `role:master` probe is what makes a stale annotation harmless. If the
  recorded pod is gone or has been demoted, the step declines and the ordinal
  fallback runs — the previous behavior.

Phase 1 is untouched, so an established master with connected replicas still
wins over the recorded address.

The annotation does not enter the config hash (`ComputeConfigHash` uses
`GenerateValkeyConfForHash`, which ignores the override), so publishing it during
a failover cannot mark every pod outdated.

#### Verification

- New `internal/builder/init_script_exec_test.go` **executes** the generated
  shell script instead of asserting on its text: the three config mounts are
  redirected to a `t.TempDir()`, and stub `valkey-cli` / `timeout` binaries on
  PATH answer `INFO replication` from a per-host table. A text assertion would
  only prove a line exists, not that its branch is taken.
  - `TestInitScript_ReturningPod0FollowsKnownMaster` — the NA20 shape: pod-0
    returns, pod-1 is a master with zero replicas, the replica config names
    pod-1. The produced config must carry `replicaof <pod-1>`.
  - `TestInitScript_Pod0IgnoresSelfAsKnownMaster` — default config, pod-0 must
    not replicate from itself.
  - `TestInitScript_StaleKnownMasterFallsBackToOrdinal` — recorded pod
    unreachable, ordinal fallback, no `replicaof`.
  - `TestInitScript_EstablishedMasterWinsOverKnownMaster` — Phase 1 still has
    priority over the new step.
  - `TestInitScript_ReplicaFollowsKnownMaster` — the step is not pod-0-specific.
  - `TestInitScript_AnnouncesOwnHostname` — the announce directives survive the
    new branch.
- New `internal/controller/manual_failover_known_master_test.go`:
  - `TestHandleManualFailover_PublishesKnownMasterBeforeDeletingOldMaster` — a
    `Delete` interceptor captures the replica ConfigMap **at the moment** pod-0
    is deleted and requires it to already name pod-1. A fake RESP server makes
    `REPLICAOF`/`WAIT` succeed so the failover path runs to completion.
  - `TestPromotePod0AndRedirect_ResetsKnownMasterToPod0` — the pointer follows
    the topology back.
  - `TestKnownMasterAnnotation_DoesNotChangeConfigHash` — pins the hash
    neutrality the fix depends on.
- Mutation checks: neutralizing the init-script guard
  (`if [ -n "$KNOWN_MASTER" ] ...` → `if false`) fails exactly the two tests that
  depend on the new branch and leaves the four control tests green — pod-0 falls
  back to the bare master config, which is the pre-fix behavior verbatim.
  Removing the `reconcileReplicaConfigMap` call from `handleManualFailover` fails
  the ordering test with the ConfigMap still naming pod-0.
- New `test/e2e/two_replica_failover_test.go`
  (`TestE2E_RollingUpdate_TwoReplicasNoSentinel`): a real 2-replica cluster
  without Sentinel goes through an image rolling update; the assertion reads the
  `init-config-selector` log of the recreated pod-0 and requires that it did not
  take the ordinal branch. Asserting on the decision log rather than racing the
  split window makes the test deterministic — the window is short and the
  operator repairs it, so polling for two masters would be both flaky and unable
  to distinguish the split from the legitimate promote/demote moments.

  Its first run failed, and that failure was worth more than the test: pod-0 read
  the published address and *declined* it, because pod-1 answered `role:slave`.
  The operator had demoted its own promoted pod seconds earlier — NA21 below. The
  run also lost the pre-update key, so the two findings share one symptom. After
  the NA21 fix the log reads

  ```
  Queried 1 peers but no master with connected replicas found
  Using known master from replica config: roll-2r-1.roll-2r-headless....
  This pod is a replica, discovered master=roll-2r-1.roll-2r-headless....
  ```

  and all five sub-tests pass, including the data check.
- `make test-unit`, `make test-integration`, `make lint` (0 issues), `make cyclo`
  (all < 15), `make gosec` (0 issues) — green. No existing test needed a change.

#### What this does not fix

**Upgrade consequence, found by the fourth review (NA25):** the new init-script
phase is inline in the pod spec and covered by `ComputePodSpecHash`, so the
first reconcile after the operator upgrade rolls every multi-replica
non-Sentinel cluster once — on every install path, through the manual-failover
cycle this very change modifies. (NA25, closed without a release note: the
rotation is the norm on the canonical install path.)

The exposure is closed for the failover window the operator itself creates. Two
neighbouring cases stay as they were, both pre-existing and out of NA20's scope:

- If both pods are down and come back together, pod-0 starts first, finds no
  peer, and takes the ordinal fallback as master — even when pod-1 held the
  post-failover data. The known-master step cannot help, because there is no
  reachable peer to confirm.
- If the replica ConfigMap write is rejected (the admission gap this document is
  about), the returning pod-0 sees the stale pod-0 default and the original NA20
  behavior returns. The warning event names it; the `ReconcileBlocked` condition
  from NA1/NA17 explains why.

### NA21 — split-brain resolver demotes the pod it just promoted, losing the data — DONE

**Status (2026-08-20):** found by the e2e written for NA20, fixed in
`internal/controller/rolling_update.go` (`handleMultiReplicaRollingUpdate`,
`detectAndResolveSplitBrain`).

#### The finding

`handleMultiReplicaRollingUpdate` runs `detectAndResolveSplitBrain` at the top of
every pass, with an empty authoritative-master name. During a manual failover two
pods report master by design: the promoted pod (`REPLICAOF NO ONE`) and the old
master, which is deleted moments later but still answers until it terminates. A
reconcile that lands in that window sees "2 masters" and resolves it.

The resolution prefers the master with the most connected slaves. With **two**
replicas neither has one at that instant — the promoted pod's only peer is the
old master being deleted — so `bestSlaves` never improves on the initial
`bestIdx = masterIndices[0]` and the tie goes to the lowest ordinal: pod-0, the
pod the operator itself just deleted. The promoted pod-1 is then demoted with
`REPLICAOF <pod-0>`, pointing the only surviving copy of the data at a
disappearing pod.

Observed in the first e2e run (operator log, `e2e-rolling-two-replicas`, all
within one second):

```
Promoted replica to temporary master        pod=roll-2r-1
Deleting old master pod after manual failover  pod=roll-2r-0
Split-brain detected: multiple masters found   masterCount=2 masterIndices=[0 1]
Split-brain resolution: identified real master realMaster=roll-2r-0 rogueCount=1
Successfully demoted rogue master              roguePod=roll-2r-1 realMaster=roll-2r-0
```

The key written before the update was gone at the end of that run. Three
replicas hide the bug: `promoteAndRedirect` attaches the third pod to the
promoted one, so it wins the connected-slaves comparison outright — which is why
`TestE2E_RollingUpdate_MultiReplicaNoSentinel` has been green all along.

Independent of NA20 in cause, but they compound: NA20 leaves pod-0 electing
itself, NA21 makes pod-1 a replica of the dead pod-0, and the NA20 fix cannot
even take effect while NA21 is present, because its guard correctly refuses a
peer that answers `role:slave`.

#### The fix

`detectAndResolveSplitBrain` already takes the name of a pod some authority has
designated as master — it was only ever fed from Sentinel, and the two
non-Sentinel call sites passed `""`. The parameter is renamed `knownMaster` and
`handleMultiReplicaRollingUpdate` now fills it with `annotationPromotedPod` while
the state is `manualFailover` or `replacingMaster`. The promoted pod is then
identified as the real master and the terminating old master is the one demoted
— which is the intended end state anyway.

The state gate is load-bearing: the annotation lives on through
`restoringTopology` and `verifyingTopology`, where pod-0 is the intended master
again. Passing the promoted pod there would invert the restoration the operator
just performed.

#### Verification

- `TestDetectAndResolveSplitBrain_PrefersPromotedPodDuringFailover` — both pods
  report master with zero connected slaves, the exact tie the fallback loses; the
  promoted pod must come out as the real master and pod-0 as the demoted one.
- `TestHandleMultiReplicaRollingUpdate_DoesNotDemotePromotedPod` — the same case
  through the caller: no Valkey client may be opened against the promoted pod's
  address while the failover state is set.
- Mutation check: forcing `preferredMaster = ""` fails the second test with
  `"test-1.test-headless.default.svc.cluster.local:6379" should not contain
  "test-1."` — the demotion the fix prevents.
- `TestE2E_RollingUpdate_TwoReplicasNoSentinel` passes end to end after the fix,
  including "Data survives the update", which failed before it.

## Follow-up work from the third review (2026-08-20)

Review of the finished branch (HEAD `9aa307c`, NA1-NA21 all landed) against
this ticket: ticket-reality drift, hidden shortcuts, anything open before the
merge. Verified in this pass:

- **CI is fully green at HEAD.** Run 32365884642 (@ `9aa307c`) completed
  success on every job, including both e2e legs and the `e2e-gate` aggregator.
  It is the first CI execution of `TestE2E_RollingUpdate_TwoReplicasNoSentinel`
  — PASS at 97.26 s with all five subtests, including "Data survives the
  update" (NA21's proof) and "Returning pod-0 joins the promoted master
  instead of electing itself" (NA20's proof). The previous run 32359935550
  (@ `519eaa4`) was already green on both legs: multi-node with all
  anti-affinity and PDB tests (incl. hard spread on three distinct nodes and
  both first-attempt eviction refusals), single-node with the full suite incl.
  all three admission scenarios.
- **NA13's edits are in the code** (commit `8beaccf`): the poll at
  `test/e2e/admission_recovery_test.go:245-259`, the forward-assertion header
  at lines 183-191.
- **The nudge annotation survives operator StatefulSet updates:**
  `reconcileStatefulSet` assigns only `Spec.Replicas`, `Spec.Template`,
  `Labels` and the operator-version annotation onto the live object
  (`internal/controller/valkey_controller.go:854-861`), and
  `ApplyOperatorVersion` merges into the existing annotation map.
- **`NudgeDue` is robust against a hand-edited annotation:** absent,
  unparsable and future timestamps all count as due
  (`internal/builder/annotations.go:75-85`), so a bad value can delay a nudge
  by at most one interval, never block it forever.
- **No hidden shortcuts found.** The three deliberate tradeoffs stand as
  documented: message-based admission classification (brittle across K8s
  versions, but misclassification only degrades the condition reason to
  `WriteFailed`), best-effort known-master publish (NA20's "what this does not
  fix"), unbounded nudge repetition for a permanently stuck StatefulSet.

Corrected in this pass, per the keep-current rule: WP1-WP3 status headers and
their "not verified" e2e bullets, WP6's e2e note, NA8's `max-parallel` /
"no multi-node CI run" claims, NA13's "re-verified open" block. One new
finding, below.

### NA22 — Three dead tests: fail without `-short`, never run anywhere automated — DONE

**Status (2026-08-20):** all three repaired, all eight `testing.Short()` gates
removed, `-short` dropped from `test-unit` and `test-unit-coverage`. Nothing in
`internal/controller` is skipped in CI any more.

**Found during WP4 verification (2026-08-19) as a footnote, promoted to its
own numbered item 2026-08-20** per this ticket's rule that findings become
items, not footnotes.

#### The finding

`make test-unit` and `make test-unit-coverage` both passed `-short`
(`Makefile:76,82`), and CI's Unit Tests job runs `make test-unit-coverage` —
so every Short-gated test was skipped everywhere automated. Eight tests in
`internal/controller` carried the gate (seven in `rolling_update_test.go`, one
in `valkey_controller_test.go`); five of them passed without `-short`, three
failed:

```
go test ./internal/controller/ -count=1 \
  -run 'TestHandleTopologyRestoration|TestHandleMultiReplicaRollingUpdate_SplitBrainDemotedBeforeUpdate'
```

- `TestHandleMultiReplicaRollingUpdate_SplitBrainDemotedBeforeUpdate` failed in
  the fixture: `statefulsets.apps "mr-split" not found`.
- `TestHandleTopologyRestoration_Phase1SetsAnnotationAndRequeues` and
  `TestHandleTopologyRestoration_SelfLoopRecovery` both failed asserting the
  state advances to `verifying-topology` while it actually read
  `restoring-topology`.

**Verified pre-existing (2026-08-20):** identical failures — same messages,
same lines — on `main` @ `37552a5` in a clean worktree. Not caused, changed or
worsened by this branch; consistent with WP4's earlier check at `74460e9`.

**Why it mattered:** `SplitBrainDemotedBeforeUpdate` was the pre-existing guard
for exactly the code NA21 changed (`detectAndResolveSplitBrain`,
`handleMultiReplicaRollingUpdate`). The area was not unguarded — NA21 added
`TestDetectAndResolveSplitBrain_PrefersPromotedPodDuringFailover` and
`TestHandleMultiReplicaRollingUpdate_DoesNotDemotePromotedPod` — but a broken
test contributes nothing while its name promises coverage it does not deliver.

#### Root causes — all three are the fixture, none is a product defect

**1. `SplitBrainDemotedBeforeUpdate` — the StatefulSet was never created.**
The test built the CR and three pods and then did
`r.Get(..., "mr-split", sts)` on an object nobody had written. Since NA19 the
rolling update reads the *persisted* StatefulSet, so the missing object is fatal
before a single line of split-brain logic runs.

Its assertions were wrong on top of that. The fixture describes a cluster where
the rolling update is already complete, and that path ends in
`finalizeMultiReplicaRollingUpdate` → `Completed: true` — never the
`NeedsRequeue: true` the test demanded. Both assertions
(`NeedsRequeue`, `Error == nil`) would also hold if `detectAndResolveSplitBrain`
were deleted outright, so the test could not fail for the reason it was named
after.

**2+3. The two `handleTopologyRestoration` tests — no server answered
`REPLICAOF NO ONE`.** Both mocked `InstanceChecker` but left
`NewValkeyClientFn` at the `newTestReconciler` default, which points every
connection at `127.0.0.1` and gets an instant refusal. `promotePod0AndRedirect`
(`internal/controller/rolling_update.go:2543`) returns
`RollingUpdateResult{NeedsRequeue: true}` **before** `setRollingUpdateState`
when the promotion fails — correct behaviour: an unpromoted pod-0 must not be
declared promoted. So `restoring-topology` was the right answer to the wrong
question. Neither an outdated assertion after a state-machine change nor a
dormant defect: the fixture never let Phase 1 reach its own transition.

#### The fix

- `SplitBrainDemotedBeforeUpdate` now builds the StatefulSet with
  `stsForValkey` and its pods with `podFromStsTemplate` (the helpers NA19 added
  in `rolling_update_blocked_write_test.go`), so the pods are up to date with
  respect to the persisted template by construction. It asserts what its name
  claims: exactly one pod is contacted for demotion and it is `mr-split-0`, the
  master without connected slaves. `Completed` replaces the impossible
  `NeedsRequeue`.
- Both topology tests get `fakeValkeyServer(t)` — the RESP stub NA20 added in
  `manual_failover_known_master_test.go` — wired through `NewValkeyClientFn`,
  so `REPLICAOF NO ONE` succeeds and Phase 1 reaches
  `stateVerifyingTopology`. Assertions unchanged.
- No production code was touched.

#### The `-short` decision: gate removed, flag removed

The stated skip reason — "makes network connection attempts to non-existent
pods" — has not been true since `newTestReconciler` started redirecting every
Valkey connection to `127.0.0.1` (instant refusal instead of a DNS/TCP
timeout). Measured on this branch, `internal/controller` takes **3.36 s**
without `-short` and **3.33 s** with it. The gate bought nothing and cost three
tests.

All eight gates are gone, and `-short` is gone from both unit-test targets with
the reason recorded in the `Makefile`. That is the durable half: a future
`testing.Short()` gate can no longer remove a test from CI, because CI no
longer passes the flag. It also repairs `make test`, which never passed
`-short` and therefore failed on these three tests all along.

#### Verification

- The three repaired tests pass: `go test ./internal/controller/ -count=1 -run
  'TestHandleTopologyRestoration|TestHandleMultiReplicaRollingUpdate_SplitBrainDemotedBeforeUpdate'`.
- Mutation check 1 — replacing the
  `pods, masterIdx = r.detectAndResolveSplitBrain(...)` call in
  `handleMultiReplicaRollingUpdate` with a no-op fails
  `SplitBrainDemotedBeforeUpdate` with `"[]" should have 1 item(s), but has 0`.
  The old version of the test passed that mutation.
- Mutation check 2 — disabling the self-loop guard in
  `handleTopologyRestoration` fails `SelfLoopRecovery` with
  `expected verifying-topology, actual restoring-topology`.
- `make test-unit` green (50 s wall clock for `./...`), `make test-integration`
  green, `make lint` 0 issues, `make cyclo` all below 15, `make gosec` 0 issues.
- `grep -rn "testing.Short()" --include="*.go" .` returns nothing.

### NA23 — `restoring-topology` (Phase 1) has no stall escape — DONE

**Found 2026-08-20 while root-causing NA22's two topology tests.** Code-read
only; not reproduced at runtime.

`verifyTopologyRestored` (Phase 2, `internal/controller/rolling_update.go:2606`)
bounds itself: it calls `ensureFinalizationTimestamp` and completes after
`finalizationStallTimeout` even with rogue masters still present. Phase 1,
`handleTopologyRestoration` (`rolling_update.go:2493`), has no such bound. Every
failure it can hit returns a bare requeue:

- `GetReplicationInfo` on pod-0 errors,
- pod-0 still reports `role:master`, or `master_link_status != up`,
- `master_sync_in_progress` is set,
- `promotePod0AndRedirect`: TLS config error, or `REPLICAOF NO ONE` fails.

The outer loop offers no escape either. `clearStaleRollingUpdateState`
(`rolling_update.go:457`) would clear a stale state, but it only runs on the
`updatedCount != totalPods` branch of `handleMultiReplicaRollingUpdate`
(`rolling_update.go:2216`). During topology restoration every pod is updated by
definition, so the dispatch takes the early branch at `rolling_update.go:2199`
and never reaches it.

**Consequence:** a pod-0 that stays unreachable after the master replacement
leaves `vko.gtrfc.com/rolling-update-state: restoring-topology` on the CR
indefinitely and the rolling update never finalizes. Not data loss — pod-0 was
already made a replica of the promoted pod by `handlePostManualFailover`, and
the promoted pod keeps serving writes — but the status never returns to `OK`
and the original topology is never restored. NA22's now-green tests do not
cover this; they exercise the success path.

#### The fix — bound Phase 1, escape through Phase 2

Phase 1 now arms `vko.gtrfc.com/topology-restore-started` on every wait and is
bounded by `v.GetSyncTimeout()` (default 5 m, `spec.rollingUpdate.syncTimeout`).
The budget is the same one the replica-replacement phase uses because it is the
same wait — a replaced pod pulling a full dataset — and it is the only one of the
three candidates the user can raise when the dataset does not fit in the default.
It gets its own annotation rather than reusing `annotationSyncWaitStarted` (which
the replica phase leaves behind whenever it returns early) or
`annotationFinalizationTimestamp` (which Phase 2 owns — sharing it would let a
long Phase 1 eat Phase 2's budget).

`abandonTopologyRestoration` is the escape. Forcing the promotion stays off the
table for the reason above, so it gives up the canonical topology instead of the
data: warning event `TopologyRestoreAbandoned`, condition `TopologyRestored=False`,
and a transition to `stateVerifyingTopology` — **not** to a cleared state.

That last part is the non-obvious half. Clearing the state directly would end all
split-brain resolution: `checkAndHandleRollingUpdate` returns early at
`rolling_update.go:180` once no pod needs an update and no state is set, and
`detectAndResolveSplitBrain` has no caller outside the rolling update. Phase 2 is
the last pass that can consolidate the cluster, and it is already bounded, so the
stall escapes *into* it rather than around it.

Three things had to be true for that hand-over to be safe, and only the first was:

1. Phase 2 is bounded — it was, via `finalizationStallTimeout`, but only inside
   its `rogueCount > 0` branch. A permanently failing `collectPodStates` requeued
   forever, the same defect one function over. Now bounded too, completing the
   update unverified with a `TopologyVerifyIncomplete` event.
2. Phase 2 must not demote the pod holding the data. It called
   `detectAndResolveSplitBrain(..., "")`, and with the restoration abandoned the
   real master is the promoted replica, which in a shrunken cluster has no
   connected slaves. A returning pod-0 reporting master ties it at zero, and the
   "most connected slaves" fallback then picks pod-0 by lowest ordinal — NA21,
   reopened one state later. Both restoration states now name their authority:
   the known-master annotation, which `promotePod0AndRedirect` moves to pod-0
   only once the promotion actually succeeded, and which therefore still names
   the promoted replica on the abandoned path. `handleMultiReplicaRollingUpdate`
   passes it for `restoringTopology`/`verifyingTopology` for the same reason.
3. A non-pod-0 master must be a viable end state. It is: the `-rw`/`-r` Services
   select on `instanceRole`, not on ordinal (`internal/builder/service.go:168`).

The end state of a failed restoration is therefore "finished, single master, not
pod-0" instead of "stuck", with `TopologyRestored=False` as the durable record —
the phase itself returns to `OK`, because the cluster is healthy.

#### Verification

- New tests in `internal/controller/topology_restore_stall_test.go`: Phase 1 arms
  the timestamp and keeps waiting; all three Phase-1 failure classes (pod-0
  unreachable / still master / mid-sync) abandon after the timeout without ever
  contacting pod-0; the success path records `TopologyRestored=True` and moves
  the known-master back to pod-0; Phase 2 prefers the known master over the
  lowest ordinal; Phase 2 completes when the pod lookup keeps failing.
- Mutation check 1 — reverting Phase 1 to a bare requeue fails all three
  `AbandonsAfterSyncTimeout` cases on the state assertion and the condition.
- Mutation check 2 — passing `""` back into Phase 2's split-brain call fails
  `PrefersKnownMasterOverLowestOrdinal` with the demotion target
  `topo-km-1...`, i.e. exactly the NA21 data loss.
- Mutation check 3 — removing the Phase-2 lookup bound fails
  `CompletesWhenPodLookupKeepsFailing` on the missing timestamp.
- `make lint` 0 issues, `make cyclo` all below 15, `make gosec` 0 issues,
  `make test-unit` and `make test-integration` green.

**Not covered:** no E2E test forces a pod-0 that never returns, so the escape is
verified at unit level only.

**Qualified by the fourth review:** NA26 (the Phase-2 timeout exit can leave a
live split brain that nothing ever re-detects), NA27 (the arming write is
discarded, so the bound can fail to arm), NA28 (a stale timestamp from a died
rolling update pre-expires the next update's Phase-1 budget).

## Follow-up work from the fourth review (2026-08-20)

Review of the finished branch (HEAD `91b9647`, NA1-NA23 all landed) with three
focus questions: what is still open, whether the operator-upgrade migration
path is safe, and weaknesses in the shipped implementation. Everything below
is **code-read verified with file:line evidence; nothing was re-run on a
cluster in this pass.**

Held up under verification (no findings): the nudge mechanism (unconditional
for both StatefulSets, NA16 error split, mutex-guarded tracker, metadata-only
merge patch, RU requeue authority), the 30 s backoff cap and its wiring, the
blocked-pass phase authority (context-scoped, no leak across CRs), the
`ReconcileBlocked` dedup incl. ObservedGeneration, both PDB ownership guards,
NA19's four live-StatefulSet read sites, the NA21/NA23 state gates and bounds,
annotation cleanup via `clearRollingUpdateState`, RBAC markers + generated
role + Helm ClusterRole (policy and events.k8s.io both present), CRD defaults
(`off` correctly quoted), the Helm CRD living in `templates/` (so `helm
upgrade` applies it), README/values documentation for PDB and anti-affinity,
the Makefile without `-short`, and the CI matrix with its guard greps.

CI at HEAD `91b9647` (run 32372586667): **completed success on every job**,
both e2e legs and the `e2e-gate` aggregator included — confirmed after this
review's first pass, which had caught the single-node leg still in progress.
Every commit on the branch now has a green run.

The open points carried from earlier reviews (the `waitForValkeyEvent` poll
deduplication from the NA14 note, NA23's missing E2E for the abandon path,
NA20's residual windows) are promoted to their own numbered items below
(NA35, NA36, NA38) per this ticket's findings-become-items rule.

Findings NA24-NA38, ordered by severity within each pass. NA25 is closed
without change (decision recorded there); NA24 was rescoped to the canonical
upgrade path in README.

**Status as of the fifth pass (2026-08-20): all of NA24 and NA26-NA38 are
implemented** — see the DONE block at each item for what was verified and how.
Two carry an explicit caveat: **NA36 is code-complete but has never been executed
against a cluster**, and NA31's UID precondition cannot be observed in a unit test
(NA42). The findings that surfaced while implementing them are NA39-NA46, in the
fifth-pass section at the end of this file.

### NA24 — Image-only operator upgrade with a stale ClusterRole crashloops the operator — DONE (documentation only)

**The migration hazard of this branch.** `SetupWithManager` registers
`Owns(&policyv1.PodDisruptionBudget{})` unconditionally
(`internal/controller/valkey_controller.go:1909`, refreshed 2026-08-21 from the
`:1778` this item was written against). The manager has no cache
options and no missing-informer tolerance (`cmd/main.go`, `managerOptions` at
`:74-81` — no `Cache` field, refreshed from `:87-92`); with a
ClusterRole that lacks the new `policy` rule the PDB informer's initial LIST
gets 403, the cache never syncs, `mgr.Start` returns the error and the process
exits (`cmd/main.go:122-125`) → CrashLoopBackOff. Health probes are
`healthz.Ping` only, so nothing degrades gracefully: **all reconciliation
stops, not just the PDB feature — for every user, opted in or not.**

Reachable in practice: bumping `image.tag` against an older chart, mirrored
manifests, a paused/partial Flux apply of the RBAC. A full `helm upgrade` with
this branch's chart is safe (the ClusterRole ships unconditionally,
`templates/clusterrole.yaml:1-7`).

**Decision (2026-08-20, Hans) — rescoped.** A crashlooping operator that
states its cause on the console is the *accepted* failure mode for an upgrade
performed outside the documented path. The docs get **one canonical upgrade
path** that, when followed, applies CRD and RBAC together (`helm upgrade` with
the chart — both live in `templates/`, verified in this review). No per-path
warnings, no "apply RBAC before the image" special-casing: an admin who
deviates from the documented path is responsible for the deviation. The
originally proposed release-note/README warning is dropped with this decision.

**Crash output meets the bar without new code** (expected from standard
client-go/controller-runtime behavior; not reproduced on a cluster): during
the cache-sync wait the reflector repeatedly logs
`poddisruptionbudgets.policy is forbidden: User "system:serviceaccount:..."
cannot list resource "poddisruptionbudgets" in API group "policy"` as ERROR,
and the fatal `problem running manager` error (`cmd/main.go:122-125`) names
the unsynced Kind (`*v1.PodDisruptionBudget`). A fail-fast preflight
(SelfSubjectAccessReview before `mgr.Start`) would only sharpen the last line
and was deliberately not added — minimum code, the reason is already printed.

**Remaining fix, and all of it:** README has **no upgrade section at all**
(verified: `grep -i upgrade README.md` matches only the anti-affinity feature
bullet). Add the canonical `helm upgrade` snippet (collapsed `<details>` per
the documentation standard) so following the docs performs the update
correctly. The statement at `README.md:617-618` ("no permission change is
needed to turn the feature on") stays — it is correct on the canonical path.

The marker-vs-chart drift test moved to NA37 and is unaffected by this
decision — it is what keeps the canonical path sufficient.

**DONE 2026-08-20 (fifth pass) — documentation only, exactly as decided.**
`README.md:41-118` now carries an `Upgrade the Operator` section directly below
`Install the Operator`: the canonical command inline
(`helm upgrade valkey-operator deploy/helm/valkey-operator --namespace
valkey-operator-system`), one sentence stating that updating the operator image
on its own is not a supported upgrade path, and a collapsed `<details>` block for
the long tail — pre-upgrade hook, what the upgrade does to running clusters,
verify step, released chart repository, rollback, uninstall.

Verified while writing it, not assumed:

- `helm template valkey-operator deploy/helm/valkey-operator -n
  valkey-operator-system` (helm v3.21.3) renders ServiceAccount, **CRD**,
  **ClusterRole**, ClusterRoleBinding and Deployment from one release, plus the
  `valkey-operator-pre-upgrade` hook Job with its own RBAC. The documented command
  therefore does apply CRD and RBAC together — the premise of the decision holds.
- `templates/crd.yaml` carries no `helm.sh/resource-policy: keep` (grepped), so
  `helm uninstall` deletes the CRD and with it every `Valkey` CR. Documented
  explicitly, with the safe order and with the PVC consequence: no
  `PersistentVolumeClaimRetentionPolicy` is set on the StatefulSets
  (`internal/builder/statefulset.go:996-1020`), so PVCs survive and reattach.
- The released chart repository is real and current: `origin/gh-pages` holds
  `index.yaml` listing `valkey-operator` up to `1.10.47` (created 2026-08-20) at
  `https://guided-traffic.github.io/valkey-operator/`, and
  `valkey-operator-1.10.47.tgz` contains `templates/crd.yaml` — the released chart
  ships the CRD the same way the local one does.
- The sidecar-rolls-every-cluster statement in the details block is grounded:
  the chart passes `--operator-image` / `OPERATOR_IMAGE` from
  `image.repository:tag` (`deploy/helm/valkey-operator/templates/deployment.yaml:42,47-48`)
  and the sidecar uses that image (`internal/builder/statefulset.go:794-801`).
- Release name and namespace match the existing install snippet.
- The statement "no permission change is needed to turn the feature on" was left
  untouched, as decided. It moved from `README.md:617-618` to `:700-701` because
  the new section sits above it — the coordinate in the paragraph above this one
  is the pre-edit one.

**Not verified, and it stays that way: the crashloop console output.** It was not
reproduced on a cluster in this pass either. It remains an expectation derived
from standard client-go / controller-runtime behavior (reflector 403 on the
initial LIST, cache never syncs, `mgr.Start` returns, `cmd/main.go:122-125`
exits). No code was added to sharpen it, per the decision.

### NA25 — Upgrade roll from NA20's init-script change — CLOSED without change (2026-08-20, decided by Hans)

The NA20 block sits inline in the pod spec
(`internal/builder/statefulset.go:434-455`) and `ComputePodSpecHash` covers
init containers (`statefulset.go:1019-1021`), so the hash changes for every
`replicas > 1 && !sentinel` cluster. Verified upgrade matrix for an unchanged
CR:

| Scenario | Data STS | Sentinel STS |
|---|---|---|
| Helm, pinned tag, HA+Sentinel | rolls (sidecar image — NA9, known) | rolls once (NA11 TGPS, known) |
| kustomize / floating tag, HA+Sentinel | **no roll** (sidecar constant unchanged) | rolls once (NA11) |
| multi-replica **without** Sentinel | **rolls on BOTH install paths — new, undocumented** (routed through `handleMultiReplicaRollingUpdate`, i.e. the full manual-failover cycle) | n/a |
| standalone `replicas: 1`, no persistence | no delete, no data loss — sidecar-only delta is deferred (`rolling_update.go:2147-2156`); note the deferral compares images only and holds because the sidecar is the sole delta on this branch | n/a |

**Decision (2026-08-20, Hans): closed without change.** Every operator
upgrade already rotates all multi-replica Valkey instances via the sidecar
image on the canonical install path (Helm — NA9), so the NA20 script change
adds no roll there and warrants no separate mention. The one boundary,
recorded so it is not mistaken for an oversight: on the kustomize/floating-tag
path the sidecar constant never changes, so there the roll IS new — those
paths sit outside the canonical upgrade path and fall under the NA24 doctrine
(the deviating admin owns the consequences). The matrix above stays as the
verified factual record.

Still true and unaffected by the closure: the first post-upgrade pass executes
the code this branch rewrote most (manual failover, NA20/NA21/NA23) once per
multi-replica cluster — which is where NA26/NA29 live. That is an argument for
landing the NA26-NA30 hardening PR promptly, not for a release note. The
standalone `replicas: 1` row remains load-bearing as a regression guard: it
holds only while the sidecar is the sole pod-spec delta for single-replica
pods.

### NA26 — Permanent split brain after the Phase-2 timeout exit: nothing ever re-detects a rogue master — DONE

`verifyTopologyRestored` completes and clears the state with rogue masters
still live (`internal/controller/rolling_update.go:2789-2797`). Afterwards
`checkAndHandleRollingUpdate` early-returns (`:162-190`) and
`detectAndResolveSplitBrain` has no caller outside the rolling update;
`checkAndRecoverNoMaster` handles only `masterCount == 0`
(`valkey_controller.go:1674-1706`). Without Sentinel the sidecar labeler
labels each self-reported master `instanceRole=master`
(`internal/sidecar/labeler.go:171,213-227`; the demote cross-check is wired
only with Sentinel, `internal/sidecar/run.go:70-78`), and the `-rw` Service
selects on that label (`internal/builder/service.go:180`) — **writes
round-robin across two independent masters indefinitely.** On this exit path
`TopologyRestored` can remain `True` (`:2727`); the only trace is the
`TopologyRestoreIncomplete` warning event.

Consequence: NA23's escape converts "stuck forever" into, worst case,
"finished with a live, undetected split brain".

**Decision (2026-08-20, Hans): steady-state multi-master check, coupled with
NA35(a).** A new check in `handlePostRollingUpdateChecks` for non-Sentinel
multi-replica clusters, triggered cheaply — probes only when at least two pods
carry `instanceRole=master` (no per-pass connections in the healthy case).
Authority is the known-master annotation; the check demotes only when the
named pod is alive and itself reports `role:master` — never on tie-breaking
alone outside a rolling update. This closes NA26 for every path (not just the
Phase-2 timeout exit) and is the prerequisite that makes NA35 option (a)
safe. Ships together with NA35(a) in the hardening PR.

**DONE 2026-08-20 (fifth pass), shipped together with NA35(a) as decided.**
New file `internal/controller/steady_state_master.go`:
`checkSteadyStateSplitBrain` (`:46`), `listMasterLabeledPods` (`:94`),
`confirmedMasterAuthority` (`:114`), `demoteConfirmedRogues` (`:142`),
`steadyStateRecheckDelay = 15s` (`:23`, deliberately above the 1 s sidecar label
poll). Wired with six lines in `handlePostRollingUpdateChecks`
(`valkey_controller.go:361-366`), after `checkAndRecoverNoMaster` (mutually
exclusive: zero masters vs >= 2) and before `updateStatus`, so a pass that changed
replication does not publish a status describing the topology it just replaced.

`detectAndResolveSplitBrain` is deliberately **not** reused, so its
connected-slaves tie-break stays unreachable from steady state. It reuses
`knownMasterPodName`, `demoteRogueMaster` (which emits `SplitBrainResolved`),
`podState`, `isPodReady`, `recordEvent`, `getRollingUpdateState` and
`common.MasterSelectorLabels` — the same selector the `-rw` Service uses
(`internal/builder/service.go:180`), so no second selector and no second demote
path exist.

Verified by running: `make test-unit` green with 10 new tests in
`internal/controller/steady_state_master_test.go`. A plain revert is impossible
(pre-fix the function does not exist), so each guard was confirmed by targeted
mutation, each reverted immediately and the file re-checked byte-identical:
dropping the `len(labeled) < 2` gate fails `_NoopWhenOnlyOneMasterLabeled`;
dropping the rolling-update guard fails `_SkippedDuringRollingUpdate`; dropping
the multi-replica/no-Sentinel guard fails `_SkippedWithSentinel`; tie-breaking to
the lowest ordinal fails `_RefusesWithoutKnownMaster`; accepting an unreachable
or replica authority fails `_RefusesWhenKnownMasterUnreachable` /
`_RefusesWhenKnownMasterIsReplica`; trusting the label on candidates fails
`_IgnoresStaleMasterLabel`; removing the wiring fails
`TestPostRollingUpdateChecks_RunsSteadyStateCheck`. `make cyclo`:
`checkSteadyStateSplitBrain` 7, `demoteConfirmedRogues` 6,
`confirmedMasterAuthority` 4, `listMasterLabeledPods` 2,
`handlePostRollingUpdateChecks` 7 -> 8.

**Amended by the recheck of the fix round (2026-08-20) — see NA50 and NA52.** Two
things changed here after this item was written:

1. **The authority contract is sharpened, not widened:** *the known-master
   annotation is the tie-breaker among multiple masters; it is never used to
   overrule a single undisputed master.* The version shipped first made it the
   authority full stop, which turned an ordinary node drain into data loss (NA50).
   `adoptUnrecordedPromotion` (`internal/controller/steady_state_master.go`) now
   runs at `len(labeled) == 1` and moves the annotation to a confirmed sole master
   the operator did not promote itself. **Amended again by the sixth pass
   (2026-08-21):** that adoption needs *evidence* — the drain stamp, the structural
   rule, or the recorded pod answering that it is no longer master — because the
   label alone cannot tell a drain promotion from a self-election, and it also runs
   at `len(labeled) >= 2` on an unambiguous stamp. See the sixth-pass section.
2. **The refusal path schedules a recheck after all** (NA52): when a confirmed
   second master could not be demoted, `steadyStateRecheckDelay` rides back as a
   non-terminal `ctrl.Result` that `reconcileWorkload` applies after
   `updateStatus`. This replaces the "Refusal paths schedule no requeue" residual
   below, which was correct for the first shape and is not correct any more.

**Invariant this item establishes — stated here so it is not weakened later:**
*a promotion the operator could not record is not a completed promotion.* This
check demotes **toward** the known-master annotation, so the annotation stopped
being telemetry the moment the check shipped: it is a data-plane authority. Every
write path that records a promotion is therefore part of the promotion and must
fail the pass rather than proceed unrecorded — `persistManualFailoverState`
(NA29) and `recordPromotedMaster` inside `promotePod0AndRedirect`. The fifth-pass
adversarial review found three separate defects (its findings 1, 3 and 4) that
were all the same thing: this invariant not yet holding while the check already
relied on it. Do not relax either write back to `_ = r.Update(...)`.

The recheck extended the invariant to the third promotion site and gave the second
one a rollback: `checkAndRecoverNoMaster` records **before** it promotes and
returns an error when the record fails (NA51), and `promotePod0AndRedirect` hands
pod-0 back to the previously recorded master when the record fails after the
promotion already happened (NA52). Both follow from the same sentence: a promotion
that is not recorded must not be left standing.

**Residuals, all deliberate:**

- Inert without the annotation. A cluster that never failed over, or whose CR
  annotations a GitOps prune stripped, keeps two masters with only a
  `SplitBrainUnresolved` Warning. Self-consistent: without a failover the init
  script also never lets a non-zero ordinal self-claim, so the operator cannot
  have produced that state itself.
- ~~Refusal paths return `done=false` and therefore schedule no requeue; re-entry
  rides on StatefulSet events or the informer resync.~~ **Superseded by NA52:** the
  *unresolvable* refusal (a confirmed second master that would not demote) now
  carries the 15 s recheck back non-terminally. The other refusals (no annotation,
  authority unreachable or a replica, stale label only) still schedule nothing, on
  purpose — the operator has no fix to poll for.
- **Runs during a blocked pass.** The demotion path performs no API writes (cached
  List, Valkey RESP commands, Events), so `withBlockedPass` does not suppress it.
  Intended — a split brain is a data-plane emergency — but anyone reading the
  NA33/NA34 blocked-pass work should know this one behavior escapes it. The
  adoption added by NA50 is the exception: it writes the annotation and the replica
  ConfigMap, so a blocked pass fails it with a log line and the next pass retries.
- No e2e; NA36 owns the observation and is itself not executed yet.

### NA27 — The NA23 bound can fail to arm: the annotation write error is discarded — DONE

`ensureTopologyRestoreTimestamp` does `_ = r.Update(ctx, v)`
(`rolling_update.go:702`, same pattern at `:688`). If that write keeps failing
(persistent conflicts, an admission gate on the CR), the annotation never
persists, `isTopologyRestoreStalled` stays false forever, and Phase 1 requeues
indefinitely — the exact stall NA23 exists to break. Fix: log the error and
back the bound with an in-memory first-seen timestamp (the `nudgeTracker`
pattern), or treat N consecutive arming failures as stalled.

**DONE 2026-08-20 (fifth pass).** Both annotation-armed bounds now go through one
pair of helpers in `internal/controller/rolling_update.go:678-800`:
`ensureWaitBound` records a first-seen entry in the existing `nudgeTracker`
(`r.nudges`) under a collision-proof key, **logs** the annotation Update error
instead of discarding it, and deletes the annotation it could not persist;
`waitBoundExceeded` prefers the annotation (survives an operator restart) and
falls back to the in-memory first-seen (survives a failing API server).
`isTopologyRestoreStalled` and `isFinalizationStalled` (`:865`) both use it.
The in-memory copy is dropped in `clearRollingUpdateState` (`:1912-1917`, every
completion path funnels through it) and in `forgetNudges`
(`internal/controller/nudge.go:41-110`, the CR-gone / being-deleted exits at
`valkey_controller.go:195,206`).

`ensureFinalizationTimestamp` got the same treatment on purpose: identical
discarded-write pattern, and it bounds Phase 2 plus the sentinel finalization, so
an unarmed bound there requeues forever exactly like Phase 1. Sharing one helper
made it free.

**New hazard found while implementing this, and fixed because the fix does not
work without it:** if `ensureWaitBound` leaves the annotation on the in-memory CR
after a failed write, every later pass re-arms it with a fresh value,
`waitBoundExceeded` reads that instead of the tracker, and the deadline is never
reached — NA27 reproduced one indirection deeper. Hence the delete-on-failure at
`rolling_update.go:713-724`.

Verified by running (`make test-unit`) plus per-fix reverts:
`internal/controller/rolling_update_bounds_test.go` —
`TestWaitOrAbandonTopologyRestoration_BoundHoldsWhenArmingWriteFails`,
`TestEnsureWaitBound_DropsTheAnnotationItCouldNotPersist`,
`TestClearRollingUpdateState_ForgetsInMemoryWaitBounds`,
`TestForgetNudges_AlsoDropsWaitBounds`,
`TestWaitBoundKey_CannotCollideWithNudgeKey`. Removing the `r.nudges.observe`
call fails the first four; removing the delete-on-failure fails the first two;
removing the forget in `clearRollingUpdateState` fails the third; removing it in
`forgetNudges` fails the fourth. The key-collision test cannot fail pre-fix (the
symbol did not exist) — it is a design-invariant guard, stated as such: the data
StatefulSet is named after the CR (`common/labels.go:135`), so the wait-bound key
would collide with the nudge key without the `/` separator, which cannot appear
in an object name.

**Residual, deliberate:** the in-memory bound is per operator process, so a
restart before the annotation ever lands restarts the budget. The alternative is
a durable store the operator does not have, and the annotation covers the restart
case whenever it can be written at all.

### NA28 — A stale `topology-restore-started` pre-expires the next rolling update's Phase-1 budget — DONE

`clearStaleRollingUpdateState` runs only on the `replacedCount == 0` branch
(`rolling_update.go:465-473`). A rolling update that dies in
`restoring-topology` (operator crash/OOM before any clear path), followed by a
later update that starts with at least one pod already matching the new
template, dispatches straight into Phase 1 against an hours-old timestamp →
immediate `TopologyRestoreAbandoned` with no restoration attempt. Fix: re-arm
the timestamp when entering `stateRestoringTopology`
(`handlePostManualFailover`, `rolling_update.go:2560`), or reject timestamps
predating the current state transition.

**DONE 2026-08-20 (fifth pass).** `handlePostManualFailover` calls
`armTopologyRestoreBound(v)` immediately before
`setRollingUpdateState(stateRestoringTopology)`
(`rolling_update.go:789-800` and the call site at `:2674-2680`). It overwrites
`vko.gtrfc.com/topology-restore-started` in memory — persisted by the state Update
that follows, so no extra API call — and forgets plus re-observes the in-memory
key. A timestamp left behind by a rolling update that died in
`restoring-topology` can therefore no longer spend the new update's Phase 1
budget. The entry point is the right place because the stale-state escape it
complements, `clearStaleRollingUpdateState`, runs only on the
`replacedCount == 0` branch (`:457-473`) — as this item recorded.

Verified by running:
`TestHandlePostManualFailover_ReArmsPhase1BudgetOnEntry`
(`rolling_update_bounds_test.go`) — a CR parked in manual-failover with a
four-hour-old timestamp ends the pass with the annotation within a minute of now
and the tracker armed, and the following `handleTopologyRestoration` pass with
pod-0 unreachable requeues instead of recording `TopologyRestoreAbandoned`.
Confirmed failing with the `armTopologyRestoreBound` call temporarily removed.

**Sibling not fixed, now NA40:** entering `stateVerifyingTopology` does not
re-arm the finalization bound, which is the same defect on Phase 2.

### NA29 — Manual failover: an annotation persist failure after the promotion reopens NA21 for one window — DONE (residual named)

`handleManualFailover` runs `promoteAndRedirect` (`rolling_update.go:2368`)
**before** persisting `promoted-pod`/state/known-master (`:2380-2389`). If
that Update fails, the cluster is already failed over while the state is `""`
— the next pass feeds `knownMaster = ""` into the resolver, and with two
replicas the 0-0 tie demotes the promoted pod (the NA21 data loss).

**Decision (2026-08-20, Hans): retry-in-pass.** The order stays
promote-then-persist; the annotation Update gets a bounded conflict retry
(fresh Get + re-apply, ~3 attempts) before the pass fails. This addresses the
dominant failure cause (resourceVersion conflicts under concurrent status
writes) with no semantic change. **Named residual, accepted:** an operator
crash or total API outage in the instant between promotion and persist still
leaves the NA21 window open — small but not zero; the alternative
(persist-before-promote) was rejected as too much state-machine rework for
that margin.

**DONE 2026-08-20 (fifth pass) — retry-in-pass, as decided; order stays
promote-then-persist.** The three annotations (promoted-pod, rolling-update-state,
known-master) are written by `persistManualFailoverState`
(`rolling_update.go:2488-2540`, called at `:2455-2461`): apply + Update, and on a
conflict a bounded `retry.RetryOnConflict(retry.DefaultRetry, ...)` that re-Gets
the CR, re-applies the three annotations, updates, and `DeepCopyInto`s the fresh
object back over the caller's `v` — so `reconcileReplicaConfigMap`, the master
delete, `updatePhase` and `recordEvent` keep working on a valid, current object.
Non-conflict errors return immediately: refetching cannot fix an admission
rejection. No new module dependency — `k8s.io/client-go` is already a direct
require (`go.mod:13`), and `go.mod`/`go.sum` are unchanged.

Verified by running, tests in
`internal/controller/manual_failover_known_master_test.go`:
`TestHandleManualFailover_RetriesTheStateWriteOnConflict` (first Update conflicts;
the pass still succeeds, all three annotations land, the caller's `v` carries
them, the replica ConfigMap names the promoted host and pod-0 is deleted),
`TestHandleManualFailover_StateWriteRetryIsBounded` (conflict never clears: the
pass fails, the retry count stays bounded, **pod-0 is not deleted** while the
promotion is unrecorded), `TestHandleManualFailover_DoesNotRetryNonConflictErrors`.
With the retry removed the first two fail; the third passes either way by design
(it pins that we did not start retrying everything).

**Residual, accepted and unchanged from the decision:** an operator crash or total
API outage in the instant between the promotion and a successful persist still
leaves the NA21 window open. Closing it needs persist-before-promote, which was
rejected.

### NA30 — `handlePostManualFailover`'s new-pod guard is image-only — DONE

The guard meant to keep `REPLICAOF` away from the old, about-to-die pod
compares only `valkeyImageFromSts` and is skipped entirely when it is empty
(`rolling_update.go:2507-2515`). A **config-hash-only** rolling update (image
unchanged) passes it trivially; the remaining protection is the
`DeletionTimestamp` check (`:2500-2504`), which misses a stale cache read.
Fix: compare `AnnotationConfigHash`/`AnnotationPodSpecHash` on the pod, as
`podNeedsUpdate` does.

**DONE 2026-08-20 (fifth pass).** The new-pod guard in `handlePostManualFailover`
now calls `podNeedsUpdate(masterPod, valkeyImageFromSts, sidecarImageFromSts,
configHashFromSts, podSpecHashFromSts, template containers)` against the live
StatefulSet (`rolling_update.go:2629-2647`), replacing the image-only loop. The
image check is subsumed — `podNeedsUpdate` compares the Valkey and sidecar images
first — and the comment says so. It reuses the existing helper (`:224`) and the
same four sts-derived inputs as `checkAndHandleRollingUpdate` (`:155-173`) and
`collectPodStates` (`:1026`), so no comparison logic is duplicated; the
`DeletionTimestamp` check above it is untouched.

Verified by running:
`TestHandlePostManualFailover_WaitsWhenOnlyTheConfigHashChanged` (two cases,
stale `AnnotationConfigHash` and stale `AnnotationPodSpecHash` with an identical
image — no `REPLICAOF` is sent and the state does not advance),
`TestHandlePostManualFailover_GuardVerdicts` and
`TestHandlePostManualFailover_WaitsForTerminatingPod`. Restoring the image-only
loop fails the config/spec-hash test; the other two pass before and after, which
is their point — the tightened comparison did not deadlock the phase or drop the
image case.

**Residual, deliberate:** `podNeedsUpdate` returns false when the pod lacks the
hash annotation (pods from older operator versions) — the same semantics as the
rest of the rolling update, not changed here.

### NA31 — PDB cleanup delete lacks a UID precondition — DONE (unit-verifiable only in part, see NA42)

`cleanupPodDisruptionBudget` decides on a cache-backed Get and then deletes by
name (`internal/controller/pdb.go:198,212`). Race: the operator-owned PDB is
deleted, the user recreates their own budget under the same name before the
cache catches up → the operator deletes the user's object — the exact outcome
the NA14 guard advertises against (`api/v1/valkey_types.go:309-313`). Fix:
`client.Preconditions{UID: &pdb.UID}` on the Delete. (No delete path in the
repo uses preconditions; the PDB path is the one that promises
non-destruction.)

**DONE 2026-08-20 (fifth pass).** `cleanupPodDisruptionBudget` deletes with
`client.Preconditions{UID: &pdb.UID}` (`internal/controller/pdb.go:265`), so the
Delete applies to the object the ownership check inspected or to none at all. The
precondition failure (Conflict) is treated as the guard working, not as a pass
failure: logged, pass returns nil — a different UID under that name is by
definition not the budget this pass decided about, and the next pass re-Gets the
name and takes the foreign-budget branch. Both decisions are documented in code
(`pdb.go:248-262`, `:268-273`); the switch on `err` is at `:248-279`.

**ResourceVersion was deliberately not added:** kube-controller-manager rewrites
PDB `.status` (`disruptionsAllowed`, `currentHealthy`) continuously, so a
cache-backed read is routinely a few revisions behind and an RV precondition
would reject nearly every cleanup forever. A changed RV is still the same object;
only identity matters here.

Verified by running: `TestCleanupPodDisruptionBudget_DeletesWithUIDPrecondition`
(asserts the Delete carries `Preconditions.UID` equal to the stored UID, that
`Preconditions.ResourceVersion` is nil with the reason, and that the owned budget
is really removed) and `TestCleanupPodDisruptionBudget_ToleratesPreconditionConflict`
(injected `apierrors.NewConflict`: the pass returns nil and the object survives).
New helpers `operatorOwnedPDB` (explicit UID — the fake client does not mint UIDs
on Create, so a reconcile-written budget would have made the assertion vacuous)
and `capturedDeleteOptions`. Both tests FAIL against `pdb.go` restored from HEAD.

**Residuals:** `apierrors.IsConflict` on a Delete is not exclusively a
precondition failure (an admission/quota conflict would be swallowed the same
way) — accepted, other Delete conflicts are effectively unreachable here and the
pass is re-driven next reconcile. And the fake client never enforces the UID
precondition at all, so the real rejection cannot be observed in a unit test —
split out as **NA42**.

### NA32 — `PodDisruptionBudgetNotOwned` warns forever for CRs that never opted in; content warnings contradict it — DONE

With `spec.podDisruptionBudget` absent, the cleanup branch warns on every pass
about a same-named user PDB (`pdb_test.go:518-521` pins this as intended) —
after the upgrade, every user who hand-created the documented pre-feature F3
workaround PDB gets a permanent Warning stream without having changed
anything. Separately, when the feature IS enabled against a foreign budget,
`warnIfDataBudgetProtectsNothing`/`warnIfSentinelBudgetBlocksEveryDrain` still
run (`pdb.go:50,98`) and describe spec values that were never written. Fix:
gate the cleanup-side warning on `IsPodDisruptionBudgetEnabled()`; skip the
content warnings when the budget is not owned.

**DONE 2026-08-20 (fifth pass), both halves.**

(a) The cleanup-side `PodDisruptionBudgetNotOwned` warning is gated on
`v.IsPodDisruptionBudgetEnabled()` (`internal/controller/pdb.go:242-247`,
rationale at `:215-229`). A CR that never opted in no longer emits a permanent
Warning stream about a hand-written pre-feature workaround budget; the
non-destruction behavior is unchanged. **Explicit decision for the second case:**
the warning still fires when the feature is enabled but not applicable
(`replicas < MinPDBReplicas`, or Sentinel disabled), because that CR asked for
operator-managed budgets, the name is taken, and scaling back up would otherwise
silently produce no budget at all.

(b) `reconcilePodDisruptionBudget` now returns `(applied bool, err error)`
(`pdb.go:170-176`); `false` means a foreign budget under the name blocked the
write. Both callers (`:48-56`, `:103-111`) gate
`warnIfDataBudgetProtectsNothing` / `warnIfSentinelBudgetBlocksEveryDrain` on that
verdict, so the operator no longer describes a budget it never wrote using values
from spec. The verdict is returned rather than duplicating the `IsControlledBy`
check in the callers. Verdict values: create -> true, foreign -> false,
owned + update/no-op -> true.

Verified by running: `TestCleanupPodDisruptionBudget_KeepsForeignBudget` was
**rewritten** — the `require.Len(warnings, 4)` that pinned the old behavior is now
`assert.Empty`, with an in-file comment stating why the expectation flipped; its
non-destruction assertions are untouched. New
`TestCleanupPodDisruptionBudget_WarnsWhenEnabledButNotApplicable` and
`TestReconcilePodDisruptionBudget_NoContentWarningsForForeignBudget`. Against
HEAD's `pdb.go` the rewritten test and the no-content-warnings test FAIL; the
enabled-but-not-applicable test pins the half that must *not* change, so it was
mutation-verified instead (replacing the gate with `if false` fails exactly that
test and no other).

Documentation follow-through: `README.md:637-647` now says the Event is recorded
only while `spec.podDisruptionBudget.enabled` is true and that the content
warnings are suppressed for a foreign budget. The same qualifier is still missing
from `api/v1/valkey_types.go:309-314,328-330`, its two generated CRD copies and
`deploy/helm/valkey-operator/values.yaml:71` — those files were outside the
writing agents' ownership; tracked as **NA43**.

**Residual:** with the feature enabled, a foreign budget under a name whose
StatefulSet does not currently exist (e.g. `enabled: true` with Sentinel disabled
and a stale `<name>-sentinel` budget) still warns every pass. Narrowing further
needs a StatefulSet-existence check; the decision was to gate on
`IsPodDisruptionBudgetEnabled()` and that is what shipped.

### NA33 — Empty-phase early return bypasses the ReconcileBlocked machinery — DONE

`valkey_controller.go:211-215` returns on a failing first phase write before
`reconcileResources` ever runs. A webhook blocking the CR status subresource
(or lost `valkeys/status` RBAC) leaves a brand-new CR with an empty phase and
no condition — invisible for exactly the failure class WP2 surfaces. Low
likelihood. Fix: proceed on error instead of returning; the phase is written
again later in the same pass.

**DONE 2026-08-20 (fifth pass).** The empty-phase branch in `Reconcile` logs and
continues instead of returning (`valkey_controller.go:210-226`, the log line at
`:224`), so `reconcileResources`, `setReconcileBlockedCondition` and
`reconcileWorkload` all run even when the CR status subresource is blocked.

Whole-pass audit performed before the change, recorded because it is what makes
"proceeding is lossless" a fact rather than a hope: (1) the only other read of
`Status.Phase` in the pass is the `Error || Syncing` requeue check, and after a
failed initial write the in-memory phase is `Provisioning` or `""` — neither, and
`updateStatus` overwrites it first anyway; (2) the phase really is written again
in the same pass — `persistStatus` (`:1536-1550`) compares against a `prevStatus`
captured *after* this point, `"Setting up Valkey resources"` is a unique string in
the repo, and `OperatorVersion` moves off `""` on a fresh CR, so `statusUnchanged`
is false; (3) on a blocked pass the final `writePhase` bypasses suppression and
owns the phase anyway; (4) while the phase stays empty every later pass retries
it; (5) nothing else in the pass reads `Status.Message`.

Verified by running: `internal/controller/status_phase_test.go` —
`rejectFirstValkeyStatusWrite` helper plus
`TestReconcile_InitialPhaseWriteFailureDoesNotAbortPass` (no error returned, the
data StatefulSet exists afterwards, the stored phase is `Provisioning`) and
`TestReconcile_InitialPhaseWriteFailureStillReportsReconcileBlocked`
(first status write rejected **and** the NetworkPolicy create rejected:
`ReconcileBlocked=True/AdmissionWebhookDenied` still lands and the blocked pass
still writes phase `Error`). With `return ctrl.Result{}, err` restored, both fail.

**Residual, deliberately unguarded:** if the CR is deleted between the
top-of-Reconcile Get and `writePhase`'s refresh Get, the pass now proceeds instead
of returning, and `reconcileResources` creates children with an ownerRef to a gone
UID that GC collects immediately. Guarding it would need a NotFound special case
no other mid-pass write in this reconciler has. Split out as **NA41**.

### NA34 — Minor bundle (observability/hygiene) — DONE (1 + 2 implemented; 3-5 were accepted-as-is statements)

1. `setStatusCondition` stamps `ObservedGeneration` from the **refreshed**
   object (`valkey_controller.go:1637`): a spec edit racing the pass makes the
   condition claim a generation it did not evaluate — the over-fresh
   direction; the doc comment (`:1612-1614`) overstates the guarantee.
2. `setSidecarUpdatePendingCondition` issues an unconditional status Update on
   every standalone pass (`rolling_update.go:2180`) — contradicts the
   no-write-per-pass rationale (`reconcile_blocked.go:82-84`); the API server
   no-ops identical writes, so cost only.
3. Under a sustained admission block the effective nudge cadence stretches
   from 5 s toward the 30 s backoff cap, because the blocked path returns the
   error and controller-runtime ignores the discarded Result
   (`valkey_controller.go:246`). Post-recovery worst case is therefore
   ~30 s + nudge interval, not 5 s + interval. Acceptable; stating it so the
   "≤ 30 s design bound" is read correctly.
4. The invariant "short of pods ⇒ the pass requeues" does not cover the
   STS-absent case: a NotFound in `nudgeStatefulSet` reports not-short
   (`nudge.go:166-168`) and the Provisioning path ends with no requeue —
   recovery then rides on the `Owns()` watch event. By design, but
   watch-dependent.
5. Fleet cost, accepted-by-construction: the PDB informer LISTs/WATCHes every
   PodDisruptionBudget in the cluster (no `Cache.ByObject` filter in
   `cmd/main.go`), paid on upgrade even without opt-in. A label-filtered cache
   would blind the NA14 foreign-budget guard, so filtering is not a free fix —
   documenting the tension is the actionable part.

**DONE 2026-08-20 (fifth pass) — items 1 and 2 implemented, items 3-5 were
statements of accepted behavior and needed no code.**

**1 (NA34.1) — option (i), documentation.** The `setStatusCondition` doc comment
(`valkey_controller.go:1622-1631`) now states the real, weaker guarantee:
`ObservedGeneration` names the generation the CR carried at the moment of the
write, normally but not necessarily the evaluated one; a spec edit landing between
the caller's evaluation and the refresh Get (`:1650`) makes the condition claim a
generation it did not evaluate, and the over-claim lasts one pass because the next
reconcile recomputes. It also records why the field is stamped at all (kstatus
reads a missing `observedGeneration` as 0, i.e. permanently stale).

Option (ii) — capturing `v.Generation` before the refresh Get — is two lines but
**reverses NA17's recorded decision**, breaks
`TestSetStatusCondition_UsesRefreshedGeneration`
(`condition_generation_test.go:65-78`), which exists to pin refreshed-generation
semantics, and desynchronises `setReconcileBlockedCondition`'s skip guard, which
compares `existing.ObservedGeneration` against the caller's `v.Generation`
(`reconcile_blocked.go:96-97,111-115`). Small in lines, not in consequence.
Recorded here in case the item is ever reopened.

**No test can fail pre-fix for this half** — it is documentation-only. Stated
plainly rather than papered over with a test that passes either way. The behavior
the corrected comment describes is already pinned by
`TestSetStatusCondition_UsesRefreshedGeneration`.

**2 (NA34.2) — implemented.** `setStatusCondition` returns without issuing
`Status().Update` when `meta.SetStatusCondition` reports no change
(`valkey_controller.go:1658-1666`, rationale at `:1634-1641`). Semantics verified
against the vendored source
(`k8s.io/apimachinery@v0.36.3/pkg/api/meta/conditions.go:30-67`): `changed` is
true when the condition is new or when Status, Reason, Message **or
ObservedGeneration** differ; `LastTransitionTime` alone never sets it. Since `v`
was refreshed from the API server two lines earlier, a false return means the
stored condition already matches every field this write would set — the
ObservedGeneration bump on an otherwise-identical condition is still persisted.
All five call sites audited (`valkey_controller.go:1675,1682`,
`reconcile_blocked.go:101,118`, `rolling_update.go:548,1299,2807`); none can
depend on the unconditional write, because the refresh Get already overwrites the
caller's `v` wholesale.

Verified by running: `condition_generation_test.go` — `statusWriteCounter`
interceptor plus `TestSetStatusCondition_SkipsWriteWhenNothingChanged` (four
identical calls, exactly one write) and
`TestSetSidecarUpdatePendingCondition_NoWritePerPass` (the live call site,
`rolling_update.go:2260`, reached on every standalone pass: three no-drift calls
produce one write, a real False -> True transition still writes). Both fail with
the unconditional write restored. Two guards pin what the skip must not swallow —
`TestSetStatusCondition_WritesOnObservedGenerationBump` and
`TestSetStatusCondition_WritesOnReasonOrMessageChange` — and pass against both
versions by design.

**3, 4, 5 — no change, as this item already recorded.** The stretched nudge
cadence under a sustained block (~30 s + interval, not 5 s + interval), the
STS-absent case riding on the `Owns()` watch, and the unfiltered PDB informer are
accepted-by-construction; they were documented findings, not work items. Nothing
in this pass changed them.

### NA35 — NA20 residual: a full pod-set restart after a failover syncs the surviving data away — DONE (option (a), shipped with NA26)

Promoted from NA20's "what this does not fix" list; **pre-existing, not
introduced by this branch**, reachable in the supported non-Sentinel
multi-replica configuration.

Scenario: the recorded master is not pod-0 (post-failover topology), then the
whole pod set goes down and comes back — a node reboot of a node hosting both
pods of a 2-replica cluster is enough. Pod-0 starts first, finds no reachable
peer, and takes the ordinal fallback as master
(`internal/builder/statefulset.go`, non-Sentinel init script). When the
promoted pod returns, Phase 1 rejects pod-0 (`role:master` with
`connected_slaves: 0`), Phase 2 sees **itself** in the replica config and the
self-guard skips it, so the ordinal fallback applies — and the surviving copy of
the post-failover writes is lost.

**Corrected 2026-08-20 (fifth pass, measured against the pre-fix script), the
original text described only one of two shapes.** Which degenerate end state the
ordinal fallback produces depends on where the known-master annotation stands:

- annotation still naming the promoted pod (the shape the NA36/NA35 fixture
  builds): the promoted pod copies the replica ConfigMap verbatim and boots with
  `replicaof <itself>` — a replica of itself, log line `No existing master
  discovered, using ordinal-based config (ordinal=1)`. Captured verbatim from the
  pre-fix failure output of `TestInitScript_PromotedPodSelfNamedBootsAsMaster`.
- annotation already re-pointed to pod-0 by `promotePod0AndRedirect`: Phase 2
  follows pod-0 and the full sync discards the delta — the "replica of pod-0"
  shape the original text described.

Both are data-loss or degenerate, and both are closed by the self-claim; only the
mechanism sentence needed correcting. Persistence does not close either: pod-0
restores its pre-failover RDB/AOF and the promoted pod still syncs from it.

Why no cheap fix landed with NA20: every option needs a design decision.
(a) Letting a pod that finds *itself* in the replica config boot as master
closes this window but produces two masters when pod-0 already elected itself
— consolidation then depends on a steady-state split-brain check, i.e. on
NA26's fix; the two items should be decided together. (b) Making pod-0's
ordinal fallback wait for the recorded master before self-electing narrows the
window but needs a timeout to avoid deadlocking a genuinely first boot, and
after the timeout the original behavior returns. (c) Status quo: documented
residual, no guard.

**Decision (2026-08-20, Hans): option (a).** A pod that finds itself named in
the replica config boots as master instead of taking the ordinal fallback. The
transient two-master state this can produce is consolidated by the NA26
steady-state check, which is why both land in the same PR — (a) must not
merge without it. Note for that PR: the init-script change alters the
pod-spec hash again, so its release rolls non-Sentinel multi-replica clusters
once more (the norm per NA9/NA25, recorded for completeness).

The second NA20 residual — the replica ConfigMap write being rejected during
an admission gap — stays an accepted risk with existing visibility
(`KnownMasterPublishFailed` event, `ReconcileBlocked` condition) and needs no
own item.

**DONE 2026-08-20 (fifth pass) — option (a), shipped in the same change as NA26.**
Three edits inside the single indexed `fmt.Sprintf` of the non-Sentinel init
container: `SELF_IS_KNOWN_MASTER=0` next to `MASTER_ADDR`
(`internal/builder/statefulset.go:395`); Phase 2's self-guard turned into a
self-claim (`if [ "$KNOWN_MASTER" = "$MY_HOST" ]` sets the flag, `elif [ -n
"$KNOWN_MASTER" ]` keeps the old behavior for every other value, `:450-454`); and
a Phase 3 `elif` that copies the master config when the flag is set (`:473-475`).
The `fmt` argument list is untouched — the branch reuses `%[1]s/%[2]s/%[3]s`,
which are already passed, and adds no bare `%`.

Precedence is Phase 1 > self-claim > ordinal fallback, load-bearing in both
directions: below Phase 1 so a stale config can never displace an established
master with replicas (which NA26 would then amplify, same annotation as
authority), above the ordinal fallback because Phase 1 rejects a master with
`connected_slaves: 0` — exactly what a freshly promoted pod looks like. `MY_HOST`
is never empty in a pod, so an empty `KNOWN_MASTER` cannot match the first test.
pod-0 in a never-failed-over cluster now takes the `elif` instead of the ordinal-0
fallback and produces a **byte-identical config from the same mount** — only the
log line changes.

Verified by running, and this one had a real revert available:
`internal/builder/init_script_exec_test.go` —
`TestInitScript_PromotedPodSelfNamedBootsAsMaster` (the regression),
`TestInitScript_EstablishedMasterOutranksSelfClaim` (precedence),
`TestInitScript_NoKnownMasterFallsBackToOrdinal`, and
`TestInitScript_Pod0IgnoresSelfAsKnownMaster` renamed to
`TestInitScript_Pod0NamedAsMasterKeepsMasterConfig` with its log assertion updated
and its produced-config assertion unchanged (the byte-identity proof). Render test
`internal/builder/statefulset_test.go:1522`
`TestBuildStatefulSet_MultiReplica_InitScript_RendersSelfClaimBranch` (subtests
plain and tls-auth) asserts the rendered script contains no `%!` — a wrong verb
index surfaces only as literal text, never as a compile error. With
`statefulset.go` reverted to HEAD the three self-claim tests and both render
subtests FAIL, while `_ReturningPod0FollowsKnownMaster`,
`_EstablishedMasterOutranksSelfClaim`, `_NoKnownMasterFallsBackToOrdinal`,
`_StaleKnownMasterFallsBackToOrdinal`, `_EstablishedMasterWinsOverKnownMaster`,
`_ReplicaFollowsKnownMaster` and `_AnnouncesOwnHostname` all pass unchanged —
that is the every-other-case-is-identical evidence.

**Upgrade roll, measured, not estimated.** `ComputePodSpecHash` under both the new
and the HEAD version of `statefulset.go` (throwaway probe test, created, run,
deleted): multi-replica non-Sentinel `e0f91fd6 -> 8d564ce4` (**changed**),
standalone `30c90672` (unchanged), Sentinel `b44c030a` (unchanged). So this
release rolls non-Sentinel multi-replica clusters once through the failover-aware
cycle — the NA9/NA25 norm — and restarts nothing else.

**Same invariant as NA26, from the other side:** the init script boots a pod as
master **from** the known-master annotation, so a promotion the operator failed to
record does not degrade into a missing log line — it becomes a boot decision, and
the wrong pod comes up master. *A promotion the operator could not record is not a
completed promotion*: the annotation writes on both non-Sentinel promotion paths
are part of the promotion, never best-effort telemetry.

**Amended by the recheck (2026-08-20), same sharpening as NA26 — see NA50.** The
annotation this script self-claims from is the *tie-breaker among multiple
masters*, never a licence to overrule a single undisputed one. That distinction
lives on the operator side (`adoptUnrecordedPromotion` — which since the sixth pass
adopts only on evidence, never on the master label alone), and it is what keeps
this script honest: after a node drain the sidecar promotes a replica and cannot record
it, so without adoption the annotation — and the replica ConfigMap derived from it
— keep naming the drained pod, and this self-claim boots that pod as a second
master on a record that is simply out of date.

**This item is not load-bearing for the NA50 loss.** Removing the self-claim would
not have prevented it: the drained pod is normally pod-0, and a returning pod-0
reaches master through the ordinal fallback anyway. NA26's demotion alone was
sufficient to destroy the drain-window dataset. What (a) changes is the breadth —
a drained pod of any ordinal can now come back master on a stale record, not just
ordinal 0.

**Residuals, both inherent:** R2 — a pod restarting with a stale mounted replica
ConfigMap (kubelet refresh lag, up to ~1 min) can self-claim after the operator
re-pointed the annotation; bounded by NA26, because the CR annotation is already
correct, plus the 15 s recheck. It cannot be closed inside the init script: the
self-claim must outrank a zero-slave master, which is precisely what makes it
indistinguishable from this case at boot. R3 — in a >= 3-replica cluster where a
returning pod-0 has already attracted a replica before the promoted pod boots,
Phase 1 hands the promoted pod to pod-0 and its writes are still lost: narrower
than before, not eliminated by (a).

### NA36 — E2E coverage for the NA23 abandon path — DONE (executed 4x in CI, verified 2026-08-21)

**Status (ninth pass, 2026-08-21): executed and verified, beyond PASS/FAIL, against the CI
logs.** `TestE2E_RollingUpdate_TopologyRestoreAbandoned` ran in the single-node E2E leg of
four green CI runs on `feat/support-pdb` — runs `32451432385` (commit `9294ad9`, 149.19s),
`32459204736` (`ab9403a`, 190.87s), `32461153927` (`6140386`, 201.27s) and `32467455895`
(`2d49762`, 199.87s) — all five subtests PASS each time, including the NA26 observation
subtest ("The abandoned topology converges once pod-0 restarts", 14.27s in the latest run).
That satisfies the "run it at least three times" requirement below.

The three beyond-PASS checks this item demanded, read from the logs (checks 2 and 3 from run
`32467455895`; check 1 from all four):

1. **Jam timing:** `Jammed replication on abandon-2r-0 while the rolling update was in state
   "manual-failover"` — in **all four runs**, never late, so readiness is not faster than
   the poll.
2. **The masterauth poison bites:** the INFO dump shows `master_link_status:down` with
   `master_host:abandon-2r-1.abandon-2r-headless.e2e-topology-abandon.svc.cluster.local` —
   the load-bearing unverified assumption of this item is now runtime-confirmed.
3. **No `RollingUpdatePaused`** anywhere in the job log — 60 s `syncTimeout` is sufficient
   for the replica phase.

The item text below is kept as written (including its own sketch correction); everything
marked "not executed" in it is superseded by this block.

NA23's escape (Phase-1 timeout → `TopologyRestoreAbandoned` → Phase 2 →
completion with a non-pod-0 master) is verified at unit level only; no e2e
forces a pod-0 that never returns.

Sketch: 2-replica non-Sentinel CR with a short `spec.rollingUpdate.syncTimeout`
(verify first that the CRD accepts sub-minute values — `GetSyncTimeout`,
`api/v1/valkey_types.go:970-975`); trigger an image rolling update, poll the
state annotation, and install the existing pod-create block
(`blockPodCreation`) once the state reaches `replacing-master`/`manual-failover`
so the deleted pod-0 cannot come back. Assert: `TopologyRestoreAbandoned`
event, `TopologyRestored=False`, state cleared, phase back to `OK`, writes
served by the promoted master, `-rw` Service endpoints pointing only at it.
The block-install timing races the master replacement — the poll interval must
be well under the failover duration; if that proves flaky, an alternative is
rejecting only pod-0's create via an object-selector label, which first needs
a label that exists at CREATE time (the `instanceName` label is applied by the
sidecar at runtime, so it is NOT usable for this — verified reasoning, not
tested).

Also the natural place to observe NA26's exposure once its fix exists: after
the abandon, un-blocking pod-0 and asserting the cluster converges to one
master would fail against today's code.

**CODE-COMPLETE 2026-08-20 (fifth pass), NOT EXECUTED AGAINST A CLUSTER.** The
test exists and compiles; it has never run. Nothing below is runtime-verified —
treat this item as open until someone runs it.

`test/e2e/topology_abandon_test.go`, `TestE2E_RollingUpdate_TopologyRestoreAbandoned`
(namespace `e2e-topology-abandon`, CR `abandon-2r`, 2 replicas, no
Sentinel/TLS/auth/persistence, `spec.rollingUpdate.syncTimeout: 60s`, image
8.0 -> 8.1). Five subtests: Phase 1 gives up and leaves the promoted replica as
master; the rolling update completes with a non-pod-0 master; writes survive on
it; the `-rw` Service selects only it; the cluster converges once pod-0 restarts.

**This item's own sketch was wrong and is corrected here.** The proposed
`blockPodCreation` mechanism cannot reach the abandon path:
`handlePostManualFailover` requeues unbounded on `IsNotFound` /
`DeletionTimestamp` / `podNeedsUpdate` / `!isPodReady`
(`internal/controller/rolling_update.go:2618-2677`) and only then flips to
`restoring-topology` (`:2682`), while `clearStaleRollingUpdateState` (`:465`)
needs `replacedCount == 0` — a permanently blocked pod-0 stalls in
`manual-failover` and never enters Phase 1. The test therefore jams pod-0's
*replication* instead (`jamPod0Replication`: `CONFIG SET masterauth <poison>` plus
`REPLICAOF 240.0.0.1 6379` once the state annotation reads
manual-failover/replacing-master/restoring-topology and pod-0 answers
`role:slave`), so pod-0 comes back Ready but never reports
`master_link_status: up`.

Verified by code reading (not by running): `pod0SyncWaitReason` (`:2716`) stays
non-empty while the link is down; `waitOrAbandonTopologyRestoration` (`:2763`)
arms the bound and `abandonTopologyRestoration` (`:2783`) records the
`TopologyRestoreAbandoned` Event (`:2789`) and the `RestoreTimeout` condition with
the message format the assertion matches (`:2796-2798`);
`clearRollingUpdateState` (`:1909-1939`) deletes state/promoted-pod/
topology-restore-started but **not** known-master, and `promotePod0AndRedirect`
(`:2811`) is the only writer that would move it — hence the known-master
assertion. `syncTimeout: "60s"` is accepted by the CRD (bare `type: string`,
`config/crd/bases/vko.gtrfc.com_valkeys.yaml:586-591`; no admission webhook in the
tree). The jammed pod-0 stays Ready: the probe is a plain PING
(`internal/builder/statefulset.go:1294-1323`) with `replica-serve-stale-data yes`
(`internal/builder/configmap.go:174-176`), and the sidecar `/readyz` is sticky
once a role was seen (`internal/sidecar/health.go:36-42`,
`labeler.go:147-149`).

Also verified while writing it, and narrower than this item feared:
`dispatchMultiReplicaState` routes restoring/verifying-topology before
`replaceNextReplica` (`rolling_update.go:2380-2390`), so
`verifyReplacedReplicasSynced` — the only consumer of `syncTimeout` that can call
`pauseRollingUpdate` — never runs during the restore phases. The shared budget is
two-sided only during the pod-1 replacement.

**What running it needs.** No workflow change: the single-node CI leg runs the
package with an empty `run_filter` (`.github/workflows/release.yml:38-42`) and
picks it up; the multi-node leg's filter and its `--- PASS` guard list
(`:398-406`) were deliberately left untouched, nothing here depends on node count.
Locally: `make kind-create && make kind-load && make e2e-local`, or against an
existing cluster with the operator deployed
`make test-e2e E2E_RUN='TestE2E_RollingUpdate_TopologyRestoreAbandoned'`.
Expect 6-9 min per run. Run it at least three times and check beyond PASS/FAIL:

1. the log line `Jammed replication on abandon-2r-0 while the rolling update was
   in state ...` shows `manual-failover`, not `restoring-topology` — consistently
   late means readiness is faster than assumed;
2. the INFO replication dump at the abandon shows `master_link_status:down` with
   `master_host` naming the pod-1 FQDN — that is the proof the masterauth poison
   bites;
3. no `RollingUpdatePaused` Event in the namespace — that would mean 60 s
   `syncTimeout` is too short for the replica phase.

**The load-bearing unverified assumption** is the masterauth poison: a replica
with `masterauth` set against a master with no `requirepass` must abort the
handshake at AUTH so `master_link_status` never reaches `up`. That follows from
the Valkey/Redis `syncWithMaster` handshake but was not executed. If it does not
bite, the test fails loudly at the `TopologyRestored=False` wait (5 min budget,
prints the last observed condition) — it cannot pass falsely. The documented
fallback is re-issuing `REPLICAOF` to the blackhole on a short interval
(`jamPod0Replication` doc comment).

**Deviations from this item's sketch, all deliberate:** `syncTimeout` 60 s not
30 s (the two failure directions are asymmetric — too short kills the test with a
confusing `RollingUpdatePaused` before the failover, too long only delays the
abandon inside a 5 min poll budget); `wait.PollUntilContextTimeout` at 500 ms
instead of `require.Eventually` at 300 ms (testify runs its condition in a
goroutine that can outlive the test on timeout); in the recovery subtest the wait
for `connected_slaves:1` on pod-1 comes **before** `waitForPodReady(pod-0)`,
because the still-terminating old pod-0 also reports Ready; and the Event budget
is 2 min via NA38's parameterised helper instead of the fixed 60 s
`pdbSettleTimeout`.

No unit tests were added, deliberately: the abandon logic is already pinned by
`internal/controller/topology_restore_stall_test.go`, and a sub-minute
`syncTimeout` unit test would only restate `TestGetSyncTimeout_Custom`.

**Not done, and it was the second half of this item:** the NA26 observation
(un-block pod-0 and assert convergence to one master) is present as the
"converges once pod-0 restarts" subtest but, like the rest of the file, has never
been executed.

**Re-checked 2026-08-21 (seventh pass): status unchanged, still CODE-COMPLETE and
NOT EXECUTED.** The seventh pass was unit-test and mutation work; no e2e target
appears among the gates it ran (`vet`, `lint`, `cyclo`, `test-unit`,
`test-unit-coverage` — see the seventh-pass section), so nothing in it can have
executed this file. `test/e2e/topology_abandon_test.go` appears in commit 34c351c
only because that series was cut at file granularity, not because the test ran.

### NA37 — No guard against RBAC drift between the kubebuilder markers and the Helm ClusterRole — DONE (a live drift was found and fixed)

Split out of NA24 because it survives NA24's documentation fix. The generated
`config/rbac/role.yaml` and the hand-maintained
`deploy/helm/valkey-operator/templates/clusterrole.yaml` are only kept in sync
by convention; nothing in CI compares them. The failure modes are proven, not
hypothetical: NA12 (events silently discarded for every install) was exactly
this drift, and NA24 shows the next occurrence would crashloop the operator
outright (a missing informer-backing rule, not a degraded feature).

Fix sketch: a unit test (or `make lint` step) that parses both files and
asserts every `apiGroups`/`resources`/`verbs` tuple in the generated role is
covered by the chart ClusterRole. The chart template contains no Go-template
actions in the rules block (verified in NA24's pass), so plain YAML parsing
suffices; scope the assertion to a superset check so chart-only additions
stay allowed.

**DONE 2026-08-20 (fifth pass) — and the guard found a live drift on its first
run.**

**The drift:** `deploy/helm/valkey-operator/templates/clusterrole.yaml` lacked
`delete` on core `secrets`, which the marker
(`internal/controller/valkey_controller.go:178`) and the generated
`config/rbac/role.yaml` both carry. Fixed at `clusterrole.yaml:60-72`, with a
four-line comment naming the caller so the verb is not pruned again as "unused".
The full-comparison result the fix sketch asked for: **exactly one missing
triple**, `""/secrets:delete`. Nothing else in the whole role was short.

**Severity, precisely.** The delete removes `<name>-sentinel-tls`, the Secret
cert-manager produced for the legacy standalone Sentinel Certificate
(`internal/builder/certificate.go:41`), and is reached only when *all* of:
`spec.tls.enabled` **and** `spec.tls.certManager != nil` (step gate,
`valkey_controller.go:416`) **and** `spec.tls.unifiedCertificate: true` **and** the
Sentinel STS has finished rolling onto the unified Secret (`:1076`) **and** the
legacy Secret still exists (GET-first guard, `:1057`). That is one scenario: an
existing Sentinel + TLS + cert-manager cluster migrating `unifiedCertificate`
false -> true. A fresh install in unified mode never has the legacy Secret, the GET
returns NotFound and no Delete is attempted — which is why the drift stayed
invisible. Where it does land it is permanent: the Certificate delete succeeds
(that verb *is* in the chart) and the Secret delete 403s on every pass, the error
propagates through `reconcileTLSCertificates` into `runReconcileSteps`, which
joins it — sibling steps still run, but every reconcile of that CR ends in error:
permanent `ReconcileBlocked`, error phase, endless requeue, stale TLS material
left behind. Not a crashloop; a CR that never reaches a clean state again.

Worth recording: the comment at `valkey_controller.go:1035-1038` already reasons
about exactly this ("the apiserver evaluates authz before existence, so a Delete
against a non-existent resource on a cluster without `delete` RBAC returns 403
rather than 404 and would loop the reconciler") and the GET-first pattern was
written for it. Someone understood the failure mode and the verb was still missing
on the install path that matters — the best argument there is for the guard being
a test rather than a comment.

**The guard:** `internal/controller/rbac_drift_test.go`,
`TestHelmClusterRoleCoversGeneratedRole` (`:160`). It parses the top-level `rules:`
block of both files, expands every PolicyRule into `(group, resource, verb)`
triples and asserts **containment** (generated subset of chart), so chart-only
extras such as `coordination.k8s.io/leases` stay legal. Details that make it
degrade loudly rather than silently: `repoRoot()` uses `runtime.Caller(0)` and
asserts `go.mod` is there, so it ignores `go test`'s cwd; only the `rules:` block
is fed to the parser because the chart metadata contains
`{{ include "valkey-operator.fullname" . }}`, and
`require.Falsef(strings.Contains(block, "{{"))` turns a future template action in
that block into a failure instead of a skip; rules restricted by `resourceNames`
or `nonResourceURLs` are excluded from the chart's covered set (counting them
would hide a real gap) and rejected outright on the generated side; wildcards are
not expanded, documented in-file — a `*` causes a loud false failure, never a
silent pass. Parsing uses `k8s.io/apimachinery/pkg/util/yaml`, a **direct**
`go.mod` require, deliberately not `sigs.k8s.io/yaml` (indirect, would be
reclassified by a future `go mod tidy` and produce an unowned `go.mod` diff).
It lives in `internal/controller` because the markers it defends do.

Verified by running, both failure modes provoked rather than assumed: before the
chart fix the test failed with `Missing group/resource:verb triples:
""/secrets:delete`; injecting `{{- if .Values.leaderElection.enabled }}` into the
chart rules block failed with the "contains a Go-template action" message, after
which the file was restored and `git diff` confirmed only the intended five lines
remain. `make test-unit` green (`--- PASS: TestHelmClusterRoleCoversGeneratedRole`).

**`make manifests` produces no diff** in `config/rbac/role.yaml`,
`config/crd/bases` or the chart CRD, run before and after the edit — so the
generated role is in sync with the markers and there is no second live drift.

**Residuals:** the guard compares manifest against manifest, never marker against
manifest, so a stale `config/rbac/role.yaml` would pass — split out as **NA44**.
It reads only the first top-level `rules:` block per file (both files hold one
ClusterRole today; the hook role in `pre-upgrade-rbac.yaml` has its own
ServiceAccount and is correctly out of scope). And: existing installs that already
migrated to `unifiedCertificate` before this fix have a wedged CR and an orphaned
`<name>-sentinel-tls` Secret; a `helm upgrade` grants the verb and the next
reconcile cleans it up, but nothing announces that.

**Not done:** no cluster verification — the 403-loop analysis is read from the
code, not reproduced against a real apiserver.

**Consequence raised separately: [NA49](#na49).** Granting the verb is what makes
the delete site reachable on the Helm install path, and that site deletes a Secret
by name with no ownership check — the rule NA14 and NA31 established for
PodDisruptionBudgets in this same shipment. The RBAC fix here is not in question;
the missing guard on the delete is, and it awaits a decision.

### NA38 — Deduplicate the Valkey-Event poll helpers in the e2e suite — DONE

Carried from the NA14 note, unblocked since `8beaccf`: `waitForValkeyEvent`
(`test/e2e/pdb_test.go`) duplicates the inline Event poll in
`test/e2e/admission_recovery_test.go:278-293` (the "nudge Event is visible"
subtest). Fold both onto one helper next time either file is touched —
test-only, no operator behavior involved.
**DONE 2026-08-20 (fifth pass).** `waitForValkeyEvent`
(`test/e2e/pdb_test.go:413`) now takes a timeout plus the caller's failure message
and args; the 16-line inline Event poll in the "nudge Event is visible on the
Valkey CR" subtest of `admission_recovery_test.go` is replaced by a call to it
(`:284`), with a new `nudgeEventTimeout = 60 * time.Second` documenting the budget
the inline poll had hardcoded. It is now the only Event poll in the suite.

Both original budgets are preserved (`pdbSettleTimeout` at the PDB call site,
60 s at the nudge call site) and the NA12 message survives byte-for-byte: "a
StatefulSetNudged Event must appear on Valkey %s/%s; if it never does, the
operator RBAC is missing create/patch on events.k8s.io". The poll interval was
already 2 s at both call sites, so nothing needed parameterising there.
`require.NoError(t, err, append([]interface{}{failureMsg}, msgArgs...)...)`
reaches testify's `messageFromMsgAndArgs`, which Sprintf-formats with more than
one element.

No new tests — test-only refactor, no operator behavior involved. Both existing
call sites keep their assertions; the NA36 test is the third caller and is what
made the parameterised timeout worth having. Verified by running
`make test-e2e E2E_RUN='TestE2E_CompileCheckOnly'` (package compiles); the e2e
tests themselves were not executed.

**Residual:** the helper stays in `pdb_test.go` rather than moving to
`e2e_test.go`, which was outside the writing agent's ownership. A second, larger
duplication in the same suite was left alone and is tracked as **NA45**.

## Follow-up work from the fifth pass (2026-08-20)

NA24 and NA26-NA38 were implemented in one batch on `feat/support-pdb` (base
`91b9647`) by six parallel agents plus a verification pass. Statuses are recorded
at each item above; this section records the shape of the pass and the findings
that became new items.

### Shape of this pass — what was run versus what was only read

**Verified by running** (Makefile targets only; the two direct `go test -run`
probes used to demonstrate a failure mode are named at NA37):

| Target | Result |
|---|---|
| `make fmt` | exit 0, `git status` byte-identical before and after — no file reformatted |
| `make vet` | exit 0, no diagnostics |
| `make lint` | `0 issues.` (`go vet` + `gofmt -l` + golangci-lint) |
| `make cyclo` | `All functions are below complexity threshold 15` |
| `make test-unit` | exit 0, all 9 packages; re-run uncached (`-count=1`) and repeated (`-count=2`): **0 FAIL, 0 SKIP** |
| `make test-e2e E2E_RUN='TestE2E_CompileCheckOnly'` | `PASS ... [no tests to run]` — the e2e package compiles |
| `make test-integration` | `ok .../test/integration 15.374s`, 0 FAIL (run unprompted: the branch changed the unexported `reconcilePodDisruptionBudget` signature) |
| `make gosec` | 37 files, 14782 lines, 1 nosec, **0 issues** |
| `make generate-all` | exit 0, `git status` byte-identical afterwards — no CRD/DeepCopy/Helm-CRD drift |

Uncached unit timings: `api/v1` 1.1s, `cmd/migrate` 0.8s, `internal/builder`
14.6s, `internal/common` 2.1s, `internal/controller` 6.0s, `internal/health`
4.5s, `internal/observer` 50.2s, `internal/sidecar` 17.6s,
`internal/valkeyclient` 6.6s. Zero SKIPs confirms no `testing.Short()` gate crept
back in. Diff: 14 modified tracked files + 5 new untracked files, ~2400 lines
added, 112 removed; `go.mod`/`go.sum` untouched.

Per-item confidence beyond the gates: every behavioral fix was **revert- or
mutation-verified** — the new test was confirmed to fail against the pre-fix code,
and where a plain revert was impossible (NA26 adds a function that did not exist)
each guard was knocked out individually. The exceptions are stated at the items:
NA34.1 is documentation-only and has no test that can fail pre-fix, and three
tests deliberately pass both before and after because they pin behavior that must
*not* change.

The documentation pass that wrote this section re-ran `make fmt`, `make vet`,
`make lint` (`0 issues.`), `make cyclo`, `make test-unit` (all 9 packages ok,
exit 0) and the e2e compile check after its own edits; it touched only Markdown,
and the results were unchanged.

**Verified by code reading only — no cluster was touched in this pass:**

- NA24's crashloop console output (unchanged from the fourth pass: expected
  client-go/controller-runtime behavior, never reproduced).
- NA37's permanent 403 loop on the `unifiedCertificate` migration — read from the
  code, not reproduced against an apiserver.
- **NA36's entire e2e test: compiled, never executed.** See the item for what
  running it needs and which assumption is load-bearing.
- The upgrade documentation for NA24 is an exception worth naming: `helm template`
  (v3.21.3) was actually executed against the chart and the published `gh-pages`
  chart repository was inspected, so the rendered resources and the released
  package contents are verified facts; no `helm upgrade` was performed on a live
  cluster.
- The init-script tests (NA35) *do* execute the generated shell script, but in the
  test harness, not in a pod.

### The adversarial review of this pass (2026-08-20)

The batch above was re-reviewed adversarially after it was implemented. **17
findings were raised, 9 survived verification.** Seven of the nine are defects
this change introduced; they were fixed in the same round that wrote this section,
each at the item it belongs to (this documentation agent verified and fixed only
its own, finding 9). Two are **pre-existing stalls** that this change neither
caused nor touched — they are recorded as items instead of fixed, because both
need a design decision: **NA47** (`handlePostManualFailover` has no bound on any
wait branch) and **NA40** (the Phase 2 finalization bound is never re-armed on
entry). A third, **NA48**, was found while verifying the documentation finding.
That fix round was itself re-reviewed; the fixes held, and what the recheck found
is recorded in its own section at the end of this document (**NA50**, **NA51**,
**NA52**).

**Three of the nine had one root cause.** Findings 1, 3 and 4 were separate code
sites, but the same defect: the known-master annotation became a *data-plane
authority* in this pass — NA26 demotes **toward** it, NA35 boots a pod as master
**from** it — while its write paths were still the best-effort telemetry writes
they had been when nothing consumed them. The fix is one invariant, now stated at
NA26, at NA35 and in `CLAUDE.md`:

> **A promotion the operator could not record is not a completed promotion.**

Every write that records a promotion is part of the promotion: it retries, and on
failure it fails the pass instead of advancing the state machine
(`persistManualFailoverState`, `recordPromotedMaster`). A future change must not
relax either back to `_ = r.Update(...)`.

**Finding 9 was documentation, and it stated the opposite of the code.** The
"Upgrade the Operator" section this pass added to `README.md` claimed that a
single-pod cluster without persistence "is restarted and its in-memory data is
lost". The code does the reverse: a sidecar-only delta on a single-replica
non-Sentinel cluster is **deferred, never applied** — `handleStandaloneRollingUpdate`
(`internal/controller/rolling_update.go:2276`, guard at `:2301`,
`isSidecarOnlyChange` at `:2347`) sets `SidecarUpdatePending=True` and leaves the
pod running the old sidecar image. An admin following the old text would have scheduled downtime or a
dump-and-restore for nothing, and would never have learned the actual consequence:
the pod keeps the **old** sidecar indefinitely. Corrected in the same pass
(`README.md:64-98` and the condition table row at `:847`, line numbers as of the
recheck's own edits — see NA48(c) and the recheck section for what it changed
there); the restart/data-loss wording at `README.md:460` was re-verified and is
correct — enabling metrics adds
the `exporter` container, which changes the pod-spec hash while the sidecar image
stays current, so `isSidecarOnlyChange` returns false and the pod really is
restarted.

Verified by this documentation pass against the source: finding 9's premise and
the `README.md:460` counter-case (`rolling_update.go:2276-2367`,
`podNeedsUpdate` at `:246`), NA47's six unbounded branches and their entry point,
NA48's clearing path, and NA40's line references (refreshed below). The counts
(17 raised / 9 surviving) and the attribution of findings 1, 3 and 4 are taken
from the review report, not re-derived. No code was run for this section and no
cluster was touched.

### NA39 — `status.masterPod` always reports pod-0 on the non-Sentinel path — DONE

`updateStandaloneStatus` sets
`v.Status.MasterPod = fmt.Sprintf("%s-0", v.Name)` unconditionally on the OK path
(`internal/controller/valkey_controller.go:1411`, with the comment "Pod-0 is the
master (standalone single pod or ordinal-based multi-replica)"). That assumption
is exactly what NA23 and NA35 made false: after any non-Sentinel failover — the
NA23 abandon path, or a manual failover whose topology restoration was given up —
the real master is not pod-0 and the status lies. The Sentinel path is fine, it
uses `clusterState.MasterPod` (`:1492,:1505`).

Not cosmetic: `replicaPodNames` in the e2e suite already consumes
`status["masterPod"]` to pick non-master pods (`test/e2e/pdb_test.go:469`), so a
fix has a second caller, and today that helper can hand back the actual master as
a "replica". Found while writing NA36, which is why that test reads the master via
`INFO replication` and the `-rw` endpoints instead of the status field.

Fix sketch: derive the field from the same signal the `-rw` Service uses (the
`instanceRole=master` label, or the known-master annotation), not from the
ordinal. Check `replicaPodNames` at the same time.


**Status (sixth pass, 2026-08-21): DONE.** `currentMasterPod`
(`internal/controller/valkey_controller.go:1485`), called from the OK path of
`updateStandaloneStatus` (`:1421`), answers in authority order:

1. the `instanceRole=master` label when **exactly one** pod carries it — chosen
   first because it is the literal selector of the `-rw` Service, so it is not a
   guess at the master, it *is* the pod receiving writes;
2. the known-master annotation when zero or several pods are labeled — the
   operator's own record, and what the replica ConfigMap is built from, so it is
   right while labels are in flux;
3. pod-0, correct for a single pod and for a cluster that never failed over.

`replicas <= 1` returns pod-0 without reading anything, so the (cache-served)
List is only paid by multi-replica clusters. Two labeled masters deliberately
does **not** pick a winner — that state belongs to `checkSteadyStateSplitBrain`.

Revert-verified: reinstating the hardcoded pod-0 fails
`TestUpdateStatus_ReportsTheLabeledMasterInsteadOfPodZero` (`:41`),
`TestUpdateStatus_FallsBackToTheKnownMasterRecord` (`:58`) and
`TestUpdateStatus_DoesNotPickAWinnerBetweenTwoLabeledMasters` (`:74`)
(`internal/controller/status_master_pod_test.go`). Coverage of the new function:
90.9% — re-measured 2026-08-21 against `coverage/unit.out`, still exact.
(The three names were written with a `TestUpdateStandaloneStatus_` prefix until
2026-08-21; no such test exists. Corrected to the names in the tree.)

**Second caller, checked and left alone:** `replicaPodNames`
(`test/e2e/pdb_test.go:469`) consumes `status.masterPod` and only becomes more
correct with this change — it needs no edit. Not re-run against a cluster in this
pass.

### NA40 — the Phase 2 finalization bound is never re-armed on entry (NA28's sibling) — DONE

**Re-verified 2026-08-20 after the fix round; still accurate, line references
refreshed** (the file shifted by ~70-85 lines when the review fixes landed) and the
annotation name corrected — it is `vko.gtrfc.com/finalization-started`
(`internal/controller/rolling_update.go:64`), not `finalization-timestamp`.

Entering `stateVerifyingTopology` — from `abandonTopologyRestoration`
(`internal/controller/rolling_update.go:2863`) and from `promotePod0AndRedirect`
(`:2948`) — does not re-arm `vko.gtrfc.com/finalization-started`;
`ensureFinalizationTimestamp` (`:833`) only fills a gap, and Phase 2 calls it at
`:2970` and `:3008`. A rolling update that died mid-update leaves that annotation
behind under exactly the NA28 conditions — `clearRollingUpdateState` (`:1977`)
does delete it, but `clearStaleRollingUpdateState` (`:487`) only reaches that
delete on the `replacedCount == 0` branch, and an operator killed mid-update
reaches neither — so the next update's Phase 2 sees an hours-old timestamp,
reports stalled on its first pass, and completes the rolling update unverified —
**without consolidating rogue masters**, which is the one job Phase 2 has on the
abandoned path.

Fix is the mirror image of NA28: an `armFinalizationBound` at both
`stateVerifyingTopology` entries. Not done in the fifth pass because NA28's
recorded decision names only the `restoring-topology` entry. Found while
implementing NA27/NA28; code-read only.

**Same family as NA47** — both are a bound that is missing or unarmed on a
non-Sentinel rolling-update state, and both end in a state machine that requeues
forever or gives up without doing the one thing that state exists for. Decide them
together: NA47 needs an escape target, and `stateVerifyingTopology` is the natural
one, which only works if its own bound is armed on entry. NA47 without NA40 hands
a stalled manual failover to a Phase 2 that may declare itself stalled on its
first pass.


**Status (sixth pass, 2026-08-21): DONE.** `armFinalizationBound`
(`internal/controller/rolling_update.go:909`) is now called at both
`stateVerifyingTopology` entries — `abandonTopologyRestoration` (`:3021`) and
`promotePod0AndRedirect` (`:3121`) — each immediately before the state write, so a
single `Update` persists both.

The arming body was extracted to `armWaitBound(v, annotation, bound)` (`:881`) and
`armTopologyRestoreBound` (`:895`) now delegates to it, so the three entries share
one implementation instead of three copies.

Revert-verified: removing both calls fails
`TestAbandonTopologyRestoration_ReArmsPhase2BudgetOnEntry`,
`TestPromotePod0AndRedirect_ArmsPhase2BudgetOnEntry` and
`TestHandlePostManualFailover_AbandonsIntoPhase2WhenPodZeroNeverReturns`.

### NA41 — a CR deleted mid-pass now survives further than it used to (NA33 residual) — CLOSED without change (2026-08-21), argument re-checked

`writePhase` and `setStatusCondition` both refresh the caller's object in place, so
a CR deleted mid-pass yields NotFound on that Get. `setStatusCondition` already
special-cases NotFound and returns silently; `writePhase`
(`internal/controller/valkey_controller.go:1610`) does not — and since NA33 the
`Reconcile` pass continues past it instead of returning. `reconcileResources` then
creates children carrying an ownerRef to a gone UID, which GC collects
immediately.

Harmless in practice, and pre-existing in kind elsewhere in the pass, but it is
the one behavior NA33 changed that NA33's text does not mention. Deliberately not
guarded there under the minimum-code rule: no other mid-pass write in this
reconciler special-cases NotFound either, so this is a decision about the whole
pass, not about one call site.


**Status (sixth pass, 2026-08-21): closed without a code change; the argument was
re-checked against everything that landed since, and it holds.**

What was re-verified, by reading:

- `writePhase` (`internal/controller/valkey_controller.go:1667`) still refreshes
  in place and still returns the NotFound; the caller at `:223` logs it and
  continues, which is the NA33 behaviour.
- Every object `reconcileResources` creates carries an ownerReference to the CR —
  including the two `unstructured` ones, which set it explicitly
  (`ServiceMonitorOwnerRef` at `:504-509`, `CertificateOwnerRef` at `:1161-1166`);
  the rest go through `controllerutil.SetControllerReference`. So a child created
  after the CR is gone is collected by GC, in the same namespace, with no orphan.
- The **new** mid-pass writers this branch added do not change the picture.
  `recordPromotedMaster`, `adoptMaster` and `persistManualFailoverState` all
  return the NotFound instead of swallowing it (the NA26/NA35 invariant), so the
  pass fails visibly rather than acting on a record it could not write. The
  destructive action the pass can now take — a `REPLICAOF` demotion from
  `checkSteadyStateSplitBrain` — is aimed at pods whose StatefulSet is being
  deleted along with the CR, so there is no dataset left for it to lose.
- The loop terminates: the next reconcile Gets NotFound at the top of `Reconcile`
  (`:190-198`), forgets the nudge state and returns cleanly.

Why it stays unguarded, unchanged from the original reasoning: **no** mid-pass
write in this reconciler special-cases NotFound, and adding the check to
`writePhase` alone would make the pass inconsistent rather than correct. If this
is ever fixed it should be one guard for the whole pass — re-read the CR once
after `reconcileResources` and return on NotFound — not a per-call-site
`IsNotFound`. Cost of the status quo, measured in what it can actually do:
a handful of API writes that GC undoes within seconds.

Not verified: nothing was reproduced against a cluster, and no test was added —
there is no behaviour change to pin.

### NA42 — the UID delete precondition (NA31) is not observable in a unit test — DONE (envtest integration test)

The controller-runtime v0.24.1 fake client enforces only the ResourceVersion
delete precondition, never the UID one
(`sigs.k8s.io/controller-runtime@v0.24.1/pkg/client/fake/client.go:705-724`). The
NA31 tests can therefore assert that the option is *sent* and inject the resulting
Conflict, but they cannot show the API server rejecting the delete. Real
enforcement needs an envtest or e2e case: create an owned budget, delete it out of
band, recreate a foreign one under the same name, and assert the operator's
delete fails and the foreign object survives.

Verified by reading the vendored fake client, not by writing that test.


**Status (sixth pass, 2026-08-21): DONE.**
`test/integration/pdb_uid_precondition_test.go`
(`TestPodDisruptionBudgetUIDPrecondition_Integration`, build tag `integration`,
runs under `make test-integration`) proves against a **real API server** what the
fake client cannot express:

1. a PDB is created and its UID captured — the object the operator would have
   inspected;
2. it is deleted and a different object takes the name over — the user's own
   budget in the NA31 scenario;
3. `Delete(ctx, obj, client.Preconditions{UID: &staleUID})` — the exact option
   `internal/controller/pdb.go:265` sends — is **rejected with 409 Conflict** and
   the foreign object survives with its own UID;
4. the same delete with the matching UID succeeds, so the guard does not block the
   legitimate cleanup.

The test uses a direct (uncached) client built from `testEnv.Config`, not the
manager's cache-backed one, so no informer lag can make it flaky.

**Negative control, actually run:** dropping the `client.Preconditions` option
from step 3 makes both subtests FAIL (the foreign budget is deleted). The
assertion is therefore not vacuous.

**What it deliberately does not prove, recorded so nobody reads more into it:** it
does not schedule the interleaving itself. The race the precondition guards is a
cache-backed Get followed by a Delete after the name was reused; reproducing that
against a running controller means winning a race with informer delivery, which
is a coin flip, not a test. The two halves are covered separately — the unit tests
pin that the operator *sends* the inspected object's UID, this test pins that an
API server *honours* it.

### NA43 — the PDB not-owned documentation overstates after the NA32 gate — DONE (2026-08-21)

**Status (tenth pass, 2026-08-21): the three remaining sites are fixed.** Both
`api/v1/valkey_types.go` doc comments (the `PodDisruptionBudgetSpec` struct comment and the
`Enabled` field comment) and `deploy/helm/valkey-operator/values.yaml` now carry the
qualifier "while `spec.podDisruptionBudget.enabled` is true". The two generated CRD copies
(`config/crd/bases/vko.gtrfc.com_valkeys.yaml`,
`deploy/helm/valkey-operator/templates/crd.yaml`) were regenerated via `make generate-all`,
not hand-edited. Verified: `make test-unit` (12/12 packages), `make lint` (0 issues),
`make vet` — all green; the generated tree is clean apart from the intended edits.

NA32(a) made the `PodDisruptionBudgetNotOwned` Event conditional on
`spec.podDisruptionBudget.enabled`. Four places still say it is recorded on every
reconcile unconditionally:

- `api/v1/valkey_types.go:309-314` and `:328-330` (the field doc comments, i.e.
  the source of truth),
- the generated copies in `config/crd/bases/vko.gtrfc.com_valkeys.yaml:494` and
  `deploy/helm/valkey-operator/templates/crd.yaml:496` — these follow from the
  types via `make generate-all`, they are not edited by hand,
- `deploy/helm/valkey-operator/values.yaml:71`.

Each needs the qualifier "while `spec.podDisruptionBudget.enabled` is true".
`README.md:637-647` was corrected in this pass. The e2e suite is unaffected:
`test/e2e/pdb_test.go:167` waits for the Event only after patching
`podDisruptionBudget.enabled=true`, and the feature-off subtest asserts
intactness only.


**Status (sixth pass, 2026-08-21): README done and re-verified; the three
remaining sites are untouched and this item stays open for them.**

Re-verified against the code, not just against the earlier text: the reconcile
path warns at `internal/controller/pdb.go:192` and is only reachable when the
feature is enabled, and the cleanup path gates explicitly on
`v.IsPodDisruptionBudgetEnabled()` (`:242-247`). The README paragraph the fifth
pass corrected is accurate. It gained the one case it did not state — the Event
**is** recorded while the feature is on but not applicable to that StatefulSet
(fewer than two replicas, or Sentinel disabled), because the name is taken and
scaling back up would silently produce no budget at all, which is exactly the
reasoning in the `cleanupPodDisruptionBudget` doc comment.

**Still overstating, deliberately not touched in this pass** (the pass owned
documentation, tests and CI; `api/v1` is production Go and the two CRD copies are
generated from it, so the fix is one edit plus `make generate-all`):

- `api/v1/valkey_types.go:309-314` — "reported as a PodDisruptionBudgetNotOwned
  Warning Event on the CR" — needs "while `spec.podDisruptionBudget.enabled` is
  true".
- `api/v1/valkey_types.go:328-330` — "records a PodDisruptionBudgetNotOwned Event
  on every reconcile while that holds" — same qualifier.
- `deploy/helm/valkey-operator/values.yaml:71` — same claim, same qualifier.
- `config/crd/bases/vko.gtrfc.com_valkeys.yaml` and
  `deploy/helm/valkey-operator/templates/crd.yaml` follow automatically once the
  types are fixed; they must not be hand-edited.

### NA44 — nothing in CI proves the generated manifests are current — DONE

NA37's guard compares manifest against manifest (generated role vs chart
ClusterRole), never marker against manifest, so a stale `config/rbac/role.yaml`
passes it silently. `make manifests` was run by hand in this pass and produced no
diff, so there is no drift *today*.

Note the partial overlap: `.github/workflows/build.yml` already runs
`make generate-all` and fails on a dirty tree, in its `release-helm-gh` job —
but that workflow triggers on `release: published` only. The workflow that runs
on pushes and PRs to `main` (`release.yml`) has no such step, so drift is caught
at release time, after review. A `make generate-all && git diff --exit-code
config/ deploy/` step in `release.yml` would close the gap where it costs least.


**Status (sixth pass, 2026-08-21): DONE.**

What the existing coverage actually was, checked before adding anything:
`.github/workflows/build.yml:159-170` runs `make generate-all` and fails on a
dirty tree — but inside the `release-helm-gh` job of a workflow that triggers on
`release: published` **only**. Nothing on the push/PR path ran the generator.
`make generate-all` = `manifests` + `generate` + `sync-helm-crd`, and `manifests`
runs `controller-gen rbac:roleName=valkey-operator-role crd`, so it covers
`config/rbac/role.yaml` as well as the CRDs — the marker-to-manifest comparison
NA37's drift test cannot make.

Added: job **`generated-manifests`** ("Generated Manifests Up To Date") in
`.github/workflows/release.yml`, which triggers on push and pull_request to
`main`. It checks out, sets up Go, runs `make generate-all`, and fails when
`git diff` is non-empty **or** `git status --porcelain` reports an untracked file
(a new API type produces a new CRD file, which `git diff` alone would miss). The
job is also added to `semantic-release`'s `needs:` list, so drift blocks the
release rather than only reddening the run. No duplicate: `build.yml` keeps its
own copy for the release path, which is a different trigger.

**Verified:** `make generate-all` was run locally in this pass and left the tree
byte-identical (`git status` unchanged), so the job's premise holds today; the
edited workflow parses as YAML (`yaml.safe_load`) and the job list and `needs`
were read back after the edit. **Not verified, and it cannot be from here:** the
job has never executed on a runner. The first push to `main` or the first PR is
its first real run.

### NA45 — test-hygiene bundle from the fifth pass — DONE (items 2 and 4 closed in the ninth pass)

**Status (ninth pass, 2026-08-21): all four items done.** Item 2: the two checks whose
arming write discarded its error are converted as defects (NA56 — `isSentinelAwarenessStalled`
and `isSyncWaitTimedOut` now go through `waitBoundExceeded`); the remaining three
(`isFailoverTimedOut`, `isReplicaReconnectTimedOut`, `hasMinWaitElapsed`) fold into the new
`annotationTimestampExceeded` helper, which `waitBoundExceeded` itself also uses, so the
RFC3339 parse-and-compare pattern now exists exactly once. One deliberate edge-semantics
change: `hasMinWaitElapsed` compared with `>=` and now inherits the helper's `>` — the two
differ only when `time.Since(ts)` equals `failoverResetMinWait` to the nanosecond,
practically unreachable. Item 4: the `nudges` field comment in `valkey_controller.go` now
names both key sets (grace periods and wait bounds) and points at the `nudgeTracker` type
doc.

Four items, all test-only or comment-only, none breaking anything:

1. **The e2e condition helpers are duplicated again.**
   `valkeyStatusCondition` / `waitForValkeyCondition`
   (`test/e2e/topology_abandon_test.go:296,322`) are a strict generalisation of
   `reconcileBlockedCondition` / `waitForReconcileBlocked`
   (`test/e2e/admission_recovery_test.go:335,361`) — identical body, identical
   `unstructured.NestedSlice(cr.Object, "status", "conditions")` walk; the older
   pair just hard-codes the type. This is the exact shape NA38 just closed for the
   Event helpers, reintroduced one file over, and it was left knowingly (the NA36
   agent's mandate on `admission_recovery_test.go` was NA38 only). The fold is
   mechanical: delete the two older functions and repoint their four call sites at
   `waitForValkeyCondition(..., "ReconcileBlocked", ...)`.
2. **Five inline RFC3339 stall checks remain** in
   `internal/controller/rolling_update.go` — line numbers refreshed 2026-08-21:
   `:996` `isSentinelAwarenessStalled`, `:1495` `isSyncWaitTimedOut`, `:2136`
   `isFailoverTimedOut`, `:2156` `isReplicaReconnectTimedOut`, `:2174`
   `hasMinWaitElapsed` — that NA27's `waitBoundExceeded` could now absorb.
   Pre-existing pattern, correctly out of scope for NA27 under minimum-code — but
   the helper now exists.
3. **Three near-identical "read the CR back from the fake client" test helpers**,
   one per agent: `crGet` (`rolling_update_bounds_test.go:44`), `storedValkey`
   (`status_phase_test.go:80`) and the inline read in `conditionOf`
   (`condition_generation_test.go:29`).
4. **A now-too-narrow field comment.** `nudges tracks how long each StatefulSet
   has been short of pods` (`internal/controller/valkey_controller.go:76-77`) —
   since NA27 the same tracker also carries the rolling-update wait bounds, under
   `"<cr-name>/<bound>"` keys. The `nudgeTracker` type doc in `nudge.go` already
   describes both uses; only the field comment lags. One line.


**Status (sixth pass, 2026-08-21): items 1 and 3 done, items 2 and 4 not touched.**

1. **DONE — the e2e condition helpers are one helper again.**
   `reconcileBlockedCondition` and `waitForReconcileBlocked` are deleted from
   `test/e2e/admission_recovery_test.go`; its four call sites now use
   `tc.waitForValkeyCondition(t, ns, name, "ReconcileBlocked", ...)` from
   `test/e2e/topology_abandon_test.go`. Behaviour is identical: the deleted pair
   polled every 2 s and the survivor uses `pollInterval`, which is
   `2 * time.Second` (`test/e2e/e2e_test.go:44`). The comment on
   `valkeyStatusCondition` that announced the duplication was replaced by one that
   records the fold. Verified with `gofmt -l`, `go vet -tags=e2e ./test/e2e/` and
   the e2e compile check; the suite itself was not executed (no cluster).
3. **DONE — one CR reader instead of three.** `storedValkey`
   (`status_phase_test.go`) and the inline read inside `conditionOf`
   (`condition_generation_test.go`) are gone; both now use `crGet`
   (`rolling_update_bounds_test.go:48`; "43 call sites before the fold, 59 after"
   is a **sixth-pass snapshot**, not a current count — the seventh pass's new tests
   took it past 110, and the `:44` originally cited here is a comment line, the
   function starts at `:48`), whose doc comment now states that it is the package's only CR reader and why
   the namespace is not a parameter (every fixture lives in `testNamespace`).
2. **NOT DONE — the five inline RFC3339 stall checks** in
   `internal/controller/rolling_update.go` (`isSentinelAwarenessStalled` and four
   others) that `waitBoundExceeded` could absorb. Production code; this pass owned
   documentation, tests and CI. Unchanged and still worth doing.
   **Corrected 2026-08-21: one of the five is not hygiene.** This entry filed all
   five as a readability fold. `isSentinelAwarenessStalled` is a live stall —
   its arming write discards its error and there is no in-memory fallback, so a CR
   whose writes keep failing never arms the bound and the Sentinel rolling update
   requeues forever. That half is now tracked as **NA56** and must be fixed as a
   defect, not folded as a duplicate; the other four remain hygiene.
4. **NOT DONE — the `nudges` field comment**
   (`internal/controller/valkey_controller.go:76-77`) still says the tracker only
   holds "how long each StatefulSet has been short of pods", while since NA27 it
   also carries the rolling-update wait bounds under `"<cr-name>/<bound>"` keys —
   and since NA47 a third bound. One line, in a production file, same ownership
   reason.

### NA46 — the operator's privilege footprint is documented nowhere — DONE

The repo has no `DEVELOPER.md` and no `SECURITY_ARCHITECTURE.md`, and the README
documents no RBAC verbs, so the operator's permission set exists only in the
kubebuilder markers, the generated role and the chart ClusterRole. That was
tolerable while the two manifests were the whole story; after NA37 the drift guard
is the only place the permission set is stated twice and checked. Surfaced by the
NA37 agent against this repo's documentation standard (three-file layout); no work
was done on it. [NA49](#na49) is the first concrete cost of that gap: a
verb was added for one narrow caller and nothing in the repo states what the
cluster-wide grant now permits.


**Status (sixth pass, 2026-08-21): DONE.**
[`SECURITY_ARCHITECTURE.md`](SECURITY_ARCHITECTURE.md) was written to the repo
documentation standard and is linked from a new **Documentation** table in
`README.md`. Sections: roles and trust boundaries (table plus ascii diagram),
data and secret flow, isolation and what it does *not* defend against, the
privilege footprint rule by rule, the validation story, rotation and change
propagation, vulnerability reporting, and a hardening checklist.

Every rule was read out of `config/rbac/role.yaml`,
`deploy/helm/valkey-operator/templates/clusterrole.yaml`,
`internal/builder/rbac.go` and the kubebuilder markers
(`internal/controller/valkey_controller.go:167-184`) — not out of intent. Findings
worth repeating here because they are consequences, not restatements:

- **`roles: escalate` + `rolebindings: create` + `serviceaccounts: create`,
  cluster-wide, is namespaced admin in every namespace.** `escalate` lifts the
  rule that a principal may only grant what it holds. The operator needs *some*
  form of this to create the per-instance sidecar Role; whether it still needs
  `escalate` now that the sidecar Role is a strict subset of its own `pods` grant
  is **not verified** and is on the checklist.
- `secrets: get,list,watch` cluster-wide is the heaviest confidentiality exposure
  and predates every item in this ticket; NA49 adds `delete` on top of it.
- The chart ClusterRole grants `coordination.k8s.io/leases`, which the generated
  role does not. Legal: NA37's guard checks containment, not equality, and no
  controller reconciles Leases.
- **Corrections to assumptions made while writing it:** Sentinel pods run under
  the namespace `default` ServiceAccount (`internal/builder/sentinel.go:368`), not
  the sidecar one; the observer runs under the sidecar ServiceAccount
  (`internal/builder/observer.go:113`) while making no API call at all; and no
  workload pod the operator creates has any `securityContext` (verified by the
  absence of the field in `internal/builder`), while the operator's own Deployment
  sets `runAsNonRoot`, `readOnlyRootFilesystem`, `drop: [ALL]` and
  `seccompProfile: RuntimeDefault`.

Two items were filed out of the same reading: **NA54** (the sidecar Role) and
**NA55** (the metrics endpoint). Nothing in the document was reproduced against a
cluster; it is a reading of this repository at this commit.

### NA47 — `handlePostManualFailover` has no bound on any wait branch: the non-Sentinel rolling update parks in `manual-failover` forever — DONE (option (a))

**Pre-existing, not introduced by the NA24/NA26-NA38 change.** Confirmed by the
fifth-pass adversarial review; recorded as an item rather than fixed because the
escape needs a design decision (see NA40, same family).

Once `handleManualFailover` has promoted a replica and deleted pod-0
(`internal/controller/rolling_update.go:2497`, promote at `:2518`, state persisted
at `:2530`, delete at `:2551`), the state machine sits in `stateManualFailover`
and every subsequent pass lands in `handlePostManualFailover` (`:2671`, dispatched
at `:2451`). **Every wait branch of that function returns `NeedsRequeue` with no
bound:**

| Guard | Requeue | Branch | Waits for |
|---|---|---|---|
| `:2688` | `:2690` | `IsNotFound` on pod-0 | the StatefulSet to recreate it |
| `:2696` | `:2698` | `DeletionTimestamp != nil` | the old pod to finish terminating |
| `:2713` | `:2717` | `podNeedsUpdate` against the live template | the pod to come back on the new template |
| `:2720` | `:2722` | `!isPodReady` | readiness |
| `:2727` | `:2729` | `buildTLSConfig` failed | the TLS Secret |
| `:2741` | `:2743` | `REPLICAOF` to pod-0 failed | the pod to answer |

None of them consults a timestamp, a `waitBoundExceeded` (`:809`) or a tracker.
Only the success path advances (`armTopologyRestoreBound` + `setRollingUpdateState`,
`:2751-2752`), and only Phase 1 and Phase 2 past it are bounded (NA23, NA28).

**Reachable, and not exotically:** a PVC that cannot bind; `ImagePullBackOff` on
the new tag; a fail-closed admission webhook rejecting the pod CREATE — the
incident this whole ticket started from; the operator killed between the promote
and the delete; or the pod `Delete` at `:2551` itself rejected, which returns an
error but leaves the state annotation already written by `:2530`.
`clearStaleRollingUpdateState` (`:487`) cannot rescue any of them: it clears only
when `replacedCount == 0`, and the replicas were replaced before the failover.

**Consequences, all four at once:**

1. The cluster serves from the *temporary* promoted master indefinitely — a
   supported topology, but never declared as the end state.
2. `TopologyRestored` is never written, so nothing records that the topology is
   non-canonical.
3. The phase freezes at `Rolling Update N/M` (last written at `:2434-2435`).
4. **The rest of the reconcile is never reached.** `Reconcile` returns on
   `NeedsRequeue` (`internal/controller/valkey_controller.go:290`), *before*
   `handlePostRollingUpdateChecks` (`:295`) and `updateStatus` (`:300`) — so the
   NA26 steady-state split-brain check does not run either, for as long as the
   stall lasts.

**This is the hole NA36's e2e spec had to work around.** That item's original
sketch — block pod-0's CREATE and wait for the abandon path — is unreachable for
exactly this reason, which NA36 records in its "this item's own sketch was wrong"
paragraph; the test jams pod-0's *replication* instead so that pod-0 comes back
Ready and the state machine reaches Phase 1, where a bound exists. Fixing NA47
would make the original, simpler fixture viable.

**NA30 interaction, and it is a trade, not a regression.** Since NA30 the `:2713`
guard compares the full template (`podNeedsUpdate`) rather than the image alone.
A config-only or resources-only rolling update whose pod-0 `Delete` never took
effect therefore now stalls here too, where the old image-only guard would have
proceeded — wrongly, which is precisely why NA30 changed it: proceeding meant
sending `REPLICAOF` to the old, about-to-die pod. The stall is the correct
behavior of the guard. The missing bound is the gap.

**Fix options.**

- **(a) Arm a bound at the `stateManualFailover` entry and hand over to
  `stateVerifyingTopology` on expiry.** Mirror of NA28: an `armManualFailoverBound`
  next to the state write in `persistManualFailoverState` (`:2578`), a
  `waitBoundExceeded` check at the top of `handlePostManualFailover`, and on expiry
  an abandon that behaves exactly like `abandonTopologyRestoration` (`:2853`) —
  record an Event and `TopologyRestored=False`, then set `stateVerifyingTopology`
  so Phase 2 still runs its split-brain consolidation. Cost: one more annotation
  and one more state transition to reason about, and it depends on **NA40** — Phase
  2 must re-arm its own bound on entry, or the handover lands in a Phase 2 that
  declares itself stalled on its first pass and completes unverified. Preferred.
- **(b) Arm the same bound, but expire into `pauseRollingUpdate` (`:1362`).** Cheaper
  and louder: `RollingUpdatePaused=True`, a clear operator signal, resumes on the
  next spec change. Cost: it does **not** consolidate masters, so a split brain
  that opened during the failover stays open until an admin acts — and it leaves
  the CR in a state a GitOps loop will not clear by itself.
- **(c) Status quo.** Documented residual. Cost: the four consequences above,
  unbounded, with the NA26 check suppressed for the whole duration.

**Explicitly not an option: clearing the state on expiry.** Once
`annotationRollingUpdateState` is empty, `checkAndHandleRollingUpdate` early-returns
whenever no pod needs an update (`:180-190`) and nothing calls
`detectAndResolveSplitBrain` again — the exact reasoning NA23 recorded for Phase 1,
one state earlier. An expiry that clears the state is a promoted master, a
half-updated pod set and no consolidation pass left.


**Status (sixth pass, 2026-08-21): DONE — option (a), the recommended one.**

New annotation `vko.gtrfc.com/manual-failover-started`
(`internal/controller/rolling_update.go:87`) and bound `manual-failover` (`:773`).
`armManualFailoverBound` (`:917`) arms it inside `persistManualFailoverState`, at
the state write: the stamp is computed **once**, before the first attempt, so the
conflict retry re-applies the same deadline instead of granting a fresh budget per
attempt.

All six wait branches now return `waitOrAbandonManualFailover` (`:2901`), which
`ensureWaitBound`s first (covering a state written by an older operator, or an
annotation write that never landed), requeues while inside
`v.GetSyncTimeout()`, and on expiry calls `abandonTopologyRestoration` — so the
handover lands in **Phase 2** with an Event, `TopologyRestored=False` and, thanks
to NA40, a freshly armed Phase 2 budget. Clearing the state was never an option
and is not done. The annotation is deleted in `clearRollingUpdateState` and the
bound is added to `forgetWaitBounds`.

Reusing `abandonTopologyRestoration` rather than adding a second abandon is a
deliberate choice: the Event then reads "Topology restoration abandoned after 5m0s
(test-0 was never recreated after the failover); test-1 stays master", which is
accurate for a manual-failover stall — the topology genuinely was not restored —
and it buys the condition, the Phase 2 handover and the NA40 arming for free. A
distinct reason string (`MasterNeverReturned` instead of `RestoreTimeout`) is a
one-line change if it is ever wanted.

Revert-verified: unbounding the waits again fails
`TestHandlePostManualFailover_AbandonsIntoPhase2WhenPodZeroNeverReturns`,
`_ArmsTheBoundAndKeepsWaiting` and
`TestPersistManualFailoverState_ArmsTheBoundOnEntry`.
`TestClearRollingUpdateState_ForgetsTheManualFailoverBound` is hygiene, not a
regression test, and is honest about it: pre-fix the annotation it asserts on
cannot exist.

**Residual, stated rather than hidden.** After the handover Phase 2 consolidates
and completes, the state clears — and if pod-0 *still* cannot be created, the next
pass re-enters the rolling update at `replaceNextReplica` ("waiting for pod to be
recreated") and requeues. Consequence 4 of this item (the pass tail, hence the
NA26 steady-state check, is skipped) therefore returns for that residual case.
What the fix buys is that the one pass which can consolidate masters and record a
non-canonical topology now happens instead of never. Closing the rest means
bounding the outer loop, which is a different item.

### NA48 — `SidecarUpdatePending` is set but never cleared — DONE (option (b), with one addition)

Found while verifying the fifth-pass adversarial review's finding 9 (the README
upgrade paragraph); **pre-existing**, cosmetic in effect, but it makes the one
condition that reports deferred work permanently untrustworthy.

`setSidecarUpdatePendingCondition` has exactly one production call site
(`internal/controller/rolling_update.go:2433`, refreshed 2026-08-21 from `:2330`;
still exactly one, at the end of
`handleStandaloneRollingUpdate`), so that line is also the only place that can ever
pass `false`. And that function is only reached from
`checkAndHandleRollingUpdate` when `needsRollingUpdate` is true or a rolling-update
state annotation is set (`:180-192`). Both computations use the same
`podNeedsUpdate` call with the same four sts-derived inputs (`:174` and `:2297`),
and the standalone path never writes a state annotation. So the moment the
deferred sidecar update actually applies — the pod is deleted, recreated on the
current template — `needsRollingUpdate` goes false, the pass returns at `:190`, and
the clearing call is unreachable. The condition stays `True` with
`reason: SidecarImageDrift` forever.

Failure scenario: an admin deletes the pod to pick up a new operator sidecar, the
pod comes back current, and `kubectl describe valkey` still reports
`SidecarUpdatePending=True` — indistinguishable from a cluster that never applied
it. Any automation keyed on the condition (a fleet dashboard, a GitOps health
assertion) reports permanent drift on a converged cluster.

Fix options:

- **(a) Clear it from a path that runs in steady state** — e.g. in
  `handlePostRollingUpdateChecks` next to the NA26 check, or in `updateStatus`.
  Cost: a per-pass condition evaluation on the healthy path; the existing no-change
  skip in `setStatusCondition` keeps it from writing (that skip exists for exactly
  this caller, see the NA17/NA34.2 notes), so the cost is a comparison, not an API
  call.
- **(b) Clear it in `checkAndHandleRollingUpdate` right before the
  `needsRollingUpdate == false` early return** (`:190`). One line, at the only place
  that provably knows every pod matches the template. Cost: puts a status write in a
  function that otherwise only reports.
- **(c) Status quo, documented.** Cost: the condition is not a signal; the README
  says so at `:89-94` and in the condition table row at `:847`, which is honest but
  not a fix. Both places were re-worded by the recheck: they used to assert the
  condition is "only ever set, never cleared", which is too absolute — the `False`
  branch (inside `setSidecarUpdatePendingCondition`, `valkey_controller.go:1740`;
  refreshed 2026-08-21 from `:1703-1707`) is unreachable on the
  pending-to-resolved transition for the reason this item gives, but it *is*
  reachable on the "all pods updated but rolling-update state still present" branch
  (`rolling_update.go:189-192`), which the standalone dispatch can hit after a
  scale-down from a multi-replica cluster. The reachability argument replaced the
  absolute claim; the user-facing advice (read the running image, not the
  condition) is unchanged.

Nothing here is urgent — no data and no availability depends on it — but option (b)
is a one-line change and the condition is currently worse than absent.


**Status (sixth pass, 2026-08-21): DONE — option (b), plus a guard the item did
not mention.**

`clearSidecarUpdatePending` (`internal/controller/valkey_controller.go:1775`) is
called from `checkAndHandleRollingUpdate` immediately before the
`needsRollingUpdate == false && state == ""` early return
(`internal/controller/rolling_update.go:204`) — the only place that provably knows
every pod matches the live template.

**The addition is a `meta.FindStatusCondition` presence guard.**
`meta.SetStatusCondition` *adds* an absent condition and reports a change, so an
unguarded call would write `SidecarUpdatePending=False` onto every CR in the fleet
on the first upgraded pass — an upgrade changing the status of every existing
cluster, which the repo's upgrade-neutral rule forbids. With the guard the healthy
path costs one map lookup and only a genuine pending-to-resolved transition
writes.

Revert-verified: removing the clearing call fails
`TestCheckAndHandleRollingUpdate_ClearsSidecarUpdatePendingOnceApplied`. The two
blast-radius guards (`_DoesNotAddSidecarConditionToACleanCluster`,
`_KeepsThePendingConditionWhileWorkRemains`) pass in both directions by
construction and are honest about that.

The README wording from option (c) can now be reduced to the reachability
statement it always rested on; it was left as it stands in this pass, since it is
no longer wrong — the condition does clear.

### NA49 — the legacy Sentinel TLS Secret is deleted by name, and NA37 made that reachable on the canonical install path — DONE (2026-08-21)

Raised by the orchestrator of the fifth pass, not by an item above. It is a
consequence of NA37, so it belongs to this pass even though the code it concerns
is older than the branch.

**What NA37 changed.** The Helm ClusterRole gained `delete` on core `secrets`
(`deploy/helm/valkey-operator/templates/clusterrole.yaml:69`), because the
kubebuilder marker and the generated role always had it
(`internal/controller/valkey_controller.go:178`, `config/rbac/role.yaml`) and the
chart did not — so `reconcileLegacySentinelCertificateCleanup` returned 403 on
every pass for the clusters that need it. Without the fix that migration is
broken; the fix itself is not in question.

**What the added verb makes reachable.** The binding is a ClusterRoleBinding
(`deploy/helm/valkey-operator/templates/clusterrolebinding.yaml`), so the grant is
cluster-wide, and the delete site decides purely on a name:

```go
// internal/controller/valkey_controller.go:1068-1071
secret := &corev1.Secret{}
if err := r.Get(ctx, types.NamespacedName{Name: legacyName, Namespace: v.Namespace}, secret); err == nil {
    if err := r.Delete(ctx, secret); err != nil && !apierrors.IsNotFound(err) {
```

`legacyName` is `builder.SentinelCertificateName(v)` = `<cr-name>-sentinel-tls`
(`internal/builder/certificate.go`). There is no ownerReference check, no label
check and no UID precondition. The only guard is that the name must differ from
the active TLS Secret (`valkey_controller.go:1027-1030`).

**Failure scenario — corrected 2026-08-21, and it is worse than this item first
described.** A principal who may create `Valkey` CRs in namespace X names one so
that `<cr-name>-sentinel-tls` collides with an unrelated Secret in X, and sets
`spec.tls.certManager` plus `spec.tls.unifiedCertificate: true`. The operator then
deletes that Secret. The attacker needs create-Valkey rights in X and nothing
else; the damage is confined to X, but the Secret need not be theirs.

**There is no window to wait for.** This item originally had the attacker wait for
the Sentinel rollout to finish before the delete fires. That wait does not exist
for the shape that matters: `sentinelRolloutComplete` opens with

```go
// internal/controller/valkey_controller.go:1087-1090
func (r *ValkeyReconciler) sentinelRolloutComplete(ctx context.Context, v *vkov1.Valkey) (bool, error) {
    if !v.IsSentinelEnabled() {
        return true, nil
    }
```

and `IsUnifiedCertificateEnabled` (`api/v1/valkey_types.go:745-747`) is
`IsTLSEnabled() && Spec.TLS.UnifiedCertificate` — it never consults Sentinel. So
`certManager` + `unifiedCertificate: true` + **Sentinel disabled** is a valid,
schema-clean spec that reaches the delete on the **first** reconcile: the
reconcile table admits the whole path on `IsCertManagerEnabled` alone
(`valkey_controller.go:427`), the two early returns at `:1022` and `:1027` do not
apply, the rollout gate at `:1032` answers `true` immediately, and the Get/Delete
pair at `:1068-1071` runs. Zero-delay, no rollout to observe, no timing to hit.

Sharpest detail: the doc comment on `IsUnifiedCertificateEnabled`
(`api/v1/valkey_types.go:744`) states "When Sentinel is disabled, the flag has no
observable effect". Deleting a Secret in the namespace is an observable effect,
so that comment is wrong today and should be corrected together with whichever
option is chosen.

Both shapes are now pinned by tests that assert the **current** behaviour and
carry an inversion instruction in their doc comments, so the decision cannot land
without touching them:
`TestReconcileLegacySentinelCleanup_NA49_DeletesForeignSecretUnderLegacyName`
(`internal/controller/certificate_reconcile_test.go:582`, foreign Secret, no owner
reference, no cert-manager stamp, Sentinel rollout complete) and
`TestReconcileLegacySentinelCleanup_NA49_NoSentinelMeansNoWaitBeforeDeleting`
(`:621`, Sentinel disabled, first pass, nothing else in the namespace).

**Privilege delta, stated precisely.** The operator could already read every
Secret in the cluster (`get`/`list`/`watch`, pre-existing and unchanged) — that is
the heavier confidentiality exposure and NA37 does not touch it. What NA37 adds is
destruction: a compromised operator can now delete any Secret cluster-wide. The
shift is from "reads everything" to "reads and destroys everything".

**Why this is inconsistent with the rest of the branch.** NA14 and NA31 established
the opposite rule for PodDisruptionBudgets, in the same shipment: an object under a
name the operator manages is never deleted without an ownership check (NA14) and
never deleted without a UID precondition (NA31), precisely because the name alone
is what a user's own object would also be called. The Secret path predates that
rule and does not follow it.

**Fix options.**

- **(a) Gate the delete on the cert-manager provenance annotation.** cert-manager
  stamps the Secret it issues with `cert-manager.io/certificate-name`; deleting
  only when that annotation names `legacyName` makes a foreign Secret under the
  same name untouchable. About four lines, no new dependency, and it applies the
  NA14/NA31 discipline the branch already enforces elsewhere. **Unverified:** the
  annotation key is external knowledge, not read out of this repo — nothing in the
  tree references it (`grep -rn 'cert-manager.io/certificate-name' --include='*.go'`
  returns nothing). It must be confirmed against the cert-manager version in use
  before it is relied on; if it were wrong, the guard would silently stop deleting
  and leave stale TLS material, which is the safe direction to fail.
- **(b) Add the NA31 UID precondition as well. This does NOT close NA49.** It
  closes the read-then-delete race — the Secret being replaced between the Get and
  the Delete — and nothing else. A name collision passes the precondition
  perfectly: the UID the operator reads is the foreign Secret's own UID, so the
  Delete matches and succeeds. The 2026-08-21 correction above makes this decisive
  rather than merely true: with no rollout window there is not even a race left for
  the precondition to win. Complementary to (a), never a substitute for it. **Only
  (a) or (c) closes this item.**
- **(c) Do not delete the Secret at all; log it and let an admin remove it.** Keeps
  the RBAC grant unnecessary and would let the chart rule be dropped again, but
  leaves stale TLS material and the name occupied — the property the cleanup exists
  to provide.
- **(d) Status quo.** The RBAC fix stands, the delete stays name-based, the risk is
  documented here and nowhere else.

**Two omissions in the option list above, both found while implementing
(2026-08-21).**

- **The Certificate delete has the identical shape and was never listed.** The same
  function deletes the `<cr>-sentinel-tls` **Certificate** by name, with no
  ownership check. It is *not* a consequence of NA37 — the chart has granted
  `delete` on `cert-manager.io/certificates` all along
  (`deploy/helm/valkey-operator/templates/clusterrole.yaml:123-135`), so this half
  predates the branch entirely. Deleting a foreign Certificate stops somebody
  else's issuance and renewal. It is also the cheaper half to close: the operator
  sets the ownerReference itself on every Certificate it creates
  (`reconcileCertificate`, `builder.CertificateOwnerRef`), so `IsControlledBy` is a
  self-issued fact needing no external convention.
- **A second provenance proof exists that needs no external knowledge.** The legacy
  Certificate carries our ownerReference and `spec.secretName == legacyName`, so a
  Certificate we own proves what the Secret beside it is. Call it **(a')**. Alone
  it is not enough: it is only available while that Certificate still exists, and
  the population NA37 repaired is precisely the one where the Certificate delete
  landed and the Secret delete then 403'd — Certificate gone, Secret orphaned, no
  proof left. It is an additional accept-path, never a replacement for (a).

The same reasoning kills a third candidate that looked attractive: stamping our own
label through cert-manager's `spec.secretTemplate`. It stamps only Secrets issued
from that change onward, and legacy Secrets are by definition older — dead on
arrival for the migration it would have to serve.

**Option (a) verified against a real cluster (2026-08-21, wds18-k8s-main).**
cert-manager **v1.21.1**, Kubernetes v1.34.4. A sweep over **all 119 Certificates**
of the cluster and the Secrets they issue into:

```
checked 119 | missing-ann 0 | wrong-value 0 | wrong-type 0 | no-fao 0 | with-ownerRefs 0
```

- `cert-manager.io/certificate-name` is present on every issued Secret.
- `type: kubernetes.io/tls` without exception.
- `controller.cert-manager.io/fao: "true"` on all 124 fao-labelled Secrets — a
  second, independent cert-manager signal.
- **No ownerReferences on any issued Secret.** `--enable-certificate-owner-ref` is
  off, so cert-manager does not garbage-collect the Secret when its Certificate
  goes. That makes option (c) — do not delete — concretely lossy rather than
  theoretically so: the material really does linger.

**Correction to the wording of (a):** the annotation value is the **Certificate**
name, not the Secret name. Proven by a case where the two differ —
`cert-manager/trust-manager-tls` carries
`cert-manager.io/certificate-name: trust-manager`. For the legacy Sentinel material
they coincide, because `SentinelCertificateName` and `SentinelTLSSecretName` both
derive `<cr>-sentinel-tls` in split-cert mode (`internal/builder/certificate.go`).
The implemented guard compares against the Certificate name explicitly and carries
that reason in a comment, so a future split of those two derivations fails loudly
instead of silently authorising the wrong Secret.

**The migration population exists on that cluster — five live instances**, all in
the shape the guard needs (operator `1.10.48`, split-cert mode, not yet migrated):

```
database-examples/valkey8-sentinal-tls-sentinel-tls   ann=<self>  tls  fao  ownerRefs=[]
database-examples/valkey9-sentinal-tls-sentinel-tls   ann=<self>  tls  fao  ownerRefs=[]
gitlab/gitlab-valkey-sentinel-tls                     ann=<self>  tls  fao  ownerRefs=[]
gpt/gpt-valkey-sentinel-tls                           ann=<self>  tls  fao  ownerRefs=[]
harbor/harbor-valkey-sentinel-tls                     ann=<self>  tls  fao  ownerRefs=[]
```

Each matching Certificate is still present, `spec.secretName == metadata.name`,
ownerReference `Valkey/<cr>` with `controller: true`. Both proofs hold for all five,
so the guard changes nothing about their eventual cleanup. Note what this does
*not* show: none of the five has migrated yet, so the Certificate-gone-Secret-
orphaned state is unobserved on this cluster. It stays a real cross-pass case —
just not one this cluster demonstrates.

**Decision (2026-08-21, Hans): implement, stacking (a) + (a') + (b) + (e).** Not
(f) — the Sentinel gate would have stranded the legacy material of any instance
that turns Sentinel off, and `reconcileTLSCertificates` does not clean that up on
the split-cert path either.

**What was implemented.**

1. `deleteLegacySentinelCertificate` refuses any Certificate not controlled by this
   Valkey (`metav1.IsControlledBy`), and returns whether the deleted object was ours
   *and* issued into `legacyName`. That verdict is the (a') proof, passed forward
   in-pass so the Secret decision costs no second read.
2. `deleteLegacySentinelSecret` deletes only on (a') **or** (a), with
   `type == kubernetes.io/tls` as a hard precondition above both.
3. Both deletes carry `client.Preconditions{UID:}` (option (b), NA31 discipline). A
   `Conflict` is logged and not an error — and on the Certificate it revokes the
   in-pass proof, so an unstamped Secret beside it is then left alone.
4. Every refusal records a `LegacySentinelTLSNotOwned` Warning naming what was
   missing, modelled on `warnPodDisruptionBudgetNotOwned`. A stranded Secret is
   visible, not silent.
5. `IsUnifiedCertificateEnabled`'s doc comment no longer claims the flag has no
   observable effect without Sentinel; it now names the effect and points here.

**On (e), stated honestly:** the type precondition does **not** close NA49 on its
own — an attacker can aim the name at a genuine TLS Secret. It removes the class of
accidental collateral (token, config, registry Secrets) before either proof runs,
and it outranks the annotation: a Secret that is not `kubernetes.io/tls` was not
issued by cert-manager whatever it claims. `controller.cert-manager.io/fao` was
deliberately **not** made an accept-path — it says "cert-manager owns this", not
"we own this", so it would admit every foreign cert-manager Secret under the name.

**Fail direction.** Where no proof holds, the material stays and a Warning fires.
That leaves stale TLS material and an occupied name, both recoverable by hand; the
other direction destroys a Secret the operator never created. If a future
cert-manager release renames the annotation, (a') still covers the in-pass case and
the failure is a stranded Secret, not a deletion.

**Tests.** The two tests that pinned the hazard are inverted and renamed
(`..._NA49_LeavesForeignSecretUnderLegacyName`,
`..._NA49_NoSentinelStillGuardsTheDelete`); the second keeps the zero-rollout-window
shape pinned, since the guard rather than the timing is now what protects the
Secret. Added: foreign Certificate survives; foreign Certificate grants nothing to
the Secret beside it; annotation alone authorises (the orphaned-Secret migration
path); owned Certificate alone authorises an unstamped Secret; an owned Certificate
pointing at a different `secretName` authorises nothing; a non-TLS type is never
deleted even with both proofs present; both deletes carry a UID precondition; a
Certificate conflict revokes the in-pass proof. The shared test helpers now build
the legacy objects as the real actors write them — `newTestValkey` carries a UID
(without one `IsControlledBy` matches an empty-UID ownerReference and an ownership
test proves nothing), `newLegacySentinelCert` sets the controller ownerReference,
`newForeignLegacySentinelCert` does not, and `newLegacySentinelSecret` reproduces
the cert-manager shape verified above.

**Verified in this repo:** the ClusterRoleBinding scope, both delete sites and their
enabling conditions, the name derivations, that the chart rule is what makes the
Secret path reachable on the Helm install path, that a Sentinel-less instance
reaches the delete with no wait at all, and that the chart always granted
Certificate `delete`. **Verified against wds18-k8s-main:** the cert-manager
annotation key, its value semantics, the Secret type, the absence of Secret
ownerReferences, and the shape of five real legacy instances. **Not verified:** the
annotation on cert-manager releases older than v1.21.1 — the failure direction
there is a stranded Secret, not a deletion. Nothing was executed against the
cluster; every command was a read.

## Follow-up work from the recheck of the fifth-pass fix round (2026-08-20)

The nine surviving findings of the adversarial review were closed in one fix round
(recorded at NA26, NA29, NA35 and in the review section above). That round was then
re-reviewed. **The fixes held** — no regression was found in any of them — but the
recheck surfaced one larger hole that this pass's own work had opened and that
neither the review nor the fix round had seen (**NA50** — NA26 made a routine node
drain destructive), one behavior change that nobody had written down
(**NA51**), and two cheap residuals of the fixes themselves (**NA52**).

Two documentation corrections came out of the same recheck, both in `README.md`
and both in the deferred-sidecar paragraph the fifth pass had rewritten
(`:72-98`, condition table row at `:847`): the claim that `SidecarUpdatePending`
is "only ever set, never cleared" was replaced by the reachability argument it
actually rests on (recorded at NA48(c), where the clearing path is analyzed), and
the `kubectl delete pod <name>-0` advice gained the data-loss clause it was
missing — the only pod of a cluster has no failover target, so an instance without
`persistence.enabled` comes back empty. The metrics note at `:460` had carried that
warning since the fifth pass; the upgrade paragraph, which recommends the delete,
did not.

### NA50 — the sidecar's drain promotion is never recorded, and NA26 turns the ordinary node drain into data loss — DONE

**Numbering: a new item, not an amendment to NA26.** Three reasons. The ticket's
own rule is that a finding becomes an item, and this is a defect in this pass's own
work (same handling as NA47, NA48, NA41). NA26's `DONE` block records a shipped and
verified state; rewriting it in place would erase the fact that the first shape of
the steady-state check was destructive in a routine operation, which is precisely
what a later reader must not lose. And the fix belongs to two items at once — the
check (NA26) and the init-script self-claim (NA35) — so it needs one place of its
own that both link to. Amendments were added at NA26 and NA35 so no path through
this document misses it.

**The gap, pre-existing.** On SIGTERM of a master pod the sidecar patches its own
pod label to `instanceRole=draining` (`internal/sidecar/drain.go:106-112`), then
`manualFailover` finds a synced replica, promotes it with `REPLICAOF NO ONE`
(`:144-172`, the promote at `:159`; refreshed 2026-08-21 from `:140-157`/`:151`,
which the sixth pass invalidated when it added the `masterPresent` early return —
`:151` is now that return, not the promote) and repoints the remaining replicas. **It
records nothing on the CR, and it cannot:** `BuildSidecarRole` grants the sidecar
`pods` `get`/`list`/`patch` and no access to the `Valkey` resource at all
(`internal/builder/rbac.go:39-44`). The known-master annotation keeps naming the
drained pod. *(Sixth pass: it records on the **pod** now — `stampPromotion` writes
`vko.gtrfc.com/drain-promoted-at` on the pod it promoted, which needs no new grant.
The CR is still out of reach and still should be.)* Harmless for as long as nothing consumes the annotation as authority —
which is exactly what this pass changed.

**The scenario, a 2-replica non-Sentinel cluster (the supported HA shape without
Sentinel):**

- **(a)** Steady state: pod-0 master, pod-1 replica, `vko.gtrfc.com/known-master`
  names pod-0, no rolling-update state on the CR.
- **(b)** The node hosting pod-0 is drained — `kubectl drain`, an autoscaler
  scale-down, a PDB-respecting eviction, or a plain `kubectl delete pod`. The
  sidecar promotes pod-1 as described above. Nothing is recorded; the annotation
  still names pod-0.
- **(c)** pod-1 is the master, the sidecar labeler labels it `instanceRole=master`
  and the `-rw` Service routes writes to it. For the whole length of the drain,
  every write lands **only** in pod-1's dataset. Exactly one pod is labeled master
  during this window: the labeler loop of the draining pod has already exited when
  the drain handler runs (`internal/sidecar/run.go:96-106` — `labeler.Run` returns
  on the cancelled context, *then* `drainHandler.Handle` is called), so the
  `draining` label is never re-patched back to `master`. That is what makes the
  window observable and the fix below possible.
- **(d)** pod-0 is rescheduled and returns. Peer discovery in its init container
  rejects pod-1 — a master reporting `connected_slaves: 0`, because its only
  replica was pod-0 and pod-0 was away — so pod-0 comes up master: via the NA35
  self-claim (its replica ConfigMap, derived from the annotation, names pod-0
  itself) or, without NA35, via the ordinal-0 fallback. Two pods are labeled
  master.
- **(e)** The next reconcile runs `checkSteadyStateSplitBrain`. Two labeled
  masters, authority = the annotation = pod-0, pod-0 confirms `role:master` —
  so **pod-1 is demoted toward pod-0**, full-syncs, and every write from (c) is
  gone. The operator records `SplitBrainResolved`: from its own point of view it
  repaired a split brain.

**NA35 is not load-bearing for the loss.** Without the self-claim, a returning
pod-0 takes the ordinal-0 fallback and becomes master anyway, so (d) plays out
identically and (e) is unchanged. NA26 alone was sufficient. What NA35 adds is
breadth: a drained pod of *any* ordinal can now come back master on a stale record,
not only ordinal 0. Before NA26 the same drain left two masters standing —
wrong and visible, but nothing deleted a dataset; NA26 is what converted the
recording gap into deletion. That is why an item about `drain.go`, which is older
than this branch, belongs to this pass.

**The fix — a sharpening of NA26's contract, not a weakening:**

> **The known-master annotation is the tie-breaker among multiple masters; it is
> never used to overrule a single undisputed master.**

The reason is data, not tidiness: a demotion is a `REPLICAOF` that discards the
demoted dataset, so an operator that overrules the only master in the cluster
destroys the writes of whatever promoted it — **including promotions the operator
did not perform itself**, which is the normal case for every eviction and node
drain.

`adoptUnrecordedPromotion` (`internal/controller/steady_state_master.go`,
called from `checkSteadyStateSplitBrain` at `len(labeled) == 1`) implements it:
when the annotation names a different pod than the single labeled master, that pod
answers `role:master` on a live probe **and there is evidence that somebody else
promoted it**, the annotation is moved to it and a `MasterAdopted` Event is
recorded. In the scenario above that happens during step (c) — the drain window,
when exactly one pod is labeled master — so at step (e) the authority is pod-1 and
the pod that gets demoted is the returning stale copy, which is the correct
direction.

> **Superseded, 2026-08-21 (sixth pass).** The paragraph that stood here said the
> adoption needs nothing beyond the live `role:master` answer, and that it sits at
> `len(labeled) == 1` and nowhere else. Both were rewritten by the sixth pass and
> the reasoning is recorded in
> [The evidence-based split-brain resolution replaced the replication forensics](#the-evidence-based-split-brain-resolution-replaced-the-replication-forensics):
>
> - **The label alone is not evidence.** A pod that elected itself off a stale
>   mount answers `role:master` exactly as convincingly as one a drain promoted, so
>   adopting on that answer republishes the replica ConfigMap toward a self-elected
>   pod and full-resyncs the real master's newer dataset away. Adoption now needs
>   the **drain stamp**, the **structural rule** (`couldNotHaveSelfElected`), or the
>   **recorded pod answering that it is no longer master**. No evidence means no
>   adoption and a `MasterAdoptionRefused` Warning Event.
> - **Adoption also happens at two or more labeled masters**, but only on the
>   unambiguous stamp: exactly one stamped pod that still confirms `role:master` is
>   recorded and the others are demoted toward it (`adoptAndConsolidate`). Two
>   stamped masters refuse instead of consolidating — ambiguous evidence must not
>   fall through into a demotion that then picks its target by the annotation alone.
> - The guards named below are unchanged and still hold.

Guards kept: an unreachable pod is not adopted, a pod reporting `role:replica` is
not adopted (a stale label is not a promotion), and nothing happens while a rolling
update is in flight. Cost in the healthy case is a string comparison, both names
being already in hand; the probe runs only on a disagreement.

**Second effect, worth naming separately:** in a >= 3-replica cluster the drained
pod usually returns as a plain replica (peer discovery succeeds), so there is no
split brain and no loss — but the annotation stays stale and becomes a landmine for
the *next* restart, where the NA35 self-claim boots that pod as a second master on
an out-of-date record. The adoption defuses that in the same pass.

**Verified by reading, in this repo:** the promote site and the missing record
(`internal/sidecar/drain.go:106-112`, `:144-172`, promote at `:159`), the sidecar's RBAC
(`internal/builder/rbac.go:39-44`), the authority and demotion path
(`internal/controller/steady_state_master.go`), and that no Pod watch exists
(`internal/controller/valkey_controller.go:1825-1843`: `Owns` StatefulSet,
Deployment, ConfigMap, Service, ServiceAccount, Role, RoleBinding, NetworkPolicy,
PodDisruptionBudget; `Watches` Secret only). **Not reproduced against a cluster.**
Unit coverage is at `internal/controller/steady_state_master_test.go`; the sixth
pass renamed and extended it, and the current adoption set is
`TestSteadyStateSplitBrain_AdoptsTheDrainStampedMaster`,
`_AdoptsAMasterThatCannotHaveSelfElected`,
`_AdoptsTheMasterTheRecordedPodYieldedTo`,
`_DoesNotAdoptASelfElectedPeerWhileTheRecordedMasterIsBooting`,
`_DoesNotAdoptWhileTheRecordedPodIsGone`, `_DoesNotAdoptAPodThatReportsReplica`,
`_DoesNotAdoptAnUnreachablePod`, `_DoesNotAdoptDuringRollingUpdate`,
`_RefusesToAdoptOrdinalZero`, `_RefusesToAdoptAPodTheConfigMapNames`,
`_AnUnparseableStampIsNoStamp`, plus the multi-master rules
(`_RecordsTheStampedMasterAndDemotesTheOthers`,
`_TwoStampsRefuseInsteadOfConsolidating`, `_StaleLabelDoesNotBlockTheStampedMaster`)
and the stamp lifecycle (`TestRecordPromotedMaster_ClearsEveryDrainStamp`,
`TestClearRollingUpdateState_ClearsEveryDrainStamp`,
`TestPodNeedsUpdate_IgnoresTheDrainStamp`).

**Residual — largely closed by the sixth pass; the original text is corrected
here rather than left standing.** As written, the adoption needed a reconcile pass
that *observed* the single-master window, and a drain that completed and returned
the pod between two passes fell through to (d) and (e). The drain stamp closes
exactly that hole: it is written by the promoter, it lives on the **Pod object**,
and it is therefore still readable long after the window shut — a pass that arrives
late now sees two labeled masters and resolves toward the stamped one (rule 1)
instead of toward the stale record. The original conclusion that it "cannot be
closed from the operator side alone" was wrong in one direction: it did not need a
Pod watch and it did not need the CR write grant `BuildSidecarRole` withholds, only
a place for the sidecar to write that it already had permission for.

What genuinely remains: the stamp does **not** survive a delete-recreate of the
promoted pod, so a promoted pod that loses its node before any pass reads the stamp
comes back without it, and the resolution falls back to the structural rule or to a
refusal. And there is still no Pod watch (verified again in the sixth pass:
`internal/controller/valkey_controller.go:1901-1913` — `Owns` StatefulSet,
Deployment, ConfigMap, Service, ServiceAccount, Role, RoleBinding, NetworkPolicy,
PodDisruptionBudget; `Watches` Secret only), so the operator still learns about pod
changes only through owned-object events and the resync.

### NA51 — no-master recovery fails closed on an unwritable CR: a silent data-loss path traded for a visible availability freeze — DONE (deliberate, undocumented until now)

Not a defect and not new work — a **named behavior change** that the fix round made
and nobody wrote down. It is recorded here because the symptom is one an on-call
engineer must be able to look up instead of reverse-engineering from a log line.

`checkAndRecoverNoMaster` (`internal/controller/valkey_controller.go:1789`) now
**records the promotion before performing it**: `recordPromotedMaster` at `:1855`
(reasoning in the comment at `:1844-1854`), and its error is returned instead of
swallowed. (All four line numbers in this item were refreshed 2026-08-21; the
function moved ~72 lines when the sidecar-condition work landed. The originals
were `:1717`, `:1783`, `:1772-1782` and `:1746-1748`.) The caller `handlePostRollingUpdateChecks` (`:361-366`) sets phase
`Error` with the message `No-master recovery failed: <err>` and requeues after
10 s — for as long as the write keeps failing.

**Consequence, stated plainly:** a multi-replica non-Sentinel cluster in which
every pod reports `role:replica` — no master, so the `-rw` Service has no endpoint
and every write fails — is **not recovered** while the CR cannot be written. That
is the WP2/NA33 failure class: a fail-closed admission webhook on the CR, lost
`valkeys` RBAC, a permanently conflicting writer. Reads keep working (the `-r`
Service selects replicas); writes stay down until the write path is fixed.

**Why this is the right direction.** Promoting without recording is the
NA26/NA35 invariant violation with the largest blast radius, because this function
never gets a second chance to correct it: the next pass finds a master and
short-circuits on `hasMaster` (`:1803`, set at `:1813`, tested at `:1818`). The annotation would then name some
other pod permanently, feeding both the init-script self-claim (NA35) and the
steady-state authority (NA26) — and the first demotion would go the wrong way, i.e.
exactly the NA50 loss, self-inflicted. Ordering the record first is what makes the
failure recoverable: nothing has been promoted yet, so the returned error simply
retries the whole recovery on the next pass, and naming a pod that is still a
replica is harmless in the meantime (`confirmedMasterAuthority` demotes nothing
until the named pod itself reports `role:master`).

**How to find it in the field:** `kubectl get valkey <name>` shows phase `Error`
with `No-master recovery failed: ...` in the message; the `ReconcileBlocked`
condition (WP2, NA33) names the underlying write failure and distinguishes an
admission rejection from any other. Fix the webhook or the RBAC and the next pass
performs the recovery.

**No bound, on purpose.** There is no timeout that eventually promotes anyway — the
escape would be exactly the unrecorded promotion this ordering exists to prevent.
Same shape as **NA47** (the unbounded `handlePostManualFailover` waits): the
operator holds still rather than acting on a record it could not write. The
difference is that NA47 is an accidental stall and still open, while this one is a
deliberate trade with a phase and a message attached.

**Verified by reading:** the record-before-promote ordering and its error return
(`:1844-1855`), the `hasMaster` short-circuit (`:1803-1818`), the caller's phase,
message and 10 s requeue (`:361-366`, unchanged and re-verified 2026-08-21). Unit coverage:
`internal/controller/known_master_authority_test.go`
(`TestCheckAndRecoverNoMaster_RecordsThePromotion`,
`TestCheckAndRecoverNoMaster_PromotesNothingWhenTheRecordFails`). Not reproduced
against a cluster.

### NA52 — two residuals of the fix round: the promotion rollback and the restored recheck cadence — DONE

**1. `promotePod0AndRedirect` rolls its promotion back when it cannot record it**
(`internal/controller/rolling_update.go:3043`, `rollbackPod0Promotion` at `:3145`,
called from `:3100`; refreshed 2026-08-21 from `:2900-2943`/`:2969-3003`). The fix round had made the function stop advancing to Phase 2 on a
failed record — necessary, but it left the promotion itself standing: pod-0 was
already master, unrecorded, and stayed master for up to the Phase 1 budget
(`spec.rollingUpdate.syncTimeout`, default **5 m**) while the `-rw` Service sent it
writes that Phase 2 then discarded when it demoted pod-0 back toward the promoted
replica. The previously recorded master is now captured before the promotion
(`:3059-3061`, refreshed 2026-08-21 from `:2902`) and pod-0 is handed back to it, collapsing that window to zero. It loses
nothing: Phase 1 only promotes pod-0 once it has fully synced from the promoted
replica, so making it a replica of that same pod again discards no data it did not
already have. When nothing else was recorded, or the record already names pod-0
(the annotation write landed and only the ConfigMap republish failed), there is
nothing to hand back to and pod-0 stays promoted, with a log line saying so. A
rollback that itself fails is logged only — the caller requeues either way and the
bounded abandon path (NA23) still applies. Covered by
`TestPromotePod0AndRedirect_RollsThePromotionBackWhenTheRecordFails` and
`_NoRollbackWithoutAPreviousMaster`.

**2. `checkSteadyStateSplitBrain` schedules the recheck again.** Keeping the status
write on the unresolvable path (the fix round's correction — otherwise the CR
freezes at its last verdict, usually `OK`, while the operator loops on a split
brain invisibly) meant returning `done=false`, and a `done=false` result was
dropped by the caller. That silently removed the guaranteed next look: the CR watch
is generation-gated, there is no Pod watch and no `SyncPeriod` override, so the
next pass would have been the 10 h cache resync. `steadyStateRecheckDelay` (15 s)
now travels back as a **non-terminal** `ctrl.Result` which `reconcileWorkload`
applies after `updateStatus`, behind every other requeue reason
(`internal/controller/valkey_controller.go:300-331`). Only the unresolved case sets
it — a merely stale label is not a data split and the operator does not repatch
role labels, so requeueing on it would poll for a fix it cannot perform. Covered by
`TestReconcileWorkload_CarriesTheSplitBrainRecheck`,
`TestSteadyStateSplitBrain_UnresolvableRogueKeepsTheStatusWrite` and
`TestReconcileWorkload_HealthyClusterKeepsNoRequeue`.

*Corrected by the sixth pass:* "only the unresolved case sets it" is one case short.
`reportDemotionOutcome` requeues on `unresolved + refused > 0`
(`internal/controller/steady_state_master.go:606-607`; `:605` in the original text
is the last line of the explaining comment, not the branch), so a demotion the operator
**refused** (the missed-drain shape) also carries the 15 s recheck — deliberately,
since that cluster is still split. What schedules nothing is a pass with no
confirmed rogue at all: a stale label, or the no-admissible-authority branch, where
the operator has no fix to poll for. `TestReconcileWorkload_CarriesTheRefusedDemotionRecheck`
pins the refused case.

Both are recorded at NA26's residual list as well, since they change statements
made there.

## Follow-up work from the sixth pass (2026-08-21)

This pass worked the ten items the fifth pass left open (NA39-NA48) — eight closed,
NA43 and NA45 partially, with what remains named at each — reworked the
steady-state split-brain resolution that NA50 had introduced, fixed a defect in
which **the operator manufactured the very evidence that resolution consumes**,
rebuilt two vacuous tests in `internal/observer`, and lifted statement coverage
from 68.4% to 87.3%. Three new items came out of it: **NA53**, **NA54**, **NA55**.

### Shape of this pass — what was run versus what was only read

| Target | Result |
|---|---|
| `make fmt` | exit 0, no file reformatted |
| `make vet` | exit 0, no diagnostics |
| `make lint` | `0 issues.` |
| `make cyclo` | all functions below 15 |
| `make test-unit` | exit 0, **12** packages ok (9 before: `cmd`, `cmd/observer` and `cmd/sidecar` had no test file at all), 0 SKIP |
| `make test-unit-coverage` | `total: 87.3% of statements` |
| `make test-integration` | `ok .../test/integration`, 0 FAIL — includes the new envtest case of NA42 |
| `make test-e2e E2E_RUN='TestE2E_CompileCheckOnly'` | the e2e package compiles |
| `make generate-all` | exit 0, `git status` unchanged afterwards — no CRD/RBAC/DeepCopy/Helm drift today |

**Verified by code reading only, and by nothing else:** every claim about the
production reconcile paths below, the CI job added for NA44 (a workflow file
cannot be executed from a developer machine — see the item), and NA53/NA54/NA55.
No cluster was touched in this pass.

Revert-verification, and who did it. Every behavioural fix in this pass was
confirmed to fail against the pre-fix code by **actually reverting it, running the
target and restoring** — the implementing agent did that for the production changes
(NA39, NA40, NA47, NA48 and the split-brain rework; the individual items name the
tests that flipped), and the documentation pass that wrote this section did it for
the one test it added itself: dropping the `client.Preconditions` option from the
NA42 integration test makes both of its subtests fail. Three exceptions are stated
rather than glossed: NA41 is a closure with no code change, NA45 is test hygiene
whose whole point is that behaviour does not change, and the CI job of NA44 has
never run on a runner.

### The evidence-based split-brain resolution replaced the replication forensics

NA50 gave `checkSteadyStateSplitBrain` an adoption path so that a master promoted
by the **sidecar** drain handler — a promotion the operator cannot see, because the
sidecar has no CR access — is not demoted back into the stale record. The first
shape of that path adopted on the strength of the label alone (a single labeled
master that answers `role:master`). This pass replaced it, because the label is
not evidence: a pod that elected itself off a stale mount answers `role:master`
just as convincingly as one a drain promoted.

**The forensic approach was tried first and abandoned on a measurement.** The idea
was to ask Valkey itself which candidate had been demoted: `INFO replication`
exposes `master_replid2` and `second_repl_offset`, which a server sets when it
gives up the master role, so a genuine promotion should be distinguishable from a
self-election. Measured on a running pair under `persistence.mode: rdb` — the
default — **both candidates reported byte-identical `master_replid2` and
`second_repl_offset`**, so the field could not separate them. (Measured by the
implementing agent against live pods; this documentation pass did not re-run the
measurement. What *is* verifiable in this repo: `valkeyclient.ReplicationInfo`
([`internal/valkeyclient/client.go:27`](internal/valkeyclient/client.go)) still
parses six fields and none of them is a replication ID — the forensic route left
no trace in the code, because it never worked.)

What replaced it are three pieces of **positive** evidence, in
[`internal/controller/steady_state_master.go`](internal/controller/steady_state_master.go),
cheapest first:

1. **The drain stamp.** The sidecar drain handler now annotates the pod it
   promotes with `vko.gtrfc.com/drain-promoted-at`
   ([`internal/common/annotations.go`](internal/common/annotations.go),
   `stampPromotion` in [`internal/sidecar/drain.go`](internal/sidecar/drain.go)).
   That closes NA50's recording gap from the side that actually knows: it is the
   promoter itself that records. Free to read — the stamp is already on the listed
   Pod.
2. **The structural rule** (`couldNotHaveSelfElected`). The init script grants the
   master config to ordinal 0 on the ordinal fallback, and otherwise only through
   the NA35 self-claim, which needs the mounted replica ConfigMap to name the pod
   itself. A labeled master with ordinal > 0 that the live replica ConfigMap does
   not name therefore **cannot** have elected itself. Costs a cache-served read.
3. **The recorded pod yielded** (`recordedGaveUpTheRole`). The pod the annotation
   names answers a probe and reports a role other than master. A pod replicating
   from somewhere else has already given up its dataset, so republishing the
   ConfigMap away from it destroys nothing. Costs one Valkey connection, and only
   on a label/annotation disagreement — never in the steady state.

**The stamp has a lifecycle, and that is the part worth reviewing.** A stamp means
"a promotion nobody recorded", so the moment the operator *does* record one the
stamp is spent — and spent evidence outranks the annotation on the next
multi-master pass, because rule 1 is evidence-first. Left behind, a stale stamp on
pod-N would have the operator adopt pod-N and send `REPLICAOF` to the master it
legitimately promoted: the exact loss the stamp exists to prevent, one pass later.
`clearDrainStamps` (`internal/controller/steady_state_master.go:658`) therefore
wipes every stamp of the cluster at the two sites that end a promotion —
`recordPromotedMaster` (`rolling_update.go:761`) and `clearRollingUpdateState`
(`:2112`, needed because `persistManualFailoverState` and `syncSentinelWithMaster`
write the known master *without* going through `recordPromotedMaster`). A failed
clear is logged and nothing else: the promotion is already recorded, so aborting
would trade a possible wrong adoption later for a certainly unrecorded promotion
now. Two more properties keep the annotation harmless: an unparseable value reads
as "no stamp", never as "corrupt, therefore fresh" (`hasDrainStamp`, `:627`), and
`podNeedsUpdate` ignores it, so stamping a pod can never trigger a rolling restart
(`TestPodNeedsUpdate_IgnoresTheDrainStamp`).

Rule 3 exists because rule 2 is blind to exactly the pod a drain promotes most
often: `buildReplicaAddrs` walks ordinals ascending and `findSyncedReplica` takes
the first synced peer, so draining a non-pod-0 master promotes **pod-0** whenever
pod-0 is healthy — and `couldNotHaveSelfElected` can never exonerate pod-0.

**The invariant this produced, and it is the load-bearing sentence of the whole
resolution:**

> **The creation order may only ever REFUSE a demotion. It may never ADOPT.**

"The pod the annotation names is the younger Pod object" is true after a drain —
and equally true after the recorded master's node hard-failed with no SIGTERM
(hence no drain, no stamp) while a peer that could reach nobody took the ordinal
fallback and elected itself. Adopting there republishes the replica ConfigMap
toward the self-elected pod, and the real master full-resyncs its newer dataset
away the moment it finishes booting — silently, and caused by the operator.
Refusing on the same signal is safe in the opposite direction: the worst outcome
is two masters a human can see.

Two consequences the invariant forced, both implemented:

- **Absence is not evidence either.** "The recorded pod no longer exists" was
  offered as a third adoption route and **rejected**: it is the same data-loss
  shape with the Pod object deleted instead of rescheduled. Only "answers and is
  not master" is admitted; unreachable, absent and still-master all read as false
  and end in a `MasterAdoptionRefused` Event.
  (`TestSteadyStateSplitBrain_DoesNotAdoptWhileTheRecordedPodIsGone`.)
- **The refusal expires.** `recreatedAfter` now requires the recreation to be
  inside `spec.rollingUpdate.syncTimeout` (default 5 m) as well as strictly later.
  An absolute comparison of two `creationTimestamps` is *permanent* after any
  reschedule, so unbounded it would have frozen every future split brain of that
  pair, whatever its cause. The window is the operator's own budget for a deleted
  pod to come back and rejoin its master — the same knob the replica-replacement
  phase and Phase 1 of the topology restoration already use, so one setting widens
  all three for a slow environment. Past the window the operator resolves the way
  it did before the rule existed: toward the annotation.

One comment was deleted rather than corrected: `refuseDemotion` used to claim "a
StatefulSet recreates in ordinal order". The data StatefulSet runs
`PodManagementPolicy: Parallel`
([`internal/builder/statefulset.go`](internal/builder/statefulset.go)), so a
co-restart recreates every pod at once and **ties** the timestamps; `Before` is
strict, so the rule is inert there. The equal-age fixture in
`TestSteadyStateSplitBrain_RefusesTheMissedDrainShape` pins that.

### The operator was manufacturing the evidence it consumes

**The defect.** On every rolling update of a non-Sentinel cluster with **three or
more replicas**, the operator promotes a replica (`promoteAndRedirect`) and then
deletes the old master. The deleted master receives SIGTERM, and its sidecar drain
handler ran its ordinary manual failover: it walked the peers ascending, skipped
the operator's fresh master (a master is not a *synced replica*, so
`isSyncedReplica` rejected it), found the **next** pod — a healthy replica — sent
it `REPLICAOF NO ONE`, and stamped it. Two damages at once:

1. **A forged stamp.** The operator then reads that stamp as evidence of a drain
   promotion nobody recorded, on a pod nobody promoted deliberately.
2. **A REPLICAOF fight, and this one is older than the stamp.**
   `reconfigureReplicas` points *every* remaining peer at the pod it just
   promoted — including the master the operator promoted seconds earlier. The
   outgoing master demoted the incoming one. Pre-existing, and never noticed
   because nothing consumed the outcome until NA26.

**The fix** (`findSyncedReplica`, `internal/sidecar/drain.go`): every reachable
peer is queried before "no master" may be concluded, and a peer that answers
`role:master` ends the failover before it starts — there is a master in the
topology, so this drain has nothing to promote and nothing to stamp. A genuine
node drain has no master among its peers and is unaffected. Querying *all* peers
first is deliberate: returning at the first synced replica could hand back a
promotion target while a master sits further down the list.

Verified: `internal/sidecar/drain_test.go` covers the skip, the stamp, the
stamp-failure degradation and the host-to-pod-name derivation; the change was
revert-verified.

**Half-closed, and filed as [NA53](#na53-promoteandredirect-leaves-the-outgoing-master-answering-rolemaster-for-the-whole-termination-window--open):**
`promoteAndRedirect` still never demotes the pod it is about to delete
(`internal/controller/rolling_update.go:2763-2772`, the loop skips `masterIdx`),
so for the whole termination window two pods answer `role:master`. The sidecar
fix means nobody acts destructively on that window any more, but it is exactly the
shape the rest of this document treats as a split brain.

### Two vacuous tests in `internal/observer`, and what replaced them

A mutation audit of the security-relevant paths caught two findings — three test
functions — that could not fail:

- `TestNewSentinelClient_WithSentinelTLSConfig` set both `sentinelTLSConfig` and
  `tlsConfig`, commented "should use sentinelTLSConfig, not tlsConfig", and then
  asserted `assert.NotNil(t, c)`. `newSentinelClient` returns a client on **every**
  branch, so the assertion held whichever TLS config was picked — including the
  wrong one, which would have meant presenting the Valkey client certificate to
  Sentinel, or verifying Sentinel against the wrong CA.
- `TestNewClient_AllCombinations` / `TestNewSentinelClient_FallbackToTLSConfig`
  did the same for the password and TLS-vs-plaintext combinations: three
  constructions, three `assert.NotNil`, no observation of what was constructed.

All three are deleted. What replaced them observes the wire and the parsed config
instead of the pointer:
[`internal/observer/tls_config_test.go`](internal/observer/tls_config_test.go)
asserts the actual `*tls.Config` contents (`MinVersion`, `RootCAs` present or
nil, exactly one client certificate under mTLS and none without it, and that the
Sentinel config omits the client certificate unless `mtls.sentinel` is set), and
[`internal/observer/checks_endpoint_test.go`](internal/observer/checks_endpoint_test.go)
drives the constructors against a fake endpoint so that
`TestSentinelCalls_DisableAuthControlsTheAuthCommand` can assert whether an `AUTH`
command is sent at all, and `TestNewSentinelClient_PrefersTheSentinelTLSConfig`
can distinguish the two configs by their effect.

Same file, a smaller hygiene item worth naming: every fixture hostname moved from
`*.svc.cluster.local` to `*.invalid`. The old names are resolvable in some
environments, so a unit test could leave the process and hang on a DNS lookup;
`.invalid` is reserved by RFC 2606 and never resolves.

### Coverage: 68.4% -> 87.3% of statements

Measured with `make test-unit-coverage` (`go tool cover -func` on
`coverage/unit.out`), 12 packages:

| Package | Coverage |
|---|---|
| `api/v1` | 100.0% |
| `internal/common` | 100.0% |
| `internal/health` | 100.0% |
| `internal/observer` | 97.8% |
| `internal/valkeyclient` | 97.7% |
| `internal/builder` | 96.9% |
| `internal/sidecar` | 93.7% |
| `cmd/observer` | 85.9% |
| `cmd/sidecar` | 80.4% |
| `internal/controller` | 79.6% |
| `cmd/migrate` | 69.8% |
| `cmd` | 25.5% |

Three packages had **no test file at all** before this pass (`cmd`,
`cmd/observer`, `cmd/sidecar`), which is why `make test-unit` now reports 12
packages instead of 9. The per-package *baseline* numbers were not recorded before
the work started, so the only honest before/after statement is the total:
**68.4% -> 87.3%**.

**What is still uncovered**, exhaustively — 15 functions have 0%:

- the four process entry points (`cmd/main.go:main`, and `Run` in `cmd/migrate`,
  `cmd/observer`, `cmd/sidecar`) and `SetupWithManager` — these build a manager or
  parse `os.Args` and exit; covering them means an in-process manager, which the
  integration suite already does end-to-end;
- `reconcileTLSCertificates` / `reconcileCertificate`
  (`internal/controller/valkey_controller.go:993,1157`) and
  `ServiceMonitorOwnerRef` — the unstructured cert-manager and
  Prometheus-Operator paths, exercised by the e2e suite against a real
  cert-manager, not by unit tests;
- five `rolling_update.go` functions reachable only from `handleRollingUpdate`,
  which is the **Sentinel** dispatch target (`checkAndHandleRollingUpdate:213-215`
  routes `IsSentinelEnabled()` there): `handleFailoverRetrigger`,
  `handleMasterFailover`, `replaceRemainingPods`, `handleNewMasterFound`,
  `verifyNewMasterReady`;
- and two on the non-Sentinel side that no unit test reaches either:
  `deleteNextPendingPod` (the tail of `dispatchMultiReplicaState`) and
  `hasPendingUpdates` (used by both dispatchers).

The Sentinel five are the largest genuine gap: the non-Sentinel state machine is
now covered in detail while its Sentinel sibling is exercised only by the e2e
suite.

`internal/controller` at 79.6% is the lowest of the library packages and the one
where the remaining risk lives.

**Superseded 2026-08-21 by the seventh pass — the list above is a record of this
pass, not the current state.** Ten of those fifteen functions are covered now, and
all ten at 100.0% — including all five Sentinel ones (`internal/controller/sentinel_failover_test.go`
drives them against a RESP router), `reconcileTLSCertificates` /
`reconcileCertificate` (`certificate_reconcile_test.go`), `ServiceMonitorOwnerRef`,
`deleteNextPendingPod` and `hasPendingUpdates`. Five remain at 0.0%, and they are
the four process entry points plus `SetupWithManager` — the sentence above that
called those "an in-process manager" is the one part of this paragraph that still
holds. `internal/controller` is at 95.13%. See the seventh-pass coverage section
for the current table.

### NA53 — `promoteAndRedirect` leaves the outgoing master answering `role:master` for the whole termination window — DONE (2026-08-21, decided by Hans)

**Status (ninth pass, 2026-08-21): implemented as sketched below, decided by Hans.**
`promoteAndRedirect` now demotes the outgoing master — `REPLICAOF <promotedHost>` against
`pods[masterIdx]` — as its own step strictly after the promotion succeeds and before the
redirect loop, best-effort with a log line on failure. The redirect loop still skips
`masterIdx`. Guarded by `TestPromoteAndRedirect_DemotesTheOutgoingMaster`
(`internal/controller/manual_failover_known_master_test.go`), which runs one recording fake
server per target pod and asserts the demotion reached the old master and named the promoted
host. ADR 0012 D9 is rewritten from a conditional rule to an implemented one, and its
residual-risk entry for the two-master window is closed in place — with the named residual
that a failed demotion is only logged, so the window returns for that one failover.
Verified: unit suite, lint, vet green. Not reproduced against a cluster.

**Pre-existing; the other half of the sidecar self-poisoning fix above.**

`promoteAndRedirect` (`internal/controller/rolling_update.go:2739`) promotes the
chosen replica with `REPLICAOF NO ONE` and then redirects the other replicas to
it — but its loop skips `masterIdx` (`:2764`), so the pod it is about to delete is
never demoted. `handleManualFailover` then persists the state, republishes the
replica ConfigMap and deletes that pod (`:2654`). Between the promotion and the
kubelet actually stopping `valkey-server`, **two pods answer `role:master`**.

What is and is not damaged, precisely:

- The `-rw` Service is *not* split: the outgoing pod's sidecar patches its own
  label to `draining` at the top of the drain handler
  (`internal/sidecar/drain.go:106-112`), so it leaves the Service before the
  failover work starts. Clients holding an open connection keep writing to it
  until it closes, and those writes are lost — that is inherent to any failover.
- `checkSteadyStateSplitBrain` does not see it: a rolling-update state annotation
  is set, and the check returns early for exactly that reason.
- Since the sidecar fix, nothing acts on the window destructively any more. What
  remains is that the operator produces, on every rolling update, the state its
  own steady-state check calls a split brain — and that the outgoing sidecar's
  `waitForRoleChange` now polls until `valkey-server` refuses connections instead
  of until a role change it will never observe (bounded by the 60 s drain timeout,
  `internal/sidecar/run.go:116-120`, inside a 75 s termination grace period).

**Fix sketch:** demote `masterIdx` inside `promoteAndRedirect`, immediately after
the promotion succeeds and before the redirect loop — `REPLICAOF <promotedHost>
<port>` to `pods[masterIdx]`, best-effort with a log line, since the pod is about
to be deleted and a failure must not abort the failover. It loses no data: the
pod was drained of writes by `waitForWriteSync` (`WAIT`) before the promotion, and
it is deleted seconds later. It makes the outgoing pod a replica of the incoming
one, which is the topology the rest of the state machine already assumes.

**Cost / risk to weigh before doing it:** the demotion is a destructive command
sent to the pod that still holds the authoritative dataset, at the one moment the
promotion may still fail. If the `REPLICAOF NO ONE` on the promoted pod succeeded
but the ConfigMap republish or the delete then fails, the cluster is left with a
demoted old master and a promoted new one — which is the intended end state, so
the risk is small, but it must be ordered *after* the promotion, never before.

Verified by reading: the skip at `:2764`, the delete at `:2654`, the draining
label patch, the drain timeout and the grace period. Not reproduced against a
cluster.

### NA54 — the sidecar Role grants namespace-wide `pods` `get,list,patch`; only `patch` is used — DONE (2026-08-21, all of (a), (b), (c))

**Status (eleventh pass, 2026-08-21): closed. (a) and (c) implemented, (b) already shipped
in the ninth pass.** ADR 0012 D8 has no open step left.

**(c) — the observer's own ServiceAccount.** `BuildObserverServiceAccount`
(`internal/builder/observer.go`) creates `<cr-name>-observer`; nothing binds a Role to it.
`BuildObserverDeployment` names it instead of the hard-coded `fmt.Sprintf("%s-sidecar", ...)`
and sets `AutomountServiceAccountToken: ptr.To(false)`, so the observer mounts no token at
all. `reconcileObserver` writes the ServiceAccount **before** the Deployment (a pod naming a
missing ServiceAccount is rejected by the ServiceAccount admission plugin), and
`cleanupObserverServiceAccount` deletes it when the observer is switched off — with an
`IsControlledBy` check and a UID delete precondition, unlike the two name-only cleanups
beside it, because `<cr-name>-observer` is a name a CR author can aim at a pre-existing
ServiceAccount (ADR 0006). `ObserverDeploymentHasChanged` now also compares the
ServiceAccount name and the automount flag (nil resolves to "mounts", so it compares equal
to an explicit `true`); without that, an observer created before this change would have kept
the sidecar token forever, since nothing else about its pod changed. Existing clusters roll
the observer Deployment once — stateless, and worth one line in the release notes.

**(a) — `resourceNames` on the sidecar Role, wider than ADR 0012 D8 originally specified.**
`BuildSidecarRole(v, livePodNames)` emits `verbs: [patch]` with
`resourceNames: [<cr-name>-0 … ]`, and `SidecarRolePodNames` builds that list as the **union
of the pods `spec.replicas` asks for and the pods that currently exist**. The original
wording (`["<sts>-0" … "<sts>-N-1"]`, i.e. `spec.replicas` alone) covered scale-up and broke
scale-down, and that was **decided by Hans before implementation**, with the trade stated:

> Scale-down 5 -> 3 revokes the grant of pods 3 and 4 while they are still terminating. The
> departing master needs exactly that grant for `PatchLabel(own pod, instanceRole=draining)`
> (`internal/sidecar/drain.go:107`), the write that takes it out of the `-rw` Service before
> it fails over. Denying it keeps client writes flowing into a dying master. The drain stamp
> itself is unaffected — `stampPromotion` targets the *promoted* peer, which is in the list
> either way — so the promotion evidence survives; what is lost is the endpoint fencing.

Three properties hold the grant together, each with its own test:

- **Scale-up ordering.** The step list moved into `resourceReconcileSteps` so the order is
  assertable: `sidecar RBAC` runs before `StatefulSet`, so pod N is named before the write
  that creates it. `TestResourceReconcileSteps_RBACBeforeStatefulSet`.
- **An empty name list is not an empty grant.** `resourceNames: []` matches *every* pod in
  Kubernetes RBAC, so a cluster with no pods gets **no rule at all**.
  `TestBuildSidecarRole_NoPodsYieldsNoRuleRatherThanAnOpenOne`.
- **A label cannot widen the grant.** The live names come from a label selector, and labels
  are set by whoever creates the pod, so only names of the exact form
  `<cr-name>-<canonical ordinal>` are accepted — `test-007`, `test-x`, `test--1`,
  `other-cluster-0` and `test-sentinel-0` are all rejected.
  `TestSidecarRolePodNames_IgnoresNamesThatAreNotThisStatefulSets`.

`reconcileSidecarRole` lists the data pods itself (`listDataPodNames`, cache-backed) and
**fails the step** if that List fails rather than narrowing on incomplete information: the
Role already in the cluster is the wider one, and leaving it is the safe direction
(`TestReconcileSidecarRole_FailsTheStepWhenThePodListFails`). Existing clusters narrow on
their next reconcile with no migration step, pinned by
`TestReconcileSidecarRole_NarrowsALegacyNamespaceWideRole`.

**What ran, from this tree.** `make test-unit` (12/12 packages), `make test-integration`
(7/7 envtest tests, 15.7s), `make lint` (0 issues), `make vet`, `make cyclo` (all functions
under 15), `make gosec` (0 issues, 38 files), `make generate-all` followed by a clean
`git status` (no generated drift), `go vet -tags=e2e ./test/e2e/` (compile check only).
**Not run:** any e2e suite, any cluster reproduction.

**What was deliberately left out, and why.** An envtest test for the scale-up widening was
written, measured and then **deleted**. It cost 105s and destabilised the suite: envtest runs
no kubelet, so a reconcile pass for a pod-less multi-replica CR spends 15-25s dialing pods
that will never answer (5s `net.DialTimeout` per client, `internal/valkeyclient/client.go:152`),
the manager runs a single reconcile worker, and the next test in the package
(`TestSidecarServicesRouting_Integration`) then timed out waiting for its own Services —
measured, not assumed: reconcileID `337c562e` logged "Updating sidecar Role" at 13:37:08 and
did not finish until 13:37:33. Waiting for the CR's StatefulSet to disappear does not help
either, because envtest has no garbage collector to remove owned objects. What replaced it:
the unit tests above, plus the existing envtest assertion on a real API server accepting the
`resourceNames` rule (`test/integration/sidecar_services_test.go`, exact list for a
3-replica CR) and the observer ServiceAccount assertions in
`test/integration/observer_test.go` (owner reference, bound to no RoleBinding, automount
false, deleted on disable). The e2e RBAC-drift test
(`test/e2e/upgrade_test.go`) now pins the exact rule of a single-replica cluster; that the
sidecar still does its job under the narrowed grant is covered on a real cluster by the
labeling assertions in `test/e2e/sidecar_test.go`, which run against this same Role. **Not
verified in this pass:** any of that e2e evidence — no e2e leg was executed here.

**New findings from this work:** NA59 and NA60, both in the eleventh-pass section at the
end of this document.

**Superseded record of the ninth pass** — what shipped then was **(b) only**:

- **(b) DONE.** `BuildSidecarRole` grants `verbs: ["patch"]`; the unused `get`/`list` are
  gone. Pinned exactly (`assert.Equal`, not `Contains`) by `TestBuildSidecarRole`
  (`internal/builder/rbac_test.go`) and by the envtest integration test
  (`test/integration/sidecar_services_test.go`). `reconcileSidecarRole` compares `Rules` and
  rewrites on drift, so existing clusters narrow on their next reconcile — no migration
  step. ADR 0012 D8 step 1 marked done; SECURITY_ARCHITECTURE.md (trust table, section 4.2,
  residual list) rewritten to the new grant.
- **(c) — superseded 2026-08-21, implemented; see the status block above.** As recorded in
  the ninth pass: decided but NOT implemented, the pass was cut short deliberately. Per ADR 0012 D8
  step 2 it needs both halves: an observer-own Role-less ServiceAccount **and**
  `automountServiceAccountToken: false` on the observer pod spec. Note the builder currently
  hard-codes `fmt.Sprintf("%s-sidecar", v.Name)` in `BuildObserverDeployment`
  (`internal/builder/observer.go`) instead of calling `SidecarServiceAccountName`. The SA
  swap changes the observer pod template and rolls the observer Deployment once — stateless,
  acceptable, but worth naming in the release notes.
- **(a) — superseded 2026-08-21, implemented; see the status block above.** As recorded in
  the ninth pass: deferred, `resourceNames` needs the Role-before-pod ordering guarantee on
  scale-up plus a test for it. (b) removed its `list` blocker. What the ninth pass did not
  see: the same derivation breaks scale-down, which is why the shipped list is a union.

**Verified claim, checked for this item rather than assumed:** `internal/sidecar`
and `cmd/sidecar` make exactly **one** Kubernetes API call —
`clientset.CoreV1().Pods(namespace).Patch` in `patchMetadata`
([`internal/sidecar/labeler.go:283`](internal/sidecar/labeler.go)), reached from
`PatchLabel` (own pod, `instanceRole`) and `PatchAnnotation` (a peer pod, the
drain stamp). There is no `Get`, no `List`, and no other clientset call site;
`grep -rn "CoreV1()\|kubernetes.Interface"` over both packages returns only those
lines. The observer, which runs under the **same** ServiceAccount
([`internal/builder/observer.go:113`](internal/builder/observer.go)), imports no
Kubernetes client at all.

`BuildSidecarRole` ([`internal/builder/rbac.go:32`](internal/builder/rbac.go))
nevertheless grants:

```yaml
- apiGroups: [""]
  resources: ["pods"]
  verbs: ["get", "list", "patch"]   # no resourceNames
```

namespace-wide, per Valkey CR.

**Why this is worth narrowing now, and was not before.** A forged
`instanceRole=master` label moves the `-rw` Service endpoint — visible, and the
sidecar of the real master keeps repatching. A forged
`vko.gtrfc.com/drain-promoted-at` stamp is different in kind since this pass: the
operator accepts it as evidence and will issue `REPLICAOF` against the pods that
do **not** carry it (`stampedMasters` -> `adoptAndConsolidate`), which discards
their datasets. Any principal that can patch a pod in the namespace — another
cluster's sidecar token, or anything with namespaced `pods: patch` — can therefore
aim a destructive command with two annotations.

**Options.** (Historical, from the sixth pass. All of (a), (b), (c) are implemented as of
2026-08-21; (a) shipped in a wider form than described here — the union, see the status
block.)

- **(a) `verbs: [patch]` with `resourceNames: ["<sts>-0" … "<sts>-N-1"]`.** The
  precondition is dropping the unused `list` verb: `resourceNames` is incompatible
  with `list` (and with `watch`/`create`/`deletecollection`), which is precisely
  why the unused verb has to go first. Cost: the name list is derived from
  `spec.replicas`, so a scale-up has to reconcile the Role **before** the new pod's
  sidecar starts patching, or the new sidecar 403s until the next pass. The
  operator already writes the Role on every reconcile
  (`valkey_controller.go:750`), so the fix is one builder change plus an ordering
  check, not a new mechanism.
- **(b) Drop `get` and `list`, keep `patch` namespace-wide.** One line, no
  ordering concern, removes the unused read of every pod in the namespace but not
  the cross-cluster write.
- **(c) Give the observer its own ServiceAccount with no Role at all.** Orthogonal
  to (a)/(b) and cheap: it needs nothing.
- **(d) Status quo.** Documented in
  [SECURITY_ARCHITECTURE.md](SECURITY_ARCHITECTURE.md) section 4.2.

Not implemented in *that* pass: `BuildSidecarRole` is production code and the sixth pass
owned documentation, tests and CI. Recommended order was (b) + (c) now, (a) with the
scale-up ordering test — which is the order it was executed in.

### NA55 — `--metrics-bind-address` is parsed and never applied; the operator metrics endpoint cannot be moved or switched off — DONE (2026-08-21)

**Status (tenth pass, 2026-08-21): implemented exactly as decided — flag wired through,
authentication filter still deliberately rejected.** `managerOptions` (`cmd/main.go`) now sets
`Metrics: metricsserver.Options{BindAddress: f.metricsAddr}`. The defect-documenting test
`TestManagerOptions_MetricsBindAddressIsNotWired` was replaced, per its own instruction, by
`TestManagerOptions_MetricsBindAddress` (non-default `:9090` reaches the options struct) and
`TestManagerOptions_MetricsDisabledByZero` (the literal `0` that disables the server).
ADR 0018 updated in the same change: Status amended, D8 rewritten as implemented with the
superseded wording kept in place, D9 rewritten (endpoint movable/disableable, still
unauthenticated — D10 unchanged), the open residual closed in place.
SECURITY_ARCHITECTURE.md hardening-checklist item rewritten accordingly. The chart passes
`--metrics-bind-address=:8080` explicitly, which equals the flag default, so the canonical
install behaves identically. Verified: `go build ./...`, `make test-unit` (12/12),
`make lint` (0 issues), `make vet`. Not reproduced against a cluster (no scrape of a moved or
disabled endpoint was measured).

**Decision (2026-08-21, Hans): wire the flag through only** —
`Metrics: metricsserver.Options{BindAddress: f.metricsAddr}` in `managerOptions` plus a test
that a non-default value reaches the options struct. The authentication filter
(`FilterProvider: filters.WithAuthenticationAndAuthorization`) was considered and **rejected
for now**: it would add `TokenReview`/`SubjectAccessReview` to the ClusterRole, which per
ADR 0014 means the kubebuilder marker, the Helm chart rule and the SECURITY_ARCHITECTURE
entry in one change — a deliberate trade to make separately, not a free hardening. Not
implemented in this pass (the pass was closed out early); the fix description above stands.

`bindOperatorFlags` declares `--metrics-bind-address` (default `:8080`,
[`cmd/main.go:62`](cmd/main.go)) and `managerOptions` (`:74-81`) builds
`ctrl.Options` **without a `Metrics` field**, so the flag value reaches nothing.
controller-runtime then defaults `Metrics.BindAddress` to `:8080`
(`sigs.k8s.io/controller-runtime@v0.24.1/pkg/metrics/server/server.go:45,163-166`),
which happens to equal the flag's default and the chart's argument
(`deployment.yaml`), so the mistake is invisible today.

Consequences: the endpoint is plain HTTP with no authentication filter and no
`SecureServing`, and there is **no supported way to move it or turn it off** —
`--metrics-bind-address=0`, the documented controller-runtime way to disable the
metrics server, is silently ignored. The payload is standard controller-runtime
and workqueue metrics; no Secret material and no CR contents, so this is exposure
of operational metadata, not of credentials.

Fix: `Metrics: metricsserver.Options{BindAddress: f.metricsAddr}` in
`managerOptions`, plus a test that a non-default value reaches the options struct.
Optionally `FilterProvider: filters.WithAuthenticationAndAuthorization`, which
adds a `TokenReview`/`SubjectAccessReview` grant to the ClusterRole — a
deliberate trade, not a free hardening.

Verified by reading `cmd/main.go` and the vendored controller-runtime default. Not
reproduced against a cluster.

## Follow-up work from the seventh pass (2026-08-21)

Three parallel **test-only** lanes plus a mutation audit, aimed at line coverage.
No lane was allowed to change production code, which is why this pass closes no
defect and files several: the lanes carried statement coverage from the sixth
pass's 87.3% to **95.55%**, and on the way they walked into four findings they
were not allowed to act on. Three concern production code — one corrects **NA49**
in place (the attacker does not have to wait for anything), the other two are
filed below as **NA56** and **NA57**. The fourth concerned a test and did not
survive checking; it is recorded as checked-and-dropped rather than filed.

The acceptance criterion for the new tests was announced to the lanes *before*
they started: every test they add must be able to fail. A mutation audit
enforced it, and three of the tests could not — the reason is the same in all
three cases and is the transferable part of this pass.

### Shape of this pass — what was run versus what was only read

| Target | Result |
|---|---|
| `make vet` | exit 0, no diagnostics |
| `make lint` | `0 issues.` |
| `make cyclo` | `✅ All functions are below complexity threshold 15` |
| `make test-unit` | **12** packages `ok`, 0 FAIL, 0 SKIP |
| `make test-unit-coverage` | 0 FAIL; `coverage/unit.out` → **4637/4853 = 95.55%** |
| mutation audit, 62 mutations over 6 files | 59 killed on first application, 3 survived (95.2%); **62/62** after the three tests were repaired |

**Disproved 2026-08-21 by the eighth-pass audit — the table above does not
reproduce on the committed tree, and the row that matters is rewritten here rather
than left standing.** Every compile-dependent row (`vet`, `lint`, `cyclo`,
`test-unit`, `test-unit-coverage`) fails at `a0ac61f` today, because the tree does
not build: `go.mod` is missing `golang.org/x/time v0.15.0 // indirect`. Filed as
**NA58**, with the reproduction. The two rows that survive the audit unchanged are
the coverage arithmetic — recomputed from the `coverage/unit.out` artifact and
exact to the statement — and the mutation-audit repairs, which were verified to be
in the tree by reading them. What cannot be reconstructed is *how* the four gates
were green when this section was written; the most likely explanation is a
transiently patched `go.mod` that was restored before `git status` was checked, but
that is a hypothesis and is labelled as one.

The original claim, kept so the correction is legible: all five `make` gates were
run by the orchestrator of the pass, and four of them — `vet`, `lint`, `cyclo`,
`test-unit` — were reported as **re-run independently while writing this section**,
from the clean tree. The fifth was not re-executed; instead the coverage total was recomputed
from the `coverage/unit.out` artifact with `awk` (sum of field 2 for the
denominator, field 2 where field 3 > 0 for the numerator) rather than taken from
the agents' reports, and the package table below matches that recomputation to the
statement.

**Verified by reading only, and by nothing else:** the mutation counts (the audit
itself was not re-run for this section — what was verified is that all three
repairs are in the tree, quoted below), the failure scenarios of NA56 and NA57,
and everything the NA49 correction says about the reconcile path. No cluster was
touched, and no e2e target ran in this pass — so **NA36 stays CODE-COMPLETE and
NOT EXECUTED**; the re-check is recorded in the item itself.

### The branch as committed — nine commits, and one caveat

Nine commits, `9e5634d` through `a0ac61f` inclusive — as a range that is
`git log 91b9647..a0ac61f`, since `9e5634d..a0ac61f` excludes its own base and
shows only eight. Working tree clean, nothing pushed:

| Commit | Subject |
|---|---|
| `9e5634d` | fix(rbac): grant delete on secrets and guard the chart ClusterRole against drift |
| `d583a35` | fix(controller): touch only PodDisruptionBudgets that are in effect |
| `cc7e034` | fix(sidecar): record the drain promotion and stop promoting into a live master |
| `30588bd` | fix(controller): bound every rolling update wait and record every promotion |
| `2357946` | fix(controller): resolve the steady-state split brain on evidence |
| `744b589` | fix(controller): keep the pass alive and report the master that serves writes |
| `4ec4a56` | ci: fail on generated manifests that are out of date |
| `34c351c` | test: raise statement coverage from 68 to 96 percent |
| `a0ac61f` | docs: document the privilege footprint, the upgrade path and the split-brain contract |

Cut at **file** granularity, ordered so the sidecar writes the drain stamp
(`cc7e034`) before the controller consumes it (`2357946`).

**Caveat, stated because it will bite someone otherwise: the series was NOT
verified to build commit by commit** — the user chose to skip that check
deliberately. `git bisect` over this range may therefore fail at intermediate
commits for reasons that have nothing to do with the bug being hunted:
`rolling_update.go` and `valkey_controller.go` each carry changes belonging to
several topics, and test helpers that earlier commits already use only land in
`34c351c`. Only the tip `a0ac61f` is known to build and pass. Bisect the tip
against `main`, not inside the range.

One consequence for reading this document: a file appearing in a commit does not
mean that commit's topic touched it. `test/e2e/topology_abandon_test.go` shows up
in `34c351c` purely because the cut followed files.

### Coverage: 68.4% -> 95.55% of statements

Session arc, both ends measured the same way: **3202/4682 = 68.4%** at the start
of the work, **4637/4853 = 95.55%** now. The denominator grew by 171 statements
because production code was added along the way (bounds, the evidence-based
split-brain resolution, the drain stamp), so the two percentages are not over an
identical body of code — the covered count nearly doubling is the honest headline.

| Package | Covered / statements | Coverage |
|---|---|---|
| `internal/builder` | 780/780 | 100.00% |
| `api/v1` | 394/394 | 100.00% |
| `internal/health` | 134/134 | 100.00% |
| `internal/common` | 37/37 | 100.00% |
| `internal/observer` | 315/322 | 97.83% |
| `internal/valkeyclient` | 253/259 | 97.68% |
| `internal/controller` | 2268/2384 | 95.13% |
| `internal/sidecar` | 315/333 | 94.59% |
| `cmd/observer` | 55/64 | 85.94% |
| `cmd/sidecar` | 37/46 | 80.43% |
| `cmd/migrate` | 37/53 | 69.81% |
| `cmd` | 12/47 | 25.53% |
| **total** | **4637/4853** | **95.55%** |

The total clears a 90% bar by 269 statements (4637 covered against the 4368 that
90% of 4853 would require). `internal/controller`, the sixth pass's named risk at
79.6%, is at 95.13%.

**Functions still at 0.0%, exhaustively — five, down from the sixth pass's
fifteen** (`go tool cover -func=coverage/unit.out`): `cmd/main.go:95 main`,
`cmd/migrate/migrate.go:43 Run`, `cmd/observer/observer.go:23 Run`,
`cmd/sidecar/sidecar.go:23 Run`, and
`internal/controller/valkey_controller.go:1897 SetupWithManager`. That is the same
`main`-wiring boundary as the package table: everything else in the repo is
entered by at least one test. The sixth pass's exhaustive list is marked
superseded in place in its own section.

The gap that section called "the largest genuine" one is closed:
`handleFailoverRetrigger` (`rolling_update.go:518`), `handleMasterFailover`
(`:1516`), `replaceRemainingPods` (`:1687`), `handleNewMasterFound` (`:1776`) and
`verifyNewMasterReady` (`:1979`) — the five functions reachable only through the
Sentinel dispatch, all at 0.0% one pass ago — are each at 100.0%, driven against a
RESP router in `internal/controller/sentinel_failover_test.go` rather than against
a real Sentinel. Unit coverage of that path is not e2e proof of it; what it buys is
that a mutation in any of the five now has a test that fails.

**The four packages below 90% are all `main`-package wiring and were left there on
purpose.** Together they hold 210 statements, 69 of them uncovered — 32% of every
uncovered statement in the repo. Reaching the rest means reaching `mgr.Start` and
signal delivery, which needs a live `rest.Config`; the integration suite already
exercises that path end to end with a real manager. Buying those 69 statements with
a fake `rest.Config` in a unit test would move the number and prove nothing.

**One coverage limit that is a missing production seam, not a missing test —
recorded here rather than as an NA item, because nothing about it is wrong at
runtime.** The 7 uncovered statements in `internal/observer/checks.go` cannot be
reached by the package's own RESP fakes: `discoverMasterViaProbe` (`:31`) and
`checkReplicaRead` (`:134`) build their addresses as
`fmt.Sprintf("%s-%d.%s:%d", ClusterName, i, ValkeyHeadlessSvc, port)` with `port`
hardcoded to 6379/16379 (`:32-34`, `:135-137`), and `Observer.newClient` (`:223`)
returns a concrete `*valkeyclient.Client`, so there is no seam through which a
fake listening on an ephemeral port can be addressed. `observer.Config`
(`internal/observer/observer.go:33-73`) has a `ValkeyHeadlessSvc` field but no
port field. Adding `ValkeyPort int` to `Config` would unlock all 7 — a two-line
production change with a real (if small) benefit of its own, since the port is
today an assumption rather than a configuration. Filed as an observation because
the current behaviour is correct: the operator always deploys these ports.

### The mutation audit — the criterion, and the three tests that could not fail

**Protocol.** 62 single-behaviour mutations, applied one at a time by
unique-string replacement into the live tree, then compile, then run the owning
package, then restore from a byte-exact backup with a `sha256` assertion so no
mutation could survive into the working tree. Distribution over the six files the
lanes claimed to cover: `rolling_update.go` 22, `valkey_controller.go` 19,
`internal/builder` 10, `steady_state_master.go` 5, `pdb.go` 4,
`internal/sidecar/drain.go` 2.

**Result.** 59 killed on first application, 3 survived — 95.2%. After the three
tests were repaired, 62/62.

**The lesson, and it generalises past this repo: a fixture that fails every write
from N onward cannot pin a write-ORDERING guarantee.** Breaking write 1 also
breaks write 2, so a pass that swallowed the first error still fails on the
second, still sends nothing on the wire, and still stores nothing. From outside
the two behaviours are indistinguishable, and the test that asserts "an error came
out" holds either way. Pinning "this write is the gate" needs a fixture that fails
**exactly** that write and lets the rest through.

Two of the three survivors were exactly that:

- **R2 / R19 — `TestHandleMasterFailover_SurfacesTheStateWriteFailure`
  (`internal/controller/sentinel_failover_test.go:685`) and
  `TestHandleFailoverRetrigger_SurfacesTheStateWriteFailure` (`:838`).** Both used
  `failCRUpdateFrom(1, &writes)` (`:351`), which rejects the first CR write and
  every one after it. With the state-write error swallowed by the mutation, the
  very next write — the failover timestamp — failed instead: same returned error,
  same absent `SENTINEL FAILOVER`, same empty state annotation. Every assertion
  held for the wrong reason.
  **Fix:** a new helper that fails exactly the nth write, with the reason recorded
  where the next person will read it (`:365-382`):

  ```go
  // failOnlyCRUpdate rejects exactly the nth Valkey CR update (1-based) and lets
  // every other one through. failCRUpdateFrom cannot pin a write-ordering
  // guarantee on its own: when it breaks the first write it breaks the second one
  // too, so a pass that ignored the first error would still fail on the second and
  // look identical from the outside.
  func failOnlyCRUpdate(n int, seen *int) interceptor.Funcs {
  ```

  Both tests now use `failOnlyCRUpdate(1, &writes)`. Their siblings
  `..._SurfacesTheTimestampWriteFailure` (`:710`, `:858`) keep `failCRUpdateFrom(2)`,
  and that is correct rather than an oversight: **no plain CR write stands between
  the timestamp write and the wire.** After `setFailoverTimestamp`
  (`rolling_update.go:1569`) the only write is `updatePhase` (`:1572`), which goes
  through `r.Status().Update` (`valkey_controller.go:1680`) and is therefore not
  seen by an `interceptor.Funcs{Update: …}` hook at all; the next observable event
  is `triggerSentinelFailover` (`:1577`). So a swallowed timestamp error is caught
  by the router assertion, not by the returned error, and the broader fixture
  cannot mask it. Checked for this section rather than assumed.
  **Completed 2026-08-21:** those three line numbers are the `handleMasterFailover`
  path only, while the sentence they justify covers both siblings. On the
  `handleFailoverRetrigger` path the argument is stronger, not weaker — there is no
  `updatePhase` at all between `setFailoverTimestamp` (`rolling_update.go:550`) and
  `triggerSentinelFailover` (`:554`), so nothing whatsoever stands between the
  timestamp write and the wire. The conclusion held; only half of its evidence had
  been written down.

- **R4 — `TestHandleMasterFailover_SkipsWhenAFailoverIsAlreadyInFlight/failover-reset`
  (`:484`).** The mutation targeted the in-flight guard
  (`internal/controller/rolling_update.go:1525-1529`). Its three disjuncts map
  one-to-one onto the three subtests, so a mutation of the `stateFailoverReset`
  disjunct is killable only by the `failover-reset` one — that mapping is an
  inference from the guard and the table-driven subtest, not a re-read of the
  audit's mutation text, and it is the only reading under which exactly one subtest
  survives. That subtest installed no `InstanceChecker`, so with the disjunct gone
  the pass fell through into `waitForReplicasReady`, could not read replication info
  against the `127.0.0.1` redirect every unit reconciler uses, and returned a
  requeue with the same `rollingUpdateRequeueDelay`, no wire traffic and no
  annotation change. Every assertion in the subtest held — for a pass that never
  reached the code they describe.
  **Fix:** install a healthy per-pod checker so everything behind the guard is
  reachable, and say why in the fixture itself — "*Everything behind the guard is
  healthy on purpose: without the state check the pass would run all the way to
  `SENTINEL FAILOVER`. With an unreachable checker it would stall in
  `waitForReplicasReady` and produce the same requeue, and the guard would go
  untested.*"

Both repairs share one shape: **an assertion that a bad thing did not happen is
only worth anything if the good path could have happened.** A test that blocks the
subject before the guard it is testing proves nothing about the guard.

### NA56 — the Sentinel-awareness bound can fail to arm: NA27 reintroduced through the arming path — DONE (option (a), plus the sync-wait sibling)

**Status (ninth pass, 2026-08-21): fixed as option (a), exactly with the obligations this
item names.** `ensureSentinelAwarenessTimestamp` delegates to `ensureWaitBound` and
`isSentinelAwarenessStalled` to `waitBoundExceeded`, under the new suffix
`boundSentinelAwareness`; the suffix is in `forgetWaitBounds` (covers
`clearRollingUpdateState` and `forgetNudges`), and the two mid-update reset sites drop the
in-memory copy — `incrementReconnectResetCount` and `clearSentinelAwarenessTimestamp` both
call `nudges.forget`, closing the NA28-one-layer-down hazard. Tests:
`TestSentinelAwarenessBound_HoldsWhenArmingWriteFails` (write rejected forever → bound armed
in memory → stalls after `sentinelAwarenessTimeout`) and
`TestSentinelAwarenessBound_ResetRebaselines` (both reset sites re-baseline), in
`internal/controller/rolling_update_bounds_test.go`.

**Converted in the same change: `ensureSyncWaitTimestamp`, the sibling this item missed.**
ADR 0010 (Status and Residual risks, as committed) already tracked it as the *second*
unconverted bound with the identical defect — `_ = r.Update`, no in-memory copy — and wider
scope: every non-Sentinel multi-replica rolling update, where `verifyReplacedReplicasSynced`
requeues forever and never reaches `pauseRollingUpdate`. Now `boundSyncWait` through the
same mechanism; `clearSyncWaitTimestamp` (the mid-update clear once all replicas are
synced) forgets the in-memory copy so the next sync wait of the same update starts with a
fresh budget. Tests: `TestSyncWaitBound_HoldsWhenArmingWriteFails`,
`TestClearSyncWaitTimestamp_ForgetsTheBound`. One pre-existing test rewritten:
`TestSyncWaitTimestamp_SetAndCheck` armed against a stale in-memory CR whose write silently
failed and asserted the annotation stayed on the object — exactly the behaviour the fix
removes; it now arms against the stored copy. ADR 0010's Status and Residual sections
rewritten in place (both bounds closed, guards named).

**This is NA27's exact defect, in the one wait bound of `rolling_update.go` that
NA27 did not convert.** NA45 item 2 filed it as readability hygiene ("five inline
RFC3339 stall checks that `waitBoundExceeded` could absorb"); that entry is
corrected in place, because one of the five is a live stall.

```go
// internal/controller/rolling_update.go:973-982
func (r *ValkeyReconciler) ensureSentinelAwarenessTimestamp(ctx context.Context, v *vkov1.Valkey) {
	...
	v.Annotations[annotationSentinelAwarenessStarted] = time.Now().UTC().Format(time.RFC3339)
	_ = r.Update(ctx, v)          // <- the error is discarded, and there is no second copy
}
```

`isSentinelAwarenessStalled` (`:988-1001`) reads the annotation and nothing else:
absent means `false`. Every other bound on this path goes through `ensureWaitBound`
(`:801`) / `waitBoundExceeded` (`:833`), which keep a second copy in the in-memory
`nudgeTracker` precisely so that a CR write that keeps failing cannot disarm the
bound — that is what NA27 fixed, and the doc comment at `:790-797` spells out the
failure it prevents.

**Failure scenario.** A fail-closed admission webhook on the `Valkey` CR, or a
permanently conflicting writer, makes every CR update fail. Sentinel has not yet
discovered the replicas (a fresh `SENTINEL RESET`, a slow INFO cycle, a
resource-starved cluster — the situation the 90 s
`sentinelAwarenessTimeout` at `:107` exists for). Then:

1. `ensureSentinelAwarenessTimestamp` writes, the write fails, the error is dropped.
2. The next pass reads the CR from cache: no annotation.
3. `isSentinelAwarenessStalled` answers `false`. Forever.
4. Both call sites requeue: `handleMasterFailover` at `:1558` and
   `handleFailoverRetrigger` at `:541`, each `RequeueAfter: 5 * time.Second`.
   (`:1541` in the first draft of this item was a transposition —
   `handleFailoverRetrigger` spans `:518-559`, so the requeue cannot be at 1541.
   Corrected 2026-08-21.)

Nothing bounds this from outside. `handleRollingUpdate` (`:423`) wraps the two
calls in no timeout, and in the `handleMasterFailover` case the rolling-update
state annotation is still empty, so `clearStaleRollingUpdateState` cannot intervene
either. The Sentinel rolling update parks before it ever sends
`SENTINEL FAILOVER`, at 5-second intervals, indefinitely. This is the NA47 shape
(unbounded park in a rolling-update phase) on the Sentinel path instead of the
manual-failover path.

**Scope, named per case rather than as a blanket:** Sentinel-enabled clusters
only; reachable only while CR writes fail persistently; costs availability of the
rolling update, not data — no destructive command is issued, and the cluster keeps
serving on the old image. The same precondition that makes NA27 reachable makes
this reachable, which is the argument for fixing them the same way.

**One nuance verified while writing this, because it changes what a reader would
expect:** unlike `ensureWaitBound`, this helper does **not** delete the annotation
back off the in-memory object after a failed write (`ensureWaitBound` does, at
`:823`, and explains why). So within the failing pass itself the check reads a
timestamp of age ~0 and still answers `false`; the next pass starts from a cache
copy that never had it. The end state is identical, but for two different reasons
in the two passes.

**Fix options.**

- **(a) Convert to `ensureWaitBound` / `waitBoundExceeded` with a fourth bound
  suffix.** Two lines at the arming and checking sites — and one thing more that
  must not be missed: `nudgeTracker.observe` is first-seen-wins
  (`internal/controller/nudge.go:62-73`), so the in-memory copy has to be dropped
  wherever the annotation is cleared today, namely `incrementReconnectResetCount`
  (`rolling_update.go:1906`) and `clearSentinelAwarenessTimestamp` (`:1926-1930`),
  both of which reset the baseline after a `SENTINEL RESET`. Without those
  `forget` calls a stale entry pre-expires the next attempt's budget and the
  operator proceeds into a failover Sentinel is not ready for — NA28 one layer
  down. The third clearing site, `clearRollingUpdateState` (`:2083`), needs
  nothing extra: it already calls `forgetWaitBounds` (`:848-852`, from `:2061`), so
  adding the suffix to that list covers the end-of-update path — but not those two
  mid-update resets. Cost: ~6 lines plus two unit tests (write-fails-forever still
  expires; a reset re-baselines).
- **(b) Keep the annotation, surface the write error.** Return it and let the pass
  fail loudly instead of parking. Smaller, but it converts a silent stall into a
  reconcile error loop on a cluster whose CR writes are already broken — worse
  behaviour, not better.
- **(c) Status quo.** Defensible only if one accepts that a cluster with
  permanently failing CR writes has bigger problems. NA27 already rejected that
  argument for the other three bounds; this item exists because the rejection was
  not applied here.

Recommended: (a), together with the four remaining hygiene folds of NA45 item 2 —
they touch the same functions.

**Verified by reading:** both helpers, both call sites and their requeue values,
the absence of an outer bound in `handleRollingUpdate`, the three existing bound
suffixes (`:771-773`), the first-seen-wins semantics of the tracker, and the two
annotation-clearing sites. **Not verified:** not reproduced against a cluster, and
no test exists today that fails on it.

### NA57 — `NewLabeler` is dead code, and the live path re-implements it — DONE (option (a))

**Status (ninth pass, 2026-08-21): deleted, as recommended.** `NewLabeler` and its two
tests (`TestNewLabeler_TLSConfigError`, `TestNewLabeler_NeedsAnInClusterConfig`) are gone;
the doc comment on `NewLabelerWithDeps` now records why there is no self-wiring constructor
(the detector/patcher pair is shared with the drain handler, so `runSidecar` is the single
wiring path). Nothing replaced the tests — per the verification below, both asserted error
strings already asserted directly elsewhere and in wrapped form on the live path. ADR 0012's
residual entry closed in place. `internal/sidecar` suite, lint, vet green.

`internal/sidecar/labeler.go:69-97` builds a `Labeler` from a `Config`: role
detector, pod patcher, and the Sentinel cross-check block. **Nothing in production
calls it.** `grep -rn "NewLabeler(" --include="*.go" .` returns exactly two call
sites, both in `internal/sidecar/labeler_test.go` (`:707`, `:717`), both asserting
error paths.

The live wiring does the same work in two places:

| Step | Dead copy (`labeler.go`) | Live path |
|---|---|---|
| role detector | `:70-73` | `run.go:57-60` (`Run`) |
| pod patcher | `:75-78` | `run.go:62-65` (`Run`) |
| construct `Labeler` | `:80-86` | `run.go:89` (`runSidecar`, via `NewLabelerWithDeps`) |
| Sentinel cross-check | `:88-94` | `run.go:91-100` |

The two cross-check blocks agree today — same guard `cfg.SentinelEnabled &&
cfg.SentinelAddrs != ""`, same `newSentinelMasterQuerier`, same abort on error,
same `podName + "." + headlessSvc` FQDN. That agreement is the hazard: two copies
of one wiring, one of them unreachable, both of them plausible-looking, and only
the reachable one has consequences if they drift. A change made in the wrong copy
is invisible in production and invisible in the tests, because the tests exercise
the other one.

**Coverage consequence, measured:** 7 of `labeler.go`'s 12 uncovered statements are
inside `NewLabeler` — blocks `80.2,88.52` (2), `88.52,90.17` (2), `90.17,92.4` (1),
`93.3` (1) and `96.2` (1) in `coverage/unit.out`. The other five sit at `246-251`
and `279`. So the dead function is the single largest uncovered region of the
package, and no test can honestly close it: covering the happy path of a
constructor nobody calls is coverage theatre.

**Fix options.**

- **(a) Delete `NewLabeler` and its two tests. Nothing has to replace them —
  checked, not assumed.** `TestNewLabeler_TLSConfigError` (`:706`) asserts
  `"building TLS config"` and `TestNewLabeler_NeedsAnInClusterConfig` (`:713`)
  asserts `"getting in-cluster config"`; both strings come from
  `newValkeyRoleDetector` (`labeler.go:196`) and `newKubernetesPodPatcher`
  (`:243`), and both are *already* asserted directly by
  `TestValkeyRoleDetector_TLSConfigError` (`labeler_test.go:513`) and
  `TestNewKubernetesPodPatcher_OutsideClusterFails` (`:586`). Their wrapped forms
  on the live path are covered too — `internal/sidecar/run_test.go:150` and `:163`
  drive `Run` and assert `"creating role detector"` / `"creating pod patcher"`. So
  the deletion removes two duplicate tests and 7 statements from the denominator
  and loses no coverage of anything reachable. Roughly 40 lines out, zero
  production risk.
- **(b) Make `runSidecar` call `NewLabeler`.** Collapses the duplicate block from
  the other side, but `NewLabeler` constructs its *own* detector and patcher while
  `runSidecar` receives them injected — and those same two objects are shared with
  the drain handler (`run.go:67`, `buildDrainHandler(cfg, detector, patcher)`).
  Calling `NewLabeler` would build a second pair, or force a signature change.
  More invasive than (a) and it costs the injection seam the sidecar tests rely on.
- **(c) Status quo, documented.** Acceptable; the cost is a permanent asterisk on
  the package's coverage number and a drift trap.

Recommended: (a).

**Not a runtime defect.** Nothing behaves wrongly today; this is dead code with a
drift hazard and a measurable coverage cost, filed because "two copies of one
piece of wiring" is precisely the class of thing this document has been tracking
(NA38, NA45 items 1 and 3).

**Verified by reading:** the grep result, both wiring paths line by line, the two
tests and what they assert, and the uncovered blocks in `coverage/unit.out`.

### One claim from this pass that did not survive checking — filed nowhere

A lane reported that `TestDrainHandler_WaitForRoleChange_TransientError_NotConnectionRefused`
never runs the branch it is named after: it installed the detector error 150 ms in
and cleared it 200 ms later, while `waitForRoleChange`
(`internal/sidecar/drain.go:295-320`) polls on a 1 s ticker, so the first poll
already saw the corrected role and the "retry a transient probe failure" branch was
never entered.

**The diagnosis was right and the item is nevertheless not filed: that test no
longer exists.** `git log -S` puts its removal in `cc7e034`, inside this branch's
own series; the diff replaces it with a pointer comment. The deterministic
replacements are in `internal/sidecar/drain_failure_paths_test.go`: a
`scriptedRoleDetector` (`:112-138`) that answers `DetectRole` from a fixed script
and repeats the last entry, plus
`TestDrainHandler_WaitForRoleChangeRetriesATransientProbeFailure` (`:145`), which
asserts `detector.calls() == 2` — that is, that the loop *polled again* rather than
merely that the call returned `nil` — and
`TestDrainHandler_TransientProbeFailureDoesNotFailTheDrain` (`:169`), which drives
the same script through `Handle`.

Recorded rather than dropped, because it is the same lesson as the
`failCRUpdateFrom` survivors in different clothing: **a sleep racing a ticker
cannot pin a retry, exactly as a fixture that breaks every write cannot pin a write
order.** In both cases the assertion passes on a run that never entered the branch.

### What this pass moved

| Item | Before | After |
|---|---|---|
| NA49 | DONE (2026-08-21) | **CLOSED** — option (a) verified against wds18-k8s-main (cert-manager v1.21.1, 119/119 Secrets), implemented as (a) + (a') + (b) + (e); the Certificate half of the same hazard, which predates NA37, was found and closed with it |
| NA45 item 2 | NOT DONE (hygiene, five inline stall checks) | **corrected in place** — one of the five is a live stall, split out as NA56; the other four remain hygiene |
| NA36 | CODE-COMPLETE, NOT EXECUTED | **unchanged**, re-check recorded in the item: no e2e target ran in this pass |
| NA53, NA54, NA55 | OPEN | **unchanged** — all three are production changes, and the lanes were test-only |
| NA56, NA57 | — | **new, OPEN** |
| sixth-pass "what is still uncovered, exhaustively — 15 functions have 0%" | current | **superseded in place** in the sixth-pass coverage section: ten of the fifteen are now at 100.0%, five remain |
| the drain transient-probe test | reported by a lane as a fourth defect | **not filed** — the test it names was already deleted in `cc7e034` and replaced by a deterministic pair; recorded above |

Nothing else changed status. The open list is now **NA36** (needs a cluster),
**NA43** and **NA45** (partially done), **NA53**,
**NA54**, **NA55**, **NA56**, **NA57**.

## Audit of this document (eighth pass, 2026-08-21) — read-only on the repo

No production file, test or manifest was touched. The pass re-verified this
document against the code as committed at `a0ac61f` and corrected it in place.
One new defect came out of it — **NA58** — and it is the reason the seventh
pass's gate table now carries a disproof block.

### What was checked, and what held

**Coverage arithmetic — recomputed, exact, nothing to correct.** `coverage/unit.out`
was re-summed independently (`awk`: denominator = sum of field 2, numerator = field 2
where field 3 > 0): **4637/4853 = 95.5491%**, so the `95.55%` in the seventh-pass
table is right, and every one of the twelve per-package rows reproduces to the
statement. So do the three derived figures: 269 statements above a 90% bar
(4637 − ⌈0.9 × 4853⌉ = 4637 − 4368), the four sub-90% packages at 210 statements
with 69 uncovered, and 69/216 = 32% of all uncovered statements in the repo. The
five functions listed at 0.0% are exactly the five that `go tool cover -func`
reports, and the ten functions the seventh pass claims it lifted to 100.0% are each
at 100.0%. NA57's measurement is exact too: `labeler.go` has 12 uncovered
statements, 7 of them in `NewLabeler` under precisely the five block ranges quoted.

**The four claims the audit was pointed at were all already handled correctly.**

- *"the known-master annotation is the single funnel"* — this claim does **not**
  stand anywhere in the document; `grep` for "single funnel" returns nothing. The
  code comment at `internal/controller/rolling_update.go:2094-2099` rejects it
  explicitly and names the other writers (`persistManualFailoverState`, plus
  `verifyTopologyRestored` / `finalizeMultiReplicaRollingUpdate` /
  `handlePostManualFailover`, which clear the state without passing through
  `recordPromotedMaster`). Nothing to rewrite.
- *the replication forensics* — recorded honestly as tried-and-abandoned, and the
  "left no trace in the code" claim verifies: no Go file mentions `master_replid2`
  or `second_repl_offset`, and `valkeyclient.ReplicationInfo`
  (`internal/valkeyclient/client.go:27-34`) has exactly the six fields the text
  claims, none of them a replication ID.
- *the structural rule and an operator-upgrade window* — no such claim exists in
  the document. Every mention of `couldNotHaveSelfElected` is correctly scoped, and
  the sixth pass states its blind spot itself (it can never exonerate pod-0).
- *the stamp-clearing invariant* — accurate. `clearDrainStamps` has exactly two
  production call sites, `rolling_update.go:761` and `:2112`, both cited exactly,
  and it does log-and-continue on failure as described
  (`steady_state_master.go:658-683`).

**The fifth and sixth passes kept the header rule.** NA50's residual was rewritten
in place rather than annotated, NA52's "only the unresolved case sets it" carries
its own correction, NA26 carries both amendments, and the sixth-pass 0%-function
inventory is marked superseded where it stands. No orphaned superseded claim was
found in either pass.

**Every OPEN item is still genuinely open**, checked against the code rather than
against the earlier text: NA43 (all three sites still overstate — `api/v1/valkey_types.go`
in both doc comments and `deploy/helm/valkey-operator/values.yaml`), NA45 items 2
and 4 (the five inline `time.Parse(time.RFC3339, …)` calls are still at `:996`,
`:1495`, `:2136`, `:2156`, `:2174`, each inside the named function; the `nudges`
comment at `valkey_controller.go:76-77` is verbatim as quoted), NA49 (~15
references, all exact; both pinning tests present with their inversion
instructions), NA53 (`promoteAndRedirect:2739`, the `masterIdx` skip at `:2764`,
the delete at `:2654`), NA54 (`BuildSidecarRole` still grants `get,list,patch`;
`labeler.go:283` is still the only clientset call in the package), NA55
(`managerOptions` at `cmd/main.go:74-81` still builds `ctrl.Options` with no
`Metrics` field), NA56 and NA57. Nothing was silently fixed by later work.

**No DONE item was found describing a fix that is not in the code.** Every
sampled fix from NA24 onward is present and does what its item says —
`armFinalizationBound` at both Phase 2 entries, `currentMasterPod` in its stated
authority order, the UID precondition at `pdb.go:265`, the envtest case, the
`generated-manifests` CI job, `clearSidecarUpdatePending` at
`rolling_update.go:204`, `waitOrAbandonManualFailover`, the evidence-based
adoption, the rollback, the recheck cadence. What had rotted was **line numbers and
three test names**, not substance.

### Line-number drift found and corrected — 17 references

In the **seventh-pass section and the new items**, 3 of roughly 60 references were
wrong; the rest resolve exactly.

| Where | Was | Is |
|---|---|---|
| NA56, the second requeue site | `rolling_update.go:1541` | **`:541`** — a transposition; `handleFailoverRetrigger` spans `:518-559` |
| NA57 | `labeler_test.go:512` | **`:513`** |
| NA57 | `labeler_test.go:585` | **`:586`** |

One further seventh-pass correction is not a wrong number but a half-written
argument: the "no plain CR write stands between the timestamp write and the wire"
paragraph justifies **both** `..._SurfacesTheTimestampWriteFailure` tests but cited
only the `handleMasterFailover` path. The retrigger path's own lines are now given;
the conclusion holds there more strongly, because that path has no `updatePhase` at
all.

In **older sections**, 14 references had drifted with the code and are refreshed in
place, each marked with the value it replaced: NA24 (2), NA39 (3 test *names* —
they were written `TestUpdateStandaloneStatus_*` and no such test exists; the tree
has `TestUpdateStatus_*`), NA45 item 3 (`crGet` is at `:48`, and its call-site count
is now labelled a sixth-pass snapshot rather than a current number), NA48 (2), NA50
(2 — `:151` is now the `masterPresent` early return the sixth pass added, not the
promote, which moved to `:159`), NA51 (4, the function moved ~72 lines), NA52 (3).

### NA58 — the committed tree does not build: `go.mod` is missing `golang.org/x/time` — DONE (fixed by commit 7521649, verified 2026-08-21)

**Status (ninth pass, 2026-08-21): fixed on the branch before this pass started, and now
verified.** Commit `7521649` ("fix(deps): restore the module requirements the rebase
dropped") added `golang.org/x/time v0.14.0` as a direct requirement; the branch has since
also merged `origin/main` (merge commit `9294ad9`). Reproduced from the current tree in this
pass: `go build ./...` exit 0, `make vet` clean, `make test-unit` green across all twelve
packages, `make test-integration` green, `make lint` 0 issues. The evidence caveat this
item raised for the fifth/sixth/seventh pass gate tables still stands for *those trees*;
from this pass on the gates reproduce.

**Found by re-running the seventh pass's own gates from the clean tree, which is
the only reason it surfaced.** Every compile-dependent gate this document reports
as green fails at `a0ac61f`:

```
$ make vet
../../go/pkg/mod/k8s.io/client-go@v0.36.4/util/workqueue/default_rate_limiters.go:24:2:
  missing go.sum entry for module providing package golang.org/x/time/rate
  (imported by github.com/guided-traffic/valkey-operator/internal/controller)
make: *** [vet] Error 1
```

`make test-unit` fails the same way in 8 of 12 packages (`[setup failed]`); only
`api/v1`, `internal/builder`, `internal/common` and `internal/valkeyclient` — the
four that do not reach `internal/controller` — still compile.

**Mechanism, and it is deterministic, not environmental.**

1. `internal/controller/ratelimiter.go:7` imports `k8s.io/client-go/util/workqueue`
   (the NA2 backoff cap), and at client-go **v0.36.4** that package imports
   `golang.org/x/time/rate`.
2. `go.mod` does not list `golang.org/x/time` at all — not even as `// indirect`.
   With the requirement absent, MVS takes the version from the dependency graph,
   where all four Kubernetes modules require **v0.14.0** (`go list -m
   golang.org/x/time` → `v0.14.0`).
3. `go.sum` carries hashes for **v0.15.0** only (lines 135-136). No v0.14.0 entry
   exists, so the build cannot verify the selected module and stops.

**Reproduced in isolation, not inferred.** `git archive HEAD` into a scratch
directory: `go build ./...` fails identically. Adding the single line
`golang.org/x/time v0.15.0 // indirect` to `go.mod` makes `go build ./...` exit 0
with no other change. That one line is the whole fix.

**Not caused by this branch, but carried by its tip.** `go.mod`/`go.sum` are
untouched by all nine commits (`git log 91b9647..a0ac61f -- go.mod go.sum` is
empty). `main` has the line (`git show main:go.mod`, line 61) and builds; this
branch does not. The branch's `go.mod` is also *ahead* of `main` on the Kubernetes
modules (v0.36.4 vs v0.36.3) — and v0.36.4 is the bump that introduced the
`golang.org/x/time/rate` import — so the branch took the dependency bump without
the requirement line that bump needs. `gopkg.in/yaml.v3 v3.0.1 // indirect` is
missing against `main` in the same way; it is not needed to build, only to keep
`go mod tidy` quiet.

**What this costs the record.** It is not primarily a code problem — one line fixes
it — but an evidence problem: the fifth, sixth and seventh passes all report green
`make` gates, and none of those runs can be reproduced from the tree they were
reported against. The seventh pass's table carries the disproof; the fifth and
sixth are older trees and were not re-run here, so their tables are left as
snapshots with this item as the caveat. How the gates were green when they were
reported cannot be reconstructed from here — a transiently patched `go.mod`
restored before `git status` was checked would explain it, and that is a
hypothesis, not a finding.

**Fix:** add `golang.org/x/time v0.15.0 // indirect` to `go.mod` (a plain
`go mod tidy` promotes it to a direct requirement and also restores
`gopkg.in/yaml.v3`; either is fine, the indirect line is the minimal change). Then
re-run the gates and update the fifth, sixth and seventh pass tables with numbers
that reproduce.

**Verified:** the failing build at `a0ac61f`, the failing `make vet` and
`make test-unit`, the selected version via `go list -m`, the absent requirement and
the v0.15.0-only `go.sum` pair, the one-line repair in a scratch copy, and that no
branch commit touched either file. **Not verified:** whether CI is currently red on
this branch — no network from here, and no run was inspected.

## Ninth pass (2026-08-21) — decisions recorded, five items closed

### The decisions, as made by Hans (2026-08-21)

| Item | Decision |
|---|---|
| NA53 | **Implement the demotion** of the outgoing master in `promoteAndRedirect` (recommended option) — done this pass |
| NA54 | **(b)+(c) now, (a) later** with the scale-up ordering test — (b) done this pass, (c) decided but not implemented, (a) deferred |
| NA55 | **Wire `--metrics-bind-address` through only**; the authentication filter is rejected for now (it would grow the ClusterRole — a separate, deliberate trade) — not yet implemented |
| NA36 / gates | **Unit/lint locally, e2e left to CI** — no local `e2e-local` run. Resolved the same day: the CI e2e legs had already been running the test on every push, four green runs verified from the logs → NA36 DONE, see its status block |

### What ran, from the current tree (all green)

`go build ./...`, `make test-unit` (12/12 packages), `make test-integration`,
`make lint` (0 issues), `make vet`, `go vet -tags=e2e ./test/e2e/` (compile check
only). `graphify update .` ran after the code changes. Not run: any e2e suite, any
cluster reproduction — by decision, see above.

### What changed, per item

- **NA56 DONE** — Sentinel-awareness bound converted to `ensureWaitBound`/`waitBoundExceeded`
  (option (a)) with the two reset-site `forget`s; **plus the sync-wait sibling** ADR 0010
  already tracked as the second unconverted bound. Four new tests, one legacy test rewritten.
- **NA45 DONE** — items 2 and 4: three hygiene folds into the new
  `annotationTimestampExceeded` helper (one edge nuance: `hasMinWaitElapsed` `>=` → `>`),
  and the `nudges` field comment.
- **NA57 DONE** — `NewLabeler` and its two tests deleted; `run.go` is the single wiring path.
- **NA53 DONE** — the outgoing master is demoted after the promotion, best-effort, with a
  per-target-pod recording test.
- **NA54 (b) DONE** — sidecar Role verbs are `["patch"]` only, pinned exactly in the builder
  unit test and the envtest integration test (both previously asserted `get`/`list` and were
  updated).
- **NA58 DONE** — verified fixed (commit `7521649` predates this pass); gates reproduce from
  the tree again.
- **Docs kept in the same change:** ADR 0010 (Status + Residual risks: both unconverted
  bounds closed), ADR 0012 (Status; D8 step 1 done; D9 rewritten as implemented;
  residual entries for the two-master window and `NewLabeler` closed in place),
  SECURITY_ARCHITECTURE.md (trust table, section 4.2, residual list).

### Current open list

**Superseded 2026-08-21 by the eleventh pass below.** As recorded in the ninth pass:
NA54 (a) and (c) were decided but not implemented. Both shipped on 2026-08-21; see the NA54
status block and the eleventh-pass open list at the end of this document.

NA43 and NA55 closed in the tenth pass (2026-08-21), see their status blocks.

NA36 closed later the same day: the CI single-node e2e leg had been executing the
test on every push all along; four green runs verified from the logs, all three
beyond-PASS checks held. See the NA36 status block.

## Eleventh pass (2026-08-21) — NA54 closed, two findings filed

### What changed

- **NA54 DONE** — (a) and (c) implemented; (b) had shipped in the ninth pass. ADR 0012 D8 is
  complete. The shipped (a) is **wider than D8 originally specified**: the `resourceNames`
  list is the union of the desired and the existing pods, decided by Hans before
  implementation because the specified derivation revokes a departing master's grant on the
  one write that fences it out of the `-rw` Service. Details, tests and measurements: the
  NA54 status block.
- **Docs in the same change** — ADR 0012 (Status second amendment; D8 steps 2 and 3 marked
  done with the superseded step-3 wording kept in place and the reason for the wider rule
  recorded; two residual bullets closed and four written, including the ones that are
  inherent rather than unfinished), SECURITY_ARCHITECTURE.md (trust table, the ascii
  diagram — which still showed the `get,list,patch` the ninth pass had removed, section 3
  isolation both halves, section 4.1 `escalate` paragraph, section 4.2 rewritten around the
  name list, two hardening items ticked and one new one written, one drifted line reference
  corrected).
- **New findings:** NA59 (one reconcile worker, and a stuck cluster blocks every other CR)
  and NA60 (the observer ServiceAccount is adopted by name).

### What ran, from the current tree (all green)

`make test-unit` (12/12), `make test-integration` (7/7, 15.7s), `make lint` (0 issues),
`make vet`, `make cyclo`, `make gosec` (0 issues), `make generate-all` + clean `git status`,
`go vet -tags=e2e ./test/e2e/`. **Not run:** any e2e suite, any cluster reproduction.

### NA59 — one reconcile worker: a cluster whose pods do not answer delays every other CR — DONE (2026-08-21, both halves)

**Status (twelfth pass, 2026-08-21): fixed on both levers, recorded as
[ADR 0019](docs/adr/0019-reconcile-concurrency-and-the-cost-of-a-stuck-pass.md).**

The check this item named as "the actual work" — whether any per-CR state outside the
workqueue assumes fleet-wide serialisation — came out **clean**, all read in this tree:
`nudgeTracker` is mutex-guarded and every key carries namespace plus CR name
(`internal/controller/nudge.go:56`, `waitBoundKey` at `internal/controller/rolling_update.go:785`);
the blocked-pass marker rides on the context, not on the reconciler, and its own comment
already said it was built for `MaxConcurrentReconciles > 1`
(`internal/controller/reconcile_blocked.go:139`); there is no package-level mutable state in
`internal/`; every managed object carries the CR name, including the NetworkPolicy, where
`spec.networkPolicy.namePrefix` is a prefix **in front of** it
(`internal/builder/networkpolicy.go:29`) — so no two CRs write the same object. Two further
facts that removed follow-up questions: controller-runtime v0.24.1 sets `rest.Config.QPS = -1`
(client-side throttling off, APF instead), and the chart runs `replicaCount: 1` with
`--leader-elect`, so there is never a second active manager.

Shipped, decided by Hans before implementation:

1. **Worker count.** `reconcileControllerOptions(maxConcurrent int)` sets
   `MaxConcurrentReconciles`; `SetupWithManager` passes `ValkeyReconciler.MaxConcurrentReconciles`;
   zero or less falls back to `DefaultMaxConcurrentReconciles = 4`, so the integration suite and
   every test helper get the decoupling too. Flag `--max-concurrent-reconciles` (`cmd/main.go`),
   chart value `maxConcurrentReconciles: 4` rendered into the Deployment args, documented in the
   README chart-values section.
2. **Cost of a stuck pass.** `findMaster` probes all ordinals concurrently
   (`internal/health/checker.go`), with the readiness gate and the dial moved into
   `probeMasterRole`. Results are collected **indexed by ordinal**, never in completion order,
   and the multi-master tie-break is `sort.SliceStable` plus that ordinal order — the previous
   `sort.Slice` was unstable, so equal `connected_slaves` counts already had an unspecified
   winner. `replicas x 5 s` in that function becomes one timeout.

**Deliberately not done:** the other 41 dial sites stay sequential — most sit on the
failover-critical rolling-update paths where ordering is the invariant. A per-pass
`context.WithTimeout` was rejected twice over: it would cut a pass mid-failover, and
`valkeyclient.Client` takes no context, so an in-flight dial would not be interrupted anyway.

**Verified, from the current tree:** `make test-unit` (12/12), `make test-integration` (8/8,
18.4s), `make lint` (0 issues), `make cyclo`, `make gosec` (0 issues), `make generate-all` with
a clean tree afterwards, `helm lint` + `helm template` showing `--max-concurrent-reconciles=4`,
`go test ./internal/health/ -race`. **Both new guards were mutation-checked:**
`TestReconcileConcurrency_StuckClusterDoesNotBlockOthers`
(`test/integration/reconcile_concurrency_test.go`) fails after 8.1 s with the worker count
forced back to 1, and `TestFindMaster_ProbesPodsConcurrently` fails at 1.51 s against its
900 ms bound with the probes back in a sequential loop; collecting candidates in completion
order instead of by ordinal fails `TestFindMaster_TiedMastersResolveToTheLowestOrdinal`.

**Not verified:** nothing was reproduced on a real cluster, no fleet was run at any worker
count, and the API-server load of four concurrent passes was not measured. Two existing tests
had to be relaxed from probe order to probe *set* because order no longer carries meaning —
`TestFindMaster_ScansEveryOrdinalOfTheStatefulSet` (which also had to take a mutex around its
interceptor, caught by `-race`) and the dial assertion of
`TestCheckCluster_ReportsTheMasterAndItsSyncedReplicas`, now sorted.

**Original finding (2026-08-21), left standing because the measurement is still the evidence:**

`SetupWithManager` passes `reconcileControllerOptions()`
([`internal/controller/ratelimiter.go:68`](internal/controller/ratelimiter.go)), which sets a
`RateLimiter` and **no `MaxConcurrentReconciles`**, so controller-runtime defaults it to 1.
Every `Valkey` CR in the cluster is reconciled by that single worker, in sequence.

The Valkey clients use a 5s dial/read/write timeout
([`internal/valkeyclient/client.go:152,161,171,180`](internal/valkeyclient/client.go)), and a
reconcile pass for a multi-replica cluster dials its pods. Measured in envtest, where no
kubelet ever starts a pod: one pass for a 5-replica CR occupied the worker for **15-25s**
(reconcileID `337c562e`, "Updating sidecar Role" at 13:37:08, pass end at 13:37:33), during
which a *different* CR created in another test got no reconcile at all and its test timed out
after 10s.

What this means outside envtest: any cluster whose pods stop answering — a node drain in
progress, a NetworkPolicy mistake, a hung Valkey — costs every other Valkey CR in the
fleet that much added latency per pass. It is a delay, not a deadlock, and per-CR
serialisation would be preserved by controller-runtime even at a higher concurrency (the
workqueue never runs two passes for the same key), so `MaxConcurrentReconciles: N` is the
obvious lever. **Not verified:** whether any per-CR state outside the workqueue (the
annotations the rolling update writes, the `nudges` map) assumes fleet-wide serialisation.
That check is the actual work of this item.

### NA60 — the observer ServiceAccount is adopted by name — DONE (2026-08-21, wider than filed)

**Status (fourteenth pass, 2026-08-21): fixed, and the fix is wider than this item asked for.
Recorded as [ADR 0020](docs/adr/0020-write-only-what-the-operator-owns.md).**

Two sentences below are **wrong** and are left standing because the corrections are the
finding. Both are recorded in the thirteenth-pass section above and in ADR 0020's Context.

1. *"The same shape exists for `<cr-name>-sidecar`"* implies the sidecar case is the same
   severity. It is not, and not for the reason this item assumes. Adopting the ServiceAccount
   transfers no capability at all: `BuildSidecarRoleBinding` names its subject **by name**
   without a UID, and `reconcileSidecarRoleBinding` never reads the ServiceAccount — so the
   grant lands on whatever holds that name whether the operator adopted it, created it, or no
   such object exists. Refusing the ServiceAccount closes nothing; only refusing the binding
   does.
2. *"the damage is limited to labels"* is false. Both ServiceAccount reconcilers assigned
   `current.Annotations = desired.Annotations`, and `desired` carries at most the
   operator-version key — every other annotation on the target was erased.

What shipped, decided by Hans one decision at a time before implementation (D1-D6 of ADR 0020):
the ownership guard on all four paths (observer ServiceAccount, sidecar ServiceAccount, Role,
RoleBinding); the observer refusal returns `nil` so the Deployment still runs; the sidecar
refusal reaches the RoleBinding through the ServiceAccount **and** the Role verdicts and fails
the pass; annotations merged instead of assigned; a distinct `ReconcileBlocked` reason
`ForeignObject` that outranks `AdmissionWebhookDenied`; and a 30 s recheck on both refusal
kinds so an administrator who removes the collision needs no CR edit and no operator restart.
The ADR 0006 residual on `reconcileSidecarRoleBinding` is closed with it, UID precondition
included.

**Original finding (2026-08-21), left standing:**

`reconcileObserverServiceAccount` fetches `<cr-name>-observer` by name and, if it exists,
rewrites its labels and annotations — it does not check `IsControlledBy` first. A principal
who may `create valkeys` in a namespace can therefore name a CR so the derived name collides
with a pre-existing ServiceAccount and have the operator relabel it, and the observer pod
then runs under someone else's identity. The same shape exists for `<cr-name>-sidecar` and
predates this work.

Why it is filed as low and not fixed in the same change: the observer sets
`automountServiceAccountToken: false`, so it mounts no token and gains **no capability** from
a foreign ServiceAccount; the damage is limited to labels on an object the CR author does not
own. The *destructive* half is already closed — `cleanupObserverServiceAccount` refuses to
delete a ServiceAccount the CR does not control and sends a UID precondition (ADR 0006).

The fix, when it is taken: refuse the adoption and record a Warning, the way
`warnPodDisruptionBudgetNotOwned` does (NA32 shows the shape, including the part that has to
stay quiet for CRs that never opted in). The open question that makes it a decision rather
than a chore: refusing means the observer Deployment names a ServiceAccount the operator does
not manage, so either the Deployment must be refused too or it runs under a foreign identity
anyway — the same trade ADR 0006 made for PDBs, but with a pod's identity instead of a
budget.

### Current open list (eleventh pass) — superseded by the twelfth pass below

**NA59** (concurrency decision) and **NA60** (ServiceAccount adoption by name). Both are new,
both are named above with what is verified and what is not. Nothing from NA1-NA58 is open.

## Twelfth pass (2026-08-21) — NA59 closed

### What changed

- **NA59 DONE** — both halves shipped: `MaxConcurrentReconciles` behind
  `--max-concurrent-reconciles` (default 4, chart value `maxConcurrentReconciles`) and the
  concurrent, order-independent `findMaster`. The shared-state audit that this item called its
  actual work came out clean and is recorded as a standing constraint (ADR 0019 D3), not as a
  one-time note. Details, decisions, tests and the mutation checks: the NA59 status block.
- **Docs in the same change** — new
  [ADR 0019](docs/adr/0019-reconcile-concurrency-and-the-cost-of-a-stuck-pass.md) with its index
  line under "Reconciliation and availability"; README chart-values section documents
  `maxConcurrentReconciles` and what it bounds.
- **New tests** — `test/integration/reconcile_concurrency_test.go` (envtest: a CR stuck in a
  data-plane probe must not delay another CR; the suite reconciler now carries a
  `slowProbeChecker` that delegates for every unmarked CR) and
  `internal/health/checker_parallel_test.go` (concurrency, determinism of the tie-break, and a
  `-race` exercise of the shared result slice).

### What ran, from the current tree (all green)

`make test-unit` (12/12), `make test-integration` (8/8, 18.4s), `make lint` (0 issues),
`make cyclo`, `make gosec` (0 issues), `make generate-all` + clean `git status`, `helm lint`,
`go test ./internal/health/ -race`. **Not run:** any e2e suite, any cluster reproduction.

### Current open list (twelfth pass) — superseded by the thirteenth pass below

**NA60** (the observer ServiceAccount is adopted by name) is the only open item. Nothing from
NA1-NA59 is open.

## Thirteenth pass (2026-08-21) — NA60 re-examined, two larger siblings filed

### What changed

Nothing in the code. This pass read the NA60 family against the tree before deciding how to
fix it, and the reading moved the finding twice.

- **NA60's causal claim is wrong, and the correction changes the fix.** The item reads as if
  adopting the ServiceAccount were what hands a foreign identity a capability. It is not.
  `BuildSidecarRoleBinding` writes `Subjects[0].Name = SidecarServiceAccountName(v)` — a plain
  name with no UID ([`internal/builder/rbac.go`](internal/builder/rbac.go), `BuildSidecarRoleBinding`)
  — and `reconcileSidecarRoleBinding`
  ([`internal/controller/valkey_controller.go`](internal/controller/valkey_controller.go))
  never reads the ServiceAccount object at all. The grant to
  `system:serviceaccount:<ns>:<cr>-sidecar` therefore lands identically whether the operator
  created that ServiceAccount, overwrote a foreign one, or no such object exists. **Refusing
  the ServiceAccount write closes nothing; only refusing the RoleBinding fails closed on the
  grant.**
- **NA60's severity sentence is wrong in the other direction too.** "the damage is limited to
  labels" does not hold: both ServiceAccount reconcilers assign `current.Annotations =
  desired.Annotations` wholesale, and `desired` carries at most the operator-version key
  (`ApplyOperatorVersion`, [`internal/builder/annotations.go`](internal/builder/annotations.go)).
  A pre-existing ServiceAccount therefore loses every annotation it had, including
  `eks.amazonaws.com/role-arn`, `iam.gke.io/gcp-service-account` and
  `kubernetes.io/enforce-mountable-secrets`. That is an availability and
  privilege-configuration impact on a foreign object, not a relabel. It applies to
  operator-owned ServiceAccounts too, where an ownership guard does not help — see the merge
  question in the open list.
- **The shape is repo-wide, and PodDisruptionBudget is its only exception.** On the reconcile
  write path exactly one managed kind checks provenance before writing: `reconcilePodDisruptionBudget`
  ([`internal/controller/pdb.go`](internal/controller/pdb.go)), which refuses and warns. Every
  other kind — StatefulSet, Service, ConfigMap, NetworkPolicy, Deployment, Role, RoleBinding,
  ServiceAccount, ServiceMonitor, Certificate — is written by generated name with no ownership
  check. [ADR 0006](docs/adr/0006-delete-only-what-the-operator-owns.md) D1 binds deletions
  only; its Context already records the update half as the failure that motivated the PDB
  guard, without generalising the fix beyond PDBs.
- **Two siblings filed: NA61 and NA62.** Both are larger than NA60 and neither was ticketed.

### What ran, from the current tree

Reading only. `reconcileObserverServiceAccount`, `reconcileSidecarServiceAccount`,
`reconcileSidecarRole`, `reconcileSidecarRoleBinding`, `reconcileStatefulSet`,
`reconcileSentinelStatefulSet`, `reconcileServiceMonitor`, `reconcileCertificate`,
`reconcilePodDisruptionBudget`, `cleanupObserverServiceAccount`,
[`internal/builder/rbac.go`](internal/builder/rbac.go),
[`internal/builder/observer.go`](internal/builder/observer.go),
[`internal/builder/statefulset.go`](internal/builder/statefulset.go),
[`internal/sidecar/labeler.go`](internal/sidecar/labeler.go),
[`internal/sidecar/drain.go`](internal/sidecar/drain.go),
[`internal/common/labels.go`](internal/common/labels.go), ADR 0006.
**Not run:** no test target, no build, no cluster reproduction. Nothing in this pass was
executed.

### NA61 — the data and Sentinel StatefulSets are written by generated name with no ownership check — DONE (2026-08-22)

**Status: DONE.** Shipped in the fifteenth pass as the ADR 0020 amendment (D1 extended, D7
superseded for these kinds, new D8); details, decisions and verification in the
[fifteenth pass](#fifteenth-pass-2026-08-22--na61-closed-na63-filed) below. The analysis
that follows describes the tree **before** the fix and is kept as the filing record; both
outcomes it derives are now pre-empted by the guard, which refuses the write before the
apiserver ever sees it.

`reconcileStatefulSet` applies `SetControllerReference` to `desired`, Gets by name, and on the
existing-object branch writes `current.Spec.Replicas`, `current.Spec.Template` and
`current.Labels` before `Update` — with no `metav1.IsControlledBy`
([`internal/controller/valkey_controller.go`](internal/controller/valkey_controller.go),
`reconcileStatefulSet`). `reconcileSentinelStatefulSet` is the same function shape. The step
is wired without a `when` predicate, so it runs on every pass of every CR.

The name is the bare CR name: `common.StatefulSetName(v, ComponentValkey)` returns `v.Name`
([`internal/common/labels.go`](internal/common/labels.go)) — not an operator-suffixed name like
`-observer` or `-sidecar`, but exactly the name an arbitrary existing StatefulSet is most
likely to carry. The Sentinel half is `<cr>-sentinel`.

**Two outcomes, and which one applies is not verifiable from this repository.**

* The likely one is a **rejected write, not a destroyed workload**. `spec.selector` is not
  among the fields written and is immutable after creation, so an Update that installs a pod
  template whose labels do not match the existing selector is refused by the apiserver. A
  foreign StatefulSet almost certainly has a different selector. The CR then ends every pass
  in error: permanent `ReconcileBlocked`, error phase, endless requeue
  ([ADR 0001](docs/adr/0001-continue-reconciling-past-a-rejected-write.md),
  [ADR 0002](docs/adr/0002-surface-a-blocked-reconcile-on-the-cr.md)). That is a denial of
  service against the *new* CR, aimed by whoever named it.
* The destructive one needs a foreign StatefulSet whose selector already matches this
  operator's data-pod selector labels. Then the Update succeeds and replaces the pod template.
  `spec.updateStrategy` is also not written, so the victim keeps its own — a default
  `RollingUpdate` rolls its pods to the Valkey template immediately, without the `OnDelete`
  pacing the operator relies on for its own set
  ([ADR 0007](docs/adr/0007-failover-aware-rolling-update.md)).

**Verified from this tree:** the missing ownership check, the exact fields written, the
unconditional wiring, and that the data StatefulSet name is the bare CR name. **Not verified:**
the apiserver's selector-immutability and selector-matches-template validation is upstream
Kubernetes behaviour and was not reproduced here; no collision was run against a cluster; and
nothing establishes how common a selector-matching foreign StatefulSet is in practice.

The same rejected-write reasoning applies to `reconcileObserverDeployment`, which writes
`current.Spec = desired.Spec` — the whole spec including the immutable `selector` — so a
foreign Deployment under `<cr>-observer` produces the same permanent error rather than a
takeover. Filed here rather than as a third item because it is the same mechanism.

### NA62 — ServiceMonitor and Certificate stamp the CR ownerReference onto an object they never verified — DONE (2026-08-22, wider than filed)

**Status: DONE.** Closed together with the four typed write paths and the three name-only
deletes; see the sixteenth pass at the end of this file for the decisions, what shipped and
what was verified. The filing below stands as written, with two corrections recorded in that
pass: the ownerReference stamp is not the only harm on those two paths (the spec write
repoints a foreign Certificate's `secretName` and `issuerRef` before any CR is deleted), and
the fix shape is therefore ADR 0020 D1 after all, not the narrower "do not stamp" rule this
item proposed.

The two `unstructured` reconcilers go one step further than the typed ones. Both build an
ownerReference with `Controller: true` and `BlockOwnerDeletion: true`, and on the
existing-object branch write it onto `current`:

* `reconcileServiceMonitor` — `current.SetOwnerReferences(desired.GetOwnerReferences())`
  ([`internal/controller/valkey_controller.go`](internal/controller/valkey_controller.go)),
  gated on `IsServiceMonitorEnabled()`.
* `reconcileCertificate` — the identical line, gated on `IsCertManagerEnabled()`.

Neither checks `metav1.IsControlledBy` first. A pre-existing ServiceMonitor under the derived
name, or a pre-existing cert-manager Certificate under `<cr>-tls` / `<cr>-sentinel-tls`,
therefore acquires this CR as its controller owner. **Deleting the CR then garbage-collects a
foreign object.** For the Certificate that ends someone else's issuance and renewal, and the
Secret it manages goes with it.

This is precisely the harm [ADR 0006](docs/adr/0006-delete-only-what-the-operator-owns.md) D1
forbids, arriving through a door D1 does not watch: the operator issues no `Delete`, so no
provenance proof and no UID precondition is ever consulted. The delete side of the very same
Certificate *is* guarded — the legacy-cleanup path checks `IsControlledBy` — which makes the
asymmetry accidental rather than considered. D14 states that every child the operator creates
carries an ownerReference to the CR; nothing states that an object the operator did **not**
create must not be given one.

Unlike NA61 there is no immutability backstop: these are `unstructured` writes of `spec` plus
metadata, and an ownerReference is freely mutable, so the Update succeeds.

**Verified from this tree:** both call sites, both ownerReference constructions, both feature
gates, and that neither path calls `IsControlledBy` before the write. **Not verified:** no
collision was reproduced, and Kubernetes garbage-collector behaviour on the stamped
ownerReference is upstream behaviour asserted from the API contract, not observed here.

### Current open list (thirteenth pass) — superseded by the fourteenth pass below

**NA60** (ServiceAccount adoption by name, with the two corrections above), **NA61** (the
StatefulSets are written by generated name) and **NA62** (ServiceMonitor and Certificate
adopt ownership of a foreign object). Nothing from NA1-NA59 is open.

The open decisions that gate a fix — scope, the refusal point for the sidecar, merge versus
replace on metadata, which ADR carries the rule, and how the refusal is surfaced without
reintroducing the NA32 warning noise — are being taken separately and will be recorded with
the change that implements them.

## Fourteenth pass (2026-08-21) — NA60 closed

### What changed

- **NA60 DONE**, wider than filed: the ownership guard covers the observer ServiceAccount and
  the whole `<cr-name>-sidecar` triple, and the refusal is routed so it actually closes the
  grant. Details, the two corrected sentences and the shipped decisions: the NA60 status block.
- **New code** — [`internal/controller/foreign_object.go`](internal/controller/foreign_object.go)
  (the `errForeignObject` sentinel, four Event reasons, the per-pass `passState` recheck
  channel and its `applyRecheck` fold, the warn helper). Four reconcilers in
  [`internal/controller/valkey_controller.go`](internal/controller/valkey_controller.go) carry
  the guard; `reconcileSidecarServiceAccount` and `reconcileSidecarRole` now return
  `(bool, error)` like the PDB precedent. `reconcileBlockedReason` gained the new reason and
  its precedence.
- **Docs in the same change** — new
  [ADR 0020](docs/adr/0020-write-only-what-the-operator-owns.md) with its index line under
  "Security and API surface"; ADR 0006 Status and the `reconcileSidecarRoleBinding` residual
  marked closed, plus a new residual naming the still-unguarded write path; ADR 0012 third
  amendment closing its own residual and correcting the bounded-consequence claim in place;
  ADR 0013 two stale bullets corrected — the sidecar grant is no longer described as
  namespace-wide `get,list,patch`, the observer half is closed, and the name-collision surface
  is named per case; SECURITY_ARCHITECTURE.md section 3 both halves, plus one ticked and one
  rewritten hardening item.

### What ran, from the current tree (all green)

`make test-unit` (12/12), `make test-integration` (10/10, 20.0s), `make lint` (0 issues),
`make vet`, `make cyclo`, `make gosec` (0 issues), `make generate-all` + clean `git status`.
**Three mutation checks**, each reverted afterwards: dropping the `!saOwned` return fails
`TestReconcileSidecarRBAC_WritesNoGrantWhenTheServiceAccountIsForeign`; restoring
`current.Annotations = desired.Annotations` fails
`TestReconcileObserverServiceAccount_MergesAnnotationsOnAnOwnedServiceAccount`; removing the
UID precondition fails `TestReconcileSidecarRoleBinding_RecreateCarriesTheUIDPrecondition`.
**Not run:** any e2e suite, any cluster reproduction.

### Current open list (fourteenth pass) — superseded by the fifteenth pass below

**NA61** (the StatefulSets are written by generated name) and **NA62** (ServiceMonitor and
Certificate stamp the CR ownerReference onto a foreign object). Nothing from NA1-NA60 is open.

## Fifteenth pass (2026-08-22) — NA61 closed, NA63 filed

### The decision, as made by Hans (2026-08-22)

Strict proof only: `metav1.IsControlledBy`, no second evidence channel. An auto-re-own on
structural evidence (operator label set + generated selector) was offered and declined —
it would break the "a label is not a proof" line of ADR 0006 D2 / ADR 0020 D1 and would
adopt a crafted mimic object together with its immutable `volumeClaimTemplates`. Hans
raised the 1.10.x upgrade question; answered and verified before implementation, see below.

### What changed

- **Write guards** — `reconcileStatefulSet` and `reconcileSentinelStatefulSet` refuse a
  StatefulSet the CR does not control and fail their step (`errForeignObject` →
  `ReconcileBlocked/ForeignObject`, phase `Error`); `reconcileObserverDeployment` refuses
  without failing and requests the 30 s recheck, mirroring the observer ServiceAccount
  (ADR 0020 D2). New Event reasons `StatefulSetNotOwned`, `SentinelStatefulSetNotOwned`,
  `ObserverDeploymentNotOwned` in
  [`internal/controller/foreign_object.go`](internal/controller/foreign_object.go).
- **Treat-as-absent (new ADR 0020 D8)** — a foreign StatefulSet is invisible to every other
  consumer: `nudgeStatefulSet` (the nudge patch is a write), `checkAndHandleRollingUpdate`
  and `handlePostFailover` (pod deletes against a template that is not ours),
  `checkAndHandleSentinelRollingUpdate`, `sentinelRolloutComplete` (foreign = trivially
  complete), `updateStatus` and `updateHAStatus` (no status from foreign replica counts).
  Only the two reconcilers report, so one Event series per pass.
- **Delete guard** — the Deployment half of `cleanupObserverDeployment` now requires
  `IsControlledBy` and sends the UID precondition, closing its ADR 0006 residual entry;
  the NetworkPolicy half stays name-only and stays listed there.
- **Docs in the same change** — ADR 0020: Status amendment, D1 strictness + upgrade
  verification, D2 fail directions for the three paths, D7 superseded in place, new D8,
  two new Consequences bullets, two new Alternatives (auto-re-own rejected; D8-without-D1
  rejected), Residuals rewritten (remaining unguarded kinds, NA63 pod door, selector
  backstop unverified); ADR 0006 residual bullet rewritten; SECURITY_ARCHITECTURE.md
  section 3 both halves plus one ticked and one new open hardening item.

### The upgrade question (raised by Hans), answered

An operator upgrade replaces the operator Deployment, never the CR — the CR UID the guard
compares against survives. And every release ever built stamped the controller reference on
create: verified `git show` on the first commit of each reconciler — data StatefulSet
`b0081d9`, Sentinel StatefulSet `88b721b`, observer Deployment `c6f97e2`. So no
operator-created object is refused after an upgrade. Refused is only what lost the
reference out-of-band (CR orphan-delete + recreate, restore with a UID change, hand edit);
recovery is downtime-free: `kubectl delete sts <name> --cascade=orphan` keeps the pods, the
operator recreates the StatefulSet, the statefulset-controller re-adopts the orphans
(upstream behaviour, asserted not reproduced). The from-previous-release upgrade e2e
(`e282cc2`) creates its fleet with the real prior image and must stay green — not run
locally in this pass, covered by CI.

### What ran, from the current tree (all green)

`make test-unit` (13/13 packages), `make test-integration` (41.0s, including the new
`TestForeignDataStatefulSet_Integration` with its recovery half), `make lint` (0 issues),
`make vet`, `make cyclo` (all < 15), `make gosec` (0 issues), `make generate-all` + no
generated diffs. **Four mutation checks**, each reverted after the run: disabling the data
StatefulSet guard fails `TestReconcileStatefulSet_RefusesAForeignStatefulSet` and
`TestReconcileResources_ForeignDataStatefulSetFailsThePass`; disabling the rolling-update
guard fails `TestCheckAndHandleRollingUpdate_TreatsAForeignStatefulSetAsAbsent`; disabling
the nudge guard fails `TestNudgeShortStatefulSets_DoesNotNudgeAForeignStatefulSet`;
disabling the cleanup ownership check fails
`TestCleanupObserverDeployment_LeavesAForeignDeploymentAlone`. Ten new unit tests in
[`internal/controller/foreign_object_test.go`](internal/controller/foreign_object_test.go);
existing fixtures gained the controller reference (`controllerRefTo`) they were implicitly
relying on not being checked. **Not run:** any e2e suite, any cluster reproduction; the
selector-immutability claim from the filing remains unreproduced and is no longer
load-bearing.

### NA63 — steady-state pod probes and commands never verify pod provenance (the pod door) — DONE (2026-08-22, wider than filed)

**Status: DONE.** Closed together with two doors this filing does not name and with the six
pod deletes in the rolling update; see the seventeenth pass at the end of this file. The
filing below stands, with one correction recorded there: it names the *most expensive* door
to use and misses the two cheap ones, so its severity reading is upside down.

Two paths act on pods derived from the CR alone, not from the (now guarded) StatefulSet:

* `checkAndRecoverNoMaster`
  ([`internal/controller/valkey_controller.go`](internal/controller/valkey_controller.go))
  probes `<cr>-0..N-1` by generated name for `i < spec.Replicas` and, on a unanimous
  no-master answer, promotes pod-0 (`REPLICAOF NO ONE`). Gated to multi-replica
  non-Sentinel CRs outside a rolling update; any unreachable pod aborts it.
* `checkSteadyStateSplitBrain`
  ([`internal/controller/steady_state_master.go`](internal/controller/steady_state_master.go))
  acts on `listMasterLabeledPods` — label selection — and its resolver demotes pods with
  `REPLICAOF`, the data-discarding command the whole master-authority design guards.

Neither checks `metav1.IsControlledBy` on the pods. A foreign pod that carries the derived
name (a same-named foreign StatefulSet produces exactly those) or the crafted label set
receives Valkey admin commands from the operator.

**The bound, verified from the code:** every probe and command authenticates with the CR's
own credentials (`checker` reads `spec.auth`), so a foreign Valkey with a different
password refuses, the probe errors, and `checkAndRecoverNoMaster` refuses to act on any
unreachable pod. **The unbounded case:** a CR without `spec.auth` aimed at unauthenticated
foreign pods — no backstop. **Not verified:** no collision was reproduced; whether the
resolver paths abort as cleanly as the recovery path on partial reachability was read but
not exercised.

Also deliberately left out of NA61 and still open: the Service/ConfigMap/NetworkPolicy
write paths (ADR 0020 Residuals — the Service selector is mutable, so that family lacks
even the immutability backstop), and the NetworkPolicy half of `cleanupObserverDeployment`
(ADR 0006 residual).

### Current open list (fifteenth pass) — superseded by the sixteenth pass below

**NA62** (ServiceMonitor and Certificate stamp the CR ownerReference onto a foreign object)
and **NA63** (the pod door). Nothing from NA1-NA61 is open.

## Sixteenth pass (2026-08-22) — NA62 closed, D7 retired, ADR 0006 residual list emptied

### The decisions, as made by Hans (2026-08-22)

Four questions, four answers, all taken before any code was written:

1. **Fix shape: the full ADR 0020 D1 guard**, not the narrow "do not stamp an ownerReference"
   rule the item was filed with and D7 predicted. What decided it was a fact the filing does
   not name: the same Update branch writes `current.Object["spec"] = desired.Object["spec"]`,
   which for a Certificate replaces `issuerRef`, `dnsNames` and `secretName`. cert-manager
   then maintains this cluster's Secret and abandons the other party's — no CR deletion
   involved. A stamp-only fix leaves that open and costs a guard anyway.
2. **Fail directions: Certificate fails the step, ServiceMonitor does not.** The data
   StatefulSet mounts the TLS Secret by name (`internal/builder/statefulset.go:540`), so a
   foreign Certificate under `<cr>-tls` means either no Secret or one with foreign SANs.
   Scraping is observability and takes the observer's direction.
3. **Scope: all five remaining kinds, D7 dies.** Once the shape is identical the reason for
   splitting is gone, and leaving three kinds unguarded would give the tree three answers to
   one question. The typed three are not harmless: `reconcileService` writes
   `current.Spec.Selector`, which is mutable — a foreign Service is taken over with no
   immutability backstop at all — and `reconcileConfigMap` overwrites `current.Data`.
4. **Already-adopted objects: document, do not detect.** Verified and decisive: the stamping
   Update also ran `current.SetLabels(desired.GetLabels())`, replacing the label map, and
   `ApplyOperatorVersion`. A stamped foreign object is byte-identical in metadata to a real
   child, so any heuristic must guess, and its false positive strips a genuine child's
   ownerReference and takes the CR down on the next pass.

A fifth question came out of the verification rather than the filing and was decided the same
way: **the three name-only deletes go in too.** `cleanupMetricsService`,
`cleanupServiceMonitor` and the NetworkPolicy half of `cleanupObserverDeployment` delete
whatever holds the derived name, and the trigger is not an accident but a feature flag —
`spec.metrics.enabled`, `spec.metrics.serviceMonitor.enabled`, `spec.observer.enabled` — which
the CR author owns. Refusing to write an object while still deleting it when a boolean flips
is not a defensible state.

### What changed

- **Write guards** on the five remaining kinds, all in
  [`internal/controller/valkey_controller.go`](internal/controller/valkey_controller.go):
  `reconcileService`, `reconcileConfigMap`, `reconcileReplicaConfigMap`,
  `reconcileSentinelConfigMap`, `reconcileNetworkPolicy` and `reconcileCertificate` refuse and
  fail their step; `reconcileServiceMonitor` refuses without failing and requests the 30 s
  recheck. New Event reasons `ServiceNotOwned`, `ConfigMapNotOwned`, `NetworkPolicyNotOwned`,
  `ServiceMonitorNotOwned`, `CertificateNotOwned` in
  [`internal/controller/foreign_object.go`](internal/controller/foreign_object.go) — one per
  kind, since three ConfigMap reconcilers and six Service callers share their functions.
- **Two caller-side downgrades.** `reconcileService` serves six callers and
  `reconcileNetworkPolicy` three, so two fail directions live in the caller that knows why it
  is writing: `reconcileMetricsService` and the observer branch of `reconcileNetworkPolicies`
  convert `errForeignObject` into `nil` plus the recheck. Putting them in the reconcilers
  would make the direction depend on which builder produced `desired`.
- **Treat-as-absent (ADR 0020 D8) extended to the replica ConfigMap.** `replicaConfigMaster`
  ([`internal/controller/steady_state_master.go`](internal/controller/steady_state_master.go))
  reads the live object and returns its `replicaof` target as the published master, which
  feeds the steady-state resolver that issues `REPLICAOF`. A foreign ConfigMap now reports
  "unknown". A sweep found no other consumer of the remaining kinds; the one Certificate
  reader (`deleteLegacySentinelCertificate`) was already ownership-checked.
- **Delete guards** through a new shared `deleteIfOwned` (`foreign_object.go`), which
  `cleanupMetricsService`, `cleanupServiceMonitor`, the NetworkPolicy half of
  `cleanupObserverDeployment` and the already-guarded `cleanupObserverServiceAccount` now all
  use. It carries the `IsControlledBy` proof, the `client.Preconditions{UID}` and the
  Conflict-is-not-a-failure branch in one place.
- **Docs in the same change** — ADR 0020: two Status amendments, a fourth Context failure, D1
  widened to every kind with two new corollaries, D2 gained the per-path table and the
  NetworkPolicy exception, D5 the full Event-reason list, **D7 superseded in full and its
  wrong sentence corrected in place**, D8 the replica-ConfigMap half, four Consequences, four
  Alternatives, Residuals rewritten. ADR 0006: Status amendment and the name-only-cleanup
  residual closed, leaving `deleteLegacyServices` as the only open item.
  SECURITY_ARCHITECTURE.md: section 3 rewritten, two hardening items ticked and one new one
  added (look for already-adopted objects before upgrading).

### The upgrade question, answered again

Same check as NA61, repeated for all six newly guarded reconcilers, and clean: every one
stamped the controller reference in its very first commit — `reconcileService` and
`reconcileConfigMap` `b0081d9`, `reconcileReplicaConfigMap` `88b721b`, `reconcileNetworkPolicy`
`6aa85f1`, `reconcileCertificate` `aa259fc`, `reconcileServiceMonitor` `28b6830`. For the two
`unstructured` ones that is not enough on its own, because `IsControlledBy` requires
`Controller: true` rather than any ownerReference; both set `isController := true` in that
same first commit, verified by reading the function bodies out of `git show`. So no
operator-created object is refused after an upgrade.

### What ran, from the current tree (all green)

`make test-unit` (13/13 packages), `make test-integration` (40.9 s, including the two new
envtest cases), `make lint` (0 issues), `make vet`, `make cyclo` (all < 15), `make gosec`
(0 issues), `make generate-all` with no generated diffs.

**Nine mutation checks**, each reverted after the run — every guard was disabled in turn and
the run had to go red: `reconcileService` (2 tests), `reconcileServiceMonitor` (1),
`reconcileCertificate` (2), `reconcileConfigMap` (1), `reconcileReplicaConfigMap` (1),
`reconcileSentinelConfigMap` (1), `reconcileNetworkPolicy` (2), the `replicaConfigMaster` D8
read (1) and `deleteIfOwned` (3). The first two attempts on the last two mutations reported a
false negative — replacing the condition with `if false` made `metav1` an unused import and
the package stopped compiling, so nothing "failed". They were re-run as `&& false`, which
keeps the import live, and both went red as required.

**Fifteen new unit tests** in
[`internal/controller/foreign_object_test.go`](internal/controller/foreign_object_test.go),
covering every guard, both downgrades, the D8 read, the three delete guards and the
`deleteIfOwned` Conflict branch. **Two new integration tests** in
[`test/integration/foreign_object_test.go`](test/integration/foreign_object_test.go):
`TestForeignConfigMap_Integration` (the failing direction end to end —
`ReconcileBlocked/ForeignObject`, the object untouched, and recovery once the collision is
removed) and `TestForeignMetricsService_Integration` (the *non*-failing direction, which only
this tier can pin: the phase is written by `updateStatus` from the data plane and the
condition by the joined pass error, so only a real manager shows what the two authorities
agree on after a downgraded refusal).

**Twenty-one existing tests** went red the moment the guards landed and gained the controller
reference their fixtures were implicitly relying on nobody checking. Two of them asserted the opposite of the new rule —
"a missing owner reference must be restored", on the Certificate and the ServiceMonitor — and
were rewritten: an object without a controller reference is foreign, and restoring one is
exactly what D1 forbids.

**Not run:** any e2e suite, any cluster reproduction. **Not verifiable in this repo at all:**
that the garbage collector actually deletes an object carrying a `Controller: true` /
`BlockOwnerDeletion: true` reference when the CR goes. envtest starts no
kube-controller-manager, so no tier here runs a GC. The tests prove the operator's half — the
reference is never written onto an object it did not verify — and the deletion itself stays
an assertion from the upstream API contract, as the filing said.

### Two things deliberately left open

- **The guard protects only forward.** An object an earlier release already stamped passes
  `IsControlledBy`, and no field distinguishes it from a genuine child. Documented in ADR 0020
  Consequences and Residuals and as a hardening item; not detected, because detection would
  have to guess.
- **The Secret door.** The data StatefulSet mounts `ValkeyTLSSecretName(v)` by name. With a
  user-provided Secret that is the documented feature; under cert-manager the name is derived,
  so a foreign `<cr>-tls` Secret is mounted unverified. Not a new door — `spec.tls.secretName`
  and `spec.auth.secretName` already let a CR name any Secret in its namespace — and not
  closed here either. Recorded in ADR 0020 Residuals.

### Current open list (sixteenth pass) — superseded by the seventeenth pass below

**NA63** (the pod door) is the only open item. Nothing from NA1-NA62 is open.

## Seventeenth pass (2026-08-22) — NA63 closed, the pod door was three doors

### The decisions, as made by Hans (2026-08-22)

Five questions, five answers, all taken before any code was written:

1. **Scope: all three doors**, not the two the filing names. The verification found that the
   filed door — the network commands — is the *most* expensive to use, not the least: every
   probe and every `REPLICAOF` goes through `valkeyPodAddress` / `PodAddressForComponent`
   (`internal/health/checker.go`; `podAddress` was removed by ADR 0029 on 2026-08-26),
   so reaching a foreign pod needs the cluster label set, a per-pod record under the headless
   Service and the CR password. The two doors nobody had filed need only the label set.
2. **Proof: two-hop through the StatefulSet.** A pod is the only managed object whose
   controller is not the CR. `metav1.IsControlledBy(pod, sts)` against a StatefulSet ADR 0020
   D1 already proved, so every link compares a UID. The one-hop variant (an ownerReference
   naming a StatefulSet, without looking it up) was offered and declined: it would hang the
   comparison on a name the CR author chooses, which is ADR 0006 D2's mistake one level down.
3. **The sidecar grant: filter the live half, leave the desired half.** `SidecarRolePodNames`
   is the union of the pods that exist and the pods `spec.replicas` asks for. The live half is
   filtered; the desired half keeps granting `<cr>-0..N-1` before those pods exist, because
   that is what stops a scale-up 403ing until the next pass — and a name under which no pod of
   ours can ever exist is a name our own StatefulSet is already blocked on. Documented as a
   residual rather than fixed.
4. **The rolling update comes in, and its pod deletes first.** Its entry points were gated on
   the StatefulSet since NA61, which proves the wrong object: a StatefulSet can be provably
   ours while the pod under `<cr>-N` is not, and a foreign pod differs from our persisted
   template by construction — so the very next step classifies it as outdated and deletes it.
   This is the only pod door that destroys something.
5. **In the rolling update the refusal fails the step**, rather than treating the pod as
   absent. Treat-as-absent had a ready branch (`exists=false, needsUpdate=true`, the one the
   NotFound case takes) and is the method ADR 0020 D8 states, but the resulting wait is honest
   about the wrong thing: "waiting for pod-N to be recreated", forever, when the truth is that
   a foreign object holds the name. Failing keeps the rolling-update state annotation in
   place, so ADR 0010's bounded waits stay armed.

Two things decided without asking, and stated as such: **one reporter** — `reconcileSidecarRole`
runs unconditionally and lists the data pods anyway, so it carries the `PodNotOwned` Event for
the family while every filtering path stays quiet, and the rolling update emits nothing of its
own because its refusal already reaches the CR as `ReconcileBlocked/ForeignObject` with the pod
name in it. And **the rule became D9 of ADR 0020**, not a new ADR: same decision family, only
with the two-hop proof a pod forces.

### What changed

- **The proof and its plumbing** in
  [`internal/controller/foreign_object.go`](internal/controller/foreign_object.go):
  `podIsOurs`, `ownedDataStatefulSet`, `podUnderNameIsOurs`, `filterOwnedPods`,
  `deleteOwnedPod` and the `PodNotOwned` Event reason.
- **The grant door** — `listDataPodNames` filters the live half and returns the refused names
  to `reconcileSidecarRole`, which reports them.
- **The API-write door** — `clearDrainStamps` filters before it patches.
- **The label-selection door** — `listMasterLabeledPods` filters, which is a correctness fix
  as much as a security one: the count is load-bearing (one labeled master is healthy, two are
  a split brain), so an unfiltered stray turned a healthy cluster into a resolution that
  demotes a real master.
- **The network door** — `checkAndRecoverNoMaster` counts an unprovable pod as unreachable,
  which is the branch it already fails closed on; the probe loop moved into `probeForAnyMaster`
  to keep the function under the complexity threshold.
- **Fail-closed reads** — `recreatedAfter` and `sentinelRolloutComplete` treat an unproven pod
  the same way they treat a missing one.
- **The rolling update** — the five pod reads refuse with `errForeignObject`, and all six pod
  deletes go through `deleteOwnedPod` with the ADR 0006 UID precondition and Conflict-is-not-a-
  failure.
- **Docs in the same change** — ADR 0020: Status amendment, a fifth Context failure spelling
  out all three doors, the new **D9** with its per-path fail-direction table and its two stated
  non-goals, three Consequences, four Alternatives, the old pod-door residual struck through in
  place with the reason it is kept. ADR 0006: Status amendment for the pod deletes.
  SECURITY_ARCHITECTURE.md: section 3 rewritten, NA63 ticked with the corrected severity
  ordering. CLAUDE.md: the pod exception added to the standing provenance rule.

### What ran, from the current tree (all green)

`make test-unit` (13/13 packages), `make test-integration` (14 tests, the same 14 as before the
change — verified against the stashed tree, so nothing dropped out), `make lint` (0 issues),
`make vet`, `make cyclo` (all < 15, after extracting `probeForAnyMaster`), `make gosec`
(0 issues), `make generate-all` with no generated diffs.

**Three mutation checks**, each reverted after the run: making `podIsOurs` always return true
fails 7 tests; dropping the UID precondition from `deleteOwnedPod` fails 1; disabling the
ownership check inside `ownedDataStatefulSet` fails 1. The last one initially came back as a
no-op — the guard had no test that exercised the *second* hop on its own — and
`TestReconcileSidecarRole_GrantsNothingLiveWhenTheStatefulSetIsForeign` was written to close
that, staging a pod that is a valid child of a foreign StatefulSet under the generated name.

**Ten new unit tests** in
[`internal/controller/foreign_object_test.go`](internal/controller/foreign_object_test.go), one
per door plus the two `deleteOwnedPod` branches and the second-hop case.

**No new integration test, deliberately.** envtest starts no kubelet, so no pod exists there at
all; the pod guards cannot be exercised in that tier. The two envtest cases added for NA62 still
cover the machinery around them.

**The test suite had to become honest about parentage.** Roughly a hundred pod fixtures were
built from literals with no ownerReference, and the fake client assigns no UID, so a StatefulSet
the reconciler created under test had an empty one. Fixed centrally in two halves: fixtures point
at a deterministic `<sts-name>-sts-uid`, and a Create interceptor stamps the same string on any
StatefulSet the fake client creates. Without the second half the suite would have measured the
fixture rather than the guard — exactly the trap `newTestValkey` already documents for the CR's
own UID. One test was asserting the no-master recovery with no pods staged at all; it now stages
them, because a pod that exists only in the mock checker's answers is no longer an answer.

**Not run:** any e2e suite, any cluster reproduction.

### Two things deliberately left open

- **The desired half of the sidecar grant.** `<cr>-0..N-1` is granted before those pods exist,
  which is what keeps a scale-up from 403ing. Filtering it would break the behaviour ADR 0020 D3
  describes as load-bearing.
- **Upstream adoption bounds the whole guard.** The statefulset-controller adopts an orphan pod
  matching its selector and stamps its own controller reference, so a pod built to carry this
  cluster's label set and left without a controller becomes genuinely ours by Kubernetes' own
  rules. D9 closes collisions and strays, not a deliberate mimic. Read from the API contract,
  reproduced nowhere here.

### Current open list (seventeenth pass)

Nothing is open. NA1-NA63 are all closed.
