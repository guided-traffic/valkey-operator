# ADR 0032: Generated pods run rootless, and existing clusters move with the operator upgrade

## Status

Accepted. Date: 2026-09-26. Decided by Hans on the T31 analysis
([`local_T31-generated-pods-run-as-root.md`](../tickets/local_T31-generated-pods-run-as-root.md));
released only together with [ADR 0026](0026-a-pod-being-deleted-is-not-available.md) D11 (T32),
because this is the first change that rolls every cluster of a fleet automatically.

Implemented:

- the posture on every data, Sentinel and observer pod, applied by one walk
  ([`pod_security.go`](../../internal/builder/pod_security.go));
- the pre-flight `check-data-writable` on every persistent data pod, and the migration-only
  `fix-data-ownership` repair while a data pod an earlier operator built still exists
  ([`pod_security_migration.go`](../../internal/controller/pod_security_migration.go));
- the single-pod rule and its condition `PodSecurityUpdatePending`;
- securityContext in the drift comparisons of both StatefulSets and of the observer
  Deployment;
- the second roll of the pods that carry the retired repair (`podCarriesRetiredRepair`, asked
  through `podOutdated` at every data-tier site of
  [`rolling_update.go`](../../internal/controller/rolling_update.go)).

Reviewed adversarially on 2026-09-26 before release; the review tightened D2 (a best-effort
repair), D3 (persistence from the persisted StatefulSet; rotated TLS material and changed
configuration are not deferred) and D4 (the template as evidence; "migrated" means past the
repair *(since 2026-09-26: Ready, and no data-tier roll recorded — the measured amendment
below)*), and found that the pods created during the migration keep the repair in their spec
(D2, D6) — recorded here as a consequence, with a second roll as the open alternative
*(decided 2026-09-26 for the second roll, next paragraph)*.

Amended 2026-09-26, after the first commit of this ADR (`bb6c78f`): Hans decided the two
questions it had left open.

- **The pods the migration creates are replaced by a second roll** (D2, D3, D6, Consequences).
  Once the repair has left the template, a data pod that still carries it is outdated for that
  alone. The text of `bb6c78f` left these pods to their next replacement and named the second
  roll as the open alternative; it was never released, so it is corrected in place below, each
  correction marked with its date. The option that lost is under *Alternatives Considered*.
- **A tier of one or two Sentinels rolls serially** — decided in
  [ADR 0024](0024-the-sentinel-tier-reports-its-own-completion.md) D10 and recorded here for its
  reach: this ADR rolls every Sentinel tier once, and such a tier, whose quorum equals its size,
  refused the delete of every Ready Sentinel and never finished that roll
  ([ADR 0026](0026-a-pod-being-deleted-is-not-available.md), *Residual risks*).

Amended again 2026-09-26, on a measurement: **the second roll starts only after the first has
finalized** (D4). The first fleet-upgrade run with the second roll counted one
`RollingUpdateComplete` per persistent tier instead of two — the repair had left the template on
the last replacement's pre-flight, `reconcileStatefulSet` runs before the rolling update in the
same pass, and the first roll's state was discarded as stale before it finalized. D4's
migration evidence is now Ready rather than past the pre-flight, and the repair stays while a
data-tier roll is recorded. The second fleet-upgrade run measured what that gate does on its own:
the repair stayed in the template after the first roll and the second roll never started, because
the pass that may remove it is the one after the completion, and a completing pass schedules
none. `finishDataRoll` now requests that pass (D4). ~~The fleet-upgrade e2e on both fixes: not yet
run.~~ *(Run 2026-09-26: green from 1.12.8, with exactly two `RollingUpdateComplete` per
persistent tier and nothing rolling after the second roll — see the verification update below.)*

Amended 2026-09-26 by
[ADR 0033](0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md), in the
same unreleased release: D1 gains a pointer to what the walk now sets beyond the list here (a
seccomp profile configurable as `RuntimeDefault` or `Localhost`, an opt-in user namespace,
`privileged: false`, `enableServiceLinks: false`); D1's observer identity and its "no CRD field"
are marked in place; D5 gains a second exact comparison (`hostUsers`); *Residual risks* gains the
repair under a user namespace, which nothing has measured. *(Amended again 2026-09-26 by ADR 0033
D9:)* a `Localhost` profile reaches the pods only when the operator's allow-list
(`--allowed-seccomp-localhost-profiles`, empty by default) names that exact path; D1's two notes
on the profile say so.

