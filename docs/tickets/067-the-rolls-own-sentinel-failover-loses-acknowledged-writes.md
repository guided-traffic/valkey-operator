---
id: T67
title: the roll's own Sentinel failover loses the writes the outgoing master acknowledges after the promotion
state: analysed       # filed and analysed the same day: the loss is measured in docker on both pinned lines, every option is costed and one is marked (History 2026-09-27)
severity: high        # silent loss of acknowledged writes on every Sentinel roll - image, config, an operator upgrade that changes the pod spec, and every TLS rotation roll (ADR 0030 D4) - 8633 to 11249 per forced failover in docker at about 500-600 writes/s; not critical because it is bounded to the seconds between the promotion and the conversion and the pre-roll dataset survives; the Kubernetes size is not measured (Fact, Impact)
security: none        # durability; no principal gains anything and no guard is weakened; T12 and T36 classify the same kind of loss none
urgency: now          # rule 1, second clause, first match top-down: fifteen tracked places (twelve statements, three test comments) state that every multi-replica roll is lossless, measured false in docker for the writes of a Sentinel roll's failover (Work list, XS); the first clause does not match, the forced trigger is released since v1.0.0; back to next by rule 3 once the XS item lands (History 2026-09-27)
effort: M             # the recommended option A: a coordinated client call with its fallback, the forced retrigger, unit tests with revert checks, the Kind e2e subtest on both legs, an ADR amending ADR 0025 D9 and ADR 0007; the XS text item on top
blocked-by: decision
filed-from: T12 (its "Separate finding" and Decision 2), re-verification of 2026-09-27 at 84a39c2
opened: 2026-09-27
decided:
done:
---

# T67 - the roll's own Sentinel failover loses the writes the outgoing master acknowledges after the promotion

Filed on 2026-09-27 from [T12](012-no-write-fencing-min-replicas-to-write-as-an-opt-in-field.md),
where the re-verification at `84a39c2` recorded it as a separate finding with its own Decision 2.
Its mechanism is the roll's failover command, not `min-replicas-to-write`, and its decision amends
ADR 0025 D9 and ADR 0007, so under the filing rule (same mechanism, same decision) it is a ticket
of its own. Everything T12 held about it is moved here; T12 keeps a pointer.

## Fact

### A. The forced failover leaves the outgoing master writable for about 16 s

