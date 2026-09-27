---
id: T72
title: the chart's localhostProfile path check passes a value with a leading blank
state: analysed       # every fact re-checked at 84a39c2, the render measured with helm v3.21.3, the upstream path handling read at Kubernetes v1.36.1, the options costed and the recommended clause rendered on a scratch copy (History 2026-09-27)
severity: low         # an installer typo that looks like an absolute or '..' path renders instead of failing; by the v1.36.1 source it resolves below the kubelet seccomp root and names a file no node holds, so it fails at container start, not at render (read, not measured; Impact)
security: hardening   # not in doubt between hardening and boundary: the only principal who sets the value is the chart installer, who already writes the whole operator and hook pod spec; no trust boundary is crossed
threat: "would additionally cover an installer-supplied podSecurity.seccompProfile.localhostProfile for the operator Deployment and the pre-upgrade hook Job whose leading whitespace hides a leading '/' or a '..' element from the chart's render refusal (for example ' /abs.json'): no principal gains anything, the installer already controls that pod spec, and by the Kubernetes v1.36.1 source such a value is a relative path that resolves below the kubelet seccomp root, so the clause would make the render refusal independent of API server, kubelet and runtime behaviour that is read here, not measured"
urgency: later        # rule 4: cheap known fix (derived top-down 2026-09-27: rule 1 does not match, the check shipped in v1.13.0 with b13377e and no tracked statement is false, see Fact; rule 2 no release gating; rule 3 severity low)
effort: XS            # one clause and its message at one fail site, one negative row in T58's render check, and one sentence at each place that states the rule (values comment, README values row, ADR 0033 D6/D9/residual risks, ADR 0013, the operations and security pages, CLAUDE.md; Work list item 2)
blocked-by: decision  # one decision: refuse surrounding whitespace, or record the shape as accepted (Options)
filed-from: T58, section "Found while verifying, owned elsewhere" and its Not verified item on localhostProfile " /abs.json", during the re-verification of 2026-09-27 at 84a39c2
opened: 2026-09-27
decided:              # not recorded - no decision yet
done:                 # not recorded - not done
---

# The chart's localhostProfile path check passes a value with a leading blank

Filed on 2026-09-27 from
[ticket 058](058-no-ci-gate-renders-the-chart.md) (T58), which recorded the finding under
"Found while verifying, owned elsewhere" and carried what the API server and kubelet do with the
value as Not verified. This ticket is now the record of both; T58 keeps its accepted-shape row
for the value (its Work list item 1). ~~*(Checked 2026-09-27: T58 still carries the finding under
that heading and does not yet point here; turning that bullet into a pointer is an edit of T58.)*~~
*(Sweep 2026-09-27: T58's host update turned that bullet into a pointer here, and its Fact, Not
verified and Work list item 1 now name this ticket too.)*

## Fact

**Mechanism.** The operator Deployment and the pre-upgrade hook Job take their pod-level
posture from `valkey-operator.podHardening`
([`_helpers.tpl:86-116`](../../deploy/helm/valkey-operator/templates/_helpers.tpl)), included at
[`deployment.yaml:30`](../../deploy/helm/valkey-operator/templates/deployment.yaml) and
[`pre-upgrade-job.yaml:27`](../../deploy/helm/valkey-operator/templates/pre-upgrade-job.yaml).
With `podSecurity.seccompProfile.type: Localhost` the helper refuses a missing path (:105-106)
and then a path that is absolute or carries a `..` element, with one condition on the **raw**
value
([`_helpers.tpl:108-109`](../../deploy/helm/valkey-operator/templates/_helpers.tpl)):

```
{{- if or (hasPrefix "/" $sp.localhostProfile) (regexMatch "(^|/)[.][.](/|$)" $sp.localhostProfile) }}
```

and writes the value unchanged into the pod spec, quoted (:112). Neither test trims. A value that
starts with whitespace therefore passes both: `hasPrefix "/"` sees the blank, not the slash, and
the element regex needs `..` to start the string or follow a `/`, so ` ../x.json` has the
element ` ..`, which is not `..`.

