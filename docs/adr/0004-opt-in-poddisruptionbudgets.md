# ADR 0004: Opt-In PodDisruptionBudgets with a Quorum-Derived Sentinel Budget

## Status

Accepted. Date: 2026-08-21.

Implemented on branch `feat/support-pdb` (`604cd91` and its follow-ups). Unreleased:
no tag in this repository contains those commits (`git tag --contains 604cd91` is
empty).

[`test/e2e/pdb_test.go`](../../test/e2e/pdb_test.go) asserts eviction serialization
for data pods and for Sentinel pods, the quorum-derived Sentinel `minAvailable`,
deletion when the feature is switched off, the foreign-budget guard, and the
single-replica skip — verified by reading the file; no run of it is recorded in this
repository. Those assertions call the Eviction API directly; a real `kubectl drain`
is deliberately not exercised (see the file header), so quorum preservation is
verified through the budget a drain would hit, not through a drain. That also makes
them node-count-agnostic: the `multi-node-valkey9` CI leg
([`.github/workflows/release.yml`](../../.github/workflows/release.yml)) re-runs them
under three workers and greps its log for
`--- PASS: TestE2E_PodDisruptionBudget_SerializesEvictions`, which proves the test
ran rather than that a second node was needed — and the outcome of any leg is not
checkable from this repository. Cleanup on scale-down below two replicas is covered by
envtest ([`test/integration/pdb_test.go`](../../test/integration/pdb_test.go)) and
unit tests ([`internal/controller/pdb_test.go`](../../internal/controller/pdb_test.go)),
not by e2e.

One documentation follow-through is **open**: the "while
`spec.podDisruptionBudget.enabled` is true" qualifier on the
`PodDisruptionBudgetNotOwned` Event is present in `README.md` but still missing from
[`api/v1/valkey_types.go`](../../api/v1/valkey_types.go) (the source of truth) and
`deploy/helm/valkey-operator/values.yaml`. The generated CRD copies follow
automatically via `make generate-all` and must never be hand-edited.

## Context

`kubectl get pdb -A` on the cluster hit by the 2026-08-19 incident listed no budget
for either the data or the Sentinel StatefulSet. A single node drain was therefore
allowed to evict all three data pods at once, which is what turned a ~90 s webhook
outage into a total data-plane outage. With a budget the drain would have been
serialized and the gap would have cost one pod, not all three. All of that is an
incident observation, measured on a cluster and not reproducible from this
repository; the in-repo traces are the scenario headers of
[`test/e2e/pdb_test.go`](../../test/e2e/pdb_test.go) and
[`test/e2e/admission_recovery_test.go`](../../test/e2e/admission_recovery_test.go),
which restate the incident rather than measure it.

Two properties of the environment constrain the design:

* The Eviction API refuses eviction outright when **more than one** PDB matches a
  pod. Auto-creating budgets would therefore break exactly the users who already
  manage their own. That refusal is the upstream Kubernetes contract, taken as read:
  no test in this repository puts two budgets over the same pods.
* Sentinel's ability to run a failover is a **majority** property. A drain that takes
  Sentinel below quorum silently disables automatic failover for the whole cluster.

## Decision

**D1 — PDBs are opt-in.** They are created only when
`spec.podDisruptionBudget.enabled` is true (CRD default `false`); an omitted block
means no budgets. Builders in [`internal/builder/pdb.go`](../../internal/builder/pdb.go),
create-or-cleanup in [`internal/controller/pdb.go`](../../internal/controller/pdb.go)
(`reconcilePodDisruptionBudgets`), wired as the `PodDisruptionBudgets` reconcile step
after `Sentinel resources`. This follows the repo-wide rule that new CRD features
default to off ([ADR 0005](0005-upgrade-neutral-defaults-and-anti-affinity.md)).

