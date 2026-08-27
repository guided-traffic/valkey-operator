# ADR 0016: Authentication and TLS Posture

## Status

Accepted. Date: 2026-08-21.

Implemented and documented in [SECURITY_ARCHITECTURE.md](../../SECURITY_ARCHITECTURE.md)
sections 2 and 6. ~~Four~~ **Three** items stay open — see Residual risks; two of them carry an
unchecked entry on that document's hardening checklist (section 9).

Amended 2026-08-26 by [ADR 0030](0030-rotating-certificates-rotate-the-instances-that-cannot-reload-them.md):
**D12 no longer holds for TLS material.** The cert-manager residual risk fired — measured on a
live fleet, on the client side — and is rewritten rather than ticked off: the half about
`valkey-server` is still unmeasured and is now covered by replacement instead of by
verification.

## Context

The operator manages a datastore whose only built-in client authentication is a shared
password, and whose TLS support authenticates the *server* to the client. It also has to keep
[ADR 0005](0005-upgrade-neutral-defaults-and-anti-affinity.md)'s rule: an upgrade must not
change an existing cluster's behaviour, which rules out flipping ports or turning
authentication on by default.

Two structural facts constrain everything below:

* **Kubernetes resolves `env.valueFrom.secretKeyRef` exactly once, at pod start.** Whatever the
  operator hashes, a value change inside a Secret cannot reach a running pod.
* **`sentinel.conf` requires the credential inside the file**, because Sentinel rewrites that
  file at runtime.

## Decision

**D1 — Authentication is opt-in, and the auth Secret is always user-owned.**
`spec.auth.secretName` names a Secret the **user** creates; the operator never generates one.
Never generating a credential keeps the operator out of the secret-lifecycle business: there
is no operator-owned password to rotate, to leak into status, or to orphan on CR deletion.
**The cost is stated rather than hidden:** a CR without `spec.auth` runs an unauthenticated
Valkey, and the generated config always sets `protected-mode no`, so the only remaining
barrier is the network.

**D2 — The password reaches every consumer as an env var from the same `secretKeyRef`**, and
the `--requirepass "$VALKEY_PASSWORD"` shell-wrapper form is kept. `VALKEY_PASSWORD` for the
`valkey` container, the init container, the `sidecar` container and the observer;
`REDIS_PASSWORD` for the exporter. The operator reads the Secret through its cache-backed
client (`readValkeyPassword`) and passes the plaintext into a single client call; only that
string is call-scoped. The Secret object itself stays resident in the manager's Secret
informer cache for the process lifetime, because the controller watches `corev1.Secret`
cluster-wide with no cache filter. This is the standard Redis/Valkey deployment pattern and
keeps the pod spec free of literal secret values — it references, never embeds. **The
exposure is declared, not defended:** neither the env visibility nor the argv visibility is a
defect, but neither is this a secret store.

**D3 — No credential is written into a ConfigMap, the CR status, an Event, or a log.** The
operator logs pod names, addresses and roles. **The two halves are verified to different
strengths, both by reading:** the ConfigMap half is structural — `GenerateValkeyConf` renders no
password and `sentinel.conf` carries a placeholder instead (D4) — while the status, Event and
log half rests on a grep over the log and Event call sites, which finds no password value among
their arguments. No redaction test enforces it.

**D4 — Where a config file structurally requires the secret, the ConfigMap carries a
placeholder.** `sentinel.conf` ships with the literal `%VALKEY_PASSWORD%` and the
`init-sentinel-config` init container substitutes it from the env var into a writable copy on
an `emptyDir`. ConfigMaps are not Secrets: readable by anyone with `get configmaps` in the
namespace, not covered by a Secret-only KMS config, and they end up in GitOps diffs and
backups. **Any new config surface that needs a credential follows this pattern.** The Valkey
config needs no placeholder at all — `GenerateValkeyConf` renders no password.

**D5 — TLS material comes from exactly one of two sources, and the operator never generates
or uses a private key.** Either `spec.tls.secretName` (user-provided, with
`tls.crt`/`tls.key`/`ca.crt`), or `spec.tls.certManager`, where the operator creates a
cert-manager `Certificate` as an `unstructured.Unstructured` object — **no typed cert-manager
dependency** — and cert-manager issues the Secret. For its own connections the operator picks
only `ca.crt` out of that Secret (`buildTLSConfig`): it signs nothing and presents no client
certificate. **It does `Get` and cache the whole Secret object**, and D2's cluster-wide Secret
watch has no cache filter, so issued private keys are resident in the manager's informer cache
for the process lifetime — narrowing that cache is a hardening item
([ADR 0013](0013-operator-is-cluster-wide-privileged.md)). The `unstructured` handling is what
keeps the operator installable and runnable on clusters without cert-manager: nothing
references the cert-manager types at compile time, and `reconcileTLSCertificates` is gated on
`IsCertManagerEnabled`. The graceful skip on an absent CRD (`meta.IsNoMatchError`) is
implemented for the Prometheus ServiceMonitor only
([ADR 0018](0018-metrics-and-the-exporter-sidecar.md)) — `reconcileCertificate` returns the
`NoKindMatch` unchanged, so a CR that does set `spec.tls.certManager` on a cluster without
cert-manager fails its pass rather than skipping.

