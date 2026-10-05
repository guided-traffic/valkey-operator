# Secrets and TLS

Where the cluster password and the TLS material come from, which component reads which part
of them, and where credentials are deliberately not written. The decision behind the
authentication and TLS posture is [ADR 0016](../adr/0016-authentication-and-tls-posture.md). How a changed password or a renewed
certificate reaches running pods is
[rotation and change propagation](rotation-and-change-propagation.md); who holds a
Kubernetes credential is [trust boundaries](trust-boundaries.md).

## The cluster password

`spec.auth.secretName` names a Secret the **user** creates and owns; the operator
never generates one. Authentication is on only when that field is set
(`IsAuthEnabled`, [`api/v1/valkey_types.go:1169`](../../api/v1/valkey_types.go)) — a CR
without `spec.auth` runs an **unauthenticated** Valkey, and the generated config
sets `protected-mode no`
([`internal/builder/configmap.go:70`](../../internal/builder/configmap.go)), so the only
remaining barrier is the network.

| Consumer | How it receives the password | Reference |
|---|---|---|
| `valkey` container | env `VALKEY_PASSWORD` from `secretKeyRef`, expanded into `--requirepass` / `--masterauth` by a shell wrapper | [`statefulset.go:830,874`](../../internal/builder/statefulset.go) |
| init container | same env var, used for the `-a` flag of its discovery probes | [`statefulset.go:376,561`](../../internal/builder/statefulset.go) |
| `sidecar` container | same env var | [`statefulset.go:946`](../../internal/builder/statefulset.go) |
| `exporter` sidecar | env `REDIS_PASSWORD` from the same `secretKeyRef` | [`statefulset.go:1088`](../../internal/builder/statefulset.go) |
| observer | same env var | [`internal/builder/observer.go:249`](../../internal/builder/observer.go) |
| **operator** | reads the Secret through the API and holds the plaintext in memory for the duration of a call | [`readValkeyPassword`, `valkey_controller.go:177`](../../internal/controller/valkey_controller.go) |

