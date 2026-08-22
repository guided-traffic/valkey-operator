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
Every behavioural change must be traceable to a CR edit.

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
this project has — no admission webhook, no CEL rule
([ADR 0015](0015-one-crd-validated-by-schema-only.md)). If it is ever bypassed — a stripped
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
not extended to compare `Affinity`.** The hash annotation already covers the whole
`PodSpec`, so a field-by-field comparison would be a second, partial source of truth
that has to be extended for every future pod-spec feature — exactly the drift the hash
exists to avoid.

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

* Sentinel pods carry no sidecar, so the Sentinel StatefulSet is the one pod class a
  plain operator upgrade does not roll.
* On the kustomize path and on a Helm install with a floating `image.tag`, the sidecar
  image is a static string that no upgrade changes — there a pod-spec change *is* a new
  roll. That path sits outside the canonical upgrade path and is the deviating admin's
  responsibility ([ADR 0014](0014-rbac-lives-in-three-places.md) D8).

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
  guard.
* Users see a controlled failover per multi-replica cluster on **every** operator
  upgrade, permanently (D11). The only written mention is the README upgrade paragraph
  "What it does to running clusters", which names the sidecar operator image as the
  cause — and it sits inside the collapsed `<details>` block of the fast start.
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
field.

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

## References

* [`internal/builder/affinity.go`](../../internal/builder/affinity.go) — `BuildPodAntiAffinity`
* [`api/v1/valkey_types.go`](../../api/v1/valkey_types.go) — `AntiAffinityMode()`, `IsAntiAffinityEnabled()`, `NeedsDataAntiAffinity`, `NeedsSentinelAntiAffinity`, `MinAntiAffinityReplicas`
* [`internal/builder/statefulset.go`](../../internal/builder/statefulset.go) — `ComputePodSpecHash`, `buildSidecarContainer`, the data-pod wiring of `BuildPodAntiAffinity`
* [`internal/builder/sentinel.go`](../../internal/builder/sentinel.go) — the Sentinel wiring of `BuildPodAntiAffinity`, `ComputeSentinelPodSpecHash` (no sidecar, no operator image)
* [ADR 0004](0004-opt-in-poddisruptionbudgets.md) — the same opt-in and replica-minimum shape
* [ADR 0007](0007-failover-aware-rolling-update.md) — what a hash change actually costs
* [ADR 0016](0016-authentication-and-tls-posture.md) — the security defaults this rule produces, and their cost
