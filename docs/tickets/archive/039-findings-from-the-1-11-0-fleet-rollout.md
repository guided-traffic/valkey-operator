# Ticket: Findings from the 1.11.0 fleet rollout on wds18-k8s-main (2026-08-22)

> **Archived 2026-09-27** as ticket 039, renamed from `local_neue_baustellen.md` when the tickets
> were numbered. The open items T12, T18, T23, T26 and T29 were moved into their own ticket files
> the same day, and their headings below are stubs. Passages carrying the details of two open
> security findings (T26, T30) were replaced by an embargo note or removed. Without the `local_`
> prefix the owner's global `local_*` ignore rule no longer matches this file, so the statements below that
> call it gitignored describe it before that day.

> **ARCHIVE (frozen 2026-08-27).** Triage moved to [`local_BOARD.md`](#board-archive--final-state-of-local_boardmd-retired-2026-09-27); filing
> rules and the ticket template are [`README.md`](../README.md). This file stays
> as the analysis record for T1–T29 and receives **no new items and no index refreshes**.
> When an open item is next worked on, extract it into its own `local_TNN-*.md` (lazy
> migration) and update its board row. The index tables below are superseded by the board.

> **Keep this file current.** Update it as part of every fix or assignment that
> touches this ticket — in the same change, not afterwards. Move an item's status
> (open → DONE / still open), record what was actually verified and how
> (commands, measured numbers, file:line), state what was deliberately left out
> and why, and correct any claim the work disproved — a superseded statement gets
> rewritten, not left standing next to its correction. New findings become their
> own numbered item. Separate verified from unverified explicitly.

> **The index below is the part that goes stale first, and it is the part a later
> reader trusts.** Refresh it **after every task and before every commit** — not at
> the end of a session, not "next time". Two rules make that non-optional:
>
> 1. This file is **gitignored** (`local_*` in `~/.gitignore`), so a fix commit
>    physically cannot carry it. It never shows up in a diff, a `--stat` or a review.
>    Nothing but this instruction will catch the skew. On 2026-08-26 a sweep found
>    **nine** items whose text the code contradicted, including three `Status: open`
>    lines sitting under a header that already said `IMPLEMENTED`.
> 2. A per-item status line contradicting the index is a **defect in this file**.
>    Fix both, in the same edit. When they disagree, the section wins and the index
>    is the thing that was forgotten.
>
> Before a commit, walk the index top to bottom and ask per row: did this change
> touch it, and does the row still read true? An item that moved to DONE records
> **what was verified and how** in its own section — the index only carries the verdict.

---

## Open items index

**Refreshed 2026-08-26 ~09:45**, after (a) a 10-agent verification sweep against
`HEAD` = `1c309d8` (branch `fix/bad-findings`), (b) a read-only inspection of the live wds18
fleet, and (c) the **T8 remediation actually being executed**. Every row was re-verified in
the code or measured on the cluster, not carried over.

**Second refresh 2026-08-26 ~22:15:** T16 and T15 were analysed in depth, decided and
implemented in one sitting. Both are DONE and moved to the done table; T23 was filed out of
T15 D4; two of the "Corrections owed" rows below were discharged with T16. Everything is
unit- and integration-verified in this repository and **nothing was run against a cluster**.

**Third refresh 2026-08-27 ~09:50:** T25 was analysed in depth and **decided** (no code written).
Read-only against `4a8b92e`, the HEAD at the time, plus one
throwaway working tree that built and ran the suite and was reverted. **Nothing was run against
a cluster.** The analysis falsified two claims standing in this repository: that a per-container
ServiceAccount-token split is not expressible in Kubernetes (it is, and it was verified against a
real kube-apiserver), and that `tls-material-hash` is the *third* forgeable data-pod field (it is
roughly the seventh). Both are recorded in T25 rather than fixed — the fix is owed by the change
that lands option C, and `SECURITY_ARCHITECTURE.md` and the ADRs were **deliberately not edited**
in this pass.

**Fourth refresh 2026-08-27 ~11:30:** **T25 is DONE** — both options built, C then B, with A
folded into both, and every obligation the decision listed discharged. Two deviations from the
recorded plan were put to the user and decided by them: B gained a **self-extinguishing
annotation fallback** on the read path, because without it the Sentinel tier would have gone
silently unmeasured (Sentinel pods never roll on a plain upgrade, ADR 0005 D11); and the
**Sentinel pod's `default`-ServiceAccount token** — a new finding, not in the ticket — was fixed
in the same pass rather than filed, so no item was opened for it — the T27 that exists below is
a different and later finding, and the offer to file the token one as "T27" is why the numbering
reads oddly in the Execution section. Unlike the three
refreshes above, this one **was run against a cluster**: the new cert-rotation e2e ran on the
local Kind cluster. `SECURITY_ARCHITECTURE.md`, `CLAUDE.md`, `README.md`, the chart alert comment
and five ADRs were edited; ADR 0031 is new.

**T25(a) was decided in the same session and is documentation only** — accepted rather than
closed, after the re-examination found that one of the three arguments against a strong digest
does not hold and had already been copied into two ADRs by this work. The correction landed in
ADR 0030 D11, ADR 0031, `SECURITY_ARCHITECTURE.md`, `CLAUDE.md` and ADR 0016. **T28 and T29**
were filed out of the two ideas T25 had recorded as "better filed separately" and never filed.

**And the new e2e immediately found two defects that every green gate had missed.** One is
mine and was fixed in the same pass: the token projection shipped with `DefaultMode: 0o420`,
which is `r---w----`, so every data pod `CrashLoopBackOff`ed on the first real kubelet — the
`420` in every manifest example is *decimal* for `0644`. The other is **pre-existing and is now
[T27](#t27-the-pods-of-a-freshly-created-tls-cluster-carry-no-fingerprint-and-are-never-rolled-by-a-rotation)**:
the pods of a freshly created TLS cluster carry no fingerprint at all and are therefore never
rolled by a rotation. It is filed rather than fixed, because the fix is a decision about the
presence rule and not a bug fix.

The execution changed five rows and falsified three claims written earlier the same day —
`known-master` cannot be corrected by hand under Flux, a data-tier roll does not clean the
Sentinel peer tables, and "zero pod restarts" is measurably false. Each is marked as a
correction in place in its own item rather than quietly rewritten.

**Fifth refresh 2026-08-27 ~14:10:** **T27 was analysed in depth. No decision recorded yet and no
code written** — the section now carries six costed options with an adversarial review of each, a
recommendation, and an explicit verified/not-verified split. Read-only against `4ebe070`; **nothing
was run against a cluster and no test was executed**, so every complexity and compile claim is
against HEAD, not against a modified tree. The analysis widened the item twice: the record-less
template also reaches pods through `spec.tls.enabled: false -> true` on a **serving** cluster and
through any pass that cannot read the Secret, which makes **T24(c) the same defect through a
different door** — the two are now cross-referenced and are to be decided together. It also
falsified two statements standing in the item itself: option 3 as written is a **no-op** rather than
a wrong answer, and option 4's "armed within one release" is false for Sentinel pods and for
kustomize / floating-tag installs, both of which ADR 0005 D11 names itself. Rows T27, T24 and T28
were touched; **no ADR and no source file was edited.**

**Eleventh refresh 2026-08-28: the merge to main fired the accepted residue, and the fix is
in the test.** Both e2e legs failed `NoSecondDelete` on the no-sentinel topology: test and
operator deleted in the same second, each after a read that truthfully showed a quiet tier —
exactly the two-writer physics the ADR 0026 second amendment had just recorded as
unclosable. The operator held its invariant (its uncached pre-delete look could not have
seen the injection); the sampler attribution could not express simultaneity and blamed it.
The sampler now carries a time dimension: `firstSeen` per pod, and an overlap containing
the test victim whose other pod began terminating within `simultaneousDeleteWindow` (3 s)
of the injection is the test's — at most one such pod, and a delete past the window stays a
violation, since a real gate breach is a requeue interval away. Both CI-measured shapes are
deterministic test cases now. No operator code changed in this pass.

**Tenth refresh 2026-08-27 (late night): the T13 guard held, and the CI rerun exposed the
two layers underneath it.** On `ca2377a` the same leg failed again with the resolver logging
`Split-brain resolution refused: every reported master is terminating` — the guard working —
while the dataset still died: the **drain handler** of the dying recorded master had already
promoted, by ordinal, the peer the roll had deleted in the same second; the fleet then
followed the empty reborn pod with no resolver involved. Fix: `findSyncedReplica` skips
peers provably carrying a `DeletionTimestamp` (as candidate and as "master already
present"), unknown reads as alive; the sidecar Role gains `get` on the same named pods —
D8s one deliberate widening, costed in `SECURITY_ARCHITECTURE.md` 4.2. Second layer: the
same run measured **two data pods terminating at once** — the ADR 0026 delete gate is
check-then-act over cached pod states, and a chaos delete in the cache lag slipped through.
Fix: the gate re-reads the tier uncached (`APIReader`) immediately before every delete;
ignorance still holds nothing. ADR 0028 D5a extended, ADR 0026 second amendment, ADR 0012
amended; drain unit tests cover skip/defer/ignorance, gate test simulates the diverging
cache. The e2e attribution gap (test-caused overlap not excused because the operator delete
was in flight at injection) is real but now moot on the operator side - the live look
closes the operator half.

**Ninth refresh 2026-08-27 (night): T13 is DONE, and it was the CI failure.** The
`single-node-valkey9` leg on `2689d31` was not a flake: the chaos delete of the ADR 0028
e2e landed in the same second the roll deleted the outgoing master, the dying recorded
master's drain stamped the already-terminating peer, the resolver adopted the stamp, and
every pod ended at `dbsize=0` — the exact terminating-authority gap T13 predicted, one door
over, promoted from "dormant, unmeasured" to measured by a slow runner. Fixed at all four
adoption/confirmation doors (roll resolver candidate set, `stampedMasters`,
`confirmedMasterAuthority`, `adoptUnrecordedPromotion`), refusing only on positive
evidence; ADR 0028 D5a new, ADR 0026 and 0011 amended. Five unit tests reconstruct the CI
shape; the guarded suite plus the two previously failing e2e are to be re-verified on Kind
before push. The parallel `TopologyRestoreAbandoned` timeout in the same job showed no
wrong end state and is watched, not chased.

**Eighth refresh 2026-08-27 (late): T7, T17, T28 and T10(A) are DONE — the LATER shelf is
worked off except T13 and T23.** Per item: **T17** — all three completion exits of
`verifyTopologyRestored` now state the end state they actually reached (abandoned
restoration names the promoted replica, the stalled rogue-master exit counts its rogues,
and the verify-incomplete exit emits the `RollingUpdateComplete` marker it used to omit);
the verdict is read before `clearRollingUpdateState` refreshes the CR, and the message
finally has tests. **T7** — new level `RWServiceEmpty` (ADR 0012 D12): a settled cluster
with no master-labeled pod is reported on the CR, riding the existing status write at zero
extra Updates; presence-guarded in the T24(d) style, so no fleet gains the row from an
upgrade. **T28** — `observeServedCertificate` arms the health pass dial with a report-only
`VerifyConnection` hook comparing the served leaf against the Secret (mismatch at Info,
match at V(1)); the D6 measuring instrument exists, D6 itself deliberately unmoved.
**T10(A)** — `recreationWait` (ADR 0010 D16): the last three unbounded rolling-update waits
are bounded observations now, `PodRecreationStalled` + DeferredRequeueAfter past 2 min, per
episode, no Event. **T10(B) was already discharged by T16** (ADR 0023 references 0028 three
times); the T10 section text claiming it open was stale and is corrected in place. Verified:
`make test-unit`, `make lint`, `make cyclo` green; new unit tests for every piece.
**Not verified:** nothing ran against a cluster in this pass, and the T28 hook has not yet
seen a real rotation window — the D6 log capture is still owed.

**Seventh refresh 2026-08-27 (late): T24(b) is DONE — T24 closes entirely.** The reporting
half of option C, on explicit instruction: when no measured pod is stale but record-less
legacy pods exist, `TLSMaterialStale` stays `False` with reason `TLSMaterialUnmeasured` and
names them. Status- and alert-neutral (the shipped rule matches `True` only); the reason flip
on legacy-carrying clusters is the one deliberate ADR 0005 D10 exception, recorded in ADR
0030 D9. Precedence fixed: stale > unmeasured > current. What option C also offered — a
companion 24 h alert — was **not** built: the chart alerting is default off and the reason is
already a label on `vko_valkey_status_condition`, so an operator can alert on it without us
shipping a second rule. Nothing rolls the legacy pods, deliberately — the exemption is D8's
whole point; only the silence about it is gone. Verified: unit (three new tests: named
population, sentinel-legacy shape, stale-outranks-unmeasured), integration, lint, cyclo, and
the rotation e2e re-ran green on Kind against the rebuilt image (75 s, `TLSMaterialCurrent`
still the post-roll verdict).

**Sixth refresh 2026-08-27 (evening): T27 is DONE and T24 is DONE except (b).** The decision
deviates from the recorded recommendation, deliberately: not option A's `unarmed` sentinel but
a **write gate** — ADR 0030 D12, "the operator never persists a TLS pod template without a
material record" — which closes all three doors with no third fingerprint state, no read-side
change, no ADR 0007 D3 amendment, and none of the arming rolls that were A's own strongest
argument against itself (a fresh cluster no longer rolls itself right after creation, and a
`tls.enabled` flip is one roll, not two). Case 2 of A survives inside the gate: an unreadable
Secret inherits the persisted record instead of stripping it, which is the T24(c) fix. T24(a)
(AND-ed measurability, one way) and T24(d) (the `when: IsTLSEnabled` step gate replaced by an
evaluator-owned, presence-guarded retraction) shipped in the same change; **T24(b) — the
legacy Sentinel pod population — stays open** and is option C territory. The C1 README
correction (zero pod restarts, measured false) landed too. Verified: `make test-unit`,
`make test-integration` (including a new gate test against a real API server), `make lint`,
`make cyclo` all green; the rotation e2e now asserts pods are armed from birth **without** the
test arming them, and **ran green on the local Kind cluster in 85 s** against the rebuilt
image. New accepted residual,
recorded in ADR 0030: a TLS Secret that never appears parks the tier silently (phase
`Provisioning` on create, a non-converging flip on update; only the operator log names TLS).

### Repo work — open

| Item | Sev | Effort | State | One line |
|---|---|---|---|---|
| [T29](#t29-a-chart-shipped-validatingadmissionpolicy-default-off) | low | M | open, **filed 2026-08-27 out of T25**, needs an ADR 0015 re-decision | The only in-Kubernetes control reaching `ownerReferences`, `finalizers` and `spec.containers[*].image`. ADR 0015 D2 refuses admission *webhooks* for a reason that does not transfer to a VAP — but D2 must be amended, not stretched. |
| [T26](#t26-embargoed-security-finding-open---details-in-its-own-ticket-file-until-it-is-fixed) | medium | M | open, **filed 2026-08-27**, needs a decision | Embargoed security finding, open - details in its own ticket file until it is fixed. |
| [T23](#t23-pauserollingupdate-records-no-pause--it-clears-the-state-and-re-arms-a-fresh-budget) | low | L | open, **filed 2026-08-26 by T15 D4** | The pause halts nothing: it clears the state, and the next pass re-arms a fresh syncTimeout and pauses again. Four tracked sentences promise otherwise. Needs an ADR 0010 re-decision. |
| [T18](#t18-ready-keeps-its-pre-roll-value-for-the-whole-rolling-update--decided-in-adr-0001-d4-re-decision-request) | low | S or L | open, **needs a human** | Not a defect. A re-decision on which of two readings of `Ready` is intended; effort depends entirely on the answer. |
| [T12](#t12-no-write-fencing--a-master-with-zero-replicas-keeps-accepting-writes-min-replicas-to-write-as-an-opt-in-field) | medium | L | open, **needs a product call** | Write fencing. The Decision section is empty — no option was ever chosen. |


**Committed 2026-08-26** on `fix/bad-findings`, three commits as decided:
`7415e5e` `fix(storage)`, `2d6b238` `fix(rolling-update)`, `4a8b92e` `docs(conditions)`.
Each intermediate tree was built and verified separately (`make test-unit`, `make lint`,
`make cyclo`), and the final tree was diffed byte-for-byte against the verified working
state before the last commit.

**One defect was caught by the split itself and is worth recording.** The
`RollingUpdatePaused` registry row was still the pre-fix row — old `clearSite`,
`presenceGuarded: false`, `declaredGap` intact — while the code was already fixed, and the
whole unit tier stayed green, because `declaredGap` suppresses exactly the guard that would
have caught it. That is the residual risk
[ADR 0027](../../adr/0027-conditions-are-levels-edges-or-history.md) names in its own words
("a `clearSite` string that describes a function which no longer clears anything"), firing
on the first change after the ADR was written. Fixed before committing, and the corrected row
was knocked out (`presenceGuarded: false`) to prove the guard now bites.

### Repo work — done

| Item | Done | What shipped |
|---|---|---|
| [T1](#t1-stale-sentinel-peer-entries-erode-the-failover-margin) | 2026-08-23 | Sentinel identity pinned to the pod hostname; `SentinelPeersStale` reports drift. ADR 0022. |
| [T2](#t2-persistence-toggled-on-an-existing-cluster-is-silently-unsupported--no-guard-for-immutable-volumeclaimtemplates) | 2026-08-23 | The StatefulSet write is refused rather than retried forever. ADR 0023. |
| [T3](#t3-rollingupdatecomplete-fires-before-the-sentinel-tier-is-rolled--the-sentinel-tier-has-no-completion-marker) | 2026-08-23 | The Sentinel tier reports its own completion. ADR 0024. |
| [T4](#t4-transient-splitbraindetected-warnings-during-every-controlled-failover) | 2026-08-24 | `MultipleMasters` is the level, `SplitBrainDetected` the 90 s edge. ADR 0025. |
| [T5](#t5-a-pod-being-deleted-still-counts-as-ready--the-duplicate-deletes-are-the-visible-tip) | 2026-08-25 | `available()` vs `reachable()`; no tier deletes while a pod of it terminates. ADR 0026. |
| [T6a/T6b/T6c/T6d](#t6-stale-status-surfaces) | 2026-08-26 | `ObserverReady` moved past the capture; `TopologyRestored` documented as history; the `Ready`/`phase` contract written down. |
| [T11](#t11-a-recorded-master-that-returns-empty-wins-split-brain-resolution-and-wipes-the-promoted-replica) | 2026-08-26 | A demotion may not discard the only dataset. ADR 0028. |
| [T14](#t14-statusobserverready-reads-a-deployment-the-operator-has-not-proven-it-owns) | 2026-08-26 | `isObserverDeploymentReady` proves ownership first. |
| [T19](#t19-the-health-checker-guessed-a-pods-tier-from-its-name--a-cr-named--sentinel-was-unreachable-to-its-own-operator) | 2026-08-26 | A name is not a component — the tier is passed, never parsed. Commit `1e92db0`. |
| [T20](#t20-the-coverage-pr-comment-outgrew-a-kernel-limit--argument-list-too-long-in-combined-coverage-report) | 2026-08-26 | The coverage comment outgrew a kernel limit, not a GitHub one. Commit `1c309d8`. |
| [T16](#t16-storagespecnotapplied-has-two-evaluators-and-the-second-one-clears-what-the-first-reported) | 2026-08-26 | Commit `7415e5e`. Either StatefulSet tier may report a claim conflict, only the data tier may clear one. `mayClear` on the guard, `ownershipRule` in the registry, a step-order pin. ADR 0023 D4a, ADR 0027 D1/D2. |
| [T15](#t15-rollingupdatepausedtrue-can-never-be-cleared-on-a-non-sentinel-cluster) | 2026-08-26 | Commit `2d6b238`. `RollingUpdatePaused` is cleared from the two sites every dispatch target reaches, the converged one gated on tier convergence, and the unguarded `False` write that had stamped the condition onto the whole Sentinel fleet is deleted. ADR 0002 D10b. |
| [T22](#t22-the-no-second-delete-e2e-blamed-the-operator-for-an-overlap-the-test-itself-created) | 2026-08-26 | The chaos delete is injected on the very event that unblocks the roll, so the operator could delete first and be blamed for the overlap. The sampler now attributes; the classification has its own deterministic test. |
| [T25](#t25-the-tls-material-fingerprint-is-a-change-detector-being-read-as-a-control) | 2026-08-27 | **(b) built, (a) accepted.** Both decided options, C then B, plus A. **C:** the data pod sets `automountServiceAccountToken: false` and projects the token into the `sidecar` container alone; Sentinel pods project none at all (a new finding, fixed rather than filed). **B:** the fingerprint left pod metadata for `VKO_TLS_MATERIAL_HASH` in the carrier container's spec, with a self-extinguishing annotation fallback on the read path. **A:** `SECURITY_ARCHITECTURE.md` section 3 now enumerates the eight forgeable fields instead of counting them wrong. ADR 0031 is new; ADR 0012 D8 gained step 4 and retracted its last residual; ADR 0030 D4 amended and its T25 residual closed; ADR 0020 gained D10; ADR 0007 D2 corrected its input count. Certificate rotation gained its first e2e. **(a), the Secret writer, is documentation only:** accepted permanently in ADR 0030 D11, after a re-examination falsified one of the three grounds against option D — and the entropy framing of the password brake was corrected in five documents, where "a 32-bit digest of ..." read as though a wider hash would make the password case safe. |
| [T13](#t13-the-steady-state-master-authority-has-no-deletiontimestamp-guard-anywhere) | 2026-08-27 | **A terminating pod is never the adopted or confirmed master authority** (ADR 0028 D5a; ADR 0026/0011 amended). Measured by CI as full-fleet `dbsize=0` before the fix; all four doors guarded, demotion of a dying rogue still allowed. |
| [T7](#t7-master-label-not-restored-after-an-operator-external-failover---rw-service-can-go-empty) + [T17](#t17-rollingupdatecomplete-says-topology-restored-on-the-path-that-just-recorded-the-opposite) + [T28](#t28-measure-what-the-pods-actually-serve-off-the-handshake-the-operator-already-performs) + [T10](#t10-the-orphan-delete-recovery-wedges-the-statefulset-controller-when-claims-are-added)(A) | 2026-08-27 | The eighth-refresh batch. **T7:** `RWServiceEmpty` level, ADR 0012 D12 — an empty `-rw` selection on a settled cluster is finally visible on the CR, rides the existing status write, presence-guarded. **T17:** every completion exit of `verifyTopologyRestored` states its real end state and every exit emits `RollingUpdateComplete`; message-level tests exist for the first time. **T28:** `observeServedCertificate` — the served-leaf observation off the health-pass dial, report-only, the ADR 0030 D6 measuring instrument (D6 unmoved until a fleet log capture across a rotation window). **T10(A):** `recreationWait`, ADR 0010 D16 — the last three unbounded waits are bounded observations with `PodRecreationStalled`; the wedge itself stays upstream Kubernetes. T10(B) had already been discharged by T16; its section text was stale. |
| [T27](#t27-the-pods-of-a-freshly-created-tls-cluster-carry-no-fingerprint-and-are-never-rolled-by-a-rotation) + [T24](#t24-tlsmaterialstalefalse-can-mean-one-tier-is-fine-and-nobody-looked-at-the-other) (all four letters; (b) landed in a follow-up pass the same day: `TLSMaterialUnmeasured` names the legacy pods instead of absorbing them) | 2026-08-27 | **The write gate, not option A.** ADR 0030 D12: the operator never persists a TLS pod template without a material record — Secret fingerprint when readable, the persisted record when not (an unreadable Secret never erases a record, closing T24(c)), and a refusal without an error when neither is known, re-entered by the Secret watch. All three T27 doors closed; a fresh TLS cluster is armed from birth with **no** arming roll and no third fingerprint state, which is why the recorded recommendation A was overruled. Same change: T24(a) AND-ed one-way measurability, T24(d) evaluator-owned retraction on `tls.enabled: false`, `ReasonTLSMaterialNotApplicable`, registry row updated, `ensureTLSMaterialRecord` replacing `stampTLSMaterialHash`, new unit + integration coverage (`TestTLSMaterialGate_TheStatefulSetWaitsForTheSecret_Integration`), rotation e2e asserts armed-from-birth and `armPodsWithTheTemplateRecord` deleted, C1 README correction. Open residue: T24(b); new accepted residual: a never-appearing Secret parks the tier silently. |
| [T21](#t21-the-sidecar-pins-its-tls-client-certificate-for-the-pod-lifetime--a-cert-manager-rotation-silently-breaks-the-labeler-and-the-drain-promotion) | 2026-08-26 | Option E, capability-gated, both halves. `internal/tlsmaterial` re-reads CA **and** keypair per dial for the sidecar and the observer; `vko.gtrfc.com/tls-material-hash` on both StatefulSet pod templates rolls everything that cannot reload; the Secret watch now matches TLS Secrets; `TLSMaterialStale` + `ValkeyTLSMaterialStale` (72 h) cover the roll that never starts. **ADR debt paid 2026-08-26:** ADR 0030 written; ADR 0016 D12 and its cert-manager residual risk amended in place; ADR 0012 gained D11; `SECURITY_ARCHITECTURE.md` sections 2, 6 and 9 updated. One correction the debt note itself carried: ADR 0016 asked about **`valkey-server`**, which is still unmeasured — what fired and was measured is the client side. |

### Not repo work — needs a human and a maintenance window

**T8 was executed on 2026-08-26 with explicit authorisation.** The row below is kept because
the analysis it carries is still the reference for the same failure on another cluster.

| Item | Urgency | One line |
|---|---|---|
| [T8](#t8-cluster-ops-on-wds18-gitlab-valkey-remediation-not-repo-work) | **DONE 2026-08-26** | Executed: backup, `persistence.enabled: false` via Flux (`2d66b96`), full roll, `phase=OK`, **27153 keys intact**, block cleared. Whole fleet `OK`. |
| [T9](#t9-cluster-ops-on-wds18-one-time-sentinel-peer-reset-not-repo-work) | **DONE 2026-08-28** | Executed on all 8 sentinel clusters after the v1.12.0 rollout: preconditions verified, resets sequential, tables 2/2/2 everywhere, and `SentinelPeersStale=False/SentinelPeersConsistent` on every CR within one recheck — the verification this item waited two days for. One-time by mechanism (ADR 0022 pinned identities). |

### Cross-cutting, and it gates the two above

**Nothing in this file is released.** Verified 2026-08-26:

```
$ git rev-parse --short origin/main      # 81e1108
$ git describe --tags --abbrev=0 origin/main   # v1.11.1
$ git log --oneline origin/main..HEAD | wc -l  # 10
$ git merge-base --is-ancestor 1b1f6ed origin/main; echo $?   # 1 = NO
```

The wds18 fleet runs **v1.11.1**, which contains none of T1–T6, T11 or T14. It has no
pinned `sentinel myid`, no `SentinelPeersStale`, no `RecreateRequired` reason and no
ADR 0026/0028 behaviour. Consequences that are easy to get wrong:

* "Fixed in the repo" and "fixed on wds18" are different claims. Every DONE row above is
  the first one only.
* T9 step 4 ("the `SentinelPeersStale` condition must be absent or `False` on every CR")
  **cannot be performed today**. Verification falls back to reading `SENTINEL master`
  by hand.
* Peer drift re-accrues on every partial pod churn, which in `database-examples` is
  continuous — Chaos Mesh kills a vko pod every 5 minutes.

Merging and releasing PR #195 is worth more to the fleet than any single item above.

### Corrections owed in tracked files

These are **not** in this file, so the gitignore argument does not protect them — they are
standing false statements in the repository, found by the same 2026-08-26 sweep and listed
here so they are not lost. Each is XS.

| File | What is wrong |
|---|---|
| ~~[`docs/adr/0023`](../../adr/0023-volume-claim-templates-are-immutable.md) `:164-171`, `:285-286`~~ **DONE 2026-08-26 with T16** | Still states the recorded-master-wins demotion is "a defect in its own right and is not fixed here" and "needs its own ADR and its own fix". ADR 0028 **is** that ADR. `grep -c 0028` in the file returns **0**, and `git show --stat 2051a34` shows the ADR 0028 commit never touched it. This breaks the CLAUDE.md rule that a reader must never find the old rule stated as current — and the commit that broke it is in PR #195 right now. |
| ~~[`docs/adr/0023`](../../adr/0023-volume-claim-templates-are-immutable.md) `:108-112`~~ **DONE 2026-08-26 with T16** | ~~D4 asserts the Sentinel call "compares empty against empty and costs nothing". That is exactly the claim T16 disproves.~~ Struck in place, D4a added. |
| ~~[`README.md`](../../../README.md) `:884-886`~~ **DONE 2026-08-27 with T27** | ~~Promises reverting `spec.persistence` costs "zero pod restarts".~~ **Measured false on 2026-08-26**: the T8 execution reverted persistence on `gitlab-valkey` and all three data pods were replaced. The README now states the condition (unchanged operator rendering) and the measured normal case (a lossless roll). |
| [`docs/adr/0017`](../../adr/0017-test-and-ci-policy.md) Alternatives | Records the *fact* of the three-tier test split but never the *decision* to keep it. That is the reopening risk `041-the-integration-tier-writes-no-valkey-values.md` was written to close. |
| 53 lines in tracked files | `NA61`/`NA62`/`NA63` citations pointing at a gitignored document: 5 Go, 7 `SECURITY_ARCHITECTURE.md`, 41 in `docs/adr/`. Same defect `040-tracked-files-cite-work-items-instead-of-adrs.md` exists to remove, reintroduced after its `NA3`–`NA49` scope was discharged. |

### Sibling ticket files

| File | State |
|---|---|
| [`042-enforce-the-standing-constraints-with-a-static-analysis-test-net.md`](../042-enforce-the-standing-constraints-with-a-static-analysis-test-net.md) | **open, nothing built.** The most significant of the sibling files. |
| [`040-tracked-files-cite-work-items-instead-of-adrs.md`](../040-tracked-files-cite-work-items-instead-of-adrs.md) | original `NA3`–`NA49` scope **discharged**; `NA61`–`NA63` residue open (see above). |
| [`041-the-integration-tier-writes-no-valkey-values.md`](../041-the-integration-tier-writes-no-valkey-values.md) | documentation half **done**; the ADR 0017 Alternatives entry it asks for is not written. |
| [`038-fleet-upgrade-analysis-1-10-48-to-1-11-0.md`](038-fleet-upgrade-analysis-1-10-48-to-1-11-0.md) | historical record of the 1.10.48 → `feat/support-pdb` analysis. No open work. |
| [`037-recovery-after-transient-admission-webhook-rejection.md`](037-recovery-after-transient-admission-webhook-rejection.md) | still present at 7544 lines, still gitignored. `040-tracked-files-cite-work-items-instead-of-adrs.md` claims it is "about to be deleted"; that has not happened. |

---

## Context

Operator upgrade 1.9.6 → 1.11.0 went live on wds18-k8s-main at 2026-08-22
~21:32 UTC and rolled every managed Valkey cluster. A 14-agent read-only audit
(2026-08-22 ~21:45–22:00 UTC) verified all 12 Valkey CRs live: `INFO
replication` on every data pod, sentinel consensus on every sentinel pod,
labels vs. live roles, DBSIZE master/replica, the full operator log
(1334 lines), and a code cross-check in this repo.

**Overall result: the failover-aware rolling update worked as designed on all
11 reconcilable clusters.** All 11 started at 21:33, all reached an explicit
completion marker in the log; no timeout expired, no promotion was refused, no
write conflicts. End state everywhere: exactly one master,
`master_link_status:up` on every replica, byte-identical offsets, sentinel
consensus, labels == status.masterPod == known-master annotation, no leftover
rolling-update state annotation. Data survived (harbor DBSIZE 2347/2346,
gpt 28/28).

Bonus live validation: minutes after the rollout, the Chaos Mesh schedule
`valkey-chaos` (database-examples, pod-kill every 5 min on vko-managed pods,
running since ~April 2026) killed valkey9-0 — the freshly promoted-back master.
The drain sidecar promoted valkey9-1, stamped `drain-promoted-at`, and the
operator adopted it via the evidence path within 1 s (`MasterAdopted` event,
one pass phase Error, then OK). ADR 0011/0012 passed an unplanned production
chaos test.

The items below are what the audit surfaced beyond that. T1–T7 are repo
work; T8 is cluster ops on wds18 (recorded here so the causal chain is not
lost).

---

## T1: Stale sentinel peer entries erode the failover margin

**Severity: high, but not for the reason first recorded. Status: DONE in the repo
2026-08-23 (Option 1 + Option 5, see Decision). Remaining fleet work is T9.**

### What was verified on the cluster (2026-08-22)

On all 7 sentinel-enabled clusters `num-other-sentinels` reads 4/3/2 across the
three sentinels instead of 2/2/2, and the rolled pods old IPs remain as `s_down`
entries in `SENTINEL sentinels`.

### What the mechanism actually is (measured 2026-08-23, docker harness)

The sentinel config lives on an **emptyDir**
([`internal/builder/sentinel.go:286-292`](../../../internal/builder/sentinel.go#L286-L292)),
so every replacement pod starts from the ConfigMap template and Sentinel
generates a **new `sentinel myid`** on boot. The pod also gets a new IP, and
Sentinel announces itself by IP. A peer therefore matches neither by runid nor
by address, and its hello handling adds a second entry instead of switching the
address of the existing one. Sentinel never garbage-collects the old entry.

Measured against `valkey/valkey:9.1.1` and `valkey/valkey:8.1.9` (1 master,
3 sentinels, quorum 2, each sentinel a container with its own config dir; a
"pod replacement" is `docker rm -f` + a fresh config dir + a new static IP):

| Scenario | Result |
|---|---|
| initial | 2/2/2 |
| one full ascending roll of all 3 sentinels | **4/3/2** |
| a second full roll | **4/3/2** — no growth |
| replacing only sentinel-2, three times | s0 2 → 3 → 4 → **5**, s2 stays 2 |

**Correction to the original entry.** "One more roll generation without a reset
pushes the known-peer count to 7" is wrong, and the superseded sentence is
replaced by this one: a full-tier roll is **self-limiting at 4/3/2** for three
sentinels, because each pod destroys its own table when it is replaced, and the
last pod rolled always ends up clean. What grows without bound is **partial
churn** — pod kills, evictions, node drains, OOM kills that hit a subset while
the others stay up. That is not orchestrated by the operator and reaches no
rolling-update hook. wds18 runs Chaos Mesh pod-kill every 5 min on vko pods in
`database-examples`, which is exactly this pattern.

### The consequence, measured rather than argued

A sentinel can lead a failover only when `live >= floor((known_others+1)/2)+1`.
Same topology, same timings, three runs:

| Run | Peer tables | Master killed | Outcome |
|---|---|---|---|
| control | 2 live sentinels, 2 known each | yes | replica promoted **within 10 s** |
| stale | 2 live sentinels, 5 known each (after 3 replacements of the third) | yes | **no failover after 45 s** — 6 voters need 4 votes, 2 exist |
| pinned myid | same churn, `sentinel myid` pinned per pod | yes | 2 known each, replica promoted **within 20 s** |

So the failure is real and reproducible, but it needs two things at once:
inflated tables **and** the loss of the freshest sentinel. After a plain
operator roll the last-rolled sentinel is always clean and can still lead, which
is why the fleet failed over fine on 2026-08-22.

### Two more measured facts that constrain any fix

- **`SENTINEL RESET <name>` keeps the current master address.** After a real
  failover moved the master away from the config-file value, RESET left
  `sentinel master` pointing at the new master and the rewritten config kept it.
  The comment at
  [`internal/controller/rolling_update.go:2346-2348`](../../../internal/controller/rolling_update.go#L2346-L2348)
  claims the opposite ("reverts to the initial config from the config file and
  loses the current master address after failovers") and is **wrong** — fix it in
  whatever change touches this area next.
- **`SENTINEL RESET` while the master is down is destructive.** Peer and replica
  discovery both run through the master pub/sub channel and its INFO output. A
  reset issued with the master unreachable left the sentinel at
  `num-other-sentinels=0` and `num-slaves=0` with no way back — total amnesia,
  and with quorum 2 it can then never call a failover. Any reset path must gate
  on a reachable master reporting `flags=master`.
  With a healthy master the recovery is fast: peers back within ~2 s, replicas
  within ~10 s.

### Closing the "not verified" item

The original entry left open whether stale entries distort gates the operator
itself uses. **They do not.** Verified by grep: `ckquorum` appears nowhere
outside [`test/e2e/tls_test.go`](../../../test/e2e/tls_test.go); the sentinel-roll
guard counts *Kubernetes pod readiness* against `replicas/2+1`
([`internal/controller/rolling_update.go:3542`](../../../internal/controller/rolling_update.go#L3542),
[`:3579`](../../../internal/controller/rolling_update.go#L3579)),
`isSentinelAwareOfReplicas` reads `num-slaves`, and `checkSentinel` reads
`flags`. Nothing in the operator reads `num-other-sentinels` today. The damage
is confined to Sentinel internal leader election.

### Relevant code

| Site | Why it matters |
|---|---|
| [`internal/builder/sentinel.go:286-292`](../../../internal/builder/sentinel.go#L286-L292) | the emptyDir that makes every pod a new sentinel identity |
| [`internal/builder/sentinel.go:589-660`](../../../internal/builder/sentinel.go#L589-L660) | `buildSentinelInitCommand` — where a pinned identity would be injected |
| [`internal/builder/statefulset.go:312-316`](../../../internal/builder/statefulset.go#L312-L316) | precedent: the data init container already appends `replica-announce-ip $MY_HOST` |
| [`internal/builder/sentinel.go:571-577`](../../../internal/builder/sentinel.go#L571-L577) | `ComputeSentinelPodSpecHash` covers the init command — touching it costs exactly one sentinel-tier roll |
| [`internal/controller/rolling_update.go:3524-3588`](../../../internal/controller/rolling_update.go#L3524-L3588) | `checkAndHandleSentinelRollingUpdate`; since the T3 fix (2026-08-23) it records `SentinelUpdatePending` and emits `SentinelUpdateComplete` at convergence (ADR 0024) — the "no completion marker" this line originally noted is resolved |
| [`internal/controller/rolling_update.go:2355-2436`](../../../internal/controller/rolling_update.go#L2355-L2436) | `resetSentinelState` — an existing REMOVE+MONITOR path per sentinel, with TLS/auth/port handling already solved |
| [`internal/valkeyclient/client.go:241-249`](../../../internal/valkeyclient/client.go#L241-L249) | `SentinelReset` already exists on the client |
| [`internal/valkeyclient/client.go:623-643`](../../../internal/valkeyclient/client.go#L623-L643) | `parseSentinelMasterInfo` — `num-other-sentinels` is in the reply and simply not parsed |
| [`internal/health/checker.go:262-311`](../../../internal/health/checker.go#L262-L311) | `checkSentinel` already queries `SENTINEL MASTER` on every sentinel every pass — detection costs zero extra connections |
| [`internal/metrics/collector.go:184-192`](../../../internal/metrics/collector.go#L184-L192) | any new condition type becomes a `vko_valkey_status_condition` series for free (ADR 0021) |
| [`internal/builder/service.go:226-240`](../../../internal/builder/service.go#L226-L240) | sentinel headless Service has `PublishNotReadyAddresses: true` — a DNS-based announce resolves before readiness |
| [`internal/builder/image_requirements.go`](../../../internal/builder/image_requirements.go) | a new tool in a generated script needs a line here, enforced in both directions by unit tests |

### Options

**Option 1 — pin `sentinel myid` per pod (prevention, favoured).**
The init container appends `sentinel myid <40 hex derived from namespace + pod
hostname>` to the writable config. A replacement pod then keeps the identity of
its ordinal, Sentinel takes the documented address-switch path
(`+sentinel-address-switch`) and replaces the entry instead of adding one.
Verified: churn that produced 5 stale peers produces 0 with the pin, on 9.1.1
and 8.1.9, and the failover that was blocked succeeds.
Derivation needs `sha1sum` (present in both pinned images, verified) plus a line
in `RequiredImageTools`; the ordinal/hostname pattern
(`echo $HOSTNAME | rev | cut -d- -f1 | rev`) already exists in the data init
container. Costs one sentinel-tier roll at upgrade (pod-spec hash change).
Side effect worth having: a dead pod stops counting as a separate voter, so the
electorate matches the pod count exactly.

**Option 2 — announce a stable address (`sentinel announce-ip <pod FQDN>`).**
Also verified to hold the table at 2/2/2 across replacements on both image
lines, and it makes `SENTINEL sentinels` readable (hostnames instead of pod
IPs), matching the operator existing `resolve-hostnames`/`announce-hostnames`
posture. Cost: it puts DNS into the sentinel↔sentinel path, which today is pure
IP and therefore survives a DNS outage or a NetworkPolicy that forgets port 53.
The headless Service already publishes not-ready addresses, so startup ordering
is not the risk — DNS availability is. Composable with Option 1.

**Option 3 — `SENTINEL RESET` after the sentinel tier finishes rolling (cure).**
What the original entry proposed. Needs T3 first: at the time of this analysis
there was no completion point in the code (the T3 fix has since added one —
`finishSentinelRollingUpdate`, ADR 0024 — which does not change this rejection:
the recurring-risk argument below stands on its own). Serialized, one sentinel at a time, waiting for `num-slaves`
to recover (~10 s each) before the next. Cures operator-driven rolls only,
leaves partial churn — the case that actually grows without bound — untouched,
and it must run on every roll forever for a problem the prevention options make
impossible. Hard gate required: reachable master, `flags=master`, no failover in
progress; a reset with the master down is unrecoverable (measured above).

**Option 4 — drift-triggered `SENTINEL RESET` in steady state (cure, broad).**
Detect `num-other-sentinels > live-1` in the existing health pass and reset one
sentinel per pass, gated on a healthy master, all sentinel pods Ready, no
failover in progress, and a cooldown. Covers every cause including partial
churn, and needs no completion marker. It is also the only option that heals the
fleet automatically — the prevention options do not clean up entries that
already exist. Biggest new write surface into the quorum machinery, and every
gate is load-bearing.

**Option 5 — surface only (add-on, not a fix).**
Parse `num-other-sentinels` in `parseSentinelMasterInfo`, compare against live
sentinels in `checkSentinel`, and raise a condition (e.g. `SentinelPeersStale`)
with the per-sentinel counts. Zero extra connections, and the collector turns
any new condition into a metric for free. Makes the state visible fleet-wide and
makes a manual one-time `SENTINEL RESET` verifiable instead of hopeful.

### Recommendation

**Option 1 + Option 5.** Option 1 removes the cause rather than the symptom, and
it is the only one that also covers the unbounded case (partial churn) without
the operator writing into the quorum machinery at all; it is a config line, and
it is measured to fix the exact failure the harness reproduced. Option 5 costs
almost nothing and answers the question the prevention leaves open — the
existing 4/3/2 tables do **not** heal by themselves at the fix roll (the pods
rolled first still witness the identity change of the pods rolled after them);
they clear at the *next* roll, or immediately via a one-time manual
`SENTINEL RESET <monitor>` per cluster with the master healthy. The condition
tells an operator which clusters still carry drift and confirms when it is gone.

Option 3 is rejected: it pays a recurring risk on every roll for a class of
causes the prevention already covers, and it depends on T3. Option 4 is held in
reserve — if the condition from Option 5 shows drift reappearing after Option 1
is live, that is the evidence that automatic healing is worth its gates. Option
2 is optional and independent; take it for the readability win if the DNS
dependency is acceptable.

Either fix needs an ADR (sentinel identity and peer-table hygiene) and an e2e
that replaces a sentinel pod twice and asserts `num-other-sentinels` stays at
`replicas-1`.

### Decision

- 2026-08-23: analysis and options recorded.
- 2026-08-23: **Option 1 + Option 5 accepted.** Pin `sentinel myid` per pod in
  the sentinel init container (prevention), and parse `num-other-sentinels` in
  the health pass to raise a `SentinelPeersStale` condition (detection). Option 3
  rejected — recurring risk on every roll for a cause class prevention already
  covers, and it depends on T3. Option 4 held in reserve: if the condition shows
  drift reappearing after Option 1 is live, that is the evidence its gates are
  worth paying. Option 2 (`announce-ip`) not taken — the DNS dependency in the
  sentinel-to-sentinel path buys only readability.
- 2026-08-23: **Implemented.** `sentinel myid` is derived from the pod hostname in
  the Sentinel init container
  ([`internal/builder/sentinel.go`](../../../internal/builder/sentinel.go),
  `buildSentinelInitCommand`), `sha1sum` is declared in `RequiredImageTools`,
  `num-other-sentinels` is parsed into `SentinelMasterInfo`, `observeSentinels`
  carries the per-pod counts in `ClusterState`, and `recordSentinelPeerDrift` writes
  the `SentinelPeersStale` condition. Rationale, alternatives and residual risks:
  [ADR 0022](../../adr/0022-sentinel-identity-is-pinned-to-the-pod.md).

  One thing the analysis had not anticipated and the live check caught: the condition
  is written only on a reconcile pass, and a healthy cluster does not reconcile —
  the CR watch is generation-gated and the next pass is the manager's 10 h cache
  resync. A cluster with real drift therefore kept reporting `False`. A cluster that
  *does* report drift now asks to be looked at again every 5 minutes
  (`sentinelPeerDriftRecheckInterval`) and stops once the tables agree, which is what
  makes the T9 reset verifiable at all (ADR 0022 D7).

  Verified: `make test-unit`, `make test-integration`, `make lint`, `make cyclo`,
  `make test-image-tools` all pass. In a Kind cluster:
  `TestE2E_SentinelPeerTableSurvivesPodReplacement` passes (peer tables stay 2/2/2
  across two replacements of the same sentinel pod, `SENTINEL MYID` unchanged, and a
  Sentinel failover still completes afterwards); on a cluster driven into real drift
  by a rogue sentinel joining the monitor group, the condition read True with all
  three pods named, the T9 procedure below cleared the tables (peers back to 2
  immediately, `num-slaves` back within ~9 s per pod), and the condition flipped to
  False about 4 minutes later.

  Not done, deliberately: the wrong comment above `resetSentinelState`
  ([`internal/controller/rolling_update.go:2346-2348`](../../../internal/controller/rolling_update.go#L2346-L2348))
  is left standing — this change does not touch that function, and it is recorded in
  ADR 0022 for whichever change does.

- 2026-08-23: **Existing fleet drift is remediated manually, once.** The
  prevention does not clean up entries that already exist, and it must not: a
  reset is a write into the quorum machinery with a hard precondition. Ops step
  per sentinel-enabled cluster, in a window with a healthy master:
  `SENTINEL RESET <monitor-name>` on each sentinel pod, one at a time, waiting
  until `num-slaves` recovers (~10 s) before the next. **Never with the master
  unreachable** — measured above, the sentinel loses peers and replicas with no
  way back. The `SentinelPeersStale` condition tells which clusters still need
  it and confirms when they do not. Tracked as T9.

### Reproducing the measurements

Not committed to the repo (scratchpad only). Recipe: docker network with a fixed
subnet, one `valkey-server`, three `valkey-sentinel` containers each with its own
host directory mounted at `/etc/sentinel` holding a config equivalent to the
generated one (`sentinel monitor <mon> <master> 6379 2`,
`down-after-milliseconds 5000`, `failover-timeout 10000`, `resolve-hostnames yes`,
`announce-hostnames yes`; no TLS, no auth). A pod replacement is
`docker rm -f` + a fresh config directory + a **new static IP**; the pinned
variant appends `sentinel myid <40 hex>` to that fresh config, the announce
variant appends `sentinel announce-ip <container hostname>`. Read the counter
with `valkey-cli -p 26379 sentinel master <mon>`.

## T2: Persistence toggled on an existing cluster is silently unsupported — no guard for immutable volumeClaimTemplates

**Severity: high (this is what keeps gitlab-valkey unreconcilable). Status:
open — analysis complete 2026-08-23 (envtest probe + ADR cross-check +
adversarial review), options below await a decision.**

Mechanism, verified in code: `BuildStatefulSet` makes the volumeClaimTemplate
the only source of a volume named `data` when persistence is enabled — the
emptyDir `data` volume is added only when persistence is disabled
(`internal/builder/statefulset.go:539-547`), the container always mounts `data`
as `containers[0].volumeMounts[1]` (`statefulset.go:684-687`), and VCTs are
attached only when persistence is enabled (`statefulset.go:151-154`).
`reconcileStatefulSet` copies Replicas, Template, and Labels onto the live
object and never touches `Spec.VolumeClaimTemplates`
(`internal/controller/valkey_controller.go:1224-1229`) — which are immutable on
a StatefulSet anyway. A cluster whose STS was created without persistence
therefore gets a desired template that mounts `data` while no volume of that
name can ever exist on the live object. The API server rejects every update
(`spec.template.spec.containers[0].volumeMounts[1].name: Not found: "data"`),
`StatefulSetHasChanged` re-detects the same diff each pass
(`statefulset.go:1133-1141`), and the CR is permanently blocked with the
catch-all `WriteFailed` reason carrying the raw API error
(`internal/controller/reconcile_blocked.go:59-68`).

No code path detects the case: no guard, no dedicated event, no ADR
(`docs/adr/README.md` has no persistence-migration entry), no webhook
(ADR 0015 is schema-only). README.md:1138-1143 names "a field that is immutable
on an already created object" as the failure shape the
generation/observed-generation metric pair exists to catch — acknowledged at
monitoring level, unhandled in code. README.md:808-816 documents
`spec.persistence` with no migration caveat.

### The full failure surface (measured 2026-08-23, envtest kube-apiserver 1.29.0)

The original entry covered only the enable direction. A probe against a real
apiserver (standalone envtest harness, scratchpad only, recipe at the end of
this item) established that **every direction of the toggle fails, each in a
different way**:

| # | Spec change | What actually happens |
|---|---|---|
| 1 | `enabled` false → true | Update rejected every pass (`volumeMounts[1].name: Not found: "data"` — the live gitlab error). CR blocked forever under catch-all `WriteFailed`, retried at the 30 s rate-limiter cap, **no Event**. |
| 2 | `enabled` true → false | Update **accepted**: the template gains the emptyDir `data` volume while the untouched VCTs stay on the object — the apiserver does not cross-validate template volume names against VCT names on update (probe EXP2; persisted state re-read and verified). No error ever, STS keeps its VCTs forever. Upstream statefulset-controller `updateStorage()` replaces a same-named template volume with the VCT-derived PVC volume in generated pods — knowledge-based, not run. **Silent spec drift.** |
| 3 | `size`/`storageClass` change, `enabled` stays true | The operator never writes at all: `StatefulSetHasChanged` ignores `Spec.VolumeClaimTemplates` and neither the pod-spec hash nor the config hash covers size/class (`statefulset.go:1123-1172`, `configmap.go:186-246`). **Silent no-op.** (A direct VCT update would be rejected by the STS spec whitelist anyway — probe EXP4a/4b.) |

Two more probe facts that constrain any fix:

- Every VCT-touching update variant (add, clear to nil, resize, re-class) fails
  with the **same** field-level `Forbidden` cause on `spec` — HTTP 422,
  `apierrors.IsInvalid` true, `IsForbidden` **false** — so neither the error
  type nor the status reason can distinguish causes. Detection must compare
  objects, not parse errors.
- Orphan-delete + recreate with different VCTs is accepted by the apiserver
  (EXP5). Pod **re-adoption** by the recreated StatefulSet cannot be verified in
  envtest (no controller-manager) and is the one step ADR 0020 itself marks as
  "asserted from the API contract, reproduced nowhere in this repo"
  (`docs/adr/0020:179-183`, `:412-416`).

### The ConfigMap converges first — no fix can claim "nothing changes while blocked"

The ConfigMap step runs before the StatefulSet step and keeps succeeding when a
later step fails (step order `valkey_controller.go:472-484`; `runReconcileSteps`
at `:438-449` continues past failures by design, ADR 0001). So on every shape
the **first** pass after the spec edit persists the new `save`/`appendonly`
lines into the ConfigMap the pods mount (`configmap.go:186-246`), and every
organically restarted pod boots the new persistence config against the old
volume layout. On wds18 that restart arrives within minutes (Chaos Mesh
pod-kill every 5 min). Data safety is governed by the replication chain, not
the dumps — a restarted pod rejoins as replica and resyncs — so this is a
consistency wart, not data loss; but a fix must either hold the persistence
lines too or own the mixed state explicitly (sub-decision A2 below).

### Relevant code

| Site | Why it matters |
|---|---|
| [`internal/builder/statefulset.go:151-154`](../../../internal/builder/statefulset.go#L151-L154), [`:539-547`](../../../internal/builder/statefulset.go#L539-L547), [`:684-687`](../../../internal/builder/statefulset.go#L684-L687) | VCTs only when enabled; emptyDir only when disabled; unconditional `data` mount |
| [`internal/builder/statefulset.go:1086-1116`](../../../internal/builder/statefulset.go#L1086-L1116) | `buildVolumeClaimTemplates` — name/size/class/accessModes **plus labels including the image-version label**: the false-positive trap for any naive desired-vs-live comparison (live VCTs are frozen; desired labels move with every image bump) |
| [`internal/builder/statefulset.go:1123-1172`](../../../internal/builder/statefulset.go#L1123-L1172) | `StatefulSetHasChanged` + hashes: VCTs never compared; size/class in no hash |
| [`internal/controller/valkey_controller.go:1187-1232`](../../../internal/controller/valkey_controller.go#L1187-L1232) | `reconcileStatefulSet`: create-on-NotFound rebuilds **with** VCTs (`:1196-1201`); `IsControlledBy` guard (`:1214-1219`); copies Replicas/Template/Labels only (`:1224-1229`) |
| [`internal/controller/valkey_controller.go:472-484`](../../../internal/controller/valkey_controller.go#L472-L484), [`:438-449`](../../../internal/controller/valkey_controller.go#L438-L449) | step order (ConfigMap first, STS sixth) and continue-past-failure (ADR 0001) |
| [`internal/controller/valkey_controller.go:1093-1119`](../../../internal/controller/valkey_controller.go#L1093-L1119) | RoleBinding roleRef precedent: inline delete+recreate with UID precondition — but RoleBindings hold no data and have no pods; the precedent does not transfer its safety |
| [`internal/controller/reconcile_blocked.go:59-68`](../../../internal/controller/reconcile_blocked.go#L59-L68) | three-reason taxonomy, `WriteFailed` catch-all; reason chosen via `errors.Is` over the joined pass error — a new sentinel error must survive unwrapping (ADR 0020 residual note) |
| [`internal/controller/foreign_object.go:63-78`](../../../internal/controller/foreign_object.go#L63-L78), [`:182-203`](../../../internal/controller/foreign_object.go#L182-L203) | `errForeignObject` — the template for a new sentinel error; `deleteIfOwned` — UID precondition only, **no propagation-policy parameter** (an orphan delete needs a new variant) |
| [`deploy/helm/valkey-operator/templates/prometheusrule.yaml:51-55`](../../../deploy/helm/valkey-operator/templates/prometheusrule.yaml#L51-L55), [`:63-66`](../../../deploy/helm/valkey-operator/templates/prometheusrule.yaml#L63-L66) | `ValkeyReconcileBlocked` matches **any** reason (no chart logic change needed); its description enumerates the reasons and goes stale |
| [`internal/controller/valkey_controller_test.go:1285-1313`](../../../internal/controller/valkey_controller_test.go#L1285-L1313) | the existing green test a guard inverts: toggles persistence on an existing cluster, asserts ConfigMap convergence, `require.NoError` on the pass |
| [`api/v1/valkey_types.go:68-88`](../../../api/v1/valkey_types.go#L68-L88), [`:48-53`](../../../api/v1/valkey_types.go#L48-L53) | reason constants and the `ReconcileBlocked` doc comment (already stale re `ForeignObject`; worse with a fourth reason) |

### Options

**Option A — guard + surface (favoured).** A semantic VCT comparison, one
shared function called from both `reconcileStatefulSet` and
`reconcileSentinelStatefulSet` (sentinel is empty-vs-empty today — free, and
future-proof against a sentinel VCT feature reintroducing the trap), running
after the `IsControlledBy` guard and before drift detection:

- **Comparison whitelist, nothing else**: VCT count, name, storage request via
  `Quantity.Cmp` (semantic equality — `1Gi` == `1024Mi`; `equality.Semantic` is
  already used at `valkey_controller.go:1172`), `storageClassName` nil-aware
  (DefaultStorageClass admission touches PVC objects, not STS templates —
  knowledge-based), accessModes as a set. **Never** labels (image-version label
  → permanent false positive on every image bump), volumeMode/status/TypeMeta
  (apiserver-defaulted on the live object).
- **Presence/name mismatch (shapes 1+2, persistence toggled): block.** Skip the
  doomed STS Update, fail the step with a new sentinel error → new
  `ReconcileBlocked` reason (proposal: `RecreateRequired`; **not**
  `ImmutableFieldConflict` — "Conflict" is a term of art for the UID-precondition
  guard, ADR 0006 D10, and no shape ever produces an immutable-field API error),
  ranked below `ForeignObject` (structural anyway — the foreign check returns
  first), above `AdmissionWebhookDenied`. One Warning Event per kind (proposal:
  `StatefulSetRecreateRequired`) carrying the supported manual path
  (orphan-delete → operator recreates → failover-aware roll) and the warnings:
  never without `--cascade=orphan`; single-replica-without-persistence loses the
  in-memory dataset on the roll; the disable direction leaves the old PVCs
  behind — manual deletion, data-retention note.
  *(Superseded 2026-08-23: the Event names no procedure at all for the enabling
  direction. Walking that path in Kind measured it to wedge and then to lose the
  dataset — T10, T11. The shipped Event states the cost and names the revert.
  Amended 2026-08-26: the loss half is fixed (T11, ADR 0028) — clearing the wedge
  now ends in a visible split brain rather than an empty cluster. The wedge itself
  is unchanged, so the Event stays as shipped.)*
  Honest consequence, stated in
  ADR and Event: **while blocked, replicas scaling, image and label updates are
  held too** — they ride the same Update (`valkey_controller.go:1224-1229`).
  Fail direction per ADR 0020 D2: `persistence.enabled=true` on a memory-only
  cluster is a durability statement that is not true — the NetworkPolicy
  precedent transfers.
- **Parameter-only mismatch (shape 3, size/class): surface without failing.**
  The template update is legal and orthogonal to the mismatch — holding it
  hostage would wedge a GitOps atomic apply of size+image exactly the way
  ADR 0015 D4 describes. Dedicated condition + Event + recheck (the non-fatal
  pattern of the metrics Service, `foreign_object.go:123-146`), template/replica
  writes proceed.
- **Surfacing rides existing machinery**: the condition auto-exports
  (ADR 0021 D1/D3), `ValkeyReconcileBlocked` fires for any reason, phase flips
  to `Error` via the blocked-pass phase authority. Known limitation to record:
  `ValkeySpecNotObserved` will **not** fire (the blocked condition stamps the
  current observedGeneration), so the two named alerts are the only metric
  signals — and the whole PrometheusRule is default-off.
- **Tests per ADR 0017**: unit (mismatch function incl. "image change does not
  trip the guard"; controller-level "no doomed Update issued"; pin
  "BuildSentinelStatefulSet emits no VCTs"); envtest (real apiserver rejects the
  doomed update and the guard prevents it; **negative case: an untouched
  persistent cluster produces no mismatch** against apiserver-defaulted live
  VCTs — the false-positive surface the fake client cannot cover); e2e for the
  advertised manual path (orphan-delete → recreate → re-adopt → failover-aware
  roll onto PVCs, both directions) — required because the Event actively
  recommends a path whose adoption step ADR 0020 marks unverified, and
  ADR 0017 D9 demands the fails-before-fix demonstration.
- **Docs in the same change**: README migration caveat + condition-table text;
  CLAUDE.md example-CRD persistence block gets the caveat line its siblings
  have; prometheusrule description; `api/v1` reason comment (`:48-53`);
  SECURITY_ARCHITECTURE sentence on left-behind PVCs; **ADR 0023** + index line.
  ADR 0023 additionally records: scope statement (VCTs are today the only
  spec-varying immutable STS field — ServiceName/Selector/PodManagementPolicy
  are CR-name constants, and nothing sets persistentVolumeClaimRetentionPolicy);
  the guard-vs-mid-rolling-update interaction (stateless per-pass refusal; an
  in-flight update completes against the persisted template, ADR 0007 D2); the
  defense-in-depth benefit (after a restore/mimic the guard blocks foreign VCTs
  — the exact hazard 0020's rejected auto-re-own alternative names); the
  unverified items (`updateStorage` volume replacement, re-adoption, probe run
  against kube-apiserver 1.29 only); and the honest pro-D counterargument (an
  admission rejection reaches the human making the edit; a condition reaches
  only someone who looks).
- **Upgrade visibility, named per case**: shape-2/3 clusters that show `OK`
  today (silent drift) show the new condition — shape 2 additionally flips
  phase to `Error`. Shape-1 clusters (gitlab) change reason
  `WriteFailed` → `RecreateRequired`. One release-notes line each; the existing
  green test `TestReconcile_UpdatesConfigMapOnSpecChange` is rewritten, not
  deleted.

**Sub-decision A2 — the ConfigMap during a shape-1/2 conflict.**
(i) *Accept and document*: ConfigMap keeps converging to the spec; restarted
pods boot the new persistence config against the old volumes until the
migration happens. Consistency wart, not data loss; zero coupling.
(ii) *Live-STS authority*: while the conflict stands, render the persistence
lines from what is materialized (live VCT presence), so config and volumes stay
consistent and the roll fires once, after the migration. Cost: config rendering
and the config hash gain a live-cluster input — a structural change to the
ADR 0007 hash architecture. **Recommendation: (i)**, with the mixed state named
in ADR 0023 and the Event text; revisit (ii) only if A's condition shows
conflicts routinely living unmigrated for months (gitlab: 4 and counting).

**Option B — automated orphan-delete + recreate, unconditional (rejected).**
Fatal: it fires at operator-upgrade time. Every shape-2 silent-drift cluster in
the fleet would get its StatefulSet orphan-deleted and its pods rolled through
failovers on upgrade day, up to `maxConcurrentReconciles=4` CRs simultaneously —
PDBs bind only the Eviction API (`pdb.go:23-31`), and the obvious fleet-wide
throttle is exactly what ADR 0019 D3 forbids. Violates the standing
upgrade-neutral-defaults rule. Further serious gaps that price Option C:
`reconcileStatefulSet` has no DeletionTimestamp branch, so during
orphan-finalization the operator would fire the doomed Update onto the
terminating STS (needs a deleting-STS wait state); the re-adoption wait has no
forcing action, so its only honest ADR 0010 successor state **is Option A's
blocked condition** — B structurally contains A as its failure mode, which makes
A-first the dependency order, not caution; during the window the CR speaks
through falsified vocabulary (`foreignObjectError` is documented "clears only
when a human acts", `PodNotOwned` warnings per pod per pass) and needs its own
Migrating state; an in-flight rolling update is parked, not driven, while its
bounds age (`rolling_update.go:156-162`). Confirmed safe if ever built:
crash-window converges (NotFound → Create with VCTs), every consumer's fail
direction during the ownership gap is refuse/wait, never a wrong delete, the
sidecar Role keeps its ordinal-derived pod names, and the roll itself would
fire correctly (pod-spec hash covers volumes).

**Option C — A now, automation later behind explicit opt-in (held in reserve).**
Same reserve pattern as T1's Option 4. The opt-in must be a **spec field**
(e.g. `persistence.migrationPolicy: Manual|OrphanRecreate`, default `Manual`),
not an annotation: the CR watch is generation-gated, so an annotation edit on a
healthy CR is invisible until the ~10 h resync, and no user-set `vko` annotation
precedent exists (all are operator-written state). A spec field bumps the
generation, is schema-documented, and matches every existing opt-in
(`podDisruptionBudget.enabled`, `antiAffinity.mode`, `metrics.enabled`). GitOps
consequence stated up front: a Git-resident standing policy turns any future
accidental toggle into an automated migration — the field name must be honest
about being standing policy. Trigger for taking C: A's condition showing
conflicts that live unmigrated across the fleet instead of being resolved via
the documented path. C inherits B's serious findings as its work list
(deleting-STS state, bounded re-adoption expiry into A's condition, Migrating
vocabulary, fleet pacing decision, PVC disposition on disable).

**Option D — CEL transition rule making `spec.persistence` immutable (rejected).**
ADR 0015 D3 names CEL as a permitted channel and D2 (no webhook) would be
untouched — but D5's own split assigns this defect to the operator: "Schema and
cluster policy engines own the rejecting half of input validation; the operator
owns convergence of the running topology." A persistence toggle is valid input;
the defect is convergence against the live StatefulSet, and old-spec-vs-new-spec
is the wrong proxy for spec-vs-live-STS: it rejects safe edits (size change
before the STS exists; any edit after a manual migration already cleared the
conflict) and **deadlocks the documented manual path** — the spec edit is step 1
of it (ADR 0020:179-183), leaving CR delete+recreate as the only migration:
cascade delete of every child except the PVCs, a full outage. ADR 0015 D4's
GitOps-wedging argument applies verbatim and worse (no legal edit ordering
exists at all). Softer variants (forbid only disable/resize) split one defect
family across two enforcement surfaces with different failure UX that drift
independently of the builder. Two corrections from review: the chart-CRD sync
is **not** an anti-D argument (the chart CRD is generated, `sync-helm-crd`);
and `optionalOldSelf` is gated behind CRDValidationRatcheting (default-on from
1.30, knowledge-based) — do not restate "the 1.29 floor covers it" as fact.

### Recommendation

**Option A, with sub-decision A2 = (i).** A is the only option that makes all
three shapes visible and actionable without any new write surface into the data
plane; it is the dependency of any later automation (B's own failure analysis
terminates in A's condition); and it converts the T8 class from catch-all
`WriteFailed` with a raw API error into a named reason with the supported path
in the Event — the same shape T1 chose: prevention/surface over cure, automation
as a separate, evidence-triggered decision. C stays in reserve with its trigger
named. B and D are rejected for the reasons above. T8's remediation on wds18
stays cluster ops and is possible today via T8 (a)/(b) regardless of this
decision.

### Decision

- 2026-08-23: analysis complete — envtest probe of all toggle directions, ADR
  cross-check, adversarial review of all four options. Options and
  recommendation recorded above.
- 2026-08-23: **Option A accepted, with sub-decision A2 = (i).** Guard the
  volumeClaimTemplates comparison in the StatefulSet reconcilers and surface the
  conflict; the ConfigMap keeps converging to the spec while a conflict stands,
  and the mixed state is documented rather than engineered away. Option B
  rejected — an unconditional automated migration fires on upgrade day against
  the existing silent-drift fleet, up to `maxConcurrentReconciles` clusters at
  once, with no throttle that ADR 0019 D3 permits. Option C held in reserve with
  a named trigger (conflicts living unmigrated across the fleet) and a named
  mechanism (a spec field, never an annotation — the CR watch is
  generation-gated). Option D rejected — CEL can only compare old spec against
  new spec, which is the wrong proxy for spec against live StatefulSet, and it
  deadlocks the documented manual migration whose first step is exactly that
  spec edit.
- 2026-08-23: **Implemented.** `VolumeClaimTemplatesConflict`
  ([`internal/builder/volumeclaim_conflict.go`](../../../internal/builder/volumeclaim_conflict.go))
  compares the claims semantically on name, size (`Quantity.Cmp`), storage class
  and access modes — **never labels**, which carry the image version and would
  false-positive on every image bump of every persistent cluster.
  `guardVolumeClaimTemplates`
  ([`internal/controller/volumeclaim_conflict.go`](../../../internal/controller/volumeclaim_conflict.go))
  runs in both StatefulSet reconcilers, after the ownership guard and before the
  drift check. Structural conflict → new `ReconcileBlocked` reason
  `RecreateRequired`, `StorageSpecNotApplied=True` and a Warning Event, pass
  fails; parameter conflict → condition and Event only, pass continues.
  Reasoning, alternatives and residual risks:
  [ADR 0023](../../adr/0023-volume-claim-templates-are-immutable.md).

  Verified: `make test-unit`, `make test-integration`, `make lint`, `make cyclo`
  all pass. Mutation checks per ADR 0017 D7, both run and reverted
  byte-identical: adding a label comparison to `claimParameterDetail` fails the
  image-bump test (`Should be empty, but was data: labels differ`); removing the
  guard call from `reconcileStatefulSet` fails four tests with `An error is
  expected but got nil`. The integration tier pins the apiserver premises
  directly — adding a volumeClaimTemplate to a live StatefulSet is rejected,
  shadowing an existing claim with an emptyDir is accepted. In Kind: enabling
  persistence on a running three-replica cluster is refused with both conditions
  set, no pod rolled and no PVC created.

  **Two findings the Kind verification produced, neither anticipated by the
  analysis — T10 and T11 below.** T11 is fixed since 2026-08-26 (ADR 0028); T10 is
  not, and either one alone invalidates the manual migration that this
  ticket, ADR 0020 D1 and the first draft of ADR 0023 all recommended for the
  *enabling* direction. The shipped Event, README, PrometheusRule description and
  ADR were rewritten before merge: they state the cost and the free way out
  (revert the spec) instead of naming a procedure measured to wedge. The e2e was
  reduced to the half that is true — the refusal on a running cluster, and the
  revert — because a test walking the broken migration would enshrine it.

### Reproducing the probe

Not committed (scratchpad only): standalone Go module `stsprobe` starting
envtest with the repo's own binaries
(`KUBEBUILDER_ASSETS=bin/k8s/1.29.0-darwin-arm64`), controller-runtime v0.24.1
and k8s.io v0.36.4 pinned to match `go.mod`. Experiments: create emptyDir STS →
add VCT (rejected, spec whitelist); create VCT STS → template-only emptyDir add
with VCTs untouched (accepted, both persisted); clear VCTs (rejected); resize /
re-class VCT (rejected, same error); orphan-delete + recreate with added VCT
(accepted; orphan finalizer had to be stripped manually — envtest runs no
controller-manager, so GC and re-adoption are out of scope there).

Related: `037-recovery-after-transient-admission-webhook-rejection.md` (validation-gap family, but a
different incident; do not merge the tickets).

## T3: RollingUpdateComplete fires before the sentinel tier is rolled — the sentinel tier has no completion marker

**Severity: medium. Status: DONE in the repo 2026-08-23 (Option 3 + sub-decision
3a, see Decision). The sentinel tier now carries the `SentinelUpdatePending`
condition and emits `SentinelUpdateComplete`; ADR 0024.**

Verified from the log: on every sentinel-enabled cluster the
`RollingUpdateComplete` event and the state-cleared marker were emitted before
any sentinel pod was replaced; sentinel rolling continued for up to ~90 s
afterwards (last activity harbor-valkey-sentinel-2 delete + quorum wait at
21:35:06) and has no completion marker of its own. The audit could not prove
sentinel-tier completion from the log at all — only from live cluster state.

Consequence at the time of the audit (resolved by the fix below): "Completed"
in status and events did not mean the update was finished; anything sequencing
on it (a human, a pipeline) acted too early. Since the fix, the update as a
whole is finished when `SentinelUpdatePending` reads False/`Completed` — the
`RollingUpdateComplete` event still fires at data-tier completion, by design
(ADR 0024 D1).

*(Superseded 2026-08-23: the original entry called T3 "the natural hook point
for T1". That motivation is gone — T1's Decision rejected the reset-after-roll
option (T1 Option 3), so nothing in the repo plans to sequence on this marker.
The remaining consumers are humans, pipelines, and the audit trail itself.)*

### Mechanism, verified in code (2026-08-23)

- **The ordering is structural, not incidental.** `reconcileWorkload` runs the
  data-tier rolling update first
  ([`valkey_controller.go:321-328`](../../../internal/controller/valkey_controller.go#L321-L328));
  only a pass in which it neither errors nor requeues reaches the sentinel
  check (`handlePostRollingUpdateChecks`,
  [`valkey_controller.go:374-391`](../../../internal/controller/valkey_controller.go#L374-L391)).
  The data tier therefore always completes — including its completion event —
  before the first sentinel pod is touched.
- **Both `RollingUpdateComplete` emission sites are data-tier-only** and clear
  the rolling-update state annotation in the same breath:
  `finalizeRollingUpdate`
  ([`rolling_update.go:594`](../../../internal/controller/rolling_update.go#L594))
  and `verifyTopologyRestored`
  ([`rolling_update.go:3456`](../../../internal/controller/rolling_update.go#L3456)).
  The ADR 0010 bounded-wait machinery ends here **by design** — the absence of
  the state annotation is what means "no data-tier update in flight" (nothing
  calls `detectAndResolveSplitBrain` once it is gone).
- **The sentinel roll is stateless.** `checkAndHandleSentinelRollingUpdate`
  ([`rolling_update.go:3524-3590`](../../../internal/controller/rolling_update.go#L3524-L3590))
  compares pods against the persisted template each pass
  (`sentinelPodNeedsUpdate`), deletes at most one outdated pod under a quorum
  guard, and requeues. When no pod is outdated it returns an empty result
  ([`:3573-3576`](../../../internal/controller/rolling_update.go#L3573-L3576)) —
  the steady-state answer of every healthy pass, indistinguishable from "a roll
  just finished". No event, no condition, no annotation, no phase write
  anywhere in the path; log lines are the only trace.
- **The status surface is wrong during the sentinel roll** (code reading, not
  observed live — the audit did not record phase during the window): while
  sentinel pods roll, every pass ends at the requeue in
  [`valkey_controller.go:388-390`](../../../internal/controller/valkey_controller.go#L388-L390)
  *before* `updateStatus` runs
  ([`:341-344`](../../../internal/controller/valkey_controller.go#L341-L344)).
  After an image bump the phase keeps showing the last data-tier value
  ("Rolling Update N/N"); on a sentinel-only spec change (sentinel podLabels,
  resources) no data roll happens at all and the phase shows `OK` for the whole
  roll. Both violate the CLAUDE.md status contract ("OK when healthy, otherwise
  the current task").
- **A second, independent convergence predicate already exists and must not be
  reused for this.** `sentinelRolloutComplete`
  ([`valkey_controller.go:1601-1668`](../../../internal/controller/valkey_controller.go#L1601-L1668),
  gates the legacy-certificate cleanup) is revision-based, while the roll
  driver is image/hash-based (`sentinelPodNeedsUpdate`,
  [`rolling_update.go:3478-3510`](../../../internal/controller/rolling_update.go#L3478-L3510)).
  The predicates can disagree: a template change covered by no hash bumps the
  controller revision but rolls nothing, so a revision-based completion marker
  could wait forever for pods the driver will never replace. Any completion
  marker must use the driver's own predicate.

### Relevant code

| Site | Why it matters |
|---|---|
| [`internal/controller/valkey_controller.go:321-339`](../../../internal/controller/valkey_controller.go#L321-L339) | data roll strictly before sentinel roll; sentinel requeue skips `updateStatus` |
| [`internal/controller/valkey_controller.go:374-391`](../../../internal/controller/valkey_controller.go#L374-L391) | sentinel branch of `handlePostRollingUpdateChecks`; called every pass on sentinel-enabled CRs — a completion detector here self-heals after a crashed roll |
| [`internal/controller/rolling_update.go:580-607`](../../../internal/controller/rolling_update.go#L580-L607), [`:3451-3458`](../../../internal/controller/rolling_update.go#L3451-L3458) | the two `RollingUpdateComplete` emissions, both data-tier |
| [`internal/controller/rolling_update.go:3524-3590`](../../../internal/controller/rolling_update.go#L3524-L3590) | the sentinel roll: quorum guard, one delete per pass, empty result when converged; the loop already computes readyCount and first-outdated — a completion predicate costs no extra API call |
| [`internal/controller/rolling_update.go:3478-3510`](../../../internal/controller/rolling_update.go#L3478-L3510) | `sentinelPodNeedsUpdate` — the driver's predicate, the one a marker must mirror |
| [`internal/controller/valkey_controller.go:2451-2491`](../../../internal/controller/valkey_controller.go#L2451-L2491) | `setStatusCondition`/`writeStatusCondition` — retry-on-conflict condition writes, ready to use |
| [`internal/controller/valkey_controller.go:2176-2219`](../../../internal/controller/valkey_controller.go#L2176-L2219) | `recordSentinelPeerDrift` — the T1 precedent for a sentinel condition with per-pod message |
| [`api/v1/valkey_types.go:24-81`](../../../api/v1/valkey_types.go#L24-L81) | existing condition types; `SidecarUpdatePending` is the naming precedent |
| [`internal/metrics/collector.go:184-192`](../../../internal/metrics/collector.go#L184-L192) | any new condition auto-exports as `vko_valkey_status_condition` (ADR 0021) |
| [`test/e2e/rolling_update_test.go:218`](../../../test/e2e/rolling_update_test.go#L218) | `TestE2E_RollingUpdate_HA` — the e2e to extend |

### Options

**Option 1 — move `RollingUpdateComplete` behind the sentinel tier.**
Emit the existing event only when both tiers have converged. Passes are
stateless, so "a roll was in flight" must be persisted past
`clearRollingUpdateState` — either the data-tier state machine survives into
the sentinel tier, which breaks the ADR 0010 invariant that the annotation's
absence means no update in flight (and re-opens the split-brain-resolver
lifetime question), or a second marker is introduced anyway, at which point
Option 1 contains Option 2 plus a semantic break: a sentinel-only roll has no
data-tier update and would emit a data-named event or nothing.

**Option 2 — annotation-based edge: `SentinelUpdateComplete` event only.**
Set a progress annotation on the first sentinel pod delete; on a later pass
with the annotation present and the tier converged (no outdated pod,
readyCount == replicas), emit the event and clear the annotation. Honest and
cheap, but it adds a new annotation lifecycle to reason about (crash windows,
sentinel disabled mid-roll, foreign STS), and an event is an edge only:
missable, not queryable after the fact, no metric. It duplicates memory the
status could carry.

**Option 3 — condition as the level, event as the edge (favoured).**
New condition `SentinelUpdatePending`, written from inside
`checkAndHandleSentinelRollingUpdate`, which already sees every fact it needs:

- Outdated pod found → condition True (reason `SentinelPodsOutdated`, message
  "i of n sentinel pods on an outdated spec"), written in the same pass that
  deletes — before the delete.
- Converged (no outdated pod **and** readyCount == replicas **and** no missing
  pod) **and** the condition currently reads True → flip to False (reason
  `Completed`) and emit one Normal `SentinelUpdateComplete` event. The
  condition's prior value is the memory — no annotation, no new state
  machine. A healthy steady-state pass (condition absent or False) writes and
  emits nothing, so the empty-result path stays as silent as today.
- Sentinel disabled mid-roll: the check is skipped entirely
  (`IsSentinelEnabled` gate), so the condition must be cleared explicitly on
  that path — named here so T3 does not spawn a T6-class stale condition.
- Foreign sentinel STS: treated as absent, no writes —
  `reconcileSentinelStatefulSet` stays the one reporter (ADR 0020).
- The condition auto-exports as a metric (ADR 0021): fleet-wide "who is
  mid-sentinel-roll" for free, read at collect time, no reconcile-written
  gauge.
- `RollingUpdateComplete` message reworded to name its scope ("all data pods
  running desired version") — message text, not a contract change.

Residual (accepted): a crash after the last pod delete but before the True
write loses the completion event for that roll — the condition stays
consistent (never True), only the edge is missed. The self-healing direction
is covered: a stale True from a crashed roll is flipped False by the next
converged pass, because the detector runs every pass.

**Sub-decision 3a — phase during the sentinel roll.** Write
"Sentinel Rolling Update i/n" via `updatePhase` while the roll is active; the
normal `updateStatus` of the first converged pass restores `OK`. Closes the
status-contract gap for both the image-bump tail (stale "Rolling Update N/N")
and sentinel-only rolls (misleading `OK`). Alternative: leave the phase
untouched and let the condition alone carry it — cheaper, but CLAUDE.md names
the phase as the surface that shows the current task.

Tests per ADR 0017: unit — condition True while a pod is outdated; completion
event emitted exactly once at convergence and only when the condition was True
(pass sequence against the fake client); steady-state pass writes nothing;
sentinel-disabled clears the condition; quorum-blocked roll keeps it True.
E2E — extend `TestE2E_RollingUpdate_HA`: after the image bump, assert
`SentinelUpdateComplete` arrives after `RollingUpdateComplete` and after every
sentinel pod is on the new template, and the condition ends False
(fails-before-fix: the event does not exist today).

Docs in the same change: new ADR (completion is reported per tier —
`RollingUpdateComplete` is the data tier, `SentinelUpdateComplete` the
sentinel tier; the condition as memory; Option 1 rejected because the state
annotation's absence must keep meaning "no data-tier update in flight",
ADR 0010) + index line; README events/conditions; CLAUDE.md status sentence if
3a is taken.

### Recommendation

**Option 3 + sub-decision 3a.** It is the only option that makes sentinel-tier
completion both provable from the log (event) and queryable afterwards
(condition, exported as a metric via ADR 0021) without inventing a new
annotation lifecycle — the memory lives in the status, where its consumers
look, and the detector self-heals because it runs on every pass. It mirrors
the shape T1 chose (condition riding existing machinery). Option 1 is rejected
for breaking the ADR 0010 invariant; Option 2 is Option 3 minus the level
signal at essentially the same complexity.

### Decision

- 2026-08-23: analysis and options recorded above.
- 2026-08-23: **Option 3 + sub-decision 3a accepted.** The `SentinelUpdatePending`
  condition is the level and its previous value the memory; the
  `SentinelUpdateComplete` event is the edge, emitted exactly when the condition
  flips back to False; the phase reads `Sentinel Rolling Update i/n` while the
  roll is active. Option 1 rejected — it breaks the ADR 0010 invariant that the
  state annotation's absence means no data-tier update in flight. Option 2
  rejected — Option 3 minus the level signal at the same complexity.
- 2026-08-23: **Implemented.** `checkAndHandleSentinelRollingUpdate` records the
  roll before acting (`recordSentinelUpdateProgress`: condition True with an
  updated-and-ready count, phase per the status contract) and hands the
  converged pass to `finishSentinelRollingUpdate`
  ([`internal/controller/rolling_update.go`](../../../internal/controller/rolling_update.go)),
  which requires every pod current **and** Ready, flips the condition, and emits
  the event only when the flip actually landed — `writeStatusCondition` now
  reports whether the write changed anything, which is what makes the edge
  exactly-once. A CR whose Sentinel is disabled mid-roll gets the condition
  cleared on the non-Sentinel path (`clearSentinelUpdatePending`, reason
  `SentinelDisabled`, no event — disabling is not completing). The
  `RollingUpdateComplete` message now names its scope (data pods). Rationale,
  alternatives, residual risks:
  [ADR 0024](../../adr/0024-the-sentinel-tier-reports-its-own-completion.md);
  README condition/phase tables and CLAUDE.md updated in the same change.

  Verified: `make test-unit`, `make test-integration`, `make lint`, `make cyclo`
  all pass. Mutation checks per ADR 0017 D7, both run and reverted
  byte-identical: removing the event emission fails
  `TestCheckAndHandleSentinelRollingUpdate_EmitsCompletionEventExactlyOnce`
  ("Exactly one completion event on the flip"); removing the readiness gate in
  `finishSentinelRollingUpdate` fails
  `TestCheckAndHandleSentinelRollingUpdate_NoCompletionWhileReplacementBoots`
  ("No completion event while a replacement pod is still booting"). In Kind:
  `TestE2E_RollingUpdate_HA` extended with "Sentinel tier reports its own
  completion" — asserts the condition ends False/Completed after every sentinel
  pod is current and Ready, and that the `SentinelUpdateComplete` event exists
  and is not recorded before `RollingUpdateComplete`; the full test passes
  against the rebuilt operator image (97.66 s, all 11 subtests green,
  2026-08-23).

  Known limitation, recorded in ADR 0024: the quorum/readiness wait of the
  sentinel roll remains unbounded, as it always was — this change makes it
  visible (condition True, phase naming the roll), it does not bound it. A
  crash after the last pod delete but before the True write loses that roll's
  completion event; the condition stays consistent.

## T4: Transient SplitBrainDetected warnings during every controlled failover

**Severity: medium (operator ergonomics, alarms). Status: DONE 2026-08-24 —
Option 5 implemented in full, including sub-decision (e).
[ADR 0025](../../adr/0025-a-split-brain-warning-means-one-that-did-not-resolve-itself.md) carries
the decision; ADR 0011 D20 amended in place. See *Implementation* at the end of this item for
what was verified and how.**

Verified from log + events: 6 clusters emitted Warning `SplitBrainDetected`
("2 pods report master role") within ~1 s of the operator's own controlled
promotion (valkey8-tls 21:33:27, valkey9-tls 21:33:28 twice, valkey9 21:33:28,
valkey9-sentinal-tls 21:33:29, valkey9-sentinal 21:33:29, valkey8-sentinal-tls
21:33:32). All resolved within the same minute; end states verified clean.

A Warning event named "split-brain" during a planned update trains operators to
ignore the one Warning that must never be ignored.

### What the mechanism actually is (verified in code 2026-08-23)

The original framing — "the demotion of the outgoing master races its own pod
termination" — is half the story and the wrong half is load-bearing. Three
separate facts produce the observed storm.

**1. Two masters during a controlled failover are not a race, they are the
design.** ADR 0008 says it verbatim
([`0008-…:26`](../../adr/0008-known-master-annotation-is-the-recorded-authority.md)):
"During a manual failover two pods report master **by design** — the promoted
pod (`REPLICAOF NO ONE`) and the old master, which answers until it
terminates." There are ten such windows across both topologies (Sentinel: the
gap between Sentinel promoting the replica and reconfiguring the old master; a
half-completed failover in `failover-reset`; the Terminating ex-master in
`replacing-master`; a drain-handler failover. Non-Sentinel: the in-pass
promote→demote gap, a failed best-effort demotion, the Terminating ex-master via
the label fallback, a self-elected returning pod-0, both topology-restoration
phases). **In every one of them except two, an authority name is available and
is itself one of the reported masters.** The two exceptions are the in-pass
window (invisible to any resolver pass) and a failed `persistManualFailoverState`
([`rolling_update.go:2794-2799`](../../../internal/controller/rolling_update.go)) —
which is the documented ADR 0008 D10/D11 data-loss shape and must keep its
Warning.

**2. `detectAndResolveSplitBrain` reports before it knows anything.** The
Warning at
[`rolling_update.go:1078`](../../../internal/controller/rolling_update.go) fires on
`len(masterIndices) > 1`, four lines after the count and **before** the
authority is consulted at :1085. It has no way to distinguish the ten designed
windows from the two undesigned ones, because at that point it has not looked.

**3. A Terminating ex-master is manufactured back into a master by its own
stale label.** `collectPodStates` has no `DeletionTimestamp` guard
([`rolling_update.go:1247-1297`](../../../internal/controller/rolling_update.go));
its only filters are NotFound, foreign provenance and readiness. When
`GetReplicationInfo` fails, :1287 trusts `vko.gtrfc.com/instanceRole` — and
nothing clears that label at delete time (the labeler polls at 1 s and the
kubelet gives no ordering against the delete, ADR 0012). So the operator demotes
the outgoing master at :2903 exactly as intended, deletes it, the pod stops
answering, and the *label* resurrects it as a second master. `demoteRogueMaster`
then refuses it — `"rogue pod %s is not ready for demotion"`
([:1198-1200](../../../internal/controller/rolling_update.go)) — while :1133 still
clears `isMaster` locally, so **the Warning re-fires every pass with no
`SplitBrainResolved` ever closing it**. That is precisely the "not ready for
demotion" / "no route" log signature the audit recorded, and it is the largest
contributor to the storm.

Two amplifiers on top:

- **`SplitBrainResolved` is itself typed Warning**
  ([:1218](../../../internal/controller/rolling_update.go)) although it reports a
  repair that *succeeded*. Fixing only :1078 leaves a Warning storm. It has a
  second caller that never emits `SplitBrainDetected` —
  `demoteConfirmedRogues`
  ([`steady_state_master.go:594`](../../../internal/controller/steady_state_master.go)).
- **`verifyTopologyRestored` double-reports one fact.** `rogueCount > 0`
  ([:3433](../../../internal/controller/rolling_update.go)) and
  `len(masterIndices) > 1` (:1072) are the same predicate, so
  `TopologyRestoreIncomplete` at :3436 **always** brings `SplitBrainDetected`
  from :3445 with it — minimum 2, typically 3 Warnings per pass, every 10 s
  (`rollingUpdateRequeueDelay`), for up to `finalizationStallTimeout` = 2 min,
  i.e. ~36 Warning emissions for one incomplete restore.

**On a genuinely clean path the operator emits zero Warnings**, in both
topologies: `RollingUpdate` ×n → `FailoverTriggered` / `ManualFailover` →
`RollingUpdateComplete` → `SentinelUpdateComplete`, all Normal. The Warning-free
property rests entirely on the two best-effort calls at
[:2900-2910](../../../internal/controller/rolling_update.go) and
[`statefulset.go:450-478`](../../../internal/builder/statefulset.go) succeeding.
It is not asserted by any test.

### The event recorder does not damp this

`Recorder` is `k8s.io/client-go/tools/events.EventRecorder`
(`mgr.GetEventRecorder`, [`cmd/main.go:96`](../../../cmd/main.go)), the
`events.k8s.io/v1` API. Unlike the legacy `record` broadcaster it has **no spam
filter and no rate limiter** — no `EventCorrelator`, no token bucket. Only an
isomorphic series cache keyed on `(eventType, action, reason, reportingController,
reportingInstance, regarding, related)`; since `recordEvent` passes `action ==
reason` and `related == nil`, the effective key is `(type, reason, CR)`. Repeats
within `finishTime` = 6 min become a series count; **the note is not part of the
key, so only the first message survives** — `"2 pods report master role"` is
frozen even if the count later changes. After 6 min of silence the key is
evicted and the next occurrence creates a fresh Event object.

### Corrections to the original T4 text

- **"The detection logic itself must not change (ADR 0011)" is misattributed.**
  ADR 0011 governs `checkSteadyStateSplitBrain` only. Its two statements about
  `detectAndResolveSplitBrain` are D3 ("deliberately **not** reused, so the
  'most connected slaves' fallback stays unreachable from outside a rolling
  update") and a Consequence ("the two paths must not be merged"). Neither
  constrains its detection. `grep -n "must not change"` on that file returns
  nothing. The two checks are not even the same predicate: ADR 0011 D2 counts
  pods carrying the `instanceRole=master` **label**; the resolver counts
  `ps.isMaster`, derived from a live `GetReplicationInfo` probe with the label
  only as a fallback. **The ADRs that govern the rolling-update resolver are
  ADR 0007 D8 and ADR 0008 D10/D11**, both about which authority is passed in.
  The *intent* of the original sentence stands: resolution behaviour stays
  untouched.
- **No ADR names `SplitBrainDetected` or `SplitBrainResolved`.** Repo-wide the
  two strings occur only at their emission sites and in this ticket. The
  rolling-update event vocabulary has no owner (`RollingUpdate`,
  `FailoverTriggered`, `ManualFailover`, `SplitBrainDetected`,
  `SplitBrainResolved` are all ungoverned), so this is a **new ADR**, not an
  amendment.
- **The one "a Warning means something" promise in the ADR set** is ADR 0011
  Consequences ([`0011-…:286`](../../adr/0011-evidence-based-steady-state-split-brain-resolution.md)):
  "visible **only as Warning Events** (`SplitBrainUnresolved`,
  `SplitBrainDemotionRefused`, `MasterAdoptionRefused`) … **Monitoring must key
  on all three Events**; the CR status will not show the split brain."
  `SplitBrainDetected` is **not** in that contract, so retyping it breaks no
  documented promise. Retyping `SplitBrainResolved` reaches the steady-state
  path through the shared helper ADR 0011 D20 names
  (`demoteRogueMaster`, `recordEvent`) — that ADR is amended in the same change.

### Test coverage before the fix: none

**Superseded 2026-08-24 — the *Implementation* section below lists what now
covers this.** The state of the world when T4 was written, and the reason it
shipped:

`SplitBrainDetected` had **zero** assertions anywhere — unit, integration or
e2e. So did `TopologyRestoreIncomplete`. Mechanically guaranteed for the unit
tier: `newTestReconciler`
([`valkey_controller_test.go:79`](../../../internal/controller/valkey_controller_test.go))
never set `Recorder`, and `recordEvent` returns early on a nil recorder
([:1530-1532](../../../internal/controller/rolling_update.go)) — so a test that does
not opt in can neither observe an event **nor fail on a new one**. The event
helper is `fakeEventRecorder`
([`pdb_test.go:223-257`](../../../internal/controller/pdb_test.go), package-wide
despite the filename); e2e has `waitForValkeyEvent`
([`pdb_test.go:415`](../../../test/e2e/pdb_test.go)). No test anywhere asserted the
**absence** of Warning events during a rolling update — which is why this shipped.
Both halves are now closed: `newTestReconciler` installs a `fakeEventRecorder` by
default (sub-decision (e)), and each topology has a "Rolling update raised no
Warning" e2e subtest.

### Relevant code

**Line numbers below are as of 2026-08-23 and no longer resolve — the fix moved
the reporting out of `rolling_update.go`.** Kept as the record of where the
mechanism was found; the current locations are in the *Implementation* section
and in [ADR 0025](../../adr/0025-a-split-brain-warning-means-one-that-did-not-resolve-itself.md).

- [`internal/controller/rolling_update.go:1061-1140`](../../../internal/controller/rolling_update.go) —
  `detectAndResolveSplitBrain`; Warning at :1078 before the authority at :1085.
  **The Warning is gone; the function now reports nothing.**
- [`internal/controller/rolling_update.go:1247-1297`](../../../internal/controller/rolling_update.go) —
  `collectPodStates`; no `DeletionTimestamp` guard, label fallback at :1287.
- [`internal/controller/rolling_update.go:1192-1224`](../../../internal/controller/rolling_update.go) —
  `demoteRogueMaster`; refuses a not-Ready pod at :1198, Warning
  `SplitBrainResolved` at :1218.
- Three resolver call sites and their authorities:
  [:454-455](../../../internal/controller/rolling_update.go) (Sentinel,
  `getSentinelMasterPodName`), [:2627-2634](../../../internal/controller/rolling_update.go)
  (non-Sentinel state switch — the place ADR 0008 D11 declares single),
  [:3445](../../../internal/controller/rolling_update.go) (Phase 2 verify).
- [`internal/controller/rolling_update.go:2883-2910`](../../../internal/controller/rolling_update.go) —
  the promote-then-best-effort-demote that keeps the happy path Warning-free.
- [`internal/controller/rolling_update.go:3425-3452`](../../../internal/controller/rolling_update.go) —
  `verifyTopologyRestored`, the double-report.
- [`internal/controller/rolling_update.go:806-880`](../../../internal/controller/rolling_update.go) —
  `ensureWaitBound` / `waitBoundExceeded` / `forgetWaitBounds`, the bound
  machinery a persistence gate would reuse.
- [`internal/controller/valkey_controller.go:2480-2507`](../../../internal/controller/valkey_controller.go) —
  `writeStatusCondition`, which **re-`Get`s the CR into `v`** (:2484) and would
  drop unpersisted annotation edits if called mid-resolution.

### Options

**Option 1 — retype at the emission site: Normal when an authority names one of
the masters.** Inside `detectAndResolveSplitBrain`, move the report below the
authority resolution: if `knownMaster != ""` and it is among `masterIndices`,
emit Normal `MultipleMastersExpected`; otherwise keep the Warning. One function,
no new state, no new surface.
Rejected on a verified fact: in the Sentinel path the authority comes from a
live `SENTINEL MASTER` reply on **every** pass including `replacing-replicas`,
so a genuinely self-elected rogue during the replica phase would be downgraded
to Normal — and ADR 0011 D2 skips the steady-state checker **entirely with
Sentinel enabled**, so nothing else would ever raise it. It also leaves
`SplitBrainResolved` and the `verifyTopologyRestored` double-report untouched.

**Option 2 — gate on the rolling-update state.** Expected iff
`getRollingUpdateState(v)` is one of `failover-triggered`, `failover-reset`,
`replacing-master`, `manual-failover`, `restoring-topology`,
`verifying-topology`, **and** the authority is among the masters, **and** the
count is exactly 2. Everything else stays a Warning.
Tighter than Option 1: `replacing-replicas` and the empty state stay Warning,
and those are exactly the two unexplained shapes (a self-elected pod during the
replica phase; a promotion whose state write never landed). But it keys on the
switch ADR 0008 D11 declares "the single place that decision is made", so that
ADR is amended and every future rolling-update state has to answer a second
question there. And it fixes only the *reporting*: the Terminating ex-master
stays a false input to the resolver, where it also sets `masterIdx` to the
highest-ordinal master.

**Option 3 — fix the root cause: silence is not evidence.** In
`collectPodStates`, do not apply the label fallback to a pod carrying a
`DeletionTimestamp`: an unreachable pod that is being deleted is evidence of
nothing. An INFO-confirmed master keeps counting, terminating or not, so no
live master is ever dropped.
This is the ADR 0011 D6 rule ("Silence is not evidence") applied to the
rolling-update regime, and it removes the biggest single contributor to the
observed storm — as a correctness improvement, not cosmetics. ~~It pairs with T5
(the same `DeletionTimestamp` guard in the replace-candidate selection)~~
**superseded 2026-08-24: the T5 analysis showed the guard must not go into
candidate selection — removing the terminating pod from `sortReplicaCandidates`
makes `replaceNextReplica` delete a second replica while the first terminates.**
T5 bounds this option's one side effect a different way: a terminating pod that
loses `isMaster` and still `needsUpdate` becomes a `sortReplicaCandidates`
candidate ([:1421](../../../internal/controller/rolling_update.go#L1421)) and gets
deleted a second time — a verified API no-op, and exactly the duplicate T5 found
and now proposes to turn into a wait.
Alone it does **not** close T4: the genuine windows remain — Sentinel promoting
before it demotes, and a failed best-effort demotion where the old master keeps
answering `INFO` for up to 60 s under the drain preStop hook
([`statefulset.go:654-670`](../../../internal/builder/statefulset.go)).

**Option 4 — the level is a condition, the Warning is the edge that outlives the
bound.** New condition `MultipleMasters`, True while ≥2 pods report master, the
message naming the pods and the authority. `SplitBrainDetected` fires **only**
once the state has persisted longer than `splitBrainWarnAfter`; below the bound
the event channel stays silent and the condition plus the existing Normal
`FailoverTriggered` / `ManualFailover` events carry the story.
The bound is 90 s — above the 75 s `terminationGracePeriodSeconds` and the 60 s
drain preStop hook, so no legitimately-terminating master can reach it, and it
joins the existing 90 s family (`replicaReconnectTimeout`,
`sentinelAwarenessTimeout`) rather than inventing a new magic number.
This is the shape T1 and T3 already chose. It gives the Warning a meaning an
operator can act on — *a split brain that did not resolve itself* — and it
survives the Sentinel objection that sinks Option 1: a real rogue during
`replacing-replicas` still produces a Warning, 90 s later. It also closes a gap
ADR 0011 admits in its own Consequences ("the check writes no status and sets no
condition"): the condition auto-exports as a metric per ADR 0021, so "who has
two masters right now" becomes fleet-queryable for the first time.
Costs, named: a status write per transition; and `writeStatusCondition` re-`Get`s
the CR into `v`, so the condition must be written at the call sites after
resolution, never from inside the resolver, or unpersisted annotation edits are
lost. Arming can reuse `ensureWaitBound`, or stay memory-only in `r.nudges` —
memory-only is defensible here because failing to arm only delays an event and
cannot stall anything, but that is a deliberate carve-out from ADR 0010 and says
so in the new ADR.

**Option 5 — Option 3 + Option 4 + the event hygiene, as one change (favoured).**
- (a) `collectPodStates`: no label fallback for a pod with a
  `DeletionTimestamp` (Option 3).
- (b) The resolver reports a level and the Warning is the edge past 90 s
  (Option 4).
- (c) `SplitBrainResolved` becomes **Normal** — it reports a repair that
  succeeded. This reaches the steady-state path through the shared helper
  (ADR 0011 D20), which is amended in the same change; ADR 0011's monitoring
  contract lists three other reasons, so nothing documented breaks.
- (d) `verifyTopologyRestored` stops double-reporting: it already owns
  `TopologyRestoreIncomplete` for the same predicate, so the resolver call at
  :3445 reports nothing. One fact, one event.
- (e) Sub-decision: give `newTestReconciler` a `fakeEventRecorder` by default,
  so a newly added event can *fail* a test instead of being dropped on the
  floor.

Tests per ADR 0017. Unit: a terminating pod with a stale `master` label and a
failing `GetReplicationInfo` is not counted as master; an INFO-confirmed
terminating master still is; the condition goes True on the first double-master
pass; no Warning below the bound and exactly one past it — in every state, since
the bound and not the state is what the Warning means; the condition clears when
the second master goes away; the resolver still demotes identically in every case
(`TestDetectAndResolveSplitBrain_*` unchanged, which is the ADR 0007 D8 guard
test). E2E: a clean rolling update on **both**
topologies emits **zero** Warning events on the CR — fails-before-fix, and it
is the assertion whose absence let this ship.

Docs in the same change: new ADR ("a Warning named split-brain means one that
did not resolve itself"; the ten designed windows; the label-fallback rule;
Option 1 and Option 2 rejected with the Sentinel/ADR 0008 D11 reasons) + index
line; in-place amendment of ADR 0011 D20 for the `SplitBrainResolved` retype;
README events/conditions tables; CLAUDE.md pointer.

### Recommendation

**Option 5.** Options 1 and 2 treat the event as the bug; the verified mechanism
says two thirds of the observed storm come from a false input — a deleted pod
counted as master by its own stale label — and from a helper that reports
success as a Warning. Fixing only the type would leave the operator resolving
against a master that does not exist, and would leave `SplitBrainResolved`
firing.

Option 3 is the correctness half and must land regardless; it is also the half
that makes T5 cheaper rather than competing with it. Option 4 is the half that
gives the remaining, genuinely-designed double-master windows a home that is not
an alarm — and it is the only option that makes a split brain *queryable*, which
ADR 0011 explicitly says it is not today. Together they change what a Warning
named split-brain promises, from "the operator is doing a failover" to "nobody
resolved this in 90 seconds", without touching a single line of resolution
behaviour (ADR 0007 D8, ADR 0008 D10/D11 unchanged).

The 90 s of event silence is the price and it is bounded: the resolver still
demotes on every pass, `SplitBrainUnresolved` still reports a failed repair
immediately, and the condition is True from the first pass — only the Warning
waits.

### Decision

- 2026-08-23: analysis and options recorded above; the original text's ADR 0011
  citation corrected in place (see *Corrections*).
- 2026-08-23: **Option 5 accepted, including sub-decision (e).** The five parts
  land as one change:
  (a) `collectPodStates` does not apply the `instanceRole` label fallback to a
  pod carrying a `DeletionTimestamp` — an unreachable pod that is being deleted
  is evidence of nothing (ADR 0011 D6, applied to the rolling-update regime); an
  INFO-confirmed master still counts, terminating or not.
  (b) A `MultipleMasters` condition carries the level, True from the first pass
  that sees two masters, with the pods and the authority in the message;
  `SplitBrainDetected` fires only once the state outlives `splitBrainWarnAfter`
  = 90 s. The condition is written at the resolver's call sites, never from
  inside it, because `writeStatusCondition` re-`Get`s the CR into `v`.
  (c) `SplitBrainResolved` becomes Normal — it reports a repair that succeeded.
  (d) `verifyTopologyRestored` reports the incomplete restore once, through
  `TopologyRestoreIncomplete`; the resolver call at :3445 stays silent.
  (e) `newTestReconciler` gets a `fakeEventRecorder` by default, so a newly
  added event can fail a test instead of being dropped on a nil recorder.

  Option 1 rejected — the Sentinel path has a live authority on every pass
  including `replacing-replicas`, so a self-elected rogue would be downgraded to
  Normal with nothing else to catch it (ADR 0011 D2 skips the steady-state
  checker entirely with Sentinel enabled). Option 2 rejected — it amends
  ADR 0008 D11 and fixes only the reporting, leaving the terminating pod as a
  false input that also drags `masterIdx` to the highest-ordinal master.
  Options 3 and 4 alone rejected as halves of the same fix: 3 does not close the
  designed windows, 4 leaves the resolver resolving against a master that no
  longer exists.

  Accepted cost: up to 90 s in which a genuine split brain raises no Warning
  Event. Bounded — the resolver demotes on every pass regardless,
  `SplitBrainUnresolved` reports a failed repair immediately, and the condition
  is True from the first pass; only the Warning waits. The 90 s is chosen above
  the 75 s `terminationGracePeriodSeconds` and the 60 s drain preStop hook, and
  joins the existing 90 s family (`replicaReconnectTimeout`,
  `sentinelAwarenessTimeout`).

  Docs required in the same change: new ADR (a Warning named split-brain means
  one that did not resolve itself; the ten designed windows; the label-fallback
  rule; the ADR 0010 carve-out if the bound is armed in memory only) plus an
  index line in [`docs/adr/README.md`](../../adr/README.md); in-place amendment of
  ADR 0011 D20 for the `SplitBrainResolved` retype; README events/conditions
  tables; CLAUDE.md pointer. ~~T5 lands the matching `DeletionTimestamp` guard in
  the replace-candidate selection~~ — **corrected 2026-08-24**: T5 lands the
  guard at the availability-spending sites, never in the candidate selection.
  See the cross-ref in T5.
- 2026-08-24: **out of scope for T4, raised as T12.** The discussion of what two
  masters actually cost surfaced that no master this operator builds refuses
  writes without a replica (`min-replicas-to-write` is set nowhere and has no
  CRD escape hatch), which is what lets both sides of a split accumulate writes
  that the repair then discards. T4 does not depend on it and does not wait for
  it — T4 changes what a Warning promises, T12 would change what a divergence
  costs.
- 2026-08-24: **implemented.** See *Implementation* below.

### Implementation (2026-08-24)

All five parts of Option 5 landed as one change. New ADR:
[0025](../../adr/0025-a-split-brain-warning-means-one-that-did-not-resolve-itself.md), indexed in
[`docs/adr/README.md`](../../adr/README.md); ADR 0011 amended in place (Status note, D20, and the
"visible only as Warning Events" Consequence narrowed to that check).

**(a) The label fallback no longer believes a pod that is being deleted.**
`collectPodStates` routes the fallback through the new `labelClaimsMaster`
([`internal/controller/rolling_update.go`](../../../internal/controller/rolling_update.go)), which
refuses a pod carrying a `DeletionTimestamp`. An INFO-confirmed master still counts,
terminating or not.

**(b) `MultipleMasters` carries the level, the Warning is the edge.** New
[`internal/controller/split_brain_report.go`](../../../internal/controller/split_brain_report.go):
`resolveSplitBrain` wraps the resolver and reports; `reportMultipleMasters` writes the
condition and emits `SplitBrainDetected` once, on the transition from reason
`MultipleMastersTransitional` to `MultipleMastersPersisted`.

Two deviations from the option text, both narrowing rather than widening it — neither changes
what was decided:

1. **The bound is armed in the condition, not in an annotation and not in memory only.** The
   option left the choice open ("`ensureWaitBound`, or stay memory-only … a deliberate
   carve-out from ADR 0010"). Neither was taken: the durable copy is the
   `LastTransitionTime` of `MultipleMasters` — a write this path performs anyway — with the
   `nudgeTracker` (`boundMultipleMasters`) as the in-memory fallback for a failing status
   write. That is the ADR 0010 D7/D8 two-copy discipline with **no carve-out and no fourth
   annotation on the CR**, and it survives an operator restart, which the memory-only variant
   would not have.
2. **The resolver was made pure instead of being given a level parameter.**
   `detectAndResolveSplitBrain` now emits nothing at all; the report lives in the wrapper.
   This is what let every `TestDetectAndResolveSplitBrain_*` stand unchanged, which is the
   ADR 0007 D8 / ADR 0008 D10-D11 guard the option asked for.

**(c) `SplitBrainResolved` is Normal.** One line in `demoteRogueMaster`; the steady-state
caller inherits it through the shared helper (ADR 0011 D20, amended).

**(d) `verifyTopologyRestored` reports once.** It calls the bare resolver, so
`TopologyRestoreIncomplete` is the only Event for that predicate.

**(e) Every test reconciler records Events.** `newTestReconciler` installs a
`fakeEventRecorder`; the recorder is now mutex-guarded (`findMaster` probes concurrently) and
gained `all()` and `withType()`.

**Verified, and how.**

- `make lint` → `0 issues`; `make cyclo` → all functions below 15; `make generate-all` leaves
  the tree clean (conditions are not part of the CRD schema).
- `make test-unit` green; `go test ./internal/controller/ -race` green (29.5 s).
- `make test-integration` green (23.8 s).
- **Fails-before-fix, measured** by reverting each part in a scratch copy and re-running:
  `TestCollectPodStates_TerminatingPodWithAStaleMasterLabelIsNoMaster`,
  `TestDemoteRogueMaster_ReportsASucceededRepairAsNormal`,
  `TestVerifyTopologyRestored_ReportsTheIncompleteRestoreOnce` and
  `TestHandleMultiReplicaRollingUpdate_DoubleMasterPassSetsTheConditionSilently` all fail with
  the old behaviour restored and pass with it removed.
- New unit tier:
  [`internal/controller/split_brain_report_test.go`](../../../internal/controller/split_brain_report_test.go)
  — terminating pod with a stale label is no master; an INFO-confirmed terminating master
  still is; the live-pod label fallback still works; condition True and silent on the first
  pass; exactly one Warning past the bound and none on the pass after; the bound survives an
  operator restart (fresh reconciler, empty tracker, condition aged past 90 s); the in-memory
  bound answers when every status write is rejected; the clear drops both the condition and
  the tracker entry; the clear is presence-guarded (no new condition on an untouched cluster);
  `splitBrainWarnAfter` is above 75 s and below `finalizationStallTimeout`.
- E2E: `requireNoWarningEvents` ([`test/e2e/pdb_test.go`](../../../test/e2e/pdb_test.go), next to
  `waitForValkeyEvent`) plus a "Rolling update raised no Warning" subtest in
  `TestE2E_RollingUpdate_MultiReplicaNoSentinel` and `TestE2E_RollingUpdate_HA`
  ([`test/e2e/rolling_update_test.go`](../../../test/e2e/rolling_update_test.go)). **Not run
  here** — no cluster in this session; it needs `make e2e-local`.

**Deliberately left out, with the reason.**

- **Nothing clears `MultipleMasters` outside a rolling update.** `resolveSplitBrain` only runs
  while one is in flight, so a rolling update abandoned with rogue masters still present
  leaves the condition standing until the next one — an administrator who repairs the split
  brain by hand does not clear it. Writing `False` because nobody measured would be the worse
  answer, and the Warning, `TopologyRestoreIncomplete` and `TopologyRestored=False` all
  survive independently. Closing it properly needs a steady-state master count for **Sentinel**
  clusters, which is the gap ADR 0011 D2 already names — T7 territory, not T4. Recorded in
  ADR 0025 *Residual risks*.
- **T5 is not in this change.** The matching `DeletionTimestamp` guard in the
  replace-candidate selection stays T5, as the T4 decision said. Part (a) makes the duplicate
  delete T5 fixes *reachable* on one more path (a terminating pod that loses `isMaster` and
  still `needsUpdate` becomes a `sortReplicaCandidates` candidate) — it is an API no-op, and
  it is exactly the shape T5 already describes.

## T5: A pod being deleted still counts as Ready — the duplicate deletes are the visible tip

**Severity: re-assessed 2026-08-24. Low for the reported symptom (duplicate
deletes are a verified API no-op), medium for two further paths the same blind
spot opens: the Sentinel quorum guard and the manual-failover promotion
candidate — both confirmed by test, see *Verification run*.
Status: **DONE 2026-08-25** — implemented as E1-E9, with two deviations from the
decided text recorded under *Implementation*. The rule now lives in
[ADR 0026](../../adr/0026-a-pod-being-deleted-is-not-available.md); this item keeps
the analysis that produced it.
The intermediate "R1 + R2" formulation is superseded and marked as such where it
stands. What changed on 2026-08-25 (see *Second re-review*): R1 was an
enumeration that had been incomplete three times and one of its members was a
verified regression on the Sentinel path, so the spend rule became a renamed
default (`available()`/`reachable()`); R2 survived but moved from function heads
to the individual deletes; (i-a) survived as "the delete is never resumed" but
lost "the stall is visible", which was false. The headline 60 s measurement was
also wrong and is corrected in place.**

### What was verified on the cluster (2026-08-22)

Nearly every CR logged "Deleting replica pod X" twice in the same or adjacent
second under two different reconcileIDs; sentinel pods up to 3x (e.g.
oauth2-valkey-sentinel-0 at 21:34:03 by three passes). reconcileID interleaving
proves these are strictly sequential passes, not concurrency — no line of pass
A appears after any line of pass B in any pair examined; ADR 0019 workqueue
serialization holds. Pass A's delete triggers a watch event that requeues
instantly; pass B sees the pod still present (Terminating) and still on the old
template, and deletes again.

### What the mechanism actually is (verified in code 2026-08-24)

**`isPodReady` reads the `PodReady` condition and nothing else**
([`rolling_update.go:417`](../../../internal/controller/rolling_update.go#L417)),
and no caller in the rolling update pairs it with `DeletionTimestamp`.

**Measured 2026-08-24, kind `valkey-operator-test`, Kubernetes 1.36.1.** Two
pods built in the shape of the operator's own workloads, deleted, then polled
once a second for `deletionTimestamp` and the `PodReady` condition:

| Shape | grace | Result |
|---|---|---|
| `valkey:9.1.1`, exec readiness probe (`valkey-cli ping`), `preStop: sleep 60` — an unconditional 60 s hold, i.e. the **worst case** of the ADR 0012 drain, not its normal shape (corrected 2026-08-25, see below) | 75 s | `deletionTimestamp` set at 0 s; **`Ready=True` and `ContainersReady=True` for all 61 s** until the pod object disappeared. No flip. |
| `valkey:9.1.1`, exec readiness probe, `trap '' TERM` — a pod slow or wedged on shutdown | 30 s | `deletionTimestamp` set at 0 s; **`Ready=True` for all 30 s** until the pod object disappeared. No flip. |

**kubelet does not flip `PodReady` for a terminating pod.** As long as the
readiness probe keeps passing, the pod is Ready right up to the moment it is
gone — which is why the Kubernetes endpoints controller carries its own
`DeletionTimestamp` check instead of relying on readiness. The window is
therefore **the whole termination**, however long that is.

**How long it actually is — corrected 2026-08-25, verified in the builder and the
sidecar.** ~~multi-replica non-Sentinel data pods carry the 60 s drain `preStop`
(ADR 0012) → a 60 s window on every rolling-update pod replacement~~ — **wrong,
and it was the synthetic probe that said so.** The probe used
`preStop: sleep 60`, an unconditional sleep. The operator's hook is a wait loop
with a 60 s *cap*
([`statefulset.go:660-668`](../../../internal/builder/statefulset.go#L660-L668)):

```sh
i=0; while [ "$i" -lt 60 ]; do [ -f /var/run/vko/drain-complete ] && exit 0; sleep 1; i=$((i+1)); done
```

and the marker is released by a `defer` on every exit path of `Handle`, which
returns immediately for a non-master
([`drain.go:103-116`](../../../internal/sidecar/drain.go#L103-L116)). So:

* **a replica delete releases the hook in ~1 s**, on every topology, drain hook
  or not — which is exactly the 2-to-3-duplicate signature the 2026-08-22 log
  shows;
* **60 s is a cap that only a master can approach**, and only while its drain
  failover is still running;
* **up to the full grace period** (75 s data, 30 s sentinel) for a pod that is
  slow or wedged on shutdown, on any topology.

Two consequences for the rest of T5, applied in place below: the severity
argument loses its "60 s on every replacement" premise, and **R2 gets cheaper**
— holding the next delete costs ~1 s in a clean roll, not a minute.

Reproduce: see *Reproducing the measurement* at the end of T5. Note the probe
reproduces the *cap*, not the operator's hook — to see the real timing, delete a
replica of a multi-replica non-Sentinel cluster and watch the pod disappear
within about a second.

So `collectPodStates` stamps `ps.ready = true`
([`:1309`](../../../internal/controller/rolling_update.go#L1309)) on a pod the
operator itself just deleted, and every downstream `!ps.ready` guard — the ones
that exist precisely to stop the update spending the cluster's redundancy —
sees a healthy pod.

**The repeat delete really is free.** `deleteOwnedPod` sends `Delete` with a UID
precondition and no `GracePeriodSeconds`
([`foreign_object.go:305`](../../../internal/controller/foreign_object.go#L305)).
`rest.BeforeDelete` (verified against `k8s.io/apiserver@v0.35.0`,
`pkg/registry/rest/delete.go:108-151`) takes the `DeletionTimestamp != nil`
branch, and with `options.GracePeriodSeconds == nil` returns
`graceful=false, gracefulPending=true` — "graceful deletion is pending, do
nothing". No shortened grace period, no generation bump, no second SIGTERM. The
cost is one API round trip, one audit entry, and one log line per pass.

### Corrections to the original T5 text

- **The proposed work is the wrong direction.** "Skip pods with non-nil
  `DeletionTimestamp` in the replace-candidate selection" removes the
  terminating pod from `sortReplicaCandidates`
  ([`:1421`](../../../internal/controller/rolling_update.go#L1421)), so
  `candidates[0]` becomes the *next* replica and `replaceNextReplica` deletes a
  second pod while the first is still terminating.
  `verifyReplacedReplicasSynced` does not catch it either: it skips every pod
  with `ps.needsUpdate` ([`:1457`](../../../internal/controller/rolling_update.go#L1457)),
  which the terminating pod still has. The youngest-first sort is what keeps the
  terminating pod at position 0 today and is the only reason the duplicate is
  harmless; skipping it converts a benign no-op into two replicas down at once —
  exactly the invariant ADR 0007 D1 exists for. **The terminating pod must stay
  the candidate and be waited on, not skipped.**
- **"which also deduplicates the RollingUpdate events" is wrong.** The Event
  series cache keys on `(eventType, action, reason, reportingController,
  reportingInstance, regarding, related)` and **not on the note**
  (`k8s.io/client-go@v0.36.4`, `tools/events/event_broadcaster.go:58-66,297-310`;
  the same finding T4 recorded). `recordEvent` passes `action == reason`, so
  every `Normal/RollingUpdate` event on one CR collapses into a single Event
  object whose note is frozen at the first pod named, with `Series.Count`
  counting up. Deduplicating the deletes only lowers that count. The visible
  defect — one Event that names pod 2 and never mentions pods 1 and 0 — is
  caused by `action == reason`, and is not T5.
- **Severity "benign, idempotent" is right about the delete and wrong about the
  blind spot.** See below.

### The three consequences the blind spot has beyond the duplicate

**(a) A terminating pod can be promoted to master.** `findPromotionCandidate`
([`:2894`](../../../internal/controller/rolling_update.go#L2894)) accepts any pod
with `!needsUpdate && ps.ready && ps.exists`, and `waitForReplicasReady`
([`:1669`](../../../internal/controller/rolling_update.go#L1669)) lets it through
for the same reason. On the non-Sentinel manual-failover path the promoted pod
then takes `REPLICAOF NO ONE`, is recorded as `known-master`
(`persistManualFailoverState`, ADR 0008/ADR 0009), the replica ConfigMap is
republished at it, and **the outgoing master is deleted seconds later**
([`:2818`](../../../internal/controller/rolling_update.go#L2818)).
`verifyPromotionCandidateHoldsData` passes — the dying pod does hold the data
right up to the moment it stops. Without persistence the dataset is then gone.
The Sentinel path has the mirror-image hole in `verifyNewMasterReady`
([`:2161`](../../../internal/controller/rolling_update.go#L2161)), which is the
gate before the former master is deleted. This is the same class ADR 0007 D10
and ADR 0009 exist for, reached through a different input.
*Reachability:* needs a pod deletion (chaos, eviction, node drain, kubectl) to
land inside the window between the candidate scan and the delete. On
wds18-k8s-main the Chaos Mesh `valkey-chaos` schedule kills a vko pod every
5 min, so the window is sampled continuously rather than never.

**(b) The Sentinel quorum guard counts a pod that is already dying.**
`checkAndHandleSentinelRollingUpdate` builds `readyCount` from `isPodReady`
alone ([`:3614`](../../../internal/controller/rolling_update.go#L3614)) and gates
the next delete on `readyCount-1 < quorum`. When the terminating sentinel is
the *outdated* one, `firstOutdatedPod` re-picks it and the delete is the same
harmless no-op. When it is **already up to date** — chaos-killed, evicted,
drained — it inflates `readyCount` while `firstOutdatedPod` points at a
different, live pod. With 3 sentinels and quorum 2: `readyCount=3`, `3-1=2 >= 2`
→ the operator deletes a second sentinel, leaving one live Sentinel of three
and **no quorum for a failover** for the union of both termination windows.
ADR 0004 derives the Sentinel PDB from exactly this quorum, and ADR 0022
measured what a Sentinel tier that cannot reach a majority costs: no promotion
at all.

**(c) A terminating pod can complete the rolling update.** `countUpdatedPods`
([`:1335`](../../../internal/controller/rolling_update.go#L1335)) counts
`!needsUpdate && ready`, so an already-replaced pod that is being deleted for an
unrelated reason still counts. `updatedCount == totalPods` routes straight to
`finalizeRollingUpdate` / `finalizeMultiReplicaRollingUpdate`: the
`RollingUpdateComplete` Event fires and `clearRollingUpdateState` runs. ADR 0010
names the cost of clearing that annotation — once it is gone nothing calls
`detectAndResolveSplitBrain` again. Self-correcting on the next pass (the pod
returns on the current template, so no new update is triggered), but the
completion marker ADR 0024 made load-bearing has then fired over a pod that was
not running.

### The constraint that kills the one-line fix

The obvious minimal change is `ps.ready = isPodReady(pod) && pod.DeletionTimestamp == nil`
at [`:1309`](../../../internal/controller/rolling_update.go#L1309). It must not be
made, but **not for the reason first written here.**

*Corrected 2026-08-24, verified in code.* The first version of this section
claimed a blanket `ready` change would make `demoteRogueMaster` refuse the
terminating outgoing master and turn every controlled failover into a
`SplitBrainUnresolved` **Warning**. That is wrong.
`detectAndResolveSplitBrain` logs a failed demotion and clears `isMaster`
anyway; it emits nothing
([`:1129-1138`](../../../internal/controller/rolling_update.go#L1129)).
`SplitBrainUnresolved` is raised only by `reportDemotionOutcome` on the
**steady-state** path
([`steady_state_master.go`](../../../internal/controller/steady_state_master.go)),
which ADR 0011 D2 skips entirely with Sentinel enabled. The rolling-update
report is `reportMultipleMasters`
([`split_brain_report.go:87`](../../../internal/controller/split_brain_report.go#L87)),
which keys on the double-master state outliving `splitBrainWarnAfter` = 90 s and
never on whether a demotion succeeded — and `observed` is captured *before*
resolution, so the report is byte-identical either way.

**The real reason the carve-out is needed is a data one, and the measurement
makes it concrete.** `demoteRogueMaster` refuses a pod with `!ready`
([`:1203`](../../../internal/controller/rolling_update.go#L1203)). The terminating
outgoing master still answers `INFO` as master and is therefore still
`ps.isMaster` (deliberately — ADR 0025: "the guard drops the heuristic, never
the answer"). Today the `REPLICAOF` lands and it becomes a replica within the
pass. Under a blanket `ready` change the demotion is refused and **the outgoing
master keeps accepting writes as a master for the rest of its termination — up
to the 60 s cap of the drain hook, and for as long as its drain failover runs.**
(Corrected 2026-08-25: the cap is the right number here, because this is the one
pod that can approach it; ~~"now measured"~~ was not — the measurement was a
synthetic `sleep 60`, see the mechanism section.) T12 is the other half of that
sentence: no master this operator builds refuses writes without a replica, so
both sides accumulate writes the repair then discards. Trading a REPLICAOF that
works today for a divergence window of up to 60 s is the wrong direction.

The 90 s bound survives either way — 60 s is under it — so ADR 0025's promise is
not what is at stake. **But that promise is a hard constraint on everything else
the fix does**, and it is broader than expected: `requireNoWarningEvents`
([`test/e2e/pdb_test.go:447`](../../../test/e2e/pdb_test.go#L447)) fails on **any**
Warning of **any** reason regarding the CR, and is asserted by two subtests —
non-Sentinel ([`rolling_update_test.go:222`](../../../test/e2e/rolling_update_test.go#L222))
and Sentinel ([`:485`](../../../test/e2e/rolling_update_test.go#L485)). **The fix
may report its new waits through logs, the phase string and conditions only —
never through an Event.**

`ready` therefore carries two questions that a terminating pod answers
differently:

| Question | A terminating pod | Call sites |
|---|---|---|
| **Can I talk to it?** | yes, for now | `demoteRogueMaster`, the `GetReplicationInfo` probes |
| **May I spend it?** (delete it, promote it, count it toward quorum or completion) | no | the delete candidates, `findPromotionCandidate`, `verifyNewMasterReady`, the Sentinel `readyCount`, `countUpdatedPods` |

The fix belongs to the second question only.

### Relevant code

The per-site table the first two decisions carried is **superseded** (2026-08-25):
the spend rule is no longer an enumeration, so a list of "which sites change" is
the wrong artefact. What replaces it is the full read set with the answer each
site gets — see *Second re-review* and the Decision block.

**Every `podState.ready` reader (13), and what it asks.** Counted in the clean
tree 2026-08-25.

| Site | Function | Answer |
|---|---|---|
| [`:1338`](../../../internal/controller/rolling_update.go#L1338) | `countUpdatedPods` | `reachable()` — **corrected 2026-08-25**: this row said `available()` and contradicted E2 in the Decision block, which is the current rule (S1 is the regression it avoids) |
| [`:1390`](../../../internal/controller/rolling_update.go#L1390) | `replaceNextReplica`, `candidates[0]` | `available()` |
| [`:1463`](../../../internal/controller/rolling_update.go#L1463) | `verifyReplacedReplicasSynced` | `available()` |
| [`:1669`](../../../internal/controller/rolling_update.go#L1669) | `waitForReplicasReady` | `available()` |
| [`:1868`](../../../internal/controller/rolling_update.go#L1868) | `replaceRemainingPods` | `available()` |
| [`:1930`](../../../internal/controller/rolling_update.go#L1930) | `handlePostFailover`, new-master selection | `available()` — **the site both earlier site lists missed** |
| [`:2161`](../../../internal/controller/rolling_update.go#L2161) | `verifyNewMasterReady` | `available()` |
| [`:2741`](../../../internal/controller/rolling_update.go#L2741) | `deleteNextPendingPod` | `available()` |
| [`:2894`](../../../internal/controller/rolling_update.go#L2894) | `findPromotionCandidate` | `available()` |
| [`:1203`](../../../internal/controller/rolling_update.go#L1203) | `demoteRogueMaster` | `reachable()` — the ADR 0025 carve-out |
| [`:1800`](../../../internal/controller/rolling_update.go#L1800) | `waitForWriteSync`, `numReplicas` | `reachable()` — excluding *relaxes* the gate toward `numReplicas == 0`, which skips WAIT entirely |
| [`:2037`](../../../internal/controller/rolling_update.go#L2037) | `forceReplicaConnections` | `reachable()` — a best-effort REPLICAOF at a dying pod costs nothing |
| [`:2949`](../../../internal/controller/rolling_update.go#L2949) | `promoteAndRedirect`, redirect targets | `reachable()` — same |

**Raw `isPodReady` call sites.**

| Site | Function | Answer |
|---|---|---|
| [`:2566`](../../../internal/controller/rolling_update.go#L2566), [`:2581`](../../../internal/controller/rolling_update.go#L2581) | `handleStandaloneRollingUpdate` | available, inline `&& pod.DeletionTimestamp == nil` |
| [`:3614`](../../../internal/controller/rolling_update.go#L3614) | sentinel scan — **one predicate feeding two counters** | available; both counters inherit it (E6) |
| [`:3029`](../../../internal/controller/rolling_update.go#L3029) | `handlePostManualFailover` | unchanged — an explicit `DeletionTimestamp` wait already sits above it at [`:3003`](../../../internal/controller/rolling_update.go#L3003) |
| [`valkey_controller.go:1673`](../../../internal/controller/valkey_controller.go#L1673) | `sentinelRolloutComplete` | unchanged — already revision-gated, and it only defers a Secret delete |

**All six pod deletes and their gate status under R2.**

| Site | Function | Gate |
|---|---|---|
| [`:1412`](../../../internal/controller/rolling_update.go#L1412) | `replaceNextReplica` | gated, *after* `verifyReplacedReplicasSynced` and the `len(candidates) == 0` return |
| [`:1888`](../../../internal/controller/rolling_update.go#L1888) | `replaceRemainingPods` | gated, *inside* the loop — not at the function head |
| [`:2575`](../../../internal/controller/rolling_update.go#L2575) | `handleStandaloneRollingUpdate` | covered by the loop's own available checks |
| [`:2745`](../../../internal/controller/rolling_update.go#L2745) | `deleteNextPendingPod` | gated (transitively unreachable today — Finding 3) |
| [`:2818`](../../../internal/controller/rolling_update.go#L2818) | `handleManualFailover`, old-master delete | **exempt when the pod being deleted is itself `isMaster`** (E4) |
| [`:3644`](../../../internal/controller/rolling_update.go#L3644) | sentinel delete | gated, after the quorum guard |

**Unchanged, and named so nobody re-derives it.** `isPodReady` itself
([`:417`](../../../internal/controller/rolling_update.go#L417)); `sortReplicaCandidates`
([`:1421`](../../../internal/controller/rolling_update.go#L1421)) — the terminating pod
must stay `candidates[0]`; `countReplacedPods`
([`:1348`](../../../internal/controller/rolling_update.go#L1348)) — readiness-blind and
therefore termination-blind already.


### Options

**Option 1 — Dedupe only (literal T5, corrected).** At each of the five delete
sites, if the pod already carries a `DeletionTimestamp`, requeue with a "waiting
for X to terminate" log line instead of issuing the delete. `sortReplicaCandidates`
untouched. Nothing else changes.
*Fixes:* the reported symptom. *Leaves:* (a), (b) and (c) exactly as they are.
*Cost:* ~5 guarded branches, a handful of unit tests. No ADR change beyond a
sentence in ADR 0007.

**Option 2 — Sentinel tier only.** Exclude terminating pods from `readyCount`
and from `firstOutdatedPod` in `checkAndHandleSentinelRollingUpdate`. The
existing quorum guard then blocks by itself; no new wait is written.
*Fixes:* (b), and the sentinel half of the reported symptom. *Leaves:* (a), (c)
and the data-tier duplicates. *Cost:* one loop. *New behaviour:* a sentinel
stuck `Terminating` stalls the sentinel roll — visibly, via
`SentinelUpdatePending=True` with its progress message (ADR 0024) — where today
it rolls on.

**Option 3 — `available()` at the availability-spending sites (the two-question
split).** Add `terminating bool` to `podState`, set it in `collectPodStates`,
and introduce `func (ps podState) available() bool { return ps.ready && !ps.terminating }`.
Apply it at every availability-spending site (the per-site table this referred to
was replaced on 2026-08-25 — the current answers are in *Relevant code*); leave
`ready` and its two "can I talk to it" call sites alone. The Sentinel loop gets
the same predicate inline.
*Fixes:* the reported symptom, (a), (b) and (c). *Cost:* ~12 call sites, one
field, one helper; unit tests per site plus one e2e that deletes a pod during a
roll. *New behaviour:* a pod stuck `Terminating` now blocks where it did not —
see sub-decision (i).

**Option 4 — Option 3 plus a named bound.** As Option 3, but every new wait is
armed and bounded rather than requeueing forever, per ADR 0010 D-level ("a bound
that can silently fail to arm is not a bound"). Two shapes for the bound, see
sub-decision (i).

**Option 5 — Do nothing; re-file.** Record the verified no-op, drop the delete
log line to `V(1)`, close T5 as accepted noise, and raise (a) and (b) as their
own items with their own severities.
*Fixes:* nothing. *Value:* it separates a cosmetic finding from two safety
findings instead of letting the cosmetic one carry them.

#### Sub-decision (i): what bounds the new waits

Option 3 routes terminating pods into `replaceNextReplica`'s existing
`!ps.ready` requeue, which is **unbounded today** and already named as such in
ADR 0010 Residual risks (a pod-0 "stuck `Terminating` … keeps
`needsRollingUpdate` true, so the next pass re-enters at `replaceNextReplica`
and requeues with no bound"). Option 3 does not create that class, it routes
more cases into it. Three ways out:

- **(i-a) Accept it** and extend the existing ADR 0010 residual-risk item to say
  so explicitly. Cheapest; the stall is visible in the phase string.
- **(i-b) Reuse the sync-wait bound** (`ensureSyncWaitTimestamp` /
  `isSyncWaitTimedOut` → `pauseRollingUpdate`). No new field, no new constant,
  consistent exit (`RollingUpdatePaused`). Semantically it is not a sync wait,
  and it shares one timestamp with the real sync waits — a termination wait
  would consume budget the next sync wait needs.
- **(i-c) A named `terminationWaitTimeout`** with its own annotation and its own
  pause reason. Honest, and it is what ADR 0010 asks for; costs a constant, an
  annotation, arm/clear plumbing and its two "arming write failed" tests.

#### Sub-decision (ii): where the decision is recorded

- **(ii-a) Amend ADR 0007 D9.** D9 already defines what readiness means and
  where it may be used ("readiness can never be used as a proxy for replication
  health anywhere in the operator"). The new rule is the second half of the same
  sentence. Cheapest, and it puts the rule where a reader of the rolling update
  will find it — but the rule also binds the Sentinel tier, which ADR 0007 does
  not cover.
- **(ii-b) New ADR 0026** — "A pod being deleted is not available." A durable
  invariant that binds both tiers and any future code that reads `ready`, with
  the ADR 0025 carve-out (`demoteRogueMaster`) stated as part of the decision
  rather than as a comment. Per CLAUDE.md, a new durable invariant gets its own
  ADR. Plus an index line and a cross-reference from ADR 0007 D9, ADR 0004 and
  ADR 0022.

### Recommendation

> **Superseded 2026-08-25.** This section records why Option 3 beat Options 1, 2,
> 4 and 5 — that comparison still holds and is why the fix is one predicate rather
> than three tickets. What it says about the *shape* of Option 3 (a site list) and
> about (i-a) ("already visible", "no bound needed") is superseded by E1 and E5 in
> the Decision block. Read it as the option comparison, not as the current rule.

**Option 3 + (i-a) + (ii-b).**

Option 1 fixes the log line and leaves the two paths where the same blind spot
actually costs something. Option 5 is honest bookkeeping but the three
consequences share one predicate and one commit — splitting them into three
tickets pays the analysis cost three times for a fix that is one field and one
helper. Option 2 is the highest safety-per-line of any of them and should land
regardless; it is a strict subset of Option 3, not a competitor.

Option 3 over Option 4, with one claim corrected. *The first version of this
paragraph said the promotion path "is already bounded". It is not.*
`findPromotionCandidate` returning -1 lands in a plain unbounded requeue
([`:2772`](../../../internal/controller/rolling_update.go#L2772)), and a terminating
replica actually stops one layer earlier, in `waitForReplicasReady`'s
`!ps.ready || ps.needsUpdate` branch
([`:1669`](../../../internal/controller/rolling_update.go#L1669)) — also a plain
unbounded requeue, not the bounded `waitOrPauseForReplicaSync` beneath it.

So (i-a) is a deliberate acceptance, not an appeal to an existing bound: Option 3
**widens** the unbounded-requeue class ADR 0010 already names in its Residual
risks, and the item there has to say so. It is still the right direction. A pod
that never finishes terminating means the cluster has a real problem, and the
answer to that is not a timeout that resumes — the operator must not delete a
*second* pod because the first is wedged. Stopping half-done keeps a serviceable
cluster (ADR 0007), and the stall is visible in the phase string and, on the
Sentinel tier, in `SentinelUpdatePending` (ADR 0024). Adding a fourth timeout
family to a state machine that has five buys a resume for a case that should not
resume. If the silent-stall half turns out to matter in the field, (i-c) costs
the same then as now.

(ii-b) over (ii-a) because the rule binds the Sentinel tier, the data tier and
the steady-state carve-out; ADR 0007 owns none of the last two. The ADR is also
the right place to state the carve-out as a decision — "the guard applies to
spending a pod, never to talking to it" — so the next person who reaches for
`ps.ready` finds the two questions separated instead of rediscovering the
ADR 0025 regression the hard way.

Verification the change owes: unit tests per guarded site (a terminating pod is
not a promotion candidate, is not counted by the Sentinel `readyCount`, is not
counted by `countUpdatedPods`, is waited on rather than re-deleted), a
regression test that a terminating pod answering `INFO` as master is still
demoted quietly (the ADR 0025 carve-out), and one e2e that deletes a pod
mid-roll on both topologies and asserts no second pod is deleted while the first
terminates.

### Verification run before the decision (2026-08-24)

Six characterization tests were written against the tree, run green, and then
removed — they assert what the operator does **today**, so they cannot stay next
to the fix that inverts them. Source kept at
`t5-characterization-tests.go.txt` in the session scratchpad; they are the shape
of the regression tests the fix owes.

| Claim | Test | Result |
|---|---|---|
| `collectPodStates` stamps `ready=true` on a pod carrying a `DeletionTimestamp` | `TestT5_Char_CollectPodStates_TerminatingPodIsReady` | **confirmed** |
| (a) `findPromotionCandidate` accepts a terminating pod | `TestT5_Char_FindPromotionCandidate_AcceptsTerminatingPod` | **confirmed** |
| (c) `countUpdatedPods` counts a terminating pod toward completion | `TestT5_Char_CountUpdatedPods_CountsTerminatingPod` | **confirmed** |
| The terminating pod stays `candidates[0]`, and skipping it hands position 0 to a second live replica | `TestT5_Char_SortReplicaCandidates_TerminatingPodStaysFirst` | **confirmed — the original proposed work is a regression** |
| (b) with an up-to-date sentinel terminating-but-Ready, the quorum guard authorises deleting a second sentinel | `TestT5_Char_SentinelQuorumGuard_CountsTerminatingPod` | **confirmed — `p0` was deleted, leaving 1 live Sentinel of 3** |
| Control: same topology, terminating sentinel **not** Ready → `readyCount-1 = 1 < quorum 2`, `p0` survives | `TestT5_Char_SentinelQuorumGuard_ControlNotReady` | **confirmed — the stale `Ready` is what authorises the delete, nothing else in the setup** |

Two claims of the first analysis were **falsified** and are corrected in place
above: the `SplitBrainUnresolved` regression mechanism (it does not exist; the
real cost is a divergence window of up to 60 s) and "the promotion branch is already
bounded" (it is not; Option 3 widens an unbounded-requeue class ADR 0010 already
names). One claim was **strengthened by measurement**: the terminating-Ready
window is the whole termination, not a second or two.

### Reproducing the measurement

Against any cluster, in a throwaway namespace: run a pod with an exec readiness
probe and either `preStop: ["sh","-c","sleep 60"]` or a command that traps
SIGTERM, `kubectl delete pod --wait=false`, then poll
`kubectl get pod -o json` once a second and print
`.metadata.deletionTimestamp` next to the `Ready` condition. `Ready` stays
`True` for the entire grace period in both shapes. Scripts kept in the session
scratchpad (`t5-probe.yaml`, `t5-probe2.yaml`, `measure.sh`).

### Re-review of the decided site list (2026-08-24, after the decision)

A second adversarial pass over the decided site list, against the code. All
four findings are verified by reading the functions named; the two two-down
scenarios are traced through the code, not executed. Consequence: the
per-site list of the first decision is superseded and was rewritten as R1 + R2 —
which the *Second re-review* below then superseded in turn. All four findings
still stand; what changed is which rule closes them (E1 and E3).

**Finding 1 — `verifyReplacedReplicasSynced` was missing, and it is the data
tier's redundancy gate.** Its own comment names the invariant
([`:1459-1466`](../../../internal/controller/rolling_update.go#L1459-L1466):
"Without this guard, the operator would … immediately delete the next
candidate, resulting in multiple replicas being replaced simultaneously") —
and a terminating replaced pod passes it: `Ready=True` for its whole
termination, `master_link_status:up` until the process stops. (Corrected
2026-08-25: ~~for the full 60 s drain~~ — a replaced *replica* releases the drain
hook in ~1 s. The blindness is real regardless of the duration; only its size
was overstated.) Chaos-kill
of an already-replaced replica mid-roll → the guard passes → `replaceNextReplica`
deletes the next candidate → two replicas down at once. This is consequence (b)
on the data tier; the first decision fixed it only on the Sentinel `readyCount`.
Not a regression (today behaves the same), but the promised e2e — "no second pod
is deleted while the first terminates" — **fails on exactly this shape under the
first decision's site list.**

**Finding 2 — a terminating un-replaced replica away from `candidates[0]` blocks
nothing.** `verifyReplacedReplicasSynced` skips `needsUpdate` pods
([`:1455`](../../../internal/controller/rolling_update.go#L1455)), and
`candidates[0]` is chosen by age, never by termination. Five-replica shape:
chaos kills replica-2 (old template, terminating); `candidates[0]` is the
younger replica-4 → delete → again two down. Neither today's code nor the first
decision's site list prevents it.

**Finding 3 — the table's claim about `deleteNextPendingPod` was wrong.**
"waits instead of re-deleting" — the code is `continue`
([`:2740-2743`](../../../internal/controller/rolling_update.go#L2740-L2743)),
i.e. skip-to-next. With exactly one pending pod the loop falls through to a
requeue, which happens to be a wait; with ≥ 2 pending pods (stale-multi-master
shapes, rare — reachable only with state `""`/`replacing-replicas` and several
`isMaster` pods still needing update) a naive `available()` substitution
deletes the second while the first terminates — the same regression class the
*Corrections* section forbids for `sortReplicaCandidates`. Corrected in
*Relevant code*; the gate that resolves it is E3.

**Finding 4 — excluding terminating pods from `firstOutdatedPod` changes
Sentinel serialization for tiers ≥ 5.** Today the no-op re-delete of the
terminating outdated sentinel serializes the roll by accident. Under the first
decision: 5 sentinels, quorum 3, one terminating → `readyCount=4`,
`4-1=3 >= 3` → a second sentinel is deleted, two down concurrently. That is
consistent with the existing NotFound skip
([`:3600-3606`](../../../internal/controller/rolling_update.go#L3600-L3606)) — the
quorum guard, counting correctly, permits it by design — but it contradicts the
"one at a time" in the strategy comment
([`:3560-3570`](../../../internal/controller/rolling_update.go#L3560-L3570)). For
the fleet's 3-replica tiers the quorum guard blocks either way, so there is no
observable difference there. R2 resolves it in the strict direction: the delete
waits while any sentinel pod terminates.

**What the re-review confirmed unchanged.** The wait-semantics substitutions
are safe as analysed (`replaceNextReplica` [`:1390`](../../../internal/controller/rolling_update.go#L1390),
`replaceRemainingPods` [`:1868`](../../../internal/controller/rolling_update.go#L1868),
`waitForReplicasReady` [`:1669`](../../../internal/controller/rolling_update.go#L1669),
standalone [`:2566`](../../../internal/controller/rolling_update.go#L2566)/[`:2581`](../../../internal/controller/rolling_update.go#L2581));
skip semantics are correct at `findPromotionCandidate`
([`:2894`](../../../internal/controller/rolling_update.go#L2894)) and
`verifyNewMasterReady` ([`:2161`](../../../internal/controller/rolling_update.go#L2161));
the `countUpdatedPods` exclusion routes correctly at both dispatchers
([`:458-462`](../../../internal/controller/rolling_update.go#L458-L462),
[`:2671-2689`](../../../internal/controller/rolling_update.go#L2671-L2689) — the
`== totalPods` branch dispatches to the same state handlers as the `<` branch,
so exclusion delays finalization and changes no routing) and self-heals through
the NotFound → `needsUpdate` path
([`:1296`](../../../internal/controller/rolling_update.go#L1296)); and the
ADR 0025 carve-out already stands as a code comment
([`:1316-1319`](../../../internal/controller/rolling_update.go#L1316-L1319)).

**Why the amendment is a delete gate and not a longer site list.** All four
findings share one cause: "may I spend it" was distributed over ~12 individual
sites, but the delete half of the invariant is one sentence — *no pod delete
while any pod of the same tier is terminating*. Stated as a gate (R2), it
subsumes findings 1–4 in one branch per delete path, makes the promised e2e
true unconditionally, and costs the normal roll nothing: the roll already
serializes on the termination of its own delete (the terminating pod stays
`candidates[0]` and is waited on). The safe direction holds for a terminating
master too: hold the delete, and `masterIdx < 0 && hasPendingUpdates`
([`:502`](../../../internal/controller/rolling_update.go#L502)) catches the state
once the pod is gone. Deliberately **not** gated by R2: the post-failover
states (`handlePostManualFailover`, topology restoration) — they must proceed
while the master the operator itself just deleted terminates, or the roll would
deadlock on its own delete.

### Second re-review (2026-08-25): the decided rules do not hold as written

A third adversarial pass, this time against **both** decided rules rather than
the site list alone: 15 agents, each finding re-checked by an independent
refuter, then every load-bearing claim re-verified by hand in the clean tree
(`git status` clean, `rolling_update.go` at 3715 lines). Six of the findings
change the implementation; two of them are regressions the decision would have
introduced. Consequence: **R1 is replaced, R2 is re-placed, and (i-a) is
re-argued.** The Decision block below records the outcome as E1-E8.

**S1 — `countUpdatedPods` in R1 is a regression, and the worst one available.**
Verified: [`handleRollingUpdate:459-462`](../../../internal/controller/rolling_update.go#L459-L462)
is a bare `if updatedCount == totalPods { return r.finalizeRollingUpdate(...) }`
with **no state switch** — unlike the multi-replica dispatcher at
[`:2671-2689`](../../../internal/controller/rolling_update.go#L2671-L2689), which is
what the first re-review looked at when it concluded "exclusion delays
finalization and changes no routing". On the Sentinel path the exclusion does not
delay finalization, it *re-enters the post-failover state machine*: the pass falls
through to [`:485-487`](../../../internal/controller/rolling_update.go#L485-L487) →
`handlePostFailover`. There `isFailoverTimedOut` (30 s) and
`isReplicaReconnectTimedOut` (90 s) both read the same `annotationFailoverTimestamp`
([`:2296-2318`](../../../internal/controller/rolling_update.go#L2296-L2318)), which in
the terminal pass is minutes old — so the very first pass takes the timed-out
branch with no wait:

* the terminating pod still answers `role:master, connected_slaves:0` (a Valkey
  shutting down closes replica links first) → `handleMasterWithNoReplicas` →
  `forceReplicaConnections` + `resetSentinelState(<the dying pod>)`. Per ADR 0022
  a reset routed through a dead master is the unrecoverable direction;
* it has stopped answering → `masterIdx = -1` (`labelClaimsMaster` refuses a
  `DeletionTimestamp` pod) → `handleNoMasterFound` → `stateFailoverReset` →
  `handleFailoverRetrigger` → a real `SENTINEL FAILOVER` **on a fully-updated,
  healthy cluster**.

Today that same input goes to `finalizeRollingUpdate` → `checkFinalizationTopology`,
which arms `annotationFinalizationTimestamp` and is capped by
`finalizationStallTimeout = 2 min`. R1 removes that bound from the shape entirely.
And consequence (c) — the only reason the member was on the list — is reachable
*only* with the state annotation set, which is exactly the input that reroutes.

**S2 — `make cyclo` is already at the ceiling.** Measured in the clean tree:
`CYCLO_THRESHOLD = 15`, `gocyclo -over 15`, and **five functions sit at exactly
15** — including `checkAndHandleSentinelRollingUpdate`
([`:3571`](../../../internal/controller/rolling_update.go#L3571)), where both rules
must land, and `sentinelRolloutComplete`
([`valkey_controller.go:1613`](../../../internal/controller/valkey_controller.go#L1613)).
Any added branch turns CI red. Extracting the sentinel per-pod scan into a helper
is a **prerequisite of the change**, not follow-up work.

**S3 — R2 at a function head is a pass gate, not a delete gate.** Three separate
failures, all verified:

* `replaceNextReplica` opens with `verifyReplacedReplicasSynced`
  ([`:1372`](../../../internal/controller/rolling_update.go#L1372)), the only site on
  that path that both arms and clears the sync-wait bound — and `ensureWaitBound`
  is first-seen-wins. A gate above it leaves an armed bound ageing unobserved, so
  the next genuine sync wait inherits a spent budget and lands in
  `pauseRollingUpdate` — a **Warning Event** plus `clearRollingUpdateState`, the
  handover ADR 0010 D2-D4 forbids. A gate below it makes
  `verifyReplacedReplicasSynced` probe the terminating pod and arm the bound
  against it: same ending, other route.
* `replaceNextReplica` returns `nil` for "nothing to replace", and both
  dispatchers read that `nil` as "advance"
  ([`:491-508`](../../../internal/controller/rolling_update.go#L491-L508),
  [`:2720-2735`](../../../internal/controller/rolling_update.go#L2720-L2735)). A
  requeue at the head makes `handleMasterFailover` and `handleManualFailover`
  unreachable.
* `handleMasterWithNoReplicas` clears the reconnect counter **before** its tail
  call to `replaceRemainingPods`
  ([`:1996-2003`](../../../internal/controller/rolling_update.go#L1996-L2003)). A gate
  at that function's head discards the call, the counter restarts at zero, and the
  infinite retry loop the comment at [`:1975`](../../../internal/controller/rolling_update.go#L1975)
  claims to break is open again.

**S4 — the decision's visibility claim is false, and the blast radius is bigger
than "the roll stops".** [`valkey_controller.go:326`](../../../internal/controller/valkey_controller.go#L326)
returns on `NeedsRequeue` **before** `handlePostRollingUpdateChecks` and
`updateStatus`. Suspended for the whole stall: the Sentinel roll,
`checkAndRecoverNoMaster` ([`:407`](../../../internal/controller/valkey_controller.go#L407)),
**`checkSteadyStateSplitBrain`** ([`:421`](../../../internal/controller/valkey_controller.go#L421))
— per ADR 0011 D1 the only thing that re-detects a split brain outside a rolling
update — and the status write. So ~~the stall is visible via
`SentinelUpdatePending`~~ is **wrong**: that condition is written behind the
return the stall causes. ADR 0010's Residual risks already recorded this cost
("the tail of the pass is skipped") for the stuck-`Terminating` case; the first
decision did not carry it forward. On a NotReady node the `DeletionTimestamp`
never clears, so the blackout is permanent.

**S5 — the same file already contains the bounded version of this wait.**
[`:3003-3008`](../../../internal/controller/rolling_update.go#L3003-L3008):
`if masterPod.DeletionTimestamp != nil { ... return r.waitOrAbandonManualFailover(...) }`.
Same question, same tier, bounded, with an abandon exit. Shipping the identical
wait unbounded three hundred lines away puts two contradictory contracts in one
file, selected by which state annotation happens to be set. ADR 0026 has to name
`:3003` as the one bounded exception and say why the post-failover state earns a
bound the replica phase does not.

**S6 — one more missing site, and the pattern behind it.** [`:1930`](../../../internal/controller/rolling_update.go#L1930)
`handlePostFailover` selects the **new master** on `!ps.ready` and leads to the old
master's delete; it is on neither of the two earlier site lists. Counted properly,
there are 13 `podState.ready` readers and **exactly one** wants "can I talk to it".
A rule stated as a list of the other 12 has now been wrong three times — first
missing `verifyReplacedReplicasSynced`, then Findings 2-4, now `:1930`. That is the
argument for E1: the failure is the *shape* of the rule, not the diligence of the
reviews.

**S7 — `anyTerminating` must be ordinal-scoped.** `collectPodStates`
([`:1280-1288`](../../../internal/controller/rolling_update.go#L1280-L1288)) and the
sentinel loop ([`:3588-3597`](../../../internal/controller/rolling_update.go#L3588-L3597))
are both bounded by the *current* replica count. A label-selector `List` — the
literal reading of "any pod of the same tier" — also sees surplus ordinals draining
from a concurrent **scale-down**, which the roll neither caused nor can influence.
A 5→3 scale-down plus an image bump in one apply would then hold every delete for
the whole drain.

**S8 — there is no regression signal today, and envtest cannot supply one.** No
rolling-update unit fixture sets a `DeletionTimestamp`; the only pod-level helper
that does is `split_brain_report_test.go:47` (from T4), and it adds a
`foregroundDeletion` finalizer — which also makes `deleteOwnedPod` a silent no-op,
so a naive "delete A while B terminates, assert A survives" passes whether or not
R2 exists. Fixtures that pass `pod: nil` mean an R2 written as
`ps.pod.DeletionTimestamp` panics rather than failing usefully. And in envtest a
deleted pod never leaves `Terminating` (no kubelet), so R2 is not exercisable
there — a trap for whoever writes the first rolling-update integration test.

**Left open on purpose, and re-filed rather than folded in (E8).**
`grep DeletionTimestamp internal/controller/steady_state_master.go` returns
**nothing**: `listMasterLabeledPods` filters on label and ownership only, and
`adoptUnrecordedPromotion` → `adoptMaster` → `recordPromotedMaster` writes the
`known-master` authority. An ungracefully killed master keeps its
`instanceRole=master` label. Likewise `valkey_controller.go` reads exactly one
`DeletionTimestamp` — the CR's own ([`:224`](../../../internal/controller/valkey_controller.go#L224))
— so `checkAndRecoverNoMaster` / `probeForAnyMaster` can send `REPLICAOF NO ONE`
to a dying pod. **Not obviously a defect**, which is why it is not folded in: a
StatefulSet pod name is stable, so recording a terminating pod *by name* still
resolves to the pod that returns. Whether that is right depends on persistence and
on what the pod comes back holding — an analysis T5 does not contain. See T13.

**What the re-review confirmed unchanged.** `sortReplicaCandidates` untouched is
right (verified [`:1421-1442`](../../../internal/controller/rolling_update.go#L1421-L1442):
filtered on `needsUpdate && !isMaster`, sorted by age, no termination input — the
terminating pod genuinely stays `candidates[0]`). The ADR 0025 carve-out is right
and is already half-implemented in the file
([`:1316-1319`](../../../internal/controller/rolling_update.go#L1316-L1319),
`labelClaimsMaster`). **R2 does not suppress split-brain repair**: `resolveSplitBrain`
runs at [`:454`](../../../internal/controller/rolling_update.go#L454) and
[`:2669`](../../../internal/controller/rolling_update.go#L2669), before any dispatch on
both paths — which is also what disqualifies an entry-level gate as an alternative
shape. No self-deadlock on the operator's own delete: every self-delete writes its
routing state first. `clearStaleRollingUpdateState` is unaffected — it takes
`countReplacedPods`, which is readiness-blind. `reconcileResources` and
`nudgeShortStatefulSets` run before the rolling-update check, so a gated pass still
reconciles every managed object and still nudges a short StatefulSet: the blast
radius of a stall is the workload half only. And **R2 costs a clean roll nothing** —
it replaces a no-op re-delete with a no-op wait at the same cadence, one API call
fewer, for the ~1 s a replica actually takes to terminate.


### Decision

- 2026-08-24: analysis recorded above; the original proposed work corrected in
  place (it would delete a second replica), and the "deduplicates the
  RollingUpdate events" claim withdrawn. Severity re-assessed.
- 2026-08-24: **assumptions verified before deciding** — see *Verification run*
  above. Two of the first analysis's claims were falsified and rewritten in
  place; the load-bearing one (kubelet keeps `PodReady=True` through graceful
  termination) was measured on Kubernetes 1.36.1 and holds for the full grace
  period.
- 2026-08-24: **Option 3 accepted, with sub-decisions (i-a) and (ii-b).**

  **What lands — amended 2026-08-24 after the site-list re-review; the original
  single-rule site list is superseded and rewritten here (what it got wrong:
  see *Re-review of the decided site list*).**
  > **Superseded 2026-08-25 by E1-E5.** R1 is replaced by the renamed default
  > (E1); `countUpdatedPods` leaves the spend rule entirely (E2, it was a
  > regression on the Sentinel path); R2 survives but moves from the function
  > heads named below to the individual deletes (E3); `:2818` gains a stated
  > exemption (E4). The carve-out, `sortReplicaCandidates` and the no-Warning
  > constraint below are unchanged and still current. `podState` gains
  `terminating bool`, set in `collectPodStates` from
  `pod.DeletionTimestamp != nil`, plus
  `func (ps podState) available() bool { return ps.ready && !ps.terminating }`.
  Two rules:

  **R1 — counting and promotion sites use `available()`**, and only they:
  `countUpdatedPods` ([`:1335`](../../../internal/controller/rolling_update.go#L1335)),
  `waitForReplicasReady` ([`:1669`](../../../internal/controller/rolling_update.go#L1669)),
  `verifyNewMasterReady` ([`:2161`](../../../internal/controller/rolling_update.go#L2161)),
  `findPromotionCandidate` ([`:2894`](../../../internal/controller/rolling_update.go#L2894)),
  and the Sentinel `readyCount`
  ([`:3614`](../../../internal/controller/rolling_update.go#L3614)). The standalone
  loop substitutes `available()` at its two `isPodReady` calls
  ([`:2566`](../../../internal/controller/rolling_update.go#L2566),
  [`:2581`](../../../internal/controller/rolling_update.go#L2581)) — a delete gate
  and a boot wait on a single-pod tier, so R1 and R2 coincide there; both are
  verified wait semantics.

  **R2 — the delete gate: no pod delete while any pod of the same tier is
  terminating.** One `anyTerminating → requeue` check at the top of
  `replaceNextReplica` ([`:1366`](../../../internal/controller/rolling_update.go#L1366)),
  `replaceRemainingPods` ([`:1854`](../../../internal/controller/rolling_update.go#L1854))
  and `deleteNextPendingPod` ([`:2738`](../../../internal/controller/rolling_update.go#L2738)),
  and before the Sentinel delete
  ([`:3643`](../../../internal/controller/rolling_update.go#L3643)). R2 replaces the
  first decision's per-delete-site `available()` substitutions: it additionally
  closes the `verifyReplacedReplicasSynced` blindness (re-review Finding 1), the
  non-`candidates[0]` terminating replica (Finding 2), the `deleteNextPendingPod`
  skip-to-next hazard (Finding 3), and it keeps the Sentinel roll strictly
  serialized for tiers ≥ 5 (Finding 4). `firstOutdatedPod` therefore keeps
  selecting a terminating outdated pod — R2 waits on it instead of skipping it,
  mirroring the data tier's `candidates[0]` rule. The existing `!ps.ready`
  branches at the delete sites stay as they are: they answer "booting", R2
  answers "terminating". Explicitly **not** gated by R2: the post-failover
  states (`handlePostManualFailover`, topology restoration) — they must proceed
  while the master the operator itself just deleted terminates, or the roll
  would deadlock on its own delete.

  **What deliberately does not change.** `isPodReady` itself; `demoteRogueMaster`'s
  `!ready` refusal ([`:1203`](../../../internal/controller/rolling_update.go#L1203));
  the `podState` built in
  [`steady_state_master.go:593`](../../../internal/controller/steady_state_master.go#L593).
  Those three answer "can I talk to it", and a terminating master that still
  answers `INFO` must still be demoted — refusing it would leave a real second
  master accepting writes for up to the 60 s cap of the drain hook, which is the
  wrong trade against T12. **`sortReplicaCandidates` is not touched**: the
  terminating pod must stay `candidates[0]` and be waited on, never skipped.

  **Rejected.** Option 1 fixes the log line and leaves (a), (b), (c). Option 2 is
  a strict subset of Option 3, not a competitor, and lands inside it. Option 4
  buys a resume for a case that should not resume — see (i-a). Option 5 pays the
  analysis cost three times for a fix that is one field and one helper.

  **(i-a) accepted: the new waits stay unbounded, and ADR 0010 says so.**
  > **Partly superseded 2026-08-25 by E5.** The delete is still never resumed —
  > that half stands. ~~The stall is visible — the phase string on the data tier,
  > `SentinelUpdatePending=True` on the Sentinel tier.~~ False: a
  > `NeedsRequeue` return ends the pass before the Sentinel check ever runs
  > (S4). The wait now arms a bound whose expiry restores the pass tail instead
  > of resuming the delete. Option 3
  widens the unbounded-requeue class ADR 0010 already names in its Residual risks
  (a pod "stuck `Terminating` … requeues with no bound"); the R2 delete-gate waits
  belong to the same class, and that item is extended in the same change to name
  the terminating-pod inputs — R1 exclusions and R2 gate — explicitly. Accepted
  cost: a pod that never finishes terminating stalls the roll instead of letting
  it delete the next pod. That is the safe direction (ADR 0007: stopping half-done keeps a
  serviceable cluster) and it is visible — the phase string on the data tier,
  `SentinelUpdatePending=True` with its progress message on the Sentinel tier
  (ADR 0024). (i-c) — a named `terminationWaitTimeout` — is the follow-up if the
  stall proves to matter in the field; it costs the same then as now.

  **(ii-b) accepted: new ADR 0026, "A pod being deleted is not available."**
  > **Extended 2026-08-25.** Still ADR 0026, with the two-question split now
  > expressed in the type (`available()`/`reachable()`) rather than as a
  > convention, plus an ADR 0024 amendment (E6) and the additional content listed
  > under the 2026-08-25 Decision entry. The
  rule binds the data tier, the Sentinel tier and a steady-state carve-out;
  ADR 0007 owns only the first. The ADR states the two-question split as the
  decision — `ready` answers "can I talk to it", `available()` answers "may I
  spend it" — so the carve-out is a rule rather than a comment, and records the
  measurement as its Context. Index line in
  [`docs/adr/README.md`](../../adr/README.md); cross-references from ADR 0007 D9,
  ADR 0004 (the quorum the Sentinel PDB is derived from), ADR 0022 (what a
  Sentinel tier without a majority costs), ADR 0010 (the widened residual risk)
  and ADR 0025 (the carve-out). CLAUDE.md gets a short pointer.

  **Hard constraint on the implementation, verified:** `requireNoWarningEvents`
  ([`test/e2e/pdb_test.go:447`](../../../test/e2e/pdb_test.go#L447)) fails on **any**
  Warning of any reason regarding the CR, asserted by two subtests
  ([`rolling_update_test.go:222`](../../../test/e2e/rolling_update_test.go#L222),
  [`:485`](../../../test/e2e/rolling_update_test.go#L485)). Every new wait reports
  through logs, the phase string and conditions — never an Event.

  **Verification the change owes.** Unit: the six characterization tests above,
  inverted (a terminating pod is not a promotion candidate, is not counted by the
  Sentinel `readyCount`, is not counted by `countUpdatedPods`, is waited on rather
  than re-deleted, stays `candidates[0]`), plus the R2 gate cases from the
  re-review — a terminating **already-replaced** replica holds the next candidate
  delete (Finding 1), a terminating `needsUpdate` replica away from
  `candidates[0]` holds it too (Finding 2), `deleteNextPendingPod` with ≥ 2
  pending pods and the first terminating deletes nothing (Finding 3), and no
  second sentinel delete while any sentinel pod terminates, also at 5 replicas
  where the quorum guard alone would permit it (Finding 4) — plus the control
  that a terminating pod answering `INFO` as master is **still demoted quietly**
  — the ADR 0025/T12 carve-out, which is the one regression this change could
  plausibly cause. E2E: delete a pod mid-roll on both topologies — including the
  already-replaced-pod shape, the one the first decision's site list would have
  failed — and assert no second pod is deleted while the first terminates, and
  that the roll finishes afterwards.

- 2026-08-24 (later the same day): **decision amended in place — the spend rule
  is now R1 + R2** (see *Re-review of the decided site list* for the four
  findings that forced it). Falsified from the first decision and corrected
  above: the `deleteNextPendingPod` table row claimed "waits instead of
  re-deleting" where the code skips to the next pod; the site list missed
  `verifyReplacedReplicasSynced`, the data tier's own redundancy gate; and the
  promised e2e would have failed under the first site list whenever the
  chaos-killed pod was an already-replaced one. Unchanged by the amendment:
  Option 3 itself, the carve-out (`demoteRogueMaster`, steady-state `podState`),
  `sortReplicaCandidates` untouched, (i-a), (ii-b), and the no-Warning-Event
  constraint. ADR 0026 states both rules, with R2 as the sentence that carries
  the invariant: *the operator never deletes a pod of a tier while any pod of
  that tier is terminating* — and names the deliberate exception (the
  post-failover states proceed while the master the operator itself deleted
  terminates).
  > **Read on.** The 2026-08-25 entry below supersedes R1 entirely and re-places
  > R2. The invariant sentence quoted here survives; the exemption list quoted
  > here does not — it named the wrong states (S3, E3, E4).

- 2026-08-25: **decision amended again — R1 is replaced, R2 is re-placed, (i-a) is
  re-argued.** Driver: the *Second re-review* above, plus a corrected measurement.
  The earlier decision bodies stay readable for the reasoning they carry, but where
  they conflict with the rules below, **the rules below are the current ones.**

  **Corrected measurement, applied throughout T5.** ~~Multi-replica non-Sentinel
  data pods carry a 60 s drain `preStop`, so every rolling-update pod replacement
  has a 60 s terminating-Ready window.~~ The probe that produced that number used
  an unconditional `preStop: sleep 60`; the operator's hook is a wait loop with a
  60 s **cap** ([`statefulset.go:660-668`](../../../internal/builder/statefulset.go#L660-L668))
  whose marker is released by a `defer` on every exit path of `Handle`, which
  returns immediately for a non-master
  ([`drain.go:103-116`](../../../internal/sidecar/drain.go#L103-L116)). A replica
  delete releases it in ~1 s on every topology; 60 s is approachable only by a
  master whose drain failover is still running. What survives unchanged: kubelet
  keeps `PodReady=True` for the whole termination, whatever its length.

  **E1 — the spend rule is a default, not a list.** `podState.ready` is renamed to
  `readyCondition`, and two accessors are added:
  `available() = readyCondition && !terminating` and `reachable() = readyCondition`,
  with `terminating` set in `collectPodStates` from `pod.DeletionTimestamp != nil`
  and at [`steady_state_master.go:593`](../../../internal/controller/steady_state_master.go#L593),
  so `available()` is never structurally false at a construction site. The rename is
  the point, not cosmetic: it turns all 13 reads into compile errors, so each is
  decided once and recorded, and new code that reaches for the obvious name gets the
  safe answer. Per-site answers: the table in *Relevant code*. This **supersedes R1**
  and the two site lists before it — the rule had been wrong three times as a list
  (missing `verifyReplacedReplicasSynced`, then re-review Findings 2-4, then
  [`:1930`](../../../internal/controller/rolling_update.go#L1930)), and 13:1 makes the
  enumeration an enumeration of the wrong half.

  **E2 — `countUpdatedPods` leaves the spend rule; the completion hold moves inside
  `finalizeRollingUpdate`, Sentinel path only.** The dispatch predicate at
  [`:461`](../../../internal/controller/rolling_update.go#L461) and
  [`:2671`](../../../internal/controller/rolling_update.go#L2671) is computed exactly as
  today — S1 is the reason. On the Sentinel path the `RollingUpdateComplete` Event
  and `clearRollingUpdateState` are held inside `finalizeRollingUpdate` while a data
  pod terminates, requeueing from there and never falling back into the state
  machine; that path already sits inside `checkFinalizationTopology`'s
  `finalizationStallTimeout = 2 min`. On the non-Sentinel path the hold is **not**
  applied: `finalizeMultiReplicaRollingUpdate`
  ([`:3510`](../../../internal/controller/rolling_update.go#L3510)) has no bound, so
  holding there would create exactly the unbounded class E5 exists to close.
  Accepted there: consequence (c) stands, i.e. the Event can fire seconds early over
  a pod that is terminating — which is what happens today.

  **E3 — R2 is pinned to each delete, never to a function head.** The gate goes
  immediately before each `deleteOwnedPod`: in `replaceNextReplica` **after**
  `verifyReplacedReplicasSynced` and after the `len(candidates) == 0` return; in
  `replaceRemainingPods` **inside** the loop; before
  [`:2745`](../../../internal/controller/rolling_update.go#L2745); and before the
  sentinel delete, after the quorum guard. S3 is the reason for each placement. The
  invariant sentence is unchanged: *the operator never deletes a pod of a tier while
  any pod of that tier is terminating.* `anyTerminating` is computed over the
  ordinal range `[0, *sts.Spec.Replicas)` — **never a label-selector List** (S7).

  **E4 — the manual-failover old-master delete
  ([`:2818`](../../../internal/controller/rolling_update.go#L2818)) is exempt when the
  pod being deleted is itself `isMaster`**, and the exemption is written down rather
  than left to the accident of call order. The promotion has already happened, so the
  two-down risk is already accepted; and if `promoteAndRedirect`'s best-effort
  demotion failed, holding the delete would extend a genuine two-master state toward
  `splitBrainWarnAfter` = 90 s — the edge the e2e asserts on. Noted honestly: with the
  demotion succeeding, that clock never starts, and nothing terminates at that point
  in a clean roll, so the exemption is insurance, not a hot path.

  **E5 — the refusal stays unbounded; the *observation* does not.** Supersedes
  (i-a) as argued. The delete is never resumed — that direction is unchanged and is
  ADR 0007's. What changes: the wait arms its own bound via `ensureWaitBound`, and on
  expiry (a) sets a condition naming the pod (`PodTerminationStalled`, reason
  `PodStuckTerminating`) and (b) **stops setting `NeedsRequeue`**, so the pass tail
  runs again — `checkAndHandleSentinelRollingUpdate`, `checkAndRecoverNoMaster`,
  `checkSteadyStateSplitBrain`, `updateStatus`. The rolling-update state annotation
  stays, nothing is deleted, and the roll resumes on its own once the pod is gone,
  because the StatefulSet watch fires (there is no Pod watch —
  [`valkey_controller.go:2710-2727`](../../../internal/controller/valkey_controller.go#L2710-L2727)).
  This needs one field on `RollingUpdateResult`
  ([`:133`](../../../internal/controller/rolling_update.go#L133)) and one branch in
  `reconcileWorkload`; the pattern already exists as the `pending` result
  `handlePostRollingUpdateChecks` returns. **No Event on any of it** — the
  `requireNoWarningEvents` constraint is unchanged. Rejected again, with a second
  reason: (i-c) via `pauseRollingUpdate` emits a Warning *and* clears the state.

  **E6 — the Sentinel guard goes on the shared predicate, and ADR 0024 is amended in
  the same change.** [`:3614`](../../../internal/controller/rolling_update.go#L3614) is a
  single `isPodReady` feeding both `readyCount` (the quorum guard) and
  `updatedReadyCount` (the `SentinelUpdatePending` False flip and the
  `SentinelUpdateComplete` Event). Guarding the shared predicate closes the too-early
  completion edge — which is the class T5 is about, and which matches
  `finishSentinelRollingUpdate`'s own stated contract — at the price that the
  completion marker now also waits out a sentinel termination unrelated to the roll.
  That is a change to a marker ADR 0024 made load-bearing for external sequencing, so
  ADR 0024 says so in the same commit rather than afterwards.

  **E7 — Sentinel serialization for tiers ≥ 5 is not claimed, and the false comment
  is corrected.** At [`:3600-3606`](../../../internal/controller/rolling_update.go#L3600-L3606)
  a pod that is already **gone** (NotFound, not terminating) is skipped, which lowers
  `readyCount` and advances `firstOutdatedPod`; at 5 sentinels with quorum 3 the
  arithmetic permits deleting the next one while the previous replacement is still
  booting, and R2 cannot see it because the `DeletionTimestamp` is gone by then. The
  quorum guard is accepted as the invariant — it is the same one ADR 0004 derives the
  Sentinel PDB from — and the doc comment at
  [`:3560-3570`](../../../internal/controller/rolling_update.go#L3560-L3570) claiming "one
  at a time" is corrected. **ADR 0026 must not claim R2 delivers strict
  serialization.** For the fleet's 3-replica tiers there is no observable difference.

  **E8 — the steady-state adoption path is re-filed, not folded in.** See the
  paragraph in *Second re-review* and **T13**. T5 stays the rolling-update rule.

  **Prerequisite, not follow-up (S2).** Extract the per-pod scan of
  `checkAndHandleSentinelRollingUpdate` into a helper returning
  `{readyCount, updatedReadyCount, firstOutdatedPod, anyTerminating}` **before**
  adding any branch. Five functions currently measure exactly 15 against
  `gocyclo -over 15`; two of them are touched by this change. Verify `make cyclo`
  before and after.

  **Verification the change owes — amended.** Unchanged from the 2026-08-24 list:
  the six characterization tests inverted, the R2 gate cases from re-review
  Findings 1-4, and the ADR 0025 carve-out control (a terminating pod answering
  `INFO` as master is still demoted quietly). Added: a test that the Sentinel
  dispatcher still reaches `finalizeRollingUpdate` when a data pod terminates
  (the S1 regression, which is the one this change could most plausibly
  re-introduce); a test that the sync-wait bound is neither armed nor stranded by a
  termination wait (S3); a test that `handleMasterWithNoReplicas` still reaches
  `replaceRemainingPods` after `maxReconnectResets` (S3); and a test that a
  concurrent scale-down does not gate the roll (S7). **Fixture warning (S8):** no
  existing rolling-update fixture sets a `DeletionTimestamp`, the only helper that
  does adds a `foregroundDeletion` finalizer that makes `deleteOwnedPod` a silent
  no-op, and several fixtures pass `pod: nil` — so `anyTerminating` must read through
  `ps.terminating`, never `ps.pod.DeletionTimestamp`. R2 is **not** exercisable in
  envtest: no kubelet, so a deleted pod never leaves `Terminating`.

  **ADR 0026 owes, beyond the two rules.** The full read set with each site's
  answer; "the tier" defined as the ordinal range; the carve-out phrased as a rule
  about the *question* rather than about `steady_state_master.go` as a file;
  [`:3003`](../../../internal/controller/rolling_update.go#L3003) named as the one
  bounded termination wait and why; and S7, S8, E7 and E8 recorded as residual
  risks. Cross-references as before, plus ADR 0024 (E6).

### Implementation (2026-08-25)

Landed on `fix/bad-findings`. `make lint`, `make cyclo`, `make gosec` and
`make test-unit` all green; the e2e is written but **not run** — no cluster was
available in this session, which is stated here rather than implied.

**What landed, against E1-E8.**

| Rule | Landed as | Note |
|---|---|---|
| E1 | `podState.ready` → `readyCondition`, plus `available()` / `reachable()` and `terminating` / `terminatingSince` | all 13 readers decided individually; `steady_state_master.go` fills `terminating` too, so `available()` is never structurally false at a construction site |
| E2 | `countUpdatedPods` keeps `reachable()`; the hold sits inside `finalizeRollingUpdate`, Sentinel path only | **the *Relevant code* table above said `available()` for `countUpdatedPods` and contradicted E2 — E2 wins, and that row is wrong. Corrected in place below.** Placed *before* `checkFinalizationTopology`, not after: that function ends in `syncSentinelWithMaster`, and a terminating master still answers `INFO` as master (ADR 0022) |
| E3 | gate immediately before each of the four gated `deleteOwnedPod` calls | placements exactly as decided |
| E4 | no gate at [`handleManualFailover`](../../../internal/controller/rolling_update.go)'s old-master delete, with the reasoning as a code comment and two tests | **strengthened by a finding while testing**: on that path `waitForReplicasReady` refuses every *other* terminating pod of the tier one layer up, so the gate would only ever have fired on the master. The exemption cannot widen the two-down risk |
| E5 | `terminationWait` → `NeedsRequeue` inside the budget, `DeferredRequeueAfter` + `PodTerminationStalled` past it; one field on `RollingUpdateResult`, one branch in `reconcileWorkload` | **deviation 1, decided with the user: the clock is the pod's own `deletionTimestamp`, not `ensureWaitBound`.** See below |
| E6 | `scanSentinelPods` returns `{readyCount, updatedReadyCount, firstOutdatedPod, terminating}`; one availability predicate feeds both counters; ADR 0024 amended with a D8 in the same change | as decided |
| E7 | the "one at a time" doc comment corrected; ADR 0026 D8 states the quorum guard as the invariant | as decided |
| E8 | not folded in; T13 carries it, and ADR 0026 D10 names it as out of scope | as decided |
| S2 | the per-pod scan extracted **before** any branch was added | `checkAndHandleSentinelRollingUpdate` was at exactly 15 and is now off the ≥14 list; `make cyclo` green before and after |

**Deviation 1 — the stall clock (agreed with the user before implementing).**
E5 said the wait arms its bound via `ensureWaitBound`. It does not. The pod
already carries the exact timestamp: `metav1.DeletionTimestamp`, which the API
server sets to `now + gracePeriodSeconds` — verified in
`k8s.io/apiserver@v0.35.0`, `pkg/registry/rest/delete.go:162`, **not** the instant
of the delete. `time.Since` of it is therefore the *overrun past the graceful
deadline*, zero at the deadline, and per-tier-correct without the code knowing any
grace period (75 s data, 30 s Sentinel are inside the zero point).
`podTerminationOverrun` = 2 min.

Why this is not a weakening of ADR 0010: D7/D8 exists because those bounds measure
something the operator started and have no timestamp but the one *they write* — a
write that can fail forever, leaving the bound unarmed. Here nothing is armed, so
nothing can fail to arm. What it additionally buys: no CR write per pod
replacement (the `ensureWaitBound` version cost two, arm and clear), no tenth
annotation in `clearRollingUpdateState`, and a deadline **per pod** instead of
first-seen-wins per CR — the latter would have reported the next termination as
stalled immediately whenever a bound aged through a slow pod boot. Recorded as
ADR 0026 D5 and as a narrowing note in ADR 0010's Residual risks. Residual: clock
skew between operator and API server, absorbed by a 2 min budget, not measured.

**Deviation 2 — E5's mechanism had to cover more than the delete gate, and that
was a hole the decision did not see.** E1 makes `verifyReplacedReplicasSynced`,
`replaceNextReplica`, `waitForReplicasReady` and `replaceRemainingPods` return on
`!available()` *before* they probe the pod. Before the change those same inputs
went through the probe, the probe failed, and the **sync-wait bound** ended the
retry. Excluding the pod without routing it anywhere therefore replaced four
bounded waits with four unbounded ones — the ADR 0010 D1 failure in the other
direction, and it is also why the promised "the pass tail runs again" test passed
for the wrong reason until this was fixed. All four now call
`waitForUnavailablePod`, which splits by *why* the pod is unavailable: a booting
pod keeps the plain requeue it always had, a terminating one gets the bounded
observation. `verifyNewMasterReady` got the same treatment for the candidate it
skips. Found by mutation testing, not by review.

**Deviation 3 — two sites the decision listed or implied were missed on the first
pass and closed on re-read, both found before any test was written for them.**

* **The standalone tier.** The *Relevant code* "Raw `isPodReady` call sites" table
  names [`:2566`](../../../internal/controller/rolling_update.go)/[`:2581`](../../../internal/controller/rolling_update.go)
  as "available, inline". They were left unchanged in the first pass. A single-pod
  tier is a tier: both branches now go through `standaloneWait`, so the only pod is
  not re-deleted while it terminates — which is the cheapest place to observe the
  duplicate delete that started this item.
* **The stall condition could outlive the roll.** `clearPodTerminationStalled`
  runs from the delete gate on every clean pass, and there is exactly one shape
  with no later gate: the stall is on the **last** pod the roll replaces, the pod
  returns, everything is current, and the pass finalizes. The condition would then
  stand forever — the T6 permanent-drift class, introduced by the fix for T5. The
  clear now also runs in `clearRollingUpdateState`, next to `forgetWaitBounds` and
  for the same reason.

`handleStandaloneRollingUpdate` went to 16 against `gocyclo -over 15` when the
first version of the standalone guard landed; `standaloneWait` returning `nil` for
an available pod took it back under. `make cyclo` is green.

**Verification actually performed.**

* `make test-unit`, `make test-integration`, `make lint`, `make cyclo`,
  `make gosec`: green. `make generate-all` leaves the tree clean — the new
  condition is a status condition, not a CRD schema field.
* **Mutation matrix** (each mutation applied alone, tree restored after):

  | Mutation | Killed by |
  |---|---|
  | `available()` degenerates to `readyCondition` | 6 tests, incl. `FindPromotionCandidate`, `VerifyNewMasterReady`, `HandleManualFailover_TerminatingReplicaIsRefusedOneLayerUp` |
  | the delete gate always allows | 7 tests, incl. both Finding 2/3 shapes and the 5-replica Sentinel shape |
  | the Sentinel scan counts terminating pods again | `ScanSentinelPods_...`, `SentinelRollingUpdate_CompletionWaitsOut...` |
  | no completion hold in `finalizeRollingUpdate` | `FinalizeRollingUpdate_HoldsCompletion...` |
  | the stall never defers | 3 tests, incl. the `reconcileWorkload` wiring test |
  | `waitForUnavailablePod` ignores `terminating` | the `reconcileWorkload` wiring test |
  | no stall clear in `clearRollingUpdateState` | `ClearRollingUpdateState_ClearsAStandingStallCondition` |

  Five tests initially survived their mutation **for the wrong reason** (an
  unanswered probe requeues too, so the assertion held either way) and were
  strengthened with a reachable fake Valkey or a healthy `InstanceChecker` until
  the mutation killed them. Two tests are killed only by a *double* mutation, and
  that is correct: `verifyReplacedReplicasSynced` and the 3-pod Sentinel quorum
  guard are each covered by two independent guards by design. Both were confirmed
  non-vacuous by applying both mutations together.
* **Not run: the e2e.** `TestE2E_RollingUpdate_NoSecondDeleteWhileAPodTerminates`
  ([`test/e2e/pod_termination_test.go`](../../../test/e2e/pod_termination_test.go))
  runs both topologies, samples the data tier every 250 ms for the whole roll and
  fails on any moment at which two pods carry a `DeletionTimestamp`, deletes an
  already-replaced **replica** mid-roll (never the master — a different scenario),
  and asserts the roll finishes, the data survives, `PodTerminationStalled` is not
  standing and no Warning was raised. It compiles (`go vet -tags e2e`) and has not
  been executed.

**Also corrected in place above:** the *Relevant code* table's row for
`countUpdatedPods` said `available()`, which contradicts E2 in the same document.
E2 is the current rule; the row now says `reachable()` and names E2.


T4 part (a) put a `DeletionTimestamp` guard one layer up, in the role-label
fallback (`labelClaimsMaster`,
[`rolling_update.go:1261`](../../../internal/controller/rolling_update.go#L1261)),
so a terminating pod is no longer counted as master by its own stale
`instanceRole` label. That guard **drops `isMaster` on such a pod, which adds it
to the `sortReplicaCandidates` set** — T4 made this path reachable on one more
shape without T5 having landed. The extra delete is the verified API no-op,
which is why T4 shipped without it.

~~Whichever lands first, the other belongs in the same change.~~
~~Fixing T5 also deduplicates the RollingUpdate events.~~ Both superseded: the
two guards are complements, and the Event series cache already collapses the
duplicates on a key that does not include the note (see *Corrections*).

T4 also established the constraint that shapes T5: ADR 0025 promises a clean
rolling update emits **zero** Warning Events, and `demoteRogueMaster` turns
`!ready` into `SplitBrainUnresolved`. That is why T5 may not simply redefine
`ready`.

## T6: Stale status surfaces

**Severity: mixed — one verified live defect, one documentation defect, one already
resolved in production, one decided-by-design. Status: analysed 2026-08-25, DECIDED and
IMPLEMENTED 2026-08-26 (T6a A1b + A5, T6b B1, T6c C1 + C2 + C6, T6d P1, cross-cutting X1,
plus T14 in the same change). T6b B2 was NOT taken; T15–T18 stay open.**

The original filing (2026-08-22) called the whole item "low, cosmetic". That verdict
survives for T6b and T6d and is **withdrawn for T6a**: `observerReady` is the record
ADR 0020 leans on when it refuses a foreign observer Deployment, and it cannot change
at all. T6c is **not a defect** — the code was already fixed in v1.11.0 and the audit
window closed 15 minutes before the cluster cleared it.

### Re-check on the live fleet (2026-08-25, ~06:55–07:20 UTC, read-only, wds18-k8s-main)

The window matters for one row: `valkey9-sentinal-tls` was read twice and changed
between the reads, which is what produced the measurement in T6a. The table is the
first read.

`kubectl get valkey -A` plus `kubectl get deploy -n database-examples`:

| CR | phase | ready | masterPod | `status.observerReady` | observer Deployment `readyReplicas` | verdict |
|---|---|---|---|---|---|---|
| valkey8 | OK | 3/3 | valkey8-0 | `false` | 1 | **wrong** |
| valkey8-sentinal | OK | 3/3 | valkey8-sentinal-0 | `false` | 1 | **wrong** |
| valkey8-sentinal-tls | OK | 3/3 | valkey8-sentinal-tls-0 | `false` | 1 | **wrong** |
| valkey8-tls | OK | 3/3 | valkey8-tls-0 | `false` | 1 | **wrong** |
| valkey9 | OK | 3/3 | valkey9-**1** | `false` | 1 | **wrong** |
| valkey9-sentinal | OK | 3/3 | valkey9-sentinal-1 | `false` | 1 | **wrong** |
| valkey9-sentinal-tls | Provisioning | 2/3 | valkey9-sentinal-tls-0 | `true` | *(absent)* | **wrong, other direction** |
| valkey9-tls | OK | 3/3 | valkey9-tls-0 | `true` | 1 | correct |
| gitlab-valkey | Error | 3/3 | gitlab-valkey-1 | *(unset)* | observer disabled | n/a |
| gpt-valkey, harbor-valkey, oauth2-valkey | OK | 3/3 | — | *(unset)* | observer disabled | n/a |

Six of the eight CRs with an observer report the wrong value, in both directions. Not
a sampling artefact: see T6a.

Conditions, same read:

| CR | condition | status/reason | lastTransitionTime |
|---|---|---|---|
| valkey9 | `TopologyRestored` | True / `Restored` — *"valkey9-0 was promoted back to master"* | 2026-08-22T21:33:51Z |
| valkey9 | `SidecarUpdatePending` | **False** / `SidecarUpToDate` | 2026-08-22T21:40:00Z |
| valkey9-tls | `SidecarUpdatePending` | **False** / `SidecarUpToDate` | 2026-08-22T22:15:00Z |
| valkey8 | `SidecarUpdatePending` | **False** / `SidecarUpToDate` | 2026-08-22T21:33:31Z |
| valkey8-tls | `SidecarUpdatePending` | **False** / `SidecarUpToDate` | 2026-08-22T21:33:28Z |
| gitlab-valkey | `Ready` | True / `HAClusterReady` | 2026-08-22T21:33:35Z |
| gitlab-valkey | `ReconcileBlocked` | True / `WriteFailed` | 2026-08-22T21:33:03Z |

---

### T6a: `status.observerReady` cannot trigger a status write — the change detector snapshots too late

**Severity: medium as filed. Status: DONE 2026-08-26** — shipped as A1b + A5 in commit
`75b3c92`, and the T14 ownership guard rode along in the same change. The
`Status: open` that stood here until 2026-08-26 was stale from the day the fix landed;
it contradicted this section's own parent header. Cause is structural, not carelessness:
this file is gitignored, so the fix commit could not carry it — see the index note at the
top of the file.

Verified on `HEAD` = `1c309d8` by re-reading the code, and `make test-unit` exits 0:

* [`valkey_controller.go:2387-2394`](../../../internal/controller/valkey_controller.go#L2387-L2394) —
  the `IsObserverEnabled()` block now sits inside `persistStatus`, immediately after
  `v.Status.OperatorVersion`, i.e. on the **far** side of the capture. The captures are at
  [`:2067`](../../../internal/controller/valkey_controller.go#L2067) (`updateStandaloneStatus`)
  and [`:2285`](../../../internal/controller/valkey_controller.go#L2285) (`updateHAStatus`),
  and `persistStatus` is the sole return of both — so `prev != curr` is now reachable.
* [`:2422`](../../../internal/controller/valkey_controller.go#L2422) — `statusUnchanged` still
  compares `ObserverReady`, which is now a real trigger rather than a field compared
  against itself.
* [`:2047-2050`](../../../internal/controller/valkey_controller.go#L2047-L2050) — the old
  prologue assignment is gone, replaced by a NOTE naming the hazard and ADR 0002 D5.
* `Status.ObserverReady` outside tests occurs at exactly two lines, `:2391` and `:2393`.
  No second writer re-opened the hole.
* Both directions are pinned:
  [`valkey_controller_test.go:2855`](../../../internal/controller/valkey_controller_test.go#L2855)
  `TestUpdateStatus_ObserverReadyTransitionIsPersistedOnItsOwn` and
  [`:2884`](../../../internal/controller/valkey_controller_test.go#L2884)
  `TestUpdateStatus_DisablingTheObserverClearsAStoredVerdict` — the enabled→disabled hole
  this section called "the second hole". The `if ObserverReady != nil` hedge is gone.
* ADR amended as this section demanded:
  [ADR 0002](../../adr/0002-surface-a-blocked-reconcile-on-the-cr.md) `:18` (amendment stamp),
  `:92-123` (D5 correction), `:403` (residual risk recording that `readyReplicas` is
  deliberately left as it is).

**Deliberately left out, and still true:** `ReadyReplicas` carries the same
compared-against-itself defect and was not fixed. ADR 0002 `:403` records that as an
accepted residual, and T18 reading 2 is where it would be closed.

**One residual this section owes and did not get:** it flagged *two* doc defects and only
one was fixed. `persistStatus`'s comment was rewritten and is correct
([`:2364-2377`](../../../internal/controller/valkey_controller.go#L2364-L2377)), but
`statusUnchanged`'s at
[`:2403-2405`](../../../internal/controller/valkey_controller.go#L2403-L2405) still lists
"phase, message, readyReplicas, masterPod, operatorVersion, and conditions" and omits
`observerReady`, which it compares at `:2422`. One word. Fold it into whoever next touches
the file; not worth its own item.

**Not verified:** nothing here says anything about wds18. HEAD is a branch, not a release —
see the cross-cutting note in the index. `status.observerReady` can still be wrong on the
fleet today.

`updateStatus` mutates two status fields **before** either sub-function captures the
baseline the change detector compares against:

- [`valkey_controller.go:2044`](../../../internal/controller/valkey_controller.go#L2044) — `v.Status.ReadyReplicas = readyReplicas`
- [`valkey_controller.go:2047-2052`](../../../internal/controller/valkey_controller.go#L2047-L2052) — `v.Status.ObserverReady = &observerReady`, or `nil` when the observer is disabled
- then [`:2066`](../../../internal/controller/valkey_controller.go#L2066) (`updateStandaloneStatus`) or [`:2278`](../../../internal/controller/valkey_controller.go#L2278) (`updateHAStatus`) does `prevStatus := v.Status.DeepCopy()`
- and [`statusUnchanged`](../../../internal/controller/valkey_controller.go#L2379) compares exactly those two fields at [`:2385`](../../../internal/controller/valkey_controller.go#L2385) and [`:2394`](../../../internal/controller/valkey_controller.go#L2394)

So `prev.ObserverReady == curr.ObserverReady` by construction. **A pass in which only
the observer's readiness changed writes nothing.** The value that gets persisted is
whatever the field happened to hold on the last pass that changed something *else* —
phase, message, masterPod, operatorVersion or a condition.

`ReadyReplicas` carries the identical defect and is saved only by accident: every
branch that changes the count also changes the phase message
([`:2091`](../../../internal/controller/valkey_controller.go#L2091),
[`:2098`](../../../internal/controller/valkey_controller.go#L2098),
[`:2301`](../../../internal/controller/valkey_controller.go#L2301)) or the `Ready`
condition, so it rides along as a passenger. That coupling is not an invariant — it is
a property of the current message strings.

**Why the observer is the field that actually gets stuck.** Nothing else in the status
mentions it, so it has no passenger seat. On top of that its readiness probe is
`PeriodSeconds: 2, FailureThreshold: 1`
([`internal/builder/observer.go:95-104`](../../../internal/builder/observer.go#L95-L104)),
so a single failed `/readyz` flips the Deployment to zero ready replicas — and in
`database-examples` the Chaos Mesh schedule kills a vko-managed pod every 5 minutes
(see the memory note and T8), so every cluster eventually snapshots a `false` and keeps
it.

**Measured to the second, and both directions on one CR
(`valkey9-sentinal-tls`, live 2026-08-25):**

| Time (UTC) | What happened | `status.observerReady` |
|---|---|---|
| before `07:05:07` | CR is `Provisioning`, `readyReplicas 2/3`; observer Deployment not Available | **`true`** — frozen from an earlier healthy pass |
| `07:05:07` | recovery pass: phase → `OK`, `readyReplicas` → 3, `Ready` → True. Status write lands (`Ready.lastTransitionTime = 07:05:07Z`). The observer is **not yet ready at this instant** | **`false`** — persisted |
| `07:05:10` | observer Deployment goes `Available` / `readyReplicas: 1` (`Available.lastTransitionTime = 07:05:10Z`) | still `false` |
| `07:19:46` | observer Deployment still `readyReplicas: 1`, phase still `OK`, nothing else changed | still **`false`** |

Three seconds of real lag became a permanent wrong value, because the recovery is the
last thing that changed anything else in the status. The same CR shows the stale `true`
and the stale `false` inside fifteen minutes — so this is not a one-directional
rounding error, it is the field being unable to move on its own.

**Second instance, longer (valkey9):** last status-subresource write
`2026-08-25T04:15:07Z` (`managedFields`, `manager=manager`, `subresource=status`, field
set includes `f:observerReady`); observer pod ready since `05:20:01Z`; `observerReady`
still `false` three hours later, while the same read shows `masterPod: valkey9-1` and
phase `OK` — so the pass is running and converging and only this field is frozen.

**Verified, not assumed:** the watch is not the problem.
[`Owns(&appsv1.Deployment{})`](../../../internal/controller/valkey_controller.go#L2727) is
wired, so an observer readiness flip *does* enqueue a reconcile. The pass runs and the
write is dropped.

**Second hole in the same lines.** `v.Status.ObserverReady = nil` on the
observer-disabled branch ([`:2051`](../../../internal/controller/valkey_controller.go#L2051))
is subject to the same comparison, so **disabling the observer never clears a stored
`true`**. `TestReconcile_ObserverStatus_NilWhenDisabled`
([`valkey_controller_test.go:2783`](../../../internal/controller/valkey_controller_test.go#L2783))
passes only because its fixture never held a `true`, and
`TestReconcile_ObserverStatus...` at
[`:2778`](../../../internal/controller/valkey_controller_test.go#L2778) asserts the false
direction under an `if updated.Status.ObserverReady != nil` — the hedge that let the
defect ship.

**The sharpest evidence is four lines above the skip that breaks it.**
`persistStatus`'s own doc comment
([`:2354-2359`](../../../internal/controller/valkey_controller.go#L2354-L2359)) says:
*"Everything else — `readyReplicas`, `masterPod`, `observerReady`, `conditions` — keeps
updating: a rejected managed write says nothing about the running data plane."* The
skip at [`:2369-2370`](../../../internal/controller/valkey_controller.go#L2369-L2370) makes
that false for two of the four. `statusUnchanged`'s doc comment
([`:2376-2378`](../../../internal/controller/valkey_controller.go#L2376-L2378)) then lists
*"phase, message, readyReplicas, masterPod, operatorVersion, and conditions"* and omits
`observerReady` entirely, although the function compares it — a second reason the defect
was invisible to readers of the code.

**And the neighbouring field documents the exact hazard, fixed for itself only.**
[`:2039-2041`](../../../internal/controller/valkey_controller.go#L2039-L2041): *"NOTE:
`v.Status.OperatorVersion` is set inside the sub-functions (after prevStatus capture) so
that a version change is detected by `statusUnchanged`."* `OperatorVersion` and
`MasterPod` are set after the capture and are therefore detected; `ReadyReplicas` and
`ObserverReady` are the only two set before it.

**Why the stuck value is usually `false`.** The observer's `/readyz` returns 503 whenever
its **last cluster health cycle** said not-ready
([`internal/observer/server.go`](../../../internal/observer/server.go),
[`observer.go:374-385`](../../../internal/observer/observer.go#L374-L385)), polled every 2 s
([`internal/builder/observer.go:158`](../../../internal/builder/observer.go#L158)) against a
probe with `FailureThreshold: 1`. So the observer is NotReady exactly while the cluster
is disturbed — and `updateStatus` is skipped for the whole data roll and the whole
Sentinel roll (T18), so the first pass that reaches `persistStatus` again is the recovery
pass, sampled while the observer is still catching up. That is precisely the
`07:05:07` / `07:05:10` pair measured above. Stated as the mechanism of the measured
instance; as a *general* bias it is plausible, not proven — which write persisted the
`false` on the other five clusters cannot be determined from the code.

**Why this is not cosmetic.** Two ADR decisions rest on this field:

- **ADR 0002 D5** states as fact: *"`persistStatus` restores the previous phase and
  message while blocked, but `readyReplicas`, `masterPod`, `observerReady` and the
  `Ready` condition keep updating."* Verified false for `observerReady` (never) and
  for `readyReplicas` (only as a passenger); true for `masterPod` and `Ready`, both of
  which are set after the capture. The ADR must be amended in the same change.
- **ADR 0020** decided that a foreign observer Deployment is *refused without failing
  the step* because *"the observer is diagnostic, the CR does its job without it, and
  `status.observerReady` records the degradation"*
  ([0020, Consequences](../../adr/0020-write-only-what-the-operator-owns.md)). That
  record cannot change — and see **T14**, where the same field reads a stranger's
  Deployment in the first place.

**Existing test coverage of the true direction: none.** `statusUnchanged` has a
dedicated unit test
([`valkey_controller_test.go:851-883`](../../../internal/controller/valkey_controller_test.go#L851-L883))
that checks each field in isolation and therefore cannot see the capture-order defect.

---

### T6b: `TopologyRestored` is the verdict of one rolling update — the type comment claims otherwise

**Severity: low. Status: DONE 2026-08-26** — shipped as B1. B2 was deliberately **not**
taken and is now a recorded refusal rather than a pending item. The `Status: open` that
stood here until 2026-08-26 was stale; same cause as T6a.

Verified on `HEAD` = `1c309d8`:

* [`api/v1/valkey_types.go:58-74`](../../../api/v1/valkey_types.go#L58-L74) — the type comment
  no longer claims liveness. It now reads "a one-shot verdict about that update, not a live
  statement about the topology now (ADR 0010 D15) … `status.masterPod` is the live answer".
  The sentence this section quoted — "the only durable record that the topology differs from
  the canonical one" — is gone.
* [`valkey_controller.go:2136-2140`](../../../internal/controller/valkey_controller.go#L2136-L2140) —
  the `currentMasterPod` prose states the direction of the reading explicitly.
* [`README.md:946`](../../../README.md#L946) — the condition row says the same in bold, naming
  the "True next to a non-pod-0 master indefinitely" case.
* [`condition_registry.go:162-174`](../../../internal/controller/condition_registry.go#L162-L174) —
  `TopologyRestored` is declared `conditionHistory` with `clearSite: ""` and the reason
  inline, `loadBearingField: "Message"`.

**B2 (clear the condition on class exit) is recorded as intentionally absent, not
forgotten** — that is what the registry row encodes. The permanent-drift channel this
section paired it with is its sibling **T15**, which is still open and is where that work
actually lives.

Exactly two writers, both inside the data-tier rolling update of a multi-replica
non-Sentinel cluster, both through `recordTopologyRestoredCondition`
([`rolling_update.go:3625`](../../../internal/controller/rolling_update.go#L3625)):

- `promotePod0AndRedirect` → True / `Restored` / `"%s was promoted back to master"`
  ([`:3727`](../../../internal/controller/rolling_update.go#L3727))
- `abandonTopologyRestoration` → False / `RestoreTimeout`
  ([`:3591`](../../../internal/controller/rolling_update.go#L3591))

**Nothing outside a rolling update rewrites or clears it**, and
`meta.RemoveStatusCondition` is called nowhere in the tree — verified by grep. So the
valkey9 message is a *true statement about the rolling update of 21:33:51*; the later
chaos-kill drain adoption at 21:35 is a different event on a path
(`checkSteadyStateSplitBrain`, ADR 0011) that deliberately does not touch it.

**The defect is that two documents disagree about what the condition means:**

| Source | Says |
|---|---|
| [ADR 0010 D15](../../adr/0010-every-rolling-update-wait-is-bounded.md) | a one-shot record, *"not a steady-state report that the next pass recomputes"* |
| [README.md:942](../../../README.md) | *"The **last** multi-replica rolling update handed the master role back to pod-0"* |
| [ADR 0011, Context](../../adr/0011-evidence-based-steady-state-split-brain-resolution.md) | already books *"`TopologyRestored` possibly still `True`"* as an accepted trace-gap |
| [`api/v1/valkey_types.go:43-45`](../../../api/v1/valkey_types.go#L43-L45) | *"the condition is the only durable record that the topology differs from the canonical one"* — a **live** claim no writer supports |
| [`valkey_controller.go:2126-2133`](../../../internal/controller/valkey_controller.go#L2126-L2133) | *"the status field was lying exactly where the condition next to it was trying to tell the truth"* — reads the same way |

Three of five say history, two say liveness. The wds18 observation is what a reader of
the last two expects to be a bug. **The honest fix is the sentence, not the code.**

**The half that *may* be a defect — and it hangs on the same question.** A CR that leaves the multi-replica-non-Sentinel class
— `spec.sentinel.enabled: true`, or scaling to `replicas: 1` — routes every later pass
to `handleRollingUpdate` or `handleStandaloneRollingUpdate`
([`rolling_update.go:268-275`](../../../internal/controller/rolling_update.go#L268-L275))
and never reaches a writer again. `TopologyRestored` then freezes for the life of the
cluster with no path back. That is precisely the class the code already names in so
many words at
[`valkey_controller.go:403-409`](../../../internal/controller/valkey_controller.go#L403-L409):
*"Disabling Sentinel mid-roll skips the check above forever, which would leave a
standing `SentinelUpdatePending=True` as permanent drift (the T6 class)."* The remedy
already exists as `clearSentinelUpdatePending` — a presence-guarded clear on class
exit.

**But the precedent does not transfer for free.** `clearSidecarUpdatePending` and
`clearSentinelUpdatePending` exist because those conditions report **deferred work**,
which ADR 0002 D10 requires to be clearable from the converged state. `TopologyRestored`
reports a **completed verdict**. Under the historical reading this analysis endorses,
a verdict about the last data-tier roll legitimately survives the CR gaining Sentinel —
so the freeze is only a defect if the condition is meant to be live, which is exactly
the question B1 answers. **Extending D10 from deferred work to verdicts is a new
decision and has to be written as one.**

**And a clear here destroys information.** Writing `False`/`TopologyNotApplicable` over
a standing `False`/`RestoreTimeout` erases the abandon record that ADR 0010 D15 calls
load-bearing (*"loses the verdict for the life of the cluster"*) and that
`TestAbandonTopologyRestoration_PermanentFailureStillEscapes`
([`topology_restore_stall_test.go:459-474`](../../../internal/controller/topology_restore_stall_test.go#L459-L474))
accepts only when nothing else is possible. Any clear must carry the prior verdict
forward in its reason or message.

**Adjacent, verified, small:** `verifyTopologyRestored` emits the
`RollingUpdateComplete` Event with the text *"Multi-replica rolling update completed,
topology restored"* unconditionally
([`rolling_update.go:3850-3852`](../../../internal/controller/rolling_update.go#L3850-L3852)),
including on the path that just recorded `TopologyRestored=False`. Filed as **T17**.

---

### T6c: `SidecarUpdatePending` — already fixed in v1.11.0; the audit window closed before the cluster caught up

**Severity: none as filed. The code is correct; the README documents the pre-fix
behaviour. Status: the original claim is withdrawn, see below.**

**Correction to the original T6 text.** It said the condition *"survived a completed
1.11.0 rollout that replaced every pod"*. Verified live on 2026-08-25: it did not.
All four affected clusters carry `SidecarUpdatePending=False / SidecarUpToDate`, and
the timestamps show the clear firing *after* the audit read its snapshot:

| CR | `TopologyRestored` stamp | `SidecarUpdatePending=False` stamp | lag |
|---|---|---|---|
| valkey8-tls | 21:33:27Z | 21:33:28Z | 1 s |
| valkey8 | 21:33:28Z | 21:33:31Z | 3 s |
| valkey9 | 21:33:51Z | 21:40:00Z | 6 min 9 s |
| valkey9-tls | 21:33:51Z | **22:15:00Z** | **41 min 9 s** |

The audit read the fleet between ~21:45 and ~22:00 — after valkey9 cleared, before
valkey9-tls did.

**Where the March `True` came from** (and why the message says "Standalone pod" on a
3-replica cluster): the `isTrueStandalone` guard and `handleMultiReplicaRollingUpdate`
were both introduced by commit `3f0a1fe` on 2026-03-20. Before it, the dispatch was
*"if Sentinel … else standalone"*, so **every** non-Sentinel cluster went through
`handleStandaloneRollingUpdate` and got the standalone message.
`git show v1.5.0:…/rolling_update.go | grep -c isTrueStandalone` = 0, v1.5.1 = 2, both
tagged the same day. It is a legacy value, not a scale-up artefact — corroborated by
all four non-Sentinel clusters carrying it and no Sentinel cluster doing so.

**The key question in the original text is answered NO.** The clear is *not* on the
standalone path: `clearSidecarUpdatePending` is called at
[`rolling_update.go:259`](../../../internal/controller/rolling_update.go#L259), inside
`checkAndHandleRollingUpdate` and **before** the topology switch at `:268-275`. It is
topology-blind, it shipped in v1.11.0 (`744b589`, `merge-base --is-ancestor` = yes),
and it fired on exactly these clusters.

**What remains, and it is real: a latency, not a stall.** The clear needs a pass that
(a) enters `checkAndHandleRollingUpdate`, (b) finds no pod needing update and (c) finds
no rolling-update state annotation. The pass that *completes* a roll satisfies none of
that combination, and completion clears only the state annotation
([`:289-293`](../../../internal/controller/rolling_update.go#L289-L293)) — never the
condition. The completing pass also schedules no follow-up: the healthy path returns a
zero requeue, the CR watch is `GenerationChangedPredicate`-gated
([`:2725`](../../../internal/controller/valkey_controller.go#L2725)), there is no Pod
watch and no `SyncPeriod` override. The guaranteed next pass is the controller-runtime
cache resync of an owned object (~10 h). On the wds18 fleet the chaos schedule
supplied that event within 3 s to 41 min; on a quiet cluster the resync is the
realistic bound.

**Also stale, and it is what turned a 41-minute lag into a reported permanent stall:**
[README.md:943](../../../README.md) still says *"A clearing branch exists but is not
reached when the drift resolves … so a `True` can outlive it — confirm the running
image rather than this field."* That describes the code before `744b589`; the comment
correcting it was committed 25 seconds later.

---

### T6d: `Ready=True` beside `phase=Error` is ADR 0002 operating as designed

**Severity: none as a defect; it was a re-decision request. Status: DONE 2026-08-26** —
answered P1: document the contract, do not change the surface. It never was a code defect,
which is why "open" read wrong here for a day; same stale-line cause as T6a.

Verified on `HEAD` = `1c309d8`:

* [`api/v1/valkey_types.go:28-45`](../../../api/v1/valkey_types.go#L28-L45) — `conditionTypeReady`
  is no longer an unexported constant in `internal/controller` (grep for it in `internal/`
  and `api/` returns nothing). It is `vkov1.ConditionTypeReady` and carries the contract,
  including "Ready=True next to phase=Error means: your cluster is serving, and the operator
  cannot write something".
* All nine controller write sites use `vkov1.ConditionTypeReady`
  ([`valkey_controller.go`](../../../internal/controller/valkey_controller.go) `:2077`, `:2090`,
  `:2102`, `:2113`, `:2299`, `:2312`, `:2325`, `:2338`, `:2350`).
* [`README.md:944`](../../../README.md#L944) (the condition row this section said was missing)
  and [`:965`](../../../README.md#L965) (the `Error` phase row, "Covers two different things").
* [`status_phase_test.go:243`](../../../internal/controller/status_phase_test.go#L243)
  `TestUpdateHAStatus_KeepsReadyTrueWhileBlocked` pins the shape no tier reached before.
* `CLAUDE.md` carries the same pair in its own Status section, which had made the same
  half-true promise.
* [ADR 0002](../../adr/0002-surface-a-blocked-reconcile-on-the-cr.md) D5a.

**P2 was weighed and parked**, in ADR 0002's Alternatives, as the option to revisit if this
surface is misread a third time.

Both verdicts come out of the **same** switch branch of `updateHAStatus`
([`:2281-2327`](../../../internal/controller/valkey_controller.go#L2281-L2327)) — the
cluster is healthy — and then half of it is overwritten by an unrelated authority:
`persistStatus` restores phase and message while blocked
([`:2362-2365`](../../../internal/controller/valkey_controller.go#L2362-L2365)) and
`Reconcile` stamps the pass's single phase write
([`:281`](../../../internal/controller/valkey_controller.go#L281)). The condition slice is
untouched by design.

This is not a stale condition. It is `status.phase` carrying two meanings — the
data-plane verdict and the operator's ability to converge the spec — with the second
one winning while blocked. `Ready` carries only the first.

ADR 0002 decided this three times over:

- **D3** — the blocked pass has exactly one phase authority and it writes last.
- **D5** — suppression covers the phase only; `Ready` keeps updating, because *"a
  rejected managed write says nothing about the running data plane; freezing the whole
  status would hide real cluster state behind an unrelated admission failure."*
- **Consequences** — *"While blocked, the CR reports `Error` even when the data plane
  is perfectly healthy. The health verdict is still readable from the non-phase
  fields."*
- **Alternatives** — *"Let `updateStatus` own the phase"* was considered and refused.

The tension T6 names is real but internal to the ADR: **D12** promises the status reads
*"`OK` when the instance is healthy, otherwise a short description of the current
task"*, and D3 overrides it while blocked. `Ready=True` is the field D5 deliberately
kept; it is doing its job.

**Constraints on any re-decision, verified:**

- `phase` is a **free-form string** — no `+kubebuilder:validation:Enum` on
  `Status.Phase` ([`valkey_types.go:809`](../../../api/v1/valkey_types.go#L809)), and
  `Rolling Update 2/3` is already built with `fmt.Sprintf`. A new value costs no CRD
  change.
- `vko_valkey_status_phase` carries the phase as a **label**
  ([`collector.go:176-177`](../../../internal/metrics/collector.go#L176-L177)), so a
  changed value resets the `for: 30m` accumulation of `ValkeyPhaseNotOK` once, at
  upgrade.
- Per **ADR 0023 Consequences**, `ValkeySpecNotObserved` does **not** fire for a
  blocked CR (the condition is written with the current `observedGeneration`, so the
  generation pair stays closed). For the gitlab shape, `ValkeyPhaseNotOK` and
  `ValkeyReconcileBlocked` are therefore the *entire* shipped alert coverage — any
  option that silences the phase alert must name its replacement.
- Nothing in this repo gates on the `Ready` condition: no shipped alert, no chart
  template, no e2e. **Checked on wds18 too** (this was the open question that decides
  option P3): no Flux `Kustomization` managing a Valkey CR carries `healthChecks`;
  `oauth2-proxy-databases` and `valkey-operator` set `wait: true`, which drives kstatus
  — and this CRD has no top-level `status.observedGeneration` and no
  `Reconciling`/`Stalled` conditions, so kstatus judges every Valkey `Current`
  regardless of `Ready`. Verified for the CR listing; *not* verified against the
  kstatus source itself.
- `ValkeyStatus` has **no top-level `observedGeneration` field at all** — verified by
  grep over `api/v1/valkey_types.go` and absent on all eight live CRs. The
  `ObservedGeneration` ADR 0002 D9 is about is the per-condition
  `metav1.Condition.ObservedGeneration`. This is what makes the kstatus conclusion above
  hold, and it is also the cheapest thing to add if a GitOps consumer ever needs to gate
  on convergence — with the consequence that `wait: true` would then start blocking on a
  blocked CR.
- `conditionTypeReady` is an **unexported package constant** in
  [`valkey_controller.go:52-53`](../../../internal/controller/valkey_controller.go#L52-L53),
  not a type in `api/v1` — the one condition every CR carries is the one with no
  declared contract. It is also missing from the README condition table.

---

### Relevant code

| Site | Role |
|---|---|
[`valkey_controller.go:2044-2052`](../../../internal/controller/valkey_controller.go#L2044-L2052) | T6a: the two fields mutated before the baseline is captured |
[`valkey_controller.go:2066`](../../../internal/controller/valkey_controller.go#L2066), [`:2278`](../../../internal/controller/valkey_controller.go#L2278) | T6a: `prevStatus := v.Status.DeepCopy()`, too late |
[`valkey_controller.go:2361-2401`](../../../internal/controller/valkey_controller.go#L2361-L2401) | T6a/T6d: `persistStatus` + `statusUnchanged` |
[`valkey_controller.go:1999-2009`](../../../internal/controller/valkey_controller.go#L1999-L2009) | T6a/T14: `isObserverDeploymentReady`, no ownership guard |
[`rolling_update.go:3591`](../../../internal/controller/rolling_update.go#L3591), [`:3727`](../../../internal/controller/rolling_update.go#L3727) | T6b: the only two `TopologyRestored` writers |
[`rolling_update.go:268-275`](../../../internal/controller/rolling_update.go#L268-L275) | T6b/T6c/T15: the topology dispatch that decides which writers a CR ever reaches |
[`valkey_controller.go:403-409`](../../../internal/controller/valkey_controller.go#L403-L409) | T6b/T15: `clearSentinelUpdatePending` — the existing class-exit remedy, and the code that names "the T6 class" |
[`rolling_update.go:245-261`](../../../internal/controller/rolling_update.go#L245-L261) | T6c: the topology-blind clear, and the pass that must reach it |
[`rolling_update.go:2858-2911`](../../../internal/controller/rolling_update.go#L2858-L2911) | T6c: the only `SidecarUpdatePending=True` writer, standalone-only twice over |
[`valkey_controller.go:2540-2563`](../../../internal/controller/valkey_controller.go#L2540-L2563) | T6b/T6c: the presence-guarded clear pattern and its upgrade-neutrality argument |
[`valkey_controller.go:262-283`](../../../internal/controller/valkey_controller.go#L262-L283) | T6d: the blocked-pass phase authority |
[`internal/metrics/collector.go:173-205`](../../../internal/metrics/collector.go#L173-L205) | every condition becomes a `vko_valkey_status_condition` series; `observedGeneration` is the max over conditions |

### The condition-lifecycle inventory this analysis produced

Eleven condition types, four write styles, and **no condition is ever deleted** —
`meta.RemoveStatusCondition` appears nowhere in the tree, so the only lifecycle is
True↔False and the presence guard is the entire upgrade-neutrality story.

| Type | Kind | Clear site | Presence-guarded |
|---|---|---|---|
| `Ready` (unexported const) | level | recomputed in `updateStatus` | n/a |
| `ReconcileBlocked` | level | `reconcile_blocked.go:120` | yes |
| `SentinelPeersStale` | level | `valkey_controller.go:2201` | no |
| `StorageSpecNotApplied` | level, **two evaluators** | `volumeclaim_conflict.go:174` | yes — see **T16** |
| `SentinelUpdatePending` | level / state machine | `rolling_update.go:4111`, `valkey_controller.go:2571` | yes |
| `MultipleMasters` | level **with a deadline in its `LastTransitionTime`** | `split_brain_report.go:125` | yes — **must not be touched by any GC** (ADR 0025) |
| `SidecarUpdatePending` | edge | `valkey_controller.go:2558` | yes |
| `PodTerminationStalled` | edge | `rolling_update.go:1603` | yes |
| `RollingUpdatePaused` | edge | `rolling_update.go:661` — **Sentinel path only** | no — see **T15** |
| `TopologyRestored` | history | **none exists** | n/a |

Write styles, so a fix does not invent a fifth: **A** in-memory `meta.SetStatusCondition`
persisted by `persistStatus` (zero extra API calls; `Ready`, `SentinelPeersStale`);
**B** `setStatusCondition` (one Get + one Update, error swallowed — the steady-state
reporters); **C** `writeStatusCondition` directly, caller consumes `(changed, err)`
(`TopologyRestored`, the Sentinel completion Event); **D** the presence-guarded clear
wrapper.

---

### Options

Each sub-finding is decidable on its own. `[F]` marks the recommendation.

#### T6a — observerReady

- **A1b `[F]` — set the observer field where `OperatorVersion` is already set.** Move
  the `IsObserverEnabled()` block out of `updateStatus` and into `persistStatus`,
  directly beside `v.Status.OperatorVersion = r.OperatorVersion`
  ([`:2367-2368`](../../../internal/controller/valkey_controller.go#L2367-L2368)) — i.e.
  **after** the baseline capture, which is the idiom the NOTE at `:2039-2041` already
  documents for exactly this hazard. Verified complete: `persistStatus` has exactly two
  callers and each is the **sole `return`** of its function
  ([`:2120`](../../../internal/controller/valkey_controller.go#L2120),
  [`:2351`](../../../internal/controller/valkey_controller.go#L2351)), so every path that
  sets the field today still sets it, and the two early returns of `updateStatus` did
  not reach the block before either. ~7 lines, no signature change, no call-site change,
  and it leaves `ReadyReplicas` semantics untouched. Raises `persistStatus` complexity
  from ~2 to ~4, well inside the limit of 15.
- **A1 — capture the baseline before anything mutates it (the class version of A1b).** Move
  `prevStatus := v.Status.DeepCopy()` into `updateStatus`, immediately after the CR
  refresh at `:2034` (before `:2044`), and pass it into
  `updateStandaloneStatus`/`updateHAStatus` as a parameter, deleting their captures.
  Verified safe: nothing between `:2034` and `:2066` touches `Phase` or `Message`, so
  the blocked-pass restore keeps identical semantics, and `OperatorVersion` is still
  set after the capture inside `persistStatus`. Both sub-functions have no other
  caller. Fixes `ReadyReplicas` and the enabled→disabled hole in the same stroke.
  ~15 lines, one file. Upgrade-neutral. Verified no test breaks: no test calls
  `updateStatus`/`updateHAStatus`/`updateStandaloneStatus`/`persistStatus` directly, and
  the two encodings of ADR 0002 D8 — `TestReconcile_Idempotent_NoUnnecessaryStatusUpdates`
  ([`valkey_controller_test.go:798`](../../../internal/controller/valkey_controller_test.go#L798))
  and its HA twin at
  [`:822`](../../../internal/controller/valkey_controller_test.go#L822), both asserting
  `ResourceVersion` unchanged across two passes — survive only because neither enables
  the observer. **The extra scope over A1b is that it also makes `ReadyReplicas` a live
  trigger, for a defect that is currently unreachable.** Cost: one status write on each pass where the
  observer actually flips — and because the probe is 2 s/threshold 1, a genuinely
  flapping observer becomes a write generator. ADR 0002 D5 and its Consequences bullet
  must be amended (they state as verified something the code does not do).
- **A2 — compare against the stored object instead of a pass-local snapshot.** Drop
  the `prevStatus` parameter; have `persistStatus` do its own cache-served `Get` and
  diff `stored.Status` against `v.Status`. Kills the whole class rather than this
  instance. Larger diff; the fresh read does not reflect writes this same pass already
  made through `writePhase`/`writeStatusCondition`, so those passes gain a redundant
  write and a higher conflict rate — and `persistStatus` has no `RetryOnConflict`, so a
  conflict fails the pass. Needs a new ADR 0002 decision, and D8's write count
  re-verified.
- **A3 — make it a condition instead of a bare bool.** Route it through
  `writeStatusCondition`, which re-Gets and has its own working skip guard, so it
  structurally cannot stick; a Reason then says *which* check failed. Not
  upgrade-neutral (new condition on every observer cluster), needs CRD regeneration,
  and **needs a hold-down** or the 2 s probe turns it into a stream of status writes
  with `LastTransitionTime` churn — the opposite of ADR 0002 D8.
- **A4 — stop caching it: expose it as a collect-time metric only.** The ADR 0021
  pattern ("no gauge written from a reconcile pass") applied to a value that is a cache
  with no invalidation. Correct for alerting, wrong for the CR: ADR 0002 D12 and the
  CLAUDE.md contract want the state readable in Lens, and a Prometheus series is not.
  Removing a status field is a breaking CR change. Complement, not replacement.
- **A5 — test-only net.** The two unit tests (false→true persists; enabled→disabled
  clears) plus an e2e assertion. **Not an option on its own** — it ships a red test.
  It is the companion to whichever code option wins, and the reason the defect shipped
  is that the existing test asserts under `if != nil`. It must also extend
  `TestStatusUnchanged_DetectsChanges`
  ([`valkey_controller_test.go:852-882`](../../../internal/controller/valkey_controller_test.go#L852-L882)),
  which today exercises Phase, Message, ReadyReplicas and MasterPod only — so the
  `ObserverReady`, `OperatorVersion` and `Conditions` branches have no test at all.

#### T6b — TopologyRestored

- **B1 `[F]` — fix the semantics in the docs, leave the code.** Reword
  `api/v1/valkey_types.go:43-45` to stop claiming liveness: the condition is the verdict
  of the **last data-tier rolling update of a multi-replica non-Sentinel cluster**; a
  later steady-state adoption (ADR 0011) or no-master recovery moves the master without
  touching it; `status.masterPod` is the live answer. Mirror it into the README row,
  add the clarifying sentence to ADR 0010 D15 and a cross-ref from ADR 0002 D11. No
  code, no manifest regeneration (the comment sits on a Go const, not a CRD field).
- **B2 — clear it on class exit (only if B1 is decided the other way).** Presence-guarded clear when
  the CR is no longer multi-replica-non-Sentinel, exactly as
  `clearSentinelUpdatePending` already does at `valkey_controller.go:403-409`. This is
  the half that is a defect rather than a wording question: today the freeze is
  permanent with no path back.
- **B3 — rewrite it on adoption.** Hook the single funnel `adoptMaster`
  ([`steady_state_master.go:476`](../../../internal/controller/steady_state_master.go#L476)),
  which is on the same topology gate and already knows the previous master, the new one
  and the evidence. Presence-guarded, and through the swallowing `setStatusCondition`
  so it can never fail an adoption (ADR 0002 D7, ADR 0009). Makes the surface honest
  at the cost of turning a history record into a live one — i.e. it re-decides ADR
  0010 D15 rather than clarifying it. **And a blanket clear is backwards in the
  commonest case:** `buildReplicaAddrs` walks ordinals ascending, so *draining a
  non-pod-0 master promotes pod-0 whenever pod-0 is healthy*
  ([`steady_state_master.go:80-84`](../../../internal/controller/steady_state_master.go#L80-L84)),
  and `promotionEvidence` grants the drain stamp to pod-0 like any other pod
  ([`:282-284`](../../../internal/controller/steady_state_master.go#L282-L284)). Most drain
  adoptions therefore *restore* the canonical topology; B3 needs a pod-0 branch writing
  `True`, not one blanket `False`. It also expands ADR 0011, which currently states
  that this path deliberately reports nothing.
- **B4 — recompute it live in `updateStatus`.** Presence-guarded, in-memory (style A),
  free of API calls. Same re-decision as B3 plus it inherits `updateStatus`'s
  unreachability during a roll.
- **B5 — split into two conditions** (`TopologyRestored` history +
  `TopologyCanonical` live). Honest but adds a public condition type to every
  multi-replica non-Sentinel CR. Not upgrade-neutral, new ADR.

#### T6c — SidecarUpdatePending

- **C1 `[F]` — resolve the README contradiction (do this regardless of the rest).**
  Sharper than "stale": [README.md:102-105](../../../README.md) *already* states the
  current behaviour correctly, and has since 2026-08-21 09:23 (`92db23c`) — while the
  wrong row at README.md:943 was written 2 h 50 m earlier the same day (`a0ac61f`). The
  two statements have contradicted each other in the same file ever since, and the
  replacement wording is three paragraphs above the row that needs it. One table row.
- **C2 `[F]` — clear at the single completion point.** Add
  `r.clearSidecarUpdatePending(ctx, v)` to the `if result.Completed` block at
  [`rolling_update.go:289-293`](../../../internal/controller/rolling_update.go#L289-L293),
  next to `clearRollingUpdateState`, on the argument the comment there already makes:
  it is the one point every dispatch target reports completion. Justify it from
  `updatedCount == totalPods`
  ([`:2995`](../../../internal/controller/rolling_update.go#L2995),
  [`:498`](../../../internal/controller/rolling_update.go#L498)) rather than from
  `Completed` — two of the five completion sites are *stalled* completions inside
  `verifyTopologyRestored` ([`:3804`](../../../internal/controller/rolling_update.go#L3804),
  [`:3853`](../../../internal/controller/rolling_update.go#L3853)), and at `:3804` the pass
  could not read the pods a second time at all; both are still safe because both are
  entered only through that branch, which proved `!needsUpdate && reachable()` for every
  pod earlier in the same pass. **Place the call before `clearRollingUpdateState`** so an
  annotation-update error cannot skip it. ~2 lines; presence-guarded, so no CR gains a
  condition; breaks no existing test (all three sidecar tests reach `Completed=false`).
  Directly unit-testable without a fake Valkey server — `reachable()` is
  `ps.readyCondition`, a pure pod-object read
  ([`:1364-1366`](../../../internal/controller/rolling_update.go#L1364-L1366)), and
  `TestHandleMultiReplicaRollingUpdate_CleanPassEmitsNoWarning`
  ([`split_brain_report_test.go:328`](../../../internal/controller/split_brain_report_test.go#L328))
  already drives a 3-replica roll on a bare `newTestReconciler`. **ADR 0002 D10 must be
  amended in the same change** — it currently names `:259` as *the only* site that
  proves convergence.
- **C3 — clear inside the two finalizers** instead. Its only claimed advantage over C2
  was testability, and that advantage is not real (see C2). What remains is the
  per-handler duplication the `:277-288` consolidation comment exists to prevent, plus a
  third completion path outside both finalizers (`verifyTopologyRestored`, `:3804` and
  `:3853`) that C3 would silently miss. Strictly weaker than C2.
- **C4 — give the completing pass one guaranteed recheck** via
  `DeferredRequeueAfter`. Shortens the lag instead of removing the shape, costs one
  extra pass per completed roll fleet-wide, and buys nothing for an existing leftover.
- **C5 — accept and record.** Production is already clean and the `True` is
  unreachable for any cluster with more than one replica. Then C1 is mandatory and the
  accepted latency belongs in ADR 0002's Residual risks.
- **C6 — name the pod and the reason in the `True` message.** The word "Standalone" on
  a 3-replica cluster is what made a legacy value look like a contradiction. And the
  mismatch is **still reachable today**, so this is diagnosability, not cosmetics: the
  guard reads `v.Spec.Replicas <= 1`
  ([`:2858`](../../../internal/controller/rolling_update.go#L2858)) while the loop iterates
  `*currentSts.Spec.Replicas` ([`:2860`](../../../internal/controller/rolling_update.go#L2860)),
  and a structural volumeClaimTemplates conflict holds the whole StatefulSet write
  ([`valkey_controller.go:1243-1245`](../../../internal/controller/valkey_controller.go#L1243-L1245)),
  so a CR scaled 3 → 1 in that state writes "Standalone pod …" while three pods run.
  Existing tests assert Reason, not Message, so they stay green. One status write on
  upgrade, only where the condition already exists.
- **C8 — clear inside `clearRollingUpdateState`, next to `clearPodTerminationStalled`.**
  **Named and rejected**, because it is the option the next reviewer will propose: it is
  one call site covering all eight callers, but `:562` and `:1799`
  (`clearStaleRollingUpdateState`, `pauseRollingUpdate`) clear state *mid-roll* where
  convergence is not proven, so it would erase a `True` that is still accurate.
- **C7 — clear from `updateStatus`.** Explicitly considered and refused in ADR 0002's
  Alternatives. Re-opening needs an argument the ADR does not already answer; C2 gets
  the same result at the site that already proves convergence.

#### T6d — phase vs Ready precedence

- **P1 `[F]` — document the split; change no behaviour.** Move `conditionTypeReady`
  into `api/v1` as `ConditionTypeReady` with the doc comment that states the contract:
  `Ready` is the **data-plane** verdict; the operator's ability to converge the spec is
  `ReconcileBlocked` plus `status.phase`; on a blocked-but-healthy cluster the two
  disagree **by design**, and here is how to read the pair. Add the missing `Ready` row
  to the README condition table and one sentence to the phase table saying `Error`
  covers both a broken data plane and a healthy cluster the operator cannot converge —
  read `message` and `ReconcileBlocked` to tell them apart. Pin it by adding the
  `Ready` assertion to `TestUpdateStatus_KeepsNonPhaseFieldsWhileBlocked`. **Caveat:**
  that test uses a standalone CR, so it pins reason `AllReplicasReady`; the
  `HAClusterReady` shape this sub-finding is actually about needs its own case.
  **No tier reaches the observed shape today** — verified: the two admission-rejection
  e2e tests both end with `Ready=False` (`ReplicasNotReady` / `NoReplicasReady`), because
  in both the StatefulSet never reaches the all-ready branch. `Ready=True` beside
  `phase=Error` has no regression net at any tier.
- **P2 — add a distinct phase value `Blocked`.** Costs no CRD change (free-form
  string). `Error` then means only what the health path means by it, and the phase stops
  claiming the data plane is broken. Not label-neutral: every currently blocked cluster
  flips its `vko_valkey_status_phase` label once, resetting the `for: 30m` timer of an
  already-firing `ValkeyPhaseNotOK`, and any user silence pinned to `phase="Error"`
  stops matching. Does not fix the same contradiction on the four non-blocked `Error`
  phases. Amends ADR 0002 D3 and D12.
- **P3 — make `Ready` follow the phase (False/`ReconcileBlocked` while blocked).**
  **Rejected on the merits.** It is a direct reversal of ADR 0002 D5 and deletes the
  only field that says "your three-node HA cluster is fine" during a block — exactly
  gitlab-valkey, whose data plane was verified healthy while the block was a
  StatefulSet the operator refused to touch. One cost the option does not advertise:
  `Ready` also feeds `newestObservedGeneration`
  ([`collector.go:208-225`](../../../internal/metrics/collector.go#L208-L225), ADR 0021 D2),
  so changing *when* it is written moves `vko_valkey_status_observed_generation` and
  therefore `ValkeySpecNotObserved` — a second alert, not just the two golden tests.
- **P4 — make the phase health-only; `ReconcileBlocked` alone carries the block.**
  This is the alternative ADR 0002 refused verbatim. It would also collapse the shipped
  alert coverage for the gitlab shape to `ValkeyReconcileBlocked` alone, because per
  ADR 0023 `ValkeySpecNotObserved` does not fire for a blocked CR either. Would
  supersede ADR 0002, not amend it.
- **P5 — split the two meanings into two fields** (`status.phase` = data plane, plus a
  `converged`/`blockedReason` field with its own printer column). The most honest
  surface and the largest change: CRD field in four files, a printer column that
  changes `kubectl get valkey` for every user, a collector series, a new ADR.
- **P6 — `Ready` goes stale across a rolling update.** Independent of the precedence
  question, and **decided already**: ADR 0001 D4 states it in so many words — *"The
  rolling-update exits of `reconcileWorkload` still own their own returns: a pass with a
  rolling update in flight — blocked or not — returns before `updateStatus` and writes
  its phase itself."* So this is a re-decision of ADR 0001 D4, not an uncovered defect.
  The control flow is verified: `NeedsRequeue` is set on essentially every pass of an
  active roll, so `Ready` keeps its pre-roll value — normally `True/HAClusterReady` —
  for the whole roll, and `masterPod`, `readyReplicas` and `observerReady` freeze with
  it. Filed separately as **T18**.

#### Cross-cutting: how to stop the fourth instance of this class

The same shape has now been fixed three times at three different sites (ADR 0002 D10
for `SidecarUpdatePending`, ADR 0024 D6 for `SentinelUpdatePending`, ADR 0026 D5 for
`PodTerminationStalled`) and T15/T16 are the fourth and fifth instances, found by
building the inventory above rather than by an incident.

- **X1 `[F]` — a declared ownership registry plus a test.** One table in
  `internal/controller` declaring, per condition type: its owner site, whether it is a
  **level** (re-measured every pass), an **edge** (needs a presence-guarded clear) or
  **history** (never cleared), and whether its `LastTransitionTime` or `Reason` is
  load-bearing. A unit test asserts every type declared in `api/v1` plus
  `conditionTypeReady` appears exactly once, every level has exactly one evaluator, and
  every edge has a presence-guarded clear. **It would have caught all three verified
  defects here and the `Ready`-not-in-`api/v1` asymmetry.** ~150 lines, zero runtime
  behaviour change, so it cannot break a cluster; its failure mode is a stale table,
  which the test catches for the membership half but not the semantic half. This is the
  ADR 0014 idiom — a test instead of a convention. Needs one new ADR for the taxonomy.
- **X2 — a single `reconcileConditions` GC pass.** **Rejected.** It becomes a second
  authority beside each producer, contradicting ADR 0002's one-reporter design, ADR
  0025's Consequences (the condition is written at the resolver's call sites, never
  inside it) and ADR 0026 D5. It would have to hard-exclude `MultipleMasters` (a flip
  resets the 90 s deadline read at
  [`split_brain_report.go:156`](../../../internal/controller/split_brain_report.go#L156))
  and `TopologyRestored` (history), so the abstraction leaks on the first two entries.
- **X3 — extend the batched status pass (`pruneStaleConditions` inside
  `updateStatus`).** Zero extra API calls, which is its real appeal. But it inherits
  `updateStatus`'s unreachability (P6): a cluster whose roll requeues forever — the
  exact T6 shape — would never GC.

### Recommendation

**T6a → A1b + A5 (+ T14 in the same change). T6b → B1, with B2 as a conditional
follow-up. T6c → C1 + C2 (+ C6). T6d → P1. Cross-cutting → X1.**

The reasoning is one sentence: **three of the four sub-findings are documentation
defects and one is a lost write, so the cheap, upgrade-neutral fixes are the correct
ones — and the only structural investment worth making is the one that finds the next
instance rather than the one that fixes them all at once.**

Per sub-finding:

- **A1b over A1/A2/A3/A4** because the defect is a capture-order mistake, not a design
  mistake: the field list in `statusUnchanged` is right, the assignment sits three lines
  too early. A1b moves it to where the codebase already documents the correct side of
  the capture, in seven lines, with no signature change and no behaviour change to any
  other field — and it makes the code do what `persistStatus`'s own doc comment already
  claims it does. A1 is the same fix generalised, and its extra scope is to make
  `ReadyReplicas` a live trigger for a defect nobody can currently reach; take A1 only if
  the message-string coupling that masks `ReadyReplicas` is judged too fragile to keep.
  A2 is the real class fix and is tempting, but it trades a provable seven-line change
  for a `Get`-per-pass whose conflict path fails the pass with no `RetryOnConflict`, and
  it puts the two ADR 0002 D8 idempotency tests on cache timing — X1 covers the
  recurrence risk more cheaply. A3 is the right answer for a field that needs a *reason*
  and the wrong answer for one driven by a 2 s probe with `FailureThreshold: 1`.
- **B1 over B3/B4** because ADR 0010 D15, README:942 and ADR 0011 already decided that
  this condition is history — three sources against two. Making it live (B3/B4)
  re-decides a rule to fix a sentence, and B3 would additionally get the commonest
  drain adoption backwards. **B2 is recommended only conditionally**: if B1 confirms
  the historical reading, the class-exit freeze is *correct* and B2 should not ship; if
  the decision goes the other way, B2 is the code half and must carry the prior verdict
  forward rather than overwrite it. I would take B1 alone and note B2 as the follow-up
  the answer selects.
- **C1 + C2 over C3/C5/C8** because C5 accepts a stale surface for up to the ~10 h cache
  resync on any cluster that is not being chaos-tested, and C2 costs two lines at a site
  the code already argues is the right one, is unit-testable without a probe, and breaks
  nothing. C1 is unconditional and nearly free: the file already contains the correct
  sentence, and a README that documents a fixed bug as current is how a 41-minute lag
  got filed as a permanent stall. C6 is worth taking with them — cheap, and the
  "Standalone pod" wording is reachable on a multi-pod cluster today.
- **P1 over P2/P5** because the surface is not wrong, it is undocumented — and the
  failure mode of P0 (close it silently) is *recurrence*, not incorrectness: the same
  finding will be re-raised by the next person who looks at a blocked cluster. P2 is
  the option I would revisit if that happens again; it is genuinely cheap now that
  `phase` is verified free-form, and its only real cost is a one-time label transition.
  It is not first because it changes an observable value to fix a legibility problem
  that a doc comment fixes for free.
- **X1 over X2/X3** because the recurrence is the actual finding here. Three ADRs have
  each fixed one instance of "the clear sits behind the code path whose absence caused
  the staleness", and T15/T16 are two more found by inventory. A registry plus a test
  changes no behaviour, so it cannot regress a cluster, and it is the same move ADR 0014
  made for RBAC drift: encode the invariant as a test because the convention has already
  been missed.

**What this deliberately leaves out:** B5, P5 and A4 are the designs that would make
every surface here structurally honest, and all three cost a public CR surface change
for a class of problem that is currently costing trust, not availability. If T6d gets
re-raised a third time, P5 is the answer, not P2.

### Decision

**Decided 2026-08-26 (Hans): follow the recommendation. Implemented in the same change.**

| Sub-finding | Decision | What shipped |
|---|---|---|
| T6a | **A1b + A5** | The `observerReady` assignment moved out of `updateStatus`'s prologue into `persistStatus`, next to `v.Status.OperatorVersion` — the far side of the `prevStatus` capture, which is the side the NOTE in `updateStatus` already documented for exactly this hazard. `ReadyReplicas` deliberately left where it is (A1 not taken); the reason and its cost are recorded in ADR 0002's Residual risks. Tests: `TestUpdateStatus_ObserverReadyTransitionIsPersistedOnItsOwn`, `TestUpdateStatus_DisablingTheObserverClearsAStoredVerdict`, plus the three fields `TestStatusUnchanged_DetectsChanges` never covered (`observerReady`, `operatorVersion`, `conditions`) and the removal of the `if ObserverReady != nil` hedge that let this ship. |
| T6b | **B1 only. B2 explicitly NOT taken.** | The type comment on `ConditionTypeTopologyRestored` no longer claims liveness, the `currentMasterPod` prose now states the direction of the reading, the README row says the same, and ADR 0010 D15 carries the clarification. B2 (clear on class exit) was dropped on the argument the analysis itself made: under the historical reading B1 confirms, the freeze is *correct*, and extending ADR 0002 D10 from deferred work to a completed verdict is a different decision. ADR 0010 D15 records that, and records that any future clear must carry the prior verdict forward rather than overwrite it. B3/B4/B5 not taken. |
| T6c | **C1 + C2 + C6** | C1: the contradicting README row is gone, replaced with the correct behaviour (the right sentence had been three paragraphs above it since 2026-08-21 09:23). C2: `clearSidecarUpdatePending` now also runs in the `result.Completed` branch, **before** `clearRollingUpdateState`, justified from `updatedCount == totalPods` rather than from the `Completed` flag. C6: `setSidecarUpdatePendingCondition` takes the pod name as a parameter, so a pending state with no named pod is unrepresentable, and the message reports `spec.replicas` as the number the decision was made on. C8 (clear inside `clearRollingUpdateState`) is named and rejected in ADR 0002's Alternatives so the next reader does not propose it. |
| T6d | **P1** | `conditionTypeReady` moved from an unexported constant in `internal/controller` to `vkov1.ConditionTypeReady` in `api/v1`, carrying the contract: `Ready` is the data-plane verdict, `phase` also carries convergence, and on a blocked cluster they disagree by design. Stated in the README condition table, in the phase table (`Error` covers two different things), in CLAUDE.md's own Status section — which made the same half-true promise — and in ADR 0002 D5a. Pinned by the existing blocked-pass test plus a new `TestUpdateHAStatus_KeepsReadyTrueWhileBlocked`, because no tier reached the `HAClusterReady` shape the finding was reported on. P2 is weighed and parked in ADR 0002's Alternatives as the option to revisit if the surface is misread a third time. |
| Cross-cutting | **X1** | `conditionRegistry` + five guard tests ([`condition_registry.go`](../../../internal/controller/condition_registry.go), [`condition_registry_test.go`](../../../internal/controller/condition_registry_test.go)), and [ADR 0027](../../adr/0027-conditions-are-levels-edges-or-history.md) for the level/edge/history taxonomy. Zero runtime behaviour. X2 (central GC pass) and X3 (`pruneStaleConditions`) are rejected in the ADR with the reasons, so the attractive-but-dangerous option is closed rather than merely unchosen. |
| T14 | **Fixed in the same change** | `isObserverDeploymentReady` gained the `IsControlledBy` guard its two sibling readers already had. Cost more than the four lines predicted: the two positive subtests were passing against Deployments with no ownerReference at all, so the fixtures had to start declaring the ownership they were implicitly assuming. ADR 0020's read-path sweep is corrected in place — it had omitted Deployments. |

**Verification run before this was called done:** `make fmt`, `make vet`, `make lint`
(0 issues), `make cyclo` (all functions under 15), `make test-unit` (every package green).
Each new regression test was confirmed to **fail** against the pre-fix code before being
kept — the A1b pair against the old assignment order, the C2 test against a removed clear
call. **Not run:** `make test-integration` and `make test-e2e`.

**Deliberately left out**, and each is a decision rather than an omission: A1
(`ReadyReplicas`), B2, B3, B4, B5, C3, C4, C5, C7, C8, P2, P3, P4, P5, X2, X3. The
structural options that would make every surface here honest by construction — B5, P5, A3,
A4 — all cost a public CR surface change for a class of problem that costs trust, not
availability. ADR 0002's Alternatives names P2 as the next step if T6d recurs, and P5 as
the one after that.

### Reproducing the measurements

```bash
export KUBECONFIG=/Users/hfi/repos/business_onpremise/kubernetes_configs/wds18-k8s-main

# T6a: observerReady vs. the observer Deployment it claims to describe
kubectl get valkey -A -o custom-columns='NS:.metadata.namespace,NAME:.metadata.name,\
PHASE:.status.phase,OBS:.status.observerReady'
kubectl get deploy -n database-examples -o custom-columns='NAME:.metadata.name,\
READY:.status.readyReplicas'

# T6a: the last status write, and therefore how long the value has been frozen
kubectl get valkey valkey9 -n database-examples -o json | \
  jq '.metadata.managedFields[] | select(.subresource=="status") | {manager, time}'

# T6b/T6c: the condition timestamps in the table above
for n in valkey8 valkey8-tls valkey9 valkey9-tls; do
  echo "== $n"; kubectl get valkey $n -n database-examples \
    -o jsonpath='{range .status.conditions[*]}{.type}{"\t"}{.status}{"\t"}{.reason}{"\t"}{.lastTransitionTime}{"\t"}{.message}{"\n"}{end}'
done

# T6d: no Flux Kustomization gates on the Valkey CR
kubectl get kustomization -A -o json | \
  jq -r '.items[] | select(.metadata.name|test("valkey|database")) |
         "\(.metadata.name) healthChecks=\(.spec.healthChecks) wait=\(.spec.wait)"'
```

```bash
# T6c: where the March True came from, and that the clear shipped in v1.11.0
git show v1.5.0:internal/controller/rolling_update.go | grep -c isTrueStandalone   # 0
git show v1.5.1:internal/controller/rolling_update.go | grep -c isTrueStandalone   # 2
git show v1.11.0:internal/controller/rolling_update.go | grep -n clearSidecarUpdatePending
git merge-base --is-ancestor 744b589 v1.11.0 && echo "clear is in v1.11.0"
```


## T7: Master label not restored after an operator-external failover — `-rw` Service can go empty

**Severity: medium, but not for the reason recorded here. Status: DONE 2026-08-27** — what
survived the two re-scopes (the observability gap: an empty `-rw` is invisible on the CR) is
closed by the `RWServiceEmpty` level, ADR 0012 D12: `reportRWServiceEndpoints` runs between
the prevStatus capture and persistStatus of both status arms, reports a settled cluster
(all pods ready, no roll in flight) with no master-labeled pod, and clears presence-guarded
— no fleet gains the row from an upgrade, and the operator still never writes the label
(ADR 0012 D1). Registry row added, unit tests in `rw_service_report_test.go`, README
condition row written. The refuted history below stands unchanged. RE-SCOPED
2026-08-26 — the verification this item demanded was performed against `HEAD` = `1c309d8`
and **refuted the headline mechanism**. Read the "Re-scoped" subsection at the end of this
item before acting on anything above it: the labeler is a 1 s poll loop, the controller
writes no `instanceRole` label at all, no Sentinel failover writes a drain stamp, and the
e2e this item proposes already exists. What survives is one unproven edge plus a genuine
observability gap — an empty `-rw` Service is invisible on the CR.

Observed on gitlab (under 1.9.6, verified from managedFields): after the
sentinel-initiated failover of 2026-08-17 promoted pod-1, the operator
relabeled the recreated pod-2 to `replica` but never labeled pod-1 `master`.
Since then all three data pods carry `instanceRole=replica`, the `-rw` Service
(selector `instanceRole=master`) has zero endpoints, and the `-r` Service
contains the master. GitLab was unaffected only because its clients use
sentinel discovery.

Not verified: whether 1.11.0 has the same gap. Corrected 2026-08-23 (the
original sentence claimed the gitlab 1.11.0 pass "dies at the STS write before
any label reconciliation could run" — both halves are wrong): the pass does
NOT die at a failed STS write — `runReconcileSteps` continues past failing
steps and `reconcileWorkload` still runs on a blocked pass
(`valkey_controller.go:438-449`, `:270`, ADR 0001) — and the controller writes
no `instanceRole` labels at all; the pod's own sidecar labeler owns that label
(ADR 0012; grep found no controller-side write of `LabelInstanceRole`). So
gitlab's stale labels are not explained by the blocked pass, and the live
cluster still proves nothing about 1.11.0 — the relabel-after-external-failover
path, if it exists, lives in the sidecar, not the controller. The chaos-kill
adoption on valkey9 (1.11.0) DID end with correct labels — but that was the
drain-stamp path, not a sentinel-initiated failover without a drain stamp.

~~Proposed work: first reproduce under 1.11.0 (e2e: sentinel failover by killing
the master ungracefully — no preStop, no drain stamp — then assert the steady
state relabels the new master and `-rw` follows). If the gap exists, fix the
steady-state label reconciliation; if not, close this item with the e2e as the
regression net.~~

### Re-scoped 2026-08-26: the headline claim is refuted, and the e2e above is already written

The verification this item asked for was done by reading the code on `HEAD` = `1c309d8`.
**Three of its load-bearing sentences are wrong**, and the fix it proposes ("fix the
steady-state label reconciliation") would have been built against a mechanism that does not
exist. The superseded text is struck above rather than deleted, per the file rule.

1. **"No code path relabels the new master" — false.**
   [`internal/sidecar/labeler.go:94-111`](../../../internal/sidecar/labeler.go#L94-L111) is a
   `time.NewTicker` loop running at `--poll-interval=1s`
   ([`statefulset.go:819`](../../../internal/builder/statefulset.go#L819)) which patches the
   label whenever the role changes ([`:142-148`](../../../internal/sidecar/labeler.go#L142-L148)).
   It is not a startup-only or drain-only labeler.
2. **"The operator relabeled the recreated pod-2 to `replica`" — impossible as written.**
   The controller never *writes* `instanceRole`; it only reads it
   ([`rolling_update.go:1556`](../../../internal/controller/rolling_update.go#L1556),
   [`common/labels.go:114-119`](../../../internal/common/labels.go#L114-L119)) and the pod
   template does not carry it ([`common/labels.go:71-73`](../../../internal/common/labels.go#L71-L73)).
   A recreated pod starts **unlabeled** and receives `replica` from its own sidecar. The
   original 2026-08-22 reading of managedFields was misattributed.
3. **The drain-stamp distinction this item leans on does not separate its two cases.**
   `stampPromotion` is **non-Sentinel only**
   ([`internal/sidecar/drain.go:187-196`](../../../internal/sidecar/drain.go#L187-L196)), so
   **no** Sentinel failover writes a stamp — graceful or ungraceful. The sentence "that was
   the drain-stamp path, not a sentinel-initiated failover" therefore does not distinguish
   the chaos-kill success from the gitlab observation.
4. **The regression net already exists.**
   [`test/e2e/sidecar_test.go:375-419`](../../../test/e2e/sidecar_test.go#L375-L419) already
   proves `-rw` follows a Sentinel failover. Do not write the proposed e2e a second time.

> **Superseded 2026-08-26 by a live measurement: the sentinel-0 edge below is NOT what is
> happening on gitlab.** The read-only inspection of that cluster found the sidecar labeler
> unable to run at all — it fails once per second with
> `remote error: tls: expired certificate` on all three data pods, because the sidecar pins
> its TLS client certificate at process start and those processes predate the 2026-08-23
> cert-manager rotation. That is **[T21](#t21-the-sidecar-pins-its-tls-client-certificate-for-the-pod-lifetime--a-cert-manager-rotation-silently-breaks-the-labeler-and-the-drain-promotion)**,
> and it is a sufficient explanation for the empty `-rw` on this cluster.
>
> gitlab is the **only** cluster still running 1.9.6 sidecars, i.e. the only one whose pods
> were not rolled by the 1.11.x upgrade. All 11 others have exactly one pod labeled `master`
> and a populated `-rw`. **So T7 must not be generalised to 1.11.x from the gitlab evidence
> — the fleet contradicts it.**
>
> **Still not verified, and it is the reason this item does not simply close:** all three
> pods were already `replica` on **2026-08-22**, *before* the 2026-08-23 rotation. The
> container log buffer does not reach back that far, so something may have stuck the label
> first and the expiry layered on top. One cause is measured; a second is possible and
> unexcluded.
>
> **What survives as T7's own scope**, after T21 takes the mechanism half: the observability
> gap in the paragraph below — an empty `-rw` is invisible on the CR. That is unchanged by
> T21 and is now the more valuable half of this item.

> **The originating observation is RESOLVED as of 2026-08-26 ~09:30.** The T8 execution
> rolled all three gitlab data pods; `-rw` went from **zero endpoints to one** (the master),
> `-r` dropped the master and holds only the two replicas, and exactly one pod carries
> `instanceRole=master` again. So the concrete cluster state this item was filed from no
> longer exists, and it was cured by **restarting the sidecar**, not by any label
> reconciliation — which is the T21 diagnosis confirmed.
>
> **This item does not close with it.** Two things outlive the observation:
> 1. the observability gap below — an empty `-rw` is still invisible on the CR, and that is
>    now the whole substance of T7;
> 2. the unproven sentinel-0 edge, which nobody has produced and which the resolution says
>    nothing about either way.
>
> **Do not re-derive this item from gitlab.** The evidence is gone; anything further has to
> come from the code or from a reproduction.

**What actually survives, and it is narrower than the original item.** A permanently
disagreeing sentinel-0 can pin the label cross-check at
[`labeler.go:131-139`](../../../internal/sidecar/labeler.go#L131-L139), because
`GetMasterAddress` is first-answer-wins. That is unproven — nobody has produced it — and it
is the only mechanism left that could yield the observed gitlab state.

**And one thing that is not a mechanism question at all, which is the more alarming half:**
an empty `-rw` Service is **invisible on the CR**. `status.masterPod` is derived from an
`INFO` probe ([`internal/health/checker.go:107-115`](../../../internal/health/checker.go#L107-L115)),
not from the Service endpoints, so a cluster whose `-rw` has zero endpoints still reports a
master and stays `phase=OK`. No condition covers it. A cluster whose clients do **not** use
sentinel discovery would be hard-down with a green CR. GitLab was spared only because it
does use sentinel discovery.

**Revised work:** (a) add the endpoint-vs-label disagreement as an observable — it is the
part that would have made this a five-minute diagnosis instead of a fleet audit; (b) leave
the sentinel-0 edge open and unproven, recorded here, until something reproduces it. The
verification vehicle for both is the T8 maintenance window, which will produce exactly this
transition on a real cluster.

## T8: Cluster ops on wds18: gitlab-valkey remediation (not repo work)

**Status: DONE 2026-08-26 ~09:25-09:30 UTC.** Option (b) executed end to end: dataset backed
up, `spec.persistence.enabled: false` committed to the Flux source, the operator unblocked
and rolled all three data pods, and the cluster ended `phase=OK` with the dataset intact.
**The whole fleet is `OK` for the first time in four days.** Full record in "Execution" at
the end of this item, including the two things that did not go as this item predicted.

> **Re-measured read-only 2026-08-26 ~09:10 UTC; every claim below CONFIRMED, plus three
> facts the item did not have.**

> **Re-measurement, 2026-08-26.** Namespace is **`gitlab`**. All eight claims in the causal
> chain below verified exactly as written, including the `known-master` managedFields
> timestamp (`2026-04-16T05:26:55Z`) and the Flux `firstReconciled: 2026-04-25T09:51:08Z`
> that dates the persistence change. What the item did not have:
>
> **1. The dataset, which is the number that governs the whole window.**
> **27103 keys, ~27 MB, byte-identical on all three pods** (`DBSIZE` 27103/27103/27103,
> offsets 66283354974 on master and pod-2). pod-1 is master with 2 replicas `state=online,
> lag=0`; **223 of the 239 client connections are on pod-1**, via Sentinel discovery.
> Nuance the item gets wrong by omission: `dir /data` is an emptyDir but `save 900 1 300 10
> 60 10000` is **active** and `rdb_last_bgsave_status:ok` — so the data survives a
> *container* restart on the same node (which is why pods 0/1 kept their keys across the
> 2026-07-24 restart) but **not a reschedule**. Memory-only across node moves, not across
> everything.
>
> **2. The refusal is the PRE-T2 behaviour, and it is silent.** The deployed v1.11.1 does
> **not** contain the T2 fix — `git merge-base --is-ancestor 1275bf1 v1.11.1` is false and
> `git show v1.11.1:internal/controller/valkey_controller.go | grep -c
> guardVolumeClaimTemplates` returns 0. So the CR carries `ReconcileBlocked=True` with the
> catch-all reason **`WriteFailed`**, not `RecreateRequired`, and the message is the raw API
> error (`volumeMounts[1].name: Not found: "data"`). **Zero Events in the namespace, of any
> kind** — 892 refusals in 7h22m, 30 s apart, in complete silence. `kubectl describe` tells
> an operator nothing.
>
> **3. The pods cannot drain, for two independent reasons.** The 1.9.6 template has **no
> `lifecycle`/`preStop` block on any container** (measured), *and* the sidecar's drain client
> would fail with the expired certificate of
> **[T21](#t21-the-sidecar-pins-its-tls-client-certificate-for-the-pod-lifetime--a-cert-manager-rotation-silently-breaks-the-labeler-and-the-drain-promotion)**.
> Failover on pod deletion therefore rests **entirely on Sentinel** — which is healthy, all
> three agree on pod-1 with `quorum 2`, and would take it. But it is the only mechanism left,
> so it must be verified live before the second pod is touched, not assumed.
>
> Three smaller measured facts worth carrying into the window:
> * `status.observedGeneration` **does not exist** on any of the 12 CRs — it is not in the
>   deployed CRD schema at all. Do not use it as a convergence check.
> * The data tier runs **two different Valkey builds**: 8.1.8 on pods 0/1, **8.1.6** on
>   pod-2. Mutable tag `valkey/valkey:8.1` + `imagePullPolicy: IfNotPresent`; node omega had
>   an older layer cached.
> * The **replica ConfigMap and the sentinel monitor line both name pod-2** — the stale
>   authority. The running sentinels monitor pod-1 only because their init container's
>   `ROLE`-scan fallback rewrites the line at pod start. The persisted ConfigMap is wrong and
>   the correction is re-derived on every sentinel boot. Nothing durable holds the truth.
>
> **Flux source located:** `github.com/hans-fischer/wds18-apps-flux`, branch `main`,
> directory `apps/gitlab/valkey/`, via Kustomization `flux-system/gitlab-valkey`
> (`prune: true`, `interval: 1m`). Option (b) is a one-line edit there.
>
> **Rest of the fleet: all 11 other CRs are `OK`.** gitlab-valkey is the only blocked one.

Causal chain (all verified on cluster 2026-08-22):

1. 2026-03-18: STS created by 1.9.6 without persistence (`/data` = emptyDir,
   no volumeClaimTemplates).
2. 2026-04-25: Flux adds `spec.persistence` (rdb, 5Gi, local-storage) → CR
   generation 2. 1.9.6 never materialized it — zero PVCs exist in the
   namespace. The cluster has run without persistence ever since; the dataset
   lives in RAM + emptyDir only.
3. 2026-08-22: 1.11.0 renders persistence correctly → permanent STS write
   rejection (T2). Data pods never rolled to 1.11.0 (still on the 1.9.6
   template, ages 71d/71d/5d); the sentinel STS write succeeded and all 3
   sentinel pods were rolled.
4. Stale recorded authority: `vko.gtrfc.com/known-master` says pod-2 (last
   written 2026-04-16); 1.11.0 propagates it into the replica ConfigMap
   (`replicaof gitlab-valkey-2...`) and the sentinel ConfigMap monitor line.
   Reality and `status.masterPod` say pod-1. Dormant: becomes live only if all
   sentinels are simultaneously unreachable while a data pod initializes.
5. Data plane itself healthy: pod-1 single master, 2 replicas online,
   byte-identical offsets, all 3 fresh sentinels agree on pod-1, quorum 2.
   GitLab connects via sentinel discovery directly to pod-1 (233 connections
   verified); the empty `-rw` Service (T7) affects nothing observed.

Remediation options, in preference order:

- **(a) ~~Persistence is wanted: orphan-delete the STS and let the operator
  recreate it.~~ WITHDRAWN 2026-08-23 — measured to wedge and to lose the
  dataset.** The superseded claim was: "Verified in code: pods survive, the
  operator recreates the STS with VCTs, the StatefulSet controller adopts the
  orphaned pods, and the operator then performs the failover-aware pod-by-pod
  roll. Lossless for HA." Only the first half holds. Walking it end to end in
  Kind (T10, T11) showed the statefulset-controller wedges on the adopted pods
  and that clearing the wedge cost the whole dataset. It was a code reading, not
  a run — the run disagrees. Amended 2026-08-26: the dataset half no longer
  follows — the operator refuses that demotion since ADR 0028 — but the wedge
  does, and it still leaves the cluster short of pods with a split brain a human
  has to resolve. **Do not perform this on gitlab-valkey**, whose dataset is
  memory-only and therefore unrecoverable.
- **(b) The remaining option, and now the recommended one:** set
  `spec.persistence.enabled=false` in the Flux source to match reality; the
  reconcile clears ~~with zero pod restarts~~ and the CR leaves `RecreateRequired`
  immediately. If persistence is genuinely wanted afterwards, it is a rebuild:
  stand up a second cluster with persistence enabled, replicate or restore into
  it, and move the clients — not an in-place migration, until T10 is fixed (T11
  was, on 2026-08-26).
- After either: verify the operator heals the master label / `-rw` endpoints
  and the known-master annotation (feeds T7's verification), and consider a
  one-time `SENTINEL RESET` (T1 applies to gitlab's sentinels too:
  num-other-sentinels 4/3/2).

> **"Zero pod restarts" is wrong for this cluster — corrected 2026-08-26. Plan the window
> for a full roll.** The promise holds only when the operator's *rendering* of the pod
> template is otherwise unchanged. It is not: gitlab-valkey's persisted StatefulSet template
> was written by **1.9.6**, and the rolling update compares pods against the **persisted
> template**, not against the CR
> ([`rolling_update.go:208-215`](../../../internal/controller/rolling_update.go#L208-L215)).
> Several commits have changed `internal/builder/statefulset.go` since 1.9.6 — the drain
> `preStop` hook among them — so the moment the write is no longer refused, every one of
> those diffs lands at once and all three data pods are replaced.
>
> Consequences for the window, and they are the reason this is not a quiet config change:
> * Expect a **GitLab-visible failover**. Lossless by design at 3 replicas, but see below.
> * The pods being replaced **predate the drain `preStop` hook**, so the outgoing master
>   cannot run the drain path the current design assumes. Watch the promotion rather than
>   assuming it.
> * The dataset is **memory-only**. There is no disk copy to fall back on. Take a
>   `BGSAVE`-independent backup — a logical dump from a replica — before starting.
>
> **The same correction is owed in a tracked file:** [`README.md:884-886`](../../../README.md#L884-L886)
> makes the identical "zero pod restarts" promise and is the half a user actually reads.
> It is listed in the "Corrections owed in tracked files" table at the top of this file.

> **Ordering correction, 2026-08-26 — do the annotation FIRST.** This item lists the
> `known-master` repair under "after either", as a verification step. That is the wrong
> order and it is the one real hazard in the whole procedure.
>
> `vko.gtrfc.com/known-master` names **pod-2**; reality is **pod-1**. The annotation is
> deliberately excluded from the config hash
> ([`internal/builder/configmap.go`](../../../internal/builder/configmap.go)), so correcting it
> triggers **no** roll and costs nothing — it is free to do first and expensive to do last.
> If pod-2 restarts in a window where init Phase 1 finds no master with connected replicas,
> Phase 2 ([`internal/builder/statefulset.go`](../../../internal/builder/statefulset.go)) has it
> **self-claim master** — and on a memory-only cluster that is an *empty* master adopted as
> the authority. Note that the steady-state resolver has **no dataset veto** (T13): ADR 0028
> protects the roll path only.
>
> Revised order: **(1)** correct `known-master` to pod-1 and confirm the replica ConfigMap's
> `replicaof` follows it (a blocked CR still reconciles, at the 30 s rate-limiter cap);
> **(2)** then flip `spec.persistence.enabled=false` in Flux; **(3)** expect the full roll
> above; **(4)** verify master label, `-rw` endpoints, `known-master`, sentinel peer counts.
> Step 4 is also the verification vehicle for **T7**, and the roll's finalization runs
> `resetSentinelState`, which very likely does **T9**'s job for this cluster for free.

### Execution (2026-08-26, 09:20-09:30 UTC)

Performed with explicit authorisation, on `wds18-k8s-main`, with the Flux source in
`github.com/hans-fischer/wds18-apps-flux`.

**Step 0 - backup, because the dataset had no disk copy that survives a reschedule.**
`BGSAVE` on the **replica** `gitlab-valkey-0` (not the master, which carried 223 of 239
client connections), then `kubectl exec ... -- cat /data/dump.rdb`. Result: **7542354 bytes**,
RDB magic `REDIS0011`, ~27052 keys, SHA256 recorded. Stored outside the repo tree in
`tmp/gitlab-valkey-backup-20260826/` with a restore note (`tmp/` is gitignored,
`.gitignore:38`).

**Step 1 - correcting `known-master` by hand: ATTEMPTED, REVERTED BY FLUX, AND IT CANNOT WORK.**
This is the first of two predictions in this ticket that the execution falsified, and it is
the more useful one.

`kubectl annotate` set the annotation to pod-1 and the read-back confirmed it. Within 45 s it
was **gone entirely** - not overwritten, deleted - and the replica ConfigMap had fallen back
to the structural default `replicaof gitlab-valkey-0`. Cause, measured from `managedFields`:

```
--- kustomize-controller Apply 2026-08-26T09:23:30Z   top keys: ['f:metadata', 'f:spec']
```

**Flux pruned it**, on its 1-minute reconcile, because the Kustomization runs `prune: true`
and server-side apply and the annotation is not in the git source. The operator's own writes
survive because they carry field manager `manager`; a `kubectl annotate` write does not.

**Consequence, and it supersedes the ordering advice added to this item earlier today:
`known-master` cannot be corrected by hand on a Flux-managed CR.** Do not plan a maintenance
step around it. The two options are to let the operator record it (which is what happened -
see step 3) or to pin it in git, and pinning it in git is worse than the problem: it would
freeze the authority at one pod forever and fight the operator on every future failover.

**It also turned out not to be needed, and the reason is worth recording.** The deployed init
container resolves the master in three phases, read off the live StatefulSet: **Phase 1**
queries all three Sentinels with exponential backoff for up to 30 s; **Phase 2** falls back to
the `replicaof` line in the replica ConfigMap; **Phase 3** is the ordinal rule. The stale
ConfigMap is therefore only reachable if **no Sentinel answers for 30 s** during a pod boot.
The sentinels were healthy on three different nodes with `quorum 2`. So the hazard this item
calls "dormant" is dormant for a specific, now-verified reason rather than by assumption.

**Step 2 - the Flux change.** `apps/gitlab/valkey/valkey-ha.yml`, `persistence.enabled`
`true` -> `false`, with a comment recording why and that re-enabling is a rebuild. Commit
`2d66b96`, pushed to `main`. Flux applied it **~60 s later**; the CR moved to
`Rolling Update 0/3` in the same pass.

**Step 3 - the roll, which went exactly as the failover-aware design intends.**

| Phase | State |
|---|---|
| `Rolling Update 1/3` | pod-0 `Pending` - replicas replaced first |
| `Rolling Update 2/3` | pod-0 back on the 1.11.1 sidecar, still labeled `replica` |
| `Rolling Update 2/3` | pod-0 -> **`master`**, `-rw` endpoints **0 -> 1** |
| `Rolling Update 2/3` | pod-1, the outgoing master, replaced last |
| `Provisioning` -> **`OK`** | `masterPod=gitlab-valkey-0` |

Replacement order was pod-2, pod-0, pod-1: **the master went last**, as designed. The full
roll of all three pods is what this item predicted only after the "zero pod restarts"
correction added earlier today - that correction held.

**Result, measured after completion:**

| Check | Result |
|---|---|
| `DBSIZE` per pod | **27154 / 27153 / 27153** - the +/-1 is live traffic. **No data lost.** |
| Replication | both replicas `master_link_status:up`, offsets identical |
| Clients | 135 on the new master; GitLab reconnected through Sentinel discovery |
| `status.phase` | `OK`, `readyReplicas: 3` |
| `ReconcileBlocked` | **`False` / `ReconcileSucceeded`** - the block is gone |
| `known-master` | **`gitlab-valkey-0`, written by the operator itself** - correct, and it stuck |
| Both ConfigMaps | now name pod-0 |
| `-rw` / `-r` endpoints | `-rw` = 1 (the master), `-r` = 2 (the replicas, **master no longer among them**) |
| Sentinel consensus | all three: `flags=master`, pod-0, `num-slaves=2`, `quorum=2` |
| Fleet | **all 12 CRs `OK`** |

**Step 4 - T21 was repaired as a side effect, and that is now measured rather than
predicted.** Zero `expired certificate` lines on all three sidecars after the roll, against a
continuous once-per-second failure before it. The sidecar log even shows the Sentinel
cross-check working correctly during the transition - pod-0 reported `master` locally while
Sentinel still named pod-1, so it labeled itself `replica` until Sentinel caught up, then
`role changed {"from":"replica","to":"master"}`. See
[T21](#t21-the-sidecar-pins-its-tls-client-certificate-for-the-pod-lifetime--a-cert-manager-rotation-silently-breaks-the-labeler-and-the-drain-promotion).

**What was deliberately left out:** the sentinel peer tables. See the correction in T9 - the
prediction that this roll would clean them was wrong.

**Not verified:** whether GitLab saw an application-level error during the failover. Only the
Valkey side was observed; no GitLab logs were read and no user-facing check was performed.

---


## T9: Cluster ops on wds18: one-time sentinel peer reset (not repo work)

**Status: open — needs a maintenance window. Follows from the T1 decision. The
procedure below was rehearsed end to end in a Kind cluster on 2026-08-23.**

> **Gated on releasing this branch — added 2026-08-26.** The T1 fix is **not on the
> fleet**: `git merge-base --is-ancestor 1b1f6ed origin/main` returns non-zero, and
> `origin/main` = `81e1108` = `v1.11.1`. Two consequences for this procedure:
>
> 1. **Step 4 cannot be performed as written.** `SentinelPeersStale` does not exist in
>    v1.11.1, so "the condition must be absent or `False` on every CR" has no field to read.
>    Until the release lands, verification falls back to reading `SENTINEL master <monitor>`
>    on each sentinel by hand and checking `num-other-sentinels` directly.
> 2. **The reset is not durable yet.** Without the pinned `sentinel myid` the drift
>    re-accrues on every partial pod churn — and in `database-examples` that is continuous,
>    because Chaos Mesh kills a vko pod every 5 minutes. Resetting before the release buys
>    margin back on the 6 stable-namespace clusters and buys almost nothing in
>    `database-examples`.
>
> **Recommended order: release first, then run this.** If the failover margin is needed
> sooner, run it on the six non-chaos clusters and repeat for `database-examples` after the
> release. ~~Skip gitlab-valkey entirely if the T8 window has already rolled it — the roll's
> finalization runs `resetSentinelState` and does this job for free.~~

> **That last sentence was wrong, and the T8 execution disproved it the same day it was
> written. Corrected 2026-08-26 ~09:30 UTC.** The T8 remediation rolled all three
> gitlab-valkey **data** pods to completion, and the peer tables did **not** change:
>
> | Sentinel | `num-other-sentinels` before | after the roll |
> |---|---|---|
> | sentinel-0 | 4 | **4** |
> | sentinel-1 | 3 | **3** |
> | sentinel-2 | 2 | **2** |
>
> The mechanism is obvious in hindsight and is the thing to carry forward: **a data-tier roll
> does not restart the Sentinel pods.** gitlab's sentinel StatefulSet was already at 1.11.1
> and its pods were rolled on 2026-08-22; nothing in the T8 change touched them, so nothing
> rebuilt their tables. Whatever `resetSentinelState` does, it is not a substitute for
> `SENTINEL RESET` on an unchanged Sentinel tier.
>
> **T9 therefore still applies to gitlab-valkey in full**, exactly as it does to the other six.
> Do not skip it. The consensus itself is healthy — all three agree on pod-0 with
> `quorum 2` — so the precondition for running the procedure is met.

All 7 sentinel-enabled clusters carry stale peer entries (`num-other-sentinels`
4/3/2 instead of 2/2/2). The T1 fix prevents new drift but deliberately does not
clean up what exists. ~~Those entries clear either at the next sentinel-tier roll
or by this one-time step.~~ **Corrected 2026-08-28, measured live on wds18 after
the v1.12.0 rollout:** the FIRST sentinel roll after the identity pin does not
clean the tables — it recreates them. The rolls are sequential, so every new
sentinel except the last learns the still-running old random identities over the
hello channel, and those become ghosts the moment their pods are replaced: the
measured 4/3/2 pattern IS the roll order (sentinel-0 rolled first, collected
both successors old ids; sentinel-2 rolled last, clean at 2). Only a roll in a
fully pinned world — the second one — or this one-time reset clears them. New
drift no longer accrues (Chaos kills reuse the pinned ordinal identity), so the
ghost count is frozen until then. `SentinelPeersStale=True/StaleSentinelEntries`
stands on all 7 CRs since 2026-08-28 ~08:55, so step 4 of this procedure is
verifiable for the first time.

Per cluster, with the master verified healthy first
(`SENTINEL master <monitor>` must report `flags=master` on every sentinel):

1. `SENTINEL RESET <monitor-name>` on sentinel-0.
2. Wait until that sentinel reports `num-slaves` back at the replica count
   (~10 s measured) and `num-other-sentinels` at `replicas-1`.
3. Repeat for sentinel-1, then sentinel-2 — one at a time, never in parallel.

**Never run this while the master is unreachable.** Measured in the T1 harness:
peer and replica discovery both run through the master, so a reset with the
master down leaves the sentinel at `num-other-sentinels=0` / `num-slaves=0` with
no way back, and with quorum 2 it can then never call a failover.

Measured while rehearsing this (Kind, valkey 9.1.1, three sentinels at 3 known
peers each): `num-other-sentinels` drops to the correct value within ~3 s of the
reset, `num-slaves` is 0 for 3-9 s and then back at 2. That gap is the reason for
one at a time.

Verification after the step: the `SentinelPeersStale` condition (T1, Option 5)
must be absent or `False` on every CR. It is re-evaluated every 5 minutes while it
reads True, so allow one interval before concluding the reset did not take.

## T10: The orphan-delete recovery wedges the statefulset-controller when claims are added

**Severity: high (it breaks the recovery every other ADR points at). Status: what is ours
is DONE 2026-08-27; the wedge itself is upstream Kubernetes and stays.** (A) is closed by
ADR 0010 D16: `recreationWait` bounds the observation of the three waits — 2 min plain
requeue, then `PodRecreationStalled` plus `DeferredRequeueAfter`, per episode, no Event —
so the status surface and the steady-state split-brain check keep running inside a wedge.
(B) was **already discharged by T16 on 2026-08-26** (ADR 0023 cites ADR 0028 three times);
the re-verified note below claiming it open predates that commit and is superseded.
Measured 2026-08-23.

Found while verifying T2 end to end in Kind (Kubernetes 1.36). ADR 0020 D1 documents
`kubectl delete sts <name> --cascade=orphan` as the downtime-free recovery and marks
its load-bearing step — the statefulset-controller re-adopting the orphaned pods — as
"asserted from the API contract, reproduced nowhere in this repo". It has now been
run.

**Re-adoption itself works.** The orphaned pods were adopted by the recreated
StatefulSet, `ownerReferences[0].uid` matching the new object. That half of ADR 0020
D1 is confirmed and can stop being hedged.

**What follows does not.** With claim templates added, the controller then tries to
attach the new claim to each adopted pod, and a pod spec is immutable:

```
Update Pod persist-mig-0 in StatefulSet persist-mig failed error: Pod "persist-mig-0"
is invalid: spec: Forbidden: pod updates may not change fields other than
`spec.containers[*].image`, ...
```

with the rejected diff naming `+ "ClaimName": "data-persist-mig-0"`. The sync fails on
the **lowest** mismatching ordinal and returns, so no missing pod is created either.
Measured: 31 `FailedUpdate` events over five minutes, then rate-limited out; the
cluster sat at 2/3 pods, the third having been deleted by this operator's own rolling
update and now uncreatable. Deleting the adopted pods by hand, lowest ordinal first,
clears it — at the cost in T11. *(Amended 2026-08-26: that cost is no longer the
dataset. Since [ADR 0028](../../adr/0028-a-demotion-may-not-discard-the-only-dataset.md)
the operator refuses to demote the pod holding the keys toward the empty one that
returned, so the step ends in a visible split brain instead of an empty cluster. It
still needs a human, and the wedge itself is untouched.)*

**The asymmetry matters and is measured.** Disabling persistence through the same
procedure works and is lossless: no claim templates means nothing for
`storageMatches` to check, so the adopted pods are never candidates for the failing
update. Three new emptyDir-backed pods, `phase=OK`, dataset intact on all three.

Not verified: whether moving the master to the highest ordinal before the recreate
makes the enabling direction lossless (the pod deleted first would then be a replica).
Plausible from the mechanism, untested.

> **Re-verified 2026-08-26 on `HEAD` = `1c309d8`. All six "consequences already applied"
> below are genuinely in the tree.** What that leaves is narrower than this item's severity
> line suggests, and it is worth being honest about: **the wedge itself is upstream
> Kubernetes behaviour and cannot be fixed here.** Two things do remain, and neither is the
> wedge.
>
> **(A) Three unbounded waits on a pod that will never be created** —
> [`rolling_update.go:1874-1877`](../../../internal/controller/rolling_update.go#L1874-L1877),
> [`:2378-2381`](../../../internal/controller/rolling_update.go#L2378-L2381),
> [`:3103-3106`](../../../internal/controller/rolling_update.go#L3103-L3106). Inside the wedge
> the missing pod never arrives, so the pass ends on the wait every time: the whole CR status
> surface freezes and the steady-state split-brain check is suspended for as long as it
> lasts. **This is reachable without T10 at all** — anything that makes a pod uncreatable
> produces it — so it deserves its own item rather than living under a Kubernetes bug.
>
> **(B) [ADR 0023](../../adr/0023-volume-claim-templates-are-immutable.md) is stale, and it is a
> tracked file.** `:164-171` still states the recorded-master-wins demotion is "a defect in
> its own right and is not fixed here", and `:285-286` that it "needs its own ADR and its own
> fix". **ADR 0028 is that ADR.** `grep -c 0028` in ADR 0023 returns **0**, and
> `git show --stat 2051a34` confirms the ADR 0028 commit never touched it. This is the last
> place in the tree that hands a reader a worse risk picture than the README, it breaks the
> CLAUDE.md rule that a reader must never find the old rule stated as current, and **the
> commit that broke it is in PR #195 right now** — so it should be corrected before that
> merges, not after. XS.
>
> **Still not verified, unchanged since 2026-08-23:** whether moving the master to the
> highest ordinal before the recreate makes the enabling direction lossless. Plausible from
> the mechanism, untested, and nobody should rely on it.

Consequences already applied: the Event, README, PrometheusRule text and ADR 0023 no
longer name this procedure for the enabling direction, T8 (a) is withdrawn, and the
T2 e2e stops before it.

## T11: A recorded master that returns empty wins split-brain resolution and wipes the promoted replica

**Severity: high (silent total data loss). Status: DONE 2026-08-26 — measured 2026-08-23,
analysed and fixed 2026-08-26 as Option D with the fail-closed sub-decision, shipped as
[ADR 0028](../../adr/0028-a-demotion-may-not-discard-the-only-dataset.md). Independent of T2;
T2 is only how it was found.**

### What was measured (2026-08-23, Kind)

Clearing the T10 wedge forces deleting pod-0 first, and pod-0 is usually the master.
Its sidecar drained and promoted the last surviving replica, which held the data —
the ADR 0012 path working exactly as designed. Pod-0 then came back on a **fresh,
empty** volume, still named by `vko.gtrfc.com/known-master`, and reported master.

`detectAndResolveSplitBrain`
([`internal/controller/rolling_update.go:1142`](../../../internal/controller/rolling_update.go#L1142))
then picked it. With a non-empty `knownMaster` that names one of the masters, the
function selects it **unconditionally**
([`:1164-1172`](../../../internal/controller/rolling_update.go#L1164-L1172)); the "most
connected slaves (preserves data)" tiebreak
([`:1176-1195`](../../../internal/controller/rolling_update.go#L1176-L1195)) is only
reached when the recorded name matches none of them. Log:

```
Split-brain detected: multiple masters found   masterCount=2 masterIndices=[0 2]
Split-brain resolution: identified real master realMaster=probe-0 rogueCount=1
Demoting rogue master to replica               roguePod=probe-2 realMaster=probe-0
Successfully demoted rogue master
```

End state: `phase=OK`, three bound claims, replication up, `DBSIZE=0` on every pod.
A healthy-looking cluster with nothing in it.

The promotion path has `verifyPromotionCandidateHoldsData`
([`:2014`](../../../internal/controller/rolling_update.go#L2014)) for precisely this shape
(CLAUDE.md: "a promotion candidate holding no keys while the master holds some is
refused", ADR 0011). This path has no equivalent.

Not recorded at the time and no longer recoverable: whether `probe` had Sentinel
enabled. The reachability below is therefore established by reading the code, not by
reproducing the original run.

### The mechanism is a loop, not a coincidence (verified in code 2026-08-26)

The record does not merely *fail to notice* that the returning pod is empty — **it is
what makes the returning pod a master at all**:

1. `known-master` names pod-0, so the replica ConfigMap's `replicaof` directive names
   pod-0 ([ADR 0008](../../adr/0008-known-master-annotation-is-the-recorded-authority.md) D1).
2. Pod-0 boots, reads the ConfigMap it is itself named in, and takes the init-script
   self-claim ([ADR 0008](../../adr/0008-known-master-annotation-is-the-recorded-authority.md)
   D8, D9) — on whatever volume it happens to have. An empty volume changes nothing about
   the claim.
3. Pod-0 now answers `role:master`, which is the only precondition
   `detectAndResolveSplitBrain` puts on the authority: the name has to match one of the
   pods in `masterIndices`.
4. The pod that actually holds the data is by construction *not* the recorded one — it
   was promoted by the sidecar, which has no CR access
   ([ADR 0012](../../adr/0012-the-sidecar-records-its-drain-promotion-on-the-pod.md)) — so it
   is the rogue, and it is demoted.

So the exposure is not "a fresh volume". It is **any path that separates the recorded
name from the data**, with a returning pod on a volume that lost the dataset. Persistence
narrows it (the returning pod usually brings its data back) but does not close it: a
recreated PVC, a `storageClass` that binds a new volume, or an emptyDir cluster all land
in the same place.

### Reachability, per call site

The resolver has exactly three callers, and each names its own authority. Verified by
grep 2026-08-26.

| Call site | Authority passed | Can it name an empty master? |
|---|---|---|
| `handleRollingUpdate` ([`:510`](../../../internal/controller/rolling_update.go#L510)), Sentinel path | `getSentinelMasterPodName` — Sentinel's **live** verdict | **No, while Sentinel answers.** Sentinel fails the dead master over and reconfigures the returning one, so it names the data holder. Unreachable Sentinel returns `""` and the pass falls into the tiebreak instead — a different defect, see below. |
| `handleMultiReplicaRollingUpdate` ([`:3013`](../../../internal/controller/rolling_update.go#L3013)), states `stateManualFailover` / `stateReplacingMaster` | `vko.gtrfc.com/promoted-pod` | **Yes.** The operator verified the pod held data at promotion time; nothing re-checks it after the pod is killed and returns. |
| same, states `stateRestoringTopology` / `stateVerifyingTopology` | `vko.gtrfc.com/known-master` | **Yes — the measured shape.** |
| `verifyTopologyRestored` ([`:3857`](../../../internal/controller/rolling_update.go#L3857)) | `knownMasterPodName(v)` | **Yes**, same as above, and this one calls the **bare** resolver with no reporting wrapper. |
| same switch, every other state (`""`, `stateReplacingReplicas`) | `""` by decision ([ADR 0008](../../adr/0008-known-master-annotation-is-the-recorded-authority.md) D10) | n/a — reaches the tiebreak, which has its own problem. |

**The steady-state check would have survived this shape, and that is the sharpest
evidence that the roll path is the defect.** Once the roll state is cleared,
`checkSteadyStateSplitBrain` reaches rule 2, `confirmedMasterAuthority` returns pod-0,
and `refuseDemotion`'s structural branch fires — `podOrdinal("probe-0") == 0` and
`couldNotHaveSelfElected("probe-2", cmMaster="probe-0")` — so it **refuses**, emits
`SplitBrainDemotionRefused` and leaves both datasets alone
([ADR 0011](../../adr/0011-evidence-based-steady-state-split-brain-resolution.md) D11).
It never gets the chance: `checkAndHandleRollingUpdate` runs the resolver, and the
demotion has happened by the time `handlePostRollingUpdateChecks` is reached in the same
pass.

### What constrains any fix

Six constraints, all verified 2026-08-26. Together they eliminate most of the obvious
shapes.

1. **`detectAndResolveSplitBrain` is at cyclomatic complexity 15** — `make cyclo-report`,
   the project ceiling exactly. **No fix may add a branch inline**; every option below
   costs at least one extracted helper.
2. **The resolver reports nothing, by decision**
   ([ADR 0025](../../adr/0025-a-split-brain-warning-means-one-that-did-not-resolve-itself.md)
   D2) — the report belongs to `resolveSplitBrain`. A refusal that must be visible
   therefore cannot be reported from inside it, and `verifyTopologyRestored` calls the
   bare resolver with no wrapper at all.
3. **The direction rule.**
   [ADR 0011](../../adr/0011-evidence-based-steady-state-split-brain-resolution.md) D7 is
   normative for any new signal: *a signal that is ambiguous about who promoted may
   REFUSE a demotion; it may never GRANT an adoption.* Emptiness is such a signal — it
   says nothing about who promoted. The drain stamp is not: D5 ranks it as positive
   evidence written by the promoter itself.
4. **The tiebreak is not a safe destination.**
   [ADR 0011](../../adr/0011-evidence-based-steady-state-split-brain-resolution.md) D3
   records it: with a shrunken cluster both masters report zero connected slaves, the tie
   goes to `masterIndices[0]` — the lowest ordinal, which in this shape is the empty
   pod-0. **The original "fall through to the connected-slaves tiebreak" proposal in this
   item is withdrawn**; it reaches the same loss by a longer route.
5. **`refuseDemotion` cannot be ported verbatim, and the reason generalises.** Inside a
   roll, both of its branches fire on states the operator itself manufactures:
   - creation order: in `stateManualFailover` the authority is the just-replaced promoted
     pod, which is younger than the outgoing master and inside `syncTimeout` — so
     `recreatedAfter` would refuse **every** normal failover;
   - structural: in `stateRestoringTopology` the authority is pod-0 and the rogue is the
     promoted replica, which by construction could not have self-elected — so it would
     refuse the **designed** end of every topology restoration.

   Generalised: **inside a rolling update, every structural or temporal signal is
   something the operator produces itself, so only two signals still discriminate — the
   drain stamp and the dataset.** The dataset qualifies because the operator never
   promotes an empty pod over a non-empty one (`verifyPromotionCandidateHoldsData`), so
   "authority empty while the rogue holds keys" is a state the operator cannot have
   created.
6. **`persistManualFailoverState` does not clear drain stamps** —
   [`:3209-3223`](../../../internal/controller/rolling_update.go#L3209-L3223) writes
   `AnnotationKnownMaster` directly, and
   [ADR 0011](../../adr/0011-evidence-based-steady-state-split-brain-resolution.md) D16 names
   exactly this class of site ("several paths write the known master without going through
   `recordPromotedMaster`"), covering it only at `clearRollingUpdateState`, i.e. at the
   **end** of the roll. Latent today, because the roll resolver ignores stamps and the
   steady-state check is skipped while roll state is set. **Any option that reads the
   stamp inside a roll makes it live**, and then a stale stamp outranks the operator's
   own fresh promotion.

Two more facts that size the work rather than constrain it:

- The reporting surface already exists and self-escalates. A refused demotion leaves
  `MultipleMasters=True` and, past `splitBrainWarnAfter` = 90 s, emits the
  `SplitBrainDetected` Warning
  ([`split_brain_report.go`](../../../internal/controller/split_brain_report.go)). **No new
  condition and no new Event reason are needed for a refusal to be visible** — with the
  gap in constraint 2 for the `verifyTopologyRestored` call site.
- 11 unit tests exercise the resolver directly (`rolling_update_test.go` ×9,
  `manual_failover_known_master_test.go` ×2), and `newTestReconciler` points every client
  at `127.0.0.1` for an instant refusal — so under a fail-closed data check **every**
  existing demotion test reads "unreadable" and would flip to a refusal unless it is moved
  onto `fakeValkeyServer(t)`.

### Options

**Option A — the dataset veto: never demote a data-holding master toward an empty
authority.**
A helper mirroring `verifyPromotionCandidateHoldsData`, called per rogue inside the
demote loop: read `DBSIZE` on the authority and on the rogue; if the authority holds 0
while the rogue holds > 0, skip that demotion and leave `isMaster` set.
*Covers:* every path in the reachability table, including the ones with no drain stamp
(pre-1.11 sidecar, hard node failure with no SIGTERM, a human `REPLICAOF NO ONE`).
*Costs:* one `DBSIZE` per master, only on a pass that already found a split brain. The
roll makes no further progress on that pod until its bound expires (see the fail-direction
sub-decision, and "what a refusal costs" below). Complexity: one extracted helper.
*Does not:* repair anything — it converts a silent loss into a visible split brain.

**Option B — evidence first: port ADR 0011 rule 1 (the drain stamp) into the resolver.**
Before consulting the authority: if exactly one master carries `vko.gtrfc.com/drain-promoted-at`
and confirms `role:master`, resolve toward **it** and record it via `recordPromotedMaster`
(which also clears the spent stamps). `podState` already carries `pod *corev1.Pod`, so
`hasDrainStamp` costs nothing. More than one stamped master is ambiguous evidence and must
refuse, per [ADR 0011](../../adr/0011-evidence-based-steady-state-split-brain-resolution.md) D10.
*Covers:* the measured shape and every sidecar-drain promotion during a roll — and it
**repairs**, ending with one master and the data intact.
*Requires:* the constraint-6 companion fix — `persistManualFailoverState` must clear drain
stamps, otherwise a stamp from an earlier drain in the same roll outranks the operator's
own promotion and demotes a pod it just verified holds data.
*Does not:* cover a returning empty master where no stamp exists.

**Option C — treat an empty authority as no authority and fall through to the tiebreak.**
The second half of this item's original proposal. **Rejected on constraint 4**: the
tiebreak ties at zero connected slaves and picks the lowest ordinal, which is the empty
pod. It replaces an unconditional wrong answer with a coin flip that lands on the same
side in this exact shape.

**Option D — A + B: evidence first, dataset veto as the backstop.**
The same two-rule shape `resolveMultiMaster` already has, ported into the roll with the
two signals that survive constraint 5. Stamped master resolves and repairs; no stamp and
an empty authority refuses; everything else behaves as today.

**Option E — unify the two resolvers: route the roll through `resolveMultiMaster`.**
**Rejected on constraint 5 and on
[ADR 0011](../../adr/0011-evidence-based-steady-state-split-brain-resolution.md) D3**, which
decided the non-reuse deliberately in the other direction. Inside a roll the operator has
real authorities the steady state does not, and the steady-state refusal rules fire on
states the roll produces on purpose.

**Option F — accept and document.**
**Rejected.** The failure is silent, total and reachable without any manual step: a chaos
kill of the recorded master on a non-persistent cluster mid-roll is enough, and wds18 runs
one every five minutes.

### Sub-decision: the fail direction on an unreadable DBSIZE

Only relevant to A and D.

- **Fail closed (skip the demotion when either count cannot be read).** Follows the
  precedent of `verifyPromotionCandidateHoldsData`, of
  [ADR 0007](../../adr/0007-failover-aware-rolling-update.md) D3 ("an empty desired value
  means cannot tell and degrades toward not acting") and of
  [ADR 0011](../../adr/0011-evidence-based-steady-state-split-brain-resolution.md) D6
  ("silence is not evidence"). Price, stated rather than hidden: **the asymmetry with the
  promotion path is real.** There, waiting is free — the master keeps serving. Here,
  not demoting leaves two masters accepting writes, so the divergence keeps growing for
  the length of the bound. Any probe failure — TLS, auth, NetworkPolicy — stalls split-brain
  resolution cluster-wide.
- **Fail open (demote as today when a count cannot be read).** No behaviour change on any
  existing path, no test churn, and the veto only fires on a positive reading. Price: the
  loss window survives exactly when the operator cannot talk to the pods, which is not an
  independent event from a cluster in trouble.

### What a refusal costs, per state

A refusal leaves two masters and the pass makes no progress on the rogue. Verified: every
state that passes a non-empty authority is bounded, so none of this is an unbounded stall
([ADR 0010](../../adr/0010-every-rolling-update-wait-is-bounded.md)).

| State | Bound that ends the refusal | End state |
|---|---|---|
| `stateManualFailover` / `stateReplacingMaster` | `boundManualFailover` | handled by `handlePostManualFailover`'s expiry |
| `stateRestoringTopology` | `boundTopologyRestore` | `abandonTopologyRestoration`, `TopologyRestored=False` |
| `stateVerifyingTopology` | `finalizationStallTimeout` | completes despite rogue masters, clears state — and `checkSteadyStateSplitBrain` then refuses correctly (see above), so the split brain stays visible instead of being resolved wrongly |
| Sentinel path | Sentinel reconfigures the returning pod itself | resolved without the operator |

`MultipleMasters` carries the level throughout and `SplitBrainDetected` fires at 90 s, so
the refusal is legible on the CR without any new surface. **Open**: the
`verifyTopologyRestored` call site uses the bare resolver (constraint 2), so a refusal
there is reported only because the reporting resolver ran earlier in the same pass — true
today, and worth an assertion rather than a comment.

### Recommendation

**Option D, with the dataset veto as the load-bearing half and fail-closed on an
unreadable count.**

- A is what removes the *loss*; B only removes it where a stamp exists. Shipping B alone
  would leave the no-stamp paths — a hard node failure produces no SIGTERM, hence no drain
  and no stamp — losing data exactly as today, and those are the paths nobody notices.
- B is what makes the outcome *good* rather than merely not-catastrophic. With A alone the
  routine chaos-kill-mid-roll ends as a split brain a human has to clear; with B it ends
  as one master holding the data, which is what the steady-state path already achieves for
  the same shape.
- Both use signals that survive constraint 5, and both are already-decided evidence classes
  — B is [ADR 0011](../../adr/0011-evidence-based-steady-state-split-brain-resolution.md) D5
  rule 1 applied at a second site, A is `verifyPromotionCandidateHoldsData`'s rule applied
  to the destructive direction. Neither invents an authority, so
  [ADR 0011](../../adr/0011-evidence-based-steady-state-split-brain-resolution.md) D22 holds
  unchanged.
- Fail-closed, despite the growing-divergence price: the two-master window is a **designed**
  state that is bounded and visible
  ([ADR 0025](../../adr/0025-a-split-brain-warning-means-one-that-did-not-resolve-itself.md)),
  and divergence is recoverable by a human. A wrong `REPLICAOF` is neither bounded nor
  visible nor recoverable. This is the same trade
  [ADR 0011](../../adr/0011-evidence-based-steady-state-split-brain-resolution.md) already
  makes in so many words: *two masters a human can see beat one dataset silently
  discarded.*

Scope if D is chosen: one new ADR (the refusal is a new durable rule, per the CLAUDE.md ADR
policy), amendments in place to
[ADR 0008](../../adr/0008-known-master-annotation-is-the-recorded-authority.md) D10 (the
authority is no longer unconditional),
[ADR 0011](../../adr/0011-evidence-based-steady-state-split-brain-resolution.md) D3 (the
premise "inside an update the operator knows which pod it promoted" is what this item
disproves) and D16 (the `persistManualFailoverState` gap), the companion stamp-clear, unit
tests for both rules plus the existing 11 moved onto `fakeValkeyServer(t)` where they now
read a count, and a regression e2e: kill the recorded master mid-roll on a non-persistent
cluster and assert the dataset survives.

### Decision

- 2026-08-23: filed from the T2/T10 measurement. Two shapes proposed, no option chosen.
- 2026-08-26: analysed against the code. The second proposed shape ("fall through to the
  connected-slaves tiebreak") is **withdrawn** — constraint 4. Six constraints recorded,
  six options written, Option D recommended with fail-closed.
- 2026-08-26: **Option D chosen and implemented, fail-closed.**
  [ADR 0028](../../adr/0028-a-demotion-may-not-discard-the-only-dataset.md) carries the rules;
  [ADR 0008](../../adr/0008-known-master-annotation-is-the-recorded-authority.md) D10 and
  [ADR 0011](../../adr/0011-evidence-based-steady-state-split-brain-resolution.md) D3, D16 are
  amended in place, and CLAUDE.md's master-authority section grew a sixth rule.

### Implementation (2026-08-26)

`detectAndResolveSplitBrain`
([`rolling_update.go`](../../../internal/controller/rolling_update.go)) is now three ordered
rules instead of one, with the demotion itself gated:

| Part | What it does |
|---|---|
| `stampedMastersAmong` | rule 1, ADR 0028 D2/D5 — reads `hasDrainStamp` off the `Pod` `podState` already holds, filtered by `podIsOurs`; one stamped master resolves, more than one demotes nobody |
| `adoptStampedMaster` | records the adoption through `recordPromotedMaster` **before** anything is demoted; a failed record resolves nothing that pass (ADR 0009) |
| `indexOfName` | rule 2, the authority the caller named — unchanged behaviour, extracted |
| `mostConnectedMaster` | rule 3, the tiebreak — unchanged behaviour, extracted |
| `demoteRogues` + `demotionRefusalReason` + `dbSizeReader` | ADR 0028 D1/D3/D4 — the key-count veto, applied per rogue whichever rule chose the authority, fail-closed on an unreadable count; a vetoed rogue keeps `isMaster` (D8) |
| `persistManualFailoverState` → `writeManualFailoverState` + `clearDrainStamps` | the companion fix of constraint 6 / ADR 0028 D7 |

Two deviations from the analysis above, both deliberate:

1. **The refusal reports through the existing surface only.** The analysis left this as a
   question; the decision is log-only. `resolveSplitBrain` already writes `MultipleMasters`
   and emits `SplitBrainDetected` at 90 s, so a per-pass Event would rebuild the Warning
   noise [ADR 0025](../../adr/0025-a-split-brain-warning-means-one-that-did-not-resolve-itself.md)
   removed — and the `verifyTopologyRestored` call site has no recorder wrapper anyway
   (constraint 2). Pinned by `TestDetectAndResolveSplitBrain_RefusalEmitsNoEvent`.
2. **`vko.gtrfc.com/promoted-pod` is NOT rewritten by a rule-1 adoption.** The switch in
   `handleMultiReplicaRollingUpdate` is the single place that annotation's authority role is
   decided ([ADR 0008](../../adr/0008-known-master-annotation-is-the-recorded-authority.md) D11),
   and a second writer on it was not worth the reach. Recorded as a residual risk in ADR 0028:
   after an adoption during `stateManualFailover` the two names differ, and a *second* split
   brain in that state would fall through to the tiebreak — where the veto still guards the
   demotion. Reasoned from the code, not measured.

**Verified before this was called done:** `make fmt`, `make vet`, `make lint` (0 issues),
`make cyclo` (all functions below 15 — `detectAndResolveSplitBrain` sat at exactly 15 before
this change and is now out of the top of `make cyclo-report`), `make test-unit` green.
`golangci-lint --build-tags=e2e` reports nothing on the new e2e file (the 22 findings it
reports for `test/e2e` are pre-existing and untouched).

**Tests.** New: `internal/controller/split_brain_dataset_test.go` — eleven cases covering the
veto in both directions, both-empty, the unreadable count, the tiebreak guard, the silence, the
stamp adoption with its record and stamp clear, the ambiguous double stamp, the foreign-pod
stamp, the failed adoption write, and the companion stamp clear. New e2e:
`test/e2e/split_brain_dataset_test.go` — the faithful reproduction, a non-persistent 3-replica
non-Sentinel cluster whose recorded master is deleted mid-roll, asserting the dataset survives
and the roll still finishes.

**Existing tests that changed, and why.** Nine unit tests asserted a demotion against an
unreachable Valkey, which under D3 now reads as a refusal; six were moved onto
`withReachableValkey` and three onto `newValkeyFleet` because they used "which pod was
contacted" as a proxy for "which pod was demoted" — the resolver now reads a key count from the
pod it is protecting, so contact no longer implies demotion.

**One fixture bug found on the way, and it was hiding a real check.** `createPodForSts` and
`podFromStsTemplate` built pods without `LabelManagedBy`, so every
`List(MatchingLabels(SelectorLabels))` matched nothing in a unit test — `clearDrainStamps`
looked like a no-op that succeeded. Both fixtures now use `common.SelectorLabels`.

**Not done, deliberately:** the `promoted-pod` coherence of deviation 2, and nothing about the
T10 wedge — this item was only ever the second half of that incident.

## T12: No write fencing — a master with zero replicas keeps accepting writes; `min-replicas-to-write` as an opt-in field

Moved on 2026-09-27 to [012-no-write-fencing-min-replicas-to-write-as-an-opt-in-field.md](../012-no-write-fencing-min-replicas-to-write-as-an-opt-in-field.md).

## T13: The steady-state master authority has no `DeletionTimestamp` guard anywhere

**Severity: was "unassessed, dormant" — CI measured it on 2026-08-27 as a full-fleet
`dbsize=0`, so it was a defect, and it is DONE the same day.** The
`single-node-valkey9` leg hit the race this item predicted, one door over: the chaos delete
of `TestE2E_SplitBrain_RecordedMasterReturningEmptyKeepsTheDataset` landed in the same
second the roll deleted the outgoing master, the dying pod's drain stamped the peer that was
itself terminating, `detectAndResolveSplitBrain` adopted the stamp, and the fleet
consolidated toward a pod that returned empty — the dataset veto could not fire because both
masters still held their keys at demotion time; the loss happened when the adopted pod
finished dying. **The guard now binds all four adoption/confirmation doors** — the roll
resolver selects only among non-terminating masters (refusing when none is left),
`stampedMasters` skips a terminating stamped pod, `confirmedMasterAuthority` refuses a
recorded master provably carrying a `DeletionTimestamp` (positive evidence only — an
unreadable Pod object refuses nothing, per the creation-order precedent), and
`adoptUnrecordedPromotion` refuses a terminating sole master. Demotion of a terminating
rogue stays allowed (the ADR 0026 `reachable()` carve-out — the direction was wrong, not the
contact). ADR 0028 D5a is new; ADR 0026 and ADR 0011 amended in place; five unit tests
reconstruct the CI shape. The companion CI failure
(`TestE2E_RollingUpdate_TopologyRestoreAbandoned`, 300 s Phase-1 timeout) is consistent with
the same overloaded-runner window and had no wrong end state in its logs; watch it on the
next run rather than chasing it now.

~~Status: open, split out of T5 on 2026-08-25 (T5 E8) rather than folded into it.~~
T5 shipped the same day and its ADR names this item as deliberately out of scope
([ADR 0026](../../adr/0026-a-pod-being-deleted-is-not-available.md) D10 and its
Residual risks), so the boundary is now recorded in the code rather than only
here. The `available()`/`reachable()` split T5 introduced is also the vocabulary
any fix here would use: `steady_state_master.go` builds a `podState` that fills
`terminating` and asks `reachable()`, which is correct for the demotion it drives
and says nothing about `listMasterLabeledPods` or `probeForAnyMaster`.**

### What is verified (2026-08-25, by reading the code; re-verified 2026-08-26 on `HEAD` = `1c309d8`)

> **Corrected 2026-08-26.** The sentence that opened this subsection —
> "~~`grep DeletionTimestamp internal/controller/steady_state_master.go` returns
> **nothing**~~" — stopped being true at commit `360cb03`, which added
> [`:602`](../../../internal/controller/steady_state_master.go#L602)
> `terminating: pod.DeletionTimestamp != nil`. The substance is unaffected: that line is a
> `podState` **construction-site fill**, not a guard, and no decision in this file branches
> on it. But the verbatim claim has to go, because a later reader runs the grep, sees a hit,
> and concludes the item is closed.

`listMasterLabeledPods`
([`:195`](../../../internal/controller/steady_state_master.go#L195)) filters on the
master label and ownership only. From there `adoptUnrecordedPromotion`
([`:233`](../../../internal/controller/steady_state_master.go#L233)) →
`adoptMaster` ([`:476`](../../../internal/controller/steady_state_master.go#L476)) →
`recordPromotedMaster` writes `vko.gtrfc.com/known-master` — the recorded master
authority of ADR 0008 — and republishes the replica ConfigMap at it.

**The reachable shape with the worst consequence is not the adoption half this item leads
with.** It is `confirmedMasterAuthority`
([`:530-552`](../../../internal/controller/steady_state_master.go#L530-L552)), added after this
item was filed and never named in it. It accepts the recorded authority on two checks —
the pod answers, and it answers `role:master` — and checks `DeletionTimestamp` at neither.
A terminating pod that still serves therefore stays the authority, and
`demoteConfirmedRogues` ([`:567`](../../../internal/controller/steady_state_master.go#L567))
then sends `REPLICAOF` to the **live** master toward it. That is the exact shape ADR 0028
measured as silent total data loss on the roll path.

**And this path has no dataset veto.** `grep -n 'DBSize\|holdsData'
internal/controller/steady_state_master.go` returns **nothing** — verified 2026-08-26. The
ADR 0028 key-count refusal exists only in `rolling_update.go`
([`:1388`](../../../internal/controller/rolling_update.go#L1388),
[`:1358`](../../../internal/controller/rolling_update.go#L1358)). The steady-state path has a
`refuse` veto ([`:405`](../../../internal/controller/steady_state_master.go#L405) →
`refuseDemotion`), but it is the drain-window and structural veto of ADR 0011, which is a
different question and does not look at the data. Note also that the second call site,
`adoptAndConsolidate` ([`:471`](../../../internal/controller/steady_state_master.go#L471)),
passes `nil` — no veto at all.

Nothing clears `instanceRole=master` at delete time: the sidecar relabel is
best-effort and the kubelet gives no ordering between the two SIGTERMs
(ADR 0012). So a pod that is being deleted can still carry the label that makes
it a candidate here. This is the same blind spot T5 documents, one layer up — and
T4 already closed the *rolling-update* half of it in `labelClaimsMaster`
([`rolling_update.go:1552`](../../../internal/controller/rolling_update.go#L1552) — the
ticket said `:1261`, stale since `360cb03`/`2051a34`),
which refuses a `DeletionTimestamp` pod. The steady-state half was never touched.

The same is true one file over: `valkey_controller.go` reads exactly one
`DeletionTimestamp`, the CR's own
([`:221`](../../../internal/controller/valkey_controller.go#L221) — the ticket said `:224`,
stale). So `checkAndRecoverNoMaster` → `probeForAnyMaster`
([`:2642-2662`](../../../internal/controller/valkey_controller.go#L2642-L2662)) classifies
purely by INFO reachability, and the `REPLICAOF NO ONE` plus `recordPromotedMaster` that
follow can land on a dying pod.

**Why it is still ranked below two cosmetic-looking condition bugs, and this is the
tradeoff worth stating plainly:** the consequence is the worst in this file, but the
trigger is dormant and **unmeasured**. It needs a reconcile pass to land inside the pod's
own termination window (≤75 s `terminationGracePeriodSeconds`), and there is **no Pod
watch** — the CR watch is generation-gated, so the pass has to arrive for an unrelated
reason. Nobody has produced this state. T16 and T15 are wrong on every pass, today, on
running clusters.

### Why this is not obviously a defect, and why it is not part of T5

A StatefulSet pod name is stable. Recording a terminating pod *by name* as the
known master still resolves, after the pod returns, to the pod that returns —
which is not the same situation as promoting a pod that is about to vanish. Whether
the adoption is wrong therefore depends on what the pod comes back holding:

* **with persistence**, it returns with its dataset and the record was right;
* **without persistence**, it returns empty, and the replica ConfigMap now points
  every replica at an empty master — the ADR 0008 D10/D11 loss, reached from a new
  input;
* and the adoption paths require *evidence* (the drain stamp, the structural rule,
  or the recorded pod answering that it is no longer master — ADR 0011), so the
  question is really "is a `DeletionTimestamp` pod a valid **evidence source**",
  which ADR 0011 does not currently answer either way.

T5 is the rolling-update spend rule and can ship without answering that. Folding
it in would pull T5 into the ADR 0008/0011 evidence family and make it
unfinishable — which is why E8 re-filed it here instead.

### What this item owes before a decision

1. Decide the question above per branch of the ADR 0011 decision table, not as a
   blanket "reject terminating pods".
2. Check whether the drain-stamp evidence path is *more* exposed than the others:
   on wds18 Chaos Mesh kills a vko pod every 5 minutes, so a drain-promoted pod
   being itself killed shortly after is a sampled case, not a hypothetical.
3. Decide `probeForAnyMaster` separately — a `DeletionTimestamp` pod treated as
   *unreachable* there fails closed exactly like the existing foreign-pod branch,
   which looks like the cheap and obviously-safe half.
4. Whatever is decided goes into ADR 0011 (and ADR 0008 if the record changes), not
   into ADR 0026 — ADR 0026 is about spending a pod during a roll.
5. **Added 2026-08-26:** decide whether `confirmedMasterAuthority` should gain the ADR 0028
   dataset veto as well as a `DeletionTimestamp` guard. The two are independent: the
   `DeletionTimestamp` guard stops a *dying* authority, the key-count veto stops an *empty*
   one, and the wds18 gitlab shape (memory-only, stale recorded authority) is the empty one.
   Do not assume ADR 0028 covers this path — it does not; it is `rolling_update.go` only.

### Decision

- 2026-08-25: **split out of T5 (E8), no option chosen, no work started.** Recorded
  explicitly: the mechanism is verified, the defect is not — and the reason it is a
  separate item is that the evidence question needs the persistence analysis T5 does
  not contain.
- 2026-08-26: **still open, no option chosen.** Re-verified on `HEAD` = `1c309d8`; two claims
  in "What is verified" were corrected in place (the grep sentence, two drifted line refs)
  and the `confirmedMasterAuthority` shape was added, because the item as filed led with the
  adoption half and that is not the reachable path with the worst consequence.
  **Recommended split, so this does not stay blocked on the hard question:** take owed-item 3
  (`probeForAnyMaster` treats a `DeletionTimestamp` pod as unreachable) **now** — it is XS, it
  fails closed like the branch next to it, and it is the only path that sends
  `REPLICAOF NO ONE` to a dying pod. Leave owed-items 1, 2, 4 and 5 — the ADR 0011 evidence
  question — open. Splitting it this way is what keeps the M-sized decision from holding up
  the XS-sized safety win.


## T14: `status.observerReady` reads a Deployment the operator has not proven it owns

**Severity: low today, but it inverts the record ADR 0020 relies on. Status: DONE
2026-08-26, shipped with T6a. Found 2026-08-25 while analysing T6a. Was latent — no name
collision exists on wds18.**

`isObserverDeploymentReady`
([`valkey_controller.go:1999-2009`](../../../internal/controller/valkey_controller.go#L1999-L2009))
`Get`s the Deployment by the derived name and returns `deploy.Status.ReadyReplicas > 0`
with **no `metav1.IsControlledBy` check**. Both sibling readers inside the same
function do have one, each with a comment citing ADR 0020: the data StatefulSet at
[`:2029-2032`](../../../internal/controller/valkey_controller.go#L2029-L2032) (*"A foreign
StatefulSet is treated as absent"*) and the Sentinel StatefulSet at
[`:2265`](../../../internal/controller/valkey_controller.go#L2265). Every observer
Deployment **write** path is guarded
([`:1861`](../../../internal/controller/valkey_controller.go#L1861),
[`:1904`](../../../internal/controller/valkey_controller.go#L1904),
[`:1934`](../../../internal/controller/valkey_controller.go#L1934)); only this reader is
not.

So a foreign Deployment holding `<cr>-observer` makes the CR report
`observerReady: true` from a stranger's ready replica — while
`reconcileObserverDeployment` is refusing to write and ADR 0020 is relying on exactly
this field to *record the degradation*. The one CLAUDE.md rule that covers it is
explicit: *"If a second code path reads or acts on the object, that path treats a
foreign one as absent and stays quiet — the reconciler is the one reporter."*

**Fix:** add the guard, return `false`. **Not a four-line change, though:** it breaks
the `"one ready replica is ready"` subtest at
[`resource_reconcile_test.go:1434-1439`](../../../internal/controller/resource_reconcile_test.go#L1434-L1439),
because `builder.BuildObserverDeployment` stamps no ownerReference —
`SetControllerReference` is applied by the reconciler at
[`valkey_controller.go:1885`](../../../internal/controller/valkey_controller.go#L1885), not by
the builder — so the fixture must stamp a controller reference first. Fail direction is toward `false`,
which is both the honest answer and the direction ADR 0006 D2 prescribes for a
diagnostic component. No new Event; `reconcileObserverDeployment` stays the one
reporter. Plus one unit test (seed a Deployment under the derived name with a foreign
ownerReference and `ReadyReplicas: 1`, assert `false`). Needs an ADR 0020 amendment:
the NA62 sweep sentence — *"nothing reads back a Service, a NetworkPolicy, a
ServiceMonitor or a Certificate"* — omitted Deployments. The load-bearing citation for
the amendment is ADR 0020's own Consequences (*"the observer is diagnostic, the CR does
its job without it, and `status.observerReady` records the degradation"*) and its
rejected alternative, which argues the degradation *"would be invisible in
`kubectl get valkey`"* because the field has no print column.

**Belongs in the same change as T6a A1b** — a fixed write path with a wrong input is
still a wrong field.

### Decision

**DONE 2026-08-26.** Fixed together with T6a, as argued. `isObserverDeploymentReady` returns
`false` for a Deployment this CR does not control; no Event, because
`reconcileObserverDeployment` stays the one reporter. Guarded by the `a foreign deployment is
not ready` subtest of `TestIsObserverDeploymentReady`
([`resource_reconcile_test.go`](../../../internal/controller/resource_reconcile_test.go)), and
the two positive subtests now stamp the controller reference they were implicitly assuming —
`builder.BuildObserverDeployment` does not set one, the reconciler does, so the fixtures had
been testing a foreign Deployment without knowing it. ADR 0020's D8/NA62 read-path sweep is
corrected in place: it enumerated Services, NetworkPolicies, ServiceMonitors and
Certificates and omitted Deployments, which was the one kind whose reader the ADR's own
observer refusal direction depends on.

## T15: `RollingUpdatePaused=True` can never be cleared on a non-Sentinel cluster

**Status: DONE 2026-08-26 — Option H′ (both clear sites, early return gated on tier
convergence) + the resume-semantics doc correction. DONE, implemented, unit- and
integration-verified. Ships with T16 as one PR,
three commits — see the Decision section at the end.**
~~OPEN — un-deferred 2026-08-26. Ranked SECOND, still paired with T16~~
(~~recommended next item together with T16~~ — ~~Ranked THIRD~~; the pairing stands and the
position moved up once more: T21 was chosen ahead of both on 2026-08-26 ~09:40 and **shipped
the same day**, so T16 is first again and this is second).**
~~deferred 2026-08-26~~ — declared as a gap in `conditionRegistry`. **Severity: medium** — a
permanent, user-visible False positive on the topology it affects, exported as a metric, and
the operator refuses to resume until a spec change. Found 2026-08-25 while building the T6
condition inventory. Latent on wds18: no non-Sentinel cluster currently carries the
condition. **Effort: S, and it needs no design decision at all** — the fix site already
exists on HEAD. See the Decision section at the end of this item; the line references and the
ADR 0007 claim in the body were corrected on 2026-08-26 and the corrections are marked in
place.

Two sites reference the type, verified by grep:

- **set** `pauseRollingUpdate` →
  [`rolling_update.go:1783-1791`](../../../internal/controller/rolling_update.go#L1783-L1791)
- **clear** `finalizeRollingUpdate` →
  [`rolling_update.go:661-666`](../../../internal/controller/rolling_update.go#L661-L666)

`finalizeRollingUpdate` has exactly one caller,
[`:499`](../../../internal/controller/rolling_update.go#L499), inside
`handleRollingUpdate` — the **Sentinel** dispatch arm. The multi-replica non-Sentinel
path completes through `finalizeMultiReplicaRollingUpdate`
([`:3857-3870`](../../../internal/controller/rolling_update.go#L3857-L3870)), which writes
nothing, and `handleStandaloneRollingUpdate` clears nothing either.

`pauseRollingUpdate` is reachable on both topologies, verified by tracing the callers:

| Path | Chain |
|---|---|
| Sentinel | `handleRollingUpdate:528` → `replaceNextReplica:1626` → `verifyReplacedReplicasSynced` → `pauseRollingUpdate` |
| non-Sentinel | `dispatchMultiReplicaState:3043` → `replaceNextReplica:1626` → `verifyReplacedReplicasSynced` → `pauseRollingUpdate` |
| non-Sentinel | `handleManualFailover:3091` → `waitForReplicasReady` → `waitOrPauseForReplicaSync:1975` → `pauseRollingUpdate` |

A non-Sentinel multi-replica cluster that hits `syncTimeout` therefore gets
`RollingUpdatePaused=True / SyncTimeout` and keeps it for the life of the cluster, even
after the next spec change resumes and completes the roll. This is the same shape ADR
0002 D10 fixed once for `SidecarUpdatePending` and ADR 0026 D5 fixed once for
`PodTerminationStalled` — the clear sits behind the code path whose absence caused the
staleness.

**Rejected before filing: the obvious fix is self-defeating.** Putting the clear into
`clearRollingUpdateState` next to the existing `clearPodTerminationStalled`
([`:2561`](../../../internal/controller/rolling_update.go#L2561)) looks right — one site,
all eight callers — but `pauseRollingUpdate` **calls `clearRollingUpdateState` itself**
([`:1799`](../../../internal/controller/rolling_update.go#L1799)) immediately after setting
the condition, so the clear would flip it back to False in the same pass and erase the
report it exists to make durable. The presence guard does not help: the condition is
present by then.

**Proposed fix (not decided):** the `if result.Completed` branch at
[`:288-292`](../../../internal/controller/rolling_update.go#L288-L292) — the same site
T6c C2 uses. Every dispatch target funnels through it on all three topologies, and
`pauseRollingUpdate` never reaches it (it returns `RollingUpdateResult{}` with
`Completed: false` at [`:1804`](../../../internal/controller/rolling_update.go#L1804)).
Presence-guard it, which also fixes the currently unguarded `False` write at `:661`.
~~ADR 0007 and ADR 0010 state this condition's lifecycle and must be amended in the same
change.~~

> **Corrected 2026-08-26 — this would have cost a day of the wrong work.** ADR 0007 does
> **not** state this condition's lifecycle and does not mention it at all:
> `grep -n 'Paused' docs/adr/0007-failover-aware-rolling-update.md` returns **nothing**.
> Do not budget an ADR 0007 edit. The third ADR that actually goes stale the moment this
> ships is **ADR 0002**, at `:231` and `:360`, where the wording "a separate open item"
> stops being true. Plus [ADR 0027](../../adr/0027-conditions-are-levels-edges-or-history.md)
> `:188-192`, whose Residual risks list shrinks by one gap.
>
> Line references in this item drifted too — they were written before `2051a34` and
> `75b3c92`. Re-verified against `HEAD` = `1c309d8`:
>
> | This item says | Actually |
> |---|---|
> | set `:1783-1791` | [`rolling_update.go:2018-2031`](../../../internal/controller/rolling_update.go#L2018-L2031) |
> | clear `:661-666` | [`rolling_update.go:678-683`](../../../internal/controller/rolling_update.go#L678-L683) |
> | `finalizeRollingUpdate` caller `:499` | [`rolling_update.go:517`](../../../internal/controller/rolling_update.go#L517) |
> | dispatch arm | [`rolling_update.go:269-274`](../../../internal/controller/rolling_update.go#L269-L274) — `case v.IsSentinelEnabled():` → `handleRollingUpdate`, else → `handleStandaloneRollingUpdate` ([`:3087`](../../../internal/controller/rolling_update.go#L3087)) |
> | proposed fix site `:288-292` | [`rolling_update.go:289`](../../../internal/controller/rolling_update.go#L289) — the `if result.Completed` branch, which already carries the twin `clearSidecarUpdatePending` at [`:307`](../../../internal/controller/rolling_update.go#L307) |
>
> The mechanism itself re-verified unchanged and correct as described.

**One thing the proposed fix must not skip, or it does not actually close this item.** The
`False` write at [`:678-683`](../../../internal/controller/rolling_update.go#L678-L683) calls
`setStatusCondition` directly, unguarded — so every Sentinel cluster that completes a roll
*gains* a condition it never had. Until that is presence-guarded, the registry row cannot
claim `presenceGuarded: true` and the `declaredGap` cannot be dropped. Landing only the new
clear site leaves T15 open by the registry's own definition.

**And the blast surface is wider than `kubectl`:** the condition is exported as
`vko_valkey_condition{type="RollingUpdatePaused",status="True"}`
([`internal/metrics/collector.go:186-193`](../../../internal/metrics/collector.go#L186-L193)),
so a stale `True` is a permanently firing series, not just a cosmetic row.

**Note on reproducing it:** a fixture that merely seeds `RollingUpdatePaused=True` on a
converged cluster shows nothing, and shows nothing on *either* topology —
`pauseRollingUpdate` clears the state annotation, so both take the early return at
[`:253-261`](../../../internal/controller/rolling_update.go#L253-L261) before the dispatch.
The asymmetry appears only on a **subsequent completed roll**: the Sentinel path reaches
`finalizeRollingUpdate` and its clear at `:660-666`, while the non-Sentinel path ends at
`:3852` or in `finalizeMultiReplicaRollingUpdate`, neither of which clears. The fixture
must therefore carry outdated pods or the state annotation so the dispatch actually
happens.

### Decision

**Deferred 2026-08-26, deliberately and not by omission.** The change of 2026-08-26 took the
five recommended T6 blocks plus T14; this item was filed as a finding without an
implementation recommendation.

It is, however, no longer only in this ticket: `RollingUpdatePaused` now carries a
**declared gap** in `conditionRegistry`
([`condition_registry.go`](../../../internal/controller/condition_registry.go)) naming T15, and
[ADR 0027](../../adr/0027-conditions-are-levels-edges-or-history.md) D4 requires that reference
— so the gap is visible in the code and traceable to this item rather than resting on
someone remembering it. ADR 0027's Residual risks state plainly that a declared gap is an
open defect and not an accepted design. ADR 0002's Alternatives additionally records why
`clearRollingUpdateState` is the wrong site, which is the trap this item nearly walked into.

Severity check before deferring: latent on the inspected fleet — no non-Sentinel cluster
carries the condition today, and it is only reachable after a `syncTimeout` on that topology.

- **2026-08-26, second pass: un-deferred. This is next up, together with T16.** Re-verified
  on `HEAD` = `1c309d8`; the mechanism holds, the line refs and the ADR 0007 claim were
  corrected in place above. Reasons it moved:
  - **It is the cheapest real defect in the file.** Zero design decisions — the fix site
    already exists on HEAD and already carries its twin one line away. T16 needs one
    ownership call; this needs none.
  - Ship it with **T16** in one change: they share the ADR 0027 `:188-192` residual-risk
    edit, and together they **empty the `declaredGap` list**, which is a clean and checkable
    definition of done for the pair.
  - Trap to carry into the fixture, already stated in "Note on reproducing it" above and
    worth repeating because it is the way this gets falsely marked done: seeding the
    condition on a converged cluster proves **nothing**, because
    `checkAndHandleRollingUpdate` takes the no-update early return at
    [`:245-260`](../../../internal/controller/rolling_update.go#L245-L260) before dispatching.
    The fixture needs outdated pods or a state annotation so a roll really completes.


### Option analysis 2026-08-26 (third pass) — superseded by the Decision and Implementation sections below

Re-verified against `HEAD` = `5af86d2`, not `1c309d8`: the item's own "corrected" refs above
drifted a second time. **Corrected again:** the `True` write is
[`rolling_update.go:2061-2065`](../../../internal/controller/rolling_update.go#L2061-L2065), the
`False` write [`:716-720`](../../../internal/controller/rolling_update.go#L716-L720), the sole
production caller of `finalizeRollingUpdate` is
[`:554`](../../../internal/controller/rolling_update.go#L554), and the two candidate fix sites are
[`:256-262`](../../../internal/controller/rolling_update.go#L256-L262) (converged early return,
twin `clearSidecarUpdatePending` at `:261`) and
[`:291-313`](../../../internal/controller/rolling_update.go#L291-L313) (completion branch, twin at
`:309`). The dispatch has **three** arms
([`:269-277`](../../../internal/controller/rolling_update.go#L269-L277)), not two. The metric is
`vko_valkey_status_condition`
([`internal/metrics/collector.go:37`](../../../internal/metrics/collector.go#L37)), **not**
`vko_valkey_condition` as this item claimed.

**The `declaredGap` text is itself wrong.** Both
[`condition_registry.go:160`](../../../internal/controller/condition_registry.go#L160) and
[ADR 0027](../../adr/0027-conditions-are-levels-edges-or-history.md) `:34-35` say
`pauseRollingUpdate` is "reachable on every topology". It is not: the `default:` dispatch arm
(`replicas <= 1 && !sentinel`) reaches `handleStandaloneRollingUpdate`, whose call graph
contains no pause site. Two topologies, not three.

**Half two is live on the fleet, not latent.** The `False` write at `:716-720` calls
`setStatusCondition` directly and `meta.SetStatusCondition` **adds** an absent condition, so
every Sentinel CR that has ever completed a roll already carries
`RollingUpdatePaused=False`. That is not a stale `True` waiting to happen — it is a condition
the whole fleet gained without ever pausing. This half must be deleted or presence-guarded, or
the registry row cannot claim `presenceGuarded: true` and the gap cannot be dropped.

**`phase=Error` is not durable, so the condition really is the only residue.** The pause
returns `&RollingUpdateResult{}` ([`:2078`](../../../internal/controller/rolling_update.go#L2078))
with no error and no requeue, the pass falls through to `updateStatus`, and
`updateStandaloneStatus` reassigns `OK` on `readyReplicas == spec.replicas`
([`valkey_controller.go:2098-2099`](../../../internal/controller/valkey_controller.go#L2098-L2099)).
[`README.md:912`](../../../README.md) ("phase `Error`") is therefore already wrong in the common
case.

**Refuted while analysing: the documented resume semantics.** `pauseRollingUpdate` calls
`clearRollingUpdateState` at [`:2073`](../../../internal/controller/rolling_update.go#L2073),
which drops `annotationSyncWaitStarted` and the in-memory bound, so the next dispatching pass
re-arms a **fresh** `syncTimeout` budget and pauses again, re-emitting the Warning Event each
cycle. "The operator will not resume until the user applies a new spec change" is false in
four tracked places: [`api/v1/valkey_types.go:53-54`](../../../api/v1/valkey_types.go#L53-L54),
[`rolling_update.go:2055-2056`](../../../internal/controller/rolling_update.go#L2055-L2056),
[`README.md:912`](../../../README.md) and [`README.md:945`](../../../README.md). See Q4 below.

#### Options

| # | Option | LOC | Closes both halves | Verdict |
|---|---|---|---|---|
| 1 | **H′ — clear at both sites, early-return clear gated on tier convergence; delete `:715-720`** | +14/−6 | yes | **recommended** |
| 2 | A — completion branch only, plus delete `:715-720` | +8/−6 | yes | cheapest defensible |
| 3 | I — make the pause a real bounded state (`statePaused`) | ~80 | yes, and fixes the docs | own item, see Q4 |
| 4 | B — clear inside each of the five finalizers (`:721`, `:3200`, `:4108`, `:4157`, `:4173`) | +12 | yes | dominated; a sixth site reopens it silently |
| 5 | E — durable pause annotation + level evaluator | ~+45 | yes | only worth it as part of I |
| 6 | C1/C2 — clear from inside `clearRollingUpdateState` | small | no | self-defeating; ADR 0002 `:228-232` rejected this site **by name** |
| 7 | J — reclassify as history | 0 | no | declares a permanently firing Prometheus series to be the design |
| 8 | D (level), F (clear on dispatch entry), G (tie to phase) | — | no | dead: no durable state survives `clearRollingUpdateState`; `phase` self-heals |

**Why the naive both-sites fix (H) was rejected in favour of H′.** H clears
unconditionally at the converged early return. `verifyReplacedReplicasSynced` skips every pod
with `ps.needsUpdate` ([`:2007`](../../../internal/controller/rolling_update.go#L2007)), so the
pod it pauses on is up to date by construction, and the pause already deleted the state
annotation — so a pass in which the remaining ordinals are absent or merely not Ready reaches
`needsRollingUpdate == false && state == ""` and H writes `False` onto a **stuck** roll.
Measured over two passes in a scratch tree: `pass2 = False/Completed` under H,
`True/SyncTimeout` under H′ and under HEAD. H therefore converts a permanently-stuck `True`
into a permanently-wrong `False`, which is worse for the alerting surface this item exists
for. **Not verified: whether that shape occurs in production** — it was produced in a fixture,
not observed on a cluster. The gate costs ~2 lines, so it is insurance either way.

**H′ in detail.** (1) a presence-guarded `clearRollingUpdatePaused` next to
`clearSidecarUpdatePending`
([`valkey_controller.go:2617-2622`](../../../internal/controller/valkey_controller.go#L2617-L2622));
(2) the existing ordinal loop
([`:219-244`](../../../internal/controller/rolling_update.go#L219-L244)) accumulates a converged
count as a plain statement — it already `Get`s every ordinal and discards both the NotFound
fact and readiness; (3) a two-line `clearPausedIfTierConverged` at `:261`; (4) the plain clear
at `:309`; (5) **delete** `:715-720`.

**The binding constraint on the shape.** `checkAndHandleRollingUpdate` measures cyclomatic
complexity **exactly 15** and `make cyclo` runs `gocyclo -over 15`
([`Makefile:57`](../../../Makefile#L57), `CYCLO_THRESHOLD ?= 15` at
[`:377`](../../../Makefile#L377)) — passes at 15, fails at 16. Every clear must therefore be an
unconditional call to a helper that carries the branch, never an inline `if`.

#### A standing false claim this item is about to cite

The convergence proof at
[`rolling_update.go:301-305`](../../../internal/controller/rolling_update.go#L301-L305), repeated
in [ADR 0002](../../adr/0002-surface-a-blocked-reconcile-on-the-cr.md) D10 `:219-223` — "two of
the completion sites inside `verifyTopologyRestored` ... are entered only through that same
count" — is **false**. `verifyTopologyRestored` has two callers:
[`:3287`](../../../internal/controller/rolling_update.go#L3287), inside
`if updatedCount == totalPods`, and
[`:3318`](../../../internal/controller/rolling_update.go#L3318) inside
`dispatchMultiReplicaState`, whose only caller
([`:3303`](../../../internal/controller/rolling_update.go#L3303)) sits **after** that block
closes. `Completed: true` is therefore reachable with `updatedCount != totalPods`. The ADR
sentence must be corrected in this change regardless of which option ships — see Q5.

#### Registry row after H′

```
conditionType:   RollingUpdatePaused
kind:            conditionEdge
evaluators:      1
clearSite:       "clearRollingUpdatePaused, from the converged early return (tier-converged) and from the completion branch of checkAndHandleRollingUpdate"
presenceGuarded: true
declaredGap:     ""
```

Byte-for-byte the shape of the `SidecarUpdatePending` row
([`condition_registry.go:139-145`](../../../internal/controller/condition_registry.go#L139-L145)).
**The registry does not discriminate H′ from A** — `TestConditionRegistryEdgesHaveAPresenceGuardedClear`
reads struct fields only. The hand-written probes are the enforcement.

#### Tests

Five, and the fixture already exists — `TestCheckAndHandleRollingUpdate_CompletionClearsSidecarUpdatePending`
([`sidecar_pending_condition_test.go:117-140`](../../../internal/controller/sidecar_pending_condition_test.go#L117-L140))
builds exactly what is needed: 3 replicas, no Sentinel, state annotation set, every pod on the
current template, `result.Completed == true`.

| Probe | Asserts | On HEAD |
|---|---|---|
| N | the completion branch clears a seeded pause | **red** |
| N2 | the converged early return clears it | **red** |
| P | the pause survives while an outdated pod remains | green |
| Q / Q2 | the pause survives the next pass (pod absent / present-but-not-Ready) | green — **red under H** |
| G-live | a Sentinel CR completing a roll must not *gain* the condition (direct `finalizeRollingUpdate` call) | **red** |

A unit test that merely calls the new helper on a condition-free CR is **not** a guard: it
exercises a function written in the same commit and cannot fail on HEAD.

#### Docs owed in the same change

ADR 0002 D10 gains a third instance and the corrected proof; `:231-232` ("whose own clear gap
is **a separate open item**") is marked closed in place; `:354-360` needs **no** edit — the
claim there is still true. ADR 0027 `:34-36` is falsified twice over ("cleared only by
`finalizeRollingUpdate`" dies with the deletion; "reachable on every topology" was already
false) and is marked in place; plus D4 `:91-92`, Consequences `:133-135`, Residual `:188-192`,
Status. Then `condition_registry.go:14-16`, `CLAUDE.md:379-381`, `README.md:912`, `:945`, the
stale "the one place ... the only place" comment at
[`rolling_update.go:257-258`](../../../internal/controller/rolling_update.go#L257-L258), and this
item's status line. **ADR 0007: nothing** (`grep -c` = 0, as this item already recorded).
**ADR 0010: nothing required** — all three hits concern where the condition is *set*.
**SECURITY_ARCHITECTURE.md: nothing. No chart change.** ADR 0017 D7 applies: each new guard
knocked out individually, failure message recorded.


### DECIDED 2026-08-26 (third pass) — Option H′, plus the doc correction

Decided by Hans on 2026-08-26 after the option analysis above was tabled. Not yet implemented.

**D1 — Option H′ ships: clear at both sites, the early-return clear gated on tier
convergence.** (a) a presence-guarded `clearRollingUpdatePaused` next to
`clearSidecarUpdatePending`
([`valkey_controller.go:2617-2622`](../../../internal/controller/valkey_controller.go#L2617-L2622));
(b) the existing ordinal loop
([`rolling_update.go:219-244`](../../../internal/controller/rolling_update.go#L219-L244))
accumulates a converged count as a plain statement — it already `Get`s every ordinal and
discards both the NotFound fact and readiness; (c) a two-line `clearPausedIfTierConverged` at
[`:261`](../../../internal/controller/rolling_update.go#L261); (d) the plain clear at
[`:309`](../../../internal/controller/rolling_update.go#L309); (e) **delete** the unguarded
`False` write at [`:715-720`](../../../internal/controller/rolling_update.go#L715-L720).

*Why H′ over A:* A leaves probe N2 red — the revert path, which hits **Sentinel clusters too**,
because a spec revert makes every pod current again and the pass takes the converged early
return, where `finalizeRollingUpdate` is never reached. A also keeps a single write site, and
`setStatusCondition` swallows write errors, so one swallowed 409 there is permanent.
*Why H′ over the naive both-sites fix (H):* H is **measured** to erase a live, correct pause on
the pass after the pause — `pass2 = False/Completed` under H against `True/SyncTimeout` under
H′ and under HEAD. *Why H′ over I:* I fixes a different real defect and coupling a
status-lifecycle fix to a rolling-update state-machine change makes both unreviewable.

**D2 — The gate is insurance, and the reachability of what it guards is NOT verified.** The
Q/Q2 shape was produced in a fixture, never observed on a cluster. It is taken anyway because
it costs two lines and the failure direction it prevents — a permanently *wrong* `False` on a
stuck roll — is worse than the stale `True` this item exists to remove.

**D3 — Half two is deleted, not merely guarded.** The write at `:715-720` is unconditional and
`meta.SetStatusCondition` adds an absent condition, so every Sentinel CR that has ever
completed a roll already carries `RollingUpdatePaused=False`. Without the deletion the registry
row cannot claim `presenceGuarded: true` and the gap cannot be dropped. `G-live` — a direct
`finalizeRollingUpdate` call asserting the condition is `nil` — is the probe, and it is **red
on HEAD**.

**D4 — The resume semantics are corrected in text, not in behaviour.** `pauseRollingUpdate`
calls `clearRollingUpdateState` at
[`:2073`](../../../internal/controller/rolling_update.go#L2073), which drops the sync-wait anchor,
so the next dispatching pass re-arms a fresh `syncTimeout` and pauses again. The four places
that promise otherwise — [`api/v1/valkey_types.go:53-54`](../../../api/v1/valkey_types.go#L53-L54),
[`rolling_update.go:2055-2056`](../../../internal/controller/rolling_update.go#L2055-L2056),
[`README.md:912`](../../../README.md) and [`README.md:945`](../../../README.md) — are rewritten to
describe the re-pause loop. `README.md:912` additionally loses its claim that the phase stays
`Error`; `updateStandaloneStatus`
([`valkey_controller.go:2098-2099`](../../../internal/controller/valkey_controller.go#L2098-L2099))
reassigns `OK` in the same pass. **Option I (`statePaused`) is filed as its own item** rather
than dropped: it is the version in which those sentences become true at the mechanism instead
of at the text, and it needs an ADR 0010 re-decision on whether a state that ends only at a
spec change is bounded at all.

**D5 — The false convergence proof is corrected, the behaviour at the completion branch is
not. Decided by default, reversible.** [`rolling_update.go:301-305`](../../../internal/controller/rolling_update.go#L301-L305)
and [ADR 0002](../../adr/0002-surface-a-blocked-reconcile-on-the-cr.md) D10 `:219-223` claim the
`verifyTopologyRestored` completion sites are entered only through
`updatedCount == totalPods`. Verified false: the second caller
([`:3318`](../../../internal/controller/rolling_update.go#L3318)) sits in
`dispatchMultiReplicaState`, whose only caller
([`:3303`](../../../internal/controller/rolling_update.go#L3303)) is **after** that block closes.
The sentence is corrected in both places. Gating `:309` on the same convergence helper was
declined for scope: it would also constrain the pre-existing `clearSidecarUpdatePending` call
and change a condition T15 is not about.

**D6 — The form is forced by the complexity budget.** `checkAndHandleRollingUpdate` measures
**exactly 15** and `make cyclo` is `gocyclo -over 15` ([`Makefile:57`](../../../Makefile#L57),
`CYCLO_THRESHOLD ?= 15` at [`:377`](../../../Makefile#L377)). Every clear is an unconditional call
to a helper that carries the branch; an inline `if` at either site turns CI red.

**D7 — The registry row is the `SidecarUpdatePending` row, and the registry does not enforce
it.** `TestConditionRegistryEdgesHaveAPresenceGuardedClear` reads struct fields only — the
identical row would pass under Option A. The five probes (N, N2, P, Q/Q2, G-live) are the
enforcement, and a unit test that merely calls the new helper on a condition-free CR is
**not** a guard: it exercises a function written in the same commit and cannot fail on HEAD.

**D8 — The `declaredGap` text and ADR 0027 `:34-35` are corrected while being removed.** Both
say `pauseRollingUpdate` is "reachable on every topology". It is not: the `default:` dispatch
arm (`replicas <= 1 && !sentinel`) reaches `handleStandaloneRollingUpdate`, whose call graph
contains no pause site. Two topologies, not three.

**Not yet implemented.**


### Implementation 2026-08-26 — DONE

Option H′ shipped as decided.

**Code.**
- `clearRollingUpdatePaused(ctx, v, reason, message)` — presence-guarded, next to
  `clearSidecarUpdatePending` in
  [`valkey_controller.go`](../../../internal/controller/valkey_controller.go).
- `clearRollingUpdatePausedIfConverged(ctx, v, readyPods, tierSize)` — the gate, carrying the
  one branch.
- `readyOne(pod)` in [`rolling_update.go`](../../../internal/controller/rolling_update.go) —
  `isPodReady` as an addend rather than a branch, so the ordinal loop stays a plain
  statement. It exists **only** because of the complexity budget, and its comment says so.
- The loop counts `readyPods`; the converged early return calls the gated clear, the
  completion branch the plain one.
- The unguarded `False` write inside `finalizeRollingUpdate` is **deleted**, with a comment
  in its place saying why it is not merely moved.

Two new reasons in `api/v1`: `ReasonRollingUpdateCompleted = "Completed"` (unchanged value,
so a CR already carrying the old write sees no spurious transition) and
`ReasonRollingUpdateConverged = "Converged"`.

**Probes — six, three red before the change:**

| Probe | Test | Before |
|---|---|---|
| N | `..._CompletionClearsRollingUpdatePaused` | **red** |
| N2 | `..._ConvergedEarlyReturnClearsRollingUpdatePaused` | **red** |
| G-live | `TestFinalizeRollingUpdate_DoesNotAddThePausedConditionToACleanCluster` | **red** |
| P | `..._KeepsThePauseWhileAnOutdatedPodRemains` | green, must stay |
| Q | `..._KeepsThePauseWhenAPodIsMissing` | green, must stay |
| Q2 | `..._KeepsThePauseWhenAPodIsNotReady` | green, must stay |

plus `..._DoesNotAddThePausedConditionToACleanCluster` for the blast radius of the new clears.
All in
[`rolling_update_paused_condition_test.go`](../../../internal/controller/rolling_update_paused_condition_test.go).
Q and Q2 are the probes the gate exists for, and they are green under HEAD as well as after —
they fail only under the **ungated** variant, which is the point of D2.

**Docs.** ADR 0002: **D10b** added; D10's convergence-proof sentence struck in place with the
correction (`verifyTopologyRestored` is reachable via `dispatchMultiReplicaState` with
`updatedCount != totalPods`); "whose own clear gap is a separate open item" marked closed; a
second `Amended` stanza; three new References. ADR 0027: the `RollingUpdatePaused` Context
bullet struck where it says "every topology" and "cleared only by `finalizeRollingUpdate`",
plus D4, Consequences, Alternatives, Residual risks, Status. Then `condition_registry.go`
header, `CLAUDE.md`, `README.md` `:912`-area and the `RollingUpdatePaused` row.

**D4 — the four resume-semantics sentences, corrected in text.**
[`api/v1/valkey_types.go`](../../../api/v1/valkey_types.go) (the `ConditionTypeRollingUpdatePaused`
doc, rewritten and extended with both clear reasons), the `pauseRollingUpdate` function
comment and its two inline comments, `README.md` `:912` (which also loses "phase `Error`" as
a durable claim) and the `RollingUpdatePaused` condition row. Each now describes the re-pause
loop and points at **T23** for the version that makes the halt real.

**D5 held:** the false proof is corrected in the code comment and in ADR 0002; the behaviour
at the completion branch is unchanged.

**Gates:** `make test-unit`, `make lint`, `make cyclo`, `make test-integration` green.
`checkAndHandleRollingUpdate` still measures **exactly 15**. No e2e run — no cluster.

## T16: `StorageSpecNotApplied` has two evaluators, and the second one clears what the first reported

**Status: DONE 2026-08-26 — Option A (tier-aware clear) + registry shape A2, implemented,
unit- and integration-verified. Ships with T15 as one PR, three commits — see the Decision section at the end.**
~~OPEN — un-deferred 2026-08-26. Ranked FIRST again, still paired with T15~~
(~~THE recommended next item~~ → ~~Ranked SECOND behind T21 as of 2026-08-26 ~09:40~~ — T21 was
found during the T8 execution, carried higher severity and a dated trigger, went first, and
**shipped the same day**; this is THE recommended next item again).**
~~deferred 2026-08-26~~ — declared as a gap in `conditionRegistry`. **Severity: medium** — on
a Sentinel cluster the condition ADR 0023 exists to raise ends the pass reporting `False`,
which is a false claim of health rather than a missing signal. Found 2026-08-25 while
building the T6 condition inventory. Latent on wds18: gitlab-valkey is blocked by a
`volumeMounts` error, not a `volumeClaimTemplates` conflict, so it carries no such condition.
**Effort: S.** See the Decision section at the end of this item for the recommended shape,
the one ownership call it needs, and the shortcut that must not be taken; line references in
the body were corrected on 2026-08-26 and the corrections are marked in place.

`guardVolumeClaimTemplates`
([`volumeclaim_conflict.go:73-103`](../../../internal/controller/volumeclaim_conflict.go#L73-L103))
writes the condition in its two conflict arms and **unconditionally clears it in the
`default:` arm** — and it has two independent callers:

- the data StatefulSet, [`valkey_controller.go:1243`](../../../internal/controller/valkey_controller.go#L1243)
- the Sentinel StatefulSet, [`valkey_controller.go:1364`](../../../internal/controller/valkey_controller.go#L1364)

Step order is fixed and verified: `{name: "StatefulSet"}` at
[`:497`](../../../internal/controller/valkey_controller.go#L497) runs before
`{name: "Sentinel resources"}` at
[`:498`](../../../internal/controller/valkey_controller.go#L498), and `runReconcileSteps`
continues past a failing step (ADR 0001). The Sentinel StatefulSet has no
`volumeClaimTemplates` at all (its config is an `emptyDir`), so its guard always lands
in `default:` and calls `clearStorageSpecNotApplied`.

**Consequence:** on any Sentinel-enabled cluster whose *data* tier has a claim
conflict, the pass reports the conflict and then clears it, and the CR ends the pass
with `StorageSpecNotApplied=False`. Last writer wins and the last writer is the one
that never has anything to say. ADR 0023 D4's claim that the Sentinel call "costs
nothing" is false in that shape.

**Proposed fix (not decided):** either key the condition per StatefulSet kind, or
accumulate both evaluations in `passState` (which already exists for exactly this kind
of per-pass fact, ADR 0019 D3) and write once at the end of the pass. ADR 0023 D4 must
be amended in the same change.

> **Line refs corrected 2026-08-26** against `HEAD` = `1c309d8` — this item was written
> before `2051a34`/`75b3c92`. Callers are
> [`valkey_controller.go:1241`](../../../internal/controller/valkey_controller.go#L1241) (data)
> and [`:1362`](../../../internal/controller/valkey_controller.go#L1362) (Sentinel), not
> `:1243`/`:1364`; the reconcile steps are `:495`/`:496`, not `:497`/`:498`. The
> `default:` arm that clears unconditionally is
> [`volumeclaim_conflict.go:99-101`](../../../internal/controller/volumeclaim_conflict.go#L99-L101).
> Mechanism re-verified unchanged.

### Decision

**Deferred 2026-08-26, deliberately and not by omission.** Same reasoning as T15: filed as a
finding, no implementation recommendation, not part of the recommended set.

`StorageSpecNotApplied` now carries a **declared gap** in `conditionRegistry` naming T16,
with `evaluators: 2` recorded explicitly — so the racing-evaluator shape is stated in the
code and the guard that would otherwise fail on it is suppressed traceably
([ADR 0027](../../adr/0027-conditions-are-levels-edges-or-history.md) D4). ADR 0027's Context
names this item as one of the two defects that writing the inventory produced, which is the
argument for the registry existing at all.

Severity check before deferring: latent on the inspected fleet. gitlab-valkey is blocked by a
`volumeMounts` error rather than a `volumeClaimTemplates` conflict, so it carries no such
condition, and no other CR has a claim conflict. **Note ADR 0023 D4 still contains the claim
this item disproves** ("the Sentinel call costs nothing"); it is left standing because
amending it belongs with the fix, and this line is the record that it is known to be wrong.

- **2026-08-26, second pass: un-deferred. This is the recommended next item, with T15.**
  Re-verified on `HEAD` = `1c309d8`; line refs corrected in place above.

  **Why it outranks everything else open.** In the *parameter-conflict* shape — a changed
  `size` or `storageClass` — `guardVolumeClaimTemplates` returns `nil`
  ([`:97`](../../../internal/controller/volumeclaim_conflict.go#L97)) and does **not** block the
  reconcile, so the cluster stays `phase=OK`. The condition is then the **only durable
  statement** the CR makes about storage, and it is wrong. The Warning Event that would
  otherwise carry it expires — outliving the Event is precisely what ADR 0023 D5 built the
  condition for. This is a false *positive* claim of health, not a missing signal, and it is
  wrong on the majority topology.

  It is also the visibility half of the refusal that stands between a user and the T10
  wedge: T10's own re-review concluded that fixing T16 buys more practical safety than
  anything filed under T10 itself.

  **Recommended shape: (a) tier-aware clear — either tier may REPORT, only the data tier may
  CLEAR.** Add a `mayClear` parameter to `guardVolumeClaimTemplates`, pass `true` at
  [`:1241`](../../../internal/controller/valkey_controller.go#L1241) and `false` at
  [`:1362`](../../../internal/controller/valkey_controller.go#L1362), gate the `default:` arm on
  it. Preferred over the `passState` accumulation because it is ~20 lines against a new
  per-pass field, and preferred over a second condition type because that adds permanent CRD
  surface for a feature that does not exist.

  **The one thing that must be settled, and it is the only reason this is not strictly
  cheaper than T15:** `condition_registry.go` records `evaluators: 2`. The docstring at
  [`:55-57`](../../../internal/controller/condition_registry.go#L55-L57) already anticipates
  exactly this case — "a race unless an ownership rule says which one is authoritative" — so
  either add an ownership field or redefine the count as "evaluators that may clear". Then
  drop `declaredGap` and set `clearSite` to name the data tier.

  **Trap — do not take the shortcut.** "Skip the clear when both desired and live claims are
  empty" looks equivalent and is not:
  [`test/e2e/persistence_migration_test.go:210-228`](../../../test/e2e/persistence_migration_test.go#L210-L228)
  reverts persistence to `false` and then requires `False`/`StorageSpecApplied` at a moment
  when both sides are empty. That clear is load-bearing for the data tier.

  **Definition of done, and it is provable in the unit tier — no e2e needed.** A Sentinel CR
  with a data-tier claim conflict, driven through `reconcileResources` (**not** a single
  reconcile step — driving one step is why no existing test catches this), asserting
  `StorageSpecNotApplied` is still `True`/`RecreateRequired` at the end of the pass. Write it
  first and watch it go red.

  **Docs in the same change**, per the CLAUDE.md ADR rule: ADR 0023 `:108-112` D4 is the
  sentence this item disproves and it is standing as current text in a **tracked** file —
  mark it superseded in place with a Status date, do not delete it. Then ADR 0027 `:188-192`,
  `CLAUDE.md:380`, and this item's own status line.


### Option analysis 2026-08-26 (third pass) — superseded by the Decision and Implementation sections below

Re-verified against `HEAD` = `5af86d2`. **The line refs corrected above drifted again:** the
data caller is
[`valkey_controller.go:1251`](../../../internal/controller/valkey_controller.go#L1251), the
Sentinel caller [`:1376`](../../../internal/controller/valkey_controller.go#L1376), and the
now-false Sentinel call-site comment is `:1371-1375`. Step order `:495`/`:496` is correct. The
mechanism itself re-verified unchanged.

**Three facts the item did not carry, all measured:**

1. **The condition flips twice per pass, forever.** `writeStatusCondition` re-`Get`s the CR
   into `v` ([`valkey_controller.go:2537`](../../../internal/controller/valkey_controller.go#L2537)),
   so the presence guard in `clearStorageSpecNotApplied` finds the `True` the data tier just
   stored and clears it. Two `Status().Update`s and two `LastTransitionTime` moves per
   reconcile on every affected cluster, not one stale value.
2. **`reconcileSentinelResources` is not a nested `runReconcileSteps`** — it is a fail-fast
   chain ([`:1269-1286`](../../../internal/controller/valkey_controller.go#L1269-L1286)). A
   failing Sentinel ConfigMap or headless Service skips the guard entirely. That matters for
   the fixture *and* for the residual risk below.
3. **Every single-condition option treats "this tier did not run" as "this tier agreed".**
   Measured on a patched tree with the recommended option applied, Sentinel ConfigMap failing
   on pass 2: `pass1 = True/RecreateRequired`, `pass2 = False/StorageSpecApplied`. Option B
   (`passState` accumulation), implemented including its "nothing evaluated" branch, produces
   **byte-identical** output — its guard covers "no tier evaluated", not "one agreed and the
   other never ran". B's claimed advantage over A is therefore refuted; they are twins on this
   path. A third case nobody listed: with a **foreign** data StatefulSet neither tier
   evaluates ([`:1219`, `:1226`, `:1229`, `:1244`](../../../internal/controller/valkey_controller.go#L1219))
   and a `True` stands indefinitely. That one needs a residual-risk sentence, not code.

#### Options

| # | Option | LOC | Verdict |
|---|---|---|---|
| 1 | **A — tier-aware clear: `mayClear bool` on `guardVolumeClaimTemplates`, `true` at the data caller, `false` at the Sentinel one** | ~6 code + 14 comment | **recommended** |
| 2 | D — delete the Sentinel guard call outright | −9 prod, −46/+12 test | only option with a negative net; **reverses ADR 0023 D4** |
| 3 | F — tier-scoped verdicts in `passState`, single writer at the end of `reconcileResources` | ~80 prod, ~120 test | the only shape measured to survive a skipped step; buys it for an unreachable tier |
| 4 | C1 — a second condition type `SentinelStorageSpecNotApplied` | ~50 prod | self-enforcing via the registry; permanent public API surface for a tier that cannot conflict |
| 5 | H — move the report into its own reconcile step | ~45 | ADR 0023 D3 pins detection in front of the drift check → four detections per pass from two `Get`s that can disagree |
| 6 | B — accumulate in `passState`, write once | ~60 | **demoted**: measured identical to A on correctness, hard-fails five tests, and writes **no** condition at all in the single-step test idiom |
| 7 | G — tier in the reason/message | small | the **only** option that is not upgrade-neutral: a CR already carrying `True` has no tier stamp |
| 8 | I — re-derive in `updateStatus` | — | **refuted** by `condition_registry.go:143-147`: every arm of `reconcileWorkload` returns early while a roll is in flight |
| 9 | E — skip the clear on empty-vs-empty | small | **refuted**: [`test/e2e/persistence_migration_test.go:213-228`](../../../test/e2e/persistence_migration_test.go#L213-L228) requires the clear at exactly that moment. The trap this item warned about is real |

**Option D is not independent of A.** D deletes A's only mutation guard: the mutant that gates
the *whole* guard on `mayClear` rather than only its `default:` arm is caught by exactly one
test — `TestReconcileSentinelStatefulSet_RefusesAClaimTheSpecNoLongerAsksFor`
([`volumeclaim_conflict_test.go:357-402`](../../../internal/controller/volumeclaim_conflict_test.go#L357-L402)),
the one D removes.

**Two things A must carry or it is half a fix:**

- **An order-pinning test.** A's ownership rule depends on the data step running before the
  Sentinel step and **nothing pins that order**. The only order assertion in the repo is
  `TestResourceReconcileSteps_RBACBeforeStatefulSet`
  ([`reconcile_steps_test.go:219-236`](../../../internal/controller/reconcile_steps_test.go#L219-L236));
  swapping `:495`/`:496` inverts the rule and the whole unit suite stays green (measured).
  ~12 lines in the same idiom.
- **A repaired sibling test.** `TestReconcileSentinelStatefulSet_ReportsNoConflictOnAHealthySentinelCluster`
  claims in its comment (`volumeclaim_conflict_test.go:339-342`) to catch "an unconditional
  clear". After `mayClear=false` that half is unreachable — measured: the mutant that disables
  the presence guard no longer makes it fail. Seed a `True` into that fixture and fix the
  comment in the same edit.

#### Tests — three, not one

- **Full pass** (pins the cross-step rule): `reconcileResources` on an `haCluster` CR with
  persistence enabled, a data StatefulSet built **without** claims, **and a pre-created
  Sentinel StatefulSet**. The Sentinel object is load-bearing: without it the test is green on
  unfixed HEAD, because `:1352-1355` creates and returns before the guard. Needs a positive
  control in the `require.True(..., "the fixture must present real drift")` idiom already used
  at `volumeclaim_conflict_test.go:383-386`. Measured red on HEAD, green after; the
  parameter-conflict twin measured `err=nil` + `False` on HEAD.
- **Single step**: seed `StorageSpecNotApplied=True`, live Sentinel StatefulSet, call
  `reconcileSentinelStatefulSet` alone. Measured red on HEAD. This **corrects** the claim in
  the second-pass note above that only a `reconcileResources` test can catch it — the
  single-step test discriminates too, in 12 lines. Write both: one catches the guard, the
  other the ordering.
- **Order pin**, as above.

#### Docs owed in the same change

ADR 0023: `:109-110` ("compares empty against empty and costs nothing") marked **superseded in
place**, `:110-112`'s rationale kept; a new **D4a** carrying the ownership rule and the two
residual risks (a Sentinel report has no clear owner; the zero-evaluator case); the file's
**first** `Amended` stanza; `:11-15` must name the topology the 2026-08-23 Kind verification
ran on — it was non-Sentinel
([`persistence_migration_test.go:100`](../../../test/e2e/persistence_migration_test.go#L100)),
which is precisely why this survived. ADR 0027: Context `:41-45`, D4 `:91-92`, Consequences
`:133-135`, Alternatives `:163-168`, Residual `:185-192`, Status — plus **D1 `:62`** and
**D2 `:71-77`** if the registry gains an ownership field (Q1). Then
`condition_registry.go:14-16`, `CLAUDE.md:372`, `:379-381`, `README.md:832`, `:949` (both are
**false today on a Sentinel cluster** and become true), and this item's status line.

**Bundle the owed ADR 0023 correction.** `grep -c 0028 docs/adr/0023-*.md` returns **0**,
while `:171` ("is not fixed here") and `:288` ("It needs its own ADR and its own fix") stand
as false statements about a decision ADR 0028 already made. T16 opens that file anyway. This
is the row the index already carries under "Corrections owed in tracked files".


### DECIDED 2026-08-26 (third pass) — Option A, registry shape A2

Decided by Hans on 2026-08-26 after the option analysis above was tabled. Not yet implemented.

**D1 — Option A ships: the tier-aware clear.** `guardVolumeClaimTemplates` gains a
`mayClear bool`; the `default:` arm clears only when it is set; the data caller
([`valkey_controller.go:1251`](../../../internal/controller/valkey_controller.go#L1251)) passes
`true`, the Sentinel caller
([`:1376`](../../../internal/controller/valkey_controller.go#L1376)) passes `false`.

*Why A over D:* D removes the very guard ADR 0023 D4 built against a future author's
inattention, and that argument has not become weaker just because it catches nothing today —
it is the ADR 0020 shape, "a new managed object inherits the rule, not an exemption". D also
deletes A's only mutation guard. *Why A over F and C1:* both buy correctness for a Sentinel
tier that cannot conflict through any operator-written path; the day someone changes the
builder they meet the `mayClear` parameter. *Why A over B:* measured identical on correctness,
five hard test failures, and it makes the condition invisible to the single-step test idiom
this package documents.

**D2 — A does not close the class, and the ADR says so.** ADR 0023 gains **D4a** carrying the
ownership rule *and* two residual risks stated plainly: a Sentinel-tier report has no clear
owner of its own, and with a **foreign** data StatefulSet neither tier evaluates and a `True`
stands indefinitely. Neither is argued away.

**D3 — Registry shape A2.** `evaluators` stays **2**; a new `ownershipRule` field names which
evaluator decides; `TestConditionRegistryLevelsHaveOneEvaluator`
([`condition_registry_test.go:141-151`](../../../internal/controller/condition_registry_test.go#L141-L151))
accepts more than one evaluator **only** when `ownershipRule` is non-empty. `declaredGap` is
dropped. A1 (recount as "authoritative evaluators") was rejected as dishonest: both call sites
still reach `reportStorageSpecNotApplied`
([`volumeclaim_conflict.go:81`](../../../internal/controller/volumeclaim_conflict.go#L81),
[`:90`](../../../internal/controller/volumeclaim_conflict.go#L90)) and the last writer still owns
Reason and Message. Consequence: ADR 0027 **D1 `:62`** ("exactly one evaluator") and
**D2 `:71-77`** (what a row declares) are amended in the same change — the escape the field
docstring at [`condition_registry.go:55-58`](../../../internal/controller/condition_registry.go#L55-L58)
already posed is now implemented instead of only described.

**D4 — Two beigaben are part of the fix, not follow-ups.** (a) an order-pinning test in the
`TestResourceReconcileSteps_RBACBeforeStatefulSet` idiom — nothing pins `:495` before `:496`
today and swapping them silently inverts the ownership rule; (b) the repair of
`TestReconcileSentinelStatefulSet_ReportsNoConflictOnAHealthySentinelCluster`, whose comment
claims to catch "an unconditional clear" — after `mayClear=false` that half is unreachable, so
the fixture gains a seeded `True` and the comment is rewritten.

**D5 — No integration leg. Decided by default, reversible.** Flipping `claimGuardCR`
([`test/integration/volumeclaim_conflict_test.go:446-458`](../../../test/integration/volumeclaim_conflict_test.go#L446-L458))
to Sentinel-enabled was considered and declined: what T16 is about is **step order inside the
operator**, which ADR 0017 assigns to the unit tier — the integration tier covers what only a
real API server decides. Two consequences that must land anyway: the comment there
("a Sentinel tier would only add objects to wait for") is itself an artefact of this bug and is
corrected, and ADR 0023's amendment states the fix is **unit-verified only**. Say so if the
integration leg is wanted after all; it is one field.

**D6 — The owed ADR 0023 → ADR 0028 correction rides along.** `grep -c 0028` on that file
returns 0 while `:171` and `:288` state as current that the recorded-master-wins demotion
"needs its own ADR and its own fix". ADR 0028 is that ADR. T16 opens the file anyway.

**Not yet implemented.** Definition of done unchanged from the analysis above: three tests
(full pass, single step, order pin), the ADR 0023 / ADR 0027 edits, the registry row and its
test-guard change, `condition_registry.go:14-16`, `CLAUDE.md:372`/`:379-381`,
`README.md:832`/`:949`, and this status line.


### DECIDED 2026-08-26 — T15 and T16 ship as one PR, three commits

The code is independent (zero overlap). Five prose surfaces are shared, and the ADR 0027
Residual-risks bullet at `:188-192` names **both** gaps in one sentence.

**The arithmetic trap that decides it.** There are **three** `declaredGap` rows, not two:
[`condition_registry.go:94`](../../../internal/controller/condition_registry.go#L94) (`Ready`,
T18), `:118` (T16), `:160` (T15). ADR 0027 says "two" in every place it counts, and
`git log -S"T18:"` shows that row was added in `75b3c92`, the commit that created ADR 0027 —
so the number was wrong the day it was written. If either item ships alone, "two declared
gaps" becomes *accidentally* correct and the natural "two → one" edit produces a **new** false
statement.

**Shape:** `fix(storage): ...` (T16 code, its three tests, ADR 0023, the T16 registry row and
the `ownershipRule` test-guard change), `fix(rolling-update): ...` (T15 code, the five probes,
ADR 0002, the T15 registry row, the four resume-semantics corrections), then
`docs(conditions): ...` doing ADR 0027, `condition_registry.go:14-16`, `CLAUDE.md:379-381`,
the README rows and this ticket **once**, from the final state, with the count corrected to
name `Ready`/T18 as the one remaining gap. The first two commits stay independently reviewable
and revertable; the shared prose is written once rather than shifted twice.


### Implementation 2026-08-26 — DONE

Option A shipped as decided, with one deviation from D4, marked below.

**Code.** `guardVolumeClaimTemplates`
([`internal/controller/volumeclaim_conflict.go`](../../../internal/controller/volumeclaim_conflict.go))
takes `mayClear bool` and gates its `default:` arm on it; the data caller passes `true`, the
Sentinel caller `false`, and both call-site comments were rewritten — the Sentinel one said
the call "costs nothing", which is the claim this item disproves.

**Registry (shape A2).** `conditionOwnership` gains `ownershipRule`; the
`StorageSpecNotApplied` row keeps `evaluators: 2`, names the rule, updates `clearSite` and
drops `declaredGap`. `TestConditionRegistryLevelsHaveOneEvaluator` now skips a row that names
a rule, and the new `TestConditionRegistryOwnershipRulesAreEarned` fails a rule claimed on a
single-evaluator row — the same traceability `declaredGap` gets from its ticket reference.

**Tests, all three, red before and green after:**

| Test | File | What it pins |
|---|---|---|
| `TestReconcileResources_SentinelTierDoesNotClearTheDataTiersStorageConflict` | `volumeclaim_conflict_test.go` | the structural shape, through a full `reconcileResources` pass — the step order |
| `TestReconcileResources_SentinelTierDoesNotClearAParameterConflict` | same | the shape that is **not** blocked, where the condition is the only durable signal |
| `TestReconcileSentinelStatefulSet_DoesNotClearAConflictItDidNotFind` | same | the guard itself, in twelve lines |
| `TestResourceReconcileSteps_StatefulSetBeforeSentinelResources` | `reconcile_steps_test.go` | the step order the ownership rule rests on (D4a) |

Both `reconcileResources` fixtures carry a **pre-created Sentinel StatefulSet** and a
positive control. Without the Sentinel object the Sentinel reconciler creates it and returns
before the guard, and the test is green on the unfixed code.

**Deviation from D4(b).** The sibling test
`TestReconcileSentinelStatefulSet_ReportsNoConflictOnAHealthySentinelCluster` did **not**
gain a seeded `True`. Its comment claimed to catch "an unconditional clear", which after
`mayClear=false` is unreachable from that site; seeding a condition there would have
duplicated `DoesNotClearAConflictItDidNotFind`. The comment was corrected instead, and it
names the test that carries that half now.

**Docs, all in the same change.** ADR 0023: D4's "costs nothing" struck in place, **D4a**
added with the ownership rule and three residual risks (a Sentinel report has no clear owner;
the zero-evaluator case; unit-verified only), the first `Amended` stanza, the Status
verification note now saying the 2026-08-23 Kind run was **non-Sentinel** — which is why it
passed while the defect stood — and five new References. **D6 discharged:** `:235` and `:352`
no longer state that the recorded-master-wins demotion needs its own ADR; both are struck in
place and point at ADR 0028. ADR 0027: D1's table row and its amendment paragraph, D2, D4,
Consequences, Alternatives, Residual risks, Status. Then
`condition_registry.go` header, `CLAUDE.md`, `README.md` `:832`-area and the
`StorageSpecNotApplied` row, and the misleading `claimGuardCR` comment in
`test/integration/volumeclaim_conflict_test.go` (D5).

**Gates:** `make test-unit`, `make lint`, `make cyclo`, `make test-integration` all green.
`guardVolumeClaimTemplates` complexity 3 → 4. No e2e run — no cluster.

## T23: `pauseRollingUpdate` records no pause — it clears the state and re-arms a fresh budget

Moved on 2026-09-27 to [023-pauserollingupdate-records-no-pause.md](../023-pauserollingupdate-records-no-pause.md).

## T24: `TLSMaterialStale=False` can mean "one tier is fine and nobody looked at the other"

**Severity: medium at filing. Status: DONE 2026-08-27, all four letters.** Filed 2026-08-26
by the adversarial review of ADR 0030. What shipped, per letter: (a) measurability is AND-ed
one way — stale pods in a readable tier are reported even while the other tier is
unmeasurable, the all-clear needs both; (b) the record-less legacy population is named, not
absorbed — reason `TLSMaterialUnmeasured` on a `False`, message naming the pods a rotation
will never replace, stale > unmeasured > current precedence; the exemption from the roll
itself stands, deliberately (ADR 0030 D8/D9); (c) `ensureTLSMaterialRecord` case 2 — an
unreadable Secret inherits the persisted record instead of stripping it (ADR 0030 D12);
(d) the `when: IsTLSEnabled` step gate is gone and the evaluator retracts a standing True
with `TLSMaterialNotApplicable`, presence-guarded in both directions. The original analysis
follows unchanged.

**a) Measurability is OR-ed across the tiers.** `scanTLSMaterial`
([`internal/controller/tls_material.go:185-196`](../../../internal/controller/tls_material.go#L185-L196))
does `measured = measured || tierMeasured`, and `scanTierTLSMaterial` returns
`(nil, false, nil)` for a tier whose Secret it cannot read
([`:223-226`](../../../internal/controller/tls_material.go#L223-L226)). On a Sentinel cluster in
non-unified cert-manager mode, an absent or unreadable `<name>-sentinel-tls` while `<name>-tls`
is fine therefore yields `TLSMaterialStale=False` with
`"Every pod runs the TLS material currently in its Secret"` — about a tier nothing inspected.
The function's own doc comment
([`:106-109`](../../../internal/controller/tls_material.go#L106-L109)) states the opposite rule, so
this is a defect against a stated contract rather than an undocumented gap.

**b) An un-annotated Sentinel pod lands in the same message.** The scan skips a pod whose
`vko.gtrfc.com/tls-material-hash` is empty
([`:244`](../../../internal/controller/tls_material.go#L244)) — correct as upgrade neutrality, and
the same guard the roll uses. But the Sentinel tier has **no container of the operator's**
(`internal/builder/sentinel.go:344`, `:509` — both `v.Spec.Image`) and its StatefulSet is
`OnDelete`, so no operator upgrade ever replaces those pods. The window is closed by the user's
next `spec.image`, config or resource change, not by ours, and while it is open the CR reports
the all-clear.

The data tier does not have shape (b): its pods carry the sidecar, so the next operator upgrade
rolls them and they gain the annotation.

**Shape of a fix, not decided.** Either make the verdict per tier — a tier that could not be
measured suppresses the `False` rather than being absorbed into it — or keep one condition and
make the message name the tiers it covers and the pods it could not judge. The second is
cheaper and keeps one series; the first is the honest one if an operator is meant to alert on
it. `ValkeyTLSMaterialStale` fires on `status="True"` only
([`deploy/helm/valkey-operator/templates/prometheusrule.yaml`](../../../deploy/helm/valkey-operator/templates/prometheusrule.yaml)),
so today neither shape alerts.

**c) An unreadable Secret stops an in-flight roll and reports nothing.** `stampTLSMaterialHash`
returns without stamping when the hash is empty
([`:69-72`](../../../internal/controller/tls_material.go#L69-L72)), and pod-template annotations are
compared by **full equality** (`stringMapChanged`,
[`internal/builder/statefulset.go:1302-1312`](../../../internal/builder/statefulset.go#L1302-L1312)),
so a desired template without the annotation is drift: `reconcileStatefulSet` overwrites the live
template without it, `tlsMaterialHashFromSts` then returns `""`, and `podTLSMaterialHashChanged`
returns false for every pod. A rotation roll that was in flight stops, and the same pass reports
nothing because that tier is unmeasurable. Reachable by pointing `spec.tls.secretName` at a
Secret that does not exist yet, or by a briefly absent cert-manager Secret. **This is the only
conditionally present pod-template annotation** — the config and pod-spec hashes are written
unconditionally, which is why the pattern is safe for them.

**d) The condition has no writer once TLS is disabled.** The reporting step carries
`when: IsTLSEnabled` ([`valkey_controller.go:506`](../../../internal/controller/valkey_controller.go#L506)),
which is what keeps a non-TLS cluster from ever gaining the condition. It also means a cluster
that carried `True` and then turns TLS off keeps it for life, with `ValkeyTLSMaterialStale`
firing on it indefinitely. The registry row records the gate as doing the presence guard's job;
it does so in one direction only. Same shape as T15, and it was missed by the same reasoning.

**Recorded in [ADR 0030](../../adr/0030-rotating-certificates-rotate-the-instances-that-cannot-reload-them.md)**
D8, D9 and its Residual risks rather than left to this file — the ADR describes the mechanism
and must not describe it as sounder than it is.

**Not verified:** whether (a) is reachable on the live fleet. Every inspected cluster uses the
unified certificate or has both Secrets present.

## T25: the TLS material fingerprint is a change detector being read as a control

**Severity: medium. Status: DONE 2026-08-27. Filed 2026-08-26 by the adversarial review of
ADR 0030, decided 2026-08-27 morning, built the same day.** Both decided options landed, C then
B, with A folded into both. Part (b) — the half a data-pod container could exploit — is closed.
Part (a), the Secret writer, is **accepted and documented**, decided the same day after a
re-examination that falsified one of the three grounds on which option D had been rejected: see
[T25(a), decided](#t25a-decided-2026-08-27-accepted-and-documented). The execution record, the two deviations the user decided, and what was verified how, are
in [Execution](#execution-2026-08-27).

**The analysis and decision below are kept as written.** Where the execution changed or
falsified something, it is marked in place, not rewritten.

Two ways the fingerprint can be made
to lie, and the second one needs no privilege the cluster does not already hand out.

**a) The digest is forgeable by whoever can write the Secret.** `ComputeTLSMaterialHash`
([`internal/builder/tls_material.go:37-52`](../../../internal/builder/tls_material.go#L37-L52)) is
FNV-1a **32-bit**, and `tls.key` is hashed **last**. Every PEM parser ignores bytes after the
block, so appending a few inert bytes to `tls.key` lets any chosen digest be hit by search in
seconds. The material changes, `podTLSMaterialHashChanged` stays false, no roll happens, and
`TLSMaterialStale` stays `False`. That principal can replace the cluster's TLS identity anyway
— what this buys is **evasion of the report**, which matters because
`SECURITY_ARCHITECTURE.md` now lists that report on the hardening checklist.

**b) The annotation is patchable from inside a data pod.** `BuildSidecarRole` grants
`pods: patch` on this cluster's data pod names
([`internal/builder/rbac.go`](../../../internal/builder/rbac.go)), and the data pod spec does
**not** set `automountServiceAccountToken: false` — only the observer does
([`internal/builder/observer.go:139`](../../../internal/builder/observer.go#L139)). So
`valkey-server` and the third-party `redis_exporter` carry that token. One patch setting the
annotation to the desired value suppresses the roll **and** the detector, because both read the
same field. It is the third forgeable field of that grant, next to `instanceRole` and the drain
stamp that [`SECURITY_ARCHITECTURE.md`](../../security/isolation-and-tenancy.md#what-does-not-hold) section 3 already
enumerates — and that enumeration did not list it.

**The decision this needs, before any code.** Is the fingerprint meant to be *only* a change
detector — in which case the fix is documentation, which has now landed, plus adding the field
to the compromised-sidecar enumeration — or is it meant to survive a hostile writer? The second
is a different mechanism: a cryptographic digest closes (a) and closes nothing about (b), which
needs the pods to stop mounting a token that can patch them, or the operator to keep the
expected fingerprint somewhere a pod cannot write. **Do not reach for SHA-256 and call it
fixed:** it addresses the weaker half of the problem.

**Already done, 2026-08-26:** ADR 0030 D11 and `SECURITY_ARCHITECTURE.md` sections 2 and 9 now
state plainly that this answers "did the roll happen" and never "is the material unchanged".
That removes the false claim; it does not remove the gap.

~~**Not verified:** whether `automountServiceAccountToken: false` on the data pods is viable at
all — the sidecar needs the token, and it runs in that pod.~~

> **Answered 2026-08-27, and the answer is yes.** See the analysis below: the per-container
> token split is a standard, GA-since-1.20 Kubernetes pattern, it was implemented end to end in
> a throwaway working tree and verified against a real kube-apiserver, and the claim that
> Kubernetes does not offer it — carried in three places in this repository — is false.

### Analysis, 2026-08-27

Ten agents, read-only against `4a8b92e` (branch `fix/bad-findings`), the HEAD at the time, plus
one that built and ran the suite in a throwaway tree and reverted it. `bebd71b` landed during
the analysis and touches documentation only, so every code reference below is unaffected.
**Nothing was run against a cluster.** Every claim below carries its file:line; the ones that
were not verified say so.

**The load-bearing finding reshapes the option list: deleting the annotation beats forging
it.** Both consumers carry a presence rule — `podHash != "" && podHash != desiredHash`
([`rolling_update.go:393-399`](../../../internal/controller/rolling_update.go#L393-L399)) and
`recorded != "" && recorded != desired`
([`tls_material.go:244`](../../../internal/controller/tls_material.go#L244)). A single merge patch
setting the key to `null` therefore makes the pod **unmeasured**: the roll skips it and the
condition does not name it. No collision is needed, and consequently **every scheme that keeps
the carrier in pod `metadata` is defeated regardless of digest strength** — SHA-256, an HMAC and
an opaque generation counter alike. Compounding it, every data-pod container mounts
`ca.crt`/`tls.crt`/`tls.key` itself, so it can compute any *unkeyed* desired digest without help.

**The "third forgeable field" count in this item is wrong; it is roughly the seventh.** The same
`pods: patch` grant also reaches `config-hash` and `pod-spec-hash`
([`rolling_update.go:419-441`](../../../internal/controller/rolling_update.go#L419-L441)),
`metadata.ownerReferences` and `metadata.finalizers` — neither is in apimachinery's immutable
set, which is exactly `name, namespace, uid, creationTimestamp, deletionTimestamp,
deletionGracePeriodSeconds` (`k8s.io/apimachinery@v0.36.4/pkg/api/validation/objectmeta.go:329-334`,
read in the module cache) — and `spec.containers[*].image`, which is one of the five entries in
`updatablePodSpecFields` (`k8s.io/kubernetes@v1.36.4/pkg/apis/core/validation/validation.go:5691-5697`).
Whichever option lands must correct that sentence in `SECURITY_ARCHITECTURE.md:156-162` rather
than inherit it.

**One agent claim was refuted before it reached this file.** The Sentinel-rollout-complete
verdict at [`valkey_controller.go:1705`](../../../internal/controller/valkey_controller.go#L1705)
does trust `pod.Labels[appsv1.StatefulSetRevisionLabel]` to gate deleting the legacy Sentinel TLS
Secret, but it iterates the **Sentinel** StatefulSet's pods (`sts.Name` is `<cr>-sentinel`,
`:1688`), and `SidecarRolePodNames` grants only `<cr>-0 … <cr>-N`
([`rbac.go:92-116`](../../../internal/builder/rbac.go)). Sentinel pods run the namespace `default`
ServiceAccount ([`sentinel.go:368`](../../../internal/builder/sentinel.go)). Not reachable by the
data-pod token. **Do not file it.**

#### The five options, and why three of them lost

**A — accept and finish the documentation.** XS, closes nothing, and it is a *component* of
every other option rather than an alternative to them: `SECURITY_ARCHITECTURE.md:255-261` still
enumerates only `instanceRole` and the drain stamp, while `:156-162` points at that list as if
it already had the third entry.

**B — move the carrier from pod `metadata` into pod `spec`.** Inject
`VKO_TLS_MATERIAL_HASH=<hash>` into the sidecar container (data tier) and the sentinel container
(Sentinel tier) at exactly the site that stamps the template annotation today
([`tls_material.go:62-79`](../../../internal/controller/tls_material.go#L62-L79), called at
[`valkey_controller.go:1216`](../../../internal/controller/valkey_controller.go#L1216) and
[`:1349`](../../../internal/controller/valkey_controller.go#L1349)). `env` is **not** in
`updatablePodSpecFields`, so the API server rejects any later change from any principal —
including a compromised **sidecar**, which is the one container option C cannot help. Preserves
ADR 0007 D2 (the desired side stays the persisted StatefulSet template), preserves the presence
guard (absent env = unmeasured, so ADR 0005 and ADR 0030 D8 are untouched), needs no RBAC, no
CRD change, no new object and no operator-held record. Two auflagen: inject **after**
`BuildStatefulSet` so `ComputePodSpecHash`
([`statefulset.go:1123-1129`](../../../internal/builder/statefulset.go#L1123-L1129)) does not also
move — otherwise one rotation produces two signals — and never use an image reference as the
carrier, because image is the one spec field this grant may rewrite. Generalises for free to
`config-hash` and `pod-spec-hash`, which are forgeable by the identical mechanism today. Closes
(b); closes nothing about (a).

**C — scope the ServiceAccount token to the sidecar container.** `automountServiceAccountToken:
false` on the data pod spec plus a hand-declared projected volume (`serviceAccountToken` +
`kube-root-ca.crt`) mounted only into the sidecar. Removes the token from `valkey-server`, both
init containers and the third-party `redis_exporter`, and therefore closes the **whole class**
for those containers at once — all three hashes, `instanceRole`, the drain stamp,
`ownerReferences`, `finalizers` and the image swap. Verified by implementing it end to end in a
throwaway tree, building it, running the full suite and an envtest round-trip against a real
kube-apiserver 1.31, then reverting: `rest.InClusterConfig` reads only `token` and `ca.crt` from
the default path ([`labeler.go:207`](../../../internal/sidecar/labeler.go#L207); path constants in
`k8s.io/client-go@v0.36.4/rest/config.go:545-546`) and refreshes the bearer token from
`BearerTokenFile` every 60 s; the preStop drain hook is a pure filesystem poll; both init
containers shell out to `valkey-cli` only, and `RequiredImageTools` names no `kubectl`. Leaves
(a), and leaves the sidecar itself — which must hold the grant by ADR 0012 D8.

**D — cryptographic digest. Rejected.** Three independent grounds, any one sufficient.
(1) At today's width the change is inert: `ComputeTLSMaterialHash` emits `%08x` over `Sum32()`
([`tls_material.go:51`](../../../internal/builder/tls_material.go#L51)), and ~2^32 SHA-256
evaluations over appendable trailing bytes is seconds. The security parameter is the truncation
width, and the option never names it. (2) Even at full width it closes nothing against the
Secret writer, because the cheap suppression is hash-agnostic: write the previous content back
byte-identical and every hash function agrees. (3) It rolls the **Sentinel** tier on a plain
operator upgrade — the one pod class ADR 0005 D11 says never rolls on an upgrade — and reports a
fleet-wide false `TLSMaterialStale` in the same pass, because the presence guard exempts pods
with *no* annotation, not pods carrying a previous-generation one. It would also make the
password brake unstateable: five documents phrase the refusal in terms of width and weakness
(CLAUDE.md, ADR 0030 D11 and its residual, ADR 0016 D12's amendment,
`SECURITY_ARCHITECTURE.md:138-146` and `:709-714`), and a strong digest turns this into the
blessed-looking mechanism. ADR 0030's own warning — *"Do not reach for SHA-256 and call it
fixed"* — is upheld.

**E — operator-held record plus an unforgeable timestamp. Rejected.** The unforgeability premise
holds (`creationTimestamp` is validated immutable, objectmeta.go:333, verified in the module
cache), and the proposal still loses. It breaks **ADR 0007 D2**: today's predicate is
self-satisfying, because the statefulset-controller recreates the pod from the very template
being compared, which is why
[`rolling_update.go:220-222`](../../../internal/controller/rolling_update.go#L220-L222) can say a
recreated pod is up to date the instant it appears. A temporal inequality between an
apiserver-stamped `creationTimestamp` and an operator-stamped record has no such guarantee:
clock skew beyond the 75 s `terminationGracePeriodSeconds` yields a pod-delete loop, one
controlled failover per requeue, unbounded — the exact failure
[`rolling_update.go:497-506`](../../../internal/controller/rolling_update.go#L497-L506) was written
to foreclose. It also only relocates (b), since both consumers gate on `podIsOurs`; record loss
must adopt unarmed for upgrade neutrality, making amnesia indistinguishable from health on
precisely the failure the condition exists to report; and it makes T24 worse, because a
timestamp has no "cannot tell" state while ADR 0007 D3 requires one.

### Decision

**Decided 2026-08-27: C first, then B, with A folded into both. D and E are rejected and are to
be recorded as rejected alternatives in ADR 0030 so they do not return.**

**Why C first.** It is the only option on the list that **shrinks the trust set** instead of
hardening one field inside an unchanged one. The realistic hostile container is `valkey-server`,
which processes client traffic, or `redis_exporter`, which is third-party and unaudited — not
our own sidecar binary. C disarms both, and it does so for eight fields rather than one. It also
falsifies a standing claim rather than merely adding to the record.

**Why B second and not instead.** B is cheap and closes the one gap C cannot: a compromised
sidecar, which by ADR 0012 D8 must keep `pods: patch`. It also generalises to `config-hash` and
`pod-spec-hash` at no extra cost. It is second because it hardens a single field while C removes
a capability.

**Why A is not a decision on its own.** The section 3 enumeration is incomplete today and the
count is wrong; correcting it is owed by whichever change lands, not instead of one.

~~**Scope of this pass, deliberately:** ticket and decision only. **No code, and no edit to
`SECURITY_ARCHITECTURE.md` or the ADRs yet** — ADR 0030's residual still reads `(T25, open)`,
which remains true until C lands. That is a choice, not an oversight, and the obligations it
defers are enumerated below so the next change carries them.~~ *(Superseded by the build the
same day. Every obligation in the two tables below was discharged; the ADR 0030 residual is
marked closed with the two corrections it earned, and the tables now carry a Done column.)*

#### What C owed when it was built — all discharged 2026-08-27

| Obligation | Where | Done |
|---|---|---|
| Fix the real defect the option ships with | `podSpecChanged` never compares `AutomountServiceAccountToken`. The initial upgrade lands because `volumesChanged` sees 2 volumes become 3, but flipping automount back to `true` on the live StatefulSet is then never converged back. Four lines, reusing `automountsToken` exactly as `ObserverDeploymentHasChanged` does. | Yes — and it covers **both** tiers, because `SentinelStatefulSetHasChanged` routes through the same `podTemplateChanged`. Pinned by `TestPodSpecChanged_AutomountFlagIsCompared` (four cases, incl. the nil default) and one convergence test per tier. |
| Mark superseded **in place**, not delete | ADR 0012's last residual: *"a token-projection split that Kubernetes does not offer per container."* False. | Yes — struck through in place with a `(Closed 2026-08-27, and the reasoning above was wrong.)` paragraph, plus a Status amendment. A second residual was **added**: the sidecar container itself still holds the grant and must. |
| Same false claim, two more places | `SECURITY_ARCHITECTURE.md` section 3 and the hardening row. | Yes — section 3 bullet struck through in place, hardening row flipped to `[x]` and its false premise named. |
| ADR shape | Step 4 belongs in D8 in place. No new ADR for C. | Yes. No new ADR and no index row for C. (ADR 0031 exists for **B**, which is a new durable rule and was always scoped to get one.) |
| Volume naming | Must not start with `kube-api-access-`; mount path exactly `/var/run/secrets/kubernetes.io/serviceaccount`. | Yes — `sidecar-api-access`. The prefix rule was **verified in the module cache** rather than assumed: `mountServiceAccountToken` adopts the first volume with that prefix and mounts it into every container (`admission.go:423`, k8s.io/kubernetes@v1.36.4). Both pinned by their own test. |
| Do not add the downwardAPI namespace projection | client-go never reads the namespace file; the sidecar uses `POD_NAMESPACE`. | Yes — two sources only, asserted (`assert.Nil(src.DownwardAPI)`). |
| Append the volume unconditionally | `buildPodSpec` sits at the gocyclo ceiling of 15. | Yes — unconditional append. `make cyclo` green. |
| Test churn, measured | `statefulset_test.go` `:66` volumes 2→3, `:823` Empty→1, `:843` 1→2. | **Measured exactly right.** Those three failed and nothing else did. |
| Accept and name one gap | Single-replica clusters do not get the fix on the upgrade — `isSidecarOnlyChange` looks only at container images, so the sidecar-image bump carrying this change suppresses the replacement that would apply it. | Named, not fixed. Recorded in ADR 0012 D8 step 4. Blast radius: one pod whose sidecar Role names only itself. |
| Cost to state plainly | One fleet roll on the operator upgrade via the pod-spec hash. | Stated in D8 step 4 — **plus a Sentinel-tier roll**, which the plan did not anticipate and the user accepted (see Execution). |

#### What B owed when it was built — all discharged 2026-08-27

| Obligation | Where | Done |
|---|---|---|
| New durable rule ⇒ **new ADR 0031** | *A per-pod record the operator trusts lives in pod `spec`, not pod `metadata`.* Plus an index row. | Yes — [ADR 0031](../../adr/0031-a-record-the-operator-trusts-lives-in-pod-spec.md), indexed under Security and API surface. Its D6 records that `config-hash` and `pod-spec-hash` stay in metadata as a **filed follow-up, not a decided non-goal**. |
| Amend | ADR 0030 D4 and ADR 0020. | Yes, and two more the plan did not list: **ADR 0007 D2** (see below) and **ADR 0016**, whose amendment quotes the annotation name. ADR 0020 gained **D10**: provenance of an object is not provenance of a field. |
| Injection site | After `BuildStatefulSet`, so `ComputePodSpecHash` does not move with it. | Yes — `builder.StampTLSMaterialHash` mutates the built StatefulSet. Pinned by `TestStampTLSMaterialHash_DoesNotMoveThePodSpecHash` **and** its counterpart `..._IsSeenByTheStatefulSetComparison`, because "does not move the hash" is only half the requirement. |
| Inherited, unchanged | T24(c) moves with it verbatim: a conditionally present template field is still drift when the Secret is unreadable. | Confirmed unchanged. B swapped which field is conditionally present; the shape is identical and T24(c) stays open. |
| Fixture churn | The pod builders in `internal/controller/tls_material_test.go` set env instead of annotations. | Yes, plus a **second** builder the plan did not anticipate: `tlsTierPodLegacy`, which produces the pre-move shape the new fallback exists for. Without it the fallback would have been untested code. |

#### Owed by whoever touches either, and pre-existing — both discharged 2026-08-27

~~**ADR 0007 D2 undercounts its own inputs.**~~ **Fixed.** D2 said "Four are named values"; it is
five, and `tlsMaterialHashFromSts` is now registered there by name, along with a Status
amendment recording that the rule it states never changed — that input always did come from the
persisted template.

~~**There is zero e2e coverage of certificate rotation.**~~ **Fixed, and this was the largest
single gap in the item.** `TestE2E_TLS_CertificateRotation_RollsTheFleet`
([`test/e2e/tls_rotation_test.go`](../../../test/e2e/tls_rotation_test.go)) creates a 3-replica TLS
cluster, writes a canary, deletes the Secret so cert-manager reissues with a fresh key, and then
asserts: the template fingerprint moves, **every** data pod is replaced by UID and comes back
carrying the new record, `TLSMaterialStale` returns to False with reason `TLSMaterialCurrent`,
the canary survives, and the roll raises **no Warning Event** (ADR 0025 D7). It also asserts the
two new security properties where a real kubelet built the pod: `automountServiceAccountToken`
is false, exactly one container mounts the token path, and every pod carries an `instanceRole`
label — which is the only end-to-end proof that the scoped token still authenticates, since that
label is the sidecar's single API write.

The test uses **literals, not `internal/builder` constants**, unlike the first draft. No other
e2e in this repository imports the operator's own packages, and the reason holds: a test that
reads the constant agrees with a renamed carrier instead of catching it.

### Execution, 2026-08-27

Built in two commits, C then B, in the order the decision set. Everything below was run in this
repository; the e2e was run **against the local Kind cluster**, which makes this the first T25
pass that touched a cluster at all.

#### The two deviations from the plan, both put to the user and decided by them

**1. B needed a migration path the plan did not have.** The plan said "move the carrier".
Moving it plainly would have left the **Sentinel tier silently unmeasured**: Sentinel pods carry
no sidecar, so a plain operator upgrade never rolls them (ADR 0005 D11); they would keep the
annotation, the new reader would find no env, the presence rule would call them unmeasured, and
a certificate rotation in that window would neither replace them nor report them — for as long
as nothing else rolled the tier. That is precisely the silent failure ADR 0030 exists to
prevent, reintroduced by the change meant to harden it.

Three options were offered. The user chose the **read-side fallback**: `RecordedTLSMaterialHash`
reads the env first and the annotation only when there is no env. It is self-extinguishing — the
roll it enables replaces the pod with one carrying the env — and it is not a way back in for a
forger, because a pod with the env never consults the annotation and every pod the operator
writes from now on has one. Recorded as ADR 0031 D5 with the reasoning, not as a code comment.

**2. Sentinel pods mount a `default`-ServiceAccount token they never use.** A new finding while
reading `sentinel.go` for the carrier work, not in the ticket: the Sentinel pod spec names the
namespace `default` ServiceAccount and sets no automount flag, so kubelet mounts a token into a
tier that runs `valkey-sentinel` and `valkey-cli` and nothing else. The `default` SA usually
carries no RBAC, but its token is a valid cluster identity — anything bound to
`system:authenticated` or to that SA by a cluster admin.

Offered as "file it as T27" or "fix it now". The user chose **fix it now**, so no item was
opened for it; the number T27 went to the unrelated fresh-cluster finding the e2e turned up
later the same day. The cost is stated plainly in ADR 0012 D8 step 4: **one Sentinel-tier roll on the
operator upgrade**, for every Sentinel cluster, TLS or not. ADR 0005 D11 records the Sentinel
tier as the one pod class a plain upgrade does not roll; this is a deliberate one-time exception
against that boundary, not an oversight.

**A third, smaller deviation, taken without asking.** The decision said D and E were to be
recorded as rejected alternatives *in ADR 0030*. They are in **ADR 0031** instead, under
Alternatives Considered, because that is the decision they were alternatives to — ADR 0031 did
not exist when the sentence was written. ADR 0030 points at them from its closed residual, so
neither is findable only by knowing where to look.

#### What the e2e caught that nothing else could

The token projection shipped with `DefaultMode: 0o420`. Unit tests, envtest, `make lint`,
`make cyclo` and `make gosec` were all green, the API server accepted the pod spec, and the
sidecar **could not start**:

```
sidecar error: creating pod patcher: getting in-cluster config:
open /var/run/secrets/kubernetes.io/serviceaccount/token: permission denied
```

Octal `0420` is `r---w----`. The `420` that appears in every manifest example is the **decimal**
form of `0644`, and writing it as a Go octal literal silently produces a different mode. Every
data pod went `CrashLoopBackOff` within 60 s of the first real kubelet building one. Fixed to
`0o644` — what the API server defaults a projected volume to anyway — and pinned by an assertion
that names the trap. **This is the argument for the e2e in one paragraph:** the gap the ticket
called out as "zero e2e coverage of certificate rotation" was hiding a defect on the first run.

#### What was verified, and how

| Claim | How |
|---|---|
| The token reaches the sidecar and nothing else, on six topologies | `TestBuildStatefulSet_TokenReachesTheSidecarAndNothingElse`, asserting on the *mount path* rather than the volume name — a mount of anything there is a token as far as client-go is concerned |
| The projection is what `rest.InClusterConfig` reads | `TestSidecarTokenVolume_ReproducesWhatInClusterConfigReads`; the two path constants read out of `client-go@v0.36.4/rest/config.go:545-546` in the module cache, not from memory |
| The `kube-api-access-` prefix rule is real | Read `mountServiceAccountToken` at `k8s.io/kubernetes@v1.36.4/plugin/pkg/admission/serviceaccount/admission.go:423` — the plugin **adopts** such a volume and mounts it into every container. Pinned by its own test |
| Sentinel pods carry no token | `TestBuildSentinelStatefulSet_MountsNoTokenAtAll` |
| The automount flip converges on both tiers | `TestPodSpecChanged_AutomountFlagIsCompared` plus one `...HasChanged` test per tier |
| The API server accepts the projection | `TestSidecarTokenProjection_IsAcceptedByTheAPIServer_Integration` (envtest) — the round-trip is the assertion |
| **The API server refuses to change pod env and accepts any change to pod annotations** | `TestTLSMaterialCarrier_TheAPIServerRefusesToChangeIt_Integration` — the load-bearing claim of ADR 0031, settled by a real kube-apiserver rather than asserted. The annotation is patched **and deleted** successfully; the env patch fails with `spec: Forbidden` |
| One rotation is one signal | `TestStampTLSMaterialHash_DoesNotMoveThePodSpecHash` and `..._IsSeenByTheStatefulSetComparison`, as a pair |
| The whole chain on a live cluster | `TestE2E_TLS_CertificateRotation_RollsTheFleet` — **green, 279 s, all eight subtests**, against the local Kind cluster with cert-manager 1.17.2. Measured in it: template fingerprint `02eed8d8` -> `d1f40b78` after the reissue, all three data pods replaced in 51 s, canary intact, `TLSMaterialStale=False/TLSMaterialCurrent`, zero Warning Events |
| Repository gates | `make lint`, `make cyclo`, `make gosec`, `make test-unit`, `make test-integration` and `make test-image-tools` all green; `make generate-all` leaves the tree clean |

#### T25(a), decided 2026-08-27: accepted and documented

The (b) half was built. The (a) half — the hostile Secret writer — was re-examined the same day
and **accepted rather than closed**, by the user, after the re-examination changed what the
options were.

**What the re-examination found.** Option D had been rejected on three grounds. One of them
does not hold, and it had been copied into ADR 0031 and ADR 0030 hours earlier by this work:

| Ground as recorded | Verdict |
|---|---|
| "Writing the previous content back byte-identical satisfies every hash function, so a strong digest closes nothing against the Secret writer" | **Wrong.** That is a *denial of rotation*, not a forged fingerprint: the material really was reverted and the digest really does say so. No content digest can ever detect it — that needs `notAfter`, which ADR 0030 D7 refuses on purpose. The actual (a) attack is an **identity swap by collision**: write your own key and cert plus trailing bytes until the FNV-32 matches, roughly 2^32 trials, seconds. A wide cryptographic digest closes exactly that. |
| "At today's width the change is inert; the option never names the truncation width" | **Fair.** The width is the whole security parameter and D never named it. 128 bits of SHA-256 would do. |
| "It rolls the Sentinel tier on a plain upgrade and reports a fleet-wide false `TLSMaterialStale`" | **Real, and solvable** — with the pattern this very change shipped: version the record (`v2:<hex>`) and treat a previous-generation value as *unmeasured* rather than stale, the same presence-rule widening as ADR 0031 D5. |
| "It makes the password brake unstateable" | **A wording bug, and fixing it strengthens the brake.** The refusal is about the **entropy of the input**, not the width of the digest: SHA-256 of a 12-character password is a marginally slower oracle, not a safe one. Five documents phrased it as "a 32-bit digest of …", which read as though a wider hash would make the password case fine. |

**Why it was still accepted.** Not because the digest does not work — it does. Because of what
is left afterwards: the attacker loses the **silent** swap and gains one **indistinguishable
from a legitimate rotation**. Both produce one fleet roll and one `TLSMaterialStale` transition,
and there is no observer for whom those two differ. The detection has no consumer. And the
principal in question can replace the cluster TLS identity outright either way.

**The ceiling of (a) is lower than the item suggested**, and that is worth stating once so
nobody re-opens it expecting more: *nothing here authenticates the material*. A strong digest
turns a silent substitution into a visible one. Reading the served leaf off the handshake
(T28) answers whether the running process presents what its mount holds — not whether the mount
is right, because once a swap has propagated the leaf and the Secret agree again. Raising the
ceiling needs a **trust anchor outside the Secret**, comparing the served leaf against the
issuer this operator asked for. No decision has been taken to build one.

**What landed for (a), 2026-08-27:** documentation only, no code.

* ADR 0030 D11 records the acceptance with its reason, and names the two counter-arguments that
  do **not** hold so they are not reused.
* The same D11 corrects the entropy framing; the correction is carried into
  `SECURITY_ARCHITECTURE.md` (section 2 and the hardening row), `CLAUDE.md` and ADR 0016.
* ADR 0031 Alternatives Considered is rewritten: the digest is *irrelevant to that decision*
  (the deletion attack is hash-agnostic) rather than rejected on a wrong argument, and the two
  wrong arguments are corrected in place.
* ADR 0030 residual and ADR 0031 residual both move from "open" to "accepted".

#### What is still open, deliberately
* **The sidecar container still holds `pods: patch`,** and must (ADR 0012 D8). What changed is
  that reaching it means compromising the operator image, not `valkey-server` or a third-party
  exporter. Recorded as a **new** residual in ADR 0012, because the old one was retracted and
  the honest replacement is narrower, not absent.
* **`config-hash` and `pod-spec-hash` are still in pod metadata.** ADR 0031 D6 calls them a
  filed follow-up rather than a decided non-goal, and names the reason each is its own change:
  `pod-spec-hash` is self-referential, and `config-hash` should not ride along in a TLS change.
* **Not verified: the downgrade direction.** Nothing here runs an older operator against a pod
  template carrying the env. It was reasoned about — an older reader sees no annotation, calls
  every pod unmeasured, and that is the safe direction — but not measured.

#### Considered and not taken

Recorded so they are not re-proposed. **HMAC keyed by an operator-held secret** closes (a) and
half of (b) but dies on the deletion attack, and costs a key that must survive restarts and
leader election; it is worth remembering the day someone wants a *password* rotation
fingerprint, because it is the construction that would make that safe. **Content-addressed
immutable TLS Secrets** (`<name>-tls-<fingerprint>`, mounted by name so `pod.spec.volumes`
becomes the unforgeable record) is the only idea that removes ADR 0030's failure instead of
detecting it — rejected because the operator holds `secrets: get;list;watch;delete` and would
need cluster-wide `create;update`, a worse grant than the bug, and because it collides with the
`unifiedCertificate` migration and with a user-supplied Secret name. **The
`controller-revision-hash` label** is a label and rides the same grant. **A `pods/status` pod
condition** works and needs cluster-wide `pods/status: patch` on the operator — a fleet-wide
readiness-manipulation primitive — plus a Pod watch this operator deliberately does not have.
**An RBAC field or label selector** does not exist in Kubernetes; `resourceNames` is the only
object-level narrowing and it is already used. **A second pod for the exporter** leaves
`valkey-server` holding the token and adds a network hop and a second TLS identity.

Two ideas were **not** rejected and are better filed separately than folded in here.
**Filed 2026-08-27 as T28 and T29**, because an idea left inside a DONE item is an idea nobody
finds again:

1. **Read the leaf off the TLS handshake the operator already performs.** `valkeyclient.dial`
   does `tls.DialWithDialer` and the operator dials every data pod every pass
   ([`checker.go:180-217`](../../../internal/health/checker.go), TLS port at `:414-433`); a per-pod
   clone of the `*tls.Config` with a `VerifyConnection` hook captures `PeerCertificates[0]`. It
   is the only mechanism that observes the running process rather than a proxy for it — which is
   the exact failure ADR 0030 exists for — and it would **measure D6's open question** (do
   `valkey-server` and `valkey-sentinel` reload?) for free. Today that unknown costs every
   metrics-disabled TLS cluster a roll that may be unnecessary.
2. **A chart-shipped `ValidatingAdmissionPolicy`, default off.** The only in-Kubernetes control
   reaching `ownerReferences`, `finalizers` and `spec.containers[*].image`. ADR 0015 D2 refuses
   admission *webhooks*, and its stated reason is a measured outage of a third-party webhook
   backend; a VAP has no backend to lose, so D2's reasoning does not transfer and D2 would need
   an explicit amendment rather than a silent stretch. VAP is GA from 1.30 and
   [`README.md:42`](../../../README.md) declares a 1.29+ floor, so it is opt-in or a floor bump.

## T28: measure what the pods actually serve, off the handshake the operator already performs

**Severity: low as a defect, high as a lever. Status: DONE 2026-08-27 as the instrument,
deliberately not as the D6 answer** — `observeServedCertificate`
([`internal/health/served_certificate.go`](../../../internal/health/served_certificate.go))
arms every health-pass dial with a report-only `VerifyConnection` hook: mismatch between the
served leaf and the Secret's `tls.crt` logs at Info, a match at V(1). The three open
questions the analysis posed are answered minimally: the verdict lands in the **log** (no
condition, no registry row, and no metric — a gauge written from a reconcile pass is the
ADR 0021 constraint); on a mismatch **nothing acts** (ADR 0030 D4 stands unamended); and
one measurement is **not** enough to move D6 — the fleet log capture across a rotation
window is still owed, recorded in ADR 0030 D6. Never fails a handshake, proven against a
real TLS listener in `served_certificate_test.go`. Filed 2026-08-27 out of T25, where
it sat inside the prose of a DONE item. **It also decides T27's severity** - if
`valkey-server` reloads, a metrics-free TLS pod has no pinning container at all and T27's `high`
is unearned. Not a bug report — a mechanism that would
answer a question this repository has been paying for since ADR 0030 shipped.

**The question it answers.** ADR 0030 D6 treats a process whose reload behaviour has never been
measured as pinning: `valkey-server` and `valkey-sentinel` are both unmeasured, and so **every
TLS cluster rolls on every certificate rotation**, every 60 days at cert-manager defaults. If
`valkey-server` in fact re-reads its material, that roll is unnecessary for every TLS cluster
that does not enable metrics — the exporter is the only container in the pod that provably
pins, and it is opt-in. D6 says plainly: *"Measuring it is a way to remove a roll, never a
prerequisite for having one."* Nothing has measured it.

**Why it is nearly free.** The operator already opens a TLS connection to every data pod on
every pass. `Client.dial` calls `tls.DialWithDialer(dialer, "tcp", c.addr, c.tlsConfig)`
([`internal/valkeyclient/client.go:399`](../../../internal/valkeyclient/client.go#L399)), and the
health checker builds one client per probe through a seam the unit tier already fakes,
`NewValkeyClientFn` ([`internal/health/checker.go:383`](../../../internal/health/checker.go#L383)).
A per-call clone of the `*tls.Config` carrying a `VerifyConnection` hook captures
`PeerCertificates[0]`. Comparing its serial or its own fingerprint against the leaf in the
mounted Secret says whether the process running right now presents the material its mount
currently holds.

That is the **only** mechanism in the design that observes the running process rather than a
proxy for it — which is the exact failure ADR 0030 exists for, and which the fingerprint can
only ever approximate by "was the pod replaced".

**What it does not do,** so it is not oversold: it does not authenticate the mount. Once a
hostile Secret swap has propagated, the served leaf and the Secret agree again, so this is not
a repair for T25(a) (accepted). It answers "did the roll reach the process", not "is the
material the right material".

**Open questions before it can be scoped.**

* Where the verdict lands. A new condition is a row in `conditionRegistry` and a decision about
  level-versus-edge (ADR 0027); a log line is cheap and invisible; a metric is a third option.
* What happens on a mismatch. Reporting is safe. *Acting* on it — rolling a pod because its
  served leaf is stale — is a new replacement trigger, and ADR 0030 D4 says **no new
  replacement mechanism was introduced, and none may be**. That sentence would need an
  amendment, or the answer is "report only".
* Whether one measurement is enough to move D6. Answering "valkey-server reloads" from one
  Valkey line on one issuer is thin; the D6 asymmetry is deliberately conservative.

## T29: a chart-shipped ValidatingAdmissionPolicy, default off

Moved on 2026-09-27 to [029-a-chart-shipped-validatingadmissionpolicy-default-off.md](../029-a-chart-shipped-validatingadmissionpolicy-default-off.md).

## T27: the pods of a freshly created TLS cluster carry no fingerprint and are never rolled by a rotation

**Severity: high. Status: open, filed 2026-08-27 out of the T25 build. Effort: M, and it needs a
decision before it needs code.** Not caused by T25 — the superseded annotation behaved
identically. **Measured on a cluster**, which is what makes it different from every other
open item here.

### What was measured

Local Kind cluster, cert-manager 1.17.2, operator built from `HEAD` of the T25 work,
`TestE2E_TLS_CertificateRotation_RollsTheFleet`. A 3-replica TLS cluster with a cert-manager
issuer, created from nothing:

```
tls_rotation_test.go:115: Recorded fingerprints before the rotation:
    map[tls-rotation-0: tls-rotation-1: tls-rotation-2:]
```

Three pods, `2/2 Running`, `phase: OK`, TLS working — and **not one of them carries a TLS
material fingerprint**. `kubectl get pod tls-rotation-0 -o jsonpath='{.spec.containers[?(@.name=="sidecar")].env[*].name}'`
returns `POD_NAME POD_NAMESPACE` and nothing else. The StatefulSet template carries the record
correctly; the pods do not.

### Why

`reconcileResources` creates the `Certificate` and the `StatefulSet` in the same pass.
cert-manager needs a second or two to issue, so at StatefulSet-creation time
`r.tlsMaterialHash` reads a Secret that does not exist yet, returns `""`, and
`stampTLSMaterialHash` deliberately writes nothing — the documented "cert-manager has not
issued yet" path. The StatefulSet controller creates the three pods from that template
immediately (`podManagementPolicy: Parallel`); they block on the missing Secret volume and
become Ready the moment cert-manager issues.

The next reconcile stamps the record onto the template. **Nothing rolls the pods**, and
nothing should by today's rules: the presence guard exists precisely so that a pod carrying no
record is unmeasured rather than stale, which is the whole of ADR 0030's and ADR 0005's
upgrade-neutrality story.

The result is a cluster where:

* a certificate rotation moves the template and **replaces nothing** — the pods keep the
  material they parsed at startup, which is the exact failure ADR 0030 exists to prevent;
* `TLSMaterialStale` stays `False` with reason `TLSMaterialCurrent`, because unmeasured is not
  stale, so the one signal meant to catch "a roll that never starts" reports health;
* the condition clears itself only in the sense that it never fired.

It ends the first time anything else replaces the pods — an operator upgrade (the sidecar image
moves, ADR 0005 D11), a spec change, a manual delete. A cluster created and then left alone
never leaves this state.

### Why it is worse than it looks

The whole fleet is exposed, not a legacy subset. Every TLS cluster this operator has ever
created passed through this window; only the ones that were subsequently changed left it.
ADR 0030's residual list has ~~"a pre-upgrade Sentinel pod is exempt until the user changes
something (T24)"~~, which is the *upgrade* shape of this; the fresh-cluster shape is not
recorded anywhere and is strictly larger.

It also makes the T25 work look better than it is on a fresh cluster: the carrier is now
unforgeable, on pods that have no carrier.

### The window is wider than "cluster creation" — three doors, one defect

Verified in the code on 2026-08-27 by a 27-agent analysis sweep (6 mappers, 5 costed option
designs, 3 adversarial lenses each, one synthesis) against `4ebe070`. The measurement above
stands unchanged; what it did **not** say is that the same record-less template reaches pods
through two further doors, so a fix that closes only the creation path closes one instance and
not the mechanism:

* **a) Creation.** The measured case. `Certificate` and `StatefulSet` are written in the same
  pass ([`valkey_controller.go:492`](../../../internal/controller/valkey_controller.go#L492) vs
  [`:495`](../../../internal/controller/valkey_controller.go#L495)), and `reconcileCertificate`
  never waits for issuance.
* **b) `spec.tls.enabled: false → true` on a serving cluster.** There is no CEL and no
  validating webhook (`grep XValidation api/v1 config/crd/bases` finds nothing; there is no
  `*webhook*` file), so the flip is accepted, the template is written record-less in the same
  pass the `Certificate` is created, and `reconcileWorkload` runs immediately after
  `reconcileResources` ([`:259`](../../../internal/controller/valkey_controller.go#L259)/[`:268`](../../../internal/controller/valkey_controller.go#L268))
  — so the pod-spec-hash roll deletes pods **against that record-less template**, in that pass.
  A cluster that was serving data lands in the T27 state.
* **c) Any pass that cannot read the Secret strips the record off the live template.** `desired`
  without the env vs `current` with it is drift through
  [`envVarsEqual`](../../../internal/builder/statefulset.go#L1431), so
  [`:1265`](../../../internal/controller/valkey_controller.go#L1265) writes the template back
  **without** the fingerprint. Every pod recreated in that window — drain, eviction, chaos kill,
  node loss — comes back record-less. This is **T24(c) through a different door**, and it is the
  reason the two items have to be decided together.

Two further corrections the sweep earned:

* **Option 3 as written is a no-op, not a wrong answer.** "Treat the pod as carrying the
  template's value" is exactly what the roll and the scan already do for a record-less pod: skip
  it and report `Current`. Adopting it changes no behaviour at all, so `armPodsWithTheTemplateRecord`
  would stay forever. Only a *latched* adoption is worth costing, and that one loses for other
  reasons (below).
* **Option 4's premise is false for two populations ADR 0005 D11 names itself**
  ([`0005-…:133-138`](../../adr/0005-upgrade-neutral-defaults-and-anti-affinity.md#L133-L138)):
  Sentinel pods carry no sidecar, and on the kustomize path or a Helm install with a floating
  `image.tag` the sidecar image is a static string. "Armed within one release" holds only for
  the canonical Helm upgrade of the data tier.

**The population is: armed template, record-less pods.** The stamp runs on every pass, so the
second pass arms the template and the env delta is real drift. Any design gated on "the template
has no record" therefore matches nothing — that kills the latched-adoption option outright.

**And one security fact reorders the option list.** `RecordedTLSMaterialHash` falls back to the
`vko.gtrfc.com/tls-material-hash` **annotation** whenever no container carries the env
([`tls_material.go:119-130`](../../../internal/builder/tls_material.go#L119-L130)). The sidecar is
the sole token holder in a data pod and holds `pods: patch` on every data pod name of its
cluster ([`rbac.go:66-113`](../../../internal/builder/rbac.go#L66-L113)), and it mounts the material
whose digest format is published in the same file. So **for any pod with no env record the roll
verdict is decided by an attacker-writable field.** Today that buys an attacker nothing, because
those pods are exempt anyway. It becomes a silent off-switch the moment a design adds a third
state that is *read only when the env record is absent*.

### The options, costed

The tension is unchanged and exact: **the presence rule cannot tell "this pod never had a
record" from "this pod predates the mechanism"**, and it must keep exempting the second or an
operator upgrade rolls the fleet for nothing.

#### A — Option 1′: arm the template with an explicit `unarmed` record

**Rule.** On a TLS cluster the carrier container always carries `VKO_TLS_MATERIAL_HASH`: the
Secret digest when readable, the value already persisted when not, and the literal `unarmed`
only when neither was ever known. A *desired* value of `unarmed` replaces no pod; `unarmed` on a
*pod* is replaced the first time a real digest reaches the template.

Three cases, over what is known rather than over lifecycle phase:

```
recorded := tlsMaterialHashFromSts(current)   // "" when the StatefulSet does not exist
hash     := r.tlsMaterialHash(ctx, v, secretName)
1. hash != ""                    -> stamp hash       // byte-identical to today
2. hash == "" && recorded != ""  -> stamp recorded   // an unreadable Secret never erases a record
3. hash == "" && recorded == ""  -> stamp "unarmed"
```

Case 2 shadows case 3 forever after the first successful write and is **on its own the fix for
door (c)** — it removes a write the operator performs today. Read side is one clause in
`podTLSMaterialHashChanged` ([`rolling_update.go:398`](../../../internal/controller/rolling_update.go#L398)):
`if desiredHash == "" || desiredHash == builder.TLSMaterialUnarmed { return false }`.
`podNeedsUpdate` and `sentinelPodNeedsUpdate` inherit it through that one function.
The sentinel cannot collide: `ComputeTLSMaterialHash` is `fmt.Sprintf("%08x", …)`, exactly eight
lowercase hex.

**Covers** all three doors, both tiers, both Secret sources. **Does not cover:** it is **not
retroactive** — every pod that exists today with no record stays exempt whatever the template
says, because the read side still requires `podHash != ""`.

**Cost.** ~60 lines over 4 files, no new `ConditionType`, no registry row, no RBAC, no extra API
call (the `Get` of `current` already happens one line below the stamp and is cache-served). ADRs:
0030 D8/D9/D4-block, 0031 D4, and **0007 D3 gains a third state** — the empty string is no longer
the only "cannot tell" value, and the comparison becomes deliberately asymmetric for this one
input. Two code details that are ADR-normative and easy to get wrong: read `current` for case 2
**after** `metav1.IsControlledBy` ([`:1241`](../../../internal/controller/valkey_controller.go#L1241)/[`:1372`](../../../internal/controller/valkey_controller.go#L1372)),
because ADR 0020 D10 is exactly "provenance of an object is not provenance of a field"; and gate
case 2 on `IsTLSEnabled`, or a phantom fingerprint pins itself onto a plaintext cluster with no
step left that could clear it.

**Strongest argument against, and it survived review.** The arming roll is not free and not
confined to empty clusters. On door (a) it is a roll of a fresh, empty cluster — cheap in data
terms but a *failover-aware* roll on a Sentinel topology is **two tier rolls** plus a spurious
`RollingUpdateComplete`/`SentinelUpdateComplete` pair (ADR 0024), and at least seven e2e sites
wait `testTimeout` = 5 min after creating a TLS cluster while the identical roll is budgeted at
12–15 min elsewhere in the same suite. On door (b) it is a **guaranteed double replacement** of a
serving cluster: the pod-spec-hash roll fires against the `unarmed` template, the replacements
come back armed, and the real digest rolls them again. Today the second roll does not happen —
because the replacements are unmeasured, which is the bug. The trade is correct and has to be
written down rather than discovered.

#### B — gate the create until the material exists (complement, or standalone runner-up)

**Rule.** A tier whose TLS Secret cannot be fingerprinted gets no StatefulSet, so no pod is ever
created from a record-less template on the create path.

**How.** Inside the existing `apierrors.IsNotFound` branch only
([`:1225-1228`](../../../internal/controller/valkey_controller.go#L1225-L1228),
[`:1358-1361`](../../../internal/controller/valkey_controller.go#L1358-L1361)), both tiers, both
Secret sources. Refuse without failing the pass — `requestRecheck` + `return nil`, the prior art
the foreign-object paths already use — so ADR 0002 D13 stays unwidened and the phase stays
`Provisioning` rather than `Error` on every fresh TLS cluster. Gate on the record already sitting
on `desired`, not on a second Secret read: a second read is a race that reproduces T27 under its
own fix.

**Covers** door (a) completely, and removes A's arming roll for the dominant path. **Does not
cover** doors (b) and (c) — both are *update* paths and a creation-only gate is inert there. So
**B does not close the mechanism, only the instance that was measured.**

**Cost.** A new `ConditionType` and therefore a **conditionRegistry row** (kind `level`, one
evaluator, no clear site). ADR 0001 D2 gains the sentence it already anticipates; ADR 0020 D2
gains an explicit extension for "a refusal expected to clear in seconds from an event the
operator already watches does not fail the pass". Breaks
[`test/integration/integration_test.go:571-610`](../../../test/integration/integration_test.go#L571-L610),
which creates a TLS CR with no cert-manager and no Secret and asserts the StatefulSet appears
within 10 s. It rests **entirely** on the Secret watch ([`:2853-2856`](../../../internal/controller/valkey_controller.go#L2853-L2856)):
`Provisioning` is not in the 10 s requeue set and `nudgeStatefulSet` forgets a NotFound, so
ADR 0030 D10 becomes load-bearing.

**Strongest argument against.** Its selling point is overstated. Today's failure for a
never-appearing Secret is *not* "visibly stuck in `ContainerCreating`" from the CR's point of
view: the StatefulSet exists, `status.replicas == spec.replicas`, the nudge deliberately stays
quiet, and the phase reads "Waiting for replicas to become ready" with no mention of TLS, the
Secret or the Certificate. B trades one uninformative message for another **unless** it ships the
condition — and that condition must not carry a raw API error into a tenant-readable CR message,
and must not enumerate which of the three keys are missing for a user-supplied `secretName`,
which would be a per-key existence oracle over an arbitrary Secret in the namespace. B also
stretches the ADR 0020 D9 orphan-adoption window from one reconcile to unbounded and externally
visible.

#### C — Option 4-strong: report the population nobody rolls (separable, and the only retroactive half)

**Rule.** The operator does not roll pods whose material it never recorded, and it says so on the
CR instead of reporting an all-clear.

**How.** A new reason on the **existing** `TLSMaterialStale` level: any stale pod →
`True/TLSMaterialRollPending`; else any owned, record-less pod → `False/TLSMaterialUnmeasured`
naming them; else `False/TLSMaterialCurrent`. Status stays `False`, so the shipped 72 h
`ValkeyTLSMaterialStale` alert (which matches `status="True"`) does not fire; a companion rule at
24 h would.

**This is the only option that covers the pods that exist today.** A and B are both
forward-only. **Does not cover:** nothing rolls — it converts a silent correctness hole into a
permanent operational chore, one alert per pre-existing TLS cluster until a human forces a roll.

**Cost.** S. No new `ConditionType`, no registry row, no CRD change, no new metric series
(`reason` is already a label on `vko_valkey_status_condition` and the alert already groups on it).
One alert rule in a chart that is default off.

**Strongest argument against.** It is the one part that is **not** status-neutral on upgrade:
every existing TLS cluster flips its reason on the first upgraded pass. That is a deliberate
ADR 0005 D10 exception and needs an explicit yes. And if C ships next to A, the level carries two
new reasons at once, so reason precedence must be deterministic — the identical-write guard
([`tls_material.go:141-149`](../../../internal/controller/tls_material.go#L141-L149)) compares the
reason, and a flapping one issues a `Status().Update` every pass and never lets `for: 72h`
accumulate.

#### D — a second env key (`VKO_TLS_MATERIAL_ARMED=1`) instead of a sentinel value

Same three states, carried in a separate key, so the fingerprint key keeps exactly two states and
**ADR 0007 D3 is untouched** — the empty string keeps meaning "cannot tell" for every input, with
no asymmetric comparison written into the ADR. The upgrade is a literal no-op on a healthy TLS
cluster, because the two keys are mutually exclusive and the desired template is byte-identical.

**Fatal as specified**, and this is the security fact above biting: an armed pod carries **no**
fingerprint env by construction, so the classifier's `recorded == ""` branch routes the entire
armed population through `RecordedTLSMaterialHash`, which falls through to the forgeable
annotation. A compromised sidecar computes the current digest from its own `/tls` mount and merge-
patches `vko.gtrfc.com/tls-material-hash` onto every data pod of the cluster; the witness branch
is then never reached, the pods are never rolled, and the CR reports `TLSMaterialCurrent`. It is
repairable — read the spec only, check the witness *before* the annotation, consult the
annotation only for pods carrying neither spec key, plus an ADR 0031 D5 amendment — and it is the
option to re-cost if the ADR 0007 D3 amendment is what one objects to in A. **A needs no such
repair:** every pod A writes carries the env, so the annotation is unreachable for that
population. Secondary cost: D makes "am I exempt" a one-bit answer readable from inside the
hostile container with zero API calls.

#### E — latched adoption (`Certificate.status.revision == 1`)

Adopt a record-less pod only where the tier's material is provably issued exactly once — the TLS
volume is a **required** `SecretVolumeSource` on both tiers
([`statefulset.go:587-596`](../../../internal/builder/statefulset.go#L587-L596),
[`sentinel.go:302-312`](../../../internal/builder/sentinel.go#L302-L312)), so a pod cannot have
started before its Secret existed — latch the verdict on the StatefulSet, report every refusal as
unmeasured. The structural-not-temporal argument is sound and correctly cites ADR 0011.

**Refuted twice.** Its only advantage is retroactivity, and its own guard "template already has a
record → return" reads the **template**, which every T27 cluster has armed — so it adopts nothing,
ever. Dropping that guard turns it into a clear/re-latch write loop with a live `Certificate` GET
per pass. And the latch is consulted only when the pod record is empty, so it inherits D's
annotation-forgery path *and* adds one: the latch itself is forgeable and deletable by any
namespace principal holding `statefulsets: patch`, permanently, with no way for the operator to
correct it. That is ADR 0031's own finding one privilege level up.

#### Killed, one line each

* **Option 3 literal (stateless adoption)** — a no-op; identical to today, and `armPodsWithTheTemplateRecord` stays forever.
* **Option 1 unconditional (stamp the sentinel on every unreadable pass)** — the template is not sticky; one unreadable pass flips a real digest to the sentinel and rolls both tiers plus every legacy annotation-carrier for nothing. Case 2 of A exists to kill exactly this.
* **Option 2 alone (creation gate only)** — leaves doors (b) and (c) open; same defect, different entry.
* **Timestamp gates** (`creationTimestamp`, container `startedAt`, `tls.crt` NotBefore) — ADR 0011: creation order may only ever REFUSE, never GRANT. Also unsound in the wrong direction, since T27 pods are created *before* the Secret.
* **Fingerprint as a required field inside `BuildStatefulSet`** — `ComputePodSpecHash` would then cover it, so one rotation moves two signals (ADR 0031 D3) and the upgrade rolls every fleet including Sentinel (ADR 0005 D1).
* **`replicas: 0` until the record exists** — writes a false statement that the nudge, the status and the roll all read.
* **Unconditional witness key on both tiers** — a Sentinel template write bumps `sts.Status.UpdateRevision`, and `sentinelRolloutComplete` compares pods against it, so the legacy unified-certificate cleanup would be deferred indefinitely on a tier nothing else rolls.
* **Sidecar image as a retroactive witness** — misclassifies exactly the kustomize / floating-tag population ADR 0005 D11 carves out, and does nothing for Sentinel.

### Recommendation put to the user (decision pending)

**A, plus C as a separate yes/no, with B as an optional cost-removal for door (a).**

A is the only option that supplies the missing bit — "this operator wrote me and had nothing to
record" — **on a carrier the API server refuses to change after creation**, and it supplies it at
every entry into the window rather than at one door. Every other three-state design classifies via
a branch reached only when the env record is absent, which routes the whole armed population
through an annotation a compromised sidecar can write. A never lets that happen for a pod it
wrote. Case 2 is worth having on its own: it stops the operator stripping its own record.

C is separable and is the **only** half that covers the fleet running today; A and B are both
forward-only. B wins a place only if the arming roll on door (a) is judged unacceptable, and it
can be added later without changing anything in A.

### What is verified and what is not

**Verified in the code:** the full chain and every file:line above; that both StatefulSets are
`OnDelete`, so a template write rolls nothing by itself; that the TLS volume is required on both
tiers; that the sidecar holds `pods: patch` on every data pod name and that the annotation
fallback is reachable for any pod without the env record; that no CEL or webhook guards
`spec.tls.enabled`; that `requestRecheck` prior art exists; that the condition-registry guard
parses `ConditionType` declarations only, so a new *reason* needs no row; `gocyclo` today —
`podTLSMaterialHashChanged` 3, `podNeedsUpdate` 5, both StatefulSet reconcilers 8,
`reportTLSMaterialStale` 9, `scanTierTLSMaterial` 12, `sentinelPodNeedsUpdate` 14, threshold 15.

**Not verified:**

* **The arming roll against still-booting pods.** Nothing measured whether a roll starting seconds
  after creation — before replication is established and before `instanceRole` is set — merely
  waits, or can reach `RollingUpdatePaused` or a no-master requeue spin. *Proof:* one e2e per
  topology asserting the phase returns to `OK` with zero Warning Events.
* **Wall-clock of the arming roll on CI Kind**, and therefore whether the 5-minute creation-time
  e2e waits actually break. *Proof:* run the suite.
* **Post-edit `gocyclo`.** `reportTLSMaterialStale` is 9 today and one reviewer measured ~14 after
  the reason/message partition, against a hard 15. Not re-measured here.
* **That the StatefulSet controller creates pods before the volume Secret exists** — taken from
  the 2026-08-27 Kind measurement above, not re-measured.
* **Whether `valkey-server` actually pins its material.** That is [T28](#t28-measure-what-the-pods-actually-serve-off-the-handshake-the-operator-already-performs),
  and it changes T27's **severity**, not its correctness: if `valkey-server` reloads, the only
  pinning container in a metrics-free TLS pod is none, and `high` is unearned. **T27 and T28 did
  not cross-reference each other and now do.**

### What the e2e does about it meanwhile

~~`TestE2E_TLS_CertificateRotation_RollsTheFleet` arms the pods itself — it waits for the
template to carry a record, deletes the three pods so the StatefulSet recreates them from it,
and only then rotates.~~ **Discharged with the fix below:** `armPodsWithTheTemplateRecord` is
deleted, and the test now asserts the opposite — a new subtest "the pods of a fresh cluster
are armed from birth" requires every data pod to carry the template record with no help from
the test, before the rotation is forced.

### Decision (2026-08-27, delegated: "finde die eleganteste Loesung")

**Not option A. The write gate: the operator never persists a TLS pod template without a
material record — ADR 0030 D12.** Three cases, replacing `stampTLSMaterialHash` with
`ensureTLSMaterialRecord`:

1. Secret readable → stamp its fingerprint (byte-identical to before);
2. Secret unreadable, persisted template carries a record → inherit it (A's case 2 verbatim;
   closes door (c) = T24(c));
3. neither → **refuse the StatefulSet write** — create and update path, both tiers — without
   failing the pass: `requestRecheck(30s)` plus the ADR 0030 D10 Secret watch, which re-enters
   the pass the moment cert-manager issues.

**Why the deviation from the recorded recommendation is justified, point by point:**

* A's own "strongest argument against, and it survived review" — the arming roll of every
  fresh cluster (two tier rolls on Sentinel topologies, ~7 e2e sites budgeted at 5 min for
  what rolls cost 12–15 elsewhere) and the guaranteed double replacement on door (b) — simply
  does not exist under the gate: pods are armed from birth, door (b) is one roll.
* The killed "Option 2 alone (creation gate only)" objection — "leaves doors (b) and (c)
  open" — does not apply: the gate covers the update path (door b) and case 2 covers door (c).
* No third fingerprint state, so ADR 0007 D3 keeps "empty means cannot tell" for every input,
  `podTLSMaterialHashChanged` is untouched, and there is no `unarmed` value for the scan, the
  message renderer or a future reader to mishandle.
* The refusal is the repo's own design language — ADR 0009, "a promotion the operator could
  not record is not a completed promotion", applied to a template.

**Cost, stated rather than glossed:** a TLS Secret that never appears parks the tier silently
(create: no StatefulSet, phase `Provisioning`/"Waiting for StatefulSet creation"; update: a
`tls.enabled` flip that does not converge while the cluster keeps serving) — accepted and
recorded in ADR 0030 residuals, against the pre-fix behaviour of a pod wedged in
`ContainerCreating`, which was louder and worse for a serving cluster. Reporting that wait on
the CR is the surviving half of option C, **not built**, like C itself (the retroactive
report), which remains the answer to T24(b) if it is ever wanted.

### Implementation (2026-08-27) — DONE

* [`internal/controller/tls_material.go`](../../../internal/controller/tls_material.go):
  `ensureTLSMaterialRecord` (the gate), header comment updated; `reportTLSMaterialStale` gains
  the T24(a) one-way AND and the T24(d) retraction (`clearTLSMaterialStaleOnDisable`,
  presence-guarded both directions); `scanTLSMaterial` measurability is AND-ed.
* [`internal/controller/valkey_controller.go`](../../../internal/controller/valkey_controller.go):
  both StatefulSet reconcilers call the gate on the create path and after the ownership proof
  on the update path (ADR 0020 D10); the `when: IsTLSEnabled` gate on the "TLS material" step
  is removed with its reason documented in place.
* [`api/v1/valkey_types.go`](../../../api/v1/valkey_types.go): `ReasonTLSMaterialNotApplicable`.
  [`internal/controller/condition_registry.go`](../../../internal/controller/condition_registry.go):
  row comment rewritten.
* Tests: unit — gate cases 1/2/3 incl. legacy annotation inheritance, reconciler-level
  create-refusal for both tiers, strip regression, T24(a) both directions, T24(d) both
  directions; integration — `TestTLSMaterialGate_TheStatefulSetWaitsForTheSecret_Integration`
  (`require.Never` on the create, then armed-from-birth via the real watch), the
  replica-announce TLS test now pre-creates its Secrets; e2e — armed-from-birth subtest
  replaces `armPodsWithTheTemplateRecord`.
* Docs: ADR 0030 (D12 new, D8 superseded in place, D9 rewritten, four residuals closed with
  strikes, one new accepted residual), `CLAUDE.md`, `README.md` (gap paragraph replaced,
  condition row, C1).
* Verified: `make test-unit`, `make test-integration`, `make lint`, `make cyclo` — green.
  **And the rotation e2e ran on the local Kind cluster against the rebuilt operator image**
  (2026-08-27, same day): `TestE2E_TLS_CertificateRotation_RollsTheFleet` green in **85 s**
  total — faster than with the deleted self-arming, because the three extra pod replacements
  are gone — with "the pods of a fresh cluster are armed from birth" passing on the real
  cert-manager issuance path and zero Warning events. **Not verified:** the same timing on the
  CI runner (its cert-manager and dockerd are slower; the suite budget still applies).

## T26: embargoed security finding, open - details in its own ticket file until it is fixed

Moved on 2026-09-27 into its own ticket file, which is embargoed until the finding is fixed.

## T17: `RollingUpdateComplete` says "topology restored" on the path that just recorded the opposite

**Severity: low, cosmetic. Status: DONE 2026-08-27** — all three completion exits decided,
as the 2026-08-26 correction demanded: the clean exit keeps its text, the abandoned
restoration says the promoted replica stays master (a supported end state, ADR 0002 D11,
read from the `TopologyRestored` verdict **before** `clearRollingUpdateState` refreshes the
CR), the stalled rogue-master exit counts its rogues, and the verify-incomplete exit now
emits the `RollingUpdateComplete` marker it used to omit — anything sequencing on the
completion reason no longer misses exactly the completions that deserve a second look. The
missing assertion exists: three message-level tests in `topology_restore_stall_test.go`.
ADR 0010 status note updated. ~~open, deferred 2026-08-26.~~ Found 2026-08-25 while
analysing T6b.

`verifyTopologyRestored` emits the completion Event with the fixed text
*"Multi-replica rolling update completed, topology restored"*
([`rolling_update.go:3850-3852`](../../../internal/controller/rolling_update.go#L3850-L3852)),
regardless of which verdict Phase 1 recorded. On the abandon path
(`abandonTopologyRestoration`, `TopologyRestored=False / RestoreTimeout`) the Event
therefore states the opposite of the condition written moments earlier, and the same
function also completes with rogue masters still live
([`:3843-3846`](../../../internal/controller/rolling_update.go#L3843-L3846)) under the same
message.

**Proposed fix (not decided):** read the recorded verdict and say which end state was
reached — the promoted replica stays master is a supported outcome (ADR 0002 D11), so
the Event should name it rather than claim the canonical one. One string plus one
assertion. ~~ADR 0007 quotes this Event and would be amended with it.~~

> **Corrected 2026-08-26, and one path was missed.** Re-verified on `HEAD` = `1c309d8`.
>
> 1. **"ADR 0007 quotes this Event" is false** — ADR 0007 contains no Event mention
>    whatsoever. The ADR that owns these paths is
>    [ADR 0010](../../adr/0010-every-rolling-update-wait-is-bounded.md): D3 `:89-99`,
>    D5 `:108-115`, Consequences `:249-256`. Do not budget an ADR 0007 edit.
> 2. **There is a third completion exit this item does not mention**, in the same
>    function: `TopologyVerifyIncomplete` at
>    [`rolling_update.go:4059-4066`](../../../internal/controller/rolling_update.go#L4059-L4066)
>    returns `Completed: true` and emits **no** `RollingUpdateComplete` at all. So the Event
>    is not only sometimes wrong, it is also **inconsistently present**. A fix must decide
>    all three exits, not two — otherwise it trades a wrong Event for a missing one.
> 3. Line refs drifted by ~262 lines (content unchanged since `5da613d`): the Event is at
>    [`:4112-4114`](../../../internal/controller/rolling_update.go#L4112-L4114), the
>    rogue-master stall branch at
>    [`:4103-4106`](../../../internal/controller/rolling_update.go#L4103-L4106).
> 4. **The assertion is the larger half, and there is currently none.**
>    `grep -rn 'completed, topology' --include='*_test.go'` is empty; unit tests count
>    `rec.withReason("RollingUpdateComplete")` and the e2e helper matches by reason only
>    ([`test/e2e/pdb_test.go:415`](../../../test/e2e/pdb_test.go#L415)). Changing the string
>    breaks nothing — which is exactly why the message has drifted from the truth unnoticed.

### Decision

**Deferred 2026-08-26.** Cosmetic, one string, and it belongs with whoever next touches the
completion Events rather than with a condition-lifecycle change. Not carried in the registry:
it is an Event, not a condition, so ADR 0027's guard has no opinion on it — which is worth
noting, because Event text is the one surface in this family with no test and no registry
behind it.

## T18: `Ready` keeps its pre-roll value for the whole rolling update — decided in ADR 0001 D4, re-decision request

Moved on 2026-09-27 to [018-ready-keeps-its-pre-roll-value-during-a-rolling-update.md](../018-ready-keeps-its-pre-roll-value-during-a-rolling-update.md).

## T19: The health checker guessed a pod's tier from its name — a CR named `*-sentinel` was unreachable to its own operator

**Severity: high (a whole cluster unreachable to the operator, chosen by its name).
Status: FIXED 2026-08-26. Found by the e2e suite of T5.**

### How it surfaced

`TestE2E_RollingUpdate_NoSecondDeleteWhileAPodTerminates` (added 2026-08-25 with T5,
`360cb03`) failed on **both** legs. The no-sentinel leg reported:

```
Valkey term-no-sentinel phase: Error (want: OK) message: Instance unreachable:
term-no-sentinel-0: ping term-no-sentinel-0.term-no-sentinel-sentinel-headless...:6379
```

A **data** pod, addressed through the **Sentinel** headless Service, on the **Valkey** port,
in a cluster that has no Sentinel at all.

### Root cause

`podAddress` ([`internal/health/checker.go`](../../../internal/health/checker.go), removed in this
fix) derived the component from the pod name by testing a fixed-offset window:
`podName[len-10:len-2] == "sentinel"`. The test builds its cluster name as `"term-" + suffix`
([`pod_termination_test.go:59`](../../../test/e2e/pod_termination_test.go)) over the subtests
`sentinel` and `no-sentinel`, so **both** legs produced a CR name ending in `sentinel` and both
tripped it.

Two independent failure classes, measured:

| Pod | Classified as | Correct? |
|---|---|---|
| `term-no-sentinel-0` (data) | sentinel | **no** |
| `term-sentinel-0` (data) | sentinel | **no** |
| `myvalkey-sentinel-10` (sentinel) | valkey | **no** |
| `myvalkey-0`, `myvalkey-sentinel-0` | correct | yes |

Class A is any CR whose `metadata.name` ends in `sentinel`, for every data pod with a
single-digit ordinal. Class B is any Sentinel pod from ordinal 10 up —
`spec.sentinel.replicas` has `Minimum=1` and **no Maximum**
([`api/v1/valkey_types.go:274-276`](../../../api/v1/valkey_types.go)), so it is reachable.

Not a regression: the code shipped 2026-02-17 in `88b721b`, the first Helm/E2E commit.

### The part worth keeping in mind

The defect was **already written down as a passing test**. `34c351c` (2026-08-21, "test: raise
statement coverage from 68 to 96 percent") added three characterization tests that asserted the
*wrong* addresses with the message `"BUG (documented, not fixed)"` and named the fix in their
doc comments. Four days later the T5 e2e test picked cluster names that landed inside that
documented window. Pinning a bug kept the suite green and kept the bug; nothing put it on a
list anybody read. → ADR 0029 D5.

### Blast radius while it was live

Not a test artefact. Any user naming a CR `foo-sentinel` got a cluster the operator could never
report `OK` for. Split by how the blindness presents:

- **Loud (Sentinel disabled only):** `PingPod` → `verifyValkeyConnectivity` → `phase: Error`,
  `Instance unreachable: …`. With Sentinel enabled this path is never reached — the failure
  arrives via `CheckCluster` → `findMaster` as `Cluster health check failed: no master found`.
- **Silent, and worse:** `GetReplicationInfo` and `findMaster` read an unreachable pod as *not
  the master*. `collectPodStates` then falls back to the `instanceRole` label
  ([`rolling_update.go:1651`](../../../internal/controller/rolling_update.go)), so for such a
  cluster the stale label was the **only** input on who the master is — permanently, on pods
  that were alive and merely unaddressable.

Verified by reasoning over the naming scheme, **not measured in a cluster**: no misroute could
reach a foreign Valkey; both tiers' pod names are disjoint under either Service, so every
misroute was an NXDOMAIN. The damage was blindness, not misdirection.

### The fix

`podAddress` is deleted. Component and port are now chosen together and cannot be separated:

- `valkeyPodAddress(v, podName)` — Valkey component + `builder.ServicePort(v)`
- `sentinelPodAddress(v, podName)` — Sentinel component + `sentinelPort(v)`

All four former call sites (`PingPod`, `GetReplicationInfo`, `findMaster`, `observeSentinels`)
use the helper for the tier they address. Nothing reads a pod name any more, so the length
guard that existed only to keep the slice from panicking is gone with it.

Audited and **not** changed: the exported `PodAddressForComponent` and its 19 call sites in
`internal/controller` — all already pass the component explicitly and all pair it with a
matching port.

### Verification

- `make test-unit`, `make test-integration`, `make lint` (0 issues), `make vet`, `make cyclo`
  all pass.
- **Mutation checks per ADR 0017 D7**, both run and both reverted byte-identical:
  reintroducing the old predicate in `valkeyPodAddress` fails
  `TestFindMaster_ClusterNameEndingInSentinelStillUsesTheDataService`,
  `TestPodAddress_TheTwoTiersNeverShareAnAddress` and two rows of
  `TestPodAddress_ComponentIsNeverDerivedFromTheName`; reintroducing it in
  `sentinelPodAddress` fails `TestObserveSentinels_DoubleDigitOrdinalUsesTheSentinelService`
  and the two-digit-ordinal row.
- Three tests that asserted the buggy addresses were rewritten to the correct ones and renamed
  (the two `…IsMisrouted` names described behaviour that no longer exists). Four small tests in
  `checker_test.go` became rows of the new table; the one case they covered that the table did
  not — the shortest legal cluster name — was added as a row.
- **The e2e cluster names `term-sentinel` and `term-no-sentinel` were deliberately left
  unchanged.** They are the only fixtures in the repo where a CR name collides with a component
  name; renaming them would retire the coverage that caught this.
- **Not verified:** the e2e suite itself was not re-run here (it needs a Kind cluster). The
  failing assertion was `phase: OK`, which the unit-level regression tests now cover at the
  address-construction layer, but the green e2e run is still outstanding.
- **Not verified:** whether any cluster in the field currently hits Class A, and the obvious
  search finds only half of them. Without Sentinel the cluster shows `phase: Error` with
  `Instance unreachable: …`; with Sentinel, `updateStatus` branches to `updateHAStatus`
  ([`valkey_controller.go:2055`](../../../internal/controller/valkey_controller.go)) and never
  calls `verifyValkeyConnectivity`, so the same blindness reads
  `Cluster health check failed: no master found among N pods`. That is the shape the
  `term-sentinel` e2e leg took. Neither search was run.

Rationale, alternatives and residual risks:
[ADR 0029](../../adr/0029-a-name-is-not-a-component.md).

### Follow-up left open

The Sentinel port selection (`SentinelPort` / `SentinelTLSPort` by `IsTLSEnabled`) is now a
named function in `internal/health`, but it is still open-coded four times in
[`internal/controller/rolling_update.go`](../../../internal/controller/rolling_update.go)
(`:1460`, `:2915`, `:2990`, `:3055`). All four pair their component correctly today. Not a bug,
not fixed — named here so the duplication is on a list rather than in somebody's memory.

## T20: The coverage PR comment outgrew a kernel limit — `Argument list too long` in Combined Coverage Report

**Severity: medium (CI job fails on every PR once main crosses the line; no product
impact). Status: FIXED 2026-08-26.**

### Symptom

`Combined Coverage Report` → step `Comment PR with combined coverage`, on
[run 32947718245](https://github.com/guided-traffic/valkey-operator/actions/runs/32947718245):

```
An error occurred trying to start process '/runner/externals/node24/bin/node'
with working directory '/runner/_work/valkey-operator/valkey-operator'.
Argument list too long
```

Not a test failure and not a flake. `E2BIG` from `execve`: a `with:` input reaches an
action as a single environment variable, and Linux caps one of those at
`MAX_ARG_STRLEN` = 32 pages = **131072 bytes**. Exceeding it does not truncate — the
process never starts.

### Root cause

The comment body at
[`.github/workflows/release.yml`](../../../.github/workflows/release.yml) carried the same
report **twice**:

| Piece | What it actually was | Size |
|---|---|---|
| `${{ env.COVERAGE_SUMMARY }}` | titled "Coverage by Package", but `awk`'d over `coverage.txt`, which is the `go tool cover -func` output — **one line per function**, 660 lines | 62039 B |
| `${{ steps.merge-coverage.outputs.coverage_text }}` | the same report again, raw | 73175 B |

Together ~135 KB against a 131072 B ceiling. The repo has **14 packages**; the
"by package" section listed 660 functions.

### Measured, not estimated

`go test ./... -coverprofile` on each tree, then the workflow's own commands:

| Tree | coverage.txt | summary.txt | combined | under 131072? |
|---|---|---|---|---|
| `main` | 67033 | 56779 | **123812** | yes |
| `fix/bad-findings` | 73175 | 62039 | **135214** | **no** |

609 functions on main, 661 on the branch. The CI history matches exactly: the
`renovate/go-openapi` PR passed on 2026-08-26 (based on main) while `fix/bad-findings`
had been failing since 2026-08-25. The last comment that *did* post on PR #195 measured
**130926 characters** — 146 under the limit. It had been running at the edge for weeks.

**This is not a branch problem.** main sits ~7 KB under the ceiling. Merging this branch
puts every future PR, Renovate included, over it.

**An assumption of mine was falsified and is worth recording:** I expected GitHub's
65536-character comment limit to bite first. The bodies actually posted on PRs #192–#197
measure 90387 to 130926 characters, so GitHub accepts them. `MAX_ARG_STRLEN` is the only
effective limit here.

### The fix

1. **`summary.txt` is now aggregated per package from the profile**, not `awk`'d from the
   per-function report: **16 lines / 1182 bytes** instead of 660 lines / 62039. Blocks are
   deduplicated by location before summing, because the merge step concatenates the unit
   and integration profiles and every block both runs touched appears twice; under
   `mode: set` a block counts as covered when either run covered it. Verified: the
   weighted total from the aggregation is `95.9%`, byte-identical to
   `go tool cover -func` total.
2. **The full per-function report is out of the comment.** It was already written to
   `$GITHUB_STEP_SUMMARY` and uploaded as the `coverage-combined` artifact, so nothing is
   lost — the comment now links rather than inlines. The `coverage_text` step output is
   removed entirely; it had no other consumer.
3. **A size guard** caps the summary at 50000 bytes and appends a line naming where the
   full data is, so an unexpected growth degrades into a truncated comment instead of a
   failed job.

Final comment body: **1675 bytes**, down from ~135214.

### Verification

- The embedded aggregation was extracted from the YAML exactly as the runner would see it
  (`yaml.safe_load` → the `run` block) and executed against a real merged profile.
- Aggregation total `95.9%` == `go tool cover -func` total `95.9%`.
- Dedup: the same profile concatenated twice produces a byte-identical table.
- `mode: set` max semantics: a block uncovered in one profile and covered in the other
  counts as covered (`0/5` → `3/5`).
- Blank and trailing lines are ignored.
- Guard, both directions: at 1182 bytes it does not fire (`GITHUB_ENV` 1208 B); at 207000
  bytes it truncates to 50091 B and annotates (`GITHUB_ENV` 50117 B).
- YAML parses; the heredoc survives block-scalar dedenting, so the Python does not hit an
  `IndentationError` on the runner.
- **Not verified:** the job has not been re-run in CI. The repo has no workflow linter
  (`actionlint` is not configured and was not available locally), so everything above is
  local reproduction of the workflow's own commands, not a green run.

### Follow-up left open

The comparison against main reads `.github/badges/coverage.json` from the main branch, so
the "Main Branch Coverage" figure is only as fresh as the last release commit. Unchanged
by this fix, named because the diff it prints is easy to over-trust.

## Audit trail

- Operator log snapshot: taken 2026-08-22 ~21:41 UTC from
  valkey-operator-699df45847-x84c8 (covers 21:32–21:41; scratchpad, not
  persisted — the findings above quote everything load-bearing).
- Chaos Mesh schedule `valkey-chaos` documented in the project memory
  (database-examples only; production namespaces are not targeted).
- Zero ERROR-level log lines existed outside the gitlab STS rejection; all
  30+ ERROR lines are that identical write failure.
- T5's second re-review (2026-08-25) was a 15-agent adversarial pass with an
  independent refuter per finding; every load-bearing claim was then re-verified by
  hand against the clean tree. Two numbers came from running the repo's own tooling
  rather than from reading: `make cyclo` / `gocyclo -top 8` (five functions at
  exactly 15 against a threshold of 15), and the drain-hook timing, which was read
  out of `internal/builder/statefulset.go` and `internal/sidecar/drain.go` after the
  2026-08-24 probe turned out to have measured an unconditional `sleep 60` rather
  than the operator's wait loop. **Not verified in this pass:** nothing was executed
  against a cluster, so every claim about behaviour under a real termination is a
  code reading; and the two-down scenarios in re-review Findings 1-4 remain traced,
  not reproduced.
- T20's byte counts were produced 2026-08-26 by running the workflow's own commands
  (`go test ./... -coverprofile`, `go tool cover -func`, the merge and awk pipeline) against
  both trees, main in a throwaway `git worktree` so the working tree was untouched; the
  posted-comment lengths come from `gh api .../issues/N/comments`.
- T19's classification table was measured 2026-08-26 by executing the removed predicate
  `podName[len(podName)-10:len(podName)-2] == "sentinel"` over the pod names in question;
  the commit dates come from `git log -S` and `git log --follow`. No cluster involved.
- T12's gate behaviour table was measured 2026-08-24 in docker against
  `valkey/valkey:9.1.1` (the repo's own pinned image, `test/testimages/images.go`),
  not read from upstream documentation: `docker run --rm valkey/valkey:9.1.1
  valkey-server --min-replicas-to-write 1 --min-replicas-max-lag 10`, then
  `valkey-cli` for each command. No cluster involved.
- T6's analysis (2026-08-25) was a 10-agent pass — one analyst per sub-finding plus a
  cross-cutting condition-lifecycle inventory, each followed by an independent
  adversarial refuter — and every load-bearing claim was then re-verified by hand
  against the clean tree at `360cb03`. Three of the refuters changed a conclusion:
  the T18 finding is decided by ADR 0001 D4 rather than uncovered, the T15 fix as first
  drafted would have been erased by `pauseRollingUpdate`s own call to
  `clearRollingUpdateState`, and the T6a minimum (A1b) is seven lines in
  `persistStatus`, not the fifteen-line capture move first proposed.
  **Cluster work in this pass was read-only** — `kubectl get` on Valkey CRs, observer
  Deployments, observer pods and Flux Kustomizations on wds18-k8s-main; nothing was
  patched, deleted or applied. The T6a measurement (`07:05:07Z` status write against
  `07:05:10Z` observer availability on `valkey9-sentinal-tls`) comes from two reads of
  the same CR minutes apart, so it is an observation, not a controlled experiment.
  **Not verified:** no test was run and no code changed, so every claim about a
  proposed fix breaking or not breaking a named test is a code reading; and the kstatus
  conclusion in T6d rests on the absence of `status.observedGeneration` and of
  `Reconciling`/`Stalled` conditions (both verified here) plus upstream kstatus
  behaviour (not read).
- T6's implementation (2026-08-26) took the five decided blocks plus T14 and closed them in
  one change: `observerReady` computed in `persistStatus`, the observer-Deployment ownership
  guard, the `SidecarUpdatePending` clear at the completion branch and its pod-naming
  message, the `TopologyRestored` and `Ready` contracts stated where they belong, and the
  condition registry with [ADR 0027](../../adr/0027-conditions-are-levels-edges-or-history.md).
  Documentation touched in the same change, per the ADR discipline: ADR 0001 D4 (clarified),
  ADR 0002 (D5 amended, D5a and D10a added, D10 amended, two Alternatives added, four
  Residual risks added), ADR 0010 D15 (clarified), ADR 0020 (read-path sweep corrected in
  place), ADR 0027 (new, indexed), README (four rows plus the phase table), CLAUDE.md (new
  section, and its own Status promise corrected — it made the same half-true claim the
  README did).
  **Verified:** `make fmt`, `make vet`, `make lint` (0 issues), `make cyclo` (all under 15),
  `make test-unit` (every package green). Each new regression test was confirmed to fail
  against the pre-fix code before being kept — the two `observerReady` tests against the old
  assignment order, the completion-clear test against a removed call. **Not run:**
  `make test-integration`, `make test-e2e`. **Not fixed, and each is a recorded decision
  rather than an oversight:** T6b B2, T15, T16, T17, T18, and `readyReplicas`, which carries
  the same defect as `observerReady` and is masked by the phase message strings — the two
  registry gaps and ADR 0002's Residual risks carry those.

---

## T21: The sidecar pins its TLS client certificate for the pod lifetime — a cert-manager rotation silently breaks the labeler and the drain promotion

**Severity: high (silent, permanent loss of the `instanceRole` labeler, the Sentinel
cross-check and the ADR 0012 drain promotion, on every TLS cluster whose pods outlive a
certificate rotation). Status: IMPLEMENTED 2026-08-26 — Option E, capability-gated, both
halves. Found 2026-08-26 during the T8 read-only cluster inspection. It is a repo defect, not
cluster ops — filed here because the inspection found it, but it belongs to the operator.**

> **What shipped, and what is still owed, is in
> [Implemented 2026-08-26](#implemented-2026-08-26) at the end of this item.** Everything
> between here and there is the analysis that led to the decision and is kept for the
> reasoning. Three of its statements are superseded by the implementation and say so in
> place: the "recurring 90-day cost", the 2026-09-26 deadline, and the fix direction of the
> original proposal.

### How it surfaced

Measured on `gitlab/gitlab-valkey` (wds18-k8s-main), on all three data pods, once per
second, continuously:

```
ERROR sidecar.labeler failed to detect role
  {"error": "info replication localhost:16379: AUTH failed on localhost:16379:
             remote error: tls: expired certificate"}
```

5781 occurrences on pod-1 inside the retained log buffer alone; the buffer does not reach
back to the start, so the true onset is earlier and not recoverable from logs.

**The certificate on disk is valid.** `gitlab-valkey-tls` is at revision 4,
`notBefore=2026-08-23T09:38:42Z`, `notAfter=2026-11-21`, cert-manager reports `Ready=True`
with `renewalTime 2026-10-22`. The Secret was rotated on **2026-08-23**. The sidecar
processes started **2026-06-12** (pods 0/1, `restartCount=0`) and **2026-08-21** (pod-2) —
all *before* the rotation. `remote error:` means the peer sent the alert: valkey-server is
rejecting the **client** certificate the sidecar presents.

### The mechanism, verified in this repo at `HEAD` = `1c309d8`

[`buildSidecarTLSConfig`](../../../internal/sidecar/labeler.go#L271-L298) reads the CA with
`os.ReadFile` and the keypair with `tls.LoadX509KeyPair` **once**, and stores the parsed
result in `tlsCfg.Certificates`. There is **no `GetClientCertificate` callback, no
`GetConfigForClient`, and no reload path** — verified by grep across `internal/` and `cmd/`.

It has exactly three callers, and [`Run`](../../../internal/sidecar/run.go#L54) builds all
three at **process start**:

| Caller | Site | What stops working |
|---|---|---|
| `newValkeyRoleDetector` | [`labeler.go:167`](../../../internal/sidecar/labeler.go#L167) | the `instanceRole` labeler — so `-rw` never follows a failover |
| `newSentinelMasterQuerier` | [`labeler.go:324`](../../../internal/sidecar/labeler.go#L324) | the Sentinel cross-check, i.e. the split-brain defense in depth |
| `newRealValkeyClientFactory` | [`drain.go:426`](../../../internal/sidecar/drain.go#L426), via [`:451`](../../../internal/sidecar/drain.go#L451) | **the ADR 0012 drain promotion** |

The factory stores `tlsConfig` once ([`drain.go:415-418`](../../../internal/sidecar/drain.go#L415-L418))
and `NewClient` reuses it per connection, so nothing re-reads the file at drain time either.

**The observer has the same shape:**
[`internal/observer/observer.go:407-413`](../../../internal/observer/observer.go#L407-L413),
called at [`:113`](../../../internal/observer/observer.go#L113) and
[`:118`](../../../internal/observer/observer.go#L118).

**The controller and the health checker are NOT affected, verified:** both build their
`tls.Config` by `Get`-ing the Secret from the API server **per call**
([`valkey_controller.go:134`](../../../internal/controller/valkey_controller.go#L134),
[`health/checker.go:375`](../../../internal/health/checker.go#L375)) and set only `RootCAs` —
they present no client certificate at all. So the operator keeps talking to the data plane
throughout, which is exactly why this is invisible from the CR.

### Why no tier catches it

The certificate lifetime is 90 days and the failure needs a pod that outlives a rotation.
**No e2e can ever reach this**, and no unit test can either without injecting a clock or a
short-lived CA. That is the reason it survived to production rather than an oversight in
the suite.

It is also invisible on the CR: `Ready` stays `True`, `phase` stays `OK`, replication stays
`up`, and the sentinels keep working — they refresh `INFO` themselves and my `valkey-cli`
execs succeeded, because both read the certificate file fresh per invocation. **Only the
long-lived Go sidecar is broken**, and nothing reports it.

### Blast radius, stated per contract rather than as a blanket

* **ADR 0012** — "the sidecar records its drain promotion on the pod" — stops holding for
  any pod that outlives a rotation. The drain cannot reach the local Valkey, so it cannot
  promote and cannot stamp. On a multi-replica non-Sentinel cluster that is the *only*
  promotion authority, and the `preStop` gate then waits out its full 60 s for a
  `drain-complete` marker that will not appear.
* **ADR 0022 / the labeler** — `instanceRole` freezes at whatever it last was. On gitlab
  that is all three pods at `replica` and an empty `-rw` Service (see T7 and 14b below).
* **The Sentinel cross-check** — the defense-in-depth path of ADR 0011 goes dark silently.

### Contrast that proves it is the rotation and not the cluster

`harbor/harbor-valkey-0`, same day, same operator version, sidecar started
2026-08-26T01:47 — i.e. **after** the rotation: zero `expired certificate` lines, and it
logs `role changed {"from":"replica","to":"master"}` normally. Every one of the other 11
clusters has exactly one pod labeled `master` and a populated `-rw`.

### The fleet has dates — corrected 2026-08-26 ~10:05 UTC

> **A correction to this item, made hours after it was filed: rotation is not expiry, and the
> first version of this section confused the two.** It claimed the defect "fires on
> `iam/oauth2-valkey` 2026-08-27", the renewal date. That is wrong by a month.
>
> The sidecar pins a **certificate**, and that certificate stays valid until its own
> `notAfter`. A rotation writes a new one into the Secret and the mounted volume; the pinned
> one in memory is unaffected and keeps working. **The break is at the pinned certificate's
> expiry, not at the rotation.**

Measured with `kubectl get certificate -A` plus the cert actually on disk in the pods. All
data pods started **2026-08-26 01:47** (the 1.11.1 upgrade), so each holds the revision that
was current then, and that revision's `notAfter` is its break date:

| Cluster | pinned rev | rotates | **breaks** |
|---|---|---|---|
| `iam/oauth2-valkey` | 2 (notBefore 2026-06-28) | 2026-08-27 | **2026-09-26** |
| `gpt/gpt-valkey` | 2 (notBefore 2026-07-01) | 2026-08-30 | **2026-09-29** |
| `gitlab`, `harbor`, 4x `database-examples` | 4 / 2 (notBefore 2026-08-23) | 2026-10-22 | **2026-11-21** |

Certificates are 90 days with cert-manager renewing 30 days before expiry, so there is a
**30-day window** between a rotation and the corresponding breakage. That window is the
grace period, and it is why the fleet is not already broken.

**The mechanism is confirmed backwards by gitlab.** Its rev 4 has `notBefore 2026-08-23`, so
rev 3 expired then. Its pods had been running since **2026-06-12**, holding rev 3. The
`expired certificate` errors were measured on **2026-08-26** — three days after rev 3 expired,
and nowhere near rev 3's rotation. The dates fit only the expiry model.

### The open question that decides this item's severity, and tomorrow answers it for free

**Does `valkey-server` also pin its certificate, or does it reload?** The sidecar is a Go
process and provably pins. The server is a separate question and nobody has measured it.

* If the server **reloads**: T21 is what this item describes — the labeler, the cross-check
  and the drain promotion die, the data plane keeps serving. A degradation that becomes an
  outage only at the next failover.
* If the server **also pins**: then on the break date the server presents an **expired**
  certificate and **every TLS client fails**, not just the sidecar. That is a full outage of
  the cluster, and it would make this item critical rather than high.

**Indirect evidence points at reload, and it is not proof.** On gitlab on 2026-08-26 —
three days after the pods' pinned rev 3 had expired — a `valkey-cli` connection with
`--cacert` succeeded. A server presenting an expired certificate should have failed that
verification. So the server appears to have picked up rev 4. Indirect, single observation,
not conclusive.

**The direct experiment is `iam/oauth2-valkey` tomorrow, and it costs nothing.** Its
rotation is 2026-08-27 07:06 UTC and its pods will not be restarted. Baseline captured
2026-08-26 ~10:00 UTC, via `kubectl port-forward` and `openssl s_client` against the pod:

```
server presents: serial=5267BF9A69C9904607624E90C7FC4A2CD53F8ADB
                 notBefore=Jun 28 07:06:23 2026  notAfter=Sep 26 07:06:23 2026
Secret holds:    serial=5267BF9A69C9904607624E90C7FC4A2CD53F8ADB   (identical - rev 2)
```

After 2026-08-27 07:06, re-run the same check:

* **new serial** -> the server reloads; only the Go processes pin; severity stays high.
* **still `5267BF9A...`** -> the server pins too; re-rank this item as **critical** and treat
  2026-09-26 as an outage date for `iam/oauth2-valkey`, not a degradation date.

Note the pod-internal image has no `openssl`, so the check has to run from outside through a
port-forward rather than by `kubectl exec`.

**Mitigation, unchanged by the correction but now correctly dated:** restarting a cluster's
pods after its rotation re-pins the fresh certificate and resets its 90-day clock. There is
no urgency before **2026-09-26**, and the earlier "restart oauth2-valkey tomorrow" advice was
a consequence of the date error and is withdrawn.

### Confirmed by repair, not only by reading — 2026-08-26

The T8 remediation rolled all three `gitlab-valkey` data pods. Before the roll: a continuous
once-per-second `remote error: tls: expired certificate` on all three sidecars, all three
pods labeled `replica`, `-rw` with zero endpoints. After: **zero** error lines on all three,
`-rw` back to exactly one endpoint, and the sidecar log shows the full mechanism working —
pod-0 reported `master` locally while Sentinel still named pod-1, so the cross-check labeled
it `replica`, and only when Sentinel agreed did it log
`role changed {"from":"replica","to":"master"}`.

That is the diagnosis confirmed from both directions: the defect is the **pinned certificate
in a long-lived process**, and a fresh process is sufficient to clear it.

**One component that is NOT affected, verified and worth recording** because it is what keeps
this survivable: the **init container** resolves the master by shelling out to `valkey-cli`
per invocation, so it reads the certificate files fresh every time. Master discovery at pod
boot therefore keeps working through a rotation. Only the long-lived Go processes —
the sidecar and the observer — are broken.

### Fix direction (not decided) — SUPERSEDED, see the Decision sections below and
### [Implemented 2026-08-26](#implemented-2026-08-26)

> ~~The proposal below is what the item was filed with.~~ **It was not what shipped**, in two
> ways: the decision is Option E (roll what cannot reload) with reload as the *exemption*, not
> reload alone; and the reload that did ship compares **bytes**, not modtime, and rebuilds the
> whole config rather than installing a `GetClientCertificate` callback — `RootCAs` cannot be
> swapped per handshake on a client config, which is exactly why the callback approach could
> never have covered the CA half. Kept for the reasoning.

Replace the pinned `Certificates` with a `GetClientCertificate` callback that re-reads the
keypair from disk, with a small cache keyed on file modtime so the hot path does not stat
per connection. The CA pool wants the same treatment via `GetConfigForClient` or a periodic
reload — a rotated CA has the same failure shape and would additionally survive an issuer
change. Both belong in `buildSidecarTLSConfig` so all three callers inherit them, plus the
observer's `buildTLSConfig`.

**Testable in the unit tier**, which is the point: generate a short-lived pair, start a TLS
listener, rewrite the files, and assert the next dial presents the new certificate. No
clock injection needed.

### What this changes about T7

T7's remaining "unproven sentinel-0 edge" is **not** what is happening on gitlab. The
labeler there is provably unable to run at all. See the correction inside T7.

### Not verified

* When the labeler first stopped working. All three pods were already `replica` on
  **2026-08-22**, *before* the 2026-08-23 rotation, and the container log buffer does not
  reach back that far. So something may have stuck the label first and the expiry layered
  on top. Do not assume a single cause.
* Whether the operator's own `:8080/metrics` surfaces anything for this. Not scraped —
  it would have needed a port-forward, which is a write-adjacent action and was out of
  scope for a read-only pass.

### Decision

- 2026-08-26: **filed, no option chosen.** The mechanism is verified in the code and the
  symptom is measured on a live cluster; the fix direction above is a proposal, not a
  decision.
- 2026-08-26 ~09:40: **prioritised FIRST, ahead of T16 and T15**, which had been the
  recommended pair earlier the same day. Reasons, in order: higher severity than anything
  else open; a **dated** trigger rather than a hypothetical one; and a blast radius that
  includes an ADR 0012 contract. T16 and T15 keep their pairing and move to second and third.
  **Implementation deliberately deferred to a fresh session** — the analysis is complete and
  the fix direction is written down, so nothing is lost by starting clean.
- **Open decision the implementation must make first:** whether to reload the **CA pool** as
  well as the client certificate. The client cert alone fixes the measured failure; the CA
  has the same shape and would additionally survive an issuer change. Deciding it up front
  avoids touching `buildSidecarTLSConfig` twice.

### Operational items

> **Corrected 2026-08-26 ~10:05.** ~~`iam/oauth2-valkey` rotates 2026-08-27 07:06, no fix can
> ship before that, restart its pods after the rotation.~~ **Withdrawn — it rested on
> confusing rotation with expiry.** The pinned certificate stays valid for 30 more days after
> a rotation. There is nothing to do tomorrow and no deadline this week.

1. **Read the experiment on 2026-08-27, after 07:06 UTC.** Free, one command, and it decides
   whether this item is high or critical. Procedure and baseline serial are in "The open
   question" above. Do it before the fix is designed: if the server pins too, the fix has a
   second half.
2. **First real deadline: 2026-09-26**, when `iam/oauth2-valkey`'s pinned certificate expires.
   ~~If T21 has not shipped by then, restart that cluster's three data pods.~~ **Superseded
   2026-08-26: T21 has shipped in the repo.** The deadline now reads: *deploy the operator
   carrying this fix before 2026-09-26*. Deploying it is itself the fleet-wide repair — the
   sidecar runs the operator image, so the new version changes the pod-spec hash and rides
   the failover-aware rolling update, which re-pins fresh material on every TLS cluster as a
   side effect. If the deployment slips past the date, the manual restart advice stands
   unchanged: `gpt/gpt-valkey` follows on 2026-09-29, the remaining six on 2026-11-21.
3. ~~Restarting pods is a **recurring 90-day cost** until T21 ships, on every TLS cluster.~~
   **Superseded 2026-08-26.** The roll is now automatic and triggered by the rotation rather
   than by the expiry, so it happens inside the 30-day cert-manager grace window with a month
   of slack. It is still a roll per cluster per rotation — that is Option E working as
   decided, not a residual cost. The **manual** cost is gone.

**Not verified:** whether anything triggers an operator rolling update at rotation time.
Nothing in the code schedules one on a certificate change, so the assumption is that a
restart must be deliberate — but that assumption was not tested. If some other churn
(a chaos kill, a node drain, an operator upgrade) restarts the pods first, the clock resets
silently and the deadline moves without anyone noticing.

### Deep analysis 2026-08-26 (second pass) — five facts that reshape the option space

Verified at `HEAD` = `1c309d8`. Each of these was **not** in the item when it was filed, and
three of them change which fix is correct.

**F1 — The server never requires a client certificate. `tls-auth-clients optional` is
hard-coded and there is no override.**
[`configmap.go:93`](../../../internal/builder/configmap.go#L93) and
[`sentinel.go:143`](../../../internal/builder/sentinel.go#L143) render it unconditionally, and
`TLSSpec` ([`api/v1/valkey_types.go:341-376`](../../../api/v1/valkey_types.go#L341-L376)) has no
mTLS field and the repo has no `extraConfig` mechanism at all (grepped: no
`ExtraConfig`/`CustomConfig`/`configOverride` anywhere). With `optional`, OpenSSL verifies a
certificate that *is* presented but admits a client that presents none.

**So the client certificate the sidecar presents buys nothing today and is the entire cause
of the outage.** A client that presents nothing is admitted; a client that presents an expired
one is rejected. The proof is already in the repo: the reconciler
([`valkey_controller.go:134-137`](../../../internal/controller/valkey_controller.go#L134-L137))
and the health checker
([`health/checker.go:375-378`](../../../internal/health/checker.go#L375-L378)) set **only**
`RootCAs`, present no client certificate, and talk to the same servers without trouble —
which the item already recorded as "why this is invisible from the CR", without drawing the
consequence.

**F2 — The metrics exporter has the same defect, and it is third-party.**
[`statefulset.go:991-1002`](../../../internal/builder/statefulset.go#L991-L1002) hands
`oliver006/redis_exporter` a client keypair by file path
(`REDIS_EXPORTER_TLS_CLIENT_CERT_FILE` / `_KEY_FILE`) — again to a server that does not
require one, and the comment on that block says so out loud. It is a long-lived Go process.
Whether it reloads is **not verified and cannot be verified in this repo**; the fix that makes
the question moot is to stop passing the two variables. Note it already sets
`REDIS_EXPORTER_SKIP_TLS_VERIFICATION=true`, so its CA is not load-bearing either — the client
keypair is its only pinned material.

**F3 — Nothing rolls a pod when the TLS Secret rotates. Now verified, was "not verified".**
The controller does watch Secrets
([`valkey_controller.go:2780-2783`](../../../internal/controller/valkey_controller.go#L2780-L2783)),
but `findValkeyForSecret`
([`:2801-2811`](../../../internal/controller/valkey_controller.go#L2801-L2811)) enqueues only
when `v.IsAuthEnabled() && v.Spec.Auth.SecretName == secret.Name`. A **TLS** Secret matches no
CR and enqueues nothing. The pod hash does not cover certificate content either —
`ComputePodSpecHash` ([`:1123`](../../../internal/builder/statefulset.go#L1123)) hashes the
rendered pod spec, which names the Secret and never its contents. **The item's assumption was
right and is now a fact: a restart has to be deliberate.**

**F4 — The observer is less exposed than the item states, by default.**
`IsObserverValkeyMTLSEnabled` / `IsObserverSentinelMTLSEnabled`
([`api/v1/valkey_types.go:1095-1117`](../../../api/v1/valkey_types.go#L1095-L1117)) both default
to **false**, and [`observer.go:390-413`](../../../internal/observer/observer.go#L390-L413) loads
the keypair only `if withClientCert`. So on a default TLS cluster the observer pins the **CA
pool only**, and the CA pool breaks on an *issuer* rotation, not on a leaf rotation — a
different, much rarer event. The observer is the same *shape* as the sidecar but not the same
exposure. Its client-cert half is live only where `spec.observer.mtls` was opted into.

**F5 — Reload is cheap here, because the client dials per command.**
`valkeyclient.Client` holds no connection: `exec`, `ExecMulti` and `ExecGet` each call
`c.dial()` ([`client.go:334, 364, 430`](../../../internal/valkeyclient/client.go#L393-L407)), so
`tls.DialWithDialer` runs a fresh handshake every command. Any per-handshake callback or
per-dial config rebuild is invoked at the labeler's poll rate — `--poll-interval=1s`
([`statefulset.go:819`](../../../internal/builder/statefulset.go#L819)). Reading a 2 KB PEM from
a tmpfs mount once a second is not a hot path.

**A correction to the item's own fix direction:** it proposes a cache "keyed on file modtime".
The mount is a Kubernetes Secret volume with no `subPath`
([`statefulset.go:549-558, 691-696`](../../../internal/builder/statefulset.go#L549-L558)), which
kubelet updates by swapping the `..data` symlink — so modtime does work, but comparing the
**bytes** is simpler, has no symlink-resolution subtlety, and at 1 Hz costs nothing. The
modtime idea also carries a race the item does not mention: `tls.LoadX509KeyPair` reads
`tls.crt` and `tls.key` in two separate `os.ReadFile` calls, so a swap landing between them
yields a mismatched pair and a hard error. Any keypair-reloading option needs a
"keep the last good pair" fallback. **An option that stops presenting a keypair at all does
not have this problem.**

### Solution options

Five options. Each states what it fixes, what it leaves, and what it costs.

#### Option A — Stop presenting a client certificate nobody requires

Drop `tlsCfg.Certificates` from
[`buildSidecarTLSConfig`](../../../internal/sidecar/labeler.go#L271-L298), drop
`--tls-cert` / `--tls-key` from the sidecar args
([`statefulset.go:826-828`](../../../internal/builder/statefulset.go#L826-L828)), and drop the
two `REDIS_EXPORTER_TLS_CLIENT_*` env vars
([`statefulset.go:994-996`](../../../internal/builder/statefulset.go#L994-L996)). The sidecar
then matches what the reconciler and the health checker already do: CA only.

* **Fixes:** the whole measured failure, on all three sidecar callers *and* on the exporter —
  permanently, because there is no longer any material to expire.
* **Leaves:** the pinned **CA pool** in the sidecar and the observer. Breaks on an issuer/CA
  rotation, which is rare but has the identical silent shape.
* **Cost:** ~10 lines and their tests. No new mechanism, no cache, no race.
* **Security:** loses nothing that exists. mTLS is not enforced by any server this operator
  renders (F1), and it forecloses nothing that is not *already* foreclosed — the ADR 0016
  hardening item "require client certificates where the deployment can" is blocked today by
  the reconciler and the health checker, which present no certificate either. Flipping
  `tls-auth-clients yes` would have to be a coordinated change across five clients regardless.

#### Option B — Reload the client certificate (the item's original fix direction)

Add `GetClientCertificate` to `buildSidecarTLSConfig` and the observer's `buildTLSConfig`,
re-reading the keypair with a small cache and a keep-last-good fallback.

* **Fixes:** the sidecar and the observer's opt-in mTLS.
* **Leaves:** the CA pool, and **the exporter** — which is not our process and cannot be fixed
  this way at all.
* **Cost:** a cache, a mutex, the mismatched-pair race, and a test that rewrites files under a
  live listener. Keeps a certificate in play whose only current effect is to be a failure mode.

#### Option C — A TLS config provider: re-read CA *and* keypair per dial

Replace the pinned `*tls.Config` with a `func() (*tls.Config, error)` the client calls at dial
time, caching on content. `RootCAs` cannot be swapped per handshake on a client config —
`GetConfigForClient` is server-side only, and the `InsecureSkipVerify` +
`VerifyPeerCertificate` alternative is the pattern that is easy to get subtly wrong — so the
provider has to live one level up, in `valkeyclient`.

* **Fixes:** both halves, in sidecar and observer, with one mechanism.
* **Leaves:** the exporter.
* **Cost:** touches the `valkeyclient` public surface (`NewTLS`, `NewTLSWithPassword`) that the
  reconciler and health checker also use, so it is the widest blast radius of the five for a
  failure nobody has measured.

#### Option D — Let the sidecar exit when its material changes

Watch the mount; on change, return from `Run` so kubelet restarts the container with fresh
material.

* **Fixes:** sidecar CA and keypair together, no TLS surgery.
* **Leaves:** the exporter, the observer.
* **Cost and risk:** the exit path must **not** run the ADR 0012 drain handler — `Run` today
  falls straight into `Handle` after the loop returns
  ([`run.go:112-120`](../../../internal/sidecar/run.go#L112-L120)), so a self-exit that reuses
  that path would fire a spurious drain promotion on every rotation. Rising `restartCount`
  also reads as a crash loop in every dashboard. Rejected on the drain interaction alone.

#### Option E — Roll the pods on rotation (operator-side)

Extend the Secret watch to TLS Secrets and put a fingerprint of the certificate into the pod
template, so the failover-aware rolling update replaces pods after every rotation.

* **Fixes:** *everything*, including the exporter and — if the open question resolves that way
  — `valkey-server` itself.
* **Cost:** a full failover-aware roll per cluster every ~60 days, forever. That is exactly the
  recurring cost the item names as the argument *for* a fix and against the workaround; this
  option automates the workaround instead of removing the cause.
* **Where it is still right:** if 2026-08-27 shows that `valkey-server` also pins, then nothing
  in A–D helps the server, and E (or a `CONFIG SET tls-cert-file` nudge) becomes necessary
  **in addition**. It is the contingency, not the fix.

### Recommendation: A, plus the CA half of C, scoped to the two builders

**Option A is the fix; add a content-compared CA reload inside the same two functions
(`buildSidecarTLSConfig`, the observer's `buildTLSConfig`) so the function is touched once.**
The item's own open decision — "whether to reload the CA pool as well" — is answered **yes**,
and A is what makes that answer cheap: with no keypair to reload, the CA reload is one file,
no pair-matching race, no keep-last-good bookkeeping, and it needs no change to `valkeyclient`
because the CA is only consumed through `RootCAs` on a config the sidecar owns.

Why A over B, which is what the item proposed:

1. **B keeps the cause and manages it.** The certificate has no consumer that requires it
   (F1). B builds a cache, a mutex and a race window to keep alive material whose only
   observable effect in this repo has been an outage.
2. **A is the only option that fixes the exporter** (F2), because the exporter is not our
   process. Under B the same 90-day clock keeps running in the container next door, and the
   next incident looks identical.
3. **A converges the fleet on one posture.** Five clients talk to these servers; three already
   present no certificate. A makes it five, and any future move to `tls-auth-clients yes`
   becomes one coordinated decision instead of a half-built state.
4. **Both ship with a roll anyway.** The sidecar runs the operator image
   (`buildPodContainers(v, operatorImage)`), so shipping either fix bumps the image, changes
   the pod spec and rides the failover-aware rolling update — which re-pins fresh material on
   every TLS cluster as a side effect. The fix and the fleet-wide repair are the same event.

Residual risk of A, stated plainly: an issuer rotation would still break the observer's
default CA-only config unless the CA reload ships with it — which is why it is not "A alone".
And A does nothing for `valkey-server` if the 2026-08-27 experiment shows the server pins too;
that stays Option E territory and is a separate item.

### ADR consequence

This is a durable rule, not a bug fix: **a long-lived process may not pin TLS material it does
not need, and must re-read what it does need.** Per the repo convention that a new invariant
gets its own ADR, that is **ADR 0030**, with two edits elsewhere in the same change:

* **ADR 0016** — its Residual risks already carry *"cert-manager renewal is only partially
  covered … whether the running `valkey-server` reloads the new material is not verified"*.
  That risk fired, on the client side rather than the server side. It gets the measured
  outcome and a pointer to 0030, and its `tls-auth-clients optional` risk gains the note that
  three — after A, five — clients present no certificate, so the hardening item is a
  five-client decision.
* **ADR 0012** — the drain promotion contract is what breaks. It gains the precondition that
  the drain's Valkey client must hold usable TLS material, and a pointer to 0030.

`SECURITY_ARCHITECTURE.md` sections 2/6 and the hardening checklist need the same correction.

### Decision (second pass) — SUPERSEDED the same day by "Decision 2026-08-26: Option E,
capability-gated" below. Kept for the reasoning, not for the outcome.

- 2026-08-26, analysis pass: ~~**options A–E written up, A + CA-reload recommended, no option
  chosen yet.** Awaiting the call.~~ **The call was E, capability-gated, and the A
  recommendation was withdrawn — see C7a.** The item's own open question — CA pool yes/no — is
  answered **yes** by the recommendation.
- 2026-08-27 experiment (`iam/oauth2-valkey`, after 07:06 UTC) is **independent of this
  choice**: it decides whether a *sixth* option is additionally required for the server, not
  which of A–E fixes the clients. Do not block the fix on it.

### Decision 2026-08-26: Option E, capability-gated

**Chosen: E — the operator restarts pods whose TLS material it cannot hot-reload, and only
those.** Stated by the decider as: *when the certificate expires and cert-manager issues a new
one and hot-reload is not possible, the pods are brought to restart. Pods that can live-reload
naturally do not have to restart.*

This is **not E as it was written up above**, and the difference matters: the write-up framed E
as "roll everything on rotation, forever", and rejected it as automating the workaround. The
gate removes that objection — the roll becomes the **fallback for containers that provably
cannot reload**, and reload becomes the way a container earns its exemption. A and C do not
lose; they move inside E as the mechanism that earns exemptions.

**ADR handling: deferred by decision (`ADR spaeter, erst Code`).** This is a deliberate
deviation from the CLAUDE.md rule *"Changing behaviour an ADR describes means updating that ADR
in the same change"*, taken with the rule quoted in the option. Until the follow-up lands,
**ADR 0016's residual risk and ADR 0012's drain contract are knowingly stale** — 0016 still
says the renewal gap is "not verified" when it is now measured, and 0012 still states the drain
promotion contract without its TLS precondition. Owed, and recorded here so it is not silently
carried: ADR 0030 plus amendments to 0016 and 0012, plus `SECURITY_ARCHITECTURE.md` 2/6 and the
hardening checklist.

**Experiment 2026-08-27 (`iam/oauth2-valkey` after 07:06 UTC): runs in parallel, does not
block.** Its role changed, see C1.

#### Consequences of the gate, derived after the decision

**C1 — The restart unit is the pod, not the container, so one non-reloading container spends
the whole pod's exemption.** A TLS data pod holds four kinds of process:

| In the data pod | Reloads? | Basis |
|---|---|---|
| init containers | **yes, immune** | shell out to `valkey-cli` per invocation, reads the files fresh — already recorded in this item |
| `valkey-server` | **open** | the 2026-08-27 experiment |
| sidecar (ours) | **can be made to** | Go, our code, F5 says the cost is negligible |
| exporter (`oliver006/redis_exporter`) | **presumed no, unfixable in-process** | F2; third-party, long-lived |

So the data tier is exempt from the roll **only when metrics are off *and* `valkey-server`
reloads**. With `spec.metrics.enabled` the pod rolls no matter what the sidecar does; if the
server pins, the pod rolls no matter what *anything* does — and it **must**, because otherwise
it serves an expired certificate to every client.

**This re-scopes the 2026-08-27 experiment.** It no longer decides severity — the gate covers
both outcomes. It decides **whether the data tier can ever take the exemption at all**. Reading
it stays free and stays worth doing; it just does not gate the design.

**C2 — The observer is where hot-reload clearly pays.** Its own Deployment, our Go process
alone in the pod, no server and no exporter beside it. It can reload and then never restart.
And by F4 its default configuration pins only the CA, so a CA reload alone earns it a permanent
exemption.

**C3 — The Sentinel tier has no sidecar.** Grepped: `internal/builder/sentinel.go` contains no
sidecar container and no `operatorImage`. Its probes shell out to `valkey-cli`
([`sentinel.go:415`](../../../internal/builder/sentinel.go#L415)), fresh per exec. So the Sentinel
tier's exemption is decided purely by whether `valkey-sentinel` reloads — the same open
question as C1, no code of ours involved.

**C4 — A rotation stampede is a real, dated event and E is a new class of automatic action.**
Today **nothing** rolls a pod except a spec change a human made (F3, now verified). E makes a
cert-manager timer a fleet-wide disruptive trigger. This item's own fleet table has
`gitlab`, `harbor` and 4x `database-examples` — **six of eight TLS clusters sharing rotation
date 2026-10-22**. At `--max-concurrent-reconciles=4` ([ADR 0019](../../adr/0019-reconcile-concurrency-and-the-cost-of-a-stuck-pass.md))
that is four failover-aware rolls in parallel, each containing a controlled failover, in one
window. Needs a decision, see the open sub-decisions below.

**C5 — Upgrade neutrality already has the pattern, and it is free.**
`podAnnotationHashChanged` and `podSpecHashChanged`
([`rolling_update.go:367-386`](../../../internal/controller/rolling_update.go#L367-L386)) both
use `podHash != "" && podHash != desiredHash`: **a pod that lacks the annotation is never
forced to restart.** A new `vko.gtrfc.com/tls-material-hash` inherits that, so the operator
upgrade that ships E adopts the current fingerprint without rolling anything — which satisfies
[ADR 0005](../../adr/0005-upgrade-neutral-defaults-and-anti-affinity.md).

One structural note: the fingerprint is Secret **content**, and `buildPodSpec` /
`ComputePodSpecHash` ([`statefulset.go:1123`](../../../internal/builder/statefulset.go#L1123))
take only `*vkov1.Valkey` and never see it. So it must be a **separate pod-template annotation
injected by the reconciler**, not a term inside the existing pod-spec hash. The Secret watch
also has to grow past `findValkeyForSecret`'s auth-only filter (F3).

**C6 — Trigger is the rotation, not the expiry**, as stated in the decision. That buys the full
cert-manager grace window (30 days at the current 90d/30d issuance) for the roll to proceed at
its own pace, and needs no `notAfter` parsing. The cost — a roll that was unnecessary because
everything reloaded — is exactly what the capability gate exists to prevent.

**C7 — ~~A rides inside E, and it is what makes exemptions reachable.~~ SUPERSEDED the same
day, see C7a.** ~~Dropping the client
certificate (Option A) is no longer an alternative to E; it is the cheapest way to make two
containers exempt:
* the **exporter** already sets `REDIS_EXPORTER_SKIP_TLS_VERIFICATION=true`
  ([`statefulset.go:993`](../../../internal/builder/statefulset.go#L993)), so the client keypair is
  its *only* pinned material. Stop passing `REDIS_EXPORTER_TLS_CLIENT_CERT_FILE` / `_KEY_FILE`
  and the exporter has nothing left to expire — which is the only way a metrics-enabled cluster
  can ever take the C1 exemption.
* the **sidecar** without a keypair needs to reload exactly one file, with no
  `LoadX509KeyPair` pair-matching race and no keep-last-good bookkeeping.

Per F1 this costs nothing: no server this operator renders requires a client certificate, and
three of five clients already present none.~~

**C7a — The client certificates stay. C7 argued from the wrong premise, corrected
2026-08-26.** C7 was written before the gate and carried its reasoning over unexamined: Option
A was attractive because it *removed* a failure mode nothing else caught. **E catches it** —
the pod rolls and re-pins. So removing the certificate no longer buys correctness, it buys
one roll exemption for the exporter. That is a roll-frequency argument, and C7 presented it as
a security one.

The premise "the certificate buys nothing" is also an argument from the *current
configuration*, not from the design. At `tls-auth-clients optional` a presented certificate
**is** verified; what is missing is the server-side requirement, not the client-side material.
Throwing the material away does not make a later `tls-auth-clients yes` cheaper — it makes it
more expensive, since it would have to be re-provisioned across five clients.

**The rule that replaces C7:** rotating certificates rotate the instances that cannot reload
them. That is the consequence of the choice to rotate, not a defect to be engineered away.
E implements exactly that, and the exporter is simply one of those instances.

* **Sidecar and observer:** keep the client certificate; earn the exemption by reloading it
  (Option C mechanics — keypair **and** CA, with the keep-last-good fallback that F5 requires).
* **Exporter:** keeps `REDIS_EXPORTER_TLS_CLIENT_CERT_FILE` / `_KEY_FILE`. It is a
  non-reloading instance and therefore rides the roll, by design.
* **Follow-up idea, recorded not scheduled:** should the operator ever ship its **own**
  exporter instead of `oliver006/redis_exporter`, that container becomes reload-capable and
  the metrics-enabled data pod can take the C1 exemption. That is the one change that would
  make C7's conclusion correct — through ownership, not through deletion.

#### Open sub-decisions inside E

1. ~~Does A ride inside E?~~ **Decided 2026-08-26: no.** Client certificates stay everywhere;
   reload earns the exemption where the process is ours, the roll covers the rest. See C7a.
2. ~~Stampede handling for C4.~~ **Decided 2026-08-26: do nothing, accept four parallel rolls.**
   See C4a. The follow-on requirement is an alarm, see C8.

#### C4a — the stampede is not a problem, because the roll is not time-critical

**C4 asked the wrong question, corrected 2026-08-26.** It treated six clusters rolling in one
window as a scheduling problem and looked for a way to spread it. It is not: **cert-manager
rotates 30 days before expiry, and the previously issued certificate stays valid for those 30
days.** Nothing on a pod is broken while its roll is pending, and nothing gets worse if the
roll takes hours. There is no deadline to miss inside the window, so there is nothing for a
deterministic offset to buy — it would add an offset, a requeue and a test to solve a
contention that costs nothing.

What the concurrency cap of 4 ([ADR 0019](../../adr/0019-reconcile-concurrency-and-the-cost-of-a-stuck-pass.md))
already provides is enough: four failover-aware rolls at a time, each internally serialised to
one pod, each with the full [ADR 0007](../../adr/0007-failover-aware-rolling-update.md) machinery.
**The real risk is not "too slow", it is "a roll goes wrong and nobody looks".** That is an
observability requirement, not a scheduling one.

#### C8 — the alarm, and how much of it already exists

**Requirement (decided 2026-08-26): a rollout that fails or stalls because of a certificate
rotation must raise an alarm so a human looks at it.**

Most of that is already true at `HEAD`, verified in
[`deploy/helm/valkey-operator/templates/prometheusrule.yaml`](../../../deploy/helm/valkey-operator/templates/prometheusrule.yaml)
(chart default **off**, per [ADR 0021](../../adr/0021-per-resource-metrics-and-the-alert-that-was-missing.md)):

| A cert-driven roll that... | is already caught by |
|---|---|
| stalls mid-roll | `ValkeyPhaseNotOK` — any non-`OK` phase for 30 min, and the phase reads `Rolling Update i/n` |
| leaves pods not ready | `ValkeyReplicasMissing` — 15 min short of `spec.replicas` |
| cannot write the StatefulSet | `ValkeyReconcileBlocked` — 15 min on the condition |
| wedges on a terminating pod | `PodTerminationStalled` condition ([ADR 0026](../../adr/0026-a-pod-being-deleted-is-not-available.md)) |
| pauses on an unmet sync | `RollingUpdatePaused` condition ([ADR 0010](../../adr/0010-every-rolling-update-wait-is-bounded.md)) |

**Two gaps remain, and only the first is load-bearing:**

* **The roll that never starts.** If the operator never notices the rotation — the Secret watch
  misses it, the annotation write fails, the CR is blocked for an unrelated reason — then
  nothing rolls, nothing stalls, and **no existing rule fires**. The pods keep their pinned
  certificate until it expires and the labeler dies silently, which is T21 unchanged. This is
  the one genuinely new alarm.
* **Attribution.** `ValkeyPhaseNotOK` firing on six clusters at 03:00 with `Rolling Update 2/3`
  says nothing about *why* the roll started. Cosmetic, but it is the difference between a
  five-minute and a one-hour diagnosis.

**Shape of the new alarm, and it is cheap:** a **level** condition — working name
`TLSMaterialStale`, True while any pod's recorded TLS fingerprint differs from the current
Secret. Per [ADR 0027](../../adr/0027-conditions-are-levels-edges-or-history.md) a level is
re-measured every pass, owes exactly one evaluator and needs **no clear site**, so it
self-corrects when the roll completes. It needs a `conditionRegistry` row
([`condition_registry.go`](../../../internal/controller/condition_registry.go)) or the unit tier
goes red.

**The metric is free.** `collectResource`
([`internal/metrics/collector.go:186-192`](../../../internal/metrics/collector.go#L186-L192))
emits one `vko_valkey_status_condition{condition,status,reason}` series per condition on the
resource, so a new condition is exported with **zero** collector work — and that respects the
standing ADR 0021 constraint that no gauge is written from a reconcile pass.

**The rule's `for:` is where C4a gets encoded.** Because the roll is not time-critical, the
threshold is **days, not minutes** — a stale fingerprint for ~72 h means the roll is not
happening while ~27 days of grace remain. A short threshold would page on every normal roll.

```
- alert: ValkeyTLSMaterialStale
  expr: |
    max by (namespace, name, reason) (
      vko_valkey_status_condition{condition="TLSMaterialStale",status="True"}
    ) == 1
    and on () max(vko_valkey_collector_success) == 1
  for: 72h
  labels: {severity: warning}
```

Same `collector_success` guard and same `max by (...)` aggregation as every existing rule, for
the reasons stated in that file's header comment.

### Implemented 2026-08-26

**Option E, capability-gated, both halves, at `HEAD` = `1c309d8` plus this change.** The
decision is unchanged from "Decision 2026-08-26: Option E, capability-gated" above; nothing
in it was re-opened during implementation.

#### The gate, as it actually resolves today

Reload earns the exemption; everything else rides the roll. The table is written down in code
at the top of [`internal/controller/tls_material.go`](../../../internal/controller/tls_material.go)
so the next reader finds it next to the mechanism rather than in this file:

| Process | Verdict | Basis |
|---|---|---|
| init containers | reloads, exempt | shell out to `valkey-cli` per invocation, already recorded above |
| the sidecar | reloads, exempt | new, `internal/tlsmaterial` |
| the observer | reloads, **and takes the exemption** | new, and it is alone in its Deployment (C2) |
| `valkey-server` | treated as pinning | never measured; the 2026-08-27 experiment is still open |
| `valkey-sentinel` | treated as pinning | same, and the tier has no code of ours at all (C3) |
| `oliver006/redis_exporter` | pins | third-party (F2); rides the roll by design (C7a) |

So today **both StatefulSets carry the fingerprint and roll on rotation, and the observer
Deployment carries none and never restarts.** That is the honest reading of the gate with the
server question unmeasured — if the 2026-08-27 experiment shows `valkey-server` reloads, the
data tier still rolls whenever `spec.metrics.enabled` is set, and exempting it otherwise is a
one-place change in `stampTLSMaterialHash`.

#### What shipped

| Half | Where |
|---|---|
| Re-read CA **and** keypair per dial, bytes-compared, keep-last-good | [`internal/tlsmaterial/reloader.go`](../../../internal/tlsmaterial/reloader.go) |
| Sidecar: one source for all three collaborators (role detector, Sentinel querier, drain client factory) | [`internal/sidecar/labeler.go`](../../../internal/sidecar/labeler.go) `sidecarTLSReloader` / `newValkeyClient`, [`internal/sidecar/drain.go`](../../../internal/sidecar/drain.go) |
| Observer: same, for the Valkey and the Sentinel config | [`internal/observer/observer.go`](../../../internal/observer/observer.go) `newTLSReloader`, [`internal/observer/checks.go`](../../../internal/observer/checks.go) |
| Fingerprint of `ca.crt`/`tls.crt`/`tls.key` | [`internal/builder/tls_material.go`](../../../internal/builder/tls_material.go) `ComputeTLSMaterialHash` |
| `vko.gtrfc.com/tls-material-hash` | [`internal/builder/annotations.go`](../../../internal/builder/annotations.go) |
| Stamped onto both pod templates by the reconciler (C5: the builder never sees Secret content) | [`internal/controller/tls_material.go`](../../../internal/controller/tls_material.go) `stampTLSMaterialHash`, called from `reconcileStatefulSet` and `reconcileSentinelStatefulSet` |
| Read from the **persisted** StatefulSet, never from the CR | [`internal/controller/rolling_update.go`](../../../internal/controller/rolling_update.go) `tlsMaterialHashFromSts`, `podTLSMaterialHashChanged`, wired into `podNeedsUpdate` and `sentinelPodNeedsUpdate` |
| Secret watch past the auth-only filter (F3) | [`internal/controller/valkey_controller.go`](../../../internal/controller/valkey_controller.go) `secretConcernsValkey` |
| `TLSMaterialStale` level + reasons | [`api/v1/valkey_types.go`](../../../api/v1/valkey_types.go), evaluator `reportTLSMaterialStale` as the last **resource** step |
| Registry row (ADR 0027) | [`internal/controller/condition_registry.go`](../../../internal/controller/condition_registry.go) |
| `ValkeyTLSMaterialStale`, `for: 72h` | [`deploy/helm/valkey-operator/templates/prometheusrule.yaml`](../../../deploy/helm/valkey-operator/templates/prometheusrule.yaml) |
| User-facing docs | `README.md` — new `TLSMaterialStale` condition row, a **Certificate rotation** section under TLS Details, alert count 7 → 8 |
| The standing rule | `CLAUDE.md` — "Rotating certificates rotate the instances that cannot reload them" |

Three decisions taken during implementation that the analysis had left implicit:

1. **`valkeyclient` was not touched.** Option C's write-up said the provider "has to live one
   level up, in `valkeyclient`", and called that the widest blast radius of the five. It does
   not: the client holds no connection (F5), so the caller builds one per command from the
   current config and the client stays a dumb per-dial thing. The reconciler and the health
   checker keep their existing `NewTLS`/`NewTLSWithPassword` signatures untouched.
2. **Bytes, not modtime**, as the F5 correction already argued — plus `tls.X509KeyPair` on the
   bytes that were compared, so the pair that is verified is the pair that was read.
3. **The evaluator is a resource step, not a workload one.** Every arm of `reconcileWorkload`
   returns early while a rolling update is in flight, which is precisely when the condition is
   True; `runReconcileSteps` runs every step of every pass. This is the ADR 0027 staleness
   shape, avoided rather than re-created.

#### What was verified, and how

Everything below was run on this machine on 2026-08-26.

* `make lint` — `0 issues` (golangci-lint, `go vet`, `gofmt -l .`).
* `make cyclo` — `All functions are below complexity threshold 15`.
* `make gosec` — `Issues: 0` over 50 files.
* `make generate-all` then `git status --porcelain` — no generated drift, so the
  `generated-manifests` CI job stays green.
* `helm template ... --set metrics.prometheusRule.enabled=true` — the new alert renders.
* Unit tier, `go test ./... -count=1` — every package `ok`, including the new
  `internal/tlsmaterial`. `go test ./internal/tlsmaterial/ -race` also `ok`.
* Integration tier, `make test-integration` — `ok`, including the two new tests.

**The tests that carry the mechanism**, since the item's own "why no tier catches it" said no
tier could:

| Claim | Test |
|---|---|
| a rotated client certificate is presented on the next handshake | `TestReloader_PresentsARotatedClientCertificate` — a TLS listener with `RequireAndVerifyClientCert` records the peer serial; it changes from 100 to 200 after the files are rewritten, no restart |
| a rotated **CA** is trusted on the next handshake | `TestReloader_TrustsARotatedCA` — the dial fails, the CA file is replaced, the dial succeeds |
| a half-swapped mount does not replace a working config | `TestReloader_KeepsTheLastGoodConfigOnAMismatchedPair` — `tls.crt` from revision 2 next to `tls.key` from revision 1 returns the same config pointer, and the pair is adopted once the key lands |
| unchanged bytes are not reparsed | `TestReloader_UnchangedFilesAreNotReparsed` — pointer identity |
| all three sidecar collaborators re-read per call | `TestValkeyRoleDetector_RereadsTLSMaterialPerCall`, `TestSentinelMasterQuerier_RereadsTLSMaterialPerQuery`, `TestRealValkeyClientFactory_RereadsTLSMaterialPerClient` — each broken-then-repaired, which is the production order |
| the observer re-reads per call | `TestNewClient_RereadsTLSMaterialPerCall` |
| a pod without the annotation is never restarted for one | `TestPodTLSMaterialHashChanged`, `TestReportTLSMaterialStale_PodsWithoutTheAnnotationAreUnmeasured` |
| a rotation reaches the pod template with **no CR change at all** | `TestTLSMaterialRotation_ReachesThePodTemplate_Integration` — the CR watch carries `GenerationChangedPredicate`, so the Secret watch is the only thing that can have started that pass |
| a non-TLS cluster never gains the condition | `TestTLSMaterialStale_NonTLSClusterIsNeverMeasured_Integration` |

The item's claim that **"no unit test can reach this without injecting a clock or a
short-lived CA"** is the one thing the implementation disproved: rewriting the files under a
live listener reproduces the event in milliseconds, because the failure is not about time at
all — it is about a process holding bytes the disk no longer has.

#### Not verified, and deliberately left out

* **The 2026-08-27 `iam/oauth2-valkey` experiment is still open**, and still worth reading. It
  no longer decides severity or design (C1), only whether the data tier could ever take the
  exemption. Nothing in this change depends on it.
* **No e2e.** A rotation needs cert-manager to issue twice, which the e2e cluster does not do;
  what an e2e *could* cover — that a changed pod-template annotation rolls the cluster — is the
  existing rolling-update machinery and is already covered. Recorded as a gap rather than
  claimed.
* **The exporter still pins its client certificate**, by design (C7a). It is a non-reloading
  instance and rides the roll. The "ship our own exporter" idea stays recorded, not scheduled.
* ~~**The ADR debt was not paid, by decision** (`ADR spaeter, erst Code`)~~ — **paid
  2026-08-26**, the same day it was deferred. ADR 0030 is written and carries eleven decisions;
  ADR 0016 D12 is struck in place for TLS and stands for the password, its Status item count
  corrected, its cert-manager residual risk **rewritten rather than ticked off** because the
  `valkey-server` half is still unmeasured; ADR 0012 gained **D11**, the second way D10's
  premise fails; `SECURITY_ARCHITECTURE.md` gained a two-consumers table in section 2, a
  corrected section 6 row, a corrected `findValkeyForSecret` line reference and **two new
  hardening-checklist entries** — the roll that never starts, and a refusal to extend the
  content fingerprint to low-entropy secrets. `CLAUDE.md`s debt paragraph is replaced by the
  discharge plus that correction.
* **Nothing was deployed.** The fleet still runs the old operator, so every date in
  "Operational items" applies until it is rolled out.

#### One consequence that was not in the analysis, found while implementing

**On a single-replica cluster the rotation roll is a restart of the only pod, and without
`spec.persistence` that discards the dataset.** Nothing new was introduced — a config-hash or
pod-spec change already deletes the only pod, and only a *sidecar-only* change is deferred
([`rolling_update.go`](../../../internal/controller/rolling_update.go), `isTrueStandalone &&
isSidecarOnlyChange`). What is new is the **trigger**: a cert-manager timer now starts that
restart without anyone editing the CR, which is the first time anything in this operator has
done that (F3 verified that nothing did before).

Not treated as a defect, and deliberately not special-cased: refusing to roll a standalone
would leave `valkey-server` holding a certificate that expires 30 days later, and if the
server pins — still unmeasured — that is an outage instead of a restart. Recorded in
`README.md` under **Certificate rotation** so it is a documented property rather than a
surprise, with the recommendation to enable persistence on standalone instances whose dataset
matters.


---

## T22: The "no second delete" E2E blamed the operator for an overlap the test itself created

**Severity: none for the product, high for trust in CI** — a green invariant reported as
violated is the failure mode that gets an invariant switched off. **Status: FIXED 2026-08-26.**
Found from a red run on PR #195, [job
98116271850](https://github.com/guided-traffic/valkey-operator/actions/runs/32949039400/job/98116271850),
which predates the T21 change and is unrelated to it.

```
--- FAIL: TestE2E_RollingUpdate_NoSecondDeleteWhileAPodTerminates/no-sentinel/No_two_data_pods_terminated_at_once
    Error: Should be empty, but was [[term-no-sentinel-1 term-no-sentinel-2] terminated simultaneously ...x3]
```

### The operator was right, and the log proves the order

| Time (UTC) | Who | What |
|---|---|---|
| 08:57:55 | operator | deletes `pod-1` (roll, youngest-first) |
| 08:58:05 | **operator** | `pod-1` back and available → deletes `pod-2`, and the very next pass logs `Waiting for the replaced pod to become available {"pod":"term-no-sentinel-2"}` |
| 08:58:05.628 | **the test** | deletes `pod-1` as its chaos victim → both terminating, 3 samples ≈ 750 ms |
| 08:58:17 | operator | manual failover, deletes `pod-0` — the tier was quiet again |

**That the operator was allowed to delete `pod-2` at all is the proof it did not violate
anything**: the ADR 0026 gate sits immediately in front of every `deleteOwnedPod`, so a
terminating `pod-1` would have made it hold. It deleted, therefore the tier was quiet,
therefore the test deleted second. The operator issued exactly one delete in that window and
then waited.

### Why it is systematic and not bad luck

`waitForAReplacedPodMidRoll` waits for *a replaced pod to be Ready while another is still
outdated*. That is the **same event** that unblocks the roll to take its next candidate. Test
and operator are triggered by one edge and race for who deletes second; the loser is blamed.
The sentinel topology passed in the same run purely because it lost the race the other way.

### What was NOT the fix

**"Only inject while the tier is quiet."** The window between a replacement becoming Ready and
the operator deleting the next pod is closed inside the same second (measured above: both at
08:58:05). A test that waits for that window would mostly never inject, pass, and silently stop
testing the scenario it is named for. Rejected.

### The fix: attribute, do not avoid

`test/e2e/pod_termination_test.go`. Immediately before the injection the test reads which data
pods already carry a `DeletionTimestamp` and hands that plus the victim to the sampler
(`attributeTo`, armed **before** the delete so the first 250 ms sample cannot miss it). An
overlap is excused only when it contains the victim and every other pod in it was already on
its way out at that instant — that is the operator having deleted first, into a quiet tier, and
the test piling on. **A pod the operator puts on its way out after the injection is still a
violation**, and so is any overlap the victim is not part of, and so is a third pod joining an
excusable pair.

Ordering the two deletions from the objects is not possible: `metav1.Time` has second
granularity, and both deletions landed in the same second.

**Residual hole, stated rather than papered over:** an operator delete landing between the
test's snapshot and its own delete is excused. That is one API round trip against a roll that
deletes about three times in ninety seconds — and a delete the operator issues that fast came
from a pass whose state read predates ours, which is manager-cache lag, not the bug ADR 0026
fixes.

### Verified

* `TestTerminationSampler_AttributesOverlapsToWhoCausedThem` — six cases including the exact CI
  shape, driven directly through `record` so the path does **not** depend on the race firing.
  `TestTerminationSampler_KeepsReportingAfterAnExcusedOverlap` pins that excusing is per
  observation and not a switch that disables the assertion.
* The full e2e twice against Kind (`valkey-operator-test`, Kubernetes 1.36.1,
  `E2E_VALKEY_LINE=9`), both topologies **PASS**. Second run confirms the injection really
  happens: `Deleting the already-replaced pod term-sentinel-1 mid-roll (already terminating: [])`
  and the same for `no-sentinel`, `excused 0` — on this machine the test wins the race, in CI
  the operator won it once. Both outcomes are now correct.
* `gofmt`, `go vet -tags=e2e ./test/e2e/`, and `golangci-lint --build-tags=e2e` clean **for this
  file** (the package carries 22 pre-existing findings and is not linted in CI).

### Not verified

* The failure was not reproduced deliberately on a cluster — forcing the operator to win the
  race would need a timing hook that does not exist. The deterministic sampler test stands in
  for it, and it is built from the measured CI timeline rather than from a guess.
* ADR 0026 gained a residual-risk bullet for this. No decision of that ADR changed; the
  operator behaviour it describes was never at fault.

## Board archive — final state of `local_BOARD.md`, retired 2026-09-27

Retired on Hans's instruction: a board row was never sufficient; every finding is a ticket
file, new or appended to its family ticket ([`README.md`](../README.md#the-filing-rule), "The filing rule"). Kept
here verbatim, headings demoted one level, because its RELEASE narrative records cluster
operations that exist nowhere else — the T9 `SENTINEL RESET` campaign across six
environments and the harbor-valkey `ForeignObject` repair — and its tables are the only
listing of the open items that never got a file (T12, T18, T23, T26, T29, C2, C3, S1). The
T35 row below predates T35's decisions of 2026-09-27; the ticket file is current.

### Ticket board (verbatim)

last groomed: 2026-09-26 (T31 reopened, T33 and T34 filed, ADR 0025 D9 with its own clock and the drain-test fix recorded on T31; T33 analysed into its own file, moved to NEXT and adversarially re-checked; T34 analysed into its own file and moved to NOW by rule 1, then adversarially checked: three more false "single-node" records added, the NOW heading moved off the released PR #195; T34 doc half landed in `f5c6886`, T34 moved to NEXT; T35 filed in LATER from the wds18 v1.13.0 upgrade check; T35 refined with an Options section on request, decision open)

One row per open item, grouped by urgency. Rules for filing, the severity/security scales and
the urgency derivation: [`README.md`](../README.md). Analysis lives in the ticket
files — for T7–T29 currently still in the archive
[`039-findings-from-the-1-11-0-fleet-rollout.md`](039-findings-from-the-1-11-0-fleet-rollout.md); rows link into it. New tickets get
their own `local_TNN-*.md`.

Columns: **Sev** = impact if never fixed. **Sec** = threat-model class
(`live` = weakens an existing guarantee, no attacker needed; `boundary` = needs a hostile
principal; `hardening` = defense-in-depth; `—` = none). **Eff** = XS/S/M/L.

### NOW — before merge/release of the current branch (`feat/rootless`)

| ID | Item | Sev | Sec | Eff | Blocked by | Note |
|---|---|---|---|---|---|---|

*Empty since 2026-09-26: T34's doc half landed in `f5c6886`, T34 is in NEXT.*

*History, 2026-08-28 (PR #195, since released as v1.12.0, see RELEASE):* *~~Empty.~~ The valkey9 leg peeled three layers off the same race: T13 (terminating pod adopted
as authority), then the drain promoting a terminating peer, then the delete gate trusting a
lagging cache — all three fixed 2026-08-27 and **confirmed by CI on 2026-08-28: 16/16
checks green on `e748ed1`, both e2e legs and integration included, PR #195 mergeable.**
Merge and release; T9 cluster ops unblock with the release.*

### RELEASE — gated on shipping PR #195

*Empty — v1.12.0 released 2026-08-28 and rolled across the fleet, and **T9 was executed the
same morning**: `SENTINEL RESET` on all 8 sentinel clusters (the filed 7 plus
valkey9-sentinal-tls), master-health verified per cluster first, one sentinel at a time,
tables rebuilt in 3-12 s each, final state 2/2/2 on all 24 sentinels. Verified by the
condition itself: `SentinelPeersStale=False/SentinelPeersConsistent` on every CR within one
5-minute recheck (09:14-09:16 UTC). The reset is one-time by mechanism - ADR 0022 pins the
sentinel identity to the ordinal, so replacements reuse their identity and no new ghosts
accrue; the operator watches via the condition and deliberately never resets itself.
**Also executed on mgmt-p (ske-mgmt-prod) later the same morning**: gitlab-valkey (was 4/3)
and oauth2-valkey (was 3) reset and verified `SentinelPeersConsistent`; harbor-valkey was
already consistent and was left untouched. One finding handed to the user there, pending an
explicit go: **harbor-valkey is `ReconcileBlocked=True/ForeignObject`** — its three
ConfigMaps carry the operator labels but no ownerReferences (orphaned by a July 20 CR
recreate, CMs predate the CR by 20 min), written anyway by the pre-1.12.0 operator and
correctly refused since the upgrade brought the ADR 0020 provenance checks. Data plane is
healthy (`Ready=True`), but config changes do not converge and the Sentinel roll has not run
(its sentinel pods are `TLSMaterialUnmeasured`). **Fixed with explicit go later that day**: the three
orphaned ConfigMaps were backed up and deleted one at a time, the operator recreated each
owned within 2-28 s, the block cleared on the next pass, the backed-up Sentinel roll ran and
armed the TLS records (`TLSMaterialUnmeasured` -> `TLSMaterialCurrent`), the transition
roll's fresh ghosts were reset, and all three mgmt-p CRs verified
`OK`/`SentinelPeersConsistent`/`TLSMaterialCurrent`.
**T9 also executed on infra-d, awe-d, awe-q and mgmt-d** (five CRs, same runbook, all
verified `SentinelPeersConsistent` within one recheck) — the campaign now covers all six
environments, 16 sentinel clusters, every table live-verified 2/2/2 and every verdict read
off the condition v1.12.0 shipped. Note: `~/.kube/ske-awe-dev.yaml` carries expired
credentials; awe-d was reached via `kubeconfig-awe-d.yaml`.*

### NEXT — after release

| ID | Item | Sev | Sec | Eff | Blocked by | Note |
|---|---|---|---|---|---|---|
| T30 | Embargoed security finding, open - details in its own ticket file until it is fixed | high | boundary | M | decision | |
| T33 | [Integration tests read through the manager cache right after a write](../033-integration-tests-read-the-cache-after-a-write.md) | medium | — | M | decision | Analysed and adversarially re-checked 2026-09-26, every site re-verified at `a8e8931`. Reproduced locally on one test (T31) and fixed on `feat/rootless`; CI's integration check was red on `e2ce8bb`. The rest is pre-existing on `main`: three flake sources, negative checks a regression can pass (one by construction), latent cached reads, and `writePhase` without a conflict retry. Recommended: uncached test reads plus per-site fixes for the write-order half. Moved from LATER by derivation rule 3. |
| T34 | [e2e fixtures wait on controller state after deleting a pod](../034-e2e-fixtures-wait-on-controller-state-after-a-pod-delete.md) | medium | — | S | decision | Analysed and adversarially checked 2026-09-26 at `a8e8931`; doc half (six false and three stale sentences, the D51 miscitation) landed in `f5c6886`, NOW → NEXT by rule 3. Open: two of five sites are vacuous (`sidecar_test.go:484`, `sentinel_stale_master_test.go:126-127`), three are fine. Recommended: UID waits at both sites, no delete-and-wait helper. |

### LATER — real, dormant or cheap, no deadline

| ID | Item | Sev | Sec | Eff | Blocked by | Note |
|---|---|---|---|---|---|---|
| T23 | [`pauseRollingUpdate` records no pause](../023-pauserollingupdate-records-no-pause.md) | low | — | L | adr-0010 | Four tracked sentences promise a pause that does not exist. |
| C2 | [ADR 0017](../../adr/0017-test-and-ci-policy.md) Alternatives records the fact of the three-tier split, not the decision | low | — | XS | — | Reopening risk `041-the-integration-tier-writes-no-valkey-values.md` exists to close. |
| C3 | 53 `NA61`–`NA63` citations in tracked files point at a gitignored document | low | — | XS | — | 5 Go, 7 `SECURITY_ARCHITECTURE.md`, 41 ADR lines. Mechanical. |
| T35 | [Records of who the master is lag the real master after a handover](../035-master-records-lag-the-real-master.md) | low | — | S | decision | Seen on wds18 after the v1.13.0 upgrade (which itself went as intended), pre-existing: A `status.masterPod` of `valkey9` names a replica for 27+ min (no pass after the stale-label check; fix: recheck); B Sentinel-path `known-master` lags a chaos failover (cosmetic, init validates); C the killed master's replacement booted as a second empty master for ~5–10 s (to reproduce). File on explicit request, below the filing bar. **Refined 2026-09-26 with an Options section** (A1–A4, B1–B2, C1–C3, L1–L2): recommended first step A1+A2+B2, C gated on a Kind reproduction, A4 a nine-document sweep; decision open. |

### ICEBOX — needs a product call, a human re-decision, or an unaccepted threat-model escalation

| ID | Item | Sev | Sec | Eff | Blocked by | Note |
|---|---|---|---|---|---|---|
| T12 | [Write fencing: `min-replicas-to-write` as opt-in](../012-no-write-fencing-min-replicas-to-write-as-an-opt-in-field.md) | medium | — | L | product | Feature, not a fix. Decision section deliberately empty. |
| T18 | [`Ready` keeps its pre-roll value during a rolling update](../018-ready-keeps-its-pre-roll-value-during-a-rolling-update.md) | low | — | S–L | human | Not a defect; a re-decision on which reading of `Ready` is intended. |
| T26 | [Embargoed security finding, open - details in its own ticket file until it is fixed](#t26-embargoed-security-finding-open---details-in-its-own-ticket-file-until-it-is-fixed) | medium | boundary | M | decision | |
| T29 | [Chart-shipped ValidatingAdmissionPolicy, default off](../029-a-chart-shipped-validatingadmissionpolicy-default-off.md) | low | hardening | M | adr-0015 | Only in-cluster control reaching `ownerReferences`/`finalizers`/image; D2 must be amended, not stretched. |
| S1 | [`042-enforce-the-standing-constraints-with-a-static-analysis-test-net.md`](../042-enforce-the-standing-constraints-with-a-static-analysis-test-net.md) | — | — | L | — | Sibling file, open, nothing built. |

### DONE

| ID | Item | What shipped |
|---|---|---|
| T31 | [Generated pods run as root](031-generated-pods-run-as-root.md) | **Reopened and done 2026-09-26:** extension to the full pod-manifest hardening for every Valkey pod and the operator, ADR 0033 (seccomp `RuntimeDefault`/allow-listed `Localhost`, opt-in user namespaces, digests, `privileged`/service links, Sentinel resources); the CI-red analysis of `e2ce8bb`; [ADR 0025](../../adr/0025-a-split-brain-warning-means-one-that-did-not-resolve-itself.md) D9, decided 2026-09-26 — the hardening e2e found the Sentinel rolling update demoting the replica Sentinel was promoting (pre-existing on `main`); while `failover-triggered` the double master is reported, not resolved — since the follow-up of the same day only within 90 s of the failover timestamp (`ownFailoverInFlight`), and state and timestamp are armed in one write (`setFailoverTriggered`, ADR 0010 D14). ~~Final e2e on the final image green (fleet upgrade, 53/53 on Valkey 9 and 8); lint/cyclo/gosec/vuln/coverage/image tools not yet recorded.~~ *(Updated 2026-09-26: that run is now the one before the final one.)* Final e2e, image with the D9 clock and the one-write arming: fleet upgrade green, Valkey 8 53/53, Valkey 9 52/53 — the failure `TestE2E_SidecarFailoverDrainMaster`, a fixture that waited on controller state after deleting the master (T34), fixed by waiting for the new UID, 8/8 green on Valkey 9 alone. CI parity (generate-all, lint, cyclo, gosec, vuln, coverage, image tools, release tooling) green on the tree before these follow-ups; rerun on the final code not yet recorded. Before: 2026-09-26, `feat/rootless`, ADR 0032: every generated pod rootless (uid/gid/fsGroup 999, RuntimeDefault, read-only root, drop ALL), no option; pre-flight + hash-neutral best-effort CHOWN repair while legacy pods exist; single pods split by persistence (`PodSecurityUpdatePending`). Decided 2026-09-26: a second roll replaces the pods that carry the retired repair. |
| T32 | [A replaced pod that never becomes available stalls the roll](032-unavailable-replaced-pod-waits-unbounded.md) | 2026-09-26, `feat/rootless`, ADR 0026 D11: outdated pods replaced not waited for; every other wait bounded by `syncTimeout` on the pod's clock, `PodAvailabilityStalled`; a holding data tier holds the Sentinel roll. 1-/2-Sentinel tiers now roll serially (ADR 0024 D10, decided 2026-09-26). |

21 items shipped on `fix/bad-findings` (T1–T7, T10(A, and B via T16), T11, T13, T14–T17,
T19–T22, T24 complete, T25, T27, T28) plus T8 executed on the cluster and the C1 README
correction. The 2026-08-27 evening batch: `RWServiceEmpty` (T7, ADR 0012 D12), honest
completion Events with tests (T17), the served-certificate observation as the ADR 0030 D6
measuring instrument (T28, report-only, D6 unmoved), and `PodRecreationStalled` bounding
the last three unbounded rolling-update waits (T10(A), ADR 0010 D16). Newest: **T27 closed 2026-08-27
by the write gate** — ADR 0030 D12, "the operator never persists a TLS pod template without a
material record" — a deliberate deviation from the recorded recommendation A, reasoned in the
ticket's Decision section. Full done table with commits and what shipped:
[archive, "Repo work — done"](#repo-work--done).
