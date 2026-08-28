# ADR 0031: A per-pod record the operator trusts lives in pod spec, not pod metadata

## Status

Accepted. Date: 2026-08-27.

Implemented: the TLS material fingerprint moved from the
`vko.gtrfc.com/tls-material-hash` pod annotation into the `VKO_TLS_MATERIAL_HASH`
environment variable of the tier's carrier container — the sidecar on the data tier, the
sentinel container on the Sentinel tier. The operator no longer writes the annotation;
`RecordedTLSMaterialHash` reads the env first and falls back to the annotation for objects
written before this date.

Not done, deliberately: `config-hash` and `pod-spec-hash` are forgeable by the identical
mechanism and stay in pod metadata. Consequences names why, and it is not "we forgot".

Amends [ADR 0030](0030-rotating-certificates-rotate-the-instances-that-cannot-reload-them.md)
D4, which named the annotation as the carrier, and closes the second half of the gap
[ADR 0012](0012-the-sidecar-records-its-drain-promotion-on-the-pod.md) D8 step 4 opened the
first half of.

## Context

ADR 0030 made a certificate rotation visible to a pod by stamping a fingerprint of the
Secret's content onto the pod template. The rolling update compares the pod's copy against
the template's; a mismatch replaces the pod, and the `TLSMaterialStale` condition reports a
mismatch that is not being resolved. The carrier was a pod annotation.

An adversarial review of ADR 0030 filed two ways to make that fingerprint lie. The first —
FNV-1a is 32 bits and `tls.key` is hashed last, so anyone who can write the Secret can append
inert bytes until the digest collides — is real and is *not* what this ADR is about; ADR 0030
D11 already states the fingerprint answers "did the roll happen" and never "is the material
unchanged".

The second is the one that forced a design change. The sidecar Role grants `pods: patch` on
this cluster's data pods, and until 2026-08-27 every container of the data pod mounted that
token. So `valkey-server` — the process that terminates client traffic — could patch its own
pod.

**The load-bearing finding was that deletion beats forgery.** Both consumers carry a presence
rule (`recorded != "" && recorded != desired`), because a pod created by an older operator
must be unmeasured rather than stale — that rule is the whole upgrade-neutrality story of
ADR 0030 and is not negotiable. A single merge patch setting the key to `null` therefore makes
the pod **unmeasured**: the roll skips it, the condition does not name it, and no collision is
needed. That kills every scheme that keeps the carrier in pod `metadata`, whatever the digest:
SHA-256, an HMAC, an opaque generation counter, all equally deletable.

Two options addressed it and both were taken, in this order:

1. **Take the token away from the containers that never needed it** — ADR 0012 D8 step 4,
   which shrinks the trust set rather than hardening one field inside it. It removes the whole
   class for `valkey-server`, both init containers and the third-party exporter at once.
2. **Move the record where nobody can patch it** — this ADR. It closes the one gap step 4
   cannot: the sidecar container itself, which by D8 must keep `pods: patch`.

`env` is not one of the five entries in the API server's `updatablePodSpecFields`, so the
change is refused for **every** principal, the operator included. That is stronger than any
RBAC narrowing available: `resourceNames` is the only object-level restriction Kubernetes
offers and it is already in use, and there is no field-level or label-level selector in RBAC.

## Decision

**D1 — A per-pod record the operator later reads back as an input lives in pod `spec`, not in
pod `metadata`.** Metadata is patchable by anything holding `pods: patch` on the pod's name,
and the sidecar of every cluster holds exactly that on its own pods. Spec is not: outside the
five fields the API server allows a pod update to change, the write is rejected. The rule
binds a *record* — a value the operator writes and then trusts. It does not bind telemetry, an
annotation written for a human, or a value the operator recomputes from a source of truth on
every pass.

**D2 — The carrier is an environment variable on the container that belongs to the tier.**
Data tier: the sidecar. Sentinel tier: the sentinel container. One container, one copy — a
second copy is a second truth, and nothing in the pod reads the value. It is a record, not
configuration.

**D3 — The record is stamped after the builder has run, never inside it.**
`StampTLSMaterialHash` mutates the built StatefulSet
([`internal/builder/tls_material.go`](../../internal/builder/tls_material.go)), called from
`reconcileStatefulSet` and `reconcileSentinelStatefulSet`. `ComputePodSpecHash` digests the
spec `buildPodSpec` produced, so folding the fingerprint into the builder would make one
rotation move two independent signals for a single event. The StatefulSet comparison still
sees it — `containerChanged` compares env — which is exactly what has to happen.

