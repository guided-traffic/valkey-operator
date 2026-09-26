# ADR 0013: The Operator Is a Cluster-Wide Privileged Component

## Status

Accepted. Date: 2026-08-21.

The privilege model is implemented and documented rule by rule in
[SECURITY_ARCHITECTURE.md](../../SECURITY_ARCHITECTURE.md). **Several narrowing items in
this ADR are open** and live as the hardening checklist there; they are listed under
Residual risks.

The privilege footprint was verified by reading only: the repository at one commit, taken
from `config/rbac/role.yaml`, the Helm ClusterRole, `internal/builder/rbac.go` and the
kubebuilder markers. No rule was exercised against a live API server. One claim below is not
of that kind — cert-manager's ownerReference behaviour in the Consequences rests on the
reference cluster, recorded beside the code that depends on it
(`deleteLegacySentinelSecret`, [`internal/controller/valkey_controller.go`](../../internal/controller/valkey_controller.go)):
measured on a cluster, not reproducible from this repository.

Amended 2026-09-26: **D9 is superseded by [ADR 0032](0032-generated-pods-run-rootless.md)
D1 — every generated pod runs rootless, with no option.** The asymmetry D9 stated as a
decision is gone: the five `securityContext` controls D8 counts on the operator's own
Deployment are now set on every data, Sentinel and observer pod, and data and Sentinel pods
additionally run with a fixed uid, gid and `fsGroup` of 999. D9, the Consequence that a
`restricted` namespace rejects the generated pods, the "Set a workload `securityContext`"
alternative and the workload-`securityContext` residual risk are marked in place below; D8
gains an addition. The operator's own privilege footprint — every other decision here — is
unchanged. Verified by reading
[`internal/builder/pod_security.go`](../../internal/builder/pod_security.go), its three call
sites (`buildPodSpec`, `buildSentinelPodSpec`, `BuildObserverDeployment`), the repair's
insertion in `reconcileStatefulSet`, the Pod Security evaluator matrix in
[`pod_security_test.go`](../../internal/builder/pod_security_test.go), and — for what a
namespace label change does to running pods — `ValidateNamespace` and `isSignificantPodUpdate`
in `k8s.io/pod-security-admission` v0.37.0, the version `go.mod` pins; the evaluator matrix
was read, not run, for this amendment. **Run on a node, locally and not in CI** (2026-09-26,
Kind with a control plane and three workers, Kubernetes v1.36.1, containerd; ~~the branch has not
been through the pipeline~~ *(corrected 2026-09-26: pushed as `e2ce8bb`, where
`Generated Manifests Up To Date` and `Integration Tests (envtest)` failed — a stale local
controller-gen and a cache read right after a Create, both fixed in the working tree; CI has not
run on the fix)*): `make test-e2e` on both Valkey lines, 51/51 green each, including
`TestE2E_PodSecurity_RestrictedNamespace`; and the fleet-upgrade migration e2e
`TestE2E_FleetUpgrade`, green from released chart 1.12.8 — not from its default 1.10.48, whose
amd64-only images could not be run on the arm64 host used.

