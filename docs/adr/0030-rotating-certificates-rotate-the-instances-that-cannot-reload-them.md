# ADR 0030: Rotating certificates rotate the instances that cannot reload them

## Status

Accepted. Date: 2026-08-26.

Implemented in `0aaf79d`, ahead of this document: the ADR was **deliberately deferred**
(`ADR spaeter, erst Code`) with the debt written down in
[`CLAUDE.md`](../../CLAUDE.md) rather than silently carried, and this file pays it. That
sequence is a deviation from the rule in [`README.md`](README.md) that an ADR is updated in
the same change as the behaviour it describes, and it is recorded here rather than tidied
away.

Implemented: [`internal/tlsmaterial`](../../internal/tlsmaterial/reloader.go) and its three
sidecar consumers plus the observer; the material fingerprint on both StatefulSet pod
templates; the `TLSMaterialStale` condition and the `ValkeyTLSMaterialStale` alert; the Secret
watch predicate.

Amended 2026-08-27: **D4's carrier moved out of pod metadata.** The fingerprint was a pod
annotation, and pod metadata is patchable by anything holding the sidecar token — so deleting
the key made the pod unmeasured and switched the roll off. It is now the
`VKO_TLS_MATERIAL_HASH` env var of the tier's carrier container
([ADR 0031](0031-a-record-the-operator-trusts-lives-in-pod-spec.md)), which the API server
refuses to change. The digest, the trigger, the presence rule and the rolling update are
unchanged. The T25 residual below is closed with the two corrections it earned.

Also amended 2026-08-27: **the hostile Secret writer is accepted rather than left open.** D11
now records the decision and the reason — a strong digest closes the collision and leaves a
substitution indistinguishable from a legitimate rotation — and corrects the framing of the
password brake, which five documents phrased in terms of digest width when the parameter is the
entropy of the input.

Also amended 2026-08-27: certificate rotation gained end-to-end coverage, which it had none of
when this ADR was written — `TestE2E_TLS_CertificateRotation_RollsTheFleet`
([`test/e2e/tls_rotation_test.go`](../../test/e2e/tls_rotation_test.go)) deletes the Secret so
cert-manager reissues, then asserts the template moves, every data pod is replaced, the
dataset survives and the condition clears.

Also amended 2026-08-27 (third pass): **the unmeasured population is reported.** D9 gained
the `TLSMaterialUnmeasured` reason — no measured pod stale, some pods record-less — closing
the silence half of the T24(b) residual while keeping the exemption itself. This is the
surviving, retroactive half of what the T27 analysis called option C.

Also amended 2026-08-27 (second pass): **D12 is new — the operator never persists a TLS pod
template without a material record.** The e2e above immediately measured what shipping without
that rule costs: the pods of a freshly created TLS cluster carried no fingerprint and were
therefore exempt from every rotation forever (T27). D8's "an unreadable Secret leaves the
record off the template" is superseded in place by D12's three cases; the D9 defect paragraph
(per-pass, not per-tier measurability) and the no-writer-once-disabled gap are fixed in the
same change, and four residual risks below are closed with their strikes.

Not implemented, deliberately: nothing parses `notAfter`, nothing schedules, and the operator
never issues a certificate. `valkey-server` and `valkey-sentinel` are still **unmeasured** and
covered by D6 rather than by verification.

Amends [ADR 0016](0016-authentication-and-tls-posture.md) D12 and its cert-manager residual
risk, [ADR 0012](0012-the-sidecar-records-its-drain-promotion-on-the-pod.md) D10, and
[SECURITY_ARCHITECTURE.md](../../SECURITY_ARCHITECTURE.md) sections 2, 6 and 9.

## Context

Two facts hold at once and neither is a defect on its own.

A Kubernetes Secret volume is **rewritten in place** when cert-manager rotates the certificate
it holds: the pod keeps the same mount and the bytes underneath change, which kubelet performs
by swapping the `..data` symlink. And `crypto/tls` copies **nothing** back from the filesystem:
a `*tls.Config` built once holds a parsed `tls.Certificate` and a parsed `x509.CertPool`
forever. A long-lived process that built its config at startup therefore keeps presenting the
certificate it parsed then, indefinitely, while valid material sits in its own mount.

**Measured on a live fleet on 2026-08-26** (`gitlab/gitlab-valkey`, wds18-k8s-main), on all
three data pods, once per second, continuously:

```
ERROR sidecar.labeler failed to detect role
  {"error": "info replication localhost:16379: AUTH failed on localhost:16379:
             remote error: tls: expired certificate"}
```

