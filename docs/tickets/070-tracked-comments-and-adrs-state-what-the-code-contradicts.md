---
id: T70
title: tracked comments, ADRs and pages state what the code and the 2026-09-27 measurements contradict
state: analysed       # every item re-verified at 84a39c2 by reading, three of them measured again (docker, gh api), 2026-09-27
severity: low         # no behaviour is wrong; a contributor or reviewer who trusts one of these records reasons from a false premise - the worst of them, ADR 0012 D9 "It loses no data", hides a write loss measured in docker on the non-Sentinel roll's promote-to-demote gap under client writes
security: hardening   # items (d) and (e) are statements in the security records (ADR 0013, ADR 0016, ADR 0012 Consequences, docs/security/secrets-and-tls.md); the corrections give no principal anything and take nothing from one, so not boundary and not live
threat: "no attacker; would additionally cover a reviewer of the threat model, who today reads that the auth password is readable from valkey-server's argv (measured false: the process scrubs argv and environ) and not that it sits in the argv of every probe's and init step's valkey-cli and, on a Sentinel topology after Sentinel's CONFIG REWRITE, in cleartext in /etc/valkey-active; and who reads a sidecar Role wider than the code grants (list, namespace-wide) - every carrier the corrected text names is readable only inside a container that already holds VALKEY_PASSWORD in its environment"
urgency: now          # rule 1, second clause, first match top-down: measured-false statements in tracked files - (a) and (b) measured in docker, (e) measured in docker, (g) read back from the GitHub API, all 2026-09-27; the first clause does not decide it, no item is a defect in an unreleased feature (the newest false sentence, item (i), is documentation written on main in 4a7543e)
effort: S             # about fifteen sentence-level edits in twelve tracked files (CLAUDE.md included): Go comments and one shell comment inside the non-Sentinel init script, ADR corrections marked in place with a Status line each; no behaviour change, no test change
blocked-by: human     # only Work list item 10, the CLAUDE.md sentence of item (g); every other item, the shell comment of item (a) included (ADR 0005 D11, Options), is decision-free and can land now
filed-from: re-verification of 2026-09-27 at 84a39c2 - moved from tickets 012 (Work list), 034 ("Outside this family"), 050 ("Findings for other tickets"), 056 (Fact and Work list item 2), 057 (the sentinelPodNeedsUpdate note), 058 ("Found while verifying, owned elsewhere") and 060 ("Cross-ticket, outside this ticket's scope")
opened: 2026-09-27
decided:              # not recorded - no decision yet
done:                 # not recorded - not done
---

# T70 - tracked comments, ADRs and pages state what the code and the 2026-09-27 measurements contradict

Filed on 2026-09-27 from the re-verification of every open ticket at `84a39c2`, which found these
records false or stale outside the decision of the ticket that found them and parked each in its
host (`filed-from`). One work list, because every item is the same kind of work - a sentence that
is corrected, not a behaviour that is changed - and none of them depends on another. This file
is now their record; the hosts ~~are to point here (at the review of this file, on 2026-09-27, none
of the seven hosts named T70 yet)~~ point here *(checked in the sweep of 2026-09-27: 012, 034, 050,
056, 057, 058 and 060 each link this file from the place the item was parked)*.

Every location below was re-read at `84a39c2` (`git diff --stat HEAD` shows no working-tree change
under `internal/`, `api/`, `test/`, `deploy/`, `.github/`, `docs/adr/`, `docs/security/`,
`DEVELOPER.md` or `CLAUDE.md`; `git status --porcelain` lists changes under `docs/tickets/` only). No make target, `go test`,
Kind cluster or kubectl was run for this file.

## Fact

### (a) "no replicas attached yet" holds only for `replicas: 2`