Verified on a node locally, not in CI, and said so: ~~the branch has not been through the
pipeline~~ *(corrected 2026-09-26: pushed as `e2ce8bb`, where two gate jobs failed —
`Generated Manifests Up To Date` on a stale local controller-gen, ADR 0017 D49, and
`Integration Tests (envtest)` on a test that read the manager cache right after a Create; both
fixed in the working tree, and CI has not run on the fix)*. On 2026-09-26 on Kind (control
plane + 3 workers, Kubernetes v1.36.1, containerd) `make test-e2e` ran 51/51 green on both pinned Valkey lines, and the fleet-upgrade e2e passed
from released chart 1.12.8; the runs are recorded in the ticket. Both runs predate the two
decisions of the first amendment above (the second roll, the serial small Sentinel tier) ~~: the
fleet-upgrade e2e as changed for the second roll and the new
`TestE2E_RollingUpdate_TwoSentinelsRollSerially` have **not run**~~ *(superseded 2026-09-26 by
the update at the end of this paragraph)*. `make test-unit` is green with both implemented
(2026-09-26). See Residual risks for what each tier does and does not prove.
*(Updated 2026-09-26:)* the changed fleet-upgrade e2e has since run twice and found one ordering
defect each time (the amendment above); ~~the rerun on the fixed code, both full suites with
`TestE2E_RollingUpdate_TwoSentinelsRollSerially`, and ADR 0033's
`TestE2E_PodHardening_UserNamespacesLocalhostSeccompAndDigest` have not run yet~~.
*(Updated again 2026-09-26, Kind: Kubernetes 1.36.1, containerd 2.3.1, runc 1.4.2, Linux
6.10:)* the fleet-upgrade e2e from 1.12.8 is green on the fixed code — exactly two
`RollingUpdateComplete` per persistent tier, nothing rolling after the second roll; the full
suite ran 53/53 on Valkey 8 and 52/53 on Valkey 9, the one failure being the ADR 0033 hardening
e2e's own `/data` owner assertion (Kind's hostPath volume root is root-owned `0777`, and a cluster
this operator built never ran the repair), which now compares the root owner before and after the
move and passed on Valkey 8 and, rerun alone, on Valkey 9;
`TestE2E_RollingUpdate_TwoSentinelsRollSerially` is green on both lines in an earlier run. All of
these ran before ADR 0033 D9's allow-list and ADR 0033's CEL path rule existed; ~~a rerun with
them is not recorded here~~ *(superseded 2026-09-26 by the final run below)*.
*(Final run, 2026-09-26, same Kind stack, on one operator image built from the final code of the
branch — ADR 0033's allow-list with its gate at the workload write, its CEL path rule and
[ADR 0025](0025-a-split-brain-warning-means-one-that-did-not-resolve-itself.md) D9 included:)*
the fleet-upgrade e2e from 1.12.8 green; the full suite 53/53 green on Valkey 9 and 53/53 on
Valkey 8, which includes `TestE2E_PodSecurity_RestrictedNamespace`,
`TestE2E_RollingUpdate_TwoSentinelsRollSerially` and the ADR 0033 hardening e2e with its
allow-list refusal subtest; and two further Valkey 8 runs of the hardening e2e and of
`TestE2E_PodSecurity_RestrictedNamespace`, green. The run before that one, without ADR 0025 D9,
had a red Valkey 8 leg: a pre-existing Sentinel split-brain defect that
demoted the replica Sentinel was promoting, not a defect of this ADR. Locally, not in CI; the
remaining CI-parity gates are not recorded here.

Amends [ADR 0005](0005-upgrade-neutral-defaults-and-anti-affinity.md) D1 (scope), D7 (one
recorded exception) and D11 (this release rolls the Sentinel tier);
[ADR 0007](0007-failover-aware-rolling-update.md) D6 and D7 (the single-pod rule);
[ADR 0012](0012-the-sidecar-records-its-drain-promotion-on-the-pod.md) D8 step 4 (the token
under `fsGroup`); [ADR 0017](0017-test-and-ci-policy.md) (the new guards). Supersedes
[ADR 0013](0013-operator-is-cluster-wide-privileged.md) D9.

## Context

The builder set no `securityContext` on any pod or container — ADR 0013 D9, a stated decision.
Kubernetes therefore ran each container as its image's user, with the runtime's default
capability set, `Unconfined` seccomp and a writable root filesystem.

The upstream Valkey image declares **no `USER`**. It is built to start as root and drop
privileges itself: `docker-entrypoint.sh` chowns the working directory to `valkey` and
re-execs through `setpriv --reuid=valkey`. **The operator never ran that entrypoint**: every
container on the Valkey image sets `command:`, which replaces `ENTRYPOINT`. Measured in Docker
on both pinned lines (T31, 2026-09-25): `valkey-server` ran as uid 0 with fourteen
capabilities — `NET_RAW`, `DAC_OVERRIDE`, `SETUID` among them — and `NoNewPrivs: 0`. The
operator ran Valkey with *less* isolation than the image has when started the way its authors
intended, and a namespace enforcing Pod Security `restricted` refused every generated pod.

The same measurement under `--user 999:999 --read-only --cap-drop ALL
--security-opt no-new-privileges` — the restricted posture, with a tmpfs per `emptyDir` —
passed on both pins: RDB and AOF writes, an AOF rewrite, `valkey-sentinel` rewriting its
config, `sed -i` and `sha1sum` in the Sentinel init script, the exporter. Nothing the operator
runs inside the Valkey image needs root.

What does need care is **data an earlier operator wrote as root**: `root:root 0644` files and a
`0755` `appendonlydir`, because the entrypoint that would have set a restrictive umask and the
right owner was bypassed. Measured against a uid-999 pod on the same volume:

| Volume | Result |
|---|---|
| RDB, volume root `0755 root` | starts, serves reads, answers `PONG` — then `BGSAVE` fails, and because the generated config sets `stop-writes-on-bgsave-error yes`, **every write returns `MISCONF` while the pod stays Ready** |
| AOF, either mode | exits 1 (`Can't open the append-only file`) → CrashLoopBackOff |
| AOF after a simulated kubelet `fsGroup` re-group | works |
| AOF after `find /data ! -user 999 -exec chown 999:999 {} +` as uid 0 with **only `CAP_CHOWN`** | works |

