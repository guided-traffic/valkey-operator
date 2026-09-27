---
id: T52
title: the sidecar and the data pod's init containers state no resources, so a cpu/memory ResourceQuota without LimitRange defaults refuses the data pods  # was "... so a cpu/memory ResourceQuota refuses the data pods" until 2026-09-27; a namespace LimitRange admits them (Fact)
state: analysed       # was filed until 2026-09-27: code, the upstream admission mechanism (ResourceQuota, LimitRanger, pod-level resources) and the options verified by reading at 84a39c2; nothing measured end to end, nothing decided
severity: low         # without LimitRange defaults a cpu/memory quota refuses every new data pod: a fresh CR gets no pods, an existing tier loses each replaced pod (roll, eviction, drain, chaos kill) until the namespace gains defaults; nothing else breaks. Comment corrected 2026-09-27, was "a quota namespace cannot host a data tier at all; nothing else breaks"
security: hardening   # kept 2026-09-27 against a proposal of none (History): the quota fails closed, but the gap is tracked as H-18 on a security page and is resource isolation, defense in depth
threat: "would additionally bound, once values are set (a namespace LimitRange today, a field under option B), the CPU and memory the data pod's sidecar and init containers can take from co-located pods on their node; today nothing the operator writes bounds them"  # rewritten 2026-09-27, was "... today the data pods are refused there, so such a namespace cannot host a Valkey data tier unless the quota is loosened" - false, a LimitRange admits them under an unchanged quota
urgency: later        # rule 4, first match: documenting the LimitRange path is a cheap known fix that needs no decision and reopens no ADR; was icebox (rule 5) until 2026-09-27
effort: S             # the recommended course: documentation in about seven places, the ADR 0033 amendment, one unit test; option B alone stays M. Was M until 2026-09-27, S before that
blocked-by: decision  # D1 and D2, see Options; the documentation and the unit test in the Work list are not blocked
filed-from: T31, section "Further security measures — not in this change, each open" (archive/031)
opened: 2026-09-27
decided:              # not recorded - no decision yet
done:                 # not recorded - not done
---

