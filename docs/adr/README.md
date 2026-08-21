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
stays checkable against the tree.

## Keeping them current

**An ADR is part of the code, not a historical note.** When a decision changes, the ADR is
updated in the same change — the `Decision` section states the new rule, the `Status` records
the amendment with its date, and the superseded rule is marked in place rather than deleted.
A reader must never find the old rule stated as current.

## Index

### Reconciliation and availability

| ADR | Decision |
|---|---|
| [0001](0001-continue-reconciling-past-a-rejected-write.md) | Continue reconciling past a rejected sub-resource write |
| [0002](0002-surface-a-blocked-reconcile-on-the-cr.md) | Surface a blocked reconcile on the CR |
| [0003](0003-nudge-a-short-of-pods-statefulset.md) | Nudge a short-of-pods StatefulSet |
| [0019](0019-reconcile-concurrency-and-the-cost-of-a-stuck-pass.md) | Reconcile concurrency, and the cost of a stuck pass |

### Workload guarantees

| ADR | Decision |
|---|---|
| [0004](0004-opt-in-poddisruptionbudgets.md) | Opt-in PodDisruptionBudgets with a quorum-derived Sentinel budget |
| [0005](0005-upgrade-neutral-defaults-and-anti-affinity.md) | Upgrade-neutral defaults, and pod anti-affinity off by default |
| [0006](0006-delete-only-what-the-operator-owns.md) | Delete only what the operator can prove it owns |

### Data plane and master authority

| ADR | Decision |
|---|---|
| [0007](0007-failover-aware-rolling-update.md) | Failover-aware rolling update against the persisted template |
| [0008](0008-known-master-annotation-is-the-recorded-authority.md) | The known-master annotation is the operator's recorded master authority |
| [0009](0009-an-unrecorded-promotion-is-not-a-promotion.md) | A promotion the operator could not record is not a completed promotion |
| [0010](0010-every-rolling-update-wait-is-bounded.md) | Every rolling-update wait is bounded and has a named exit |
| [0011](0011-evidence-based-steady-state-split-brain-resolution.md) | Evidence-based steady-state split-brain resolution |
| [0012](0012-the-sidecar-records-its-drain-promotion-on-the-pod.md) | The sidecar records its drain promotion on the pod, not on the CR |

### Security and API surface

| ADR | Decision |
|---|---|
| [0013](0013-operator-is-cluster-wide-privileged.md) | The operator is a cluster-wide privileged component |
| [0014](0014-rbac-lives-in-three-places.md) | RBAC lives in three places, and drift is guarded by a test and a CI job |
| [0015](0015-one-crd-validated-by-schema-only.md) | One CRD, validated by schema only — no admission webhook |
| [0016](0016-authentication-and-tls-posture.md) | Authentication and TLS posture |

### Process

| ADR | Decision |
|---|---|
| [0017](0017-test-and-ci-policy.md) | Test, verification and CI policy |
| [0018](0018-metrics-and-the-exporter-sidecar.md) | Metrics — an opt-in exporter sidecar, and the operator's own endpoint |

## Related documents

* [README.md](../../README.md) — user-facing reference
* [SECURITY_ARCHITECTURE.md](../../SECURITY_ARCHITECTURE.md) — the privilege footprint and hardening checklist ([ADR 0013](0013-operator-is-cluster-wide-privileged.md), [ADR 0014](0014-rbac-lives-in-three-places.md), [ADR 0016](0016-authentication-and-tls-posture.md))
* [CLAUDE.md](../../CLAUDE.md) — project conventions and the ADR obligation
