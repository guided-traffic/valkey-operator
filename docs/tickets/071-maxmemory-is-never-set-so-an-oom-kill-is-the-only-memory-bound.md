---
id: T71
title: maxmemory is never set, so an OOM kill of the container is the only bound on a growing dataset
state: analysed       # the code read at 84a39c2, the Valkey side measured in docker on both pins (with and without maxmemory, standalone and master plus replica), the options costed with a marked recommendation; nothing run on Kubernetes (History 2026-09-27)
severity: medium      # a dataset that outgrows spec.resources.limits.memory ends in an OOM kill of valkey-server with nothing refused and nothing logged before it, instead of refused writes; the data loss that can follow the restart is T36's mechanism and is rated there (high)
security: none        # decided 2026-09-27, not hardening: every principal who can drive the master to its memory limit can already delete the dataset, and the isolation of co-located pods is the container memory limit and the QoS order, which exist today and which maxmemory does not change (Impact, Security)
urgency: next         # rule 3, first match: severity medium and the trigger is live in released code (any cluster with a memory limit whose dataset grows past it); rule 1 checked and not matched (no unreleased feature, no tracked statement measured false), rule 2 not matched
effort: M             # the recommended option A: one CRD field, the render in both data configs, CRD regeneration, the README reference row, an ADR, unit tests with a hash-neutrality check, and an e2e on both Valkey lines
blocked-by: decision  # decisions 1 and 2 under Options
filed-from: T36       # the Fact bullet "maxmemory is never set; the memory limit is the backstop" and the removed option "maxmemory derived from the memory limit", moved here during the re-verification of 2026-09-27 at 84a39c2; T52 option C cites the same question
opened: 2026-09-27
decided:
done:
---

# T71: maxmemory is never set, so an OOM kill of the container is the only bound on a growing dataset

Filed on 2026-09-27 from [T36](036-non-persistent-master-restarts-empty.md) (its Fact bullet
"`maxmemory` is never set; the memory limit is the backstop", its Work list item that asked for
this file, and the option "`maxmemory` derived from the memory limit" that T36's re-verification
removed), during the re-verification of that day at `84a39c2`. It is its own ticket under the
filing rule because it is a capacity and eviction decision with its own mechanism: T36 is what
happens after a restart, this ticket is why a growing dataset ends in one.
[T52](052-the-sidecar-and-data-init-containers-state-no-resources.md) option C cites the same
question. This file is the record; the hosts keep a pointer.

## Fact

**Mechanism.** The operator renders the data tier's Valkey config from one generator,
`generateValkeyConf`, into two ConfigMaps, the master config and the replica config
([`configmap.go:62`](../../internal/builder/configmap.go), `BuildConfigMap` and
`BuildReplicaConfigMap` at [`configmap.go:255-281`](../../internal/builder/configmap.go)); both
carry the same memory block. That block is
`maxmemory-policy noeviction` plus four `lazyfree-*` lines, and nothing else
([`configmap.go:126-135`](../../internal/builder/configmap.go), the policy at
[`configmap.go:129`](../../internal/builder/configmap.go)). No `maxmemory` line is rendered,
so Valkey runs with its built-in `maxmemory 0`, which is no limit. Under `noeviction` the
policy acts only when `maxmemory` is reached, so with no `maxmemory` the server never refuses a
write for memory reasons. The container memory limit is `spec.resources`, passed through
unchanged to the `valkey` container ([`statefulset.go:871`](../../internal/builder/statefulset.go);
the field, with no default, at [`valkey_types.go:1072-1074`](../../api/v1/valkey_types.go)).
Valkey does not read that limit (measured below: `total_system_memory` reports the host). So a
growing dataset grows until the kernel's cgroup OOM killer ends the container at the limit
(`OOMKilled`, exit code 137), and the kubelet restarts it; on a non-persistent master that
restart is the entry into [T36](036-non-persistent-master-restarts-empty.md). Without a memory
limit, the same growth ends at node memory pressure instead (Impact, case C).

**No CRD field reaches it.** The CRD has no field for `maxmemory` or for the policy, and no
generic config pass-through: the only `ExtraArgs` in the CRD is the exporter's
([`valkey_types.go:675-677`](../../api/v1/valkey_types.go)). `git grep -n -i maxmemory` outside
`docs/tickets/` finds exactly two hits at `84a39c2`: the policy line
([`configmap.go:129`](../../internal/builder/configmap.go)) and the unit test that asserts it
([`configmap_test.go:53`](../../internal/builder/configmap_test.go)). No ADR, no page under
`docs/` and no chart alert mentions `maxmemory` or `noeviction`; the chart's `PrometheusRule`
carries no memory alert ([`prometheusrule.yaml`](../../deploy/helm/valkey-operator/templates/prometheusrule.yaml),
alerts at lines 32-161).

