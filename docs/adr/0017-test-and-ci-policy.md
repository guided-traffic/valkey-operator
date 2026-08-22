# ADR 0017: Test, Verification and CI Policy

## Status

Accepted. Date: 2026-08-21.

Implemented. Open items live at the decision that owns them; the load-bearing one is that the
abandon-path e2e is CODE-COMPLETE / **NOT EXECUTED** (D30), and D6 and D25 each carry one more.
The `generated-manifests` CI job has since run green on a runner, but only its passing path
([ADR 0014](0014-rbac-lives-in-three-places.md)) — a CI run outcome observed in the Actions UI,
not reproducible from this repository, which carries the workflow definition and no run record.

Amended 2026-08-22: **D42 and D43 are new.** The operator runs shell inside an image it does
not build, and nothing checked that the image still contained what the shell executes -- the
unit tier stubs `timeout` away because macOS lacks it, and the integration tier runs no
container at all. A fourth tier (`make test-image-tools`) now asks the real images, and the
Valkey images the suites run against are pinned in one file that Renovate maintains. The
declared tool list found three dependencies its own author had missed on a careful read
(`sed`, `seq`, `valkey-sentinel`), which is why the list is guarded from both sides rather
than hand-maintained.

## Context

Three things happened in this repo that shaped every rule below.

* **A feature shipped inert with green tests.** The StatefulSet nudge
  ([ADR 0003](0003-nudge-a-short-of-pods-statefulset.md)) never fired in production or in
  its own e2e — measured on a cluster, not reproducible from this repository: 5 min 03 s at
  `status.replicas=0`, `resourceVersion` constant, zero "Nudged StatefulSet" log lines — while
  its unit tests passed. Coverage and a green
  suite were not evidence that anything was constrained.
* **`-short` silently removed eight tests from CI.** `make test-unit` and
  `make test-unit-coverage` both passed `-short`, and eight `internal/controller` tests
  gated on `testing.Short()`. Three of them **had been failing unnoticed**, including the
  pre-existing guard for exactly the split-brain code a later fix changed. A green
  `make test-unit` was not evidence that the controller logic passed. Checkable in this tree:
  commit `b093fc2` drops the flag and repairs the three dead tests in one change.
* **A skipped e2e read as green.** `TestE2E_AntiAffinity_HardSpreadsAcrossNodes` skips below
  three schedulable nodes, and CI ran a single-node cluster — so hard-mode spread reported as
  covered while never executing. It is the only e2e carrying a node-count guard.
  `TestE2E_PodDisruptionBudget_SerializesEvictions` has none and did execute on one node; it
  joined the multi-node leg for a realistic node shape, not because it was skipping — a PDB is
  enforced per pod set, not per node (D23).

All three are the same failure: **something that cannot fail was believed.**

## Decision

### Entry points and tiers

**D1 — The Makefile is the only entry point.** `make test-unit`, `test-integration`,
`test-e2e`, `e2e-local`, `test`, `lint`, `lint-fix`, `gosec`, `vuln`, `cyclo`,
`cyclo-report`, `fmt`, `vet`, `build`, `docker-build`, `kind-load`. Go test commands and
tools are never invoked directly, by humans or by agents. CI invokes the same targets, so a
local run that bypasses the Makefile can pass with different flags than the pipeline uses.
Centralising the flags is also what makes D3 enforceable in one place.

**D2 — Three tiers with fixed responsibilities.** Unit tests cover all reconciliation logic;
integration tests (envtest) cover what only a real API server decides — CRD defaulting (D14),
delete preconditions such as the UID one (D12), and controller-manager wiring; E2E covers
rolling updates, failover and recovery against real Valkey instances, and is the **only** tier
that writes actual values into Valkey and verifies replication reaches the replicas
(`valkeyMSET`, `waitForConnectedReplicas`). envtest starts a kube-apiserver and etcd and no
kubelet, so no pod ever runs there and nothing under `test/integration/` opens a Valkey
connection. **The line between "add an e2e" and "do not" is "does the API server or a real
Valkey change the outcome", not "is the fix important."**

### What may be skipped, and what may not