**D6 — TLS provides confidentiality and server authentication only.** The rendered config
always sets `protected-mode no`; under TLS it sets `tls-auth-clients optional` (client
certificates accepted, never required), `tls-replication yes`, `tls-port 16379`
(Sentinel 36379), and `port 0` unless `allowUnencrypted`. All four TLS directives live inside
the same `IsTLSEnabled()` block in `generateValkeyConf` and `GenerateSentinelConf`, so a
cluster without TLS renders no `tls-auth-clients` line at all. **The password is the only
client authentication mechanism.** Requiring client certificates would force every client of
every cluster to hold issued material the operator does not manage.

**D7 — Enabling TLS closes the plaintext port unless explicitly told otherwise.**
`spec.tls.enabled: true` moves the data port to 16379 and closes 6379. TLS that leaves the
cleartext port listening is not TLS for anyone who forgets to reconfigure a client.

**D8 — Where TLS and auth are on, Sentinel inherits both, and downgrading Sentinel below them
takes an explicit field.** `spec.sentinel.allowUnencrypted` and `spec.sentinel.disableAuth`
both default to `false`, and each is inert without the outer feature: `sentinel.conf` renders
its TLS block only under `IsTLSEnabled()` and `requirepass` only under `IsAuthEnabled()`.
**Among these two switches the secure configuration is the one you get by omitting the field**,
so an encrypted, authenticated cluster cannot lose its Sentinel port through inattention — only
through a field a reviewer can see in the manifest. The outer defaults run the other way
(`spec.tls.enabled: false` per [ADR 0005](0005-upgrade-neutral-defaults-and-anti-affinity.md)
D1, `spec.auth` absent per D1 above), so on a default CR Sentinel is plaintext and anonymous
because nothing was enabled, not because a switch was flipped.

**D9 — The three downgrade switches are migration-only and never a steady state.**
`spec.tls.allowUnencrypted` and `spec.sentinel.allowUnencrypted` exist for clients that cannot
do TLS yet. `spec.sentinel.disableAuth` is not a TLS switch: it suppresses `requirepass` in
`sentinel.conf` while still emitting `sentinel auth-pass`, so Sentinel keeps authenticating to
the Valkey nodes and only its own clients connect anonymously — the migration it serves is a
client that cannot authenticate to Sentinel, which is a different one. The operating rule is the
same for all three: **do not leave any of them on after the migration that needed them.** An
escape hatch left permanently open becomes the deployment's actual posture.

**D10 — `spec.tls.unifiedCertificate` gives Valkey and Sentinel one Secret covering both sets
of hostnames, and migrates the legacy material automatically under cert-manager.** go-redis in
Sentinel mode dials both the Sentinel and the data endpoint against the same TLS config, so two
separate certificates produce verification failures the user cannot fix from the client side —
a client-side observation, not reproducible from this repository: go-redis is not a dependency
(`go.mod`). Default `false`, so upgrades change nothing. The deletion of the legacy
`<name>-sentinel-tls` Certificate and Secret is **part of the switch, not a follow-up chore**,
and it is provenance-gated ([ADR 0006](0006-delete-only-what-the-operator-owns.md)) — but the
automatic half runs **only on the cert-manager source.**
`reconcileLegacySentinelCertificateCleanup` early-returns unless `IsCertManagerEnabled()`, and
its only caller `reconcileTLSCertificates` is itself a reconcile step gated on
`IsCertManagerEnabled`. With a user-provided `spec.tls.secretName` both StatefulSets already
mount that one Secret, so the flag is informational and no split material is ever produced —
but legacy `<name>-sentinel-tls` material issued while the instance still ran cert-manager is
then collected by nothing and has to be deleted by hand.

