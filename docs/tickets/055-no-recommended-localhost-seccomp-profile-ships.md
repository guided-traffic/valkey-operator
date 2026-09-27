---
id: T55
title: no recommended Localhost seccomp profile exists for the generated containers
state: filed
severity: low         # every generated pod runs under RuntimeDefault; a narrower filter is defense in depth
security: hardening
threat: "would additionally cover the syscalls the container runtime's generic RuntimeDefault profile still allows valkey-server, valkey-sentinel, the sidecar and the exporter: today no narrower profile exists, and the one Localhost profile in the repository is an allow-by-default e2e fixture"
urgency: icebox       # rule 5: shipping and maintaining a profile is a product call
effort: L
blocked-by: product   # whether this repository ships and maintains a profile
filed-from: T31, section "Further security measures — not in this change, each open" (archive/031)
opened: 2026-09-27
decided:              # not recorded - no decision yet
done:                 # not recorded - not done
---

Filed on 2026-09-27 from the eighth row of the table
["Further security measures — not in this change, each open"](archive/031-generated-pods-run-as-root.md#further-security-measures--not-in-this-change-each-open)
in the archived ticket 031, which is `done` and tracks none of its rows. The operator-facing
statement is gap [H-19](../security/seccomp-profiles.md#h-19).

## Fact

**Verified** (read 2026-09-27):

- `spec.podSecurity.seccompProfile` is `RuntimeDefault` (default) or `Localhost`, and a
  `Localhost` profile is written into a workload only if the operator's allow-list names its
  exact path
  ([ADR 0033](../adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md)
  D1, D9).
- The only `Localhost` profile in the repository is the e2e fixture `hardeningProfile`
  ([`test/e2e/pod_hardening_test.go`](../../test/e2e/pod_hardening_test.go) line 46):
  `defaultAction: SCMP_ACT_ALLOW` with a deny list of 18 syscalls, described in its own comment
  as "a fixture that proves the Localhost path, not a recommended profile".
- A profile for the data pods has to allow the `chown` of the `fix-data-ownership` repair
  (`find /data ! -user 999 -exec chown -h 999:999 {} +`,
  [`pod_security.go`](../../internal/builder/pod_security.go) line 206;
  [ADR 0032](../adr/0032-generated-pods-run-rootless.md)).

**Not verified:**

- The syscall set of each generated container on either pinned Valkey line.
- Whether the Security Profiles Operator's recording works on the Kind clusters the e2e uses.

## Impact

None today beyond what `RuntimeDefault` leaves open; an operator who wants a narrower filter
has to build and maintain one themselves, then list it
([H-19](../security/seccomp-profiles.md#h-19)).

## Options

The decision is a product call: whether this repository ships and maintains a profile.

- **A — ship nothing.** The documentation keeps explaining how to list one's own profile.
- **B — recorded profiles, shipped as documented files, not installed by the chart (best).**
  Recorded per container with the Security Profiles Operator on both pinned lines, published
  with how to install them on nodes, and exercised by an e2e run with them listed.
- **C — ship them as Security Profiles Operator objects in the chart.** Installation becomes
  one value, and the chart gains a dependency on an optional operator.

B is marked because it gives operators a tested starting point without making the chart
depend on another operator. Its cost is maintenance: a Valkey release that needs a syscall the
profile lacks crashes the pods of whoever installed it, so the profile needs a check wherever
the image pins move ([ADR 0017](../adr/0017-test-and-ci-policy.md) D42, D43).

## Decision

None yet.

## Verification

- The full e2e suite on both pinned lines, with the shipped profiles listed and named by the
  test clusters, is green.
- A negative control: a profile with one required syscall removed makes the affected pod fail
  to start.

## History

- 2026-09-27 — filed from the row "A recommended `Localhost` seccomp profile (e.g. recorded with
  the Security Profiles Operator)" of archive/031. Gap [H-19](../security/seccomp-profiles.md#h-19)
  states what the operator enforces today and how to list a profile.
