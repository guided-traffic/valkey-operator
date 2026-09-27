---
id: T58
title: no CI gate renders the chart's non-default paths or checks its refusals
state: analysed       # every claim checked, options costed
severity: low         # refusals and posture work today; a regression merges green, and a broken hook posture fails `helm upgrade` loudly in an enforcing namespace
security: hardening
threat: "would additionally cover a template change that silently breaks one of the chart's refusals, the operator and hook pod posture on a non-default path, or the hook Job's pod posture on any path (helm install never creates the pre-upgrade Job): today such a change merges green"
urgency: later        # rule 4: cheap known fix
effort: S
blocked-by: decision  # Q1 the tooling, then Q2 where it runs
filed-from: T31, section "Further security measures — not in this change, each open" (archive/031)
opened: 2026-09-27
decided:
done:
---

# T58 - no CI gate renders the chart's non-default paths or checks its refusals

## Current state

**What CI renders.** No workflow and no Makefile target runs `helm template` or `helm lint`. CI
renders the chart only through the e2e job's `helm install`
([`release.yml:353`](../../.github/workflows/release.yml)) with the defaults plus
[`test/e2e/helm-values.yaml`](../../test/e2e/helm-values.yaml) (image, resources, leader
election, a two-entry allow-list); `podSecurity` and `image.digest` stay at their defaults. A
failed render still fails the leg (the debug step after it exits 1). No Go test renders the
chart; `go.mod` requires no `helm.sh` module.

**What the chart refuses.** Six `fail` sites in
[`_helpers.tpl`](../../deploy/helm/valkey-operator/templates/_helpers.tpl) refuse ten value
shapes; nothing else in `templates/` calls `fail` or `required`, and there is no
`values.schema.json`:

| Site | Refused shape |
|---|---|
| :72 | `image.digest` not `sha256:<64 hex>` |
| :101 | `localhostProfile` under `RuntimeDefault` |
| :106 | `Localhost` without a path |
| :108-109 | path with a leading `/`; path with a `..` element |
| :114 | any type other than `RuntimeDefault`/`Localhost`, `Unconfined` and `""` included |
| :138-139 | allow-list entry empty, with a leading `/`, with a `,`, with a `..` element |

The chart refuses only those exact shapes: the allow-list entries `" "` and `" /abs.json"` and
the operator's own `localhostProfile: " /abs.json"` render with exit 0 (whether the latter
should be refused is T72's decision).

**What the posture comes from.** `valkey-operator.podHardening` (`_helpers.tpl:86-116`:
`automountServiceAccountToken: true`, `enableServiceLinks: false`, `hostUsers: false` when
`podSecurity.userNamespaces` is set, `runAsNonRoot`, uid/gid/fsGroup 65532, the seccomp profile)
and `valkey-operator.containerSecurityContext` (:121-129: `privileged: false`,
`allowPrivilegeEscalation: false`, `readOnlyRootFilesystem: true`, `drop: [ALL]`), included at
[`deployment.yaml:30`](../../deploy/helm/valkey-operator/templates/deployment.yaml) and 54 and
[`pre-upgrade-job.yaml:27`](../../deploy/helm/valkey-operator/templates/pre-upgrade-job.yaml) and
37. The operator image appears at `deployment.yaml:34`, 39, 49 and `pre-upgrade-job.yaml:34`. The
Job renders when `preUpgradeHook.enabled` (default true) and is a `pre-upgrade` hook only, so
`helm install` never creates it.

**Helm behaviour the check must respect** (identical in v3.21.3 and v4.3.0):

- `helm lint` never detects a refusal: `fail` is a no-op in lint mode (`helm lint --strict ...
  --set podSecurity.seccompProfile.type=Unconfined` exits 0). Negative rows must run
  `helm template` and check exit code and stderr.
- The error reads `execution error at (<file:line:col>): <message>`. The location depends on
  which templates render (the Job's line with the hook on, the Deployment's with it off) and
  `-s` filters output, not evaluation, so a test matches the message text, never the location.
- `--set 'valkeyPodSecurity.allowedSeccompLocalhostProfiles={""}'` passes the literal string
  `""` and exits 0; the empty entry needs a values file (`- ""`) or `--set-json`.
- The defaults render nine documents; `helm template -s templates/deployment.yaml` and
  `-s templates/pre-upgrade-job.yaml` each output exactly one object.

**Tooling around it.** CI pins `helm` v4.3.0 via `azure/setup-helm@v5`
([`release.yml:313-316`](../../.github/workflows/release.yml),
[`build.yml:150-153`](../../.github/workflows/build.yml)); Renovate keeps these copies in step
and automerges them. `Code Linting` ([`release.yml:498-519`](../../.github/workflows/release.yml))
installs no `helm`. `make e2e-local` calls `helm` from `PATH`
([`Makefile:218`](../../Makefile)). `k8s.io/pod-security-admission` is already a direct require
([`go.mod:14`](../../go.mod)), used by
[`internal/builder/pod_security_test.go`](../../internal/builder/pod_security_test.go) under
ADR 0017 D52. The required contexts are the twelve of ADR 0017 D47, held in repository ruleset
"main" (id 23985346), not in classic branch protection.

