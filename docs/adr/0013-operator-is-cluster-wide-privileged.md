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
at — pod-level `runAsNonRoot: true` and `seccompProfile: RuntimeDefault`, container-level
`allowPrivilegeEscalation: false`, `readOnlyRootFilesystem: true` and
`capabilities: drop [ALL]`. `terminationGracePeriodSeconds: 10` sits beside them in the same
pod spec and is not a `securityContext` control. The operator holds the cluster's most
privileged token, so hardening its own runtime is the highest-value posture control
available — **and it is the existence proof for giving the workload pods the same
treatment.**

**D9 — The asymmetry between the operator and the workloads it creates is a stated decision,
not an oversight.** No pod generated by `internal/builder` sets any `securityContext`
(verified by the absence of the field across the package). Generated clusters inherit whatever
the namespace's Pod Security admission level allows.

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
  anywhere, including to the API server — where it can use the mounted sidecar token. The open
  health and exporter ports are unauthenticated surfaces reachable from anywhere in the
  cluster.
* **A `restricted`-PSA namespace will reject the generated pods outright**, so the operator
  cannot be used in a hardened namespace today. Data pods run as root with full capabilities
  unless the namespace forces otherwise.
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

On the checklist, not implemented. `readOnlyRootFilesystem` in particular conflicts with the
Valkey data path unless volumes are carved out.

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
* **Workload pods have no `securityContext` (open)** while the operator's own Deployment sets
  all five `securityContext` controls listed in D8.
* **`automountServiceAccountToken` is never disabled on the data pods (open)** — the whole
  pod runs under `<cr-name>-sidecar`, so the `valkey`, `sidecar` and `exporter` containers
  all carry the token although only the sidecar uses it. A compromise of the `valkey` or
  `exporter` container — **including via an attacker-chosen `spec.metrics.image`** — yields
  that token. Corrected 2026-08-21: the grant it carries is no longer namespace-wide
  `pods get,list,patch`. Since [ADR 0012](0012-the-sidecar-records-its-drain-promotion-on-the-pod.md)
  D8 step 3 it is `pods: patch` restricted by `resourceNames` to this cluster's own data
  pods — still the ability to move the `instanceRole` label and to write drain stamps, but
  only on this cluster. The observer half of this bullet is **closed**: it has its own pod
  spec with `automountServiceAccountToken: false`.
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
* [`deploy/helm/valkey-operator/templates/`](../../deploy/helm/valkey-operator/templates/) — `clusterrole.yaml`, `clusterrolebinding.yaml`, `pre-upgrade-rbac.yaml`, `deployment.yaml`
* [ADR 0006](0006-delete-only-what-the-operator-owns.md) — the call-site guard that pairs with the destructive verb
* [ADR 0012](0012-the-sidecar-records-its-drain-promotion-on-the-pod.md) — the sidecar half of the trust boundary
* [ADR 0014](0014-rbac-lives-in-three-places.md) — how the grant is kept in sync across three manifests
* [ADR 0016](0016-authentication-and-tls-posture.md) — what the data plane authenticates and encrypts
