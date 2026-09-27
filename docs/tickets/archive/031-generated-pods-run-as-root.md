---
id: T31
title: generated Valkey and Sentinel pods run as root with the runtime's default capabilities
state: done
severity: high
security: hardening
threat: "bounds code execution inside valkey-server, valkey-sentinel or an init script (a server bug reachable by an authenticated client, or a CR author's own spec.image): today uid 0 with 14 capabilities incl. NET_RAW, DAC_OVERRIDE, SETUID and no seccomp filter; after: uid 999, zero capabilities, no_new_privs, RuntimeDefault seccomp, read-only root filesystem"
urgency: next
effort: L
blocked-by: T32 (done 2026-09-26)
filed-from: user request, 2026-09-25
opened: 2026-09-25
decided: 2026-09-26
done: 2026-09-26
reopened: 2026-09-26 (extension: the full pod-manifest hardening, ADR 0033)
extension-done: 2026-09-26
---

Goal as decided: **every pod the operator generates runs rootless, and every existing cluster
is moved onto that posture by the operator upgrade.** Root was a defect, not a setting; there
is no field and no opt-out. Claims below are labelled the way ADR 0017 D36 asks: **run**
(measured here), **read** (read in the tree at `9925539` or in upstream docs), **hypothesis**
(reasoned, not checked).

## Fact

### Why the processes are root

The builder sets no `securityContext` on any pod or container — a stated decision,
[ADR 0013](../../adr/0013-operator-is-cluster-wide-privileged.md) D9 (read:
`grep -rn SecurityContext internal/builder` matches nothing). Kubernetes therefore runs each
container as the image's user, with the runtime's default capability set, with seccomp
`Unconfined` (the Kubernetes default unless the kubelet runs `--seccomp-default`; read,
upstream) and a writable root filesystem.

The upstream Valkey image declares **no `USER`**. It is built to start as root and drop
privileges itself: `docker-entrypoint.sh` chowns the working directory to `valkey` and re-execs
through `setpriv --reuid=valkey --regid=valkey --clear-groups` (run: read out of both pinned
images). **The operator never runs that entrypoint.** Every container on the Valkey image sets
`command:`, which replaces the image `ENTRYPOINT`:

| Container | Image | `command` | Identity today |
|---|---|---|---|
| `valkey` | `spec.image` | `valkey-server <conf>`, or `sh -c 'exec valkey-server …'` under auth ([`statefulset.go:816-827`](../../../internal/builder/statefulset.go)) | **uid 0, 14 caps** (run) |
| `init-config-selector` | `spec.image` | `sh -c` ([`statefulset.go:272-276`](../../../internal/builder/statefulset.go), `:433-437`) | **uid 0, 14 caps** (read: same mechanism) |
| `sentinel` | `spec.image` | `valkey-sentinel <conf>` ([`sentinel.go:514-520`](../../../internal/builder/sentinel.go)) | **uid 0, 14 caps** (read) |
| `init-sentinel-config` | `spec.image` | `sh -c` ([`sentinel.go:343-351`](../../../internal/builder/sentinel.go)) | **uid 0, 14 caps** (read) |
| `sidecar` | operator image | `./manager sidecar` | uid 65532, distroless `nonroot` ([`Containerfile:35`](../../../Containerfile)); no `no_new_privs`, unconfined seccomp (read) |
| `exporter` | `spec.metrics.image`, default `oliver006/redis_exporter:v1.66.0` | image entrypoint | uid 59000, image `USER 59000:59000` (run) |
| `observer` (Deployment) | operator image | `./manager observer` | uid 65532 (read) |

So the operator runs Valkey with **less** isolation than the upstream image has when it is
started the way its authors intended. The root half of the fleet is exactly the four containers
that run the Valkey image.

### Measured: today's shape against a restricted runtime

Docker 28.4.0 (Docker Desktop), 2026-09-25, pins `valkey/valkey:9.1.1` and `valkey/valkey:8.1.9`
from [`test/testimages/images.go`](../../../test/testimages/images.go). All **run**.

- **Image facts.** `id valkey` → `uid=999(valkey) gid=999(valkey)` in both pins; `WORKDIR /data`;
  `User=""`; `Entrypoint=["docker-entrypoint.sh"]`. Exporter image: `User="59000:59000"`.
- **Today's shape** (`--entrypoint valkey-server`, the Docker equivalent of `command:`),
  `/proc/1/status`: `Uid: 0`, `CapEff: 00000000a80425fb`, `NoNewPrivs: 0`. The mask decodes to
  CHOWN, DAC_OVERRIDE, FOWNER, FSETID, KILL, SETGID, SETUID, SETPCAP, NET_BIND_SERVICE, NET_RAW,
  SYS_CHROOT, MKNOD, AUDIT_WRITE, SETFCAP — Docker's default set; containerd's CRI default is the
  same list (read, not measured on a node).
- **Restricted runtime** (`--user 999:999 --read-only --cap-drop ALL --security-opt
  no-new-privileges`, a tmpfs on `/data` and on the Sentinel config dir standing in for the
  emptyDirs, Docker's default seccomp filter active — `Seccomp: 2`), both pins:
  `Uid: 999`, `CapEff: 0`, `CapBnd: 0`, `NoNewPrivs: 1`; `BGSAVE` → `rdb_last_bgsave_status:ok`;
  `BGREWRITEAOF` → `aof_last_bgrewrite_status:ok`; `aof_last_write_status:ok`; `valkey-cli ping`
  through `docker exec` (the identity an exec probe gets) → `PONG`; no warning in the server log.
  `valkey-sentinel` starts, and `SENTINEL SET m down-after-milliseconds 5000` rewrites the config
  file on the tmpfs. `sed -i` and `sha1sum`, which the Sentinel init script uses, work under the
  read-only root. The exporter starts under the same flags as uid 999.

Nothing the operator runs inside the Valkey image needs root, a capability, or a writable root
filesystem — **as measured in Docker**. The Kubernetes half is under "Not verified".

### Measured: an existing dataset written by today's pods, then opened by a uid-999 pod

The legacy writer is today's shape: root, 14 capabilities, umask `0022` (the entrypoint that
would have set `0077` is bypassed). It leaves `root:root 0644` files and a `root:root 0755`
`appendonlydir`. Then the restricted runtime opens the same volume (`-v vol:/data:nocopy`, so the
volume root really has the simulated mode — without `nocopy` Docker copies the image's
`1777 999:999` `/data` onto the empty volume and masks the case). All **run**, `9.1.1`:

| Persistence | Volume root | Stands for | Restricted result |
|---|---|---|---|
| RDB | `0755 root` | fresh ext4/xfs PV root, or a root-created `hostPath`, **kubelet not applying fsGroup** | Starts, serves reads, answers `PONG` — then `BGSAVE` fails (`Failed opening the temp RDB file temp-28.rdb (in server root dir /data) for saving: Permission denied`) and, because the generated config sets `stop-writes-on-bgsave-error yes` ([`configmap.go:209`](../../../internal/builder/configmap.go)), **every write returns `MISCONF` while the pod stays Ready**. A silent write outage. |
| RDB | `0777 root` | rancher local-path (Kind, k3s), nfs-subdir | Works: replacing a root-owned `dump.rdb` by rename needs only write on the directory. |
| AOF | `0755` or `0777` | either | Exits 1: `Can't open the append-only file appendonly.aof.1.incr.aof: Permission denied` → CrashLoopBackOff. |
| AOF | `0755`, after `chgrp -R 999`, `chmod -R g+rwX`, setgid on dirs — a simulation of what kubelet does for `fsGroup` | storage where kubelet applies fsGroup | Works. |
| AOF | `0755`, after `find /data ! -user 999 -exec chown 999:999 {} +` as uid 0 with **only `CAP_CHOWN`** (read-only root, `no_new_privs`) | the ownership-repair init container | Works. |

Operator downgrade onto repaired, `999`-owned data: today's shape (root with `DAC_OVERRIDE`)
starts and writes, so rolling the operator back is safe. **uid 0 with every capability dropped
does not** — same `Can't open the append-only file … Permission denied` — which is why no
"root without capabilities" intermediate posture exists in the decision.

**Hypothesis, not measured**, and the reason the RDB/`0755` row is worse than it looks in a
replicated cluster: a restarted replica holding a readable `dump.rdb` can resume by partial
resync without writing a file, so it satisfies the rolling update's "synced" check
([ADR 0007](../../adr/0007-failover-aware-rolling-update.md) D10) and can be promoted. The
`MISCONF` then lands on the new master. The pre-flight in the decision exists for this row.

### Code facts that shape the fix (read)

- **The pod-spec hashes cover the whole built `PodSpec`** — `json.Marshal` of `buildPodSpec` /
  `buildSentinelPodSpec` ([`statefulset.go:1217-1223`](../../../internal/builder/statefulset.go),
  [`sentinel.go:578-584`](../../../internal/builder/sentinel.go)), kept that way by
  [ADR 0005](../../adr/0005-upgrade-neutral-defaults-and-anti-affinity.md) D7. The securityContext
  therefore reaches every existing pod through the ordinary failover-aware roll, the Sentinel
  tier included — which a plain operator upgrade otherwise never rolls (ADR 0005 D11). The one
  thing that must **not** ride the hash is the migration-only repair container (decision D2).
- `podSpecChanged` compares a field list without `SecurityContext`
  ([`statefulset.go:1252-1274`](../../../internal/builder/statefulset.go), `containerChanged`
  `:1303-1322`). The hash annotation still carries the change to the StatefulSet, but an
  out-of-band edit of the persisted template's securityContext would never be converged back —
  the gap the `AutomountServiceAccountToken` line closes for its field. `InitContainers` *are*
  compared, which is what lets the repair container come and go without a hash change.
- `ObserverDeploymentHasChanged` compares replicas, identity, container count, image, args and
  resources, nothing else, and the observer carries no hash annotation
  ([`observer.go:332-364`](../../../internal/builder/observer.go)). **Without a new comparison
  line an existing observer Deployment never receives a securityContext.**
- **Single-pod clusters decide by image, not by hash.** `handleStandaloneRollingUpdate`
  ([`rolling_update.go:3364`](../../../internal/controller/rolling_update.go) ff.) defers a pod
  update on a `replicas: 1` cluster when `isSidecarOnlyChange` says so, and that function
  compares only the `valkey` and `sidecar` images. A release ships a new sidecar image *and* the
  new securityContext, so on the canonical Helm path the fix would be classified sidecar-only and
  deferred under `SidecarUpdatePending` with the message "outdated sidecar image"; on kustomize or
  a floating tag the sidecar image does not move, and the only pod is deleted at once — for a
  non-persistent cluster, with its data. [ADR 0007](../../adr/0007-failover-aware-rolling-update.md)
  D7 foresaw exactly this: any change beyond the sidecar image to a single-pod spec "must be
  treated as a data-loss change". Decision D3 is that treatment.
