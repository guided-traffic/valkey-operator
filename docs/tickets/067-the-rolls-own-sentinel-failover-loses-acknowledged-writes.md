---
id: T67
title: the roll's own Sentinel failover loses the writes the outgoing master acknowledges after the promotion
state: analysed       # loss measured in docker on both pinned lines, options costed, one recommended
severity: high        # silent loss of acknowledged writes on every Sentinel roll, bounded to seconds per roll; the pre-roll dataset survives
security: none        # durability only; no principal gains anything and no guard is weakened
urgency: now          # rule 1: fifteen tracked places state that every multi-replica roll is lossless, measured false; next by rule 3 once the XS item lands
effort: M             # option A: a coordinated client call with fallback, unit tests, the Kind e2e subtest, an ADR; plus the XS text item
blocked-by: decision
filed-from: T12
opened: 2026-09-27
decided:
done:
---

# T67 - the roll's own Sentinel failover loses the writes the outgoing master acknowledges after the promotion

## Current state

**The forced failover.** During a Sentinel roll, `handleMasterFailover`
([rolling_update.go:2695-2758](../../internal/controller/rolling_update.go#L2695-L2758)) waits for
the replicas, sends `WAIT` to the master, stamps the failover state (`setFailoverTriggered`,
`:2743`), triggers the failover (`:2751`) and requeues after 15 s. The retrigger after a reset,
`handleFailoverRetrigger` ([:849-887](../../internal/controller/rolling_update.go#L849-L887)),
triggers at `:882`. Both call `triggerSentinelFailover`
([:3700-3740](../../internal/controller/rolling_update.go#L3700-L3740)), which tries each Sentinel
in ordinal order and sends a plain `SENTINEL FAILOVER <name>` through `SentinelFailover`
([valkeyclient/client.go:231-238](../../internal/valkeyclient/client.go#L231-L238)).

Sentinel runs the plain command as a **forced** failover: no leader election, a replica is promoted
with `REPLICAOF NO ONE` and the old master is not told
([sentinel.c 9.1.1:3957-3960](https://github.com/valkey-io/valkey/blob/9.1.1/src/sentinel.c#L3957-L3960)).
The old master keeps answering `role:master` and acknowledging writes until Sentinel converts it,
which it does only after the old master has reported `role:master` for 8 s while listed as a replica
([:2630-2641](https://github.com/valkey-io/valkey/blob/9.1.1/src/sentinel.c#L2630-L2641)). Every
write acknowledged in that window is discarded when it resyncs. `WAIT` covers only writes
acknowledged before it.

Nothing on the operator side shortens the window:
- The outgoing master keeps its `instanceRole=master` label, and the `-rw` endpoint, until the
  Sentinel its labeler asks ([sidecar/labeler.go:135-145](../../internal/sidecar/labeler.go#L135-L145),
  first answering Sentinel in ordinal order) reaches its own `+switch-master`.
- Pooled client connections stay on the old master until the conversion kills them.
- The operator deletes it only in `replaceRemainingPods`
  ([:2984-3051](../../internal/controller/rolling_update.go#L2984-L3051)), reached through
  `handlePostFailover` and `handleNewMasterFound` on a later pass, once `verifyNewMasterReady`
  ([:3331-3397](../../internal/controller/rolling_update.go#L3331-L3397)) sees one connected replica
  on the new master in any sync state (T69). That pass comes at the 15 s requeue or earlier on any
  watch event.

[ADR 0025](../adr/0025-a-split-brain-warning-means-one-that-did-not-resolve-itself.md) D9 accepts
the loss in its Consequences (`:285-295`) without a size. No e2e runs a writer during a roll
(`TestE2E_RollingUpdate_HA_NoDataLoss`,
[rolling_update_test.go:489](../../test/e2e/rolling_update_test.go#L489), writes its keys before).

**Second failover by the dying master (part B).** Nothing asks the outgoing master whether it is a
replica before `replaceRemainingPods` deletes it
([:3034](../../internal/controller/rolling_update.go#L3034); the comment there calls it "now a
replica", which holds only after `+convert-to-slave`). The delete SIGTERMs its sidecar; the drain
handler ([sidecar/drain.go:98-148](../../internal/sidecar/drain.go#L98-L148)) reads the local role
and, if still `master`, sends the same plain `SENTINEL FAILOVER <name>`. Sentinel then fails over
the pod the roll has just promoted, opening a second window of the same loss. By timing (promotion
about 1 s after the trigger, conversion about 16.4 s after that, the post-failover pass at 15 s or
earlier) the delete before the conversion is the expected order.

**Measured in docker** (master, two replicas with the operator's diskless settings, one Sentinel
with the operator's timeouts, a writer on the master, one connection per write):

| Trigger | Acknowledged writes lost | Timing |
|---|---|---|
| Forced, no fence | 9.1.1: 9568, 9553, 9133, 11249 of 13271; 8.1.9: 9510, 8633 (about 500-600 writes/s) | `+switch-master` 6.2-6.4 s, `+convert-to-slave` 16.3-16.4 s after `+promoted-slave`; old master `role:master` up to +16 s |
| Forced, `min-replicas-to-write 1` fence on every node | 9.1.1: 595, 556; 8.1.9: 596 | |
| Forced, runtime `CONFIG SET` fence on the old master before the trigger | 9.1.1: 529, 512; 8.1.9: 542, 516 | new master writable at +0.42 to +0.50 s |
| `SENTINEL FAILOVER <name> COORDINATED` | 9.1.1: 0 in eight runs (one Sentinel, fenced, and three Sentinels at quorum 2); 8.1.9: refused with `ERR wrong number of arguments for 'sentinel\|failover' command`, nothing fails over | new master writable at +0.62 to +1.4 s; old master `role:slave` at once, a late write gets `READONLY`; all replicas resync partially |
| Part B: second forced failover 14 s after the first (9.1.1, one run) | | answered `OK`; the other replica promoted 1.14 s later; the pod promoted 13 s before converted 14.2 s after that |

**Impact.** Every client writing to a Sentinel-enabled cluster while its data tier rolls: image or
config changes, operator upgrades that change the pod spec, enabling metrics or anti-affinity, and
every TLS rotation roll ([ADR 0030](../adr/0030-rotating-certificates-rotate-the-instances-that-cannot-reload-them.md)
D4), which cert-manager starts unasked. Each roll fails over once
([ADR 0007](../adr/0007-failover-aware-rolling-update.md) D1) and each failover is forced. Clients
see `OK`; the roll completes, `Ready` stays `True`, and the only Event is the Normal
`FailoverTriggered`. The pre-roll dataset survives. No opt-out.

**Tracked text that contradicts it.** Fifteen places state that a multi-replica roll loses nothing:
[README.md:46](../../README.md#L46), [CLAUDE.md:1068](../../CLAUDE.md#L1068),
[ADR 0005:357](../adr/0005-upgrade-neutral-defaults-and-anti-affinity.md#L357),
[ADR 0018:100](../adr/0018-metrics-and-the-exporter-sidecar.md#L100) and
[:179](../adr/0018-metrics-and-the-exporter-sidecar.md#L179),
[ADR 0032:366](../adr/0032-generated-pods-run-rootless.md#L366),
[docs/operations/anti-affinity.md:16](../operations/anti-affinity.md#L16) and
[:39](../operations/anti-affinity.md#L39),
[docs/operations/monitoring.md:31](../operations/monitoring.md#L31),
[docs/operations/upgrading.md:52](../operations/upgrading.md#L52) and
[:161](../operations/upgrading.md#L161),
[test/e2e/tls_rotation_test.go:219](../../test/e2e/tls_rotation_test.go#L219),
[test/e2e/fleet_upgrade_test.go:22-23](../../test/e2e/fleet_upgrade_test.go#L22-L23),
[test/e2e/rolling_update_test.go:489-490](../../test/e2e/rolling_update_test.go#L489-L490),
[test/testimages/images.go:76-78](../../test/testimages/images.go#L76-L78). Out of scope: the
dataset statements in `docs/operations/persistence.md` and ADR 0023, ADR 0012 D9 (non-Sentinel
path, T70 item (b)), and the password-propagation wish in ADR 0016 `:261` and
`docs/security/rotation-and-change-propagation.md:47`.

## Required changes

### Independent of the open question

- **XS, now:** reword the fifteen places above to what holds: the dataset present before the roll
  survives; on a Sentinel cluster the writes the outgoing master acknowledges during the roll's
  failover are lost (ADR 0025 D9). Cite the ADR, not this ticket; name no Kubernetes size. Do not
  edit the lines T70 item (b) owns. `CLAUDE.md:1068` needs Hans.
  Check: `git grep -n -i 'lossless\|without data loss\|no data is lost\|loses no data\|zero data loss'`
  over `README.md`, `CLAUDE.md`, `docs/adr/`, `docs/operations/`, `test/` finds no claim that a
  Sentinel roll loses nothing, apart from the out-of-scope places.
- **Kind measurement:** an e2e subtest that writes through the `-rw` Service during a Sentinel roll,
  logs every acknowledged key and counts acknowledged-but-missing keys afterwards, refused writes
  counted separately (needs T68's reply classification). Run it on both legs before any fix.
- **Part B on Kind:** check whether the outgoing master is deleted while still `role:master` and
  whether its drain handler moves the master again (sidecar log "sentinel failover triggered" on
  the deleted pod, two `+switch-master` in the Sentinel log).

### Depends on the answer to Q1

- **A:** a new coordinated call in `valkeyclient` beside `SentinelFailover` (the drain handler
  shares `SentinelFailover` through `ValkeyCommander`, [drain.go:27-32](../../internal/sidecar/drain.go#L27-L32),
  and keeps the forced command). In `triggerSentinelFailover`, the first trigger (`:2751`) sends
  `COORDINATED`; on the Valkey 8 `ERR wrong number of arguments` reply or `-NOGOODPRIMARY` it asks
  the same Sentinel again with the forced command; `-INPROG` and `-NOGOODSLAVE` stay a failed
  attempt. The retrigger (`:882`) stays forced, so a coordinated failover that aborts for lack of a
  majority falls back through the existing 30 s `failoverRetryTimeout` in `handleNoMasterFound`.
  New ADR amending ADR 0025 D9 and ADR 0007, index row, the fifteen places reworded again.
  Tests: unit (RESP fake) - first trigger sends `COORDINATED`; each of the two replies leads to the
  forced command on the same Sentinel; `-INPROG`/`-NOGOODSLAVE` do not; the retrigger and the drain
  handler send the forced command; revert checks per [ADR 0017](../adr/0017-test-and-ci-policy.md).
  E2E - the writer subtest reports 0 lost on the Valkey 9 leg, non-zero with the argument reverted
  locally; the Valkey 8 leg records its count and the fallback log line; a clean roll still emits
  zero Warning Events.
- **C:** amend ADR 0025 D9's consequence with the measured sizes and name the Kind measurement as
  the revisit trigger; the XS item is the whole change.

## Open questions

### Q1: How should the roll's own Sentinel failover stop the outgoing master from acknowledging writes that are later discarded?

Today the roll sends a forced `SENTINEL FAILOVER`, and the old master acknowledges writes for about
16 s after the promotion, all lost. None of the options changes crash failovers, the non-Sentinel
path, the drain handler's command, the pod templates or the CRD.

- **A - `COORDINATED` on Valkey 9, forced command as fallback (recommended).** M. Sentinel pauses
  writes on the old master, hands over with `FAILOVER TO`, then promotes: no second master. 0 lost
  in eight docker runs, and part B disappears on Valkey 9 because the old master is a replica at
  once. Costs: a stalled `FAILOVER TO` can block writes to the old master for up to the remaining
  60 s `failover-timeout` (refused late, not lost; not measured); Valkey 8 Sentinel clusters keep
  today's loss.
- **B - runtime fence (`CONFIG SET min-replicas-to-write 1`) on the outgoing master before each
  trigger.** M. Works on both lines, still loses about 500 writes per failover, does not touch part
  B, and adds a data-pod write that must be cleared wherever the pod stays master; Sentinel's
  `CONFIG REWRITE` persists it into the pod's config, where a later crash failover can promote a pod
  that refuses every write. Would share one ADR with T12's refusal of that setting.
- **C - keep the loss and record its size.** S. Amend ADR 0025 D9 with the numbers; nothing changes
  at runtime, part B stays possible.

A loses 0 where B loses about 500 and today loses 8633-11249, by one argument on a command the
operator already sends, with no state to clear; its no-majority and Valkey 8 paths fall back to
exactly today's command. B could be added later for the Valkey 8 residual. Decide before T12's
Decision 1, whose cost depends on the failover path.

**Answer:** _open_

## Not verified

- The loss on Kubernetes (write rate, `-rw` routing lag, pooled connections): the Kind subtest settles it.
- Part B on Kubernetes and on 8.1.9, including whether the sidecar's role read wins against the
  Valkey container's own SIGTERM (a Sentinel cluster renders no `preStop` hook,
  [builder/statefulset.go:746-749](../../internal/builder/statefulset.go#L746-L749)), and what the
  split-brain resolver does with its double master in state `replacing-master`: the Kind part B check.
- For A: TLS replication (`FAILOVER TO` over `tls-replication`), a tier of two Sentinels, a tier
  without a majority, a stalled `FAILOVER TO`: A's e2e on a TLS cluster and a fault-injected run.
- Whether a client confirming writes with `WAIT` sees `0` for the lost ones: inferred, not measured.

## Related

- [T12](012-no-write-fencing-min-replicas-to-write-as-an-opt-in-field.md) - origin; its Decision 1 is priced by this answer; under C its option 1 becomes the only protection of this window, under B both belong in one ADR.
- [T35](035-master-records-lag-the-real-master.md) - decision 8 shortens how long `-rw` routes to the old master; composes with this ticket.
- [T62](062-resetsentinelstate-falls-back-to-sentinel-reset.md) - A's fallback bound runs through `resetSentinelState`.
- [T68](068-the-e2e-exec-helpers-do-not-check-the-valkey-reply.md) - the reply check the writer subtest needs.
- [T69](069-three-sync-checks-read-a-replica-field-from-the-master.md) - `verifyNewMasterReady` passes on the first connected replica, making part B's early delete likelier.
- [T70](070-tracked-comments-and-adrs-state-what-the-code-contradicts.md) - item (b), the non-Sentinel counterpart of the XS item.
- [T75](075-the-sentinel-failover-reset-and-retrigger-cycle-has-no-cap.md) - the uncapped reset-and-retrigger cycle A's fallback enters once.
- [T36](036-non-persistent-master-restarts-empty.md) - severity precedent.
