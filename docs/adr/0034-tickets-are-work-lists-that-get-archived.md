# ADR 0034: Tickets Are Work Lists That Get Archived, and an Open Security Finding Is Embargoed

## Status

Accepted, amended 2026-09-27 (D2, D4: the ignore line reaches `archive/`, see the amendment
below; D4: `state: done` is not proof of a fix, a clarification; D5 re-decided and D9 added:
a finding goes into an existing ticket first, and a ticket carries no history, see the last
amendment). Date: 2026-09-27.

Every decision below was taken by the owner on 2026-09-27, each presented on its own with its
options, during the documentation restructure that also produced
[ADR 0035](0035-the-readme-advertises-the-reference-lives-under-docs.md) and
[ADR 0036](0036-the-security-architecture-is-one-page-per-perspective.md). The model is the
ticket lifecycle of a sibling project, adapted where this repository differs (D4, D5, D8).

**Implemented in the change that wrote this record, which is not committed yet.** Verified in
the working tree on 2026-09-27:

- Every ticket file is named `NNN-<slug>.md` (D1). The open ones are in `docs/tickets/`, the
  closed ones and the collection ticket in `docs/tickets/archive/` (D2, D6).
- The embargoed tickets carry the `local_` prefix, and the repository's own `.gitignore`
  ignores them through the line ~~`docs/tickets/local_*`~~ `docs/tickets/**/local_*`
  *(corrected 2026-09-27: the first line matched only `docs/tickets/` itself and not
  `archive/`; see the amendment below)*. Checked with
  `git -c core.excludesFile=/dev/null check-ignore -v`, so the result does not depend on the
  owner's global rule. A tracked ticket is not ignored (D4).
- [docs/tickets/README.md](../tickets/README.md) carries the rules and no index. The
  frontmatter grep it documents is the index (D5).
- `.graphifyignore` excludes `docs/tickets/archive` from the knowledge graph.

**Open:**

- **D7 is partly implemented, by decision.** Measured with `git grep` on `HEAD` = `f5c6886`,
  outside `docs/tickets/`: 200 lines cite a `T<n>` label (110 in `docs/adr/`, 70 in Go files,
  20 in the root documents), 8 name a path into `docs/tickets/`, and 53 cite one of the three
  finding labels an archived record defines. The working tree measured the same 200 `T<n>`
  lines on 2026-09-27. These citations stay until they are rewritten, and they are the work
  list of the family ticket of the reference ban.
- The tickets become tracked only when the change is committed. Until then, every link from an
  ADR into `docs/tickets/` resolves only on the owner's machine, as it did before.

**Amended 2026-09-27, in the same uncommitted change: the ignore line reaches `archive/`.**
D2 moves every closed ticket to `docs/tickets/archive/` under its own name, and a ticket can
close while it is still embargoed: dropped, or done with a live or boundary risk accepted rather
than fixed (D4). Such a ticket therefore arrives in `archive/` with its `local_` prefix, and the
line `docs/tickets/local_*` did not match it there:
`git -c core.excludesFile=/dev/null check-ignore -v --no-index` on a `local_` path under
`archive/` exited 1, and only the owner's global rule `local_*` matched it. In a clone without
that rule, `git add -A` would have staged the file. The line is now `docs/tickets/**/local_*`
(D4), and D2 and D4 say where a closed embargoed ticket goes: into `archive/`, prefix kept,
with no exception to D2. That applies D2 as the owner decided it; the placement was not put to
the owner as a question of its own. The alternative, an exception that keeps such a ticket in
`docs/tickets/`, is under *Alternatives Considered*.

Re-verified on 2026-09-27 with `git -c core.excludesFile=/dev/null check-ignore -v --no-index`,
so without the owner's global rule: `.gitignore:46` (`docs/tickets/**/local_*`) matches a
`local_` path directly under `docs/tickets/`, a `local_` path under `docs/tickets/archive/`, and
both embargoed files in the tree. It matches no numbered ticket and neither `README.md`, in
`docs/tickets/` or in `archive/`. `git -c core.excludesFile=/dev/null status --porcelain
--untracked-files=all docs/tickets` lists every numbered ticket and both READMEs, and no
`local_` file.