Amended again 2026-09-26, in the same unreleased release:
[ADR 0033](0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md) D6
extends D8 — the operator Deployment and the pre-upgrade hook Job share one pod and one container
block with a fixed uid, gid and `fsGroup` of 65532, `privileged: false`,
`enableServiceLinks: false`, a stated `automountServiceAccountToken: true`, a seccomp profile of
`RuntimeDefault` or `Localhost` from `podSecurity.seccompProfile`, and an opt-in
`hostUsers: false` — and D4 of that ADR supersedes D8's reason for the observer's identity. The
D9 note, the restricted-namespace Consequence and the closed workload-`securityContext` residual
risk are also brought up to the second roll of [ADR 0032](0032-generated-pods-run-rootless.md) D2
and its ordering (D4), decided the same day: they still said that dropping the repair from the
template rolls nothing. The privilege footprint (D1–D7, D10–D14) is unchanged. Verified by
reading the chart templates, `_helpers.tpl`, `values.yaml` and
[`pod_security.go`](../../internal/builder/pod_security.go); the chart variants were rendered by
hand according to the T31 ticket, and CI renders only the default `podSecurity` and an empty
`image.digest` (the e2e job's `helm install`); ~~the second roll has not yet passed on a node, and
ADR 0033's e2e has not run yet~~ *(superseded 2026-09-26 by the runs below)*.

Amended a third time 2026-09-26. **The residual risk "`automountServiceAccountToken` is never
disabled on the data pods" is closed**, and has been since 2026-08-27: the data pod sets it
`false` and projects the token into the `sidecar` container alone, the Sentinel pod sets it and
projects nothing ([ADR 0012](0012-the-sidecar-records-its-drain-promotion-on-the-pod.md) D8
step 4, out of the adversarial review of ADR 0030 that also produced
[ADR 0031](0031-a-record-the-operator-trusts-lives-in-pod-spec.md)); the item and the
Consequence "where it can use the mounted sidecar token" are marked in place. D9's note gains [ADR 0033](0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md)
D9: a `Localhost` profile a Valkey resource names is written into its workloads only if the
operator's `--allowed-seccomp-localhost-profiles` lists that exact path (chart `valkeyPodSecurity.allowedSeccompLocalhostProfiles`,
default empty). That list bounds the CR authors, not the operator: its own pods take
`podSecurity.seccompProfile` unchecked against it (D8). No grant changes. Verified by reading
[`statefulset.go`](../../internal/builder/statefulset.go) (`AutomountServiceAccountToken`,
`sidecarTokenVolume`), [`sentinel.go`](../../internal/builder/sentinel.go),
[`pod_hardening.go`](../../internal/controller/pod_hardening.go), `cmd/main.go` and the chart's
`deployment.yaml` and `_helpers.tpl`; `helm template` rendered the flag for a non-empty list,
none for the default, and failed the render for each refused entry (2026-09-26). Runs on Kind
(2026-09-26, Kubernetes 1.36.1, containerd 2.3.1, runc 1.4.2, Linux 6.10), all before the
allow-list and ADR 0033's CEL path rule existed: the fleet-upgrade e2e from 1.12.8 green including the second roll (two
`RollingUpdateComplete` per persistent tier), the full suite 53/53 on Valkey 8 and 52/53 on
Valkey 9 with ADR 0033's hardening e2e failing once on its own `/data` owner assertion, since
fixed and passed on Valkey 8 and, rerun alone, on Valkey 9 (ADR 0032 Status). The allow-list's
e2e subtest has not run, and no run of its integration test is recorded.

## Context

The operator is installed once and expected to serve `Valkey` CRs in arbitrary namespaces.
It discovers those namespaces only by watching CRs, and for each cluster it must read a
**user-owned** auth Secret and TLS Secret and write the generated objects. That is what
"install-and-forget" costs: cluster-scoped reads of Secrets and cluster-scoped writes of
almost everything else.

The failure mode worth preventing is not the grant itself — it is an operator deployed into
a multi-tenant cluster under the belief that a namespace confines it.

## Decision

**D1 — The operator manager is treated as equivalent to a cluster-admin credential, and
that is stated rather than defended.** ServiceAccount `<release>`, bound by a
**ClusterRoleBinding**, watching and writing in every namespace. **Namespace is explicitly
not a trust boundary for the operator itself.** Access to its ServiceAccount token, its image
and its CR API must be governed the way cluster-admin access is governed.

**D2 — The privilege ceiling is named as what it permits, not as the narrow use it was
added for.** `roles: escalate,bind` + `rolebindings` + `serviceaccounts: create` is namespaced
admin in every namespace: **create SA → write Role (with `escalate`) → bind → use.**
`escalate` lifts the rule that a principal may only grant permissions it holds, so the
operator can write a Role containing *any* namespaced permission in *any* namespace; `bind`
is the matching verb for referencing that Role from a RoleBinding. On top of that,
`secrets: get,list,watch` cluster-wide is the heaviest confidentiality exposure, and the
operator is the only component that changes cluster topology (`REPLICAOF`) from outside the
data plane — the drain sidecar issues `REPLICAOF` itself on SIGTERM
([ADR 0012](0012-the-sidecar-records-its-drain-promotion-on-the-pod.md)), and with Sentinel
enabled failover authority is Sentinel's.

**D3 — `escalate` and `bind` are retained until the narrower configuration is actually
tested.** The rationale on record is that without them the API server refuses to let the
operator create the `<cr-name>-sidecar` Role — an observation from when the grant was added,
not reproduced against a cluster from this repository. It **may no longer apply** either:
verified by reading, the chart ClusterRole holds `pods: delete,get,list,patch,watch`
cluster-wide while `BuildSidecarRole` grants `pods: get,list,patch` in one namespace, so the
sidecar Role is a strict subset of the operator's own grants and neither verb should be
needed. The document records that question as **not verified** on this Kubernetes version
rather than asserting either answer.

