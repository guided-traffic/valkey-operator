---
id: T34
title: e2e fixtures wait on controller state after deleting a pod
state: analysed       # re-verified 2026-09-27 at 84a39c2: facts and options checked, D1 open
severity: low         # was medium (2026-09-27): the only red of the class was the drain-master instance, fixed in b13377e; sites 1 and 2 show no failure in 17 legs; left are two subtests that cannot fail as named (ADR 0017 D10), unobserved flake paths, and a replica-drain regression on the Sentinel path that would merge green - the impact T43 and T58 rate low
security: none        # no guard involved is a security control
urgency: now          # was next (2026-09-27): rule 1 - tracked test comments are false today: sidecar_test.go:523-524 names a helper :526 does not call, and sidecar_test.go:487 and sentinel_stale_master_test.go:130 promise a recovery their waits do not wait for (refuted by the run timings); precedent T46, T63, T62
effort: M             # the code is about 15-35 lines; the Kind runs, revert checks, mutation, CI run and close edits dominate
blocked-by: decision  # D1 only (shared wait semantics or per-site UID waits); the comment fix at sidecar_test.go:523-524 and site 1's effect read need no decision
filed-from: T31, section "Drain-test finding", and ADR 0017 D50 as amended 2026-09-26
opened: 2026-09-26
decided:
done:
---

Filed as a board row on 2026-09-26 out of the drain-test finding recorded on
[T31](archive/031-generated-pods-run-as-root.md) (section "Drain-test finding") and at
[ADR 0017](../adr/0017-test-and-ci-policy.md) D50. This file was written the same day. Every
file:line below was re-read in the tree at `a8e8931` (`feat/rootless`, clean). *(Re-read
2026-09-27 at `4a7543e`: in `test/e2e/` only `sidecar_test.go` changed since `a8e8931`, where
`f5c6886` split the comment line `:281` in two, so every `sidecar_test.go` line after `:281` is
one higher; those cites, and the moved ADR 0017 and `CLAUDE.md` lines, are corrected in place.)*
*(Re-read 2026-09-27 at `84a39c2`: `test/e2e/` changed since `4a7543e` only by one removed
comment line in `migrate_e2e_test.go`, which no cite here names, so every `test/e2e` line below
is current; `git diff 4a7543e 84a39c2 --stat -- test/e2e`.)*
Each claim is
labelled by how it was verified ([ADR 0017](../adr/0017-test-and-ci-policy.md) D36):

- **run** means taken from the log of an executed run. The log is named at the claim.
- **read** means read in the tree, in the module cache (`k8s.io/kubernetes@v1.36.4`), or in a
  log line that records a fact and not a measurement.
- **measured** *(added 2026-09-27)* means a docker run of the pinned Valkey images in this
  re-verification, with the command recorded under "Measured 2026-09-27".
- **hypothesis** means neither.

**No test was executed for this file.** Every "run" label points at a log of an earlier run.
*(2026-09-27: still no e2e, unit or integration test was run; the re-verification at `84a39c2`
ran Sentinel failovers in docker on the two pinned Valkey images, recorded under "Measured
2026-09-27".)* The logs are in the session scratchpad
`/private/tmp/claude-501/-Users-hfi-repos-valkey-operator/538d7ed7-2fba-46e2-87eb-bb14662fc87c/scratchpad/`,
which is not tracked *(2026-09-27: the directory still existed and the counts below were
recounted from it, but it lies under `/private/tmp` and is volatile; the numbers extracted into
this file are the durable record)*:

| Log | What it is |
|---|---|
| `e2e_full9.log`, `e2e_full8.log`, `final2.log` … `final6.log` | The seven local runs of 2026-09-26 on Kind (`kindest/node:v1.36.1`), every one on a cluster of **control-plane + 3 workers** (`make kind-create`; `final6.log:7-20` shows four nodes joining, `:160` loads onto `valkey-operator-test-worker3`). Together they hold 11 full-suite legs. `leg8.log` is an excerpt of `final4.log` (byte-for-byte substring) and is not counted twice. |
| `ci-b13-failed.log`, `ci-a04-failed.log`, `ci-e6a-full.log` | CI on `b13377e`, `a04e2d0` and `e6a9d7c`. Each has two single-node full-suite legs, Valkey 9 and Valkey 8, on `kindest/node:v1.33.4`. |
| `drainexp.log`, `drain-orig-*/`, `drain-fixed-*/` | The Kind experiment with `TestE2E_SidecarFailoverDrainMaster` run alone: `test.log` per run, and `roles.txt` from a watcher that sampled role and `DBSIZE` ~~about once a second~~ *(corrected 2026-09-26, adversarial check: about every 2 s per pod — `drainwatch.sh` sleeps 1 s between rounds of nine `kubectl` calls; `drain-orig-1/roles.txt` samples `sc-drain-0` at :36, :38, :40, :42, :43)*. |
| `ci-repro-full9.log` | **Excluded from every count.** A local full suite on the CI Kind config (`kindest/node:v1.33.4`, 17:41) with the drain fix, 36 of 49 `TestE2E_*` red: the environment did not start pods (`ready=0/N` 4362 times). The drain-master and replica-drain tests failed at their setup wait (`sidecar_test.go:244`, ~~`:460`~~ *(corrected 2026-09-27: `:461`)*) and never reached their delete, so it is no run of the fix. Site 2 did reach its delete and logged `:137` right after two 3/3 polls. |

Across those logs, 17 distinct legs ran each of the tests below: 11 local and 6 CI.

## Fact

### Mechanism (read)

After a `Delete` of a pod, every wait and read that the fixtures use can be answered by the pod
that is terminating:

- **kubelet keeps a terminating pod Ready.** It does not flip `PodReady` while the readiness
  probe still passes, for the whole termination
  ([ADR 0026:150-162](../adr/0026-a-pod-being-deleted-is-not-available.md), measured there on
  Kubernetes 1.36.1).
- **The StatefulSet status counts terminating pods.**
  - `readyReplicas` counts every pod with `isRunningAndReady`, and that check has no terminating
    test (`stateful_set_control.go:378`, `stateful_set_utils.go:457-458`, v1.36.4).
  - `replicas` counts every pod with `isCreated` (`:373`, `:466`).
  - So `waitForStatefulSetReady` ([`e2e_test.go:147-163`](../../test/e2e/e2e_test.go)) returns
    on the pods that existed before the delete. Its poll compares `ReadyReplicas` alone
    ([`e2e_test.go:160`](../../test/e2e/e2e_test.go)) and runs its first round immediately
    (`:151`) *(added 2026-09-27, read at `84a39c2`)*.
- **The other helpers name the pod, not the process.**
  - `waitForPodReady` (`e2e_test.go:285-305`) reads the pod by name, with no UID and no
    `deletionTimestamp` check.
  - `getPod` (`:604-611`) requires the name to exist.
  - `valkeyExec`/`valkeyExecQuick` (`:230`, `:523`) exec into whichever process holds the name at
    that moment. `valkeyExecQuick` returns an empty string on any exec error
    ([`e2e_test.go:520-542`](../../test/e2e/e2e_test.go)) *(added 2026-09-27, read)*.
  - `findMasterPod` ([`rolling_update_test.go:733-750`](../../test/e2e/rolling_update_test.go))
    returns the first ordinal that answers `role:master`. Its `require.Eventually` evaluates the
    condition once immediately (testify v1.12.1, `assert/assertions.go:2023-2024`, read), so a
    poll that succeeds returns within the first round of execs.
- **A phase wait for `OK` can be met by the previous status.** The operator writes the phase
  asynchronously, so a phase that is already `OK` before the delete satisfies the wait.
- **The identity wait exists.** `waitForPodRecreated` (`e2e_test.go:307-340`) waits until the
  name exists under another UID and is Ready. Its own comment records the same trap: the
  StatefulSet wait returned 106 ms after a delete in CI.
- **Two exceptions hold by construction.**
  - A PodDisruptionBudget's `currentHealthy` skips terminating pods (`disruption.go:919-924`,
    v1.36.4).
  - `status.replicas == 0` holds only once every pod, terminating or not, is gone.
- **What separates the old pod from the new one** *(added 2026-09-27, read)*. ADR 0017 D50 names
  the identity signals: image, UID or `deletionTimestamp`
  ([ADR 0017:568-572](../adr/0017-test-and-ci-policy.md)). The API server sets
  `deletionTimestamp` when a graceful deletion is requested, and once set it "may not be unset"
  (`k8s.io/apimachinery@v0.37.1`, `pkg/apis/meta/v1/types.go:215-221`, the `ObjectMeta` field
  doc, read), so after the delete a pod of that name without one is the replacement. D1 option E
  below builds on this signal, option A on the UID.

### The fixed instance: `TestE2E_SidecarFailoverDrainMaster`

The test deletes the master of a 3+3 Sentinel cluster
([`sidecar_test.go:274`](../../test/e2e/sidecar_test.go)). It then waited for the StatefulSet at
3/3 (~~`:285`~~), phase `OK` (~~`:286`~~), every pod Ready (~~`:289-291`~~), and exactly one pod
answering master (~~`:295-306`~~) *(corrected 2026-09-27: `:286`, `:287`, `:290-292`,
`:296-307`)*. The terminating old master meets all four.

- **The red run (run, `final6.log`, the Valkey 9 leg of the day's last local run,
  2026-09-26).**
  - The cluster had four nodes, control-plane + 3 workers (`final6.log:7-20`), not one. Three
    tracked records call it single-node (below).
  - The delete subtest passed in 0.38 s. Its one master-count poll read 1 (`final6.log`,
    "Master count after failover: 1"), which was the old master alone: the next subtest found
    `sc-drain-0` still answering master with the key.
  - "Data survives failover" logged `Pod sc-drain-0: role=master EXISTS drain:key:0=1` and chose
    `sc-drain-0`, the pod it had just deleted, as the new master.
  - Its first `DBSIZE` exec failed and was retried after the 4 s backoff (`Retrying valkeyExec on
    pod sc-drain-0 (attempt 2/5, backoff 4s)`). The retry read `0`, and the test failed on
    "got 0" at the old line `sidecar_test.go:334`, now ~~`:343`~~ *(corrected 2026-09-27: `:344`)*.
  - The next subtest found `-rw` selecting `sc-drain-1`.
  - These log lines fit the recorded diagnosis: the old process left between the `EXISTS` and
    the `DBSIZE`, and the retry reached the empty replacement. **Which process answered each
    exec is not traced**, because the pod logs were lost with the CR.
  - Whether `sc-drain-1` held all 50 keys in that run was never read. The log has no `DBSIZE` of
    it.
  - ADR 0017 D50 also says the operator log of that cluster shows no operator action. **Not
    re-verified here:** that operator log is not among the kept files.
  - *(Recounted 2026-09-27 from `final6.log`: four nodes at `:7-20`, "Master count after
    failover: 1" at `:1343`, the `EXISTS` line at `:1354`, the retry at `:1367`, "(got 0)" at
    `:1466`.)*
- **Frequency before the fix (run).**
  - In the 11 local full-suite legs, the delete subtest took under 1 s twice: 0.35 s in
    `final5.log`, which passed, and 0.38 s in `final6.log`, the red run. The other 9 legs took
    3.30–3.55 s.
  - Run alone on Kind (`drainexp.log`), 5 of 10 runs took 0.28–0.30 s and 5 took 3.28–3.31 s.
    All 10 passed.
  - The 3.3 s path did observe the failover. In `drain-orig-1/test.log`, the master count read 2
    and then 1 one poll later. It still did not wait for a Ready replacement, which took at
    least 8.3 s in the fixed runs. This part is an inference from those two logs.
  - **The drain's failover is fast (run, inference marked).** All five 3.3 s runs of the
    experiment (`drain-orig-1, 2, 4, 5, 7`) read a master count of **2** on the first poll, and
    all five sub-second runs read 1. Testify evaluates the first poll immediately (above), so
    that poll ran right after the StatefulSet, phase and pod waits, about 0.3 s after the delete
    — the sub-second runs finished in 0.28–0.30 s in total. So a Sentinel failover triggered by
    a drain handler (`internal/sidecar/drain.go:128-129`, `SENTINEL FAILOVER` on SIGTERM, read)
    had a second pod answering master about 0.3 s after the delete in half the runs
    (inference from the subtest durations; the test log has no timestamps).
