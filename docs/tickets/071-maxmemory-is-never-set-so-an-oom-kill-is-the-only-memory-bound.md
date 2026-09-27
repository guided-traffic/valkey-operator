---
id: T71
title: maxmemory is never set, so an OOM kill of the container is the only bound on a growing dataset
state: analysed       # code read and the Valkey side measured in docker on both pins; nothing run on Kubernetes
severity: medium      # a dataset that outgrows the memory limit ends in an OOM kill instead of refused writes; the data loss after the restart is rated in T36
security: none        # whoever can grow the dataset to the limit can already delete it; co-located pods are protected by the container limit and QoS, which maxmemory does not change
urgency: next         # rule 3: severity medium, trigger live in released code
effort: M             # recommended option: one CRD field, render, regeneration, README row, ADR, unit tests, e2e on both Valkey lines
blocked-by: decision  # Q1 and Q2
filed-from: T36
opened: 2026-09-27
decided:
done:
---

# T71 - maxmemory is never set, so an OOM kill of the container is the only bound on a growing dataset

## Current state

- **No `maxmemory` is rendered.** `generateValkeyConf` ([`configmap.go:62`](../../internal/builder/configmap.go))
  renders both data configs (`BuildConfigMap`, `BuildReplicaConfigMap`,
  [`configmap.go:255-281`](../../internal/builder/configmap.go)). Their memory block is
  `maxmemory-policy noeviction` plus four `lazyfree-*` lines and nothing else
  ([`configmap.go:126-135`](../../internal/builder/configmap.go), policy at
  [`configmap.go:129`](../../internal/builder/configmap.go)). Valkey therefore runs with its
  default `maxmemory 0` (no limit), and `noeviction` never refuses a write.
- **Valkey does not see the container limit.** `spec.resources` (no default,
  [`valkey_types.go:1072-1074`](../../api/v1/valkey_types.go)) is passed unchanged to the `valkey`
  container ([`statefulset.go:871`](../../internal/builder/statefulset.go)); under a 128 MiB cgroup
  limit `INFO memory` reports `total_system_memory_human:47.21G` (the host).
- **No CRD field reaches it.** No field for `maxmemory` or the policy, no generic config
  pass-through; the only `ExtraArgs` is the exporter's
  ([`valkey_types.go:675-677`](../../api/v1/valkey_types.go)). No ADR, operations page or chart
  alert mentions `maxmemory`; the chart's
  [`prometheusrule.yaml`](../../deploy/helm/valkey-operator/templates/prometheusrule.yaml) has no
  memory alert.
- **A new config line rolls both tiers.** `ComputeConfigHash` folds both data configs and, with
  Sentinel, the Sentinel config into one hash
  ([`configmap.go:293-305`](../../internal/builder/configmap.go)), stamped on the data pod template
  ([`statefulset.go:153`](../../internal/builder/statefulset.go)) and the Sentinel pod template
  ([`sentinel.go:234`](../../internal/builder/sentinel.go)) and compared by both rolls
  ([`rolling_update.go:446`](../../internal/controller/rolling_update.go),
  [`rolling_update.go:503-510`](../../internal/controller/rolling_update.go),
  [`rolling_update.go:4864-4866`](../../internal/controller/rolling_update.go)). A line that is not
  rendered changes nothing. A `spec.replicas: 1` data pod whose config hash moves is replaced at
  once, persistent or not (`singlePodDeferral`,
  [`pod_security_migration.go:128-150`](../../internal/controller/pod_security_migration.go)).
- **What would read a refusal.** The probes run `valkey-cli ping`
  ([`statefulset.go:847-870`](../../internal/builder/statefulset.go)); `PING` is answered at
  `maxmemory`, so a refusing master stays Ready. The observer's write test sends `SELECT <db>` then
  `SET vko:health <value> EX 10` on one connection, no `MULTI`
  ([`checks.go:109-116`](../../internal/observer/checks.go),
  [`client.go:330-359`](../../internal/valkeyclient/client.go)); the `-OOM` reply becomes an error
  ([`client.go:479-481`](../../internal/valkeyclient/client.go)) and makes the observer unready by
  default (`writeTestFailure`, [`valkey_types.go:908-911`](../../api/v1/valkey_types.go)). That
  chain is read, not run. The observer's `SET` is the only data write any operator component
  issues.

**Measured in docker** (`valkey/valkey:9.1.1` and `8.1.9`, the generated non-persistent config,
1 MiB values via `SETRANGE`, identical on both pins):

- No `maxmemory`, 128 MiB limit: 114 keys accepted, key 115 closes the connection,
  `OOMKilled=true`, exit 137, no log line before the kill; restarted with `DBSIZE 0`.
- `maxmemory 96mb`, 128 MiB limit: key 78 answers `OOM command not allowed ...`, container
  keeps running, `DBSIZE 77`, reads and `PING` served; the last accepted write overshoots
  `maxmemory` by up to its own size.
- Master plus replica, 128 MiB each: without `maxmemory` the master is killed and the replica
  survives at its own limit (127.8 MiB of 128 MiB). With `maxmemory 96mb` the master refuses,
  both stay up, the replica keeps the full dataset with link `up` - the master's refusal bounds
  the replica, which ignores its own `maxmemory` (`replica-ignore-maxmemory yes`).
- `allkeys-lru` with `maxmemory 96mb`: all writes accepted, 74 keys evicted, no kill.

**Impact.**

- **Memory limit set:** the master is OOM-killed with no refused write and no warning; on a
  non-persistent cluster it restarts empty and its replicas can resync to empty (T36); in mode
  `rdb` it reloads its last save. Replicas sit at the same edge, so a promoted one is likely
  killed by the next burst (inference).
