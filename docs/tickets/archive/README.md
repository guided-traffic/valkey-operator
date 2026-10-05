# Archived tickets

Finished work lists. **Nothing in this directory is a current rule**, and nothing here is
maintained: a file arrives when its work has landed and is never updated afterwards, so every
path, line number and status it names describes the tree of the day it was archived.

What a file here is good for is the question a decision record does not answer — why does this
look the way it does: what was tried, what was measured, what was refused on the way. What it is
never good for is what the operator does today. That is:

| Question | Where |
|---|---|
| What the operator does and why, what was rejected | [docs/adr/](../../adr/) |
| How a subsystem works, an invariant, a hard-won detail | [docs/developer/](../../developer/) and [DEVELOPER.md](../../../DEVELOPER.md) |
| What somebody running the operator or writing a `Valkey` resource needs | [README.md](../../../README.md) and [docs/operations/](../../operations/) |
| The threat model and the gaps | [docs/security/](../../security/) |
| Work still outstanding | a live ticket in [docs/tickets/](../) |

**A ticket is archived only after everything durable in it has been moved out**
([ADR 0034](../../adr/0034-tickets-are-work-lists-that-get-archived.md)). If you find a rule here
that no ADR holds, that is a process defect from the day it was archived — lift it into an ADR
rather than citing this file.

**Three files here predate the one-file-per-ticket rule.** 037, 038 and 039 are multi-item
analysis records — the admission-webhook recovery note whose `NA1`–`NA63` findings became the
ADRs, the pre-upgrade analysis of the 1.10.48 fleet, and the findings of the 1.11.0 fleet rollout
with T1–T29 and the retired board — and were numbered and archived on 2026-09-27, when the
tickets were numbered. The open items of 039 were moved into their own tickets that day; their
headings in 039 are stubs. Some links inside 037 and 038 were already broken
when they were archived, because those documents were written against another path base.
**Not verified:** that every decision in the files archived on 2026-09-27 is held by an ADR. It
was not re-checked when they were moved here.

The reference ban is unchanged: nothing outside `docs/tickets/` may reference a ticket, archived
or live. It is partly implemented — the citations that predate it are listed in
[ticket 040](../040-tracked-files-cite-work-items-instead-of-adrs.md); see
[the tickets README](../README.md#nothing-outside-this-directory-references-a-ticket).

This directory is excluded from the knowledge-graph corpus:
[`.graphifyignore`](../../../.graphifyignore) lists `docs/tickets/archive` (verified 2026-09-27),
so that a finished plan is not surfaced as an answer about the operator. **Not verified:** that
the graph in `graphify-out/` has been rebuilt since the entry was added.
