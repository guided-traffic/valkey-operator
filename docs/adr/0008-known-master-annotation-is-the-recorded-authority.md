# ADR 0008: The Known-Master Annotation Is the Operator's Recorded Master Authority

## Status

Accepted. Date: 2026-08-21. Applies to the **non-Sentinel** path; the Sentinel path
maintains the same annotation but takes its live truth from Sentinel.

Implemented on branch `feat/support-pdb`; the self-claim and the steady-state check
landed together in `2357946`, which no release tag contains — verified by reading this
repository. Init-script behaviour is verified by executing the generated script, not by
asserting on its text ([ADR 0017](0017-test-and-ci-policy.md)). The incident observations
in Context are operator logs of cluster runs and are not reproducible from this
repository.

## Context

Without Sentinel there is no external arbiter of who the master is. Three actors need an
answer and none of them can derive it alone:

* **A booting pod.** Its init container asks its peers, and only its peers — the Phase 1
  loop skips the pod's own host, so a pod can never discover itself as master. The peer
  test requires `role:master && connected_slaves > 0`, because a pod answering
  `role:master` with zero slaves is **indistinguishable** from a pod that elected itself
  in isolation. A freshly promoted master in a 2-replica cluster has exactly zero slaves,
  so peer discovery rejects the real master.
* **The split-brain resolver during a rolling update.** During a manual failover two
  pods report master by design — the promoted pod (`REPLICAOF NO ONE`) and the old
  master, which answers until it terminates. With two replicas neither has a connected
  slave at that instant, so a "most connected slaves" heuristic ties at zero and the tie
  goes to the lowest ordinal: **pod-0, the pod the operator just deleted**. The promoted
  pod-1 was then demoted with `REPLICAOF <pod-0>`, pointing the only surviving copy of
  the data at a disappearing pod. Observed live in the `e2e-rolling-two-replicas`
  namespace that `TestE2E_RollingUpdate_TwoReplicasNoSentinel` creates: the pre-update key
  was gone at the end of the run. The test is in this repository; the run output that
  recorded the loss is not, so the observation itself cannot be reproduced from the tree.
* **The steady-state check**, outside any rolling update
  ([ADR 0011](0011-evidence-based-steady-state-split-brain-resolution.md)).

Three replicas hide this entirely — `promoteAndRedirect` attaches the third pod, so the
promoted one wins outright — which is why the 3-replica e2e stayed green throughout (a
report of past runs; this repository holds no run record to check it against).

## Decision

**D1 — `vko.gtrfc.com/known-master` carries the address of the pod the operator
currently considers master, and it is a data-plane authority, not telemetry.** It feeds
the `replicaof` directive of the replica ConfigMap through `GenerateValkeyConf`. Three
consumers read one recorded truth: the init container, the rolling-update split-brain
resolver, and the steady-state check.

**D2 — The address never enters the config hash.** `GenerateValkeyConfForHash`
deliberately ignores the `replicaof` override. The annotation changes on every failover;
if it were hashed, every promotion would mark every pod outdated and trigger a rolling
restart of the very pods the failover just stabilised — a restart storm caused by the
recovery mechanism itself. Any future failover-varying directive must be excluded the
same way.

**D3 — Both paths maintain it.** Sentinel path: `syncSentinelWithMaster` persists the
confirmed master at finalization, through `persistKnownMaster`. Non-Sentinel path:
`handleManualFailover` publishes the promoted pod, and `promotePod0AndRedirect` points it
back at pod-0 once the topology is restored.

**D4 — Publish the known master and republish the replica ConfigMap *before* deleting
the old master.** A pod mounts the ConfigMap as it exists when the kubelet starts it, so
a write landing after the delete may or may not reach the recreated pod. Publishing first
is the only way to guarantee that a returning pod-0 reads the new master address instead
of the stale pod-0 default and elects itself.

**D5 — The ConfigMap republish is best-effort; the annotation write is not.** A failed
`reconcileReplicaConfigMap` is logged, raised as a `KnownMasterPublishFailed` Warning,
and the old-master delete proceeds — blocking it would leave the CR in `manualFailover`
with an undeleted old master and stall the rolling update indefinitely, which is worse
than the window this publish closes. The asymmetry with `persistManualFailoverState` is
deliberate and is the subject of
[ADR 0009](0009-an-unrecorded-promotion-is-not-a-promotion.md).

