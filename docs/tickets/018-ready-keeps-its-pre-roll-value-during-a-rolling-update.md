---
id: T18
title: "`Ready` keeps the value of the last status computation on every pass that ends on a rolling-update exit — decided in ADR 0001 D4, re-decision request"
state: analysed
severity: low         # no data-plane effect; the one production reader passes on a healthy roll
security: none
urgency: now          # rule 1: tracked files state what the code contradicts (Required changes 1-3, and the registry string); icebox (rule 5) once those land
effort: S             # reading 1 plus the XS corrections; reading 2 is L plus e2e
blocked-by: human
filed-from: T6d analysis (option P6), 2026-08-25
opened: 2026-08-25
decided:
done:
---

# T18 - `Ready` keeps the value of the last status computation on every pass that ends on a rolling-update exit — decided in ADR 0001 D4, re-decision request

## Current state

`Ready` is computed only in `updateStatus`
([`valkey_controller.go:2181-2223`](../../internal/controller/valkey_controller.go#L2181-L2223)), which
hands over to `updateStandaloneStatus`
([`:2226-2287`](../../internal/controller/valkey_controller.go#L2226-L2287)) or `updateHAStatus`
([`:2427-2529`](../../internal/controller/valkey_controller.go#L2427-L2529)); `persistStatus`
([`:2548-2571`](../../internal/controller/valkey_controller.go#L2548-L2571)) adds `operatorVersion` and
`observerReady` and writes only on a difference. `reconcileWorkload` reaches `updateStatus` only at
[`:369`](../../internal/controller/valkey_controller.go#L369). These exits return before it:

- data roll `Error` ([`:336-339`](../../internal/controller/valkey_controller.go#L336-L339)) and
  `NeedsRequeue` ([`:340-342`](../../internal/controller/valkey_controller.go#L340-L342));
- terminal returns of `handlePostRollingUpdateChecks`, propagated at
  [`:363-366`](../../internal/controller/valkey_controller.go#L363-L366): Sentinel roll error
  ([`:473-481`](../../internal/controller/valkey_controller.go#L473-L481)), Sentinel roll requeue
  ([`:482-484`](../../internal/controller/valkey_controller.go#L482-L484)), no-master recovery
  ([`:422-428`](../../internal/controller/valkey_controller.go#L422-L428)), and, outside any roll, the
  steady-state split-brain check ([`:440-443`](../../internal/controller/valkey_controller.go#L440-L443)).

An ordinary roll pass of either tier writes `Rolling Update i/n` and ends on one of these exits, so
`Ready` (normally `True/HAClusterReady`) keeps the value of the last pass that reached `updateStatus`.
This is decided behaviour: ADR 0001 D4
([`0001:100-126`](../adr/0001-continue-reconciling-past-a-rejected-write.md)) says the rolling-update
exits own their returns and leaves open which reading of `Ready` is intended.

Two kinds of pass do reach `updateStatus` mid-roll and recompute everything:

- a wait past its bound: `terminationWait`, `recreationWait`, `availabilityWait` return
  `DeferredRequeueAfter` ([`rolling_update.go:2075`](../../internal/controller/rolling_update.go#L2075),
  [`:2186`](../../internal/controller/rolling_update.go#L2186),
  [`:2268`](../../internal/controller/rolling_update.go#L2268)), on which `reconcileWorkload` does not
  return ([`valkey_controller.go:355`](../../internal/controller/valkey_controller.go#L355), Sentinel
  tier [`:485`](../../internal/controller/valkey_controller.go#L485)) (ADR 0026 D5, D11; ADR 0010 D16, D17);
- the pass in which the data roll pauses (`pauseRollingUpdate` returns an empty result,
  [`rolling_update.go:2646`](../../internal/controller/rolling_update.go#L2646)), unless a post-update
  check ends it.

**What freezes on a pass that ends on a roll exit:** `Ready`, `readyReplicas`, `masterPod`,
`observerReady`, `status.operatorVersion` (only writer `persistStatus`,
[`:2555`](../../internal/controller/valkey_controller.go#L2555)), the `SentinelPeersStale` level
(`recordSentinelPeerDrift` runs only in the all-Ready arm of `updateHAStatus`,
[`:2458-2462`](../../internal/controller/valkey_controller.go#L2458-L2462)), and the `Ready` condition's
`ObservedGeneration`, which feeds `vko_valkey_status_observed_generation`
([`internal/metrics/collector.go:201-223`](../../internal/metrics/collector.go#L201-L223)). ADR 0001's
clarification (`0001:109-121`) names only the first four.

**Readers of `Ready`.** Nothing in this repository gates on it and the operator never reads it. Flux
does: kstatus `status.Compute` (fluxcd/cli-utils v1.3.0) finds no top-level `status.observedGeneration`
and no `Reconciling`/`Stalled` condition on this CRD, so it falls back to `checkReadyCondition`:
`Ready=True` is `Current`, anything else `InProgress`; a per-condition `observedGeneration` is not read.
kustomize-controller uses that generic reader and health-checks every applied object when `spec.wait`
is set, up to `spec.timeout`, then marks the Kustomization `Ready=False/HealthCheckFailed`; a
Kustomization that `dependsOn` it is `DependencyNotReady` meanwhile. In production,
`oauth2-proxy-databases` (`k8s-flux-base`, `apps/iam/oauth2-proxy/ks.yml`: `wait: true`, `interval: 1m`,
`timeout: 5m`) applies Valkey `oauth2-valkey` (3 replicas, 3 Sentinels, TLS), and `oauth2-proxy` depends
on it. The other Valkey Kustomizations set neither `wait` nor `healthChecks`.

**Impact** (no data-plane effect; no failover, promotion or delete decision reads `Ready`):

- `oauth2-proxy-databases` reads `Current` on a healthy roll. Once a stall or pause pass writes
  `Ready=False`, it reads `InProgress` and fails after 5 min; if the roll resumes, the `False` stays
  frozen until the completing pass. For a Valkey spec change Flux applies, the check reads the stale
  `Ready=True`, so `wait: true` gives no rollout signal.
- Two default-off alerts can fire on a long healthy roll (traced by reading):
  `ValkeySpecNotObserved`
  ([`prometheusrule.yaml:32-50`](../../deploy/helm/valkey-operator/templates/prometheusrule.yaml#L32-L50),
  `for: 30m`) on a multi-replica non-TLS CR whose spec edit starts a roll in the same pass (TLS CRs
  restamp a condition through `reportTLSMaterialStale`); `ValkeyOperatorVersionStale`
  ([`:104-122`](../../deploy/helm/valkey-operator/templates/prometheusrule.yaml#L104-L122), `for: 1h`) on an
  upgrade roll longer than an hour without a pause or stall pass.
  [`docs/operations/monitoring.md:57-61`](../operations/monitoring.md) says a generation gap means a spec
  change "never converged"; [`README.md:497`](../../README.md) says `operatorVersion` is the version that
  last reconciled the resource.
- `ValkeyReplicasMissing` and the `kubectl get` column `Ready` read the frozen `readyReplicas` in the
  harmless direction.

**Tracked statements the code contradicts:**

- ADR 0001 D4's rule sentence
  ([`0001:104-107`](../adr/0001-continue-reconciling-past-a-rejected-write.md)) "a pass with a rolling
  update in flight — blocked or not — returns before `updateStatus`", and the Status line `0001:19`
  "D4 itself is unchanged".
- ADR 0002 Residual risks
  ([`0002:540-542`](../adr/0002-surface-a-blocked-reconcile-on-the-cr.md)) "because a pass with a roll in
  flight returns before `updateStatus`", and `:543-544` "come apart for the duration of a roll".
- The `Ready` row of the condition registry: `clearSite`
  ([`condition_registry.go:99`](../../internal/controller/condition_registry.go#L99)) says the status arms
  recompute it every pass; the `declaredGap`
  ([`:102`](../../internal/controller/condition_registry.go#L102)) says "for the whole roll".
- ADR 0002 D9 ([`0002:218-220`](../adr/0002-surface-a-blocked-reconcile-on-the-cr.md)) and the
  `setStatusCondition` doc comment
  ([`valkey_controller.go:2640-2643`](../../internal/controller/valkey_controller.go#L2640-L2643)) say
  kstatus reads a condition without `ObservedGeneration` as generation 0. kstatus reads only the
  top-level `status.observedGeneration`. D9's rule itself stands, because
  `vko_valkey_status_observed_generation` depends on the stamp.

**Registry gap.** The `Ready` row carries a `declaredGap` citing T18
([`condition_registry.go:102`](../../internal/controller/condition_registry.go#L102), package comment
[`:16-18`](../../internal/controller/condition_registry.go#L16-L18)).
`TestConditionRegistryGapsAreTraceable`
([`condition_registry_test.go:200-211`](../../internal/controller/condition_registry_test.go#L200-L211))
demands `T\d+` in every gap, while ADR 0034 D7 forbids new ticket citations outside `docs/tickets/`. So
the `:102` string cannot be rewritten alone: it lands with this decision (either reading deletes the gap)
or with T40 decision 2 option A (the test regexp becomes `ADR \d{4}`). T18 is cited outside
`docs/tickets/` at `condition_registry.go:17` and `:102`, `CLAUDE.md:568`, and ADR 0027 `:201`, `:252`,
`:332`. With the gap removed, `TestConditionRegistryLevelsHaveOneEvaluator`
(`condition_registry_test.go:149`) checks the row and passes on `evaluators: 1`
(`condition_registry.go:98`).

## Required changes

### Independent of the open question

1. ADR 0001: correct the D4 rule sentence (`0001:104-107`) in place: a pass that ends on a
   rolling-update exit returns before `updateStatus`; a pass past a wait bound (ADR 0026 D5, D11;
   ADR 0010 D16, D17) and the pause pass continue to it. Correct the Status line `0001:19`. No rule
   changes. Same change: ADR 0002 `:540-544`.
2. `condition_registry.go:99`: `clearSite` says the arms recompute `Ready` on every pass that reaches
   `updateStatus`. `make test-unit`, `make lint`.
3. ADR 0002 D9 (`0002:218-220`) and the `setStatusCondition` doc comment (`valkey_controller.go:2640-2643`):
   kstatus reads only a top-level `status.observedGeneration`; the stamp stays for
   `vko_valkey_status_observed_generation`. Check: `git grep -n "generation 0" -- ':!docs/tickets'`
   finds no current statement.

### Depends on the answer

**Reading 1 (option A):**

- ADR 0001: the "deliberately left open" sentence (`0001:123-126`) replaced by the decided reading; the
  frozen-value list (`:110-111`) completed with `operatorVersion`, `SentinelPeersStale` and the `Ready`
  `ObservedGeneration`; the kstatus consequence named (a `wait: true` Kustomization reads a healthy roll
  as `Current`).
- ADR 0002 Residual risks (`0002:535-545`): the open question marked decided.
- ADR 0027 `:201`, `:252`, `:332`: no declared gap left.
- `condition_registry.go`: the `Ready` `declaredGap` (`:102`) and the package-comment sentence
  (`:16-18`) removed.
- [`docs/operations/status.md:17`](../operations/status.md#ready): what a Flux Kustomization with
  `wait: true` or `healthChecks` sees during a roll.
- `docs/operations/monitoring.md:57-61` and ADR 0021 Residual risks (`0021:200-218`): a spec-triggered
  roll of a non-TLS CR can keep the generation gap open; a long upgrade roll keeps `operatorVersion`
  behind.
- `README.md:497`: `operatorVersion` is written with the status, not on every reconcile.
- `CLAUDE.md:568` (needs Hans; coordinate with T40 work item 2).
- Proof: `make test-unit` and `make lint` green; `git grep -n -w T18 -- ':!docs/tickets'` is empty.

**Reading 2 (option B):**

- Every roll exit of `reconcileWorkload` and of the Sentinel roll reaches `persistStatus`, with a
  per-pass context marker (the `passIsBlocked` pattern) that keeps the roll's phase and message.
- `updateStandaloneStatus` and `updateHAStatus` gain a roll-in-flight branch writing `Ready=False` with a
  rolling-update reason, placed before the health check in `updateHAStatus` (spares roll passes the
  `CheckCluster` probe, [`:2457-2460`](../../internal/controller/valkey_controller.go#L2457-L2460)).
  Without the branch, `persistStatus` publishes a per-pass verdict that flips between `False` and `True`
  across replacements.
- ADR 0001 D4 second half superseded in place; ADR 0002, the ADR 0027 row, the registry gap and the docs
  amended. The release notes name the change (ADR 0005 D10: upgrade neutrality covers `status`).
- Proof: an e2e roll on both topologies observes `Ready=False` with the reason, `True` after completion,
  and the phase `Rolling Update i/n` throughout.

## Open questions

### Q1: Does `Ready` mean "the last computed data-plane verdict" or "the cluster is serving now, and not mid-roll"?

Today `Ready` keeps the value of the last pass that reached `updateStatus`, so a healthy roll reads
`True` and Flux's `wait: true` on `oauth2-proxy-databases` reads `Current`. The stall surfacing of
ADR 0026 D5, D11 and ADR 0010 D16, D17 already recomputes `Ready` on a stuck roll either way.

- **A - reading 1, decide the current behaviour (recommended).** Text and one registry row, no
  behaviour change, no e2e; cost S. `wait: true` stays vacuous for a Valkey spec change, and the frozen
  fields and the two alert effects stay.
- **B - reading 2, `Ready=False` with a rolling-update reason while either tier rolls.** Cost L plus
  e2e. The frozen fields become current and `wait: true` gains a rollout signal. `oauth2-proxy-databases`
  turns `InProgress` for every roll of `oauth2-valkey`, including certificate-rotation and
  operator-upgrade rolls it did not cause, and holds `oauth2-proxy`'s applies (`DependencyNotReady`) for
  the whole roll; a roll longer than 5 min fails it with `HealthCheckFailed`.

A changes nothing for the one production reader and breaks no standing constraint. The rollout signal B
buys gains that reader nothing, because the failover-aware roll keeps `oauth2-valkey` serving and
oauth2-proxy does not need the roll to finish; if a GitOps consumer ever needs to gate on convergence, a
top-level `status.observedGeneration` is the carrier that does not redefine `Ready`.

**Answer:** _open_

## Not verified

- The mid-roll recompute cases and the alert effects are traced by reading, not measured.
- The live wds18 cluster and its kustomize-controller version were not read; the Flux facts come from
  the local clones and upstream source.
- How long a roll of `oauth2-valkey` takes; decides whether B fails `oauth2-proxy-databases` (5 min). A
  timed roll of a 3+3 Sentinel TLS cluster on Kind would settle it.
- Whether `ValkeySpecNotObserved` fires on a healthy roll depends on whether the informer has delivered
  the template write in the pass that applies the CR edit; an e2e reading
  `vko_valkey_status_observed_generation` during the replica phase of a non-TLS multi-replica roll would
  settle it.
- Whether a tool other than kstatus reads a per-condition `observedGeneration`; Argo CD was not checked.

## Related

- T40 - decision 2 and work item 2 touch the same registry line, ADR 0027 D4 and `CLAUDE.md:568`.
- T23 - its pause change makes the pause pass a holding pass; only ADR 0001 `:118`'s reason ("because
  `pauseRollingUpdate` returns an empty result") becomes false, and T23 rewrites it.
- T59 - `readyReplicas` compared against itself; independent of this reading.
- T69 - changes `AllSynced` in `updateHAStatus`; composes with B's roll-in-flight branch.