**Amended 2026-09-27, in the same uncommitted change: `state: done` is not proof of a fix.** A
clarification of the owner decision, not a new rule: D4 already ended the embargo at the fix,
but [docs/tickets/README.md](../tickets/README.md) had ended it, and renamed the file, at
`state: done`. A ticket can reach `done` with a live or boundary risk accepted rather than
fixed, and that rule would have published it. D4 now says so outright, and the rules page
renames an embargoed ticket only when its ~~"what shipped" History entry~~ `shipped:`
frontmatter line *(amended 2026-09-27 with D9: tickets carry no History)* names the fix of every
live or boundary item.

**Re-decided 2026-09-27 by the owner, later the same day: D5's filing rule, and D9 added.** A
re-verification of every open ticket had filed nineteen new tickets from side findings under
"every finding is a file" and annotated every correction in place, with a History entry per
pass. The backlog reached 50 files and more than 33,000 lines, and the owner judged the tickets
overloaded and unreadable: small leftovers had started to block feature work. His rule, taken as
stated: a finding is first assigned to an existing ticket, a new ticket is opened only when none
fits, and a ticket collects similar findings (D5); ticket files carry no History entries (D9).
Embargoed tickets stay as D4 has them. The two embargo records that D4 and the rules page used to
keep as History entries - the "what shipped" line and the owner's dated publishing acceptance -
move into the frontmatter fields `shipped:` and `publication-accepted:`, with `dropped-reason:`
for a dropped ticket; that placement was chosen when the rule was implemented and was not put to
the owner as a question of its own. **Implemented** in
[docs/tickets/README.md](../tickets/README.md) and `CLAUDE.md` in the same uncommitted change, and
every open ticket was rewritten to the D9 form. Under the new D5 the open tickets were then
consolidated by subject, on the owner's instruction the same day: 50 files became 21 (15 tracked,
6 embargoed), each merged ticket keeping the number and id of the ticket that took the others in,
chosen so that every ticket cited outside `docs/tickets/` survives. The numbers of the merged
tickets are not reused; the numbering command of the rules page reads them from git history.

## Context

Until 2026-09-27 every ticket file was named `local_*`. The owner's global ignore rule
therefore kept each one out of the repository: at `HEAD` = `f5c6886` nothing under
`docs/tickets/` is tracked. The ADRs cited them anyway. Six ADRs named a path into
`docs/tickets/`, eight references in all, and seven of those were Markdown links. Every one
pointed at an untracked file, so each link was dead on GitHub and in every clone. It resolved
on exactly one machine. The same asymmetry had already bitten once. A working note's finding
labels were cited from 170 lines in 29 Go files while the note itself was untracked and about
to be deleted (measured on 2026-08-21, not re-measured for this record). For every reader but
its author, each citation named something that did not exist.

Three further things did not work.

- **A board beside the files.** A one-line row per ticket hid the analysis behind it and had to
  be groomed separately from the files it summarised. The owner retired it earlier on
  2026-09-27, before this record was written: a board row was never a sufficient record of a
  finding.
- **A collection ticket.** One file had grown to hold every finding of a fleet rollout — a
  numbered item list, the analyses and the retired board. Closed and open items shared it, so
  it could be neither archived nor closed.
- **No lifecycle.** Nothing said when a ticket ends, where the decision inside it goes, or
  whether a finished ticket may be cited.

Tracking the tickets fixes the dead links and creates a new exposure. Some open tickets
describe a security finding that is not fixed yet: an attack path that works today. Such a file
must not be published before its fix.

## Decision

**D1 — One file per ticket, `NNN-<kebab-slug>.md`.** Three digits. A ticket that carries a
`T<n>` label takes that label's number, and its frontmatter keeps the label as its `id:`. A new
ticket takes the highest number in use plus one. **A number is never reused**: not the number of
an embargoed ticket, and not the number of an item that never got a file of its own.