**Verified** (at `84a39c2`, 2026-09-27):

- Rendered with the local `helm` v3.21.3+g1ad6e68, one values file per value, holding
  `podSecurity.seccompProfile.type: Localhost` and the `localhostProfile` below, command
  `helm template x deploy/helm/valkey-operator -f v.yaml`:

  | `localhostProfile` | exit | rendered into the Deployment (output line 1199) and the Job (1377) |
  |---|---|---|
  | `" /abs.json"` | 0 | `localhostProfile: " /abs.json"` |
  | `" ../x.json"` | 0 | `localhostProfile: " ../x.json"` |
  | `"\t/abs.json"` (a tab) | 0 | `localhostProfile: "\t/abs.json"` |
  | `"profiles/ok.json "` (trailing blank) | 0 | `localhostProfile: "profiles/ok.json "` |
  | `"/abs.json"` | 1 | `execution error at (valkey-operator/templates/pre-upgrade-job.yaml:27:10): podSecurity.seccompProfile.localhostProfile "/abs.json" must be a relative path without '..'` |

  The first row reproduces the measurement recorded in T58 (output lines 1199 and 1377); the
  `..`, tab and trailing-blank rows are new. The check has been in the chart since `b13377e`
  (2026-09-26, `git log -S` on the condition), which `v1.13.0` and `v1.13.1` contain
  (`git tag --contains b13377e`; the condition read at both tags, `_helpers.tpl:108`).
- *(Added by the review of 2026-09-27, same helm, same form of values file, written with
  YAML `\u`/`\n` escapes.)* Other invisible leading characters pass as well, each exit 0 and
  rendered escaped at output lines 1199 and 1377, because `quote` is `fmt.Sprintf("%q", ...)`
  (sprig v3.3.0 `strings.go:87`, read in the local module cache; the sprig version inside the
  helm binary was not checked): a newline (`"\n/abs.json"`), a no-break space U+00A0
  (`"\u00a0/abs.json"`, also `"\u00a0../x.json"`), a zero-width space U+200B
  (`"\u200b/abs.json"`) and a byte order mark U+FEFF (`"\ufeff/abs.json"`). The rendered
  manifest shows each one; a values file does not.
- **The Kubernetes definition of "absolute" is the same prefix test, so the chart and the API
  server agree on these values.** Read in the Kubernetes source at tag `v1.36.1`
  (`https://raw.githubusercontent.com/kubernetes/kubernetes/v1.36.1/pkg/apis/core/validation/validation.go`):
  `validateSeccompProfileField` (line 5249) validates a `Localhost` path with
  `validateLocalDescendingPath` (called at line 5263), which refuses `path.IsAbs(targetPath)`
  (lines 1398-1407) and, in `validatePathNoBacksteps` (lines 1413-1423), an element equal to
  `..` after `strings.Split(filepath.ToSlash(targetPath), "/")`. Neither trims. Evaluated with
  those exact Go calls in a scratch program (go1.26.5): `path.IsAbs(" /abs.json")`,
  `path.IsAbs(" ../x.json")` and `path.IsAbs("\t/abs.json")` are false and none has a `..`
  element; `path.IsAbs("/abs.json")` is true (re-run by the review, which added
  `"\u200b/abs.json"` and `"\u00a0../x.json"`: neither absolute, neither with a `..` element).
  So each value in the table that the chart passes
  is one this API server validation also passes, and the one it refuses is one the API server
  refuses. [ADR 0033](../adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md)
  D6 defines the chart check as "the two shapes D1's second CEL rule refuses on the CR and the
  API server refuses on every pod write"; the chart implements exactly that rule, and the
  finding is the gap between the rule and what a reader of the values sees.
