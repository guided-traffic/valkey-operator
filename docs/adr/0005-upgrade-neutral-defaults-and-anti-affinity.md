# ADR 0005: Upgrade-Neutral Defaults, and Pod Anti-Affinity Off by Default

## Status

Accepted. Date: 2026-08-21. Supersedes the earlier "no off switch" stance, which
carried `soft` as the default for one day on this branch and nowhere else: commit
`0c8b424` (2026-08-19) introduced it, `146ffa5` (2026-08-20) added `off` and made it
the default. Nothing was shipped in that window — no tag contains either commit.

Implemented on branch `feat/support-pdb`, not yet released. Covered at three levels:
unit ([`internal/builder/affinity_test.go`](../../internal/builder/affinity_test.go),
[`api/v1/valkey_types_test.go`](../../api/v1/valkey_types_test.go)), envtest
(`TestAntiAffinity_Integration`,
[`test/integration/affinity_test.go`](../../test/integration/affinity_test.go)) and
e2e — four tests in [`test/e2e/affinity_test.go`](../../test/e2e/affinity_test.go):
`TestE2E_AntiAffinity_OffByDefault`, `TestE2E_AntiAffinity_SoftWhenRequested`,
`TestE2E_AntiAffinity_HardSpreadsAcrossNodes` and
`TestE2E_AntiAffinity_HardLeavesSurplusPending`. Only the third depends on the node
count (`requireThreeSchedulableNodes`, three schedulable nodes); the other three run
on any cluster shape, and all four run in both CI legs, since the multi-node filter is
`TestE2E_AntiAffinity|TestE2E_PodDisruptionBudget`. Whether a CI run of either leg went
green is not verifiable from this repository — no run record is committed.

Amended 2026-09-26 by [ADR 0032](0032-generated-pods-run-rootless.md) (ticket T31, rootless
pods): three decisions change scope, none is reversed. **D1** governs features, not the repair
of a defect — root was a defect, and existing clusters move with the upgrade. **D7** gains one
recorded exception: the migration-only `fix-data-ownership` init container is inserted after
`ComputePodSpecHash`, ~~so it comes and goes without a roll~~ *(superseded the same day, see
below)* so the template writes that add and remove it are not rolls themselves; the refusal to
compare `Affinity` field by field stands, and is no longer generalised to every field.
**D11**: the release that makes pods rootless rolls the Sentinel tier once, because the posture
is in the Sentinel pod-spec hash. The superseded sentences are struck through in place. D10
holds unchanged: the new condition `PodSecurityUpdatePending` is written `False` only over a
standing `True` (`reportPodSecurityUpdatePending`). **Verified locally, not in CI:** the
fleet-upgrade e2e (`TestE2E_FleetUpgrade`,
`make test-e2e-fleet-upgrade E2E_UPGRADE_FROM=1.12.8`) passed on 2026-09-26 on Kind (control
plane + 3 workers, Kubernetes v1.36.1) on a real `helm upgrade` from the released chart 1.12.8
to the local chart: each Sentinel tier completed exactly one roll, every persistent pod ran
`fix-data-ownership` and the repair then left the template, and no pod was replaced in the 90 s
after it did *(the behaviour re-decided below)*. It is not a CI job, and ~~the branch has not
been through the pipeline~~ *(corrected 2026-09-26: the branch was pushed as `e2ce8bb`, where two
gate jobs failed — [ADR 0017](0017-test-and-ci-policy.md) D49; CI has not run on the fixed working
tree)*. **Not verified:** the run does not attribute the Sentinel roll to
the posture (see D11); what backs that part is the code read.

Re-decided 2026-09-26, after `bb6c78f` and before any release (ADR 0032 D2, decided by Hans):
**a second roll replaces the pods that still carry the repair once it has left the template**
(`podCarriesRetiredRepair`, D7). The alternative on record — leave the repair in those pod specs
until their next replacement — was the recommendation and lost. The D7 and D11 sentences it
contradicts are struck through and restated in place; the repair stays outside the hash. The
local run above exercised the code without the second roll, so its last clause is the
superseded behaviour. The changed `TestE2E_FleetUpgrade` asserts the opposite — it waits for
the second roll, asserts that `/data`, `/data/appendonlydir` and the entries in both are owned
by 999, and counts two `RollingUpdateComplete` Events for a persistent multi-replica tier and
one for a non-persistent one (single pods are not counted) — and ~~**has not run**~~ *(updated
2026-09-26)* has run twice, and each run found a defect in the order of the two data-tier
rolls: first the second roll overtook the first (one completion per persistent tier instead of
two), then, with the repair held while a roll is recorded, the repair
stayed in the template because no pass followed the completion. Both are fixed (ADR 0032 D4,
the D7 re-decision below); ~~the rerun on the fixed code has not run yet~~ *(rerun 2026-09-26,
locally on Kind and not in CI, from 1.12.8: green on the fixed code, and green again on one
operator image built from the final code of the branch — ADR 0033's allow-list (D9) and CEL path
rule and [ADR 0025](0025-a-split-brain-warning-means-one-that-did-not-resolve-itself.md) D9
included — on Kubernetes 1.36.1, containerd 2.3.1, runc 1.4.2, Linux 6.10. The test asserts
exactly two `RollingUpdateComplete` per persistent multi-replica tier, one per non-persistent
one, one `SentinelUpdateComplete` per Sentinel tier and no pod replaced in the 90 s after the
second roll, so a green run means each of those held)*. The same day
[ADR 0024](0024-the-sentinel-tier-reports-its-own-completion.md) D10 let a tier of one or two
Sentinels roll serially, which the D11 amendment's "rolls the Sentinel tier once" relies on.

