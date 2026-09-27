# ADR 0036: The Security Architecture Is One Page per Perspective

## Status

Accepted. Date: 2026-09-27. **Supersedes parts of
[ADR 0013](0013-operator-is-cluster-wide-privileged.md):**

- D12 (the footprint lives in one document);
- D13 (one hardening checklist, ordered by blast radius, completed items kept in it);
- the "There is no `SECURITY.md`" half of D14;
- its rejected alternative "Drop completed items from the checklist";
- its residual "`DEVELOPER.md` does not exist yet". That one is closed by
  [ADR 0035](0035-the-readme-advertises-the-reference-lives-under-docs.md), which created the
  file.

**Amends the home named by [ADR 0014](0014-rbac-lives-in-three-places.md) D11**, which is now
[docs/security/privilege-footprint.md](../security/privilege-footprint.md). Both records are
marked in place.

**Implemented in the change that wrote this record, which is not committed yet.** Verified in
the working tree on 2026-09-27:

- `SECURITY_ARCHITECTURE.md` (1764 lines at `HEAD` = `f5c6886`) is deleted.
- `docs/security/` holds twelve perspective pages and a README that describes the form.
- `SECURITY.md` at the root is the vulnerability-reporting policy.
- Each of the twelve pages ends with `## What this does not cover`.
- The 24 open items of the old checklist are the gaps H-1 to H-24, each under an explicit
  `<a id="h-<n>">` anchor on the page whose mechanism has the gap.

**What is not done:**

- The text was moved, not re-verified (Residual risks).
- The knowledge graph under `graphify-out/` is rebuilt by a separate run and still names the
  deleted file.