**D2 — When enabled, both applicable budgets are created; the Sentinel budget is
not optional and not configurable.** "Applicable" carries a second gate beyond D4's
replica minimum: `NeedsSentinelPodDisruptionBudget`
([`api/v1/valkey_types.go`](../../api/v1/valkey_types.go)) requires PDBs enabled
**and** `spec.sentinel.enabled` **and** at least `MinPDBReplicas` Sentinel replicas,
so a CR without Sentinel gets the data budget alone. The Sentinel budget uses
`minAvailable = SentinelQuorumFor(replicas) = floor(replicas/2)+1`, computed by the
operator. Only the data budget's `maxUnavailable` is user-settable (default 1). A
configurable Sentinel budget would let a spec silently break the quorum guarantee
through a field that looks like a tuning knob.

**D3 — One definition of the quorum formula.** `SentinelQuorumFor(replicas)` lives in
[`internal/builder/sentinel.go`](../../internal/builder/sentinel.go) and is called by
both the Sentinel PDB and the Sentinel config generation, which previously had the
expression inline. A copy could diverge from the running `sentinel monitor` quorum on
any future change — invisible until a drain, and with the failure mode of a budget
that permits exactly the eviction which breaks its own election quorum.

**D4 — No budget for a StatefulSet with fewer than `MinPDBReplicas` (2) replicas,
even when enabled.** Evaluated independently for `spec.replicas` and
`spec.sentinel.replicas`. Scaling below 2 deletes an existing budget; scaling back up
recreates it. With one pod both possible budgets are wrong: `maxUnavailable: 1`
permits evicting the only pod (a useless object implying protection it does not give),
`minAvailable: 1` blocks `kubectl drain` forever — stalled node maintenance in exchange
for fake safety, since a single-pod instance is not HA either way.

**D5 — The skip is not enforced as CRD/CEL validation.** A rule like
`replicas > 1 || !pdb.enabled` would reject scaling an existing CR with an enabled
budget down to 1 and force the user into two ordered edits. The operator honours the
spec and warns at runtime instead
([ADR 0015](0015-one-crd-validated-by-schema-only.md)).

**D6 — Create-or-cleanup with owner references.** Same pattern as `reconcileMetrics`:
create/update while enabled and the replica count qualifies, delete when disabled or
when the count drops below 2. Owner references are set on both budgets and
`Owns(&policyv1.PodDisruptionBudget{})` is registered in `SetupWithManager`. A leaked
PDB is a permanent, silent drain block on that namespace, so `kubectl delete valkey`
must remove them.

**D7 — The Sentinel formula stays quorum-derived even when it blocks every drain.**
At `spec.sentinel.replicas: 2`, `minAvailable` equals the replica count and the
Eviction API refuses every voluntary disruption indefinitely. The operator does not
relax it: a `minAvailable` below quorum would trade a stalled drain (visible,
recoverable by a human) for silent loss of HA (invisible, unrecoverable in the moment).
Only runtime visibility was added — `warnIfSentinelBudgetBlocksEveryDrain` records the
Warning Event `SentinelPodDisruptionBudgetBlocksDrains` naming the remedy (an odd
Sentinel count of 3 or more), not just the symptom.

**D8 — Configuration-sanity warnings are evaluated on every applicable pass, never
gated on a write.** Scaling `spec.replicas` 5 → 2 with `maxUnavailable: 2` leaves the
PDB byte-identical, so nothing is written — yet the budget now permits evicting every
data pod at once. A write-gated warning is silent for exactly the change that removes
the protection. Both `warnIfDataBudgetProtectsNothing` and
`warnIfSentinelBudgetBlocksEveryDrain` therefore run per applicable pass.

