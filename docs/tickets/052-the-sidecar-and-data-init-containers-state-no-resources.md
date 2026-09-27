---
id: T52
title: the sidecar and the data pod's init containers state no resources, so a cpu/memory ResourceQuota without LimitRange defaults refuses the data pods
state: analysed       # code and upstream admission path verified by reading; nothing measured, nothing decided
severity: low         # without LimitRange defaults a cpu/memory quota refuses every new data pod; nothing else breaks
security: hardening   # the quota fails closed, but the gap is resource isolation, tracked as H-18
threat: "would additionally bound, once values are set (a namespace LimitRange today, a field under option B), the CPU and memory the data pod's sidecar and init containers can take from co-located pods on their node; today nothing the operator writes bounds them"
urgency: later        # rule 4: documenting the LimitRange path is a cheap known fix that needs no decision
effort: S             # recommended course: documentation, the ADR 0033 amendment, one unit test; option B alone is M
blocked-by: decision  # Q1 and Q2; the documentation and the unit test are not blocked
filed-from: T31, section "Further security measures — not in this change, each open" (archive/031)
opened: 2026-09-27
decided:
done:
---

# T52 - the sidecar and the data pod's init containers state no resources

## Current state

Resources in a data pod ([statefulset.go](../../internal/builder/statefulset.go),
[pod_security.go](../../internal/builder/pod_security.go)):

| Container | Resources |
|---|---|
| `valkey` | `spec.resources` ([statefulset.go:871](../../internal/builder/statefulset.go)) |
| exporter (optional) | `spec.metrics.resources` ([statefulset.go:1124-1125](../../internal/builder/statefulset.go)) |
| sidecar, `buildSidecarContainer` ([statefulset.go:894](../../internal/builder/statefulset.go)) | none |
| `init-config-selector` ([statefulset.go:275](../../internal/builder/statefulset.go) with Sentinel, [statefulset.go:436](../../internal/builder/statefulset.go) multi-replica without) | none |
| `check-data-writable` ([pod_security.go:176](../../internal/builder/pod_security.go), persistent pods only) | none |
| `fix-data-ownership` ([pod_security.go:213](../../internal/builder/pod_security.go), migration only, inserted after `ComputePodSpecHash` by `WithDataOwnershipRepair`, [pod_security.go:247](../../internal/builder/pod_security.go)) | none |

This is deliberate:
[ADR 0033](../adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md)
D7 sets no default, because a limit guessed too low OOM-kills the process that holds the drain
promotion ([ADR 0012](../adr/0012-the-sidecar-records-its-drain-promotion-on-the-pod.md)). ADR
0033 *Alternatives Considered* rejects per-container fields for the sidecar and init containers
("More API surface with no known user", lines 484-485) and measured defaults (lines 481-483);
pod-level resources are recorded as "not weighed in D7" (lines 711-713). The operator-facing
statement is gap [H-18](../security/workload-pod-posture.md#h-18).

Admission, read in Kubernetes v1.36.4 source, not measured:

- `ResourceQuota` (validating, `podEvaluator.Constraints` in `pkg/quota/v1/evaluator/core/pods.go`)
  refuses a pod unless every container and init container states each quota-tracked cpu/memory
  key. A pod with pod-level `spec.resources` skips that check while `PodLevelResources` is on
  (Beta, default-on since 1.34, still Beta in 1.37.1; the `k8s.io/api` doc comment calling it
  alpha is stale).
- `LimitRanger` (default-enabled, mutating, runs before validating admission) fills a namespace
  LimitRange's `defaultRequest` and `default` into every container **and init container** that
  lacks the key, per key. So a quota namespace **with** LimitRange defaults admits the data pods
  today; one **without** refuses every new data pod.
- A LimitRange with `defaultRequest` only (no `default`, no `max`) adds requests and no limit,
  which satisfies a quota on `cpu`, `memory` or `requests.*`. A quota on `limits.*` needs
  `default` limits, which then reach the sidecar and the init containers. A `max` alone becomes a
  default limit. A default limit below a request a container sets makes the pod invalid.
- A LimitRange default reaches every container lacking the key: the exporter when
  `spec.metrics.resources` is unset, every Sentinel container when `spec.sentinel.resources` is
  unset, and the observer's limits (its default is a 50m/64Mi request and no limit,
  `GetObserverResources`, [valkey_types.go:1408-1418](../../api/v1/valkey_types.go)).
