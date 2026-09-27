---
id: T76
title: the exec probes pass on any server reply, so a loading or busy data pod is Ready
state: analysed       # exit codes measured in docker on both pinned lines, options costed
severity: low         # a loading or busy data pod is routed to and counted healthy; no data is lost
security: none        # the probe is no authentication control; passing with a wrong password gives no principal anything
urgency: now          # a code comment and an operations page state that the probe authenticates, which is false
effort: M             # option D: sidecar gate change in Go, tests, one e2e, ADR 0007 D9 amendment; items 1-3 are XS
blocked-by: decision  # Q1; the decision-free items are not blocked
filed-from: T51 (measurement M5)
opened: 2026-09-27
decided:
done:
---

# T76 - the exec probes pass on any server reply, so a loading or busy data pod is Ready

## Current state

**The exec probes.** Every exec probe the operator generates is one `valkey-cli ... ping`, and the
kubelet reads only its exit code. Without `-e`, `valkey-cli` exits 1 only when it cannot connect
(refused, failed TLS handshake); an error reply, to `AUTH` or to `PING`, is printed and the process
exits 0. The verdict is therefore "a server answered within `timeoutSeconds`", not "the server
answered `PONG`".

- Data tier: `ProbeCommand` ([`statefulset.go:1512-1545`](../../internal/builder/statefulset.go))
  builds four forms (auth and TLS, auth only, TLS only, neither), none with `--no-auth-warning` or
  `-e`. The same command is readiness (delay 5 s, period 5 s, timeout 3 s, failure threshold 3) and
  liveness (delay 15 s, period 10 s, timeout 5 s, failure threshold 5) of the `valkey` container
  ([`statefulset.go:847-870`](../../internal/builder/statefulset.go)). No startup probe exists.
- Sentinel tier: `SentinelProbeCommand` ([`sentinel.go:412-459`](../../internal/builder/sentinel.go))
  builds the same shape on 26379/36379 when TLS is on or auth is required, otherwise a `tcpSocket`
  probe (not covered here).
- The unit tests assert command strings only; the image-tools tier asserts stdout `PONG` for the
  no-auth, no-TLS data probe
  ([`restricted_runtime_test.go:104`](../../test/imagetools/restricted_runtime_test.go)). No test pins
  an exit code.

**The sidecar gate.** Every data pod also carries the sidecar, whose readiness is `GET /readyz`
(delay 3 s, period 3 s, timeout 2 s, failure threshold 3,
[`statefulset.go:1008-1020`](../../internal/builder/statefulset.go)). `/readyz` answers 200 once
`SetReady` has been called ([`health.go:36-38`](../../internal/sidecar/health.go),
[`:60-68`](../../internal/sidecar/health.go)); the only caller is the labeler poll (every 1 s) after
its first successful `DetectRole`, one `INFO replication` with the sidecar's start-time password
([`labeler.go:124-133`](../../internal/sidecar/labeler.go),
[`:190-204`](../../internal/sidecar/labeler.go)). Nothing sets it back, so the gate is latched for
the life of the sidecar container.

**Measured** (docker, `valkey/valkey:9.1.1` and `8.1.9`, the exact generated probe strings, no TTY):

| Server state | Probe prints | Exit |
|---|---|---|
| healthy, right password | `PONG` | 0 |
| wrong or empty password, or `requirepass` and no `-a` | `AUTH failed: WRONGPASS ...` / `NOAUTH ...` | 0 |
| loading after a restart, or loading the RDB of a full sync | `LOADING Valkey is loading the dataset in memory` | 0 |
| script past `busy-reply-threshold` | `BUSY Valkey is busy running a script ...` | 0 |
| nothing listening, or TLS CA mismatch | `Could not connect ...` | 1 |
| process stopped (`SIGSTOP`) | nothing, no answer within the timeout | none |

During a load `INFO replication` answers normally and `INFO persistence` shows `loading:1`; during a
script `INFO replication` answers `-BUSY`. With `-e` the probe exits 1 on every error reply,
including a wrong password. The candidate readiness command of option C exits 0 on `PONG` and on a
wrong password, 1 on `-LOADING`, `-BUSY` and no connection.

**Impact.**

- A persistent data pod whose `valkey` container restarts, and any replica loading a full sync,
  answers `-LOADING` to every command for the load, yet is Ready: in the `-rw`, `-r` and `-all`
  Services ([`service.go:171-224`](../../internal/builder/service.go)), counted healthy by the data
  PodDisruptionBudget and counted by the operator's `available()`
  ([`rolling_update.go:1899`](../../internal/controller/rolling_update.go)). A script past
  `busy-reply-threshold` has the same effect with `-BUSY`.
- Liveness is right as it is and must stay: it catches no connection and no answer, and failing it
  on `-LOADING`, `-BUSY` or an auth error would kill long loads, discard script work and turn a
  runtime password withdrawal into a crash loop. T51's design relies on the probes passing on
  `NOAUTH`.
- Two statements are false: the comment at [`sentinel.go:429`](../../internal/builder/sentinel.go)
  says the probe "must authenticate"; [`authentication.md:51-52`](../operations/authentication.md)
  says the replacement becomes Ready because its readiness probe authenticates. It becomes Ready
  because the exec probe passes on any reply and the sidecar authenticates once with the same Secret
  value.
- Cosmetic: the auth forms lack `--no-auth-warning`, so every probe run writes the `-a` warning to
  stderr.

