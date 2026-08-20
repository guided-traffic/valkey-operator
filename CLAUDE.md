# Valkey Operator

Repo: https://github.com/guided-traffic/valkey-operator

## Language Policy

All code, comments, commit messages, documentation, and CRD fields in this repository **must be written in English**.

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
  auth:
    secretName: my-valkey-secret
    secretPasswordKey: password
  metrics:
    enabled: true                 # adds a Prometheus exporter sidecar to each Valkey pod
    image: oliver006/redis_exporter:v1.66.0  # optional; sensible default when omitted
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
  networkPolicy:
    enabled: true
    namePrefix: "my-prefix"
  persistence:
    enabled: true
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
- A short description of the current task otherwise (e.g., `Rolling Update 2/3`, `Syncing`, `Failover in progress`)

## Testing

- **Unit tests**: High coverage for all reconciliation logic
- **Integration tests**: Must write actual values to Valkey and verify replication to replicas
- **E2E tests**: Required for rolling update scenarios (image change, failover verification)

### Makefile as Entry Point

Always use Makefile targets to run tests, linting, and analysis. Never invoke Go test commands or tools directly. The CI pipeline relies on the same targets.

| Task                          | Makefile Target                |
|-------------------------------|--------------------------------|
| Unit tests                    | `make test-unit`               |
| Unit tests with coverage      | `make test-unit-coverage`      |
| Integration tests             | `make test-integration`        |
| Integration tests w/ coverage | `make test-integration-coverage` |
| E2E tests                     | `make test-e2e`                |
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

### No `-short`, no `testing.Short()` gates

The unit targets deliberately do **not** pass `-short`, and no test in this
repo may gate itself behind `testing.Short()`. Both rules exist because the
combination silently removed eight `internal/controller` tests from CI, three
of which had been failing unnoticed (NA22). Unit tests reach no real Valkey:
`newTestReconciler` redirects every client to `127.0.0.1` for an instant
refusal, and tests that need a command to succeed use `fakeValkeyServer(t)`
(`internal/controller/manual_failover_known_master_test.go`) via
`NewValkeyClientFn`. There is no runtime left to save by skipping.

### E2E cluster topology

CI runs the E2E job twice, as a matrix in `.github/workflows/release.yml`:

| Leg          | Cluster                             | Scope                                             |
|--------------|-------------------------------------|---------------------------------------------------|
| `single-node`| control-plane only                  | full suite (`make test-e2e`)                      |
| `multi-node` | control-plane + 3 workers           | `make test-e2e E2E_RUN='TestE2E_AntiAffinity\|TestE2E_PodDisruptionBudget'` |

The multi-node leg exists because two behaviors are meaningless on one node:
eviction serialization (T3) and hard-mode anti-affinity spread (T5). Three
workers, not two: Kind keeps the control-plane `NoSchedule` taint on multi-node
clusters, so spreading three replicas needs three schedulable workers.

- `E2E_RUN` narrows `make test-e2e` to matching test names; empty runs everything.
- `E2E_REQUIRE_MULTI_NODE=true` turns the "fewer than 3 schedulable nodes" skip in
  `test/e2e/affinity_test.go` into a failure, so a cluster that came up smaller
  than requested cannot pass as a green skip. The multi-node leg sets it, and it
  additionally greps the test output to prove both scenarios actually ran.
- Locally: `make kind-create` already builds control-plane + 3 workers, so
  `make e2e-local` covers both.

## Rolling Update Strategy

1. Replace replica pods one by one
2. Verify new pod joins cluster and is seen by other instances
3. Wait for replication sync to complete
4. After 2 replicas are migrated: initiate controlled leader failover
5. Verify failover succeeded
6. Replace last pod (former master)

### Known master (`vko.gtrfc.com/known-master`)

The annotation carries the address of the pod the operator currently considers
master. It feeds the `replicaof` directive of the replica ConfigMap
(`GenerateValkeyConf`), never the config hash (`GenerateValkeyConfForHash`
ignores it), so publishing it during a failover cannot trigger a rolling restart.

Both init containers consult it, and the operator maintains it on both paths:

- Sentinel path: `syncSentinelWithMaster` persists the confirmed master at
  finalization; the init container uses it when no Sentinel answers.
- Non-Sentinel path: `handleManualFailover` publishes the promoted pod **and
  republishes the replica ConfigMap before deleting the old master**;
  `promotePod0AndRedirect` points it back at pod-0 after the topology is
  restored. The init container consults it only after peer discovery fails, only
  when the address is not the pod itself, and only when that peer answers
  `role:master`.

Why it is needed without Sentinel: with 2 replicas the promoted pod has no
replicas attached, so the init container's `role:master && connected_slaves > 0`
test rejects it and a returning pod-0 would elect itself master (NA20).

Related: while the manual failover is in flight, `detectAndResolveSplitBrain`
must be told that the promoted pod is the real master (`handleMultiReplicaRollingUpdate`
passes `annotationPromotedPod` for the `manualFailover`/`replacingMaster` states).
Otherwise its "most connected slaves" fallback ties at zero, picks the lowest
ordinal — the old master that was just deleted — and demotes the promoted pod,
losing the data (NA21).

