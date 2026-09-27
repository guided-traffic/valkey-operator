---
id: T52
title: the sidecar and the data pod's init containers state no resources, so a cpu/memory ResourceQuota refuses the data pods
state: filed
severity: low         # a quota namespace cannot host a data tier at all; nothing else breaks
security: hardening
threat: "would additionally cover namespaces that use a cpu/memory ResourceQuota as a tenancy control: today the data pods are refused there, so such a namespace cannot host a Valkey data tier unless the quota is loosened"
urgency: icebox       # rule 5: reopens ADR 0033 D7 and the alternative its Alternatives section rejected by name
effort: M             # was S until 2026-09-27: two fields are S, the quota e2e with a negative control, CRD regeneration and the ADR 0033 amendment make it M
blocked-by: decision  # ADR 0033 D7, see Options
filed-from: T31, section "Further security measures — not in this change, each open" (archive/031)
opened: 2026-09-27
decided:              # not recorded - no decision yet
done:                 # not recorded - not done
---

Filed on 2026-09-27 from the fifth row of the table
["Further security measures — not in this change, each open"](archive/031-generated-pods-run-as-root.md#further-security-measures--not-in-this-change-each-open)
in the archived ticket 031, which is `done` and tracks none of its rows. The row itself notes
"ADR 0033 D7 (decided: no defaults)". The operator-facing statement is gap
[H-18](../security/workload-pod-posture.md#h-18).

## Fact

**Verified** (read 2026-09-27):

- In [`statefulset.go`](../../internal/builder/statefulset.go) only the `valkey` container
  (line 871, `v.Spec.Resources`) and the exporter (lines 1124–1125, `spec.metrics.resources`)
  are given container resources. `buildSidecarContainer` (line 894) sets none, and neither do
  the data pod's init containers — `init-config-selector` (built at lines ~~274 and 435~~
  *(corrected 2026-09-27: 275 and 436)*) and the
  containers of [`pod_security.go`](../../internal/builder/pod_security.go), which contains no
  `Resources` at all (`dataWritableCheck`, line 176; `WithDataOwnershipRepair`, line 247).
- [ADR 0033](../adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md)
  D7: a cpu/memory `ResourceQuota` admits a pod without pod-level resources only when every
  container, init containers included, states the values (read in Kubernetes v1.36.4); the
  sidecar and the data pod's init containers deliberately keep stating none, because a limit
  guessed too low OOM-kills the process that holds the drain promotion
  ([ADR 0012](../adr/0012-the-sidecar-records-its-drain-promotion-on-the-pod.md)).
- ADR 0033's residual risks: pod-level `spec.resources` (`PodLevelResources`, on by default
  since Kubernetes 1.34, read in v1.36.4) exempts a pod from the per-container quota check. The
  [README](../../README.md) declares Kubernetes v1.29+.

*Added 2026-09-27 (enrichment, at `4a7543e`):*

- **ADR 0033 already rejected option B by name.** Its *Alternatives Considered* (ADR 0033
  lines 484–485) read: "Fields for the sidecar and the init containers as well. More API surface
  with no known user." Lines 481–483 reject D (measured defaults), and lines 711–713 record
  pod-level resources (C) as "not weighed in D7". Choosing B therefore overturns a recorded
  rejection, and the reason to overturn it is a known user. This ticket names none.
- The existing resource fields sit at [`valkey_types.go`](../../api/v1/valkey_types.go) lines
  510 (Sentinel), 671 (metrics), 973 (observer) and 1072 (`spec.resources`). D7's Sentinel
  pattern mirrors one field onto every Sentinel container, init included
  ([`sentinel.go`](../../internal/builder/sentinel.go) lines 398–401). The README states the
  v1.29 floor at line 198.
- **An unset field rolls nothing, and a written value rolls the tier.** The pod-spec hash is
  FNV-32a over the JSON of the whole built `PodSpec` (`ComputePodSpecHash` and `podSpecDigest`,
  `statefulset.go` lines 1228–1239). Container `resources` is serialised today as an empty
  object, so a new field left unset leaves the JSON, and the hash, unchanged. A value newly
  written into any container changes the hash and rolls the tier.
- `k8s.io/api` v0.37.1 carries `PodSpec.Resources`, but its doc comment still calls the field
  alpha behind the `PodLevelResources` gate. This is at odds with ADR 0033's "on by default
  since 1.34", which was not re-checked here (see Not verified).

**Not verified:**

- The sidecar's memory and CPU under a drain promotion; nobody has measured them.
- What an API server below 1.34 does with pod-level resources (drops or refuses the field).
- *(added 2026-09-27)* The feature-gate default of `PodLevelResources` per Kubernetes version
  (the `k8s.io/api` doc comment and ADR 0033 disagree, above). Also whether a silent drop would
  need the same read-back that `writeWorkload`
  ([`pod_hardening.go`](../../internal/controller/pod_hardening.go) line 54) does for
  `hostUsers`.

