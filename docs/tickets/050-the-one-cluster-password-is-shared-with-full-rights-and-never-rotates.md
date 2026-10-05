---
id: T50
title: the one cluster password is shared by every component with full rights, and a change reaches no running pod
state: analysed       # every fact the decisions need is measured in docker on both pinned lines; no end-to-end rotation on a cluster
severity: medium      # rotation replaces every pod by hand and loses non-persistent data; a compromised exporter can flush the dataset and read the password
security: hardening
threat: "would additionally cover a compromised exporter (third-party code in every data pod of an auth-enabled cluster with metrics on, today the default user with +@all and able to read the password) and a leaked cluster password, which stays valid on every pod not yet replaced by hand, with its authenticated connections surviving until then"
urgency: now          # the rotation part: ADR 0016 D13 states a measured-false premise; next once its correction lands
effort: L             # rotation option C with C-b and (a) is L, the exporter user M; the decision-free items are XS to S
blocked-by: decision  # Q1 and Q3-Q5; the decision-free items are not blocked
filed-from: T31, section "Further security measures — not in this change, each open" (archive/031)
opened: 2026-09-27
decided:
done:
---

# T50 - the one cluster password is shared by every component with full rights, and a change reaches no running pod

**Scope.** Every consumer authenticates as the default user with the one `spec.auth` password,
fixed per process from its start: nothing narrows what a component may do with it, and nothing
carries a new value into a running pod. Both parts touch the same wiring (the `valkey` wrapper,
every container's `secretKeyRef`, ADR 0016 D1, D2, D12, D13) and interact: a second credential
needs its own rotation, and the env-pinned consumers decide when an old password can be
withdrawn. Operator-facing: [H-6](../security/secrets-and-tls.md#h-6),
[the password rotation gap](../security/rotation-and-change-propagation.md#the-password-rotation-gap),
[H-24](../security/rotation-and-change-propagation.md#h-24).

- **Exporter user** - the third-party exporter holds `+@all` and the password.
- **Password rotation** - a changed Secret reaches no running pod, and the documented manual
  rotation loses data. The owner's wish ([`.github/idea.md:7`](../../.github/idea.md)): the
  instances take a new password without losing state, also without a persistent volume.

## Current state

**Shared facts.** Every consumer gets the password from the same `spec.auth` `secretKeyRef`
([ADR 0016](../adr/0016-authentication-and-tls-posture.md) D2):
[`statefulset.go`](../../internal/builder/statefulset.go) lines 381 and 566 (the two
`init-config-selector` variants), 879 (`valkey`), 951 (`sidecar`), 1078–1084 (exporter);
[`sentinel.go`](../../internal/builder/sentinel.go) 356 (`init-sentinel-config`) and 563
(`sentinel`, only while `spec.sentinel.disableAuth` is false, line 559);
[`observer.go`](../../internal/builder/observer.go) 249–258. `valkey-server` starts with
`--requirepass "$VALKEY_PASSWORD" --masterauth "$VALKEY_PASSWORD"` (`statefulset.go:830`). No
config builder renders an ACL directive, so every client is the default user,
`user default on sanitize-payload #… ~* &* +@all`. `valkey-cli` exits 0 on `WRONGPASS`,
`NOAUTH` and any error reply, so the exec probes
([`statefulset.go:1515-1545`](../../internal/builder/statefulset.go),
[`sentinel.go:420-459`](../../internal/builder/sentinel.go)) pass on any server reply (M5). All
measurements below are docker, `valkey/valkey:9.1.1` and `8.1.9`, identical on both unless noted.

### Exporter user

The exporter runs `spec.metrics.image`, default `DefaultMetricsExporterImage`
([`valkey_types.go:692`](../../api/v1/valkey_types.go), redis_exporter v1.92.1, pinned by
digest), the only third-party client process in the pod. Its container gets `REDIS_ADDR`
(`redis://localhost:6379`, or `rediss://localhost:16379` under TLS, `statefulset.go:1071–1079`),
the listen address, the two variables that switch off `/scrape` and key-value export (1081–1082),
`REDIS_PASSWORD` (1086–1098) and the TLS paths; no token, no config volume.
The other components stay out of scope: probes and init containers run beside the `valkey`
container, which holds the password anyway, and a probe user would check nothing (M5); the
`sidecar` needs `REPLICAOF` and `SENTINEL FAILOVER` ([`drain.go:139,172,365`](../../internal/sidecar/drain.go));
the observer serves only read-only handlers ([`server.go:15–31`](../../internal/observer/server.go));
[`client.go:410–424`](../../internal/valkeyclient/client.go) cannot send a username.

**Impact.** Dormant until the exporter is compromised; then total: `FLUSHALL`, `CONFIG SET`,
`REPLICAOF`, and the password through `CONFIG GET masterauth`. Mitigation today is the digest pin
([ADR 0033](../adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md) D5)
and leaving metrics off.

**Measured (exporter v1.66.0):**

- **`CONFIG GET` returns the password.** `+config|get` reads `masterauth` and `requirepass` in
  cleartext; a per-parameter grant is refused by Valkey. The exporter user must not hold it
  (upstream's recommended ACL grants it), and the exporter runs with
  `REDIS_EXPORTER_CONFIG_COMMAND=-`.
- **The minimal user works.** `resetkeys resetchannels -@all +info +client|setname
  +latency|latest +latency|histogram +slowlog|len +slowlog|get` with `REDIS_USER`: `redis_up 1`,
  latency and slowlog series present, `ACL LOG` empty. `ACL DRYRUN` refuses `config|get`,
  `flushall`, `replicaof`, `client|kill`, `client|pause`, `slowlog|reset`, `latency|reset`,
  `debug`.
- **Without `CONFIG GET`** 12 series disappear: `redis_config_maxmemory`,
  `redis_config_maxclients`, `redis_config_io_threads` and 9
  `redis_config_client_output_buffer_limit_*` (9.1.1). Keyspace series stay (fallback to 16
  databases, which [`configmap.go:121`](../../internal/builder/configmap.go) renders). No shipped
  alert or doc uses a `redis_*` series.
- **`SLOWLOG GET` hands out data.** Refused `GET customer:42:token`, the user still reads
  `SET customer:42:token tok-abc-123` from `SLOWLOG GET 5`. The exporter uses only id and
  duration of the newest entry. Without `+slowlog|get`: `redis_up 1` and `redis_slowlog_length`
  stay, `redis_slowlog_last_id` and `redis_last_slow_execution_duration_seconds` go, `ACL LOG`
  holds one grouped entry (`count 3` after three scrapes).
- **A command-line user crash-loops after `CONFIG REWRITE`.** `--user exporter on >pw …` on the
  `valkey-server` command line lands in the config file on rewrite, and the next restart fails
  with `Duplicate user found`. Written into the writable config file instead, the user survives
  rewrite and restart (9.1.1 only). Sentinel sends `CONFIG REWRITE` after every `REPLICAOF`; the
  operator and the sidecar never do. The rewritten file is the writable `/etc/valkey-active`
  emptyDir (Sentinel and multi-replica non-Sentinel, `statefulset.go:645–661`); a standalone pod
  reads the read-only ConfigMap mount.
- **Password-file mode.** `REDIS_PASSWORD_FILE` with `{"<uri>":"<pw>"}` and no `REDIS_PASSWORD`:
  `redis_up 1` (9.1.1). Read once at start, only when `REDIS_PASSWORD` is empty, looked up by the
  exact URI; a key differing from `REDIS_ADDR` sends no `AUTH`.
- **The command set moves** with the pin, `spec.metrics.image` and `spec.metrics.extraArgs`
  ([`valkey_types.go`](../../api/v1/valkey_types.go) `MetricsSpec.ExtraArgs`, appended at
  `statefulset.go:1137–1139`): `--check-keys`, `--check-single-keys`, `--count-keys` need
  `SELECT`, `SCAN`, `TYPE`, `GET`, `STRLEN`; `--export-client-list` needs `CLIENT LIST`; v1.92.0
  adds `COMMANDLOG`. By reading, `extraArgs` can pass `--include-config-metrics
  --redact-config-metrics=false`, publishing `masterauth` as a label; no `config|get` closes that.
- No e2e reads the exporter's `/metrics`. [`RequiredImageTools`](../../internal/builder/image_requirements.go)
  declares `head`, `sha1sum`, `cut`, `sed`, not `cat`.

### Password rotation

**How the password flows.**

- The operator reads the Secret per call (`readValkeyPassword`,
  [`valkey_controller.go:177-190`](../../internal/controller/valkey_controller.go); `readAuthPassword`,
  [`checker.go:75-87`](../../internal/health/checker.go)) and switches at once; the Secret is
  watched (`secretConcernsValkey`, [`valkey_controller.go:3053-3055`](../../internal/controller/valkey_controller.go)).
- Kubelet resolves `secretKeyRef` at every container start, in-place restarts included. The
  pod-spec hash covers the reference, not the value (ADR 0016 D12,
  [`statefulset.go:1471-1482`](../../internal/builder/statefulset.go)), so nothing rolls.
- Sentinel's init writes `requirepass` and `auth-pass` into an `emptyDir` config
  ([`sentinel.go:176-190`](../../internal/builder/sentinel.go), `:715`), fixed per pod; no
  `sentinel-pass` is set.
- Read once at process start: the sidecar ([`cmd/sidecar/sidecar.go:75`](../../cmd/sidecar/sidecar.go))
  and its drain handler ([`drain.go:462-476`](../../internal/sidecar/drain.go)), the observer
  ([`cmd/observer/observer.go:95`](../../cmd/observer/observer.go)), the exporter
  ([`statefulset.go:1075-1087`](../../internal/builder/statefulset.go)).
- ADR 0016 D13 makes rotation manual: change the Secret, then replace pods, replicas first. Its
  claim that a non-persistent cluster loses data only "if it has no failover target"
  ([`0016-...md:185-186`](../adr/0016-authentication-and-tls-posture.md)) is false (M1). ADR 0016
  D1 forbids an operator-owned copy of the password; ADR 0030 D11 forbids a password digest on the
  pod template. Neither is reopened here.

**Measured:**

- **M1:** a replica on the new password cannot sync from a master on the old one
  (`master_link_status:down`, `DBSIZE 0`, `-WRONGPASS`). **M1b:** `ACL SETUSER default >new` on
  that master brings the replica up with the data within 8 s.
- **M2:** `ACL SETUSER default >new` keeps `old` valid; `CONFIG SET requirepass` replaces all.
  **M7:** `>new` is idempotent; runtime changes are not persisted (read-only ConfigMap config).
- **M3:** `masterauth` applies at the next reconnect; an established link survives removal of the
  old password on the master (`<old`). **M4:** authenticated clients survive password removal.
- **M6:** Sentinel refuses `CONFIG SET` but takes `ACL SETUSER default >new`; `SENTINEL SET
  <monitor> auth-pass` applies at once; Sentinels whose own old password is removed mark each
  other `s_down` unless `SENTINEL CONFIG SET sentinel-pass new` is set.

**Between the Secret change and the last pod replacement.**

- The pass fails closed and names no auth failure on Sentinel clusters: `probeMasterRole`
  swallows the error ([`checker.go:193-197`](../../internal/health/checker.go)), phase `Error`,
  `no master found among N pods`, `Ready=False/ClusterHealthCheckFailed`
  ([`valkey_controller.go:2464-2474`](../../internal/controller/valkey_controller.go)). Without
  Sentinel: `Instance unreachable: ... WRONGPASS` (`:2237-2247`). No promotion, no `REPLICAOF`,
  no Event.
- A `valkey` container restarted in place comes back on the new password while its sidecar and
  exporter keep the old one: the labeler freezes `instanceRole`
  ([`labeler.go:124-127`](../../internal/sidecar/labeler.go)), the drain handler exits
  ([`drain.go:106-110`](../../internal/sidecar/drain.go)), the exporter reports down. This follows
  any Secret change today.
- A failover retry in `resetSentinelState` ([`rolling_update.go:3617`](../../internal/controller/rolling_update.go))
  reaches only Sentinels on the new password; with `disableAuth: true` it switches every
  Sentinel's `auth-pass` and cuts them off old-password data pods. Dormant unless a roll's retry
  is in flight.
- `SentinelSet` puts the option value, for `auth-pass` the password, into its error
  ([`client.go:285`](../../internal/valkeyclient/client.go), pinned by
  [`exec_test.go:480-484`](../../internal/valkeyclient/exec_test.go)); every caller discards it,
  so it breaches ADR 0016 D3 only once one logs it.
- Changing `spec.auth.secretName`/`secretPasswordKey` rolls, but with a different password the
  first replica cannot sync (M1), the roll pauses after `syncTimeout`, and a single
  non-persistent pod is replaced at once and loses its data
  ([`pod_security_migration.go:128-150`](../../internal/controller/pod_security_migration.go)).

**Impact.** Every auth-enabled cluster whose password changes: `Error`/`Ready=False` for the
whole window; the manual roll loses the data of every non-persistent cluster at any replica
count. A leaked password stays valid on every unreplaced pod (M4). No e2e changes a password.

## Required changes

**Shared, whatever the answers:** documentation on close covers ADR 0016 (D1 rationale wording
under Q1 = E, D2, D12, D13; the refusal under Q1 = A; or a new ADR for Q3 = C), H-6 and the
exporter row of [`secrets-and-tls.md:25`](../security/secrets-and-tls.md), H-24 and
[`rotation-and-change-propagation.md`](../security/rotation-and-change-propagation.md),
[`authentication.md`](../operations/authentication.md) (`:26` and the rotation section),
[`monitoring.md:15`](../operations/monitoring.md), the README CRD reference and the `spec.auth`
and `spec.metrics` blocks of the CRD example in `CLAUDE.md`. If Q1 = D, the Q3 rotation must
cover the exporter's Secret as well; under E or F the exporter holds no cluster password and
drops out of the Q4 list of env-pinned consumers.

### Password rotation - independent of the open questions

The manual path stays in every outcome.

1. **XS:** correct ADR 0016: D13 (`:185-186`) "if it has no failover target" becomes "at any
   replica count", with the docker measurement recorded in the ADR; Context `:30` becomes "at
   every container start; for Sentinel fixed per pod by the init-written config". Mark the old
   clauses superseded in place, add a dated Status amendment.
2. **XS:** [`rotation-and-change-propagation.md`](../security/rotation-and-change-propagation.md):
   `:28` as in item 1; `:23` `:3005` → `:3006`; the "not measured" labels at `:42-46` and
   [`authentication.md:43`](../operations/authentication.md) point at the ADR's measurement.
3. **S:** replace the lossy steps in [`authentication.md#changing-the-password`](../operations/authentication.md#changing-the-password)
   (`:63-107`) and ADR 0016 D13 with the lossless runbook: `ACL SETUSER default >new` on every
   data pod and Sentinel; `CONFIG SET masterauth new` on every data pod; `SENTINEL SET <monitor>
   auth-pass new` and, with Sentinel auth on, `SENTINEL CONFIG SET sentinel-pass new` on every
   Sentinel; then change the Secret; then replace pods, replicas first, and restart the observer.
   Document: a pod created or restarted before the Secret change must be treated again; use
   `REDISCLI_AUTH`, not `-a`; revoking the old password means replacing every pod; the M1b
   rescue; the same runbook before a reference change (`:109-116`) makes that roll lossless.
   Correct `:51-52`: the replacement becomes Ready because the probe passes on any reply (M5) and
   its sidecar authenticates with the value it started with.
4. **XS:** keep the value out of the `SentinelSet` error ([`client.go:285`](../../internal/valkeyclient/client.go)),
   at least for `auth-pass`; invert [`exec_test.go:480-484`](../../internal/valkeyclient/exec_test.go)
   to assert the password is absent. Must land before any caller logs it.
5. **S:** report an authentication failure with a reason of its own instead of `no master found`
   (`checker.go:193-197`); check the status reading of [ADR 0002](../adr/0002-surface-a-blocked-reconcile-on-the-cr.md) first.

Test: `git grep -n "no failover target\|once, at pod start" -- docs ':!docs/tickets'` finds each
hit struck with its correction.

### Exporter user - under Q1 = A

ADR 0016 records the refusal and its reason as a residual risk; H-6 is rewritten as accepted. No
code.

### Exporter user - under Q1 = D, E or F

1. CRD field under `spec.metrics` (Secret reference under D, boolean under E, none under F),
   `make generate-all`; its docs name the lost series and the `extraArgs` incompatibility.
2. `valkey` wrapper (`statefulset.go:826–832`): declare the exporter user with the Q2 command
   set. On writable-config topologies add a **rewrite guard**: strip only the exporter's own
   `user` line from `/etc/valkey-active` before `exec` (never `user default`), or write the user
   line into the writable config instead of the command line. Standalone pods keep the
   command-line form. Rendered only while the option is on, so the upgrade rolls nothing.
3. Exporter env (`statefulset.go:1071–1088`): `REDIS_USER`, `REDIS_EXPORTER_CONFIG_COMMAND=-`,
   and `REDIS_PASSWORD` from the new Secret (D) or `REDIS_PASSWORD_FILE` with **no**
   `REDIS_PASSWORD` (E, F).
4. E and F: an init step on `spec.image` writes a per-pod password
   (`head -c 32 /dev/urandom | sha1sum | cut -d' ' -f1`) and the password-file JSON, keyed
   byte-identical to the rendered `REDIS_ADDR`, into a Memory emptyDir mounted into the init
   step, `valkey` and the exporter. `valkey` reads it with `read -r`, or `cat` is added to
   `RequiredImageTools`.
5. Unit tests: pod template unchanged while the field is unset; under E and F no
   `REDIS_PASSWORD` in the exporter env; password-file key equals `REDIS_ADDR` for both schemes.
6. e2e on both pinned lines with the option on, with a new helper that reads `/metrics`: as the
   exporter user `ACL DRYRUN` refuses `CONFIG GET masterauth`, `CONFIG GET requirepass`,
   `FLUSHALL`, `REPLICAOF`, `CONFIG SET`, `GET`, and under Q2 = 2b `SLOWLOG GET`; `/metrics`
   reports `redis_up 1`; after a scrape `ACL LOG` is empty (2a) or holds only the `slowlog|get`
   entry (2b); a Sentinel failover followed by a restart of the `valkey` container in the same pod
   brings the pod back; negative control: the default user is still allowed `FLUSHALL`
   ([ADR 0017](../adr/0017-test-and-ci-policy.md) D11).
7. Re-measure the command set on every exporter pin bump (T45's review path).

### Password rotation - under Q3 = C (with Q4 = C-b and Q5 = (a))

1. The previous-key CRD field (`make generate-all`, README CRD reference) and a
   `conditionRegistry` row ([ADR 0027](../adr/0027-conditions-are-levels-edges-or-history.md)).
2. Typed client wrappers (thin, like `SentinelSet`) for `ACL SETUSER`, `CONFIG SET masterauth`,
   `SENTINEL CONFIG SET sentinel-pass`, errors without the password.
3. Try-both authentication (current key, fallback to previous on `WRONGPASS`/`NOAUTH`) at every
   `r.newValkeyClient` site and in `readAuthPassword`.
4. The ensure-both level, re-measured every pass and bounded ([ADR 0010](../adr/0010-every-rolling-update-wait-is-bounded.md)),
   on pods proven ours ([ADR 0020](../adr/0020-write-only-what-the-operator-owns.md)); then the
   rotation record, the closing roll, the single-pod deferral and the withdrawal (Q4).
5. e2e on both pinned lines: write keys, rotate with the previous key; afterwards every data and
   Sentinel pod accepts only the new password, the keys survive on a non-persistent cluster,
   writes replicate, role labels follow a failover, the observer stays Ready, the exporter reports
   up. Revert checks: without ensure-both the non-persistent key count fails (and
   `RollingUpdatePaused` appears); without the closing roll and withdrawal the old password is
   still accepted.

## Open questions

### Q1: Does the exporter get a Valkey user of its own, and where does its credential come from? (exporter user)

Today the exporter is the default user with `+@all`. A least-privilege user is measured to work
on both lines. Every doing-option rolls the affected data tier once, lossless except for a single
non-persistent pod, and drops the 12 `redis_config_*` series.

- **A - refuse and record.** No code; ADR 0016 and H-6 record the accepted risk. The exporter,
  whose pin keeps moving and whose next versions add commands, keeps `FLUSHALL`, `REPLICAOF`,
  `CONFIG SET` and the password.
- **D - user from a user-provided Secret.** Opt-in field referencing a second Secret, inert
  without `spec.auth`; keeps ADR 0016 D1 literally intact. Every CR author creates, encrypts for
  GitOps and rotates a second Secret per cluster; a changed value reaches no running pod (the
  rotation part, again); a missing key stops the `valkey` container with
  `CreateContainerConfigError`.
- **E - password generated in the pod (recommended).** Opt-in boolean, inert without
  `spec.auth`; an init step writes a per-pod password into a Memory emptyDir. Never in etcd, Git,
  the operator's Secret cache or status, dies with the pod, needs no rotation. D1's rule (the
  operator never generates a Secret) holds; its rationale sentence "never generating a
  credential" needs a carve-out for an in-pod, never-persisted credential.
- **F - E for every auth- and metrics-enabled cluster, no toggle.** Rolls every such data tier at
  the operator upgrade (a single non-persistent pod loses its data), drops the series fleet-wide
  and silently breaks every `extraArgs` user of `--check-keys`, `--check-single-keys`,
  `--count-keys` or `--export-client-list`, which would need an opt-out.

E narrows as far as D with one boolean, no rotation gap and no new `secretKeyRef` failure mode;
none of D1's reasons (rotate, leak into status, orphan) applies to an in-pod credential.

**Answer:** _open_

### Q2: Does the exporter user keep `SLOWLOG GET`? (exporter user, only if Q1 is D, E or F)

The exporter uses `SLOWLOG GET` only for the id and duration of the newest entry, but the grant
returns the full arguments of every logged command (default threshold 10 ms), values included, to
a user refused `GET`.

- **2a - keep `+slowlog|get`.** Both slowlog series stay and `ACL LOG` stays empty; a compromised
  exporter reads key names and values of slow commands.
- **2b - drop `+slowlog|get` (recommended).** `redis_slowlog_last_id` and
  `redis_last_slow_execution_duration_seconds` go; `redis_slowlog_length` and `redis_up 1` stay;
  one grouped `ACL LOG` entry per pod stands.

2b makes `resetkeys` mean something and the exporter user reads no data at all; the grouped entry
keeps `ACL LOG` readable, and no shipped alert or doc uses either series.

**Answer:** _open_

### Q3: Does the operator carry a password change to the running pods itself? (password rotation)

Valkey and Sentinel can accept a second password and switch `masterauth`, `auth-pass` and
`sentinel-pass` at runtime (M2-M4, M6). C reopens ADR 0016 D12/D13; the runbook ships either way.

- **M - stays manual with the lossless runbook.** No code. Every rotation is three to five
  `kubectl exec` commands per pod; a pod created or restarted inside the window must be noticed
  and treated by hand; the owner's wish is not met.
- **C - operator-driven runtime rotation (recommended).** The user puts the previous password next
  to the new one (Q5); while it is present the operator tries both passwords when it connects and
  ensures on every pod, per pass, that both are accepted, `masterauth` and `auth-pass` are new and
  `sentinel-pass` is set; withdrawal is Q4. Nothing happens without the previous key, so an upgrade
  changes nothing. No `REPLICAOF` or delete is added. Cost L; a bug can lock the operator and
  sidecars out, which is why every step is idempotent and re-measured.

C is the only option that meets the owner's wish, every Valkey behaviour it relies on is
measured, and its per-pass level repairs a pod created or restarted inside the window.

**Answer:** _open_

### Q4: When does a pod stop accepting the old password? (password rotation, only if Q3 = C)

The sidecar, drain handler, observer and exporter (the exporter only while it uses the cluster
password, so not under Q1 = E or F) hold the password from their start and cannot be told a new
one; a replaced pod or restarted container accepts only the value then in the Secret. Withdrawing
the old password while such a process lives cuts it off; the exec probes do not matter (M5).

- **C-a - never withdraw; report the pods still accepting the old one.** Cost S. A leaked password
  stays valid on unreplaced pods, and every pod replaced later is unreachable for the observer and
  the old sidecars: a permanent split.
- **C-b - close with an operator-driven failover-aware roll, then withdraw (recommended).** When
  ensure-both first holds, the operator stamps a record (for example the Secret's
  `resourceVersion`, captured once and inherited) into the hashed pod spec of both tiers and the
  observer template ([ADR 0031](../adr/0031-a-record-the-operator-trusts-lives-in-pod-spec.md)),
  absent until the first rotation. During the roll ensure-both keeps adding the old password to
  replacements, so an unplanned master delete still finds a reachable replica for the drain
  promotion. When all carry the record it runs `ACL SETUSER default <old` everywhere (safe per M3,
  M4). A single non-persistent pod is deferred and reported. Cost M on top of C; the previous key
  must stay until completion.

C-b is the only variant that ends with no pod accepting the old password without a fleet-wide
roll at release, and it reuses the existing roll.

**Answer:** _open_

### Q5: Where does the operator get the previous password from? (password rotation, only if Q3 = C)

ADR 0016 D1 forbids an operator-held copy, and adding the new password to an old pod needs AUTH
with the old value (M2), so the user must supply it.

- **(a) - an optional CRD field naming a key in the same auth Secret (recommended)**, for example
  `spec.auth.previousPasswordKey`, unset by default; the user removes the key on completion. The
  existing watch covers it.
- **(b) - a second Secret reference**, for example `spec.auth.previousSecretName`. Fits one Secret
  per credential; the two writes to two objects reopen today's split between them.
- **(c) - the previous Secret is the one the running pods still reference.** Rotation is a change
  of `spec.auth.secretName`/`secretPasswordKey`; the operator reads each pod's `secretKeyRef` and
  ensures both before the roll. No new field, makes the reference-change roll lossless, but cannot
  see an in-place value change, and under Flux prune a hash-suffixed old Secret may be gone first.

(a) delivers both values in one write to one object and covers in-place rotation (`kubectl edit`,
a refreshed ExternalSecret), which (c) cannot see; the runbook covers the reference-change path.

**Answer:** _open_

## Not verified

- Settled by the exporter e2e: E's init step on a real rootless pod (`readOnlyRootFilesystem`,
  `fsGroup` 999, Memory emptyDir, user namespace); D or E under TLS; the rewrite guard's
  config-file form on 8.1.9 and on a Sentinel pod after a failover; init-step consistency across
  restarts (by reading, both write variants hold).
- The single `ACL LOG` entry under 2b over hours: by reading, grouped for scrape intervals below
  60 s; measured over three scrapes only.
- Exporter images other than redis_exporter: every option assumes a compatible
  `spec.metrics.image`, as today's wiring already does.
- No rotation has run end to end on a cluster (manual, runbook or C); settled by the chosen
  option's e2e or a Kind run of the runbook. The reference-change roll's pause and kubelet's
  re-resolution on container restart are read (code, upstream `release-1.36`), not measured.
- The production facts behind Q3 and Q4 (Flux with prune, Chaos Mesh killing a pod every 5 min in
  `database-examples`) come from the owner's run context, not this repository.
- Whether External Secrets Operator can map a previous remote version to a second key, and the
  order of kustomize-controller's prune against a CR change (both bear on Q5).
- Whether a one- or two-Sentinel tier keeps a failover majority while its Sentinels are on
  different passwords.

## Related

- [T45](045-release-pipeline-image-pins-and-renovate-coverage.md) - under Q1 = D, E or F an
  exporter pin bump can drop series silently; the bump needs a command-set re-measurement.
- [T35](035-who-the-master-is-after-a-restart-or-failover.md) - same `CONFIG REWRITE` of
  `/etc/valkey-active`; a fix re-rendering it changes the rewrite guard, design together.
- [T23](023-stalled-or-cycling-rolling-update-and-the-sentinel-reset.md) - decides `resetSentinelState`'s
  `RESET` fallback and gate, which changes the failover-retry behaviour above.
- [T35](035-who-the-master-is-after-a-restart-or-failover.md) - the probe exit-code finding (M5); its
  option B (`-e`) would break Q4's premise that probes do not matter.
- [T40](040-tracked-text-cites-tickets-and-states-what-the-code-contradicts.md) - item (e) corrects the
  password's `/proc` carrier in ADR 0016 and names the cleartext `/etc/valkey-active`.
