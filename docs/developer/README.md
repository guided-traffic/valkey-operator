# Developer documentation

Overviews for people changing this code. The detail is in the code; what lives here is the
shape of things — how the pieces fit, which invariants hold, and why a design that looks odd
is the way it is.

**What belongs here:** anything a future developer needs before touching a subsystem, that the
code cannot state on its own. A package map. The order a reconcile pass runs in. The fixtures
a test tier gives you. The hard-won knowledge from a defect that was expensive to find.

**What does not:** decisions (those are [ADRs](../adr/README.md)), work lists (those are
[tickets](../tickets/README.md), archived when the work lands), what somebody running the
operator needs (that is [docs/operations/](../operations/), with the CRD reference itself in
[README.md](../../README.md#crd-reference)), and the security design (that is [docs/security/](../security/)).
[ADR 0035](../adr/0035-the-readme-advertises-the-reference-lives-under-docs.md) is the rule that
separates those homes.

A page here **may and should** point at files and functions. That is the point of it. It also
means it goes stale when the tree moves, so whoever moves the tree updates the page in the same
change.

| Page | Read it when |
|---|---|
| [package-map.md](package-map.md) | You are new, or you are looking for where something lives |
| [architecture.md](architecture.md) | You want the picture: which processes run where, and which objects one `Valkey` resource turns into |
| [reconcile-loop.md](reconcile-loop.md) | You touch `Reconcile`, add a reconcile step, change a requeue, or need to know when and in what order a pass writes the status |
| [testing.md](testing.md) | You are adding a test, choosing a tier, or a suite is failing and you need to know what it is for and what it needs |

## What has no page here

The contributor-facing material that is not per-subsystem — repository layout, the build and
test matrix, continuous integration and the release, the extension checklists, the conventions,
the toolchain versions — is [DEVELOPER.md](../../DEVELOPER.md).

Most subsystems are not covered by a page above. That is a gap, not a hidden document; this is
where their material actually is today:

| Subsystem | Where it is |
|---|---|
| The data-tier rolling update: replica replacement, the controlled failover, topology restoration, the bounded waits | [`internal/controller/rolling_update.go`](../../internal/controller/rolling_update.go); the decisions are [ADR 0007](../adr/0007-failover-aware-rolling-update.md), [ADR 0009](../adr/0009-an-unrecorded-promotion-is-not-a-promotion.md), [ADR 0010](../adr/0010-every-rolling-update-wait-is-bounded.md) and [ADR 0026](../adr/0026-a-pod-being-deleted-is-not-available.md) |
| The Sentinel-tier roll and when it counts as complete | the Sentinel half of `rolling_update.go`; [ADR 0024](../adr/0024-the-sentinel-tier-reports-its-own-completion.md) |
| The master authority without Sentinel, and split-brain resolution | [`internal/controller/steady_state_master.go`](../../internal/controller/steady_state_master.go), [`split_brain_report.go`](../../internal/controller/split_brain_report.go); [ADR 0008](../adr/0008-known-master-annotation-is-the-recorded-authority.md), [ADR 0011](../adr/0011-evidence-based-steady-state-split-brain-resolution.md), [ADR 0025](../adr/0025-a-split-brain-warning-means-one-that-did-not-resolve-itself.md), [ADR 0028](../adr/0028-a-demotion-may-not-discard-the-only-dataset.md) |
| The sidecar: role labeler, drain handler, readiness endpoint | [`internal/sidecar/`](../../internal/sidecar/); [ADR 0012](../adr/0012-the-sidecar-records-its-drain-promotion-on-the-pod.md) |
| The generated `valkey.conf`, `sentinel.conf` and the init-container scripts | [`internal/builder/configmap.go`](../../internal/builder/configmap.go), [`statefulset.go`](../../internal/builder/statefulset.go), [`sentinel.go`](../../internal/builder/sentinel.go); the election ranking is [ADR 0008](../adr/0008-known-master-annotation-is-the-recorded-authority.md) D6, the Sentinel identity [ADR 0022](../adr/0022-sentinel-identity-is-pinned-to-the-pod.md), how a script change is tested [ADR 0017](../adr/0017-test-and-ci-policy.md) D19, D20 |
| TLS: certificates, rotation, the material record on the pod | [`internal/controller/tls_material.go`](../../internal/controller/tls_material.go), [`internal/tlsmaterial/`](../../internal/tlsmaterial/); [ADR 0016](../adr/0016-authentication-and-tls-posture.md), [ADR 0030](../adr/0030-rotating-certificates-rotate-the-instances-that-cannot-reload-them.md), [ADR 0031](../adr/0031-a-record-the-operator-trusts-lives-in-pod-spec.md) |
| The pod security posture, the ownership repair and the seccomp allow-list | [`internal/builder/pod_security.go`](../../internal/builder/pod_security.go), [`internal/controller/pod_security_migration.go`](../../internal/controller/pod_security_migration.go), [`pod_hardening.go`](../../internal/controller/pod_hardening.go); [ADR 0032](../adr/0032-generated-pods-run-rootless.md), [ADR 0033](../adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md) |
| Ownership proofs before a write or a delete | [`internal/controller/foreign_object.go`](../../internal/controller/foreign_object.go); [ADR 0020](../adr/0020-write-only-what-the-operator-owns.md), [ADR 0006](../adr/0006-delete-only-what-the-operator-owns.md); the checklist in [DEVELOPER.md](../../DEVELOPER.md#adding-things) |
| Status conditions and their lifecycles | [`internal/controller/condition_registry.go`](../../internal/controller/condition_registry.go); [ADR 0027](../adr/0027-conditions-are-levels-edges-or-history.md), [ADR 0002](../adr/0002-surface-a-blocked-reconcile-on-the-cr.md) |
| PodDisruptionBudgets, anti-affinity, immutable volume claims | [`internal/controller/pdb.go`](../../internal/controller/pdb.go), [`internal/builder/affinity.go`](../../internal/builder/affinity.go), [`internal/controller/volumeclaim_conflict.go`](../../internal/controller/volumeclaim_conflict.go); [ADR 0004](../adr/0004-opt-in-poddisruptionbudgets.md), [ADR 0005](../adr/0005-upgrade-neutral-defaults-and-anti-affinity.md), [ADR 0023](../adr/0023-volume-claim-templates-are-immutable.md) |
| The StatefulSet nudge | [`internal/controller/nudge.go`](../../internal/controller/nudge.go); [ADR 0003](../adr/0003-nudge-a-short-of-pods-statefulset.md) |
| The health checker and how pods are dialled | [`internal/health/checker.go`](../../internal/health/checker.go); the concurrent master probe is [ADR 0019](../adr/0019-reconcile-concurrency-and-the-cost-of-a-stuck-pass.md) D4, D5, the pairing of headless Service and port [ADR 0029](../adr/0029-a-name-is-not-a-component.md) |
| The observer | [`internal/observer/`](../../internal/observer/), [`internal/builder/observer.go`](../../internal/builder/observer.go); no ADR covers it as a whole — its identity is [ADR 0012](../adr/0012-the-sidecar-records-its-drain-promotion-on-the-pod.md) D8 and [ADR 0020](../adr/0020-write-only-what-the-operator-owns.md) D2; what it checks and serves is [docs/operations/observer.md](../operations/observer.md), its fields the [`spec.observer`](../../README.md#specobserver) reference |
| The metrics exporter sidecar and the operator's own metrics | [`internal/builder/statefulset.go`](../../internal/builder/statefulset.go) (`buildExporterContainer`), [`internal/metrics/collector.go`](../../internal/metrics/collector.go); [ADR 0018](../adr/0018-metrics-and-the-exporter-sidecar.md), [ADR 0021](../adr/0021-per-resource-metrics-and-the-alert-that-was-missing.md) |
| The operator's privileges | [ADR 0013](../adr/0013-operator-is-cluster-wide-privileged.md), [ADR 0014](../adr/0014-rbac-lives-in-three-places.md); the rule-by-rule footprint is [docs/security/privilege-footprint.md](../security/privilege-footprint.md) |