**D4 — The presence rule survives the move unchanged.** A pod carrying no record is
unmeasured, not stale. ADR 0005 and ADR 0030 D8 are untouched: an operator upgrade rolls
nothing on account of this mechanism.

**D5 — The superseded annotation is read, never written.** `RecordedTLSMaterialHash` consults
`vko.gtrfc.com/tls-material-hash` only when the spec carries no record. That fallback exists
for one population — objects written before this date — and it is self-extinguishing, because
the roll it enables replaces the pod with one that carries the env. It is not a way back in
for a forger: a pod with the env never consults the annotation, and every pod the operator
writes from now on has the env.

Without it the **Sentinel tier** would go silently unmeasured. Sentinel pods carry no sidecar,
so a plain operator upgrade never rolls them (ADR 0005 D11); they would keep the annotation,
the reader would see no env, and a rotation in that window would neither replace them nor
report them — the exact silent failure ADR 0030 exists to prevent, reintroduced by the change
meant to harden it.

**D6 — This rule is not a licence to move every hash.** `config-hash` and `pod-spec-hash` stay
in pod metadata for now. Moving them is a separate change with its own risks (see
Consequences), and the sentence that justifies this one — *a value the operator writes and
then trusts as an input* — is true of them too. They are a filed follow-up, not a decided
non-goal.

## Consequences

* **The record is visible in `kubectl describe pod` under the container's environment rather
  than in the annotation block.** Anything scripted against the annotation reads nothing after
  the next roll. The annotation was never documented as an interface, but it was named in
  `README.md`, `SECURITY_ARCHITECTURE.md` and the chart's `PrometheusRule` comment, all
  updated with this change.

* **A second, permanent read path.** D5's fallback is code that will still be there when the
  last pod predating it is long gone. It is six lines and one comment, and the alternative was
  a coverage hole with no end date. The same shape as every other presence guard in this
  repository.

* **`config-hash` and `pod-spec-hash` remain forgeable.** Suppressing them suppresses a
  rolling update, which is the same severity as what this ADR fixes. They are not moved here
  because `pod-spec-hash` is self-referential — putting it into the spec changes the spec it
  digests — and `config-hash` deserves its own change rather than riding along in one about
  TLS. After ADR 0012 D8 step 4 the reachable principal for both is a compromised sidecar
  container, not `valkey-server`.

* **A rotation now writes to a container's env list.** `StampTLSMaterialHash` replaces an
  existing entry rather than appending: a duplicated env var is a pod the API server rejects,
  which would take the whole StatefulSet write with it.

* **The operator cannot correct the record on a live pod either.** That is the point, and it
  costs nothing: the operator's answer to a wrong record was always to replace the pod.

## Alternatives Considered

**A stronger digest (SHA-256, or an HMAC keyed by an operator-held secret).** Not taken, and
the reason is narrower than it first looked.

It is **irrelevant to this decision**: the attack this ADR closes is deletion of the record,
which is hash-agnostic — a pod carrying nothing is unmeasured whatever the digest would have
been. A strong digest is an answer to a different question, the one about a hostile *Secret
writer*, and that question is settled in
[ADR 0030](0030-rotating-certificates-rotate-the-instances-that-cannot-reload-them.md) D11:
accepted, permanently, because the collision is what makes a substitution silent and removing
it only buys a substitution indistinguishable from a legitimate rotation.

**Two arguments made against it here on 2026-08-27 were wrong and are corrected in place rather
than deleted.** The first was that writing the previous Secret content back byte-identical
satisfies every hash function, so a strong digest closes nothing: it does satisfy every hash
function, but that is a *denial of rotation* and not a forged fingerprint — the material really
was reverted and the digest really does say so. The second was that the migration is
prohibitive because a changed digest function makes every recorded value differ, rolling the
fleet and lighting up `TLSMaterialStale` on the Sentinel tier: real, and solvable with the same
presence-rule widening D5 above already ships — version the record and treat a
previous-generation value as unmeasured rather than stale.

The HMAC variant additionally costs a key that must survive restarts and leader election —
worth remembering the day someone wants a *password* rotation fingerprint, because it is the
construction that would make that safe, and an unkeyed digest is what ADR 0030 D11 forbids
there **at any width**.

**An operator-held record compared against the pod's `creationTimestamp`.** The unforgeability
premise holds — `creationTimestamp` is in apimachinery's immutable ObjectMeta set — and the
proposal still loses. It breaks [ADR 0007](0007-failover-aware-rolling-update.md) D2: today's
predicate is self-satisfying, because the StatefulSet controller recreates the pod from the
very template being compared, which is why a recreated pod is up to date the instant it
appears. A temporal inequality between an apiserver-stamped timestamp and an operator-stamped
record has no such guarantee, and clock skew beyond the 75 s `terminationGracePeriodSeconds`
yields a pod-delete loop with one controlled failover per requeue. Record loss would also have
to adopt "unarmed" for upgrade neutrality, making amnesia indistinguishable from health on
exactly the failure the condition exists to report.

