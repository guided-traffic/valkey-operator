---
id: T29
title: a chart-shipped ValidatingAdmissionPolicy, default off
state: filed
severity: low
security: hardening
threat: "would additionally cover metadata.ownerReferences, metadata.finalizers and spec.containers[*].image, writable by anything holding pods: patch and not expressible as an RBAC restriction"
urgency: icebox
effort: M
blocked-by: adr-0015
filed-from: T25
opened: 2026-08-27
decided:
done:
---

# T29 - a chart-shipped ValidatingAdmissionPolicy, default off

**Severity: low. Status: open, filed 2026-08-27 out of T25, same reason as T28. Effort: M, and
it needs an ADR 0015 re-decision before it needs code.**

The only in-Kubernetes control that reaches the three fields nothing else can:
`metadata.ownerReferences`, `metadata.finalizers` and `spec.containers[*].image` — all three
writable by anything holding `pods: patch`, ~~all three enumerated in `SECURITY_ARCHITECTURE.md`
section 3 (since 2026-09-27
[`docs/security/isolation-and-tenancy.md`, "What does not hold"](../security/isolation-and-tenancy.md#what-does-not-hold)),~~
*(corrected 2026-09-27: `SECURITY_ARCHITECTURE.md` is gone; all three are rows of the table in
[`isolation-and-tenancy.md`, "What does not hold"](../security/isolation-and-tenancy.md#what-does-not-hold),
`:94`, `:95` and `:96`)*
and none of them expressible as an RBAC restriction, because `resourceNames` is the only
object-level narrowing Kubernetes offers and it is already in use.

**The blocker is a decision, not the code.** [ADR 0015](../adr/0015-one-crd-validated-by-schema-only.md)
D2 refuses admission **webhooks**, and ~~its stated reason~~ *(corrected 2026-09-27: D2 itself
states no reason; the reason is ADR 0015's Context,
`0015:31-44`, and its rejected
alternative "A validating webhook enforcing cross-field rules and a registry allowlist",
`0015:191-195`)* is a measured outage of a third-party
webhook backend. A `ValidatingAdmissionPolicy` has no backend to lose, so D2's reasoning does
not transfer — but it must be **amended explicitly** rather than silently stretched, because
"we refuse admission control" is how the sentence currently reads to anyone who has not read
the reason. *(Added 2026-09-27: D2's own text,
`0015:56-65`, is scoped to
what validates a `Valkey` object, so a policy on pods is outside its letter; the heading "No
admission webhook" and the ADR title are what a reader takes as the rule.)*

VAP is GA from Kubernetes 1.30 and [`README.md`](../../README.md) declares a 1.29 floor, so it
is opt-in behind a chart value or a floor bump. Default off either way, which is the
[ADR 0005](../adr/0005-upgrade-neutral-defaults-and-anti-affinity.md) rule for anything that
can reject a write an upgrade would otherwise have made.

## Fact (enriched 2026-09-27, `HEAD` = `4a7543e`)

**Verified** (read at `HEAD`):

- Nothing ships a policy today: `git grep -i ValidatingAdmissionPolicy -- ':!docs/tickets'`
  finds only prose, [ADR 0031](../adr/0031-a-record-the-operator-trusts-lives-in-pod-spec.md) `:190`
  ("Filed, not taken here").
- Who holds `pods: patch`: the per-cluster sidecar Role, `get` and `patch` on `pods` narrowed by
  `resourceNames` ([`rbac.go:69-74`](../../internal/builder/rbac.go#L69-L74)), bound to the
  ServiceAccount `<cr>-sidecar` ([`rbac.go:18-19`](../../internal/builder/rbac.go#L18-L19)); and the
  operator's ClusterRole, `pods` with `patch` cluster-wide
  ([`clusterrole.yaml:50-59`](../../deploy/helm/valkey-operator/templates/clusterrole.yaml#L50-L59)).
- What the sidecar legitimately writes, all as a JSON merge patch on pod metadata
  ([`labeler.go:255-262`](../../internal/sidecar/labeler.go#L255-L262)): the label
  `vko.gtrfc.com/instanceRole` on its own pod ([`labeler.go:154`](../../internal/sidecar/labeler.go#L154),
  and `draining` at [`drain.go:119-120`](../../internal/sidecar/drain.go#L119-L120)), and the
  annotation `vko.gtrfc.com/drain-promoted-at` on the pod it promotes
  ([`drain.go:216-217`](../../internal/sidecar/drain.go#L216-L217)). Nothing else.
- The floor: [`README.md`](../../README.md) `:198` "Kubernetes cluster (v1.29+)";
  [`Chart.yaml`](../../deploy/helm/valkey-operator/Chart.yaml) has no `kubeVersion`; envtest is
  1.29.0 ([`Makefile:5`](../../Makefile#L5)), CI Kind 1.33.4
  ([`release.yml:23`](../../.github/workflows/release.yml#L23)).
- The chart's precedent for a default-off template whose API may be missing:
  [`servicemonitor.yaml:1-7`](../../deploy/helm/valkey-operator/templates/servicemonitor.yaml#L1-L7)
  is gated by its value alone, and a missing CRD is an install error, "which is why it is
  default-off". No template uses `.Capabilities` (grep over `templates/`).

**Not verified:**

- That VAP is GA (`admissionregistration.k8s.io/v1`) from 1.30 and beta, off by default, before
  it: an upstream fact, not checked here.
- That the CEL of Decision 2 A (compare the object with `oldObject` except two keys, ignoring
  server-managed fields) fits the VAP cost budget; nothing was written.
- That the garbage collector and the StatefulSet controller write `ownerReferences` and
  `finalizers` on these pods (the reason Decision 2 C loses): upstream behaviour, not measured.
- That envtest 1.29 cannot serve a v1 VAP: inferred from the version, not run.

## Impact

Principal: a compromised **sidecar container** of cluster X, the only container holding the
`<cr>-sidecar` token ([ADR 0012](../adr/0012-the-sidecar-records-its-drain-promotion-on-the-pod.md)
D8 step 4). Verb: `patch`. Object: the data pods of cluster X only (`resourceNames`). Dormant
today: it needs a compromised sidecar first. The operator's own cluster-wide `pods: patch` is out
of scope; the operator is trusted.

## Options

Three decisions, in this order: 1 gates 2 and 3.

### Decision 1 — may the chart ship a ValidatingAdmissionPolicy at all? (take first)

- **A. A new ADR admits a chart-shipped, default-off VAP, and ADR 0015 gets a Status note** that
  D2 refuses webhooks that validate `Valkey` objects and does not reach an in-process policy on
  pods. **(recommended)** Cost M: the ADR, one template pair (policy and binding), a value, the
  README Helm values row, the rows at `isolation-and-tenancy.md:94-96`, and one Kind e2e. Rolls
  nothing, because it is default off (ADR 0005). Leaves open every cluster that does not enable
  it. Why A: a VAP runs inside the API server, so the outage in ADR 0015's Context does not
  transfer. And a policy shipped with the chart can follow the generated ServiceAccount name
  (`rbac.go:18-19`) and the two keys the sidecar writes; a copied recipe cannot.
- **B. A documented recipe only**, applied by the administrator. Cost S. Nothing tests it, and it
  drifts from the generated names and keys without anything going red.
- **C. Drop T29 and accept the three rows.** They are already documented at
  `isolation-and-tenancy.md:94-96`. Costs only the close. A compromised sidecar can still swap a
  container image of a pod of its cluster or pin a pod with a finalizer. ADR 0015 D6 delegates
  image control to cluster policy tools, but D6 is about the image the CR author chooses, not a
  running pod rewritten by its sidecar.
- **D. Raise the floor to 1.30 and ship the policy on by default.** Rejected: an on-by-default
  policy that refuses writes is what ADR 0005 keeps off, and it would cost every 1.29 user the
  install for a hardening item.

### Decision 2 — whom the policy binds and what it refuses (after 1 = A)

- **A. Only the cluster's own sidecar ServiceAccount, derived from the stored pod**
  (`request.userInfo.username == 'system:serviceaccount:' + namespace + ':' +
  oldObject.metadata.labels['vko.gtrfc.com/cluster'] + '-sidecar'`). It refuses any change by
  that principal except the label `vko.gtrfc.com/instanceRole` and the annotation
  `vko.gtrfc.com/drain-promoted-at` — an allow-list. **(recommended)** One rule covers the three
  filed rows and also the `config-hash` and `pod-spec-hash` rows of the same table. It permits
  exactly the writes the sidecar exists to make (ADR 0012 D8,
  [`isolation-and-tenancy.md`](../security/isolation-and-tenancy.md#what-does-not-hold) "Nothing
  narrower is expressible for the label and the drain stamp"). A new sidecar write then needs a
  policy change, and the e2e shows it. Costs the most CEL (a map comparison minus two keys).
- **B. The same principal, with a deny-list of the three filed fields.** The smallest CEL. It
  leaves every other metadata key open, the two hash annotations included, and each new row of
  the table needs a new rule.
- **C. Every principal except the operator's ServiceAccount.** Rejected: the garbage collector
  and the StatefulSet controller write `ownerReferences` and `finalizers` on these pods (not
  verified here, see above), so deletion and adoption would break.

### Decision 3 — a cluster without the v1 API (after 1 = A; a one-liner)

- **A. Gate on the value alone, as `servicemonitor.yaml:1-7` does**: enabling the policy on a
  cluster without the v1 API is an install error. **(recommended)** It follows the chart's own
  precedent, the error is loud, and the README floor stays 1.29.
- **B. Gate on `.Capabilities.APIVersions.Has`**: where the API is missing it silently renders
  nothing, so the administrator believes the policy is on. `helm template` without
  `--api-versions` also renders nothing. No template uses `.Capabilities` today.
- **C. Raise the floor:** see Decision 1 D.

## Decision

Not yet decided.

## Work list

Nothing here is XS without a decision. The no-decision corrections were stale sentences in this
file, and they are done above.

1. *(waits on Decision 1)* A new ADR amending ADR 0015, a Status note on ADR 0015, and the row in
   [`docs/adr/README.md`](../adr/README.md).
2. *(waits on Decisions 2 and 3)* The policy and binding templates under
   `deploy/helm/valkey-operator/templates/`, a default-off value in `values.yaml`, and the row in
   the README Helm values reference.
3. [`isolation-and-tenancy.md:94-96`](../security/isolation-and-tenancy.md#what-does-not-hold):
   the rows name the policy as the opt-in control.
4. A Kind e2e (envtest 1.29 cannot serve it). T58's chart render gate would also cover the
   enabled render path.
5. Close ([ADR 0034](../adr/0034-tickets-are-work-lists-that-get-archived.md)): extraction into
   the ADR and the security page, `git grep -nwE 'T29|029'` outside `docs/tickets/`, then
   `archive/`.

## Verification

- Kind, policy enabled, acting as `<cr>-sidecar`: a patch of `ownerReferences`, `finalizers`, a
  container image or `config-hash` is refused with the policy's message. The labeler patch, the
  drain patch and every operator patch still pass.
- `helm template` renders no policy with the value off, and the policy plus its binding with it
  on.

## History

- 2026-09-27: adversarial review of the enrichment below. One clause the enrichment added to
  Options was removed before commit under the
  [embargo rule](README.md#an-open-security-finding-is-embargoed); its wording is not repeated
  here. Spot-checked at `4a7543e`: the `file:line` locations of Fact, the ADR 0015 lines and
  `isolation-and-tenancy.md:94-96` hold. Recommendations, urgency and effort unchanged.
- 2026-09-27: enriched - Fact split into Verified / Not verified with `file:line` locations,
  Impact, Options (three ordered decisions, each with a recommendation), Decision (not yet
  decided), work list, Verification. Two stale sentences corrected in place: the
  `SECURITY_ARCHITECTURE.md` pointer, and where ADR 0015 states its reason. Frontmatter
  unchanged: urgency re-derived as `icebox` (rule 5: it needs a re-decision of ADR 0015 that
  nobody has accepted), effort M, blocked by `adr-0015`.
- 2026-09-27 - `SECURITY_ARCHITECTURE.md` was split into `docs/security/` by the documentation
  restructure; the section 3 pointer above now also names its new place. No finding changed.
- 2026-09-27 - extracted verbatim from the collection ticket (now [archive/039-findings-from-the-1-11-0-fleet-rollout.md](archive/039-findings-from-the-1-11-0-fleet-rollout.md)) into its own file when the tickets were numbered. Frontmatter filled from the final board row (board archive of that file, groomed 2026-09-26) and from the section text. One sentence of the section was left out under the embargo rule of [README.md](README.md#an-open-security-finding-is-embargoed); its wording is kept in an embargoed ticket file and returns when that embargo ends (corrected 2026-09-27: this entry first recorded the omission in other words, which are kept in the same file).
