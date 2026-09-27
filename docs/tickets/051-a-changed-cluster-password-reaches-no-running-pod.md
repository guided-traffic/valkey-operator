---
id: T51
title: a changed cluster password reaches no running pod
state: filed
severity: medium      # rotating means a manual roll of every pod; a non-persistent cluster without a failover target loses its data on it
security: hardening
threat: "would additionally cover a leaked cluster password: today revoking it takes a manual roll of every pod, and until that roll the operator authenticates with the new password against pods that accept only the old one, so its health checks and any REPLICAOF it sends fail"
urgency: icebox       # rule 5: reopens ADR 0016 D13, and ADR 0030 D11 fences the obvious mechanism
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
  the Valkey resource when it changes.
- The password reaches the pods as `env.valueFrom.secretKeyRef`, resolved when the container
  starts, and the pod-spec hash covers the reference, not the value
  ([ADR 0016](../adr/0016-authentication-and-tls-posture.md) D12). Nothing rolls.
- [ADR 0016](../adr/0016-authentication-and-tls-posture.md) D13 makes rotation a documented
  manual procedure: change the Secret, roll the pods yourself, replicas first, master last. Its
  steps 2 and 3 were derived by reading, not reproduced against a cluster.
- [ADR 0030](../adr/0030-rotating-certificates-rotate-the-instances-that-cannot-reload-them.md)
  D11: a published digest of the password is a brute-forceable oracle at any digest strength,
  and the gap "must not be closed by copying this mechanism".
- The owner's wish, in [`.github/idea.md`](../../.github/idea.md): after the password Secret
  changes, the instances take the new password without losing their state, also without a
  persistent volume.

**Not verified:**

- Valkey's multi-password semantics on both pinned lines: that `ACL SETUSER default >new`
  keeps the old password valid, and that `CONFIG SET requirepass` replaces all of them.
- Whether `CONFIG SET masterauth` reaches an established replication link or only the next
  reconnect, and Sentinel's runtime equivalents (`SENTINEL SET <master> auth-pass`,
  `requirepass` on Sentinel).
- Whether kubelet resolves a `secretKeyRef` again when a container restarts inside the same pod.

## Impact

Every auth-enabled cluster, whenever its password changes. Between the Secret change and the
last manual pod restart the operator cannot authenticate to the pods that still run with the
old password, so its health checks fail and it cannot send `REPLICAOF`. A cluster without
persistence and without a failover target loses its data on the manual roll.

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
- **C — rotation at runtime, no restart (best).** The user writes the new password next to the
  old one (a second key, name to be decided; the user keeps owning both, so D1 holds). The
  operator adds the new password to the default user on every data and Sentinel pod, switches
  `masterauth` and Sentinel's `auth-pass` at runtime, switches its own client, removes the old
  password and reports completion on the resource. A pod restarted later reads the new value
  from the unchanged `secretKeyRef`.
- **D — a digest of the password on the pod template, like the TLS fingerprint.** Refused by
  ADR 0030 D11 at any digest strength; listed so that it is not proposed again.

C is marked because it is the only option that meets the owner's wish (no state loss without a
persistent volume), publishes nothing, and avoids B's authentication split in the middle of a
roll. It costs the most: new API surface, runtime ACL and `CONFIG` commands on every pod, each
wait bounded ([ADR 0010](../adr/0010-every-rolling-update-wait-is-bounded.md)), and every item
under *Not verified* has to hold on both pinned lines before it is designed.

## Decision

None yet.

## Verification

- e2e on both pinned lines: write keys, rotate the Secret, then every data and Sentinel pod
  accepts the new password and refuses the old one, the keys are still there on a
  non-persistent cluster, and a write on the master reaches the replicas.
- Revert check: with the runtime switch removed, the same test fails on the old password still
  being accepted.

## History

- 2026-09-27 — filed from the row "Password rotation" of archive/031, which names ADR 0030 D11.
  Gap [H-24](../security/rotation-and-change-propagation.md#h-24) states what is missing and
  what to do in the meantime.
