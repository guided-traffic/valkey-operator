# ADR 0017: Test, Verification and CI Policy

## Status

Accepted. Date: 2026-08-21.

Implemented. Open items live at the decision that owns them; D6 and D25 each carry one. The
abandon-path e2e, listed here as CODE-COMPLETE / **NOT EXECUTED** (D30) when this ADR was
written, has run since: D50 records it on CI legs, and it passed in both local full-suite runs
of 2026-09-26 (Kind, Valkey 9 and Valkey 8) *(and in both full suites on the last image of that
day, with [ADR 0025](0025-a-split-brain-warning-means-one-that-did-not-resolve-itself.md) D9's
clock: 53/53 on Valkey 8, 52/53 on Valkey 9, the one failure in another test — D50 as amended)*.
The `generated-manifests` CI job has since run green on a runner, but only its passing path
([ADR 0014](0014-rbac-lives-in-three-places.md)) — a CI run outcome observed in the Actions UI,
not reproducible from this repository, which carries the workflow definition and no run record.
*(Its failing path fired on 2026-09-26, on `feat/rootless` at `e2ce8bb`: see the D49 amendment.)*

Amended 2026-09-26, ~~three~~ four times on `feat/rootless` *(the fourth added the same day)*:
**D35** is scoped to hand-written code
(`make cyclo` ignores `zz_generated`); **D49** puts the version into every tool path, after a
stale local `controller-gen` turned `Generated Manifests Up To Date` red; **D55** follows the
fleet-upgrade e2e through the second roll of ADR 0032 D2; **D50** records a second instance of
its fixture rule, `TestE2E_SidecarFailoverDrainMaster`, found by the last full e2e run of the
day and fixed. Each is recorded at its decision. The same day also made D52 to D55 new and
amended D6 and D42 (the paragraphs below).

Amended 2026-08-22: **D42 and D43 are new.** The operator runs shell inside an image it does
not build, and nothing checked that the image still contained what the shell executes -- the
unit tier stubs `timeout` away because macOS lacks it, and the integration tier runs no
container at all. A fourth tier (`make test-image-tools`) now asks the real images, and the
Valkey images the suites run against are pinned in one file that Renovate maintains. The
declared tool list found three dependencies its own author had missed on a careful read
(`sed`, `seq`, `valkey-sentinel`), which is why the list is guarded from both sides rather
than hand-maintained.

Amended 2026-08-22: **D44 is new.** `TestE2E_TLS_HACluster` failed on the Valkey 9 leg
because a replica logged one refused SYNC connect while pod-0 was still binding its TLS port,
and the log scan treated "Connection refused" as a critical error wherever it appeared.

Amended 2026-08-22: **D31 is restated.** The matrix has three legs, not two, since D43 added
the Valkey 8 leg, and every leg is now named for the Valkey line it runs and passes that line
explicitly (`single-node-valkey9`, `multi-node-valkey9`, `single-node-valkey8`) instead of
leaving the default one nameless behind an empty selector.

Amended 2026-08-22: **D45 is new.** An e2e failed on a promoted master that served an empty
dataset, and nothing in the run could say whether the operator promoted an empty replica or
the promoted pod lost its process afterwards: the workflow collects pod logs after the suite
has finished, and every test namespace deletes itself in a defer, so the collection step had
printed an empty section for months.

Amended 2026-09-18: **D50 and D51 are new.** `TestE2E_RollingUpdate_TopologyRestoreAbandoned`
failed on four runs in ten days, each time on a different leg, which is the shape of a test
whose setup races rather than of a regression. It forces the abandon path by jamming pod-0's
replication, and it picked the pod to jam by rolling-update state plus `role:slave` -- a pair
the *outgoing* master also satisfies for the one second between the operator logging "Demoted
outgoing master to replica" and "Deleting old master pod after manual failover". D51 is
unrelated and came out of the same logs: every suite run was printing a deprecation warning
for an API the operator does not use at all.

Amended 2026-09-18: **D49 is new.** Three of the Makefile's own quality targets could not
run on a developer machine at all, which is how D1's "the Makefile is the only entry point"
had been true of CI and false locally.

Amended 2026-09-18: **D47 and D48 are new.** `main` had been red for 15 days and nobody was
stopped: `Generated Manifests Up To Date` is a CI job, not a *required* status check, so
Renovate's platform automerge merged PR #209 (controller-tools v0.21.0 -> v0.22.0, which
stamps its own version into the CRD annotation and was never regenerated) and then #210, #212,
#214, #215, #218 and #219 on top of it, each carrying the same red check. Four of the twelve
gate jobs had never been required; `semantic-release` already `needs:` all of them, so the
release correctly stopped while the merges did not. The second break arrived the same way from
the other side: Renovate advanced `k8s.io/kube-openapi` — a pseudo-versioned indirect dep — past
the commit that switched it to `structured-merge-diff/v7`, which `k8s.io/apimachinery` v0.37.0
cannot compile against, and that one *was* caught by required checks and simply blocked the
whole `k8s-go-modules` group forever instead.

Amended 2026-08-22: **D46 is new.** The npm dependency set behind semantic-release was
exercised in exactly one place: the release job, on pushes to main, after a Renovate bump had
merged. Two failures rode that gap. `conventional-changelog-conventionalcommits` v10 ships its
templates for conventional-changelog-writer@9 as compiled functions; the writer@8 that
`@semantic-release/release-notes-generator` 14 loads rendered them as an accidentally-valid
no-op, so every release from v1.10.26 (2026-06-28) to v1.10.48 published header-only notes and
nobody noticed. Preset 10.4.0 added an upstream guard that turned the same mismatch into a
hard `Missing helper` failure, and releasing stopped entirely.

Amended 2026-09-26: **D52 to D55 are new; D6, D42 and the D42 residual risk on executing the
scripts are amended.** [ADR 0032](0032-generated-pods-run-rootless.md) makes every generated pod
rootless and moves every existing cluster onto that posture at the operator upgrade, which is
the first change that rolls a whole fleet automatically. Pod Security is a profile the API
server enforces, and no tier could evaluate it: the unit tier could only restate it, the
imagetools tier checked that tools exist but not who runs them, and the only test that starts
from data a root process wrote runs outside CI. Each new decision covers what the tier below it
cannot see. D52 and D53 ran green on 2026-09-26; D54 and D55 ran green the same day, locally on
Kind and not in CI (~~the branch has not been through the pipeline~~ *(corrected 2026-09-26: it
was pushed as `e2ce8bb`, where two gate jobs failed — D49; what that run's E2E legs reported is
not recorded in this repository, and CI has not run on the fixed working tree)*), so both stay
open. D55's run
is of its first version, before the amendment that follows. The
open items above grow with it: D6 now carries two (the root skip joins the second skip). The
new e2e waits do not add to D25's: they were written against it (Residual risks).

Amended 2026-09-26, later the same day: **D55 is amended, ~~and two e2e are NOT EXECUTED (D30)~~** *(both executed since, the same day — see the end of this paragraph)*.
Hans decided two questions the amendment above had left open. The persistent data pods the
migration creates keep the root repair in their immutable spec, and a second roll now replaces
them ([ADR 0032](0032-generated-pods-run-rootless.md) D2). D55's fleet e2e therefore waits for
that roll and reads the re-owned files, and its no-second-roll assertion is gone. A Sentinel tier of one or two now rolls serially
([ADR 0024](0024-the-sentinel-tier-reports-its-own-completion.md) D10), and the new
`TestE2E_RollingUpdate_TwoSentinelsRollSerially` covers it. ~~Neither e2e has run.~~ *(Both have
run since, locally on Kind and not in CI: see D55 and Residual risks, updated 2026-09-26.)* The
D55 run recorded below stays true for the code it ran against (Residual risks).

Amended 2026-09-26, last on the day: **D50 gains a second instance.** The last full e2e run of
the day went red once, on the single-node Valkey 9 leg, in `TestE2E_SidecarFailoverDrainMaster`.
Every wait after its master delete was already met by the terminating old master, which kubelet
keeps Ready ([ADR 0026](0026-a-pod-being-deleted-is-not-available.md)) *(the diagnosis, from the
test code and its timing, not traced in the failed run — noted 2026-09-26)*; the test now waits
for the replacement by UID. The diagnosis, the experiment behind it and the five sites of the
same shape still unaudited (T34) are recorded at D50.