**D4 — A cluster-wide destructive verb obliges its call site to prove ownership. Grant and
guard ship together.** The chart keeps `delete` on core `secrets` (without it the
`unifiedCertificate` migration 403'd on every pass and was broken for exactly the clusters
that need it — observed on a cluster, not reproducible from this repository, which holds only
the fix in commit `9e5634d` and the comment beside the rule in `clusterrole.yaml`), and the
delete site is provenance-gated
([ADR 0006](0006-delete-only-what-the-operator-owns.md)). The footprint shifts from "reads
every Secret in the cluster" to "reads and destroys everything", and **only a name-independent
guard at the call site keeps that grant safe**. The ClusterRoleBinding scope means namespace
confinement comes from the guard, not from RBAC.

**D5 — Every Valkey CR gets its own sidecar ServiceAccount, Role and RoleBinding.**
`<cr-name>-sidecar`, never a shared one, plus an ownerReference on every object the operator
itself creates — the RBAC triple included — so CR deletion removes the whole cluster through
garbage collection and no orphan is left holding a grant. The reference reaches exactly that
set and no further: the PVCs the StatefulSet controller creates from `volumeClaimTemplates`
and the Secret cert-manager issues from the operator-owned `Certificate` are not built here
and carry no reference to the CR, so both outlive it (see Consequences). Per-CR credentials
bound the blast radius of a stolen sidecar token to one namespace rather than the fleet.

**D6 — Sentinel pods run under the namespace `default` ServiceAccount.** They carry no
labeler sidecar and need no API access; with Sentinel enabled, failover authority is
Sentinel's. Handing them the sidecar credential would widen the set of pods holding a
namespace-wide pod-patch token for no functional gain. The trust is stated explicitly:
whatever `default` is bound to — nothing, in a stock cluster.

**D7 — NetworkPolicies are opt-in and ingress-only.** `spec.networkPolicy.enabled` writes up
to three policies, every one with `PolicyTypes: [Ingress]`: the Valkey policy always, the
Sentinel and observer policies only when those components are enabled, so a standalone
cluster gets exactly one. The data port accepts traffic from Valkey pods,
Sentinel pods, observer pods and **the operator namespace** (matched on
`kubernetes.io/metadata.name`, because the operator connects directly to the data plane). The
sidecar health port and the exporter port are deliberately **open to everyone**: kubelet
probes originate from the node, not from a pod a policy can select, and Prometheus is not
locatable from the CR.

**D8 — The operator process itself runs fully restricted.** Verified by reading the chart
Deployment: five `securityContext` controls, split across the two levels the field exists
at — pod-level `runAsNonRoot: true` and `seccompProfile: RuntimeDefault` *(since 2026-09-26 the
chart default of `podSecurity.seccompProfile`, with `Localhost` selectable — the ADR 0033 D6
amendment below)*, container-level
`allowPrivilegeEscalation: false`, `readOnlyRootFilesystem: true` and
`capabilities: drop [ALL]`. `terminationGracePeriodSeconds: 10` sits beside them in the same
pod spec and is not a `securityContext` control. The operator holds the cluster's most
privileged token, so hardening its own runtime is the highest-value posture control
available — **and it is the existence proof for giving the workload pods the same
treatment.** *(Added 2026-09-26:)* They have it now:
[ADR 0032](0032-generated-pods-run-rootless.md) D1 applies these five controls to every
generated pod — ~~the observer with exactly these five, because its image user is numeric like
this one~~ *(superseded 2026-09-26 by [ADR 0033](0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md)
D4: the observer is pinned to `runAsUser`, `runAsGroup` and `fsGroup` 65532, `OperatorUID`)*;
data and Sentinel pods with `runAsUser`, `runAsGroup` and `fsGroup` 999 on top,
because the Valkey image declares no `USER` and would otherwise start as root. The one
container outside the posture is the migration-only `fix-data-ownership` repair (ADR 0032 D2).

