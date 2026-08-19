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

## Rolling Update Strategy

1. Replace replica pods one by one
2. Verify new pod joins cluster and is seen by other instances
3. Wait for replication sync to complete
4. After 2 replicas are migrated: initiate controlled leader failover
5. Verify failover succeeded
6. Replace last pod (former master)

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

## StatefulSet Nudge (short-of-pods recovery)

Pod creation for both StatefulSets is entirely the statefulset-controller's job
(`updateStrategy: OnDelete`, `podManagementPolicy: Parallel`). When its creates are
rejected — e.g. by a fail-closed admission webhook whose backend is temporarily gone —
it retries on an exponential workqueue backoff that reached **5 min 29 s** in the
2026-08-19 infra-d incident, long after the rejection cause was resolved. Nothing else
wakes it: the StatefulSet object is not written (no spec drift) and with zero pods there
are no pod events either.

The operator therefore bumps an annotation to force an immediate resync:

- Annotation `vko.gtrfc.com/nudge: <RFC3339>` (`builder.AnnotationNudge`), written as a
  **merge patch** on the StatefulSet *metadata* — not on the pod template. It is
  therefore invisible to `StatefulSetHasChanged` / `SentinelStatefulSetHasChanged` /
  `OperatorVersionChanged` and never triggers a rolling update.
- The stored timestamp doubles as rate-limit state: re-bumped only when older than
  `builder.NudgeInterval` (20 s). No CRD field, no in-cluster state.
- Grace period `nudgeGracePeriod` (10 s, in-memory `nudgeTracker`) before the first bump
  so normal pod churn is not nudged. Losing the map on restart is harmless.
- Suppressed while `vko.gtrfc.com/rolling-update-state` is set — there the operator
  deletes pods on purpose.
- Applies to the data and the Sentinel StatefulSet; keyed on
  `status.replicas < spec.replicas` (created pods, not ready ones).
- Code: `internal/controller/nudge.go` (`nudgeShortStatefulSets`), called from
  `Reconcile` after the rolling-update checks; helpers in `internal/builder/annotations.go`.
- E2E guard: `TestE2E_AdmissionRejection_StatefulSetNudgeRecovery`
  (`test/e2e/admission_recovery_test.go`) blocks CREATE pods with a namespace-scoped
  `failurePolicy: Fail` webhook, deletes all data pods, then asserts recovery within 60 s
  of removing the webhook.

## ReconcileBlocked Condition

`status.conditions[type=ReconcileBlocked]` tells a user *why* a CR is stuck without
reading operator logs — specifically it separates "a cluster-side admission gate
rejects my writes" from "the write itself failed".

- Set from the outcome of every `reconcileResources` pass in `Reconcile`
  (`valkey_controller.go`), via `setReconcileBlockedCondition`
  (`internal/controller/reconcile_blocked.go`).
- Reasons: `AdmissionWebhookDenied` when `isAdmissionRejection` matches the error
  (message contains `failed calling webhook`, or `admission webhook ... denied the
  request`, or an internal error mentioning the admission chain), `WriteFailed`
  otherwise, `ReconcileSucceeded` when cleared. Matching is on the message, not only
  the typed reason: callers wrap errors (`fmt.Errorf("sentinel statefulset: %w", err)`)
  and explicit denials arrive as `Forbidden`, not as an internal error.
- Message carries the underlying error (truncated at `conditionMessageLimit`, 1024
  runes) including the webhook name.
- **No status write when nothing changes** — neither on a healthy pass that was never
  blocked, nor on repeated identical failures. A blocked cluster reconciles every few
  seconds; rewriting the condition each time would be pure API churn.
- No CRD schema change: `status.conditions` already exists, so `make manifests`
  produces no diff.
- Note on scope: only writes the *operator* performs reach this condition. Pod
  creation is the statefulset-controller's job, so the 2026-08-19 incident's
  `CREATE pods` rejection never surfaced here — that failure mode is covered by the
  StatefulSet nudge above.
- E2E guard: `TestE2E_AdmissionRejection_ReconcileBlockedCondition`
  (`test/e2e/admission_recovery_test.go`) blocks `CREATE configmaps` with a
  namespace-scoped `failurePolicy: Fail` webhook and asserts the condition names it,
  then flips to `False` after removal.

## Aggregate Reconcile (no abort on the first failing sub-resource)

A rejected write on one managed object must not silence the rest of the pass. In the
2026-08-19 incident a single webhook rejection on the Sentinel StatefulSet skipped
NetworkPolicies, monitoring, `updateStatus` and the health/rolling-update handling
for as long as the rejection lasted.

- `reconcileResources` is a **step list** (`reconcileStep{name, when, run}`) executed by
  `runReconcileSteps` (`valkey_controller.go`): every applicable step runs, failures are
  collected and returned as one `errors.Join`, each wrapped with its step name
  (`"StatefulSet: ..."`). Same helper inside `reconcileServices`, `reconcileMonitoringResources`
  and `reconcileMetrics`, so a failing Service does not skip its siblings either.
  Steps only reference earlier objects by name, so continuing is safe.
- Step order is unchanged (ConfigMap → replica ConfigMap → TLS → Services → sidecar RBAC →
  StatefulSet → Sentinel → NetworkPolicies → monitoring); `when` replaces the old `if` chains,
  which also kept cyclomatic complexity down.
- `Reconcile` no longer returns on a resource error. The data-plane part moved to
  `reconcileWorkload` (rolling update, post-rolling checks, nudge, `updateStatus`, requeue)
  and runs either way. The joined error is returned afterwards so the controller-runtime
  rate limiter backs off instead of spinning on the 10 s requeue.
- **Phase is written once, last.** The per-step `updatePhase` calls are gone; a blocked pass
  ends with `Error` + `"Failed to reconcile resources: <joined>"`, written *after*
  `updateStatus` so it is not overwritten. `compactErrorMessage`
  (`internal/controller/reconcile_blocked.go`) folds `errors.Join`'s newlines into `"; "` —
  both the phase message and the `ReconcileBlocked` message must stay single-line for
  `kubectl`/Lens.
- Tradeoff accepted: while blocked, `updateStatus` and the final phase write disagree, so a
  blocked pass costs two status writes instead of one. Healthy passes are unaffected.
- Unit guards: `internal/controller/reconcile_steps_test.go` (all steps run despite a
  failure, joined error carries every rejection, data plane still reconciled while blocked).
- E2E guard: `TestE2E_AdmissionRejection_ReconcileContinuesPastRejectedWrite`
  (`test/e2e/admission_recovery_test.go`) blocks `UPDATE apps/v1 statefulsets`, scales the CR
  and enables NetworkPolicies in one patch, then asserts the NetworkPolicies appear anyway,
  the condition names the `StatefulSet` step, and `status.readyReplicas` still reports the
  running pod. `blockCoreResourceCreation` is now a wrapper around the generalized
  `blockResourceOperations(t, ns, name, group, version, operations, resources...)`.

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
