---
id: T18
title: "`Ready` keeps the value of the last status computation on every pass that ends on a rolling-update exit — decided in ADR 0001 D4, re-decision request"   # was "`Ready` keeps its pre-roll value for the whole rolling update — …" until 2026-09-27: false for the two kinds of pass that recompute it mid-roll
state: analysed       # "open"; deferred 2026-08-26 as a re-decision
severity: low
security: none
urgency: now          # rule 1, re-derived 2026-09-27 at 84a39c2: false by code or source reading at condition_registry.go:99 and :102, ADR 0001:104-107, ADR 0002:540-542, ADR 0002:218-220 and valkey_controller.go:2640-2643 (item 1, the 'whole roll' sentences, is committed in bcc63c9); :102 can land only with this ticket's decision or with 040 decision 2 (ADR 0034 D7), so `now` holds until one of them lands and items 3-5 are done; then icebox (rule 5)
effort: S             # reading 1 (recommended) plus the XS items 3-5; reading 2 is L plus e2e. Was S–L until 2026-09-27
blocked-by: human
filed-from: T6d analysis (option P6), 2026-08-25
opened: 2026-08-25
decided:
done:
---

# T18 - `Ready` keeps the value of the last status computation on every pass that ends on a rolling-update exit — decided in ADR 0001 D4, re-decision request

**Severity: low, and it is not an uncovered defect. Status: open, found 2026-08-25
while analysing T6d (option P6). Corrected before filing: the first draft called this
an uncovered gap; ADR 0001 D4 decides it explicitly and is quoted below.**

~~Two~~ **Five** `reconcileWorkload` exits return before `updateStatus`:

