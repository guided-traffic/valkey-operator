# Valkey Operator

Repo: https://github.com/guided-traffic/valkey-operator

## Language Policy

All code, comments, commit messages, documentation, and CRD fields in this repository **must be written in English**.

## Architecture Decision Records

Every durable architecture decision lives in [`docs/adr/`](docs/adr/README.md), one file per
decision family, named `NNNN-kebab-case-title.md`. The index in
[`docs/adr/README.md`](docs/adr/README.md) lists all of them grouped by theme. Read the
relevant ADR before changing the behaviour it describes.

Structure of an ADR:

| Section | Content |
|---|---|
| `# ADR NNNN: Title` | the decision as a title, not a topic |
| `## Status` | `Accepted` / `Superseded by ADR NNNN` / `Amended`, with `Date:` and what is implemented versus open |
| `## Context` | the forces and the concrete failure that made the decision necessary |
| `## Decision` | `D1 … Dn`, each a rule that holds going forward, in present tense |
| `## Consequences` | what it costs, including the parts nobody likes |
| `## Alternatives Considered` | each option and why it lost |
| `## Residual risks` | accepted risks, open items, and what was **not** verified |
| `## References` | relative links to the code and to sibling ADRs |

**ADRs must be kept current. They are part of the code, not a historical note.**

- Changing behaviour an ADR describes means updating that ADR **in the same change**, never
  afterwards.
- **On a re-decision**, the `Decision` section states the new rule, `Status` records the
  amendment with its date, and the superseded rule is marked in place rather than deleted.
  A reader must never find the old rule stated as current.
- A new durable decision — a rule, an invariant, a default, a refusal to act — gets its own
  ADR and a line in the index.
- Every claim is verified against the code; anything unverified says so explicitly.

## CRD

Namespace: `vko.gtrfc.com`

There is only one CRD: `Valkey`. Sentinel is integrated into the Valkey CRD via `spec.sentinel.enabled`.

### Example CRD (HA with Sentinel)

```yaml
apiVersion: vko.gtrfc.com/v1
kind: Valkey
metadata:
  name: test
spec:
  replicas: 3
  image: valkey/valkey:8.0
  sentinel:
    enabled: true
    replicas: 3
    allowUnencrypted: false   # set to true to keep port 26379 open alongside TLS port 36379
    disableAuth: false        # set to true to allow unauthenticated Sentinel client connections
    podLabels:
      app: sentinel
    podAnnotations:
      example.com/sentinel: "true"
    resources:                # optional, no default (omitted = no requests, no limits);
      requests:               # goes to every Sentinel container, init included, so a
        cpu: "50m"            # cpu/memory ResourceQuota can admit the pod - ADR 0033 D7
        memory: "32Mi"        # (values are an example)
  auth:
    secretName: my-valkey-secret
    secretPasswordKey: password
  metrics:
    enabled: true                 # adds a Prometheus exporter sidecar to each Valkey pod
    image: oliver006/redis_exporter:v1.66.0@sha256:d98e6db8094f491b95791e9f776b0ba30a20aeacb90e18334935d5e51bf2e6a1
                                  # optional; this is the default when omitted, pinned by
                                  # digest (DefaultMetricsExporterImage, ADR 0033 D5)
    port: 9121                    # optional; exporter /metrics port (default 9121)
    resources:                    # optional; compute resources for the exporter container
      limits:
        cpu: "100m"
        memory: "64Mi"
    extraArgs: []                 # optional; extra exporter CLI flags, e.g. ["--check-keys=*"]
    service:
      enabled: true               # optional; dedicated <name>-metrics Service (default true)
      labels: {}                  # optional; extra labels on the metrics Service
    serviceMonitor:
      enabled: false              # set true to create a Prometheus-Operator ServiceMonitor
      interval: 30s               # optional; scrape interval (default 30s)
      scrapeTimeout: ""           # optional; per-scrape timeout
      labels:                     # optional; match your Prometheus serviceMonitorSelector
        release: prometheus
  tls:
    enabled: true
    allowUnencrypted: false      # set to true to keep port 6379 open alongside TLS port 16379
    unifiedCertificate: false    # set to true so Valkey and Sentinel share one TLS Secret covering
                                 # both sets of hostnames (avoids TLS verify errors with go-redis
                                 # Sentinel mode); under cert-manager, the legacy
                                 # <name>-sentinel-tls Cert/Secret is migrated automatically
    certManager:
      issuer:
        # group: cert-manager.io
        kind: ClusterIssuer
        name: cluster-ca
  podDisruptionBudget:
    enabled: true            # opt-in; no PDBs are created when the block is absent
    maxUnavailable: 1        # optional, default 1; data StatefulSet only
                             # Sentinel PDB is quorum-derived (minAvailable =
                             # floor(replicas/2)+1) and not configurable;
                             # StatefulSets with < 2 replicas get no PDB
  antiAffinity:
    mode: soft               # optional, default off (no term - upgrades change
                             # nothing); soft = scheduler preference, never blocks;
                             # hard = required spread, surplus pods Pending
    topologyKey: kubernetes.io/hostname  # optional, default kubernetes.io/hostname
                             # applies to data and sentinel pods, each repelling only
                             # its own kind; StatefulSets with < 2 replicas get no term
  podSecurity:               # optional; data, Sentinel and observer pods - ADR 0033
    seccompProfile:
      type: RuntimeDefault   # default; RuntimeDefault | Localhost, Unconfined is refused
      # localhostProfile: profiles/valkey.json  # required for Localhost, forbidden otherwise;
                             # relative, no '..'; the workloads are written only if the
                             # operator's allow-list names this exact path (default
                             # empty: every Localhost profile refused) - ADR 0033 D9
    userNamespaces: false    # default; true = hostUsers: false, needs node support
                             # (Kubernetes 1.33, containerd 2.0 / CRI-O 1.25, Linux 6.3,
                             # no NFS data volume); changing the effective profile or
                             # this flag rolls the data and Sentinel tiers (an explicit
                             # RuntimeDefault is no change)
  networkPolicy:
    enabled: true
    namePrefix: "my-prefix"
  persistence:
    enabled: true    # volumeClaimTemplates are immutable: toggling this on an existing
                     # cluster blocks reconciliation until the StatefulSet is recreated
                     # by hand, which is a rebuild and does not preserve the dataset; a
                     # changed size/storageClass is reported, never applied - ADR 0023
    mode: rdb        # rdb | aof | both
    storageClass: ""
    size: 1Gi
  podLabels:
    app: valkey
  podAnnotations:
    example.com/annotation: "true"
  resources:
    limits:
      cpu: "500m"
      memory: "512Mi"
    requests:
      cpu: "250m"
      memory: "256Mi"
```

### Example CRD (Standalone)

```yaml
apiVersion: vko.gtrfc.com/v1
kind: Valkey
metadata:
  name: standalone
spec:
  replicas: 1
  image: valkey/valkey:8.0
```

### Common Labels

```
app.kubernetes.io/component: valkey | sentinel
app.kubernetes.io/instance: metadata.name
app.kubernetes.io/managed-by: vko.gtrfc.com
app.kubernetes.io/name: valkey
app.kubernetes.io/version: <valkey-image-version>
vko.gtrfc.com/cluster: <cluster-name>
vko.gtrfc.com/instanceName: <name of the pod>
vko.gtrfc.com/instanceRole: <replica | master>
```

### Status

The CRD status must be visible in Lens and show the current operator task per instance:
- `OK` when the instance is healthy
- A short description of the current task otherwise (e.g., `Rolling Update 2/3`,
  `Sentinel Rolling Update 1/3`, `Syncing`, `Failover in progress`)

**With one exception, and it is deliberate:** while a managed write is being refused — by the
API server, an admission webhook, or the operator itself for a `Localhost` seccomp profile
outside its allow-list (`SeccompProfileNotAllowed`) — or is stored without a field the spec
asks for (`UserNamespacesUnsupported`: the API server dropped `hostUsers` without an error),
the phase reports `Error` even on a perfectly healthy cluster, because a spec the operator
accepted and cannot apply has to be visible. `phase` therefore carries two meanings and the
blocked pass wins the field; the **`Ready` condition** carries only the data-plane verdict
and stays `True`. That pair is not a contradiction — it reads as "your cluster is serving,
and the operator cannot write something".
→ [ADR 0002](docs/adr/0002-surface-a-blocked-reconcile-on-the-cr.md) D3, D5, D5a, D12;
[ADR 0033](docs/adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md) D3, D9

## Testing

