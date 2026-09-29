---
id: T34
title: e2e and integration fixtures pass on evidence that does not prove what they assert
state: analysed       # every part analysed, none decided
severity: medium      # the integration cache reads can turn a required check red for nothing; the other parts are low
security: none        # test code and one status write; no security control rests on them
urgency: now          # rule 1: tracked e2e comments in sidecar_test.go, sentinel_stale_master_test.go, e2e_test.go and standalone_test.go state false behaviour
effort: L             # four packages of XS-M; Kind runs on both Valkey lines, mutations and revert checks dominate
blocked-by: decision  # Q1-Q7; the independent items are not blocked
filed-from: T31, section "Drain-test finding", and ADR 0017 D50
opened: 2026-09-26
decided:
done:
---

# T34 - e2e and integration fixtures pass on evidence that does not prove what they assert

The e2e and integration tiers accept evidence that does not come from the state they assert: a
wait met by the pod that is still terminating, a read served by an informer cache that has not
seen the write, a Valkey error reply taken for success, and a tier split nobody recorded as a
decision. All parts are test code except one status write in the operator (`writePhase`); they
share ADR 0017 (D9, D10, D25, D50), two required checks and one full e2e run on both Valkey lines.

- **E2E waits after a pod delete** - readiness and phase waits a terminating pod satisfies.
- **E2E exec helpers** - the strict helpers treat a Valkey error reply as success.
- **Integration cache reads** - reads through the manager cache right after a write, and the
  unretried phase write.
- **Generated config below e2e** - no tier below e2e boots `valkey-server` on the generated config,
  and the split is not recorded as a decision.

## Current state

### Shared facts

- `E2E Tests` and `Integration Tests (envtest)` are required checks (ruleset `23985346`, ADR 0017
  D47) that Renovate's automerge waits for (`renovate.json:15-18`): a flake holds every merge.
- Every test file touched here carries a build tag; `make lint` and `go vet` skip them (T43).
- Both e2e parts edit [`e2e_test.go`](../../test/e2e/e2e_test.go) (different functions); one full
  e2e run on both Valkey lines serves both.

### E2E waits after a pod delete

After a pod `Delete`, every wait the fixtures use can be answered by the terminating pod. kubelet
keeps it `Ready` for its whole termination
([ADR 0026](../adr/0026-a-pod-being-deleted-is-not-available.md)); the StatefulSet's
`readyReplicas` counts it (upstream `stateful_set_control.go:378`, v1.36.4), and
`waitForStatefulSetReady` ([`e2e_test.go:147-163`](../../test/e2e/e2e_test.go)) compares
`ReadyReplicas` alone (`:160`); `waitForPodReady` (`:285-305`), `getPod`, `valkeyExec` and
`valkeyExecQuick` work by name without UID or `deletionTimestamp`; a phase wait for `OK` is met by
the status written before the delete. `waitForPodRecreated` (`:307-340`) waits for a new UID, Ready,
and five delete sites use it. ADR 0017 D50 requires identity (image, UID or `deletionTimestamp`)
wherever "which pod" is part of the assertion; a `deletionTimestamp` cannot be unset.

**Site 1 - `TestE2E_SidecarDrainReplica`** ([`sidecar_test.go`](../../test/e2e/sidecar_test.go)):
master `sc-repdr-0` by construction, replica `sc-repdr-1` deleted at `:485`.

- `:488-489` (StatefulSet 3/3, phase `OK`) are met by the terminating replica; the comment at
  `:487` is false; the subtest ends 0.10-0.32 s after its start in 17 of 17 recorded legs.
- `:492-494` "does not trigger master failover" compares `findMasterPod`
  ([`rolling_update_test.go:733-750`](../../test/e2e/rolling_update_test.go)), which asks ordinal 0
  first, with `initialMaster`; an old master answers `role:master` for 10.3-10.5 s after a Sentinel
  failover (measured on 9.1.1 and 8.1.9), so the check cannot fail (ADR 0017 D10 in substance).
- `:505` `waitForPodLabel`, `:520` `waitForConnectedReplicas` and the read at `:525-530` can be met
  by the old pod (vacuous in 2 of 17 legs). The comment at `:523-524` names
  `valkeyExecAllowError`; `:526` calls `valkeyExecQuick`. `:534` (`-r` endpoint count) is sound.
- The unit tier catches removal of the replica role check at
  [`drain.go:114`](../../internal/sidecar/drain.go) (`TestDrainHandler_ReplicaExitsImmediately`),
  but no unit test drains a replica with `sentinelEnabled`: a Sentinel-path regression, a role
  misread against real Valkey or a foreign failover passes both tiers.