**D9 — Every misconfiguration warning has two channels: a log line and a Warning
Event on the CR.** `PodDisruptionBudgetTooPermissive`,
`SentinelPodDisruptionBudgetBlocksDrains`, `PodDisruptionBudgetNotOwned`. A log-only
warning is invisible to the person who caused the misconfiguration. General rule this
leaves behind: **an Event-based user-facing signal is only as real as the RBAC that
permits it** — the operator records through `events.k8s.io/v1`, and until commit
`2b4f1a3` added that group both the generated role and the chart ClusterRole granted
core-group `events` only ([ADR 0014](0014-rbac-lives-in-three-places.md)). Two of the
three reasons above existed inside that gap (`PodDisruptionBudgetTooPermissive`,
`SentinelPodDisruptionBudgetBlocksDrains`); `PodDisruptionBudgetNotOwned` arrived
after the grant and was never exposed to it. That ordering is checkable in git
history; that the API server actually rejected the writes and left only the log line
is the commit message's report of a running cluster, not reproducible here.

**D10 — The operator never describes a budget it did not write.**
`reconcilePodDisruptionBudget` returns `(bool, error)` — unnamed in the signature,
read as `applied` at both call sites; `false` means a foreign budget under that name
blocked the write, and both callers gate their content warnings on that verdict.
Returning the verdict keeps the ownership logic in one place instead of re-running
`IsControlledBy` in each caller.

**D11 — The foreign-budget Warning is gated on the feature being enabled, but still
fires when it is enabled and merely inapplicable.** A CR that never set
`spec.podDisruptionBudget` emits no Warning about a same-named user PDB — otherwise
every user who had hand-created a budget under the StatefulSet name, the only
remediation available before this feature, would get a permanent per-pass Warning
stream after upgrading, without having changed anything. That remediation was never
written down here: the first mention of a PodDisruptionBudget in `README.md` arrives
with the feature commit `604cd91`, so it is what users did, not a page they can be
pointed at. The counter-rule matters just as much: with the feature **on** but not applicable
(fewer than two replicas, or Sentinel disabled), the Warning still fires, because that
CR asked for operator-managed budgets, the name is taken, and scaling back up would
otherwise silently produce no budget at all.

**D12 — The operator's own rolling update deletes pods directly, so a PDB never
constrains it.** This is the invariant that makes the feature safe to enable: if the
operator evicted, its rolling update could deadlock against a budget it created and
enabling the feature would wedge upgrades. It also bounds what the feature promises —
it constrains third parties (node drain, cluster autoscaler), never the operator.

**D13 — The PDB informer cache stays unfiltered.** The informer itself is not
optional: `Owns(&policyv1.PodDisruptionBudget{})` in `SetupWithManager`
([`internal/controller/valkey_controller.go`](../../internal/controller/valkey_controller.go))
is registered unconditionally. What D13 decides is that it stays unfiltered —
`managerOptions` in [`cmd/main.go`](../../cmd/main.go) sets no `Cache` field at all.
A label-selector-filtered cache would hide user-created PDBs, which is exactly what
the ownership guard must see in order to refuse to overwrite or delete them
([ADR 0006](0006-delete-only-what-the-operator-owns.md)). Filtering trades a
correctness guarantee for informer memory, so it is not a free optimisation.

## Consequences

* **The incident's blast radius is not prevented on the default path.** A cluster must
  opt in. Together with anti-affinity defaulting to `off`
  ([ADR 0005](0005-upgrade-neutral-defaults-and-anti-affinity.md)) this leaves the
  unconditional nudge ([ADR 0003](0003-nudge-a-short-of-pods-statefulset.md)) as the
  only default-path protection from this work.
* Users who want a different Sentinel disruption policy cannot express it through the
  CR; they must disable the feature and manage their own budgets.
* A user who scales 3 → 1 loses the data budget with only a V(1) log line and the
  ordinary "Deleting PodDisruptionBudget" line as evidence; scaling back up restores it.
* Disabling the feature or scaling below 2 is a destructive operation on the budget
  object, by design.
* The sanity warnings repeat every applicable pass. Bounded on the Event side because
  the recorder aggregates repeats into one series, and on the log side because a
  healthy pass returns `ctrl.Result{}` with no periodic requeue.
* Fleet-scale LIST/WATCH cost for every PDB in the cluster is paid on upgrade even by
  users who never opt in. The informer comes from the unconditional `Owns(...)`
  registration, not from any cache setting; D13 is what leaves it unfiltered.
