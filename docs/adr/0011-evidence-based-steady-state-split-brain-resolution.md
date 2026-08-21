# ADR 0011: Evidence-Based Steady-State Split-Brain Resolution

## Status

Accepted. Date: 2026-08-21. Applies to **non-Sentinel** multi-replica clusters.

Supersedes two earlier shapes of the same check, both written and discarded during
development on this branch. **Neither shipped, and neither is reproducible from git:**
`internal/controller/steady_state_master.go` has exactly one commit in the whole history
(`2357946`) and is absent from `origin/main` and from every release tag, so the two shapes
below are development history, not committed artifacts. Each loses data by construction:

1. the annotation as unconditional authority — which demoted the pod a node drain had
   promoted, discarding every write of the drain window;
2. adoption on the `instanceRole=master` label alone — which cannot tell a drain promotion
   from a self-election off a stale mount.

Implemented on branch `feat/support-pdb`, not yet released. Unit-verified in
[`internal/controller/steady_state_master_test.go`](../../internal/controller/steady_state_master_test.go);
**no e2e coverage**.

## Context

`verifyTopologyRestored` can complete and clear the rolling-update state with rogue
masters still live. Afterwards `checkAndHandleRollingUpdate` early-returns,
`detectAndResolveSplitBrain` has no caller outside the rolling update, and
`checkAndRecoverNoMaster` handles only `masterCount == 0`. Without Sentinel the sidecar
labeler labels every self-reported master `instanceRole=master`, and the `-rw` Service
selects on that label — so **writes round-robin across two independent datasets
indefinitely**, with `TopologyRestored` possibly still `True` and only a
`TopologyRestoreIncomplete` Warning as a trace. The bounded escape of
[ADR 0010](0010-every-rolling-update-wait-is-bounded.md) would otherwise convert "stuck
forever" into "finished with a live, undetected split brain".

Resolving that is destructive by nature: **a demotion is a `REPLICAOF`, which discards the
demoted pod's dataset.** So the question is never "who looks like the master" but "who
promoted, and can the operator prove it".

Two facts make that hard:

* **The sidecar promotes without being able to record it.** `internal/sidecar/drain.go`
  promotes a replica on every SIGTERM of a master pod — any node drain or eviction — and
  its Role grants `pods get/list/patch` and no CR access at all
  ([ADR 0012](0012-the-sidecar-records-its-drain-promotion-on-the-pod.md)). So the
  known-master annotation is trustworthy exactly when the operator promoted, and
  untrustworthy exactly when the sidecar did.
* **The data plane cannot tell the two apart.** `master_replid2` and
  `second_repl_offset` looked like the answer on paper: a server sets them when it gives
  up the master role. Measured on a running pair under `persistence.mode: rdb` (the
  default), **both candidates reported byte-identical values for both fields.** That
  measurement was taken on a cluster by the implementing agent and is **not reproducible
  from this repository**; what is checkable here is only that neither field appears in
  `valkeyclient.ReplicationInfo`. The forensic route was abandoned on it and left no trace
  in the code, because it never worked.

## Decision

**D1 — `checkSteadyStateSplitBrain` is the only split-brain check outside a rolling
update.** Wired into `handlePostRollingUpdateChecks` after `checkAndRecoverNoMaster` and
before `updateStatus`, so a pass that changed replication does not publish a status
describing the topology it just replaced.

**D2 — It probes only when at least two pods carry `instanceRole=master`.** Skipped
entirely with Sentinel enabled, for single-replica clusters, and while a rolling-update
state annotation is set. The healthy case costs no connection at all.

**D3 — It never tie-breaks.** `detectAndResolveSplitBrain` is deliberately **not** reused,
so the "most connected slaves" fallback stays unreachable from outside a rolling update.
Inside an update the operator knows which pod it promoted; in steady state it does not,
and that fallback ties at zero in a shrunken cluster and picks the lowest ordinal — the
exact mechanism that destroyed a promoted pod's data (the incident the code comments label
NA21; the loss was observed on a cluster, what is checkable in this repository is the
tie-at-zero fallback itself).

**D4 — The contract on the annotation, sharpened rather than widened:**

> **`vko.gtrfc.com/known-master` is the tie-breaker AMONG MULTIPLE masters. It is never
> used to overrule a single, undisputed master.**