**Site 2 - `TestE2E_SentinelStaleMaster`**
([`sentinel_stale_master_test.go`](../../test/e2e/sentinel_stale_master_test.go)) deletes all three
data and Sentinel pods (`:125-128`). The waits at `:131-136` are met by the old pods; the comment at
`:130` is false and "All pods restarted and ready" (`:137`) appeared 0.34-0.62 s after the delete in
5 of 6 CI legs. The subtests at `:140-210` read the replacements only because the old Sentinels
stop answering within about a second.

Checked and fine: `admission_recovery_test.go:237`, `pod_termination_test.go:128`,
`topology_abandon_test.go:241`. The PDB eviction waits (`pdb_test.go:102`, `:110`, `:274`) are
vacuous but gate on `DisruptionsAllowed > 0`; only their comments (`:100-101`, `:272-273`) promise
a recovery. Impact: a vacuous wait turned `E2E Tests` red once
(`TestE2E_SidecarFailoverDrainMaster`, now waiting by UID); sites 1 and 2 carry the same exposure,
no failure in 17 legs; site 1 does not guard what it names, site 2 holds on timing only.

### E2E exec helpers

The tier runs `kubectl exec <pod> -- valkey-cli --raw -p <port> <args>`. The strict helpers decide
on the exit status of `kubectl exec` alone: `valkeyExec`
([`e2e_test.go:225-268`](../../test/e2e/e2e_test.go), `--raw` at `:247`; five attempts, linear
4/6/8/10 s, then `require.NoError` at `:266`) and `valkeyTLSExec`
([`tls_test.go:45-88`](../../test/e2e/tls_test.go), `--raw` at `:63`; three attempts 2 s apart).

Measured on 9.1.1 and 8.1.9: without `-e`, `valkey-cli` prints an error reply (`NOREPLICAS`,
`READONLY`, `OOM`, `LOADING`, `WRONGTYPE`, Sentinel `NOGOODSLAVE`) on stdout and exits 0; with
`-e` it goes to stderr with exit 1, while `OK`, integers (`TTL` `-1`/`-2`), an empty `GET`, `PING`
and `PUBLISH` exit 0. "stdout starts with `-`" fails (`--raw` drops the `-`, `TTL` prints `-1`). A
connection refusal already exits 1. `READONLY`, `OOM`, `NOREPLICAS` and `LOADING` are rejected
before execution, so resending is safe. The lenient helpers `valkeyExecAllowError`
([`standalone_test.go:550-596`](../../test/e2e/standalone_test.go)), `valkeyTLSExecAllowError`
([`tls_test.go:90-115`](../../test/e2e/tls_test.go)) and `valkeyExecQuick` (`e2e_test.go:520-542`)
return error text on purpose and are not part of the fix.

- **15 writes discard the strict reply:** [`tls_test.go:496-499`](../../test/e2e/tls_test.go),
  `:830`, `:836`, `:842`, `:1284`, `:1288`, `:1295`, `:1298`;
  [`tls_rotation_test.go:131`](../../test/e2e/tls_rotation_test.go);
  [`sidecar_test.go:481`](../../test/e2e/sidecar_test.go);
  [`rolling_update_test.go:291`](../../test/e2e/rolling_update_test.go);
  [`fleet_upgrade_test.go:768`](../../test/e2e/fleet_upgrade_test.go). Each is read back, so no test
  passes because of it, but a refusal fails at the read-back as data loss (`tls_rotation_test.go:221-222`,
  `sidecar_test.go:499-500`, `fleet_upgrade_test.go:397-416`) and triage starts at the operator.
  Refusals happen exactly after the failovers and rolls the suite performs.
- **Two `SENTINEL FAILOVER` calls discard the reply**
  ([`sentinel_stale_master_test.go:80`](../../test/e2e/sentinel_stale_master_test.go),
  [`sentinel_peer_table_test.go:92`](../../test/e2e/sentinel_peer_table_test.go)); `INPROG` or
  `NOGOODSLAVE` shows only as a wait timing out. Resent after the running failover ended it starts a
  second one (measured with one master and one replica), so it must not ride a retry.
- **20 strict calls sit inside polls** (11 `require.Eventually`, 7 `pollUntil`, 2
  `wait.PollUntilContextTimeout`), two through `authTLSExec`
  ([`pod_security_test.go:84-88`](../../test/e2e/pod_security_test.go)); an error reply makes the
  condition false. Ten send `INFO`.
- **False comments:** [`standalone_test.go:591`](../../test/e2e/standalone_test.go) (a `READONLY`
  reply exits 0 and leaves through `:579-581`); `e2e_test.go:228` ("exponential backoff"; it is
  linear); `e2e_test.go:520-522` ("returns \"\" on any error"; only on an exec error).

The operator's own client returns an error reply as a Go error
([`client.go:479-481`](../../internal/valkeyclient/client.go)) and is not affected.

### Integration cache reads