5781 occurrences on pod-1 inside the retained log buffer alone. The certificate on disk was
valid — Secret at revision 4, rotated 2026-08-23, `notAfter` in November, cert-manager
`Ready=True`. The sidecar processes started 2026-06-12 and 2026-08-21, all **before** the
rotation. `remote error:` means the peer sent the alert: `valkey-server` was rejecting the
**client** certificate the sidecar presented.

What died with it was not cosmetic. Three things run on that client:

| Consumer | What stops |
|---|---|
| the `instanceRole` labeler | `-rw` and `-r` stop following a failover |
| the Sentinel cross-check | the split-brain defence in depth |
| the **ADR 0012 drain promotion** | the failover a draining master performs itself |

The third is the one that costs a dataset. [ADR 0012](0012-the-sidecar-records-its-drain-promotion-on-the-pod.md)
D10 already establishes that the drain fails **open** — no promotion, no stamp, no Event, one
log line in a container about to be deleted — and it establishes that for a Valkey that exited
first. A `*tls.Config` parsed at process start is a **second, independent way the same premise
fails**, with the same silence.

Three things made it invisible:

* **The reconciler and the health checker were never affected**, and that is why nothing on the
  CR moved. Both read the Secret per call and present **no client certificate at all** — only
  `ca.crt` reaches their config ([`valkey_controller.go`](../../internal/controller/valkey_controller.go),
  [`internal/health/checker.go`](../../internal/health/checker.go)). `Ready` stayed `True`,
  `phase` stayed `OK`, and every status field was correct about a cluster whose sidecars had
  been blind for days.
* **The Secret watch matched auth Secrets only.** A TLS rotation enqueued **nothing** — not a
  degraded reconcile, no reconcile.
* The failure is not at renewal. It is at **expiry of the pinned certificate**, up to a month
  later, on pods nobody touched in between.

The question this ADR answers is therefore not "how do we reload TLS material". It is: **for
which processes is reloading our job at all, and what happens to the rest.**

## Decision

**D1 — A long-lived process this repository owns re-reads its TLS material and earns an
exemption from restart. Every other process is replaced.** That split is the rule, and it is
the whole design. Reloading is how a process *earns* an exemption; the roll is the fallback for
everything that cannot, will not, or has not been shown to.

**D2 — The reload is per dial, compares the bytes of the CA *and* the keypair, and keeps the
last config that worked.** [`tlsmaterial.Reloader.Config()`](../../internal/tlsmaterial/reloader.go)
re-reads all three files on every call and rebuilds only when their contents differ.

* **Bytes, not modification time.** The mount is a Secret volume without `subPath`, updated by
  swapping the `..data` symlink, so a modtime comparison would have to resolve the link.
  Comparing bytes has no such subtlety.
* **Both halves.** A CA-only reloader would have missed exactly the failure that was measured:
  the client certificate is what the peer rejected. The keypair half is live for the **sidecar**,
  which always presents a client certificate; the observer passes an empty cert and key unless
  `mutualTLS` was opted into, so by default its `Reloader` tracks the CA alone
  ([`internal/observer/observer.go`](../../internal/observer/observer.go), `newTLSReloader`).
* **Last-good on failure.** kubelet writes the new contents by swapping a symlink, so a caller
  can observe `tls.crt` and `tls.key` from either side of a rotation and get a mismatched pair
  for one call. Returning the last working config makes that cost one degraded call; returning
  an error would make it an outage. Material that is merely stale still works until it expires.
* **The commit is atomic.** The new bytes and the new config are stored only after the pair
  parsed, so a half-written mount never replaces a working config, and the failure is retried
  on the next call rather than latched.
* **The log is an edge.** A `degraded` flag makes it one Error entering the state and one Info
  leaving it, not one line per dial. Per `Reloader`, not per process: `sidecarTLSReloader`
  returns a fresh one to each of its callers, so a sidecar whose mount goes unreadable logs the
  transition three or four times. Harmless, and worth knowing before reading the logs as a
  count.

**D3 — A new long-lived client of ours inherits D1's first half, not an exemption.**
`valkeyclient.Client` holds no connection, so constructing one per command is an allocation and
nothing else; there is no pooled-connection argument for pinning a config. A future process of
ours that dials Valkey takes its `*tls.Config` from the `Reloader` per dial. A process that
cannot is a process that has to say why, in an amendment to this ADR.

