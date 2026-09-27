---
id: T50
title: every component authenticates with the one cluster password and full rights
state: analysed       # every fact the decision needs is verified, the options are complete
severity: medium      # a compromised exporter can flush or re-point the dataset and reads the cluster password back
security: hardening
threat: "would additionally cover a compromised exporter (third-party code chosen by spec.metrics.image, default redis_exporter v1.66.0, in every data pod of an auth-enabled cluster with metrics on): today it authenticates as the default user, may run FLUSHALL, CONFIG SET or REPLICAOF, and reads the cluster password back with CONFIG GET masterauth"
urgency: icebox       # rule 5: dormant trigger, nothing decided, fix is M
effort: M             # each of the options D, E and F is M
blocked-by: decision  # ADR 0016 D2 (and the rationale wording of D1 under E), see Q1
filed-from: T31, section "Further security measures — not in this change, each open" (archive/031)
opened: 2026-09-27
decided:
done:
---

# T50 - every component authenticates with the one cluster password and full rights

The operator-facing statement of the gap is [H-6](../security/secrets-and-tls.md#h-6).

## Current state

**One password, one user.** Every consumer gets the same password from the same `spec.auth`
`secretKeyRef` ([ADR 0016](../adr/0016-authentication-and-tls-posture.md) D2):
[`statefulset.go`](../../internal/builder/statefulset.go) lines 381 and 566 (the two
`init-config-selector` variants), 879 (`valkey`), 951 (`sidecar`), 1078–1084 (exporter);
[`sentinel.go`](../../internal/builder/sentinel.go) 356 (`init-sentinel-config`) and 563
(`sentinel`, only while `spec.sentinel.disableAuth` is false, line 559);
[`observer.go`](../../internal/builder/observer.go) 249–258. The operator reads the same key
([`valkey_controller.go:177`](../../internal/controller/valkey_controller.go),
[`checker.go:75`](../../internal/health/checker.go)). `valkey-server` starts with
`--requirepass "$VALKEY_PASSWORD" --masterauth "$VALKEY_PASSWORD"` (`statefulset.go:830`). No
config builder renders an ACL directive, so every client is the default user,
`user default on sanitize-payload #… ~* &* +@all` (measured with `ACL LIST` on 9.1.1 and 8.1.9).

**The exporter is the one component where a user of its own narrows something.** It runs
`spec.metrics.image`, default `DefaultMetricsExporterImage`
([`valkey_types.go:645`](../../api/v1/valkey_types.go), redis_exporter v1.66.0, pinned by
digest), the only third-party client process in the pod. Its container gets only `REDIS_ADDR`
(`redis://localhost:6379`, or `rediss://localhost:16379` under TLS, `statefulset.go:1063–1073`),
the listen address, `REDIS_PASSWORD` (1076–1088) and the TLS paths; no token, no config volume.
The other components stay out of scope:

- probes and init containers run `valkey-cli` beside the `valkey` container, which holds the
  password anyway; `valkey-cli` also exits 0 on `NOAUTH`, so a probe user would check nothing;
- the `sidecar` needs `REPLICAOF` and `SENTINEL FAILOVER` anyway
  ([`drain.go:139,172,365`](../../internal/sidecar/drain.go)), the observer serves only
  read-only handlers ([`server.go:15–31`](../../internal/observer/server.go)), and
  [`client.go:410–424`](../../internal/valkeyclient/client.go) cannot send a username.

**Impact.** Dormant until the exporter is compromised; then total. On every auth-enabled
cluster with metrics on, whoever controls the exporter may `FLUSHALL`, `CONFIG SET` or
`REPLICAOF`, and reads the cluster password through `CONFIG GET masterauth`. Today's mitigation
is the digest pin ([ADR 0033](../adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md)
D5) and leaving metrics off where not needed.

**Measured facts that shape the fix** (docker, `valkey/valkey:9.1.1` and `8.1.9`, exporter
v1.66.0, unless noted):

- **`CONFIG GET` returns the password.** Any user granted `+config|get` reads `masterauth` and
  `requirepass` in cleartext; a per-parameter grant (`+config|get|maxmemory`) is refused by
  Valkey. So the exporter user must not hold `config|get` (which upstream's recommended exporter
  ACL grants), and the exporter runs with `REDIS_EXPORTER_CONFIG_COMMAND=-`.
- **The minimal user works.** `resetkeys resetchannels -@all +info +client|setname
  +latency|latest +latency|histogram +slowlog|len +slowlog|get` with `REDIS_USER` and
  `REDIS_EXPORTER_CONFIG_COMMAND=-`: `redis_up 1`, latency and slowlog series present, `ACL LOG`
  empty. `ACL DRYRUN` refuses `config|get`, `flushall`, `replicaof`, `client|kill`,
  `client|pause`, `slowlog|reset`, `latency|reset`, `debug`.
- **Without `CONFIG GET`** 12 series disappear: `redis_config_maxmemory`,
  `redis_config_maxclients`, `redis_config_io_threads` and 9
  `redis_config_client_output_buffer_limit_*` (9.1.1). Keyspace series stay (the exporter falls
  back to 16 databases, which [`configmap.go:121`](../../internal/builder/configmap.go)
  renders). No shipped alert or doc uses a `redis_*` series.
- **`SLOWLOG GET` hands out data.** As the restricted user, `GET customer:42:token` is refused
  but `SLOWLOG GET 5` returns `SET customer:42:token tok-abc-123`. The exporter uses only the id
  and duration of the newest entry. Without `+slowlog|get`: `redis_up 1`, `redis_slowlog_length`
  stays, `redis_slowlog_last_id` and `redis_last_slow_execution_duration_seconds` go, and
  `ACL LOG` holds one grouped entry (`object slowlog|get`, `count 3` after three scrapes).
- **A command-line user crash-loops after a `CONFIG REWRITE`.** `--user exporter on >pw …` on
  the `valkey-server` command line is accepted, but after `CONFIG REWRITE` the config file holds
  the user too, and the next restart fails with `Duplicate user found` (exit 1). The same user
  written into the writable config file instead survives rewrite and restart (9.1.1 only).
  Sentinel sends `CONFIG REWRITE` after every `REPLICAOF` it sends, in and outside a failover;
  the operator and the sidecar never do. The rewritten file is the writable `/etc/valkey-active`
  emptyDir, used on Sentinel and on multi-replica clusters without Sentinel
  (`statefulset.go:645–661`); a standalone pod reads the read-only ConfigMap mount.
- **Password-file mode.** With `REDIS_PASSWORD_FILE` pointing at `{"<uri>":"<pw>"}` and no
  `REDIS_PASSWORD`: `redis_up 1` (9.1.1). The file is read once at start, only when
  `REDIS_PASSWORD` is empty, and looked up by the exact URI; a key that differs from `REDIS_ADDR`
  sends no `AUTH` (`redis_up 0`).
- **The command set moves** with the pin, `spec.metrics.image` and `spec.metrics.extraArgs`
  ([`valkey_types.go:675–677`](../../api/v1/valkey_types.go), appended at
  `statefulset.go:1127–1129`): `--check-keys`, `--check-single-keys` and `--count-keys` need
  `SELECT`, `SCAN`, `TYPE`, `GET`, `STRLEN`; `--export-client-list` needs `CLIENT LIST`; v1.92.0
  adds `COMMANDLOG` commands. By reading, `extraArgs` can pass `--include-config-metrics
  --redact-config-metrics=false`, publishing `masterauth` as a metric label; no `config|get`
  closes that too.
- No e2e reads the exporter's `/metrics`. [`RequiredImageTools`](../../internal/builder/image_requirements.go)
  declares `head`, `sha1sum`, `cut` and `sed`, but not `cat`.

## Required changes

**Under Q1 = A:** ADR 0016 records the refusal and its reason as a residual risk; H-6 is
rewritten as accepted. No code.

**Under Q1 = D, E or F:**

1. CRD field under `spec.metrics` (Secret reference under D, boolean under E, none under F),
   `make generate-all`; its docs name the lost series and the `extraArgs` incompatibility.
2. `valkey` wrapper (`statefulset.go:826–832`): declare the exporter user with the Q2 command
   set. On writable-config topologies add a **rewrite guard**: either strip only the exporter's
   own `user` line from `/etc/valkey-active` before `exec` (never `user default`), or write the
   user line into the writable config instead of the command line. Standalone pods keep the
   command-line form. Rendered only while the option is on, so the upgrade rolls nothing.
3. Exporter env (`statefulset.go:1071–1088`): `REDIS_USER`, `REDIS_EXPORTER_CONFIG_COMMAND=-`,
   and `REDIS_PASSWORD` from the new Secret (D) or `REDIS_PASSWORD_FILE` with **no**
   `REDIS_PASSWORD` (E, F).
4. E and F: an init step on `spec.image` writes a per-pod password
   (`head -c 32 /dev/urandom | sha1sum | cut -d' ' -f1`) and the exporter's password-file JSON,
   keyed byte-identical to the rendered `REDIS_ADDR`, into a Memory emptyDir mounted into the
   init step, `valkey` and the exporter. The `valkey` container reads it with `read -r`, or
   `cat` is added to `RequiredImageTools`.
5. Unit tests: pod template unchanged while the field is unset; under E and F no
   `REDIS_PASSWORD` in the exporter env; password-file key equals `REDIS_ADDR` for both schemes.
6. e2e on both pinned lines with the option on, with a new helper that reads `/metrics`:
   - as the exporter user, `ACL DRYRUN` refuses `CONFIG GET masterauth`, `CONFIG GET
     requirepass`, `FLUSHALL`, `REPLICAOF`, `CONFIG SET`, `GET`, and under 2b `SLOWLOG GET`;
   - `/metrics` reports `redis_up 1`;
   - after a scrape `ACL LOG` is empty (2a) or holds only the `slowlog|get` entry (2b);
   - a Sentinel failover followed by a restart of the `valkey` container in the same pod brings
     the pod back;
   - negative control: the default user is still allowed `FLUSHALL`
     ([ADR 0017](../adr/0017-test-and-ci-policy.md) D11).
7. Re-measure the command set on every exporter pin bump (T54's review path).

**Documentation on close (all options):** ADR 0016 (D2; D1's rationale wording under E; the
refusal under A), H-6 and the exporter row of
[`secrets-and-tls.md:25`](../security/secrets-and-tls.md),
[`authentication.md:26`](../operations/authentication.md),
[`monitoring.md:15`](../operations/monitoring.md), the README CRD reference and the
`spec.metrics` block of the CRD example in `CLAUDE.md`.

## Open questions

### Q1: Does the exporter get a Valkey user of its own, and where does its credential come from?

Today the exporter is the default user with `+@all`. A least-privilege user is measured to work
on both lines; the choice is whether to do it and how the password reaches `valkey` and the
exporter. Every doing-option rolls the affected data tier once, lossless except for a single
non-persistent pod, and drops the 12 `redis_config_*` series.

- **A - refuse and record.** No code; ADR 0016 and H-6 record the accepted risk. The exporter,
  whose pin keeps moving and whose next versions add commands, keeps `FLUSHALL`, `REPLICAOF`,
  `CONFIG SET` and the password.
- **D - user from a user-provided Secret.** Opt-in field referencing a second Secret, inert
  without `spec.auth`. Keeps ADR 0016 D1 literally intact. Every CR author creates, encrypts
  for GitOps and rotates a second Secret per cluster; a changed password reaches no running pod
  (T51's gap); a missing key stops the `valkey` container with `CreateContainerConfigError`.
- **E - password generated in the pod (recommended).** Opt-in boolean, inert without
  `spec.auth`; an init step writes a per-pod password into a Memory emptyDir. The credential is
  never in etcd, Git, the operator's Secret cache or status, dies with the pod and needs no
  rotation. D1's rule (the operator never generates a Secret) holds; its rationale sentence
  "never generating a credential" needs a carve-out for an in-pod, never-persisted credential.
- **F - E for every auth- and metrics-enabled cluster, no toggle.** Rolls every such data tier
  at the operator upgrade (a single non-persistent pod loses its data), drops the series
  fleet-wide and silently breaks every `extraArgs` user of `--check-keys`,
  `--check-single-keys`, `--count-keys` or `--export-client-list`, which would need an opt-out.

E narrows exactly as far as D, with one boolean instead of a second Secret per cluster, no
rotation gap and no new `secretKeyRef` failure mode; none of D1's stated reasons (rotate, leak
into status, orphan) applies to a credential that lives and dies in the pod. F costs too much
for a dormant hardening trigger; A leaves a third-party process with `+@all` and the password.

**Answer:** _open_

### Q2: Does the exporter user keep `SLOWLOG GET`? (only if Q1 is D, E or F)

The exporter uses `SLOWLOG GET` only for the id and duration of the newest entry, but the grant
returns the full arguments of every logged command (default threshold 10 ms), values included,
to a user refused `GET`.

- **2a - keep `+slowlog|get`.** `redis_slowlog_last_id` and
  `redis_last_slow_execution_duration_seconds` stay and `ACL LOG` stays empty; a compromised
  exporter reads key names and values of slow commands.
- **2b - drop `+slowlog|get` (recommended).** Those two series go; `redis_slowlog_length` and
  `redis_up 1` stay, and one grouped `ACL LOG` entry per pod stands. The exporter user reads no
  data at all.

2b makes `resetkeys` mean something; the grouped log entry keeps `ACL LOG` readable, so any
other denied command still shows as a new entry. No shipped alert or doc uses either series.

**Answer:** _open_

## Not verified

- Settled by the e2e of item 6: E's init step on a real rootless pod (`readOnlyRootFilesystem`,
  `fsGroup` 999, Memory emptyDir, user namespace); D or E under TLS; the rewrite guard's
  config-file form on 8.1.9 and on a real Sentinel pod after a failover; whether the init step
  writing unconditionally or only when absent stays consistent across restarts (both do by
  reading).
- That the single `ACL LOG` entry under 2b stays one over hours: by reading, Valkey groups a
  matching denial updated within 60 s, so it holds for scrape intervals below 60 s; measured
  only over three scrapes.
- Exporter images other than redis_exporter: every option assumes a compatible
  `spec.metrics.image`, as today's `REDIS_ADDR`/`REDIS_PASSWORD` wiring already does.

## Related

- [T51](051-a-changed-cluster-password-reaches-no-running-pod.md) - D's Secret inherits T51's
  propagation gap; a T51 rotation record would have to cover D's Secret, E needs nothing.
- [T54](054-renovate-does-not-track-the-default-exporter-image.md) - under D, E or F an exporter
  pin bump can drop series silently; the bump needs a command-set re-measurement.
- [T36](036-non-persistent-master-restarts-empty.md) - same `CONFIG REWRITE` of
  `/etc/valkey-active`; a T36 fix re-rendering it changes the rewrite guard, design together.
- [T70](070-tracked-comments-and-adrs-state-what-the-code-contradicts.md) - item (e) corrects the
  password's `/proc` carrier in ADR 0016 and names the cleartext `/etc/valkey-active`.