*Amended 2026-09-26 by [ADR 0033](0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md)
D6:* the five controls are no longer the whole of it, and they are no longer the Deployment's
alone. The chart's Deployment and its pre-upgrade hook Job — which carried the same five before,
unnamed here — now render one shared pod block and one shared container block
(`valkey-operator.podHardening`, `valkey-operator.containerSecurityContext` in
[`_helpers.tpl`](../../deploy/helm/valkey-operator/templates/_helpers.tpl)): `runAsNonRoot` with
`runAsUser`, `runAsGroup` and `fsGroup` 65532 — the image's distroless `nonroot` user, now named
rather than inherited — `enableServiceLinks: false`, `automountServiceAccountToken: true`
(stated: both pods call the API server), and `privileged: false` beside the three container
fields. The seccomp profile is `podSecurity.seccompProfile`: `RuntimeDefault` by default, or
`Localhost` with a path; any other type, a `Localhost` without a path, or a path without
`Localhost` fails the render, so `Unconfined` cannot be installed through the chart. *(Added
2026-09-26, ADR 0033 D9:)* the operator's own profile is the installer's choice and is not
checked against `valkeyPodSecurity.allowedSeccompLocalhostProfiles`, which bounds only what a
Valkey resource may name; unlike that list and the CRD, the chart does not refuse an absolute
path or a `..` element here (rendered by hand 2026-09-26; what the API server then does with the
Deployment was not run).
`hostUsers: false` is behind `podSecurity.userNamespaces`, default off, because a node without
user-namespace support would not start the operator pod; an API server with the
`UserNamespacesSupport` gate off drops the field from both pods without an error, and for the
chart nothing reports that (ADR 0033 *Residual risks*). None of this narrows D1: the
operator's power is its ServiceAccount token, and no pod-level control changes what that token
may do at the API server. `image.digest`, when set (the default is empty, and nothing in the
release pipeline fills it), pins the operator, the hook and, through `--operator-image`, the
sidecar and observer images the operator generates (`valkey-operator.image`, ADR 0033 D5). Verified by reading the templates and `values.yaml`; `helm template`
with each variant was run by hand (ADR 0033 *Residual risks*). In CI the chart is rendered
only by the e2e job's `helm install` (`.github/workflows/release.yml`, with
`test/e2e/helm-values.yaml`), which leaves `podSecurity` and `image.digest` at their defaults; no
CI job renders the digest, user-namespace or `Localhost` variants or checks a refused value.

**D9 — ~~The asymmetry between the operator and the workloads it creates is a stated decision,
not an oversight.~~** ~~No pod generated by `internal/builder` sets any `securityContext`
(verified by the absence of the field across the package). Generated clusters inherit whatever
the namespace's Pod Security admission level allows.~~ *(**Superseded 2026-09-26 by
[ADR 0032](0032-generated-pods-run-rootless.md) D1:** every generated pod runs rootless, with
~~no CRD field~~ no CRD field that lowers it *(amended 2026-09-26: `spec.podSecurity` of ADR
0033 chooses a seccomp profile and an opt-in user namespace, never root and never
`Unconfined`)*, no `baseline` level and no opt-out. The stated decision was the wrong one: the
upstream Valkey image declares no `USER` and drops to `valkey` in its own entrypoint, which
every generated container on that image replaces with `command:` — so under this rule
`valkey-server` ran as uid 0 with Docker's default set of fourteen capabilities and
`NoNewPrivs: 0`, less isolation than the image itself intends (measured in Docker on both
pinned lines, ADR 0032 Context). What holds now: one walk over the assembled `PodSpec`
(`applyValkeyPodSecurity`, `applyObserverPodSecurity` in
[`pod_security.go`](../../internal/builder/pod_security.go)) sets pod-level
`runAsNonRoot: true` and `seccompProfile: RuntimeDefault` *(or a `Localhost` profile the CR
names, never `Unconfined` — ADR 0033 D1, 2026-09-26 — and only one the operator's
`--allowed-seccomp-localhost-profiles` lists; an unlisted one is never written, ADR 0033 D9,
same day)* — plus `runAsUser`, `runAsGroup` and
`fsGroup` 999 on data and Sentinel pods *(and 65532 on the observer, ADR 0033 D4)* — and
`allowPrivilegeEscalation: false`,
`readOnlyRootFilesystem: true` and `capabilities.drop: [ALL]` on every container and init
container, the sidecar and the third-party exporter included *(plus `privileged: false` on every
container, `enableServiceLinks: false` on every pod, and an opt-in `hostUsers: false` — ADR 0033
D2, D4)*. The one root container the
builders still generate is `fix-data-ownership` — uid 0, only `CAP_CHOWN` — carried by a
persistent data template only during the migration (added while a data pod an earlier
operator built exists, kept until every ordinal holds a ~~rootless pod proven ours~~ pod proven
ours, rootless and Ready, and while a data-tier roll is recorded — *tightened 2026-09-26, ADR
0032 D4*) and kept out
of the pod-spec hash, so a pod created from that template keeps it in its spec after the
template drops it — until the second roll replaces it *(decided 2026-09-26, ADR 0032 D2)* (ADR
0032 D2, D4); a template carrying it passes `baseline`, not
`restricted`. D8's addition records what replaced this rule.)*

