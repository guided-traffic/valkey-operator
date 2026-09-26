# ADR 0015: One CRD, Validated by Schema Only — No Admission Webhook

## Status

Accepted. Date: 2026-08-21.

Implemented: the operator ships no webhook of its own. There is no
`ValidatingWebhookConfiguration` anywhere in the tree and nothing under `config/webhook` —
`config/` holds `crd`, `default`, `manager` and `rbac`, and no more. The one
`MutatingWebhookConfiguration` in this repository is an E2E fixture: `blockCoreResourceCreation`
([`test/e2e/admission_recovery_test.go`](../../test/e2e/admission_recovery_test.go)) installs a
fail-closed one at runtime to reproduce the incident and deletes it again.

Amended 2026-09-26 by
[ADR 0033](0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md), in the
unreleased `feat/rootless` release. **CEL is no longer untried here**: `SeccompProfileSpec`
carries two `XValidation` rules, the first CEL in this CRD, so D3's statement that
`grep -rn XValidation api/v1/` matches nothing is struck through in place and D2 lists CEL among
the validators. **The operator refuses one input at runtime**: a `Localhost` seccomp profile its
allow-list does not name is stored on the CR and never written into a workload (ADR 0033 D9),
which D5's "never a refusal" and D6's "an allowlist would require the webhook" are amended for.
The Consequence on the code a CR author chooses is corrected in place: it had still described
the token and the posture before ADR 0012 D8 step 4 and ADR 0032. No webhook was added; D1, D2's
"no admission webhook", D4 and D7 are unchanged. Verified by reading
[`valkey_types.go`](../../api/v1/valkey_types.go), the generated
[`vko.gtrfc.com_valkeys.yaml`](../../config/crd/bases/vko.gtrfc.com_valkeys.yaml) and
[`pod_hardening.go`](../../internal/controller/pod_hardening.go).

## Context

The operator needs an API surface and a validation story. Both choices were made against the
same backdrop: this project's defining incident was caused by somebody **else's** fail-closed
admission webhook losing its backend for 90 seconds, which stopped every matching `CREATE`
cluster-wide and cost ~7 minutes of total data-plane outage
([ADR 0001](0001-continue-reconciling-past-a-rejected-write.md)). Both figures were measured on
the cluster during the incident and are not reproducible from this repository. The only in-tree
record of either is prose: the header comment of
[`test/e2e/admission_recovery_test.go`](../../test/e2e/admission_recovery_test.go) and
[ADR 0001](0001-continue-reconciling-past-a-rejected-write.md) for the ~90 s window,
[ADR 0003](0003-nudge-a-short-of-pods-statefulset.md) for the ~7 min total.

A validating webhook of our own would put the same dependency in the admission path of every
`Valkey` write, and would add a serving certificate, a Service and an availability obligation
to the install footprint.

## Decision

**D1 — Exactly one CRD: `Valkey`, in API group `vko.gtrfc.com`.** Sentinel is not a separate
kind; it is enabled per cluster through `spec.sentinel.enabled` and configured under the same
block. **Any future HA component is modelled as a sub-block of `Valkey`, not as an additional
CRD.** One object owns the whole cluster lifecycle, so the operator never has to reconcile the
ordering and ownership of two independently-versioned CRs, there is exactly one place the
reconcile loop reads desired state from, and a change to either topology goes through the same
rolling-update machinery in one generation.

**D2 — No admission webhook.** Everything that validates a `Valkey` object is CRD schema
validation generated from the kubebuilder markers in
[`api/v1/valkey_types.go`](../../api/v1/valkey_types.go): enums
(`certManager.issuer.kind` ∈ {Issuer, ClusterIssuer}, `observer.logLevel`), defaults
(`auth.secretPasswordKey: password`, `podDisruptionBudget.enabled: false`,
`tls.enabled: false`), types and required fields. *(Added 2026-09-26, ADR 0033 D1:)* and CEL
rules (`x-kubernetes-validations`), so far two, both on `spec.podSecurity.seccompProfile`
(`SeccompProfileSpec`): `localhostProfile` is set and non-empty exactly when `type` is
`Localhost`, and it is never an absolute path nor contains a `..` element. Both are single-object
rules on one sub-block, none couples fields to `replicas` (D4).

