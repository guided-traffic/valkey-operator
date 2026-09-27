---
id: T51
title: a changed cluster password reaches no running pod
state: filed
severity: medium      # rotating means a manual roll of every pod; a non-persistent cluster ~~without a failover target~~ loses its data on it (corrected 2026-09-27: at any replica count)
security: hardening
threat: "would additionally cover a leaked cluster password: today revoking it takes a manual roll of every pod, and until that roll the operator authenticates with the new password against pods that accept only the old one, so its health checks and any REPLICAOF it sends fail"
urgency: next         # rule 3 since 2026-09-27: severity medium, and the trigger (a password change) is a routine user action, live on every auth-enabled cluster; was icebox (rule 5)
effort: L
blocked-by: decision  # ADR 0016 D13, ADR 0030 D11, see Options
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

## Fact

**Verified** (read 2026-09-27):

- The auth Secret is watched: `findValkeyForSecret`
  ([`valkey_controller.go`](../../internal/controller/valkey_controller.go) line 3005) enqueues
  the Valkey resource when it changes. *(2026-09-27: the watch is registered at lines
  2996–2999, and `secretConcernsValkey`, lines 3050–3057, matches the auth Secret by name.)*
- The password reaches the pods as `env.valueFrom.secretKeyRef`, resolved when the container
  starts, and the pod-spec hash covers the reference, not the value
  ([ADR 0016](../adr/0016-authentication-and-tls-posture.md) D12). Nothing rolls.
  *(2026-09-27: the reference sites are listed in
  [ticket 050](050-every-component-authenticates-with-the-one-cluster-password.md), Fact.)*
- [ADR 0016](../adr/0016-authentication-and-tls-posture.md) D13 makes rotation a documented
  manual procedure: change the Secret, roll the pods yourself, replicas first, master last. Its
  steps 2 and 3 were derived by reading, not reproduced against a cluster.
- [ADR 0030](../adr/0030-rotating-certificates-rotate-the-instances-that-cannot-reload-them.md)
  D11: a published digest of the password is a brute-forceable oracle at any digest strength,
  and the gap "must not be closed by copying this mechanism".
- The owner's wish, in [`.github/idea.md`](../../.github/idea.md): after the password Secret
  changes, the instances take the new password without losing their state, also without a
  persistent volume.

*Added 2026-09-27 (enrichment, at `4a7543e`):*

- **The operator switches to the new password at once.** `readValkeyPassword`
  (`valkey_controller.go` lines 177–190) reads the Secret on every call and returns `""` on a
  read error. The health checker does the same (`readAuthPassword`,
  [`checker.go`](../../internal/health/checker.go) line 75).
- **`masterauth` is the same value as `requirepass`.** `valkey-server` gets both from
  `$VALKEY_PASSWORD` ([`statefulset.go`](../../internal/builder/statefulset.go) line 830), so no
  pod started with the new password can replicate from one still running with the old.
  Sentinel writes `requirepass` and `sentinel auth-pass` into its config at init
  ([`sentinel.go`](../../internal/builder/sentinel.go) lines 180–187, `sed` at line 715).
- **The Sentinel half of option C already has plumbing.** The client has `SentinelSet`
  ([`internal/valkeyclient/client.go`](../../internal/valkeyclient/client.go) line 282). The
  operator already sends `SENTINEL SET <monitor> auth-pass <current Secret value>` at runtime,
  in `resetSentinelState` ([`rolling_update.go`](../../internal/controller/rolling_update.go)
  line 3614; password read at line 3644, set at line 3689). That is a best-effort step of the
  rolling-update failover retry (callers at lines 957, 1006, 1048, 3158, 3305). The client has
  no `CONFIG SET`, `ACL` or ~~generic command method~~ exported generic command method (its
  methods, `client.go` lines 197–429), so the data half of C needs new client methods.
  *(precised 2026-09-27, review: the unexported `exec(args ...string)` at line 429 is generic,
  so each new method is a thin wrapper, as `SentinelSet` is at line 283.)*
- No auth failure is handled as such: a grep for `WRONGPASS` and `NOAUTH` over `internal` and
  `cmd`, outside tests, finds only the init script's guard (`statefulset.go` lines 300–302).
