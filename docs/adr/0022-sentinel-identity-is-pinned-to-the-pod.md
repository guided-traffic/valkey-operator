# ADR 0022: A Sentinel's identity is pinned to its pod, and peer drift is reported rather than reset

## Status

Accepted. Date: 2026-08-23.

Implemented: the pinned `sentinel myid` in the Sentinel init container, the
`num-other-sentinels` field on `SentinelMasterInfo`, the `SentinelPeersStale`
condition and its recheck.

Verified in a Kind cluster on 2026-08-23: peer tables stay at 2/2/2 across two
replacements of the same Sentinel pod and the identity survives both
(`TestE2E_SentinelPeerTableSurvivesPodReplacement`); a cluster carrying real drift
reports the condition True and clears it within the recheck interval after the manual
reset of D6.

Not implemented, deliberately: the operator never issues `SENTINEL RESET` to clean a
peer table. Drift that predates this ADR is cleared manually or at the next Sentinel
roll — see D6.

## Context

Sentinel discovers its peers through hello messages on the monitored master's
pub/sub channel and keys them by the announcing Sentinel's own id (`sentinel myid`)
and its announced address. **It never forgets a peer it has seen**, and the majority
a failover leader needs is computed over the whole table: a Sentinel can lead only
when `live >= floor((known_others + 1) / 2) + 1`.

The Sentinel config lives on an `emptyDir`
([`internal/builder/sentinel.go`](../../internal/builder/sentinel.go), `buildSentinelPodSpec`).
Every replacement pod therefore starts from the ConfigMap template, generates a
**fresh** `myid`, and comes back on a **new** pod IP. Its peers match it neither by id
nor by address, so they record it next to the dead one instead of switching the
existing entry's address.

The 1.11.0 fleet rollout on 2026-08-22 made this visible: on all seven
sentinel-enabled clusters `num-other-sentinels` read 4/3/2 across the three
sentinels instead of 2/2/2, with the replaced pods' old IPs still listed as `s_down`.

Measured on 2026-08-23 against `valkey/valkey:9.1.1` and `valkey/valkey:8.1.9` — one
master, three Sentinels, quorum 2, a "pod replacement" being a fresh config directory
and a new IP:

| Scenario | Result |
|---|---|
| initial | 2/2/2 |
| one full ascending roll of all three sentinels | 4/3/2 |
| a second full roll | 4/3/2 — no growth |
| replacing only sentinel-2, three times | 2 → 3 → 4 → 5 on the survivors |

A full-tier roll is **self-limiting**: each pod destroys its own table when it is
replaced, and the pod rolled last always ends up clean, which is why the fleet still
failed over on 2026-08-22. What grows without bound is **partial churn** — chaos
kills, evictions, node drains and OOM kills that hit a subset while the others stay
up. That churn is not orchestrated by the operator and reaches no rolling-update
hook.

The consequence is not cosmetic. Same topology, same timings, three runs:

| Run | Peer tables | Master killed | Outcome |
|---|---|---|---|
| control | 2 live Sentinels, 2 known each | yes | replica promoted within 10 s |
| stale | 2 live Sentinels, 5 known each | yes | **no failover after 45 s** — 6 voters need 4 votes, 2 exist |
| pinned id | same churn, `myid` pinned | yes | 2 known each, replica promoted within 20 s |

Two further measurements constrain the fix. `SENTINEL RESET <name>` **keeps** the
current master address, including after a failover moved it away from the config-file
value — the comment in
[`internal/controller/rolling_update.go`](../../internal/controller/rolling_update.go)
above `resetSentinelState` claims the opposite and is wrong. And a `SENTINEL RESET`
issued while the master is unreachable leaves the Sentinel at
`num-other-sentinels=0` and `num-slaves=0` with no way back: peer and replica
discovery both run through the master, so there is no other channel to rebuild from.

Nothing in the operator read `num-other-sentinels` before this ADR.
`checkAndHandleSentinelRollingUpdate` guards on Kubernetes pod readiness against
`replicas/2+1`, `isSentinelAwareOfReplicas` reads `num-slaves`, the health check read
`flags` alone, and `SENTINEL CKQUORUM` appears nowhere outside the e2e TLS test. The damage is confined to Sentinel's own leader
election, where the operator has no visibility at all.

## Decision