Where exactly one labeled master disagrees with the record, the operator **adopts** (moves
the record) or **refuses**. It never demotes.

**D5 — Adoption requires positive evidence, ranked cheapest first. The label is not
evidence.**

1. **The drain stamp** `vko.gtrfc.com/drain-promoted-at` (`hasDrainStamp`) — free, the
   stamp is already on the listed Pod, and it is written by the promoter itself.
2. **The structural rule** `couldNotHaveSelfElected` — a labeled master with ordinal > 0
   that the **live replica ConfigMap** does not name cannot have elected itself, because
   the init script grants the master config to ordinal 0 on the ordinal fallback and
   otherwise only through the self-claim, which needs the mounted config to name the pod.
   Costs one cache-served read. The **live ConfigMap**, never the CR annotation: the two
   diverge whenever a republish did not land, and it is the ConfigMap the pod actually
   read. An unreadable ConfigMap reports **unknown**, and the two callers read that
   asymmetrically on purpose: `promotionEvidence` requires the read to have succeeded, so
   unknown is no evidence and the adoption is refused, while `resolveMultiMaster` discards
   the bool (`cmMaster, _ :=`), so unknown compares unequal to every pod name, fires
   `couldNotHaveSelfElected` and refuses the demotion. That is D7's direction rule applied
   to a read failure — refuse on ambiguity, never adopt — and both halves are pinned
   (`TestSteadyStateSplitBrain_RefusesToAdoptWhenTheReplicaConfigMapIsMissing`,
   `TestSteadyStateSplitBrain_RefusesToDemoteWhenTheReplicaConfigMapIsMissing`).
3. **The recorded pod yielded** `recordedGaveUpTheRole` — the pod the annotation names
   answers a probe and reports a role other than master. A pod replicating from somewhere
   else has already given up its dataset, so republishing away from it destroys nothing.
   Costs one Valkey connection, and only on a label/annotation disagreement.

Rule 3 exists because rule 2 is blind to exactly the pod a drain promotes most often:
`buildReplicaAddrs` walks ordinals ascending and `findSyncedReplica` takes the first synced
peer, so draining a non-pod-0 master promotes **pod-0** whenever pod-0 is healthy — and
`couldNotHaveSelfElected` can never exonerate pod-0. A non-pod-0 master is not exotic
either: it is the routine output of this design, since every adoption leaves one behind.

**D6 — Silence is not evidence.** Unreachable, absent and still-master all read as **no**
evidence, not weak evidence. "The recorded pod no longer exists" was offered as a third
adoption route and **rejected**: it is the same data-loss shape with the Pod object deleted
instead of rescheduled. A pod that does not answer may be a master that is merely
restarting, and the writes it holds are exactly the ones an adoption would discard.

**D7 — The load-bearing invariant of the whole resolution:**

> **The creation order may only ever REFUSE a demotion. It may never GRANT an adoption.**

"The pod the annotation names is the younger Pod object" is true after a drain — and just
as true after the recorded master's node hard-failed with no SIGTERM (hence no drain and no
stamp) while a peer that could reach nobody took the ordinal fallback and elected itself.
Adopting there republishes the replica ConfigMap toward the self-elected pod, and the real
master full-resyncs its newer dataset away the moment it boots — silently, and caused by
the operator. Refusing on the same signal is safe in the opposite direction: the worst
outcome is two masters a human can see. So `refuseDemotion` uses `recreatedAfter` and
`promotionEvidence` does not.

**Any new signal must be classified by direction before it is wired.** A signal that is
ambiguous about *who promoted* may refuse; it may never adopt.

**D8 — The refusal expires.** `recreatedAfter` compares with `metav1.Time.Before` (strict,
one-second resolution), fails closed on an unreadable or absent pod, and requires the
recreation to be inside `spec.rollingUpdate.syncTimeout` (default 5 m). Unbounded, "B was
created after A" is a **permanent** property of two Pod objects — true after any reschedule,
forever — and the operator would never consolidate that pair again. Reusing `syncTimeout`
means one knob widens the replica replacement, Phase 1 of the topology restoration and this
refusal together for a slow environment.

