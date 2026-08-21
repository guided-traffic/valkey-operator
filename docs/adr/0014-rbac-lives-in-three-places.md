# ADR 0014: RBAC Lives in Three Places, and Drift Is Guarded by a Test and a CI Job

## Status

Accepted. Date: 2026-08-21.

Implemented. `TestHelmClusterRoleCoversGeneratedRole`
([`internal/controller/rbac_drift_test.go`](../../internal/controller/rbac_drift_test.go))
found a live drift on its first run — **reported from a local run, not reproducible from this
repository's history**: commit `9e5634d` added the test and the missing chart rule in one
change, so no committed state ever had the test facing the broken chart. The drift itself is in
the tree: at `v1.10.48` the generated role granted `delete` on core `secrets` while the chart
ClusterRole granted only `get`, `list`, `watch`. The `generated-manifests` CI job is wired into
`.github/workflows/release.yml` and into `semantic-release`'s `needs:`, and **has run green
on a runner**: Actions run 32451432385, job `Generated Manifests Up To Date`, 2026-08-21, on
PR #184 at `9294ad9` — that record is held by GitHub Actions, not by this repository. Only its
passing path has executed; see Residual risks.

## Context

The operator's permission set exists three times:

1. the **kubebuilder markers** in
   [`internal/controller/valkey_controller.go`](../../internal/controller/valkey_controller.go);
2. the **generated** `config/rbac/role.yaml`, produced by `make manifests`;
3. the **hand-maintained** `deploy/helm/valkey-operator/templates/clusterrole.yaml` — which
   is the ClusterRole that actually reaches users.

Only convention kept them aligned, and **RBAC shipped broken twice — once as a missing
marker, once as chart drift**:

* The `events.k8s.io` marker did not exist in the Go source at all, so it was absent from the
  generated role and from the chart alike: all three places agreed, and all three were wrong.
  `recordEvent` goes through a `k8s.io/client-go/tools/events` recorder, which writes
  `events.k8s.io/v1`, so **every** Event the operator recorded was denied — 47 occurrences of
  `Server rejected event (will not retry!)` with `events.k8s.io is forbidden` in one captured
  log, and `kubectl describe valkey` showed no operator events at all. **Both are cluster
  observations**: the log is not in this repository, and neither the count nor the `describe`
  output is re-verifiable from it. What the tree carries is the shape of the fix — `2b4f1a3`
  adds the marker and the chart rule in one change, and every tag up to `v1.10.48` grants
  core-group `events` only. The feature ran blind and nothing failed loudly, which is why it
  went unnoticed. **Neither guard below would have caught this one**: generated ⊆ chart held,
  so D2 passes, and the markers matched the manifest, so D5 passes. What caught it was a
  cluster.
* A missing `delete` on core `secrets` — carried by the marker and by the generated role,
  absent from the chart — was genuine drift between places 2 and 3, and it is the failure D2
  does catch. It wedged every **cert-manager-backed** cluster migrating to
  `spec.tls.unifiedCertificate` that still had the legacy `<name>-sentinel-tls` Secret:
  `reconcileLegacySentinelCertificateCleanup` returns immediately unless both
  `IsCertManagerEnabled()` and `IsUnifiedCertificateEnabled()` hold, so an install with a
  user-provided `spec.tls.secretName` — where the flag is informational — never reached the
  `Delete`. The `Delete` is reached only through a GET — `deleteLegacySentinelSecret` returns
  `nil` on `NotFound`, and the code released as v1.10.48 already did — so the wedge required the
  legacy Secret to still exist; where it did, the 403 came back on every pass: permanent
  `ReconcileBlocked`, error phase, endless requeue. **The apiserver evaluates authorization
  before existence** is the reason that GET is there, not the mechanism of the wedge: without
  it, a missing verb would 403 even for an object that is already gone.

A third failure mode is worse still: a marker whose rule backs an **informer**. Against a
ClusterRole lacking it, the informer's initial LIST 403s, the cache never syncs, `mgr.Start`
returns the error and the process exits — CrashLoopBackOff, with **all** reconciliation
stopped for every user, opted in or not. That chain is derived from client-go and
controller-runtime behaviour and was not reproduced on a cluster (see Residual risks).

## Decision

**D1 — A new marker requires the chart rule in the same change.** Together with the CRD
schema sync and, where the field is user-visible, a `values.yaml` note. `make generate-all`
runs in the same change.

**D2 — Parity is enforced by a test, not by review discipline.**
`TestHelmClusterRoleCoversGeneratedRole` expands both manifests into `(group, resource, verb)`
triples and asserts **generated ⊆ chart**, naming the missing triple on failure. The guard
lives in `internal/controller` because the markers it defends do.

**D3 — Containment, not equality.** Chart-only extras stay legal — `coordination.k8s.io/leases`
exists only in the chart, because leader election (`--leader-elect`, off unless
`leaderElection.enabled`) is a deployment concern with no kubebuilder marker behind it.
Asserting equality would either force a fake marker or forbid the chart from expressing
deployment-only needs, while catching nothing extra: the real failure mode is the chart
**missing** a rule the code needs.

