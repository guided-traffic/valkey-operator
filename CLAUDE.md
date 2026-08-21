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

### RBAC lives in three places — keep them in sync

The full privilege footprint — every rule, what it permits, the operator versus
sidecar split, and the hardening checklist — is documented in
`SECURITY_ARCHITECTURE.md` (linked from `README.md`). Update it in the same change
whenever a marker, the chart ClusterRole or `BuildSidecarRole` changes.

The kubebuilder markers in `internal/controller/valkey_controller.go` generate
`config/rbac/role.yaml` (`make manifests`), but the ClusterRole that actually
reaches users is the hand-maintained
`deploy/helm/valkey-operator/templates/clusterrole.yaml`. Only convention kept
them aligned, and the drift shipped twice: NA12 (all operator Events silently
discarded) and a missing `delete` on core `secrets` that wedged every cluster
migrating to `spec.tls.unifiedCertificate` (NA37) — the apiserver evaluates authz
before existence, so a missing verb returns 403 even for an object that is gone.

`TestHelmClusterRoleCoversGeneratedRole` (`internal/controller/rbac_drift_test.go`)
expands both manifests into `(group, resource, verb)` triples and asserts
generated ⊆ chart, so chart-only extras (leader-election leases) stay legal.
**A new marker needs the chart rule in the same change**; the test names the
missing triple. It compares manifest against manifest, never marker against
manifest, so a stale `config/rbac/role.yaml` would pass it — that half is covered
by the `generated-manifests` job in `.github/workflows/release.yml`, which runs
`make generate-all` on every push and PR and fails on a dirty tree (NA44).
`make generate-all` regenerates the CRDs, the DeepCopy code **and**
`config/rbac/role.yaml`, and syncs the CRD into the chart; the chart ClusterRole is
hand-maintained and is not generated by it.

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

### Steady-state split brain and the drain stamp (`vko.gtrfc.com/drain-promoted-at`)

Outside a rolling update `checkSteadyStateSplitBrain`
(`internal/controller/steady_state_master.go`) is the only thing that re-detects a
second master. A demotion there is a `REPLICAOF`, which discards the demoted
dataset, so it never acts on the known-master annotation alone: the sidecar drain
handler promotes a replica on every SIGTERM of a master pod and has no CR access
to record it, so the annotation is trustworthy exactly when the operator promoted
and untrustworthy exactly when the sidecar did.

Three independent pieces of evidence separate the two, and none is required:

- **The stamp.** `internal/sidecar/drain.go` patches
  `vko.gtrfc.com/drain-promoted-at` (RFC3339 UTC) onto the pod it promotes, right
  after the promotion and before any best-effort step. Best-effort itself: a lost
  stamp is a degradation, never a corruption. Non-Sentinel path only, and it does
  not survive a delete-recreate of the promoted pod (it lives on the Pod object).
  The handler writes no stamp when a peer already answers `role:master`
  (`findSyncedReplica` sweeps every peer first): during a rolling update the
  operator promotes a replica itself and then deletes the old master without
  demoting it, so the drain runs on a pod that is still master while the topology
  already has a new one, and promoting again would stamp a third pod nobody
  promoted.
- **The structural rule.** The init script (Phase 3) grants the master config to
  ordinal 0 on the ordinal fallback, and otherwise only via the NA35 self-claim,
  which needs the mounted replica config to name the pod itself. A labeled master
  with ordinal > 0 that the **live** replica ConfigMap does not name therefore
  cannot have elected itself. The live ConfigMap, not the CR annotation: the two
  diverge whenever a republish did not land, and it is the ConfigMap the pod reads.
- **The recorded pod yielded.** The pod the annotation names answers a probe and
  reports a role other than master (`recordedGaveUpTheRole`). A pod that replicates
  from somewhere else has already given up its dataset, so republishing the replica
  ConfigMap away from it destroys nothing — the record is simply out of date.
  Unreachable, absent or still-master all read as **no** evidence: that pod may be
  a master that is merely restarting, and the writes it holds are exactly the ones
  an adoption would discard.