**D6 — The init container's master election is ranked, and the ranking is load-bearing
in both directions:**

```
Phase 1  peer discovery  (peers only, role:master && connected_slaves > 0)
Phase 2  read the known-master address out of the mounted replica config:
         adopt the recorded peer, or set SELF_IS_KNOWN_MASTER when it names this pod
Phase 3  apply — discovered or recorded master, else the self-claim, else the ordinal
         fallback (ordinal 0 becomes master)
```

The phases are not the ranking. Phase 2 only decides what the record says; Phase 3 is
where the decision is applied, and its own header names the order: discovery result, then
the self-claim, then the ordinal fallback. `SELF_IS_KNOWN_MASTER` is therefore set in
Phase 2 and consumed in Phase 3.

The recorded address sits **below** peer discovery so a stale record can never displace
an established master with replicas, and **above** the ordinal fallback because peer
discovery rejects a zero-slave master.

**D7 — Phase 2 adopts a *peer* only under two guards: not-self, and a live `role:master`
probe.** The self guard makes the default configuration a no-op — outside a failover the
replica config names pod-0, so pod-0 matches itself, takes the self-claim branch (D8) and
copies the same master config the ordinal fallback would have given it, which is why
upgrades change nothing on healthy clusters. The `role:master` probe makes a stale
annotation harmless: if the recorded pod is gone or has been demoted, the step declines
and the previous behaviour returns instead of chaining a pod onto a dead address.

**D8 — A pod named in its own replica config boots as master (the self-claim).** When
the mounted replica config's known-master value equals the pod's own host, the init
script sets `SELF_IS_KNOWN_MASTER=1` and copies the master config instead of taking the
ordinal fallback. This exists because a full pod-set restart (a node reboot suffices for
a 2-replica cluster) makes pod-0 find no peer and elect itself, while the returning
promoted pod is rejected by Phase 1 and previously fell through to the ordinal fallback —
losing the only surviving copy of the post-failover writes. That earlier shape is in git
history rather than only in this text: before `2357946` the Phase 2 guard required
`KNOWN_MASTER != MY_HOST`, so the recorded pod skipped its own record. Persistence closes
neither degenerate shape: pod-0 restores its pre-failover RDB/AOF and the promoted pod
still syncs from it.

**D9 — The self-claim must not ship without the steady-state split-brain check.** It can
produce two masters when pod-0 has already elected itself, and only the operator-side
check consolidates them — using the *same* annotation as authority on both sides. Any
refactor that disables or bypasses `checkSteadyStateSplitBrain` must also disable the
self-claim.

**D10 — During the failover states the resolver is fed a named authority; the heuristic
is a fallback that must never decide while an authority exists.**
`handleMultiReplicaRollingUpdate` passes `vko.gtrfc.com/promoted-pod` for
`stateManualFailover` and `stateReplacingMaster`, and the **known-master** annotation for
`stateRestoringTopology` and `stateVerifyingTopology`. Those four are exactly the states
in which the operator promoted a pod itself. Every other pass through that switch reaches
`detectAndResolveSplitBrain` with an empty authority, and is meant to: before any state is
set and during `stateReplacingReplicas` the master is the one the cluster already had, the
operator has no promotion on record, and the connected-slaves heuristic then picks the
master that is actually serving replicas. (`stateFailoverTriggered` and
`stateFailoverReset` fall through the same way, but they belong to the Sentinel path,
which names the Sentinel-reported master at its own call site; they reach this switch only
as leftovers on a cluster whose Sentinel was disabled mid-update.) **The rule is therefore
narrower than "never pass an empty authority": any state in which the operator itself
promoted a pod must name that pod here** — leaving it empty there is the zero-tie of the
Context, one state later.

**D11 — The promoted-pod annotation may not be used as authority in the restoration
states.** It lives on through them, and passing it there would name the pod the operator
is deliberately demoting as the real master, inverting the restoration it just performed.
The switch in `handleMultiReplicaRollingUpdate` is the single place that decision is made;
**any new rolling-update state must choose its authority explicitly there.**

