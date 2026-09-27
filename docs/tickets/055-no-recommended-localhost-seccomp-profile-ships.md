---
id: T55
title: no recommended Localhost seccomp profile exists for the generated containers
state: analysed       # facts verified, options weighed; the open items are needed only for option B
severity: low         # every generated pod runs under RuntimeDefault; a narrower filter is defense in depth
security: hardening
threat: "would additionally cover the syscalls the node runtime's RuntimeDefault filter still allows every process of the data, Sentinel and observer pods, which share one pod-level profile per Valkey resource; none ships, and the one Localhost profile in the repository is an e2e fixture weaker than RuntimeDefault"
urgency: now          # rule 1: three tracked sentences contradict the RDB measurement; icebox (rule 5) once reworded
effort: L             # the cost of option B; option A and the wording correction are XS each
blocked-by: product   # whether this repository ships and maintains a profile; the wording correction is not blocked
filed-from: T31, section "Further security measures — not in this change, each open" (archive/031)
opened: 2026-09-27
decided:
done:
---

# T55 - no recommended Localhost seccomp profile exists for the generated containers

## Current state

The operator-facing statement of the gap is [H-19](../security/seccomp-profiles.md#h-19).

- **One profile per Valkey resource, for three pod kinds.** `spec.podSecurity.seccompProfile` is
  `RuntimeDefault` (default) or `Localhost`; the CRD enum refuses `Unconfined`, and
  `GetSeccompProfile` ([`valkey_types.go:1431-1441`](../../api/v1/valkey_types.go#L1431)) maps
  everything but `Localhost` to `RuntimeDefault`. That one value becomes the pod-level profile of
  the data and Sentinel pods ([`pod_security.go:117`](../../internal/builder/pod_security.go#L117))
  and the observer pod ([`pod_security.go:133`](../../internal/builder/pod_security.go#L133)). No
  container sets its own, the root `fix-data-ownership` repair
  ([`pod_security.go:222-234`](../../internal/builder/pod_security.go#L222)) included. A shipped
  profile is therefore necessarily one union over `valkey-server`, `valkey-sentinel`, `dash` and
  the tools of [`RequiredImageTools`](../../internal/builder/image_requirements.go), the root
  `chown` repair, the sidecar and observer (operator-image Go binaries) and the exporter.
  Per-container fields are rejected in
  [ADR 0033:484-485](../adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md#L484).
- **A `Localhost` profile is written only if the allow-list names its exact path**
  ([`pod_hardening.go:27-37`](../../internal/controller/pod_hardening.go#L27), ADR 0033 D9); the
  production chart default is empty ([`values.yaml:49`](../../deploy/helm/valkey-operator/values.yaml#L49)).
  The tracked docs name the intended user of `Localhost` as clusters that manage their own
  profiles, the Security Profiles Operator for example
  ([`seccomp-profiles.md:24-27`](../security/seccomp-profiles.md#L24),
  [ADR 0033:469-470](../adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md#L469)).
- **The only `Localhost` profile in the repository is the e2e fixture** `hardeningProfile`
  ([`pod_hardening_test.go:46`](../../test/e2e/pod_hardening_test.go#L46)): allow by default,
  18 syscalls denied, "a fixture that proves the Localhost path, not a recommended profile".
  It is weaker than `RuntimeDefault`: containerd v2.1.3 (the CI Kind runtime) ships a
  default-deny `RuntimeDefault` that, for capability-free containers, already refuses all 18
  and more. Measured in docker on 9.1.1 and 8.1.9 (uid 999, `--cap-drop ALL`,
  `no-new-privileges`): `unshare -U -r id` fails under the default profile and succeeds as root
  under the fixture.
- **A shipped profile could only be tested against the repository's own pins.** `spec.image`
  and `spec.metrics.image` are CR-chosen
  ([`valkey_types.go:1032-1034`](../../api/v1/valkey_types.go#L1032),
  [`valkey_types.go:663`](../../api/v1/valkey_types.go#L663)); the repository pins only its test
  images ([`images.go:39-45`](../../test/testimages/images.go#L39), Debian 13 with `sh -> dash`
  on 9.1.1 and 8.1.9), whose minor and patch moves automerge
  ([`renovate.json:225-236`](../../renovate.json#L225)). Go toolchain bumps for the operator
  image ([`Containerfile:2`](../../Containerfile#L2)) automerge too
  ([`renovate.json:47-57`](../../renovate.json#L47)). The exporter default is digest-pinned and
  not tracked by Renovate ([`valkey_types.go:645`](../../api/v1/valkey_types.go#L645)).
- **A too-strict profile fails differently per persistence mode.** Measured in docker with a
  profile denying `fsync` and `fdatasync` and the arguments the operator generates
  ([`configmap.go:203-231`](../../internal/builder/configmap.go#L203)):
  - AOF and both: `valkey-server` exits 1 at startup, a crashloop (9.1.1 and 8.1.9).
  - RDB: the container keeps running; after the first failed `BGSAVE` every write and `PING`
    answer `MISCONF`, while `valkey-cli ping` exits 0. The probes
    ([`statefulset.go:1515-1545`](../../internal/builder/statefulset.go#L1515)) do not check the
    reply and nothing in the operator reads the save status, so the pod stays Ready and the
    status stays quiet while every write is refused. Under `save 900 1` the first automatic
    `BGSAVE` comes at most 900 s after the first change.
- **Three tracked sentences contradict the RDB result**, saying a too-strict profile fails the
  container: [`pod-security.md:68-69`](../operations/pod-security.md#L68),
  [`seccomp-profiles.md:39`](../security/seccomp-profiles.md#L39),
  [ADR 0033:416](../adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md#L416).
- **The hardening e2e already runs a `Localhost` profile**:
  `TestE2E_PodHardening_UserNamespacesLocalhostSeccompAndDigest`
  ([`pod_hardening_test.go:199`](../../test/e2e/pod_hardening_test.go#L199)) moves 3 replicas,
  3 Sentinels, `aof`, metrics and the observer onto the fixture. It does not cover RDB, TLS, the
  drain `preStop` hook or the ownership repair.
- **No decision is recorded.** ADR 0033 Alternatives Considered (`:464-500`) has no entry for a
  shipped profile; H-19's last sentence
  ([`seccomp-profiles.md:189-191`](../security/seccomp-profiles.md#L189)) words the absence as a
  current state.

**Impact:** nothing beyond what `RuntimeDefault` leaves open; an administrator who wants a
narrower filter builds, maintains and lists their own. That administrator is told a too-strict
profile fails a container, while on an RDB tier the pod stays Ready and refuses every write.

## Required changes

### Independent of the open questions

1. Reword the three sentences ([`pod-security.md:68-69`](../operations/pod-security.md#L68),
   [`seccomp-profiles.md:39`](../security/seccomp-profiles.md#L39),
   [ADR 0033:416](../adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md#L416))
   to what was measured: the blocked syscall fails, and what follows depends on the syscall and
   the persistence mode; with `fsync`/`fdatasync` blocked, AOF crashloops and RDB stays Ready
   answering `MISCONF`. Do not claim every missing syscall behaves like that pair. The ADR edit
   is marked in place as the ADR rules require; no ticket citation.
2. Proof: `git grep -n 'too strict fails\|fails a container\|container fails at a syscall'`
   outside `docs/tickets/` finds nothing. Then urgency becomes `icebox` (rule 5).

### Depends on the answers

- **Under A:** ADR 0033 gains D10 ("This repository ships no `Localhost` profile; the
  `Localhost` path serves clusters that record and maintain their own") with its reopen trigger
  (a user who names their `spec.image`, exporter and topology and asks for a profile), an
  Alternatives Considered entry, a Status amendment, and the index row in
  [`docs/adr/README.md:103`](../adr/README.md#L103) checked. H-19's last sentence states the
  refusal and links D10. Close as `dropped`.
- **Under B:**
  1. Record every container with the Security Profiles Operator on Kind for 9.1.1 and 8.1.9 and
     merge into one pod-level profile.
  2. Ship it as a file with an operations page, including the roll it triggers for adopters and
     the data loss of a single non-persistent data pod without Sentinel on that roll.
  3. Swap the fixture in `pod_hardening_test.go` for it, add RDB and both, and add a negative
     control asserting writes and `BGSAVE` (`rdb_last_bgsave_status:ok`) per mode, not only pod
     start.
  4. Tie a profile re-check to Valkey pin moves, Go toolchain bumps and exporter moves.
  5. Amend ADR 0033 and H-19. Close as `done`.

## Open questions

### Q1: Does this repository ship and maintain a recommended `Localhost` seccomp profile?

Every generated pod already runs the node's `RuntimeDefault`; a shipped profile would be one
union over all programs of the data, Sentinel and observer pods, testable only on the pinned
images, while users choose their own images. A missing syscall costs a crashloop (AOF) or a
Ready pod refusing every write (RDB).

- **A - refuse, recorded as ADR 0033 D10 (recommended).** XS; changes nothing that runs;
  administrators keep recording their own profiles as the docs already describe.
- **B - ship one merged union profile as a documented file, not installed by the chart.** L up
  front, plus a permanent re-check on every Valkey pin, Go toolchain and exporter move; the gain
  over `RuntimeDefault` is bounded by the union and unmeasured.

A is recommended: `RuntimeDefault` already refuses everything the only in-repository profile
refuses, B's benefit is small and unmeasured while its cost is permanent and its failure lands
in a data tier, and A writes down the design intent the docs already state.

**Answer:** _open_

### Q2: Under A, does the refusal go into ADR 0033 as D10 or into a new ADR?

The seccomp choice is ADR 0033's decision family (D1 introduced `Localhost`, D9 the allow-list);
CLAUDE.md says a new durable decision gets its own ADR. The content is the same either way.

- **ADR 0033 D10 (recommended).** Keeps all `Localhost` rules in one file.
- **New ADR plus an index line.** Follows the "own ADR" rule literally; splits the `Localhost`
  rules across two files.

**Answer:** _open_

## Not verified

- The syscall set of each generated container on 9.1.1 and 8.1.9; settled by a `ProfileRecording`
  with the Security Profiles Operator on Kind. Needed only for B.
- Whether SPO recording works on the e2e Kind clusters (needs auditd/syslog or the BPF recorder
  on the node). Needed only for B.
- "The fixture is weaker than `RuntimeDefault`" holds for Docker's moby profile and containerd
  v2.1.3; not checked for containerd 2.3.1 or CRI-O.
- That an RDB pod stays Ready in a real cluster under a too-strict profile: inferred from docker
  and the probe command; the auth and TLS probe variants were not run.
- The persistence mode of production clusters, which decides which failure shape they would show.

## Related

- T54 - an exporter Renovate manager would add to B's re-check cost.
- T40 - lists T55 behind H-19; the H-19 rewording must cite no ticket.