Consequences worth naming: the password is visible in every one of those
containers' environments (`kubectl exec ... env`, and in the pod spec as a
reference, not a value), and the `--requirepass "$VALKEY_PASSWORD"` form means the
**expanded password appears in the `valkey-server` process arguments** inside the
container, so any process in that container can read it from `/proc` — not one in
another container of the pod, because no generated pod shares its process namespace
([workload pod posture](workload-pod-posture.md#the-fields-on-every-generated-pod)). Both are the
standard Redis/Valkey deployment pattern; neither is a defect, but neither is a
secret store either.

**The exporter is the one consumer that also listens on the network**, and its listener on
`spec.metrics.port` is plain HTTP without authentication. Until 2026-09-29 it served a route,
`/scrape`, that connected to any target a request named, with this password, and returned key
values; one unauthenticated request from anything that reached the port sent the password of
the default user to a host of the caller's choosing. The operator now switches that route and
the export of key values off on every cluster
([ADR 0018](../adr/0018-metrics-and-the-exporter-sidecar.md) D11), and the generated
NetworkPolicy no longer admits every source on the port. It stays open where the switch does
not reach: an image set through `spec.metrics.image` older than v1.83.0, and the pod of a
non-persistent single-replica cluster, with or without Sentinel, until it restarts
(`PodSecurityUpdatePending=True/ExporterOutdated` names it).

## TLS material

Two mutually exclusive sources ([`TLSSpec`](../../api/v1/valkey_types.go)):

- `spec.tls.secretName` — a Secret the user provides (`tls.crt`, `tls.key`, `ca.crt`).
- `spec.tls.certManager` — the operator creates a **cert-manager `Certificate`**
  (`unstructured`, no typed dependency) and cert-manager issues the Secret. The
  operator never writes a private key and never persists one of its own. It does **hold**
  them: the manager cache backs an unfiltered Secret informer, so every watched Secret —
  `tls.key` and every cluster password included — is resident in operator memory for the
  process lifetime. That is not new with the fingerprint, and it is what the `secrets` scope
  gap [H-1](privilege-footprint.md#h-1) is about.

**Two different consumers read that Secret, and they read different parts of it.**

| Consumer | Reads | Why |
|---|---|---|
| the reconciler's and the health checker's own client config | `ca.crt` only | they verify the server and present **no client certificate**, so a rotation never breaks them |
| the material fingerprint (`ComputeTLSMaterialHash`) | `ca.crt`, `tls.crt`, `tls.key` | it has to notice that the *content* changed, which is what triggers the roll |

The fingerprint is a 32-bit FNV-1a digest, carried as the `VKO_TLS_MATERIAL_HASH`
environment variable of the sidecar container on the data tier and the sentinel
container on the Sentinel tier, on both StatefulSet pod templates, and therefore
**readable by anyone with `get pods` or `get statefulsets`**. It is derived from a
private key, which is high-entropy and not guessable, so the digest confirms nothing
an attacker does not already hold.

It sits in the pod **spec** rather than in pod metadata since 2026-08-27, because
metadata is patchable by anything holding the sidecar token and spec is not — and the
cheap attack was never forging the value but *deleting* it: both consumers skip a pod
that carries no record, so one merge patch setting the key to `null` switched the roll
off. The superseded `vko.gtrfc.com/tls-material-hash` annotation is still read for pods
written before that date and is never written again
([ADR 0031](../adr/0031-a-record-the-operator-trusts-lives-in-pod-spec.md)).

The same construction over the **password** would be a brute-forceable oracle, and
[ADR 0030](../adr/0030-rotating-certificates-rotate-the-instances-that-cannot-reload-them.md)
D11 refuses it there — **at any digest strength**, because what makes the TLS case safe is that
nobody enumerates 2048-bit RSA keys, not that FNV-1a is narrow. A wider hash of a password is a
marginally slower oracle, not a safe one.

**It is a change detector, not an integrity control, and must not be read as one.**
FNV-1a is non-cryptographic and 32 bits wide, and `tls.key` is hashed last, so trailing
bytes appended after the PEM block — which every PEM parser ignores — let a chosen digest
be hit by search. Anyone who can **write** the TLS Secret can therefore replace the
material while keeping the fingerprint identical, and neither the rolling update nor
`TLSMaterialStale` would notice. That principal can already replace the cluster's TLS
identity outright, so this buys evasion of the report rather than new access — but the
report must not be presented as evidence that the material is unchanged.

**Decided 2026-08-27: this stays.** A wide cryptographic digest would remove the collision and
therefore the silence, and it was still not taken, because of what remains afterwards: the
attacker loses the silent swap and gains one **indistinguishable from a legitimate rotation** —
same fleet roll, same condition transition, no observer for whom the two differ. What would
raise the ceiling is a trust anchor outside the Secret, not a better hash over it, and nothing
here has one. ADR 0030 D11 carries the reasoning and the two counter-arguments that do not
hold.

**The record used to be writable from inside a data pod, and is not any more.** The sidecar
Role grants `pods: patch` on this cluster's data pods. Until 2026-08-27 the fingerprint was a
pod annotation and every container of the data pod mounted the token, so `valkey-server` and
the third-party exporter could delete or overwrite it and suppress both the roll and the
staleness report. Two changes closed it, and either alone would have left a hole:

- **The token reaches one container.** `automountServiceAccountToken: false` on the data pod
  plus a projected volume mounted into the `sidecar` container
  ([ADR 0012](../adr/0012-the-sidecar-records-its-drain-promotion-on-the-pod.md) D8 step 4).
  `valkey-server`, both init containers and the exporter now hold no credential — but the
  sidecar must keep the grant, so this does not close the record.
- **The record left pod metadata.** It is an env var of that container's spec, and env is not
  one of the fields the API server lets a pod update change, so the patch is refused for every
  principal — the compromised sidecar included, and the operator too
  ([ADR 0031](../adr/0031-a-record-the-operator-trusts-lives-in-pod-spec.md)).

Two corrections to what this section used to say. It called the annotation the *third*
forgeable field of that grant. [Isolation and tenancy](isolation-and-tenancy.md#what-does-not-hold) now enumerates them instead of counting: nine rows,
eight of them still live. And it treated forgery as the attack: **deletion was cheaper**, and
no digest strength would have touched it.

**Who reloads and who is replaced.** A process that parsed a certificate at startup
keeps presenting it until it exits — measured on a live fleet, it killed the sidecar
labeler, the Sentinel cross-check and the drain promotion of
[ADR 0012](../adr/0012-the-sidecar-records-its-drain-promotion-on-the-pod.md) on
every TLS cluster whose pods outlived a rotation, silently. The rule that follows is
in [rotation and change propagation](rotation-and-change-propagation.md) and in ADR 0030: **a long-lived process this repository owns re-reads its
material; every other process is replaced by a rolling update the rotation triggers.**

Server-side settings the operator renders
([`configmap.go:80-93`](../../internal/builder/configmap.go)):

| Directive | Value | Meaning |
|---|---|---|
| `port 0` | when `tls.enabled` and not `allowUnencrypted` | plaintext port closed |
| `tls-port 16379` | when TLS is on | Sentinel uses 36379 |
| `tls-replication yes` | always under TLS | replication traffic is encrypted |
| `tls-auth-clients optional` | always | **client certificates are accepted, never required** — TLS gives confidentiality, not client authentication. The password is the only client authentication |
| `protected-mode no` | always | see above |

`spec.tls.allowUnencrypted: true` keeps 6379 open next to 16379, and
`spec.sentinel.allowUnencrypted` does the same for 26379 — both default `false`
and both are a deliberate downgrade for clients that cannot do TLS yet.
`spec.sentinel.disableAuth: true` removes `requirepass` from **Sentinel** while
keeping `sentinel auth-pass` toward the data nodes: anyone who can reach port
26379/36379 can then read the topology and issue Sentinel commands without a
password.

## Where credentials are *not*

- No credential is written into a ConfigMap, and the Sentinel path is the case
  worth knowing: `sentinel.conf` needs `requirepass` and `sentinel auth-pass`
  *inside the file*, so the ConfigMap carries the literal placeholder
  `%VALKEY_PASSWORD%` and the `init-sentinel-config` init container substitutes it
  from the env var into a writable copy on an `emptyDir`
  ([`internal/builder/sentinel.go:180-188,715`](../../internal/builder/sentinel.go)).
  The consequence to be aware of: the **rendered** file inside the Sentinel pod
  does contain the plaintext password, on an `emptyDir` that lives and dies with
  the pod, and Sentinel rewrites that file at runtime. The ConfigMap object in etcd
  never holds it. The Valkey config needs no placeholder at all —
  `GenerateValkeyConf` renders no password and `valkey-server` gets it through
  `--requirepass "$VALKEY_PASSWORD"`.
- No credential is written into the CR status or into an Event.
- The operator logs no password; it logs pod names, addresses and roles.

## What this does not cover

<a id="h-4"></a>

### H-4: Do not extend the TLS material fingerprint to low-entropy secrets — at any digest strength

The `VKO_TLS_MATERIAL_HASH` record is a digest of Secret
content, published on the pod template and readable with `get pods`. Over a
private key that is harmless, because nobody can enumerate 2048-bit RSA keys;
over the cluster password it would be a brute-forceable oracle, because an
attacker holding the digest guesses candidates and hashes them. **The security
parameter is the entropy of the input, not the width of the digest** — this entry
used to say "32-bit", which read as though SHA-256 would make the password case
safe. It would not; it would only make the guessing marginally slower. Moving
the carrier into the pod spec
([ADR 0031](../adr/0031-a-record-the-operator-trusts-lives-in-pod-spec.md))
changed who can *write* it and nothing about who can read it. ADR 0030 D11
bounds the exception to TLS material, and the password rotation gap in
[rotation and change propagation](rotation-and-change-propagation.md#the-password-rotation-gap) must not be closed by copying it.

<a id="h-5"></a>

### H-5: Require client certificates where the deployment can

`tls-auth-clients optional` means TLS authenticates the server only.

<a id="h-6"></a>

### H-6: Give the probes, the sidecar, the exporter and the observer least-privilege Valkey ACL users

Today every component authenticates with the one cluster password
([ADR 0016](../adr/0016-authentication-and-tls-posture.md) D2) and full rights, so the
exporter — a third-party image — holds the same authority as a client that may `FLUSHALL`.

<a id="h-7"></a>

### H-7: Pin `enable-debug-command` and `enable-module-command` to `no` in the generated config

The config builder renders neither, nor `enable-protected-configs`
([`configmap.go`](../../internal/builder/configmap.go), verified by grep), so the default
compiled into the image a cluster runs applies. ~~Believed `no` since Redis 7; **not
re-checked for either pinned Valkey line**.~~ *(corrected 2026-09-27, read in upstream
source:)* on both pinned lines ([`images.go`](../../test/testimages/images.go)) that default is
`no` — `src/config.c` of `valkey-io/valkey` defines all three directives as `IMMUTABLE_CONFIG`
with default `no`, lines 3375-3377 at tag `9.1.1` and lines 3267-3269 at tag `8.1.9`, so
`CONFIG SET` cannot switch them on at runtime. **Not verified:** the images themselves — neither
was run with `CONFIG GET` — and any other image a CR author puts in `spec.image`, whose own
default applies. Nothing in this repository states or checks the value.

<a id="h-8"></a>

### H-8: Do not leave `spec.sentinel.disableAuth` or either `allowUnencrypted` on after the migration that needed them

What each of the three switches opens is stated under [TLS material](#tls-material).