**What "the final image" means in this ADR** *(clarified 2026-09-26)*. Every site that says
"the final image" or "the final code" means the last image at the time that site was written,
and several images of that day were once the last. The sites that name the image of the 53/53
pair of full suites — under D54, D55, the Consequences and the Residual risks — mean one built before
[ADR 0025](0025-a-split-brain-warning-means-one-that-did-not-resolve-itself.md) D9's clock and the
single failover write of [ADR 0010](0010-every-rolling-update-wait-is-bounded.md) D14; each of
those sites is marked, as "the code before ADR 0025 D9's clock" or, for short, "pre-clock". The last image of the day was built with both, and its suites ran before
the D50 fix above, which changes test code only. Run on Kind (Kubernetes 1.36.1, containerd
2.3.1, runc 1.4.2, Linux 6.10), not in CI:
`TestE2E_FleetUpgrade` from 1.12.8 green; the full suite 53/53 on Valkey 8 and 52/53 on Valkey 9,
the one failure the D50 instance; two further Valkey 8 runs of D54's test and ADR 0033's hardening
e2e green. No full suite has run on the D50 fix. The CI-parity gates — `make generate-all` (no diff
with a fresh controller-gen v0.22.0), `make lint` (golangci-lint v2.14.0, 0 issues), `make cyclo`,
`make gosec` (v2.29.0, 0 issues), `make vuln` (no vulnerabilities), the unit and integration
coverage targets, `make test-image-tools` and `make test-release-tooling` — are recorded green
from a clean-copy run on the code **before** the clock, the single write and the D50 fix. Their
rerun on the final code had not finished when this was written, and nothing here claims it.

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

**D49 — Every pinned tool installs into `$(LOCALBIN)` and is invoked by its path.** `gocyclo`,
`gosec` and `govulncheck` used `which <tool> > /dev/null || go install …@$(VERSION)`, which
probes `PATH` but installs into `GOBIN`/`GOPATH/bin`. On a machine where that directory is not
on `PATH` — the default on macOS — `make cyclo`, `make gosec` and `make vuln` reinstalled on
every run and then died with `command not found`; where it *was* on `PATH`, any binary another
project had put there shadowed the pinned version silently and forever. All three now use the
`go-install-tool` define and `$(LOCALBIN)` like `controller-gen`, `setup-envtest`, `kustomize`
and `golangci-lint` already did, and `govulncheck` gains the renovate-managed pin it never had
(it was installed `@latest`, so the three tools CI ran were not the three a developer ran).

**Prerequisites are expanded when a rule is read, not when it runs**, so the tool-path and
tool-version variables move above the first target that names one; only the `$(LOCALBIN)` mkdir
rule stays where it was, because a rule placed above `all: build` becomes the default goal.

