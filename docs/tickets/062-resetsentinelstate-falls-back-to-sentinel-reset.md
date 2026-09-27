---
id: T62
title: resetSentinelState falls back to SENTINEL RESET, which ADR 0022 D6 and CLAUDE.md say the operator never issues
state: filed
severity: medium      # estimate, not measured: the verified half (the RESET fallback) alone is low - it needs REMOVE to fail and the next dial's RESET to succeed; medium if the open question holds, because then the stall and timeout paths can empty every Sentinel's tables when a failover is most likely needed, and no condition reports it (Impact)
security: none
urgency: now          # rule 1: CLAUDE.md:743 and ADR 0022 D6 (:110) state an unconditional rule the code breaks (rolling_update.go:3668), and a test comment repeats a claim ADR 0022 measured false (sentinel_failover_test.go:1467-1468)
effort: S             # the recommended option B, which includes A; A alone is XS, C is XS, D is M-L
blocked-by: decision  # which option, below; CLAUDE.md edits also need Hans
filed-from: ticket enrichment of 2026-09-27 (review of the security-hardening family)
opened: 2026-09-27
decided:              # not recorded - no decision yet
done:                 # not recorded - not done
---

# T62 - resetSentinelState falls back to SENTINEL RESET, which ADR 0022 D6 and CLAUDE.md say the operator never issues

Filed on 2026-09-27. The orchestrator of the ticket enrichment found it by reading at
`4a7543e`; this file re-read every location in the working tree of
`chore/maintenance-2026-09-27` on the same day. The only change to `rolling_update.go` in that
working tree above the cited lines is a comment that grew by two lines at `:2640`, so the
working-tree lines below are the `4a7543e` lines plus two from `:2643` on (the `4a7543e` line is
given where it differs). No make target, container or cluster was run for this file.

## Fact

