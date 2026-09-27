# Developer guide

For people changing this code. [README.md](README.md) is the product's front page and
carries the [CRD reference](README.md#crd-reference), [docs/operations/](docs/operations/) is
what somebody running the operator needs, [docs/security/](docs/security/) is the security
design and the gaps it leaves, and this page is the contributor's entry point: where things
live, how to build and test them, what continuous integration gates, and the conventions that
are not obvious from the tree.

**Per-subsystem depth is in [docs/developer/](docs/developer/README.md), and this page never
repeats it.** That directory's README says which page to read when. Read the page for a
subsystem before you change it, and update it in the same change: those pages point at files
and functions on purpose, so they go stale when the tree moves.

## What has to be in your head first

- **One CRD, `Valkey`, in `vko.gtrfc.com/v1`.** Sentinel is part of it
  (`spec.sentinel.enabled`), not a second kind, and nothing validates it but the CRD schema
  ([ADR 0015](docs/adr/0015-one-crd-validated-by-schema-only.md) D1, D2).
- **One binary, four modes.** [`cmd/main.go`](cmd/main.go) runs the operator, or — when the
  first argument is `sidecar`, `observer` or `migrate` — the sidecar in every data pod, the
  optional observer Deployment, or the Helm pre-upgrade hook. The sidecar and the observer
  therefore run the operator image
  ([architecture.md](docs/developer/architecture.md#one-binary-four-processes)).
- **The operator replaces pods itself.** Both StatefulSets use `updateStrategy: OnDelete` and
  `podManagementPolicy: Parallel`; the rolling update compares pods against the persisted
  StatefulSet template, never against the CR
  ([architecture.md](docs/developer/architecture.md#what-runs-in-a-pod),
  [ADR 0007](docs/adr/0007-failover-aware-rolling-update.md) D2).
- **Every managed object is named from the CR name**, which the CR author picks. Nothing is
  written or deleted on a generated name without proof of ownership
  ([ADR 0020](docs/adr/0020-write-only-what-the-operator-owns.md),
  [ADR 0006](docs/adr/0006-delete-only-what-the-operator-owns.md)).
- **Without Sentinel, `vko.gtrfc.com/known-master` on the CR is the recorded master
  authority** ([ADR 0008](docs/adr/0008-known-master-annotation-is-the-recorded-authority.md)),
  and the sidecar, which has no CR access, records its own drain promotion on the pod
  ([ADR 0012](docs/adr/0012-the-sidecar-records-its-drain-promotion-on-the-pod.md)).

## Repository layout

```
cmd/
  main.go                 the binary: operator mode, and dispatch to the three subcommands
  sidecar/                `manager sidecar` - flags and wiring for internal/sidecar
  observer/               `manager observer` - flags and wiring for internal/observer
  migrate/                `manager migrate` - the Helm pre-upgrade hook Job: writes field
                          defaults into every existing Valkey CR
api/v1/                   the Valkey types, condition types, reasons and kubebuilder markers;
                          zz_generated.deepcopy.go is generated
internal/
  controller/             the reconciler: resource pass, rolling updates, master authority,
                          status and conditions
  builder/                builds every managed object, the generated shell scripts and the
                          drift comparisons; no API calls
  common/                 labels, names and the constants the sidecar shares with the operator
  health/                 cluster health assessment, the reconciler's default InstanceChecker
  valkeyclient/           the small RESP client every component dials Valkey and Sentinel with
  sidecar/                role labeler, drain handler and readiness endpoint of every data pod
  observer/               checks, metrics and HTTP server of the optional observer Deployment
  metrics/                per-resource series on the operator's own /metrics endpoint
  tlsmaterial/            per-dial TLS reload for the long-lived processes this repo owns
config/
  crd/bases/              generated CRD (make manifests)
  rbac/                   generated ClusterRole (role.yaml), its binding and ServiceAccount
  manager/, default/      kustomize overlays behind make deploy
deploy/helm/valkey-operator/
                          the chart; `helm upgrade` with it is the one supported upgrade
                          path. templates/crd.yaml is synced from config/crd/bases,
                          templates/clusterrole.yaml is maintained by hand
test/
  integration/            envtest tier, build tag `integration`
  e2e/                    Kind tier, build tag `e2e` (plus `fleetupgrade` and `e2e_helm`
                          for two dedicated targets)
  imagetools/             checks the pinned Valkey images, build tag `imagetools`
  testimages/             the Valkey image pins every image-pulling suite uses
hack/
  boilerplate.go.txt      the header controller-gen writes into generated Go
  verify-release-tooling.mjs
                          renders release notes through .releaserc.json
docs/
  adr/                    every durable decision
  developer/              how the subsystems work (this guide's depth)
  operations/             what an operator of the product needs
  security/               the security design, one page per perspective
  tickets/                work lists; archive/ holds the finished ones
.github/
  workflows/              release.yml (test and release), build.yml (image and chart on a
                          published release), renovate.yml
  badges/coverage.json    the coverage badge, committed by the release
  release-template.hbs    a release-notes template .releaserc.json does not reference
  idea.md                 free-form feature ideas
Containerfile             the operator image: golang builder, distroless nonroot runtime
Makefile                  the only entry point for build, test and analysis
.releaserc.json, package.json, package-lock.json
                          semantic-release and its pinned npm dependency set
renovate.json             dependency update rules
```

File-level detail is [docs/developer/package-map.md](docs/developer/package-map.md).

## Core flows, one fact each

- **A pass starts on a spec change, an owned-object event or a referenced Secret changing** —
  never on a status write, because the CR watch uses `GenerationChangedPredicate`. Everything
  else is a requeue or a retry the pass itself arranged
  ([reconcile-loop.md](docs/developer/reconcile-loop.md#what-starts-a-pass)).
- **The resource pass runs every applicable step and joins the errors**; a rejected write fails
  its own step and nothing else
  ([ADR 0001](docs/adr/0001-continue-reconciling-past-a-rejected-write.md) D1).
- **"sidecar RBAC" runs before "StatefulSet"**, so the sidecar Role names a new pod before the
  StatefulSet creates it; `TestResourceReconcileSteps_RBACBeforeStatefulSet` holds the order
  ([reconcile-loop.md](docs/developer/reconcile-loop.md#the-resource-steps)).
- **A blocked pass writes its phase once, as `Error`, and `ReconcileBlocked` names the cause**
  ([ADR 0002](docs/adr/0002-surface-a-blocked-reconcile-on-the-cr.md) D1, D3), while the
  data-plane half of the pass still runs
  ([ADR 0001](docs/adr/0001-continue-reconciling-past-a-rejected-write.md) D3).
- **The workload pass nudges first, then rolls the data tier, then the Sentinel tier**, then
  runs the no-master recovery, the steady-state split-brain check and the status write
  ([reconcile-loop.md](docs/developer/reconcile-loop.md#the-workload-pass),
  [ADR 0003](docs/adr/0003-nudge-a-short-of-pods-statefulset.md) D7,
  [ADR 0024](docs/adr/0024-the-sentinel-tier-reports-its-own-completion.md)).
- **The status is written only when it changed**, compared against a copy captured before the
  pass touched it ([ADR 0002](docs/adr/0002-surface-a-blocked-reconcile-on-the-cr.md) D5, D8).
- **Four CRs are reconciled at a time by default** (`--max-concurrent-reconciles`), **and two
  passes for one CR never overlap**, because the work queue serialises a key
  ([ADR 0019](docs/adr/0019-reconcile-concurrency-and-the-cost-of-a-stuck-pass.md)).
- **A failing pass is retried with a per-item backoff capped at 30 s**
  ([ADR 0001](docs/adr/0001-continue-reconciling-past-a-rejected-write.md) D6).

## Build, test and lint

Everything goes through the [Makefile](Makefile); CI invokes the same targets
([ADR 0017](docs/adr/0017-test-and-ci-policy.md) D1). `make help` lists the targets with their
one-line descriptions.

### Prerequisites

- Go at the version `go.mod` declares (`go 1.27.1` today; see
  [The toolchain versions](#the-toolchain-versions))
- Docker (`docker-build`, `test-image-tools`, `e2e-local`)
- [Kind](https://kind.sigs.k8s.io/) (for local E2E testing)
- [cert-manager](https://cert-manager.io/) (for TLS E2E tests; `make cert-manager-install`
  applies v1.17.2 to the current cluster)
- `kubectl` and Helm (`cert-manager-install`, `e2e-local`, `install`/`deploy`)
- Node.js and npm (`test-release-tooling` only)

Every pinned Go tool — controller-gen, setup-envtest, kustomize, golangci-lint, gocyclo, gosec,
govulncheck — installs itself into `bin/` on first use.

### Build

```bash
make build        # Build operator binary
make docker-build # Build container image
```

| Target | What it does |
|---|---|
| `build` | `fmt` and `vet` first, then `go build -o bin/manager cmd/main.go`. `fmt` rewrites files (`gofmt -s -w .`) |
| `docker-build` | runs `generate-all` first, then `docker build -f Containerfile -t ${IMG}` (`IMG` defaults to `guidedtraffic/valkey-operator:latest`) |
| `docker-push` / `docker-buildx` | push `${IMG}`; `docker-buildx` builds and pushes `linux/amd64` and `linux/arm64` |
| `generate-all` | `manifests` + `generate` + `sync-helm-crd`: the CRD and `config/rbac/role.yaml` from the kubebuilder markers, `zz_generated.deepcopy.go`, and the CRD copied into `deploy/helm/valkey-operator/templates/crd.yaml`. Run it after any `api/v1/` or RBAC-marker change and commit the result |

### Test

```bash
make test-unit               # Unit tests
make test-unit-coverage      # Unit tests with coverage
make test-integration        # Integration tests (envtest)
make test-e2e                # E2E tests (requires running cluster)
make test-e2e E2E_RUN='TestE2E_PodDisruptionBudget'  # E2E tests filtered by name
make e2e-local               # Full E2E: create Kind cluster (control-plane + 3 workers) → deploy → test → cleanup
make lint                    # Linting (golangci-lint + go vet)
make gosec                   # Security scan
make vuln                    # Vulnerability check
make cyclo                   # Cyclomatic complexity check
```

What each tier is for, its fixtures and its environment variables are
[docs/developer/testing.md](docs/developer/testing.md). The targets the block above does not
show:

| Target | What it does |
|---|---|
| `test-integration-coverage` | the integration tier with `-coverpkg=./...` into `coverage/integration.out`; what CI runs |
| `test-image-tools` | the pinned Valkey images against `RequiredImageTools`, under the rootless posture |
| `test-release-tooling` | `npm ci`, then `node hack/verify-release-tooling.mjs` |
| `test-e2e-fleet-upgrade` / `e2e-fleet-upgrade-local` | `TestE2E_FleetUpgrade`: the test installs the released chart named by `E2E_UPGRADE_FROM` itself and upgrades it to the local image. The `-local` form first creates the Kind cluster, installs cert-manager and loads the image; it installs no operator, because the test owns both ends of the upgrade |
| `test-e2e-helm` | builds `bin/manager` and runs `TestE2E_Migrate*` against a running Kind cluster |
| `test` / `test-coverage` | `fmt`, `vet`, then the untagged tree (the unit tier) with `-coverprofile cover.out`; `test-coverage` renders `coverage.html` from it |

`make e2e-local` stops at the first failing step, so a red E2E run leaves the Kind cluster
`valkey-operator-test` up; `make kind-delete` removes it.

**Quality, security and coverage**

| Target | What it does |
|---|---|
| `fmt` / `vet` | `gofmt -s -w .` / `go vet ./...` |
| `lint` / `lint-fix` | `go vet ./...`, `gofmt -l .` and golangci-lint (configured in [.golangci.yml](.golangci.yml)); `lint-fix` runs golangci-lint with `--fix` |
| `cyclo` / `cyclo-report` | gocyclo over 15 (`CYCLO_THRESHOLD`), ignoring `_test.go` and `zz_generated`; `cyclo-report` prints the top 20, tests included |
| `gosec` | gosec with bounded concurrency and memory (`GOSEC_CONCURRENCY`, `GOSEC_MEMLIMIT`), which the shared self-hosted runners need |
| `vuln` | govulncheck |
| `coverage` / `coverage-ci` | untagged tree with a profile in `coverage/`; `coverage` also renders HTML |
| `coverage-merge` / `coverage-json` | merge `coverage/unit.out` and `coverage/integration.out`, and write `.github/badges/coverage.json` from the result. `coverage-merge` installs `gocovmerge@latest` into `GOBIN` — the one tool not pinned into `bin/`. CI merges in its own step and calls neither |

**Cluster targets**

| Target | What it does |
|---|---|
| `kind-create` / `kind-delete` | the Kind cluster `valkey-operator-test`, control plane plus three workers |
| `kind-load` | `docker-build`, then load `${IMG}` into that cluster |
| `cert-manager-install` | cert-manager v1.17.2 plus the self-signed `ClusterIssuer` from `test/e2e/testdata/cert-manager-issuer.yaml` |
| `install` / `uninstall` | apply / delete `config/rbac`. The help text says "CRDs", but that directory holds the ClusterRole, its binding and the ServiceAccount, not the CRD |
| `deploy` / `undeploy` | apply / delete `config/default` (rbac and manager) with the image set to `${IMG}`; the CRD is not part of it |

The chart is the one supported upgrade path
([ADR 0014](docs/adr/0014-rbac-lives-in-three-places.md) D8) and carries the CRD in
`templates/`. No Make target lints or renders the chart on its own; `e2e-local` and the E2E
jobs install it.

Four things the Makefile encodes that are not obvious:

- **No `-short`, anywhere.** `test-unit` and its siblings pass none, and no test may gate
  itself behind `testing.Short()`; the reason is the comment above `test-unit`
  ([ADR 0017](docs/adr/0017-test-and-ci-policy.md) D3).
- **A tool's path carries its version** (`bin/controller-gen-v0.22.0`), so a version bump is
  a missing file and installs itself, and a tool-path or tool-version variable sits above the
  first target that names it ([ADR 0017](docs/adr/0017-test-and-ci-policy.md) D49).
- **`lint` and `vet` pass no build tags**, and `.golangci.yml` sets none, so the build-tagged
  files under `test/` are compiled only by their own test targets; what that leaves unchecked,
  and why `gosec` and `vuln` are unaffected, is
  [testing](docs/developer/testing.md#what-is-wrong-today-or-not-verified).
- **`docker-build` regenerates before it builds**, and `kind-load` inherits that through its
  prerequisite. `docker-buildx`, `e2e-local` and `e2e-fleet-upgrade-local` call `docker build` /
  `docker buildx build` directly and do not regenerate. After an `api/v1/` or RBAC-marker change,
  run `make generate-all` first. Otherwise, after an `api/v1/` change, those targets compile the
  stale `zz_generated.deepcopy.go` into the image, and `e2e-local` also installs the stale chart
  CRD. *(Corrected 2026-09-27: this line said that a stale generated file never reaches an image
  built through the Makefile, which holds for `docker-build` and `kind-load` only.)*

### Run Locally

```bash
make run  # Run the operator against the current kubeconfig
```

`run` is `fmt`, `vet`, then `go run ./cmd/main.go --zap-log-level=debug`. Two things it does
not do for you:

- **The CRD must already be in the cluster.** No Make target applies it on its own (see
  `install` above); `kubectl apply -f config/crd/bases/vko.gtrfc.com_valkeys.yaml` does, and a
  chart install does.
- **`run` passes no `--operator-image`.** The flag falls back to the `OPERATOR_IMAGE`
  environment variable, and the sidecar container of every data pod and the observer
  Deployment take their image from it, so set it (for example
  `OPERATOR_IMAGE=guidedtraffic/valkey-operator:<tag> make run`). What an empty value leads to
  was not verified.

## The toolchain versions

| What | Where the literal lives | What moves it |
|---|---|---|
| Go | `go.mod` (`go 1.27.1`), `Containerfile` (`FROM golang:1.27.1-alpine`), `GO_VERSION` in `.github/workflows/release.yml` and `build.yml` | Renovate, one group "Go version": custom regex managers for each of these files, minor and patch automerged, major reviewed by hand. `golang.org/x/*` modules ride the same group |
| Go badge | `.github/release-template.hbs` (`go-1.26`, major.minor) | The same group. The file is referenced by nothing but `renovate.json`, and its badge lags `go.mod`; why Renovate has not moved it was not verified |
| Pinned Go tools | the `*_VERSION` variables in the [Makefile](Makefile), each under a `# renovate:` comment | Renovate's Makefile regex manager. The regex captures only values starting with `v`, so `ENVTEST_VERSION ?= release-0.19` is not matched (read from the regex, not observed) |
| envtest API server | `ENVTEST_K8S_VERSION = 1.29.0` in the Makefile | nothing automatic |
| CI cluster | `KUBERNETES_VERSION: '1.33.4'` (Kind node image and kubectl) and the Kind `version: v0.33.0` input in `release.yml`; Helm `v4.3.0` in both workflows | no custom manager in `renovate.json` names them; ~~whether Renovate's github-actions manager moves the action inputs was not verified~~ *(corrected 2026-09-27)* Renovate moves the Kind and Helm inputs anyway — bot commits `795d332` (Kind `v0.30.0` → `v0.33.0`, `release.yml`) and `d3e731f` (Helm `v4.2.4` → `v4.3.0`, both workflows) — so a built-in manager extracts them, which one the commits do not record; `KUBERNETES_VERSION` is moved by nothing, last changed by hand in `0a90483` |
| cert-manager | v1.17.2 in the Makefile (`cert-manager-install`) and in `release.yml` | nothing automatic |
| Valkey test images | [`test/testimages/images.go`](test/testimages/images.go), one pin per major line | Renovate custom manager, capped per major so a new major is a decision ([ADR 0017](docs/adr/0017-test-and-ci-policy.md) D43) |
| Exporter default image | `DefaultMetricsExporterImage` in [`api/v1/valkey_types.go`](api/v1/valkey_types.go), pinned by digest | nothing automatic; no Renovate manager covers `api/v1` ([ADR 0033](docs/adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md) D5) |
| Kubernetes Go modules | `go.mod` | Renovate group "Kubernetes Go modules"; `k8s.io/kube-openapi` and `sigs.k8s.io/structured-merge-diff` are disabled and follow `k8s.io/apimachinery` by MVS ([ADR 0017](docs/adr/0017-test-and-ci-policy.md) D48) |
| semantic-release | exact versions in `package.json`, locked in `package-lock.json`; Node is `lts/*` in the workflows | Renovate's npm manager (enabled by `config:recommended`, not verified in a run); the conventionalcommits preset stays on 9.x ([ADR 0017](docs/adr/0017-test-and-ci-policy.md) D46) |

## Continuous integration and the release

`.github/workflows/release.yml` ("Test and Release") runs on every push to `main`, on every
pull request into it, and on demand. All jobs run on self-hosted runners.

| Job | Gates |
|---|---|
| E2E Tests (&lt;leg&gt;) | three matrix legs on Kind, each building the image with `make docker-build` and running `make test-e2e`; the legs are in [testing.md](docs/developer/testing.md#what-ci-runs) |
| E2E Tests | `e2e-gate`: fails unless every leg succeeded, so the check name survives a matrix change ([ADR 0017](docs/adr/0017-test-and-ci-policy.md) D31) |
| Code Linting | `make lint` |
| GoSec Security Scan | `make gosec` |
| Vulnerability Check | `make vuln` |
| Cyclomatic Complexity | `make cyclo` |
| Malware Scan (Source Code) | ClamAV over the checkout |
| Container Malware Scan | builds the image and runs Trivy (vulnerabilities, secrets, misconfiguration); fails on `CRITICAL` or `HIGH` |
| Unit Tests | `make test-unit-coverage` |
| Integration Tests (envtest) | `make test-integration-coverage` |
| Valkey Image Tools | `make test-image-tools` |
| Generated Manifests Up To Date | `make generate-all`, then fails on any diff or untracked file ([ADR 0014](docs/adr/0014-rbac-lives-in-three-places.md) D5) |
| Release Tooling | `npm ci`, `npm audit signatures`, `node hack/verify-release-tooling.mjs` |
| Combined Coverage Report | merges the unit and integration profiles, writes the badge artifact and comments on the pull request. No coverage threshold fails it |
| Semantic Release | push to `main` only, and only when all thirteen jobs in its `needs:` succeed: every job above except `e2e-gate`, with the E2E matrix counted once |

**Which of these branch protection requires is repository configuration, not a file here.**
The list and the rule that a new gate job joins it in the same change are
[ADR 0017](docs/adr/0017-test-and-ci-policy.md) D47; the list could not be read back while
this page was written (`gh` was not authenticated), so it is stated from the ADR, not
verified against GitHub.

**The release.** `semantic-release` ([.releaserc.json](.releaserc.json)) analyses the commits
with the conventionalcommits preset, writes the release notes, creates the GitHub release and
commits `.github/badges/coverage.json` back as `chore(release): <version> [skip ci]`. It
authenticates with a GitHub App token rather than `GITHUB_TOKEN`, because a release created
with the latter would not start the next workflow. That next workflow is
`.github/workflows/build.yml` ("Release Docker & Helm"), triggered by the published release:

- builds and pushes `guidedtraffic/valkey-operator` for `linux/amd64` with provenance and an
  SBOM, attaches the SBOM to the release and runs a Docker Scout CVE scan;
- runs `make generate-all` and fails on a dirty tree, so a released chart always carries the
  CRD of its Go types;
- stamps the release version into `Chart.yaml` (`version`, `appVersion`) and the image tag in
  `values.yaml`, packages the chart, publishes it to the `gh-pages` Helm repository at
  `https://guided-traffic.github.io/valkey-operator/`, and attaches the package and
  `index.yaml` to the release.

A green pull request therefore proves nothing about the published image or chart.

`.github/workflows/renovate.yml` runs self-hosted Renovate daily at 02:00 Europe/Berlin, or on
demand, with [renovate.json](renovate.json).

## Adding things

**A CRD field.**

1. Add the field with its doc comment and markers to
   [`api/v1/valkey_types.go`](api/v1/valkey_types.go). The doc comment becomes the field's
   description in the generated CRD, which is never edited by hand
   ([ADR 0014](docs/adr/0014-rbac-lives-in-three-places.md) D10). Defaults are
   `+kubebuilder:default` markers, cross-field rules are CEL
   (`+kubebuilder:validation:XValidation`, as on `SeccompProfileSpec`); there is no admission
   webhook ([ADR 0015](docs/adr/0015-one-crd-validated-by-schema-only.md) D2).
2. A new feature defaults to off, so an operator upgrade changes nothing
   ([ADR 0005](docs/adr/0005-upgrade-neutral-defaults-and-anti-affinity.md) D1). A value that
   reaches a data or Sentinel pod spec changes that tier's pod-spec hash
   (`ComputePodSpecHash`, `ComputeSentinelPodSpecHash`: FNV-32a over the whole built `PodSpec`,
   stored as a pod-template annotation), so every pod of the tier is replaced by the operator's
   rolling update; a default that builds the same pod spec rolls nothing.
3. `make generate-all` and commit the regenerated files; `Generated Manifests Up To Date` fails
   otherwise.
4. CRD defaulting and CEL behaviour are asserted in `test/integration/`, never in a unit test
   ([ADR 0017](docs/adr/0017-test-and-ci-policy.md) D14).
5. Add the field to the [CRD reference](README.md#crd-reference) in README.md, which is the one
   place the reference lives ([ADR 0035](docs/adr/0035-the-readme-advertises-the-reference-lives-under-docs.md)).
6. Check [`cmd/migrate/migrate.go`](cmd/migrate/migrate.go): `applyDefaults` writes six field
   defaults into every existing CR during the pre-upgrade hook; no ADR decides yet which new
   field needs a line there (searched 2026-09-27).

**A status condition.**

1. Declare it in [`api/v1/valkey_types.go`](api/v1/valkey_types.go) as
   `ConditionType<Name> ConditionType = "<Name>"`, with its reasons beside the existing
   `Reason*` constants.
2. Add exactly one row to `conditionRegistry` in
   [`internal/controller/condition_registry.go`](internal/controller/condition_registry.go):
   level, edge or history, the number of evaluators and the ownership rule when there is more
   than one, the clear site, the presence guard. `TestConditionRegistryCoversEveryConditionType`
   parses `api/v1` and fails on a type without a row; its siblings fail on an edge without a
   presence-guarded clear, a level with racing evaluators and a history row that grew a clear
   ([ADR 0027](docs/adr/0027-conditions-are-levels-edges-or-history.md) D1-D3).
3. Write it either through `setStatusCondition` / `writeStatusCondition`, which re-read the CR
   and update the status immediately, or in place on `v.Status.Conditions` between the
   `prevStatus` capture and `persistStatus`, so it rides the pass's own status write
   ([reconcile-loop.md](docs/developer/reconcile-loop.md#the-status-write)). No condition is
   ever deleted ([ADR 0027](docs/adr/0027-conditions-are-levels-edges-or-history.md) D8).
4. Nothing to add for metrics: the collector exports every condition a CR carries as
   `vko_valkey_status_condition`.
5. Add it to the [`status` reference](README.md#status) in README.md.

**An RBAC rule for the operator.** It lives in three places
([ADR 0014](docs/adr/0014-rbac-lives-in-three-places.md)):

1. The `+kubebuilder:rbac` marker above `Reconcile` in
   [`internal/controller/valkey_controller.go`](internal/controller/valkey_controller.go).
2. `make generate-all`, which renders it into `config/rbac/role.yaml`.
3. The same rule by hand in
   [`deploy/helm/valkey-operator/templates/clusterrole.yaml`](deploy/helm/valkey-operator/templates/clusterrole.yaml).
   `TestHelmClusterRoleCoversGeneratedRole`
   ([`rbac_drift_test.go`](internal/controller/rbac_drift_test.go)) asserts generated ⊆ chart
   and names the missing triple.
4. The rule's entry in
   [docs/security/privilege-footprint.md](docs/security/privilege-footprint.md), in the same
   change (ADR 0014 D11).
5. A destructive verb comes with its call-site guard in the same change
   ([ADR 0006](docs/adr/0006-delete-only-what-the-operator-owns.md) D13).

The sidecar's per-cluster Role is not one of the three: it is built by `BuildSidecarRole` in
[`internal/builder/rbac.go`](internal/builder/rbac.go)
([ADR 0012](docs/adr/0012-the-sidecar-records-its-drain-promotion-on-the-pod.md) D8).

**A tool in a generated script.** Anything the init containers, probes, container commands or
the preStop hook execute runs inside the upstream Valkey image
([ADR 0017](docs/adr/0017-test-and-ci-policy.md) D42):

1. Name the tool in `RequiredImageTools`
   ([`internal/builder/image_requirements.go`](internal/builder/image_requirements.go)), with a
   comment on what breaks without it.
2. `TestRequiredImageTools_CoversTheGeneratedScripts` fails on a tool a generated script uses
   and the list does not name — but only for tools in `shellCommandCatalog`
   ([`image_requirements_test.go`](internal/builder/image_requirements_test.go)); a tool outside
   the catalog passes unseen, so add it there too. `TestRequiredImageTools_AreAllUsed` fails the
   other way round.
3. `make test-image-tools` checks the tool in both pinned images.
4. An init-script change is tested by executing the script
   ([ADR 0017](docs/adr/0017-test-and-ci-policy.md) D19, D20).

**A managed object.** A new kind inherits the provenance rule, not an exemption
([ADR 0020](docs/adr/0020-write-only-what-the-operator-owns.md)):

1. A builder in [`internal/builder/`](internal/builder/) that names the object from the CR
   name; the object carries a controller reference to the CR
   ([ADR 0006](docs/adr/0006-delete-only-what-the-operator-owns.md) D14).
2. Before any write onto an existing object, `metav1.IsControlledBy(obj, v)`. A foreign object
   gets its own `reason…NotOwned` Warning in
   [`internal/controller/foreign_object.go`](internal/controller/foreign_object.go) and either
   fails the step (`foreignObjectError`) or asks for a recheck
   (`requestRecheck(ctx, foreignObjectRecheckInterval)`), decided by whether the CR can still do
   the job it was asked to do (ADR 0020 D1, D2, D5, D6).
3. Deletes go through `deleteIfOwned`, which adds the UID precondition
   ([ADR 0006](docs/adr/0006-delete-only-what-the-operator-owns.md) D2, D8). A pod is proven
   two-hop with `podIsOurs(pod, sts)` and deleted with `deleteOwnedPod` (ADR 0020 D9).
4. A step in `resourceReconcileSteps` — mind the order — and, if a change to the object should
   start a pass, an `Owns(...)` line in `SetupWithManager`.
5. The RBAC rule for its verbs (above), and no reconciler state that is not keyed by the CR
   ([ADR 0019](docs/adr/0019-reconcile-concurrency-and-the-cost-of-a-stuck-pass.md) D3).
6. A foreign-object case in
   [`test/integration/foreign_object_test.go`](test/integration/foreign_object_test.go).

**An operator flag.** Declare it in `bindOperatorFlags` in [`cmd/main.go`](cmd/main.go), hand it
to the reconciler in `newReconciler` or to `managerOptions` — a parsed flag must be applied
([ADR 0018](docs/adr/0018-metrics-and-the-exporter-sidecar.md) D8) — and extend
`TestBindOperatorFlags_AllFlagsParsed`. Then pass it in
[`deploy/helm/valkey-operator/templates/deployment.yaml`](deploy/helm/valkey-operator/templates/deployment.yaml)
from a value in `values.yaml`; the chart is how the flag reaches a cluster.

## Conventions

- **Decisions are ADRs** in [docs/adr/](docs/adr/README.md), written in the session the
  decision is taken and kept current: a changed decision updates its ADR in the same change,
  with the superseded rule marked in place. This repository's ADRs may link into the code; the
  format is in the [ADR README](docs/adr/README.md).
- **A ticket is a work list and nothing else.** It lives in
  [docs/tickets/](docs/tickets/README.md) while work is outstanding and is archived when the
  work lands ([ADR 0034](docs/adr/0034-tickets-are-work-lists-that-get-archived.md)). **Cite
  the ADR, never a ticket**, from anything outside `docs/tickets/` — code, commits, pages
  like this one. Older citations elsewhere in the tree predate the rule; add no new one.
- **Documentation ships with the change it describes.** A subsystem change updates its page
  under `docs/developer/`; an RBAC change updates the security documentation
  ([ADR 0014](docs/adr/0014-rbac-lives-in-three-places.md) D11). Nothing enforces this but
  review.
- **The Makefile is the only entry point** for tests, lint and analysis; never `go test` by
  hand ([ADR 0017](docs/adr/0017-test-and-ci-policy.md) D1).
- **Cyclomatic complexity stays under 15** for every hand-written function, with no `nolint`
  exemption ([ADR 0017](docs/adr/0017-test-and-ci-policy.md) D35).
- **Every fix ships with a recorded mutation or revert check**
  ([ADR 0017](docs/adr/0017-test-and-ci-policy.md) D7).
- **Commits follow Conventional Commits** — the release version is computed from them by the
  conventionalcommits preset.
- **English everywhere**: code, comments, documentation, commit messages, CRD fields
  ([ADR 0017](docs/adr/0017-test-and-ci-policy.md) D41).
- **Generated files are never edited by hand**: the CRD in both places, `role.yaml`,
  `zz_generated.deepcopy.go`. The chart ClusterRole is the named exception
  ([ADR 0014](docs/adr/0014-rbac-lives-in-three-places.md) D10).
- **No fleet-wide reconciler state**: every tracker key carries namespace and CR name, and
  every managed name contains the CR name
  ([ADR 0019](docs/adr/0019-reconcile-concurrency-and-the-cost-of-a-stuck-pass.md) D3).
- **No gauge written from a reconcile pass**: the operator's metrics are read from the cache at
  scrape time ([ADR 0021](docs/adr/0021-per-resource-metrics-and-the-alert-that-was-missing.md)
  D3).
