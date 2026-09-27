---
id: T62
title: resetSentinelState falls back to SENTINEL RESET, which ADR 0022 D6 and CLAUDE.md say the operator never issues
state: analysed       # both decisions carry a recommended option
severity: medium      # measured: an unverified reset of every Sentinel leaves the tier unable to fail over; the triggers are narrow
security: none
urgency: now          # rule 1: the test comment and the cooldown comments are measured false, and ADR 0022 D6 and CLAUDE.md state a rule the code breaks
effort: S             # gate, two call deletions, agreement check, fallback fix, unit tests with revert checks, full e2e on both legs
blocked-by: decision  # Q1 and Q2
filed-from: ticket enrichment of 2026-09-27 (review of the security-hardening family)
opened: 2026-09-27
decided:
done:
---

# T62 - resetSentinelState falls back to SENTINEL RESET, which ADR 0022 D6 and CLAUDE.md say the operator never issues

The hazard is wider than the title: it is the unverified REMOVE + MONITOR on every Sentinel,
of which the RESET fallback is one branch.

## Current state

**Mechanism.** `resetSentinelState`
([`rolling_update.go:3617`](../../internal/controller/rolling_update.go#L3617)) walks every
Sentinel and sends `SENTINEL REMOVE <name>`, `SENTINEL MONITOR <name> <masterAddr> <port> <quorum>`,
five `SENTINEL SET`s and the `auth-pass`
([`:3666-3693`](../../internal/controller/rolling_update.go#L3666-L3693)). When REMOVE errors it
sends `SENTINEL RESET <name>` (`:3669`) and moves on without a MONITOR. An empty `masterAddr`
falls back to pod-0 ([`:3628-3632`](../../internal/controller/rolling_update.go#L3628-L3632),
[`configmap.go:33-36`](../../internal/builder/configmap.go#L33-L36)). The function never checks
the pod it names, returns nothing, and every caller carries on.

**Five callers:**

| Site | Function | When it runs | Address |
|---|---|---|---|
| [`:958`](../../internal/controller/rolling_update.go#L958) | `checkFinalizationTopology` | finalization stalled (2 min) and `masterCount != 1` | `""`, so pod-0: one of several masters, or a replica |
| [`:1007`](../../internal/controller/rolling_update.go#L1007) | `syncSentinelWithMaster` | finalization stalled and `GetReplicationInfo` on the master just failed | that pod |
| [`:1049`](../../internal/controller/rolling_update.go#L1049) | `syncSentinelWithMaster` | **final pass of every Sentinel data roll**; stalled, a partial replica count passes (`:1016-1027`) | the master, role not re-checked |
| [`:3161`](../../internal/controller/rolling_update.go#L3161) | `handleMasterWithNoReplicas` | an `available()` new-image master with no replica after 90 s; `forceReplicaConnections` runs first (`:3159`) | that master |
| [`:3308`](../../internal/controller/rolling_update.go#L3308) | `handleNoMasterFound` | failover timed out (30 s), no available new-image master | first pod the scan marks master (INFO `role:master`, a terminating pod included, or its label), else pod-0 |

- **The completion hold does not cover the stalled calls.** `finalizeRollingUpdate` keeps
  Sentinel off a terminating master (ADR 0026 D4) only while not stalled
  ([`:912`](../../internal/controller/rolling_update.go#L912)). Past the stall, `:958`, `:1007`
  and `:1049` run, and `:1049` can name a terminating master that still answers INFO. ADR 0026
  D4 names a reset through a dying master as the unrecoverable direction; it is reachable at
  `:3308`.
- **The rule the code breaks.** [ADR 0022](../adr/0022-sentinel-identity-is-pinned-to-the-pod.md)
  D6 (`:116`) and [`CLAUDE.md:743`](../../CLAUDE.md) say the operator never issues
  `SENTINEL RESET`, unrestricted. ADR 0022 Context (`:71-79`) measured why: a RESET while the
  master is unreachable empties the peer and replica tables.
- **Each command dials anew** ([`client.go:429-453`](../../internal/valkeyclient/client.go#L429-L453));
  a `-` reply is a plain error (`:480-482`). A REMOVE that fails at dial can be followed by a
  RESET that succeeds; a REMOVE whose reply is lost after it executed counts as failed.
- **A unit test pins the fallback under a false comment.**
  `TestResetSentinelState_FallsBackToResetWhenRemoveFails`
  ([`sentinel_failover_test.go:1470-1486`](../../internal/controller/sentinel_failover_test.go#L1470-L1486))
  asserts REMOVE, RESET, no MONITOR, with "No such master" injected - the case in which RESET
  matches nothing. Its comment (`:1467-1469`) says a plain RESET reverts to the pod-0 address;
  ADR 0022 measured that RESET keeps the current address.
- **Two SETs always fail.** `resolve-hostnames` and `announce-hostnames` (`:3685-3686`) are
  global options, rejected per master, already in the config
  ([`sentinel.go:162-163`](../../internal/builder/sentinel.go#L162-L163)); errors discarded.
  Pinned by `sentinel_failover_test.go:1458-1459`.
- **False comments.** A Sentinel cooldown that refuses the operator's retrigger is claimed at
  `:140-142`, `:3068-3070`, `:3527-3529`, `:3614-3615` and in `valkeyclient.SentinelReset`
  ([`client.go:249-251`](../../internal/valkeyclient/client.go#L249-L251)). Upstream refuses a
  forced `SENTINEL FAILOVER` only with `-INPROG` or `-NOGOODSLAVE`; the 2 x failover-timeout delay
  gates only the automatic failover. `:151`, `:3545` and `rolling_update_bounds_test.go:760` call
  REMOVE + MONITOR "a SENTINEL RESET". The comment at `:3665` is accurate.
- **No command re-points a monitor and keeps its tables** (valkey 9.1.1 and 8.1.9 `sentinel.c`).
  A running Sentinel persists a switched address itself; a restarted pod gets it from the
  known-master annotation (`persistKnownMaster`, `:1045`,
  [ADR 0008](../adr/0008-known-master-annotation-is-the-recorded-authority.md)). The finalization
  reset exists for a Sentinel monitoring a stale address.
- **Rebuild window.** After REMOVE + MONITOR a Sentinel learns replicas from the master's INFO
  and peers from hellos: about 2 s with every replica connected. At `:3161` (no connected
  replica by definition) and on a stalled `:1049` it lasts until the missing replicas connect,
  and at `:3161` it drops the replica list Sentinel held after its own failover.
- **Silent.** No condition reports a Sentinel with no monitor or too few peers or replicas
  ([`valkey_controller.go:2415-2424`](../../internal/controller/valkey_controller.go#L2415-L2424)
  checks only too many); the readiness probe is `PING`, so the pod stays Ready (T74).
- **A data pod boots as master when a Sentinel names it**
  ([`statefulset.go:288-332`](../../internal/builder/statefulset.go#L288-L332)); an `s_down`
  Sentinel still names its address. Replicas announce by hostname and Sentinel `myid` is pinned,
  so pod replacement leaves no stale replica or peer entries.

**Measured** (docker, `valkey/valkey:9.1.1` and `8.1.9`, identical; one master, two replicas,
three Sentinels in the operator's config; "unreachable" = `docker pause`; no TLS, no auth):

| Scenario | Result |
|---|---|
| Replies | `REMOVE nosuch`: `ERR No such master with that name`; `RESET nosuch`: `0`; per-master `SET resolve-hostnames` / `announce-hostnames`: `ERR Unknown option`; `MONITOR` at an unresolvable host: `ERR Invalid IP address or hostname specified`; `MONITOR` of an existing name: `ERR Duplicate master name.`, monitor unchanged |
| A: REMOVE + MONITOR on all three, master unreachable | all three `s_down`, 0 replicas, 0 peers, no failover (+60 s) |
| A2: forced failover after A | `NOGOODSLAVE` |
| B: address back as master | all three recover to 2/2 within 20 s |
| C: address back as a replica of a promoted pod | all three still name it, 0 replicas, 90 s and later |
| D: RESET on one Sentinel only | the other two fail over and heal it within 30 s |
| K: the code's shape (RESET on one, REMOVE + MONITOR on two) | all three `s_down` 0/0, forced failover `NOGOODSLAVE`; recover after unpause |
| E: MONITOR fails after a successful REMOVE | no monitor; every later REMOVE `No such master`, RESET `0`, MONITOR skipped - never re-added |
| F5: forced failover back to back | `OK`, `OK`, `OK` - no cooldown refusal |

**Impact.** Sentinel clusters during a data roll, on a failover timeout (`:3308`), a 2 min
finalization stall (`:958`, `:1007`, stalled `:1049`), a new master without replica after 90 s
(`:3161`), and through the rebuild window at the end of every Sentinel roll (`:1049`):

- A reset toward an unreachable master leaves the whole tier unable to fail over until that
  address answers as master; toward a pod that returns as a replica, the tier stays on it.
- A Sentinel can lose its monitor for good (E); only a Sentinel roll repairs it.
- On a cluster without persistence the no-failover state can end in an empty master that flushes
  its replicas (Not verified; T36's mechanism).
- ADR 0022 D6 and `CLAUDE.md` promise no RESET; a code path issues one.
- No pod template changes, so no option rolls the fleet.

## Required changes

**Independent of the open questions**

1. Remove the RESET fallback: keep the log at `:3667`, delete `:3668-3672`, so a failed REMOVE
   goes on to the MONITOR (a duplicate answers `Duplicate master name.` and the existing branch
   at `:3676-3679` skips the SETs; a missing monitor is re-added). Delete
   `valkeyclient.SentinelReset` (`client.go:249-257`) and its tests (`client_test.go:685`,
   `exec_test.go:467`). Rewrite `TestResetSentinelState_FallsBackToResetWhenRemoveFails` to
   assert REMOVE error, then MONITOR and SETs, with a revert check, and drop its false comment
   (`:1467-1469`). ADR 0022 Residual risks gains
   that a monitor lost after a successful REMOVE and a failed MONITOR stays missing until the
   next call. Check: `git grep -n "SentinelReset(" -- internal cmd` is empty.
2. Delete the two per-master SETs (`:3685-3686`) and their expectations
   (`sentinel_failover_test.go:1458-1459`).
3. Correct the cooldown comments (`:140-142`, `:3068-3070`, `:3527-3529`, `:3614-3615`) and
   reword "SENTINEL RESET" to REMOVE + MONITOR at `:151`, `:3545` and
   `rolling_update_bounds_test.go:760`. Check: `grep -n -i cooldown` on `rolling_update.go`
   finds only `:3665` or corrected text.
4. ADR 0022 Context (`:76-79`, "no way back"): a single reset Sentinel is healed by peers that
   keep their tables (D); a tier reset as a whole stays `s_down` 0/0 until the address answers as
   master, and a reset toward a replica stays on it (A2, B, C, K). ADR 0025 `:219` ("resets
   Sentinel onto the pod answering master") is imprecise: the scan also takes a label-only master
   or pod-0.

**Depends on the answers**

5. Q1 (B): the gate in `resetSentinelState`, the deletion of `:958` and `:1007`, and ADR 0022 D6
   amended to "REMOVE + MONITOR, only toward a verified master, never `SENTINEL RESET`". Unit
   tests, each failing with the gate removed or a call restored: named pod terminating, not
   Ready, `role:slave`, not answering, one of two scanned masters at `:3308` (no command); a
   verified pod (commands sent); a stalled finalization sends no Sentinel command. Re-read the
   stalled-finalization tests (`rolling_update_test.go:2849`, `:2901`, `:4625`). `make test-unit`,
   `make lint`, `make cyclo`, full e2e on `single-node-valkey9` and `single-node-valkey8`.
6. Q2 (skip if agreeing): read `SENTINEL MASTER` per Sentinel before resetting. Unit tests: all
   agree, no REMOVE; one disagrees, only it is reset; `SENTINEL MASTER` errors, it is reset; each
   fails with the check removed.
7. Close: the rule into ADR 0022 D6 with its residual risks; `CLAUDE.md:743` stays true under
   every kept option.

## Open questions

### Q1: Which master may `resetSentinelState` point every Sentinel at?

Today it takes any address, or pod-0, without checking the pod. A reset toward an unreachable
master rebuilds nothing until that address answers as master, and toward a returning replica it
points the tier wrong. The function returns nothing, so no caller's state transition or ADR 0010
bound changes either way.

- **A - no gate.** Only the independent changes land. XS. The tier-wide empty-table state stays
  reachable at `:958`, `:1007`, `:3308` and a stalled `:1049`, the paths where the master is
  least trustworthy.
- **B - gate on a verified master and delete the two stall resets (recommended).** Before the
  loop require that the named pod is `available()` (ADR 0026 D1) and answers `role:master` now;
  otherwise log and return. Delete `:958` (runs only with zero or several masters, never
  verifiable) and `:1007` (runs only after INFO just failed). At `:1049` uniqueness holds by
  construction. At `:3308` also require it is the only pod the scan marks master. At `:3161` no
  uniqueness check, because two masters is the designed post-failover state (ADR 0025) and only
  Sentinel promoted this pod. S; one extra INFO per Sentinel roll, no new state, no wait.

B, because a skipped unverified reset loses nothing (measured: such a reset rebuilds nothing or
the wrong tier), it needs no state and keeps the operator from choosing among masters for
Sentinel, and the retrigger at `:3308` meets no cooldown (F5). If the full e2e shows a test
depending on a stalled finalization resetting Sentinel, that is the reason to re-weigh A.

**Answer:** _open_

### Q2: Does a verified reset still blank every Sentinel, or only the Sentinels that disagree?

Even gated, the loop blinds every Sentinel at once for the rebuild window, at the end of every
Sentinel roll (`:1049`) and at `:3161`. Intact peers heal a reset Sentinel (D); a tier reset as a
whole does not (A2, K). `:3308` stays unconditional under both options: there the reset aborts a
Sentinel attempt in progress before the retrigger.

- **Keep the loop and record the window** in the ADR 0022 amendment. XS. Every roll keeps
  blanking the tier; `:3161` keeps dropping Sentinel's own replica list.
- **Skip each Sentinel that already names the verified master (recommended).** Read
  `SENTINEL MASTER` (`SentinelMasterInfo`,
  [`client.go:37-52`](../../internal/valkeyclient/client.go#L37-L52)) and leave a Sentinel alone
  if its `ip` is the verified address, `flags` are exactly `master`, and - at a non-stalled
  `:1049` only - `num-slaves` is at least the expected count. An error (no monitor) counts as
  disagreeing. Peer count is not a criterion. One round trip per Sentinel.

Skip-if-agreeing keeps exactly the stale-address case the reset exists for, removes the
tier-wide blanking on every roll and the long windows at `:3161` and a stalled `:1049`, for one
read per Sentinel under the same e2e run as Q1.

**Answer:** _open_

## Not verified

- Kubernetes behaviour of the measured states (termination instead of pause, TLS, auth): a Kind
  Sentinel roll with the master killed inside the stall would settle it.
- Whether `:1049` completes in production: an archived fleet observation (peer tables 4/3/2
  unchanged after a roll) suggests it does not; the operator log of a Sentinel roll with V(1),
  showing "Sentinel reconfigured successfully" three times or not, settles it.
- Data loss on a cluster without persistence after a tier-wide reset (inference plus upstream
  documentation).
- The sidecar labeler trusting the first Sentinel (`internal/sidecar/labeler.go:135-141`) could
  move the master label when `:958` points the tier at one of two masters.
- DNS for a terminating pod, which decides how often MONITOR at a pod FQDN fails.
- A forced failover while another Sentinel holds `-INPROG`.
- Whether anything depends on the routine all-Sentinel wipe at `:1049` (Q2).
- That a container restart keeps the monitor-less or emptied state (by reading).
- That Sentinel's `ip` field holds the pod FQDN in Kubernetes (Q2 comparison).

## Related

- [T34](034-e2e-fixtures-wait-on-controller-state-after-a-pod-delete.md) - lists the five callers and the `config_epoch` restart a reset causes.
- [T35](035-master-records-lag-the-real-master.md) - relies on `persistKnownMaster` at `:1045`; the gate must stay below it.
- [T36](036-non-persistent-master-restarts-empty.md) - the tier-wide empty-table state is a route into its mechanism.
- [T51](051-a-changed-cluster-password-reaches-no-running-pod.md) - `auth-pass` is set here (`:3692`); under Q1 B a master on the old password fails the gate; T51 does not reuse this function.
- [T73](073-forcereplicaconnections-re-points-a-data-holder-at-an-empty-master.md) - `forceReplicaConnections` runs right before `:3161`; unchanged here.
- [T74](074-no-condition-reports-a-sentinel-with-no-monitor-or-too-few-peers-or-replicas.md) - reports the states this ticket produces.
- [T75](075-the-sentinel-failover-reset-and-retrigger-cycle-has-no-cap.md) - the uncapped cycle repeats the `:3308` reset.