Amended 2026-09-27, the same day: the body of gap H-16 on
[workload-pod-posture.md](../security/workload-pod-posture.md#h-16) had become a run log of
2026-09-26 that [ADR 0032](0032-generated-pods-run-rootless.md),
[ADR 0033](0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md),
[ADR 0025](0025-a-split-brain-warning-means-one-that-did-not-resolve-itself.md) and
[ADR 0017](0017-test-and-ci-policy.md) already keep. It is now a gap statement — what is proven
and where, what ran locally only, what is not covered, what an operator can check on their own
runtime — with one line pointing at those ADRs. The residual risk on long pages is marked in
place. No decision changed.

Amended again 2026-09-27 (correction, no decision changes): a second review pass of the same
change shortened [isolation-and-tenancy.md](../security/isolation-and-tenancy.md) from 270 to
about 245 lines, so the long-pages residual risk gives stale figures. They are struck and
restated in place, measured again with the time of the measurement, because that pass was
still editing the pages. The risk itself still holds.

## Context

`SECURITY_ARCHITECTURE.md` had grown to 1764 lines under nine numbered sections:

- Section 3, isolation and tenancy, was 301 lines on its own.
- Section 4.5, the workload pod posture, was 411.
- Section 9, the hardening checklist, was 456 lines holding 35 items: 24 open and 11 closed.

ADR 0013 D12 made it one document on purpose, and D13 kept closed items in the list together
with what they did not close. The three-file layout this repository followed named the file at
the root. Four things did not work.

**The checklist became a backlog inside the design.** The eleven closed items carried closure
dates and verification records, one of them a full run log with mutation counts and e2e results.
The checklist's own preamble sent the reader into a ticket for the analysis. Much of what a
closed item was worth was one sentence explaining why a current rule reads the way it does,
and that sentence sat in section 9, hundreds of lines away from the section that states the
rule.

**One list served no reader who evaluates one mechanism.** Items were added as findings
arrived, from 2026-08-21 to 2026-09-26. Read today, the order that resulted follows arrival
more than blast radius. The rootless-migration items sat as a block of their own, and
"Restrict who may `create valkeys`", the one step that bounds what every CR author can choose,
stood 33rd of 35. Whatever the order, a reader evaluating the seccomp allow-list had to find its
gap in a list sorted by something else.

**Section numbers were the citation form.** Measured at `HEAD` with `git grep`, outside
`docs/tickets/`: 36 lines in 15 files named the document, and 16 lines cited one of its sections
by number. The citations came from seven ADRs, `CLAUDE.md` and two Go comments, and "section
4.2" was cited from both Go files. A number holds only until a section is inserted above it.

**A policy lived inside a design.** Section 8 was how to report a vulnerability, and it stated
"This repository has no `SECURITY.md`". Yet `SECURITY.md` is the file GitHub and every reporter
look for.

A sibling project decomposed its security design the same way (its own record), and this
change takes the form over with the valkey numbers.

## Decision

**D1 — The security architecture is the directory `docs/security/`, one page per
perspective.** A perspective is a question a reader arrives with: who is trusted with what,
what a grant permits, where the password lives, what a CR author can choose. It is not a code
mechanism. A new subject becomes a new page. It is never added as a section to a page about
something else, and a page that has grown past one sitting holds two perspectives and is split.

**D2 — `docs/security/README.md` describes the form and deliberately names no page.** It says
what belongs in the directory and what goes elsewhere, the shape of a page, the ground rules, how
to cite and how to add a page. The file names are the index. A second index would be one more
thing to keep current.

**D3 — Every page ends with `## What this does not cover`, and an open gap lives there, beside
its mechanism.** The gap states which mechanism, which adversary, whether it is live today and
what an operator can do in the meantime. There is no central list of gaps.

**D4 — An open gap carries a fixed id, `H-<n>`, in its heading.** An explicit
`<a id="h-<n>"></a>` anchor sits above the heading, so a link survives a reworded title. The
number is never changed and never reused, because reports and conversations name it. The next
number is one above the highest in use. When a gap closes, its entry and its number go.

**D5 — A closed item keeps at most three sentences, and only where it explains a current
rule.** Those sentences are past-tense prose inside the mechanism's own section. Everything
else about a closed item is history in git. Nothing under `docs/security/` takes the shape of
a ticket: no checkbox list, no owner, no closure log, no date for work that has not happened.
The ticket citations that moved with the old text are debt under
[ADR 0034](0034-tickets-are-work-lists-that-get-archived.md) D7, not a pattern.

**D6 — A security page is cited by file name and heading, never by a section number.** A gap is
cited by its page and its `#h-<n>` anchor. The pages use no numbered sections.

**D7 — `SECURITY.md` at the root is vulnerability reporting and nothing else.** It holds:

- the private route;
- what to include in a report;
- a pointer to `docs/security/` for what is already known;
- the supported versions.

ADR 0013 D14's rule is kept in substance: state the gap, and never invent a contact. The file
carries no design.

**D8 — The privilege footprint is [docs/security/privilege-footprint.md](../security/privilege-footprint.md).**
That page covers:

- the operator ClusterRole;
- the per-instance sidecar Role;
- the pre-upgrade hook;
- the gaps of those three.

It is the page that ADR 0014 D11 obliges an RBAC change to update. The rest of ADR 0013 D12
holds for it: every rule is read out of the manifests, not out of intent, and it is updated in
the same change as the code.

**D9 — The five homes of ADR 0035 D2 stay five.** The security home is a directory instead of
a single root file.

### Where each section went

Text written before 2026-09-27 cites the deleted document by section number: a commit message, a
ticket, a copy of the old file. This is what each section became.

| Cited as | Now |
|---|---|
| §1 roles and trust boundaries | [trust-boundaries.md](../security/trust-boundaries.md) |
| §2 data and secret flow | [secrets-and-tls.md](../security/secrets-and-tls.md) |
| §3 isolation and tenancy | [isolation-and-tenancy.md](../security/isolation-and-tenancy.md); its seccomp bullet went to [seccomp-profiles.md](../security/seccomp-profiles.md) |
| §4.1–4.3 the ClusterRole, the sidecar Role, the pre-upgrade hook | [privilege-footprint.md](../security/privilege-footprint.md) |
| §4.4 operator process posture | [operator-pod-posture.md](../security/operator-pod-posture.md) |
| §4.5 workload pod posture | [workload-pod-posture.md](../security/workload-pod-posture.md), [seccomp-profiles.md](../security/seccomp-profiles.md), [user-namespaces.md](../security/user-namespaces.md), [rootless-migration.md](../security/rootless-migration.md) |
| §5 validation story | [validation.md](../security/validation.md) |
| §6 rotation and change propagation | [rotation-and-change-propagation.md](../security/rotation-and-change-propagation.md) |
| §7 backup and restore | [backup-and-restore.md](../security/backup-and-restore.md) |
| §8 how to report a vulnerability | [SECURITY.md](../../SECURITY.md) |
| §9 the hardening checklist, open items | H-1 to H-24, in the closing section of the page whose mechanism has the gap |
| §9 the hardening checklist, closed items | history. As the move recorded it, four survive as a few sentences of past-tense prose beside their mechanism, one as a present-tense rule (`readOnlyRootFilesystem`), and six are gone |

## Consequences

- **There is no single page to hand to somebody who asks for the security architecture.** They
  get a directory of thirteen files. That is the point: a single page of 1764 lines is one that
  few readers finish. But anybody who wanted one artefact has lost it.
- **Every inbound reference had to name a page.** That covers the 36 lines of the Context and
  the links inside this directory. It was done in the same change: outside `docs/tickets/` no
  link to the deleted file is left, and no Go comment cites a section number (checked with the
  link checker and `grep`, 2026-09-27). A reference that names a page and a heading breaks on the
  next split, and one that names only the directory does not. D6 accepts that trade on
  purpose.
- **A closed gap cannot be counted any more.** Finding out what H-n was after it closed takes
  the git history. What that buys is a gap read by the person reading the rule it explains.
- **Allocating the next H number means searching the directory**, with the `grep` that
  [docs/security/README.md](../security/README.md) gives.
- **ADR 0013 lost D12, D13 and half of D14 as current rules.** They stay in the record,
  struck through, with pointers here.
- **The pages are longer than the source they came from.** Each page gained an intro, a closing
  section and anchored gap headings. In total they are longer than the old file. No single page
  is longer than 280 lines, against 411 for the old section 4.5 alone (measured 2026-09-27).

## Alternatives Considered

**Keep the single document (ADR 0013 D12).** Rejected. The cost D12 avoided was measurable,
and it was measured: 16 lines of section-number citations to rewrite. On the other side stood
a 1764-line document and a 456-line checklist whose order, read today, followed arrival.

**Keep a thin root `SECURITY_ARCHITECTURE.md` as a map of the directory.** Rejected. It is a
second index beside the directory README, and an index that is not read decays into a summary
that contradicts the pages. The documentation map already lives in the README, where a reader
who does not know the layout starts.

**Collect the open gaps on one page.** Rejected. The old section 9 was that page: a list
organised by item is a backlog, and nobody evaluating a mechanism reads it. Beside its mechanism,
a gap is read by exactly the reader who needs it.

**Keep the closed items, as D13 did, and only remove the checkboxes.** Rejected. What made them
a backlog was their organisation by item rather than by subject, not the checkboxes. The one
thing worth keeping, what the fix did not cover, now stands beside the rule it qualifies.

**Order the gaps by blast radius across pages.** Not possible without a central list, and
rejected together with one.

## Residual risks

- **The text was moved by line range, not re-verified.** The facts were carried across from
  text that had been verified when it was written. Three kinds of edit were applied
  mechanically:
  - links rebased;
  - section-number citations turned into links to page and heading;
  - checklist items turned into H entries.

  The past-tense sentences kept from closed items were reworded and not re-checked. The move
  carried `file:line` citations that had gone stale against today's code, and a sentence on the
  sidecar Role that said "one verb" where `BuildSidecarRole` grants `get` and `patch`. A review
  pass in the same change fixed both: it re-pointed the line citations on the footprint, secrets
  and rotation pages and corrected the verb count on
  [privilege-footprint.md](../security/privilege-footprint.md). **Not verified by this record:**
  each re-pointed line number. Two were spot-checked (the kubebuilder markers at
  `valkey_controller.go:201-218`, the Sentinel `requirepass` lines at `sentinel.go:180-188`).
- **Two open gaps read more like standing rules than gaps:** H-4 (do not extend the TLS
  fingerprint to low-entropy secrets) and H-8 (do not leave `disableAuth` or `allowUnencrypted`
  on). They were unchecked items, so they kept their numbers. Folding them into their pages as
  rules would retire the numbers before anybody cites them. Not decided.
- **Some pages are long.** ~~`workload-pod-posture.md` is 280 lines, of which H-16's verification
  record is about a hundred, and~~ ~~`isolation-and-tenancy.md` is 270 (measured 2026-09-27).~~
  D1's one-sitting rule is close to its limit there. *(Amended 2026-09-27, Status: H-16's
  verification record was replaced by a gap statement that points at the ADRs holding the runs;
  `workload-pod-posture.md` is 220 lines after it, measured the same day.)* *(Corrected
  2026-09-27, Status: the 270 is stale too. Measured with `wc -l` at 10:20 that day, while a
  second review pass was still editing the directory: `rootless-migration.md` 245 lines,
  `isolation-and-tenancy.md` 244, `workload-pod-posture.md` 224, every other page under 210.
  The risk still holds: D1 gives one sitting no number, and the pages near 250 are where a
  split is weighed first.)*
