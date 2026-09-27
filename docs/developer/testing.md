# Testing

The test tiers, what each needs, the fixtures a test builds on, and the environment variables
that steer the suites. **The rules — what belongs in which tier, what may be skipped, what a
fix has to prove — are [ADR 0017](../adr/0017-test-and-ci-policy.md) and are not restated
here.** The Make targets themselves are listed in [DEVELOPER.md](../../DEVELOPER.md#test).

Read against the tree on 2026-09-27.

## The tiers

| Tier | Command | Build tag | Needs | What it is for |
|---|---|---|---|---|
| Unit | `make test-unit` | none | nothing running | All reconciliation logic, against the controller-runtime fake client and fake Valkey servers |
| Integration | `make test-integration` | `integration` | the envtest binaries, which the target downloads | What only a real API server decides: CRD defaulting and CEL, delete preconditions, what the API server stores or refuses, controller-manager wiring. envtest starts an API server and etcd and **no kubelet**, so no pod ever runs here |
| E2E | `make test-e2e` | `e2e` | a Kind cluster with cert-manager and the operator installed (`make e2e-local` builds all of it) | Rolling updates, failover and recovery against real Valkey; the only tier that writes values into Valkey and checks they reach the replicas |
| Fleet upgrade | `make test-e2e-fleet-upgrade` | `e2e && fleetupgrade` | a Kind cluster with cert-manager and the local image loaded, no operator | Installs a released chart and upgrades it to the local image |
| Helm migration | `make test-e2e-helm` | `e2e && e2e_helm` | a Kind cluster with the operator | Runs `manager migrate` against real CRs |
| Image tools | `make test-image-tools` | `imagetools` | docker, no cluster | What the operator executes inside the pinned Valkey images, and the processes under the rootless posture |
| Release tooling | `make test-release-tooling` | — (node) | node and npm | That the semantic-release dependency set still renders release notes |

The tier boundaries are [ADR 0017](../adr/0017-test-and-ci-policy.md) D2. The per-package
timeouts are in the Makefile: 60 min integration, 30 min E2E, 45 min fleet upgrade, 10 min
Helm migration, 15 min image tools.

`make test-unit` sets `KUBEBUILDER_ASSETS` like the integration targets do, but no unit test
starts an API server: outside `test/`, envtest appears only in comments.

## Unit tests

They sit next to the code and reach no real Valkey — a new controller path that touches Valkey
decides explicitly whether its command fails (the default) or succeeds
([ADR 0017](../adr/0017-test-and-ci-policy.md) D4).

| Fixture | Where | What it gives you |
|---|---|---|
| `newTestReconciler(objs...)` | [`valkey_controller_test.go`](../../internal/controller/valkey_controller_test.go) | A `ValkeyReconciler` on a fake client with status subresources for `Valkey` and `StatefulSet`, a deterministic UID stamped onto every StatefulSet created through it (the fake client assigns none, and the ownership guards compare UIDs), the `mockInstanceChecker` (a healthy cluster by default), a `fakeEventRecorder`, and a `NewValkeyClientFn` that sends every Valkey dial to `127.0.0.1` on the original port — an instant refusal |
| `newTestReconcilerWithInterceptor`, `newTestReconcilerWithVersion` | same file | The same with client interceptors, for a test that has to make one API call fail; or with an operator version set |
| `fakeValkeyServer(t)`, `fakeValkeyServerWithKeys(t, n)` | [`manual_failover_known_master_test.go`](../../internal/controller/manual_failover_known_master_test.go) | A RESP listener on `127.0.0.1` that answers every command: `WAIT` with 1, `DBSIZE` with the key count (4711 by default), everything else `+OK`. Injected through `NewValkeyClientFn` when a command has to succeed |
| `fakeValkeyServer(t, tlsCfg, handler)` | [`internal/sidecar/testsupport_test.go`](../../internal/sidecar/testsupport_test.go) | The sidecar's own RESP server, answering through a handler, optionally over TLS, with generated test certificates beside it |
| `fakeRESP` | [`internal/observer/fake_endpoint_test.go`](../../internal/observer/fake_endpoint_test.go) | A RESP endpoint on loopback driven by a per-command handler that records what it was asked, so an observer test asserts the commands actually sent |
| `newTestValkey(name, opts...)` | [`internal/builder/configmap_test.go`](../../internal/builder/configmap_test.go) | A `Valkey` for the builder tests; the controller and common packages have their own variants |
| `newInitScriptEnv` | [`internal/builder/init_script_exec_test.go`](../../internal/builder/init_script_exec_test.go) | A temporary filesystem standing in for the init container's mounts, with stub `valkey-cli` and `timeout` on `PATH`, so the generated election script is executed rather than read ([ADR 0017](../adr/0017-test-and-ci-policy.md) D19) |

Some unit tests guard a convention rather than a behaviour, and name what they enforce when
they fail:

| Test | Guards |
|---|---|
| `TestConditionRegistry*` ([`condition_registry_test.go`](../../internal/controller/condition_registry_test.go)) | Every `ConditionType` in `api/v1` has exactly one registry row, and the row's kind has what it owes ([ADR 0027](../adr/0027-conditions-are-levels-edges-or-history.md)) |
| `TestHelmClusterRoleCoversGeneratedRole` ([`rbac_drift_test.go`](../../internal/controller/rbac_drift_test.go)) | Every permission the generated role grants is also in the chart ClusterRole ([ADR 0014](../adr/0014-rbac-lives-in-three-places.md) D2) |
| `TestRequiredImageTools_*` ([`image_requirements_test.go`](../../internal/builder/image_requirements_test.go)) | `RequiredImageTools` and the generated scripts agree, in both directions ([ADR 0017](../adr/0017-test-and-ci-policy.md) D42) |
| `TestResourceReconcileSteps_RBACBeforeStatefulSet` ([`reconcile_steps_test.go`](../../internal/controller/reconcile_steps_test.go)) | The sidecar RBAC step runs before the StatefulSet step ([reconcile-loop.md](reconcile-loop.md#the-resource-steps)) |

## Integration tests

[`test/integration/suite_test.go`](../../test/integration/suite_test.go) starts **one** envtest
control plane — Kubernetes `ENVTEST_K8S_VERSION` (1.29.0), CRDs from `config/crd/bases` — and
one controller manager with one `ValkeyReconciler` for the whole package; a second controller
of the same name in one process would fail to register. That reconciler runs with:

- `OperatorImage` `valkey-operator:test`;
- `slowProbes`, a `slowProbeChecker` that delegates to the real `health.Checker` unless a test
  arms it (only the reconcile-concurrency test does);
- one allowed `Localhost` seccomp profile, so both sides of the allow-list run against the real
  API server;
- the metrics collector registered the way `cmd/main.go` registers it, on `127.0.0.1:18080`.

Two clients are shared: `k8sClient` reads through the manager's cache, which is right for
polling; `apiReader` reads straight from the API server, which is what an assertion that
something was **not** created needs — a cache miss proves nothing.

| File | Subject |
|---|---|
| `integration_test.go` | The objects of a standalone cluster, and the `replica-announce-ip` injection of an HA cluster's init container |
| `affinity_test.go`, `pdb_test.go`, `pdb_uid_precondition_test.go` | Anti-affinity terms; the PodDisruptionBudgets and the UID precondition on their delete |
| `foreign_object_test.go` | A generated name held by a foreign ServiceAccount, StatefulSet, Service or ConfigMap |
| `metrics_test.go` | The operator's `/metrics` endpoint |
| `observer_test.go` | The observer Deployment |
| `pod_security_test.go`, `pod_hardening_test.go` | The generated templates after API-server defaulting; the seccomp CEL rules and default; a dropped `hostUsers`; the `Localhost` allow-list |
| `reconcile_concurrency_test.go` | A stuck cluster does not block the others |
| `sidecar_services_test.go` | Service routing on the sidecar's role label |
| `tls_material_test.go` | The TLS material record: rotation reaching the template, non-TLS clusters, the wait for the Secret, the API server refusing to change the carrier |
| `token_projection_test.go` | The sidecar's projected token is accepted by the API server |
| `volumeclaim_conflict_test.go` | Immutable `volumeClaimTemplates`: a persistent cluster keeps converging, enabling persistence is refused and recovers, a resize is reported |

## E2E tests

The suite talks to whatever cluster `KUBECONFIG` (or `~/.kube/config`) names and expects the
operator already installed by Helm in `valkey-operator-system` with
[`test/e2e/helm-values.yaml`](../../test/e2e/helm-values.yaml) — image `valkey-operator:test`,
`pullPolicy: Never`, leader election off, and the two `Localhost` seccomp profiles the hardening
test names. `make e2e-local` does all of that on a fresh Kind cluster; the CI legs do the same
by hand.

### Environment variables

| Variable | Read by | Effect |
|---|---|---|
| `E2E_RUN` | the Makefile's `test-e2e` | Passed as `-run`; empty runs everything |
| `E2E_VALKEY_LINE` | `testimages.Default()` | `""` or `9` runs Valkey 9, `8` runs Valkey 8; any other value panics rather than falling back |
| `E2E_VALKEY_IMAGE` | `testimages.Default()` | Names an image directly and wins over the line. The upgrade pair does not follow it |
| `E2E_REQUIRE_MULTI_NODE` | `requireThreeSchedulableNodes` ([`affinity_test.go`](../../test/e2e/affinity_test.go)) | `true` turns the "fewer than 3 schedulable nodes" skip into a failure |
| `E2E_REQUIRE_USER_NAMESPACES` | `TestE2E_PodHardening_UserNamespacesLocalhostSeccompAndDigest` ([`pod_hardening_test.go`](../../test/e2e/pod_hardening_test.go)) | `true` fails the test when a probe pod with `hostUsers: false` cannot start; otherwise the test runs without the user namespace and skips only those assertions |
| `E2E_UPGRADE_FROM` | [`fleet_upgrade_test.go`](../../test/e2e/fleet_upgrade_test.go), the Makefile | The released chart version the fleet starts on; `1.10.48` in both |
| `E2E_UPGRADE_FROM_REPO` | `fleet_upgrade_test.go` | The chart repository; defaults to `https://guided-traffic.github.io/valkey-operator/` |
| `E2E_UPGRADE_TO_IMAGE` | `fleet_upgrade_test.go`; the Makefile passes `E2E_IMG` | The image the upgrade moves to; defaults to `valkey-operator:test` |
| `MANAGER_BINARY` | [`migrate_e2e_test.go`](../../test/e2e/migrate_e2e_test.go); `test-e2e-helm` sets `./bin/manager` | The binary run as `manager migrate`; defaults to `../../bin/manager` |
| `KUBECONFIG` | `newTestClients` ([`e2e_test.go`](../../test/e2e/e2e_test.go)) | The cluster; `~/.kube/config` when unset |

Why the skip guards exist, and that a skipped E2E never counts as coverage, is
[ADR 0017](../adr/0017-test-and-ci-policy.md) D5.

### Image pins

[`test/testimages/images.go`](../../test/testimages/images.go) holds the only Valkey image pins
of the suites, each maintained by Renovate within its major line
([ADR 0017](../adr/0017-test-and-ci-policy.md) D43):

| Constant | Value (example — Renovate moves it) | Used as |
|---|---|---|
| `Valkey9` | `valkey/valkey:9.1.1` | the default for every suite, and `UpgradeTo` |
| `Valkey8` | `valkey/valkey:8.1.9` | the second E2E leg, and `UpgradeFrom` |

Unit and integration tests pull no image; the image strings in them are fixtures that only have
to differ from one another.

### Shared helpers

[`test/e2e/e2e_test.go`](../../test/e2e/e2e_test.go) holds what the E2E files share. The ones a
new test most often needs:

| Helper | What it does |
|---|---|
| `newTestClients` | Kubernetes and dynamic clients, with the client rate limit raised to 50 QPS / burst 100 for parallel polling |
| `createNamespace`, `createValkey`, `deleteValkey` | Test namespace and CR lifecycle |
| `waitForValkeyPhase…`, `waitForStatefulSetReady`, `waitForPodReady` | Polling waits on status and readiness |
| `waitForPodRecreated` | Waits until a pod exists under a **new UID** and is Ready — the wait after deleting a pod, because a StatefulSet counts a terminating pod as ready until it is gone ([ADR 0017](../adr/0017-test-and-ci-policy.md) D50) |
| `readyEndpointPodNames` | The pods behind a Service with a ready address, read from `discovery.k8s.io/v1` EndpointSlices ([ADR 0017](../adr/0017-test-and-ci-policy.md) D51) |
| `valkeyExec`, `valkeyMSET` | Commands inside a Valkey pod; writing real keys |
| `waitForConnectedReplicas`, `waitForReplicaSynced`, `replicationEstablished` | Replication checks against the live instances |
| `valkeyPodForensics` | Pod state and logs for a failure message |

## Image tools

[`test/imagetools/`](../../test/imagetools/) runs both pinned images with docker:
`TestImageProvidesEveryRequiredTool` asks each image for every tool in `RequiredImageTools`,
`TestImageShellSupportsTheConstructsTheScriptsUse` checks the shell, and the
`TestRestrictedRuntime_*` tests run `valkey-server`, `valkey-sentinel` (on a hand-written
config), the generated probe, drain hook ~~, pre-flight and repair as uid 999 with every
capability dropped and a read-only root filesystem.~~ and pre-flight as uid 999 with every
capability dropped, and the `fix-data-ownership` repair as uid 0 with every capability dropped
except `CAP_CHOWN`, as it runs in the pod ([ADR 0032](../adr/0032-generated-pods-run-rootless.md)
D2); every one of them has a read-only root filesystem and no privilege escalation *(corrected
2026-09-27: this said the repair ran as uid 999 as well;
`TestRestrictedRuntime_PreflightAndRepair` runs it with `--user 0:0 --cap-drop ALL --cap-add
CHOWN`)*. The generated config writers are not run there, and nothing in this tier exercises a
Kubernetes node ([ADR 0017](../adr/0017-test-and-ci-policy.md)
D42, D53).

## What CI runs

The `E2E Tests` matrix in `.github/workflows/release.yml` runs three legs in parallel, each on
its own Kind cluster (Kubernetes 1.33.4, containerd's `native` snapshotter):

| Leg | Workers | Valkey line | Scope |
|---|---|---|---|
| `single-node-valkey9` | 0 | 9 | the full suite |
| `multi-node-valkey9` | 3 | 9 | `E2E_RUN='TestE2E_AntiAffinity\|TestE2E_PodDisruptionBudget'`, with `E2E_REQUIRE_MULTI_NODE=true`, and a grep that `TestE2E_AntiAffinity_HardSpreadsAcrossNodes` and `TestE2E_PodDisruptionBudget_SerializesEvictions` passed |
| `single-node-valkey8` | 0 | 8 | the full suite |

Why these three, and why three workers, is [ADR 0017](../adr/0017-test-and-ci-policy.md) D31,
D32. No leg sets `E2E_REQUIRE_USER_NAMESPACES`, and no workflow runs the fleet-upgrade or the
Helm-migration target. A failing leg collects the operator log, events and the pod logs of
every `e2e-*` namespace, previous containers included.

Unit, integration, image tools and release tooling each have their own job; the full job list
is in [DEVELOPER.md](../../DEVELOPER.md#continuous-integration-and-the-release).

## What is wrong today, or not verified

- **`E2E_TESTS=true`** is set by the CI step that runs `make test-e2e`, and no test reads it
  (searched 2026-09-27).
- **`make test-e2e-helm` passes `MANAGER_BINARY=./bin/manager`**, but `go test` runs the
  package with `test/e2e/` as its working directory and the test does not set one for the
  command, so that relative path points at `test/e2e/bin/manager`. The test's own default,
  `../../bin/manager`, is the path that resolves to the binary the target builds. Read from the
  code; the target was not run.
- **Nothing in `test/integration/`, `test/e2e/` or `test/imagetools/` is vetted or linted**,
  because every file there carries a build tag and the lint targets pass none — the lint
  targets are listed in [DEVELOPER.md](../../DEVELOPER.md#build-test-and-lint). *(Corrected
  2026-09-27: "the lint targets" are `lint` and `vet` only, and "vetted" means with the full
  analyzer set — `go test` in each tier's own target still runs vet's test subset on the files
  it compiles. `gosec` and `vuln` are not part of this gap. They pass no tag either, but they
  skip `_test.go` files by default: the Makefile passes neither gosec's `-tests` nor
  govulncheck's `-test`, both default false (gosec v2.29.0 source, `bin/govulncheck -h`
  v1.8.0), and all 42 files in these three directories are `_test.go` files, so a tag would not
  bring them into scope.)*