**Rollout constraint for a probe change.** `ComputePodSpecHash` covers the whole `PodSpec`
([`statefulset.go:1228-1238`](../../internal/builder/statefulset.go)), so a new readiness command is
a pod-spec change. On the Helm path it adds no roll (every release already rolls multi-replica data
tiers for the sidecar image). For a single data pod, `singlePodDeferral`
([`pod_security_migration.go:128-150`](../../internal/controller/pod_security_migration.go)) decides
by `isSidecarOnlyChange`, which compares images only
([`rolling_update.go:3841-3866`](../../internal/controller/rolling_update.go)): on kustomize or a
floating tag the pod is replaced at once, a non-persistent one with its dataset. That is the ADR 0007
D7 case (traced by reading, not run).

## Required changes

### Independent of the open questions

1. Rewrite the comment at [`sentinel.go:429`](../../internal/builder/sentinel.go): the probe passes
   the password so a healthy Sentinel answers `PONG`; its verdict does not depend on it. Comment
   only.
2. Correct [`authentication.md:51-52`](../operations/authentication.md) to name both gates: the exec
   probe passes on any reply, the sidecar authenticates once with the same Secret value. T51 owns the
   rewrite of that page; do it once, in whichever change lands first.
3. Record the measured semantics in [ADR 0007](../adr/0007-failover-aware-rolling-update.md) D9: both
   exec probes pass on any reply and fail only on no connection or no answer; liveness keeps this on
   purpose; the sidecar gate latches at the first answered `INFO replication`, which a loading server
   gives. Write the measurement into the ADR itself.

Verification: `git grep -n "must authenticate" -- internal/builder/sentinel.go` returns nothing.

### Depends on the answers (option D)

4. In the labeler poll ([`labeler.go:120-160`](../../internal/sidecar/labeler.go)) detect loading
   (`loading:1` or `-LOADING`) and `-BUSY`, close the health server gate on either
   ([`health.go`](../../internal/sidecar/health.go)) and reopen it on the next normal answer; leave
   it unchanged on an auth error or a lost connection; closed before the first answer, as today.
5. Amend ADR 0007 D9 (sticky only across auth errors and lost connections), the sidecar row of
   [`architecture.md:86`](../developer/architecture.md) and the operations pages describing
   readiness. `ProbeCommand` and the pod spec stay unchanged.
6. Unit tests (`make test-unit`) on a labeler with a scripted detector and the health server: 503
   before any answer; a normal answer opens; `-LOADING`/`loading:1` closes; `-BUSY` closes; the next
   normal answer reopens; an auth error and a refused connection leave it open. Revert check:
   restoring the latch-only poll fails the loading and busy rows. Mutations: auth error as not
   ready, lost connection as not ready, dropped loading check, each fails its row.
7. Unit: `ComputePodSpecHash` for a fixed `Valkey` is unchanged, so no roll comes from the change.
8. E2E (`make test-e2e`, both lines): on a multi-replica cluster a replica running an endless `EVAL`
   past `busy-reply-threshold` leaves the ready endpoints of `-r` (`readyEndpointPodNames`) within
   15 s, returns after `SCRIPT KILL`, and its restart count is unchanged. The load case stays at the
   unit tier.

## Open questions

### Q1: What takes a loading or busy data pod out of Ready?

Today neither readiness gate sees a load, and neither sees `-BUSY` once the sidecar has latched.
Liveness, the Sentinel tier and ADR 0007 D9's rule that readiness is not replication health stay
unchanged under every option.

- **A - keep both gates, document them.** XS (items 1-3 only). A loading or busy pod stays routed
  to and counted healthy for its whole window, now as a documented property.
- **C - a readiness command of its own.** Readiness becomes `sh -c 'out=$(valkey-cli
  --no-auth-warning -a "$VALKEY_PASSWORD" ping 2>/dev/null) || exit 1; case "$out" in
  PONG*|NOAUTH*|WRONGPASS*) exit 0;; esac; exit 1'` (with today's TLS arguments, without `-a` when
  auth is off); liveness stays. M: measured end to end, no restart window, stays independent of the
  password; but it is a pod-spec change and needs an ADR 0007 D7 treatment for the single pod
  (re-deciding ADR 0007 D6). A loading pod whose probe holds a password the server rejects still
  counts as ready.
- **D - the sidecar gate closes on `-LOADING` and `-BUSY` (recommended).** Items 4-8. M: ships
  inside the sidecar image, no pod-spec change, no roll of its own, no D7 treatment; auth behaviour
  unchanged. Costs: a stalled poll freezes the gate in its current state, and at a `valkey` container
  restart a few seconds may pass before the gate closes; designed, not measured end to end.

D removes the defect at the one place that already reads the server every second and cannot become
the ADR 0007 D7 data-loss change on an operator upgrade. If a D7 treatment lands for another reason
first, C becomes the better option (S, fully measured).

**Answer:** _open_

## Not verified

- Cluster behaviour: that a loading or busy pod stays in the `-r` EndpointSlice, that the PDB admits
  an eviction meanwhile, and how long a real `-LOADING` window lasts. Settled by a Kind run with a
  large dataset.
- Under D, how long a pod stays Ready after the sidecar sees `loading:1`, and whether the `valkey`
  container opens a window before the gate closes. Settled by the D e2e or a timed Kind run.
- That a Sentinel never answers `PING` with `-LOADING` or `-BUSY` (inferred: no dataset, no
  scripts). Settled by reading `sentinel.c`.
- The single-pod replacement on a probe-only pod-spec delta (traced by reading). Settled by a unit
  test of `singlePodDeferral` with a probe-only drift; matters only for option C.

## Related

- T51 - password rotation; its design relies on the probes passing on `NOAUTH` and owns the
  `authentication.md` rewrite.
- T36 - non-persistent master restarts empty; reasons from the liveness probe semantics measured
  here.
- T12 - the same `valkey-cli` exit-0-on-error behaviour for `--raw SET`; `-e` is the switch.
- T23 - states the readiness probe is a `PING` an unsynced replica passes; true under every option.