- **Kubelet joins the value below its seccomp root.** Same tag,
  `pkg/kubelet/kuberuntime/helpers.go`, `fieldSeccompProfile` (lines 271-300): for `Localhost`
  it computes `fname := filepath.Join(profileRootPath, *scmp.LocalhostProfile)` (line 289) and
  returns it as `LocalhostRef` (line 292); the root is `filepath.Join(rootDirectory, "seccomp")`
  (`pkg/kubelet/kuberuntime/kuberuntime_manager.go` line 266, same tag), which is
  `/var/lib/kubelet/seccomp` under the kubelet's default root directory.
  Evaluated with that call and the default root: `" /abs.json"` becomes
  `/var/lib/kubelet/seccomp/ /abs.json` (a directory named by one blank), `" ../x.json"` becomes
  `/var/lib/kubelet/seccomp/ ../x.json`. Neither leaves the root; `filepath.Join` would keep even
  the refused `/abs.json` below it (`/var/lib/kubelet/seccomp/abs.json`).
- The values comment states the rule as "not absolute and without a '..' element, or the render
  fails" ([`values.yaml:25-28`](../../deploy/helm/valkey-operator/values.yaml)), the row of the
  pod and container field table
  [`operator-pod-posture.md:20`](../security/operator-pod-posture.md) as "a `localhostProfile`
  that starts with `/` or has a `..` element". Both are literally true of the values above, so no
  tracked statement is false. ADR 0033's residual risks
  ([`0033:633-637`](../adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md))
  record the blank pass-through only for `valkeyPodSecurity.allowedSeccompLocalhostProfiles`,
  not for the operator's own profile.
- **The same shape elsewhere fails closed, and is not this ticket's work.** The allow-list
  helper (`_helpers.tpl:136-143`) passes `" "` and `" /abs.json"` the same way (measured in T58:
  `--allowed-seccomp-localhost-profiles= , /abs.json`, exit 0), and `profileList`
  ([`main.go:91-99`](../../cmd/main.go)) trims every entry. The CRD's path rule on
  `spec.podSecurity.seccompProfile.localhostProfile`
  ([`valkey_types.go:542`](../../api/v1/valkey_types.go), `startsWith('/')` and the same element
  regex) passes a leading blank as well (read from the rule, not evaluated by an API server),
  but `seccompProfileAllowed`
  ([`pod_hardening.go:27-30`](../../internal/controller/pod_hardening.go)) requires the CR's value
  to equal a trimmed allow-list entry exactly (`slices.Contains`), so a CR value with a leading
  blank is never allowed and no Valkey workload is written with it. Read, not unit-tested:
  `TestSeccompProfileAllowed` has no row with a blank, and `TestProfileList` covers only blank
  entries. The operator's own profile is the only place where such a value reaches a pod spec,
  because the allow-list does not apply to it (ADR 0033, line 384).

**Not verified, and this is the gap:**

- That a real API server admits the Deployment and the Job with `localhostProfile: " /abs.json"`,
  and what a real kubelet and container runtime do with the joined path: read in the v1.36.1
  source above, not measured. No cluster is available to this re-verification (no Kind, no
  kubectl), and the owner's clusters may run another Kubernetes version. The runtime side
  (containerd or CRI-O opening `LocalhostRef`) was not read at all; the expected outcome, a
  container that is not created because the profile file does not exist, is an assumption.
- That a failing hook Job fails `helm upgrade` and leaves the Deployment on its old spec: read
  from the hook semantics (`helm.sh/hook: pre-upgrade`,
  [`pre-upgrade-job.yaml:11`](../../deploy/helm/valkey-operator/templates/pre-upgrade-job.yaml)),
  not measured. *(Added by the review of 2026-09-27, inferred, not read at a tag.)* A container
  that cannot be created leaves its pod waiting rather than `Failed`, so the Job's
  `backoffLimit: 3` (:15) is expected never to count, and `helm upgrade` is expected to fail on
  its `--timeout` (Helm default 5m0s), not on the Job.
