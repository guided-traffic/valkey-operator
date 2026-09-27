---
id: T54
title: Renovate does not track DefaultMetricsExporterImage
state: analysed       # was filed; 2026-09-27 at 84a39c2: facts re-verified, options re-weighed, two decisions open
severity: low         # the pin ages; nothing is broken today. 2026-09-27: 23 months and 30 releases old, arm64 image built with go1.23.2 (Go 1.23 unsupported since 2025-08-12); no vulnerability verified as reachable
security: hardening
threat: "would additionally cover vulnerabilities fixed in redis_exporter and its Go toolchain after v1.66.0 (its arm64 image measured as built with go1.23.2, a Go line out of support since 2025-08-12): today the default exporter, third-party code that holds the cluster password on every auth-enabled cluster with metrics on and the private key of the Valkey server certificate on every TLS cluster with metrics on, ages until someone moves the pin by hand"  # sharpened 2026-09-27: TLS key and toolchain added, old line in History
urgency: later        # rule 4: a cheap known fix once the option is chosen (re-derived 2026-09-27: rules 1-3 do not match, every tracked statement about this pin is true today)
effort: S             # unchanged 2026-09-27: one manager, one packageRule, one e2e subtest and ADR text
blocked-by: decision  # decision 1 (review or age-gated automerge), taken together with T45's decision 1 (shared override rule); decision 2 (how the copies follow)
filed-from: T31, section "Further security measures — not in this change, each open" (archive/031)
opened: 2026-09-27
decided:              # not recorded - no decision yet
done:                 # not recorded - not done
---