**D12 — `clearRollingUpdateState` does not clear the known master.** It removes every
other rolling-update annotation — `rolling-update-state`, `promoted-pod`,
`failover-timestamp`, `reconnect-reset-count`, `sentinel-awareness-started` and all four
wait bounds (`sync-wait-started`, `finalization-started`, `topology-restore-started`,
`manual-failover-started`) — drops the in-memory copies of the three tracked bounds
(`forgetWaitBounds`) and clears the drain stamps. The known master survives all of it: the
annotation is the authority outside the rolling update as well, so clearing it would leave
the steady-state check with nothing to work from — and on the abandoned path it still
names the promoted replica, which is what keeps a non-pod-0 master coherent.

**D13 — There is no single funnel for the annotation, and no design may assume one.**
`recordPromotedMaster` is one writer; `persistManualFailoverState` writes it directly
(one `Update`, for its conflict retry), `syncSentinelWithMaster` goes through
`persistKnownMaster`, and `verifyTopologyRestored`, `finalizeMultiReplicaRollingUpdate`
and `handlePostManualFailover` end a rolling update without recording a master at all.
**Anything that must happen once per completed promotion belongs at
`clearRollingUpdateState`**, where all of those paths converge — hanging it off
`recordPromotedMaster` silently skips the manual-failover and Sentinel paths.

**D14 — A non-pod-0 master is a supported end state.** The `-rw` and `-r` Services select
on the `vko.gtrfc.com/instanceRole` label, never on ordinal. This is the precondition that
makes abandoning a topology restoration safe at all: if clients only reached pod-0,
giving up on returning the master role to pod-0 would be an outage rather than a cosmetic
loss.

**D15 — 2-replica non-Sentinel stays a supported topology.** `spec.replicas` carries only
`Minimum=1` and no CEL rule ties it to Sentinel. The exposure is closed in the init
container and the controller, not by forbidding the topology: the failure was reachable in
a supported configuration, so rejecting it at admission would break existing clusters
rather than fix them.

**D16 — The annotation is a tie-breaker among multiple masters; it never overrules a
single, undisputed one.** Sharpened in
[ADR 0011](0011-evidence-based-steady-state-split-brain-resolution.md), where it belongs
with the evidence rules.

## Consequences

* Every promotion site is obliged to write the annotation, and the write is part of the
  promotion rather than a report about it
  ([ADR 0009](0009-an-unrecorded-promotion-is-not-a-promotion.md)).
* Two config-rendering functions must stay in sync in everything except the `replicaof`
  override (D2).
* The non-Sentinel path depends on the annotation being present and current for
  correctness **at pod boot**, which is what makes the recording invariant and the
  adoption logic load-bearing rather than cosmetic.
* Any future change that reintroduces ordinal-based routing, or assumes pod-0 is the
  master, breaks the abandon path, the drain path and the steady-state adoption path at
  once (D14).
* The 2-replica non-Sentinel topology must stay covered by e2e
  (`TestE2E_RollingUpdate_TwoReplicasNoSentinel`) — three replicas hide this whole class
  of bug, so a 3-replica-only suite is not sufficient coverage.
* A stale `instanceRole=master` label on a pod that is actually a replica makes that pod
  a write target until something corrects it. The steady-state check only *demotes* when
  at least two pods are labeled master — a single labeled master goes to
  `adoptUnrecordedPromotion` instead — and deliberately does not fix a label it does not
  own.
* The self-claim changes the init-script text, so the release rolls non-Sentinel
  multi-replica clusters once through the failover-aware cycle
  ([ADR 0005](0005-upgrade-neutral-defaults-and-anti-affinity.md) D11).

## Alternatives Considered

### Leave the master decision to the init container's peer discovery alone

Undecidable from the pod's viewpoint: a promoted master with zero replicas and a rogue
master with zero replicas look identical over `INFO replication`. The operator can tell
them apart because it performed the promotion.

### Place the known-master step above peer discovery

Rejected: a stale record would overrule a live, established master.

### Trust the recorded address unconditionally at boot

Rejected: it would chain a returning pod onto a demoted or absent peer.

### Accept any `role:master` peer in Phase 1

Rejected: a self-elected pod answers identically.

### Hash the full rendered config

Rejected: it couples every failover to a rolling restart.

### Fail the pass when the replica-ConfigMap publish is rejected

Rejected: it stalls the rolling update indefinitely with an undeleted old master.

### Improve the connected-slaves heuristic

Rejected: the operator already knows which pod it promoted, so a heuristic is the wrong
instrument.