- **Unit tests**: High coverage for all reconciliation logic
- **Integration tests** (`test/integration`, envtest): cover what only a real API server decides —
  CRD defaulting, delete preconditions, controller-manager wiring. envtest starts a
  kube-apiserver and etcd and **no kubelet**, so no pod runs there and nothing in this tier
  opens a Valkey connection.
- **E2E tests**: rolling updates, failover and recovery against real Valkey instances. This is
  the tier that **must write actual values into Valkey and verify replication reaches the
  replicas**, and the only one that can.

Tier responsibilities, the verification rules (mutation and revert checks, what may be an e2e
and what may not) and the CI matrix: [ADR 0017](docs/adr/0017-test-and-ci-policy.md).

**A fixture that manipulates a pod the operator is concurrently replacing names the pod by
identity, never by controller state.** A rolling-update state annotation names a phase, and a
phase outlives the pod it is about: the abandon e2e jammed pod-0 on "state is one of three
values and pod-0 answers `role:slave`", which the *outgoing* master also satisfies for the one
second between the demote and the delete — runtime `CONFIG SET` dies with that pod, and the
test failed on four runs in ten days, each on a different leg. Again on 2026-09-26, in the shape
`waitForPodRecreated` was written for on 2026-08-22 (`TestE2E_NoSentinel_MasterKill_NoSplitBrain`,
`cea8222`): `TestE2E_SidecarFailoverDrainMaster` waited, after deleting the master, on
conditions the terminating old master already met (kubelet keeps it Ready, ADR 0026) — vacuous
in 5 of 10 green runs of it alone on Kind, red once in a Valkey 9 suite by reading `DBSIZE 0`
from the empty replacement (inferred from the test code and timing; that run's pod logs were
lost) — and now waits for the new UID (`waitForPodRecreated`, 8/8 green alone on Valkey 9); the
sites of the same shape, not yet audited, are T34. Use the image, the UID or
`deletionTimestamp`, and read the effect back rather than trusting the write. Endpoint
membership is read through `discovery.k8s.io/v1` EndpointSlice (`readyEndpointPodNames`); `v1
Endpoints` is deprecated since 1.33 and the operator never used it.
→ [ADR 0017](docs/adr/0017-test-and-ci-policy.md) D50, D51

### Makefile as Entry Point

Always use Makefile targets to run tests, linting, and analysis. Never invoke Go test commands or tools directly. The CI pipeline relies on the same targets.

| Task                          | Makefile Target                |
|-------------------------------|--------------------------------|
| Unit tests                    | `make test-unit`               |
| Unit tests with coverage      | `make test-unit-coverage`      |
| Integration tests             | `make test-integration`        |
| Integration tests w/ coverage | `make test-integration-coverage` |
| E2E tests                     | `make test-e2e`                |
| Valkey image tool check       | `make test-image-tools`        |
| Release tooling check         | `make test-release-tooling`    |
| Full E2E local (Kind)         | `make e2e-local`               |
| All tests with coverage       | `make test`                    |
| Linting                       | `make lint`                    |
| Lint with auto-fix            | `make lint-fix`                |
| Security scan (GoSec)         | `make gosec`                   |
| Vulnerability check           | `make vuln`                    |
| Cyclomatic complexity check   | `make cyclo`                   |
| Cyclomatic complexity report  | `make cyclo-report`            |
| Format code                   | `make fmt`                     |
| Vet code                      | `make vet`                     |
| Build operator binary         | `make build`                   |
| Build Docker image            | `make docker-build`            |
| Build & load into Kind        | `make kind-load`               |

Every pinned tool installs into `bin/` and is invoked by its path, never by bare name — probing
`PATH` while installing into `GOPATH/bin` made `make cyclo`, `make gosec` and `make vuln` fail
with `command not found` on any machine without that directory on `PATH`. A tool path or
version variable must stay **above** the first target naming it: prerequisites expand when the
rule is read, so a late definition silently drops the dependency. *(2026-09-26)* **The path
carries the version** (`bin/controller-gen-v0.22.0`): `go-install-tool` installs only a missing
file, so an unversioned `bin/controller-gen` v0.21.0 outlived the bump to v0.22.0, stamped
v0.21.0 into the CRDs and turned `Generated Manifests Up To Date` red on `e2ce8bb`.
→ [ADR 0017](docs/adr/0017-test-and-ci-policy.md) D49

### No `-short`, no `testing.Short()` gates

The unit targets deliberately do **not** pass `-short`, and no test in this
repo may gate itself behind `testing.Short()`. Both rules exist because the
combination silently removed eight `internal/controller` tests from CI, three
of which had been failing unnoticed. Unit tests reach no real Valkey:
`newTestReconciler` redirects every client to `127.0.0.1` for an instant
refusal, and tests that need a command to succeed use `fakeValkeyServer(t)`
(`internal/controller/manual_failover_known_master_test.go`) via
`NewValkeyClientFn`. There is no runtime left to save by skipping.

The full verification policy — mutation and revert checks, what may be an e2e and what may
not, fixture rules, coverage boundaries — is
[ADR 0017](docs/adr/0017-test-and-ci-policy.md).

### The Valkey image is a dependency, pinned in one place

The operator runs shell **inside** the upstream Valkey image: the init container scripts (the
config writers of both tiers, and ADR 0032's `check-data-writable` pre-flight and
`fix-data-ownership` repair), the auth-wrapped container command, the exec probes and the drain
preStop hook. What those execute is declared in `RequiredImageTools`
([`internal/builder/image_requirements.go`](internal/builder/image_requirements.go)) and
checked against the real images by `make test-image-tools` — docker, no cluster, its own CI
job — which since ADR 0032 also runs `valkey-server` and `valkey-sentinel` (on a hand-written
config), the generated probe, drain hook, pre-flight and repair under the rootless posture
([`test/imagetools/restricted_runtime_test.go`](test/imagetools/restricted_runtime_test.go));
the config-writer scripts are not run there.
**A new tool in a generated script needs a line in that list**; a unit test walks the
generated scripts and fails otherwise, and the converse test fails on a declared tool nothing
uses any more.

Both images live in [`test/testimages`](test/testimages/images.go): the current Valkey 9
release is the default for every suite, the current Valkey 8 release is the second e2e leg and
the start of every upgrade the suite performs. Renovate maintains both and is capped per major,
so crossing to a future major is a decision, not an arriving PR. **Do not copy a pin anywhere
else** — CI passes `E2E_VALKEY_LINE=8`, a selector, and an unrecognised value panics rather than
falling back. Only e2e is pinned; unit and integration never pull an image, so their image
strings are fixtures.
→ [ADR 0017](docs/adr/0017-test-and-ci-policy.md) D42, D43

### The release tooling is tested in PR CI, against the committed lockfile

The npm dependency set behind semantic-release broke twice without a PR ever going red: the
conventionalcommits preset v10 rendered header-only release notes for two months silently,
then 10.4.0 failed every release hard — both only visible on main, because only the release
job installs npm dependencies. `make test-release-tooling`
([`hack/verify-release-tooling.mjs`](hack/verify-release-tooling.mjs)) renders release notes
through the plugin config in `.releaserc.json` and fails on a throw **and** on silently
missing sections; the `release-tooling` CI job runs it on every PR, and the `semantic-release`
job depends on it. `package-lock.json` is committed, both jobs use `npm ci`, and the preset
stays on the 9.x line until `@semantic-release/release-notes-generator` ships
conventional-changelog-writer@9 — a red Renovate PR for preset 10.x is the signal that
upstream is still incompatible.
→ [ADR 0017](docs/adr/0017-test-and-ci-policy.md) D46

### A gate job that is not required is not a gate

`main` was red for 15 days and seven automerges rode over it, because
`Generated Manifests Up To Date` was a CI job and not a *required status check*: a
controller-tools bump stamped a new version into the CRD annotation, nothing regenerated, and
Renovate merged that PR and every one after it. Four of the twelve gate jobs had never been
required at all. **Adding a job that can fail the build means adding it to branch protection in
the same change** — the twelve required contexts are enumerated in the ADR, and nothing in this
repository checks that the list is still complete. The matrix legs are never required by name;
`e2e-gate` ("E2E Tests") is the only E2E context.

The same automerge path broke the build from the other side: `k8s.io/kube-openapi` is
pseudo-versioned with no release branches, and advancing it past the commit where it swapped
`structured-merge-diff/v6` for `/v7` made every package-loading job die inside
`k8s.io/apimachinery`. **It and `sigs.k8s.io/structured-merge-diff` are disabled in
`renovate.json` and taken from `apimachinery` by MVS** — they still move with the
`k8s-go-modules` group, which is the only version of them that was ever supported.
→ [ADR 0017](docs/adr/0017-test-and-ci-policy.md) D47, D48

