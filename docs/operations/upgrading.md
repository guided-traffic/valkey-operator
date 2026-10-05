# Upgrading the operator

How to move an installed operator to a new release, what that does to the clusters it
already runs, how to verify it and how to roll it back. **Read the one-time migration
below before upgrading from an operator that still ran Valkey as root.** Installing and
uninstalling are [installation.md](installation.md); every chart value is in the
[Helm chart values](../../README.md#helm-chart-values) table.

## The supported path

`helm upgrade` with the chart is the supported path. The CRD, the operator
ClusterRole and the Deployment all live in the chart's `templates/`, so one
command carries schema, permissions and image forward together:

```bash
helm upgrade valkey-operator deploy/helm/valkey-operator \
  --namespace valkey-operator-system
```

Updating the operator image on its own — `kubectl set image`, or a bumped tag
applied against an older chart — leaves the CRD and the ClusterRole behind and is
not a supported upgrade path.

## One-time migration to rootless pods

> **One-time migration: the release that makes generated pods rootless.** Upgrading
> from an operator that still ran Valkey as root rolls **every Sentinel tier** once —
> tiers of one or two Sentinels serially — and every multi-replica data tier once, a
> persistent one **twice**; restarts the only data pod of a persistent single-replica
> cluster — with or without Sentinel — twice; and re-owns data an older operator wrote as root. On NFS with `root_squash`, act
> **before** the upgrade. Details below and in
> [ADR 0032](../adr/0032-generated-pods-run-rootless.md); the security view of the migration
> is [docs/security/rootless-migration.md](../security/rootless-migration.md).

### What rolls and what restarts

Every data and Sentinel pod now runs as uid/gid 999 with `fsGroup: 999`, the observer
as uid/gid/`fsGroup` 65532 (the operator image's non-root user; ~~its image's non-root
user~~ *pinned 2026-09-26*, [ADR 0033](../adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md)
D4), and every container — the sidecar and the exporter included — with all capabilities
dropped, no privilege escalation, `privileged: false`, a read-only root filesystem and the
`RuntimeDefault` seccomp profile (or the `Localhost` profile [`spec.podSecurity`](../../README.md#specpodsecurity)
names) — the one exception is the migration-only repair container below. There is no CRD
field to switch it off and no opt-out. The same release sets `enableServiceLinks: false` on
every generated pod and pins the default exporter image by digest; those changes are part
of the same pod-spec change and add no roll of their own.
The posture is part of the pod spec, so the upgrade moves each cluster once, a persistent
data tier a second time for the repair container below, and one kind of data pod not at all:

| Cluster | What the upgrade does |
|---|---|
| Multi-replica data tier | Rolls through the failover-aware rolling update, like every roll: the pre-roll dataset survives, and where the roll's failover is forced — Sentinels before Valkey 9.0, or a coordinated failover that fell back — the writes the outgoing master acknowledges during that failover are lost ([below](#writes-during-the-rolls-failover)). Once without persistence; **twice** with it: the second roll replaces the pods created while the template carried the repair container below, once that container has left it — which it does only after the first roll has finalized (its `RollingUpdateComplete`) and every data pod is Ready, so the two rolls run one after the other and report two completions. |
| Sentinel tier | Rolls once, behind the quorum guard; a tier of one or two Sentinels rolls serially (below). Sentinel pods carry no sidecar, so an operator upgrade rolls them only when the release changes their pod spec or configuration — this one does. |
| Observer Deployment | Restarts once; it holds no data. |
| Single replica, persistent (with or without Sentinel) | The only data pod is replaced at the upgrade, and once more when the repair container below has left the template — **two short downtimes**, data kept (it reloads its RDB/AOF each time). The second restart waits until the first replacement is Ready, so the pod serves between the two. The sidecar-only deferral described further down holds back neither. |
| Single replica, not persistent (with or without Sentinel) | **Not restarted**, because that would discard the dataset. The pod keeps running as root and the CR carries `PodSecurityUpdatePending=True` (reason `PodRunsAsRoot`) naming it until the pod restarts for any other reason. `kubectl delete pod <name>-0` applies the posture now and discards the dataset. A new `spec.image`, a certificate rotation or a configuration change still replaces the pod, as it always did. A Sentinel tier beside it rolls regardless. |

"Persistent" is what the data StatefulSet was created with, not what `spec.persistence`
says now: a toggle the operator refused to apply (see
[persistence.md](persistence.md#changing-storage-on-an-existing-cluster))
does not turn an `emptyDir` pod into one that may be restarted.

### A Sentinel tier of one or two pods

**A Sentinel tier of one or two pods rolls serially.** Its quorum equals its pod count,
so it has no spare vote for the quorum guard to spend. It replaces one Sentinel at a
time instead, and only while every other Sentinel is available; the terminating-pod
gate still applies, and a Sentinel that is not Ready holds no vote and is replaced
without the guard, as in every tier. What it costs: while the replaced Sentinel
restarts the tier cannot reach its quorum, so no automatic failover happens for those
seconds — the same as any single Sentinel failure costs a tier sized to tolerate
none — and with one Sentinel there is no Sentinel to ask for the master either. The
data pods keep serving. Tiers of three or more are unchanged: at least the quorum
remains after every delete. Decided 2026-09-26
([ADR 0024](../adr/0024-the-sentinel-tier-reports-its-own-completion.md) D10); before
that the guard refused every delete in such a tier, the roll requeued without end with
the status frozen on `Sentinel Rolling Update`, and no Sentinel change — an image, a
certificate rotation, this posture — ever reached it.

### Data an older operator wrote as root

**Data an older operator wrote as root.** Where kubelet applies `fsGroup` to the
volume type (CSI drivers with an `fsType`, in-tree `local`), it re-groups the files
itself. Where it does not — `hostPath` (Kind's local-path provisioner), NFS,
CSI drivers with `fsGroupPolicy: None` — the files stay root-owned, so the data
StatefulSet of every persistent cluster carries the migration-only init container
`fix-data-ownership` while the migration runs. It runs as root with `CAP_CHOWN` as its
only capability, and re-owns everything under `/data` that uid 999 does not own. It
cannot tell the two kinds of storage apart, so it runs on both. It is best-effort and
always exits 0, so the pre-flight below is the one gate. When the repair leaves the
template, why its removal waits for the first roll to finalize, and how the second roll
sheds it from the pods that still carry it is
[`fix-data-ownership`, the one root process](../security/rootless-migration.md#fix-data-ownership-the-one-root-process).

**The pre-flight stays**, permanent and not migration-only: what `check-data-writable` checks
on every persistent data pod and how to fix a volume it refuses is
[the data-writable pre-flight](persistence.md#the-data-writable-pre-flight).

### NFS with `root_squash`

**NFS with `root_squash`: re-own the data before upgrading.** Root is squashed on the
server, so no pod can re-own the files. Run `chown -R 999:999` on every Valkey volume
server-side first. Otherwise the pre-flight holds the first replaced pod: on a
multi-replica cluster the roll stops there while the pods not yet replaced keep
serving, and `PodAvailabilityStalled` names the held pod once it has been unavailable
for longer than [`syncTimeout`](../../README.md#specrollingupdate); a persistent single-pod cluster is
down until the files are re-owned. After a late fix, delete the held pod so it starts
again without waiting out its crash backoff.

### Debugging, rollback and enforcing `restricted` afterwards

**The root filesystem is read-only.** Debug with `kubectl debug` (an ephemeral
container), not by writing into a running container.

**Rollback** to the previous operator is safe for the data: its pods run as root and
can write the re-owned files.

**Afterwards a namespace can enforce Pod Security `restricted`.** ~~Once no Valkey pod
in it runs a container as root — every cluster back to `PHASE=OK`, no
`PodSecurityUpdatePending=True`, and no data StatefulSet and no data pod still listing
`fix-data-ownership`, which `restricted` refuses — label it,~~ *(corrected 2026-09-27)*
Once no data StatefulSet in it still lists `fix-data-ownership` in its template (the
first command below) — while one does, `restricted` refuses every pod the StatefulSet
creates from it, and the repair leaves the template only once every ordinal holds a
Ready rootless pod and no roll is recorded — label it, server-side dry run first;
the dry run lists the pods that would violate. ~~Check the pods and not only the
templates: a template is clean before the second roll begins, its pods only once that
roll has replaced them.~~ *(Corrected 2026-09-27: those pods do not block the label.
A pod still listing `fix-data-ownership` (the second command) or still running as root
keeps running under the label, and its replacement comes from the clean template.
Check the pods to know when the namespace no longer runs a root container: after the
second roll, and for a pod `PodSecurityUpdatePending` names, once it is replaced —
[H-21](../security/rootless-migration.md#h-21). Why the template and not the pods is
the precondition: [H-22](../security/rootless-migration.md#h-22).)* The operator does
not label namespaces.

```bash
kubectl get statefulset -n <ns> -o jsonpath='{range .items[*]}{.metadata.name}: {.spec.template.spec.initContainers[*].name}{"\n"}{end}'
kubectl get pod -n <ns> -o jsonpath='{range .items[*]}{.metadata.name}: {.spec.initContainers[*].name}{"\n"}{end}'
kubectl label --dry-run=server --overwrite ns <ns> pod-security.kubernetes.io/enforce=restricted
kubectl label --overwrite ns <ns> pod-security.kubernetes.io/enforce=restricted
```

Not covered: OpenShift, whose `restricted-v2` SCC refuses a fixed `runAsUser` outside
the namespace's UID range. Nothing in this repository targets it.

## The exporter update and the narrower NetworkPolicies

The release that pins the metrics exporter to v1.92.1 changes two things on running clusters.
Both apply at the upgrade, with no switch to keep the old behaviour.

**The exporter.** The default `spec.metrics.image` moves from v1.66.0 to v1.92.1, and the
exporter gets two variables that switch off its `/scrape` route and the export of key values
([monitoring.md](monitoring.md#the-exporter-sidecar)). Both are part of the pod spec:

| Cluster with `spec.metrics.enabled` | What the upgrade does |
|---|---|
| Multi-replica data tier | Rolls once through the failover-aware rolling update, together with the sidecar image every operator upgrade moves ([below](#what-an-upgrade-does-to-running-clusters)). |
| Single replica, persistent (with or without Sentinel) | The only data pod is replaced once — a short downtime, the data kept on its volume. |
| Single replica, not persistent (with or without Sentinel) | **Not restarted**, because that would discard the dataset. The pod keeps the old exporter, `/scrape` included, and the CR carries `PodSecurityUpdatePending=True` with reason `ExporterOutdated` naming it until the pod restarts for another reason. `kubectl delete pod <name>-0` applies the update now and discards the dataset. |
| Own `spec.metrics.image` | Used as given. An image older than v1.83.0 starts but keeps `/scrape`; move it to v1.83.0 or later. |

Upstream v1.90.0 changed the keyspace metrics; check dashboards and alerts built on the
exporter's `redis_*` series after the upgrade.

**The NetworkPolicies** (only on clusters with `spec.networkPolicy.enabled`). The first reconcile
rewrites the generated policies to admit only the operator's own components
([network-policy.md](network-policy.md)):

- **A scraper that reached the exporter (`9121`) or the observer (`8084`) through the old
  any-source rule loses that access.** Write the policy that admits it
  ([example](network-policy.md#admitting-a-scraper)) before upgrading, and it keeps scraping.
- Pods in the operator's namespace other than the operator lose access to the data and
  Sentinel ports.
- The operator is admitted as its pod. The chart upgrade brings the label and the flag that
  requires; an operator installed without the chart must set `POD_NAMESPACE` and
  `--operator-pod-selector` ([how the operator pod is recognised](network-policy.md#how-the-operator-pod-is-recognised)),
  or it cannot reach the pods wherever the policies are enforced.
- **Policies that an earlier release left behind are deleted.** Turning
  `spec.networkPolicy.enabled` off used to leave the last written policies in place; the
  first reconcile after the upgrade deletes the ones the resource owns on every cluster whose
  spec no longer asks for them — the policy is off, Sentinel or the observer is off, or the
  `namePrefix` changed.

## What an upgrade does to running clusters

**Before the new operator starts.** The chart runs a `pre-upgrade` hook Job
(`valkey-operator-pre-upgrade`) that executes `manager migrate` and writes the
current field defaults into existing `Valkey` CRs, so the new operator never
reconciles a CR that predates its defaults. It is enabled by default; skip it
with `--set preUpgradeHook.enabled=false`.

**What it does to running clusters.** The sidecar that runs in every data pod (Sentinel
pods carry none), and the observer, use the operator image (`--operator-image`, set by the chart from
`image.repository:tag`, with `@image.digest` appended when that value is set), so an
upgrade that changes the operator tag — or setting or changing `image.digest` — changes
the managed pod spec. Every multi-replica cluster is then migrated once through the
failover-aware rolling update — replicas first, then a controlled failover, then
the former master. The pre-roll dataset survives; on a Sentinel cluster whose Sentinels
cannot run a coordinated failover (before Valkey 9.0, the 8 to 9 upgrade roll included)
and on any roll whose coordinated failover fell back to forced, the writes the outgoing
master acknowledges during the roll's failover are lost. *(corrected 2026-09-28: this
said "without data loss")*

### Writes during the roll's failover

**A Sentinel cluster's roll asks for a coordinated failover, and falls back to the forced
one where the Sentinels cannot run it** — unless it has one data pod, which has nothing to fail
over to and asks for none ([below](#a-single-replica-cluster)) ([the master handover](rolling-updates.md#the-master-handover-on-a-sentinel-cluster),
[ADR 0037](../adr/0037-the-master-handover-loses-no-acknowledged-write-and-no-dataset.md)
D1). The operator that runs the roll sends the command, so the roll an operator upgrade
starts already asks for the coordinated failover (read from the code). Two things follow
for an upgrade:

- **The roll that moves `spec.image` from Valkey 8 to Valkey 9 is forced.** The Sentinel
  tier runs `spec.image` too and rolls after the data tier, so the data tier's failover
  is asked of Sentinels still on Valkey 8, which refuse the coordinated command: the
  writes the outgoing master acknowledges during that failover are lost. The first
  coordinated roll is the next one. A cluster that stays on Valkey 8 keeps the forced
  failover on every roll.
- **A coordinated failover can block writes for up to 60 s.** In its stall shape — the
  selected replica not online when Sentinel acts, or never catching up — the outgoing
  master holds every write for up to Sentinel's `failover-timeout` (60 s) and then
  aborts: clients see a hang and a disconnect, not an error. The roll then falls back to its
  forced retrigger, which loses that failover's window of writes.

### A single-replica cluster

**A single-replica cluster — with or without Sentinel — is not restarted for this.** A
sidecar-only delta on the only data pod has no failover target, so the operator
deliberately does not apply it: it sets the `SidecarUpdatePending` condition on the
`Valkey` CR and leaves the pod running the **old** sidecar image. There is no
downtime and nothing to schedule — but there is also no automatic convergence: the
pod keeps the old sidecar until something restarts it, which means a manual
`kubectl delete pod`, an eviction, a new `spec.image`, a configuration change or a
certificate rotation — those three replace the pod even while its sidecar is old. A change
of the pod spec alone (resources, affinity, the exporter) does not: it waits with the
sidecar, and the condition stands for both. Force it when you want it — but the restart is
not free on the only pod of the cluster: it has no failover target, so an instance
without `persistence.enabled` comes back empty (with persistence it reloads its
RDB/AOF). This is the same exception as the
[metrics note](monitoring.md#enabling-metrics-on-a-running-cluster). **The
rootless release does restart a persistent single-replica cluster**: a pod that still
runs as root is decided by persistence, not by the sidecar image — see the one-time
migration above.

**A Sentinel cluster with one data pod** — what `sentinel.enabled: true` gives without
`spec.replicas` — follows these single-pod rules too, and its Sentinel tier rolls after the
data pod, or in the same pass when the data pod's change is held back
([ADR 0007](../adr/0007-failover-aware-rolling-update.md) D11). Releases before this one sent it
through the failover roll, which has no replica to promote: the operator asked Sentinel for a
failover about every 15 s, never replaced the pod and never rolled the Sentinel tier, and every
change of either tier — the rootless posture and rotated TLS material included — stayed
unapplied. The first reconcile after upgrading takes what that held by the rules above: the data
pod is replaced when its image, its configuration or its TLS certificate changed in the meantime
— without persistence its dataset is gone — or when it is persistent and still runs as root
(twice); a non-persistent pod still running as root is held and named by
`PodSecurityUpdatePending`; anything else waits with the upgrade's sidecar. The Sentinel tier
rolls in every case.

```bash
kubectl get valkey <name> -o jsonpath='{range .status.conditions[?(@.type=="SidecarUpdatePending")]}{.status}{"\n"}{end}'
kubectl delete pod <name>-0        # the deferred sidecar update applies on recreation
```

The condition clears itself once the deferred update has applied; [`SidecarUpdatePending`](status.md#sidecarupdatepending) names the two passes that clear it ([ADR 0002](../adr/0002-surface-a-blocked-reconcile-on-the-cr.md) D10). To confirm the running sidecar directly rather than through the condition:

```bash
kubectl get pod <name>-0 -o jsonpath='{range .spec.containers[*]}{.name}={.image}{"\n"}{end}'
```

## Verify

```bash
kubectl -n valkey-operator-system rollout status deployment/valkey-operator
kubectl get valkey -A
```

Every instance returns to `PHASE=OK` with `READY` equal to `REPLICAS`. `Rolling
Update` means the migration above is still running; `kubectl describe valkey
<name>` shows the current step and any `ReconcileBlocked` condition. A roll stuck on
a pod that does not come up carries `PodAvailabilityStalled` naming that pod once
[`syncTimeout`](../../README.md#specrollingupdate) has passed: read its events with
`kubectl describe pod` and fix the cause — after a spec fix the operator
[replaces the stuck pod itself](rolling-updates.md#a-pod-that-never-comes-up). A
single-pod cluster without Sentinel reports it differently, as the end of
[`PodAvailabilityStalled`](status.md#podavailabilitystalled) explains. A Sentinel
cluster's roll that holds the delete of its former master carries
[`MasterHandoverStalled`](status.md#masterhandoverstalled) once `syncTimeout` has passed,
with the repair in its message.

## Upgrading from the released chart repository

**Upgrading from the released chart repository** instead of a checked-out tree:

```bash
helm repo add valkey-operator https://guided-traffic.github.io/valkey-operator/
helm repo update
helm upgrade valkey-operator valkey-operator/valkey-operator \
  --namespace valkey-operator-system
```

## Rollback

```bash
helm rollback valkey-operator --namespace valkey-operator-system
```

The CRD is part of the release, so a rollback restores the previous CRD schema as
well. Spec fields that only the newer schema knows are pruned from existing CRs by
the API server, so roll back before adopting new fields, or re-apply them after
upgrading again.
