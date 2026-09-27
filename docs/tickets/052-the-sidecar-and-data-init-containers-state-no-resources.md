---
id: T52
title: the sidecar and the data pod's init containers state no resources, so a cpu/memory ResourceQuota refuses the data pods
state: filed
severity: low         # a quota namespace cannot host a data tier at all; nothing else breaks
security: hardening
threat: "would additionally cover namespaces that use a cpu/memory ResourceQuota as a tenancy control: today the data pods are refused there, so such a namespace cannot host a Valkey data tier unless the quota is loosened"
urgency: icebox       # rule 5: reopens ADR 0033 D7
effort: S
blocked-by: decision  # ADR 0033 D7, see Options
filed-from: T31, section "Further security measures — not in this change, each open" (archive/031)
opened: 2026-09-27
decided:              # not recorded - no decision yet
done:                 # not recorded - not done
---

Filed on 2026-09-27 from the fifth row of the table
["Further security measures — not in this change, each open"](archive/031-generated-pods-run-as-root.md#further-security-measures--not-in-this-change-each-open)
in the archived ticket 031, which is `done` and tracks none of its rows. The row itself notes
"ADR 0033 D7 (decided: no defaults)". The operator-facing statement is gap
[H-18](../security/workload-pod-posture.md#h-18).

## Fact

**Verified** (read 2026-09-27):

- In [`statefulset.go`](../../internal/builder/statefulset.go) only the `valkey` container
  (line 871, `v.Spec.Resources`) and the exporter (lines 1124–1125, `spec.metrics.resources`)
  are given container resources. `buildSidecarContainer` (line 894) sets none, and neither do
  the data pod's init containers — `init-config-selector` (built at lines 274 and 435) and the
  containers of [`pod_security.go`](../../internal/builder/pod_security.go), which contains no
  `Resources` at all (`dataWritableCheck`, line 176; `WithDataOwnershipRepair`, line 247).
- [ADR 0033](../adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md)
  D7: a cpu/memory `ResourceQuota` admits a pod without pod-level resources only when every
  container, init containers included, states the values (read in Kubernetes v1.36.4); the
  sidecar and the data pod's init containers deliberately keep stating none, because a limit
  guessed too low OOM-kills the process that holds the drain promotion
  ([ADR 0012](../adr/0012-the-sidecar-records-its-drain-promotion-on-the-pod.md)).
- ADR 0033's residual risks: pod-level `spec.resources` (`PodLevelResources`, on by default
  since Kubernetes 1.34, read in v1.36.4) exempts a pod from the per-container quota check. The
  [README](../../README.md) declares Kubernetes v1.29+.

**Not verified:**

- The sidecar's memory and CPU under a drain promotion; nobody has measured them.
- What an API server below 1.34 does with pod-level resources (drops or refuses the field).

## Impact

Every namespace with a cpu/memory `ResourceQuota`: the data StatefulSet is written, and its
pods are refused at admission. What an operator can do today is set every field that exists —
`spec.resources`, `spec.metrics.resources`, `spec.observer.resources`,
`spec.sentinel.resources` — which admits the Sentinel and observer pods, and host the data tier
in a namespace without such a quota.

## Options

The decision is whether to reopen ADR 0033 D7.

- **A — keep D7.** No fields, no defaults; quota namespaces cannot run the data tier.
- **B — explicit fields, no default (best).** A resources field for the sidecar and one for the
  data pod's init containers (names to be decided); unset means no requests and no limits, as
  today, so the upgrade changes nothing
  ([ADR 0005](../adr/0005-upgrade-neutral-defaults-and-anti-affinity.md) D1). D7's reason
  stays intact: nothing is guessed, the CR author sets a value they measured.
- **C — pod-level `spec.resources`.** One field, and a pod carrying it is exempt from the
  per-container check. Needs Kubernetes 1.34 while the README declares 1.29.
- **D — defaults for the sidecar and the init containers.** The alternative D7 rejected.

B is marked because it works on every Kubernetes version the project supports and keeps D7's
reason. C is the better option once the floor reaches 1.34, because one field covers every
present and future container.

## Decision

None yet.

## Verification

- e2e in a namespace with a cpu/memory `ResourceQuota`: with the new fields and the existing
  ones set, the data pods are admitted and the cluster reaches `OK`; with the new fields unset,
  the data pods are refused (the negative control of
  [ADR 0017](../adr/0017-test-and-ci-policy.md) D11).
- A unit test pins that an unset field yields a pod template identical to today's.

## History

- 2026-09-27 — filed from the row "Sidecar / init container requests" of archive/031. Gap
  [H-18](../security/workload-pod-posture.md#h-18) states what is missing and what to set in the
  meantime.
