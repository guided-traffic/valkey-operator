---
id: T57
title: enable-debug-command and enable-module-command follow the image default
state: filed
severity: low         # believed off in both images; the gap is that nothing states it
security: hardening
threat: "would additionally cover an authenticated client — anything holding the one cluster password — running DEBUG or MODULE LOAD, should an image default ever enable them: today the generated config renders neither directive, so the image decides"
urgency: later        # rule 4: two config lines, shipped with a release that rolls the data tier anyway
effort: XS
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
  directive rolls every data tier on the operator upgrade that ships it.

**Not verified:**

- The default of either directive in the two pinned images
  ([`test/testimages/images.go`](../../test/testimages/images.go)); believed `no` since
  Redis 7.
- Whether Sentinel's config accepts either directive.

## Impact

None today if the believed default holds. The pin follows the image across Valkey majors
([ADR 0017](../adr/0017-test-and-ci-policy.md) D43), so a changed default would arrive without
a change in this repository.

## Options

- **A — render both as `no` in the data config (best),** in a release that rolls the data tier
  anyway, so the change costs no extra roll.
- **B — assert the defaults in `make test-image-tools`** (`CONFIG GET` on both pinned images)
  and render nothing: no roll, the directives stay implicit, and a changed default turns only
  that check red.

A is marked because a stated value does not depend on any image default, now or after a major
upgrade, and bundling it with a rolling release removes its only cost.

## Decision

None yet.

## Verification

- A unit test on the generated config asserts both lines.
- `make test-image-tools`, or an e2e step, reads `CONFIG GET enable-debug-command` and
  `CONFIG GET enable-module-command` from a running data pod on both pinned lines and gets `no`.

## History

- 2026-09-27 — filed from the row "`enable-debug-command` / `enable-module-command` pinned to
  `no` in the generated config" of archive/031. Gap [H-7](../security/secrets-and-tls.md#h-7)
  states what is missing; its "open follow-up" lead-in was removed from the page in the same
  change.
