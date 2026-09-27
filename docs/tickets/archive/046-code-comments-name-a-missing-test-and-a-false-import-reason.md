---
id: T46
title: code comments name a test that does not exist and an import reason that does not hold
state: done
severity: cosmetic    # comments only; no behaviour depends on them
security: none
urgency: now          # rule 1: measured-false statements in tracked files
effort: XS
filed-from: the documentation restructure of 2026-09-27 (package map and extension checklists)
opened: 2026-09-27
decided:              # not recorded - no decision was needed (the fix had no alternative)
done: 2026-09-27
---

Filed on 2026-09-27 from two findings of the documentation restructure, which checked the
contributor pages against the code. Both are comments that state something the code refutes. Read
in the working tree of `feat/rootless` on 2026-09-27; no code was changed for this file.
*(Re-read 2026-09-27 at `4a7543e` on `chore/maintenance-2026-09-27`: every line cited below still
holds.)* *(Closing note 2026-09-27: every comment quoted below was corrected later that day, so
the quotes describe `4a7543e`, not the tree after the fix; see History.)*

## Fact

**Verified (read):**

1. **A test name that never existed.**
   [`internal/builder/image_requirements.go:12`](../../../internal/builder/image_requirements.go)
   ~~says~~ *(said until 2026-09-27; fixed, see History)* the list "is kept honest from the other side too:
   `TestRequiredImageTools_MatchesTheGeneratedScripts` walks every exec command …". No test of
   that name exists; the guard is `TestRequiredImageTools_CoversTheGeneratedScripts`
   ([`image_requirements_test.go:132`](../../../internal/builder/image_requirements_test.go)). The
   comment and the test arrived together in `f5f3256` (2026-08-22), and the test carried the
   `Covers…` name in that commit already, so the comment was wrong from the day it was written.
   `DEVELOPER.md` and [ADR 0017](../../adr/0017-test-and-ci-policy.md) use the correct name; outside
   `docs/tickets/`, a grep for the comment's name finds only the comment.
2. **An import reason that does not hold.**
   [`internal/common/annotations.go:14-17`](../../../internal/common/annotations.go) ~~says~~
   *(said until 2026-09-27; fixed, see History)* `AnnotationDrainPromotedAt` lives in `internal/common` because it "is the only package both
   internal/sidecar and internal/controller already import; putting it in internal/builder would
   pull the whole API type tree into the sidecar binary for one string".
   [`internal/common/drain.go:21-23`](../../../internal/common/drain.go) ~~repeats~~ *(repeated
   until 2026-09-27; fixed, see History)* the reason for the drain-signal constants. Three things refute it:
   - [`internal/common/labels.go:9`](../../../internal/common/labels.go) imports `api/v1` and has
     since `0aaa3a2` (2026-02-17), six months before the comment (`cc7e034`, 2026-08-21). Importing
     `internal/common` already pulls in the API types.
   - There is no separate sidecar binary. [`cmd/main.go:129-133`](../../../cmd/main.go) dispatches
     `sidecar` as a subcommand of the one `manager` binary, which the
     [`Containerfile`](../../../Containerfile) (`:29-32`) builds from `./cmd/main.go` and which also links
     `internal/controller`.
   - `internal/common` is not the only package both import: `internal/sidecar/drain.go` and
     `labeler.go` import `internal/valkeyclient`, and so do `internal/controller/rolling_update.go`
     and `valkey_controller.go`.
