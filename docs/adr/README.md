# Architecture Decision Records

Every durable architecture decision of this operator lives here, one file per decision
family. An ADR records **what was decided, why, what was rejected, and what it costs** — so a
later change can argue with the decision instead of rediscovering it.

## Format

Filename: `NNNN-kebab-case-title.md`, numbered in the order they were written.

Sections, in this order:

| Section | Content |
|---|---|
| `# ADR NNNN: Title` | The decision as a title, not a topic |
| `## Status` | `Accepted` / `Superseded by ADR NNNN` / `Amended`, plus `Date:` and what is actually implemented versus open |
| `## Context` | The forces and the concrete failure that made the decision necessary |
| `## Decision` | `D1 … Dn`, each a rule that holds going forward, in present tense |
| `## Consequences` | What this costs, including the parts nobody likes |
| `## Alternatives Considered` | Each option and why it lost |
| `## Residual risks` | Accepted risks, open items, and what was **not** verified |
| `## References` | Relative links to the code and to sibling ADRs |

Ground rules: English only; every claim verified against the code, with unverified statements
marked as such; identifiers (`functions`, `annotations`, constants) quoted exactly so the ADR
stays checkable against the tree. An ADR here may link into the code, and it cites no ticket —
the rule is [ADR 0034](0034-tickets-are-work-lists-that-get-archived.md) D7, D8, and the ticket
citations that predate it are not a precedent.

## Keeping them current

**An ADR is part of the code, not a historical note.** When a decision changes, the ADR is
updated in the same change — the `Decision` section states the new rule, the `Status` records
the amendment with its date, and the superseded rule is marked in place rather than deleted.
A reader must never find the old rule stated as current.

The record's row in the index below changes in the same change as its `Status`: a new record
gets its row, with its *State*, in the change that writes it, and an amendment that moves what is
built, or supersedes a rule, updates the row's *State* with it.

## Index

Every record here is **Accepted**. Rules superseded by a later decision: D9 of ADR 0013 by
ADR 0032, and its D12, D13 and the first half of its D14 by ADR 0036; D7 of ADR 0020 by that
record's own amendment of 2026-08-22. A part of a rule that a later amendment replaced is struck
through in place in its record and not listed here. The *State* column is the coarse build state
as of 2026-09-27, on the uncommitted working tree of the `feat/rootless` branch:
**Implemented**, **Partly built** (some rules of the decision hold, the rest are decided and
outstanding) or **Not built** (decided, nothing of it exists yet). A state that reads
*Implemented, except …* names the decided part that is still outstanding. Accepted risks, the
open gaps `H-<n>` of [docs/security/](../security/README.md) and questions nobody has decided yet
are not build state and are not in this column; the record's `Status` and `Residual risks` carry
them. The record's own `Status` section is the authority; this column is a reading aid.

### Reconciliation and availability

| ADR | Decision | State |
|---|---|---|
| [0001](0001-continue-reconciling-past-a-rejected-write.md) | Continue reconciling past a rejected sub-resource write | Implemented |
| [0002](0002-surface-a-blocked-reconcile-on-the-cr.md) | Surface a blocked reconcile on the CR | Implemented |
| [0003](0003-nudge-a-short-of-pods-statefulset.md) | Nudge a short-of-pods StatefulSet | Implemented |
| [0019](0019-reconcile-concurrency-and-the-cost-of-a-stuck-pass.md) | Reconcile concurrency, and the cost of a stuck pass | Implemented |
| [0027](0027-conditions-are-levels-edges-or-history.md) | Every condition is a level, an edge or history, and a test says which | Implemented |

### Workload guarantees

| ADR | Decision | State |
|---|---|---|
| [0004](0004-opt-in-poddisruptionbudgets.md) | Opt-in PodDisruptionBudgets with a quorum-derived Sentinel budget | Implemented |
| [0005](0005-upgrade-neutral-defaults-and-anti-affinity.md) | Upgrade-neutral defaults, and pod anti-affinity off by default | Implemented |
| [0006](0006-delete-only-what-the-operator-owns.md) | Delete only what the operator can prove it owns | Implemented, except `deleteLegacyServices`, whose delete carries no UID precondition and accepts any ownerReference, not only the controller one |
| [0023](0023-volume-claim-templates-are-immutable.md) | A StatefulSet whose volumeClaimTemplates no longer match the spec is refused, not rewritten | Implemented |

### Data plane and master authority