- `k8sClient = mgr.GetClient()` ([`suite_test.go:129`](../../test/integration/suite_test.go))
  serves `Get`/`List` from the informer cache shared with the reconciler (`:98-99`); `apiReader`
  (`:130`, comment `:47-50`) is used once
  ([`pod_hardening_test.go:257`](../../test/integration/pod_hardening_test.go)). Every type has its
  own informer: a `Get` right after a write can miss it, and seeing X says nothing about Y.
- **Write order.** One pass in [`valkey_controller.go`](../../internal/controller/valkey_controller.go)
  runs the resource steps (`:551-573`), `ReconcileBlocked` (`:276`), `reconcileWorkload` (`:284`:
  nudge `:332`, `persistStatus` with `Ready`, `:2241-2283`), then the `Error` phase (`:295`). A
  blocked pass suppresses intermediate phase writes (`:2606-2611`, `:2549-2552`), so the phase stays
  `Provisioning` (`:256-260`) until `:295`. A negative read after a poll rules out only earlier writes.
- testify v1.12.1 `Eventually` runs the condition once immediately in its own goroutine
  (`assert/assertions.go:2023-2024`): a negative poll passes on a cache that has not seen the object,
  and a `require` inside it calls `FailNow` off the test goroutine.
- The object passed to `Create`/`Update`/`Patch` holds the stored object afterwards
  (controller-runtime v0.25.1, `pkg/client/apiutil/apimachinery.go:224-240`); the one reproduced
  instance (red in CI, 2 of 4 local runs) is fixed that way
  ([`pod_security_test.go:43-50`](../../test/integration/pod_security_test.go)). Sound patterns:
  `pod_hardening_test.go:102-105`, `:184-201`, `requirePhaseError` `:204-220`.

**`writePhase` drops a conflicting phase write:** a cached `Get` (`:2617`) and a `Status().Update`
(`:2628`) with no retry, where `writeStatusCondition` (`:2689-2716`) retries the same 409. A blocked
pass discards the error at `:295` and writes `Error` next pass
([`ratelimiter.go:71-79`](../../internal/controller/ratelimiter.go)). Both `Failover in progress`
writers call `updatePhase` right after an `r.Update` of the CR and discard its error
([`rolling_update.go:2743-2746`](../../internal/controller/rolling_update.go), `:4045-4062`), so a
lagging cache drops a phase CLAUDE.md (Status) requires; `Ready` and the failover are unaffected.
Callers returning the error (`:2190`, `:2200`) fail the pass on the operator's own 409.

**Class A - can go red.**

| # | Site | Why |
|---|---|---|
| A1 | [`foreign_object_test.go:75-78`](../../test/integration/foreign_object_test.go), `:223-226`; [`volumeclaim_conflict_test.go:230-233`](../../test/integration/volumeclaim_conflict_test.go) | One unpolled `Get` asserts `phase == Error` after a poll on `ReconcileBlocked` (or `StorageSpecNotApplied`, [`volumeclaim_conflict.go:183`](../../internal/controller/volumeclaim_conflict.go)); a tick between `:276` and `:295` reads `Provisioning`. |
| A2 | [`tls_material_test.go:293-295`](../../test/integration/tls_material_test.go) | Cached `Get` of a pod created at `:272`; the Pod informer may not have the ADD yet. |
| A3 | [`observer_test.go:159-164`](../../test/integration/observer_test.go) | Cached `Get` plus unretried spec `Update` while the same pass writes the CR status: a 409 by write order. |

**Class B - never a false red, but a regression can pass** (ADR 0017 D10 for B2, D9 for the rest).

| # | Site | Why | Unit guard |
|---|---|---|---|
| B1 | [`sidecar_services_test.go:413-419`](../../test/integration/sidecar_services_test.go), `:421-427` | `IsNotFound` polls pass on a cache that has not seen the test's own `Create` (`:392`, `:409`). | [`valkey_controller_test.go:534`](../../internal/controller/valkey_controller_test.go), `:567`; [`resource_reconcile_test.go:915`](../../internal/controller/resource_reconcile_test.go) ff. |
| B2 | [`foreign_object_test.go:236-237`](../../test/integration/foreign_object_test.go) | No nudge on the foreign StatefulSet; none can land before `nudgeGracePeriod` = 10 s ([`nudge.go:26`](../../internal/controller/nudge.go), `:229`), the subtest ends about 0.4 s after the first pass. | [`foreign_object_test.go:424`](../../internal/controller/foreign_object_test.go) |
| B3 | [`observer_test.go:87-94`](../../test/integration/observer_test.go), `:123-128`; [`foreign_object_test.go:82-85`](../../test/integration/foreign_object_test.go), `:88-93`, `:162-167`, `:230-235`, `:311-326`, `:369-375`; [`tls_material_test.go:182-184`](../../test/integration/tls_material_test.go); [`volumeclaim_conflict_test.go:150-169`](../../test/integration/volumeclaim_conflict_test.go), `:356-362`; [`integration_test.go:99-105`](../../test/integration/integration_test.go) | "Absent", "not `Error`" or "untouched" asserted before the write it rules out has necessarily happened, or through another type's informer. | foreign-object sets: [`foreign_object_test.go:63`](../../internal/controller/foreign_object_test.go), `:132`, `:344`, `:627`, `:648`; others not traced |
| B4 | [`observer_test.go:177-183`](../../test/integration/observer_test.go) | `err != nil` poll on the observer ServiceAccount, the B1 shape. | not traced |
| B5 | [`sidecar_services_test.go:466-481`](../../test/integration/sidecar_services_test.go) | Standalone `-all`/`-r` absence after `time.Sleep(2 * time.Second)`. | not traced |