- The operator does not fight a defaulted pod. `LimitRanger` acts on Pods only, never on the
  StatefulSet template. `podSpecHashChanged`
  ([rolling_update.go:515-525](../../internal/controller/rolling_update.go)) and
  `sentinelPodNeedsUpdate` ([rolling_update.go:4838-4861](../../internal/controller/rolling_update.go))
  decide on the pod-spec-hash annotation first; only a legacy pod without it is compared by
  resources and is replaced once. A policy that mutated the StatefulSet template instead would be
  reverted every pass by `containerChanged` ([statefulset.go:1348-1350](../../internal/builder/statefulset.go)).
- That operator-side half is not pinned by a test: `TestPodNeedsUpdate_PodSpecHashMatch_NoUpdate`
  ([rolling_update_test.go:225-239](../../internal/controller/rolling_update_test.go)) passes
  `nil` desired containers, and `TestSentinelPodNeedsUpdate_PodSpecHashMatch_NoUpdate`
  ([rolling_update_test.go:2126-2148](../../internal/controller/rolling_update_test.go)) uses
  identical containers without resources.
- A policy engine that validates the StatefulSet template for container resources (for example
  Kyverno `require-pod-requests-limits` with autogen under `Enforce`) refuses the data
  StatefulSet write itself; neither a LimitRange nor pod-level resources help there. The
  operator reports it as `ReconcileBlocked`, phase `Error`. Read in Kyverno docs, not measured.

No tracked file mentions LimitRange. These statements present the refusal as unconditional:
[compute-resources.md:26-33](../operations/compute-resources.md),
[docs/operations/README.md:21](../operations/README.md),
[workload-pod-posture.md:110](../security/workload-pod-posture.md), H-18
([workload-pod-posture.md:217-224](../security/workload-pod-posture.md)), ADR 0033 Context
(line 168) and Consequences (lines 452-453), CLAUDE.md line 1007. The comments at
[sentinel.go:394](../../internal/builder/sentinel.go) and
[valkey_types.go:509](../../api/v1/valkey_types.go) are correct.

**Impact.** Every namespace with a cpu/memory `ResourceQuota` and no LimitRange defaults for the
quota's keys:

- A fresh CR gets its data StatefulSet and no data pod (`FailedCreate`).
- An existing tier keeps its running pods (quota is enforced at admission only) and loses each
  replaced pod: a roll holds with `PodRecreationStalled` after 2 min, a pod lost outside a roll
  (eviction, drain, chaos kill) stays missing, a single pod stays down.

Workaround with no operator change: a namespace LimitRange with `defaultRequest` for the quota's
keys (`default` limits for `limits.*`), and every existing field (`spec.resources`,
`spec.metrics.resources`, `spec.observer.resources`, `spec.sentinel.resources`) set explicitly for
every key the LimitRange defaults, so the namespace default does not reach `valkey-server`. No
hostile principal is involved; the refusal fails closed.

## Required changes

### Independent of the open questions

1. Document the LimitRange path and its caveats (see Q1 option A) in every statement listed
   above, marked as Q2 decides (default: "read in upstream source v1.36.4, not measured").
2. Unit test for `podOutdated`/`podSpecHashChanged` and `sentinelPodNeedsUpdate`: a pod whose
   pod-spec hash matches and whose containers and init containers carry requests and limits the
   desired template lacks is not outdated, with the real desired containers passed. Mutation
   check (ADR 0017): comparing resources after a hash match must turn it red.

### Depends on the answers

- **Q1 = A:** amend ADR 0033: D7 stands, B and C recorded as weighed and lost to the LimitRange
  path; correct Context line 168 and Consequences lines 452-453.
- **Q1 = B:**
  1. Two optional fields in `api/v1/valkey_types.go` (names to be decided), no default.
  2. Wire them to the sidecar and the init containers listed above through a helper or a
     post-assembly loop like [sentinel.go:397-402](../../internal/builder/sentinel.go)
     (`buildPodSpec` has no gocyclo budget left); decide whether `fix-data-ownership`, which sits
     outside the pod-spec hash, takes the value at all.
  3. Unit test: unset fields yield an identical PodSpec and hash
     ([statefulset.go:1228-1239](../../internal/builder/statefulset.go)).
  4. `make generate-all`, README CRD reference.
  5. E2E in a quota namespace: fields set, data pods admitted and cluster `OK`; fields unset,
     pods refused (the ADR 0017 D11 positive control).
  6. Amend ADR 0033 D7 and mark the Alternatives entry at lines 484-485 superseded in place.
