# Seccomp profiles

Which seccomp profile the data, Sentinel and observer pods run under, who chooses it, and
the default-deny allow-list of `Localhost` profiles that bounds the choice. The decisions are
[ADR 0033](../adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md) D1 and D9. The rest of the pod posture is
[workload pod posture](workload-pod-posture.md); the operator's own profile is set through
the chart ([operator pod posture](operator-pod-posture.md#the-pod-and-container-fields)).

## `RuntimeDefault` or `Localhost`, never `Unconfined`

([ADR 0033](../adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md)
D1). `spec.podSecurity.seccompProfile` sets the pod-level profile of the data, Sentinel and
observer pods (`GetSeccompProfile`). Omitted, or `type: RuntimeDefault`, is the runtime's default
filter as before; `Localhost` takes `localhostProfile`, a path relative to the kubelet's seccomp
directory. The CRD enforces the enum and, by the first CEL rule this CRD carries, that the path is
set exactly for `Localhost` (envtest refuses `Unconfined`, a `Localhost` without or with an empty
path, and a path with `RuntimeDefault` — `make test-integration`, green 2026-09-26); since later
that day a second CEL rule refuses a path starting with `/` or holding a `..` element (envtest
rows added, ~~no recorded run yet~~ green in repeated runs on 2026-09-26, Kubernetes 1.29 API
server), and the operator writes a `Localhost` profile only when its
allow-list names it, which is empty by default ([the `Localhost` allow-list](#the-localhost-allow-list), ADR 0033 D9). `Unconfined`
is refused because it is the one value that removes the filter: every syscall the kernel offers
becomes reachable from a compromised `valkey-server`, and one CR would take its namespace out of
`restricted`. No Valkey workload is known to need a syscall `RuntimeDefault` blocks, so the escape
hatch buys nothing. `Localhost` exists for clusters that manage their own profiles (the Security
Profiles Operator, say) — a fixed `RuntimeDefault` would have left them a mutating policy, which
the drift comparison would rewrite back on every pass. Changing the profile moves both pod-spec
hashes and rolls the tiers failover-aware; an explicit `RuntimeDefault` hashes like the omitted
field and rolls nothing (`TestPodHardening_OptInsMoveThePodSpecHashes`). Three limits:

- A `Localhost` profile is **node state the operator cannot see**. Missing on a node, it keeps a
  pod scheduled there from starting (~~the e2e asserts that — not yet run~~ measured on Kind
  2026-09-26, before the allow-list existed: a container of the pod waits, and the runtime's
  message names the file; since the allow-list only a listed profile gets that far, and the e2e
  values list the missing one on purpose — measured again with the allow-list in the final run of
  2026-09-26, green on both Valkey lines); a multi-replica or
  Sentinel roll holds on that pod and reports `PodAvailabilityStalled` after
  `spec.rollingUpdate.syncTimeout` (ADR 0026 D11), a single pod is not reported (ADR 0032 D7).
  Too strict, a container fails at a syscall. It must allow every generated container, the
  `chown` of `fix-data-ownership` included.
- It is **only as strict as the file it names**, and every CR author may name ~~any file in that
  directory~~ any file the operator's allow-list names — none by default *(amended 2026-09-26,
  ADR 0033 D9)* ([the `Localhost` allow-list](#the-localhost-allow-list)). Pod Security `restricted` does not tell a permissive `Localhost`
  profile from a strict one, and neither does the allow-list: it compares names, not content.
- It is set **at pod level only**. Container-level `seccompProfile` stays unset and uncompared
  ([the drift comparison](workload-pod-posture.md#the-drift-comparison-checks-only-what-the-operator-sets)).

## What a CR author chooses

**A CR author also picks the seccomp profile, ~~among the ones on the nodes~~ among the
`Localhost` profiles the operator's allow-list names — none by default** *(re-decided
2026-09-26,
[ADR 0033](../adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md)
D9)*. Since 2026-09-26
`spec.podSecurity.seccompProfile` is `RuntimeDefault` or `Localhost` with ~~any path~~ a relative
path below the kubelet's seccomp directory
([ADR 0033](../adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md)
D1). `Unconfined` is refused by the CRD, but a `Localhost` profile is only as strict as the
file it names: an allow-by-default profile installed on a node — the e2e fixture is one —
is weaker than `RuntimeDefault` and still passes Pod Security `restricted`, which accepts
every `Localhost` profile (read in `k8s.io/pod-security-admission` v0.37.0,
`check_seccompProfile_restricted.go`). The path cannot leave that directory: the API server
refuses an absolute path or a `..` element in every pod template (read in upstream
`validateSeccompProfileField`, Kubernetes v1.36.4; not tested here) — ~~the CRD itself checks
only that the path is non-empty~~ *(superseded 2026-09-26: a second CEL rule on
`SeccompProfileSpec` now refuses both at CR admission, [validation](validation.md#what-that-means-in-practice))*. ~~The profile set on the
nodes is therefore part of what a CR author may choose from ([H-19](#h-19)).~~ *(Superseded
2026-09-26 by the allow-list below; so is the stance that the operator has no allow-list and an
admission policy has to narrow the choice.)*

## The `Localhost` allow-list

**The operator enforces a default-deny allow-list of `Localhost` profiles** (ADR 0033 D9).
`--allowed-seccomp-localhost-profiles` — chart value
`valkeyPodSecurity.allowedSeccompLocalhostProfiles`, `[]` # default — lists the profiles, as
paths relative to the kubelet's seccomp directory, a Valkey resource may name; the chart
renders the flag only for a non-empty list, and `profileList` trims the comma-separated
entries and drops blanks ([`cmd/main.go`](../../cmd/main.go)). `RuntimeDefault` needs no entry and
is always allowed. A `Localhost` path matching no entry exactly — every one while the list is
empty — is refused ~~before any of the three workloads is written~~ at the write of each of the
three workloads *(gate moved 2026-09-26 from the head of each workload step)*, on create and on
update alike: `reconcileStatefulSet` returns `errSeccompProfileNotAllowed` ~~ahead of the data
StatefulSet write~~ right before it writes — on create after the TLS material record, on update
after the ownership proof, the claim guard (`guardVolumeClaimTemplates`), the TLS material
record and the repair decision, and before the drift detection — and is the one reporter
(`ReconcileBlocked=True/SeccompProfileNotAllowed`, phase
`Error`, the message naming the profile and the chart value; ranked below `RecreateRequired`
and above `UserNamespacesUnsupported` in `reconcileBlockedReason`), while the Sentinel
StatefulSet and observer Deployment steps withhold their writes silently at the same point —
after their own ownership proof and, on the Sentinel StatefulSet, its claim guard and TLS
material record (`seccompProfileAllowed` in [`pod_hardening.go`](../../internal/controller/pod_hardening.go), called
from [`valkey_controller.go`](../../internal/controller/valkey_controller.go)). The
chart refuses at render an empty entry, one starting with `/`, one containing `,` or one with a
`..` element (`valkey-operator.allowedSeccompLocalhostProfiles` in
[`_helpers.tpl`](../../deploy/helm/valkey-operator/templates/_helpers.tpl)). Documenting the risk
only was decided first and reversed the same day; removing `Localhost` would have taken the
option from clusters that manage their own profiles ([RuntimeDefault or Localhost](#runtimedefault-or-localhost-never-unconfined)).

- *What it defends against.* A CR author naming a profile nobody chose for Valkey — a
  permissive test profile left on a node, one installed for another workload, or any path at
  all on a cluster whose administrator never considered `Localhost`: the default refuses every
  one. What CR authors choose from becomes a decision of whoever configures the operator
  instead of a side effect of what lies on the nodes.
- *What it does not defend against.* The list holds **names**; the operator cannot see the
  files. It trusts that a listed path holds, on every node a pod can land on, a profile at
  least as strict as intended — whoever can write the kubelet's seccomp directory on a node, or
  the tool that manages it, decides what a listed name enforces, and a listed file that differs
  between nodes or is loosened later goes unnoticed. `RuntimeDefault` is whatever the node's
  runtime ships. The list is one per operator, not per namespace: every CR author may pick any
  listed profile. It binds only what the operator writes; a principal who may create pods or
  StatefulSets in the namespace directly is not bound by it, and an admission policy on pods
  stays the control for those.
- *What a refusal costs.* The whole workload write is held, not only the profile: until an
  administrator lists the profile or the spec names another, an image change or a certificate
  rotation of that cluster does not reach its templates either (~~the TLS fingerprint is stamped
  after the check in `reconcileStatefulSet`~~ *(superseded 2026-09-26)* the TLS fingerprint is
  stamped before the check since the gate moved, onto a template the refusal then does not
  write — read, not tested), and the running pods keep
  their template. The rest of the pass still runs — `runReconcileSteps` joins the step errors
  and carries on — so ConfigMaps, Services, sidecar RBAC, certificates, PodDisruptionBudgets,
  NetworkPolicies and the metrics objects are written as usual, and a rotated certificate is
  still reported by `TLSMaterialStale`, which compares the pods against the Secret (read, not
  tested). Removing a profile from the list freezes the clusters that name it the same
  way; it does not take the profile off their running pods.
- *What is still reported while a profile is refused* *(added 2026-09-26, when the gate moved:
  at the head of each workload step a refusal had hidden a name collision, frozen the
  `StorageSpecNotApplied` level and skipped the TLS material record — closed the same day)*.
  Everything a workload
  step proves or measures before its write. A data or Sentinel StatefulSet under the generated
  name that this Valkey does not control is reported as `ForeignObject` with its Warning Event,
  and outranks the refusal in `reconcileBlockedReason`; a foreign observer Deployment still
  gets its Warning Event. The claim guard re-measures `StorageSpecNotApplied` on every pass, and
  a `RecreateRequired` conflict ends the data step before the gate and outranks the refusal as
  well. The TLS material record is armed before the gate, so on a fresh TLS cluster whose
  Secret cert-manager has not issued yet the step ends at the record (ADR 0030 D12) and the
  refusal is reported once the Secret exists. Because the gate sits before the drift
  detection, a template that already carries a profile the list no longer holds is reported on
  every pass, not only when something else would be written. Verified by
  `TestSeccompProfileNotAllowed_GateSitsAtTheWrite` (a foreign StatefulSet is reported as
  `ForeignObject`; a live template the shrunk list no longer holds is reported without drift
  and not written); the claim-guard and TLS-record orderings are read from
  `reconcileStatefulSet`, not tested row by row.

Verified: `TestSeccompProfileAllowed`, `TestSeccompProfileNotAllowed_NoWorkloadIsWritten`
(create and update, all three workloads) and `TestProfileList` (`make test-unit`, green
2026-09-26 before the gate moved; ~~the full unit tier on the final code is not claimed~~
*(amended 2026-09-26)* the full unit tier with the gate at the write was green in a clean copy,
`make test-unit-coverage`, on the code before ADR 0025 D9 gained its own clock; its rerun on
the final code has no result yet),
`TestSeccompProfileNotAllowed_GateSitsAtTheWrite` (added with the move; 7 of 7
mutations of the D9 code killed, the gate-position mutations included); the chart's rendering
and refusals with `helm template` by hand (2026-09-26). ~~No
recorded run yet: `TestPodSecurity_LocalhostProfileAllowList_Integration` (the green
`make test-integration` of 2026-09-26 predates it) and the e2e subtest "a Localhost profile
the operator does not allow is refused and reported".~~ *(Superseded 2026-09-26.)*
`TestPodSecurity_LocalhostProfileAllowList_Integration` was green in repeated envtest runs
(Kubernetes 1.29 API server), and the e2e subtest "a Localhost profile the operator does not
allow is refused and reported" — `ReconcileBlocked=True/SeccompProfileNotAllowed` through the
real chart, no StatefulSet created — was green on every run of the final image ([ADR 0033](../adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md) Status).

## What this does not cover

<a id="h-19"></a>

### H-19: List only the `Localhost` seccomp profiles you would accept for every Valkey pod, and treat each listed file as trusted

~~**Treat every `Localhost` seccomp profile on a node as selectable by every CR
author**~~ *(re-decided 2026-09-26)*
([ADR 0033](../adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md)
D1, D9, [the `Localhost` allow-list](#the-localhost-allow-list)). ~~Install on nodes that run Valkey pods only profiles you would accept on
any of them, and never leave a permissive test profile there — the e2e fixture is
allow-by-default with a short deny list, weaker than `RuntimeDefault`. Pod Security
`restricted` does not distinguish them. If the choice must be narrower than "whatever
is on the node", an admission policy — on `spec.podSecurity.seccompProfile` of the CR,
or on the `localhostProfile` of the pods it generates — has to narrow it; the operator
has no allow-list.~~ *(Superseded 2026-09-26: the operator enforces an allow-list.)*
`valkeyPodSecurity.allowedSeccompLocalhostProfiles` (`--allowed-seccomp-localhost-profiles`)
is empty by default, which refuses every `Localhost` profile — the operator then writes no
workload of a Valkey resource naming one and reports
`ReconcileBlocked=True/SeccompProfileNotAllowed`; `RuntimeDefault` is always allowed.
Add a path only for a profile at least as strict as you accept for every Valkey pod in
the cluster: the list is operator-wide, so every CR author may pick any entry, and
neither the allow-list nor Pod Security `restricted` looks at what the file enforces.
Keep every listed file identical on every node that runs Valkey pods and guard who may
write the kubelet's seccomp directory there — that principal decides what a listed name
means. Never list a permissive test profile outside a test cluster: the e2e lists its
fixture, which is allow-by-default with a short deny list and weaker than
`RuntimeDefault` ([`test/e2e/helm-values.yaml`](../../test/e2e/helm-values.yaml)). For pods and
StatefulSets the operator does not write, an admission policy remains the control. No
recommended `Localhost` profile for the generated containers ships today, and a profile must
allow the repair's `chown`.
