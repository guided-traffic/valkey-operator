---
id: T57
title: enable-debug-command and enable-module-command follow the image default
state: filed
severity: low         # off on both pinned lines (upstream source, 2026-09-27); the gap is that nothing states or checks it
security: hardening
threat: "would additionally cover an authenticated client — anything holding the one cluster password — running DEBUG or MODULE LOAD, should an image default ever enable them: today the generated config renders neither directive, so the image decides"
urgency: later        # rule 4: a cheap known fix either way (Options); was annotated with option A's reasoning until 2026-09-27
effort: XS
blocked-by: decision  # render the directives (A) or assert the default (B), below
filed-from: T31, section "Further security measures — not in this change, each open" (archive/031)
opened: 2026-09-27
decided:              # not recorded - no decision yet
done:                 # not recorded - not done
---

Filed on 2026-09-27 from the tenth row of the table
["Further security measures — not in this change, each open"](archive/031-generated-pods-run-as-root.md#further-security-measures--not-in-this-change-each-open)
in the archived ticket 031, which is `done` and tracks none of its rows. The operator-facing
statement is gap [H-7](../security/secrets-and-tls.md#h-7).

## Fact

**Verified** (read 2026-09-27):

- A grep for `enable-debug-command` and `enable-module-command` over `internal`, `api`, `cmd`
  and `deploy` finds nothing; the config builder
  ([`configmap.go`](../../internal/builder/configmap.go)) renders neither.
- A data pod whose config-hash annotation differs from the desired one is outdated and is
  replaced by the rolling update
  ([`rolling_update.go`](../../internal/controller/rolling_update.go) line 410), so adding a
  directive rolls every data tier on the operator upgrade that ships it. *(corrected
  2026-09-27: and every Sentinel tier. `ComputeConfigHash`
  ([`configmap.go:293-305`](../../internal/builder/configmap.go)) hashes both data configs and,
  with Sentinel, the Sentinel config into one value. That value is stamped on the data template
  ([`statefulset.go:153`](../../internal/builder/statefulset.go)) and on the Sentinel template
  ([`sentinel.go:234`](../../internal/builder/sentinel.go)), and compared by `podNeedsUpdate`
  (`rolling_update.go:424`, through `podAnnotationHashChanged` at 503-509) and by
  `sentinelPodNeedsUpdate` (`rolling_update.go:4835`, check at 4860-4864).)*
- *(Added 2026-09-27, re-read at `4a7543e`.)* The config builder is `generateValkeyConf`
  (`configmap.go:62-135`), and its `# General` block (lines 116-123) is where the directives
  would go.
- *(Added 2026-09-27.)* **The default is `no` on both pinned lines, read in upstream source.**
  `src/config.c` of `valkey-io/valkey` defines `enable-debug-command` and `enable-module-command`,
  and also `enable-protected-configs`, as `IMMUTABLE_CONFIG` with default
  `PROTECTED_ACTION_ALLOWED_NO`: lines 3375-3377 at tag `9.1.1`, lines 3267-3269 at tag `8.1.9`
  (the pins at [`images.go:40, 45`](../../test/testimages/images.go)). Immutable means `CONFIG SET`
  cannot change them at runtime. The generated container runs `valkey-server <config>` directly
  ([`statefulset.go:822-833`](../../internal/builder/statefulset.go)), so the compiled default
  applies.
- *(Added 2026-09-27.)* **Option A reaches the single data pod of a `spec.replicas: 1` cluster
  in two ways, and one of them loses data.** `handleStandaloneRollingUpdate`
  (`rolling_update.go:3756`) asks `singlePodDeferral`
  ([`pod_security_migration.go:128-150`](../../internal/controller/pod_security_migration.go)).
  For a rootless pod it defers only when `isSidecarOnlyChange` (`rolling_update.go:3842-3863`,
  images only) holds. So a release that moves the sidecar image and the config hash together is
  deferred on the Helm path. Where the sidecar image does not move (kustomize or a floating tag,
  as the comment at `pod_security_migration.go:109-113` records), the pod is deleted at once
  (`rolling_update.go:3812-3813`), and a non-persistent one loses its dataset. The comment at
  lines 122-126 rests on "a configuration change is the CR author's", which A would make false.
- *(Added 2026-09-27.)* `spec.image` is required and chosen by the CR author
  ([`valkey_types.go:1030-1032`](../../api/v1/valkey_types.go)). The pins in `images.go` decide
  only what CI runs.

**Not verified:**

- ~~The default of either directive in the two pinned images
  ([`test/testimages/images.go`](../../test/testimages/images.go)); believed `no` since
  Redis 7.~~ *(corrected 2026-09-27: verified from upstream source at both pinned tags, see Fact.
  What is still not verified is the images themselves: neither was run with `CONFIG GET`.)*
- Whether Sentinel's config accepts either directive.
- *(Added 2026-09-27.)* Whether any cluster runs a `spec.image` that predates these directives
  (a Redis 6 image, for example). Such a server is expected to refuse an unknown directive at
  startup, so under A every pod of such a cluster would crash-loop. Neither half was checked.

## Impact

None today ~~if the believed default holds~~ *(corrected 2026-09-27: the default is verified
`no` in upstream source on both pinned lines)*. ~~The pin follows the image across Valkey majors
([ADR 0017](../adr/0017-test-and-ci-policy.md) D43), so a changed default would arrive without
a change in this repository.~~ *(corrected 2026-09-27: that conflated the test pins with
production. What runs is `spec.image`, the CR author's choice, so a changed default reaches a
cluster through its own image and never through this repository. The test pins, which Renovate
moves within a major (ADR 0017 D43), only decide what CI would notice.)*

## Options

- **A — render both as `no` in the data config ~~(best)~~,** in a release that rolls the data tier
  anyway, so the change costs no extra roll. *(Added 2026-09-27.)* Cost the line above missed:
  it rolls every data **and** every Sentinel tier (Fact), and there is no release on `main` that
  rolls the fleet anyway (`v1.13.1` is the latest, and only a docs commit follows it). On a
  `replicas: 1` cluster whose sidecar image does not move it deletes the only pod, and a
  non-persistent one loses its data (Fact). Its one unique gain is that the value no longer
  depends on the CR author's image. If taken, render `enable-protected-configs no` as well.
- **B — assert the defaults in `make test-image-tools`** (`CONFIG GET` on both pinned images)
  and render nothing: no roll, the directives stay implicit, and a changed default turns only
  that check red. *(Added 2026-09-27.)* The place is
  `TestRestrictedRuntime_ValkeyServerPersistsAndAnswers`
  ([`restricted_runtime_test.go:99`](../../test/imagetools/restricted_runtime_test.go)), which
  already starts `valkey-server` on every pinned image. Assert all three switches.
- **C — B now, A later, riding a release that moves the config hash of every cluster for its own
  reasons** *(added 2026-09-27)*. B's cost now, with A's cost deferred to a roll that happens
  anyway. The single-pod data-loss path of A remains when that day comes.

A is marked because a stated value does not depend on any image default, now or after a major
upgrade, and bundling it with a rolling release removes its only cost.

*(Re-weighed 2026-09-27; the mark above is superseded, not deleted.)* **B (recommended).** The
default is `no` and immutable at runtime on both pinned lines (Fact). A buys nothing on those
images, and it costs a roll of every data and Sentinel tier plus a data-loss path that
contradicts the premise of `singlePodDeferral`. B is one assertion in a test that already runs,
and it turns red on the Renovate PR that would bring a changed default. What B leaves open is a
CR author's own image, which A would cover. The threat needs the cluster password, and that
password already has every other right (gap H-6, T50), so this residual case is small. C is B
plus a promise with no carrier yet.

## Decision

None yet.

## Work list

1. **XS, no decision needed:** rewrite gap
   [H-7](../security/secrets-and-tls.md#h-7) (`docs/security/secrets-and-tls.md:198-203`). Its
   "Believed `no` since Redis 7; **not re-checked for either pinned Valkey line**" becomes the
   verified upstream-source default: `no`, immutable, at `9.1.1` and `8.1.9`, images not run. It
   also names `enable-protected-configs`. This is true under every option and does not close the
   ticket. **Done 2026-09-27** (History).
2. *(waits on the decision; B or C)* Add `valkey-cli config get enable-debug-command
   enable-module-command enable-protected-configs` to the script at
   `restricted_runtime_test.go:99`, and assert `no` for each. `make test-image-tools` is green.
   Mutation: expect `yes`, and the test goes red.
3. *(waits on the decision; A only)* Add three lines to the `# General` block
   (`configmap.go:116-123`), with a unit test. Revisit the premise at
   `pod_security_migration.go:122-126`. Ship only with a release that rolls the fleet anyway.
4. Close ([ADR 0034](../adr/0034-tickets-are-work-lists-that-get-archived.md)): record the
   decision, ADR 0016 or ADR 0033, whichever holds the Valkey config posture at the time. Rewrite
   H-7 to its final form, `git grep` `057` and `T57`, then move to `archive/`.

## Verification

- A unit test on the generated config asserts both lines. *(2026-09-27: A only.)*
- `make test-image-tools`, or an e2e step, reads `CONFIG GET enable-debug-command` and
  `CONFIG GET enable-module-command` from a running data pod on both pinned lines and gets `no`.
  *(2026-09-27: and `enable-protected-configs`. Under B, the mutation check in the work list
  applies.)*

## History

- 2026-09-27: work list item 1 landed, one file (read in `git diff` of the working tree):
  [`secrets-and-tls.md`](../security/secrets-and-tls.md) gap H-7 (`:198-209` now). "Believed `no`
  since Redis 7; **not re-checked for either pinned Valkey line**" is struck and corrected in
  place: on both pinned lines the default is `no`, `src/config.c` defines all three directives
  as `IMMUTABLE_CONFIG` with default `no` (lines 3375-3377 at `9.1.1`, 3267-3269 at `8.1.9`,
  re-fetched by the implementer that day), so `CONFIG SET` cannot switch them on; the builder
  renders none of the three (grep). An explicit **Not verified** names the images themselves
  (no `CONFIG GET` was run) and any other `spec.image`. The gap stays open, heading unchanged.
  The decision (B recommended) and items 2-4 are untouched; urgency `later`, severity and
  effort unchanged. **Not verified:** the upstream source was not re-read for this entry; the
  line numbers match the ones this ticket's review read the same day.
- 2026-09-27: reviewed - re-read upstream `src/config.c` at `9.1.1` and `8.1.9` (the three
  `createEnumConfig` lines hold as cited) and spot-checked the code locations; B stays
  recommended. The enrichment rewrote two frontmatter comments without keeping their old text,
  so it is recorded here verbatim: severity was `# believed off in both images; the gap is that
  nothing states it`, urgency was `# rule 4: two config lines, shipped with a release that rolls
  the data tier anyway`. Values unchanged.
- 2026-09-27: enriched - verified the default (`no`, immutable) in upstream source at both
  pinned tags. Corrected the roll scope to data and Sentinel tiers, and recorded the single-pod
  data-loss path of A. Added C, re-weighed to B (recommended), and added a work list with one
  decision-free XS item (H-7). `blocked-by: decision` added. The urgency comment now gives the
  rule-4 reason without assuming A, and the severity comment records the verified default.
  Urgency (`later`), severity (low) and effort (XS) are unchanged.
- 2026-09-27 — filed from the row "`enable-debug-command` / `enable-module-command` pinned to
  `no` in the generated config" of archive/031. Gap [H-7](../security/secrets-and-tls.md#h-7)
  states what is missing; its "open follow-up" lead-in was removed from the page in the same
  change.