- **The watcher (run, `drain-orig-1/roles.txt`).**
  - `sc-drain-1` was master with `dbsize=50` at 15:53:35.
  - The replacement `sc-drain-0` (uid `9edea779`) read `dbsize=0` at 15:53:38 and 15:53:40, and
    50 at 15:53:42.
  - ~~In `drain-orig-3` and `drain-orig-6`, the replacement's first answer was `role:slave` with
    `dbsize=0`.~~ *(corrected 2026-09-26, adversarial check: not only those two.)* In **all 18
    runs** of the experiment, 10 before the fix and 8 after, the replacement `sc-drain-0` (the
    last UID of that name in each `roles.txt`) first answered `role:slave` with `dbsize=0`.
  - *(Added 2026-09-27, run: the Sentinel logs of `drain-orig-1`,
    `sc-drain-sentinel-*-sentinel.log`.)* The leader logged `+try-failover` at 13:53:33.841Z, the
    promotion at 13:53:34.93Z, the two followers `+switch-master` at 13:53:34.99Z, and the leader
    its own `+switch-master` at 13:53:36.03Z: on Kind, with the old master deleted, the leader's
    master address lagged the followers by about 1.0 s.
- **The fix (read).** The test records the UID at `:272` and calls
  `waitForPodRecreated(t, ns, initialMaster, killedUID)` at ~~`:282`~~ *(corrected 2026-09-27:
  `:283`)*, before any role or data
  check. It landed in `b13377e`.
- **Runs of the fix (run).**
  - Alone: 8 of 8 green, delete subtest 8.29–10.32 s (`drainexp.log`).
  - CI full suite, both single-node legs, all green:

    | Commit | Valkey 9 | Valkey 8 |
    |---|---|---|
    | `b13377e` | 33.16 s | 36.47 s |
    | `a04e2d0` | 20.49 s | 14.41 s |
    | `e6a9d7c` | 22.78 s | 12.84 s |

  - "Full suite" means `go test` ran without `-run`, with ~~49 or 50~~ *(corrected 2026-09-26,
    adversarial check, counted per leg in all six)* 53 top-level tests per leg, 50 of them
    `TestE2E_*` and three the package's own unit tests.
  - `b13377e` and `a04e2d0` were red only on
    `TestE2E_PodHardening_UserNamespacesLocalhostSeccompAndDigest`.
- **The fix's comment cites the wrong rule (read).** `sidecar_test.go:276` says "(ADR 0017 D50,
  D51)". D51 is the EndpointSlice rule
  ([`0017:604`](../adr/0017-test-and-ci-policy.md)). The board row corrected its own copy of
  that citation on 2026-09-26 *(not verifiable: the board is untracked and keeps no earlier
  version; no ticket file carries "D50, D51" today)*, but the code comment still carries it.
  `CLAUDE.md:227` cites "D50, D51" correctly: its paragraph states both rules.
- **The same comment calls the red run single-node (read, `sidecar_test.go:281`).** It was not;
  see the next section.
- *(2026-09-27: both comment defects were fixed in `f5c6886`, read at `4a7543e`: `:276` cites
  D50 alone, and `:281-282` name the local Kind cluster of control-plane + 3 workers. D51 is at
  ADR 0017 `:619` today, the `CLAUDE.md` cite of both rules at `:288`. Re-read at `84a39c2`:
  unchanged; `git branch -r --contains f5c6886` lists `origin/main`.)*

### Tracked records of the fixed instance that are false now

*(Added 2026-09-27: superseded — all of them were corrected on 2026-09-26 in `f5c6886`, the doc half. The lines in this
section are those at `a8e8931`, before that change; at `4a7543e` the corrected text sits at ADR
0017 `:111-114`, `:118-120`, `:133-136`, `:582-584`, `:603-604` and `CLAUDE.md:282-284`,
`:1045-1047`. The D5 record of `e6a9d7c` is at ADR 0017 `:285-288`, the filing statement at
`:606-617`.)*

- **Measured false (run: the CI logs above).** Three sentences say that no full suite has run
  on the drain fix. CI ran the full suite on it in six single-node legs on `b13377e`,
  `a04e2d0` and `e6a9d7c`, and the drain test passed in all six.
  - [ADR 0017:130](../adr/0017-test-and-ci-policy.md): "No full suite has run on the D50 fix."
  - [ADR 0017:595](../adr/0017-test-and-ci-policy.md): "**Not verified:** no full suite has run
    on the fix".
  - [`CLAUDE.md:975`](../../CLAUDE.md): "no full suite has run on the drain fix."

  ADR 0017 records the `e6a9d7c` run itself, in its D5 amendment at `:279-282`, but it did not
  supersede these three sentences.

  *(Precised 2026-09-26, adversarial check, read with `git blame`.)* All three were written in
  `b13377e` (committed 16:26 CEST) and were true then; CI on that commit started its e2e legs at
  14:32Z (16:32 CEST) and made them false, and `a8e8931` added the D5 record without touching
  them. `:130` and `CLAUDE.md:975` each follow a clause about the local Kind runs and can be read
  as scoped to those, though neither says so; `:595` carries no scope at all. The match does not
  depend on the reading: `:595` alone is false as written.
- **False by the red run's own log (read: `final6.log:7-20`, which records the cluster it
  created), found in the adversarial check.** Three tracked records place the red run on a
  single-node cluster. Every local run of that day, the red one included, ran on
  `make kind-create`'s control-plane + 3 workers (`Makefile:181-185`; `e2e_setup.log` and
  `final2.log` … `final6.log` each show the workers joining); only the CI legs are single-node.
  - [ADR 0017:112](../adr/0017-test-and-ci-policy.md): "went red once, on the single-node
    Valkey 9 leg";
  - [ADR 0017:576](../adr/0017-test-and-ci-policy.md): "On the single-node Valkey 9 leg of the
    last full e2e run of the day";
  - [`sidecar_test.go:281`](../../test/e2e/sidecar_test.go): "(measured 2026-09-26, single-node
    Valkey 9 leg)".

  Whether four nodes changed the timing of the red run is not known; nothing in this ticket
  depends on it.
- **~~Untracked,~~ T31, same two errors (read).** ~~[T31](archive/031-generated-pods-run-as-root.md):948
  says "single-node Valkey 9 suite", and T31:975-976 says "No full-suite run with the fix is
  recorded". T31 is gitignored, so neither counts for rule 1, but both belong to the doc half.~~
  *(corrected 2026-09-27 at 84a39c2: T31 is tracked, as
  [`archive/031-generated-pods-run-as-root.md`](archive/031-generated-pods-run-as-root.md), since
  `4a7543e` (`git ls-files docs/tickets`, `git log --diff-filter=A`); "gitignored" was true on
  2026-09-26, when it was a `local_` file. `f5c6886` corrected its `:948` and `:975-976`. A third
  copy of the second error stands at
  [`archive/031:798-799`](archive/031-generated-pods-run-as-root.md), "No full-suite run with the
  fix is recorded.", inside T31's dated Final-run block. It is left as it is, knowingly: an
  archived ticket is history and never the source of a current rule
  ([README](README.md) "The extraction is the close"), and the current record of the CI runs is
  ADR 0017 D50 `:603-605`.)*
