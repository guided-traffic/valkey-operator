---
id: T51
title: a changed cluster password reaches no running pod
state: analysed       # Valkey and Sentinel behaviour measured in docker on both pinned lines; no end-to-end rotation run on a cluster
severity: medium      # rotating means replacing every pod by hand, and a non-persistent cluster loses its data on it at any replica count
security: hardening
threat: "would additionally cover a leaked cluster password: today it stays valid on every pod not yet replaced by hand, and connections already authenticated with it survive until their pod is replaced"
urgency: now          # rule 1: ADR 0016 D13 states a measured-false premise; next (rule 3) once Required change 1 lands
effort: L             # option C with Q2 C-b and Q3 (a); the decision-free items are XS to S
blocked-by: decision  # Q1-Q3; the decision-free items are not blocked
filed-from: T31, section "Further security measures — not in this change, each open" (archive/031)
opened: 2026-09-27
decided:
done:
---

# T51 - a changed cluster password reaches no running pod

The operator-facing statement is [the password rotation gap](../security/rotation-and-change-propagation.md#the-password-rotation-gap)
and gap [H-24](../security/rotation-and-change-propagation.md#h-24). The owner's wish
([`.github/idea.md:7`](../../.github/idea.md)): after the password Secret changes, the instances
take the new password without losing their state, also without a persistent volume.

## Current state

**How the password flows today.**

- The operator reads the auth Secret per call (`readValkeyPassword`,
  [`valkey_controller.go:177-190`](../../internal/controller/valkey_controller.go); `readAuthPassword`,
  [`checker.go:75-87`](../../internal/health/checker.go)), so it switches to a new value at once. The
  Secret is watched (`secretConcernsValkey`, [`valkey_controller.go:3053-3055`](../../internal/controller/valkey_controller.go)).
- The pods get it as `env.valueFrom.secretKeyRef`, which kubelet resolves at every container start,
  in-place restarts included. The pod-spec hash covers the reference, not the value (ADR 0016 D12,
  [`statefulset.go:1471-1482`](../../internal/builder/statefulset.go)), so nothing rolls.
- `valkey-server` takes `requirepass` and `masterauth` from the same `$VALKEY_PASSWORD`
  ([`statefulset.go:830`](../../internal/builder/statefulset.go)). Sentinel's init writes
  `requirepass` and `auth-pass` into an `emptyDir` config ([`sentinel.go:176-190`](../../internal/builder/sentinel.go),
  `:715`), fixed per pod; no `sentinel-pass` is set.
- Read once at process start: the sidecar ([`cmd/sidecar/sidecar.go:75`](../../cmd/sidecar/sidecar.go))
  and its drain handler ([`drain.go:462-476`](../../internal/sidecar/drain.go)), the observer
  ([`cmd/observer/observer.go:95`](../../cmd/observer/observer.go)), the exporter
  ([`statefulset.go:1075-1087`](../../internal/builder/statefulset.go)).
- ADR 0016 D13 makes rotation manual: change the Secret, then replace pods, replicas first. Its
  claim that a non-persistent cluster loses data only "if it has no failover target"
  ([`0016-...md:185-186`](../adr/0016-authentication-and-tls-posture.md)) is false (M1). ADR 0016
  D1 forbids an operator-owned copy of the password; ADR 0030 D11 forbids a password digest on the
  pod template. Neither is reopened here.

**Measured in docker on `valkey/valkey:9.1.1` and `8.1.9`** (identical on both):

- **M1:** a replica on the new password cannot sync from a master on the old one
  (`master_link_status:down`, `DBSIZE 0`, `-WRONGPASS`). **M1b:** `ACL SETUSER default >new` on that
  master brings the replica up with the data within 8 s.
- **M2:** `ACL SETUSER default >new` keeps `old` valid; `CONFIG SET requirepass` replaces all.
  **M7:** `>new` is idempotent; runtime changes are not persisted (read-only ConfigMap config).
- **M3:** `masterauth` applies at the next reconnect; an established link survives removal of the
  old password on the master (`<old`). **M4:** authenticated clients survive password removal.
- **M5:** `valkey-cli` exits 0 on `WRONGPASS`/`NOAUTH` and any error reply, so the exec probes
  ([`statefulset.go:1515-1545`](../../internal/builder/statefulset.go),
  [`sentinel.go:420-459`](../../internal/builder/sentinel.go)) pass on any server reply.
- **M6:** Sentinel refuses `CONFIG SET` but takes `ACL SETUSER default >new`; `SENTINEL SET <monitor>
  auth-pass` applies at once; Sentinels whose own old password is removed mark each other `s_down`
  unless `SENTINEL CONFIG SET sentinel-pass new` is set.

**What goes wrong between the Secret change and the last pod replacement.**

- The pass fails closed and names no auth failure on Sentinel clusters: `probeMasterRole` swallows the
  error ([`checker.go:193-197`](../../internal/health/checker.go)), phase `Error`,
  `no master found among N pods`, `Ready=False/ClusterHealthCheckFailed`
  ([`valkey_controller.go:2464-2474`](../../internal/controller/valkey_controller.go)). Without
  Sentinel: `Instance unreachable: ... WRONGPASS` (`:2237-2247`). No promotion, no `REPLICAOF`, no Event.
- A `valkey` container restarted in place comes back on the new password while its sidecar and
  exporter keep the old one: the labeler freezes `instanceRole` ([`labeler.go:124-127`](../../internal/sidecar/labeler.go)),
  the drain handler exits ([`drain.go:106-110`](../../internal/sidecar/drain.go)), the exporter
  reports down. This follows any Secret change today.
- A failover retry in `resetSentinelState` ([`rolling_update.go:3617`](../../internal/controller/rolling_update.go))
  reaches only Sentinels on the new password; with `disableAuth: true` it switches every Sentinel's
  `auth-pass` and cuts them off old-password data pods. Dormant unless a roll's retry is in flight.
- `SentinelSet` puts the option value, for `auth-pass` the password, into its error
  ([`client.go:285`](../../internal/valkeyclient/client.go), pinned by
  [`exec_test.go:480-484`](../../internal/valkeyclient/exec_test.go)); every caller discards it, so
  it breaches ADR 0016 D3 only once one logs it.
- Changing `spec.auth.secretName`/`secretPasswordKey` does roll, but with a different password the
  first replica cannot sync (M1), the roll pauses after `syncTimeout`, and a single non-persistent pod
  is replaced at once and loses its data ([`pod_security_migration.go:128-150`](../../internal/controller/pod_security_migration.go)).

**Impact.** Every auth-enabled cluster whose password changes: `Error`/`Ready=False` for the whole
window; the manual roll loses the data of every non-persistent cluster at any replica count. A leaked
password stays valid on every unreplaced pod (M4). Hardening: ADR 0016 D13 documents the gap. No e2e
changes a password.

## Required changes

**Independent of the open questions** (the manual path stays in every outcome):

1. **XS:** correct ADR 0016: D13 (`:185-186`) "if it has no failover target" becomes "at any
   replica count", with the docker measurement recorded in the ADR; Context `:30` becomes "at every
   container start; for Sentinel fixed per pod by the init-written config". Mark the old clauses
   superseded in place, add a dated Status amendment.
2. **XS:** [`rotation-and-change-propagation.md`](../security/rotation-and-change-propagation.md):
   `:28` as in item 1; `:23` `:3005` → `:3006`; the "not measured" labels at `:42-46` and
   [`authentication.md:43`](../operations/authentication.md) point at the ADR's measurement.
3. **S:** replace the lossy steps in [`authentication.md#changing-the-password`](../operations/authentication.md#changing-the-password)
   (`:63-107`) and ADR 0016 D13 with the lossless runbook: `ACL SETUSER default >new` on every data
   pod and Sentinel; `CONFIG SET masterauth new` on every data pod; `SENTINEL SET <monitor>
   auth-pass new` and, with Sentinel auth on, `SENTINEL CONFIG SET sentinel-pass new` on every
   Sentinel; then change the Secret; then replace pods, replicas first, and restart the observer.
   Document: a pod created or restarted before the Secret change must be treated again; use
   `REDISCLI_AUTH`, not `-a`; revoking the old password means replacing every pod; the M1b rescue;
   the same runbook before a reference change (`:109-116`) makes that roll lossless. Correct
   `:51-52`: the replacement becomes Ready because the probe passes on any reply (M5) and its
   sidecar authenticates with the value it started with.
4. **XS:** keep the value out of the `SentinelSet` error ([`client.go:285`](../../internal/valkeyclient/client.go)),
   at least for `auth-pass`; invert [`exec_test.go:480-484`](../../internal/valkeyclient/exec_test.go)
   to assert the password is absent. Must land before any caller logs it.
5. **S:** report an authentication failure with a reason of its own instead of `no master found`
   (`checker.go:193-197`); check the status reading of [ADR 0002](../adr/0002-surface-a-blocked-reconcile-on-the-cr.md) first.

Test: `git grep -n "no failover target\|once, at pod start" -- docs ':!docs/tickets'` finds each hit
struck with its correction.

**Depends on the answers** (for C with C-b and (a)):

1. The previous-key CRD field (`make generate-all`, README CRD reference) and a
   `conditionRegistry` row ([ADR 0027](../adr/0027-conditions-are-levels-edges-or-history.md)).
2. Typed client wrappers (thin, like `SentinelSet`) for `ACL SETUSER`, `CONFIG SET masterauth`,
   `SENTINEL CONFIG SET sentinel-pass`, errors without the password.
3. Try-both authentication (current key, fallback to previous on `WRONGPASS`/`NOAUTH`) at every
   `r.newValkeyClient` site and in `readAuthPassword`.
4. The ensure-both level, re-measured every pass and bounded ([ADR 0010](../adr/0010-every-rolling-update-wait-is-bounded.md)),
   on pods proven ours ([ADR 0020](../adr/0020-write-only-what-the-operator-owns.md)); then the
   rotation record, the closing roll, the single-pod deferral and the withdrawal (Q2).
5. e2e on both pinned lines: write keys, rotate with the previous key; afterwards every data and
   Sentinel pod accepts only the new password, the keys survive on a non-persistent cluster, writes
   replicate, role labels follow a failover, the observer stays Ready, the exporter reports up.
   Revert checks: without ensure-both the non-persistent key count fails (and `RollingUpdatePaused`
   appears); without the closing roll and withdrawal the old password is still accepted.
6. Close: amend ADR 0016 D12/D13 or write a new ADR; update the rotation page, H-24,
   `authentication.md`.

## Open questions

### Q1: Does the operator carry a password change to the running pods itself?

Valkey and Sentinel can accept a second password and switch `masterauth`, `auth-pass` and
`sentinel-pass` at runtime (M2-M4, M6). C reopens ADR 0016 D12/D13; the runbook ships either way.

- **M - stays manual with the lossless runbook.** No code. Every rotation is three to five
  `kubectl exec` commands per pod; a pod created or restarted inside the window must be noticed and
  treated by hand; the owner's "automatically" is not met.
- **C - operator-driven runtime rotation (recommended).** The user puts the previous password next
  to the new one (Q3); while it is present the operator tries both passwords when it connects and
  ensures on every pod, per pass, that both are accepted, `masterauth` and `auth-pass` are new and
  `sentinel-pass` is set; withdrawal is Q2. Nothing happens without the previous key, so an upgrade
  changes nothing. No `REPLICAOF` or delete is added. Cost L; a bug can lock the operator and
  sidecars out, which is why every step is idempotent and re-measured.

C is the only option that meets the owner's wish, and every Valkey behaviour it relies on is
measured; its per-pass level repairs a pod created or restarted inside the window, which under M a
human must catch.

**Answer:** _open_

### Q2: (only if C) When does a pod stop accepting the old password?

The sidecar, drain handler, observer and exporter hold the password from their start and cannot be
told a new one; a replaced pod or restarted container accepts only the value then in the Secret
(`statefulset.go:830`). Withdrawing the old password while such a process lives cuts it off; the
exec probes do not matter, because they pass on an auth error (M5).

- **C-a - never withdraw; report the pods still accepting the old one.** Cost S. A leaked password
  stays valid on unreplaced pods, and every pod replaced later is unreachable for the observer and
  the old sidecars: a permanent split.
- **C-b - close with an operator-driven failover-aware roll, then withdraw (recommended).** When
  ensure-both first holds, the operator stamps a record (for example the Secret's `resourceVersion`,
  captured once and inherited) into the hashed pod spec of both tiers and the observer template
  ([ADR 0031](../adr/0031-a-record-the-operator-trusts-lives-in-pod-spec.md)), absent until the
  first rotation. During the roll ensure-both keeps adding the old password to replacements, so an
  unplanned master delete still finds a reachable replica for the drain promotion. When all carry
  the record it runs `ACL SETUSER default <old` everywhere (safe per M3, M4). A single
  non-persistent pod is deferred and reported. Cost M on top of C; the previous key must stay until
  completion.

C-b is the only variant that ends with no pod accepting the old password without a fleet-wide roll
at release, and it reuses the existing roll.

**Answer:** _open_

### Q3: (only if C) Where does the operator get the previous password from?

ADR 0016 D1 forbids an operator-held copy, and adding the new password to an old pod needs AUTH with
the old value (M2), so the user must supply it.

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

- No rotation has run end to end on a cluster (manual, runbook or C); settled by the e2e of the
  chosen option, or a Kind run of the runbook.
- The reference-change roll's pause and kubelet's re-resolution on container restart are read
  (code, upstream `release-1.36` source), not measured on a cluster.
- The production facts behind Q1 and Q2 (Flux with prune, Chaos Mesh killing a pod every 5 min in
  `database-examples`) come from the owner's run context, not this repository.
- Whether External Secrets Operator can map a previous remote version to a second key, and the order
  of kustomize-controller's prune against a CR change (both bear on Q3).
- Whether a one- or two-Sentinel tier keeps a failover majority while its Sentinels are on different
  passwords.

## Related

- [T50](050-every-component-authenticates-with-the-one-cluster-password.md): its default-user
  clients are the env-pinned consumers of Q2; an exporter user of its own needs the same rotation.
- [T62](062-resetsentinelstate-falls-back-to-sentinel-reset.md): decides `resetSentinelState`'s
  `RESET` fallback and gate, which changes the failover-retry behaviour above.
- [T76](076-the-exec-probes-pass-on-any-server-reply.md): the probe exit-code finding (M5); its
  option B (`-e`) would break Q2's premise that probes do not matter.