**D1 — The Sentinel init container pins `sentinel myid` to the pod.** It appends
`sentinel myid <sha1 of "$HOSTNAME.<namespace>">` to the writable config
(`buildSentinelInitCommand`). A replacement pod of the same ordinal therefore carries
the identity its predecessor had, and its peers take Sentinel's documented
address-switch path (`+sentinel-address-switch`), replacing the entry instead of
adding one.

**D2 — A missing `HOSTNAME` leaves the identity to Sentinel.** An empty hostname would
derive the *same* id for every Sentinel of the cluster, collapsing three voters into
one — strictly worse than the drift this prevents. The script falls back to Sentinel's
own random id and says so on stdout.

**D3 — `sha1sum` is a declared image dependency.** It is named in `RequiredImageTools`
and checked against the real images by `make test-image-tools`. The fallback in D2 is
silent by design, so the image check is the only thing that would notice its absence.

**D4 — The peer count is collected from the reply the health pass already asks for.**
`observeSentinels` reads `num-other-sentinels` out of the same `SENTINEL MASTER`
response it uses for the quorum answer, and carries it in `ClusterState.SentinelPeers`.
Peer-table hygiene costs no additional connection per pass.

**D5 — Drift is reported as the `SentinelPeersStale` condition, and cleared explicitly.**
`recordSentinelPeerDrift` sets it True with the offending pods and their counts when
any responding Sentinel knows more than `replicas-1` others, and False when they all
agree with the replica count. A pass in which **no** Sentinel answered writes nothing:
an empty measurement must not overwrite a True condition with False.

**D6 — The operator never issues `SENTINEL RESET` on its own.** Removing a stale entry
means resetting that Sentinel's whole table, which is harmless with a healthy master
and unrecoverable without one. Existing drift is cleared by an operator, one Sentinel
at a time with the master verified healthy, or by the next Sentinel roll — which
rebuilds every table from scratch anyway. D5 is what makes either verifiable.

**D7 — A cluster that reports drift is re-checked every 5 minutes; a clean one is
not.** Clearing the entries changes no Kubernetes object: no pod restarts, no owned
object writes, and the CR watch is generation-gated — so without a recheck the
condition would keep asserting drift until the manager's 10 h cache resync, which is
precisely the window an operator needs it in. Measured before the recheck existed: a
cluster with real drift kept `SentinelPeersStale=False` indefinitely because no pass
re-entered. Only clusters that already report drift pay for the poll
(`sentinelPeerDriftRecheckInterval`), and they stop on the pass that finds the tables
agreeing again. A clean cluster relies on the `Owns(&appsv1.StatefulSet{})` watch,
which fires whenever a Sentinel pod is replaced — the only way drift can appear once
D1 holds.

**D8 — The identity is namespace-scoped.** Two clusters of the same name in different
namespaces derive different ids. Their Sentinels never meet — they monitor different
masters and discover each other through that master alone — so this is defence in
depth, at no cost.

## Consequences

- Adopting this rolls the Sentinel tier once: the init command is part of
  `ComputeSentinelPodSpecHash`, so every Sentinel pod is replaced on upgrade. Data pods
  are untouched.
- The fix generation does not clean itself up. The pods rolled first witness the
  identity change of the pods rolled after them, so a cluster ends that roll at 4/3/2
  exactly as before. It converges at the *next* roll, or immediately via D6.
- A pod that returns after a long absence resumes an identity its peers already know,
  instead of appearing as a new Sentinel. That is the point, and it also means the
  electorate now matches the pod count exactly rather than counting dead voters.
- One more condition on the CR, and therefore one more
  `vko_valkey_status_condition` series per resource (ADR 0021).
- `SENTINEL MYID` is now something the e2e suite depends on. Verified present on both
  pinned image lines.

## Alternatives Considered

**Announce a stable address (`sentinel announce-ip <pod FQDN>`) instead.** Measured to
hold the table at 2/2/2 across replacements on both image lines, and it would make
`SENTINEL sentinels` readable — hostnames instead of pod IPs — matching the operator's
existing `resolve-hostnames`/`announce-hostnames` posture. Rejected as the primary fix
because it puts DNS into the Sentinel-to-Sentinel path, which today is pure IP and
survives a DNS outage or a NetworkPolicy that forgets port 53. It composes with D1 and
can be added later for the readability alone.

