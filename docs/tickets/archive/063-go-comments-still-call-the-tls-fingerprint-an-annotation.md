---
id: T63
title: Go comments still call the TLS fingerprint an annotation, and one says the data pod has two init containers
state: done
severity: cosmetic    # comments only; no behaviour depends on them
security: none
urgency: now          # rule 1: measured-false statements in tracked files
effort: XS
filed-from: review of the XS text corrections of 2026-09-27 (the secretConcernsValkey comment fix handed back three more sites of the same shape, the text-vs-code review a fourth)
opened: 2026-09-27
decided:              # not recorded - no decision was needed (correct in place)
done: 2026-09-27
---

Filed and closed on 2026-09-27. Read at `4a7543e` plus the working tree of
`chore/maintenance-2026-09-27`.

## Fact

**Verified** (by reading, 2026-09-27):

- Since [ADR 0031](../../adr/0031-a-record-the-operator-trusts-lives-in-pod-spec.md) the TLS
  material fingerprint is recorded in the pod spec as the `VKO_TLS_MATERIAL_HASH` env var
  ([`tls_material.go:22`](../../../internal/builder/tls_material.go#L22), written by
  `StampTLSMaterialHash` at `:79`). `RecordedTLSMaterialHash` (`:119-130`) reads the env var
  first and falls back to the legacy `vko.gtrfc.com/tls-material-hash` annotation, which is read
  and never written. Three comments still described the annotation as the record:
  - [`rolling_update.go:420-424`](../../../internal/controller/rolling_update.go#L420-L424)
    (`podNeedsUpdate`): "a pod without the annotation is unmeasured". The check it describes,
    `podTLSMaterialHashChanged` (`:478`), reads through `RecordedTLSMaterialHash`, so a pod with
    the env var and no annotation is measured.
  - [`tls_material.go:47-49`](../../../internal/builder/tls_material.go#L47-L49)
    (`ComputeTLSMaterialHash`): "no annotation is written, and a pod without the annotation is
    never restarted for it". Nothing writes the annotation any more.
  - [`valkey_types.go:236-238`](../../../api/v1/valkey_types.go#L236-L238)
    (`ConditionTypeTLSMaterialStale`): "only for pods that already carry the fingerprint
    annotation".
- [`test/imagetools/image_tools_test.go:7`](../../../test/imagetools/image_tools_test.go#L7)
  said "both init containers are scripts". Data pods now run up to three init containers (the
  config writer, the `check-data-writable` pre-flight and the `fix-data-ownership` repair,
  [ADR 0032](../../adr/0032-generated-pods-run-rootless.md)), and every one of them, like the
  Sentinel config writer, runs `sh -c`. The same claim was corrected in
  `internal/builder/image_requirements.go` earlier the same day.
- `grep -rn -i 'fingerprint annotation\|without the annotation\|both init containers'
  --include='*.go' .` found these four and three unrelated hits about other annotations
  (`sidecar/drain.go:205` drain stamp, `builder/sentinel.go:87` known-master,
  `builder/configmap.go:291` config hash), which are true and were left alone.

**Not verified:** nothing was run for this ticket beyond the greps; the comment edits ride the
`make vet`, `make lint` and `make test-unit` run of the same change.

## Impact

Cosmetic. A reader of `podNeedsUpdate` or of the condition's doc comment would conclude that a
pod created since ADR 0031, which carries only the env var, is unmeasured, which is the opposite
of what the code does.

## Decision

None needed: correct in place.

## Verification

- The grep above prints only the three unrelated hits.
- `gofmt -l internal api test cmd` prints nothing.
- The `ConditionTypeTLSMaterialStale` comment is on a `ConditionType` constant and does not reach
  the generated CRD; `make manifests` leaves `config/crd/bases` and the chart CRD unchanged.

## History

- 2026-09-27: **done** - shipped the four corrected comments (`rolling_update.go` `podNeedsUpdate`,
  `builder/tls_material.go` `ComputeTLSMaterialHash`, `api/v1/valkey_types.go`
  `ConditionTypeTLSMaterialStale`, `test/imagetools/image_tools_test.go` package comment).
  **Close ([ADR 0034](../../adr/0034-tickets-are-work-lists-that-get-archived.md)):** nothing
  durable to extract - ADR 0031 already states where the record lives, and the corrections decide
  nothing. `git grep` of `T63` and `063-` outside `docs/tickets/`: no citation. Filed directly
  into `archive/` because it was filed and closed in the same change.
- 2026-09-27: filed from the review of the day's XS text corrections. The first three sites were
  handed back by the fix of the same claim in `secretConcernsValkey`
  (`internal/controller/valkey_controller.go`); the imagetools comment came from the text-vs-code
  review.
