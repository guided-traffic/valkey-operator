# ADR 0028: A demotion may not discard the only dataset

## Status

Accepted. Date: 2026-08-26.

Implemented: the dataset veto of D1, D3 and D4, the drain-stamp rule of D2, the ambiguity
refusal of D5, the silence of D6 and the companion stamp clear of D7. Unit coverage in
[`internal/controller/split_brain_dataset_test.go`](../../internal/controller/split_brain_dataset_test.go),
field coverage in
[`test/e2e/split_brain_dataset_test.go`](../../test/e2e/split_brain_dataset_test.go).

Amends [ADR 0008](0008-known-master-annotation-is-the-recorded-authority.md) D10 (the named
authority is no longer unconditional) and
[ADR 0011](0011-evidence-based-steady-state-split-brain-resolution.md) D3 (its premise about
what the operator knows inside a rolling update) in place.

Open, and named as such: `vko.gtrfc.com/promoted-pod` is not rewritten by the adoption of D2
(*Residual risks*).

## Context

`detectAndResolveSplitBrain` resolves a multi-master state during a rolling update by naming a
real master and sending `REPLICAOF` to every other one. `REPLICAOF` discards the demoted pod's
dataset. Until this change the function had exactly one rule: **if the authority the caller
named answers `role:master`, it is the real master, unconditionally.**

**Measured 2026-08-23, kind, Kubernetes 1.36** (item T11 of `docs/tickets/local_neue_baustellen.md`,
found while verifying [ADR 0023](0023-volume-claim-templates-are-immutable.md) end to end). A
three-replica cluster, pod-0 the recorded master. Pod-0 was deleted; its sidecar drained and
promoted the last surviving replica, which held the data — the
[ADR 0012](0012-the-sidecar-records-its-drain-promotion-on-the-pod.md) path working as designed.
Pod-0 came back on a fresh, empty volume, still named by `vko.gtrfc.com/known-master`, and
reported master:

```
Split-brain detected: multiple masters found   masterCount=2 masterIndices=[0 2]
Split-brain resolution: identified real master realMaster=probe-0 rogueCount=1
Demoting rogue master to replica               roguePod=probe-2 realMaster=probe-0
Successfully demoted rogue master
```

End state: `phase=OK`, replication up, `DBSIZE=0` on every pod. A healthy-looking cluster with
nothing in it.

### The record is what made the empty pod a master

This is not a resolver that failed to notice an empty pod. It is a loop:

1. `known-master` names pod-0, so the replica ConfigMap's `replicaof` directive names pod-0
   ([ADR 0008](0008-known-master-annotation-is-the-recorded-authority.md) D1).
2. Pod-0 boots, reads the ConfigMap it is itself named in, and takes the init-script self-claim
   ([ADR 0008](0008-known-master-annotation-is-the-recorded-authority.md) D8, D9) — on whatever
   volume it happens to have. An empty volume changes nothing about the claim.
3. Answering `role:master` is the **only** precondition the resolver put on the authority.
4. The pod that actually holds the data is by construction *not* the recorded one — the sidecar
   promoted it and has no CR access to record that — so it is the rogue, and it is demoted.

The exposure is therefore not "a fresh volume" but **any path that separates the recorded name
from the data**, with a returning pod on a volume that lost the dataset: a non-persistent
cluster, a recreated PVC, a changed `storageClass`.

### Why none of ADR 0011's rules could simply be reused

[ADR 0011](0011-evidence-based-steady-state-split-brain-resolution.md) already solves the same
shape outside a rolling update, and would have refused this exact demotion: rule 2's
`refuseDemotion` fires on its structural branch, because the authority is pod-0 and the rogue
could not have self-elected. It never gets the chance — `checkAndHandleRollingUpdate` runs the
roll resolver, and the demotion has happened by the time `handlePostRollingUpdateChecks` is
reached in the same pass.

Porting `refuseDemotion` verbatim breaks the rolling update, and the reason generalises:

* **Creation order.** In `stateManualFailover` the authority is the just-replaced promoted pod,
  which is younger than the outgoing master and inside `syncTimeout`. `recreatedAfter` would
  refuse **every** normal failover.
* **The structural rule.** In `stateRestoringTopology` the authority is pod-0 and the rogue is
  the promoted replica, which by construction could not have self-elected. It would refuse the
  **designed** end of every topology restoration.