### E2E cluster topology

CI runs the E2E job three times, as a matrix in `.github/workflows/release.yml`:

| Leg                   | Cluster                    | Valkey line              | Scope                                             |
|-----------------------|----------------------------|--------------------------|---------------------------------------------------|
| `single-node-valkey9` | control-plane only         | `E2E_VALKEY_LINE=9`      | full suite (`make test-e2e`)                      |
| `multi-node-valkey9`  | control-plane + 3 workers  | `E2E_VALKEY_LINE=9`      | `make test-e2e E2E_RUN='TestE2E_AntiAffinity\|TestE2E_PodDisruptionBudget'` |
| `single-node-valkey8` | control-plane only         | `E2E_VALKEY_LINE=8`      | full suite                                        |

Every leg names the line it runs and passes it explicitly. An empty selector
resolves to whatever the default is, so a leg carrying one would keep its name
and quietly change what it tests the day the default moves - and crossing a
major is meant to be a decision, not an arriving PR (ADR 0017 D43).

The multi-node leg exists because two behaviors are meaningless on one node:
eviction serialization and hard-mode anti-affinity spread. Three
workers, not two: Kind keeps the control-plane `NoSchedule` taint on multi-node
clusters, so spreading three replicas needs three schedulable workers.

- `E2E_RUN` narrows `make test-e2e` to matching test names; empty runs everything.
- `E2E_REQUIRE_MULTI_NODE=true` turns the "fewer than 3 schedulable nodes" skip in
  `test/e2e/affinity_test.go` into a failure, so a cluster that came up smaller
  than requested cannot pass as a green skip. The multi-node leg sets it, and it
  additionally greps the test output to prove both scenarios actually ran.
- `E2E_REQUIRE_USER_NAMESPACES=true` turns the "this node cannot start a pod with
  `hostUsers: false`" skip in `test/e2e/pod_hardening_test.go` into a failure. **No CI leg sets
  it, because none can**: the legs run Kind inside Docker-in-Docker with containerd's `native`
  snapshotter, where such a pod fails with "container ID … cannot be mapped to a host ID"
  (measured 2026-09-26 with the CI Kind config), or already at its sandbox
  (`FailedCreatePodSandBox`, seen only as an Event — CI logs). A probe pod decides, reading its
  container states and its Warning Events; without support the
  hardening e2e moves the cluster without the user namespace and skips only those assertions.
  The user-namespace half runs on a local Kind cluster (`make kind-create`, overlayfs).
- Locally: `make kind-create` already builds control-plane + 3 workers, so
  `make e2e-local` covers both.

Rationale and the rest of the CI policy: [ADR 0017](docs/adr/0017-test-and-ci-policy.md).

### RBAC lives in three places — keep them in sync

The kubebuilder markers in `internal/controller/valkey_controller.go` generate
`config/rbac/role.yaml` (`make manifests`), but the ClusterRole that actually reaches users is
the hand-maintained `deploy/helm/valkey-operator/templates/clusterrole.yaml`.
**A new marker needs the chart rule in the same change**, plus an entry in
`SECURITY_ARCHITECTURE.md`. `TestHelmClusterRoleCoversGeneratedRole`
(`internal/controller/rbac_drift_test.go`) asserts generated ⊆ chart and names the missing
triple; the `generated-manifests` CI job covers the half it cannot see by running
`make generate-all` and failing on a dirty tree.

Why it is a test and not a convention, what "legal drift" means, and the one supported
upgrade path: [ADR 0014](docs/adr/0014-rbac-lives-in-three-places.md). The privilege footprint
itself — every rule, what it permits, the hardening checklist — is
[`SECURITY_ARCHITECTURE.md`](SECURITY_ARCHITECTURE.md) and
[ADR 0013](docs/adr/0013-operator-is-cluster-wide-privileged.md).

## Rolling Update Strategy

1. Replace replica pods one by one
2. Verify new pod joins cluster and is seen by other instances
3. Wait for replication sync to complete
4. After 2 replicas are migrated: initiate controlled leader failover
5. Verify failover succeeded
6. Replace last pod (former master)

The data StatefulSet uses `updateStrategy: OnDelete` and `podManagementPolicy: Parallel`, so
pod replacement is the operator's job, not the StatefulSet controller's — which is also why a
PodDisruptionBudget never constrains it. The rolling update compares pods against the
**persisted StatefulSet template**, never against the CR, so a rejected StatefulSet write
cannot turn an image change into a pod-delete loop.

**"Synced" in step 3 and 4 is the full replication answer** — role, `master_link_status:up`
and no sync in progress (`replicationNotEstablishedReason`), never the sync flag alone: a
replica whose link is still connecting reports `master_sync_in_progress:0` while holding
nothing, and step 4 is followed by the delete of the outgoing master. Zero WAIT
acknowledgements is not a partial acknowledgement, and a promotion candidate holding no keys
while the master holds some is refused (`verifyPromotionCandidateHoldsData`). Every one of
these waits is bounded by `spec.rollingUpdate.syncTimeout` and pauses the update rather than
promoting.
→ [ADR 0007](docs/adr/0007-failover-aware-rolling-update.md) D10

**Completion is reported per tier.** `RollingUpdateComplete` means the data tier and fires
before the first Sentinel pod is replaced — except in the pass where a data roll *pauses*
(`pauseRollingUpdate` returns no requeue), which releases the Sentinel roll; a known exception,
ADR 0026 D11. The Sentinel tier rolls afterwards, carries the
`SentinelUpdatePending` condition while it does (phase `Sentinel Rolling Update i/n`), and
emits `SentinelUpdateComplete` exactly when that condition flips back to False. Anything
sequencing on "the update is finished" on a sentinel-enabled cluster waits for the Sentinel
marker, not the data one.

**A tier of one or two Sentinels rolls serially** (decided 2026-09-26). Its quorum
`replicas/2+1` equals its size, so the quorum guard never let a Ready outdated Sentinel go: the
roll requeued unbounded, the pass ended before the status write, and no Sentinel change — image,
TLS rotation, the rootless posture — ever reached that tier. `sentinelDeleteKeepsVotes` keeps the
quorum guard for three or more and asks a smaller tier for every *other* Sentinel available
instead: one at a time, delete gate unchanged, a non-voting target still exempt. The cost is
automatic failover for the seconds one Sentinel restarts, which is what any single Sentinel
failure already costs a tier sized to tolerate none.
→ [ADR 0024](docs/adr/0024-the-sentinel-tier-reports-its-own-completion.md) (D10 for the small
tier)

## A Warning named split-brain means one that did not resolve itself

Two pods answering `master` is the **design** of every controlled failover — the promoted pod
has taken `REPLICAOF NO ONE` and the outgoing one answers until it terminates. The level is a
condition (`MultipleMasters`, True from the first pass, message naming the pods and the
authority); the `SplitBrainDetected` **Warning** is the edge where that level outlived
`splitBrainWarnAfter` = 90 s — above the 75 s `terminationGracePeriodSeconds` and the 60 s
drain preStop hook, below `finalizationStallTimeout`. `SplitBrainResolved` is Normal: it
reports a repair that succeeded.

The deadline lives in the condition's `LastTransitionTime` and its *reason* remembers whether
the Warning already fired — no annotation. **`detectAndResolveSplitBrain` reports nothing**;
the reporting wrapper is `resolveSplitBrain`, and the condition is written at its call sites
because `writeStatusCondition` re-`Get`s the CR. **An unreachable pod carrying a
`DeletionTimestamp` is not a master**: nothing clears the `instanceRole` label at delete time,
so the label used to resurrect the pod the operator had just demoted and deleted. A clean
rolling update emits **zero** Warning Events on either topology, and an e2e subtest per
topology says so. **While the roll's own Sentinel failover is in flight
(`failover-triggered`), the double master is reported and not resolved**
(`resolveSplitBrainUnlessFailingOver`, ADR 0025 D9, decided 2026-09-26): Sentinel's master
pointer names the old master until `+switch-master`, and resolving in that window demoted the
replica Sentinel was promoting — measured as a reset-and-retrigger loop ~~of over ten minutes~~
*(corrected 2026-09-26 against the operator log, as in ADR 0025: ten cycles on Valkey 8, until
the test's ten-minute wait gave up)* on an observer-enabled cluster. **The window carries its own
clock** (`ownFailoverInFlight`): the state **and** a failover timestamp younger than 90 s; a
state without a timestamp is no window. `setFailoverTriggered` writes state and timestamp in one
update at both trigger sites — with a second write that fails, the first trigger was left with
no stamp (a state that never expires) and a retrigger with the reset's stale one (timeouts that
fire early), ADR 0010 D14; one test per site. ~~The final e2e runs of 2026-09-26 logged~~ *(corrected 2026-09-26: those
were the runs with the guard, before its clock and the one-update arm, not the final one)* The
e2e runs of 2026-09-26 with the guard logged, on that cluster `hard`, 4 failover triggers (one
per run of the test), 0 demotions and 0 timeouts — 11, 11 and 9 in the run before D9, both legs
together.
→ [ADR 0025](docs/adr/0025-a-split-brain-warning-means-one-that-did-not-resolve-itself.md)

