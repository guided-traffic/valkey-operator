---
id: T53
title: the release pipeline does not stamp the digest of the image it pushes into the chart
state: analysed       # was filed; the load-bearing facts are verified (run log, registry, runtime source, prototype renders) and every option carries a keep/drop reason (2026-09-27)
severity: low         # the default install runs by tag; pinning is available to every installer today
security: hardening
threat: "would additionally cover whoever can re-push the operator's version tag on Docker Hub without controlling the GitHub repository (other accounts with push rights on the guidedtraffic Docker Hub organisation, the push token if it leaks outside GitHub): today a default install pulls by tag the operator (cluster-wide ClusterRole), its pre-upgrade hook (its own ClusterRole) and, through --operator-image, every sidecar and observer, which hold the cluster's Valkey password when auth is enabled (the sidecar also its pod-patch token); a GitHub repository writer, who controls both the chart and the push credential, is covered by no stamp"  # sharpened 2026-09-27: the sidecar and observer do not run under the operator's grant; was "... so that principal decides what runs under the operator's cluster-wide grant"
urgency: now          # rule 1 (was later, rule 4): the ADR 0033 D5 title "by default are, pinned by digest" is measured false for the operator image; returns to later (rule 4) once that XS item lands, or if the owner reads the title as scoped to its body (History 2026-09-27)
effort: S
blocked-by: decision  # which guard the stamp gets, below; Work list item 1 is not blocked
filed-from: T31, section "Further security measures — not in this change, each open" (archive/031)
opened: 2026-09-27
decided:              # not recorded - no decision yet
done:                 # not recorded - not done
---