During a Sentinel roll, `handleMasterFailover`
([rolling_update.go:2695-2758](../../internal/controller/rolling_update.go#L2695-L2758)) waits for
the replicas (`waitForReplicasReady`, `:2711`), sends `WAIT` to the master (`waitForWriteSync`,
`:2717`), stamps the failover state and its timestamp in one update (`setFailoverTriggered`,
`:2743`), triggers the failover (`:2751`) and requeues after 15 s (`:2757`). The retrigger after a
reset does the same in `handleFailoverRetrigger`
([rolling_update.go:849-887](../../internal/controller/rolling_update.go#L849-L887): state `:878`,
trigger `:882`, requeue `:886`). Both sites call `triggerSentinelFailover`
([rolling_update.go:3700-3740](../../internal/controller/rolling_update.go#L3700-L3740)), which tries
each Sentinel in turn (`:3711-3737`) and sends a plain `SENTINEL FAILOVER <name>` through
`SentinelFailover`
([valkeyclient/client.go:231-238](../../internal/valkeyclient/client.go#L231-L238)).

Sentinel executes the plain command as a **forced** failover: it sets `SRI_FORCE_FAILOVER`, skips
the leader election and promotes a replica with `REPLICAOF NO ONE` behind the old master's back
([sentinel.c 9.1.1:3957-3960](https://github.com/valkey-io/valkey/blob/9.1.1/src/sentinel.c#L3957-L3960)).
Nothing tells the old master. It keeps answering `role:master` and acknowledging writes until
Sentinel converts it with its `REPLICAOF` transaction, which also kills its normal and pubsub
clients ([sentinel.c 9.1.1:4868-4935](https://github.com/valkey-io/valkey/blob/9.1.1/src/sentinel.c#L4868-L4935)).
Sentinel does that only after the old master has reported `role:master` for
`sentinel_publish_period * 4` = 8 s while its table lists it as a replica
([sentinel.c 9.1.1:2630-2641](https://github.com/valkey-io/valkey/blob/9.1.1/src/sentinel.c#L2630-L2641),
the period at `:89`). Measured in docker: `+switch-master` 6.2-6.4 s and `+convert-to-slave`
16.3-16.4 s after `+promoted-slave` on both pins. Every write the old master acknowledges in that
time is discarded when it resyncs from the new master.

On the operator side nothing shortens it. The outgoing master keeps its `instanceRole=master`
label, and with it the `-rw` endpoint, until the Sentinel its labeler asks stops naming it (the
cross-check, [sidecar/labeler.go:135-145](../../internal/sidecar/labeler.go#L135-L145), reads
`SENTINEL MASTER` from the first Sentinel that answers,
[:350-363](../../internal/sidecar/labeler.go#L350-L363)). The labeler and
`triggerSentinelFailover` both walk the Sentinels in ordinal order
([builder/statefulset.go:1168-1188](../../internal/builder/statefulset.go#L1168-L1188),
[rolling_update.go:3711](../../internal/controller/rolling_update.go#L3711)), so the Sentinel asked is normally the failover's leader, whose answer moves only at its own
`+switch-master`: 6.2-6.4 s after the promotion in the docker runs below, 1.07-5.38 s in T35's
measurements (its decision 8). Pooled client connections stay on the old master until the
conversion kills them. The operator replaces it only through `handlePostFailover`
([rolling_update.go:3071](../../internal/controller/rolling_update.go#L3071)) →
`handleNewMasterFound` ([:3119-3129](../../internal/controller/rolling_update.go#L3119-L3129)) →
`replaceRemainingPods` ([:2984-3051](../../internal/controller/rolling_update.go#L2984-L3051)), not
before the next pass and not before `verifyNewMasterReady`
([:3331-3397](../../internal/controller/rolling_update.go#L3331-L3397)) sees a new master with a
connected replica - in any sync state, because its `MasterSyncInProgress` check reads a field a
master never reports ([T69](069-three-sync-checks-read-a-replica-field-from-the-master.md)). The
next pass is the 15 s requeue, or earlier: the state dispatch sends every pass in
`failover-triggered` straight to `handlePostFailover`
([:746-747](../../internal/controller/rolling_update.go#L746-L747)), and any event of an owned
object or a referenced Secret enqueues one
([valkey_controller.go:2984-3001](../../internal/controller/valkey_controller.go#L2984-L3001)); the
15 s is a requeue, not a floor. `WAIT` covers only the writes acknowledged before it; it blocks
only the connection that sent it.

[ADR 0025](../adr/0025-a-split-brain-warning-means-one-that-did-not-resolve-itself.md) D9 (`:188`)
accepts this loss in its Consequences (`:285-295`: "Writes that reach the old master after the
promotion are lost when Sentinel reconfigures it") and records no size for it; its "read, not
measured" at that place qualifies the 90 s bound of the window, not the loss. No e2e measures it:
`TestE2E_RollingUpdate_HA_NoDataLoss`
([test/e2e/rolling_update_test.go:489](../../test/e2e/rolling_update_test.go#L489)) writes its 100
keys before the roll and runs no writer during it, and the TLS rotation e2e checks that the
dataset survived ([test/e2e/tls_rotation_test.go:217-219](../../test/e2e/tls_rotation_test.go#L217-L219)).

**Verified:** the code path above, read at `84a39c2`; the forced path, the conversion delay and the
`REPLICAOF` transaction in upstream `sentinel.c` at tag 9.1.1 (fetched 2026-09-27, byte-identical
to the copy the T12 review read); the loss in docker on both pins, twice independently in the T12
re-verification and once more for this filing (Measurements): 9568, 9553 and 9133 (9.1.1), 9510
and 8633 (8.1.9) acknowledged writes lost per forced failover, 11249 of 13271 in a second
reviewer's run; in the run for this filing the old master still answered `role:master` 16 s after
the trigger on both pins. The write rate: about 600 writes/s in T12's runs, about 500 in this
filing's (5638 and 5284 acknowledged between +5 s and +16 s on 9.1.1 and 8.1.9). The Sentinel
order and the dispatch above, read at `84a39c2`.

**Not verified:** the size on Kubernetes. It depends on the client write rate, on how long `-rw`
keeps routing to the old master and on how long pooled connections stay on it; the docker writer
opens a new connection to the old master for every write, so connection reuse is not modelled.
Whether a client that confirms its writes with `WAIT` would see `0` for them (the old master's
replicas are redirected at the promotion) is inferred, not measured.

### B. Appendix, same mechanism: the dying outgoing master can force a second failover

*(Found 2026-09-27 while re-verifying A; not in T12.)* `verifyNewMasterReady` checks the new
master only; nothing asks the outgoing master whether it is a replica yet before
`replaceRemainingPods` deletes it
([rolling_update.go:3034](../../internal/controller/rolling_update.go#L3034)). The comment above
that delete says "the former master (now a replica after failover)", which on the forced path
holds only after `+convert-to-slave`. The delete SIGTERMs the pod's sidecar, whose drain handler
([sidecar/drain.go:98-131](../../internal/sidecar/drain.go#L98-L131)) reads the local role; if it
is still `master`, it labels the pod `draining` and sends the same plain `SENTINEL FAILOVER <name>`
to the first Sentinel that accepts it
([sidecar/drain.go:135-148](../../internal/sidecar/drain.go#L135-L148)). By then Sentinel's monitor
names the new master, so that command fails over the pod the roll has just promoted.

**Verified:** by reading at `84a39c2`; in docker on 9.1.1, one run (Measurements): a second forced
`SENTINEL FAILOVER mm` sent 14 s after the first, while the old master still answered `master`,
was answered `OK`; Sentinel selected the other replica, promoted it 1.14 s after the command,
moved its pointer away from the pod it had promoted 13 s before, and converted that pod 14.2 s after the
second promotion. The second failover is forced as well, so it opens a second window of A on the
freshly promoted pod.

**Not verified:** whether on Kubernetes the operator's delete precedes the conversion. By timing it
is the expected order, not a corner case: the promotion came about 1 s after the trigger and the
conversion about 16.3-16.4 s after the promotion (docker), so about 17.4 s after the trigger,
while the requeued post-failover pass comes 15 s after it, earlier on any watch event (Fact A),
and `verifyNewMasterReady` passes as soon as the new master counts one connected replica, in any
sync state ([T69](069-three-sync-checks-read-a-replica-field-from-the-master.md)). Also not
verified: whether the sidecar's role read wins the race against the Valkey container's own
SIGTERM - a Sentinel cluster renders no `preStop` hook
([builder/statefulset.go:746-749](../../internal/builder/statefulset.go#L746-L749)), and if the
local Valkey is already gone the handler exits at "failed to detect role"
([sidecar/drain.go:106-110](../../internal/sidecar/drain.go#L106-L110)); and which replica the
second failover picks and what it holds at that moment (in the docker run it had resynced). Not
measured on Kind, and not measured on 8.1.9. What the rolling-update split-brain resolver does
with the double master of the second failover (the roll state is then `replacing-master`, outside
ADR 0025 D9's guard) is not examined.

### Related, not this mechanism

On the non-Sentinel path `promoteAndRedirect` demotes the outgoing master in its second call; the
promote-to-demote gap lost 1 (9.1.1) and 2 (8.1.9) acknowledged writes at about 250 writes/s in
T12's docker run. A fence would not help there, because the other replica is still attached in
that gap. The comments that call the outgoing master "drained of writes" and say `WAIT` "prevents
data loss" are corrected by
[T70](070-tracked-comments-and-adrs-state-what-the-code-contradicts.md) item (b), which also
covers ADR 0012 D9's "It loses no data". Automatic failovers after a crash are not this finding:
there the old master is not alive to acknowledge anything.

## Measurements

All runs on the local `valkey/valkey:9.1.1` and `valkey/valkey:8.1.9` with `docker run --rm`, one
network per run; the T12 runs used containers named `vko-verify-012-*`, the runs for this filing
`vko-file-067-*`, all removed afterwards (checked with `docker ps -a` and `docker network ls`).
Topology in every run: master `m`, replicas `r1`, `r2` (`--repl-diskless-sync yes
--repl-diskless-sync-delay 5 --save '' --appendonly no`, the operator's diskless settings), one
`valkey-sentinel` (`resolve-hostnames yes`, `announce-hostnames yes`, quorum 1,
`down-after-milliseconds 5000`, `failover-timeout 60000`, `parallel-syncs 1`, the operator's values
at [builder/sentinel.go:157-159](../../internal/builder/sentinel.go#L157-L159)); a writer loop
inside `m` (`valkey-cli SET load:<i> x`, one connection per write) logs every acknowledged index;
after two online replicas and a 10 s settle, `WAIT 2 1000`, then the trigger; afterwards every
acknowledged index is checked with `EXISTS load:<i>` on the new master. The script of this filing
is below; the T12 scripts differ only in the trigger and in the fence variants.

| Run | Result |
|---|---|
| T12, forced `SENTINEL FAILOVER mm`, no fence | 9.1.1: 9568 and 9553 lost (a second reviewer: 11249 of 13271); 8.1.9: 9510. `+promoted-slave` to `+failover-end` about 5-6 s, `+switch-master` about 6.4 s and `+convert-to-slave` about 15-16 s after the promotion |
| T12, forced, `--min-replicas-to-write 1 --min-replicas-max-lag 10` on every node | 9.1.1: 595 lost (second reviewer 556, new master first write +6.55 s); 8.1.9: 596 |
| T12, forced, runtime fence on `m` only (`CONFIG SET min-replicas-to-write 1`, `min-replicas-max-lag 10` just before the trigger) | 9.1.1: 529 lost (second reviewer 512), new master first write +0.48 s (+0.42 s); 8.1.9: 542 (516), +0.48 s (+0.50 s) |
| T12, `SENTINEL FAILOVER mm COORDINATED` | 9.1.1, one Sentinel: 0 lost in five runs (acknowledged 1289, 1310, 1419, 2100, 1864), new master first write +0.72, +0.72, +1.01, +1.39, +1.37 s; three Sentinels at quorum 2 (`+vote-for-leader`, `+elected-leader`): 0 lost, one run; fenced on every node: 0 of 2047 lost, first write +0.62 s. `+switch-master` / `+failover-end` about 1.1 s after `+promoted-slave`; the old master answered `role:slave` at once; a `SET` to it 3 s after the trigger got `READONLY` at once, no pause hang; `m`, `r1` and `r2` each logged an accepted partial resynchronization. 8.1.9: `ERR wrong number of arguments for 'sentinel\|failover' command` |
| This filing, forced, no fence | 9.1.1: 9133 of 10368 acknowledged lost; `m` `role:master` at +5, +10, +13 and +16 s after the trigger, `role:slave` at +20 s; `+promoted-slave` +1.03 s after the trigger, `+switch-master` +6.28 s and `+convert-to-slave` +16.43 s after the promotion. 8.1.9: 8633 of 9799 lost; `role:master` up to +16 s; `+switch-master` +6.19 s, `+convert-to-slave` +16.28 s after the promotion |
| This filing, `COORDINATED` | 9.1.1: 0 of 1118 lost; `m` `role:slave` from the first sample (+5 s) on; `+elected-leader` 58 ms after the trigger, `+promoted-slave` +2.08 s, `+failover-end` 1.10 s after it. 8.1.9: the command answered `ERR wrong number of arguments for 'sentinel\|failover' command` and nothing failed over (0 lost, `m` still master) |
| This filing, part B: a second forced `SENTINEL FAILOVER mm` 14 s after the first, then `docker stop -t 10 m` | 9.1.1, one run: at +14 s `m` master, `r2` master (promoted), `r1` slave, Sentinel naming `r2`; the second command answered `OK`, Sentinel selected `r1` 0.14 s and `+promoted-slave` 1.14 s after the command, `+switch-master` from `r2` to `r1`, `r2` `+convert-to-slave` 14.2 s after the second promotion; final `r1` master, `r2` slave |

<details>
<summary>The script of this filing (<code>loss.sh &lt;ver&gt; &lt;forced|coord&gt;</code>)</summary>

```bash
#!/bin/bash
# Acknowledged writes lost by the outgoing master in a Sentinel failover.
# usage: loss.sh <ver> <forced|coord>
ver=${1:-9.1.1}; mode=${2:-forced}; tag=${ver//./}-$mode
p=vko-file-067-$tag; net=$p-net
docker network create $net >/dev/null
common="--repl-diskless-sync yes --repl-diskless-sync-delay 5 --save '' --appendonly no"
docker run -d --rm --name $p-m --network $net valkey/valkey:$ver sh -c "exec valkey-server $common --replica-announce-ip $p-m" >/dev/null
sleep 1
for n in r1 r2; do docker run -d --rm --name $p-$n --network $net valkey/valkey:$ver sh -c "exec valkey-server $common --replica-announce-ip $p-$n --replicaof $p-m 6379" >/dev/null; done
docker run -d --rm --name $p-s --network $net valkey/valkey:$ver sh -c "mkdir -p /tmp/s && printf 'port 26379\ndir /tmp/s\nsentinel resolve-hostnames yes\nsentinel announce-hostnames yes\nsentinel monitor mm $p-m 6379 1\nsentinel down-after-milliseconds mm 5000\nsentinel failover-timeout mm 60000\nsentinel parallel-syncs mm 1\n' > /tmp/s/s.conf && exec valkey-sentinel /tmp/s/s.conf" >/dev/null
cli() { docker exec $p-$1 valkey-cli --raw "${@:2}" 2>/dev/null; }
for i in $(seq 1 60); do c=$(cli m INFO replication | tr -d '\r' | grep -c "state=online"); ns=$(cli s -p 26379 SENTINEL REPLICAS mm | grep -c "^name$"); [ "$c" = "2" ] && [ "$ns" = "2" ] && break; sleep 0.5; done
sleep 10
docker exec -d $p-m sh -c 'i=0; : > /tmp/acked; while [ $i -lt 100000 ]; do i=$((i+1)); r=$(valkey-cli SET load:$i x 2>/dev/null); [ "$r" = "OK" ] && echo $i >> /tmp/acked; done'
sleep 2
echo "WAIT: $(cli m WAIT 2 1000)"
if [ "$mode" = "coord" ]; then echo "trigger: $(cli s -p 26379 SENTINEL FAILOVER mm COORDINATED)"; else echo "trigger: $(cli s -p 26379 SENTINEL FAILOVER mm)"; fi
t0=$(python3 -c "import time;print(time.time())")
for k in 5 10 13 16 20; do
  while [ "$(python3 -c "import time;print(int(time.time()-$t0))")" -lt $k ]; do sleep 0.2; done
  echo "t+${k}s old master $(cli m INFO replication | tr -d '\r' | grep '^role:'), acked so far $(docker exec $p-m sh -c 'wc -l < /tmp/acked' | tr -d ' ')"
done
new=$(cli s -p 26379 SENTINEL get-master-addr-by-name mm | head -1 | sed "s/$p-//")
docker exec $p-m sh -c 'pkill -f "valkey-cli SET" ; true' >/dev/null 2>&1
sleep 1
acked=$(docker exec $p-m sh -c 'wc -l < /tmp/acked' | tr -d ' ')
missing=$(docker exec $p-m cat /tmp/acked | docker exec -i $p-$new sh -c 'while read i; do echo "EXISTS load:$i"; done | valkey-cli --raw' | grep -c "^0$")
echo "[$ver $mode] new master $new; acked by old master: $acked; acked but missing on the new master: $missing"
docker logs $p-s 2>&1 | grep -E "user requested|elected-leader|\+promoted-slave|\+switch-master|\+convert-to-slave|\+failover-end|abort" | sed -E 's/^[0-9]+:X [0-9]+ [A-Za-z]+ [0-9]+ //' | cut -c1-70
docker rm -f $p-m $p-r1 $p-r2 $p-s >/dev/null; docker network rm $net >/dev/null
```

The `pkill` line is a no-op: neither image ships `pkill` (checked 2026-09-27 with `command -v
pkill` in both), so the writer runs on. It does not change the counts: after the conversion its
`SET`s answer `READONLY` and are not logged, and the final acknowledged counts equal the +20 s
samples (10368 and 9799); in the 8.1.9 `COORDINATED` run, where nothing failed over, the keys are
checked on `m` itself. The part B run is the same topology with a writer that does not log, the first trigger, `sleep 14`,
`ROLE` on every node, a second plain `SENTINEL FAILOVER mm`, `docker stop -t 10` on `m`, `sleep 25`,
then `ROLE` and the Sentinel log. The T12 fence variants add `--min-replicas-to-write 1
--min-replicas-max-lag 10` to every node (fence 1) or the two `CONFIG SET` calls on `m` right after
`WAIT` (fence 2).

</details>

## Impact

**Who and when.** Every client that writes to a Sentinel-enabled cluster while its data tier rolls:
an image change, a config change, an operator upgrade that changes the pod spec (ADR 0005 D11; the
rootless release of ADR 0032 rolled every data tier), enabling metrics or anti-affinity, and every
TLS rotation roll under
[ADR 0030](../adr/0030-rotating-certificates-rotate-the-instances-that-cannot-reload-them.md) D4
(`:159`), which a cert-manager renewal starts without anybody asking. Each roll fails over once
([ADR 0007](../adr/0007-failover-aware-rolling-update.md) D1, `:166`), each failover is forced,
and each discards what the outgoing master acknowledges from the promotion until its conversion or
its delete. Clients see `OK`. Nothing the operator reports names it: the roll completes, `Ready`
stays `True`, the one Event is the Normal `FailoverTriggered`
([rolling_update.go:2748-2749](../../internal/controller/rolling_update.go#L2748-L2749)), which names
the failover and not the loss, and ADR 0025 D9 deliberately reports the double master without
acting on it.

**What breaks.** Acknowledged writes, silently, up to about 16 s of them per roll in docker (the
Kubernetes size is not measured), on the whole Sentinel fleet, with no opt-out. The dataset that existed before the roll survives. With part B,
when it happens, a second forced failover follows on the freshly promoted pod and opens a second
window of the same kind.

**What it contradicts.** Fifteen tracked places state that a multi-replica roll loses nothing,
twelve as statements:
[README.md:46](../../README.md#L46) ("without data loss"), [CLAUDE.md:1068](../../CLAUDE.md#L1068)
("lossless except for a single standalone pod without persistence"),
[ADR 0005:357](../adr/0005-upgrade-neutral-defaults-and-anti-affinity.md#L357),
[ADR 0018:100](../adr/0018-metrics-and-the-exporter-sidecar.md#L100) (D7) and
[:179](../adr/0018-metrics-and-the-exporter-sidecar.md#L179),
[ADR 0032:366](../adr/0032-generated-pods-run-rootless.md#L366) ("lossless like every roll"),
[docs/operations/anti-affinity.md:16](../operations/anti-affinity.md#L16) and
[:39](../operations/anti-affinity.md#L39),
[docs/operations/monitoring.md:31](../operations/monitoring.md#L31) ("no data is lost even without
persistence"), [docs/operations/upgrading.md:52](../operations/upgrading.md#L52) ("lossless, like
every roll") and [:161](../operations/upgrading.md#L161), and the comment at
[test/e2e/tls_rotation_test.go:219](../../test/e2e/tls_rotation_test.go#L219) ("A failover-aware
roll is lossless by design"). Three more are test comments that describe what their test proves
in the same words:
[test/e2e/fleet_upgrade_test.go:22-23](../../test/e2e/fleet_upgrade_test.go#L22-L23) ("No data is
lost while the failover-aware rolling update replaces every data pod"),
[test/e2e/rolling_update_test.go:489-490](../../test/e2e/rolling_update_test.go#L489-L490) ("zero
data loss during a rolling update of an HA cluster") and
[test/testimages/images.go:76-78](../../test/testimages/images.go#L76-L78) (the 8 to 9 upgrade
path "loses no data"). Each is true of the pre-roll dataset and measured false for writes
during a Sentinel roll's failover; a reader takes them as the second. Not in the list: the
page-specific measurements that say "losslessly" about a dataset
([docs/operations/persistence.md:77](../operations/persistence.md#L77), `:111`, ADR 0023), ADR 0012
D9's "It loses no data" (`:340`, the non-Sentinel path, T70 item (b)), and ADR 0016's heading
"Automatic password propagation without data loss" (`:261`) and
[docs/security/rotation-and-change-propagation.md:47](../security/rotation-and-change-propagation.md#L47),
which name a product wish, not a roll (found with `git grep -n -i 'lossless\|without data
loss\|no data is lost\|loses no data\|zero data loss'` at `84a39c2`).

**Why high and not medium or critical.** Medium would fit an opt-in feature or a rare trigger;
this is the operator's own action on every Sentinel roll, fleet-wide, silent, and it discards
writes the client was told were stored, the same class as T36, which is high. Critical would fit a
loss of the dataset or an outage; this is bounded to seconds of writes per roll.

**Security: none.** The scale is threat-model based: no principal gains anything, no guard is
weakened, and the lossless statements above are durability statements, not security guarantees;
no page under `docs/security/` states a durability guarantee (the grep above finds only the
product wish there). T12, T36 and T69 classify the same kind of loss `none`.

## Options

**Mechanism.** Today the roll triggers a forced `SENTINEL FAILOVER <name>` at two sites
([rolling_update.go:2751, :882](../../internal/controller/rolling_update.go#L2751)) through
`triggerSentinelFailover` and `SentinelFailover`. Sentinel promotes a replica behind the old
master's back; the old master keeps acknowledging writes until Sentinel converts it (about 16 s
after the promotion in docker) or the operator deletes it (at the next post-failover pass that sees
a connected replica on the new master), and those writes are lost (Fact A). **What the choice
changes:** how the old master stops acknowledging writes that are later discarded. **What it does
not change:** automatic failovers after a crash, the non-Sentinel path, the drain handler's own
command (below), the pod templates (no option rolls anything) and the CRD (no option adds a
field).

- **A (T12's 2a) - Coordinated failover on Valkey 9, the forced trigger as fallback
  (recommended).** M. The first trigger (`:2751`) sends `SENTINEL FAILOVER <name> COORDINATED`.
  Sentinel then runs a leader election and sends the live old master `MULTI; CLIENT PAUSE <t>
  WRITE; FAILOVER TO <replica> TIMEOUT <t>; EXEC`
  ([sentinel.c 9.1.1:4753-4800](https://github.com/valkey-io/valkey/blob/9.1.1/src/sentinel.c#L4753-L4800),
  [:5139-5168](https://github.com/valkey-io/valkey/blob/9.1.1/src/sentinel.c#L5139-L5168)): the old
  master blocks writers, waits until the target has caught up, demotes itself, and only then is the
  target promoted, so there is no second master and no stream divergence. At the promotion Sentinel
  kills every normal and pubsub client of both pods and sends `CLIENT UNPAUSE`
  ([sentinel.c 9.1.1:2624-2627](https://github.com/valkey-io/valkey/blob/9.1.1/src/sentinel.c#L2624-L2627),
  `sentinelKillClients` [:4809-4855](https://github.com/valkey-io/valkey/blob/9.1.1/src/sentinel.c#L4809-L4855);
  read, not measured separately), so on success the pause ends at the promotion and clients
  reconnect, as they already do when the forced path's conversion kills them. Measured on 9.1.1:
  0 acknowledged writes lost in eight runs (six unfenced with one Sentinel, one fenced on every
  node with one Sentinel, one with three Sentinels at quorum 2), against 8633-11249 forced; new
  master writable at +0.62 to +1.4 s; every replica resynced partially; a late write to the old
  master got `READONLY` at once.
  It also removes part B on Valkey 9: the old master answers `role:slave` at once, so its drain
  handler exits at "not master" ([sidecar/drain.go:114-116](../../internal/sidecar/drain.go#L114-L116)).
  Implementation requirements, from the re-verification of this filing:
  1. **A call of its own, not a changed `SentinelFailover`.** The drain handler shares
     `SentinelFailover` through its `ValkeyCommander` interface
     ([sidecar/drain.go:27-32](../../internal/sidecar/drain.go#L27-L32), the call at `:139`). On a
     Sentinel cluster no `preStop` hook holds the Valkey container
     ([builder/statefulset.go:746-749](../../internal/builder/statefulset.go#L746-L749) renders it
     for multi-replica clusters without Sentinel only), so the drain's failover races the Valkey
     process's own SIGTERM, and a coordinated failover needs that process alive through
     `FAILOVER TO`. The drain keeps the forced command; its coordinated variant is not measured
     and not proposed.
  2. **The fallback on the reply.** A Valkey 8 Sentinel refuses the argument
     (`ERR wrong number of arguments for 'sentinel|failover' command`, measured; the 8.1.9
     `sentinel.c` checks `argc != 3`, `:3873`). A Valkey 9 Sentinel answers `-NOGOODPRIMARY
     Primary does not support FAILOVER command`
     ([sentinel.c 9.1.1:3931-3934](https://github.com/valkey-io/valkey/blob/9.1.1/src/sentinel.c#L3931-L3934))
     while it has not yet read `master_failover_state` from the master's `INFO` (the initial state,
     `:1365`, set from `INFO` at `:2569-2575`, which Sentinel reads every 10 s, `:86`): after a
     Sentinel restart, and after the operator's own `resetSentinelState`, whose `SENTINEL REMOVE`
     plus `MONITOR` creates the monitor anew
     ([rolling_update.go:3666-3679](../../internal/controller/rolling_update.go#L3666-L3679)); a
     plain `SENTINEL RESET` keeps the state (`sentinelResetPrimary`, `:1528`, does not touch it).
     On these two replies the same Sentinel is asked again with the forced command; `-INPROG` and
     `-NOGOODSLAVE` (`:3940-3946`) stay what they are today, a failed attempt on that Sentinel. The
     fallback also covers the 8→9 upgrade roll, because the data tier rolls before the Sentinel
     tier and the Sentinels still run Valkey 8 when the data failover fires.
  3. **The fallback on a bound.** Without a majority the coordinated command still answers `OK`
     (it only starts the election, `:3952-3956`), and the failover aborts
     `-failover-abort-not-elected` after the election timeout, min(10 s, `failover-timeout`)
     ([sentinel.c 9.1.1:5102-5111](https://github.com/valkey-io/valkey/blob/9.1.1/src/sentinel.c#L5102-L5111));
     the operator sees no error, only no promotion. Cases: `SentinelPeersStale`
     ([ADR 0022](../adr/0022-sentinel-identity-is-pinned-to-the-pod.md) D5), a tier of two with one
     Sentinel down ([ADR 0024](../adr/0024-the-sentinel-tier-reports-its-own-completion.md) D10). The
     bound that already exists catches it: with no promoted pod, `handleNoMasterFound`
     ([rolling_update.go:3287-3326](../../internal/controller/rolling_update.go#L3287-L3326)) resets
     after `failoverRetryTimeout` = 30 s ([:143](../../internal/controller/rolling_update.go#L143)),
     read from the timestamp `setFailoverTriggered` writes with the state (ADR 0010 D14), and
     `handleFailoverRetrigger` fires the retrigger at `:882`. That site keeps the forced command, so
     the fallback is armed by the timestamp every trigger already writes; the cost is roughly 30 s
     plus the retrigger's waits before the forced failover, with one master and nothing lost in
     between. That path passes through `resetSentinelState`, which is
     [T62](062-resetsentinelstate-falls-back-to-sentinel-reset.md)'s subject. `SENTINEL CKQUORUM
     <name>` (both lines; the command at
     [sentinel.c 9.1.1:4016-4040](https://github.com/valkey-io/valkey/blob/9.1.1/src/sentinel.c#L4016-L4040),
     the check `sentinelIsQuorumReachable` at
     [:3748-3767](https://github.com/valkey-io/valkey/blob/9.1.1/src/sentinel.c#L3748-L3767))
     reports whether the asked Sentinel counts a usable majority and could pick the forced command
     up front; read, not measured, and a point-in-time answer, so it can only shorten the bound,
     never replace it.

  Costs: `CLIENT PAUSE WRITE` lifts only on its timeout or `CLIENT UNPAUSE` (networking.c
  9.1.1:5501, :6366), so a stalled `FAILOVER TO` blocks writes to the old master for up to the
  remaining `failover-timeout` (60 s, [builder/sentinel.go:50](../../internal/builder/sentinel.go#L50);
  sentinel.c 9.1.1:5162; `sentinelAbortFailover`, `:5358-5369`, sends no `CLIENT UNPAUSE`), not
  measured; blocked writes are refused late, not lost. On success the old master blocks writers for
  the handover itself (at most `+elected-leader` to `+promoted-slave`, 2.02 s in this filing's run),
  and their connections are then killed, so they get an error, not an acknowledgement (read; the
  measured part is that no acknowledged write went missing). It needs an ADR
  amending ADR 0025 D9 (its accepted loss and its double-master window) and ADR 0007, and the e2e
  on both Valkey lines (ADR 0017: only e2e can show it). It sends no new command to a data pod,
  leaves no state to clear, touches no master-authority rule (Sentinel still arbitrates) and ships
  fleet-wide as the repair of the operator's own failover, not as an ADR 0005 D1 feature: ADR 0025
  D9 itself changed the roll without a field. It leaves Valkey 8 Sentinel clusters on today's loss,
  recorded with its measured size the C way.

  **Not verified for A** (carried from T12's list, still open): TLS replication (the old master's
  `FAILOVER TO` connects to the target like a replica, over `tls-replication`), a tier of two
  Sentinels and a tier without a majority (the fallback of item 3 is read, not measured), a stalled
  `FAILOVER TO` and how long writes then hang, anything on Kubernetes. Read, not measured: the
  post-failover handler asks no role of the outgoing pod (`handlePostFailover` looks for a master
  among the current pods only, `replaceRemainingPods` deletes the outdated one), so an old master
  that is already a replica takes the same path; and with no second master ADR 0025 D9's
  double-master window should stay empty on Valkey 9, which the ADR amending D9 has to state as
  measured or not. The docker model mirrors the operator's hostname announcements
  (`replica-announce-ip $MY_HOST`,
  [builder/statefulset.go:355](../../internal/builder/statefulset.go#L355); `resolve-hostnames` and
  `announce-hostnames`, [builder/sentinel.go:162-163](../../internal/builder/sentinel.go#L162-L163)),
  which `FAILOVER TO <host>` has to match.
- **B (T12's 2b) - Runtime fence on the outgoing master before each trigger.** M. Best-effort
  `CONFIG SET min-replicas-to-write 1` and `min-replicas-max-lag 10` on the outgoing master right
  before the trigger at both sites. `CONFIG SET` is not gated and recomputes the good-replica count
  at once through `updateGoodReplicas` (config.c 9.1.1:2617-2619), and `waitForReplicasReady` has
  just proved the replicas synced, so the gate is satisfied when it is set. Measured: 529 / 512
  (9.1.1) and 542 / 516 (8.1.9) acknowledged writes lost instead of 9510-9568 unfenced in the same
  series, the new master writable at +0.42 to +0.50 s; about a second of loss remains until Sentinel moves the last
  replica. Costs: it still loses about 500 writes per failover on either line; it does not touch
  part B, because a fenced old master still answers `role:master`; it adds a write to a data pod on
  the failover path (not a promotion record, so ADR 0009 does not bind it) that must be undone
  wherever the pod stays master (trigger error, reset, pause, abandon). Sentinel's conversion
  carries `CONFIG REWRITE` ([sentinel.c 9.1.1:4868-4935](https://github.com/valkey-io/valkey/blob/9.1.1/src/sentinel.c#L4868-L4935))
  and persists the fence into the pod's writable config on its `emptyDir`
  ([builder/statefulset.go:645-657](../../internal/builder/statefulset.go#L645-L657)), where it
  survives a `valkey` container restart (not a sandbox restart: the init container copies the
  config anew, [builder/statefulset.go:329-339](../../internal/builder/statefulset.go#L329-L339)); if that pod is not replaced - the image reverted mid-roll - a later crash failover
  can promote it with its only peer down, and it refuses every write. If taken, the clear must be a
  level, not a list of paths: `min_slaves_good_slaves` appears in `INFO replication` on every role
  while the directive is set (server.c 9.1.1:6568-6572), `findMaster` already reads that section
  from every pod ([health/checker.go:108](../../internal/health/checker.go#L108)), and a reconciler
  step can clear it on any pod reporting it outside `ownFailoverInFlight`. It reopens ADR 0025 D9's
  accepted consequence, and it forces T12's Decision 1 refusal into the same ADR as this fence, or
  one ADR says the operator does not set `min-replicas-to-write` and another says it does. At most
  it is a later addition for the Valkey 8 residual of A.
- **C (T12's 2c) - Keep the loss and record its measured size.** S. Amend ADR 0025 D9's
  consequence (`:285-295`), which states the loss without a size, with the docker numbers, correct
  the fifteen lossless statements, and name the Kind measurement as the trigger to revisit. Nothing
  changes at runtime: every Sentinel roll, TLS rotation rolls included, keeps discarding what the
  outgoing master acknowledges for about 16 s after the promotion, and part B stays possible.

Removed in T12's analysis and not reinstated: "B behind an opt-in CRD field" - a CRD surface for a
repair inside the roll that helps nobody by default, while ADR 0025 D9 itself changed the roll
fleet-wide without a field.

**Why A.** In docker it lost 0 acknowledged writes where B lost about 500 and the forced path
8633-11249, and it does so by adding one argument to a command the operator already sends,
with a pause that expires on its own: no new write to a data pod, no fence to clear, nothing that
can strand, and on Valkey 9 it also removes part B, which B does not touch. B loses to it on the
measured loss, on the strandable state and on part B. The case for B, argued: it works on both
lines today, needs no election and cannot hang writers on a stalled handover, and its cost is
measured on both pins, where A's no-majority and stalled-handover paths are only read. It does not
survive: its no-election advantage buys nothing while A falls back to exactly today's forced
command, a stalled handover refuses writes late where B loses about 500 on every roll, and B's one
real gain, Valkey 8, is a residual the fallback already bounds to today's behaviour. C would win
only if the Kind measurement showed the window already short on Kubernetes; the code ends it only
through the delete of the outgoing master, which needs a post-failover pass and a connected replica
on the new master, and on the forced path that delete is what part B turns into a second failover.
The Valkey 8 residual under A is recorded the C way; B on top of it is a separate later call.
**Ordering with T12:** this decision comes first, because T12's
Decision 1 (an opt-in `min-replicas-to-write` field) prices its failover cost by which path the
roll uses: at least 5 s of refused writes per controlled failover on the forced path, about a
second with the coordinated one.

## Decision

**Current: not decided** *(2026-09-27)* - A recommended, Options.

## Work list

- **XS, needs no decision, can land now (it returns urgency to `next`):** correct the fifteen
  lossless statements listed under Impact (the three test comments included) to what holds today - the dataset present before the
  roll survives; on a Sentinel cluster the writes the outgoing master acknowledges during the
  roll's failover are lost
  ([ADR 0025](../adr/0025-a-split-brain-warning-means-one-that-did-not-resolve-itself.md) D9) -
  citing the ADR, not this ticket, and naming no Kubernetes size. Coordinate the wording with
  [T70](070-tracked-comments-and-adrs-state-what-the-code-contradicts.md) item (b), which corrects
  the `WAIT` comments and ADR 0012 D9 for the non-Sentinel path; neither ticket edits the other's
  lines. `CLAUDE.md:1068` needs Hans. Reword again when A lands.
- **Needs no decision, before it (the Kind measurement T12 asked for):** an e2e subtest that runs
  a writer on the `-rw` Service through a Sentinel roll, logs every acknowledged key and counts the
  acknowledged-but-missing ones afterwards, with refused writes counted separately. Run it before
  the fix on both legs to record the Kubernetes size (C's revisit trigger); after A it is A's
  acceptance test on the Valkey 9 leg (expected 0) and the residual's recorded size on the Valkey 8
  leg. The writer must classify error replies, which the e2e helpers do not
  ([T68](068-the-e2e-exec-helpers-do-not-check-the-valkey-reply.md), filed from T12 the same day).
- **Needs no decision:** measure part B on Kind - whether the outgoing master is deleted while it
  still answers `role:master`, and whether its drain handler's failover then moves the master a
  second time (sidecar log "sentinel failover triggered" on the deleted pod, two
  `+switch-master` in the Sentinel log). If confirmed, the Valkey 8 residual gets its own decision
  here (a candidate: `replaceRemainingPods` deletes the outgoing master only once it answers
  `role:slave` or a bound has passed; not costed).
- **Waits on the decision:** A - a coordinated call in `valkeyclient` beside `SentinelFailover`,
  the reply classification and same-Sentinel fallback in `triggerSentinelFailover`, the first
  trigger coordinated and the retrigger at `:882` forced, the drain handler unchanged; an ADR
  amending ADR 0025 D9 and ADR 0007, ADR index rows in the same change; the pages above reworded.
  Or C - the ADR 0025 D9 amendment with the measured size, and the XS item as the whole change.

## Verification

- XS item: `git grep -n -i 'lossless\|without data loss\|no data is lost\|loses no data\|zero
  data loss'` over `README.md`, `CLAUDE.md`, `docs/adr/`, `docs/operations/` and `test/` finds no
  statement that a Sentinel roll loses nothing; what may stay is named under Impact (the dataset
  measurements, the password-propagation wish, and ADR 0012 D9 until T70 item (b) rewrites it).
- A, unit tier (a RESP fake records the commands): the first trigger sends `SENTINEL FAILOVER
  <name> COORDINATED`; the Valkey 8 reply and `-NOGOODPRIMARY` each lead to the forced command on
  the same Sentinel; `-INPROG` and `-NOGOODSLAVE` do not; the retrigger at `:882` sends the forced
  command; the drain handler still sends the forced command. Revert checks per
  [ADR 0017](../adr/0017-test-and-ci-policy.md): dropping the `COORDINATED` argument turns the
  first test red, removing the fallback turns the Valkey 8 reply test red, and making the retrigger
  coordinated turns its test red.
- A, e2e tier: the writer subtest above reports 0 acknowledged-but-missing keys on the Valkey 9 leg
  through a full Sentinel roll, and reverting the coordinated argument on a local Kind run makes it
  non-zero (the revert check that shows the subtest can fail); the Valkey 8 leg records its count
  and the fallback's log line; a clean roll still emits zero Warning Events (ADR 0025 D7).
- C: the ADR 0025 D9 consequence carries the measured size and the Kind trigger; the XS item done.
- "Merged" is not verification.

## Related tickets

- **[T12](012-no-write-fencing-min-replicas-to-write-as-an-opt-in-field.md)** - origin. Its
  Decision 1 (the opt-in `min-replicas-to-write` field, refusal recommended) is decided after this
  one; if this ticket goes to C, T12's option 1 becomes the only protection of this window for the
  clusters that opt in, and its comparison has to be redone. If this ticket goes to B, T12's
  refusal ADR and B's fence belong in one ADR.
- **[T35](035-master-records-lag-the-real-master.md)** decision 8 (L4, the labeler's cross-check
  reads `get-master-addr-by-name`) shortens how long `-rw` keeps routing to the outgoing master
  after the promotion; it does not stop that master from acknowledging writes, which is this
  ticket. The two compose and neither replaces the other; with A on Valkey 9 the old master is a
  replica at the promotion, so there is nothing left to route to it.
- **[T62](062-resetsentinelstate-falls-back-to-sentinel-reset.md)** - A's bound runs through
  `handleNoMasterFound` and `resetSentinelState`, T62's subject; T62 changes the reset, not the
  bound A relies on.
- **[T75](075-the-sentinel-failover-reset-and-retrigger-cycle-has-no-cap.md)** - the
  reset-and-retrigger cycle that A's no-majority fallback enters once (coordinated first trigger,
  forced retrigger). A adds no cycle of its own; a forced retrigger that cannot promote either
  stays in T75's uncapped loop exactly as today, so a cap decided there applies unchanged.
- **[T70](070-tracked-comments-and-adrs-state-what-the-code-contradicts.md)** item (b) - the
  non-Sentinel promote-to-demote loss and the `WAIT` comments; the XS item here is its Sentinel
  counterpart.
- **[T69](069-three-sync-checks-read-a-replica-field-from-the-master.md)** - `verifyNewMasterReady`'s
  "no sync in progress" check cannot fire, so the gate in front of the outgoing master's delete
  passes on the first connected replica; that makes the delete before the conversion (part B)
  earlier, and a fix there delays it only while that replica full-resyncs. Neither ticket closes the
  other.
- **[T68](068-the-e2e-exec-helpers-do-not-check-the-valkey-reply.md)** - the e2e reply check the
  writer subtest of the Work list depends on.
- **[T36](036-non-persistent-master-restarts-empty.md)** - the severity precedent (silent loss of
  data the client was told was stored, security `none`).

## History

- 2026-09-27: filed from T12 during the re-verification at `84a39c2`. **Moved from T12:** the
  "Separate finding" section (Fact, Verified / Not verified, Impact, the non-Sentinel relation),
  its Decision 2 complete (mechanism, options 2a/2b/2c as A/B/C, the removed opt-in variant, the
  recommendation and its justification, what is not measured), the Measurements rows of the forced
  loss with and without fences and of the coordinated failover, the proposed frontmatter (severity
  high, security none, effort M, blocked-by decision), the Kind e2e acceptance test from its Work
  list, and the order of the two decisions; T12's journal of the day (the audit, the facts and
  design reviews, the edit and its review) was the source of the per-run numbers. **Re-verified
  now:** every code location at `84a39c2` (the triggers `:2751` and `:882`, the requeue `:2757`,
  `triggerSentinelFailover` `:3700-3740`, `SentinelFailover` `client.go:231-238`, the labeler
  `:135-145`, `handleNewMasterFound` `:3119-3129`, `verifyNewMasterReady`, `replaceRemainingPods`,
  `failoverRetryTimeout` `:143`, `SentinelFailoverTimeout` `sentinel.go:50`); upstream `sentinel.c`
  at 9.1.1 and 8.1.9 fetched again (9.1.1 byte-identical to the review's copy, 8.1.9 without
  `coordinated`); the forced trigger in released code since `91ca86d` (v1.0.0). **Measured now**
  (docker, `vko-file-067-*`, all removed): the forced loss again on both pins (9133 and 8633), the
  old master still `role:master` 16 s after the trigger and converted 16.3-16.4 s after the
  promotion; the coordinated failover again on 9.1.1 (0 of 1118) and its refusal on 8.1.9; a
  second forced failover sent while the old master still answers `master` moves the master a second
  time (part B). **Corrected against T12's text:** option 2a said `SentinelFailover` itself sends
  `COORDINATED` at both sites - that would change the drain handler, which shares it, so A now
  names a call of its own and keeps the drain forced; 2a's "falls back to the forced trigger on a
  bound" is made concrete - the coordinated command answers `OK` without a majority and aborts
  later, so the fallback is the existing 30 s `failoverRetryTimeout` with the retrigger at `:882`
  kept forced; `-NOGOODPRIMARY` is precised to a Sentinel that has not yet read
  `master_failover_state`, not "a master that cannot do it"; "Sentinel converts it about 15 s after
  the promotion" is 16.3-16.4 s; the coordinated run count is eight, counting this filing's run.
  **New:** part B (appendix, same mechanism: the dying outgoing master's drain handler can force a
  second failover), and the twelve tracked lossless statements, which make urgency `now` by rule 1
  where T12 had proposed `next` by rule 3 for this finding; if Hans reads "lossless" as a statement
  about the pre-roll dataset only, rule 1 does not match and rule 3 gives `next`. State
  `analysed`, no decision taken. **Adversarial review the same day** (every load-bearing line
  re-read at `84a39c2`, `sentinel.c` 9.1.1 and 8.1.9 fetched a third time, byte-identical to this
  filing's copies; one docker check, `vko-file-067-pk-*`, removed): the forced range is
  8633-11249, not 8633-9568 (the second reviewer's 11249 was left out); the 15 s requeue is not a floor - every pass in
  `failover-triggered` goes to `handlePostFailover` and any watch event brings one earlier - and
  `verifyNewMasterReady` passes on the first connected replica in any sync state (T69), so part B's
  delete before the conversion is the expected order on the requeue alone (15 s against about
  17.4 s), not a corner case; the label lag is the leader's own `+switch-master` because the
  labeler and the trigger both ask the Sentinels in ordinal order; the roll does emit an Event, the
  Normal `FailoverTriggered`, which names no loss; three test comments state the same lossless
  claim (fifteen places, not twelve) and the verification grep is widened to find them;
  `-NOGOODPRIMARY` follows the operator's own `resetSentinelState` (a new monitor) but not a plain
  `SENTINEL RESET`; the `CKQUORUM` citation pointed at the helper, not the command; the published
  script now matches the one that ran, and its `pkill` line is recorded as a no-op (no `pkill` in
  either image, checked in docker) that does not change the counts; added: the not-verified
  list of A carried from T12, the case for B argued against A (the mark stays on A), the race of the
  drain handler with the Valkey container's SIGTERM as a part B unknown, T68 and T69 as related.
  Severity, security class, urgency rule and effort re-derived and unchanged.
