# Ticket: the integration tier writes no Valkey values — decide whether it should

Ticket 041, formerly C2 (`local_integration_tier_writes_no_valkey.md`); renamed on 2026-09-27 when
the tickets were numbered.

> **Status: documentation half DONE, the deliverable this ticket names is OPEN. Verified
> 2026-08-26 on `HEAD` = `1c309d8`.** Index:
> [`archive/039-findings-from-the-1-11-0-fleet-rollout.md`](archive/039-findings-from-the-1-11-0-fleet-rollout.md) (archived 2026-09-27, no longer maintained). Keep this line current — update it
> in the same change that touches this ticket.
>
> **Done:** `CLAUDE.md` no longer carries the wrong rule. Its Testing section now says
> envtest "starts a kube-apiserver and etcd and **no kubelet**, so no pod runs there and
> nothing in this tier opens a Valkey connection", and attributes write-and-verify-replication
> to E2E, "the only one that can".
> [ADR 0017](../adr/0017-test-and-ci-policy.md) D2 `:82-90` states the same three-tier split
> verbatim.
>
> **Open, and it is precisely the thing this ticket exists for.** Its closing paragraph names
> the deliverable: *"if it is Option A, the outcome is a line in ADR 0017 recording that the
> split was considered and kept deliberately, so the question is not reopened from scratch
> next time somebody reads the old requirement."* ADR 0017's Alternatives Considered
> (`:536-628`) holds 19 entries and **none of them is this one** — grep for
> `testcontainer|docker run|valkey pair|second integration` returns **0**. So the tree records
> the **fact** of the split and never the **decision** to keep it, which is exactly the
> reopening risk the ticket was written to close. One Alternatives entry ("A real Valkey pair
> in the integration tier") plus a Status date. **Effort: XS.**
>
> **Premise drift, minor and not load-bearing.** The Context below says the tier has seven
> files and that a `valkeyclient|go-redis|redis.NewClient` grep returns nothing. HEAD has
> **eleven** files (added: `foreign_object_test.go`, `metrics_test.go`,
> `reconcile_concurrency_test.go`, `volumeclaim_conflict_test.go`) and the grep returns
> **one** hit,
> [`test/integration/reconcile_concurrency_test.go:20`](../../test/integration/reconcile_concurrency_test.go#L20).
> That hit is a **false positive**: the import is only a type in a stub signature
> (`GetReplicationInfo` on a fake that sleeps and returns `context.DeadlineExceeded`), and no
> connection is opened. The substantive claim survives intact; only the grep no longer proves
> it, so do not re-run it as evidence without reading the hit.

## Context

`CLAUDE.md` carried this rule for the integration tier:

> **Integration tests**: Must write actual values to Valkey and verify replication to replicas

That is not what `test/integration/` does, and it is not what it can do in its current shape.

Verified 2026-08-21 on branch `feat/support-pdb`:

* `test/integration/suite_test.go` starts `envtest.Environment{CRDDirectoryPaths: ...}` and
  nothing else. envtest brings up a **kube-apiserver and etcd, and no kubelet** — so no pod
  ever runs, and no Valkey process exists to talk to.
* `grep -rln 'valkeyclient\|go-redis\|redis.NewClient' test/integration/` returns **nothing**.
  Not one file in the tier opens a Valkey connection.
* The seven files it contains — `affinity_test.go`, `integration_test.go`, `observer_test.go`,
  `pdb_test.go`, `pdb_uid_precondition_test.go`, `sidecar_services_test.go`, `suite_test.go` —
  assert CRD defaulting, object shape, ownership, delete preconditions and controller-manager
  wiring. All of that is real API-server behaviour a fake client cannot produce, and all of it
  is worth having. None of it is a data-plane assertion.
* The tier that does write values and check replication is E2E: `valkeyMSET` and
  `waitForConnectedReplicas` in `test/e2e/`.

## What was already changed

Nothing in the test code. Two documents were made to describe the tree as it is, in the ADR
work of 2026-08-21:

* `CLAUDE.md` — the Testing section now describes envtest as what it is and attributes the
  "write actual values into Valkey and verify replication reaches the replicas" requirement to
  the E2E tier, "the only one that can".
* [`docs/adr/0017-test-and-ci-policy.md`](../adr/0017-test-and-ci-policy.md) D2 states the
  same three-tier split, with the envtest limitation spelled out.

So the documentation is no longer wrong. **The open question is whether the original rule was
an aspiration worth implementing, or a misfiling that is now correctly filed.**

## The decision

**Option A — accept the split as documented. Nothing to build.**

The requirement moves to E2E permanently, where it already holds. The argument for it: a
replication assertion needs two running Valkey servers and a network between them, which is
exactly what a Kind cluster provides and exactly what envtest refuses to. Adding a Valkey to
the integration tier would either mean running the servers outside the cluster the tests
manage (so the operator's own wiring is not what is under test), or reinventing a small part
of Kind.

Cost of A: the gap between "the operator created a replica ConfigMap naming pod-1" and "pod-0
actually replicates from pod-1" is covered only by the slowest tier. Every replication
regression costs a full e2e cycle to catch.

**Option B — give the integration tier a real Valkey pair.**

Not envtest — envtest cannot host one. It would be a second integration mode: two Valkey
containers started by the test (testcontainers, or `docker run` behind a build tag), the
operator's generated config rendered into them by the builder under test, then a write on the
master and a read on the replica.

What that buys: `internal/builder`'s config generation — `GenerateValkeyConf`, the `replicaof`
directive, the TLS directives, the `%VALKEY_PASSWORD%` substitution — would be verified against
a real `valkey-server` instead of against a golden string. That is the layer where a wrong
directive is currently invisible until e2e.

What it costs: a third fixture kind to maintain, a Docker dependency in a tier that has none
today, and a new build tag plus Makefile target. It does **not** cover the operator's
reconciliation of that config, because there are still no pods and no StatefulSet controller —
so it narrows the e2e gap rather than closing it.

## If Option B is chosen

Scope it to config-correctness, not to topology:

1. New build tag `//go:build valkey_integration`, separate from the existing `integration`
   tag, so `make test-integration` stays Docker-free.
2. New Makefile target `test-valkey-integration`, listed in the `CLAUDE.md` Makefile table —
   the Makefile is the only entry point ([ADR 0017](../adr/0017-test-and-ci-policy.md) D1).
3. Fixture: start two `valkey/valkey:8.0` containers, render the master and replica configs
   with the real builders, mount them, wait for `master_link_status:up` on the replica.
4. Assertions, minimum set: a `SET` on the master is readable on the replica; the replica
   reports `role:slave` with the master's address; with `spec.auth` set an unauthenticated
   client is refused; with TLS enabled the plaintext port is closed unless
   `allowUnencrypted`.
5. CI: its own job, not folded into the existing `integration` one — a Docker-dependent tier
   must be able to fail without reddening a tier that does not need Docker.
6. Every new test must be able to fail: mutate the `replicaof` directive out of the generated
   config and the replication assertion must break
   ([ADR 0017](../adr/0017-test-and-ci-policy.md) D7, D10).
7. Update [`docs/adr/0017-test-and-ci-policy.md`](../adr/0017-test-and-ci-policy.md) D2 in
   the same change — it currently says E2E is "the **only** tier that writes actual values into
   Valkey", and that sentence becomes false. Per the ADR rule, amend D2 in place and record the
   amendment in `Status` with its date; do not leave the old rule standing as current.
8. Update the `CLAUDE.md` Testing section to match.

## Verification

For Option A: nothing to run. Close the ticket with the decision recorded.

For Option B:

```bash
make test-valkey-integration    # green
make test-integration           # still green, still Docker-free
make test-unit                  # unchanged
make lint && make cyclo         # 0 issues, all functions below 15
```

Plus the mutation check of step 6, with its failure message recorded in the change.

## Recommendation

**Option A**, unless the config-generation layer starts producing regressions that e2e catches
late. The tier boundary is currently honest and the documents now say so; Option B adds a
Docker dependency and a fixture class to narrow a gap that has not yet cost anything
measurable. Revisit if a `GenerateValkeyConf` defect ever reaches a cluster.

The decision itself is the deliverable here — if it is Option A, the outcome is a line in
[ADR 0017](../adr/0017-test-and-ci-policy.md) recording that the split was considered and
kept deliberately, so the question is not reopened from scratch next time somebody reads the
old requirement.