### Pass the promoted-pod annotation unconditionally while it exists

Rejected: it inverts topology restoration (D11).

### Clear all rolling-update annotations uniformly

Rejected: it would destroy the steady-state authority (D12).

### Refactor every writer through `recordPromotedMaster`

Not chosen: `persistManualFailoverState` needs its own single `Update` for the conflict
retry. The convergence point is `clearRollingUpdateState` instead.

### Select the write Service on `statefulset.kubernetes.io/pod-name: <name>-0`

Rejected: it would require force-promoting pod-0 and thereby discarding a promoted
replica's data.

### A CEL rule forbidding `replicas: 2` without Sentinel

Rejected: it breaks existing clusters instead of fixing them, and the 2-replica case is
exactly where the operator's own failover produces a zero-slave master anyway.

### Make pod-0's ordinal fallback wait for the recorded master before self-electing

Narrows the window, but needs a timeout to avoid deadlocking a genuine first boot — and
after the timeout the original behaviour returns.

### Ship the self-claim first and the consolidating check later

Explicitly forbidden: it trades a data-loss window for an unconsolidated split brain,
with the `-rw` Service round-robining writes across two independent datasets.

## Residual risks

* **Both pods down and returning together.** The data StatefulSet runs
  `PodManagementPolicy: Parallel`, so every pod is recreated at once and no ordinal start
  order is guaranteed. A pod-0 that reaches Phase 3 with no peer reachable takes the
  ordinal fallback as master even when the other pod held the post-failover data. Phase 1
  skips the pod's own host, so with every peer still down there is nothing left to
  confirm the record against, and refusing to boot at all would be worse.
* **A rejected replica-ConfigMap write** returns the original pre-fix behaviour for that
  window. Surfaced by the `KnownMasterPublishFailed` Event and the `ReconcileBlocked`
  condition; the operator cannot write through an admission block.
* **Writes that reach an independently elected pod-0 during the window are discarded**
  when it starts syncing. The operator repairs the *topology* —
  `handlePostManualFailover` sends `REPLICAOF <promoted>` to a returned pod-0 and
  `stateVerifyingTopology` runs the resolver — so the split is a window, not a permanent
  state, but the data in that window is not repaired. Window width was never measured and
  is deliberately not decision-relevant.
* **A stale mounted replica ConfigMap** (kubelet refresh lag, up to ~1 min — the
  kubelet's own sync period, not measured here) can make a pod self-claim after the
  operator re-pointed the annotation. Bounded by the steady-state check and its 15 s
  recheck, and impossible to close inside the init script — the self-claim must outrank a
  zero-slave master, which is precisely what makes the two indistinguishable at boot.
* **In a ≥ 3-replica cluster where a returning pod-0 has already attracted a replica
  before the promoted pod boots**, Phase 1 hands the promoted pod to pod-0 and its writes
  are still lost. Narrower than before the self-claim, not eliminated.
* **A genuinely empty `KNOWN_MASTER`** cannot match the self test, because `MY_HOST` is
  never empty in a pod — stated so the guard is not "hardened" into something weaker.

## References

* [`internal/builder/statefulset.go`](../../internal/builder/statefulset.go) — the non-Sentinel init script (`init-config-selector`), `SELF_IS_KNOWN_MASTER`
* [`internal/builder/configmap.go`](../../internal/builder/configmap.go) — `GenerateValkeyConf`, `GenerateValkeyConfForHash`
* [`internal/controller/rolling_update.go`](../../internal/controller/rolling_update.go) — `handleManualFailover`, `persistManualFailoverState`, `promotePod0AndRedirect`, `clearRollingUpdateState`, `detectAndResolveSplitBrain`, `knownMasterPodName`
* [`internal/builder/service.go`](../../internal/builder/service.go) — the `-rw` / `-r` selectors
* [ADR 0007](0007-failover-aware-rolling-update.md) — the sequence that creates the failover window
* [ADR 0009](0009-an-unrecorded-promotion-is-not-a-promotion.md) — why every write of this annotation is part of the promotion
* [ADR 0011](0011-evidence-based-steady-state-split-brain-resolution.md) — the tie-breaker-not-override rule, and what happens outside a rolling update
* [ADR 0012](0012-the-sidecar-records-its-drain-promotion-on-the-pod.md) — the promotion the operator cannot record at all
