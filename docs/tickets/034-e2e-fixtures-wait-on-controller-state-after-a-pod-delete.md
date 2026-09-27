---
id: T34
title: e2e fixtures wait on controller state after deleting a pod
state: analysed
severity: medium
security: none
urgency: next
effort: S
blocked-by: decision
filed-from: T31, section "Drain-test finding", and ADR 0017 D50 as amended 2026-09-26
opened: 2026-09-26
decided:
done:
---

Filed as a board row on 2026-09-26 out of the drain-test finding recorded on
[T31](archive/031-generated-pods-run-as-root.md) (section "Drain-test finding") and at
[ADR 0017](../adr/0017-test-and-ci-policy.md) D50. This file was written the same day. Every
file:line below was re-read in the tree at `a8e8931` (`feat/rootless`, clean). Each claim is
labelled by how it was verified ([ADR 0017](../adr/0017-test-and-ci-policy.md) D36):

- **run** means taken from the log of an executed run. The log is named at the claim.
- **read** means read in the tree, in the module cache (`k8s.io/kubernetes@v1.36.4`), or in a
  log line that records a fact and not a measurement.
- **hypothesis** means neither.

**No test was executed for this file.** Every "run" label points at a log of an earlier run.
The logs are in the session scratchpad
`/private/tmp/claude-501/-Users-hfi-repos-valkey-operator/538d7ed7-2fba-46e2-87eb-bb14662fc87c/scratchpad/`,
which is not tracked:

| Log | What it is |
|---|---|
| `e2e_full9.log`, `e2e_full8.log`, `final2.log` … `final6.log` | The seven local runs of 2026-09-26 on Kind (`kindest/node:v1.36.1`), every one on a cluster of **control-plane + 3 workers** (`make kind-create`; `final6.log:7-20` shows four nodes joining, `:160` loads onto `valkey-operator-test-worker3`). Together they hold 11 full-suite legs. `leg8.log` is an excerpt of `final4.log` (byte-for-byte substring) and is not counted twice. |
| `ci-b13-failed.log`, `ci-a04-failed.log`, `ci-e6a-full.log` | CI on `b13377e`, `a04e2d0` and `e6a9d7c`. Each has two single-node full-suite legs, Valkey 9 and Valkey 8, on `kindest/node:v1.33.4`. |
| `drainexp.log`, `drain-orig-*/`, `drain-fixed-*/` | The Kind experiment with `TestE2E_SidecarFailoverDrainMaster` run alone: `test.log` per run, and `roles.txt` from a watcher that sampled role and `DBSIZE` ~~about once a second~~ *(corrected 2026-09-26, adversarial check: about every 2 s per pod — `drainwatch.sh` sleeps 1 s between rounds of nine `kubectl` calls; `drain-orig-1/roles.txt` samples `sc-drain-0` at :36, :38, :40, :42, :43)*. |
| `ci-repro-full9.log` | **Excluded from every count.** A local full suite on the CI Kind config (`kindest/node:v1.33.4`, 17:41) with the drain fix, 36 of 49 `TestE2E_*` red: the environment did not start pods (`ready=0/N` 4362 times). The drain-master and replica-drain tests failed at their setup wait (`sidecar_test.go:244`, `:460`) and never reached their delete, so it is no run of the fix. Site 2 did reach its delete and logged `:137` right after two 3/3 polls. |

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
    on the pods that existed before the delete.
- **The other helpers name the pod, not the process.**
  - `waitForPodReady` (`e2e_test.go:285-305`) reads the pod by name, with no UID and no
    `deletionTimestamp` check.
  - `getPod` (`:604-611`) requires the name to exist.
  - `valkeyExec`/`valkeyExecQuick` (`:230`, `:523`) exec into whichever process holds the name at
    that moment.
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

### The fixed instance: `TestE2E_SidecarFailoverDrainMaster`