Amended 2026-09-26 by
[ADR 0033](0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md), which ships
in the same unreleased release as ADR 0032. **D1 is applied, not bent:** the two new choices are
features and default off. `spec.podSecurity` has no CRD default at object level, so no existing
CR gains it, and neither does `spec.sentinel.resources` (ADR 0033 D7: omitted means no requests
and no limits, as before); an omitted or explicit `RuntimeDefault` is what `GetSeccompProfile` returns and what
ADR 0032 already renders, so neither pod-spec hash moves; a `Localhost` profile and
`userNamespaces: true` (`hostUsers: false`, `UsesUserNamespaces`) take a CR edit, and each rolls
the tiers through the hash (D7). What ADR 0033 renders unconditionally — `privileged: false` on
every container, `enableServiceLinks: false` on every pod, the observer's uid, gid and `fsGroup`
65532, and the exporter default pinned by digest (`DefaultMetricsExporterImage`, reaching every
metrics-enabled CR without `spec.metrics.image`, ADR 0033 D5) — sits behind no CRD switch and
moves the data and Sentinel pod-spec hashes and the observer comparison; it adds no roll of its own because it rides the one ADR 0032 already causes (ADR 0033
Consequences). D4 gains a correction (the schema now carries ~~one CEL rule~~ two CEL rules
*(corrected 2026-09-26: the second, ADR 0033 D1 as amended the same day, refuses an absolute
`localhostProfile` and a `..` element)*) and D7 a comparison
(`hostUsers` exactly); both are marked in place. ~~**Not verified on a node:**
`TestE2E_PodHardening_UserNamespacesLocalhostSeccompAndDigest` has not run yet.~~ *(Run
2026-09-26, locally on Kind and not in CI — Kubernetes 1.36.1, containerd 2.3.1, runc 1.4.2,
Linux 6.10 — on one operator image built from the final code: green inside both full suites,
Valkey 9 and Valkey 8, and in two further Valkey 8 runs, its subtest for a `Localhost` profile
the operator's allow-list does not hold (ADR 0033 D9) included. What it proves on a node is
recorded in ADR 0033.)*

## Context

Two separate pressures produced the same rule.

The first is the anti-affinity feature itself. The 2026-08-19 incident was enabled by
co-location: all three data pods of the affected cluster sat on the node that was
drained — observed on that cluster and recorded in the header comment of
[`test/e2e/affinity_test.go`](../../test/e2e/affinity_test.go), not reproducible from
this repository. A pod anti-affinity term prevents that, and the first implementation
on this branch rendered it as `soft` for every multi-replica cluster with no way to
switch it off.

The second is what that would do on upgrade. A default `soft` term renders a new
`Affinity` block into every multi-replica pod template, which flips the pod-spec hash,
which starts a failover-aware rolling update of every cluster in the fleet — for a
behaviour change nobody asked for. The same argument recurs for every field the
operator adds: a CRD default is applied by the API server to objects that already
exist, so a "helpful" default is a fleet-wide mutation with no CR edit behind it.

## Decision

**D1 — New CRD features default to off, so an operator upgrade changes nothing.**
`podDisruptionBudget.enabled: false`, `tls.enabled: false`, `antiAffinity.mode: off`.
A feature the user did not opt into produces no object and no behavioural change.
~~Every behavioural change must be traceable to a CR edit.~~ *(superseded 2026-09-26 in
scope, see below)*