Filed on 2026-09-27 from the fifth row of the table
["Further security measures — not in this change, each open"](archive/031-generated-pods-run-as-root.md#further-security-measures--not-in-this-change-each-open)
in the archived ticket 031, which is `done` and tracks none of its rows. The row itself notes
"ADR 0033 D7 (decided: no defaults)" ([archive/031:682](archive/031-generated-pods-run-as-root.md)).
The operator-facing statement is gap [H-18](../security/workload-pod-posture.md#h-18).

## Fact

**Verified** (read 2026-09-27, re-read at `84a39c2`):

- In [`statefulset.go`](../../internal/builder/statefulset.go) only the `valkey` container
  ([statefulset.go:871](../../internal/builder/statefulset.go), `v.Spec.Resources`) and the
  exporter ([statefulset.go:1124-1125](../../internal/builder/statefulset.go),
  `spec.metrics.resources`) are given container resources. `buildSidecarContainer`
  ([statefulset.go:894](../../internal/builder/statefulset.go)) sets none (`Resources` occurs 0
  times in lines 894-1043), and neither do the data pod's init containers —
  `init-config-selector` (built at lines ~~274 and 435~~ *(corrected 2026-09-27: 275 and
  436)*; the Sentinel branch and the multi-replica branch without Sentinel) and the containers
  of [`pod_security.go`](../../internal/builder/pod_security.go), which contains no `Resources`
  at all (`dataWritableCheck`, [pod_security.go:176](../../internal/builder/pod_security.go),
  persistent pods only; `dataOwnershipRepairContainer`,
  [pod_security.go:213](../../internal/builder/pod_security.go); `WithDataOwnershipRepair`,
  [pod_security.go:247](../../internal/builder/pod_security.go)).
- [ADR 0033](../adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md)
  D7: a cpu/memory `ResourceQuota` admits a pod without pod-level resources only when every
  container, init containers included, states the values (read in Kubernetes v1.36.4); the
  sidecar and the data pod's init containers deliberately keep stating none, because a limit
  guessed too low OOM-kills the process that holds the drain promotion
  ([ADR 0012](../adr/0012-the-sidecar-records-its-drain-promotion-on-the-pod.md)). Re-read
  2026-09-27 in
  [pods.go v1.36.4](https://raw.githubusercontent.com/kubernetes/kubernetes/v1.36.4/pkg/quota/v1/evaluator/core/pods.go),
  `podEvaluator.Constraints` (lines 129-171): it loops `pod.Spec.Containers` and
  `pod.Spec.InitContainers` through `enforcePodContainerConstraints` (lines 275-291) against the
  quota's resources intersected with `{cpu, memory, requests.cpu, requests.memory, limits.cpu,
  limits.memory}`.
- ADR 0033's residual risks: pod-level `spec.resources` (`PodLevelResources`, on by default
  since Kubernetes 1.34, read in v1.36.4) exempts a pod from the per-container quota check
  (`Constraints` returns nil first when the gate is on and pod-level resources are set). The
  [README](../../README.md) declares Kubernetes v1.29+.

*Added 2026-09-27 (enrichment, at `4a7543e`):*

- **ADR 0033 already rejected option B by name.** Its *Alternatives Considered* (ADR 0033
  lines 484–485) read: "Fields for the sidecar and the init containers as well. More API surface
  with no known user." Lines 481–483 reject D (measured defaults), and lines 711–713 record
  pod-level resources (C) as "not weighed in D7". Choosing B therefore overturns a recorded
  rejection, and the reason to overturn it is a known user. This ticket names none.
- The existing resource fields sit at
  [valkey_types.go:512](../../api/v1/valkey_types.go) (Sentinel),
  [valkey_types.go:673](../../api/v1/valkey_types.go) (metrics),
  [valkey_types.go:975](../../api/v1/valkey_types.go) (observer) and
  [valkey_types.go:1074](../../api/v1/valkey_types.go) (`spec.resources`). D7's Sentinel
  pattern mirrors one field onto every Sentinel container, init included
  ([sentinel.go:397-402](../../internal/builder/sentinel.go)). The README states the v1.29
  floor at [README.md:198](../../README.md).
- **An unset field rolls nothing, and a written value rolls the tier.** The pod-spec hash is
  FNV-32a over the JSON of the whole built `PodSpec` (`ComputePodSpecHash` and `podSpecDigest`,
  [statefulset.go:1228-1239](../../internal/builder/statefulset.go)). Container `resources` is
  serialised today as an empty object (`k8s.io/api` v0.37.1 `core/v1/types.go:3197`,
  `json:"resources,omitempty"` on a struct, which `encoding/json` never omits; `limits` and
  `requests` inside it are omitempty maps), so a new field left unset leaves the JSON, and the
  hash, unchanged. A value newly written into any container changes the hash and rolls the tier.
- ~~`k8s.io/api` v0.37.1 carries `PodSpec.Resources`, but its doc comment still calls the field
  alpha behind the `PodLevelResources` gate. This is at odds with ADR 0033's "on by default
  since 1.34", which was not re-checked here (see Not verified).~~ *(corrected 2026-09-27 at
  84a39c2: re-checked, ADR 0033 is right and the upstream doc comment is stale. `k8s.io/api`
  v0.37.1 `core/v1/types.go:4677-4678` still says "This is an alpha field and requires enabling
  the PodLevelResources feature gate" (field at :4682), but
  [kube_features.go v1.36.4](https://raw.githubusercontent.com/kubernetes/kubernetes/v1.36.4/pkg/features/kube_features.go)
  and the same file at v1.37.1 declare `PodLevelResources` Alpha default-off in 1.32 and Beta
  default-on since 1.34; it is still Beta in v1.37.1.)*

*Added 2026-09-27 (re-verification at `84a39c2`; upstream read at the cited tags, nothing
measured):*

- **A namespace LimitRange admits the data pods under an unchanged cpu/memory quota.** The
  `LimitRanger` admission plugin is enabled by default and is mutating; mutating admission runs
  before validating admission, where `ResourceQuota` enforces
  ([admission controllers](https://kubernetes.io/docs/reference/access-authn-authz/admission-controllers/):
  "In the first phase, mutating admission controllers are run. In the second phase, validating
  admission controllers are run."). At pod creation it fills the LimitRange's `defaultRequest`
  into `requests` and `default` into `limits` of every container **and init container**
  ([limitranger/admission.go v1.36.4](https://raw.githubusercontent.com/kubernetes/kubernetes/v1.36.4/plugin/pkg/admission/limitranger/admission.go):
  `defaultContainerResourceRequirements` lines 216-233, `mergePodResourceRequirements` lines
  274-292). The upstream quota page names the pairing: "You can define a LimitRange to force
  defaults on pods that make no compute resource requirements"
  ([resource quotas](https://kubernetes.io/docs/concepts/policy/resource-quotas/)). No tracked
  file of this repository mentions it: `git grep -n -i 'limitrange\|limit range'` returns
  nothing.
- **The defaulting is per resource key.** `mergeContainerResources`
  ([admission.go v1.36.4](https://raw.githubusercontent.com/kubernetes/kubernetes/v1.36.4/plugin/pkg/admission/limitranger/admission.go)
  lines 236-270) sets a default key only where the container's own list lacks it (lines 247 and
  254, `if !found`). A container that states a memory request but no memory limit therefore
  still receives a LimitRange's default memory limit.
- **A LimitRange with `defaultRequest` only, and no `default` and no `max`, adds requests and
  no limit.** `SetDefaults_LimitRangeItem`
  ([core/v1/defaults.go v1.36.4](https://raw.githubusercontent.com/kubernetes/kubernetes/v1.36.4/pkg/apis/core/v1/defaults.go)
  lines 360-390) fills `default` from `max`, `defaultRequest` from `default`, then from `min`;
  no path derives a limit from `defaultRequest`. A `max` alone therefore does become a default
  limit. `podComputeUsageHelper` (pods.go v1.36.4) counts a request under both the bare name
  and `requests.*`, and `limits.*` only from `limits`, so a requests-only default satisfies a
  quota on `cpu`, `memory`, `requests.cpu` or `requests.memory`; a quota on `limits.cpu` or
  `limits.memory` needs `default` limits, which then reach the sidecar and the init containers.
  The upstream [limit range page](https://kubernetes.io/docs/concepts/policy/limit-range/) adds
  that a default limit below a request the container sets makes the pod invalid.
- **A LimitRange default reaches every container that lacks the key, not only the data
  pod's.** It also fills the exporter when `spec.metrics.resources` is unset, every Sentinel
  container when `spec.sentinel.resources` is unset, and the observer's limits: the observer's
  default is a 50m/64Mi **request** and no limit (`GetObserverResources`,
  [valkey_types.go:1408-1418](../../api/v1/valkey_types.go); used at
  [observer.go:77](../../internal/builder/observer.go)), so under a `limits.*` quota the
  observer too needs either `spec.observer.resources` with limits or a namespace default.
- **The operator does not fight a LimitRange-defaulted pod.** `LimitRanger` acts on Pods and
  PersistentVolumeClaims only, and not on a Pod update (`SupportsAttributes`, admission.go
  v1.36.4 lines 420-440), so it never touches the StatefulSet template. The data tier asks
  `podOutdated` ([rolling_update.go:445-449](../../internal/controller/rolling_update.go)),
  whose `podSpecHashChanged`
  ([rolling_update.go:515-525](../../internal/controller/rolling_update.go)) returns on the
  pod-spec-hash annotation when the pod carries it (stamped on the template at
  [statefulset.go:154](../../internal/builder/statefulset.go)); the direct resource comparison
  `containersResourceChanged` ([rolling_update.go:529-546](../../internal/controller/rolling_update.go))
  runs only for a pod without the annotation; such a legacy pod, once defaulted, is judged
  outdated (`resourceListEqual`, [rolling_update.go:550-561](../../internal/controller/rolling_update.go),
  finds a non-empty list against the template's empty one; only `Containers` are compared) and
  is replaced once by a pod that carries the annotation. The Sentinel tier's `sentinelPodNeedsUpdate`
  ([rolling_update.go:4838-4861](../../internal/controller/rolling_update.go)) follows the same
  hash-first rule. The `kubernetes.io/limit-ranger` annotation the plugin stamps (admission.go
  line 49) is not a key the operator reads. A policy that mutated the **StatefulSet template**
  instead would be reverted on every pass by the exact resource comparison of `containerChanged`
  ([statefulset.go:1348-1350](../../internal/builder/statefulset.go)).
- **That operator-side half is not pinned by a test.**
  `TestPodNeedsUpdate_PodSpecHashMatch_NoUpdate`
  ([rolling_update_test.go:225-239](../../internal/controller/rolling_update_test.go)) passes
  `nil` desired containers and a pod without resources; with an empty desired list
  `containersResourceChanged` never reports a change, so a mutation that compared resources
  after a hash match would survive it. Its Sentinel twin,
  `TestSentinelPodNeedsUpdate_PodSpecHashMatch_NoUpdate`
  ([rolling_update_test.go:2126-2148](../../internal/controller/rolling_update_test.go)), has
  the same gap: pod and template carry identical containers without resources.
- **The impact on an existing tier is every replacement, not only a roll.** A quota is enforced
  at admission only ("Neither contention nor changes to quota will affect already created
  resources", resource quotas page), so a quota added later leaves running pods alone, but the
  StatefulSet controller cannot recreate any data pod that disappears: roll, eviction, node
  drain, node loss, or the Chaos Mesh pod-kill of the database-examples namespace. Inside a roll
  `recreationWait` ([rolling_update.go:2163-2187](../../internal/controller/rolling_update.go),
  called only from the roll paths at :2438, :2994 and the standalone handler at :3777) reports
  `PodRecreationStalled` after `podRecreationOverrun` = 2 min
  ([rolling_update.go:1143](../../internal/controller/rolling_update.go)) and the roll holds.
  Outside a roll the dispatch skips a missing pod rather than reporting it
  ([rolling_update.go:257-261](../../internal/controller/rolling_update.go)). A single
  persistent pod stays down with its PVC; a single non-persistent pod loses its dataset on any
  delete with or without a quota, and the quota only keeps it from coming back.
- **Pod-level resources are dropped silently below 1.34, not refused.** On 1.32 and 1.33 with
  the default gate off, `dropDisabledPodLevelResources`
  ([pkg/api/pod/util.go v1.33.0](https://raw.githubusercontent.com/kubernetes/kubernetes/v1.33.0/pkg/api/pod/util.go)
  lines 728-734, called at :629) sets `podSpec.Resources = nil` unless the old spec already uses
  it, and `DropDisabledTemplateFields` reaches it for StatefulSet templates; the same holds on
  1.34+ with the gate turned off. On 1.29-1.31 the field does not exist and is dropped as an
  unknown field under the default `fieldValidation=Warn`, which answers with a `Warning:`
  response header (kubernetes/website `api-concepts.md` lines 1230-1242). Neither `cmd/` nor
  `internal/` configures a `WarningHandler` (`grep -rn WarningHandler cmd internal` is empty).
- **`writeWorkload` reads back only `hostUsers`.**
  [pod_hardening.go:54-72](../../internal/controller/pod_hardening.go) checks
  `spec.HostUsers` (:56, :66) and reports `errUserNamespacesDropped` (:46); a dropped
  `PodSpec.Resources` would go unreported. `podSpecChanged`
  ([statefulset.go:1269-1300](../../internal/builder/statefulset.go)) and `podHardeningChanged`
  ([pod_security.go:324](../../internal/builder/pod_security.go)) compare no pod-level
  `Resources`, so an out-of-band edit of it would not converge back without a new line, as the
  automount line at [statefulset.go:1273-1279](../../internal/builder/statefulset.go) was added
  for its field.
- **Two wiring costs of a per-container field.** The repair container is inserted after
  `ComputePodSpecHash` ([pod_security.go:236-247](../../internal/builder/pod_security.go)) and
  `dataOwnershipRepairContainer` takes only an image, so a value given to it would sit outside
  the pod-spec hash. `buildPodSpec` has no gocyclo budget left according to its own comments
  ([statefulset.go:208-211](../../internal/builder/statefulset.go),
  [pod_security.go:167-169](../../internal/builder/pod_security.go)), so wiring goes through a
  helper or a post-assembly loop like [sentinel.go:397-402](../../internal/builder/sentinel.go).
- **A single rootless pod is deferred, not deleted, on a release that also moves the sidecar
  image.** `singlePodDeferral`
  ([pod_security_migration.go:128-150](../../internal/controller/pod_security_migration.go))
  returns it as sidecar-pending when `isSidecarOnlyChange`
  ([rolling_update.go:3845-3866](../../internal/controller/rolling_update.go), images only)
  holds, and the whole pending change, a new pod-spec hash included, waits under
  `SidecarUpdatePending` (ADR 0033 lines 446-451). The sidecar image is the operator image
  (`buildSidecarContainer(v, operatorImage)`). This corrects the cost this ticket's analysis
  first gave option D (History).
- **A policy engine that validates the StatefulSet template is a case the LimitRange path does
  not reach.** The Kyverno best-practice policy `require-pod-requests-limits`
  ([policy source](https://raw.githubusercontent.com/kyverno/policies/main/best-practices/require-pod-requests-limits/require-pod-requests-limits.yaml))
  matches `Pod` and requires cpu and memory requests and a memory limit on `containers` and,
  where present, `initContainers`; its sample ships `validationFailureAction: Audit`. Kyverno
  auto-generates such rules for StatefulSets by default
  ([autogen](https://kyverno.io/docs/policy-types/cluster-policy/autogen/)). Under `Enforce`
  it would refuse the data StatefulSet write itself, before any pod exists, so `LimitRanger`
  cannot help, and pod-level resources cannot either, because the pattern asks for container
  values. The operator would report the refused write as `ReconcileBlocked`, phase `Error`
  ([ADR 0002](../adr/0002-surface-a-blocked-reconcile-on-the-cr.md) D3). Read in Kyverno
  source and docs, not measured.
- **The tracked statements that omit the LimitRange path**, each unconditional where the
  outcome depends on the namespace: [compute-resources.md:28](../operations/compute-resources.md)
  ("Data pods under a `ResourceQuota`"), [docs/operations/README.md:21](../operations/README.md)
  (index row), [workload-pod-posture.md:110](../security/workload-pod-posture.md) and H-18 at
  [workload-pod-posture.md:217-224](../security/workload-pod-posture.md), ADR 0033 Context
  (line 168) and Consequences (lines 452-453), and CLAUDE.md line 1007. They are false by
  source reading, not measured-false. The code comments at
  [sentinel.go:394](../../internal/builder/sentinel.go) and
  [valkey_types.go:509](../../api/v1/valkey_types.go) state the per-container rule and stay true.
- Neither ClusterRole grants `limitranges`
  (`deploy/helm/valkey-operator/templates/clusterrole.yaml`, `config/rbac/role.yaml`; the
  `git grep` above is empty). Nothing outside `docs/tickets/` cites this ticket
  (`git grep -n 'T52\|052-the-sidecar' -- . ':!docs/tickets'` is empty).

**Not verified:**

- The sidecar's memory and CPU under a drain promotion; nobody has measured them. What would
  settle it: a Kind drain run with metrics.
- ~~What an API server below 1.34 does with pod-level resources (drops or refuses the field).~~
  *(corrected 2026-09-27 at 84a39c2: settled by reading upstream source, see Verified - dropped,
  not refused; on 1.29-1.31 with a response warning, on 1.32-1.33 and with the gate off
  without one. Not measured.)*
- ~~*(added 2026-09-27)* The feature-gate default of `PodLevelResources` per Kubernetes version
  (the `k8s.io/api` doc comment and ADR 0033 disagree, above). Also whether a silent drop would
  need the same read-back that `writeWorkload`
  ([`pod_hardening.go`](../../internal/controller/pod_hardening.go) line 54) does for
  `hostUsers`.~~ *(corrected 2026-09-27 at 84a39c2: both settled, see Verified - Beta
  default-on since 1.34, the doc comment is stale; option C needs its own read-back, because
  `writeWorkload` checks only `hostUsers`.)*
- *(added 2026-09-27)* The whole LimitRange admission path end to end: no run of this
  repository has put a Valkey CR into a namespace with a `ResourceQuota` and a `LimitRange`.
  What would settle it: option V3 or V2 of D2.
- *(added 2026-09-27)* Whether any production namespace (gitlab, gpt, harbor, iam,
  database-examples) carries a cpu/memory `ResourceQuota`, a `LimitRange`, or a Kyverno or
  Gatekeeper policy requiring container resources. What would settle it: the owner's
  `kubectl get resourcequota,limitrange -A` and a look at the cluster's policy engine; this run
  may not touch the cluster.
- *(added 2026-09-27)* What the Valkey CR reports on a fresh CR whose data pods are refused,
  and on an existing tier that loses a pod outside a roll. What would settle it: a Kind run.
- *(added 2026-09-27)* Whether controller-runtime's default logs the API server's `Warning:`
  headers (relevant only to option C on 1.29-1.31); its default was not read.
- *(added 2026-09-27)* Whether envtest's kube-apiserver 1.29 enables the `LimitRanger` and
  `ResourceQuota` admission plugins, and whether quota admission refuses while `status.hard`
  is unset (envtest runs no controller-manager to populate it). Relevant only to option V3.
- *(added 2026-09-27)* Which process the OOM killer picks when a pod-level memory limit is
  reached (relevant only to option C); inferred, not measured.

## Impact

~~Every namespace with a cpu/memory `ResourceQuota`: the data StatefulSet is written, and its
pods are refused at admission. What an operator can do today is set every field that exists —
`spec.resources`, `spec.metrics.resources`, `spec.observer.resources`,
`spec.sentinel.resources` — which admits the Sentinel and observer pods, and host the data tier
in a namespace without such a quota.~~ *(corrected 2026-09-27 at 84a39c2:)* Every namespace
with a cpu/memory `ResourceQuota` **and no LimitRange defaults for the quota's keys**. A fresh
CR there gets its data StatefulSet written and no data pod, each refused at admission
(`FailedCreate` on the StatefulSet). An existing tier in a namespace that gains such a quota
keeps its running pods and loses each one that is replaced — a roll holds at the first
deleted pod with `PodRecreationStalled`, a pod lost outside a roll simply stays missing, and a
single pod stays down (Fact).

What an operator can do today, with no operator change: add a namespace `LimitRange` with
cpu/memory `defaultRequest` for the keys the quota tracks (and `default` limits where it tracks
`limits.*`), which admits the data pods under the unchanged quota; and set every existing field
explicitly — `spec.resources`, `spec.metrics.resources`, `spec.observer.resources`,
`spec.sentinel.resources` — with requests, and with limits where the quota tracks `limits.*`,
stating every key the LimitRange defaults, so that the namespace default does not reach
`valkey-server` or the other containers that have a field. Read in upstream source, not
measured. A namespace whose policy engine validates the StatefulSet template for container
resources (Fact, Kyverno) is not served by that path and today refuses the data StatefulSet.

The threat line, per case: no hostile principal. The quota refusal fails closed, so no tenancy
guarantee is weakened. What set values would add is a bound on the CPU and memory the sidecar
and the init containers take on their node; with no LimitRange default and no field, nothing
the operator writes bounds them.

## Options

Two decisions, presented one at a time.

### D1 - does the operator gain a way to state resources for the sidecar and the data init containers?

**Mechanism.** A data pod has these containers today: `valkey` takes `spec.resources`
([statefulset.go:871](../../internal/builder/statefulset.go)); the sidecar takes nothing
([statefulset.go:894](../../internal/builder/statefulset.go)); the optional exporter takes
`spec.metrics.resources` ([statefulset.go:1124-1125](../../internal/builder/statefulset.go));
`check-data-writable` ([pod_security.go:176](../../internal/builder/pod_security.go), persistent
pods only), `init-config-selector` ([statefulset.go:275](../../internal/builder/statefulset.go)
with Sentinel, [statefulset.go:436](../../internal/builder/statefulset.go) multi-replica
without) and the migration-only `fix-data-ownership`
([pod_security.go:213](../../internal/builder/pod_security.go), inserted after the hash) state
nothing. In the API server, `LimitRanger` (mutating) fills a namespace's defaults into every
container and init container lacking a key at pod creation, then `ResourceQuota` (validating)
refuses a pod unless every container and init container states each quota-tracked cpu/memory
key, and skips that check for a pod with pod-level resources while `PodLevelResources` is on.
So a quota namespace **with** LimitRange defaults admits the data pods today, and the operator
does not fight the defaulted pods (hash-first comparison, Fact); a quota namespace **without**
them refuses every new data pod.

**What the choice changes:** whether the CRD gets a per-CR way to state those values, either
per-container fields (B) or pod-level resources (C). B overturns ADR 0033 Alternatives lines
484-485; C weighs the residual risk ADR 0033 lines 711-713 left open. **What it does not
change:** the pods of CRs that leave a new field unset (the PodSpec JSON and hash stay
identical, [statefulset.go:1228-1239](../../internal/builder/statefulset.go), so nothing rolls);
the LimitRange path, which works under every option; the Sentinel, observer and exporter fields.

- **A - keep ADR 0033 D7; a quota namespace uses a LimitRange, and the documentation says how
  (recommended).** No field, no default. The operations page, H-18 and ADR 0033 gain the path
  and its caveats: a `defaultRequest`-only LimitRange (no `default`, no `max`) meets a quota on
  `cpu`, `memory` or `requests.*` without putting any limit on the sidecar; a quota on
  `limits.*` needs `default` limits, which reach the sidecar and the init containers; default
  exactly the keys the quota tracks and nothing more; state every key the LimitRange defaults in
  `spec.resources` and the other existing fields; a default limit below a request a container
  sets makes the pod invalid; the default must act on Pods (a LimitRange does), because a policy
  mutating the StatefulSet template is reverted every pass. At close, ADR 0033 records B and C
  as weighed and why they lost. *Cost:* S, documentation in the places listed in Fact plus the
  ADR amendment. *Consequences:* the default is namespace-wide, but when it defaults exactly the
  keys the quota tracks it changes no pod the quota admits today — every admitted pod already
  states those keys in every container (`Constraints`), and the per-key merge sets only a
  missing key — so it only fills pods that are refused today, which makes it safe in a shared
  namespace such as gitlab or harbor. That holds for an unscoped quota only: a quota with
  `scopes` or a `scopeSelector` constrains only the pods it matches (`podEvaluator.Matches`,
  pods.go v1.36.4 lines 203-205, `podMatchesScopeFunc` line 350), and a pod with pod-level
  resources skips `Constraints`, so either kind of pod can receive a new default; such pods
  need the LimitRange keys checked against their own values. Setting `spec.resources` on an
  existing CR to state the defaulted keys changes the pod-spec hash and rolls that tier, like any
  CR change of it, and on a single non-persistent pod that replacement loses the dataset. A
  `limits.*` quota forces the namespace administrator to
  pick a memory limit for the sidecar whose peak nobody has measured. A tenant without
  LimitRange rights depends on whoever set the quota; in the Flux fleet a LimitRange is an
  ordinary object in the namespace Kustomization, not a hand-set CR annotation, so the path is
  executable there. It does not serve a namespace whose policy engine validates the StatefulSet
  template.
- **B - explicit resources fields for the sidecar and the data init containers, no default.**
  Two optional fields (names to be decided); unset means no requests and no limits, as today,
  and changes no hash ([ADR 0005](../adr/0005-upgrade-neutral-defaults-and-anti-affinity.md) D1).
  Wired through a helper or a post-assembly loop (no gocyclo budget in `buildPodSpec`), and the
  repair container's value would sit outside the pod-spec hash (Fact). *Cost:* M - CRD fields,
  wiring, `make generate-all`, the README CRD reference, a unit test pinning an identical
  PodSpec and hash while unset, a quota e2e with its D11 positive control, and the amendment of
  ADR 0033 D7 and its Alternatives lines 484-485, reopening a rejection recorded on 2026-09-26.
  *Consequences:* per-CR values without a namespace-wide policy object, on the v1.29 floor, and
  the only option that serves a namespace whose policy engine validates the StatefulSet template
  for container values. It does not remove the guess about the sidecar's peak; it moves it from
  the maintainers to the CR author, who is less able to measure it, and under a `limits.*`
  quota or a policy requiring a memory limit the author must still set the sidecar limit D7
  refuses to guess. Two more fields to maintain, and every future data-pod container needs a
  decision about which field reaches it.
- **C - an opt-in pod-level resources field for the data pod (`PodSpec.Resources`).** One field;
  a pod carrying it is exempt from the per-container quota check. It does not need a floor bump:
  ADR 0033 D3 already ships `userNamespaces` above the v1.29 floor with a read-back, and C
  follows that precedent. *Cost:* M - the field, the PodSpec wiring, a `podSpecChanged` line, a
  `writeWorkload` read-back with its own `ReconcileBlocked` reason and tests (the field is
  dropped silently below 1.34 or with the gate off), a quota e2e that can run only on Kind 1.34
  or later, and the ADR 0033 amendment. *Consequences:* it builds on an API that is still Beta
  in v1.37.1; a pod-level memory limit makes `valkey-server` and the sidecar share one budget,
  the opposite of isolating the sidecar (which process the OOM killer picks is not verified); a
  policy engine requiring container values refuses it too, so it serves only the quota case A
  already serves; it interacts with ~~[T36](036-non-persistent-master-restarts-empty.md)'s option
  "`maxmemory` derived from the memory limit" if both are chosen~~ *(corrected 2026-09-27,
  consistency pass: 036's re-verification removed that option; whether and how the operator sets
  `maxmemory` is decided in
  [T71](071-maxmemory-is-never-set-so-an-oom-kill-is-the-only-memory-bound.md), filed the same
  day. The interaction stays for T71's option B, a fraction of `spec.resources.limits.memory`,
  which would have to read a pod-level memory limit too if C were chosen; T71's recommended
  option A, an absolute `spec.maxMemory`, reads no limit, and its optional CEL rule cannot see a
  pod-level limit)*.

**A is recommended**, for two checkable reasons. First, a LimitRange admits the data pods under
an unchanged cpu/memory quota on every supported Kubernetes version (LimitRanger is
default-enabled, mutating, runs before the validating quota plugin and defaults init containers
too, admission.go v1.36.4 lines 216-292), and the operator does not treat a defaulted pod as
outdated ([rolling_update.go:515-525](../../internal/controller/rolling_update.go),
[rolling_update.go:4838-4861](../../internal/controller/rolling_update.go)) - wherever someone
with LimitRange rights adds the default. Second, a `defaultRequest`-only LimitRange meets a
request quota with no limit (`SetDefaults_LimitRangeItem` derives none), so D7's OOM concern
does not arise. A beats the runner-up B because B adds two CRD fields, reopens a rejection
recorded one day earlier and costs M, while delivering the same unmeasured sidecar value per CR
instead of per namespace. **B's trigger** is a named tenant whose namespace carries a
cpu/memory quota without LimitRange defaults they can get added, or whose admission policy (for
example Kyverno `require-pod-requests-limits` with autogen, under `Enforce`) validates the
StatefulSet template; no such tenant or policy is named, and whether the production cluster has
either is not verified. C ranks third: it depends on a Beta API, needs the silent-drop read-back
and a new blocked reason, and serves no case A leaves open.

### D2 - how is the documented LimitRange path verified?

**Mechanism.** The statement that a LimitRange admits the data pods rests on upstream source
(`LimitRanger`, the quota `podEvaluator` and `SetDefaults_LimitRangeItem`, v1.36.4) and on the
operator's hash-first pod comparison. Nothing in this repository has measured it, and the
operator-side half is not pinned by a test (Fact:
`TestPodNeedsUpdate_PodSpecHashMatch_NoUpdate` passes `nil` desired containers). ADR 0033 D8
already documents upstream kubelet behaviour as "read in upstream source, not measured" (ADR
0033 line 654). **What the choice changes:** whether the documentation marks the path
unmeasured or cites a test that measures admission. **What it does not change:** the operator's
code, and the unit test that pins the operator-side half, which is work under every option
(Work list).

- **V1 - document it as read in upstream source v1.36.4, not measured (recommended).** The text
  names the source files and the tag. *Cost:* XS. *Consequences:* nobody has run a quota plus
  LimitRange namespace against the operator; a surprise, such as a platform policy that mutates
  StatefulSet templates, would show first in production.
- **V3 - an integration-tier admission test.** In `test/integration` (envtest, a real
  kube-apiserver, no kubelet), create or server-side dry-run a Pod from
  `BuildStatefulSet(...).Spec.Template` in a namespace with a cpu/memory `ResourceQuota` and a
  `LimitRange` with `defaultRequest`, and assert it is admitted with the defaults on the sidecar
  and the init containers; a second namespace without the LimitRange, where the Pod is refused,
  is the D11 positive control ([ADR 0017](../adr/0017-test-and-ci-policy.md) D11). It measures
  admission in the component that enforces it, the tier CLAUDE.md assigns to what only a real
  API server decides, as [pod_security_test.go:29-60](../../test/integration/pod_security_test.go)
  already round-trips the built template. *Cost:* S, plus the two open questions in Not
  verified (admission plugins of envtest 1.29, quota `status.hard`), either of which may force
  the test to write quota status itself. *Consequences:* the documentation statement becomes a
  measurement of admission; the StatefulSet controller and a live roll stay unmeasured.
- **V2 - an e2e.** A quota plus LimitRange namespace in which the data and the Sentinel tier
  reach `OK` and roll, with a control namespace without the LimitRange in which the pods are
  refused (`FailedCreate`), on both single-node CI legs. *Cost:* S-M plus suite time on two
  legs. *Consequences:* measures the whole path including the StatefulSet controller and a roll;
  it is the natural carrier once B is chosen, whose Verification already needs a quota e2e.

**V1 is recommended**, because it follows the repository's precedent for upstream behaviour
(ADR 0033 D8) and the operator-side risk - a defaulted pod judged outdated - is closed by the
unit test in the Work list, not by the verification level. V1 beats V3 because V3 costs S for a
low-severity documentation statement with no named user and carries two unverified envtest
preconditions; V3 is the runner-up over V2 because it measures the admission half at a
fraction of the cost and without CI suite time. V3 or V2 wins as soon as a production quota
namespace exists or B is chosen.

## Work list

~~**Not waiting on a decision:** nothing. The one open question, whether a concrete
quota-namespace tenant exists, *is* the decision.~~ *(corrected 2026-09-27 at 84a39c2: the
LimitRange path is upstream behaviour that holds under every option, so its documentation and
the pinning test need no decision.)*

**Not waiting on a decision** (outside `docs/tickets/`, a later change):

1. Document the LimitRange path and its caveats (D1 option A text), marked "read in upstream
   source v1.36.4, not measured" unless D2 chooses otherwise, in
   [compute-resources.md:26-33](../operations/compute-resources.md), the index row
   [docs/operations/README.md:21](../operations/README.md),
   [workload-pod-posture.md:110](../security/workload-pod-posture.md) and H-18
   ([workload-pod-posture.md:217-224](../security/workload-pod-posture.md)), ADR 0033 Context
   line 168 and Consequences lines 452-453, and CLAUDE.md line 1007. The Sentinel comment at
   [sentinel.go:394](../../internal/builder/sentinel.go) and
   [valkey_types.go:509](../../api/v1/valkey_types.go) stay as they are.
2. A unit test for both comparisons (`podOutdated` / `podSpecHashChanged` and
   `sentinelPodNeedsUpdate`): a pod whose pod-spec hash matches and whose containers and init
   containers carry requests and limits the desired template does not have is not outdated,
   with the real desired containers passed, and a mutation check under ADR 0017 (compare
   resources after a hash match; the test must go red). XS.

**Waiting on D1 = B** (only if the trigger above is met):

1. The fields (names to be decided) in `api/v1/valkey_types.go`.
2. The wiring at [statefulset.go:894](../../internal/builder/statefulset.go) (sidecar),
   [statefulset.go:275](../../internal/builder/statefulset.go) and
   [statefulset.go:436](../../internal/builder/statefulset.go) (`init-config-selector`), and
   [pod_security.go:176](../../internal/builder/pod_security.go) and
   [pod_security.go:213](../../internal/builder/pod_security.go) (`check-data-writable`,
   `fix-data-ownership`), through a helper or a post-assembly loop; decide whether the repair
   container, which sits outside the pod-spec hash, takes the value at all.
3. A unit test that pins an unchanged pod template and hash while the fields are unset.
4. `make generate-all` and the README CRD reference.
5. The quota e2e below.

**Close (ADR 0034):** under A, amend ADR 0033 (D7 stands; record B and C as weighed with the
LimitRange path as the reason they lost; correct Context line 168 and Consequences lines
452-453). Under B, amend ADR 0033 D7 and mark the Alternatives entry at lines 484-485
superseded in place. Then H-18 and [compute resources](../operations/compute-resources.md).
`git grep -n 'T52\|052-the-sidecar'` outside `docs/tickets/` (none today), then move to
`archive/`.

## Decision

None yet.

## Verification

- Under A: the unit test of the Work list goes red under the mutation "compare resources after a
  hash match" and green without it, for the data and the Sentinel comparison; and the D2 choice
  (V1: the documentation cites the upstream files and tag; V3 or V2: the test named there).
- Under B: e2e in a namespace with a cpu/memory `ResourceQuota`: with the new fields and the
  existing ones set, the data pods are admitted and the cluster reaches `OK`; with the new
  fields unset, the data pods are refused ~~(the negative control of
  [ADR 0017](../adr/0017-test-and-ci-policy.md) D11)~~ *(corrected 2026-09-27 at 84a39c2: the
  D11 positive control that the quota enforces at all, [ADR 0017](../adr/0017-test-and-ci-policy.md)
  D11 at line 373, as the repository names that leg at line 900)*. A unit test pins that an
  unset field yields a pod template identical to today's.

## History

- 2026-09-27 — re-verified at 84a39c2. Checked every code citation (all hold; locations re-read
  at 84a39c2, the `valkey_types.go` field lines moved +2 to 512, 673, 975, 1074 through
  `bcc63c9`), ADR 0033, ADR 0017 and the upstream admission path in Kubernetes v1.36.4 and
  v1.33.0 source, Kubernetes docs and the Kyverno policy and docs. **Found false:** the central
  premise that a cpu/memory quota namespace cannot host the data tier unless the quota is
  loosened or D7 is reopened — the default-enabled, mutating `LimitRanger` fills a namespace
  LimitRange's defaults into every container and init container before the validating quota
  check, and the operator does not fight the defaulted pods (hash-first comparison); corrected
  in the title, severity comment, threat line, Impact and the Option texts. Also false: option
  B's "nothing is guessed" (the CR author guesses the unmeasured sidecar peak instead), option
  C's floor premise (ADR 0033 D3's read-back precedent ships above the floor), the Work list's
  "nothing is independent of the decision", and the Verification label "negative control of
  ADR 0017 D11" (it is the D11 positive control). **Found incomplete:** the existing-tier impact
  (every pod replacement, not only a roll), the observer's request-only default under a
  `limits.*` quota, the per-key defaulting (a LimitRange limit reaches `valkey-server` unless
  `spec.resources` states that key), `max` becoming a default limit, and the list of tracked
  statements omitting the path (CLAUDE.md line 1007, docs/operations/README.md line 21 and ADR
  0033 line 168 added). **Settled from Not verified:** `PodLevelResources` is Beta default-on
  since 1.34 and still Beta in v1.37.1, the `k8s.io/api` alpha comment is stale; below 1.34 the
  field is dropped, not refused; `writeWorkload` reads back only `hostUsers`. **Found not
  pinned:** `TestPodNeedsUpdate_PodSpecHashMatch_NoUpdate` passes `nil` desired containers and
  does not pin that a defaulted pod is not outdated; a unit test is added to the Work list.
  Nothing was measured; no container was started, because the ticket makes no Valkey behaviour
  claim. **Options:** rewritten as two decisions. D1 keeps A (recommended), B (runner-up, its
  trigger now named precisely: a tenant whose quota comes without LimitRange defaults they can
  get added, or a policy engine validating the StatefulSet template) and C (third). Removed:
  **D** (measured defaults for the sidecar and init containers) - rejected by ADR 0033 lines
  481-483, rolls every multi-replica data tier on the upgrade against ADR 0005 D1, and its
  requests-only variant cannot meet a `limits.*` quota; its earlier stated cost "a single
  non-persistent pod loses its dataset" was false for a release, because `singlePodDeferral`
  defers a rootless single pod when the sidecar image moves. **E** (mirror `spec.resources` onto
  the data init containers) - cannot admit the pod, the sidecar still states nothing, and it
  rolls every tier that sets `spec.resources`. Weighed and not added: **F** (the operator or the
  chart creates a LimitRange per CR namespace) - it would change the defaults of foreign
  workloads in shared namespaces, needs a `limitranges` write grant neither ClusterRole has
  (ADR 0013), acts on pods the operator does not own (ADR 0020) and chooses the sidecar limit D7
  refuses to guess, disproportionate for a low gap. D2 is new: V1 (recommended), V3 (envtest
  admission test, runner-up), V2 (e2e). **Recommendation:** still A, but its justification
  changed from "no known user" to the LimitRange path, which serves even a named quota user.
  **Frontmatter:** `state` filed → analysed (facts, mechanism and options verified by reading,
  the unmeasured rest listed with what settles it); `urgency` icebox → later (rule 4, first
  match: the documentation is a cheap known fix with no decision; rule 5 is never reached);
  `effort` M → S for the recommended course (B alone stays M); `security` kept at `hardening`
  with a rewritten threat line — the auditor of this run proposed `none` because the quota fails
  closed, the design review kept `hardening` because H-18 is tracked on a security page and the
  gap is resource isolation; disputed, the owner may reclassify; `severity` stays low with its
  comment corrected; `title` narrowed to quotas without LimitRange defaults. **Review pass, same
  day:** upstream and code line citations re-read and moved where they had drifted (admission.go,
  pods.go, pod_hardening.go, the gocyclo comments, compute-resources.md); added that a legacy pod
  without the pod-spec-hash annotation is replaced once after defaulting, that the Sentinel test
  `TestSentinelPodNeedsUpdate_PodSpecHashMatch_NoUpdate` has the same gap as the data one (the
  first text said no Sentinel test existed), and two caveats to option A: its "changes no
  admitted pod" argument holds for an unscoped quota only, and stating the defaulted keys in
  `spec.resources` on an existing CR rolls that tier.
  Cross-ticket: in the consistency pass of the same day, option C's citation of T36's `maxmemory`
  option was corrected in place (036 removed it; the question lives in 036's Fact bullet until its
  own ticket is filed).
  Filed: the `maxmemory` question that option C's note parked in T36 is now
  [T71](071-maxmemory-is-never-set-so-an-oom-kill-is-the-only-memory-bound.md) (medium, security
  none, effort M, analysed); option C's note points there and names the T71 option the
  interaction concerns (B, a fraction of the memory limit). No frontmatter field, option or
  recommendation of this ticket rested on it, so none changed.
- 2026-09-27 — enriched - corrected the `init-config-selector` lines, and recorded that ADR 0033
  Alternatives (lines 484–485) already rejected B for "no known user". Added the pod-spec-hash
  fact (unset rolls nothing) and option E (mirror), and moved the recommendation from B to A
  until a quota user is named.
- 2026-09-27 — effort `S` → `M`. Two fields and their wiring are S. The Verification this
  ticket already required (a ResourceQuota e2e with a negative control), CRD regeneration and
  the ADR 0033 amendment make it M. Urgency unchanged (`icebox`, rule 5).
- 2026-09-27 — filed from the row "Sidecar / init container requests" of archive/031. Gap
  [H-18](../security/workload-pod-posture.md#h-18) states what is missing and what to set in the
  meantime.