**Mechanism.** `resetSentinelState`
([`rolling_update.go:3616`](../../internal/controller/rolling_update.go#L3616), `:3614` at
`4a7543e`) walks every Sentinel ordinal and, per Sentinel, sends `SENTINEL REMOVE <name>`, then
`SENTINEL MONITOR <name> <masterAddr> <port> <quorum>`, then five `SENTINEL SET`s and the
`auth-pass`. When the REMOVE returns an error, it sends `SENTINEL RESET <name>` instead and moves
on to the next Sentinel without a MONITOR
([`:3665-3671`](../../internal/controller/rolling_update.go#L3665-L3671), `:3663-3669` at
`4a7543e`). An empty `masterAddr` falls back to pod-0 (`:3626-3630`). The function is best-effort:
it returns nothing, and every caller carries on whatever happened.

**Verified (read):**

- **Five callers, three of them on paths where the master may be unreachable or unverified:**
  - `:957`, `checkFinalizationTopology`: the finalization is stalled
    (`finalizationStallTimeout` = 2 min, `:126`) and the pass counts zero or several masters; the
    reset runs with `""`, so every Sentinel is pointed at pod-0.
  - `:1006`, `syncSentinelWithMaster`: the finalization is stalled and `GetReplicationInfo` on
    the pod identified as master has just failed; the reset runs anyway with that pod's address.
  - `:1048`, `syncSentinelWithMaster`: the master answered and its replicas are connected (or
    the stall let a partial count through); the address is that master's.
  - `:3160` (`:3158` at `4a7543e`), `handleMasterWithNoReplicas`: a new-image master answered
    `role:master` with no connected replica past `replicaReconnectTimeout` (90 s, `:149`).
  - `:3307` (`:3305` at `4a7543e`), `handleNoMasterFound`: the failover timed out and no
    new-image master was found; the address is the pod the pass's scan marked as master, else
    pod-0.
- **The completion hold does not protect the stalled calls.** `finalizeRollingUpdate` holds the
  completion while a data pod terminates so that Sentinel is never pointed at a dying master
  (ADR 0026 D4), but only while the finalization is not stalled
  ([`:911`](../../internal/controller/rolling_update.go#L911),
  `!r.isFinalizationStalled(v)`). Past the 2 min stall the calls at `:957` and `:1006` run.
- **The rule the code breaks.** [ADR 0022](../adr/0022-sentinel-identity-is-pinned-to-the-pod.md)
  D6 (`:110`): "The operator never issues `SENTINEL RESET` on its own." [`CLAUDE.md:743`](../../CLAUDE.md):
  "The operator never issues `SENTINEL RESET` itself." Neither is restricted. Only the ADR's
  Status (`:17-19`) narrows it: "the operator never issues `SENTINEL RESET` to clean a peer
  table".
- **Why the rule exists.** ADR 0022 Context (`:66-73`) measured on both pinned images that a
  `SENTINEL RESET` issued while the master is unreachable leaves the Sentinel at
  `num-other-sentinels=0` and `num-slaves=0` "with no way back: peer and replica discovery both
  run through the master". ADR 0026 D4 (`:248-260`) reads the timed-out branch of the Sentinel
  roll - `handleNoMasterFound`, which calls `resetSentinelState` at `:3307` - as one that
  "resets Sentinel through a dying master (the unrecoverable direction per ADR 0022)". So an ADR
  already treats the REMOVE + MONITOR path as the hazardous shape, by reading.
- **Each command dials anew.** `Client.exec` opens a connection per command and closes it
  ([`client.go:429-452`](../../internal/valkeyclient/client.go#L429-L452)), so a REMOVE that
  fails at dial or write can be followed by a RESET that succeeds on the next dial.
- **A unit test pins the fallback, under a false comment.**
  `TestResetSentinelState_FallsBackToResetWhenRemoveFails`
  ([`sentinel_failover_test.go:1470`](../../internal/controller/sentinel_failover_test.go#L1470))
  asserts `SENTINEL REMOVE` then `SENTINEL RESET` and no MONITOR. Its comment (`:1467-1468`)
  says "a plain RESET would revert sentinel to the pod-0 address from its config file" - the
  claim ADR 0022 Context measured false (RESET keeps the current master address, also after a
  failover). The function's own doc comment said the same until 2026-09-27 and was corrected
  that day (text only); the test comment was not.
- **The corrected doc comment discharges an ADR residual risk.** ADR 0022 Residual risks
  (`:204-205`) says the wrong comment above `resetSentinelState` "was left in place by this
  change and is corrected in the change that next touches that function", and Context
  (`:68-70`) says the comment "claims the opposite and is wrong". Both describe a comment that
  no longer exists. Work list item 1.
- **An emptied table is not reported.** `SentinelPeersStale` is True only for a Sentinel that
  knows *more* peers than expected (`staleSentinelPods`,
  [`valkey_controller.go:2415-2424`](../../internal/controller/valkey_controller.go#L2415-L2424),
  `known > SentinelPeersExpected`). A Sentinel that knows none raises nothing.
- **Other comments call the REMOVE + MONITOR a "SENTINEL RESET".** `failoverResetMinWait`
  (`:151`) and `hasMinWaitElapsed` (`:3544`) speak of "a SENTINEL RESET" where the code sends
  REMOVE + MONITOR; `:130`, `:978`, `:3274` and `:3557` name it correctly.

**Not verified:**

- **Open question: whether REMOVE + MONITOR with the master unreachable leaves the Sentinel in
  the same empty-table state.** A re-added monitor starts with no known replicas and no known
  peers, and both are discovered through the master (`INFO` for replicas, the master's hello
  channel for peers - ADR 0022 Context, and the comment at `rolling_update.go:975-979` relies on
  the same for replicas). Read that way, every call on `:957`, `:1006` and `:3307` that names an
  unreachable or wrong master empties every Sentinel it reaches, not one. What happens once the
  named address answers again - as a master, or as a replica of a pod promoted meanwhile - was
  not measured. If this holds, the hazard is not limited to the RESET fallback.
- **When the RESET fallback actually fires.** Upstream Sentinel answers `SENTINEL REMOVE` for an
  unknown name with "No such master with that name", the error the unit test uses; `SENTINEL
  RESET <pattern>` then matches no master and resets nothing. So the fallback changes a table
  only when REMOVE fails for another reason (a failed dial, TLS handshake or write) and the RESET
  on the next dial succeeds. From memory of upstream `sentinel.c`, not re-read for this file.
- Whether any Valkey 8 or 9 command re-points a monitor or clears the failover cooldown without
  dropping the tables (option D). Not researched.
- Nothing was run: no unit test, no docker measurement, no Kind run.

## Impact

Sentinel-enabled clusters only, during a data-tier rolling update, on three paths: a failover
that times out (`:3307`), a finalization that stalls for 2 min (`:957`, `:1006`), and a new
master with no replica after 90 s (`:3160`). Those are the paths on which the master is most
likely to be unreachable, terminating or ambiguous.

- **Verified half:** a Sentinel whose REMOVE fails and whose RESET succeeds while the master is
  unreachable ends with empty peer and replica tables, by ADR 0022's measurement of RESET. It
  cannot then lead or vote a failover it would have been needed for.
- **If the open question holds:** every Sentinel the loop reaches ends that way, on every call
  that names an unreachable master; the tier cannot fail over until the named address answers as
  a master again or the Sentinel pods are replaced (the init container rewrites the config on
  the pod's `emptyDir`; a container restart keeps Sentinel's rewritten config - inference, not
  measured).
- **Invisible either way:** no condition reports a Sentinel that knows too few peers, and
  `Ready`, `phase` and the alerts read other signals.
- **Documentation:** a contributor who reads ADR 0022 D6 or `CLAUDE.md` concludes that no code
  path issues `SENTINEL RESET`; one does, and a unit test pins it.

## Options

One decision: what `resetSentinelState` may do when the master it names cannot be trusted.

- **A - drop the RESET fallback.** A failed REMOVE is logged and that Sentinel is skipped.
  Cost XS: delete `:3667-3670`; rewrite `TestResetSentinelState_FallsBackToResetWhenRemoveFails`
  to assert REMOVE and nothing else, with a true comment, and a revert check (the fallback back
  in turns it red, ADR 0017); reword the two "SENTINEL RESET" comments at `:151` and `:3544`;
  `valkeyclient.SentinelReset` loses its only production caller (keep or delete it). Makes ADR
  0022 D6 and `CLAUDE.md:743` true as written without touching either. Leaves the open question
  entirely: REMOVE + MONITOR still runs through an unreachable master on `:957`, `:1006` and
  `:3307`.
- **B - A, plus a gate: no Sentinel is touched unless the named master is verified (recommended).**
  Before the loop, resolve the pod `masterAddr` names (pod-0 for `""`), and require it to be
  available - Ready and not being deleted, [ADR 0026](../adr/0026-a-pod-being-deleted-is-not-available.md)
  D1 - and to answer `role:master` to `INFO replication`; otherwise log and return without a
  command. Per caller: `:1048` and `:3160` have just verified that master and pass (one extra
  `INFO`); `:957`, `:1006` and `:3307` reset only if the named pod verifies now, and skip
  otherwise. No caller waits on the result - `:957` and `:1006` return `nil` (proceed), `:3160`
  and `:3307` go on to their own state changes - so the gate adds no wait and no state, and the
  bounds of [ADR 0010](../adr/0010-every-rolling-update-wait-is-bounded.md) D5 are untouched.
  Cost S: the gate (about fifteen lines, under the cyclomatic limit), unit tests for a named pod
  that is terminating, not Ready, answers `role:slave` or does not answer (no command sent) and
  one that verifies (commands sent), each with a revert check; ADR 0022 D6 amended (the operator's
  own reset is REMOVE + MONITOR, gated on a verified master, and never RESET), with a line in the
  index if its State moves; the full e2e suite on both legs, because the finalization-stall
  branches exist for flaky `GetReplicationInfo` calls in CI (comment at `:952-954`) and the
  failover-retry path runs in every Sentinel roll test. What it costs in behaviour: at `:3307` a
  skipped reset leaves Sentinel's failover cooldown in place, so the retrigger after
  `failoverResetMinWait` may be refused again - the reset-and-retrigger cycle has no cap already
  (ADR 0010 Residual risks, `:775`), and B does not add one. What it leaves: a master that
  verifies and dies before the MONITOR (one round trip).
- **C - narrow ADR 0022 D6 and `CLAUDE.md` to what the code does, and accept it.** D6 would say
  the operator never issues `SENTINEL RESET` to clean a peer table, and that `resetSentinelState`
  sends REMOVE + MONITOR on the rolling update's failover-retry and finalization paths, with RESET
  as a fallback. Cost XS in text; `CLAUDE.md` needs Hans. It records as accepted a hazard ADR
  0022 measured as unrecoverable and ADR 0026 D4 already names, without a measurement that the
  paths are rare or harmless.
- **D - replace REMOVE + MONITOR with commands that keep the tables** (`SENTINEL SET`, or a
  failover-specific command). The function exists to do three things: point every Sentinel at a
  given address, clear a failover cooldown, and re-apply parameters. `SENTINEL SET` covers the
  third; whether anything covers the first two without dropping the tables is not known (Not
  verified). Cost M-L: upstream source of both pinned tags read, a docker measurement like ADR
  0022's, then the code. Re-pointing at an unreachable address would still need B's gate.

**B is marked.** The condition it adds is the one ADR 0022 D6 itself sets for the manual reset
("one Sentinel at a time with the master verified healthy"), so the code would follow the rule
its own ADR writes for people. It closes the verified half (A is part of it) and the open
question's hazard without waiting for the measurement: a reset through an unreachable master
cannot rebuild anything, because the rebuild runs through that master, so skipping it loses
nothing the reset could have gained - and ADR 0026 D4 already names that direction
unrecoverable. It adds no wait, because every caller already ignores the outcome. A alone is the
fallback if the full e2e run shows that a stalled finalization needs the unverified reset to
complete in CI; then the open question must be measured before accepting what remains. C loses
because it accepts, unmeasured, what an ADR measured as unrecoverable. D loses because its
feasibility is unknown and it would still need B's gate.

## Decision

None yet.

## Work list

1. **XS, no decision needed (docs):** ADR 0022 describes a comment that no longer exists. In
   Context (`:68-70`) strike "the comment in `rolling_update.go` above `resetSentinelState` claims
   the opposite and is wrong" with a dated correction (the comment was corrected on 2026-09-27
   and now states the measured behaviour); in Residual risks (`:204-205`) mark the entry "The
   wrong comment above `resetSentinelState` was left in place …" as discharged on 2026-09-27; add
   a dated Status line ("Corrected 2026-09-27, no decision changes"). No ticket citation in the
   ADR (ADR 0034). Does not close this ticket. Not done in the filing pass. *(Done 2026-09-27,
   see History.)*
2. **XS, no decision needed (test comment):** correct the comment of
   `TestResetSentinelState_FallsBackToResetWhenRemoveFails`
   ([`sentinel_failover_test.go:1467-1468`](../../internal/controller/sentinel_failover_test.go#L1467-L1468)):
   REMOVE + MONITOR sets the address the caller names, and a plain RESET keeps the address each
   Sentinel holds (ADR 0022 Context). True under every option; under A or B the test itself is
   rewritten in the same change.
3. **Measurement (informs the ADR text, not the choice):** in docker against both pinned images
   (`test/testimages`), ADR 0022's topology - one master, two replicas, three Sentinels, quorum 2.
   Stop the master; send `SENTINEL REMOVE` and `SENTINEL MONITOR` naming its address to one
   Sentinel; read `num-other-sentinels` and `num-slaves` from `SENTINEL MASTER`. Then bring the
   address back (a) as a master and (b) as a replica of a promoted replica, and read them again.
   Settles the open question under Not verified.
4. **Waits on the decision:** the option's code, tests and ADR 0022 amendment as listed under
   Options; `CLAUDE.md:743` changes only under C and needs Hans.
5. Close ([ADR 0034](../adr/0034-tickets-are-work-lists-that-get-archived.md)): the rule into ADR
   0022 D6 (amended in place), the contributor-facing sentence in `CLAUDE.md` only if it changes;
   `git grep` `T62` and `062-` outside `docs/tickets/`, then move to `archive/`.

## Verification

- Item 1: `git grep -n "claims the opposite and is wrong\|was left in place by this change" -- docs/adr`
  finds only struck text, and ADR 0022 Status carries the dated line.
- Item 2: `grep -n "revert sentinel to the pod-0 address" internal/controller/*_test.go` is empty;
  `make lint` is green.
- A: `git grep -n "SentinelReset(" -- internal cmd` finds no production caller; the rewritten
  test fails with the fallback restored; `make test-unit`, `make lint`.
- B: the unit tests above, each failing with the gate removed; `make test-unit`, `make lint`,
  `make cyclo`; the full e2e suite green on both legs (`single-node-valkey9`,
  `single-node-valkey8`); ADR 0022 D6 states the gated rule, and `git grep -n "SENTINEL RESET"`
  outside `docs/tickets/` finds no statement that contradicts it.

## History

- 2026-09-27: work list item 1 landed in the text-vs-code review of the maintenance branch: ADR
  0022 Context and Residual risks strike the two sentences about the wrong comment in place, and
  the Status carries a dated "Corrected 2026-09-27 (no decision changes)" line. Item 1 does not
  close this ticket. **Verified:** by reading the corrected comment above `resetSentinelState`.
  **Not verified:** nothing was run.
- 2026-09-27: filed from the ticket enrichment of that day (the review of the security-hardening
  family). The orchestrator found the fallback by reading at `4a7543e`; this file re-read the
  five callers, the fallback, `Client.exec`, the unit test, `staleSentinelPods`, ADR 0022 Status,
  Context, D6 and Residual risks, ADR 0026 D4 and the ADR 0010 residual risk in the working tree,
  and added: the stall gate on the completion hold (`:911`), that ADR 0026 D4 already reads the
  REMOVE + MONITOR path as the unrecoverable direction, the per-command dial, the false test
  comment, that an emptied table is not reported, and the two comments that call REMOVE +
  MONITOR a RESET. Severity `medium` is an estimate (frontmatter). Urgency `now` by rule 1:
  `CLAUDE.md:743` and ADR 0022 D6 state an unconditional rule that `rolling_update.go:3668`
  breaks, and the test comment repeats a claim ADR 0022 measured false. **Verified:** by reading.
  **Not verified:** nothing was run; the REMOVE + MONITOR hazard and the upstream REMOVE error
  are open (Not verified).