*Amended 2026-09-26* ([ADR 0032](0032-generated-pods-run-rootless.md) D1): **D1 governs
features, not the repair of a defect.** Every behavioural change a *feature* brings must be
traceable to a CR edit. The repair of a defect is not a feature: it has no CRD field, no opt-in
and no opt-out, and it reaches existing clusters with the operator upgrade. Running every
container on the Valkey image as uid 0 with the runtime's default capabilities was such a
defect — each of them sets `command:`, which bypasses the entrypoint that would have dropped to
the `valkey` user; the sidecar, the observer and the exporter ran as their images' non-root
users — so the rootless posture is rendered unconditionally (`applyValkeyPodSecurity`,
`applyObserverPodSecurity`) and rolls onto every cluster, apart from the deferred single pods
of the D11 amendment. The three defaults above are features and stay off.

**D2 — `spec.antiAffinity.mode` is an enum `off;soft;hard`, defaulting to `off`.**
Off renders no term at all. `soft` renders
`preferredDuringSchedulingIgnoredDuringExecution` with weight 100 — a scheduler
preference that can never block scheduling. `hard` renders
`requiredDuringSchedulingIgnoredDuringExecution`. `spec.antiAffinity.topologyKey`
defaults to `kubernetes.io/hostname`.

**D3 — Presence of the `antiAffinity` block is not an opt-in; only `mode: soft|hard`
is.** A block that sets only `topologyKey` is still off, because the API server
defaults `mode: off` into it. One unambiguous switch instead of two overlapping
signals. Documented at the field.

**D4 — An unknown mode falls back to the weakest setting, never to a constraint.**
`AntiAffinityMode()` resolves a nil block, an empty mode and any out-of-enum value to
`off`. The OpenAPI enum generated from `+kubebuilder:validation:Enum=off;soft;hard` makes
a bogus value unreachable through the API server, and that schema is the only validation
this project has — no admission webhook, ~~no CEL rule~~
([ADR 0015](0015-one-crd-validated-by-schema-only.md)) *(corrected 2026-09-26: the schema carries
~~one CEL rule~~ two CEL rules since [ADR 0033](0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md)
D1, both on `spec.podSecurity.seccompProfile` (`SeccompProfileSpec`): the first requires
`localhostProfile` exactly for `Localhost`, the second — added by D1's amendment the same day —
refuses a `localhostProfile` that is absolute or has a `..` element;
they are part of the CRD schema and touch nothing here — the mode enum is still enforced by the
OpenAPI enum alone)*. If it is ever bypassed — a stripped
CRD, a direct etcd write, a future schema change — the failure must be inert. Falling back
to a required term would leave pods `Pending` because of unparsed configuration: an
availability incident caused by defensive code.

**D5 — No anti-affinity term below `MinAntiAffinityReplicas` (2), per component.**
Same shape as the PDB skip rule ([ADR 0004](0004-opt-in-poddisruptionbudgets.md) D4),
evaluated by `NeedsDataAntiAffinity` / `NeedsSentinelAntiAffinity`. A singleton has no
peer to repel, and injecting the term would still flip the pod-template hash and
restart a standalone instance for nothing — which for `replicas: 1` without persistence
loses in-memory data.

**D6 — One builder for both components, reusing the StatefulSet selector labels.**
`BuildPodAntiAffinity(v, component)`
([`internal/builder/affinity.go`](../../internal/builder/affinity.go)) takes the
component and reuses `common.SelectorLabels` — exactly the label set the StatefulSet
selector uses — so each pod set repels only its own kind
(`app.kubernetes.io/instance` + `app.kubernetes.io/managed-by` +
`app.kubernetes.io/component`; `vko.gtrfc.com/cluster` is a `BaseLabels` key and is not in
the selector). A hand-written second selector could drift and produce a term that repels
the wrong set, or matches nothing and silently gives no spread.

**D7 — Anti-affinity changes ride the pod-spec hash; `podSpecChanged` is deliberately
not extended to compare `Affinity`.** The hash annotation ~~already covers the whole
`PodSpec`~~ *(corrected 2026-09-26: covers the whole `PodSpec` that `buildPodSpec` /
`buildSentinelPodSpec` produce; two things are stamped onto the built object afterwards, see
below)*, so a field-by-field comparison would be a second, partial source of truth
that has to be extended for every future pod-spec feature — exactly the drift the hash
exists to avoid.