**D2 — A closed ticket moves to `docs/tickets/archive/` and keeps its name.** An archived file
is history. Nobody maintains it. Its paths, line numbers and states describe the tree of the
day it was archived, and it is never the source of a current rule. *(Added 2026-09-27:)* A
ticket that closes while it is still embargoed — dropped, or done with a live or boundary risk
accepted rather than fixed (D4) — is no exception: it moves to `archive/` with its `local_`
prefix, which stays until the embargo ends, and the ignore line of D4 keeps it untracked there.
The owner's dated publishing acceptance and the rename that end the embargo are the one change
such a file still receives in `archive/`.

**D3 — The extraction is the close.** Before a ticket is archived, everything durable leaves
it:

- the decision goes into an ADR;
- the operator-facing consequence goes into `README.md`, `docs/operations/` or
  `docs/security/`;
- the contributor-facing one goes into `docs/developer/` or `DEVELOPER.md`
  ([ADR 0035](0035-the-readme-advertises-the-reference-lives-under-docs.md) D2).

Then the tree is searched for the ticket's number and its `T<n>` label, and whatever is left is
cleared. Moving the file is only what happens afterwards. A rule found in the archive is a
defect of the day that file was archived.

**D4 — Tickets are tracked, and an open security finding is embargoed.** An open ticket whose
frontmatter says `security: live` or `security: boundary` keeps the `local_` prefix. The line
~~`docs/tickets/local_*`~~ `docs/tickets/**/local_*` in this repository's `.gitignore` keeps it
untracked, in `docs/tickets/` and in `archive/` alike, and so does the owner's global rule
*(corrected 2026-09-27: the first line did not reach `archive/`, where D2 puts a ticket that
closes while still embargoed; see Status)*. While the embargo holds:

- no tracked file names the ticket's file, links to it, or describes its finding — no threat
  line, no mechanism, no attack path;
- the same applies to commit messages and pull requests;
- a tracked file may give its id, severity, security class, effort and state.

**The embargo ends when the finding is fixed.** For a finding that is dropped, it ends only
when the owner explicitly accepts publishing it. A dropped finding is unfixed by definition,
and dropping the prefix would publish an open attack path. Until then a dropped embargoed
ticket sits in `archive/` with the prefix (D2, added 2026-09-27). Once the embargo ends, the
file is renamed without the prefix and is tracked from then on. A `hardening` finding is not embargoed.

*(Clarified 2026-09-27, same day:)* `state: done` is not proof of a fix: an accepted risk,
including one item of a multi-item ticket, ends the embargo only on the owner's explicit
acceptance, as a dropped finding does.

**D5 — There is no index table and no board.** The `state:` line in each ticket's frontmatter
is the index, read by the `grep` that [docs/tickets/README.md](../tickets/README.md) documents.
That page carries the rules and nothing else. ~~**Every finding is a file**, either a new ticket
or an appendix to the existing ticket of its family (same mechanism, same decision).~~
*(Superseded 2026-09-27, re-decided by the owner the same day; see Status.)* **A finding goes
into an existing ticket first**: the open ticket whose subject it shares - the same component,
mechanism or kind of change - takes it into its current state and its required changes. A ticket
collects similar findings. A new ticket is opened only when no open ticket fits. A collecting
ticket is still one subject; findings that share only the event or the analysis that found them
are not collected in one file (the collection ticket of the Context). A row somewhere, or a
finding that lives only in a report, is never enough.