**`SENTINEL RESET` after the Sentinel tier finishes rolling.** Cures instead of
preventing, and only for rolls the operator drives — leaving partial churn, the case
that actually grows without bound, untouched. It also has no place to hang: the
Sentinel tier has no completion marker today (`checkAndHandleSentinelRollingUpdate`
returns an empty result when the last pod is current). It would pay a recurring risk on
every roll, forever, for a failure mode D1 makes impossible.

**Drift-triggered `SENTINEL RESET` in steady state.** Detect `num-other-sentinels >
live-1` and reset one Sentinel per pass, gated on a healthy master, all pods Ready, no
failover in progress, and a cooldown. It is the only option that heals automatically
and covers causes nobody has thought of. Held in reserve rather than rejected: every
gate is load-bearing, and a reset with the master down is unrecoverable. If the D5
condition shows drift reappearing once D1 is live, that is the evidence this is worth
its gates.

**Persist the Sentinel config on a PVC.** Would keep both the identity and the peer
table across restarts, but it puts a volume on every Sentinel pod, and a surviving
config also resurrects stale master addresses the init container exists to correct.

**Read each Sentinel's current `myid` and pin that.** Would avoid the messy fix
generation, but the ids would have to be persisted somewhere the operator can rewrite
per pod, and the ConfigMap is shared by the whole tier. Not worth a durable state
store for a one-off.

## Residual risks

- **Two pods sharing one identity.** If a node partitions and the pod object is deleted
  while its container keeps running, the replacement carries the same id, and the peers
  will flip the address between them; votes from that identity are counted once where
  there are two voters. Kubernetes' at-most-one-pod-per-ordinal guarantee is what this
  rests on. Narrow, and not verified in a cluster.
- **The address-switch path is verified empirically, not read.** The behaviour was
  measured on 9.1.1 and 8.1.9; the Valkey source implementing it was not read, and
  nothing pins it for future majors beyond the e2e regression test.
- **The condition is only written where `CheckCluster` runs** — the branch of
  `updateHAStatus` where all Valkey and Sentinel pods are Ready. A cluster that never
  reaches that branch keeps whatever the last measurement said, and D7's recheck does
  not help there: a cluster stuck in Provisioning or Error is not measured at all.
- **The first True still depends on a pass happening.** D7 polls only once drift has
  been reported. Drift that appears while nothing else triggers a reconcile is seen at
  the next StatefulSet event or the cache resync, whichever comes first. With D1 in
  place drift can only arrive through a path D1 does not cover — a foreign Sentinel
  joining the monitor group — which is not a failure mode this operator produces.
- **Existing fleet drift is a manual step** (D6) and nothing enforces that it happens.
  The condition makes it visible; it does not make it mandatory.
- The wrong comment above `resetSentinelState` was left in place by this change and is
  corrected in the change that next touches that function.

## References

- [`internal/builder/sentinel.go`](../../internal/builder/sentinel.go) — `buildSentinelInitCommand`, `buildSentinelPodSpec`, `ComputeSentinelPodSpecHash`
- [`internal/builder/image_requirements.go`](../../internal/builder/image_requirements.go) — `RequiredImageTools`
- [`internal/valkeyclient/client.go`](../../internal/valkeyclient/client.go) — `SentinelMasterInfo.NumOtherSentinels`, `parseSentinelMasterInfo`
- [`internal/health/checker.go`](../../internal/health/checker.go) — `observeSentinels`, `ClusterState.SentinelPeers`
- [`internal/controller/valkey_controller.go`](../../internal/controller/valkey_controller.go) — `recordSentinelPeerDrift`, `staleSentinelPods`, `sentinelPeerDriftRecheckInterval`
- [`api/v1/valkey_types.go`](../../api/v1/valkey_types.go) — `ConditionTypeSentinelPeersStale`
- [`internal/builder/sentinel_init_script_exec_test.go`](../../internal/builder/sentinel_init_script_exec_test.go), [`test/e2e/sentinel_peer_table_test.go`](../../test/e2e/sentinel_peer_table_test.go)
- [ADR 0017](0017-test-and-ci-policy.md) — the tier this is verified in, and the image-tool contract
- [ADR 0021](0021-per-resource-metrics-and-the-alert-that-was-missing.md) — why a condition is enough to make this alertable
- [ADR 0007](0007-failover-aware-rolling-update.md) — the Sentinel tier roll this rides on
