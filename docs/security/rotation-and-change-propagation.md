# Rotation and change propagation

Which changes reach running pods, and how: a spec change, a renewed certificate, a changed
Secret name — and the password change that does not. The certificate half is
[ADR 0030](../adr/0030-rotating-certificates-rotate-the-instances-that-cannot-reload-them.md). Where the password and the TLS material live is
[secrets and TLS](secrets-and-tls.md).

## What propagates

| Change | Propagates? | Mechanism |
|---|---|---|
| `spec.image`, resources, probes, config | Yes | Pod-spec hash / config hash on the pod template, failover-aware rolling update ([`ComputePodSpecHash`](../../internal/builder/statefulset.go), `ComputeConfigHash`) |
| cert-manager certificate renewal | **Yes**, since 2026-08-26 | The Secret content changes, the mount follows it, and a fingerprint of that content (`VKO_TLS_MATERIAL_HASH` in the carrier container of both StatefulSet pod templates) makes the rotation ride the failover-aware rolling update — on a tier of one or two Sentinels only since 2026-09-26, whose quorum guard had refused every delete of an available Sentinel ([ADR 0024](../adr/0024-the-sentinel-tier-reports-its-own-completion.md) D10). Processes this repo owns re-read their material instead and are exempt. **Whether `valkey-server` itself reloads is still not verified** — it is treated as pinning so that nobody has to find out ([ADR 0030](../adr/0030-rotating-certificates-rotate-the-instances-that-cannot-reload-them.md)) |
| `spec.tls.secretName` (different Secret) | Yes | The name is part of the pod spec, so the hash changes and the pods roll |
| **Password change inside the auth Secret** | **No** | See below |
| `spec.auth.secretName` (different Secret) | Yes | Same reason as the TLS Secret name. *(Added 2026-09-27, read from the code, not measured:)* with a different password in the new Secret the roll pauses at the first replaced replica, which cannot sync from the old master — [pointing `spec.auth.secretName` at another Secret](../operations/authentication.md#pointing-specauthsecretname-at-another-secret) |
| Workload `securityContext` (the operator upgrade onto [ADR 0032](../adr/0032-generated-pods-run-rootless.md)) | Yes, at the operator upgrade (a persistent data tier twice), **except a non-persistent single pod without Sentinel** | Part of the built `PodSpec`, so both pod-spec hashes move and every tier rides the failover-aware rolling update; the observer Deployment follows through its own `securityContext` comparison. Which tier rolls how often and in which order: [what rolls and what restarts](../operations/upgrading.md#what-rolls-and-what-restarts). Two kinds of pod stay root, and [rootless migration](rootless-migration.md#single-pod-clusters-decide-by-persistence) covers both: the deferred non-persistent single pod, which carries `PodSecurityUpdatePending` until its Valkey image, TLS material or configuration changes or it is deleted, and a pod without a `pod-spec-hash` annotation, which is not recognised as outdated ([how existing clusters move](rootless-migration.md#how-existing-clusters-move)) |
| `spec.podSecurity` (`seccompProfile`, `userNamespaces`) | Yes | Part of the built `PodSpec`, so both pod-spec hashes move and the tiers roll failover-aware; the observer Deployment follows through `podSecurityContextChanged`/`podHardeningChanged`. An explicit `RuntimeDefault` hashes like the omitted field and rolls nothing. A `hostUsers: false` the API server drops is reported, not silently lost (`ReconcileBlocked/UserNamespacesUnsupported`, [user namespaces](user-namespaces.md#when-the-api-server-drops-the-field)) |

## The password rotation gap

**The password rotation gap, stated precisely.** The Secret is watched
([`findValkeyForSecret`, `valkey_controller.go:3005`](../../internal/controller/valkey_controller.go),
whose predicate `secretConcernsValkey` matches the auth Secret **and** the TLS Secrets of
both tiers, unified and user-provided — until 2026-08-26 it matched auth Secrets only, so a
certificate rotation enqueued nothing at all)
and a change does enqueue a reconcile — but the password reaches the pods as an
`env.valueFrom.secretKeyRef`, which Kubernetes resolves **once, at pod start**, and
the pod-spec hash covers the *reference*, not the value. So after `kubectl edit
secret`:

1. Running pods keep serving with the **old** password; no rolling update is
   triggered.
2. The operator re-reads the Secret on its next pass and starts authenticating
   with the **new** password — against pods that still expect the old one. Its
   health checks and any `REPLICAOF` it needs to send begin to fail.
3. The cluster converges only when every pod is restarted manually.

Rotating a password today therefore means replacing every pod by hand. The steps for each
tier, and what the cluster goes through until the last pod is replaced, are
[changing the password](../operations/authentication.md#changing-the-password)
*(corrected 2026-09-27: this paragraph stated the procedure here and said a cluster without
persistence loses its in-memory data "if it has no failover target". `masterauth` is the same
value as `requirepass`, so no pod started with the new password can replicate from one still
running with the old: without persistence the procedure loses the dataset at any replica
count — read from the code, not measured)*. Automatic
propagation without data loss is an open product wish
(`.github/idea.md`), not an implemented feature.

## What this does not cover

<a id="h-23"></a>

### H-23: Watch for a certificate roll that never starts

Why a rotation roll is never urgent — the previous certificate stays valid through the
cert-manager overlap — is [certificate rotation](../operations/tls.md#certificate-rotation).
What that window does not cover is a roll that does not
happen at all. The `TLSMaterialStale` condition reports it per cluster and the
shipped `ValkeyTLSMaterialStale` alert fires after **72 h**; the chart's
`PrometheusRule` is **default off**, so this entry stays open until it is
enabled. Read it as a liveness check on the roll, **not** as an integrity check on
the material: the fingerprint it compares is forgeable by whoever can write
the Secret, by collision against a 32-bit digest (see [TLS material](secrets-and-tls.md#tls-material)). That is
**accepted permanently** as of 2026-08-27 — a wide digest would remove the
collision and leave a substitution indistinguishable from a legitimate
rotation, which no observer can act on. See
[ADR 0030](../adr/0030-rotating-certificates-rotate-the-instances-that-cannot-reload-them.md) D11.

<a id="h-24"></a>

### H-24: Rotate the cluster password by hand until propagation exists

Changing the auth
Secret rolls nothing, and the operator starts authenticating with the new password
against pods that still expect the old one ([the password rotation gap](#the-password-rotation-gap)). The steps, tier by tier,
are [changing the password](../operations/authentication.md#changing-the-password). Propagation
must not publish a digest of the password, at any digest strength ([H-4](secrets-and-tls.md#h-4),
[ADR 0030](../adr/0030-rotating-certificates-rotate-the-instances-that-cannot-reload-them.md) D11).