**D9 — No ordering argument may assume the data StatefulSet recreates pods in ordinal
order.** It runs `PodManagementPolicy: Parallel`, so a co-restart recreates every pod at
once and **ties** the timestamps; `Before` is strict, so D7's rule is **inert** there and
the annotation decides. The `refuseDemotion` comment that claimed ordinal-order recreation
was deleted outright rather than patched — the property it described does not exist. That
comment is development history on this branch and is not in git either: the file has one
commit (`2357946`) and does not carry it.
`TestSteadyStateSplitBrain_RefusesTheMissedDrainShape` carries an equal-age fixture for
that reason, but it does **not** pin the inertness: `refuseDemotion` short-circuits on its
structural branch there (`podOrdinal("test-0") == 0` and `couldNotHaveSelfElected("test-1",
"test-0")`) and never reaches `recreatedAfter`, so the test produces the same refusal
whether the creation-order rule is inert, active or absent. **No test currently pairs tied
creation timestamps with a demotion that must proceed**, so the inertness rests on `Before`
being strict, not on a fixture that would fail if it stopped being true.

**D10 — Ambiguous evidence routes into the refusal, never past it.** In the shape this one
replaced — development history on this branch, never committed: `recordAmbiguousStamps` is
present in the single commit `2357946` from the start — two live stamped masters fell
through to the annotation, which then demoted **both** and discarded two drain windows at
once. That was the most destructive action in the file, taken precisely because nothing said
which dataset mattered. Ambiguity is a reason to stop, not a reason to consult a weaker
signal.

**D11 — The decision table is normative.** `labeled` = pods carrying `instanceRole=master`.
Rows are evaluated in order; the first match wins.

| State | Outcome |
|---|---|
| `len == 0` | return; `checkAndRecoverNoMaster` owns it |
| `len == 1`, annotation names it, **or nothing is recorded** | no-op, **no probe at all**, and no Event — nothing recorded means nothing to contradict (D22) |
| `len == 1`, the labeled pod is unreachable or does not report master | no-op, log line only: a stale label is not a promotion |
| `len == 1`, stamped and confirms master | adopt |
| `len == 1`, ordinal > 0 and the live ConfigMap names someone else | adopt |
| `len == 1`, the recorded pod answers and is **not** master | adopt |
| `len == 1`, none of the three — the recorded pod is unreachable, gone, or still master | refuse, `MasterAdoptionRefused` (no requeue: one master is not a split brain) |
| `len >= 2`, exactly one stamped master confirms | record it, demote the others toward it |
| `len >= 2`, more than one stamped master confirms | refuse, `SplitBrainDemotionRefused` |
| `len >= 2`, annotation names pod-0 and another confirmed master could not have self-elected | refuse, `SplitBrainDemotionRefused` |
| `len >= 2`, annotation names a pod recreated after a confirmed master, inside `syncTimeout` | refuse, `SplitBrainDemotionRefused` |
| `len >= 2`, annotation names a confirmed master | demote the others toward it |
| `len >= 2`, no admissible authority | refuse, `SplitBrainUnresolved` |

**D12 — Demotion refusals requeue; adoption refusals do not.** Both demotion refusals keep
`steadyStateRecheckDelay` (15 s) and never suppress `updateStatus` — the cluster is still
split, so the operator owes it another look. The adoption refusal does not requeue, and
neither does the no-admissible-authority branch: one labeled master is not a split brain
(writes reach exactly one dataset), and no amount of polling fixes a record only a human
can correct. `reportDemotionOutcome` requeues on `unresolved + refused > 0`; a merely stale
role label schedules nothing, because the operator does not repatch labels it does not own.

**D13 — The recheck travels as a non-terminal `ctrl.Result`, applied after
`updateStatus`.** Ending the pass would skip the status write and freeze the CR at its last
verdict — usually `OK` — while the operator loops on a split brain invisibly. Dropping the
requeue would leave the next look to the 10 h cache resync, because the CR watch is
generation-gated and there is no Pod watch. Every other requeue reason wins over the
recheck.

