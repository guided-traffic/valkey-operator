---
id: T61
title: non-English comments and prose in tracked files
state: done
severity: cosmetic    # no behaviour depends on a comment or on the ideas file
security: none
urgency: later        # rule 4: a cheap known fix (translate in place). Rule 1 does not match: a German comment breaks the language policy but states nothing false
effort: XS
filed-from: ticket triage of 2026-09-27
opened: 2026-09-27
decided:              # not recorded - no decision was needed (translate in place)
done: 2026-09-27
---

Filed on 2026-09-27 from the ticket triage of that day. It found German comments in
`.github/workflows/build.yml`, against the language policy in
[`CLAUDE.md`](../../../CLAUDE.md) (lines 5-7: "All code, comments, commit messages, documentation,
and CRD fields in this repository **must be written in English**"). The sweep below extends that
finding to the whole tracked tree. Read at `4a7543e` on `chore/maintenance-2026-09-27`.
*(Closing note 2026-09-27: every German line below was translated in place later that day, so
the tables and quotes describe `4a7543e`, not the tree after the fix; see History.)*

## Fact

**Verified:**

- **Nine German comment lines in the chart publish job** *(translated 2026-09-27, see History)*
  ([`build.yml`](../../../.github/workflows/build.yml), job `release-helm-gh`). All come from
  `25483b2` (2026-02-17, per `git blame`):

  | Line | Text |
  |---|---|
  | 175 | `# Extrahiere Version aus dem Release-Tag (ohne 'v' Prefix)` |
  | 178 | `# Aktualisiere Chart und App Version` |
  | 181 | `# Aktualisiere Image Tag in values.yaml` |
  | 190 | `# Erstelle temporären Index für das neue Chart` |
  | 213 | `# Kopiere neue Charts ins Root-Verzeichnis` |
  | 216 | `# Merge mit existierendem index.yaml falls vorhanden` |
  | 218 | `# Erstelle temporären Ordner für Merge` |
  | 221 | `# Merge mit existierendem Index` |
  | 226 | `# Erstelle neuen Index falls keiner existiert` |

- **Two German comment lines in the Renovate workflow** *(translated 2026-09-27, see History)*
  ([`renovate.yml`](../../../.github/workflows/renovate.yml)): line 3, `# Täglich um 2 Uhr morgens
  Berlin ausführen` (last edited in `87254ad`, 2026-09-06), and line 7, `# Manueller Trigger`
  (`25483b2`).
- **One file entirely in German** *(translated in place 2026-09-27, see History)*:
  [`.github/idea.md`](../../../.github/idea.md), nine lines
  (`c906950`, 2026-02-28). It holds a heading and four free-form wishes, on lines 1, 3, 5, 7 and 9.
  Three tracked files point at it by path, and none quotes its text:
  [`DEVELOPER.md:91`](../../../DEVELOPER.md),
  [ADR 0016](../../adr/0016-authentication-and-tls-posture.md) line 263 and
  [`rotation-and-change-propagation.md:48`](../../security/rotation-and-change-propagation.md). A
  translation in place keeps all three valid.
- **How the sweep ran:** over `git ls-files`, 308 files, leaving out `docs/tickets/`,
  `zz_generated*`, `go.sum`, `package-lock.json` and images. It had three passes, each run in
  Python over UTF-8 text: (1) every line with `ä ö ü Ä Ö Ü ß`; (2) every line with a German
  function or verb word (`und`, `nicht`, `falls`, `wird`, `oder`, `für`, `mit`, `Erstelle`,
  `Aktualisiere` and similar); (3) every word of every line that is missing from
  `/usr/share/dict/words` and has German morphology or an umlaut. Each hit was read by hand. The
  false positives are dropped: "falls" (English "falls back"; `git grep -nw falls` gives 75
  lines, and only `build.yml:216` and 226 among them are German), emoji in workflow echo lines,
  and English words the dictionary lacks. Only the hits above remain.

**Not verified:**

- A German comment made only of words that are also English or that the dictionary knows (for
  example a bare `# Merge Index`) passes all three passes. `renovate.yml:7` was missed by the
  first two passes and caught by the third.
- `docs/tickets/` was not swept, as the triage asked. Commit messages (for example `25483b2`,
  "feat: projekt structure") are history, not tracked files, and are out of scope.

## Impact

Cosmetic. No behaviour depends on these lines. A reader without German loses the intent of the
publish steps in `build.yml` and of the four product wishes in `idea.md`. ADR 0016 and the
security page cite `idea.md` as the place where a wish is recorded, so that wish cannot be read
there today.

## Decision

None needed: translate in place. The language policy decides the language, and a translation
changes no meaning, so no option is open. *Not proposed:* a CI check against non-English text. All
three files date from the first weeks of the repository (`25483b2`, `c906950`), and nothing
since has added German. Whether the four wishes in `idea.md` should become tickets under the
filing rule is a separate question, and the close does not need it.

## Work list

Items 1 and 2 are XS and need no decision. With the close in item 3 they finish the ticket.

1. **Translate the eleven workflow comments in place**, wording as proposed:
   - `build.yml:175` → `# Extract the version from the release tag (without the 'v' prefix)`
   - `build.yml:178` → `# Update the chart version and the app version`
   - `build.yml:181` → `# Update the image tag in values.yaml`
   - `build.yml:190` → `# Create a temporary index for the new chart`
   - `build.yml:213` → `# Copy the new charts into the root directory`
   - `build.yml:216` → `# Merge with the existing index.yaml, if there is one`
   - `build.yml:218` → `# Create a temporary directory for the merge`
   - `build.yml:221` → `# Merge with the existing index`
   - `build.yml:226` → `# Create a new index if none exists`
   - `renovate.yml:3` → `# Run daily at 2 a.m. Berlin time`
   - `renovate.yml:7` → `# Manual trigger`

   **Done 2026-09-27**, every line with the wording above.

   Before landing, check the open embargoed tickets for an overlapping edit of these two
   workflow files. *(Reworded 2026-09-27 by the review, see History.)* *(2026-09-27: checked before
   landing.)*
2. **Translate `.github/idea.md` in place**, keeping its structure:
   - line 1 → `My ideas:`
   - line 3 → `- The operator must be able to update clusters that were created by older versions of
     the operator itself and bring them to the target state of its current version. This means
     the operator must be able to update resources created by an older version so that they
     become compatible with the current version.`
   - line 5 → `- Which permissions do the sidecars of the Valkey instances have on a Kubernetes
     cluster?`
   - line 7 → `- After the password Secret is updated, I want the Valkey instances to switch to the
     new password automatically, without losing their state, even when they run without a PV.`
   - line 9 → `- The user must be able to control whether the PVs are kept or deleted when the Valkey
     cluster is deleted.`

   **Done 2026-09-27**, structure kept (a heading and four bullets on lines 1, 3, 5, 7 and 9).
3. Close ([ADR 0034](../../adr/0034-tickets-are-work-lists-that-get-archived.md)): nothing needs
   extracting. The rule already lives in `CLAUDE.md:5-7`, and no ADR, README or `docs/` page
   changes. Set `state: done` with a "what shipped" History entry, `git grep` `061` and `T61`
   outside `docs/tickets/` (expected: nothing), then move to `archive/`. **Done 2026-09-27**, see
   History.

## Verification

- The three sweep passes, rerun over `git ls-files` outside `docs/tickets/`, report no hit.
  The quickest proxy is two greps that print nothing afterwards. At `4a7543e` the first prints
  6 lines and the second 13:
  `git grep -nP '(*UTF)[äöüÄÖÜß]' -- ':!docs/tickets'` (the `(*UTF)` prefix keeps emoji bytes
  from matching) and
  `git grep -nwE 'Erstelle|Aktualisiere|Extrahiere|Kopiere|Manueller|Täglich|existierendem|Ideen|möchte' -- ':!docs/tickets'`.
- `DEVELOPER.md:91`, ADR 0016 line 263 and `rotation-and-change-propagation.md:48` still point at
  an existing `.github/idea.md`.
- `build.yml` and `renovate.yml` still parse. `git diff` of both files shows only `#` lines
  changed.
- *(Run 2026-09-27, on the working tree after the fix:)* the two greps print nothing (0 and 0
  lines; at `HEAD` the first still prints 6). `DEVELOPER.md:91`, ADR 0016 line 263 and
  `rotation-and-change-propagation.md:48` still point at `.github/idea.md`, which exists.
  `yaml.safe_load` (Python) parses `build.yml`, `renovate.yml` and `release.yml`. `git diff -U0`
  of `build.yml` and `renovate.yml`, filtered to changed lines that do not start with `#`, prints
  nothing. **Not run:** the third sweep pass (dictionary words) was not rerun; the two greps are
  its proxy, as stated above.

## History

- 2026-09-27: **done** - shipped the eleven workflow comments in English
  (`.github/workflows/build.yml`, `.github/workflows/renovate.yml`) and `.github/idea.md`
  translated in place; archived the same day.
- 2026-09-27: work list items 1-3 landed, file by file (read in `git diff` of the working tree,
  not taken from the implementer's report):
  - [`build.yml`](../../../.github/workflows/build.yml): lines 175, 178, 181, 190, 213, 216, 218,
    221 and 226, each with the wording of item 1; no other line changed.
  - [`renovate.yml`](../../../.github/workflows/renovate.yml): lines 3 and 7, wording of item 1.
  - [`.github/idea.md`](../../../.github/idea.md): the heading and the four bullets, wording of
    item 2.

  **Close ([ADR 0034](../../adr/0034-tickets-are-work-lists-that-get-archived.md)):** nothing
  durable to extract - the language rule already lives in [`CLAUDE.md`](../../../CLAUDE.md) lines
  5-7, and a translation decides nothing; the question whether the four wishes in `idea.md`
  become tickets stays outside this ticket (Decision). `git grep` of `T61`, `061` and `061-`
  outside `docs/tickets/` on 2026-09-27: no citation (the one raw hit for `061` is a hex
  replication id inside a log line in `test/e2e/tls_log_filter_test.go:41`). Moved to `archive/`
  with a plain `mv`, and its relative links rewritten for the new depth; links from other tickets
  to this file were retargeted to `archive/` in the same pass.
- 2026-09-27: reviewed - re-ran both verification greps at `4a7543e` (6 and 13 lines, as
  recorded) and checked every quoted line, the `git blame` dates and the three `idea.md`
  references. Replaced a coordination note that located an embargoed ticket's edit with a
  neutral check. It was removed, not struck: a struck line still carries the detail, and the
  embargo rule outranks keeping it. The file was untracked, so no commit carried it. Frontmatter unchanged.
- 2026-09-27: enriched - swept the whole tracked tree beyond the `build.yml` lines of the
  triage. Found two more comments in `renovate.yml` and the German `.github/idea.md`, and gave
  exact translations for all three files.
- 2026-09-27: filed from the ticket triage of that day (the `build.yml` finding).
