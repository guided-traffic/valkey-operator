# Operator pod posture

How the operator Deployment and the pre-upgrade hook Job run, how the chart that renders
them is checked, and what the operator's own metrics endpoint discloses. The decisions are
[ADR 0033](../adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md) for the pod posture and [ADR 0021](../adr/0021-per-resource-metrics-and-the-alert-that-was-missing.md) for the endpoint. The pods
the operator generates are [workload pod posture](workload-pod-posture.md); what the
operator's ServiceAccount may do is [privilege footprint](privilege-footprint.md).

## The pod and container fields

The operator Deployment and the pre-upgrade hook Job render the same two helpers since
2026-09-26
([`_helpers.tpl`](../../deploy/helm/valkey-operator/templates/_helpers.tpl),
[ADR 0033](../adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md)
D6), verified in the templates:

| Level | Field | Value |
|---|---|---|
| pod (`valkey-operator.podHardening`) | `runAsNonRoot`, `runAsUser`, `runAsGroup`, `fsGroup` | `true`, 65532, 65532, 65532 — the distroless `nonroot` user, now named instead of inherited from the image |
| pod | `seccompProfile` | `podSecurity.seccompProfile`: `RuntimeDefault` # default, or `Localhost` with `localhostProfile`. Any other type, `Localhost` without a path, or a path without `Localhost` fails the render — `Unconfined` cannot be rendered. Since 2026-09-26 so does a `localhostProfile` that starts with `/` or has a `..` element (`..` inside a file name passes) — the path rule the CRD applies to `spec.podSecurity` ([validation](validation.md#what-that-means-in-practice)), which for these two pods the API server would otherwise apply only when it validates the Deployment or hook Job ([seccomp profiles](seccomp-profiles.md#what-a-cr-author-chooses)) |
| pod | `hostUsers` | `false` only with `podSecurity.userNamespaces: true` # default `false` |
| pod | `enableServiceLinks` | `false` |
| pod | `automountServiceAccountToken` | `true`, stated: both pods call the API server |
| container (`valkey-operator.containerSecurityContext`) | `privileged`, `allowPrivilegeEscalation`, `readOnlyRootFilesystem`, `capabilities` | `false`, `false`, `true`, `drop: [ALL]` |
| image (`valkey-operator.image`) | `image.digest` | empty # default. When set ([how](../operations/installation.md#imagedigest)), the same reference reaches the operator, the hook **and** `--operator-image` / `OPERATOR_IMAGE`, so the sidecar and observer containers the operator generates run the pinned image too |

