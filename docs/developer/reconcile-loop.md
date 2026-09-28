# Reconcile loop

What starts a reconcile pass, the order a pass does its work in, and when it writes the
`Valkey` status. The objects the steps produce are [architecture.md](architecture.md); the
rolling updates the workload half calls into have no page and live in their ADRs — see
[What this page does not cover](#what-this-page-does-not-cover).

All of it is in
[`internal/controller/valkey_controller.go`](../../internal/controller/valkey_controller.go)
unless a line names another file. Read against the tree on 2026-09-27.

## What starts a pass

`SetupWithManager` wires three kinds of trigger:

| Trigger | Wiring |
|---|---|
| The `Valkey` itself | `For(&vkov1.Valkey{})` with `GenerationChangedPredicate`: a spec change starts a pass, a status or metadata-only write does not |
| An object the CR owns | `Owns(...)` for StatefulSets, Deployments, ConfigMaps, Services, ServiceAccounts, Roles, RoleBindings, NetworkPolicies and PodDisruptionBudgets. A Pod is not watched, and neither are the `unstructured` Certificates and ServiceMonitors |
| A Secret | `Watches(&corev1.Secret{}, …findValkeyForSecret)`: a change to the auth Secret a CR names, or to either TLS Secret its pods mount (`secretConcernsValkey`), enqueues that CR |

Everything else is a pass asking for its own successor:

- **A returned error** hands the retry to the work-queue rate limiter
  ([`ratelimiter.go`](../../internal/controller/ratelimiter.go)): per item, exponential from
  5 ms and capped at 30 s, combined with an overall token bucket of 10 per second, burst 100
  ([ADR 0001](../adr/0001-continue-reconciling-past-a-rejected-write.md) D5, D6).
- **A `RequeueAfter`** from a rolling-update wait, the nudge, an unhealthy phase or a recheck —
  the ladder is under [The workload pass](#the-workload-pass).
- **A recheck the pass asked for without failing** (`requestRecheck`; the shortest request
  wins). There are three sources: a refusal that did not fail its step
  (`foreignObjectRecheckInterval`, 30 s: a foreign object,
  [ADR 0020](../adr/0020-write-only-what-the-operator-owns.md) D6, or a TLS pod template without
  a material record,
  [ADR 0030](../adr/0030-rotating-certificates-rotate-the-instances-that-cannot-reload-them.md)
  D12); Sentinel peer-table drift (`sentinelPeerDriftRecheckInterval`, 5 min, only while drift
  is reported, [ADR 0022](../adr/0022-sentinel-identity-is-pinned-to-the-pod.md) D7); and the
  pass after a completed data roll whose persisted template still carries the ownership repair
  (`rollingUpdateRequeueDelay`, 10 s, from `finishDataRoll`,
  [ADR 0032](../adr/0032-generated-pods-run-rootless.md) D4). The request is carried on a
  per-pass `passState` in the context. `applyRecheck` folds it into the result only on the
  error-free path, and it never lengthens a shorter requeue.

The controller runs `MaxConcurrentReconciles` workers — `--max-concurrent-reconciles`, default
`DefaultMaxConcurrentReconciles` = 4 — and the work queue never runs two passes for the same
CR at once ([ADR 0019](../adr/0019-reconcile-concurrency-and-the-cost-of-a-stuck-pass.md)).
That is why per-pass state rides on the context and never on the reconciler.

## One pass

`Reconcile`, in order:

1. **Read the CR.** Not found: forget its nudge observations and stop. Being deleted
   (`DeletionTimestamp` set): stop — the garbage collector removes the owned objects.
2. **An empty phase is written as `Provisioning`.** A failure is logged and the pass goes on;
   the phase is recomputed later in the same pass
   ([ADR 0002](../adr/0002-surface-a-blocked-reconcile-on-the-cr.md) D7).
3. **Attach a fresh `passState`** to the context.
4. **The resource pass**, `reconcileResources`: every applicable step of
   `resourceReconcileSteps` runs, and the failures are joined into one error
   ([The resource steps](#the-resource-steps)).
5. **`setReconcileBlockedCondition`** mirrors that error into the `ReconcileBlocked`
   condition — or clears it — in
   [`reconcile_blocked.go`](../../internal/controller/reconcile_blocked.go). If the resource
   pass failed, `withBlockedPass` marks the context so that every intermediate phase write of
   the pass is dropped ([ADR 0002](../adr/0002-surface-a-blocked-reconcile-on-the-cr.md) D3,
   D4).
6. **The workload pass**, `reconcileWorkload`, runs either way
   ([ADR 0001](../adr/0001-continue-reconciling-past-a-rejected-write.md) D3).
7. **A blocked pass ends with the one phase write that is allowed**: `writePhase` sets `Error`
   with the compacted resource error, and the pass returns the resource error joined with any
   workload error, so the rate limiter backs off
   ([ADR 0002](../adr/0002-surface-a-blocked-reconcile-on-the-cr.md) D6).
8. **Otherwise** a workload error is returned as it is, and a clean pass returns the workload
   result with the recheck folded in.

## The resource steps

`resourceReconcileSteps` returns the steps in the order they run. A step with a predicate runs
only when it applies; a failing step fails only itself, because steps reference the objects of
earlier steps by name only
([ADR 0001](../adr/0001-continue-reconciling-past-a-rejected-write.md) D1, D2).

| # | Step | Applies when | Writes |
|---|---|---|---|
| 1 | ConfigMap | always | the Valkey ConfigMap |
| 2 | replica ConfigMap | Sentinel, or more than one replica without it | the replica ConfigMap |
| 3 | TLS Certificates | cert-manager issues the TLS material | the Valkey Certificate, the Sentinel one unless `unifiedCertificate`, and the legacy Sentinel cleanup |
| 4 | Services | always | headless and `-rw`; `-all` and `-r` with more than one replica; deletes legacy Services |
| 5 | sidecar RBAC | always | ServiceAccount, then Role, then RoleBinding — the binding only when both others are proven ours ([ADR 0020](../adr/0020-write-only-what-the-operator-owns.md) D3) |
| 6 | StatefulSet | always | the data StatefulSet (`reconcileStatefulSet`) |
| 7 | Sentinel resources | Sentinel enabled | Sentinel ConfigMap, headless Service, StatefulSet |
| 8 | PodDisruptionBudgets | always (opt-in is decided inside) | both budgets, or their cleanup ([`pdb.go`](../../internal/controller/pdb.go)) |
| 9 | NetworkPolicies | `spec.networkPolicy.enabled` | Valkey, Sentinel and observer policies |
| 10 | monitoring | always | the observer ServiceAccount and Deployment or their cleanup; the metrics Service and ServiceMonitor or their cleanup |
| 11 | TLS material | always, deliberately ungated | nothing — it reports `TLSMaterialStale` by measuring the pods against the fingerprints steps 6 and 7 just stamped ([`tls_material.go`](../../internal/controller/tls_material.go)) |
| 12 | pod name collision | always, deliberately ungated | nothing — it reports `ReconcileBlocked=True/ForeignObject` when a pod at a generated ordinal name is not controlled by its StatefulSet, so the roll's own hold on that pod ([ADR 0020](../adr/0020-write-only-what-the-operator-owns.md) D9) reaches the critical alert ([`reportPodNameCollision`](../../internal/controller/foreign_object.go), [ADR 0002](../adr/0002-surface-a-blocked-reconcile-on-the-cr.md) D13) |

**Step 5 runs before step 6 on purpose.** The sidecar Role grants `patch` on named pods, so on
a scale-up it has to name the new pod before the StatefulSet write creates it;
`TestResourceReconcileSteps_RBACBeforeStatefulSet`
([`reconcile_steps_test.go`](../../internal/controller/reconcile_steps_test.go)) fails if the
order is swapped.

## The workload pass

`reconcileWorkload` handles what depends on the running pods rather than on the objects:

1. **Nudge first.** `nudgeShortStatefulSets` ([`nudge.go`](../../internal/controller/nudge.go))
   bumps a StatefulSet that has been short of pods past its 10 s grace period, and records
   whether one is short. It runs ahead of both rolling-update checks because both return
   early while waiting for a recreated pod — exactly when a blocked recreation needs the nudge
   ([ADR 0003](../adr/0003-nudge-a-short-of-pods-statefulset.md) D7).
2. **The data-tier rolling update**, `checkAndHandleRollingUpdate`
   ([`rolling_update.go`](../../internal/controller/rolling_update.go)). An error writes phase
   `Error` and returns it; `NeedsRequeue` ends the pass with that delay; a
   `DeferredRequeueAfter` — a wait past its bound — is kept for the end of the pass and marks
   the data tier as holding.
3. **The post-update checks**, `handlePostRollingUpdateChecks`:
   - the Sentinel-tier roll, `runSentinelRollingUpdate`. Without Sentinel it only clears a
     standing `SentinelUpdatePending` and the Sentinel report of `PodAvailabilityStalled`; it
     is skipped for the pass while the data tier is holding
     ([ADR 0026](../adr/0026-a-pod-being-deleted-is-not-available.md) D11,
     [ADR 0024](../adr/0024-the-sentinel-tier-reports-its-own-completion.md));
   - on a multi-replica cluster without Sentinel, the no-master recovery
     `checkAndRecoverNoMaster`;
   - the steady-state split-brain check `checkSteadyStateSplitBrain`
     ([`steady_state_master.go`](../../internal/controller/steady_state_master.go),
     [ADR 0011](../adr/0011-evidence-based-steady-state-split-brain-resolution.md) D1).

   Each may end the pass; a recheck one of them wants without ending it is carried to the end.
4. **The status**, `updateStatus` — see [The status write](#the-status-write).
5. **The requeue ladder**, first match wins:

   | Condition | Requeue |
   |---|---|
   | phase is `Error` or `Syncing` | 10 s |
   | a StatefulSet is short of pods | `nudgeRequeueInterval`, 5 s |
   | a post-update check asked for a recheck | its interval |
   | otherwise | the deferred rolling-update recheck, zero when there is none |

## The status write

`updateStatus` reads the data StatefulSet. Missing or not controlled by this CR, it writes
`Provisioning` ("Waiting for StatefulSet creation") and stops. Otherwise it re-reads the CR,
takes `readyReplicas` from the StatefulSet and hands over to `updateHAStatus` (Sentinel) or
`updateStandaloneStatus` (everything else), which compute phase, message, master pod and
conditions.

Both capture `prevStatus` before they change anything themselves, but after `updateStatus` has
already set `readyReplicas`, and end in `persistStatus`, which:

- on a blocked pass, restores the previous phase and message, so the blocked pass's single
  `Error` write stays the phase authority;
- sets `operatorVersion` and `observerReady` **after** the capture, so that a change in either
  alone is still a reason to write
  ([ADR 0002](../adr/0002-surface-a-blocked-reconcile-on-the-cr.md) D5);
- writes the status subresource only when `statusUnchanged` finds a difference in phase,
  message, ready replicas, master pod, operator version, observer readiness or conditions.
  `readyReplicas` can never be the only difference: it is assigned before the capture and
  compared against itself. It reaches the CR only because, in every branch, the phase, the
  message or the `Ready` condition changes with the count. On a blocked pass, where phase and
  message are put back, the `Ready` condition alone carries it. ADR 0002 D5 (amended) and its
  residual risk accept this masking as fragile
  ([ADR 0002](../adr/0002-surface-a-blocked-reconcile-on-the-cr.md) D5).

Conditions reach the CR in two ways:

| Path | Behaviour | Used by |
|---|---|---|
| `setStatusCondition` / `writeStatusCondition` | Re-reads the CR, sets the condition with `ObservedGeneration`, updates the status at once, retrying on conflict. `setStatusCondition` logs a failure and goes on; `writeStatusCondition` returns it and reports whether anything changed | most conditions, from wherever they are evaluated |
| in place on `v.Status.Conditions` | Rides the pass's own write, because the caller runs between the `prevStatus` capture and `persistStatus` | e.g. `reportRWServiceEndpoints` ([`rw_service_report.go`](../../internal/controller/rw_service_report.go)) |

Phase writes outside `persistStatus` go through `updatePhase`, which does nothing on a blocked
pass; only `writePhase` bypasses that.

## What this page does not cover

- **The inside of the rolling updates** — the state annotations, the failover, topology
  restoration and every bounded wait — is
  [`rolling_update.go`](../../internal/controller/rolling_update.go) and its ADRs:
  [ADR 0007](../adr/0007-failover-aware-rolling-update.md),
  [ADR 0010](../adr/0010-every-rolling-update-wait-is-bounded.md),
  [ADR 0024](../adr/0024-the-sentinel-tier-reports-its-own-completion.md),
  [ADR 0026](../adr/0026-a-pod-being-deleted-is-not-available.md).
- **Which condition is a level, an edge or history**, and who may clear it, is the registry
  in [`condition_registry.go`](../../internal/controller/condition_registry.go) and
  [ADR 0027](../adr/0027-conditions-are-levels-edges-or-history.md).
- **What `reconcileStatefulSet` checks before it writes** — ownership, the volume-claim guard,
  the TLS material record, the ownership repair, the seccomp allow-list, `hostUsers` — is
  spread over [`foreign_object.go`](../../internal/controller/foreign_object.go),
  [`volumeclaim_conflict.go`](../../internal/controller/volumeclaim_conflict.go),
  [`tls_material.go`](../../internal/controller/tls_material.go),
  [`pod_security_migration.go`](../../internal/controller/pod_security_migration.go) and
  [`pod_hardening.go`](../../internal/controller/pod_hardening.go); the order of those checks is
  recorded in [ADR 0033](../adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md)
  D9. This page does not restate it.