> **Inside a rolling update, every structural or temporal signal is a state the operator itself
> produces. Only two signals still discriminate: the drain stamp and the dataset.**

The dataset qualifies because the operator never promotes an empty candidate over a non-empty
master — `verifyPromotionCandidateHoldsData`
([ADR 0007](0007-failover-aware-rolling-update.md) D10) — so "the authority holds no keys while
the rogue holds some" is a state the operator cannot have created. The stamp qualifies because
`recordPromotedMaster` clears every stamp of the cluster at the moment the operator records a
promotion of its own ([ADR 0011](0011-evidence-based-steady-state-split-brain-resolution.md)
D16), so a stamp still present during a roll is a promotion that happened *after* the last one
the operator recorded.

## Decision

**D1 — A demotion that would discard the only dataset is refused.** Before each `REPLICAOF`,
`demotionRefusalReason` compares `DBSIZE` on the authority and on the rogue. An authority
holding zero keys while the rogue holds some ends the demotion of that rogue. Both empty is
**not** a refusal: an empty cluster is a legitimate state, and refusing there would stall the
resolution of every cluster that holds no data yet. An authority that holds keys of its own is
not the shape at all and costs a single `DBSIZE`.

**D2 — The drain stamp outranks the recorded authority, and the adoption is recorded first.**
Exactly one reported master carrying `vko.gtrfc.com/drain-promoted-at` becomes the real master,
recorded through `recordPromotedMaster` before anything is demoted. This is
[ADR 0011](0011-evidence-based-steady-state-split-brain-resolution.md) D5 rule 1 applied at a
second site and it obeys the same gate as `adoptAndConsolidate`: **a promotion the operator
could not record is not an authority**
([ADR 0009](0009-an-unrecorded-promotion-is-not-a-promotion.md)), so a failed record resolves
nothing that pass and the next one retries with the stamp still in place. The stamp is read off
the `Pod` object `podState` already holds, so the rule costs no connection, and the role is not
re-probed — `collectPodStates` built the state from a live `INFO` moments earlier, which is the
same confirmation.

**D3 — Fail closed. An unreadable key count is a refusal, not a demotion.** Same reading as
[ADR 0007](0007-failover-aware-rolling-update.md) D3 and
[ADR 0011](0011-evidence-based-steady-state-split-brain-resolution.md) D6: silence is not
evidence, and the destructive direction needs positive justification. **The asymmetry with the
promotion path is deliberate rather than overlooked.** There an unreadable count costs a wait
while the master keeps serving; here it costs a growing divergence between two masters that both
accept writes, for the length of the bound — and the operator ships no write fencing, so both
sides keep accepting them (item T12). The trade is still one-sided: the divergence is bounded,
visible and repairable by a human, and a wrong `REPLICAOF` is none of the three.

**D4 — The veto guards the demotion, whatever chose the authority.** It applies to the stamp
rule, to the named authority and to the connected-slaves tiebreak alike. The tiebreak needs it
most: with a shrunken cluster every master reports zero connected slaves and the tie falls to
the lowest ordinal ([ADR 0011](0011-evidence-based-steady-state-split-brain-resolution.md) D3),
which in this shape is the empty pod-0. **"Treat an empty authority as no authority and fall
through to the tiebreak" was considered and rejected for exactly that reason** — it reaches the
same loss by a longer route.

**D5 — Ambiguous evidence ends the resolution.** More than one reported master carrying a stamp
demotes nobody, the same direction
[ADR 0011](0011-evidence-based-steady-state-split-brain-resolution.md) D10 takes: falling
through to the authority would demote **both** stamped pods and discard two drain windows at
once, precisely because nothing said which dataset mattered. A stamp on a pod this cluster's
StatefulSet did not create is not evidence either
([ADR 0020](0020-write-only-what-the-operator-owns.md) D9) — the rule may adopt, so it fails
closed on an unproven pod.

**D6 — A refusal emits no Event, and the resolver still reports nothing.** The level is already
carried: `resolveSplitBrain` writes `MultipleMasters` and emits `SplitBrainDetected` once the
window outlives `splitBrainWarnAfter` = 90 s
([ADR 0025](0025-a-split-brain-warning-means-one-that-did-not-resolve-itself.md)). A per-pass
Event here would rebuild exactly the Warning noise ADR 0025 removed, and the resolver has no
recorder of its own by decision (ADR 0025 D2) — `verifyTopologyRestored` calls it bare.

