---
id: T53
title: the release pipeline does not stamp the digest of the image it pushes into the chart
state: filed
severity: low         # the default install runs by tag; pinning is available to every installer today
security: hardening
threat: "would additionally cover whoever can move the operator's image tag on Docker Hub: today a default install runs the operator, its pre-upgrade hook and, through --operator-image, every sidecar and observer by tag, so that principal decides what runs under the operator's cluster-wide grant"
urgency: later        # rule 4: cheap known fix
effort: S
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

**Not verified:**

- That the `digest` output of `docker/build-push-action` with `provenance: true` and
  `sbom: true` is the digest a pull of `tag@digest` resolves (expected: the pushed index).

## Impact

Every install that keeps the chart default. An installer can pin today by setting
[`image.digest`](../operations/installation.md#imagedigest).

## Options

- **A — pass the digest through a job output and stamp it next to the tag (best).** Export
  `steps.build.outputs.digest` as an output of the `build` job and set `image.digest` in
  `release-helm-gh` the same way `image.tag` is set. The committed `values.yaml` keeps `""`,
  like the tag it stamps only in the release checkout.
- **B — commit the digest back to `main` after each release.** A bot commit per release, and
  `main`'s `values.yaml` carries the previous release's digest in between.
- **C — keep leaving it to the installer.**

A is marked because it is the smallest change, uses the mechanism the tag already uses, and
makes the chart users install pin by default.

## Decision

None yet.

## Verification

- For the next release: `image.digest` in the published chart equals the digest
  `docker buildx imagetools inspect guidedtraffic/valkey-operator:<version>` reports.
- A Kind install from the published chart runs the operator pod with an image reference that
  ends in that digest, and a new data pod's sidecar image carries it too.

## History

- 2026-09-27 — filed from the row "Release pipeline stamps the pushed digest into the chart" of
  archive/031, which names ADR 0033's residual risks. Gap
  [H-12](../security/operator-pod-posture.md#h-12) states what is missing and how an installer
  pins in the meantime.