**D11 — The legacy cleanup is not gated on Sentinel being enabled.**
`IsUnifiedCertificateEnabled` stays `IsTLSEnabled() && Spec.TLS.UnifiedCertificate` and never
consults Sentinel, so within cert-manager mode — D10's gate still applies above it — the
cleanup remains reachable with Sentinel disabled, on the first reconcile:
`sentinelRolloutComplete` short-circuits to "complete" when no Sentinel pod is bound to the
legacy Secret. A Sentinel gate would strand the legacy material of any instance that turns
Sentinel off, which nothing else cleans up. **The protection is the provenance guard, not the
reachability of the path** — and the zero-rollout-window shape is pinned by its own test
(`TestReconcileLegacySentinelCleanup_NoSentinelStillGuardsTheDelete`).

**D12 — Propagation is hash-driven, so Secret *names* propagate and Secret *values* do not.**
`spec.image`, resources, probes and config changes alter the pod-spec or config hash and ride
the failover-aware rolling update. Changing `spec.auth.secretName` or `spec.tls.secretName` to
a **different Secret** propagates, because the name is part of the pod spec. Changing the
password **inside** the auth Secret does not. ~~Hashing the referenced name rather than the
resolved value is what keeps secret material out of the pod template and out of every hash the
operator publishes.~~

> **Amended 2026-08-26 — the second sentence is now false for TLS, and deliberately so.**
> [ADR 0030](0030-rotating-certificates-rotate-the-instances-that-cannot-reload-them.md) D4
> stamps a fingerprint of the **content** of `ca.crt`, `tls.crt` and `tls.key` onto both
> StatefulSet pod templates — as the `VKO_TLS_MATERIAL_HASH` env var of the tier's carrier
> container since 2026-08-27
> ([ADR 0031](0031-a-record-the-operator-trusts-lives-in-pod-spec.md)) — because a long-lived process
> that parsed a certificate at startup keeps presenting it until it exits, and rotation had to
> reach those processes somehow. **D12 stands unchanged for the auth password**, and ADR 0030
> D11 states why the exception must not be extended to it: a 32-bit digest of a high-entropy
> private key confirms nothing, a 32-bit digest of a password is a brute-forceable oracle.
> The password rotation gap of section 6 therefore stays open.

**D13 — Password rotation is a documented manual procedure, stated precisely rather than
glossed.** The Secret **is** watched and a change does enqueue a reconcile, so:

1. running pods keep serving with the **old** password, and no rolling update is triggered;
2. the operator re-reads the Secret and starts authenticating with the **new** password
   against pods that still expect the old one — its health checks and any `REPLICAOF` it needs
   to send begin to fail;
3. the cluster converges only when every pod is restarted.

Rotating therefore means: change the Secret, then roll the pods yourself, replicas first,
master last, accepting that a cluster without persistence loses in-memory data if it has no
failover target. The enqueue in the premise is pinned by the `findValkeyForSecret` unit tests;
steps 2 and 3 are derived by reading `readValkeyPassword` and the `secretKeyRef` fact in the
Context, and were not reproduced against a cluster.

## Consequences

* **A CR that omits `spec.auth` is a fully open datastore** reachable by anything the
  NetworkPolicy allows — and NetworkPolicies are themselves opt-in
  ([ADR 0013](0013-operator-is-cluster-wide-privileged.md) D7).
* **Anyone with `exec` into a Valkey pod has the cluster password.** It is readable via
  `kubectl exec ... env` in every one of those containers — as `VALKEY_PASSWORD`, and as
  `REDIS_PASSWORD` in the exporter sidecar — and any process inside the pod can read it from
  `/proc`, because it appears in `valkey-server`'s argv.
* The **rendered** `sentinel.conf` inside the pod does contain the plaintext password, on an
  `emptyDir` that lives and dies with the pod. The ConfigMap object in etcd never holds it.
* **A cluster with `spec.auth` unset and TLS enabled is encrypted but unauthenticated in both
  directions.** `protected-mode no` is rendered unconditionally, including when auth is off.
* User-owned Secrets and PVCs survive CR deletion, by design.
* Mutual exclusivity of the two TLS sources is documented but **not enforced at admission**
  ([ADR 0015](0015-one-crd-validated-by-schema-only.md) D3).
* Clients that cannot do TLS or auth need an explicit, auditable opt-in field in the CR — the
  weakened posture is visible in the spec.
* The `unifiedCertificate` migration is why the operator needs `delete` on core `secrets` at
  all, and the missing verb wedged every migrating cluster once — observed on a cluster and
  not reproducible from this repository
  ([ADR 0014](0014-rbac-lives-in-three-places.md)).
* A password rotation degrades the cluster until it is manually rolled.

## Alternatives Considered

### Operator-generated auth Secret when `spec.auth` is absent

Not taken: it would put the operator into the secret-lifecycle business — generation, rotation,
orphaning on delete — for a credential the user must hand to their clients anyway.