**D9 — A ticket shows the current state and nothing else.** *(Added 2026-09-27.)* A ticket file
has no History section and no dated entries. It holds its frontmatter, the current state (what
the code, configuration or documentation does today, with code locations, and the impact), the
required changes (the target state and the tests that prove it), the open questions (each
understandable on its own, with the sensible options, the recommended one marked and justified,
and an answer line the owner fills in), what is not verified where it still matters, and related
tickets. When a fact, a question or an answer changes, the text is rewritten to the new state:
nothing is struck through or annotated with a date, done work items are removed, and no account
of how the code or the ticket evolved is kept. Git keeps that for a tracked ticket. The records
the embargo needs are frontmatter fields: `shipped:` (set with `done:`, it names the fix of every
live or boundary item before the rename of D4), `dropped-reason:`, and `publication-accepted:`
(the date of the owner's explicit acceptance to publish an unfixed finding).

**D6 — Only the open items of the collection ticket were extracted.** Each got a ticket of its
own. The rest — the closed items, the analyses and the final state of the retired board — was
archived as one file, with the extracted items left as stubs. Tickets written before the
one-file-per-ticket rule stay in the multi-item shape they had and were archived as they were.

**D7 — Nothing outside `docs/tickets/` cites a ticket.** No ADR, no page under `docs/`, no root
document, no code comment, no commit message and no pull request names a ticket's number, its
`T<n>` label, its file name or its path. A durable statement cites the ADR that holds the rule.
A ticket may cite an ADR, and an ADR does not cite a ticket. **Partly implemented, by
decision.** The rule binds every citation written from 2026-09-27 on. The roughly 200
citations that already exist stay until they are rewritten. They are the work list of the
family ticket of this rule, which this record does not name (D7 itself), and they are not a
precedent.

**D8 — An ADR in this repository may link into the code.** The sibling project's ADRs carry no
file paths and no identifiers from the tree. This repository deliberately keeps its own
convention, and there are two reasons:

- A reader here verifies an ADR against the code. The ground rules in
  [README.md](README.md) ask for identifiers quoted exactly so that the record stays checkable
  against the tree, and every ADR's `References` section links the files it rests on.
- The convention is stated in `CLAUDE.md` (the ADR structure table). Changing it would mean
  rewriting 33 records without making any of them more correct.

The price: moving code that an ADR names means updating that ADR in the same change.

## Consequences

- **Closing a ticket costs more at the end.** It takes the extraction, then the search, then the
  move. That is the point: what survives is the record a later reader needs, and the live
  directory stays small.
- **The links from the ADRs resolve for everybody once the change is committed.** Resolving does
  not make them allowed. D7 forbids them whether they resolve or not, so every one of them is on
  the family ticket's work list.
- **The embargo creates gaps in the tracked numbering.** An embargoed ticket takes its number,
  and a reader of the repository sees the number missing without learning what sits there.
- **The archive is published with the repository.** Its multi-item records carry what they
  carried as untracked files: environment names, namespaces, resource names and local file
  paths. The owner accepted this; see Residual risks.
- **Commit messages cannot be repaired.** Earlier commits name tickets and work items by label,
  and history is not rewritten for this.
- **A reader who skips `Status` may take D7 for implemented.** It is not. The existing
  citations are in plain sight in the ADRs, the Go sources and the root documents.

## Alternatives Considered

**Track every ticket, without an embargo.** Rejected. It publishes each open `live` or
`boundary` finding, attack path included, before the fix exists. This repository is hosted on
GitHub, and its README carries public-service badges. Whether the repository is public was not
verified here, and the rule does not depend on it.

**Keep every ticket local and untracked.** Rejected. It is the status quo that produced the dead
links. The work list stays invisible to everybody but the owner, and the analyses behind closed
work live on one machine.

**An index table in the tickets README, as the sibling project keeps.** Rejected. It is a
second record of every ticket's state that has to be groomed apart from the files. The board had
just been retired for exactly that cost, and the frontmatter already carries the state.

**A minimal index — one line per file.** Rejected for the same reason at a smaller scale. The
file names and one `grep` answer the question the list would answer.

**Split every item of the collection ticket into its own file.** Rejected. Most of its items
were no longer open, and splitting a finished item manufactures a work list for finished work. The
extraction duty (D3) applies to open work, and history reads better as the one record it was
written as.

**Keep the collection ticket live.** Rejected. A live file that mixes closed and open items is
the shape that could never be closed, and its open items could never be archived one by one.

**Do not adopt the reference ban.** Rejected. A citation of a ticket outlives the ticket. Once
the ticket is archived, the citation points at history. A reader who follows it lands in a plan
instead of a rule, and nobody can archive a ticket without breaking the files that cite it.

**Rewrite all existing citations in the same change.** Rejected. That would mean editing about
200 lines — 110 in ADRs, 70 in Go sources, 20 in the root documents — inside a documentation
move. Some of those citations sit in ADR `Amended` headers, and whether those are provenance
markers to keep is not decided. The ban binds new citations now, and the existing ones become a
work list.

**Adopt the sibling project's rule that an ADR carries no code references.** Rejected; D8
gives the reason.

**Every finding is a file.** *(Added 2026-09-27, the rule D5 held until it was re-decided the
same day.)* Rejected by the owner after it had run for a day. It turned every side finding of an
analysis into a file of its own, grew the backlog instead of shrinking it, and spread similar
findings over several tickets that each had to be read, decided and closed on their own.

**A History section in every ticket.** *(Added 2026-09-27.)* Rejected by the owner. Correction
trails, dated review notes and the story of each pass made the tickets unreadable and hid the
question that had to be answered. For a tracked ticket git keeps the story. For an untracked
embargoed ticket the story is lost when the text is rewritten; that is accepted.

**Keep a closed embargoed ticket in `docs/tickets/` until its embargo ends.** *(Added
2026-09-27.)* Rejected. It is an exception to D2 for one case, and it does not save the ignore
line: `docs/tickets/**/local_*` is needed either way, as defence in depth against a `local_` file
that lands in `archive/` by any route. With the wider line, D2 holds without an exception. The
price of that: the owner's dated publishing acceptance and the rename that ends the embargo are
then made to a file in `archive/`, the one change an archived file still receives.

## Residual risks

- **The embargo rests on a naming convention and on review.** Two ignore rules stop
  `git add -A` from staging a `local_` file: this repository's `.gitignore` line and the owner's
  global rule. Neither stops these:
  - `git add -f`;
  - a rename that drops the prefix too early;
  - a tracked file, commit message or pull request that names or describes the finding.

  Nothing in CI checks for any of them.
- **The archived records carry internal environment inventory**: cluster and environment names,
  namespaces, `Valkey` resource names, key counts and local kubeconfig paths. **Accepted by the
  owner, not scrubbed.** It becomes public with the commit that tracks the archive, if the
  repository is public.
- **The knowledge graph.** `.graphifyignore` keeps `docs/tickets/archive` out of the graph, so a
  finished plan is not surfaced as an answer about the operator. The live tickets stay in the
  graph. Whether the next `graphify update` honours the file was not verified; it was not run
  for this record.
- **Enforcement of D7 is manual.** No CI job searches a tracked file or a commit message for a
  ticket citation. `.github/` carries no such check (searched 2026-09-27).
- **Line numbers in archived files go stale by design.** An archived file is never updated
  (D2), so each of its `file:line` references describes the tree of the day it was archived.
- **Not verified:** that every decision in the files archived on 2026-09-27 is held by an ADR.
  It was not re-checked when they were moved. A decision that no ADR holds is now history only.
- **Not verified:** that this text states each decision exactly as the owner took it. The
  record was written from the decision summary of the restructure. The owner reviews it before
  the change is committed.
- **Not verified:** the citation counts on the committed tree. They were measured on
  `HEAD` = `f5c6886`, and once on the working tree for the `T<n>` labels. The restructure is
  meant to add no citation.

## References

- [docs/tickets/README.md](../tickets/README.md) — the rules page: naming, the embargo, the
  reference ban, the frontmatter, and the grep that is the index
- [`.gitignore`](../../.gitignore) — the ~~`docs/tickets/local_*`~~ `docs/tickets/**/local_*`
  line of D4 *(corrected 2026-09-27)*
- [`.graphifyignore`](../../.graphifyignore) — the archive exclusion
- [ADR 0035](0035-the-readme-advertises-the-reference-lives-under-docs.md) — the five homes that
  the extraction of D3 moves material into
- [ADR 0036](0036-the-security-architecture-is-one-page-per-perspective.md) — why a security
  page carries no ticket shape and names open gaps by `H-<n>` rather than by ticket
- [README.md](README.md) — the ADR format and the ground rules that D8 keeps
