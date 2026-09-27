---
id: T56
title: the operator has no namespace-scoped mode
state: filed
severity: medium      # the operator stays equivalent to cluster-admin, with every Secret of the cluster in its memory
security: hardening
threat: "would additionally cover a compromise of the operator's ServiceAccount token or process: today it may get, list, watch and delete every Secret in the cluster and holds every Secret it watches in its informer cache; a namespace-scoped mode would confine that to the namespaces it serves"
urgency: icebox       # rule 5: reopens ADR 0013 D1
effort: L
blocked-by: decision  # ADR 0013 D1, see Options
filed-from: T31, section "Further security measures — not in this change, each open" (archive/031)
opened: 2026-09-27
decided:              # not recorded - no decision yet
done:                 # not recorded - not done
---

Filed on 2026-09-27 from the ninth row of the table
["Further security measures — not in this change, each open"](archive/031-generated-pods-run-as-root.md#further-security-measures--not-in-this-change-each-open)
in the archived ticket 031, which is `done` and tracks none of its rows. The operator-facing
statement is gap [H-1](../security/privilege-footprint.md#h-1).

## Fact

**Verified** (read 2026-09-27):

- The chart's ClusterRole grants `secrets: delete, get, list, watch`
  ([`clusterrole.yaml`](../../deploy/helm/valkey-operator/templates/clusterrole.yaml)
  lines 64–72), bound cluster-wide.
- The manager restricts its cache neither by namespace nor per object: a grep for
  `DefaultNamespaces` and `ByObject` in [`cmd/main.go`](../../cmd/main.go) finds nothing, and
  the Secret informer runs cluster-wide with no filter
  ([ADR 0016](../adr/0016-authentication-and-tls-posture.md) D2).
- [ADR 0013](../adr/0013-operator-is-cluster-wide-privileged.md) D1 treats the operator as
  equivalent to a cluster-admin credential and states that a namespace does not confine it; its
  *Alternatives Considered* record "a namespaced Role per watched namespace, or a cache filtered
  by label with the ClusterRole narrowed to match", with the cost that the operator stops being
  install-and-forget for new namespaces.
- Since 2026-08-26 the TLS Secret is read on every pass of every TLS cluster, for the material
  fingerprint ([H-1](../security/privilege-footprint.md#h-1)), so a filtered cache has one more
  consumer to satisfy.

**Not verified:**

- Which cluster-scoped reads remain in a namespace-scoped mode (how the operator detects the
  ServiceMonitor and cert-manager CRDs was not read).
- Whether a namespaced Role still needs `escalate` to create the sidecar Role — the question
  ADR 0013 D3 leaves open.

## Impact

Every install: whoever obtains the operator's token or runs code in its process reads every
Secret in the cluster. Multi-tenant clusters cannot confine the operator to its tenants'
namespaces.

## Options

The decision is whether to reopen ADR 0013 D1.

- **A — keep D1.** Cluster-wide, install-and-forget.
- **B — an opt-in namespace-scoped mode (best).** A chart value lists namespaces; the manager
  cache is restricted to them (controller-runtime `cache.Options.DefaultNamespaces`); the
  namespaced rules become a Role and RoleBinding per listed namespace, and the ClusterRole keeps
  only what is cluster-scoped. Default off, so install-and-forget stays the default. It adds a
  second RBAC shape to the three places ADR 0014 keeps in sync, and a namespace added later
  needs a chart upgrade.
- **C — a label-filtered Secret cache** (`cache.Options.ByObject`) with the ClusterRole
  unchanged. Fewer Secrets resident in memory; the token may still read every one of them,
  because RBAC cannot narrow by label.

B is marked because it is the only option that narrows what the token may do, and it leaves
the default untouched.

## Decision

None yet.

## Verification

- In the mode: `kubectl auth can-i get secrets -n <unlisted namespace>` as the operator's
  ServiceAccount answers `no`, and the e2e suite is green in a listed namespace.
- A Valkey resource in an unlisted namespace is not reconciled; how that is reported is part
  of the decision.

## History

- 2026-09-27 — filed from the row "Namespace-scoped operator mode" of archive/031, which names
  ADR 0013. Gap [H-1](../security/privilege-footprint.md#h-1) states the scope it needs and the
  options' cost.