**D14 — 15 s, deliberately above the sidecar's label poll.** The check keys on
`instanceRole=master` and the sidecar is the only writer of that label; a faster recheck
would repeatedly evaluate a label set that has not caught up, producing verdicts about a
transition the sidecar is already resolving. The poll is a **default, not a constant**:
`--poll-interval` / `SIDECAR_POLL_INTERVAL` (`cmd/sidecar/sidecar.go`) sets it and the
Labeler uses whatever it is given, so a deployment that raises it past 15 s inverts this
ordering. Nothing enforces the relation between the two values.

**D15 — The demotion path performs no API writes, so an admission gap cannot stop it.**
Cached List, cached Gets (the replica ConfigMap, the recorded Pod, the TLS and auth
Secrets), Valkey RESP commands and Events only. Two separate mechanisms carry it through a
blocked pass, and they must not be conflated: `reconcileWorkload` runs regardless of
`resourceErr`, so the check is reached at all; and `withBlockedPass`
([ADR 0002](0002-surface-a-blocked-reconcile-on-the-cr.md)) suppresses **only** phase and
message writes — `passIsBlocked` has exactly two consumers, `persistStatus` and
`updatePhase` — so it gates no `Update` or `Patch` of any resource and could not have
suppressed the demotion even if the path did write. Intended either way: a split brain is a
data-plane emergency and an admission gap elsewhere in the cluster must not stop the one
check that can end it. **The adoption path is the single exception** — it writes the CR
annotation and republishes the replica ConfigMap — so the same admission rejection that
blocked the managed-resource writes can also reject those two; that failure is logged, the
next pass retries, and `persistKnownMaster` restores the in-memory value it could not write,
leaving nothing half-applied.

**D16 — The stamp has a lifecycle, and clearing it is correctness, not hygiene.** A stamp
means "a promotion nobody recorded", so once the operator records one the stamp is **spent
evidence** — and evidence beats the annotation on the next pass (rule 1 is evidence-first),
so a leftover stamp would have the operator adopt the stale pod and `REPLICAOF` the master
it had legitimately promoted. `clearDrainStamps` wipes every pod of the cluster at exactly
two sites: `recordPromotedMaster` after the known-master write succeeded, and
`clearRollingUpdateState` after the state annotations were removed. The second is not
redundant — several paths write the known master without going through
`recordPromotedMaster`, and `clearRollingUpdateState` is where they converge
([ADR 0008](0008-known-master-annotation-is-the-recorded-authority.md) D13).

**D17 — The clear sits *below* the early returns of `clearRollingUpdateState`**, so it runs
only when state annotations were actually present and removed. `checkAndHandleRollingUpdate`
calls the function on every pass reporting `Completed`, including passes where nothing was
running, and it runs **before** `checkSteadyStateSplitBrain` in the same reconcile —
clearing unconditionally would delete a fresh drain stamp in the pass before the check that
exists to read it. **The ordering of those two calls inside a reconcile is load-bearing.**

**D18 — A failed stamp clear is logged and nothing else.** At that point the promotion is
already recorded; aborting would trade a *possible* wrong adoption later for a *certainly*
unrecorded promotion now, which is the strictly worse failure.

**D19 — An unparseable stamp reads as "no stamp", and the stamp stays outside
`podNeedsUpdate`.** Garbage must fail closed toward "no evidence", never open toward
"corrupt, therefore fresh". And because the operator now writes an annotation onto running
pods on every drain, an update detector that hashed pod annotations would turn every drain
into a rolling restart — an unbounded restart loop driven by the operator's own
bookkeeping. The second half is currently true **by construction, not by an exclusion
anyone wrote**: `podNeedsUpdate` compares images, resources and exactly two named
annotations (`AnnotationConfigHash`, `AnnotationPodSpecHash`) and never hashes the pod's
annotation map, so `AnnotationDrainPromotedAt` appears nowhere in `rolling_update.go`.
`TestPodNeedsUpdate_IgnoresTheDrainStamp` pins the property against that shape changing.
**Both properties are required of any future annotation-as-evidence.**

**D20 — One selector, one demote path.** The check reuses `common.MasterSelectorLabels` —
the same selector the `-rw` Service uses — plus `knownMasterPodName`, `demoteRogueMaster`,
`podState`, `isPodReady`, `recordEvent` and `getRollingUpdateState`. A second selector could
disagree with the one that actually routes writes, so the operator would consolidate a
different set of masters than clients reach; a second demote path would duplicate the most
destructive operation in the codebase.

