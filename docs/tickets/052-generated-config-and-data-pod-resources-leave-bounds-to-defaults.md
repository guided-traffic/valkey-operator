---
id: T52
title: the generated valkey config and the data pod's resources leave the memory bound, the protected commands and the small containers to defaults
state: analysed       # every part: code read, the Valkey side measured in docker on both pins, nothing run on Kubernetes, nothing decided
severity: medium      # the maxmemory part; the other two parts are low
security: hardening   # the switches and the container resources; the maxmemory part alone is none
threat: "should the valkey-server in spec.image ever default a protected-action switch to yes, any client reaching the data port could additionally run DEBUG, MODULE LOAD or CONFIG SET on a protected config; and nothing the operator writes bounds the CPU and memory the data pod's sidecar and init containers can take from co-located pods on their node"
urgency: next         # driven by the maxmemory part (severity medium, trigger live in released code); the other parts alone are later
effort: M             # recommended course: maxmemory field (M), pin assertion (XS), LimitRange docs and one unit test (S); Q5 = B adds M
blocked-by: decision  # Q1-Q6; the documentation, the Kind headroom measurement and the pod-hash unit test are not blocked
filed-from: T31, section "Further security measures — not in this change, each open" (archive/031)
opened: 2026-09-27
decided:
done:
---

# T52 - the generated valkey config and the data pod's resources leave the memory bound, the protected commands and the small containers to defaults

**Scope.** What the operator renders into the Valkey configuration and the data pod spec leaves
three bounds to someone else's default. The parts share one render path and one config-hash roll,
and two of them share the LimitRange admission path, so they are decided as one package.

- **maxmemory**: never rendered, so an OOM kill is the only bound on a growing dataset.
- **Protected-action switches**: `enable-debug-command`, `enable-module-command` and
  `enable-protected-configs` follow the compiled default of the image.
- **Sidecar and data init container resources**: none stated, so a cpu/memory `ResourceQuota`
  without LimitRange defaults refuses the data pods.

## Current state

### Shared: render path, config-hash roll, LimitRange

- `generateValkeyConf` ([`configmap.go:62-138`](../../internal/builder/configmap.go)) renders both
  data configs (`BuildConfigMap`, `BuildReplicaConfigMap`, [`:255-281`](../../internal/builder/configmap.go)).
  The data container runs `valkey-server <config>` directly
  ([`statefulset.go:822-833`](../../internal/builder/statefulset.go)), so an unrendered directive
  takes the compiled default of `spec.image`, which the CR author chooses
  ([`valkey_types.go:1032-1034`](../../api/v1/valkey_types.go)); the pins in
  [`images.go`](../../test/testimages/images.go) decide only what CI runs. The CRD has no free-form
  config field; the only `ExtraArgs` is the exporter's ([`valkey_types.go:675-677`](../../api/v1/valkey_types.go)).
- **Any rendered line rolls both tiers.** `ComputeConfigHash` ([`configmap.go:293-305`](../../internal/builder/configmap.go))
  hashes both data configs and, with Sentinel, the Sentinel config into one value, stamped on the
  data template ([`statefulset.go:153`](../../internal/builder/statefulset.go)) and the Sentinel
  template ([`sentinel.go:234`](../../internal/builder/sentinel.go)) and compared by both rolls
  ([`rolling_update.go:446`](../../internal/controller/rolling_update.go),
  [`:503-510`](../../internal/controller/rolling_update.go), [`:4864-4866`](../../internal/controller/rolling_update.go)).
  A `spec.replicas: 1` data pod whose config hash moves is replaced at once, persistent or not
  (`singlePodDeferral`, [`pod_security_migration.go:128-150`](../../internal/controller/pod_security_migration.go)),
  so a non-persistent single pod loses its dataset. An unrendered line changes nothing. The only
  hash exclusion is `GenerateValkeyConfForHash` ([`configmap.go:50-58`](../../internal/builder/configmap.go)),
  for the known-master runtime state.
- A namespace LimitRange default (below) reaches every container lacking the key, `valkey`
  included when `spec.resources` is unset, so it can set the memory limit of the maxmemory part.

### maxmemory

- The memory block of both data configs is `maxmemory-policy noeviction` plus four `lazyfree-*`
  lines ([`configmap.go:126-135`](../../internal/builder/configmap.go)): Valkey runs with
  `maxmemory 0`, and `noeviction` never refuses a write. `spec.resources` (no default,
  [`valkey_types.go:1072-1074`](../../api/v1/valkey_types.go)) goes unchanged to the `valkey`
  container ([`statefulset.go:871`](../../internal/builder/statefulset.go)), and Valkey does not
  see it: under a 128 MiB limit `INFO memory` reports the host's 47.21G.
