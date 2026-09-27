---
id: T58
title: no CI gate renders the chart's non-default paths or checks its refusals
state: filed
severity: low         # the refusals and the posture work today, checked by hand; a regression would merge green
security: hardening
threat: "would additionally cover a template change that silently breaks one of the chart's refusals (a seccomp type other than RuntimeDefault or Localhost, a Localhost path that is absolute or has a '..' element, a malformed image.digest, a bad allow-list entry) or the operator and hook pod posture on a non-default path: today such a change merges green"
urgency: later        # rule 4: cheap known fix
effort: S
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

**Not verified:**

- Whether a render matrix in CI would have caught any past chart regression; no history of one
  was searched.

## Impact

A pull request that breaks a refusal or the posture on a non-default path is green. On the
default path, a change that drops a control Pod Security `restricted` requires is caught by the
restricted-namespace e2e ([ADR 0017](../adr/0017-test-and-ci-policy.md) D54); what that test
does not check is listed under gap [H-15](../security/operator-pod-posture.md#h-15).

## Options

- **A — a `make` target and a required CI job (best).** `helm lint` plus `helm template` over a
  values matrix — the defaults, `image.digest`, `podSecurity.userNamespaces`, a `Localhost`
  profile, a non-empty allow-list — asserting the rendered pod posture, and each refusal as an
  expected failure with its message. The job goes into branch protection in the same change
  (ADR 0017 D47).
- **B — the helm-unittest plugin.** Declarative assertions; one more pinned tool and plugin to
  maintain.
- **C — a Go test that renders through the Helm SDK.** Runs in the unit tier and pulls Helm's
  dependency tree into `go.mod`.

A is marked because it uses the `helm` binary CI already installs for the e2e job
(`azure/setup-helm`, `release.yml` line 314), keeps the Makefile the entry point every CI job
uses, and adds no module dependency.

## Decision

None yet.

## Verification

- The new target is green on the current chart and in CI, and the job is listed among the
  required contexts of ADR 0017 D47.
- Revert check: deleting one refusal from the chart's templates turns the target red, naming
  that refusal.

## History

- 2026-09-27 — filed from the row "Chart render test in CI" of archive/031, which names
  ADR 0017. Gap [H-15](../security/operator-pod-posture.md#h-15) states what is missing.