*Amended 2026-09-26* ([ADR 0032](0032-generated-pods-run-rootless.md) D2): **one recorded
exception** ~~**rolls nothing.**~~ *(superseded 2026-09-26 after `bb6c78f`, see below)* **stays
outside the hash.** While `dataOwnershipRepairNeeded` finds the persisted template or a data pod
of a persistent cluster proven ours without `runAsNonRoot` — and until every ordinal holds a
migrated pod: proven ours, rootless and Ready, with no data-tier roll recorded (~~past its
pre-flight~~, tightened 2026-09-26, ADR 0032 D4) — `reconcileStatefulSet` inserts the
migration-only init container `fix-data-ownership` with `WithDataOwnershipRepair` on the *built*
StatefulSet, after `ComputePodSpecHash`. Adding and removing it writes the StatefulSet
(`containersChanged` compares init containers) but never moves the pod-spec-hash annotation, so
~~no pod becomes outdated on its account~~ neither template write is itself a roll. ~~The
justification is narrow: the container acts only at pod start, and a pod that ran it is
identical to one that did not need it; inside the hash every persistent cluster would roll
twice.~~ Guards: `TestWithDataOwnershipRepair_IsHashNeutral` (the data pod's posture inside the
hash, the repair outside it) and ~~`TestReconcileStatefulSet_RepairComesAndGoesWithoutARoll`~~
`TestReconcileStatefulSet_RepairComesAndGoesAndTheRetiredRepairRolls`. The other post-builder
stamp, the TLS material record of [ADR 0031](0031-a-record-the-operator-trusts-lives-in-pod-spec.md)
D3, is not an exception of this kind and was never recorded here: it stays out of the hash so
that one rotation moves one signal, and it rolls pods through its own per-pod comparison.
**Anything else stamped after the hash is invisible to the roll until a per-pod comparison of
its own names it, and owes the same recorded justification.** *(Refined 2026-09-26 with the
second roll below; it read "is invisible to the roll and owes the same recorded
justification".)*

*Re-decided 2026-09-26, after `bb6c78f`* (ADR 0032 D2, decided by Hans): **the repair's
retirement rolls the pods that carry it — a second roll of every persistent cluster the
migration repaired.** The struck justification was wrong about the pods: one created while the
template carried the repair keeps a uid-0 init container in its immutable spec, re-runs it on
every sandbox restart and fails a Pod Security `restricted` check, so it is not identical to one
that did not need it.
`podCarriesRetiredRepair` is true when the pod spec carries `fix-data-ownership` and the
persisted template no longer does, and `podOutdated` — `podNeedsUpdate` against every input of
the persisted template, or that — is what every data-tier site asks: the dispatch loop,
`collectPodStates`, `handleStandaloneRollingUpdate` and the master check of
`handlePostManualFailover`
([`internal/controller/rolling_update.go`](../../internal/controller/rolling_update.go)). The
repair leaves the template only once every ordinal holds a migrated pod, so it is this
comparison, not the hash, that starts the second roll — the ordinary failover-aware one — and
after it no data pod of the tier carries the root container. *(Ordering, added 2026-09-26 on two
fleet-upgrade runs, ADR 0032 D4: the repair also stays while a data-tier roll is recorded, so the
second roll cannot overtake the first before it finalizes — the first run counted one
`RollingUpdateComplete` per persistent tier instead of two; and because the removal then falls
into the pass after the completion, which nothing scheduled — the second run found the repair
stranded — `finishDataRoll` requests that pass with `requestRecheck` when the template it
completed against still carries the repair.)* The comparison is one-way: a
template that gains the repair makes no pod outdated, a pod created without it is not outdated
by its removal, and a pod missing during the second roll is no evidence, so the repair does not
return. The alternative — keep it in those pod specs until their next replacement for any other
reason — was the recommendation and lost. Guards, unit: `TestPodCarriesRetiredRepair`, the
renamed test above (one pod at a time, and the repair stays gone) and
`TestHandleStandaloneRollingUpdate_ReplacesAPodCarryingTheRetiredRepair`, and for the ordering
`TestDataOwnershipRepairNeeded_StaysWhileARollIsRecorded` and
`TestCompletedRoll_AsksForThePassThatRemovesTheRepair`. ~~**Not verified:** the second roll end to
end~~ **Verified locally, not in CI** *(2026-09-26)*: the second roll end to end — the changed
`TestE2E_FleetUpgrade` ~~has not run~~ has run twice and found the two ordering defects above,
~~and its rerun on the fixed code has not run yet~~ and its rerun on the fixed code is green, again
on the final image of the branch (see Status).

The refusal to compare `Affinity` field by field stands, but it does not generalise to every
field: the hash detects a change of the *desired* spec, while only a field comparison converges
an out-of-band edit of the *persisted* template back. `podSpecChanged` therefore compares
`AutomountServiceAccountToken` ([ADR 0012](0012-the-sidecar-records-its-drain-promotion-on-the-pod.md)
D8 step 4) and the pod- and container-level `securityContext` fields the operator sets (ADR
0032 D5), with subset semantics except for `capabilities.add`, which the live template may not
grow — the out-of-band edit there is a capability grant. *(Amended 2026-09-26 by
[ADR 0033](0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md) D2:)* it
also compares `enableServiceLinks` as a subset and `hostUsers` **exactly**
(`podHardeningChanged`), because opting out of the user namespace leaves the desired field unset
and a subset comparison would keep the persisted `false`. Both opt-ins are nevertheless decided
through the hash like every other pod-spec feature: they move `ComputePodSpecHash` and
`ComputeSentinelPodSpecHash` and roll the tiers, while an explicit `RuntimeDefault` moves neither
hash (`TestPodHardening_OptInsMoveThePodSpecHashes`).

**D8 — Hard mode's degraded state is `Pending`, and it is documented at the field.**
With fewer schedulable topology domains than replicas, surplus pods stay `Pending`;
enabling `hard` on a constrained cluster wedges its next rolling update; during a node
drain an evicted pod stays `Pending` until a node without a replica of the same cluster
is schedulable. That is deliberate — staying `Pending` preserves the spread guarantee
instead of silently re-co-locating. Because the failure mode is availability loss under
node pressure, it belongs at the CRD field, where the person who sets `hard` will read
it, not in a release note.

**D9 — Boolean-looking enum defaults are quoted in the generated CRD.** controller-gen
emits `"off"`; an unquoted bare `off` is parsed as the boolean `false` by YAML and the
default would be dropped or rejected at install time — a failure that appears only when
the CRD is applied, not when the Go code compiles. Any future enum value YAML treats as
a boolean or number (`on/off/yes/no/y/n`) needs the same check after
`make generate-all`.

**D10 — Condition clears are guarded by a presence check, and the guard sits in a
wrapper.** `meta.SetStatusCondition` *adds* an absent condition and reports a change,
so an unguarded `setSidecarUpdatePendingCondition(ctx, v, false)` would write
`SidecarUpdatePending=False` onto every CR in the fleet on the first upgraded pass.
The setter is not the guarded function: `clearSidecarUpdatePending`
([`internal/controller/valkey_controller.go`](../../internal/controller/valkey_controller.go))
returns when `meta.FindStatusCondition` finds no `SidecarUpdatePending`, and only
otherwise calls the setter with `false`. That wrapper is what the steady-state path in
`checkAndHandleRollingUpdate` calls; the raw setter keeps one direct caller in
`handleStandaloneRollingUpdate`
([`internal/controller/rolling_update.go`](../../internal/controller/rolling_update.go)),
which is reachable only while an update is needed or a rolling-update state annotation
is set — never on a pass over an untouched CR. Upgrade neutrality covers `status`, not
only `spec`.

**D11 — The one-time rotation that the sidecar image already causes is the baseline,
not an exception.** Every Valkey data pod carries the operator image as its sidecar, and
`ComputePodSpecHash` covers it, so **every operator release already rolls every
multi-replica data StatefulSet** with one controlled failover per cluster. A pod-spec
change that rides along in that same pass adds nothing and earns no release note. The
boundaries are recorded rather than glossed:

* ~~Sentinel pods carry no sidecar, so the Sentinel StatefulSet is the one pod class a
  plain operator upgrade does not roll.~~ *(superseded 2026-09-26, see below)* Sentinel
  pods carry no sidecar, so the Sentinel StatefulSet is the one pod class a plain operator
  upgrade does not roll **unless the release changes the Sentinel pod spec or the generated
  configuration** — `sentinelPodNeedsUpdate` compares the pod-spec hash and the config hash,
  and `ComputeConfigHash` covers `sentinel.conf` as well as `valkey.conf`.
* On the kustomize path and on a Helm install with a floating `image.tag`, the sidecar
  image is a static string that no upgrade changes — there a pod-spec change *is* a new
  roll. That path sits outside the canonical upgrade path and is the deviating admin's
  responsibility ([ADR 0014](0014-rbac-lives-in-three-places.md) D8).

*Amended 2026-09-26* ([ADR 0032](0032-generated-pods-run-rootless.md) D1, Consequences):
**the release that makes pods rootless rolls the Sentinel tier once.** `buildSentinelPodSpec`
calls `applyValkeyPodSecurity` last and `ComputeSentinelPodSpecHash` digests that spec, so the
posture moves the Sentinel pod-spec hash, `sentinelPodNeedsUpdate` reports every Sentinel pod
that carries a hash annotation outdated, and the Sentinel roll replaces each one once. *(On a
tier of one or two Sentinels only since [ADR 0024](0024-the-sentinel-tier-reports-its-own-completion.md)
D10, decided 2026-09-26 after `bb6c78f`: there the quorum equals the size, the roll's quorum
guard refused every delete of an available Sentinel, and a healthy tier never rolled; it now rolls
serially, one Sentinel at a time and only while every other one is available —
`sentinelDeleteKeepsVotes`.)* The absence of a sidecar never exempted the tier; the absence of
a Sentinel pod-spec or configuration change did — by reading, v1.11.0 (`458605b`, an explicit
`terminationGracePeriodSeconds`) moved that hash too. The same release goes beyond the baseline
in two more places: it is a new data-tier roll on the kustomize/floating-tag path of the second
bullet, and it restarts every persistent single-pod cluster without Sentinel ~~once~~ (ADR 0032
D3), where a single pod whose only drift is the sidecar image is deferred (ADR 0007 D6).
*(Re-decided 2026-09-26 after `bb6c78f`, see the D7 re-decision: a third place, on every
install path — every persistent data tier rolls a second time when the ownership repair leaves
the template, one more controlled failover per persistent multi-replica cluster, and the
persistent single pod without Sentinel restarts twice, for the posture and then for the retired
repair, each a short downtime with its data kept.)*
Non-persistent single pods without Sentinel are deferred while their Valkey image is unchanged,
and reported by `PodSecurityUpdatePending`. **Not verified:** that the posture alone moves the
Sentinel hash rests on reading `podSpecDigest` (the JSON of the whole built spec); no unit test
pins it for the Sentinel spec. `TestE2E_FleetUpgrade` passed locally on 2026-09-26 (Kind, not
CI) from the released chart 1.12.8: the Sentinel tier completed exactly one roll — one
`SentinelUpdateComplete` Event — and no pod was replaced in the 90 s after the ownership repair
left the template *(the behaviour re-decided in D7; the changed test waits for the second roll,
still asserts one Sentinel roll, and asserts that nothing rolls in the 90 s after it — ~~not
run~~ run twice since, each run finding a data-tier ordering defect, ~~and not yet rerun on the
fix~~ and green on the fix and on the final image of the branch, 2026-09-26, see Status — so each
Sentinel tier of the fleet completed exactly one roll there too)*. Its default starting release 1.10.48 could not run on the arm64 host used (the
released images are amd64-only), and from there it could not have attributed the roll to the
posture anyway, because v1.11.0 lies on that path. From 1.12.8, v1.11.0 is off the path;
whether nothing else between 1.12.8 and this branch moves the Sentinel hash was not checked, so
the run shows that the tier rolled once, not why.

## Consequences

* **The incident's enabling co-location is not prevented on the default path.** The
  spread is opt-in. README, the CRD field docs and the Helm `values.yaml` therefore
  recommend `mode: soft` for every multi-replica cluster, and enabling it later costs
  one failover-aware rolling update — lossless for multi-replica clusters.
* The safest posture is never the one a user gets by accident: unauthenticated,
  unencrypted, unbudgeted, unspread clusters are the default — and no document asks for
  them to be turned on in one place. Section 5 of
  [SECURITY_ARCHITECTURE.md](../../SECURITY_ARCHITECTURE.md) names two of the four as
  schema defaults (`tls.enabled: false`, `podDisruptionBudget.enabled: false`); its
  hardening checklist in section 9 has **no** item for enabling auth, TLS, PDBs or
  anti-affinity — its two adjacent items ("Require client certificates where the
  deployment can", "Do not leave `spec.sentinel.disableAuth` or either
  `allowUnencrypted` on") presuppose TLS and auth are already enabled. The
  recommendation to opt in lives at the CRD fields, in README and in the Helm
  `values.yaml`; the auth and TLS posture and its cost are recorded in
  [ADR 0016](0016-authentication-and-tls-posture.md). Every new field must follow the
  same rule.
* A `topologyKey`-only block is a silent no-op. Documented at the field; nothing rejects
  it.
* A typo that somehow bypasses the CRD schema validation silently yields no spread
  instead of a visible error (D4) — the accepted direction of failure.
* A node can host one data pod *and* one Sentinel pod: anti-affinity gives no protection
  against losing both to the same node failure, because cross-repelling would forbid a
  normal and desirable layout on small clusters.
* Any new pod-spec-level feature inherits rolling-update detection for free (D7), but
  only as long as the hash keeps covering the whole `PodSpec`. The hash tests are the
  guard. Since 2026-09-26 the ownership repair is deliberately outside it (D7 amendment);
  `TestWithDataOwnershipRepair_IsHashNeutral` pins both directions for the data pod — the
  posture inside the hash, the repair outside it. Outside the hash is not outside the roll:
  since the re-decision of the same day its retirement rolls the pods that carry it, through
  `podCarriesRetiredRepair` (D7).
* Users see a controlled failover per multi-replica cluster on **every** operator
  upgrade, permanently (D11). The only written mention is the README upgrade paragraph
  "What it does to running clusters", which names the sidecar operator image as the
  cause — and it sits inside the collapsed `<details>` block of the fast start. The rootless
  release adds a second one to every persistent multi-replica cluster (D11 amendment).
* Users on kustomize or a floating tag get an unannounced rolling update on releases
  that change the pod spec.
* Scaling 1 → 3 with an enabled mode adds the term and rolls the pods at that point;
  scaling 3 → 1 removes it.
* Node-spread itself is only asserted in `TestE2E_AntiAffinity_HardSpreadsAcrossNodes`,
  which needs three schedulable nodes and skips below that — a failure instead when
  `E2E_REQUIRE_MULTI_NODE=true`. Soft is a preference and not deterministic, so the soft
  test asserts only the rendered term. The negative case,
  `TestE2E_AntiAffinity_HardLeavesSurplusPending`, is node-count independent by
  construction: it collapses every node into one spread domain with
  `topologyKey: kubernetes.io/os` rather than cordoning nodes out from under the tests
  running in parallel.

## Alternatives Considered

### Default `hard`

Decided 2026-08-19 and revised the same day, before it reached a commit — no version of
`api/v1/valkey_types.go` in this repository ever carried `+kubebuilder:default=hard`, so
the only record of that decision is the untracked admission-gap ticket. The reasons for
reversing it stand on their own: `hard` wedges any cluster with fewer schedulable spread
domains than replicas, and it changed the e2e topology requirements.

### Default `soft`, with no off switch

WP5 as built in `0c8b424`, reversed on 2026-08-20 by `146ffa5`. (The work-package
numbering lives in the admission-gap ticket, which is untracked — only the commits are
in this repository.) It renders a term into every multi-replica pod template on upgrade,
flipping the hash and rolling the fleet for a change the user never requested.

### Treat block presence as opt-in with an implicit `soft`

Rejected: it makes an omitted `mode` behave differently from an omitted block, and
reintroduces an upgrade-visible default.

### Fall back to `hard` or `soft` on an unknown mode, or error out

Rejected: an unrecognised value must never add a scheduling constraint, and erroring
would block reconciliation on a field the API server already validates.

### Separate anti-affinity builders per component, or one shared cluster-wide selector

Two selectors can drift; one shared selector forbids co-locating a Sentinel with a data
pod, which is valid and desirable on small clusters.

### Add `Affinity` to the explicit `podSpecChanged` comparison

Rejected: duplicates what the hash already guarantees and must then be maintained per
field. *(Narrowed 2026-09-26: the hash guarantees that a change of the desired spec is
detected, not that an out-of-band edit of the persisted template converges back. For
`Affinity` that gap is accepted; for `securityContext` it was not — see the D7 amendment.)*

### Secure-by-default (TLS on, PDBs on)

Rejected for upgrade neutrality. The cost is named explicitly above rather than hidden.

### A `BREAKING CHANGE:` footer, a major version bump, or a release note for the
### init-script roll

All rejected together with their premise. The claim — that the default anti-affinity
term would trigger "an orchestrated mass-failover event" — comes from the review record
in the untracked admission-gap ticket, not from anything in this repository. Verified by
reading the source, the data StatefulSet rolls on every release anyway (D11), so there
was nothing new to announce.

### Extract the inline init script into a ConfigMap to keep it out of the hash

Not taken. It would make init-script edits upgrade-neutral, at the cost of a second
object in the boot path.

## Residual risks

* **Init-script edits are not upgrade-neutral by construction**, and they are rolled
  through the very manual-failover cycle they modify. The first post-upgrade pass
  executes the most-rewritten code once per multi-replica cluster. That is an argument
  for landing the failover hardening promptly, not for a release note — but it is a real
  tension with D1 and is recorded as such.
* `hard` mode can leave pods `Pending` indefinitely on a cluster with fewer topology
  domains than replicas.
* Clusters that never opt in get no spread guarantee at all.
* **The feature/defect line of the D1 amendment is a judgement, not a mechanism.** Nothing in
  the code tells the two apart; the next change that calls itself the repair of a defect takes
  the same door — a fleet-wide roll with no CR edit behind it — and has to argue it the way
  [ADR 0032](0032-generated-pods-run-rootless.md) did.

## References

* [`internal/builder/affinity.go`](../../internal/builder/affinity.go) — `BuildPodAntiAffinity`
* [`api/v1/valkey_types.go`](../../api/v1/valkey_types.go) — `AntiAffinityMode()`, `IsAntiAffinityEnabled()`, `NeedsDataAntiAffinity`, `NeedsSentinelAntiAffinity`, `MinAntiAffinityReplicas`
* [`internal/builder/statefulset.go`](../../internal/builder/statefulset.go) — `ComputePodSpecHash`, `buildSidecarContainer`, the data-pod wiring of `BuildPodAntiAffinity`
* [`internal/builder/sentinel.go`](../../internal/builder/sentinel.go) — the Sentinel wiring of `BuildPodAntiAffinity`, `ComputeSentinelPodSpecHash` (no sidecar, no operator image; `applyValkeyPodSecurity` inside `buildSentinelPodSpec`, so the posture is in that hash)
* [`internal/builder/pod_security.go`](../../internal/builder/pod_security.go) — `applyValkeyPodSecurity`, `applyObserverPodSecurity`, `WithDataOwnershipRepair` (the D7 exception), `HasDataOwnershipRepair`, `podSecurityContextChanged`, `containerSecurityContextChanged`, `podHardeningChanged` (`hostUsers` exact, ADR 0033 D2), `applyPodHardening`
* [`internal/controller/pod_security_migration.go`](../../internal/controller/pod_security_migration.go) — `dataOwnershipRepairNeeded`, `singlePodDeferral`, `reportPodSecurityUpdatePending`
* [`internal/controller/valkey_controller.go`](../../internal/controller/valkey_controller.go) — `reconcileStatefulSet`, where the repair is inserted after the builder
* [`internal/controller/rolling_update.go`](../../internal/controller/rolling_update.go) — `sentinelPodNeedsUpdate`, `podSpecHashChanged`, `podOutdated` and `podCarriesRetiredRepair` (the D7 re-decision), `finishDataRoll` (the recheck that lets the repair leave), `sentinelDeleteKeepsVotes` (ADR 0024 D10)
* Tests: [`internal/builder/pod_security_test.go`](../../internal/builder/pod_security_test.go) (`TestWithDataOwnershipRepair_IsHashNeutral`), [`internal/controller/pod_security_migration_test.go`](../../internal/controller/pod_security_migration_test.go) (`TestReconcileStatefulSet_RepairComesAndGoesAndTheRetiredRepairRolls`, `TestPodCarriesRetiredRepair`, `TestHandleStandaloneRollingUpdate_ReplacesAPodCarryingTheRetiredRepair`, `TestDataOwnershipRepairNeeded_StaysWhileARollIsRecorded`, `TestCompletedRoll_AsksForThePassThatRemovesTheRepair`), [`internal/builder/pod_hardening_test.go`](../../internal/builder/pod_hardening_test.go) (`TestPodHardening_OptInsMoveThePodSpecHashes`), [`internal/controller/pod_availability_test.go`](../../internal/controller/pod_availability_test.go) (`TestSentinelDeleteKeepsVotes`, `TestSentinelRollingUpdate_SmallTiersRollSerially`), [`test/e2e/fleet_upgrade_test.go`](../../test/e2e/fleet_upgrade_test.go) (passed locally on Kind 2026-09-26 from chart 1.12.8 before the second roll existed; ~~the changed version has not run~~ the changed version ran twice and found the two roll-ordering defects, ~~the rerun on the fix has not run yet~~ the rerun on the fix is green, and green again on the final image of the branch (2026-09-26); not a CI job), [`test/e2e/pod_hardening_test.go`](../../test/e2e/pod_hardening_test.go) (`TestE2E_PodHardening_UserNamespacesLocalhostSeccompAndDigest`, ADR 0033; green 2026-09-26 on both Valkey lines, locally)
* [ADR 0004](0004-opt-in-poddisruptionbudgets.md) — the same opt-in and replica-minimum shape
* [ADR 0007](0007-failover-aware-rolling-update.md) — what a hash change actually costs; D6, the sidecar-only deferral of a single pod
* [ADR 0012](0012-the-sidecar-records-its-drain-promotion-on-the-pod.md) D8 step 4 — the `AutomountServiceAccountToken` line in `podSpecChanged`
* [ADR 0016](0016-authentication-and-tls-posture.md) — the security defaults this rule produces, and their cost
* [ADR 0024](0024-the-sentinel-tier-reports-its-own-completion.md) D10 — a tier of one or two Sentinels rolls serially, so the D11 amendment's Sentinel roll reaches it
* [ADR 0031](0031-a-record-the-operator-trusts-lives-in-pod-spec.md) D3 — the TLS material record, stamped after the hash with a signal of its own
* [ADR 0032](0032-generated-pods-run-rootless.md) — the amendment of D1, D7 and D11: rootless pods, the ownership repair and its second roll of persistent tiers, the one-time Sentinel roll
* [ADR 0033](0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md) — two opt-in features under D1 (`Localhost` seccomp, user namespaces), the two CEL rules that correct D4, the exact `hostUsers` comparison of D7
