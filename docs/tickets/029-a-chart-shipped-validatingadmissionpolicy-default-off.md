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
writable by anything holding `pods: patch`, all three enumerated in `SECURITY_ARCHITECTURE.md`
section 3 (since 2026-09-27
[`docs/security/isolation-and-tenancy.md`, "What does not hold"](../security/isolation-and-tenancy.md#what-does-not-hold)),
and none of them expressible as an RBAC restriction, because `resourceNames` is the only
object-level narrowing Kubernetes offers and it is already in use.

**The blocker is a decision, not the code.** [ADR 0015](../adr/0015-one-crd-validated-by-schema-only.md)
D2 refuses admission **webhooks**, and its stated reason is a measured outage of a third-party
webhook backend. A `ValidatingAdmissionPolicy` has no backend to lose, so D2's reasoning does
not transfer — but it must be **amended explicitly** rather than silently stretched, because
"we refuse admission control" is how the sentence currently reads to anyone who has not read
the reason.

VAP is GA from Kubernetes 1.30 and [`README.md`](../../README.md) declares a 1.29 floor, so it
is opt-in behind a chart value or a floor bump. Default off either way, which is the
[ADR 0005](../adr/0005-upgrade-neutral-defaults-and-anti-affinity.md) rule for anything that
can reject a write an upgrade would otherwise have made.

## History

- 2026-09-27 - `SECURITY_ARCHITECTURE.md` was split into `docs/security/` by the documentation
  restructure; the section 3 pointer above now also names its new place. No finding changed.
- 2026-09-27 - extracted verbatim from the collection ticket (now [archive/039-findings-from-the-1-11-0-fleet-rollout.md](archive/039-findings-from-the-1-11-0-fleet-rollout.md)) into its own file when the tickets were numbered. Frontmatter filled from the final board row (board archive of that file, groomed 2026-09-26) and from the section text. One sentence of the section was left out under the embargo rule of [README.md](README.md#an-open-security-finding-is-embargoed); its wording is kept in an embargoed ticket file and returns when that embargo ends (corrected 2026-09-27: this entry first recorded the omission in other words, which are kept in the same file).