The test deletes the master of a 3+3 Sentinel cluster
([`sidecar_test.go:274`](../../test/e2e/sidecar_test.go)). It then waited for the StatefulSet at
3/3 (`:285`), phase `OK` (`:286`), every pod Ready (`:289-291`), and exactly one pod answering
master (`:295-306`). The terminating old master meets all four.

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
    "got 0" at the old line `sidecar_test.go:334`, now `:343`.
  - The next subtest found `-rw` selecting `sc-drain-1`.
  - These log lines fit the recorded diagnosis: the old process left between the `EXISTS` and
    the `DBSIZE`, and the retry reached the empty replacement. **Which process answered each
    exec is not traced**, because the pod logs were lost with the CR.
  - Whether `sc-drain-1` held all 50 keys in that run was never read. The log has no `DBSIZE` of
    it.
  - ADR 0017 D50 also says the operator log of that cluster shows no operator action. **Not
    re-verified here:** that operator log is not among the kept files.
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
- **The fix (read).** The test records the UID at `:272` and calls
  `waitForPodRecreated(t, ns, initialMaster, killedUID)` at `:282`, before any role or data
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

### Tracked records of the fixed instance that are false now

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
- **Untracked, same two errors (read).** [T31](archive/031-generated-pods-run-as-root.md):948
  says "single-node Valkey 9 suite", and T31:975-976 says "No full-suite run with the fix is
  recorded". T31 is gitignored, so neither counts for rule 1, but both belong to the doc half.
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
| 1 | `TestE2E_SidecarDrainReplica` | [`sidecar_test.go:484`](../../test/e2e/sidecar_test.go) | **vacuous**, two hypothetical flake paths |
| 2 | `TestE2E_SentinelStaleMaster` | [`sentinel_stale_master_test.go:126-127`](../../test/e2e/sentinel_stale_master_test.go) (six deletes, loop `:125-128`) | **vacuous** waits; the assertions are protected only by timing |
| 3 | `TestE2E_AdmissionRejection_StatefulSetNudgeRecovery` | [`admission_recovery_test.go:237`](../../test/e2e/admission_recovery_test.go) (loop `:236-238`) | fine, by construction |
| 4 | `TestE2E_RollingUpdate_NoSecondDeleteWhileAPodTerminates` | [`pod_termination_test.go:128`](../../test/e2e/pod_termination_test.go) | fine; observes termination deliberately |
| 5 | `TestE2E_RollingUpdate_TopologyRestoreAbandoned` | [`topology_abandon_test.go:241`](../../test/e2e/topology_abandon_test.go) | fine, by effect |

**1 — `TestE2E_SidecarDrainReplica` (read; timings run).** The test deletes a replica of a 3+3
Sentinel cluster.

- `:487-488` wait for the StatefulSet at 3/3 and for phase `OK`. The terminating replica meets
  both.
- `:491-492` then compare `findMasterPod` with `initialMaster`. ~~At that moment a failover
  triggered by the replica delete could not have happened yet, because Sentinel needs seconds to
  declare a master down.~~ *(corrected 2026-09-26, adversarial check: the conclusion holds, the
  reason did not.)* A failover a replica drain could trigger is a forced `SENTINEL FAILOVER`
  from its drain handler (the regression is the role check at `drain.go:114` letting a replica
  through), which needs no down detection; the drain-master experiment above shows such a
  failover putting a second master up about 0.3 s after the delete in half the runs. Timing
  does not make the check vacuous. The ordinal order does:
  - In **17 of 17 legs** `initialMaster` was `sc-repdr-0` and the deleted replica `sc-repdr-1`
    (run, `sidecar_test.go:477`, formerly `:468`, in every log).
  - `findMasterPod` asks ordinal 0 first and returns the first pod answering `role:master`
    (read). `sc-repdr-0` keeps answering master until Sentinel reconfigures it, which comes
    after any promotion (read in the Valkey `sentinel.c` copy in the scratchpad: the failover
    states at `:111-117` send `REPLICAOF NO ONE` before any reconfiguration, and the old
    primary is turned into a replica on a later INFO refresh, `+convert-to-slave` at `:2641`;
    which Valkey release that copy is, is not recorded).
  - So the assertion passes whatever happened to the other two pods, as long as `sc-repdr-0`
    has not been demoted yet.
  - The subtest ended 0.10–0.32 s after its start in **17 of 17 legs** (run).
  - So "delete replica does not trigger master failover" never looks after the drain, and could
    not see a promotion elsewhere even if it looked early enough.
