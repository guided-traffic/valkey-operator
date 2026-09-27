---
id: T72
title: the chart's localhostProfile path check passes a value with a leading blank
state: analysed       # facts, render and recommended clause measured; decision open
severity: low         # a typo that looks absolute or '..' renders instead of failing, and fails at container start
security: hardening   # only the chart installer sets the value, and it already writes the whole operator and hook pod spec
threat: "would additionally cover an installer-supplied podSecurity.seccompProfile.localhostProfile for the operator Deployment and the pre-upgrade hook Job whose leading whitespace hides a leading '/' or a '..' element from the chart's render refusal: no principal gains anything, and by the Kubernetes v1.36.1 source the value resolves below the kubelet seccomp root, so the clause only removes the dependency on unmeasured API server, kubelet and runtime behaviour"
urgency: later        # rule 4: cheap known fix, no tracked statement is false
effort: XS            # one clause and its message at one fail site, one render-check row, one sentence where the rule is stated
blocked-by: decision  # Q1
filed-from: T58
opened: 2026-09-27
decided:
done:
---

# T72 - The chart's localhostProfile path check passes a value with a leading blank

## Current state

The operator Deployment and the pre-upgrade hook Job take their pod posture from
`valkey-operator.podHardening`
([`_helpers.tpl:86-116`](../../deploy/helm/valkey-operator/templates/_helpers.tpl)), included at
[`deployment.yaml:30`](../../deploy/helm/valkey-operator/templates/deployment.yaml) and
[`pre-upgrade-job.yaml:27`](../../deploy/helm/valkey-operator/templates/pre-upgrade-job.yaml).
With `podSecurity.seccompProfile.type: Localhost` it refuses a missing path (:105-106), then tests
the **raw** value ([`_helpers.tpl:108-109`](../../deploy/helm/valkey-operator/templates/_helpers.tpl)):

```
{{- if or (hasPrefix "/" $sp.localhostProfile) (regexMatch "(^|/)[.][.](/|$)" $sp.localhostProfile) }}
```

and writes the value unchanged and quoted into both pod specs (:112). Nothing trims, so a value
starting with whitespace passes both tests. `helm template` (helm v3.21.3) with
`type: Localhost` renders these with exit 0 into the Deployment and the Job: `" /abs.json"`,
`" ../x.json"`, `"\t/abs.json"`, `"\n/abs.json"`, `" /abs.json"`, `" ../x.json"`,
`"​/abs.json"`, `"﻿/abs.json"`, `"profiles/ok.json "`. `"/abs.json"` fails with exit 1
("must be a relative path without '..'"). The rendered manifest shows the invisible character
escaped (`quote` is `%q`); the values file does not.

Upstream behaviour, read in the Kubernetes v1.36.1 source, not measured:

- The API server validates a `Localhost` path with `path.IsAbs` and a `..`-element check, without
  trimming, so it accepts exactly what the chart accepts. The chart therefore implements the rule
  [ADR 0033](../adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md)
  D6 states; the gap is between that rule and what a reader of the values file expects.
- Kubelet computes `filepath.Join("/var/lib/kubelet/seccomp", localhostProfile)`, so
  `" /abs.json"` becomes `/var/lib/kubelet/seccomp/ /abs.json`. Nothing leaves the seccomp root.

The values comment ([`values.yaml:25-28`](../../deploy/helm/valkey-operator/values.yaml)) and
[`operator-pod-posture.md:20`](../security/operator-pod-posture.md) state the rule as "not
absolute and without a '..' element" / "starts with `/` or has a `..` element"; both are literally
true. ADR 0033's residual risks (lines 633-637) record the blank pass-through only for the
allow-list.

Out of scope, because it fails closed: the allow-list helper (`_helpers.tpl:136-143`) passes
blanks too, but `profileList` ([`main.go:91-99`](../../cmd/main.go)) trims every entry; the CRD
rule ([`valkey_types.go:542`](../../api/v1/valkey_types.go)) passes a leading blank, but
`seccompProfileAllowed`
([`pod_hardening.go:27-30`](../../internal/controller/pod_hardening.go)) requires an exact match
with a trimmed entry, so no Valkey workload is written with it. The operator's own profile is
the only place such a value reaches a pod spec.

**Impact.** Only an installer who types whitespace into the operator's own `localhostProfile`
(default `""` under `RuntimeDefault`) is affected:

- Install: the render succeeds, the operator pod does not start (profile file missing), like any
  misspelled path, but the value looks like a shape the chart promises to refuse.
- Upgrade with the hook: the hook Job never succeeds, `helm upgrade` fails (expected on its
  `--timeout`), the Deployment keeps its old spec.
