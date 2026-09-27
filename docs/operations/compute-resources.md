# Compute resources

Which containers of the generated pods state CPU and memory requests, and what that
means under a cpu/memory `ResourceQuota`. The fields — `spec.resources`,
`spec.sentinel.resources`, `spec.metrics.resources`, `spec.observer.resources` — and
their defaults are in the [CRD reference](../../README.md#crd-reference).

## Sentinel pods

Which Sentinel containers take `spec.sentinel.resources` is in its
[reference row](../../README.md#specsentinel). Setting it:

```yaml
spec:
  sentinel:
    enabled: true
    replicas: 3
    resources:            # example; omitted means no requests and no limits
      requests:
        cpu: 20m          # example
        memory: 32Mi      # example
      limits:
        memory: 64Mi      # example
```

## Data pods under a `ResourceQuota`

A `ResourceQuota` on cpu/memory still refuses the **data** pods: their sidecar and init
containers state no resources, and there is no field for them. Neither they nor the
Sentinel containers get a default, deliberately — a guessed memory limit is an OOM kill in
the sidecar that carries the drain promotion
([ADR 0033](../adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md)
D7).