**Impact.** A pull request that breaks a refusal, or the posture on a non-default path, is green.
On the default path the operator Deployment's `restricted` controls are caught by the
`kubectl label --dry-run=server` subtest of the hardening e2e
([`pod_hardening_test.go:446-451`](../../test/e2e/pod_hardening_test.go)), but that tests only
existing pods: a posture change confined to `pre-upgrade-job.yaml` (dropping the includes at :27
or :37) is admitted by no CI test on any path. Fields `restricted` does not require are checked
on no path. In a namespace enforcing `restricted`, a broken hook posture makes `helm upgrade` fail
before the Deployment is updated; elsewhere the hook pod runs with the weaker posture silently.
Gap [H-15](../security/operator-pod-posture.md#h-15)
([`operator-pod-posture.md:123-131`](../security/operator-pod-posture.md)) overstates the
coverage: it credits the dry run for the hook posture too.

## Required changes

### Independent of the open questions

- Correct H-15's sentence at `operator-pod-posture.md:123-131`: the dry-run subtest covers the
  operator Deployment only; a change confined to `pre-upgrade-job.yaml` is admitted by no CI test.

### Depends on the answers

1. **The check** (Q1), with a Makefile target separate from `lint`, so `make lint` needs no
   `helm`. `helm` comes from `PATH`. Whatever the tooling:
   - values pass through values files, never `--set {""}` for the empty allow-list entry;
   - ten negative rows, one per refused shape, via `helm template`, matching the message
     substring, never the location;
   - accepted-shape rows pin `" "`, `" /abs.json"` (allow-list) and `localhostProfile:
     " /abs.json"` (operator); if T72 lands first with its option A, the last becomes an eleventh
     negative row;
   - positive rows: defaults, a valid `image.digest`, `podSecurity.userNamespaces=true`,
     `Localhost` with a relative path, a non-empty allow-list, `preUpgradeHook.enabled=false`.
     Each asserts on Deployment and Job the fields `restricted` does not require
     (`automountServiceAccountToken: true`, `enableServiceLinks: false`, uid/gid/fsGroup 65532,
     seccomp type and path, `hostUsers: false` where set, `privileged: false`,
     `readOnlyRootFilesystem: true`, empty `capabilities.add`) and `@sha256:` at all four image
     sites.
2. **The CI placement** (Q2), with its own `azure/setup-helm@v5` step at v4.3.0.
3. **Docs**: the gate into ADR 0017 (a new tier under A2); the ADR 0033 residual-risk chart bullet
   amended; H-15 rewritten to name the check; the target into `DEVELOPER.md` (CI table),
   `docs/developer/testing.md` (tier table, under A2) and the `CLAUDE.md` Makefile table.
4. **Tests that prove it**: the target is green on the current chart and in CI; deleting one
   clause of a refusal (for example `(contains "," .)` at `_helpers.tpl:138`) turns exactly that
   shape's row red; deleting the include at `pre-upgrade-job.yaml:27` turns the Job's rows red; the
   positive control (A2) is denied.

## Open questions

### Q1: How is the check written?

The check must run `helm template` over a values matrix and assert refusals and posture. The
question is whether posture is checked by restating fields or by running the Pod Security
evaluator on the rendered pods, and whether anything new enters `go.mod` or the runner.

- **A - shell script under `hack/` plus a Makefile target.** Negative rows check exit code and
  message; positive rows grep `helm template -s` output. Cost S, no module. Every posture row
  restates fields, the shape ADR 0017 D52 rejects for the admission question; grep cannot reliably
  tell pod-level from container-level `securityContext`; no linter covers the script.
- **A2 - build-tagged Go test that execs `helm`, with the Pod Security evaluator (recommended).**
  For example `test/chart/` under `//go:build chart`, `make test-chart`: decode the output into
  `appsv1.Deployment` and `batchv1.Job`, evaluate both pod templates with
  `k8s.io/pod-security-admission` at `restricted`/`latest` on every row, plus a positive control
  that strips `securityContext` and must be denied; field checks only for what `restricted` does
  not require; ten named negative subtests; fails, never skips, without `helm`. Cost S, no new
  module. The tagged file is not linted until T43 lands; the evaluator is the library's profile,
  not the cluster's.

A2 is the only option that admits the hook Job's pod in PR CI with the checks the API server
runs, closing the gap no e2e can reach, at the same cost as A and without restating fields.

**Answer:** _open_

### Q2: Where does the check run in CI?

A job gates a merge only if it is a required context in ruleset 23985346 (ADR 0017 D47's twelve).
The hosting job needs its own `setup-helm` step either way.

- **(i) New job, e.g. `Helm Chart`.** Clearest failure name, runs in parallel, follows the
  image-tools and release-tooling precedent. Needs an admin edit of ruleset 23985346, an entry in
  `semantic-release`'s `needs:` ([`release.yml:1148`](../../.github/workflows/release.yml)), and
  D47 amended to thirteen; a forgotten ruleset edit leaves a gate that does not block.
- **(ii) Step in `Code Linting` after `make lint` (recommended).** With
  `if: success() || failure()` so a lint failure does not hide the result. Cost XS, no ruleset,
  `needs:` or D47 change; a chart failure shows as `Code Linting` red, the step name says which.

(ii) gates exactly as strongly as (i), because `Code Linting` is already required, and avoids
three edits including an admin action, for a low hardening item.

**Answer:** _open_

## Not verified

- That a hook posture regression fails `helm upgrade` in a namespace enforcing `restricted`:
  read from Helm's hook semantics; one upgrade on Kind with a broken hook posture would settle it.
- The v4.3.0 binary itself was not run; its behaviour is read from source. The first CI run of
  the check settles it.

## Related

- T43 - a build-tagged A2 adds `chart` to its `run.build-tags` list, whichever lands second.
- T45 - cites the same `setup-helm` version line, `release.yml:316`.
- T29, T48, T49, T53, T56 - each adds non-default render paths or refusals that become rows of
  this check, added by whichever lands second.
- T70 - owns the "branch protection" wording that the ruleset contradicts and ADR 0017 D52's stale
  version.
- T72 - decides whether the chart refuses the operator's `localhostProfile` with a leading blank.
