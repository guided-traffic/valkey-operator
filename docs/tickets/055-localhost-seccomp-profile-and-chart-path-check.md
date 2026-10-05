---
id: T55
title: localhost seccomp profiles - none is recommended for the generated pods, and the chart path check passes a leading blank
state: analysed       # facts verified and measured, options weighed; every decision open
severity: low         # every generated pod runs under RuntimeDefault; both parts are defense in depth
security: hardening
threat: "would additionally cover the syscalls RuntimeDefault still allows the data, Sentinel and observer pods (one pod-level profile per Valkey resource, none shipped, the only in-repository Localhost profile an e2e fixture weaker than RuntimeDefault), and an installer-supplied localhostProfile for the operator and its pre-upgrade hook whose leading whitespace hides a '/' or '..' from the chart's render refusal, which gains no principal anything"
urgency: now          # rule 1: three tracked sentences contradict the RDB measurement; the rest is later or icebox
effort: L             # option B of Q1; the wording correction, Q1 = A, Q2 and the chart clause are XS each
blocked-by: product   # Q1 (does this repository ship a profile); the chart clause waits on Q3; the wording correction is not blocked
filed-from: T31, section "Further security measures — not in this change, each open" (archive/031)
opened: 2026-09-27
decided:
done:
---

# T55 - localhost seccomp profiles - none is recommended for the generated pods, and the chart path check passes a leading blank

**Scope.** Both halves of the `Localhost` seccomp path: the profile a Valkey resource may name for
its generated pods, and the profile the chart installer may name for the operator's own pods.
Neither weakens the posture below `RuntimeDefault`; one decides whether this repository ships a
profile and corrects a false failure description, the other decides whether the chart refuses a
path whose whitespace hides a shape the chart promises to refuse.

- **Recommended profile for the generated pods** - none ships, and the docs misdescribe how a
  too-strict profile fails.
- **Chart path check for the operator's own profile** - a leading blank passes the absolute and
  `..` refusal.

## Current state

### Recommended profile for the generated pods