- *(Added by the review of 2026-09-27.)* What an upgrade with `preUpgradeHook.enabled: false`
  does: no hook runs, so the Deployment is updated directly. The template sets no `strategy`
  (read in [`deployment.yaml`](../../deploy/helm/valkey-operator/templates/deployment.yaml)) and
  `replicaCount` defaults to 1, so by the documented `RollingUpdate` defaults (25 % surge rounded
  up, 25 % unavailable rounded down) the old operator pod is expected to keep running while the
  new one never starts; `helm upgrade` without `--wait` then reports success. Not measured.
- Whether a Pod Security `restricted` namespace changes anything: not measured; by its rule any
  `Localhost` profile is accepted, so it is not expected to.

## Impact

Nobody hits this unless the installer types whitespace into the operator's own
`podSecurity.seccompProfile.localhostProfile`, which defaults to `""` under `RuntimeDefault`
([`values.yaml:20-29`](../../deploy/helm/valkey-operator/values.yaml)).

- **Install, value with surrounding whitespace.** The render succeeds; the Deployment is
  created with a profile path that, by the v1.36.1 reading, names a file below a blank-named
  directory in the seccomp root that no node holds. The operator pod does not start. This is the
  same outcome as any misspelled profile path (the values comment already says "A node missing
  the file cannot start the operator pod"); the difference is only that this misspelling looks
  like one of the two shapes the chart promises to refuse, so a reviewer of the values file
  trusts a refusal that does not fire.
- **Upgrade, same value.** The pre-upgrade hook Job carries the same profile, so, read and not
  measured, the hook never succeeds, `helm upgrade` fails (by inference on its `--timeout`) and
  the Deployment keeps its previous spec. Loud, not silent.
- **Upgrade with `preUpgradeHook.enabled: false`, same value.** Read, not measured: the
  Deployment is updated, its new pod does not start, the old pod keeps serving, and `helm
  upgrade` without `--wait` reports success. The operator stays on the old release while the
  release record names the new one, visible only in the Deployment's rollout status. Quieter
  than the hook case, still no posture change.
- **No weakening of the seccomp posture, by reading.** No value in the Fact table resolves
  outside the kubelet seccomp root, and none selects `Unconfined`; `Unconfined` stays
  unrenderable (`_helpers.tpl:113-114`). The Valkey data, Sentinel and observer pods are not
  affected: their profile comes from the CR and passes the exact-match allow-list gate.

**Threat line, expanded.** Principal: the chart installer, who sets Helm values. Verb: sets
`podSecurity.seccompProfile.localhostProfile` to a value with leading whitespace. Object: the
seccomp profile of the operator Deployment and the pre-upgrade hook Job. Live or dormant: the
pass-through is live at `84a39c2` and in `v1.13.0`/`v1.13.1`, but it gives nobody anything: the
installer can already write any pod spec for these two objects, so there is no trust boundary to
cross, and the value does not escape the seccomp root by the v1.36.1 source. What a fix would
additionally cover is the unmeasured part, API server, kubelet and runtime behaviour on such a
path, by never emitting it.

## Options

**What the code does today and what the choice changes.** The chart refuses at render the two
path shapes the API server refuses on every pod write, tested on the raw string, and writes the
raw string into both pod specs. A value with surrounding whitespace passes, as it passes the API
server's own validation by the v1.36.1 reading, and fails later, at container start or at the
hook. The decision is whether the chart refuses surrounding whitespace in the operator's own
`localhostProfile`. It does not change the allow-list (its entries are trimmed by `profileList`
and fail closed, and refusing `" a.json"` there would break a `helm upgrade` of an install whose
entry works today), the CRD rule (closed by the exact-match gate), or anything about the Valkey
pods.

**A — Refuse surrounding whitespace at the existing fail site (recommended).** Add
`(ne $sp.localhostProfile (trim $sp.localhostProfile))` to the condition at `_helpers.tpl:108`
and extend the message at :109 to name it, for example "must be a relative path without '..'
and without surrounding whitespace". Sprig `trim` is `strings.TrimSpace` (sprig v3.3.0
`functions.go:121`, read in the local module cache), so Unicode white space on either side is
refused: blanks, tabs, newlines and U+00A0; a blank inside the path (`a /b.json`) still renders.
Measured on a scratch copy of the chart at `84a39c2` with the clause added and the message
unchanged: `" /abs.json"`, `" ../x.json"`, `"\t/abs.json"`, `"\n/abs.json"`,
`"\u00a0/abs.json"`, `"\u00a0../x.json"`, `"profiles/ok.json "`, `"/abs.json"` and
`"profiles/../x.json"` exit 1 at `pre-upgrade-job.yaml:27:10` with the `%q`-quoted value, which
shows the blank; `"profiles/ok.json"`, `"profiles/..v..json"` and `"a /b.json"` render; the
defaults render. **Its limit, measured on the same copy:** `"\u200b/abs.json"` and
`"\ufeff/abs.json"` still render, exit 0, because a zero-width space and a byte order mark are
format characters, not white space. Cost: one clause, one message, and the text that states the
rule (Work list item 2: the values comment, the README values row, ADR 0033 D6, its D9 scope
bullet and its residual-risk paragraph, ADR 0013, the operations page and the security page; the
chart becomes stricter than the CEL rule and the API server by exactly this clause, which D6 has
to say), and one negative row in T58's check. Consequence: an install that sets such a value
fails at render instead of at container start; nothing that can work today is refused, because
a profile file whose path starts or ends with whitespace is not something any node is expected to
hold (an assumption, not a measurement).