- **No memory limit** (the default): growth ends at node memory pressure; a pod without requests
  is BestEffort and evicted first. A namespace `LimitRange` default memory limit puts a cluster
  into the first case with no limit in its CR (read in upstream source, not measured).
- Nothing in the CR status or chart alerts precedes the kill; the operator reads no memory figure.

## Required changes

### Independent of the open questions

- Document today's behaviour in [persistence.md](../operations/persistence.md) (next to "Without
  persistence, a restarted master can empty its replicas") and
  [compute-resources.md](../operations/compute-resources.md): `noeviction` without `maxmemory`,
  an `OOMKilled` at `spec.resources.limits.memory` with no refusal first, and that a `LimitRange`
  can set the limit. Cite no ticket.
- Measure on Kind the headroom a pod needs between `maxmemory` and the limit (a full sync and, in
  mode `rdb`, a `BGSAVE` under the limit); it decides the sizing advice in the docs.

### Depends on the answers

- The field(s) in [`valkey_types.go`](../../api/v1/valkey_types.go) with a doc comment naming the
  roll of both tiers and the data loss of a `spec.replicas: 1` non-persistent pod on setting it.
- The render in the memory block of [`configmap.go`](../../internal/builder/configmap.go), in both
  data configs; a zero value renders nothing or is refused, never `maxmemory 0`.
- `make generate-all` (CRD, DeepCopy, chart CRD copy), the README CRD reference row, a new ADR
  (the field, default off, why no derived default, the policy decision).
- Tests:
  - Unit, upgrade neutrality: a CR without the field renders no `maxmemory` line and
    `ComputeConfigHash` equals a pinned constant computed on the current code; mutation
    "render `maxmemory 0` unconditionally" must go red.
  - Unit, render: `maxMemory: 96Mi` renders `maxmemory 100663296` in master and replica config;
    mutation "master only" must go red. One case per policy if Q2 picks P2.
  - Unit or integration: `maxMemory: 0` renders nothing or is refused.
  - Integration (envtest 1.29), if Q3 picks the CEL rule: a value not below
    `spec.resources.limits.memory` is refused, a lower one admitted, the rule cost accepted.
  - E2E, both Valkey lines: three replicas, memory limit, lower `maxMemory`; write until `-OOM`;
    master `restartCount` unchanged, no `OOMKilled`, reads served, every replica's `DBSIZE`
    equals the master's.

## Open questions

### Q1: How does `maxmemory` reach the config?

ADR 0005 D1: a new feature defaults to off so an upgrade changes nothing; only a defect repair
may reach existing clusters without a CR edit. `noeviction` without `maxmemory` is Valkey's own
default.

- **A - opt-in `spec.maxMemory` (`resource.Quantity`, no default) (recommended).** Unset renders
  nothing, upgrade rolls nothing; set, it is rendered in both data configs and rolls the data tier
  and, with Sentinel, the Sentinel tier. Cost M. A later lowered memory limit can leave it above
  the limit unless Q3 adds the CEL rule.
- **B - opt-in `spec.maxMemoryPercent` of `spec.resources.limits.memory`.** Follows a resized
  limit; needs a rule for a CR without a limit and ties the render to the resources block. Cost M
  plus that rule.
- **C - derived default whenever a memory limit is set.** Cost S, but every cluster with a limit
  rolls on upgrade, write semantics change fleet-wide on an unmeasured fraction, and every
  `spec.replicas: 1` non-persistent pod with a limit loses its dataset on upgrade.

A keeps every existing cluster unchanged and shows in `INFO memory` exactly the number the author
wrote. B only saves one CR edit on a resize that rolls the tier anyway and sees a `LimitRange` or
pod-level limit no better than A.

**Answer:** _open_

### Q2: Does the eviction policy become a field too?

With `maxmemory` set, `noeviction` refuses writes and an eviction policy silently drops keys.

- **P1 - keep `noeviction` fixed (recommended).** No extra cost; a cache user gets no eviction yet.
- **P2 - opt-in enum `spec.maxMemoryPolicy` over the eight upstream policies, `noeviction` when
  unset.** Cost S; adds a data-deleting mode, and upstream advises a lower `maxmemory` with
  replicas under eviction policies, so sizing advice splits by policy.

Nobody has asked for eviction, and P2 can be added later byte-identical to P1 when unset, so
deferring it costs nothing; ADR 0028 exists to stop the only dataset being discarded.

**Answer:** _open_

### Q3: Is a `spec.maxMemory` not below the memory limit refused at admission? (only with A)

A CEL rule can compare `spec.maxMemory` with `spec.resources.limits.memory` when both are set
(quantity library from Kubernetes 1.29). It sees only the CR, not a `LimitRange` or pod-level
limit.

- **CEL rule (recommended).** Refuses the value and a later limit reduction below it; cost one
  rule and an integration test; the CEL cost budget is unverified.
- **No validation, documented headroom.** No cost; a lowered limit silently brings the kill back.

The rule closes the one drift case that favours B in Q1. If its cost budget fails in envtest,
re-weigh Q1.

**Answer:** _open_

## Not verified

- Anything on Kubernetes: kubelet restart after `OOMKilled`, which container the OOM killer
  picks, kernel kill order under node pressure - settled by a Kind run.
- Headroom needed for fork copy-on-write, full-sync buffers, fragmentation - the Kind measurement
  above.
- How `WAIT` answers on a master at `maxmemory`; which `redis_exporter` metric exposes
  `maxmemory` - settled by running each once.
- Whether any production CR sets a memory limit and how close its master runs to it.

## Related

- T36 - what happens after the restart this ticket's kill causes.
- T52 - option C (pod-level memory limit) and the `LimitRange` analysis touch the same limit.