- ~~[`valkey_controller.go:320-322`](../../internal/controller/valkey_controller.go#L320-L322)~~ *(corrected 2026-09-27: [`:336-339`](../../internal/controller/valkey_controller.go#L336-L339))* — `rollingResult.Error`
- ~~[`valkey_controller.go:324-326`](../../internal/controller/valkey_controller.go#L324-L326)~~ *(corrected 2026-09-27: [`:340-342`](../../internal/controller/valkey_controller.go#L340-L342))* — `rollingResult.NeedsRequeue`

> **Corrected 2026-08-26 — this item undercounts, and the correction makes reading 2
> bigger, not smaller.** Three further exits skip `updateStatus`: the terminal
> (`done == true`) returns of `handlePostRollingUpdateChecks`, propagated at
> ~~[`:343-345`](../../internal/controller/valkey_controller.go#L343-L345)~~ — the Sentinel roll
> error (~~[`:396`](../../internal/controller/valkey_controller.go#L396)~~), the Sentinel roll
> requeue (~~[`:399`](../../internal/controller/valkey_controller.go#L399)~~) and the no-master
> recovery (~~[`:417`](../../internal/controller/valkey_controller.go#L417)~~,
> ~~[`:419`](../../internal/controller/valkey_controller.go#L419)~~).
> *(corrected 2026-09-27, at `4a7543e`: propagated at [`:363-366`](../../internal/controller/valkey_controller.go#L363-L366); the Sentinel
> roll error [`:473-481`](../../internal/controller/valkey_controller.go#L473-L481), the Sentinel roll requeue [`:482-484`](../../internal/controller/valkey_controller.go#L482-L484),
> the no-master recovery [`:422-428`](../../internal/controller/valkey_controller.go#L422-L428). A sixth terminal return, outside any roll,
> is the steady-state split-brain check's at [`:440-443`](../../internal/controller/valkey_controller.go#L440-L443).)*
>
> The Decision below says those exits "were already changed for exactly this reason". That
> is true only of the **non-terminal** (`done == false`) result, which is now carried to the
> end of the pass. The terminal ones still return early. **Consequence: a full Sentinel-tier
> roll also freezes `Ready`** — so reading 2 is a larger change than this item already warns,
> and `updateStatus` is only reached at
> ~~[`:349`](../../internal/controller/valkey_controller.go#L349)~~ *(corrected 2026-09-27: [`:369`](../../internal/controller/valkey_controller.go#L369))*.

`NeedsRequeue` is set on essentially every pass of an active roll, so `updateStatus`
never runs *(corrected 2026-09-27: runs on two kinds of pass, see "Fact, re-verified" below)* and the `Ready` condition keeps whatever it held before the roll started —
normally `True / HAClusterReady` — while `updatePhase` writes `Rolling Update i/n`. So a
cluster reports Ready while its pods are being deleted one by one. ~~`masterPod` and
`observerReady` freeze for the same reason and the same duration.~~ *(corrected 2026-09-27 at
`84a39c2`: the list was incomplete. `readyReplicas`, `masterPod`, `observerReady`,
`status.operatorVersion`, the `SentinelPeersStale` level and the `Ready` condition's own
`ObservedGeneration` freeze for the same reason, on every pass that ends on a rolling-update
exit; see "Fact, re-verified" below.)*

**This is decided behaviour, not an oversight.** ADR 0001 D4
(~~`0001:84-91`~~ *(corrected 2026-09-27: [`0001:100-126`](../adr/0001-continue-reconciling-past-a-rejected-write.md), D4 at `:100-107` and its clarification at `:109-126`)*) closes with:
*"The rolling-update exits of `reconcileWorkload` still own their own returns: a pass
with a rolling update in flight — blocked or not — returns before `updateStatus` and
writes its phase itself."* Changing it means amending ADR 0001 D4 in place with the
superseded sentence marked, per the CLAUDE.md ADR rules — not writing a new decision.
*(Added 2026-09-27 at `84a39c2`: the quoted rule sentence, `0001:104-107`, is itself false as
stated since ADR 0026 D5 made a pass past a wait bound continue to `updateStatus`, and it is not
marked; work list item 3.)*

The *same argument* ADR 0026 D5 made for the `DeferredRequeueAfter` case — *"the pass
must not end on it, or everything below stays suspended … and the status write"* — is
what would be extended to the remaining early exit. The two later exits
(`handlePostRollingUpdateChecks`, the deferred recheck) were already changed for exactly
this reason and carry comments saying so, so the precedent for extending it exists.

**Two readings, and the decision is which one is intended:**

1. `Ready` means *the last steady state was healthy* — then this is correct and ~~the
   only work is a doc sentence (and it merges into T6d P1)~~ *(corrected 2026-09-27 at
   `84a39c2`: the T6d P1 doc comment exists; what reading 1 still needs is the edit list under
   Options, option A)*.
2. `Ready` means *the cluster is serving now* — then a roll must write
   `Ready=False` with a rolling-update reason, which is a behaviour change for any
   consumer treating `Ready` as a gate. ~~Verified: nothing in this repo, and nothing in
   the wds18 Flux setup, currently gates on it (see T6d).~~ *(corrected 2026-09-27 at
   `84a39c2`: false. Nothing in this repository gates on it, but Flux kstatus falls back to the
   `Ready` condition for this CRD, and the production Kustomization `oauth2-proxy-databases`
   (`wait: true`, `timeout: 5m`) health-checks the Valkey CR `oauth2-valkey` through it; see
   "Fact, re-verified" below.)*

**Proposed fix (not decided):** whichever reading is chosen, state it in the
`ConditionTypeReady` doc comment T6d P1 introduces, so the two are decided together
rather than twice. Note the scope if reading 2 wins: the exits that skip `Ready` skip
`masterPod`, `readyReplicas` and `observerReady` too, so "recompute `Ready` somewhere
every exit reaches" is really "reach `persistStatus` on every exit" — a larger change
than one write site, and one that has to respect ADR 0002 D8 (steady state costs no
status write), whose `ReadyReplicas`/`ObserverReady` guards are dead until T6a A1
lands. *(Added 2026-09-27 at `84a39c2`: reaching `persistStatus` is necessary and not
sufficient. It publishes the per-pass data-plane verdict, `False` while a replica is replaced
and `True` between two replacements; a rolling-update reason needs its own branch in both status
arms. Options, option B.)*

> **Half of that last clause is stale, corrected 2026-08-26.** T6a A1 **landed** in
> `75b3c92`. `ObserverReady` is now assigned inside `persistStatus` on the far side of the
> capture (~~[`:2386-2393`](../../internal/controller/valkey_controller.go#L2386-L2393)~~ *(corrected 2026-09-27:
> [`:2557-2564`](../../internal/controller/valkey_controller.go#L2557-L2564))*) and
> compared by `statusUnchanged` at
> ~~[`:2422`](../../internal/controller/valkey_controller.go#L2422)~~ *(corrected 2026-09-27: [`:2592`](../../internal/controller/valkey_controller.go#L2592))* — that guard is live.
> **`ReadyReplicas` still carries the defect**, by the fix commit's own admission and
> recorded as an accepted residual in ADR 0002 ~~`:403`~~ *(corrected 2026-09-27: `0002:491-529`)*.
> So this item is the place where that
> remainder would be closed, and reading 2 subsumes it. *(Superseded 2026-09-27: the
> `readyReplicas` remainder has its own ticket,
> [059](059-status-readyreplicas-is-compared-against-itself.md), whose option A closes it
> independently of the reading chosen here; reading 2 no longer needs to carry it.)*
> *(Precised 2026-09-27, cross-ticket: 059 now recommends option A-prime, one `prevStatus`
> capture directly after the refresh `Get`, with A as its runner-up; either closes the remainder
> independently of the reading chosen here.)*
>
> The documentation dependency is also discharged: the `ConditionTypeReady` doc comment this
> item wanted to decide together with T6d P1 exists at
> [`api/v1/valkey_types.go:42-46`](../../api/v1/valkey_types.go#L42-L46). **The coupling is
> therefore one-sided now — the doc exists, the decision does not.** This item is blocked on
> a human picking a reading, nothing else.

## Fact, re-verified 2026-09-27 (at `84a39c2`)

**Verified** (by reading at `4a7543e`, re-read at `84a39c2`):

- The exits above, at the corrected lines. `updateStatus` is reached only at
  [`valkey_controller.go:369`](../../internal/controller/valkey_controller.go#L369).
- **"For the whole roll" is not precise.** Two kinds of pass reach `updateStatus` while a roll is
  in flight, so `Ready`, `masterPod`, `readyReplicas` and `observerReady` are recomputed there:
  - a pass whose wait has outlived its bound: `terminationWait`, `recreationWait` and
    `availabilityWait` return `DeferredRequeueAfter`
    ([`rolling_update.go:2075`](../../internal/controller/rolling_update.go#L2075), [`:2186`](../../internal/controller/rolling_update.go#L2186), [`:2268`](../../internal/controller/rolling_update.go#L2268)), which
    `reconcileWorkload` does not return on ([`valkey_controller.go:355`](../../internal/controller/valkey_controller.go#L355); the Sentinel
    tier's at [`:485`](../../internal/controller/valkey_controller.go#L485)). ADR 0026 already says so
    ([`0026:626-634`](../adr/0026-a-pod-being-deleted-is-not-available.md): "keep their pre-roll
    values only for the budget rather than for the whole stall");
  - the pass in which the data roll pauses: `pauseRollingUpdate` returns an empty result
    ([`rolling_update.go:2646`](../../internal/controller/rolling_update.go#L2646)), so the pass continues to the Sentinel roll and,
    ~~unless that ends it~~ *(corrected 2026-09-27, review: unless a post-update check ends it -
    the Sentinel roll ([`valkey_controller.go:473-484`](../../internal/controller/valkey_controller.go#L473-L484)), the no-master
    recovery (`:422-428`) or the split-brain check (`:440-443`))*, to `updateStatus`
    ([023](023-pauserollingupdate-records-no-pause.md)).

  **No third kind** *(checked 2026-09-27 at `84a39c2`)*. Two degenerate data-roll results also
  let the pass reach `updateStatus`, and neither recomputes anything: a StatefulSet that is
  NotFound ([`rolling_update.go:239-240`](../../internal/controller/rolling_update.go#L239-L240))
  makes `updateStatus` write `Provisioning` on its own NotFound branch and return
  ([`valkey_controller.go:2188-2190`](../../internal/controller/valkey_controller.go#L2188-L2190));
  a StatefulSet that stopped being ours
  ([`rolling_update.go:246-248`](../../internal/controller/rolling_update.go#L246-L248),
  [`:3084-3086`](../../internal/controller/rolling_update.go#L3084-L3086)) returns from
  `updateStatus` before either status arm
  ([`valkey_controller.go:2199-2201`](../../internal/controller/valkey_controller.go#L2199-L2201)),
  and its `Provisioning` write is dropped because `reconcileStatefulSet` failed the step on the
  foreign object ([`:1317-1321`](../../internal/controller/valkey_controller.go#L1317-L1321)), the
  pass is blocked ([`:275-282`](../../internal/controller/valkey_controller.go#L275-L282)) and
  `updatePhase` returns on `passIsBlocked`
  ([`:2606-2609`](../../internal/controller/valkey_controller.go#L2606-L2609)). Not traced: a
  StatefulSet that changes owner between `reconcileResources` and `reconcileWorkload` of the same
  pass.

  So ADR 0001 ([`:7-11`](../adr/0001-continue-reconciling-past-a-rejected-write.md#status),
  [`:109-121`](../adr/0001-continue-reconciling-past-a-rejected-write.md)), ADR 0002
  (`0002:535-545`), the `ConditionTypeReady` doc comment
  ([`api/v1/valkey_types.go:42-46`](../../api/v1/valkey_types.go#L42-L46)) and
  [`docs/operations/status.md:17`](../operations/status.md#ready) ~~contradict~~ *(contradicted,
  until work list item 1 on 2026-09-27; all five are made precise, History)* ADR 0026 `:626-634`,
  and the code sides with ADR 0026. Work list item 1. The registry string at
  `condition_registry.go:102` still says "for the whole roll" (item 2). *(Added 2026-09-27 at
  `84a39c2`: item 1 is committed in `bcc63c9`, which is `HEAD~1`
  (`git show --stat bcc63c9`: `api/v1/valkey_types.go`, ADR 0001, ADR 0002,
  `docs/operations/status.md`). Three statements the item-1 grep could not find, because they use
  other words, are still unmarked and false:*
  - *ADR 0001 D4's own rule sentence,
    [`0001:104-107`](../adr/0001-continue-reconciling-past-a-rejected-write.md): "a pass with a
    rolling update in flight — blocked or not — returns before `updateStatus`". The same D4
    section (`:112-120`) and the Status amendment (`:13-21`) state the two recompute cases right
    below it, and the amendment says "D4 itself is unchanged", so the rule sentence contradicts
    its own dated correction. Neither ADR 0026 nor ADR 0010 references ADR 0001
    (`grep -n 0001` on both files returns nothing), so the sentence was never marked when ADR 0026
    D5 changed the behaviour. Item 3.*
  - *ADR 0002 Residual risks,
    [`0002:540-542`](../adr/0002-surface-a-blocked-reconcile-on-the-cr.md): the same bullet that
    `bcc63c9` qualified keeps the unqualified causal clause "because a pass with a roll in flight
    returns before `updateStatus`", and `:543-544` "come apart for the duration of a roll" beside it. Item 3.*
  - *The `clearSite` string of the `Ready` row,
    [`condition_registry.go:99`](../../internal/controller/condition_registry.go#L99):
    "updateStandaloneStatus / updateHAStatus recompute it every pass". A pass that ends on
    [`valkey_controller.go:340-342`](../../internal/controller/valkey_controller.go#L340-L342)
    never calls `updateStatus`. Item 4.)*
- **The complete list of what freezes** *(added 2026-09-27 at `84a39c2`)*. A pass that ends on a
  rolling-update exit skips `updateStatus` and `persistStatus`
  ([`valkey_controller.go:2548-2571`](../../internal/controller/valkey_controller.go#L2548-L2571)), so
  besides `Ready`, `readyReplicas`, `masterPod` and `observerReady` it leaves unchanged:
  - `status.operatorVersion`, whose only writer is `persistStatus`
    ([`:2555`](../../internal/controller/valkey_controller.go#L2555);
    `grep -n 'OperatorVersion =' internal/controller/*.go` outside tests finds nothing else).
    [`README.md:497`](../../README.md) describes it as the version "that last reconciled this
    resource", which a roll pass does not update;
  - the `SentinelPeersStale` level: `recordSentinelPeerDrift` is called only in the all-Ready arm
    of `updateHAStatus` ([`:2458-2462`](../../internal/controller/valkey_controller.go#L2458-L2462)),
    so it is also frozen on every non-all-Ready pass outside a roll;
  - the `Ready` condition's `ObservedGeneration`, which feeds
    `vko_valkey_status_observed_generation`, the newest `ObservedGeneration` across all conditions
    ([`internal/metrics/collector.go:201-223`](../../internal/metrics/collector.go#L201-L223));
  - `RWServiceEmpty`, reported only inside the status arms
    ([`:2232`](../../internal/controller/valkey_controller.go#L2232),
    [`:2455`](../../internal/controller/valkey_controller.go#L2455)); its evaluator judges only a
    settled cluster ([`condition_registry.go:236-246`](../../internal/controller/condition_registry.go#L236-L246)),
    so the freeze changes nothing there.

  ADR 0001's clarification (`0001:109-121`) names only the first four.
- **Shipped alerts that read a frozen value** *(added 2026-09-27 at `84a39c2`; all chart default
  off; traced by reading, not measured)*. Two can fire on a healthy roll:
  - `ValkeySpecNotObserved`
    ([`prometheusrule.yaml:32-50`](../../deploy/helm/valkey-operator/templates/prometheusrule.yaml#L32-L50),
    critical, `for: 30m`) fires while `metadata.generation` is ahead of the newest condition
    `ObservedGeneration`. A TLS CR restamps a condition on every generation change through
    `reportTLSMaterialStale`, which runs as a resource step
    ([`valkey_controller.go:563-572`](../../internal/controller/valkey_controller.go#L563-L572),
    [`tls_material.go:206-214`](../../internal/controller/tls_material.go#L206-L214)); a standalone
    CR restamps through `setSidecarUpdatePendingCondition`
    ([`rolling_update.go:3828`](../../internal/controller/rolling_update.go#L3828)), reached only
    once its single pod is replaced and available; a pause pass
    restamps through `RollingUpdatePaused`. On a multi-replica non-TLS CR that never carried
    `ReconcileBlocked`, a spec-triggered roll can leave the gap open for the whole replica phase,
    so a replica phase longer than 30 min without a pause pass can fire it on a healthy roll.
    **Timing-dependent, not decidable by reading:** on the pass that applies the CR edit,
    `dispatchDataRollingUpdate` reads the StatefulSet through the cached client
    ([`rolling_update.go:237`](../../internal/controller/rolling_update.go#L237)); if the informer
    has not yet delivered the template write, no pod looks outdated, the pass reaches
    `updateStatus` and restamps `Ready` with the new generation, which closes the gap. Only when
    the cache has caught up does the roll start in that pass and leave the gap open.
    [`docs/operations/monitoring.md:57-61`](../operations/monitoring.md) says a gap means a spec
    change "never converged".
  - `ValkeyOperatorVersionStale`
    ([`prometheusrule.yaml:104-122`](../../deploy/helm/valkey-operator/templates/prometheusrule.yaml#L104-L122),
    warning, `for: 1h`) reads `status.operatorVersion`. Every operator release rolls every
    multi-replica data tier ([ADR 0005](../adr/0005-upgrade-neutral-defaults-and-anti-affinity.md)
    D11), and the rootless release rolled persistent tiers twice, so a CR whose upgrade roll
    progresses for more than an hour without a pause or stall pass can fire it. Its description
    says the operator "writes it on every successful status update", which is true, but a roll
    pass performs none.

  One reads a frozen value in the other direction and cannot fire on a healthy roll:
  `ValkeyReplicasMissing`
  ([`prometheusrule.yaml:87-103`](../../deploy/helm/valkey-operator/templates/prometheusrule.yaml#L87-L103),
  warning, `for: 15m`) reads `status.readyReplicas`, which stays at the count of the last pass
  that reached `updateStatus`, normally the full one; a stalled roll reaches it only after the
  `syncTimeout` budget, as ADR 0026 records (`0026:631-633`). The `kubectl get` column `Ready`
  shows the same frozen field
  ([`api/v1/valkey_types.go:1133`](../../api/v1/valkey_types.go#L1133), printcolumn on
  `.status.readyReplicas`, not the condition).
- ~~**Nothing gates on the Valkey `Ready` condition.**~~ *(corrected 2026-09-27 at `84a39c2`:
  **nothing in this repository gates on it**; Flux reads it, next bullet)* The `kubectl wait --for=condition=Ready` calls
  in [`.github/workflows/release.yml`](../../.github/workflows/release.yml) wait on nodes
  (`:168`), a probe pod (`:262`) and cert-manager pods (`:337`); the e2e readers of a `Ready`
  condition read a pod's ([`test/e2e/rolling_update_test.go:717`](../../test/e2e/rolling_update_test.go#L717))
  and a Certificate's ([`test/e2e/tls_test.go:182`](../../test/e2e/tls_test.go#L182)); nothing under
  `deploy/` or `hack/` reads it (no shipped alert reads `Ready`'s status; the
  `ObservedGeneration` point above is the only indirect reader). The operator itself writes it
  only in `updateStandaloneStatus` and `updateHAStatus`
  ([`valkey_controller.go:2242-2520`](../../internal/controller/valkey_controller.go#L2242-L2520):
  `:2242`, `:2255`, `:2267`, `:2278` in `updateStandaloneStatus` `:2226-2287`, and `:2469`,
  `:2482`, `:2495`, `:2508`, `:2520` in `updateHAStatus` `:2427-2529`) and never reads it.
- **Flux kstatus reads the Valkey `Ready` condition** *(added 2026-09-27 at `84a39c2`, read in
  the upstream source)*. `status.Compute` in Flux's fork
  ([fluxcd/cli-utils v1.3.0 `pkg/kstatus/status/status.go`](https://raw.githubusercontent.com/fluxcd/cli-utils/v1.3.0/pkg/kstatus/status/status.go))
  runs `checkGenericProperties` first (`generic.go:22-71`): a deletion timestamp, then
  `checkGeneration` (`generic.go:73-99`), which reads only the top-level
  `status.observedGeneration` (`:82`) and skips the check when it is absent, then a `Reconciling`
  or `Stalled` condition (`:51`, `:54`). `ValkeyStatus`
  ([`api/v1/valkey_types.go:1099-1127`](../../api/v1/valkey_types.go#L1099-L1127)) has no
  `observedGeneration` and the CRD has no condition of either type, so `Compute` falls through
  to `checkReadyCondition` (`status.go:125`, `:154-183`): `Ready=True` gives `Current`, `False`
  or `Unknown` gives `InProgress`, and the condition's own `observedGeneration` is not read.
  Flux's default reader is `NewGenericStatusReader(mapper, status.Compute)`
  ([`polling.go:112`](https://raw.githubusercontent.com/fluxcd/cli-utils/v1.3.0/pkg/kstatus/polling/polling.go));
  kustomize-controller (`main` at `eb3c30a38821`,
  [`internal/controller/kustomization_controller.go`](https://raw.githubusercontent.com/fluxcd/kustomize-controller/main/internal/controller/kustomization_controller.go))
  requires `github.com/fluxcd/cli-utils v1.3.0` and adds only a Job reader and CEL readers
  (`:1370`), so a Valkey falls to the generic reader. It health-checks when `spec.wait` or
  `spec.healthChecks` is set (`:1023`); `wait` covers every applied object that was not skipped,
  unchanged ones included (`:1015-1021`), on every reconciliation; it waits up to `spec.timeout`
  (`:1066`) and then marks the Kustomization `Ready=False/HealthCheckFailed` (`:1075`). While it
  waits the Kustomization is `Ready=Unknown` "Reconciliation in progress" (`:330`), and a
  Kustomization that `dependsOn` it is marked `Ready=False/DependencyNotReady` and retried
  (`:251-263`). The fallback is old: `checkReadyCondition` appears in
  `kubernetes-sigs/cli-utils` at v0.20.0, v0.25.0 and v0.30.0 (auditor measurement, re-run by the
  facts skeptic).
- **A production Kustomization gates on it** *(added 2026-09-27 at `84a39c2`, read in the
  owner's local clone `/Users/hfi/repos/k8s-flux-base`, `guided-traffic/k8s-base-flux` at
  `3d105ed4`, 2026-08-27)*. `apps/iam/oauth2-proxy/ks.yml`: the Kustomization
  `oauth2-proxy-databases` (`:7`, `targetNamespace: iam` `:10`, `wait: true` `:18`,
  `interval: 1m` `:19`, `timeout: 5m` `:21`) applies `apps/iam/oauth2-proxy/databases`, which
  holds the Valkey `oauth2-valkey` (`databases/valkey/valkey-ha.yml:4-6`: 3 replicas, Sentinel
  enabled with 3 replicas, TLS enabled); the Kustomization `oauth2-proxy` `dependsOn` it
  (`:49-50`). The parent Kustomization (`components/apps.yml`) sets no `wait`, so nothing
  propagates further. The other Valkey Kustomizations set neither `wait` nor `healthChecks`:
  `gitlab-valkey`, `harbor-valkey` and `gpt-valkey` in `/Users/hfi/repos/wds18-flux-apps`
  (`b8fd5ae`, 2026-09-03; `wait` only at `repositories/ks.yml:15`, `healthChecks` nowhere), and
  `database-examples-valkey` (`components/database-examples/valkey/ks.yml` in `k8s-flux-base`).
  This agrees with the wds18 listing of 2026-08-26 in
  [archive/039](archive/039-findings-from-the-1-11-0-fleet-rollout.md) (`:3172-3178`), whose
  conclusion that "kstatus judges every Valkey `Current` regardless of `Ready`" was written
  without reading the kstatus source and is false.
- **What that consumer sees today** *(added 2026-09-27 at `84a39c2`; mechanism by reading,
  timing not measured)*. On a healthy roll `Ready` stays `True`, so the health check reads
  `Current` and the Kustomization stays healthy. Once a stall or pause pass recomputes
  `Ready=False` (ADR 0026 D5, D11; ADR 0010 D16, D17), the check reads `InProgress` and, after
  5 min, fails the Kustomization; if the roll then resumes, that `False` stays frozen until a
  later pass reaches `updateStatus`, normally the completing one, so the Kustomization stays
  failed for the rest of that roll. And for a Valkey spec change that Flux itself applies, the
  health check runs right after the apply and reads the stale `Ready=True`, possibly before the
  operator has observed the new generation, so `wait: true` gives no rollout signal for a Valkey
  CR.
- **ADR 0002 D9's kstatus rationale is false** *(added 2026-09-27 at `84a39c2`)*.
  [`0002:218-220`](../adr/0002-surface-a-blocked-reconcile-on-the-cr.md) and the doc comment
  of `setStatusCondition`
  ([`valkey_controller.go:2640-2643`](../../internal/controller/valkey_controller.go#L2640-L2643);
  the comment starts at `:2631`, the function at `:2666`)
  justify stamping `ObservedGeneration` on every condition by saying kstatus reads a condition
  without one as generation 0, "permanently stale". kstatus reads no per-condition
  `observedGeneration` at all: the only generation it reads is the top-level one
  (`generic.go:82-90`), and `checkReadyCondition` switches on the status alone. The design
  skeptic found the same in `fluxcd/cli-utils` `main` and `kubernetes-sigs/cli-utils` `master`.
  D9's rule stands, because `vko_valkey_status_observed_generation` depends on the stamp; only
  the stated reason is false. Item 5.
- **Blocked-pass suppression.** Only `updatePhase`
  ([`:2607`](../../internal/controller/valkey_controller.go#L2607)) and `persistStatus`
  ([`:2549`](../../internal/controller/valkey_controller.go#L2549)) call `passIsBlocked`
  (`grep -n passIsBlocked internal/controller/*.go` outside tests; defined at
  [`reconcile_blocked.go:171`](../../internal/controller/reconcile_blocked.go#L171));
  `setStatusCondition` ([`:2666`](../../internal/controller/valkey_controller.go#L2666)) and
  `writeStatusCondition` ([`:2689`](../../internal/controller/valkey_controller.go#L2689)) are not
  gated, so a condition written by the roll would land on a blocked pass. `persistStatus` writes
  only on a difference (`statusUnchanged`, [`:2566-2568`](../../internal/controller/valkey_controller.go#L2566-L2568),
  [`:2576-2599`](../../internal/controller/valkey_controller.go#L2576-L2599)).
- **The registry gap and its guard.** The `Ready` row carries the `declaredGap` naming T18
  ([`condition_registry.go:102`](../../internal/controller/condition_registry.go#L102); the
  package comment at [`:16-18`](../../internal/controller/condition_registry.go#L16-L18)), and
  `TestConditionRegistryGapsAreTraceable`
  ([`condition_registry_test.go:200-211`](../../internal/controller/condition_registry_test.go#L200-L211))
  demands a `T\d+` in every `declaredGap` (`:205`), which the no-ticket-citation rule of ADR 0034 now
  forbids for new text ([040](040-tracked-files-cite-work-items-instead-of-adrs.md)). Reading 1
  removes the gap and with it this conflict for the row. T18 is cited outside `docs/tickets/` at
  `condition_registry.go:17` and `:102`, `CLAUDE.md:568` and ADR 0027 `:201`, `:252`, `:332`
  (`git grep -n -w T18 -- ':!docs/tickets'` at `84a39c2`, six hits). *(Added 2026-09-27 at
  `84a39c2`:)* two tests skip a row that declares a gap: `TestConditionRegistryLevelsHaveOneEvaluator`
  (`condition_registry_test.go:149`) and the edge test (`:122`). Removing the `Ready` gap subjects
  the row to the level test for the first time, and it passes on `evaluators: 1`
  (`condition_registry.go:98`); the edge test does not apply to a level.
- **The `:102` string cannot be made precise on its own** *(added 2026-09-27 at `84a39c2`)*.
  [ADR 0034](../adr/0034-tickets-are-work-lists-that-get-archived.md) D7 (`:159-166`) binds every
  ticket citation written from 2026-09-27 on and keeps the existing ones "until they are
  rewritten". Read strictly, rewriting the `declaredGap` string while keeping its `T18:` prefix
  writes a ticket citation into Go code today, and dropping the prefix turns
  `TestConditionRegistryGapsAreTraceable` red. The string therefore lands with this ticket's
  decision (either reading deletes the gap, because the gap is the open re-decision itself) or
  with [040](040-tracked-files-cite-work-items-instead-of-adrs.md) decision 2 option A (the
  regexp becomes `ADR \d{4}` and the row cites ADR 0001 D4). The History entry of 2026-09-27 below
  (item 1 landed) read D7 the other way and allowed keeping the prefix; the work list follows the
  strict reading, which the owner may overrule.
- **Cross-ticket** *(read 2026-09-27 at `84a39c2`)*:
  - [040](040-tracked-files-cite-work-items-instead-of-adrs.md): its decision 2 and its work item 3
    touch the same line (`condition_registry.go:102`) and ADR 0027 D4; its work item 2 maps
    `CLAUDE.md:568` "`Ready`/T18" to "`Ready` (ADR 0001 D4)", which presumes the gap still
    exists. If reading 1 lands first, that sentence has to say that no gap is left, and 040
    decision 2 then governs only future gaps. Both `CLAUDE.md` edits need Hans.
  - [023](023-pauserollingupdate-records-no-pause.md): ~~its recommended option C makes
    `pauseRollingUpdate` return `DeferredRequeueAfter` and keep the state. The pause pass still
    reaches `updateStatus` but becomes a holding pass (the Sentinel roll is skipped,
    [`valkey_controller.go:465-468`](../../internal/controller/valkey_controller.go#L465-L468)), so
    the wording "the pass in which a data roll pauses … unless a post-update check (the Sentinel
    roll, …) ends it" at ADR 0001 `:118-120`, ADR 0002 `:538-539`,
    `api/v1/valkey_types.go:45-46` and `docs/operations/status.md:17` has to be revisited in
    023's change.~~ *(corrected 2026-09-27, cross-ticket, read at `84a39c2`: 023 now recommends
    its option D - keep the clear and return `DeferredRequeueAfter` - with C, which also keeps
    the state, as runner-up. Under either, `reconcileWorkload` passes
    `DeferredRequeueAfter > 0` as `dataTierHolding`
    ([`valkey_controller.go:363`](../../internal/controller/valkey_controller.go#L363)), so the
    pause pass becomes a holding pass that skips the Sentinel roll
    ([`:465-468`](../../internal/controller/valkey_controller.go#L465-L468)) and still reaches
    `updateStatus`. The sentences at ADR 0002 `:538-539`, `api/v1/valkey_types.go:45-46` and
    `docs/operations/status.md:17` stay true under both options, because a Sentinel roll that
    does not run cannot end the pass; only the reason ADR 0001 `:118` gives, "because
    `pauseRollingUpdate` returns an empty result", becomes false, and 023's change rewrites it.
    023 records the same, so the two tickets agree.)*
  - [059](059-status-readyreplicas-is-compared-against-itself.md): consistent; ~~`059:117-120`~~
    *(corrected 2026-09-27, cross-ticket: that bullet is struck and corrected in 059's Not
    verified list, "How this interacts with 018", and 059's "What it does not change" names the
    passes that skip `updateStatus`)* says the roll freeze is a different mechanism that 059 does
    not change, and reading 2 would not close 059 either, because the capture order stays.
  - [archive/039](archive/039-findings-from-the-1-11-0-fleet-rollout.md): the false kstatus
    conclusion above; the archive is history, so the correction lives here.
- ADR 0001 `:122-123` says "The `Ready` contract states this (ADR 0002 D5a)"; D5a
  (`0002:158-177`) points at the doc comment and `status.md`, which do state it. Not false; no
  change.

**Not verified:**

- Nothing was run; the two mid-roll recompute cases are traced by reading, not measured. No
  Valkey behaviour is in question here, so no docker measurement applies; no container was
  started.
- ~~The wds18 Flux claim of 2026-08-26 (no `healthChecks` on a `Kustomization` managing a Valkey
  CR) lives in another repository and was not re-checked.~~ *(corrected 2026-09-27 at
  `84a39c2`: re-checked in the local clones named above. It holds for `healthChecks` but is the
  wrong test, because `wait: true` health-checks every applied object. What is still not
  verified: the live cluster was not read, so the Kustomizations deployed on wds18 may differ
  from the clones, and the deployed kustomize-controller version was not read; the fallback
  exists in every cli-utils release checked since v0.20.0.)*
- ~~Whether a kstatus-style reader (Flux, Argo CD) would treat this CR's `Ready=True` mid-roll as
  "rolled out". No such consumer is known; it is the one argument for reading 2.~~
  *(corrected 2026-09-27 at `84a39c2`: answered for Flux in the Verified list above; kstatus reads
  a mid-roll `Ready=True` as `Current`, and the consumer exists. Argo CD was not checked; no Argo
  CD installation is known in this fleet.)*
- How long a roll of `oauth2-valkey` takes. Whether reading 2 would fail `oauth2-proxy-databases`
  (5 min timeout) depends on it; a timed roll of that CR, or of a 3+3 Sentinel TLS cluster on
  Kind, would settle it.
- Whether `ValkeySpecNotObserved` fires on a healthy roll depends on the cache race described
  above; an e2e that edits the spec of a multi-replica non-TLS CR and reads
  `vko_valkey_status_observed_generation` during the replica phase would settle it.
- Whether a tool other than kstatus reads a per-condition `observedGeneration` (the D9 comment
  says "everything modelled on it"); only the three cli-utils trees above were read.
- That the vacuous `wait: true` harms anything in practice: inferred from the failover-aware roll
  keeping the cluster serving and from oauth2-proxy not waiting for roll completion, not
  measured.

## Impact

*(Added 2026-09-27 at `84a39c2`.)* No data-plane effect: nothing in the operator reads `Ready`,
so no failover, promotion or delete decision depends on the value. The effects are on readers:

- The production Kustomization `oauth2-proxy-databases` reads the condition through kstatus on
  every reconcile (1 min). Today it passes on a healthy roll and fails once a stalled roll's
  recompute pass writes `Ready=False`; its `wait: true` carries no rollout signal for a Valkey
  spec change it applies.
- `status.readyReplicas`, `masterPod`, `observerReady`, `operatorVersion` and `SentinelPeersStale`
  are stale on roll passes; `Rolling Update i/n` carries the progress meanwhile.
- Two default-off alerts can fire on a long healthy roll (`ValkeySpecNotObserved`,
  `ValkeyOperatorVersionStale`), traced by reading, not measured.

## Options

### Decision — what `Ready` means while a rolling update is in flight (a re-decision of ADR 0001 D4, second half)

**Mechanism today.** `Ready` is computed in one place: `updateStatus`
([`valkey_controller.go:2181-2223`](../../internal/controller/valkey_controller.go#L2181-L2223))
hands over to `updateStandaloneStatus`
([`:2226-2287`](../../internal/controller/valkey_controller.go#L2226-L2287)) or `updateHAStatus`
([`:2427-2529`](../../internal/controller/valkey_controller.go#L2427-L2529)), which set `Ready`,
phase, message and `masterPod`; `persistStatus`
([`:2548-2571`](../../internal/controller/valkey_controller.go#L2548-L2571)) adds `operatorVersion`
and `observerReady` and writes only on a difference. `reconcileWorkload` reaches `updateStatus` at
[`:369`](../../internal/controller/valkey_controller.go#L369) only when the data roll returned
neither `Error` (`:336-339`) nor `NeedsRequeue` (`:340-342`) and no post-update check ended the
pass (`:363-366`). An ordinary roll pass writes `Rolling Update i/n` and ends on `NeedsRequeue`,
so every field listed under "the complete list of what freezes" keeps the value of the last pass
that reached `updateStatus`. Two kinds of pass reach it mid-roll: a wait past its bound and the
pause pass; there `updateStatus` recomputes everything and overwrites the roll's phase, the
alternation ADR 0026 records (`0026:800-810`). Outside readers: Flux kustomize-controller through
kstatus's `Ready` fallback (in production `oauth2-proxy-databases` on `oauth2-valkey`), and the
operator's own `vko_valkey_status_observed_generation`.

**What the choice changes:** what `Ready` (and, for option B, the other frozen fields) report on
a pass that ends on a rolling-update exit. **What it does not change:** the roll's phase writes,
the stall surfacing of ADR 0026 D5, D11 and ADR 0010 D16, D17 (which already recompute `Ready`
on a stuck roll, and which is what makes Flux fail on a stuck roll today), and any failover,
promotion or delete decision.

- **A — Reading 1: `Ready` is the data-plane verdict of the last pass that reached
  `updateStatus` (recommended).** Decide the current behaviour and write it down:
  - ADR 0001: a dated Status line; the "deliberately left open" sentence
    ([`0001:123-126`](../adr/0001-continue-reconciling-past-a-rejected-write.md)) struck and
    replaced by the decided reading; the clarification's list of frozen values (`:110-111`)
    completed with `operatorVersion`, `SentinelPeersStale` and the `Ready` condition's
    `ObservedGeneration`; the kstatus consequence named (a `wait: true` Kustomization reads a
    healthy roll as `Current` and gets no rollout signal).
  - ADR 0002 Residual risks (`0002:535-545`): "an open question, not a settled one" marked
    decided. D5a (`0002:158-177`) does not mention the roll and needs no change.
  - ADR 0027 `:201`, `:252`, `:332`: no declared gap is left.
  - [`condition_registry.go`](../../internal/controller/condition_registry.go): the `Ready`
    `declaredGap` (`:102`) and the package-comment sentence (`:16-18`) removed; `clearSite`
    (`:99`) precise, if item 4 has not already done it.
  - [`docs/operations/status.md:17`](../operations/status.md#ready): what a Flux Kustomization
    with `wait: true` or `healthChecks` sees during a roll.
  - [`docs/operations/monitoring.md:57-61`](../operations/monitoring.md) and ADR 0021 Residual
    risks (`0021:200-218`): a spec-triggered roll of a non-TLS CR can keep the generation gap
    open, and a long upgrade roll keeps `operatorVersion` behind.
  - [`README.md:497`](../../README.md): `operatorVersion` is written with the status, not on every
    reconcile.
  - `CLAUDE.md:568`: needs Hans (coordinate with 040 work item 2).

  Cost S: text plus one registry row, no behaviour change, no e2e. Consequences: nothing changes
  for any consumer. `oauth2-proxy-databases` keeps reading `Current` on a healthy roll and
  `InProgress` once a stall or pause pass recomputed `Ready=False`, and stays failed until the
  roll completes if it then resumes. `wait: true` stays vacuous for a Valkey spec change Flux
  applies, which is the strongest argument against A. The frozen fields stay frozen on roll
  passes, and the two alert effects above stay possible.
- **B — Reading 2 with one evaluator: `Ready=False` with a rolling-update reason while a roll of
  either tier is in flight.** Every roll exit of `reconcileWorkload` and of the Sentinel roll
  reaches `persistStatus`, with a per-pass context marker (the `passIsBlocked` pattern) that keeps
  the roll's phase and message. `updateStandaloneStatus` and `updateHAStatus` gain a
  roll-in-flight branch that writes `Ready=False` with a rolling-update reason; placed before the
  health check in `updateHAStatus`, it spares roll passes the `CheckCluster` probe of every data
  pod and Sentinel ([`:2457-2460`](../../internal/controller/valkey_controller.go#L2457-L2460)).
  Reaching `persistStatus` without that branch is not enough: it publishes the per-pass verdict
  (`False/HAClusterProvisioning` while a replica is replaced, `True/HAClusterReady` between
  replacements), which kstatus reads as `Current` or `InProgress` by poll timing. The second
  half of ADR 0001 D4 is superseded (reopened deliberately, because the meaning of `Ready` is
  the question), and ADR 0002, the ADR 0027 row and the docs are amended.
  *(Added 2026-09-27, final pass: the interaction with
  [T69](069-three-sync-checks-read-a-replica-field-from-the-master.md) option A.)* The per-pass
  verdict above comes from `updateHAStatus`, whose `AllSynced` today is the master's
  `connected_slaves` count alone (T69: a master never emits `master_sync_in_progress`), so a
  replaced replica still in its full sync already counts as synced. Under T69's recommended A,
  `AllSynced` asks every replica for the full replication answer, and the same pass reads
  `False/ReplicationSyncing` (phase `Syncing`) until that replica's sync ends, then
  `True/HAClusterReady`. For B this changes the argument, not the design: without B's
  roll-in-flight branch the published verdict still flips between False and True across
  replacements, only with a longer False stretch, so the kstatus reading stays poll-timed; with
  the branch placed before the health check, T69 A's `CheckCluster` half does not run on roll
  passes, B decides `Ready` during the roll and T69 A outside it, and the two compose without
  either reopening the other. Only the Sentinel topology is touched: `CheckCluster` has one
  caller, `updateHAStatus`
  ([`valkey_controller.go:2461`](../../internal/controller/valkey_controller.go#L2461), read at
  `84a39c2`). Under this ticket's recommended A, T69 A affects only the passes that reach
  `updateHAStatus`, which a pass ending on a roll exit does not (T69 says the same).

  Cost L plus an e2e per ADR 0017: a roll on both topologies observing `Ready=False` with the
  reason, `True` after completion, and the phase still `Rolling Update i/n`. Status writes
  happen only on change, not on every roll pass. Consequences: `readyReplicas`, `masterPod`,
  `observerReady`, `operatorVersion` and the observed generation are current mid-roll, and
  `wait: true` gains a rollout signal. `oauth2-proxy-databases` turns `InProgress` for every roll
  of `oauth2-valkey`: it stays `Ready=Unknown` while its health check waits, and `oauth2-proxy`
  is marked `DependencyNotReady` and its applies are postponed for the whole roll (running
  oauth2-proxy pods are not affected). If the roll outlasts 5 min, `oauth2-proxy-databases` fails
  with `HealthCheckFailed`; whether it does is unmeasured. This applies to rolls the Kustomization
  did not cause as well: every certificate rotation (ADR 0030), and every operator release,
  because every release rolls every multi-replica data tier (ADR 0005 D11). ADR 0005 D1 does not
  force an opt-in field, because D1 governs new CRD features and its 2026-09-26 amendment says it
  does not govern the repair of a defect; whether reading 2 is a feature or a repair of `Ready`'s
  meaning is what this decision settles. Status-semantics changes have shipped fleet-wide before
  (ADR 0002 D3, ADR 0026 D5, D11). ADR 0005 D10's "upgrade neutrality covers `status`" is the
  constraint to weigh: B changes what `Ready` reports during the roll every upgrade already
  causes, so the release that ships B has to name it.

**Recommendation: A.** It changes nothing for the one production reader of `Ready`, costs S and
breaks no standing constraint; after it `git grep -n -w T18 -- ':!docs/tickets'` is empty, the
`Ready` row has no `declaredGap`, and `TestConditionRegistryLevelsHaveOneEvaluator` checks the
row for the first time and passes on `evaluators: 1`. A stuck roll already surfaces to Flux
through the recompute of ADR 0026 D5, D11 and ADR 0010 D16, D17. B, the runner-up, buys the one
thing A lacks, a rollout signal for `wait: true`. That signal gains the dependent nothing here,
because the failover-aware roll keeps `oauth2-valkey` serving and oauth2-proxy does not need the
roll to finish. B would hold oauth2-proxy's applies through every roll, including rotation and
upgrade rolls that the Kustomization did not cause, and it costs L plus e2e. Its other gain,
current `readyReplicas`, `masterPod`, `observerReady` and `operatorVersion` during a healthy roll,
has no reader that needs them while `Rolling Update i/n` carries the progress. If a GitOps
consumer ever needs to gate on convergence, the carrier that does not redefine `Ready` is a
top-level `status.observedGeneration` (History 2026-09-27 at `84a39c2`); it is not an option for
this decision.

## Work list

1. **XS, no decision needed** *(added 2026-09-27)*: make the "whole roll" sentences precise,
   since they are false for the two recompute cases above: ADR 0001 `:9` and `:99-100` (struck
   and corrected in place, dated), ADR 0002 `:521` (same), the `ConditionTypeReady` doc comment
   `api/v1/valkey_types.go:42-44`, and `docs/operations/status.md:17`. The registry string at
   `condition_registry.go:102` says the same and is left to item 2: rewriting it touches a T18
   citation that `TestConditionRegistryGapsAreTraceable` requires. Does not close this ticket.
   *(Review 2026-09-27:)* the corrections name the pause pass as reaching the status write
   "unless a post-update check ends it", not "unless the Sentinel roll ends it": the no-master
   recovery and the split-brain check can end it too. **Done 2026-09-27**, all five places, in
   the "post-update check" wording. *(Added 2026-09-27 at `84a39c2`: committed in `bcc63c9`.)*
2. **Waits on the decision**: reading 1 (option A) as listed under Options, then `git grep` T18
   outside `docs/tickets/` and archive; or reading 2 (option B). *(Added 2026-09-27 at
   `84a39c2`:)* the registry string at `condition_registry.go:102` lands here, with the decision,
   or earlier with 040 decision 2 option A; not alone (Fact, ADR 0034 D7).
3. **XS, no decision needed** *(added 2026-09-27 at `84a39c2`)*: correct ADR 0001 D4's rule
   sentence ([`0001:104-107`](../adr/0001-continue-reconciling-past-a-rejected-write.md)) in
   place, struck and restated, with a dated Status line: a pass that ends on a rolling-update exit
   returns before `updateStatus`; since ADR 0026 D5, D11 and ADR 0010 D16, D17 a pass whose wait
   has outlived its bound continues to it, and so does the pause pass. In the same change the
   causal clause at ADR 0002 `:540-542` and "come apart for the duration of a roll" at `:543-544`. This records
   what ADR 0026 already decided; no rule changes. Correct the Status line `0001:19` ("D4 itself
   is unchanged") accordingly.
4. **XS, no decision needed** *(added 2026-09-27 at `84a39c2`)*: make the `clearSite` string of
   the `Ready` row ([`condition_registry.go:99`](../../internal/controller/condition_registry.go#L99))
   precise: the status arms recompute it on every pass that reaches `updateStatus`, which a pass
   ending on a rolling-update exit does not. It carries no ticket citation. `make test-unit` and
   `make lint`.
5. **XS, no decision needed** *(added 2026-09-27 at `84a39c2`)*: correct the kstatus rationale
   of ADR 0002 D9 ([`0002:218-220`](../adr/0002-surface-a-blocked-reconcile-on-the-cr.md)) in
   place, dated, and the matching sentence of the `setStatusCondition` doc comment
   ([`valkey_controller.go:2640-2643`](../../internal/controller/valkey_controller.go#L2640-L2643)):
   kstatus reads only a top-level `status.observedGeneration`; the stamp is kept because
   `vko_valkey_status_observed_generation` depends on it. D9's rule is unchanged.

## Decision

**Not decided.** Deferred 2026-08-26 as a re-decision, and half of it is now documented. No code changed:
this is ADR 0001 D4 operating as written, so closing it means amending that ADR in place, not
shipping a fix.

What did land is the part that was actually missing — the *price* of D4's second half is now
stated rather than implied. ADR 0001 D4 carries a clarification saying that those exits leave
`Ready`, `masterPod`, `readyReplicas` and `observerReady` at their pre-roll values ~~for the
whole roll~~ *(corrected 2026-09-27, work list item 1: the clarification now says "on every
pass that ends on a rolling-update exit" and names the two kinds of pass that recompute them)*,
and explicitly leaves open which reading of `Ready` is intended.
`vkov1.ConditionTypeReady`'s doc comment and the ~~README row~~ *(corrected 2026-09-27: since
`4a7543e` the text lives in [`docs/operations/status.md:17`](../operations/status.md#ready))* say the same, and
`conditionRegistry` carries it as a declared gap naming T18. So the behaviour is discoverable
from three places instead of zero, which is what made it look like a defect.

The open question is unchanged and is a product decision, not a code one: does `Ready` mean
"the last steady state was healthy" or "the cluster is serving now"? ~~Reading 1 needs nothing
further. Reading 2 is bigger than one write site — it means reaching `persistStatus` on every
exit, while respecting ADR 0002 D8. Also verified while deferring: nothing gates on the
condition today, in this repo or in the wds18 Flux setup (no `Kustomization` managing a
Valkey CR carries `healthChecks`), so neither reading currently breaks a consumer.~~
*(corrected 2026-09-27 at `84a39c2`: reading 1 still needs the ADR, registry and docs edits of
option A; reading 2 also needs a roll-in-flight branch in both status arms, and its status write
happens only on change. The consumer claim is false: Flux kstatus reads `Ready` for this CRD,
and `oauth2-proxy-databases` (`wait: true`, `timeout: 5m`) health-checks `oauth2-valkey`
through it. Reading 1 changes nothing for that consumer; reading 2 would turn it `InProgress`
for every roll. Options.)*

## Verification

- Item 1: `git grep -n -i "whole roll\|whole rolling update\|whole duration of a roll" -- ':!docs/tickets'`
  finds, for `Ready`, only struck text and the registry string; `make lint` is green. *(Run
  2026-09-27 after the fix: for `Ready` it finds the struck text in ADR 0001 `:9`, `:112` and
  ADR 0002 `:535` and the registry string at `condition_registry.go:102`; the other hits - ADR
  0011 `:255`, 0023 `:264`, 0024 `:72` and three e2e comments - are about other things.
  `make lint` was not run.)* *(Re-run 2026-09-27 at `84a39c2` with
  `git grep -n -i "pre-roll\|whole roll\|returns before .updateStatus\|returns before updateStatus" -- ':!docs/tickets'`:
  unstruck and false are ADR 0001 `:106` (the D4 rule sentence `:104-107`) and
  `condition_registry.go:102`; the clause at ADR 0002 `:540-542` is unstruck too, under a
  qualified heading.)*
- Item 3: the same grep finds the ADR 0001 D4 rule sentence and the ADR 0002 `:540-542` clause
  only struck, each followed by a dated correction.
- Item 4: `make test-unit` and `make lint` green after the `clearSite` edit.
- Item 5: `git grep -n "generation 0" -- ':!docs/tickets'` finds only struck text.
- Reading 1: ADR 0001 D4 records the decided reading with its date and the superseded sentence
  marked; the `Ready` registry row carries no `declaredGap` and
  `TestConditionRegistryLevelsHaveOneEvaluator` checks it; `make test-unit` and `make lint` are
  green; `git grep -n -w 'T18' -- ':!docs/tickets'` is empty.
- Reading 2: an e2e roll on both topologies observes `Ready=False` with the rolling-update reason
  and `True` after completion; the phase still reads `Rolling Update i/n` during the roll.

## History

- 2026-09-27: re-verified at `84a39c2` against an auditor report and the amendments of a facts
  skeptic and a design skeptic; disputed points re-read in the code and in the kstatus source.
  - **Checked and holding:** the five roll exits and the split-brain exit (`valkey_controller.go`
    `:336-342`, `:363-366`, `:369`, `:422-428`, `:440-443`, `:473-485`), the two recompute
    kinds, the registry gap and its test, the six T18 citations, `observerReady` after the
    capture, ADR 0026 `:626-634`, `75b3c92`, and that the `ConditionTypeReady` comment reaches no
    generated CRD.
  - **Found false or outdated, corrected in place:** the ticket's title and H1 ("for the whole
    rolling update"; old title kept in the frontmatter comment); "nothing gates on it" in the top
    section, the Fact, the justification and the Decision (Flux kstatus falls back to `Ready`,
    and `oauth2-proxy-databases`, `wait: true`, `timeout: 5m`, health-checks `oauth2-valkey`);
    the frozen-field list (plus `operatorVersion`, `SentinelPeersStale`, the `Ready`
    `ObservedGeneration`); "the only work is a doc sentence"; "reach `persistStatus` on every
    exit" as sufficient for reading 2; the Decision's "Reading 1 needs nothing further". Three
    claims of the old Options were false: reading 2's "every roll pass would cost a status write"
    (`persistStatus` skips on no difference), reaching `persistStatus` alone giving a
    rolling-update reason (it gives the per-pass verdict), and the second writer's write being
    "suppressed on a blocked pass" (`setStatusCondition` is not gated). The earlier History
    entry's "read in `git diff` of the working tree": item 1 is committed in `bcc63c9`.
  - **New findings:** ADR 0001 D4's rule sentence `0001:104-107` and the ADR 0002 `:540-542`
    clause are false and unmarked (item 3); the `clearSite` string `:99` (item 4); ADR 0002 D9's
    kstatus rationale and its code comment are false (item 5); `ValkeySpecNotObserved` and
    `ValkeyOperatorVersionStale` can read a frozen value; `archive/039`'s kstatus conclusion is
    false; the `:102` string cannot land alone under a strict reading of ADR 0034 D7, against the
    reading of the earlier entry below; cross-ticket dependencies on 040, 023, 059.
  - **Disputed and settled by re-reading:** the auditor's third, degenerate recompute case (a
    StatefulSet that stopped being ours mid-roll) is refuted: that pass reaches `updateStatus` but
    returns before the status arms, and its phase write is dropped on the blocked pass; the
    NotFound sibling recomputes nothing either. The auditor's claim that option B must be opt-in
    under ADR 0005 D1 overstated D1 and is replaced by the D10 compatibility cost. B's Flux cost
    "fails at every certificate rotation and every fleet-rolling release" is conditional on the
    unmeasured roll duration; the certain cost is `DependencyNotReady` on `oauth2-proxy` for the
    whole roll.
  - **Measured / read:** `git show --stat bcc63c9`; `git grep -n -w T18 -- ':!docs/tickets'` (six
    hits); `grep -n passIsBlocked` and `grep -n 'OperatorVersion ='` outside tests;
    `curl` of fluxcd/cli-utils v1.3.0 `status.go` and `generic.go` and of kustomize-controller
    `main` (`eb3c30a38821`) `kustomization_controller.go` and `go.mod`; `grep` of the local Flux
    clones `k8s-flux-base` (`3d105ed4`) and `wds18-flux-apps` (`b8fd5ae`). No docker measurement
    (no Valkey behaviour is in question); no container was started. Nothing was run in the
    repository.
  - **Locations re-read at `84a39c2`** and fixed directly where only the line moved:
    `rolling_update.go` `:2075`, `:2186`, `:2268`, `:2646`; `valkey_controller.go` `:2557-2564`,
    `:2592`, `:2242-2520`; ADR 0001 `:100-126`, `:7-11`, `:109-121`; ADR 0002 `:491-529`,
    `:535-545`; ADR 0026 `:626-634`; `api/v1/valkey_types.go:42-46`;
    `condition_registry.go:16-18`; `condition_registry_test.go:200-211`.
  - **Options:** rewritten as one coherent decision. Kept: A (reading 1, still recommended) and B
    (reading 2 with one evaluator, runner-up, mechanism corrected: it needs a roll-in-flight
    branch in both arms). Removed: *"Reading 2 by a second writer — the roll writes `Ready=False`
    next to its own phase"* (a second evaluator of a level beside `updateStatus`) — it builds the
    level race ADR 0027 exists to prevent, alternates `Ready` on every stall and pause pass the
    way the phase already does (`0026:800-810`), leaves `masterPod` and the other fields frozen,
    rests partly on the false "suppressed on a blocked pass" premise, and has B's Flux cost with a
    worse mechanism. Considered and not offered: *a kstatus `Reconciling` condition, True while a
    roll is in flight* — speculative scope, nobody asked to gate on roll completion, and it has
    B's Flux cost (`generic.go:51` short-circuits before the `Ready` fallback); *a top-level
    `status.observedGeneration` written in `persistStatus`* — the targeted carrier if a GitOps
    consumer ever needs to gate on convergence of a CR edit (kstatus `checkGeneration`, `generic.go:73-99`;
    rotation and upgrade rolls bump no generation), with open points for blocked, stall and
    pause passes; `archive/039` already named it; not a re-decision of `Ready` and nobody asked
    for it, so not an option here and not a work item. The recommendation stays A, and its basis
    changed: from "nothing reads the condition" (false) to "A changes nothing for the one
    production reader, and B would hold that reader through every roll, including rolls it did
    not cause"; the vacuous-`wait` trade-off is now stated next to A.
  - **Frontmatter:** `title` rewritten (old value in its comment); `urgency` stays `now` by
    rule 1, with the comment now naming all six false places and that `:102` waits on this
    decision or 040 decision 2; `icebox` (rule 5) afterwards, not `later` (rule 4), because what
    remains is a re-decision of documented behaviour, not a fix. `effort` comment names items 3-5.
    `state`, `severity` (low: the only production reader passes on a healthy roll and fails on a
    stalled one, and the alert effects need default-off alerts and rolls past 30 min or 1 h),
    `security` (none), `blocked-by` (human) unchanged.
  - **Review of this entry's edit, same day:** spot-checked at `84a39c2` every location of
    `valkey_controller.go`, `rolling_update.go`, `condition_registry.go` and its test, ADR 0001,
    0002, 0005, 0026 and 0034, the alert rules, `collector.go`, `tls_material.go` and
    `valkey_types.go` the entry cites, the kstatus and kustomize-controller source (re-fetched)
    and the local `k8s-flux-base` clone; all hold except one. The kstatus sentence at
    `valkey_controller.go:2640-2643` belongs to the doc comment of `setStatusCondition`, not of
    `writeStatusCondition` (Fact and item 5 fixed). Added: `ValkeyReplicasMissing` and the
    `kubectl get` column `Ready` read the frozen `readyReplicas` (in the harmless direction), and
    the standalone restamp happens only once the single pod is replaced. The "nothing gates"
    bullet now strikes before it corrects. **Not verified:** nothing was run.
  - Cross-ticket: in the consistency pass of the same day, the 023 note was corrected in place
    (023 now recommends D, not C; under both the pause pass holds the Sentinel roll through
    `dataTierHolding`, `valkey_controller.go:363`, and only ADR 0001 `:118`'s reason becomes
    false, which 023 also states), the 059 cite `059:117-120` was replaced by the section names
    it moved to, and the superseded note on 059's option A was precised (059 now recommends
    A-prime, A runner-up, either closes the remainder); 040 decision 2 and this ticket's
    decision remain the two ways the `condition_registry.go:102` string can land, as 040 also
    records.
  - Final pass: option B gained a note on its interaction with T69 option A (under T69 A the
    per-pass verdict between replacements on a Sentinel cluster reads `False/ReplicationSyncing`
    while a replaced replica is still in its full sync; B's roll-in-flight branch before the
    health check composes with it, and the flip argument for the branch stands), checked against
    T69's Options and Related tickets and against `updateHAStatus` and the one `CheckCluster`
    caller at `84a39c2`. Recommendation and frontmatter unchanged.
- 2026-09-27: work list item 1 landed, file by file (read in `git diff` of the working tree):
  - [ADR 0001](../adr/0001-continue-reconciling-past-a-rejected-write.md): "for the whole roll"
    in Status (`:9`) and "for the whole duration of a roll" under D4 (`:112`) struck and
    corrected in place ("on every pass that ends on a rolling-update exit", naming the pass past
    a wait bound and the pause pass, "unless a post-update check ends that pass"); a dated
    "Amended 2026-09-27 (correction, no decision changes)" Status line; D4 itself unchanged.
  - [ADR 0002](../adr/0002-surface-a-blocked-reconcile-on-the-cr.md): the Residual-risks bullet
    (`:535` now) struck and corrected the same way; a dated Status line that also carries the
    T35 correction of D11.
  - [`api/v1/valkey_types.go`](../../api/v1/valkey_types.go), the `ConditionTypeReady` doc
    comment (`:42-46` now): "a pass that ends on a rolling-update exit returns before
    updateStatus …, so during a roll Ready keeps the value of the last pass that reached
    updateStatus (ADR 0001 D4). A pass whose wait has outlived its bound (ADR 0026 D5, D11) and
    the pass in which a data roll pauses do reach it, unless a post-update check ends the pass."
  - [`docs/operations/status.md:17`](../operations/status.md#ready): "keeps the value of the last
    status computation", both recompute cases named, a dated corrected marker.

  The registry string (`condition_registry.go:102`) is untouched, as planned (item 2). The doc
  comment grew by two lines, so `api/v1/valkey_types.go` references after `:44` in the tickets
  are two lower than the working tree now. The implementer asked for `make manifests` because
  an `api/v1` comment changed; a grep shows the `ConditionTypeReady` comment in neither
  `config/crd/bases/` nor the chart CRD (it documents a const, not a field), so no generated
  diff is expected - **not run**. **Urgency not recomputed in this pass** (the orchestrating run
  left every urgency but one to the owner). The frontmatter says it returns to `icebox` once
  item 1 lands, but read strictly rule 1 still matches: the registry string at
  `condition_registry.go:102` ("Ready keeps its pre-roll value for the whole roll") is itself a
  false statement in a tracked file, left to item 2 only because rewriting that line touches the
  `T18:` prefix `TestConditionRegistryGapsAreTraceable` requires (ticket 040, decision 2). So
  `now` holds until that string is made precise - keeping the prefix, or under 040's decision 2 -
  and `icebox` (rule 5) after it. **Not verified:** `make lint` was not run.
- 2026-09-27: adversarial review of the enrichment. Re-read at `4a7543e`: the exits
  `valkey_controller.go:336-342`, `:355`, `:363-366`, `:369`, `:422-428`, `:440-443`,
  `:473-485`; `rolling_update.go:2074`, `:2185`, `:2267`, `:2643`; ADR 0001 `:9`, `:88-107`,
  ADR 0002 `:521`, ADR 0026 `:629-633`, `valkey_types.go:42-44`, `status.md:17`,
  `condition_registry.go:17`, `:102`, `condition_registry_test.go:200-210` hold. Two
  precisions: the pause pass reaches `updateStatus` unless any post-update check ends it, not
  only the Sentinel roll (struck and corrected in place; the same wording belongs in the
  item 1 edits), and reading 1 also has to fix the registry row's `clearSite` string. Reading 1
  stays marked; work list item 1 is confirmed as XS with no decision. **Verified:** by reading.
  **Not verified:** nothing was run.
- 2026-09-27: enriched - re-verified at `4a7543e` and corrected every stale location in place
  (exits, ADR 0001 range, README row now `status.md:17`, the ADR 0002 residual now carried by
  059); added Fact, Options, Work list and Verification. Found that "for the whole roll" is false
  for a pass past a wait bound and for the pass that pauses. Urgency `icebox` -> `now` by rule 1
  (those sentences are false by code reading; back to `icebox` under rule 5 once work list item 1
  lands); effort `S–L` -> `S` (the recommended reading 1). **Verified:** by reading and grep at
  `4a7543e`. **Not verified:** nothing was run; the wds18 claim was not re-checked.
- 2026-09-27 - extracted verbatim from the collection ticket (now [archive/039-findings-from-the-1-11-0-fleet-rollout.md](archive/039-findings-from-the-1-11-0-fleet-rollout.md)) into its own file when the tickets were numbered. Frontmatter filled from the final board row (board archive of that file, groomed 2026-09-26) and from the section text.
