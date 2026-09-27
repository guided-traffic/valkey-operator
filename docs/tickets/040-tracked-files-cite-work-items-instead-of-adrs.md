# Ticket: ~~replace the dangling `NA…` ticket references with ADR references~~ rewrite every ticket citation outside `docs/tickets/` to the ADR that holds the rule

Ticket 040, formerly C3 (`local_na_references_to_adr.md`); renamed on 2026-09-27 when the tickets
were numbered.

> **Status: original scope DISCHARGED, a later residue is OPEN, and since 2026-09-27 the scope
> is wider: this is the family ticket for the reference ban of
> [ADR 0034](../adr/0034-tickets-are-work-lists-that-get-archived.md), and every ticket citation
> outside `docs/tickets/` is its work list — see
> [the section of 2026-09-27](#2026-09-27-the-t-label-citations-join-this-ticket). The residue
> below was verified 2026-08-26 on `HEAD` = `1c309d8`.** *(Added 2026-09-27: re-measured on
> `HEAD` = `4a7543e`. ~~Two decisions are open, and two XS items need neither of them;~~
> *(corrected 2026-09-27 at `84a39c2`: four decisions are open, and six work items need none of
> them, see the note below;)* see
> [Current state](#current-state-2026-09-27-head--4a7543e), [Options](#options) and
> [Work list](#work-list). ~~Urgency: `later` (re-derived 2026-09-27, rule 4: the rewrite is decided
> and mechanical).~~ *(corrected 2026-09-27 at `84a39c2`: urgency is `now`, rule 1, see the note
> below.)* Effort: L.)* *(2026-09-27, later: work item 1, the 8 `NA` noun uses, landed;
> item 2 still waits on Hans for the `CLAUDE.md` edit, ~~and both decisions are still open~~
> *(corrected 2026-09-27 at `84a39c2`: four decisions are open, see the note below)*; see
> History.)* ~~Index:
> [`archive/039-findings-from-the-1-11-0-fleet-rollout.md`](archive/039-findings-from-the-1-11-0-fleet-rollout.md) (archived 2026-09-27, no longer maintained).
> Keep this line current — update it in the same change that touches this ticket.~~
> *(corrected 2026-09-27 at `84a39c2`: the Index line is obsolete. The board is retired, and for
> 040–042 this Status blockquote is itself the index,
> [docs/tickets/README.md](README.md#there-is-no-index-table-and-no-board) `:111-117`.)*
>
> *(Re-verified 2026-09-27 at `84a39c2`, see
> [Re-verified 2026-09-27 at 84a39c2](#re-verified-2026-09-27-at-84a39c2): work item 1 is committed
> in `bcc63c9`. **Four decisions are open** (1, 2, 3 and 4 under [Options](#options); decide 2
> first, then 1, 4 and 3). **Severity low, security none, effort L.** **Urgency `now`** by rule 1
> (first match): tracked files of this family carry measured-false statements — ADR 0003 `:112`
> and `:229` ("not in this repository"), ADR 0009 `:43` (the same), ADR 0034 `:14` and `:37-38`
> ("not committed yet"). It returns to `later` (rule 4: decided scope, mechanical rewrite) once
> work item 6 lands. Blocked by: the decisions, for work items 3, 4 and 5; items 2, 6, 7, 8, 9
> and 10 are not blocked by a decision.)*
>
> **Every number in the Context below is now wrong, in the good direction:**
>
> | This ticket claims | Measured 2026-08-26 |
> |---|---|
> | `grep -rn 'NA[0-9]' --include='*.go' . \| wc -l` = 170 | **5** |
> | `grep -rln 'NA[0-9]' --include='*.go' . \| wc -l` = 29 | **3** |
> | `grep -rn '_NA49_' --include='*.go' . \| wc -l` = 20 | **0** |
> | `grep -rn 'NA[0-9]' .github \| wc -l` = 2 | **0** |
>
> *(corrected 2026-09-27 at `84a39c2`: the two Go rows are **0** lines in **0** files since
> `bcc63c9`, which rewrote the last five, see work item 1. The 2026-08-26 column measured a state
> reached on 2026-08-21: commit `6140386` ("docs(comments): cite ADRs instead of the deleted
> admission-gap note", 2026-08-21 10:00) took the Go `NA` lines from 170 (`ab9403a`) to 0 and the
> workflow lines from 2 to 0, and `2d49762` (11:20 the same day) retired the T1–T5 and WP1–WP6
> identifiers from code comments. The 5 Go lines of 2026-08-26 were `NA61`/`NA63` lines written
> after that sweep.)*
>
> The whole `NA3`–`NA49` mapping table is discharged, including the two follow-ons it
> attached: the ten `_NA49_` test names are renamed, ADR 0016 `:147` cites the renamed test,
> and both "comments to fix while in there" are applied
> ([`ratelimiter.go:40-42`](../../internal/controller/ratelimiter.go#L40-L42) and
> [`:62-70`](../../internal/controller/ratelimiter.go#L62-L70),
> [`nudge.go:125-133`](../../internal/controller/nudge.go#L125-L133)).
>
> **What is open — and it is this ticket's own definition of done, still red.** A *later*
> batch the mapping table does not cover (it stops at `NA49`) reintroduced the same defect in
> **53 lines of tracked files**:
>
> * **5 Go lines** (`NA61`/`NA63`): [`test/integration/foreign_object_test.go:170`](../../test/integration/foreign_object_test.go#L170),
>   [`internal/controller/rolling_update.go:227`](../../internal/controller/rolling_update.go#L227) (`:273` on 2026-09-27),
>   [`internal/controller/foreign_object_test.go:318`](../../internal/controller/foreign_object_test.go#L318), `:913`, `:1062`
>   *(rewritten 2026-09-27 by work item 1, committed in `bcc63c9`)*
> * **7 lines** in `SECURITY_ARCHITECTURE.md` (`NA62`/`NA63`): `:237`, `:247`, `:597`, `:608`, `:615`, `:623`, `:631`
>   *(2026-09-27: that file is gone, split into [`docs/security/`](../security/README.md) by the
>   documentation restructure. Three of the seven lines moved verbatim, all into
>   [`isolation-and-tenancy.md`](../security/isolation-and-tenancy.md): `:163` (`NA62`) and
>   `:181` (`NA63`) under "What does not hold", `:216` (`NA62`) under gap
>   [H-9](../security/isolation-and-tenancy.md#h-9) *(renumbered 2026-09-27 after that page's
>   rootless bullets were shortened; before it, the H-9 line stood at `:239`, not `:229`)*. The other four stood in hardening-checklist
>   items already ticked as done, which the restructure did not carry over, so the residue is
>   49 lines, not 53 — see [the 2026-09-27 section](#2026-09-27-the-t-label-citations-join-this-ticket).)*
>   *(The three moved lines were rewritten by work item 1, `bcc63c9`.)*
> * **41 lines** in [`docs/adr/`](../adr/) (`NA61`/`NA62`/`NA63`)
>
> Mapping for the residue: `NA61` → ADR 0020 (amended 2026-08-22, StatefulSets and observer
> Deployment); `NA62` → ADR 0020 (every managed kind) and ADR 0006; `NA63` → ADR 0020 (pods).
>
> **Premise falsified:** this ticket says `local_valkey_operator_admission_gap.md` is "about
> to be deleted". ~~It is **still there**, 7544 lines, untracked and gitignored, five days on.~~
> *(corrected 2026-09-27: it is no longer at the repository root; it became the tracked
> [archive/037](archive/037-recovery-after-transient-admission-webhook-rejection.md), see
> [below](#2026-09-27-the-t-label-citations-join-this-ticket))*
> `NA61`–`NA63` are documented in it at `:7061`, `:7110` and `:7269` ~~— so the references resolve for
> Hans locally and dangle for every clone. That asymmetry is the whole defect.~~
> *(corrected 2026-09-27 at `84a39c2`: since `4a7543e` the file is tracked, so the labels resolve
> for every clone. The defect is now the citation itself, which ADR 0034 D7 forbids whether it
> resolves or not.)*
>
> **Not verified:** whether the `NA61`/`NA62`/`NA63` tags inside ADR "Amended … (NAxx)"
> headers are intentional provenance markers rather than oversights. No ADR or CLAUDE.md rule
> speaks to it; treating them as citations to rewrite is a judgement call, not a fact read off
> the tree. Decide it before starting, or the 41 ADR lines get churned twice. *(2026-09-27:
> [ADR 0034](../adr/0034-tickets-are-work-lists-that-get-archived.md) now records the question
> as undecided, under Alternatives, "Rewrite all existing citations in the same change". It is
> Decision 1 under [Options](#options).)* *(2026-09-27 at `84a39c2`: at the time they were kept on
> purpose. Commit `36fadd9` (2026-08-22, "docs(adr): drop the last reference to the untracked
> ticket file") replaced "Filed as NA63 in `local_valkey_operator_admission_gap.md`" with "Tracked
> as NA63; closed 2026-08-22 by D9" in ADR 0020: it dropped the file name and kept the label. That
> predates D7 and does not decide Decision 1.)*
>
> **Effort for the residue: XS.** *(corrected 2026-09-27: XS covered the 49 `NA` lines only.
> Since 2026-09-27 the work list is every ticket citation outside `docs/tickets/`, about 250
> lines, so the effort is L.)*

## Context

Until 2026-08-21 the architecture rationale of this operator lived in a working note,
`local_valkey_operator_admission_gap.md`, whose findings were numbered `NA1 … NA58`. That note
~~is **untracked, gitignored (`local_*`) and about to be deleted**. It was never part of the
repository and cannot be linked from it.~~ *(corrected 2026-09-27 at `84a39c2`: it was never
deleted. Since `4a7543e` (2026-09-27) it is the tracked
[archive/037](archive/037-recovery-after-transient-admission-webhook-rejection.md), and it can be
linked; ADR 0034 D7 forbids linking it from outside `docs/tickets/` all the same.)*

Its content has been moved into 18 Architecture Decision Records under
[`docs/adr/`](../adr/README.md). ~~The prose in `README.md`, `CLAUDE.md`,
`SECURITY_ARCHITECTURE.md` and the ADRs themselves no longer cites `NA…` numbers.~~
*(corrected 2026-09-27 at `84a39c2`: true at `ab9403a`, 2026-08-21 09:33, apart from the `_NA49_`
test name at ADR 0016 `:138`; false from commit `9f1efaa` on, 2026-08-21 16:46, which wrote the
first `NA61`/`NA62` lines into ADR 0020 and `SECURITY_ARCHITECTURE.md`. At `84a39c2` 41 ADR lines
cite `NA61`–`NA63`: 37 in ADR 0020 and 4 in ADR 0006.)*

~~**The code does.** 170 lines across 29 Go files, plus 2 in a workflow, still point at a
document that will not exist. A reader who greps `NA26` after the deletion finds a comment
explaining that some decision is "NA26" and no way to learn what that means.~~ *(corrected
2026-09-27 at `84a39c2`: true on 2026-08-21 before `6140386`, which rewrote all 170 Go lines and
both workflow lines the same morning. The document was not deleted either, see above.)*

Verified 2026-08-21 on branch `feat/support-pdb`:

```
$ grep -rn 'NA[0-9]' --include='*.go' . | wc -l
170                     # 150 comment lines + 20 lines carrying the _NA49_ identifier
$ grep -rln 'NA[0-9]' --include='*.go' . | wc -l
29
$ grep -rn '_NA49_' --include='*.go' . | wc -l
20                      # 10 test declarations and their call/reference sites
$ grep -rn 'NA[0-9]' .github | wc -l
2
```

## What to do

Rewrite every `NA…` citation as a reference to the ADR and decision that now carries it.
**The prose around the citation stays** — these comments explain real reasoning and several of
them are load-bearing (they are the only in-tree record of why a guard exists). Only the
pointer changes.

Form to use, matching how the ADRs cite each other:

```go
// before
// Outside a rolling update nothing else re-detects a split brain (NA26).

// after
// Outside a rolling update nothing else re-detects a split brain
// (docs/adr/0011-evidence-based-steady-state-split-brain-resolution.md, D1).
```

Keep it to one line where the original was one line; a bare `ADR 0011 D1` is acceptable where
the full path does not fit, as long as the number is there.

## Mapping

Every `NA` tag that occurs in the tree, and the decision that now owns it. The D-numbers were
read off the ADRs on 2026-08-21 and are stable — ADRs never renumber decisions.

| Tag | ADR | Decision |
|---|---|---|
| `NA3` | 0002 | D3–D6 — one phase authority per blocked pass |
| `NA4` | 0003 | D7, D8 — nudge ordering, no rolling-update suppression |
| `NA5` | 0001 | D7 — every reconcile exit keeps a retry clock |
| `NA6` | 0004 | D8 — sanity warnings are never gated on a write |
| `NA7` | 0004 | D7 — the Sentinel formula stays quorum-derived |
| `NA12` | 0014 | D7 — both events API groups are granted |
| `NA14` | 0006 | D1, D2 — ownerReference-based provenance |
| `NA15` | 0003 | D8 — duration, not state, is the discriminator |
| `NA16` | 0003 | D10 — unknown is not recovered |
| `NA17` | 0002 | D9 — every condition carries `ObservedGeneration` |
| `NA19` | 0007 | D2 — compare against the persisted template |
| `NA20` | 0008 | D4–D7 — publish before deleting, ranked init election |
| `NA21` | 0008 | D10, D11 — the resolver is fed a named authority |
| `NA23` | 0010 | D2–D4 — Phase 1 bound, abandon into Phase 2 |
| `NA26` | 0011 | D1 — the only split-brain check outside a rolling update |
| `NA27` | 0010 | D7, D8 — bounds are dual-stored, arming errors are not discarded |
| `NA28` | 0010 | D10 — arm on entry, never inherit |
| `NA29` | 0009 | D2, D3 — bounded conflict retry, then fail the pass |
| `NA30` | 0007 | D4 — the freshness guard compares the full template |
| `NA31` | 0006 | D8, D9 — UID precondition, never ResourceVersion |
| `NA32` | 0004 | D11 — the foreign-budget Warning is gated on the feature |
| `NA33` | 0002 | D7 — a failed status write never ends the pass |
| `NA34.2` | 0002 | D8 — steady state costs no status write |
| `NA35` | 0008 | D8, D9 — the self-claim, and why it ships with the check |
| `NA37` | 0006 | D12, D13 — GET-first, and grant plus guard in one change |
| `NA38` | 0017 | D25, D26 — e2e polling and race-window helpers |
| `NA39` | 0002 | D11 — `status.masterPod` from the `-rw` selector |
| `NA40` | 0010 | D10 — Phase 2 arms its own bound on entry |
| `NA42` | 0017 | D12 — a refusal guard needs a real API server |
| `NA45` | 0010 | D14 — a discarded arming error is a defect, not hygiene |
| `NA47` | 0010 | D6 — every manual-failover wait is bounded |
| `NA48` | 0002 | D10 — the condition must be clearable from the converged state |
| `NA49` | 0006 | D4–D11 — provenance gating of the legacy TLS material |

## Files

| File | Tags to rewrite |
|---|---|
| `api/v1/valkey_types.go` | NA49 |
| `internal/builder/init_script_exec_test.go` | NA35 |
| `internal/builder/statefulset.go` | NA35 |
| `internal/builder/statefulset_test.go` | NA35 |
| `internal/controller/certificate_reconcile_test.go` | NA31, NA37, NA49 |
| `internal/controller/condition_generation_test.go` | NA17, NA34.2 |
| `internal/controller/known_master_authority_test.go` | NA26, NA35 |
| `internal/controller/manual_failover_known_master_test.go` | NA20, NA29 |
| `internal/controller/nudge_test.go` | NA4, NA15, NA16 |
| `internal/controller/pdb.go` | NA6, NA7, NA31, NA32 |
| `internal/controller/pdb_test.go` | NA6, NA7, NA14, NA31, NA32 |
| `internal/controller/rbac_drift_test.go` | NA12 |
| `internal/controller/rolling_update.go` | NA21, NA23, NA26, NA27, NA28, NA30, NA40, NA47, NA48 |
| `internal/controller/rolling_update_blocked_write_test.go` | NA19 |
| `internal/controller/rolling_update_bounds_test.go` | NA26, NA27, NA28, NA30, NA40, NA45, NA47 |
| `internal/controller/rolling_update_test.go` | NA5 |
| `internal/controller/sidecar_pending_condition_test.go` | NA48 |
| `internal/controller/status_master_pod_test.go` | NA39 |
| `internal/controller/status_phase_test.go` | NA3, NA33 |
| `internal/controller/steady_state_master.go` | NA21, NA26, NA35 |
| `internal/controller/steady_state_master_test.go` | NA21, NA26, NA35 |
| `internal/controller/topology_restore_stall_test.go` | NA21, NA23 |
| `internal/controller/valkey_controller.go` | NA26, NA31, NA39, NA48, NA49 |
| `internal/controller/valkey_controller_test.go` | NA49 |
| `test/e2e/admission_recovery_test.go` | NA12 |
| `test/e2e/pdb_test.go` | NA12, NA14 |
| `test/e2e/topology_abandon_test.go` | NA23, NA38, NA45 |
| `test/e2e/two_replica_failover_test.go` | NA20 |
| `test/integration/pdb_uid_precondition_test.go` | NA31, NA42 |
| `.github/workflows/release.yml` | NA37 (line 640), NA44 (line 644) |

`NA44` occurs only in the workflow and maps to **ADR 0014 D5** — CI proves the generated
manifests are current.

## The ten test names

`internal/controller/certificate_reconcile_test.go` carries `_NA49_` in ten function names:

```
TestReconcileLegacySentinelCleanup_NA49_LeavesForeignSecretUnderLegacyName
TestReconcileLegacySentinelCleanup_NA49_NoSentinelStillGuardsTheDelete
TestReconcileLegacySentinelCleanup_NA49_LeavesForeignCertificate
TestReconcileLegacySentinelCleanup_NA49_LeavesForeignCertificateAndItsSecret
TestReconcileLegacySentinelCleanup_NA49_AnnotationAloneAuthorisesTheDelete
TestReconcileLegacySentinelCleanup_NA49_OwnedCertificateAuthorisesUnstampedSecret
TestReconcileLegacySentinelCleanup_NA49_OwnedCertificatePointingElsewhere
TestReconcileLegacySentinelCleanup_NA49_NonTLSTypeIsNeverDeleted
TestReconcileLegacySentinelCleanup_NA49_UIDPreconditionOnBothDeletes
TestReconcileLegacySentinelCleanup_NA49_CertificateConflictRevokesInPassProof
```

Drop the `NA49_` segment from all ten. A test name should say what it guards, and
`_NA49_` says nothing once the note is gone.

Two things ride on this rename and must move in the same change:

* `docs/adr/0016-authentication-and-tls-posture.md:138` cites
  `TestReconcileLegacySentinelCleanup_NA49_NoSentinelStillGuardsTheDelete` by name.
* Checked and clear: the CI `--- PASS:` grep guards in `.github/workflows/release.yml` name only
  `TestE2E_AntiAffinity_HardSpreadsAcrossNodes` and
  `TestE2E_PodDisruptionBudget_SerializesEvictions`, so no workflow filter breaks.

## Two comments to fix while in there

Both were found by an ADR verification sweep on 2026-08-21 and are wrong independently of the
renaming:

1. `internal/controller/ratelimiter.go` — the comments on `reconcileRetryQPS` /
   `reconcileRetryBurst` and on `newReconcileRateLimiter` call the token bucket
   "controller-runtime's default overall (not per-item) token bucket, kept unchanged". It is
   not kept, it is **added**: controller-runtime v0.24.1 uses the bare per-item exponential
   limiter whenever the priority queue is on, and the priority queue is on by default
   (`pkg/controller/controller.go:252-257`, `UsePriorityQueue` defaults true, and
   `SetupWithManager` does not set it). The `MaxOf(exponential, 10 qps / burst 100)` shape is
   client-go's `DefaultTypedControllerRateLimiter`. See ADR 0001 D6, which already states this
   correctly.
2. `internal/controller/nudge.go` — the `nudgeShortStatefulSets` doc comment claims
   "`replaceNextReplica`, `replaceRemainingPods`, `handleManualFailover` — every delete site
   requeues into a 'waiting for pod to be recreated' branch". `deleteNextPendingPod` has no
   missing-pod branch. See ADR 0003 D8, corrected there.

## Verification

*(Corrected 2026-09-27: the two `grep` lines and the note under them below are superseded by
the two commands of* Done when *at the end of
[the 2026-09-27 section](#2026-09-27-the-t-label-citations-join-this-ticket). `docs` now includes `docs/tickets/`,
`SECURITY_ARCHITECTURE.md` is gone, and this file and archive/037 are tracked, not gitignored.
The `make` lines stay. Because lint skips build-tagged files (T43), the comment edits under
`test/integration/` and `test/e2e/` are proven only by `make test-integration` and the e2e
compile run below.)* *(Re-read 2026-09-27 at `84a39c2`: `.golangci.yml` sets no build tags and
`make lint` runs `go vet ./...` and `golangci-lint run` without tags (Makefile `:81-86`), while
`test/integration/foreign_object_test.go:1` carries `//go:build integration` and
`test/e2e/topology_abandon_test.go:1` `//go:build e2e`, so the note holds. The e2e compile run
below was read, not run: the Makefile passes `-run '$(E2E_RUN)'` (`:158-162`), `test/e2e` has no
`TestMain` and no `init()`, and an empty `E2E_VALKEY_LINE` resolves to Valkey 9 in
`test/testimages/images.go` without a panic.)*

```bash
# no dangling references left anywhere
grep -rn 'NA[0-9]' --include='*.go' .        # expect: no matches
grep -rn 'NA[0-9]' .github Makefile deploy config docs \
  README.md CLAUDE.md SECURITY_ARCHITECTURE.md            # expect: no matches
# note: this ticket file and its sibling quote NA numbers by design; both are
# gitignored (local_*) and are deleted together with the note they describe.

# nothing else moved
make fmt && make vet && make lint            # 0 issues
make test-unit                               # green, zero SKIPs at -count=1
make test-integration                        # green
make generate-all && git status --porcelain  # clean tree
```

The e2e suite needs no run for this change — no test body and no production behaviour is
touched — but `make test-e2e E2E_RUN='TestE2E_CompileCheckOnly_NoSuchTest'` is worth doing to
prove the e2e package still compiles after the `test/e2e/` comment edits.

## Out of scope

* Rewording the comments beyond the citation. If a comment is wrong, fix it under its own
  item — the two above are named because they were already found, not because this ticket is a
  comment audit.
* Adding new ADRs. Every tag in the mapping already has a home.
* Touching `local_valkey_operator_admission_gap.md`. ~~It is untracked and is being deleted.~~
  *(corrected 2026-09-27 at `84a39c2`: it is the tracked archive/037 since `4a7543e`, and an
  archived file is not updated (ADR 0034 D2), so touching it stays out of scope.)*

## 2026-09-27: the T-label citations join this ticket

**Decided by the owner on 2026-09-27, while the tickets were numbered:** the rule that nothing
outside `docs/tickets/` references a ticket
([ADR 0034](../adr/0034-tickets-are-work-lists-that-get-archived.md)) is adopted **as partly
implemented**. No new ticket citation is written anywhere outside `docs/tickets/`; the citations
that exist today stay until this ticket rewrites them. The decision is taken — option B of four
put to the owner — ~~so no `## Options` section is owed here.~~ *(corrected 2026-09-27: two
narrower decisions inside it are still open, see [Options](#options).)*

**What joins the work list.** Every `T<n>` label cited outside `docs/tickets/`, and every path
into this directory from outside it. They are the same defect as the `NA…` residue: a tracked
file citing a work item instead of the rule. Once the numbered ticket files are committed they
resolve for every clone, which removes the asymmetry the status above calls "the whole defect" —
and discharges nothing, because the ban forbids citing a ticket from outside `docs/tickets/`
whether the citation resolves or not.

**Measured 2026-09-27 on `HEAD` = `f5c6886`.** Run against `HEAD` rather than the working tree,
because the working tree was being restructured at the time; the same commands without `HEAD`
measure the working tree.

```
$ git grep -nwE 'T[0-9]{1,2}' HEAD -- ':!docs/tickets' | wc -l
200
$ git grep -nwE 'T[0-9]{1,2}' HEAD -- docs/adr | wc -l
110
$ git grep -nwE 'T[0-9]{1,2}' HEAD -- '*.go' | wc -l
70
$ git grep -nwE 'T[0-9]{1,2}' HEAD -- README.md CLAUDE.md SECURITY_ARCHITECTURE.md | wc -l
20        # README.md 0, CLAUDE.md 7, SECURITY_ARCHITECTURE.md 13
$ git grep -nE 'docs/tickets|\.\./tickets/' HEAD -- ':!docs/tickets' | wc -l
8         # all in docs/adr: 0012:286, 0017:612, 0025:444, 0025:496, 0028:28, 0032:6, 0033:6, 0033:750
$ git grep -nE 'NA6[123]' HEAD -- ':!docs/tickets' | wc -l
53
```

The three areas sum to the total, so nothing else in the tree cites a T-label. Per label
(occurrences, `git grep -nowE`): T31 49, T32 47, T24 31, T10 14, T27 12, T34 11, T7 6, T17 6,
T18 6, T23 5, T12 4, T13 3, T21 3, T28 3, T6 2, T11 2, T15 2, T16 2, T25 2, T1 1, T4 1, T5 1.
The labels of the two embargoed findings, T26 and T30, occur nowhere outside `docs/tickets/`.

**Measured again 2026-09-27, 09:21 CEST, on the working tree**, after the documentation
restructure had moved its text and while the ADR and `CLAUDE.md` edits of the same day were
still being written, so a later run may differ. `git grep` without `HEAD` reads only tracked
files, and the pages the restructure created (`docs/security/`, `docs/developer/`,
`docs/operations/`, `DEVELOPER.md`, `SECURITY.md`) were untracked at the time, so every
working-tree command ran with `--untracked`, which still honours `.gitignore`:

| Command, all with `-- ':!docs/tickets'` unless another path is named | `HEAD` = `f5c6886` | working tree, 2026-09-27 09:21 | working tree, 2026-09-27 10:24 |
|---|---|---|---|
| `git grep -nwE 'T[0-9]{1,2}'` | 200 | 200 | 190 |
| the same, `-- docs/adr` | 110 | 110 | 110 |
| the same, `-- '*.go'` | 70 | 70 | 70 |
| the same over the root documents: `README.md CLAUDE.md SECURITY_ARCHITECTURE.md` at `HEAD`, `README.md CLAUDE.md SECURITY.md DEVELOPER.md` in the tree | 20 (`CLAUDE.md` 7, `SECURITY_ARCHITECTURE.md` 13) | 7 (`CLAUDE.md` 7) | 7 (`CLAUDE.md` 7) |
| the same, `-- docs/security docs/developer docs/operations` | did not exist | 13, all in `docs/security/` | 3, all `T31` in `docs/security/rootless-migration.md` |
| `git grep -nE 'NA6[123]'` | 53 | 49 | 49 |
| `git grep -nE 'tickets/(archive/)?(local_)?[0-9A-Za-z_-]+\.md'`, minus `tickets/README.md` | 8 | 8 | 8 |

Per label (`git grep -nowE`) the working tree gives exactly the `HEAD` numbers above, and T26 and
T30 still occur nowhere outside `docs/tickets/`. The four areas sum to the total, so nothing
else cites a T-label.

**The root file set changed.** `SECURITY_ARCHITECTURE.md` is gone; the restructure split it into
[`docs/security/`](../security/README.md) plus a root `SECURITY.md`, and its 13 T-label lines
moved there verbatim, none added and none dropped. They join this work list at their new place
(line numbers of 2026-09-27, 09:21):

- **`T31`, ~~12~~ ~~11~~ ~~9~~ 3 lines:** *(corrected 2026-09-27, see the notes after this list)* [`rootless-migration.md`](../security/rootless-migration.md) ~~`:41`, `:61`,
  `:99`~~ `:27`, `:47`, `:85` *(renumbered 2026-09-27, 10:24; the struck numbers are those of
  09:21)*; ~~[`privilege-footprint.md`](../security/privilege-footprint.md) `:144` (gap
  [H-1](../security/privilege-footprint.md#h-1));
  [`rotation-and-change-propagation.md`](../security/rotation-and-change-propagation.md) `:72`
  ([H-24](../security/rotation-and-change-propagation.md#h-24));
  [`operator-pod-posture.md`](../security/operator-pod-posture.md) `:98`
  ([H-13](../security/operator-pod-posture.md#h-13)), `:108`
  ([H-14](../security/operator-pod-posture.md#h-14)), `:125`
  ([H-15](../security/operator-pod-posture.md#h-15));~~ *(gone 2026-09-27, by 10:24, see the
  third note after this list; the three `operator-pod-posture.md` lines had moved to `:92`,
  `:102` and `:119` before they went, through that page's own review edits of the same day —
  as that review reported it, not re-measured here)*
  ~~[`secrets-and-tls.md`](../security/secrets-and-tls.md) `:192`
  ([H-6](../security/secrets-and-tls.md#h-6)), `:200` ([H-7](../security/secrets-and-tls.md#h-7));~~
  *(gone 2026-09-27, 10:09, see the second note after this list)*
  ~~[`seccomp-profiles.md`](../security/seccomp-profiles.md) `:190`
  ([H-19](../security/seccomp-profiles.md#h-19))~~ *(gone 2026-09-27, by 10:24, see the third
  note after this list)*~~;
  [`workload-pod-posture.md`](../security/workload-pod-posture.md) `:166`
  ([H-16](../security/workload-pod-posture.md#h-16))~~.
- ~~**`T34`, 1 line:** [`workload-pod-posture.md`](../security/workload-pod-posture.md) `:252`
  ([H-16](../security/workload-pod-posture.md#h-16)).~~ *(Gone 2026-09-27, see the note after
  this list.)*
- **`NA62` and `NA63`, 3 lines:** [`isolation-and-tenancy.md`](../security/isolation-and-tenancy.md)
  ~~`:163`~~ `:160` (`NA62`) and ~~`:181`~~ `:178` (`NA63`) under "What does not hold",
  ~~`:216`~~ `:213` (`NA62`, gap
  [H-9](../security/isolation-and-tenancy.md#h-9)) *(renumbered 2026-09-27 after that page's
  rootless bullets were shortened; before it, the H-9 line stood at `:239`, not `:229`;
  renumbered again at 10:24, the struck numbers are the first renumbering)*. The four other `NA` lines of the old file
  (its `:1366`, `:1377`, `:1392`, `:1400` at `HEAD`) stood in checklist items already ticked as
  done, which the restructure did not carry over; that is the whole drop from 53 to 49.
  *(Rewritten 2026-09-27 by work item 1, committed in `bcc63c9`.)*
- ~~**One citation without a label**, which none of the commands finds:
  [`validation.md`](../security/validation.md) `:43` ends a bullet on the rejected-write state with
  "the whole reason this ticket family exists" (at `HEAD` `SECURITY_ARCHITECTURE.md:1169`). On
  its new page nothing says which family is meant; read in context it is the admission-webhook
  recovery work of [archive/037](archive/037-recovery-after-transient-admission-webhook-rejection.md)
  (an inference from the text, not recorded anywhere).~~ *(Gone 2026-09-27, found at 10:24: the
  bullet, now `validation.md:40-44`, ends on the reason the `ReconcileBlocked` condition exists
  and cites [ADR 0002](../adr/0002-surface-a-blocked-reconcile-on-the-cr.md); "ticket" occurs
  nowhere on that page.)*

*(Corrected 2026-09-27, 10:01 CEST.)* The body of gap
[H-16](../security/workload-pod-posture.md#h-16) was replaced by a gap statement that cites no
ticket; its run log stays in ADRs 0032, 0033, 0025 and 0017. That removed the `T31` line at
`workload-pod-posture.md:166` and the `T34` line at `:252`, and nothing else on this list. Measured
on the working tree at 10:01 with the same `--untracked` commands: 11 T-label lines in
`docs/security docs/developer docs/operations` (was 13), and per label outside `docs/tickets/`
`T31` 48 and `T34` 10 (was 49 and 11). The table above stays the 09:21 measurement; its totals
were not re-run, and other edits of the same day may move them further.

*(Corrected 2026-09-27, 10:09 CEST.)* Gaps [H-6](../security/secrets-and-tls.md#h-6) and
[H-7](../security/secrets-and-tls.md#h-7) lost their "Open follow-up (T31 …)" lead-ins; the work
those lead-ins carried is tickets [050](050-every-component-authenticates-with-the-one-cluster-password.md)
and [057](057-debug-and-module-commands-follow-the-image-default.md). That removed the `T31`
lines at `secrets-and-tls.md:192` and `:200`, and nothing else on this list. Measured on the
working tree at 10:09 with the same `--untracked` commands: 9 T-label lines in
`docs/security docs/developer docs/operations` (was 11), per label outside `docs/tickets/`
`T31` 46 (was 48), and 196 T-label lines outside `docs/tickets/` in all. Six more `T31` lines on
this list carry the same kind of lead-in — gaps H-1, H-13, H-14, H-15, H-19 and H-24, whose
work is now tickets 056, 048, 049, 058, 055 and 051 — and stay on this list until their pages
drop it. Line numbers in the list are still those of 09:21; several pages have moved since.

*(Corrected 2026-09-27, 10:24 CEST.)* Gaps [H-1](../security/privilege-footprint.md#h-1),
[H-13](../security/operator-pod-posture.md#h-13), [H-14](../security/operator-pod-posture.md#h-14),
[H-15](../security/operator-pod-posture.md#h-15), [H-19](../security/seccomp-profiles.md#h-19) and
[H-24](../security/rotation-and-change-propagation.md#h-24) lost their "open follow-up (T31 …)"
work items; each gap still states what is missing, and the work is tickets
[056](056-no-namespace-scoped-operator-mode.md),
[048](048-the-operator-metrics-endpoint-is-unauthenticated.md),
[049](049-no-networkpolicy-guards-the-operator-namespace.md),
[058](058-no-ci-gate-renders-the-chart.md),
[055](055-no-recommended-localhost-seccomp-profile-ships.md) and
[051](051-a-changed-cluster-password-reaches-no-running-pod.md). Read on the six gaps at 10:24:
none carries a T-label or "open follow-up". That removed six `T31` lines and nothing else on
this list; the three that stay are the `rootless-migration.md` lines, renumbered in the list.
Measured on the working tree at 10:24 with the same `--untracked` commands, recorded as the 10:24
column of the table above: 190 T-label lines outside `docs/tickets/` (was 196 at 10:09) — 110 in
`docs/adr`, 70 in Go files, 7 in `CLAUDE.md`, 3 in `docs/security docs/developer docs/operations`
(was 9) — so the four areas still sum to the total. Per label (`git grep -nowE`) `T31` 40 (was
46; that is 39 lines, because `docs/adr/0010-every-rolling-update-wait-is-bounded.md:34` carries
it twice) and `T34` 10; every other label gives its 09:21 number, and `T26` and `T30` occur
nowhere outside `docs/tickets/`. The two commands of *Done when* return 239 lines (the 190 plus
the 49 `NA` lines) and 54 lines: the 8 path citations plus 46 that name the directory or its
rules page and cite no ticket — besides the files *Done when* names, ADR 0035 (1 line), ADR 0036
(2) and [docs/adr/README.md](../adr/README.md) (1) now carry such lines as well.

Apart from these moved lines, the pages the restructure created cite no ticket (searched
2026-09-27 for T-labels, `NA` labels, ticket paths and "ticket" followed by a number). The eight
path citations are all still in
`docs/adr` (`0012:286`, `0017:612`, `0025:444`, `0025:496`, `0028:30`, `0032:6`, `0033:6`,
`0033:750`). At 09:21 the ADR edits of the same day had already retargeted them from the old
`local_…` names to the numbered files, and the seven that are links resolved (link check of the
five ADRs, same time); that repairs the links and leaves every one of the eight on this list.

**`NA61`–`NA63` now point into [archive/037](archive/037-recovery-after-transient-admission-webhook-rejection.md).** The 53 lines of the residue — 7 in
`SECURITY_ARCHITECTURE.md`, 4 in `docs/adr/0006`, 37 in `docs/adr/0020`, 3 in
`internal/controller/foreign_object_test.go`, 1 in `internal/controller/rolling_update.go`, 1 in
`test/integration/foreign_object_test.go` (same `HEAD`) — cite labels defined only in the source
document that used to be `local_valkey_operator_admission_gap.md` and is now ticket 037:
[NA61](archive/037-recovery-after-transient-admission-webhook-rejection.md#na61--the-data-and-sentinel-statefulsets-are-written-by-generated-name-with-no-ownership-check--done-2026-08-22), [NA62](archive/037-recovery-after-transient-admission-webhook-rejection.md#na62--servicemonitor-and-certificate-stamp-the-cr-ownerreference-onto-an-object-they-never-verified--done-2026-08-22-wider-than-filed) and [NA63](archive/037-recovery-after-transient-admission-webhook-rejection.md#na63--steady-state-pod-probes-and-commands-never-verify-pod-provenance-the-pod-door--done-2026-08-22-wider-than-filed). None of the 53 lines links to
that file or names it; each cites the bare label. So the rename broke nothing, and the residue
is unchanged.

**The Verification block above predates the rename.** Its second `grep` covers `docs`, which now
includes `docs/tickets/`, and its note that this file and 037 are gitignored no longer holds.
Exclude `docs/tickets` as the commands in this section do. Its file list also names
`SECURITY_ARCHITECTURE.md`, which no longer exists: `docs` covers its successor
`docs/security/`, and `SECURITY.md` and `DEVELOPER.md` are the new root documents to add.

**Done when** every citation outside `docs/tickets/` is rewritten to name the rule it stands for,
or the ADR that holds it (the form the Mapping above uses for the `NA…` tags), and
`git grep -nwE 'T[0-9]{1,2}|NA[0-9]+' -- ':!docs/tickets'` and
`git grep -nE 'docs/tickets|\.\./tickets/' -- ':!docs/tickets'` return only history: lines this
ticket lists by name, each with the reason it stays — for example a provenance marker in an ADR
`Amended` header, if that question under *Not verified* in the status above is decided that way.
*(Amended 2026-09-27:)* the second command now also finds lines that name the directory or its
rules page and cite no ticket — the ~~`docs/tickets/local_*`~~ `docs/tickets/**/local_*` line
of `.gitignore` *(corrected 2026-09-27: the line was widened the same day to reach `archive/`;
see History)*, the
`docs/tickets/archive` line of `.graphifyignore`, ADR 0034, the ticket rule in `CLAUDE.md`, and
the pointers to the rules page in `DEVELOPER.md`, `docs/developer/README.md` and
`docs/security/README.md`; 37 lines in all at 09:21 that day. Those stay and are not on this list.
The narrower command in the table above counts only paths that name a ticket file. Neither
command finds a ticket cited by its bare number ("ticket 040") or by the unlabelled phrase above.
*(Amended 2026-09-27 at `84a39c2`:)* use `T[0-9]+` instead of `T[0-9]{1,2}` in the first command:
ticket ids are `T` plus the ticket number ([README](README.md#naming-and-numbering) `:24-28`), so
`{1,2}` stops matching at ticket 100. It changes nothing today
(`git grep -nwE 'T[0-9]{3,}' HEAD -- ':!docs/tickets' | wc -l` = 0). The close also needs the two
read-through checks of work item 9, because about 38 citing lines match neither command (see
[Re-verified at 84a39c2](#re-verified-2026-09-27-at-84a39c2)).

## Current state (2026-09-27, `HEAD` = `4a7543e`)

*(2026-09-27 at `84a39c2`: the line numbers in this section are updated to `84a39c2`; at
`4a7543e` the ADR 0012 path citation stood at `:286` and the `api/v1/valkey_types.go` labels at
`:173`, `:249`, `:255`. The counts are as annotated.)*

**Verified** with `git grep` at `HEAD`, using the commands of the table above:

- **190 T-label lines outside `docs/tickets/`.** 110 are in `docs/adr` and 70 in Go files (26
  files). 7 are in [`CLAUDE.md`](../../CLAUDE.md): `:284`, `:568`, `:620`, `:785`, `:794`,
  `:1014`, `:1047`. 3 are in [`rootless-migration.md`](../security/rootless-migration.md): `:27`,
  `:47`, `:85`. The total and every per-label count equal the 10:24 column. `T26` and `T30`
  occur nowhere.
- **49 `NA6[123]` lines.** 37 are in ADR 0020 and 4 in ADR 0006. 3 are in
  [`isolation-and-tenancy.md`](../security/isolation-and-tenancy.md): `:160`, `:178`, `:213`.
  5 are in Go:
  [`foreign_object_test.go:318`](../../internal/controller/foreign_object_test.go#L318), `:913`,
  `:1062`, [`rolling_update.go:273`](../../internal/controller/rolling_update.go#L273) and
  [`test/integration/foreign_object_test.go:170`](../../test/integration/foreign_object_test.go#L170).
  *(2026-09-27, after work item 1, measured on the working tree: ~~49~~ 41 lines, all in ADRs -
  37 in ADR 0020 and 4 in ADR 0006. The 3 `isolation-and-tenancy.md` lines and the 5 Go lines
  are rewritten; `git grep -nE 'NA6[123]' -- ':!docs/tickets'` finds only the two ADRs.)*
- **T and `NA` labels together:** 151 lines in 18 ADRs, heaviest 0020 (37), 0026 (24), 0030 (19),
  0010 (13) and 0017 (12); and ~~75 lines in 28 Go files~~ *(2026-09-27, after work item 1: 70
  lines in 26 Go files; the ADR count is unchanged)*.
- **The 8 path citations are unchanged**, all in ADRs: `0012:292`, `0017:612`, `0025:444`, `:496`,
  `0028:30`, `0032:6`, `0033:6`, `:750`. The two commands of *Done when* return ~~239~~ *(231 after
  work item 1, 2026-09-27; the T-label count is still 190)* and 54 lines.
- **No label reaches a generated artifact.** The three in `api/v1/valkey_types.go` (`:175`,
  `:251`, `:257`) are doc comments on `ConditionType` constants, and `config/crd/bases/` carries
  none (grep).
- **New: the code demands new citations.**
  [`condition_registry_test.go:205`](../../internal/controller/condition_registry_test.go#L205)
  asserts the regexp `T\d+` on every `declaredGap`. The failure messages at `:128`, `:158` and
  `:206` tell the author to add a ticket reference, and the field doc at
  [`condition_registry.go:84-86`](../../internal/controller/condition_registry.go#L84-L86) calls
  it "the ticket reference". [ADR 0027](../adr/0027-conditions-are-levels-edges-or-history.md) D4
  (`0027:198-205`) decides it. So every future gap has to break ADR 0034 D7. The one gap today,
  the `Ready` row ([`condition_registry.go:102`](../../internal/controller/condition_registry.go#L102)),
  suppresses no assertion that would fail. The only skips on `declaredGap` are
  `condition_registry_test.go:122` (edges) and `:149` (levels). `Ready` is a level, so `:149`
  does skip its one-evaluator assertion, but it declares `evaluators: 1`
  (`condition_registry.go:98`) and would pass it. *(Precised by the review of 2026-09-27: the
  enrichment said `:149` skips only levels with more than one evaluator; it skips every level
  that declares a gap.)*

**Not verified:**

- ~~That archive/039 gives an unambiguous ADR and decision for every label T1–T29; it was not read
  through.~~ *(corrected 2026-09-27 at `84a39c2`: it does not. T1–T5 are ambiguous: archive/037
  defines its own T1–T5 as test scenarios (`:139`, `:162`, `:168`, `:175`, `:871`), and archive/039
  defines T1–T29 as findings (T1 at `:350`). ADR 0003 `:111` ("T1's recovery target") cites
  archive/037's T1 (`:253`, "recovery ≤ 30 s"), not archive/039's T1 (stale Sentinel peers). T4 and
  T5 at ADR 0025 `:496-497` are archive/039's (`:1142`, `:1604`). Every other cited label (T6–T28)
  has one heading, in archive/039. See work item 8.)*
- Which label lines are tags and which carry meaning (the split Decision 1 turns on). A rough
  regex puts 56 of the 151 ADR lines in tag shape; they were not classified by hand, and neither
  were the Go lines. *(2026-09-27 at `84a39c2`: the figure depends on the pattern; a second rough
  pattern gives 51. Read it as about 50–56. Many ADR 0020 lines are handles, not tags, see
  Decision 1.)*
- No `make` target was run.

### Re-verified 2026-09-27 at `84a39c2`

Read-only `git` at `HEAD` = `84a39c2`, and reading of code, configuration and CI files. No `make`
target, no test and no docker run: nothing here depends on Valkey behaviour.

**Verified:**

- **The original scope is done, and was done on 2026-08-21.** `grep -rn 'NA[0-9]' --include='*.go' . | wc -l`
  = 0, `grep -rn 'NA[0-9]' .github | wc -l` = 0, `grep -rn '_NA49_' --include='*.go' . | wc -l` = 0.
  `certificate_reconcile_test.go` has 17 `func TestReconcileLegacySentinelCleanup…`, none with
  `NA49`; ADR 0016 `:147` names
  [`TestReconcileLegacySentinelCleanup_NoSentinelStillGuardsTheDelete`](../../internal/controller/certificate_reconcile_test.go#L622).
  `git grep -nE 'NA[0-9]' <commit> -- '*.go' | wc -l` gives 170 at `ab9403a` and 0 at `6140386`
  (2026-08-21 10:00); `.github` 2 and 0. The corrected comments are at
  [`ratelimiter.go:40-42`](../../internal/controller/ratelimiter.go#L40-L42), `:67-70`
  ("The bucket is added, not kept") and
  [`nudge.go:129-131`](../../internal/controller/nudge.go#L129-L131).
- **Work item 1 is committed** in `bcc63c9` ("docs: correct comments and records that the code
  contradicts"), not only present in the working tree; `git diff 4a7543e HEAD -- ':!docs/tickets'`
  adds no T or `NA` label line.
- **Every count of Current state reproduces.** `git grep -nwE 'T[0-9]{1,2}' HEAD -- ':!docs/tickets' | wc -l`
  = 190; `docs/adr` 110; `'*.go'` 70 in 26 files; `CLAUDE.md` 7 at the lines listed;
  `docs/security/rootless-migration.md` `:27`, `:47`, `:85`. Per label (`git grep -howE … | sort | uniq -c`):
  T32 47, T31 40, T24 31, T10 14, T27 12, T34 10, T7/T17/T18 6, T23 5, T12 4, T13/T21/T28 3,
  T6/T11/T15/T16/T25 2, T1/T4/T5 1; no T26 and no T30. `git grep -cE 'NA6[123]' HEAD -- ':!docs/tickets'`:
  ADR 0006 4, ADR 0020 37. ADR label lines: 151 in 18 ADRs. The 8 path citations
  (`git grep -nE 'tickets/(archive/)?(local_)?[0-9A-Za-z_-]+\.md' HEAD -- ':!docs/tickets'`): ADR
  0012 `:292` (moved from `:286` by `bcc63c9`), 0017 `:612`, 0025 `:444`, `:496`, 0028 `:30`, 0032
  `:6`, 0033 `:6`, `:750`; the seven links resolve, the anchor of 0017 `:612` included. *Done when*
  gives 231 and 54; the 54 are the 8 path citations plus 46 rule or pointer lines (ADR 0034 30,
  `CLAUDE.md` 6, `DEVELOPER.md` 2, ADR 0036 2, and 1 each in `.gitignore`, `.graphifyignore`, ADR
  0035, `docs/adr/README.md`, `docs/developer/README.md`, `docs/security/README.md`). The `f5c6886`
  baseline (200 / 110 / 70 / 20 / 8 / 53) was re-run by the audit of this run and matches.
- **No citation was written after the ban.** The lines added by `git diff f5c6886 HEAD -- ':!docs/tickets'`
  that carry a T or `NA` label are 8: 5 with text not present at `f5c6886`, all retargeted path
  citations (ADR 0012 `:292`, 0017 `:612`, 0025 `:496`, 0028 `:30`, 0033 `:750`), and the 3
  `rootless-migration.md` lines moved verbatim. `bcc63c9` adds none. `4a7543e` did add
  unlabelled mentions of "the admission-gap ticket" at ADR 0005 `:96-101`, `:414-415`,
  `:422-424` and `:458-459` (Decision 4).
- **Three statements of this family are measured false** (rule 1). All three became false with
  `4a7543e`, which made archive/037 tracked (`git ls-tree -r --name-only 4a7543e -- docs/tickets/archive/`
  lists `037-recovery-after-transient-admission-webhook-rejection.md`):
  - [ADR 0003](../adr/0003-nudge-a-short-of-pods-statefulset.md) `:111-113`: the 30 s target
    "comes from the admission-gap ticket and is not in this repository"; `:228-229`: "WP1's stated
    assumption — from the admission-gap ticket, which is not in this repository". The target is at
    archive/037 `:253`. ADR 0003 was last changed in `92db23c` (2026-08-21), when it was true. ADR
    0005 received the same correction on 2026-09-27 (`0005:95-101`); ADR 0003 did not.
  - [ADR 0009](../adr/0009-an-unrecorded-promotion-is-not-a-promotion.md) `:43`: "The review is not in
    this repository". archive/037 carries it: `:4330-4345` ("The adversarial review of this pass
    (2026-08-20)", "Findings 1, 3 and 4 were separate code sites, but the same defect") and
    `:3422-3424` ("found three separate defects (its findings 1, 3 and 4)"), and lists the two fix
    commits ADR 0009 `:43-44` names, `30588bd` and `744b589`, at `:6178` and `:6180`. ADR 0009 was
    last changed in `ab9403a` (2026-08-21).
  - [ADR 0034](../adr/0034-tickets-are-work-lists-that-get-archived.md) `:14` ("Implemented in the
    change that wrote this record, which is not committed yet") and `:37-38` ("The tickets become
    tracked only when the change is committed. Until then, every link … resolves only on the
    owner's machine"): committed in `4a7543e`. The residual risk at `:268-270` ("The owner reviews
    it before the change is committed") states as open what the commit has passed; whether the
    owner reviewed the text before it is not recorded. The amendment headers at `:40` and `:61`
    ("in the same uncommitted change") are dated history and may stay.
  - The same "which is not committed yet" sentence stands in ADR 0035 `:35` and ADR 0036 `:20`,
    with the same cause (records of the 2026-09-27 restructure written before its commit). Those
    two are outside the citation family; work item 6 keeps all three together so one finding is
    not split across two files.
- **About 38 citing lines match neither *Done when* command.**
  - 23 Go lines use labels defined only in archived tickets
    (`git grep -nwE '(E[1-9]|S[1-9]|F[1-9]|Q[1-9])' HEAD -- '*.go'`):
    [`pod_termination_test.go`](../../internal/controller/pod_termination_test.go) `:93`, `:139`,
    `:251`, `:294`, `:315`, `:390`, `:408`, `:442`, `:519`, `:548`, `:613`, `:643`, `:645`, `:751`,
    `:787`, `:860`, `:890`, `:931`, `:990`, `:1158`, `:1178` (E1–E6, S1, S3, S4, S7, S8 of
    archive/039's T5 analysis: S1 `:2159`, S3 `:2198`, S4 `:2223`, S7 `:2255`, S8 `:2264`, E1
    `:2474`); [`reconcile_steps_test.go:126`](../../internal/controller/reconcile_steps_test.go#L126)
    (F1, archive/037 `:97`);
    [`rolling_update_paused_condition_test.go:167`](../../internal/controller/rolling_update_paused_condition_test.go#L167)
    (Q2, archive/039 `:4999`).
  - 2 ADR lines carry WP labels (`git grep -nwE 'WP[0-9]+'`): ADR 0003 `:229` (WP1) and ADR 0005
    `:421` (WP5), both archive/037 work packages (`:213-:591`). `2d49762` retired these
    identifiers from code comments on 2026-08-21 and did not reach the ADRs.
  - 14 lines mention a ticket without any identifier
    (`git grep -niE 'ticket' HEAD -- ':!docs/tickets' ':!package-lock.json'`, minus the labelled
    and path lines, minus rule text): ADR 0003 `:112`, `:229`; ADR 0005 `:96`, `:101`, `:414`,
    `:422`, `:458`; ADR 0027 `:54` (borderline); ADR 0028 `:283`; ADR 0032 `:76`; ADR 0033 `:573`
    (struck), `:714`; [`test/e2e/topology_abandon_test.go:13`](../../test/e2e/topology_abandon_test.go#L13).
    ADR 0003 `:229` is also a WP line, so the three groups are 38 distinct lines, not 39.
  - The E/S/F/Q/WP sub-labels are cited labels exactly like `NA61`–`NA63`, which ADR 0034 Status
    (`:33-34`) already counts on this work list. Whether the unlabelled mentions are citations is
    not settled by D7's text; that is Decision 4.
- **Some labels have no one-to-one ADR home**, so the sweep needs a mapping table first (work item 8):
  - The E-labels are not ADR 0026's D-numbers. By content, read against the decision headings at
    `0026:212-445`: E1 → D1 (with the D2 carve-out), E2 → D4 (`countUpdatedPods`, `:248`), E3 → D5
    (the delete gate, `:262`), E4 → D9 (the manual-failover exemption, `:428`), E5 → D5, second
    half (the bounded observation), E6 → D6 (the Sentinel counters, `:332`), S7 → D7 (the ordinal
    range, `:364`). S1, S3, S4 and S8 are review findings with no D-number and have to be restated
    inline. Q2 → ADR 0002 D10b, F1 → ADR 0001.
  - The T24 sub-labels (a)–(d) are used inside ADR 0030 itself (`:41`, `:248`, `:272`, `:276`,
    `:278`, `:348`) and in Go: `tls_material.go` (7 lines), `tls_material_test.go` (6),
    `condition_registry.go:230`, `:243`, `api/v1/valkey_types.go:257`, `rw_service_report.go:34`,
    `rw_service_report_test.go:54`, `valkey_controller.go:571`,
    `test/integration/tls_material_test.go:164`. "The T24(d) neutrality style" names a pattern, the
    presence-guarded clear of ADR 0005 D10 (`0005:282`), which `condition_registry.go:74-76` and
    `condition_registry_test.go:131-133` already cite as "ADR 0005 D10".
  - "Row N of T32" ([`pod_availability_test.go:186`](../../internal/controller/pod_availability_test.go#L186),
    `:211`, `:249`, `:708`) points at the seven-row table of archive/032 (`:36-42`). No ADR carries
    that table.
  - The ADR 0026 Alternatives headings "(T32 Q1 B)", "(T32 Q1 C)", "(T32 Q2)", "(T32 Q3 (b))" and
    "(T32 Q3 (c))" (`:693-:717`) are option labels of the archived ticket.
- **ADR 0020 labels are handles, not only tags.** ADR 0020 has three amendments dated 2026-08-22:
  `:41` "(NA61)", `:46` "(NA62)", `:53` "(NA63)". The labels are the only names for them across
  the ADR — "the NA62 amendment adds" (`:206`), "The NA62 amendment sorts" (`:281`), the
  "(2026-08-22, NA62/NA63)" bullets (`:563-:595`, `:725`, `:767`), the "(NA62)"/"(NA63)"
  Alternatives headings (`:653-:699`) — and ADR 0006 `:21-23`, `:322-323` use them the same way.
  ADR 0026 has the same shape: two amendments on 2026-09-26, the second saying "after the T32
  amendment was committed" (`:121`).
- **Provenance is recoverable without a tag, but through `git log -S`, not `git blame`.**
  `git blame -L41,41 docs/adr/0020-write-only-what-the-operator-owns.md` returns `221b92b` (the
  `NA62` commit, "prove ownership before every write and every delete on a generated name"), which
  only rewrote that line; `git log -S'Amended 2026-08-22 (NA61)' -- docs/adr/0020-…` returns
  `69824bc` ("… onto foreign objects (NA61)"), the commit that shipped the amendment. After a
  rewrite `git blame` would name the rewrite commit.
- **The history of the regression.** `6140386` swept `NA3`–`NA49` on 2026-08-21 at 10:00;
  `9f1efaa` wrote the first 7 `NA61`/`NA62` lines (5 in ADR 0020, 2 in `SECURITY_ARCHITECTURE.md`)
  the same day at 16:46; the count (`git grep -nE 'NA6[0-9]' <c> -- ':!docs/tickets' | wc -l`) went
  7 → 13 (`69824bc`) → 36 (`221b92b`) → 52 (`995f186`), all by 2026-08-22 14:33. No tracked rule
  forbade a label then: `git show 69824bc:CLAUDE.md | grep -ncE 'NA[0-9]|ticket'` = 0.
- **The enforcement surface.** `grep -nE 'T\[0-9\]|NA\[0-9\]|ticket' Makefile .github/workflows/*.yml`
  finds no citation check, and the repository has no hook configuration. `make lint` runs in the
  `Code Linting` job (`.github/workflows/release.yml:497-519`), which runs on every push to and
  pull request against `main` with no path filter (`:3-10`) and is one of the twelve required
  contexts (ADR 0017 D47, `0017:533-539`). `git grep -cwE 'T[0-9]+|NA[0-9]+' HEAD -- go.sum package-lock.json`
  finds nothing today; `package-lock.json:4739` contains `NA8` inside a base64 integrity hash and
  matches only without `-w`.
- **The registry facts hold:** `condition_registry_test.go:122`, `:128`, `:149`, `:158`, `:200-210`;
  `condition_registry.go:17`, `:84-86`, `:98`, `:102`; only `condition_registry_test.go` consumes
  `declaredGap`. ADR 0027 `:198` ("must name the ticket item that owns the decision"), `:203` ("A
  gap with no `T<number>` reference fails the test") and `:119`. `CLAUDE.md:563` says the gaps were
  "declared in the registry with their ticket reference", `:568` "`Ready`/T18", `:620` "a
  pre-existing gap T32 does not close"; the residual risk is at ADR 0026 `:787-791`, which itself
  says "not fixed by T32" (`:791`).
- **Four open tickets are cited outside `docs/tickets/`**, 25 label lines plus one path:
  T12 at ADR 0025 `:443` (path `:444`), ADR 0028 `:123`, `:230`, `pod_termination_test.go:257`;
  T18 at `condition_registry.go:17`, `:102`, `CLAUDE.md:568`, ADR 0027 `:201`, `:252`, `:332`;
  T23 at ADR 0002 `:316`, ADR 0010 `:812`, ADR 0024 `:531`, ADR 0026 `:771`,
  `rolling_update.go:2616`; T34 at `CLAUDE.md:284`, `:1047` and ADR 0017 `:114`, `:119`, `:136`,
  `:584`, `:593`, `:604`, `:611`, `:612`. Their urgencies at `84a39c2`: 018 `now`, 012 and 034
  `next`, 023 `icebox`. The re-verification of those tickets in this run re-derived 034 to `now`
  and 023 to `now`, then `later`, then `icebox` once its own work items land (rule 5: its D1
  re-decides ADR 0010 D4), read in their working-tree frontmatter. Some of their ADR homes cite the ticket too: ADR 0010 `:812` "the pause's shape is
  T23's item", ADR 0026 `:791` "not fixed by T32".
- **ADR 0034 D7 already assigns every existing citation to this ticket** (`0034:162-166`: "The
  roughly 200 citations that already exist stay until they are rewritten. They are the work list
  of the family ticket of this rule"). The 25 open-ticket lines were among the 200.
- **The `CLAUDE.md` edit and who may make it.** No repository rule reserves `CLAUDE.md` edits for
  the owner; `CLAUDE.md:1092` asks agents to "persist important information about the project and
  implementation in this file", and `.claude/settings.json` holds only a graphify hook. The
  restriction the enrichment recorded is real but lives in the agent harness, not in the
  repository: an agent-orchestrated run is told that no agent message can authorize a change to
  `CLAUDE.md`. So a session in which Hans himself asks for the edit can make it; an orchestrated
  run limited to `docs/tickets/` cannot.
- **Cross-checks that hold:** T46 landed and is archived
  ([archive/046](archive/046-code-comments-name-a-missing-test-and-a-false-import-reason.md)); the
  gap-to-ticket mapping of the 10:09 and 10:24 notes (H-13 → 048, H-14 → 049, H-6 → 050, H-24 → 051,
  H-19 → 055, H-1 → 056, H-7 → 057, H-15 → 058) matches the most frequent gap id of each ticket; the
  CI `--- PASS:` guards are at `.github/workflows/release.yml:418-420`; the `NA` residue mapping
  holds (ADR 0020 `:41`, `:46`, `:464` D9 "(2026-08-22, NA63.)"; ADR 0006 `:21-23`, `:322-323`).

**Not verified:**

- Which ADR 0030 decision T24(a), (b) and (d) map to; only T24(c) → ADR 0030 D12 is stated in the
  tree (`tls_material_test.go:217`). Settled by reading archive/039's T24 against ADR 0030's
  decisions before the sweep (work item 8).
- The E and S label definitions in archive/039 were read by the design review of this run; this
  re-verification checked the mapping against ADR 0026's decision headings only.
- The hand classification of every label line into tag, handle and noun.
- Whether ADR 0005 `:100-101` ("the existing mentions of the admission-gap ticket stay") records an
  owner decision or the author's reading of D7 on 2026-09-27. Only the owner can settle it.
- Whether the owner reviewed the ADR 0034 text before `4a7543e` (`0034:268-270`).
- No `make` target, no test and no e2e compile run.

**Cross-ticket findings** (the tickets named were re-verified in the same run, and what that
changed is noted per bullet; ~~sibling line numbers are those at `84a39c2`~~ *(corrected
2026-09-27, sweep: sibling tickets are cited by section, because their line numbers drift)*):

- **T18** and Decision 2 meet on [`condition_registry.go:102`](../../internal/controller/condition_registry.go#L102).
  018 is urgency `now` because that string's "for the whole roll" is false, and 018 plans to keep
  the `T18:` prefix or follow this ticket's Decision 2 (018, Fact, bullet "The `:102` string
  cannot be made precise on its own"). Rewriting that string with
  the prefix after 2026-09-27 is a citation D7 forbids, so Decision 2 has to be decided before 018
  item 2 lands. 018's Reading 1 would also rewrite `CLAUDE.md:568` and ADR 0027 `:201`, `:252`,
  `:332`, the same lines as work item 2 and item 4 here. In this run 018 adopted the strict reading:
  the string lands with 018's own decision or with Decision 2 A, not with the prefix kept (the
  same 018 bullet).
- **T42**: 042's close grep `git grep -nwE 'T42|042|S1'` (042, Work list item 5, the close) would hit
  `pod_termination_test.go:408` ("the S1 regression guard"), which is archive/039's S1 (`:2159`),
  not 042's former label. Whichever ticket runs first: 040 rewrites `:408` (work item 8), or 042's
  grep carries that note (042 carries it since this run, in its Fact bullet "Close-step grep
  collision" and in Work list item 5). 042 also adds an ADR 0020 status note and residual risk,
  next to the 37 `NA` lines of ADR 0020.
- **T60**: 060 item 1 amended ADR 0020 on 2026-09-27 (in `bcc63c9`, which moved ADR 0020 lines),
  and its option work edits ADR 0020 again near `:477-479` and `:570-571`; coordinate with the ADR
  0020 rewrite here.
- **T23**: 023 lists its own citations for rewrite (023, Fact, bullet "T23 is cited outside
  `docs/tickets/`", and Work list item 6); its locations are
  stale after `bcc63c9` (ADR 0002 `:306` is now `:316`, `rolling_update.go:2615` is now `:2616`);
  023 corrected them in this run (the same Fact bullet).
- **T12** (012, Decision 1 and Work list, "Waits on Decision 1") and **T34** (034, Fact, the list
  "Verified 2026-09-27 at `4a7543e`", and Work list) each plan to rewrite their own
  citations. Work item 7 states who does what.
- **T43**: the Verification note above relies on T43's finding that `make lint` skips build-tagged
  files; it holds.

## Options

Four decisions. The scope decision of 2026-09-27 (option B,
[above](#2026-09-27-the-t-label-citations-join-this-ticket)) stands, and none of them reopens it.
The numbers are kept stable because ticket 018 cites "040, decision 2".

**Order: Decision 2 first**, because it is XS and ticket 018's item 2 (urgency `now`) rewrites the
one string the registry test guards; **then Decision 1**, which shapes the bulk of the ADR half;
**then Decision 4**, the scope of the unlabelled mentions; **then Decision 3**, the enforcement at
the close, which needs the sweep to bring the grep to zero first.

### Decision 2 — what a `declaredGap` in the condition registry has to name

**Mechanism.** `TestConditionRegistryGapsAreTraceable`
([`condition_registry_test.go:200-210`](../../internal/controller/condition_registry_test.go#L200-L210))
asserts the regexp `T\d+` (`:205`) on every row whose `declaredGap` is not empty. Its message
(`:206`), the messages at `:128` and `:158`, and the field doc at
[`condition_registry.go:84-86`](../../internal/controller/condition_registry.go#L84-L86) call the
gap "the ticket reference". [ADR 0027](../adr/0027-conditions-are-levels-edges-or-history.md) D4
(`0027:198-205`, and `:119`) decides that. ADR 0034 D7 (`0034:159-166`) forbids every new ticket
citation outside `docs/tickets/`. The only gap today is the `Ready` row
([`condition_registry.go:102`](../../internal/controller/condition_registry.go#L102)): "T18: …
(ADR 0001 D4 decides this; re-decision open)". It suppresses no assertion that would fail: the only
skips are `:122` (edges) and `:149` (levels), and `Ready` declares `evaluators: 1` (`:98`). The two
ADRs also collide at a ticket's close: ADR 0034 D3 (`0034:122-123`) clears a ticket's T-label when
the ticket closes, so a ticket that closes with its gap kept (dropped, or the risk accepted) has to
remove a label the test demands.

The choice changes the test regexp, its three messages, the field doc, ADR 0027 D4 and `:119`, and
the prefix of the `Ready` string. It changes no reconcile behaviour, and it does not decide 018's
re-decision of ADR 0001 D4; if 018's Reading 1 lands, the `Ready` row loses its gap and this choice
only governs future gaps.

- **A. The gap names the ADR that owns the exception.** **(recommended)** The test asserts
  `ADR \d{4}`, and additionally checks that a `docs/adr/NNNN-*.md` file exists for the number named,
  so a typo such as `ADR 0101` does not pass the way any `T` number passes today (the registry test
  already reads the tree: it parses `api/v1` with `go/parser`). The three messages, the field doc,
  ADR 0027 D4 and `:119` are amended in place. The `Ready` row drops `T18: `; its text already names
  ADR 0001 D4. A defect gap with no decision yet is first recorded under the owning ADR's Residual
  risks, which is the home the ADR format in `CLAUDE.md` gives open items. Cost XS, unit tier only.
  `CLAUDE.md:563` ("declared in the registry with their ticket reference") is history of 2026-08-26
  and may stay; `CLAUDE.md:568` ("`Ready`/T18") is work item 2.
- **B. Keep `T\d+` and add a carve-out for `declaredGap` strings to ADR 0034 D7.** No code change;
  cost XS, a D7 amendment. A gap fixed together with its ticket leaves no citation, because D3 clears
  the label at the close. A gap that outlives its ticket forces a choice between breaking D3 and
  failing the test. D7 gains its first exception in the week it was decided, and the Decision 3
  guard needs an exception for `condition_registry.go`.

**Recommended: A.** It keeps ADR 0027 D4's purpose — "an exception has to be traceable to a
decision" (`condition_registry.go:85-86`) — by pointing at the record that holds decisions, needs no
exception to D7, and breaks nothing today, because the `Ready` string already names ADR 0001 D4
(`:102`). It beats B because B saves one regexp and three messages and pays with the first carve-out
from D7, an unresolvable conflict with D3 for every gap that outlives its ticket, and a special case
in the Decision 3 guard.

### Decision 1 — is a ticket label used as a provenance tag rewritten, or kept as a listed exception?

**Mechanism.** A **tag** is a label that records where a statement came from and can be removed
without changing what the sentence says: `Amended 2026-08-22 (NA61):`, `Added 2026-09-26 (T32).`,
`*(corrected 2026-09-26, T34)*`, `(measured, T31)`, `(T24)`. Tags occur in ADRs (a rough regex puts
about 50–56 of the 151 ADR label lines in tag shape, not classified by hand), in Go comments (for
example [`internal/builder/pod_security.go:175`](../../internal/builder/pod_security.go#L175) and
`:197`, "(measured, T31)"), in `CLAUDE.md` `:284`, `:785`, `:794`, `:1047` (`:1014` sits in struck
text) and in `rootless-migration.md` `:27`, `:47`, `:85`. About ten lines carry the label inside
struck, superseded text (ADR 0030 `:459-526`, ADR 0020 `:771`, ADR 0025 `:395`) or in the dated
note next to struck text (ADR 0020 `:375` "Superseded in full on 2026-08-22 (NA62)", ADR 0017
`:119` "(T34)" after a struck phrase). ADR 0034 `:228-232` leaves exactly this question undecided for the ADR `Amended` headers.
A label used as a **noun** or **handle** ("the NA62 amendment adds", "`Ready`/T18", "the S1
regression guard") is rewritten under every option. In ADR 0020 the three same-day amendments are
known only by their labels (see [Re-verified](#re-verified-2026-09-27-at-84a39c2)), so there the date
alone does not identify an amendment. Provenance survives without the tag through
`git log -S '<header text>' -- <adr>` (ADR 0020's NA61 header → `69824bc`); `git blame` names only
the last commit that touched the line. At the time, the tags were kept on purpose (`36fadd9`,
2026-08-22, dropped a file name and kept the label), which predates D7.

The choice changes the shape of roughly 50–100 lines and whether *Done when* and the Decision 3
guard need an allow-list. It changes no code behaviour, and it does not change the rewrite of noun
and handle uses.

- **A. Rewrite a tag to the decision it records, like every other citation.** **(recommended)** An
  amendment tag becomes the decision it amended, the form the ADRs already use:
  `Amended 2026-08-23 (ADR 0023)` (`0020:33`), `Amended 2026-09-26 (ADR 0032 D2)` (`0017:710`),
  `(correction, no decision changes)` (`0001:13`). Where several amendments share a date, the scope
  or the ordinal names each one, as ADR 0012 does ("second amendment", "third amendment",
  `0012:37`, `:46`): for ADR 0020 `Amended 2026-08-22 (D1, StatefulSets)`,
  `(D1, every managed kind)`, `(D9, pods)`, and the handle nouns follow ("the NA62 amendment" →
  "the every-kind amendment of 2026-08-22"). A tag is dropped only where the header already names
  its decision. An evidence tag becomes the ADR that records the evidence
  (`rootless-migration.md:27` and `:85` → ADR 0032, whose measurement table is at `0032:142-145`).
  Struck text is treated the same, and the strike stays. Cost: part of the L sweep. The first
  *Done when* command ends at zero hits, so neither *Done when* nor a guard needs an allow-list.
  What is lost is the direct pointer from an amendment to its analysis in `archive/`;
  `git log -S` recovers the commit that shipped it.
- **B. Keep tags as listed exceptions, and amend ADR 0034 D7 to allow provenance tags.** *Done
  when* lists each kept line with its reason. The tags resolve, now that `archive/` is tracked.
  Cost: the sweep stays L, because noun and handle uses are rewritten anyway and B keeps only the
  roughly 50–56 tag lines of about 270; plus a permanent exception list whose line numbers move
  with every ADR edit. Every kept tag points into `archive/`, the failure ADR 0034 `:224-226` gives
  for rejecting "Do not adopt the reference ban", and the Decision 3 guard needs a fragile allow-list
  or a fuzzy pattern.

**Recommended: A.** It leaves a zero-hit grep, so *Done when* and the Decision 3 guard work with no
exception list, and it keeps the information a tag carries by naming the decision or the ADR that
holds the evidence (checkable: `0020:33`, `0017:710` and `0012:37` already use that form). It beats
B because B saves about a fifth of the lines, not a size class, and keeps about 50 pointers into
archived plans alive under a D7 carve-out, whose allow-list shifts with every ADR edit.

### Decision 4 — is a mention of a ticket without an identifier a citation?

**Mechanism.** D7 (`0034:159-161`) and [docs/tickets/README.md](README.md#nothing-outside-this-directory-references-a-ticket)
`:88-90` list the forbidden forms: a ticket's number, its T-label, its file name, its path. Fourteen
lines outside `docs/tickets/` mention a ticket with none of them (listed under
[Re-verified](#re-verified-2026-09-27-at-84a39c2)). They are of two kinds. Some send the reader to a
ticket for content — evidence, a list, a number, a rationale: ADR 0003 `:112` (the 30 s target),
`:229` (WP1's assumption), ADR 0005 `:414` ("the only record of that decision is the admission-gap
ticket"), `:422` (the work-package numbering "lives in" it), `:458` (the claim "comes from the review
record in" it), ADR 0028 `:283` ("the per-call-site reachability in the ticket"), ADR 0032 `:76`
("the runs are recorded in the ticket"), ADR 0033 `:714` ("from the ticket's list"), and
`topology_abandon_test.go:13` ("the one the ticket sketched"). The others are narrative: ADR 0027
`:54` ("in the course of analysing a stale-status ticket"), ADR 0005 `:96` and `:101` (the
correction note itself), ADR 0033 `:573` (struck). ADR 0005 `:100-101`, written on 2026-09-27 in
`4a7543e` together with D7, says "the existing mentions of the admission-gap ticket stay".

The choice changes about ten lines, ADR 0005 `:100-101`, and one sentence of D7 stating the reading.
It does not change what a grep can enforce: these mentions carry no identifier, so under either
option the close checks them by reading (work item 9). ADR 0003 `:112` and `:229` and ADR 0009 `:43`
are corrected under work item 6 either way, because they are false.

- **A. A mention that sends the reader to a ticket for content is a citation and is rewritten; a
  narrative mention is not.** **(recommended)** Each content pointer states the content in the ADR
  or points at its in-repo trace (for ADR 0003 `:112` the `admissionRecoveryDeadline` doc comment
  the ADR already names; for ADR 0032 `:76` the run results ADR 0032 carries). ADR 0005 `:100-101`
  is amended in place with the reason, and D7 gains one sentence stating the reading. Cost: about
  ten lines inside the L sweep, in ADRs the sweep edits anyway.
- **B. Only the four enumerated forms are citations.** ADR 0005 `:100-101` stands, and D7 gains one
  sentence stating that reading. Cost XS. ADR text that defers its evidence to "the ticket" stays,
  and without an identifier a reader cannot find that ticket at all.

**Recommended: A.** D7's own reason for the ban — a reader who follows a citation "lands in a plan
instead of a rule" (`0034:224-226`) — applies in full to "the runs are recorded in the ticket" (ADR
0032 `:76`), and more so, because without an identifier the reader cannot even find the plan. The
cost is about ten lines in files the sweep already edits. It beats B because B is cheaper only by
those lines and keeps ADRs whose evidence lives in an unnamed work list. Whether ADR 0005 `:100-101`
records an owner decision is not recorded; if it does, A reopens it, and the reason is the one above.

### Decision 3 — is ADR 0034 D7 enforced mechanically once the rewrite is done?

**Mechanism.** ADR 0034 records "Enforcement of D7 is manual" (`0034:262-263`). No Makefile target
and no workflow step searches for ticket citations, and there is no hook configuration. `make lint`
(Makefile `:81-86`) runs in the `Code Linting` job (`.github/workflows/release.yml:497-519`), on
every push to and pull request against `main`, with no path filter (`:3-10`); it is a required
context (ADR 0017 D47, `0017:533-539`). The convention has come back once already: `6140386` swept
the `NA` labels on 2026-08-21 at 10:00, and `9f1efaa` wrote new ones the same day at 16:46, 52 lines
by 2026-08-22 — though no tracked rule forbade a label then. Since D7 was written, no citation with
new text has been added. The live risk is prospective: authors edit files that still carry 231
labelled lines next to the text they write. Ticket ids are `T` plus the number, so the pattern must
be `T[0-9]+`. `go.sum` and `package-lock.json` have no `-w` hit today, but a base64 hash bounded by
`/` or `+` can produce one, so they are excluded.

The choice adds a standing check or not. No option covers commit messages or pull request bodies,
which D7 also binds, nor the archived sub-labels (E1, S1, WP1) or the unlabelled mentions; those
stay review items, and ADR 0034's residual risk must keep saying so.

- **A. Stay manual.** No cost. The residual risk stays as written, and a regression surfaces only
  at the next audit.
- **B. At the close, a `git grep` step in `make lint`.** **(recommended)** It fails on
  `git grep -nwE 'T[0-9]+|NA[0-9]+'` and on `git grep -nE 'tickets/(archive/)?(local_)?[0-9]{3}-'`
  outside `docs/tickets/`, excluding `go.sum` and `package-lock.json`, as a named target (for
  example `make check-ticket-citations`) that `lint` calls, so the non-Go check can be found by
  name. ADR 0034's residual risk is amended to say what it enforces and what it cannot, and the
  check is documented where the lint target is. Cost XS. CI fails on a new label or ticket path in
  any tracked file outside `docs/tickets/`. A docs-only commit pushed straight to `main` turns
  `main` red instead of being blocked. A future Go identifier named `T1` would trip it (none today;
  all 190 hits are citations). It needs Decision 1 A, or B's allow-list, and the sweep first.
- **C. A diff-scoped guard now.** On a pull request it compares `base...HEAD`, on a push
  `github.event.before...HEAD`, and fails on an added line carrying a label or ticket path. It
  enforces D7's "from 2026-09-27 on" during the sweep, whose date is open. Cost S. Its false
  positive is a touched line that keeps an old label (the 5 retargeted path citations since
  `f5c6886` would have tripped it), which forces the rewrite of any line that is touched. It needs
  event-specific base handling in CI, and B replaces it at the close.

**Recommended: B.** It costs XS, rides a context that is already required (so no branch-protection
change, ADR 0017 D47), is permanent, and turns the residual risk at `0034:262-263` into a checked
rule. It beats C, the runner-up, because C needs event-specific base handling and is temporary; its
protection matters only if the sweep is not scheduled soon. It beats A because A keeps a residual
risk for a citation shape that has already come back within seven hours of a sweep.

## Decision

The scope decision of 2026-09-27 stands (option B,
[above](#2026-09-27-the-t-label-citations-join-this-ticket)). Decisions 1, 2, 3 and 4 under
[Options](#options) are not yet decided.

## Work list

1. **[XS, no decision] The 8 `NA` noun uses outside ADRs and `CLAUDE.md`.** D7 is decided and
   none of these is a tag: *(**Done 2026-09-27**, with two deviations from the mapping below,
   see History: `:1062` and `rolling_update.go:273` cite ADR 0020 D8, not D1, and `:318` and the
   integration test cite D1 and D8.)* *(Committed in `bcc63c9`.)*
   - `internal/controller/foreign_object_test.go:318`: `the NA61 half of ADR 0020` becomes
     `ADR 0020 D1, amended 2026-08-22`.
   - `internal/controller/foreign_object_test.go:913`: `the NA63 half of ADR 0020` becomes
     `ADR 0020 D9`.
   - `internal/controller/foreign_object_test.go:1062`: `The NA61 StatefulSet guard` becomes
     `The ADR 0020 D1 StatefulSet guard`.
   - `internal/controller/rolling_update.go:273`: `The NA61 guard above` becomes
     `The ADR 0020 D1 guard above`.
   - `test/integration/foreign_object_test.go:170`: `is the NA61 half of ADR 0020:` becomes
     `is the StatefulSet half of ADR 0020 D1:`.
   - `docs/security/isolation-and-tenancy.md:160`: `Since the NA62 amendment of` becomes
     `Since the 2026-08-22 amendment of`.
   - `docs/security/isolation-and-tenancy.md:178`: `since the NA63 amendment:` becomes
     `since 2026-08-22:`. The sentence already cites ADR 0020 D9 at `:180`.
   - `docs/security/isolation-and-tenancy.md:213`: `The NA62 guard is not retroactive.` becomes
     `The ADR 0020 D1 guard on every managed kind (2026-08-22) is not retroactive.`
2. **[XS, no open decision, but not executable without Hans: it edits `CLAUDE.md`, and no agent
   run may change that file on its own authority]** *(precised 2026-09-27 at `84a39c2`: no
   repository rule says this — `CLAUDE.md:1092` asks agents to persist information there. The
   restriction is the agent harness's: an orchestrated agent run cannot take it, and a session in
   which Hans himself asks for the edit can.)* The two
   `CLAUDE.md` noun uses:
   - `:568`: `` `Ready`/T18 `` becomes `` `Ready` (ADR 0001 D4) ``. *(2026-09-27 at `84a39c2`: this
     line follows Decision 2 and ticket 018. If 018's Reading 1 lands first, that sentence is
     rewritten there and this proposed text would be false.)*
   - `:620`: `a pre-existing gap T32 does not close` becomes `a pre-existing gap, an ADR 0026
     residual risk`; the residual risk is at `0026:787`. *(2026-09-27 at `84a39c2`: ADR 0026 `:791`
     in that residual risk says "not fixed by T32" itself; rewrite it in the same edit, see item 7.)*
3. *(waits on Decision 2)* The registry test, its messages, the `Ready` row and ADR 0027 D4.
   *(2026-09-27 at `84a39c2`: and ADR 0027 `:119`; under Decision 2 A also the ADR file existence
   check. Decide before 018 item 2 rewrites `condition_registry.go:102`.)*
4. *(waits on Decision 1)* The 151 ADR lines and the 8 path links, the Go T-label lines,
   `CLAUDE.md` `:284`, `:785`, `:794`, `:1014` and `:1047`, and `rootless-migration.md` `:27`,
   `:47` and `:85`. Noun uses among them are rewritten either way. Do it as one comment sweep
   together with T46, and rewrite ADR 0020 before T42 touches it. *(2026-09-27: T46 landed and
   is archived, [archive/046](archive/046-code-comments-name-a-missing-test-and-a-false-import-reason.md),
   so the sweep no longer has a sibling to coordinate with; it cited no ticket in its new text.)*
   *(2026-09-27 at `84a39c2`: plus the 23 Go sub-label lines and the 2 WP lines, mapped per work
   item 8, and, under Decision 4 A, the content-pointer mentions. About 270 lines in all. Coordinate
   ADR 0020 with 042 and 060, see Cross-ticket findings.)*
5. Close ([ADR 0034](../adr/0034-tickets-are-work-lists-that-get-archived.md)):
   - Set D7 to implemented in ADR 0034 Status, and its State in `docs/adr/README.md`.
   - Update the "Partly implemented" paragraphs in `CLAUDE.md` and `docs/tickets/README.md`.
   - Run the two *Done when* greps and `git grep -nwE 'T40|040|C3'` outside `docs/tickets/`.
   - Move this file to `archive/`.
   - *(Added 2026-09-27 at `84a39c2`:)* Also update the other "known debt" statements that go stale
     when D7 is implemented: `DEVELOPER.md:449`, `docs/adr/README.md:26-28` (besides the State at
     `:112`), `docs/security/README.md:59-61`, ADR 0036 D5 `:116-117`, ADR 0034 D7 `:162-166` and
     its residual risk `:262-263` (per Decision 3), ADR 0005 `:100-101` (per Decision 4), and
     `docs/tickets/README.md:93-100`. Run the *Done when* greps with `T[0-9]+`, and the read-through
     checks of item 9. Under Decision 3 B, the guard lands in the same change.
6. **[XS, no decision, urgency `now`]** *(Added 2026-09-27 at `84a39c2`.)* Correct the measured-false
   statements in place, dated, naming no ticket path, as ADR 0005 did at `0005:95-101`:
   - ADR 0003 `:111-113` and `:228-229`: drop "is not in this repository", and rewrite the `T1` and
     `WP1` labels and the ticket mentions in the same edit (the in-repo trace of the 30 s target,
     `admissionRecoveryDeadline` in `test/e2e/admission_recovery_test.go`, is already named at
     `0003:112-115`).
   - ADR 0009 `:43`: "The review is not in this repository" is false; state the three defects and
     their fix commits (`30588bd`, `744b589`), which the ADR already names, without saying where the
     review lives.
   - ADR 0034 `:14`, `:37-38`, and the residual risk `:268-270`: the change was committed in
     `4a7543e`; whether the owner reviewed the text first is not recorded, so the residual risk says
     that instead of "before the change is committed". The headers at `:40` and `:61` are dated
     history and stay.
   - ADR 0035 `:35` and ADR 0036 `:20`, the same "which is not committed yet" sentence with the same
     cause. They are outside the citation family and sit here so one finding is not split; if the
     owner prefers a ticket of their own, all three "not committed yet" sentences move there
     together.
   - In the same ADR 0034 edit, keep the `f5c6886` counts at `0034:31-36` as a dated measurement,
     name the first *Done when* command, and discharge "Not verified: the citation counts on the
     committed tree" (`0034:271-273`) with the dated measurement at `84a39c2` (190 T-label lines,
     41 `NA` lines, 8 path citations). Do not write a "current" count: every commit of the sweep
     would make it stale.
   When this item lands, urgency returns to `later` (rule 4).
7. **[no decision] Citations of open tickets are rewritten here too.** *(Added 2026-09-27 at
   `84a39c2`.)* ADR 0034 D7 (`0034:162-166`) makes every existing citation this ticket's work list,
   so the 25 label lines and one path of T12, T18, T23 and T34 (listed under
   [Re-verified](#re-verified-2026-09-27-at-84a39c2)) are part of item 4. Whichever of 040 and the
   owning ticket lands first rewrites the line; the other verifies it with the ADR 0034 D3 close
   grep. Each points at the ADR that records the open item: T12 → the residual risks of ADR 0025
   (`:441-444`) and ADR 0028 (`:123`, `:230`); T18 → ADR 0001 D4; T23 → ADR 0010 `:808-813`; T32's
   `verifyNewMasterReady` gap → ADR 0026 `:787-791`. The T34 lines of ADR 0017 are mostly tags
   (`:114`, `:119`, `:136`, `:584`, `:593`, `:604`) and follow Decision 1. Where the ADR home itself
   cites the ticket (ADR 0010 `:812` "the pause's shape is T23's item", ADR 0026 `:791` "not fixed by
   T32"), the ADR sentence is rewritten in the same edit, or the citation only moves. Consistency
   edits to the work lists of 012, 018, 023 and 034 follow.
8. **[no decision] A mapping table before the sweep starts.** *(Added 2026-09-27 at `84a39c2`.)* Write
   it into this ticket first, from the facts under [Re-verified](#re-verified-2026-09-27-at-84a39c2):
   the E and S labels to ADR 0026 decisions (S1, S3, S4, S8 restated inline), F1 → ADR 0001,
   Q2 → ADR 0002 D10b, WP1 and WP5 → the facts they carry, T24(a)–(d) → ADR 0030 decisions (only
   T24(c) → D12 is known; the rest needs reading archive/039's T24), "the T24(d) neutrality style"
   → "the presence-guarded clear of ADR 0005 D10", "Row N of T32" → the row's meaning inline or a
   table in ADR 0026 D11, and the ADR 0026 `:693-:717` option labels. Resolve T1 at ADR 0003 `:111`
   against archive/037, not archive/039.
9. **[no decision] Two read-through checks for the close.** *(Added 2026-09-27 at `84a39c2`.)* No
   permanent guard can catch these forms:
   `git grep -nwE '(E[1-6]|S[1-8]|F1|Q2|WP[0-9]+)' -- ':!docs/tickets'` must return only ADR 0032's
   own option labels (`:431-432`, S2 and S3), and `git grep -niE 'admission-gap ticket|the ticket|ticket.s list'`
   outside `docs/tickets/` must return only rule text and, under Decision 4 A, narrative mentions.
   Both need reading, not only counting.
10. **[XS, no decision, inside `docs/tickets/`]** *(Added 2026-09-27 at `84a39c2`.)*
    [docs/tickets/README.md](README.md#naming-and-numbering) `:29-30` maps T1–T29 to archive/039 only;
    add that archive/037 defines its own T1–T5 (test scenarios) and WP1–WP6 (`:213-:720`), so a T1–T5 in a record
    older than 2026-08-21 may mean archive/037.

## History

- 2026-09-27: re-verified at `84a39c2` (read-only `git`, code, configuration and CI files; no
  `make` target, no test, no docker run). Checked: every count, location and claim of Status,
  Context, Current state, Options and Work list, against an audit and two skeptic reviews of this
  run, with the disputed points re-checked here. Details and commands are in the new
  [Re-verified at 84a39c2](#re-verified-2026-09-27-at-84a39c2) section.
  - **Found false or outdated, corrected in place:** the "Measured 2026-08-26" Go figures (0 since
    `bcc63c9`; the mapping scope was discharged on 2026-08-21 by `6140386`); "resolve for Hans
    locally and dangle for every clone" (archive/037 tracked since `4a7543e`); Context "untracked,
    gitignored and about to be deleted", "the ADRs themselves no longer cite `NA` numbers" (false
    from `9f1efaa`, 2026-08-21 16:46) and "The code does. 170 lines…"; Out of scope "It is untracked
    and is being deleted"; the obsolete Index line; the Not verified item on T1–T29 (T1–T5 collide
    between archive/037 and archive/039); the enrichment's "nothing is measured false" (ADR 0003
    `:112`, `:229`, ADR 0009 `:43`, ADR 0034 `:14`, `:37-38` are); "and both decisions are still
    open" (four are). Work item 2's reason is precised: no repository rule reserves `CLAUDE.md`, the
    restriction is the agent harness's.
  - **Locations re-read at `84a39c2`** and fixed directly: ADR 0016 `:147`, `ratelimiter.go:40-42`
    and `:62-70`, `nudge.go:125-133`, archive/037 `:7061`, `:7110`, `:7269`, ADR 0012 path citation
    `:292`, `api/v1/valkey_types.go` `:175`, `:251`, `:257`.
  - **Measured:** see the section; the new facts are about 38 citing lines neither *Done when*
    command finds (23 Go sub-label lines, 2 WP lines, 14 unlabelled mentions, ADR 0003 `:229`
    counted once), the ADR 0020 labels as handles, the `git log -S` provenance route, and the
    regression timeline.
  - **Options:** rewritten as four decisions (2, 1, 4, 3 in that order). Decision 3 (enforcement)
    and Decision 4 (unlabelled mentions) are new. The accumulated review text inside Options is
    replaced by the current analysis. Removed options:
    - Decision 1 **C** (replace each tag with the hash of the shipping commit): `git log -S`
      recovers that commit without an edit, before and after the rewrite, so a hand-copied hash per
      tag buys nothing, and for measurement tags the record is an ADR, not a commit. Its old
      argument "a hash in prose tells a reader no more than a label does" was false (a hash resolves
      in every clone) and is not reused.
    - Decision 2 **C** (drop the traceability test): removes the guard ADR 0027 D4 exists for, with
      no benefit A does not also give.
    - Proposed by the audit and not added as a decision, "who rewrites the citations of the open
      tickets T12, T18, T23, T34 — this ticket or each at its own close": ADR 0034 D7 (`0034:162-166`)
      already makes every existing citation this ticket's work list, so its option "leave them to
      their owners" contradicts D7 and would keep 040 unclosable while 023, whose decision
      re-decides ADR 0010 D4 and whose urgency ends in `icebox` (023 frontmatter), stays open. It
      is work item 7.
    - Proposed by the audit for Decision 3 and not kept, "a Go unit test that walks the repository":
      the same coverage as the grep at higher cost, and a file-system walk sees untracked local files.
  - **Recommendations:** Decision 1 A kept, but its content changed: "a bare tag is dropped, because
    the date already identifies the amendment" was false for ADR 0020 (three amendments on
    2026-08-22, known only by their labels), so A now replaces a tag with the decision it amended
    and drops it only where the header already names that decision; its justification now cites
    `git log -S`, because `git blame` names the wrong commit. B's "the rewrite drops to about M" was
    not supported and is corrected (B keeps about 50–56 of about 270 lines; still L). Decision 2 A
    kept; B's old cost "every future gap adds a citation that rots once its ticket is archived" was
    false (ADR 0034 D3 clears the label at the close), and B's real cost is the D3 conflict for a gap
    that outlives its ticket; A gains the ADR-file existence check. Decision 3: B recommended, C
    (diff-scoped guard) the runner-up; the pattern is `T[0-9]+`. Decision 4: A recommended.
  - **Work list:** item 1 recorded as committed in `bcc63c9`; items 2–5 annotated; items 6–10 added
    (6: the false statements, urgency `now`; 7: open-ticket citations; 8: mapping table; 9:
    read-through checks; 10: the T1–T5 caveat in the tickets README).
  - **Status (no frontmatter; the blockquote is the index):** urgency `later` → **`now`** by rule 1,
    because tracked files of this family carry measured-false statements; back to `later` (rule 4)
    once item 6 lands. Severity low (misleading and dangling pointers, three of them false; no
    operator behaviour). Security none (T26 and T30 occur nowhere outside `docs/tickets/`; the
    change touches comments, ADR text and one unit-test regexp). Effort L, unchanged (about 270
    lines). Blocked by the decisions for items 3–5 only.
  - **Review of this edit** (same run, spot-checked at `84a39c2`: the commit timeline and counts,
    ADR 0003, 0009, 0034, 0035, 0036 lines, the archive/037 and archive/039 definitions, the Go
    sub-label lines, the registry code, the Makefile and workflow lines): two strikethroughs that
    GitHub would not render were repaired; "Two decisions are open" in the first Status note
    corrected; the Current state section marked as carrying `84a39c2` line numbers; ADR 0020 `:375`
    and ADR 0017 `:119` precised as dated notes next to struck text, not struck text; archive/037
    defines WP1–WP6, not WP1–WP5; the ADR 0005 D10 citations in the registry precised; the
    cross-ticket findings annotated with what the same run changed in 018, 023, 034 and 042.
  - Sweep: The Cross-ticket findings cited 018, 042, 023, 012 and 034 by line number (`018
    :275-276`, `:355-361`; `042 :348`, `:554-557`, `:722-724`; `023 :121`, `:155`, `:208`,
    `:209-211`; `012 :292`, `:357`; `034 :434-436`, `:574`), several of which no longer showed the
    cited text; they now name the section and bullet (042's close grep is its Work list item 5, the
    collision note its Fact bullet "Close-step grep collision"). Frontmatter, options and work list
    unchanged.
- 2026-09-27: work list item 1 landed, file by file (read in `git diff` of the working tree):
  - [`internal/controller/foreign_object_test.go`](../../internal/controller/foreign_object_test.go):
    `:318` header "ADR 0020 D1 and D8, amended 2026-08-22"; `:913` "pods: ADR 0020 D9"; `:1062`
    "The ADR 0020 D8 StatefulSet guard".
  - [`internal/controller/rolling_update.go:273`](../../internal/controller/rolling_update.go#L273):
    "The ADR 0020 D8 guard above".
  - [`test/integration/foreign_object_test.go:170`](../../test/integration/foreign_object_test.go#L170):
    "covers ADR 0020 D1 and D8 for StatefulSets:".
  - [`docs/security/isolation-and-tenancy.md`](../security/isolation-and-tenancy.md): `:160`
    "Since the 2026-08-22 amendment of"; `:178` "since 2026-08-22:"; `:213` "The ADR 0020 D1
    guard on every managed kind (2026-08-22)", the H-9 paragraph rewrapped.

  Deviation from the mapping of the work item, checked and accepted: the guard `:1062` and
  `rolling_update.go:273` name is the `IsControlledBy` check in `dispatchDataRollingUpdate` that
  treats a foreign StatefulSet as absent, and ADR 0020 D8 (`:403`) lists
  `checkAndHandleRollingUpdate` among its guarded consumers (`:410`), so D8 is the decision it
  stands for; D1 governs writes. The section header and the integration test cover both a
  refused write (D1) and a consumer that treats the object as absent (D8). Measured afterwards:
  `grep -rnE 'NA6[123]' --include='*.go' .` and `git grep -nE 'NA6[123]' docs/security` print
  nothing; the `NA` residue is 41 lines in ADRs 0020 and 0006; the first *Done when* command
  returns 231 (was 239); the T-label count is still 190; no added line outside `docs/tickets/`
  cites a ticket (grep over the added lines of `git diff`). Work item 2 is untouched (it needs
  Hans). **Not verified:** `make lint` and `make test-unit` were not run for the comment edits.
  Urgency, effort and both decisions unchanged.
- 2026-09-27: adversarial review of the enrichment below. Spot-checked at `4a7543e`: every
  count of Current state, the 8 path citations, the 8 `NA` lines of work item 1 and the
  7 `CLAUDE.md` and 3 `rootless-migration.md` lines hold. One sentence precised: the `:149` skip
  covers every level with a gap, and `Ready` would pass the assertion it skips. Work item 2 is
  relabelled: it needs Hans for the `CLAUDE.md` edit, so the XS run does not take it. Both
  recommendations unchanged.
- 2026-09-27: enriched - re-measured on `4a7543e`: 190 T-label lines, 49 `NA` lines and 8
  path citations, every number unchanged since 10:24. New finding: the condition-registry test
  demands a `T\d+` citation in code (`condition_registry_test.go:205`, ADR 0027 D4), against
  ADR 0034 D7. Added Current state (Verified / Not verified), Options (two ordered decisions,
  each with a recommendation), Decision, and a work list with two XS items that need no
  decision. Stale text corrected in place: the title, the "still there" premise, the XS effort,
  "no Options section owed", and the Verification block. **Effort re-derived: L, was XS**; the
  XS covered only the 49 `NA` lines, and the scope has been about 250 lines since 2026-09-27.
  **Urgency re-derived: `later`, unchanged from the board row** (rule 4: the rewrite is decided
  and mechanical; nothing is measured false, and D7 accepts the existing citations as partly
  implemented). The title was "replace the dangling `NA…` ticket references with ADR
  references" and is kept struck through in the heading.
- 2026-09-27, 10:24 — the six remaining "open follow-up (T31 …)" work items on the security
  pages (gaps H-1, H-13, H-14, H-15, H-19 and H-24) were removed by the review of those pages;
  their work is tickets 048, 049, 051, 055, 056 and 058. The `T31` list above strikes them and
  renumbers the three `rootless-migration.md` lines that stay, the `NA62`/`NA63` entry is
  renumbered, the unlabelled `validation.md` citation is struck as gone, the table gains a 10:24
  column, and the third note after the list records the counts (`T31` 46 → 40, all T-label lines
  outside `docs/tickets/` 196 → 190).
- 2026-09-27, 10:09 — the eleven rows of the table "Further security measures — not in this
  change, each open" of [archive/031](archive/031-generated-pods-run-as-root.md#further-security-measures--not-in-this-change-each-open)
  were filed as tickets 048–058. The two `T31` citations of
  [`secrets-and-tls.md`](../security/secrets-and-tls.md) (gaps H-6 and H-7) left with their
  "open follow-up" lead-ins; the `T31` list above strikes them, and the second note after it
  records the new counts (`T31` 48 → 46 outside `docs/tickets/`).
- 2026-09-27 — the `.gitignore` line named in *Done when* above was widened from
  `docs/tickets/local_*` to `docs/tickets/**/local_*`, because the first line matched nothing
  in `archive/`, where a ticket that closes while still embargoed is moved with its prefix
  ([ADR 0034](../adr/0034-tickets-are-work-lists-that-get-archived.md) D2, D4). It is still one
  line and still cites no ticket, so the change adds no line to the result of the second command
  and nothing to this ticket's work list (the comment above the line names neither
  `docs/tickets` nor `../tickets/`).
