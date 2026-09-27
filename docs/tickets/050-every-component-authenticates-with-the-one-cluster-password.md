---
id: T50
title: every component authenticates with the one cluster password and full rights
state: filed
severity: medium      # a compromised exporter (third-party image) can flush or re-point the dataset of every auth-enabled cluster with metrics on
security: hardening
threat: "would additionally cover code execution in the exporter (a third-party image), the sidecar, the observer or a probe: today each authenticates as the default user with the one cluster password and may run FLUSHALL, CONFIG SET or REPLICAOF"
urgency: icebox       # rule 5: component credentials reopen ADR 0016 D1 and D2 (rule 3 does not match: the trigger, a component compromise, is dormant)
effort: L             # L for B or C; option D (exporter only) would be M
blocked-by: decision  # ADR 0016 D1, D2, see Options
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

**Verified** (read 2026-09-27):

- Every consumer receives the same password from the same `secretKeyRef`: `VALKEY_PASSWORD`
  for the `valkey` container, the init container, the `sidecar` and the observer,
  `REDIS_PASSWORD` for the exporter ([`statefulset.go`](../../internal/builder/statefulset.go)
  line 1078, [`observer.go`](../../internal/builder/observer.go) line 249;
  [ADR 0016](../adr/0016-authentication-and-tls-posture.md) D2). *(located 2026-09-27 at
  `4a7543e`: in `statefulset.go` at lines 381 and 566 (the two `init-config-selector`
  variants), 879 (`valkey`), 951 (`sidecar`) and 1078–1084 (exporter); in
  [`sentinel.go`](../../internal/builder/sentinel.go) at lines 356 and 563; in `observer.go` at
  lines 249–258.)*
- The exec probes authenticate the same way: `ProbeCommand` runs
  `valkey-cli ... -a "$VALKEY_PASSWORD" ping` (`statefulset.go` ~~lines 1512–1526~~
  *(corrected 2026-09-27: lines 1515–1545, the auth branch 1516–1531)*). *(added 2026-09-27)*
  It is the readiness and liveness probe of the `valkey` container (lines 847 and 859), so it
  runs inside the container that already holds the password in its environment (line 879).
- Neither config builder renders an ACL directive: a grep for `aclfile`, `user `,
  `masteruser` and `auth-user` over [`configmap.go`](../../internal/builder/configmap.go) and
  [`sentinel.go`](../../internal/builder/sentinel.go) finds nothing, so every client is the
  default user with every command. *(re-run 2026-09-27, case-insensitive, with `acl ` and
  `--user` added and `statefulset.go` included: no match.)*
- [ADR 0016](../adr/0016-authentication-and-tls-posture.md) D1: the operator never generates a
  credential — "no operator-owned password to rotate, to leak into status, or to orphan on CR
  deletion".

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
  ([`valkey_types.go`](../../api/v1/valkey_types.go) lines 1030–1032). The `sidecar` and the
  observer run the operator image (`statefulset.go` lines 989–996, `observer.go` line 82). The
  exporter runs `spec.metrics.image`, default `DefaultMetricsExporterImage`
  (`valkey_types.go` line 643). **The exporter is the only third-party code among them.**

**Not verified:**

- The command set each component needs: the exporter's; the sidecar's, which include
  `REPLICAOF` for the drain promotion
  ([ADR 0012](../adr/0012-the-sidecar-records-its-drain-promotion-on-the-pod.md)); the
  observer's; the probes' `PING`.
- That `masteruser` for replication and Sentinel's `auth-user` behave the same on both pinned
  Valkey lines.
- How a component password would reach running pods when it changes; the cluster password has
  no such path ([ticket 051](051-a-changed-cluster-password-reaches-no-running-pod.md)).
- *(added 2026-09-27)* That `valkey-server` accepts a `user` directive on its command line
  (`--user <name> on >… <rules>`) on both pinned lines. Also whether redis_exporter v1.66.0
  takes a user name from its environment.

## Impact

Dormant until a component is compromised; then total. The exporter is third-party code that
runs in every data pod of a metrics-enabled cluster and, with auth on, holds the cluster
password. Whoever controls it may flush the dataset, rewrite the config at runtime or re-point
replication. What an operator can do today: keep the exporter image pinned by digest (the
default is, [ADR 0033](../adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md)
D5) and leave metrics off where they are not needed.

*(added 2026-09-27, per component)* Least-privilege users buy something only where the
component does not already hold more:

- **The probes and the init containers run beside the `valkey` container, which holds the
  default password.** A probe user would narrow nothing.
- **The `sidecar` and the observer run the operator image.** A compromise of that image is a
  compromise of the operator, which is cluster-admin equivalent
  ([ADR 0013](../adr/0013-operator-is-cluster-wide-privileged.md) D1). The sidecar also keeps
  `REPLICAOF` in any command set.
