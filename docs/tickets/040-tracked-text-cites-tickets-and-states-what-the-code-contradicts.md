---
id: T40
title: tracked text cites tickets and states what the code contradicts
state: analysed       # every line located; the citation sweep waits on Q1-Q4
severity: low         # misleading text, no operator behaviour; the worst record (ADR 0012 D9) hides a measured write loss
security: hardening   # the password carriers, the sidecar Role and the ClusterRole name are statements in the security records
threat: "no attacker; a reviewer of the threat model reads the password exposure on the wrong process and a sidecar Role wider than the code grants, while every real carrier is readable only inside a container that already holds VALKEY_PASSWORD."
urgency: now          # rule 1: measured-false statements in tracked files, in both parts
effort: L             # about 270 citation lines plus sentence-level corrections
blocked-by: human     # only the CLAUDE.md edits; the sweep also waits on Q1-Q4
filed-from: formerly labelled C3
opened: 2026-09-27    # earliest recorded member date
decided:
done:
---

# T40 - tracked text cites tickets and states what the code contradicts

**Scope.** Tracked files outside `docs/tickets/` carry two kinds of wrong text: pointers into
tickets, which [ADR 0034](../adr/0034-tickets-are-work-lists-that-get-archived.md) D7 forbids,
and sentences in comments, ADRs and pages that the code or a measurement contradicts. Both are
text-only edits in the same files (ADRs 0012, 0017, 0026, `CLAUDE.md`, Go comments), so they land
as one correction pass with one verification.

- **Ticket citations**: every T-label, `NA` label, sub-label, path and unlabelled "the ticket"
  pointer outside `docs/tickets/`, rewritten to the ADR that holds the rule.
- **Contradicted sentences**: eleven comments, ADR sentences and security-page statements (a)-(k)
  that the code or a measurement refutes.

## Current state

### Ticket citations

D7 forbids citing a ticket (number, T-label, file name, path) outside `docs/tickets/`. It is
partly implemented: no new citation may be written, the existing ones are this ticket's work
list (`0034:162-166`). Enforcement is manual (`0034:262-263`); nothing in the Makefile, CI or a
hook checks it.

- **190 T-label lines** (`git grep -nwE 'T[0-9]+'`): 110 in `docs/adr`, 70 in 26 Go files, 7 in
  [`CLAUDE.md`](../../CLAUDE.md) (`:284`, `:568`, `:620`, `:785`, `:794`, `:1014`, `:1047`), 3 in
  [`rootless-migration.md`](../security/rootless-migration.md) (`:27`, `:47`, `:85`). No embargoed
  label occurs; `config/crd/bases/` carries none.
- **41 `NA61`-`NA63` lines** (37 in ADR 0020, 4 in ADR 0006). `NA61` -> ADR 0020 D1 (StatefulSets,
  observer Deployment), `NA62` -> ADR 0020 D1 (every managed kind) and ADR 0006, `NA63` -> ADR 0020
  D9 (pods); a consumer treating a foreign object as absent is ADR 0020 D8.
- **8 path citations** in ADRs: `0012:292`, `0017:612`, `0025:444`, `0025:496`, `0028:30`,
  `0032:6`, `0033:6`, `0033:750`.