*(Amended 2026-09-26.)* **The tool path carries the version** (`bin/controller-gen-v0.22.0`,
`bin/golangci-lint-v2.14.0`, …). `go-install-tool` installs only when its file is missing (make
may start the recipe more often, because `$(LOCALBIN)` is a normal prerequisite and newer than
the tools, but the `[ -f ]` guard skips it), so a version bump never reached an existing `bin/`: Renovate moved `CONTROLLER_GEN_VERSION` to v0.22.0 (#209), a local
`bin/controller-gen` v0.21.0 regenerated the CRDs on `feat/rootless` and stamped
`controller-gen.kubebuilder.io/version: v0.21.0` back into them, and `Generated Manifests Up To
Date` — which installs fresh — failed on exactly that line (reproduced locally: regenerating the
pushed commit with v0.22.0 changes the two stamps and nothing else). The same stale file served
`make lint` a golangci-lint older than the pinned v2.14.0. With the version in the name a bump
is a missing file; `go-install-tool` installs into a directory of its own (`<path>.install`) and
moves the binary onto the versioned path, because `go install` names it after the package. The
tool-path variables stay above the first target and expand the version lazily. Unversioned
binaries an older checkout left in `bin/` are no longer read by any target.
*(2026-09-26, the fix verified end to end.)* The CI-parity rerun on the final tree — ADR 0025 D9's own clock, the one-write arming and the
D50 drain fix included; only comments and the retrigger arming test changed after it — ran every
gate target in a fresh clean copy with an empty `bin/`: `make generate-all` (no diff), `make lint`
(golangci-lint v2.14.0, 0 issues), `make cyclo`, `make gosec` (v2.29.0, 0 issues), `make vuln`
(no vulnerabilities), `make test-unit-coverage`, `make test-integration-coverage`,
`make test-image-tools` and `make test-release-tooling`, all green (2026-09-26); `make test-unit`
and `make test-integration` green again after the last test and comment changes. CI itself has
not run on it.
Naming a tool in a target's prerequisites while its variable is still undefined expands to
nothing and silently drops the dependency — which is how the first attempt at this decision
failed. *(Verified 2026-09-26: `make generate-all` in a clean copy of the working tree with an
empty `bin/`, so controller-gen v0.22.0 installed fresh the way the CI job installs it, leaves no
diff. CI itself has not run on the fix.)* *(That clean copy predates the last three changes of the
day — ADR 0025 D9's clock, ADR 0010 D14's single failover write and the D50 fixture fix; the rerun
on the final code is not recorded here, Status.)*

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
tests. *(Amended 2026-09-26: a fourth, `E2E_REQUIRE_USER_NAMESPACES`, below.)* `E2E_REQUIRE_MULTI_NODE=true` turns the "fewer than 3 schedulable nodes" skip into
`t.Fatalf` on the leg that exists to run it — but that skip lives in exactly one place,
`requireThreeSchedulableNodes` (`test/e2e/affinity_test.go`), whose only caller is
`TestE2E_AntiAffinity_HardSpreadsAcrossNodes`; `TestE2E_PodDisruptionBudget_SerializesEvictions`
has no node-count skip for the variable to convert. The workflow greps the output for
`--- PASS:` of **both** named tests, so for the PDB test that grep is the only guard. And a
`Verify Kind cluster` step asserts the node count equals `workers + 1` before any test runs.
**A skip is indistinguishable from a pass in a CI summary**, and renaming either grepped test
must break the grep on purpose.

*(Amended 2026-09-26.)* **`E2E_REQUIRE_USER_NAMESPACES=true`** is the fourth guard, and the first
one no CI leg sets. `TestE2E_PodHardening_UserNamespacesLocalhostSeccompAndDigest` starts a
restricted probe pod with `hostUsers: false` (`userNamespacesSupported`); when its container does
not start, the test moves the cluster without the user namespace and skips only the
user-namespace subtest, by name and with the runtime's message — the variable turns that into a
failure. The first push of the test failed both single-node legs on `b13377e`: the legs run Kind
inside Docker-in-Docker with containerd's `native` snapshotter, and there a pod with
`hostUsers: false` never starts. **Measured** locally with the CI Kind config (`kindest/node`
v1.33.4, containerd 2.1.3, `snapshotter = "native"`, single node): the init container failed with
"mount callback failed … container ID 1109000192 cannot be mapped to a host ID", the observer's
container with Kind's `createContainer` hook "permission denied", the roll held at its first
replica, and the CR reported `PodAvailabilityStalled=True/ValkeyPodNotAvailable` — ADR 0026 D11
doing its job. ~~The CI log itself was not readable here (no API credentials); that the legs failed
on this test is inferred from the reproduction and from the multi-node leg, which does not run the
test, going green.~~ *(Confirmed from the CI logs, 2026-09-26: on `b13377e` and on `a04e2d0` this
test was the only failure of both single-node legs. In CI the refusal comes one step earlier than
in the reproduction: the pod sandbox itself fails (`FailedCreatePodSandBox … OCI runtime create`),
the container stays `ContainerCreating`, and the reason shows only as an Event — so the first probe
on `a04e2d0`, which read container states alone, saw a pod stuck in `Pending` and timed out. The
probe now also reads the probe pod's Warning Events and counts a pod that has not started within
its two minutes as unsupported, with what it last showed.)* With the probe the test passes on the
reproduced config (user-namespace subtest skipped) and fails with the variable set; on a local Kind
cluster with overlayfs it runs the whole test, with the variable set, on both Valkey lines.
So the user-namespace half is **verified locally only**, never in CI.

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

**A third conditional skip arrived on 2026-09-26** with ADR 0032.
`TestDataWritableCheck_Executes` (`internal/builder/pod_security_test.go`) skips under euid 0,
because root passes every `-w` test and the refusals it exists to observe cannot occur. D53
covers part of what it would then hide: it runs the same script as uid 999 against the real
images, but only on a root-written AOF volume and on the same volume after the repair. The
hidden-file, empty-volume and `lost+found` cases run nowhere else. D6 grants the skip no
exception: on a root run of `make test-unit` it is a SKIP this rule counts as a defect, and that
stays open next to the second skip. A non-root
`make test-unit` on 2026-09-26 reported zero SKIPs, with `internal/builder` and
`internal/controller` uncached and the other packages from the cache, so it is not the `-count=1`
run this decision asks for. That the self-hosted runner is not root is read from the
workflow (it installs through `sudo`), not measured on a runner.

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

**D31 — E2E runs as a three-leg matrix with an aggregating gate job.**
~~Two legs, the full-suite one named `single-node` and the topology one `multi-node`~~
(superseded 2026-08-22, when D43 added the third leg): `single-node-valkey9` runs the full
suite; `multi-node-valkey9` (control-plane + **3** workers) runs
`E2E_RUN='TestE2E_AntiAffinity|TestE2E_PodDisruptionBudget'`; `single-node-valkey8` runs the
full suite against the other pinned line. The legs run in parallel on separate runner pods with
distinct cluster names. A separate `e2e-gate` job named "E2E Tests" aggregates all of them, so
the pre-existing required status check keeps one stable name while legs are renamed or added.

**Every leg is named for the Valkey line it runs and passes that line explicitly**, never an
empty selector that resolves to the default — including the leg that varies the node count,
whose result does not depend on the line at all. All three spellings run the same images today;
they differ on the day the default moves, and then an empty selector reports a green leg that
ran a line nobody chose for it. The cost is the honest one: moving the fleet to a new major
edits the names and the selectors together, which is what D43 means by calling that move a
decision.

**D32 — Three workers, never two.** Kind removes the control-plane `NoSchedule` taint only on
single-node clusters, so 2 workers plus a tainted control plane leaves 2 schedulable nodes and
the hard-spread test would still skip — the "≥ 2 nodes" lower bound would have reproduced
exactly the defect it was written to fix.

**D33 — Every cluster-setup step is node-count agnostic**, derived from `KIND_WORKERS`: the
kind config renders one worker line per worker via an explicit `while` loop (`seq` counts
*down* on BSD and emitted two workers for the zero-worker leg), and the sysctl step, the image
import and the kube-proxy settings all loop over `kind get nodes`. The image import matters
specifically because the operator runs with `pullPolicy: Never`, so on a multi-node cluster its
pod can land on any worker.

**D47 — A CI job that can fail the build is a required status check, enumerated here.** The
required contexts on `main` are exactly: `Code Linting`, `Cyclomatic Complexity`,
`GoSec Security Scan`, `Unit Tests`, `Integration Tests (envtest)`, `E2E Tests`,
`Vulnerability Check`, `Malware Scan (Source Code)`, `Container Malware Scan`,
`Generated Manifests Up To Date`, `Valkey Image Tools`, `Release Tooling` — twelve, matching
the twelve `needs:` of `semantic-release` minus `coverage-report`. **A new gate job is added to
branch protection in the same change that adds the job**, and the matrix legs are never
required by name; `e2e-gate` is the only E2E context (D31).

Two jobs are deliberately **not** required, and each for its own reason. `Combined Coverage
Report` is conditional (`if: always() && (unit || integration)`) and GitHub scores a skipped
required check as passing, so requiring it would buy nothing while risking a PR that can never
go green; it is a report, and the tiers it reports on are required already. `Semantic Release`
runs only on `push` to `main`, so on a pull request it never reports at all.

**D48 — A pseudo-versioned indirect dependency whose compatible version is dictated by a direct
one is not Renovate's to bump.** `k8s.io/kube-openapi` and `sigs.k8s.io/structured-merge-diff`
are disabled in [`renovate.json`](../../renovate.json) and left to MVS, which takes them from
`k8s.io/apimachinery`. Their tip is not a version the repo may hold independently: kube-openapi
has no release branches, the k8s release branch pins one digest per minor, and crossing the
digest where it swapped `structured-merge-diff/v6` for `/v7` made every package-loading job die
on `cannot use typeSchema.Types (… v7 …) as … v6 … in struct literal` — `go build`, `go vet`,
`golangci-lint`, `govulncheck`, unit and integration alike. **This is not a security carve-out:**
both still advance whenever the `k8s-go-modules` group does, which is the only version of them
that was ever supported; what is given up is a fix landing in the days between a kube-openapi
commit and the k8s patch release that adopts it.

**D50 — An e2e that forces a transient state pins the object identity it acts on, never a
state name.** A rolling-update state annotation names a *phase*, and a phase spans more than
one generation of the pod it is about. `jamPod0Replication` therefore waits for the pod-0 that
runs the **new** image and carries no `deletionTimestamp` -- the outgoing master still runs the
old one -- and reads the poisoned `masterauth` back rather than trusting the two `OK`s, because
a pod replaced between the two commands answers `OK` from the process that is going away.
Runtime `CONFIG SET` does not survive a pod replacement, so a jam that lands one second early
is silently discarded, Phase 1 then finds a healthy link on the replacement, and the test fails
on `TopologyRestored=True`/`Restored` having never entered the path it exists to cover
(measured on runs 35312304295, 35315129844 and 35317540380).

**The general rule is the one the four failures teach:** where a fixture manipulates a pod the
operator is concurrently replacing, "which pod" is part of the assertion and has to be
expressed as identity -- image, UID or `deletionTimestamp` -- not inferred from a controller
state that outlives the object.

*(Amended 2026-09-26: a second instance, found and fixed.)* The rule binds a fixture that
deletes a pod itself just as much. `TestE2E_SidecarFailoverDrainMaster`
([`sidecar_test.go`](../../test/e2e/sidecar_test.go)) deletes the master of a 3+3 Sentinel
cluster and then waited for the StatefulSet at 3/3, phase `OK`, every pod Ready and exactly one
pod answering master. The **terminating** old master satisfies every one of those: kubelet keeps
it Ready for its whole termination ([ADR 0026](0026-a-pod-being-deleted-is-not-available.md)),
and it still answers master. On the single-node Valkey 9 leg of the last full e2e run of the day
the delete subtest passed in 0.38 s. "Data survives failover" then picked the dying pod as the
master holding the keys, and its `DBSIZE`, sent by pod name, reached the empty replacement and
read 0. The operator log of that cluster shows no operator action between its creation and its
deletion. The pod logs were lost with the CR, so this diagnosis is read from the test code and
its timing, supported by the experiment below and not traced in the red run itself.

**Experiment, on Kind.** The unchanged test run alone went green 10 of 10, but 5 of the 10 took
the vacuous path, with the delete subtest finishing in 0.28–0.30 s. A watcher recorded role and
`DBSIZE` ~~of every pod~~ once a second *(corrected 2026-09-26: what is recorded is what it
showed for the new master and the replacement; that it covered every pod is not)*. It showed the new master holding all 50 keys, and the
replacement reading `dbsize=0` for several seconds while it resynchronised. So on the vacuous
path the verdict is timing: the `DBSIZE` is green when it reaches the dying master or a
resynchronised replacement, and red inside that window (inferred from the two observations, not
measured on one run).

**The fix names the pod by identity.** The subtest records the deleted pod's UID and waits for a
Ready pod of that name with a different UID (`waitForPodRecreated`) before any role or data
check. The fixed test ran 8 of 8 green on Valkey 9, with the delete subtest taking 8.3–10.3 s.
**Not verified:** no full suite has run on the fix, and 8 green runs on one host are a streak,
not a failure rate (the same limit as D50 itself, Residual risks). ~~Five more e2e sites wait on
controller state after deleting a pod~~ *(corrected 2026-09-26, read: five more e2e sites delete a
pod and none of them then waits for the replacement by UID; whether their waits can be met by the
terminating pod is what the audit has to establish)*, in `sidecar_test.go` (the replica drain),
`admission_recovery_test.go`, `sentinel_stale_master_test.go`, `pod_termination_test.go` and
`topology_abandon_test.go`. They are filed unaudited as T34 on
[the ticket board](../tickets/local_BOARD.md).

**D51 — Endpoint membership is read through `discovery.k8s.io/v1` EndpointSlice.** `v1 Endpoints`
is deprecated since Kubernetes 1.33 and each client using it logs
`Warning: v1 Endpoints is deprecated in v1.33+; use discovery.k8s.io/v1 EndpointSlice` once.
The operator never used the API -- it holds no `endpoints` RBAC marker and makes no such call --
so this was the e2e suite alone, in three helpers: two now read through one
`readyEndpointPodNames`, the same way D25 and D26 folded the duplicated Event pollers, and
`waitForServiceEndpoints` is deleted rather than migrated -- it had no callers, and a second
endpoint reader that nothing exercises is exactly what this decision says not to keep. The
reader collects across every slice of a Service and de-duplicates by pod: a Service owns one
slice per address type and more once it outgrows the per-slice limit, so the per-address count
it replaces would have counted a dual-stack pod twice. A nil `Conditions.Ready` counts as
ready, which is what keeps it equivalent to the ready-only `Subsets[].Addresses` it replaces.

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
*(Amended 2026-09-26, scope.)* "Every function" means every function a person writes:
`make cyclo` ignores `zz_generated` files as it ignores `_test.go`. controller-gen's
`(*ValkeySpec).DeepCopyInto` gains one branch per optional pointer field and reached 16 with
`spec.podSecurity` ([ADR 0033](0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md));
nobody reviews or edits that code, so the ceiling's reason does not apply to it, and the only
ways to satisfy it would have been an API shaped around a counter or a `nolint`, which this rule
refuses. Hand-written code stays under the ceiling without exception.

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

**Amended 2026-09-26 (ADR 0032 D2): the walker recognises two more command positions.**
`RequiredImageTools` gains `find` and `chown` for the migration-only ownership repair, the
one-liner `find /data ! -user 999 -exec chown 999:999 {} +`. The walker as written could see
neither: `commandsUsedBy` matched a tool at the start of a script, after a pipe or a separator,
and inside `$( )`, while the repair's `find` is the first word of an `sh -c` body and its
`chown` is the command `find -exec` runs. The pattern now also accepts `^sh -c` (anchored at the
start of the joined command) and `-exec`. And because `BuildStatefulSet` never inserts the
repair (the controller does, while legacy pods exist), `valkeyImageScripts` applies
`WithDataOwnershipRepair` to persistent fixtures itself, and a `persistent` fixture joins the
set. Take away any one of the three and the converse test fails on `find`, `chown` or both as
declared but unused, and, worse, a tool the repair gained later would pass the forward test
unseen. That is read from the test; none of the three was removed as a mutation. The "two init
container scripts" above (`init-config-selector` and `init-sentinel-config`) are now four: the
pre-flight `check-data-writable` on every persistent data pod (shell builtins only, so it
declares no tool) and the repair while it is in the template.

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

**D44 — A log scan ignores a retry only when the same log shows the recovery, and never a
pattern outright.** The data StatefulSet uses `podManagementPolicy: Parallel`, so all pods
start at once and a replica regularly reaches pod-0 before pod-0 binds its port: Valkey logs
`Error condition on socket for SYNC: Connection refused`, retries a second later and
synchronises. `dropResolvedSyncRetries` ([`test/e2e/tls_test.go`](../../test/e2e/tls_test.go))
removes that line only when a later line reports the sync that followed it, so a replica that
never synchronised -- and a master that went away after a successful sync -- still trips the
pattern.

Dropping `"Connection refused"` from the pattern list would have been the smaller edit and the
worse one: it is exactly the message a wrong port, a wrong hostname or a dead master produces,
and the assertion exists to catch those. The filter is pinned by a table test built from the
ordering that failed CI, including the two cases where the line must survive
([`test/e2e/tls_log_filter_test.go`](../../test/e2e/tls_log_filter_test.go)).

**D45 — A failing e2e leaves behind the evidence that names the cause, and it collects it
while the objects still exist.** Three parts, because each covers what the others cannot:

* The assertion dumps the pod at the moment it fails --  restart count, last termination
  state, the previous container log when it restarted, DBSIZE, the replication and
  persistence sections, and the pod's events (`valkeyPodForensics`,
  [`test/e2e/e2e_test.go`](../../test/e2e/e2e_test.go)). This is the only dump taken while
  the cluster is still in the failed state.