- The operator's own Deployment already runs `runAsNonRoot` without `runAsUser`,
  `seccompProfile: RuntimeDefault`, `allowPrivilegeEscalation: false`,
  `readOnlyRootFilesystem: true`, `drop: [ALL]`
  ([`deployment.yaml:30-33, 54-58`](../../../deploy/helm/valkey-operator/templates/deployment.yaml)).
  The same binary is the sidecar and the observer, and the image user is numeric (kubelet would
  refuse `runAsNonRoot` otherwise), so those two need nothing new to run hardened.
- With persistence disabled the data volume is an `emptyDir`
  ([`statefulset.go:577-585`](../../../internal/builder/statefulset.go)) and the config carries no
  `dir` directive ([`configmap.go:190-198`](../../../internal/builder/configmap.go)), so a replica's
  full-sync RDB lands in the CWD — the image's `WORKDIR /data`, i.e. the emptyDir. True for the
  upstream image by inheritance only.
- The drain handshake crosses no uid boundary it cares about: the sidecar writes the marker with
  mode `0600` ([`internal/sidecar/drain.go:343`](../../../internal/sidecar/drain.go)); the `valkey`
  container's preStop only tests `[ -f … ]`
  ([`statefulset.go:740-757`](../../../internal/builder/statefulset.go)), which needs search on the
  directory, not read on the file.
- The sidecar token projection is `DefaultMode 0644`
  ([`statefulset.go:704`](../../../internal/builder/statefulset.go), measured necessary in
  [ADR 0031](../../adr/0031-a-record-the-operator-trusts-lives-in-pod-spec.md)). **Hypothesis:** with
  a pod `fsGroup`, kubelet rewrites projected ServiceAccount tokens to `0600`, owned by the pod's
  uniform `runAsUser`, or group-readable via `fsGroup` when uids differ. Either way the sidecar
  reads it or CrashLoopBackOffs loudly, which the e2e tier sees.
- **Three** production call sites build pod templates:
  [`valkey_controller.go:1225`](../../../internal/controller/valkey_controller.go)
  (`BuildStatefulSet`), `:1372` (`BuildSentinelStatefulSet`), `:1946`
  (`BuildObserverDeployment`). `reconcileStatefulSet` builds before it `Get`s the live object
  (`:1225`, `:1233`).
- RBAC: nothing in the decision needs a new grant — `securityContext` and `fsGroup` are pod
  fields, and the migration evidence is read off data pods the operator already `get`s and
  `list`s ([`valkey_controller.go:195-212`](../../../internal/controller/valkey_controller.go)).
- **A replaced pod that never becomes available is never replaced again by the operator** —
  filed as [T32](032-unavailable-replaced-pod-waits-unbounded.md), which this ticket was
  blocked by. **T32 is done (2026-09-26, ADR 0026 D11)**: such a pod is reported as
  `PodAvailabilityStalled` past `syncTimeout`, and an outdated one is replaced after a fix.
- The fleet-upgrade e2e (`TestE2E_FleetUpgrade`, build tag `fleetupgrade`) is the only test that
  starts from pods a **released** operator built, i.e. from real root-written data. It runs from
  the Makefile only (`make e2e-fleet-upgrade-local`, `test-e2e-fleet-upgrade`); no workflow in
  `.github/workflows` runs it.

**Verified:** everything under both "Measured" sections (run, both pins where stated); every
file:line above (read at `9925539`).
**Not verified:** anything on a Kubernetes node. Specifically: which PV type Kind's local-path
provisioner creates and hence whether kubelet applies `fsGroup` there (expected `hostPath`, i.e.
no); containerd's `RuntimeDefault` profile against Valkey (Docker's moby profile was measured, and
containerd's is derived from it); the projected-token mode under `fsGroup`; CRI-O's smaller
default capability set; OpenShift SCC behaviour; the partial-resync promotion path above. No e2e
was run and no cluster was touched.

## Impact

Severity is `high` because two things hold on every cluster the operator builds today:

- **Code execution inside `valkey-server` or `valkey-sentinel` lands as uid 0 with `NET_RAW`
  (raw packets on the pod network), `DAC_OVERRIDE`/`FOWNER` (file modes mean nothing on anything
  the pod mounts), `SETUID`/`SETGID`, `MKNOD`, `SYS_CHROOT`, and no seccomp filter** — the widest
  kernel surface a `baseline` namespace admits. Principals: an authenticated client exploiting a
  server bug (the Lua scripting engine is the historical source of such bugs), or a CR author,
  who picks `spec.image` ([ADR 0015](../../adr/0015-one-crd-validated-by-schema-only.md) D6). Live
  posture on every pod, dormant until such a bug is used.
- **A namespace enforcing Pod Security `restricted` rejects every generated pod.** The
  StatefulSet is created; the statefulset-controller's pod creates are refused. The operator
  cannot be deployed into a hardened namespace at all
  ([ADR 0013](../../adr/0013-operator-is-cluster-wide-privileged.md) residual risks). Live.

What **the fix itself** does to the fleet, because it runs at the operator upgrade, per case:

- Every multi-replica data tier rolls once, failover-aware and lossless — as on every release
  (ADR 0005 D11). Every **Sentinel tier rolls once**, which a plain upgrade otherwise never does.
- Persistent data on storage where kubelet applies `fsGroup` (CSI with an `fsType` and RWO,
  `fsGroupPolicy: File`, in-tree `local`): nothing to do, kubelet re-groups the files.
- Persistent data on storage where it does not (`hostPath`/local-path, NFS, `fsGroupPolicy:
  None`): the repair container re-owns the files once during the roll. **NFS with
  `root_squash`** is the case no pod can repair — root is squashed, `chown` fails, the pre-flight
  holds the first replica. Needs a server-side `chown` before the upgrade.
- Persistent single-pod clusters: one restart, i.e. downtime, at the upgrade; data kept.
- Non-persistent single-pod clusters: not restarted; they keep running as root until their next
  natural restart and say so in a condition.
- Observer Deployments: one restart each; they hold no data.

## Options

The decision round of 2026-09-26 withdrew the first framing (a per-cluster level field; see
History). What remained were three questions, each decided on the mechanism below.

### Q1 — who re-owns root-written data on storage without fsGroup support

`fsGroup: 999` and the pre-flight `check-data-writable` are part of every option; Sentinel,
observer and non-persistent pods need no repair, their emptyDirs are fresh.

| | What | Cost |
|---|---|---|
| **M1** (chosen) | While any data pod of the StatefulSet still runs without `runAsNonRoot` — built by an earlier operator — the template carries `fix-data-ownership` (uid 0, only `CAP_CHOWN`). It leaves the template when no such pod remains, without a second roll, because the pod-spec hash is computed without it. | Root runs once per pod for a fraction of a second during the migration, also where `fsGroup` alone would have sufficed. A StatefulSet re-created by hand over PVCs that were never migrated gets no repair; the pre-flight stops it loudly and a manual `chown` fixes it. |
| M2 | Repair only after the pre-flight proves it is needed (own exit code); the operator adds the container and deletes exactly that pod. | Root only where physically needed and the PVC edge case covered, but one failed start per affected cluster and a new "delete an unavailable pod" exception in ADR 0026, the rule that was incomplete three times. |
| M3 | Strictly no root container; `fsGroup` and pre-flight only. | Clusters with legacy data on such storage stop at their first replica at the upgrade, single-pod ones are down, until an administrator runs a manual `chown`. |

### Q2 — single-pod clusters, which have no failover target

| | What | Cost |
|---|---|---|
| **S1** (chosen) | Persistent: replaced at the upgrade. Non-persistent: deferred to the next natural restart, reported by its own condition. | Downtime for persistent single-pod clusters; non-persistent ones stay root until they restart. The line sits where a restart turns from downtime into data loss (the principle of [ADR 0028](../../adr/0028-a-demotion-may-not-discard-the-only-dataset.md)). |
| S2 | All deferred. | No downtime at the upgrade; every single-pod cluster stays root until it restarts, possibly for weeks. |
| S3 | All replaced at the upgrade. | Fastest; non-persistent single-pod clusters lose their data to an operator upgrade. |

### Q3 — the unbounded wait found on the way

| | What | Cost |
|---|---|---|
| **D1** (chosen) | File T32; T31 does not ship before it. | T31 waits for T32. |
| D2 | File T32, independent; T31 ships with the manual runbook. | A fleet-wide automatic roll where a stuck cluster shows no condition. |
| D3 | Fold into T31. | T31 grows into ADR 0010 and the rolling update; ADR 0017 D38 says file it instead. |
| D4 | No ticket; the runbook suffices. | The gap stays open for every future roll. |

## Decision

Decided 2026-09-26 by Hans. Supersedes the open recommendation of 2026-09-25 (History).

- **D1 — Every generated pod is rootless, with no option.** Data and Sentinel pods: pod-level
  `runAsNonRoot: true`, `runAsUser: 999`, `runAsGroup: 999`, `fsGroup: 999`,
  `seccompProfile: RuntimeDefault`; every container and init container, sidecar and exporter
  included: `allowPrivilegeEscalation: false`, `readOnlyRootFilesystem: true`,
  `capabilities.drop: [ALL]`. Observer: `runAsNonRoot` and `RuntimeDefault` at pod level (its
  image user 65532 is numeric) plus the same three container fields. No CRD field, no
  `baseline` level, no opt-out. Root was a defect: [ADR 0005](../../adr/0005-upgrade-neutral-defaults-and-anti-affinity.md)
  D1's "new features default to off" governs features, not the repair of a defect, so existing
  clusters move with the operator upgrade through the ordinary failover-aware roll.
- **D2 — Data ownership (M1).** `fsGroup: 999` with `fsGroupChangePolicy` unset (= `Always`:
  `OnRootMismatch` inspects only the volume root and would skip files a later root writer left
  behind; a Valkey data dir holds a handful of files). Every persistent data pod runs the
  pre-flight `check-data-writable` (uid 999, shell builtins only), which fails the pod with a
  message naming the fix when `/data`, `/data/appendonlydir` or any file in them is not
  writable. While any data pod of the StatefulSet runs without `runAsNonRoot`, the template
  carries `fix-data-ownership` in front of it — uid 0, `drop: [ALL]`, `add: [CHOWN]`, read-only
  root, `no_new_privs`, `find /data ! -user 999 -exec chown 999:999 {} +`. The pod-spec hash is
  computed **without** it, so adding and removing it rolls nothing: a narrow, recorded exception
  to ADR 0005 D7, justified because the container only acts at pod start and a pod that ran it
  is identical to one that did not need it. No repair for Sentinel, observer or non-persistent
  pods. In steady state no root process runs anywhere.
- **D3 — Single-pod clusters (S1).** On a `replicas: 1` data cluster whose pod runs without
  `runAsNonRoot`: persistent ⇒ replaced at once, the repair running on its way up;
  non-persistent ⇒ deferred until the pod restarts for another reason, reported by a condition of
  its own. The image-only `isSidecarOnlyChange` no longer decides this case; amends
  [ADR 0007](../../adr/0007-failover-aware-rolling-update.md) D6 and D7.
- **D4 — T32 first (D1 of Q3).** The unbounded wait on a never-available replaced pod is
  [T32](032-unavailable-replaced-pod-waits-unbounded.md); T31 is not released before it,
  because T31 is the first change that rolls every cluster of a fleet automatically.
- **D5 — Withdrawn with the field:** the e2e level selector and every `baseline` code path.