### `tls-auth-clients yes` (mutual TLS)

An open hardening item, not the default: it forces every client of every cluster to hold issued
material the operator does not manage.

### A typed cert-manager client dependency

Rejected in favour of `unstructured`, which keeps the operator installable on clusters without
cert-manager.

### Write the rendered `sentinel.conf` into a Secret instead of a ConfigMap

Not taken; the placeholder plus init-container substitution was chosen instead.

### No escape hatch at all — force an atomic TLS cutover

Rejected as unusable for clients mid-migration.

### Keep the legacy plaintext port open by default

Rejected as a silent downgrade, twice: for the data port and for Sentinel.

### Two separate TLS certificates

The pre-existing behaviour, kept as the **default** so upgrades change nothing — but it is the
shape that breaks go-redis Sentinel mode, which is why D10 exists. Same client-side observation
as D10, with the same limit: not reproducible from this repository.

### Manual deletion of the legacy Sentinel Secret

Rejected: the deletion is part of the switch.

### Gate the legacy cleanup behind `IsSentinelEnabled()`

Rejected — see D11.

### Hash the resolved secret value so a password change propagates

Rejected: it would put secret-derived material into the pod template.

### Automatic password propagation without data loss

Named as an open product wish (`.github/idea.md`), explicitly **not** an implemented feature.

## Residual risks

* **`tls-auth-clients optional` (open)** — TLS authenticates the server only. "Require client
  certificates where the deployment can" is on the hardening checklist.
* **Nothing in the operator expires or warns about a downgrade switch left enabled (open).**
  Enforcement of D9 is a documented human checklist item only.
* ~~**cert-manager renewal is only partially covered:** the Secret content changes and the mount
  follows it, but **whether the running `valkey-server` reloads the new material is not
  verified in this repository.**~~ **Fired 2026-08-26, and rewritten rather than closed.** The
  risk was real and it was measured — on the **client** side, not the server one: every
  long-lived process of ours that had parsed its certificate at startup kept presenting it
  until it expired, which killed the sidecar labeler, the Sentinel cross-check and the ADR 0012
  drain promotion on every TLS cluster whose pods outlived a rotation. The answer is
  [ADR 0030](0030-rotating-certificates-rotate-the-instances-that-cannot-reload-them.md):
  processes this repository owns re-read their material, everything else is replaced by a
  rolling update the rotation triggers. **Whether `valkey-server` itself reloads is still not
  verified** — ADR 0030 D6 treats it as pinning precisely so that nobody has to find out.
* `protected-mode no` is rendered unconditionally, so Valkey's own last-resort refusal to serve
  unauthenticated external clients is removed even on clusters with auth off.

## References

* [`api/v1/valkey_types.go`](../../api/v1/valkey_types.go) — `AuthSpec`, `TLSSpec`, `IsAuthEnabled`, `IsTLSEnabled`, `IsUnifiedCertificateEnabled`
* [`internal/builder/configmap.go`](../../internal/builder/configmap.go) — the rendered directives, `GenerateValkeyConf`
* [`internal/builder/sentinel.go`](../../internal/builder/sentinel.go) — the `%VALKEY_PASSWORD%` placeholder and `init-sentinel-config`
* [`internal/builder/certificate.go`](../../internal/builder/certificate.go) — the `unstructured` cert-manager `Certificate`
* [`internal/controller/valkey_controller.go`](../../internal/controller/valkey_controller.go) — `readValkeyPassword`, `findValkeyForSecret`, the legacy-material cleanup
* [SECURITY_ARCHITECTURE.md](../../SECURITY_ARCHITECTURE.md) — sections 2 and 6, and the hardening checklist
* [ADR 0005](0005-upgrade-neutral-defaults-and-anti-affinity.md) — why none of these defaults is the secure one
* [ADR 0006](0006-delete-only-what-the-operator-owns.md) — the provenance gate on the legacy cleanup
* [ADR 0013](0013-operator-is-cluster-wide-privileged.md) — the surrounding trust model
* [ADR 0030](0030-rotating-certificates-rotate-the-instances-that-cannot-reload-them.md) — how a rotated certificate reaches a running process, and the bounded exception to D12
* [`internal/tlsmaterial/reloader.go`](../../internal/tlsmaterial/reloader.go) — the reload half
* [`internal/builder/tls_material.go`](../../internal/builder/tls_material.go), [`internal/controller/tls_material.go`](../../internal/controller/tls_material.go) — the fingerprint half
* [ADR 0015](0015-one-crd-validated-by-schema-only.md) — why the two TLS sources are not mutually exclusive at admission
