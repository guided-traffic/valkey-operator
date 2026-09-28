# ADR 0038: The operator does not offer `min-replicas-to-write` as a CRD field

## Status

Accepted. Date: 2026-09-28.

Implemented: the refusal holds — `generateValkeyConf` writes no `min-replicas` directive and
the CRD has no field. Outstanding are two corrections this record names (*Residual risks*): a
unit fixture that mocks a reply `WAIT` never gives, and an e2e helper that reads a refused write
as a success.

Companion of [ADR 0037](0037-the-master-handover-loses-no-acknowledged-write-and-no-dataset.md),
which closes the losses the fence was considered for.

## Context

`min-replicas-to-write N` with `min-replicas-max-lag S` makes a master answer every write with
`NOREPLICAS` while fewer than N replicas are attached with a lag of at most S seconds. Reads,
`WAIT` (which answers `0`), `REPLICAOF`, `PUBLISH`, `CONFIG SET` and `CLIENT KILL` are not
gated; a stalled link lets `max-lag` plus one server cron tick of writes through; `max-lag 0`
switches the gate off silently. The operator sets the directive nowhere and the CRD has no
field, which [ADR 0028](0028-a-demotion-may-not-discard-the-only-dataset.md) D3 names as the
price of every refused demotion: two masters that both accept writes.

Measured in docker on `valkey/valkey:9.1.1` and `8.1.9` with the operator's settings:

- Forced Sentinel failover under 500-600 writes/s, three pods, `min-replicas-to-write 1` on
  every node: 556-596 acknowledged writes lost instead of 9133-11249 — Sentinel re-points the
  third replica to the new master within seconds, and the old one then refuses. A runtime
  `CONFIG SET` of the same fence on the old master first: 512-542.
- The cost when opted in: the non-Sentinel `promoteAndRedirect` refuses writes for 0.23-0.25 s
  idle and 5.1-5.7 s plus the transfer under load; a forced Sentinel failover 5.7-6.9 s (nine
  runs); a tier of two until the replacement joins; a sibling's 522 MB full sync refused 0 of 11
  `SET`s on the survivor (lag measured once).
- The operator reads neither `slaveN` `state`/`lag` nor `min_slaves_good_slaves`
  (`parseReplicationInfo`), so a fenced master refusing writes behind a healthy `-rw` Service
  would be unexplained: one pod down of two, two of three, one stalled replica, and the 270 s of
  `handleMasterWithNoReplicas`. The observer's `/readyz` fails only on a real refusal.
- `spec.sentinel.enabled` with `replicas: 1` is accepted by the CRD and would be fenced for
  good behind `PING`-only checks reading `OK`.

What the fence would protect once ADR 0037 is built: the Valkey 8 residual of a forced failover
on a tier of three or more (16 s of accepted-then-discarded writes become about 1-2 s; a tier of
two cannot be fenced under `minReplicas <= replicas - 2`); the held handover of ADR 0037 D5 and
D6, where a replica-less empty master would refuse writes, stay empty and keep the dataset veto
standing until a human instead of one cycle; and the steady-state splits of ADR 0028 D3. What it
would not: a promoted non-persistent pod restarting empty and full-syncing its replicas (a full
sync is not a write), the side of a split that keeps a replica, and the `max-lag` window.

## Decision

**D1 — No CRD field renders `min-replicas-to-write` or `min-replicas-max-lag`.** The
`replicationConfig` tail of `generateValkeyConf` stays without them. A generic `spec.extraConfig`
is no substitute and is refused with it: it would expose `replicaof`, `save` and the TLS lines
the operator owns.

**D2 — The operator sets the fence at runtime nowhere either.** A `CONFIG SET
min-replicas-to-write` on a master — before a failover, or on a held empty master — is refused
as a mechanism: Sentinel's `CONFIG REWRITE` (sent in `sentinelKillClients` and with every
`REPLICAOF` it issues) persists the directive into that pod's running config, so a later crash
failover can promote a pod that refuses every write, silently; and the clear would be a write of
the kind [ADR 0009](0009-an-unrecorded-promotion-is-not-a-promotion.md) exists for.

**D3 — The fence is re-decided on one of three triggers, and from this record.** A user on
Valkey 8 with `replicas >= 3` who cannot move to Valkey 9 asks for it;
`MasterHandoverStalled` with reason `DatasetWouldBeDiscarded` (ADR 0037 D6) is seen in the
field; a non-Sentinel split is seen diverging beyond the bounds ADR 0028 D8 names. The
re-decision starts from the design under *Alternatives Considered*, not from scratch.