kubelet applies `fsGroup` on some volume types (CSI with an `fsType`, in-tree `local`) and not on
others (`hostPath` — Kind's local-path provisioner — NFS, `fsGroupPolicy: None`). An operator
downgrade onto repaired data is safe (root with `DAC_OVERRIDE` writes anything); uid 0 with
every capability dropped is **not** — the same AOF permission error — which is why no
"root without capabilities" intermediate posture exists below.

Three code facts shaped the migration. The pod-spec hashes cover the whole built `PodSpec`
(ADR 0005 D7), so a posture change reaches every existing pod through the ordinary
failover-aware roll — the Sentinel tier included, which a plain upgrade otherwise never rolls
(ADR 0005 D11). `podSpecChanged` did not compare `securityContext`, and the observer Deployment
carries no hash at all, so without new comparison lines an existing observer would never have
received the posture. And the single-pod rule was decided by `isSidecarOnlyChange`, an image
comparison: on the Helm path a release ships a new sidecar image *with* the new posture, the
change was classified sidecar-only and deferred; on kustomize the only pod was deleted at once —
for a non-persistent cluster, with its data.

## Decision

**D1 — Every generated pod is rootless, with no option.** Data and Sentinel pods run with the
pod-level `runAsNonRoot: true`, `runAsUser: 999`, `runAsGroup: 999`, `fsGroup: 999` and
`seccompProfile: RuntimeDefault` *(since 2026-09-26 the default, with `Localhost` selectable —
ADR 0033 D1, below — and, by ADR 0033 D9 of the same day, only a `Localhost` profile the
operator's allow-list names: the profile is `RuntimeDefault` unless an allowed `Localhost` one is
chosen, and an unlisted one is never written)*; every container and init container — the
operator's sidecar and the third-party exporter included — with `allowPrivilegeEscalation: false`,
`readOnlyRootFilesystem: true` and `capabilities.drop: [ALL]`. The observer runs with
`runAsNonRoot` and `RuntimeDefault` *(since 2026-09-26 the same profile as the data pods, ADR
0033 D1 and D9)* at pod level ~~(its image user, 65532, is numeric)~~
*(superseded 2026-09-26 by ADR 0033 D4: pinned to `runAsUser`, `runAsGroup` and `fsGroup` 65532
— `OperatorUID`, the operator image's numeric `nonroot` user — instead of inheriting whatever its
image declares)* and the same three container fields. The `valkey` container states
`workingDir: /data`, which it used to inherit from the image, because without persistence a
replica's full-sync RDB lands in the working directory. There is ~~no CRD field~~ no CRD field
that lowers this posture *(struck 2026-09-26, the amendment at the end of this paragraph)*, no
`baseline` level and no opt-out: root was a defect, not a setting, and ADR 0005 D1's "new
features default to off" governs features, not the repair of a defect. *(Amended 2026-09-26 by ADR 0033: a CRD
field, `spec.podSecurity`, now exists. It chooses between the two seccomp profiles Pod Security
`restricted` allows and adds an opt-in user namespace; `Unconfined` cannot be expressed, and
nothing in it touches the uid, the capabilities, privilege escalation or the read-only root
filesystem of this decision. A `Localhost` profile is not by construction stricter than
`RuntimeDefault`: its content is node state the operator cannot see — the fixture the ADR 0033
e2e installs allows every syscall it does not name — so how strict the filter is then rests
with whoever installs the profile ~~.~~ *(amended 2026-09-26, ADR 0033 D9)* and with whoever lists
it in `--allowed-seccomp-localhost-profiles`: a CR may name any relative path, but only a listed
one is ever written into a workload, not every file a node holds.)*

The posture is applied by one walk over the assembled `PodSpec` (`applyValkeyPodSecurity`,
`applyObserverPodSecurity`), called last in each builder. **A container added later inherits
the posture because it is in the pod, not because its builder remembered to ask for it.**

*Amended 2026-09-26 by
[ADR 0033](0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md):* the walk
sets more than the list above, on every data, Sentinel and observer pod. The seccomp profile is
`RuntimeDefault` unless `spec.podSecurity.seccompProfile` names a `Localhost` one
(`GetSeccompProfile`, ADR 0033 D1) that the operator's allow-list names (`seccompProfileAllowed`,
ADR 0033 D9; an unlisted one blocks the workload writes, so the pods keep their template);
`hostUsers: false` is set only when
`spec.podSecurity.userNamespaces` is true (`applyPodHardening`, ADR 0033 D2); every container
states `privileged: false` (`restrictedContainerSecurityContext`, and the repair's own context of
D2 below) and every pod `enableServiceLinks: false` (ADR 0033 D4). The walk is unchanged as the mechanism, so a
container added later inherits these too.

**D2 — Root-written data is re-owned once, by a repair the hash never sees.**

- `fsGroup: 999` with `fsGroupChangePolicy` unset (= `Always`): `OnRootMismatch` inspects only
  the volume root and would skip files a later root writer left beneath a correct one.
- Every persistent data pod runs the pre-flight `check-data-writable` first (uid 999, shell
  builtins only). It fails the pod when `/data`, `/data/appendonlydir`, or a regular file in
  either is not writable, and names the fix; `terminationMessagePolicy: FallbackToLogsOnError`
  puts that message into `kubectl describe pod`. It turns the silent `MISCONF` of the RDB row
  above into a loud refusal.
- While the migration evidence of D4 holds, the data template carries `fix-data-ownership` in
  front of the pre-flight: uid 0, `drop: [ALL]`, `add: [CHOWN]`, read-only root,
  `no_new_privs`, `find /data ! -user 999 -exec chown -h 999:999 {} + ; exit 0`. **The repair is
  best-effort and the pre-flight is the one gate**: a pod created while the template carried the
  repair keeps it in its immutable spec until the second roll below replaces it, and re-runs it
  on every sandbox restart until then; a second run cannot enter a directory the first one
  handed to 999 with mode `0700` — `lost+found` on an ext4 root — without the DAC override it
  deliberately lacks. A failing repair would then block a migrated pod after every node reboot.
  `-h` re-owns a symlink itself, never its target.
- **The repair is inserted after `ComputePodSpecHash`** (`WithDataOwnershipRepair`, on the
  built object), so the template writes that add it and remove it do not move the hash and are
  not rolls themselves. ~~so adding it and removing it rolls nothing.~~ *(Corrected 2026-09-26:
  removing it rolls the pods that carry it — the second roll below — through a comparison of its
  own, not through the hash.)* This is a narrow, recorded exception to ADR 0005 D7: the
  container acts only at pod start, and after the migration it is a no-op.
- No repair for Sentinel, observer or non-persistent pods: their volumes are fresh `emptyDir`s.

