---
id: T76
title: the exec probes pass on any server reply, so a loading or busy data pod is Ready
state: analysed       # every exit code measured in docker on both pinned lines with the exact generated probe strings, the sidecar gate read and its inputs measured, the valkey-cli source read at both tags, four options costed and one marked (History 2026-09-27)
severity: low         # if never fixed: a data pod loading its dataset (after a restart, or while loading the RDB of a full sync) or blocked by a long script is Ready and routed to by -rw/-r, counted as healthy by the PodDisruptionBudget and as available by the operator, and answers every client command with an error for that window. Not higher: no data is lost, the liveness verdict is the right one (Impact, case 5), and a restarted master fails writes for its load window whether or not it is Ready
security: none        # the probe is no authentication control and no document claims it is one; that it passes with a wrong password gives no principal anything (Impact, "Not a security finding")
urgency: now          # rule 1 as this repository applies it (the 018/023/059 precedent): internal/builder/sentinel.go:429 says the probe "must authenticate", and the same probe without -a passes identically (measured, both pins); docs/operations/authentication.md:51-52 gives the probe authenticating with the pod's own password as the reason a replacement becomes Ready, and that probe passes with any password (measured); what needs a password is the sidecar gate, which the sentence does not name (a correction T51's Work list item 3 carries in part, Work list item 2). Once Work list items 1 and 2 have landed, recompute: rules 2-4 do not match (no release gate, severity low, no decided or cheap fix: every code option is M and re-decides ADR 0007 D9, B and C also D6), so icebox by rule 5
effort: M             # option D: the sidecar gate closing on -LOADING and -BUSY (S in Go), unit tests with revert and mutation checks, one e2e on both lines, the ADR 0007 D9 amendment; no pod-spec change. C and B are M as well, through the ADR 0007 D7 treatment they need; items 1-3 of the Work list are XS
blocked-by: decision  # the one decision under Options (what takes a loading or busy data pod out of Ready); Work list items 1-3 are not blocked
filed-from: T51 (ticket 051, measurement M5, the cross-ticket paragraph "The probe exit-code finding (M5) is a different mechanism", Work list item 6) during the re-verification of 2026-09-27 at 84a39c2
opened: 2026-09-27
decided:              # not recorded - no decision yet
done:                 # not recorded - not done
---

# T76 - the exec probes pass on any server reply, so a loading or busy data pod is Ready

Filed on 2026-09-27 from ticket 051 (a changed cluster password reaches no running pod), whose
re-verification at `84a39c2` measured the `valkey-cli` exit codes as its M5 and decided that the
probe finding is a different mechanism with its own decision. This file is the durable record of
that finding; 051 keeps a pointer.

## Fact

**Mechanism.** Every exec probe the operator generates is one `valkey-cli ... ping`, and the
kubelet reads only its exit code. `valkey-cli` in non-interactive mode exits 1 only when it cannot
connect; an error reply, to `AUTH` or to `PING`, is printed and the process exits 0. So the probe
verdict is "a server answered within `timeoutSeconds`", not "the server answered `PONG`": an
authentication error, `-LOADING`, `-BUSY` and `-MASTERDOWN` all pass both the readiness and the
liveness probe. A data pod has a second readiness gate, the sidecar's `/readyz`, and it does not
close the gap: it latches Ready at the first `INFO replication` that answers, and a loading server
answers `INFO replication` (measured below).

**Verified** (read at `84a39c2`; `git diff --stat HEAD -- internal api test cmd` is empty in the
working tree of 2026-09-27):

- **The data-tier probe.** `ProbeCommand`
  ([`statefulset.go:1512-1545`](../../internal/builder/statefulset.go)) builds four forms: auth and
  TLS, `sh -c 'valkey-cli --tls --cert /tls/tls.crt --key /tls/tls.key --cacert /tls/ca.crt -p 16379
  -a "$VALKEY_PASSWORD" ping'` ([`:1520-1523`](../../internal/builder/statefulset.go)); auth only,
  `sh -c 'valkey-cli -a "$VALKEY_PASSWORD" ping'` ([`:1525-1528`](../../internal/builder/statefulset.go));
  TLS only, the argv `valkey-cli --tls --cert /tls/tls.crt --key /tls/tls.key --cacert /tls/ca.crt -p
  16379 ping` ([`:1534-1542`](../../internal/builder/statefulset.go)); neither, the argv `valkey-cli ping`
  ([`:1544`](../../internal/builder/statefulset.go)). None carries `--no-auth-warning` or `-e`. The
  same command is the readiness probe (initial delay 5 s, period 5 s, timeout 3 s, failure
  threshold 3, success threshold 1) and the liveness probe (initial delay 15 s, period 10 s,
  timeout 5 s, failure threshold 5) of the `valkey` container
  ([`statefulset.go:847-870`](../../internal/builder/statefulset.go)). There is no startup probe
  in `internal/builder`.
- **The Sentinel probe.** `SentinelProbeCommand`
  ([`sentinel.go:412-459`](../../internal/builder/sentinel.go)) builds auth and TLS,
  `sh -c 'valkey-cli --tls --cacert /tls/ca.crt -p 36379 -a "$VALKEY_PASSWORD" ping'`
  ([`:434-438`](../../internal/builder/sentinel.go)); auth only, `sh -c 'valkey-cli -p 26379 -a
  "$VALKEY_PASSWORD" ping'` ([`:440-443`](../../internal/builder/sentinel.go)); TLS only, the argv
  `valkey-cli --tls --cacert /tls/ca.crt -p 36379 ping` ([`:448-456`](../../internal/builder/sentinel.go)).
  The exec handler is used when TLS is on or Sentinel auth is required; otherwise the probe is a
  `tcpSocket` on 26379 ([`:483-503`](../../internal/builder/sentinel.go)), which this ticket does not
  cover. Readiness and liveness share the handler and the data tier's timings
  ([`:537-552`](../../internal/builder/sentinel.go)).
