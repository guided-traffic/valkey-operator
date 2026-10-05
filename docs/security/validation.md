# Validation

What checks a `Valkey` resource before the operator acts on it: CRD schema validation with
two CEL rules at admission, and one refusal in the reconciler. The decision to validate by
schema only is [ADR 0015](../adr/0015-one-crd-validated-by-schema-only.md). What a CR author can still choose unchecked is
[isolation and tenancy](isolation-and-tenancy.md#what-does-not-hold).

## Schema validation, and no webhook

There is **no admission webhook** in this project — no `ValidatingWebhookConfiguration`,
no `MutatingWebhookConfiguration`, nothing under `config/webhook`. Everything that
validates a `Valkey` object is CRD schema validation generated from the kubebuilder
markers in [`api/v1/valkey_types.go`](../../api/v1/valkey_types.go): enums
(`certManager.issuer.kind` ∈ {Issuer, ClusterIssuer}, `observer.logLevel`,
`podSecurity.seccompProfile.type` ∈ {RuntimeDefault, Localhost}),
defaults (`auth.secretPasswordKey: password`, `podDisruptionBudget.enabled: false`,
`tls.enabled: false`, `podSecurity.seccompProfile.type: RuntimeDefault`,
`podSecurity.userNamespaces: false`), types and required fields — and, since 2026-09-26, ~~one
CEL rule~~ two CEL rules, both on `SeccompProfileSpec` (below).

## What that means in practice

- **Cross-field rules are not enforced at admission — with one exception.** `spec.tls.secretName` and
  `spec.tls.certManager` are documented as mutually exclusive; nothing rejects a
  CR that sets both. The reconciler resolves it, the API server does not. The exception is
  the seccomp profile: a CEL rule on `SeccompProfileSpec` requires a non-empty
  `localhostProfile` exactly when `type` is `Localhost`, and the enum refuses `Unconfined`
  ([ADR 0033](../adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md)
  D1; envtest, green 2026-09-26). ~~The rule does not check the path's shape: an absolute path
  or a `..` element passes the CRD and is refused by the API server only when the operator
  writes the workload (upstream `validateSeccompProfileField`), which surfaces as
  `ReconcileBlocked/WriteFailed` (`reconcileBlockedReason`) — both read, neither tested here.~~
  *(Superseded 2026-09-26.)* A second rule refuses a `localhostProfile` that starts with `/` or
  holds a `..` element (`(^|/)[.][.](/|$)`, so `..` inside a file name passes), at CR admission
  instead of at the workload write ([`valkey_types.go`](../../api/v1/valkey_types.go); envtest rows for
  an absolute path, a leading, inner and trailing `..` and dots inside a name — ~~no recorded run
  yet~~ green in repeated runs on 2026-09-26, Kubernetes 1.29 API server). Which `Localhost` profile may be named at all is **not** admission validation: the API
  server accepts any well-formed path, and the reconciler refuses one its allow-list does not
  name (`ReconcileBlocked/SeccompProfileNotAllowed`, [the `Localhost` allow-list](seccomp-profiles.md#the-localhost-allow-list)).
- **A rejected CR write is a first-class runtime state, not an error path.** A
  third-party fail-closed webhook (Kyverno, OPA) that rejects the operator's writes
  is surfaced on the CR as the `ReconcileBlocked` condition with the rejecting
  webhook named in the message — an admission rejection the CR once did not show at all is
  why that condition exists ([ADR 0002](../adr/0002-surface-a-blocked-reconcile-on-the-cr.md)).
- **The operator validates the data plane, not the input.** Its checks are about
  reachability, replication role and sync state; it trusts the CR — with one exception since
  2026-09-26, the `Localhost` seccomp allow-list above.

## What this does not cover

No admission check narrows what a CR author may choose beyond the schema and its two CEL
rules. The image, the generated names and the other choices the schema admits are stated
under [what does not hold](isolation-and-tenancy.md#what-does-not-hold); the one choice the
reconciler refuses — a `Localhost` profile outside the allow-list — under
[the `Localhost` allow-list](seccomp-profiles.md#the-localhost-allow-list). This page carries
no open gap of its own.
