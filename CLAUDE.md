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

**With one exception, and it is deliberate:** while a managed write is being refused, the
phase reports `Error` even on a perfectly healthy cluster, because a spec the operator
accepted and cannot apply has to be visible. `phase` therefore carries two meanings and the
blocked pass wins the field; the **`Ready` condition** carries only the data-plane verdict
and stays `True`. That pair is not a contradiction — it reads as "your cluster is serving,
and the operator cannot write something".
→ [ADR 0002](docs/adr/0002-surface-a-blocked-reconcile-on-the-cr.md) D3, D5, D5a, D12

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

The operator runs shell **inside** the upstream Valkey image: two init container scripts, the
auth-wrapped container command, the exec probes and the drain preStop hook. What those execute
is declared in `RequiredImageTools`
([`internal/builder/image_requirements.go`](internal/builder/image_requirements.go)) and
checked against the real images by `make test-image-tools` — docker, no cluster, its own CI
job. **A new tool in a generated script needs a line in that list**; a unit test walks the
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
before the first Sentinel pod is replaced; the Sentinel tier rolls afterwards, carries the
`SentinelUpdatePending` condition while it does (phase `Sentinel Rolling Update i/n`), and
emits `SentinelUpdateComplete` exactly when that condition flips back to False. Anything
sequencing on "the update is finished" on a sentinel-enabled cluster waits for the Sentinel
marker, not the data one.
→ [ADR 0024](docs/adr/0024-the-sentinel-tier-reports-its-own-completion.md)

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
topology says so.
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

It is a test and not a convention because the convention was missed four times — the clear
kept ending up behind the very code path whose absence caused the staleness — and writing
the table down for the first time immediately found two more (`RollingUpdatePaused` on
non-Sentinel topologies, `StorageSpecNotApplied` with two evaluators), both declared in the
registry with their ticket reference rather than silently carried. **There is deliberately no
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
*spends* a pod (deletes, promotes, counts toward a quorum or a completion) uses it;
`reachable()` = Ready alone is the carve-out for the four sites that only *talk* to a pod, of
which `demoteRogueMaster` is the load-bearing one — refusing to demote a terminating master
would leave it accepting writes for the rest of its termination. **The rule is the rename, not
a list of sites**: it had been stated as a list three times and been incomplete every time.

On top of it one invariant: **the operator never deletes a pod of a tier while any pod of that
tier is terminating.** The gate sits immediately in front of each `deleteOwnedPod` and never at
a function head; "the tier" is the ordinal range `[0, *sts.Spec.Replicas)`, never a
label-selector List. The refusal is never resumed — the *observation* of it is bounded, by the
pod's own `deletionTimestamp` (which the API server sets to `now + gracePeriodSeconds`, so
`time.Since` of it is the overrun) rather than by `ensureWaitBound`. Past
`podTerminationOverrun` = 2 min the pass stops ending on the wait and reports
`PodTerminationStalled`, so the Sentinel roll, the no-master recovery, the steady-state
split-brain check and the status write run again. **No Event on any of it** — ADR 0025 D7 still
promises zero Warnings on a clean roll. `countUpdatedPods` deliberately still counts a
terminating pod; the completion hold lives in `finalizeRollingUpdate`, Sentinel path only.
→ [ADR 0026](docs/adr/0026-a-pod-being-deleted-is-not-available.md)

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
- Everything else is replaced. The reconciler stamps `vko.gtrfc.com/tls-material-hash` - a
  fingerprint of `ca.crt`/`tls.crt`/`tls.key`, Secret *content* the builders never see - onto
  both StatefulSet pod templates, so a rotation rides the ordinary failover-aware rolling
  update. `valkey-server` and `valkey-sentinel` are treated as pinning because nobody has
  measured otherwise; the third-party exporter provably is, and the restart unit is the pod, so
  one non-reloading container spends the whole pod's exemption. The observer Deployment carries
  no fingerprint and is never restarted for a rotation.

**The trigger is the rotation, not the expiry.** That buys the full 30-day cert-manager grace
window, so the roll is never time-critical and needs neither scheduling nor stampede control -
the ADR 0019 concurrency cap is the only pacing there is, and the shipped
`ValkeyTLSMaterialStale` alert waits **72 h** rather than minutes. The one thing the window does
not cover is a roll that never starts, and that is what the `TLSMaterialStale` level reports.
Upgrade neutrality is the presence guard the other hashes already use: a pod without the
annotation is never restarted for one.

**ADR owed, deliberately deferred (`ADR spaeter, erst Code`, decided 2026-08-26):** ADR 0030,
plus amendments to ADR 0016 (its "whether the running server reloads the new material is not
verified" residual risk has now fired, on the client side, and is measured) and ADR 0012 (the
drain promotion gains the precondition that its Valkey client holds usable TLS material), plus
`SECURITY_ARCHITECTURE.md` sections 2/6 and the hardening checklist. Until that lands, those
documents are knowingly stale.

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