Filed on 2026-09-27 from the sixth row of the table
["Further security measures — not in this change, each open"](archive/031-generated-pods-run-as-root.md#further-security-measures--not-in-this-change-each-open)
in the archived ticket 031, which is `done` and tracks none of its rows. The operator-facing
statement is gap [H-12](../security/operator-pod-posture.md#h-12).

## Fact

**Verified** (read 2026-09-27, re-read at `84a39c2`):

- [`build.yml`](../../.github/workflows/build.yml) runs on a published release
  ([`build.yml:2-4`](../../.github/workflows/build.yml)). Its `build` job pushes the image with
  `docker/build-push-action` (step `id: build`, line 78, `push: true` at line 87) and declares no
  job `outputs:` (lines 18-125; the only `outputs:` key, line 97, is an input of the step).
- Its `release-helm-gh` job (`needs: build`, line 129) stamps the chart version, the
  `appVersion` and `image.tag` with `sed` (lines 179–182) and packages the chart; nothing stamps
  `image.digest`, which stays `""` ([`values.yaml:14`](../../deploy/helm/valkey-operator/values.yaml)).
  The published chart of the latest release ships it empty: `gh release download v1.13.1 -p
  valkey-operator-1.13.1.tgz` gives `values.yaml` `tag: "1.13.1"`, `digest: ""` and `Chart.yaml`
  `appVersion: 1.13.1` (2026-09-27).
- When `image.digest` is set, the chart renders `repository:tag@digest` for the operator, the
  hook, `--operator-image` and `OPERATOR_IMAGE`
  ([ADR 0033](../adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md)
  D5). The value reaches the sidecar through `BuildStatefulSet`
  ([`statefulset.go:989-996`](../../internal/builder/statefulset.go), sidecar unconditional at
  1044-1049) and the observer through `BuildObserverDeployment`
  ([`observer.go:82`](../../internal/builder/observer.go)), both fed from `--operator-image`
  ([`cmd/main.go:74`](../../cmd/main.go), `OPERATOR_IMAGE` as its default;
  [`valkey_controller.go:1278, 2033`](../../internal/controller/valkey_controller.go)). The
  Sentinel builder takes no operator image, so Sentinel pods are not touched by it. Setting it
  changes `--operator-image` and so rolls the data tiers as any operator image change does; set
  in the same upgrade that moves the tag it costs nothing extra (ADR 0033 *Consequences*). Every
  release moves the tag. *(Added 2026-09-27 at `84a39c2`: the pod-spec hash covers the whole built
  PodSpec, sidecar image included ([`statefulset.go:154, 1228-1229`](../../internal/builder/statefulset.go)).
  The exception ADR 0033 *Consequences* itself names: a rootless single standalone data pod defers
  a sidecar-only change to its next restart under `SidecarUpdatePending` (ADR 0007 D6), so a
  changed operator reference reaches that pod's sidecar only then, exactly as a new tag does today.
  The observer Deployment is rewritten.)*
- *(Added 2026-09-27.)* Locations: the push step is
  [`build.yml:76-97`](../../.github/workflows/build.yml) (`platforms: linux/amd64` at line 86,
  `provenance`/`sbom` at 94-95, an explicit `outputs:` entry at 97). The stamp lines are
  `build.yml:179-182`, after the dirty-tree check at 162-170. `values.yaml` has exactly one
  `  repository:`, `  tag:` and `  digest:` line (6, 9, 14), so a `sed` stamp of `  digest:` is
  unambiguous, and a release-only key such as `  releaseDigest:` or `  releaseTag:` does not
  match `^  digest:` or `^  tag:`. The helper is
  [`_helpers.tpl:68-77`](../../deploy/helm/valkey-operator/templates/_helpers.tpl), used at
  [`deployment.yaml:34, 39, 49`](../../deploy/helm/valkey-operator/templates/deployment.yaml)
  and [`pre-upgrade-job.yaml:34`](../../deploy/helm/valkey-operator/templates/pre-upgrade-job.yaml).
  ~~The latest release is `v1.13.1`, and only a docs commit follows it.~~ *(corrected 2026-09-27
  at `84a39c2`: `v1.13.1` (`7017676`, released 2026-09-27T03:23Z) is still the latest release
  (`gh release list`), and three docs-only commits follow it: `4a7543e`, `bcc63c9`, `84a39c2`
  (`git log --oneline v1.13.1..HEAD`).)*
- *(Added 2026-09-27.)* **The helper appends the digest to whatever repository and tag are
  set** (`_helpers.tpl:69-75`). A digest in the published chart's `image.digest` therefore
  outlives an installer's override. `--set image.tag=X` renders `repository:X@<release digest>`,
  and `--set image.repository=<mirror>` renders `<mirror>:<version>@<Docker Hub digest>`.
  *(Measured 2026-09-27 with `helm template` (v3.21.3) on a copy of the published v1.13.1 chart
  with `digest: "sha256:6a6b1d7d…"` stamped in: `--set image.tag=1.12.8` renders
  `guidedtraffic/valkey-operator:1.12.8@sha256:6a6b1d7d…`, a repository override renders
  `harbor.example/…:1.13.1@sha256:6a6b1d7d…`.)* A single value cannot tell a stamped default
  from an installer's pin: Helm merges chart defaults and user values before rendering, and
  `_helpers.tpl:70` reads only `.Values.image.digest`.
- *(Added 2026-09-27.)* The fleet-upgrade e2e installs its starting release from the published
  chart without an image override
  ([`fleet_upgrade_test.go:226-234`](../../test/e2e/fleet_upgrade_test.go)), and upgrades to the
  local chart, whose digest stays `""`, with repository and tag set (lines 319-326, the exact
  image asserted at 329). A stamp reaches that e2e only once `E2E_UPGRADE_FROM`
  ([`Makefile:167`](../../Makefile), `1.10.48`) names a stamped release. *(Added 2026-09-27 at
  `84a39c2`: no workflow runs this e2e (`grep -rn fleet .github/workflows/` is empty), so it runs
  only locally, Makefile:170-173. Its starting-version check,
  `require.Contains(image, fromVersion)` at line 238, still passes with `@digest` appended.)*
- *(Verified 2026-09-27; was Not verified.)* **The step's `digest` output is the index Docker Hub
  serves under the version tag.** The pushed version tag names an OCI image index, not a single
  manifest: `docker buildx imagetools inspect guidedtraffic/valkey-operator:1.13.1` gives
  `MediaType: application/vnd.oci.image.index.v1+json`, `Digest:
  sha256:6a6b1d7dc2c05342f6d425bc250195b3336ba11e6371687a3f46d6e8dcc9bbc1`, with a `linux/amd64`
  manifest `sha256:53fbc018…` and an attestation manifest `sha256:80685331…`. The release run of
  v1.13.1 (`gh run view 36291251509 --log`, event `release`, head `7017676`) logs `exporting
  manifest list sha256:6a6b1d7d…` (log line 1338), `##[group]Digest sha256:6a6b1d7d…` (1368-1369)
  and `containerimage.digest: sha256:6a6b1d7d…` with media type
  `application/vnd.oci.image.index.v1+json` (1438). `docker/build-push-action@v7` declares the
  output in its `action.yml:118-119` (`digest: Image digest`). Both the auditor and a second
  reviewer re-measured this on 2026-09-27 with the same result.
- *(Verified 2026-09-27; was Not verified.)* **How `outputs:` at `build.yml:97` combines with
  `push: true`.** buildx runs with both `--output type=image,name=target,annotation-index.org.opencontainers.image.description=Valkey …`
  and `--push` (run log line 1154). The build metadata's `image.name` is the metadata-action tag
  list (`guidedtraffic/valkey-operator:1.13.1,…:1.13,…:1,…:sha-7017676,…:latest`, line 1439), not
  `target`, and `docker buildx imagetools inspect --raw …:1.13.1` shows the index annotation
  `org.opencontainers.image.description: Valkey Operator`. The entry adds only that annotation
  and does not change what the `digest` output reports.
- *(Added 2026-09-27.)* **Every release moves `1`, `1.13`, `latest` and `sha-<commit>` to the same
  index** (run log 1347-1360; `imagetools inspect` of `1.13`, `1`, `latest` and `sha-7017676`
  all give `sha256:6a6b1d7d…`). `latest` is pushed on a release event although the raw rule at
  [`build.yml:56`](../../.github/workflows/build.yml) is enabled only for `refs/heads/main`: the
  docker/metadata-action README states that its default `flavor: latest=auto` generates `latest`
  for `type=semver` (https://github.com/docker/metadata-action#latest-tag).
- *(Verified 2026-09-27; was Not verified.)* **A runtime pulls `repository:X@D` by `D` alone and
  ignores `X`.** containerd v2.1.4 `internal/cri/server/images/image_pull.go:153` calls
  `distribution.ParseDockerRef(name)` (imported at line 39 from `github.com/distribution/reference`),
  and distribution/reference v0.6.0 `normalize.go:107-121` returns only the digested reference
  when one is both tagged and digested ("only return digested"). CRI-O: PR "Favour the digest over
  the tag if both specified" (https://github.com/cri-o/cri-o/pull/3060), merged 2020-03-10 and
  backported to 1.16 and 1.17 (read by the auditor, not re-read by the second reviewer).
- *(Added 2026-09-27.)* **Every build has its own digest**: `BUILD_TIME` is in the operator's
  ldflags ([`Containerfile:31`](../../Containerfile), fed from `build.yml:81-85`). A re-run of the
  whole release workflow re-pushes the version tag with a new digest.
- *(Added 2026-09-27.)* **Helm `upgrade --reuse-values` keeps the old chart's defaults.** Helm
  v3.21.3 `pkg/action/upgrade.go:559-574`: with `ReuseValues`, `oldVals :=
  CoalesceValues(current.Chart, current.Config)` and `chart.Values = oldVals`, so the old chart's
  stamped `image.tag` (and any stamped release value) replaces the new chart's. **Flux does not
  do this by default**: helm-controller `internal/action/upgrade.go:110-111` (main at
  `9e3b5577`, fetched 2026-09-27) sets `ResetValues = !PreserveValues` and `ReuseValues =
  PreserveValues`, and neither production HelmRelease below sets `preserveValues`.
- *(Added 2026-09-27.)* **Both production fleets install the default image.** The owner's local
  clones, read 2026-09-27: `k8s-flux-base` (HEAD `3d105ed4`, file last changed `52ef2a6d`, 2026-04-28)
  `components/database-operators/valkey-operator/app/helmrelease.yml` follows chart
  `version: "*"` with `interval: 1m` from `https://guided-traffic.github.io/valkey-operator`, and
  `stutor-k8s-flux-base` (HEAD `e8e735f`, file last changed `c68a391`, 2026-07-09) pins chart `1.10.29`.
  Both set only `podLabels`, `resources` and `preUpgradeHook`; neither sets `image.repository`,
  `image.tag` or `image.digest`. So wds18 takes every published chart within about a minute.
- *(Added 2026-09-27.)* **The sidecar and the observer hold the cluster's Valkey password** when
  auth is enabled (`IsAuthEnabled`), by `secretKeyRef` ([`statefulset.go:946-957`](../../internal/builder/statefulset.go),
  [`observer.go:251-257`](../../internal/builder/observer.go)); the hook runs under its own
  ClusterRole (get/list/patch/update on `valkeys` and CRDs,
  [`pre-upgrade-rbac.yaml:18-50`](../../deploy/helm/valkey-operator/templates/pre-upgrade-rbac.yaml)).
  The builders set no `ImagePullPolicy` (`grep ImagePullPolicy internal/builder` is empty), so the
  sidecar and observer pull with the Kubernetes default for a tagged image, `IfNotPresent`, as the
  chart's own `pullPolicy` does ([`values.yaml:7`](../../deploy/helm/valkey-operator/values.yaml)).
- *(Added 2026-09-27.)* **The pre-upgrade hook runs only on `helm upgrade`**, and only while
  `preUpgradeHook.enabled` (default `true`,
  [`values.yaml:151-153`](../../deploy/helm/valkey-operator/values.yaml);
  [`pre-upgrade-job.yaml:1, 11`](../../deploy/helm/valkey-operator/templates/pre-upgrade-job.yaml)).
  A reference the runtime cannot pull therefore fails a `helm upgrade` at the hook, but on a fresh
  `helm install` shows only as `ImagePullBackOff` on the operator pod.
- *(Added 2026-09-27.)* **ADR 0033 D5's title contradicts the published chart for the operator
  image.** [ADR 0033:262](../adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md)
  reads "Images can be, and by default are, pinned by digest"; the v1.13.1 chart renders
  `guidedtraffic/valkey-operator:1.13.1` with no digest, and the same ADR's residual risk
  (lines 645-646) says the chart default is empty. The body of D5 pins only the exporter by
  default. T54 plans to rephrase the D5 body, not this title.
- *(Added 2026-09-27, noted only.)* The SBOM and Docker Scout steps reference the image by tag
  (`build.yml:102, 121`); with the digest as a job output they could reference `@digest`. Not
  proposed as T53 work.
- *(Added 2026-09-27.)* The chart is packaged in CI with Helm v4.3.0 (`build.yml:153`); every
  render above used Helm v3.21.3 locally.

**Not verified:**

- ~~That the `digest` output of `docker/build-push-action` with `provenance: true` and
  `sbom: true` is the digest a pull of `tag@digest` resolves (expected: the pushed index).
  *(Added 2026-09-27.)* Nor how the explicit `outputs:` entry at `build.yml:97` combines with
  `push: true` in naming and reporting the pushed image; it was not traced.~~ *(resolved
  2026-09-27 at `84a39c2`: both verified, see Verified.)*
- ~~*(Added 2026-09-27.)* What a runtime does with `repository:X@D` when the tag X names another
  image (expected: pulls D and ignores X), and~~ *(resolved 2026-09-27: verified from containerd
  and CRI-O source, see Verified.)* *(Added 2026-09-27.)* Whether any installer mirrors the image
  with a tool that keeps the index digest (expected: `crane copy` or `skopeo copy --all` keep it,
  a single-platform `docker pull`/`push` does not). Not measured; it bears only on the removed
  options A and A″, because under the recommended option a repository override gets no stamped
  digest. A `crane copy` or `skopeo copy --all` of `…:1.13.1` into a scratch registry, compared
  with `sha256:6a6b1d7d…`, would settle it. Whether a Harbor proxy-cache project keeps the index
  digest is likewise not verified.
- *(Added 2026-09-27.)* Whether a Kind node on arm64, loaded with the amd64 image by
  `kind load`, can resolve a `tag@<index digest>` reference without pulling. This matters for the
  local fleet-upgrade e2e once its starting release is a stamped one. *(2026-09-27:)* If
  containerd does not find `repo@<index digest>` locally it pulls the index from Docker Hub, which
  holds only a `linux/amd64` manifest, so it is expected to fail on an arm64 node; whether
  `kind load` of a platform-specific `docker pull` keeps the index digest in the node's
  containerd depends on the Docker image store and is not measured either. The fallback is
  `--set image.releaseDigest=` in the install step at `fleet_upgrade_test.go:226-234`. CI is
  unaffected. A local Kind cluster on arm64 with a stamped release would settle it.
- *(Added 2026-09-27.)* That GitHub reuses the `build` job's outputs when only failed jobs are
  re-run. The auditor cited
  https://docs.github.com/en/actions/how-tos/manage-workflow-runs/re-run-workflows-and-jobs for
  it; a second reviewer read that page and found no such sentence (it states only that a re-run
  keeps `GITHUB_SHA` and `GITHUB_REF`). A test workflow with a job output, re-run with
  `--failed`, would settle it. Either way a stamp can only name a digest the same run pushed.
- *(Added 2026-09-27.)* Helm 4 `--reuse-values` semantics were not read; the Helm source above is
  v3.21.3.
- *(Added 2026-09-27.)* The remote state of the two fleet repositories was not fetched; the
  HelmRelease facts above are from the local clones.
- *(Added 2026-09-27.)* Which Docker Hub role may change repository settings such as immutable
  tags (https://docs.docker.com/docker-hub/repos/manage/hub-images/immutable-tags/ does not
  state it).

## Impact

Every install that keeps the chart default. An installer can pin today by setting
[`image.digest`](../operations/installation.md#imagedigest).

*(Added 2026-09-27, per case, `security: hardening`.)*

- **Principal:** whoever can push tags to `docker.io/guidedtraffic/valkey-operator`. The release
  job logs in as `guidedtraffic` with the repository secret `DOCKERHUB_PAT`
  ([`build.yml:36-40`](../../.github/workflows/build.yml)).
- **Verb:** re-push the version tag (`1.13.1`) to another image. `1`, `1.13`, `latest` and
  `sha-<commit>` already move to each release's index by design.
- **Object:** the operator (cluster-wide ClusterRole), its pre-upgrade hook (its own
  ClusterRole) and, through `--operator-image`, every sidecar and observer, which hold the
  cluster's Valkey password when auth is enabled (the sidecar also its pod-patch token, ADR 0031). A node pulls on a
  cache miss (`IfNotPresent`), so the re-pushed image reaches every node that schedules a new
  pod, not only fresh installs.
- **Live or dormant:** dormant, no known compromise of the Docker Hub side.
- **What a stamp moves and what it does not.** With the digest stamped, what a default install
  runs is decided by the chart on `gh-pages`, not by the tag. That removes the principals who
  hold Docker Hub push rights alone: other accounts on the Docker Hub organisation, and the push
  token if it leaks outside GitHub. It does not remove a GitHub repository writer, who already
  controls every install through the chart (they could change `image.repository` there just as
  easily) and can read the push secret through a workflow. `gh-pages` carries no branch
  protection (`gh api repos/guided-traffic/valkey-operator/branches/gh-pages/protection`: 404
  "Branch not protected", 2026-09-27). A compromise of the release workflow run itself, which
  pushes the image and publishes the chart, is covered by no stamp.

## Options

### Decision 1 — how the published chart pins the pushed image, and for which installs

**Mechanism today.** `build.yml:76-97` builds and pushes `guidedtraffic/valkey-operator` for
`linux/amd64` with provenance and an SBOM; the version tag names an OCI index
(`sha256:6a6b1d7d…` for v1.13.1). The step already reports that index digest as
`steps.build.outputs.digest`, but the `build` job exports no output, and `release-helm-gh`
stamps only `version`, `appVersion` and `image.tag` (`build.yml:179-182`). The published chart
therefore ships `image.digest: ""`, and every default install pulls the operator, the hook, every
sidecar and the observer by tag. `_helpers.tpl:68-77` appends any non-empty `image.digest` to
whatever repository and tag are set, and containerd and CRI-O then pull by the digest alone.

**What the choice changes:** whether the published chart carries the pushed index digest, and
which installs it applies to. **What it does not change:** the committed `values.yaml` on `main`
(the stamp happens only in the release checkout, like the tag), so the CI e2e and local installs
stay as they are; the roll count of default installs, which already roll on every release
because the tag moves; installers who set `image.digest` themselves; the Valkey and exporter
images (`spec.image`, `DefaultMetricsExporterImage`); a GitHub repository writer and a
compromised release run (Impact).

Both options below share the stamp: export `steps.build.outputs.digest` as a `build` job output,
check it in `release-helm-gh` against `^sha256:[0-9a-f]{64}$`, and `sed` it into a release-only
value, for example `image.releaseDigest` (committed empty). The helper applies it only while
`image.digest` is empty, `image.repository` is literally `guidedtraffic/valkey-operator` and the
effective tag passes the guard; an explicit `image.digest` still wins. They differ only in the
tag guard. Both fail open: when a condition does not hold the render is by tag, as today, never
a different image.

Common to both, measured with `helm template` (v3.21.3) on a copy of the published v1.13.1 chart
with `releaseDigest: sha256:6a6b1d7d…` and the guard (prototypes under the session scratchpad,
`work/t53-skeptic/ap` and `work/t53-writer/rt`): the default renders
`--operator-image=guidedtraffic/valkey-operator:1.13.1@sha256:6a6b1d7d…` (the hook image too);
`image.tag=1.12.8` renders `…:1.12.8`; a Harbor repository override renders `harbor…:1.13.1`;
an explicit `image.digest=sha256:aaa…` renders `…:1.13.1@sha256:aaa…`; `image.releaseDigest=`
renders `…:1.13.1`; `image.repository=docker.io/guidedtraffic/valkey-operator` renders by tag,
because the comparison is literal. The literal lives in `values.yaml:6` and in the helper, and a
render row catches drift between them.

Costs common to both: the first stamped release changes a default install's `--operator-image`
from `repo:X` to `repo:Y@D` in the same upgrade that already moves the tag, so there is no extra
roll (ADR 0033:455-459; a rootless single standalone data pod defers it under
`SidecarUpdatePending` and loses no data). **Not every override stays as today**: an installer
who sets `image.tag` explicitly to the chart's own release gets pinned, and on the next chart
upgrade without moving the tag the pin disappears (`image.tag=1.13.1` on a 1.13.2 chart renders
`…:1.13.1`), so `--operator-image` changes although the image does not, and the data tiers roll
once with identical content; a tag set ahead of the chart becomes pinned when the chart catches
up, which is another such roll. Any tag guard has this. An installer who overrides repository or
tag to something else stays by tag and pins through `image.digest`, as today. Both production
fleets use the default image (Fact) and are pinned; wds18 on the first stamped release within
about a minute, stutor once its pin `1.10.29` moves past it.

- **A′ — guard on a stamped release tag (recommended).** `release-helm-gh` also stamps
  `image.releaseTag` (committed empty) with the same `${VERSION}` it writes into `image.tag`, and
  the guard is `eq $tag .Values.image.releaseTag`. Cost: about five workflow lines (job output,
  format check, two `sed`), two values lines with a comment, four template lines, and the render
  rows. Under `helm upgrade --reuse-values` the old chart's `image.tag`, `image.releaseTag` and
  `image.releaseDigest` travel together into the new templates, so the install keeps a consistent
  old pin: no change, no roll (measured: the stamped 1.13.1 values under `appVersion: 1.13.2`
  render `…:1.13.1@sha256:6a6b1d7d…`).
- **A′v — guard on `.Chart.AppVersion`.** The guard is `eq $tag .Chart.AppVersion`, one values
  line and one `sed` fewer. Under `helm upgrade --reuse-values` the old tag no longer equals the
  new `AppVersion`, so a pin this option set is silently dropped and `--operator-image` changes
  from `…:1.13.1@D` to `…:1.13.1`, a roll with identical content (measured on the same values
  with the `AppVersion` guard: `…:1.13.1`). Under Flux, which resets values on upgrade
  (helm-controller `upgrade.go:110-111`), the two options behave the same.

**Recommended: A′**, because it is the only form whose pin survives `--reuse-values`, and that is
checkable: the same stamped 1.13.1 values rendered under a 1.13.2 chart give `…:1.13.1@sha256:6a6b1d7d…`
with A′ and `…:1.13.1` with A′v. It beats A′v for one values line and one `sed` line; A′v is
equally good only for installers that never use `--reuse-values`, which includes both production
fleets but not every installer of a published chart.

Implementation note for either: a malformed release value must be refused with a message that
names the value in use. The prototype that reuses the existing check says `image.digest must be
sha256:<64 hex characters>, got "bogus"` for a bad `releaseDigest`; the A′ prototype's message
`image.digest (or image.releaseDigest) must be …` shows the fix.

*Superseded 2026-09-27 at `84a39c2` (the option set above replaces the earlier A / A′ / B / C
list; the removed options and their reasons are in History):* ~~A is marked because it is the
smallest change, uses the mechanism the tag already uses, and makes the chart users install pin
by default.~~ ~~A′ … the tag resolves to `.Chart.AppVersion`. An explicit `image.digest` still
wins, and every override behaves as it does today.~~ ~~Neither A nor A′ adds a roll: every
release already moves the tag in `--operator-image`, so pinning inside the release that ships it
costs nothing extra.~~ *(corrected 2026-09-27: under A an installer who pins `image.tag` gets the
release digest appended and rolls on every stamped release, because every build has its own
digest; under either tag guard an explicit tag equal to the release flips between pinned and
unpinned with a roll of identical content, see "Costs common to both"; and the `AppVersion` guard,
now A′v, drops its own pin under `--reuse-values`.)*

## Decision

Not decided.

## Work list

~~No item here is both XS and free of the decision above.~~ *(corrected 2026-09-27 at `84a39c2`:
item 1 is XS and free of the decision.)*

1. *(XS, no decision needed)* Correct the title of ADR 0033 D5
   ([ADR 0033:262](../adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md))
   in place (struck through, dated): the operator image is pinnable by `image.digest` and not
   pinned by default; the exporter default is. Also re-read ADR 0033:58, which lists "`image.digest`
   in the chart" among the digest fixes. This item alone sets `urgency: now` (rule 1); once it lands
   the urgency returns to `later` (rule 4).
2. *(waits on the decision)* `build` job: `outputs: digest: ${{ steps.build.outputs.digest }}`.
   `release-helm-gh`: check the format (`^sha256:[0-9a-f]{64}$`) and fail the release otherwise,
   then stamp the release value(s) next to `build.yml:182`. That is after the dirty-tree check at
   `build.yml:162-170`, so the stamp cannot trip it.
3. *(waits on the decision)* The release value(s) in `values.yaml` (committed empty, with a
   comment), the guard in `_helpers.tpl:68-77` with a refusal message that names the value in
   use, and render rows: default with a stamped value, tag override, repository override,
   explicit digest, release value cleared, malformed release value refused, explicit tag equal to
   the release, and the `--reuse-values` simulation (old values under a newer `appVersion`). The
   rows belong in T58's render matrix; if T53 lands first, they run by hand with `helm template`
   and move there when T58 lands.
4. *(follows the decision; no decision of its own)* A release-time check in `release-helm-gh`
   after packaging and before publishing: `helm template` of the packaged `.tgz` must contain
   `--operator-image=guidedtraffic/valkey-operator:${VERSION}@${DIGEST}`, and the release fails
   otherwise. Item 2 checks only the format, and T58's matrix runs on the committed chart, whose
   release values are empty, so neither sees a stamped chart that renders unpinned (literal drift
   between helper and `values.yaml:6`, a quoting difference between stamped tag and release tag).
   That failure is safe (by tag) but undetected, and wds18 takes a published chart within a
   minute.
5. Close ([ADR 0034](../adr/0034-tickets-are-work-lists-that-get-archived.md)): the decision
   into ADR 0033 (its residual risk at lines 645-646 amended, D5 re-read), gap H-12
   (`operator-pod-posture.md:71-77`) rewritten, [`installation.md#imagedigest`](../operations/installation.md#imagedigest)
   (lines 59-68) and the `README.md:550` values row describing the stamped default and what an
   override does, `git grep` `053` and `T53`, then move to `archive/`. *(Added 2026-09-27:)*
   Also [ADR 0013:253-254](../adr/0013-operator-is-cluster-wide-privileged.md) ("nothing in the
   release pipeline fills it"), [`operator-pod-posture.md:25`](../security/operator-pod-posture.md)
   (the table row "empty # default"), [`upgrading.md:155-158`](../operations/upgrading.md) (how
   `--operator-image` is composed), [`workload-pod-posture.md:99`](../security/workload-pod-posture.md),
   [`DEVELOPER.md:313-316`](../../DEVELOPER.md) (the list of what `build.yml` stamps),
   `CLAUDE.md:1001-1003` ("default empty") and the `values.yaml:10-13` comment. Done-ness needs
   the next real release (Verification).

## Verification

- For the next release: ~~`image.digest` in the published chart~~ *(2026-09-27: under A′ and
  A′v, the release-only `image.releaseDigest` in the published chart)* equals the digest
  `docker buildx imagetools inspect guidedtraffic/valkey-operator:<version>` reports.
- *(Added 2026-09-27, cluster-free.)* `gh release download v<next> -p valkey-operator-<next>.tgz`,
  then `helm template` of it shows `--operator-image=guidedtraffic/valkey-operator:<next>@<digest>`
  equal to that imagetools digest, and with `--set image.tag=<previous>` shows no digest. Done for
  v1.13.1 on 2026-09-27 as the baseline: by tag, no digest.
- A Kind install from the published chart runs the operator pod with an image reference that
  ends in that digest, and a new data pod's sidecar image carries it too.
- *(Added 2026-09-27, A′ only.)* The same install with `--set image.tag=<previous version>`
  runs that version's image by tag, with no digest in the reference.
- *(Added 2026-09-27.)* The release run of that version shows item 4's render check green.

## History

- 2026-09-27: re-verified at `84a39c2` (auditor, a facts reviewer and a design reviewer; edited
  by the writer, who re-rendered the disputed guard variants). **Checked:** every location claim
  (holds; locations re-read at `84a39c2`, the helper, stamp and push lines unchanged), the
  published v1.13.1 chart (`digest: ""`), the release run log `36291251509`, the Docker Hub
  index, containerd and CRI-O source, Helm v3.21.3 and Flux helm-controller upgrade semantics,
  and both production HelmReleases in the owner's local clones. **Found outdated:** "only a docs
  commit follows" v1.13.1 (three follow: `4a7543e`, `bcc63c9`, `84a39c2`); "No item here is both
  XS and free of the decision above" (the ADR 0033 D5 title correction is). **Found false:** the
  re-weighed claim "Neither A nor A′ adds a roll" (under A an installer who pins `image.tag`
  rolls on every stamped release, because every build has a new digest), and A′'s "every override
  behaves as it does today" (an explicit tag equal to the chart's release flips between pinned
  and unpinned with a roll of identical content). **Resolved Not-verified items:** the step's
  digest is the served index; `outputs:` adds only an index annotation; containerd and CRI-O pull
  by digest alone. **Measured:** `imagetools inspect` of `1.13.1`, `1.13`, `1`, `latest`,
  `sha-7017676` (all `sha256:6a6b1d7d…`); `gh release download` of the v1.13.1 chart;
  `helm template` renders of an A copy, an A′v prototype and an A′ prototype (Options);
  `imagetools inspect ghcr.io/guided-traffic/valkey-operator:latest` ("not found"). No container
  was started. **Not verified, and disputed:** that "re-run failed jobs" reuses job outputs (the
  cited GitHub page does not say so). **Options:** rewritten as one decision between two guard
  variants. **A′ changed** from the `.Chart.AppVersion` guard to a guard on a stamped
  `image.releaseTag`, because the `AppVersion` guard silently drops its own pin under
  `helm upgrade --reuse-values` (Helm v3.21.3 `upgrade.go:559-574`, measured by render); the
  previous form stays as the runner-up **A′v**. The recommendation stays on A′ in its new form.
  **Removed options:** **A** (stamp the digest into `image.digest` itself) - it survives every
  override, so `--set image.tag=1.12.8` silently runs the release image under the old label
  (containerd, CRI-O) and rolls that installer's data tiers on every stamped release, and a
  mirror override fails the pull unless the mirror kept the index digest (at the hook on upgrade,
  as `ImagePullBackOff` on a fresh install); A′ removes all three for a few more lines, and A's
  one advantage (it would also pin proxy installs) is worth nothing to the production fleets,
  which use the default image. **A″** (new, not kept: A′ without the repository literal, guard on
  the tag only) - a mirror that re-pushed the image would get a reference that does not exist, so
  an installer who changed nothing breaks, where A′ fails open. **B** (a bot commits the digest to
  `main` after each release) - the committed digest would be appended to every in-tree install
  that overrides repository and tag, the CI e2e install
  ([`release.yml:353-358`](../../.github/workflows/release.yml)), `test/e2e/helm-values.yaml:4-7`
  (`pullPolicy: Never`) and the fleet-upgrade upgrade step (`fleet_upgrade_test.go:319-329`, exact
  image asserted), and every e2e leg would break unless each clears it; it also needs a write
  path to `main` past the required checks (ADR 0017 D47), disproportionate for a low hardening
  item. **C** (refuse, keep leaving pinning to the installer, record it in ADR 0033) - the stamp
  is verified trustworthy and S effort with no extra roll for default installs, so a refusal saves
  nothing measurable and leaves every default install, both production fleets included, by tag.
  **E** (new, not kept: Docker Hub immutable tags for the full-version tags) - not a digest pin,
  a registry setting outside this repository that nothing here checks and that the Docker Hub
  principal it would constrain can switch off; it would also make a re-run of the whole release
  fail at the push. **Frontmatter:** `state` filed -> analysed (facts verified, options costed,
  the production unknown resolved); `threat` sharpened (the sidecar and observer do not run under
  the operator's grant but hold the Valkey password; the principal is the Docker Hub side, not a
  repository writer); `urgency` later -> now by rule 1, because the ADR 0033 D5 title
  (ADR 0033:262) is a measured-false statement in a tracked file about this gap and this ticket
  now carries its correction (Work list item 1). The reviewers disagreed here: one read the title
  as scoped by its body (the exporter default), under which rule 1 does not match and `later` by
  rule 4 stands; the other applied the first match mechanically. This entry takes the mechanical
  reading; if the owner takes the scoped one, urgency returns to `later` and item 1 is dropped
  with that reason. Severity, security class, effort and `blocked-by` unchanged. **Cross-ticket:**
  T58 - A′'s render rows (Work list item 3) belong in its matrix, and its "malformed
  `image.digest`" row should get a twin for the release value. T54 - rephrases the D5 body, not
  its title; item 1 here covers the title. T50 - no contradiction: its argument that the sidecar and
  observer need no ACL user of their own (050:95-97 at `84a39c2`) was corrected in the same run to
  hold only for a supply-chain compromise of the operator image (050:296-302), which is exactly
  what T53 hardens for default installs. T47 - under its option C the hook
  (`pre-upgrade-job.yaml:34`) drops out of the tag-pinned consumers of the operator image
  (047:329). **T44 (not T53 scope, to be carried into T44):**
  [`config/manager/manager.yaml:19-23`](../../config/manager/manager.yaml) passes neither
  `--operator-image` nor `OPERATOR_IMAGE` (`grep -rn 'operator-image\|OPERATOR_IMAGE' config/` is
  empty), so on the kustomize path the sidecar falls back to
  `ghcr.io/guided-traffic/valkey-operator:latest` (`statefulset.go:989-992`), which does not exist
  (`docker buildx imagetools inspect`: "not found", 2026-09-27), and the observer gets an empty
  image (`observer.go:82`), which the API server refuses; this bears on T44's E1 (`make deploy`)
  and supports retiring that path. *(Review, 2026-09-27: T44 already carries the fallback and
  the empty observer image at 044:545-550; new for it is only the measurement that the ghcr
  `latest` image does not exist, to be carried into T44.)* 031 provenance re-checked
  (archive/031:674-688, `state: done`).
  Cross-ticket: in the consistency pass of the same day, T30's state was corrected to `analysed`,
  the ghcr `latest` measurement marked for T44 is now in 044's E1 appendix (re-run: "not found"),
  and 058's Related tickets now name A′'s release-only values and its malformed-release-value row.
  Filed: no new ticket from this one, because it parked no finding; removed a bullet on a question
  raised in review that lies outside this ticket's subject. Frontmatter, options and work list do not
  rest on that bullet and are unchanged.
  Sweep: One sentence of this entry's cross-ticket list and the wording of its Filed sentence were
  shortened before commit; frontmatter, options and work list unchanged.
- 2026-09-27: enriched - re-verified at `4a7543e` and added the locations. Found that the helper
  appends a stamped digest to any overridden reference, so added A′ (guarded stamp) and marked it
  recommended over A. Added a work list and `blocked-by: decision`. Urgency (`later`, rule 4:
  only the verification waits for a release) and effort (S) unchanged.
- 2026-09-27 — filed from the row "Release pipeline stamps the pushed digest into the chart" of
  archive/031, which names ADR 0033's residual risks. Gap
  [H-12](../security/operator-pod-posture.md#h-12) states what is missing and how an installer
  pins in the meantime.