The third rule exists because the second is blind to exactly the pod a drain
promotes most often: `buildReplicaAddrs` walks the ordinals ascending and
`findSyncedReplica` takes the first synced peer, so draining a non-pod-0 master
promotes **pod-0** whenever pod-0 is healthy — and `couldNotHaveSelfElected` can
never exonerate pod-0. A non-pod-0 master is not exotic either: it is the routine
output of this design, since every adoption leaves one behind. So the
operator-upgrade window, in which old sidecars write no stamp, rests on the
structural rule for a pod-0 master and on the recorded pod's own answer for every
other one.

**The creation order may only ever REFUSE a demotion, never grant an adoption.**
"The pod the annotation names is the younger Pod object" is true after a drain, and
just as true after the recorded master's node hard-failed with no SIGTERM (hence no
drain and no stamp) while a peer that could reach nobody took the ordinal fallback
and elected itself. Adopting there republishes the replica ConfigMap toward the
self-elected pod and the real master full-resyncs its newer dataset away the moment
it boots — silently, and caused by the operator. Being newer is evidence of
nothing. The same signal is safe in the refusing direction, where the worst outcome
is two masters a human can see, so `refuseDemotion` uses it and
`promotionEvidence` does not.

`recreatedAfter` is deliberately strict about the order (`metav1.Time.Before`,
one-second resolution), fails closed on an unreadable or absent pod, and is
**bounded**: the recreation must be inside `spec.rollingUpdate.syncTimeout`
(default 5 m, the same budget the replica replacement and Phase 1 of the topology
restoration use for a deleted pod to come back). Unbounded it would be a permanent
property of two Pod objects — true after any reschedule, forever — and the operator
would never consolidate that pair again. Past the window it resolves toward the
annotation, as it did before the rule existed.

Where the refusal does **not** fire, so nobody reads more into it: a simultaneous
restart of the whole pod set. The data StatefulSet runs
`PodManagementPolicy: Parallel`, so a co-restart recreates every pod at once and
**ties** the timestamps; `Before` is strict, so the rule is inert and the
annotation decides. (An earlier version of this section claimed a StatefulSet
recreates in ordinal order — it does not, for the data set.) Its accepted cost
stays: inside the window it cannot tell a returning drained master from a master
that merely crashed and came back while a peer self-elected in the gap, and that
case stays a visible split brain until a human resolves it or the window expires.

Decision table (`labeled` = pods carrying `instanceRole=master`):

| State | Outcome |
|---|---|
| `len == 0` | return; `checkAndRecoverNoMaster` owns it |
| `len == 1`, annotation names it | no-op, **no probe at all** |
| `len == 1`, stamped and confirms master | adopt |
| `len == 1`, ordinal > 0 and the ConfigMap names someone else | adopt |
| `len == 1`, the recorded pod answers and is **not** master | adopt |
| `len == 1`, the recorded pod is unreachable, gone, or still master | refuse, `MasterAdoptionRefused` |
| `len == 1`, none of them | refuse, `MasterAdoptionRefused` (no requeue: one master is not a split brain) |
| `len >= 2`, exactly one stamped master confirms | record it, demote the others toward it |
| `len >= 2`, more than one stamped master confirms | refuse, `SplitBrainDemotionRefused` |
| `len >= 2`, annotation names pod-0 and another confirmed master could not have self-elected | refuse, `SplitBrainDemotionRefused` |
| `len >= 2`, annotation names a pod recreated after a confirmed master, inside `syncTimeout` | refuse, `SplitBrainDemotionRefused` |
| `len >= 2`, annotation names a confirmed master | demote the others toward it |
| `len >= 2`, no admissible authority | refuse, `SplitBrainUnresolved` |

Ambiguous evidence routes into the refusal, never past it: two live stamped masters
used to fall through to the annotation, which then demoted **both** of them and
discarded two drain windows at once — the most destructive action in the file,
taken precisely because nothing said which dataset mattered.

Both **demotion** refusals keep the recheck requeue (`steadyStateRecheckDelay`,
15 s) and never suppress `updateStatus` — the cluster is still split, so the
operator owes it another look. The **adoption** refusal does not requeue: one
labeled master is not a split brain (writes reach exactly one dataset), and no
amount of polling fixes a record only a human can correct. Neither does the
no-admissible-authority branch, for the same reason.

