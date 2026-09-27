---
id: T53
title: the release pipeline does not stamp the digest of the image it pushes into the chart
state: filed
severity: low         # the default install runs by tag; pinning is available to every installer today
security: hardening
threat: "would additionally cover whoever can move the operator's image tag on Docker Hub: today a default install runs the operator, its pre-upgrade hook and, through --operator-image, every sidecar and observer by tag, so that principal decides what runs under the operator's cluster-wide grant"
urgency: later        # rule 4: cheap known fix
effort: S
blocked-by: decision  # which stamp, below
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

**Verified** (read 2026-09-27):

- [`build.yml`](../../.github/workflows/build.yml) runs on a published release. Its `build` job
  pushes the image with `docker/build-push-action` (step `id: build`, line 78, `push: true`)
  and declares no job `outputs:`.
- Its `release-helm-gh` job (`needs: build`, line 129) stamps the chart version, the
  `appVersion` and `image.tag` with `sed` (lines 179–182) and packages the chart; nothing stamps
  `image.digest`, which stays `""` ([`values.yaml`](../../deploy/helm/valkey-operator/values.yaml)
  line 14).
- When `image.digest` is set, the chart renders `repository:tag@digest` for the operator, the
  hook, `--operator-image` and `OPERATOR_IMAGE`
  ([ADR 0033](../adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md)
  D5). Setting it changes `--operator-image` and so rolls the data tiers as any operator image
  change does; set in the same upgrade that moves the tag it costs nothing extra (ADR 0033
  *Consequences*). Every release moves the tag.
- *(Added 2026-09-27, re-read at `4a7543e`.)* Locations: the push step is
  [`build.yml:76-97`](../../.github/workflows/build.yml) (`platforms: linux/amd64` at line 86,
  `provenance`/`sbom` at 94-95, an explicit `outputs:` entry at 97). The stamp lines are
  `build.yml:179-182`. `values.yaml` has exactly one `  repository:`, `  tag:` and `  digest:`
  line (6, 9, 14), so a `sed` stamp of `  digest:` is unambiguous. The helper is
  [`_helpers.tpl:68-77`](../../deploy/helm/valkey-operator/templates/_helpers.tpl), used at
  [`deployment.yaml:34, 39, 49`](../../deploy/helm/valkey-operator/templates/deployment.yaml)
  and [`pre-upgrade-job.yaml:34`](../../deploy/helm/valkey-operator/templates/pre-upgrade-job.yaml).
  The latest release is `v1.13.1`, and only a docs commit follows it.
- *(Added 2026-09-27.)* **The helper appends the digest to whatever repository and tag are
  set** (`_helpers.tpl:69-75`). A digest in the published chart's `image.digest` therefore
  outlives an installer's override. `--set image.tag=X` renders `repository:X@<release digest>`,
  and `--set image.repository=<mirror>` renders `<mirror>:<version>@<Docker Hub digest>`.
- *(Added 2026-09-27.)* The fleet-upgrade e2e installs its starting release from the published
  chart without an image override
  ([`fleet_upgrade_test.go:226-234`](../../test/e2e/fleet_upgrade_test.go)), and upgrades to the
  local chart, whose digest stays `""`, with repository and tag set (lines 319-326). A stamp
  reaches that e2e only once `E2E_UPGRADE_FROM` ([`Makefile:167`](../../Makefile), `1.10.48`)
  names a stamped release.

**Not verified:**

- That the `digest` output of `docker/build-push-action` with `provenance: true` and
  `sbom: true` is the digest a pull of `tag@digest` resolves (expected: the pushed index).
  *(Added 2026-09-27.)* Nor how the explicit `outputs:` entry at `build.yml:97` combines with
  `push: true` in naming and reporting the pushed image; it was not traced.
- *(Added 2026-09-27.)* What a runtime does with `repository:X@D` when the tag X names another
  image (expected: pulls D and ignores X), and whether any installer mirrors the image with a
  tool that keeps the index digest (expected: `crane copy` or `skopeo copy --all` keep it, a
  single-platform `docker pull`/`push` does not). Neither was measured.
- *(Added 2026-09-27.)* Whether a Kind node on arm64, loaded with the amd64 image by
  `kind load`, can resolve a `tag@<index digest>` reference without pulling. This matters for the
  local fleet-upgrade e2e once its starting release is a stamped one.

## Impact

