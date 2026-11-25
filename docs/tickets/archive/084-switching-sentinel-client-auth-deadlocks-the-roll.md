---
id: T84
title: switching spec.sentinel.disableAuth on a running cluster deadlocks the data-tier roll, and the Sentinels never get the new auth setting
state: done           # fallback merged (#299), e2e green on Valkey 9 and 8 in CI; the unreported refusal moved to T23
severity: high        # the roll never completes, no automatic recovery, nothing reports it; data plane stays serving
security: boundary
threat: "a client on the pod network that reaches the Sentinel port (36379, no client certificate needed under tls-auth-clients optional) keeps issuing unauthenticated SENTINEL commands (SET, REMOVE, FAILOVER, RESET) to Sentinels the CR author has ordered to require the cluster password, for as long as the deadlock lasts, which has no bound"
urgency: next         # severity >= medium and the trigger is live: ADR 0016 D9 tells every user to switch disableAuth off after the migration
effort: M             # one client-side fallback shared by the operator, the health checker, the sidecar and the observer, a report, unit tests, one e2e per direction
filed-from: live incident, cluster stulz (kubeconfig stulz-admin), namespace iam, Valkey oauth2-valkey, 2026-11-24
opened: 2026-11-24
decided: 2026-11-24
done: 2026-11-24
shipped: Sentinel credential fallback (ADR 0016 D14) in every Sentinel client of the operator, health checker, sidecar and observer, merged to main in #299 (90c07aa); no release tag contains it yet
---

# T84 - switching spec.sentinel.disableAuth on a running cluster deadlocks the data-tier roll, and the Sentinels never get the new auth setting

## Current state

**The operator changes its Sentinel credential right away. The Sentinels change theirs only when their own pods roll, and that roll waits for the data tier, which needs a working Sentinel failover.**

- The operator works out its Sentinel password from the spec on every call, empty only under
  `IsSentinelAuthDisabled()`. On `main` this is `sentinelPassword`, used by all four Sentinel
  sites of the roll (`getSentinelMasterPodName`, `isSentinelAwareOfReplicas`,
  `resetSentinelState`, `triggerSentinelFailover`) and by the health checker's
  `observeSentinels`. On the branch it is `sentinelCredentials` plus the fallback (below).
