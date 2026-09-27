# T40 - Rewrite every ticket citation outside `docs/tickets/` to the ADR that holds the rule

> **Status:** open; severity low (misleading and dangling pointers, no operator behaviour);
> security none; urgency `now` (tracked files carry measured-false statements, item 1; `later`
> once it lands); effort L (about 270 lines); items 6-8 wait on the open questions.

## Current state

[ADR 0034](../adr/0034-tickets-are-work-lists-that-get-archived.md) D7 forbids citing a ticket
(number, T-label, file name, path) outside `docs/tickets/`. It is partly implemented: no new
citation may be written, and every existing one is this ticket's work list (`0034:162-166`).
Enforcement is manual (`0034:262-263`); no Makefile target, workflow step or hook checks it.

**What the tree cites today** (all outside `docs/tickets/`):

- **190 T-label lines** (`git grep -nwE 'T[0-9]+'`): 110 in `docs/adr`, 70 in 26 Go files, 7 in
  [`CLAUDE.md`](../../CLAUDE.md) (`:284`, `:568`, `:620`, `:785`, `:794`, `:1014`, `:1047`), 3 in
  [`rootless-migration.md`](../security/rootless-migration.md) (`:27`, `:47`, `:85`). No embargoed
  label occurs. No label reaches a generated artifact (`config/crd/bases/` carries none).
- **41 `NA61`-`NA63` lines**, all in ADRs: 37 in ADR 0020, 4 in ADR 0006. Mapping: `NA61` -> ADR
  0020 D1 (StatefulSets and observer Deployment), `NA62` -> ADR 0020 D1 (every managed kind) and
  ADR 0006, `NA63` -> ADR 0020 D9 (pods); a consumer that treats a foreign object as absent is
  ADR 0020 D8.
- **8 path citations**, all in ADRs: `0012:292`, `0017:612`, `0025:444`, `0025:496`, `0028:30`,
  `0032:6`, `0033:6`, `0033:750`.