**D3 — No `-short`, and no `testing.Short()` gate anywhere in the repo. Both halves are
permanent.** The reason is recorded as a comment in the `Makefile` above `test-unit` so the
flag cannot be reintroduced by habit. Even if someone adds a gate later, CI can no longer
trigger it. Any test that would genuinely be slow must be made fast or moved to another tier
— **there is no skip mechanism.**

**D4 — Unit tests reach no real Valkey, and failure is instant by construction.**
`newTestReconciler` redirects every client to `127.0.0.1`; tests that need a command to
actually succeed inject `fakeValkeyServer(t)` through `NewValkeyClientFn`. This is what makes
D3 cost nothing: measured on a developer machine and not reproducible from this repository,
`internal/controller` takes **3.36 s** without `-short` and **3.33 s** with it. Every new
controller path touching Valkey must decide explicitly whether the command should fail
(default) or succeed (`fakeValkeyServer`); there is no third option.

**D5 — A skipped E2E never counts as coverage.** Three guards, and they do not cover the same
tests. `E2E_REQUIRE_MULTI_NODE=true` turns the "fewer than 3 schedulable nodes" skip into
`t.Fatalf` on the leg that exists to run it — but that skip lives in exactly one place,
`requireThreeSchedulableNodes` (`test/e2e/affinity_test.go`), whose only caller is
`TestE2E_AntiAffinity_HardSpreadsAcrossNodes`; `TestE2E_PodDisruptionBudget_SerializesEvictions`
has no node-count skip for the variable to convert. The workflow greps the output for
`--- PASS:` of **both** named tests, so for the PDB test that grep is the only guard. And a
`Verify Kind cluster` step asserts the node count equals `workers + 1` before any test runs.
**A skip is indistinguishable from a pass in a CI summary**, and renaming either grepped test
must break the grep on purpose.

**D6 — A pass's unit run must report zero SKIPs on the uncached run (`-count=1`)**, which is
also repeated (`-count=2`) so no result comes from the cache. Zero SKIPs at `-count=1` is the
observable proof that no gate crept back in. Exactly one SKIP is permitted at `-count=2` and it
is not a gate: `TestBuildObserverLogger_WarnLevelSuppressesInfoLogs`
(`internal/observer/observer_cycle_test.go`) skips when the global log sink was already
fulfilled by an earlier run **in the same process**, which happens only under `-count>1`. Any
other SKIP, at either count, is a defect.

**The count does not see the repo's second conditional skip, and that is an open item.**
`TestStatefulSetHasChanged_InitContainerImageChange`
(`internal/builder/statefulset_test.go`) skips when the built pod spec has no init containers.
It never fires today — its fixture is a 3-replica Sentinel cluster and `BuildStatefulSet` always
adds the config-selection init container in Sentinel mode — so zero SKIPs still holds. It is
nonetheless the shape D3 and D10 forbid: `internal/builder/statefulset.go` assigns
`InitContainers` only `if len(initContainers) > 0`, so a change that stopped producing them
would silently self-disable the test instead of failing it. The fix is to assert the
precondition instead of skipping on it.

### A test must be able to fail

**D7 — Every fix ships with a recorded mutation or revert check.** The fix is reverted or
inverted in place, the named test must fail, and the failure message is recorded in the
change so a reviewer can reproduce it. Where a plain revert is impossible because the code is
new, each guard is knocked out individually and the file re-checked byte-identical afterwards.

**D8 — Tests that pass in both directions are labelled hygiene, in their own doc comments.**
A test that passes with and without the fix proves nothing about the defect; labelling it
prevents a later reader from treating it as protection it does not provide. The exception
classes are named rather than implied: documentation-only items have no test that can fail
pre-fix, and behaviour-pinning tests deliberately pass both ways.
**The rule binds tests written from here on; it is not retroactive and nothing enforces
it.** Verified today: no test doc comment in the repo carries the label — `grep -rn "hygiene"
--include="*_test.go" .` returns a single hit, and it is prose about production code rather
than a label on a test. `TestClearRollingUpdateState_ForgetsTheManualFailoverBound`
(`internal/controller/rolling_update_bounds_test.go`) is a both-directions test whose doc
comment says nothing about it — the first site to fix when this rule is applied backwards.

