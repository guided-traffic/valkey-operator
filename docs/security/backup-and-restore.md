# Backup and restore

Restore is the one legitimate operation that makes the operator's own objects
look foreign to it, so it gets its own page. The ownership guards
([ADR 0020](../adr/0020-write-only-what-the-operator-owns.md),
[ADR 0006](../adr/0006-delete-only-what-the-operator-owns.md)) prove provenance
through the controller ownerReference's **UID**, and a restore is precisely the
operation that changes UIDs. The guards themselves are described under
[isolation and tenancy](isolation-and-tenancy.md#what-holds).

## What a naive full-namespace restore does

A backup tool that restores the CR *and* the operator-managed children (Velero
restores objects with their backed-up `ownerReferences` but necessarily new UIDs)
produces this sequence:

1. The restored Valkey CR has a **new UID**. Every restored child still carries a
   controller ownerReference pointing at the **old** UID.
2. Every guard refuses correctly — these objects are genuinely not controlled by
   the live CR. The pass reports `ReconcileBlocked` with reason `ForeignObject`
   plus one Warning Event per colliding kind
   ([`internal/controller/foreign_object.go`](../../internal/controller/foreign_object.go)),
   and rechecks every 30 seconds.
3. The Kubernetes garbage collector resolves owner references by UID, treats an
   owner that cannot be verified as absent, and **deletes the restored children**.
   *Upstream behaviour, read from the garbage-collection contract
   ([Kubernetes docs](https://kubernetes.io/docs/concepts/architecture/garbage-collection/));
   not reproduced in this repo — envtest runs no garbage collector (same caveat
   as in ADR 0020's residual risks).*
4. The operator recreates every child with correct ownerReferences on the new
   CR UID, and the guards pass again.

So a full restore **converges on its own**, but through a delete-and-rebuild
window in which the restored pods are removed and recreated. It is churn, not a
dead end.

## The supported restore path: restore state, not derived objects

Everything the operator creates is derived, deterministic state. Only three
things in a namespace are not derivable and are what a backup must carry:

| What | Why it must be in the backup | How it survives the guards |
|---|---|---|
| The **Valkey CR** | Carries the spec and the operator's recorded facts as metadata annotations: `vko.gtrfc.com/known-master` ([`internal/builder/sentinel.go`](../../internal/builder/sentinel.go), [ADR 0008](../adr/0008-known-master-annotation-is-the-recorded-authority.md)) and the rolling-update state family ([`internal/controller/rolling_update.go`](../../internal/controller/rolling_update.go)) | It *is* the owner; guards do not apply to it |
| The **auth Secret** (`spec.auth.secretName`) | User-provided; the operator only reads it and never stamps an ownerReference on it | No guard touches it — it was never operator-owned |
| The **PVCs** | The data. Created by the statefulset-controller from `volumeClaimTemplates` ([`internal/builder/statefulset.go`](../../internal/builder/statefulset.go)); the operator never writes or deletes a PVC and sets no `persistentVolumeClaimRetentionPolicy`, so they carry no ownerReference to anything | The rebuilt StatefulSet's pods rebind them by name |

Everything else — StatefulSets, Services, ConfigMaps, NetworkPolicies, the
sidecar ServiceAccount/Role/RoleBinding, observer Deployment, ServiceMonitor,
Certificates — should be **excluded from the restore**. The operator rebuilds
all of it from the CR on the first pass, with correct ownership. Under
cert-manager, the rebuilt Certificates make cert-manager reissue the TLS
Secrets; a user-provided TLS Secret (`spec.tls.secretName`) is user state and
belongs in the backup like the auth Secret.

A CR restored mid-rolling-update carries stale rolling-update annotations. That
is survivable by design: every rolling-update wait is bounded and expiry hands
over to another bounded state
([ADR 0010](../adr/0010-every-rolling-update-wait-is-bounded.md)), so a stale
state machine expires instead of wedging.

*Not verified: no restore of any kind has been exercised against a real cluster
from this repository. The convergence claim in step 3–4 above rests on the
upstream garbage-collector contract plus the operator behaviour the tests do
prove (refusal, recheck, rebuild-on-absence).*

## Why the operator does not honor `velero.io/restore-name` as adoption evidence

Velero labels every restored object with `velero.io/backup-name` and
`velero.io/restore-name`
([Velero restore reference](https://velero.io/docs/main/restore-reference/)).
It is tempting to treat these as proof that a foreign-looking object is a
restored child and adopt it. The operator deliberately does not: **a label is
writable by anyone who can create the object**, so a label-gated adoption path
would hand the exact capability ADR 0020 closed — a principal with `create` on
a kind in the namespace could stamp the two Velero labels on a colliding object
and have the operator adopt, overwrite and eventually garbage-collect it. The
adoption question and the alternative that was considered live in
[ADR 0020's residual risks](../adr/0020-write-only-what-the-operator-owns.md#residual-risks).

## What this does not cover

The operator takes no backup and carries no restore-specific code: no source file under
`internal/`, `cmd/` or `api/` other than a test mentions `backup` or `velero` (searched
2026-09-27). What a backup carries, and when, is therefore the backup tool's configuration,
and the restore path this page supports has not been exercised against a real cluster (see
[the supported restore path](#the-supported-restore-path-restore-state-not-derived-objects)).
