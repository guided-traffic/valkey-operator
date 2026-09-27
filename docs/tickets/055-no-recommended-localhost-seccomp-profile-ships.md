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

*Added 2026-09-27 (enrichment, at `4a7543e`):*

- The fixture's comment goes on: "the runtime's default filter refuses more"
  (`pod_hardening_test.go` lines 42–45). The one `Localhost` profile in the repository is
  therefore weaker than `RuntimeDefault`. `git grep` for `defaultAction` and `SCMP_ACT` outside
  `docs/tickets/` finds only that fixture and ADR 0033 lines 671–672. Nothing outside
  `docs/tickets/` mentions the Security Profiles Operator.
- **A shipped profile could be verified only against the repository's own pins, not against
  what users run.**
  - `spec.image` is required and chosen by the CR author
    ([`valkey_types.go`](../../api/v1/valkey_types.go) lines 1030–1032).
  - The repository pins only its test images
    ([`test/testimages/images.go`](../../test/testimages/images.go) lines 39–45).
  - Renovate moves those pins through the `custom.regex` manager for that file, and the
    `custom.regex` automerge rule ([`renovate.json`](../../renovate.json) lines 225–235) has
    no dependency filter, so their minor and patch moves automerge after CI. This was read in
    the config and not observed on a PR: `git log` shows no pin move since `f5f3256`
    (2026-08-22).
  - The exporter default is pinned by digest and not tracked by Renovate (`valkey_types.go`
    line 643, [ticket 054](054-renovate-does-not-track-the-default-exporter-image.md)).
- Besides the servers, a data or Sentinel profile has to allow what the generated scripts
  execute: the list in `RequiredImageTools`
  ([`image_requirements.go`](../../internal/builder/image_requirements.go)). The sidecar and
  the observer run the operator image
  ([`statefulset.go`](../../internal/builder/statefulset.go) lines 989–996).

**Not verified:**

- The syscall set of each generated container on either pinned Valkey line.
- Whether the Security Profiles Operator's recording works on the Kind clusters the e2e uses.

## Impact

None today beyond what `RuntimeDefault` leaves open; an operator who wants a narrower filter
has to build and maintain one themselves, then list it
([H-19](../security/seccomp-profiles.md#h-19)).

## Options

The decision is a product call: whether this repository ships and maintains a profile.

- **A — ship nothing (recommended, until someone asks for a profile).** The documentation keeps
  explaining how to list one's own profile.
- **B — recorded profiles, shipped as documented files, not installed by the chart.**
  ~~(best)~~ Recorded per container with the Security Profiles Operator on both pinned lines,
  published with how to install them on nodes, and exercised by an e2e run with them listed.
- **C — ship them as Security Profiles Operator objects in the chart.** Installation becomes
  one value, and the chart gains a dependency on an optional operator.

~~B is marked because it gives operators a tested starting point without making the chart
depend on another operator. Its cost is maintenance: a Valkey release that needs a syscall the
profile lacks crashes the pods of whoever installed it, so the profile needs a check wherever
the image pins move ([ADR 0017](../adr/0017-test-and-ci-policy.md) D42, D43).~~ *(superseded as
the recommendation 2026-09-27)*

A is marked because every generated pod already runs a filter (`RuntimeDefault`), and the
`Localhost` path is default-deny (ADR 0033 D9). What B would ship is a promise the repository
can test only against its own pinned images. Users run a `spec.image` of their choice, and the
pins move by automerge (Fact). A missing syscall is a crashloop in someone's data tier, and
the cost B already named, a profile check on every pin move
([ADR 0017](../adr/0017-test-and-ci-policy.md) D42, D43), would be a permanent e2e leg for a
low-severity gap with no named user. **If Hans wants a profile anyway, B over C**: the chart
does not depend on another operator.

## Work list

**Not waiting on a decision:** nothing. Without the product call there is nothing to build,
and A changes no file.

**Waiting on the decision (B):**

1. Record per container with the Security Profiles Operator on Kind, for Valkey 8 and 9.
2. Ship the profiles as files (location to be decided).
3. Extend `pod_hardening_test.go` to install and list them, with a negative control that
   removes one syscall.
4. Tie a profile check to image pin moves.

**Close (ADR 0034):** under A, drop the ticket with the product call as its reason. The next
steps below apply to B only. Amend ADR 0033 or write a new ADR, then H-19 and a page on
installing the profiles under `docs/operations/`. `git grep -n 'T55\|055-no-recommended'`
outside `docs/tickets/` (none today), then move to `archive/`.

## Decision

None yet.

## Verification

- The full e2e suite on both pinned lines, with the shipped profiles listed and named by the
  test clusters, is green.
- A negative control: a profile with one required syscall removed makes the affected pod fail
  to start.

## History

- 2026-09-27 — enriched - added that the only profile is weaker than `RuntimeDefault`, that
  `spec.image` is user-chosen while the pins automerge, the exporter pin, and
  `RequiredImageTools`. Moved the recommendation from B to A until someone asks for a profile.
  Urgency, effort and blocked-by unchanged.
- 2026-09-27 — filed from the row "A recommended `Localhost` seccomp profile (e.g. recorded with
  the Security Profiles Operator)" of archive/031. Gap [H-19](../security/seccomp-profiles.md#h-19)
  states what the operator enforces today and how to list a profile.