- The operations and security pages were corrected on 2026-09-27: without persistence, the
  manual procedure loses the dataset at any replica count
  ([`rotation-and-change-propagation.md`](../security/rotation-and-change-propagation.md)
  lines 42–46, [`authentication.md`](../operations/authentication.md#changing-the-password)).

**Not verified:**

- Valkey's multi-password semantics on both pinned lines: that `ACL SETUSER default >new`
  keeps the old password valid, and that `CONFIG SET requirepass` replaces all of them.
- Whether `CONFIG SET masterauth` reaches an established replication link or only the next
  reconnect, and Sentinel's runtime equivalents (`SENTINEL SET <master> auth-pass`,
  `requirepass` on Sentinel).
- Whether kubelet resolves a `secretKeyRef` again when a container restarts inside the same pod.
- *(added 2026-09-27)* What a reconcile pass does when every pod answers `WRONGPASS`. Also
  whether a failover retry during the manual procedure really switches the Sentinels'
  `auth-pass` to the new password while the data pods still require the old one. The code
  reads that way (above), but it was not measured.

## Impact

Every auth-enabled cluster, whenever its password changes. Between the Secret change and the
last manual pod restart the operator cannot authenticate to the pods that still run with the
old password, so its health checks fail and it cannot send `REPLICAOF`. A cluster without
persistence ~~and without a failover target~~ loses its data on the manual roll. *(corrected
2026-09-27, as on the rotation page: at any replica count, because `masterauth` equals
`requirepass` and no replacement can sync from a pod still on the old password.)*

## Options

The decision is whether to reopen ADR 0016 D13, and by which mechanism. ADR 0030 D11's fence
is not reopened by any option below.

- **A — keep the manual procedure** (D13). Costs nothing; the Impact above stays.
- **B — roll on a record that is not derived from the content:** the auth Secret's
  `metadata.resourceVersion` on the pod template, the change riding the failover-aware rolling
  update. It publishes nothing about the value, so D11 does not apply. Any write to the Secret —
  a label, an annotation — rolls the tier; a non-persistent single pod loses its data; and the
  roll stalls midway, because a replacement that boots with the new password as `masterauth`
  cannot authenticate to a master that still requires the old one, so the synced check
  ([ADR 0007](../adr/0007-failover-aware-rolling-update.md) D10) pauses the roll. B needs a
  window in which both passwords are accepted — C's mechanism.
- **C — rotation at runtime, no restart (recommended).** The user writes the new password next
  to the old one (a second key, name to be decided; the user keeps owning both, so D1 holds).
  The operator adds the new password to the default user on every data and Sentinel pod,
  switches `masterauth` and Sentinel's `auth-pass` at runtime, switches its own client, removes
  the old password and reports completion on the resource. A pod restarted later reads the new
  value from the unchanged `secretKeyRef`. *(added 2026-09-27)* The operator's own client has
  to switch last, after every pod accepts the new password. Today `readValkeyPassword`
  switches it the moment the Secret changes (Fact), so C needs the second key to tell "old" from
  "new".
- **D — a digest of the password on the pod template, like the TLS fingerprint.** Refused by
  ADR 0030 D11 at any digest strength; listed so that it is not proposed again.

C is marked because it is the only option that meets the owner's wish (no state loss without a
persistent volume), publishes nothing, and avoids B's authentication split in the middle of a
roll. It costs the most: new API surface, runtime ACL and `CONFIG` commands on every pod, each
wait bounded ([ADR 0010](../adr/0010-every-rolling-update-wait-is-bounded.md)), and every item
under *Not verified* has to hold on both pinned lines before it is designed.

## Work list

**Not waiting on a decision:** no XS item. The first step is decision-independent and
S-sized: a docker spike on Valkey 8 and 9 (image-tools style, no Kind). It checks `ACL SETUSER
default >new` next to the old password, `CONFIG SET masterauth` on a live link,
`SENTINEL SET <monitor> auth-pass`, and Sentinel `requirepass` at runtime. Its answer decides
whether C is buildable at all, so run it before the decision is put to Hans.

**Waiting on the decision (C):**

1. The second key in the CRD, and `make generate-all`.
2. New client methods (`CONFIG SET`, `ACL SETUSER`).
3. A bounded rotation state ([ADR 0010](../adr/0010-every-rolling-update-wait-is-bounded.md)).
4. The operator client switching last.
5. A completion condition (a `conditionRegistry` row,
   [ADR 0027](../adr/0027-conditions-are-levels-edges-or-history.md)).
6. e2e on both lines with a revert check.

**Close (ADR 0034):** amend ADR 0016 D13 or write a new ADR. Then the rotation page, H-24,
[`authentication.md`](../operations/authentication.md#changing-the-password) and the README
CRD reference. `git grep -n 'T51\|051-a-changed'` outside `docs/tickets/` (none today), then
move to `archive/`.

## Decision

None yet.

## Verification

- e2e on both pinned lines: write keys, rotate the Secret, then every data and Sentinel pod
  accepts the new password and refuses the old one, the keys are still there on a
  non-persistent cluster, and a write on the master reaches the replicas.
- Revert check: with the runtime switch removed, the same test fails on the old password still
  being accepted.

## History

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