Filed on 2026-09-27 from the seventh row of the table
["Further security measures — not in this change, each open"](archive/031-generated-pods-run-as-root.md#further-security-measures--not-in-this-change-each-open)
in the archived ticket 031, which is `done` and tracks none of its rows. The operator-facing
statement is gap [H-17](../security/workload-pod-posture.md#h-17).

## Fact

**Verified** (read 2026-09-27; re-verified at `84a39c2` the same day):

- `DefaultMetricsExporterImage` is the literal
  `oliver006/redis_exporter:v1.66.0@sha256:d98e6db8…` in
  [`api/v1/valkey_types.go:645`](../../api/v1/valkey_types.go), its doc comment at `:640-644`
  ([ADR 0033](../adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md)
  D5). `MetricsImage()` ([`valkey_types.go:1211-1217`](../../api/v1/valkey_types.go)) falls back to
  it when `spec.metrics.image` is empty.
- The six custom managers in [`renovate.json`](../../renovate.json) match `Makefile`,
  `Containerfile`, `go.mod`, `.github/release-template.hbs`,
  ~~the workflows and `test/testimages/images.go`~~ *(corrected 2026-09-27 at 84a39c2: the
  workflows, and by file pattern `test/testimages/images.go`, which Renovate never reads: see the
  ignorePaths bullet below)*; none matches `api/v1/valkey_types.go`. ADR 0033's residual risks
  record the gap
  ([`0033:647-648`](../adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md)).
- The same reference is quoted in four documents: [`README.md:368`](../../README.md),
  [`CLAUDE.md`](../../CLAUDE.md) ~~line 131~~ *(corrected 2026-09-27: line 133)*,
  [`docs/operations/examples.md:235`](../operations/examples.md) and ADR 0033 D5
  ([`0033:266`](../adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md),
  truncated to `v1.66.0@sha256:d98e6db8…`). The CLAUDE.md example sets it explicitly and
  uncommented, examples.md carries it commented out ("default shown").
- The exporter receives the cluster password as `REDIS_PASSWORD`
  ([`statefulset.go:1076-1088`](../../internal/builder/statefulset.go)). *(Added 2026-09-27 at
  84a39c2.)* Under TLS it also mounts the data tier's TLS Secret (`ValkeyTLSSecretName`,
  [`certificate.go:48-53`](../../internal/builder/certificate.go); the volume at
  [`statefulset.go:589-598`](../../internal/builder/statefulset.go)) and is handed `tls.key` as its
  client key ([`statefulset.go:1097-1107`](../../internal/builder/statefulset.go)): the private key
  of the Valkey server certificate.
- *(Added 2026-09-27, re-read at `4a7543e` and at `84a39c2`.)* **A new custom manager would
  automerge unless it is told not to.** `packageRules[16]`
  ([`renovate.json:225-236`](../../renovate.json)) automerges minor, patch and digest updates for
  every `custom.regex` manager. Its description says "Makefile-pinned Go tools", but it matches by
  manager alone. `:automergeDigest` is extended at line 6. A v1.66.0 → v1.92.0 bump is a minor
  (Renovate's docker versioning strips a leading `v`, `lib/modules/versioning/docker/index.ts` on
  main), so it would automerge. Majors are already manual under `packageRules[17]`
  (`renovate.json:237-250`), so an override changes minor, patch and digest only. The rule is live:
  PR #227 (golangci-lint, Makefile manager) was merged by `app/guided-traffic-automation` on
  2026-09-25. ~~The precedent for the manager's shape is the sixth custom manager
  (`renovate.json:292ff`, on `test/testimages/images.go`), which reads a
  `// renovate: datasource=docker depName=…` comment above the constant.~~
  *(corrected 2026-09-27 at 84a39c2, replacing the review's location fix to `358-369`: the sixth
  custom manager, `renovate.json:358-368` on `test/testimages/images.go` (292 opens the
  `customManagers` list, 369 closes it), reads a `// renovate: datasource=docker depName=…` comment
  above the constant, but it is no working precedent: it has never produced a dependency, because
  Renovate ignores `**/test/**` (bullet below). Its regex shape is usable; its evidence is not.)*
  *(Precised 2026-09-27 by the review: that manager captures only `currentValue`, a tag, because
  the test pins carry no digest. The exporter pin needs `currentDigest` as well, so the regex is
  new, not a copy.)*
- *(Added 2026-09-27 at 84a39c2.)* **A merge of this dependency cuts an operator release.**
  `packageRules[20]` (`renovate.json:274-283`, no manager filter) makes every minor, patch, digest
  and pin update a `fix(deps)` commit, `.releaserc.json` runs the conventionalcommits
  commit-analyzer on main, and the `semantic-release` job
  ([`release.yml:1146-1204`](../../.github/workflows/release.yml)) releases. Observed: `v1.12.1`
  contains only `21c0b85`, a `fix(deps)` tool bump. `fix` is the right type for a shipped image;
  only T45's CI-only pins take `chore`.
- *(Added 2026-09-27 at 84a39c2.)* **The version has never moved, and Renovate has never seen
  it.** `git log -G 'DefaultMetricsExporterImage = ' -- api/v1/valkey_types.go` returns two hand
  commits by Hans Fischer, and neither moved the version: `28b6830` (2026-07-21, "Feat/metrics
  endpoint (#163)") introduced the constant as `oliver006/redis_exporter:v1.66.0`, when upstream
  was already 24 releases further (v1.87.0, published 2026-07-15, per the releases API below), and
  `b13377e` (2026-09-26, released in v1.13.0 and v1.13.1) added the digest to the same v1.66.0
  while v1.92.0 was out. `gh pr list --state all --search redis_exporter` is empty.
  Dependency Dashboard issue #229 (updated 2026-09-27T02:41Z) lists no dependency from
  `api/v1/valkey_types.go`. Of the 117 built-in managers on renovatebot/renovate main that carry
  default `managerFilePatterns` (118 directories under `lib/modules/manager`, read with `gh api`),
  none matches a Go source file; `gomod` matches `(^|/)go.mod$` only.
- *(Added 2026-09-27 at 84a39c2.)* **ignorePaths.** `config:recommended`
  (`renovate.json:4`) extends `:ignoreModulesAndTests`, whose `ignorePaths` include `**/test/**`
  and `**/examples/**` (renovatebot/renovate main, `lib/config/presets/internal/default.preset.ts:303-315`;
  https://docs.renovatebot.com/presets-default/), and `lib/workers/repository/extract/file-match.ts`
  filters ignored files before any manager's patterns apply, custom managers included. Neither
  `renovate.json` nor `.github/workflows/renovate.yml` sets `ignorePaths`. The files this ticket's
  manager would read, `api/v1/valkey_types.go`, `README.md`, `CLAUDE.md` and
  `docs/operations/examples.md`, match no ignore glob: `**/examples/**` needs a directory named
  `examples`, which `docs/operations/examples.md` is not. The consequence for the test-image
  manager (it never ran, and five tracked statements say otherwise) is T45's finding and its
  decision-free work item 2, not this ticket's.
- *(Added 2026-09-27 at 84a39c2.)* **One dependency matched in several files by one manager
  becomes one PR.** Renovate's default `branchName` is
  `{{{branchPrefix}}}{{{additionalBranchPrefix}}}{{{branchTopic}}}` and the default `branchTopic` is
  `{{{depNameSanitized}}}-{{{newMajor}}}…x` (renovatebot/renovate main,
  `lib/config/options/index.ts:2474-2499`): keyed on the dependency and the new major, not on the
  file. Verified by reading the source; no dry run. `edcbf39` (PR #206, golang in `build.yml`,
  `release.yml` and `go.mod` in one PR) is not evidence of the default: it was grouped by the
  explicit `packageRules[0]` (`groupSlug: go-version`).
- *(Added 2026-09-27.)* Upstream, per the GitHub releases API (unauthenticated, 2026-09-27,
  reproduced at 84a39c2 with `curl -s 'https://api.github.com/repos/oliver006/redis_exporter/releases?per_page=100'`):
  v1.66.0 was published 2024-10-31T04:10:22Z, and the latest, v1.92.0, on 2026-09-23T06:01:06Z.
  The newest 100 releases hold 30 non-prerelease releases after v1.66.0 (v1.67.0 … v1.92.0) and
  no prerelease. *(Added at 84a39c2.)* None of the 30 release notes mentions CVE, GHSA,
  "vulnerab" or "security". They record Go toolchain bumps (1.24 in v1.68.0 up to 1.27 in
  v1.92.0), `golang.org/x/crypto` bumps (v1.81.0, v1.82.0, v1.85.0, v1.90.0, v1.91.1, v1.92.0) and
  metric-set changes in v1.90.0 (#1168 "Fix keyspace metrics for Redis and Valkey", #1170 "Skip
  duplicate Valkey cluster metrics", #1172 "Add Valkey TLS, scripting engine, and other metrics";
  also #1169, #1176, #1177).
- *(Added 2026-09-27 at 84a39c2.)* Docker Hub
  (`https://hub.docker.com/v2/repositories/oliver006/redis_exporter/tags?name=v1.66.0`): the
  digest of `v1.66.0` is `sha256:d98e6db8…e51bf2e6a1`, equal to the pin; `v1.92.0` exists with
  index digest `sha256:ca3abd5f19da…` (3 images). Suffixed variants such as `-alpine` exist;
  docker versioning does not propose them for an unsuffixed tag, and the shape test below would
  reject one.
- *(Added 2026-09-27 at 84a39c2.)* **The pinned binary is built with an unsupported Go.**
  `docker run --rm --name vko-verify-t054-ver oliver006/redis_exporter:v1.66.0 --version` prints
  `Redis Metrics Exporter v1.66.0 … Go: go1.23.2 GOOS: linux GOARCH: arm64`; the local RepoDigest
  equals the pin, and `/metrics` reports `redis_exporter_build_info` with
  `golang_version="go1.23.2"`. Per https://go.dev/doc/devel/release, go1.25.0 was released
  2025-08-12 and "each major Go release is supported until there are two newer major releases",
  so Go 1.23 is out of support; go1.23.5 (crypto/x509, net/http), .6 (crypto/elliptic), .7 and .8
  (net/http), .10 (net/http, os), .11 (go command) and .12 (database/sql, os/exec) carry security
  fixes the binary predates.
- *(Added 2026-09-27.)* Tests do not pin the value. `TestDefaultMetricsExporterImage_IsPinnedByDigest`
  checks only its shape, `^oliver006/redis_exporter:v[0-9.]+@sha256:[0-9a-f]{64}$`
  ([`valkey_types_test.go:1367`](../../api/v1/valkey_types_test.go)), and
  `deepcopy_test.go:92` uses a tag-only fixture of its own. `valkey_types_test.go:356-357` and
  `internal/builder/metrics_test.go:61` compare against the constant, so a bump keeps every test
  green.
- *(Added 2026-09-27.)* Five places state that the pin is maintained by hand, and they change on
  close: `README.md:368` ("It is not updated automatically"), `DEVELOPER.md:271`,
  `CLAUDE.md:1001-1002`, ADR 0033 residual risks (lines 647-648) and gap H-17
  (`docs/security/workload-pod-posture.md:205-213`, text at `:211-213`). *(Added at 84a39c2.)* Three
  more name the version alone and go stale at the first bump:
  [`workload-pod-posture.md:96-97`](../security/workload-pod-posture.md) ("the digest of the
  multi-arch image index behind `v1.66.0`"), the `v1.66.0` in H-17 (`:212`) and ADR 0033 `:648`.
  All eight are true today.
- *(Added 2026-09-27.)* **What CI checks of a bump:** the e2e enables metrics in
  `pod_security_test.go:134` and `pod_hardening_test.go:216`, so the exporter has to start under
  the restricted posture. A crash-looping exporter leaves the pod not Ready and fails those tests
  (the exporter has no readiness probe, [`statefulset.go:1055-1059`](../../internal/builder/statefulset.go),
  ADR 0018 D2, so it is ready while it runs; `pod_security_test.go:151-153` waits for the
  StatefulSet Ready; read, not measured). No e2e reads `/metrics`: a grep for `/metrics`, `9121`
  and `redis_up` over `test/e2e/` finds nothing, and over `test/` only
  `test/integration/suite_test.go:31`, the operator's own endpoint. A changed metric set or a
  broken auth therefore passes CI. `rl-mr` (`pod_security_test.go:128-135`) is the one e2e cluster
  with TLS, auth and metrics, on both Valkey legs through `testimages.Default()`.
- *(Added 2026-09-27 at 84a39c2, measured twice.)* **A broken exporter auth is invisible.**
  `docker run -d --rm --name vko-verify-t054-valkey -p 127.0.0.1:19121:9121 valkey/valkey:9.1.1 valkey-server --requirepass right-pw`,
  then `docker run -d --rm --network container:vko-verify-t054-valkey -e REDIS_ADDR=redis://localhost:6379 -e REDIS_PASSWORD=<pw> -e REDIS_EXPORTER_WEB_LISTEN_ADDRESS=:9121 oliver006/redis_exporter:v1.66.0`
  and `curl -s 127.0.0.1:19121/metrics`. With `wrong-pw` the container stays up (after 20 s:
  `Up 20 seconds`, RestartCount 0), `/metrics` serves `redis_up 0` and
  `redis_exporter_last_scrape_error{err="dial redis: unknown network redis"} 1` (the label does not
  name authentication), and the log says "Couldn't connect to redis instance
  (redis://localhost:6379)". With `right-pw`: `redis_up 1`, `redis_connected_clients 1`. Every
  container started was removed afterwards.
- *(Added 2026-09-27 at 84a39c2.)* **A bump adds no roll on the chart-default install path.**
  `ComputePodSpecHash` ([`statefulset.go:1228`](../../internal/builder/statefulset.go)) covers the
  whole built pod spec, so the template changes. But every data pod's sidecar runs the operator
  image (`--operator-image`,
  [`deployment.yaml:39`](../../deploy/helm/valkey-operator/templates/deployment.yaml)), whose tag
  defaults to `Chart.AppVersion`
  ([`_helpers.tpl:69`](../../deploy/helm/valkey-operator/templates/_helpers.tpl)), and
  [`build.yml:179-182`](../../.github/workflows/build.yml) stamps `appVersion` and the values `tag`
  per release. ADR 0005 D11
  ([`0005:297-301`](../adr/0005-upgrade-neutral-defaults-and-anti-affinity.md)): "every operator
  release already rolls every multi-replica data StatefulSet", and a change riding along "adds
  nothing". Per topology
  (dispatch [`rolling_update.go:321-328`](../../internal/controller/rolling_update.go)):
  - multi-replica, with or without Sentinel: rolled by the release anyway.
  - a Sentinel cluster with `spec.replicas: 1` goes through `handleRollingUpdate`, never
    `singlePodDeferral`, so no exporter-specific deferral applies to it; ~~its only data pod is
    replaced on every release anyway, and without persistence loses its data to that release, not
    to the exporter ([ADR 0032](../adr/0032-generated-pods-run-rootless.md) residual risks,
    `0032:544-548`)~~ *(struck in the final pass of 2026-09-27: what that roll does with the
    only data pod is not this ticket's to state, and the sentence is not relied on by any option
    here)*.
  - a rootless `spec.replicas: 1` pod without Sentinel defers the combined change:
    `isSidecarOnlyChange` ([`rolling_update.go:3845-3866`](../../internal/controller/rolling_update.go))
    compares only the Valkey and sidecar images, so it is true, and `singlePodDeferral`
    ([`pod_security_migration.go:134-139`](../../internal/controller/pod_security_migration.go))
    defers under `SidecarUpdatePending`; the new exporter arrives at the next restart (the reading
    ADR 0033 `:437-448` records for the digest pin and for `spec.podSecurity`).
  - on kustomize or a Helm install with a floating `image.tag` the sidecar image does not move
    (ADR 0005 D11, `0005:310-313`), so the exporter drift is a roll of its own, and on a rootless
    single pod without Sentinel `isSidecarOnlyChange` is false and the pod is deleted
    ([`rolling_update.go:3816`](../../internal/controller/rolling_update.go)): without persistence,
    with its data. That is the class ADR 0007 D7
    ([`0007:273-293`](../adr/0007-failover-aware-rolling-update.md)) calls a data-loss change
    "unless it gets its own decision"; T54 turns it from a one-off (ADR 0033 D5, `0033:437-441`)
    into a recurring one.
- *(Added 2026-09-27 at 84a39c2.)* CI runs Trivy only against the operator image
  ([`release.yml:1087-1098`](../../.github/workflows/release.yml), exit code 1 on fixed CRITICAL
  or HIGH); the default exporter image the operator deploys is never scanned. The shipped
  PrometheusRule (`deploy/helm/valkey-operator/templates/prometheusrule.yaml`) uses only `vko_*`
  series, so a changed exporter metric set breaks no alert this repository ships, only users' own
  dashboards and alerts.
- *(Added 2026-09-27 at 84a39c2.)* The repository already automerges code that ships in the
  operator: Go toolchain minor and patch (`packageRules[2]`, `renovate.json:47-57`), Go modules
  (`[5]`, `:83-97`) and Containerfile base images (`[8]`, `:129-139`). `minimumReleaseAge` is set
  nowhere in `renovate.json`.

**Not verified:**

- ~~Which redis_exporter releases followed v1.66.0, and~~ *(corrected 2026-09-27: the releases
  are answered in Fact)* ~~whether any of them fixes a vulnerability. No changelog was read and no
  scanner was run on either image.~~ *(corrected 2026-09-27 at 84a39c2: the changelogs were read,
  name no CVE, and the toolchain is measured, see Fact.)* Not verified: whether any Go or
  `x/crypto` fix after go1.23.2 is reachable in the exporter (it serves `/metrics` over
  `net/http` and dials Valkey over TLS). A Trivy or Grype scan of both image digests
  (`d98e6db8…` and `ca3abd5f19da…`) would settle it.
- *(Added 2026-09-27 at 84a39c2.)* The toolchain of the `linux/amd64` image of the same index,
  the one most nodes run: only arm64 was measured. `docker run --rm --platform linux/amd64
  oliver006/redis_exporter:v1.66.0@sha256:d98e6db8… --version` would settle it (it pulls the amd64
  image).
- ~~*(Added 2026-09-27.)* That Renovate puts one dependency, matched in several files by one
  manager, into a single PR. No dry run was made.~~ *(corrected 2026-09-27 at 84a39c2: verified by
  reading Renovate's default branch template, see Fact; a dry run remains the direct proof.)*
- ~~That no built-in Renovate manager extracts a Go string constant. Inferred: `git log -S
  'redis_exporter:'` on the file finds only `28b6830` (2026-07-21, by hand), and no Renovate run
  was observed.~~ *(corrected 2026-09-27 at 84a39c2: verified, see Fact; `-S` missed `b13377e`
  because adding `@sha256:` does not change the count of `redis_exporter:`, `-G` finds both.)*
- *(Added 2026-09-27 at 84a39c2.)* That the literal-keyed regex of work item 2 extracts the
  dependency as intended: no dry run. Dashboard #229 shows it after the merge.
- *(Added 2026-09-27 at 84a39c2.)* Option B's quarantine: read, not observed. Per
  https://docs.renovatebot.com/key-concepts/minimum-release-age/, docker release timestamps come
  from Docker Hub's `tag_last_pushed` only, digests get the tag's timestamp, and since Renovate 42
  the default `minimumReleaseAgeBehaviour` `timestamp-required` holds a release without a
  timestamp; an update younger than the age gets a pending status check. Renovate 44.115.10 runs
  here (T45, from dashboard #229). Whether that pending check holds a platform automerge
  (`platformAutomerge: true`, `renovate.json:15`) was not observed, and whether the Renovate PR
  body carries upstream release notes for this image was not checked.
- *(Added 2026-09-27 at 84a39c2.)* Whether redis_exporter v1.87.0 #1135 "Add TLS server name"
  would let the exporter drop `REDIS_EXPORTER_SKIP_TLS_VERIFICATION` (ADR 0018 D1): not read.
- *(Added 2026-09-27 at 84a39c2.)* Whether an e2e can read an exporter's `/metrics` through the
  API server's pod proxy (work item 1): not tried.

## Impact

Every metrics-enabled cluster that does not set `spec.metrics.image`. An operator can pin a
newer exporter per resource today with `spec.metrics.image`. *(Added 2026-09-27 at 84a39c2.)*
Per case, all dormant (`hardening`: no reachable vulnerability and no hostile principal is
identified): on an auth-enabled cluster the aging binary holds the cluster password; on a TLS
cluster it also holds the private key of the Valkey server certificate; on every metrics-enabled
cluster it serves `/metrics` over a `net/http` built with go1.23.2 (arm64 measured). Pin aging is
also what makes the first bump large: 30 releases, with a metric-set change in v1.90.0.

## Options

*(Rewritten 2026-09-27 at 84a39c2 as the current analysis; the removed options and the reasons are
in History.)* Two decisions, the first before the second.

### Decision 1: how the pin moves

**What the code does today.** The constant
([`valkey_types.go:645`](../../api/v1/valkey_types.go)) is compiled into the operator and reaches
the exporter container of every metrics-enabled data pod that leaves `spec.metrics.image` empty,
together with the password and, under TLS, the server key (Fact). No manager reads the file, and
the version has never moved: introduced at v1.66.0 on 2026-07-21 already 24 releases behind,
digest-pinned on 2026-09-26 at the same version, now 30 releases behind (Fact). A new custom manager
without an override falls under `packageRules[16]` and automerges, as a `fix(deps)` commit that
cuts an operator release (Fact). CI proves only that the exporter starts: with a wrong password it
stays up with `redis_up 0` (measured), until work item 1 lands.

**What the choice changes:** whether a person reads the upstream changelog before an exporter
release ships in an operator release, and how fast a bump lands. **What it does not change:** the
roll (on the chart-default path none is added, Fact), the commit type (`fix` either way), the
release cut (it happens under both), `spec.metrics.image` overrides, and the data-loss case on
kustomize or a floating tag (work item 4).

- **A — a custom regex manager plus an `automerge: false` override (recommended).** The manager of
  work item 2 and one packageRule placed after `renovate.json:250` (later rules win, and none of
  `packageRules[18]`-`[21]` sets `automerge`): `matchDepNames: ["oliver006/redis_exporter"]`,
  `automerge: false`. It can be the one rule that also lists T45's `kindest/node` and
  `cert-manager/cert-manager` (T45 decision 1); T45's `chore` rule must not match the exporter.
  *Cost:* S; a person merges each PR. *Consequences:* one open Renovate PR, updated in place as
  upstream releases; digest updates of a re-pushed tag wait for review too. A PR nobody merges ages
  the pin again, but visibly: as an open PR and on dashboard #229, not silently as today.
- **B — the same manager, automerged after a `minimumReleaseAge` quarantine.** One packageRule
  with `matchDepNames: ["oliver006/redis_exporter"]` and `minimumReleaseAge` (for example
  `"14 days"`), automerge left on. Defensible only once work item 1 has landed, because without it
  CI checks nothing but startup. *Cost:* S, no merge work. *Consequences:* a compromised or broken
  upstream tag gets a quarantine window before it reaches an operator release (a re-pushed tag
  restarts the clock, Not verified); auth and TLS regressions are caught by work item 1; a changed
  metric set reaches users' dashboards and alerts in an operator patch release with nobody having
  read the notes, which breaks no alert this repository ships (the PrometheusRule uses `vko_*`
  only). It matches how the repository already treats the Go toolchain, Go modules and base images.

**A is marked** because the one thing B cannot catch is the one that recurs in this dependency:
metric-set changes land in minor releases (v1.90.0 #1168, #1170, #1172), no test and no shipped
alert reads the exporter's metrics, so only a person reading the changelog sees them before users'
dashboards do. It costs the same one packageRule as B, plus a merge click per PR. Two arguments
used earlier do not separate them and are not reused: "a change that rolls every metrics-enabled
data tier" (false on the chart-default path, Fact), and "an unreviewed third-party binary holding
the password in an automatically cut release" (the release is cut under both options, and the
repository already automerges shipped code). B is the real runner-up: with work item 1 in place
and a quarantine, it is sound for auth and supply-chain risk and loses only on the metric set.

### Decision 2: how the documentary copies follow a bump

**What the code does today.** The full reference is copied at `README.md:368` (the CRD
reference row, which ADR 0035 D3,
[`0035:134-136`](../adr/0035-the-readme-advertises-the-reference-lives-under-docs.md), requires
"with its default"), `CLAUDE.md:133` (an explicit, uncommented `image:` in the example CR) and
`docs/operations/examples.md:235` (commented, "default shown"). Renovate edits only files its
manager matches, and none of these files is under an ignore glob (Fact). **What the choice
changes:** whether the copies move in the Renovate PR or are replaced by a name. **What it does
not change:** the constant, decision 1, and the four prose mentions that are rephrased to name the
constant in any case (work item 3).

- **(a) the manager also matches `README.md`, `CLAUDE.md` and `docs/operations/examples.md`
  (recommended).** The same literal-keyed regex (tag and full digest), so one PR moves the
  constant and three copies. *Cost:* three more file patterns. *Consequences:* each Renovate PR
  touches four files; the CLAUDE.md example keeps setting the image explicitly, but current.
- **(d) keep the literal only in the README row**, and have `CLAUDE.md` and `examples.md` name
  `DefaultMetricsExporterImage` instead of the value; the manager matches the Go file and
  `README.md`. *Cost:* two prose edits once. *Consequences:* follows ADR 0035 D3 (one row with the
  default, an operations page never restates it) more strictly, and removes an explicit exporter
  pin from an example CR that a reader may copy (a copied `spec.metrics.image` freezes that
  cluster's exporter, which is the aging this ticket ends); the examples no longer show the value.

**(a) is marked** because it is the only option under which every copy is correct the moment the
PR merges with no hand step, and the owner's documentation standard asks for examples populated
with the default where one exists. (d) is the runner-up: cleaner against ADR 0035 D3, but it trades
a copy Renovate maintains for free for a name the reader has to look up.

## Decision

None yet.

## Work list

No item here is both XS and free of the decisions above.

1. *(decision-free; lands before item 2)* **A functional check of the exporter.** One e2e subtest on
   `rl-mr` ([`pod_security_test.go:128-135`](../../test/e2e/pod_security_test.go), TLS, auth and
   metrics, both Valkey legs) that reads `/metrics` of each data pod (for example through the API
   server's pod proxy, not tried) and asserts `redis_up 1`. It checks image and wiring together
   (the Secret key ref at `statefulset.go:1076-1088`, the TLS env and mount at `:1097-1107`) against
   a real Valkey, which ADR 0017 places in e2e. First, so the 30-release first PR is checked. The
   gap exists without T54 (a builder change to the exporter env is unchecked as well); ~~the owner
   may split it into its own ticket~~ *(sweep 2026-09-27: it is filed as this item, which closes
   it, so it needs no file of its own under the filing rule; Hans may still split it)*. Mutation check (ADR 0017): with the `REDIS_PASSWORD` env var
   removed from `buildExporterContainer` in a scratch copy, the exporter still runs and serves
   `redis_up 0` (the measured shape), so only this subtest goes red. A key ref pointed at a missing
   Secret key is no discriminating mutation: the container would not start at all (Kubernetes
   reports `CreateContainerConfigError` for a missing, non-optional key; read, not measured), which
   the existing Ready waits already catch.
2. *(waits on Decision 1)* **The manager and its rule.** A `matchString` keyed on the literal
   `oliver006/redis_exporter:` capturing `currentValue` and `currentDigest`, with `depNameTemplate`
   `oliver006/redis_exporter`, `datasourceTemplate` `docker` and `versioningTemplate` `docker`, on
   `api/v1/valkey_types.go`. No `// renovate:` comment: a line between the doc comment
   (`valkey_types.go:640-644`) and the constant (`:645`) would join its godoc. Then the rule of
   option A or B. The commit type stays `fix` (`packageRules[20]`).
3. *(waits on Decision 2)* The file patterns of (a), or the prose edits of (d). Either way,
   rephrase to name the constant instead of a version: ADR 0033 `:266` (truncated digest, the regex
   does not match it), `workload-pod-posture.md:96-97` and `:212`, ADR 0033 `:648`.
4. *(decision-free, part of the close)* **ADR text for the recurring exporter bump against ADR 0007
   D7.** Amend ADR 0033 D5, cross-referenced from ADR 0007 D7: an exporter-default bump rides the
   release roll; on the chart-default path a rootless single pod without Sentinel defers it with
   the sidecar; ~~a Sentinel cluster with `spec.replicas: 1` is replaced by every release anyway;~~ *(struck in
   the final pass of 2026-09-27, as in Fact)* on
   kustomize or a floating `image.tag` it replaces a rootless single pod without Sentinel, and a
   non-persistent one loses its data, which ADR 0005 D11 and ADR 0014 D8 leave to the deviating
   admin. The text says openly that ADR 0032 D3 is a counter-precedent (it protected every install
   path in code, for root pods, `pod_security_migration.go:109-119`), why this recurring change does
   not get that protection (the only trigger is an operator upgrade on a non-canonical path, and
   deferring would keep a security bump out of the pod), and that T54 makes the deletion recurring:
   the only earlier change of the default (`b13377e`, the digest) shipped with the rootless posture,
   where a non-persistent single pod still running as root deferred it with the posture (ADR 0033
   `:437-441`), so no non-persistent single pod has yet lost its data to an exporter default (read
   from the code and the history of the constant, not observed on a fleet). The code alternative is in
   History (removed options).
5. *(waits on items 2 and 3)* `renovate-config-validator` plus a local dry run, and the revert
   check (Verification).
6. Close ([ADR 0034](../adr/0034-tickets-are-work-lists-that-get-archived.md)): ADR 0033 D5
   (from line 262, including item 4) and its residual risk (647-648) amended, the five
   hand-maintenance statements in Fact rewritten, `git grep` `054` and `T54`, then move to
   `archive/`. The first Renovate PR (v1.66.0 → v1.92.0) is not part of this ticket.
   ~~It rolls every metrics-enabled data tier on the release that ships it
   ([ADR 0018](../adr/0018-metrics-and-the-exporter-sidecar.md)).~~ *(corrected 2026-09-27 at
   84a39c2: on the chart-default path it adds no roll, the release rolls those tiers anyway, ADR
   0005 D11; ADR 0018 D7 is about enabling metrics through a CR edit. On kustomize or a floating
   `image.tag` it is a roll of its own, see item 4.)*

## Verification

- A Renovate dry run (or the dependency dashboard) lists `oliver006/redis_exporter` from
  `api/v1/valkey_types.go`. *(Precised 2026-09-27 at 84a39c2.)* Dependency Dashboard issue #229
  is the post-merge proof, for the Go file and, under (a), for the three documents; it also proves
  the files are not under `ignorePaths`, which a regex test alone would not. Expect each entry
  twice while T45's work item 4 (the double load of `renovate.json`) is open.
- Revert check: with the manager's regex broken in a scratch copy of `renovate.json`, the same
  dry run no longer lists it.
- *(Added 2026-09-27 at 84a39c2.)* Work item 1: the subtest is green on both Valkey legs and red
  under its mutation.

## Cross-ticket (2026-09-27 at 84a39c2)

- **T45** shares the `automerge: false` rule of option A; its `chore` rules must exclude the
  exporter, which ships and keeps `fix`. Under option B, T54 needs its own rule anyway. T45 carries
  the finding that the test-image manager never ran (`ignorePaths`) and the double load of
  `renovate.json` (why dashboard #229 lists every regex file twice); neither blocks this ticket.
- **T50** ~~cites `valkey_types.go` "line 643" (now 645), and its option D ("that pin is maintained
  by hand (ticket 054)") goes stale when T54 lands.~~ *(corrected 2026-09-27, consistency pass:
  050's re-verification of the same day cites `:645` and rewrote option D; the "maintained by
  hand" sentence is gone. 050 now recommends E, an exporter user whose password is generated in
  the pod, with D as runner-up, and its cross-ticket note asks this ticket's choice between
  review and automerge to account for the exporter's command set.)* Under T50 D, E or F every
  exporter bump must re-check
  the exporter's ACL command set, and a missing permission would pass today's CI (measured:
  `redis_up 0`, container up). Work item 1 would catch it.
- **T55**: if a recommended `Localhost` seccomp profile ships, every exporter bump must
  re-validate it against the new binary; the Renovate PR of option A is where that happens.
  *(Precised 2026-09-27, consistency pass: 055 recommends A, refusing to ship a profile, with B,
  one merged profile, as runner-up; this applies only if B is taken, and 055 counts it in B's
  cost.)*
- **T30** (severity high, security boundary, effort M, state ~~filed~~ analysed *(corrected
  2026-09-27, consistency pass)*): embargoed security finding,
  open - details in its own ticket file until it is fixed.
- **archive/031** row 7 (`:686`) is this ticket's source and matches it; nothing to change there.

## History

- 2026-09-27: re-verified at 84a39c2 - checked every Fact claim against the code, `renovate.json`,
  the chart, the CI files, dashboard #229, Renovate source and docs, the GitHub releases API, Docker
  Hub and go.dev; locations re-read at 84a39c2 (`valkey_types.go:643` → `:645`,
  `renovate.json:358-369` → `:358-368`, `statefulset.go` 1078 → `:1076-1088`, `CLAUDE.md:1002` →
  `:1001-1002`, H-17 `207-213` → `205-213`). **False or outdated:** the sixth custom manager is no
  working precedent (it never ran, Renovate ignores `**/test/**`; T45's finding); the claim that a
  bump rolls every metrics-enabled data tier (Options A, B, the closing line, Work list item 4 of
  the earlier numbering, now item 6) is false
  on the chart-default path, where the release rolls those tiers anyway (ADR 0005 D11) and a
  rootless single pod defers the change; "one answer and one change can cover" T45 and T54 was
  overstated (only the `automerge: false` rule is shared, the commit types differ); the close list
  missed three version-only mentions; `git log -S` missed `b13377e`; `edcbf39` is not evidence for
  the default branch grouping (it was grouped by `packageRules[0]`). **Moved to Verified:** one PR
  per dependency across files (Renovate branch template), no built-in manager reads a `.go` file.
  **Measured:** `redis_exporter:v1.66.0 --version` prints go1.23.2 (arm64; amd64 not measured);
  with a wrong password the exporter stays up and serves `redis_up 0` (twice, on
  `valkey/valkey:9.1.1`); the v1.66.0 Docker Hub digest equals the pin. **New facts:** the TLS
  server key in the exporter, the release cut by a `fix(deps)` merge (`v1.12.1`), no CVE in 30
  release notes, the metric-set changes of v1.90.0, Trivy scans only the operator image, the
  PrometheusRule uses `vko_*` only, the ADR 0007 D7 data-loss case on kustomize or a floating tag.
  **Options rewritten** into decision 1 (A, B) and decision 2 ((a), (d)). **Removed:** C ("stay
  manual, with a comment at the constant") - it measurably failed: the version has never moved, the
  constant was introduced 24 releases behind (`28b6830`), the digest pin (`b13377e`) kept the same
  version, and it is now 30 releases behind on an unsupported Go; bare B ("the same manager with automerge") - replaced
  by B with a `minimumReleaseAge` quarantine, which dominates it at the cost of one field, and its
  cost text ("a fleet roll lands on one green CI run") was false; (b) ("a unit test asserts each
  document contains the constant") - every Renovate PR goes red by construction, and a hand commit
  stops Renovate maintaining the branch (`rebaseWhen: conflicted`, `renovate.json:22`); (c) ("drop
  the literal from the documents") - contradicts ADR 0035 D3. **Considered and not added:** a Trivy
  scan of the exporter image (detects but does not move the pin; as a required gate it turns
  unrelated PRs red on a published CVE, as a non-required job it is no gate, ADR 0017 D47; its own
  ticket if wanted); an operator-owned exporter (L, disproportionate, its own motivation); deferring
  exporter-only drift on single pods in code (M, an ADR 0007 D6 re-decision only for the
  non-canonical install path ADR 0005 D11 assigns to the deviating admin); checking the exporter in
  the imagetools tier (M, dominated by the e2e subtest, and blind to the Secret and mount wiring).
  **Added:** B (age-gated automerge) as decision 1's runner-up, (d) as decision 2's runner-up, and
  two decision-free work items the ticket missed: the functional exporter check (item 1) and the
  ADR 0007 D7 text (item 4). **Recommendations:** decision 1 stays A, with its justification
  replaced (the roll argument is false, the third-party-password argument does not separate A from
  B; the metric set does); decision 2 stays (a). **Frontmatter:** `state` filed → analysed (facts
  and options verified; what stays open is implementation verification); the threat line names the
  server key and the Go toolchain, old line: "would additionally cover vulnerabilities fixed in
  redis_exporter after v1.66.0: today the default exporter, third-party code that holds the cluster
  password on every auth-enabled cluster with metrics on, ages until someone moves the pin by
  hand"; severity (low), urgency (`later`, rule 4) and effort (S) re-derived and unchanged;
  `blocked-by` comment names the two decisions. **Review of this entry's edit (same day, at
  84a39c2):** the draft said the pin was "moved by hand twice in 23 months"; `git show 28b6830`
  shows that commit introduced the constant at v1.66.0 (upstream then at v1.87.0, 24 releases
  further, counted from the releases API) and `b13377e` only added the digest, so the version never
  moved - corrected in Fact, decision 1 and here, which strengthens the removal of C. Also
  corrected: A "beats B by exactly one packageRule" (both need one rule; A adds the merge click),
  work item 1's mutation (a key ref to a missing key stops the container, which the Ready waits
  already catch; removing `REDIS_PASSWORD` is the discriminating one), work item 4's "happened
  twice before" (no non-persistent single pod has lost data to an exporter default yet, because
  `b13377e` shipped with the rootless posture and deferred with it), the ADR 0035 D3 lines
  (`133-135` → `134-136`), the counter-precedent comment lines (`pod_security_migration.go:109-114`
  → `:109-119`), and a nested correction in the precedent-manager Fact bullet folded into one.
  Cross-ticket: in the consistency pass of the same day, the T50 bullet was corrected (050 cites
  `:645`, rewrote option D and recommends E), the T55 bullet precised (055 recommends A, so the
  re-validation applies only under its B), and T30's state corrected to `analysed`.
  Sweep: The Work list's e2e item no longer leaves "the owner may split it into its own ticket"
  open: the gap it closes is filed as that item, which satisfies the filing rule (Hans may still
  split it). The T30 line now carries only the words the embargo permits. Frontmatter unchanged.
  Final pass: in Fact (the per-topology list) and Work list item 4 the claim that the only data pod
  of a Sentinel cluster with `spec.replicas: 1` is replaced on every release is struck, not
  corrected here; no option of this ticket rests on it. Frontmatter unchanged.
- 2026-09-27: reviewed - spot-checked the new line numbers at `4a7543e`. Corrected the location of
  the precedent manager (358-369, not 292ff) and noted that it captures no digest. Frontmatter and
  recommendations unchanged.
- 2026-09-27: enriched - corrected the CLAUDE.md line and answered which releases followed
  v1.66.0 (30, the latest from 2026-09-23). Found the custom-regex automerge rule that would turn
  A into B, and that CI never reads `/metrics`. Added Decision 2 (the copies, (a) recommended), a
  work list and `blocked-by: decision`. Urgency (`later`, rule 4: severity stays low, no
  vulnerability verified) and effort (S) unchanged.
- 2026-09-27 — filed from the row "Renovate for `DefaultMetricsExporterImage`" of archive/031,
  which names ADR 0033's residual risks. Gap [H-17](../security/workload-pod-posture.md#h-17)
  states what is missing.