* A failed test keeps its namespace (`createNamespace`), so the post-run collection has
  something to read. Events outlive the pods they describe and answer "killed, evicted or
  unhealthy" even for a pod the test already tore down.
* The workflow collects node conditions and per-namespace pods and events, not only pod
  logs -- a pod that lost its process to node pressure leaves its reason there and nowhere
  else.

The rule this encodes: **a red e2e whose cause cannot be distinguished from a different
cause is a second defect**, and it is fixed in the same pass as the first. The cost is that
a failed run leaves one namespace running for the rest of the suite, on a cluster that is
deleted at the end of the job anyway.

**D46 — The release tooling is exercised where its updates land: in PR CI, against the
committed lockfile.** Three rules:

* [`hack/verify-release-tooling.mjs`](../../hack/verify-release-tooling.mjs) drives
  `analyzeCommits` and `generateNotes` through the plugin configuration read from
  [`.releaserc.json`](../../.releaserc.json), against a synthetic commit set covering patch,
  minor and breaking commits, and asserts the rendered notes contain the type sections and
  the commit subjects. Both observed failure modes stay covered: a render that throws
  (preset 10.4.0) and a render that silently drops every section (preset 10.0.0–10.3.0) —
  the second is the one a smoke test that only checks the exit code would bless. The script
  runs as the `release-tooling` job on every PR and push, via `make test-release-tooling`
  locally, and the `semantic-release` job lists it in `needs`.
* `package-lock.json` is committed and both jobs install with `npm ci`, so the release job
  runs the byte-identical tree the PR tested. Before, `npm install` resolved transitive
  ranges at run time — the preset's `@conventional-changelog/template ^1.3.0` floated to a
  version published days after the pin that referenced it.
* `conventional-changelog-conventionalcommits` stays pinned on the 9.x line, the last one
  that ships handlebars string templates for writer@8, until
  `@semantic-release/release-notes-generator` ships conventional-changelog-writer@9. A
  Renovate PR bumping the preset to 10.x goes red in `release-tooling`; that red is the
  signal that upstream is still incompatible, not an obstacle to work around.

### The rootless posture ([ADR 0032](0032-generated-pods-run-rootless.md))

**D52 — Pod Security is asserted with the checks the API server runs, not a restatement of
them, and from both sides.** [`pod_security_test.go`](../../internal/builder/pod_security_test.go)
evaluates the rendered templates with `k8s.io/pod-security-admission/policy`, the library behind
the API server's PodSecurity admission (`DefaultChecks()`, `LatestVersion()`):

* `TestPodSecurity_EveryRenderedTemplateIsRestricted`: every template is `restricted`-allowed.
  The matrix is {standalone, 3 replicas, 3+3 Sentinel} × TLS × auth × metrics × persistence
  {off, rdb, aof}, which is 72 CRs and 168 templates (data, observer, and Sentinel where enabled).
* `TestPodSecurity_TheRepairIsBaselineButNotRestricted`: the data template after
  `WithDataOwnershipRepair` is `baseline`-allowed **and** `restricted`-denied, on all 48
  persistent rows. The denial is the matrix's other side: an evaluator wired to allow
  everything fails here.
* `TestPodSecurity_EvaluatorRefusesTheLegacyShape`: the D11 positive control against the shape
  the posture replaces. One 3-replica data template with every `securityContext` stripped,
  which is what every cluster ran before ADR 0032, must fail `restricted`. The repair's denial
  shows the evaluator can refuse at all; this one shows that it refuses the missing posture, so
  the matrix passes because of the posture.

Pod Security does not require a read-only root filesystem, and after `drop: [ALL]` it lets a
container add `NET_BIND_SERVICE` back. So `TestPodSecurity_EveryContainerHasTheFullPosture`
checks the container half of ADR 0032 D1 field by field on every container of the same matrix:
`allowPrivilegeEscalation: false`, `readOnlyRootFilesystem: true`, `drop: [ALL]` and an empty
`add`. The pod-level identity (uid, gid and `fsGroup` 999, `fsGroupChangePolicy` unset) and
`workingDir: /data` are pinned on single fixtures, not on the matrix
(`TestPodSecurity_DataAndSentinelPodsRunAsTheValkeyUser`,
`TestPodSecurity_ValkeyContainerStatesItsWorkingDir`).

The module sits at the `k8s.io/api` version (v0.37.0 for both). Only that one `_test.go` file
imports it, so no binary links it. `go.mod` lists it as a direct `require` anyway, because a
module file does not separate test imports, and it brings in `k8s.io/component-base` as
indirect. Renovate's `k8s-go-modules` group matches `^k8s.io/`, so the library moves in the
same PR as the API types the templates are built from. Run on 2026-09-26 with `make test-unit`:
168 + 48 + 1 green. The revert check that the test's doc comment names has **not** been
recorded (D7).

**D53 — The restricted posture is run inside the real images, on both pins, in the imagetools
tier (D42).** [`restricted_runtime_test.go`](../../test/imagetools/restricted_runtime_test.go)
turns the posture into Docker flags: `--user 999:999 --read-only --cap-drop ALL
--security-opt no-new-privileges`, plus a tmpfs owned by 999 for each `emptyDir`. On both D43
pins it runs:

* `valkey-server` with RDB and AOF writes and an AOF rewrite, reading `Uid: 999`, `CapEff: 0`
  and `NoNewPrivs: 1` off `/proc/1/status`, which is the `sh` that starts `valkey-server` and
  passes those credentials on to it;
