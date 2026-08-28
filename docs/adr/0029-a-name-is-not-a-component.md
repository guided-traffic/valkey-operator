# ADR 0029: A name is not a component — the tier is passed, never parsed

## Status

Accepted. Date: 2026-08-26.

Implemented: the name-sniffing `podAddress` is gone from
[`internal/health/checker.go`](../../internal/health/checker.go); all four of its call sites
address a pod through a helper that pairs the headless Service with the matching port.

Not changed: the exported `PodAddressForComponent`, which already takes the component
explicitly and whose 19 call sites in `internal/controller` were audited and all pair
component and port correctly.

## Context

`internal/health` addressed a pod by guessing which tier it belongs to from its name:

```go
component := common.ComponentValkey
// Detect sentinel pods by name suffix.
if len(podName) > 9 && podName[len(podName)-10:len(podName)-2] == "sentinel" {
    component = common.ComponentSentinel
}
```

The window is a fixed offset from the end — eight characters ending two before the last one.
It answers correctly only by coincidence of two things it never looks at: the width of the
ordinal, and whether the cluster name itself ends in `sentinel`. Each is wrong on its own, in
opposite directions, and in one combination the two errors cancel:

| Pod | Predicate | Outcome |
|---|---|---|
| `<cr>-i`, `<cr>` does not end in `sentinel` | false | correct |
| `<cr>-sentinel-i`, `i` in 0..9 | true | correct |
| `<cr>-sentinel-i`, `i` >= 10 | false — window is `entinel-` | **Sentinel pod dialled through the data Service** |
| `<cr>-i` where `<cr>` ends in `sentinel`, `i` in 0..9 | true — window is `sentinel` | **data pod dialled through the Sentinel Service** |
| `<cr>-i` where `<cr>` ends in `sentinel`, `i` >= 10 | false | correct — the two errors cancel |

**Class A — the CR name.** `metadata.name` is chosen by whoever may `create valkeys`. A CR
named `term-no-sentinel` produces the data pod `term-no-sentinel-0`, whose last ten characters
are `sentinel-0`, so every data pod with a single-digit ordinal — on the usual `spec.replicas`
of 1 to 5, every data pod of the cluster — was addressed through `<cr>-sentinel-headless`.

**Class B — the ordinal.** `spec.sentinel.replicas` carries `+kubebuilder:validation:Minimum=1`
and **no Maximum** ([`api/v1/valkey_types.go:274-276`](../../api/v1/valkey_types.go)); the only
two `Maximum` markers in the CRD are on `metrics.port` and `observer.db`. From `replicas: 11`
the Sentinel pod `<cr>-sentinel-10` was addressed through the data Service.

Neither misroute reaches a wrong Valkey. For `<cr>-0.<cr>-sentinel-headless` to resolve, some
CR would have to publish the hostname `<cr>-0` under that Service, and the only two CRs that
can own that Service name (`<cr>`'s Sentinel tier, `<cr>-sentinel`'s data tier) both publish
`<cr>-sentinel-*`. **Not verified in a cluster** — this is read off the naming scheme in
[`internal/common/labels.go:131-144`](../../internal/common/labels.go) and headless-Service
DNS semantics, not measured. The damage is therefore blindness, not misdirection.

Blindness is not uniform, and that is what made it expensive:

- **Loud (Sentinel disabled).** `PingPod` → `verifyValkeyConnectivity` → `phase: Error`,
  message `Instance unreachable: …`. Visible, wrong-looking, actionable. With Sentinel enabled
  this path is never reached; the same reachability failure arrives through `CheckCluster` →
  `findMaster` and reads `Cluster health check failed: no master found among N pods`.
- **Silent.** `GetReplicationInfo` and `findMaster` read an unreachable pod as *not the
  master*. `collectPodStates` then falls back to the pod's `instanceRole` label
  ([`internal/controller/rolling_update.go:1651`](../../internal/controller/rolling_update.go),
  through `labelClaimsMaster` at
  [`:1552`](../../internal/controller/rolling_update.go)), so for such a cluster the stale
  label becomes the **only** input on who the master is. That fallback is written for a
  transient failure — `labelClaimsMaster` already refuses a pod carrying a `DeletionTimestamp`
  on the "silence is not evidence" rule ([ADR 0011](0011-evidence-based-steady-state-split-brain-resolution.md), D6)
  — and a Class A cluster turned it into the permanent and only input, on pods that are alive
  and simply unaddressable.

