---
id: T58
title: no CI gate renders the chart's non-default paths or checks its refusals
state: analysed       # was filed; every claim re-checked at 84a39c2, the required contexts read from GitHub, Helm v4.3.0 read from source, the options costed (History 2026-09-27)
severity: low         # the refusals and the posture work today, checked by hand; a regression would merge green, and a broken hook posture fails `helm upgrade` loudly in an enforcing namespace rather than silently (read, not measured; Impact)
security: hardening
threat: "would additionally cover a template change that silently breaks one of the chart's refusals (a seccomp type other than RuntimeDefault or Localhost, a Localhost path that is absolute or has a '..' element, a malformed image.digest, a bad allow-list entry), the operator and hook pod posture on a non-default path, or the hook Job's pod posture on any path (helm install never creates the pre-upgrade Job, so no CI test admits its pod): today such a change merges green"  # extended 2026-09-27 by the hook-Job case (Impact); was "... or the operator and hook pod posture on a non-default path: today such a change merges green"
urgency: later        # rule 4: cheap known fix (re-derived 2026-09-27: rule 1 does not match, the refusals shipped in v1.13.0 and the false statements found are this ticket's own lines, fixed here, and an H-15 overstatement established by reading, not measured; rule 2 no release gating; rule 3 severity low)
effort: S
blocked-by: decision  # two, in order: Decision 1 the tooling, then Decision 2 where it runs (Options); the helm binary is no longer a decision (History 2026-09-27)
filed-from: T31, section "Further security measures — not in this change, each open" (archive/031)
opened: 2026-09-27
decided:              # not recorded - no decision yet
done:                 # not recorded - not done
---

