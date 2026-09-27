---
id: T45
title: Renovate coverage gaps - the CI Kubernetes and cert-manager pins, ENVTEST_VERSION, the dead release-template.hbs, the Valkey test-image manager that never runs, and releases cut by CI-only bumps
state: analysed       # every Not-verified item settled or narrowed on 2026-09-27 at 84a39c2; decisions 1, 2 and 4 costed with one option marked (was filed)
severity: low         # the E2E environment ages; nothing shipped depends on it. Would be medium if the production fleet runs a Kubernetes minor newer than 1.33 (then the release gate never tests the fleet's version); the fleet version was not checked
security: none
urgency: now          # rule 1, re-derived 2026-09-27 at 84a39c2: measured-false statements in tracked files - the Makefile:49 directive, renovate.json:29, DEVELOPER.md:268 (kubectl), and the Valkey test-image statements (ADR 0017 :31 and D43 :729, DEVELOPER.md:270, testing.md:116-117, images.go:31, CLAUDE.md) (was later, rule 4, before the first appendix)
effort: S             # unchanged 2026-09-27: the additions (kubectl alignment, ignorePaths, the chore rule of decision 4, the double load) are each XS (XS until the first 2026-09-27 enrichment)
blocked-by: decision  # decisions 1, 2 and 4; decision 1 together with T54's decision 1 (shared override rule)
filed-from: the documentation restructure of 2026-09-27 (DEVELOPER.md toolchain table)
opened: 2026-09-27
decided:              # not recorded - no decision yet
done:                 # not recorded - not done
---

Filed on 2026-09-27 from a finding of the documentation restructure, whose toolchain table asked
whether any Renovate manager moves the Kind and Helm version inputs of the workflows. Verification
answered that question with **yes** and found two other pins that nothing moves. Everything below
was read on 2026-09-27; ~~no Renovate run was observed or started.~~ *(corrected 2026-09-27 at
`84a39c2`: no Renovate run was started, but the self-hosted Renovate's own output was read - the
Dependency Dashboard, issue #229, updated 2026-09-27T02:41:30Z by Renovate 44.115.10, and the log
of its scheduled run 36289150623 - and so was the E2E log of CI run 36331871661 on `84a39c2`.)*
*(Re-read 2026-09-27 at `4a7543e` on `chore/maintenance-2026-09-27`: every line cited below still
holds, except where a correction says otherwise. Re-read again at `84a39c2` on the same branch.)*

## Fact

**Verified:**