**D9 — A test that fails only probabilistically against unfixed code is not a regression
guard.** Recorded case, measured on a cluster and not reproducible from this repository: an
e2e subtest PASSED at 15.02 s in the full run and FAILED at 60.01 s in isolation, same
nudge-less binary, zero nudge log lines — recovery came purely
from the statefulset-controller's own retry, which is roughly uniform in
[0, current backoff]. Such a test stays as a **forward assertion** and the deterministic
guard is a unit test with a mutation check. **Any claim that an e2e guards a behaviour must
be backed by a demonstration that it fails against the unfixed code.**

**D10 — A test named after a guard must assert something that breaks when that guard is
removed.** Assertions that would still hold with the guarded code deleted outright do not
count as coverage. Concretely rejected shapes, each found in this repo:

* `assert.NotNil(t, c)` on a constructor that returns non-nil on **every** branch — three
  such tests in `internal/observer` were **deleted, not repaired**, because they stood in
  front of a real security regression (presenting the Valkey client certificate to Sentinel,
  or verifying Sentinel against the wrong CA). Replacements observe the wire or the parsed
  `*tls.Config`.
* A fixture that fails **every** write from the nth onward, used to pin a write **ordering**.
  Breaking write 1 also breaks write 2, so a pass that swallowed the first error still fails
  on the second — the two behaviours are indistinguishable from outside.
  `failOnlyCRUpdate(n, seen)` fails exactly one write; `failCRUpdateFrom(n)` is correct only
  where no other write stands between the one under test and the observable effect.
* A guard test whose subject cannot reach the guarded path at all. One subtest installed no
  `InstanceChecker`, so with the guard mutated away the pass fell through elsewhere and
  returned the same requeue — **every assertion held for a pass that never reached the code
  they describe.** A guard test must arrange the environment so the unguarded path could
  actually have run, and say why in the fixture.
* A retry test driven by a sleep racing a ticker. The installed error was cleared before the
  first poll, so the retry branch was never entered and the assertion passed anyway. Retry
  behaviour is pinned by a **scripted** fake and by asserting on the **call count**, not on
  the returned value — a nil return is reachable without the branch.

**D11 — Every negative-test set carries a positive control.** Without one, a helper that
always reported "no drift", or a UID comparison that never matches, would satisfy every
negative test while disabling the feature outright.

**D12 — A guard that refuses to act is tested from both sides, at four layers.** Negative
table over every foreign shape, positive control with a real UID, a zero-write assertion, and
a real-API-server test — plus the mutation check. The fake client writes no UIDs and runs no
garbage collection, and controller-runtime's fake client enforces only the ResourceVersion
delete precondition, **never the UID one** — so the unit tests can assert the option is sent
but can never show an API server rejecting the delete.

**D13 — The mutation audit runs against the live tree with a `sha256`-checked restore.**
Single-behaviour mutations by unique-string replacement, compile, run the owning package,
restore from a byte-exact backup with a `sha256` assertion, so no mutation can survive into
the working tree. Counted in one manual run that leaves no artifact in this repository: 62
mutations over six files, 59 killed on first application (95.2%), 62/62 after the three
surviving tests were repaired. **It is a manual protocol, not automation**,
and its results are only as good as the hand-chosen mutation distribution.

### Fixtures

**D14 — CRD-default behaviour is asserted in envtest, never in unit tests.** The fake client
never applies CRD defaults, so a unit test asserting default behaviour asserts the **Go zero
value** and would keep passing after the default changed or was dropped. Any new defaulted
field needs an envtest assertion; unit tests must construct explicit values.

**D15 — Rolling-update fixtures build the persisted StatefulSet and derive pods from its
template.** `stsForValkey` + `podFromStsTemplate`, so "pod matches persisted template" is a
property of the fixture rather than a constant somebody has to maintain. A rolling-update
fixture that only builds the CR and pods is invalid — one such test died with
`statefulsets.apps "mr-split" not found` before a single line of the logic it named ran.

**D16 — Test helpers build objects the way the real actors write them.** A CR fixture carries
a real UID, an owned object carries the controller ownerReference and a foreign one does not,
and a cert-manager Secret reproduces the verified shape. Without a UID on the owner,
`metav1.IsControlledBy` matches an empty-UID ownerReference and the ownership test is
vacuously green over exactly the guard it claims to cover.

