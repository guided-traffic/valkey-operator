---
id: T45
title: release pipeline, image pins and renovate coverage - the CI Kubernetes and cert-manager pins, the default exporter image and the operator image digest the release does not stamp
state: analysed       # every decision costed, one option recommended each
severity: low         # nothing shipped is exploitable today; medium if the fleet runs a Kubernetes minor newer than 1.33
security: hardening
threat: "whoever can re-push the operator's version tag on Docker Hub without controlling the GitHub repository decides the image that default installs run for the operator, its pre-upgrade hook and every sidecar and observer, and the default exporter, third-party code holding the cluster password (auth on) and the Valkey server key (TLS on), ages without the redis_exporter and Go fixes after v1.66.0 until someone moves the pin by hand"
urgency: now          # rule 1: false statements in tracked files (CI pins, test images, the ADR 0033 D5 title)
effort: M             # three S packages sharing one renovate.json edit and one ADR 0033 D5 edit
blocked-by: decision  # Q1-Q7; the independent changes are not blocked
filed-from: the documentation restructure (DEVELOPER.md toolchain table)
opened: 2026-09-27
decided:
done:
---

# T45 - release pipeline, image pins and renovate coverage

**Scope.** What the release ships and what CI measures against is pinned in several places; some
pins are moved by nobody, some by a manager that never runs, some in a way that cuts a release for
nothing shipped, and the operator image the chart names is not pinned at all. All parts meet in one
`renovate.json` edit, one ADR 0033 D5 edit and one ADR 0017 decision.

- **CI environment and commit types** - Kubernetes, kubectl, cert-manager and envtest pins, the
  Valkey test images, a dead release template, a doubled config load, CI-only bumps that release.
- **Default exporter image** - `DefaultMetricsExporterImage` has no manager and never moved.
- **Operator image digest** - the release does not stamp the digest it pushes into the chart.

## Current state