**D10 — The pre-upgrade hook's grant is bounded in time, not in scope.** `<release>-upgrade`
gets `valkeys: get,list,patch,update` and `customresourcedefinitions: get,list,patch,update`
cluster-wide for the lifetime of the hook Job. `patch`/`update` on CRDs is a **cluster-wide
schema-change grant**. The bound is Helm's, and only on the success path: the ServiceAccount,
ClusterRole and ClusterRoleBinding carry
`helm.sh/hook-delete-policy: hook-succeeded,before-hook-creation` and **no `hook-failed`**, so
a hook that exhausts its `backoffLimit: 3` leaves the grant in place until the next
`helm upgrade` deletes it via `before-hook-creation`. It is opt-out via
`preUpgradeHook.enabled: false`, at the cost of the field-default migration it performs.

**D11 — The default kubebuilder marker verb set on `valkeys` is kept, including the unused
`create`.** The footprint document's rule is that every rule is read out of the manifests and
its consequence stated — not that every rule is justified. An unused verb documented as
unused is auditable, and trimming it would put the generated role permanently out of step with
what `make manifests` reproduces.

**D12 — The privilege footprint is documented rule by rule, and updated in the same change
as the code.** `SECURITY_ARCHITECTURE.md` covers roles and trust boundaries, data and secret
flow, isolation and what it does *not* defend against, the footprint rule by rule, the
validation story, rotation, vulnerability reporting and the hardening checklist. Every rule is
read out of the manifests, not out of intent, and unverified statements say so. Before it
existed, the permission set lived only in the markers, the generated role and the chart
ClusterRole, and the README documented no verbs at all.

**D13 — The hardening checklist is ordered by what a compromise buys an attacker, never by
effort, and completed items stay in the list with what they did *not* close.**
Effort-ordered lists get worked top-down and leave the expensive, highest-impact items
permanently last — here that would be exactly the two things that define the trust model.
Keeping closed items visible with their residual prevents a checked box from being read as
"this class of risk is gone".

**D14 — Vulnerability intake states the gap rather than inventing a contact.** There is no
`SECURITY.md` and no published address; reports are routed to GitHub private vulnerability
reporting, or to the maintainer organisation, and reporters are asked not to open a public
issue for anything that reads a Secret, escalates RBAC or destroys data, and to include the
operator version, the chart version and whether TLS and auth were enabled. An invented or
aspirational address is worse than none — it routes a real finding into a channel nobody
reads.

## Consequences

* **Trust boundary 1 (operator ↔ workload) collapses on operator compromise:** a compromised
  operator is a cluster-wide compromise. It can delete any pod, Secret, NetworkPolicy or
  StatefulSet in the cluster, replace the pod template — hence the image, hence the code — of
  any Deployment or StatefulSet, and reach namespaced admin everywhere.
* Nothing survives CR deletion except user-owned Secrets, PVCs, and the cert-manager-issued
  TLS Secret. The first two are deliberate: the operator does not own user data or user
  credentials. The third is not the operator's to collect — it sets the ownerReference on the
  `Certificate` only, and cert-manager leaves the Secret it issues without one unless its
  controller runs with `--enable-certificate-owner-ref` — measured on the reference cluster,
  not reproducible from this repository — so `<name>-tls` (and
  `<name>-sentinel-tls` in split mode) outlives the CR. That is the same leftover the
  `unifiedCertificate` migration has to delete by hand
  ([ADR 0006](0006-delete-only-what-the-operator-owns.md)).
* **The per-CR isolation is per-namespace, not per-cluster.** Within one namespace the per-CR
  Roles are indistinguishable, because none uses `resourceNames`
  ([ADR 0012](0012-the-sidecar-records-its-drain-promotion-on-the-pod.md) D8).
* **No egress rule is written at all**, so a compromised Valkey pod may open connections
  anywhere, including to the API server — ~~where it can use the mounted sidecar token~~
  *(corrected 2026-09-26: since ADR 0012 D8 step 4, 2026-08-27, only a compromised `sidecar`
  container holds that token; the `valkey` and `exporter` containers and the init containers
  mount none)*. The open health and exporter ports are unauthenticated surfaces reachable from
  anywhere in the cluster.