**D4 — Everything else rides the ordinary failover-aware rolling update, driven by a
fingerprint of the Secret's *content* on the pod template.** The fingerprint
([`internal/builder/tls_material.go`](../../internal/builder/tls_material.go)) is a
length-prefixed FNV-1a digest of `ca.crt`, `tls.crt` and `tls.key`, stamped by the reconciler —
not by a builder, because a builder never sees Secret content
([`internal/controller/tls_material.go`](../../internal/controller/tls_material.go)). A changed
fingerprint is pod-template drift like any other and is replaced by the rolling update of
[ADR 0007](0007-failover-aware-rolling-update.md), with its failover, its sync waits and its
bounds. **No new replacement mechanism was introduced, and none may be.**

> **Amended 2026-08-27 — the carrier changed, the mechanism did not.** This decision
> originally named the ~~`vko.gtrfc.com/tls-material-hash` pod annotation~~ as the carrier.
> Pod metadata is patchable by anything holding `pods: patch` on the pod, and because both
> consumers carry a presence rule, *deleting* the key was cheaper than forging it: the pod
> becomes unmeasured, the roll skips it and the condition stays quiet. The fingerprint now
> lives in the `VKO_TLS_MATERIAL_HASH` env var of the tier's carrier container, which the API
> server refuses to change after creation
> ([ADR 0031](0031-a-record-the-operator-trusts-lives-in-pod-spec.md)). The digest, the
> stamping site, the presence rule and the rolling update are unchanged. The annotation is
> read as a fallback for pods written before that date and is never written again.

**D5 — The restart unit is the pod, so one non-reloading container spends the whole pod's
exemption.** A TLS data pod holds `valkey-server`, our sidecar and, with metrics enabled, the
third-party exporter. The sidecar reloads, and the exporter cannot earn an exemption whatever
it does — it is third-party, so nothing in this repository can make it reload or keep it
reloading, which is the argument, not a measurement of its behaviour. The pod is replaced
regardless. That is why **both** StatefulSets carry the fingerprint and the **observer
Deployment carries none**: its pod template declares exactly one container
([`internal/builder/observer.go`](../../internal/builder/observer.go)), that container is ours,
and it reloads. (It builds a second `Reloader` when Sentinel is enabled, and the two differ
only in whether they present a client certificate — both read the **same** CA from the **same**
mount, because the observer has exactly one TLS Secret in every mode.)

**D6 — A process whose reload behaviour has not been measured is treated as pinning.**
`valkey-server` and `valkey-sentinel` have never been measured in this repository, and this
decision does not measure them. Treating "unknown" as "pins" costs a roll that may be
unnecessary; treating it as "reloads" costs the failure this ADR exists for, silently. The
asymmetry is not close. **Measuring it is a way to remove a roll, never a prerequisite for
having one.**

**D7 — The trigger is the rotation, not the expiry.** Nothing parses `notAfter`, and nothing
needs to: the rotation is an event the operator already sees.

**How much slack that buys is the issuer's answer, not this operator's.** The `Certificate`
rendered here sets neither `duration` nor `renewBefore`
([`internal/builder/certificate.go`](../../internal/builder/certificate.go) — verified, zero
occurrences of either), so the issuer decides both. At cert-manager's own defaults that is a
90-day certificate renewed 30 days before expiry — a rotation every 60 days, with a month
during which the previous certificate is still valid. An ACME or Vault issuer caps or ignores a
requested duration and gives a different, often much shorter, overlap.

**The measured fleet is evidence that the default does not always hold.** A sidecar that
started 2026-08-21 was presenting an expired certificate by 2026-08-26, five days later, on a
Secret rotated 2026-08-23 — which a 30-day overlap does not permit. The overlap on that cluster
was days, not a month, and how many was not determined. **Not verified:** the issuer
configuration behind it.

The rule survives that, because it never depended on the number. Starting at the rotation is
the earliest an operator could act at all, so no schedule can do better, and no stampede
control is needed for a roll nothing is racing — the concurrency cap of
[ADR 0019](0019-reconcile-concurrency-and-the-cost-of-a-stuck-pass.md) is the only pacing there
is. **What the shortened overlap does change is the margin, not the mechanism:** the 72 h the
shipped alert waits is comfortable against a 30-day overlap and merely adequate against a
five-day one. If an issuer with a short overlap is in use, that threshold is the number to
lower, not this decision.

**D8 — Upgrade neutrality is the presence guard the other hashes already use.** A pod without
the record is never restarted for one, and contributes no staleness. The operator upgrade
that introduces this mechanism therefore rolls nothing and lights up nothing
([ADR 0005](0005-upgrade-neutral-defaults-and-anti-affinity.md)). ~~A Secret that is absent or
unreadable leaves the annotation off the template rather than stamping an empty value; the
stamp never fails a reconcile step.~~ **Superseded 2026-08-27 by D12:** leaving the record off
the template is exactly what made every pod built from that template permanently exempt (T27),
and one blind pass stripped the record off a live template and produced the same population
through a different door (T24(c)). The stamp still never fails a step — a template that cannot
be armed is refused without an error, not persisted bare.