**Shared.** Renovate runs self-hosted ([`renovate.yml:42-51`](../../.github/workflows/renovate.yml),
44.115.10; Dependency Dashboard issue #229). `renovate.json:225-236` automerges minor, patch and
digest updates of every `custom.regex` manager, so a new custom manager inherits automerge; majors
are manual (`:237-250`); `minimumReleaseAge` is set nowhere. `:274-283` makes custom-manager commits
`fix(deps)`, only github-actions is `chore` (`:284-290`). Every `fix` is a patch release, and under
[ADR 0005](../adr/0005-upgrade-neutral-defaults-and-anti-affinity.md) D11 a release rolls every
multi-replica data StatefulSet on the chart-default path (not on kustomize or a floating Helm
`image.tag`). [ADR 0033](../adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md)
D5 is titled "Images can be, and by default are, pinned by digest" (`:262`); its body pins only the
exporter by default, and its residual risks (`:645-648`) say the chart default is empty and the
exporter pin is maintained by hand.

### CI environment and commit types

The Kind and Helm `version:` inputs are moved by the built-in github-actions manager and are fine.

- **Kubernetes.** `KUBERNETES_VERSION: '1.33.4'` ([`release.yml:23`](../../.github/workflows/release.yml))
  selects every E2E node image (`:158`); the github-actions manager skips it as `contains-variable`.
  1.33 is end of life; Kind `v0.33.0` builds `v1.34.11` to `v1.37.0` (default) and asks for the
  `@sha256` digest. CI is green on the pair anyway.
- **kubectl.** The `azure/setup-kubectl` step (`release.yml:148-151`) is shadowed:
  `helm/kind-action@v1.15.0` (`:155`) puts kubectl `v1.37.0` first on `PATH`, and the e2e tests shell
  out to it ([`e2e_test.go:252`](../../test/e2e/e2e_test.go)), four minors from the server where the
  skew policy supports one. Nothing before the Kind step uses kubectl. kind-action caches kubectl
  keyed on the Kind version only.
- **cert-manager.** `v1.17.2` from a release URL at [`Makefile:200`](../../Makefile) and
  `release.yml:331`; no manager matches. 1.17 is end of life; supported are 1.21 (Kubernetes
  1.33-1.36) and 1.20 (1.32-1.35). No released cert-manager declares 1.37, the tag a manager proposes
  first (Renovate offers only the newest minor unless `separateMultipleMinor` is set).
- **ENVTEST_VERSION.** [`Makefile:49-50`](../../Makefile) has a `# renovate:` comment above
  `ENVTEST_VERSION ?= release-0.19`, a branch; the Makefile regex (`renovate.json:300`) captures only
  `v[\d.]+` and never matches. setup-envtest is tagged at every controller-runtime release since
  `v0.24.0`; [`go.mod:16`](../../go.mod) requires `v0.25.1` (no `replace`). It feeds the required
  `Unit Tests` and `Integration Tests (envtest)` jobs. `ENVTEST_K8S_VERSION = 1.29.0` (`Makefile:5`,
  the declared floor) and `E2E_UPGRADE_FROM` stay hand-pinned, out of scope.
- **Dead release template.** [`.github/release-template.hbs`](../../.github/release-template.hbs) is
  loaded by nothing ([`.releaserc.json`](../../.releaserc.json) has no `writerOpts`); only
  `renovate.json:29`, its manager (`:331-344`) and [`DEVELOPER.md:90`](../../DEVELOPER.md), `:265`
  name it. Its `go-1.26` badge never moves (Renovate's semver rejects `1.26`).
- **Valkey test images.** The manager at `renovate.json:358-368` targets
  [`test/testimages/images.go`](../../test/testimages/images.go) (9.1.1, 8.1.9) and never extracts:
  `config:recommended` includes `:ignoreModulesAndTests`, whose `ignorePaths` contain `**/test/**`.
  9.1.2 and 8.1.10 exist, no PR proposes them. The opposite is stated in ADR 0017 `:31` and D43
  (`:729`), `DEVELOPER.md:270`, [`testing.md:116-117`](../developer/testing.md), `images.go:31` and
  CLAUDE.md. `ignorePaths` is not mergeable; no other file under `test/` matches a built-in manager.
- **Double load.** `renovate.yml:46` passes `configurationFile: renovate.json` as global config and
  `RENOVATE_REQUIRE_CONFIG` (`:51`) loads it again, so every custom manager runs twice (the dashboard
  lists each regex file twice). No key in it is global-only.
- **CI-only bumps release.** A golangci-lint bump alone cut `v1.12.1`. golangci-lint, gocyclo, gosec,
  govulncheck and kustomize never ship; controller-gen stamps the shipped CRD
  ([`crd.yaml:8`](../../deploy/helm/valkey-operator/templates/crd.yaml)); npm covers release tooling.

**Impact:** every E2E result is measured on an end-of-life Kubernetes and cert-manager, with a
kubectl outside the skew policy, on test images the docs wrongly call maintained; CI-only bumps cut
releases that roll the fleet for nothing shipped.

### Default exporter image

Operator-facing statement: gap [H-17](../security/workload-pod-posture.md#h-17).

- `DefaultMetricsExporterImage` is `oliver006/redis_exporter:v1.66.0@sha256:d98e6db8…`
  ([`valkey_types.go:645`](../../api/v1/valkey_types.go), doc comment `:640-644`); `MetricsImage()`
  (`:1211-1217`) falls back to it when `spec.metrics.image` is empty. The Docker Hub digest of
  `v1.66.0` equals the pin. It has never moved; upstream is at v1.92.0, 30 releases, none naming a
  CVE; they bump Go (1.24 to 1.27) and `x/crypto`, and v1.90.0 changes the metric set (#1168, #1170,
  #1172). The pinned arm64 binary is built with `go1.23.2`, out of support; go1.23.5 to .12 carry
  security fixes it predates.
- The exporter holds the password as `REDIS_PASSWORD` and, under TLS, uses the data tier's `tls.key`
  (the Valkey server key) as its client key
  ([`statefulset.go:1076-1107`](../../internal/builder/statefulset.go)).
- No manager reads the file (the custom managers match other files, no built-in manager matches Go
  source); none of the files carrying the reference matches an `ignorePaths` glob. A new custom
  manager would automerge v1.66.0 to v1.92.0 (a minor) as a `fix` release, the right type for a
  shipped image. One dependency matched in several files becomes one PR.
- CI only proves the exporter starts: metrics are enabled in `pod_security_test.go:134` and
  `pod_hardening_test.go:216`, no test reads `/metrics`. With a wrong password the exporter stays up
  and serves `redis_up 0` (measured on `valkey/valkey:9.1.1`). Trivy scans only the operator image;
  the shipped PrometheusRule uses only `vko_*` series.
  `TestDefaultMetricsExporterImage_IsPinnedByDigest` ([`valkey_types_test.go:1367`](../../api/v1/valkey_types_test.go))
  checks only the shape; a bump keeps every test green.
- A bump changes the pod-spec hash. On the chart-default path it adds no roll, and a rootless
  `spec.replicas: 1` pod without Sentinel defers it with the sidecar (`isSidecarOnlyChange`,
  [`rolling_update.go:3845-3866`](../../internal/controller/rolling_update.go); `singlePodDeferral`,
  [`pod_security_migration.go:134-139`](../../internal/controller/pod_security_migration.go)). On
  kustomize or a floating `image.tag` it is a roll of its own and such a pod is deleted
  (`rolling_update.go:3816`), without persistence with its data - the data-loss class of
  [ADR 0007](../adr/0007-failover-aware-rolling-update.md) D7, recurring with every bump.
- The literal reference is copied in [`README.md:368`](../../README.md) (CRD reference row, "with its
  default" per ADR 0035 D3), [`CLAUDE.md:133`](../../CLAUDE.md) (explicit `image:` in the example CR)
  and [`examples.md:235`](../operations/examples.md) (commented). Five places say the pin is kept by
  hand (`README.md:368`, `DEVELOPER.md:271`, `CLAUDE.md:1001-1002`, ADR 0033 `:647-648`,
  `workload-pod-posture.md:211-213`); three name the version alone (ADR 0033 `:266`,
  [`workload-pod-posture.md:96-97`](../security/workload-pod-posture.md), `:212`). All true today.

**Impact:** every metrics cluster with an empty `spec.metrics.image` runs an aging third-party binary
holding the password and the server key, serving `/metrics` over a go1.23.2 `net/http`. Dormant: no
reachable vulnerability, no hostile principal identified. The first bump grows. A user can pin a
newer exporter with `spec.metrics.image` today.

### Operator image digest

Operator-facing statement: gap [H-12](../security/operator-pod-posture.md#h-12).

- [`build.yml`](../../.github/workflows/build.yml) runs on a published release; its `build` job pushes
  in step `id: build` (`:76-97`, `linux/amd64`, provenance and SBOM) and declares no job `outputs:`.
  `release-helm-gh` stamps `version`, `appVersion` and `image.tag` with `sed` (`:179-182`, after the
  dirty-tree check at 162-170); `image.digest` stays `""`
  ([`values.yaml:14`](../../deploy/helm/valkey-operator/values.yaml)), as in the published v1.13.1 chart.
- `steps.build.outputs.digest` is the image index Docker Hub serves under the version tag (v1.13.1:
  `sha256:6a6b1d7d…`, amd64 plus an attestation manifest); `build.yml:97` adds only an annotation.
  Every build has its own digest (`BUILD_TIME`, [`Containerfile:31`](../../Containerfile)). A release
  also moves `1`, `1.13`, `latest` and `sha-<commit>` to the same index.
- [`_helpers.tpl:68-77`](../../deploy/helm/valkey-operator/templates/_helpers.tpl) appends a non-empty
  `image.digest` to whatever repository and tag are set, for the operator, the hook, `--operator-image`
  and `OPERATOR_IMAGE` ([`deployment.yaml:34, 39, 49`](../../deploy/helm/valkey-operator/templates/deployment.yaml),
  [`pre-upgrade-job.yaml:34`](../../deploy/helm/valkey-operator/templates/pre-upgrade-job.yaml)).
  containerd and CRI-O pull `repository:X@D` by `D` alone, so a digest stamped into `image.digest`
  itself would outlive an installer's tag or repository override.
- `--operator-image` feeds the sidecar ([`statefulset.go:989-996`](../../internal/builder/statefulset.go))
  and the observer ([`observer.go:82`](../../internal/builder/observer.go)), not Sentinel pods; a
  change rolls the data tiers at no extra cost in the upgrade that already moves the tag (a rootless
  single standalone pod defers it under `SidecarUpdatePending`, ADR 0007 D6). Both hold the Valkey
  password when auth is enabled (`statefulset.go:946-957`, `observer.go:251-257`) and pull with
  `IfNotPresent`.
- `helm upgrade --reuse-values` (Helm v3.21.3) carries old values, stamped ones included, into the
  new templates; Flux resets values unless `preserveValues` is set. Both production fleets use the
  default image; wds18 follows chart `version: "*"` (`interval: 1m`), stutor pins `1.10.29`.
- The fleet-upgrade e2e installs its start release from the published chart without an image
  override ([`fleet_upgrade_test.go:226-234`](../../test/e2e/fleet_upgrade_test.go)), locally only; a
  stamp reaches it once `E2E_UPGRADE_FROM` (`Makefile:167`) names a stamped release.

**Impact.** Whoever can push tags to `docker.io/guidedtraffic/valkey-operator` (secret
`DOCKERHUB_PAT`, `build.yml:36-40`) can re-push the version tag and so choose the image of the
operator (cluster-wide ClusterRole), the pre-upgrade hook (its own ClusterRole) and every sidecar
and observer on a cache miss. Dormant. A stamp removes the Docker-Hub-only principals, not a GitHub
repository writer (controls chart and push secret; `gh-pages` is unprotected) or a compromised
release run. Installers can pin today with [`image.digest`](../operations/installation.md#imagedigest).

## Required changes

### Shared, one change each

1. **Double load first** (independent): one debug run (`workflow_dispatch`, `logLevel: debug`) to
   confirm, then delete `renovate.yml:46`. Proof: the dashboard lists each regex file once, which the
   later dashboard proofs rely on.
2. **One `renovate.json` rule edit**, as the last packageRules (after `:284-290`): the
   `kindest/node` and cert-manager rule (Q1), the exporter rule (Q4; one rule with Q1's
   `automerge: false` if both take the recommended option), the `chore` rule (Q3), which must not
   match the exporter. Proof: `renovate-config-validator` passes and the resolved config shows the
   intended `automerge` and commit type per dependency.
3. **One ADR 0033 D5 edit:** the title (Q7: the operator image is pinnable by `image.digest`, the
   exporter default is pinned; re-read `:58`), the body for the recurring exporter bump (change 11,
   independent), and residual risks `:645-648` with the Q4 and Q6 decisions.
4. **Close:** decision D56 in [ADR 0017](../adr/0017-test-and-ci-policy.md) (Status, index row):
   CI-environment pins are Renovate-managed with manual review and `chore`, with Q1's criterion and
   Q3's rule, naming test-only Go modules left at `fix`.

### CI environment and commit types

5. **kubectl** (independent): `kubectl_version: v${{ env.KUBERNETES_VERSION }}` on the kind-action
   step (`release.yml:155-162`, or the literal under Q1 E), delete `:148-151`. Proof: the job log shows
   "Installing kubectl..." and a `Client Version` equal to the node version.
6. **Test images** (independent, land with or after Q3, or 9.1.2 and 8.1.10 become `fix` releases):
   top-level `ignorePaths` = the preset list without `**/test/**` (`**/node_modules/**`,
   `**/bower_components/**`, `**/vendor/**`, `**/examples/**`, `**/__tests__/**`, `**/tests/**`,
   `**/__fixtures__/**`). Proof: the dashboard lists `images.go` with two `valkey/valkey`.
7. **Template** (independent): delete `.github/release-template.hbs` and `renovate.json:331-344`;
   fix `renovate.json:29`, `DEVELOPER.md:90`, `:265`. Proof: `git grep -n release-template --
   ':!docs/tickets'` empty, `make test-release-tooling` green.
8. **Toolchain table** (independent): `DEVELOPER.md:268` (drop "and kubectl"), `:266`, `:273`.
9. **(Q1)** the managers; point `DEVELOPER.md:143`, `:213`, `:268-269`,
   [`testing.md:164`](../developer/testing.md) and `workload-pod-posture.md:185` at the variable.
   Proof: the dashboard lists `kindest/node` and `cert-manager/cert-manager`; a broken regex in a
   scratch copy removes them from a dry run.
10. **(Q2)** the `Makefile:50` line, then `make test-unit` and `make test-integration`. **(Q3)**
    Proof: the next CI-only bump is `chore(deps)` and cuts no tag.

### Default exporter image

11. **ADR text** (independent, in change 3, cross-referenced from ADR 0007 D7): a default bump rides
    the release roll; on the chart-default path a rootless single pod without Sentinel defers it; on
    kustomize or a floating `image.tag` it replaces that pod, a non-persistent one with its data, which
    ADR 0005 D11 and ADR 0014 D8 leave to the deviating admin. Name ADR 0032 D3 as counter-precedent
    (`pod_security_migration.go:109-119`) and why this change does not get that protection (only an
    operator upgrade on a non-canonical path triggers it, and deferring keeps a security bump out).
12. **Functional check** (independent, before the manager lands): one e2e subtest on `rl-mr`
    ([`pod_security_test.go:128-135`](../../test/e2e/pod_security_test.go), TLS, auth, metrics, both
    Valkey legs) reads `/metrics` of each data pod (for example via the API server pod proxy) and
    asserts `redis_up 1`. Mutation: without `REDIS_PASSWORD` in `buildExporterContainer` only it fails.
13. **Manager** (independent of the answers): custom regex on `api/v1/valkey_types.go` keyed on
    `oliver006/redis_exporter:`, capturing `currentValue` and `currentDigest`, `depNameTemplate`
    `oliver006/redis_exporter`, datasource and versioning `docker`, no `// renovate:` comment (it
    would join the godoc), type `fix`. Proof: a dry run lists it from every matched file, a broken
    regex in a scratch copy does not; after the merge, dashboard #229.
14. **(Q5)** the copies; either way ADR 0033 `:266` and `workload-pod-posture.md:96-97`, `:212` name
    the constant instead of a version. **Close:** rewrite the five hand-maintenance statements. The
    first Renovate PR (v1.66.0 to v1.92.0) is not part of this ticket.

### Operator image digest

15. **(Q6)** `build` job: `outputs: digest: ${{ steps.build.outputs.digest }}`. `release-helm-gh`:
    fail unless it matches `^sha256:[0-9a-f]{64}$`, then `sed` it into the release-only
    `image.releaseDigest` next to `build.yml:182` (under A′ also `image.releaseTag` = `${VERSION}`).
16. **(Q6)** `values.yaml`: the release-only value(s), committed empty, with a comment.
    `_helpers.tpl`: apply `releaseDigest` only while `image.digest` is empty, `image.repository` is
    literally `guidedtraffic/valkey-operator` and the tag passes the guard; else render by tag. A
    malformed release value is refused naming that value.
17. Render rows (in T43's matrix, by hand with `helm template` until it exists): default stamped,
    tag override, repository override, explicit digest, release value cleared, malformed value
    refused, explicit tag equal to the release, `--reuse-values` simulation, literal drift between
    helper and `values.yaml:6`.
18. Release-time check after packaging, before publishing: `helm template` of the `.tgz` must contain
    `--operator-image=guidedtraffic/valkey-operator:${VERSION}@${DIGEST}`, or the release fails.
19. Docs: H-12 (`operator-pod-posture.md:71-77`, row `:25`), `installation.md#imagedigest`,
    `README.md:550`, [ADR 0013:253-254](../adr/0013-operator-is-cluster-wide-privileged.md),
    [`upgrading.md:155-158`](../operations/upgrading.md), `workload-pod-posture.md:99`,
    `DEVELOPER.md:313-316`, `CLAUDE.md:1001-1003`, the `values.yaml:10-13` comment.

**Verification** (next real release): the published `image.releaseDigest` equals
`docker buildx imagetools inspect guidedtraffic/valkey-operator:<version>`; `helm template` of the
`.tgz` shows `--operator-image=…:<version>@<digest>`, with `--set image.tag=<previous>` no digest; a
Kind install runs operator and a new sidecar by that digest, the tag override by tag; change 18 green.

## Open questions

### Q1: How are the CI Kubernetes version and the cert-manager pin kept current? (CI environment)

A new custom manager inherits automerge and `fix`. The catch-up is cert-manager first (1.21 runs on
1.33.4), then Kubernetes inside its range; each node image also swaps the containerd ADR 0017 `:270`
measures against.

- **A - two custom regex managers, manual review (recommended).** One on `KUBERNETES_VERSION`
  (`docker`, `kindest/node`, `extractVersionTemplate: "^v(?<version>.+)$"`), one on the cert-manager
  URL in `Makefile` and workflows. Rule: `automerge: false`, `chore`, `separateMultipleMinor` for
  `kindest/node`. Criterion: the newest Kubernetes minor inside the newest supported cert-manager's
  range, cert-manager first. Cost S.
- **B - the same managers, automerged.** One rule line less; a green matrix merges `v1.37.0`, which
  no released cert-manager declares.
- **E - literals in the kind-action inputs** (`node_image` with digest, `kubectl_version`), built-in
  extractor, `chore`; still needs `automerge: false`, a `groupName` and the cert-manager manager.
  Cost S. Native digest pin, but the version lives in two literals.

A keeps one literal for node image, kubectl and docs, and every move reaches a reviewer.

**Answer:** _open_

### Q2: How is `ENVTEST_VERSION` kept current? (CI environment)

- **B - pin the tag `v0.25.1`.** The existing manager matches. Cost XS. Each bump is its own `fix`
  release unless Q3 lists it; tool and controller-runtime can drift a minor.
- **C - derive it from `go.mod`, delete the comment (recommended).**
  `$(shell awk '$$1=="sigs.k8s.io/controller-runtime"{print $$2}' go.mod)` with Kubebuilder's
  empty-result guard. Cost XS. Moves inside the controller-runtime PR; a missing tag turns it red.

C removes the separate releases and the drift; its only failure mode is a red PR.

**Answer:** _open_

### Q3: Which Renovate bumps get the `chore` type so they cut no release? (commit types)

`chore` defers a change to the next release. Shipped Go modules, the Go toolchain and the exporter
stay `fix` in both options.

- **b - an explicit list (recommended).** golangci-lint, gocyclo, gosec, govulncheck, kustomize (full
  Makefile `depName`s), `kindest/node`, `cert-manager/cert-manager`, setup-envtest only if Q2 = B,
  `valkey/valkey` scoped to `images.go`; a second rule for npm. controller-gen stays `fix`. A
  forgotten CI-only tool causes an extra release.
- **c - by file** (`Makefile`, `images.go`, npm), controller-gen included. No list, but a
  controller-gen bump changes the shipped CRD without its own release.

b fails toward an extra release, c toward a silent non-release of a shipped change.

**Answer:** _open_

### Q4: Does an exporter PR wait for a human review, or automerge after a quarantine? (exporter)

Without an override the new manager automerges at once. Both options cut a `fix` release and add no
roll on the chart-default path; they differ in whether a person reads the upstream changelog first.

- **A - `automerge: false` (recommended).** One packageRule plus a merge click per PR; an unmerged
  PR ages the pin again, visibly on the dashboard. Can share the rule of Q1 A.
- **B - automerge after `minimumReleaseAge`** (for example `"14 days"`). No merge work, a quarantine
  window for a broken or compromised tag; sound only after change 12. A changed metric set reaches
  users in a patch release unread. Matches the Go toolchain, modules and base images.

A, because metric-set changes land in minors (v1.90.0) and no test or shipped alert reads the
exporter's metrics, so only a person reading the changelog catches them.

**Answer:** _open_

### Q5: How do the documentary copies of the full exporter reference follow a bump? (exporter)

`README.md:368`, `CLAUDE.md:133` and `examples.md:235` carry the literal; Renovate edits only files
its manager matches.

- **(a) The manager also matches the three documents (recommended).** One PR moves constant and
  copies; three more file patterns, four files per PR.
- **(d) Literal only in the README row**, `CLAUDE.md` and `examples.md` name the constant. Stricter
  ADR 0035 D3, and no copyable exporter pin in an example CR (which would freeze that cluster's
  exporter); the examples no longer show the value.

(a), because every copy is correct on merge with no hand step, and the documentation standard asks
for examples populated with the default.

**Answer:** _open_

### Q6: Which tag guard decides that the stamped operator digest applies? (operator digest)

The stamped digest may apply only when the rendered tag is the chart's own release, or a tag
override would run the release image under another label. Both fail open (render by tag) and share
one cost: an installer who sets `image.tag` to the chart's own release is pinned, loses the pin on the
next chart upgrade without moving the tag, and the data tiers roll once with identical content.

- **A′ - guard on a stamped `image.releaseTag` (recommended).** `eq $tag .Values.image.releaseTag`,
  one values line and one `sed` more. Under `--reuse-values` old tag, release tag and digest travel
  together and the pin stays with no roll (rendered `…:1.13.1@sha256:6a6b1d7d…` under a 1.13.2 chart).
- **A′v - guard on `.Chart.AppVersion`.** Under `--reuse-values` the old tag no longer equals the new
  `AppVersion`, the pin drops silently and the data tiers roll with identical content (rendered
  `…:1.13.1`). Under Flux both behave the same.

A′ is the only form whose pin survives `--reuse-values`, for two lines more.

**Answer:** _open_

### Q7: Is the ADR 0033 D5 title a false statement about the operator image? (operator digest)

The title says images are "by default pinned by digest"; the body pins only the exporter by default.
Read literally, rule 1 applies and change 3 corrects the title; read as scoped by the body, the title
correction is dropped (change 11 stays) and this alone would be `later`, while the package stays
`now` through the CI-environment statements.

- **Literal (recommended).** Correct the title; a reader of the title alone is misled today.
- **Scoped by its body.** Drop the title correction.

**Answer:** _open_

## Not verified

- The fleet's Kubernetes minor; it decides whether severity becomes medium.
- Whether all self-hosted runners are ephemeral (kubectl caching); change 5's log check settles it.
- The causes of the `images.go` skip and the doubled counts; change 1's debug run settles both.
- That setup-envtest `v0.25.1` installs the 1.29.0 assets with both tiers green; Q2's test run.
- Whether a Go or `x/crypto` fix after go1.23.2 is reachable in the exporter; a Trivy or Grype scan
  of both digests (`d98e6db8…`, `ca3abd5f19da…`) settles it.
- The toolchain of the `linux/amd64` exporter image (`--version` of the pinned digest).
- That the literal-keyed exporter regex extracts as intended; a dry run or dashboard #229.
- Q4 B only: whether a pending `minimumReleaseAge` holds a platform automerge (`renovate.json:15`).
- Whether an e2e can read the exporter's `/metrics` through the API server pod proxy (change 12).
- Whether an arm64 Kind node with the amd64 operator image resolves `tag@<index digest>` without
  pulling (fallback: `--set image.releaseDigest=` in the fleet-upgrade e2e); a local run settles it.
- Whether GitHub reuses the `build` job's outputs on "re-run failed jobs"; either way a stamp names
  only a digest the same run pushed.
- Helm 4 `--reuse-values` semantics (CI packages with Helm v4.3.0, `build.yml:153`; renders used
  v3.21.3).

## Related

- T44 - retiring the kustomize path removes the kustomize pin and drops it from Q3's list.
- T43 - the render matrix that takes the rows of change 17, plus a malformed-release-value twin.
- T44 - under its option C the hook drops out of the tag-pinned consumers of the operator image.
- T50 - its no-own-ACL-user argument holds only against a compromised operator image, which this
  hardens; under its options every exporter bump re-checks the ACL command set (change 12 catches it).
- T55 - if a `Localhost` seccomp profile ships (its option B), every exporter bump re-validates it.
- T30 - embargoed security finding, dropped.