**The stamp is cleared from every pod of the cluster at two sites**, and both are
correctness rather than hygiene: the stamp means "a promotion nobody recorded", so
once the operator has recorded one the stamp is spent evidence — and evidence beats
the annotation on the next pass, so a leftover stamp would have the operator adopt
the stale pod and `REPLICAOF` the master it legitimately promoted.

- `recordPromotedMaster`, after the known-master write succeeded.
- `clearRollingUpdateState`, after the state annotations were removed.
  `recordPromotedMaster` is **not** the single funnel for the known-master
  annotation: `persistManualFailoverState` writes it directly (one `Update`, for
  its conflict retry) and `syncSentinelWithMaster` goes through
  `persistKnownMaster`, while `verifyTopologyRestored`,
  `finalizeMultiReplicaRollingUpdate` and `handlePostManualFailover` end a rolling
  update without recording a master at all. `clearRollingUpdateState` is where all
  of them converge.

  The call sits **below** the early returns on purpose: `checkAndHandleRollingUpdate`
  calls the function on every pass that reports `Completed`, including passes where
  nothing was running, and it runs before `checkSteadyStateSplitBrain` in the same
  reconcile — clearing unconditionally would delete a fresh drain stamp in the pass
  before the check that exists to read it.

Accepted residuals:

- An in-place sandbox recreation within kubelet's ConfigMap refresh window (~1 min)
  can self-claim off a stale mount and be adopted by the structural rule.
- The stamp lives on the Pod object, so a promoted pod that loses its node before
  the operator adopts the promotion returns without it. That degrades to the
  structural rule, the recorded pod's own answer, or a refusal.
- The creation-order refusal turns a crash-and-self-elect race into a visible split
  brain instead of resolving it toward the annotation — for the length of
  `spec.rollingUpdate.syncTimeout`, after which the annotation decides again.

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

The same annotation is also the authority of the steady-state check below, and of
the init container's self-claim — three consumers, one recorded truth.

**Contract, sharpened (NA50): the annotation is the tie-breaker AMONG MULTIPLE
masters; it is never used to overrule a single, undisputed master.** The reason is
data, not tidiness: demoting a master is a `REPLICAOF` that discards its dataset,
so an operator that overrules the only master in the cluster destroys the writes of
whatever promoted it — including promotions the operator did not perform itself.
The sidecar performs exactly such a promotion on every SIGTERM of a master pod
(`internal/sidecar/drain.go`, any node drain or eviction) and cannot record it: its
Role grants `pods get/list/patch` and no CR access at all
(`internal/builder/rbac.go`) — it records the promotion on the promoted **pod**
instead (`vko.gtrfc.com/drain-promoted-at`). Where one labeled master disagrees
with the annotation, `adoptUnrecordedPromotion` moves the annotation to it **only
with evidence** that somebody else promoted it: the stamp, the structural rule, or
the recorded pod answering that it is no longer master. The label alone is not
evidence — a pod that elected itself off a stale mount answers `role:master` just
as convincingly.

**Invariant: a promotion the operator could not record is not a completed
promotion.** Once the steady-state check demotes *toward* this annotation and the
init script boots a pod as master *from* it, the annotation is a data-plane
authority, not telemetry — so every write that records a promotion is part of the
promotion, never best-effort. `persistManualFailoverState` therefore retries and
then fails the pass instead of deleting the old master with the promotion
unrecorded, and `promotePod0AndRedirect` moves the annotation only after the
promotion actually succeeded. Do not relax either write back to `_ = r.Update(...)`.

### Steady-state split brain (non-Sentinel)

`checkSteadyStateSplitBrain` (`internal/controller/steady_state_master.go`, wired
into `handlePostRollingUpdateChecks`) is the only split-brain check that runs
**outside** a rolling update. Without it nothing re-detects a second master once
the rolling-update state annotation is cleared, and the `-rw` Service keeps
round-robining writes across two independent datasets (NA26).

- It probes only when at least two pods carry `instanceRole=master`, so the
  healthy case costs no connection. Skipped with Sentinel, for single-replica
  clusters, and while a rolling-update state is set.
- **The known-master annotation is its only authority among two or more masters.**
  It demotes a rogue only toward the pod that annotation names, and only when that
  pod itself answers `role:master` on a live probe.
