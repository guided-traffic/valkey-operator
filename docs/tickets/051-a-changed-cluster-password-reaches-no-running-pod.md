---
id: T51
title: a changed cluster password reaches no running pod
state: analysed       # was filed; every former "Not verified" item is measured in docker on both pinned lines or read in upstream kubelet source (2026-09-27), and the options are restated; only an end-to-end rotation on a cluster is still unrun
severity: medium      # rotating means replacing every pod by hand; a non-persistent cluster ~~without a failover target~~ loses its data on it (corrected 2026-09-27: at any replica count; the premise, a replica on the new password cannot sync from a master on the old, measured in docker on 9.1.1 and 8.1.9 on 2026-09-27). Not higher: the loss needs a non-persistent cluster and the user-run procedure, and the decision-free runbook rewrite removes it
security: hardening
threat: "would additionally cover a leaked cluster password: today revoking it means changing the Secret and replacing every pod by hand; the old password stays valid on every pod not yet replaced, connections already authenticated with it survive until their pod is replaced, and until the last pod is replaced the operator authenticates with the new password against pods that accept only the old one, so its health checks and any REPLICAOF it sends fail"
urgency: now          # rule 1 since 2026-09-27 (re-verification at 84a39c2): ADR 0016 D13 (docs/adr/0016-authentication-and-tls-posture.md:185-186) still says a non-persistent cluster loses data only "if it has no failover target", and its load-bearing premise is measured false; recompute to next (rule 3) once that XS correction lands. Was next (rule 3), which was applied without testing rule 1
effort: L             # option C with D2 C-b and D3 (a); the decision-free items are XS to S
blocked-by: decision  # D1-D3 in Options (ADR 0016 D12/D13, ADR 0030 D11 stays); the decision-free items in the Work list are not blocked
filed-from: T31, section "Further security measures — not in this change, each open" (archive/031)
opened: 2026-09-27
decided:              # not recorded - no decision yet
done:                 # not recorded - not done
---

