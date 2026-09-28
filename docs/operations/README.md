# Operating the Valkey Operator

Reference for the people who install the operator and run `Valkey` resources with it.
[README.md](../../README.md) is the short version — what the operator is, how to start it,
and the complete CRD and Helm chart values reference. Everything it links to for detail is
here. A page here explains a setting; it never restates the reference tables, which live in
the README and nowhere else
([ADR 0035](../adr/0035-the-readme-advertises-the-reference-lives-under-docs.md)).

| Page | Read it when |
|---|---|
| [installation.md](installation.md) | You are installing the operator from the chart repository or a checked-out tree, tuning `maxConcurrentReconciles`, pinning the operator image by digest, or uninstalling |
| [upgrading.md](upgrading.md) | You are moving the operator to a new release. **Read it before upgrading from an operator that still ran Valkey as root** — it says what rolls, what restarts and what to do first |
| [examples.md](examples.md) | You want a complete manifest to start from: standalone, the TLS variants, HA with Sentinel, the observer, metrics, authentication |
| [authentication.md](authentication.md) | You turn on password authentication, or change the password of a running cluster |
| [tls.md](tls.md) | You turn TLS on, need the port map, keep plaintext ports open, rotate certificates, or a Sentinel-aware client fails certificate verification |
| [persistence.md](persistence.md) | You choose a persistence mode, want to change the storage of an existing cluster, or a data pod fails its `check-data-writable` pre-flight |
| [rolling-updates.md](rolling-updates.md) | A rolling update paused, gave up handing the master back to pod-0, waits on a pod that does not come up, or holds the delete of the outgoing master |
| [disruption-budgets.md](disruption-budgets.md) | You want a node drain to leave enough data pods and the Sentinel quorum running |
| [anti-affinity.md](anti-affinity.md) | You want the pods of a cluster spread across nodes or zones |
| [compute-resources.md](compute-resources.md) | A cpu/memory `ResourceQuota` refuses the generated pods |
| [pod-security.md](pod-security.md) | You enforce Pod Security `restricted`, pick a seccomp profile, opt into user namespaces, list `Localhost` profiles for the operator, or harden the operator's own pods |
| [monitoring.md](monitoring.md) | You scrape the exporter sidecar or the operator's own metrics, or wire up its alerts |
| [observer.md](observer.md) | You run the cluster observer and need to know what it checks and what makes it unready |
| [status.md](status.md) | A condition or the phase of a `Valkey` resource needs explaining |

## The other documentation

| Where | What |
|---|---|
| [README.md](../../README.md) | What the operator is, the fast start, the naming conventions, the complete CRD and Helm chart values reference |
| [docs/security/](../security/) | Trust boundaries, the privilege footprint, where the password and the TLS material live, and what each mechanism leaves open. Reporting a vulnerability is [SECURITY.md](../../SECURITY.md) |
| [docs/adr/](../adr/README.md) | Why the operator behaves the way it does, and what was rejected |
| [docs/developer/](../developer/README.md) | Changing the code |