**Content-addressed immutable TLS Secrets** (`<name>-tls-<fingerprint>`, mounted by name so
`pod.spec.volumes` becomes the unforgeable record). The only idea that removes ADR 0030's
failure instead of detecting it. Rejected because the operator holds `secrets:
get;list;watch;delete` and would need cluster-wide `create;update` — a worse grant than the
bug — and because it collides with the `unifiedCertificate` migration and with a user-supplied
Secret name.

**A pod condition via `pods/status`.** Works, and needs cluster-wide `pods/status: patch` on
the operator — a fleet-wide readiness-manipulation primitive — plus a Pod watch this operator
deliberately does not have.

**The `controller-revision-hash` label.** A label, so it rides the same `pods: patch` grant.

**A `ValidatingAdmissionPolicy`, chart-shipped and default off.** The only in-Kubernetes
control that also reaches `ownerReferences`, `finalizers` and `spec.containers[*].image`.
[ADR 0015](0015-one-crd-validated-by-schema-only.md) D2 refuses admission *webhooks*, and its
stated reason is a measured outage of a third-party webhook backend; a VAP has no backend to
lose, so D2's reasoning does not transfer — but D2 would need an explicit amendment rather
than a silent stretch. VAP is GA from Kubernetes 1.30 and this project declares a 1.29 floor,
so it is opt-in or a floor bump. Filed, not taken here.

## Residual risks

* **The Secret writer is untouched, and accepted.** Anyone who can write the TLS Secret can
  replace the material and hit the 32-bit digest by search, so the swap is silent. This ADR is
  about who can lie about the *record*, not about who can change the *material*. Decided
  2026-08-27 to leave it: closing it costs a wide digest and buys a substitution that looks
  exactly like a legitimate rotation. ADR 0030 D11 carries the reasoning.

* **`config-hash` and `pod-spec-hash` are still in metadata** (D6, Consequences). Reachable by
  a compromised sidecar container only, since ADR 0012 D8 step 4.

* **A compromised sidecar still has other levers.** `metadata.ownerReferences`,
  `metadata.finalizers`, any selector label and `spec.containers[*].image` are all reachable
  through `pods: patch`, and none of them is a record this ADR could move.
  `SECURITY_ARCHITECTURE.md` section 3 enumerates them.

* **Not verified: nothing in this repository proves an *older operator* tolerates a pod
  template carrying the env.** The downgrade direction was reasoned about, not measured: an
  older reader looks only at the annotation, finds none, and treats every pod as unmeasured —
  which is the safe direction — but no test in this repository runs an old binary against a
  new template.

* **The e2e that covers this is new and single-path.** `TestE2E_TLS_CertificateRotation_RollsTheFleet`
  covers a Sentinel-less 3-replica TLS cluster. The Sentinel tier's carrier is exercised by
  unit and envtest only.

## References

* [`internal/builder/tls_material.go`](../../internal/builder/tls_material.go) —
  `TLSMaterialHashEnvName`, `StampTLSMaterialHash`, `RecordedTLSMaterialHash`
* [`internal/builder/annotations.go`](../../internal/builder/annotations.go) —
  `AnnotationTLSMaterialHash`, marked superseded in place
* [`internal/controller/tls_material.go`](../../internal/controller/tls_material.go) —
  `stampTLSMaterialHash`, `scanTierTLSMaterial`
* [`internal/controller/rolling_update.go`](../../internal/controller/rolling_update.go) —
  `podTLSMaterialHashChanged`, `tlsMaterialHashFromSts`, `sentinelPodNeedsUpdate`
* [`test/integration/tls_material_test.go`](../../test/integration/tls_material_test.go) —
  the API server refuses a change to pod env and accepts any change to pod annotations
* [`test/e2e/tls_rotation_test.go`](../../test/e2e/tls_rotation_test.go) — the rotation, the
  roll and the dataset, on a live cluster
* [ADR 0030](0030-rotating-certificates-rotate-the-instances-that-cannot-reload-them.md) — the
  mechanism this amends
* [ADR 0012](0012-the-sidecar-records-its-drain-promotion-on-the-pod.md) D8 step 4 — the token
  narrowing this completes
* [ADR 0020](0020-write-only-what-the-operator-owns.md) — provenance before every write
* [ADR 0005](0005-upgrade-neutral-defaults-and-anti-affinity.md) D11 — why the Sentinel tier
  needed the fallback of D5
