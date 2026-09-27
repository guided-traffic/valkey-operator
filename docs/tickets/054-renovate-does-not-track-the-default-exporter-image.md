---
id: T54
title: Renovate does not track DefaultMetricsExporterImage
state: analysed       # facts and options verified; two decisions open
severity: low         # the pin ages; no vulnerability verified as reachable
security: hardening
threat: "would additionally cover vulnerabilities fixed in redis_exporter and its Go toolchain after v1.66.0 (arm64 image built with the unsupported go1.23.2): today the default exporter, third-party code holding the cluster password on every auth-enabled cluster with metrics on and the private key of the Valkey server certificate on every TLS cluster with metrics on, ages until someone moves the pin by hand"
urgency: later        # rule 4: a cheap known fix once the option is chosen
effort: S             # one manager, one packageRule, one e2e subtest and ADR text
blocked-by: decision  # Q1 (review or age-gated automerge, shared rule with T45) and Q2 (how the copies follow)
filed-from: T31, section "Further security measures — not in this change, each open" (archive/031)
opened: 2026-09-27
decided:
done:
---

# T54 - Renovate does not track DefaultMetricsExporterImage

The operator-facing statement is gap [H-17](../security/workload-pod-posture.md#h-17).

## Current state

- `DefaultMetricsExporterImage` is the literal `oliver006/redis_exporter:v1.66.0@sha256:d98e6db8…`
  in [`api/v1/valkey_types.go:645`](../../api/v1/valkey_types.go) (doc comment `:640-644`,
  [ADR 0033](../adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md) D5).
  `MetricsImage()` (`valkey_types.go:1211-1217`) falls back to it when `spec.metrics.image` is empty.
- The version has never moved. Upstream is at v1.92.0: 30 releases after v1.66.0, none of whose
  release notes mentions a CVE or security fix; they bump Go (1.24 up to 1.27) and
  `golang.org/x/crypto`, and v1.90.0 changes the metric set (#1168, #1170, #1172).
- The pinned arm64 binary reports `Go: go1.23.2`, a Go line out of support; go1.23.5 to .12 carry
  security fixes it predates. The Docker Hub digest of `v1.66.0` equals the pin.
- The exporter holds the cluster password as `REDIS_PASSWORD`
  ([`statefulset.go:1076-1088`](../../internal/builder/statefulset.go)) and, under TLS, mounts the
  data tier's TLS Secret and uses `tls.key`, the private key of the Valkey server certificate, as its
  client key ([`statefulset.go:1097-1107`](../../internal/builder/statefulset.go)).
- No Renovate manager reads the file. The six custom managers in [`renovate.json`](../../renovate.json)
  match other files, and no built-in manager matches a Go source file. `api/v1/valkey_types.go`,
  `README.md`, `CLAUDE.md` and `docs/operations/examples.md` match no `ignorePaths` glob.
- A new custom manager would automerge: `packageRules[16]` (`renovate.json:225-236`) automerges
  minor, patch and digest updates for every `custom.regex` manager, and v1.66.0 to v1.92.0 is a
  minor. Majors are already manual (`packageRules[17]`, `:237-250`). `minimumReleaseAge` is set
  nowhere.
- A merge cuts an operator release: `packageRules[20]` (`:274-283`) makes it a `fix(deps)` commit,
  which semantic-release releases. `fix` is the right type for a shipped image.
- One dependency matched in several files by one manager becomes one PR (Renovate's default
  branch name is keyed on the dependency and the new major).
- CI only proves the exporter starts: the e2e enables metrics in `pod_security_test.go:134` and
  `pod_hardening_test.go:216`, but no test reads `/metrics`. With a wrong password the exporter
  stays up and serves `redis_up 0` (measured on `valkey/valkey:9.1.1`). A broken auth or a changed
  metric set passes CI. Trivy scans only the operator image, never the exporter image. The shipped
  PrometheusRule uses only `vko_*` series.
- Tests do not pin the value: `TestDefaultMetricsExporterImage_IsPinnedByDigest`
  ([`valkey_types_test.go:1367`](../../api/v1/valkey_types_test.go)) checks only the shape
  `^oliver006/redis_exporter:v[0-9.]+@sha256:[0-9a-f]{64}$`; other tests compare against the
  constant. A bump keeps every test green.
- A bump changes the pod-spec hash. On the chart-default path it adds no roll: every release
  already rolls multi-replica data tiers ([ADR 0005](../adr/0005-upgrade-neutral-defaults-and-anti-affinity.md)
  D11), and a rootless `spec.replicas: 1` pod without Sentinel defers it with the sidecar
  (`isSidecarOnlyChange`, [`rolling_update.go:3845-3866`](../../internal/controller/rolling_update.go);
  `singlePodDeferral`, [`pod_security_migration.go:134-139`](../../internal/controller/pod_security_migration.go)).
  On kustomize or a floating Helm `image.tag` the sidecar image does not move, so the exporter drift
  is a roll of its own, and a rootless single pod without Sentinel is deleted
  (`rolling_update.go:3816`): without persistence, with its data. That is the data-loss class of
  [ADR 0007](../adr/0007-failover-aware-rolling-update.md) D7, recurring with every bump.
- The full reference is copied in [`README.md:368`](../../README.md) (CRD reference row, required
  "with its default" by ADR 0035 D3), [`CLAUDE.md:133`](../../CLAUDE.md) (explicit, uncommented
  `image:` in the example CR) and [`docs/operations/examples.md:235`](../operations/examples.md)
  (commented, "default shown").
- Five places say the pin is maintained by hand: `README.md:368`, `DEVELOPER.md:271`,
  `CLAUDE.md:1001-1002`, ADR 0033 residual risks (`:647-648`) and H-17
  (`workload-pod-posture.md:211-213`). Three more name the version alone: ADR 0033 `:266`
  (truncated digest), [`workload-pod-posture.md:96-97`](../security/workload-pod-posture.md) and
  `:212`. All are true today.

**Impact:** every metrics-enabled cluster that leaves `spec.metrics.image` empty runs an aging
third-party binary that holds the password (auth on) and the server key (TLS on) and serves
`/metrics` over a `net/http` built with go1.23.2. All cases are dormant: no reachable
vulnerability and no hostile principal is identified. The aging also makes the first bump large.
A user can pin a newer exporter per resource with `spec.metrics.image` today.

## Required changes

### Independent of the open questions

1. **Functional exporter check, before the manager lands.** One e2e subtest on `rl-mr`
   ([`pod_security_test.go:128-135`](../../test/e2e/pod_security_test.go), TLS, auth and metrics,
   both Valkey legs) reads `/metrics` of each data pod (for example through the API server pod
   proxy) and asserts `redis_up 1`. Mutation check: with `REDIS_PASSWORD` removed from
   `buildExporterContainer`, only this subtest goes red.
2. **ADR text for the recurring bump.** Amend ADR 0033 D5, cross-referenced from ADR 0007 D7: an
   exporter-default bump rides the release roll; on the chart-default path a rootless single pod
   without Sentinel defers it with the sidecar; on kustomize or a floating `image.tag` it replaces
   that pod, and a non-persistent one loses its data, which ADR 0005 D11 and ADR 0014 D8 leave to
   the deviating admin. State ADR 0032 D3 as the counter-precedent (it protected every install path
   in code, `pod_security_migration.go:109-119`) and why this change does not get that protection
   (the only trigger is an operator upgrade on a non-canonical path, and deferring keeps a security
   bump out of the pod).

### Depends on the answers

3. **Manager (any answer to Q1).** A custom regex manager on `api/v1/valkey_types.go` keyed on the
   literal `oliver006/redis_exporter:`, capturing `currentValue` and `currentDigest`, with
   `depNameTemplate` `oliver006/redis_exporter` and datasource and versioning `docker`. No
   `// renovate:` comment, because it would join the constant's godoc. Commit type stays `fix`.
4. **Rule (Q1).** One packageRule after `renovate.json:250` with
   `matchDepNames: ["oliver006/redis_exporter"]` and either `automerge: false` (A) or a
   `minimumReleaseAge` (B).
5. **Copies (Q2).** Either add `README.md`, `CLAUDE.md` and `docs/operations/examples.md` to the
   manager (a), or make `CLAUDE.md` and `examples.md` name `DefaultMetricsExporterImage` and add
   only `README.md` (d). In both cases rephrase ADR 0033 `:266` and `:648` and
   `workload-pod-posture.md:96-97` and `:212` to name the constant instead of a version.
6. **Validation.** `renovate-config-validator` and a local dry run list `oliver006/redis_exporter`
   from every matched file; with the regex broken in a scratch copy, the dry run no longer lists it.
   After the merge, Dependency Dashboard issue #229 is the proof (each entry shows twice while T45's
   double load of `renovate.json` is open).
7. **Close.** Rewrite the five hand-maintenance statements and ADR 0033 D5 and its residual risk.
   The first Renovate PR (v1.66.0 to v1.92.0) is not part of this ticket.

## Open questions

### Q1: Does a Renovate PR for the exporter wait for a human review, or automerge after a quarantine?

Without an override the new manager falls under `packageRules[16]` and automerges immediately.
Both options cut a `fix` release and add no roll on the chart-default path; they differ in
whether a person reads the upstream changelog before the exporter ships.

- **A - `automerge: false` (recommended).** A person merges each PR. Cost: one packageRule plus a
  merge click per PR; an unmerged PR ages the pin again, but visibly on the dashboard. Can be the
  same rule as T45's `automerge: false` for `kindest/node` and `cert-manager` (T45's `chore` rule
  must not match the exporter).