**C — Keep the check, record the shape as accepted.** State in the values comment, in ADR 0033
D6 and in the security page that the check mirrors the API server's prefix rule and therefore
passes a value with leading whitespace, which resolves below the seccomp root; T58's check pins
`" /abs.json"` as an accepted-shape row (it already plans that row). Cost: text only.
Consequence: the chart stays equal to the CEL rule and the API server rule, and the safety of
the pass-through rests entirely on the upstream behaviour this ticket could only read, at one
Kubernetes version, with the runtime side not read at all.

Considered and not kept: **trimming the value in the helper** (check and render
`trim $sp.localhostProfile`, as `profileList` does for the allow-list). It silently rewrites a
security field, so the rendered pod spec differs from the values the installer reviewed, and it
makes `" x.json"` and `"x.json"` one profile in the chart while the CRD, the API server and
kubelet treat them as two paths. A refuses the same values without either cost.

Considered and not kept *(added by the review of 2026-09-27)*: **A plus a refusal of every
control and format character**, `(regexMatch "\\p{C}" $sp.localhostProfile)` as one more clause.
Measured on a scratch copy with both clauses: `"\u200b/abs.json"` and `"\ufeff/abs.json"` exit 1,
`"profiles/ok.json"`, `"profiles/..v..json"` and `"a /b.json"` still render. It closes A's
measured limit at the same XS cost, and it is the runner-up to A rather than to C. It is not
kept because what it adds is a copy-paste artifact, not a typing slip: nobody types a zero-width
space, the harm is the same container-start failure as any misspelled path, and the rendered
manifest already shows such a character escaped (Fact); a Unicode-category rule is one more
thing D6 and the values comment have to explain for a case nobody has met. If the owner prefers
the render refusal to be complete for invisible characters, this is the variant to take, and the
decision stays one decision.

Considered and not kept: **a character allow-list pattern** (refuse anything outside, for
example, `^[A-Za-z0-9._/-]+$`). It closes every invisible character, but it refuses paths that
work today and that the API server accepts (a `+`, a `:` or a blank inside a file name), so a
`helm upgrade` of a working install could fail at render; A and its variant refuse nothing that
any node is expected to hold.

**Why A beats C.** Both cost XS. A removes the dependency on unmeasured behaviour for the one
value source where such a string reaches a pod spec, at the cost of one clause that refuses
nothing known to work, and the refusal message quotes the value with `%q`, so the stray blank
is visible where C leaves a container-start error on a node. C's only advantage, keeping the
chart textually equal to the CEL rule, is worth less than that, because D6 can state the one
extra clause in a sentence and a stricter render check fails in the safe direction.

