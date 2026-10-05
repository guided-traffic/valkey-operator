# Persistence

What each persistence mode writes, what happens when the storage of an existing cluster
is changed, and the pre-flight every persistent data pod runs before Valkey starts. The
fields are in the [`spec.persistence`](../../README.md#specpersistence) table; the
decision behind the immutable storage is
[ADR 0023](../adr/0023-volume-claim-templates-are-immutable.md).

## Persistence Modes

| Mode | Description |
|------|-------------|
| `rdb` | Point-in-time snapshots (`save 900 1`, `save 300 10`, `save 60 10000`) |
| `aof` | Append-only file with `appendfsync everysec` |
| `both` | RDB + AOF combined for maximum durability |

## Without persistence, a restarted master can empty its replicas

With `spec.persistence.enabled: false` (the default) nothing is saved (`save ""`,
`appendonly no`). A restart of the master's `valkey-server` container — an `OOMKilled` at the
memory limit, a crash, a liveness-probe kill — can bring it back on the same address with no
data, or with the snapshot of its last full sync as a replica. Its replicas can then
resynchronize from it and drop what they held, so replication does not protect the dataset
against this event. Enable persistence for any dataset that must survive a restart of the
master's container. This is derived from reading the operator and from running the Valkey
side in plain docker against the Valkey 9 and Valkey 8 images the tests pin; it has not been
reproduced on Kubernetes.

## Changing storage on an existing cluster

`mode` is a config-file setting and propagates like any other one — it changes the
config hash and rides the failover-aware rolling update. **`enabled`,
`storageClass` and `size` are read when the StatefulSet is created and never
again.** A StatefulSet's `volumeClaimTemplates` are immutable, the API server
rejects every update that touches them, and the operator never writes them — so
changing storage on an existing cluster is not drift that a later pass converges
([ADR 0023](../adr/0023-volume-claim-templates-are-immutable.md)).

The operator reports the difference instead of submitting a write that cannot fix
it — enabling persistence on an existing StatefulSet is rejected by the API server,
and disabling it is *accepted*, which is worse: the pod template gains an
`emptyDir` while the live claims stay on the object.

| Change on an existing cluster | What the operator does |
|---|---|
| `enabled` toggled in either direction | Writes **nothing** to that StatefulSet — replica, image and label changes are held together with the storage change. `StorageSpecNotApplied=True` with reason `RecreateRequired`, `ReconcileBlocked=True` with the same reason, and a `StatefulSetRecreateRequired` Warning Event on every pass. |
| `size` or `storageClass` changed while `enabled: true` | Applies every other change normally; only the storage stays as it is. `StorageSpecNotApplied=True` with reason `VolumeClaimTemplatesImmutable` and a `StatefulSetRecreateRequired` Warning Event naming the difference. The reconcile is **not** blocked. |

Both shapes use the same Event reason — `StatefulSetRecreateRequired` for the data
StatefulSet, `SentinelStatefulSetRecreateRequired` for the Sentinel one, which
carries no `volumeClaimTemplates` today and therefore never conflicts — and the
message says which shape it is. Because the Sentinel tier never conflicts, it is
also never allowed to *resolve* `StorageSpecNotApplied`: either tier may report a
conflict, only the data tier may clear one. A tier that compares empty against
empty has proven nothing about the tier that holds the claims. While a `RecreateRequired` conflict stands the
**ConfigMap keeps converging** — the `save`/`appendonly` directives follow the spec
even though the volumes do not, so a pod that restarts for any other reason boots
the new persistence config against the old volume layout. That costs consistency
between the dump settings and the volume, never the dataset: the pod rejoins as a
replica and resyncs from the master.

### Recreating the StatefulSet

**The migration works in one direction and not the other.** Both were walked on a
running three-replica cluster (2026-08-23, Kind, Kubernetes 1.36); the results are
not symmetric and the difference decides whether you can do this at all.

Either way the first step is the same, and `--cascade=orphan` is not optional:

```bash
kubectl delete statefulset <name> -n <namespace> --cascade=orphan
```

> **Never omit `--cascade=orphan`.** Without it the delete takes every pod at once,
> and a cluster whose data is only in memory loses it on the spot.

**Turning persistence off: verified, lossless.** The pods survive the delete, the
operator recreates the StatefulSet without claim templates, the
statefulset-controller re-adopts the pods, and the failover-aware rolling update
replaces them one by one with `emptyDir`-backed ones. Measured end state: three new
pods, `phase: OK`, and the dataset intact on all three. The old
PersistentVolumeClaims stay behind, still bound (see below).

**Turning persistence on: do not do this on a cluster whose data matters.** The
same first step wedges. Re-adoption itself works, but the statefulset-controller
then tries to attach the new claim to each adopted pod, which pod immutability
forbids — `Pod "<name>-0" is invalid: spec: Forbidden: pod updates may not change
fields other than ...`. The sync fails on the lowest such ordinal and returns, so no
missing pod is created either: a cluster the operator had already started rolling
stays short of pods indefinitely. Deleting the adopted pods by hand, lowest ordinal
first, does clear the wedge — and in the measured run the dataset did **not**
survive that step: the empty replacement of ordinal 0 is still the recorded master,
and the split-brain resolver demoted the drain-promoted pod that held the data. **That
second half is fixed** since [ADR 0028](../adr/0028-a-demotion-may-not-discard-the-only-dataset.md):
the operator no longer demotes a master holding keys toward a recorded one holding
none, so the split brain stays visible instead of costing the dataset. The wedge
itself is not fixed. Treat enabling persistence on an existing cluster
as "stand up a new cluster and restore into it", and back up first
(`valkey-cli --rdb`, or `BGSAVE` plus a copy out of the pod). Both findings, with
their reproductions, are in
[ADR 0023](../adr/0023-volume-claim-templates-are-immutable.md).

### Reverting instead

Reverting `spec.persistence` to what the StatefulSet was created with clears the
block at once — the right move whenever the change was not deliberate. It touches
no pod only when the operator's rendering of the pod template matches what the
StatefulSet already carries; on a cluster whose StatefulSet predates the running
operator version that is usually not the case, and the revert then rides an
ordinary failover-aware rolling update (measured 2026-08-26: reverting
persistence on a live cluster replaced all three data pods, losslessly).

### What happens to the existing volumes

**Recreating does not resize or reclass the volumes that already exist.** The
claims are named `data-<name>-<ordinal>` and are reused by name, so a recreated
StatefulSet binds the same PersistentVolumeClaims it had before — only claims
created *later*, when a scale-out adds a new ordinal, follow the new `size` or
`storageClass`. Growing a volume is an edit on each PersistentVolumeClaim and needs
a StorageClass with `allowVolumeExpansion: true`; changing the class means moving
the data.

**Turning persistence off leaves the data on disk.** The operator never deletes a
PersistentVolumeClaim and sets no `persistentVolumeClaimRetentionPolicy`, so the
old claims and their RDB/AOF files outlive the migration. They are reattached if a
persistent cluster of the same name is created again, and removed only by hand.

## The data-writable pre-flight

**Every persistent data pod runs the init container `check-data-writable` before Valkey starts.**
The pods run as uid 999 ([ADR 0032](../adr/0032-generated-pods-run-rootless.md)); if
`/data`, `/data/appendonlydir` or a regular file directly in either is not writable by
that uid, the pre-flight fails the pod and names the fix in the termination message
`kubectl describe pod` shows — instead of an RDB-mode Valkey starting, serving reads
and answering every write with `MISCONF` while the pod stays Ready. The usual cause is a volume an
older operator wrote as root on storage no pod can re-own (NFS with `root_squash`), or
a StatefulSet recreated by hand over claims that were never migrated; `chown -R
999:999` on the volume fixes both. See the one-time migration under
[upgrading.md](upgrading.md#one-time-migration-to-rootless-pods).