- No field, ADR, operations page or chart alert covers `maxmemory`;
  [`prometheusrule.yaml`](../../deploy/helm/valkey-operator/templates/prometheusrule.yaml) has no
  memory alert, and the operator reads no memory figure.
- A refusing master stays Ready: the probes run `valkey-cli ping`
  ([`statefulset.go:847-870`](../../internal/builder/statefulset.go)), answered at `maxmemory`.
  The observer's write test (`SET vko:health`, [`checks.go:109-116`](../../internal/observer/checks.go),
  [`client.go:330-359`](../../internal/valkeyclient/client.go)), the only data write any operator
  component issues, turns `-OOM` into an error ([`client.go:479-481`](../../internal/valkeyclient/client.go))
  and makes the observer unready by default (`writeTestFailure`,
  [`valkey_types.go:908-911`](../../api/v1/valkey_types.go)); read, not run.

Measured in docker (`valkey/valkey:9.1.1` and `8.1.9`, generated non-persistent config, 1 MiB
values, identical on both pins):

- No `maxmemory`, 128 MiB limit: key 115 closes the connection, `OOMKilled`, exit 137, no log line
  first; restarted with `DBSIZE 0`.
- `maxmemory 96mb`: key 78 answers `OOM command not allowed ...`, reads and `PING` served; the last
  accepted write overshoots by up to its own size.
- Master plus replica: without `maxmemory` the master is killed, the replica survives at 127.8 MiB;
  with it both stay up with the full dataset, the master's refusal bounding the replica
  (`replica-ignore-maxmemory yes`).
- `allkeys-lru` with `maxmemory 96mb`: all writes accepted, 74 keys evicted, no kill.

**Impact.** With a memory limit the master is OOM-killed with no refused write and no warning; a
non-persistent one restarts empty and its replicas can resync to empty (T35), mode `rdb` reloads
its last save; a promoted replica sits at the same edge (inference). Without a limit (the default)
growth ends at node memory pressure, and a pod without requests is evicted first.

### Protected-action switches

- None of the three is rendered; the `# General` block ([`configmap.go:116-124`](../../internal/builder/configmap.go))
  is where they would go. The operator issues no `CONFIG SET`, `DEBUG` or `MODULE`.
- Upstream `src/config.c` defines all three as `IMMUTABLE_CONFIG`, default
  `PROTECTED_ACTION_ALLOWED_NO`, on every tag from `7.2.4-rc1` through `9.2.0-rc1` and `unstable`.
- Measured on both pins (uid 999): each `CONFIG GET` returns `no`, each `CONFIG SET` is refused as
  immutable; `DEBUG SLEEP 0` and `MODULE LOAD` are refused, also from inside the container;
  `CONFIG SET dir`/`dbfilename` are refused as protected, `maxmemory-policy` answers `OK`. The
  generated config plus the three lines as `no` boots and behaves identically. A multi-key
  `CONFIG GET` returns keys in an unspecified order, so a test reads one key per call. Sentinel is
  out of scope (`DEBUG` and `MODULE` are unknown commands there).
- `Valkey Image Tools` ([`release.yml:624-657`](../../.github/workflows/release.yml)) runs
  `make test-image-tools` on every pull request and is required; Renovate caps the pins per major
  ([`renovate.json:212`](../../renovate.json), [`:223`](../../renovate.json)). Its
  `TestRestrictedRuntime_ValkeyServerPersistsAndAnswers`
  ([`restricted_runtime_test.go:99`](../../test/imagetools/restricted_runtime_test.go)) starts
  `valkey-server` on both pins with no config file ([`:107`](../../test/imagetools/restricted_runtime_test.go)),
  so a `CONFIG GET` there reads the compiled default; ADR 0017 D53 does not mention config defaults.

