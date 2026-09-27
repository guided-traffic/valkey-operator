---
id: T54
title: Renovate does not track DefaultMetricsExporterImage
state: filed
severity: low         # the pin ages; nothing is broken today
security: hardening
threat: "would additionally cover vulnerabilities fixed in redis_exporter after v1.66.0: today the default exporter, third-party code that holds the cluster password on every auth-enabled cluster with metrics on, ages until someone moves the pin by hand"
urgency: later        # rule 4: a cheap known fix once the option is chosen
effort: S
blocked-by: decision  # manual review, automerge or manual; then how the documentary copies follow
filed-from: T31, section "Further security measures — not in this change, each open" (archive/031)
opened: 2026-09-27
decided:              # not recorded - no decision yet
done:                 # not recorded - not done
---

Filed on 2026-09-27 from the seventh row of the table
["Further security measures — not in this change, each open"](archive/031-generated-pods-run-as-root.md#further-security-measures--not-in-this-change-each-open)
in the archived ticket 031, which is `done` and tracks none of its rows. The operator-facing
statement is gap [H-17](../security/workload-pod-posture.md#h-17).

## Fact

**Verified** (read 2026-09-27):

- `DefaultMetricsExporterImage` is the literal
  `oliver006/redis_exporter:v1.66.0@sha256:d98e6db8…` in
  [`api/v1/valkey_types.go`](../../api/v1/valkey_types.go) line 643
  ([ADR 0033](../adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md)
  D5).
- The six custom managers in [`renovate.json`](../../renovate.json) match `Makefile`,
  `Containerfile`, `go.mod`, `.github/release-template.hbs`, the workflows and
  `test/testimages/images.go`; none matches `api/v1/valkey_types.go`. ADR 0033's residual risks
  record the gap.
- The same reference is quoted in four documents: [`README.md`](../../README.md) line 368,
  [`CLAUDE.md`](../../CLAUDE.md) ~~line 131~~ *(corrected 2026-09-27: line 133)*,
  [`docs/operations/examples.md`](../operations/examples.md) line 235 and ADR 0033 D5.
- The exporter receives the cluster password as `REDIS_PASSWORD`
  ([`statefulset.go`](../../internal/builder/statefulset.go) line 1078).
- *(Added 2026-09-27, re-read at `4a7543e`.)* **A new custom manager would automerge unless it
  is told not to.** `packageRules[16]` ([`renovate.json:225-236`](../../renovate.json)) automerges
  minor, patch and digest updates for every `custom.regex` manager. Its description says
  "Makefile-pinned Go tools", but it matches by manager alone. `:automergeDigest` is extended at
  line 6. A v1.66.0 → v1.92.0 bump is a minor, so it would automerge. The precedent for the
  manager's shape is the sixth custom manager (~~`renovate.json:292ff`~~ *(corrected 2026-09-27 by
  the review: 292 opens the `customManagers` list; the sixth manager is `renovate.json:358-369`)*,
  on `test/testimages/images.go`),
  which reads a `// renovate: datasource=docker depName=…` comment above the constant.
  *(Precised 2026-09-27 by the review: that manager captures only `currentValue`, a tag, because
  the test pins carry no digest. The exporter pin needs `currentDigest` as well, so the regex is
  new, not a copy.)*
- *(Added 2026-09-27.)* Upstream, per the GitHub releases API (unauthenticated, 2026-09-27):
  v1.66.0 was published 2024-10-31, and the latest, v1.92.0, on 2026-09-23. The newest 100
  releases hold 30 non-prerelease releases after v1.66.0 (v1.67.0 … v1.92.0).
- *(Added 2026-09-27.)* Tests do not pin the value. `TestDefaultMetricsExporterImage_IsPinnedByDigest`
  checks only its shape ([`valkey_types_test.go:1367`](../../api/v1/valkey_types_test.go)), and
  `deepcopy_test.go:92` uses a tag-only fixture of its own.
- *(Added 2026-09-27.)* Five places state that the pin is maintained by hand, and they change on
  close: `README.md:368` ("It is not updated automatically"), `DEVELOPER.md:271`, `CLAUDE.md:1002`,
  ADR 0033 residual risks (lines 647-648) and gap H-17
  (`docs/security/workload-pod-posture.md:207-213`).
- *(Added 2026-09-27.)* **What CI checks of a bump:** the e2e enables metrics in
  `pod_security_test.go:134` and `pod_hardening_test.go:216`, so the exporter has to start under
  the restricted posture. A crash-looping exporter leaves the pod not Ready and fails those tests.
  No e2e reads `/metrics`: a grep for `/metrics`, `9121` and `redis_up` over `test/e2e/` finds
  nothing. A changed metric set or a broken auth therefore passes CI.

**Not verified:**

- ~~Which redis_exporter releases followed v1.66.0, and~~ *(corrected 2026-09-27: the releases
  are answered in Fact)* whether any of them fixes a vulnerability. No changelog was read and no
  scanner was run on either image.
- *(Added 2026-09-27.)* That Renovate puts one dependency, matched in several files by one
  manager, into a single PR. No dry run was made.