## Every condition is a level, an edge or history

Adding a status condition means adding a row to `conditionRegistry`
([`internal/controller/condition_registry.go`](internal/controller/condition_registry.go)) —
the unit tier goes red otherwise, because the guard parses every `ConditionType` out of
`api/v1` and demands exactly one row each. The row declares which of three things the
condition is, and that decides what it owes: a **level** is re-measured every pass and owes
exactly one evaluator; an **edge** records something and owes a clear at a site that
*proves* the precondition is gone, plus a presence guard; **history** is a verdict about a
completed operation and must never gain a clear.

A **level** may have more than one evaluator only with a declared `ownershipRule` naming
which site decides — the loosening `StorageSpecNotApplied` earned when its two StatefulSet
reconcilers stopped racing: either tier may report a claim conflict, only the data tier may
clear one, and that holds only while the data step runs first
([ADR 0023](docs/adr/0023-volume-claim-templates-are-immutable.md) D4a).

It is a test and not a convention because the convention was missed four times — the clear
kept ending up behind the very code path whose absence caused the staleness — and writing
the table down for the first time immediately found two more (`RollingUpdatePaused` on
non-Sentinel topologies, `StorageSpecNotApplied` with two evaluators), both declared in the
registry with their ticket reference rather than silently carried, and **both fixed on
2026-08-26** — one by narrowing an evaluator, one by moving the clear up one frame to the
two sites every dispatch target reaches and deleting the unguarded `False` write that had
stamped the condition onto the whole Sentinel fleet
([ADR 0002](docs/adr/0002-surface-a-blocked-reconcile-on-the-cr.md) D10b). The one gap left
is `Ready`/T18, an open re-decision rather than a defect. **There is deliberately no
central condition-GC pass**: the producer stays the one reporter, and a sweep would have to
exclude `MultipleMasters` (a flip resets the `splitBrainWarnAfter` deadline) and
`TopologyRestored` (history) on its first two rows. No condition is ever deleted, which is
why the presence guard is the whole upgrade-neutrality story.
→ [ADR 0027](docs/adr/0027-conditions-are-levels-edges-or-history.md)

**`Ready` reports the data plane; `status.phase` also reports whether the operator can
converge the spec.** On a blocked-but-healthy cluster the two disagree by design, and
`Ready=True` next to `phase=Error` means "your cluster is serving, and the operator cannot
write something" — read `status.message` and `ReconcileBlocked` for what. A status field that
only *it* can change needs its assignment on the far side of the `prevStatus` capture, next
to `OperatorVersion`: `observerReady` sat on the near side and was therefore compared against
itself, frozen at whatever the last pass that changed something else had sampled.
→ [ADR 0002](docs/adr/0002-surface-a-blocked-reconcile-on-the-cr.md) D5, D5a

## A pod being deleted is not available

kubelet keeps `PodReady=True` for the **whole termination** of a pod whose readiness probe
still passes — measured on Kubernetes 1.36.1, no flip, right up to the moment the object is
gone. `podState.ready` is therefore renamed `readyCondition` and read only through two
accessors: **`available()` = Ready and not being deleted is the default**, and every site that
*spends* a pod (deletes, promotes, counts toward a quorum or a completion) uses it — except the
replacement of an outdated pod, below; `reachable()` = Ready alone is the carve-out for the four
sites that only *talk* to a pod, of which `demoteRogueMaster` is the load-bearing one — refusing
to demote a terminating master would leave it accepting writes for the rest of its termination.
**The rule is the rename, not a list of sites**: it had been stated as a list three times and
been incomplete every time.

On top of it one invariant: **the operator never deletes a pod of a tier while any pod of that
tier is terminating.** The gate sits immediately in front of each `deleteOwnedPod` and never at
a function head; "the tier" is the ordinal range `[0, *sts.Spec.Replicas)`, never a
label-selector List. The refusal is never resumed — the *observation* of it is bounded, by the
pod's own `deletionTimestamp` (which the API server sets to `now + gracePeriodSeconds`, so
`time.Since` of it is the overrun) rather than by `ensureWaitBound`. Past
`podTerminationOverrun` = 2 min the pass stops ending on the wait and reports
`PodTerminationStalled`, so the no-master recovery, the steady-state split-brain check and the
status write run again — on a Sentinel cluster the status write alone. **The Sentinel roll is
not among them: a holding data tier holds it**, for all three stall conditions
(`PodTerminationStalled`, `PodRecreationStalled`, `PodAvailabilityStalled`), because the tiers
share `spec.image` and a released Sentinel roll takes a healthy Sentinel onto the spec the data
tier is stuck on and spends the spare vote. One known exception, a residual risk and not a rule:
a data roll that *pauses* (`pauseRollingUpdate` returns no requeue) is not holding, so the pass
that pauses runs the Sentinel roll. **No Event on any of it** — ADR 0025 D7 still
promises zero Warnings on a clean roll. `countUpdatedPods` deliberately still counts a
terminating pod; the completion hold lives in `finalizeRollingUpdate`, Sentinel path only.

**An outdated pod is replaced, not waited for.** The three roll delete sites — the standalone
delete, `replaceNextReplica`, `replaceRemainingPods` — ask of the pod they delete only whether it
is terminating (`terminationWait`); its readiness is not asked. The tier gate above and the
preconditions each site already had stay (`verifyReplacedReplicasSynced`,
`verifyNewMasterReady` — which reads the new master's `DBSIZE` but does not refuse on it, a
pre-existing gap T32 does not close). The old wait was justified as "recently replaced", which
no outdated pod ever is, and after a spec fix the replacement that never came up *is* the next
candidate, so the roll waited for it forever. The Sentinel roll
deletes an unavailable outdated pod ahead of `firstOutdatedPod`, and its quorum guard
(`sentinelDeleteKeepsVotes`, serial on one or two Sentinels — ADR 0024 D10) applies
only to a delete that spends a vote (`cost > 0`): with the quorum already lost — two of three
Sentinels stuck on the broken spec, `readyCount` 1 — a non-voting outdated pod is still
replaced, and the delete gate still serialises those deletes. `deleteNextPendingPod` keeps
`available()`, and a pod on the current template is only ever waited on — deleting it brings it
back identical.

**Every remaining wait on a pod that exists, is not terminating and is not available is
bounded** (`availabilityWait`): budget `spec.rollingUpdate.syncTimeout`, clock the pod's own
(`podNotReadySince`: `Ready.lastTransitionTime`, else `creationTimestamp` — nothing armed, no
annotation). A Ready=False kubelet stamped at its first status sync (`stampedAtFirstSync`:
`lastTransitionTime` no later than `status.startTime` + `firstSyncSlack` = 5 s) is not a transition — that pod was never
Ready and keeps `creationTimestamp`, or a pod Pending past the budget would restart its clock
when scheduled. Past the budget the pass continues and `PodAvailabilityStalled` is reported — a
**level** with two evaluators, one per tier, the tier in the reason (`ValkeyPodNotAvailable`,
`SentinelPodNotAvailable`). **Each tier retracts only its own report, and only on evidence**:
`expiredUnavailablePod` must find no pod of the tier that exists, is not terminating and has
been not-Ready past the budget. A pass that stopped at another wait first measured nothing, and
retracting on its silence made the condition flap. One condition for two tiers is accepted: a
data report overwrites a standing Sentinel one, which returns on the first Sentinel pass after
the data tier finishes. The wait writes nothing; the tier's wrapper
(`checkAndHandleRollingUpdate`, `checkAndHandleSentinelRollingUpdate`) does. No Event.
→ [ADR 0026](docs/adr/0026-a-pod-being-deleted-is-not-available.md) (D11 for the availability
half), [ADR 0010](docs/adr/0010-every-rolling-update-wait-is-bounded.md) D17

## Reconcile concurrency