**The two tiers do not pay the same price for that guard, and the difference is not one
upgrade.** A data pod carries a container of ours, so the next operator upgrade rolls it and it
gains the annotation. **A Sentinel pod carries none** — its containers run `spec.image`
([`internal/builder/sentinel.go`](../../internal/builder/sentinel.go)) — and its StatefulSet is
`OnDelete`, so no operator upgrade replaces it. A pre-upgrade Sentinel pod therefore stays
un-annotated, and stays exempt from the rotation roll, until something else replaces it: a
Valkey image bump, a Sentinel config change, a resource edit. That window is bounded by the
user's next change, not by ours. See the residual risks.

**D9 — The one thing the grace window does not cover is a roll that never starts, and that is
reported rather than assumed.** `TLSMaterialStale` is a level, re-measured on every pass by
comparing each pod's recorded fingerprint against the Secret it mounts. It is a **resource**
step and not a workload one on purpose: every arm of `reconcileWorkload` returns early while a
rolling update is in flight, which is precisely when this condition is True
([ADR 0027](0027-conditions-are-levels-edges-or-history.md)). A read it could not complete is
*not measured*, never *everything is current* — overwriting a `True` on the strength of a
failed `Get` would clear the one signal an operator is meant to act on.

**Since 2026-08-27 that rule holds per tier, not only per pass.** Measurability is AND-ed
across the two tiers, one way: stale pods in a readable tier are reported even while the other
tier cannot be measured, but the affirmative "every pod runs the TLS material currently in its
Secret" is only written over a complete measurement (T24(a); it used to be OR-ed, an all-clear
covering a tier nothing inspected). The evaluator also owns the disabled case itself instead of
sitting behind a `when: IsTLSEnabled` step gate: a standing `True` carried into `tls.enabled:
false` is retracted with reason `TLSMaterialNotApplicable`, presence-guarded in both directions
(T24(d) — the gate had silenced the condition's only writer, freezing the `True` with the alert
firing on it for the life of the CR). And the record-less legacy pod is **named, not
absorbed** (T24(b), closed later the same day): when no measured pod is stale but some pods
record no fingerprint, the condition stays `False` — unmeasured is not stale, per D8, and the
shipped alert matches `True` only — with reason `TLSMaterialUnmeasured` and a message naming
the pods a rotation will never replace. Precedence is fixed: stale outranks unmeasured
outranks current, so an in-flight roll is never demoted to an unmeasured report. The reason
flip on existing clusters that carry legacy pods is the one deliberate
[ADR 0005](0005-upgrade-neutral-defaults-and-anti-affinity.md) D10 exception of that fix —
status-neutral, alert-neutral, reason-visible — decided explicitly rather than slipped in.

**D10 — The Secret watch matches every Secret the fingerprint reads.** Both tiers, the unified
certificate and a user-provided Secret name. Before `0aaf79d` the predicate matched auth
Secrets only, so the mechanism above would have had no event to run on.

**D11 — Publishing a fingerprint of Secret content is a deliberate exception, bounded to TLS
material.** [ADR 0016](0016-authentication-and-tls-posture.md) D12 states that Secret *names*
propagate and Secret *values* do not, and gives the reason: hashing the name keeps secret
material out of the pod template and out of every hash the operator publishes. D4 breaks that
for TLS, knowingly. It is acceptable **here** because the digested material is a private key —
high-entropy, not guessable, so a published digest of it confirms nothing an attacker does not
already have.

**The security parameter is the entropy of the input, not the width of the digest.** That
sentence is a correction, made 2026-08-27: this decision and four other documents used to
phrase the refusal as *"a 32-bit digest of a low-entropy secret is a brute-forceable oracle"*,
which reads as though a wider or cryptographic digest would make the password case safe. It
would not. An attacker who holds a published digest of a password guesses candidates and hashes
them, and SHA-256 obliges as readily as FNV-1a — faster hardware, same game. What makes the TLS
case safe is that nobody can enumerate 2048-bit RSA keys. **The password rotation gap therefore
stays open and must not be closed by copying this mechanism at any digest strength.**

**And it is a change detector, not an integrity control.** FNV-1a is non-cryptographic, the
digest is 32 bits, and `tls.key` is hashed last — trailing bytes after a PEM block are ignored
by every parser, so a chosen digest is reachable by search. Whoever can write the TLS Secret can
swap the material and keep the fingerprint. That principal can replace the cluster's TLS
identity anyway, so what this buys is evasion of the *report*, not new access; the rule that
follows is that neither `TLSMaterialStale` nor the alert may ever be presented as evidence that
the material is unchanged. They answer "did the roll happen", nothing more.