- **About 38 lines no T or NA grep finds:** 23 Go lines with sub-labels of archived tickets (21 in
  [`pod_termination_test.go`](../../internal/controller/pod_termination_test.go): E1-E6, S1, S3,
  S4, S7, S8;
  [`reconcile_steps_test.go:126`](../../internal/controller/reconcile_steps_test.go#L126) F1;
  [`rolling_update_paused_condition_test.go:167`](../../internal/controller/rolling_update_paused_condition_test.go#L167)
  Q2); 2 WP lines (ADR 0003 `:229` WP1, ADR 0005 `:421` WP5); 14 "the ticket" mentions with no
  identifier - content pointers at ADR 0003 `:112`, `:229`, ADR 0005 `:414`, `:422`, `:458`, ADR
  0028 `:283`, ADR 0032 `:76`, ADR 0033 `:714`,
  [`topology_abandon_test.go:13`](../../test/e2e/topology_abandon_test.go#L13); narrative at ADR
  0027 `:54`, ADR 0005 `:96`, `:101`, ADR 0033 `:573` (struck). ADR 0005 `:100-101` says "the
  existing mentions of the admission-gap ticket stay"; whether that records a decision by Hans is
  not recorded.
- **Tags, nouns and handles.** A tag records provenance and can be dropped without changing the
  sentence (`Amended 2026-08-22 (NA61):`, `(measured, T31)`): roughly 50-56 of the 151 labelled ADR
  lines, plus Go comments such as [`pod_security.go:175`](../../internal/builder/pod_security.go#L175),
  `:197`. A noun or handle carries meaning ("the NA62 amendment adds", "`Ready`/T18", "the S1
  regression guard"). ADR 0020's three amendments of 2026-08-22 (`:41`, `:46`, `:53`) are named
  only by their labels (`:206`, `:281`, `:563-595`, `:653-699`, `:725`, `:767`); so are ADR 0006
  `:21-23`, `:322-323` and ADR 0026 `:121`. `git log -S '<header text>' -- <adr>` recovers the
  provenance (`git blame` names only the last commit on the line).
- **Labels without a one-to-one home.** By ADR 0026's decisions: E1 -> D1 (with the D2 carve-out),
  E2 -> D4, E3 -> D5, E4 -> D9, E5 -> D5 (bounded observation), E6 -> D6, S7 -> D7; S1, S3, S4, S8
  are review findings with no decision and must be restated inline. Q2 -> ADR 0002 D10b, F1 ->
  ADR 0001, T24(c) -> ADR 0030 D12; T24(a), (b), (d) (ADR 0030 and about 20 Go lines) have no
  stated mapping; "the T24(d) neutrality style" is ADR 0005 D10's presence-guarded clear. "Row N
  of T32" ([`pod_availability_test.go:186`](../../internal/controller/pod_availability_test.go#L186),
  `:211`, `:249`, `:708`) points at a table no ADR carries; ADR 0026 `:693-717` headings carry
  archived option labels ("(T32 Q1 B)"). T1 at ADR 0003 `:111` is archive/037's T1 (the 30 s
  recovery target); T1-T5 exist in both archives.
- **The registry test demands a ticket citation.**
  [`condition_registry_test.go:205`](../../internal/controller/condition_registry_test.go#L205)
  asserts `T\d+` on every `declaredGap`; the messages (`:128`, `:158`, `:206`) and the field doc
  ([`condition_registry.go:84-86`](../../internal/controller/condition_registry.go#L84-L86)) call
  it "the ticket reference"; [ADR 0027](../adr/0027-conditions-are-levels-edges-or-history.md) D4
  (`:198-205`, `:119`) decides it, so every future gap breaks D7. The only gap is the `Ready` row
  ([`condition_registry.go:102`](../../internal/controller/condition_registry.go#L102), "T18: ...
  (ADR 0001 D4 decides this; re-decision open)"); it suppresses no failing assertion.
- **Open-ticket citations** (T12, T18, T23, T34): 25 label lines and one path (ADR 0025 `:444`);
  some ADR homes cite the ticket themselves (ADR 0010 `:812`, ADR 0026 `:791`).
- **Measured-false statements about tickets** (archive/037 is tracked, ADR 0034's change is
  committed): [ADR 0003](../adr/0003-nudge-a-short-of-pods-statefulset.md) `:111-113`, `:228-229`
  ("not in this repository"); [ADR 0009](../adr/0009-an-unrecorded-promotion-is-not-a-promotion.md)
  `:43` ("The review is not in this repository"); ADR 0034 `:14`, `:37-38` ("not committed yet")
  and `:268-270` ("The owner reviews it before the change is committed"); ADR 0035 `:35`, ADR 0036
  `:20` ("not committed yet").

### Contradicted sentences

**(a) "No replicas attached yet" holds only for `replicas: 2`.** `promoteAndRedirect`
([rolling_update.go:4188-4249](../../internal/controller/rolling_update.go#L4188-L4249)) demotes
the old master and redirects every other replica to the promoted pod before the old master is
deleted, so with 3+ replicas the promoted pod has replicas attached (a syncing one counts toward
`connected_slaves`) and the init script's Phase 1
([statefulset.go:457-488](../../internal/builder/statefulset.go#L457-L488)) can find it. Two
comments say it never can: [statefulset.go:490-496](../../internal/builder/statefulset.go#L490-L496)
(Phase 2, in the script) and
[rolling_update.go:4039-4043](../../internal/controller/rolling_update.go#L4039-L4043). Phase 2
stays load-bearing (no redirected replica attached yet, or a failed best-effort redirect); only the
scope changes. The shell comment is in the init container's `Command`, so editing it moves the
pod-spec hash of every multi-replica data tier without Sentinel; that tier rolls on every release
anyway (the sidecar runs the operator image), and
[ADR 0005 D11](../adr/0005-upgrade-neutral-defaults-and-anti-affinity.md) lets it ride that roll
without a release note.

**(b) `WAIT` does not drain a master of writes.** `waitForWriteSync`
([rolling_update.go:2901-2977](../../internal/controller/rolling_update.go#L2901-L2977)) covers
only its own connection's writes; writes acknowledged between `WAIT` and the demotion are not on
the promoted pod (M1: 1 of about 1500 acknowledged keys lost per sequence at about 200 writes/s,
9.1.1 and 8.1.9). Four places claim otherwise:
[rolling_update.go:4216-4217](../../internal/controller/rolling_update.go#L4216-L4217) ("drained of
writes by waitForWriteSync"),
[ADR 0012 D9](../adr/0012-the-sidecar-records-its-drain-promotion-on-the-pod.md) `:340-341` ("It
loses no data"), the `waitForWriteSync` doc comment `:2901-2903` ("prevents data loss") and the
Sentinel-path call site `:2715-2716` ("prevents data loss from async replication").

**(c) The `sentinelPodNeedsUpdate` doc comment starts mid-sentence**
([rolling_update.go:4837](../../internal/controller/rolling_update.go#L4837)). The function
decides on four inputs (`:4838-4873`): image by container name, `pod-spec-hash` (resource
comparison when absent), `config-hash`, the TLS material fingerprint.

**(d) The sidecar Role is recorded wider than granted.** `BuildSidecarRole` grants `pods` `get`,
`patch` restricted by `resourceNames` to this cluster's data-pod names, and no rule for an empty
list ([rbac.go:58-121](../../internal/builder/rbac.go#L58-L121)).
[ADR 0013 D3](../adr/0013-operator-is-cluster-wide-privileged.md) `:167-168` says `pods:
get,list,patch` in one namespace (its strict-subset conclusion holds);
[ADR 0012 Consequences](../adr/0012-the-sidecar-records-its-drain-promotion-on-the-pod.md)
`:451-460` state in present tense that a sidecar can patch another cluster's pods and the observer
token carries namespace-wide `pods get,list,patch`, though ADR 0012's Status records D8 steps 2
and 3 as shipped.

**(e) ADR 0016 and the secrets page name the wrong `/proc` carrier of the password.**
[ADR 0016](../adr/0016-authentication-and-tls-posture.md) `:195-198` and
[secrets-and-tls.md:31-35](../security/secrets-and-tls.md) put it in `valkey-server`'s argv.
Measured (M2) on both pins: `valkey-server` keeps no copy (`/proc/1/cmdline` reads
`valkey-server *:6379`, `/proc/1/environ` holds none; `set-proc-title yes`, the default).
`valkey-cli` does while it runs: the probes
([statefulset.go:1515-1531](../../internal/builder/statefulset.go#L1515-L1531)) and the init
discovery calls (`cliAuthFlags`, `:265-268` on Sentinel topologies with Sentinel auth, `:430-433`
without Sentinel) pass `-a "$VALKEY_PASSWORD"`. `/etc/valkey-active`, the writable config copy on
topologies with an init container
([statefulset.go:645-663](../../internal/builder/statefulset.go#L645-L663), `:232-238`), holds
`requirepass` and `primaryauth` in cleartext after a `CONFIG REWRITE`, which Sentinel sends after
every `REPLICAOF` (the operator and sidecar never do); ADR 0016 `:199-200` names only
`sentinel.conf`. "Inside the pod" is also wrong: no generated pod shares its process namespace,
so `/proc` shows a process only to its own container. Every carrier sits in a container that
already has `VALKEY_PASSWORD`, so the correction names no new reader.

**(f) ADR 0026 Context `:164-170` puts the drain hook on every topology**
([ADR 0026](../adr/0026-a-pod-being-deleted-is-not-available.md)). It exists only when
`IsMultiReplicaWithoutSentinel()`
([statefulset.go:678-680, :746-749](../../internal/builder/statefulset.go#L746-L749)); elsewhere
termination is `valkey-server`'s own shutdown within the 75 s grace period.

**(g) The required checks live in a ruleset, not in branch protection.**
`gh api repos/guided-traffic/valkey-operator/branches/main/protection` answers 404. The ruleset
`main` (id 23985346, active, default branch; read with `gh api .../rules/branches/main`) has
`deletion`, `non_fast_forward` and `required_status_checks` with exactly ADR 0017 D47's twelve
contexts (bound to the `github-actions` app, not strict); bypass `OrganizationAdmin` and
`Integration` 5070048, both `always`; no review rule. Stale:
[ADR 0017](../adr/0017-test-and-ci-policy.md) D47 (`:539`), Consequences (`:1102-1104`), Residual
risks (`:1310-1317`, including `enforce_admins`, a classic-protection setting `main` lacks),
[DEVELOPER.md:298-302](../../DEVELOPER.md) ("could not be read back"),
[release.yml:482](../../.github/workflows/release.yml#L482), [CLAUDE.md:392-393](../../CLAUDE.md).

**(h) ADR 0017 D52 pins a stale version** (`:835`: v0.37.0); [go.mod](../../go.mod) has
`k8s.io/api` and `k8s.io/pod-security-admission` at v0.37.1. The rule holds; other v0.37.0
mentions record what something was read in and stay.

**(i) The isolation page misdescribes ADR 0020.**
[isolation-and-tenancy.md:192-193](../security/isolation-and-tenancy.md) says ADR 0020 leaves out
the name filter; [ADR 0020](../adr/0020-write-only-what-the-operator-owns.md) (`:18-25`,
`:171-175`, `:701-702`) says "the label set plus a name of the `<cr>-<ordinal>` form".

**(j) The `ExecMulti` doc comment says it returns the last error**
([client.go:330-332](../../internal/valkeyclient/client.go#L330-L332)). The loop (`:348-357`)
returns on the first failed write, read or `-` reply and sends nothing after it
(`TestExecMulti_StopsAtTheFirstRejectedCommand`,
[exec_test.go:252](../../internal/valkeyclient/exec_test.go#L252)); no `MULTI`/`EXEC`, so not
atomic. The only production caller
([checks.go:110-116](../../internal/observer/checks.go#L110-L116)) is unaffected.

**(k) The trust-boundaries diagram names a ClusterRole the chart never creates.**
[trust-boundaries.md](../security/trust-boundaries.md) `:24` says `valkey-operator-role`; the
chart uses `valkey-operator.fullname`
([clusterrole.yaml:4](../../deploy/helm/valkey-operator/templates/clusterrole.yaml#L4));
`valkey-operator-role` exists only in the generated [role.yaml:5](../../config/rbac/role.yaml#L5).

**Measurements.** M1: docker, `valkey/valkey:9.1.1` and `8.1.9`, `m`, `r1`, `r2` with `--save ''
--appendonly no`, r1 and r2 replicas of `m`; a writer in `m` logs every acknowledged `SET
load:$i`; after 3 s `WAIT 2 1000` (answers 2), then r1 `REPLICAOF NO ONE`, `m` and `r2` `REPLICAOF
r1 6379`; after 4 s `EXISTS` of the acknowledged keys on r1: `role:master`, `connected_slaves:2`,
1 key missing on both pins. M2: docker, both pins, `--user 999:999`, `valkey-server` via `sh -c
'exec valkey-server <conf> --requirepass "$VALKEY_PASSWORD" --masterauth "$VALKEY_PASSWORD"'`;
`/proc/1/cmdline`, `/proc/1/environ`, `/proc/*/cmdline` during a background `valkey-cli -a ...
BLPOP`, and the config after `CONFIG REWRITE`.

### Impact

A reader who follows a ticket citation lands in a work plan instead of a rule, or finds nothing;
new labels keep being written next to the old ones. (b) matters most: a contributor reading ADR
0012 D9 before touching `promoteAndRedirect` learns the roll is lossless where it loses writes
acknowledged between promote and demote. (a) invites removing or over-trusting Phase 2; (e) makes
moving the password off `valkey-server`'s command line (already off) look like a fix; (d)
overstates the RBAC footprint; (g) sends the next gate-job author to an endpoint with no effect;
(j) misleads a new `ExecMulti` caller; (c), (f), (h), (i), (k) carry no decision.

## Required changes

### Shared, in one pass

1. **Edit form.** A false ADR sentence is struck in place with a dated correction, and the ADR
   gains a dated "correction, no decision changes" line in `## Status` naming what was corrected
   and how it was verified; no ADR index State changes except ADR 0034's at the close. A citation
   rewrite keeps the prose and changes only the pointer, to the ADR and decision that carry it
   (`ADR 0011 D1`, or the full path). Archived tickets are not edited (ADR 0034 D2).
2. **One `CLAUDE.md` edit** (needs a session in which Hans asks for it): `:568` `` `Ready`/T18 ``
   -> `` `Ready` (ADR 0001 D4) `` (follows Q1 and T18); `:620` "a pre-existing gap T32 does not
   close" -> "a pre-existing gap, an ADR 0026 residual risk"; `:392-393` "adding it to branch
   protection" -> "adding it to the required status checks of the repository ruleset `main`"; and
   `:284`, `:785`, `:794`, `:1014`, `:1047` per Q2.
3. **Each ADR touched by both parts is edited once:** ADR 0012 (`:292`; D9 `:340-341`;
   Consequences `:451-460`), ADR 0017 (the T34 lines; D47, Consequences, Residual risks, D52),
   ADR 0026 (`:693-717`; `:787-791` with "not fixed by T32" at `:791`; Context `:164-170`).

### Ticket citations - independent of the open questions

4. Correct the measured-false statements, naming no ticket path: ADR 0003 `:111-113`, `:228-229`
   (drop "not in this repository", rewrite `T1`, `WP1` and the ticket mentions; the in-repo trace
   is `admissionRecoveryDeadline` in `test/e2e/admission_recovery_test.go`); ADR 0009 `:43` (the
   three defects and their fix commits `30588bd`, `744b589`, which the ADR names); ADR 0034 `:14`,
   `:37-38`, `:268-270` (committed; whether the owner reviewed the text first is not recorded),
   ADR 0035 `:35`, ADR 0036 `:20`. In the ADR 0034 edit keep the counts at `:31-36` as a dated
   measurement and discharge "Not verified: the citation counts on the committed tree"
   (`:271-273`) with 190 T-label, 41 `NA` and 8 path lines.
5. Before the sweep, add to this ticket a mapping table for every sub-label above, and settle
   T24(a), (b), (d).
6. Open-ticket citations are part of the sweep; whichever of this ticket and the owning ticket
   lands first rewrites the line, the other verifies it: T12 -> residual risks of ADR 0025
   (`:441-444`) and ADR 0028 (`:123`, `:230`); T18 -> ADR 0001 D4; T23 -> ADR 0010 `:808-813`; the
   `verifyNewMasterReady` gap -> ADR 0026 `:787-791`; the T34 lines of ADR 0017 are tags (Q2).
7. [`docs/tickets/README.md`](README.md#naming-and-numbering) `:29-30`: archive/037 defines its
   own T1-T5 (test scenarios) and WP1-WP6.

### Ticket citations - depends on the answers

8. (Q1) The registry test regexp, its three messages, the field doc, the `Ready` row prefix, ADR
   0027 D4 and `:119`; decide before T18 rewrites `condition_registry.go:102`.
9. (Q2, Q3) The sweep: the 151 labelled ADR lines, the 8 path citations, the Go T-label and 23
   sub-label lines, the 2 WP lines, `rootless-migration.md` `:27`, `:47`, `:85`, the `CLAUDE.md`
   lines of change 2, and under Q3 A the content pointers. Noun and handle uses are rewritten
   under every answer. Coordinate ADR 0020 with T43 and T60.
10. (Q4) The guard, if chosen, lands with the close.

### Contradicted sentences - independent of the open questions

11. (a) Scope `rolling_update.go:4039-4043` and the shell comment at `statefulset.go:490-496`:
    discovery rejects the promoted pod when no replica is attached - always with `replicas: 2`, on
    larger clusters until a redirected replica attaches or when a redirect failed. No release note.
12. (b) Rewrite `:4216-4217`: the demotion is best-effort, writes acknowledged after `WAIT` are
    lost. Scope `:2901-2903` and `:2715-2716` to "writes acknowledged before the call" (drop
    `:2715-2716` if T12 rewrites it first). Strike ADR 0012 D9's "It loses no data" and state the
    measured gap with the M1 method.
13. (c) A complete `sentinelPodNeedsUpdate` doc comment naming its four inputs.
14. (d) ADR 0013 D3: `pods: get, patch` restricted by `resourceNames` to this cluster's data-pod
    names, subset conclusion kept (T29 edits the same paragraph; the second to land rebases). Mark
    both ADR 0012 Consequences bullets superseded by D8 steps 2 and 3, pointing at
    [privilege-footprint.md](../security/privilege-footprint.md#the-per-instance-sidecar-role).
15. (e) ADR 0016 and secrets-and-tls.md: the password is in each probe's and init step's
    `valkey-cli` argv while it runs, visible in that container's `/proc` only, not in
    `valkey-server`'s argv; add `/etc/valkey-active` (cleartext after `CONFIG REWRITE`, on Sentinel
    topologies after every Sentinel `REPLICAOF`) next to `sentinel.conf`; keep the conclusion.
16. (f) ADR 0026 Context: scope the one-second release and the 60 s cap to multi-replica clusters
    without Sentinel.
17. (g) ADR 0017 D47, Consequences, Residual risks, DEVELOPER.md, release.yml:482: name the ruleset
    `main` (id 23985346), its `rules/branches/main` read-back matching D47, and the bypass list in
    place of `enforce_admins`; the earlier API-call sentence stays as dated history in the ADR.
18. (h) ADR 0017 D52: "sits at the same version as `k8s.io/api`; the `k8s-go-modules` group moves
    both", no number.
19. (i) isolation-and-tenancy.md:192-193: say ADR 0020 reads the same way, or drop the sentence.
20. (j) `ExecMulti` doc comment: sequential on one connection, the first failure ends the sequence
    and is returned, later commands are not sent, no `MULTI`/`EXEC`, not atomic. No rename.
21. (k) trust-boundaries.md `:24`: `<release>` instead of `valkey-operator-role`, same box width.

### Close and verification

- ADR 0034 Status: D7 implemented; its State in `docs/adr/README.md` (`:112`) and `:26-28`.
  Update the "partly implemented" statements: `CLAUDE.md`, `docs/tickets/README.md` `:93-100`,
  `DEVELOPER.md:449`, `docs/security/README.md:59-61`, ADR 0036 D5 `:116-117`, ADR 0034 D7
  `:162-166` and `:262-263` (per Q4), ADR 0005 `:100-101` (per Q3).
- Citation greps outside `docs/tickets/` keep nothing: `git grep -nwE 'T[0-9]+|NA[0-9]+'`,
  `git grep -nE 'docs/tickets|\.\./tickets/'` (rule lines naming the directory without citing a
  ticket stay), `git grep -nwE 'T40|040|C3'`. Read-through:
  `git grep -nwE '(E[1-6]|S[1-8]|F1|Q2|WP[0-9]+)'` returns only ADR 0032's option labels
  (`:431-432`); `git grep -niE 'admission-gap ticket|the ticket|ticket.s list'` returns only rule
  text and, under Q3 A, narrative mentions.
- Correction greps outside `docs/tickets/`: `no replicas attached yet` (outside tests) only
  scoped; `drained of writes\|drained the pod of writes\|It loses no data`, `last non-OK error`
  and `valkey-operator-role -- docs/` nothing; `-i 'branch protection'` only dated history. ADR
  0013 `get,list,patch` and the `valkey-server` argv claim only as struck text; ADRs 0012, 0013,
  0016, 0017, 0026 carry a Status line.
- `git diff -U0 -- internal/ .github/` touches only comment lines (the `#` lines only in the Phase
  2 comment), except the regexp of change 8. No mutation check applies to comment edits.
- `make fmt && make vet && make lint`, `make test-unit`, `make test-integration`,
  `make generate-all` with a clean tree, `make test-e2e E2E_RUN='TestE2E_CompileCheckOnly_NoSuchTest'`
  (lint skips build-tagged files; the last two prove comment edits under `test/`).
- Move this file to `archive/`.

## Open questions

All four belong to the ticket citations. Answer Q1 first (T18 waits on it), then Q2, Q3, Q4.

### Q1: What does a `declaredGap` in the condition registry have to name? (ticket citations)

The registry test demands `T\d+`, which ADR 0034 D7 forbids for every new gap; a ticket that
closes with its gap kept would also have to remove a label the test demands (ADR 0034 D3).

- **A. The ADR that owns the exception** (recommended): the test asserts `ADR \d{4}` and checks
  that a `docs/adr/NNNN-*.md` file exists; the `Ready` row drops `T18: ` (it already names ADR
  0001 D4); an open defect gap is first recorded under the owning ADR's Residual risks. Cost XS,
  unit tier only.
- **B. Keep `T\d+` and carve `declaredGap` out of D7**: no code change, but the first exception to
  D7, a conflict with D3 for every gap that outlives its ticket, and a special case in a Q4 guard.

A keeps ADR 0027 D4's purpose (traceable to a decision) without any exception to D7.

**Answer:** _open_

### Q2: Is a ticket label used as a provenance tag rewritten, or kept as a listed exception? (ticket citations)

About 50-56 ADR lines, some Go comments and the `CLAUDE.md` and `rootless-migration.md` lines use
a label only as a tag; ADR 0034 `:228-232` leaves this open for `Amended` headers. Noun and
handle uses are rewritten either way.

- **A. Rewrite the tag to the decision it records** (recommended): `Amended 2026-08-22 (NA61)` ->
  `Amended 2026-08-22 (D1, StatefulSets)`, the form ADR 0020 `:33`, ADR 0017 `:710` and ADR 0012
  `:37` already use; an evidence tag names the ADR with the evidence (`rootless-migration.md:27`,
  `:85` -> ADR 0032). The greps end at zero; the direct pointer to the archived analysis is lost,
  `git log -S` recovers the commit.
- **B. Keep tags as listed exceptions** and amend D7: about a fifth fewer lines, but a permanent
  exception list whose line numbers move with every ADR edit, and about 50 pointers into archived
  plans.

A leaves a zero-hit grep for the close and for a guard, and keeps the information by naming the
decision.

**Answer:** _open_

### Q3: Is a mention of a ticket without an identifier a citation? (ticket citations)

D7 lists number, T-label, file name and path. Fourteen lines mention "the ticket" with none of
them; nine send the reader there for content, the rest are narrative. ADR 0005 `:100-101` says
these mentions stay.

- **A. A content pointer is a citation and is rewritten; a narrative mention is not**
  (recommended): each pointer states the content or its in-repo trace; ADR 0005 `:100-101` is
  amended and D7 gains one sentence stating the reading. About ten lines in ADRs the sweep edits
  anyway.
- **B. Only the four listed forms are citations**: ADR 0005 `:100-101` stands, D7 gains one
  sentence. Cost XS; ADR text keeps deferring its evidence to a ticket a reader cannot find.

D7's reason (a reader lands in a plan instead of a rule) applies even more when the plan cannot be
found.

**Answer:** _open_

### Q4: Is ADR 0034 D7 enforced mechanically once the sweep is done? (ticket citations)

Nothing checks it today, and labels were written again within hours of an earlier sweep. `make
lint` runs in `Code Linting`, a required context on every push and pull request (ADR 0017 D47). No
option covers commit messages, pull request bodies, sub-labels or unlabelled mentions.

- **A. Stay manual**: no cost; a regression surfaces only at the next audit.
- **B. A `git grep` target called by `make lint`** (recommended), for example
  `make check-ticket-citations`, failing on `T[0-9]+|NA[0-9]+` (word match) and on
  `tickets/(archive/)?(local_)?[0-9]{3}-` outside `docs/tickets/`, excluding `go.sum` and
  `package-lock.json`. Cost XS; a docs-only push to `main` turns `main` red instead of being
  blocked; only after the sweep and with Q2 = A (or Q2 B's allow-list).
- **C. A diff-scoped guard now** (added lines, `base...HEAD`): protects during the sweep, cost S,
  needs event-specific base handling in CI; B replaces it at the close.

B is cheap, rides an already required context and turns the residual risk into a checked rule.

**Answer:** _open_

## Not verified

- Which ADR 0030 decisions T24(a), (b), (d) map to; reading archive/039's T24 against ADR 0030
  settles it (change 5).
- The hand classification of every label line into tag, handle and noun; settled in the sweep.
- The (b) loss and the Phase 1 hit rate on Kubernetes under real write load; an e2e run settles it.
- Whether an exec probe's command line is visible outside the container (kubelet log, runtime
  events); reading or measuring on a node settles it.
- Which app actor 5070048 is, and whether a non-org-admin repository admin can bypass the
  ruleset; the GitHub ruleset settings settle it.

## Related

- T12, T23, T34: their citations are part of the sweep (change 6).
- T18: its citations are part of the sweep; it meets Q1 on `condition_registry.go:102`.
- T43: its close grep for `S1` would hit `pod_termination_test.go:408`, an archive/039 label; it
  also edits ADR 0020.
- T43: `make lint` skips build-tagged files.
- T60: edits ADR 0020 near `:477-479` and `:570-571`; coordinate with the ADR 0020 rewrite.
- T12: owns the Sentinel-path loss after `WAIT` and its "lossless" statements.
- T29: edits the same ADR 0013 D3 paragraph as (d).