**D4 — The drift guard fails loudly and never skips silently.** Concretely:

* only the top-level `rules:` block is fed to the YAML parser, because the chart metadata
  contains Go-template actions;
* `require.Falsef(strings.Contains(block, "{{"))` turns any future template action **inside**
  that block into a failure rather than a skip — provoked, not assumed: injecting
  `{{- if .Values.leaderElection.enabled }}` into the block produced the expected failure. That
  provocation was run locally and is not committed as a test, so it is reproducible only by
  repeating it;
* rules restricted by `resourceNames` or `nonResourceURLs` are excluded from the chart's
  covered set and rejected outright on the generated side, because counting them as coverage
  would hide a real gap;
* wildcards are not expanded, documented in-file, so a `*` causes a loud false failure and
  never a silent pass;
* `repoRoot()` uses `runtime.Caller(0)` and asserts `go.mod` is present, so the test ignores
  `go test`'s working directory.

> A drift guard that silently stops checking is worse than none.

**D5 — CI proves the generated manifests are current.** The `generated-manifests` job runs
`make generate-all` on every push and pull request to `main` and fails when `git diff` is
non-empty **or** `git status --porcelain` reports an untracked file — the untracked check
exists because a new API type produces a new CRD file that `git diff` alone would miss. It is
in `semantic-release`'s `needs:`, so drift blocks the release rather than only reddening the
run.

**D6 — The two guards cover different halves, and both are needed.** D2 compares **manifest
against manifest**, so a stale `config/rbac/role.yaml` would pass it; only `make generate-all`
makes the **marker-to-manifest** comparison. The pre-existing check lived in a job that
triggers on `release: published` only, so drift was caught after review, at release time.
Both halves start from the markers, so **a permission no marker ever expressed falls outside
both**: in NA12 all three places agreed and all three were wrong, and neither guard has
anything to compare against. That class is caught only on a cluster, which is why the NA12 fix
landed with two e2e assertions instead of a manifest check
([`test/e2e/admission_recovery_test.go`](../../test/e2e/admission_recovery_test.go), lines
278-330): the `StatefulSetNudged` Event must appear on the CR, and the operator log must stay
free of `Server rejected event`.

**D7 — Both events API groups are granted, and that duplication is intentional.** The
operator records through `events.k8s.io/v1` while older API servers and tooling still read the
core group. Both markers and both chart rules must be kept.

**D8 — There is exactly one supported upgrade path.**
`helm upgrade valkey-operator deploy/helm/valkey-operator --namespace valkey-operator-system`,
which applies the CRD and the RBAC together because both live in the chart's `templates/`.
**Updating the operator image on its own is explicitly not a supported upgrade path.** No
per-path warnings, no "apply RBAC before the image" special-casing, and no fail-fast
preflight in code: the reflector already logs the 403 repeatedly and the fatal error names
the unsynced Kind, so the cause is already printed. A `SelfSubjectAccessReview` preflight
would only sharpen the last line, and minimum code wins. **A crashlooping operator that
states its cause on the console is the accepted failure mode for an upgrade performed outside
the documented path.**

**D9 — The CRD ships inside `templates/` with no `helm.sh/resource-policy: keep`.** Keeping
it in `templates/` rather than `crds/` is what makes `helm upgrade` update the CRD at all,
which is the premise of D8. The consequence — `helm uninstall` deletes the CRD and with it
every `Valkey` CR — is **documented with the safe uninstall order** rather than changed.

**D10 — Never hand-edit a generated manifest.** `config/crd/bases/vko.gtrfc.com_valkeys.yaml`
and `deploy/helm/valkey-operator/templates/crd.yaml` come from the `api/v1` type doc comments;
a documentation or schema correction is made once in the Go types and propagated with
`make generate-all`. **The chart ClusterRole is the named exception** — hand-maintained, not
generated — and naming it keeps the exception from being generalised.

**D11 — The privilege footprint is documented in `SECURITY_ARCHITECTURE.md` and updated in
the same change** as any marker, chart ClusterRole or `BuildSidecarRole` edit. RBAC is the
operator's blast radius, and a permission added without a written justification is a
permission nobody can later argue for removing — the moment of the code change is the only
point at which the rationale is still known.

**D12 — Test-only imports must be direct `go.mod` requires.** The drift guard parses YAML
with `k8s.io/apimachinery/pkg/util/yaml`, deliberately not `sigs.k8s.io/yaml`, which is only
an indirect dependency today: importing it from repo code would have a future `go mod tidy`
reclassify it and produce an unowned `go.mod` diff.

## Consequences

* Every RBAC change now carries **three** obligations: the marker, the chart rule, and the
  `SECURITY_ARCHITECTURE.md` entry. The last has no automated drift check and rests on review.
* Every marker, API type or CRD change requires running `make generate-all` and committing the
  result in the same change.
* Legitimate future use of Go templating or wildcards inside the chart's `rules:` block will
  break the drift test and force a deliberate decision about the guard — intended.