* `valkey-sentinel` persisting a `SENTINEL SET` into its config file;
* the drain preStop hook releasing on the marker.

This makes the T31 measurements permanent. A Valkey release that needs root, a capability or a
writable root filesystem fails on the Renovate PR that brings it. The file has the same build
tag and target as D42, so it runs in the existing `Valkey Image Tools` job, and D47's twelve
contexts are unchanged.

**The migration is one sequence on one volume** (`TestRestrictedRuntime_PreflightAndRepair`):

1. A root writer leaves the legacy AOF shape: `/data` at `0755` and files owned by root.
2. The pre-flight, run as 999, must **fail** and name `chown -R 999:999`. This refusal is the
   positive control that the check can fail at all.
3. The repair runs as uid 0 with `--cap-add CHOWN` and nothing else, and must succeed.
4. The pre-flight must now pass.

The order is the assertion. A pre-flight that passed the legacy volume, or a repair that needed
more than `CAP_CHOWN`, fails it.

**The commands under test come from the builder, not restatements**: `ProbeCommand`, the preStop
hook, and the pre-flight and repair scripts of the data template after
`WithDataOwnershipRepair`. `valkey-server` and `valkey-sentinel` themselves start from
hand-written flags and a minimal config, not the generated one, and the probe is the plaintext
variant without auth. The unit tier also executes the pre-flight (D19), but under euid 0 it has
to skip (D6). This tier runs the refusal as 999 no matter who runs the suite.

What it cannot see is a Kubernetes node: containerd's `RuntimeDefault` profile (Docker's moby
profile is what runs here), kubelet's `fsGroup` handling and the projected-token mode. Those
belong to D54, with one gap: Kind's local-path volumes are `hostPath` (the premise D55
asserts; measured on 2026-09-26 as `hostPath` `DirectoryOrCreate` with a `0777` root-owned
volume root), and kubelet applies no `fsGroup` to them, so `fsGroup` on a
persistent volume runs in no tier. Run on 2026-09-26 with `make test-image-tools`: all four
restricted-runtime tests and the rest of the package green on both pins.

**D54 — The restricted-namespace e2e makes the API server the Pod Security oracle.**
`TestE2E_PodSecurity_RestrictedNamespace` ([`test/e2e/pod_security_test.go`](../../test/e2e/pod_security_test.go))
labels its namespace `pod-security.kubernetes.io/enforce: restricted` (`enforce-version:
latest`) and creates four clusters:

* a persistent standalone pod with AOF, which brings the pre-flight;
* 3 replicas with TLS, auth and metrics;
* 3+3 Sentinel with TLS and the observer;
* a plain 3-replica cluster for the drain.

The API server refuses a pod that violates the profile at creation, and its StatefulSet then
never becomes Ready. So "every StatefulSet and the observer Deployment Ready, every CR `OK`"
measures admission in the component that enforces it, not in a library copy of it (D52).
Before any cluster, a pod with no `securityContext` is created by server-side dry run and must
be refused with `Forbidden`: the positive control (D11) that the namespace enforces at all. On
top of that it checks:

* `Uid: 999`, `CapEff: 0`, `CapBnd: 0` and `NoNewPrivs: 1` in `/proc/1/status` of the `valkey`
  and `sentinel` containers: the identity kubelet and containerd actually applied.
* An AOF write, then an AOF rewrite and an RDB snapshot, each awaited to completion.
* TLS and auth data reaching every replica.
* The sidecar labelling the master, which shows the token is readable under `fsGroup`.
* A Sentinel image roll 8 → 9 and a drain failover, each with zero Warning Events (ADR 0025 D7).

Valkey starting, writing and replicating in those pods is what runs it under containerd's
`RuntimeDefault` profile, which D53 cannot see; no subtest reads the seccomp mode itself.

It is a plain `e2e`-tagged test. Both single-node legs run it, and the multi-node leg's
`E2E_RUN` leaves it out (D31). It ran green on 2026-09-26, locally and not in CI: in the full
`make test-e2e` on Kind (control plane + 3 workers, Kubernetes v1.36.1, containerd) with
`E2E_VALKEY_LINE=9` and with `E2E_VALKEY_LINE=8` (51/51 each), both re-run green on the final
operator image. Every subtest above passed, the positive control included. *(Rerun 2026-09-26 on
one operator image built from ~~the final code of the branch~~ the code before ADR 0025 D9's
clock *(corrected 2026-09-26, Status)* — ADR 0033's allow-list and CEL path
rule and ADR 0025 D9 included — on Kind with Kubernetes 1.36.1, containerd 2.3.1, runc 1.4.2 and
Linux 6.10: green inside both full suites, 53/53 on each line, and in two further Valkey 8 runs
of this test ~~alone~~ outside the suite, together with ADR 0033's hardening e2e (`E2E_RUN`
naming both; corrected 2026-09-26). The suite count grew from 51 by
`TestE2E_RollingUpdate_TwoSentinelsRollSerially` and ADR 0033's
`TestE2E_PodHardening_UserNamespacesLocalhostSeccompAndDigest`; 53 is every `Test` function
under the `e2e` tag without `fleetupgrade` or `e2e_helm`, read from the build tags of
`test/e2e`.)* *(Rerun again 2026-09-26 on the last image of the day, with ADR 0025 D9's clock:
green inside both full suites — 53/53 on Valkey 8, 52/53 on Valkey 9 with another test failing
(D50) — and in two further Valkey 8 runs with the hardening e2e.)* It stays open until both CI
legs that run it have recorded a green run.

**D55 — The fleet-upgrade e2e is the migration proof. Its premise is asserted first, and it
runs locally only.** `TestE2E_FleetUpgrade` (tags `e2e,fleetupgrade`) is the only test that
starts from pods and volumes a released operator built as uid 0, from chart `E2E_UPGRADE_FROM`
(default 1.10.48). T31 adds four members:

* persistent AOF and RDB 3-replica clusters;
* a persistent single pod and a non-persistent one (the two sides of ADR 0032 D3).

Each persistent master runs a `BGSAVE` first, so the dataset is on the volume the way a root
process wrote it. The test then asserts:

* every rolled data and Sentinel pod is built rootless (pod `runAsNonRoot: true`,
  `runAsUser: 999`);
* the **second roll** of ADR 0032 D2 finished on every persistent member: the template and
  every ordinal are free of `fix-data-ownership`, every ordinal is Ready, and the phase is `OK`;
* keys are intact on every replica, not only on the master;
* on every ordinal of a persistent member, everything `stat -c %u` reads (`/data`, its entries,
  `appendonlydir` and its entries) is owned by uid 999;
* every multi-replica data tier completed its roll, counted as `RollingUpdateComplete` Events
  since the upgrade: the two non-persistent members exactly one, the persistent 3-replica
  members exactly two (~~at least one~~, restored 2026-09-26 with the ordering fix below); and
  each Sentinel tier exactly one (`SentinelUpdateComplete`);
* data and Sentinel pod UIDs stay unchanged over a 90 s window after that, so no third roll
  happened;
* the persistent single pod was replaced (its UID changed), and the non-persistent one was not
  replaced and reports `PodSecurityUpdatePending=True/PodRunsAsRoot`.

**Amended 2026-09-26, the same day, when the open question of ADR 0032 D2 was decided.** The
first version of this list asserted the opposite of the second roll: every persistent pod
carried the repair in its spec with exit code 0 (`requireRepairRan`, now removed), and the 90 s
window showed that *no second* roll happened. The second roll deletes the pods whose status
held that exit code, so the evidence moved from the container to its effect. Before the
upgrade `shapeLegacyVolumes` requires a root-owned `/data` and files written by uid 0; a
`hostPath` volume gets no `fsGroup`, and uid 999 cannot take over what root owns. A 999-owned
`/data` afterwards is therefore something only the repair (uid 0, `CAP_CHOWN` and nothing else)
can have produced. The persistent single pod now restarts twice; the test asserts that it was
replaced, not the count, and the second restart shows only as the repair-free pod the
second-roll wait demands.

~~A persistent tier's completed rolls are asserted as at least one, not two.~~ *(Superseded
2026-09-26, later the same day: the first run of the changed test measured one completion per
persistent tier where it expected two, which is the defect this paragraph had rationalised. ADR
0032 D4 now keeps the repair while a data-tier roll is recorded and counts a pod as migrated
only once Ready, so the first roll finalizes before the second starts, and the count is exactly
two again. The count is therefore a test of the ordering, not noise to be loosened around; the
superseded reasoning follows unchanged.)* The repair leaves the
template as soon as every ordinal holds a migrated pod, and the last one counts once its
pre-flight has exited 0, before it is Ready (`dataOwnershipRepairNeeded`). That can be before
the roll that replaced it has finished, so whether the second roll ends in an Event of its own
depends on the state that roll is in; the test's comment names the path on which it simply
continues the first. That is read from the code and has not been measured. The count is
asserted exactly only for the non-persistent members, where nothing can vary it. The proof that
the second roll happened is the wait above, which requires that no pod carries the repair.