**D3 — Cross-field constraints are documented at the field and enforced nowhere.** *(Since
2026-09-26 with one exception: the `type`/`localhostProfile` pair of `SeccompProfileSpec` is
enforced by CEL — end of this decision.)*
`spec.tls.secretName` and `spec.tls.certManager` are documented as mutually exclusive in
`TLSSpec` ([`api/v1/valkey_types.go`](../../api/v1/valkey_types.go)) and nothing rejects a CR
that sets both. Nothing resolves it either: the `TLS Certificates` reconcile step is gated on
`IsCertManagerEnabled` alone
([`internal/controller/valkey_controller.go`](../../internal/controller/valkey_controller.go)) —
`tls.enabled && tls.certManager != nil`, which never consults
`IsTLSSecretProvided` — and `BuildValkeyCertificate` issues a Certificate whose
`spec.secretName` is `ValkeyTLSSecretName`, which returns the user's `spec.tls.secretName`
whenever it is set ([`internal/builder/certificate.go`](../../internal/builder/certificate.go)).
Both fields are honoured at once: the StatefulSet mounts that Secret and the operator points a
cert-manager Certificate at the same name, so cert-manager takes the user's own Secret over.
There is no skip, no Warning Event and no error path for the conflict. Verified in this
repository: the Certificate the operator creates names the user's Secret. That cert-manager then
overwrites the contents of a Secret it is pointed at is upstream behaviour, not reproduced
against a cluster here.
**Every future cross-field constraint must be enforced in the reconciler (or via CEL in the
CRD schema), never assumed at admission.** ~~The two halves of that sentence do not have equal
standing: `grep -rn XValidation api/v1/` matches nothing and the generated CRDs carry no
`x-kubernetes-validations` rule, so CEL is untried here, and D4 rejects it for the only
cross-field constraint that has come up so far. Reconciler enforcement is the half with a
precedent.~~ *(Superseded 2026-09-26 by
[ADR 0033](0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md) D1:)*
both halves now have a precedent. `SeccompProfileSpec` carries two `XValidation` markers, and
both generated CRDs (`config/crd/bases/vko.gtrfc.com_valkeys.yaml` and the chart's `crd.yaml`)
carry them as one `x-kubernetes-validations` block — the first a cross-field rule between `type`
and `localhostProfile`, the second a path rule;
`TestPodSecurity_CRDValidatesTheSeccompProfile_Integration` (envtest, Kubernetes 1.29) asserts
that the API server refuses each case — its rows for the first rule ran green on 2026-09-26, a
run of the path rows is not recorded. D4 still rejects CEL for replica-coupled
constraints; nothing about that argument changed.

**D4 — Replica-coupled constraints are deliberately *not* expressed as CEL.** A rule such as
`replicas > 1 || !podDisruptionBudget.enabled`, or rejecting `maxUnavailable >= replicas`,
would reject an otherwise legitimate edit — scaling an existing CR with an enabled budget down
to 1, or a plain scale-down past `maxUnavailable` — and force the user into **two ordered
edits**, with the intermediate state as the only legal path. That converts a scale-down into a
multi-step migration and can wedge GitOps tooling that applies one manifest atomically. The
operator honours the spec and answers at runtime instead: skip the object, or create it and
record a Warning Event plus a log line
([ADR 0004](0004-opt-in-poddisruptionbudgets.md) D5, D8).

**D5 — The operator owns data-plane validation and never *rejects* input** *(it refuses one at
runtime since 2026-09-26 — the amendment at the end of this decision)*. Its blocking
checks are about reachability, replication role and sync state. It does judge input where it can
act on the answer — `maxUnavailable >= replicas` raises a `PodDisruptionBudgetTooPermissive`
Warning plus a log line on every applicable pass (`warnIfDataBudgetProtectsNothing`,
[`internal/controller/pdb.go`](../../internal/controller/pdb.go), documented at the field in
[`api/v1/valkey_types.go`](../../api/v1/valkey_types.go)) — ~~but the answer is always a Warning,
never a refusal~~ and there the answer is a Warning, not a refusal. Schema and cluster policy
engines own the rejecting half of input validation; the operator owns convergence of the
running topology. *(Amended 2026-09-26 by
[ADR 0033](0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md) D9:
one refusal exists.)* A `Localhost` seccomp profile that `--allowed-seccomp-localhost-profiles`
does not list (default empty, so every one) is refused at runtime, not at admission: the CR is
stored, `reconcileStatefulSet` returns `errSeccompProfileNotAllowed` before writing anything, the
Sentinel and observer steps withhold their writes, and the CR reports
`ReconcileBlocked=True/SeccompProfileNotAllowed` with phase `Error` while the running pods keep
their template. It is a refusal to act on an accepted object, reported the ADR 0002 way, and it
exists because a profile that allows every syscall is as good as no filter while Pod Security
`restricted` accepts every `Localhost` profile.

**D6 — Image choice and generated-name choice are delegated to the CR author; the control is
Kubernetes RBAC on `create valkeys`.** `spec.image` and `spec.metrics.image` are arbitrary
strings with no registry allowlist, and the CR name drives every generated object name.
~~Enforcing an allowlist would require the webhook D2 rejects, and cluster policy tools already
do it better.~~ *(Amended 2026-09-26:)* the first half of that reason no longer holds —
[ADR 0033](0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md) D9
enforces an allow-list without a webhook, in the reconciler (D5). The image decision itself is
unchanged: there is still no registry allowlist, it rests on cluster policy tools doing it better,
and an image allowlist enforced the D9 way was not weighed.

**D7 — `failurePolicy: Fail` on a third-party admission webhook is correct and must stay.**
The remedy for the incident is **HA for the webhook backend**, never weakening the policy to
`Ignore`. An `Ignore` webhook silently stops enforcing policy exactly when its backend is
down — the enforcement gap is invisible and unbounded, whereas a fail-closed rejection is loud
and self-healing. **The operator-side work shortens the outage and reduces the blast radius;
it does not remove the cause and must not be mistaken for a reason to relax the policy.**

## Consequences

* **The `Valkey` spec grows monolithically.** Every feature — TLS, metrics, PDB,
  anti-affinity, persistence, networkPolicy — adds a sub-block to the same type, and Sentinel
  cannot be lifecycled independently of the data StatefulSet.
* **A user can write a CR the operator considers self-contradictory and get no feedback at
  admission time.** The behaviour is whatever the reconcile steps do with every field applied;
  for the one documented mutually exclusive pair that means both paths run against the same
  Secret name.
* Semantically invalid CRs — an unreachable image, a nonsensical replica count — surface as
  **data-plane failures rather than validation errors**. Both TLS sources set does not even do
  that: the cluster converges on cert-manager's material and the overwrite is silent.
* Invalid-but-harmless specs reach the cluster and are answered with a Warning rather than a
  rejection, so a user who ignores Events can run a budget that permits evicting every data
  pod at once.
* **A CR author chooses the code the cluster runs**, ~~under the namespace's default (permissive)
  security posture, with a ServiceAccount token mounted into it~~
  ([ADR 0013](0013-operator-is-cluster-wide-privileged.md)). *(Corrected 2026-09-26: since
  [ADR 0012](0012-the-sidecar-records-its-drain-promotion-on-the-pod.md) D8 step 4 (2026-08-27)
  the containers running `spec.image` and `spec.metrics.image` mount no ServiceAccount token —
  the data pod hands it to the sidecar alone — and since
  [ADR 0032](0032-generated-pods-run-rootless.md) D1 that code runs rootless under a fixed
  posture whatever the namespace allows — with one exception: `spec.image` also runs the
  migration-only `fix-data-ownership` repair as uid 0 with `CAP_CHOWN` alone, in the persistent
  data pods a migration creates, until the second roll replaces them (ADR 0032 D2) — with a
  seccomp profile of the CR author's choosing only
  among `RuntimeDefault` and the `Localhost` profiles the operator's allow-list names, ADR 0033
  D9.)*
* **Generated names collide with pre-existing objects by design.** One destructive path was
  closed with provenance discipline; the general property remains, which is why every new
  delete-by-generated-name needs the same treatment
  ([ADR 0006](0006-delete-only-what-the-operator-owns.md)).
* Because no webhook of our own exists, **a rejected CR write from somebody else's webhook is
  modelled as a first-class runtime state** rather than an error path
  ([ADR 0002](0002-surface-a-blocked-reconcile-on-the-cr.md)).

## Alternatives Considered

### A separate `ValkeySentinel` CRD

Rejected: two independently-versioned CRs whose ordering and ownership the operator would have
to reconcile, and two generations for one cluster's desired state.

### A validating webhook enforcing cross-field rules and a registry allowlist

Deliberately not built. It adds a fail-closed dependency of exactly the kind that caused the
incident this whole family of decisions is about, plus a serving certificate, a Service and an
availability obligation.

### CEL rules for replica-coupled constraints

Rejected — see D4: they block legal edits and force ordered multi-step migrations.

### Silently doing nothing about a too-permissive budget

Rejected: the budget then protects nothing, with no signal at all.

### Reconciler-side input validation with a status condition per invalid field

Not built. It would be the natural next step if D3's runtime resolution proves too opaque.
*(2026-09-26: one field is now validated in the reconciler — the `Localhost` seccomp profile
against the operator's allow-list, ADR 0033 D9 — and it reports through the existing
`ReconcileBlocked` condition with its own reason, not through a condition of its own.)*

### Switch the third-party webhook to `failurePolicy: Ignore`

Explicitly rejected: it trades a visible availability event for a silent security-control gap.

## Residual risks

* **The webhook-HA remedy is cross-repo work outside this repository (open).** Until the
  policy engine runs HA with its own PDB, a single node drain can still remove the admission
  backend cluster-wide — 15 Flux Kustomizations failed reconciliation in the same window as
  the original incident (counted on the cluster during the incident, not reproducible from this
  repository).
* **No registry allowlist (open).** Whoever may `create valkeys` in a namespace chooses the
  image that runs there.
* **No reconciler-side resolution for the one documented mutually exclusive pair (open).**
  Setting `spec.tls.secretName` together with `spec.tls.certManager` hands the user's Secret to
  cert-manager with no Event, no status condition and no log line.

## References

* [`api/v1/valkey_types.go`](../../api/v1/valkey_types.go) — the whole API surface and every kubebuilder validation marker, the two `XValidation` rules on `SeccompProfileSpec` included
* [`internal/controller/pod_hardening.go`](../../internal/controller/pod_hardening.go) — `seccompProfileAllowed`, the one runtime refusal of input (D5 amendment)
* [ADR 0033](0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md) — the first CEL rules (D1) and the seccomp allow-list (D9)
* [`internal/builder/certificate.go`](../../internal/builder/certificate.go) — `ValkeyTLSSecretName`, the reason both TLS sources land on one Secret name (D3)
* [`internal/controller/valkey_controller.go`](../../internal/controller/valkey_controller.go) — the `TLS Certificates` step and its `IsCertManagerEnabled` gate (D3)
* [`internal/controller/pdb.go`](../../internal/controller/pdb.go) — the runtime-warning pattern that replaces CEL
* [ADR 0002](0002-surface-a-blocked-reconcile-on-the-cr.md) — how a third-party rejection is reported
* [ADR 0004](0004-opt-in-poddisruptionbudgets.md) — the concrete constraint that was not encoded as CEL
* [ADR 0005](0005-upgrade-neutral-defaults-and-anti-affinity.md) — what the schema defaults must guarantee
* [ADR 0006](0006-delete-only-what-the-operator-owns.md) — the discipline that generated names require
* [ADR 0013](0013-operator-is-cluster-wide-privileged.md) — why RBAC on `create valkeys` is the real control