The operator reconciles **4 Valkey CRs at a time** (`--max-concurrent-reconciles`, chart value
`maxConcurrentReconciles`); passes for the *same* CR stay serialised by the work queue at any
value. Concurrency is only safe because no reconciler state is fleet-wide — the `nudgeTracker`
keys carry namespace and CR name, the blocked-pass marker rides on the context, there is no
package-level mutable state, and every managed object name contains the CR name. **That is a
standing constraint on new code**, not a one-time audit. The same ADR carries the second half:
`findMaster` probes pods concurrently and collects them indexed by ordinal, so the answer never
depends on which pod replied first.
→ [ADR 0019](docs/adr/0019-reconcile-concurrency-and-the-cost-of-a-stuck-pass.md)

## The non-Sentinel master authority, in six rules

Without Sentinel nothing external arbitrates who the master is, and every mistake in this area
is a `REPLICAOF` that discards a dataset. Six ADRs carry the design; the load-bearing
sentences are repeated here so nothing is changed without them.

1. **`vko.gtrfc.com/known-master` is the operator's recorded master authority.** It feeds the
   `replicaof` directive of the replica ConfigMap, is deliberately excluded from the config
   hash, and is read by three consumers: the init container, the rolling-update split-brain
   resolver and the steady-state check. A non-pod-0 master is a supported end state — the
   `-rw`/`-r` Services select on `instanceRole`, never on ordinal.
   → [ADR 0008](docs/adr/0008-known-master-annotation-is-the-recorded-authority.md)
2. **A promotion the operator could not record is not a completed promotion.** Every write
   that records a promotion is *part of* the promotion: it retries where retrying helps, and
   on failure it fails the pass rather than letting the promotion stand unrecorded. Do not
   relax any of them back to `_ = r.Update(...)`.
   → [ADR 0009](docs/adr/0009-an-unrecorded-promotion-is-not-a-promotion.md)
3. **Every rolling-update wait is bounded, and expiry hands over to another bounded state** —
   never to a cleared rolling-update state, because once the state annotation is gone nothing
   calls `detectAndResolveSplitBrain` again. A bound that can silently fail to arm is not a
   bound.
   → [ADR 0010](docs/adr/0010-every-rolling-update-wait-is-bounded.md)
4. **In steady state the annotation is a tie-breaker among multiple masters; it never
   overrules a single, undisputed one.** Adoption requires evidence — the drain stamp, the
   structural rule, or the recorded pod answering that it is no longer master. **Pod creation
   order may only ever REFUSE a demotion, never grant an adoption.** The normative decision
   table lives in the ADR.
   → [ADR 0011](docs/adr/0011-evidence-based-steady-state-split-brain-resolution.md)
5. **The sidecar has no CR access and records its drain promotion on the pod**
   (`vko.gtrfc.com/drain-promoted-at`), which is why the operator has to reason from evidence
   at all. The labeler exits before the drain handler runs, so exactly one pod carries the
   master label during a drain. **The drain needs the local Valkey alive**, and the kubelet
   gives no ordering between the two SIGTERMs, so a `preStop` hook on the Valkey container of
   multi-replica non-Sentinel clusters waits for `/var/run/vko/drain-complete`. Anything added
   to `Handle` inherits that contract: every exit path releases the marker, or every pod
   deletion in the fleet pays the 60 s bound.
   → [ADR 0012](docs/adr/0012-the-sidecar-records-its-drain-promotion-on-the-pod.md)
6. **A demotion may not discard the only dataset.** The rolling-update resolver stopped
   trusting the named authority unconditionally: an authority holding zero keys while the rogue
   holds some ends that demotion, and an unreadable count is a refusal, not a demotion. Both
   empty is not a refusal. **Inside a roll only two signals discriminate — the drain stamp and
   the dataset** — because every structural or temporal signal is a state the operator itself
   produces: the pod it just promoted is legitimately the younger object, and the replica it is
   about to demote legitimately could not have self-elected. One stamped master is adopted and
   recorded before anything is demoted; two are ambiguous and demote nobody. A refusal emits no
   Event — `MultipleMasters` and the 90 s `SplitBrainDetected` already carry it — and it
   deliberately re-enters the deadlock the resolver exists to break, which is safe only because
   every state that names an authority is bounded.
   → [ADR 0028](docs/adr/0028-a-demotion-may-not-discard-the-only-dataset.md)

## Provenance before every write and every delete

Every object the operator manages is named from the CR name, and whoever may `create valkeys`
in a namespace picks that name. **No write and no delete onto a generated name without
`metav1.IsControlledBy(obj, v)` first** — a label is not a proof, and an ownerReference is a
write like any other, the one that decides whether the garbage collector takes the object
with the CR. Since 2026-08-22 the rule binds *every* managed kind, so **a new managed object
inherits it, not an exemption**: guard the write, decide the fail direction by "can the CR do
the job it was asked to do", give the kind its own Event reason, and guard the delete with the
UID precondition (`deleteIfOwned`). If a second code path reads or acts on the object, that
path treats a foreign one as absent and stays quiet — the reconciler is the one reporter.

**Pods are the exception to who the controller is, not to the rule.** The StatefulSet creates
them, so the proof is two-hop: `podIsOurs(pod, sts)` against a StatefulSet already proven,
never a label and never a name. It binds touching a pod, deleting one, and putting its name
into the sidecar Role — that grant follows the name of the *object*, so an unfiltered pod hands
this cluster's sidecar `patch` on a stranger's pod.
→ [ADR 0020](docs/adr/0020-write-only-what-the-operator-owns.md) (writes, grants and pods),
[ADR 0006](docs/adr/0006-delete-only-what-the-operator-owns.md) (deletes)

## Sentinel identity is pinned to the pod

Sentinel never forgets a peer it has seen, and a failover leader needs a majority of that
whole table. Because the Sentinel config lives on an `emptyDir`, a replacement pod used to
boot with a fresh `sentinel myid` and a new IP, so every survivor recorded it next to the
dead one — measured: two live Sentinels with five known peers each never promoted a replica
after the master was killed, where the same topology with clean tables promoted one in under
ten seconds. The init container now derives `sentinel myid` from the pod hostname, so the
ordinal *is* the identity and peers switch the address instead of adding a voter. **A missing
`HOSTNAME` falls back to Sentinel's own random id on purpose** — one shared id across the
tier is worse than the drift.

**The operator never issues `SENTINEL RESET` itself.** A reset rebuilds that Sentinel's peer
and replica tables through the master, which is harmless with a healthy master and
unrecoverable without one. Drift is *reported* as the `SentinelPeersStale` condition, read
from the `SENTINEL MASTER` reply the health pass already asks for, and cleared by an operator
or by the next Sentinel roll.
→ [ADR 0022](docs/adr/0022-sentinel-identity-is-pinned-to-the-pod.md)

## Rotating certificates rotate the instances that cannot reload them

A Secret volume is rewritten in place when cert-manager rotates the certificate it holds, and a
process that parsed the old bytes at startup keeps presenting them until it exits. Measured on
a live fleet: the sidecar labeler, the Sentinel cross-check and the **ADR 0012 drain promotion**
died on every TLS cluster whose pods outlived a rotation - silently, with valid material sitting
in the mount, `Ready` still True and `phase` still `OK`, because the reconciler and the health
checker read the Secret per call and present no client certificate at all.

Two halves, and the split is the rule: **a long-lived process this repo owns re-reads its
material and earns an exemption; every other process rides a roll.**

- The sidecar and the observer take their `*tls.Config` from
  [`internal/tlsmaterial`](internal/tlsmaterial/reloader.go) per dial - CA **and** keypair,
  compared on bytes, keeping the last config that worked so a half-swapped mount costs one
  degraded call instead of an outage. **A new long-lived client of ours inherits that, not an
  exemption**: `valkeyclient.Client` holds no connection, so building one per command is an
  allocation and nothing else.
- Everything else is replaced. The reconciler stamps `VKO_TLS_MATERIAL_HASH` - a
  fingerprint of `ca.crt`/`tls.crt`/`tls.key`, Secret *content* the builders never see - onto
  the carrier container of both StatefulSet pod templates, so a rotation rides the ordinary
  failover-aware rolling update. `valkey-server` and `valkey-sentinel` are treated as pinning because nobody has
  measured otherwise; the third-party exporter provably is, and the restart unit is the pod, so
  one non-reloading container spends the whole pod's exemption. The observer Deployment carries
  no fingerprint and is never restarted for a rotation.