**D21 — No Pod watch.** The operator watches its owned objects and Secrets only. Evidence
must therefore be durable enough to be read by a pass that arrives **late**, not only by one
that observes a transient window — which is exactly what the drain stamp provides, at no new
grant and no new watch.

**D22 — Without the annotation the check is inert, by decision.** A cluster whose CR
annotations a GitOps prune stripped keeps two masters with only a `SplitBrainUnresolved`
Warning, and the adoption path does not re-establish a missing annotation either (nothing
recorded means nothing to contradict). Inventing an authority where none was recorded would
be exactly the tie-breaking this design refuses to do, and would pick a dataset to destroy
at random.

## Consequences

* **CR annotations are operational state, not decoration.** Tooling that prunes unmanaged
  annotations disables split-brain resolution for that cluster.
* Two split-brain code paths exist, one per regime, and they must not be merged.
* Any change to the master label or to `demoteRogueMaster` propagates to routing, sidecar
  labelling and both regimes at once — intended.
* New code in the demotion path must not introduce API writes, or D15 is lost silently.
* Every future writer of the known-master annotation must route through one of the two
  clearing sites, or add its own clear.
* Unresolvable split brains persist until a human intervenes. They are visible **only as
  Warning Events** (`SplitBrainUnresolved`, `SplitBrainDemotionRefused`,
  `MasterAdoptionRefused`) — the check writes no status and sets no condition, so with every
  pod ready `updateStatus` keeps reporting `Phase = OK`, `Message = "All replicas are ready"`
  and the recorded pod as `status.masterPod` (`currentMasterPod` deliberately declines to
  pick a winner between two labeled masters). **Monitoring must key on all three Events**;
  the CR status will not show the split brain.
* An unadopted, unrecorded promotion persists until the next natural reconcile. Accepted:
  it is not a data-plane emergency.
* A split brain that appears while a rolling-update state annotation is *stuck* is not
  detected until the state clears — which is why an abandonment must hand over to Phase 2
  rather than clear the state ([ADR 0010](0010-every-rolling-update-wait-is-bounded.md) D4).
* Timeliness of detection depends on other events or the requeue cadence, not on pod
  transitions (D21). The 15 s recheck is the only timely re-examination path.
* `handlePostRollingUpdateChecks` cyclomatic complexity stayed at 7 — the new call is a tail
  `return`, adding no branch — well below the repo limit of 15 (`CYCLO_THRESHOLD ?= 15`,
  `make cyclo`).
* Weakening the evidence requirement re-opens **boot-time** mis-election, not just a stale
  label: the init-script self-claim reads the replica ConfigMap, which is rendered from this
  annotation ([ADR 0008](0008-known-master-annotation-is-the-recorded-authority.md) D8).

## Alternatives Considered

### Keep split-brain detection inside the rolling update only

Rejected: nothing re-detects a second master once the state annotation clears.

### Fix only the Phase-2 timeout exit

Rejected: the steady-state check closes every path, not just that one.

### Reuse `detectAndResolveSplitBrain` with a flag

Rejected: it leaves the connected-slaves tie-break reachable in steady state.

### Replication forensics (`master_replid2`, `second_repl_offset`)

Tried first and abandoned on a measurement: both candidates reported byte-identical values,
so the signal could not separate them at all. Building the resolution on it would have
produced a confident, wrong demotion. **Any future attempt to resolve a split brain by
asking the servers what happened to them must re-measure this first.**

### Adopt on the `instanceRole=master` label alone

The second shape written during development on this branch; never committed, never
released. Rejected: a pod that elected itself off a stale mount answers `role:master` just
as convincingly as one a drain promoted.

### Treat the annotation as absolute truth

The first shape written during development on this branch; never committed, never
released. Rejected: it discards the drain-window dataset every time.

### Adopt on the recorded pod's absence or unreachability

Rejected — indistinguishable from a restarting master, whose writes an adoption would
discard.

### Use creation order symmetrically as a tie-breaker

Rejected: it is the data-loss shape in the granting direction (D7).

### An unbounded creation-order comparison

Rejected: permanently unconsolidatable pod pairs.

### A separate timeout knob for the refusal window

Rejected in favour of reusing `syncTimeout`.

### Fall through to the annotation when the stamp is ambiguous