**The premise is checked before any of that (D29).** kubelet applies no `fsGroup` to a
`hostPath` volume, so on Kind the repair is the only thing that can re-own root-written data.
On a volume type that supports `fsGroup`, every migration assertion would pass with the repair
doing nothing. `requireHostPathVolume` therefore fails the test, printing the PV source, if the
PV bound to `data-<name>-0` of any persistent member is not `hostPath`.

The other premise, that the data really is root-written, is asserted from the other end. If
`E2E_UPGRADE_FROM` ever moves past the release that ships ADR 0032, no legacy pod exists and the
repair is never inserted. The test then fails instead of passing, in two places:
`shapeLegacyVolumes` before the upgrade, because it requires a root-owned `/data` and files
written by uid 0, and the ownership read after it, because without the repair nothing re-owns
the volume root Kind creates as root (D53), so `/data` still reads uid 0. Both are read from
the code; no run has started from such a release. Until the amendment above, `requireRepairRan`
was the check after the upgrade. The roll count is not one: it asks a persistent tier for at
least one roll, which a later from-version can still produce.

The no-third-roll window is sampled, not proved (D9). The deterministic guards are in the unit
tier. `TestWithDataOwnershipRepair_IsHashNeutral` shows the repair never enters the hash, so
neither template write is itself a roll.
`TestReconcileStatefulSet_RepairComesAndGoesAndTheRetiredRepairRolls` shows a tier whose pods
carry no repair does not roll, a tier whose pods carry the retired one deletes one pod, and the
repair does not return to the template. `TestPodCarriesRetiredRepair` and
`TestHandleStandaloneRollingUpdate_ReplacesAPodCarryingTheRetiredRepair` pin the comparison and
the single pod. All four passed in `make test-unit` on 2026-09-26 with zero SKIPs, every package
from the test cache, so not the `-count=1` run D6 asks for. Only the hash test's doc comment
names a revert check, and none of the four records a failure message (D7).

**It is not a CI job**, for the reason the file gives: it reinstalls the operator that every
other e2e runs against, so it needs a cluster of its own (`make e2e-fleet-upgrade-local`).
Whether it becomes a CI job is a separate decision. Until then it proves the migration only on
runs someone records.

**Recorded run, 2026-09-26, local Kind, of the first version of the test** against the operator
before the second roll existed (control plane + 3 workers, Kubernetes v1.36.1,
containerd): `make test-e2e-fleet-upgrade E2E_UPGRADE_FROM=1.12.8` passed in 253 s, from the
released chart 1.12.8 to the local chart. The default starting point 1.10.48 has **not** run:
its released images are amd64-only, and the arm64 host answers "no match for platform in
manifest". 1.12.8 ran with its amd64 image loaded into Kind under emulation. The fleet was six
members on Valkey 9.1.1: 3+3 Sentinel with TLS, a plain 3-replica cluster with the observer,
3-replica AOF and RDB persistent clusters, a persistent single pod and a non-persistent one;
every persistent volume root was set to `0755` root before the upgrade (`shapeLegacyVolumes`).
Green, on that version and not on the amended list above:

* the Kind PV is `hostPath`;
* every multi-replica and Sentinel cluster converged, all pods rootless, keys on every replica;
* every persistent pod ran `fix-data-ownership` with exit 0, and the repair then left the
  template;
* the migrated persistent masters wrote and snapshotted, with no `MISCONF`;
* the existing observer Deployment received the posture (ADR 0032 D5);
* each Sentinel tier completed exactly one roll (one `SentinelUpdateComplete` Event);
* no pod was replaced in the 90 s after the repair left the template;
* the persistent single pod restarted once and kept its keys; the non-persistent one was not
  restarted, kept its keys and reports `PodSecurityUpdatePending=True/PodRunsAsRoot`;
* no `ReconcileBlocked`, no ownership refusals, and the pre-upgrade hook completed.

Three pre-existing bugs in the test were fixed on the way: the cleanup scope, chart paths
resolved relative to the package directory, and a hook assertion that demanded a Job the chart
deletes on success. One local run on one host is what this item records; it is still not a CI
job.

~~**The amended test is NOT EXECUTED (D30).**~~ *(Executed 2026-09-26, locally on Kind and not
in CI, from 1.12.8 as above.)* Two data points of the run above describe the
behaviour ADR 0032 D2 then turned down: no pod replaced in the 90 s after the repair left the
template, and a persistent single pod restarted once. They stay recorded as what that code did.
The run checklist for the amended test is under Residual risks.