- **With exactly one labeled master it adopts instead of demoting.**
  `adoptUnrecordedPromotion` runs at `len(labeled) == 1`: if the annotation names a
  different pod, the labeled one confirms `role:master` **and one of the three
  pieces of evidence above holds**, the annotation is moved to it and a
  `MasterAdopted` Event is recorded. This is the drain case — the sidecar promoted
  a replica on SIGTERM and had no way to record it on the CR — and adopting is what
  keeps the later demotion pointed at the pod that does **not** hold the
  drain-window writes (NA50). A pod that is unreachable or reports `role:replica`
  is not adopted (a stale label is not a promotion), and neither is one with no
  evidence behind it: that ends in a `MasterAdoptionRefused` Warning. With two or
  more labeled masters the same adoption runs on an unambiguous drain stamp
  (`adoptAndConsolidate`); two stamped masters refuse instead of consolidating.
- **It never tie-breaks.** Inside a rolling update the operator knows which pod it
  promoted; in steady state it does not, and the "most connected slaves" fallback
  ties at zero in a shrunken cluster and picks the lowest ordinal — the mechanism
  that destroyed the promoted pod's data in NA21. A refusal for lack of an
  admissible authority (no annotation, named pod unreachable, named pod reports
  replica) records a `SplitBrainUnresolved` Warning; a refusal because the shape
  says a drain promoted the rogue records `SplitBrainDemotionRefused`. Both change
  nothing.
- The demotion path performs no API writes (cached List, Valkey commands, Events),
  so it still runs during a blocked pass. Intended: a split brain is a data-plane
  emergency and an admission gap must not suppress it. The adoption path is the one
  exception — it writes the annotation and the replica ConfigMap — and a blocked
  pass simply fails it with a log line; the next pass retries, and nothing is left
  half-applied (`persistKnownMaster` restores the in-memory value it could not
  write).
- A confirmed second master it could **not** demote asks for a recheck without
  ending the pass: the `steadyStateRecheckDelay` (15 s) travels back as a
  non-terminal `ctrl.Result` that `reconcileWorkload` applies after `updateStatus`.
  Ending the pass instead would skip the status write and freeze the CR at its last
  verdict (usually `OK`) while the operator loops on a split brain invisibly, and
  dropping the requeue would leave the next look to the 10 h cache resync — the CR
  watch is generation-gated and there is no Pod watch. Only the unresolved case
  requeues; a merely stale label does not, because the operator cannot fix a label
  it does not own.

It is therefore inert without the annotation — a cluster whose CR annotations were
stripped (a GitOps prune) keeps two masters with only a Warning, and the adoption
does not re-establish a missing annotation either (nothing recorded means nothing
to contradict). That is the decision, not an oversight.

### Init container: a pod named in its own `replicaof` boots as master

The non-Sentinel init script (`internal/builder/statefulset.go`) ranks its master
decision: peer discovery > **self-claim** > ordinal fallback. When the mounted
replica config names the pod itself, `SELF_IS_KNOWN_MASTER` is set and the pod
boots with the master config instead of taking the ordinal fallback (NA35).

- Below peer discovery, so an established master with replicas always wins.
- Above the ordinal fallback, because peer discovery rejects a master reporting
  `connected_slaves: 0` — exactly what a freshly promoted pod looks like after a
  full pod-set restart. That is the case where the promoted pod used to full-sync
  its own post-failover writes away.
- **It must not ship without the check above.** The self-claim can produce two
  masters when pod-0 already elected itself; `checkSteadyStateSplitBrain`
  consolidates them, using the same annotation as authority on both sides.
- **It is only as good as the annotation, which is why adoption matters.** After a
  node drain the sidecar promotes a replica and cannot record it, so the annotation
  keeps naming the drained pod — and its replica ConfigMap keeps naming it too.
  Without `adoptUnrecordedPromotion` the returning pod self-claims on a stale
  record, becomes a second master, and the steady-state check then demotes the pod
  that took every write during the drain (NA50). The self-claim is not what causes
  that loss — a returning pod-0 reaches master through the ordinal fallback anyway —
  but it widens the set of ordinals it applies to.
- Accepted residual: a stale mounted ConfigMap (kubelet refresh lag, up to ~1 min)
  can make a pod self-claim after the operator re-pointed the annotation. The CR
  annotation is already correct then, so the first steady-state pass demotes the
  claimant.

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