**Amended 2026-08-27: that is accepted permanently, and the reason is worth writing down so the
question stops coming back.** A wide cryptographic digest *would* close the substitution — the
collision is what makes the swap silent, and 128 bits of SHA-256 is not searchable. It was
still not taken, because of what it buys: the attacker loses the ability to swap **silently**,
and gains a swap that is **indistinguishable from a legitimate rotation**. Both produce one
fleet roll and one `TLSMaterialStale` transition. There is no observer for whom those two
differ, so the detection has no consumer.

Two arguments that have been made against the strong digest and do **not** hold, recorded so
they are not reused: that writing the previous Secret content back defeats every hash — it
does, but that is a *denial of rotation*, in which the fingerprint is telling the truth about a
mount that really was reverted, and no content digest can ever detect it (that needs `notAfter`,
which D7 refuses); and that the migration is prohibitive — changing the digest changes every
recorded value, which would roll the whole fleet including the Sentinel tier, but versioning the
record and treating a previous-generation value as *unmeasured* rather than stale is the same
presence-rule widening [ADR 0031](0031-a-record-the-operator-trusts-lives-in-pod-spec.md) D5
already ships.

What would actually raise the ceiling is not a better digest but a **trust anchor outside the
Secret** — comparing the served leaf against the issuer this operator asked for, rather than
against the bytes the Secret happens to hold. Nothing here does that, and no decision has been
taken to.

**D12 — The operator never persists a TLS pod template without a material record.** Added
2026-08-27, superseding the D8 rule that an unreadable Secret leaves the record off the
template. The reason is T27, measured on a cluster: the Certificate and the StatefulSet are
created in the same pass, cert-manager has not issued when the template is first written, and
the StatefulSet controller builds the pods from that bare template — after which the D8
presence guard exempts them from every rotation permanently, because it cannot tell "never had
a record" from "predates the mechanism" and must keep exempting the second. The same bare
template reached pods through two more doors: `spec.tls.enabled: false → true` on a serving
cluster, and any pass that could not read the Secret, which overwrote the live template
without the record so that every pod recreated in the window came back unmeasurable (T24(c)).

`ensureTLSMaterialRecord` ([`internal/controller/tls_material.go`](../../internal/controller/tls_material.go))
enforces the invariant at both StatefulSet reconcilers, both tiers, with three cases over what
is known rather than over lifecycle phase:

1. **The Secret is readable → its fingerprint is stamped.** The only case that existed before
   this decision, byte-identical to it.
2. **The Secret is unreadable and the persisted template carries a record → that record is
   inherited.** An unreadable Secret never erases a record. The persisted value is read only
   after the ownership proof, because provenance of the object is not provenance of the field
   ([ADR 0020](0020-write-only-what-the-operator-owns.md) D10), and the legacy annotation
   carrier counts as a record — a pre-[ADR 0031](0031-a-record-the-operator-trusts-lives-in-pod-spec.md)
   template keeps its fingerprint through a blind pass the same way.
3. **Neither is known → the write is refused.** Not an error and not a failed pass: the step
   returns clean, asks for a bounded re-entry, and D10's Secret watch re-triggers the pass the
   moment the Secret appears — so on the ordinary create path the StatefulSet materialises
   seconds after the CR, armed from birth. The refusal never touches a live template: on the
   update path it leaves the persisted one standing, on the create path there is nothing to
   leave.

Two consequences worth naming. A fresh TLS cluster is now created *after* its material exists
instead of alongside it — no pod ever blocks on a missing Secret volume, and there is no
arming roll: the alternative design (stamping an explicit `unarmed` sentinel and rolling the
pods once the real digest arrives) was rejected because it converts every cluster creation
into an immediate two-tier roll and every TLS enablement into a double replacement. And a TLS
Secret that **never** appears now parks the tier silently — create path: no StatefulSet, phase
`Provisioning`/"Waiting for StatefulSet creation"; update path (a `tls.enabled` flip whose
Secret never comes): the flip simply does not converge while the cluster keeps serving its old
configuration, and only the operator log names the wait. That silence is a known cost,
recorded in the residual risks; the pre-D12 behaviour for the same misconfiguration was a pod
wedged in `ContainerCreating` on a missing volume, louder and strictly worse for a serving
cluster.

## Consequences

