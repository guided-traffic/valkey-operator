---
id: T58
title: no CI gate renders the chart's non-default paths or checks its refusals
state: filed
severity: low         # the refusals and the posture work today, checked by hand; a regression would merge green
security: hardening
threat: "would additionally cover a template change that silently breaks one of the chart's refusals (a seccomp type other than RuntimeDefault or Localhost, a Localhost path that is absolute or has a '..' element, a malformed image.digest, a bad allow-list entry) or the operator and hook pod posture on a non-default path: today such a change merges green"
urgency: later        # rule 4: cheap known fix
effort: S
blocked-by: decision  # two, in order: the tooling, then where it runs (Options)
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

**Verified** (read 2026-09-27):

- No workflow and no Makefile target runs `helm template` or `helm lint` (grep over
  `.github/workflows/*.yml` and the [`Makefile`](../../Makefile)).
- CI renders the chart only through the e2e job's `helm install`
  ([`release.yml`](../../.github/workflows/release.yml) line 353), with the defaults plus
  [`test/e2e/helm-values.yaml`](../../test/e2e/helm-values.yaml): image, resources, leader
  election and a two-entry allow-list. `podSecurity` and `image.digest` stay at their defaults.
- The refusals and the operator and hook pod posture were checked with `helm lint` and
  `helm template` by hand on 2026-09-26; "no CI gate renders the digest, user-namespace or
  `Localhost` paths or checks a refusal"
  ([ADR 0033](../adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md),
  *Residual risks*).
- A CI job that can fail the build has to be a required status check, added to branch
  protection in the same change ([ADR 0017](../adr/0017-test-and-ci-policy.md) D47).
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
- *(Added 2026-09-27.)* Rendered to stdout with the local `helm` v3.21.3: the defaults render,
  and one `--set` per refusal fails each of the six with its own message. The error names
  `pre-upgrade-job.yaml` for the first five and `deployment.yaml:42` for the allow-list.
- *(Added 2026-09-27.)* CI pins `helm` v4.3.0 in the e2e job
  (~~[`release.yml:314-316`](../../.github/workflows/release.yml)~~ *(corrected 2026-09-27 by the
  review: the step is [`release.yml:312-315`](../../.github/workflows/release.yml), the version at
  315)*) and in the release workflow
  ([`build.yml:150-153`](../../.github/workflows/build.yml)). The two required jobs that could
  host a step install no `helm`: `Code Linting` (`release.yml:499-520`) and
  `Generated Manifests Up To Date` (`release.yml:715-756`). `make e2e-local` also installs with
  `test/e2e/helm-values.yaml` ([`Makefile:218`](../../Makefile)).
- *(Added 2026-09-27.)* No Go test renders the chart. `TestHelmClusterRoleCoversGeneratedRole`
  reads `clusterrole.yaml` as plain YAML
  ([`rbac_drift_test.go:35`](../../internal/controller/rbac_drift_test.go)), and `go.mod`
  requires no `helm.sh` module.

**Not verified:**

- Whether a render matrix in CI would have caught any past chart regression; no history of one
  was searched.
- *(Added 2026-09-27.)* The refusal messages and `helm lint` under CI's `helm` v4.3.0; only
  v3.21.3 was run. A script that greps the message text, not the error layout, avoids depending
  on either.
- *(Added 2026-09-27.)* That `Code Linting` and `Generated Manifests Up To Date` are required
  contexts on GitHub today. ADR 0017 D47 lists both, and GitHub was not queried.

## Impact