The defect shipped on 2026-02-17 in `88b721b`, the first Helm/E2E commit. On **2026-08-21**
`34c351c` pinned it as a characterization test that asserts the wrong addresses with the
message `"BUG (documented, not fixed)"`, naming the fix in its own doc comment. On
**2026-08-25** `360cb03` added `TestE2E_RollingUpdate_NoSecondDeleteWhileAPodTerminates`,
which derives its cluster name as `"term-" + suffix` over the subtests `sentinel` and
`no-sentinel` — landing both legs squarely inside the documented failure window. Both e2e
legs went red, four days after the bug was written down and six months after it was written.

## Decision

**D1 — The tier a pod belongs to is never derived from its name.** Not by suffix, not by
prefix, not by slicing, not by splitting on `-`. The caller knows which tier it is addressing
and says so. A name is user input reflected back; it carries no structure the operator may
rely on.

**D2 — The headless Service and the port are chosen together, in one expression.**
`valkeyPodAddress` pairs `common.ComponentValkey` with `builder.ServicePort(v)`;
`sentinelPodAddress` pairs `common.ComponentSentinel` with `sentinelPort(v)`. Both live in
[`internal/health/checker.go`](../../internal/health/checker.go). A Sentinel Service with a
Valkey port resolves to nothing and so does the converse, so the pairing is not a convention
to remember — it is the only shape the helpers can produce.

**D3 — `PodAddressForComponent` stays, and whoever calls it owns the pairing.** It is the
exported entry point and already takes the component explicitly. Inside `internal/health` the
paired helpers are used instead. Its doc comment states the obligation.

**D4 — A guard that exists only to make a guess safe is a sign the guess is wrong.** The
removed predicate needed `len(podName) > 9` purely so the slice would not panic. The length
check protected the process and not the answer; nothing in the fix needs it, because nothing
reads the name.

**D5 — A characterization test that asserts a bug is a debt with a due date, not a
resolution.** Pinning current behaviour is a legitimate step, and `34c351c` did it well: the
assertion message said `BUG (documented, not fixed)` and the doc comment named the fix. What
it did not do is stop anything from being built on top. Four days later an e2e test picked
names inside the documented window and paid for it. Where a defect is pinned rather than
fixed, the pin belongs in a ticket in the same change, so it is on a list somebody reads.

## Consequences

- The unit suite was green throughout the defect's six months, but for two different reasons
  in sequence. For the first six months nothing exercised a name inside the failure window at
  all — the defect was simply unreached. For the last four days, three tests reached it and
  asserted the wrong addresses as expected values. Only the second period is a tripwire, and
  it worked exactly as its author intended: fixing the code *required* editing those tests, so
  the fix could not land quietly.
- `TestPodAddress_SentinelDetectionIsPositional` became
  `TestPodAddress_ComponentIsNeverDerivedFromTheName`, growing from four rows to ten: the two
  previously-wrong rows were corrected and six added (a cluster named exactly `sentinel`, a
  two-digit data ordinal, a single-character cluster name, a Sentinel pod of a `-sentinel`
  cluster, and one row per tier on the TLS port).
  `TestFindMaster_ClusterNameEndingInSentinelIsMisrouted` and
  `TestCheckSentinel_DoubleDigitOrdinalIsMisrouted` lost the word `Misrouted` from their
  names along with the behaviour it described.
- Four small tests in `checker_test.go` that exercised `podAddress` with a hand-passed port
  were removed; every case they covered is a row of the table above, and the one case that
  was not — the shortest legal cluster name — was added as a row.
- The e2e cluster names `term-sentinel` and `term-no-sentinel` are **deliberately left as
  they are**. They are the only *e2e* fixtures whose CR name collides with a component name;
  the unit tier now covers the same collision in
  `TestPodAddress_ComponentIsNeverDerivedFromTheName`,
  `TestPodAddress_TheTwoTiersNeverShareAnAddress` and
  `TestFindMaster_ClusterNameEndingInSentinelStillUsesTheDataService`, so renaming them would
  not lose the property. It would lose the only fixture that reaches it against a real
  cluster, which is where it was found. (`internal/builder` has colliding names too —
  `annotations_test.go`, `drain_signal_test.go` — but neither drives a component-derivation
  path.)
- `findMaster` used to hoist the port above its goroutine loop; the paired helper computes it
  inside each goroutine instead, so `builder.ServicePort(v)` is now evaluated once per pod
  rather than once per call. It is a pure read of `v.Spec.TLS` with no writer in that scope —
  `go test -race ./internal/health/... -run TestFindMaster` passes — so this is N cheap reads,
  not a race. Named because it is a real change the pairing costs, not because it bites.
- The Sentinel port selection (`SentinelPort` / `SentinelTLSPort` by `IsTLSEnabled`) is now
  a named function here, but it is still open-coded four times in
  `internal/controller/rolling_update.go` (`:1460`, `:2915`, `:2990`, `:3055`). Those four
  pair their component correctly today; consolidating them was left out as scope.