- **B - automerge after `minimumReleaseAge`** (for example `"14 days"`). No merge work; a broken or
  compromised tag gets a quarantine window. Sound only once work item 1 has landed. A changed
  metric set reaches users' dashboards in a patch release with nobody having read the notes. Matches
  how the Go toolchain, Go modules and base images are already treated.

A is recommended because metric-set changes land in minor releases (v1.90.0) and no test and no
shipped alert reads the exporter's metrics, so only a person reading the changelog catches them.

**Answer:** _open_

### Q2: How do the documentary copies of the full image reference follow a bump?

`README.md:368`, `CLAUDE.md:133` and `docs/operations/examples.md:235` carry the literal
reference. Renovate edits only files its manager matches.

- **(a) The manager also matches the three documents (recommended).** One PR moves the constant and
  all copies. Cost: three more file patterns; each PR touches four files.
- **(d) Keep the literal only in the README row.** `CLAUDE.md` and `examples.md` name the constant
  instead. Follows ADR 0035 D3 more strictly and removes an explicit exporter pin from an example CR
  a reader may copy (which would freeze that cluster's exporter); the examples no longer show the
  value.

(a) is recommended because every copy is correct the moment the PR merges with no hand step, and
the documentation standard asks for examples populated with the default.

**Answer:** _open_

## Not verified

- Whether any Go or `x/crypto` fix after go1.23.2 is reachable in the exporter: a Trivy or Grype
  scan of both digests (`d98e6db8…`, `ca3abd5f19da…`) would settle it.
- The toolchain of the `linux/amd64` image: `docker run --rm --platform linux/amd64 oliver006/redis_exporter:v1.66.0@sha256:d98e6db8… --version`.
- That the literal-keyed regex extracts the dependency as intended: a dry run, or dashboard #229
  after the merge.
- Option B only: whether the pending `minimumReleaseAge` check holds a platform automerge
  (`platformAutomerge: true`, `renovate.json:15`).
- Whether an e2e can read the exporter's `/metrics` through the API server pod proxy (work item 1).

## Related

- T45 - shares the `automerge: false` rule of option A; its `chore` rules must exclude the exporter.
- T50 - under its options every exporter bump must re-check the exporter's ACL command set; work
  item 1 would catch a missing permission.
- T55 - if a `Localhost` seccomp profile ships (its option B), every exporter bump must re-validate it.
- T30 - embargoed security finding.
