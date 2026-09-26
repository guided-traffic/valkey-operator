# ADR 0032: Generated pods run rootless, and existing clusters move with the operator upgrade

## Status

Accepted. Date: 2026-09-26. Decided by Hans on the T31 analysis
([`local_T31-generated-pods-run-as-root.md`](../tickets/local_T31-generated-pods-run-as-root.md));
released only together with [ADR 0026](0026-a-pod-being-deleted-is-not-available.md) D11 (T32),
because this is the first change that rolls every cluster of a fleet automatically.

Implemented:

- the posture on every data, Sentinel and observer pod, applied by one walk
  ([`pod_security.go`](../../internal/builder/pod_security.go));
- the pre-flight `check-data-writable` on every persistent data pod, and the migration-only
  `fix-data-ownership` repair while a data pod an earlier operator built still exists
  ([`pod_security_migration.go`](../../internal/controller/pod_security_migration.go));
- the single-pod rule and its condition `PodSecurityUpdatePending`;
- securityContext in the drift comparisons of both StatefulSets and of the observer
  Deployment.

Reviewed adversarially on 2026-09-26 before release; the review tightened D2 (a best-effort
repair), D3 (persistence from the persisted StatefulSet; rotated TLS material and changed
configuration are not deferred) and D4 (the template as evidence; "migrated" means past the
repair), and found that the pods created during the migration keep the repair in their spec
(D2, D6) — recorded here as a consequence, with a second roll as the open alternative.

Verified on a node locally, not in CI, and said so: the branch has not been through the
pipeline. On 2026-09-26 on Kind (control plane + 3 workers, Kubernetes v1.36.1, containerd)
`make test-e2e` ran 51/51 green on both pinned Valkey lines, and the fleet-upgrade e2e passed
from released chart 1.12.8; the runs are recorded in the ticket. See Residual risks for what
each tier does and does not prove.

Amends [ADR 0005](0005-upgrade-neutral-defaults-and-anti-affinity.md) D1 (scope), D7 (one
recorded exception) and D11 (this release rolls the Sentinel tier);
[ADR 0007](0007-failover-aware-rolling-update.md) D6 and D7 (the single-pod rule);
[ADR 0012](0012-the-sidecar-records-its-drain-promotion-on-the-pod.md) D8 step 4 (the token
under `fsGroup`); [ADR 0017](0017-test-and-ci-policy.md) (the new guards). Supersedes
[ADR 0013](0013-operator-is-cluster-wide-privileged.md) D9.

## Context

The builder set no `securityContext` on any pod or container — ADR 0013 D9, a stated decision.
Kubernetes therefore ran each container as its image's user, with the runtime's default
capability set, `Unconfined` seccomp and a writable root filesystem.

The upstream Valkey image declares **no `USER`**. It is built to start as root and drop
privileges itself: `docker-entrypoint.sh` chowns the working directory to `valkey` and
re-execs through `setpriv --reuid=valkey`. **The operator never ran that entrypoint**: every
container on the Valkey image sets `command:`, which replaces `ENTRYPOINT`. Measured in Docker
on both pinned lines (T31, 2026-09-25): `valkey-server` ran as uid 0 with fourteen
capabilities — `NET_RAW`, `DAC_OVERRIDE`, `SETUID` among them — and `NoNewPrivs: 0`. The
operator ran Valkey with *less* isolation than the image has when started the way its authors
intended, and a namespace enforcing Pod Security `restricted` refused every generated pod.

The same measurement under `--user 999:999 --read-only --cap-drop ALL
--security-opt no-new-privileges` — the restricted posture, with a tmpfs per `emptyDir` —
passed on both pins: RDB and AOF writes, an AOF rewrite, `valkey-sentinel` rewriting its
config, `sed -i` and `sha1sum` in the Sentinel init script, the exporter. Nothing the operator
runs inside the Valkey image needs root.

