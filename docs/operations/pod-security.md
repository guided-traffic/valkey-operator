# Pod security

The security posture of every pod the operator generates, the two settings of
`spec.podSecurity` that depend on the nodes (the seccomp profile and the user namespace),
the operator's allow-list for `Localhost` profiles, and the chart settings for the
operator's own pods. The fields and their defaults are in the
[`spec.podSecurity`](../../README.md#specpodsecurity) and
[Helm chart values](../../README.md#helm-chart-values) tables. The decisions are
[ADR 0032](../adr/0032-generated-pods-run-rootless.md) (rootless, no option) and
[ADR 0033](../adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md)
(seccomp profile, user namespace, allow-list). Upgrading from an operator that still ran
Valkey as root is a one-time migration: [upgrading.md](upgrading.md#one-time-migration-to-rootless-pods).
What each of these defends against and what it leaves open is the security design:
[workload pod posture](../security/workload-pod-posture.md),
[seccomp profiles](../security/seccomp-profiles.md),
[user namespaces](../security/user-namespaces.md) and
[operator pod posture](../security/operator-pod-posture.md).

## The fixed rootless posture

The rootless posture itself — uid/gid 999 (65532 in the observer), `runAsNonRoot`, all
capabilities dropped, no privilege escalation, `privileged: false`, a read-only root filesystem,
`enableServiceLinks: false` — is fixed and has no field (see the one-time migration under
[upgrading.md](upgrading.md#one-time-migration-to-rootless-pods)). `spec.podSecurity` sets the two things on
top of it that depend on the nodes, for the data, Sentinel and observer pods alike
([ADR 0033](../adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md)).

## Seccomp profile and user namespaces

```yaml
spec:
  podSecurity:
    seccompProfile:
      type: RuntimeDefault                     # default
    userNamespaces: false                      # default
```

```yaml
spec:
  podSecurity:
    seccompProfile:
      type: Localhost                          # example
      localhostProfile: profiles/valkey.json   # example; must exist on every node and be on the allow-list
    userNamespaces: true                       # example; needs node support, below
```

Omitting the block, or stating the defaults explicitly, renders the same pod spec — nothing
rolls. Changing the profile (to one the [allow-list](#localhost-profiles-need-the-operators-allow-list)
admits) or `userNamespaces` changes the pod-spec hash of both
StatefulSets: the data tier rolls failover-aware and the Sentinel tier rolls as for any
template change; the observer Deployment, where
enabled, is rewritten and restarts. As with every pod-spec change, the only data pod of a
single-replica cluster, with or without Sentinel, is restarted, and without persistence it comes
back empty.
Turning `userNamespaces` off again rolls the pods back out of their user namespace.
`hostUsers` is compared exactly, not as a subset: when a mutating admission policy adds
`hostUsers: false` to the template of a resource without the opt-in, the operator writes
the template without it again on every pass — on such a cluster, opt in.

> **Security note — `Localhost`:** the profile is node state the operator cannot see.
> Install it on every node a data, Sentinel or observer pod can be scheduled on, and have it
> put on [the operator's allow-list](#localhost-profiles-need-the-operators-allow-list),
> **before** referencing it. A pod on a node without the file does not start. A roll then holds on
> the first replacement, and [`PodAvailabilityStalled`](status.md#podavailabilitystalled) names it once
> [`syncTimeout`](../../README.md#specrollingupdate) has passed while the pods not yet replaced keep
> serving; a single-pod cluster without Sentinel shows phase `Provisioning`
> instead. The profile has to allow every syscall of every generated container —
> `valkey-server`, `valkey-sentinel`, the sidecar, the exporter, the observer, the init
> scripts and the `chown` of the migration-only `fix-data-ownership` container; one that is too strict fails
> a container at the syscall it blocks.

> **Security note — `userNamespaces`:** it needs support on every node a pod can land on:
> Kubernetes 1.33 or later (1.30 with the `UserNamespacesSupport` feature gate),
> containerd 2.0 or CRI-O 1.25 or later, Linux 6.3 or later (idmapped `tmpfs`), and idmap
> support in the file system of every data volume — **NFS has none** — and in the container
> runtime's snapshotter: containerd's `native` snapshotter cannot map the ids (measured on a
> Kind node: "container ID … cannot be mapped to a host ID"), overlayfs can. The securityContext
> inside the namespace is unchanged (uid 999, 65532 in the observer; `drop: [ALL]`; no
> privilege escalation). Two ways it fails, and how each shows:
>
> - **The API server has the feature gate off** (the default before Kubernetes 1.33). It
>   stores the StatefulSet or Deployment **without** `hostUsers`, and without an error. The
>   operator reads the stored template out of the answer to each of those writes and
>   reports the loss:
>   `ReconcileBlocked=True` with reason `UserNamespacesUnsupported`, phase `Error`, and a
>   message naming the gate. The rest of the template still applies, so the pods run —
>   without a user namespace — and `Ready` keeps reporting the data plane. It clears when the
>   gate is enabled or `userNamespaces` is set back to `false`. The opt-in still moves both
>   pod-spec hashes, so every data and Sentinel tier rolls once onto a template stored
>   without `hostUsers`, and setting it back to `false` rolls them again (read from the
>   code, not measured).
> - **The API server keeps the field, a node cannot honour it.** Not detected before a pod
>   fails to start: the roll holds on the first replacement and `PodAvailabilityStalled`
>   names it after `syncTimeout` (read from the code, not measured with a user namespace).
>   A single-pod cluster without Sentinel does not carry that condition; its replacement
>   shows as phase `Provisioning`.

## No AppArmor profile

There is nothing to configure: no AppArmor profile is set on any generated pod, and why is
[what is deliberately not set](../security/workload-pod-posture.md#what-is-deliberately-not-set).

## Localhost profiles need the operator's allow-list

Why the operator bounds the choice, and what the list does and does not defend against, is
[the `Localhost` allow-list](../security/seccomp-profiles.md#the-localhost-allow-list).
The operator therefore writes a `Localhost` profile only when it is listed in its
`--allowed-seccomp-localhost-profiles` flag, set by the chart value
[`valkeyPodSecurity.allowedSeccompLocalhostProfiles`](../../README.md#helm-chart-values),
as paths relative to the kubelet's seccomp directory. The chart renders
`--allowed-seccomp-localhost-profiles` with the entries joined by commas, and only when the
list is non-empty; an empty entry, a leading `/`, a `,` or a `..` path element fails the
render. The list is **empty by default, which refuses every `Localhost` profile**;
`RuntimeDefault` needs no entry. Decided 2026-09-26 ([ADR 0033](../adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md)
D9).

A `Valkey` naming a profile that is not listed is refused before anything of its workloads is
written, on create and on update alike:

- `ReconcileBlocked=True` with reason `SeccompProfileNotAllowed`, phase `Error`, and a message
  naming the profile and the flag.
- **The workloads are not written**: the data StatefulSet, the Sentinel StatefulSet and the
  observer Deployment are neither created nor updated — the one write left is the nudge
  annotation the operator bumps on a StatefulSet that is short of pods, which touches no spec.
  A new resource gets none of them; an existing one keeps its stored pod template, and every
  other change to those workloads — `spec.image`, `spec.replicas`, `spec.resources` — waits
  until the refusal clears. The ConfigMaps, Services and other resources are still reconciled.
- **Everything checked before the write is still reported.** The refusal sits at each
  workload's write, not at the start of its reconcile step (moved 2026-09-26): a data or
  Sentinel StatefulSet under the generated name that this `Valkey` does not own is still
  reported as `ForeignObject` with its Warning Event (a foreign observer Deployment gets its
  Warning Event), and a StatefulSet whose `volumeClaimTemplates` no longer match
  `spec.persistence` still sets [`StorageSpecNotApplied`](persistence.md#changing-storage-on-an-existing-cluster) — both
  `ForeignObject` and `RecreateRequired` rank above `SeccompProfileNotAllowed` in
  `ReconcileBlocked`. A StatefulSet that already runs a profile the list no longer holds is
  reported on every pass, not only when something else would change. On a new TLS cluster the
  refusal appears once cert-manager has issued the certificate Secret: until then the operator
  writes no StatefulSet anyway.
- It clears when the profile is added to the list — a `helm upgrade`, which restarts the
  operator, because the flag is read at startup — or when the spec names a listed profile or
  `RuntimeDefault`.

**Removing an entry does not move running pods off that profile.** Their StatefulSets and
observer Deployment keep the template they have — a pod deleted or evicted comes back under
the same profile — and each resource naming it stays blocked until its spec names a listed
profile or `RuntimeDefault`.

> **Security note:** which profiles to list, and what a listed name does not bound, is
> [H-19](../security/seccomp-profiles.md#h-19).

How this behaviour was verified, and which parts of it are read from the code rather than
tested, is recorded under [the `Localhost` allow-list](../security/seccomp-profiles.md#the-localhost-allow-list).

## The operator's own pods

**The operator and hook pods carry a fixed posture** that `podSecurity` only extends:
uid, gid and `fsGroup` 65532 with `runAsNonRoot`, `enableServiceLinks: false`, and on the
container `privileged: false`, no privilege escalation, a read-only root filesystem and
`drop: [ALL]`. `automountServiceAccountToken: true` is stated rather than defaulted,
because both pods talk to the API server
([ADR 0033](../adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md)
D6).

- `podSecurity.seccompProfile` — `RuntimeDefault` or `Localhost`; `Unconfined`, any other
  type, `Localhost` without a path and a path without `Localhost` all fail the render.
  `localhostProfile` is a path relative to the kubelet's seccomp directory: one that starts
  with `/` or has a `..` path element (`../op.json`, `profiles/../op.json`) fails the render
  too — the rule the CRD applies to `spec.podSecurity` — while `..` inside a file name
  (`profiles/..op.json`) passes. It is **not** checked against
  `valkeyPodSecurity.allowedSeccompLocalhostProfiles`, which bounds the Valkey pods only.
  **Security note:** a `Localhost` profile must exist on every node the operator pod can
  be scheduled on, or the pod does not start.
- `podSecurity.userNamespaces` — sets `hostUsers: false` on both pods. Same node
  requirements as [`spec.podSecurity.userNamespaces`](#seccomp-profile-and-user-namespaces). **Security
  note:** the operator's `UserNamespacesUnsupported` report covers only the pods it
  generates; for its own Deployment and the hook Job nothing reports an API server that
  dropped the field (check `kubectl -n valkey-operator-system get deploy valkey-operator -o
  jsonpath='{.spec.template.spec.hostUsers}'` — `false` when stored, empty when dropped).
- Neither value reaches the Valkey pods; those take `spec.podSecurity` on each `Valkey`
  resource, bounded by [the operator's allow-list](#localhost-profiles-need-the-operators-allow-list).
