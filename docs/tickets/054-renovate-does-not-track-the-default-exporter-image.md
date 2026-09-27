---
id: T54
title: Renovate does not track DefaultMetricsExporterImage
state: filed
severity: low         # the pin ages; nothing is broken today
security: hardening
threat: "would additionally cover vulnerabilities fixed in redis_exporter after v1.66.0: today the default exporter, third-party code that holds the cluster password on every auth-enabled cluster with metrics on, ages until someone moves the pin by hand"
urgency: later        # rule 4: a cheap known fix once the option is chosen
effort: S
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
  [`CLAUDE.md`](../../CLAUDE.md) line 131,
  [`docs/operations/examples.md`](../operations/examples.md) line 235 and ADR 0033 D5.
- The exporter receives the cluster password as `REDIS_PASSWORD`
  ([`statefulset.go`](../../internal/builder/statefulset.go) line 1078).

**Not verified:**

- Which redis_exporter releases followed v1.66.0, and whether any of them fixes a vulnerability.
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

## Decision

None yet.

## Verification

- A Renovate dry run (or the dependency dashboard) lists `oliver006/redis_exporter` from
  `api/v1/valkey_types.go`.
- Revert check: with the manager's regex broken in a scratch copy of `renovate.json`, the same
  dry run no longer lists it.

## History

- 2026-09-27 — filed from the row "Renovate for `DefaultMetricsExporterImage`" of archive/031,
  which names ADR 0033's residual risks. Gap [H-17](../security/workload-pod-posture.md#h-17)
  states what is missing.