### Topology restoration (non-Sentinel, two phases)

After the master was replaced, `handleTopologyRestoration` (Phase 1,
`stateRestoringTopology`) waits for pod-0 to sync back from the promoted replica
and then promotes it again; `verifyTopologyRestored` (Phase 2,
`stateVerifyingTopology`) confirms every replica reconnected.

Both phases are bounded, and they end differently:

- Phase 1 is bounded by `spec.rollingUpdate.syncTimeout` (default 5m), tracked in
  `vko.gtrfc.com/topology-restore-started`. On timeout `abandonTopologyRestoration`
  gives up the canonical topology, not the data: pod-0 is never force-promoted
  (an unsynced pod-0 would come up empty and discard the promoted replica's
  writes). It records `TopologyRestoreAbandoned` + `TopologyRestored=False` and
  hands over to **Phase 2**, not to a cleared state — once the state annotation is
  gone, `checkAndHandleRollingUpdate` returns early and nothing calls
  `detectAndResolveSplitBrain` again, so Phase 2 is the last pass that can
  consolidate the masters (NA23).
- Phase 2 is bounded by `finalizationStallTimeout` (2m, own annotation) on both
  its rogue-master branch and its pod-lookup-error branch.

**The known-master annotation is the split-brain authority for both states.**
`promotePod0AndRedirect` moves it to pod-0 only after the promotion succeeded, so
on the abandoned path it still names the promoted replica. Without naming it, the
"most connected slaves" fallback ties at zero in a shrunken cluster and picks the
returning pod-0 by lowest ordinal — NA21, one state later.

A non-pod-0 master is a supported end state: the `-rw`/`-r` Services select on
`instanceRole`, not on ordinal.

## Metrics / Exporter

`spec.metrics.enabled` adds an exporter sidecar (default `oliver006/redis_exporter`)
to every Valkey pod, serving `/metrics` on `spec.metrics.port` (default 9121, named
port `metrics`). Implementation:

- Sidecar container: `buildExporterContainer` in `internal/builder/statefulset.go`,
  appended via `buildPodContainers`. Connects to `localhost` (TLS port + skip-verify
  when TLS is on), reads the auth password from the Secret as `REDIS_PASSWORD`. It
  carries **no readiness probe** so a failing exporter never removes the pod from the
  `-rw`/`-r` Services.
- Service: `BuildMetricsService` (`<name>-metrics`) carries the marker label
  `vko.gtrfc.com/metrics=true` so the ServiceMonitor selects only it.
- ServiceMonitor: `BuildServiceMonitor` in `internal/builder/servicemonitor.go` is an
  `unstructured.Unstructured` (`monitoring.coreos.com/v1`) — no typed dependency,
  mirroring the cert-manager Certificate handling. Gated behind
  `spec.metrics.serviceMonitor.enabled`; skipped gracefully when the CRD is absent.
- Controller: `reconcileMetrics` (create-or-cleanup) in `valkey_controller.go`,
  wired via `reconcileMonitoringResources`.
- NetworkPolicy: the exporter port is opened on the Valkey ingress rule when metrics
  are enabled.
- **Lossless migration:** enabling metrics on a running cluster changes the pod-spec
  hash, so the existing failover-aware rolling update migrates pods without data loss
  — no persistence required. Exception: a single standalone pod (`replicas: 1`, no
  persistence) has no failover target, so adding the sidecar restarts it and loses
  in-memory data (physically unavoidable).

# Important Notes

- Remember Cyclomatic Complexity: Keep it under 15 for all functions. Refactor if it exceeds this threshold.
- Check Code linting and formatting before reporing task done
- We have Unit-Tests, Integration-Tests and E2E-Tests. Always write tests for new features and bug fixes. Aim for high coverage, especially for critical reconciliation logic.
- Use the Makefile targets for all testing, linting, and analysis tasks. Do not run Go test commands or tools directly. This ensures consistency between local development and CI pipelines.
- For E2E tests, focus on real-world scenarios like rolling updates, failover, and recovery. Use actual Valkey instances to verify behavior.
- Do not commit to git, ask the user for a review and let the user commit to git. This ensures that the user is aware of all changes and can provide feedback before they are finalized.
- if you need to write temporary files, write them to local tmp-folder. Do not use the system tmp folder at /tmp
- persist important information about the project and implementation in this file
- if you are done with your task, always report a conventional commit message to the user, but do not commit to git. Let the user review and commit to git. This ensures that the user is aware of all changes and can provide feedback before they are finalized.
- If I ask you to investigate in my kubernetes cluster use this kube_config: /Users/hfi/repos/business_onpremise/kubernetes_configs/wds18-k8s-main

## graphify

This project has a graphify knowledge graph at graphify-out/.

Rules:
- Before answering architecture or codebase questions, read graphify-out/GRAPH_REPORT.md for god nodes and community structure
- If graphify-out/wiki/index.md exists, navigate it instead of reading raw files
- After modifying code files in this session, run `graphify update .` to keep the graph current (AST-only, no API cost)
