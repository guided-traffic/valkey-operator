# Ticket: the integration tier writes no Valkey values — decide whether it should

Ticket 041, formerly C2 (`local_integration_tier_writes_no_valkey.md`); renamed on 2026-09-27 when
the tickets were numbered.

> **Status: documentation half DONE, the deliverable this ticket names is OPEN. Verified
> 2026-08-26 on `HEAD` = `1c309d8`.** *(Re-verified 2026-09-27 at `4a7543e`: still open, still
> waiting on Hans's choice between the Options below; the stale cites in this block are corrected
> in place. By the README's urgency rules this ticket would derive `later` — rule 4, a cheap
> known fix — and its effort stays XS; it keeps its frontmatter-less form.)* *(Re-verified
> 2026-09-27 at `84a39c2`: state **analysed** in substance — every load-bearing claim is verified
> or marked not verified and not load-bearing, and the option set is complete (Option A
> recommended, Option C-prime the runner-up; B and C removed, see History). Severity low, security
> none, urgency `later` by rule 4, effort XS under A, blocked by the decision. Still waiting on
> Hans's choice.)* ~~Index:
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
> `:225-233`)* ~~states the same three-tier split verbatim.~~ *(corrected 2026-09-27 at
> `84a39c2`: states the same three-tier split in its own words, not verbatim; its exclusive
> clause for E2E is a conjunction, "the **only** tier that writes actual values into Valkey and
> verifies replication reaches the replicas",
> [`docs/adr/0017-test-and-ci-policy.md:228-230`](../adr/0017-test-and-ci-policy.md#L228-L230).)*
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
> in the integration tier") plus a Status date. **Effort: XS.** *(Precision 2026-09-27 at
> `84a39c2`: part of the record exists already, outside Alternatives — the ADR 0017 Residual
> risks record that executing the init container scripts inside the pinned images was deferred
> and is still not done, and that only the two pinned images are checked
> ([`docs/adr/0017-test-and-ci-policy.md:1271-1282`](../adr/0017-test-and-ci-policy.md#L1271-L1282)).
> The new entry cross-references those two bullets instead of restating them; see Options.)*
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
> ~~(`GetReplicationInfo` on a fake that sleeps and returns `context.DeadlineExceeded`), and no
> connection is opened.~~ *(corrected 2026-09-27 at `84a39c2`: the stub sleeps and returns
> `context.DeadlineExceeded` only for the CR it stalls; for every other CR it delegates to the
> real `health.Checker`
> ([`test/integration/suite_test.go:96`](../../test/integration/suite_test.go#L96),
> [`test/integration/reconcile_concurrency_test.go:88-101`](../../test/integration/reconcile_concurrency_test.go#L88-L101)).
> The real checker dials `<pod>.<headless>.<ns>.svc.cluster.local` only once the reconciler
> reaches `CheckCluster`, which needs every StatefulSet reported ready
> ([`internal/controller/valkey_controller.go:2457-2461`](../../internal/controller/valkey_controller.go#L2457-L2461));
> envtest runs no StatefulSet controller, and the one test that fakes readiness does it for the
> stalled CR
> ([`test/integration/reconcile_concurrency_test.go:145-154`](../../test/integration/reconcile_concurrency_test.go#L145-L154)).
> So no Valkey connection can open — no server exists and the name resolves nowhere — but that
> no dial is ever attempted is not guaranteed.)*
> The substantive claim survives intact; only the grep no longer proves
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

## Fact

*(Re-verified 2026-09-27 at `84a39c2`; everything below was read or measured on that date.)*

The question is no longer "does envtest write values" — it cannot — but whether the **generated
Valkey configuration** gets a real-server check below e2e, and whether the decision to keep it
in e2e is recorded so it is not reopened.

**Where the file `valkey-server` reads comes from.** The ConfigMap text is generated by
`GenerateValkeyConf`
([`internal/builder/configmap.go:46`](../../internal/builder/configmap.go#L46)); the replica
variant always carries `replicaof` when Sentinel is enabled or the cluster is multi-replica
without Sentinel, naming pod-0 (`MasterAddress`,
[`configmap.go:33-39`](../../internal/builder/configmap.go#L33-L39)) or the known master
([`configmap.go:164-172`](../../internal/builder/configmap.go#L164-L172)). The data init
container `init-config-selector` does not use that text verbatim on multi-replica pods:

* **Sentinel branch**
  ([`internal/builder/statefulset.go:275-356`](../../internal/builder/statefulset.go#L275-L356)):
  Phase 1 asks the Sentinels for up to 30 s (`MAX_WAIT=30`, `:291`); Phase 2 takes the
  `replicaof` of the mounted replica ConfigMap (`:318-327`), which on a Sentinel cluster always
  exists; then pod-0 (or whichever pod the address names) copies the **master** file and every
  other pod copies the master file and **appends** `replicaof $MASTER_ADDR` (`:329-340`). Phase 3,
  the ordinal fallback (`:341-350`: the master file on ordinal 0, the replica file on every other
  ordinal), is reachable only when the replica file is missing or has no `replicaof` line — by
  reading, not measured in a pod.
* **Non-Sentinel branch**
  ([`statefulset.go:440-544`](../../internal/builder/statefulset.go#L440-L544)): Phase 1 asks the
  peers for up to 15 s (`MAX_WAIT=15`, `:454`) and accepts a peer answering `role:master` with
  `connected_slaves` above 0; Phase 2 (`:490-519`) reads the replica file's `replicaof` and
  self-claims when it names this pod, or accepts that peer when it answers `role:master`. A
  master found either way is written as an appended `replicaof` (`:522-527`); otherwise the
  self-claim copies the master file (`:528-530`), or the ordinal fallback copies the master file
  on ordinal 0 and the replica file on every other ordinal (`:531-536`).
* **Both branches** append `replica-announce-ip`/`replica-announce-port`
  (`:352-356`, `:540-544`); `#37` (`6a9c593`, 2026-03-18) introduced exactly those lines.
* **Auth never enters the file.** The data tier passes the password on the command line,
  `exec valkey-server … --requirepass "$VALKEY_PASSWORD" --masterauth …`
  ([`statefulset.go:822-834`](../../internal/builder/statefulset.go#L822-L834)); the ConfigMap
  emits only a comment ([`configmap.go:98-104`](../../internal/builder/configmap.go#L98-L104)).
  The `%VALKEY_PASSWORD%` placeholder exists only in the Sentinel config
  (`internal/builder/sentinel.go:180`, `:186-187`, substituted at `:715`).

**What runs that file, and where.**

| Tier | Runs a real `valkey-server`? | On which config | Writes values? | Verifies replication? |
|---|---|---|---|---|
| Unit | no | executes the non-Sentinel data init ([`internal/builder/init_script_exec_test.go:96-119`](../../internal/builder/init_script_exec_test.go#L96-L119)) and the Sentinel pod init (`sentinel_init_script_exec_test.go:84-89`) on the host shell with stub `valkey-cli`/`timeout` (ADR 0017 D19); `GenerateValkeyConf` is asserted by substring (`configmap_test.go`, no golden file) | no | no |
| Integration (envtest) | no — no kubelet, no StatefulSet controller ([`test/integration/suite_test.go:63-65`](../../test/integration/suite_test.go#L63-L65)) | none | no | no |
| Imagetools (docker) | yes, on both pins | a **hand-written** flag set, not the generated config ([`test/imagetools/restricted_runtime_test.go:29-32`](../../test/imagetools/restricted_runtime_test.go#L29-L32), `:108`) | **yes** — `valkey-cli set k v` ([`restricted_runtime_test.go:111`](../../test/imagetools/restricted_runtime_test.go#L111)), `set after-rewrite 1` (`:122`), asserted at `:142` | no |
| E2E | yes, on both pins, every PR | the generated config plus the init-container appends, in real pods | yes (`valkeyMSET`, [`test/e2e/e2e_test.go:273`](../../test/e2e/e2e_test.go#L273)) | yes (`waitForConnectedReplicas`, [`e2e_test.go:491`](../../test/e2e/e2e_test.go#L491)) |

So E2E's exclusive property under ADR 0017 D2 is **replication verification**, not writing
values: imagetools has written values into a real single `valkey-server` since `bb6c78f`. No test
**below e2e** executes the Sentinel-branch data init (`statefulset.go:275-356`); e2e executes it
in the real image on every Sentinel-enabled cluster it creates, on all three legs.

**The pins do not move by themselves.** Both images live only in
[`test/testimages/images.go:40`, `:45`](../../test/testimages/images.go#L40-L45) (`9.1.1`,
`8.1.9`). A Renovate custom regex manager targets that file
([`renovate.json:358-369`](../../renovate.json#L358-L369)) and its regex matches both pins, but
Renovate never processes the file: `renovate.json` extends `config:recommended`, which extends
`:ignoreModulesAndTests`, whose `ignorePaths` contain `**/test/**`, and `renovate.json` sets no
`ignorePaths` of its own. `"ignoreTests": false` at
[`renovate.json:18`](../../renovate.json#L18) is not a path override — it only stops Renovate
from automerging without passing status checks. `valkey/valkey:9.1.2` and `8.1.10` were present
upstream on 2026-09-27 with no Renovate PR. This is a finding of the T45 family
([045](045-ci-kubernetes-and-cert-manager-pins-have-no-renovate-manager.md)), recorded here only
because this ticket's earlier argument for A rested on that Renovate PR (see History).

**A rejected directive is fatal at boot**, measured below; in e2e it shows up as a pod that never
becomes Ready, on the PR that introduces it.

**Verified (2026-09-27 at `84a39c2`):**

* The deliverable is still absent: ADR 0017 Alternatives Considered spans `:1135-1264`
  (`## Residual risks` at `:1265`) with 25 `###` entries
  (`awk 'NR>1135 && NR<1265 && /^### /' docs/adr/0017-test-and-ci-policy.md | wc -l` = 25), and
  `grep -ciE 'testcontainer|docker run|valkey pair|second integration'` over the ADR = 0. ADR 0017
  is unchanged since `4a7543e` (`git log -- docs/adr/0017-test-and-ci-policy.md`).
* The Residual risks already record the D42 deferral of running the init container scripts
  inside the images (struck through and superseded in part by D53, which still leaves "the two
  original init container scripts, the auth-wrapped container command and the auth and TLS probe
  variants" unrun) and "Only the two pinned images are checked (D42)"
  ([`docs/adr/0017-test-and-ci-policy.md:1271-1282`](../adr/0017-test-and-ci-policy.md#L1271-L1282)).
  The ticket's grep missed them because it searched only for `testcontainer|docker run|valkey
  pair|second integration`.
* ADR 0017 D2 at `:225-233`, its exclusive clause at `:228-230` as quoted in the Status block;
  [`docs/developer/testing.md:15-16`](../developer/testing.md#L15-L16) carries the same
  conjunction. Three tracked statements say the config writers are not run in imagetools:
  `CLAUDE.md` ("The Valkey image is a dependency" paragraph, "the config-writer scripts are not
  run there"), [`docs/developer/testing.md:157`](../developer/testing.md#L157) and the ADR 0017
  residual risk above.
* `test/integration` holds 15 files (`ls test/integration | wc -l`); the Valkey-client grep hits
  only `reconcile_concurrency_test.go:20` (import) and `:99` (stub return type); no connection
  can open (Status block).
* The imagetools job `valkey-image-tools` is at
  [`.github/workflows/release.yml:624-657`](../../.github/workflows/release.yml#L624-L657) and is
  among the `needs:` of `semantic-release` at `:1148`; the target is `make test-image-tools`
  ([`Makefile:147-150`](../../Makefile#L147-L150), `-tags=imagetools`); the fixtures iterate
  `pinnedImages()`
  ([`test/imagetools/image_tools_test.go:76-81`](../../test/imagetools/image_tools_test.go#L76-L81)).
  The workflow runs on every `pull_request` to `main`, with no paths filter (`release.yml:3-10`);
  the E2E matrix (`release.yml:27-60`) boots generated configs on all three legs behind the gate
  "E2E Tests" (`release.yml:486-497`).
* `Valkey Image Tools` and `E2E Tests` are required status checks on `main`: the GitHub rules API
  for the default branch (`gh api repos/guided-traffic/valkey-operator/rules/branches/main`, read
  2026-09-27) lists the twelve ADR 0017 D47 contexts, both of these among them.
* `internal/builder/configmap.go` last changed in `085ae23` (2026-03-27). Of the
  `internal/builder/statefulset.go` commits since the ticket, only `2357946` (2026-08-21, the
  self-claim branch, covered by unit exec tests at `init_script_exec_test.go:175`, `:189`)
  changed a config-writer script; `86cf1f4`, `bb6c78f`, `b13377e`, `bb0f127` did not, and
  `6140386` changed one shell comment ("(NA35)" to "(ADR 0008 D8, D9)"). Command: for each commit
  of `git log --since=2026-08-20 -- internal/builder/statefulset.go`, `git show <c> --
  internal/builder/statefulset.go | grep -E '^[+-]' | grep -E
  'replicaof|announce|cp %|MASTER_ADDR|KNOWN_MASTER|ORDINAL|>> %|SELF_IS'`.
* `#31` (`f1108eb`, 2026-03-13): "All pods in a non-Sentinel multi-replica deployment started as
  master because the replicaof directive and init container were only created when Sentinel was
  enabled … Update E2E tests to verify replication behavior." Non-Sentinel replication has been
  asserted in e2e since then:
  [`test/e2e/rolling_update_test.go:97`](../../test/e2e/rolling_update_test.go#L97)
  (`TestE2E_RollingUpdate_MultiReplicaNoSentinel`), `two_replica_failover_test.go:32`,
  `splitbrain_test.go:38`.
* `#37` (`6a9c593`) needed Sentinel, a failover and a TLS client to show (commit message);
  `1b1f6ed` (ADR 0022) lives in the Sentinel init script and showed only across pod replacements,
  on all seven Sentinel clusters of the 1.11.0 rollout
  ([`docs/adr/0022-sentinel-identity-is-pinned-to-the-pod.md:40-42`](../adr/0022-sentinel-identity-is-pinned-to-the-pod.md#L40-L42)).
* E2E exercises persistence `rdb` (the CRD default), `aof` (`test/e2e/pod_security_test.go:125`,
  `pod_hardening_test.go:214`), non-persistent clusters and TLS; **no test in any tier uses
  persistence mode `both`** (`grep -rn 'PersistenceModeBoth\|"both"' test/` prints nothing),
  whose config carries `dir /data` twice
  ([`configmap.go:203-243`](../../internal/builder/configmap.go#L203-L243)). It boots (measured
  below).
* No tracked file outside `docs/tickets/` cites this ticket
  (`git grep -nw -e T41 -e C2 -- ':!docs/tickets'` and the slug: nothing); inside,
  `044` links it.

**Measured 2026-09-27 (docker, local images only, every `vko-verify-*` container and network
removed afterwards).** All runs under the imagetools posture: `--user 999:999 --read-only
--cap-drop ALL --security-opt no-new-privileges --tmpfs /data:rw,uid=999,gid=999,mode=0755`, the
config passed in an env var and written to `/data/valkey.conf` by `sh -c 'printf "%s\n" "$CONF" >
/data/valkey.conf && exec valkey-server /data/valkey.conf'`. The configs were **transcribed by
hand** from `configmap.go:62-243` and the non-Sentinel appends (`statefulset.go:540-544`), not
rendered by the builder (no `go run`/`go test` in this pass).

| What | Result |
|---|---|
| Generated master and replica configs as a pair, on one docker network with network aliases equal to the generated FQDNs (`test-0.test-headless.ns.svc.cluster.local`, …); `valkey-cli set` on the master, `get` on the replica | `9.1.1`, `8.1.9` and the local `valkey/valkey:8.0` (`8.0.10`): value read back on the replica within about 10 s, `master_link_status:up`, `slave0 … state=online`. No DNS stub needed |
| Master config with `no-such-directive yes` appended | `*** FATAL CONFIG FILE ERROR (Version 9.1.1) *** … >>> 'no-such-directive yes' Bad directive or wrong number of arguments`, server exits; same on `8.1.9` |
| Replica config alone, `replicaof` naming an unresolvable FQDN | `PONG`, `role:slave`, `master_link_status:down`, log `# Unable to connect to PRIMARY: Resource temporarily unavailable`, on `9.1.1` and `8.1.9` — the directive only has to parse at boot |
| TLS block of `configmap.go:85-94` with no `/tls` files | `# Failed to load certificate: /tls/tls.crt: … No such file or directory`, `# Failed to configure TLS`, server exits (`9.1.1`) |
| Persistence `both` (network, rdb+aof, general and memory blocks) | `PONG`, `config get dir` = `/data`, `aof_enabled:1` on `9.1.1` and `8.1.9` — the duplicate `dir` is accepted (measured twice, independently) |
| Renovate regex of `renovate.json:365` evaluated with `node` over `images.go` | `{datasource: docker, depName: valkey/valkey, currentValue: 9.1.1}` and `… 8.1.9` |
| Whether Renovate processes `images.go` | Dependency Dashboard issue #229 (`gh issue view 229`, updated 2026-09-27T02:41:30Z): the "regex (12)" section lists only `.github/release-template.hbs`, `build.yml`, `release.yml`, `Containerfile`, `go.mod`, `Makefile`, nothing under `test/`; `gh pr list --state all --author app/guided-traffic-automation --limit 200 \| grep -i valkey`: nothing; `images.go` has one commit, `f5f3256`. Presets: <https://docs.renovatebot.com/presets-config/> (`config:recommended` extends `:ignoreModulesAndTests`), <https://docs.renovatebot.com/presets-default/> (`ignorePaths` include `**/test/**`). Docker Hub v2 tags API lists `9.1.2` and `8.1.10` (their `tag_last_pushed` moves on every re-push, so it is not a release date) |

**Not verified:**

* Whether `#31` and `#37` were found on a cluster or in e2e: the commit messages and PR bodies do
  not say. Not load-bearing — the argument below rests on where the assertion lives today, not on
  where the defects were found. Only Hans can settle it.
* The configs above were transcribed by hand, not rendered by `GenerateValkeyConf`; a rendering
  divergence would not show. A test that renders through the builder would settle it.
* The Sentinel-branch Phase 3 unreachability while the replica ConfigMap is mounted is by
  reading; not measured in a pod.
* The effort of Option C-prime (an estimate) and the timing of the scripts in the real image with
  no peer answering (the backoff sleeps sum to about 15 s and 30 s by reading).
* Whether any Valkey image other than the two pins (and `8.0.10`, measured above) accepts the
  generated config: the README examples name `valkey/valkey:8.0`
  ([`README.md:224`](../../README.md#L224), `:248`, `:301`) and no supported Valkey version range
  is documented in `README.md` or `docs/operations/`. The ADR 0017 residual risk "Only the two
  pinned images are checked" already records the gap; nothing here decides it, and it needs a
  product call on a supported range before a test matrix makes sense.

## Impact

No production code path and no dataset is affected; security class none, because no guarantee or
trust boundary is touched (the auth and TLS behaviour a pair would assert is already asserted in
e2e). If this is never done: a directive a Valkey release rejects is found as an e2e pod that
never becomes Ready rather than as a named line in the imagetools job, and the tier question is
reopened from scratch by the next reader of the old requirement, because the tree records the
split and not the decision to keep it.

## Options

**One decision: does the generated Valkey configuration get a real-server check below e2e?**
Whatever the answer, it is recorded in ADR 0017 so the question is not reopened.

**Mechanism today.** A real `valkey-server` reads the generated config — the ConfigMap text plus
the lines the init container appends (Fact) — only in e2e, on the two pins, on every PR, behind
the required `E2E Tests` context. Imagetools boots the servers on hand-written flags and never
reads the generated config; the unit tier executes two of the three data/Sentinel init scripts on
the host shell and asserts the text they write, never boots a server on it. envtest has no
kubelet and no StatefulSet controller, so no pod is ever created from a template there, and a
docker fixture runs a rendered config outside any cluster the operator manages: **no option below
e2e tests the operator's reconciliation of the config**. A rejected directive is fatal at boot
(measured), so e2e turns it into a pod that never becomes Ready — the PR goes red either way.

**What the choice changes:** whether that failure surfaces within seconds in the required
`Valkey Image Tools` job with the directive named, or later as an e2e timeout on the same PR.
**What it does not change:** replication verification stays E2E-only in both remaining options,
so ADR 0017 D2 is unchanged; no option covers images other than the two pins; no option makes the
pins move — that is the Renovate `ignorePaths` gap in the T45 family.

**Option A — keep the split and record it. (recommended)**
One ADR 0017 Alternatives entry, "A real Valkey pair in the integration tier", plus a dated Status
amendment line. The entry carries:
(a) why it lost — envtest has no kubelet, and a pair would duplicate the replication assertion
the required `E2E Tests` context already makes on the same two pins in the same PR run; `#31`
(`f1108eb`) is the precedent for why that assertion lives in e2e, which has carried it since the
fix;
(b) the fact that imagetools already writes values into a single server on hand-written flags,
so D2's exclusive clause is replication;
(c) Option C-prime as the upgrade path, noting that the Sentinel-branch data init needs an
ADR 0017 D19 unit exec harness regardless;
(d) a revisit trigger that can be observed — a generated-config or config-writer defect reaches a
cluster, or an e2e run fails because `valkey-server` rejects a generated directive;
(e) the measured statement that persistence `both` boots on both pins, since no test boots it;
(f) cross-references to the two existing D42 residual risks (`:1271-1282`) instead of restating
them.
It must **not** claim that Renovate brings new pins while `**/test/**` is ignored.
*Cost:* XS, docs only; no code, no CI change. *Consequences:* a rejected directive is found in
e2e as a pod that never becomes Ready, on the PR that brings it — and while the pins do not move,
that PR is a hand-made pin bump. D2, `CLAUDE.md` and `docs/developer/testing.md` stay as they are.

**Option C-prime — boot the file the init container actually writes, in `test/imagetools`.**
Per pin (`pinnedImages()`): execute the generated `init-config-selector` script inside the image
with no peer and no Sentinel answering, the three config mounts as tmpfs with the ConfigMap texts
written to the read-only ones, then boot `valkey-server` on what the script wrote — through the
generated container command, so the auth variant runs with `VALKEY_PASSWORD` — plus a TLS variant
with a certificate fixture. On the Sentinel branch that exercises Phase 2 (pod-0 on the master
file, other ordinals on the master file plus the appended `replicaof`), not the ordinal fallback;
on the non-Sentinel branch pod-0 takes the self-claim and the others the ordinal fallback, and the
discovered-master path (`statefulset.go:522-527`) needs a stub `valkey-cli` answering
`role:master` or a live peer. No replication is asserted; what is asserted is that the server
boots and, from `INFO replication`, the role and `master_host` the script chose — a replica
reports `role:slave` with no master running (measured, Fact), so no second server is needed.
*Cost:* S–M (estimate): one test file, no new tag, target or required context; about 15 s and
30 s of backoff per script run unless the harness shortens it; a certificate fixture (TLS without
`/tls` is fatal, measured); and three doc amendments in the same change — `CLAUDE.md` ("the
config-writer scripts are not run there"), `docs/developer/testing.md:157`, and the ADR 0017
D53 (`:843`) with its residual risk (`:1271-1278`). The file is not vetted or golangci-linted
until [T43](043-lint-and-vet-skip-every-build-tagged-test-file.md) is resolved ~~(`gofmt -l .` in
`make lint` still covers it)~~ *(corrected 2026-09-27, cross-ticket from T43: `gofmt -l .` at
`Makefile:84` lists an unformatted tagged file but exits 0, so `make lint` gates nothing in it)*. It should run on the script as
[T35](035-master-records-lag-the-real-master.md) rewrites it, so it is sequenced after T35.
*Consequences:* a rejected directive fails the required `Valkey Image Tools` job within seconds
and names the line; D2 is unchanged. Replication, reconciliation and non-pinned images stay with
e2e or uncovered.

**Why A, checkably, and why it beats C-prime.**
1. No revisit trigger has fired: `configmap.go` is unchanged since `085ae23`, and the only
   config-writer change since the ticket (`2357946`) is covered by unit exec tests (Fact).
2. Of the three known config-adjacent defects, `#31` is the one a pair check would catch — and
   e2e has asserted non-Sentinel replication since that fix, so a pair would duplicate it; `#37`
   needed Sentinel, a failover and a TLS client, and `1b1f6ed` showed only across pod
   replacements, which no boot check below e2e reaches.
3. For the two pins, e2e already boots every directive the generator emits, on every PR, as a
   required context — directive by directive; the one combination no test boots, `both`, was
   measured to boot. C-prime would make a failure faster and clearer, not newly visible.

C-prime is the runner-up: it is the only option below e2e that checks the full file
`valkey-server` reads, in the real image's shell, and it costs no new CI context. It loses because
its main production value — catching a Valkey release that rejects a directive on the PR that
bumps the pin — does not occur while Renovate never bumps the pins (fixed in the T45 family, not
here), and because its other unique gain, executing the Sentinel-branch data init, is bought
cheaper and more precisely by an ADR 0017 D19 unit exec harness, which T35 needs for its rewrite
of that script anyway. After the T45 fix, the A entry's revisit trigger decides whether C-prime
is built.

## Decision

Not decided. Waiting on Hans's choice; Option A is recommended above.

## Verification

For Option A: `grep -n 'A real Valkey pair in the integration tier'
docs/adr/0017-test-and-ci-policy.md` finds the entry inside Alternatives Considered, and a dated
Status line names it; every fact in it matches the Fact section above (in particular no claim
that Renovate bumps the pins, and the duplication argument rather than "out of class");
`git grep -nw -e T41 -e C2 -- ':!docs/tickets'` and the file slug print nothing.

For Option C-prime: `make test-image-tools` green on both pins; a mutation check recorded in the
change — a bad directive injected into the generated config fails the boot with the directive
named, and the `replicaof` append removed from the script fails the `role:slave`/`master_host`
assertion (ADR 0017 D7, D10); `make lint` and
`make cyclo` clean; the three doc amendments present.

## Work list

- [ ] **Waits on the decision (XS under A):** the ADR 0017 Alternatives entry "A real Valkey pair
  in the integration tier" with the content (a)–(f) under Option A, plus the ADR's dated Status
  amendment.
- [x] **No decision needed, not 041's file:** the Renovate `ignorePaths` finding (Fact) belongs
  to the T45 family as an appendix to
  [045](045-ci-kubernetes-and-cert-manager-pins-have-no-renovate-manager.md), with the rule-1
  urgency the measured-false Renovate statements in tracked files carry there; it is not carried
  here as 041's urgency. 045 carries it since its edit of 2026-09-27, section "The Valkey
  test-image manager never extracts" (read in the working tree of that run, not yet committed).
- [x] **No decision needed, not 041's file:** the Sentinel-branch data init
  (`statefulset.go:275-356`) has no exec harness below e2e;
  [T35](035-master-records-lag-the-real-master.md) rewrites that script (decided changes C1 + C2,
  its Phase 1) and needs one under ADR 0017 D19/D20. 035 does not name such a harness in the
  working tree of 2026-09-27 (`grep -n 'harness\|D19\|init_script_exec'` over it prints
  nothing); the item belongs in 035, not here. *(Done 2026-09-27 in the consistency pass: 035
  names the D19 exec harness in its C4 cost and its C1 + C2 work item, and corrects its claim that
  `make test-image-tools` runs the script's commands.)*
- [ ] **Close ([ADR 0034](../adr/0034-tickets-are-work-lists-that-get-archived.md)):** the
  decision is the ADR entry itself; no README or `docs/operations/` consequence, and
  [`docs/developer/testing.md:15-16`](../developer/testing.md) already describes the split (under
  C-prime, `:157` changes); the ADR index row ([`docs/adr/README.md:109`](../adr/README.md))
  needs no change, because an Alternatives entry does not change the State text;
  `git grep -nw -e T41 -e C2` and the file name outside `docs/tickets/` (none on 2026-09-27); the
  Status blockquote says done with a "what shipped" line; `git mv` to [archive/](archive/).

## History

- 2026-09-27: re-verified at `84a39c2`. Checked every cite and claim against the tree, the GitHub
  rules API, Renovate's Dependency Dashboard and presets, and docker on both pins; locations
  re-read at `84a39c2` (the `release.yml` job `:625-658` is now `:624-657` and the
  `semantic-release` `needs:` `:1149` is now `:1148`, shifted by the `E2E_TESTS` line `bcc63c9`
  deleted; `renovate.json:359-363` is the manager at `:358-369`).
  **Found false or outdated, corrected** (in place in the Status block; in the merged option
  text, which this entry records because those sections were replaced): (1) "E2E stops being the
  only tier that writes values" (Options table, B) and "D2 unchanged (no value is written)" (C) —
  imagetools writes values since `bb6c78f` (`restricted_runtime_test.go:111`, `:122`, `:142`);
  E2E's exclusive property is replication verification; (2) step 7 quoted D2 as "the **only** tier
  that writes actual values into Valkey", dropping "and verifies replication reaches the
  replicas" (`0017:228-230`); (3) B's "what that buys" listed the `%VALKEY_PASSWORD%`
  substitution — it exists only in the Sentinel config (`sentinel.go:180-187`, `:715`), data-tier
  auth is on the command line (`statefulset.go:827-832`); (4) B claimed verification "against a
  golden string" — `configmap_test.go` asserts substrings, no golden file; (5) C's "the replica
  variant [needs] a master to name" — ambiguous, and under either reading no fixture is needed:
  `GenerateValkeyConf` writes `replicaof <pod-0>` itself and a lone replica boots (measured);
  (6) step 3 named `valkey/valkey:8.0` — the pin lives only in `images.go` and fixtures iterate
  `pinnedImages()` (ADR 0017 D42, D43); (7) Verification named `make test-valkey-integration`,
  which does not exist — `make test-image-tools`; (8) "Cost of A" said the replica ConfigMap names
  pod-1 and pod-0 replicates from pod-1 — the default names pod-0 as master (`configmap.go:33-39`),
  pod-1 as master is only the post-failover known-master case; (9) Why A relied on "the Renovate
  PR that brings a new Valkey pin" — Renovate never processes `images.go` (`**/test/**` in
  `config:recommended`'s `ignorePaths`; Dashboard #229 lists nothing under `test/`; `9.1.2` and
  `8.1.10` upstream with no PR); (10) Why A said none of `#31`, `#37`, `1b1f6ed` is in the class a
  boot or pair check catches — false for `#31` and a non-Sentinel pair; the argument is now
  duplication of e2e, which carries that assertion since `f1108eb`; (11) the adversarial note
  said the init container "chooses the master or replica file per ordinal" (`:322`, `:345-349`) —
  on the Sentinel branch with no Sentinel answering it takes Phase 2 and appends `replicaof` to
  the master file, and the ordinal fallback is reachable only without a `replicaof` in the replica
  file; (12) the Status block called D2 "verbatim" the same split, and described the stub as a
  fake that sleeps — corrected in place; (13) "whether any of those touched the config-writer
  lines was not traced" — traced: none of `86cf1f4`, `bb6c78f`, `b13377e`, `bb0f127` did.
  **Moved to Verified:** the regex matches both pins but Renovate never applies it; `Valkey Image
  Tools` and `E2E Tests` are required (rules API); the recent `statefulset.go` commits. `#31`/`#37`
  provenance stays not verified, marked not load-bearing. **Newly recorded:** the ADR 0017
  residual risks (`:1271-1282`) already hold part of the record; no test uses persistence `both`;
  the Sentinel-branch data init has no test below e2e (e2e runs it).
  **Measured** (docker, commands and results in Fact): a hand-transcribed generated pair
  replicates on `9.1.1`, `8.1.9`, `8.0.10`; an unknown directive is fatal; a lone replica boots;
  TLS without `/tls` is fatal; persistence `both` boots on both pins.
  **Options:** the sections "The decision", "Options" (table, "Why A" and the adversarial note),
  "If Option B is chosen" and "Recommendation" were merged into one Options section with one
  decision, the mechanism first. **Removed:** **B** — a Valkey pair in `test/imagetools` with
  replication, auth and TLS assertions (its minimum set was: a `SET` on the master readable on the
  replica; the replica reporting `role:slave` with the master's address; with `spec.auth` an
  unauthenticated client refused; with TLS the plaintext port closed unless `allowUnencrypted`;
  its own CI job so a Docker tier fails without reddening one that needs no Docker; a mutation
  removing `replicaof` breaking the replication assertion; and its earlier form, a new tier with
  tag `valkey_integration` and target `test-valkey-integration`) — removed because it duplicates the
  replication assertion the required `E2E Tests` context makes on the same pins in the same PR run,
  forces a D2 and `CLAUDE.md` amendment, adds speed not coverage, and is disproportionate to a
  low-severity gap; its step list was also stale (items 2, 3, 6, 7 above). **C** — one
  `valkey-server` per pin on the ConfigMap text alone — removed as superseded by C-prime: no
  multi-replica pod reads that text verbatim, so it misses the appended directives that `#37`
  changed. **Added:** **C-prime**, the runner-up and the upgrade path the A entry names.
  **Recommendation:** A unchanged; its justification changed — no longer the Renovate PR (false),
  and duplication of e2e instead of "out of class" for `#31`.
  **Status (no frontmatter):** state `filed` in substance → `analysed`, because every load-bearing
  claim is verified or explicitly not load-bearing and the option set is complete; severity low,
  security none, urgency `later` (rule 4: rule 1 does not match on 041's subject, D2, `CLAUDE.md`
  Testing and `testing.md:16` being true as conjunctions — the measured-false Renovate statements
  match rule 1 in the T45 family, where they belong; rule 2 no; rule 3 severity low), effort XS
  under A, blocked by the decision — all unchanged except the state.
  **Review of this pass, same day:** the Fact descriptions of both ordinal fallbacks said they copy
  the replica file — they copy the master file on ordinal 0; the non-Sentinel bullet gained its
  Phase 2 (`statefulset.go:490-519`); C-prime's Verification named a `replicaof` mutation its
  scope could not detect, so C-prime now asserts the role and `master_host` from `INFO
  replication`; the C-prime cost cites D53 at `0017:843`; the 045 appendix is present (work item
  checked); 035 names no D19 harness for the Sentinel-branch data init.
  Cross-ticket: in the consistency pass of the same day, the claim that `gofmt -l .` in `make
  lint` still covers a tagged file was corrected per T43 (it exits 0), and the Work list item for
  the Sentinel-branch exec harness is done: 035 now names the ADR 0017 D19 harness and corrects
  its `make test-image-tools` claim.
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
