---
id: T49
title: no NetworkPolicy guards the operator namespace
state: filed
severity: low         # reach to :8080 and :8081, no write path; an operator can write the policy themselves today
security: hardening
threat: "would additionally cover any pod in the cluster that reaches the operator pod's :8080 (the per-resource inventory, T48) and :8081 (health): today nothing the chart renders restricts ingress to the operator pod, or its egress"
urgency: later        # rule 4: a default-off chart template is a cheap known fix once the option is chosen
effort: S
filed-from: T31, section "Further security measures — not in this change, each open" (archive/031)
opened: 2026-09-27
decided:              # not recorded - no decision yet
done:                 # not recorded - not done
---

Filed on 2026-09-27 from the second row of the table
["Further security measures — not in this change, each open"](archive/031-generated-pods-run-as-root.md#further-security-measures--not-in-this-change-each-open)
in the archived ticket 031, which is `done` and tracks none of its rows. The operator-facing
statement of the gap is [H-14](../security/operator-pod-posture.md#h-14).

## Fact

**Verified** (read 2026-09-27):

- The chart renders no NetworkPolicy. Its templates are `_helpers.tpl`, `clusterrole.yaml`,
  `clusterrolebinding.yaml`, `crd.yaml`, `deployment.yaml`, `pre-upgrade-job.yaml`,
  `pre-upgrade-rbac.yaml`, `prometheusrule.yaml`, `service.yaml`, `serviceaccount.yaml` and
  `servicemonitor.yaml` ([`templates/`](../../deploy/helm/valkey-operator/templates/)).
- The operator pod declares `:8080` (metrics) and `:8081` (health)
  ([`deployment.yaml`](../../deploy/helm/valkey-operator/templates/deployment.yaml) lines 57 and
  60).
- The workload NetworkPolicies the operator writes are opt-in and ingress-only, and admit the
  operator namespace on the data port because the operator connects to the data plane directly
  ([ADR 0013](../adr/0013-operator-is-cluster-wide-privileged.md) D7).

**Not verified:**

- The full egress set of the operator: the API server, the Valkey and Sentinel ports
  (6379/16379, 26379/36379), and whether it needs DNS.
- How the API server's address appears to an egress rule on the CNIs users run (Service
  ClusterIP or endpoint addresses after translation); it differs by distribution and was not
  measured.
- Whether the CI Kind cluster's CNI enforces NetworkPolicy at all.

## Impact

Live on every install: the inventory of [ticket 048](048-the-operator-metrics-endpoint-is-unauthenticated.md)
is readable from any pod, and the health port answers anyone. An operator can write such a
policy today; the chart does not offer one.

## Options

- **A — an ingress-only chart policy, default off (best).** A value (name to be decided) renders
  one policy selecting the operator pod, `policyTypes: [Ingress]`, admitting `:8080` from a
  configurable peer list (the scraper) and `:8081` from anywhere, because kubelet probes come
  from the node — the reason ADR 0013 D7 leaves the sidecar health port open. The same shape as
  the workload policies.
- **B — ingress and egress:** egress to the API server, the Valkey and Sentinel ports and DNS,
  the shape the archived row proposed. The API server's addresses are not something the chart
  can know, and a wrong egress rule cuts the operator off the API server and stops every
  reconcile of the fleet.
- **C — a documented example policy, no template.**

A is marked because it covers the exposure H-14 names first (who reaches `:8080` and `:8081`)
at no risk to reconciling. Egress filtering matters only after the operator process is
compromised, and that process already holds a cluster-wide grant
([ADR 0013](../adr/0013-operator-is-cluster-wide-privileged.md) D1), so B's added failure mode
buys little.

## Decision

None yet.

## Verification

- `helm template` renders no NetworkPolicy by default and exactly one, selecting the operator
  pod, with the value on.
- On a cluster whose CNI enforces NetworkPolicy: a pod outside the peer list cannot reach
  `:8080`, the scraper can, and the operator keeps reconciling (an e2e Valkey resource reaches
  `OK`).

## History

- 2026-09-27 — filed from the row "NetworkPolicy for the operator namespace (ingress
  metrics/health only, egress API server + Valkey ports)" of archive/031. Gap
  [H-14](../security/operator-pod-posture.md#h-14) states what is missing.
