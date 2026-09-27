---
id: T55
title: no recommended Localhost seccomp profile exists for the generated containers
state: analysed       # was filed; 2026-09-27: facts re-verified at 84a39c2, options corrected and weighed
severity: low         # every generated pod runs under RuntimeDefault; a narrower filter is defense in depth
security: hardening
threat: "would additionally cover the syscalls the node runtime's RuntimeDefault filter still allows the processes of the data, Sentinel and observer pods (valkey-server, valkey-sentinel, the operator-image sidecar and observer, the exporter, the init scripts including the root chown repair); the operator sets one pod-level profile per Valkey resource for all three pod kinds, so a shipped profile would be their union; today none ships, and the one Localhost profile in the repository is an allow-by-default e2e fixture, measured weaker than RuntimeDefault"  # rewritten 2026-09-27: was limited to four processes (valkey-server, valkey-sentinel, the sidecar, the exporter), which misses the observer and every init container
urgency: now          # was icebox (rule 5); 2026-09-27: rule 1 matches first - three tracked sentences say a too-strict profile fails a container, and the RDB measurement contradicts them; back to icebox (rule 5) once they are reworded
effort: L             # the cost of option B; the recommended option A is XS, and the decision-free wording correction is XS
blocked-by: product   # whether this repository ships and maintains a profile; the wording correction in the Work list is not blocked
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
  ([`test/e2e/pod_hardening_test.go:46`](../../test/e2e/pod_hardening_test.go#L46)):
  `defaultAction: SCMP_ACT_ALLOW` with a deny list of 18 syscalls, described in its own comment
  as "a fixture that proves the Localhost path, not a recommended profile".
- A profile for the data pods has to allow the `chown` of the `fix-data-ownership` repair
  (`find /data ! -user 999 -exec chown -h 999:999 {} +`,
  [`pod_security.go:206`](../../internal/builder/pod_security.go#L206);
  [ADR 0032](../adr/0032-generated-pods-run-rootless.md)).

*Added 2026-09-27 (enrichment, at `4a7543e`):*

- The fixture's comment goes on: "the runtime's default filter refuses more"
  (`pod_hardening_test.go` lines 42–45). The one `Localhost` profile in the repository is
  therefore weaker than `RuntimeDefault`. `git grep` for `defaultAction` and `SCMP_ACT` outside
  `docs/tickets/` finds only that fixture and ADR 0033 lines 671–672. ~~Nothing outside
  `docs/tickets/` mentions the Security Profiles Operator.~~ *(corrected 2026-09-27 at 84a39c2:
  two tracked files mention it, line-wrapped so that a one-line grep misses the name —
  [`docs/security/seccomp-profiles.md:25-26`](../security/seccomp-profiles.md#L25) and
  [ADR 0033:469-470](../adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md#L469),
  both times as the reason the `Localhost` option exists: "`Localhost` exists for clusters that
  manage their own profiles (the Security Profiles Operator, say)". No tracked file outside
  `docs/tickets/` recommends or ships a profile recorded with it. Found with
  `git grep -n -i 'profiles operator'` and `git grep -n 'Security Profiles'`.)*
- **A shipped profile could be verified only against the repository's own pins, not against
  what users run.**
  - `spec.image` is required and chosen by the CR author
    ([`valkey_types.go:1032-1034`](../../api/v1/valkey_types.go#L1032);
    `required: - image` in `config/crd/bases/vko.gtrfc.com_valkeys.yaml`).
  - The repository pins only its test images
    ([`test/testimages/images.go:39-45`](../../test/testimages/images.go#L39)).
  - Renovate moves those pins through the `custom.regex` manager for that file, and the
    `custom.regex` automerge rule ([`renovate.json:225-236`](../../renovate.json#L225)) has
    no dependency filter, so their minor and patch moves automerge after CI. This was read in
    the config and not observed on a PR: `git log` shows no pin move since `f5f3256`
    (2026-08-22).
  - The exporter default is pinned by digest and not tracked by Renovate
    ([`valkey_types.go:645`](../../api/v1/valkey_types.go#L645),
    [ticket 054](054-renovate-does-not-track-the-default-exporter-image.md)).
- Besides the servers, a data or Sentinel profile has to allow what the generated scripts
  execute: the list in `RequiredImageTools`
  ([`image_requirements.go`](../../internal/builder/image_requirements.go)). The sidecar and
  the observer run the operator image (sidecar:
  [`statefulset.go:989-996`](../../internal/builder/statefulset.go#L989); observer:
  [`observer.go:71`](../../internal/builder/observer.go#L71) and
  [`observer.go:82`](../../internal/builder/observer.go#L82)).

*Added 2026-09-27 (re-verification at `84a39c2`):*

- **One pod-level profile per Valkey resource, for three pod kinds.** `GetSeccompProfile`
  ([`valkey_types.go:1431-1441`](../../api/v1/valkey_types.go#L1431)) returns `RuntimeDefault`
  unless the field names `Localhost`; the CRD enum refuses `Unconfined`
  ([`valkey_types.go:549`](../../api/v1/valkey_types.go#L549)). That one value becomes the
  pod-level `SeccompProfile` of the data pods
  ([`statefulset.go:627`](../../internal/builder/statefulset.go#L627) →
  [`pod_security.go:111-121`](../../internal/builder/pod_security.go#L111), profile at `:117`),
  the Sentinel pods ([`sentinel.go:407`](../../internal/builder/sentinel.go#L407) → the same
  function) and the observer pod ([`observer.go:128`](../../internal/builder/observer.go#L128) →
  [`pod_security.go:127-137`](../../internal/builder/pod_security.go#L127), profile at `:133`).
  No container sets its own: `restrictedContainerSecurityContext`
  ([`pod_security.go:58-64`](../../internal/builder/pod_security.go#L58)) and the root
  `fix-data-ownership` container's own `securityContext`
  ([`pod_security.go:222-234`](../../internal/builder/pod_security.go#L222), the one container
  with `CAP_CHOWN`) carry no `seccompProfile`, so both inherit the pod-level one. Any shipped
  profile is therefore necessarily one union over `valkey-server`, `valkey-sentinel`, `dash` and
  the GNU tools of `RequiredImageTools`, the root `chown` repair, two operator-image Go binaries
  (sidecar and observer) and the exporter. Per-container or per-tier profiles would need new CRD
  fields, which [ADR 0033:484-485](../adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md#L484)
  already rejects ("Fields for the sidecar and the init containers as well. More API surface
  with no known user."). Container-level `seccompProfile` is also never compared
  ([`docs/security/seccomp-profiles.md:45-46`](../security/seccomp-profiles.md#L45)), so a
  cluster can already give single containers their own profiles through its own mutating
  admission policy without the operator reverting it — outside this repository.
- **The operator image** is a static Go binary built with `golang:1.27.1-alpine` on
  `gcr.io/distroless/static-debian12:nonroot` ([`Containerfile:2`](../../Containerfile#L2),
  [`Containerfile:35`](../../Containerfile#L35)). Go minor and patch moves automerge
  ([`renovate.json:47-57`](../../renovate.json#L47)), and reach the Containerfile through the
  `golang-version` custom manager. A profile covering the sidecar and the observer would need a
  re-check on every such bump.
- **The exporter is CR-chosen too:** `spec.metrics.image`
  ([`valkey_types.go:663`](../../api/v1/valkey_types.go#L663)), resolved by `MetricsImage`
  ([`valkey_types.go:1212-1217`](../../api/v1/valkey_types.go#L1212)) with the digest-pinned
  default as fallback.
- **Both pinned Valkey images** (9.1.1 and 8.1.9) are Debian 13 trixie with `/usr/bin/sh`
  linked to `dash`, and `/usr/bin/find` and `/usr/bin/chown` present. Measured:
  `docker run --rm --name vko-verify-t055-os valkey/valkey:9.1.1 sh -c 'cat /etc/os-release | head -3; ...'`
  and the same on 8.1.9 → `Debian GNU/Linux 13 (trixie)` on both, `valkey-server --version`
  `v=9.1.1` and `v=8.1.9`, `/usr/bin/sh -> dash`.
- **`RuntimeDefault` on the CI runtime is default-deny.** containerd v2.1.3 (the runtime of the
  CI Kind nodes, `kindest/node v1.33.4`, per
  [`pod_hardening_test.go:110-117`](../../test/e2e/pod_hardening_test.go#L110)),
  [`contrib/seccomp/seccomp_default.go`](https://raw.githubusercontent.com/containerd/containerd/v2.1.3/contrib/seccomp/seccomp_default.go):
  `DefaultAction` `specs.ActErrno` (line 487); `chown` (:74), `fchownat` (:118), `fsync` (:133)
  and `lchown` (:198) are in the unconditional allow list; `bpf`, `perf_event_open`, `quotactl`
  and `unshare` are allowed under `CAP_SYS_ADMIN` (:578-604), `bpf` also under `CAP_BPF` (:693)
  and `perf_event_open` also under `CAP_PERFMON` (:699), `open_by_handle_at` under
  `CAP_DAC_READ_SEARCH` (:572), `reboot` under `CAP_SYS_BOOT` (:609), the module calls under
  `CAP_SYS_MODULE` (:621), `acct` under `CAP_SYS_PACCT` (:631); `kexec_load`,
  `kexec_file_load`, `swapon`, `swapoff`, `keyctl`, `add_key`, `request_key`, `userfaultfd` and
  `pivot_root` appear nowhere. For the generated containers, which drop every capability,
  `RuntimeDefault` on that runtime already refuses all 18 syscalls the fixture refuses, and more.
- **Measured: the fixture is weaker than the default profile.** `docker run --rm --name
  vko-verify-t055-def --user 999:999 --cap-drop ALL --security-opt no-new-privileges --read-only
  valkey/valkey:9.1.1 sh -c 'grep Seccomp: /proc/self/status; unshare -U -r id; echo exit=$?'`,
  and the same with `--security-opt seccomp=fixture.json`, where `fixture.json` is the
  `hardeningProfile` const copied verbatim (Docker 28.4.0 linux/arm64, kernel 6.10.14-linuxkit).
  Default (moby) profile: `Seccomp: 2`, `unshare: unshare failed: Operation not permitted`,
  `exit=1`. Fixture: `Seccomp: 2`, `uid=0(root) gid=0(root) groups=0(root)`, `exit=0`. Repeated
  on 8.1.9 with the same result. Under the fixture a uid 999, capability-free, `no_new_privs`
  container creates a user namespace; under the default it cannot.
- **Measured: a too-strict profile fails in a way that depends on the persistence mode.** A
  profile that allows everything and returns `SCMP_ACT_ERRNO` for `fsync` and `fdatasync`, run
  as `docker run -d --name vko-verify-t055e-<mode> --user 999:999 --cap-drop ALL --security-opt
  no-new-privileges --security-opt seccomp=nofsync.json --tmpfs /data:uid=999,gid=999
  valkey/valkey:9.1.1 valkey-server --dir /data <mode args>`, with the arguments the operator
  generates ([`configmap.go:203-215`](../../internal/builder/configmap.go#L203) for RDB: `save`
  points plus `stop-writes-on-bgsave-error yes` at `:206-209`;
  [`configmap.go:225-231`](../../internal/builder/configmap.go#L225) for AOF: `appendfsync
  everysec` at `:230`):
  - **AOF, and both:** `valkey-server` logs `# Write error writing append only file on disk:
    Operation not permitted` at startup and exits 1 — a crashloop. AOF measured on 9.1.1 and
    8.1.9; both (`--save '900 1' --stop-writes-on-bgsave-error yes --appendonly yes
    --appendfsync everysec`) on 9.1.1.
  - **RDB:** `SET` answers `OK`, `BGSAVE` answers `Background saving started`,
    `rdb_last_bgsave_status:err` follows; afterwards every write **and `PING`** answer
    `MISCONF Valkey is configured to save RDB snapshots ...`, while `valkey-cli ping` still
    exits 0 and the container keeps running with 0 restarts; the log reads `# Write error while
    saving DB to the disk(fsync): Operation not permitted`. Measured on 9.1.1 and 8.1.9, and
    re-measured on 9.1.1 in the review of this run (`vko-verify-t055r-rdb`, same result,
    container removed). The readiness and liveness probes are `valkey-cli ... ping`
    (`ProbeCommand`,
    [`statefulset.go:1515-1545`](../../internal/builder/statefulset.go#L1515), used at `:850`
    and `:862`), which do not check the reply, so such a pod stays Ready while refusing every
    write. Nothing in the operator reads the save status: `grep -rn -i
    'misconf\|bgsave_status\|rdb_last'` over `internal/` and `cmd/` (tests excluded) finds only
    comments, so the operator's status does not report it either.
    [`pod_security.go:171-175`](../../internal/builder/pod_security.go#L171) records the
    same shape, measured in-cluster under T31, for an unwritable volume. Under the generated
    save points the first automatic `BGSAVE` comes at the latest 900 s after the first change
    (`save 900 1`, [`configmap.go:206`](../../internal/builder/configmap.go#L206)), so the pod
    accepts writes until then (read in the config, not timed).

  An earlier measurement in this run used `--save ''` with no AOF — the configuration of a
  non-persistent cluster, which never calls `fsync` — and read "writes succeed, saves fail
  silently"; that is no configuration the operator generates for a persistent cluster and is not
  carried. Every container started was removed.
- **Three tracked sentences contradict the RDB measurement.** They state that a too-strict
  profile fails the container:
  [`docs/operations/pod-security.md:68-69`](../operations/pod-security.md#L68) ("one that is too
  strict fails a container at the syscall it blocks"),
  [`docs/security/seccomp-profiles.md:39`](../security/seccomp-profiles.md#L39) ("Too strict, a
  container fails at a syscall.") and
  [ADR 0033:416](../adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md#L416)
  ("too strict, a container fails at a syscall"). For AOF they hold; for RDB the container does
  not fail, it stays Ready and refuses every write.
- **The existing hardening e2e already runs a `Localhost` profile.**
  `TestE2E_PodHardening_UserNamespacesLocalhostSeccompAndDigest`
  ([`pod_hardening_test.go:199`](../../test/e2e/pod_hardening_test.go#L199)) installs the
  fixture (`:206`) and moves a cluster of 3 replicas and 3 Sentinels with persistence mode
  `aof` (`:214`), metrics and the observer onto it (`:231`), in both single-node full-suite
  legs. It does not cover RDB mode (the quiet failure above), TLS, the drain `preStop` hook
  (multi-replica without Sentinel only) or the ownership repair (the cluster is built rootless).
  The e2e chart values list the fixture and a deliberately missing path
  ([`test/e2e/helm-values.yaml:19-25`](../../test/e2e/helm-values.yaml#L19)); the production
  chart default is empty
  ([`values.yaml:49`](../../deploy/helm/valkey-operator/values.yaml#L49)).
- **The Security Profiles Operator** records one profile per container
  ([`installation-usage.md`](https://raw.githubusercontent.com/kubernetes-sigs/security-profiles-operator/c90ef3a168eed8c9a31ea413039f4a8695f7641e/installation-usage.md)
  at `c90ef3a`, line 361: a two-container pod yields `recording-nginx` and `recording-redis`),
  requires cert-manager to install (line 46), and saves an installed profile at
  `/var/lib/kubelet/seccomp/operator/<namespace>/<name>.json` (line 75). That path passes the
  chart's allow-list entry check
  ([`_helpers.tpl:135-143`](../../deploy/helm/valkey-operator/templates/_helpers.tpl#L135)),
  which refuses only an empty entry, a leading `/`, a `,` or a `..` element.
- **No recorded decision exists.** [ADR 0033](../adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md)'s
  Alternatives Considered (`:464-500`) has no entry for a shipped profile, and H-19's last
  sentence ([`seccomp-profiles.md:189-191`](../security/seccomp-profiles.md#L189): "No
  recommended `Localhost` profile for the generated containers ships today") words the absence
  as a current state. `git grep -n 'T55\|055-no-recommended'` outside `docs/tickets/` finds
  nothing. Since `4a7543e`, the only commit touching `api/`, `internal/builder`, the hardening
  e2e, `seccomp-profiles.md`, ADR 0033 or `renovate.json` is the comment commit `bcc63c9`.

**Not verified:**

- The syscall set of each generated container on either pinned Valkey line. Settling it needs
  cert-manager plus the Security Profiles Operator on a local Kind cluster and a
  `ProfileRecording` against the hardening topology on 9.1.1 and 8.1.9. Needed only for B.
- Whether the Security Profiles Operator's recording works on the Kind clusters the e2e uses
  (its log enricher needs auditd or syslog on the node, or the BPF recorder, per the SPO
  documentation, not run). That Docker Desktop's linuxkit kernel offers no audit log to record
  in was stated by the audit of this run and not tested. Needed only for B.
- `RuntimeDefault` is whatever the node's runtime ships, not one profile. "The fixture is weaker
  than `RuntimeDefault`" is verified for Docker's moby profile (measured) and containerd v2.1.3
  (source). It is not verified for containerd 2.3.1 (the local Kind cluster) or CRI-O.
- That the RDB pod stays Ready in a real cluster under a too-strict profile: inferred from
  `valkey-cli ping` exiting 0 on `MISCONF` in docker and from the probe command, corroborated by
  the T31 in-cluster measurement of the same shape for another cause. The probe variants with
  auth (`sh -c "valkey-cli -a ..."`) and TLS were not run; they call the same binary.
- The persistence mode and topology of the production clusters: not recorded in any file this
  run could read, so which of the two failure shapes a production tier would show is open.

## Impact

None today beyond what `RuntimeDefault` leaves open; an operator who wants a narrower filter
has to build and maintain one themselves, then list it
([H-19](../security/seccomp-profiles.md#h-19)). Such an administrator is told by three tracked
sentences (Fact) that a too-strict profile fails a container. On an RDB tier it does not: the
pod stays Ready and every write answers `MISCONF`, which neither the probes nor the operator's
status report. That misstatement is what gives this ticket urgency `now`; the absence of a
shipped profile alone would not.

## Options

### Decision 1 — does this repository ship and maintain a recommended `Localhost` profile?

**Mechanism.** One CR field, `spec.podSecurity.seccompProfile`, becomes one pod-level
`SeccompProfile` shared by the data, Sentinel and observer pods
([`pod_security.go:117`](../../internal/builder/pod_security.go#L117),
[`:133`](../../internal/builder/pod_security.go#L133)); no container sets its own. A `Localhost`
path is written only if the operator's allow-list names it exactly
([`pod_hardening.go:27-37`](../../internal/controller/pod_hardening.go#L27)), and the chart's
allow-list defaults to empty. Without a listed profile every generated pod runs the node
runtime's `RuntimeDefault`, which on the CI runtime is default-deny and already refuses, for
these capability-free containers, every syscall the repository's only `Localhost` profile
refuses. The tracked documentation names the intended user of the `Localhost` path as clusters
that manage their own profiles ([`seccomp-profiles.md:24-27`](../security/seccomp-profiles.md#L24),
[ADR 0033:469-470](../adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md#L469)).

**What the choice changes:** whether the repository publishes a syscall list it promises works
for its generated pods and keeps that list current, and where that promise, or the refusal of
it, is recorded. **What it does not change:** the default (`RuntimeDefault` for every pod), the
allow-list of ADR 0033 D9, the CRD, and every running pod. No option rolls the fleet, because a
profile applies only where an administrator lists it and a CR names it. No option can constrain
what a user runs: `spec.image` and `spec.metrics.image` are CR-chosen.

- **A — refuse, recorded as a rule in ADR 0033 (recommended).** The repository ships no
  recommended `Localhost` profile, and that is recorded as a decision: ADR 0033 gains **D10**,
  in present tense ("This repository ships no `Localhost` profile; the `Localhost` path serves
  clusters that record and maintain their own"), with its reopen trigger (a user who names their
  `spec.image`, exporter and topology and asks for a profile); an Alternatives Considered entry
  "A recommended `Localhost` profile shipped by this repository" with the reasons below; a dated
  Status amendment; the index row in [`docs/adr/README.md:103`](../adr/README.md#L103) checked in the
  same change (its State stays Implemented). H-19's last sentence
  ([`seccomp-profiles.md:189-191`](../security/seccomp-profiles.md#L189)) changes from "ships
  today" to the refusal, linking ADR 0033 D10 and citing no ticket
  ([ADR 0034](../adr/0034-tickets-are-work-lists-that-get-archived.md) D7).
  *Home:* ADR 0033 rather than a new ADR, because the seccomp profile choice is that ADR's
  decision family (D1 introduced `Localhost`, D9 the allow-list) and a separate ADR would split
  the `Localhost` rules across two files; CLAUDE.md's "gets its own ADR" read literally would
  instead mean a new ADR plus an index line — Hans may prefer that, and the content is the same.
  *Close:* `dropped`, because the gap the title names stays open by design and `done` would say
  a recommended profile exists; the History reason links ADR 0033 D10, and the ADR extraction
  (ADR 0034 D3) happens before the move to `archive/`.
  *Cost:* XS — one ADR amendment, one H-19 sentence. *Consequences:* administrators who want a
  narrower filter record and list their own, as
  [`docs/operations/pod-security.md:59-70`](../operations/pod-security.md#L59) and H-19 already
  describe; the repository makes no syscall promise it cannot keep for user images; reopening
  needs a new decision, which D10 names.
- **B — one merged union profile, shipped as a documented file, not installed by the chart.**
  Record the containers of the data, Sentinel and observer pods with the Security Profiles
  Operator on Kind for 9.1.1 and 8.1.9, and merge the per-container recordings into one profile,
  because the operator applies one pod-level profile to all three pod kinds. Ship it as a file
  with install instructions under `docs/operations/`, and record the decision in ADR 0033.
  *Verification:* swap the fixture in the hardening e2e for the shipped profile — no new CI leg
  for that minimal check — but that topology runs only AOF, so the check must add RDB and both,
  and a negative control must assert that writes and `BGSAVE` succeed per mode
  (`rdb_last_bgsave_status:ok`), not only that the pod starts, because an RDB gap leaves the pod
  Ready. Covering TLS, the drain `preStop` and the repair means the whole suite under the
  profile: a new leg, or giving up the suite's coverage of the production default.
  *Cost:* L up front (SPO and cert-manager on Kind, recording on two lines, merge, e2e and
  negative control, an operations page, the ADR amendment), and a permanent re-check on every
  Valkey pin move, every automerged Go toolchain bump and every exporter move (more of those if
  ticket 054's Renovate manager lands). *Consequences:* a tested starting point, for the pinned
  images and the tested topologies only. A user on another `spec.image`, another exporter or an
  untested path can get a crashloop (AOF) or a Ready pod refusing every write (RDB), both
  measured. An adopter who names the profile moves both pod-spec hashes and rolls their tiers
  ([`seccomp-profiles.md:27-29`](../security/seccomp-profiles.md#L27)); a single data pod
  without Sentinel and without persistence loses its dataset on that roll, which the operations
  page has to say. The union of Go runtimes, shell tools and `valkey-server` bounds how much
  narrower than `RuntimeDefault` it can be, and that gain is unmeasured.

**A is recommended**, on checkable grounds: (1) every generated pod already runs a filter —
`GetSeccompProfile` never yields `Unconfined` — and on the CI runtime that filter is default-deny
and refuses, for these containers, every syscall the only in-repository `Localhost` profile
refuses, plus `unshare`, measured; (2) the API allows one pod-level profile per Valkey resource,
so a shipped profile is a union over every program of the three pod kinds (Fact) whose gain over
`RuntimeDefault` is bounded and unmeasured; (3) the repository can test only 9.1.1, 8.1.9 and its own Go toolchain,
while `spec.image` and `spec.metrics.image` are CR-chosen; (4) a missing syscall is a crashloop
on AOF and, on RDB, a Ready pod that refuses every write while the probes and the status stay
quiet, both measured, and the only e2e that runs a `Localhost` profile uses AOF; (5) the tracked
documentation already names the `Localhost` path's user as clusters that bring their own
profiles, so A writes the existing design intent down as a rule; (6) severity is low and nobody
has asked. **A beats B** because B's benefit is small and unmeasured, its cost is permanent
(every pin move, every automerged Go bump, every exporter move), and its failure mode lands in
someone's data tier, while A costs XS and changes nothing that runs. A as recorded here also
replaces the earlier "ship nothing, change no file" wording, which ADR 0034 D3 and the CLAUDE.md
rule that a refusal to act is a durable decision do not allow to close.

## Decision

None yet.

## Work list

**Not waiting on a decision:**

1. Reword the three sentences that say a too-strict profile fails a container —
   [`docs/operations/pod-security.md:68-69`](../operations/pod-security.md#L68),
   [`docs/security/seccomp-profiles.md:39`](../security/seccomp-profiles.md#L39) and
   [ADR 0033:416](../adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md#L416)
   — to what was measured: the blocked syscall fails, and what follows depends on the syscall
   and the persistence mode; with `fsync` and `fdatasync` blocked, the container crashloops
   (AOF, both) or, on RDB, the pod stays Ready and every write answers `MISCONF`. The wording
   must not claim that every missing syscall behaves like `fsync`: only that pair was measured.
   No ticket citation (ADR 0034 D7); the
   ADR 0033 edit is a correction marked in place with its date. Then recompute urgency (rule 5,
   `icebox`) with a History entry.

**Waiting on the decision:**

- *Under A:* ADR 0033 D10, the Alternatives Considered entry, the dated Status amendment and the
  index row check; H-19's last sentence reworded to the refusal linking D10.
- *Under B:*
  1. Record per container with the Security Profiles Operator on Kind, for Valkey 8 and 9, and
     merge into one pod-level profile.
  2. Ship the profile as a file (location to be decided) with an operations page, including the
     roll it triggers and the single non-persistent pod's data loss.
  3. Swap the fixture in `pod_hardening_test.go` for it, add RDB and both, and add a negative
     control asserting writes and `BGSAVE` per mode.
  4. Tie a profile check to Valkey pin moves, Go toolchain bumps and exporter moves.
  5. Amend ADR 0033 and H-19.

**Close (ADR 0034):** after the extraction above for the chosen option,
`git grep -n 'T55\|055-no-recommended'` outside `docs/tickets/` (none today), then state
`dropped` (A) or `done` (B) and the move to `archive/`.

## Verification

- The wording correction: the three sentences no longer say the container fails; `git grep -n
  'too strict fails\|fails a container\|container fails at a syscall'` outside `docs/tickets/`
  finds nothing (at 84a39c2 it finds all three; `pod-security.md` wraps "fails" and "a
  container" across lines 68 and 69, so `too strict fails` is the pattern that catches it).
- Under A: ADR 0033 carries D10, the Alternatives entry and a dated Status line; H-19's last
  sentence states the refusal and links D10; no tracked file outside `docs/tickets/` cites T55.
- Under B: the full e2e suite on both pinned lines with the shipped profile listed and named is
  green, in RDB, AOF and both; a negative control with one required syscall removed fails the
  test on writes or `BGSAVE` (RDB) and on start (AOF), not only on pod start.

## History

- 2026-09-27 — re-verified at 84a39c2. Checked every Fact line against the code, the ADRs, the
  docs and `renovate.json`; locations were re-read at 84a39c2 and fixed in place (`spec.image`
  at `valkey_types.go:1032-1034`, was 1030–1032; exporter default at `:645`, was 643; the
  `custom.regex` rule ends at `renovate.json:236`; the observer image is `observer.go:71,82`,
  not in `statefulset.go`). **False:** "Nothing outside `docs/tickets/` mentions the Security
  Profiles Operator" — two tracked files mention it, line-wrapped (corrected in place). **Wrong
  premises in Options:** B's "recorded per container" (the operator applies one pod-level
  profile to data, Sentinel and observer pods, so only a union can ship); A's "changes no file"
  and "drop with the product call as its reason" (ADR 0034 D3 and CLAUDE.md need the refusal
  recorded in an ADR); "a missing syscall is a crashloop" (true for AOF, false for RDB); "a
  permanent e2e leg" (the hardening e2e can carry a minimal check); the Verification's "fail to
  start" oracle. **Measured** (docker, all `vko-verify-*` containers removed): the fixture lets a
  uid 999, capability-free container create a user namespace, the default profile does not
  (9.1.1 and 8.1.9); under a profile denying `fsync` and `fdatasync`, AOF and both exit 1 at
  startup and RDB stays running with `PING` answering `MISCONF` and `valkey-cli ping` exiting 0
  (9.1.1 and 8.1.9; both on 9.1.1); both images are Debian 13 with `sh -> dash`. Read in
  containerd v2.1.3 source: `RuntimeDefault` is default-deny and covers the fixture's 18
  syscalls. **Not carried:** an intermediate reading "writes succeed while saves fail silently",
  measured with `--save ''`, a configuration the operator does not generate for a persistent
  cluster; and a claim of silent persistence loss in named production namespaces, whose
  persistence modes are not recorded anywhere this run could read. **Options:** Options rewritten
  as one decision; A rewritten from "ship nothing, change no file" to "refuse, recorded as ADR
  0033 D10 plus an Alternatives entry, H-19 reworded, close as dropped"; B restated as one union
  profile with a per-mode negative control and the roll it causes for adopters. Removed: **C —
  Security Profiles Operator objects in the chart**, it carries all of B's cost and failure
  modes and adds a chart dependency on SPO and, through it, cert-manager, plus a CRD-presence
  guard and a default-off value, disproportionate to a low hardening gap and adding chart paths
  ticket 058 says no CI gate renders; **E — a documented recording recipe instead of a
  profile** (proposed in this run), it duplicates `docs/operations/pod-security.md:59-70` and the
  SPO's own documentation and would put an unrun procedure into tracked docs, and its one useful
  part, the corrected failure-mode wording, is decision-free work now. The superseded B
  justification ("a Valkey release that needs a syscall the profile lacks crashes the pods ... a
  check wherever the image pins move"), struck in the previous text, was removed from Options; its
  valid half lives on in B's cost. Recommendation unchanged: A, now with its recording in ADR
  0033 as the recommended home. **Frontmatter:** `state` filed → analysed (the facts the decision
  rests on are verified and the options weighed; the two open items are needed only for B);
  `urgency` icebox → now, because rule 1 matches first: three tracked sentences say a too-strict
  profile fails a container and the RDB measurement contradicts them (the looser reading "the
  syscall fails" would leave rule 5 and icebox; the stricter one is taken because
  `pod-security.md` says "fails a container"); `threat` rewritten from four processes
  ("valkey-server, valkey-sentinel, the sidecar and the exporter") to every process of the data,
  Sentinel and observer pods and the union constraint; `effort` L kept with the note that A is
  XS; `blocked-by` product kept with the note that the wording correction is not blocked.
  Cross-ticket: T54 would add exporter Renovate PRs to B's re-check cost; T40 lists T55 behind
  H-19 and the H-19 rewording under A must cite no ticket; T58 is a further reason C is not
  sensible; archive/031 row 8's "RuntimeDefault is generic" overstates the gap once the union
  constraint and containerd's default-deny profile are counted. Rules 2 to 4 do not match
  either reading: ADR 0033 D9 shipped (`git tag --contains ad81a47` gives v1.13.0 and v1.13.1),
  nothing gates a release, severity is low, and there is no decided fix; if Hans decides A, the
  XS close would fall under rule 4 (`later`). Review pass of this run: re-measured the RDB shape
  on 9.1.1 (same result, container removed); confirmed in the code that nothing reads the save
  status; fixed the `ProbeCommand` range to `statefulset.go:1515-1545` (was 1515–1535); made the
  containerd capability list exact (`bpf` also under `CAP_BPF`, `perf_event_open` under
  `CAP_PERFMON`, `open_by_handle_at` under `CAP_DAC_READ_SEARCH`; the conclusion for
  capability-free containers is unchanged); narrowed the proposed doc wording to the measured
  syscall pair; and fixed the Verification grep, which missed the line-wrapped `pod-security.md`
  sentence.
- 2026-09-27 — enriched - added that the only profile is weaker than `RuntimeDefault`, that
  `spec.image` is user-chosen while the pins automerge, the exporter pin, and
  `RequiredImageTools`. Moved the recommendation from B to A until someone asks for a profile.
  Urgency, effort and blocked-by unchanged.
- 2026-09-27 — filed from the row "A recommended `Localhost` seccomp profile (e.g. recorded with
  the Security Profiles Operator)" of archive/031. Gap [H-19](../security/seccomp-profiles.md#h-19)
  states what the operator enforces today and how to list a profile.