No pod created after the migration runs a root process. **The pods created *during* it are
replaced by a second roll** *(decided 2026-09-26)*. Until then they carry the repair in their
spec, re-run it on a sandbox restart as a no-op, and a Pod Security `restricted` dry-run lists
them (D6). Once the repair has left the template — which D4 allows only when every ordinal
holds a migrated pod *(and, since 2026-09-26, no data-tier roll is recorded; `finishDataRoll`
asks for the pass that removes it)* — a data pod that still carries it is outdated for that alone:
`podOutdated` is `podNeedsUpdate` against the persisted StatefulSet or `podCarriesRetiredRepair`
(the pod spec carries `fix-data-ownership`, the persisted template does not), and it is what
every data-tier site asks — the dispatch loop, `collectPodStates`, the standalone handler and
the manual-failover master check. The ordinary failover-aware roll then replaces those pods
from the clean template. The comparison is one-directional: while the template carries the
repair no pod is outdated for lacking it, so the write that adds it stays a non-roll. A pod
missing during the second roll is no evidence for the repair — D4 keeps only a repair the
template already carries — so it does not come back. After the second roll the only generated
pods with a root container are the deferred non-persistent single pods of D3 and a pod too old
to carry a `pod-spec-hash` (*Residual risks*). In a persistent tier such an old pod is itself
migration evidence (D4): it keeps the repair in the template, so that tier's second roll waits
for the same restart that migrates it.

~~The pods created *during* it keep the repair in their spec until they are next replaced for
any other reason; on a sandbox restart it runs again as a no-op, and a Pod Security `restricted`
dry-run lists them (D6). Clearing them would take a second roll, which this decision does not
make (Consequences).~~ *(Superseded 2026-09-26 by the paragraph above; the option that lost is
under Alternatives.)*

**D3 — Single-pod clusters are decided by persistence.** On a `spec.replicas: 1` data cluster
whose pod runs without `runAsNonRoot` (`singlePodDeferral`):

- persistent: replaced at once, with the repair running on its way up, and once more when the
  repair has left the template (D2) — ~~one restart~~ two restarts *(corrected 2026-09-26)*,
  data kept;
- not persistent, Valkey image unchanged: **deferred** until the pod restarts for any other
  reason, reported by `PodSecurityUpdatePending=True/PodRunsAsRoot` naming the pod. The
  operator upgrade alone never discards a dataset;
- not persistent, Valkey image changed: replaced — the CR author asked for a new image, the
  same data-loss change that was always applied;
- not persistent, TLS material rotated or configuration changed: replaced as well. Both are
  records outside the pod-spec hash, so the operator upgrade alone never moves them; a rotation
  roll of a non-persistent single pod is the loss [ADR 0030](0030-rotating-certificates-rotate-the-instances-that-cannot-reload-them.md)
  already accepted — deferring it would keep an expiring certificate in the pod — and a
  configuration change is the CR author's. A change the pod-spec hash carries cannot be told
  apart from the posture and is held with it; the condition message says so. (Refined on
  2026-09-26 in the adversarial review, beyond the three cases decided in T31.)

**Persistence is read off the persisted StatefulSet** (`volumeClaimTemplates`), never off the CR:
a persistence toggle the operator refused to write (ADR 0023) would otherwise read as
"persistent" and delete the only pod together with its `emptyDir`.

`isSidecarOnlyChange` no longer decides a root pod; it still decides a rootless one (ADR 0007
D6). `PodSecurityUpdatePending` is a level with one evaluator in `checkAndHandleRollingUpdate`
(the deferral is decided one dispatch target down, which most passes never reach), written
`False/PodSecurityUpdateApplied` only over a standing True, Event-free (ADR 0025 D7), with a
row in `conditionRegistry` (ADR 0027).

**D4 — The migration evidence is the pod spec, derived per pass and stored nowhere.** Pod specs
are immutable, so "a pod proven ours (`podIsOurs`) whose `spec.securityContext.runAsNonRoot` is
not `true`" cannot be forged by a label or an annotation and survives an operator restart
(`PodRunsRootless`). The repair is **added** when such a pod exists in the ordinal range of the
live StatefulSet, or when the live template itself still lacks the posture — at the first pass
after the upgrade a pod may be missing, and the rootless template would otherwise be written
without the repair. It is **kept** until every ordinal holds a *migrated* pod: proven ours,
rootless and Ready — ~~and past the repair — its pre-flight exited 0, or it has been Ready~~
*(tightened 2026-09-26, below)* — and it is kept while a data-tier roll is recorded. A missing or
foreign pod is not proof, and neither is a rootless replacement stuck before its init containers
(an image it cannot pull). The asymmetry closes two races: a pass between the last legacy pod
disappearing and its recreation, and a replacement that exists but has not run its repair.
Persistence, again, comes from the persisted StatefulSet. *(Added 2026-09-26.)* The removal is
what starts the second roll of D2, so the same hysteresis orders the two rolls: the second
cannot start before the first has replaced every pod ~~— though it can start before the first has
finalized, and on the multi-replica non-Sentinel path it always does (*Residual risks*)~~.
*(Amended 2026-09-26, measured.)* Nor before the first has finalized: `reconcileStatefulSet` runs
before the rolling update in the same pass, so a removal under a recorded roll outdates every pod
before that roll's finalization — topology check, state clear, `RollingUpdateComplete` — and
`clearStaleRollingUpdateState` then discards its state as stale, on the non-Sentinel path in the
middle of the topology restoration. The repair therefore stays while
`annotationRollingUpdateState` is set, and a migrated pod is one that is Ready, not one past its
pre-flight: a single pod records no roll state, and Ready is what keeps its second restart behind
the first having served. The state is a gate on *keeping* the repair, never evidence for adding
it (`TestDataOwnershipRepairNeeded_StaysWhileARollIsRecorded`). *(Added 2026-09-26, measured.)*
The gate moves the removal into the pass **after** the one that completes the first roll, and
nothing schedules that pass: the CR watch is generation-gated (`GenerationChangedPredicate`),
the state clear is an annotation write that moves no generation, and there is no Pod watch, so
the next guaranteed pass is the owned-object cache resync (the ADR 0002 D10 finding). The
second fleet-upgrade run showed the result — the repair stranded in the template, the second
roll never started. `finishDataRoll`, the one completion site of every data-tier dispatch
target, therefore calls `requestRecheck(ctx, rollingUpdateRequeueDelay)` (10 s) when the
persisted template it completed against still carries the repair. The recheck changes nothing
the completing pass does — no requeue result, so the Sentinel roll and the status write still
run in it — and asks for nothing when the template carries no repair
(`TestCompletedRoll_AsksForThePassThatRemovesTheRepair`).