* The drift guard reads only the first top-level `rules:` block per file; the hook role in
  `pre-upgrade-rbac.yaml` has its own ServiceAccount and is deliberately out of scope.
* An admin who deviates from the canonical upgrade path loses **all** reconciliation, not just
  the affected feature, until RBAC is applied.
* A `helm uninstall` destroys all Valkey CRs. PVCs survive and reattach because no
  `PersistentVolumeClaimRetentionPolicy` is set on the StatefulSets — documented as the
  mitigation.
* Fixing a CRD field doc costs a production-Go edit plus a regeneration, which is why
  documentation-scoped passes leave such sites open rather than patching the rendered file.
* Test code is constrained to the apimachinery YAML API surface; the rule generalises to any
  new test-only import.

## Alternatives Considered

### Rely on `make manifests` alone

Rejected: the chart ClusterRole is hand-maintained and is not generated, so nothing regenerates
it.

### Generate the chart ClusterRole from the markers

Rejected: the chart legitimately carries rules the markers do not generate, leader-election
leases among them.

### Strict equality between the generated role and the chart ClusterRole

Rejected: it would outlaw the leases rule, and it catches nothing the containment check misses.

### Rely on review discipline

Rejected empirically — it failed twice, and in one of those cases the delete site *already*
carried a comment invoking the 403-before-existence rule to justify its GET-first guard, while
the verb that guard still needed was missing from the chart on the install path that matters.
That is the strongest argument for a test over a comment.

### A `make lint` step instead of a unit test

Sketched; a unit test was chosen so the failure names the missing triple.

### Skip unparseable input, or expand wildcards, in the drift guard

Both rejected: they convert an unverifiable state into a green run.

### Replace the core-group events rule with `events.k8s.io`, or switch to the legacy recorder

Rejected: older API servers and tooling still read the core group.

### A release-note or README warning about image-only upgrades

Proposed, then dropped in favour of D8 — one documented path, deviations are the admin's
responsibility.

### A fail-fast `SelfSubjectAccessReview` before `mgr.Start`

Deliberately not added: the cause is already printed by the reflector and the fatal error.

### Missing-informer tolerance or cache options, so one feature degrades instead of the operator

Not adopted.

### `helm.sh/resource-policy: keep` on the CRD

Not taken; the uninstall risk is documented instead, because moving the CRD out of
`templates/` would break the canonical upgrade path.

### Rely on the existing `build.yml` generated-manifests step

Rejected — wrong trigger (`release: published`), so drift was caught only at release time.
Moving it was also rejected: `build.yml` keeps its own copy for the release path.

### Regenerate in CI and commit automatically

Not chosen.

### Parse with `sigs.k8s.io/yaml`

Rejected for the `go.mod`-ownership reason in D12.

## Residual risks

* **Only the green path of the `generated-manifests` job has run on a runner.** It completed
  `success` in Actions run 32451432385 on 2026-08-21 — a run record held by GitHub Actions, not
  by this repository — and `make generate-all` leaves the tree byte-identical when run locally,
  which is an observation of a run rather than a committed artifact. A deliberately stale
  manifest has never been pushed, so the job's *failing* path — the half that gives it its
  value — is not reproduced.
* **The `SECURITY_ARCHITECTURE.md` obligation has no automated check.**
* **cert-manager-backed installs that migrated to `unifiedCertificate` before the
  `secrets: delete` fix keep a wedged CR and an orphaned `<name>-sentinel-tls` Secret** until
  a `helm upgrade` grants the verb. The wedge itself is announced — `reconcileTLSCertificates`
  is a step of `reconcileResources`, whose error sets `ReconcileBlocked=True` with reason
  `WriteFailed`, the truncated 403 as the message, and the error phase — but nothing names the
  orphaned Secret or points at the `helm upgrade` as the remedy.
* **The crashloop console output of D8 was never reproduced on a cluster** — it is an
  expectation derived from standard client-go / controller-runtime behaviour.

## References

* [`internal/controller/rbac_drift_test.go`](../../internal/controller/rbac_drift_test.go) — `TestHelmClusterRoleCoversGeneratedRole`, `repoRoot()`
* [`internal/controller/valkey_controller.go`](../../internal/controller/valkey_controller.go) — the kubebuilder RBAC markers
* [`deploy/helm/valkey-operator/templates/clusterrole.yaml`](../../deploy/helm/valkey-operator/templates/clusterrole.yaml) — the ClusterRole that ships
* `.github/workflows/release.yml` — the `generated-manifests` job
* [SECURITY_ARCHITECTURE.md](../../SECURITY_ARCHITECTURE.md) — the footprint that must move with the rules
* [ADR 0013](0013-operator-is-cluster-wide-privileged.md) — what those rules actually permit
* [ADR 0006](0006-delete-only-what-the-operator-owns.md) — why a destructive verb ships with a call-site guard
* [ADR 0017](0017-test-and-ci-policy.md) — the surrounding CI and verification policy