## Plan

Target shape — the only shape the operator renders once this lands:

```yaml
# data and Sentinel pods, pod level
securityContext:
  runAsNonRoot: true
  runAsUser: 999          # the image's `valkey` user, measured in both pins
  runAsGroup: 999
  fsGroup: 999            # fsGroupChangePolicy deliberately unset (= Always)
  seccompProfile:
    type: RuntimeDefault
# every container and init container of those pods, sidecar and exporter included
securityContext:
  allowPrivilegeEscalation: false
  readOnlyRootFilesystem: true
  capabilities:
    drop: ["ALL"]
# observer pod: runAsNonRoot + RuntimeDefault at pod level, the same three container fields
# migration only (D2), data pods with persistence, never part of the pod-spec hash:
#   fix-data-ownership: runAsUser 0, runAsNonRoot false, drop ALL, add CHOWN, readOnlyRootFilesystem
```

**Phase 0 — record**

1. New ADR 0032, titled as the decision ("Generated pods run rootless"): D1–D3 above, the hash
   exception, the pre-flight, the namespace label `pod-security.kubernetes.io/enforce: restricted`
   as what an administrator enforces afterwards. Index line under "Security and API surface".
2. Amend in place, same change: ADR 0005 — D1 scope (features vs defects), D7 exception (repair
   container), D11 (this release rolls the Sentinel tier once); ADR 0007 D6/D7 (explicit
   single-pod rule instead of the image-only guarantee); ADR 0013 D9 superseded, its
   residual-risk bullet closed; ADR 0012 D8 step 4 (token mode under `fsGroup`, once measured);
   ADR 0017 (the evaluator guard, the restricted-namespace e2e, the imagetools restricted check,
   the fleet-upgrade e2e as the migration proof).

**Phase 1 — builder**

3. `internal/builder/pod_security.go` (new): one post-assembly walk that applies the posture to
   **every** container and init container of a `PodSpec`, called by the data, Sentinel and
   observer builders. `buildPodSpec` sits at the gocyclo ceiling (comment at
   [`statefulset.go:208-211`](../../../internal/builder/statefulset.go)), and a walk means a future
   container inherits the posture instead of having to remember it.
4. Data pod: `workingDir: /data` on the `valkey` container (states the inherited `WORKDIR`);
   `check-data-writable` ahead of `init-config-selector` when persistence is on.
5. `WithDataOwnershipRepair(sts)` (or equivalent) inserts `fix-data-ownership` into an
   already-built StatefulSet template, **after** `ComputePodSpecHash` ran — so no signature change
   ripples through the unit fixtures and the hash never sees it. `find` and `chown` join
   `RequiredImageTools` ([`image_requirements.go`](../../../internal/builder/image_requirements.go));
   the walker test forces it.
6. Change detection: pod- and container-level securityContext in `podSpecChanged` with subset
   semantics (compare the fields the operator sets; `nil` ≡ `{}`, the API-server default for an
   absent pod securityContext) — the fleet runs Kyverno (ADR 0003 context), so check whether any
   mutate policy rewrites template securityContext before relying on convergence; a
   securityContext line in `ObserverDeploymentHasChanged` (required, see Fact).

**Phase 2 — controller**

7. Migration evidence per pass, derived, never stored: "a data pod proven ours (`podIsOurs`)
   whose `spec.securityContext.runAsNonRoot` is not `true` exists" ⇒ apply step 5, if persistence
   is on. Pod specs are immutable, so the evidence cannot be forged by a label and survives an
   operator restart.
8. Single-pod rule (D3) ahead of the sidecar-only deferral: a legacy pod on a persistent
   `replicas: 1` cluster is replaced even when the image difference alone would read
   sidecar-only; a non-persistent one is deferred with a new condition — a level with one
   evaluator and a `conditionRegistry` row ([ADR 0027](../../adr/0027-conditions-are-levels-edges-or-history.md)),
   Event-free (ADR 0025 D7).
9. No new RBAC, no CRD change.

**Phase 3 — tests** (ADR 0017: a mutation or revert check per guard, a positive control per
negative set)

10. Unit, PSS evaluator: `k8s.io/pod-security-admission/policy` at the `k8s.io/api` version
    (currently v0.37.0) — the checks the API server's PodSecurity admission runs, as a test-only
    import in the k8s Renovate group. Matrix {standalone, 3 replicas, 3+3 Sentinel} × TLS × auth ×
    metrics × persistence {off, rdb, aof} over the data StatefulSet, Sentinel StatefulSet and
    observer templates: every rendered template ⇒ `restricted` allowed; the data template with
    the repair inserted ⇒ `baseline` allowed and `restricted` denied (both sides, D12 — and the
    positive control that the evaluator can fail at all).
11. Unit: `readOnlyRootFilesystem` and `drop: [ALL]` on every container (PSS does not require
    the read-only root); the repair insertion leaves `ComputePodSpecHash` unchanged while any
    other posture field changes it (revert check); migration-evidence table (no pods / all legacy
    / mixed / all restricted / foreign pod); single-pod table (persistent × legacy × sidecar-only
    image diff), including the regression that a sidecar bump plus a posture change is **not**
    sidecar-only; a reconcile guard that the template does not flip-flop across passes.
12. Integration (envtest): create both StatefulSets (with and without the repair) and the
    observer Deployment through the real API server, read them back, assert
    `StatefulSetHasChanged` / `SentinelStatefulSetHasChanged` / `ObserverDeploymentHasChanged`
    report nothing — no write loop against API-server defaulting.
13. Imagetools (docker, no cluster), both pins, `--user 999:999 --read-only --cap-drop ALL
    --security-opt no-new-privileges` with a tmpfs per emptyDir: `valkey-server` with RDB and AOF
    writes and a rewrite, `valkey-sentinel` with a config rewrite, the probes, the preStop loop,
    `check-data-writable` in a pass and a fail case, `fix-data-ownership` with `--cap-add CHOWN`
    only. The measurements of this ticket, made permanent.
14. E2E `TestE2E_PodSecurity_RestrictedNamespace`: namespace labelled `enforce=restricted`;
    standalone+AOF, 3 replicas+TLS+auth+metrics, 3+3 Sentinel+TLS; every pod admitted and Ready
    (the API server is the PSS oracle); `Uid: 999` and `CapEff: 0` in `/proc/1/status` of the
    Valkey-image containers; data written and replicated; an 8→9 image roll and a drain failover
    complete with zero Warning Events; the sidecar still labels roles (token readable under
    `fsGroup`). The rest of the suite now runs rootless by construction; fixtures that assumed root
    or a writable root fs get fixed, not their assertions (ADR 0017 D18).
15. Migration proof, fleet-upgrade e2e: provision on the released chart a persistent AOF 3-replica
    cluster, a persistent RDB cluster, a Sentinel cluster, a persistent single-pod and a
    non-persistent single-pod cluster; write keys; `helm upgrade`. Assert the premise first — the
    Kind PV is `hostPath`, so the repair path is what runs (D29: fail loudly if Kind changes).
    Then: every multi-replica and Sentinel cluster converges rootless with keys intact on every
    replica; the persistent single-pod cluster restarts once and keeps its keys; the non-persistent
    one is not restarted, keeps its keys and carries the deferral condition; after convergence the
    repair container is gone from the templates and no second roll happened; the Sentinel tier
    rolled exactly once. It is not in CI: run it with `make e2e-fleet-upgrade-local` and record
    the run here (ADR 0017 D30); whether it becomes a CI job is a separate call.

**Phase 4 — docs, same change as the code**

16. README: the upgrade section states the one-time migration (Sentinel roll, single-pod
    behaviour, repair on storage without `fsGroup`, NFS `root_squash` needs a server-side chown
    first, debugging via `kubectl debug` because the root fs is read-only); the two init
    containers in the naming tables. No CRD reference change — there is no new field.
17. SECURITY_ARCHITECTURE.md: supersede the "no securityContext at all" bullet (line 319) in
    place; check the item at line 762; document the migration-only repair container as the one
    root process the operator still creates, and when; recommend the namespace label once a
    namespace is migrated.
18. CLAUDE.md: a short rule section pointing at ADR 0032 — "every generated pod is rootless; a new
    container inherits it through the walk; the repair container is the only exception and never
    enters the hash".

**Phase 5 — rollout**

19. Before upgrading a Kubernetes cluster: find NFS-backed Valkey PVCs with `root_squash` and
    chown them server-side; list non-persistent single-pod clusters (they stay root until
    restarted) and plan their restarts; for the rest the storage type only decides whether kubelet
    or the repair re-owns the files.
20. Upgrade a non-production Kubernetes cluster first — wds18 with `database-examples`, whose
    Chaos Mesh pod-kill every five minutes soaks restarts under the new posture — then the
    production clusters. Watch the rolls; T32's condition names any stuck one.
21. Once a namespace holds no pod without `runAsNonRoot`:
    `kubectl label --dry-run=server --overwrite ns <ns> pod-security.kubernetes.io/enforce=restricted`
    lists violators; then label it for real.

## Implementation notes (2026-09-26)

Implemented as planned on `feat/rootless` (ADR 0032), then reviewed adversarially (four lenses,
three refuters per finding, 73 agents). What the review changed beyond the Plan:

- **Persistence is read off the persisted StatefulSet** (`volumeClaimTemplates`), in
  `singlePodDeferral` and in the evidence. Reading the CR meant a persistence toggle the operator
  refused to write (ADR 0023) turned a deferred non-persistent single pod into "persistent" and
  deleted it with its `emptyDir` — the one data loss this ticket exists to avoid.
- **The repair is best-effort** (`find … -exec chown -h 999:999 {} + ; exit 0`), the pre-flight
  the one gate. Pods created while the template carried the repair keep it in their immutable
  spec and re-run it on every sandbox restart; a second run cannot enter the `lost+found` the
  first handed to 999 with `0700`, and a failing repair would have blocked a migrated pod after
  every node reboot. Imagetools now runs the repair twice.
- **The evidence is stricter** (ADR 0032 D4): a live template without the posture is evidence on
  its own (a pod missing at the first pass after the upgrade), and an ordinal counts as migrated
  only when its rootless pod passed the pre-flight or has been Ready (a replacement stuck in
  `ImagePullBackOff` had counted). *(Tightened again 2026-09-26 after the decision for the second
  roll: Ready only, and never while a data-tier roll is recorded — see "Ordering fix" below.)*
- **D3 refined**: rotated TLS material or a changed configuration is not deferred on a root,
  non-persistent single pod — both are records outside the pod-spec hash that the upgrade alone
  never moves, and deferring a rotation would keep an expiring certificate (ADR 0030 accepted the
  loss). One refuter voted this "decided behaviour"; kept, recorded in ADR 0032 D3, flagged to
  Hans.
- Tests hardened: fixtures rootless by default (`podFromStsTemplate` copies the posture); the
  drain hook bounded with a negative control; persistence checks read replies and counters;
  `CapBnd: 0`; a Pod Security positive control in the restricted namespace; the fleet e2e writes
  after the migration on a `0755` volume root and asserts the observer and exactly one Sentinel
  roll.