**D5 — The drift comparisons include the posture, with subset semantics and one exception.**
`podSpecChanged`/`containerChanged` compare the pod- and container-level `securityContext`
fields the operator sets; a field it does not set is not compared (`nil` ≡ `{}`), so a
mutating admission policy adding one is not a drift the operator rewrites the StatefulSet
over. The exception is `capabilities.add`: the live template may not add a capability the
desired one does not, because a subset comparison there would never converge an out-of-band
grant of `NET_RAW` back. `ObserverDeploymentHasChanged` gains the same two lines — without them
an existing observer would never have received the posture. *(Amended 2026-09-26 by ADR 0033
D2:)* a second exception: `hostUsers` is compared exactly (`podHardeningChanged`, reached from
`podSpecChanged` for both StatefulSets and from `ObserverDeploymentHasChanged`), because opting
out leaves the desired field unset and a subset comparison would never converge the persisted
`false` back. `enableServiceLinks`, `privileged` and a `Localhost` profile path are compared as
subsets like the rest.

**D6 — What an administrator enforces afterwards.** Once a namespace holds no pod without
`runAsNonRoot` and no pod carrying the repair, `pod-security.kubernetes.io/enforce: restricted`
can be set on it;
`kubectl label --dry-run=server --overwrite ns <ns> pod-security.kubernetes.io/enforce=restricted`
lists the violators first. While the migration runs it also lists the persistent data pods that
carry the repair (D2), until the second roll has replaced them from the clean template. After
it the generated pods the dry-run can still name are the non-persistent single pods deferred
under D3 and a pod too old to carry a `pod-spec-hash` (*Residual risks*); both run as root until
they restart, and in a persistent tier the old pod also holds the repair in the template, so
that tier's pods keep carrying it until then (D2). ~~It also lists the persistent data pods created
during the migration, which still carry the repair (D2); enforcement does not evict running
pods, and their next replacement comes from the clean template.~~ *(Superseded 2026-09-26 by
the second roll of D2.)* The operator does not label namespaces.