- **About 38 lines no grep for T or NA labels finds:**
  - 23 Go lines with sub-labels of archived tickets: 21 in
    [`pod_termination_test.go`](../../internal/controller/pod_termination_test.go) (E1-E6, S1, S3,
    S4, S7, S8; `git grep -nwE '(E[1-6]|S[1-8])'`);
    [`reconcile_steps_test.go:126`](../../internal/controller/reconcile_steps_test.go#L126) (F1);
    [`rolling_update_paused_condition_test.go:167`](../../internal/controller/rolling_update_paused_condition_test.go#L167) (Q2).
  - 2 ADR lines with WP labels: ADR 0003 `:229` (WP1), ADR 0005 `:421` (WP5).
  - 14 lines that mention "the ticket" with no identifier. Content pointers: ADR 0003 `:112`,
    `:229`; ADR 0005 `:414`, `:422`, `:458`; ADR 0028 `:283`; ADR 0032 `:76`; ADR 0033 `:714`;
    [`topology_abandon_test.go:13`](../../test/e2e/topology_abandon_test.go#L13). Narrative: ADR
    0027 `:54`, ADR 0005 `:96`, `:101`, ADR 0033 `:573` (struck). ADR 0005 `:100-101` says "the
    existing mentions of the admission-gap ticket stay".
- **Labels used as tags, nouns and handles.** A tag records provenance and can be removed without
  changing the sentence (`Amended 2026-08-22 (NA61):`, `(measured, T31)`); roughly 50-56 of the
  151 labelled ADR lines have tag shape, plus Go comments such as
  [`pod_security.go:175`](../../internal/builder/pod_security.go#L175) and `:197`. A noun or
  handle carries meaning ("the NA62 amendment adds", "`Ready`/T18", "the S1 regression guard"). In
  ADR 0020 the three amendments of 2026-08-22 (`:41`, `:46`, `:53`) are named only by their labels
  (`:206`, `:281`, `:563-595`, `:653-699`, `:725`, `:767`); ADR 0006 `:21-23`, `:322-323` and ADR
  0026 `:121` do the same. Provenance stays recoverable with `git log -S '<header text>' -- <adr>`
  (`git blame` names only the last commit that touched the line).
- **Some labels have no one-to-one ADR home.** By content against ADR 0026's decisions: E1 -> D1
  (with the D2 carve-out), E2 -> D4, E3 -> D5, E4 -> D9, E5 -> D5 (bounded observation), E6 -> D6,
  S7 -> D7; S1, S3, S4, S8 are review findings with no decision and must be restated inline. Q2 ->
  ADR 0002 D10b, F1 -> ADR 0001. T24(c) -> ADR 0030 D12; T24(a), (b), (d) are used in ADR 0030
  itself and in about 20 Go lines, and have no stated mapping. "The T24(d) neutrality style" is the
  presence-guarded clear of ADR 0005 D10. "Row N of T32"
  ([`pod_availability_test.go:186`](../../internal/controller/pod_availability_test.go#L186),
  `:211`, `:249`, `:708`) points at a table no ADR carries. ADR 0026 `:693-717` headings carry
  option labels of an archived ticket ("(T32 Q1 B)"). T1 at ADR 0003 `:111` is archive/037's T1
  (the 30 s recovery target), not archive/039's; T1-T5 exist in both archives.
- **The registry test demands a ticket citation.**
  [`condition_registry_test.go:205`](../../internal/controller/condition_registry_test.go#L205)
  asserts `T\d+` on every `declaredGap`; the messages at `:128`, `:158`, `:206` and the field doc
  at [`condition_registry.go:84-86`](../../internal/controller/condition_registry.go#L84-L86) call
  it "the ticket reference"; [ADR 0027](../adr/0027-conditions-are-levels-edges-or-history.md) D4
  (`:198-205`, `:119`) decides it. Every future gap therefore breaks D7. The only gap is the
  `Ready` row ([`condition_registry.go:102`](../../internal/controller/condition_registry.go#L102),
  "T18: ... (ADR 0001 D4 decides this; re-decision open)"); it suppresses no failing assertion.
- **Citations of open tickets** T12, T18, T23 and T34: 25 label lines and one path (ADR 0025
  `:444`). Some ADR homes cite the ticket themselves (ADR 0010 `:812`, ADR 0026 `:791`).
- **Measured-false statements** (archive/037 is tracked, and ADR 0034's change is committed):
  - [ADR 0003](../adr/0003-nudge-a-short-of-pods-statefulset.md) `:111-113` and `:228-229`: the
    30 s target and WP1's assumption come from a ticket "not in this repository".
  - [ADR 0009](../adr/0009-an-unrecorded-promotion-is-not-a-promotion.md) `:43`: "The review is
    not in this repository".
  - ADR 0034 `:14`, `:37-38`: "not committed yet"; residual risk `:268-270`: "The owner reviews it
    before the change is committed". ADR 0035 `:35` and ADR 0036 `:20` carry the same "not
    committed yet" sentence.

**Impact.** A reader who follows a citation lands in a work plan instead of a rule, or finds
nothing (unlabelled mentions); new labels keep being written next to the old ones.

## Required changes

Rewrite form: the prose around a citation stays, only the pointer changes, to the ADR and
decision that carry it (`ADR 0011 D1`, or the full path where it fits). Archived tickets are not
edited (ADR 0034 D2).

### Independent of the open questions

1. Correct the measured-false statements in place, naming no ticket path: ADR 0003 `:111-113`,
   `:228-229` (drop "not in this repository", rewrite `T1`, `WP1` and the ticket mentions; the
   in-repo trace is `admissionRecoveryDeadline` in `test/e2e/admission_recovery_test.go`); ADR
   0009 `:43` (state the three defects and their fix commits `30588bd`, `744b589`, which the ADR
   already names); ADR 0034 `:14`, `:37-38`, `:268-270` (committed; say that whether the owner
   reviewed the text first is not recorded), ADR 0035 `:35`, ADR 0036 `:20`. In the same ADR 0034
   edit, keep the counts at `:31-36` as a dated measurement and discharge "Not verified: the
   citation counts on the committed tree" (`:271-273`) with 190 T-label, 41 `NA` and 8 path lines.
2. `CLAUDE.md` noun uses (needs a session in which Hans asks for the edit): `:568`
   `` `Ready`/T18 `` -> `` `Ready` (ADR 0001 D4) `` (follows Q2 and ticket 018); `:620` "a
   pre-existing gap T32 does not close" -> "a pre-existing gap, an ADR 0026 residual risk", and
   rewrite "not fixed by T32" at ADR 0026 `:791` in the same edit.
3. Before the sweep, add a mapping table to this ticket for every sub-label listed above, and
   settle T24(a), (b), (d) (see Not verified).
4. Open-ticket citations (T12, T18, T23, T34) are part of the sweep: whichever of 040 and the
   owning ticket lands first rewrites the line, the other verifies it. Targets: T12 -> residual
   risks of ADR 0025 (`:441-444`) and ADR 0028 (`:123`, `:230`); T18 -> ADR 0001 D4; T23 -> ADR
   0010 `:808-813`; T32's `verifyNewMasterReady` gap -> ADR 0026 `:787-791`; the T34 lines of ADR
   0017 are tags (Q1).
5. [`docs/tickets/README.md`](README.md#naming-and-numbering) `:29-30`: add that archive/037
   defines its own T1-T5 (test scenarios) and WP1-WP6.

### Depends on the answers

6. Q2: change the registry test regexp, its three messages, the field doc, the `Ready` row
   prefix, ADR 0027 D4 and `:119`. Decide before ticket 018 rewrites `condition_registry.go:102`.
7. Q1 and Q4: the sweep - the 151 labelled ADR lines, the 8 path citations, the Go T-label lines,
   the 23 Go sub-label lines, the 2 WP lines, `CLAUDE.md` `:284`, `:785`, `:794`, `:1014`,
   `:1047`, `rootless-migration.md` `:27`, `:47`, `:85`, and under Q4 A the content-pointer
   mentions. Noun and handle uses are rewritten under every answer. Coordinate ADR 0020 with
   tickets 042 and 060.
8. Q3: the guard, if chosen, lands with the close.

### Close

- ADR 0034 Status: D7 implemented; its State in `docs/adr/README.md` (`:112`) and `:26-28`.
- Update the "partly implemented" statements: `CLAUDE.md`, `docs/tickets/README.md` `:93-100`,
  `DEVELOPER.md:449`, `docs/security/README.md:59-61`, ADR 0036 D5 `:116-117`, ADR 0034 D7
  `:162-166` and residual risk `:262-263` (per Q3), ADR 0005 `:100-101` (per Q4).
- Both greps outside `docs/tickets/` return nothing a close must keep:
  `git grep -nwE 'T[0-9]+|NA[0-9]+' -- ':!docs/tickets'` and
  `git grep -nE 'docs/tickets|\.\./tickets/' -- ':!docs/tickets'` (rule and pointer lines that
  name the directory but cite no ticket stay). Also `git grep -nwE 'T40|040|C3' -- ':!docs/tickets'`.
- Read-through checks: `git grep -nwE '(E[1-6]|S[1-8]|F1|Q2|WP[0-9]+)' -- ':!docs/tickets'`
  returns only ADR 0032's own option labels (`:431-432`); `git grep -niE 'admission-gap
  ticket|the ticket|ticket.s list' -- ':!docs/tickets'` returns only rule text and, under Q4 A,
  narrative mentions.
- `make fmt && make vet && make lint`, `make test-unit`, `make test-integration`,
  `make generate-all` with a clean tree, and
  `make test-e2e E2E_RUN='TestE2E_CompileCheckOnly_NoSuchTest'` (lint skips build-tagged files, so
  comment edits under `test/integration/` and `test/e2e/` are proven only by the last two).
- Move this file to `archive/`.

## Open questions

Answer Q2 first (ticket 018 waits on it), then Q1, Q4, Q3.

### Q1: Is a ticket label used as a provenance tag rewritten, or kept as a listed exception?

About 50-56 ADR lines, some Go comments and the `CLAUDE.md` and `rootless-migration.md` lines use
a label only as a tag. ADR 0034 (`:228-232`) leaves this open for ADR `Amended` headers. Noun and
handle uses are rewritten either way.

- **A. Rewrite the tag to the decision it records** (recommended): `Amended 2026-08-22 (NA61)` ->
  `Amended 2026-08-22 (D1, StatefulSets)`, the form ADR 0020 `:33`, ADR 0017 `:710` and ADR 0012
  `:37` already use; an evidence tag names the ADR with the evidence (`rootless-migration.md:27`,
  `:85` -> ADR 0032). The greps end at zero, no allow-list; the direct pointer to the archived
  analysis is lost, `git log -S` recovers the commit.
- **B. Keep tags as listed exceptions** and amend D7 to allow them: saves about a fifth of the
  lines, but needs a permanent exception list whose line numbers move with every ADR edit, and
  keeps about 50 pointers into archived plans.

A leaves a zero-hit grep for the close and for a guard, and keeps the information by naming the
decision.

**Answer:** _open_

### Q2: What does a `declaredGap` in the condition registry have to name?

The test demands `T\d+`, which D7 forbids for every new gap; a ticket that closes with its gap
kept would also have to remove a label the test demands (ADR 0034 D3).

- **A. The ADR that owns the exception** (recommended): the test asserts `ADR \d{4}` and checks
  that a `docs/adr/NNNN-*.md` file exists for it; the `Ready` row drops `T18: ` (it already names
  ADR 0001 D4); an open defect gap is first recorded under the owning ADR's Residual risks. Cost
  XS, unit tier only.
- **B. Keep `T\d+` and carve `declaredGap` out of D7**: no code change, but the first exception to
  D7, a conflict with D3 for every gap that outlives its ticket, and a special case in a Q3 guard.

A keeps ADR 0027 D4's purpose (traceable to a decision) without any exception to D7.

**Answer:** _open_

### Q3: Is D7 enforced mechanically once the sweep is done?

Nothing checks it today, and labels were written again within hours of an earlier sweep.
`make lint` runs in `Code Linting`, a required context on every push and pull request
(ADR 0017 D47). No option covers commit messages, pull request bodies, sub-labels or unlabelled
mentions; ADR 0034's residual risk keeps saying so.

- **A. Stay manual**: no cost; a regression surfaces only at the next audit.
- **B. A `git grep` target called by `make lint`** (recommended), for example
  `make check-ticket-citations`: fails on `T[0-9]+|NA[0-9]+` (word match) and on
  `tickets/(archive/)?(local_)?[0-9]{3}-` outside `docs/tickets/`, excluding `go.sum` and
  `package-lock.json`. Cost XS; a docs-only push to `main` turns `main` red instead of being
  blocked; needs Q1 A (or B's allow-list) and the sweep first.
- **C. A diff-scoped guard now** (added lines only, `base...HEAD`): protects during the sweep, cost
  S, needs event-specific base handling in CI, and B replaces it at the close.

B is cheap, rides an already required context and turns the residual risk into a checked rule.

**Answer:** _open_

### Q4: Is a mention of a ticket without an identifier a citation?

D7 lists number, T-label, file name and path. Fourteen lines mention "the ticket" with none of
them; nine send the reader there for content (evidence, a list, a rationale), the rest are
narrative. ADR 0005 `:100-101` says these mentions stay; whether that records Hans's decision is
not recorded.

- **A. A content pointer is a citation and is rewritten; a narrative mention is not**
  (recommended): each pointer states the content or its in-repo trace; ADR 0005 `:100-101` is
  amended and D7 gains one sentence stating the reading. About ten lines in ADRs the sweep edits
  anyway.
- **B. Only the four listed forms are citations**: ADR 0005 `:100-101` stands, D7 gains one
  sentence. Cost XS; ADR text keeps deferring its evidence to a ticket a reader cannot find.

D7's reason (a reader lands in a plan instead of a rule) applies even more when the plan cannot be
found.

**Answer:** _open_

## Not verified

- Which ADR 0030 decisions T24(a), (b) and (d) map to; settled by reading archive/039's T24
  against ADR 0030 (required change 3).
- The hand classification of every label line into tag, handle and noun; settled during the
  sweep.

## Related

- T12, T18, T23, T34: their citations are part of this sweep (required change 4).
- T18: meets Q2 on `condition_registry.go:102`; its string lands with 018's own decision or with
  Q2 A, not with the `T18:` prefix.
- T42: its close grep for `S1` would hit `pod_termination_test.go:408`, an archive/039 label; it
  also edits ADR 0020.
- T60: edits ADR 0020 near `:477-479` and `:570-571`; coordinate with the ADR 0020 rewrite.
- T43: `make lint` skips build-tagged files.
