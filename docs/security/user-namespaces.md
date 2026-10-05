# User namespaces

What `spec.podSecurity.userNamespaces` buys a Valkey pod, what it needs from the cluster,
and what happens when the API server or a node cannot honour it. The decision is
[ADR 0033](../adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md) D2 and D3. The rest of the pod posture is
[workload pod posture](workload-pod-posture.md); the chart's separate switch for the
operator's own pods is [operator pod posture](operator-pod-posture.md#consequences-of-the-opt-ins).

## Opt-in, per Valkey resource

([ADR 0033](../adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md)
D2, D3). `spec.podSecurity.userNamespaces: true` sets `hostUsers: false` on the data, Sentinel and
observer pods (`applyPodHardening`); omitted or `false` leaves the field unset, so no existing
cluster changes. Toggling it moves both pod-spec hashes and rolls the tiers failover-aware
(ADR 0005 D7). Inside the user namespace the securityContext of [every generated pod](workload-pod-posture.md#the-fields-on-every-generated-pod) is unchanged — uid 999,
`drop: [ALL]`, no privilege escalation — read from the builder; that it still yields an empty
bounding set and `no_new_privs` on a node is what the e2e checks, ~~and it has not run yet~~
*(measured 2026-09-26 on Kind, in one data pod's `valkey` container: uid 999, `CapEff` and
`CapBnd` 0, `NoNewPrivs 1`)*.

## What it defends against

A process that escapes its container — a runtime or kernel
breakout — arrives on the node as an unprivileged uid from the pod's own mapped range, not as
host uid 999. Without it, uid 999 inside is uid 999 on the node: the same uid for every data and
Sentinel pod of every Valkey cluster scheduled there, and the owner of what they wrote to
node-local volumes such as Kind's `hostPath` claims — so one escaped Valkey process would own
the other clusters' files on that node (reasoned, not tested). A capability held inside the
user namespace — the repair's `CAP_CHOWN` is the only one — is honoured by the kernel only
for files whose owner maps into that namespace (Linux semantics, not measured here). That
kubelet gives every pod a distinct, non-overlapping host range is **read in
the upstream user-namespace documentation, not measured here**; the e2e checks only that
`uid_map` is not the identity map ~~, and has not run yet~~ — measured 2026-09-26 on Kind for
the `valkey` container of the three data pods and the `sentinel` container of the three
Sentinel pods of the test cluster (not the sidecar, exporter or observer containers), each in a
user namespace of its own under the `Localhost` seccomp filter, after
`TestE2E_PodHardening_UserNamespacesLocalhostSeccompAndDigest` had moved that persistent
Sentinel cluster into the user namespace in one failover-aware roll; the dataset written before
the move was intact through the idmapped mount. Where that ran and what it did not cover is
[H-20](#h-20).

## What it does not defend against

Everything that stays inside the pod: the cluster password
in the environment ([the cluster password](secrets-and-tls.md#the-cluster-password)), the dataset, the sidecar's token ([the per-instance sidecar Role](privilege-footprint.md#the-per-instance-sidecar-role)), unrestricted
egress ([isolation and tenancy](isolation-and-tenancy.md#what-does-not-hold)), and every command an authenticated Valkey client can send. It narrows no
syscall and closes no kernel bug reachable from inside the pod's own namespace. It is off
unless a CR asks for it, and the chart's `podSecurity.userNamespaces` is a separate switch for
the operator and hook pods only ([operator pod posture](operator-pod-posture.md#the-pod-and-container-fields)).

## What it needs

The node and cluster requirements are listed in the `userNamespaces` security note of
[pod security](../operations/pod-security.md#seccomp-profile-and-user-namespaces) — from the field's documentation and the ADR, not measured here beyond
the e2e's Kind node ~~(not yet run)~~ (Kubernetes 1.36.1, containerd 2.3.1, runc 1.4.2,
Linux 6.10, where it passed on 2026-09-26 — a `hostPath` volume, which says nothing about
idmap support on other file systems).

## When the API server drops the field

(ADR 0033 D3.) An API server with `UserNamespacesSupport` off —
the default before Kubernetes 1.33 — drops `hostUsers` from a pod template **without an error**
(measured in envtest on Kubernetes 1.29). Every create and update of the data StatefulSet, the
Sentinel StatefulSet and the observer Deployment — every write that carries a pod template; the
nudge is a metadata-only merge patch — goes through `writeWorkload`
([`pod_hardening.go`](../../internal/controller/pod_hardening.go)), which reads the stored template
out of the write's answer; sent `false` and stored nothing fails the step with
`errUserNamespacesDropped`, reported as `ReconcileBlocked=True/UserNamespacesUnsupported` and
phase `Error`, the message naming the gate and both ways out. The write itself is not withheld
— an image change or a TLS rotation in the same template still applies — so the pods run
**without** a user namespace and the CR says so, while `Ready` keeps reporting the data plane
(ADR 0002 D5). The next pass sees the same drift and writes again, so the report stands for as
long as the cluster drops the field, paced by the rate limiter; in `reconcileBlockedReason` it
ranks directly below `RecreateRequired` and above an admission rejection, because it too
clears only when a human acts
(gate on, or the field back to `false`). Report and release measured in envtest, `make
test-integration` green 2026-09-26.

## When a node cannot honour it

What an operator sees when the API server keeps the field and a node cannot honour it —
nothing before a pod fails to start — is the `userNamespaces` security note of
[pod security](../operations/pod-security.md#seccomp-profile-and-user-namespaces). Not measured: the migration repair inside a user
namespace — uid 0 of the namespace re-owning root-written legacy files through an idmapped
mount. The e2e moves a cluster that was rootless from birth, so no repair runs in it.

## What this does not cover

<a id="h-20"></a>

### H-20: Turn on user namespaces where every node can honour them

([ADR 0033](../adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md)
D2, D3). `spec.podSecurity.userNamespaces: true` per Valkey resource, and
`podSecurity.userNamespaces: true` in the chart for the operator and hook pods — two
separate switches, both off by default. Check first: Kubernetes 1.33 (or the
`UserNamespacesSupport` gate), containerd 2.0 or CRI-O 1.25, Linux 6.3, and a data-volume
file system with idmap support — not NFS — plus a container runtime snapshotter that can map the
ids, which containerd's `native` snapshotter cannot *(added 2026-09-27: this list lacked it; the
measurement is in [pod security](../operations/pod-security.md#seccomp-profile-and-user-namespaces))*. Turn it on for one cluster and watch it: an
API server that drops the field shows `ReconcileBlocked=True/UserNamespacesUnsupported`
and phase `Error` (the pods keep running without the namespace); a node that cannot
honour it holds the roll on a replacement that never starts (`PodAvailabilityStalled`),
and a single pod is not reported — except the data pod of a Sentinel cluster with one, whose
replacement is reported like a roll's ([ADR 0007](../adr/0007-failover-aware-rolling-update.md) D11). For the operator and the hook no Valkey resource
reports either case: a dropped field is silent, and a pod a node cannot start shows
it only in its own status and events — on `helm upgrade` the hook runs first, so the
upgrade fails there (read from the chart, not run). ~~Not yet measured on any
node: the e2e that does so has not completed,~~ *(superseded 2026-09-26)* Measured on one
node setup only, locally and not in CI: Kind with Kubernetes 1.36.1, containerd 2.3.1, runc
1.4.2 and Linux 6.10 ([what it defends against](#what-it-defends-against)); CRI-O is not covered, and the migration
repair inside a user namespace is not covered at all ([when a node cannot honour it](#when-a-node-cannot-honour-it)). It bounds an escape
from the container; it changes nothing an attacker can do inside the pod.