3. **A verb list that stopped holding.** *(Added 2026-09-27, the same family: a comment the code
   refutes.)* The `BuildSidecarRole` doc comment,
   [`internal/builder/rbac.go:38-42`](../../../internal/builder/rbac.go), ~~says~~ *(said until
   2026-09-27; fixed, see History)* "patch is the only verb
   the sidecar calls — patchMetadata in internal/sidecar/labeler.go is the package's single
   clientset call site — so nothing else is granted"; its opening sentence at `:35` says the Role
   "grants patch access". The unit test's comment,
   [`internal/builder/rbac_test.go:58-59`](../../../internal/builder/rbac_test.go), ~~opens~~
   *(opened until 2026-09-27; fixed, see History)* with "patch is
   the only verb the sidecar calls" and calls a reintroduced `get` a silent widening. The code
   says otherwise:
   - The Role grants `get` and `patch` on the named pods (`rbac.go:73`), and the same test asserts
     exactly that list (`rbac_test.go:68`), right under the comment that rules `get` out.
   - `IsTerminating` calls `Pods(namespace).Get`
     ([`internal/sidecar/labeler.go:243`](../../../internal/sidecar/labeler.go)), a second clientset
     call site next to the `Patch` in `patchMetadata` (`:269`).
   - Both comments arrived in `44a974a` (2026-08-21); `get` was added by `e32f0d2` (2026-08-27) for
     the drain handler's terminating-peer check, which changed the verb list and the test assertion
     and left both comments standing.

**Adjacent, same comment block, not counted as a finding:** `image_requirements.go:5` ~~says~~
*(said until 2026-09-27; fixed, see History)* "both init containers are shell scripts". Since ADR 0032 a persistent data pod also runs the
`check-data-writable` pre-flight and, during the migration, `fix-data-ownership`
([`pod_security.go:43`, `:49`](../../../internal/builder/pod_security.go)), besides
`init-config-selector` and the Sentinel tier's `init-sentinel-config`. Whether "both" meant only
the two config writers cannot be decided from the text; rewrite it if the block is touched.
*(2026-09-27: the fix of finding 1 touches that block, so the rewrite is now a work item. All four
run `sh -c`: `init-config-selector`
([`statefulset.go:275`, `:436`](../../../internal/builder/statefulset.go), one per builder),
`init-sentinel-config` ([`sentinel.go:342`](../../../internal/builder/sentinel.go)),
`check-data-writable` and `fix-data-ownership`
([`pod_security.go:183`, `:217`](../../../internal/builder/pod_security.go)). Naming them is true
whatever "both" meant.)*

**Adjacent, found 2026-09-27 while enriching, same family (verified by reading):**

- [`rbac.go:40-42`](../../../internal/builder/rbac.go), the same doc comment as finding 3 *(fixed
  2026-09-27, see History)*: "Dropping
  the unused get/list was the precondition for the resourceNames restriction below, which is
  incompatible with list". `get` is granted again (`:73`), and it is compatible with
  `resourceNames`; only `list` is not. The sentence has to say that `list` stays dropped.
- [`rbac.go:80`](../../../internal/builder/rbac.go) *(fixed 2026-09-27, see History)*:
  `SidecarRolePodNames` "returns the data-pod names the sidecar Role grants patch on". The Role grants `get` and `patch` on them. The sentence
  is incomplete, not false, and belongs to the same rewrite.
- [ADR 0012 `:349-350`](../../adr/0012-the-sidecar-records-its-drain-promotion-on-the-pod.md) ~~says~~
  *(said until 2026-09-27; struck and corrected in place, see History)* `internal/common/drain.go` carries both constants "for the reason D2 gives for the annotation".
  D2 (`:134-138`) gives no placement reason. `git log -S'API type tree'` finds the phrase only in
  `cc7e034` and `bb0f127` (the Go comments) and `4a7543e` (this ticket), never in an ADR. So the
  ADR points at the false comment's reason, and it is corrected in the same change.