* Enabling a PDB gives no protection against operator-driven disruption. Any future
  change that moves pod deletion onto the Eviction API must re-derive D12 and would
  need a deadlock analysis first.

## Alternatives Considered

### Create PDBs by default for every multi-replica cluster

Rejected twice over: it produces a double-PDB deadlock in the Eviction API for users
with their own budgets, and it changes the eviction behaviour of every existing cluster
on upgrade.

### Expose `minAvailable` for the Sentinel budget

Rejected: it lets a spec configure away the failover guarantee, silently.

### Ship only the data PDB

Rejected: the Sentinel set is where the quorum lives, and it is the one a drain can
break invisibly.

### Duplicate the `floor(n/2)+1` expression in the PDB builder

Rejected: the PDB's `minAvailable` and the running `sentinel monitor` quorum could then
silently disagree.

### Create the budget anyway for a single-replica set

Rejected: either useless (`maxUnavailable: 1`) or a permanent drain block
(`minAvailable: 1`).

### Enforce the replica minimum as CEL validation

Rejected — see D5.

### Lower `minAvailable` at 2 Sentinel replicas so drains can proceed

Rejected: it sacrifices automatic failover to make node maintenance smoother.

### Keep the create/update gate on the sanity warnings

The branch's first implementation (`604cd91`, replaced by `5d80ec2` and `a173131`
before any release — nothing shipped with it), and what the follow-up review asked for on
the Sentinel side as well. Rejected in both places: the byte-identical-PDB scale-down is
exactly the case a write-gated warning cannot see.

### Warn about a foreign budget only on the transition

Rejected: the collision is a property of the cluster, not of a transition, and a user
who arrives afterwards would see nothing.

### Re-run `IsControlledBy` in each warning caller

Rejected: duplicated ownership logic. The `applied` verdict is returned instead.

### `Cache.ByObject` with a label selector on the operator's own labels

Rejected: it blinds the foreign-budget guard. Any future move to a filtered cache must
first replace that guard with a mechanism that does not depend on seeing foreign objects.

## Residual risks

* **With `spec.sentinel.replicas: 2`, node drains hosting a Sentinel pod stall until
  manual intervention.** Deliberate (D7). Below 2 replicas no Sentinel PDB is created
  at all, so the blocking condition is exactly `replicas: 2`.
* **With the feature enabled, a foreign budget under a name whose StatefulSet does not
  currently exist still warns every pass** (e.g. `enabled: true`, Sentinel disabled, a
  stale `<name>-sentinel` budget). Narrowing further needs a StatefulSet-existence check
  that was not built.
* Single-replica clusters have no drain protection at all — correct, since there is
  nothing to protect, but it is worth knowing before treating "PDB enabled" as a
  fleet-wide guarantee.

## References

* [`internal/builder/pdb.go`](../../internal/builder/pdb.go) — `BuildValkeyPodDisruptionBudget`, `BuildSentinelPodDisruptionBudget`, `PodDisruptionBudgetHasChanged`, `ApplyPodDisruptionBudgetSpec`
* [`internal/builder/sentinel.go`](../../internal/builder/sentinel.go) — `SentinelQuorumFor`
* [`internal/controller/pdb.go`](../../internal/controller/pdb.go) — `reconcilePodDisruptionBudgets`, `warnIfDataBudgetProtectsNothing`, `warnIfSentinelBudgetBlocksEveryDrain`, `warnPodDisruptionBudgetNotOwned`
* [ADR 0005](0005-upgrade-neutral-defaults-and-anti-affinity.md) — the defaults rule this follows
* [ADR 0006](0006-delete-only-what-the-operator-owns.md) — the ownership and UID-precondition rules the cleanup obeys
* [ADR 0007](0007-failover-aware-rolling-update.md) — why the operator's own pod replacement is not subject to a budget
* [ADR 0014](0014-rbac-lives-in-three-places.md) — why an Event-based signal needs its RBAC rule in the same change