- **Why `valkey-cli` exits 0 on an error reply** (upstream source, read at tags `9.1.1` and `8.1.9`,
  [valkey-cli.c at 9.1.1](https://github.com/valkey-io/valkey/blob/9.1.1/src/valkey-cli.c)). With a
  command on the command line, `main` calls `cliConnect(CC_QUIET)`, ignores its result and calls
  `noninteractive` (9.1.1 `:10262-10263`). An error reply to `AUTH` prints `AUTH failed: ...` to
  stderr (`:1557-1559`) and makes `cliConnect` return early (`:1679`), but the connection stays
  open and the command is sent anyway, so `PING` draws `NOAUTH`. An error reply to the command
  exits 1 only when `config.set_errcode` is set (`:2229-2233`), which only the `-e` flag does
  (`:2714-2715`; help text `:3012`, "Return exit error code when command execution fails."); without
  it `noninteractive` returns 0 (`:3488`). A connection that cannot be made (refused, or a failed
  TLS handshake) leaves no context and returns 1. The same code is at 8.1.9 (`AUTH failed` `:1553`,
  `set_errcode` `:2228`, `-e` `:2708`, help `:2974`).
- **What the code and the documents say the probe does.** The comment at
  [`sentinel.go:429`](../../internal/builder/sentinel.go) says "the probe must authenticate"; the
  same probe without `-a` against a Sentinel with `requirepass` passes (`NOAUTH`, exit 0, measured
  below). [`docs/operations/authentication.md:51-52`](../operations/authentication.md) says a
  replaced replica's "readiness probe authenticates with the pod's own password, so the replacement
  still becomes Ready"; that probe passes with any password (measured below), and the pod-level
  outcome rests on the sidecar gate (next bullet). The `ProbeCommand`
  comment ([`statefulset.go:1512-1514`](../../internal/builder/statefulset.go), "accounting for TLS
  and auth") describes the arguments and is true. [ADR 0007](../adr/0007-failover-aware-rolling-update.md)
  D9 (`:317-322`) says readiness "reflects server liveness, not replication health", that the
  readiness probe is a plain `PING` and that the sidecar `/readyz` is sticky once a role has been
  observed; true, and silent on error replies.
- **The sidecar is the second readiness gate of a data pod.** Every data pod carries the sidecar
  container ([`statefulset.go:1044-1048`](../../internal/builder/statefulset.go),
  `buildPodContainers`), whose readiness probe is an HTTP `GET /readyz` (initial delay 3 s, period
  3 s, timeout 2 s, failure threshold 3,
  [`statefulset.go:1008-1020`](../../internal/builder/statefulset.go)); a pod is Ready only when
  every container is. `/readyz` answers 200 once `SetReady` has been called
  ([`health.go:36-38`](../../internal/sidecar/health.go), [`:60-68`](../../internal/sidecar/health.go)),
  and the only caller is the labeler's poll, every 1 s (`--poll-interval=1s`,
  [`statefulset.go:916`](../../internal/builder/statefulset.go)), after the first `DetectRole` that
  succeeds ([`labeler.go:124-133`](../../internal/sidecar/labeler.go)); nothing ever sets it back
  (`git grep -n "SetReady\|ready.Store" 84a39c2 -- internal/sidecar cmd/sidecar` finds the
  definition and that one call). `DetectRole` is one `INFO replication` with the sidecar's own
  start-time password ([`labeler.go:190-204`](../../internal/sidecar/labeler.go),
  [`client.go:214-215`](../../internal/valkeyclient/client.go)). So the gate needs the sidecar to
  authenticate once, and after that it is Ready for the life of the sidecar container, through
  every later auth error, `-BUSY`, `-LOADING` and restart of the `valkey` container. The sticky
  half is pinned by `TestHealthServer_ReadyzReady`
  ([`health_test.go:40`](../../internal/sidecar/health_test.go)).
- **A pod-spec change on an operator upgrade.** Every data-tier site asks `podOutdated`, which
  compares the sidecar image before any hash
  ([`rolling_update.go:425-449`](../../internal/controller/rolling_update.go), `podImageChanged`
  [`:485-500`](../../internal/controller/rolling_update.go)), so on the Helm path, where every
  release moves the operator image and with it the sidecar image, every release already rolls every
  multi-replica data tier once, failover-aware. For the single data pod of a `spec.replicas: 1`
  cluster, `singlePodDeferral`
  ([`pod_security_migration.go:128-150`](../../internal/controller/pod_security_migration.go))
  decides a rootless pod by `isSidecarOnlyChange` alone, which compares **images only**
  ([`rolling_update.go:3841-3866`](../../internal/controller/rolling_update.go)): a probe change
  shipped with a new sidecar image is deferred with it (`SidecarUpdatePending`, until the pod's next
  restart), while on kustomize or a floating tag, where the sidecar image string does not move (the
  case the comment at [`pod_security_migration.go:109-114`](../../internal/controller/pod_security_migration.go)
  names for the root posture), the pod-spec hash differs, the pod is outdated and is replaced at
  once, a non-persistent one with its dataset. This is the data-loss change ADR 0007 D7
  (`:273-278`) says any single-replica pod-spec delta beyond the sidecar image must be treated as.
  Traced by reading at `84a39c2`; not run.
- **What the tests check.** The unit tests assert the command strings only
  ([`statefulset_test.go:310-323`](../../internal/builder/statefulset_test.go), `:568-604`;
  [`sentinel_test.go:1033-1105`](../../internal/builder/sentinel_test.go), `:1224-1270`). The image
  tier runs the no-auth, no-TLS data probe against a running `valkey-server` and asserts its
  **stdout** is `PONG`
  ([`restricted_runtime_test.go:104`](../../test/imagetools/restricted_runtime_test.go), `:110`,
  `:131`), a stronger property than the kubelet reads; no test pins an exit code.
- **Where the probe and the server can hold different passwords.** On a data pod the server's
  `requirepass` and the probe's `-a` are both `$VALKEY_PASSWORD` of the same container at the same
  start ([`statefulset.go:830`](../../internal/builder/statefulset.go), env
  [`:876-887`](../../internal/builder/statefulset.go)), so they agree unless the password is changed
  at runtime (`ACL SETUSER`, `CONFIG SET requirepass`), which the operator never does today and
  which T51's runbook and T51's option C do. On a Sentinel pod `requirepass` is written into the
  config on an `emptyDir` by the init container ([`sentinel.go:184-188`](../../internal/builder/sentinel.go),
  the `sed` at [`:715`](../../internal/builder/sentinel.go); every Sentinel volume is an `emptyDir`,
  [`:405-406`](../../internal/builder/sentinel.go)), while the probe reads the container's env.
- **Who reads data-pod readiness.** The `-rw`, `-r` and `-all` Services publish Ready addresses
  only ([`service.go:171-224`](../../internal/builder/service.go), no `PublishNotReadyAddresses`);
  the data headless Service publishes not-ready ones as well
  ([`service.go:154`](../../internal/builder/service.go)), as does the Sentinel headless Service
  ([`:237`](../../internal/builder/service.go)).
  The operator's `available()` is Ready and not terminating
  ([`rolling_update.go:1899`](../../internal/controller/rolling_update.go), `isPodReady`
  [`:620-627`](../../internal/controller/rolling_update.go)). The data PodDisruptionBudget
  ([`pdb.go`](../../internal/builder/pdb.go)) counts Ready pods as healthy (Kubernetes PDB
  semantics, not re-read upstream in this filing). The generated config keeps
  `replica-serve-stale-data yes` and sets `repl-diskless-sync yes` with a 5 s delay, and no
  `repl-diskless-load` ([`configmap.go:176-179`](../../internal/builder/configmap.go)), so a replica
  loads a full sync from disk (upstream default `disabled`, `config.c` 9.1.1:3368, measured below).
- **The pod-spec hash covers the probe.** `ComputePodSpecHash` is FNV-32a over the JSON of the
  whole built `PodSpec` ([`statefulset.go:1228-1238`](../../internal/builder/statefulset.go)), so any
  change to the probe command is a pod-spec change that rolls every data tier it reaches.

*Measured 2026-09-27 in docker on `valkey/valkey:9.1.1` and `valkey/valkey:8.1.9` (the two pinned
lines, [`test/testimages/images.go:40`](../../test/testimages/images.go), `:45`); identical on both
lines unless stated.* Setup: `docker run -d --rm --name vko-file-076-<line> --user 999:999 -w /data
-v <certs>:/tls:ro <image> sleep 900`; an `sh -c` probe runs as `docker exec -e
VALKEY_PASSWORD=<pw> <ctr> timeout 4 sh -c '<probe string>'`, an argv probe as `docker exec <ctr>
timeout 4 valkey-cli ...`, both without a TTY, as the kubelet execs. Servers: data
`valkey-server --port 6379 --save "" --daemonize yes --requirepass right --busy-reply-threshold 1000`;
data TLS `valkey-server --port 0 --tls-port 16379 --tls-cert-file /tls/tls.crt --tls-key-file
/tls/tls.key --tls-ca-cert-file /tls/ca.crt --tls-auth-clients optional --requirepass right` (the
generated TLS lines, [`configmap.go:78-95`](../../internal/builder/configmap.go)); Sentinel from a
config of `port 26379` (TLS: `port 0`, `tls-port 36379` and the same TLS lines), `sentinel monitor
mymaster 127.0.0.1 6379 1`, `requirepass right`, `sentinel auth-pass mymaster right`. Certificates:
a throwaway CA and one server/client certificate with SAN `localhost`/`127.0.0.1` (host openssl
3.6.3), plus an unrelated second CA. `-LOADING` after a restart: `--enable-debug-command yes`,
`DEBUG POPULATE 100000`, `SAVE`, `SHUTDOWN NOSAVE`, restart with `--key-load-delay 50
--loading-process-events-interval-bytes 1024` (both hidden upstream configs, `config.c`
9.1.1:3426, `:3495`), probe 1 s and about 60 s later, `INFO persistence` showing `loading:1`.
`-LOADING` of a full sync: a master on 6380 holding 100000 keys, a replica on 6379 with the same two
flags, `REPLICAOF 127.0.0.1 6380`, the argv probe once a second. `-BUSY`: `EVAL "local i=0 while
true do i=i+1 end" 0` in the background, probe 2.5 s later, then `SCRIPT KILL`. Hang: `kill -STOP`
on `valkey-server`, then `kill -CONT`. Every container was removed (`docker ps -a --filter
name=vko-file-076` empty; no network was created).

| Generated probe | Server state | Printed | Exit |
|---|---|---|---|
| argv `valkey-cli ping` (`statefulset.go:1544`) | healthy | `PONG` | 0 |
| same | `requirepass` set | `NOAUTH Authentication required.` | 0 |
| same | nothing listening | `Could not connect to Valkey at 127.0.0.1:6379: Connection refused` | 1 |
| same, on a replica | loading the RDB of a full sync (seconds 5-6; seconds 1-4 answered `PONG` with `master_link_status:down`, `master_sync_in_progress:0`) | `LOADING Valkey is loading the dataset in memory`, `master_sync_in_progress:1`, `loading:1` | 0 |
| `sh -c` auth (`:1525-1528`) | right password | the `-a` warning on stderr, `PONG` | 0 |
| same | wrong password, or `VALKEY_PASSWORD` empty | `AUTH failed: WRONGPASS invalid username-password pair or user is disabled.` and `NOAUTH Authentication required.` | 0 |
| same | script past `busy-reply-threshold` | `BUSY Valkey is busy running a script. You can only call SCRIPT KILL or SHUTDOWN NOSAVE.` | 0 |
| same | `SIGSTOP` | nothing | none: still running when `timeout 4` killed it (124) |
| same | loading after a restart, at 1 s and at about 60 s | `LOADING Valkey is loading the dataset in memory` | 0 |
| same | loading, wrong password | `AUTH failed: WRONGPASS ...` | 0 |
| same | nothing listening | `Could not connect ...: Connection refused` | 1 |
| `sh -c` auth and TLS (`:1520-1523`) | right / wrong password / nothing listening | `PONG` / `AUTH failed: WRONGPASS ...` / `Could not connect ...` | 0 / 0 / 1 |
| argv TLS (`:1534-1542`) | no `requirepass` / `requirepass` set | `PONG` / `NOAUTH ...` | 0 / 0 |
| variant, not generated: `--cacert` pointing at the unrelated CA | healthy | `Could not connect to Valkey at 127.0.0.1:16379: SSL_...` | 1 |
| Sentinel `sh -c` auth (`sentinel.go:440-443`) | right / wrong password / nothing listening | `PONG` / `AUTH failed ...` (8.1.9 prints the two lines in the other order) / `Could not connect ...` | 0 / 0 / 1 |
| Sentinel `sh -c` auth and TLS (`:434-438`) | right / wrong password / nothing listening | `PONG` / `AUTH failed ...` / `Could not connect ...` | 0 / 0 / 1 |
| Sentinel argv TLS (`:448-456`) | `requirepass` set | `NOAUTH Authentication required.` | 0 |

Without a TTY the error replies (`NOAUTH`, `BUSY`, `LOADING`) are written to **stdout** and the
`AUTH failed` line to stderr (measured with `2>/dev/null`). The same session measured the
candidate probe forms of Options, with the auth server above:

| Candidate | healthy | wrong password | `-BUSY` | `-LOADING` | `-LOADING`, wrong password | nothing listening |
|---|---|---|---|---|---|---|
| `valkey-cli -e -a "$VALKEY_PASSWORD" ping` | 0 | 1 | 1 | 1 | 1 | 1 |
| `valkey-cli -a "$VALKEY_PASSWORD" ping 2>/dev/null \| grep -q PONG` | 0 | 1 | 1 | 1 | not run | 1 |
| option C's form (Options) | 0 | 0 | 1 | 1 | 0 | 1 |

With `-e` the error reply is printed to stderr and the process exits 1 (`valkey-cli.c` 9.1.1
`:2229-2233`).

T51's M5, the source of this ticket (docker, both lines, the same day, re-run by both reviewers of
that run): `valkey-cli --no-auth-warning -a wrong ping; echo $?` exits 0 (prints `AUTH failed:
WRONGPASS ...` and `NOAUTH Authentication required.`), also via `sh -c`; `-a right ping` exits 0;
an error reply, `-a right nosuchcommand`, exits 0; nothing listening, `-p 6390 ping`, exits 1.

*Measured in the adversarial review of 2026-09-27, both lines, what the sidecar gate reads:* in a
container started as above, a server with `--requirepass right` restarted to load 100000 keys with
`--key-load-delay 50 --loading-process-events-interval-bytes 1024`, one second after the restart:
`valkey-cli --no-auth-warning -a right ping` prints `LOADING Valkey is loading the dataset in
memory` (exit 0), `... info replication` answers `# Replication`, `role:master`,
`connected_slaves:0` (exit 0), `... info persistence` shows `loading:1`, `... role` answers
`master`, and `-a wrong info replication` gets `AUTH failed: WRONGPASS ...` and `NOAUTH
Authentication required.`. With a script past `--busy-reply-threshold 1000` (the `EVAL` above),
`ping` and `info replication` both answer `BUSY Valkey is busy running a script. You can only call
SCRIPT KILL or SHUTDOWN NOSAVE.` (exit 0). Containers `vko-file-076r-*` and `vko-file-076b-*`, run
with `--rm`, none left. Option D was not built; these are its inputs, not its verdicts.

A first run of the restart case without `--loading-process-events-interval-bytes 1024` and with
300000 keys (9.1.1) got **no answer within 4 s** (exit 124 from `timeout`): during a load the
server serves clients only between chunks of `loading-process-events-interval-bytes`, 2 MB by
default (`config.c` 9.1.1:3495), and the artificial per-key delay made each chunk slower than the
probe timeout.

**Not verified:**

- Anything on a cluster: that a loading or busy data pod stays in the `-r` EndpointSlice, that the
  PodDisruptionBudget admits an eviction while a data pod loads, and how long a real `-LOADING`
  window lasts for a given dataset and volume. The docker runs used an artificial load delay.
- That the kubelet fails an exec probe that runs past `timeoutSeconds`. It is the documented
  behaviour and [ticket 036](036-non-persistent-master-restarts-empty.md) reasons from it, but this
  filing did not measure it or re-read it upstream.
- That a real load is ever slow enough to starve the probe as the first run above did. Measured only
  with `key-load-delay`; no slow volume was tried.
- The Sentinel mismatch case (a Sentinel container restarted in place after a Secret change keeps
  the old `requirepass` from its `emptyDir` config while its probe reads the new env). Read: init
  containers do not re-run on a container restart, and T51 read the kubelet re-resolving a
  `secretKeyRef` at every container start. Not reproduced.
- Any run of the single-replica paths of Verified ("A pod-spec change on an operator upgrade"):
  traced by reading only, no unit test or cluster run of a probe-only drift.
- The timing of the sidecar gate under option D: how long a pod stays Ready after the sidecar sees
  `loading:1` (up to three failed `/readyz` probes, 3 s apart, plus one poll) and whether the
  `valkey` container, Ready 5 s after a restart, opens a window before the sidecar gate closes.
  Derived from the probe timings in Fact, not measured.
- That the Sentinel tier never answers `PING` with `-LOADING` or `-BUSY` (Options). Inferred: a
  Sentinel holds no dataset and serves no scripts; not measured, `sentinel.c` not read for it.
- Which operator waits lengthen when a loading pod is not Ready. Inferred that none does beyond the
  load: the roll's own waits read replication state through `INFO` (ADR 0007 D9, D10), and every
  wait on an unavailable pod is bounded by `spec.rollingUpdate.syncTimeout`, default 5 m
  ([`valkey_types.go:1012-1021`](../../api/v1/valkey_types.go), ADR 0026 D11). Not traced site by
  site.

## Impact

Per server reply, for the data tier unless stated:

1. **`-LOADING`.** A persistent data pod whose `valkey` container restarts, and any replica that
   loads the RDB of a full sync, answers `-LOADING` to every command until the load ends. A fresh
   container becomes Ready at its first probe (5 s in, success threshold 1) while it loads, and in a
   fresh pod the sidecar gate opens as well, because `INFO replication` answers during the load
   (measured); an already Ready replica stays Ready through a full-sync load (under any fix it needs
   three failed probes of the gate that closes, about 7-15 s, to leave). While Ready it is in the
   `-rw` or `-r` Service, so clients routed to it get `-LOADING`; on a tier with several replicas,
   `-r` sends a share of reads to the loading replica while healthy ones exist. The
   PodDisruptionBudget counts it healthy, so an eviction of another data pod can go ahead (read, not
   measured), and `available()` counts it. On a restarted master writes fail for the load either
   way; Sentinel counts `-LOADING` as available too and does not fail it over (ticket 036,
   `sentinel.c` 9.1.1:2729-2731, measured there).
2. **`-BUSY`.** A script running past `busy-reply-threshold` (default 5000 ms, `config.c`
   9.1.1:3460) leaves the pod Ready and routed to, the same way. The liveness verdict is the right
   one: killing the container discards the script's work and, on a non-persistent pod, the dataset.
3. **Authentication errors.** Passing is benign and is relied upon: T51's D2 reasons that "the
   probes do not matter, because `valkey-cli` exits 0 on `NOAUTH`". A probe that failed on an auth
   error would turn every runtime withdrawal of a password, and the Sentinel mismatch case above,
   into a readiness outage (and, for liveness, a crash loop). The sidecar gate does need one
   successful authentication, by the sidecar with its own start-time password, before a fresh pod
   is Ready; in a fresh pod that is the same Secret value the server started with, and afterwards
   the gate is latched. So [`authentication.md:51-52`](../operations/authentication.md) names the
   wrong component, not a wrong outcome: the replacement becomes Ready because its exec probe passes
   on any reply and its sidecar authenticates once against its own server, not because "its
   readiness probe authenticates". The `sentinel.go:429` comment is wrong outright.
4. **`-MASTERDOWN`.** Not reachable with the generated config (`replica-serve-stale-data yes`); 036
   measured that its probe exits 0 on it too, which matters only if 036's fallback config appends
   `replica-serve-stale-data no`.
5. **Liveness.** It catches what it should: nothing listening (exit 1) and a hung process (no
   answer). A liveness that failed on `-LOADING` would kill a large load 55-65 s after the
   container start (036's budget) and repeat it on every restart; today that cannot happen. The
   remaining edge is the starved probe of the first run above, a load so slow that no chunk
   completes within 5 s, which would be killed; not seen on real storage.
6. **Cosmetic.** The auth forms lack `--no-auth-warning`, so every probe run writes the `-a`
   warning to stderr.

**Not a security finding.** The probe runs inside the pod's own `valkey` container with that
container's own password; it is no authentication control, no document or ADR states a guarantee
that depends on its verdict, and a probe that passes with a wrong password gives no principal
access to anything. Hence `security: none` and no embargo.

## Options

**One decision: what takes a loading or busy data pod out of Ready.**

**Mechanism today.** A data pod is Ready when both of its gates are: the `valkey` container's exec
probe, which passes on any reply, and the sidecar's `/readyz`, which latches Ready at the first
`INFO replication` that answers and never closes again (Fact). A loading server answers that
`INFO` (measured), so neither gate sees a load, and neither sees `-BUSY` once the sidecar has
latched. The liveness half is right as it is (Impact, cases 2, 3 and 5): it must not fail on
`-LOADING`, `-BUSY` or an auth error, so no option below changes it. The Sentinel probe needs no
change either: a Sentinel holds no dataset and serves no scripts, so it answers `PING` with `PONG`
or an auth error (inferred, Not verified). **What the decision changes:** whether a loading or busy
data pod is in the `-rw`, `-r` and `-all` Services, counted by the PodDisruptionBudget and counted
by `available()`. **What it does not change:** liveness, the Sentinel tier, and ADR 0007 D9's rule
that readiness is not a proxy for replication health (a replica with a broken link still answers
`PONG`, measured above; no option keys on `master_link_status` or `master_sync_in_progress`).

**Cost common to B and C: a pod-spec delta.** The readiness command is part of the pod spec, and
the pod-spec hash covers the whole `PodSpec` (Fact). On the Helm path that adds no roll: every
release already rolls every multi-replica data tier for the sidecar image (Fact). For the single
data pod of a `spec.replicas: 1` cluster it is the ADR 0007 D7 case (Fact, traced by reading):
deferred with the sidecar image on the Helm path, replaced at once on kustomize or a floating tag,
and a non-persistent one loses its dataset in an operator upgrade. B or C therefore ships only
together with a D7 treatment, most plausibly `singlePodDeferral` deferring a non-persistent rootless
single pod whose only drift is the pod-spec hash, the way ADR 0032 D3 did for the root posture; that
re-decides ADR 0007 D6 and is M on its own.

- **A — keep both gates, write their semantics down.** *Cost:* XS: ADR 0007 D9 gains the measured
  rule (the exec probe passes on any reply and fails only on no connection or no answer; the
  sidecar gate latches at the first answered `INFO`, which a loading server gives), and the two
  statements of Work list items 1 and 2 are corrected. *Consequences:* no roll, no code; a loading
  or busy data pod stays in the Services and counted as healthy for its whole window, now as a
  documented property.
- **B — readiness with `-e`.** The data container's readiness becomes today's command plus `-e`,
  liveness stays. *Cost:* M: one flag (measured, both pins), unit and image-tools tests, the ADR
  0007 D9 amendment, the D7 treatment above. *Consequences:* closes `-LOADING`, `-BUSY` and
  `-MASTERDOWN`; also makes readiness fail whenever the probe's password is not accepted. Then a
  runtime `CONFIG SET requirepass` (which replaces every password, T51 M2) or any runtime withdrawal
  of the password a pod started with empties `-rw` and `-r` while the servers still serve; T51's D2
  premise "the probes do not matter" stops holding and its option C would have to order every
  withdrawal around readiness. The `grep -q PONG` form measured above gives the same verdicts with a
  pipe and one more tool, and is not kept.
- **C — a readiness command of its own: `PONG` or an auth error is ready, any other reply is
  not.** The data container's readiness becomes `sh -c 'out=$(valkey-cli --no-auth-warning -a
  "$VALKEY_PASSWORD" ping 2>/dev/null) || exit 1; case "$out" in PONG*|NOAUTH*|WRONGPASS*) exit 0;;
  esac; exit 1'`, with today's TLS arguments and without `-a` when auth is off; liveness stays.
  *Cost:* M: S in code (a builder function for the readiness command, no new image tool: `case` is a
  shell builtin, `sh` and `valkey-cli` are already in `RequiredImageTools`), unit and image-tools
  tests, the ADR 0007 D9 amendment, and the D7 treatment above. *Consequences:* closes `-LOADING`,
  `-BUSY` and `-MASTERDOWN` (measured: exit 1 on the first two, 0 on `PONG` and on a wrong
  password); a restarted `valkey` container is never Ready while it loads, because its readiness
  starts false and its first probe already fails (derived from the measured exit codes); readiness
  stays independent of the password, so T51's D2 premise and every rotation path hold unchanged. It
  rests on the reply prefixes, which are the protocol's error codes. One gap is measured and
  accepted: a loading server checks the password before it reports loading, so a loading pod whose
  probe holds a password the server does not accept answers `NOAUTH` and counts as ready; that needs
  a runtime password change in the middle of a load.
- **D — the sidecar gate closes on `-LOADING` and `-BUSY` (recommended).** The sidecar already
  polls its own server every second and is already a readiness gate of every data pod. D makes that
  gate two-way for exactly two server states: a poll that finds the server loading (`loading:1` in
  `INFO persistence`, or `-LOADING` to a `PING`; `INFO replication` alone answers during a restart
  load, measured) or gets `-BUSY` (which `INFO replication` returns, measured) makes `/readyz`
  answer 503, and the next poll with a normal answer opens it again. Every other outcome, an auth error or no
  connection, leaves the gate as it is, so the latch keeps its meaning for those; before the first
  answer the gate stays closed, as today. *Cost:* M: S in Go (one more command or field per poll in
  the labeler, a way to close the health server, the error classification), unit tests on the
  labeler with a scripted detector and on the health server, one e2e, the ADR 0007 D9 amendment
  (its sticky half narrowed to "sticky across auth errors and lost connections"). **No pod-spec
  change:** it ships inside the sidecar image, which every release moves anyway, so there is no roll
  of its own and no D7 treatment. *Consequences:* closes `-LOADING` and `-BUSY` at the pod level;
  `-MASTERDOWN` only if the same rule is given to it, which the generated config cannot reach
  (Impact, case 4); the exec probe keeps its measured semantics, so T51's D2 premise holds and
  readiness stays independent of the password after the first authentication, as today. What it
  costs: the pod's Service membership now also moves with a Go loop that carries the label patch
  ([`labeler.go:154`](../../internal/sidecar/labeler.go)), so a stalled poll freezes the gate in
  whatever state it holds, which today is always Ready and under D can be not-Ready during a load;
  the gate closes only after three failed `/readyz` probes, 3 s apart, and at a restart of the
  `valkey` container the any-reply exec probe makes that container Ready 5 s in, so a window of a
  few seconds may remain before the sidecar gate closes (Not verified); and D is designed, not
  measured end to end: its inputs are measured, its verdicts are not.

**Why D.** It removes the defect this ticket measures (a pod that cannot serve is routed to and
counted as healthy) at the one place in the pod that already reads the server's state every second,
and it touches no pod spec, so it cannot become the ADR 0007 D7 data-loss change: on kustomize or a
floating tag an operator upgrade that ships it deletes no single pod, where B and C would, unless
they carry a D7 treatment that re-decides ADR 0007 D6. The auth behaviour T51 depends on is left
exactly as it is, and liveness keeps the property that protects long loads. **Over C, the
runner-up:** C is measured end to end, keeps the verdict in the kubelet and the server's own
container, and has no restart window; those are real, and they do not outweigh an extra data-loss
path on an operator upgrade plus a re-decision of the single-pod rule, both of which D avoids, for
a residual window of a few seconds at a container restart. If a D7 treatment lands for another
reason first, the comparison flips: C then costs S, is fully measured, and becomes the better
option. **Over B:** B has C's rollout cost and couples readiness to the password, which turns a
runtime password change into an empty Service and obliges T51's design to avoid it. **Over A:** A
costs nothing but leaves the load window routed to and counted, which on a persistent tier recurs
at every container restart and on every tier at every full sync; A's documentation is the interim
until D lands.

## Decision

Not decided.

## Work list

1. **XS, decision-free, carries the rule-1 urgency:** rewrite the comment at
   [`sentinel.go:429`](../../internal/builder/sentinel.go) so it no longer says the probe "must
   authenticate": the probe passes the password so a healthy Sentinel answers `PONG`, and its verdict
   does not depend on it, because `valkey-cli` exits 0 on an auth error (this ticket, measured).
   Comment only, no test.
2. **XS, decision-free, owned by [ticket 051](051-a-changed-cluster-password-reaches-no-running-pod.md)
   Work list item 3:** the reason given at
   [`authentication.md:51-52`](../operations/authentication.md): the replacement becomes Ready
   because its exec probe passes on any reply and its sidecar authenticates once against its own
   server with the same Secret value (Impact, case 3), not because the readiness probe
   authenticates. 051's item 3 words the correction as "the probe passes on any reply" only, which
   leaves out the sidecar half. Listed here so the correction is not lost if 051's runbook rewrite
   lands without it; not to be done twice.
3. **XS, decision-free:** record the measured probe semantics in
   [ADR 0007](../adr/0007-failover-aware-rolling-update.md) D9 (both exec probes pass on any reply
   and fail only on no connection or no answer within `timeoutSeconds`; liveness keeps this on
   purpose, Impact case 5; the sticky sidecar gate opens at the first answered `INFO replication`,
   which a loading server gives), dated, with the measurement's commands and results written into
   the ADR itself, because an ADR does not cite a ticket.
4. **Waiting on the decision (D):** in the labeler poll
   ([`labeler.go:120-160`](../../internal/sidecar/labeler.go)), read whether the server is loading
   and classify a `-BUSY` or `-LOADING` error, and close the health server's gate on either and open
   it on the next normal answer, leaving it unchanged on an auth error or a lost connection
   ([`health.go`](../../internal/sidecar/health.go)); the unit tests and the e2e of Verification;
   the ADR 0007 D9 amendment stating the new rule and marking the sticky sentence in place; the
   sidecar row of [`architecture.md:86`](../developer/architecture.md), which says it "answers
   `/readyz`"; the operations page that describes readiness
   ([`authentication.md`](../operations/authentication.md), and any other page the implementation
   finds with `git grep -n -i "probe\|readyz" -- docs
   ':!docs/tickets'`). `ProbeCommand` and the pod spec stay as they are.
5. **Cross-ticket, for their own files** *(status 2026-09-27, sweep: the 036 part is done in
   036, which now points here in Fact, Decision 4 and Not verified and precises its liveness
   sentence; the 051 parts are done in 051 - D2's premise and the sidecar half of item 3 - except
   the `sentinel.go:420-446` range, which 051 keeps because M5 is about the auth branches that
   range covers; the 012 and 023 parts ask for no edit)*: [ticket 036](036-non-persistent-master-restarts-empty.md)
   marks "a persistent master answering `-LOADING` is Ready" as inferred and not measured; it is
   measured here (after a restart and during a full-sync load, both pins); and its liveness sentence
   "a master that does not answer `PING` for about 50 s is killed" (036, Fact, the liveness bullet
   of "Three operator-side facts"; `036:81-82` at `84a39c2`) holds
   only for no answer at all: a master answering an error reply (`-LOADING`, `-BUSY`, `NOAUTH`)
   passes the probe (measured here).
   [Ticket 051](051-a-changed-cluster-password-reaches-no-running-pod.md) D2's premise holds under
   A, C and D and not under B; its Work list item 3 lacks the sidecar half of the `authentication.md`
   correction (item 2 above); and its M5 and Work list item 6 cite the Sentinel probe as
   `sentinel.go:420-446`, which is the function head and its auth branches; the function is
   `:420-459`, its comment starts at `:412`.
   [Ticket 012](012-no-write-fencing-min-replicas-to-write-as-an-opt-in-field.md) measured the same
   `valkey-cli` behaviour for `--raw SET` (error on stdout, exit 0; 012, section "E2E impact";
   `012:131` at `84a39c2`); `-e`
   (Fact) is the upstream switch for it.
   [Ticket 023](023-pauserollingupdate-records-no-pause.md) states the readiness probe is a
   `PING` an unsynced replica passes; that stays true under every option.

## Verification

- Item 1: `git grep -n "must authenticate" -- internal/builder/sentinel.go` returns nothing.
- Item 3: ADR 0007 D9 states the measured semantics of both gates, dated.
- For D, unit (`make test-unit`): a labeler driven by a scripted detector and a health server, one
  row each: before any answer `/readyz` is 503; a normal answer opens it; `loading:1` (or
  `-LOADING`) closes it; `-BUSY` closes it; the next normal answer opens it again; after it has
  opened, an auth error (`WRONGPASS`/`NOAUTH`) and a refused connection leave it open. **Revert
  check:** restore today's latch-only poll, and the `-LOADING` and `-BUSY` rows fail.
  **Mutations:** treat an auth error as not ready, and the auth row fails; treat a lost connection
  as not ready, and the connection row fails; drop the loading check, and the `-LOADING` row fails.
- For D, no pod-spec change: `ComputePodSpecHash` output for a fixed `Valkey` is the same before
  and after the change (unit), so no roll comes from it and no single pod is replaced for it.
- For D, e2e (`make test-e2e`, both lines): on a multi-replica cluster a replica running a script
  past `busy-reply-threshold` (the `EVAL` loop of Fact) leaves the ready endpoints of `-r`
  (`readyEndpointPodNames`) within 15 s and is back after `SCRIPT KILL`; the pod is never restarted
  (liveness unchanged, restart count equal). The load case stays at the unit tier: a load long
  enough to observe on Kind needs a large dataset or a debug-only config.
- For D, both full suites green on the release image.

## History

- 2026-09-27: filed from ticket 051 (measurement M5, the cross-ticket paragraph "The probe
  exit-code finding (M5) is a different mechanism", Work list item 6, and the D2 sentence "The
  probes do not matter, because `valkey-cli` exits 0 on `NOAUTH`") and from that ticket's run
  records of the same day (verify, facts and design: M5 re-run by both reviewers, the
  `authentication.md:51-52` finding, the siblings 012, 023, 036), during the re-verification at
  `84a39c2`. **Moved here:** the M5 exit codes (wrong password 0, right password 0, error reply 0,
  nothing listening 1, also via `sh -c`), the probe locations, the `authentication.md:51-52` finding
  and the sibling list. **Re-verified now, by reading at `84a39c2`:** both probe builders and their
  wiring, the Sentinel config path of `requirepass`, the Service and `available()` consumers, the
  pod-spec hash, the tests, ADR 0007 D7 and D9, and the `valkey-cli` exit-code logic in upstream
  source at `9.1.1` and `8.1.9`. **Measured now** (docker, both pins, commands and results in Fact):
  every generated exec probe form, data and Sentinel, with and without auth and TLS, against a
  healthy server, a wrong and an empty password, no password, a closed port, `-BUSY`, `SIGSTOP`,
  `-LOADING` after a restart and during a full-sync load, and a TLS CA mismatch variant; where error
  replies are written; and the candidate forms `-e`, `grep -q PONG` and option C's. **Corrected:**
  (1) M5 was recorded as `valkey-cli --no-auth-warning -a wrong ping`, and the generated auth probes
  carry no `--no-auth-warning`; with the exact generated strings the exit codes are the same and the
  only difference is the warning on stderr. (2) 051's Work list item 6 cites the data probe as
  `statefulset.go:1515-1531`, which is the auth branch only; the function is `:1515-1545`, and the
  no-auth forms exit the same way. (3) 036's "a persistent master answering `-LOADING` is Ready
  (inferred for `-LOADING`, not measured)" is now measured. (4) 051's run records cite 012's `--raw
  SET` measurement at `012:129-131`; it is at `:131` at `84a39c2` (`:229` in the working tree of
  2026-09-27). **New in this filing:** the upstream mechanism (`-e`, `set_errcode`); the `-BUSY`,
  `-LOADING` and hang cases; the Sentinel mismatch case by reading; the finding that liveness is
  right as it is and must stay; the rule-1 statement at `sentinel.go:429`; three options with C
  marked. **Security class derived: `none`** (Impact), so the file is tracked and not embargoed. No
  cluster, no make target, no go test. **Adversarial review, the same day, at `84a39c2`:** found
  that the filing had left out the second readiness gate of every data pod, the sidecar's
  `/readyz`, which ADR 0007 D9 names; read it (latched at the first answered `INFO replication`,
  never reset) and measured its inputs on both lines (`INFO replication` answers during a load,
  `-BUSY` during a script; commands and results in Fact). Traced by reading, and moved from Not
  verified to Verified, how an operator upgrade treats a probe-only pod-spec delta: no extra roll on
  the Helm path, and on kustomize or a floating tag the single non-persistent pod is replaced with
  its dataset (ADR 0007 D7), which B and C would have to treat. **Options re-weighed:** D added (the
  sidecar gate closes on `-LOADING` and `-BUSY`, no pod-spec change) and marked in place of C, which
  is the runner-up and wins if a D7 treatment lands first; B's and C's effort raised to M; the
  frontmatter comments, Work list items 2-5 and Verification follow D. **Corrected:** the headless
  Services are the data one (`service.go:154`) and the Sentinel one (`:237`), not "two" of the data
  tier, and the `-all` Service reads readiness as well; the `authentication.md:51-52` correction
  names both gates (its outcome needs the sidecar to authenticate once). **Carried from the run
  records:** T51's M5 command lines, the 036 liveness sentence that holds only for no answer, and
  051's Sentinel probe citation. Urgency, severity and security class re-derived, unchanged.
  Sweep: Work list item 5 now records its status: the 036 part was carried out in 036 by the sweep,
  the 051 parts by 051's host update (the `sentinel.go:420-446` range stays in 051 because M5 is
  about the auth branches), and the 012 and 023 parts ask for no edit; its citations of 036 and 012
  by line now also name the section. Frontmatter unchanged.