- **Q2 = V3 or V2:** the test described there.

## Open questions

### Q1: Does the operator gain a way to state resources for the sidecar and the data init containers?

A LimitRange already admits the data pods under an unchanged quota, and a new field left unset
changes no pod-spec hash, so nothing rolls under any option. The question is whether a per-CR
way is worth reopening ADR 0033 D7.

- **A - keep ADR 0033 D7, document the LimitRange path (recommended).** No field. Cost S.
  Safe in a shared namespace for an unscoped quota (it only fills pods refused today); a scoped
  quota or pods with pod-level resources need their values checked. A `limits.*` quota forces
  the namespace administrator to pick an unmeasured sidecar memory limit. Does not serve a
  policy engine that validates the StatefulSet template.
- **B - optional fields for the sidecar and the data init containers, no default.** Cost M.
  Per-CR values on the v1.29 floor; the only option that serves a template-validating policy
  engine. Overturns a recorded ADR rejection, moves the sidecar guess to the CR author, and
  every future data-pod container needs a field decision.
- **C - opt-in pod-level resources (`PodSpec.Resources`) on the data pod.** Cost M. Depends on
  a Beta API, is dropped silently below 1.34 so it needs a `writeWorkload` read-back
  ([pod_hardening.go:54-72](../../internal/controller/pod_hardening.go) reads only `hostUsers`)
  with its own `ReconcileBlocked` reason and a `podSpecChanged` line; a pod-level memory limit
  makes `valkey-server` and the sidecar share one budget. Serves no case A leaves open.

A is recommended: a LimitRange admits the pods on every supported version and a
`defaultRequest`-only one meets a request quota without any limit, so D7's OOM concern does not
arise. B becomes right once a tenant is named whose quota comes without LimitRange defaults they
can get added, or whose policy engine validates the StatefulSet template.

**Answer:** _open_

### Q2: How is the documented LimitRange path verified?

The statement rests on upstream source reading; the operator-side half is covered by the unit
test above under every option.

- **V1 - document as "read in upstream source v1.36.4, not measured" (recommended).** Cost XS;
  a surprise would first show in production.
- **V3 - integration test (envtest).** Create or dry-run a Pod from the built template in a
  namespace with a quota and a `defaultRequest` LimitRange, assert admission with defaults;
  a namespace without the LimitRange refuses it (D11 positive control). Cost S, with two
  envtest preconditions unverified (see Not verified).
- **V2 - e2e.** Quota plus LimitRange namespace in which data and Sentinel tiers reach `OK` and
  roll, plus a control namespace, on both single-node legs. Cost S-M plus suite time; the natural
  carrier if Q1 = B.

V1 is recommended: it follows ADR 0033 D8's precedent for upstream behaviour, and the real
operator-side risk is closed by the unit test. V3 or V2 wins once a production quota namespace
exists or Q1 = B.

**Answer:** _open_

## Not verified

- The sidecar's CPU and memory peak under a drain promotion; a Kind drain run with metrics settles it.
- The LimitRange admission path end to end against the operator; Q2 option V3 or V2 settles it.
- Whether a production namespace (gitlab, gpt, harbor, iam, database-examples) carries a
  cpu/memory `ResourceQuota`, a `LimitRange` or a policy requiring container resources;
  `kubectl get resourcequota,limitrange -A` and a look at the policy engine settle it.
- What the CR reports when a fresh CR's data pods are refused, or a pod is lost outside a roll; a Kind run settles it.
- For V3 only: whether envtest 1.29 enables `LimitRanger` and `ResourceQuota`, and whether quota
  admission refuses while `status.hard` is unset.
- For C only: which process the OOM killer picks at a pod-level memory limit, and whether
  controller-runtime logs the API server's `Warning:` headers on 1.29-1.31.

## Related

- [T71](071-maxmemory-is-never-set-so-an-oom-kill-is-the-only-memory-bound.md) - its option B
  (a fraction of the memory limit) would also have to read a pod-level limit if Q1 = C.