**The trigger is the rotation, not the expiry.** That buys the full 30-day cert-manager grace
window, so the roll is never time-critical and needs neither scheduling nor stampede control -
the ADR 0019 concurrency cap is the only pacing there is, and the shipped
`ValkeyTLSMaterialStale` alert waits **72 h** rather than minutes. The one thing the window does
not cover is a roll that never starts, and that is what the `TLSMaterialStale` level reports.
Upgrade neutrality is the presence guard the other hashes already use: a pod without the
record is never restarted for one.

**The operator never persists a TLS pod template without a material record** (ADR 0030 D12,
closing T27): the Secret fingerprint is stamped when readable, inherited from the persisted
template when it is not — an unreadable Secret never erases a record — and a template with
neither is refused without failing the pass; the Secret watch re-enters it. A fresh TLS
cluster therefore gets its StatefulSet a few seconds after the CR, once cert-manager has
issued, and every pod it ever boots is measurable from birth. The refusal is the fix for the
pods that used to be built from a record-less template and were then exempt from every
rotation forever. `TLSMaterialStale` writes its all-clear only over a complete two-tier
measurement, retracts a standing True when TLS is turned off, and names the record-less
legacy pods a rotation will never replace (`False`/`TLSMaterialUnmeasured` — status- and
alert-neutral, the reason is the signal) instead of absorbing them into the all-clear (T24).

**The ADR debt is paid** (deferred 2026-08-26 as `ADR spaeter, erst Code`, discharged the same
day). One correction the debt note itself got wrong: ADR 0016's residual risk asked whether
**`valkey-server`** reloads, and that is *still* unmeasured — what fired and was measured is the
same shape on the **client** side, ours. ADR 0030 D6 treats an unmeasured process as pinning so
that nobody has to find out, and D11 bounds the content-fingerprint exception to TLS material.
**The security parameter there is the entropy of the input, not the width of the digest** - a
published digest of a private key confirms nothing because nobody enumerates 2048-bit RSA keys,
while a published digest of the auth password is a brute-forceable oracle **at any digest
strength**, since the attacker guesses candidates and hashes them. So **the password rotation
gap stays open and must not be closed by copying this mechanism, and reaching for SHA-256 does
not change that** - five documents used to phrase the refusal as "a 32-bit digest of ...",
corrected 2026-08-27.

**The Secret writer is accepted, permanently.** Whoever can write the TLS Secret can hit the
32-bit digest by search and swap the material silently. A wide cryptographic digest would close
exactly that and was still not taken, because what is left afterwards is a substitution
**indistinguishable from a legitimate rotation** - same roll, same condition transition, no
observer for whom the two differ. Raising the ceiling needs a trust anchor outside the Secret,
not a better hash over it. Two arguments against the strong digest are recorded in ADR 0030 D11
as **not holding**, so they are not reused: writing the previous content back is a denial of
rotation and not a forged fingerprint, and the migration cost is solvable by versioning the
record the same way ADR 0031 D5 already widens the presence rule.

## A record the operator trusts lives in pod spec, and a token goes to one container

Both halves land on 2026-08-27, out of the adversarial review of ADR 0030, and the finding that
reordered the whole option list is that **deleting a record beats forging one**. Every consumer
of a pod-template hash carries a presence rule - a pod with no record is *unmeasured*, which is
the only reason an operator upgrade rolls nothing - so one merge patch setting the key to `null`
switches the roll off. No collision needed, and no digest strength touches it. That kills every
scheme that keeps the carrier in pod `metadata`.

- **The TLS fingerprint moved into the pod spec.** `VKO_TLS_MATERIAL_HASH` on the sidecar
  container (data tier) and the sentinel container (Sentinel tier), stamped *after*
  `BuildStatefulSet` so `ComputePodSpecHash` does not move with it - one rotation, one signal.
  `env` is not in the API server's `updatablePodSpecFields`, so the record is refused to every
  principal including the operator. The superseded `vko.gtrfc.com/tls-material-hash` annotation
  is **read and never written**: the fallback is self-extinguishing and exists because a plain
  upgrade rolls Sentinel pods only when the release changes their pod spec or configuration
  (ADR 0005 D11, narrowed 2026-09-26 — the rootless release of ADR 0032 is one that does, and so,
  by reading, was v1.11.0), so without it that tier would go silently unmeasured. **`config-hash`
  and `pod-spec-hash` are still in metadata** - a filed follow-up, not a decided non-goal.
- **The data pod hands its ServiceAccount token to the sidecar container alone.**
  `automountServiceAccountToken: false` plus a hand-declared projected volume at
  `/var/run/secrets/kubernetes.io/serviceaccount`; the volume name must **not** start with
  `kube-api-access-`, which is the prefix the ServiceAccount admission plugin adopts and mounts
  everywhere. Sentinel pods set the flag and project nothing. The claim that Kubernetes does not
  offer a per-container split - carried in three places in this repository - was **false**; the
  pattern is GA since 1.20. A new container in a data pod inherits no token, and a new
  `AutomountServiceAccountToken` needs its own line in `podSpecChanged`, because the volume list
  carries the introduction but nothing carries a flip back.

→ [ADR 0031](docs/adr/0031-a-record-the-operator-trusts-lives-in-pod-spec.md),
[ADR 0012](docs/adr/0012-the-sidecar-records-its-drain-promotion-on-the-pod.md) D8 step 4,
amending [ADR 0030](docs/adr/0030-rotating-certificates-rotate-the-instances-that-cannot-reload-them.md)
D4, [ADR 0020](docs/adr/0020-write-only-what-the-operator-owns.md) D10 and
[ADR 0007](docs/adr/0007-failover-aware-rolling-update.md) D2, which had never registered
`tlsMaterialHashFromSts` among its inputs.
→ [ADR 0030](docs/adr/0030-rotating-certificates-rotate-the-instances-that-cannot-reload-them.md),
amending [ADR 0016](docs/adr/0016-authentication-and-tls-posture.md) D12 and its cert-manager
residual risk, [ADR 0012](docs/adr/0012-the-sidecar-records-its-drain-promotion-on-the-pod.md)
D11, and `SECURITY_ARCHITECTURE.md` sections 2, 6 and 9.

## Every generated pod runs rootless, with no option

Every container on the Valkey image used to run as uid 0 with the runtime's default
capabilities, because `command:` bypasses the entrypoint that drops to the `valkey` user, and a
namespace enforcing Pod Security `restricted` refused every generated pod. Now data and Sentinel
pods run `runAsNonRoot` as uid/gid 999 with `fsGroup: 999` (`fsGroupChangePolicy` unset =
`Always`) and ~~`seccompProfile: RuntimeDefault`~~ the seccomp profile of `spec.podSecurity`,
`RuntimeDefault` unless set to a `Localhost` profile the operator's allow-list names
*(configurable since 2026-09-26, ADR 0033 D1, D9)*, and
every container and init container — the sidecar and the third-party exporter included — with
`allowPrivilegeEscalation: false`, `readOnlyRootFilesystem: true` and
`capabilities.drop: [ALL]`; the observer gets `runAsNonRoot`, the same seccomp profile and the
same container fields ~~under its image's numeric user~~, pinned to uid/gid/fsGroup 65532
(`OperatorUID`) *(2026-09-26, ADR 0033 D4)*. ~~No CRD field~~ No CRD field that lowers it
*(since 2026-09-26 `spec.podSecurity` exists, ADR 0033: it chooses a seccomp profile and a user
namespace, never root and never `Unconfined`)*, no `baseline` level, no opt-out: root was a
defect, and ADR 0005 D1 governs features, not the repair of one. **The posture is applied by
one walk** (`applyValkeyPodSecurity`, `applyObserverPodSecurity`, last in each builder), so a new container
inherits it by being in the pod — and a `securityContext` a container builder sets is
overwritten.