**D4 — The two texts that assumed a fence are corrected.** The unit fixture
`TestHandleMasterFailover_DoesNotFailOverWhenWriteSyncFails` mocks `NOREPLICAS` for `WAIT`, a
reply `WAIT` never gives; it gets the reply a replica gives (`ERR WAIT cannot be used with
replica instances. …`) and a comment that any error reply blocks the promotion, assertion
unchanged. The e2e helper that runs `valkey-cli --raw SET` treats exit 0 as a success, which a
`NOREPLICAS` or `READONLY` reply also is; the reply is classified, not the exit code.

## Consequences

- Two masters in a split keep accepting writes for the length of their window, and the repair
  discards the loser's (ADR 0028 D3). The held handover of ADR 0037 D6 lasts until a human or
  the first client write on the empty master; nothing keeps that master empty.
- The Valkey 8 forced-failover loss stays at its measured size until the cluster's Sentinels
  run Valkey 9 (ADR 0037 D1).
- Nothing is built, nothing is documented at a field, and no cluster pays seconds of
  `NOREPLICAS` on every failover.

## Alternatives Considered

**Build `spec.writeFencing`, default off.** `enabled: false`, `minReplicas` (example 1),
`maxLagSeconds` (example 10, at least 1); both directives rendered in the shared
`replicationConfig` tail only when enabled ([ADR 0005](0005-upgrade-neutral-defaults-and-anti-affinity.md)
D1; both bodies feed `ComputeConfigHash`, so enabling rolls once); `minReplicas > replicas - 2`
and `maxLagSeconds < 1` refused without rendering, covering Sentinel with `replicas: 1`; `slaveN`
`state`/`lag` and `min_slaves_good_slaves` parsed and surfaced so a refusal is explained; the
refused-write cost documented at the field (ADR 0005 D8 shape); e2e writes gated on connected
replicas, and a fenced three-replica cluster rolled under the writer harness on both legs with
no acknowledged write lost. Where the two bounds are enforced was left open: at admission (CEL,
as `spec.podSecurity` does) invalid specs never arrive, but a later scale-down below
`minReplicas + 2` is refused too, against the `podDisruptionBudget.maxUnavailable` precedent;
at runtime nothing is rendered and a `WriteFencingNotApplied` condition with a Warning Event
reports it. Lost, for now: after ADR 0037 the fence adds a residual on a line the upgrade path
leaves, a narrow shape that is reported with its repair, and ADR 0028's accepted windows —
against seconds of refused writes on every failover for every cluster that opts in, degraded
states turned into write outages, and an L-sized feature nobody asked for. ADR 0005 D1 makes a
feature opt-in; built and kept it has to be regardless.

**A runtime fence, set by the operator.** Rejected in D2.

**A generic `spec.extraConfig`.** Rejected in D1.

## Residual risks

- **The duration of `NOREPLICAS` after a coordinated failover is not measured** (the new master
  has no good replica until the old one and the third attach with lag under `max-lag`), so the
  cost side of the re-decision on Valkey 9 is an estimate of a few seconds.
- **Whether the diverging side in ADR 0028's windows is the replica-less one** is not verified;
  the fence protects only that side.
- **Survivor lag under a large full sync was measured once**, on 9.1.1.
- **D4 is outstanding**: until the fixture and the helper are corrected, one unit test pins a
  reply that cannot occur and one e2e path reports a refused write as written.

## References

* [`internal/builder/configmap.go`](../../internal/builder/configmap.go) — `generateValkeyConf`, `replicationConfig`
* [`internal/valkeyclient/client.go`](../../internal/valkeyclient/client.go) — `parseReplicationInfo`, the fields not read
* [`internal/controller/sentinel_failover_test.go`](../../internal/controller/sentinel_failover_test.go) —
  `TestHandleMasterFailover_DoesNotFailOverWhenWriteSyncFails`
* [`test/e2e/sentinel_stale_master_test.go`](../../test/e2e/sentinel_stale_master_test.go) — the `--raw SET` helper
* [ADR 0005](0005-upgrade-neutral-defaults-and-anti-affinity.md) D1, D8 — the opt-in and the field-level cost note the alternative would follow
* [ADR 0009](0009-an-unrecorded-promotion-is-not-a-promotion.md) — why a runtime fence's clear is a recorded write
* [ADR 0028](0028-a-demotion-may-not-discard-the-only-dataset.md) D3, D8 — the windows the fence was the price of
* [ADR 0037](0037-the-master-handover-loses-no-acknowledged-write-and-no-dataset.md) D1, D4–D6 — what closed the losses, and the hold a fence would keep empty
