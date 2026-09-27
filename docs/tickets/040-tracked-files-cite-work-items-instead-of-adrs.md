# Ticket: ~~replace the dangling `NA…` ticket references with ADR references~~ rewrite every ticket citation outside `docs/tickets/` to the ADR that holds the rule

Ticket 040, formerly C3 (`local_na_references_to_adr.md`); renamed on 2026-09-27 when the tickets
were numbered.

> **Status: original scope DISCHARGED, a later residue is OPEN, and since 2026-09-27 the scope
> is wider: this is the family ticket for the reference ban of
> [ADR 0034](../adr/0034-tickets-are-work-lists-that-get-archived.md), and every ticket citation
> outside `docs/tickets/` is its work list — see
> [the section of 2026-09-27](#2026-09-27-the-t-label-citations-join-this-ticket). The residue
> below was verified 2026-08-26 on `HEAD` = `1c309d8`.** *(Added 2026-09-27: re-measured on
> `HEAD` = `4a7543e`. Two decisions are open, and two XS items need neither of them; see
> [Current state](#current-state-2026-09-27-head--4a7543e), [Options](#options) and
> [Work list](#work-list). Urgency: `later` (re-derived 2026-09-27, rule 4: the rewrite is decided
> and mechanical). Effort: L.)* *(2026-09-27, later: work item 1, the 8 `NA` noun uses, landed;
> item 2 still waits on Hans for the `CLAUDE.md` edit, and both decisions are still open; see
> History.)* Index:
> [`archive/039-findings-from-the-1-11-0-fleet-rollout.md`](archive/039-findings-from-the-1-11-0-fleet-rollout.md) (archived 2026-09-27, no longer maintained).
> Keep this line current — update it in the same change that touches this ticket.
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
> The whole `NA3`–`NA49` mapping table is discharged, including the two follow-ons it
> attached: the ten `_NA49_` test names are renamed, ADR 0016 `:138` cites the renamed test,
> and both "comments to fix while in there" are applied
> ([`ratelimiter.go:64-68`](../../internal/controller/ratelimiter.go#L64-L68),
> [`nudge.go:127-129`](../../internal/controller/nudge.go#L127-L129)).
>
> **What is open — and it is this ticket's own definition of done, still red.** A *later*
> batch the mapping table does not cover (it stops at `NA49`) reintroduced the same defect in
> **53 lines of tracked files**:
>
> * **5 Go lines** (`NA61`/`NA63`): [`test/integration/foreign_object_test.go:170`](../../test/integration/foreign_object_test.go#L170),
>   [`internal/controller/rolling_update.go:227`](../../internal/controller/rolling_update.go#L227) (`:273` on 2026-09-27),
>   [`internal/controller/foreign_object_test.go:318`](../../internal/controller/foreign_object_test.go#L318), `:913`, `:1062`
> * **7 lines** in `SECURITY_ARCHITECTURE.md` (`NA62`/`NA63`): `:237`, `:247`, `:597`, `:608`, `:615`, `:623`, `:631`
>   *(2026-09-27: that file is gone, split into [`docs/security/`](../security/README.md) by the
>   documentation restructure. Three of the seven lines moved verbatim, all into
>   [`isolation-and-tenancy.md`](../security/isolation-and-tenancy.md): `:163` (`NA62`) and
>   `:181` (`NA63`) under "What does not hold", `:216` (`NA62`) under gap
>   [H-9](../security/isolation-and-tenancy.md#h-9) *(renumbered 2026-09-27 after that page's
>   rootless bullets were shortened; before it, the H-9 line stood at `:239`, not `:229`)*. The other four stood in hardening-checklist
>   items already ticked as done, which the restructure did not carry over, so the residue is
>   49 lines, not 53 — see [the 2026-09-27 section](#2026-09-27-the-t-label-citations-join-this-ticket).)*
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
> `NA61`–`NA63` are documented in it at `:7041` and `:7090` — so the references resolve for
> Hans locally and dangle for every clone. That asymmetry is the whole defect.
>
> **Not verified:** whether the `NA61`/`NA62`/`NA63` tags inside ADR "Amended … (NAxx)"
> headers are intentional provenance markers rather than oversights. No ADR or CLAUDE.md rule
> speaks to it; treating them as citations to rewrite is a judgement call, not a fact read off
> the tree. Decide it before starting, or the 41 ADR lines get churned twice. *(2026-09-27:
> [ADR 0034](../adr/0034-tickets-are-work-lists-that-get-archived.md) now records the question
> as undecided, under Alternatives, "Rewrite all existing citations in the same change". It is
> Decision 1 under [Options](#options).)*
>
> **Effort for the residue: XS.** *(corrected 2026-09-27: XS covered the 49 `NA` lines only.
> Since 2026-09-27 the work list is every ticket citation outside `docs/tickets/`, about 250
> lines, so the effort is L.)*

## Context

Until 2026-08-21 the architecture rationale of this operator lived in a working note,
`local_valkey_operator_admission_gap.md`, whose findings were numbered `NA1 … NA58`. That note
is **untracked, gitignored (`local_*`) and about to be deleted**. It was never part of the
repository and cannot be linked from it.

Its content has been moved into 18 Architecture Decision Records under
[`docs/adr/`](../adr/README.md). The prose in `README.md`, `CLAUDE.md`,
`SECURITY_ARCHITECTURE.md` and the ADRs themselves no longer cites `NA…` numbers.

**The code does.** 170 lines across 29 Go files, plus 2 in a workflow, still point at a
document that will not exist. A reader who greps `NA26` after the deletion finds a comment
explaining that some decision is "NA26" and no way to learn what that means.

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
compile run below.)*

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
* Touching `local_valkey_operator_admission_gap.md`. It is untracked and is being deleted.

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

## Current state (2026-09-27, `HEAD` = `4a7543e`)

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
- **The 8 path citations are unchanged**, all in ADRs: `0012:286`, `0017:612`, `0025:444`, `:496`,
  `0028:30`, `0032:6`, `0033:6`, `:750`. The two commands of *Done when* return ~~239~~ *(231 after
  work item 1, 2026-09-27; the T-label count is still 190)* and 54 lines.
- **No label reaches a generated artifact.** The three in `api/v1/valkey_types.go` (`:173`,
  `:249`, `:255`) are doc comments on `ConditionType` constants, and `config/crd/bases/` carries
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

- That archive/039 gives an unambiguous ADR and decision for every label T1–T29; it was not read
  through.
- Which label lines are tags and which carry meaning (the split Decision 1 turns on). A rough
  regex puts 56 of the 151 ADR lines in tag shape; they were not classified by hand, and neither
  were the Go lines.
- No `make` target was run.

## Options

Two decisions. **Decision 1 comes first**: it decides the shape of the ADR half, which is the
bulk of the work. Decision 2 is independent and XS. The scope decision of 2026-09-27 (option B,
[above](#2026-09-27-the-t-label-citations-join-this-ticket)) stands, and neither decision
reopens it.

### Decision 1 — is a ticket label used as a provenance tag rewritten, or kept as a listed exception?

A **tag** is a label that records where a statement came from, and that can be removed without
changing what the sentence says. Examples: `Amended 2026-08-22 (NA61):`, `Added 2026-09-26
(T32).`, `*(corrected 2026-09-26, T34)*`, `(measured, T31)`, `(T24)`. Text already struck as
superseded counts too. Tags occur in ADRs, in Go comments, in `CLAUDE.md` (`:284`, `:785`,
`:794`, `:1014`, `:1047`) and in `rootless-migration.md` (`:27`, `:47`, `:85`). ADR 0034 names
only the ADR `Amended` headers as undecided, but the other tags have the same shape. A label
used as a noun ("the NA61 half of ADR 0020", "`Ready`/T18") is a citation the sentence depends
on. It is rewritten under every option, and that is why the first two work items need no
decision.

- **A. Rewrite or drop tags like every other citation.** **(recommended)** A bare tag is
  dropped, because the date already identifies the amendment: `Amended 2026-08-22 (NA61):`
  becomes `Amended 2026-08-22:`. Struck text is treated the same way, and the strike stays. Cost
  L, about 250 lines. *Done when* stays one grep that returns only the rules-page lines. That is
  the only check that runs mechanically, and it matters because ADR 0034 records "Enforcement
  of D7 is manual" (Residual risks). What is lost: the pointer from an amendment to its analysis
  in `archive/`. The date and `git log` still find it.
- **B. Keep tags as listed exceptions.** *Done when* lists each kept line with its reason. The
  rewrite drops to about M, but a list of tens of lines has to stay current. Every kept tag also
  points into `archive/`, which is the "points at history" failure ADR 0034 gives for rejecting
  "Do not adopt the reference ban".
- **C. Replace each tag with the commit that shipped the amendment**, for example `Amended
  2026-08-22 (<hash>):`. This keeps provenance and cites no ticket. It costs a `git log` lookup
  per tag on top of A, and a hash in prose tells a reader no more than a label does.

### Decision 2 — what a `declaredGap` in the condition registry has to name

- **A. The ADR decision that owns the exception.** **(recommended)** The test asserts
  `ADR \d{4}` instead of `T\d+`. The messages at `condition_registry_test.go:128`, `:158` and
  `:206` and the field doc at `condition_registry.go:84-86` say so. The `Ready` row drops its
  `T18: ` prefix; it already names "ADR 0001 D4 … re-decision open". ADR 0027 D4 is amended in
  place. Cost XS, unit tier only. D4's own purpose is that "an exception has to be traceable to
  a decision", and decisions live in ADRs. An ADR number never changes, while the ticket that
  `T18` names is archived when it closes and the reference then points at history. Coordinate
  the `Ready` row with T18.
- **B. Keep `T\d+` and list it as a D7 exception.** No code change, but ADR 0034 D7 gains a
  carve-out, and every future gap adds a citation that rots once its ticket is archived.
- **C. Drop the traceability test.** It removes the conflict together with the guard ADR 0027 D4
  exists for: a `declaredGap` would become a way to silence the registry.

## Decision

The scope decision of 2026-09-27 stands (option B,
[above](#2026-09-27-the-t-label-citations-join-this-ticket)). Decisions 1 and 2 under
[Options](#options) are not yet decided.

## Work list

1. **[XS, no decision] The 8 `NA` noun uses outside ADRs and `CLAUDE.md`.** D7 is decided and
   none of these is a tag: *(**Done 2026-09-27**, with two deviations from the mapping below,
   see History: `:1062` and `rolling_update.go:273` cite ADR 0020 D8, not D1, and `:318` and the
   integration test cite D1 and D8.)*
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
   run may change that file on its own authority]** The two
   `CLAUDE.md` noun uses:
   - `:568`: `` `Ready`/T18 `` becomes `` `Ready` (ADR 0001 D4) ``.
   - `:620`: `a pre-existing gap T32 does not close` becomes `a pre-existing gap, an ADR 0026
     residual risk`; the residual risk is at `0026:787`.
3. *(waits on Decision 2)* The registry test, its messages, the `Ready` row and ADR 0027 D4.
4. *(waits on Decision 1)* The 151 ADR lines and the 8 path links, the Go T-label lines,
   `CLAUDE.md` `:284`, `:785`, `:794`, `:1014` and `:1047`, and `rootless-migration.md` `:27`,
   `:47` and `:85`. Noun uses among them are rewritten either way. Do it as one comment sweep
   together with T46, and rewrite ADR 0020 before T42 touches it. *(2026-09-27: T46 landed and
   is archived, [archive/046](archive/046-code-comments-name-a-missing-test-and-a-false-import-reason.md),
   so the sweep no longer has a sibling to coordinate with; it cited no ticket in its new text.)*
5. Close ([ADR 0034](../adr/0034-tickets-are-work-lists-that-get-archived.md)):
   - Set D7 to implemented in ADR 0034 Status, and its State in `docs/adr/README.md`.
   - Update the "Partly implemented" paragraphs in `CLAUDE.md` and `docs/tickets/README.md`.
   - Run the two *Done when* greps and `git grep -nwE 'T40|040|C3'` outside `docs/tickets/`.
   - Move this file to `archive/`.

## History

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