**D17 — Fixture hostnames that a test actually dials use RFC 2606 `.invalid`, never
`*.svc.cluster.local`.** Cluster-shaped names are resolvable in some environments, so a unit
test could leave the process and hang on a DNS lookup — fast on one machine, hanging on
another. The rule binds the packages whose tests open connections, and those are the only two
where `.invalid` appears: `internal/observer` and `internal/health`. `internal/controller`
fixtures still carry `*.svc.cluster.local` names (21 occurrences) and are safe for a different
reason — `newTestReconciler` rewrites every address to `127.0.0.1` before it is dialled (D4).
`internal/builder` fixtures are rendered into manifests and never dialled at all.

**D18 — When new behaviour breaks an existing test, fix the fixture, not the assertion.**
Under the fake client there is no statefulset-controller, so a StatefulSet created during a
reconcile stays at `status.replicas = 0` and legitimately requeues; relaxing the assertion
would have hollowed out the test.

**D19 — Test the generated init script by executing it.** Mounts redirected into a
`t.TempDir()`, stub `valkey-cli` and `timeout` on PATH answering from a per-host table. A text
assertion only proves a line exists, never that its branch is taken — and the init script is a
role-election state machine whose entire correctness is which branch runs.

**D20 — Init-script edits need three proofs:** byte-identical output on every unaffected path
from the same mount; a render test asserting the script contains no `%!` (a wrong verb index
in an indexed format string surfaces only as literal text, never as a compile error); and a
**measured** `ComputePodSpecHash` delta per topology, because the hash decides whether a
release rolls running clusters.

### E2E determinism and blast radius

**D21 — The admission-webhook harness is namespace-scoped, fail-closed, and backed by an
endpoint-less Service.** An unscoped fail-closed webhook would block kind system pods and
every parallel e2e, turning one test into a cluster-wide outage. The endpoint-less Service
reproduces the incident's actual message (`no endpoints available for service`) rather than a
connection refusal — which matters because the `ReconcileBlocked` classification matches on
the message ([ADR 0002](0002-surface-a-blocked-reconcile-on-the-cr.md) D2). Every future
admission e2e goes through `blockResourceOperations` to inherit the scoping and the idempotent
cleanup.

**D22 — A harness that creates a cluster-scoped object marks it removed only after the delete
succeeded.** An optimistic flag turns the deferred cleanup into a no-op on exactly the path
where cleanup matters.

**D23 — Disruption tests drive the Eviction API directly; no test runs `kubectl drain` and no
test cordons a node.** A PDB is enforced per pod set, not per node, so the Eviction API is the
enforcement point and the equivalent for the property under test. Draining or cordoning is a
cluster-wide side effect that would strand the pods of every other test running under
`t.Parallel()`. The hard-mode `Pending` negative case collapses the spread domains
(`topologyKey: kubernetes.io/os`) instead.

**D24 — Count only truly schedulable nodes.** Ready, uncordoned, and free of a
`NoSchedule`/`NoExecute` taint — multi-node Kind keeps the control-plane taint, so counting it
would inflate the total and make the test *fail* on a 2-worker cluster instead of skipping.

**D25 — E2E waits poll for the transition, with an explicit interval and budget, and log the
last observed value.** A single read after another poll asserts on a status the operator has
not necessarily written yet — one such assertion passed in 4.01 s isolated and failed at the
identical 4.01 s under 34-way parallelism (measured on a cluster, not reproducible from this
repository). New waits, and every wait touched by a change, use
`wait.PollUntilContextTimeout` and never `require.Eventually`, whose condition goroutine can
outlive the test and touch a finished `*testing.T`. **The conversion is incomplete and stays an
open item:** 42 `require.Eventually` call sites remain across nine files in `test/e2e` against
27 `PollUntilContextTimeout` sites, and `valkeyExecQuick` is still documented as a helper for
`require.Eventually` loops.

**D26 — An assertion over a race window retries the whole sequence and pre-waits for the
precondition.** The eviction-refusal assertion retries up to three times, folds the eviction
into the poll loop, and first waits for `disruptionsAllowed > 0` — found by a mutation run,
not by reading: the disruption controller republishes the budget a moment *after* the pods
report Ready.