**Mechanism.** On the non-Sentinel rolling update, `promoteAndRedirect`
([rolling_update.go:4188-4249](../../internal/controller/rolling_update.go#L4188-L4249)) sends
`REPLICAOF NO ONE` to the promotion candidate, then demotes the outgoing master to a replica of it
(`:4218-4229`), then redirects every other reachable replica to it (`:4237-4246`; the loop skips
only `masterIdx` and `promotedIdx`). All of that happens before the outgoing master is deleted.
With `replicas: 3` or more the promoted pod therefore has replicas attached when the replaced pod
boots, and the master-discovery init script's Phase 1 (which accepts a peer only when it reports
`role:master` and `connected_slaves > 0`,
[statefulset.go:457-488](../../internal/builder/statefulset.go#L457-L488), the test at `:471`)
can find it. Two comments say otherwise, unconditionally:

- [statefulset.go:490-496](../../internal/builder/statefulset.go#L490-L496), the Phase 2 comment of
  that script: "At that moment the promoted pod is the only master and has no replicas attached
  yet, so Phase 1 rejects it".
- [rolling_update.go:4039-4043](../../internal/controller/rolling_update.go#L4039-L4043): "its
  peer-based discovery rejects the promoted pod, which has no replicas attached yet".

Both hold only for `replicas: 2`, where the promoted pod's one replica was the deleted pod. Phase 2
is still load-bearing on larger clusters - when no redirected replica has attached yet, or a
redirect failed (it is best-effort) - so the correction is the scope of the sentence, not the
removal of Phase 2. The tests that say the same are already scoped and stay:
[init_script_exec_test.go:142](../../internal/builder/init_script_exec_test.go#L142) ("with two
replicas"), [two_replica_failover_test.go:22](../../test/e2e/two_replica_failover_test.go#L22),
and the fixture comment at
[manual_failover_known_master_test.go:216](../../internal/controller/manual_failover_known_master_test.go#L216).

**The shell comment is part of the pod spec.** The script is the `Command` of the
`init-config-selector` init container ([statefulset.go:435-561](../../internal/builder/statefulset.go#L435-L561)),
built with `fmt.Sprintf` from one raw string, so the comment text is in the PodSpec.
`ComputePodSpecHash` is FNV-32a over the JSON of the whole built PodSpec
([statefulset.go:1228-1239](../../internal/builder/statefulset.go#L1228-L1239)), stamped as the
template's `pod-spec-hash` (`:154`), and a data pod whose recorded hash differs from the persisted
template's is outdated ([rolling_update.go:519](../../internal/controller/rolling_update.go#L519),
`:577`); `podSpecChanged` also compares init container commands (`:1295-1297`, `containerChanged`
`:1333`). Editing that comment therefore moves the pod-spec hash of every multi-replica data tier
without Sentinel - the only tier that carries this script (`IsMultiReplicaWithoutSentinel`,
[statefulset.go:399](../../internal/builder/statefulset.go#L399)).

**That tier rolls on every operator release anyway.** Every data pod carries the operator image
as its sidecar (`buildSidecarContainer`, [statefulset.go:989-996](../../internal/builder/statefulset.go#L989-L996);
`buildPodContainers` `:1044-1048`), the chart passes its own image as `--operator-image` and
`OPERATOR_IMAGE`
([deployment.yaml:39, :48-49](../../deploy/helm/valkey-operator/templates/deployment.yaml#L39)),
the image tag defaults to the chart `appVersion`
([values.yaml:8-9](../../deploy/helm/valkey-operator/values.yaml#L8-L9)), and the release build
stamps both `appVersion` and `image.tag` with the release version
([build.yml:175-182](../../.github/workflows/build.yml#L175-L182)). A new
release therefore changes the sidecar image, which `podImageChanged`
([rolling_update.go:485-499](../../internal/controller/rolling_update.go#L485-L499), through
`podNeedsUpdate` `:430` and `podOutdated` `:445-449`) and the pod-spec hash both see; the deferral
of a sidecar-only change (`isSidecarOnlyChange`, `:3845`) is asked only on the single-pod path.
[ADR 0005 D11](../adr/0005-upgrade-neutral-defaults-and-anti-affinity.md) (lines 297-313) records
this as decided: "every operator release already rolls every multi-replica data StatefulSet ... A
pod-spec change that rides along in that same pass adds nothing and earns no release note", with
one boundary - on the kustomize path and on a Helm install with a floating `image.tag` the sidecar
image does not move, a pod-spec change is a new roll there, and that path is "the deviating
admin's responsibility". ADR 0005's Residual risks (lines 471-475) already name init-script edits
as "not upgrade-neutral by construction", and its Alternatives Considered record extracting the
script into a ConfigMap to keep it out of the hash as not taken (lines 464-467). The shell
comment edit is such a ride-along; it is not an open decision (Options).

**Verified:** the code path above by reading at `84a39c2`; `git grep -n 'no replicas attached'`
finds the two unscoped comments above, and every other hit outside `docs/tickets/` is a test
comment scoped to its own fixture (two replicas, a sole master, a single-replica cluster). Measured
2026-09-27 (docker, Measurements M1): after the promote, demote and redirect sequence on three plain
`valkey-server` containers, the promoted pod reports `role:master` and `connected_slaves:2` (the
demoted old master and the redirected replica) on 9.1.1 and 8.1.9; with the old master deleted,
the redirected replica is the one left. The re-verification of ticket 012 measured the same
topology with the fence of that ticket: the promoted pod took its first write +0.23 s (9.1.1) and
+0.25 s (8.1.9) after a partial resynchronisation, idle; under a client write load the
redirected pods full-resynced behind `repl-diskless-sync-delay 5`
([configmap.go:179](../../internal/builder/configmap.go#L179)) and the promoted pod reported
`connected_slaves:2` with no good replica for about 5 s (first fenced write +5.69 s on 9.1.1,
+5.08 s on 8.1.9). The review of this file re-ran M1 once on 9.1.1 and read `connected_slaves:2`
on the promoted pod 4 s after the sequence while the demoted old master's log still ended at
`Trying a partial resynchronization` - so a replica whose sync has not completed counts toward
`connected_slaves`, and Phase 1 accepts the promoted pod then too. The per-release roll of the
tier (ADR 0005 D11) by reading `buildSidecarContainer`, `podImageChanged`, the chart and
`build.yml` at `84a39c2`.
**Not verified:** on Kubernetes, how often Phase 1 finds the promoted pod before Phase 2 is needed
on `replicas: 3`; the pod-spec hash change of a comment edit was derived by reading, not by
computing the hash before and after; the per-release roll of the tier was read, not observed on
a fleet upgrade for this file; whether every production install uses the chart with the default
`image.tag` (on a floating tag or the kustomize path the edit would be a roll of its own) was not
checked, no cluster was read.

### (b) `WAIT` does not drain a master of writes

**Mechanism.** `waitForWriteSync`
([rolling_update.go:2901-2977](../../internal/controller/rolling_update.go#L2901-L2977)) sends
`WAIT <n> <timeout>` on one connection. `WAIT` blocks only the client that sent it, until the
replicas acknowledge the offset of that client's own last write; every other client keeps
writing to the master before, during and after the call. Writes the master acknowledges between
`WAIT` and the demotion are not covered. Four places say otherwise:

- [rolling_update.go:4216-4217](../../internal/controller/rolling_update.go#L4216-L4217), in
  `promoteAndRedirect`: "the pod was drained of writes by waitForWriteSync before the promotion".
- [ADR 0012 D9](../adr/0012-the-sidecar-records-its-drain-promotion-on-the-pod.md), lines
  340-341: "It loses no data: the operator's `waitForWriteSync` (`WAIT`, same file, called
  immediately before the promotion) drained the pod of writes and it is deleted seconds later."
  *(Found 2026-09-27 while verifying this item; not in any host ticket.)*
- The doc comment of `waitForWriteSync`,
  [rolling_update.go:2901-2903](../../internal/controller/rolling_update.go#L2901-L2903): "to
  ensure all pending writes have been acknowledged by all replicas before failover. This prevents
  data loss that can occur during async replication when a failover happens." True for the writes
  acknowledged before the call only. *(Found 2026-09-27 while verifying.)*
- The call-site comment on the Sentinel path,
  [rolling_update.go:2715-2716](../../internal/controller/rolling_update.go#L2715-L2716): "This
  prevents data loss from async replication." *(Found 2026-09-27 while verifying.)* On that path
  the loss after the call is the separate, larger finding first recorded in ticket 012 and now
  filed as [T67](067-the-rolls-own-sentinel-failover-loses-acknowledged-writes.md) (about 9500
  acknowledged writes per forced failover at ~600 writes/s in docker), which is not this ticket's
  work; this ticket corrects only the claim that `WAIT` prevents it. T67's own text item corrects
  the "every roll is lossless" statements for the Sentinel path and names this item (b) for the
  `WAIT` comments and ADR 0012 D9; neither ticket edits the other's lines.

**Verified:** by reading at `84a39c2`. Measured 2026-09-27 (docker, Measurements M1): with a
writer inside the old master at roughly 200 acknowledged `SET`s per second, `WAIT 2 1000` answered
2, and after `REPLICAOF NO ONE` on r1, `REPLICAOF r1` on the old master and `REPLICAOF r1` on r2 -
the order of `promoteAndRedirect` - 1 of 1497 acknowledged keys (9.1.1) and 1 of 1524 (8.1.9)
were missing on the promoted pod; the old master logged `Full resync from primary`. Ticket 012's
run of the same sequence at ~250 writes/s lost 1 (9.1.1) and 2 (8.1.9).
**Not verified:** the size on Kubernetes, where the gap between the promote and the demote is two
operator round trips over the network and the TLS handshake, and client write rates differ.

### (c) The doc comment of `sentinelPodNeedsUpdate` starts mid-sentence

[rolling_update.go:4837](../../internal/controller/rolling_update.go#L4837) reads
`// differ from what the sentinel StatefulSet template specifies.` directly above
`func sentinelPodNeedsUpdate`. Its first line, `// sentinelPodNeedsUpdate returns true when the
running pod's container images`, was written in `73f6efe` (2026-03-02) and dropped in `3f0a1fe`
(2026-03-20, "Fix/reconcile logic (#44)"), found with `git log -S` on both lines. The function now
decides on four inputs, not images alone (`:4838-4873`): a container image differing by container
name, the `pod-spec-hash` annotation (with a resource comparison when the pod carries none), the
`config-hash` annotation, and the TLS material fingerprint (`podTLSMaterialHashChanged`).
**Verified:** by reading and `git log -S` at `84a39c2`. **Not verified:** nothing open.

### (d) The sidecar Role is recorded wider than the code grants

`BuildSidecarRole` grants `pods` verbs `get` and `patch`, restricted by `resourceNames`
([rbac.go:67-76](../../internal/builder/rbac.go#L67-L76)) to the names `SidecarRolePodNames`
returns - every data-pod ordinal of this cluster, desired or existing (`:97-121`) - and no rule at
all when that list is empty (`:58-65`). `get` was added on 2026-08-27 in `e32f0d2` for ADR 0028
D5a. Two ADRs state more:

- [ADR 0013 D3](../adr/0013-operator-is-cluster-wide-privileged.md), lines 167-168:
  "`BuildSidecarRole` grants `pods: get,list,patch` in one namespace". D3's conclusion - the
  sidecar Role is a strict subset of the chart ClusterRole's `pods: delete,get,list,patch,watch`
  ([clusterrole.yaml:51-59](../../deploy/helm/valkey-operator/templates/clusterrole.yaml#L51-L59))
  - holds, and holds more narrowly; only the verbs and the scope are wrong. `bcc63c9` corrected
  the same verbs in [privilege-footprint.md](../security/privilege-footprint.md) and
  [trust-boundaries.md](../security/trust-boundaries.md) and edited ADR 0013 in two other places,
  but not D3.
- [ADR 0012 Consequences](../adr/0012-the-sidecar-records-its-drain-promotion-on-the-pod.md),
  lines 451-460, in the present tense: "Cluster A's sidecar can patch cluster B's pods", and "The
  observer runs under the same ServiceAccount ... yields a token with namespace-wide
  `pods get,list,patch`". ADR 0012's own Status (lines 32-44, amended 2026-08-21) records that D8
  steps 2 and 3 shipped - the observer runs under its own Role-less ServiceAccount, the sidecar
  grant is on named pods only - but the two bullets were not marked, so they state the superseded
  reach as current. *(Found 2026-09-27 while verifying this item; not in any host ticket.)*

Checked and left: ADR 0013 lines 537-546 say "it is `pods: patch`" inside a correction dated
2026-08-21, which was true on that date and is followed by a 2026-08-27 closing note; a dated
record, not a current statement.
**Verified:** by reading at `84a39c2`, `git log -S '"get", "patch"'` (`e32f0d2`), and `git show
bcc63c9` for what that commit changed in ADR 0013. **Not verified:** nothing open for the wording.
Whether the sidecar Role can be created without `escalate` - the question D3 itself leaves open -
is gap H-2's and is not this ticket's; ticket 056 records that envtest starts its API server with
the RBAC authorizer (controller-runtime v0.25.1, `pkg/internal/testing/controlplane/apiserver.go:339`,
not overridden in [suite_test.go:63-65](../../test/integration/suite_test.go#L63-L65)), so an
integration test impersonating a ServiceAccount could answer it. The 056 run proposed this item
as the rule-1 slice of a new H-2 ticket; ~~no ticket carries H-2~~ H-2 is now
[T82](082-the-operator-is-granted-roles-escalate-and-bind-it-does-not-need.md) *(corrected in the
final pass of 2026-09-27)*, which leaves D3's verb list to this item. The wording correction needs
none of H-2's answer, so it stays here with the other record corrections.

### (e) ADR 0016 and the secrets page name the wrong carrier of the password in `/proc`

[ADR 0016 Consequences](../adr/0016-authentication-and-tls-posture.md), lines 195-198: "any
process inside the pod can read it from `/proc`, because it appears in `valkey-server`'s argv."
[secrets-and-tls.md:31-35](../security/secrets-and-tls.md) says the expanded password "appears in
the `valkey-server` process arguments", readable from `/proc` within that container. Three facts
contradict them:

1. **`valkey-server` does not keep it.** The container runs `sh -c 'exec valkey-server <conf>
   --requirepass "$VALKEY_PASSWORD" --masterauth "$VALKEY_PASSWORD"'`
   ([statefulset.go:822-833](../../internal/builder/statefulset.go#L822-L833)); after startup
   `/proc/1/cmdline` reads `valkey-server *:6379` and `/proc/1/environ` holds no copy of the
   password (`set-proc-title yes`, the default). Measured, M2.
2. **`valkey-cli` does.** The readiness and liveness probes run `sh -c 'valkey-cli -a
   "$VALKEY_PASSWORD" ping'` ([statefulset.go:1515-1531](../../internal/builder/statefulset.go#L1515-L1531)),
   every 5 s and every 10 s, and the init scripts pass `-a "$VALKEY_PASSWORD" --no-auth-warning`
   to their discovery calls (`cliAuthFlags`, `:265-268` for the data pod's init step on a Sentinel
   topology, which passes it only while Sentinel auth is not disabled, `:430-433` for the
   non-Sentinel one). While such a call runs, its `/proc/<pid>/cmdline` carries the
   expanded password. Measured, M2.
3. **After a `CONFIG REWRITE` the config file does.** On topologies with an init container
   (Sentinel, or multi-replica without Sentinel, `needsInitContainer`,
   [statefulset.go:645-663](../../internal/builder/statefulset.go#L645-L663)) the valkey container
   runs from a writable copy on an `emptyDir` at `/etc/valkey-active` (`:68`, `:232-238`). A
   `CONFIG REWRITE` writes `requirepass "<password>"` and `primaryauth "<password>"` into it,
   measured M2 (`primaryauth` is the name under which Valkey rewrites `masterauth`; ticket 050's
   facts run grepped for `masterauth` only and therefore reported the password line as
   `requirepass` alone). Sentinel sends `CONFIG REWRITE` after every `REPLICAOF` it issues
   ([sentinel.c 9.1.1:4858-4868, :4912-4913](https://github.com/valkey-io/valkey/blob/9.1.1/src/sentinel.c#L4858-L4868),
   `sentinelSendReplicaOf` starting at `:4868`;
   [8.1.9:4683, :4718-4719](https://github.com/valkey-io/valkey/blob/8.1.9/src/sentinel.c#L4683),
   both read 2026-09-27); ticket 036's run sent the same `CONFIG REWRITE` by hand to a
   writable config on both pins (its exp3), not through a Sentinel. Neither the operator nor the sidecar sends one (`grep -rn
   'REWRITE\|ConfigRewrite'` over `internal/` and `cmd/`, non-test files: no match; a
   case-insensitive `rewrite` finds only English comments), so on multi-replica clusters without
   Sentinel the file holds the password only if a client rewrites by hand. ADR 0016 lines 199-200
   name the rendered `sentinel.conf` as the one file that holds the password; this one is not
   named.

"Inside the pod" in ADR 0016 is also wrong: no generated pod shares its process namespace
(`grep -rn ShareProcessNamespace internal/ api/`: no match), so `/proc` shows a process only to its
own container. The secrets page already says so and is wrong only in its attribution.
Every carrier above is inside the valkey container or an init container, both of which carry
`VALKEY_PASSWORD` in their environment already, so the corrected record names no new reader.
**Verified:** M2 on both pins; the builder lines and the upstream source by reading.
**Not verified:** whether a Kubernetes exec probe's command line is visible anywhere outside the
container (the kubelet log, the runtime's event stream); not read and not measured.

### (f) ADR 0026 says the drain hook exists on every topology

[ADR 0026 Context](../adr/0026-a-pod-being-deleted-is-not-available.md), lines 164-170: "a
replica delete releases the drain `preStop` hook in about a second on every topology", and "60 s
is approachable only by a master whose drain failover is still running". `drainPreStop` and
`drainSignalVolumes` return nothing unless `IsMultiReplicaWithoutSentinel()`
([statefulset.go:746-749](../../internal/builder/statefulset.go#L746-L749), `:678-680`;
`replicas > 1 && !sentinel`, [valkey_types.go:1166-1168](../../api/v1/valkey_types.go#L1166-L1168)).
On a Sentinel topology and on a standalone pod there is no hook to release, and the data pod's
termination is `valkey-server`'s own shutdown, bounded by the 75 s grace period. The hook has
been gated this way since it was introduced (`bb0f127`, 2026-08-22); the sentence came later
(`360cb03`, 2026-08-25, `git log -S "on every topology"`), so it was never true for those
topologies. **Verified:** by reading and `git log` at `84a39c2`. **Not verified:** the termination
length of a Sentinel-topology data pod; not measured here.

### (g) The required status checks live in a ruleset, not in branch protection

Read 2026-09-27 with `gh api`: `repos/guided-traffic/valkey-operator/branches/main/protection`
answers 404 "Branch not protected". `repos/guided-traffic/valkey-operator/rulesets` lists one
ruleset, `main`, id 23985346, source the repository, enforcement active, target `~DEFAULT_BRANCH`,
created and last updated 2026-09-25T09:25 +02:00; `rules/branches/main` returns its three rules
only: `deletion`, `non_fast_forward`, `required_status_checks`. The required contexts are Code
Linting, Container Malware Scan, Cyclomatic Complexity, GoSec Security Scan, E2E Tests,
Integration Tests (envtest), Unit Tests, Vulnerability Check, Malware Scan (Source Code), Generated
Manifests Up To Date, Valkey Image Tools, Release Tooling - exactly D47's twelve, each bound to
`integration_id` 15368, which `gh api /apps/github-actions` answers as the `github-actions` app -
`strict_required_status_checks_policy` false; bypass actors `OrganizationAdmin` and one
`Integration` with actor id 5070048, both `always`; no pull-request (review) rule. Stale against
that:

- [ADR 0017 D47](../adr/0017-test-and-ci-policy.md), line 539: "A new gate job is added to
  branch protection in the same change". The rule holds; the mechanism is the ruleset.
- ADR 0017 Consequences, lines 1102-1104: "**Branch protection is repository state**".
- ADR 0017 Residual risks, lines 1310-1314: "The twelve contexts were set through the GitHub API
  on 2026-09-18 and verified by reading the endpoint back", and "diffing it against branch
  protection"; lines 1315-1317: "**`enforce_admins` is false and no review is required.** ... a
  repository admin can still merge past all twelve checks". `enforce_admins` is a setting of
  classic branch protection, which `main` does not have; the ruleset's equivalent is its bypass
  list above.
- [DEVELOPER.md:298-302](../../DEVELOPER.md): "Which of these branch protection requires is
  repository configuration ... the list could not be read back while this page was written (`gh`
  was not authenticated), so it is stated from the ADR, not verified against GitHub." It can be
  read back now and matches.
- [release.yml:482](../../.github/workflows/release.yml#L482): "Branch protection requires the
  status context "E2E Tests"." *(Found 2026-09-27 while verifying this item.)*
- [CLAUDE.md:392-393](../../CLAUDE.md): "Adding a job that can fail the build means adding it to
  branch protection in the same change". A `CLAUDE.md` edit needs Hans.

**Verified:** the four `gh api` reads above on 2026-09-27, read again by the review of this file
the same day with the same result; the file lines by reading at
`84a39c2`; `git grep -n -i "branch protection"` outside `docs/tickets/` finds exactly the lines
listed. **Not verified:** whether the 2026-09-18 API call wrote classic branch protection that
was later replaced by the ruleset created on 2026-09-25 (then the Residual-risks sentence is
accurate as history and only its present tense is stale), or wrote nothing that still exists;
which GitHub App actor id 5070048 is; whether a repository admin who is not an organization
admin can bypass the ruleset.

### (h) ADR 0017 D52 pins a version that moved

[ADR 0017 D52](../adr/0017-test-and-ci-policy.md), line 835: "The module sits at the
`k8s.io/api` version (v0.37.0 for both)." [go.mod](../../go.mod) pins `k8s.io/api v0.37.1`
(line 11) and `k8s.io/pod-security-admission v0.37.1` (line 14) since `7017676` (2026-09-27, #225),
which moved them together. Only the number is stale; the rule holds. Checked and left: ADR 0013
line 36, ADR 0033 lines 419 and 658, `docs/security/seccomp-profiles.md:61` and ADR 0017 line 72
name v0.37.0 as the version something was read in or failed against, which is a dated record.
**Verified:** by reading at `84a39c2`. **Not verified:** nothing open.

### (i) The isolation page misdescribes ADR 0020 after its correction

[isolation-and-tenancy.md:192-193](../security/isolation-and-tenancy.md), the last sentence of the
2026-09-27 correction block on the three pod doors that closed on 2026-08-22: "ADR 0020 says the
sidecar grant needed only the label set, which leaves out the name filter." `bcc63c9` corrected
ADR 0020 on exactly that point: its Status amendment of 2026-09-27
([ADR 0020](../adr/0020-write-only-what-the-operator-owns.md), lines 18-25) says the Context
bullet and the Alternatives entry are struck and corrected in place, to "the label set plus a
name of the `<cr>-<ordinal>` form". The page's sentence now reports a statement the ADR no longer
makes. The page's sentence was written in `4a7543e` and was true then; `bcc63c9`, later the same
day, corrected ADR 0020 in its Status, its Context (lines 171-175) and its Alternatives entry
(lines 701-702), and left the page as it was. **Verified:** by reading both files at `84a39c2`
and `git log -S "which leaves out"`. **Not verified:** nothing open.

### (j) The doc comment of `ExecMulti` says it returns the last error and suggests a transaction

*(Added in the final pass of 2026-09-27; found by the filing of
[T71](071-maxmemory-is-never-set-so-an-oom-kill-is-the-only-memory-bound.md), whose review
corrected its own "in one `MULTI`" in place.)*
[client.go:330-332](../../internal/valkeyclient/client.go#L330-L332): "All responses are read and
the last non-OK error is returned." The loop at
[client.go:348-357](../../internal/valkeyclient/client.go#L348-L357) returns on the first error:
a failed write returns at `:349-351`, a failed read at `:352-355`, and `readFullResponse` turns
every `-` reply into an error ([client.go:479-481](../../internal/valkeyclient/client.go#L479-L481)),
so the commands behind a rejected one are neither sent nor read. That is the intended behaviour:
[exec_test.go:252](../../internal/valkeyclient/exec_test.go#L252),
`TestExecMulti_StopsAtTheFirstRejectedCommand`, asserts that the command behind the failing one
is not sent. The name also suggests a `MULTI`/`EXEC` transaction; the function sends neither - it
writes each command on one connection and reads one reply per command, so the sequence is not
atomic. Its one production caller is the observer's `SELECT` + `SET` write test
([checks.go:110-116](../../internal/observer/checks.go#L110-L116)). The comment has been false
since the function was written: `c6f97e2` (2026-03-20) already returned on the first error (`git
show c6f97e2:internal/valkeyclient/client.go`, `:288-298`). **Verified:** by reading at
`84a39c2`, `git log -S 'last non-OK error'` and `git grep -n ExecMulti` (the observer is the only
non-test caller; the sidecar names it only in a comment,
[labeler.go:301](../../internal/sidecar/labeler.go#L301)). **Not verified:** nothing open; no
test was run for this item.

### (k) The trust-boundaries diagram names a ClusterRole the chart never creates

The diagram in [trust-boundaries.md:20-54](../security/trust-boundaries.md), at lines 23-30, draws
the operator's ServiceAccount `<release>` bound through a ClusterRoleBinding to a ClusterRole
named `valkey-operator-role`. The chart's ClusterRole is named `valkey-operator.fullname`
([clusterrole.yaml:4](../../deploy/helm/valkey-operator/templates/clusterrole.yaml#L4)), and the
chart's ClusterRoleBinding carries the same name and refers to that ClusterRole
([clusterrolebinding.yaml:4, :9](../../deploy/helm/valkey-operator/templates/clusterrolebinding.yaml#L4-L9));
the fullname is the release name, joined with the chart name unless the release name already
contains it, or `fullnameOverride`
([_helpers.tpl:11-22](../../deploy/helm/valkey-operator/templates/_helpers.tpl#L11-L22)). No chart
template names `valkey-operator-role`; the name is the one `controller-gen` writes into the
generated [role.yaml:5](../../config/rbac/role.yaml#L5). The principals table on the same page
already describes the chart's binding correctly (line 12); only the diagram's box label is wrong.
The label came with the diagram in `a0ac61f` (2026-08-21, then in `SECURITY_ARCHITECTURE.md`) and
moved to this page in `4a7543e`. **Verified:** by reading at `84a39c2` and `git grep -n
valkey-operator-role` (outside `docs/tickets/`: the diagram line, `role.yaml:5` and the
`Makefile` rule that generates it). **Not verified:** nothing open; no chart was rendered.

## Impact

- **(b)** is the one with a consequence beyond wording: ADR 0012 D9 is the record a contributor
  reads before touching `promoteAndRedirect`, and it says the non-Sentinel roll loses no data. It
  loses the writes acknowledged between the promote and the demote - 1 to 2 per roll in docker at
  ~200-250 writes/s, silently to the client. Anyone weighing a change there (ticket 012's fence,
  for one) starts from zero where the measured answer is not zero.
- **(a)** misleads a reader of the discovery script about when Phase 2 runs; a simplification
  that treats Phase 2 as the only path on every size, or removes it because Phase 1 "usually"
  finds the master, would be argued from a false premise.
- **(e)** puts the password's `/proc` exposure on the wrong process and omits the cleartext file,
  so a hardening that moved the password off the command line of `valkey-server` alone (it is
  already off) would look like a fix and change nothing.
- **(d)** overstates the sidecar grant; a reviewer sizing the RBAC footprint reads `list` and a
  namespace-wide scope that do not exist.
- **(g)** sends the next person who adds a gate job to the classic branch-protection endpoint,
  where the change has no effect, and repeats the "could not be read back" caveat that no longer
  applies - the D47 failure mode the ADR was written against.
- **(c)**, **(f)**, **(h)**, **(i)**: a reader gets a truncated, a too-broad, a stale or a
  misattributed statement; no decision rests on them today.
- **(j)** tells a caller of `ExecMulti` that every reply is read and that the error is the last
  one, and its name suggests atomicity; a new caller that relied on either would be wrong. The
  one caller today, the observer's write test, is not affected: it fails on the first rejected
  command either way.
- **(k)** gives a reader of the diagram a ClusterRole name that a `kubectl get clusterrole` on a
  chart install does not find; the grant it stands for is described correctly by the table above
  it.
- **Security** (items (d) and (e)): hardening, no principal. The threat line in full: the
  corrected records let a reviewer of the threat model see where the password actually sits
  (valkey-cli argv during probes and init steps; `/etc/valkey-active` after a Sentinel rewrite)
  and what the sidecar token actually carries; every carrier named is inside a container that
  already holds the password in its environment, so nothing becomes readable that is not today,
  and nothing live or dormant changes with the edit.

## Options

No decision is open. The one choice the filing draft of this file put up - whether the shell
comment of item (a) may be rewritten although it is hashed - is decided by ADR 0005 D11, and the
analysis is kept here so the next reader does not reopen it.

**What the code does today.** The false sentence sits in a `#` comment inside the shell script
that `fmt.Sprintf` renders into the `init-config-selector` container's `Command`. That command is
part of the PodSpec whose JSON `ComputePodSpecHash` digests, so any change to the comment's text
changes the `pod-spec-hash` of every multi-replica data StatefulSet without Sentinel. A Go `//`
comment beside the `fmt.Sprintf` is not in the PodSpec and changes no hash. The same tier already
rolls on every operator release, because its sidecar runs the operator image and the release
stamps a new tag into it (Fact (a), ADR 0005 D11). The choice decides only whether the corrected
sentence rides that roll or waits for a later script change; it does not change what the script
does on any path.

- **A - rewrite the shell comment in the same change as the other items (recommended).** Scope
  the Phase 2 sentence to `replicas: 2` and name the other case (no redirected replica attached
  yet, or a failed best-effort redirect). Cost on the canonical Helm path: none beyond the roll
  the release performs anyway - the edit changes the hash in the same pass in which the new
  sidecar image already outdates every pod of the tier, one controlled failover per cluster as
  on every release. Cost on the kustomize path and on a Helm install with a floating `image.tag`:
  one roll that release would otherwise not have performed, which ADR 0005 D11 assigns to the
  deviating admin. The false sentence disappears from source and from the pod specs the roll
  creates.
- **B - correct it beside the script, rewrite the script text with a later script change.** A Go
  comment directly above the `fmt.Sprintf` of the non-Sentinel discovery script
  ([statefulset.go:435-440](../../internal/builder/statefulset.go#L435-L440)) states the correct
  scope of Phase 2 and says the shell comment is kept verbatim until the next edit of this
  script. Cost: the false sentence stays in source and in running pod specs, and a reader of the
  script, 50 lines below the Go comment, still meets it; a second comment has to be removed later.
  Its one advantage is that the release shipping it moves no hash on the kustomize or
  floating-tag path.

**A is recommended** because on the path ADR 0005 D11 calls canonical the edit costs nothing -
the release rolls that tier anyway - and A removes the false sentence where the reader meets it,
while B keeps it and adds a second comment that exists only to qualify the first. The
case for B is the non-canonical install path, where the edit would be a roll of its own; ADR 0005
D11 already decided that a pod-spec change riding a release is not held back for that path and
earns no release note, and its Residual risks accept that init-script edits are not
upgrade-neutral, so B's advantage is one the repository has declined to buy for larger changes
than a comment. Rejected: dropping the shell comment's claim without replacing it (the same hash
change as A, less information), moving the explanation to `docs/developer/` (the reader of the
script does not look there), and extracting the script into a ConfigMap to keep it out of the
hash (ADR 0005 Alternatives Considered, not taken; a re-decision far beyond a comment fix).

## Decision

None is needed: item (a) follows ADR 0005 D11 (Options, A). Work list item 10 waits on Hans for
the `CLAUDE.md` edit, which is not a decision.

## Work list

**Decision-free, can land now** (ADR edits follow the ADR rules in `CLAUDE.md`: the false sentence
is struck in place with a dated correction, and the ADR's `## Status` gains a dated "Amended
2026-..-..: correction, no decision changes" line naming what was corrected and how it was
verified; the ADR index row is touched only if its State changes, which none of these does):

1. **(a)** Rewrite [rolling_update.go:4039-4043](../../internal/controller/rolling_update.go#L4039-L4043)
   to say the returning pod's peer discovery rejects the promoted pod when it has no replica
   attached - with `replicas: 2` always, on larger clusters until a redirected replica attaches -
   and that the recorded address covers that case. Rewrite the Phase 2 comment in the script
   ([statefulset.go:490-496](../../internal/builder/statefulset.go#L490-L496)) to the same scope
   (Options, A); it moves the pod-spec hash of multi-replica data tiers without Sentinel, which
   roll on the release anyway (ADR 0005 D11), so no release note.
2. **(b)** Rewrite [rolling_update.go:4216-4217](../../internal/controller/rolling_update.go#L4216-L4217):
   the demotion is best-effort because the pod is deleted moments later; writes the pod
   acknowledged after `waitForWriteSync`'s `WAIT` and before this demotion are not on the promoted
   pod and are lost (measured 1-2 per roll in docker). Scope the doc comment at `:2901-2903` and
   the call-site comment at `:2715-2716` to "the writes acknowledged before the call"; if
   [T67](067-the-rolls-own-sentinel-failover-loses-acknowledged-writes.md) changes that path
   first, its change rewrites `:2715-2716` and this item drops that line; coordinate the wording
   with T67's text item, which owns the Sentinel-path "lossless" statements. Strike ADR 0012 D9's "It loses no data
   ..." (`0012:340-341`) and state the measured gap, with the M1 method and date.
3. **(c)** Restore a complete doc comment on `sentinelPodNeedsUpdate`
   ([rolling_update.go:4837](../../internal/controller/rolling_update.go#L4837)) naming its four
   inputs: container image by name, the `pod-spec-hash` annotation with the resource fallback,
   the `config-hash` annotation, the TLS material fingerprint.
4. **(d)** ADR 0013 D3 (`0013:167-168`): strike "`pods: get,list,patch` in one namespace", state
   `pods: get, patch` restricted by `resourceNames` to this cluster's data-pod names, and keep the
   strict-subset conclusion. ADR 0012 Consequences (`0012:451-460`): mark both bullets superseded
   in place by D8 steps 2 and 3 (observer on its own Role-less ServiceAccount; the sidecar grant
   on this cluster's named data pods, `get` added 2026-08-27 for ADR 0028 D5a), pointing at
   [privilege-footprint.md](../security/privilege-footprint.md#the-per-instance-sidecar-role)
   for the current grant. D3's paragraph is also the target of
   [T82](082-the-operator-is-granted-roles-escalate-and-bind-it-does-not-need.md) Work list item
   4; whichever lands second rebases onto the other.
5. **(e)** ADR 0016 Consequences (`0016:195-198`) and
   [secrets-and-tls.md:31-35](../security/secrets-and-tls.md): the password is not in
   `valkey-server`'s argv after startup; it is in the argv of every probe's and init step's
   `valkey-cli` while that runs, readable from `/proc` within that container only; add, next to
   the rendered `sentinel.conf` (`0016:199-200`), the data pod's `/etc/valkey-active`, which holds
   `requirepass` and `primaryauth` in cleartext after a `CONFIG REWRITE` - on Sentinel topologies
   after every Sentinel `REPLICAOF`. Keep the page's conclusion (standard pattern, not a secret
   store).
6. **(f)** ADR 0026 Context (`0026:164-170`): scope the release-in-about-a-second and the 60 s
   cap to multi-replica clusters without Sentinel, and say the other topologies have no drain
   hook.
7. **(g)** ADR 0017 D47 (`0017:539`), Consequences (`0017:1102-1104`) and Residual risks
   (`0017:1310-1317`), [DEVELOPER.md:298-302](../../DEVELOPER.md) and the comment at
   [release.yml:482](../../.github/workflows/release.yml#L482): name the repository ruleset
   `main` (id 23985346) as the place the twelve contexts live, how to read it back
   (`gh api repos/guided-traffic/valkey-operator/rules/branches/main`), the 2026-09-27 read-back
   that matched D47's list, and the bypass list in place of `enforce_admins`. The 2026-09-18
   sentence stays as history, dated, with the unknown in Not verified above stated.
8. **(h)** ADR 0017 D52 (`0017:835`): drop the number - "sits at the same version as
   `k8s.io/api`; the `k8s-go-modules` group moves both" - rather than writing v0.37.1, which the
   next Renovate bump makes stale again (v0.37.0 lasted until the day it was written down here).
9. **(i)** [isolation-and-tenancy.md:192-193](../security/isolation-and-tenancy.md): replace the
   last sentence with the fact that ADR 0020's Context and Alternatives were corrected on
   2026-09-27 to the same reading (Status, `0020:18-25`), or drop it.

**Needs Hans:**

10. **(g)** [CLAUDE.md:392-393](../../CLAUDE.md): "adding it to branch protection" becomes
    "adding it to the required status checks of the repository ruleset `main`".

**Decision-free, added in the final pass of 2026-09-27, can land now** (numbered after item 10 so
the references to item 10 stay valid):

11. **(j)** Rewrite the doc comment of `ExecMulti`
    ([client.go:330-332](../../internal/valkeyclient/client.go#L330-L332)): the commands are sent
    and answered one after another on one connection; the first command that fails to send, or
    whose reply is an error, ends the sequence and its error is returned, and the commands behind
    it are not sent; no `MULTI`/`EXEC` is sent, so the sequence is not atomic. A comment edit
    only - renaming the function is not part of this item, it would touch the caller and the
    tests for no change in behaviour.
12. **(k)** In the diagram of [trust-boundaries.md](../security/trust-boundaries.md) (line
    24), replace the box label `valkey-operator-role` with `<release>`, the placeholder the page
    already uses for the chart's fullname in the principals table and in the ServiceAccount box
    (the chart names its ServiceAccount, by default, and its ClusterRole with that same fullname),
    keeping the box width. No other text of the page changes for this item.

## Verification

- `git grep -n 'no replicas attached yet'` outside `_test.go` and `test/e2e/` finds only
  scoped sentences.
- `git grep -n 'drained of writes\|drained the pod of writes\|It loses no data'` finds nothing
  outside `docs/tickets/`.
- `git grep -n -i 'branch protection'` outside `docs/tickets/` finds only dated history sentences
  (and `CLAUDE.md:392` until item 10 lands).
- `git grep -n "get,list,patch" docs/adr/0013-operator-is-cluster-wide-privileged.md` finds only
  the struck D3 text and the dated 2026-08-21 correction; the ADR 0012 bullets carry a
  superseded marker.
- `git grep -n "valkey-server[^ ]*s argv\|valkey-server. process arguments"` finds only struck
  text.
- ADR 0016, 0012, 0013, 0017 and 0026 each carry a dated Status line for the correction.
- `git grep -n 'last non-OK error'` finds nothing outside `docs/tickets/` (item 11).
- `git grep -n valkey-operator-role -- docs/` finds nothing outside `docs/tickets/` (item 12).
- **No behaviour changes, so no mutation or revert check applies** (ADR 0017's checks are for code
  that decides something). What proves that instead: `git diff -U0 -- internal/ .github/` touches
  only `//` and `#` comment lines, the `#` lines all inside the Phase 2 comment of the
  non-Sentinel init script. `make lint` and `make test-unit` green (the init-script exec test,
  [init_script_exec_test.go](../../internal/builder/init_script_exec_test.go), runs the script)
  as the ordinary gate. No test pins the comment text or the hash of a built pod spec at
  `84a39c2` (`git grep -n "Phase 2: No master"` finds only `statefulset.go`; the eight-hex hash
  literals in the tests are fixture values); if one does when the work lands, it changes with the
  comment.

## Measurements 2026-09-27 at `84a39c2`

Docker, `docker run --rm`, local images `valkey/valkey:9.1.1` and `valkey/valkey:8.1.9`, containers
and networks named `vko-file-070-*`, all removed afterwards (checked with `docker ps -a` and
`docker network ls` on that prefix). The scripts lived in the session scratchpad; their commands
are reproduced here so they can be rebuilt.

**M1 - the promote-to-demote gap of `promoteAndRedirect` (items (a), (b)).** Per image: one
network; three containers `m`, `r1`, `r2` running `valkey-server --save '' --appendonly no`;
`valkey-cli REPLICAOF m 6379` on r1 and r2, 3 s settle (`m` reports `connected_slaves:2`). A
writer inside `m`:
`i=0; : > /data/ack; while [ $i -lt 100000 ]; do r=$(valkey-cli SET load:$i $i); [ "$r" = OK ] && echo $i >> /data/ack; i=$((i+1)); done`.
After 3 s, `valkey-cli WAIT 2 1000` on `m`, then from inside `r1`, in one shell:
`valkey-cli REPLICAOF NO ONE; valkey-cli -h m REPLICAOF r1 6379; valkey-cli -h r2 REPLICAOF r1 6379`.
After 4 s the writer was stopped, `INFO replication` read on r1, and every acknowledged index
checked with `sed 's/^/EXISTS load:/' ack | valkey-cli` against r1, counting `0` replies.

| Image | `WAIT` reply | r1 after the sequence | acknowledged | missing on r1 | old master log |
|---|---|---|---|---|---|
| 9.1.1 | 2 | `role:master`, `connected_slaves:2` | 1497 | 1 | `Full resync from primary` |
| 8.1.9 | 2 | `role:master`, `connected_slaves:2` | 1524 | 1 | `Full resync from primary` |

The writer's `SET`s stop being acknowledged at the demotion (`m` answers `READONLY` from then
on), so stopping it later changes no count. Re-run by the review of this file, once, on 9.1.1 with
the same commands (`vko-file-070rv`, `vko-file-070rw`, removed): `WAIT` 2, r1 `role:master` and
`connected_slaves:2`, 1498 acknowledged, 1 missing on r1. The old master's log read 4 s after the
sequence ended at `Trying a partial resynchronization` with no `Full resync from primary` line
yet - the full resync waits for `repl-diskless-sync-delay`, 5 s by default - so the last column
depends on how long after the demotion the log is read; the missing count does not.

**M2 - where the password is readable (item (e)).** Per image:
`docker run -d --user 999:999 -e VALKEY_PASSWORD=Pw-0123456789 --entrypoint sh <image> -c 'printf "port 6379\ndir /data\n" > /data/valkey.conf; exec valkey-server /data/valkey.conf --requirepass "$VALKEY_PASSWORD" --masterauth "$VALKEY_PASSWORD"'`,
then `tr '\0' ' ' < /proc/1/cmdline`, `tr '\0' '\n' < /proc/1/environ | grep -c Pw-0123456789`, a
background `valkey-cli -a "$VALKEY_PASSWORD" --no-auth-warning BLPOP nokey 5` and a scan of
`/proc/[0-9]*/cmdline` for it, and `valkey-cli -a ... CONFIG REWRITE` followed by reading
`/data/valkey.conf`.

| Image | `/proc/1/cmdline` | password in `/proc/1/environ` | `valkey-cli` cmdline | config after `CONFIG REWRITE` |
|---|---|---|---|---|
| 9.1.1 | `valkey-server *:6379` | 0 matches | `valkey-cli -a Pw-0123456789 --no-auth-warning BLPOP nokey 5` | `requirepass "Pw-0123456789"`, `primaryauth "Pw-0123456789"` (none before) |
| 8.1.9 | `valkey-server *:6379` | 0 matches | the same | `primaryauth "Pw-0123456789"`, `requirepass "Pw-0123456789"` |

The re-verification of ticket 050 measured the first two columns the same way on both pins, with
`CONFIG GET set-proc-title` answering `yes`, and a `valkey-cli -a "$VALKEY_PASSWORD" -r 20 -i 1
ping` in the same container read back as `valkey-cli -a Pw-0123456789 -r 20 -i 1 ping` from its
`/proc/<pid>/cmdline` (9.1.1). The review of this file re-ran M2 on both pins with a 3 s `BLPOP`
and got every column of the table again, `primaryauth` and `requirepass` both written by the
rewrite.

**M3 - the required checks (item (g)).** `gh api repos/guided-traffic/valkey-operator/branches/main/protection`
(404 "Branch not protected"), `gh api repos/guided-traffic/valkey-operator/rulesets`,
`gh api repos/guided-traffic/valkey-operator/rulesets/23985346` and
`gh api repos/guided-traffic/valkey-operator/rules/branches/main`; results in item (g). Ticket
058's re-verification read the same ruleset with the same result on the same day.

## History

- 2026-09-27: filed from tickets 012 (Work list, "file as tickets of their own": the two "no
  replicas attached yet" comments and the "drained of writes" comment), 034 ("Outside this family
  - needs its own ticket file": ADR 0026:164-165), 050 ("Findings for other tickets": the ADR 0016
  and `secrets-and-tls.md` `/proc` statement and the cleartext `/etc/valkey-active`), 056 (Fact and
  Work list item 2: ADR 0013 D3's verbs), 057 (the truncated `sentinelPodNeedsUpdate` comment),
  058 ("Found while verifying, owned elsewhere": the ruleset against ADR 0017 D47, `DEVELOPER.md`
  and `CLAUDE.md`, and ADR 0017 D52's v0.37.0) and 060 (the `isolation-and-tenancy.md:192-193`
  sentence) during the re-verification at `84a39c2`; the raw results of that run are folded in
  above. Security class `hardening` rather than the `none` the 050 run proposed for its item,
  because items (d) and (e) are statements in the security records; no item is boundary or live.
  Re-verified now, every item at `84a39c2` by reading, M1 and M2 measured again in docker on both
  pins, M3 read from the GitHub API. Corrected against the hosts: the `sentinelPodNeedsUpdate`
  comment was written whole in `73f6efe` and truncated in `3f0a1fe` (2026-03-20), not "since
  `73f6efe`" as ticket 057 says; the promote-to-demote loss re-measured as 1 on both pins where
  ticket 012 recorded 1 and 2. New while verifying, same kind of record, added here: ADR 0012 D9's
  "It loses no data" (`0012:340-341`) and the two `waitForWriteSync` comments at `:2715-2716` and
  `:2901-2903` (item (b)); ADR 0012 Consequences `0012:451-460` (item (d)); ADR 0017 Consequences
  `0017:1102-1104`, the `enforce_admins` residual `0017:1315-1317` and `release.yml:482` (item
  (g)). Found that the Phase 2 comment of item (a) is inside the hashed init script, so its edit
  rolls a fleet tier; that is the ticket's one open decision (Options, B recommended). No item was
  dropped: all nine hold at `84a39c2`. Item (i) is described from tracked files alone.
  Adversarial review the same day, which superseded the decision stated in the sentence before
  last: the tier that carries the init script already rolls on every operator release, because
  its sidecar runs the operator image and the release stamps a new tag (ADR 0005 D11, read at
  `buildSidecarContainer`, `podImageChanged`, the chart and `build.yml:175-182`), so the shell
  comment edit rides that roll and no decision is open; the superseded text, verbatim: "**B is
  recommended** because the sentence misleads only someone reading the source, and B reaches that
  reader in the same function at zero cost to any running cluster, while A spends a failover of
  every non-Sentinel multi-replica cluster - with a write loss this ticket measured - to change
  text that no process reads." Options now mark A, the Work list folds the shell comment into
  item 1 (the former item 10 is gone, the `CLAUDE.md` item is now 10), `blocked-by` moved from
  `decision` to `human`. Also added by the review: ticket 012's measurement that a redirected
  replica in a pending full sync counts toward `connected_slaves` (and a docker re-run of M1 that
  shows the same), the ADR 0005 citations, the 8.1.9 `sentinel.c` lines, ticket 036's hand-sent
  rewrite, why ticket 050 saw no `masterauth` line (Valkey writes it as `primaryauth`), the
  Sentinel-auth condition on the Sentinel-topology init step's `-a`, the `github-actions`
  integration id of the required checks, the origin commits of item (i), ticket 056's envtest
  pointer for H-2, and T67 in place of "the finding recorded in ticket 012" in item (b), with the
  line split T67 already states; the claim that the hosts keep a pointer here was corrected, none
  did; the `rewrite` grep of item (e), which a case-insensitive search contradicts
  through English comments, was replaced by a case-sensitive one. Re-run by the review: M2 on both
  pins, M1 once on 9.1.1, M3 against the GitHub API, all matching.
  - Sweep: All seven hosts (012, 034, 050, 056, 057, 058, 060) now link this file from where each
    item was parked, so the opening paragraph's "none of the seven hosts named T70 yet" is struck.
    Frontmatter unchanged.
  - Final pass: added items (j), the `ExecMulti` doc comment that says the last error is returned
    where the loop returns on the first and whose name suggests a `MULTI` none is sent (found by
    the filing of T71), and (k), the trust-boundaries diagram that labels the chart's ClusterRole
    `valkey-operator-role`, a name no chart template creates, each with a Fact section, an Impact
    bullet, a Work list item (11 and 12, numbered after item 10 so its references stay valid) and a
    Verification grep, both verified at `84a39c2` by reading, `git grep` and `git log -S`; replaced
    the stale "no ticket carries H-2" in item (d) by a pointer to T82 and added to Work list item 4
    the rebase note T82 states for D3's paragraph. Frontmatter unchanged: both new items are
    comment- and label-level edits in two more tracked files, so severity low, class hardening
    ((k) is in a security page and changes no grant), urgency now and effort S stay; the effort
    comment's count of fifteen edits in twelve files covers items (a)-(i).