**Class C - latent, holds on timing.**

| # | Site | Holds because |
|---|---|---|
| C1 | [`foreign_object_test.go:107-109`](../../test/integration/foreign_object_test.go) | SA read after the RoleBinding poll; created SA -> Role -> RB (`valkey_controller.go:983-1003`). |
| C2 | [`integration_test.go:108-125`](../../test/integration/integration_test.go), `:196-200`, `:266-341`, `:423-428`, `:514-519` | Unpolled reads after a StatefulSet created several round trips later. |
| C3 | [`sidecar_services_test.go:298-321`](../../test/integration/sidecar_services_test.go), `:430-439` | Role/RB after the SA poll, `-r`/`-all` after the `-rw` poll; under a subtest filter `:298-321` is write order (`valkey_controller.go:984`, `:992`, `:1003`). |
| C4 | [`reconcile_concurrency_test.go:142-154`](../../test/integration/reconcile_concurrency_test.go) | Unretried `Status().Update` on a cached StatefulSet; the operator rewrites it only on drift (`valkey_controller.go:1366-1373`) or after the 10 s nudge. |

Sound as written: write-answer reads, `pdb_uid_precondition_test.go`'s uncached client,
poll-guarded writes, a `Get` after polling the same object, `tls_material_test.go:213-217`.

### Generated config below e2e

