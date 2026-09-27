---
id: T50
title: every component authenticates with the one cluster password and full rights
state: analysed       # was filed; every fact the decision needs is verified (docker on both pinned lines, 2026-09-27) and the options are complete
severity: medium      # a compromised exporter (third-party image) can flush or re-point the dataset of every auth-enabled cluster with metrics on, and reads the cluster password back through CONFIG GET masterauth
security: hardening
threat: "would additionally cover a compromised exporter (third-party code chosen by spec.metrics.image, default redis_exporter v1.66.0, in every data pod of an auth-enabled cluster with metrics on): today it authenticates as the default user, may run FLUSHALL, CONFIG SET or REPLICAOF, and reads the cluster password back with CONFIG GET masterauth"  # was "code execution in the exporter, the sidecar, the observer or a probe ..."; narrowed 2026-09-27, a probe is valkey-cli inside the valkey container and no sidecar or observer user is pursued
urgency: icebox       # rule 5: the exporter user reopens ADR 0016 D2 (and, under E, needs a clarification of D1's rationale wording); rule 1 does not match, this ticket's own false sentences are corrected in place and the measured-false ADR 0016 /proc statement is T70's; rule 2 does not match, not release-gated; rule 3 does not match, the trigger (a compromised exporter) is dormant; rule 4 does not match, nothing is decided and the fix is M
effort: M             # was L (L was for B or C, both removed 2026-09-27); D, E and F are each M
blocked-by: decision  # ADR 0016 D2 (and D1's rationale wording under E), see Options
filed-from: T31, section "Further security measures — not in this change, each open" (archive/031)
opened: 2026-09-27
decided:              # not recorded - no decision yet
done:                 # not recorded - not done
---