What does need care is **data an earlier operator wrote as root**: `root:root 0644` files and a
`0755` `appendonlydir`, because the entrypoint that would have set a restrictive umask and the
right owner was bypassed. Measured against a uid-999 pod on the same volume:

| Volume | Result |
|---|---|
| RDB, volume root `0755 root` | starts, serves reads, answers `PONG` — then `BGSAVE` fails, and because the generated config sets `stop-writes-on-bgsave-error yes`, **every write returns `MISCONF` while the pod stays Ready** |
| AOF, either mode | exits 1 (`Can't open the append-only file`) → CrashLoopBackOff |
| AOF after a simulated kubelet `fsGroup` re-group | works |
| AOF after `find /data ! -user 999 -exec chown 999:999 {} +` as uid 0 with **only `CAP_CHOWN`** | works |

kubelet applies `fsGroup` on some volume types (CSI with an `fsType`, in-tree `local`) and not on
others (`hostPath` — Kind's local-path provisioner — NFS, `fsGroupPolicy: None`). An operator
downgrade onto repaired data is safe (root with `DAC_OVERRIDE` writes anything); uid 0 with
every capability dropped is **not** — the same AOF permission error — which is why no
"root without capabilities" intermediate posture exists below.

Three code facts shaped the migration. The pod-spec hashes cover the whole built `PodSpec`
(ADR 0005 D7), so a posture change reaches every existing pod through the ordinary
failover-aware roll — the Sentinel tier included, which a plain upgrade otherwise never rolls
(ADR 0005 D11). `podSpecChanged` did not compare `securityContext`, and the observer Deployment
carries no hash at all, so without new comparison lines an existing observer would never have
received the posture. And the single-pod rule was decided by `isSidecarOnlyChange`, an image
comparison: on the Helm path a release ships a new sidecar image *with* the new posture, the
change was classified sidecar-only and deferred; on kustomize the only pod was deleted at once —
for a non-persistent cluster, with its data.

## Decision

**D1 — Every generated pod is rootless, with no option.** Data and Sentinel pods run with the
pod-level `runAsNonRoot: true`, `runAsUser: 999`, `runAsGroup: 999`, `fsGroup: 999` and
`seccompProfile: RuntimeDefault`; every container and init container — the operator's sidecar
and the third-party exporter included — with `allowPrivilegeEscalation: false`,
`readOnlyRootFilesystem: true` and `capabilities.drop: [ALL]`. The observer runs with
`runAsNonRoot` and `RuntimeDefault` at pod level (its image user, 65532, is numeric) and the
same three container fields. The `valkey` container states `workingDir: /data`, which it used to
inherit from the image, because without persistence a replica's full-sync RDB lands in the
working directory. There is no CRD field, no `baseline` level and no opt-out: root was a defect,
not a setting, and ADR 0005 D1's "new features default to off" governs features, not the repair
of a defect.

The posture is applied by one walk over the assembled `PodSpec` (`applyValkeyPodSecurity`,
`applyObserverPodSecurity`), called last in each builder. **A container added later inherits
the posture because it is in the pod, not because its builder remembered to ask for it.**

**D2 — Root-written data is re-owned once, by a repair the hash never sees.**

- `fsGroup: 999` with `fsGroupChangePolicy` unset (= `Always`): `OnRootMismatch` inspects only
  the volume root and would skip files a later root writer left beneath a correct one.
- Every persistent data pod runs the pre-flight `check-data-writable` first (uid 999, shell
  builtins only). It fails the pod when `/data`, `/data/appendonlydir`, or a regular file in
  either is not writable, and names the fix; `terminationMessagePolicy: FallbackToLogsOnError`
  puts that message into `kubectl describe pod`. It turns the silent `MISCONF` of the RDB row
  above into a loud refusal.
- While the migration evidence of D4 holds, the data template carries `fix-data-ownership` in
  front of the pre-flight: uid 0, `drop: [ALL]`, `add: [CHOWN]`, read-only root,
  `no_new_privs`, `find /data ! -user 999 -exec chown -h 999:999 {} + ; exit 0`. **The repair is
  best-effort and the pre-flight is the one gate**: a pod created while the template carried the
  repair keeps it in its immutable spec and re-runs it on every sandbox restart, and a second run
  cannot enter a directory the first one handed to 999 with mode `0700` — `lost+found` on an ext4
  root — without the DAC override it deliberately lacks. A failing repair would then block a
  migrated pod after every node reboot. `-h` re-owns a symlink itself, never its target.
- **The repair is inserted after `ComputePodSpecHash`** (`WithDataOwnershipRepair`, on the
  built object), so adding it and removing it rolls nothing. This is a narrow, recorded
  exception to ADR 0005 D7: the container acts only at pod start, and after the migration it is a
  no-op.
- No repair for Sentinel, observer or non-persistent pods: their volumes are fresh `emptyDir`s.

No pod created after the migration runs a root process. The pods created *during* it keep the
repair in their spec until they are next replaced for any other reason; on a sandbox restart it
runs again as a no-op, and a Pod Security `restricted` dry-run lists them (D6). Clearing them
would take a second roll, which this decision does not make (Consequences).

**D3 — Single-pod clusters are decided by persistence.** On a `spec.replicas: 1` data cluster
whose pod runs without `runAsNonRoot` (`singlePodDeferral`):

- persistent: replaced at once, with the repair running on its way up — one restart, data
  kept;
- not persistent, Valkey image unchanged: **deferred** until the pod restarts for any other
  reason, reported by `PodSecurityUpdatePending=True/PodRunsAsRoot` naming the pod. The
  operator upgrade alone never discards a dataset;
- not persistent, Valkey image changed: replaced — the CR author asked for a new image, the
  same data-loss change that was always applied;
- not persistent, TLS material rotated or configuration changed: replaced as well. Both are
  records outside the pod-spec hash, so the operator upgrade alone never moves them; a rotation
  roll of a non-persistent single pod is the loss [ADR 0030](0030-rotating-certificates-rotate-the-instances-that-cannot-reload-them.md)
  already accepted — deferring it would keep an expiring certificate in the pod — and a
  configuration change is the CR author's. A change the pod-spec hash carries cannot be told
  apart from the posture and is held with it; the condition message says so. (Refined on
  2026-09-26 in the adversarial review, beyond the three cases decided in T31.)

**Persistence is read off the persisted StatefulSet** (`volumeClaimTemplates`), never off the CR:
a persistence toggle the operator refused to write (ADR 0023) would otherwise read as
"persistent" and delete the only pod together with its `emptyDir`.

`isSidecarOnlyChange` no longer decides a root pod; it still decides a rootless one (ADR 0007
D6). `PodSecurityUpdatePending` is a level with one evaluator in `checkAndHandleRollingUpdate`
(the deferral is decided one dispatch target down, which most passes never reach), written
`False/PodSecurityUpdateApplied` only over a standing True, Event-free (ADR 0025 D7), with a
row in `conditionRegistry` (ADR 0027).

**D4 — The migration evidence is the pod spec, derived per pass and stored nowhere.** Pod specs
are immutable, so "a pod proven ours (`podIsOurs`) whose `spec.securityContext.runAsNonRoot` is
not `true`" cannot be forged by a label or an annotation and survives an operator restart
(`PodRunsRootless`). The repair is **added** when such a pod exists in the ordinal range of the
live StatefulSet, or when the live template itself still lacks the posture — at the first pass
after the upgrade a pod may be missing, and the rootless template would otherwise be written
without the repair. It is **kept** until every ordinal holds a *migrated* pod: proven ours,
rootless, and past the repair — its pre-flight exited 0, or it has been Ready. A missing or
foreign pod is not proof, and neither is a rootless replacement stuck before its init containers
(an image it cannot pull). The asymmetry closes two races: a pass between the last legacy pod
disappearing and its recreation, and a replacement that exists but has not run its repair.
Persistence, again, comes from the persisted StatefulSet.

**D5 — The drift comparisons include the posture, with subset semantics and one exception.**
`podSpecChanged`/`containerChanged` compare the pod- and container-level `securityContext`
fields the operator sets; a field it does not set is not compared (`nil` ≡ `{}`), so a
mutating admission policy adding one is not a drift the operator rewrites the StatefulSet
over. The exception is `capabilities.add`: the live template may not add a capability the
desired one does not, because a subset comparison there would never converge an out-of-band
grant of `NET_RAW` back. `ObserverDeploymentHasChanged` gains the same two lines — without them
an existing observer would never have received the posture.

**D6 — What an administrator enforces afterwards.** Once a namespace holds no pod without
`runAsNonRoot`, `pod-security.kubernetes.io/enforce: restricted` can be set on it;
`kubectl label --dry-run=server --overwrite ns <ns> pod-security.kubernetes.io/enforce=restricted`
lists the violators first. It also lists the persistent data pods created during the migration,
which still carry the repair (D2); enforcement does not evict running pods, and their next
replacement comes from the clean template. The operator does not label namespaces.

**D7 — Released after ADR 0026 D11 (T32).** A replacement of a multi-replica or Sentinel tier
that never becomes available — NFS with `root_squash` refusing the repair's `chown` is the known
case — is reported as `PodAvailabilityStalled` instead of stalling silently, and after a fix the
operator replaces the stuck pod itself. A **single pod** is not covered: its roll records no
state, so a current pod that does not start takes the converged early return and shows only as
phase `Provisioning` (ADR 0026, residual risk "A single pod that never starts is not
reported").

## Consequences

- **Every multi-replica data tier rolls once, and every Sentinel tier rolls once**, which a
  plain operator upgrade otherwise never does (ADR 0005 D11). Failover-aware and lossless like
  every roll.
- **Persistent single-pod clusters restart once at the upgrade** — downtime, not data loss.
- **Non-persistent single-pod clusters keep running as root** until their next restart for any
  other reason, and say so in `PodSecurityUpdatePending`. That is the price of never discarding
  a dataset for an operator upgrade.
- Root still runs once per persistent data pod during the migration, for a fraction of a second
  — also on storage where `fsGroup` alone would have sufficed. The repair cannot tell the two
  apart without a failed start first (Alternatives, M2).
- **NFS with `root_squash`** is the case no pod can repair: root is squashed, `chown` fails, the
  pre-flight holds the first replica, and `PodAvailabilityStalled` names it after
  `syncTimeout`. It needs a server-side `chown -R 999:999` before the upgrade.
- A StatefulSet re-created by hand over PVCs that were never migrated gets no repair (no legacy
  pod exists); the pre-flight stops it loudly and a manual `chown` fixes it. The same holds for
  a **scale-up onto claims retained from a scale-down** under an earlier operator — the operator
  sets no `persistentVolumeClaimRetentionPolicy`, so such claims keep root-written files — and
  for **fresh volumes whose root is owned by root on storage without `fsGroup` support** (a
  static `hostPath`, a CSI driver with `fsGroupPolicy: None`). Those worked while the pods ran as
  root. The storage requirement is now: kubelet applies `fsGroup`, or the volume root is writable
  by uid 999 (Kind's local-path and nfs-subdir create `0777` directories).
- **The persistent data pods created during the migration keep the repair in their spec** until
  they are next replaced (D2, D6). No second roll clears them; that was a deliberate trade in
  the decision (no second roll), and whether to add one is an open question to Hans.
- The root filesystem is read-only: debugging goes through `kubectl debug`, not through writing
  into a container.
- The sidecar and the exporter now run as 999 rather than as their images' users (65532 and
  59000). Measured for the exporter in Docker; the sidecar is the operator's own binary.
- The migration costs two extra writes of each persistent data StatefulSet: the repair in, the
  repair out.
- An operator downgrade onto repaired data is safe: the old shape runs as root with
  `DAC_OVERRIDE`.

## Alternatives Considered

- **A per-cluster level field** (`spec.podSecurity.level`, with a `baseline` default for
  existing clusters, an inherit-or-restricted rule, or a CRD default pinned by the migration
  hook) — withdrawn in the decision round: root is a defect, not an option, and an opt-in field
  leaves the fleet on the defect. The hook variant also lost on mechanism: the hook runs before
  the new CRD and its pin is pruned.
- **M2, repair only after the pre-flight proves it is needed** — root only where physically
  needed and the hand-recreated-StatefulSet case covered, but one failed start per affected
  cluster, and a new "delete an unavailable pod" exception in ADR 0026, the rule that had been
  incomplete three times.
- **M3, no root container at all** (`fsGroup` and pre-flight only) — clusters with legacy data
  on storage without `fsGroup` stop at their first replica, single-pod ones are down, until an
  administrator runs a manual `chown`.
- **S2, all single pods deferred** — no downtime, but every single-pod cluster stays root until
  it restarts, possibly for weeks. **S3, all replaced** — non-persistent single pods lose their
  data to an operator upgrade.
- **Root without capabilities as an intermediate posture** — measured failing: uid 0 with every
  capability dropped has no `DAC_OVERRIDE`, cannot open the `999`-owned AOF files the repair
  leaves behind (`Can't open the append-only file … Permission denied`), and would therefore
  break an operator downgrade that the plain root shape survives.
- **The repair inside the pod-spec hash** — every cluster would roll twice: once to add the
  repair, once to remove it.
- **"Any legacy pod exists" without the hysteresis of D4** — the race on the last pod of a tier.
- **`fsGroupChangePolicy: OnRootMismatch`** — cheaper on large volumes, and skips exactly the
  files a later root writer leaves beneath a correctly owned root.

## Residual risks

- **Measured on a node** (local Kind, control plane + 3 workers, Kubernetes v1.36.1, containerd,
  2026-09-26), both pinned Valkey lines: `TestE2E_PodSecurity_RestrictedNamespace` green on both
  legs — the namespace refuses an unrestricted pod (positive control), every generated pod is
  admitted under `enforce=restricted` and Ready, `Uid: 999`, `CapEff: 0`, `CapBnd: 0` and
  `NoNewPrivs: 1` read from `/proc/1/status` of the `valkey` and `sentinel` containers, an AOF
  rewrite and an RDB snapshot complete under containerd's `RuntimeDefault`, TLS+auth data
  replicates, a Sentinel image roll and a drain failover finish with zero Warning Events, the
  sidecar labels roles. The projected token under `fsGroup` is `0640`, owner and group 999 (read
  off the node). Kind's PV is `hostPath` (`DirectoryOrCreate`, root `0777 root`), and the
  fleet-upgrade e2e asserts that at runtime and fails loudly if Kind changes.
  `TestE2E_FleetUpgrade` (`make test-e2e-fleet-upgrade E2E_UPGRADE_FROM=1.12.8`, 253 s) passed
  from released chart 1.12.8 over a fleet of six on valkey 9.1.1: every multi-replica and
  Sentinel cluster converged rootless with its keys on every replica, every persistent pod ran
  `fix-data-ownership` (exit 0) and the repair then left the template, the migrated persistent
  masters wrote and snapshotted without `MISCONF` (RDB volume roots set to `0755 root`
  beforehand), the observer received the posture, each Sentinel tier rolled exactly once, no pod
  was replaced in the 90 s after the repair left, the persistent single pod restarted once with
  its keys, and the non-persistent one was not restarted, kept its keys and reports
  `PodSecurityUpdatePending=True/PodRunsAsRoot`. What that run does not prove: the default
  starting point 1.10.48 (its released images are amd64-only, the host was arm64; 1.12.8 ran
  under emulation), a volume kubelet re-groups under `fsGroup` (Kind's is `hostPath`), and
  anything in CI — it is still not a CI job. **Not covered at
  all**: CRI-O's smaller default capability set and OpenShift's SCC — **under OpenShift's `restricted-v2` SCC a fixed
  `runAsUser: 999` outside the namespace's UID range is refused.** Nothing in this repository
  targets OpenShift today.
- A **Sentinel** cluster with `spec.replicas: 1` is not covered by D3: it goes through the
  Sentinel rolling update (`handleRollingUpdate`), not `singlePodDeferral`, and its only data pod
  is replaced like on every other spec change of that shape — without persistence, with its
  data. That predates this ADR; this release is one more trigger for it.
- A pod created by an operator so old that it carries no `pod-spec-hash` annotation is not
  recognised as outdated by the posture change (`podSpecHashChanged` falls back to comparing
  resources), so it is not migrated by the roll; a restart for any other reason migrates it.
- The subset comparison of D5 lets an admission policy add a field the operator does not set —
  `fsGroupChangePolicy: OnRootMismatch` included, which would weaken D2. Checked read-only on
  wds18 on 2026-09-26: its Kyverno mutate rules act on Pods at CREATE and are opt-in by label
  (`minio-backup/*`, `cp/inject-truststore`); none rewrites a StatefulSet template. Other
  clusters were not checked.
- A pod deleted for any reason while its StatefulSet carries the repair — a chaos kill during
  the migration — runs the repair on its way up. That is the intended behaviour, and it is root
  for the length of one `find`.
- The pre-flight checks the two directories Valkey writes and the regular files directly in
  them. A nested directory, or a file type other than a regular file, is not checked.
- The T31 hypothesis that a restarted replica holding a readable `dump.rdb` can resume by partial
  resync without writing a file — and so pass the rolling update's sync check and be promoted
  onto the `MISCONF` of the RDB row — was never measured. The pre-flight exists so that it does
  not have to be.
- The sidecar's token projection keeps `DefaultMode 0644`; under `fsGroup` kubelet rewrote it —
  measured on the Kind node: the token is `0640`, owner 999, group 999, and `ca.crt` is
  `0644 root:999`. The sidecar runs as uid 999 like every other container of the pod and reads
  its own file, which the e2e role labelling confirms. A kubelet that leaves the mode at `0644`
  was not measured.

## References

- [`internal/builder/pod_security.go`](../../internal/builder/pod_security.go) — the posture, the
  walk, the pre-flight, the repair, the subset comparison
- [`internal/controller/pod_security_migration.go`](../../internal/controller/pod_security_migration.go)
  — the migration evidence, the single-pod rule, the condition's evaluator
- [`internal/builder/image_requirements.go`](../../internal/builder/image_requirements.go) —
  `find` and `chown`
- Tests: [`internal/builder/pod_security_test.go`](../../internal/builder/pod_security_test.go)
  (Pod Security evaluator matrix, both sides), [`internal/controller/pod_security_migration_test.go`](../../internal/controller/pod_security_migration_test.go),
  [`test/integration/pod_security_test.go`](../../test/integration/pod_security_test.go),
  [`test/imagetools/restricted_runtime_test.go`](../../test/imagetools/restricted_runtime_test.go),
  [`test/e2e/pod_security_test.go`](../../test/e2e/pod_security_test.go),
  [`test/e2e/fleet_upgrade_test.go`](../../test/e2e/fleet_upgrade_test.go)
- [ADR 0005](0005-upgrade-neutral-defaults-and-anti-affinity.md), [ADR 0007](0007-failover-aware-rolling-update.md),
  [ADR 0012](0012-the-sidecar-records-its-drain-promotion-on-the-pod.md),
  [ADR 0013](0013-operator-is-cluster-wide-privileged.md), [ADR 0017](0017-test-and-ci-policy.md),
  [ADR 0026](0026-a-pod-being-deleted-is-not-available.md) D11, [ADR 0027](0027-conditions-are-levels-edges-or-history.md)