**Impact.** None on any known image. With a `yes` default, any client reaching the data port (no
password without `spec.auth`, [ADR 0016](../adr/0016-authentication-and-tls-posture.md) D1,
`protected-mode no` always rendered) gains the commands; `MODULE LOAD` is the substantive residual
and needs a loadable object on disk, which the read-only root filesystem and fixed
`dir`/`dbfilename` prevent while `enable-protected-configs` stays `no`. Nothing states or checks the
default. Gap [H-7](../security/secrets-and-tls.md#h-7).

### Sidecar and data init container resources

| Container | Resources |
|---|---|
| `valkey` / exporter | `spec.resources` ([statefulset.go:871](../../internal/builder/statefulset.go)) / `spec.metrics.resources` ([:1124-1125](../../internal/builder/statefulset.go)) |
| sidecar ([statefulset.go:894](../../internal/builder/statefulset.go)) | none |
| `init-config-selector` ([statefulset.go:275](../../internal/builder/statefulset.go), [:436](../../internal/builder/statefulset.go)) | none |
| `check-data-writable` ([pod_security.go:176](../../internal/builder/pod_security.go), persistent only) | none |
| `fix-data-ownership` ([pod_security.go:213](../../internal/builder/pod_security.go), migration only, after `ComputePodSpecHash`, [:247](../../internal/builder/pod_security.go)) | none |

Deliberate: [ADR 0033](../adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md)
D7 sets no default, because a limit guessed too low OOM-kills the process holding the drain
promotion ([ADR 0012](../adr/0012-the-sidecar-records-its-drain-promotion-on-the-pod.md)); its
Alternatives reject per-container fields (lines 484-485) and measured defaults (481-483), and
record pod-level resources as not weighed (711-713). Gap [H-18](../security/workload-pod-posture.md#h-18).

Admission, read in Kubernetes v1.36.4 source, not measured:

- `ResourceQuota` refuses a pod unless every container and init container states each tracked
  cpu/memory key; pod-level `spec.resources` skips that while `PodLevelResources` is on (Beta,
  default-on since 1.34).
- `LimitRanger` (mutating, before validation) fills `defaultRequest` and `default` into every
  container and init container lacking the key. A `defaultRequest`-only LimitRange satisfies a
  `cpu`, `memory` or `requests.*` quota with no limit; a `limits.*` quota needs `default` limits,
  which then reach the sidecar and init containers; a `max` alone becomes a default limit; a
  default limit below a stated request makes the pod invalid. The default also reaches the
  exporter, Sentinel containers and the observer's limits when their fields are unset.
- The operator does not fight a defaulted pod: `podSpecHashChanged`
  ([rolling_update.go:515-525](../../internal/controller/rolling_update.go)) and
  `sentinelPodNeedsUpdate` ([:4838-4861](../../internal/controller/rolling_update.go)) decide on
  the pod-spec hash first, and only a legacy pod without it is replaced once. No test pins that:
  [rolling_update_test.go:225-239](../../internal/controller/rolling_update_test.go) passes `nil`
  desired containers, [:2126-2148](../../internal/controller/rolling_update_test.go) identical
  containers without resources. A template-mutating policy is reverted by `containerChanged`
  ([statefulset.go:1348-1350](../../internal/builder/statefulset.go)).
- A policy engine validating the StatefulSet template (Kyverno `require-pod-requests-limits`,
  autogen, `Enforce`) refuses the write itself, reported as `ReconcileBlocked`; neither a
  LimitRange nor pod-level resources help (read in Kyverno docs).

No tracked file mentions LimitRange; these present the refusal as unconditional:
[compute-resources.md:26-33](../operations/compute-resources.md),
[docs/operations/README.md:21](../operations/README.md),
[workload-pod-posture.md:110](../security/workload-pod-posture.md) and H-18 (lines 217-224), ADR
0033 Context line 168 and Consequences lines 452-453, CLAUDE.md line 1007.

**Impact.** In a quota namespace without LimitRange defaults, a fresh CR gets no data pod
(`FailedCreate`); an existing tier loses each replaced pod (a roll holds with
`PodRecreationStalled`, a pod lost outside a roll stays missing). Workaround without an operator
change: a LimitRange with `defaultRequest` (`default` for `limits.*`), and every existing resources
field set for every key it defaults, so the default does not reach `valkey-server`. It fails closed.

## Required changes

### Shared, independent of the open questions

1. [compute-resources.md](../operations/compute-resources.md) in one edit, plus
   [persistence.md](../operations/persistence.md) next to "Without persistence, a restarted master
   can empty its replicas": the LimitRange path and its caveats (marked as Q6 decides), and
   `noeviction` without `maxmemory`, so `OOMKilled` at the memory limit with no refusal first, a
   limit a LimitRange can set. Correct every unconditional statement listed above. Cite no ticket.
2. One upgrade-neutrality unit test pinning `ComputeConfigHash` of a CR without new fields to a
   constant computed on the current code; mutation "render `maxmemory 0` unconditionally" goes red.
   Under Q4 = A2 it also shows the three switch lines do not move it.
3. Unit test for `podSpecHashChanged` and `sentinelPodNeedsUpdate`: a hash-matching pod whose
   containers carry requests and limits the desired template lacks is not outdated, with the real
   desired containers passed; mutation: comparing resources after a hash match goes red.
4. Measure on Kind the headroom between `maxmemory` and the limit (a full sync and, in mode `rdb`,
   a `BGSAVE`); it decides the sizing advice.
5. Every CRD field decided below (Q1, Q2, Q5) goes through one `make generate-all` and one README CRD
   reference pass.

### maxmemory (Q1-Q3)

- The field in [`valkey_types.go`](../../api/v1/valkey_types.go), doc comment naming the roll of
  both tiers and the data loss of a `spec.replicas: 1` non-persistent pod; rendered in both data
  configs; zero renders nothing or is refused, never `maxmemory 0`. A new ADR (field, default off,
  why no derived default, the policy decision).
- Tests: `maxMemory: 96Mi` renders `maxmemory 100663296` in master and replica config (mutation
  "master only" goes red; one case per policy under Q2 = P2); zero renders nothing or is refused;
  under Q3 = CEL an envtest 1.29 row refusing a value not below the limit; e2e on both Valkey lines
  writing until `-OOM` with master `restartCount` unchanged, no `OOMKilled`, reads served and every
  replica's `DBSIZE` equal to the master's.

### Protected-action switches (Q4)

- **Q4 = B:** in `test/imagetools`, read each switch with its own call and `assert.Contains`
  `<switch>:no`; optionally `DEBUG SLEEP 0` answers `DEBUG command not allowed` and
  `CONFIG SET dir` answers `can't set protected config`; mutation expecting `yes` goes red. Amend ADR
  0017 D53 and [`testing.md`](../developer/testing.md) (table row, "Image tools").
- **Q4 = A2:** render the three lines as `no` in `# General`, excluded from the hash next to
  `GenerateValkeyConfForHash`; unit tests for the ConfigMap lines and an exclusion pinned to exactly
  these three constant lines; add them to the directive table in
  [`secrets-and-tls.md`](../security/secrets-and-tls.md).
- Either way: record the decision in ADR 0016 (under B, A2 as the next step if the check turns
  red); rewrite H-7 to its final form without the "Not verified: the images themselves" line.

### Sidecar and data init container resources (Q5, Q6)

- **Q5 = A:** amend ADR 0033: D7 stands, B and C weighed and lost to the LimitRange path; correct
  Context line 168 and Consequences lines 452-453.
- **Q5 = B:** two optional fields, no default, wired to the sidecar and init containers through a
  helper or a post-assembly loop like [sentinel.go:397-402](../../internal/builder/sentinel.go)
  (`buildPodSpec` has no gocyclo budget left), deciding whether `fix-data-ownership` takes the
  value; unit test that unset fields yield an identical PodSpec and hash
  ([statefulset.go:1228-1239](../../internal/builder/statefulset.go)); e2e in a quota namespace with
  the unset case as ADR 0017 D11 positive control; amend ADR 0033 D7, mark lines 484-485 superseded.
- **Q6 = V3 or V2:** the test described there.

## Open questions

### Q1: How does `maxmemory` reach the config? (maxmemory)

ADR 0005 D1: a new feature defaults to off; only a defect repair may reach existing clusters
without a CR edit. `noeviction` without `maxmemory` is Valkey's own default.

- **A - opt-in `spec.maxMemory` (`resource.Quantity`, no default) (recommended).** Unset rolls
  nothing; set, it rolls both tiers. Cost M. A later lowered limit can leave it above the limit
  unless Q3 adds the CEL rule.
- **B - opt-in `spec.maxMemoryPercent` of `spec.resources.limits.memory`.** Follows a resize; needs
  a rule for a CR without a limit, and under Q5 = C would also have to read a pod-level limit. Cost M+.
- **C - derived default whenever a limit is set.** Cost S, but every such cluster rolls on upgrade,
  write semantics change fleet-wide on an unmeasured fraction, and single non-persistent pods lose
  their data.

A keeps every cluster unchanged and shows in `INFO memory` exactly the number written; B saves one
edit on a resize that rolls anyway and sees a LimitRange limit no better.

**Answer:** _open_

### Q2: Does the eviction policy become a field too? (maxmemory)

With `maxmemory` set, `noeviction` refuses writes and an eviction policy silently drops keys.

- **P1 - keep `noeviction` fixed (recommended).** No cost; a cache user gets no eviction yet.
- **P2 - opt-in enum over the eight upstream policies, `noeviction` when unset.** Cost S; adds a
  data-deleting mode and splits the sizing advice by policy.

P1: nobody asked, P2 can be added later byte-identical when unset, and ADR 0028 exists to stop the
only dataset being discarded.

**Answer:** _open_

### Q3: Is a `spec.maxMemory` not below the memory limit refused at admission? (maxmemory, only if Q1 = A)

A CEL rule can compare it with `spec.resources.limits.memory` (quantity library from 1.29); it sees
only the CR, not a LimitRange or pod-level limit.

- **CEL rule (recommended).** Refuses the value and a later limit reduction below it; one rule and
  an integration row; the cost budget is unverified.
- **No validation, documented headroom.** No cost; a lowered limit silently brings the kill back.

The rule closes the one drift case favouring Q1 = B; if its cost budget fails in envtest, re-weigh Q1.

**Answer:** _open_

### Q4: State the three switches in the config, or rely on the image default and check it on the pins? (protected-action switches)

Every upstream image defaults to `no`, so neither option changes a known cluster.

- **B - render nothing, assert the compiled default on both pins (recommended).** No roll. Cost XS.
  Leaves open a custom build or a new major in production before the pins cross; neither is known.
- **A2 - render the lines as `no`, excluded from the config hash.** Cost S. A second hash exclusion
  a later edit could misuse; the lines take effect only at the next pod recreation, so the ConfigMap
  can say `no` while the server allows the commands; a build flipping the default can ignore them.

B closes the only realistic path, an upstream release, at the pin-bump PR behind a required check;
A2 is the next step if that check turns red.

**Answer:** _open_

### Q5: Does the operator gain a way to state resources for the sidecar and the data init containers? (container resources)

A LimitRange already admits the pods, and an unset field moves no hash, so nothing rolls under any
option; the question is whether reopening ADR 0033 D7 is worth it.

- **A - keep D7, document the LimitRange path (recommended).** Cost S. A scoped quota or pod-level
  resources need their values checked; a `limits.*` quota forces the administrator to pick an
  unmeasured sidecar limit; a template-validating policy engine stays unserved.
- **B - optional fields, no default.** Cost M. The only option serving a template-validating policy
  engine; overturns a recorded rejection and makes every future data-pod container a field decision.
- **C - opt-in pod-level resources.** Cost M. Beta API, dropped silently below 1.34, so it needs a
  `writeWorkload` read-back ([pod_hardening.go:54-72](../../internal/controller/pod_hardening.go)
  reads only `hostUsers`) with its own `ReconcileBlocked` reason; `valkey-server` and the sidecar
  share one memory budget. Serves no case A leaves open.

A: a `defaultRequest`-only LimitRange meets a request quota on every supported version without a
limit, so D7's OOM concern does not arise; B becomes right once a tenant without addable LimitRange
defaults, or with a template-validating policy engine, is named.

**Answer:** _open_

### Q6: How is the documented LimitRange path verified? (container resources)

It rests on upstream source reading; the operator-side half is the unit test under every option.

- **V1 - document as "read in upstream source v1.36.4, not measured" (recommended).** Cost XS; a
  surprise first shows in production.
- **V3 - envtest.** Dry-run the built pod in a quota namespace with and without a `defaultRequest`
  LimitRange. Cost S, two envtest preconditions unverified.
- **V2 - e2e.** Data and Sentinel tiers reach `OK` and roll in such a namespace, plus a control, on
  both single-node legs. Cost S-M; the natural carrier if Q5 = B.

V1 follows ADR 0033 D8's precedent for upstream behaviour; V3 or V2 wins once a production quota
namespace exists or Q5 = B.

**Answer:** _open_

## Not verified

- On Kubernetes: kubelet restart after `OOMKilled`, which container the OOM killer picks (also at a
  pod-level limit under Q5 = C), kill order under node pressure; the Kind headroom run settles it.
- How `WAIT` answers at `maxmemory`; which `redis_exporter` metric exposes `maxmemory`.
- The sidecar's CPU and memory peak under a drain promotion; a Kind drain run with metrics.
- Production: whether a CR sets a memory limit and how close its master runs to it; whether a
  `spec.image` is a custom build with another compiled default (`CONFIG GET` per distinct image);
  whether a namespace (gitlab, gpt, harbor, iam, database-examples) has a cpu/memory quota, a
  LimitRange or a resources policy (`kubectl get resourcequota,limitrange -A`).
- What the CR reports when a fresh CR's data pods are refused, or a pod is lost outside a roll.
- For Q6 = V3: whether envtest 1.29 enables `LimitRanger` and `ResourceQuota`, and refuses while
  `status.hard` is unset. For Q5 = C: whether controller-runtime logs `Warning:` headers on 1.29-1.31.

## Related

- T35 - what happens after the restart an OOM kill causes; cites the refused-`DEBUG` measurement.
- T34 - its option C (booting `valkey-server` on generated config per pin) could host a Q4 = A2
  assertion; Q4 = B does not depend on it.
- T50 - component ACL users would not restrict the client-facing default user; no overlap.