* **Every TLS cluster rolls once per certificate rotation** — every 60 days at cert-manager
  defaults, which is what this operator leaves in place (D7); a user-provided Secret rotates on
  whatever schedule its owner uses. That is a real, recurring cost that clusters without TLS do
  not pay, and it buys a sidecar that can still talk to its Valkey.
* **A single-replica TLS cluster without persistence loses its dataset on that roll**, because
  the roll is a restart of the only pod. Nothing new was introduced — a config-hash or pod-spec
  change already deletes the only pod — but the set of things that trigger it grew by one, and
  this one arrives on a schedule nobody set.
* **The operator now reads `tls.crt` and `tls.key`**, not just `ca.crt`, and never writes a
  private key. It does not follow that it holds one only briefly: the manager cache backs an
  unfiltered Secret informer, so every watched Secret is resident for the process lifetime, with
  or without this change. What the change adds is one more consumer to satisfy before the
  `secrets` scope on the hardening checklist can be narrowed.
* **A 4-byte digest derived from a private key is readable by anyone with `get pods` or
  `get statefulsets`.** See D11 and the residual risks.
* **`TLSMaterialStale` adds a `vko_valkey_status_condition` series per TLS cluster** and one
  alert rule. Both are inert on non-TLS clusters — the reporting step carries
  `when: IsTLSEnabled`.
* **Two mechanisms now have to stay in step.** A container added to either StatefulSet must be
  classified: it reloads (and the pod keeps its exemption only if *every* container does) or it
  pins. Getting that wrong is silent in the reloading direction, which is the direction this
  ADR is about.

## Alternatives Considered

### Reload everything — make every process re-read, including `valkey-server`

Not ours to decide. `valkey-server` and `valkey-sentinel` are upstream binaries and the
exporter is third-party; whether they reload is a property of software this repository does not
write. The reload half is available to processes we own and to nobody else, which is exactly
what D1 says.

### Roll everything — drop the reloader and replace every pod, including the observer

Simpler by one package, and rejected. The observer is alone in its Deployment and it is ours,
so a roll there is a restart for nothing. More importantly, the drain promotion of ADR 0012
runs **inside a pod that is already terminating**: rolling the pod cannot repair the client
that pod needs at that exact moment. The reloader is the only answer for the one caller whose
failure costs a dataset.

### Watch the file with fsnotify instead of re-reading per dial

Rejected as more machinery for less. The consumers dial at one and two second cadences, the
files are three small reads, and a watch on a Secret mount has to handle the symlink swap
itself — which is the same subtlety D2 avoids by comparing bytes. A pull-driven reload also has
no callback to lose on error.

### Parse `notAfter` and roll shortly before expiry

Rejected: it converts a mechanism with a month of slack into a scheduled operation with a
deadline, and it needs the certificate parsed by the operator to decide *when*. The rotation is
already an event the operator sees, and acting on it needs no clock.

### Hash the Secret's `resourceVersion` instead of its content

Attractive because no secret-derived value would reach the pod template. Rejected because
`resourceVersion` changes on writes that do not change the material — a relabel, an annotation,
a no-op apply from GitOps — and each of those would roll the fleet for nothing. The content
fingerprint rolls exactly when the material changed.

### Restart only the containers that pin, not the pod

Kubernetes has no such operation for a running pod. The restart unit is the pod, which is what
D5 records rather than wishes away.

### Ship our own exporter so the pod could keep its exemption

Recorded, not scheduled. The third-party exporter is the one container in a data pod that
provably pins, so replacing it would remove one reason to roll — but `valkey-server` remains
unmeasured (D6), so the pod would still roll. It only becomes worth doing after D6's
measurement, and in that order.

## Residual risks