**D27 — Assert the operator's decision log, not the race it produces**, where the window is
short and self-healing. The two-replica e2e reads the `init-config-selector` log of the
recreated pod-0 and requires that it did not take the ordinal branch. **This makes the init
script's log lines a test contract.**

**D28 — Separate the design bound, the asserted deadline and the measured value, and never
conflate them.** Design bound ~30 s (`grace + interval`), asserted deadline 60 s (CI
headroom), measured 9.02 s on a cluster and not reproducible from this repository. The e2e
asserts the loose deadline and **logs** the real number.
Asserting the design bound turns scheduling latency on a loaded runner into a flake; asserting
nothing loses the guard.

**D29 — An E2E built on an unverified assumption must fail loudly, never pass falsely.** It is
arranged so a wrong premise fails at an explicit wait with the last observed condition
printed. **A test whose unverified premise silently degrades into a no-op is worse than no
test — it converts an untested path into a claimed-tested one.** The mirror rule holds on the
cheap end: a test that only restates an existing assertion adds maintenance and no
information.

**D30 — A test that has never run is reported as NOT EXECUTED and its item stays open.** A
commit containing the file is not evidence that it ran. The run checklist is part of the item,
not optional, and names what to check beyond PASS/FAIL.

### CI topology

**D31 — E2E runs as a two-leg matrix with an aggregating gate job.** `single-node` runs the
full suite; `multi-node` (control-plane + **3** workers) runs
`E2E_RUN='TestE2E_AntiAffinity|TestE2E_PodDisruptionBudget'`. The legs run in parallel on
separate runner pods with distinct cluster names. A separate `e2e-gate` job named "E2E Tests"
aggregates both, so the pre-existing required status check keeps one stable name.

**D32 — Three workers, never two.** Kind removes the control-plane `NoSchedule` taint only on
single-node clusters, so 2 workers plus a tainted control plane leaves 2 schedulable nodes and
the hard-spread test would still skip — the "≥ 2 nodes" lower bound would have reproduced
exactly the defect it was written to fix.

**D33 — Every cluster-setup step is node-count agnostic**, derived from `KIND_WORKERS`: the
kind config renders one worker line per worker via an explicit `while` loop (`seq` counts
*down* on BSD and emitted two workers for the single-node leg), and the sysctl step, the image
import and the kube-proxy settings all loop over `kind get nodes`. The image import matters
specifically because the operator runs with `pullPolicy: Never`, so on a multi-node cluster its
pod can land on any worker.

### Coverage, complexity and record-keeping

**D34 — Coverage gaps are decisions, exhaustively listed and re-stated each pass.** The
entry-point function of each of the four `cmd` packages — `main` in `cmd`, plus `Run` in
`cmd/migrate`, `cmd/observer` and `cmd/sidecar` — together with `SetupWithManager` are
deliberately not unit-tested. The packages around them are (25.5 % to 85.9 %, read off one
`make test-unit-coverage` run and not stored in this repository — only the repo-wide number is,
in [`.github/badges/coverage.json`](../../.github/badges/coverage.json); only `cmd` is
`package main`, the other three declare `package migrate`/`observer`/`sidecar`), so what sits
at 0 % is those five functions, not four packages. Reaching them means reaching `mgr.Start` and
signal delivery, which the integration suite already exercises with a real manager. Buying those statements with a fake `rest.Config` would move the number
and prove nothing. **Where coverage is blocked by a missing production seam and the runtime
behaviour is correct, the gap is recorded as an observation** — not filed as a defect
(overstates it) and not closed with a test-only fake (a seam with no production justification).

**D35 — Cyclomatic complexity stays under 15 for every function; no `nolint` exemptions.**
Gated by `make cyclo` and its CI job. This is why the split-brain evidence rules are individually
nameable and testable predicates (`couldNotHaveSelfElected`, `recordedGaveUpTheRole`,
`recreatedAfter`) rather than inline conditions — the paths where an unreviewable branch tree
turns into a data-loss bug are exactly the ones the ceiling forces apart.