- **Nothing enforces D3 to D6.** A page without its closing section, a reused H number, or a
  new section-number citation is caught only by review.
- **Not verified:** whether GitHub private vulnerability reporting is switched on for this
  repository. `SECURITY.md` names it as the route and says it is unverified.
- **Links from outside this repository** to the old document or its section 8 anchor are broken
  and cannot be detected from here.

## References

- [docs/security/README.md](../security/README.md) — the form this record decides
- [docs/security/privilege-footprint.md](../security/privilege-footprint.md) — the footprint
  home of D8 and ADR 0014 D11
- [SECURITY.md](../../SECURITY.md) — the reporting policy of D7
- [ADR 0013](0013-operator-is-cluster-wide-privileged.md) — the privilege model; D12, D13 and part
  of D14 superseded here
- [ADR 0014](0014-rbac-lives-in-three-places.md) — RBAC in three places; D11's home changed here
- [ADR 0035](0035-the-readme-advertises-the-reference-lives-under-docs.md) — the five homes, of
  which this is one
- [ADR 0034](0034-tickets-are-work-lists-that-get-archived.md) — why a security page takes no
  ticket shape and cites no ticket
- [`internal/builder/rbac.go`](../../internal/builder/rbac.go) — `BuildSidecarRole`, the two
  verbs behind the carried-over "one verb" sentence