The Deployment keeps `terminationGracePeriodSeconds: 10`. Ports 8080 (metrics) and 8081
(health), both plain HTTP and unauthenticated. The chart's `podSecurity` values do not reach the
Valkey pods; those take `spec.podSecurity` per resource ([workload pod posture](workload-pod-posture.md#rootless-with-no-option)), and
`valkeyPodSecurity.allowedSeccompLocalhostProfiles` bounds which `Localhost` profile that field
may name ([seccomp profiles](seccomp-profiles.md#the-localhost-allow-list); the operator's own `podSecurity.seccompProfile` is not checked against it).

## Consequences of the opt-ins

Three consequences of the opt-ins, read from the templates and not run: `podSecurity.userNamespaces`
on a cluster whose nodes cannot honour it leaves the operator pod — and on `helm upgrade` first the
hook pod, which runs before the Deployment is updated — unable to start, and only that pod's own
status and events say why, since no Valkey resource can report on the operator; a `Localhost`
profile missing on the node does the same; and an API server
with `UserNamespacesSupport` off drops `hostUsers` from these two templates as silently as from
the operator's own writes — but the `UserNamespacesUnsupported` report ([user namespaces](user-namespaces.md#when-the-api-server-drops-the-field)) covers only
the workloads the operator writes, so for its own pod nothing says the namespace is missing.
How to check the stored field by hand is [the operator's own pods](../operations/pod-security.md#the-operators-own-pods). The chart was
checked with `helm lint` and `helm template` by hand only (defaults, `image.digest`, userns,
`Localhost`, and each refused value — since 2026-09-26 including the allow-list's and the
operator's own `localhostProfile` path check: `/etc/op.json`, `../op.json` and `profiles/../op.json`
fail the render, `profiles/op.json` and `profiles/..op.json` render into the Deployment and the
hook Job). What CI renders of the chart, and what it does not, is [H-15](#h-15).

## What `:8080` discloses

Besides controller-runtime's own counters it serves one
set of `vko_valkey_*` series per `Valkey` resource
([`internal/metrics/collector.go`](../../internal/metrics/collector.go),
[ADR 0021](../adr/0021-per-resource-metrics-and-the-alert-that-was-missing.md)):
namespace, resource name, phase, condition types with their reasons, replica
counts, and the operator version that last wrote status. That is an inventory of
the fleet and its health. It carries **no Secret material and no spec contents** —
no password, no image, no host, no TLS material.

The chart can render an optional `<release>-metrics` Service and ServiceMonitor for
this endpoint (`metrics.service.enabled`, `metrics.serviceMonitor.enabled`, both
default `false`). Neither changes reachability: the container port is declared with
or without them, so anything that can route to the operator pod already reads
`:8080`. What they add is a stable name and a scrape target.

## What this does not cover

<a id="h-12"></a>

### H-12: Pin the operator image by digest

The release pipeline does not stamp the digest of the image it pushes into
the chart, so the default stays empty and the operator, the hook and, through
`--operator-image`, every sidecar and the observer run by tag
([the pod and container fields](#the-pod-and-container-fields)). Pinning is left to the
installer: [`image.digest`](../operations/installation.md#imagedigest).

<a id="h-13"></a>

### H-13: Treat the operator metrics endpoint as public unless moved or disabled, and know that it now names every Valkey resource

By default it binds
`:8080` in plain HTTP with no authentication filter, and since
[ADR 0021](../adr/0021-per-resource-metrics-and-the-alert-that-was-missing.md)
the payload is an inventory of the fleet and its health ([what `:8080` discloses](#what-8080-discloses)), not
only controller-runtime counters. `--metrics-bind-address` is applied since
the ADR 0018 D8 fix, so the endpoint can be moved or switched off (`=0`) from
the chart; wherever it binds it stays unauthenticated — controller-runtime's
`WithAuthenticationAndAuthorization` filter is a separate trade, not taken, and needs a
`TokenReview`/`SubjectAccessReview` grant ([ADR 0018](../adr/0018-metrics-and-the-exporter-sidecar.md)
D9/D10). A NetworkPolicy for the operator namespace is the only control that
works without changing either.

<a id="h-14"></a>

### H-14: Add a NetworkPolicy for the operator namespace

The chart ships none, so nothing
restricts who reaches the operator pod's `:8080` and `:8081`, or where it connects.

<a id="h-15"></a>

### H-15: Render the chart in CI

The chart's refusals — a seccomp type other than
`RuntimeDefault`/`Localhost`, a `Localhost` without a path or a path without it, an
`image.digest` that is not `sha256:<64 hex>`, and, added later on 2026-09-26, an entry of
`valkeyPodSecurity.allowedSeccompLocalhostProfiles` that is empty, starts with `/`, holds
a `,` or a `..` element, as well as an operator/hook `podSecurity.seccompProfile.localhostProfile`
that starts with `/` or has a `..` element — and the operator and hook pod posture
were checked with `helm lint` and `helm template` by hand on 2026-09-26. In CI ~~only the
default values are rendered~~ the chart is rendered only by the e2e job's `helm install`
(`.github/workflows/release.yml`), with [`test/e2e/helm-values.yaml`](../../test/e2e/helm-values.yaml) — image, resources,
leader election and the non-empty allow-list; `podSecurity` and `image.digest` stay at
their defaults *(amended 2026-09-26)*. No CI gate renders
the digest, user-namespace or `Localhost` paths or checks a refusal, so a template change
that breaks one of them would not turn a PR red. On the default path, a change that drops
a control Pod Security `restricted` requires would be caught by the new e2e subtest that
dry-runs that label on the operator namespace — ~~which has not run yet~~ green on Kind
2026-09-26, and on every run of the final image, locally, ~~not in CI~~ and in CI's
single-node E2E legs on `e6a9d7c` *(corrected 2026-09-27: this said "not in CI"; the subtest
is part of the hardening e2e, whose CI run on `e6a9d7c` skipped only its user-namespace
subtest, [ADR 0017](../adr/0017-test-and-ci-policy.md) D5)*; one that drops
`enableServiceLinks: false`, `privileged: false`, the read-only root filesystem or the
pinned uid would not (read from the test, not run).