**D36 — Every claim is labelled by how it was verified: run, read-only, or hypothesis.** "No
cluster was touched" is a normal and acceptable statement; an assumption travelling as a fact
is not. This is precisely how a build blocker surfaced — by re-running an earlier pass's own
gates from a clean tree and finding they did not reproduce (development history; the pass logs
are not in this repository).

**D37 — A stale claim is superseded in place, never silently deleted or left orphaned**, with
the correction and the value it replaced. A silently deleted claim leaves a reader unable to
tell whether it was wrong or merely moved; an orphaned superseded claim gets quoted as current.

**D38 — A pass declares its scope and files, rather than fixes, everything outside it.** A
coverage or documentation pass that also edits production code makes its own gate results
unattributable: a green run can no longer distinguish "the tests got better" from "the code
changed underneath them". A filed item is not a lost item.

**D39 — A defect in a pass's own shipped work becomes a new item, never a rewrite of the item
that shipped it**, with amendments linking back from every item it touches. Rewriting in place
would erase the fact that the first shape was wrong — precisely the history a later reader must
not lose.

**D40 — Order a commit series so the producer of a signal lands before its consumer.** A
consumer that lands first reads an annotation nobody writes, so a partial rollout or a bisect
stop between the two commits sees the operator evaluating evidence that cannot exist.

**D41 — Every repository artifact is written in English** — code, comments, commit messages,
documentation and CRD field names — regardless of the language used in conversation. Mixed-language
identifiers in the public API can never be renamed without a breaking change.

**D42 — The image the operator runs shell in is a dependency, and it is checked like one.**
`internal/builder` generates two init container scripts, an auth-wrapped container command,
exec probes and a drain preStop hook; all of them run inside the upstream Valkey image.
`RequiredImageTools` names what they execute and `test/imagetools` asks the pinned images
whether they provide it (`make test-image-tools`, its own CI job). It needs docker and no
cluster, so it answers in seconds and names the missing binary rather than surfacing minutes
later as a cluster that will not converge.

No existing tier could answer this. The unit tier executes the generated scripts against the
developer's shell and stubs `timeout` because macOS does not ship it
(`internal/builder/init_script_exec_test.go`); the integration tier runs envtest, which has
no kubelet and therefore no container. Only a real image can say what is in a real image.

**The list is guarded from both sides, because a hand-maintained one is theatre.**
`TestRequiredImageTools_CoversTheGeneratedScripts` walks every exec command the builder puts
into a container that runs the Valkey image and fails on a tool the list does not name; the
converse test fails on a declared tool nothing uses. Its limit is stated where it lives: a
script reaching for something outside the recognised command vocabulary passes unseen. The
guard earned its place immediately -- it found `sed`, `seq` and `valkey-sentinel` missing
from a list assembled by reading the same scripts, and `sed` is the one whose absence is
silent, because it substitutes the Sentinel password placeholder.

**D43 — The Valkey images the suites run against are pinned in one file, and CI carries a
selector rather than a copy.** `test/testimages` holds the current Valkey 9 release (the
default for every suite) and the current Valkey 8 release (the second e2e leg, and the start
of every upgrade the suite performs). Renovate keeps both current and is capped per major by
`allowedVersions`, so crossing to a future major stays a decision rather than an arriving
pull request.

The e2e matrix passes `E2E_VALKEY_LINE=8`, not an image, and an unrecognised value panics
instead of falling back. A copy of the pin in the workflow would have to move in lockstep,
and the failure mode of it lagging is the one this ADR keeps closing elsewhere: a leg that
goes green while testing something other than what it claims.

**Only the tiers that pull an image are pinned.** Unit and integration never do, so their
image strings are fixtures whose only requirement is to differ from one another; pinning them
would churn dozens of call sites on every bump without changing a byte that executes.

**The rolling-update pair is the two pinned lines**, latest 8 to latest 9, rather than two
tags of one line. Both ends stay current without a third pin Renovate cannot maintain, and
the tests double as continuous proof that the upgrade users will actually perform loses no
data. Accepted cost: a genuine cross-major replication break upstream turns these tests red
for something that is not this operator -- information worth having before a support request
rather than after one.

## Consequences

* Every fix costs an extra build-and-run cycle for the mutation check, and the result is
  recorded with the exact failure message.