* ~~**The trigger and its detector read the same attacker-writable field (T25, open).** The
  sidecar Role grants `pods: patch` on this cluster's data pods and those pods mount the token —
  only the observer sets `automountServiceAccountToken: false` — so `valkey-server` and the
  third-party exporter carry it. Patching the annotation to the desired value suppresses the
  roll and the report together. It is the third forgeable field of that grant, next to
  `instanceRole` and the drain stamp, and the enumeration in `SECURITY_ARCHITECTURE.md` did not
  have it until this ADR was reviewed.~~

  **(Closed 2026-08-27, in two moves, and two claims above were wrong.)** The trigger and the
  detector still read the same field — that part was never the defect. What was, is that the
  field was writable by the pod. It is not any more:
  [ADR 0012](0012-the-sidecar-records-its-drain-promotion-on-the-pod.md) D8 step 4 takes the
  token away from `valkey-server`, both init containers and the exporter, and
  [ADR 0031](0031-a-record-the-operator-trusts-lives-in-pod-spec.md) moves the record into pod
  `spec`, which the API server refuses to change for any principal — including a compromised
  sidecar, the one container step 4 cannot help. The corrections: **forging was never the
  cheap attack** — a merge patch setting the key to `null` makes the pod unmeasured under the
  presence rule, which no digest strength addresses — and it was **not the third** forgeable
  field of that grant but one of nine. `SECURITY_ARCHITECTURE.md` section 3 now
  enumerates them instead of counting.

  **The two rejected repairs are recorded where they can be found again** — a stronger or
  keyed digest, and an operator-held record compared against an unforgeable timestamp — under
  Alternatives Considered in [ADR 0031](0031-a-record-the-operator-trusts-lives-in-pod-spec.md),
  which is the decision they were alternatives to.

  **Accepted, not open: the Secret writer.** A principal that can write the Secret can replace
  the material and keep the fingerprint identical, by collision against a 32-bit digest.
  Decided 2026-08-27 to leave it: closing it would turn a silent substitution into one that
  looks exactly like a legitimate rotation, which no observer can act on. D11 carries the full
  reasoning and the two arguments against the strong digest that do **not** hold — one of them
  written into this file earlier the same day and corrected there.
* ~~**A freshly created TLS cluster is never rolled by a rotation (T27, open, measured
  2026-08-27).** The Certificate and the StatefulSet are created in the same pass, so
  cert-manager has not issued when the template is first written and the record is correctly
  omitted (D8) — but the StatefulSet controller creates the pods from *that* template. The
  presence guard then exempts them permanently.~~ **(Closed 2026-08-27 by D12.)** The
  StatefulSet is no longer created before its template can be armed, the same refusal covers
  the `tls.enabled` flip on a serving cluster, and case 2 stops a blind pass from stripping a
  live template. Verified per tier by unit tests, against a real API server and work queue by
  `TestTLSMaterialGate_TheStatefulSetWaitsForTheSecret_Integration`, and the rotation e2e now
  asserts the pods of a fresh cluster carry the record **without** the test arming them first.
  What D12 does not cover is retroactive: a record-less pod that exists today stays exempt
  until something replaces it — that is the row below.
* ~~**An unreadable Secret does more than skip the stamp (T24, open).** A desired template
  without the record is drift: the live template is overwritten without it, an in-flight
  rotation roll stops seeing work, and the same pass reports nothing because the tier is
  unmeasurable.~~ **(Closed 2026-08-27 by D12 case 2.)** The desired template inherits the
  persisted record on a pass that cannot read the Secret, so the record is no longer the only
  conditionally present template input.
* ~~**`TLSMaterialStale` has no writer once TLS is disabled (T24, open).** The step carries
  `when: IsTLSEnabled`, which keeps a non-TLS cluster from ever gaining the condition — and
  also means a cluster that had it `True` and then turns TLS off keeps it, with the shipped
  alert firing on it indefinitely.~~ **(Closed 2026-08-27.)** The step gate is gone; the
  evaluator retracts a standing `True` with `TLSMaterialNotApplicable` and touches nothing
  else — see D9.
* ~~**The all-clear message can cover a tier nobody looked at (T24, open).** `scanTLSMaterial`
  OR-s measurability across the two tiers.~~ **(Closed 2026-08-27.)** Measurability is AND-ed,
  one way — see D9.
* **A TLS Secret that never appears parks the tier silently (new with D12, accepted).** No
  StatefulSet on the create path, a non-converging flip on the update path, and nothing on the
  CR names TLS as the reason — the phase reads `Provisioning`/"Waiting for StatefulSet
  creation" or stays `OK`, and only the operator log carries the refusal. Reporting the wait as
  a condition is the surviving half of T27 option C and was deliberately not built here; the
  pre-D12 behaviour for the same misconfiguration was a pod wedged in `ContainerCreating`,
  which is not better, only louder.
* ~~**A pre-upgrade Sentinel pod is exempt until the user changes something (T24, open).** D8
  explains the mechanism; the consequence is that the Sentinel tier of an upgraded cluster can
  hold pinned material across arbitrarily many rotations while the CR reports `False`.~~
  **(Half-closed 2026-08-27: the silence is gone, the exemption stands.)** The exemption is
  deliberate and stays — rolling a pod for a record it never had is exactly the fleet-wide
  upgrade roll D8 exists to prevent — but the CR no longer reports an all-clear over it:
  reason `TLSMaterialUnmeasured` names the pods (see D9). What remains open is only that
  nothing *rolls* them; they arm on the next replacement, which for the data tier is the next
  operator upgrade and for the Sentinel tier the next user change.