* ~~**A `restricted`-PSA namespace will reject the generated pods outright**, so the operator
  cannot be used in a hardened namespace today. Data pods run as root with full capabilities
  unless the namespace forces otherwise.~~ *(Superseded 2026-09-26 by
  [ADR 0032](0032-generated-pods-run-rootless.md) D1 and D6.)* Every template the builders
  render passes Pod Security `restricted` until the migration repair is inserted — asserted,
  with the positive control that the legacy shape is refused, by the evaluator matrix in
  [`pod_security_test.go`](../../internal/builder/pod_security_test.go) (read, not run for this
  amendment); admission by a real API server on a node is the job of
  `TestE2E_PodSecurity_RestrictedNamespace`, which passed on both Valkey lines (locally on
  Kind, 2026-09-26, not in CI): the namespace refused an unrestricted pod on a server-side dry
  run, then admitted every generated pod of four topologies and saw it Ready, with `Uid` 999,
  `CapEff` and `CapBnd` 0 and `NoNewPrivs` 1 in `/proc/1/status` of the `valkey` and
  `sentinel` containers. "Full capabilities" was imprecise
  as well: it was the runtime's default set — fourteen as measured under Docker, `NET_RAW`,
  `DAC_OVERRIDE` and `SETUID` among them. When an existing namespace can be switched to
  `enforce: restricted` is ADR 0032 D6's rule: once it holds no pod without `runAsNonRoot`.
  What makes labelling earlier unsafe is pod *creation*, not the pods already running —
  PodSecurity evicts nothing, a label change only returns warnings for existing violators
  (`ValidateNamespace`), and a label patch the sidecar makes on such a pod is not re-evaluated
  (`isSignificantPodUpdate`) — but a data pod the migration creates from a template that still
  carries the repair passes `baseline` only and would be refused. A deferred non-persistent
  single-pod cluster (`PodSecurityUpdatePending`, ADR 0032 D3) holds that precondition off
  without being at risk: its template is already rootless, only the pod is not. And the
  server-side dry-run D6 recommends lists more than the precondition names: every persistent
  data pod the migration created keeps `fix-data-ownership` in its immutable spec after the
  template drops it, and is reported until ~~it is replaced for another reason~~ the second roll
  of ADR 0032 D2 replaces it *(corrected 2026-09-26: dropping the repair from the template makes
  each such pod outdated, `podCarriesRetiredRepair`)*. The operator
  never labels namespaces.
* The documented blast radius includes creating `Valkey` CRs in any namespace on top of
  deleting them (D11).
* The checklist has to carry unchecked high-severity items indefinitely without that reading
  as neglect — scoping the `secrets` grant costs install-and-forget behaviour for new
  namespaces, and may never be done.
* Vulnerability intake depends on GitHub's private-reporting feature being enabled on the
  repository. The missing `SECURITY.md` is an open documentation item, distinct from
  `SECURITY_ARCHITECTURE.md`, which is the design document and deliberately **not** the
  GitHub reporting convention file.

## Alternatives Considered

### A namespaced Role per watched namespace

Or a cache filtered by label with the ClusterRole narrowed to match. Both are on the hardening
checklist with the cost stated: **the operator stops being install-and-forget for new
namespaces.**

### Drop `escalate` and `bind`, keeping the sidecar Role a strict subset of the operator's own grants

Explicitly untested on this Kubernetes version, and recorded as such rather than assumed
either way.

### Drop the chart's `secrets: delete` rule again and never delete the legacy Secret

Keeps the grant unnecessary, but leaves stale TLS material and an occupied name
([ADR 0006](0006-delete-only-what-the-operator-owns.md)).

### A single shared operator-namespace ServiceAccount for all sidecars

Rejected by the per-instance design: it would make one stolen token a fleet-wide credential.

### Give Sentinel pods the `<cr-name>-sidecar` ServiceAccount

Rejected: more pods holding a namespace-wide pod-patch token for no functional gain.

### Add egress NetworkPolicies

On the checklist, not implemented.

### Set a workload `securityContext`

~~On the checklist, not implemented. `readOnlyRootFilesystem` in particular conflicts with the
Valkey data path unless volumes are carved out.~~ *(Superseded 2026-09-26: taken by
[ADR 0032](0032-generated-pods-run-rootless.md) D1, for every generated pod and with no
opt-out.)* The feared conflict did not arise: every path a generated process writes was
already a mounted volume — the data PVC or `emptyDir`, the Sentinel config `emptyDir` — so the
read-only root needed no carve-out. The one thing added is `workingDir: /data` on the `valkey`
container, which it used to inherit from the image. Measured under `--read-only` in Docker on
both pinned lines (ADR 0032 Context) and kept as
[`test/imagetools/restricted_runtime_test.go`](../../test/imagetools/restricted_runtime_test.go);
on a node, `TestE2E_PodSecurity_RestrictedNamespace` completed an AOF rewrite and an RDB
snapshot under the read-only root (locally on Kind, 2026-09-26, not in CI).