**A new config line rolls both tiers of a Sentinel cluster.** `ComputeConfigHash` folds both
data configs and, when Sentinel is enabled, the Sentinel config into one FNV hash
([`configmap.go:293-305`](../../internal/builder/configmap.go)); it is stamped on the data pod
template ([`statefulset.go:153`](../../internal/builder/statefulset.go)) and on the Sentinel pod
template ([`sentinel.go:234`](../../internal/builder/sentinel.go)); the data roll compares it
([`rolling_update.go:446`](../../internal/controller/rolling_update.go),
[`rolling_update.go:503-510`](../../internal/controller/rolling_update.go)) and so does the
Sentinel roll ([`rolling_update.go:4864-4866`](../../internal/controller/rolling_update.go)). A
`maxmemory` line that appears in the rendered config therefore rolls the data tier and, on a
Sentinel cluster, the Sentinel tier. A line that is not rendered changes nothing.

**What reads the refusal once `maxmemory` exists.** The readiness and liveness probes run
`valkey-cli ping` ([`statefulset.go:847-870`](../../internal/builder/statefulset.go),
`ProbeCommand` at [`statefulset.go:1515`](../../internal/builder/statefulset.go)); `PING` is
answered at `maxmemory` (measured), so a refusing master stays Ready and is not restarted. The
observer's write test is `SELECT <db>` followed by `SET vko:health <value> EX 10`
([`checks.go:109-116`](../../internal/observer/checks.go), run at
[`observer.go:286-290`](../../internal/observer/observer.go), database 15 unless
`spec.observer.db` says otherwise). ~~in one `MULTI`~~ *(corrected 2026-09-27 in the review of
this file: despite its name, `ExecMulti` sends no `MULTI` and no `EXEC`; it writes each command
on one connection and reads one reply per command
([`client.go:330-359`](../../internal/valkeyclient/client.go)). The misleading name and the
doc comment's "the last non-OK error is returned" (the loop returns on the first) do not change
this ticket's chain and are [T70](070-tracked-comments-and-adrs-state-what-the-code-contradicts.md)
item (j).)* At `maxmemory` the `SET`
answers `-OOM` (measured, M2 and M5), `readFullResponse` turns a `-` reply into an error
([`client.go:479-481`](../../internal/valkeyclient/client.go)), and a failed write test makes
the observer unready by default (`writeTestFailure` defaults to true,
[`valkey_types.go:908-911`](../../api/v1/valkey_types.go),
[`valkey_types.go:1459-1464`](../../api/v1/valkey_types.go)); that chain is read, not run. So
with `maxmemory` set, an enabled observer turns refused writes into a visible signal; today,
nothing precedes the kill. `grep` over `internal/` for data-writing commands finds the
observer's `SET` as the only one any operator component issues; the others found are `WAIT`
([`client.go:294`](../../internal/valkeyclient/client.go)) and `SENTINEL SET`
([`client.go:283`](../../internal/valkeyclient/client.go)), and how `WAIT` behaves on a master
at `maxmemory` was not measured.