- That no built-in Renovate manager extracts a Go string constant. Inferred: `git log -S
  'redis_exporter:'` on the file finds only `28b6830` (2026-07-21, by hand), and no Renovate run
  was observed.

## Impact

Every metrics-enabled cluster that does not set `spec.metrics.image`. An operator can pin a
newer exporter per resource today with `spec.metrics.image`.

## Options

- **A — a custom regex manager on `api/v1/valkey_types.go`, automerge off (best).** Docker
  datasource, tag and digest both captured. A bump changes the exporter container of every
  metrics-enabled data pod template, so the operator upgrade that ships it rolls those tiers
  (a pod-spec change rides the rolling update,
  [ADR 0018](../adr/0018-metrics-and-the-exporter-sidecar.md)); a human should see that. The
  four documentary copies move with it: either the manager matches them too, or a unit test
  asserts they equal the constant.
- **B — the same manager with automerge.** Cheaper to run; a fleet roll lands on the strength
  of one green CI run.
- **C — stay manual**, with a comment at the constant saying so.

A is marked because it stops the silent aging and keeps a person in front of a change that
rolls every metrics-enabled data tier.

*(Refined 2026-09-27. There are two decisions: this one first, then how the copies follow.)*
**Decision 1: A (recommended)**, with a condition the option text did not state. A needs an
explicit `packageRules` entry, `matchDepNames: ["oliver006/redis_exporter"]` with
`automerge: false`, placed after `renovate.json:225-250`, because later rules win. Without it
`packageRules[16]` turns A into B (Fact). The review matters beyond the roll, too. CI proves
only that the exporter starts (Fact), so a reviewer reading the upstream changelog is the only
check of the metric set. The first PR spans 30 releases. T45 puts the same decision to other
pins in `renovate.json`, so one answer and one change can cover both tickets.

**Decision 2 (only for A or B): how the four documentary copies follow.**

- **(a) the manager also matches `README.md`, `CLAUDE.md` and `docs/operations/examples.md`**,
  with the same regex (tag and full digest), so one PR moves all four. ADR 0033 D5 (line 266)
  quotes a truncated digest that the regex does not match. It is rephrased to name the constant
  instead of stating its value as current, so Renovate never edits decision prose.
  **(recommended)**: the copies cannot drift, and nothing new has to be maintained.
- **(b) a unit test asserts each document contains `DefaultMetricsExporterImage`.** This keeps
  the documents honest, but every Renovate PR goes red until someone edits three files by hand.
  That gives back most of what the manager buys.
- **(c) drop the literal from the documents** and point at the constant. The README's CRD
  reference is where a default is shown (CLAUDE.md, "Documentation has five homes"), so a reader
  loses the value.

## Decision

None yet.

## Work list

No item here is both XS and free of the decisions above.

1. *(waits on Decision 1)* A `// renovate: datasource=docker depName=oliver006/redis_exporter`
   comment above [`valkey_types.go:643`](../../api/v1/valkey_types.go), a custom manager
   capturing `currentValue` and `currentDigest`, and the `automerge: false` rule (Options).
2. *(waits on Decision 2)* The file patterns of (a), and the ADR 0033 D5 rephrase.
3. *(waits on both)* `renovate-config-validator` plus a local dry run, and the revert check
   (Verification).
4. Close ([ADR 0034](../adr/0034-tickets-are-work-lists-that-get-archived.md)): ADR 0033 D5
   (from line 262) and its residual risk (647-648) amended, the five hand-maintenance statements
   in Fact rewritten, `git grep` `054` and `T54`, then move to `archive/`. The first Renovate PR
   (v1.66.0 → v1.92.0) is not part of this ticket. It rolls every metrics-enabled data tier on
   the release that ships it ([ADR 0018](../adr/0018-metrics-and-the-exporter-sidecar.md)).

## Verification

- A Renovate dry run (or the dependency dashboard) lists `oliver006/redis_exporter` from
  `api/v1/valkey_types.go`.
- Revert check: with the manager's regex broken in a scratch copy of `renovate.json`, the same
  dry run no longer lists it.

## History

- 2026-09-27: reviewed - spot-checked the new line numbers at `4a7543e`. Corrected the location of
  the precedent manager (358-369, not 292ff) and noted that it captures no digest. Frontmatter and
  recommendations unchanged.
- 2026-09-27: enriched - corrected the CLAUDE.md line and answered which releases followed
  v1.66.0 (30, the latest from 2026-09-23). Found the custom-regex automerge rule that would turn
  A into B, and that CI never reads `/metrics`. Added Decision 2 (the copies, (a) recommended), a
  work list and `blocked-by: decision`. Urgency (`later`, rule 4: severity stays low, no
  vulnerability verified) and effort (S) unchanged.
- 2026-09-27 — filed from the row "Renovate for `DefaultMetricsExporterImage`" of archive/031,
  which names ADR 0033's residual risks. Gap [H-17](../security/workload-pod-posture.md#h-17)
  states what is missing.