- The correct statements already exist elsewhere, so nothing has to be extracted.
  [`package-map.md:79`](../../developer/package-map.md) gives the neutral placement reason.
  [`privilege-footprint.md:68-70`](../../security/privilege-footprint.md#the-per-instance-sidecar-role)
  names `Patch` and `Get`. ADR 0012 Status ("Also amended 2026-08-27") records the `get` widening.
- Who uses the constants (grep, non-test files): `AnnotationDrainPromotedAt` is used in
  `internal/sidecar/drain.go` and `internal/controller/steady_state_master.go`, and
  `DrainSignalMountPath` / `DrainCompleteFile` in `internal/sidecar/drain.go` and
  `internal/builder/statefulset.go`. The replacement text below is written against that list.

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
*(2026-09-27: re-checked against the code. `list` is absent from `:73`, and the empty-names
branch at `:57-63` returns no rule at all. Both statements hold and stay.)*

## Work list (2026-09-27)

All items are **XS and need no decision**. Together they close the ticket. They change only
comments and one ADR phrase, so no behaviour changes.

1. [`image_requirements.go:12`](../../../internal/builder/image_requirements.go): replace
   `TestRequiredImageTools_MatchesTheGeneratedScripts` with
   `TestRequiredImageTools_CoversTheGeneratedScripts`. **Done 2026-09-27.**
2. [`image_requirements.go:5`](../../../internal/builder/image_requirements.go): replace "both init
   containers are shell scripts" with "the config-writer init containers of both tiers, the
   data-volume pre-flight and the ownership repair are shell scripts". **Done 2026-09-27.**
3. [`annotations.go:14-17`](../../../internal/common/annotations.go): replace the reason with one
   that holds: the sidecar writes the key and the controller reads it, and `internal/common` is
   where the other names both sides share live. **Done 2026-09-27.**
4. [`drain.go:21-23`](../../../internal/common/drain.go): the constants live next to
   `AnnotationDrainPromotedAt` because the sidecar and `internal/builder` both use them. Drop
   "without pulling the API type tree into the sidecar binary". **Done 2026-09-27.**
5. [`rbac.go:35-43`](../../../internal/builder/rbac.go): say that the Role grants `get` and
   `patch`. `patch` sets the `instanceRole` label and the drain stamp (`patchMetadata`,
   `labeler.go:269`). `get` is for `IsTerminating` (`labeler.go:243`, ADR 0028 D5a). `list`
   stays dropped because it is incompatible with `resourceNames`. Drop "single clientset call
   site". Also `:80`: "grants patch on" becomes "grants get and patch on". **Done 2026-09-27**,
   with one deviation: the opening sentence ("grants patch access … on a peer pod") is kept and
   a second sentence adds the `get` grant and its purpose, instead of rewriting the first.
6. [`rbac_test.go:58-60`](../../../internal/builder/rbac_test.go): "get and patch are the only verbs
   the sidecar calls". A reintroduced `list`, or any verb beyond these two, is what would widen
   the grant silently. Keep the rest of the comment. **Done 2026-09-27** ("an added verb such as
   list would widen the grant silently").
7. [ADR 0012 `:349-350`](../../adr/0012-the-sidecar-records-its-drain-promotion-on-the-pod.md):
   strike "for the reason D2 gives for the annotation" in place, with
   "*(corrected 2026-09-27: D2 gives no placement reason; the constants sit in internal/common
   because the sidecar and the builder both use them)*". A reference correction changes no
   decision, so the Status section needs no amendment line. **Done 2026-09-27**, and a dated
   "Corrected 2026-09-27 (no decision changes)" line was added to the ADR 0012 Status anyway; it
   does not repeat the struck phrase.

**Close (ADR 0034):** no decision to extract, because the true statements already live in
`package-map.md:79`, `privilege-footprint.md:68-70` and the ADR 0012 Status. Set `state: done`,
stamp `done:` and add a "what shipped" History line. `git grep -nE 'T46\b|046-'` outside
`docs/tickets/` is empty today, so nothing needs clearing. Then `git mv` into `archive/`.
*(2026-09-27: done; moved with a plain `mv`, no git state was changed - the owner stages the
move. See History.)*

## Appendix 2026-09-27: `secretConcernsValkey` called the TLS fingerprint an annotation

Same family: a comment the code refutes. Found and fixed the same day, in the change that
carried the work list.

**Verified (read):** the doc comment of `secretConcernsValkey`
([`internal/controller/valkey_controller.go`](../../../internal/controller/valkey_controller.go),
`:3049` at `4a7543e`) said "the fingerprint annotation is what turns that reconcile into a roll".
Since [ADR 0031](../../adr/0031-a-record-the-operator-trusts-lives-in-pod-spec.md) the record is
the `VKO_TLS_MATERIAL_HASH` env var in the pod spec: `StampTLSMaterialHash`
([`internal/builder/tls_material.go:79`](../../../internal/builder/tls_material.go)) writes it, and
`RecordedTLSMaterialHash` (`:119`) reads the env first and the superseded annotation only as a
fallback. The comment now names the env var and ADR 0031. Comment only.

**Not verified:** nothing was run; `make lint` was not run for this change.

**Left open, outside this ticket:** the implementer of this change found three more Go comments
that still call the fingerprint an annotation (`rolling_update.go` `podNeedsUpdate`,
`tls_material.go:48` `ComputeTLSMaterialHash`, `api/v1/valkey_types.go` the
`ConditionTypeTLSMaterialStale` doc comment). They were not part of this work list and are not
recorded here, because this file is archived; they were handed back for filing. *(2026-09-27: filed and fixed the same day as
[063](063-go-comments-still-call-the-tls-fingerprint-an-annotation.md).)*

## Decision

None recorded. The fix has no alternative worth weighing, so the go-ahead is all that is open.
*(2026-09-27: the go-ahead was given and the work list landed; see History.)*

## Verification

- `grep -rn 'MatchesTheGeneratedScripts' --include='*.go' .` returns nothing.
- `grep -rn 'API type tree' internal/common` returns nothing, or only a sentence consistent with
  `labels.go` importing `api/v1`.
- `grep -rn 'only verb the sidecar calls' internal/` returns nothing (finding 3, added
  2026-09-27).
- *(Added 2026-09-27, the adjacent items:)* `grep -rn 'both init containers\|Dropping the unused
  get' internal/builder` returns nothing. `git grep -n 'for the reason D2 gives' docs/adr`
  returns only the struck text of the in-place correction.
- `make lint` green.
- *(Run 2026-09-27, on the working tree after the fix:)* `grep -rn 'MatchesTheGeneratedScripts'
  --include='*.go' .`, `grep -rn 'API type tree' internal/common`, `grep -rn 'only verb the
  sidecar calls' internal/` and `grep -rn 'both init containers\|Dropping the unused get'
  internal/builder` return nothing; `git grep -n 'for the reason D2 gives' docs/adr` returns only
  the struck text at ADR 0012 `:355`. **Not run:** `make lint` (no make target was run in the
  closing pass); a comment-only change cannot change what compiles, but lint also checks
  comment formatting.

## History

- 2026-09-27: **done** - shipped the corrected comments in `image_requirements.go`,
  `annotations.go`, `drain.go`, `rbac.go`, `rbac_test.go` and, from the appendix,
  `valkey_controller.go`, and the ADR 0012 D10 reference; archived the same day.
- 2026-09-27: work list items 1-7 and the appendix landed, file by file (read in `git diff` of
  the working tree, not taken from the implementers' reports):
  - [`internal/builder/image_requirements.go`](../../../internal/builder/image_requirements.go):
    the test name is `TestRequiredImageTools_CoversTheGeneratedScripts` (item 1); "both init
    containers" became "the config-writer init containers of both tiers, the data-volume
    pre-flight and the ownership repair" (item 2).
  - [`internal/common/annotations.go`](../../../internal/common/annotations.go): the placement
    reason is now "both sides use it: the sidecar drain handler writes it and the controller
    reads it, and internal/common is where the other names both sides share (the labels) live"
    (item 3).
  - [`internal/common/drain.go`](../../../internal/common/drain.go): "These constants live in
    internal/common next to AnnotationDrainPromotedAt: the sidecar and internal/builder both use
    them"; the API-type-tree clause is gone (item 4).
  - [`internal/builder/rbac.go`](../../../internal/builder/rbac.go): a new sentence names the `get`
    grant for the terminating-candidate check (ADR 0028 D5a); "get and patch are the only verbs
    the sidecar calls — IsTerminating and patchMetadata … are its two clientset call sites";
    "list stays dropped: it is incompatible with the resourceNames restriction";
    `SidecarRolePodNames` "grants get and patch on" (item 5).
  - [`internal/builder/rbac_test.go`](../../../internal/builder/rbac_test.go): "get and patch are the
    only verbs the sidecar calls … an added verb such as list would widen the grant silently"
    (item 6).
  - [ADR 0012](../../adr/0012-the-sidecar-records-its-drain-promotion-on-the-pod.md) D10: ", for the
    reason D2 gives for the annotation" struck and corrected in place, plus a dated Status line
    (item 7).
  - [`internal/controller/valkey_controller.go`](../../../internal/controller/valkey_controller.go)
    `secretConcernsValkey`: "the fingerprint recorded in the pod spec (VKO_TLS_MATERIAL_HASH,
    ADR 0031)" (appendix, added to this file the same day).

  **Close ([ADR 0034](../../adr/0034-tickets-are-work-lists-that-get-archived.md)):** nothing
  durable to extract - the true statements already live in
  [`package-map.md:79`](../../developer/package-map.md) (the placement reason),
  [`privilege-footprint.md:68-70`](../../security/privilege-footprint.md#the-per-instance-sidecar-role)
  (`Get` and `Patch`) and the ADR 0012 Status ("Also amended 2026-08-27", the `get` widening);
  the corrections decide nothing. `git grep` of `T46`, `046` and `046-` outside `docs/tickets/`
  on 2026-09-27: no citation (one raw hit for `046`, a replication id inside a log line in
  `test/e2e/tls_log_filter_test.go:41`, is not one). Moved to `archive/` with a plain `mv`, and
  its relative links rewritten for the new depth. **Not verified:** `make lint` was not run.
- 2026-09-27: adversarial review of the enrichment. History reordered newest first (the
  "finding 3 added" entry had stood below "filed" since before the enrichment; no entry changed).
  Spot-checked and holding: `image_requirements.go:4-6`, `:12`, `image_requirements_test.go:132`,
  `annotations.go:14-17`, `drain.go:21-23`, `labels.go:9`, `cmd/main.go:129-133`,
  `Containerfile:29-32`, `rbac.go:35-43`, `:73`, `:80`, `rbac_test.go:58-68`, `labeler.go:243`,
  `:269`, `pod_security.go:43`, `:49`, `:183`, `:217`, `statefulset.go:275`, `:436`,
  `sentinel.go:342`, `package-map.md:79`, `privilege-footprint.md:68-70`, ADR 0012 `:134-138` and
  `:349-350`, the `internal/valkeyclient` imports and the users of the three constants. The work
  list is confirmed as XS with no decision: comments and one ADR reference only, no behaviour
  change, no cluster run needed. Frontmatter unchanged.
- 2026-09-27: enriched. Re-verified at `4a7543e`. Three adjacent items added: the stale
  `get/list` sentence at `rbac.go:40-42` together with `:80`, and the ADR 0012 `:349-350`
  reference to a reason D2 never gave. The init-container sentence moved into the work list, and
  the work list now marks every item as XS with no decision needed. Frontmatter unchanged:
  urgency `now` (rule 1), effort XS.
- 2026-09-27 — finding 3 added: the sidecar Role comments in `internal/builder/rbac.go` and
  `rbac_test.go` still say `patch` is the only verb, since `e32f0d2` granted and used `get` too.
  Verified by reading the Role, the test assertion and `internal/sidecar/labeler.go`; no code was
  changed. The opening paragraph's "two findings" describes the filing, not the count.
- 2026-09-27 — filed from the documentation restructure. The third refutation (the shared
  `internal/valkeyclient` import) and the adjacent init-container sentence were found while
  verifying.