## Impact

Every namespace with a cpu/memory `ResourceQuota`: the data StatefulSet is written, and its
pods are refused at admission. What an operator can do today is set every field that exists —
`spec.resources`, `spec.metrics.resources`, `spec.observer.resources`,
`spec.sentinel.resources` — which admits the Sentinel and observer pods, and host the data tier
in a namespace without such a quota.

## Options

The decision is whether to reopen ADR 0033 D7.

- **A — keep D7 (recommended, until a user is named).** No fields, no defaults; quota
  namespaces cannot run the data tier.
- **B — explicit fields, no default.** ~~(best)~~ A resources field for the sidecar and one for
  the data pod's init containers (names to be decided); unset means no requests and no limits,
  as today, so the upgrade changes nothing
  ([ADR 0005](../adr/0005-upgrade-neutral-defaults-and-anti-affinity.md) D1). D7's reason
  stays intact: nothing is guessed, the CR author sets a value they measured. *(added
  2026-09-27)* This is the alternative ADR 0033 rejected for "no known user" (Fact). Unset
  leaves the hash unchanged (Fact).
- **C — pod-level `spec.resources`.** One field, and a pod carrying it is exempt from the
  per-container check. Needs Kubernetes 1.34 while the README declares 1.29. *(added
  2026-09-27)* Below the gate it may be dropped silently, like `hostUsers`, and would then need
  the `writeWorkload` read-back (Not verified).
- **D — defaults for the sidecar and the init containers.** The alternative D7 rejected.
- *(added 2026-09-27)* **E — mirror `spec.resources` onto the data init containers**, D7's
  Sentinel pattern. No new field. But it does not cover the sidecar, so the quota still
  refuses the pod. It also writes a value into every data tier that sets `spec.resources`,
  which rolls those tiers on the upgrade, against ADR 0005 D1 (Fact).

~~B is marked because it works on every Kubernetes version the project supports and keeps D7's
reason. C is the better option once the floor reaches 1.34, because one field covers every
present and future container.~~ *(superseded as the recommendation 2026-09-27)*

A is marked because ADR 0033 rejected B for "no known user" on 2026-09-26, and nothing has
changed since: this ticket names no quota-namespace tenant. **If Hans names one, B is the
option**: it works on the v1.29 floor, keeps D7's no-guess reason, and rolls nothing while
unset. C becomes the better option once the floor reaches 1.34, because one field covers
every present and future container. E loses because it cannot admit the pod at all.

## Work list

**Not waiting on a decision:** nothing. The one open question, whether a concrete
quota-namespace tenant exists, *is* the decision.

**Waiting on the decision (B):**

1. The fields (names to be decided) in `api/v1/valkey_types.go`.
2. The wiring at `statefulset.go` line 894 (sidecar) and lines 275 and 436
   (`init-config-selector`), and `pod_security.go` lines 176 and 213 (`check-data-writable`,
   `fix-data-ownership`).
3. A unit test that pins an unchanged pod template and hash while the fields are unset.
4. `make generate-all` and the README CRD reference.
5. The quota e2e below.

**Close (ADR 0034):** amend ADR 0033 D7, and mark the Alternatives entry at lines 484–485
superseded in place. Then H-18 and
[compute resources](../operations/compute-resources.md). `git grep -n 'T52\|052-the-sidecar'`
outside `docs/tickets/` (none today), then move to `archive/`.

## Decision

None yet.

## Verification

- e2e in a namespace with a cpu/memory `ResourceQuota`: with the new fields and the existing
  ones set, the data pods are admitted and the cluster reaches `OK`; with the new fields unset,
  the data pods are refused (the negative control of
  [ADR 0017](../adr/0017-test-and-ci-policy.md) D11).
- A unit test pins that an unset field yields a pod template identical to today's.

## History

- 2026-09-27 — enriched - corrected the `init-config-selector` lines, and recorded that ADR 0033
  Alternatives (lines 484–485) already rejected B for "no known user". Added the pod-spec-hash
  fact (unset rolls nothing) and option E (mirror), and moved the recommendation from B to A
  until a quota user is named.
- 2026-09-27 — effort `S` → `M`. Two fields and their wiring are S. The Verification this
  ticket already required (a ResourceQuota e2e with a negative control), CRD regeneration and
  the ADR 0033 amendment make it M. Urgency unchanged (`icebox`, rule 5).
- 2026-09-27 — filed from the row "Sidecar / init container requests" of archive/031. Gap
  [H-18](../security/workload-pod-posture.md#h-18) states what is missing and what to set in the
  meantime.