| ADR | Decision | State |
|---|---|---|
| [0007](0007-failover-aware-rolling-update.md) | Failover-aware rolling update against the persisted template | Implemented |
| [0008](0008-known-master-annotation-is-the-recorded-authority.md) | The known-master annotation is the operator's recorded master authority | Implemented |
| [0009](0009-an-unrecorded-promotion-is-not-a-promotion.md) | A promotion the operator could not record is not a completed promotion | Implemented |
| [0010](0010-every-rolling-update-wait-is-bounded.md) | Every rolling-update wait is bounded and has a named exit | Implemented, except the plain requeues on a master or a promotion step that does not answer, and on a Sentinel pod that is missing |
| [0011](0011-evidence-based-steady-state-split-brain-resolution.md) | Evidence-based steady-state split-brain resolution | Implemented |
| [0012](0012-the-sidecar-records-its-drain-promotion-on-the-pod.md) | The sidecar records its drain promotion on the pod, not on the CR | Implemented |
| [0022](0022-sentinel-identity-is-pinned-to-the-pod.md) | A Sentinel's identity is pinned to its pod, and peer drift is reported rather than reset | Implemented |
| [0024](0024-the-sentinel-tier-reports-its-own-completion.md) | The Sentinel tier reports its own completion — RollingUpdateComplete means the data tier | Implemented |
| [0025](0025-a-split-brain-warning-means-one-that-did-not-resolve-itself.md) | A Warning named split-brain means one that did not resolve itself | Implemented |
| [0026](0026-a-pod-being-deleted-is-not-available.md) | A pod being deleted is not available — readiness answers reachability, never spendability | Implemented |
| [0028](0028-a-demotion-may-not-discard-the-only-dataset.md) | A demotion may not discard the only dataset — the roll resolver gets the drain stamp and a key-count veto | Implemented |
| [0029](0029-a-name-is-not-a-component.md) | A name is not a component — the tier is passed, never parsed | Implemented |
| [0037](0037-the-master-handover-loses-no-acknowledged-write-and-no-dataset.md) | The master handover loses no acknowledged write and no dataset — a coordinated failover, the replica-side predicate and the key-count veto before the delete, the veto at every `REPLICAOF`, a held handover | Implemented |
| [0038](0038-the-operator-does-not-offer-min-replicas-to-write.md) | The operator does not offer `min-replicas-to-write` as a CRD field | Implemented |

### Security and API surface

| ADR | Decision | State |
|---|---|---|
| [0013](0013-operator-is-cluster-wide-privileged.md) | The operator is a cluster-wide privileged component | Implemented; D9 superseded by ADR 0032, D12, D13 and the first half of D14 by ADR 0036 |
| [0014](0014-rbac-lives-in-three-places.md) | RBAC lives in three places, and drift is guarded by a test and a CI job | Implemented |
| [0015](0015-one-crd-validated-by-schema-only.md) | One CRD, validated by schema only — no admission webhook | Implemented |
| [0016](0016-authentication-and-tls-posture.md) | Authentication and TLS posture | Implemented |
| [0020](0020-write-only-what-the-operator-owns.md) | Write only what the operator can prove it owns, and grant only to a subject it owns | Implemented; D7 superseded by the record's own amendment of 2026-08-22 |
| [0030](0030-rotating-certificates-rotate-the-instances-that-cannot-reload-them.md) | Rotating certificates rotate the instances that cannot reload them — ours re-read their material, everything else rides a roll | Implemented |
| [0031](0031-a-record-the-operator-trusts-lives-in-pod-spec.md) | A per-pod record the operator trusts lives in pod spec, not pod metadata — a pod can patch its own metadata | Implemented, except `config-hash` and `pod-spec-hash`, which D6 leaves in pod metadata as a follow-up, not a non-goal |
| [0032](0032-generated-pods-run-rootless.md) | Generated pods run rootless, and existing clusters move with the operator upgrade | Implemented |
| [0033](0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md) | Generated pods take a seccomp profile and an opt-in user namespace, and the operator's own pods carry the same posture | Implemented |

### Process

| ADR | Decision | State |
|---|---|---|
| [0017](0017-test-and-ci-policy.md) | Test, verification and CI policy | Implemented, except the open items at D6 (two conditional skips), D25 (the `require.Eventually` conversion) and D50 (the fixture fix for two vacuous sites) |
| [0018](0018-metrics-and-the-exporter-sidecar.md) | Metrics — an opt-in exporter sidecar, and the operator's own endpoint | Implemented |
| [0021](0021-per-resource-metrics-and-the-alert-that-was-missing.md) | Export per-resource state as metrics, because the only alertable signal could not name a resource | Implemented |
| [0034](0034-tickets-are-work-lists-that-get-archived.md) | Tickets are work lists that get archived, and an open security finding is embargoed | Implemented, except D7: the ticket citations that predate it stay until they are rewritten, by decision |
| [0035](0035-the-readme-advertises-the-reference-lives-under-docs.md) | The README advertises the operator and carries the reference; the explanations live under `docs/` | Implemented, except D3's operations page for `spec.networkPolicy` and a reference for the operator's command-line flags |
| [0036](0036-the-security-architecture-is-one-page-per-perspective.md) | The security architecture is one page per perspective | Implemented |

## Related documents

This directory is one of five homes for durable documentation, and a statement goes to exactly
one of them ([ADR 0035](0035-the-readme-advertises-the-reference-lives-under-docs.md) D2):

| Home | What it holds |
|---|---|
| `docs/adr/` (here) | Decisions: what was decided, why, what was rejected and what it costs |
| [docs/developer/](../developer/README.md) and [DEVELOPER.md](../../DEVELOPER.md) | How the code works, and the contributor workflow |
| [docs/operations/](../operations/README.md) | What somebody running the operator needs |
| [docs/security/](../security/README.md) | The threat model and the gap each mechanism leaves, one page per perspective ([ADR 0036](0036-the-security-architecture-is-one-page-per-perspective.md)); the privilege footprint is [privilege-footprint.md](../security/privilege-footprint.md) ([ADR 0013](0013-operator-is-cluster-wide-privileged.md), [ADR 0014](0014-rbac-lives-in-three-places.md)) |
| [docs/tickets/](../tickets/README.md) | Work still outstanding, archived when it lands ([ADR 0034](0034-tickets-are-work-lists-that-get-archived.md)) |

* [README.md](../../README.md) — the front page, with the complete CRD reference and Helm chart values
* [SECURITY.md](../../SECURITY.md) — how to report a vulnerability
* [CLAUDE.md](../../CLAUDE.md) — project conventions and the ADR obligation