The shape this file replaced during development on this branch — never committed, never
released — and the most destructive path in it. Rejected.

### Clear the drain stamp only in `recordPromotedMaster`

Rejected: it misses the manual-failover and Sentinel write paths.

### Treat the stamp as permanent metadata

Rejected: it outranks the annotation indefinitely and creates the stale-evidence loss.

### Add a Pod watch to catch the single-master window live

Rejected as unnecessary once the evidence became durable.

### Requeue uniformly on every non-clean outcome

Rejected: pointless polling on the adoption path, and on a stale label the operator has no
fix to poll for.

### End the pass on a refusal, or return an error to force a backoff

Rejected: both skip the status write and freeze the CR at its last verdict.

### Elect a master heuristically when the annotation is missing

Rejected: that is precisely the mechanism that destroyed a promoted pod's data.

### Give the sidecar CR write access

Rejected implicitly, and deliberately: its Role stays `pods get/list/patch`
([ADR 0012](0012-the-sidecar-records-its-drain-promotion-on-the-pod.md),
[ADR 0013](0013-operator-is-cluster-wide-privileged.md)).

## Residual risks

Three are accepted rather than engineered away. Each is the cost of a rule that prevents a
**silent** data loss, and each fails in the **visible** direction — a split brain a human
can see — rather than in the direction where the operator itself discards a dataset.

* **An in-place sandbox recreation within kubelet's ConfigMap refresh window (~1 min)** can
  self-claim off a stale mount and then be adopted by the structural rule. The ~1 min is
  kubelet's upstream default sync period, not a figure measured for this operator.
* **A promoted pod that loses its node before the operator adopts the promotion returns
  without its stamp** — the stamp lives on the Pod object. That degrades to the structural
  rule, the recorded pod's own answer, or a refusal.
* **The creation-order refusal turns a crash-and-self-elect race into a visible split
  brain** instead of resolving it toward the annotation, for the length of
  `spec.rollingUpdate.syncTimeout` — after which the annotation decides again. Widening
  `syncTimeout` for a slow environment widens this window too.

Further, stated plainly rather than glossed:

* **A genuine drain promotion whose recorded predecessor was deleted before the operator
  looked is refused, not adopted.** The cluster keeps one labeled master and a stale record
  until a human corrects it.
* **During an operator-upgrade window, old sidecars write no stamp.** The model then rests
  on the structural rule for a pod-0 master and on the recorded pod's own answer for every
  other one.
* **A cluster in which the stamp clears keep failing** — the `clearDrainStamps` Patches at
  the two sites of D16 — can carry spent evidence into a later multi-master pass, where it
  outranks the annotation and is adopted wrongly (D18). Accepted, because the alternative is
  worse.
* **The forensic measurement was taken by the implementing agent against live pods and was
  not re-run by any later pass.** What is verifiable in-repo is only the absence of those
  fields from `valkeyclient.ReplicationInfo`.
* **No e2e covers this check.**

## References

* [`internal/controller/steady_state_master.go`](../../internal/controller/steady_state_master.go) — `checkSteadyStateSplitBrain`, `adoptUnrecordedPromotion`, `adoptAndConsolidate`, `promotionEvidence`, `hasDrainStamp`, `couldNotHaveSelfElected`, `recordedGaveUpTheRole`, `refuseDemotion`, `recreatedAfter`, `clearDrainStamps`, `reportDemotionOutcome`, `steadyStateRecheckDelay`
* [`internal/common/annotations.go`](../../internal/common/annotations.go) — `AnnotationDrainPromotedAt`
* [`internal/valkeyclient/client.go`](../../internal/valkeyclient/client.go) — `ReplicationInfo` and the six fields it parses
* [ADR 0008](0008-known-master-annotation-is-the-recorded-authority.md) — what the annotation is, and the init-script self-claim that reads it
* [ADR 0009](0009-an-unrecorded-promotion-is-not-a-promotion.md) — why the operator's own promotions are always recorded
* [ADR 0010](0010-every-rolling-update-wait-is-bounded.md) — the escape that makes this check necessary
* [ADR 0012](0012-the-sidecar-records-its-drain-promotion-on-the-pod.md) — the promoter that cannot record, and the stamp it writes instead
