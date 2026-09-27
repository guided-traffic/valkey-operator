# Rootless migration

How clusters an earlier operator built reach the rootless posture: the rolls the upgrade
starts, the pre-flight that fails a pod loudly instead of silently, the one root init
container the migration still adds, and the pods that stay root. The decision is
[ADR 0032](../adr/0032-generated-pods-run-rootless.md). The posture itself is [workload pod posture](workload-pod-posture.md).

## How existing clusters move

Which tiers the operator upgrade rolls, how often and in which order — a persistent data tier
twice, a Sentinel tier of one or two pods one Sentinel at a time — is
[what rolls and what restarts](../operations/upgrading.md#what-rolls-and-what-restarts).
Until a pod is replaced it runs as before; a container restart keeps the pod spec and so does
not apply the posture. A pod so old that it carries no `pod-spec-hash` annotation is not
recognised as outdated (`podSpecHashChanged` falls back to comparing resources) and keeps
running as root until it is deleted for another reason — and on a persistent tier it keeps the
repair below on the template for as long.

## The pre-flight `check-data-writable`

**The pre-flight `check-data-writable`** is the first init container of every persistent data
pod (only the repair below goes in front of it). What it checks, what a pod that fails it shows
and how to fix the volume is [the data-writable pre-flight](../operations/persistence.md#the-data-writable-pre-flight);
how it is built is [ADR 0032](../adr/0032-generated-pods-run-rootless.md). It turns a silent failure into a loud one: on an RDB volume whose root
is `0755 root`, a uid-999 pod starts, answers reads — and then every `BGSAVE` fails and, with the
generated `stop-writes-on-bgsave-error yes`, every write returns `MISCONF` while the pod stays
Ready (measured, T31). Nested directories and non-regular files are not checked.

## `fix-data-ownership`, the one root process

**`fix-data-ownership` is the one root process the operator still creates**, and only while the
migration runs (ADR 0032 D2, D4).

- *When.* Only in the data StatefulSet, only with persistence, and only while a data pod proven
  ours (`podIsOurs`) in the live StatefulSet's ordinal range runs without `runAsNonRoot: true` —
  a pod an earlier operator built — or the live template itself still lacks it, because at the
  first pass after the upgrade a pod may be missing *(the template half added 2026-09-26: it was
  in the code, not here)*. Once carried it stays until every ordinal holds a migrated
  pod — proven ours, rootless and Ready — and no data-tier roll is recorded; ~~past its
  pre-flight (exited 0, or the pod has been Ready)~~ *(tightened 2026-09-26: the removal starts
  the second roll, which must not overtake the first — ADR 0032 D4)*; a
  missing or foreign pod is not proof, which closes the race on the last pod of a tier
  (`dataOwnershipRepairNeeded`,
  [`pod_security_migration.go`](../../internal/controller/pod_security_migration.go)). No Sentinel,
  observer or non-persistent pod gets it: their volumes are fresh `emptyDir`s.
  *The ordering fix, 2026-09-26: both defects surfaced in fleet-upgrade runs on a node; the
  mechanism of the first was then read from the code (T31).* `reconcileStatefulSet` runs
  before the rolling update in the same pass. The first fleet-upgrade run with the second roll
  counted one `RollingUpdateComplete` per persistent tier instead of two: the repair had left the
  template on the last replacement's pre-flight, every pod turned outdated under the first roll,
  and `clearStaleRollingUpdateState` discarded that roll's state before it finalized — on the
  non-Sentinel path in the middle of the topology restoration. Hence Ready instead of "past its
  pre-flight", and the recorded-roll gate. The second run then stranded the repair: the pass that
  may remove it is the one after the completion, and a completing pass scheduled none (the CR
  watch is generation-gated, and there is no Pod watch). `finishDataRoll`
  ([`rolling_update.go`](../../internal/controller/rolling_update.go)) now asks for that pass
  (`requestRecheck`, 10 s) whenever the template still carries the repair. For this page the
  consequence is the length of the window: the template carries the root init container until
  the tier's first roll has **finalized** plus, normally, that one recheck — not merely until its
  last pod started — and any pod deleted in that span, a chaos kill included, is created with it.
  The recorded-roll state is a gate on *keeping* the repair, never evidence for adding
  it (`TestDataOwnershipRepairNeeded_StaysWhileARollIsRecorded`,
  `TestCompletedRoll_AsksForThePassThatRemovesTheRepair`; `make test-unit` green 2026-09-26).
- *What.* uid 0 and gid 0, `runAsNonRoot: false`, `capabilities: drop [ALL], add [CHOWN]`,
  `privileged: false` *(stated since 2026-09-26, ADR 0033 D4)*,
  `allowPrivilegeEscalation: false` (`no_new_privs`), `readOnlyRootFilesystem: true`, under the
  pod's ~~`RuntimeDefault` seccomp~~ seccomp profile — `RuntimeDefault`, or a `Localhost` one,
  which must then allow its `chown` *(amended 2026-09-26, ADR 0033 D1)* — and, where
  `spec.podSecurity.userNamespaces` is on, inside the pod's user namespace, where uid 0 is not
  root on the node. It runs `find /data ! -user 999 -exec chown -h 999:999 {} + ;
  exit 0` ([`dataOwnershipRepairScript`](../../internal/builder/pod_security.go)). It is
  best-effort — it always exits 0, and the pre-flight after it is the one gate — because a
  second run on a sandbox restart cannot enter a directory the first handed to 999 with `0700`
  (`lost+found` on an ext4 root) without the DAC override it lacks. `-h` re-owns a symlink
  itself, never its target. *(Corrected 2026-09-26: this used to quote the command from before
  the pre-release review, without `-h` and `exit 0`.)* It mounts only the data volume and
  receives no environment and no token. Its image is the `valkey` container's, i.e.
  `spec.image`.
- *Why.* kubelet applies `fsGroup` on some volume types and not on others: not on `hostPath`
  (Kind's local-path provisioner — asserted by the fleet-upgrade e2e, green in one local run
  on 2026-09-26, not a CI job), NFS or a CSI driver with `fsGroupPolicy: None`. There,
  `root:root 0644` files and a `0755`
  `appendonlydir` an earlier operator wrote stay unwritable for uid 999, and an AOF pod exits
  at start. `CAP_CHOWN` alone suffices because the legacy files are owned by the uid the
  repair runs as (measured, T31).
- *Why the template writes are not a roll.* `reconcileStatefulSet` inserts it into the *built*
  StatefulSet (`WithDataOwnershipRepair`) after `ComputePodSpecHash` ran, so the pod-spec hash
  never covers it: adding it and removing it are two writes of the StatefulSet and no pod
  replacement. Root therefore enters only a pod *created* while the template carries it — the
  replacements of the migration roll, and any pod deleted for another reason in that window, a
  chaos kill included — as one `find`. *(This bullet was headed "Why it never rolls a pod";
  superseded 2026-09-26 by the second roll below — the template writes still roll nothing, the
  pods that carry the repair afterwards do.)*
- ~~*What rolling nothing leaves behind.* A pod that received the container keeps it in its
  spec after the template drops it, until the pod is next replaced — after the upgrade that is
  every persistent data pod the migration roll created.~~ *(Superseded 2026-09-26 by
  [ADR 0032](../adr/0032-generated-pods-run-rootless.md) D2: the alternative "accept the
  repair in pod specs until their next replacement" lost.)*
- *The second roll.* A pod that received the container keeps it in its immutable spec after the
  template drops it, and that alone makes it outdated (`podCarriesRetiredRepair`, asked through
  `podOutdated` at every data-tier site — the dispatch loop, `collectPodStates`, the standalone
  handler and the manual-failover master check,
  [`rolling_update.go`](../../internal/controller/rolling_update.go)). The ordinary failover-aware
  roll replaces it: every persistent multi-replica data tier rolls twice at the upgrade, and a
  persistent single pod restarts twice — two short downtimes, data kept. The comparison, not the
  hash, starts that roll, and only once the repair has left the template, which it does only
  when every ordinal holds a migrated pod and the first roll has finalized (the ordering fix
  above); a pod missing during the second roll is no evidence,
  so the repair does not come back. Until the second roll replaces it, such a pod keeps two
  exposures: Kubernetes re-runs a pod's init containers whenever it gives the pod a new sandbox
  (a node reboot, say), so the repair runs again there as root with `CAP_CHOWN`, finding
  nothing left to re-own; and `spec.initContainers[*].image` stays writable by a pod update
  like the container images ([isolation and tenancy](isolation-and-tenancy.md#what-does-not-hold)), so a stolen sidecar token can point that root
  container at an image of its choice for its next run. Neither was measured. No root process
  runs in a pod created after the template dropped the repair, so once a tier's second roll
  completes none of its pods carries a root container. Unit-tested
  (`TestReconcileStatefulSet_RepairComesAndGoesAndTheRetiredRepairRolls`,
  `TestPodCarriesRetiredRepair`,
  `TestHandleStandaloneRollingUpdate_ReplacesAPodCarryingTheRetiredRepair`; `make test-unit`
  green 2026-09-26). ~~**not yet run on a node** ([H-16](workload-pod-posture.md#h-16)).~~ *(Corrected 2026-09-26.)* On a
  node it ran twice, and each run found one of the two ordering defects above; ~~**the run after
  both fixes is in progress and has not completed**~~ *(superseded 2026-09-26)* the run after both
  fixes, from 1.12.8 on Kind, was green, with exactly two `RollingUpdateComplete` per persistent
  tier and nothing rolling after the second roll, and green again on the final image of
  2026-09-26 — locally, not in CI ([ADR 0032](../adr/0032-generated-pods-run-rootless.md) Status).

## What can switch the repair on

The evidence is the pod's `securityContext`, which no pod update can change: no label or
annotation — nothing the sidecar token can patch ([isolation and tenancy](isolation-and-tenancy.md#what-does-not-hold)) — can switch the repair on, and it
survives an operator restart. Detaching a pod by deleting a selector label does make it "not
provably ours" and so keeps a repair that is already carried on the template; that extends the
migration window, it cannot start one. The recorded-roll gate of the ordering fix has the same
shape: `vko.gtrfc.com/rolling-update-state` is an annotation on the **CR**, which the sidecar
token cannot write (its Role names pods only, [the per-instance sidecar Role](privilege-footprint.md#the-per-instance-sidecar-role)), and a principal that can patch the
CR and keeps the annotation set holds an already-carried repair on the template — it cannot add
one. That principal already picks `spec.image`, which is the repair's image (read from
`dataOwnershipRepairNeeded`; not tested as an attack). A template carrying the repair passes Pod Security `baseline` and fails `restricted`
(`TestPodSecurity_TheRepairIsBaselineButNotRestricted`), which is why the namespace label waits
for the migration ([H-22](#h-22)).

## When the repair fails

~~Two ways the repair itself fails, and both hold the pod in its init phase until the roll
reports `PodAvailabilityStalled` naming it after `spec.rollingUpdate.syncTimeout` — before the
pre-flight gets to name the fix. The repair sets no `FallbackToLogsOnError`, so its refusal is
in `kubectl logs <pod> -c fix-data-ownership`, not in `kubectl describe pod`.~~ *(Corrected
2026-09-26: that described the repair before the pre-release review made it best-effort.)* The
repair exits 0 whatever `chown` refused, so a volume it could not re-own reaches the pre-flight,
which holds the pod in its init phase and names the fix in `kubectl describe pod`; the roll
reports `PodAvailabilityStalled` naming the pod after `spec.rollingUpdate.syncTimeout`
([ADR 0026](../adr/0026-a-pod-being-deleted-is-not-available.md) D11). What `chown` refused is
in `kubectl logs <pod> -c fix-data-ownership`.

- **NFS with `root_squash`** is the case no pod can repair. Root is squashed, so `chown` is
  refused; the fix is a server-side `chown -R 999:999` before the upgrade. Root-squash itself
  was not measured.
- **A symlink on the data volume** is why the repair passes `-h`. Measured in Docker on
  2026-09-26 (`valkey/valkey:9.1.1`, GNU coreutils 9.7) on the command without it: `chown`
  follows a symlink, so the root repair tried to re-own the link's *target* inside its own
  container rather than the link, and any `chown` failure — a dangling link, a target on the
  read-only root — made `find`, and with it the repair, exit 1. With `-h` it re-owns the link
  itself; no run of that variant against a symlink is recorded. Valkey writes no symlinks;
  placing one takes write access to the volume.

## Single-pod clusters decide by persistence

**Single-pod clusters decide by persistence** (ADR 0032 D3, `singlePodDeferral`). A persistent
`spec.replicas: 1` pod still running as root is replaced at the upgrade, the repair running on
its way up, and once more when the repair has left the template (the second roll above) —
~~one restart~~ two restarts since 2026-09-26, data kept. One exception, read from the code and
not tested: if the template's sidecar image moved between the two replacements (another
operator upgrade in that window), the second is a sidecar-only drift of a rootless pod, which
`isSidecarOnlyChange` defers to the pod's next restart under `SidecarUpdatePending`
(ADR 0007 D6) — and the pod keeps the repair until then. A non-persistent one is **not**
replaced ~~unless its Valkey image changed as well~~ *(corrected 2026-09-27, against
`singlePodDeferral` and ADR 0032 D3: a new Valkey image is not the only change that still
replaces it; which changes do is stated once, in
[what rolls and what restarts](../operations/upgrading.md#what-rolls-and-what-restarts))*,
because replacing it would discard the dataset: it keeps running as root until it is deleted
for another reason (a node drain, say — a container restart keeps the pod spec), and `PodSecurityUpdatePending=True` (reason
`PodRunsAsRoot`) names it. Deleting the pod applies the posture at once and discards the
dataset. How the condition is written and cleared is
[`PodSecurityUpdatePending`](../operations/status.md#podsecurityupdatepending).

## What this does not cover

<a id="h-21"></a>

### H-21: Finish the rootless migration where it cannot finish itself

([ADR 0032](../adr/0032-generated-pods-run-rootless.md) D3, D7, [how existing clusters move](#how-existing-clusters-move)). Before the
upgrade, `chown -R 999:999` every Valkey volume on NFS exported with `root_squash`, on
the server side — no pod can re-own it, and the first replacement never starts. After
it, three kinds of pod still run as root: a non-persistent single pod
(`PodSecurityUpdatePending` names it; deleting it discards its data), a pod so old that
it carries no `pod-spec-hash` annotation (delete it), and the pods of a tier whose
roll holds on a replacement that never became available (`PodAvailabilityStalled`).

<a id="h-22"></a>

### H-22: Enforce Pod Security `restricted` on each namespace once it is migrated

([ADR 0032](../adr/0032-generated-pods-run-rootless.md) D6). The posture is a
property of what the operator renders; the label makes the API server refuse
everything else, including a container-level `runAsUser: 0` or
`seccompProfile: Unconfined` the subset drift comparison does not converge back
([the drift comparison](workload-pod-posture.md#the-drift-comparison-checks-only-what-the-operator-sets)). List the violators first with
`kubectl label --dry-run=server --overwrite ns <ns> pod-security.kubernetes.io/enforce=restricted`,
then label for real. ~~Expect the dry run to name the persistent data pods the migration
roll created: their spec keeps the completed repair ([fix-data-ownership](#fix-data-ownership-the-one-root-process)).~~ *(Superseded
2026-09-26: a second roll replaces those pods,
[ADR 0032](../adr/0032-generated-pods-run-rootless.md) D2.)* Once every persistent
data tier has finished its second roll, the dry run should name no generated pod beyond
the three kinds of [H-21](#h-21) — derived from the unit matrix and
`podCarriesRetiredRepair`, not measured with a dry run after a migration. A data pod it
names for `fix-data-ownership` belongs to a tier whose second roll is still running or
held (`PodAvailabilityStalled`), or is a persistent single pod whose second replacement
was deferred as sidecar-only (`SidecarUpdatePending`, [single-pod clusters](#single-pod-clusters-decide-by-persistence)). Such pods do not block the label — enforcement acts at
admission and evicts no running pod, and their replacements come from the repair-free
template — so the precondition is the template, not the pods: no data StatefulSet in
the namespace may still carry `fix-data-ownership`. *(Verified 2026-09-27 from Pod
Security Admission semantics and the code, not measured on a migrated namespace; the
[upgrade page](../operations/upgrading.md) had made the pods part of the precondition
and now points here. Enforcement evaluates pod create and update requests, and labelling
a namespace only returns warnings for the pods already running in it — it evicts none.
Those pods are afterwards written only in ways enforcement lets through: the operator
and the sidecar change a pod solely by metadata merge patches (`clearDrainStamps` in
`internal/controller/steady_state_master.go`, `patchMetadata` in
`internal/sidecar/labeler.go`), which Pod Security exempts from its checks unless they
touch the seccomp or AppArmor annotations — read upstream, not measured here — and
deleting a pod is not checked at all. Their replacements are created by the
StatefulSet controller from the current template. The repair leaves that template
only when `dataOwnershipRepairNeeded` finds every ordinal holding a pod proven ours,
rootless and Ready with no roll recorded, and does not come back; a template without
it makes every pod still carrying it outdated (`podCarriesRetiredRepair`), so those
pods are replaced from a template enforcement admits. The root pods of
[H-21](#h-21) behave the same way on a non-persistent tier; on a persistent tier a
data pod without `runAsNonRoot` keeps the repair in the template
(`dataOwnershipRepairNeeded`), so the template check already covers it.)* Not
earlier: a data template carrying the migration repair is `baseline`, not
`restricted`, so the pods the roll would create are refused at admission and the roll
waits on an absent pod (`PodRecreationStalled`). The operator does not label
namespaces. What the label does **not** police: which `Localhost` seccomp profile a pod
names — `restricted` accepts every one ([H-19](seccomp-profiles.md#h-19)).