**Runs of the amended test, 2026-09-26.** The first two runs failed, each on a defect in the
order of the two data-tier rolls, not in the test: first one `RollingUpdateComplete` per
persistent tier where the list above demands two, then the repair stranded in the template after
the first roll. Both are fixed in [ADR 0032](0032-generated-pods-run-rootless.md) D4. The rerun on
the fix passed, and so did a run on one operator image built from ~~the final code of the
branch~~ the code before ADR 0025 D9's clock *(corrected 2026-09-26, Status)* (ADR 0033's
allow-list and CEL path rule, ADR 0025 D9), on Kind with Kubernetes 1.36.1,
containerd 2.3.1, runc 1.4.2 and Linux 6.10. Every assertion in the list above is exact where it
says so, so a green run means each held: the second roll finished on all three persistent
members, `fleet-aof` and `fleet-rdb` completed exactly two data-tier rolls, `fleet-ha` and
`fleet-plain` exactly one, each Sentinel tier exactly one, every ownership read on a persistent pod was uid 999,
and nothing rolled in the 90 s after the second roll. *(Green again 2026-09-26 from 1.12.8 on the
last image of the day, with ADR 0025 D9's clock and ADR 0010 D14's single failover write.)* Still
a local run on one host; it is not a CI job.

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
  (control-plane + 3 workers against a single-node leg's one).
  Renaming a guarded test requires updating the grep and `E2E_RUN` in the same change.
* The repo's headline coverage number is permanently capped by the wiring boundary (D34), and
  the exhaustive 0% list must be re-stated each pass so the gap stays a decision.
* The Makefile becomes the contract between local development and the pipeline: any new tool or
  flag has to be added as a target before it can be used.
* Known, cheap defects stay open across passes (D38), and the open list grows faster than it
  closes. Accepted.
* Documentation sections grow correction blocks rather than shrinking (D37); a reader must read
  a section to its end before quoting it.
* **Branch protection is repository state, not a file in this repository** (D47). Nothing in a
  PR proves the list is still complete; the enumeration above is the only record, and it goes
  stale silently if a job is added without touching it.
* A generator-tool bump (controller-tools, kustomize) now **blocks its own automerge** until a
  human runs `make generate-all` and commits (D47). That red PR is the intended outcome, not a
  regression — Renovate runs inside the action's container, which carries no Go toolchain, so it
  cannot regenerate for itself.
* `k8s.io/kube-openapi` and `sigs.k8s.io/structured-merge-diff` no longer appear in any Renovate
  PR (D48), so their movement is invisible until the `k8s.io/*` group bumps.
* **A flaky required check now blocks every merge (D47).** That is the cost the 2026-09-18
  required-contexts change bought and it was not visible when the change was written: three
  E2E legs run per push, and one intermittent failure in any of them stops the queue where
  it used to be waved through. D50 removes the one known instance; the policy answer for the
  next one is to fix or quarantine it, never to drop the context.
* `bin/` now holds seven tools instead of four (D49); ~~a stale one is deleted, not upgraded in
  place, because `go-install-tool` skips whenever the file exists. A version bump therefore
  needs `rm bin/<tool>` locally — CI starts from an empty `bin/` and never sees it.~~
  *(Superseded 2026-09-26 by the D49 amendment: the path carries the version, so a bump is a
  missing file and installs itself; nobody had run the `rm`, and the stale controller-gen turned
  `Generated Manifests Up To Date` red.)* Every bump leaves the previous binary behind in
  `bin/`, unreferenced.
* The unit tier now depends on `k8s.io/pod-security-admission` and, through it,
  `k8s.io/component-base` (D52). Both are test-only and move with the `k8s.io/*` group.
* **The migration path of ADR 0032 has no CI gate that starts from real legacy data** (D55).
  CI covers its parts: the evidence, the hash neutrality and the second roll in the unit tier,
  and the repair on a Docker volume in the imagetools tier. The whole path, from a released
  operator's pods to a rootless fleet, is proved only on runs someone performs and records by
  hand.
* The fleet e2e now waits for a second roll of every persistent member (D55), so it runs longer
  than the 253 s recorded for its first version. ~~How much longer has not been measured~~
  *(measured 2026-09-26, one run on the ~~final~~ pre-clock image, Status: 288 s)*; the `make` target's 45 min timeout
  was not changed.

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

### Require branches to be up to date before merging (`strict: true`)

Rejected for now. It would not have caught either break — both were red on the PR itself, not
only after merging — and with `prConcurrentLimit: 0` every merge would invalidate every other
open Renovate PR and re-run a three-leg E2E matrix on shared self-hosted runners. The cheap
guarantee is D47; a merge queue is the version of this worth revisiting, not `strict`.

### Teach Renovate to run `make generate-all` via `postUpgradeTasks`

Rejected: `renovatebot/github-action` runs Renovate in its own container, which has no Go
toolchain and no `controller-gen`, so the task would fail or silently no-op — and a
regeneration that silently no-ops is exactly the failure D47 exists to stop.

### Pin `k8s.io/kube-openapi` to a digest and let Renovate keep proposing bumps

Rejected: that is the state that broke, one PR later. The pin is not the mechanism; MVS from
`apimachinery` is (D48).

### Fix the `v6`/`v7` split by adding a direct `require` on `structured-merge-diff/v7`

Rejected: the conflict is inside `apimachinery`'s own source, which constructs a `v6` struct
from what kube-openapi now returns as `v7`. No version selection in this module can reconcile
that; only a kube-openapi digest from apimachinery's own compatibility window can.

### Assert the Pod Security profile as hand-written field checks

Rejected for the admission question (D52): a restatement is exactly what a reader has to trust,
and it stays silent when the profile gains a check. Field assertions are kept for what the
profile does not require (the read-only root filesystem, and an empty `capabilities.add` where
Pod Security would allow `NET_BIND_SERVICE`; `drop: [ALL]` itself it does require).

### Let the restricted-namespace e2e carry the Pod Security question alone

Rejected: it covers four cluster shapes, not the 72-row matrix, and it cannot express the
repair template's "baseline, but not restricted" at all. The repair is inserted only while
legacy pods exist, and a restricted namespace would simply refuse it.

## Residual risks

* **The drift guard sees only a vocabulary (D42).** `shellCommandCatalog` covers the
  coreutils and busybox applets a container script realistically uses. A generated script
  that calls something outside it is not noticed, and the image check then never asks for
  that tool.
* **The tool check proves presence, not behaviour (D42).** `command -v` finds a busybox
  applet as readily as the GNU tool, and the two differ in flags. The shell-construct test
  covers the constructs the scripts rely on; it does not cover, for example, a `timeout`
  with a different signature. ~~Executing the real scripts inside the image was considered and
  deferred as disproportionate for the observed risk.~~ Superseded in part on 2026-09-26 by D53.
  The probe (plaintext, no auth), the drain preStop hook, the pre-flight and the ownership
  repair now run inside both pinned images. The two original init container scripts, the
  auth-wrapped container command and the auth and TLS probe variants still do not.
* **Only the two pinned images are checked (D42).** `spec.image` is a user field with no
  operator default, so a cluster may run any image -- `valkey/valkey:9-alpine` being the
  realistic one. Measured on 2026-08-22: that variant provides every required tool. Nothing
  keeps it that way, and nothing checks it on a schedule.

* **The abandon-path e2e ran only after this ADR was written.** Its load-bearing premise — a
  replica with `masterauth` set against a master with no `requirepass` must abort the handshake
  at AUTH — follows from the Valkey/Redis handshake and was first exercised by the test itself:
  D50 records it on CI legs, and it passed in both local full-suite runs of 2026-09-26 (Kind,
  Valkey 9 and Valkey 8) *(and in both on the last image of that day, Status)*. A premise that does not bite fails loudly at the
  `TopologyRestored=False` wait and cannot pass falsely, so those passes are the measurement.
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
* **The release-tooling check drives the two plugins directly, not `npx semantic-release`
  (D46).** A break confined to semantic-release core — plugin loading, CLI flags, the
  verifyConditions of the git and github plugins — is outside it; those need repository
  credentials a PR must not hold, and in both observed incidents they failed loudly, not
  silently. The synthetic context also freezes the plugin API shape of semantic-release 25:
  a future major that changes what `generateNotes` receives fails the check, which is the
  point, but the failure will name this script rather than the incompatibility.
* **The header-only release notes of v1.10.26 through v1.10.48 stay as published (D46).**
  Nothing regenerates them; the commits they cover are in the git history and the compare
  links still work.
* **Nothing enforces D47.** The twelve contexts were set through the GitHub API on 2026-09-18
  and verified by reading the endpoint back; no test, job or file compares them against the
  workflow's job names, so a thirteenth gate job added without a matching API call repeats the
  exact failure this decision was written for. Making `semantic-release`'s `needs:` list the
  single source and diffing it against branch protection was **not** done.
* **`enforce_admins` is false and no review is required.** Not changed here, and not evaluated:
  a repository admin can still merge past all twelve checks, and every merge to date was an
  unreviewed automerge by `guided-traffic-bot`.
* **The 15 days of red `main` were never noticed by a human.** D47 stops the next one at the
  merge; nothing alerts on a red default branch, and that was not addressed.
* **D50 is verified by mechanism, not by repetition.** The operator log of two failing runs
  shows the jam landing between the demote and the delete, and the new gate cannot match that
  pod; what has *not* been done is running the test enough times to show the failure rate is
  now zero. A test that failed intermittently needs a green streak, not one green run, and the
  streak is what CI will or will not produce. *(The 2026-09-26 instance of the D50 amendment
  has the same limit: 8 green runs of the fixed test on one host.)*
* **D50 leaves the window itself intact.** The jam still has to land between pod-0 becoming
  Ready and Phase 1 observing a healthy link -- roughly ten seconds in the runs measured. It is
  now the *only* window rather than one of two, and the read-back makes a missed jam retry
  instead of pass, but a sufficiently slow `kubectl exec` can still miss it and would then fail
  on the `jamPollTimeout` with a message naming the image, not on a confusing `Restored`.

* **D49's `govulncheck` pin freezes what CI scans with.** It ran `@latest` before, so the
  scanner now advances only when Renovate bumps `GOVULNCHECK_VERSION`. The vulnerability
  database is fetched from `vuln.go.dev` at run time and is unaffected; a missed *scanner*
  improvement is the accepted cost of the three tools being the same ones locally and in CI.

* **D48 was verified by construction, not over time.** `go mod tidy` with kube-openapi pinned
  back to `v0.0.0-20260821135717-be32def86098` (the digest `main` last built green with)
  dropped `structured-merge-diff/v7` and the tree builds, vets, lints and passes unit,
  integration, gosec and govulncheck locally. Whether the next `k8s.io/*` group bump carries a
  kube-openapi digest that is itself consistent has not been and cannot be checked in advance.

* **D52 evaluates the profile of the library version in `go.mod`, not of the cluster.** A newer
  API server that adds a `restricted` check passes D52 until the `k8s.io/*` group bump arrives,
  and D54 sees only the Kubernetes version the Kind image of CI runs.
* **D55 checks its `hostPath` premise on ordinal 0 only.** File ownership before the upgrade is
  read on every ordinal of the persistent members (`shapeLegacyVolumes`: `/data` root-owned,
  files of uid 0), and so is ownership after it (uid 999). A from-version past ADR 0032 fails
  loudly (D55).
* **The new e2e waits honour D25.** The T31 and T32 e2e code of 2026-09-26 waits through
  `pollUntil` (`test/e2e/pod_availability_test.go`), a `wait.PollUntilContextTimeout` wrapper
  with an explicit interval and budget that fails with the last observed value; the eleven
  `require.Eventually` sites the first version had were converted before the change landed. The
  pre-existing sites of D25's open item are untouched. The later amendment of the same day keeps
  to it: D55's second-roll wait is a `pollUntil`, and the two-Sentinel e2e waits through
  `PollUntilContextTimeout` helpers. Like the first T32 e2e, it still reaches `findMasterPod`
  and `waitForConnectedReplicas`, two pre-existing `require.Eventually` helpers of that open
  item.
* ~~**Two e2e of the later 2026-09-26 amendment are NOT EXECUTED (D30).**~~ *(Both executed
  2026-09-26, locally on Kind and not in CI, and green on one operator image built from ~~the final
  code of the branch~~ the code before ADR 0025 D9's clock *(corrected 2026-09-26, Status; both
  green again on the last image of the day)*; per checklist below, what was recorded and what was
  not.)* Both run
  checklists name what to check beyond PASS:
  * The amended `TestE2E_FleetUpgrade` (D55). Run it the way the recorded run did
    (`make test-e2e-fleet-upgrade E2E_UPGRADE_FROM=1.12.8` on an arm64 host, where 1.10.48 has
    no image) and record: the second-roll subtest passing for all three persistent members; the
    `RollingUpdateComplete` counts (exactly 1 for `fleet-ha` and `fleet-plain`, asserted; ~~1 or
    2 for `fleet-aof` and `fleet-rdb`, which the test accepts either way, so write down which~~
    *(corrected 2026-09-26: exactly 2, asserted since the ordering fix of ADR 0032 D4, see D55)*);
    the ownership read printing exactly `999` on every ordinal; and the runtime against 253 s.
    Also check `fleet-aof` and `fleet-rdb` for a `TopologyRestoreAbandoned` Warning. By reading,
    not by any run: `handlePostManualFailover` asks `podOutdated` of the new pod-0, the repair
    leaves the template once that pod's pre-flight exits 0, and from then on the pod counts as
    not yet replaced and nothing in that state deletes it. So the first roll could wait out
    `syncTimeout` (5 min by default) and abandon the restoration before the second roll starts.
    The test fails on no Warning, so only the Events show it.
    *(Recorded 2026-09-26: the run on the ~~final~~ pre-clock image *(Status)* is green, so the second-roll wait passed for
    all three persistent members, the counts held exactly — 1, 1, 2, 2 — and every ownership read
    was `999`; all three are assertions. ~~Not recorded: the runtime, and whether `fleet-aof` or
    `fleet-rdb` carried a `TopologyRestoreAbandoned` Warning — the green counts do not exclude one,
    because an abandon hands over to `verifying-topology`, whose exit still emits
    `RollingUpdateComplete` (`abandonTopologyRestoration`, read).~~ *(Both read 2026-09-26 from
    that run's output and operator log: `TestE2E_FleetUpgrade` took 288 s, against 252.77 s for
    its first version. The green counts alone would not exclude an abandon, for the reason just
    struck, but the log does: it holds no `Topology restoration stalled` line — which
    `abandonTopologyRestoration` logs immediately before it emits `TopologyRestoreAbandoned` — for
    any fleet namespace, only the two of the dedicated abandon e2e.)* The path read above no longer
    exists as described: since ADR 0032 D4 the repair stays in the template while any data-tier
    roll is recorded (`dataOwnershipRepairNeeded` returns true while the template carries it and a
    rolling-update state is set, read), so it cannot leave before the first roll finalizes.)*
  * `TestE2E_RollingUpdate_TwoSentinelsRollSerially`
    ([`pod_availability_test.go`](../../test/e2e/pod_availability_test.go),
    [ADR 0024](0024-the-sentinel-tier-reports-its-own-completion.md) D10). A plain `e2e` test:
    both single-node legs run it, and the multi-node leg's `E2E_RUN` leaves it out (D31). It rolls
    a cluster of 3 data pods and 2 Sentinels from `UpgradeFrom` to `UpgradeTo` and asserts both
    Sentinels replaced and on the new image, `SentinelUpdatePending=False`, phase `OK` and a key
    kept. It does not observe that the deletes were serial; that is the unit tier's claim
    (`TestSentinelRollingUpdate_SmallTiersRollSerially`, whose doc comment names its mutation,
    and `TestSentinelDeleteKeepsVotes`, both green in the cached `make test-unit` above). So the
    run reads the operator log: two `Deleting sentinel pod for rolling update` lines for
    `two-sen-sentinel-*`, the second after the first replacement reported Ready. Its positive
    control (D11) is the guard before ADR 0024 D10. With it the tier never rolls, and by reading
    the code the test then fails at the `SentinelUpdatePending=False` wait; that revert has not
    been run. *(Recorded 2026-09-26: green inside both full suites on the ~~final~~ pre-clock image *(Status)*, Valkey 9
    and Valkey 8, and in an earlier run on both lines. ~~Not recorded: the operator-log read of the
    two serial deletes, so the one-at-a-time order still rests on the unit tier~~ *(read the same
    day from the ~~final~~ pre-clock run's operator log: per leg exactly two such lines, `two-sen-sentinel-0`
    then `two-sen-sentinel-1`, eight seconds apart in separate reconciles — 12:33:42 and 12:33:50
    UTC on Valkey 9, 12:43:30 and 12:43:38 on Valkey 8. The log does not print the replacement's
    readiness; that the second delete waited for it is `sentinelDeleteKeepsVotes`
    (`readyCount-cost >= total-1` on a tier of two), read. The e2e still does not assert the
    order)*; the positive control is still not run.)*