- Upgrade with `preUpgradeHook.enabled: false`: the new operator pod does not start, the old one
  keeps serving, and `helm upgrade` without `--wait` reports success.
- No weakening of the seccomp posture: `Unconfined` stays unrenderable (`_helpers.tpl:113-114`),
  and the Valkey data, Sentinel and observer pods are not affected.

## Required changes

### Independent of the open questions

- When T58's render check is built, it carries a row for `localhostProfile: " /abs.json"` on the
  operator's own profile: accepted-shape row under C, negative row under A or B (with the
  `" ../x.json"`, tab, U+00A0 and trailing-blank values beside it).
- Optional: a row in `TestSeccompProfileAllowed`
  ([`pod_hardening_test.go:138`](../../internal/controller/pod_hardening_test.go)) with CR value
  `" profiles/a.json"` and allow-list `profiles/a.json`, expected refused; it fails if
  `seccompProfileAllowed` ever trims the CR value.

### Depends on the answers

- Under A: add `(ne $sp.localhostProfile (trim $sp.localhostProfile))` to the condition at
  [`_helpers.tpl:108`](../../deploy/helm/valkey-operator/templates/_helpers.tpl) and extend the
  message at :109 with "and without surrounding whitespace". Under B additionally
  `(regexMatch "\\p{C}" $sp.localhostProfile)`.
- State the rule (extra clause under A/B, accepted pass-through under C) in:
  [`values.yaml:25-28`](../../deploy/helm/valkey-operator/values.yaml); ADR 0033 D6 (line 278ff),
  its D9 scope bullet (lines 385-389) and its residual-risk paragraph (lines 633-637);
  [`operator-pod-posture.md:20`](../security/operator-pod-posture.md), :46 and :113-116;
  [`pod-security.md:165-167`](../operations/pod-security.md);
  [`README.md:560`](../../README.md);
  [ADR 0013](../adr/0013-operator-is-cluster-wide-privileged.md) lines 243-247; the CLAUDE.md
  pod-hardening bullet under A/B.
- Tests under A: `helm template` fails with the new message for `" /abs.json"`, `" ../x.json"`,
  `"\t/abs.json"`, `" /abs.json"` and `"profiles/ok.json "`; `"profiles/ok.json"`,
  `"profiles/..v..json"`, `"a /b.json"` and the defaults render; `"​/abs.json"` renders
  (documented limit; refused under B). `"/abs.json"` and `"profiles/../x.json"` still fail.
  Mutation check: removing the `trim` clause turns the `" /abs.json"` row red.
- Check under A/B:
  `git grep -n -e 'relative path without' -e 'relative, no' -e 'starts with' -e 'absolute path or a' -- README.md CLAUDE.md docs deploy ':!docs/tickets'`
  lists no statement of the operator's path rule without the whitespace clause.

## Open questions

### Q1: Should the chart refuse a `localhostProfile` with surrounding whitespace for the operator's own pods?

Today the chart mirrors the API server rule exactly, so `" /abs.json"` renders and the operator
pod fails at container start. Refusing it moves the failure to render time and makes the chart
stricter than the CRD rule and the API server by one clause, which ADR 0033 D6 has to state.

- **A - refuse surrounding whitespace (recommended):** one `trim` clause; refuses blanks, tabs,
  newlines and U+00A0 on either side, measured on a scratch copy; a blank inside the path still
  renders; U+200B and U+FEFF (format characters) still render.
- **B - A plus refuse every control and format character (`\p{C}`):** also refuses U+200B and
  U+FEFF (measured); same XS cost, but one more rule for D6 and the values comment to explain,
  for copy-paste artefacts nobody has met.
- **C - keep the check, document the pass-through as accepted:** text only; the chart stays equal
  to the CEL and API server rule, and safety rests on upstream behaviour that was only read, at
  one Kubernetes version, with the runtime side not read at all.

A refuses nothing any node is expected to hold, removes the dependency on unmeasured upstream
behaviour, and the `%q`-quoted refusal message shows the stray blank at render instead of a
container-start error on a node.

**Answer:** _open_

## Not verified

- API server admission and kubelet/runtime behaviour for `" /abs.json"`: read in the v1.36.1
  source only; the runtime (containerd or CRI-O opening the path) was not read. One Kind install
  with such a value settles it.
- That the hook Job fails `helm upgrade` on its `--timeout` (pod waits, `backoffLimit: 3` never
  counts) and that a hook-disabled upgrade leaves the old pod serving: inferred, not measured.
- That no node holds a profile file whose path starts or ends with whitespace: an assumption
  behind "A refuses nothing that works".

## Related

- T58 - the chart render check that carries the row for this value.
