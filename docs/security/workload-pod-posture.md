# Workload pod posture

The `securityContext` and pod-level fields that every data, Sentinel and observer pod the
operator renders carries, what they change for a compromised process, and how the operator
keeps them on the template. The decisions are [ADR 0032](../adr/0032-generated-pods-run-rootless.md) (rootless, with no option)
and [ADR 0033](../adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md) (the rest of what a pod manifest can express). The seccomp choice is
[seccomp profiles](seccomp-profiles.md), the opt-in user namespace
[user namespaces](user-namespaces.md), and how pods an earlier operator built reach this
posture [rootless migration](rootless-migration.md). The operator's own pods are
[operator pod posture](operator-pod-posture.md).

## Rootless, with no option

Since 2026-09-26 every pod template the operator renders is rootless, with no CRD field and no
opt-out — `spec.podSecurity`, added the same day, chooses the seccomp profile and a user namespace
and cannot turn the rootless posture off; the migration repair ([rootless migration](rootless-migration.md#fix-data-ownership-the-one-root-process)) is the one root container it
still adds
([ADR 0032](../adr/0032-generated-pods-run-rootless.md) D1, superseding
[ADR 0013](../adr/0013-operator-is-cluster-wide-privileged.md) D9). Before that no generated
pod set a `securityContext`, and because every container on the Valkey image sets `command:` —
which replaces the entrypoint that would have dropped to the `valkey` user — `valkey-server`,
`valkey-sentinel` and the init scripts ran as uid 0 under `Unconfined` seccomp; for
`valkey-server`, measured in Docker on both pinned lines, with fourteen capabilities
(`NET_RAW`, `DAC_OVERRIDE` and `SETUID` among them) and `NoNewPrivs: 0`.

[ADR 0033](../adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md)
(2026-09-26, same unreleased release) adds two per-resource choices under `spec.podSecurity` — the
seccomp profile and an opt-in user namespace — and states the rest of what a pod manifest can
express instead of leaving it to API defaults. Both helpers of
[`pod_security.go`](../../internal/builder/pod_security.go) end in `applyPodHardening`, so the fields
below reach every data, Sentinel and observer pod.

## The fields on every generated pod

| Pod | Pod level | Every container and init container |
|---|---|---|
| data `<cr>-N` | `runAsNonRoot: true`, `runAsUser: 999`, `runAsGroup: 999`, `fsGroup: 999`, ~~`seccompProfile: RuntimeDefault`~~ `seccompProfile` = `GetSeccompProfile()` (`RuntimeDefault` # default, or the `Localhost` profile of `spec.podSecurity.seccompProfile`), `enableServiceLinks: false`, `hostUsers: false` only with `spec.podSecurity.userNamespaces: true` (unset # default) | `privileged: false` *(stated since 2026-09-26)*, `allowPrivilegeEscalation: false`, `readOnlyRootFilesystem: true`, `capabilities.drop: [ALL]` |
| Sentinel `<cr>-sentinel-N` | same as data | same |
| observer | `runAsNonRoot: true`, ~~`seccompProfile: RuntimeDefault` — no uid: the operator image's distroless `nonroot` user (65532) is numeric, so kubelet can verify it; no `fsGroup`: no data volume~~ *(superseded 2026-09-26, ADR 0033 D4)* `runAsUser`, `runAsGroup`, `fsGroup: 65532` — the operator image's numeric `nonroot` user (`OperatorUID`), pinned rather than inherited from whatever an image built from another base declares; the `fsGroup` makes the optional TLS Secret volume readable to that group — and the same `seccompProfile`, `enableServiceLinks` and `hostUsers` as data | same |

`hostNetwork`, `hostPID` and `hostIPC` are not set on any generated pod: they are plain booleans
whose zero value is `false`, the API has no way to carry an explicit `false`, and
`TestPodHardening_DefaultsOnEveryTemplate` fails if a builder ever sets one.
`automountServiceAccountToken` stays as [the per-instance sidecar Role](privilege-footprint.md#the-per-instance-sidecar-role) describes: `false` on every generated pod,
the data pod projecting its token into the sidecar alone.

- **One walk, not per-container code.** `applyValkeyPodSecurity` and `applyObserverPodSecurity`
  ([`pod_security.go`](../../internal/builder/pod_security.go)) run last in each builder over the
  assembled `PodSpec`, so a container added later inherits the posture by being in the pod.
  The unit tier evaluates every rendered template of the topology × TLS × auth × metrics ×
  persistence matrix with the checks the API server's PodSecurity admission runs
  (`k8s.io/pod-security-admission`,
  [`pod_security_test.go`](../../internal/builder/pod_security_test.go)). The matrix is
  evaluated without the `spec.podSecurity` opt-ins; the three templates of one cluster with both
  opt-ins are evaluated separately (`TestPodHardening_OptInsReachEveryPodKind`,
  [`pod_hardening_test.go`](../../internal/builder/pod_hardening_test.go); `make test-unit` green
  2026-09-26).
- **One uid per pod.** The sidecar and the exporter run as 999 too, not as their images'
  users (65532, 59000), so the data volume has one owner. No generated pod shares its process
  namespace, so the common uid does not let one container see, signal or trace another's
  processes, and the ServiceAccount token stays a mount of the sidecar container alone
  ([the per-instance sidecar Role](privilege-footprint.md#the-per-instance-sidecar-role)).
- **`fsGroup: 999` with `fsGroupChangePolicy` unset** (= `Always`): `OnRootMismatch` inspects
  only the volume root and would skip files a later root writer left beneath a correctly
  owned one.
- **`readOnlyRootFilesystem: true` on every container**, not only where the data path allows
  it: every path a process writes is a mounted volume.

## Stated rather than defaulted

([ADR 0033](../adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md)
D4).

- `privileged: false` on every container and init container, the repair included
  (`restrictedContainerSecurityContext`, `dataOwnershipRepairContainer`). It is the API default;
  stated so that the object reads complete and a scanner does not have to know the default, and
  compared by the drift check, so an out-of-band `privileged: true` on a template is converged
  back.
- `enableServiceLinks: false` on every generated pod. kubelet otherwise injects
  `<SERVICE>_SERVICE_HOST`, `<SERVICE>_SERVICE_PORT` and the Docker-link `<SERVICE>_PORT*`
  variables for every Service of the namespace that has a cluster IP into every container — an
  inventory of the namespace that no process here reads, and a name-collision surface for the
  variables they do read. The `KUBERNETES_SERVICE_*` and `KUBERNETES_PORT*` variables of the
  `kubernetes` Service in the `default` namespace are injected regardless (read in kubelet
  `getServiceEnvVarMap`, Kubernetes v1.36.4).

## Images by digest

([ADR 0033](../adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md)
D5). Until 2026-09-26 a digest-pinned `spec.image` could not be deployed at all:
`ExtractVersionFromImage` returned `sha256:<64 hex>` as the `app.kubernetes.io/version` label, 71
characters with a colon, and the API server refuses such a label on every object carrying it. It
now returns the tag of `repo:tag@sha256:…`, an empty value for a digest-only reference, and never
the digest; every case of `TestExtractVersionFromImage` is checked with `IsValidLabelValue`. The
exporter default
`DefaultMetricsExporterImage` is pinned to the digest of the multi-arch image index behind
`v1.66.0`, read with `docker buildx imagetools inspect` on 2026-09-26, so a re-pushed tag cannot
change what runs next to the password. The sidecar and the observer run the operator image,
pinned when the chart's `image.digest` is set ([operator pod posture](operator-pod-posture.md#the-pod-and-container-fields)). Nothing requires a digest: a tag in
`spec.image` or `spec.metrics.image` is pulled by tag as before.

## Resources

([ADR 0033](../adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md)
D7). Which containers take `spec.sentinel.resources`, which state no resources and have no
field, and why none of them gets a default is [compute resources](../operations/compute-resources.md).
*(Corrected 2026-09-27: this section said "no container gets a default"; the observer keeps
its 50m/64Mi request default, `GetObserverResources` in `api/v1/valkey_types.go`, and ADR 0033
D7 added no default rather than removing that one.)* A namespace with a cpu/memory
`ResourceQuota` therefore still refuses the data pods ([H-18](#h-18)).

## What is deliberately not set

([ADR 0033](../adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md)
D8). No AppArmor profile, on the generated pods or on the operator's: kubelet refuses a pod
that requests any AppArmor profile other than `Unconfined` on a node where AppArmor is not
enabled (`isRequired` in `pkg/security/apparmor/helpers.go` and the `Validate` refusal in
`validate.go`, read in upstream Kubernetes v1.36.4, not measured on a node), so an explicit
`RuntimeDefault` would keep every generated pod from starting on any node without AppArmor —
the SELinux-based distributions among them — while a runtime on an AppArmor node already applies
its default profile to a container that names none (runtime behaviour, neither read in source nor
measured here). No `seLinuxOptions`, no
`runtimeClassName`, no sysctls: each is cluster-specific, and nothing asked for them.

## What it changes, and what it does not

A compromised process in a generated pod holds no
capability, cannot regain one through a setuid binary or file capabilities in its image
(`no_new_privs`), runs under a syscall filter, finds nothing writable outside its mounted
volumes, and — only where `spec.podSecurity.userNamespaces` is on — is an unprivileged uid on
the node. It still holds everything the container is given: the cluster password in its
environment ([the cluster password](secrets-and-tls.md#the-cluster-password)), the dataset, and unrestricted egress ([isolation and tenancy](isolation-and-tenancy.md#what-does-not-hold)).

## The drift comparison checks only what the operator sets

`podSpecChanged`/`containerChanged`
(both StatefulSets) and `ObserverDeploymentHasChanged` compare the pod- and container-level
fields the builder sets, with subset semantics, so a field a mutating admission policy adds is
not a drift the operator rewrites on every pass (ADR 0032 D5). One field is deliberately not a
subset: a live template may not *add* a capability, so an out-of-band `NET_RAW` grant is
converged back. Everything the builder leaves unset is not compared at all — container-level
`runAsUser`, `runAsNonRoot` and `seccompProfile` included, which the restricted container
posture leaves to the pod level, and `fsGroupChangePolicy`. An out-of-band or admission-added
`runAsUser: 0` or `seccompProfile: Unconfined` on a container, or an `OnRootMismatch` that
narrows kubelet's `fsGroup` re-owning to the volume root, therefore stays on the template
until the operator writes it for another reason. Checked read-only on wds18 only (2026-09-26,
ADR 0032 residual risks): no Kyverno mutate rule there rewrites a StatefulSet template. A
namespace enforcing `restricted` refuses a pod carrying either regardless ([H-22](rootless-migration.md#h-22)).

Since 2026-09-26 the comparison also covers `privileged` (subset, like the other container
fields) and `enableServiceLinks` (`containerSecurityContextChanged`, `podHardeningChanged`); the
pod-level profile was already compared by type and `localhostProfile` (`seccompProfileDiffers`),
so a `Localhost` path edited out of band is converged back. One more
field is deliberately **exact**: `hostUsers`. Opting out leaves the desired field unset, and a
subset comparison would never converge the persisted `false` back — the `capabilities.add`
argument, for a field whose unset value is the weaker one (ADR 0033 D2). Its cost, read from
`podHardeningChanged` and not tested: a mutating policy that adds `hostUsers: false` to the
StatefulSet or Deployment template of a CR that did not opt in is fought over, one template write
per pass; ask for it through `spec.podSecurity.userNamespaces` instead. A policy that mutates
Pods at creation does not touch the templates and is not fought over.

## What this does not cover

<a id="h-16"></a>

### H-16: Do not treat the rootless posture as proven on your runtime

What a compromised process in a generated pod holds, as
[what it changes](#what-it-changes-and-what-it-does-not) states it, is measured only under
containerd on Kind and under Docker. Whether another runtime gives a process of the same
template the same, and nothing more, is not measured, and the operator would not notice if it
did not: it reads the templates and pod specs it wrote, never what a runtime made of them.
*(Corrected 2026-09-27: this gap said the posture ran on a node locally only, not in CI, and
carried the run log of 2026-09-26; the CI legs run the same e2e suite, and the run log is kept
in the ADRs named at the end.)*

- **Proven, and where.** The Pod Security `restricted` checks over every rendered template in
  the unit tier ([`pod_security_test.go`](../../internal/builder/pod_security_test.go)); the
  templates surviving API-server defaulting in envtest
  ([`test/integration/pod_security_test.go`](../../test/integration/pod_security_test.go));
  both pinned Valkey lines run under
  `--user 999:999 --read-only --cap-drop ALL --security-opt no-new-privileges` in Docker
  (`make test-image-tools`). On a node, the e2e suite, `TestE2E_PodSecurity_RestrictedNamespace`
  and the hardening e2e included: locally on Kind (Kubernetes 1.36.1, containerd), and in CI on
  Kind (Kubernetes 1.33.4, containerd), where the hardening e2e skips its user-namespace half
  because a pod with `hostUsers: false` does not start there ([user namespaces](user-namespaces.md)).
- **Local only.** The migration itself, `TestE2E_FleetUpgrade`, is not a CI job. It ran only
  locally, and only from released chart 1.12.8, never from its default start 1.10.48.
- **Not covered at all.** CRI-O's smaller default capability set, and OpenShift — **its
  `restricted-v2` SCC refuses a fixed `runAsUser: 999` outside the
  namespace's UID range**, so these pods are not admitted there; nothing in this
  repository targets OpenShift today.

Before relying on the posture on your runtime, check it there: a server-side dry run of
`enforce=restricted` on the namespace ([H-22](rootless-migration.md#h-22)), and `Uid`, `CapEff`
and `NoNewPrivs` in `/proc/1/status` of the `valkey` container of a data pod — uid 999, no
effective capability, `NoNewPrivs: 1`.

The runs are recorded in [ADR 0032](../adr/0032-generated-pods-run-rootless.md) Status and
Residual risks (the posture, the migration, CRI-O and OpenShift),
[ADR 0033](../adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md)
Status (the hardening runs) and [ADR 0017](../adr/0017-test-and-ci-policy.md) D31 and D5 (the CI
legs, the user-namespace skip).

<a id="h-17"></a>

### H-17: Pin `spec.image` and `spec.metrics.image` by digest

A digest in `spec.image` is
deployable since 2026-09-26 (`repo:tag@sha256:…` keeps the tag as the version label; a
digest-only reference yields an empty one). The exporter default is already pinned to
`v1.66.0`'s index digest; Renovate does not track
`DefaultMetricsExporterImage`, so that pin ages until someone moves it by hand.

<a id="h-18"></a>

### H-18: Know that a cpu/memory `ResourceQuota` still refuses the data pods

Set
`spec.resources`, `spec.metrics.resources`, `spec.observer.resources` and, new,
`spec.sentinel.resources` (every Sentinel container, the init container included).
The sidecar and the data pod's init containers state no requests or limits and have no
field; decided so on purpose (ADR 0033 D7: no guessed defaults, since an OOM-killed
sidecar breaks the drain promotion).