**D7 — Every direct write of the known master clears the drain stamps.**
`persistManualFailoverState` writes `vko.gtrfc.com/known-master` directly for its conflict
retry and left the stamps in place;
[ADR 0011](0011-evidence-based-steady-state-split-brain-resolution.md) D16 named that class of
site and covered it only at `clearRollingUpdateState`, i.e. at the **end** of the roll. Harmless
while the roll resolver ignored stamps; under D2 a stamp from an earlier drain in the same roll
would outrank the promotion that function just recorded and demote a pod the operator verified
holds data. A failed clear is logged and nothing else, for the reason
[ADR 0011](0011-evidence-based-steady-state-split-brain-resolution.md) D18 gives.

**D8 — A vetoed rogue keeps `isMaster`, and the deadlock that re-enters is accepted.** The
unconditional `isMaster = false` after a demotion exists to break the deadlock in which a rogue
master is skipped by `replaceNextReplica` and blocks `waitForReplicasReady`. A refusal
deliberately re-enters it. **Every state that reaches the resolver with a named authority is
bounded** ([ADR 0010](0010-every-rolling-update-wait-is-bounded.md)), so the refusal is not an
unbounded stall:

| State | Bound | End state |
|---|---|---|
| `stateManualFailover` / `stateReplacingMaster` | `boundManualFailover` | `handlePostManualFailover` abandons |
| `stateRestoringTopology` | `boundTopologyRestore` | `abandonTopologyRestoration`, `TopologyRestored=False` |
| `stateVerifyingTopology` | `finalizationStallTimeout` | completes despite rogue masters, clears state; `checkSteadyStateSplitBrain` then refuses on its own rules |
| Sentinel path | Sentinel reconfigures the returning pod itself | resolved without the operator |

**D9 — The amendments this makes to the two ADRs it touches.**

* [ADR 0008](0008-known-master-annotation-is-the-recorded-authority.md) D10 said the named
  authority decides. ~~It decides unconditionally.~~ It decides **unless** the demotion it
  implies would discard the only dataset (D1), or a drain stamp outranks it (D2).
* [ADR 0011](0011-evidence-based-steady-state-split-brain-resolution.md) D3 justified not
  reusing `detectAndResolveSplitBrain` with *"inside an update the operator knows which pod it
  promoted"*. ~~That premise is complete.~~ It holds for the operator's own promotions and
  **not** for a sidecar drain that lands mid-roll, which is the gap this ADR closes. The
  non-reuse itself stands: D5's argument shows the steady-state rules cannot be ported.

## Consequences

* Two `DBSIZE` reads per rogue on a pass that already found a split brain, and none at all
  otherwise — the resolver returns before D1 whenever at most one pod reports master.
* A cluster the operator cannot reach no longer has its split brain resolved. That is the point
  of D3, and it is a real behaviour change: previously the demotion went ahead blind.
* A refused demotion leaves two masters accepting writes until the bound of the state expires.
  Without write fencing (item T12) both sides accumulate writes, and whichever loses the
  eventual repair loses them.
* Eleven existing unit tests asserted a demotion against an unreachable Valkey, which under D3
  now reads as a refusal. They were moved onto a fake server with an explicit key count, and the
  three that used "which pod was contacted" as a proxy for "which pod was demoted" now assert on
  the command each pod received — the resolver reads a key count from the pod it is protecting,
  so contact no longer implies demotion.
* `createPodForSts` and `podFromStsTemplate` gained the `LabelManagedBy` a real pod always
  carries. Without it every `List(MatchingLabels(SelectorLabels))` matched nothing in a unit
  test, so `clearDrainStamps` looked like a no-op that succeeded.

## Alternatives Considered

* **Treat an empty authority as no authority and fall through to the connected-slaves
  tiebreak.** Rejected, D4: the tiebreak ties at zero in a shrunken cluster and picks the lowest
  ordinal, which is the empty pod in this shape.
* **Port `refuseDemotion` from the steady-state resolver.** Rejected, *Context*: both of its
  branches fire on states the roll produces on purpose, so it would refuse every normal failover
  and every topology restoration.
