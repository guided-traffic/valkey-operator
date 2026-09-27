---
id: T50
title: every component authenticates with the one cluster password and full rights
state: filed
severity: medium      # a compromised exporter (third-party image) can flush or re-point the dataset of every auth-enabled cluster with metrics on
security: hardening
threat: "would additionally cover code execution in the exporter (a third-party image), the sidecar, the observer or a probe: today each authenticates as the default user with the one cluster password and may run FLUSHALL, CONFIG SET or REPLICAOF"
urgency: icebox       # rule 5: component credentials reopen ADR 0016 D1 and D2
effort: L
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
  [ADR 0016](../adr/0016-authentication-and-tls-posture.md) D2).
- The exec probes authenticate the same way: `ProbeCommand` runs
  `valkey-cli ... -a "$VALKEY_PASSWORD" ping` (`statefulset.go` lines 1512–1526).
- Neither config builder renders an ACL directive: a grep for `aclfile`, `user `,
  `masteruser` and `auth-user` over [`configmap.go`](../../internal/builder/configmap.go) and
  [`sentinel.go`](../../internal/builder/sentinel.go) finds nothing, so every client is the
  default user with every command.
- [ADR 0016](../adr/0016-authentication-and-tls-posture.md) D1: the operator never generates a
  credential — "no operator-owned password to rotate, to leak into status, or to orphan on CR
  deletion".

**Not verified:**

- The command set each component needs: the exporter's; the sidecar's, which include
  `REPLICAOF` for the drain promotion
  ([ADR 0012](../adr/0012-the-sidecar-records-its-drain-promotion-on-the-pod.md)); the
  observer's; the probes' `PING`.
- That `masteruser` for replication and Sentinel's `auth-user` behave the same on both pinned
  Valkey lines.
- How a component password would reach running pods when it changes; the cluster password has
  no such path ([ticket 051](051-a-changed-cluster-password-reaches-no-running-pod.md)).

## Impact

Dormant until a component is compromised; then total. The exporter is third-party code that
runs in every data pod of a metrics-enabled cluster and, with auth on, holds the cluster
password. Whoever controls it may flush the dataset, rewrite the config at runtime or re-point
replication. What an operator can do today: keep the exporter image pinned by digest (the
default is, [ADR 0033](../adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md)
D5) and leave metrics off where they are not needed.

## Options

The decision is who owns the component credentials. Both ways of having them reopen
[ADR 0016](../adr/0016-authentication-and-tls-posture.md): D2 (one `secretKeyRef` for every
consumer) in either case, D1 in option B.

- **A — keep one password** (D2 stands). Costs nothing; every component keeps full rights.
- **B — operator-generated component credentials (best).** Behind a new CRD field, default off
  ([ADR 0005](../adr/0005-upgrade-neutral-defaults-and-anti-affinity.md) D1), the operator
  creates one Secret per Valkey resource with a password per component user, owned by that
  resource ([ADR 0020](../adr/0020-write-only-what-the-operator-owns.md)), renders the users with
  their command sets, and gives each container its own `secretKeyRef`. The user-owned cluster
  password stays the credential of clients and of the default user. D1's three costs become the
  operator's, for internal credentials only: the Secret never goes into status, garbage
  collection removes it with the resource, and rotating it meets the propagation gap of
  ticket 051.
- **C — user-provided component Secrets.** New CRD fields, one Secret per component. D1 holds;
  every CR author has to create and rotate four more credentials to get the benefit.

B is marked because least privilege that needs four extra Secrets per resource would be set by
almost nobody, so under C the exporter would keep full rights in practice; under B it is one
field. Its cost is exactly the D1 re-decision.

## Decision

None yet.

## Verification

- e2e on both pinned lines, with the option on: each component user is refused `FLUSHALL`
  (for example through `ACL DRYRUN`), and the rolling-update, failover and drain suites stay
  green, which proves the sidecar's and the operator's commands still pass.
- A negative control: the default user is still allowed `FLUSHALL`
  ([ADR 0017](../adr/0017-test-and-ci-policy.md) D11).

## History

- 2026-09-27 — filed from the row "Least-privilege Valkey ACL users for probes, sidecar,
  exporter, observer" of archive/031. That row names ADR 0016; the D1 conflict in option B was
  found while filing. Gap [H-6](../security/secrets-and-tls.md#h-6) states what is missing; its
  "open follow-up" lead-in was removed from the page in the same change.
