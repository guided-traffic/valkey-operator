---
id: T53
title: the release pipeline does not stamp the digest of the image it pushes into the chart
state: analysed       # facts verified, both options costed
severity: low         # the default install runs by tag; every installer can pin today
security: hardening
threat: "whoever can re-push the operator's version tag on Docker Hub without controlling the GitHub repository (other accounts on the guidedtraffic organisation, a leaked push token) decides the image that default installs run for the operator, its pre-upgrade hook and every sidecar and observer, which hold the Valkey password when auth is enabled."
urgency: now          # rule 1: the ADR 0033 D5 title is false for the operator image (Q2)
effort: S
blocked-by: decision  # Q1; required change 1 is not blocked
filed-from: T31, section "Further security measures — not in this change, each open" (archive/031)
opened: 2026-09-27
decided:              # no decision yet
done:                 # not done
---

# T53 - the release pipeline does not stamp the digest of the image it pushes into the chart

## Current state

The operator-facing statement is gap [H-12](../security/operator-pod-posture.md#h-12).

- [`build.yml`](../../.github/workflows/build.yml) runs on a published release. Its `build` job
  pushes the image in step `id: build` ([`build.yml:76-97`](../../.github/workflows/build.yml),
  `linux/amd64`, provenance and SBOM) and declares no job `outputs:`.
- `release-helm-gh` stamps `version`, `appVersion` and `image.tag` with `sed`
  (`build.yml:179-182`, after the dirty-tree check at 162-170). Nothing stamps `image.digest`, which
  stays `""` ([`values.yaml:14`](../../deploy/helm/valkey-operator/values.yaml)). The published
  v1.13.1 chart ships `tag: "1.13.1"`, `digest: ""`.
- The step output `steps.build.outputs.digest` is the OCI image index Docker Hub serves under the
  version tag (v1.13.1: `sha256:6a6b1d7d…`, a `linux/amd64` manifest plus an attestation
  manifest). The explicit `outputs:` entry at `build.yml:97` adds only an index annotation and
  does not change that digest. Every build has its own digest (`BUILD_TIME` in the ldflags,
  [`Containerfile:31`](../../Containerfile)). Every release also moves `1`, `1.13`, `latest` and
  `sha-<commit>` to the same index.
- The helper [`_helpers.tpl:68-77`](../../deploy/helm/valkey-operator/templates/_helpers.tpl)
  appends a non-empty `image.digest` to whatever repository and tag are set, for the operator, the
  hook, `--operator-image` and `OPERATOR_IMAGE`
  ([`deployment.yaml:34, 39, 49`](../../deploy/helm/valkey-operator/templates/deployment.yaml),
  [`pre-upgrade-job.yaml:34`](../../deploy/helm/valkey-operator/templates/pre-upgrade-job.yaml)).
  containerd and CRI-O pull `repository:X@D` by `D` alone and ignore `X`. So a digest stamped
  into `image.digest` itself would outlive an installer's tag or repository override.
- `--operator-image` feeds the sidecar ([`statefulset.go:989-996`](../../internal/builder/statefulset.go))
  and the observer ([`observer.go:82`](../../internal/builder/observer.go)); Sentinel pods do not
  use it. Changing it rolls the data tiers like any operator image change, which costs nothing
  extra in the upgrade that already moves the tag. A rootless single standalone data pod defers
  the sidecar change to its next restart under `SidecarUpdatePending` (ADR 0007 D6).
- Sidecar and observer hold the Valkey password by `secretKeyRef` when auth is enabled
  ([`statefulset.go:946-957`](../../internal/builder/statefulset.go),
  [`observer.go:251-257`](../../internal/builder/observer.go)); the builders set no
  `ImagePullPolicy`, so they pull with `IfNotPresent` on a cache miss.
- `helm upgrade --reuse-values` (Helm v3.21.3) carries the old chart's values, stamped ones
  included, into the new templates. Flux resets values on upgrade unless `preserveValues` is set.
  Both production fleets use the default image; wds18 follows chart `version: "*"` with
  `interval: 1m`, stutor pins chart `1.10.29`.
- The fleet-upgrade e2e installs its start release from the published chart without an image
  override ([`fleet_upgrade_test.go:226-234`](../../test/e2e/fleet_upgrade_test.go)) and runs only
  locally; a stamp reaches it once `E2E_UPGRADE_FROM` ([`Makefile:167`](../../Makefile)) names a
  stamped release.
- [ADR 0033:262](../adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md)
  (D5 title) reads "Images can be, and by default are, pinned by digest"; for the operator image
  that is false, and the same ADR's residual risk (lines 645-646) says the chart default is empty.

**Impact.** Principal: whoever can push tags to `docker.io/guidedtraffic/valkey-operator` (the
release logs in with secret `DOCKERHUB_PAT`, [`build.yml:36-40`](../../.github/workflows/build.yml)).
Action: re-push the version tag to another image. Target: the operator (cluster-wide ClusterRole),
the pre-upgrade hook (its own ClusterRole) and every sidecar and observer, on every node that pulls
on a cache miss. Dormant. A stamp removes the Docker-Hub-only principals; it does not cover a
GitHub repository writer (controls the chart and the push secret; `gh-pages` has no branch
protection) or a compromised release run. An installer can pin today with
[`image.digest`](../operations/installation.md#imagedigest).

## Required changes

### Independent of the open questions

1. Correct the ADR 0033 D5 title in place (subject to Q2): the operator image is pinnable by
   `image.digest`, not pinned by default; the exporter default is. Re-read ADR 0033:58, which
   lists "`image.digest` in the chart" among the digest fixes.

### Depends on the answers

2. `build` job: `outputs: digest: ${{ steps.build.outputs.digest }}`. `release-helm-gh`: fail the
   release unless it matches `^sha256:[0-9a-f]{64}$`, then `sed` it into the release-only value
   `image.releaseDigest` next to `build.yml:182` (under A′ also `image.releaseTag` with `${VERSION}`).
3. `values.yaml`: the release-only value(s), committed empty, with a comment. `_helpers.tpl`: apply
   `releaseDigest` only while `image.digest` is empty, `image.repository` is literally
   `guidedtraffic/valkey-operator` and the tag passes the Q1 guard; otherwise render by tag. A
   malformed release value is refused with a message naming that value.
4. Render rows (in T58's matrix, by hand with `helm template` until it exists): default with a
   stamped value, tag override, repository override, explicit digest, release value cleared,
   malformed release value refused, explicit tag equal to the release, `--reuse-values` simulation
   (old values under a newer `appVersion`), and a literal drift row between helper and `values.yaml:6`.
5. Release-time check after packaging, before publishing: `helm template` of the packaged `.tgz`
   must contain `--operator-image=guidedtraffic/valkey-operator:${VERSION}@${DIGEST}`, or the
   release fails. Items 2 and 4 do not see a stamped chart that renders unpinned.
6. Docs: ADR 0033 residual risk (645-646) with the decision, gap H-12
   (`operator-pod-posture.md:71-77` and the table row at :25),
   [`installation.md#imagedigest`](../operations/installation.md#imagedigest), `README.md:550`,
   [ADR 0013:253-254](../adr/0013-operator-is-cluster-wide-privileged.md),
   [`upgrading.md:155-158`](../operations/upgrading.md),
   [`workload-pod-posture.md:99`](../security/workload-pod-posture.md),
   [`DEVELOPER.md:313-316`](../../DEVELOPER.md), `CLAUDE.md:1001-1003`, the `values.yaml:10-13`
   comment.

**Verification** (needs the next real release): the published chart's `image.releaseDigest`
equals `docker buildx imagetools inspect guidedtraffic/valkey-operator:<version>`; `helm template`
of the downloaded `.tgz` shows `--operator-image=…:<version>@<digest>`, and with
`--set image.tag=<previous>` no digest; a Kind install runs the operator and a new sidecar with that
digest, and the tag override by tag; the release run shows item 5 green.

## Open questions

### Q1: Which tag guard decides that the stamped digest applies?

The stamped digest may apply only when the rendered tag is the chart's own release, or a tag
override would run the release image under another label. Both options fail open (render by tag)
and share one cost: an installer who sets `image.tag` explicitly to the chart's own release gets
pinned, and loses the pin on the next chart upgrade without moving the tag, so the data tiers roll
once with identical content.

- **A′ - guard on a stamped `image.releaseTag` (recommended).** `eq $tag .Values.image.releaseTag`.
  One values line and one `sed` more than A′v. Under `--reuse-values` the old tag, release tag and
  digest travel together, so the old pin stays with no roll (rendered: `…:1.13.1@sha256:6a6b1d7d…`
  under a 1.13.2 chart).
- **A′v - guard on `.Chart.AppVersion`.** `eq $tag .Chart.AppVersion`. Under `--reuse-values` the
  old tag no longer equals the new `AppVersion`, the pin is dropped silently and the data tiers
  roll with identical content (rendered: `…:1.13.1`). Under Flux both behave the same.

A′ is the only form whose pin survives `--reuse-values`, which not every installer of a published
chart avoids, for two lines more.

**Answer:** _open_

### Q2: Is the ADR 0033 D5 title a false statement about the operator image?

The title says images are "by default pinned by digest"; the body of D5 pins only the exporter by
default. Read literally, rule 1 sets `urgency: now` and required change 1 applies; read as scoped
by its body, it is no false statement, change 1 is dropped and urgency is `later` (rule 4).

- **Literal (recommended).** Correct the title; a reader of the title alone is misled today.
- **Scoped by its body.** Drop change 1, set urgency `later`.

**Answer:** _open_

## Not verified

- Whether an arm64 Kind node loaded with the amd64 image resolves a `tag@<index digest>` reference
  without pulling; matters for the local fleet-upgrade e2e once it starts from a stamped release
  (fallback: `--set image.releaseDigest=` at `fleet_upgrade_test.go:226-234`). A local arm64 run
  settles it.
- Whether GitHub reuses the `build` job's outputs on "re-run failed jobs"; a test workflow re-run
  with `--failed` settles it. Either way a stamp names only a digest the same run pushed.
- Helm 4 `--reuse-values` semantics (CI packages with Helm v4.3.0, `build.yml:153`; all renders
  used v3.21.3).

## Related

- T58 - the render matrix that takes the rows of change 4, plus a malformed-release-value twin.
- T54 - rephrases the ADR 0033 D5 body; change 1 here covers its title.
- T47 - under its option C the hook drops out of the tag-pinned consumers of the operator image.
- T50 - its argument that sidecar and observer need no ACL user of their own holds only against a
  compromise of the operator image, which this ticket hardens.