- The fleet-upgrade e2e had two pre-existing bugs that kept it from ever working as written:
  its cleanups hung on the provisioning subtest (the fleet was deleted before the upgrade), and
  it passed repository-relative chart paths although `go test` runs in the package directory.
  Both fixed.

**Asked and decided by Hans 2026-09-26: a second roll.** The persistent data pods created
during the migration kept the repair in their immutable spec (root on every sandbox restart, a
`restricted` violator). A pod that carries `fix-data-ownership` while the template no longer does
is now outdated (`podCarriesRetiredRepair`, via `podOutdated` at every data-tier site), so the
ordinary failover-aware roll replaces them once the repair has left the template. Cost: every
persistent multi-replica data tier rolls twice at the upgrade, a persistent single pod restarts
twice. The recommendation had been to accept the lingering repair; it lost. Storage without
`fsGroup` support whose fresh volume root is root-owned (static `hostPath`, CSI `fsGroupPolicy:
None`), and scale-ups onto claims retained from an earlier scale-down, stop at the pre-flight
where root pods used to work; documented as a storage requirement, not automated.

## Verification

Done when every line holds, each with the command and date recorded here:

- [x] T32 done — 2026-09-26, same branch, ADR 0026 D11.
- [x] Evaluator matrix green, both sides and the positive control; mutation check recorded.
      2026-09-26, `make test-unit`: `TestPodSecurity_EveryRenderedTemplateIsRestricted` over 168
      rendered templates (72 data, 72 observer, 24 Sentinel from the 3 topologies x TLS x auth x
      metrics x {no persistence, rdb, aof} matrix), `TestPodSecurity_TheRepairIsBaselineButNotRestricted`
      (baseline allowed, restricted denied, 48 persistent rows), positive control
      `TestPodSecurity_EvaluatorRefusesTheLegacyShape`. Mutation: dropping `applyValkeyPodSecurity`
      fails the matrix (killed). `k8s.io/pod-security-admission v0.37.0` added as the test-only
      evaluator at the `k8s.io/api` version (+ `k8s.io/component-base v0.37.0` indirect); the
      Renovate group regex `^k8s.io/` already covers it.
- [x] Hash test: repair insertion neutral, any other posture change not; revert check recorded.
      `TestWithDataOwnershipRepair_IsHashNeutral`; mutation "repair inside `buildPodSpec`" killed
      (the first version of the test did not catch it on its own assertion — sharpened).
- [x] envtest reports no change after API-server defaulting, with and without the repair.
      `TestPodSecurity_TemplatesSurviveAPIServerDefaulting_Integration`, `make test-integration`
      green 2026-09-26.
- [x] Imagetools restricted runtime check green on both pins. `make test-image-tools` green
      2026-09-26 on 9.1.1 and 8.1.9: persistence under the posture (replies and counters, not the
      status fields that start at ok), `CapBnd: 0`, Sentinel config rewrite, drain hook released
      within 5 s and still waiting without the marker, pre-flight refuses root-written AOF data
      and names the fix, the repair with `CAP_CHOWN` only, a second repair run over a 999-owned
      `0700` `lost+found` exits 0 and the pre-flight passes.
- [x] Restricted-namespace e2e and the full suite green on all three legs. Locally on Kind
      (control plane + 3 workers, v1.36.1), 2026-09-26: `make test-e2e E2E_VALKEY_LINE=9` 51/51
      (588 s, on the image before the review fixes to the migration code), `make test-e2e
      E2E_VALKEY_LINE=8` 51/51 (536 s, final image) — the multi-node scope is part of both, the
      cluster has three workers — and the hardened `TestE2E_PodSecurity_RestrictedNamespace` plus
      the T32 e2e again on the final image against valkey 9 (green). CI has not run them yet:
      this records local runs, not the three CI legs.