## Decision

Not decided.

## Work list

0. *(decision-free)* When T58's render check is built, it pins the current behaviour of
   `localhostProfile: " /abs.json"` for the operator's own profile (T58 Work list item 1 already
   plans it as an accepted-shape row); under A that row becomes a negative row, the eleventh
   refused shape, and the `" ../x.json"`, tab, U+00A0 and trailing-blank values from the Fact table
   become rows beside it.
1. *(waits on the decision; under A)* Extend the condition at
   [`_helpers.tpl:108`](../../deploy/helm/valkey-operator/templates/_helpers.tpl) with
   `(ne $sp.localhostProfile (trim $sp.localhostProfile))` and the message at :109 with "and
   without surrounding whitespace".
2. *(waits on the decision)* Same change: the comment at
   [`values.yaml:25-28`](../../deploy/helm/valkey-operator/values.yaml); ADR 0033 D6 (line 278ff:
   the chart refuses the two shapes the CEL rule and the API server refuse, plus surrounding
   whitespace under A, or passes it as the API server does under C) and its residual-risk
   paragraph at lines 633-637, which today records the blank only for the allow-list, with the
   date and in place; its D9 scope bullet (lines 385-389, "the D1 path rule, the same regular
   expression"); [`operator-pod-posture.md:20`](../security/operator-pod-posture.md), :46 and
   :113-116; [`pod-security.md:165-167`](../operations/pod-security.md);
   [`README.md:560`](../../README.md) (the values row "relative, no `..`");
   [ADR 0013](../adr/0013-operator-is-cluster-wide-privileged.md) lines 243-247. CLAUDE.md's
   pod-hardening bullet ("on an absolute path or a `..` element") gains the clause under A, in
   the same change. *(Sites added by the review of 2026-09-27 from `git grep` at 84a39c2; none
   of them is false today, each states the refused shapes and would omit the new one.)*
3. *(decision-free, optional)* A row in `TestSeccompProfileAllowed`
   ([`pod_hardening_test.go:138`](../../internal/controller/pod_hardening_test.go)) with the CR
   value `" profiles/a.json"` and the allow-list `profiles/a.json`, refused, pins the exact-match
   property that closes the CRD side today (read, not tested, Fact).

## Verification

- Under A: `helm template` with one values file per value, `" /abs.json"`, `" ../x.json"`,
  `"\t/abs.json"`, `"\u00a0/abs.json"` and `"profiles/ok.json "`, each exit 1 with the new
  message; `"profiles/ok.json"`, `"profiles/..v..json"` and `"a /b.json"` render into the
  Deployment and the Job; the defaults render; `"\u200b/abs.json"` renders, pinning the
  documented limit (under the `\p{C}` variant it is refused instead). In T58's check once it
  exists, by hand and recorded here until then.
- Under A: `git grep -n -e 'relative path without' -e 'relative, no' -e 'starts with' -e 'absolute path or a' -- README.md CLAUDE.md docs deploy ':!docs/tickets'`
  lists no statement of the operator's own path rule that lacks the whitespace clause.
- Mutation check (ADR 0017): delete the `trim` clause again and the `" /abs.json"` row must turn
  red (it renders, exit 0); restore it and the row is green. Revert check: `"/abs.json"` and
  `"profiles/../x.json"` still fail with the extended message, so the new clause did not replace
  the old ones.
- Under C: the accepted-shape row for `" /abs.json"` in T58's check, and a `git grep` for the
  new sentence in `values.yaml`, ADR 0033 and `operator-pod-posture.md`.
- Either way: the Not verified items stay open unless one install on Kind with such a value is
  run; recording its outcome (API server admission, the kubelet or runtime error) closes them.
- Item 3: the new row fails if `seccompProfileAllowed` is changed to trim the CR value.

## History

- 2026-09-27: filed from T58 ("Found while verifying, owned elsewhere", and its Not verified item
  on `localhostProfile: " /abs.json"`) during the re-verification at 84a39c2. **Moved:** the
  measurement that `" /abs.json"` renders with exit 0 into the Deployment and the Job (output
  lines 1199 and 1377) and the observation that ADR 0033 lines 633-637 record the blank
  pass-through only for the allow-list, where `profileList` trims it. **Re-verified now:**
  `_helpers.tpl:86-116` and :108-109, the includes at `deployment.yaml:30` and
  `pre-upgrade-job.yaml:27`, `values.yaml:20-29`, `main.go:91-99`, `valkey_types.go:542`,
  `pod_hardening.go:27-30`, the tags containing `b13377e`; the render re-run with helm v3.21.3.
  **Measured now:** the `" ../x.json"`, tab and trailing-blank rows (all exit 0), `"/abs.json"`
  (exit 1); the Go path semantics of the upstream checks in a scratch program; the recommended
  clause on a scratch copy of the chart (the four whitespace values and `/abs.json` refused, three
  valid values and the defaults render). **Read now:** Kubernetes v1.36.1
  `pkg/apis/core/validation/validation.go` (lines 1398-1423, 5249-5270) and
  `pkg/kubelet/kuberuntime/helpers.go` (lines 271-300). **Corrected:** the finding as handed over
  said the "absolute path" refusal "can be bypassed at render". The render does pass the value,
  but `" /abs.json"` is not an absolute path by the definition the API server applies
  (`path.IsAbs`) and kubelet joins it below its seccomp root, both read at v1.36.1 and not
  measured; so the chart implements the rule ADR 0033 D6 states, and the finding is a value that
  looks absolute to a reader, not a path that escapes. The `..` check has the same gap
  (`" ../x.json"`), which the hand-over did not name. Security class `hardening`, not in doubt
  toward `boundary`, because the installer already controls the pod spec; urgency `later` by
  rule 4. **Reviewed the same day** (adversarial review at 84a39c2): re-read every cited line of
  the chart, `values.yaml`, `main.go`, `valkey_types.go`, `pod_hardening.go`, the tests and the
  ADR and security-page lines; re-fetched Kubernetes v1.36.1 `validation.go` (1398-1423, 5249,
  5263) and kubelet `helpers.go` (271-292) and `kuberuntime_manager.go` (266); re-ran the five
  table renders (same output lines) and the Go path program; option A re-applied on a scratch
  copy. **Added:** the renders of a newline, U+00A0, U+200B and U+FEFF (all exit 0 today); A's
  measured limit (U+200B and U+FEFF still render under A, U+00A0 and the newline are refused);
  the `\p{C}` variant (measured, considered and not kept) and the character allow-list pattern
  (not kept); the hook-disabled upgrade case and the hook-timeout inference in Impact and Not
  verified; the ADR 0033 D9 scope bullet, ADR 0013, `operator-pod-posture.md:113-116`,
  `pod-security.md:165-167` and `README.md:560` to Work list item 2, and a `git grep` step.
  **Corrected:** kubelet line 289 computes the joined path and line 292 returns it; the table at
  `operator-pod-posture.md:20` is the field table, not a gap table; T58 does not yet point here.
  Frontmatter re-derived unchanged: `hardening` (the installer already writes both pod specs),
  severity `low`, urgency `later` by rule 4 (rule 1: released in v1.13.0 and no tracked statement
  measured false), effort `XS`.
  Sweep: T58 now points here (its "Found while verifying" pointer, Fact, Not verified, Work list
  item 1 and Related tickets), so the opening note that T58 did not yet point here is struck.
  Frontmatter unchanged.
  Final pass: re-checked T58's current text - its "Found while verifying, owned elsewhere" bullet,
  its struck Not verified item on `" /abs.json"`, its Related tickets and its History all point
  here - so the struck note in the opening paragraph and the sweep's replacement stand as the
  correct statement, and no further text changed; the review's "T58 does not yet point here"
  above is superseded by that sweep sentence. Frontmatter unchanged.