Filed on 2026-09-27 from the eleventh row of the table
["Further security measures — not in this change, each open"](archive/031-generated-pods-run-as-root.md#further-security-measures--not-in-this-change-each-open)
in the archived ticket 031, which is `done` and tracks none of its rows. The operator-facing
statement is gap [H-15](../security/operator-pod-posture.md#h-15).

## Fact

**Verified** (read 2026-09-27; re-read at `84a39c2` the same day):

- No workflow and no Makefile target runs `helm template` or `helm lint` (grep over
  `.github/workflows/*.yml` and the [`Makefile`](../../Makefile)). *(Re-checked at `84a39c2`:
  `grep -n -i helm .github/workflows/*.yml Makefile` finds only `helm install`
  ([`release.yml:353`](../../.github/workflows/release.yml), [`Makefile:218`](../../Makefile)),
  `helm package` ([`build.yml:187`](../../.github/workflows/build.yml)) and `helm repo index`
  ([`build.yml:191`](../../.github/workflows/build.yml), 222, 227). `hack/` holds only
  `boilerplate.go.txt` and `verify-release-tooling.mjs`.
  [`fleet_upgrade_test.go:986`](../../test/e2e/fleet_upgrade_test.go) runs `helm upgrade`, but
  under the build tag `fleetupgrade`, which no workflow runs; CI runs only `-tags=e2e`.)*
- CI renders the chart only through the e2e job's `helm install`
  ([`release.yml:353`](../../.github/workflows/release.yml)), with the defaults plus
  [`test/e2e/helm-values.yaml`](../../test/e2e/helm-values.yaml): image, resources, leader
  election and a two-entry allow-list. `podSecurity` and `image.digest` stay at their defaults.
  The step carries `continue-on-error`, but the debug step after it exits 1 on a failed install
  ([`release.yml:351`](../../.github/workflows/release.yml), 363-406), so a failed render still
  fails the leg.
- The refusals and the operator and hook pod posture were checked with `helm lint` and
  `helm template` by hand on 2026-09-26; "no CI gate renders the digest, user-namespace or
  `Localhost` paths or checks a refusal"
  ([ADR 0033](../adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md),
  *Residual risks*, lines 623-644). *(Added 2026-09-27: only `helm template` ever checked a
  refusal there; `helm lint` cannot, see below.)*
- ~~A CI job that can fail the build has to be a required status check, added to branch
  protection in the same change ([ADR 0017](../adr/0017-test-and-ci-policy.md) D47).~~
  *(corrected 2026-09-27 at 84a39c2: the rule holds, [ADR 0017](../adr/0017-test-and-ci-policy.md)
  D47 at lines 533-541, but the mechanism is not classic branch protection.
  `gh api repos/guided-traffic/valkey-operator/branches/main/protection/required_status_checks`
  answers 404 "Branch not protected". The twelve required contexts live in the repository
  ruleset "main" (id 23985346, enforcement active, target `~DEFAULT_BRANCH`, created
  2026-09-25T09:25:56+02:00), read with `gh api repos/guided-traffic/valkey-operator/rules/branches/main`
  and `.../rulesets/23985346`: rules `deletion`, `non_fast_forward` and `required_status_checks`
  listing Code Linting, Container Malware Scan, Cyclomatic Complexity, GoSec Security Scan,
  E2E Tests, Integration Tests (envtest), Unit Tests, Vulnerability Check, Malware Scan (Source
  Code), Generated Manifests Up To Date, Valkey Image Tools and Release Tooling - exactly D47's
  twelve, `strict_required_status_checks_policy` false; bypass actors OrganizationAdmin and one
  GitHub App integration (actor id 5070048), both "always". Adding a context means editing that
  ruleset, not the classic endpoint.)*
- *(Added 2026-09-27, re-read at `4a7543e`.)* The chart has six refusals, all in
  [`_helpers.tpl`](../../deploy/helm/valkey-operator/templates/_helpers.tpl): line 72
  (`image.digest` not `sha256:<64 hex>`), lines 101, 106, 109 and 114 (operator and hook
  `podSecurity.seccompProfile`: a path without `Localhost`, `Localhost` without a path, an
  absolute or `..` path, a type other than `RuntimeDefault`/`Localhost`), and line 139
  (a bad `valkeyPodSecurity.allowedSeccompLocalhostProfiles` entry). The posture reaches the
  operator Deployment ([`deployment.yaml:30`](../../deploy/helm/valkey-operator/templates/deployment.yaml),
  image at lines 34, 39 and 49) and the hook Job
  ([`pre-upgrade-job.yaml:27`](../../deploy/helm/valkey-operator/templates/pre-upgrade-job.yaml),
  image at line 34).
- *(Added 2026-09-27 at `84a39c2`.)* Those six `fail` sites refuse **ten value shapes**, and
  nothing else in `templates/` calls `fail` or `required`; the chart has no
  `values.schema.json`. The shapes: a malformed digest
  ([`_helpers.tpl:72`](../../deploy/helm/valkey-operator/templates/_helpers.tpl)); a
  `localhostProfile` under `RuntimeDefault` (:101); `Localhost` without a path (:106); a path
  with a leading `/` and a path with a `..` element (:108-109, one site); any type other than the
  two, `Unconfined` and the empty string included (:114, the `else` branch); and in the
  allow-list an empty entry, a leading `/`, a `,` and a `..` element (:138-139, one site). A test
  with one negative row per site stays green when one clause is deleted, for example
  `(contains "," .)` at :138.
- *(Added 2026-09-27 at `84a39c2`.)* The chart refuses only those exact shapes. Measured with
  the local `helm` v3.21.3 and a values file: the allow-list entries `" "` and `" /abs.json"`
  render as `--allowed-seccomp-localhost-profiles= , /abs.json`, exit 0 - recorded in ADR 0033
  (lines 633-637) as failing closed, because `profileList`
  ([`main.go:91-99`](../../cmd/main.go)) trims both. The operator's own
  `podSecurity.seccompProfile` with `type: Localhost` and `localhostProfile: " /abs.json"` also
  renders, exit 0, as `localhostProfile: " /abs.json"` in the Deployment and the Job, because
  `hasPrefix "/"` does not see the slash behind the blank; nothing trims that value, and ADR 0033
  does not record this case. Whether the chart should refuse it is
  [T72](072-the-chart-seccomp-path-check-passes-a-leading-blank.md)'s decision; this ticket pins
  today's behaviour (Work list item 1).
- *(Added 2026-09-27 at `84a39c2`.)* The operator and hook posture comes from two helpers,
  `valkey-operator.podHardening` ([`_helpers.tpl:86-116`](../../deploy/helm/valkey-operator/templates/_helpers.tpl):
  `automountServiceAccountToken: true`, `enableServiceLinks: false`, `hostUsers: false` when
  `podSecurity.userNamespaces` is set, `runAsNonRoot`, `runAsUser`/`runAsGroup`/`fsGroup` 65532,
  the seccomp profile) and `valkey-operator.containerSecurityContext` (:121-129:
  `privileged: false`, `allowPrivilegeEscalation: false`, `readOnlyRootFilesystem: true`,
  `drop: [ALL]`), included at
  [`deployment.yaml:30`](../../deploy/helm/valkey-operator/templates/deployment.yaml) and 54
  and at [`pre-upgrade-job.yaml:27`](../../deploy/helm/valkey-operator/templates/pre-upgrade-job.yaml)
  and 37. The Job is rendered only when `preUpgradeHook.enabled` is set (default true,
  [`values.yaml:153`](../../deploy/helm/valkey-operator/values.yaml)) and is a
  `helm.sh/hook: pre-upgrade` hook only
  ([`pre-upgrade-job.yaml:11`](../../deploy/helm/valkey-operator/templates/pre-upgrade-job.yaml)),
  so `helm install` never creates it.
- *(Added 2026-09-27.)* Rendered to stdout with the local `helm` v3.21.3: the defaults render,
  ~~and one `--set` per refusal fails each of the six with its own message.~~ *(corrected
  2026-09-27 at 84a39c2: true per `fail` site, not per shape. The empty allow-list entry cannot
  be expressed with `--set`: `--set 'valkeyPodSecurity.allowedSeccompLocalhostProfiles={""}'`
  passes the two-character string `""`, exits 0 and renders
  `--allowed-seccomp-localhost-profiles=""`. A values file holding `- ""`, or
  `--set-json 'valkeyPodSecurity={"allowedSeccompLocalhostProfiles":[""]}'`, is refused with
  `"" must be a non-empty relative path without '..' or ','`. Which values the earlier by-hand
  run used for that shape is not recorded.)* The error names `pre-upgrade-job.yaml` for the
  first five and `deployment.yaml:42` for the allow-list. *(Added 2026-09-27 at `84a39c2`: with
  `--set preUpgradeHook.enabled=false` the digest refusal still fires, naming
  `deployment.yaml:34:21`, and `--show-only templates/deployment.yaml` does not change the
  location: `helm template` evaluates every template and `-s` filters only the output, so the
  digest error still names `pre-upgrade-job.yaml:34:21`. A test matches the message text, never
  the location, and cannot scope a negative row to one object.)*
- *(Added 2026-09-27 at `84a39c2`.)* **`helm lint` never detects a refusal.** Helm's engine
  replaces `fail` with a function that returns `"", nil` in lint mode: v3.21.3
  `pkg/engine/engine.go:224-230`, v4.3.0 `pkg/engine/engine.go:257-263`
  (<https://raw.githubusercontent.com/helm/helm/v4.3.0/pkg/engine/engine.go>, same path at
  `v3.21.3`). Measured: `helm lint --strict deploy/helm/valkey-operator --set
  podSecurity.seccompProfile.type=Unconfined` exits 0 with "1 chart(s) linted, 0 chart(s)
  failed", and `--set image.digest=sha256:abc` only logs `[INFO] Fail: ...`. Every negative row
  has to run `helm template` and check its exit code and stderr.
- *(Added 2026-09-27 at `84a39c2`.)* Both Helm lines format a template `fail` identically:
  `execution error at (<file:line:col>): <message>` (v3.21.3 `engine.go:341`, v4.3.0
  `engine.go:501`), the message being the chart's own text.
- *(Added 2026-09-27 at `84a39c2`.)* `helm template` of the defaults renders nine documents:
  2 ClusterRole, 2 ClusterRoleBinding, 1 CustomResourceDefinition, 1 Deployment, 1 Job,
  2 ServiceAccount. `helm template -s templates/pre-upgrade-job.yaml` and
  `-s templates/deployment.yaml` each output exactly one object (`grep -c '^kind:'` = 1).
- *(Added 2026-09-27.)* CI pins `helm` v4.3.0 in the e2e job
  ([`release.yml:314-316`](../../.github/workflows/release.yml)) and in the release workflow
  ([`build.yml:150-153`](../../.github/workflows/build.yml)).
  ~~*(corrected 2026-09-27 by the review: the step is `release.yml:312-315`, the version at
  315)*~~ *(corrected 2026-09-27 at 84a39c2: the review's correction was wrong. The step is
  [`release.yml:313-316`](../../.github/workflows/release.yml): the name at 313,
  `uses: azure/setup-helm@v5` at 314, `with:` at 315, `version: 'v4.3.0'` at 316 - identical at
  `4a7543e`. The original 314-316, `uses` through `version`, was right, as is ticket 045's
  :316.)* The two required jobs that could host a step install no `helm`: `Code Linting`
  ([`release.yml:498-519`](../../.github/workflows/release.yml): checkout, setup-go,
  `go mod download`, build-essential, `make lint`) and `Generated Manifests Up To Date`
  ([`release.yml:714-754`](../../.github/workflows/release.yml)). `make e2e-local` also installs
  with `test/e2e/helm-values.yaml` ([`Makefile:218`](../../Makefile), values at 221), calling
  `helm` by bare name.
- *(Added 2026-09-27 at `84a39c2`.)* Renovate maintains the `setup-helm` version: its
  `github-actions` manager extracts the `version` input of `azure/setup-helm`
  (<https://docs.renovatebot.com/modules/manager/github-actions/>, supported-actions table), and
  `d3e731f` "chore(deps): update dependency helm to v4.3.0 (#215)" moved `build.yml` and
  `release.yml` in one PR. [`renovate.json`](../../renovate.json) automerges github-actions
  updates of every type, major included (lines 155-175). A third copy in another job is kept in
  step the same way.
- *(Added 2026-09-27.)* No Go test renders the chart. `TestHelmClusterRoleCoversGeneratedRole`
  reads `clusterrole.yaml` as plain YAML
  ([`rbac_drift_test.go:35`](../../internal/controller/rbac_drift_test.go)), and `go.mod`
  requires no `helm.sh` module. *(Re-checked at `84a39c2`: `git grep -n deploy/helm -- '*.go'`
  finds only that line.)*
- *(Added 2026-09-27 at `84a39c2`.)* The repository already carries the Pod Security admission
  checks as a library: `k8s.io/pod-security-admission v0.37.1` is a direct require
  ([`go.mod:14`](../../go.mod)), used by
  [`pod_security_test.go:16-17`](../../internal/builder/pod_security_test.go) under
  [ADR 0017](../adr/0017-test-and-ci-policy.md) D52, which rejects hand-written field checks for
  the admission question (lines 1252-1257) and keeps field assertions for what `restricted` does
  not require; `TestPodSecurity_EvaluatorRefusesTheLegacyShape` (:139) is the D11 positive
  control. The in-repo precedent for a build-tagged Go test that drives an external binary is
  `make test-image-tools` ([`Makefile:147-150`](../../Makefile),
  [`image_tools_test.go:63`](../../test/imagetools/image_tools_test.go)).
- *(Added 2026-09-27 at `84a39c2`.)* Helm v4.3.0's `go.mod`
  (<https://raw.githubusercontent.com/helm/helm/v4.3.0/go.mod>) has no `replace` directive and
  47 direct and 134 indirect requirements, among them `k8s.io/apiserver`, `cli-runtime`,
  `kubectl` (v0.37.0) and `sigs.k8s.io/kustomize` (v0.21.1), none of which this repository
  requires. Every module it shares with this `go.mod` is at or below ours today (`k8s.io/api`,
  `apimachinery`, `client-go` v0.37.0 against v0.37.1; `controller-runtime` v0.24.1 against
  v0.25.1; `kube-openapi` 20260721 against 20260821).
- *(Added 2026-09-27 at `84a39c2`.)* The feature this ticket guards is released:
  `git merge-base --is-ancestor b13377e v1.13.0` succeeds.
- *(Added 2026-09-27 at `84a39c2`.)* No past render regression: `git log --oneline --no-merges
  -- deploy/helm/valkey-operator/templates/ deploy/helm/valkey-operator/values.yaml
  ':!deploy/helm/valkey-operator/templates/crd.yaml'` without the "Release Helm chart" commits
  gives 25 commits (`0a90483` to `bcc63c9` in log order, the oldest two both of 2026-02-17); no
  subject names a broken render or a refusal fix.
  The chart diffs of the fix-typed `7d1af94`, `3c78c33`, `6aa85f1`, `0a90483` and `4b904f8` were
  read, the subjects only of `86cf1f4`, `d8d57ab`, `9e5634d` and `2b4f1a3`. The gate is
  preventive.

**Not verified:**

- ~~Whether a render matrix in CI would have caught any past chart regression; no history of one
  was searched.~~ *(corrected 2026-09-27 at 84a39c2: searched, none found, see Verified.)*
- ~~*(Added 2026-09-27.)* The refusal messages and `helm lint` under CI's `helm` v4.3.0; only
  v3.21.3 was run. A script that greps the message text, not the error layout, avoids depending
  on either.~~ *(corrected 2026-09-27 at 84a39c2: settled from the v4.3.0 source - same message
  pass-through, same error format, `fail` a no-op in lint mode, see Verified. The v4.3.0 binary
  itself was not run.)*
- ~~*(Added 2026-09-27.)* That `Code Linting` and `Generated Manifests Up To Date` are required
  contexts on GitHub today. ADR 0017 D47 lists both, and GitHub was not queried.~~ *(corrected
  2026-09-27 at 84a39c2: queried, both are required through ruleset 23985346, see Verified.)*
- ~~*(Added 2026-09-27 at `84a39c2`.)* What the API server and kubelet do with the operator's
  `localhostProfile: " /abs.json"`: whether pod validation accepts a path starting with a blank,
  and where kubelet then resolves it under its seccomp root. Settled by reading the pod
  validation and kubelet seccomp path code at the cluster's Kubernetes version, or by one
  install on Kind.~~ *(moved 2026-09-27 to
  [T72](072-the-chart-seccomp-path-check-passes-a-leading-blank.md), which read it from the
  Kubernetes v1.36.1 source, not measured.)*
- *(Added 2026-09-27 at `84a39c2`.)* That a hook posture regression fails `helm upgrade` loudly
  in a namespace enforcing `restricted`: read from the hook semantics (the Job's pod is refused,
  the pre-upgrade hook never succeeds, the Deployment is not updated), not measured.
- *(Added 2026-09-27 at `84a39c2`.)* Whether `helm` is preinstalled on the self-hosted runner that
  runs `Code Linting`. The comment at [`release.yml:32-34`](../../.github/workflows/release.yml)
  speaks only of the E2E legs. It does not matter for the options: nothing in the repository
  guarantees `helm` on any runner, so the hosting job installs its own.
- *(Added 2026-09-27 at `84a39c2`.)* helm-unittest's install paths under Helm 4 (git only with
  `--verify=false`; a release archive with GPG verification; OCI from plugin 1.1.0) were read
  from its README on `main`, not at a tag, and not run.

**Found while verifying, owned elsewhere** *(added 2026-09-27; filed the same day)*:

- The "branch protection" wording that the ruleset read above contradicts (ADR 0017 D47 and its
  residual risks, `DEVELOPER.md`, `CLAUDE.md`) and the stale v0.37.0 of ADR 0017 D52 are filed as
  items (g) and (h) of
  [T70](070-tracked-comments-and-adrs-state-what-the-code-contradicts.md), which collects the
  decision-free corrections of tracked comments, ADR sentences and pages found false or stale.
- The operator's own `localhostProfile` with a leading blank passing the chart refusal is filed
  as [T72](072-the-chart-seccomp-path-check-passes-a-leading-blank.md), which decides whether the
  chart refuses it; this ticket keeps its accepted-shape row (Work list item 1).

## Impact

A pull request that breaks a refusal or the posture on a non-default path is green.
~~On the default path, a change that drops a control Pod Security `restricted` requires is
caught by the restricted-namespace e2e ([ADR 0017](../adr/0017-test-and-ci-policy.md) D54);
what that test does not check is listed under gap [H-15](../security/operator-pod-posture.md#h-15).~~
*(corrected 2026-09-27 at 84a39c2: D54's `TestE2E_PodSecurity_RestrictedNamespace`
([`pod_security_test.go:90`](../../test/e2e/pod_security_test.go)) works only in its own namespace
`e2e-restricted` and never touches `valkey-operator-system`. On the default path the operator
**Deployment's** `restricted` controls are caught by the subtest "the operator's own namespace
would pass Pod Security restricted" of the ADR 0033 hardening e2e
([`pod_hardening_test.go:446-451`](../../test/e2e/pod_hardening_test.go): `kubectl label
--dry-run=server` on the operator namespace, no "Warning"), which runs in CI's single-node legs.
That dry run tests only pods that exist, and the pre-upgrade hook Job never exists during the
e2e, so **a posture change confined to `pre-upgrade-job.yaml`** - dropping the includes at
:27 or :37 - **is admitted by no CI test on any path**. A hook-template change that breaks the
render or trips a refusal still fails the e2e `helm install`, which renders every template. A
posture change inside the shared helpers is still caught through the Deployment. Fields
`restricted` does not require - `enableServiceLinks`, `privileged: false` stated, the read-only
root filesystem, the pinned uid - are caught on no path, as H-15 already says.)*

Gap [H-15](../security/operator-pod-posture.md#h-15)
([`operator-pod-posture.md:107-131`](../security/operator-pod-posture.md)) overstates the
coverage: its sentence at lines 123-131, in a paragraph about "the operator and hook pod
posture" (line 116), credits the dry run with catching a dropped `restricted` control on the
default path, and the dry run covers the Deployment only. The overstatement was established by reading the
test and Helm's hook semantics (<https://helm.sh/docs/topics/charts_hooks/>: pre-upgrade
"Executes on an upgrade request"), not measured.

Severity stays low with the hook case: in a namespace enforcing `restricted`, a broken hook
posture makes the pre-upgrade Job's pod refused, so `helm upgrade` fails before the Deployment is
updated and the old operator keeps running (read, not measured). In a namespace that does not
enforce it, the hook pod runs with the weaker posture for the duration of the migration, silently.

## Options

### Decision 1: the tooling

**Mechanism.** The chart refuses ten value shapes at render through six `fail` sites in
[`_helpers.tpl`](../../deploy/helm/valkey-operator/templates/_helpers.tpl) (:72, :101, :106,
:109, :114, :139) and builds the operator and hook posture from two helpers (:86-116, :121-129),
included into the Deployment (:30, :54) and the hook Job (:27, :37); the operator image
reference appears at four sites
([`deployment.yaml:34`](../../deploy/helm/valkey-operator/templates/deployment.yaml), 39, 49 and
[`pre-upgrade-job.yaml:34`](../../deploy/helm/valkey-operator/templates/pre-upgrade-job.yaml)).
PR CI renders only the defaults plus the e2e values, and the only admission check of an operator
pod is the dry run at `pod_hardening_test.go:446-451`, which never sees the hook Job. Every
negative row has to run `helm template` and match the message text, because `helm lint` cannot
see a refusal and the location depends on which templates are rendered (Fact: the Job's line
with the hook on, the Deployment's with it off). This decision chooses
how the rows and their assertions are written and whether anything new enters `go.mod` or the
runner. It does not change the chart, the operator, or where the check runs (Decision 2).

**Which `helm` runs it is not a decision** *(settled 2026-09-27)*: the check calls `helm` from
`PATH`, as `make e2e-local` already does ([`Makefile:218`](../../Makefile)); CI installs it in
the hosting job with `azure/setup-helm@v5`, version v4.3.0, which Renovate keeps in step with the
two existing copies. A developer on Helm v3 runs the same check: the message pass-through, the
error format and the lint-mode no-op are identical at v3.21.3 and v4.3.0 (Fact). A pinned
`bin/helm-v4.3.0` was considered and removed (History).

- **A — a shell script under `hack/` plus a Makefile target.** `helm template` over a values
  matrix; each negative row checks a non-zero exit and the message substring; positive rows grep
  the output of `helm template -s templates/deployment.yaml` and `-s
  templates/pre-upgrade-job.yaml`, which isolate one object each (measured), so per-object
  assertions need nothing but grep. Cost S, no module. Consequences: every positive row is a
  restatement of fields, the shape [ADR 0017](../adr/0017-test-and-ci-policy.md) D52 rejected for
  the admission question because it stays silent when `restricted` gains a check (lines
  1252-1257); a script cannot run the Pod Security evaluator; telling the pod-level from the
  container-level `securityContext` with grep alone is fragile, and `yq`/`jq` on the runner are
  unverified and would be another unpinned tool; the Makefile runs no linter over shell scripts.
- **A2 — a build-tagged Go test that execs `helm`, with the Pod Security evaluator
  (recommended).** For example `test/chart/` under `//go:build chart` and `make test-chart`: each
  row runs `helm template` with a values file, decodes the output with
  `k8s.io/apimachinery/pkg/util/yaml` (already used by `rbac_drift_test.go:30`) into
  `appsv1.Deployment` and `batchv1.Job`, and evaluates both `spec.template` pod specs with
  `k8s.io/pod-security-admission` at `restricted`/`latest`, the way
  `internal/builder/pod_security_test.go` does, on every row of the matrix. A D11 positive
  control strips a rendered template of its `securityContext` and must be denied. Field checks
  remain only for what `restricted` does not require. The ten negative rows are named,
  table-driven subtests asserting a non-zero exit and the message substring. The test fails,
  never skips, when `helm` is missing. Cost S: one test file and one target, no new module (both
  libraries are already direct requires, [`go.mod:11-14`](../../go.mod)). Consequences: like
  every tagged tier, the file is neither linted nor vetted until ticket 043 lands, and then its
  `run.build-tags` list gains `chart`; `docs/developer/testing.md` and `DEVELOPER.md` gain a tier
  row; the test is a second importer of `k8s.io/pod-security-admission`, whose version moves with
  the `k8s-go-modules` group as it does today; the evaluator is that library's profile, not the
  cluster's, the gap ADR 0017 already records for D52 (residual risks, lines 1343-1345).

**A2 is recommended** because it is the only option that admits the rendered hook Job's pod in
PR CI with the checks the API server runs, which closes the hook-Job gap of Impact that no e2e
can close (`helm install` never creates that Job), and does so without a module, a plugin or a
runner tool. Checkable: after A2 lands, deleting the include at `pre-upgrade-job.yaml:27` turns
the Job's evaluator row red, and deleting `(contains "," .)` at `_helpers.tpl:138` turns exactly
the comma subtest red. It beats the runner-up A at the same effort S because A can only restate
fields, which D52 already rejected for this question, and its revert check has no named rows.

### Decision 2: where it runs

**Mechanism.** A job gates a merge only if it is one of the required contexts, today ADR 0017
D47's twelve in ruleset 23985346; nothing in the repository compares that list with the
workflows ([ADR 0017](../adr/0017-test-and-ci-policy.md) residual risk "Nothing enforces D47",
lines 1310-1314), though it can be read back with `gh api .../rules/branches/main`.
`semantic-release`'s `needs:` ([`release.yml:1148`](../../.github/workflows/release.yml)) lists
every gate job. The hosting job needs its own `azure/setup-helm@v5` step (Fact). This decision
chooses which context turns red for a chart failure and whether the ruleset, `needs:` and D47
change. It does not change what the check asserts (Decision 1).

- **(i) a new job, e.g. `Helm Chart`, with its own context.** The clearest failure name, runs in
  parallel, and follows both in-repo precedents for a check that is not a unit test: image tools
  (D42) and release tooling (D46) each got their own required job. Cost S plus an admin action:
  an edit of ruleset 23985346 (the Rules UI or `PUT /repos/guided-traffic/valkey-operator/rulesets/23985346`),
  an entry in `semantic-release`'s `needs:`, D47's list amended from twelve to thirteen, and a
  row in the [`DEVELOPER.md`](../../DEVELOPER.md) CI table (lines 280-296). A forgotten ruleset
  edit leaves a gate that does not block - the failure D47 was written for - but the read-back
  in Verification catches it.
- **(ii) a step in `Code Linting` (recommended)**
  ([`release.yml:498-519`](../../.github/workflows/release.yml)), after `make lint`, preceded by
  `azure/setup-helm@v5` with version v4.3.0 as at
  [`release.yml:314-316`](../../.github/workflows/release.yml), and carrying
  `if: success() || failure()` so a lint failure does not hide the chart result. The job already
  has Go set up. Cost XS in the workflow. Consequences: a chart failure shows as `Code Linting`
  red and only the step name says which check failed; the `DEVELOPER.md` row "Code Linting |
  `make lint`" gains the chart target.

**(ii) is recommended**, narrowly: `Code Linting` is already one of ruleset 23985346's twelve
contexts and in `needs:` as `linter`, so the check gates exactly as strongly as a new job with no
ruleset edit, no `needs:` entry and no D47 amendment - three edits only (i) needs, one of them an
admin action, for a low hardening item (the `DEVELOPER.md` CI table changes under both). The runner-up (i) buys a distinct context name and consistency with
D42 and D46; the step name recovers most of the first, and under A2 the step is a Go test rather
than a lint, which weakens (ii)'s old "a static check like lint" argument but not its footprint.

## Decision

None yet.

## Work list

~~No item here is both XS and free of the decisions above; every item waits on them.~~
*(corrected 2026-09-27 at 84a39c2: item 0 is XS and waits on no decision.)*

0. *(no decision needed, XS)* Correct H-15's scope sentence
   ([`operator-pod-posture.md:123-131`](../security/operator-pod-posture.md)): the dry-run
   subtest covers the operator Deployment only, and a change confined to `pre-upgrade-job.yaml`
   is admitted by no CI test. It can land ahead of the decisions; item 4 rewrites the paragraph
   again once the check exists.
1. *(waits on Decision 1)* The check and a Makefile target separate from `lint`, so a local
   `make lint` needs no `helm`. Whatever the tooling:
   - rows pass values through values files; the empty allow-list entry never through
     `--set {""}` (Fact);
   - negative rows use `helm template`, never `helm lint`, and match the message substring,
     never the template location; one row per refused shape, ten in all;
   - accepted-shape rows pin today's pass-throughs, the allow-list entries `" "` and
     `" /abs.json"` and the operator's `localhostProfile: " /abs.json"`, so a tightening or
     loosening is a visible change (if T72 lands with its option A first, the last becomes an
     eleventh negative row, as T72's Work list item 0 states);
   - positive rows: the defaults, a valid `image.digest`, `podSecurity.userNamespaces=true`,
     `Localhost` with a relative path, a non-empty allow-list, and `preUpgradeHook.enabled=false`
     (the refusals still fire through the Deployment). Each asserts, on the Deployment and the
     Job, the fields `restricted` does not require - `automountServiceAccountToken: true`,
     `enableServiceLinks: false`, `runAsUser`/`runAsGroup`/`fsGroup` 65532, the seccomp type and
     path, `hostUsers: false` where set, `privileged: false`, `readOnlyRootFilesystem: true`, an
     empty `capabilities.add` - and `@sha256:` at all four image sites. Under A2 the `restricted`
     controls themselves come from the evaluator plus its positive control; under A they are
     restated field by field (`runAsNonRoot`, `allowPrivilegeEscalation: false`, `drop: [ALL]`).
   - `helm` from `PATH`; under A2 the test fails when it is missing.
2. *(waits on Decision 2)* The CI step or job, with its own `azure/setup-helm@v5` step at
   v4.3.0. For (ii) the step carries `if: success() || failure()`. For (i) also the ruleset
   23985346 edit, the `needs:` entry at `release.yml:1148`, D47's list and the `DEVELOPER.md`
   row.
3. *(waits on both)* Revert check (Verification).
4. Close ([ADR 0034](../adr/0034-tickets-are-work-lists-that-get-archived.md)): the gate into
   ADR 0017 (a new tier under A2), the ADR 0033 residual-risk chart bullet (lines 623-644)
   amended, gap [H-15](../security/operator-pod-posture.md#h-15)
   ([`operator-pod-posture.md:107-131`](../security/operator-pod-posture.md)) rewritten to name
   the check, the target into `DEVELOPER.md` (CI table, line 284 for (ii)), into
   `docs/developer/testing.md` (tier table, under A2) and into the `CLAUDE.md` Makefile table,
   `chart` into ticket 043's build-tag list if that ticket landed first, `git grep` `058` and
   `T58`, then move to `archive/`.

**Related tickets** *(added 2026-09-27)*: T43 - a build-tagged A2 adds the tag `chart` to its
`run.build-tags` list, whichever lands second. T45 - its `release.yml:316` for the `setup-helm`
version is right. T49 - its Verification item 2 (no NetworkPolicy by default, exactly one with
the value on) is a row of this check, added by whichever lands second. T29 - ~~a `.Capabilities`
gated template renders nothing under `helm template` without `--api-versions`, so its row must
pass it.~~ *(corrected 2026-09-27, consistency pass: 029's re-verification removed the
`.Capabilities` option and made "gate on the value alone" a rule of its Work list (item 5), so
its row, the policy rendered with the value on, needs no `--api-versions`; 029 asks this check
to add that row if 029 lands first, and `helm template` cannot check the CEL.)* ~~T48 (a chart
value for the metrics bind address)~~ *(corrected 2026-09-27, consistency pass: T48 now
recommends 1D, a two-state off switch: on renders byte-identical to today, off renders
`--metrics-bind-address=0`, drops the metrics container port and refuses the render with
`metrics.service`, `serviceMonitor` or `prometheusRule` enabled; T49 refuses the same three
values with an empty peer list; each refusal is a negative row)* and T56 (a namespace-scoped mode) add
non-default render paths this check should cover. T53 - if the release stamps the digest into
the published chart, the digest path becomes that chart's default; the digest row covers the
template either way. *(Precised 2026-09-27, consistency pass: 053's recommended A′ adds the
release-only values `image.releaseDigest` and `image.releaseTag` and eight render rows, among
them a refused malformed release value, which 053 asks this matrix to carry as a twin of the
malformed `image.digest` row.)* *(Added 2026-09-27, sweep:)* T72 - if its option A lands before
this check, the `localhostProfile: " /abs.json"` row of Work list item 1 is a negative row, not
an accepted shape (T72 Work list item 0). T70 - owns the branch-protection wording and the stale
ADR 0017 D52 version (its items (g) and (h)) this ticket found while verifying.

## Verification

- The new target is green on the current chart and in CI~~, and the job is listed among the
  required contexts of ADR 0017 D47~~. ~~*(corrected 2026-09-27 by the review: that clause assumed
  a new job. Under Decision 2 (i) the new context is listed in ADR 0017 D47 and in branch
  protection; under (ii) or (iii) the step runs inside a job that D47 already lists, and the
  step's name appears in that job's log on the fix commit.)*~~ *(corrected 2026-09-27 at
  84a39c2: the required contexts live in ruleset 23985346, not in branch protection, and (iii)
  is no longer an option.)* Under (i), `gh api
  repos/guided-traffic/valkey-operator/rules/branches/main` lists the new context and ADR 0017
  D47 lists thirteen; under (ii), the step's name appears in the `Code Linting` log on the fix
  commit.
- Revert check, per shape: deleting one clause of a refusal (for example `(contains "," .)` at
  `_helpers.tpl:138`) turns exactly that shape's row red, naming it; deleting the include at
  `pre-upgrade-job.yaml:27` turns the Job's rows red (under A2 through the evaluator); the
  positive control is denied.

## History

- 2026-09-27: re-verified at 84a39c2 - re-read every claim, the workflows, the Makefile, the
  chart templates, ADR 0017 and 0033 and H-15, and re-measured with the local `helm` v3.21.3.
  Locations re-read at 84a39c2: `Code Linting` is `release.yml:498-519` (was 499-520),
  `Generated Manifests Up To Date` 714-754 (was 715-756) with its error at 748 (was 749-755),
  H-15 at `operator-pod-posture.md:107-131` (was 104-126). **Found false:** the previous review's
  correction of the `setup-helm` location (312-315, "line 313") was itself wrong and is reverted
  to 313-316 with `uses` at 314 and the version at 316, in Fact and in Decision 2; Impact credited
  D54's restricted-namespace e2e, while the operator pod is admitted only by the ADR 0033
  hardening dry run and the pre-upgrade hook Job by no CI test; "one `--set` per refusal fails
  each of the six" holds per site, not per shape. **Found outdated:** all three Not verified
  items - the required contexts were read from GitHub (ruleset 23985346, not branch protection),
  Helm v4.3.0's `fail` handling and error format were read from source, the chart history was
  searched and holds no render regression. **Measured:** `helm lint --strict` exits 0 on a
  refused value (`fail` is a no-op in lint mode, also in the v4.3.0 source); six sites refuse ten
  shapes; `--set {""}` passes a literal `""`; `" "` and `" /abs.json"` pass the allow-list render
  and `" /abs.json"` passes the operator's own `localhostProfile` render; `-s` filters output,
  not evaluation; `preUpgradeHook.enabled=false` moves the refusal to the Deployment; the
  defaults render nine documents. **Options:** Decision 1 gained A2 (a build-tagged Go test
  running the Pod Security evaluator on the rendered Deployment and Job) and **the recommendation
  moved from A to A2**, because A2 admits the hook Job's pod in PR CI, which no e2e can, with no
  new module, while A can only restate fields (ADR 0017 D52); A's old reason "uses the helm
  binary CI already installs" was also wrong, the hosting job installs its own. Removed B
  (helm-unittest): a second assertion language, a plugin download on every cold runner, and no
  way to run the Pod Security evaluator, so it duplicates A2 with more supply-chain surface; its
  pin could be Renovate-managed through the Makefile custom manager, so that was not the reason.
  Removed C (Helm SDK): disproportionate for a low hardening item, 47 direct and 134 indirect
  requirements for a check the binary does, and an automerged Helm bump could raise `k8s.io/*`
  outside the `k8s-go-modules` group (ADR 0017 D48), though nothing moves today. Considered and
  not kept as a decision: which `helm` binary runs the check - option Y, a pinned
  `bin/helm-v4.3.0` via `go install helm.sh/helm/v4/cmd/helm` (ADR 0017 D49 pattern), was removed
  because its module path fixes the major, so after a Helm v5 that Renovate automerges into the
  `setup-helm` inputs it would pin local runs to a line CI no longer uses, and it compiles Helm's
  module tree per cold runner, while the source shows v3 and v4 behave identically for this
  check; option X (`helm` from `PATH`) is stated as fact in Decision 1. Decision 2: removed (iii),
  a step in `Generated Manifests Up To Date`, dominated by (ii) with an error text ("out of sync
  with the Go sources", line 748) that would be wrong for a chart failure; considered and not
  added (iv), a step in `Unit Tests`, which gates as strongly as (ii) but makes `Unit Tests` red
  for a non-unit tier and has to sit behind its coverage upload; (ii) stays recommended, now
  narrowly, with `if: success() || failure()`, and (i) is rewritten against the ruleset; the
  review's false "line 313" in Decision 1 left with the rewritten justification. Work list:
  item 1 rewritten, because one negative row per site (six) stays green when one clause of a
  site is deleted - ten negative rows, one per shape, accepted-shape rows, every helper field on both objects, four image
  sites, a hook-disabled row, and a decision-free XS item 0 for H-15. **Frontmatter:** `state`
  filed -> analysed (every claim checked, options costed); `threat` extended by the hook-Job
  case; `blocked-by` now names two decisions explicitly; severity, security, effort and urgency
  (`later`, rule 4, re-derived top-down) unchanged. Recorded, owned elsewhere and still to be
  filed: the ruleset against ADR 0017 D47, `DEVELOPER.md` and `CLAUDE.md`; ADR 0017 D52's stale
  v0.37.0; the operator `localhostProfile` blank pass-through. Reviewed the same day against the
  code at 84a39c2 and by re-running the lint, blank, `--set {""}`, `-s` and hook-off renders:
  the oldest chart commit in log order is `0a90483`, not `88b721b`; ticket 041 also records the
  ruleset read; the H-15 sentence (123-131) is quoted by what it claims; the A2 evaluator carries
  ADR 0017's library-version gap; (i)'s extra edits are three, not four; the Verification markup
  of the earlier corrections is restored.
  Cross-ticket: in the consistency pass of the same day, the Related tickets were corrected for
  T29 (no `.Capabilities` gate any more, so its row needs no `--api-versions`), T48 (now 1D, an
  off switch whose refusals, like T49's, are negative rows) and T53 (A′'s release-only values and
  its malformed-release-value row); 029 records the same row from its side.
  Filed: the ruleset wording against ADR 0017 D47, `DEVELOPER.md` and `CLAUDE.md` and ADR 0017
  D52's stale v0.37.0 as items (g) and (h) of T70, and the operator `localhostProfile`
  leading-blank pass-through as T72, together with this ticket's Not verified item on how the API
  server and kubelet treat that value, which T72 settled by reading the Kubernetes v1.36.1 source;
  "Found while verifying, owned elsewhere" now holds two pointers, the Fact on the blank points to
  T72, and Work list item 1 notes that T72's option A would turn the accepted-shape row into an
  eleventh negative row. Frontmatter unchanged: no severity, urgency or option rested on the
  moved findings.
  Sweep: Related tickets gained T72 (the accepted-shape row becomes a negative row if T72's option A
  lands first) and T70 (items (g) and (h)), which the host update had left to a later pass. The
  `release.yml` citations were re-checked against `84a39c2` and all hold. Frontmatter unchanged.
  Final pass: the Related tickets already carry T70 and T72 (the sweep's addition); checked
  against both tickets' current text - T70 still holds the ruleset wording as item (g) and D52's
  version as item (h), and T72 still plans the `" /abs.json"` row as an accepted shape that its
  option A turns into a negative row (its Work list item 0) - so no text changed. Frontmatter
  unchanged.
- 2026-09-27: reviewed - rendered the defaults and all six refusals again with local `helm`
  v3.21.3 (same messages and template locations as recorded). Corrected the `setup-helm` step
  location (312-315, not 314-316) in three places, and struck the Verification clause that
  still required a new job although Decision 2 recommends a step in an existing one.
  Frontmatter and recommendations unchanged.
- 2026-09-27: enriched - rendered the six refusals locally and added their locations. Split the
  Options into tooling (A recommended) and placement (a step in `Code Linting` recommended), and
  added a work list. `blocked-by: decision` added because both choices are open. Urgency
  (`later`, rule 4) and effort (S) unchanged.
- 2026-09-27 — filed from the row "Chart render test in CI" of archive/031, which names
  ADR 0017. Gap [H-15](../security/operator-pod-posture.md#h-15) states what is missing.
