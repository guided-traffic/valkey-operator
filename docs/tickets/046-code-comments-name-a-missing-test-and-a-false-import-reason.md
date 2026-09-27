---
id: T46
title: code comments name a test that does not exist and an import reason that does not hold
state: filed
severity: cosmetic    # comments only; no behaviour depends on them
security: none
urgency: now          # rule 1: measured-false statements in tracked files
effort: XS
filed-from: the documentation restructure of 2026-09-27 (package map and extension checklists)
opened: 2026-09-27
decided:              # not recorded - no decision yet
done:                 # not recorded - not done
---

Filed on 2026-09-27 from two findings of the documentation restructure, which checked the
contributor pages against the code. Both are comments that state something the code refutes. Read
in the working tree of `feat/rootless` on 2026-09-27; no code was changed for this file.

## Fact

**Verified (read):**

1. **A test name that never existed.**
   [`internal/builder/image_requirements.go:12`](../../internal/builder/image_requirements.go)
   says the list "is kept honest from the other side too:
   `TestRequiredImageTools_MatchesTheGeneratedScripts` walks every exec command …". No test of
   that name exists; the guard is `TestRequiredImageTools_CoversTheGeneratedScripts`
   ([`image_requirements_test.go:132`](../../internal/builder/image_requirements_test.go)). The
   comment and the test arrived together in `f5f3256` (2026-08-22), and the test carried the
   `Covers…` name in that commit already, so the comment was wrong from the day it was written.
   `DEVELOPER.md` and [ADR 0017](../adr/0017-test-and-ci-policy.md) use the correct name; outside
   `docs/tickets/`, a grep for the comment's name finds only the comment.
2. **An import reason that does not hold.**
   [`internal/common/annotations.go:14-17`](../../internal/common/annotations.go) says
   `AnnotationDrainPromotedAt` lives in `internal/common` because it "is the only package both
   internal/sidecar and internal/controller already import; putting it in internal/builder would
   pull the whole API type tree into the sidecar binary for one string".
   [`internal/common/drain.go:21-23`](../../internal/common/drain.go) repeats the reason for the
   drain-signal constants. Three things refute it:
   - [`internal/common/labels.go:9`](../../internal/common/labels.go) imports `api/v1` and has
     since `0aaa3a2` (2026-02-17), six months before the comment (`cc7e034`, 2026-08-21). Importing
     `internal/common` already pulls in the API types.
   - There is no separate sidecar binary. [`cmd/main.go:129-133`](../../cmd/main.go) dispatches
     `sidecar` as a subcommand of the one `manager` binary, which the
     [`Containerfile`](../../Containerfile) builds from `./cmd/main.go` and which also links
     `internal/controller`.
   - `internal/common` is not the only package both import: `internal/sidecar/drain.go` and
     `labeler.go` import `internal/valkeyclient`, and so do `internal/controller/rolling_update.go`
     and `valkey_controller.go`.
3. **A verb list that stopped holding.** *(Added 2026-09-27, the same family: a comment the code
   refutes.)* The `BuildSidecarRole` doc comment,
   [`internal/builder/rbac.go:38-42`](../../internal/builder/rbac.go), says "patch is the only verb
   the sidecar calls — patchMetadata in internal/sidecar/labeler.go is the package's single
   clientset call site — so nothing else is granted"; its opening sentence at `:35` says the Role
   "grants patch access". The unit test's comment,
   [`internal/builder/rbac_test.go:58-59`](../../internal/builder/rbac_test.go), opens with "patch is
   the only verb the sidecar calls" and calls a reintroduced `get` a silent widening. The code
   says otherwise:
   - The Role grants `get` and `patch` on the named pods (`rbac.go:73`), and the same test asserts
     exactly that list (`rbac_test.go:68`), right under the comment that rules `get` out.
   - `IsTerminating` calls `Pods(namespace).Get`
     ([`internal/sidecar/labeler.go:243`](../../internal/sidecar/labeler.go)), a second clientset
     call site next to the `Patch` in `patchMetadata` (`:269`).
   - Both comments arrived in `44a974a` (2026-08-21); `get` was added by `e32f0d2` (2026-08-27) for
     the drain handler's terminating-peer check, which changed the verb list and the test assertion
     and left both comments standing.

**Adjacent, same comment block, not counted as a finding:** `image_requirements.go:5` says "both
init containers are shell scripts". Since ADR 0032 a persistent data pod also runs the
`check-data-writable` pre-flight and, during the migration, `fix-data-ownership`
([`pod_security.go:43`, `:49`](../../internal/builder/pod_security.go)), besides
`init-config-selector` and the Sentinel tier's `init-sentinel-config`. Whether "both" meant only
the two config writers cannot be decided from the text; rewrite it if the block is touched.

**Not verified:**

- Whether keeping the constants in `internal/common` has another, valid reason (for example the
  import direction between packages). Nothing in the code states one; the fix below does not need
  one.

## Impact

A reader who greps the named test finds nothing and may conclude the guard was removed. A reader
who believes the import reason may keep other constants out of `internal/builder`, or move things
into `internal/common`, to protect a binary boundary that does not exist. No behaviour depends on
either comment.

## Options

No decision is open: correct the test name at `image_requirements.go:12`, and replace the stated
reason in `annotations.go` and `drain.go` with what holds (the constants are shared by the sidecar
and the builder or controller, and `internal/common` is where the other shared names live) or drop
the sentence. *(Added 2026-09-27, finding 3:)* rewrite the verb sentences at
`rbac.go:35-42` and `rbac_test.go:58-59` to name `get` and `patch` and what each is for (`patch` for
the label and the drain stamp, `get` for `IsTerminating`); the rest of each comment (no `list`,
no empty `resourceNames` list) was not re-checked for this finding.

## Decision

None recorded. The fix has no alternative worth weighing, so the go-ahead is all that is open.

## Verification

- `grep -rn 'MatchesTheGeneratedScripts' --include='*.go' .` returns nothing.
- `grep -rn 'API type tree' internal/common` returns nothing, or only a sentence consistent with
  `labels.go` importing `api/v1`.
- `grep -rn 'only verb the sidecar calls' internal/` returns nothing (finding 3, added
  2026-09-27).
- `make lint` green.

## History

- 2026-09-27 — filed from the documentation restructure. The third refutation (the shared
  `internal/valkeyclient` import) and the adjacent init-container sentence were found while
  verifying.
- 2026-09-27 — finding 3 added: the sidecar Role comments in `internal/builder/rbac.go` and
  `rbac_test.go` still say `patch` is the only verb, since `e32f0d2` granted and used `get` too.
  Verified by reading the Role, the test assertion and `internal/sidecar/labeler.go`; no code was
  changed. The opening paragraph's "two findings" describes the filing, not the count.