The operator-facing statement of the gap is [H-19](../security/seccomp-profiles.md#h-19).

- **One profile per Valkey resource, for three pod kinds.** `spec.podSecurity.seccompProfile` is
  `RuntimeDefault` (default) or `Localhost`; the CRD enum refuses `Unconfined`, and
  `GetSeccompProfile` ([`valkey_types.go:1431-1441`](../../api/v1/valkey_types.go#L1431)) maps
  everything but `Localhost` to `RuntimeDefault`. That value becomes the pod-level profile of the
  data and Sentinel pods ([`pod_security.go:117`](../../internal/builder/pod_security.go#L117)) and
  the observer pod ([`pod_security.go:133`](../../internal/builder/pod_security.go#L133)). No
  container sets its own, the root `fix-data-ownership` repair
  ([`pod_security.go:222-234`](../../internal/builder/pod_security.go#L222)) included. A shipped
  profile is therefore one union over `valkey-server`, `valkey-sentinel`, `dash`, the tools of
  [`RequiredImageTools`](../../internal/builder/image_requirements.go), the root `chown` repair,
  the sidecar and observer (operator-image Go binaries) and the exporter. Per-container fields are
  rejected in
  [ADR 0033:484-485](../adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md#L484).
- **A `Localhost` profile is written only if the allow-list names its exact path**
  ([`pod_hardening.go:27-37`](../../internal/controller/pod_hardening.go#L27), ADR 0033 D9); the
  chart default is empty ([`values.yaml:49`](../../deploy/helm/valkey-operator/values.yaml#L49)).
  The docs name clusters that manage their own profiles, the Security Profiles Operator for
  example, as the intended user
  ([`seccomp-profiles.md:24-27`](../security/seccomp-profiles.md#L24),
  [ADR 0033:469-470](../adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md#L469)).
- **The only `Localhost` profile in the repository is the e2e fixture** `hardeningProfile`
  ([`pod_hardening_test.go:46`](../../test/e2e/pod_hardening_test.go#L46)): allow by default,
  18 syscalls denied, "a fixture that proves the Localhost path, not a recommended profile". It is
  weaker than `RuntimeDefault`: containerd v2.1.3 (the CI Kind runtime) ships a default-deny
  `RuntimeDefault` that already refuses all 18 and more for capability-free containers. Measured in
  docker on 9.1.1 and 8.1.9 (uid 999, `--cap-drop ALL`, `no-new-privileges`): `unshare -U -r id`
  fails under the default profile and succeeds as root under the fixture.
- **A shipped profile could only be tested against the repository's own pins.** `spec.image` and
  `spec.metrics.image` are CR-chosen
  ([`valkey_types.go:1032-1034`](../../api/v1/valkey_types.go#L1032),
  [`valkey_types.go:663`](../../api/v1/valkey_types.go#L663)); only the test images are pinned
  ([`images.go:39-45`](../../test/testimages/images.go#L39), Debian 13 with `sh -> dash` on 9.1.1
  and 8.1.9), and their minor and patch moves automerge
  ([`renovate.json:225-236`](../../renovate.json#L225)), as do Go toolchain bumps of the operator
  image ([`Containerfile:2`](../../Containerfile#L2),
  [`renovate.json:47-57`](../../renovate.json#L47)). The exporter default is digest-pinned and not
  tracked by Renovate ([`valkey_types.go:645`](../../api/v1/valkey_types.go#L645)).
- **A too-strict profile fails differently per persistence mode.** Measured in docker with a
  profile denying `fsync` and `fdatasync` and the generated arguments
  ([`configmap.go:203-231`](../../internal/builder/configmap.go#L203)):
  - AOF and both: `valkey-server` exits 1 at startup, a crashloop (9.1.1 and 8.1.9).
  - RDB: the container keeps running; after the first failed `BGSAVE` every write and `PING`
    answer `MISCONF`, while `valkey-cli ping` exits 0. The probes
    ([`statefulset.go:1515-1545`](../../internal/builder/statefulset.go#L1515)) do not check the
    reply and nothing reads the save status, so the pod stays Ready and the status stays quiet
    while every write is refused. Under `save 900 1` the first automatic `BGSAVE` comes at most
    900 s after the first change.
- **Three tracked sentences contradict the RDB result**, saying a too-strict profile fails the
  container: [`pod-security.md:68-69`](../operations/pod-security.md#L68),
  [`seccomp-profiles.md:39`](../security/seccomp-profiles.md#L39),
  [ADR 0033:416](../adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md#L416).
- **The hardening e2e already runs a `Localhost` profile**:
  `TestE2E_PodHardening_UserNamespacesLocalhostSeccompAndDigest`
  ([`pod_hardening_test.go:199`](../../test/e2e/pod_hardening_test.go#L199)) moves 3 replicas,
  3 Sentinels, `aof`, metrics and the observer onto the fixture; it does not cover RDB, TLS, the
  drain `preStop` hook or the ownership repair.
- **No decision is recorded.** ADR 0033 Alternatives Considered (`:464-500`) has no entry for a
  shipped profile; H-19's last sentence
  ([`seccomp-profiles.md:189-191`](../security/seccomp-profiles.md#L189)) words the absence as a
  current state.

**Impact:** nothing beyond what `RuntimeDefault` leaves open; an administrator who wants a
narrower filter builds, maintains and lists their own, and is told a too-strict profile fails a
container, while on an RDB tier the pod stays Ready and refuses every write.

### Chart path check for the operator's own profile

The operator Deployment and the pre-upgrade hook Job take their posture from
`valkey-operator.podHardening`
([`_helpers.tpl:86-116`](../../deploy/helm/valkey-operator/templates/_helpers.tpl)), included at
[`deployment.yaml:30`](../../deploy/helm/valkey-operator/templates/deployment.yaml) and
[`pre-upgrade-job.yaml:27`](../../deploy/helm/valkey-operator/templates/pre-upgrade-job.yaml).
With `podSecurity.seccompProfile.type: Localhost` it refuses a missing path (:105-106), then tests
the **raw** value (:108-109):

```
{{- if or (hasPrefix "/" $sp.localhostProfile) (regexMatch "(^|/)[.][.](/|$)" $sp.localhostProfile) }}
```

and writes it unchanged and quoted into both pod specs (:112). Nothing trims. Measured with
`helm template` (helm v3.21.3): these render with exit 0 into the Deployment and the Job -
`/abs.json` preceded by a blank, a tab, a newline, U+00A0, U+200B or U+FEFF; `../x.json` preceded
by a blank or U+00A0; `profiles/ok.json` followed by a blank. `"/abs.json"` fails with exit 1
("must be a relative path without '..'"). The rendered manifest shows an invisible character
escaped (`quote` is `%q`); the values file does not.

Upstream, read in the Kubernetes v1.36.1 source, not measured: the API server validates a
`Localhost` path with `path.IsAbs` and a `..`-element check without trimming, so it accepts
exactly what the chart accepts - the chart implements the rule
[ADR 0033](../adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md) D6
states, and the gap is between that rule and what a reader of the values file expects. Kubelet
computes `filepath.Join("/var/lib/kubelet/seccomp", localhostProfile)`, so a blank-prefixed
`/abs.json` resolves below the seccomp root.

The values comment ([`values.yaml:25-28`](../../deploy/helm/valkey-operator/values.yaml)) and
[`operator-pod-posture.md:20`](../security/operator-pod-posture.md) state the rule as "not
absolute and without a '..' element" / "starts with `/` or has a `..` element"; both are literally
true. ADR 0033's residual risks (lines 633-637) record the blank pass-through only for the
allow-list.

Fails closed, therefore out of scope: the allow-list helper (`_helpers.tpl:136-143`) passes blanks,
but `profileList` ([`main.go:91-99`](../../cmd/main.go)) trims every entry; the CRD rule
([`valkey_types.go:542`](../../api/v1/valkey_types.go)) passes a leading blank, but
`seccompProfileAllowed` ([`pod_hardening.go:27-30`](../../internal/controller/pod_hardening.go))
requires an exact match with a trimmed entry, so no Valkey workload is written with it. The
operator's own profile is the only place such a value reaches a pod spec.

**Impact:** only an installer who types whitespace into the operator's own `localhostProfile`
(default `""` under `RuntimeDefault`):

- Install: the render succeeds and the operator pod does not start (profile file missing), like
  any misspelled path, but the value looks like a shape the chart promises to refuse.
- Upgrade with the hook: the hook Job never succeeds, `helm upgrade` fails (expected on its
  `--timeout`), the Deployment keeps its old spec.
- Upgrade with `preUpgradeHook.enabled: false`: the new operator pod does not start, the old one
  keeps serving, and `helm upgrade` without `--wait` reports success.
- No weakening: `Unconfined` stays unrenderable (`_helpers.tpl:113-114`), and the data, Sentinel
  and observer pods are not affected.

## Required changes

### Shared across parts

- Both parts amend ADR 0033 (the failure wording at :416, D6 and its residual risks, and D10
  under Q1 = A) and [`pod-security.md`](../operations/pod-security.md). Whatever is decided at the
  time goes into one ADR 0033 edit with one Status amendment, superseded text marked in place, no
  ticket citation.

### Recommended profile - independent of the open questions

1. Reword [`pod-security.md:68-69`](../operations/pod-security.md#L68),
   [`seccomp-profiles.md:39`](../security/seccomp-profiles.md#L39) and
   [ADR 0033:416](../adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md#L416)
   to what was measured: the blocked syscall fails, and what follows depends on the syscall and the
   persistence mode; with `fsync`/`fdatasync` blocked, AOF crashloops and RDB stays Ready answering
   `MISCONF`. Do not claim every missing syscall behaves like that pair.
2. Proof: `git grep -n 'too strict fails\|fails a container\|container fails at a syscall'`
   outside `docs/tickets/` finds nothing. Then this part's urgency becomes `icebox` (rule 5).

### Recommended profile - depends on the answers

- **Q1 = A:** ADR 0033 gains the refusal ("This repository ships no `Localhost` profile; the
  `Localhost` path serves clusters that record and maintain their own") as D10 or a new ADR (Q2),
  with its reopen trigger (a user who names their `spec.image`, exporter and topology and asks for
  a profile), an Alternatives Considered entry, a Status amendment, and the index row in
  [`docs/adr/README.md:103`](../adr/README.md#L103) checked. H-19's last sentence states the
  refusal and links the decision. This part closes as `dropped`.
- **Q1 = B:**
  1. Record every container with the Security Profiles Operator on Kind for 9.1.1 and 8.1.9 and
     merge into one pod-level profile.
  2. Ship it as a file with an operations page, including the roll it triggers for adopters and the
     data loss of a single non-persistent data pod without Sentinel on that roll.
  3. Swap the fixture in `pod_hardening_test.go` for it, add RDB and both, and add a negative
     control asserting writes and `BGSAVE` (`rdb_last_bgsave_status:ok`) per mode, not only pod
     start.
  4. Tie a profile re-check to Valkey pin moves, Go toolchain bumps and exporter moves.
  5. Amend ADR 0033 and H-19.

### Chart path check - independent of the open questions

- When T43's render check is built, it carries a row for the operator's own `localhostProfile`
  set to `/abs.json` with a leading blank: accepted-shape row under Q3 = C, negative row under
  Q3 = A or B, with the blank-prefixed `../x.json`, the tab, the U+00A0 and the trailing-blank
  values beside it.
- Optional: a row in `TestSeccompProfileAllowed`
  ([`pod_hardening_test.go:138`](../../internal/controller/pod_hardening_test.go)) with CR value
  `" profiles/a.json"` and allow-list `profiles/a.json`, expected refused; it fails if
  `seccompProfileAllowed` ever trims the CR value.

### Chart path check - depends on the answers

- **Q3 = A:** add `(ne $sp.localhostProfile (trim $sp.localhostProfile))` to the condition at
  [`_helpers.tpl:108`](../../deploy/helm/valkey-operator/templates/_helpers.tpl) and extend the
  message at :109 with "and without surrounding whitespace". **Q3 = B:** additionally
  `(regexMatch "\\p{C}" $sp.localhostProfile)`.
- State the rule (extra clause under A/B, accepted pass-through under C) in
  [`values.yaml:25-28`](../../deploy/helm/valkey-operator/values.yaml); ADR 0033 D6 (line 278ff),
  its D9 scope bullet (lines 385-389) and its residual-risk paragraph (lines 633-637);
  [`operator-pod-posture.md:20`](../security/operator-pod-posture.md), :46 and :113-116;
  [`pod-security.md:165-167`](../operations/pod-security.md);
  [`README.md:560`](../../README.md);
  [ADR 0013](../adr/0013-operator-is-cluster-wide-privileged.md) lines 243-247; the CLAUDE.md
  pod-hardening bullet under A/B.
- Tests under Q3 = A: `helm template` fails with the new message for `/abs.json` preceded by a
  blank, a tab or U+00A0, for `../x.json` preceded by a blank, and for `profiles/ok.json` followed
  by a blank; `profiles/ok.json`, `profiles/..v..json`, `a /b.json` and the defaults render;
  `/abs.json` preceded by U+200B renders (documented limit; refused under B). `/abs.json` and
  `profiles/../x.json` still fail. Mutation check: removing the `trim` clause turns the
  blank-prefixed `/abs.json` row red.
- Check under Q3 = A/B:
  `git grep -n -e 'relative path without' -e 'relative, no' -e 'starts with' -e 'absolute path or a' -- README.md CLAUDE.md docs deploy ':!docs/tickets'`
  lists no statement of the operator's path rule without the whitespace clause.

## Open questions

### Q1: Does this repository ship and maintain a recommended `Localhost` seccomp profile? (recommended profile)

Every generated pod already runs the node's `RuntimeDefault`; a shipped profile would be one union
over all programs of the data, Sentinel and observer pods, testable only on the pinned images,
while users choose their own images. A missing syscall costs a crashloop (AOF) or a Ready pod
refusing every write (RDB).

- **A - refuse, recorded as an ADR decision (recommended).** XS; changes nothing that runs;
  administrators keep recording their own profiles as the docs already describe.
- **B - ship one merged union profile as a documented file, not installed by the chart.** L up
  front, plus a permanent re-check on every Valkey pin, Go toolchain and exporter move; the gain
  over `RuntimeDefault` is bounded by the union and unmeasured.

A is recommended: `RuntimeDefault` already refuses everything the only in-repository profile
refuses, B's benefit is small and unmeasured while its cost is permanent and its failure lands in a
data tier, and A writes down the design intent the docs already state.

**Answer:** _open_

### Q2: Does the refusal go into ADR 0033 as D10 or into a new ADR? (recommended profile, only if Q1 = A)

The seccomp choice is ADR 0033's decision family (D1 introduced `Localhost`, D9 the allow-list);
CLAUDE.md says a new durable decision gets its own ADR. The content is the same either way.

- **ADR 0033 D10 (recommended).** Keeps all `Localhost` rules in one file, next to the D6 path rule
  that Q3 amends.
- **New ADR plus an index line.** Follows the "own ADR" rule literally; splits the `Localhost` rules
  across two files.

**Answer:** _open_

### Q3: Should the chart refuse a `localhostProfile` with surrounding whitespace for the operator's own pods? (chart path check)

Today the chart mirrors the API server rule exactly, so `/abs.json` with a leading blank renders
and the operator pod fails at container start. Refusing it moves the failure to render time and
makes the chart stricter than the CRD rule and the API server by one clause, which ADR 0033 D6 has
to state.

- **A - refuse surrounding whitespace (recommended).** One `trim` clause; refuses blanks, tabs,
  newlines and U+00A0 on either side, measured on a scratch copy; a blank inside the path still
  renders; U+200B and U+FEFF (format characters) still render.
- **B - A plus refuse every control and format character (`\p{C}`).** Also refuses U+200B and
  U+FEFF (measured); same XS cost, but one more rule for D6 and the values comment to explain, for
  copy-paste artefacts nobody has met.
- **C - keep the check, document the pass-through as accepted.** Text only; the chart stays equal
  to the CEL and API server rule, and safety rests on upstream behaviour that was only read, at one
  Kubernetes version, with the runtime side not read at all.

A is recommended: it refuses nothing any node is expected to hold, removes the dependency on
unmeasured upstream behaviour, and the `%q`-quoted message shows the stray blank at render instead
of a container-start error on a node.

**Answer:** _open_

## Not verified

- The syscall set of each generated container on 9.1.1 and 8.1.9 (settled by an SPO
  `ProfileRecording` on Kind), and whether SPO recording works on the e2e Kind clusters (needs
  auditd/syslog or the BPF recorder). Needed only for Q1 = B.
- "The fixture is weaker than `RuntimeDefault`" holds for Docker's moby profile and containerd
  v2.1.3; not checked for containerd 2.3.1 or CRI-O.
- That an RDB pod stays Ready in a real cluster under a too-strict profile: inferred from docker and
  the probe command; the auth and TLS probe variants were not run.
- The persistence mode of production clusters, which decides which failure shape they would show.
- API server admission and kubelet/runtime behaviour for a blank-prefixed `/abs.json`: read in the
  v1.36.1 source only, the runtime side not read; one Kind install with such a value settles it.
- That the hook Job fails `helm upgrade` on its `--timeout` (`backoffLimit: 3` never counts) and
  that a hook-disabled upgrade leaves the old pod serving: inferred, not measured.
- That no node holds a profile file whose path starts or ends with whitespace: the assumption
  behind "Q3 = A refuses nothing that works".

## Related

- T45 - an exporter Renovate manager would add to the re-check cost under Q1 = B.
- T40 - lists this ticket behind H-19; the H-19 rewording must cite no ticket.
- T43 - the chart render check that carries the whitespace row.