## Alternatives Considered

**Fix the predicate.** Anchor it as `strings.HasPrefix(podName, common.StatefulSetName(v,
common.ComponentSentinel)+"-")`, which is exact for both classes. Rejected: it keeps the
answer derived from the name, so the next question asked of a name — which cluster, which
role, which ordinal — gets answered the same way. The bug class survives its instance.

**Add the component to the `InstanceChecker` interface.** Thread it through `PingPod` and
`GetReplicationInfo` so the compiler forces every caller to state it. Rejected as
disproportionate: all 15 production callers were audited and every one passes a data-tier
name, and both methods already select the Valkey port, so the tier was never actually in
question at those two seams — only the address construction was. It would have touched 15
call sites and every fake in the controller tests to encode something the port already fixes.

**Validate the CR name.** Refuse a `metadata.name` ending in `sentinel`. Rejected twice over:
it is not enforceable — ADR 0015 allows schema validation only, and a cross-field or pattern
rule that rejects an existing cluster's name breaks it on upgrade — and it treats a naming
collision as the user's error when it is the operator's parsing that is wrong.

**Leave it and rename the e2e clusters.** Rejected: it makes the symptom go away, keeps the
defect for every user who names a CR `foo-sentinel`, and discards the only coverage that
found it.

## Residual risks

- **The NXDOMAIN reasoning is analytical, not measured.** No cluster was used to confirm that
  a misrouted address never resolved to a live pod. If it ever could, the historical severity
  of this defect is higher than "blindness" and would need re-examination. The fix makes the
  question moot going forward.
- **`PodAddressForComponent` can still be called with a mismatched port.** D3 assigns that
  obligation to the caller rather than removing the possibility. The 19 current call sites
  were audited by hand and all pair correctly; nothing tests that they stay that way.
- **The four open-coded Sentinel port selections in `rolling_update.go`** remain a place where
  a future edit can separate port from component. Named, not fixed.
- **No test asserts that no code derives a component from a name.** D1 is a rule enforced by
  review, not by a linter. `TestPodAddress_ComponentIsNeverDerivedFromTheName` fails if the
  helpers regress, which was verified by reintroducing the old predicate on each side in turn
  and observing the failures, but a *new* guess elsewhere would not be caught.
- **Not verified:** whether any cluster in the field is currently named such that it hit
  Class A. The symptom is searchable in existing status, but **the string depends on whether
  Sentinel is enabled**, and searching for only the obvious one misses half the fleet. Without
  Sentinel, `updateStatus` reaches `updateStandaloneStatus` and the cluster shows `phase: Error`
  with `Instance unreachable: …`
  ([`valkey_controller.go:2074`](../../internal/controller/valkey_controller.go)). With
  Sentinel, `updateStatus` branches to `updateHAStatus` at `:2055` and never calls
  `verifyValkeyConnectivity` at all, so the same blindness surfaces as
  `Cluster health check failed: no master found among N pods` (`:2296`, from `findMaster`,
  [`internal/health/checker.go:242`](../../internal/health/checker.go)) — which is the shape
  the e2e leg `term-sentinel` took. Neither search was run.

## References

- [`internal/health/checker.go`](../../internal/health/checker.go) — `valkeyPodAddress`,
  `sentinelPodAddress`, `sentinelPort`, `PodAddressForComponent`
- [`internal/health/checker_paths_test.go`](../../internal/health/checker_paths_test.go) —
  `TestPodAddress_ComponentIsNeverDerivedFromTheName`,
  `TestPodAddress_TheTwoTiersNeverShareAnAddress`,
  `TestFindMaster_ClusterNameEndingInSentinelStillUsesTheDataService`,
  `TestObserveSentinels_DoubleDigitOrdinalUsesTheSentinelService`
- [`internal/common/labels.go`](../../internal/common/labels.go) — `StatefulSetName`,
  `HeadlessServiceName`, the naming scheme both tiers derive from
- [`test/e2e/pod_termination_test.go`](../../test/e2e/pod_termination_test.go) — the e2e legs
  whose cluster names exposed the defect
- [ADR 0015](0015-one-crd-validated-by-schema-only.md) — why a name cannot be validated at
  admission
- [ADR 0020](0020-write-only-what-the-operator-owns.md) — the sibling rule on the write side:
  a label is not a proof, provenance is checked rather than inferred
- [ADR 0011](0011-evidence-based-steady-state-split-brain-resolution.md) D6 — "silence is not
  evidence", the rule `labelClaimsMaster` applies and that this defect made load-bearing