### Trim the `valkeys` marker to the verbs the code uses

Not taken: regeneration keeps reintroducing the default set unless the marker is hand-edited
and kept edited.

### Order the hardening checklist by effort or likelihood

Rejected: it buries the items that define the trust model.

### Drop completed items from the checklist

Rejected: it loses the statement of what the fix did *not* cover.

### Publish a maintainer email, or omit the reporting section

The first is not established; the second leaves a reporter with no channel at all.

## Residual risks

Every item below except the last is on the hardening checklist in
[SECURITY_ARCHITECTURE.md](../../SECURITY_ARCHITECTURE.md), ordered there by blast radius.

* **`secrets: get,list,watch` cluster-wide (open)** — the heaviest confidentiality exposure.
  `delete` exists for exactly one, provenance-gated caller; the guard bounds the reconcile
  path, not the grant.
* **`roles: escalate` + `rolebindings` + `serviceaccounts: create` (open)** — namespaced
  admin everywhere. Reducing it requires verifying the subset claim and dropping both
  `escalate` and `bind`; the chart grants the pair, and holding all of a Role's permissions is
  what makes either one unnecessary.
* ~~**Workload pods have no `securityContext` (open)** while the operator's own Deployment sets
  all five `securityContext` controls listed in D8.~~

  **(Closed 2026-09-26 by [ADR 0032](0032-generated-pods-run-rootless.md) D1 and D6.)** Every
  generated pod carries the five D8 controls, data and Sentinel pods run as uid 999, and the
  rendered templates pass the `restricted` evaluator the API server's PodSecurity admission
  runs, and a namespace labelled `pod-security.kubernetes.io/enforce: restricted` admitted
  every generated pod in `TestE2E_PodSecurity_RestrictedNamespace`. What it did **not** close: the migration-only repair runs as uid 0 with
  `CAP_CHOWN` in every persistent data pod the migration creates (ADR 0032 D2), and ~~because
  removing it from the template rolls nothing, it stays in each such pod's spec until the pod
  is replaced for another reason~~ it stays in each such pod's spec until the second roll
  replaces the pod once the repair has left the template *(corrected 2026-09-26, ADR 0032 D2 and
  the ordering of D4; ~~that second roll has not yet passed on a node~~ it passed the same day in
  the fleet-upgrade e2e from 1.12.8, two `RollingUpdateComplete` per persistent tier)* — until then raising the
  namespace's `enforce` label to `restricted`, or
  dry-running it, lists those pods as violators, and kubelet re-runs init containers when it
  has to recreate a pod's sandbox, so the repair can run as root there again (Kubernetes
  init-container semantics, not measured here); a non-persistent single-pod cluster keeps its
  root pod until it restarts for another reason (D3); OpenShift's `restricted-v2` SCC refuses
  the fixed `runAsUser: 999` outside a namespace's UID range, and nothing here targets it; and
  **the node evidence is local, not CI** — the restricted-namespace e2e and the fleet-upgrade
  migration e2e passed on 2026-09-26 on one Kind cluster (Kubernetes v1.36.1, containerd), ~~the
  branch has not been through the pipeline~~ the branch's one pipeline run (`e2ce8bb`) failed two
  gate jobs, fixed and not re-run *(corrected 2026-09-26)*, `TestE2E_FleetUpgrade` is not a CI job and started
  from chart 1.12.8 rather than its default 1.10.48, and Kind's hostPath PV has a `0777` root
  volume root, so the ownership repair was exercised against roots the test set to `0755 root`
  beforehand (ADR 0032 Residual risks).
* ~~**`automountServiceAccountToken` is never disabled on the data pods (open)** — the whole
  pod runs under `<cr-name>-sidecar`, so the `valkey`, `sidecar` and `exporter` containers
  all carry the token although only the sidecar uses it. A compromise of the `valkey` or
  `exporter` container — **including via an attacker-chosen `spec.metrics.image`** — yields
  that token.~~ Corrected 2026-08-21: the grant it carries is no longer namespace-wide
  `pods get,list,patch`. Since [ADR 0012](0012-the-sidecar-records-its-drain-promotion-on-the-pod.md)
  D8 step 3 it is `pods: patch` restricted by `resourceNames` to this cluster's own data
  pods — still the ability to move the `instanceRole` label and to write drain stamps, but
  only on this cluster. The observer half of this bullet is **closed**: it has its own pod
  spec with `automountServiceAccountToken: false`.

  **(Closed 2026-08-27 by [ADR 0012](0012-the-sidecar-records-its-drain-promotion-on-the-pod.md)
  D8 step 4; marked here 2026-09-26.)** The data pod sets
  `automountServiceAccountToken: false` and hands the token back to the `sidecar` container
  alone through a projected volume (`sidecarTokenVolume`, `SidecarTokenVolumeName` in
  [`statefulset.go`](../../internal/builder/statefulset.go)), so `valkey`, the exporter and every
  init container — the migration-only root repair of ADR 0032 D2 included — hold no token; the
  Sentinel pod sets the flag and projects nothing. What it did **not** close: the `sidecar`
  container itself keeps the D8 step 3 grant and must, and a sidecar compromise still yields
  it; the per-pod records it could forge are ADR 0031's subject.