`CLAUDE.md` (Testing), [ADR 0017](../adr/0017-test-and-ci-policy.md) D2
([`:225-233`](../adr/0017-test-and-ci-policy.md#L225-L233)) and
[`docs/developer/testing.md:15-16`](../developer/testing.md#L15-L16) describe the split correctly;
D2's exclusive clause for e2e is writes values **and** verifies replication
([`:228-230`](../adr/0017-test-and-ci-policy.md#L228-L230)). Alternatives Considered
([`:1135-1264`](../adr/0017-test-and-ci-policy.md#L1135-L1264)) has no entry for "a real Valkey
pair in the integration tier", so the question keeps being reopened; the Residual risks
([`:1271-1282`](../adr/0017-test-and-ci-policy.md#L1271-L1282)) already record that the init scripts
are not run in the pinned images.

`GenerateValkeyConf` ([`configmap.go:46`](../../internal/builder/configmap.go#L46)) writes the
ConfigMap text, the replica variant naming pod-0 or the known master (`:33-39`, `:164-172`).
`init-config-selector` rewrites it on multi-replica pods: the Sentinel branch
([`statefulset.go:275-356`](../../internal/builder/statefulset.go#L275-L356)) asks the Sentinels for
up to 30 s and appends `replicaof` (`:329-340`; the ordinal fallback `:341-350` is unreachable while
the replica file has `replicaof`, by reading); the non-Sentinel branch (`:440-544`) asks peers for up
to 15 s, then self-claims or appends the found master (`:490-536`). Data-tier auth is on the
command line (`:822-834`).

| Tier | Real `valkey-server` | Config | Writes values | Verifies replication |
|---|---|---|---|---|
| Unit | no | runs the non-Sentinel data init ([`init_script_exec_test.go:96-119`](../../internal/builder/init_script_exec_test.go#L96-L119)) and the Sentinel pod init with stubs; `GenerateValkeyConf` by substring | no | no |
| Integration | no ([`suite_test.go:63-65`](../../test/integration/suite_test.go#L63-L65)) | none | no | no |
| Imagetools | yes, both pins | hand-written flags ([`restricted_runtime_test.go:29-32`](../../test/imagetools/restricted_runtime_test.go#L29-L32)) | yes (`:111`) | no |
| E2E | yes, both pins, every PR | generated config plus init appends | yes ([`e2e_test.go:273`](../../test/e2e/e2e_test.go#L273)) | yes ([`e2e_test.go:491`](../../test/e2e/e2e_test.go#L491)) |

No test below e2e runs the Sentinel-branch data init; `Valkey Image Tools` is also a required check.
Measured in docker on 9.1.1 and 8.1.9 with hand-transcribed configs: a generated pair with the
generated FQDNs replicates; an unknown directive is fatal at boot (in e2e: a pod never Ready); a
lone replica with an unresolvable `replicaof` boots as `role:slave`; the TLS block without `/tls`
files is fatal; persistence mode `both` (duplicate `dir /data`,
[`configmap.go:203-243`](../../internal/builder/configmap.go#L203-L243)) boots, and no test uses it.
No production path is affected.

## Required changes

### Shared, in one change per landing

- ADR 0017 amendments land together with one Status line and one update of the index row
  [`docs/adr/README.md:109`](../adr/README.md): D50 records the fixture fix (drop "D50 (the fixture
  fix for two vacuous sites)"; under Q1 = E also that the shared waits refuse a terminating pod); a
  new decision that integration reads come from the API server and a write-order read polls for the
  completed pass; D25 per Q6; the Alternatives entry or D53 amendment per Q7.
- `CLAUDE.md:282-284` ("two are vacuous") becomes true; the citations of this ticket outside
  `docs/tickets/` become ADR 0017 D50 (shared with T40, whichever closes first).
- One full e2e run on both Valkey lines on the fix commit covers both e2e parts and names any test
  whose duration moved by more than a pod replacement.
- Close ([ADR 0034](../adr/0034-tickets-are-work-lists-that-get-archived.md)): extract, then move
  this file to [archive/](archive/).

### E2E comments (independent, can land first)

- `sidecar_test.go:523-524` names `valkeyExecQuick` (reason stays); `standalone_test.go:591`,
  `e2e_test.go:228` and `:520-522` state the measured behaviour; the retry log lines
  (`e2e_test.go:238`, `tls_test.go:54`) name the last error. `sidecar_test.go:487`,
  `sentinel_stale_master_test.go:130` and `:137` become true with the Q1 fix.

### E2E waits after a pod delete

- Independent, lands with the Q1 fix: site 1's effect read replaces `:492-494`. Before the delete
  read `config-epoch` from `SENTINEL MASTER` on all three Sentinels (walk of `sentinelPeerCount`,
  [`sentinel_peer_table_test.go:134-149`](../../test/e2e/sentinel_peer_table_test.go)); right after
  the identity wait and before any `waitForReplicaSynced` (`e2e_test.go:509-518`) assert it unchanged
  (one read each), then poll every ordinal, bounded, until exactly one answers `role:master` and it
  is `initialMaster`. `config-epoch` rises 0 -> 1 on every Sentinel within 1.3 s of a failover
  (measured on both lines) and nothing on this path resets it; before the sync wait, a failover
  fails on the right assertion (ADR 0017 D9, D10).
- (Q1 = E) `waitForStatefulSetReady` keeps `ReadyReplicas == n` and requires each pod
  `<name>-0` ... `<name>-(n-1)` to exist, be Ready and carry no `deletionTimestamp` (one List per
  poll); `waitForPodReady` returns false on a `deletionTimestamp`. No edit at site 2.
- (Q1 = A) UID captured before `sidecar_test.go:485`, `waitForPodRecreated` before `:488`; six UIDs
  before `sentinel_stale_master_test.go:125`, six waits before `:137`.
- Proof on Kind, both lines, `make test-e2e E2E_RUN='TestE2E_SidecarDrainReplica|TestE2E_SentinelStaleMaster'`:
  site 1's subtest and site 2's delete-to-log time take at least the replacement time (recorded);
  without the identity wait both return in under 1 s. Site 1 mutation: `SENTINEL FAILOVER` after the
  identity wait, 2 s, effect read; it must fail on the epoch or master poll, not a sync wait (not
  right after the delete: Sentinel clusters have no drain `preStop`).

### E2E exec helpers

- (Q2 = B or A) `"-e",` next to `"--raw",` in `valkeyExec` (`e2e_test.go:247`) and `valkeyTLSExec`
  (`tls_test.go:63`) only; the two `SENTINEL FAILOVER` calls move to `valkeyExecAllowError` and
  assert `OK` or a reply starting with `INPROG`.
- (Q2 = A) a classifier, a poll-safe variant with an `authTLSExec` counterpart, the 20 poll sites
  moved to it, the 11 `require.Eventually` among them converted to `wait.PollUntilContextTimeout`.
- (Q2 = D) `require.Equal` on the expected reply at each of the 15 discarding sites.
- Proof: revert check on Kind with `sidecar_test.go:481` pointed at a replica: with the fix it fails
  at the write with `READONLY`, without `-e` at the read-back; record both, restore. Greps: `"-e",`
  only in the two strict helpers, both `SENTINEL", "FAILOVER` calls with a reply assertion, none of
  `exponential`, `e.g., Valkey READONLY`, `on any error` left. Every `Retrying valkeyExec` or
  `Retrying valkeyTLSExec` line with a reply text in the full run is a refusal once swallowed.

### Integration cache reads

- Independent. **A1:** `foreign_object_test.go:75-78` ->
  `requirePhaseError(t, types.NamespacedName{Name: crName, Namespace: "default"}, "sidecar ServiceAccount")`;
  `:223-226` -> `requirePhaseError(t, key, "does not control")`; in `volumeclaim_conflict_test.go`
  insert `requirePhaseError(t, key, "volumeClaimTemplates are immutable")` before `:230`, delete
  `:232-233`, keep the `Get` at `:230-231` that `:235` reads.
- Independent. **A3, C4:** read and write inside one poll (pattern `pod_hardening_test.go:150-158`)
  at `observer_test.go:159-164` and `reconcile_concurrency_test.go:148-154` (`slowProbes.arm()` at
  `:144` stays in front). **C3 `:298-321`:** poll the Role and the RoleBinding.
- Independent. **Completed-pass marker:** wait for `Ready` with `observedGeneration ==
  metadata.generation` before `observer_test.go:87-94`, `:123-128`, `tls_material_test.go:182-184`,
  `foreign_object_test.go:311-326`, `volumeclaim_conflict_test.go:163-169`, `:356-362`; B5 replaces
  its `time.Sleep` with it. The marker precedes the `Error` write, so each test states that its
  `ReconcileBlocked` assertion catches what a "not `Error`" check aims at; each set gets a positive
  control (ADR 0017 D11).
- (Q3 = B) `suite_test.go:129-130` becomes
  `client.New(testEnv.Config, client.Options{Scheme: scheme.Scheme})`, `apiReader` merges into it;
  rewrite `suite_test.go:47-50` and `docs/developer/testing.md:69-71`; drop `pod_hardening_test.go:256-257`'s
  `apiReader` use. (Q3 = A) per-site fixes for A2, B1, B4, C1, C2, C3 `:430-439`, and a positive
  wait plus an `apiReader` read for the remaining B3 sets.
- (Q4) B2 per the answer; under the recommendation one sentence in ADR 0020 (D8 or Residual risks)
  naming the unit-only coverage as an ADR 0017 D12 exception.
- (Q5 = W1) `retry.RetryOnConflict(retry.DefaultRetry, ...)` around `writePhase`'s `Get`, compare
  and `Update`; two unit tests via `interceptor.Funcs.SubResourceUpdate`
  (`internal/controller/status_phase_test.go:35`): one 409 then the phase lands, and `updatePhase`
  after an `r.Update` lands `Failover in progress`; both fail with the retry removed. One sentence
  in ADR 0002 (D3 or D7).
- (Q6) the wait style of every touched wait; under a' the four `metrics_test.go` waits, with a grep
  that no wait condition calls `require` or `assert`.
- Proof: A1 by an ADR 0017 D13 mutation (500 ms delay before `writePhase` at `:295`): unfixed fails,
  fixed passes. B1 (`deleteLegacyServices` a no-op), B4 (`cleanupObserverServiceAccount` a no-op)
  and one mutation per B3 set fail the fixed test. A grep finds no read of a just-written object
  without a poll, the write answer or an uncached client. `make test-integration` green 10 times and
  in CI; a streak alone is no proof.

### Generated config below e2e

- (Q7 = A) ADR 0017 Alternatives entry "A real Valkey pair in the integration tier" plus a Status
  line: it lost because envtest has no kubelet and a pair duplicates the replication assertion
  `E2E Tests` makes on the same pins in the same run; imagetools already writes values, so D2's
  exclusive clause is replication; C-prime is the upgrade path, the Sentinel-branch data init needs
  a D19 unit exec harness regardless; revisit when a generated-config or config-writer defect
  reaches a cluster or an e2e run fails on a rejected directive; persistence `both` boots on both
  pins (measured); cross-reference the Residual risks at `:1271-1282`. It must not claim Renovate
  bumps the Valkey pins (T45). Verify by grep inside Alternatives Considered.
- (Q7 = C-prime) a `test/imagetools` test per pin (`pinnedImages()`): run the generated
  `init-config-selector` with no peer or Sentinel answering, ConfigMap texts on tmpfs, boot
  `valkey-server` through the generated command (auth variant with `VALKEY_PASSWORD`, TLS variant
  with a certificate fixture), assert boot, role and `master_host`; the discovered-master path
  (`statefulset.go:522-527`) needs a stub `valkey-cli` or a live peer. Update `CLAUDE.md` ("the
  config-writer scripts are not run there"), [`docs/developer/testing.md:157`](../developer/testing.md#L157),
  ADR 0017 D53 (`:843`) and its residual risk (`:1271-1278`). Verify: `make test-image-tools` green
  on both pins; a bad directive fails with the line named; removing the `replicaof` append fails the
  role assertion; `make cyclo` clean.

## Open questions

### Q1: Is the fix made in the shared wait helpers or per site with UID waits? (e2e waits)

Every fixture reaches for `waitForStatefulSetReady` or `waitForPodReady` after a delete, and both
are met by a terminating pod. The helpers can refuse a terminating pod, or the two known sites can
call `waitForPodRecreated`.

- **E - the shared waits refuse a terminating pod (recommended).** About 15 lines in `e2e_test.go`;
  changes the meaning of 120 calls in 22 files: a call returning while a pod of its range terminates
  now waits at most the termination (75 s data, 30 s Sentinel, within the 5 min `testTimeout`).
  Fixes site 2 with no edit, makes the PDB comments true, covers future deletes.
- **A - per-site UID waits.** About 15 lines in two files, no other timing moves; the shared waits
  stay vacuous, future deletes rely on review against D50, the PDB comments stay untrue.

E because the defect lives in the helpers: every earlier unsafe delete site used
`waitForStatefulSetReady`, the per-site rule was missed once after `waitForPodRecreated` existed,
and ADR 0026 made the same choice for operator code (fix the accessor, not a list of sites).

**Answer:** _open_

### Q2: How does `valkey-cli -e` reach the two strict helpers? (e2e exec helpers)

With `-e` an error reply becomes exit 1, which the helpers today treat as a transport failure:
retried, then "kubectl exec failed". The choice is whether a reply error takes that path or is told
apart and fails at once.

- **B - add `-e`, keep the retry path (recommended).** XS, two argv lines. A transient refusal
  (`LOADING`, `READONLY` before a role flip) heals within the retries, safe because refused commands
  were not executed; a persistent one fails with the reply text after 28 s (4 s over TLS), labelled
  "kubectl exec failed", and inside polls ends the test from the helper. Residual rule: a command
  whose repetition has an effect does not go through a strict helper.
- **A - `-e` plus a classifier and a poll-safe variant.** S. Exit 1 with `command terminated with
  exit code 1` and without `Could not connect to Valkey` fails at once, correctly labelled, never
  resent; costs parsing two texts this repository does not control, a second helper, 20 call-site
  moves and 11 wait conversions in unlinted files.
- **D - assert the reply at the 15 sites.** S. Fixes today's sites only; reads inside polls still
  take error text for an answer.

B closes the defect for every current and future strict call; A gains only on the failure path,
and no strict caller tests a refusal.

**Answer:** _open_

### Q3: How is the cache-lag half removed? (integration cache reads)

25 of the 31 cited ranges (A2, B1, B3-B5, C1-C3) depend on informer timing. Only how test reads are
served changes; the write-order sites need their fix either way.

- **A - fix each site in the cached design:** about 25 edits in 7 files; protects only the listed
  sites, and the list has already proven incomplete.
- **B - make every test read uncached (recommended):** one assignment plus two comment/doc
  rewrites; test reads stop acting as a barrier for the operator's cache, as in production. No test
  is known to rely on that barrier (no cache indexes, no unstructured reads).

B covers every present and future read without enumeration; envtest's QPS 1000 / Burst 2000
(`pkg/envtest/server.go:309-313`) does not throttle uncached polling.

**Answer:** _open_

### Q4: What happens to the B2 nudge assertion, which cannot fail? (integration cache reads)

It is the only envtest line for the nudge refusal guard (ADR 0020 D8); ADR 0017 D12 asks for a
real-API-server layer for every refusal guard, and the unit test at
`internal/controller/foreign_object_test.go:424` pins the guard.

- **Delete it and record the unit-only coverage as a named D12 exception (recommended):** XS plus
  one ADR 0020 sentence; the comment names the unit test and its mutation (`nudge.go:211` removed,
  unit test fails).
- **Wait past the nudge:** keeps D12 literally; adds 10-40 s to a suite of about 25 s.

The guard refuses before any API call, so the real API server adds nothing to it.

**Answer:** _open_

### Q5: Does `writePhase` retry a conflict? (integration cache reads)

Its unretried 409 delays `Error` in a blocked pass, can drop `Failover in progress`, and fails
non-blocked passes on the operator's own write. Only the conflict path changes; ADR 0002 D7's
handling of non-conflict errors stays.

- **W1 - `RetryOnConflict` like `writeStatusCondition` (recommended):** XS-S with two unit tests;
  the cached `Get` still replaces the caller's object (no traced caller is harmed).
- **W3 - status merge patch without `Get` or resourceVersion:** S; no self-inflicted 409, but a
  second write shape without optimistic concurrency, against ADR 0002 D7.
- **W2 - leave it:** no cost; the failover phase stays lost whenever the cache lags.

W1 is the shape ADR 0002 D7 already documents as correct, at the smallest change. If T18's R1
(refresh through the `APIReader`) is taken, reading `writePhase`'s `Get` through it is a variant.

**Answer:** _open_

### Q6: Does ADR 0017 D25 (poll with `wait.PollUntilContextTimeout`, never `Eventually`) extend to the integration tier? (integration cache reads)

D25 is e2e-only by title; the integration tier has 56 `Eventually`, one `require.Never` and 10
`PollUntilContextTimeout`. `scrapeMetrics(t)`
([`metrics_test.go:21-31`](../../test/integration/metrics_test.go)) calls `require` inside the wait
conditions at `:37-39`, `:94-105`, `:114-124`, `:131-134`: a red delayed by 15-30 s, rarely a panic
on a finished `*testing.T`, never a false pass. Under Q2 = A the e2e side applies D25 to 11 more
waits, so both tiers would share one wait rule.

- **a - extend D25 to new waits and every wait this ticket touches:** the ADR amendment;
  `metrics_test.go` keeps the forbidden shape.
- **a' - a, plus converting the four `metrics_test.go` waits (recommended):** XS more; no wait
  condition in the tier calls `FailNow` off the test goroutine.

a' removes the one verified instance of the forbidden shape at XS extra cost.

**Answer:** _open_

### Q7: Does the generated Valkey configuration get a real-server check below e2e? (generated config)

Only e2e boots `valkey-server` on the generated config, on both pins, on every PR, behind a required
check; a rejected directive already turns that PR red. The choice is whether it is found within
seconds in `Valkey Image Tools` with the line named, or as an e2e timeout. Replication stays e2e-only
either way.

- **A - keep the split and record it (recommended).** One ADR 0017 Alternatives entry, XS.
- **C-prime - boot the file the init container writes, in `test/imagetools`.** Named failure and the
  only check below e2e of the full file in the image's shell; S-M, a certificate fixture, about 15 s
  and 30 s of backoff per script run unless shortened, three doc amendments; should follow T35's
  rewrite of the script.

A because e2e already boots every directive on both pins as a required check, and C-prime's main
value (a rejected directive on a pin bump) does not occur while the pins never move; the
Sentinel-branch data init is covered cheaper by the D19 unit exec harness T35 needs anyway.

**Answer:** _open_

## Not verified

- Whether any of the 120 calls of the shared e2e waits relies on returning while a pod terminates
  (Q1 = E); the full suite on both lines shows it.
- That the old Sentinels at site 2 stop answering within a second (one CI operator log).
- That a Sentinel-path replica-drain regression fails over as fast as the master drain: inferred.
- `pod_termination_test.go:220-221` picks the victim by the lagging `instanceRole` label; not
  observed, no work item until a Kind run logs the victim's `ROLE` next to the failover time.
- That `kubectl exec` forwards `valkey-cli -e` stderr and exits 1 (read in source); the Q2 revert
  check settles it. Whether any run ever hit a refused write at the 15 sites: no log searched.
- Where a second `SENTINEL FAILOVER` leaves the master among three pods; measured with two only.
- Every integration rate (class-A flakes, the blocked-pass 409, the lost failover phase) is
  estimated from the pass structure; the mutation checks settle the tests.
- That informers deliver CR versions in resourceVersion order (client-go reflector v0.37.1), and
  that no integration test relies on the cache barrier under Q3 = B.
- The unit guards of the non-foreign-object B3 sets, B4 and B5, and whether the named unit guards
  carry a recorded mutation check.
- The docker config measurements used hand-transcribed configs, not `GenerateValkeyConf` output.

## Related

- [T12](archive/012-the-master-handover-loses-no-acknowledged-write-and-no-dataset.md) (done) - refused the write fence (ADR 0038); its writer harness classifies replies itself, and it corrected the stale-master diagnosis, not the helpers.
- [T31](archive/031-generated-pods-run-as-root.md) - holds the reproduction of the fixed cache read.
- [T35](035-who-the-master-is-after-a-restart-or-failover.md) - source of the Sentinel lag figures; rewrites
  the Sentinel-branch data init with the D19 unit exec harness; its Pod informer is untouched by Q3.
- T40 - the citations of this ticket outside `docs/tickets/` are on its list too.
- [T43](043-static-checks-and-ci-gates-miss-tagged-tests-standing-constraints-and-chart.md) - lint and vet skip every file here.
- T45 - Renovate never processes `test/testimages/images.go`, so the Valkey pins do not move.
- T23 - `resetSentinelState` restarts `config-epoch` at 0; site 1 runs no roll.
- T40 - the ADR 0026 sentence about the drain `preStop` on every topology.
- [T18](018-cr-status-reporting-and-the-status-write.md) - the
  `persistStatus` 409; its R1 bears on Q5.