## References

* [`Makefile`](../../Makefile) — every target, and the recorded reason `-short` is absent
* [`internal/controller/`](../../internal/controller/) — `newTestReconciler`, `fakeValkeyServer`, `stsForValkey`, `podFromStsTemplate`, `failOnlyCRUpdate`
* [`internal/sidecar/`](../../internal/sidecar/) — `scriptedRoleDetector`, the D10 call-count fixture for the drain retry path
* [`internal/builder/init_script_exec_test.go`](../../internal/builder/init_script_exec_test.go) — the executing init-script harness
* [`test/integration/`](../../test/integration/) — envtest suites, including the UID delete-precondition test
* [`test/e2e/`](../../test/e2e/) — `blockResourceOperations`, `assertSecondEvictionRefused`, `schedulableNodeCount`, `requireThreeSchedulableNodes`
* `.github/workflows/release.yml` — the two-leg E2E matrix, `e2e-gate`, `generated-manifests`, `release-tooling`
* [`test/e2e/topology_abandon_test.go`](../../test/e2e/topology_abandon_test.go) — `jamPod0Replication` and the D50 identity gate
* [`test/e2e/sidecar_test.go`](../../test/e2e/sidecar_test.go) — `TestE2E_SidecarFailoverDrainMaster`, the second D50 instance (2026-09-26); `waitForPodRecreated` lives in [`e2e_test.go`](../../test/e2e/e2e_test.go)
* [`test/e2e/e2e_test.go`](../../test/e2e/e2e_test.go) — `readyEndpointPodNames`, the single D51 endpoint reader
* [`renovate.json`](../../renovate.json) — the D48 rule, and the automerge rules that carried the D47 break onto `main`
* [`hack/verify-release-tooling.mjs`](../../hack/verify-release-tooling.mjs) — the D46 render check; [`package.json`](../../package.json) and `package-lock.json` carry the pins it tests
* [ADR 0003](0003-nudge-a-short-of-pods-statefulset.md) — the feature that shipped inert
* [ADR 0011](0011-evidence-based-steady-state-split-brain-resolution.md) — the decision table these tests are written against
* [ADR 0014](0014-rbac-lives-in-three-places.md) — the CI job that proves the generated manifests are current
* [`internal/builder/pod_security_test.go`](../../internal/builder/pod_security_test.go) — the D52 evaluator matrix, both sides and the legacy-shape control; `TestDataWritableCheck_Executes`, the D6 root skip
* [`internal/builder/image_requirements_test.go`](../../internal/builder/image_requirements_test.go) — `commandsUsedBy` and `valkeyImageScripts`, the D42 walker and its 2026-09-26 command positions
* [`test/imagetools/restricted_runtime_test.go`](../../test/imagetools/restricted_runtime_test.go) — the D53 restricted runtime and the pre-flight → repair → pass sequence
* [`test/e2e/pod_security_test.go`](../../test/e2e/pod_security_test.go) — `TestE2E_PodSecurity_RestrictedNamespace` (D54)
* [`test/e2e/fleet_upgrade_test.go`](../../test/e2e/fleet_upgrade_test.go) — `TestE2E_FleetUpgrade`, `requireHostPathVolume`, `shapeLegacyVolumes`, `countValkeyEventsSince` (D55; `requireRepairRan` was removed on 2026-09-26)
* [`test/e2e/pod_availability_test.go`](../../test/e2e/pod_availability_test.go) — `pollUntil` (D25) and `TestE2E_RollingUpdate_TwoSentinelsRollSerially` (~~NOT EXECUTED, D30~~ green 2026-09-26 on both Valkey lines, locally)
* [`internal/controller/pod_security_migration_test.go`](../../internal/controller/pod_security_migration_test.go) — the unit guards of the second roll (D55)
* [`go.mod`](../../go.mod) — the test-only `k8s.io/pod-security-admission` pin (D52)
* [ADR 0032](0032-generated-pods-run-rootless.md) — the posture and the migration these guards test, D2 the second roll
* [ADR 0024](0024-the-sentinel-tier-reports-its-own-completion.md) — D10, the serial roll of a Sentinel tier of one or two