- **Stale after this audit (read).** Three present-tense sentences still call the sites "of the
  same shape, not yet audited":
  - [`CLAUDE.md:222-223`](../../CLAUDE.md);
  - `SECURITY_ARCHITECTURE.md:1578`, ~~since the documentation restructure of 2026-09-27
    [`docs/security/workload-pod-posture.md:252`](../security/workload-pod-posture.md#h-16)
    (gap H-16)~~ *(gone 2026-09-27: the body of gap H-16 was replaced by a gap statement without
    the run log, so the sentence no longer exists anywhere under `docs/security/`; the audit
    result stays in ADR 0017 D50)*;
  - [ADR 0017:117](../adr/0017-test-and-ci-policy.md).

  Three of the five sites are fine (below). `0017:597-601` is a filing statement ("filed
  unaudited as T34") and stays true as history.

### Audit of the five sites

The five sites came from a grep for pod deletes not followed by a UID-aware wait within 14 lines.

| # | Site | Delete | Class |
|---|---|---|---|
| 1 | `TestE2E_SidecarDrainReplica` | [`sidecar_test.go`](../../test/e2e/sidecar_test.go) ~~`:484`~~ *(corrected 2026-09-27: `:485`)* | **vacuous**, two hypothetical flake paths |
| 2 | `TestE2E_SentinelStaleMaster` | [`sentinel_stale_master_test.go:126-127`](../../test/e2e/sentinel_stale_master_test.go) (six deletes, loop `:125-128`) | **vacuous** waits; the assertions are protected only by timing |
| 3 | `TestE2E_AdmissionRejection_StatefulSetNudgeRecovery` | [`admission_recovery_test.go:237`](../../test/e2e/admission_recovery_test.go) (loop `:236-238`) | fine, by construction |
| 4 | `TestE2E_RollingUpdate_NoSecondDeleteWhileAPodTerminates` | [`pod_termination_test.go:128`](../../test/e2e/pod_termination_test.go) | fine; observes termination deliberately |
| 5 | `TestE2E_RollingUpdate_TopologyRestoreAbandoned` | [`topology_abandon_test.go:241`](../../test/e2e/topology_abandon_test.go) | fine, by effect |

**1 — `TestE2E_SidecarDrainReplica` (read; timings run).** The test deletes a replica of a 3+3
Sentinel cluster. *(Corrected 2026-09-27: every line of this site below is one higher at
`4a7543e`; each is corrected where it stands.)*

- ~~`:487-488`~~ *(corrected 2026-09-27: `:488-489`)* wait for the StatefulSet at 3/3 and for
  phase `OK`. The terminating replica meets both. *(Added 2026-09-27, read at `84a39c2`: the
  comment above them,
  [`sidecar_test.go:487`](../../test/e2e/sidecar_test.go) "Wait for cluster to recover (all 3
  pods ready).", is therefore false as written — the subtest it opens ended 0.10–0.32 s after its
  start in 17 of 17 legs, below.)*
- ~~`:491-492`~~ *(corrected 2026-09-27: `:492-493`)* then compare `findMasterPod` with `initialMaster`. ~~At that moment a failover
  triggered by the replica delete could not have happened yet, because Sentinel needs seconds to
  declare a master down.~~ *(corrected 2026-09-26, adversarial check: the conclusion holds, the
  reason did not.)* A failover a replica drain could trigger is a forced `SENTINEL FAILOVER`
  from its drain handler (the regression is the role check at `drain.go:114` letting a replica
  through), which needs no down detection; the drain-master experiment above shows such a
  failover putting a second master up about 0.3 s after the delete in half the runs. Timing
  does not make the check vacuous. The ordinal order does:
  - In **17 of 17 legs** `initialMaster` was `sc-repdr-0` and the deleted replica `sc-repdr-1`
    (run, `sidecar_test.go` ~~`:477`~~ *(corrected 2026-09-27: `:478`)*, formerly `:468`, in
    every log).
  - *(Added 2026-09-27, read at `84a39c2`: the 17 of 17 is a property of the code, not a
    coincidence.)* A fresh cluster's master is pod-0: the replica `replicaof` target is
    `MasterAddress`, pod-0 of the StatefulSet
    ([`configmap.go:31-38`](../../internal/builder/configmap.go)), and the init container's last
    resort is the ordinal fallback that boots ordinal 0 as master
    ([`statefulset.go:340-345`](../../internal/builder/statefulset.go)). The test deletes the
    first ordinal that is not the master
    ([`sidecar_test.go:470-476`](../../test/e2e/sidecar_test.go)). Only a failover during setup
    would change that, and none was seen.
  - `findMasterPod` asks ordinal 0 first and returns the first pod answering `role:master`
    (read). `sc-repdr-0` keeps answering master until Sentinel reconfigures it, which comes
    after any promotion (read in the Valkey `sentinel.c` copy in the scratchpad: the failover
    states at `:111-117` send `REPLICAOF NO ONE` before any reconfiguration, and the old
    primary is turned into a replica on a later INFO refresh, `+convert-to-slave` at `:2641`;
    which Valkey release that copy is, is not recorded). *(Measured 2026-09-27 on both pins,
    four runs, see "Measured 2026-09-27": after a forced `SENTINEL FAILOVER` the old master
    still answered `role:master` at t+10.3–10.5 s and answered `role:slave` from t+11.5–11.7 s;
    the promoted replica answered `master` by t+0.1–1.2 s.)*
  - So the assertion passes whatever happened to the other two pods, as long as `sc-repdr-0`
    has not been demoted yet. *(Precised 2026-09-27: `findMasterPod` asks through
    `valkeyExecQuick`, which returns an empty string on an exec error, so a transient exec
    failure on pod-0 makes it ask pod-1 and pod-2; outside that case the check cannot fail
    within its window. It fails [ADR 0017 D10](../adr/0017-test-and-ci-policy.md) (`:349-351`,
    a test named after a guard must break when the guard is removed) in substance.)*
  - The subtest ended 0.10–0.32 s after its start in **17 of 17 legs** (run).
  - So "delete replica does not trigger master failover" never looks after the drain, and could
    not see a promotion elsewhere even if it looked early enough.
- ~~`:498`~~ *(corrected 2026-09-27: `:499`)* reads the key from the master. That is fine, but
  it says nothing about the delete.
- ~~`:504`~~ *(corrected 2026-09-27: `:505`)* `waitForPodLabel(replicaPod, instanceRole, replica)`
  is met by the terminating pod's own label.
  - The subtest took 0.01–0.10 s in 17 of 17 legs (run). *(2026-09-27: not recounted in the
    re-verification.)*
  - A replacement cannot be created, started and labelled by the sidecar within about 0.7 s of
    the delete. This is an inference from the 8.3 s minimum of the fixed drain test.
  - So "recreated replica labeled correctly by sidecar" reads the old pod. *(Added 2026-09-27,
    read: `waitForPodLabel` reads by name only,
    [`sidecar_test.go:28-44`](../../test/e2e/sidecar_test.go), and the replica's drain never
    patches its label — [`drain.go:114-117`](../../internal/sidecar/drain.go) returns before the
    patch at `:120`.)*
- ~~`:509`~~ *(corrected 2026-09-27: `:510`)* calls `getPod` with `require.NoError` for every
  ordinal. **Hypothesis:** this fails when
  it lands between the old pod's removal and the replacement's creation. It was not observed in
  17 legs.
- ~~`:519`~~ *(corrected 2026-09-27: `:520`)* `waitForConnectedReplicas(initialMaster, 2)` is
  met while the old replica is still online. ~~`:524-529`~~ *(corrected 2026-09-27: `:525-530`)*
  reads the key from `replicaPod` by name, and the old replica holds it.
  - Vacuous in 2 of 17 legs: 0.19 s in `e2e_full9.log`, 0.28 s in `ci-a04-failed.log` Valkey 8.
  - In the other 15 legs it took 6.2–30.2 s (run), because the old replica had disconnected
    first (inference: the durations fit a wait for the replacement's sync).
  - *(Added 2026-09-27, read at `84a39c2`.)* The comment at
    [`sidecar_test.go:523-524`](../../test/e2e/sidecar_test.go) says "Use valkeyExecAllowError",
    but `:526` calls `valkeyExecQuick`. `eba7e3d` (2026-03-21) replaced `valkeyExecAllowError`
    with `valkeyExecQuick` in the polling loops (`git log -S valkeyExecQuick --
    test/e2e/sidecar_test.go`; its message: "Replace valkeyExecAllowError (30s timeout x 3
    retries) with valkeyExecQuick"). The comment's reason, a transient exec failure, still fits
    `valkeyExecQuick`; only the helper name is false.
- ~~`:533`~~ *(corrected 2026-09-27: `:534`)* `waitForEndpointPodCount(-r, 2)` is the one step that saw a replacement whenever it
  started early (inference, resting on the endpoint readiness below). In both legs where the
  replication subtest was vacuous, it then waited 10.0 s and 16.1 s (run).
  - ~~That a terminating pod's endpoint is published not-ready is stated in ADR 0026:160-161 ("the
    endpoints controller carries its own `DeletionTimestamp` check"). The upstream source was
    **not read** here.~~ *(corrected 2026-09-27 at 84a39c2: read upstream.)* The EndpointSlice
    controller publishes a terminating pod not-ready: `podToEndpoint` sets
    `ready := service.Spec.PublishNotReadyAddresses || (serving && !terminating)` with
    `terminating := pod.DeletionTimestamp != nil`
    (<https://raw.githubusercontent.com/kubernetes/kubernetes/v1.36.4/staging/src/k8s.io/endpointslice/utils.go>,
    `podToEndpoint`, about lines 33-40; two fetches placed it at 33-39 and 35-40, so the exact
    lines are not pinned). The `-r` Service sets no `PublishNotReadyAddresses`
    ([`service.go:204-221`](../../internal/builder/service.go), `BuildReadOnlyService`; only the
    headless Services set it, `:154`, `:237`), and `readyEndpointPodNames` skips a not-ready
    endpoint ([`e2e_test.go:449-451`](../../test/e2e/e2e_test.go)). So the `-r` wait cannot be
    met by the terminating replica.
  - **Hypothesis:** the count read at ~~`:533`~~ and the list read at ~~`:534`~~ *(corrected
    2026-09-27: `:534`, `:535`)* are separate, so an
    endpoint that flips between them fails `assert.Len`. Not observed. *(2026-09-27: after an
    identity wait and a synced replica the window is expected to be closed; inference.)*

**2 — `TestE2E_SentinelStaleMaster` (read; timings run).** The test deletes all three data pods
and all three Sentinel pods.

- `:131-132` wait for both StatefulSets at 3/3, `:133-136` wait for each of the six pods to be
  Ready, and `:137` logs "All pods restarted and ready". All of it is met by the old pods.
  *(Added 2026-09-27, read at `84a39c2`: the comment at
  [`sentinel_stale_master_test.go:130`](../../test/e2e/sentinel_stale_master_test.go), "Wait for
  all pods to come back.", is false in the same legs as the log line.)*
  - In 11 of 11 local legs, both StatefulSet waits read 3/3 on their first poll (run).
  - In 5 of 6 CI legs, the log line came 0.34–0.62 s after the delete line: `b13377e` 0.61 s and
    0.36 s, `a04e2d0` Valkey 9 0.50 s, `e6a9d7c` 0.34 s and 0.62 s. The sixth leg (`a04e2d0`
    Valkey 8) took 24.4 s. *(Recounted 2026-09-27 from the timestamps of the `:124` line, logged
    just before the delete loop, and the `:137` line: `ci-b13-failed.log:4564/:4597` and
    `:15282/:15293`, `ci-a04-failed.log:5045/:5059` and `:16696/:16922` (24.36 s),
    `ci-e6a-full.log:12312/:12324` and `:25718/:25729`.)*
  - So the log line at `:137` is false in 16 of 17 legs: measured by timestamps in the 5 CI legs
    *(precised 2026-09-26, adversarial check)*; in the 11 local legs inferred, because local logs
    carry no timestamps — the first-poll 3/3 reads and at most 9 lines of the parallel tests'
    output between the delete and `:137` fit a sub-second pass, but a `waitForPodReady` that hit
    a `NotFound` would have waited for the replacement unseen.
- The subtests at `:140-210` poll `sentinel-0` and the data pods by name. In every recorded leg
  ~~they read the replacements (run)~~ *(precised 2026-09-26, adversarial check: no log line
  names a UID, so this is an inference from the runs below)* the logs fit reads of the
  replacements:
  - The master before the restart was pod-1 or pod-2 in all 17 legs. Afterwards, both the
    Sentinel answer at `:167` and `findMasterPod` at `:173` named pod-0 (run). That excludes the
    old master; it does not exclude the old pod-0 promoted by a failover during the terminations
    (hypothesis, not observed). *(2026-09-27: the pre-restart side is also forced by the test,
    [`sentinel_stale_master_test.go:77-121`](../../test/e2e/sentinel_stale_master_test.go),
    read.)*
  - 15 of the 17 legs also show "reports 0 slaves", "reports 1 slaves",
    `flags: master,disconnected` or "Sentinel not responding" on the way.
  - The first subtest took 5.1–35.2 s in 16 legs. In the one leg where it took 0.11 s, the pod
    waits had already taken 24 s.
- They do so because the old Sentinel processes stop answering within the first second, not
  because the fixture waits (inference from the one operator log below; data pods not
  checked).
  - The CI operator log of `a04e2d0` Valkey 9 shows "connection refused" for `sentinel-0` in the
    same second as the delete (run).
  - *(2026-09-27: not re-read in the re-verification. What would check it: a Kind run that logs
    the Sentinel pods' termination times next to the subtests at `:140-162`.)*
- **Hypothesis, not observed:** an early read that still reaches the old `sentinel-0` passes
  `:140-157` and `:160-162` on the pre-restart topology.
- **Hypothesis, not observed:** the one read at `:167` (`valkeyExecAllowError`, which retries
  only on exec errors, `standalone_test.go:552-596`, and never polls for an answer) and the
  `findMasterPod` at `:173` can straddle the two generations of pods. *(Added 2026-09-27:)* the
  read at `:167` is `SENTINEL get-master-addr-by-name`. For that command a failover leader
  switches to the new master **first**, at `+promoted-slave`, and before it every Sentinel names
  the old master (`sentinelGetCurrentPrimaryAddress`, read in the saved `sentinel.c` copy at
  `:1659-1670` and `:3906-3915`, release not recorded; measured on both pins in
  [T35](035-master-records-lag-the-real-master.md), not re-measured here). The leader lag
  measured below (about 1.0 s on Kind, 3.5–5.8 s in docker) is the `ip` field of `SENTINEL
  MASTER` and does not apply to `:167`. Site 2 deletes every pod and forces no failover
  ([`sentinel_stale_master_test.go:123-137`](../../test/e2e/sentinel_stale_master_test.go)), so
  no failover figure transfers; an identity wait removes only the old-generation half of the
  straddle. Still unverified.

**3 — admission recovery: fine.**

- Pod creation is blocked by the webhook at `:233` before the deletes.
- `:242-249` then waits for `sts.Status.Replicas == 0`. Terminating pods count toward
  `replicas`, so this means every old pod is gone.
- `:255-267` polls for a phase other than `OK`, and the phase before the delete was `OK`
  (`:230`).
- After the webhook is removed, only new pods can exist (`:301`).
- Nothing after the delete can be met by the state before it (read). *(Re-read 2026-09-27 at
  `84a39c2`: holds; the one StatefulSet wait after the deletes, `:310`, runs after the webhook is
  removed at `:297-306`.)*

**4 — pod termination: fine, and it observes termination deliberately.**

- **Victim by identity.** The victim is chosen by identity (`:199-237`): the new image, no
  `deletionTimestamp`, Ready.
- **Sampler.** It reads `deletionTimestamp` every 250 ms (`:405-418`). The attribution is armed
  before the delete (`:127`).
- **The final waits need the replacement.** The waits at `:134-136` cannot be met by the state
  before the delete:
  - at selection time, a pod that is not terminating still ran the old image (`:224-229`);
  - the roll can finish only by replacing that pod;
  - the operator does not replace it while the victim terminates. That is the ADR 0026 D5
    invariant this test asserts.
- **The data check reads the master** (`:149-151`), and the victim is chosen among pods not
  labelled master (`:220-221`; the label is state, not identity — see ~~Adjacent findings~~
  *(2026-09-27: Appendix A1)*) (read).

**5 — topology abandon: fine, by effect.**

- `:251` requires the promoted master to report the replica `state=online`. `:252` requires
  `master_link_status:up` on pod-0.
- The old pod-0 is jammed (masterauth poison plus `REPLICAOF` to the black hole, `:327-339`) and
  cannot meet either.
- `:255` reads `abandon:key2`, which was written after the jam, so only the replacement can hold
  it. The comment at `:243-250` gives this reasoning (read).
- The phase wait at `:257` is already met before the delete (`:185`). It is harmless, because the
  gates before it prove the replacement.

**Beyond the grep.**

- **Evictions** (`pdb_test.go`, `evictPod` `:321-325`) are deletes that the grep did not match.
  - The StatefulSet waits at `:102`, `:110` and `:274` can be met by the evicted pod while it
    terminates, so they are vacuous.
  - Every attempt, however, starts with `DisruptionsAllowed > 0` (`:251-253`), and the budget
    excludes terminating pods. The assertions are therefore fine (read).
  - *(Added 2026-09-27: the comments at `:100-101` and `:272-273` promise a recovered, full pod
    set that the wait does not guarantee; Appendix A2.)*
- **Four delete sites already wait by UID** (read): `pod_security_test.go:242→249`,
  `sentinel_peer_table_test.go:76→77`, `splitbrain_test.go:92→101` and
  `split_brain_dataset_test.go:110→115`.
- `pod_hardening_test.go:152` deletes its own probe pod as cleanup.
- **Inventory of every pod delete and eviction** *(added 2026-09-27, grep for `.deletePod(`,
  `Pods(...).Delete(` and `evictPod(` in `test/e2e` at `84a39c2`)*: 14 call sites — the 10
  `deletePod` calls in 8 files, the direct `Delete` at `pod_termination_test.go:128` and
  `pod_hardening_test.go:152`, and `evictPod` at `pdb_test.go:256` and `:308`; plus the
  `deletePod` body itself ([`sidecar_test.go:68-74`](../../test/e2e/sidecar_test.go), a plain
  `Delete` with default options).
- **Chronology since the identity helper** *(added 2026-09-27, `git log`, `git blame`)*.
  `waitForPodRecreated` landed in `cea8222` (2026-08-22 16:57). Every delete site written after
  it waits by identity or observes termination on purpose: `sentinel_peer_table_test.go:76→77`
  (`1b1f6ed`, 2026-08-23), `pod_termination_test.go:128` (`360cb03`, 2026-08-25),
  `split_brain_dataset_test.go:110→115` (`2051a34`, 2026-08-26), `pod_security_test.go:242→249`
  (`bb6c78f`, 2026-09-26). Both vacuous sites predate it (`sidecar_test.go` added in `ce97f1b`,
  2026-02-28; `sentinel_stale_master_test.go` in `a697587`, 2026-03-19), and so do the PDB waits
  (`604cd91`, 2026-08-19; `759ae3b`, 2026-08-20). **One miss after the helper existed:**
  `1422705` (2026-08-22 21:28, 4.5 h after `cea8222`) rewrote the post-delete subtest "rw service
  points to new master after failover" of `TestE2E_SidecarFailoverDrainMaster` to fix a sampling
  flake and left the vacuous waits above it in place (`git show 1422705 --
  test/e2e/sidecar_test.go`, hunk `@@ -372`); that is the instance that went red on 2026-09-26.

**Origin (read, `git log`).** All five audited files come from `main`: added between
`ce97f1b` (2026-02-28) and `360cb03` (2026-08-25). Between the merge base ~~`9925539`~~
*(corrected 2026-09-26, adversarial check: that is the stale local `main` ref; the merge base
with `origin/main`, merged in by `e2ce8bb`, is `e3e869d`, two dependency commits later that touch
no e2e file)* `e3e869d` and `HEAD`, only the drain-master hunk of `sidecar_test.go` changed among
the audited files and the helpers they call (`e2e_test.go`, `rolling_update_test.go`,
`standalone_test.go`, `pdb_test.go`). The class is **pre-existing on `main`**.
The one instance on `feat/rootless` is the fixed one, and its comment carries the wrong
citation. *(2026-09-27: `feat/rootless` is merged, `ad81a47`, and the citation is fixed,
`f5c6886`.)*

### Measured 2026-09-27: a forced Sentinel failover on the two pinned lines (docker)

Four runs, two by the auditor of this re-verification and two by its facts check, each on a
docker network with one master, two replicas and three Sentinels on the same image, configured
as the operator configures them (`sentinel monitor mymaster <m> 6379 2`,
`down-after-milliseconds 5000`, `failover-timeout 60000`, `parallel-syncs 1`,
`resolve-hostnames yes`, `announce-hostnames yes`), then `SENTINEL FAILOVER mymaster` on
Sentinel 1 and a sample of every node's `ROLE`, every Sentinel's `config-epoch` and master
address (the `ip` field of `SENTINEL MASTER mymaster`, not `get-master-addr-by-name`) about
every 1.1 s. Script: `sentinel_epoch.sh <image>
<tag>` in the run's scratchpad (`work/t034/`, a copy under `work/t034-skeptic/`; volatile).
Containers named `vko-verify-034-*` and `vko-verify-034sk-*`, all removed afterwards
(`docker ps -a` shows none).

| | `valkey/valkey:9.1.1` (auditor) | `valkey/valkey:8.1.9` (auditor) | `9.1.1` (check) | `8.1.9` (check) |
|---|---|---|---|---|
| `config-epoch`, three Sentinels, before / t+0.1 s / next sample | 0 / 0 / 1 at t+1.3 s | 0 / 0 / 1 at t+1.2 s | 0 / 0 / 1 at t+1.2 s | 0 / 0 / 1 at t+1.2 s |
| Promoted replica answers `master` | t+0.1 s | t+1.2 s | t+1.2 s | t+1.2 s |
| Followers switch the `SENTINEL MASTER` `ip` | t+1.3 s | t+1.2 s | t+1.2 s | t+1.2 s |
| Leader switches the `SENTINEL MASTER` `ip` | between t+5.8 and t+7.0 s | between t+5.8 and t+6.9 s | between t+4.7 and t+5.9 s | between t+5.8 and t+7.0 s |
| Old master: last `master` / first `slave` | t+10.5 s / t+11.6 s | t+10.3 s / t+11.5 s | t+10.5 s / t+11.7 s | t+10.4 s / t+11.6 s |
| `master_replid` before → after, on all three nodes | `3ff1b0389b0c` → `7e9b1eb79542` | `18f097593051` → `3076ccc9d109` | `0a99d46726b4` → `bafbdd043e58` | `603b6ccdad27` → `509151b6a3d4` |

What this establishes, on both pinned lines:

- `config-epoch` in the `SENTINEL MASTER` reply is Sentinel's failover counter: it rose from 0 to
  1 on **every** Sentinel within 1.3 s of the failover, and read 0 everywhere, leader included,
  at t+0.1 s. It changes only once the promoted replica reports master (the `sentinel.c` copy,
  `:2612-2619`; a new monitor record starts at 0, `:1319`).
- The old master keeps answering `role:master` until t+10.3–10.5 s and is a replica from
  t+11.5–11.7 s, so the two-master window, which opens when the promoted replica answers master
  (t+0.1–1.2 s), closes between t+10.3 s and t+11.7 s. That the close follows Sentinel's regular
  INFO refresh of about 10 s is an inference, not read.
- The leader's `SENTINEL MASTER` `ip` switched 3.5–5.8 s after the followers'. T35 records the
  same field lagging 1.07–1.11 s in its docker runs and 1.08 s and 5.38 s on Kind, and ties the
  leader's switch to the end of `RECONF_REPLICAS` (every replica's link up); why these runs lagged longer, with the old master
  still alive, was not examined. `get-master-addr-by-name`, the command the data init and site 2
  use, was not sampled here; for it the leader switches first (site 2 above, T35).
- `master_replid` changes on every node, on the old master only once it converts; during the
  two-master window the old master still carries its old replid.
- Not measured here: a failover triggered by a pod delete on Kind (the docker runs keep the old
  master alive and force the failover), and any Sentinel that is partitioned or restarting.

**Verified:**

- **read:** every file:line above at `a8e8931`; the upstream StatefulSet status computation and
  the PDB health count (v1.36.4); `git log` and `git diff` for the origin.
- **run:** the timings, all taken from the logs named.
  - The drain-master red run and the 17-leg timings of sites 1 and 2 (`final6.log` and the
    others above).
  - The Kind experiment (`drainexp.log`) and the CI legs of the fix.
  - The three measured-false sentences, against the CI logs.
  - *(Added 2026-09-26, adversarial check.)* `initialMaster`/deleted replica of site 1 in all
    17 legs; the first master count of all 10 unfixed experiment runs; the replacement's first
    watcher answer in all 18 experiment runs; the per-leg top-level test count of the six CI
    legs.
- **read (added in the adversarial check):** the four-node local cluster behind the three
  "single-node" records (`final6.log:7-20`, the cluster creation it logs; `Makefile:181-185`);
  testify's first immediate evaluation
  (`assertions.go:2023-2024`, v1.12.1); the drain handler's role check and Sentinel failover
  (`internal/sidecar/drain.go:114`, `:128-129`); `git blame` of the three measured-false
  sentences; the merge base with `origin/main`.
- The CI legs ran Kubernetes 1.33.4 on one node and the local legs 1.36.1 on four, and the
  vacuous path shows on both (run). *(2026-09-27, read:
  [`release.yml:23`](../../.github/workflows/release.yml) `KUBERNETES_VERSION` 1.33.4, workers 0
  on the single-node legs; `Makefile:181-185` writes control-plane + 3 workers.)*

**Not verified:**

- No test was executed for this file, and none in the adversarial check.
- Which process answered each exec in the red run, and whether its new master held every key.
- ADR 0017 D50's statement that the red cluster's operator log shows no action.
- ~~The EndpointSlice readiness of a terminating pod in upstream source.~~ *(corrected
  2026-09-27: read upstream, site 1 above.)*
- Whether a replacement data pod can still briefly answer `role:master`, as the comment at
  `sidecar_test.go` ~~`:314-316`~~ *(corrected 2026-09-27: `:315-317`)* says. The watcher saw `role:slave` on the first answer ~~in three
  runs, at about 1 s resolution~~ *(corrected 2026-09-26, adversarial check)* in all 18
  experiment runs, sampled about every 2 s per pod; a shorter window before its first sample
  is not excluded. *(Added 2026-09-27, read, a candidate path and not an observation:)* the
  master drain sends `SENTINEL FAILOVER` to the first Sentinel in ordinal order that accepts it
  ([`drain.go:137-145`](../../internal/sidecar/drain.go); order built at
  [`statefulset.go:1168-1187`](../../internal/builder/statefulset.go)), which makes that Sentinel
  the leader, and the replacement's init asks the Sentinels in the same order and boots as
  master when the answer names itself
  ([`statefulset.go:295-335`](../../internal/builder/statefulset.go)). The init asks `SENTINEL
  get-master-addr-by-name` (`statefulset.go:297`), for which the leader switches first, at
  `+promoted-slave` (site 2 above, T35); the leader lag measured below concerns the `ip` field of
  `SENTINEL MASTER` and does not apply. The window that matters is therefore
  the pre-promotion window, in which every Sentinel still names the old master — the pod being
  replaced. On Kind it lasted about 1.1 s after `+try-failover` (`drain-orig-1`, above), and the
  draining master does not exit before its role changes (`sentinelFailover` returns through
  `waitForRoleChange`, [`drain.go:144`](../../internal/sidecar/drain.go)), so a replacement's
  init is expected to run after it closed; the path is unlikely on Kind (inference). What would
  decide it: a Kind run that logs the init's "This pod IS the master" line next to the Sentinel
  `+promoted-slave` times.
- That a replica-drain regression would be a forced `SENTINEL FAILOVER` as fast as the master's
  drain: inferred from the master-drain experiment, never run with a replica.
- In the 11 local legs, the time from the delete to site 2's `:137`: the logs carry no
  timestamps.
- The flake paths marked as hypotheses: none was observed in 17 legs.

**Verified 2026-09-27 at `4a7543e` (read, no test run):**
- sites 1 and 2 still carry no UID capture and no `waitForPodRecreated`: site 1 deletes at
  `sidecar_test.go:485`, site 2 at `sentinel_stale_master_test.go:125-128` and logs at `:137`;
  the only UID wait in `sidecar_test.go` is the drain-master one (`:272`, `:283`);
- helpers unchanged: `waitForStatefulSetReady` `e2e_test.go:147-163`, `waitForPodReady` `:285`,
  `waitForPodRecreated` `:307-340`, `getPod` `:604-611`, `deletePod` `sidecar_test.go:68-74`,
  `findMasterPod` `rolling_update_test.go:733-750`; `testTimeout` is 5 min and `pollInterval`
  2 s (`e2e_test.go:37`, `:46`), so each `waitForPodRecreated` has its own 5 min budget;
- `deletePod` has 10 call sites in 8 files (grep);
- the open item is still recorded as open: ADR 0017 D50 `:616-617` ("The fixture fix is open"),
  the index row [`docs/adr/README.md:109`](../adr/README.md), `CLAUDE.md:282-284`;
- `git grep -nw T34` outside `docs/tickets/`: ten citation lines, `CLAUDE.md:284`, `:1047` and
  ADR 0017 `:114`, `:119`, `:136`, `:584`, `:593`, `:604`, `:611`, `:612` — all on
  [040](040-tracked-files-cite-work-items-instead-of-adrs.md)'s work list, and part of this
  ticket's close;
- the doc half's `make lint` result says nothing about `sidecar_test.go`: e2e files carry the
  `e2e` build tag, which neither `go vet` nor golangci-lint passes
  ([T43](043-lint-and-vet-skip-every-build-tagged-test-file.md)); only `gofmt -l .` sees them
  *(precised 2026-09-27, cross-ticket from T43: and it cannot fail on them - `Makefile:84` lists
  an unformatted file and exits 0, so `make lint` gates nothing in `test/e2e`)*;
- for D2 of Options *(D2 dissolved at `84a39c2`; this now backs site 1's effect read in the Work
  list)*: Sentinel's `SENTINEL MASTER` reply carries `config-epoch` for a master, and
  a failover raises it — `failover_epoch = ++sentinel.current_epoch` in `sentinelStartFailover`,
  copied into `config_epoch` once the promoted replica reports master, and spread to the other
  Sentinels by hello messages (read in the Valkey `sentinel.c` copy of the earlier session's
  scratchpad, `:3419-3420`, `:4939-4944`, `:2619`, `:2853`; release not recorded). No e2e reads
  `config-epoch` or `master_replid` today (grep); `sentinelPeerCount`
  (`sentinel_peer_table_test.go:134-149`) parses the same reply.

~~**Not verified 2026-09-27:** no test was run; that the Valkey 8.1.9 and 9.1.1 pins behave as that
`sentinel.c` copy does; that `master_replid` changes on a promotion (PSYNC2), still a hypothesis.~~
*(corrected 2026-09-27 at 84a39c2: both are measured on 9.1.1 and 8.1.9, "Measured 2026-09-27"
above. Still no e2e, unit or integration test was run.)*

**Verified 2026-09-27 at `84a39c2` (re-verification; read unless marked):**

- Everything in the block above still holds at `84a39c2`; `test/e2e` differs from `4a7543e` only
  by one removed comment line in `migrate_e2e_test.go`.
- The only callers of `waitForPodRecreated` are `pod_security_test.go:249`,
  `sentinel_peer_table_test.go:77`, `split_brain_dataset_test.go:115`, `sidecar_test.go:283` and
  `splitbrain_test.go:101` (grep).
- The unit tier catches the outright removal of the replica role check: in
  `TestDrainHandler_ReplicaExitsImmediately`
  ([`drain_test.go:136-147`](../../internal/sidecar/drain_test.go)) a replica must produce no
  label patch, and the patch at [`drain.go:120`](../../internal/sidecar/drain.go) precedes both
  failover paths (`:128-131`). Every replica-role unit test runs with `sentinelEnabled` unset
  (`newTestDrainHandler`, `drain_test.go:111-131`, defaults it to false); the sentinel-enabled
  tests start as master and set replica only to simulate the role change. So no unit test runs a
  replica drain on the Sentinel path, and none asserts on the failover calls of a replica.
- `resetSentinelState`, the one path that re-MONITORs a Sentinel and so restarts its
  `config-epoch` at 0, is called only from the roll's state machine
  ([`rolling_update.go`](../../internal/controller/rolling_update.go) `:958`
  `checkFinalizationTopology`, `:1007` and `:1049` `syncSentinelWithMaster`, `:3161`
  `handleMasterWithNoReplicas`, `:3308` `handleNoMasterFound`); site 1 runs no roll.
- `E2E Tests` is a required context through repository ruleset `23985346` on `main`, one of 12
  contexts matching ADR 0017 D47 (`gh api repos/guided-traffic/valkey-operator/rules/branches/main`);
  the classic protection endpoint
  (`gh api .../branches/main/protection/required_status_checks`) answers "404 Branch not
  protected".
- The shared wait helpers have 120 call sites in 22 files: 103 `waitForStatefulSetReady` and 17
  `waitForPodReady` (`grep -rn "\.waitForStatefulSetReady(" test/e2e`, same for
  `.waitForPodReady(`). No test holds a pod in termination on purpose: a grep for finalizers,
  `GracePeriodSeconds` and `terminationGracePeriod` in `test/e2e` finds only a comment
  (`rolling_update_test.go:626`) and the assertion of the 75 s grace (`sidecar_test.go:209-211`).
  The longest termination the operator sets is the 75 s data grace, the drain `preStop` capped
  at 60 s (ADR 0026:164-170).
- The drain `preStop` exists only on multi-replica clusters without Sentinel
  ([`statefulset.go:746-749`](../../internal/builder/statefulset.go), `drainSignalVolumes`
  `:678-680`): on the Sentinel clusters of sites 1 and 2, `valkey-server` gets SIGTERM at once.

**Not verified 2026-09-27 at `84a39c2`:** no e2e, unit or integration test was run, and no Kind
cluster; the site 2 claim that the old Sentinels stop answering within a second (one CI operator
log, not re-read); the flake hypotheses of sites 1 and 2; whether any of the 120 calls of the
shared wait helpers observes a transient that a later return would miss (not audited per call;
option E below depends on it); the exact upstream line numbers of `podToEndpoint`.

## Impact

- **A required check can go red for nothing.**
  - `E2E Tests` (`e2e-gate`) is a required context (ADR 0017 ~~:527-534~~ *(corrected
    2026-09-27: `:533-540`)*, D47, read). *(2026-09-27: required through repository ruleset
    `23985346`, not classic branch protection; Verified at `84a39c2`.)*
  - A red run of this class ~~blocks every merge~~ *(corrected 2026-09-26, adversarial check)*
    fails that required check and holds the PR it ran on until a rerun. It has happened once,
    in a local run and not in CI.
  - Sites 1 and 2 carry the same exposure, with no failure observed in 17 legs.
- **Two tests do not guard what they name.**
  - Site 1 asserts "no master failover" and "recreated replica labelled" within about 0.7 s of
    the delete, before the replacement can exist (inference, above). Its no-failover check
    could not fail even with an early promotion elsewhere, because `sc-repdr-0` answers first.
  - ~~No other e2e test deletes a replica and then checks that the master did not change (read:
    the delete sites above). A regression in which a replica drain triggers a failover therefore
    passes the suite (inference; no such regression was run).~~ *(corrected 2026-09-27 at
    84a39c2: narrower than stated.)* The unit tier fails if the replica role check at
    [`drain.go:114`](../../internal/sidecar/drain.go) is removed outright
    (`TestDrainHandler_ReplicaExitsImmediately`, Verified at `84a39c2`). What passes both tiers
    today is a regression confined to the Sentinel path of the drain handler (no unit test runs
    a replica drain with `sentinelEnabled`), a role misread against a real Valkey, and a failover
    from any other source after a replica delete. Site 1's e2e is the only test of a replica
    drain on a Sentinel cluster, and it is vacuous (inference; no such regression was run).
  - Site 2's guard of the stale-master regression holds only because the old Sentinels stop
    answering within about a second of the delete (inference from one CI operator log, above).
- ~~**Tracked files are wrong about the fix.** Six sentences are false — three about how far
  the fix was verified (against the CI runs), three about the red run's cluster (against its own
  log) — and three are stale. One code comment cites the wrong decision; the same comment carries
  one of the false "single-node" records.~~ *(corrected 2026-09-27 at 84a39c2: all nine
  sentences and the comment were corrected in `f5c6886`. What is false in tracked files today is
  test code at the two open sites, next bullet.)*
- **Tracked test comments are false.**
  - [`sidecar_test.go:523-524`](../../test/e2e/sidecar_test.go) names `valkeyExecAllowError`;
    `:526` calls `valkeyExecQuick` (false as read since `eba7e3d`).
  - [`sidecar_test.go:487`](../../test/e2e/sidecar_test.go) ("Wait for cluster to recover (all 3
    pods ready)") and
    [`sentinel_stale_master_test.go:130`](../../test/e2e/sentinel_stale_master_test.go) ("Wait for
    all pods to come back.") promise a wait the timings refute, and the log line at
    `sentinel_stale_master_test.go:137` says "All pods restarted and ready" 0.34–0.62 s after the
    delete in 5 of 6 CI legs.
- **Security: none.** No guard involved is a security control, and no principal gains anything
  from a vacuous test.

## Options

Only test code and CI change under every option. The operator does not change, nothing rolls,
no production cluster (the Flux-managed namespaces, the Chaos Mesh schedule) is affected, and no
standing operator invariant is touched. One decision is open.

### D1 — how the fixture half is closed: the shared waits refuse a terminating pod, or per-site UID waits

**What the code does today.** Site 1 deletes at
[`sidecar_test.go:485`](../../test/e2e/sidecar_test.go) and waits with
`waitForStatefulSetReady` and a phase wait (`:488-489`). Site 2 deletes six pods at
[`sentinel_stale_master_test.go:125-128`](../../test/e2e/sentinel_stale_master_test.go), waits
with `waitForStatefulSetReady` twice and `waitForPodReady` six times (`:131-136`) and logs at
`:137`. The terminating pods satisfy all of them: `waitForStatefulSetReady` compares
`ReadyReplicas` alone ([`e2e_test.go:160`](../../test/e2e/e2e_test.go)), which counts terminating
pods (`stateful_set_control.go:378`, v1.36.4), and `waitForPodReady` reads a name and the
`PodReady` condition only ([`e2e_test.go:285-305`](../../test/e2e/e2e_test.go)), which kubelet
keeps True for the whole termination (ADR 0026). The identity wait exists
(`waitForPodRecreated`, [`e2e_test.go:316-340`](../../test/e2e/e2e_test.go)), five delete sites
use it, and its own comment names the trap: "the obvious wait is not one" (`:310`). ADR 0017 D50
requires identity — image, UID or `deletionTimestamp` — wherever "which pod" is part of the
assertion ([ADR 0017:568-572](../adr/0017-test-and-ci-policy.md)).

**What the choice changes.** Whether the identity rule is enforced where the trap lives — the
two shared helpers every fixture reaches for after a delete — or per site at the two known
sites. **What it does not change.** Site 1's effect read (below, needed under both options);
sites 3 to 5, which are fine; the phase waits for `OK`, which the previous status can still meet;
a fixture that execs by name with no wait at all; the wording of ADR 0017 D50's rule.

- **E — the shared readiness waits refuse a terminating pod (recommended).**
  `waitForStatefulSetReady(t, ns, name, n)` keeps its `ReadyReplicas == n` check and additionally
  requires that each pod `<name>-0` … `<name>-(n-1)` exists, is Ready and carries no
  `deletionTimestamp` — one pod List per poll next to today's StatefulSet `Get`, filtered by
  ordinal, not one request per pod. `waitForPodReady` returns false while the pod carries a `deletionTimestamp`.
  This is the test-tier counterpart of ADR 0026's `available()`, and it uses `deletionTimestamp`,
  one of the identity signals D50 names.
  - *Cost:* about 15 lines in `e2e_test.go`. The meaning of all 120 calls in 22 files changes;
    only a call that today returns while a pod of its range terminates now waits, for at most the
    termination (75 s data grace, 30 s Sentinel — `sentinel.go:391`, ADR 0026:169-170 — within
    the 5 min `testTimeout`). The range is `[0, n)` and the `ReadyReplicas == n` check stays, so a
    caller with a smaller `n` checks exactly the ordinals its StatefulSet has: at creation
    (`pdb_test.go:154`, `:197`, `admission_recovery_test.go:459`) and after the scale-up from 1 to
    2 (`admission_recovery_test.go:505`). No e2e scales a StatefulSet down: the only `replicas`
    change through `patchValkeySpec` is that scale-up (`admission_recovery_test.go:471-472`,
    grep at `84a39c2`). The
    admission site cannot hang: its only StatefulSet wait after the deletes (`:310`) runs after
    the webhook is removed (`:297-306`).
  - *Consequences:* site 2 is fixed with no edit at the site — the waits at `:131-136` then wait
    for all six replacements, and the comment at `:130` and the log at `:137` become true. Site 1's
    `:488` waits for the replacement, so the label subtest (`:505`) and the replication subtests
    read it and the comment at `:487` becomes true; site 1 still needs its effect read. The PDB
    waits at `pdb_test.go:102`, `:110` and `:274` become the recovery their comments promise
    (Appendix A2). A future delete followed by the usual wait is covered without anyone knowing
    the rule. Risk: a test that samples a transient right after one of these waits may now sample
    it later; whether any of the 120 calls does was not audited per call (Not verified at
    `84a39c2`). The proof is the full suite on both Valkey lines, which the `E2E Tests` check runs
    on the fix commit under either option, plus the revert check of Verification.
- **A — per-site UID waits at sites 1 and 2 (runner-up).** Site 1 captures the UID before `:485`
  and calls `waitForPodRecreated` before `:488`; site 2 captures six UIDs before `:125` and calls
  `waitForPodRecreated` six times before `:137`.
  - *Cost:* about 15 lines in two test files, the same runs as E.
  - *Consequences:* closes both known sites with the helper that already exists and touches
    nothing else — no other test's timing moves. The shared waits stay vacuous after a delete, so
    every future delete relies on review against D50, and the PDB comments stay untrue (their
    assertions stay sound).

**Recommendation: E.** Checkable reasons: (1) the class lives in the helpers, not at the sites —
every delete site written before `waitForPodRecreated` that was not safe by construction reached
for `waitForStatefulSetReady` and was vacuous (`splitbrain_test.go:92` before `cea8222`, the
drain-master test, sites 1 and 2), and the helper's own comment says so; (2) the per-site
convention was already missed once after the helper existed: `1422705`, 4.5 h after `cea8222`,
edited a post-delete subtest of the very test that went red on 2026-09-26 and kept its vacuous
waits (Beyond the grep); (3) E fixes site 2 without touching it and costs no more code than A;
(4) the project already chose this shape for the same class in operator code — ADR 0026 fixed
"a pod being deleted is not available" at the accessor because "the rule is the rename, not a
list of sites: it had been stated as a list three times and been incomplete every time"
(`CLAUDE.md:594-595`). ADR 0026's refusal to fold `deletionTimestamp` into readiness
(ADR 0026:201-208) rests on `demoteRogueMaster`, which must still reach a terminating master; a
test wait that says "the cluster has recovered" has no such need.
**Why E beats A:** A closes the two known sites and leaves the trap armed for the next delete,
which D50 alone did not prevent (`1422705`). A's one advantage is locality — no other test's
timing moves — which makes it the right choice if the owner will not accept a change of meaning
across 120 calls; that risk is what the full suites on both lines bound, and they run under A
too.
**Dissent recorded.** The auditor and the facts check of this re-verification recommended A
before E was proposed, on the chronology above: every delete site written after `cea8222`
complies, so the per-site convention has held for new sites. The facts check itself weakened
that argument with `1422705` (an edit after an existing delete, not a new site), and the design
check proposed E. The owner decides between them; the mark on E rests on reasons (1) to (4).

### Site 1's effect read is no longer a decision

Until this re-verification it was D2. Of its three reads, two were removed (History
2026-09-27), and one read with one answer is work, not a decision: it is in the Work list under
both D1 options. The mechanism it rests on: after an identity wait, site 1 must read back that
deleting a replica triggered no failover. `findMasterPod` cannot, because it asks ordinal 0
first and `sc-repdr-0` is the master by construction, and an old master keeps answering
`role:master` for 10.3–10.5 s after a failover (measured). Sentinel's `config-epoch` rises on
every Sentinel within 1.3 s of a completed failover (measured), nothing on site 1's path resets
it (`resetSentinelState` runs only in rolls), and a replica that briefly answers master raises
none. The read is placed directly after the identity wait and **before** any
`waitForReplicaSynced(replicaPod)`: if a failover promotes `replicaPod`, that wait
([`e2e_test.go:509-518`](../../test/e2e/e2e_test.go), a 2 min `require.Eventually` on
`master_link_status:up`) would fail first and kill the regression — or the mutation — on the
wrong assertion (ADR 0017 D9, D10).

## Decision

**Not decided.** D1 is open. *(2026-09-27: the doc half landed in `f5c6886`, `feat/rootless` is
merged in `ad81a47`, and D2 dissolved; the superseded recommendation text is in History
2026-09-27.)*

## Work list

~~*(Added 2026-09-27.)* **No XS item needs no decision**: every open item is the fixture half,
which waits on D1, and site 1's effect read waits on D2.~~ *(corrected 2026-09-27 at 84a39c2:
the comment at `sidecar_test.go:523-524` is an XS fix that needs no decision, and site 1's effect
read needs none since D2 dissolved.)*

- [ ] **No decision, XS — the false helper name:** change the comment at
  [`sidecar_test.go:523-524`](../../test/e2e/sidecar_test.go) to name `valkeyExecQuick` (its
  reason, a transient exec failure, stays). Clears one of the rule 1 matches.
- [ ] **Waits on D1 — the fixture half.** Under E: the two helpers in
  [`e2e_test.go`](../../test/e2e/e2e_test.go) (`:147-163`, `:285-305`) as described in Options;
  no edit at site 2. Under A: the UID captures and `waitForPodRecreated` calls at
  `sidecar_test.go:485`/`:488` and `sentinel_stale_master_test.go:125`/`:137`. Either way the
  comments at `sidecar_test.go:487` and `sentinel_stale_master_test.go:130` and the log at `:137`
  become true, which clears the remaining rule 1 matches.
- [ ] **No decision, lands with the D1 fix — site 1's effect read** (replaces `:492-494`): before
  the delete, read `config-epoch` from `SENTINEL MASTER` on all three Sentinels (reuse the
  key/value walk of `sentinelPeerCount`,
  [`sentinel_peer_table_test.go:134-149`](../../test/e2e/sentinel_peer_table_test.go)). Directly
  after the identity wait, and before any `waitForReplicaSynced(replicaPod)`: assert the epoch
  unchanged on all three (one read each, not a poll — a poll "until unchanged" would be vacuous
  for a negative check), then poll every ordinal, bounded, until exactly one pod answers
  `role:master` and that pod is `initialMaster`. Keep the replica sync in the replication subtest,
  where it is. About 20 lines. Asking all three Sentinels is not needed for propagation (the epoch
  reached all three within 1.3 s, the identity wait takes at least about 6–8 s) but covers a
  Sentinel that is partitioned or restarting, at no cost. What it does not see: a failover that
  started and aborted (changes nothing for clients), and a non-Sentinel promotion that Sentinel
  reverts before the read, which no code path on a Sentinel cluster performs today.
- [ ] **Runs:** on Kind, `make test-e2e E2E_RUN='TestE2E_SidecarDrainReplica|TestE2E_SentinelStaleMaster'`
  on both Valkey lines; the revert checks and the mutation of Verification; under E, the full
  suite on both lines (the `E2E Tests` legs of the fix commit do this); then both single-node CI
  legs with the durations recorded.
- [ ] **Close ([ADR 0034](../adr/0034-tickets-are-work-lists-that-get-archived.md)):** ADR 0017
  D50 (`:606-617`, "The fixture fix is open") and its Status with the date — under E, D50 also
  records that the shared waits refuse a terminating pod; the index row
  [`docs/adr/README.md:109`](../adr/README.md) (drop "D50 (the fixture fix for two vacuous
  sites)"); `CLAUDE.md:282-284` ("two are vacuous"); rewrite the ten T34 citation lines listed under
  Verified 2026-09-27 to cite ADR 0017 D50 (shared with ticket 040's list); `git grep -nw T34`
  outside `docs/tickets/` returns nothing; then `git mv` to [archive/](archive/).

## Verification

Done when every line holds, with the command and date recorded here:

- [x] **Doc half.** Done 2026-09-26 in `f5c6886`, pushed. `git grep -n "single-node Valkey 9" -- docs test CLAUDE.md` returns nothing (the struck records read `~~single-node~~ Valkey 9`); `git grep -n "D50, D51" test/` returns nothing; every remaining hit of "no full suite has run" and "not yet audited" outside `docs/tickets` is struck; T31:948 and :975-976 corrected ~~(untracked)~~ *(corrected 2026-09-27: tracked as `archive/031` since `4a7543e`; its `:798-799` is left as history, Fact)*; `make lint` 0 issues *(precised 2026-09-27: that run checked only the formatting of `sidecar_test.go` — `go vet` and golangci-lint skip every `e2e`-tagged file, [T43](043-lint-and-vet-skip-every-build-tagged-test-file.md))*. *(Re-run 2026-09-27 at `84a39c2`: the first grep returns nothing; `SECURITY_ARCHITECTURE.md` no longer exists and was dropped from the command, `docs` covers its successor.)*
  - ADR 0017:130, :595 and `CLAUDE.md:975` are superseded in place, each naming the CI legs.
  - ADR 0017:112, :576 and `sidecar_test.go:281` no longer call the red run single-node, and
    `git grep -n "single-node Valkey 9" -- docs test` returns only superseded (struck) text.
  - `CLAUDE.md:222-223`, `SECURITY_ARCHITECTURE.md:1578` and ADR 0017:117 name the audit
    result.
  - `git grep -n "D50, D51" test/` returns nothing.
  - T31:948 and :975-976 ~~(untracked)~~ carry the same corrections.
- [ ] **Comment fix.** `git grep -n "valkeyExecAllowError" test/e2e/sidecar_test.go` no longer
  hits `:523-524`.
- [ ] **Site 1.**
  - The delete subtest takes at least the replacement time in both single-node legs of one CI
    run of the fix commit, with the durations recorded.
  - Revert check: with the identity wait removed in a scratch copy (under E the strictness of the
    two helpers, under A the UID wait), the delete subtest returns in under 1 s again.
  - ~~The new no-failover assertion fails in a scratch copy that issues `SENTINEL FAILOVER` after
    the delete, so it can fail (ADR 0017's revert rule).~~ *(corrected 2026-09-27: a failover
    issued right after the delete can pick the replica that is going away — Sentinel clusters
    have no drain `preStop`, so its `valkey-server` gets SIGTERM at once — and the mutation could
    then survive for the wrong reason; hypothesis, unverified.)* Mutation: in a scratch copy,
    issue `SENTINEL FAILOVER` after the identity wait with both replicas healthy, wait 2 s (the
    epoch reached every Sentinel within 1.3 s), then run the effect read. The subtest must fail,
    and its failure message must be the `config-epoch` or the master-poll assertion, not a sync or
    readiness wait (ADR 0017 D9, D10).
- [ ] **Site 2.**
  - "All pods restarted and ready" is logged only after six new pods — new UIDs under A, no
    `deletionTimestamp` under E — and the time from the delete to that line is recorded in both
    legs.
  - Revert check as for site 1.
- [ ] **Under E:** the full suite green on both Valkey lines on the fix commit (the `E2E Tests`
  legs), with any test whose duration moved by more than the replacement time named here.
- [ ] `make lint`, `make cyclo`, and `E2E Tests` green in CI on the fix commit. As with ADR
  0017 D50, a green streak is not a failure rate. The revert checks are the proof. *(Precised
  2026-09-27: until T43 lands `make lint` checks only the formatting of e2e files, and
  `make cyclo` ignores `_test.go`; neither proves anything about this fix.)* *(corrected
  2026-09-27, cross-ticket from T43: not even the formatting - the `gofmt -l .` line at
  `Makefile:84` lists an unformatted e2e file and exits 0, so until T43 lands `make lint` checks
  nothing in `test/e2e` that can fail.)*

## Appendix — findings of the same family

~~## Adjacent findings~~

~~Not in scope and not filed.~~ *(corrected 2026-09-27: every finding is a file, a new ticket or
an appendix to the ticket of its family ([README](README.md) "The filing rule"). The items below
that fall under ADR 0017 D50's identity rule are this ticket's appendix; the one outside the
family was filed as T70, pointer below.)*

- **A1 — `pod_termination_test.go:220-221` excludes the master from the victim choice by the
  `instanceRole` label.** That label is the sidecar labeler's state, not identity. It chooses
  what to delete; it does not wait after a delete, so it is outside this class (read).
  *(Assessed 2026-09-27, hypothesis, not observed:)* D50's rule covers it all the same — "which
  pod" is part of that assertion. The label lags a promotion
  ([T35](035-master-records-lag-the-real-master.md)), so a victim picked right after the roll's
  own failover could be the just-promoted master. It is unlikely: the picker normally fires when
  the first replaced replica becomes Ready, well before the roll's failover. Neither D1 option
  changes it. No work item until a run shows it; what would: a Kind run that logs the victim, its
  `ROLE` answer and the roll's failover time.
- **A2 — the PDB test's recovery comments** *(added 2026-09-27, hypothesis, not measured)*.
  [`pdb_test.go:100-101`](../../test/e2e/pdb_test.go) ("Recover before the sentinel round so the
  sentinel budget starts from a full pod set") and `:272-273` ("Start the next attempt from a
  full, healthy pod set") rely on `waitForStatefulSetReady`, which a pod still terminating after
  its eviction satisfies. The assertions stay sound, because every attempt gates on
  `DisruptionsAllowed > 0` (`:251-253`) and the budget excludes terminating pods. Whether the
  evicted pod is ever still terminating when these waits run was not measured. Under D1 option E
  the comments become true with no edit; under A they stay as they are.
- **`SECURITY_ARCHITECTURE.md:1579` (since 2026-09-27
  [`docs/security/workload-pod-posture.md:253`](../security/workload-pod-posture.md#h-16)) says
  "Still locally, not in CI."** CI has run the hardening
  e2e since then (`e6a9d7c`), with the user-namespace half skipped by name. The sentence is
  ambiguous, and it belongs to T31's record, not this ticket's (read). *(Gone 2026-09-27: the
  body of gap [H-16](../security/workload-pod-posture.md#h-16) was replaced by a gap statement
  that names the CI legs and the skipped user-namespace half; the ambiguous sentence no longer
  exists.)*
- **ADR 0017:585 says the watcher recorded "once a second".** It sampled each pod about every
  2 s (log table above). Cosmetic; fold it into the doc half if that paragraph is touched
  anyway (run: `drain-orig-1/roles.txt`). *(Done 2026-09-26 in `f5c6886`: read at `4a7543e`,
  ADR 0017 `:592-593` strike "once a second" and say "about every 2 s per pod".)*
- ~~**The sibling in the integration tier** is
  [T33](033-integration-tests-read-the-cache-after-a-write.md): a read that a stale
  observer can satisfy.~~ *(2026-09-27: moved to Cross-ticket below.)*

**Outside this family — filed as [T70](070-tracked-comments-and-adrs-state-what-the-code-contradicts.md)**
*(2026-09-27)*: ADR 0026:164-165 says a replica delete releases the drain `preStop` hook in about
a second "on every topology", while the hook exists only on multi-replica clusters without
Sentinel; the correction is T70's item (f), where the analysis now lives. This ticket keeps only
the fact its own Site 1 mutation needs: Sentinel clusters have no drain `preStop` (Verification).

## Cross-ticket

*(Added 2026-09-27.)*

- **[T31](archive/031-generated-pods-run-as-root.md)** — tracked as `archive/031` since
  `4a7543e`, not untracked; its `:798-799` still says no full-suite run with the fix is recorded,
  left as history (Fact).
- **[T33](033-integration-tests-read-the-cache-after-a-write.md)** — the sibling in the
  integration tier: a read that a stale observer can satisfy. T33 keeps severity medium on a race
  reproduced in 2 of 4 runs; T34 is low on no observed failure at the remaining sites in 17 legs.
  The two differ on evidence, not on class.
- **[T35](035-master-records-lag-the-real-master.md)** — T35, re-verified the same day, splits
  the two Sentinel commands: for `get-master-addr-by-name` the leader switches first, at
  `+promoted-slave`; for the `ip` field of `SENTINEL MASTER` it switches last, when
  `RECONF_REPLICAS` ends (1.07–1.11 s docker, 1.08 s and 5.38 s Kind there). This ticket's
  figures are all the `ip` field: 1.0 s on Kind in `drain-orig-1` (old master deleted) and
  3.5–5.8 s in docker with the old master alive (Measured 2026-09-27). The docker figure is
  longer than any of T35's docker runs and unexplained; T35's table could carry it with its
  condition. *(2026-09-27, consistency pass: T35 now carries both figures, with their
  conditions, in its Impact bullet L.)*
- **[T36](036-non-persistent-master-restarts-empty.md)** — "Sentinel named the booting pod" has a
  concrete candidate path after a master drain: in the pre-promotion window every Sentinel still
  names the old master, which is the pod being replaced, and the replacement's init boots as
  master when the answer names itself (Not verified, above). The leader lag is not that path; the
  draining master waits for its role change before it exits, so the window is expected to close
  first. Not observed in 18 runs.
- **[T40](040-tracked-files-cite-work-items-instead-of-adrs.md)** — the ten T34 citation lines
  (`CLAUDE.md:284`, `:1047`; ADR 0017 `:114`, `:119`, `:136`, `:584`, `:593`, `:604`, `:611`,
  `:612`) are on T40's list. Whichever ticket closes first rewrites them to ADR 0017 D50.
- **[T43](043-lint-and-vet-skip-every-build-tagged-test-file.md)** — until it lands, `make lint`
  and `go vet` prove nothing about e2e files; this ticket's Verification says so.
- **[T62](062-resetsentinelstate-falls-back-to-sentinel-reset.md)** — `resetSentinelState`
  re-MONITORs every Sentinel, and a new monitor record starts at `config_epoch` 0, so a test that
  reads `config-epoch` across a roll must not assume it only grows. Site 1 runs no roll, so its
  effect read is unaffected.
- **[T68](068-the-e2e-exec-helpers-do-not-check-the-valkey-reply.md)** *(added 2026-09-27 as a
  T12 bullet in the consistency pass; the e2e reply check moved from
  [T12](012-no-write-fencing-min-replicas-to-write-as-an-opt-in-field.md) to T68 the same day)* —
  the e2e exec helpers do not check the Valkey reply; T68's fix edits `valkeyExec`
  (`e2e_test.go:225-268`) and `valkeyTLSExec`, in the file where option E edits the readiness
  waits; different functions, no conflict, and both need a full e2e suite on both Valkey lines,
  which one run can serve (T68 Work list item 7).

## History

- 2026-09-27: re-verified at 84a39c2 — every claim of the file re-checked against the tree, the
  still-present session logs, upstream source and docker; an auditor and two adversarial checks
  (facts, design), contested points re-read. No e2e, unit or integration test run.
  - **Checked and holding:** the mechanism (StatefulSet status, helpers, testify, PDB health,
    `waitForPodRecreated`), the drain-master red run, experiment and CI table (recounted), sites
    1–5 and their 17-leg timings (site 2's six CI timings recounted from timestamps), the four
    UID sites, the doc half of `f5c6886` (greps re-run), the ten T34 citation lines, the open item
    at ADR 0017 D50 `:616-617`, `docs/adr/README.md:109`, `CLAUDE.md:282-284`. Locations
    re-read at 84a39c2: `test/e2e` differs from `4a7543e` only by a comment line in
    `migrate_e2e_test.go`, so no `test/e2e` cite moved.
  - **Found false or outdated, corrected in place:** T31 called untracked and gitignored (tracked
    as `archive/031` since `4a7543e`; its `:798-799` left as history); the Impact line that a
    replica-drain failover regression passes the suite (the unit tier catches the outright
    removal of the `drain.go:114` check; what passes both tiers is a Sentinel-path-confined
    regression, a role misread, or a failover from another source); the Impact bullet "Tracked
    files are wrong about the fix" (fixed in `f5c6886`; what is false today are three test
    comments and a log line); "EndpointSlice readiness not read" and the `master_replid` and
    pins hypotheses (now read upstream and measured); the Decision section's recommendation
    (outdated); the Work list's "No XS item needs no decision"; the Adjacent findings heading
    "not filed" (against the filing rule); the site 1 mutation timing; the Verification grep path
    `SECURITY_ARCHITECTURE.md` (the file no longer exists; dropped from the command).
  - **New facts:** site 1's vacuity is structural (fresh master pod-0 by construction, victim
    the first non-master ordinal, `findMasterPod` ordinal-first; only an exec error on pod-0
    changes it) and fails ADR 0017 D10; three more false test comments
    (`sidecar_test.go:487`, `:523-524`, `sentinel_stale_master_test.go:130`); the chronology of
    delete sites since `cea8222`, with one miss after the helper (`1422705`); the 14-site delete
    and eviction inventory; 120 calls of the shared waits in 22 files; `E2E Tests` required
    through ruleset `23985346`; no drain `preStop` on Sentinel clusters; the Kind Sentinel
    timeline of `drain-orig-1`.
  - **Measured (docker, four runs on `valkey/valkey:9.1.1` and `8.1.9`, command and table under
    "Measured 2026-09-27"):** `config-epoch` 0 → 1 on all three Sentinels within 1.3 s; old master
    answers `master` until t+10.3–10.5 s; the `SENTINEL MASTER` `ip` of the followers switches at
    t+1.2–1.3 s, the leader's 3.5–5.8 s later; `master_replid` changes on every node. All
    `vko-verify-034*` containers removed.
  - **Review of this edit (same day, read at `84a39c2`):** spot-checked the helpers, sites 1 and
    2, the builder lines, `drain.go`/`drain_test.go`, `cea8222` and `1422705`, the 103 + 17 call
    count in 22 files, the grace periods, the ADR 0017, ADR 0026 and `CLAUDE.md:594-595` cites,
    the T46/T63/T62 precedents and the frontmatter YAML; all hold. Corrected: the leader-lag
    figures were applied to `get-master-addr-by-name` (the data init and site 2's `:167`), but
    they measure the `ip` field of `SENTINEL MASTER`; for `get-master-addr-by-name` the leader
    switches first (saved `sentinel.c` copy `:1659-1670`, `:3906-3915`; T35), so the candidate
    path of a replacement booting as master is the pre-promotion window, and the site 2 analogy
    does not apply. Also: the two-master window stated as its close (t+10.3–11.7 s), not a length;
    E's request cost is one extra List per poll, not "stays one"; the "scale-type" callers are
    creation and one scale-up, with no scale-down in the suite; the `deletionTimestamp` premise
    now cites `ObjectMeta` in `apimachinery@v0.37.1`; the Impact struck block rendered as one
    paragraph and is re-flowed; the auditor's and facts check's recommendation of A is recorded
    as dissent under D1; the stale "for D2 of Options" pointer is annotated. No docker container
    or network of this run remains (`docker ps -a`, `docker network ls`).
  - **Appendix and cross-ticket:** A1 (label-based victim, assessed as a hypothesis), A2 (PDB
    recovery comments, hypothesis); the ADR 0026:164-165 "on every topology" sentence recorded as
    a finding outside the family that needs its own ticket (not filed by this edit); a
    Cross-ticket section for T31, T33, T35, T36, T40, T43, T62.
  - **Options rewritten as one decision.** Removed:
    - **B** (A plus `deletePod` returning the UID, or a `replacePod` helper that deletes and
      waits, with all 10 call sites migrated) — dominated: the UID-returning variant saves one
      line per site and enforces nothing, the `replacePod` variant would hang for the 5 min
      `testTimeout` at `admission_recovery_test.go:237` (pod creation blocked at `:233`) and
      cannot serve `pod_hardening_test.go:152` (a probe pod nothing recreates), and a direct
      `Delete` bypasses either.
    - **C** (the doc half only, fixtures unchanged) — no longer a choice: the doc half landed in
      `f5c6886`, so C means doing nothing, and as a recorded refusal it would still owe ADR 0017
      D10 renames of site 1's two subtests.
    - **D** (A plus a unit-tier `go/parser` test over `test/e2e` that fails on a delete or
      eviction not followed by `waitForPodRecreated` or an `identity:` comment; proposed in the
      audit) — dominated by E: it checks that a justification comment exists at 6 of 14 call
      sites rather than behaviour, and the only shape it catches that E misses (a delete
      followed by a hand-written poll or a bare exec) exists nowhere, so it is speculative scope.
    - **D2 i** (`initialMaster` keeps its `master_replid`) — the old master keeps its replid for
      the whole measured two-master window, so it needs the master poll anyway and then adds only
      what `config-epoch` covers.
    - **D2 iii** (`findMasterPod` behind the UID wait) — probabilistic (ADR 0017 D9): locally the
      replacement is ready about 9 s after the delete, inside the 10.3–11.7 s two-master window.
    - **D2 as a decision** — with i and iii removed only ii remains, so the effect read moved to
      the Work list as decision-free work, reordered: epoch read and master poll directly after
      the identity wait and before `waitForReplicaSynced(replicaPod)`, which would otherwise fail
      first on a failover and kill the regression or the mutation on the wrong assertion.
    - Also dropped: the sentence "The filing bar allows this section" (the bar is retired;
      Options exist because a decision is open) and the accumulated review addenda.
  - **Recommendation changed: D1 A → E** (new option, from the design check: the shared waits
    refuse a terminating pod). Reason: the class lives in the helpers, the per-site convention
    was missed after the helper existed (`1422705`), E fixes site 2 with no edit and costs no more
    code, and ADR 0026 chose the accessor over a list of sites for the same class; A stays
    runner-up for an owner who will not accept a change of meaning across 120 calls.
  - **Superseded Decision-section text, moved here verbatim:**
    "**Recommendation: A, doc half first, before `feat/rootless` merges; B not taken.**
    - **Why the doc half first.** It carries the `now` urgency. It needs no design choice, only a
      go: the rule is to supersede in place (ADR 0017 D37).
    - **Why A for the fixtures.** It closes the guard gap of site 1 and removes site 2's dependence
      on Sentinel's exit speed.
    - **Why not B.** The four sites that already wait by UID show that the pattern works when it is
      used. ~~A structural guard is speculative until a third instance appears.~~ *(corrected
      2026-09-26, adversarial check: the count argument does not hold — with the drain-master test
      and sites 1 and 2 the class has three instances, four with the 2026-08-22 one that produced
      `waitForPodRecreated`.)* B still loses on what it buys: a helper that deletes and waits can
      be bypassed by calling `Delete` directly, as `pod_termination_test.go:128` does, so it makes
      the identity wait easier without making it required. Migrating the 10 call sites fixes
      today's instances, which A does per site, and prevents no future one. A guard that forces it
      would be a lint or a test over the e2e sources, which nobody has proposed."
  - **Frontmatter:** urgency `next` → `now` — rule 1's second clause matches (the false comments
    at `sidecar_test.go:487`, `:523-524` and `sentinel_stale_master_test.go:130`, and the `:137`
    log line), as T46, T63 and T62 apply it to code and test comments; the 2026-09-26 derivation
    ("The fixture half alone would not match rule 1", "Rule 1 no longer matches") tested only
    the first clause and missed these. Once the comment fix and the D1 fix land nothing false
    remains: rule 2 no, rule 3 no (severity low), rule 4 yes, so `later` — by then only the
    close is left. Severity `medium` → `low`: the only red of the class was the fixed instance,
    sites 1 and 2 show no failure in 17 legs, and the regression site 1 names is caught by the
    unit tier in its outright form; the case for low is weaker than first stated, because a
    Sentinel-path-confined replica-drain regression passes both tiers — if the owner rates that
    gap medium, rule 3 gives `next` after rule 1 clears. `blocked-by` narrowed to D1. State stays
    `analysed`, effort `M`, security `none`; comments added to every changed field.
  - Cross-ticket: in the consistency pass of the same day, the two `make lint` statements were
    corrected per T43 (the `gofmt -l .` line exits 0, so `make lint` gates nothing in
    `test/e2e`), the T35 bullet notes that T35 now carries both lag figures, and a T12 bullet
    was added (its reply check edits `valkeyExec` in the file where option E edits the readiness
    waits; T12's claim that the two edit the same helpers was corrected on its side).
  - Filed: the ADR 0026:164-165 "on every topology" finding of the Appendix went to
    [T70](070-tracked-comments-and-adrs-state-what-the-code-contradicts.md) as its item (f), and
    the Appendix keeps a pointer; the Cross-ticket bullet on the e2e reply check now points at
    [T68](068-the-e2e-exec-helpers-do-not-check-the-valkey-reply.md), where that check moved from
    T12. No frontmatter field, option or decision of this ticket rested on either finding.
- 2026-09-27: adversarial review of the enrichment - spot-checked the corrected
  `sidecar_test.go` lines, the helper locations, the ADR 0017 and `CLAUDE.md` lines and the ten
  T34 citations: all hold. Added a caveat under D2 option ii (poll the master count and ask every
  Sentinel for `config-epoch`); the mark stays on ii and D1 stays A. Effort `M` confirmed.
- 2026-09-27: enriched - re-read at `4a7543e`: the `sidecar_test.go` lines after `:281` (one
  higher since `f5c6886`) and the moved ADR 0017 and `CLAUDE.md` lines corrected in place; the
  fixed defects and the watcher note marked done. Options ordered into D1 (fixture half, A
  recommended) and D2 (site 1's effect read, Sentinel `config-epoch` plus a full master count
  recommended); a Work list with no XS no-decision item. **Effort `S` → `M`**: the code stays
  about 30 lines, but closing needs Kind runs with two revert checks and a mutation, a CI run, and
  the ADR 0017 D50, index row and `CLAUDE.md` edits plus ten T34 citation lines to rewrite. Urgency
  stays `next` (rule 3: medium, trigger live); `blocked-by: decision` unchanged.
- 2026-09-27 — the body of gap H-16 in `docs/security/workload-pod-posture.md` was replaced by
  a gap statement; its run log stays in ADRs 0032, 0033, 0025 and 0017. The two sentences this
  ticket pointed at there (`:252`, `:253`) are gone, and both pointers say so. No finding
  changed.
- 2026-09-27 — `SECURITY_ARCHITECTURE.md` was split into `docs/security/` by the documentation
  restructure; the two pointers to its lines 1578 and 1579 now also name their new place,
  `docs/security/workload-pod-posture.md:252-253`. Line numbers only, no finding changed.
- 2026-09-27 — renamed to `034-e2e-fixtures-wait-on-controller-state-after-a-pod-delete.md` (was `local_T34-e2e-fixtures-wait-on-controller-state-after-a-pod-delete.md`) when the tickets were numbered.
- 2026-09-26 — **doc half landed** in `f5c6886` (ADR 0017 :112, :117, :130, :576, :585, :595, :597; `CLAUDE.md` :222-223, :975; `SECURITY_ARCHITECTURE.md` :1578-1579; `sidecar_test.go` :276, :281 — line numbers before the change), plus T31 :948, :975-976. Rule 1 no longer matches; recomputed: rule 2 does not match, rule 3 does (severity medium, trigger live) → **urgency `now` → `next`**. Decision on the fixture half still open.
- 2026-09-26 — **adversarial check** of this file and its board row, at `a8e8931`, clean tree.
  Every cited file:line re-opened; every run claim recounted from the scratchpad logs. No test
  run, no Go code changed. State, severity, security, effort and urgency unchanged.
  - **Refuted and corrected in place:** the reason site 1's no-failover check is vacuous. It is
    not that a failover is slow — the master-drain experiment shows a drain-triggered failover
    with a second master up about 0.3 s after the delete in 5 of 10 runs — but that
    `initialMaster` was `sc-repdr-0` in 17 of 17 legs and `findMasterPod` asks ordinal 0 first.
  - **New false records (rule 1 now rests on six):** ADR 0017:112, :576 and
    `sidecar_test.go:281` place the red run on a single-node cluster; the red run's own log
    (`final6.log:7-20`, read) shows control-plane + 3 workers, as for every local run of the
    day. Added to the doc half, the Verification list and the board row. T31:948 and :975-976
    (untracked) carry the same two errors.
  - **Board:** the row names the six false sentences and carries no timings; the NOW heading
    named PR #195, released as v1.12.0 on 2026-08-28, and now names the current branch, with
    the old note under it marked as history.
  - **Corrected counts:** the replacement's first watcher answer was `role:slave` in 18 of 18
    experiment runs, not "three"; the watcher sampled about every 2 s per pod, not once a
    second; CI legs had 53 top-level tests (50 `TestE2E_*`), not "49 or 50".
  - **Corrected claims:** "blocks every merge" (it holds one PR until a rerun); Option A's time
    cost (the UID waits move time both tests already pay); "Why not B" (the class already has
    three instances, so the count argument fell; B still loses on enforcement); the merge base
    (`e3e869d` with `origin/main`, not the stale local `9925539`; the conclusion is unchanged).
  - **Relabelled as inference:** site 2's "read the replacements" and, for the 11 local legs,
    its "false in 16 of 17" (no timestamps locally); site 1's "because the old replica had
    disconnected first" and the `-r` step having seen a replacement.
  - **Marked unverifiable:** the board row's earlier self-correction of the D51 citation.
  - **Precised:** the three "no full suite" sentences were true when written in `b13377e` and
    became false when CI ran on it minutes later; `:595` is false under any reading.
  - **Line cites fixed:** `e2e_test.go:147-164` → `:147-163`; ADR 0017 `:280-282` →
    `:279-282`; ADR 0017 `:529-534` → `:527-534`. Every other file:line was current.
  - **Logged and excluded:** `ci-repro-full9.log`, a local full suite on the CI Kind config with
    the fix whose environment started no pods (36 of 49 red); it is no run of the fix.
  - **Confirmed unchanged:** sites 3, 4 and 5 fine; the PDB eviction reasoning; the four UID
    sites; the upstream v1.36.4 lines; the 17-leg timings of sites 1 and 2; the drain-master
    red run, experiment and CI table; the three stale "not yet audited" sentences; the D51
    miscitation; urgency `now` by rule 1, `next` by rule 3 after the doc half.
- 2026-09-26 — **file written and analysed**, state `filed` → `analysed`.
  - Every line number in the board row was re-read at `a8e8931`. All five are current:
    `sidecar_test.go:484` (already corrected on the board), `admission_recovery_test.go:237`,
    `sentinel_stale_master_test.go:126-127`, `pod_termination_test.go:128` and
    `topology_abandon_test.go:241`.
  - Superseded: the board's "vacuous at worst, flaky at worst" for all five sites. The audit
    classifies two as vacuous and three as fine.
  - The board's "1 of 7 local runs of the suite that day" is consistent with the logs: seven
    local runs holding 11 full-suite legs, one red.
  - **Urgency `later` → `now`.** The derivation rules, applied top-down:
    - **Rule 1 matches.** Three statements in tracked files about this ticket's fixed instance
      are measured false (ADR 0017:130, :595 and `CLAUDE.md:975`, against six CI legs). The
      fixture half alone would not match rule 1: every remaining site predates the branch.
    - **After the doc half lands, recompute:**
      - Rule 2 does not match. CI is green on `e6a9d7c`, and nothing in the release depends on
        sites 1 and 2.
      - Rule 3 matches. The severity is medium and the trigger is live: the vacuous path is
        measured in 17 of 17 legs for site 1, and the class was red once. That gives `next`.
    - The board row had been placed in LATER without the derivation.
  - Effort stays `S`. Blocked by the decision above.
- 2026-09-26 — filed as a board row (LATER) from T31's drain-test finding, together with the
  ADR 0017 D50 amendment.