- **The only root process is the migration-only `fix-data-ownership` repair** (uid 0,
  `drop: [ALL]` + `add: [CHOWN]`, `find /data ! -user 999 -exec chown -h 999:999 {} +`, always
  exit 0), in front of the `check-data-writable` pre-flight that every persistent data pod runs
  and that is the one gate. `WithDataOwnershipRepair` inserts it on the built object **after
  `ComputePodSpecHash`**, so neither the template write that adds it nor the one that removes it
  is itself a roll. On a persistent StatefulSet it
  is added while the live template or a data pod proven ours runs without `runAsNonRoot` — the
  evidence is the persisted template and the immutable pod spec, derived per pass and stored
  nowhere (`dataOwnershipRepairNeeded`) — and **kept until every ordinal holds a pod proven ours,
  rootless and Ready, and no data-tier roll is recorded** (~~rootless and past its pre-flight —
  exited 0, or Ready~~ until the ordering fix of 2026-09-26); a missing pod, or a rootless one
  that never became Ready, is not proof, or the repair drops between the last legacy pod's
  delete and its replacement booting on a root-owned volume. The roll-state half orders the
  second roll behind the first: `reconcileStatefulSet` runs before the rolling update in the same
  pass, and a removal under a recorded roll outdated every pod before that roll finalized, so
  `clearStaleRollingUpdateState` discarded its state — measured on Kind as one
  `RollingUpdateComplete` per persistent tier instead of two. That gate alone then stranded the
  repair: the pass that may remove it is the one after the completion, and a completing pass
  schedules none (generation-gated CR watch, no Pod watch). **`finishDataRoll` therefore asks for
  that pass (`requestRecheck`) while the template still carries the repair** (ADR 0032 D4,
  amended 2026-09-26). It brought `find` and `chown` into `RequiredImageTools`; a new tool in a
  generated script still needs its line there.
- **The repair leaving the template starts a second roll** (decided 2026-09-26; until then this
  section said removing it "rolls nothing"). The pods created during the migration keep it in
  their immutable spec — root on every sandbox restart, a violator in a `restricted` dry-run —
  so `podCarriesRetiredRepair` (pod carries it, persisted template no longer does) makes them
  outdated for that alone. It sits inside `podOutdated`, the one question every data-tier site
  asks (dispatch loop, `collectPodStates`, the standalone handler, the manual-failover master
  check), and the replacement is the ordinary failover-aware roll. A pod missing mid-roll is no
  evidence, so the repair does not come back. Leaving the pods to their next replacement was the
  alternative, and lost. After the second roll no generated pod carries a root container.
- **The single data pod of a `spec.replicas: 1` cluster without Sentinel that still runs as root
  is decided by persistence** (`singlePodDeferral`, read off the persisted StatefulSet), not by
  `isSidecarOnlyChange` (which still decides a rootless one): persistent is replaced at once, the
  repair running on its way up; non-persistent is deferred and reported as
  `PodSecurityUpdatePending=True/PodRunsAsRoot`, because an operator upgrade never discards a
  dataset — unless its Valkey image, TLS material record or config hash changed, which the CR
  author or a rotation caused and which replace it as they always did.
- **The drift comparisons treat `securityContext` as a subset** (`podSpecChanged`,
  `containerChanged`, `ObserverDeploymentHasChanged`): a field the operator does not set is not
  compared, so a mutating admission policy is not fought over — **except `capabilities.add`**,
  where the live template may not add what the desired one does not, or an out-of-band `NET_RAW`
  would never converge back.

The posture is in the pod-spec hash, so this release rolls every multi-replica data tier and
every Sentinel tier once, and every persistent data tier a second time (above) — a persistent
single data pod without Sentinel therefore restarts twice, two short downtimes with the data
kept. A tier of one or two Sentinels, recorded here as open until 2026-09-26 because its roll
could never delete a Ready Sentinel, now rolls serially
([ADR 0024](docs/adr/0024-the-sentinel-tier-reports-its-own-completion.md) D10). A
replacement that never comes up (NFS `root_squash` refusing the `chown`, so the pre-flight fails)
is reported as `PodAvailabilityStalled`, which is why this ships together with ADR 0026 D11.
→ [ADR 0032](docs/adr/0032-generated-pods-run-rootless.md) (D2 for the second roll), amending
[ADR 0005](docs/adr/0005-upgrade-neutral-defaults-and-anti-affinity.md) D1, D7, D11,
[ADR 0007](docs/adr/0007-failover-aware-rolling-update.md) D6, D7,
[ADR 0012](docs/adr/0012-the-sidecar-records-its-drain-promotion-on-the-pod.md) D8 step 4 and
[ADR 0017](docs/adr/0017-test-and-ci-policy.md), superseding
[ADR 0013](docs/adr/0013-operator-is-cluster-wide-privileged.md) D9

## Pod hardening beyond rootless: a seccomp choice, an opt-in user namespace

`spec.podSecurity` reaches the data, Sentinel and observer pods; the chart's `podSecurity`
reaches the operator and its pre-upgrade hook, and neither reaches the other's pods.

- **`Unconfined` cannot be expressed.** `seccompProfile.type` is `RuntimeDefault` (default) or
  `Localhost`, enforced by the CRD enum plus two CEL rules on `SeccompProfileSpec` — one demands
  `localhostProfile` exactly for `Localhost`, the other refuses an absolute path or a `..`
  element — and `GetSeccompProfile` maps anything but `Localhost` to `RuntimeDefault`; for the
  operator's own pods the chart fails the render on any other type, `Localhost` without a path
  and a path without `Localhost`, ~~but not on an absolute path or a `..` element~~ and *(since
  2026-09-26, `valkey-operator.podHardening`)* on an absolute path or a `..` element, and that
  profile is not checked against the allow-list below.
  Every generated pod therefore runs under a seccomp filter and `restricted` stays satisfiable.
- **A `Localhost` profile is default-deny, and one step reports it** (decided 2026-09-26).
  Pod Security `restricted` accepts every `Localhost` profile, and one that allows every syscall
  is as good as no filter, so a `Localhost` profile a CR names is written into a workload only
  if `--allowed-seccomp-localhost-profiles` lists that exact path (chart
  `valkeyPodSecurity.allowedSeccompLocalhostProfiles`, default `[]` = every `Localhost` profile
  refused; `RuntimeDefault` needs no entry). Otherwise `reconcileStatefulSet` returns
  `errSeccompProfileNotAllowed` ~~before it writes anything~~ **at the write**, on create and
  update alike, and is **the one reporter** once it reaches the gate:
  `ReconcileBlocked=True/SeccompProfileNotAllowed`, phase `Error`; the Sentinel and observer
  steps withhold their writes silently, and running pods keep their template. *(Gate moved
  2026-09-26 from the head of the step, where it hid a name collision, froze
  `StorageSpecNotApplied` and skipped the TLS record.)* `seccompProfileAllowed` runs after the
  ownership proof, `guardVolumeClaimTemplates`, `ensureTLSMaterialRecord` and the repair decision
  and before the drift detection (on create: after the TLS record, right before
  `writeWorkload`); the Sentinel and observer steps withhold at the matching point after their
  own proofs. So a foreign StatefulSet is still reported as `ForeignObject`, the
  `StorageSpecNotApplied` level is still re-measured, and a CR whose profile the list no longer
  holds is reported even when its live template has not drifted — the gate asks the spec, not
  the difference. `TestSeccompProfileNotAllowed_GateSitsAtTheWrite` pins the first and the last;
  the claim level rests on the position alone. The chart refuses at render an allow-list entry
  that is empty, starts with `/`, contains a `,` or has a `..` element. A listed profile is
  still node state the operator cannot see: it must exist on every node, allow the chown of
  `fix-data-ownership`, and be as strict as whoever lists it accepts for every Valkey pod.
  *(This replaces the earlier stance that the operator keeps no allow-list and an administrator
  narrows the choice with an admission policy. Documenting the risk only — chosen first,
  reversed the same day — and removing `Localhost` are the alternatives that lost, ADR 0033
  D9.)*
- **A user namespace is opt-in, and a dropped `hostUsers` blocks the pass.**
  `userNamespaces: true` sets `hostUsers: false` (`applyPodHardening`); default off, because a
  node without support never starts the pod. An API server with the `UserNamespacesSupport` gate
  off drops the field **without an error**, so every create and update of the data and Sentinel
  StatefulSets and the observer Deployment goes through `writeWorkload` (the nudge's
  metadata-only merge patch carries no template), which reads the stored template out of the
  write's answer and fails the step: `ReconcileBlocked=True/UserNamespacesUnsupported`, phase
  `Error`, the rest of the template still applied. `hostUsers` is compared **exactly**
  (`podHardeningChanged`), not as a subset, or an opt-out would never converge.
- **A new generated container inherits `privileged: false` by being in the pod** — the ADR 0032
  walk (`restrictContainers` → `restrictedContainerSecurityContext`) sets it, and the repair
  container states its own. Every generated pod also gets `enableServiceLinks: false`;
  `hostNetwork`/`hostPID`/`hostIPC` stay unset because the API cannot carry an explicit false,
  and a unit test fails any builder that sets one.
- **A digest is never a label value.** `ExtractVersionFromImage` returns the tag of
  `repo:tag@sha256:…`, `""` for a digest-only reference, `latest` for a bare repository; before
  this, a digest-pinned `spec.image` produced a 71-character `app.kubernetes.io/version` value
  and could never be deployed. `DefaultMetricsExporterImage` is pinned by digest and
  **not** maintained by Renovate; the chart pins the operator through `image.digest` (default
  empty).