A pull request that breaks a refusal or the posture on a non-default path is green. On the
default path, a change that drops a control Pod Security `restricted` requires is caught by the
restricted-namespace e2e ([ADR 0017](../adr/0017-test-and-ci-policy.md) D54); what that test
does not check is listed under gap [H-15](../security/operator-pod-posture.md#h-15).

## Options

*(Split 2026-09-27 into two decisions, taken in this order. The tooling comes first because
option C needs no CI placement at all.)*

### Decision 1: the tooling

- **A — a `make` target ~~and a required CI job~~ over the `helm` binary (best).** `helm lint`
  plus `helm template` over a values matrix — the defaults, `image.digest`,
  `podSecurity.userNamespaces`, a `Localhost` profile, a non-empty allow-list — asserting the
  rendered pod posture, and each refusal as an expected failure with its message. ~~The job goes
  into branch protection in the same change (ADR 0017 D47).~~ *(corrected 2026-09-27: where it
  runs is Decision 2. A new job is only one of the placements.)*
- **B — the helm-unittest plugin.** Declarative assertions; one more pinned tool and plugin to
  maintain.
- **C — a Go test that renders through the Helm SDK.** Runs in the unit tier and pulls Helm's
  dependency tree into `go.mod`. *(Added 2026-09-27.)* The concrete cost: the Helm module
  brings its own `k8s.io/*` requirements, and MVS then takes the higher version. The operator's
  Kubernetes module set would no longer be moved by the `k8s-go-modules` group alone, and that
  kind of drift is what broke every package-loading job in
  [ADR 0017](../adr/0017-test-and-ci-policy.md) D48.

A is marked because it uses the `helm` binary CI already installs for the e2e job
(`azure/setup-helm`, `release.yml` ~~line 314~~ *(corrected 2026-09-27: line 313)*), keeps the Makefile the entry point every CI job
uses, and adds no module dependency. **A (recommended).**

### Decision 2: where it runs (only for A or B)

- **(i) a new job, e.g. `Helm Chart`.** Its own context name gives the clearest failure. It costs
  a branch-protection edit by a repository admin in the same change, and the ADR 0017 D47 list
  grows from twelve to thirteen contexts. Nothing in this repository checks that list.
- **(ii) a step in `Code Linting`** ([`release.yml:499-520`](../../.github/workflows/release.yml)),
  after `make lint`, with `azure/setup-helm` pinned as at ~~`release.yml:314-316`~~ `release.yml:312-315`. It is required
  already, so branch protection does not change. Cost: a chart failure shows as `Code Linting`
  red, and only the step name says which check failed. **(recommended)**: a render check is a
  static check of a shipped artifact, like lint, and this placement needs no admin action that
  could be forgotten.
- **(iii) a step in `Generated Manifests Up To Date`** (`release.yml:715-756`). It is also
  required and also about the chart, but that job answers one question, "is the tree dirty
  after regenerating" (its error at lines 749-755). A render assertion there blurs that message.

## Decision

None yet.

## Work list

No item here is both XS and free of the decisions above; every item waits on them.

1. *(waits on Decision 1)* A script under `hack/` plus a Makefile target (for example
   `lint-chart`, separate from `lint` so a local `make lint` needs no `helm`). Positive rows:
   defaults, a valid `image.digest`, `podSecurity.userNamespaces=true`, `Localhost` with a
   relative path, a non-empty allow-list, each asserting `runAsNonRoot`, uid 65532,
   `readOnlyRootFilesystem`, `drop: [ALL]`, the seccomp type, `hostUsers: false` where set and
   `@sha256:` on both images. Negative rows: the six refusals of `_helpers.tpl`, each by its
   message text.
2. *(waits on Decision 2)* The CI step or job. For (i), also the branch-protection edit and
   the D47 list.
3. *(waits on both)* Revert check (Verification).
4. Close ([ADR 0034](../adr/0034-tickets-are-work-lists-that-get-archived.md)): the gate into
   ADR 0017, the ADR 0033 residual-risk chart bullet (lines 623-644) amended, gap
   [H-15](../security/operator-pod-posture.md#h-15) (lines 104-126) rewritten, the target
   into `DEVELOPER.md` and the `CLAUDE.md` Makefile table, `git grep` `058` and `T58`, then move
   to `archive/`.

## Verification

- The new target is green on the current chart and in CI~~, and the job is listed among the
  required contexts of ADR 0017 D47~~. *(corrected 2026-09-27 by the review: that clause assumed
  a new job. Under Decision 2 (i) the new context is listed in ADR 0017 D47 and in branch
  protection; under (ii) or (iii) the step runs inside a job that D47 already lists, and the
  step's name appears in that job's log on the fix commit.)*
- Revert check: deleting one refusal from the chart's templates turns the target red, naming
  that refusal.

## History

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
