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

*Added 2026-09-27 (enrichment, at `4a7543e`):*

- **A selector on the chart's selector labels also selects the pre-upgrade hook pod.**
  `valkey-operator.selectorLabels`
  ([`_helpers.tpl`](../../deploy/helm/valkey-operator/templates/_helpers.tpl) lines 46–49) is on
  the hook pod as well ([`pre-upgrade-job.yaml`](../../deploy/helm/valkey-operator/templates/pre-upgrade-job.yaml)
  lines 19–20, plus `app.kubernetes.io/component: pre-upgrade-hook`). The operator pod carries
  no `app.kubernetes.io/component` (`deployment.yaml` lines 19–23, `_helpers.tpl` lines 34–41).
  `component` `DoesNotExist` therefore selects the operator pod alone ~~.~~ *(qualified
  2026-09-27, review: unless the user's `podLabels`, which the operator pod also carries, sets
  that key; see option A)*.
- **The operator's data-plane egress, read from the code.** It dials
  `<pod>.<headless-service>.<namespace>.svc.cluster.local:<port>` (`PodAddressForComponent`,
  [`internal/health/checker.go`](../../internal/health/checker.go) lines 446–449), so it needs
  DNS. The ports are 6379 and 16379 ([`configmap.go`](../../internal/builder/configmap.go)
  line 17, [`certificate.go`](../../internal/builder/certificate.go) line 23), and 26379 and
  36379 ([`sentinel.go`](../../internal/builder/sentinel.go) line 19, `certificate.go` line 26).
  A grep for `http.Get`, `http.Client` and `http.NewRequest` over `internal/controller` and
  `internal/health`, outside tests, finds no HTTP call to a pod.
- **CI Kind:** kind v0.33.0 through `helm/kind-action`
  ([`release.yml`](../../.github/workflows/release.yml) lines 155–157) with its default CNI,
  kindnet (line 238 waits for the `kindnet` DaemonSet). No test under `test/` checks that a
  NetworkPolicy is enforced. The one e2e that touches policies only waits for them to exist
  ([`admission_recovery_test.go`](../../test/e2e/admission_recovery_test.go) lines 417–431).

**Not verified:**

- ~~The full egress set of the operator: the API server, the Valkey and Sentinel ports
  (6379/16379, 26379/36379), and whether it needs DNS.~~ *(partly answered 2026-09-27, above:
  DNS yes, and those four ports, read in `internal/controller` and `internal/health`. Other
  packages were not read.)*
- How the API server's address appears to an egress rule on the CNIs users run (Service
  ClusterIP or endpoint addresses after translation); it differs by distribution and was not
  measured.
- Whether the CI Kind cluster's CNI enforces NetworkPolicy at all. *(2026-09-27: I believe
  kindnet enforces it since kind v0.24. That was not checked in this repository or on a
  cluster. Local kind is v0.32.0, CI uses v0.33.0.)*
- *(added 2026-09-27)* That an ingress rule with `ports` and no `from` admits every source.
  This is the documented NetworkPolicy semantics and decides the empty-peer-list shape below,
  but it was not tried here.

## Impact

Live on every install: the inventory of [ticket 048](048-the-operator-metrics-endpoint-is-unauthenticated.md)
is readable from any pod, and the health port answers anyone. An operator can write such a
policy today; the chart does not offer one.

## Options

One decision, the shape of the policy. Its detail (value names, empty peer list) belongs to
option A, which is marked.

