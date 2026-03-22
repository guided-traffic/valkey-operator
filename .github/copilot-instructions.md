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
    enabled: true
  tls:
    enabled: true
    allowUnencrypted: false   # set to true to keep port 6379 open alongside TLS port 16379
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
