# Ticket: the integration tier writes no Valkey values — decide whether it should

Ticket 041, formerly C2 (`local_integration_tier_writes_no_valkey.md`); renamed on 2026-09-27 when
the tickets were numbered.

> **Status: documentation half DONE, the deliverable this ticket names is OPEN. Verified
> 2026-08-26 on `HEAD` = `1c309d8`.** *(Re-verified 2026-09-27 at `4a7543e`: still open, still
> waiting on Hans's choice between the Options below; the stale cites in this block are corrected
> in place. By the README's urgency rules this ticket would derive `later` — rule 4, a cheap
> known fix — and its effort stays XS; it keeps its frontmatter-less form.)* ~~Index:
> [`archive/039-findings-from-the-1-11-0-fleet-rollout.md`](archive/039-findings-from-the-1-11-0-fleet-rollout.md) (archived 2026-09-27, no longer maintained).~~
> *(Corrected 2026-09-27: there is no index any more; this blockquote is the ticket's state,
> [README](README.md#there-is-no-index-table-and-no-board).)* Keep this line current — update it
> in the same change that touches this ticket.
>
> **Done:** `CLAUDE.md` no longer carries the wrong rule. Its Testing section now says
> envtest "starts a kube-apiserver and etcd and **no kubelet**, so no pod runs there and
> nothing in this tier opens a Valkey connection", and attributes write-and-verify-replication
> to E2E, "the only one that can".
> [ADR 0017](../adr/0017-test-and-ci-policy.md) D2 ~~`:82-90`~~ *(corrected 2026-09-27:
> `:225-233`)* states the same three-tier split verbatim.
>
> **Open, and it is precisely the thing this ticket exists for.** Its closing paragraph names
> the deliverable: *"if it is Option A, the outcome is a line in ADR 0017 recording that the
> split was considered and kept deliberately, so the question is not reopened from scratch
> next time somebody reads the old requirement."* ADR 0017's Alternatives Considered
> (~~`:536-628`~~) holds ~~19~~ entries *(corrected 2026-09-27: `:1135-1264`, 25 entries; the
> grep below still returns 0)* and **none of them is this one** — grep for
> `testcontainer|docker run|valkey pair|second integration` returns **0**. So the tree records
> the **fact** of the split and never the **decision** to keep it, which is exactly the
> reopening risk the ticket was written to close. One Alternatives entry ("A real Valkey pair
> in the integration tier") plus a Status date. **Effort: XS.**
>
> **Premise drift, minor and not load-bearing.** The Context below says the tier has seven
> files and that a `valkeyclient|go-redis|redis.NewClient` grep returns nothing. HEAD has
> ~~**eleven**~~ *(corrected 2026-09-27: **fifteen**; also added since: `pod_hardening_test.go`,
> `pod_security_test.go`, `tls_material_test.go`, `token_projection_test.go`)* files (added:
> `foreign_object_test.go`, `metrics_test.go`,
> `reconcile_concurrency_test.go`, `volumeclaim_conflict_test.go`) and the grep returns
> **one** hit,
> [`test/integration/reconcile_concurrency_test.go:20`](../../test/integration/reconcile_concurrency_test.go#L20)
> *(still the only one on 2026-09-27; the type is used at `:99`)*.
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

What it costs: a third fixture kind to maintain, ~~a Docker dependency in a tier that has none
today, and a new build tag plus Makefile target~~ *(corrected 2026-09-27: a Docker tier exists
since ADR 0032 — `test/imagetools`, tag `imagetools`, `make test-image-tools` at
`Makefile:147-150`, the required `Valkey Image Tools` job — so a config check would be a new file
there, with no new tag, target or required context; see Options)*. It does **not** cover the operator's
reconciliation of that config, because there are still no pods and no StatefulSet controller —
so it narrows the e2e gap rather than closing it.

## Options

*(Added 2026-09-27; one decision, refreshed against `4a7543e`.)* The question is whether the
generated config gets a real-server check below e2e. Today every PR runs the generated config for
real in three e2e legs, and the one Docker tier, `test/imagetools`, runs `valkey-server` and
`valkey-sentinel` on a **hand-written** config
([`restricted_runtime_test.go:29-32`](../../test/imagetools/restricted_runtime_test.go)) against
the same two pins as e2e ([`test/testimages/images.go:40`, `:45`](../../test/testimages/images.go)).

| | What | Cost | Leaves open |
|---|---|---|---|
| **A (recommended)** | Keep the split and record it: one ADR 0017 Alternatives entry, "A real Valkey pair in the integration tier", with the revisit trigger, and a Status date | XS, docs only; no code, no CI change | A directive a Valkey release rejects is caught in e2e, not earlier |
| **B** | A Valkey pair with the generated master and replica configs, replication asserted (steps below) | S–M: a pair fixture in `test/imagetools`, not a new tier (cost basis corrected above); ADR 0017 D2 amended, because E2E stops being the only tier that writes values | The operator's reconciliation of that config: still no pods and no StatefulSet controller |
| **C** | One `valkey-server` booted on `GenerateValkeyConf` output ([`configmap.go:46`](../../internal/builder/configmap.go)) per pin in `test/imagetools`, no replication | S: one test file; the TLS variant needs a certificate fixture at `/tls` (`configmap.go:89-91`), the replica variant a master to name; D2 unchanged (no value is written) | Replication, auth (passed on the command line, `configmap.go:98-103`, not in the file) and TLS behaviour; only "the directive set boots" |

Why A: no revisit trigger has fired. `internal/builder/configmap.go`, which holds
`GenerateValkeyConf`, has not changed since `085ae23` (2026-03-27), before this ticket was
written (`git log -- internal/builder/configmap.go`). The closest case since, `1b1f6ed`
(ADR 0022), was a Sentinel identity defect that did reach a fleet, but it lives in the Sentinel
init script (`internal/builder/sentinel.go`) and shows only across pod replacements, so neither B
nor C would have caught it. And B or C would not add
coverage the required `E2E Tests` context lacks — the Renovate PR that brings a new Valkey pin
already runs the generated config in e2e; they would only fail it sooner. C is the cheapest
upgrade path if the trigger fires, and the Alternatives entry should say so.

*(Adversarial review 2026-09-27, a precision that strengthens A, not a change of the mark:)*
`GenerateValkeyConf` is not the whole file `valkey-server` reads. The data-tier init container
copies the ConfigMap file and appends `replica-announce-ip`/`replica-announce-port`
([`statefulset.go:352-356`](../../internal/builder/statefulset.go), `:540-544`), and chooses the
master or replica file per ordinal and known master (`:322`, `:345-349`); `#37` (`6a9c593`) was
exactly such an init-container directive. B and C as scoped above boot the ConfigMap content, so
they leave the init-container half open too — imagetools does not run the config-writer scripts
(`restricted_runtime_test.go:29-32`). `statefulset.go` did change since the ticket was written
(`86cf1f4`, `bb6c78f`, `b13377e`, read with `git log`); whether any of those touched the
config-writer lines was not traced, and no defect of them is known to have reached a cluster, so
the revisit trigger has not fired either way. If the Alternatives entry names C as the upgrade
path, it should name this gap with it.

**Verified 2026-09-27 at `4a7543e` (read):** ADR 0017 D2 at `:225-233`; Alternatives at
`:1135-1264`, 25 entries, and `grep -ciE 'testcontainer|docker run|valkey pair|second integration'`
over the ADR returns 0; `test/integration` holds 15 files and the connection grep still hits only
the stub type at `reconcile_concurrency_test.go:20`/`:99`; the imagetools tier as cited above
(its job `valkey-image-tools`, `.github/workflows/release.yml:625-658`, among the `needs:` of
`semantic-release` at `:1149`); the workflow runs on every `pull_request` to `main`
(`release.yml:3-9`), and a Renovate regex manager targets `test/testimages/images.go`
(`renovate.json:359-363`);
`internal/builder/configmap.go` last changed in `085ae23`; `git grep -nw -e T41 -e C2` outside
`docs/tickets/` returns nothing.

**Not verified:** whether `#31` (`f1108eb`) and `#37` (`6a9c593`), the March 2026 config fixes,
were found on a cluster or in e2e; that `Valkey Image Tools` is a required context today (rests on
ADR 0017 D47, branch protection not read); that the Renovate regex actually matches the pins;
the effort of B and C (estimates).

## Work list

*(Added 2026-09-27.)* **No XS item needs no decision**: the deliverable is the record of Hans's
choice, so every item waits on it.

- [ ] **Waits on the decision (XS under A):** ADR 0017 Alternatives entry "A real Valkey pair in
  the integration tier" — why it lost, that a check would go into `test/imagetools` (C first),
  and the revisit trigger — plus the ADR's Status amendment with its date.
- [ ] **Close ([ADR 0034](../adr/0034-tickets-are-work-lists-that-get-archived.md)):** the
  decision is the ADR entry itself; no README or `docs/operations/` consequence, and
  [`docs/developer/testing.md:15-16`](../developer/testing.md) already describes the split; the
  ADR index row ([`docs/adr/README.md:109`](../adr/README.md)) needs no change, because an
  Alternatives entry does not change the State text; `git grep -nw -e T41 -e C2` and the file name
  outside `docs/tickets/` (none on 2026-09-27); the Status blockquote says done with a "what
  shipped" line; `git mv` to [archive/](archive/).

## If Option B is chosen

*(2026-09-27: steps 1, 2 and 5 below are superseded by the corrected cost basis — the check would
be a file in `test/imagetools` under its existing tag, target and required job; steps 3, 4, 6, 7
and 8 still hold.)*

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
measurable. Revisit if a `GenerateValkeyConf` defect ever reaches a cluster. *(Corrected
2026-09-27: B no longer adds a Docker dependency — `test/imagetools` has one since ADR 0032 —
only the fixture class; the recommendation stands, reasoned under Options.)*

The decision itself is the deliverable here — if it is Option A, the outcome is a line in
[ADR 0017](../adr/0017-test-and-ci-policy.md) recording that the split was considered and
kept deliberately, so the question is not reopened from scratch next time somebody reads the
old requirement.

## History

- 2026-09-27: adversarial review of the enrichment - spot-checked the new cites (ADR 0017 D2,
  Alternatives count, 15 files, imagetools job and pins, `configmap.go`, `release.yml`,
  `renovate.json`): all hold. Added the precision under Options that the init container appends
  to the generated config, so B and C leave that half open; recommendation A unchanged.
- 2026-09-27: enriched - re-verified at `4a7543e`; stale cites corrected in place (D2 `:225-233`,
  Alternatives `:1135-1264` with 25 entries, 15 files, the retired index), Option B's cost basis
  corrected (the `test/imagetools` Docker tier exists), an Options section with a cheaper
  Option C and A recommended, a Work list with no XS no-decision item, and this History. Still
  open, waiting on Hans's choice; derived urgency `later`, effort XS.
- 2026-09-27 — renamed from `local_integration_tier_writes_no_valkey.md` (C2) to ticket 041 when
  the tickets were numbered.
- 2026-08-26 — status re-verified on `1c309d8`: documentation half done, the ADR 0017
  Alternatives entry open.
- 2026-08-21 — Context verified on `feat/support-pdb`, in the same ADR work that wrote the
  documentation half (`CLAUDE.md` Testing section, ADR 0017 D2); the exact filing date is not
  recorded in the file.