- **A — an ingress-only chart policy, default off (recommended).** A value (name to be decided)
  renders one policy selecting the operator pod, `policyTypes: [Ingress]`, admitting `:8080`
  from a configurable peer list (the scraper) and `:8081` from anywhere, because kubelet probes
  come from the node — the reason ADR 0013 D7 leaves the sidecar health port open. The same
  shape as the workload policies. *(added 2026-09-27)*
  - ~~The pod selector adds `app.kubernetes.io/component` `DoesNotExist`, so the hook pod is not
    selected (Fact).~~ *(corrected 2026-09-27, review: for an ingress-only policy the exclusion
    buys nothing and opens a hole. The hook pod declares no port
    ([`pre-upgrade-job.yaml`](../../deploy/helm/valkey-operator/templates/pre-upgrade-job.yaml)
    has no `ports`), so denying it ingress costs it nothing; replies to its own API-server
    connections are not ingress to a policy (documented semantics, not tried here). And the
    operator pod's labels include the user's `podLabels` (`deployment.yaml` lines 21–23,
    `values.yaml` line 65): a `podLabels` entry `app.kubernetes.io/component` would silently
    take the operator pod out of its own policy, fail-open. Select on the selector labels alone;
    the hook exclusion matters only to option B's egress rules.)*
  - **With the value on and no peer listed, the template omits the `:8080` rule, so nobody
    reaches `:8080`.** A rule with `ports` and no `from` would admit everyone (Not verified,
    above).
  - The render fails when `metrics.serviceMonitor.enabled` is on and no peer is listed,
    because the scrape that value renders would be cut.
- **B — ingress and egress.** Egress to the API server, the Valkey and Sentinel ports and DNS,
  the shape the archived row proposed. The API server's addresses are not something the chart
  can know, and a wrong egress rule cuts the operator off the API server and stops every
  reconcile of the fleet. *(added 2026-09-27)* It also needs kube-dns on port 53, because the
  operator dials DNS names (Fact). A selector on the selector labels alone would put the hook
  pod, which needs the API server to patch the CRD, under the same egress rules.
- **C — a documented example policy, no template.**

A is marked because it covers the exposure H-14 names first (who reaches `:8080` and `:8081`)
at no risk to reconciling. Egress filtering matters only after the operator process is
compromised, and that process already holds a cluster-wide grant
([ADR 0013](../adr/0013-operator-is-cluster-wide-privileged.md) D1), so B's added failure mode
buys little. C leaves the render-time checks above to every reader.

## Work list

**Not waiting on a decision:** nothing. Every item follows from the shape decision.

**Waiting on the decision (A):**

1. `templates/networkpolicy.yaml` and the values.
2. `helm template`: no policy by default, exactly one with the value on.
3. Rows in the README [Helm chart values](../../README.md#helm-chart-values) table.
4. H-14 in [`operator-pod-posture.md`](../security/operator-pod-posture.md) lines 95–100, and
   H-13's mitigation sentence.
5. A local Kind enforcement check (below).

Coordinate with [ticket 048](048-the-operator-metrics-endpoint-is-unauthenticated.md)
decision 1, which edits the same `deployment.yaml` and `values.yaml` lines.

**Close (ADR 0034):** the default-off operator-namespace policy is a durable default. Record
it in an ADR (an amendment of ADR 0013 D7 or a new ADR, with a line in the index). Then the
README rows and H-14. `git grep -n 'T49\|049-no-networkpolicy'` outside `docs/tickets/` (none
today), then move to `archive/`.

## Decision

None yet.

## Verification

- `helm template` renders no NetworkPolicy by default and exactly one, selecting the operator
  pod, with the value on.
- On a cluster whose CNI enforces NetworkPolicy: a pod outside the peer list cannot reach
  `:8080`, the scraper can, and the operator keeps reconciling (an e2e Valkey resource reaches
  `OK`).
- *(added 2026-09-27)* With the value on and no peer listed, no pod reaches `:8080` (negative
  control for the omitted rule). A `helm upgrade` with the value on still completes its
  pre-upgrade hook.

## History

- 2026-09-27 — review - option A no longer excludes the hook pod: for an ingress-only policy
  the exclusion buys nothing (the hook declares no port) and a `podLabels` entry
  `app.kubernetes.io/component` would take the operator pod out of its own policy. Struck in
  place; the recommendation (A) and the frontmatter are unchanged.
- 2026-09-27 — enriched - added the selector overlap with the hook pod, the operator's DNS and
  port egress read from the code, and the CI Kind/kindnet facts; option A now carries its
  empty-peer-list and hook-exclusion detail. No decision-free XS item exists; urgency, effort
  and blocked-by unchanged (rule 4 still matches first).
- 2026-09-27 — filed from the row "NetworkPolicy for the operator namespace (ingress
  metrics/health only, egress API server + Valkey ports)" of archive/031. Gap
  [H-14](../security/operator-pod-posture.md#h-14) states what is missing.