* **`valkey-server` and `valkey-sentinel` are still unmeasured.** D6 makes that safe, not
  answered. If either reloads, every TLS cluster with metrics disabled is rolling for nothing.
  Nobody has run the experiment.
* **A mixed-CA state is adopted silently.** If `ca.crt` is new while the keypair is still old,
  both halves parse independently and the rebuilt config is committed — a new trust anchor with
  an old client certificate. This is harmless during a cert-manager rotation, where both share
  an issuer, and it is not refused. Only a mismatched *keypair* is rejected.
* **The reloader never checks expiry.** "The last config that worked" can be an expired
  certificate held indefinitely if the mount stays unreadable. The single degraded log line is
  the only signal, and it fires once. **Not verified:** whether that state is reachable outside
  a broken mount.
* **The reload is pull-driven.** A process that stops dialing never notices a rotation. Every
  current consumer dials on a timer, so this is latent — a future consumer that dials only on
  demand would inherit it.
* **The fingerprint is FNV-1a, non-cryptographic, and covers `tls.key`.** For a private key it
  confirms nothing useful (see D11). **Not verified:** whether this project considers a 4-byte
  confirmation oracle over high-entropy material acceptable at all — no prior decision addresses
  it, and D11 is the first. The danger is not this use; it is the next author extending the
  same helper to a low-entropy secret, **which no digest strength makes safe** — see the
  entropy correction in D11.
* **The overlap the slack argument rests on is the issuer's, and on the measured fleet it was
  days rather than the cert-manager default of 30 (D7).** Nothing in this repository sets or
  reads it. **Not verified:** what that fleet's issuer was configured with.
* **Nothing was verified against a cluster after the fix.** The failure was measured on a live
  fleet; the repair is verified by unit and integration tests only. The fleet still runs an
  operator without this mechanism.
* **The two halves can drift apart.** A container added to a StatefulSet that silently reloads
  buys no exemption (harmless, one wasted roll), and a container mistakenly believed to reload
  reopens the original failure (silent). Nothing in the code enforces the classification; the
  table in [`internal/controller/tls_material.go`](../../internal/controller/tls_material.go)
  is prose.
* **The single-replica dataset loss is accepted, not mitigated.** See Consequences.

## References

* [`internal/tlsmaterial/reloader.go`](../../internal/tlsmaterial/reloader.go) — `Reloader`, `New`, `Config`, the byte comparison and the last-good config
* [`internal/controller/tls_material.go`](../../internal/controller/tls_material.go) — `stampTLSMaterialHash`, `reportTLSMaterialStale`, `scanTLSMaterial`, and the reload/pins table
* [`internal/builder/tls_material.go`](../../internal/builder/tls_material.go) — `ComputeTLSMaterialHash` and the length-prefixed digest; `StampTLSMaterialHash` and `RecordedTLSMaterialHash`, the carrier of [ADR 0031](0031-a-record-the-operator-trusts-lives-in-pod-spec.md)
* [`test/e2e/tls_rotation_test.go`](../../test/e2e/tls_rotation_test.go) — the rotation, the roll and the dataset, on a live cluster
* [`internal/controller/rolling_update.go`](../../internal/controller/rolling_update.go) — `tlsMaterialHashFromSts`, `podTLSMaterialHashChanged`
* [`internal/sidecar/drain.go`](../../internal/sidecar/drain.go) — `realValkeyClientFactory`, which holds the material source rather than a parsed config
* [`internal/sidecar/labeler.go`](../../internal/sidecar/labeler.go) — `sidecarTLSReloader` and its three consumers
* [`deploy/helm/valkey-operator/templates/prometheusrule.yaml`](../../deploy/helm/valkey-operator/templates/prometheusrule.yaml) — `ValkeyTLSMaterialStale`, `for: 72h`
* [ADR 0016](0016-authentication-and-tls-posture.md) — the TLS posture this amends, and D12's name-not-value rule
* [ADR 0012](0012-the-sidecar-records-its-drain-promotion-on-the-pod.md) — the drain promotion this keeps alive, and D10's fail-open premise
* [ADR 0007](0007-failover-aware-rolling-update.md) — the replacement mechanism D4 reuses
* [ADR 0005](0005-upgrade-neutral-defaults-and-anti-affinity.md) — the presence guard of D8
* [ADR 0019](0019-reconcile-concurrency-and-the-cost-of-a-stuck-pass.md) — the only pacing the roll has
* [ADR 0027](0027-conditions-are-levels-edges-or-history.md) — why `TLSMaterialStale` is a resource step
* [SECURITY_ARCHITECTURE.md](../../SECURITY_ARCHITECTURE.md) — sections 2, 6 and the hardening checklist