Filed on 2026-09-27 from the fourth row of the table
["Further security measures — not in this change, each open"](archive/031-generated-pods-run-as-root.md#further-security-measures--not-in-this-change-each-open)
in the archived ticket 031, which is `done` and tracks none of its rows. The operator-facing
statement is [the password rotation gap](../security/rotation-and-change-propagation.md#the-password-rotation-gap)
and gap [H-24](../security/rotation-and-change-propagation.md#h-24).
*(2026-09-27: the source row is verified at
[`archive/031-generated-pods-run-as-root.md:681`](archive/031-generated-pods-run-as-root.md),
"Password rotation | open by design, must not copy the TLS fingerprint mechanism | ADR 0030
D11".)*

## Fact

**Verified** (read 2026-09-27; locations re-read at `84a39c2`):

- The auth Secret is watched: `findValkeyForSecret`
  ([`valkey_controller.go:3006`](../../internal/controller/valkey_controller.go)) enqueues
  the Valkey resource when it changes. *(2026-09-27: the watch is registered at
  [`valkey_controller.go:2997-3000`](../../internal/controller/valkey_controller.go), and
  `secretConcernsValkey`, [`valkey_controller.go:3052-3060`](../../internal/controller/valkey_controller.go),
  matches the auth Secret by name at `:3053-3055`.)*
- The password reaches the pods as `env.valueFrom.secretKeyRef`, resolved when the container
  starts, and the pod-spec hash covers the reference, not the value
  ([ADR 0016](../adr/0016-authentication-and-tls-posture.md) D12). Nothing rolls.
  *(2026-09-27: the reference sites are listed in
  [ticket 050](050-every-component-authenticates-with-the-one-cluster-password.md), Fact; they
  still hold at `84a39c2`. The comparison of a `SecretKeyRef` looks only at its name and key,
  [`statefulset.go:1471-1482`](../../internal/builder/statefulset.go).)*
- [ADR 0016](../adr/0016-authentication-and-tls-posture.md) D13 makes rotation a documented
  manual procedure: change the Secret, roll the pods yourself, replicas first, master last. Its
  steps 2 and 3 were derived by reading, not reproduced against a cluster.
  *(2026-09-27: its data-loss premise is now measured in docker, below, and D13 still states
  the refuted "if it has no failover target" at
  [`0016-authentication-and-tls-posture.md:185-186`](../adr/0016-authentication-and-tls-posture.md);
  the ADR has not changed since `4a7543e`.)*
- [ADR 0030](../adr/0030-rotating-certificates-rotate-the-instances-that-cannot-reload-them.md)
  D11: a published digest of the password is a brute-forceable oracle at any digest strength,
  and the gap "must not be closed by copying this mechanism"
  ([`0030-...md:291-306`](../adr/0030-rotating-certificates-rotate-the-instances-that-cannot-reload-them.md)).
- The owner's wish, in [`.github/idea.md:7`](../../.github/idea.md): after the password Secret
  changes, the instances take the new password without losing their state, also without a
  persistent volume.

*Added 2026-09-27 (enrichment, at `4a7543e`):*

- **The operator switches to the new password at once.** `readValkeyPassword`
  ([`valkey_controller.go:177-190`](../../internal/controller/valkey_controller.go)) reads the
  Secret on every call and returns `""` on a read error. The health checker does the same
  (`readAuthPassword`, [`checker.go:75-87`](../../internal/health/checker.go)): once per
  `CheckCluster` pass (`:97`), per call in the other checks (`:145`, `:159`, `:309`).
- **`masterauth` is the same value as `requirepass`.** `valkey-server` gets both from
  `$VALKEY_PASSWORD` ([`statefulset.go:830`](../../internal/builder/statefulset.go)), so no
  pod started with the new password can replicate from one still running with the old.
  Sentinel writes `requirepass` and `sentinel auth-pass` into its config at init
  ([`sentinel.go:176-190`](../../internal/builder/sentinel.go), `sed` at
  [`sentinel.go:715`](../../internal/builder/sentinel.go)); with `spec.sentinel.disableAuth`
  only `auth-pass` is written. The generated config sets no `sentinel-pass`.
- ~~**The Sentinel half of option C already has plumbing.**~~ *(corrected 2026-09-27 at
  84a39c2: only `SENTINEL SET <monitor> auth-pass` has a client method. A runtime rotation of
  the Sentinel tier also needs `ACL SETUSER default >new` on each Sentinel, because Sentinel
  refuses `CONFIG SET`, and `SENTINEL CONFIG SET sentinel-pass <new>` for the Sentinel-to-Sentinel
  links; neither has plumbing. Measured, M6 below.)* The client has `SentinelSet`
  ([`client.go:282`](../../internal/valkeyclient/client.go)). The operator already sends
  `SENTINEL SET <monitor> auth-pass <current Secret value>` at runtime, in `resetSentinelState`
  ([`rolling_update.go:3617`](../../internal/controller/rolling_update.go); password read at
  `:3647`, set at `:3692`). That is a best-effort step of the rolling-update failover retry
  (callers at [`rolling_update.go:958`](../../internal/controller/rolling_update.go), `:1007`,
  `:1049`, `:3161`, `:3308`). ~~The client has no `CONFIG SET`, `ACL` or exported generic
  command method (its methods, `client.go` lines 197–429; first written as "no ... generic
  command method"), so the data half of C needs new client methods.~~ *(corrected 2026-09-27
  at 84a39c2: `ExecMulti` ([`client.go:333`](../../internal/valkeyclient/client.go)) and
  `ExecGet` ([`client.go:363`](../../internal/valkeyclient/client.go)) are exported and send
  arbitrary command vectors on one authenticated connection; the observer already uses them
  ([`checks.go:112`](../../internal/observer/checks.go), `:121`, `:145`). Their error text
  carries only `cmd[0]` (`client.go:350`, `:354`, `:381`, `:385`), so an `ACL SETUSER` sent
  through them does not echo the password, and an error reply surfaces as an error
  (`client.go:479-481`). `ExecMulti`'s doc comment says "the last non-OK error is returned",
  but the function returns at the first error reply. Typed wrappers remain preferable.)*
  *(precised 2026-09-27, review: the unexported `exec(args ...string)` at
  [`client.go:429`](../../internal/valkeyclient/client.go) is generic, so each new method is a
  thin wrapper, as `SentinelSet` is at `:283`.)*
- No auth failure is handled as such: a grep for `WRONGPASS` and `NOAUTH` over `internal` and
  `cmd`, outside tests, finds only the init script's guard
  ([`statefulset.go:300-302`](../../internal/builder/statefulset.go), plus the comment at
  `:262`). Re-run 2026-09-27 at `84a39c2`, same result.
- The operations and security pages were corrected on 2026-09-27: without persistence, the
  manual procedure loses the dataset at any replica count
  ([`rotation-and-change-propagation.md`](../security/rotation-and-change-propagation.md)
  lines 42–46, [`authentication.md`](../operations/authentication.md#changing-the-password)).
  *(2026-09-27: both still label that statement "read from the code, not measured"
  (rotation page `:42-46`, [`authentication.md:43`](../operations/authentication.md)), and
  ADR 0016 D13 was not corrected with them.)*

*Added 2026-09-27 (re-verification at `84a39c2`), read in code:*

- **The Secret-change window fails closed.** On a Sentinel cluster `probeMasterRole` swallows
  the AUTH error and returns nil ([`checker.go:193-197`](../../internal/health/checker.go)),
  `findMaster` reports `no master found among N pods`
  ([`checker.go:242-243`](../../internal/health/checker.go)), and the status becomes phase
  `Error`, `Cluster health check failed: no master found among N pods`,
  `Ready=False/ClusterHealthCheckFailed`
  ([`valkey_controller.go:2464-2474`](../../internal/controller/valkey_controller.go)) — the
  message does **not** name authentication, so an auth split reads like a lost master. Without
  Sentinel, `verifyValkeyConnectivity` fails with the client's `AUTH failed on <addr>: valkey
  error: WRONGPASS ...` text (`client.go:410-425`) in `Instance unreachable: ...` and
  `Ready=False/ConnectivityCheckFailed`
  ([`valkey_controller.go:2237-2247`](../../internal/controller/valkey_controller.go)). The
  no-master recovery counts every pod that errors as unreachable and does not promote
  ([`valkey_controller.go:2866-2871`](../../internal/controller/valkey_controller.go),
  `:2905`); the steady-state resolver acts only on a pod that confirms `role:master`
  ([`steady_state_master.go:106-130`](../../internal/controller/steady_state_master.go)). No
  `REPLICAOF` is sent and no Event is emitted.
- **The password is also pinned in processes that read it once.** The sidecar
  ([`cmd/sidecar/sidecar.go:75`](../../cmd/sidecar/sidecar.go)) and the observer
  ([`cmd/observer/observer.go:95`](../../cmd/observer/observer.go)) read `VALKEY_PASSWORD` at
  process start; the exporter gets `REDIS_PASSWORD` from env
  ([`statefulset.go:1075-1087`](../../internal/builder/statefulset.go)); the drain handler's
  client factory takes `cfg.Password` at start
  ([`drain.go:462-476`](../../internal/sidecar/drain.go)) and dials every peer with it
  (`findSyncedReplica`, [`drain.go:244-292`](../../internal/sidecar/drain.go)). On a
  `DetectRole` error the labeler returns without patching
  ([`labeler.go:124-127`](../../internal/sidecar/labeler.go)), so a sidecar that cannot
  authenticate to its own Valkey freezes the pod's `instanceRole` label (only the sidecar
  writes it); the drain handler exits at once on the same error
  ([`drain.go:106-110`](../../internal/sidecar/drain.go)).
- **A replaced data pod on a Sentinel cluster** asks the Sentinels for the master with its own
  password in its init container; a Sentinel still on the old password answers with an auth
  error (`WRONGPASS`/`NOAUTH`, M5), the guard skips it, and after up to 30 s (`MAX_WAIT=30`)
  the init falls back to the known-master ConfigMap
  ([`statefulset.go:291-327`](../../internal/builder/statefulset.go)).
- **A failover retry during the manual procedure does not switch every Sentinel.**
  `resetSentinelState` dials each Sentinel with `sentinelPassword`, the new value whenever
  Sentinel auth is on ([`valkey_controller.go:194-199`](../../internal/controller/valkey_controller.go),
  [`rolling_update.go:3646`](../../internal/controller/rolling_update.go)); a Sentinel still on
  the old `requirepass` fails `SENTINEL REMOVE`, fails the `RESET` fallback on the same AUTH
  ([`rolling_update.go:3663-3673`](../../internal/controller/rolling_update.go)) and is
  skipped. Only Sentinels already on the new password — which carry `auth-pass` new from their
  init anyway — accept. With `spec.sentinel.disableAuth: true` the reset reaches every Sentinel
  and switches `auth-pass` to the new value, cutting the not-yet-replaced Sentinels off data
  pods still on the old password. Dormant unless a rolling update's failover retry is in
  flight: a Secret change alone starts no roll.
- **`SentinelSet` puts the option value into its error**
  ([`client.go:285`](../../internal/valkeyclient/client.go)), so for `auth-pass` the password,
  and [`exec_test.go:480-484`](../../internal/valkeyclient/exec_test.go) pins that text.
  Dormant: every production caller discards the error
  ([`rolling_update.go:3682-3686`](../../internal/controller/rolling_update.go), `:3692`). It
  is a latent breach of ADR 0016 D3 ("no credential is written into ... an Event, or a log",
  [`0016-...md:56`](../adr/0016-authentication-and-tls-posture.md)) the day a caller logs it.
- **Changing `spec.auth.secretName` or `secretPasswordKey` is the other rotation path, and it
  does roll**: the reference is part of the hashed pod spec
  ([`statefulset.go:874-887`](../../internal/builder/statefulset.go),
  [`observer.go:249-258`](../../internal/builder/observer.go)). With a different password in
  the new Secret the first replaced replica cannot sync (M1), so the roll pauses after
  `syncTimeout` (`RollingUpdatePaused`, [`authentication.md:109-116`](../operations/authentication.md),
  read, not measured), and a single non-persistent pod is replaced at once and loses its data:
  a rootless pod whose drift is not sidecar-only is not deferred
  ([`pod_security_migration.go:128-150`](../../internal/controller/pod_security_migration.go),
  [`rolling_update.go:3786-3791`](../../internal/controller/rolling_update.go); read, not
  measured). During such a roll `collectPodStates` falls back to the sidecar's master label
  when `INFO` fails ([`rolling_update.go:1947-1962`](../../internal/controller/rolling_update.go)),
  so the roll reaches the synced-check pause rather than failing earlier.
- ADR 0016's Context states "Kubernetes resolves `env.valueFrom.secretKeyRef` exactly once, at
  pod start" ([`0016-...md:30`](../adr/0016-authentication-and-tls-posture.md)), and the
  rotation page copies it ("once, at pod start",
  [`rotation-and-change-propagation.md:28`](../security/rotation-and-change-propagation.md)).
  Per the kubelet source below the ref is resolved at every container start. For Sentinel the
  effective password is still fixed per pod, because the init writes it once into the
  `emptyDir` config. Read-false, not measured-false. The rotation page also still cites
  `valkey_controller.go:3005` (`:23`); the function is at `:3006`.
- No e2e changes a password: `spec.auth` appears in e2e only in
  [`test/e2e/pod_security_test.go:133`](../../test/e2e/pod_security_test.go) (and a dummy
  patch in `migrate_e2e_test.go:128`).
- ADR 0016 D1 ([`0016-...md:36-42`](../adr/0016-authentication-and-tls-posture.md), the
  "operator never generates one" sentence at `:38-39`) rules out an operator-owned copy of the
  password. Capturing the old value from controller-runtime's `UpdateEvent` would be such a
  copy, and a Secret changed while the operator is down is never seen as an old/new pair, so
  the previous value has to come from the user.

*Added 2026-09-27, read in upstream Kubernetes source (not measured on a cluster):*

- **kubelet resolves a `secretKeyRef` at every container start, restarts inside the same pod
  included.** `startContainer` calls `generateContainerConfig` for every start
  ([`kuberuntime_container.go:254`](https://github.com/kubernetes/kubernetes/blob/release-1.36/pkg/kubelet/kuberuntime/kuberuntime_container.go),
  release-1.36), which calls `GenerateRunContainerOptions` (`:343-344`) →
  `makeEnvironmentVariables`
  ([`kubelet_pods.go:652`](https://github.com/kubernetes/kubernetes/blob/release-1.36/pkg/kubelet/kubelet_pods.go))
  → `kl.secretManager.GetSecret` (`:897-907`). The docs agree
  ([distribute-credentials-secure.md:248-250](https://github.com/kubernetes/website/blob/main/content/en/docs/tasks/inject-data-application/distribute-credentials-secure.md):
  "a Secret update will not be seen by the container unless it is restarted"). Consequence: a
  `valkey` container restarted in place (liveness, OOM) after the Secret change comes back on
  the new password while its own sidecar and exporter keep the old one.

*Measured 2026-09-27 in docker on `valkey/valkey:9.1.1` and `valkey/valkey:8.1.9` (the two
pinned lines, [`test/testimages/images.go:40`](../../test/testimages/images.go), `:45`), by
the auditor; M1 and M5 were re-run by both reviewers, M2, M3 (with the `holder` check of M4)
and M6 by one of them, and M1b was measured by that reviewer only; identical on both lines
unless stated. Containers named `vko-verify-051*` on a private docker network,
every container and network removed afterwards (`docker ps -a --filter name=vko-verify-051`
and `docker network ls --filter name=vko-verify-051` empty):*

- **M1 — a replica on the new password against a master on the old.** Master
  `valkey-server --save "" --appendonly no --requirepass old --masterauth old`, replica the same
  with `new`/`new`; `valkey-cli -a old set k1 v1` on the master, `valkey-cli -a new replicaof
  <master> 6379` on the replica, then `info replication`, `dbsize`, `docker logs`. Result:
  `role:slave`, `master_link_status:down`, `master_sync_in_progress:0`, `DBSIZE 0`, and every
  second `Unable to AUTH to PRIMARY: -WRONGPASS invalid username-password pair or user is
  disabled.` This is the premise of "at any replica count".
- **M1b — rescue.** In the M1 state, `acl setuser default '>new'` on the master
  (authenticated with `old`): the stuck replica went to `master_link_status:up`, `DBSIZE 1`
  within 8 s. A cluster caught mid-way through today's procedure is recoverable without loss
  while its old-password master still runs.
- **M2 — two passwords for the default user.** `valkey-cli -a old acl setuser default '>new'`;
  then `-a old ping` and `-a new ping` both `PONG`, `acl getuser default` lists two SHA-256
  hashes. `config set requirepass new2` afterwards: `old` and `new` get `WRONGPASS`/`NOAUTH
  Authentication required.`, only `new2` answers `PONG`.
- **M3 — an established link across the switch.** Master and replica on `old`/`old`, link up;
  `acl setuser default '>new'` on both; `config set masterauth new` on the replica; `acl setuser
  default '<old'` on the master. The link stays `master_link_status:up` and a write `k2`
  replicates. After `client kill type replica` it is up again within 1 s ("Successful partial
  resynchronization with primary"), and `k3` replicates. Control with a stale
  `masterauth old`: `master_link_status:down`, `Unable to AUTH to PRIMARY: -WRONGPASS`.
  `masterauth` therefore applies at the next reconnect.
- **M4 — authenticated clients survive password removal.** A raw TCP client authenticated with
  `old` and named `holder` stays connected (`client list | grep -c name=holder` = 1) after
  `acl setuser default '>new' '<old'` and after `config set requirepass new2`. Removal stops
  new dials only.
- **M5 — `valkey-cli` exit codes (what the exec probes see).** `valkey-cli --no-auth-warning
  -a wrong ping; echo $?` → `0` (prints `AUTH failed: WRONGPASS ...` and `NOAUTH
  Authentication required.`), also via `sh -c`; right password → `0`; an error reply
  (`nosuchcommand`) → `0`; nothing listening (`-p 6390`) → `1`. The probe
  ([`statefulset.go:1515-1545`](../../internal/builder/statefulset.go), its auth branch
  `:1516-1531`; [`sentinel.go:420-459`](../../internal/builder/sentinel.go), its auth branch
  `:429-446`, ~~`sentinel.go:420-446`~~ *(corrected 2026-09-27, final pass: that range is the
  function head and the auth branch only; both functions re-read at `84a39c2`, as T76 cites the
  Sentinel one)*) therefore passes on any server reply, whether or not the password matches.
- **M6 — Sentinel at runtime.** Master and replica on `old`, two Sentinels from a config of
  `port 26379`, `sentinel resolve-hostnames yes`, `sentinel monitor mymaster <M> 6379 2`,
  `sentinel down-after-milliseconds mymaster 5000`, `requirepass old`, `sentinel auth-pass
  mymaster old`. Results: `config set requirepass new` → `ERR unknown command 'config'`;
  `acl setuser default '>new'` → OK, `old` and `new` both `PONG`; `sentinel config set
  sentinel-pass new` → OK. With `>new` on the data pods, `masterauth new` on the replica,
  `sentinel set mymaster auth-pass new` on both Sentinels, `<old` on the data pods and `client
  kill type normal`, after 12 s the master stays `flags master`, `num-slaves 1`. Control
  `auth-pass old` on one Sentinel: `flags s_down,master,disconnected` within 12 s, so
  `SENTINEL SET auth-pass` takes effect at once. With `old` removed from both Sentinels and no
  `sentinel-pass`, after `client kill skipme yes` and 12 s, S1 sees S2 as `flags
  s_down,sentinel` (last-ok-ping-reply 12989 ms on 9.1.1, 12735 on 8.1.9; reviewer re-run
  14130 and 14988); after `sentinel config set sentinel-pass new` on both, `flags sentinel`
  (65 / 956 ms; re-run 1016 / 942). The rewritten config holds `requirepass "old"`,
  `sentinel auth-pass mymaster "new"`, `user default on sanitize-payload #<sha256> ~* &*
  +@all` and `sentinel sentinel-pass "new"`; a Sentinel started on a copy of it (port 26380)
  accepts only `new`, so on reload the `user` line wins over the stale `requirepass`.
  Sentinel auth-pass immediacy was measured by the auditor only.
- **M7 — idempotence.** `acl setuser default '>new'` twice still lists two hashes;
  `config get masterauth` reads back `old`; `config rewrite` answers `ERR The server is
  running without a config file` (the spike ran without one; the data pods run from a
  read-only ConfigMap file, so runtime changes are not persisted there).

**Not verified:**

- ~~Valkey's multi-password semantics on both pinned lines: that `ACL SETUSER default >new`
  keeps the old password valid, and that `CONFIG SET requirepass` replaces all of them.~~
  *(corrected 2026-09-27 at 84a39c2: measured, M2 and M7; both hold, and `>new` is
  idempotent.)*
- ~~Whether `CONFIG SET masterauth` reaches an established replication link or only the next
  reconnect, and Sentinel's runtime equivalents (`SENTINEL SET <master> auth-pass`,
  `requirepass` on Sentinel).~~ *(corrected 2026-09-27 at 84a39c2: measured, M3 and M6.
  `masterauth` applies at the next reconnect and the established link survives the removal of
  the old password on the master; Sentinel takes `auth-pass` via `SENTINEL SET` at once, its
  own password only via `ACL SETUSER default`, and its peer links need `sentinel-pass`.)*
- ~~Whether kubelet resolves a `secretKeyRef` again when a container restarts inside the same pod.~~
  *(corrected 2026-09-27 at 84a39c2: yes, at every container start, read in upstream source
  above; not measured on a cluster.)*
- ~~*(added 2026-09-27)* What a reconcile pass does when every pod answers `WRONGPASS`. Also
  whether a failover retry during the manual procedure really switches the Sentinels'
  `auth-pass` to the new password while the data pods still require the old one. The code
  reads that way (above), but it was not measured.~~ *(corrected 2026-09-27 at 84a39c2: both
  answered by reading, above. The pass fails closed. The failover retry switches every
  Sentinel only with `spec.sentinel.disableAuth: true`; with Sentinel auth on it reaches only
  the Sentinels already on the new password.)*

Still not verified, and what would settle it:

- **No rotation has run end to end on a cluster** — neither today's manual procedure, nor the
  rewritten lossless runbook, nor option C. The measurements above are two- to four-container
  docker simulations. The e2e of the chosen option settles it; for the runbook alone, a Kind run
  of it.
- The `RollingUpdatePaused` of the reference-change path and of option B is read, not measured.
- Kubelet re-resolution on restart is read in `release-1.36` source, not measured.
- Production facts used in the Options come from the owner's run context and memory, not from
  this repository: Valkey CRs applied by Flux with prune and server-side apply, production
  namespaces (gitlab, gpt, harbor, iam), a Chaos Mesh schedule killing one operator-managed pod
  every 5 min in `database-examples`.
- Whether External Secrets Operator can map a previous remote secret version to a second key of
  the same target Secret (bears on D3), and the ordering of kustomize-controller's prune
  against a CR change in the same apply (bears on D3 option d). Neither was checked.
- Whether a one- or two-Sentinel tier keeps a failover majority while its Sentinels are on
  different passwords. M6 ran two Sentinels in the monitoring role only; no failover across a
  password split was measured.

**Cross-ticket** (read 2026-09-27):

- [Ticket 050](050-every-component-authenticates-with-the-one-cluster-password.md): the
  default-user clients it lists (sidecar, observer, exporter, probes) are exactly the
  env-pinned consumers that stop a runtime withdrawal of the old password here (D2). An
  exporter user of its own, if T50 chooses one, needs the same rotation path. Two facts from
  here bear on T50 ~~and are for T50's own file~~ *(050 carries both since the consistency pass
  of 2026-09-27, in its Cross-ticket bullet on this ticket; checked in the sweep)*: `valkey-cli` exits 0 on `NOAUTH` (M5), so a
  probe user would authenticate nothing; and a Sentinel's own password governs its peer links
  unless `sentinel-pass` is set (M6), which bears on T50's `masteruser`/`auth-user` item.
- [Ticket 062](062-resetsentinelstate-falls-back-to-sentinel-reset.md) decides the fate of
  `resetSentinelState`'s `RESET` fallback (`rolling_update.go:3666-3673`). T51 does not treat
  that function as option C's plumbing; C needs its own `auth-pass`, `sentinel-pass` and ACL
  steps. (~~T62 cites the function at `:3616`; it is at `:3617` at `84a39c2`.~~ *(corrected
  2026-09-27, consistency pass: 062's re-verification of the same day cites `:3617`.)* 062's
  recommended D1 B deletes the calls at `:958` and `:1007` and gates the other three: the reset
  runs only after the named master answers `role:master` to the operator's client, which reads
  the new Secret value, so while the data pods still require the old password the gate's `INFO`
  fails and no Sentinel's `auth-pass` is switched - under B the `disableAuth: true` case of the
  Fact bullet above no longer happens; read in 062, not measured. 062's decision-free removal of
  the `RESET` fallback also changes what that bullet says a Sentinel on the old password does: it
  fails `SENTINEL REMOVE`, then the `MONITOR` on the same AUTH, and is skipped.)
- The exec probes passing on any server reply (M5), with what that means for a loading or busy
  data pod and the readiness decision it opens, is
  [ticket 076](076-the-exec-probes-pass-on-any-server-reply.md), which also carries the sibling
  notes to 012, 023 and 036. This ticket keeps M5 because D2 and Work list item 3 rest on it;
  T76's recommended option keeps an auth error counting as ready, so D2's premise holds under
  it, and its option B (`-e`) would break that premise.
- [Ticket 040](040-tracked-files-cite-work-items-instead-of-adrs.md) records that H-24 no
  longer carries a T-label and that its work is this ticket. `git grep -n "T51\|051-a-changed"
  -- ':!docs/tickets'` returns nothing at `84a39c2`.

## Impact

Every auth-enabled cluster, whenever its password changes. Between the Secret change and the
last manual pod restart the operator cannot authenticate to the pods that still run with the
old password, so its health checks fail and it cannot send `REPLICAOF`. A cluster without
persistence ~~and without a failover target~~ loses its data on the manual roll. *(corrected
2026-09-27, as on the rotation page: at any replica count, because `masterauth` equals
`requirepass` and no replacement can sync from a pod still on the old password.)*
*(2026-09-27: the premise is measured, M1, both lines.)*

*Added 2026-09-27 (re-verification at `84a39c2`):*

- **Status for the whole window:** phase `Error`, `Ready=False`. On a Sentinel cluster the
  message is `no master found among N pods` and names no authentication failure, so a user in
  the middle of a rotation cannot tell an auth split from a lost master (Fact).
- **Intra-pod split:** a `valkey` container restarted in place during or after the window
  comes back on the new password while its sidecar and exporter keep the old one; the labeler
  then freezes `instanceRole` (a later failover leaves `<name>-rw` on the old master), the
  ADR 0012 drain promotion fails, and the exporter reports the instance down, until the pod is
  replaced. The same happens after any Secret change today.
- **Sentinels on different passwords cannot authenticate to each other** unless `sentinel-pass`
  is set (M6); in a three-Sentinel tier one side of a two-way split always holds two of three
  votes (by arithmetic, not measured); smaller tiers are listed under Not verified.
- **The reference-change path rolls and still loses data**: pointing `spec.auth` at another
  Secret or key with a different password pauses the roll at the first replaced replica and
  loses the data of a single non-persistent pod (Fact).

The security side, per case: a leaked password stays valid on every pod not yet replaced, and
connections already authenticated with it survive until their pod is replaced (M4). This is
hardening, not a live weakening: ADR 0016 D13 documents the gap, and no guarantee the repo
gives is broken.

## Options

Three decisions, one after another; D2 and D3 exist only if D1 is C. ADR 0030 D11's refusal
of a content digest is not reopened by any option: a digest of the password on the pod
template is a brute-forceable oracle at any digest strength
([`0030-...md:291-306`](../adr/0030-rotating-certificates-rotate-the-instances-that-cannot-reload-them.md)),
and that refusal is already recorded there.

### D1 — does the operator carry a password change to the running pods itself?

**Mechanism today.** A change to the auth Secret enqueues a reconcile
([`valkey_controller.go:2997-3000`](../../internal/controller/valkey_controller.go),
`:3053-3055`). From then on the operator authenticates with the new value, because it reads
the Secret per call ([`valkey_controller.go:177-190`](../../internal/controller/valkey_controller.go),
[`checker.go:75-87`](../../internal/health/checker.go)). Every pod keeps the value its
containers started with: `valkey-server` takes `requirepass` and `masterauth` from
`$VALKEY_PASSWORD` ([`statefulset.go:830`](../../internal/builder/statefulset.go)), the
Sentinel init writes it into an `emptyDir` config ([`sentinel.go:176-190`](../../internal/builder/sentinel.go),
`:715`), and the sidecar, observer and exporter read the env once. Nothing rolls, because the
hash covers only the reference (ADR 0016 D12). Measured on both pinned lines: a pod started
with the new password cannot replicate from one still on the old (M1), so the documented
procedure (ADR 0016 D13) loses the dataset of every non-persistent cluster. Also measured:
Valkey and Sentinel accept a second password for the default user at runtime, `masterauth`,
`auth-pass` and `sentinel-pass` switch at runtime, and established links and authenticated
clients survive the removal of a password (M2–M4, M6).

**What the choice changes and what it does not.** It decides whether the operator drives that
runtime sequence itself, which reopens ADR 0016 D12 (a value change now reaches running pods)
and D13 (rotation stops being only a manual procedure). It does not change ADR 0030 D11, and
it does not change the manual path: the manual path stays for every cluster that does not use
the new mechanism and for every operator release before it, and its lossless rewrite is
decision-free work (Work list). A roll without a window in which both passwords are accepted
is not an option: that is what the reference-change path already does, and it pauses and
loses data (Fact).

- **M — rotation stays manual, with the measured lossless runbook.** ADR 0016 D13 stands as
  a rule; the runbook of the Work list is the whole answer: on every data pod and Sentinel
  `ACL SETUSER default >new`; on every data pod `CONFIG SET masterauth new`; on every Sentinel
  `SENTINEL SET <monitor> auth-pass new` and `SENTINEL CONFIG SET sentinel-pass new`; then
  change the Secret; then replace the pods, replicas first, and restart the observer.
  *Cost:* none beyond the runbook, which ships in every outcome. *Consequences:* every rotation
  is a `kubectl exec` runbook of three to five commands per pod on every cluster. A pod created
  or a `valkey` container restarted in place between the runbook and the Secret change starts
  with only the old password and loses the runtime changes (they are not persisted), so it must
  be treated again; after the Secret change the same holds the other way round. Pods replaced
  after the Secret change accept only the new password, so the sidecars and the observer of pods
  not yet replaced cannot reach them until those are replaced too (the D2 analysis applies to a
  manual roll as well). Revoking a leaked password still means replacing every pod. The owner's
  "automatically" is not met.
- **C — operator-driven runtime rotation, restated (recommended).** The user writes the
  previous password next to the new one in one Secret update (D3). While the previous key is
  present the operator runs a bounded, level-driven rotation
  ([ADR 0010](../adr/0010-every-rolling-update-wait-is-bounded.md)):
  1. **Try both when it connects.** Pods and containers started after the Secret update hold
     only the new password, pods not yet treated only the old one, so neither value alone
     reaches every pod. Every operator client (`r.newValkeyClient` sites) and the health
     checker (`readAuthPassword`) try the current key and fall back to the previous one on
     `WRONGPASS`/`NOAUTH`. The earlier plan, "switch the operator's own client last", cannot
     work for that reason.
  2. **Ensure both, as a level re-measured every pass**, on every data and Sentinel pod
     proven ours ([ADR 0020](../adr/0020-write-only-what-the-operator-owns.md), `podIsOurs`):
     each accepts both passwords (`ACL SETUSER default >new`, or `>old` on a pod that came back
     with only the new one — measured idempotent, M7); every data pod has `masterauth new`;
     every Sentinel has `auth-pass new` and, with Sentinel auth on, `sentinel-pass new`.
  3. How the old password is withdrawn is D2.
  4. Progress is a `conditionRegistry` row ([ADR 0027](../adr/0027-conditions-are-levels-edges-or-history.md)).
  Nothing happens without the previous key, so an operator upgrade changes nothing
  ([ADR 0005](../adr/0005-upgrade-neutral-defaults-and-anti-affinity.md) D1). The runtime
  commands go through typed client wrappers whose errors carry no password (`ExecGet` already
  exists; the `SentinelSet` redaction is a Work-list item). No REPLICAOF and no delete is added
  by the runtime phase, so the six master-authority rules are untouched by it.
  *Cost:* L — a CRD field and `make generate-all`, try-both auth at every client site, the
  wrappers, the rotation level and its bound, the condition row, e2e on both lines.
  *Consequences:* a runtime state machine changes auth on every pod of a cluster; a bug there
  can lock the operator and the sidecars out, which is why every step is idempotent and
  re-measured per pass rather than executed once. The runtime changes are not persisted in the
  data pods, whose config file is a read-only ConfigMap; that is consistent, because an
  in-place restart takes the new env value once the Secret holds it, and step 2 re-adds the
  missing password.

**Why C.** C is the only option that meets the owner's recorded wish, "automatically, without
losing their state, even ... without a PV" ([`.github/idea.md:7`](../../.github/idea.md)), and
every Valkey behaviour it depends on is now measured on both pinned lines (M2–M4, M6, M7), so
the remaining risk is operator code that ADR 0010, ADR 0020, ADR 0027 and the default-off rule
already constrain. It beats M on the one failure M cannot cover: a pod created or restarted
inside the window. Under M a human has to notice it; under C the per-pass level re-adds the
missing password. On the production fleet as described in the run context (not verifiable in
this repository: Flux-applied resources, Chaos Mesh killing one pod every 5 min in
`database-examples`) that window is not rare, and C turns a rotation into one Secret write that
can go through Git. M's benefit is not lost: its runbook ships anyway, and it is exactly the
sequence C automates.

### D2 — (only if C) when does a pod stop accepting the old password?

**Mechanism.** `valkey-server` and `valkey-sentinel` can drop the old password at runtime
(`ACL SETUSER default <old`): authenticated connections survive and new dials with it get
`WRONGPASS` (M3, M4). But several processes hold the password from their own start and never
re-read it — the sidecar ([`cmd/sidecar/sidecar.go:75`](../../cmd/sidecar/sidecar.go)), its
drain handler ([`drain.go:462-476`](../../internal/sidecar/drain.go)), the observer
([`cmd/observer/observer.go:95`](../../cmd/observer/observer.go)), the exporter
([`statefulset.go:1075-1087`](../../internal/builder/statefulset.go)) and the exec probes. The
probes do not matter, because `valkey-cli` exits 0 on `NOAUTH` (M5). The others do: the labeler
freezes `instanceRole` ([`labeler.go:124-127`](../../internal/sidecar/labeler.go)), the drain
promotion and the Sentinel cross-check fail, the observer fails, the exporter reports the
instance down. The operator cannot see which value a running process holds. A replaced pod or
a restarted container gets the value then in the Secret (kubelet source, Fact), and
[`statefulset.go:830`](../../internal/builder/statefulset.go) makes a replacement accept **only**
that value — a replacement does not accept both.

**What the choice changes and what it does not.** It decides whether and how the old password
is revoked; it does not change C's ensure-both phase, which every variant starts with.
Withdrawing the old password at runtime while any process that read it at start is alive is
not an option: it cuts that sidecar off its own Valkey (above). A variant where the sidecar
and observer re-read a mounted password file was considered and removed (History).

- **C-a — keep accepting the old password until each pod is replaced by something else.**
  After the ensure-both phase the operator reports how many pods still accept the old
  password; no roll. *Cost:* S on top of C. *Consequences:* a leaked password stays valid on
  every pod nobody replaces, so the revocation the threat line asks for stays a hand roll.
  Worse, it is a permanent split: the observer is never restarted and every sidecar of an
  unreplaced pod keeps the old password, so any pod replaced later (a Chaos Mesh kill, an
  eviction) starts with only the new one and is unreachable for those processes — observer
  failures, a failed drain promotion onto that pod. The only prevention would be to keep adding
  the old password to new pods forever, and then the rotation never ends.
- **C-b — close the rotation with an operator-driven failover-aware roll, keep both accepted
  until it ends, then withdraw the old password (recommended).** When the ensure-both level
  first holds, the operator writes a record that is not derived from the content — for example
  the auth Secret's `resourceVersion` at that moment — as an env var on the carrier container,
  where [ADR 0031](../adr/0031-a-record-the-operator-trusts-lives-in-pod-spec.md) wants such
  records, and on the observer Deployment's template. Three constraints, from reading the
  ADR 0030/0031 record machinery (not built or measured):
  (i) the record must be part of the hashed pod spec and absent until the first rotation — a
  record stamped outside the hash under the ADR 0030/0031 presence rule ("a pod without the
  record is never restarted for one") would count every pre-rotation pod as unmeasured and roll
  nothing; it is captured once and inherited from the persisted template, never re-derived from
  the live `resourceVersion`, or removing the previous key would roll a second time;
  (ii) the observer Deployment restarts with the same record, because its process dials every
  pod with the old password;
  (iii) during the roll the ensure-both level keeps adding the old password to every
  replacement, because a replacement accepts only the new one (above) while the sidecars and
  drain handlers of the pods not yet replaced still dial with the old one — without it, an
  unplanned delete of an unreplaced master mid-roll (Chaos Mesh, eviction) finds no reachable
  synced replica for the ADR 0012 drain promotion on a non-Sentinel cluster. Once every pod of
  both tiers and the observer carry the record, no process holding the old password is left,
  and the operator withdraws it at runtime (`ACL SETUSER default <old` on every pod, which M3
  and M4 show is safe for links and clients) and reports completion; the user then removes the
  previous key. A single non-persistent data pod is deferred and reported, as ADR 0032's
  `singlePodDeferral` does, and keeps accepting both. *Cost:* M on top of C (record, roll
  trigger, deferral level, withdrawal step, e2e). *Consequences:* one roll per rotation, of the
  rotated cluster only; lossless because the dual-accept window covers the whole roll (a
  new-`masterauth` replica syncs from a master that accepts both, M1b and M3). ADR 0026, ADR 0010
  and ADR 0025's zero-Warning promise apply to that roll as to any other. The price of (iii) is
  that the previous key must stay in the Secret until the operator reports completion.

**Why C-b.** It is the only variant that ends with no pod accepting the old password (the
reported single non-persistent pod aside) without a fleet-wide roll at the release that ships
it and without the operator needing to see a process's environment: it reuses the
failover-aware roll and ADR 0030's split, "every other process rides a roll". It beats C-a
because C-a never revokes the old password on pods nobody replaces — the one thing the threat
line asks for — and leaves the permanent observer/sidecar split above. Keeping both passwords
on the replacements during the roll, rather than accepting a failed sidecar hand-over for the
roll's duration, is part of the recommendation because an unplanned master delete during a
roll is to be expected, not an edge case, wherever evictions happen, and in `database-examples`
Chaos Mesh kills an operator-managed pod every 5 min (run context, not verifiable in this
repository); the cost is only that the previous key stays until completion.

### D3 — (only if C) where does the operator get the previous password from?

**Mechanism.** The operator holds no copy of the password: it reads
`spec.auth.secretName`/`secretPasswordKey` per call
([`valkey_controller.go:177-190`](../../internal/controller/valkey_controller.go)), and
ADR 0016 D1 ([`0016-...md:36-42`](../adr/0016-authentication-and-tls-posture.md)) rules out an
operator-owned credential, which also rules out capturing the old value from the watch event
(Fact). Adding the new password to a pod that accepts only the old one needs AUTH with the old
value (M2), so the user has to supply it. The pods keep their `secretKeyRef` to the key that
then holds the new value, so a pod created during or after the rotation starts on the new
password. The choice decides the API surface; it does not change D1 (the user owns both
values).

- **(a) — an optional CRD field naming a key of the previous password in the same auth Secret
  (recommended).** For example `spec.auth.previousPasswordKey`, unset by default; when it is
  set and the key is present, a rotation is in progress; the user removes the key when the
  operator reports completion. *Cost:* part of C's L (field, `make generate-all`, README CRD
  reference). *Consequences:* `secretConcernsValkey` already matches the auth Secret
  ([`valkey_controller.go:3053-3055`](../../internal/controller/valkey_controller.go)), so no
  new watch. Under Flux the old value stays in Git (SOPS) or the store until completion, which
  (c) and (d) share.
- **(c) — a second Secret reference**, for example `spec.auth.previousSecretName`. Fits setups
  that keep one Secret per credential. *Cost:* like (a), plus a second name in
  `secretConcernsValkey`. *Consequences:* the new and the old password arrive as two writes to
  two objects; if the password Secret changes first, the operator faces today's split for as
  long as the second write takes.
- **(d) — the previous password is the Secret the running pods still reference.** The
  rotation is a change of `spec.auth.secretName` or `secretPasswordKey`; the operator reads
  each pod's credential source from that pod's immutable spec (its `secretKeyRef`, pod proven
  ours) and ensures both are accepted before any delete of the roll the reference change
  already triggers. *Cost:* M–L, no new CRD field; the closing roll of D2 comes for free.
  *Consequences:* it turns today's lossy reference-change roll into a lossless one, but it
  cannot see an in-place value change at all, and with a generated Secret name (Kustomize
  `secretGenerator` hash suffix) under Flux prune the old Secret may be gone before the operator
  reads it (prune ordering not verified), so the roll would have to be held and reported.

**Why (a).** One update of one Secret carries both values, so the operator never observes the
new password without the old one, and the existing watch covers it. It beats (d) because it
covers in-place value rotation — the same Secret name and key, which is what `kubectl edit
secret` and a refreshed ExternalSecret into a fixed target produce — and (d) cannot see that
case at all. It beats (c) because (c) splits the rotation into two writes to two objects, and
whichever order the user or a secret controller applies them in, today's authentication split
opens between them. Making the reference-change path lossless as well, (d)'s one strength, is
covered for now by the runbook (Work list).

## Work list

**Not waiting on a decision** (every outcome of D1 keeps the manual path, so these hold
either way; all are outside `docs/tickets/` and were not done by the 2026-09-27
re-verification, which edited tickets only; they are this ticket's work, for whoever implements
it):

1. **XS, carries the rule-1 urgency:** correct ADR 0016 in one change — D13
   ([`0016-...md:185-186`](../adr/0016-authentication-and-tls-posture.md)): "loses in-memory
   data if it has no failover target" becomes "at any replica count", because a replica
   started with the new password cannot sync from a master on the old one (measured in docker
   on both pinned lines, recorded in the ADR itself since nothing outside `docs/tickets/` may
   cite this ticket); and the Context bullet at `:30`: "exactly once, at pod start" becomes "at
   every container start, restarts included; for Sentinel fixed per pod by the init-written
   config". Mark the old clauses superseded in place and add a dated Status amendment.
2. **XS:** [`rotation-and-change-propagation.md`](../security/rotation-and-change-propagation.md):
   `:28` "once, at pod start" as in item 1; `:23` `valkey_controller.go:3005` → `:3006`; the
   "read from the code, not measured" label at `:42-46` and at
   [`authentication.md:43`](../operations/authentication.md) can point at the ADR's measurement.
3. **S:** replace the lossy steps of
   [`authentication.md#changing-the-password`](../operations/authentication.md#changing-the-password)
   (`:63-107`) and the step text of ADR 0016 D13 with the measured lossless runbook: `ACL
   SETUSER default >new` on every data pod and Sentinel; `CONFIG SET masterauth new` on every
   data pod; `SENTINEL SET <monitor> auth-pass new` and, with Sentinel auth on, `SENTINEL
   CONFIG SET sentinel-pass new` on every Sentinel; then change the Secret; then replace the
   pods, replicas first, and restart the observer. Caveats to document: a pod created or a
   container restarted before the Secret change must be treated again; a password on a
   `valkey-cli` command line reaches the process list, so use `REDISCLI_AUTH`; revoking the old
   password means replacing every pod; the recovery step M1b (if replicas were already replaced
   after the Secret change, add the new password to the running master before deleting it);
   the same runbook before a reference change (`:109-116`) makes that roll lossless. Also
   correct `:51-52`: the replacement becomes Ready not because the probe authenticates with the
   pod's own password but because the probe passes on any reply (M5) and its sidecar
   authenticates once against its own server with the same Secret value it started with (the
   sidecar half added 2026-09-27 from T76, Impact case 3; T76 Work list item 2 lists the same
   correction as owned here, not to be done twice). This overlaps option M of
   D1: under M it is the whole answer.
4. **XS:** stop writing the value into the `SentinelSet` error
   ([`client.go:285`](../../internal/valkeyclient/client.go)), at least for `auth-pass`, and
   invert [`exec_test.go:480-484`](../../internal/valkeyclient/exec_test.go) to assert the
   password is absent (ADR 0016 D3). Dormant today; it must land before any caller logs the
   error, option C included.
5. **S, proposed:** report an authentication failure with a reason of its own instead of `no
   master found among N pods` on Sentinel clusters (`checker.go:193-197` swallows the error).
   Helps any D1 outcome, the manual runbook included. It touches `probeMasterRole`'s error
   handling, so it needs a look at the status reading of
   [ADR 0002](../adr/0002-surface-a-blocked-reconcile-on-the-cr.md) before it is built.
6. ~~**File a separate ticket** for the probe exit-code finding (M5).~~ Done 2026-09-27: filed
   as [T76](076-the-exec-probes-pass-on-any-server-reply.md), the exec probes passing on any
   server reply.
7. ~~Carry the two T50-relevant facts (Fact, cross-ticket) into ticket 050.~~ Done: 050 carries
   both in its bullet on this ticket (`valkey-cli` exits 0 on `NOAUTH`, M5; a Sentinel's own
   password governs its peer links unless `sentinel-pass` is set, M6), checked 2026-09-27.

The docker spike the earlier work list asked for is done (Fact, M1–M7).

**Waiting on the decision (C with D2 C-b and D3 (a)):**

1. The previous-key field in the CRD, and `make generate-all`.
2. Typed client wrappers for `ACL SETUSER`, `CONFIG SET masterauth`, `SENTINEL CONFIG SET
   sentinel-pass`, errors without the password.
3. Try-both authentication at every operator client site and in the health checker.
4. The ensure-both level, bounded ([ADR 0010](../adr/0010-every-rolling-update-wait-is-bounded.md)),
   on pods proven ours.
5. The rotation record in the hashed pod spec of both tiers and the observer, the closing roll,
   the deferral of a single non-persistent pod, and the runtime withdrawal after the roll.
6. A condition row ([ADR 0027](../adr/0027-conditions-are-levels-edges-or-history.md)).
7. e2e on both lines with both revert checks (Verification).

**Close (ADR 0034):** amend ADR 0016 D12/D13 or write a new ADR. Then the rotation page, H-24,
[`authentication.md`](../operations/authentication.md#changing-the-password) and the README
CRD reference. `git grep -n 'T51\|051-a-changed'` outside `docs/tickets/` (none on
2026-09-27 at `84a39c2`), then move to `archive/`.

## Decision

Not decided.

## Verification

- For the decision-free items: `git grep -n "no failover target\|once, at pod start" --
  docs ':!docs/tickets'` finds each hit struck in place with its correction next to it, and `exec_test.go` asserts
  the password is absent from the `SentinelSet` error.
- For C with C-b: e2e on both pinned lines: write keys, rotate the Secret with the previous
  key, then every data and Sentinel pod accepts the new password and refuses the old one, the
  keys are still there on a non-persistent cluster, a write on the master reaches the replicas,
  the sidecar role labels follow a failover after the rotation, the observer stays Ready, and
  the exporter reports the instance up.
- Revert checks. ~~Revert check: with the runtime switch removed, the same test fails on the old
  password still being accepted.~~ *(corrected 2026-09-27 at 84a39c2: that check is right for
  C as first written, where the runtime switch itself removed the old password; the restated C
  with C-b needs two. (1) Remove the ensure-both step: the closing roll's replacements cannot
  sync, and the test fails on the key count of the non-persistent cluster (and on
  `RollingUpdatePaused`). (2) Remove the closing roll and the withdrawal: the test fails on the
  old password still being accepted.)*

## History

- 2026-09-27 — re-verified at 84a39c2. **Checked:** every Fact line against the code (no code
  on the password path changed since `4a7543e`; `git diff 4a7543e 84a39c2` moves lines only),
  ADR 0016, ADR 0030, the operations and security pages, upstream kubelet `release-1.36`
  source, and the cross-references to 012, 023, 036, 040, 050, 062 and archive/031.
  **Locations re-read at 84a39c2:** `findValkeyForSecret` `:3005` → `:3006`, the watch
  `2996–2999` → `2997-3000`, `secretConcernsValkey` `3050–3057` → `3052-3060`,
  `resetSentinelState` `3614` → `3617` (password `:3647`, `auth-pass` `:3692`, callers `:958`,
  `:1007`, `:1049`, `:3161`, `:3308`), fixed directly in the links. **Found false:** "the
  Sentinel half of option C already has plumbing" (only `auth-pass` has; `ACL SETUSER` and
  `sentinel-pass` are also needed); "the client has no exported generic command method"
  (`ExecGet`, `ExecMulti`); "no XS item" in the Work list; option C's "removes the old password"
  (the sidecar, drain handler, observer and exporter hold it from start); the revert check
  (right only for the old C). **Answered:** all four former Not-verified items (Fact). **New
  facts:** the window fails closed and on Sentinel clusters does not name authentication; the
  env-pinned consumers; kubelet re-resolution on restart; the failover retry reaches every
  Sentinel only with `disableAuth`; `SentinelSet` puts the password into its error; ADR 0016
  Context `:30` and D13 `:185-186` still state what is now known false; the reference-change
  path rolls and loses data. **Measured** in docker on 9.1.1 and 8.1.9: M1–M7 and M1b, with
  commands and results in Fact; all containers and networks removed. **Disputed and resolved:**
  the auditor's replacement revert check was refuted for the old C and is correct only for the
  restated C, so Verification carries both; the auditor's "every pod accepts both during the
  closing roll" was refuted (a replacement accepts only the new value, `statefulset.go:830`),
  so C-b now keeps adding the old password to replacements until the roll ends; the auditor's
  "the status message carries the WRONGPASS text" holds only without Sentinel; rule 1 rests on
  reading D13's conditional "if it has no failover target" as exclusive, which the rotation page
  already did on 2026-09-27. **Options rewritten** into three decisions (D1 whether, D2 when the
  old password is refused, D3 where the previous value comes from). **Removed options:**
  *A — keep D13 as documented* (the lossy steps): dominated by the lossless runbook, which keeps
  the same no-code shape without the data loss; *B — roll on the Secret's `resourceVersion`*:
  without a dual-accept window it is the lossy reference-change roll, and any metadata write to
  the Secret would roll the tier; it survives only as the closing roll of D2 C-b; *D — a content
  digest*: not a choice, refused by ADR 0030 D11, kept as one sentence at the head of Options;
  *C-c — sidecar and observer re-read a mounted password file, then runtime removal*:
  disproportionate, the new volume rolls the whole fleet at the release, the third-party
  exporter still pins its env, and kubelet's volume refresh would race the removal; *D3 (b) — a
  fixed key-name convention*: a Secret already carrying a key of that name would start a
  rotation on the operator upgrade (ADR 0005 D1) and the contract would be invisible in the CRD
  reference. **Added options:** M (manual lossless runbook), C-a, C-b, D3 (a), (c), (d).
  **Recommendation:** C stays recommended, restated: try-both authentication instead of
  "the operator client switches last" (which cannot work once pods start on the new password),
  ensure-both as a per-pass level, `sentinel-pass` added, and no runtime withdrawal before every
  process that read the old value has restarted; D2 C-b and D3 (a) recommended. The production
  cluster names in the justification are marked as run context, not verifiable in the
  repository. **Frontmatter:** `state` filed → analysed (every former Not-verified item is
  answered and the options are restated); `urgency` next → now (rule 1 matches first: ADR 0016
  D13 states a measured-false premise; recompute to next once Work-list item 1 lands); `threat`
  reworded to what the fix would additionally cover (the data loss belongs to severity and
  Impact, and stops being true once the runbook rewrite lands); severity comment, `effort`
  comment and `blocked-by` comment updated, values unchanged (medium, L, decision).
  **Review of this entry's edit** (same day, code re-read at `84a39c2`): the
  `authentication.md` citations pointed past the end of the 116-line file and are fixed
  (`:117-123` → `:109-116`, `:63-115` → `:63-107`, `:52-53` → `:51-52`); the single-pod loss
  on a reference change is now cited to the code (`singlePodDeferral`,
  `handleStandaloneRollingUpdate`) instead of that page, which does not state it; the init
  fallback range now includes `MAX_WAIT=30` (`:291`); `readAuthPassword` reads once per
  `CheckCluster` pass, per call elsewhere; the measurement attribution now says which reviewer
  re-ran what; the `036:82` citation is pinned to `84a39c2` because 036 is being edited in the
  same run; the Verification grep excludes `docs/tickets/`, which quotes the struck phrases;
  the C-b justification no longer generalises the `database-examples` Chaos Mesh schedule to the
  whole fleet.
  Cross-ticket: in the consistency pass of the same day, the 062 note was corrected (062 cites
  `:3617`) and extended with the effect of 062's recommended gate and fallback removal on the Fact
  bullet about the failover retry (read, not measured); the two facts marked for T50 are now in
  050, and 023's note on this ticket, which named the removed option B, now names the
  reference-change path.
  Filed: the probe exit-code finding (M5) as
  [T76](076-the-exec-probes-pass-on-any-server-reply.md) (severity low, security none, effort M,
  state analysed); the cross-ticket paragraph that parked it, with its sibling notes to 012, 023
  and 036, is replaced by a pointer, and Work list item 6 is marked done. M5 stays in Fact
  because D2 and Work list item 3 rest on it; D2 is unchanged, since T76's recommended option C
  keeps an auth error counting as ready (its option B would not). Work list item 3's
  `authentication.md:51-52` correction now also names the sidecar's one authentication, the half
  T76 found missing. Severity, urgency and the other frontmatter values do not rest on M5 and are
  unchanged.
  Sweep: The Work list's lead-in no longer gives the re-verification's file limit as the reason its
  items are open; it names them as this ticket's work for whoever implements it. The Cross-ticket
  bullet on 050 no longer says its two facts are still for 050's file: 050 carries both. Frontmatter
  unchanged.
  Final pass: M5's probe citations re-read at `84a39c2` and corrected - `SentinelProbeCommand` is
  `sentinel.go:420-459` (T76 cites the same range) and `ProbeCommand` is `statefulset.go:1515-1545`,
  the former `:420-446` and `:1515-1531` being each function's head and auth branch only, which
  M5's measurement (any server reply passes, auth or not) outgrows; Work list item 3 already
  carries the sidecar half of the `authentication.md:51-52` correction that T76 found missing
  (checked against T76 Impact case 3 and its Work list item 2), so it needed no edit; the
  Cross-ticket bullet on 050 was checked against 050's current text, which carries both facts
  (M5 and M6) in its bullet on this ticket, and Work list item 7, which still asked to carry them,
  is marked done. Frontmatter unchanged.
- 2026-09-27 — review - precised the client fact (an unexported generic `exec` exists, so the
  new methods of option C are thin wrappers). Recommendation, urgency and frontmatter unchanged.
- 2026-09-27 — enriched - added the operator's immediate switch (`readValkeyPassword`), the
  shared `masterauth`/`requirepass`, and the existing runtime `SENTINEL SET auth-pass` in
  `resetSentinelState` with its client plumbing. Corrected "without a failover target" to "at
  any replica count" (severity comment, Impact), and added the docker spike as the
  decision-independent first step.
- 2026-09-27 — urgency `icebox` → `next`. Rule 3 (severity ≥ medium and trigger live) comes
  before rule 5 and matches: the trigger is a password change, a routine user action possible
  on every auth-enabled cluster today, not a dormant compromise. The filing applied rule 5
  without testing rule 3. It is still `blocked-by: decision`.
- 2026-09-27 — filed from the row "Password rotation" of archive/031, which names ADR 0030 D11.
  Gap [H-24](../security/rotation-and-change-propagation.md#h-24) states what is missing and
  what to do in the meantime.
