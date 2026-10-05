# Installing the operator

The two installation paths, the chart settings that need more than a row of the
[Helm chart values](../../README.md#helm-chart-values) table, and uninstalling. The
prerequisites and the shortest working install are in the
[fast start](../../README.md#fast-start); moving an installed operator to a new release is
[upgrading.md](upgrading.md).

## From the chart repository

The release pipeline packages the chart and publishes it to
`https://guided-traffic.github.io/valkey-operator/` (the `gh-pages` branch, written by
[`.github/workflows/build.yml`](../../.github/workflows/build.yml)):

```bash
helm repo add valkey-operator https://guided-traffic.github.io/valkey-operator/
helm repo update
helm install valkey-operator valkey-operator/valkey-operator \
  --namespace valkey-operator-system \
  --create-namespace
```

Add `--version <chart version>` to install a specific release instead of the newest one.

## From a checked-out tree

```bash
helm install valkey-operator deploy/helm/valkey-operator \
  --namespace valkey-operator-system \
  --create-namespace
```

Either path installs the CRD, the operator's permissions and the operator from the same
chart ([upgrading.md](upgrading.md#the-supported-path) says why that matters later). Check
that the operator is up:

```bash
kubectl -n valkey-operator-system rollout status deployment/valkey-operator
```

## Chart settings that need more than a row

The complete list of values, with their defaults, is the
[Helm chart values](../../README.md#helm-chart-values) table. `podSecurity` is explained in
[pod-security.md](pod-security.md#the-operators-own-pods), `valkeyPodSecurity` in
[pod-security.md](pod-security.md#localhost-profiles-need-the-operators-allow-list),
and `metrics.*` — the operator's own endpoint — in
[monitoring.md](monitoring.md#operator-metrics-and-alerting).

### `maxConcurrentReconciles`

`maxConcurrentReconciles` bounds how far one unhealthy cluster can slow down the rest:
a reconcile pass dials the pods of its cluster with a 5 s timeout each, so with a single
worker a cluster whose pods stopped answering delays every other Valkey resource in the
fleet. Passes for the *same* resource stay serialised at any value. Raise it for large
fleets, lower it to reduce concurrent API-server load
([ADR 0019](../adr/0019-reconcile-concurrency-and-the-cost-of-a-stuck-pass.md)).

### `image.digest`

**`image.digest`** pins the operator image. With it set the chart renders
`repository:tag@digest` for the operator, the pre-upgrade hook and `--operator-image` —
so the sidecar in every data pod and the observer run the pinned image too. The render
fails unless the value is `sha256:` followed by 64 lowercase hex characters. Setting or
changing it changes the sidecar image of every data pod and the observer's image, which
moves the pods exactly like a new operator tag (see
[upgrading.md](upgrading.md#what-an-upgrade-does-to-running-clusters)). The chart
ships it empty; why that matters is [H-12](../security/operator-pod-posture.md#h-12).

## Uninstall

```bash
kubectl delete valkey --all --all-namespaces   # do this knowingly, see below
helm uninstall valkey-operator --namespace valkey-operator-system
```

The CRD is a normal chart template with no `helm.sh/resource-policy: keep`, so
`helm uninstall` deletes it — and deleting a CRD removes every `Valkey` CR with
it, which garbage-collects the StatefulSets, Services and ConfigMaps those CRs
own. PersistentVolumeClaims created for `spec.persistence` are **not** removed
(the StatefulSets set no PVC retention policy): the data stays on disk and is
reattached when a cluster of the same name is created again.