- **Resources:** `spec.sentinel.resources` goes to every Sentinel container, init included, with
  no default; no container gets a new default (the observer keeps its pre-existing 50m/64Mi
  request, `GetObserverResources`), so the sidecar and the data pod's init containers still
  state none and a cpu/memory `ResourceQuota` still refuses the data pods. No AppArmor profile is
  set: an explicit one breaks nodes without AppArmor (read in upstream source, not measured).
- **`make cyclo` ignores `zz_generated`**, as it ignores `_test.go` — controller-gen's
  `(*ValkeySpec).DeepCopyInto` reached 16 with `spec.podSecurity`. Hand-written code stays under
  15, no `nolint` ([ADR 0017](docs/adr/0017-test-and-ci-policy.md) D35, amended 2026-09-26).

~~Unit and integration (envtest 1.29, which measured the silent drop) ran green, as recorded in the
T31 ticket; `TestE2E_PodHardening_UserNamespacesLocalhostSeccompAndDigest`, the fleet-upgrade e2e
and both full suites have **not yet run** with this change.~~ *(Superseded 2026-09-26 by the
runs, on the code before the allow-list and the CEL path rule:)* unit, lint, cyclo and
integration (envtest 1.29, which measured the silent drop) green, 8 of 8 mutations of the
ADR 0033 code killed; on Kind (Kubernetes 1.36.1, containerd 2.3.1,
runc 1.4.2, Linux 6.10) the fleet-upgrade e2e from 1.12.8 green, the full suite 53/53 on
Valkey 8 and 52/53 on Valkey 9 — the one failure the hardening e2e's own `/data` owner
assertion (Kind's hostPath root is root-owned `0777`, and a cluster this operator built never
ran the repair); it now compares the root owner before and after the move and passed on
Valkey 8 and, rerun alone, on Valkey 9. ~~**All of that predates the allow-list and the CEL path
rule**: the allow-list's unit tests (`TestSeccompProfileAllowed`,
`TestSeccompProfileNotAllowed_NoWorkloadIsWritten`, `TestProfileList`) pass in `make test-unit`;
the CEL path rule has no unit test, only integration rows; no run of those rows or of
`TestPodSecurity_LocalhostProfileAllowList_Integration` is recorded, and the e2e subtest "a
Localhost profile the operator does not allow is refused and reported" has **not yet run**.~~
*(Superseded 2026-09-26 by ~~the final runs, one image built from the final code~~ the runs on one
image — allow-list, CEL path rule, the gate at the write and ADR 0025 D9 included (its guard, not
yet its clock or the one-update arm; corrected 2026-09-26):)* on the same Kind versions the
fleet-upgrade e2e from 1.12.8 green, both full suites 53/53 (Valkey 9 and Valkey 8), and two
extra Valkey 8 runs of the hardening e2e and `TestE2E_PodSecurity_RestrictedNamespace` green; the
allow-list refusal subtest was green on every run. Mutations killed: 8/8 of the ADR 0033
hardening code, 7/7 of the ADR 0033 D9 allow-list code (gate position included). Integration
(envtest 1.29) repeatedly green *(precised 2026-09-26, as in ADR 0033: those runs were on the
tree before the gate moved to the write and before ADR 0025 D9; the clean-copy run below ran it
again)* — that tier runs the CEL path rows and
`TestPodSecurity_LocalhostProfileAllowList_Integration`, neither of which skips; the CEL rule
still has no unit test. *(Final run, 2026-09-26, one image with ADR 0025 D9's clock and the
one-update arm, before the drain e2e fix:)* on the same Kind versions the fleet-upgrade e2e from
1.12.8 green, the full suite 53/53 on Valkey 8 and 52/53 on Valkey 9 — the one failure
`TestE2E_SidecarFailoverDrainMaster`, a fixture waiting on controller state (section Testing),
fixed afterwards and 8/8 green alone on Valkey 9 — and two extra Valkey 8 runs of the hardening
and restricted-namespace e2e green; no full suite has run on the drain fix.
~~Of the CI-parity gates on the final code only `make generate-all` (a
clean copy, empty `bin/`, fresh controller-gen v0.22.0: no diff) and `make test-release-tooling`
are recorded green; `make test-unit`, lint, cyclo, gosec, vuln, the coverage targets and
`make test-image-tools` are not recorded in this file.~~ *(Superseded 2026-09-26:)* a clean-copy
run on the code before the clock, the one-update arm and the drain fix had `make generate-all`
(fresh controller-gen v0.22.0: no diff), lint (golangci-lint v2.14.0, 0 issues), cyclo, gosec
v2.29.0 (0 issues), vuln (no vulnerabilities), the unit and integration coverage targets, image
tools and release tooling green; the rerun on the final code has no recorded result yet.
→ [ADR 0033](docs/adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md)
(D9 for the allow-list), extending [ADR 0032](docs/adr/0032-generated-pods-run-rootless.md) (D4 of
0033 supersedes its observer user)

## Metrics / Exporter

`spec.metrics.enabled` adds an exporter sidecar to every Valkey pod, serving `/metrics` on
`spec.metrics.port` (default 9121). It carries **no readiness probe**, so a failing exporter
never removes the pod from the `-rw`/`-r` Services. The `<name>-metrics` Service carries the
marker label `vko.gtrfc.com/metrics=true` so the ServiceMonitor selects only it; the
ServiceMonitor is `unstructured` (`monitoring.coreos.com/v1`) and skipped when the CRD is
absent. Enabling metrics changes the pod-spec hash and therefore rides the failover-aware
rolling update — lossless except for a single standalone pod without persistence.
→ [ADR 0018](docs/adr/0018-metrics-and-the-exporter-sidecar.md)

**The operator's own endpoint is a separate surface.** `:8080/metrics` serves one set of
`vko_valkey_*` series per Valkey resource, labelled with namespace and name, built by a
collect-time collector over the manager cache
([`internal/metrics/collector.go`](internal/metrics/collector.go)) — so a deleted resource
stops producing series with no deletion bookkeeping. **That is a standing constraint on new
metrics here: no gauge written from a reconcile pass.** The pair that matters is
`vko_valkey_metadata_generation` against `vko_valkey_status_observed_generation`; a gap is a
spec the operator accepted and never converged. The chart's Service, ServiceMonitor and
PrometheusRule for this endpoint are all **default off**, and the endpoint is unauthenticated
wherever it binds — the per-resource series make it an inventory of the fleet.
→ [ADR 0021](docs/adr/0021-per-resource-metrics-and-the-alert-that-was-missing.md)

# Important Notes

- Remember Cyclomatic Complexity: Keep it under 15 for all functions. Refactor if it exceeds this threshold.
- Check Code linting and formatting before reporing task done
- We have Unit-Tests, Integration-Tests and E2E-Tests. Always write tests for new features and bug fixes. Aim for high coverage, especially for critical reconciliation logic.
- Use the Makefile targets for all testing, linting, and analysis tasks. Do not run Go test commands or tools directly. This ensures consistency between local development and CI pipelines.
- For E2E tests, focus on real-world scenarios like rolling updates, failover, and recovery. Use actual Valkey instances to verify behavior.
- Do not commit to git, ask the user for a review and let the user commit to git. This ensures that the user is aware of all changes and can provide feedback before they are finalized.
- if you need to write temporary files, write them to local tmp-folder. Do not use the system tmp folder at /tmp
- persist important information about the project and implementation in this file
- **architecture decisions belong in `docs/adr/`, not here.** This file carries project-wide
  working rules and short pointers; the reasoning, the alternatives and the residual risks
  live in the ADR. When a decision changes, update its ADR in the same change and mark the
  superseded rule in place — see [Architecture Decision Records](#architecture-decision-records).
- if you are done with your task, always report a conventional commit message to the user, but do not commit to git. Let the user review and commit to git. This ensures that the user is aware of all changes and can provide feedback before they are finalized.
- If I ask you to investigate in my kubernetes cluster use this kube_config: /Users/hfi/repos/business_onpremise/kubernetes_configs/wds18-k8s-main

## graphify

This project has a graphify knowledge graph at graphify-out/.

Rules:
- Before answering architecture or codebase questions, read graphify-out/GRAPH_REPORT.md for god nodes and community structure
- If graphify-out/wiki/index.md exists, navigate it instead of reading raw files
- After modifying code files in this session, run `graphify update .` to keep the graph current (AST-only, no API cost)