* **Route the rolling update through `resolveMultiMaster`.** Rejected for the same reason, and
  because [ADR 0011](0011-evidence-based-steady-state-split-brain-resolution.md) D3 decided the
  non-reuse deliberately in the other direction: inside a roll the operator has real authorities
  the steady state does not.
* **The drain stamp alone (D2 without D1).** Rejected: a hard node failure produces no SIGTERM,
  hence no drain and no stamp, and an operator upgrade window has pods whose sidecar never wrote
  one. Those paths would keep losing the dataset exactly as before, and they are the ones nobody
  notices.
* **The dataset veto alone (D1 without D2).** Not wrong, but it repairs nothing: the routine
  chaos-kill mid-roll would end as a split brain a human has to clear, where the steady-state
  path already resolves the same shape by itself.
* **Fail open on an unreadable key count.** Rejected, D3. It keeps the loss window open exactly
  when the operator cannot talk to the pods, which is not an event independent of a cluster in
  trouble.
* **An Event per refusal.** Rejected, D6.

## Residual risks

* **`vko.gtrfc.com/promoted-pod` is not rewritten by a D2 adoption.** That annotation is the
  state machine's own bookkeeping and the switch in `handleMultiReplicaRollingUpdate` is the
  single place its authority role is decided
  ([ADR 0008](0008-known-master-annotation-is-the-recorded-authority.md) D11); rewriting it from
  the resolver would put a second writer on it. After an adoption during `stateManualFailover`
  the two names differ, and a *second* split brain in the same state would find the authority
  naming a pod that is no longer a master, falling through to the tiebreak — where D4 still
  guards the demotion. Not measured; reasoned from the code.
* **D1 compares key counts, not datasets.** A rogue holding one stale key beats an authority
  holding none. The rule is about the total loss the measurement showed, not about which
  dataset is newer, and nothing here can answer that question.
* **`DBSIZE` counts the selected database only.** The operator writes no `SELECT`, so both reads
  see db0 — consistent between the two pods, and blind to keys in another database exactly as
  `verifyPromotionCandidateHoldsData` already is.
* **Not verified:** whether the original `probe` cluster of the measurement had Sentinel enabled.
  It was not recorded and is no longer recoverable, so the per-call-site reachability in the
  ticket rests on reading the code rather than on reproducing that run.
* **The Sentinel path is protected by D1 but not exercised by D2** in the way the non-Sentinel
  path is: Sentinel's live verdict names the data holder, so the veto is expected to stay inert
  there. An unreachable Sentinel returns an empty authority and lands on the tiebreak, which D4
  covers.

## References

* [`internal/controller/rolling_update.go`](../../internal/controller/rolling_update.go) —
  `detectAndResolveSplitBrain`, `stampedMastersAmong`, `adoptStampedMaster`, `demoteRogues`,
  `demotionRefusalReason`, `dbSizeReader`, `persistManualFailoverState`
* [`internal/controller/steady_state_master.go`](../../internal/controller/steady_state_master.go) —
  `hasDrainStamp`, `clearDrainStamps`, the steady-state resolver this one deliberately does not reuse
* [`internal/controller/split_brain_dataset_test.go`](../../internal/controller/split_brain_dataset_test.go)
* [`test/e2e/split_brain_dataset_test.go`](../../test/e2e/split_brain_dataset_test.go)
* [ADR 0007](0007-failover-aware-rolling-update.md) D3, D10 — the fail direction and the promotion-side guard
* [ADR 0008](0008-known-master-annotation-is-the-recorded-authority.md) D1, D8–D11 — the record, the self-claim and the authority
* [ADR 0009](0009-an-unrecorded-promotion-is-not-a-promotion.md) — why the adoption records first
* [ADR 0010](0010-every-rolling-update-wait-is-bounded.md) — the bounds D8 leans on
* [ADR 0011](0011-evidence-based-steady-state-split-brain-resolution.md) D3, D5, D7, D10, D16, D18
* [ADR 0012](0012-the-sidecar-records-its-drain-promotion-on-the-pod.md) — the promotion nobody records
* [ADR 0020](0020-write-only-what-the-operator-owns.md) D9 — the pod provenance the stamp rule needs
* [ADR 0025](0025-a-split-brain-warning-means-one-that-did-not-resolve-itself.md) D2, D5 — why the resolver reports nothing