Every install that keeps the chart default. An installer can pin today by setting
[`image.digest`](../operations/installation.md#imagedigest).

## Options

- **A — pass the digest through a job output and stamp it next to the tag ~~(best)~~.** Export
  `steps.build.outputs.digest` as an output of the `build` job and set `image.digest` in
  `release-helm-gh` the same way `image.tag` is set. The committed `values.yaml` keeps `""`,
  like the tag it stamps only in the release checkout. *(Added 2026-09-27.)* Cost beyond the
  diff: the stamped value is `image.digest` itself, so it survives every override (Fact). An
  installer who sets `image.tag` is expected to get the release image under another tag,
  silently. One who mirrors through `image.repository` is expected to fail the pull on the first
  upgrade, unless the mirror kept the index digest (both runtime behaviours are Not verified).
  The way out, `--set image.digest=""`, has to be documented.
- **A′ — the same job output, stamped into a release-only value the helper applies only while
  repository and tag are the published ones** *(added 2026-09-27)*. For example
  `image.releaseDigest`, used when `image.digest` is empty, `image.repository` is
  `guidedtraffic/valkey-operator` and the tag resolves to `.Chart.AppVersion`. An explicit
  `image.digest` still wins, and every override behaves as it does today. Cost: one chart value,
  about six lines in `_helpers.tpl:68-77`, render rows (T58), and a README values row. The
  simpler guard, ignoring `image.digest` when the tag was overridden, is rejected: it would drop
  an installer's explicit pin whenever they also set a tag.
- **B — commit the digest back to `main` after each release.** A bot commit per release, and
  `main`'s `values.yaml` carries the previous release's digest in between.
- **C — keep leaving it to the installer.**

A is marked because it is the smallest change, uses the mechanism the tag already uses, and
makes the chart users install pin by default.

*(Re-weighed 2026-09-27; the mark above is superseded, not deleted.)* **A′ (recommended).** It
pins exactly the population gap [H-12](../security/operator-pod-posture.md#h-12) is about, the
default install. It changes nothing for anyone who overrides repository or tag, and A cannot
promise that because `_helpers.tpl:69-75` appends the digest to any reference. Neither A nor A′
adds a roll: every release already moves the tag in `--operator-image`, so pinning inside the
release that ships it costs nothing extra (ADR 0033 *Consequences*). B commits to `main` on every
release, and C leaves H-12 open.

## Decision

None yet.

## Work list

No item here is both XS and free of the decision above.

1. *(waits on the decision; needed by A and A′)* `build` job: `outputs: digest:
   ${{ steps.build.outputs.digest }}`. `release-helm-gh`: check the format
   (`^sha256:[0-9a-f]{64}$`) and fail the release otherwise, then stamp it next to
   `build.yml:182`.
2. *(A′ only)* The release-only value in `values.yaml` (committed empty), the guard in
   `_helpers.tpl:68-77`, and render rows for default, tag override, repository override and
   explicit digest.
3. Close ([ADR 0034](../adr/0034-tickets-are-work-lists-that-get-archived.md)): the decision
   into ADR 0033 (its residual risk at lines 645-646 amended), gap H-12
   (`operator-pod-posture.md:71-77`) rewritten, [`installation.md#imagedigest`](../operations/installation.md#imagedigest)
   (lines 59-68) and the `README.md:550` values row describing the stamped default and what an
   override does, `git grep` `053` and `T53`, then move to `archive/`. Done-ness needs the next
   real release (Verification).

## Verification

- For the next release: `image.digest` in the published chart equals the digest
  `docker buildx imagetools inspect guidedtraffic/valkey-operator:<version>` reports.
  *(2026-09-27: under A′, read the release-only value instead.)*
- A Kind install from the published chart runs the operator pod with an image reference that
  ends in that digest, and a new data pod's sidecar image carries it too.
- *(Added 2026-09-27, A′ only.)* The same install with `--set image.tag=<previous version>`
  runs that version's image by tag, with no digest in the reference.

## History

- 2026-09-27: enriched - re-verified at `4a7543e` and added the locations. Found that the helper
  appends a stamped digest to any overridden reference, so added A′ (guarded stamp) and marked it
  recommended over A. Added a work list and `blocked-by: decision`. Urgency (`later`, rule 4:
  only the verification waits for a release) and effort (S) unchanged.
- 2026-09-27 — filed from the row "Release pipeline stamps the pushed digest into the chart" of
  archive/031, which names ADR 0033's residual risks. Gap
  [H-12](../security/operator-pod-posture.md#h-12) states what is missing and how an installer
  pins in the meantime.