- **The Kind and Helm inputs are maintained — the premise as handed over was wrong.** The
  `with: version:` inputs of `helm/kind-action` ([`release.yml:157`](../../.github/workflows/release.yml))
  and of `azure/setup-helm` ([`release.yml:316`](../../.github/workflows/release.yml),
  [`build.yml:153`](../../.github/workflows/build.yml)) were last changed by the Renovate bot:
  `795d332` (2026-09-16, "update dependency kubernetes-sigs/kind to v0.33.0 (#219)", `v0.30.0` →
  `v0.33.0`) and `d3e731f` (2026-09-10, "update dependency helm to v4.3.0 (#215)", both files,
  `v4.2.4` → `v4.3.0`). No custom manager in [`renovate.json`](../../renovate.json) names them, so
  a built-in manager extracts them; which one is not recorded in the commits. ~~*(2026-09-27: the
  commit type points at one. Both are minor updates committed as `chore(deps)`.
  `renovate.json:274-283` sets `fix` for every minor, patch, digest and pin update, and the only
  later rule that sets `chore` is the github-actions rule at `:284-290`. So the github-actions
  manager extracts them. This is inferred from the rule order and the commit type, not read from
  a Renovate log.)*~~ *(corrected 2026-09-27 at `84a39c2`: now measured, not inferred. Dashboard
  #229 (`gh issue view 229 --repo guided-traffic/valkey-operator`) lists `kubernetes-sigs/kind
  v0.33.0` and `helm v4.3.0` under the github-actions manager for `release.yml`, and `helm v4.3.0`
  for `build.yml`. Upstream, Renovate knows both actions by name: `helm/kind-action` in
  `lib/modules/manager/github-actions/known-actions/multiple.ts:147-152` and `azure/setup-helm` in
  `known-actions/github-releases.ts:69-73`, read at tag 44.115.12.)*
  - *(Added 2026-09-27 at `84a39c2`.)* **For Kind this holds only since 2026-09-16.**
    `git log -S'version: v0.30.0' -- .github/workflows/release.yml` returns `0a90483`
    (2026-02-17, by hand) and `795d332`: the Kind input sat at `v0.30.0` for seven months and
    skipped Kind `v0.31.0` (2025-12-18) and `v0.32.0` (2026-06-02) (`gh api
    repos/kubernetes-sigs/kind/releases`). Renovate gained the kind-action inputs in `76a36a07f4`
    (2026-09-15, "feat(manager/github-actions): support helm/kind-action's version inputs"), one
    day before the first Kind bump. That link is inferred from the dates; which Renovate release
    shipped it was not checked. Helm has been moved by the bot since `44f661d` (2026-06-06).
- ~~**`KUBERNETES_VERSION: '1.33.4'`** ([`release.yml:23`](../../.github/workflows/release.yml))
  selects the Kind node image (`kindest/node:v${{ env.KUBERNETES_VERSION }}`, `:158`) and the
  kubectl version (`:151`) of every E2E leg.~~ *(corrected 2026-09-27 at `84a39c2`:
  **`KUBERNETES_VERSION: '1.33.4'`** ([`release.yml:23`](../../.github/workflows/release.yml))
  selects only the Kind node image (`kindest/node:v${{ env.KUBERNETES_VERSION }}`,
  [`release.yml:158`](../../.github/workflows/release.yml)) of every E2E leg. It also feeds
  `azure/setup-kubectl` at [`release.yml:148-151`](../../.github/workflows/release.yml), but that
  binary is shadowed: `helm/kind-action@v1.15.0` (`:155`) installs its own kubectl, whose
  `kubectl_version` input defaults to `v1.37.0` (kind-action `action.yml:33-36` at tag `v1.15.0`,
  `DEFAULT_KUBECTL_VERSION=v1.37.0` at `kind.sh:23`), and appends its directory to `GITHUB_PATH`
  (`kind.sh:85-91`). The runner puts the entry added last first (actions/runner
  `src/Runner.Worker/FileCommandManager.cs:152-153` and `Handlers/Handler.cs:216`, read on the
  runner's main branch, not at the runner version that ran), so every later step resolves
  `kubectl` to v1.37.0. That includes the e2e Go tests, which shell out to it (e.g.
  [`e2e_test.go:252`](../../test/e2e/e2e_test.go)). A 1.37 client against a 1.33.4 API server is
  four minors apart; the Kubernetes skew policy supports kubectl "within one minor version (older
  or newer) of kube-apiserver" (https://kubernetes.io/releases/version-skew-policy/). The log of
  job 108655236999 (run 36331871661, `84a39c2`) shows `kubectl_version: v1.37.0`, "Installing
  kubectl...", "Adding kubectl directory to PATH..." and `Client Version: v1.37.0`; that last line
  comes from an absolute-path call (`kind.sh:94`), and no later step prints the kubectl version,
  so the shadowing of later steps is derived from the action and runner source, not measured.
  Nothing before the Kind step uses kubectl (first use at `release.yml:167`).)* It has not changed
  since `0a90483` (2026-02-17, by hand). The only workflow custom manager matches `GO_VERSION:`
  (`renovate.json:345-357`); the two consumers read the value through a `${{ env… }}` expression,
  which is not a version literal. *(Added 2026-09-27 at `84a39c2`: the built-in github-actions
  manager of Renovate 44.115.10, the version that runs here, does look at both inputs - kind-action's
  `node_image` becomes a Docker dependency and `kubectl_version` and `azure/setup-kubectl`'s
  `version` become `kubernetes/kubernetes` dependencies (`known-actions/multiple.ts:20-55`,
  `known-actions/github-releases.ts:74-79`) - and skips each value that contains a variable with
  `skipReason: contains-variable` (`lib/modules/manager/dockerfile/extract.ts:127-132`). The
  dashboard filters out every dependency with a skip reason
  (`lib/workers/repository/package-files.ts:52-59`), so its silence cannot tell "not extracted"
  from "extracted and skipped". The skip is read in the source and consistent with the run log -
  github-actions depCount 86 against 67 dependencies shown - but that gap covers every skipped
  dependency and is not attributed per dependency.)*
- **cert-manager `v1.17.2`** is applied from a release-asset URL in two places,
  [`Makefile:200`](../../Makefile) (`cert-manager-install`, a prerequisite of `e2e-local` and `e2e-fleet-upgrade-local`) and
  [`release.yml:331`](../../.github/workflows/release.yml) (every E2E leg). Neither line carries a
  `# renovate:` comment, and no custom manager matches a GitHub release URL. The version has not
  changed since `88b721b` and `5eacaba` (2026-02-17 and 2026-02-18, by hand). Two copies of one
  pin can also drift apart; today they agree. *(Added 2026-09-27 at `84a39c2`: dashboard #229 lists
  no cert-manager dependency, and the log of job 108655236999 shows the `v1.17.2` URL applied.)*
- *(Added 2026-09-27, upstream release lists read on the GitHub API and Docker Hub.)*
  - The Kind `v0.33.0` release notes (published 2026-08-26) list the node images built for it:
    `v1.37.0` (the default), `v1.36.4`, `v1.35.8` and `v1.34.11`. `v1.33.4`, the image every CI
    leg runs, is not in that list. The newest `kindest/node` tag on the 1.33 line is `v1.33.12`
    (2026-06-02). *(Added 2026-09-27 at `84a39c2`: `v1.33.4` is Kind `v0.30.0`'s build - its
    Docker Hub digest `sha256:25a6018e48dfcaee478f4a59af81157a437f15e6e140bf103f85a2e7cd0cbbf2`
    (last updated 2025-08-27) equals the one in the Kind `v0.30.0` release notes, so it was never
    re-pushed. On the 1.33 line Kind published a new patch tag per release rather than re-pushing
    one (`v0.30.0`: 1.33.4, `v0.31.0`: 1.33.7, `v0.32.0`: 1.33.12), and `v0.33.0` builds no 1.33
    image at all. The `v0.33.0` notes say "You *must* use the @sha256 digest to guarantee an image
    built for this release".)*
  - cert-manager's newest releases are `v1.21.2` (2026-09-11) and `v1.20.4` (2026-09-16), so the
    pinned `v1.17.2` is four minors behind.
  - *(Added 2026-09-27 at `84a39c2`, support windows re-read at the source.)* Kubernetes maintains
    1.37, 1.36 and 1.35; 1.33 reached end of life on 2026-06-28, final patch 1.33.13
    (https://kubernetes.io/releases/). cert-manager supports 1.21 (declares Kubernetes 1.33-1.36)
    and 1.20 (1.32-1.35); 1.17 reached end of life on 2025-10-07 and declared 1.29-1.33
    (https://cert-manager.io/docs/releases/). **No released cert-manager declares Kubernetes
    1.37**, the `kindest/node` tag a manager would propose first.
- *(Added 2026-09-27 at `84a39c2`.)* **CI is green on the aged pins.** Run 36331871661 (head
  `84a39c2d9d52`, conclusion success, all E2E legs green); job 108655236999 logs
  `kind v0.33.0 go1.26.7`, `Ensuring node image (kindest/node:v1.33.4)`, the node as
  `v1.33.4 ... containerd://2.1.3`, and `ok github.com/guided-traffic/valkey-operator/test/e2e
  708.164s`.
- *(Added 2026-09-27.)* **A new custom manager automerges unless a later rule says otherwise.**
  [`renovate.json:225-236`](../../renovate.json) is described as "Makefile-pinned Go tools", but
  it matches `matchManagers: ["custom.regex"]` with no file or dependency filter. It sets
  `automerge: true` for minor, patch and digest updates of every custom regex manager: ~~today the
  Go-version managers (`:304-357`) and the Valkey test images (`:358-368`) as well as the
  Makefile.~~ *(corrected 2026-09-27 at `84a39c2`: today it acts on the Go-version managers
  (`:304-357`) and the Makefile manager. The Valkey test-image manager (`:358-368`) extracts
  nothing, so neither this rule nor the capping rules at `:203-224` has ever acted on it - see the
  appendix "The Valkey test-image manager never extracts".)* Majors are already manual
  (`:237-250`).
- *(Added 2026-09-27.)* **A new custom manager also inherits the `fix` commit type, and on this
  repository `fix` is a release.** `:274-283` sets `semanticCommitType: "fix"` for minor, patch,
  digest and pin updates of every manager, and only github-actions is switched back to `chore`
  (`:284-290`). [`.releaserc.json`](../../.releaserc.json) runs the conventionalcommits
  commit-analyzer. Measured: `21c0b85`, a Makefile-tool bump
  (`fix(deps): update module …golangci-lint… to v2.13.2`), is tagged `v1.12.1`, so a bump of a
  CI-only tool cut an operator release with nothing shipped changed. ~~Not every such commit is
  tagged (`235fb45`, the next golangci-lint bump, is not), and why was not checked.~~
  ~~*(corrected 2026-09-27, review: checked. `git log v1.12.6..v1.12.7` holds `235fb45` and
  `9925539`, another `fix(deps)` bump; the `v1.12.7` tag sits on `9925539`. So `235fb45` was
  released too, together with the next commit, and `v1.12.0..v1.12.1` holds `21c0b85` alone.
  Every such bump lands in a release; some share one.)*~~ *(corrected 2026-09-27 at `84a39c2`:
  `git log v1.12.6..v1.12.7` holds four commits - `9925539` (another `fix(deps)` bump), `235fb45`,
  `88b9171` (ci) and `967eaab` (chore) - and the `v1.12.7` tag sits on `9925539`. So `235fb45` was
  released too, together with the next `fix`, and `v1.12.0..v1.12.1` holds `21c0b85` alone. Every
  such bump lands in a release; some share one.)*
  - *(Added 2026-09-27 at `84a39c2`.)* **A release costs a fleet-wide roll.**
    [ADR 0005](../adr/0005-upgrade-neutral-defaults-and-anti-affinity.md) D11 (`:297-300`): every
    data pod carries the operator image as its sidecar ([`cmd/main.go:74-75`](../../cmd/main.go),
    `--operator-image`) and `ComputePodSpecHash` covers it, so every operator release rolls every
    multi-replica data StatefulSet once it is deployed. D11 records its own boundary: on the
    kustomize path and on a Helm install with a floating `image.tag` the sidecar image string does
    not change, so there a release by itself rolls nothing (`0005:310-313`). A `fix(deps)` release
    caused only by a
    CI-only bump therefore ships nothing and costs a roll wherever it is adopted. The same holds
    beyond the Makefile tools: `24c723d` (semantic-release 25.0.9, npm release tooling) is
    `v1.10.44`, `3a8b660` (testify v1.12.1, a test-only Go module) is `v1.10.48`, and `1cb0eb0`
    and `95f67ae` (the conventionalcommits preset) landed inside `v1.10.41` and `v1.11.0`
    (`git describe --contains`). [`package.json`](../../package.json) is `private` and holds only
    release tooling. Whether the production fleet adopts every release automatically was not
    verified. This is decision 4.

**Not verified:**

- ~~That no built-in Renovate manager extracts `KUBERNETES_VERSION` or the cert-manager URL. The
  seven months without a bot commit suggest it, and the literals' shapes do not match any
  custom manager; only a Renovate run with debug logging, or its dependency dashboard, would
  settle it.~~ *(corrected 2026-09-27 at `84a39c2`, settled in a narrower form: that nothing moves
  either pin is verified - no dashboard entry, no bot commit. That no built-in manager extracts
  `KUBERNETES_VERSION` is false as worded: the github-actions manager extracts the two inputs that
  read it and skips them as `contains-variable` (Verified). Nothing matches the cert-manager URL
  inside a `run:` script. No debug log was read.)*
- ~~Whether Kubernetes 1.33 and cert-manager 1.17 are still inside their upstream support windows
  on 2026-09-27. Not checked against either project.~~ ~~*(corrected 2026-09-27: the release lists
  were read, see Verified. Kubernetes 1.37 and cert-manager 1.21 are out, and 1.33 and 1.17 are
  four minors behind each. That both lie outside the support windows (Kubernetes maintains the
  three newest minors; cert-manager supports the newest two) is the upstream policy as
  remembered, not re-read today.)*~~ *(corrected 2026-09-27 at `84a39c2`: re-read at the source,
  see Verified. Both pins are end of life: Kubernetes 1.33 since 2026-06-28, cert-manager 1.17
  since 2025-10-07.)*
- ~~Whether a newer `kindest/node` tag works with Kind `v0.33.0`. Kind publishes the node images it
  supports per release; not read.~~ ~~*(corrected 2026-09-27: read. Kind `v0.33.0` lists
  `v1.34.11` to `v1.37.0`, see Verified. Whether CI has run green on the pair Kind `v0.33.0` and
  `v1.33.4` since `795d332` was not checked against a run log.)*~~ *(corrected 2026-09-27 at
  `84a39c2`: checked. Run 36331871661 is green on Kind `v0.33.0` with `v1.33.4`, see Verified.)*
- ~~*(Added 2026-09-27.)* That a packageRule placed after `:225-236` overrides its `automerge`.
  Later rules override earlier ones per Renovate's documented merge order, and the existing
  major rule at `:237-250` relies on that. No Renovate run was made.~~ *(corrected 2026-09-27 at
  `84a39c2`: read at the source, still not measured by a run. Renovate's
  `docs/usage/configuration-options.md:3222-3223` at 44.115.12 says to order packageRules so that
  "important rules override settings from earlier rules", and
  `lib/util/package-rules/index.ts:57-125` applies every matching rule in order through
  `mergeChildConfig`. `renovate.json` is loaded twice (appendix "Renovate loads renovate.json
  twice"), both times in the same order, so the last rule of the file is still the last applied.)*
- *(Added 2026-09-27 at `84a39c2`.)* The Kubernetes minor the production fleet runs (no cluster
  access in this verification). It decides whether severity rises to medium (frontmatter).

**Deliberately not included:** `ENVTEST_K8S_VERSION = 1.29.0` ([`Makefile:5`](../../Makefile))
sits on the Kubernetes 1.29 floor the project declares
([ADR 0031](../adr/0031-a-record-the-operator-trusts-lives-in-pod-spec.md) line 195), and several
ADRs record measurements "on envtest 1.29"; moving it automatically would be wrong. Whether it is
meant to track the floor is not written down anywhere found. *(Added 2026-09-27 at `84a39c2`: the
floor is also declared in [`README.md:198`](../../README.md) ("Kubernetes cluster (v1.29+)"), and
[ADR 0023](../adr/0023-volume-claim-templates-are-immutable.md) `:375` calls kube-apiserver 1.29.0
"the repo's declared floor", so the pairing is recorded, though not stated as a rule. The Helm chart
declares no `kubeVersion`.)* `E2E_UPGRADE_FROM ?= 1.10.48` is hand-pinned on purpose (its comment
at `Makefile:164-166`).

## Impact

Every E2E result — the full suites on both Valkey lines and the multi-node leg — is measured on a
Kubernetes and a cert-manager that age silently. [ADR 0017](../adr/0017-test-and-ci-policy.md)
records its CI runs on Kubernetes 1.33.4 and its user-namespace measurement on `kindest/node`
v1.33.4, so it describes that environment; a Kubernetes behaviour change after 1.33 is
not in CI until someone bumps the literal by hand, while local runs (Kubernetes 1.36.1 per
CLAUDE.md) already differ. *(Added 2026-09-27 at `84a39c2`: `make kind-create`
([`Makefile:180-185`](../../Makefile)) passes no node image, so a local run uses whatever the
developer's `kind` binary defaults to; 1.36.1 is Kind `v0.32.0`'s default.)* *(Added 2026-09-27.)*
CI already runs a node image that is not among the ones its own Kind release was built for
(Fact). ~~With either manager option, the first PR is a jump rather than a step: Renovate proposes
the newest tag, `v1.37.0`, which changes every E2E leg at once, including what ADR 0017's
user-namespace probe finds. Expect manual work on that first PR.~~ *(corrected 2026-09-27 at
`84a39c2`: by default Renovate proposes only the newest minor, `v1.37.0`, and skips the ones in
between (`configuration-options.md:5017-5024` at 44.115.10), and no released cert-manager declares
1.37. A manager without `separateMultipleMinor` therefore offers only a version outside every
cert-manager's declared range; the catch-up order is cert-manager first (1.21 declares 1.33-1.36,
so it runs on today's 1.33.4), then Kubernetes inside that range. Each node image also brings its
own containerd, which ADR 0017 `:270` names in its user-namespace measurement (2.1.3 on v1.33.4).
Options, decision 1.)*

*(Added 2026-09-27 at `84a39c2`.)* Two further effects:

- **The kubectl the E2E suite drives is outside the skew policy today** - client 1.37, server
  1.33.4 (Fact). CI is green on it; what a four-minor skew could break was not analysed. The
  alignment is decision-free work (Work list).
- **The Valkey test images age as well.** `valkey/valkey` 9.1.2 and 8.1.10 are on Docker Hub
  (pushed 2026-09-21) while the suites run 9.1.1 and 8.1.9, although six statements in five
  tracked files say Renovate keeps them current (appendix "The Valkey test-image manager never extracts").

## Options

Four decisions were numbered for this ticket; the numbers name them, not the order they are
taken in (Work list). Decision 3 (F4) has only one sensible option left and is now decision-free
work. Three facts shape every option: a new custom manager inherits `automerge: true` from
`renovate.json:225-236` and the `fix` type from `:274-283`, and a `fix` release rolls every
multi-replica data tier once deployed (ADR 0005 D11).

### Decision 1 — how the CI Kubernetes version and the cert-manager pin are kept current

**Mechanism today.** [`release.yml:23`](../../.github/workflows/release.yml) holds
`KUBERNETES_VERSION: '1.33.4'`, and `:158` builds `node_image` from it for every E2E leg. The
`azure/setup-kubectl` step at `:148-151` reads it too, but its binary is shadowed by
kind-action's default kubectl v1.37.0 (Fact). Renovate runs self-hosted
([`renovate.yml:42-51`](../../.github/workflows/renovate.yml), Renovate 44.115.10). Its
github-actions manager moves Kind's and Helm's `version` inputs, and skips `node_image` as
`contains-variable`. cert-manager `v1.17.2` is a literal inside two URLs
([`Makefile:200`](../../Makefile), [`release.yml:331`](../../.github/workflows/release.yml)) that no
manager matches. **What the choice changes:** whether these two pins move by pull request, and
whether a human merges each one. **What it does not change:** the Kind and Helm pins (already moved
and automerged), `ENVTEST_K8S_VERSION` (Deliberately not included), the local `make kind-create`,
and anything shipped. It is taken together with T54's decision 1, because both need an
`automerge: false` rule for their dependency names; T54's exporter image ships and keeps `fix`.

- **A — two custom regex managers with manual review (recommended).**
  - A manager on `KUBERNETES_VERSION` in the workflows: `datasource: docker`, `depName:
    kindest/node`, `versioning: docker`, and `extractVersionTemplate: "^v(?<version>.+)$"`.
    Without the template the value written back would be the raw tag and produce
    `kindest/node:vv1.37.0` at `:158`: Docker versioning strips a leading `v` only for parsing
    (`lib/modules/versioning/docker/index.ts:25` at 44.115.10).
  - A manager on the cert-manager URL in both `Makefile` and the workflows (`datasource:
    github-releases`, `depName: cert-manager/cert-manager`), so one PR moves both copies.
  - One packageRule as the **last** entry of `packageRules` (after `:284-290`, or the `fix` rule
    at `:274-283` overwrites the type): `automerge: false` and `semanticCommitType: chore` for both
    dependency names, and `separateMultipleMinor: true` for `kindest/node`. Without the latter,
    Renovate offers only `v1.37.0` (Impact), and the reviewer can merge a version no released
    cert-manager declares or decline it, and nothing then proposes 1.36.x. With it, one PR per
    minor stream opens (`configuration-options.md:5017-5024`, read, not run); the reviewer merges
    the one inside the range, and the higher ones stay open as prompts. An `allowedVersions` cap,
    the pattern of `:203-224`, would silence every PR above it and bring back the silent aging this
    ticket is about, so it is not part of A.
  - The review criterion, written into the ADR 0017 decision at close: **CI tests the newest
    Kubernetes minor inside the declared range of the newest supported cert-manager release, and
    cert-manager moves first.** It is checkable against https://cert-manager.io/docs/releases/.
    "The fleet's minor" is the alternative criterion and cannot be executed from this repository,
    because the fleet version is recorded nowhere here.
  - Digest stance, stated rather than implied: the node image stays tag-only. Kind's release notes
    ask for the digest; on the 1.33 line Kind has published a new patch tag per release rather
    than re-pushing one (Fact), which is an observation, not a guarantee. A digest would need a
    second captured value, because `kubectl_version` is derived from the same literal.
  - **Cost S:** two manager blocks (about twelve lines each), one rule, and the docs that name the
    versions in the present tense rewritten to point at the variable: `DEVELOPER.md:143`, `:213`,
    `:268-269`, [`testing.md:164`](../developer/testing.md) and
    [`workload-pod-posture.md:185`](../security/workload-pod-posture.md). The dated measurements in
    ADR 0017 (`:135`, `:270`) and `pod_hardening_test.go:113` stay as history.
  - **Consequences:** one literal feeds the node image, kind-action's `kubectl_version` (Work list)
    and the docs, so one PR moves the whole cluster version. Each PR runs the full E2E matrix; the
    `chore` type cuts no release, so nothing rolls the fleet. A reviewer clicks merge on a few PRs
    a year. The first catch-up is cert-manager `v1.21.x`, then `kindest/node` 1.36.x. Each node
    image swaps the containerd ADR 0017 `:270` names; the reviewer reads that line. **Closing
    condition:** dashboard #229 lists `kindest/node` and `cert-manager/cert-manager` as
    dependencies - not only the file entries, since the dashboard hides skipped dependencies -
    because two of this repository's six custom managers never produced an update (the badge
    manager, whose value is skipped, and the Valkey test-image manager, whose file is ignored).
- **B — the same managers, automerged** like the other minor and patch updates, with only the
  `chore` rule added as the last packageRule (and `separateMultipleMinor`, or automerge takes the
  newest minor at once). **Cost:** S minus one rule line. **Consequences:** a green matrix merges
  a Kubernetes minor chosen only as "newest", `v1.37.0` first, which no released cert-manager
  declares; Kind and cert-manager release notes reach no one, and ADR 0017's dated environment
  text goes stale without anyone deciding it. A cheaper, real alternative, since the E2E matrix is
  a strong gate.
- **E — literals in the kind-action inputs, read by Renovate's built-in extractor.** Write
  `node_image: kindest/node:v1.36.4@sha256:…` and `kubectl_version: v1.36.x` into the `with:`
  block of `release.yml:155-162` and drop `KUBERNETES_VERSION`. The github-actions manager then
  extracts both (`known-actions/multiple.ts:16-55` at 44.115.10) and commits them as `chore`
  (`:284-290`). It needs an `automerge: false` rule for `kindest/node` and `kubernetes/kubernetes`
  placed after `:154-175` (github-actions automerges even majors there), a `groupName` so both
  move in one PR, and `separateMultipleMinor` as in A. The cert-manager custom manager and its
  own `automerge: false`/`chore` rule are needed anyway. **Cost:** S, one custom manager fewer than
  A. **Consequences:** the node image is pinned by digest natively, as Kind asks, and the extractor
  is maintained upstream rather than as one more custom regex. The Kubernetes version lives in two
  literals on two datasources: kubectl patches publish on `kubernetes/kubernetes` before Kind
  builds an image, so the two drift by patch, which is inside the skew policy (it is stated per
  minor). The kind-action extractor has existed upstream since 2026-09-15; this repository's Kind
  input has moved by it once.

**Recommended: A.** One literal decides node image, client and the docs, so a Kubernetes move is
one PR and the version cannot be read in two places. Every change reaches a reviewer, and the
first ones need a reviewer: the newest tag is outside every released cert-manager's declared range,
and each node image swaps the containerd ADR 0017 measures against. The `chore` type means no bump
cuts a release, so none rolls the fleet (ADR 0005 D11). **Why A beats E, the runner-up, and only
narrowly:** E's real gains are a native digest pin and no custom regex, and this repository's
record with custom managers (two of six silently produced nothing) argues for E. A answers that
record with its closing condition (the dependency must appear on the dashboard), and E saves only
one of A's three blocks - the cert-manager manager and a review rule are needed either way - while
splitting the Kubernetes version into two literals. If the owner weighs the digest pin higher than
the single literal, E is the choice. **A over B:** the review costs a merge click; B lets "newest"
decide an environment the release gate is measured on.

### Decision 2 — how `ENVTEST_VERSION` is kept current (appendix D4)

**Mechanism today.** [`Makefile:50`](../../Makefile) pins setup-envtest to the controller-runtime
branch `release-0.19`, which the Go proxy resolves to `v0.0.0-20250308055145-5fe7bb3edc86`
(2025-03-08). The Makefile manager's regex (`renovate.json:300`) captures only `v[\d.]+`, so the
`# renovate:` comment at `Makefile:49` has never matched since it was added in `c6f97e2`
(2026-03-20); dashboard #229 lists six Makefile tools and no setup-envtest. The tool is installed
at `Makefile:410` into a versioned path (`Makefile:38`, ADR 0017 D49) and used by every
envtest-backed target (`Makefile:103`, `:112`, `:118`, `:123`, `:129`, `:250`, `:261`); in CI by
`Unit Tests` ([`release.yml:609`](../../.github/workflows/release.yml)) and `Integration Tests
(envtest)` (`release.yml:777`), both required by the repository ruleset. [`go.mod:16`](../../go.mod)
requires controller-runtime `v0.25.1`, and since `v0.24.0` setup-envtest is tagged at every
controller-runtime release (appendix D4). **What the choice changes:** which setup-envtest binary
downloads the envtest assets, and whether its bumps are separate PRs and releases. **What it does
not change:** `ENVTEST_K8S_VERSION = 1.29.0`, the declared floor, which stays hand-pinned.

- **B — pin a tagged version, `ENVTEST_VERSION ?= v0.25.1`.** The existing Makefile manager then
  matches, and the tool joins the six others under `:225-236`. **Cost:** XS, plus one
  `make test-unit` and one `make test-integration` run (not run in this verification).
  **Consequences:** the comment becomes true with one token. Each setup-envtest bump is its own
  automerged `fix(deps)` PR and so an operator release that rolls every multi-replica data tier
  once deployed, unless decision 4 adds the tool to the `chore` list. The tool and
  controller-runtime can drift a minor apart, because the `k8s-go-modules` group (`:251-262`)
  covers only `gomod`. Whether Renovate's go datasource lists this submodule's tags was not
  verified by a run.
- **C — derive `ENVTEST_VERSION` from `go.mod`, and delete the `# renovate:` comment
  (recommended).** For example
  `ENVTEST_VERSION ?= $(shell awk '$$1=="sigs.k8s.io/controller-runtime"{print $$2}' go.mod)`,
  with the empty-result guard of Kubebuilder's current project scaffold, which derives the same
  variable the same way
  (https://raw.githubusercontent.com/kubernetes-sigs/kubebuilder/master/testdata/project-v4/Makefile
  lines 199-202, read 2026-09-27: "Set ENVTEST_VERSION manually (controller-runtime replace has no
  tag)"), so a missing requirement fails with a named message instead of `go install …@`. Copy
  only that half: the scaffold also derives `ENVTEST_K8S_VERSION` from `k8s.io/api` (`:205-206`),
  which this repository deliberately does not do. **Cost:** XS, one Makefile line, plus one
  `make test-unit` and one `make test-integration` run to prove `v0.25.1` installs the 1.29.0
  assets (not run; the controller-tools envtest index lists `v1.29.0`). **Consequences:** the tool
  moves inside the controller-runtime PR of the `k8s-go-modules` group, which already runs both
  jobs that consume it, and which is a `fix` release anyway because controller-runtime ships. No
  separate PR, no separate release, no drift by construction. `go.mod` has one controller-runtime
  line and no `replace`, so the extraction is unambiguous. A future controller-runtime release
  without a matching setup-envtest tag makes `make envtest` fail on that Renovate PR - red, not
  silent. setup-envtest `v0.25.1` declares `go 1.26.0`; the repository builds with 1.27.1.
  `ENVTEST_VERSION` stays above the first target naming it (`Makefile:50` against the `$(ENVTEST)`
  target at `:409`), as ADR 0017 D49 requires.

**Recommended: C.** It makes the tool version a function of `go.mod` and removes both residuals B
states for itself: a separate `fix(deps)` release per tool bump, each one a fleet roll once
deployed (ADR 0005 D11), and a minor of drift between tool and library. The tag sets support it:
four of four controller-runtime releases since `v0.24.0` carry a setup-envtest tag at the same
commit, and the one failure mode turns a PR red instead of aging silently. **B, the runner-up,** is
as cheap and makes the comment true, but keeps a separate PR stream whose every merge is a release
unless decision 4 intervenes.

### Decision 4 — the commit type of bumps that change nothing shipped

**Mechanism today.** `renovate.json:274-283` gives every minor, patch, digest and pin update the
`fix` type; only github-actions is switched to `chore` (`:284-290`). The conventionalcommits
analyzer turns a `fix` into a patch release, and under ADR 0005 D11 every release rolls every
multi-replica data tier once deployed. Of the Makefile manager's six tools, golangci-lint,
gocyclo, gosec and govulncheck run only in CI and locally, kustomize only in the local
install/deploy targets (`Makefile:356-369`; no workflow calls it), and controller-gen stamps its
version into the shipped chart CRD
([`crd.yaml:8`](../../deploy/helm/valkey-operator/templates/crd.yaml),
`controller-gen.kubebuilder.io/version: v0.22.0`). The npm manager covers only release tooling
(`package.json`, `private`, devDependencies only; open PR #224 is a `fix(deps)` bump of
`@semantic-release/github`). The Valkey test pins will produce bumps once their manager runs
(appendix). **What the choice changes:** the commit type, and so whether a bump is a release.
`chore` defers a change to the next `fix` or `feat` release; it does not suppress it. **What it
does not change:** automerge, shipped Go modules, the Go toolchain and T54's exporter image, for
which `fix` stays right. No ADR sets a commit-type rule today (`grep` over `docs/adr/`).

- **b — `chore` for an explicit list (recommended).** One packageRule placed last: `chore` for
  `matchDepNames` golangci-lint, gocyclo, gosec, govulncheck, kustomize (each by the full `depName`
  of its Makefile comment, e.g. `github.com/golangci/golangci-lint/v2/cmd/golangci-lint`, or the
  rule matches nothing), `kindest/node` and
  `cert-manager/cert-manager` (with decision 1), setup-envtest if decision 2 takes B, and
  `valkey/valkey` scoped with `matchFileNames: ["test/testimages/images.go"]` (the name also
  appears in test fixtures only, but the scope keeps a future shipped default out); a second rule
  gives `matchManagers: ["npm"]` the same type, because packageRule matchers combine with AND.
  controller-gen stays `fix`. **Cost:** XS - the rules sit next to decision 1's, in the same
  `renovate.json` edit - plus one line in the ADR 0017 decision at close: a new CI-only tool joins
  the list. **Consequences:** a bump whose output ships stays a release. A new CI-only tool not
  added to the list cuts releases again, failing toward an extra release. Test-only Go modules
  such as testify (`3a8b660`, `v1.10.48`) share the gomod manager with shipped modules and are
  knowingly left at `fix`; the ADR line names them.
- **c — `chore` for everything the Makefile manager, the npm manager and the test-image file
  produce**, via `matchFileNames: ["Makefile", "test/testimages/images.go"]` and the npm rule,
  controller-gen included. **Cost:** XS. **Consequences:** needs no list and extends itself to a
  new tool. A controller-gen bump then changes the shipped CRD annotation (and possibly schema)
  without a release of its own; it reaches users with the next release. Such a PR carries the
  regenerated CRD anyway, because `Generated Manifests Up To Date` is required (`890be1e`, the
  controller-gen bump committed with only the Makefile, predates that gate).

**Recommended: b.** The rule "a bump is a release when it changes what ships" stays true for the
one Makefile tool whose output ships, and the list is written in the same edit that adds a tool.
**c, the runner-up,** needs no list, but fails toward a silent non-release for a shipped CRD
change; b fails toward an extra release. That is the honest margin, not a guarantee by
construction: test-only Go modules stay `fix` under both.

## Work list (2026-09-27)

~~**XS items that need no decision: none.** Every item waits on one of three decisions. Each
false statement that makes this ticket `now` (the D4 comment, the `renovate.json:29`
description) is removed differently depending on the option taken.~~

~~**Decision order.** F4 and D4 first, in either order. Both are independent of the managers and
of every other ticket, each is a one-liner, and each removes a rule-1 statement, so the ticket
stops being `now` once both land. The managers (decision 1) come next, taken together with T54's
option, because the two tickets share the override rule. Recommended: decision 1 **A**,
D4 **B**, F4 **A**.~~

~~**Once decided:**~~

~~1. The two managers and the override rule (decision 1), then the docs listed under option A.~~
~~2. D4: the one-line change of the chosen option, then `make test-integration` (option B)
   *(and `make test-unit`, review 2026-09-27)*.~~
~~3. F4: delete the file and its manager, and fix `renovate.json:29`, `DEVELOPER.md:90` and `:265`.~~

*(corrected 2026-09-27 at `84a39c2`: five items need no decision, F4 among them, and decision 4
is new. The list below replaces the struck one.)*

**XS items that need no decision:**

1. **Align kubectl with the cluster.** Add `kubectl_version: v${{ env.KUBERNETES_VERSION }}` to the
   `helm/kind-action` step ([`release.yml:155-162`](../../.github/workflows/release.yml)) and delete
   the `azure/setup-kubectl` step (`:148-151`), whose binary is shadowed; nothing before the Kind
   step uses kubectl. Under option E, feed it from E's literal instead. Caveat: kind-action caches
   kubectl under a path keyed on the Kind version only (`kind.sh:75`) and installs it only when no
   executable is there (`:85-88`), so on a runner whose tool cache persists a changed
   `kubectl_version` is silently ignored until the Kind version changes. The runner of job
   108655236999 (`guided-traffic-runner-dkpps-lf22j`) logged "Installing kubectl...", so its cache
   was empty; whether every self-hosted runner is ephemeral was not verified. Verify by the job
   log: "Installing kubectl..." followed by a `Client Version` equal to the node version. Then
   correct [`DEVELOPER.md:268`](../../DEVELOPER.md): drop "(Kind node image and kubectl)", and
   replace "which one the commits do not record" with the github-actions manager (dashboard #229).
2. **Let Renovate read `test/testimages/images.go`.** Set a top-level `ignorePaths` in
   `renovate.json` to the `:ignoreModulesAndTests` list without `**/test/**` (appendix). This makes
   ADR 0017 `:31` and D43, `DEVELOPER.md:270`, `testing.md:116-117`, `images.go:31` and CLAUDE.md
   true without editing them. Take it with or after decision 4, or the first two bumps (9.1.2,
   8.1.10) are `fix(deps)` releases.
3. **Delete `.github/release-template.hbs` and its manager** (the former decision 3, appendix F4):
   `renovate.json:331-344`, fix the `:29` description, `DEVELOPER.md:90` and `:265`. It changes no
   release note.
4. **Stop Renovate loading `renovate.json` twice.** Confirm the cause with one debug run
   (`workflow_dispatch`, `logLevel: debug`, [`renovate.yml:8-19`](../../.github/workflows/renovate.yml)),
   then remove `configurationFile: renovate.json` (`renovate.yml:46`). Verify that dashboard #229
   lists each regex file once.
5. **Correct [`DEVELOPER.md:266`](../../DEVELOPER.md)**: "(read from the regex, not observed)" is
   now observed on the dashboard (appendix D4). If item 3 has not landed, correct `:265` too: the
   badge is extracted and skipped because semver rejects `1.26`. `:273` says the npm manager is
   "not verified in a run"; it is now observed - dashboard #229 lists `package.json (6)` under npm,
   and run 36289150623 reports npm depCount 6.

**Decision order.** Items 1, 3 and 5 at any time. Decision 4 before or with item 2. Decision 2 on
its own. Decisions 1 and 4 in one `renovate.json` edit, together with T54's decision 1. Rule-1
statements fall away with items 1 (DEVELOPER.md:268), 2 (the Valkey image statements), 3
(`renovate.json:29`) and decision 2 (`Makefile:49`). Recommended: decision 1 **A**, decision 2
**C**, decision 4 **b**.

**Once decided:**

1. Decision 1: the two managers and the last packageRule, then the docs listed under option A; the
   closing condition is the dashboard entry.
2. Decision 2: the one-line change, then `make test-unit` and `make test-integration`.
3. Decision 4: the `chore` rules next to decision 1's.

**Close (ADR 0034):** "CI-environment pins are Renovate-managed with manual review and a
`chore` commit type" is a durable rule. ~~It becomes a new decision in
[ADR 0017](../adr/0017-test-and-ci-policy.md) (D56; D55 is the last today), with a Status line
and its row in `docs/adr/README.md`.~~ *(corrected 2026-09-27 at `84a39c2`: it becomes a new
decision in [ADR 0017](../adr/0017-test-and-ci-policy.md) (D56; D55 is still the last), with a
Status line and its row in `docs/adr/README.md`, and it carries the review criterion of decision 1
and the commit-type rule of decision 4, naming the test-only Go modules knowingly left at `fix`.)*
The toolchain table in `DEVELOPER.md:260-273` is the contributor-facing home. `git grep -nE
'T45\b|045-'` outside `docs/tickets/` is empty (re-checked 2026-09-27 at `84a39c2`).

## Decision

Not decided. Decisions 1, 2 and 4 are open (Options); decision 3 was dissolved into decision-free
work on 2026-09-27.

## Verification

- A Renovate dry run (or the dependency dashboard) lists `kindest/node` and
  `cert-manager/cert-manager` as dependencies of `release.yml`, and the cert-manager one of the
  `Makefile` as well. *(Added 2026-09-27 at `84a39c2`: the dashboard is issue #229, and a debug
  run is `workflow_dispatch` with `logLevel: debug` (`renovate.yml:8-19`). The dashboard hides
  skipped dependencies, so check for the dependency line, not the file entry. Until item 4 lands,
  every regex-managed file appears twice.)*
- Revert check: with the new manager's regex broken in a scratch copy of `renovate.json`, the
  same dry run no longer lists them.
- *(Added 2026-09-27.)* The config validates with `renovate-config-validator`, and the resolved
  config of a dry run shows `automerge: false` and `semanticCommitType: chore` for both new
  dependencies. Without a dry run, the rule order in `renovate.json` is the only evidence, and
  that is a read, not a measurement.
- *(Added 2026-09-27 at `84a39c2`.)* kubectl: the E2E job log shows "Installing kubectl..." and a
  `Client Version` equal to the node version. Valkey images: dashboard #229 lists
  `test/testimages/images.go` with two `valkey/valkey` dependencies, and the first PRs propose
  9.1.2 and 8.1.10. Decision 4: the next CI-only tool bump is titled `chore(deps)` and cuts no
  tag.

## Appendix 2026-09-27: ENVTEST_VERSION and the dead release template

Two members of the same family, moved here on 2026-09-27 from a ticket that had bundled them
with unrelated work (owner decision of that day: non-security items do not stay in an embargoed
file). ~~Re-read against the working tree of `feat/rootless` on 2026-09-27.~~ *(corrected
2026-09-27 at `84a39c2`: re-read at `84a39c2` on `chore/maintenance-2026-09-27`.)*

### D4 — the `# renovate:` comment above `ENVTEST_VERSION` advertises automation that does not run

**Verified:**

- [`Makefile:49-50`](../../Makefile): `# renovate: datasource=go
  depName=sigs.k8s.io/controller-runtime/tools/setup-envtest` above
  `ENVTEST_VERSION ?= release-0.19`, ~~unchanged since `25483b2` (2026-02-17).~~ *(corrected
  2026-09-27 at `84a39c2`: the value dates from `25483b2` (2026-02-17); the comment was added in
  `c6f97e2` (2026-03-20, #39), which added the first six `# renovate:` comments of the Makefile
  (`a36fea8`, 2026-09-18, moved the block and added govulncheck's), and has never matched since -
  `git log -S` on each string, `git show` of both commits.)*
- The Makefile custom manager of [`renovate.json`](../../renovate.json) requires
  `currentValue` to match `v[\d.]+`. Run over the Makefile on 2026-09-27 it matches **6** lines -
  kustomize, controller-gen, golangci-lint, gocyclo, gosec, govulncheck - and not
  `ENVTEST_VERSION`. When the finding was first written the count was 5; govulncheck has been
  pinned since, so **a check that counts 6 would now pass with envtest still unmatched** - the
  verification below names envtest instead of a count. *(Added 2026-09-27 at `84a39c2`: now
  measured by Renovate itself - dashboard #229, "Makefile (6)", lists exactly those six and no
  setup-envtest.)*
- `release-0.19` is a branch of controller-runtime, not a version; [`go.mod:16`](../../go.mod)
  requires `sigs.k8s.io/controller-runtime v0.25.1`.
- *(Added 2026-09-27, read through the Go module proxy with `go list -m`.)* The module
  `sigs.k8s.io/controller-runtime/tools/setup-envtest` publishes **semver tags**: `v0.24.0`
  (2026-04-30), `v0.24.1`, `v0.25.0` and `v0.25.1` (2026-09-13). `release-0.19` resolves to
  `v0.0.0-20250308055145-5fe7bb3edc86`, a commit of 2025-03-08. The existing regex
  (`v[\d.]+`, `renovate.json:300`) would match `v0.25.1` as written. The controller-tools envtest
  index (`envtest-releases.yaml` at HEAD) lists `v1.29.0`, the assets `ENVTEST_K8S_VERSION`
  asks for. *(Added 2026-09-27 at `84a39c2`, `curl` on proxy.golang.org `@v/list` and `.info`:
  controller-runtime's own tags since `v0.24.0` are the same four, `v0.23.x` has no setup-envtest
  tag, `v0.25.1` of both modules is commit `67b72c2517be`, and setup-envtest `v0.25.1`'s `go.mod`
  says `go 1.26.0`. This is what makes decision 2 option C possible.)*
- *(Added 2026-09-27.)* `Integration Tests (envtest)` is among the required contexts that ADR
  0017 D47 enumerates (`:535`), and it is ~~the~~ *(corrected 2026-09-27, review: a)* job that
  runs setup-envtest. *(Review, 2026-09-27: `Unit Tests` runs it too, through
  `make test-unit-coverage` ([`release.yml:609`](../../.github/workflows/release.yml),
  `Makefile:118`); `make test-unit` and `make test` call it as well (`Makefile:112`, `:103`).
  Both jobs are required contexts at ADR 0017 `:535`.)* ~~Whether branch
  protection still carries it was not read, since that is repository state.~~ *(corrected
  2026-09-27 at `84a39c2`: read. Classic branch protection answers 404 "Branch not protected"
  (`gh api repos/guided-traffic/valkey-operator/branches/main/protection`); the required contexts
  come from a repository ruleset (`gh api repos/guided-traffic/valkey-operator/rules/branches/main`),
  which lists twelve, `Unit Tests` and `Integration Tests (envtest)` among them. The integration
  step is `release.yml:777`.)*

**Not verified:** whether a Renovate go datasource could resolve a `release-0.x` branch ref at all
(no Renovate run), and whether `setup-envtest` from `release-0.19` differs from a current one for
the `ENVTEST_K8S_VERSION = 1.29.0` assets this repo downloads - the integration tier runs green
on it (ADR 0017), which says it works, not that it is current. *(2026-09-27: also not verified
is whether `setup-envtest v0.25.1 use 1.29.0` installs the assets and the integration tier stays
green on it. Option B needs exactly that run. Nor was it verified that Renovate's go datasource
lists this submodule's tags. The Go proxy lists them, and the datasource reads the proxy by
default, but no Renovate run confirmed it.)* *(Added 2026-09-27 at `84a39c2`: the proof run is
needed by option C as well; the datasource question matters only to option B.)*

**Options:** decision 2 under [Options](#decision-2--how-envtest_version-is-kept-current-appendix-d4).

**Verification:** either the manager's regex, run over the Makefile, lists `setup-envtest` among
its matches, or no `# renovate:` comment stands above `ENVTEST_VERSION`.

### F4 — `.github/release-template.hbs` is loaded by nothing and kept current by Renovate

**Verified:**

- [`.releaserc.json`](../../.releaserc.json) configures
  `@semantic-release/release-notes-generator` with `preset: conventionalcommits` only - no
  `writerOpts`, no `template` - so no release note has ever been rendered from
  [`.github/release-template.hbs`](../../.github/release-template.hbs). *(Added 2026-09-27 at
  `84a39c2`: [`hack/verify-release-tooling.mjs`](../../hack/verify-release-tooling.mjs) reads
  `.releaserc.json` (`:21`, `:58-68`) and never the template.)*
- The only references to the file are in `renovate.json`: the Go group description (line 29)
  and a custom manager for its badge (lines 332-335). *(corrected 2026-09-27, at `4a7543e`: the
  restructure added two more, [`DEVELOPER.md:90`](../../DEVELOPER.md) (layout tree) and `:265`
  (toolchain table). Both describe the file truthfully as unreferenced and lagging. Ticket 054
  names it too, in its Fact bullet "The six custom managers" (`:31-32` in its `84a39c2`
  version). The manager spans `renovate.json:331-344`.)* The badge still reads `go-1.26`
  ([`release-template.hbs:8`](../../.github/release-template.hbs)) while `go.mod` declares
  `go 1.27.1` - the automation that keeps a dead file current does not even do that.
- *(Added 2026-09-27.)* ~~`git log -S'go-1.26-blue'` finds only `0a90483` (2026-02-17).~~
  *(corrected 2026-09-27 at `84a39c2`: `git log -S'go-1.26-blue' -- .github/release-template.hbs`
  finds only `0a90483` (2026-02-17); without the path it also finds `84a39c2`, which wrote the
  string into this ticket.)* Renovate has never moved the badge, although the Go version it names
  has moved (`edcbf39`, golang v1.27.1).
- *(Added 2026-09-27 at `84a39c2`.)* **Why the badge never moves.** The manager extracts `1.26`
  (the run log's regex depCount 22 = 12 Makefile + 2 Containerfile + 2 go.mod + 4 workflows + 2
  release-template, every file twice), and lookup skips it: Renovate's semver versioning accepts
  only `!!semver.valid(input)` (`lib/modules/versioning/semver/index.ts:28-32` at 44.115.10), and
  `node -e 'require("./node_modules/semver").valid("1.26")'` in this repository returns `null`
  while `"1.27.1"` is valid; lookup then sets `invalid-value` (`lib/workers/repository/process/
  lookup/index.ts:541` at 44.115.10). The `extractVersionTemplate` `^(?<version>\d+\.\d+)` at
  `renovate.json:343` makes every candidate two-part as well. The dashboard lists the file with
  no dependency because it hides skipped ones. The exact skip reason was not seen in a log.

**Not verified:** ~~why Renovate never moved the badge (a guess: its versioning rejects the
two-part `1.26`).~~ *(corrected 2026-09-27 at `84a39c2`: settled at the source, see Verified.
Nothing on this item is left unverified.)*

**Options:** none left. Deleting the file and its manager is decision-free work item 3; the
removed alternative (wire it in through `writerOpts`) is recorded in History. Its evidence, kept
here (read, not rendered): the template calls a `gte` helper and reads `process.env.TEST_COVERAGE`
(`release-template.hbs:5-6`, `:12-13`); Handlebars has no built-in `gte`, no step in
`.releaserc.json` or the release job passes a `process` object into the writer context, and
`git grep` finds no `TEST_COVERAGE`, `writerOpts` or `registerHelper` outside the template, so the
template would have to be rewritten before anything could load it, and the D46 release-tooling
check extended to render it.

**Verification:** ~~`git grep -n release-template` is empty~~ *(corrected 2026-09-27:
`git grep -n release-template -- ':!docs/tickets'` is empty, which needs the `DEVELOPER.md:90`
and `:265` edits)*, `renovate.json` names no such file, and `make test-release-tooling` is green.

## Appendix 2026-09-27 (re-verification at 84a39c2): the Valkey test images and the double load

Two more coverage gaps of the same `renovate.json`, found while re-verifying this ticket, fixed
in the same edit, so filed here rather than as tickets of their own.

### The Valkey test-image manager never extracts

**Verified:**

- The custom manager at [`renovate.json:358-368`](../../renovate.json) targets
  [`test/testimages/images.go`](../../test/testimages/images.go), which pins
  `valkey/valkey:9.1.1` (`:40`) and `valkey/valkey:8.1.9` (`:45`), each under a
  `// renovate: datasource=docker depName=valkey/valkey` comment. **It has never produced an
  update.** Dashboard #229's regex section lists release-template, the two workflows,
  Containerfile, `go.mod` and the Makefile (each twice) and no `images.go`, and the run log's
  regex depCount 22 decomposes without an `images.go` term. The file's only commit is `f5f3256`
  (2026-08-22, by hand). Docker Hub has `valkey/valkey` 9.1.2 (pushed 2026-09-21T08:33Z) and
  8.1.10 (2026-09-21T08:18Z), and no Renovate PR proposes them.
- **The regex is not the cause.** Run with Node's `RegExp` over the file (`node -e` with
  `new RegExp(m.matchStrings[0], "g")` from `renovate.json`), it returns both pins:
  `{datasource: docker, depName: valkey/valkey, currentValue: 9.1.1}` and `8.1.9`. Renovate may
  use RE2 rather than Node's engine; the pattern uses no feature where the two differ that was
  noticed.
- **The cause read in the source:** `renovate.json:4` extends `config:recommended`, which extends
  `:ignoreModulesAndTests` (`lib/config/presets/internal/config.preset.ts:32` at 44.115.10), whose
  `ignorePaths` include `**/test/**` (`default.preset.ts:303-315`). `picomatch('**/test/**',
  {dot: true})` from this repository's `node_modules` matches `test/testimages/images.go`
  (`true`); Renovate's own matcher was not run. `ignorePaths` is `mergeable: false`
  (`lib/config/options/index.ts:1343-1351`), so a repository value replaces the preset's.
- **Measured-false statements in tracked files** (urgency rule 1): ADR 0017 `:31` ("pinned in one
  file that Renovate maintains") and D43 at `:729` ("Renovate keeps both current and is capped per
  major"), [`DEVELOPER.md:270`](../../DEVELOPER.md), [`testing.md:116-117`](../developer/testing.md),
  the comment at [`images.go:31`](../../test/testimages/images.go), and CLAUDE.md ("Renovate
  maintains both and is capped per major"). The capping rules at `renovate.json:203-224` have
  never acted on anything.
- The only non-Go tracked files under an ignored `test/` path are `test/e2e/helm-values.yaml` and
  `test/e2e/testdata/cert-manager-issuer.yaml`. Neither matches a built-in manager's file
  pattern that was read (the helm-values pattern `(^|/)values\.ya?ml$` does not match
  `helm-values.yaml`, checked with a Node regex); not run through Renovate.

**Not verified:** that `ignorePaths` is the cause, beyond the source read and the regex
measurement above - a debug run's "ignored" lines would settle it.

**Fix (decision-free work item 2):** a top-level `ignorePaths` of `**/node_modules/**`,
`**/bower_components/**`, `**/vendor/**`, `**/examples/**`, `**/__tests__/**`, `**/tests/**` and
`**/__fixtures__/**` - the preset list without `**/test/**`. Dropping the whole preset through
`ignorePresets: [":ignoreModulesAndTests"]` has the same effect in this repository, but drops the
other entries silently rather than naming the one that is removed; moving `images.go` out of
`test/` would be a code move across every e2e import for a one-line configuration fix. Neither is
a real alternative, so there is no decision. The first bumps are patches, so they automerge under
`:225-236` and run the full E2E matrix; their commit type is decision 4.

### Renovate loads renovate.json twice

**Verified:** [`renovate.yml:46`](../../.github/workflows/renovate.yml) passes
`configurationFile: renovate.json`, which the action hands over as the global config file (the run
log of 36289150623 shows `RENOVATE_CONFIG_FILE=/github-action/renovate.json`), and
`RENOVATE_REQUIRE_CONFIG: 'required'` (`:51`) loads the same file again as repository config.
`customManagers` is a mergeable array (`lib/config/options/index.ts:3221-3228` at 44.115.12), so
each custom manager runs twice: the run reports regex fileCount 12 and depCount 22 for six
distinct files, and the dashboard lists every regex file twice while github-actions files appear
once. None of the top-level keys of `renovate.json` is a global-only option (each looked up in
`lib/config/options/index.ts` at 44.115.10; none carries `globalOnly: true`), so removing the
global load loses nothing. No duplicate PR was observed.

**Not verified:** the cause - it is inferred from the configuration and the counts, not confirmed
with a debug run. The design note: a file loaded as global config may set options that repository
config may not; no escalation path was found (on the schedule trigger the checkout is `main`, and
whoever can change `renovate.json` there can already change `renovate.yml`), so the removal is the
tighter posture at most, and this ticket's `security: none` stands.

**Fix (decision-free work item 4):** one debug run to confirm, then delete `:46`.

## Cross-ticket (2026-09-27 at 84a39c2)

- **T54** shares the override rule: one `automerge: false` rule can list `kindest/node`,
  `cert-manager/cert-manager` and `oliver006/redis_exporter`, but the `chore` rules of decisions 1
  and 4 must exclude the exporter, which ships, and must be the last packageRules. T54's own Fact
  goes stale in two places this ticket found: [054](054-renovate-does-not-track-the-default-exporter-image.md)'s
  Fact bullet "The six custom managers" counts the `images.go` manager among the working ones,
  and its Fact bullet "A new custom manager would automerge unless it is told not to" names it as
  "the precedent for the manager's shape" - a precedent that has never run, so T54's verification must be the
  dashboard entry, not the analogy. T54's own manager on `api/v1/valkey_types.go` is not under an
  ignored path, so its plan is not blocked. The count of custom managers changes: five after work
  item 3, seven with decision 1's two managers, eight with T54's. Those corrections belong in
  T54's file, and T54's own re-verification of 2026-09-27 already carries both (its Fact bullet on
  the six managers and its struck precedent sentence); ~~the line numbers above are those of its
  `84a39c2` version~~ *(corrected 2026-09-27, sweep: cited by section, because 054's lines have
  moved since `84a39c2`)*.
- **T30** (severity high, security boundary, effort M, state ~~filed~~ analysed *(corrected
  2026-09-27, consistency pass)*): embargoed security finding, open - details in its own ticket
  file until it is fixed.
- **T44**: its change, committed in ~~`84a39c2`~~ `bcc63c9` *(corrected 2026-09-27, consistency
  pass: `git log -- .github/workflows/release.yml` and `git show bcc63c9 --stat`)*, deleted one
  line of `release.yml` above the unit
  step, so every later line moved up by one; `release.yml:610` became `:609`. This ticket's other
  `release.yml` citations sit above the deletion and hold. *(Added 2026-09-27, consistency pass:
  044 recommends retiring the kustomize deploy path (its E1 B, which needs the owner's go-ahead)
  and deleting `make install`/`make uninstall` (its F1 C). Together they leave kustomize without a
  consumer, so its pin and `# renovate:` comment at `Makefile:45` go; the Makefile manager's
  count then drops from 6 to 5 - Verification here names envtest, not a count, so it is
  unaffected - and decision 4 b's `chore` list loses kustomize.)*

## History

- 2026-09-27: re-verified at `84a39c2`. Checked every Fact, appendix and Work-list claim against
  the tree, the Renovate Dependency Dashboard (issue #229, Renovate 44.115.10, updated
  2026-09-27T02:41:30Z), the log of Renovate run 36289150623, the E2E log of CI run 36331871661
  (job 108655236999), the Go module proxy, the repository ruleset, Docker Hub, the kind-action
  `v1.15.0` source, the Renovate source at 44.115.10/44.115.12, and the Kubernetes and
  cert-manager support pages. **False or outdated, corrected in place:** `KUBERNETES_VERSION`
  selects only the node image - kind-action's default kubectl v1.37.0 shadows setup-kubectl, a
  four-minor skew against the 1.33.4 API server (derived from action and runner source; the log
  line `Client Version: v1.37.0` comes from an absolute-path call); the Makefile `# renovate:`
  comment dates from `c6f97e2`, not `25483b2`; the unrestricted `git log -S'go-1.26-blue'` now
  also finds `84a39c2`; `v1.12.6..v1.12.7` holds four commits, not two; the automerge rule never
  acts on the Valkey test images; the appendix was re-read at `84a39c2`, not on `feat/rootless`;
  the Impact's "first PR is a jump" needs `separateMultipleMinor` to be manageable. **Settled
  from Not verified:** which manager moves Kind and Helm (github-actions, dashboard), that nothing
  moves the two pins (narrower form: `node_image` is extracted and skipped as `contains-variable`),
  the support windows (Kubernetes 1.33 end of life 2026-06-28, cert-manager 1.17 2025-10-07; no
  released cert-manager declares 1.37), CI green on Kind `v0.33.0` with `v1.33.4`, the packageRule
  order (read in docs and source), branch protection (a ruleset with twelve contexts), and why the
  badge never moves (semver rejects `1.26`, `invalid-value`). **Measured:** `semver.valid("1.26")`
  is `null` with the repository's `node_modules/semver`; the `images.go` regex returns both pins
  under Node; `picomatch('**/test/**')` matches `test/testimages/images.go`; Docker Hub carries
  `valkey/valkey` 9.1.2 and 8.1.10; `git describe --contains` places `24c723d`, `3a8b660`,
  `1cb0eb0`, `95f67ae` in releases. **New findings:** the Kind input was maintained only from
  2026-09-16; `v1.33.4` is Kind `v0.30.0`'s build; a release rolls every multi-replica data tier
  (ADR 0005 D11), so CI-only `fix` bumps cost a fleet roll; the Valkey test-image manager never
  extracts (`**/test/**` ignored, new appendix, more rule-1 statements); `renovate.json` is loaded
  twice (new appendix, cause inferred); kind-action caches kubectl by Kind version. **Locations
  re-read** at `84a39c2`: `release.yml:610` -> `:609` (integration `:777`), ticket 054 `:31` ->
  `:31-32`. **Options, removed:** decision 1 C (declare the pins hand-maintained) - hand
  maintenance has measurably failed, both pins are end of life seven months on; decision 1 D (drop
  `node_image`, let Kind's default decide) - Kind bumps automerge including majors
  (`renovate.json:154-175`), it cannot hold Kubernetes below Kind's default 1.37.0, which no
  released cert-manager declares, and its kubectl premise was false (kubectl already follows
  kind-action's default; client and server would align only by coincidence); decision 2 A (declare
  `ENVTEST_VERSION` hand-maintained) - its premise is refuted, the module is tagged since
  `v0.24.0`; decision 3 B (wire the template in through `writerOpts`) - speculative product scope
  that changes every release note and needs a template rewrite first, so decision 3 has one option
  left and became decision-free work item 3; decision 4 a (keep `fix` for CI-only bumps and record
  why) - no reason for it was found. **Options, added:** decision 1 E (literals in the kind-action
  inputs, built-in extractor), runner-up; decision 1 A gains `separateMultipleMinor`, a stated
  digest stance, a review criterion and a dashboard closing condition; decision 2 C (derive from
  `go.mod`, Kubebuilder's scaffold precedent); decision 4 (new) with options b and c. **Recommendation
  changes:** decision 2 B -> C, because C removes both residuals B states for itself (a separate
  release per bump, each a fleet roll; tool/library drift); decision 1 stays A, now justified by
  the single literal rather than by E's two PRs, which are inside the skew policy and can be
  grouped. **Work list:** "XS items that need no decision: none" struck; five decision-free items
  (kubectl alignment, `ignorePaths`, deleting the template, the double load, `DEVELOPER.md:266`).
  **Frontmatter:** state filed -> analysed (every Not-verified item settled or narrowed, three
  decisions costed with one option marked); title widened to the two new gaps; urgency stays `now`
  by rule 1, now with more statements (DEVELOPER.md:268 and the Valkey test-image statements);
  severity stays low with the unverified escalation condition in its comment; effort stays S;
  security stays none (the double load is hardening at most, no path found); blocked-by names the
  three open decisions. **Review of this re-verification, same day:** `c6f97e2` added the first six
  Makefile `# renovate:` comments, not every one (`a36fea8` added govulncheck's); ADR 0005 D11's
  own boundary (floating `image.tag`, kustomize path: no roll, `0005:310-313`) added; decision 4 b
  must list the full module `depName`s; the Valkey-image statements are six in five files; the
  toolchain table is `DEVELOPER.md:260-273`; work item 5 also covers `DEVELOPER.md:273` (npm
  manager now observed); the evidence of the removed F4 option B kept under F4; the T30 line
  trimmed to what the embargo allows; T54's own re-verification already carries the two
  corrections this ticket found for it.
  Cross-ticket: in the consistency pass of the same day, the T44 bullet was corrected (the
  `E2E_TESTS` deletion is in `bcc63c9`, not `84a39c2`) and extended (044's E1 B and F1 C remove
  the kustomize pin, so the Makefile manager count drops to 5 and decision 4 b's `chore` list
  loses kustomize), and T30's state corrected to `analysed`.
  Sweep: The citations of ticket 054 by line (`054:31-33`, `054:44-49`, and `:31-32` in the Go-badge
  bullet) were valid only for 054's `84a39c2` version and 054's lines have moved since; they now
  name 054's Fact bullets "The six custom managers" and "A new custom manager would automerge unless
  it is told not to". The T30 line now carries only the words the embargo permits. The `release.yml`
  citations were re-checked against `84a39c2` and all hold. Frontmatter, options and work list
  unchanged.
- 2026-09-27: adversarial review of the enrichment. Corrected in place: the override rule of
  option A (and B's commit-type rule) must sit after `:274-283`, not only after `:225-236`, or
  the `fix` rule overwrites `chore`; `235fb45` was released in `v1.12.7` together with
  `9925539`, so the "not tagged" note is struck; setup-envtest also runs in the required
  `Unit Tests` job, so D4 option B's proof is `make test-unit` plus `make test-integration`.
  Spot-checked and holding: `release.yml:23`, `:151`, `:157-158`, `:316`, `:331`, `build.yml:153`,
  `Makefile:5`, `:38`, `:49-50`, `:164-166`, `:200`, `go.mod:16`, `renovate.json:29`, `:166-175`,
  `:225-250`, `:274-290`, `:300`, `:331-357`, ADR 0017 `:135`, `:270`, `:535` (and D55 as the last
  decision), ADR 0031 `:195`, the `DEVELOPER.md` lines and `release-template.hbs:5-8`. No XS item
  without a decision, confirmed. Frontmatter values unchanged; the recommendations stand.
- 2026-09-27: enriched. Re-verified at `4a7543e`. Recorded that `renovate.json:225-236`
  automerges every custom regex manager and that `:274-283` makes each bump a `fix` (and
  `21c0b85` was tagged `v1.12.1`). Both upstream release lists read. D4 option B corrected, since
  setup-envtest has semver tags since `v0.24.0`, and it is now the recommended option. F4's
  reference list corrected, option D added, and decisions ordered (F4 and D4 first, then the
  managers together with T54). No XS item needs no decision. Effort XS -> S: two managers, an
  override rule with a commit type, D4's integration run, F4, docs, and ADR 0017 D56 at close.
  Urgency stays `now` (rule 1, the D4 comment and `renovate.json:29`).
- 2026-09-27 — D4 (`ENVTEST_VERSION`) and F4 (`release-template.hbs`) appended as members of the
  same family, moved from a ticket that had bundled them with unrelated work; both re-verified the
  same day, and D4's original verification ("count 6, not 5") corrected, because govulncheck is
  now pinned and the count is 6 without envtest. Title widened; urgency later -> now by rule 1
  (false statements in tracked files).
- 2026-09-27 — filed from the documentation restructure. The Kind and Helm half of the finding as
  handed over was refuted by the bot commits `795d332` and `d3e731f`; the two unmanaged pins were
  found while verifying.