Filed on 2026-09-27 from the third row of the table
["Further security measures — not in this change, each open"](archive/031-generated-pods-run-as-root.md#further-security-measures--not-in-this-change-each-open)
in the archived ticket 031, which is `done` and tracks none of its rows. The operator-facing
statement of the gap is [H-6](../security/secrets-and-tls.md#h-6).

## Fact

**Verified** (read 2026-09-27; re-read at `84a39c2` on the same day, the gap is open as
described — no builder renders an ACL user):

- Every consumer receives the same password from the same `secretKeyRef`: `VALKEY_PASSWORD`
  for the `valkey` container, the init container, the `sidecar` and the observer,
  `REDIS_PASSWORD` for the exporter ([`statefulset.go`](../../internal/builder/statefulset.go)
  line 1078, [`observer.go`](../../internal/builder/observer.go) line 249;
  [ADR 0016](../adr/0016-authentication-and-tls-posture.md) D2). *(located 2026-09-27 at
  `4a7543e`: in `statefulset.go` at lines 381 and 566 (the two `init-config-selector`
  variants), 879 (`valkey`), 951 (`sidecar`) and 1078–1084 (exporter); in
  [`sentinel.go`](../../internal/builder/sentinel.go) at lines 356 and 563; in `observer.go` at
  lines 249–258.)* *(re-read 2026-09-27 at `84a39c2`: every site holds. The two `sentinel.go`
  sites are the `init-sentinel-config` container (356) and the `sentinel` container (563); the
  latter receives the password only while `spec.sentinel.disableAuth` is false,
  [`sentinel.go:559`](../../internal/builder/sentinel.go).)*
- The exec probes authenticate the same way: `ProbeCommand` runs
  `valkey-cli ... -a "$VALKEY_PASSWORD" ping` (`statefulset.go` ~~lines 1512–1526~~
  *(corrected 2026-09-27: lines 1515–1545, the auth branch 1516–1531)*). *(added 2026-09-27)*
  It is the readiness and liveness probe of the `valkey` container (lines 847 and 859), so it
  runs inside the container that already holds the password in its environment (line 879).
- Neither config builder renders an ACL directive: a grep for `aclfile`, `user `,
  `masteruser` and `auth-user` over [`configmap.go`](../../internal/builder/configmap.go) and
  [`sentinel.go`](../../internal/builder/sentinel.go) finds nothing, so every client is the
  default user with every command. ~~*(re-run 2026-09-27, case-insensitive, with `acl ` and
  `--user` added and `statefulset.go` included: no match.)*~~ *(corrected 2026-09-27 at
  `84a39c2`: that re-run has one match, the comment "sentinel-specific user labels" at
  [`sentinel.go:215`](../../internal/builder/sentinel.go), which is not a directive; the
  conclusion holds. The default user is `user default on sanitize-payload #… ~* &* +@all`,
  measured with `ACL LIST` on 9.1.1 and 8.1.9, M1 below.)*
- [ADR 0016](../adr/0016-authentication-and-tls-posture.md) D1: the operator never generates a
  credential — "no operator-owned password to rotate, to leak into status, or to orphan on CR
  deletion". *(precised 2026-09-27 at `84a39c2`: D1's rule is "the auth Secret is always
  user-owned; the operator never generates one" — a **Secret** — and "never generating a
  credential" is the rationale sentence that follows it,
  [ADR 0016:37–40](../adr/0016-authentication-and-tls-posture.md). This decides how much of D1
  option E touches, see Options.)*

*Added 2026-09-27 (enrichment):*

- `valkey-server` takes the password on its command line:
  `exec valkey-server <config> --requirepass "$VALKEY_PASSWORD" --masterauth "$VALKEY_PASSWORD"`
  (`statefulset.go` line 830). Sentinel receives `requirepass` and `sentinel auth-pass` as
  placeholders that its init container replaces with `sed` (`sentinel.go` lines 180–187 and
  715). No credential reaches a ConfigMap (ADR 0016 D3/D4).
- The operator is a default-user client too: `readValkeyPassword`
  ([`valkey_controller.go`](../../internal/controller/valkey_controller.go) line 177) and the
  health checker's `readAuthPassword` ([`checker.go`](../../internal/health/checker.go) line 75)
  read the same key.
- **The image each consumer runs.** The `valkey` container, its init containers and its probes
  run `spec.image`, which is required and chosen by the CR author
  ([`valkey_types.go`](../../api/v1/valkey_types.go) lines 1032–1034). The `sidecar` and the
  observer run the operator image (`statefulset.go` lines 989–996, `observer.go` line 82). The
  exporter runs `spec.metrics.image`, default `DefaultMetricsExporterImage`
  (`valkey_types.go` line 645). ~~**The exporter is the only third-party code among them.**~~
  *(corrected 2026-09-27 at `84a39c2`: `spec.image` is the upstream `valkey/valkey` image and
  third-party as well. **The exporter is the only third-party client process among them**;
  `valkey-server` is the server that holds the dataset, so a user of its own would narrow
  nothing.)*

*Added 2026-09-27 at `84a39c2` (re-verification; the docker spike this ticket listed as open
was run on `valkey/valkey:9.1.1` and `valkey/valkey:8.1.9`, the two pins of
[`test/testimages/images.go`](../../test/testimages/images.go), with the pinned exporter
`oliver006/redis_exporter:v1.66.0`, RepoDigest `sha256:d98e6db8…` identical to the pin at
[`valkey_types.go:645`](../../api/v1/valkey_types.go); the local image is arm64. Scripts were
kept outside the repository; every `vko-verify-050*` container and network was removed.)*

- **The command set each component issues** (was "Not verified"):
  - Exporter, redis_exporter v1.66.0 with default flags, read in its source at the tag
    (<https://raw.githubusercontent.com/oliver006/redis_exporter/v1.66.0/exporter/exporter.go>
    and siblings): `CLIENT SETNAME` (`exporter.go:670`, on by default `main.go:88`),
    `CONFIG GET *` unless `REDIS_EXPORTER_CONFIG_COMMAND` is `-` (`main.go:77`; a failure is
    non-fatal), `INFO ALL` with a fallback to `INFO` (`exporter.go:690–693`; a failure aborts
    the scrape), `LATENCY LATEST` and `LATENCY HISTOGRAM` (`latency.go:27,57`), `SLOWLOG LEN`
    and `SLOWLOG GET 1` (`slowlog.go:9,13`). Without `CONFIG GET` the keyspace series still
    work, because the exporter falls back to 16 databases (`exporter.go:712–714`), which is what
    [`configmap.go:121`](../../internal/builder/configmap.go) renders.
  - `sidecar`: `INFO replication`, `REPLICAOF NO ONE` on the chosen replica and
    `REPLICAOF <new master>` on every remaining peer, `SENTINEL FAILOVER`, `SENTINEL MASTER`
    ([`drain.go:139,172,278,365`](../../internal/sidecar/drain.go),
    [`labeler.go:192,354`](../../internal/sidecar/labeler.go)).
  - observer: `PING`, `INFO replication`, a `SET`/`GET` round trip through
    `ExecGet`, `SENTINEL MASTER` and the Sentinel quorum reads
    ([`checks.go:44–296`](../../internal/observer/checks.go)).
  - probes: `PING` (`statefulset.go:1515–1545`).
- **M1 — the default user, a command-line user, and `CONFIG GET` returning the password**
  (9.1.1 and 8.1.9):
  `docker run -d --rm --name vko-verify-050-u-<v> valkey/valkey:<v> sh -c 'exec valkey-server --requirepass s3cret --masterauth s3cret --user exporter on ">exp" "~*" "+info" "+ping" "+config|get" "+client|list"'`,
  then `ACL LIST`, and as `exporter`: `CONFIG GET masterauth`, `CONFIG GET requirepass`,
  `FLUSHALL`, `REPLICAOF 1.2.3.4 6379`, `CONFIG SET maxmemory 1`. Result on both lines:
  `user default on sanitize-payload #1ec1… ~* &* +@all` and the `exporter` user as declared;
  `masterauth` and `requirepass` both return `s3cret`; the three writes return
  `NOPERM User exporter has no permissions to run the '<cmd>' command`. **Any user granted
  `+config|get` reads the cluster password**, and every data pod carries `--masterauth`
  (`statefulset.go:830`). Re-run by a second reviewer with the same result. A per-parameter
  grant does not exist: `ACL SETUSER t2 +config|get|maxmemory` fails on both lines with
  "Allowing first-arg of a subcommand is not supported".
- **M2 — `ACL DRYRUN` of the minimal exporter set** (9.1.1 and 8.1.9, auditor only):
  `ACL SETUSER exp2 on '>x' resetkeys resetchannels -@all +info +client|setname +latency|latest +latency|histogram +slowlog|len +slowlog|get`,
  then `ACL DRYRUN exp2 <cmd>`: OK for `INFO ALL`, `CLIENT SETNAME`, `LATENCY LATEST`,
  `LATENCY HISTOGRAM`, `SLOWLOG LEN`, `SLOWLOG GET 1`; refused for `config|get`, `flushall`,
  `replicaof`, `client|kill`, `client|pause`, `slowlog|reset`, `latency|reset`, `debug`.
- **M5 — the pinned exporter under that user** (9.1.1 and 8.1.9): exporter with
  `REDIS_USER=exp3 REDIS_PASSWORD=x REDIS_EXPORTER_CONFIG_COMMAND=-`, `ACL LOG RESET`, then
  `curl :9121/metrics`, then `ACL LOG`. Result: `redis_up 1`, `redis_slowlog_length 0`, latency
  percentile series present (80 lines on 9.1.1, 75 on 8.1.9), `ACL LOG` empty, no `NOPERM` in
  the exporter log. Without `REDIS_EXPORTER_CONFIG_COMMAND=-`: `redis_up 1`, but `ACL LOG`
  records `reason command, object config|get, username exp3` per scrape. Re-run by a second
  reviewer on 9.1.1 with the same result.
- **M6 — what skipping `CONFIG GET` costs** (9.1.1, auditor only): the diff of series names
  with and without `+config|get` is 12 series, `redis_config_maxmemory`,
  `redis_config_maxclients`, `redis_config_io_threads` and 9
  `redis_config_client_output_buffer_limit_*` series. `redis_memory_max_bytes` and
  `redis_io_threads_active` remain (they come from `INFO`). No shipped alert or doc uses a
  `redis_*` series (grep over `deploy/`, `docs/operations/` and `config/`: none).
- **M10 — `SLOWLOG GET` hands command arguments to a user without key access** (9.1.1 and
  8.1.9, measured by two reviewers): `valkey-server --slowlog-log-slower-than 0`, the M2 user,
  the default user runs `SET customer:42:token tok-abc-123`; as the restricted user,
  `GET customer:42:token` returns `NOPERM`, and `SLOWLOG GET 5` returns the entry
  `SET customer:42:token tok-abc-123`, while `AUTH` and `ACL SETUSER` arguments show as
  `(redacted)`. With the default threshold (`slowlog-log-slower-than` 10 ms) only commands
  slower than that are exposed, but `resetkeys` does not keep the exporter away from data while
  it holds `+slowlog|get`. The exporter uses the reply only for the id and the duration of the
  newest entry (`slowlog.go:13–31`), ignores a failed call, and v1.66.0 has no flag that turns
  the slowlog collector off.
- **M11 — the exporter without `+slowlog|get`** (measured 2026-09-27 for this ticket, 9.1.1
  and 8.1.9): user
  `resetkeys resetchannels -@all +info +client|setname +latency|latest +latency|histogram +slowlog|len`,
  exporter with `REDIS_EXPORTER_CONFIG_COMMAND=-`, three `curl .../metrics` scrapes. Result on
  both lines: `redis_up 1`, `redis_slowlog_length 0`, latency percentile series present (45 on
  9.1.1, 40 on 8.1.9 in that scrape), no `NOPERM` in the exporter log, and `ACL LOG` holds
  **one** entry, `reason command, object slowlog|get, username expa`, `count 3`. The control
  with `+slowlog|get` (9.1.1) additionally reports `redis_slowlog_last_id` and
  `redis_last_slow_execution_duration_seconds` and leaves `ACL LOG` empty. So dropping
  `SLOWLOG GET` costs exactly those 2 series and one grouped `ACL LOG` entry.
- **M9 — the exporter's password-file mode** (9.1.1, auditor only): a JSON file
  `{"redis://<valkey>:6379":"x"}`, exporter with `REDIS_ADDR` set to exactly that URI,
  `REDIS_USER=exp3`, `REDIS_PASSWORD_FILE=<file>`, `REDIS_EXPORTER_CONFIG_COMMAND=-` and no
  `REDIS_PASSWORD`: `/metrics` reports `redis_up 1`. By reading at v1.66.0: the file is read
  once at start and only when `REDIS_PASSWORD` is empty (`main.go:63,137–138`), and the
  password is looked up by the exact URI (`redis.go:35–36`, `PasswordMap[uri]`). A key that
  differs from `REDIS_ADDR` sends no `AUTH`, and the scrape fails with `redis_up 0`.
- **The exporter takes a user name from its environment** (was "Not verified"):
  `REDIS_USER`, `main.go:61` at v1.66.0, added to the dial at `redis.go:28`.
- **M7 — `--user` on the `valkey-server` command line** (was "Not verified"; 9.1.1 and 8.1.9,
  auditor only): `EXP_PW='p w"x'"'"'y$z' valkey-server --requirepass s3cret --user exporter on ">$EXP_PW" resetkeys -@all +info`,
  then `valkey-cli --user exporter --pass "$EXP_PW" INFO server`: the directive is accepted and
  the special-character password authenticates on both lines.
- **M8 — a command-line user crashes the `valkey` container after a `CONFIG REWRITE` and a
  restart** (9.1.1 and 8.1.9, uid 999, measured by two reviewers):
  `docker run -d --name vko-verify-050-rw-<v> --user 999:999 …`, seeding a writable
  `/data/v.conf` with `port 6379` and `dir /data` once, then
  `exec valkey-server /data/v.conf --requirepass "$VALKEY_PASSWORD" --masterauth "$VALKEY_PASSWORD" --user exporter on ">$EXP_PW" resetkeys resetchannels "-@all" "+info"`;
  `CONFIG REWRITE`; `docker restart`. The file gains `requirepass "s3cret"`,
  `user default on … #1ec1… ~* &* +@all` and `user exporter on … #85ed… -@all +info` (~~no
  `masterauth` line; not investigated further, it decides nothing here~~ *(corrected 2026-09-27:
  Valkey rewrites `masterauth` under the name `primaryauth`, so the file also gains
  `primaryauth "s3cret"`; this run grepped for `masterauth` only. Measured on both pins in
  [T70](070-tracked-comments-and-adrs-state-what-the-code-contradicts.md), its M2. It decides
  nothing here.)*). The restart fails with
  `*** FATAL CONFIG FILE ERROR (Version 9.1.1|8.1.9) *** … Error in user declaration 'exporter': Duplicate user found. A user can only be defined once in config files`,
  and the container exits 1. The control without `--user` restarts and answers `PONG`.
- **M8b — the same user written into the writable config instead survives** (9.1.1 only,
  facts reviewer): `/data/v.conf` seeded with
  `user exporter on >exppw resetkeys resetchannels -@all +info`, `valkey-server` started with
  only `--requirepass` and `--masterauth`; `CONFIG REWRITE` returns OK, the file holds exactly
  one `user exporter` line, `docker restart` comes up, and `INFO server` as `exporter` succeeds.
  Not measured on 8.1.9.
- **Where a `CONFIG REWRITE` comes from.** Among the product's components, only Sentinel: `sentinelSendReplicaOf`
  sends `MULTI`, `SLAVEOF`, `CONFIG REWRITE`, `CLIENT KILL`, `EXEC`
  (<https://raw.githubusercontent.com/valkey-io/valkey/9.1.1/src/sentinel.c>, function at 4868,
  the rewrite at 4912–4913; 8.1.9 at 4674–4719), and it is called outside a failover too, when
  Sentinel reconfigures a misconfigured replica (9.1.1 callers at 2639, 2652, 2667, besides the
  failover callers 5189, 5257, 5307). Neither the operator nor the sidecar sends one: a grep for
  `rewrite` over the non-test Go code of `internal/` and `cmd/` finds only comments and the AOF
  rewrite directives of `configmap.go`. The file that survives a `valkey` container restart is
  the writable `/etc/valkey-active` emptyDir, used on Sentinel and on multi-replica clusters
  without Sentinel ([`statefulset.go:645–661`](../../internal/builder/statefulset.go),
  `needsInitContainer`, `configMountForContainer`); a standalone pod reads the read-only
  ConfigMap mount. On a multi-replica cluster without Sentinel only a default-user client
  running `CONFIG REWRITE` by hand rewrites the file, which the default user's `+@all` allows.
- **M3/M4 — `masteruser` and Sentinel `auth-user`** (was "Not verified"; 9.1.1 and 8.1.9,
  auditor only): a replica with `--masteruser repl --masterauth rp` against a master whose
  `repl` user holds only `-@all +psync +replconf +ping` reaches `master_link_status:up` and
  replicates a key; a Sentinel with `sentinel auth-user mm sent` and the documented Sentinel ACL
  (`allchannels +multi +slaveof +ping +exec +subscribe +config|rewrite +role +publish +info +client|setname +client|kill +script|kill`)
  reports `flags=master`, `num-slaves=1`, and the master's `ACL LOG` stays empty. Both work on
  both lines; they mattered only for option B, removed below.
- **The operator's own client cannot log in as a named user**:
  [`client.go:410–424`](../../internal/valkeyclient/client.go) sends `AUTH <password>` with no
  username. Any sidecar or observer user would need client work first.
- **Upstream's recommended exporter ACL does not narrow anything on these pods**: the
  v1.66.0 README (line 208,
  <https://raw.githubusercontent.com/oliver006/redis_exporter/v1.66.0/README.md>) grants
  `+config|get` (M1: the password), `+client` (`CLIENT KILL`, `CLIENT PAUSE`), `+eval`, `+get`
  and `+scan`.
- **The exporter's command set moves** with the pin, with `spec.metrics.extraArgs`
  ([`valkey_types.go:675–677`](../../api/v1/valkey_types.go), appended at
  [`statefulset.go:1127–1129`](../../internal/builder/statefulset.go)) and with
  `spec.metrics.image`. `--check-keys` and `--check-single-keys` need `SELECT`, `SCAN`, `TYPE`,
  `GET`, `STRLEN` and more (`keys.go` at v1.66.0), `--export-client-list` needs `CLIENT LIST`.
  The CRD example in `CLAUDE.md` (line 141) itself suggests `["--check-keys=*"]`. The latest
  release, v1.92.0 (2026-09-23, GitHub API `releases/latest`), adds `COMMAND INFO COMMANDLOG`,
  `COMMANDLOG LEN` and `CONFIG GET commandlog-*` to the default scrape (`commandlog.go:23,42,53`
  at v1.92.0).
- **By reading, not measured**: `extraArgs` can already pass
  `--include-config-metrics --redact-config-metrics=false`, which publishes every config value,
  `masterauth` and `requirepass` included, as metric labels (`exporter.go:587–597` at v1.66.0).
  A user without `config|get` removes that path.
- **No e2e reads the exporter's output**: metrics are only switched on at
  [`pod_security_test.go:134`](../../test/e2e/pod_security_test.go) and
  [`pod_hardening_test.go:216`](../../test/e2e/pod_hardening_test.go); `grep -rn 'redis_up\|/metrics' test/e2e`
  finds nothing. Any exporter-side check needs a new helper.
- **`cat` is not a declared image tool**:
  [`RequiredImageTools`](../../internal/builder/image_requirements.go) (lines 24–78) lists `sh`,
  the Valkey binaries, `timeout`, `sleep`, `grep`, `cut`, `tr`, `rev`, `awk`, `head`, `cp`,
  `echo`, `seq`, `sha1sum`, `sed`, `find` and `chown`. `head`, `sha1sum` and `cut` are there;
  `cat` is not.
- The exporter's `REDIS_ADDR` is `redis://localhost:6379`, or `rediss://localhost:16379` under
  TLS ([`statefulset.go:1063–1073`](../../internal/builder/statefulset.go)).

**Not verified:**

- ~~The command set each component needs: the exporter's; the sidecar's, which include
  `REPLICAOF` for the drain promotion
  ([ADR 0012](../adr/0012-the-sidecar-records-its-drain-promotion-on-the-pod.md)); the
  observer's; the probes' `PING`.~~ *(corrected 2026-09-27 at `84a39c2`: verified, see "The
  command set each component issues" and M5 above.)*
- ~~That `masteruser` for replication and Sentinel's `auth-user` behave the same on both pinned
  Valkey lines.~~ *(corrected 2026-09-27 at `84a39c2`: verified, M3/M4 above; both work on both
  lines.)*
- How a component password would reach running pods when it changes; the cluster password has
  no such path ([ticket 051](051-a-changed-cluster-password-reaches-no-running-pod.md)).
  *(re-read 2026-09-27: holds for a user-provided Secret, option D; an env `secretKeyRef` is
  resolved once at container start and ADR 0016 D12 says values do not propagate. It does not
  apply to option E, whose credential is generated per pod and never changes during the pod's
  life.)*
- ~~*(added 2026-09-27)* That `valkey-server` accepts a `user` directive on its command line
  (`--user <name> on >… <rules>`) on both pinned lines. Also whether redis_exporter v1.66.0
  takes a user name from its environment.~~ *(corrected 2026-09-27 at `84a39c2`: both
  verified, M7 and `REDIS_USER` above; M8 adds the failure mode that makes the command-line
  form alone unsafe on a writable config.)*
- *(added 2026-09-27 at `84a39c2`)* Option E's init step on a real rootless pod
  (`readOnlyRootFilesystem`, `fsGroup` 999, a Memory emptyDir, a user namespace), and E or D
  under TLS. ACL is transport-independent by reading; nothing was run on Kind. What would
  settle it: the e2e of the Work list.
- *(added 2026-09-27 at `84a39c2`)* M8b (user line in the writable config) on 8.1.9, and the
  rewrite guard on a real Sentinel pod after a failover.
- *(added 2026-09-27 at `84a39c2`)* That the single `ACL LOG` entry of M11 stays one entry over
  hours: measured only over three scrapes seconds apart. By reading at 9.1.1
  (<https://raw.githubusercontent.com/valkey-io/valkey/9.1.1/src/acl.c>, `ACLLogMatchEntry` at
  2817–2826, the scan at 2928–2950), Valkey groups a denial with one of the ten newest entries
  when reason, context, object and user match and that entry was updated at most 60 s before,
  and moves it to the head; with a scrape interval below 60 s it stays one entry.
- *(added 2026-09-27 at `84a39c2`)* M2, M3, M4, M6, M7, M8b, M9 and M11 were measured by one
  reviewer only; M1, M5, M8 and M10 by two.
- *(added 2026-09-27 at `84a39c2`)* Exporter images other than the pin: the options assume a
  redis_exporter-compatible `spec.metrics.image`, as today's `REDIS_ADDR` and `REDIS_PASSWORD`
  wiring already does.

## Impact

~~Dormant until a component is compromised; then total.~~ *(corrected 2026-09-27 at `84a39c2`:
the threat is narrowed to the exporter — a probe is `valkey-cli` inside the `valkey` container,
not a component of its own, and no sidecar or observer user is pursued, per case below.)*
**Dormant until the exporter is compromised; then total.** The exporter is third-party code
that runs in every data pod of a metrics-enabled cluster and, with auth on, holds the cluster
password as the default user (`statefulset.go:1044–1052` adds it, `1076–1088` hands it the
password; M1: `+@all`). Whoever controls it may flush the dataset, rewrite the config at runtime
or re-point replication, and reads the password back through `CONFIG GET masterauth`. What an
operator can do today: keep the exporter image pinned by digest (the default is,
[ADR 0033](../adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md)
D5) and leave metrics off where they are not needed.

*(added 2026-09-27, per component)* Least-privilege users buy something only where the
component does not already hold more:

- **The probes and the init containers run beside the `valkey` container, which holds the
  default password.** A probe user would narrow nothing.
- ~~**The `sidecar` and the observer run the operator image.** A compromise of that image is a
  compromise of the operator, which is cluster-admin equivalent
  ([ADR 0013](../adr/0013-operator-is-cluster-wide-privileged.md) D1). The sidecar also keeps
  `REPLICAOF` in any command set.~~ *(corrected 2026-09-27 at `84a39c2`: the image argument
  holds only for a supply-chain compromise of the operator image
  ([ADR 0013](../adr/0013-operator-is-cluster-wide-privileged.md) D1). A runtime compromise of
  either process is not an operator compromise: the observer pod mounts no ServiceAccount token
  ([`observer.go:124`](../../internal/builder/observer.go)), and the sidecar holds only its own
  ([`statefulset.go:602–610`](../../internal/builder/statefulset.go)). The conclusion — a user
  of their own narrows little — holds for other reasons: the sidecar must keep `REPLICAOF`
  against every peer and `SENTINEL FAILOVER` in any command set
  ([`drain.go:139,172,365`](../../internal/sidecar/drain.go)), the observer's runtime surface
  is three read-only HTTP handlers that take no parameters
  ([`server.go:15–31`](../../internal/observer/server.go)), and both would need username
  support in `valkeyclient` first.)*
- **The exporter is the one case where a user of its own removes rights nobody else in that
  process holds.** *(verified 2026-09-27 at `84a39c2`: the exporter container gets only
  `REDIS_ADDR`, the listen address, `REDIS_PASSWORD` and the TLS paths
  (`statefulset.go:1070–1103`), no token and no config volume; under the M2 set it cannot
  `FLUSHALL`, `REPLICAOF`, `CONFIG SET` or `CONFIG GET`.)*

## Options

Two decisions, in order; the second exists only if the first is not A.

### Decision 1 — does the exporter get a Valkey user of its own, and where does its credential come from?

**Mechanism today.** `buildExporterContainer`
([`statefulset.go:1060–1131`](../../internal/builder/statefulset.go)) gives the exporter
`REDIS_PASSWORD` from the `spec.auth` `secretKeyRef` (1076–1088). `valkey-server` starts with
`--requirepass` and `--masterauth` from the same value (826–832) and knows no other user, so the
exporter is the default user, `~* &* +@all` (M1).

Three measured facts shape every option: a least-privilege exporter user works on both lines
(M5, M11); it must not hold `config|get`, because that returns the password (M1), so the
exporter runs with `REDIS_EXPORTER_CONFIG_COMMAND=-` and loses 12 `redis_config_*` series (M6);
and a user declared only on the command line crash-loops the `valkey` container on its next
restart in the same pod after any `CONFIG REWRITE` (M8), which Sentinel issues after every
`REPLICAOF` it sends. Every doing-option therefore carries a **rewrite guard** on the
writable-config topologies: either the wrapper deletes the exporter's own `user <name>` line
from `/etc/valkey-active` before `exec` (never the rewritten `user default` line; `sed` is
already a declared tool), or the init step writes the user line into the writable config
instead of the command line (M8b). A standalone pod reads the read-only ConfigMap mount and
keeps the command-line form, which nothing can rewrite. The guard is rendered only while the
option is on, or the wrapper change would roll every writable-config data tier at the operator
upgrade.

**What the choice changes:** the exporter container's environment, the `valkey` container's
command, and under E one init step and one Memory emptyDir. **What it does not change:** the
probes, the init scripts, the sidecar, the observer, the operator and the Sentinel tier stay on
the default user; replication stays on `masterauth`; no ConfigMap carries a credential (ADR 0016
D3/D4); clusters without `spec.auth` are unaffected. Switching an option on changes the pod
template and rolls that data tier through the failover-aware rolling update, lossless except
for a single non-persistent pod, whose dataset is lost.

- **A — refuse, and record the refusal.** Every component, the exporter included, stays on the
  default user; ADR 0016 gains the refusal and its reason as a residual risk, and H-6 is
  rewritten as accepted. Cost XS, no code. Consequence: the exporter — third-party code whose
  pin has to keep moving
  ([ticket 054](054-renovate-does-not-track-the-default-exporter-image.md)), and whose next
  versions add default commands — keeps `FLUSHALL`, `CONFIG SET`, `REPLICAOF` and
  `CONFIG GET masterauth` on every auth-enabled cluster with metrics on. A future exporter
  compromise stays total.
- **D — an exporter user from a user-provided Secret.** An optional field under `spec.metrics`
  (name to be decided), default off
  ([ADR 0005](../adr/0005-upgrade-neutral-defaults-and-anti-affinity.md) D1), inert without
  `spec.auth`. The `valkey` container gets the new `secretKeyRef` and declares the user with the
  Decision 2 command set and the rewrite guard; the exporter gets `REDIS_USER`, `REDIS_PASSWORD`
  from the new Secret and `REDIS_EXPORTER_CONFIG_COMMAND=-`. Reopens ADR 0016 D2 for one
  consumer; D1 holds because the user owns the Secret. Cost M: one field and
  `make generate-all`, the wrapper change with the guard, three exporter env vars, unit tests,
  an e2e on both lines with a `/metrics` helper, the docs. Consequences: every CR author who
  wants it creates, encrypts for GitOps (SOPS or sealed) and rotates a second Secret per
  cluster; a changed exporter password reaches no running pod (ticket 051's gap, ADR 0016 D12);
  a missing Secret or key stops the `valkey` container with `CreateContainerConfigError`, as
  `spec.auth` does today; the 12 `redis_config_*` series are gone.
- **E — an exporter user whose password is generated in the pod (recommended).** An opt-in
  boolean under `spec.metrics` (name to be decided), default off, inert without `spec.auth`. A
  new init step on `spec.image` writes a password once per pod into a Memory emptyDir
  (`head -c 32 /dev/urandom | sha1sum | cut -d' ' -f1`, tools already declared) and the
  exporter's password-file JSON, keyed byte-identical to the `REDIS_ADDR` the builder renders.
  The `valkey` container declares the user with that password (read with the shell builtin
  `read -r`, or `cat` added to `RequiredImageTools`), the Decision 2 set and the rewrite guard.
  The exporter gets `REDIS_USER`, `REDIS_PASSWORD_FILE` and
  `REDIS_EXPORTER_CONFIG_COMMAND=-`, and **no** `REDIS_PASSWORD` (M9: the file is used only
  when `REDIS_PASSWORD` is empty). The password has to come from an init step, not the
  `valkey` wrapper: the exporter reads its file once at start, and a restart of the `valkey`
  container alone must find the same password. Cost M, about D's size: the init step and the
  emptyDir replace D's `secretKeyRef`, and no CRD Secret reference is added. It reopens ADR 0016
  D2 for one consumer, and D1's **rule** holds — no Secret is generated — but its rationale
  sentence "never generating a credential" needs an explicit carve-out for an in-pod credential
  that is never persisted. Consequences: the credential is not in etcd, not in Git, not in the
  operator's cluster-wide Secret cache (ADR 0016 D2/D5) and never in status; it dies with the
  pod, so there is nothing to rotate and nothing to orphan. The 12 `redis_config_*` series are
  gone.
- **F — E for every auth- and metrics-enabled cluster, no toggle.** Treats the exporter's
  rights as a defect of the operator's own posture, fixed fleet-wide like ADR 0032. Cost M, as
  E. Consequences: it is not posture-only — it removes the 12 `redis_config_*` series (and under
  2b two slowlog series) from every metrics-enabled cluster, and it silently breaks every
  `spec.metrics.extraArgs` user of `--check-keys`, `--check-single-keys`, `--count-keys` or
  `--export-client-list`, whose commands the fixed set denies, so it would need an opt-out,
  which the fleet-wide posture rule does not allow; and it rolls every auth- and
  metrics-enabled data tier at the operator upgrade, where a single non-persistent data pod
  loses its dataset.

**E is recommended** because it narrows the exporter exactly as far as D does — the same
measured command set, `redis_up 1` on both lines (M5, M9, M11) — and beats D on three
checkable points. Adoption: E is one boolean in a GitOps-managed CR; D needs a second Secret
per cluster, created, encrypted and rotated by each CR author, which is the adoption problem
that removed C. Rotation: D's Secret inherits ticket 051's gap (ADR 0016 D12); E's credential
is replaced with every pod, by any pod replacement, with nothing to propagate. Failure modes: D
adds a `secretKeyRef` whose missing key stops the `valkey` container; E adds none. D's
strongest points — it keeps D1's wording literally intact, and it adds no init step or volume
— do not outweigh that: none of D1's three stated reasons (rotate, leak into status, orphan)
applies to a credential that lives and dies inside the pod, and both D and E depend on
redis_exporter-specific environment names, so neither is more portable across exporter images.
F loses to E on the dormant trigger of a hardening item against a fleet-wide series loss, a
silent `extraArgs` breakage and a roll of every metrics-enabled data tier; it is re-weighed
only if this ticket's security class changes. A loses because the fix is measured and M-sized,
and A leaves a third-party process whose pin keeps moving with `+@all` and `CONFIG GET` access
to the password.

### Decision 2 — does the exporter user keep `SLOWLOG GET`? (only if Decision 1 is D, E or F)

**Mechanism.** Without a decision the set is fixed by measurement:
`resetkeys resetchannels -@all +info +client|setname +latency|latest +latency|histogram +slowlog|len`,
with `REDIS_EXPORTER_CONFIG_COMMAND=-`, re-measured whenever the default exporter pin moves
(the field's docs say that an `extraArgs` feature needing more commands loses its series). The
one open question is `+slowlog|get`: the exporter uses it only for the id and the duration of
the newest slowlog entry, but the grant returns the full arguments of every logged command,
values included (M10), to a user that is refused `GET`. The choice decides which 2 series the
exporter can produce and whether the exporter user can read data at all. It does not change
the default user, any other component, or the refusal of `CONFIG GET`, `FLUSHALL`,
`REPLICAOF` and `CONFIG SET`.

- **2a — keep `+slowlog|get`.** `redis_slowlog_last_id` and
  `redis_last_slow_execution_duration_seconds` stay, `ACL LOG` stays empty per scrape (M5).
  Residual: a compromised exporter reads the arguments of every command slower than
  `slowlog-log-slower-than` (default 10 ms), key names and values included.
- **2b — drop `+slowlog|get` (recommended).** Those 2 series are gone; `redis_slowlog_length`
  and `redis_up 1` stay (M11); one `ACL LOG` entry per pod stands, grouped with a count as long
  as scrapes are less than 60 s apart. The exporter user reads no data at all.

**2b is recommended** because the ticket's threat is rights the exporter needs and nobody else
in its process holds, and `resetkeys` means nothing while `SLOWLOG GET` hands out the arguments
of slow `SET`, `MSET` or `EVAL` calls (M10). 2a's argument is the clean `ACL LOG`; it loses
because grouping keeps the log readable (M11: one entry, `count 3`), so a compromised
exporter's probe of any other command still shows as a new entry. No shipped alert or doc uses
either lost series. The Verification assertion on `ACL LOG` follows the choice.

## Work list

**Not waiting on a decision:**

- ~~No XS item. One S item informs the decision and is needed for any of B, C or D. It is a
  docker spike on Valkey 8 and 9 (image-tools style, no Kind): `--user` on the `valkey-server`
  command line; the exporter's commands, read from `ACL LOG` under a deny-all user;
  `masteruser` and Sentinel `auth-user`.~~ *(corrected 2026-09-27 at `84a39c2`: done, see Fact
  M1–M11. Reading `ACL LOG` under a deny-all user does not work, because the exporter returns
  from the scrape when `INFO` fails (`exporter.go:690–697` at v1.66.0) and never reaches
  `LATENCY` or `SLOWLOG`; the command set came from the source plus a run under the candidate
  user.)*
- ~~File the documentation finding under "Findings for other tickets" below as its own ticket.
  This change edits only this ticket, so it is not filed yet.~~ *(done 2026-09-27: filed as
  [T70](070-tracked-comments-and-adrs-state-what-the-code-contradicts.md), item (e).)*

**Waiting on the decision** (items for D, E or F):

1. The CRD field (a Secret reference under D, a boolean under E, none under F) and
   `make generate-all`; the field's docs state the lost series and the `extraArgs`
   incompatibility.
2. The `valkey` wrapper ([`statefulset.go:826–832`](../../internal/builder/statefulset.go)):
   the user declaration with the Decision 2 set, and the rewrite guard, rendered only while the
   option is on, stripping only the exporter's own user line (or the init step writing the
   user line into the writable config); the command-line form on standalone pods.
3. The exporter env ([`statefulset.go:1071–1088`](../../internal/builder/statefulset.go)):
   `REDIS_USER`, `REDIS_EXPORTER_CONFIG_COMMAND=-`, and `REDIS_PASSWORD` from the new Secret
   (D) or `REDIS_PASSWORD_FILE` with no `REDIS_PASSWORD` (E, F).
4. E and F only: the init step and the Memory emptyDir mounted into it, the `valkey` container
   and the exporter; `read -r` in place of `cat`, or `cat` added to `RequiredImageTools`
   (the unit test that walks the generated scripts fails otherwise). Whether the step writes
   unconditionally or only when the file is absent: with the rewrite guard both stay
   consistent across a restart of the `valkey` container and across a sandbox restart, where
   the init steps re-run and every container restarts (by reading, not measured).
5. Unit tests: the pod template is unchanged while the field is unset; under E and F the
   exporter env carries no `REDIS_PASSWORD`; the password-file key equals the rendered
   `REDIS_ADDR` for the plain and the TLS scheme (a mismatch sends no `AUTH` and passes every
   `ACL DRYRUN` check while `redis_up` is 0).
6. An e2e helper that reads the exporter's `/metrics` (none exists), and the e2e of
   Verification on both lines, including a Sentinel failover followed by a restart of the
   `valkey` container in the same pod.
7. The README CRD reference, and the `spec.metrics` block of the CRD example in `CLAUDE.md`.
8. A re-measurement of the command set on every exporter pin bump, recorded in
   [ticket 054](054-renovate-does-not-track-the-default-exporter-image.md)'s review path.

**Close (ADR 0034):** amend ADR 0016 (D2; under E the rationale wording of D1; under A the
refusal as a residual risk), H-6 and the consumer table row for the exporter
([`secrets-and-tls.md:25`](../security/secrets-and-tls.md)),
[`docs/operations/authentication.md:26`](../operations/authentication.md) ("The sidecar, the
metrics exporter and the observer authenticate with it"),
[`docs/operations/monitoring.md:15`](../operations/monitoring.md) ("the exporter reuses … the
auth Secret"), the README CRD reference and the `CLAUDE.md` CRD example.
`git grep -n 'T50\|050-every-component'` outside `docs/tickets/` (none on 2026-09-27 at
`84a39c2`), then move to `archive/`.

**Findings for other tickets** (found in this re-verification, not this ticket's work):

- **Ticket 051** ([a changed cluster password reaches no running pod](051-a-changed-cluster-password-reaches-no-running-pod.md)):
  D's user Secret inherits 051's propagation gap, E's per-pod credential does not. 051's
  runtime rotation of the default user does not conflict with a command-line exporter user,
  and with the exporter user lacking `config|get`, keeping `masterauth` readable does not
  reopen M1's leak. A 051 option that rolls on the auth Secret's version would also have to
  watch D's Secret if D is chosen. 051's pointer to this ticket's `secretKeyRef` sites holds at
  `84a39c2`. *(Added 2026-09-27, consistency pass: 051 now recommends C, operator-driven runtime
  rotation, closed by C-b, a failover-aware roll on a non-content rotation record in the hashed
  pod spec; if D is chosen here, that record and the ensure-both step have to cover D's Secret as
  well, E needs neither. Two facts 051 measured belong here too: `valkey-cli` exits 0 on
  `NOAUTH` on both pinned lines (051 M5), so a dedicated probe user would authenticate nothing
  the probe checks - which supports keeping the probes out of scope; and a Sentinel's own
  `requirepass` governs its links to its peers unless `sentinel-pass` is set (051 M6), which
  bears on the Sentinel `auth-user` measurement M4 above: a Sentinel ACL user changes the
  Sentinel-to-data links, not the Sentinel-to-Sentinel ones (inference from the split
  between `auth-user` and `sentinel-user`/`sentinel-pass`; not measured here).)*
- **Ticket 054** ([Renovate does not track the default exporter image](054-renovate-does-not-track-the-default-exporter-image.md)):
  under D, E or F an automatic bump of the exporter pin drops series silently (`NOPERM` is
  non-fatal except on `INFO`), and v1.92.0 already adds `COMMANDLOG` commands. 054's choice
  between manual review and automerge should account for that. 054 cited
  `valkey_types.go` line 643 for the constant, which is at 645 at `84a39c2`; 054's own
  re-verification of 2026-09-27 already corrected it to 645.
- **Ticket 036** ([a non-persistent master restarts empty](036-non-persistent-master-restarts-empty.md)):
  036 measured Sentinel's `CONFIG REWRITE` of the data pod's writable config; the same
  mechanism causes M8. A 036 fix that re-renders `/etc/valkey-active` on a container restart
  changes the rewrite guard's requirement, so the two are designed together.
- **Documentation** — filed as
  [T70](070-tracked-comments-and-adrs-state-what-the-code-contradicts.md), item (e): ADR 0016 and
  [`secrets-and-tls.md`](../security/secrets-and-tls.md) attribute the password in `/proc` to
  `valkey-server`'s argv (measured false; the carrier is `valkey-cli -a` in the probes and init
  steps) and do not name the cleartext `/etc/valkey-active` after a `CONFIG REWRITE` (M8).

## Decision

Not decided.

## Verification

- ~~e2e on both pinned lines, with the option on: each component user is refused `FLUSHALL`
  (for example through `ACL DRYRUN`), and the rolling-update, failover and drain suites stay
  green, which proves the sidecar's and the operator's commands still pass.~~ *(corrected
  2026-09-27 at `84a39c2`: a `FLUSHALL` refusal alone would also pass upstream's recommended
  ACL, which still returns the password through `CONFIG GET masterauth` (M1); and under D, E
  or F only the exporter changes, so the roll, failover and drain suites prove nothing about
  it. They stay as regression runs.)* e2e on both pinned lines with the option on:
  - as the exporter user, `ACL DRYRUN` refuses `CONFIG GET masterauth`,
    `CONFIG GET requirepass`, `FLUSHALL`, `REPLICAOF`, `CONFIG SET` and `GET`, and under 2b
    `SLOWLOG GET`;
  - the exporter's `/metrics` reports `redis_up 1` (new helper);
  - after a scrape `ACL LOG` is empty under 2a, or holds no entry other than `slowlog|get`
    under 2b;
  - a Sentinel failover followed by a restart of the `valkey` container in the same pod
    brings the pod back (M8);
  - under E and F, the exporter container's environment has no `REDIS_PASSWORD`.
- A negative control: the default user is still allowed `FLUSHALL`
  ([ADR 0017](../adr/0017-test-and-ci-policy.md) D11).
- ~~*(added 2026-09-27, option D)* The exporter's `/metrics` still reports
  `redis_up 1` with the option on.~~ *(corrected 2026-09-27 at `84a39c2`: folded into the
  first bullet, it applies to D, E and F.)*
- Unit: the password-file key equals `REDIS_ADDR` for both schemes, and the template is
  unchanged while the field is unset (Work list item 5).

## History

- 2026-09-27 — re-verified at `84a39c2`. Checked every Fact location against the code; they
  hold, except two +2 drifts in `valkey_types.go` fixed in the links (`spec.image` 1032–1034,
  `DefaultMetricsExporterImage` 645); locations were re-read at `84a39c2`. Found false or
  outdated and corrected in place: "the only third-party code" (it is the only third-party
  client; `spec.image` is third-party too), the "no match" grep (one comment match), the
  Impact reason for the sidecar and the observer (a runtime compromise is not an operator
  compromise; the conclusion holds for other reasons), "Dormant until a component is
  compromised" (narrowed to the exporter), option D's "one env var on the exporter" (three)
  and "the command set moves only when the pin does" (also with `extraArgs` and
  `spec.metrics.image`), option D's claim that declaring the user on the existing command line
  suffices (M8), and the first Verification bullet. The open spike was run in docker on 9.1.1
  and 8.1.9 with the pinned exporter: M1–M8, M8b, M9, M10 and M11, with their
  commands in Fact; the four "Not verified" items it covered moved to Verified, and new
  unverified items were added. **Options removed:** **B** (operator-generated credentials for
  every component) — disproportionate to a medium hardening item: it reopens ADR 0016 D1 in
  full (an operator-owned Secret lifecycle under ADR 0020) to narrow components where a user
  buys almost nothing, needs username support in `valkeyclient` and Sentinel-side users, effort
  L; **C** (user-provided Secrets for every component) — duplicates D for the one component
  where it matters and adds credentials that narrow nothing (the probe) or little (sidecar,
  observer). **Candidates considered and not kept:** upstream's recommended exporter ACL
  (grants `+config|get`, `+client`, `+eval`, so it narrows nothing, M1); a CR field for extra
  ACL rules (speculative, lets a CR author re-grant `config|get` and puts user strings into the
  `sh -c` argv); keeping `CONFIG GET` while hiding the password (every data pod needs
  `masterauth`, and `requirepass` returns it too; a replication user is B's territory, for 12
  series); a `nopass` exporter user (exposes `INFO` and `SLOWLOG GET` on 6379 without
  authentication). **Options added:** E (in-pod generated exporter credential) and F (E
  fleet-wide, no toggle); A is restated as "refuse and record the refusal in ADR 0016".
  **Recommendation changed** from D to E: same measured narrowing, no second Secret per
  cluster, no ticket-051 rotation gap, no new `secretKeyRef` failure mode; E keeps D1's rule
  and needs only a clarification of its rationale wording. Decision 2 (`SLOWLOG GET`) was split
  out after M10 showed that `+slowlog|get` returns command arguments, values included; 2b (drop
  it) is recommended on M11. The Work list lost the spike and gained the rewrite guard, the
  password-file key test, the `cat` tool question, the `/metrics` helper and the
  failover-plus-restart e2e; the Close list gained the exporter consumer row,
  `authentication.md:26`, `monitoring.md:15` and the `CLAUDE.md` example. Cross-ticket findings
  for 051, 054 and 036 and a documentation finding for a new ticket are recorded under
  "Findings for other tickets". **Frontmatter:** state `filed` → `analysed` (every fact the
  decision needs is verified and the options are complete); effort `L` → `M` (L was for B or C,
  both removed); the threat line narrowed to the exporter (a probe is not a component of its
  own, and no sidecar or observer user is pursued) and gained the `CONFIG GET masterauth`
  read-back; the severity comment gained the same; urgency `icebox` re-derived top-down (rule
  5; rule 1 does not match, the measured-false ADR 0016 statement belongs to its own ticket; rule
  2 does not match; rule 3 does not match on the dormant trigger; rule 4 does not match, nothing decided and
  the fix is M); blocked-by names D1's rationale wording under E. Severity `medium` and
  security `hardening` unchanged.
  Cross-ticket: in the consistency pass of the same day, the 051 note gained 051's current
  recommendation (C closed by C-b; under D here its rotation record has to cover D's Secret, E
  needs nothing) and the two 051 measurements it had marked for this file (`valkey-cli` exits 0 on
  `NOAUTH`, M5; a Sentinel's own password governs its peer links unless `sentinel-pass` is set,
  M6); 054 corrected its stale reading of this ticket's option D. Filed: the documentation finding
  (ADR 0016 and `secrets-and-tls.md` naming `valkey-server`'s argv as the `/proc` carrier of the
  password, and the unnamed cleartext `/etc/valkey-active`) is filed as T70, item (e); its
  analysis left "Findings for other tickets" for a pointer, the Work list item to file it is
  marked done, and the urgency comment names T70. M8's "no `masterauth` line" is corrected in
  place from T70's M2 (Valkey rewrites it as `primaryauth`). Frontmatter values unchanged: no
  severity, urgency, option or recommendation rested on the moved finding.
- 2026-09-27 — enriched - located every `secretKeyRef`, corrected the `ProbeCommand` lines, and
  added which image each consumer runs. Added option D (exporter-only user,
  user-provided Secret) and moved the recommendation from B to D, because a probe, sidecar or
  observer user narrows little. Urgency, effort and blocked-by unchanged.
- 2026-09-27 — filed from the row "Least-privilege Valkey ACL users for probes, sidecar,
  exporter, observer" of archive/031. That row names ADR 0016; the D1 conflict in option B was
  found while filing. Gap [H-6](../security/secrets-and-tls.md#h-6) states what is missing; its
  "open follow-up" lead-in was removed from the page in the same change.