**D7 — Released after ADR 0026 D11 (T32).** A replacement of a multi-replica or Sentinel tier
that never becomes available — NFS with `root_squash` refusing the repair's `chown` is the known
case — is reported as `PodAvailabilityStalled` instead of stalling silently, and after a fix the
operator replaces the stuck pod itself. A **single pod** is not covered: its roll records no
state, so a current pod that does not start takes the converged early return and shows only as
phase `Provisioning` (ADR 0026, residual risk "A single pod that never starts is not
reported").

## Consequences

- **Every multi-replica data tier rolls once — a persistent one twice (D2) — and every Sentinel
  tier rolls once**, which a plain operator upgrade otherwise never does (ADR 0005 D11).
  Failover-aware and lossless like every roll; on a persistent tier every pod is replaced twice
  and the master is failed over twice, ~~though the two replacements need not complete as two
  rolls (*Residual risks*, the handover)~~ *(corrected 2026-09-26: since the ordering fix of D4
  the two replacements complete as two rolls, each with its own `RollingUpdateComplete` — read
  from `dataOwnershipRepairNeeded` and `finishDataRoll`; ~~the fleet-upgrade rerun that asserts it
  has not run yet~~ and measured by the fleet-upgrade e2e from 1.12.8 on 2026-09-26: exactly two
  per persistent tier, nothing rolling after the second)*. Tiers of one or two Sentinels are included since
  2026-09-26: they roll serially, one Sentinel at a time and only while every other one is
  available, and lose automatic failover for the seconds a Sentinel restarts
  ([ADR 0024](0024-the-sentinel-tier-reports-its-own-completion.md) D10); before that decision
  such a tier never finished this roll.
- **Persistent single-pod clusters restart twice at the upgrade** — for the posture, then for
  the retired repair (D2); downtime, not data loss. ~~restart once~~ *(corrected 2026-09-26)*.
  ~~The second restart does not wait for the first to have served: D4 counts the pod as migrated
  once its pre-flight exited 0, and the standalone handler replaces an outdated single pod
  without asking readiness (ADR 0026 D11), so the two can run into one longer outage.~~
  *(Superseded 2026-09-26: D4 counts the pod as migrated only once Ready, so it serves between
  the two restarts.)*
- **Non-persistent single-pod clusters keep running as root** until their next restart for any
  other reason, and say so in `PodSecurityUpdatePending`. That is the price of never discarding
  a dataset for an operator upgrade.
- Root still runs once per persistent data pod during the migration, for a fraction of a second
  — also on storage where `fsGroup` alone would have sufficed. The repair cannot tell the two
  apart without a failed start first (Alternatives, M2).
- **NFS with `root_squash`** is the case no pod can repair: root is squashed, `chown` fails, the
  pre-flight holds the first replica, and `PodAvailabilityStalled` names it after
  `syncTimeout`. It needs a server-side `chown -R 999:999` before the upgrade.
- A StatefulSet re-created by hand over PVCs that were never migrated gets no repair (no legacy
  pod exists); the pre-flight stops it loudly and a manual `chown` fixes it. The same holds for
  a **scale-up onto claims retained from a scale-down** under an earlier operator — the operator
  sets no `persistentVolumeClaimRetentionPolicy`, so such claims keep root-written files — and
  for **fresh volumes whose root is owned by root on storage without `fsGroup` support** (a
  static `hostPath`, a CSI driver with `fsGroupPolicy: None`). Those worked while the pods ran as
  root. The storage requirement is now: kubelet applies `fsGroup`, or the volume root is writable
  by uid 999 (Kind's local-path and nfs-subdir create `0777` directories).
- ~~**The persistent data pods created during the migration keep the repair in their spec** until
  they are next replaced (D2, D6). No second roll clears them; that was a deliberate trade in
  the decision (no second roll), and whether to add one is an open question to Hans.~~
  *(Superseded 2026-09-26: Hans decided for the second roll of D2, which replaces them.)*
- The root filesystem is read-only: debugging goes through `kubectl debug`, not through writing
  into a container.
- The sidecar and the exporter now run as 999 rather than as their images' users (65532 and
  59000). Measured for the exporter in Docker; the sidecar is the operator's own binary.
- The migration costs one extra write of each persistent data StatefulSet: the repair rides in
  on the write that brings the posture (D4 adds it while the live template lacks the posture),
  and leaves in a write of its own — the one that makes every pod of that tier outdated once
  more. ~~two extra writes of each persistent data StatefulSet: the repair in, the repair
  out~~ *(corrected 2026-09-26 against `dataOwnershipRepairNeeded`)*.
- An operator downgrade onto repaired data is safe: the old shape runs as root with
  `DAC_OVERRIDE`.

## Alternatives Considered

- **A per-cluster level field** (`spec.podSecurity.level`, with a `baseline` default for
  existing clusters, an inherit-or-restricted rule, or a CRD default pinned by the migration
  hook) — withdrawn in the decision round: root is a defect, not an option, and an opt-in field
  leaves the fleet on the defect. The hook variant also lost on mechanism: the hook runs before
  the new CRD and its pin is pruned.
- **M2, repair only after the pre-flight proves it is needed** — root only where physically
  needed and the hand-recreated-StatefulSet case covered, but one failed start per affected
  cluster, and a new "delete an unavailable pod" exception in ADR 0026, the rule that had been
  incomplete three times.
- **M3, no root container at all** (`fsGroup` and pre-flight only) — clusters with legacy data
  on storage without `fsGroup` stop at their first replica, single-pod ones are down, until an
  administrator runs a manual `chown`.
- **S2, all single pods deferred** — no downtime, but every single-pod cluster stays root until
  it restarts, possibly for weeks. **S3, all replaced** — non-persistent single pods lose their
  data to an operator upgrade.
- **Root without capabilities as an intermediate posture** — measured failing: uid 0 with every
  capability dropped has no `DAC_OVERRIDE`, cannot open the `999`-owned AOF files the repair
  leaves behind (`Can't open the append-only file … Permission denied`), and would therefore
  break an operator downgrade that the plain root shape survives.
- **Accept the repair in pod specs until their next replacement** — the recommendation until
  2026-09-26, when Hans decided against it. It saves the second roll of D2, and in exchange
  every migrated persistent pod keeps a uid-0 container holding `CAP_CHOWN` in its immutable
  spec, re-runs it on every sandbox restart and is named by every `restricted` dry-run (D6) —
  until something nobody schedules replaces it. "No generated pod runs root" would have held
  only for pods created after the migration.
- **The repair inside the pod-spec hash** — ~~every cluster would roll twice: once to add the
  repair, once to remove it.~~ *(Corrected 2026-09-26: with the second roll of D2 a persistent
  tier rolls twice either way, and the repair enters the template in the same write as the
  posture, so on the migration path both designs replace the same pods.)* What still separates
  them: `ComputePodSpecHash` is a function of the CR and the operator image, while the repair is
  decided per pass from the live pods (D4), which would make pod state an input of the hash; and
  the hash is symmetric where `podCarriesRetiredRepair` rolls only a pod carrying a repair its
  template has dropped, never one for lacking a repair the template carries.
- **"Any legacy pod exists" without the hysteresis of D4** — the race on the last pod of a tier.
- **`fsGroupChangePolicy: OnRootMismatch`** — cheaper on large volumes, and skips exactly the
  files a later root writer leaves beneath a correctly owned root.

## Residual risks

- **Measured on a node** (local Kind, control plane + 3 workers, Kubernetes v1.36.1, containerd,
  2026-09-26), both pinned Valkey lines: `TestE2E_PodSecurity_RestrictedNamespace` green on both
  legs — the namespace refuses an unrestricted pod (positive control), every generated pod is
  admitted under `enforce=restricted` and Ready, `Uid: 999`, `CapEff: 0`, `CapBnd: 0` and
  `NoNewPrivs: 1` read from `/proc/1/status` of the `valkey` and `sentinel` containers, an AOF
  rewrite and an RDB snapshot complete under containerd's `RuntimeDefault`, TLS+auth data
  replicates, a Sentinel image roll and a drain failover finish with zero Warning Events, the
  sidecar labels roles. The projected token under `fsGroup` is `0640`, owner and group 999 (read
  off the node). Kind's PV is `hostPath` (`DirectoryOrCreate`, root `0777 root`), and the
  fleet-upgrade e2e asserts that at runtime and fails loudly if Kind changes.
  `TestE2E_FleetUpgrade` (`make test-e2e-fleet-upgrade E2E_UPGRADE_FROM=1.12.8`, 253 s) passed
  from released chart 1.12.8 over a fleet of six on valkey 9.1.1: every multi-replica and
  Sentinel cluster converged rootless with its keys on every replica, every persistent pod ran
  `fix-data-ownership` (exit 0) and the repair then left the template, the migrated persistent
  masters wrote and snapshotted without `MISCONF` (RDB volume roots set to `0755 root`
  beforehand), the observer received the posture, each Sentinel tier rolled exactly once, no pod
  was replaced in the 90 s after the repair left, the persistent single pod restarted once with
  its keys, and the non-persistent one was not restarted, kept its keys and reports
  `PodSecurityUpdatePending=True/PodRunsAsRoot`. What that run does not prove: the default
  starting point 1.10.48 (its released images are amd64-only, the host was arm64; 1.12.8 ran
  under emulation), a volume kubelet re-groups under `fsGroup` (Kind's is `hostPath`), and
  anything in CI — it is still not a CI job. **Not covered at
  all**: CRI-O's smaller default capability set and OpenShift's SCC — **under OpenShift's `restricted-v2` SCC a fixed
  `runAsUser: 999` outside the namespace's UID range is refused.** Nothing in this repository
  targets OpenShift today.
- **The two decisions of 2026-09-26 are unit-tested and have ~~not run~~ ~~not passed~~ passed
  on a node** *(2026-09-26, locally on Kind, not in CI)*.
  *(Corrected 2026-09-26: the changed fleet-upgrade e2e has run twice since, and each run found
  one ordering defect of the second roll, both fixed in D4 — first one
  `RollingUpdateComplete` per persistent tier instead of two, then a repair stranded in the
  template after the first roll. ~~The rerun on the fixed code has not run yet, and neither has
  `TestE2E_RollingUpdate_TwoSentinelsRollSerially` in a full suite.~~)* *(Updated 2026-09-26:
  the rerun on the fixed code is green from 1.12.8 — exactly two `RollingUpdateComplete` per
  persistent tier, nothing rolling after the second roll — and
  `TestE2E_RollingUpdate_TwoSentinelsRollSerially` is green on both Valkey lines in an earlier
  run; the node and the full-suite results are in Status. Both predate ADR 0033 D9.)* *(Updated
  again 2026-09-26: both are green on the final image of the branch too, D9 included — the
  fleet-upgrade e2e from 1.12.8, and the two-Sentinel e2e inside both full suites; Status.)* The run above
  is of the one-roll version: "no pod was replaced in the 90 s after the repair left" was the
  superseded rule's assertion and is now what a missing second roll would look like. The
  fleet-upgrade e2e as changed waits for the second roll (template and every pod free of the
  repair, every pod Ready — the persistent single pod included, whose repair-free pod can only
  come from its second restart); asserts that `/data`, `/data/appendonlydir` and every non-hidden
  entry directly in them is owned by 999 on each persistent pod — on Kind's `hostPath`, which gets
  no `fsGroup`, only the repair can have done that, and it replaces the per-pod proof that the
  repair exited 0, which the second roll takes off the pods; counts `RollingUpdateComplete`
  Events since the upgrade: exactly two per persistent multi-replica data tier (~~at least one —
  not two, because the second replacement need not complete a roll of its own (next item)~~,
  restored 2026-09-26 with the D4 ordering fix) and exactly one per non-persistent multi-replica
  tier; and moves the 90 s no-replacement check to after
  the second roll. `TestE2E_RollingUpdate_TwoSentinelsRollSerially` rolls a two-Sentinel tier
  through an image change. Unit: `TestReconcileStatefulSet_RepairComesAndGoesAndTheRetiredRepairRolls`,
  `TestPodCarriesRetiredRepair`, `TestHandleStandaloneRollingUpdate_ReplacesAPodCarryingTheRetiredRepair`,
  `TestSentinelRollingUpdate_SmallTiersRollSerially`, `TestSentinelDeleteKeepsVotes`.
- ~~**The handover from the first roll to the second was read from the code, not run.**~~
  *(Closed 2026-09-26: measured, then fixed in D4. The fleet-upgrade run counted one
  `RollingUpdateComplete` per persistent tier where the subtest expected two; the repair now
  stays while a data-tier roll is recorded, and a migrated pod is Ready. That fix had a second
  half, also measured: the next fleet-upgrade run found the repair stranded in the template after
  the first roll, because the pass that may remove it is the one after the completion and nothing
  scheduled it; `finishDataRoll` now requests it (`requestRecheck`, D4). ~~The fleet-upgrade e2e on
  both halves has not run yet.~~ The fleet-upgrade e2e on both halves is green (2026-09-26, two
  `RollingUpdateComplete` per persistent tier), and again on the final image of the branch. The
  original finding follows unchanged.)* D4 lets
  the repair leave once every ordinal holds a pod past its pre-flight, and
  `reconcileStatefulSet` runs before the rolling update in the same pass. In the pass in which
  the last replacement of the first roll qualifies — its pre-flight exited 0, or it turned
  Ready — the rolling update therefore finds every pod outdated before the first roll has
  finalized; `clearStaleRollingUpdateState` discards the first roll's state as stale (no pod
  counts as replaced), and the second roll starts at the replica step. Its youngest-first
  candidate is the pod the first roll recreated last, a replica, which `replaceNextReplica`
  deletes without asking readiness (ADR 0026 D11) — nothing is lost, the claim stays. What it
  costs: the rest of the first roll — its finalization, and on the non-Sentinel path any
  post-failover state still pending — is skipped, so the tier emits one
  `RollingUpdateComplete` for both replacements; ~~the changed fleet-upgrade e2e asserts at least
  one for a persistent tier for that reason~~ (it asserts two since the fix). It is not a rare race. The finalization waits for
  every pod to be Ready (`countUpdatedPods`), while the pre-flight exits before the pod can be,
  so any pass in between removes the repair first. On the Sentinel path the first roll
  finalizes first only when no pass lands in that window and the pass that removes the repair
  still reads the pre-removal template from the cache and completes the finalization in one
  go. On the non-Sentinel path it never does: `handlePostManualFailover` proceeds only once the
  replaced master is Ready, the topology restoration takes further passes, and
  `handleMultiReplicaRollingUpdate` runs the same stale-state clear before dispatching to them.
  How often the Sentinel path takes which order was not measured, and the non-Sentinel path was
  not traced past the state clear. *(Corrected 2026-09-26: this item called the order timing on
  both paths and said the e2e counted two Events.)*
- A **Sentinel** cluster with `spec.replicas: 1` is not covered by D3: it goes through the
  Sentinel rolling update (`handleRollingUpdate`), not `singlePodDeferral`, and its only data pod
  is replaced like on every other spec change of that shape — without persistence, with its
  data. That predates this ADR; this release is one more trigger for it. With persistence its
  pod is outdated a second time by the retired repair, like every persistent data pod (D2).
- A pod created by an operator so old that it carries no `pod-spec-hash` annotation is not
  recognised as outdated by the posture change (`podSpecHashChanged` falls back to comparing
  resources), so it is not migrated by the roll; a restart for any other reason migrates it.
  In a persistent tier it is also migration evidence (D4), so the repair stays in the template
  and the tier's second roll (D2) waits for that restart. *(Added 2026-09-26.)*
- The subset comparison of D5 lets an admission policy add a field the operator does not set —
  `fsGroupChangePolicy: OnRootMismatch` included, which would weaken D2. Checked read-only on
  wds18 on 2026-09-26: its Kyverno mutate rules act on Pods at CREATE and are opt-in by label
  (`minio-backup/*`, `cp/inject-truststore`); none rewrites a StatefulSet template. Other
  clusters were not checked.
- A pod deleted for any reason while its StatefulSet carries the repair — a chaos kill during
  the migration — runs the repair on its way up. That is the intended behaviour, and it is root
  for the length of one `find`.
- The pre-flight checks the two directories Valkey writes and the regular files directly in
  them. A nested directory, or a file type other than a regular file, is not checked.
- The T31 hypothesis that a restarted replica holding a readable `dump.rdb` can resume by partial
  resync without writing a file — and so pass the rolling update's sync check and be promoted
  onto the `MISCONF` of the RDB row — was never measured. The pre-flight exists so that it does
  not have to be.
- The sidecar's token projection keeps `DefaultMode 0644`; under `fsGroup` kubelet rewrote it —
  measured on the Kind node: the token is `0640`, owner 999, group 999, and `ca.crt` is
  `0644 root:999`. The sidecar runs as uid 999 like every other container of the pod and reads
  its own file, which the e2e role labelling confirms. A kubelet that leaves the mode at `0644`
  was not measured.
- **The repair under ADR 0033's opt-ins was not measured.** *(Added 2026-09-26.)* A CR that sets
  `spec.podSecurity.userNamespaces: true` while its tier still migrates — GitOps applying the CR
  change together with the operator upgrade is enough — runs `fix-data-ownership` in a user
  namespace, where the volume reaches the container through an idmapped mount. Whether its
  `chown` can re-own files an earlier operator wrote as host uid 0 there was neither read nor
  measured: `TestE2E_PodHardening_UserNamespacesLocalhostSeccompAndDigest` moves a cluster whose
  data was written by uid 999, not root-written data, ~~and it has not run yet~~ *(it has since run
  green, 2026-09-26, before ADR 0033 D9, and proves only that: what `valkey-server` wrote reads as
  999 through the idmapped mount and the volume root keeps the owner it had before the move —
  root on Kind's hostPath, because a cluster this operator built never ran the repair; green again
  on the final image of the branch, D9 included, on both Valkey lines and in two further Valkey 8
  runs, still without root-written data)*. The same
  holds for a `Localhost` profile that does not allow `chown` (ADR 0033 D1; since D9 only one an
  administrator listed). Either way the pre-flight of D2
  stays the one gate: a repair that achieves nothing leaves the pod failing at
  `check-data-writable` rather than serving a silent `MISCONF`; on a multi-replica tier that is
  reported as `PodAvailabilityStalled` after `syncTimeout`, while a single pod shows only as phase
  `Provisioning` (D7).

## References

- [`internal/builder/pod_security.go`](../../internal/builder/pod_security.go) — the posture, the
  walk, the pre-flight, the repair, the subset comparison
- [`internal/controller/pod_security_migration.go`](../../internal/controller/pod_security_migration.go)
  — the migration evidence, the single-pod rule, the condition's evaluator
- [`internal/controller/rolling_update.go`](../../internal/controller/rolling_update.go) —
  `podOutdated` and `podCarriesRetiredRepair` (the second roll), `finishDataRoll` (the recheck
  that lets the repair leave, D4), `sentinelDeleteKeepsVotes` (ADR 0024 D10)
- [`internal/builder/image_requirements.go`](../../internal/builder/image_requirements.go) —
  `find` and `chown`
- Tests: [`internal/builder/pod_security_test.go`](../../internal/builder/pod_security_test.go)
  (Pod Security evaluator matrix, both sides), [`internal/controller/pod_security_migration_test.go`](../../internal/controller/pod_security_migration_test.go),
  [`test/integration/pod_security_test.go`](../../test/integration/pod_security_test.go),
  [`test/imagetools/restricted_runtime_test.go`](../../test/imagetools/restricted_runtime_test.go),
  [`test/e2e/pod_security_test.go`](../../test/e2e/pod_security_test.go),
  [`test/e2e/fleet_upgrade_test.go`](../../test/e2e/fleet_upgrade_test.go); for ADR 0024 D10
  [`internal/controller/pod_availability_test.go`](../../internal/controller/pod_availability_test.go)
  and [`test/e2e/pod_availability_test.go`](../../test/e2e/pod_availability_test.go)
- [ADR 0005](0005-upgrade-neutral-defaults-and-anti-affinity.md), [ADR 0007](0007-failover-aware-rolling-update.md),
  [ADR 0012](0012-the-sidecar-records-its-drain-promotion-on-the-pod.md),
  [ADR 0013](0013-operator-is-cluster-wide-privileged.md), [ADR 0017](0017-test-and-ci-policy.md),
  [ADR 0024](0024-the-sentinel-tier-reports-its-own-completion.md) D10,
  [ADR 0026](0026-a-pod-being-deleted-is-not-available.md) D11, [ADR 0027](0027-conditions-are-levels-edges-or-history.md),
  [ADR 0033](0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md) (the
  seccomp choice, the opt-in user namespace, the observer identity and the fields every pod now
  states)