- A running Sentinel keeps the auth setting it booted with. `requirepass` is written by the
  init container into an `emptyDir` config
  ([`sentinel.go:176-190`](../../../internal/builder/sentinel.go#L176-L190)), and only a new
  Sentinel pod picks up the changed ConfigMap.
- `disableAuth` is also part of the data pod spec: the sidecar arg `--sentinel-disable-auth=true`
  ([`statefulset.go:935-937`](../../../internal/builder/statefulset.go#L935-L937)) and the `-a`
  flags of `init-config-selector` (`statefulset.go:266-268`). Flipping it therefore starts a
  data-tier roll first.
- The data roll replaces the replicas and then has to fail over through Sentinel. The client
  sends or leaves out `AUTH` by the spec
  ([`client.go:473-490`](../../../internal/valkeyclient/client.go#L473-L490)), so every Sentinel
  refuses. The roll cycles through trigger, timeout, `resetSentinelState`, retrigger, and that
  cycle has no cap (the uncapped cycle itself is T23).
- The Sentinel tier is held while the data tier holds
  ([ADR 0026](../../adr/0026-a-pod-being-deleted-is-not-available.md), ADR 0024). The one roll that
  would bring the Sentinels to the new setting therefore never starts.
- Nothing reports this. The phase stays `Rolling Update 2/3`, `Ready=True`, and there is no
  `ReconcileBlocked`, no Warning and no condition naming the auth failure. The errors show only
  at debug level (`Could not query sentinel for master`, `Sentinel failover attempt failed`,
  `Sentinel remove failed (best-effort)`).

**Measured on 2026-11-24** (operator 1.15.1, `valkey/valkey:9.2.0-alpine`, TLS with
`unifiedCertificate`, 3 data pods + 3 Sentinels, direction `disableAuth: true` -> `false`):
Flux applied generation 4 at 12:52:53. pod-0 and pod-1 were replaced by 12:52:58, and
`FailoverTriggered` fired at 12:53:14. Every Sentinel answered `ERR AUTH <password> called
without any password configured for the default user`. Resets followed at 12:53:45 and 12:58:38,
retriggers at 12:54:10 and 12:59:04, and the cycle was still running at 12:59:29. The running
`sentinel.conf` read `# Auth (sentinel client auth disabled, auth-pass only)` with no
`requirepass`. The ConfigMap already carried `requirepass`. All three Sentinel pods were on
config hash `b66e93c0`, the template on `02257d52`. Master pod-2 was on the old revision, and its
sidecar still had `--sentinel-disable-auth=true`. 201 `AUTH failed` log lines in 30 minutes.

**The other direction, `false` -> `true`, was not measured on 1.15.1; it is read from the code.** The Sentinels keep
`requirepass`, the operator no longer sends `AUTH`, and every command gets `NOAUTH`. It is the
same deadlock, with no exposure, because the Sentinels stay stricter.

**This is the documented path.** [ADR 0016](../../adr/0016-authentication-and-tls-posture.md) D9
makes `disableAuth` migration-only: switch it off after the migration. Switching it off is
exactly what deadlocks.

**Impact.**

- On every Sentinel cluster with auth, flipping `disableAuth` stops the data roll right before
  the master. The master keeps running the old revision for good, and the Sentinel roll never
  starts.
- In the `true` -> `false` direction, the Sentinels keep accepting anonymous clients after the
  CR author has turned Sentinel auth on, with no bound. The CR shows no sign of it.
- Once someone breaks the deadlock by hand, `resetSentinelState` (blocked by `AUTH` until then)
  gets through, together with the T23 hazards of the reset.

**Way out today, by hand (measured on the same cluster):** delete one Sentinel pod. It boots
from the current template with the new setting, `AUTH` succeeds there, and the forced
`SENTINEL FAILOVER` of the retrigger, which needs no agreement from the other Sentinels, lets the
data roll finish. The Sentinel roll then replaces the rest. The sequence:

| Time (UTC) | Step |
|---|---|
| ~13:04:15 | sentinel-0 deleted by hand |
| 13:04:42 | `resetSentinelState` succeeded on sentinel-0 (sentinel-1 and sentinel-2 still refused `AUTH`) |
| 13:05:07 | forced failover triggered on sentinel-0 |
| 13:05:22 | pod-0 verified as the new master with 2 replicas; old master pod-2 deleted |
| ~13:05:30 | `RollingUpdateComplete` |
| 13:05:34 and 13:05:43 | the Sentinel roll replaced sentinel-1 and sentinel-2 |
| ~13:05:52 | `SentinelUpdateComplete` |
| 13:06:17 | phase `OK`, `known-master` on pod-0, no Warning Event on the CR |

The operator's last `AUTH failed` was on sentinel-1 and sentinel-2 at 13:05:3x, before they
rolled. The new master reported `dbsize 0`. The old master's key count was not read, so whether
the cluster was empty or a dataset was lost is not established (the promotion gate refuses an
empty candidate only while the master holds keys).

## Required changes

None left. The decision is [ADR 0016](../../adr/0016-authentication-and-tls-posture.md) D14
(Q1 answered A, the client fallback, by Hans on 2026-11-24). The operator-facing consequence is
in [`docs/operations/authentication.md`](../../operations/authentication.md#switching-specsentineldisableauth),
the `valkeyclient` row in [`docs/developer/package-map.md`](../../developer/package-map.md).
Verified: unit tests per reply and at the failover trigger, with a mutation check, and
`TestE2E_SentinelDisableAuthSwitchRollsBothTiers` green in both directions on Valkey 9 and
Valkey 8 (CI, run 45427536346 on 92544b3).

Moved out, not done here:

- The report of a refusal the fallback cannot bridge (`WRONGPASS`, an auth error on every
  Sentinel): T23, failover cycle.
- The 30 s Sentinel budget of `init-config-selector` after a switch to `true`: accepted, recorded
  as a consequence in ADR 0016 D14.
- Leader election among Sentinels with mixed `requirepass` settings: unverified, recorded in the
  Residual risks of ADR 0016.

## Not verified

- On operator 1.15.1, the `false` -> `true` direction is read from the code and was not
  measured. With the fallback, the e2e runs both directions.
- Leader election among Sentinels with mixed `requirepass` settings: see ADR 0016, Residual
  risks.
- The observer under a switch: the e2e cluster has no observer. The observer takes the fallback
  through `newSentinelClient` and `discoverMasterViaSentinel`, and no test drives it.

## Related

- T23: the uncapped failover cycle and the unverified `resetSentinelState`, which this deadlock
  runs through.
- T50: the cluster password changes and reaches no running pod. The same mechanism (the operator
  changes at once, the pods at their next start) for the password value rather than the Sentinel
  posture.