- **The exporter is the one case where a user of its own removes rights nobody else in that
  process holds.**

## Options

The decision is who owns the component credentials. Both ways of having them reopen
[ADR 0016](../adr/0016-authentication-and-tls-posture.md): D2 (one `secretKeyRef` for every
consumer) in either case, D1 in option B.

- **A — keep one password** (D2 stands). Costs nothing; every component keeps full rights.
- **B — operator-generated component credentials.** ~~(best)~~ Behind a new CRD field,
  default off ([ADR 0005](../adr/0005-upgrade-neutral-defaults-and-anti-affinity.md) D1), the
  operator creates one Secret per Valkey resource with a password per component user, owned by
  that resource ([ADR 0020](../adr/0020-write-only-what-the-operator-owns.md)), renders the users
  with their command sets, and gives each container its own `secretKeyRef`. The user-owned
  cluster password stays the credential of clients and of the default user. D1's three costs
  become the operator's, for internal credentials only: the Secret never goes into status,
  garbage collection removes it with the resource, and rotating it meets the propagation gap of
  ticket 051.
- **C — user-provided component Secrets.** New CRD fields, one Secret per component. D1 holds;
  every CR author has to create and rotate four more credentials to get the benefit.
- *(added 2026-09-27)* **D — a least-privilege user for the exporter alone, from a
  user-provided Secret (recommended).**
  - What it is: an optional field under `spec.metrics` (name to be decided), default off. When
    it is set, the exporter authenticates as that user from its own `secretKeyRef`.
    `valkey-server` declares the user on the command line it already builds (`statefulset.go`
    line 830), so no credential reaches a ConfigMap (ADR 0016 D3).
  - What it reopens: D1 holds, because the user owns the Secret. D2 is reopened for one
    consumer only.
  - Cost M: one field, one env var on the `valkey` container and one on the exporter, a command
    set for one third-party program, and a docker spike on both lines. The command set moves
    only when the exporter pin does, and that pin is maintained by hand
    ([ticket 054](054-renovate-does-not-track-the-default-exporter-image.md)).
  - What it leaves open: a changed exporter password reaches no running pod either (ticket
    051).

~~B is marked because least privilege that needs four extra Secrets per resource would be set by
almost nobody, so under C the exporter would keep full rights in practice; under B it is one
field. Its cost is exactly the D1 re-decision.~~ *(superseded as the recommendation 2026-09-27)*

D is marked because it spends the ACL work on the one component where it removes rights (Impact,
per case). Under B and C, users for the probes, the sidecar and the observer narrow little and
each add a command set to maintain. The argument that lost C ("four more Secrets nobody sets")
does not hold for a single Secret, so D keeps D1 intact. B stays the option if Hans wants every
component covered. Its argument over C still holds there.

## Work list

**Not waiting on a decision:** no XS item. One S item informs the decision and is needed for
any of B, C or D. It is a docker spike on Valkey 8 and 9 (image-tools style, no Kind):

- `--user` on the `valkey-server` command line;
- the exporter's commands, read from `ACL LOG` under a deny-all user;
- `masteruser` and Sentinel `auth-user`.

**Waiting on the decision:**

1. The CRD field and `make generate-all`.
2. The builder wiring (`statefulset.go` lines 830 and 1078–1084 for D).
3. Unit tests that pin an unchanged pod template while the field is unset.
4. e2e on both lines.
5. The README CRD reference.

**Close (ADR 0034):** amend ADR 0016 (D2, and D1 under B), H-6 in
[`secrets-and-tls.md`](../security/secrets-and-tls.md) and the README CRD reference.
`git grep -n 'T50\|050-every-component'` outside `docs/tickets/` (none today), then move to
`archive/`.

## Decision

None yet.

## Verification

- e2e on both pinned lines, with the option on: each component user is refused `FLUSHALL`
  (for example through `ACL DRYRUN`), and the rolling-update, failover and drain suites stay
  green, which proves the sidecar's and the operator's commands still pass.
- A negative control: the default user is still allowed `FLUSHALL`
  ([ADR 0017](../adr/0017-test-and-ci-policy.md) D11).
- *(added 2026-09-27, option D)* The exporter's `/metrics` still reports
  `redis_up 1` with the option on.

## History

- 2026-09-27 — enriched - located every `secretKeyRef`, corrected the `ProbeCommand` lines, and
  added which image each consumer runs. Added option D (exporter-only user,
  user-provided Secret) and moved the recommendation from B to D, because a probe, sidecar or
  observer user narrows little. Urgency, effort and blocked-by unchanged.
- 2026-09-27 — filed from the row "Least-privilege Valkey ACL users for probes, sidecar,
  exporter, observer" of archive/031. That row names ADR 0016; the D1 conflict in option B was
  found while filing. Gap [H-6](../security/secrets-and-tls.md#h-6) states what is missing; its
  "open follow-up" lead-in was removed from the page in the same change.