* **(Closed 2026-08-21) The observer shared the sidecar ServiceAccount** while making no API
  call at all. [ADR 0012](0012-the-sidecar-records-its-drain-promotion-on-the-pod.md) D8
  step 2 shipped: the observer runs under `<cr-name>-observer`, bound to no Role, mounting
  no token. A pre-existing ServiceAccount under that derived name is refused rather than
  overwritten ([ADR 0020](0020-write-only-what-the-operator-owns.md) D1, D2).
* **A generated name can be held by an object the operator did not create (partly open).**
  There is no admission webhook constraining CR names
  ([ADR 0015](0015-one-crd-validated-by-schema-only.md)), so whoever may `create valkeys`
  picks the names of every derived object. Deletes are guarded
  ([ADR 0006](0006-delete-only-what-the-operator-owns.md)); writes are guarded for the
  observer ServiceAccount and the sidecar ServiceAccount, Role and RoleBinding
  ([ADR 0020](0020-write-only-what-the-operator-owns.md)). Every other managed kind is
  still written by generated name with no ownership check — ADR 0020 D7 and its Residual
  risks name what that leaves open.
* **No egress NetworkPolicies (open).**
* **The pre-upgrade hook's cluster-wide CRD write grant (open)** — taken on every upgrade
  unless disabled.
* **`DEVELOPER.md`, the third file of the documentation standard, does not exist yet
  (open).** A documentation gap, not a hardening item: `SECURITY_ARCHITECTURE.md` records it
  in its introduction, not on its checklist.

## References

* [SECURITY_ARCHITECTURE.md](../../SECURITY_ARCHITECTURE.md) — the rule-by-rule footprint, trust boundaries and hardening checklist
* [`internal/builder/rbac.go`](../../internal/builder/rbac.go) — `BuildSidecarServiceAccount`, `BuildSidecarRole`, `BuildSidecarRoleBinding`
* [`internal/builder/networkpolicy.go`](../../internal/builder/networkpolicy.go) — the three ingress-only policies
* [`internal/builder/sentinel.go`](../../internal/builder/sentinel.go) — `DefaultServiceAccountName` for Sentinel pods
* [`internal/builder/pod_security.go`](../../internal/builder/pod_security.go) — the rootless posture of every generated pod, which superseded D9
* [`deploy/helm/valkey-operator/templates/`](../../deploy/helm/valkey-operator/templates/) — `clusterrole.yaml`, `clusterrolebinding.yaml`, `pre-upgrade-rbac.yaml`, `deployment.yaml`
* [ADR 0006](0006-delete-only-what-the-operator-owns.md) — the call-site guard that pairs with the destructive verb
* [ADR 0012](0012-the-sidecar-records-its-drain-promotion-on-the-pod.md) — the sidecar half of the trust boundary
* [ADR 0014](0014-rbac-lives-in-three-places.md) — how the grant is kept in sync across three manifests
* [ADR 0016](0016-authentication-and-tls-posture.md) — what the data plane authenticates and encrypts
* [ADR 0031](0031-a-record-the-operator-trusts-lives-in-pod-spec.md) — the per-pod records the sidecar's remaining grant could rewrite (the closed `automountServiceAccountToken` residual)
* [`internal/controller/pod_hardening.go`](../../internal/controller/pod_hardening.go) — `seccompProfileAllowed`, the allow-list that bounds a Valkey resource's `Localhost` profile (ADR 0033 D9)
* [ADR 0032](0032-generated-pods-run-rootless.md) — generated pods run rootless; supersedes D9
* [ADR 0033](0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md) — the seccomp choice, the opt-in user namespace and the operator pod's shared hardening block (D8 amendment)
* [`deploy/helm/valkey-operator/templates/_helpers.tpl`](../../deploy/helm/valkey-operator/templates/_helpers.tpl) — `valkey-operator.podHardening`, `valkey-operator.containerSecurityContext`, `valkey-operator.image`