- `:498` reads the key from the master. That is fine, but it says nothing about the delete.
- `:504` `waitForPodLabel(replicaPod, instanceRole, replica)` is met by the terminating pod's own
  label.
  - The subtest took 0.01–0.10 s in 17 of 17 legs (run).
  - A replacement cannot be created, started and labelled by the sidecar within about 0.7 s of
    the delete. This is an inference from the 8.3 s minimum of the fixed drain test.
  - So "recreated replica labeled correctly by sidecar" reads the old pod.
- `:509` calls `getPod` with `require.NoError` for every ordinal. **Hypothesis:** this fails when
  it lands between the old pod's removal and the replacement's creation. It was not observed in
  17 legs.
- `:519` `waitForConnectedReplicas(initialMaster, 2)` is met while the old replica is still
  online. `:524-529` reads the key from `replicaPod` by name, and the old replica holds it.
  - Vacuous in 2 of 17 legs: 0.19 s in `e2e_full9.log`, 0.28 s in `ci-a04-failed.log` Valkey 8.
  - In the other 15 legs it took 6.2–30.2 s (run), because the old replica had disconnected
    first (inference: the durations fit a wait for the replacement's sync).
- `:533` `waitForEndpointPodCount(-r, 2)` is the one step that saw a replacement whenever it
  started early (inference, resting on the endpoint readiness below). In both legs where the
  replication subtest was vacuous, it then waited 10.0 s and 16.1 s (run).
  - That a terminating pod's endpoint is published not-ready is stated in ADR 0026:160-161 ("the
    endpoints controller carries its own `DeletionTimestamp` check"). The upstream source was
    **not read** here.
  - **Hypothesis:** the count read at `:533` and the list read at `:534` are separate, so an
    endpoint that flips between them fails `assert.Len`. Not observed.

**2 — `TestE2E_SentinelStaleMaster` (read; timings run).** The test deletes all three data pods
and all three Sentinel pods.

- `:131-132` wait for both StatefulSets at 3/3, `:133-136` wait for each of the six pods to be
  Ready, and `:137` logs "All pods restarted and ready". All of it is met by the old pods.
  - In 11 of 11 local legs, both StatefulSet waits read 3/3 on their first poll (run).
  - In 5 of 6 CI legs, the log line came 0.34–0.62 s after the delete line: `b13377e` 0.61 s and
    0.36 s, `a04e2d0` Valkey 9 0.50 s, `e6a9d7c` 0.34 s and 0.62 s. The sixth leg (`a04e2d0`
    Valkey 8) took 24.4 s.
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
    (hypothesis, not observed).
  - 15 of the 17 legs also show "reports 0 slaves", "reports 1 slaves",
    `flags: master,disconnected` or "Sentinel not responding" on the way.
  - The first subtest took 5.1–35.2 s in 16 legs. In the one leg where it took 0.11 s, the pod
    waits had already taken 24 s.
- They do so because the old Sentinel processes stop answering within the first second, not
  because the fixture waits (inference from the one operator log below; data pods not
  checked).
  - The CI operator log of `a04e2d0` Valkey 9 shows "connection refused" for `sentinel-0` in the
    same second as the delete (run).
- **Hypothesis, not observed:** an early read that still reaches the old `sentinel-0` passes
  `:140-157` and `:160-162` on the pre-restart topology.
- **Hypothesis, not observed:** the one read at `:167` (`valkeyExecAllowError`, which retries
  only on exec errors, `standalone_test.go:552-596`, and never polls for an answer) and the
  `findMasterPod` at `:173` can straddle the two generations of pods.

**3 — admission recovery: fine.**

- Pod creation is blocked by the webhook at `:233` before the deletes.
- `:242-249` then waits for `sts.Status.Replicas == 0`. Terminating pods count toward
  `replicas`, so this means every old pod is gone.
- `:255-267` polls for a phase other than `OK`, and the phase before the delete was `OK`
  (`:230`).
- After the webhook is removed, only new pods can exist (`:301`).
- Nothing after the delete can be met by the state before it (read).

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
  labelled master (`:220-221`; the label is state, not identity — see Adjacent findings) (read).

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
- **Four delete sites already wait by UID** (read): `pod_security_test.go:242→249`,
  `sentinel_peer_table_test.go:76→77`, `splitbrain_test.go:92→101` and
  `split_brain_dataset_test.go:110→115`.
- `pod_hardening_test.go:152` deletes its own probe pod as cleanup.

**Origin (read, `git log`).** All five audited files come from `main`: added between
`ce97f1b` (2026-02-28) and `360cb03` (2026-08-25). Between the merge base ~~`9925539`~~
*(corrected 2026-09-26, adversarial check: that is the stale local `main` ref; the merge base
with `origin/main`, merged in by `e2ce8bb`, is `e3e869d`, two dependency commits later that touch
no e2e file)* `e3e869d` and `HEAD`, only the drain-master hunk of `sidecar_test.go` changed among
the audited files and the helpers they call (`e2e_test.go`, `rolling_update_test.go`,
`standalone_test.go`, `pdb_test.go`). The class is **pre-existing on `main`**.
The one instance on `feat/rootless` is the fixed one, and its comment carries the wrong
citation.

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
  vacuous path shows on both (run).

**Not verified:**

- No test was executed for this file, and none in the adversarial check.
- Which process answered each exec in the red run, and whether its new master held every key.
- ADR 0017 D50's statement that the red cluster's operator log shows no action.
- The EndpointSlice readiness of a terminating pod in upstream source.
- Whether a replacement data pod can still briefly answer `role:master`, as the comment at
  `sidecar_test.go:314-316` says. The watcher saw `role:slave` on the first answer ~~in three
  runs, at about 1 s resolution~~ *(corrected 2026-09-26, adversarial check)* in all 18
  experiment runs, sampled about every 2 s per pod; a shorter window before its first sample
  is not excluded.
- That a replica-drain regression would be a forced `SENTINEL FAILOVER` as fast as the master's
  drain: inferred from the master-drain experiment, never run with a replica.
- In the 11 local legs, the time from the delete to site 2's `:137`: the logs carry no
  timestamps.
- The flake paths marked as hypotheses: none was observed in 17 legs.

## Impact

- **A required check can go red for nothing.**
  - `E2E Tests` (`e2e-gate`) is a required context (ADR 0017:527-534, D47, read).
  - A red run of this class ~~blocks every merge~~ *(corrected 2026-09-26, adversarial check)*
    fails that required check and holds the PR it ran on until a rerun. It has happened once,
    in a local run and not in CI.
  - Sites 1 and 2 carry the same exposure, with no failure observed in 17 legs.
- **Two tests do not guard what they name.**
  - Site 1 asserts "no master failover" and "recreated replica labelled" within about 0.7 s of
    the delete, before the replacement can exist (inference, above). Its no-failover check
    could not fail even with an early promotion elsewhere, because `sc-repdr-0` answers first.
  - No other e2e test deletes a replica and then checks that the master did not change (read:
    the delete sites above). A regression in which a replica drain triggers a failover therefore
    passes the suite (inference; no such regression was run).
  - Site 2's guard of the stale-master regression holds only because the old Sentinels stop
    answering within about a second of the delete (inference from one CI operator log, above).
- **Tracked files are wrong about the fix.**
  - Six sentences are false — three about how far the fix was verified (against the CI runs),
    three about the red run's cluster (against its own log) — and three are stale.
  - One code comment cites the wrong decision; the same comment carries one of the false
    "single-node" records.
- **Security: none.** No guard involved is a security control, and no principal gains anything
  from a vacuous test.

## Options

The filing bar allows this section: the severity is medium and the trigger is live. The class
was red once, and the vacuous path of site 1 is measured in every recorded leg.

| | What | Cost |
|---|---|---|
| **A** | The doc half, then a per-site fix of sites 1 and 2 (details below the table). | About 30 lines in two test files, ~~six doc sentences~~ eight doc sentences and one code comment with two fixes (`sidecar_test.go:276`, `:281`). ~~Site 1 gets about 8–35 s slower per leg (the measured replacement time). Site 2's polls already wait 5–35 s in CI, so it gains less.~~ *(corrected 2026-09-26, adversarial check:)* Both tests already pay the replacement time after the delete — site 1 in its replication or `-r` subtest (6.2–30.2 s in 15 legs, 10.0 and 16.1 s in the other two), site 2 in its first subtest (5.1–35.2 s in 16 of 17 legs) — so the UID waits move that time to the front and add little in total (inference from the run timings). |
| **B** | A, and `deletePod` also returns the UID, or a `replacePod` helper deletes and waits by identity. Either way, all 10 call sites of `deletePod` in 8 files move to it. | It makes the identity wait cheaper to write, but does not force it. Three of the five audited sites are correct without it. Speculative. |
| **C** | The doc half only. The two fixtures stay as they are. | The replica-drain guard gap and the timing-dependent Sentinel guard stay. |

What A does:

- **The doc half.** Supersede the three measured-false sentences in place with the CI facts
  (ADR 0017 D37). Supersede the three "single-node" records (ADR 0017:112, :576,
  `sidecar_test.go:281`) with the four-node local cluster. Point the three stale sentences at
  this audit. Correct the citation at `sidecar_test.go:276`. In the untracked T31, correct :948
  and :975-976 the same way.
- **Site 1.**
  - Capture the UID before `:484` and call `waitForPodRecreated` before any check that means
    "after the replacement".
  - Check "no failover" on the master itself, not with `findMasterPod`'s first match:
    `initialMaster` still answers `role:master`, with the `master_replid` it had before the
    delete, once the replacement is synced. **Hypothesis:** a promotion elsewhere, or a demotion
    and return, changes the replication ID (PSYNC2), so this reads the effect back.
- **Site 2.** Capture six UIDs before the loop and call `waitForPodRecreated` for each one
  before `:137`.

## Decision

**Open.** Hans has not decided yet.

**Recommendation: A, doc half first, before `feat/rootless` merges; B not taken.**

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
  would be a lint or a test over the e2e sources, which nobody has proposed.

## Verification

Done when every line holds, with the command and date recorded here:

- [x] **Doc half.** Done 2026-09-26 in `f5c6886`, pushed. `git grep -n "single-node Valkey 9" -- docs test CLAUDE.md SECURITY_ARCHITECTURE.md` returns nothing (the struck records read `~~single-node~~ Valkey 9`); `git grep -n "D50, D51" test/` returns nothing; every remaining hit of "no full suite has run" and "not yet audited" outside `docs/tickets` is struck; T31:948 and :975-976 corrected (untracked); `make lint` 0 issues.
  - ADR 0017:130, :595 and `CLAUDE.md:975` are superseded in place, each naming the CI legs.
  - ADR 0017:112, :576 and `sidecar_test.go:281` no longer call the red run single-node, and
    `git grep -n "single-node Valkey 9" -- docs test` returns only superseded (struck) text.
  - `CLAUDE.md:222-223`, `SECURITY_ARCHITECTURE.md:1578` and ADR 0017:117 name the audit
    result.
  - `git grep -n "D50, D51" test/` returns nothing.
  - T31:948 and :975-976 (untracked) carry the same corrections.
- [ ] **Site 1.**
  - The delete subtest takes at least the replacement time in both single-node legs of one CI
    run of the fix commit, with the durations recorded.
  - Revert check: with the UID wait removed in a scratch copy, it returns in under 1 s again.
  - The new no-failover assertion fails in a scratch copy that issues `SENTINEL FAILOVER` after
    the delete, so it can fail (ADR 0017's revert rule).
- [ ] **Site 2.**
  - "All pods restarted and ready" is logged only after six new UIDs, and the time from the
    delete to that line is recorded in both legs.
  - Revert check as for site 1.
- [ ] `make lint`, `make cyclo`, and `E2E Tests` green in CI on the fix commit. As with ADR
  0017 D50, a green streak is not a failure rate. The revert checks are the proof.

## Adjacent findings

Not in scope and not filed.

- **`pod_termination_test.go:220-221` excludes the master from the victim choice by the
  `instanceRole` label.** That label is the sidecar labeler's state, not identity. It chooses
  what to delete; it does not wait after a delete, so it is outside this class (read).
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
  anyway (run: `drain-orig-1/roles.txt`).
- **The sibling in the integration tier** is
  [T33](033-integration-tests-read-the-cache-after-a-write.md): a read that a stale
  observer can satisfy.

## History

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