* Every ownership-style guard carries a four-part cost (D12) plus its mutation check.
* Unit runs cost the full ~3.4 s for `internal/controller` (~50 s wall clock for `./...`; both
  measured on a developer machine, not reproducible from this repository) — the accepted price
  of D3. Enforcement is convention plus
  `grep -rn "testing.Short()" --include="*.go" .` returning nothing; **there is no automated
  lint rule forbidding a new gate.**
* Fixture setup is heavier (D15, D16), but pod-vs-template drift and vacuous ownership tests
  become impossible rather than accidental.
* In the dialling packages, fixture hostnames no longer read like real deployment names (D17);
  the trade is determinism.
* **A regression that doubles recovery time but stays under 60 s passes CI** and is visible only
  in the logged elapsed time (D28).
* Several behaviours have **no end-to-end coverage by design** — single-pass properties, the
  backoff cap, the nudge ordering — and the reasoning is recorded per item so the gap is not
  later mistaken for an oversight. Conversely the steady-state split-brain check has no e2e and
  that **is** a gap ([ADR 0011](0011-evidence-based-steady-state-split-brain-resolution.md)).
* The suite never exercises drain-specific kubelet/controller behaviour; only the eviction gate
  is covered (D23).
* A genuinely broken budget takes up to three attempts (~95 s) to report (D26).
* Two Kind clusters per CI run; the multi-node leg costs ~6 min (observed in the Actions UI, no
  run record in this repository) and three extra node containers
  (control-plane + 3 workers against the single-node leg's one).
  Renaming a guarded test requires updating the grep and `E2E_RUN` in the same change.
* The repo's headline coverage number is permanently capped by the wiring boundary (D34), and
  the exhaustive 0% list must be re-stated each pass so the gap stays a decision.
* The Makefile becomes the contract between local development and the pipeline: any new tool or
  flag has to be added as a target before it can be used.
* Known, cheap defects stay open across passes (D38), and the open list grows faster than it
  closes. Accepted.
* Documentation sections grow correction blocks rather than shrinking (D37); a reader must read
  a section to its end before quoting it.

## Alternatives Considered

### Keep `-short` for speed

Rejected: no runtime is actually saved (D4), and it hides failures.

### Keep the `testing.Short()` gates and simply repair the three broken tests

Rejected: it leaves the mechanism that hid them intact. Keeping `-short` in the targets while
dropping only the gates was rejected for the same reason.

### Rely on coverage percentage and a green suite

Rejected: the nudge shipped inert under exactly that regime.

### Treat the e2e as the regression guard

Rejected: it would have let the inert nudge ship green.

### A hold-and-release e2e for the backoff cap

Rejected as a coin flip — against an unfixed operator the residual wait is roughly uniform in
[0, current backoff], so a 45 s window catches it about half the time. **A probabilistic e2e is
worse than none, because it reads as a guard while guarding nothing and adds flake.**

### A polling e2e for a single-pass property

Cannot sample faster than the pass. Recording every write in a unit test via a
`SubResourceUpdate` interceptor is strictly stronger evidence than sampling from outside.

### An e2e for every fix

Rejected on cost versus information — see D2's line.

### Assert defaults in unit tests with the fake client

Rejected: it does not exercise defaulting at all.

### Assert a tunable bound against the constant it guards

Proven circular by mutation: raising `reconcileRetryMaxDelay` back to 1000 s kept the test
green. Guards assert against an **independent policy ceiling**
(`maxTolerableRetryDelay`, 60 s) that expresses "what we are willing to tolerate", not "what we
configured".

### Keep the vacuous `assert.NotNil` tests and add assertions alongside them

Rejected; all three were deleted.

### A real `kubectl drain` e2e, or cordoning a node

Rejected: cross-test side effects under `t.Parallel()`.

### Trust the skip message in a green leg's log

Rejected: nobody reads a green leg's log.

### Serialize the E2E matrix legs with `max-parallel: 1`

Built that way first under a shared-DinD-host assumption, removed once each leg got its own
runner pod.

### Rename or replace the required status check instead of adding `e2e-gate`

Rejected: the pre-existing required check must keep one stable name.

### Fake `rest.Config` to lift the entry-point coverage

Rejected as coverage theatre.

### Mutate a copy of the tree instead of the live tree

Loses the real build and test wiring. The `sha256`-checked restore is what makes mutating the
live tree safe on a working branch.

### Block pod creation to force the topology-abandon path

Proven unreachable: a permanently blocked pod-0 stalls in `manual-failover` forever and never
enters Phase 1. Rejecting only pod-0's create via an object selector was also rejected — it needs
a label present at CREATE time, and **no pod carries `instanceName` at any time**: the sidecar
patches only `instanceRole` (`internal/sidecar/labeler.go`), and `LabelInstanceName` is written
solely inside `common.PodLabels`, whose only callers are tests. `BuildStatefulSet` labels pods
from `common.BaseLabels`, which excludes it by design. An object selector on it would match
nothing, at CREATE time or later.

### Append-only corrections, or a separate changelog

Rejected: the wrong statement keeps being read, and the analysis and the status drift apart.

### Let a lane fix what it finds outside its scope

Rejected: it contaminates the pass's own verification.

## Residual risks

* **The drift guard sees only a vocabulary (D42).** `shellCommandCatalog` covers the
  coreutils and busybox applets a container script realistically uses. A generated script
  that calls something outside it is not noticed, and the image check then never asks for
  that tool.
* **The tool check proves presence, not behaviour (D42).** `command -v` finds a busybox
  applet as readily as the GNU tool, and the two differ in flags. The shell-construct test
  covers the constructs the scripts rely on; it does not cover, for example, a `timeout`
  with a different signature. Executing the real scripts inside the image was considered and
  deferred as disproportionate for the observed risk.
* **Only the two pinned images are checked (D42).** `spec.image` is a user field with no
  operator default, so a cluster may run any image -- `valkey/valkey:9-alpine` being the
  realistic one. Measured on 2026-08-22: that variant provides every required tool. Nothing
  keeps it that way, and nothing checks it on a schedule.

* **The abandon-path e2e has never been executed (open).** Its load-bearing premise — a replica
  with `masterauth` set against a master with no `requirepass` must abort the handshake at AUTH
  — follows from the Valkey/Redis handshake but has never been run. If it does not bite, the
  test fails loudly at the `TopologyRestored=False` wait and cannot pass falsely.
* **No automated rule forbids a new `testing.Short()` gate** — D3 rests on convention plus a
  grep.
* **The "never observed" retry branch of the eviction assertion has never been exercised** by a
  real run; only the "granted" branch was, via a forced mutation.
* **The mutation audit is manual** and its distribution is hand-chosen per pass.
* **The commit series was not verified to build commit by commit.** Only the tip is known to
  build and pass, so `git bisect` inside the range may fail for unrelated reasons — bisect the
  tip against `main`.
* **A documentation pass can only revert-verify the fixes it wrote itself**, so the record has to
  name who verified what.

## References

* [`Makefile`](../../Makefile) — every target, and the recorded reason `-short` is absent
* [`internal/controller/`](../../internal/controller/) — `newTestReconciler`, `fakeValkeyServer`, `stsForValkey`, `podFromStsTemplate`, `failOnlyCRUpdate`
* [`internal/sidecar/`](../../internal/sidecar/) — `scriptedRoleDetector`, the D10 call-count fixture for the drain retry path
* [`internal/builder/init_script_exec_test.go`](../../internal/builder/init_script_exec_test.go) — the executing init-script harness
* [`test/integration/`](../../test/integration/) — envtest suites, including the UID delete-precondition test
* [`test/e2e/`](../../test/e2e/) — `blockResourceOperations`, `assertSecondEvictionRefused`, `schedulableNodeCount`, `requireThreeSchedulableNodes`
* `.github/workflows/release.yml` — the two-leg E2E matrix, `e2e-gate`, `generated-manifests`
* [ADR 0003](0003-nudge-a-short-of-pods-statefulset.md) — the feature that shipped inert
* [ADR 0011](0011-evidence-based-steady-state-split-brain-resolution.md) — the decision table these tests are written against
* [ADR 0014](0014-rbac-lives-in-three-places.md) — the CI job that proves the generated manifests are current