- [x] Fleet-upgrade e2e run locally, every assertion of step 15 green, run recorded here.
      **2026-09-26, `make test-e2e-fleet-upgrade E2E_UPGRADE_FROM=1.12.8`: passed (253 s)** on a
      fresh Kind cluster (control plane + 3 workers, v1.36.1). Starting point 1.12.8, not the
      default 1.10.48: every released image is amd64-only and this host is arm64 ("no match for
      platform in manifest"); the 1.12.8 image was pulled for linux/amd64 and loaded into Kind,
      where Docker Desktop runs it emulated. Fleet of six on valkey 9.1.1 — 3+3 Sentinel TLS,
      3-replica plain with observer, 3-replica AOF, 3-replica RDB (volume roots set to `0755 root`
      in the old root pods first, asserting uid-0 files), persistent and non-persistent single
      pods. Green: hostPath premise; every multi-replica and Sentinel cluster converged, every pod
      rootless, keys on every replica; every persistent pod ran `fix-data-ownership` with exit 0
      and the repair left the template; the migrated persistent masters wrote and snapshotted (no
      MISCONF); the observer received the posture; exactly one `SentinelUpdateComplete` per
      Sentinel tier; no pod replaced in the 90 s after the repair left the template; the
      persistent single pod restarted once with its keys; the non-persistent one was not
      restarted, kept its keys and reports `PodSecurityUpdatePending=True/PodRunsAsRoot`; no
      `ReconcileBlocked`, no ownership refusals; the pre-upgrade hook completed. Three
      pre-existing bugs in the test were fixed to get there (Implementation notes). Still not a
      CI job (ADR 0017 D30).
- [x] Measured on a real node and written into this ticket: Kind's PV type, the projected-token
      mode under `fsGroup`.
      **Measured 2026-09-26** (Kind v1.36.1, containerd): the PV is `hostPath` `DirectoryOrCreate`
      under `/var/local-path-provisioner`, volume root `0777 root:root`; the projected token of
      the sidecar is `0640`, owner 999, group 999 (kubelet rewrites `DefaultMode 0644` under
      `fsGroup`; owner = `runAsUser`), `ca.crt` `0644 root:999`.
- [x] ADR 0032, the amendments of Phase 0 and the docs of Phase 4 land in the same change.

<details>
<summary>Reproducing the measurements (docker, no cluster)</summary>

```sh
IMG=valkey/valkey:9.1.1   # and valkey/valkey:8.1.9
# image facts
docker image inspect "$IMG" --format 'User={{json .Config.User}} WorkingDir={{.Config.WorkingDir}}'
docker run --rm --entrypoint sh "$IMG" -c 'id valkey; cat "$(command -v docker-entrypoint.sh)"'
# today's shape: command replaces the entrypoint
docker run -d --name t --entrypoint valkey-server "$IMG" --save ''
docker exec t grep -E '^(Uid|CapEff|NoNewPrivs)' /proc/1/status
# restricted runtime
docker run -d --name r --user 999:999 --read-only --cap-drop ALL \
  --security-opt no-new-privileges --tmpfs /data:rw,uid=999,gid=999,mode=0755 \
  --entrypoint valkey-server "$IMG" --dir /data --appendonly yes --save '60 1'
docker exec r sh -c 'grep -E "^(Uid|CapEff|NoNewPrivs|Seccomp)" /proc/1/status;
  valkey-cli set k v; valkey-cli bgsave; sleep 1; valkey-cli bgrewriteaof; sleep 2;
  valkey-cli info persistence | grep -E "status"'
# legacy data -> restricted: volume root shape set with :nocopy, legacy writer as root,
# then the restricted run above against the same volume; repair step:
docker run --rm --user 0:0 --read-only --cap-drop ALL --cap-add CHOWN \
  --security-opt no-new-privileges -v vol:/data:nocopy --entrypoint sh "$IMG" \
  -c 'find /data ! -user 999 -exec chown 999:999 {} +'
```

</details>

## Ordering fix (2026-09-26, after the second-roll decision)

**Run.** The first fleet-upgrade run with the second roll (ADR 0032 D2) counted one
`RollingUpdateComplete` per persistent tier where it expected two. **Read**, then fixed: the
repair left the template on the last replacement's pre-flight, `reconcileStatefulSet` runs
before the rolling update in the same pass, and `clearStaleRollingUpdateState` discarded the first
roll's state as stale — on the non-Sentinel path in the middle of the topology restoration — so
the first roll never finalized and the second overtook it. Now (ADR 0032 D4): a migrated pod is
**Ready**, not merely past its pre-flight, and the repair **stays while a data-tier roll is
recorded**. **Run**, second fleet attempt: that alone stranded the repair — the pass that could
remove it is the one after the completion, and a completing pass schedules none (generation-gated
CR watch, no Pod watch). `finishDataRoll` now asks for that pass (`requestRecheck`) when the
template still carries the repair. Unit: `TestDataOwnershipRepairNeeded` (new row "past its
pre-flight but not Ready: kept"), `TestDataOwnershipRepairNeeded_StaysWhileARollIsRecorded`,
`TestCompletedRoll_AsksForThePassThatRemovesTheRepair`; mutations of the state gate and of the
Ready check killed. The fleet assertion is exact again (two completions per persistent tier).

## Extension 2026-09-26: the full pod-manifest hardening, for every Valkey pod and for the operator

Hans asked, after the rootless posture was done, that everything a pod manifest can express for
security be introduced for every Valkey pod (data, Sentinel, observer) and for the operator
itself, against this reference:

```yaml
spec:
  automountServiceAccountToken: false
  hostNetwork: false
  hostPID: false
  hostIPC: false
  hostUsers: false
  securityContext: {runAsNonRoot: true, runAsUser: 10001, runAsGroup: 10001, fsGroup: 10001,
                    seccompProfile: {type: RuntimeDefault}}
  containers:
  - image: registry.example.com/app@sha256:...
    securityContext: {allowPrivilegeEscalation: false, readOnlyRootFilesystem: true,
                      privileged: false, capabilities: {drop: ["ALL"]}}
    resources: {requests: {cpu: 100m, memory: 128Mi}, limits: {memory: 256Mi}}
```

Decided in three questions (2026-09-26): **seccomp** configurable as `RuntimeDefault` or
`Localhost`, never `Unconfined` (recommended option); **`hostUsers: false`** opt-in everywhere,
default off (recommended); **resources**: only a new `spec.sentinel.resources`, no defaults
anywhere (Hans chose this over the recommended measured defaults). Recorded as
[ADR 0033](../../adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md).

### Fourth question (2026-09-26): who may pick which `Localhost` profile

**The risk put to Hans, after D1 was implemented.** Pod Security `restricted` accepts every
`Localhost` profile (read: `check_seccompProfile_restricted.go`, `k8s.io/pod-security-admission`
v0.37.0), and the CRD refused `Unconfined` by name only. So whoever may create a Valkey resource
could run its pods under any profile file an administrator ever put on a node — including an
allow-by-default one, which filters less than `RuntimeDefault`. The e2e fixture is exactly such a
file (`defaultAction: SCMP_ACT_ALLOW` with a short deny list, read in
[`test/e2e/pod_hardening_test.go`](../../../test/e2e/pod_hardening_test.go)).

| | What | Cost |
|---|---|---|
| Document only | The operator keeps no allow-list; the docs tell administrators to install only profiles they accept for every Valkey pod and, if the choice must be narrower, to write an admission policy on the CR or on the generated pods. | The wide choice is the installed default; the narrowing lives outside the operator, and nothing checks that it exists. |
| Remove `Localhost` | `RuntimeDefault` only, ADR 0033's "fixed `RuntimeDefault`" alternative. | A cluster with its own profiles gets one only through a mutating policy, which the ADR 0032 D5 comparison writes back out on every pass. |
| **Allow-list** (decided) | An operator flag listing the profiles a Valkey resource may name, empty by default = default-deny; exact match; an unlisted profile blocks the pass and writes no workload. | A flag to keep in step with the nodes, an operator rollout per change, and a string comparison that cannot see the file it names. |

**Answer.** Hans first answered "document only", and the stance went into the hardening checklist
of `SECURITY_ARCHITECTURE.md` (an admission policy has to narrow the choice; the operator has no
allow-list) — struck through there since, with a dated note, in section 3 and in the checklist
item (read 2026-09-26). He reversed it in the same
session and decided the allow-list, default-deny. Recorded as ADR 0033 D9, with "document only"
and "remove `Localhost`" as rejected alternatives; D1 amended with a second CEL rule that refuses
an absolute `localhostProfile` and a `..` element on the CR. Implemented in the same change:
`--allowed-seccomp-localhost-profiles` (`profileList`, [`cmd/main.go`](../../../cmd/main.go)),
`seccompProfileAllowed` ([`pod_hardening.go`](../../../internal/controller/pod_hardening.go)) ~~at the
head of the three workload steps~~ at the write of the three workload steps *(moved 2026-09-26:
after the ownership proof, the claim guard, the TLS record and the repair decision, before the
drift detection — at the head it hid a foreign StatefulSet and froze `StorageSpecNotApplied`;
ADR 0033 D9, `TestSeccompProfileNotAllowed_GateSitsAtTheWrite`)* — the data StatefulSet step reports, the Sentinel and observer
steps only withhold — `ReconcileBlocked/SeccompProfileNotAllowed` ranked after `RecreateRequired`
and before `UserNamespacesUnsupported`, the chart value
`valkeyPodSecurity.allowedSeccompLocalhostProfiles: []` with render-time refusals, and the two e2e
profiles in [`test/e2e/helm-values.yaml`](../../../test/e2e/helm-values.yaml).

### Measures, per pod — state after this change

| Measure | Data pod | Sentinel pod | Observer | Operator + hook | How |
|---|---|---|---|---|---|
| `automountServiceAccountToken` | false, token projected to the sidecar only | false | false | **true** (needs the API) | ADR 0031; operator stated in the chart |
| `hostNetwork` / `hostPID` / `hostIPC` | false | false | false | false | unset = false; the API cannot carry an explicit false; a test asserts it per template |
| `hostUsers: false` | **opt-in** | **opt-in** | **opt-in** | **opt-in** (`podSecurity.userNamespaces`); a dropped field is **not reported** | `spec.podSecurity.userNamespaces`, ADR 0033 D2; a dropped field blocks the pass (D3) — for the data, Sentinel and observer workloads only, which go through `writeWorkload`; the chart's switch has no D3 (ADR 0033 residual risk). *(Corrected 2026-09-26: the row read as if D3 covered every column.)* |
| `runAsNonRoot` / uid / gid / fsGroup | 999 | 999 | **65532** (new) | **65532** (new) | ADR 0032; observer and chart pinned by ADR 0033 D4, D6 |
| seccomp | RuntimeDefault, or **Localhost only when the operator's allow-list names it** (empty by default) | same | same | RuntimeDefault or Localhost (`podSecurity.seccompProfile`, the installer's choice, not allow-listed; an absolute or `..` path fails the render since 2026-09-26) | `spec.podSecurity.seccompProfile`, ADR 0033 D1, D9; allow-list `--allowed-seccomp-localhost-profiles` / chart `valkeyPodSecurity.allowedSeccompLocalhostProfiles`; an unlisted profile writes no workload and blocks the pass (`SeccompProfileNotAllowed`); `Unconfined` refused by name (CRD enum / render); an absolute path or `..` refused by CEL. *(Updated 2026-09-26 for D9; before it, every Localhost profile was accepted.)* |
| `privileged: false` | **stated** (new) | stated | stated | stated | `restrictedContainerSecurityContext`, repair container, chart |
| `allowPrivilegeEscalation: false`, `readOnlyRootFilesystem`, `drop: [ALL]` | yes | yes | yes | yes | ADR 0032 (generated), chart (operator) |
| `enableServiceLinks: false` | **new** | new | new | new | beyond the reference: no Service inventory in the environment |
| image by digest | `spec.image` may now carry one (**label bug fixed**) | same image | operator image | `image.digest` (new) | ADR 0033 D5 |
| exporter image by digest | default pinned (index digest) | — | — | — | `DefaultMetricsExporterImage` |
| requests / limits | valkey: `spec.resources`; exporter: `spec.metrics.resources`; sidecar + init: **none** | **`spec.sentinel.resources`** (new, all containers) | `spec.observer.resources` (default request) | chart `resources`, `preUpgradeHook.resources` | ADR 0033 D7 |
| AppArmor | not set | not set | not set | not set | **refused**: explicit profile breaks nodes without AppArmor (D8, read in upstream) |

### Found on the way

- **Run/read — a digest-pinned `spec.image` could never be deployed.** `ExtractVersionFromImage`
  returned `sha256:<64 hex>` as a label value (71 characters, a colon). The unit test pinned that
  output with a 6-character fake digest. Fixed; every result is now checked with
  `IsValidLabelValue`, and the e2e deploys a `tag@digest` image.
- **Run (integration) — an API server with `UserNamespacesSupport` off drops `hostUsers` without
  an error** (envtest 1.29). Without D3 the opt-in would read as applied on such a cluster.
- **Run — the integration round-trip tests read through the manager's cache.** A `Get` right
  after a `Create` could miss the object (`posture-it` not found once); the tests now take the
  stored object from the write's answer. The pushed `e2ce8bb` still had the cache read, and CI's
  integration job failed on it (section "CI red on `e2ce8bb`").
- **Run — the Sentinel rolling update demoted the replica Sentinel was promoting** (added
  2026-09-26). Pre-existing on `main`, hit by the hardening e2e on Valkey 8; decided as ADR 0025
  D9, section "Split-brain found by the hardening e2e".
- **Read — ADR 0025 D9's window had no bound of its own, and the failover state could stand
  without its timestamp** (added 2026-09-26). Both closed the same day; section "ADR 0025 D9
  follow-up: its own clock and a one-write arming".
- **Run — `TestE2E_SidecarFailoverDrainMaster` waited on controller state after deleting the
  master** (added 2026-09-26). The one failure of the final Valkey 9 suite, a fixture defect;
  section "Drain-test finding", and T34 on the board for the same shape elsewhere.
- **Cyclo:** controller-gen's `ValkeySpec.DeepCopyInto` reached 16 with the new optional field;
  `make cyclo` now ignores `zz_generated` (ADR 0017 D35 amended), hand-written code stays under 15.

### Further security measures — not in this change, each open

| Measure | Why it matters | Where |
|---|---|---|
| Authenticated operator metrics endpoint (controller-runtime `WithAuthenticationAndAuthorization`) | `:8080/metrics` is plain HTTP and an inventory of every Valkey resource | ADR 0021 |
| NetworkPolicy for the operator namespace (ingress metrics/health only, egress API server + Valkey ports) | nothing restricts who reaches the operator pod | chart, default off |
| Least-privilege Valkey ACL users for probes, sidecar, exporter, observer | today every component authenticates with the one password and full rights | ADR 0016 |
| Password rotation | open by design, must not copy the TLS fingerprint mechanism | ADR 0030 D11 |
| Sidecar / init container requests | a cpu/memory `ResourceQuota` still refuses the data pods | ADR 0033 D7 (decided: no defaults) |
| Release pipeline stamps the pushed digest into the chart | `image.digest` defaults to empty | ADR 0033 residual |
| Renovate for `DefaultMetricsExporterImage` | the pinned v1.66.0 ages by hand only | ADR 0033 residual |
| A recommended `Localhost` seccomp profile (e.g. recorded with the Security Profiles Operator) | RuntimeDefault is generic; the e2e profile is a fixture | — |
| Namespace-scoped operator mode | cluster-wide privilege footprint | ADR 0013 |
| `enable-debug-command` / `enable-module-command` pinned to `no` in the generated config | today whatever the image defaults to applies implicitly (believed `no` since Redis 7; not re-checked for either pinned line) | configmap builder |
| Chart render test in CI | `helm template` refusals were checked by hand only | ADR 0017 |

### Verification (extension)

All runs 2026-09-26. ~~**Everything in the first four bullets ran before the allow-list (D9) and
the CEL path rule were added**, except where a line names D9.~~ *(Superseded 2026-09-26: each
bullet now says which code it ran against; the final runs are marked **final**.)*

- **Run, unit:** see ADR 0033 *Residual risks*; `make test-unit`, `make lint` ~~(golangci-lint
  v2.14.0)~~ *(corrected 2026-09-26: that run invoked the stale unversioned `bin/golangci-lint`,
  2.13.1 by `--version`, not the pinned v2.14.0 — section "CI red on `e2ce8bb`", ADR 0017 D49)*,
  `make cyclo` green before D9; 8 of 8 mutations of the ADR 0033 code killed.
  D9: `TestSeccompProfileAllowed`, `TestSeccompProfileNotAllowed_NoWorkloadIsWritten`,
  `TestProfileList`, `TestBindOperatorFlags_AllFlagsParsed`, `TestNewReconciler` green in
  `make test-unit` (exit 0, no FAIL, no SKIP), ~~served from the Go test cache — an earlier run on
  the same sources, not a `-count=1` run~~ *(superseded 2026-09-26: rerun uncached,
  `GOFLAGS=-count=1 make test-unit`, exit 0, every package `ok`, none `(cached)`, no FAIL, no
  SKIP, the five tests PASS)*. ~~The revert checks in their doc comments are not recorded as
  executed, and no mutation of the D9 code was run.~~ *(Superseded 2026-09-26.)* **Final:** the
  gate moved to the write, pinned by `TestSeccompProfileNotAllowed_GateSitsAtTheWrite` (row "a
  foreign StatefulSet is still reported as foreign"; row "a live template the allow-list no longer
  holds is reported without drift"). Mutations killed: **7 of 7 of the ADR 0033 D9 code**, gate-position
  mutations among them (which seven is not recorded here); ~~**1 of 1 of the split-brain guard**
  (ADR 0025 D9, below)~~ **3 of 3 of the split-brain guard** (ADR 0025 D9 with its own clock: no
  guard, no clock, no timestamp check) and **1 of 1 of the one-write arming** (the write split
  again) *(updated 2026-09-26, section "ADR 0025 D9 follow-up")*; **2 of 2 of the ADR 0032 D4
  ordering fix** (section "Ordering fix").
  No row combines D9 with a claim conflict or a TLS template, so the gate's order relative to
  `guardVolumeClaimTemplates` and `ensureTLSMaterialRecord` is read from the code, not pinned.
  ~~**Not claimed:** the full unit tier, `make lint` and `make cyclo` on the final code — part of the
  CI-parity run below, still running.~~ *(Updated 2026-09-26.)* The full unit tier (through its
  coverage target), `make lint` (golangci-lint v2.14.0, 0 issues) and `make cyclo` were green in
  the CI-parity run on the tree before the ADR 0025 D9 follow-up and the drain-test fix (section
  "CI red on `e2ce8bb`"). ~~**Not claimed:** their rerun on the final code, still running.~~
  *(Rerun green 2026-09-26.)* The CI-parity rerun on the final tree — ADR 0025 D9's own clock, the one-write arming and the
  D50 drain fix included; only comments and the retrigger arming test changed after it — ran every
  gate target in a fresh clean copy with an empty `bin/`: `make generate-all` (no diff), `make lint`
  (golangci-lint v2.14.0, 0 issues), `make cyclo`, `make gosec` (v2.29.0, 0 issues), `make vuln`
  (no vulnerabilities), `make test-unit-coverage`, `make test-integration-coverage`,
  `make test-image-tools` and `make test-release-tooling`, all green (2026-09-26); `make test-unit`
  and `make test-integration` green again after the last test and comment changes. ~~CI itself has
  not run on it.~~ *(CI, 2026-09-26: every check green on `e6a9d7c` — Generated Manifests, Integration (envtest),
  Unit, Lint, GoSec, Vuln, Cyclo, Image Tools, Release Tooling, Coverage, both malware scans and all
  three E2E legs; the two jobs that were red on `e2ce8bb` pass. The two pushes between failed only
  `TestE2E_PodHardening_UserNamespacesLocalhostSeccompAndDigest` in the single-node legs, D5.)*
- **Run, integration (envtest 1.29):** `make test-integration` green — CEL/enum refusals,
  `seccompProfile: {}` defaulting, the dropped-`hostUsers` report and its release, drift-free
  hardened templates with a digest image. ~~**Not run:** `TestPodSecurity_LocalhostProfileAllowList_Integration`
  and the new path rows of `TestPodSecurity_CRDValidatesTheSeccompProfile_Integration`.~~
  *(Superseded 2026-09-26.)* ~~**Final:**~~ **Latest recorded runs** *(relabelled 2026-09-26:
  the three runs of 13:18–13:20 local time predate the D9 gate move — whose unit test was not yet
  in a unit run of 13:34 — and ADR 0025 D9, read from the run logs' and the source files'
  timestamps; ~~on the final code the integration tier has run only inside the CI-parity run, not
  claimed~~ *(updated 2026-09-26)* the CI-parity run's integration coverage target was green on
  the tree with the gate at the write, before the ADR 0025 D9 follow-up; its rerun on the final
  code is not claimed)*: the integration tier green repeatedly with D9 and the CEL
  path rule in the tree, `TestPodSecurity_LocalhostProfileAllowList_Integration` (unlisted profile:
  `ReconcileBlocked/SeccompProfileNotAllowed`, phase `Error`, no StatefulSet read past the cache;
  listed profile reaches the StatefulSet) and the path rows of
  `TestPodSecurity_CRDValidatesTheSeccompProfile_Integration` (absolute, leading, inner and
  trailing `..` refused; dots inside a name accepted) included. The CEL rules have no unit test;
  these rows are their only check.
- **Run, chart:** `helm lint`; `helm template` with defaults, with `image.digest`, userns and
  `Localhost`; refused: `Unconfined`, `Localhost` without path, path without `Localhost`,
  `image.digest=sha256:abc`, `image.digest=latest`. D9: the default renders no
  `--allowed-seccomp-localhost-profiles`; a two-entry list renders it comma-joined;
  `test/e2e/helm-values.yaml` renders its two profiles; `profiles/..v..json` is accepted; `""`,
  `/abs.json`, `../x.json`, `profiles/../x.json`, `profiles/..` and `a,b` each fail the render.
  *(Added 2026-09-26, final chart.)* The operator's own `podSecurity.seccompProfile` with `type:
  Localhost`: `/abs.json`, `../x.json`, `profiles/../x.json` and `profiles/..` each fail the
  render ("must be a relative path without '..'", `valkey-operator.podHardening`),
  `profiles/..v..json` and `profiles/ok.json` render; `helm lint` passes. By hand only; no CI gate
  renders it.
- **Run, e2e** on Kind — Kubernetes 1.36.1, containerd 2.3.1, runc 1.4.2, Linux 6.10:
  ~~*pending, recorded below when the run completes.*~~ *(Superseded 2026-09-26.)* The first
  run, before ADR 0033 D9:
  - Fleet-upgrade e2e from 1.12.8: green, including exactly two `RollingUpdateComplete` per
    persistent tier and nothing rolling after the second roll.
  - Full suite, Valkey 8: 53/53 green.
  - Full suite, Valkey 9: 52/53. The one failure was
    `TestE2E_PodHardening_UserNamespacesLocalhostSeccompAndDigest`'s own owner assertion on
    `/data`: the Kind hostPath volume root is root-owned `0777`, and a cluster this operator
    built never ran the ADR 0032 repair. The test now compares the volume root's owner before
    and after the move; with it the test passed on Valkey 8 and, rerun alone, on Valkey 9.
  - `TestE2E_RollingUpdate_TwoSentinelsRollSerially` green on both lines (an earlier run).

  ~~The run before the final one~~ The run before ADR 0025 D9 *(re-anchored 2026-09-26: the
  final run moved)*: its Valkey 8 leg was **red** on the split-brain bug of ADR 0025
  D9 (section "Split-brain found by the hardening e2e").

  ~~**Final run**~~ **The run before the final one** *(relabelled 2026-09-26: its image predates
  ADR 0025 D9's own clock and the one-write arming)*, same Kind versions, one operator image built
  from ~~the final code~~ the code of that time — D9 with the gate at the write, the CEL path rule,
  ADR 0025 D9 in its first form:
  - Fleet-upgrade e2e from 1.12.8: green.
  - Full suite, Valkey 9: **53/53 green**.
  - Full suite, Valkey 8: **53/53 green**.
  - Two more Valkey 8 runs of `TestE2E_PodHardening_UserNamespacesLocalhostSeccompAndDigest` and
    `TestE2E_PodSecurity_RestrictedNamespace`: green.
  - The D9 subtest "a Localhost profile the operator does not allow is refused and reported" is
    part of the hardening test and was green on every run.
  - Operator log, cluster `hard`: 4 Sentinel failover triggers (one per run of the hardening
    test), 0 demotions, 0 failover timeouts.

  **Final run** *(added 2026-09-26)*, same Kind versions, one operator image built from the final
  operator code — the above plus ADR 0025 D9's own clock and the one-write arming (section "ADR
  0025 D9 follow-up"); the drain-test fix came after it and is test code, not in the image:
  - Fleet-upgrade e2e from 1.12.8: green.
  - Full suite, Valkey 8: **53/53 green**.
  - Full suite, Valkey 9: **52/53**. The one failure, `TestE2E_SidecarFailoverDrainMaster`, is a
    fixture defect of that test (section "Drain-test finding"); the fixed test ran green 8 of 8 on
    Valkey 9 alone. No full-suite run with the fix is recorded.
  - Two more Valkey 8 runs of `TestE2E_PodHardening_UserNamespacesLocalhostSeccompAndDigest` and
    `TestE2E_PodSecurity_RestrictedNamespace`: green.
  - The hardening test, and with it the D9 subtest, passed on every run.
  - Operator log counts for cluster `hard`: not recorded for this run.
- ~~**Not yet run:** the D9 e2e subtest "a Localhost profile the operator does not allow is
  refused and reported". A rerun of the fleet-upgrade e2e and both full suites with D9 and the
  CEL path rule is in progress; nothing of it counts as run.~~ *(Superseded 2026-09-26 by the
  final run above.)*
- ~~**Not claimed:** lint, cyclo, gosec, vuln, the unit and integration coverage targets and the
  image-tools check on the final code (the CI-parity run in section "CI red on `e2ce8bb`" was
  still running); CI itself, which has not seen the uncommitted change.~~ *(Updated 2026-09-26.)*
  **Run, CI parity** in a clean copy of the tree before the ADR 0025 D9 follow-up and the
  drain-test fix: `make generate-all` (no diff with a fresh controller-gen v0.22.0), lint
  (golangci-lint v2.14.0, 0 issues), cyclo, gosec v2.29.0 (0 issues), vuln (no vulnerabilities),
  the unit and integration coverage targets, image tools and release tooling — all green.
  **Not claimed:** the rerun of those gates on the final code, running when this was written;
  CI itself, which has not seen the uncommitted change.

## Split-brain found by the hardening e2e: ADR 0025 D9 (2026-09-26)

**Run.** The Valkey 8 leg of the e2e run before ~~the final one~~ ADR 0025 D9 *(re-anchored
2026-09-26: two runs have followed it since)* went red in
`TestE2E_PodHardening_UserNamespacesLocalhostSeccompAndDigest`. Its cluster `hard` (3 data + 3
Sentinel, AOF, metrics, observer) rolls when the spec patch moves it, and the roll's master step
is a Sentinel failover. The operator log for `hard` in that run: **11 failover triggers, 11
demotions, 9 failover timeouts** over both legs — one trigger and one demotion on Valkey 9, where
the failover completed anyway and the leg stayed green, and ten triggers, ten demotions and nine
timeouts on Valkey 8, a reset-and-retrigger loop until the subtest's ten-minute wait ran out
(ADR 0025 D9). *(Per-leg split added 2026-09-26, read from the log's timestamps against each
leg's run.)*

**Mechanism** (read in `handleRollingUpdate`, [`rolling_update.go`](../../../internal/controller/rolling_update.go); the demotions and timeouts are the logged ones above):
the Sentinel rolling update resolves split brain at the start of every pass, with Sentinel's
master pointer (`getSentinelMasterPodName`) as the authority. Sentinel promotes its candidate
first and moves that pointer only at `+switch-master`, so inside the window the promoted replica
answers master while the authority still names the old one — and the resolver demoted exactly the
replica the operator had asked Sentinel to promote. Sentinel timed out (`failoverRetryTimeout`),
the roll reset and retriggered, and the next pass inside the window did it again. The re-entry a
second after the trigger was the observer turning unready and its Deployment status event
(`Owns(&appsv1.Deployment{})`) — inferred from timing and from only the observer-enabled clusters
of the run seeing it, not traced (ADR 0025 D9). **Pre-existing on `main`**, not introduced by this
ticket; this ticket's e2e is what hit the window.

**Decided by Hans, 2026-09-26: ADR 0025 D9.** While the roll's own Sentinel failover is in
flight (rolling-update state `failover-triggered`), the Sentinel rolling update reports
`MultipleMasters` and does **not** resolve or demote (`resolveSplitBrainUnlessFailingOver`). ~~The
window stays bounded: a failover that does not complete within `failoverRetryTimeout` goes to
`stateFailoverReset`, and the next pass resolves as before. The cost, recorded in ADR 0025: for up
to `failoverRetryTimeout` two pods may accept writes during the roll's own failover, the window
every Sentinel failover has.~~ *(Corrected 2026-09-26: `failoverRetryTimeout` bounds only the
branch in which no new-image pod answers master. In this decision's double master the promoted
replica does answer master, and one wait of that branch, `verifyNewMasterReady`'s plain
requeues, has no bound — so the window was not bounded by `failoverRetryTimeout`, and on that
branch not bounded at all while one of those requeues held. Closed the same day by D9's own
clock, section "ADR 0025 D9 follow-up".)*
The window is bounded by its own clock: `failover-triggered` **and** a failover timestamp younger
than `replicaReconnectTimeout` (90 s, `ownFailoverInFlight`); past that the resolver runs again in
the same state. The post-failover handler usually ends it sooner — `failoverRetryTimeout` (30 s)
hands a failover without a new-image master to `stateFailoverReset`, and a promoted pod with a
connected replica leaves the state for `replacing-master` once `verifyNewMasterReady` passes.
Each timeout of the no-replica branch sends a best-effort `REPLICAOF` of the new master to the
other pods and, `maxReconnectResets` (2) times in a row, rewrites the timestamp, re-opening the
window by 90 s; the third clears the count and hands over to `replaceRemainingPods`, and when
`verifyNewMasterReady` still waits there the state stands and the next timeout starts the count
again, so nothing caps the number of re-openings while the new master has no connected replica.
Each of these re-openings happens in a pass whose resolver already ran on the expired window,
because the resolver is asked before the post-failover handler. A failed second write of
`handleNoMasterFound` (timestamp, then `failover-reset`) also leaves `failover-triggered` with a
fresh timestamp; that timeout fires after 30 s, inside the window, and restarts its 90 s with no
resolving pass in between (ADR 0010 D14, ADR 0025 *Residual risks*). All of this read in
[`rolling_update.go`](../../../internal/controller/rolling_update.go), not measured. The cost,
recorded in ADR 0025: for up to ~~`failoverRetryTimeout`~~ `replicaReconnectTimeout` (90 s) two
pods may accept writes during the roll's own failover, the window every Sentinel failover has
*(per window — each re-opening above starts another, 2026-09-26)*.

**Verified.** Unit: `TestHandleRollingUpdate_DoesNotDemoteTheReplicaSentinelIsPromoting`
([`split_brain_failover_test.go`](../../../internal/controller/split_brain_failover_test.go)): in
`failover-triggered` no `REPLICAOF` is sent and `MultipleMasters` is True; the positive control
`failover-reset` demotes, so the fixture can observe a demotion at all. Mutation "call
`resolveSplitBrain` unconditionally": killed (1 of 1) *(since the follow-up: four rows, three
mutations killed — section "ADR 0025 D9 follow-up")*. E2E, ~~final run~~ the run with D9 in its
first form *(relabelled 2026-09-26)*: `hard` had 4 triggers, 0 demotions, 0 timeouts, and the
Valkey 8 leg is green. **Not verified:** no e2e reproduces the window deterministically; the
evidence is the rerun (ADR 0025 residual risks).

## ADR 0025 D9 follow-up: its own clock and a one-write arming (2026-09-26)

**Read.** Two gaps in D9 as first written, both found by reading after it was decided, neither
reached by a run:

- **The window had no bound of its own.** D9 withholds the resolver for as long as the state is
  `failover-triggered`, and how long that lasts is decided by the post-failover handler. On the
  branch this decision is about — the promoted replica answers master and has a connected
  replica — the state leaves only once `verifyNewMasterReady` passes, and that function's plain
  requeues carry no bound (ADR 0025 *Residual risks*, the item now struck through there). While
  one of them held and the old master still answered master, nothing in the operator resolved
  the double master.
- **The state could stand without its timestamp.** Both sites entering `failover-triggered`
  (`handleMasterFailover`, `handleFailoverRetrigger`) wrote the state and then, in a second
  update, the failover timestamp. When the second write failed at `handleMasterFailover`, the
  first trigger, the state stood without a stamp, and `annotationTimestampExceeded` reads a
  missing stamp as never expired — `isFailoverTimedOut` and `isReplicaReconnectTimedOut` never
  fired, and D9's window would have stood with them. At `handleFailoverRetrigger` the stamp
  `handleNoMasterFound` wrote for the reset stood instead, at least `failoverResetMinWait` (20 s)
  old, so there the bounds fired early rather than never (as ADR 0010 D14 narrows it).

**Fix** (in [`rolling_update.go`](../../../internal/controller/rolling_update.go), read 2026-09-26):

- `resolveSplitBrainUnlessFailingOver` asks `ownFailoverInFlight`: `stateFailoverTriggered`
  **and** a failover timestamp that is present and younger than `replicaReconnectTimeout` (90 s).
  A missing timestamp is no window (resolve as before); past 90 s resolution resumes in the same
  state. The reason given for 90 s: Sentinel moves its master pointer within seconds of the
  promotion or gives the failover up, so after 90 s the authority is settled either way — the
  operator configures Sentinel's `failover-timeout` as 60 s (`SentinelFailoverTimeout`, read);
  that Sentinel has switched or aborted by then is Sentinel's own behaviour, not measured here.
  Recorded as ADR 0025 D9 *(amended)*.
- `setFailoverTriggered` writes `stateFailoverTriggered` and the failover timestamp in **one**
  update, and both trigger sites call it; it is the only write of that state in the controller
  (read with `grep`). Recorded as ADR 0010 D14 *(amended)*: an arming write belongs in the same
  update as the state it bounds. **Not applied everywhere:** `handleNoMasterFound` still writes
  the timestamp and then `failover-reset` in two updates (read; ADR 0010 D14 and ADR 0025
  *Residual risks* record it; no board row).

**Verified (unit).** `TestHandleRollingUpdate_DoesNotDemoteTheReplicaSentinelIsPromoting` now has
four rows: `failover-triggered` just armed (no demotion, `MultipleMasters` True); `failover-reset`
(demotion — the positive control); `failover-triggered` past the window (demotion);
`failover-triggered` without a timestamp (demotion). Three mutations killed: no guard (the
resolver called unconditionally), no clock, no timestamp check — the revert checks named in the
test's doc comment. `TestHandleRollingUpdate_ArmsTheFailoverStateWithItsTimestamp` asserts, inside
a client interceptor, that the write entering `failover-triggered` already carries the timestamp;
the mutation splitting the write again is killed.
`TestHandleMasterFailover_SurfacesTheTimestampWriteFailure` and
`TestHandleFailoverRetrigger_SurfacesTheTimestampWriteFailure`
([`sentinel_failover_test.go`](../../../internal/controller/sentinel_failover_test.go)) were adapted
to the single write: the failure they inject is that write's, and they pin that a failed arming
write surfaces as an error and leaves nothing armed (no failover state without its timestamp; on
the retrigger the reset state and its old deadline stand together). They do not tell the two
shapes apart — injected from the first write, the split shape fails its state write and leaves
the same result (read) — so the split is caught only by the arming test, which runs through
`handleMasterFailover`: a split reintroduced at `handleFailoverRetrigger` alone would pass every
test (read, not tried). **Run (e2e):** the final run's image carried both (Verification
(extension)); its hardening test passed on every run. **Not verified:** that any run reached the
90 s clock or a failed arming write — no e2e reproduces either, and the operator log counts for
`hard` in the final run are not recorded. No full unit-tier run including these tests is recorded
here; the CI-parity rerun on the final code includes them, and its result is not claimed.

## Drain-test finding: `TestE2E_SidecarFailoverDrainMaster` (2026-09-26)

**Run.** The final run's ~~single-node~~ Valkey 9 suite *(corrected 2026-09-26, T34: a local run on Kind's control-plane + 3 workers, `final6.log:7-20`)* went red once, 52/53, on
`TestE2E_SidecarFailoverDrainMaster` ([`test/e2e/sidecar_test.go`](../../../test/e2e/sidecar_test.go)).
Its subtest "delete master pod triggers failover" passed in **0.38 s**, and "data survives
failover" then read `DBSIZE 0`.

**Diagnosis** — read from the test code and its timing, supported by a watcher on green runs; the
red run's pod logs were lost with the CR. After deleting the master, every wait of the subtest —
StatefulSet 3/3, phase `OK`, pods Ready, one master — was already satisfied by the **terminating**
old master: kubelet keeps a terminating pod Ready for its whole termination
([ADR 0026](../../adr/0026-a-pod-being-deleted-is-not-available.md)), and it still answered master.
"data survives failover" therefore picked the dying pod as the master, and read `DBSIZE 0` from
its empty replacement. The operator log of that cluster shows no operator action between its
creation and its deletion. The failure is explained by the fixture, which named the pod by
controller state instead of by identity, the rule of [ADR 0017](../../adr/0017-test-and-ci-policy.md)
D50. What the red run does not show is its data plane: whether its new master held all 50 keys
was not observed — the watcher below saw that on green runs only — so a loss in that run's drain
failover is not excluded by evidence; nothing points to one.

**Experiment on Kind.** The original test alone: 10 of 10 green, but 5 of the 10 took the vacuous
0.28–0.30 s path. A watcher recording each pod's role and `DBSIZE` every second showed the new
master holding all 50 keys while the replacement pod read `dbsize=0` for several seconds as it
resynced — the state the diagnosis puts the red run's data check in.

**Fix.** The subtest records the master's UID before the delete and waits for the replacement
under a new UID (`waitForPodRecreated`, [`test/e2e/e2e_test.go`](../../../test/e2e/e2e_test.go):
the named pod exists under another UID and is Ready) before its role and data checks. The fixed
test ran **8 of 8 green on Valkey 9**, its delete subtest taking 8.3–10.3 s. ~~No full-suite run
with the fix is recorded.~~ *(Corrected 2026-09-26, T34: no local one; CI ran the full suite on the
fix in six single-node legs, `b13377e`, `a04e2d0`, `e6a9d7c`, all green.)* The same shape elsewhere in the suite is filed as **T34** on the board
(five sites, ~~not yet audited~~ *audited 2026-09-26: two vacuous, three fine*).

## CI red on `e2ce8bb` (2026-09-26)

The pushed merge commit `e2ce8bb` (`bb6c78f` plus `main`) failed two gate jobs. Neither was a
defect in the operator; both are fixed in the uncommitted working tree (HEAD is still `e2ce8bb`).

- **`Generated Manifests Up To Date`.** A stale local `bin/controller-gen` v0.21.0 had
  regenerated the CRDs and stamped `controller-gen.kubebuilder.io/version: v0.21.0` into them,
  while the Makefile pins `CONTROLLER_GEN_VERSION ?= v0.22.0`; the job installs fresh and
  regenerated v0.22.0. **Cause (read):** `go-install-tool` installs only when the file at the
  tool path is missing, and the path carried no version, so the Renovate bump never reached an
  existing `bin/`. **Fix:** every tool path carries its version (`CONTROLLER_GEN ?=
  $(LOCALBIN)/controller-gen-$(CONTROLLER_GEN_VERSION)` and the six others, [`Makefile`](../../../Makefile)),
  and `go-install-tool` installs into `<path>.install` and moves the binary onto the versioned
  path; [ADR 0017](../../adr/0017-test-and-ci-policy.md) D49 amended the same day. The CRDs are
  regenerated with v0.22.0 — both `config/crd/bases/vko.gtrfc.com_valkeys.yaml` and the chart's
  `templates/crd.yaml` now carry `v0.22.0` (read). The same stale file had served `make lint` a
  golangci-lint older than the pinned v2.14.0 (ADR 0017 D49). *(Read 2026-09-26 with `--version`
  on the files still in `bin/`: the unversioned `bin/controller-gen` is v0.21.0 and
  `bin/golangci-lint` 2.13.1; the versioned `bin/controller-gen-v0.22.0` and
  `bin/golangci-lint-v2.14.0` are what the targets now run.)*
- **`Integration Tests (envtest)`.** `TestPodSecurity_TemplatesSurviveAPIServerDefaulting_Integration`
  read the object back through `k8sClient`, which reads the manager's cache, right after
  creating it — at `e2ce8bb` a `Get` inside its `roundTrip` helper (read with `git show`). The
  cache can lag the write, so the test failed with not-found for nothing: **2 of 4 local runs red
  on `e2ce8bb`**. **Fix:** the helper takes the stored object from the `Create` answer, into which
  controller-runtime decodes the API server's response, defaulting included
  ([`test/integration/pod_security_test.go`](../../../test/integration/pod_security_test.go),
  `roundTrip`). **6 of 6 local runs green, then 3 of 3 more.** This is the defect recorded under
  "Found on the way" (`posture-it` not found once); the fix was not in the pushed commit.
- **Not verified:** that CI is green with both fixes — they are not committed, so CI has not seen them.
- **CI parity, locally, 2026-09-26** *(added)*: `make generate-all` in a clean copy of the working
  tree with an empty `bin/` — a fresh controller-gen v0.22.0 — leaves no diff, so the
  `Generated Manifests Up To Date` fix holds without the stale binary; `make test-release-tooling`
  green. ~~Lint, cyclo, gosec, vuln, the unit and integration coverage targets and the image-tools
  check were still running when this was written; **no result of them is claimed here.**~~
  *(Updated 2026-09-26.)* The same clean-copy run completed green on that tree — the tree before
  the ADR 0025 D9 follow-up and the drain-test fix: lint (golangci-lint v2.14.0, 0 issues), cyclo,
  gosec v2.29.0 (0 issues), vuln (no vulnerabilities), the unit and integration coverage targets
  and the image-tools check. **Not claimed:** the rerun of all of them on the final code, still
  running when this was written.

## Adjacent findings

- **Unbounded wait on a replaced pod that never becomes available** — filed as
  [T32](032-unavailable-replaced-pod-waits-unbounded.md), a prerequisite of this ticket
  (D4).
- SECURITY_ARCHITECTURE.md:319-325 lists four controls the workloads lack and omits
  `allowPrivilegeEscalation`, which ADR 0013 D8 counts as the fifth. Cosmetic; step 17 rewrites
  the bullet anyway.

## History

- 2026-09-27 — **archived** as `archive/031-generated-pods-run-as-root.md` (was `local_T31-generated-pods-run-as-root.md`) when the tickets were numbered; state `done` unchanged.
- 2026-09-26 — **CI green on `e6a9d7c`**, all checks. The pushes `b13377e` and `a04e2d0` failed
  both single-node E2E legs on the hardening e2e alone (user namespaces unavailable in the
  Docker-in-Docker legs; the first probe missed the sandbox refusal); the probe now reads Events,
  and the user-namespace subtest is skipped by name in CI (ADR 0017 D5).
- 2026-09-26 — **extension done**, state `in-progress` → `done`: ADR 0033 (with the D9 Localhost
  allow-list), the second-roll ordering fix, ADR 0025 D9 (own clock, one-write arming), the CI-red
  fixes of `e2ce8bb`, and the drain-test fixture fix; every gate target green on the final tree in
  a clean copy; fleet-upgrade and both full e2e suites run (see the verification sections). Follow-ups
  filed as board rows T33 and T34.
- 2026-09-26 — **final e2e run recorded**, on one image built from the final operator code (ADR
  0025 D9's own clock and the one-write arming included): fleet-upgrade from 1.12.8 green, Valkey 8
  53/53, Valkey 9 52/53 — the one failure `TestE2E_SidecarFailoverDrainMaster`, a fixture defect,
  fixed by waiting for the replacement's new UID and green 8 of 8 on Valkey 9 alone (section
  "Drain-test finding"; the same shape elsewhere filed as T34) — and two extra Valkey 8 runs of
  the hardening and restricted-namespace e2e green. The CI-parity run on the tree before these
  follow-ups completed green (generate-all, lint v2.14.0, cyclo, gosec v2.29.0, vuln, unit and
  integration coverage, image tools, release tooling); its rerun on the final code is not claimed.
  The chart comment on `podSecurity.seccompProfile.localhostProfile` states the render refusal.
  State stays `in-progress`: Hans reviews before `done`.
- 2026-09-26 — **ADR 0025 D9 follow-up** (section "ADR 0025 D9 follow-up"): the window carries
  its own 90 s clock (`ownFailoverInFlight`), and `setFailoverTriggered` arms state and timestamp
  in one write (ADR 0010 D14). The split-brain section's claim that `failoverRetryTimeout` bounds
  the window corrected in place. Unit: four rows, three mutations killed, plus the arming test and
  its mutation.
- 2026-09-26 — **~~final~~ e2e run recorded** *(relabelled 2026-09-26: the run before the final
  one, on an image without ADR 0025 D9's own clock — see the two entries above)*, on one image
  built from the ~~final~~ code of that time: fleet-upgrade
  from 1.12.8 green, both full suites 53/53 (Valkey 9, Valkey 8), two extra Valkey 8 runs of the
  hardening and restricted-namespace e2e green, the ADR 0033 D9 refusal subtest green on every run;
  integration (envtest 1.29) green repeatedly with the ADR 0033 D9 test and the CEL path rows, in
  runs that predate the D9 gate move and ADR 0025 D9; mutations
  killed 7/7 ADR 0033 D9, 1/1 split-brain guard (ADR 0025 D9), 2/2 ordering fix. Lint, cyclo, gosec, vuln, coverage
  and image tools on ~~the final~~ that code *(relabelled 2026-09-26; that CI-parity run completed
  green since, see the newest entry)* not claimed (CI-parity run still running). State stays
  `in-progress`: Hans reviews before `done`.
- 2026-09-26 — **ADR 0025 D9 decided by Hans** (section "Split-brain found by the hardening
  e2e"): the run before ~~the final one~~ ADR 0025 D9 *(re-anchored 2026-09-26)* went red on
  Valkey 8 because the Sentinel rolling update
  demoted the replica Sentinel was promoting (cluster `hard`, both legs: 11 triggers, 11
  demotions, 9 timeouts — ten, ten and nine of them on Valkey 8); pre-existing on `main`. While `failover-triggered`, the double master is now
  reported and not resolved.
- 2026-09-26 — **the ADR 0033 D9 gate moved to the write**: after the ownership proof, the
  claim guard, the TLS record and the repair decision, before the drift detection; the residual
  risk "while D9 refuses, the StatefulSet steps measure nothing" closed. In the same change the
  chart refuses an absolute or `..` operator `localhostProfile` at render, and the
  `localhostProfile` doc comment (and CRD description) names the allow-list.
- 2026-09-26 — **adversarial check of the D9 write-up** against the code: the D9 unit tests rerun
  uncached (green); ADR 0033 D9 narrowed to "every write of a workload's pod template" (the
  nudge and the observer delete are not gated), a new residual risk for what the StatefulSet
  steps stop measuring while D9 refuses (ownership check, `StorageSpecNotApplied`), the
  "nothing pins the unconditional step" risk narrowed, the chart's fail-closed blank-entry
  cases recorded, and the persistent data tier's second roll named in the first Consequence.
- 2026-09-26 — **fourth question decided: an operator allow-list for `Localhost` profiles**
  (ADR 0033 D9, section "Fourth question"). Hans first answered "document only", then reversed it
  in the same session; the allow-list is default-deny, and the CEL path rule came with it. The
  "Measures, per pod" rows for `hostUsers` and seccomp corrected. D9 unit tests green; its
  integration test, the new CEL rows and its e2e subtest not yet run; the rerun of the
  fleet-upgrade e2e and both full suites with D9 is in progress. State stays `in-progress`: Hans
  reviews before `done`.
- 2026-09-26 — **the extension's e2e ran** (before D9): fleet-upgrade from 1.12.8 green, Valkey 8
  53/53, Valkey 9 52/53 with the hardening test's own `/data` owner assertion as the one failure,
  fixed in the test and green on both lines (Verification (extension)).
- 2026-09-26 — **CI red on `e2ce8bb` analysed**: a stale unversioned `bin/controller-gen` and a
  cache read in an integration test; both fixed, uncommitted (section "CI red on `e2ce8bb`").
- 2026-09-26 — **reopened** for the extension above (ADR 0033), state `done` → `in-progress`;
  and the ordering fix of the second roll, found by the fleet-upgrade e2e.
- 2026-09-26 — **implemented and done** on `feat/rootless` together with T32, state `decided` →
  `done`; every Verification line recorded with its run. Review round and open questions: Implementation
  notes. After the first commit (`bb6c78f`) Hans decided the two open questions: a second roll
  replaces the pods that carry the retired repair (ADR 0032 D2), and a Sentinel tier of one or
  two rolls serially (ADR 0024 D10, from T32). The fleet-upgrade e2e cannot start from 1.10.48 on
  an arm64 host (the released images
  are amd64-only, "no match for platform in manifest"); it was run from 1.12.8 with the amd64
  image loaded into Kind under emulation.
- 2026-09-26 — **decided** by Hans in a question round, state `analysed` → `decided`,
  `blocked-by: decision` → `T32`. He rejected the framing of the first analysis outright: root is
  a defect, not an option, so there is no `spec.podSecurity.level`, no `baseline` level and no
  opt-out, and existing clusters move with the operator upgrade. That withdrew option set A
  (A1 baseline everywhere, A2 inherit-or-restricted — recommended at the time, A3 CRD default
  plus hook pin — rejected because the hook runs before the new CRD and its pin is pruned), the
  opt-in `repairDataOwnership` field of option B2, and the e2e level selector of option C. Then
  decided: M1 (history-triggered repair, hash-neutral), S1 (single-pod split by persistence), D1
  (T32 filed as prerequisite). Found in the same round and recorded under Fact: the image-only
  `isSidecarOnlyChange` would have mis-handled single-pod clusters (ADR 0007 D7), and the
  fleet-upgrade e2e runs outside CI. The superseded decision text, verbatim:
  > **Open.** Recommendation: A2 + B2 + C as written above. Blocked on Hans, because A2 amends
  > ADR 0005 D1 and the 2026-08-20 preference that new features default to off: the protection
  > becomes default-on for **new** clusters, while no existing cluster changes on upgrade.
- 2026-09-25 — filed and analysed from a user request: "the operator sets no securityContext,
  the processes run as root; build clusters that give up every capability possible, and let old
  clusters upgrade onto it". Measured in Docker against both pinned Valkey lines and the default
  exporter image; code read at `9925539`; no cluster touched, no e2e run.