**Upstream behaviour** (valkey.conf at the two pinned tags, the memory section is identical in
substance): under `noeviction` at `maxmemory` "the server will start to reply with errors to
commands that would use more memory, like SET, LPUSH, and so on, and will continue to reply to
read-only commands like GET"
([valkey.conf 9.1.1:1307-1310](https://github.com/valkey-io/valkey/blob/9.1.1/valkey.conf#L1307-L1310),
[8.1.9:1232-1235](https://github.com/valkey-io/valkey/blob/8.1.9/valkey.conf#L1232-L1235));
the replicas' output buffers are subtracted from the used-memory count, and a lower
`maxmemory` is advised with replicas attached, "but this is not needed if the policy is
'noeviction'" (9.1.1:1315-1324, 8.1.9:1240-1249); `maxmemory-policy noeviction` is the default
(9.1.1:1355, 8.1.9:1280); and a replica ignores its own `maxmemory` "unless it is promoted to
primary", with the warning that it "may end using more memory than the one set via maxmemory"
and must have "enough memory to never hit a real out-of-memory condition before the primary
hits the configured maxmemory setting" (`replica-ignore-maxmemory yes`, 9.1.1:1377-1395,
8.1.9:1302-1320).

### Measurements (docker), 2026-09-27

Environment: Docker Desktop, linuxkit kernel 6.10.14, aarch64, cgroup v2. Images: the two
pins of [`test/testimages/images.go`](../../test/testimages/images.go), `valkey/valkey:9.1.1`
and `valkey/valkey:8.1.9`. Every container and network was named `vko-file-t71-*` and removed
after its run.

**Config.** The generated shape of a non-persistent cluster, copied line by line from
`generateValkeyConf` and `persistenceConfig` at `84a39c2` (network block, `save ""`,
`appendonly no`, the general block, and the memory block of
[`configmap.go:126-135`](../../internal/builder/configmap.go)); TLS, auth and replication
lines omitted. A second file is the same plus `maxmemory 96mb`:

```
# Network
bind 0.0.0.0
port 6379
protected-mode no
tcp-backlog 511
timeout 0
tcp-keepalive 300

# Persistence (disabled)
save ""
appendonly no

# General
daemonize no
loglevel notice
databases 16
always-show-logo no

# Memory
maxmemory-policy noeviction
lazyfree-lazy-eviction yes
lazyfree-lazy-expire yes
lazyfree-lazy-server-del yes
lazyfree-lazy-user-del yes
```

**Writer** (`fill.sh`, run from a separate client container on the same network, so the client
is not in the server's cgroup). Each `SETRANGE k<i> 1048575 x` creates a 1 MiB value:

```sh
H=$1; N=$2; i=0
while [ $i -lt $N ]; do
  i=$((i+1))
  r=$(valkey-cli -h $H SETRANGE k$i 1048575 x 2>&1)
  case "$r" in
    1048576) ;;
    *) echo "key $i reply: $r"; break;;
  esac
done
echo "last attempted key: $i"
```

**M1 - no `maxmemory`, 128 MiB limit, standalone.**
`docker run -d --name $S --network $NET --memory=128m --memory-swap=128m -v $W/conf:/conf:ro $IMG valkey-server /conf/valkey.conf`,
then `fill.sh $S 400`, then `docker inspect`, then `docker start $S` and `DBSIZE`.
Result, identical on both pins: keys 1-114 accepted (`1048576` each), key 115 answered
`Error: Server closed the connection`; `OOMKilled=true ExitCode=137`; the server log ends at
"Ready to accept connections tcp", no warning before the kill; after `docker start`,
`DBSIZE 0`. With `--restart=on-failure:1` (the kubelet's restart, approximated) the container
came back at once with `DBSIZE 0` and `uptime_in_seconds:3`. `INFO memory` on the restarted
server: `maxmemory:0`, `maxmemory_policy:noeviction`, and `total_system_memory_human:47.21G`
under a 128 MiB limit - Valkey reports the host's memory and does not see the cgroup limit.

**M2 - `maxmemory 96mb`, 128 MiB limit, standalone.** Same command with
`/conf/valkey-maxmem.conf`. Both pins: keys 1-77 accepted, key 78 answered
`OOM command not allowed when used memory > 'maxmemory'.`; `OOMKilled=false`, container
running; `DBSIZE 77`; `STRLEN k1` `1048576` (reads served); `SET small 1` refused with the same
`OOM` error; `used_memory_human` 96.03M (9.1.1) and 97.23M (8.1.9) against `maxmemory_human`
96.00M - the check refuses the next command after the limit is crossed, so the last accepted
write overshoots by up to its own size; `docker stats` about 90.7 MiB of 128 MiB;
`evicted_keys:0`; `errorstat_OOM` counted the refusals. After `DEL k1` (`1`), a new 1 MiB
`SETRANGE` was accepted on 8.1.9 and refused on 9.1.1 in the same run; with
`lazyfree-lazy-user-del yes` the free may still have been pending. Observed once per pin, not
analysed, not load-bearing.

**M3 - `maxmemory 96mb` with `--maxmemory-policy allkeys-lru`**, 150 keys written. Both pins:
all 150 accepted, `DBSIZE 76`, `evicted_keys:74`, `EXISTS k1` `0`, no kill. Recorded only to
show what an eviction policy does instead of refusing; the operator renders no other policy.

**M4 - master plus replica, each with a 128 MiB limit** (`replica.sh`, bash; the replica
started with `--replicaof $M 6379`, both with
`--replica-serve-stale-data yes --replica-read-only yes --repl-diskless-sync yes --repl-diskless-sync-delay 5`
as [`configmap.go:175-181`](../../internal/builder/configmap.go) renders them; link `up` and
`CONFIG GET replica-ignore-maxmemory` `yes` checked before the fill, both pins).
- Without `maxmemory`: the master accepted keys 1-114 and was killed on key 115
  (`OOMKilled=true`, 137). The replica survived with `DBSIZE 114`,
  `master_link_status:down`, `docker stats` 127.8 MiB of 128 MiB (9.1.1; 127.7 MiB on 8.1.9)
  and `used_memory_human` 143.6M; that `used_memory` exceeds the cgroup usage was not analysed.
  The replica sits at its own limit: the master died first, most likely because it also holds
  the replication output buffer (inference).
- With `maxmemory 96mb` on both: the master refused key 77 with the `OOM` error and stayed up;
  the replica stayed up with `DBSIZE 76`, link `up`, `used_memory_human` 96.1M, `docker stats`
  about 90 MiB. The master's refusal also bounds the replica, which ignores its own
  `maxmemory`.

**M5 - the observer's write at `maxmemory`.** After M2's fill,
`printf 'MULTI\r\nSELECT 15\r\nSET vko:health v EX 10\r\nEXEC\r\n' | valkey-cli -h $S`: both
pins answered `OK`, `QUEUED`, the `OOM` error for the `SET`, then
`EXECABORT Transaction discarded because of previous errors.`; `PING` answered `PONG`.
*(Review of 2026-09-27: this measured a `MULTI` shape the observer does not send - it sends
`SELECT` and `SET` without `MULTI` (Fact, "What reads the refusal"). The conclusion holds
through the `SET` alone, whose `-OOM` answer M2 measured with `SET small 1`; the `MULTI` result
stays recorded as measured.)*

**Review re-measurement, 2026-09-27** (same configs and `fill.sh` as above, containers
`vko-file-t71r-*` and network `vko-file-t71r-net`, all removed after the run, verified with
`docker ps -a` and `docker network ls` filtered on the prefix). M1 on both pins: key 115
answered `Error: Server closed the connection`, `OOMKilled=true ExitCode=137`, last log line
"Ready to accept connections tcp", after `docker start` `DBSIZE 0`, `maxmemory:0`,
`total_system_memory_human:47.21G`. M2 on both pins: key 78 answered the `OOM` error,
`OOMKilled=false`, container running, `DBSIZE 77`, `PING` `PONG`, `maxmemory:100663296`,
`used_memory_human` 97.26M (9.1.1) and 97.21M (8.1.9) - the 9.1.1 figure differs from the
96.03M of the first run by the overshoot of the last accepted write, which M2 already
describes. M5 on 9.1.1: the same four replies as above; `GET k1` still answered (1 MiB value
read back). Nothing in M1, M2 or M5 was found false.

**Verified (read at `84a39c2`):** no `maxmemory` rendered, the policy line, the memory block;
no CRD field, the exporter's `ExtraArgs` the only one; the shared config hash and its three
consumers; `spec.resources` passed through without a default; the probes run `PING`; the
observer's write test shape (`SELECT` then `SET`, no `MULTI` - corrected in the review) and its
unready default; the observer's `SET` as the only data write of any operator component; no
`maxmemory` anywhere outside `docs/tickets/` except the policy line and its test; no memory
alert in the chart; a `spec.replicas: 1` rootless data pod whose config hash moved is replaced
at once whether or not it is persistent (`singlePodDeferral`,
[`pod_security_migration.go:128-150`](../../internal/controller/pod_security_migration.go), the
delete at [`rolling_update.go:3786-3820`](../../internal/controller/rolling_update.go)).
**Verified (measured, docker, both pins):** M1-M5 as above, M1, M2 and M5 re-measured in the
review. **Verified (upstream):** the valkey.conf lines cited, re-fetched in the review from
`raw.githubusercontent.com/valkey-io/valkey/<tag>/valkey.conf` and matching line for line;
`maxmemory` is an unsigned `long long` config with default `0`
([config.c 9.1.1:3473](https://github.com/valkey-io/valkey/blob/9.1.1/src/config.c#L3473)), so a
rendered `maxmemory 0` is the same as none (M1 shows `maxmemory:0` with unbounded growth); the
Kubernetes CEL quantity library is available from Kubernetes 1.29
([CEL in Kubernetes](https://kubernetes.io/docs/reference/using-api/cel/), library table,
read 2026-09-27); the `oom_score_adj` per QoS class (Guaranteed -997, BestEffort 1000,
Burstable 2-999) and the node-pressure eviction order (usage above requests first, then
priority) ([node-pressure eviction](https://kubernetes.io/docs/concepts/scheduling-eviction/node-pressure-eviction/),
read 2026-09-27).

**Not verified:** anything on Kubernetes - the kubelet restart after an `OOMKilled` of the
`valkey` container is taken from T36 and from docker's restart policy, not reproduced here;
whether the container OOM killer picks `valkey-server` over the sidecar or the exporter in the
same pod (each container has its own limit, so each is its own cgroup; inference); the headroom
a `maxmemory` needs in a pod: RDB/AOF fork copy-on-write in persistent modes, the diskless
full-sync fork, client output buffers, fragmentation (M2 shows RSS close to `used_memory` on an
idle server only); the M4 difference between `used_memory` and cgroup usage on the replica;
which process order the kernel kills under node pressure when no limit is set; whether a CEL
rule comparing `spec.maxMemory` with `spec.resources.limits.memory` fits the CRD's CEL cost
budget; how a namespace `LimitRange` default memory limit reaches the `valkey` container
(carried from T52, where LimitRanger was read in upstream source, not measured); how `WAIT`
answers on a master at `maxmemory`; which `redis_exporter` metric exposes `maxmemory` (the
exporter image was not run);
whether any production CR sets a memory limit, and at what usage its master runs (this run may
not touch a cluster).

## Impact

- **A. A memory limit is set and the dataset outgrows it.** The master is OOM-killed with no
  refused write and no log line first (M1); every client sees a closed connection; the kubelet
  restarts the container. On a non-persistent cluster the master comes back empty (M1) and,
  multi-replica, its replicas can full-resync to it and drop what they held - T36, measured
  there. In persistent mode `rdb` the master reloads its last save and can roll its replicas
  back to it (T36, exp6). The replicas carry the same dataset against the same limit and end
  at the edge of it (M4), so the next write burst after a failover kills the promoted one
  (inference from M4, not measured). A
  persistent master that reloads a dataset close to the limit can reach it again, a restart
  loop by inference, not measured.
- **B. The same cluster with `maxmemory` below the limit** (what the options add): writes that
  need memory are refused with `-OOM`, reads, `PING` and deletes are served, the pod stays
  Ready, nothing restarts, the replicas keep the full dataset (M2, M4). For an application that
  treats the store as a cache and expects the old keys to go, a refused write is a behaviour
  change it may not want; under an eviction policy the keys go instead (M3).
- **C. No memory limit** (the default: `spec.resources` has none). Nothing bounds
  `valkey-server`; growth ends at node memory pressure, where the kubelet evicts the pod whose
  usage exceeds its requests first - a pod with no requests is BestEffort and first in line,
  `oom_score_adj` 1000 (upstream docs). An evicted non-persistent data pod is replaced empty;
  the replaced-pod half of T36. *(Added in the review, carried from
  [T52](052-the-sidecar-and-data-init-containers-state-no-resources.md) option A's LimitRange
  analysis:)* a namespace `LimitRange` with a `default` memory limit gives the `valkey`
  container a limit at pod admission even when `spec.resources` states none, per resource key,
  so `spec.resources` with requests only still gets the namespace limit; such a cluster is in
  case A without a limit anywhere in its CR. By reading upstream source in T52, not measured.
- **Who hits it.** Any CR with a memory limit whose dataset is not bounded by the application;
  every CR without one, at node scale. Neither the CR status nor the chart's alerts say anything
  before the kill; the operator reads no memory figure. The trigger is live in every released
  version.
- **Security: none.** Per case: with auth on, the principal who can grow the dataset holds the
  cluster password and can `FLUSHALL` directly, so a write burst to the kill gives them nothing
  they lack; with auth off, anyone who reaches the port can do the same, and that exposure is
  the auth decision, not this one; co-located pods of other tenants are protected by the
  container memory limit, which `spec.resources` already offers, and without one by the QoS
  order above, and `maxmemory` changes neither. No guarantee the repository states is weakened,
  and no trust boundary is crossed. T36 took the same class for the same triggers.

## Options

**What the code does today and what the choice changes.** The rendered config carries
`noeviction` and no `maxmemory`, the same config on every cluster, and its hash rolls both tiers
of a Sentinel cluster when it changes. Any option below only adds a `maxmemory` line (and in
decision 2 possibly another policy line) to the two data configs; it does not touch the
container memory limit, persistence, the restart behaviour T36 is about, or the Sentinel config
itself. It changes what happens at the memory bound: a refused write (or an eviction) instead
of a kill. ADR 0005 D1 governs the choice: a new CRD feature defaults to off so an operator
upgrade changes nothing, and only the repair of a defect may reach existing clusters without a
CR edit ([ADR 0005](../adr/0005-upgrade-neutral-defaults-and-anti-affinity.md) D1 and its
2026-09-26 amendment).

Not carried as options, each for its stated reason:

- **A generic config pass-through** (a free map of Valkey directives): it would reach
  directives the operator relies on (`dir`, `replicaof`, the TLS and persistence lines,
  `enable-debug-command`) and is a decision of its own, far wider than this finding.
- **Applying `maxmemory` at runtime with `CONFIG SET` and keeping it out of the config hash**
  (the way the known-master address is kept out, [`configmap.go:293-305`](../../internal/builder/configmap.go)),
  which would save the roll: the operator issues no `CONFIG` command anywhere today (`grep` over
  `internal/`), so it is a new per-pod drift mechanism with a second truth next to the
  ConfigMap, for a one-time roll that the CR author asked for. *(Added in the review.)*
- **Documentation only, no field:** it leaves no way to set a bound at all, since the CRD has
  no pass-through; the documentation of today's behaviour is the Work list item that needs no
  decision and ships with any option. *(Added in the review.)*

### Decision 1 - how `maxmemory` reaches the config

- **A - an opt-in field `spec.maxMemory` (a `resource.Quantity`, no default) (recommended).**
  Unset renders nothing, so the config and its hash are byte-identical to today and an operator
  upgrade rolls nothing. Set, it renders `maxmemory <bytes>` in both data configs (a replica
  ignores it until promoted, so both variants need it). Setting it on an existing CR is a CR
  edit and rolls the data tier and, on a Sentinel cluster, the Sentinel tier (shared hash),
  through the ordinary failover-aware roll; on a `spec.replicas: 1` data pod without
  persistence that roll deletes the only pod and discards its dataset (a configuration change is
  the CR author's and is not deferred, `singlePodDeferral`,
  [`pod_security_migration.go:128-150`](../../internal/controller/pod_security_migration.go),
  ADR 0032 D3), which the field's doc comment and the README row must say. A zero value must be
  refused or render nothing: `maxmemory 0` is Valkey's "no limit" (default `0` at config.c 9.1.1:3473; M1 reports
  `maxmemory:0` with unbounded growth).
  Optional: a CEL rule refusing a value not below `spec.resources.limits.memory` when both are
  set (the quantity library exists from 1.29; the cost budget is not verified), or no
  validation and a documented headroom. The rule sees only the CR: a limit a `LimitRange`
  defaults into the pod (Impact C) or a pod-level limit (T52 option C) is invisible to it.
  *Cost:* M - the field and its doc comment, the render, `make generate-all` (CRD, DeepCopy for
  the new `Quantity`, the chart's CRD copy), the README reference row, an ADR, unit tests, the
  e2e. *Consequences:* the CR author picks the number, including the headroom for forks and
  buffers; `-OOM` refusals replace kills only where someone set it; a later lowered memory
  limit can leave `maxMemory` above it, the kill returns silently unless the CEL rule refuses
  that edit.
- **B - an opt-in fraction of the memory limit (`spec.maxMemoryPercent`, no default).**
  `maxmemory` = the percentage of `spec.resources.limits.memory`, follows the limit when it is
  resized. *Cost:* as A plus a rule for a CR with a percentage and no limit (refuse in CEL or
  render nothing and report), and a second input to the config render that reads the resources
  block. *Consequences:* the headroom is still the author's guess, hidden in a percentage;
  resizing the limit already rolls the tier through the pod-spec hash, so following it buys
  one CR edit, not a roll; setting it discards the dataset of a `spec.replicas: 1` pod without
  persistence exactly as A does; if T52's option C (a pod-level memory limit) were chosen, B
  would have to read that too.
- **C - a derived default as a defect repair: whenever a memory limit is set, render
  `maxmemory` at a fixed fraction of it.** *Cost:* S in code, plus an ADR amending ADR 0005 D1's
  defect line and a release note. *Consequences:* on upgrade it rolls the data tier of every
  cluster with a memory limit and the Sentinel tier of every such Sentinel cluster, with no CR
  edit; it changes write semantics fleet-wide, so a cluster whose dataset sits above the chosen
  fraction today refuses writes right after its roll; the fraction is a guess the operator makes
  for every workload (fork copy-on-write in persistent modes, full-sync buffers, fragmentation -
  none measured in a pod); and the config hash moving on upgrade replaces the only pod of every
  `spec.replicas: 1` data cluster without persistence that has a memory limit, discarding its
  dataset, because `singlePodDeferral` defers no configuration change
  ([`pod_security_migration.go:135-145`](../../internal/controller/pod_security_migration.go)) -
  ADR 0032 D3 relies on "the operator upgrade alone never moves" the configuration, and C would
  make that false. *(Added in the review.)* The defect judgement is doubtful: `noeviction`
  without `maxmemory` is Valkey's own default, not something the operator broke, and ADR 0005 D1
  records that the feature/defect line is a judgement.

**A is recommended** because it is the only option that keeps every existing cluster exactly as
it is on upgrade while giving an author who knows the dataset a bound, and because the value is
what Valkey takes and what `INFO memory` shows, so an operator reading the pod sees the number
they wrote. It beats the runner-up B because B's only gain, following a resized limit, saves
one CR edit on an action that rolls the tier anyway, and it costs a no-limit rule and a coupling
to whatever form the memory limit takes. B's strongest case, argued in the review, is drift: a
lowered memory limit leaves A's absolute value above it and the kill returns, while B follows.
It does not overturn the mark, because A's optional CEL rule refuses exactly that edit within
the CR, and B follows only `spec.resources` too - a `LimitRange` or pod-level limit is as
invisible to B's render as to A's rule. If the CEL rule is dropped under decision 1 (its cost
budget fails in envtest), that case for B gets stronger and the mark should be re-weighed. C
loses on ADR 0005 D1: a fleet-wide roll and a change of write semantics without a CR edit, on a
fraction nobody has measured, and the loss of every non-persistent single pod's dataset on
upgrade.

### Decision 2 - whether the policy becomes a field too

Today the policy is fixed at `noeviction` ([`configmap.go:129`](../../internal/builder/configmap.go)).
With a `maxmemory`, `noeviction` refuses writes (M2) and an eviction policy drops keys (M3).

- **P1 - keep `noeviction` fixed; only `maxmemory` is new (recommended).** *Cost:* none beyond
  decision 1. *Consequences:* a cache user who wants eviction does not get it yet.
- **P2 - an opt-in enum `spec.maxMemoryPolicy` over the eight upstream policies,
  `noeviction` when unset.** *Cost:* S on top of decision 1 - the enum, the render, the README
  row, a unit test per branch. *Consequences:* an eviction policy deletes data silently by
  design, on a store the operator otherwise treats as the only copy (T36, ADR 0028); under an
  eviction policy with replicas attached, upstream advises a lower `maxmemory` for the replica
  output buffers, which it says `noeviction` does not need (valkey.conf 9.1.1:1315-1324), so the
  sizing advice splits by policy; an unset field renders `noeviction` as today, so it is
  upgrade-neutral.

**P1 is recommended** because nothing in the repository asks for eviction (no CR example, ADR
or ticket; whether a production user wants it is not verified), and deferring P2 costs nothing:
added later with `noeviction` as the unset value, it renders the same config byte for byte, so
it rolls nothing then either. It beats P2 because P2 adds a data-deleting mode to an operator
whose rules (ADR 0028) exist to stop the only dataset being discarded, for a use case nobody
has named.

## Decision

Not decided.

## Work list

- **Needs no decision:** document in [docs/operations/](../operations/README.md) what the memory
  limit does today - `noeviction` without `maxmemory`, an `OOMKilled` at
  `spec.resources.limits.memory`, no refusal and no log line first - next to the paragraph
  "Without persistence, a restarted master can empty its replicas" in
  [persistence.md](../operations/persistence.md), which names the
  `OOMKilled` trigger but not why nothing refuses first, and in
  [compute-resources.md](../operations/compute-resources.md), the page on memory limits, which
  does not mention `maxmemory` either; include that a namespace `LimitRange` can set the limit
  (Impact C). Cites no ticket (ADR 0034). Outside `docs/tickets/`, so not done in the
  2026-09-27 ticket run.
- ~~**Needs no decision:** point T36's Fact bullet "`maxmemory` is never set" and its Work list
  item, and T52's option C note on the removed T36 option, at this ticket (the host update of
  the same run).~~ *(Done 2026-09-27 by the host updates of T36 and T52; checked in the sweep.)*
- **Waits on decision 1 and 2:** the field(s) in [`api/v1/valkey_types.go`](../../api/v1/valkey_types.go),
  the render in the memory block of [`configmap.go`](../../internal/builder/configmap.go) for
  both data configs, `make generate-all` (CRD, DeepCopy, the chart CRD copy
  `deploy/helm/valkey-operator/templates/crd.yaml`), the README CRD reference row with the
  single non-persistent pod's data loss on setting the field (option A), a new ADR (the field,
  its default off, why no derived default, why no eviction policy yet), the tests under
  Verification.
- **Closing (ADR 0034):** the decision into the ADR, the field into the README reference, the
  sizing advice into `docs/operations/`, then `state: done`, a "what shipped" History line,
  `git grep` for T71 and this file name, and the move to `archive/`.

## Verification

- [ ] **Upgrade neutrality (unit):** a CR without the field renders a config with no
  `maxmemory` line, and `ComputeConfigHash` of a fixed CR equals the value computed at
  `84a39c2` (a pinned constant in the test). Mutation: render `maxmemory 0` unconditionally -
  the test must go red (the hash moves), which is exactly the fleet-wide roll D1 forbids;
  revert, green.
- [ ] **Render (unit):** a CR with `maxMemory: 96Mi` renders `maxmemory 100663296` in the master
  and in the replica config. Mutation: render it in the master config only - the replica case
  goes red; revert, green. If P2 is chosen, one case per policy branch.
- [ ] **Zero value (unit, or integration if refused by CEL):** `maxMemory: 0` renders no
  `maxmemory` line or is refused at admission, never `maxmemory 0` presented as a bound.
- [ ] **Validation (integration, envtest 1.29):** if the CEL rule is chosen, a value not below
  `spec.resources.limits.memory` is refused and a lower one admitted; the rule's cost is
  accepted by the API server.
- [ ] **E2E, both Valkey lines:** a three-replica cluster with a memory limit and a lower
  `maxMemory`; write values until the master answers `-OOM`; assert the master pod's
  `restartCount` did not change, no `OOMKilled` in its last state, reads still served, and
  every replica's `DBSIZE` equals the master's (ADR 0017: the e2e writes values and verifies
  replication). A negative control without `maxMemory` is not run in e2e: it would kill a
  master by design and is covered by M1.
- [ ] **Measured on Kind before the decision is closed:** the headroom a pod needs (a full sync
  and, in mode `rdb`, a `BGSAVE` under the limit with `maxmemory` set), which decides the
  sizing advice the docs give.

## History

- 2026-09-27: filed from T36 (its Fact bullet "`maxmemory` is never set; the memory limit is
  the backstop", its Work list item asking for this file, and the removed option "`maxmemory`
  derived from the memory limit" with its row: "removes only the OOM trigger and is a capacity
  decision of its own"; "under `noeviction` a `maxmemory` turns an OOM kill into refused
  writes, a behaviour change a cache user may want anyway") and from T52 option C's note on
  that removed option, during the re-verification at `84a39c2`. **Moved** from the
  re-verification records of T36: the absence of `maxmemory` at `configmap.go:126-135`, the
  exporter's `ExtraArgs` as the only CRD pass-through, and the correction C31 of that run - a
  `maxmemory` line rolls the Sentinel tier too, not only the data tier ("every data tier with a
  memory limit" was false; the shared hash of `configmap.go:293-305`, stamped at
  `statefulset.go:153` and `sentinel.go:234`, compared at `rolling_update.go:4864-4866`).
  **Re-verified now, by reading at `84a39c2`:** all of that, plus the `spec.resources`
  pass-through without a default, the `PING` probes, the observer's ~~`MULTI`~~ write test
  *(false, corrected in the review below)* and its
  unready default, and that no ADR, operations page or chart alert names `maxmemory`; nothing
  found false. **Measured now (docker, 9.1.1 and 8.1.9, commands and results under Fact):** M1
  the kill at the limit with no refusal and an empty restart, and Valkey reporting the host's
  memory under a cgroup limit; M2 the `-OOM` refusal at `maxmemory` with the pod alive and
  reads served; M3 eviction under `allkeys-lru`; M4 master plus replica, the replica at its own
  limit without `maxmemory` and bounded by the master's refusal with it; M5 the observer-shaped
  `MULTI` aborting at `maxmemory`. These were never measured before; T36 carried the kill as a
  statement. **Checked upstream:** valkey.conf at 9.1.1 and 8.1.9 (memory section), the
  Kubernetes CEL quantity library (1.29+), the QoS `oom_score_adj` table. **Classified:**
  severity medium, security none (reasoning under Impact), urgency `next` by rule 3 with rules
  1 and 2 checked, effort M, state `analysed`, `blocked-by: decision`. **Options:** decisions 1
  (A recommended over B and C) and 2 (P1 recommended over P2); the generic config pass-through
  recorded as not carried. **Adversarial review, same day, at `84a39c2`:** found false and
  corrected in place - the observer sends no `MULTI` (`ExecMulti`, `client.go:330-359`, sends
  each command plainly; the `SET`'s `-OOM` becomes an error at `client.go:479-481`, so the
  unready conclusion holds; M5 annotated). Re-measured M1, M2 (both pins) and M5 (9.1.1),
  results under Fact, nothing else false. Re-fetched valkey.conf at both tags, every cited line
  matches; added config.c 9.1.1:3473 (`maxmemory` default 0). Added: the two ConfigMaps from one
  generator; the observer's `SET` as the only data write of any operator component; the
  `LimitRange` path to a memory limit (Impact C, from T52); option A's data loss on a
  `spec.replicas: 1` non-persistent pod, its zero-value rule, the CEL rule's blindness to
  `LimitRange` and pod-level limits, `make generate-all`; option C's loss of every such pod's
  dataset on upgrade against ADR 0032 D3; B's drift case argued, the mark on A kept with the
  condition under which it is re-weighed; P2's upstream headroom note; two further not-carried
  alternatives (runtime `CONFIG SET`, documentation only); the zero-value test;
  compute-resources.md as a second documentation home. Classification rechecked and unchanged:
  severity medium, security none, urgency `next` by rule 3 (rule 1 checked: the false `MULTI`
  claim sat only in this new, not yet committed ticket and was fixed here; no tracked page
  states it), effort M.
  Sweep: The Work list item that asked T36 and T52 to point here is struck as done: both now link
  this file. Frontmatter unchanged.
  Final pass: the `ExecMulti` name and doc-comment defect, noted under Fact as outside this
  ticket's chain, now points at [T70](070-tracked-comments-and-adrs-state-what-the-code-contradicts.md)
  item (j), checked against T70's current text and against
  [`client.go:330-359`](../../internal/valkeyclient/client.go) at `84a39c2` (the loop returns on
  the first error); the T36 and T52 pointers re-checked, both still link this file. Frontmatter
  unchanged.
