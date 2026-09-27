---
id: T49
title: no NetworkPolicy guards the operator namespace
state: analysed       # was filed; 2026-09-27 at 84a39c2: every fact the two decisions rest on is verified at HEAD or against upstream source and documentation; what stays unverified (enforcement inside CI's Docker-in-Docker Kind, the production CNI) is what decision 2 measures
severity: low         # ~~reach to :8080 and :8081, no write path; an operator can write the policy themselves today~~ (corrected 2026-09-27 at 84a39c2: the disclosure is :8080 alone, the per-resource fleet inventory, names and health with no Secret material and no write path; :8081 answers only "ok"; an operator can write the policy themselves today)
security: hardening
threat: "would additionally cover any pod on the pod network, outside a configured peer list, that reads the operator pod's :8080 (the per-resource fleet inventory, ADR 0021): today nothing the chart renders restricts ingress to the operator pod. :8081 (it answers only 'ok') and the operator's egress (a compromised operator can delete or bypass its own policy) are deliberately not covered"  # was: "... reaches the operator pod's :8080 (the per-resource inventory, T48) and :8081 (health): today nothing the chart renders restricts ingress to the operator pod, or its egress"; corrected 2026-09-27 at 84a39c2 because the recommended shape covers :8080 only
urgency: later        # rule 4, re-derived top-down 2026-09-27 at 84a39c2: rule 1 does not match (nothing unreleased; H-14 is true; the ticket's own false :8081 reason is contradicted by documentation, and ADR 0013 D7 is not false, see Fact), rule 2 does not match (not release-gated), rule 3 does not match (severity low), rule 4 matches (a default-off chart template is a cheap known fix)
effort: S             # template, values, README rows, ADR amendment, docs and the enforcement check; moves toward M if the first CI run shows that Docker-in-Docker Kind does not enforce and decision 2's subtest needs a gate (Options, decision 2)
blocked-by: decision  # added 2026-09-27 at 84a39c2 (the field was never set, although an earlier History entry called it unchanged): decision 1 (the shape), then decision 2 (where enforcement is proven)
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

**Verified** (read 2026-09-27; re-read at `84a39c2` the same day, all cited lines hold unless
marked):

- The chart renders no NetworkPolicy. Its templates are `_helpers.tpl`, `clusterrole.yaml`,
  `clusterrolebinding.yaml`, `crd.yaml`, `deployment.yaml`, `pre-upgrade-job.yaml`,
  `pre-upgrade-rbac.yaml`, `prometheusrule.yaml`, `service.yaml`, `serviceaccount.yaml` and
  `servicemonitor.yaml` ([`templates/`](../../deploy/helm/valkey-operator/templates/)).
  *(Measured 2026-09-27 at 84a39c2: `helm template rel deploy/helm/valkey-operator --namespace
  vko` (helm v3.21.3) exits 0 and renders 2 ClusterRole, 2 ClusterRoleBinding, 1
  CustomResourceDefinition, 1 Deployment, 1 Job and 2 ServiceAccount, no NetworkPolicy.)*
- The operator pod declares `:8080` (metrics) and `:8081` (health)
  ([`deployment.yaml`](../../deploy/helm/valkey-operator/templates/deployment.yaml) lines 57 and
  60), bound by the arguments at lines 37–38.
- The workload NetworkPolicies the operator writes are opt-in and ingress-only, and admit the
  operator namespace on the data port because the operator connects to the data plane directly
  ([ADR 0013](../adr/0013-operator-is-cluster-wide-privileged.md) D7).
  *(Completed 2026-09-27 at 84a39c2: they admit the operator namespace on the Sentinel ports as
  well ([`networkpolicy.go`](../../internal/builder/networkpolicy.go) lines 77–88 for the data
  port, lines 197–207 for the Sentinel ports), and the peer is the whole namespace, matched on
  `kubernetes.io/metadata.name` alone, not the operator pod: any pod in the operator namespace
  reaches the data plane of every cluster with `spec.networkPolicy.enabled`. A chart policy on
  the operator pod does not change that. It is ADR 0013 D7's peer choice and out of this
  ticket's scope.)*

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
  [`internal/health/checker.go`](../../internal/health/checker.go) lines 446–450), so it needs
  DNS. The ports are 6379 and 16379 ([`configmap.go`](../../internal/builder/configmap.go)
  line 17, [`certificate.go`](../../internal/builder/certificate.go) line 23), and 26379 and
  36379 ([`sentinel.go`](../../internal/builder/sentinel.go) line 19, `certificate.go` line 26).
  A grep for `http.Get`, `http.Client` and `http.NewRequest` over `internal/controller` and
  `internal/health`, outside tests, finds no HTTP call to a pod.
- **CI Kind:** kind v0.33.0 through `helm/kind-action`
  ([`release.yml`](../../.github/workflows/release.yml) lines 155–157) with its default CNI,
  kindnet (line 238 waits for the `kindnet` DaemonSet). No test under `test/` checks that a
  NetworkPolicy is enforced. The one e2e that touches policies only waits for them to exist
  ([`admission_recovery_test.go`](../../test/e2e/admission_recovery_test.go) lines 417–432).

*Added 2026-09-27 (re-verification at `84a39c2`):*

- **The operator's full egress set, closed by reading.** `grep -rn -e 'net.Dial' -e 'tls.Dial'
  -e 'DialContext' -e 'Dialer{' -e 'http.Get' -e 'http.Client' -e 'http.NewRequest' -e
  'http.Post' -e '\.Dial(' --include='*.go' internal cmd api | grep -v _test.go` finds exactly one
  dialer, [`internal/valkeyclient/client.go`](../../internal/valkeyclient/client.go) lines
  398–401 (`net.Dialer`, `tls.DialWithDialer`, `net.DialTimeout`), and no HTTP client. The
  `ListenAndServe` hits of `internal/observer` and `internal/sidecar` run in other pods.
  `go.mod` lists no network client library beyond client-go and controller-runtime. The
  operator process therefore talks to the API server (client-go), to DNS, and to the four Valkey
  and Sentinel ports, and to nothing else. (Two further FQDN builders,
  [`rolling_update.go`](../../internal/controller/rolling_update.go) line 1030 and
  [`valkey_controller.go`](../../internal/controller/valkey_controller.go) line 2927, build
  hostnames the operator hands to Valkey or Sentinel, not addresses it dials.)
- **Nothing in this repository connects to the operator pod except kubelet.** The manager
  options set only `Metrics.BindAddress`, `HealthProbeBindAddress` and leader election
  ([`cmd/main.go`](../../cmd/main.go) lines 102–110); no `WebhookServer`. controller-runtime
  v0.25.1 adds its webhook server as a runnable only inside `GetWebhookServer()`
  (`pkg/manager/internal.go` lines 279–289, module cache), and nothing under `cmd/` or
  `internal/` calls it (`grep -rn -e GetWebhookServer -e 'webhook\.' cmd internal` finds one
  comment, `valkey_controller.go` line 1937). The chart renders no webhook configuration and the
  CRD has no conversion webhook. An ingress-only policy on the operator pod can therefore not
  cut reconciling. Users do connect: the scraper of `:8080`.
- **`:8081` discloses nothing but health.** Both `/healthz` and `/readyz` carry only
  `healthz.Ping` (`cmd/main.go` lines 188–195). controller-runtime v0.25.1
  `pkg/healthz/healthz.go` answers `ok` (line 104); with `?verbose` it lists the check names
  (`[+]healthz ok`, line 115), and a failing check prints `reason withheld` (line 119). No pprof
  listener exists: `PprofBindAddress` is left empty, which disables it. The disclosure H-14
  names is `:8080` alone.
- **What an ingress-isolated pod always admits.** Kubernetes documentation
  ([network-policies.md](https://github.com/kubernetes/website/blob/main/content/en/docs/concepts/services-networking/network-policies.md),
  fetched 2026-09-27), lines 69–72: "the only allowed connections into the pod are those from the
  pod's node and those allowed by the `ingress` list of some NetworkPolicy ... Reply traffic for
  those allowed connections will also be implicitly allowed"; lines 467–468: pods cannot "block
  access from their resident node". So kubelet probes need no rule by the documented semantics.
  A hostNetwork pod is not covered by that text: lines 402–412 of the same page call NetworkPolicy
  behaviour for hostNetwork pods "undefined", limited to two possibilities — the plugin applies
  policy to them like any pod, or it cannot tell them apart and treats their traffic "the same as
  all other traffic to/from the node IP". So whether a hostNetwork pod on the resident node
  reaches the operator pod is CNI-specific.
- **kindnet (CI's CNI) enforces NetworkPolicy upstream, and admits only root-owned node
  traffic.** kind v0.24.0 release notes
  (https://github.com/kubernetes-sigs/kind/releases/tag/v0.24.0): "Out-of-the-box support for
  network policy via sigs.k8s.io/kube-network-policies"; kind v0.33.0
  `pkg/build/nodeimage/const_cni.go` line 23 pins kindnetd `v20260820-69b56db7`, and lines 56–62
  grant it list/watch on `networkpolicies`; the v0.25.0–v0.33.0 notes remove nothing. The engine,
  kube-network-policies (main at `1501ac7`, 2026-09-27 — the revision inside kindnetd
  v20260820 was not pinned down), `pkg/dataplane/controller.go` lines 630–645: "Don't process
  traffic generated from the root user in the Node, it can block kubelet probes" (`meta skuid 0
  accept`, upstream issue #65), on unless a test-only field is set (line 58). That is the only
  node allowance found in its source; the evaluator's comment at
  `pkg/networkpolicy/networkpolicy.go` lines 290–295 restates the API rule, and no code
  admitting other node-originated traffic was found there (grep for `node`, not traced further).
  It defaults to `--fail-open=true` (`pkg/cmd/cmd.go` line 39): packets pass if its controller is
  not running.
- **Named ports and empty `from`.** `k8s.io/api` v0.37.1 (`go.mod` line 11)
  `networking/v1/types.go` lines 122–125: "If this field is empty or missing, this rule matches
  all sources"; lines 164–166: a port "can either be a numerical or named port on a pod".
  [`networkpolicy.go`](../../internal/builder/networkpolicy.go) lines 116–143 already rely on
  ports-only rules. kube-network-policies (main) matches a named port against the pod's container
  port names (`pkg/networkpolicy/networkpolicy.go` lines 545–547).
- **The API server's address in an egress rule is implementation-defined.** network-policies.md
  lines 196–206: "it is not defined whether this happens before or after NetworkPolicy
  processing".
- **A compromised operator process defeats any policy on its own pod.** The chart ClusterRole
  grants `networkpolicies` full CRUD cluster-wide
  ([`clusterrole.yaml`](../../deploy/helm/valkey-operator/templates/clusterrole.yaml) lines
  97–109; tracked statement:
  [privilege-footprint.md](../security/privilege-footprint.md) line 35, "Can delete any
  NetworkPolicy in the cluster") and `deployments`/`statefulsets` create/update/patch
  (clusterrole.yaml lines 73–85; privilege-footprint.md line 32, "Can replace the pod template
  ... of any Deployment or StatefulSet").
- **The pre-upgrade hook lists and patches Valkey CRs only.**
  [`cmd/migrate/migrate.go`](../../cmd/migrate/migrate.go) line 55 lists, line 86 patches; it
  has no apiextensions reference
  ([`pre-upgrade-rbac.yaml`](../../deploy/helm/valkey-operator/templates/pre-upgrade-rbac.yaml)
  lines 40–41, [`values.yaml`](../../deploy/helm/valkey-operator/values.yaml) lines 147–150).
  It declares no `ports` (`pre-upgrade-job.yaml` lines 32–41).
- **The workload policy's exporter port is open to every source**
  (`networkpolicy.go` lines 129–143, ADR 0013 D7 "Prometheus is not locatable from the CR").
  Option A below deliberately differs from that precedent for `:8080`: the operator's endpoint
  is a fleet-wide inventory ([ADR 0021](../adr/0021-per-resource-metrics-and-the-alert-that-was-missing.md)),
  an exporter covers one cluster, and the chart is set by the installer, who knows where the
  scraper runs.
- **No e2e reads the operator's endpoints.** `grep -rn -e 8080 -e 8081 -e vko_valkey -e
  port-forward -e PortForward test/` hits only `test/integration` (in-process, bound to
  `127.0.0.1:18080`). [`test/e2e/helm-values.yaml`](../../test/e2e/helm-values.yaml) has no
  network-policy key; CI installs with it
  ([`release.yml`](../../.github/workflows/release.yml) lines 349–360), and the fleet-upgrade
  e2e upgrades with it ([`fleet_upgrade_test.go`](../../test/e2e/fleet_upgrade_test.go) lines
  319–325). The CI runner step loads `br_netfilter`, `nf_conntrack`, the iptables modules and no
  `nfnetlink_queue` (`release.yml` lines 102–115).
- **Tracked text that a chart policy affects.** Becomes false when option A lands:
  [ADR 0018](../adr/0018-metrics-and-the-exporter-sidecar.md) Consequences, lines 148–150 ("none
  is written for the operator namespace"); goes stale:
  [ADR 0033](../adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md)
  lines 714–716 (lists the operator-namespace policy as "Out of scope here and open"). Stay true
  and should name the new value: ADR 0021 lines 155–157, `values.yaml` lines 106–111,
  [monitoring.md](../operations/monitoring.md) lines 82–84, and H-13's sentence at
  [operator-pod-posture.md](../security/operator-pod-posture.md) lines 97–98. `git grep -n
  'T49\|049-no-networkpolicy' -- ':!docs/tickets'` prints nothing.

**Not verified:**

- ~~The full egress set of the operator: the API server, the Valkey and Sentinel ports
  (6379/16379, 26379/36379), and whether it needs DNS.~~ *(partly answered 2026-09-27, above:
  DNS yes, and those four ports, read in `internal/controller` and `internal/health`. Other
  packages were not read.)* *(answered 2026-09-27 at 84a39c2: the whole of `cmd/`, `internal/`
  and `api/` was grepped, see Fact; the only dialer is `valkeyclient`.)*
- How the API server's address appears to an egress rule on the CNIs users run (Service
  ClusterIP or endpoint addresses after translation); it differs by distribution and was not
  measured. *(2026-09-27 at 84a39c2: the documentation states it is implementation-defined,
  network-policies.md lines 196–206; still not measured on any CNI. It no longer decides
  anything, because egress filtering is not an option, see Options.)*
- ~~Whether the CI Kind cluster's CNI enforces NetworkPolicy at all. *(2026-09-27: I believe
  kindnet enforces it since kind v0.24. That was not checked in this repository or on a
  cluster. Local kind is v0.32.0, CI uses v0.33.0.)*~~ *(corrected 2026-09-27 at 84a39c2:
  verified upstream, see Fact. Not measured on any cluster, and not inside CI's Docker-in-Docker
  runners, where kube-network-policies needs nfqueue, `release.yml` loads no `nfnetlink_queue`,
  whether the kernel autoloads it there was not checked, and the engine fails open by default. A
  negative control in CI (decision 2) measures it. Local kind is v0.32.0, CI uses v0.33.0.)*
- ~~*(added 2026-09-27)* That an ingress rule with `ports` and no `from` admits every source.
  This is the documented NetworkPolicy semantics and decides the empty-peer-list shape below,
  but it was not tried here.~~ *(corrected 2026-09-27 at 84a39c2: verified in the API type,
  `k8s.io/api` v0.37.1 `networking/v1/types.go` lines 122–125, and relied on by
  `networkpolicy.go` lines 116–143; still not tried on a cluster.)*
- *(added 2026-09-27 at 84a39c2)* The production CNI's handling of node-originated traffic to an
  ingress-isolated pod, and of named ports. The repository cannot see it.
- *(added 2026-09-27 at 84a39c2)* Which readers of `:8080` are not pods: an API-server pod-proxy
  read (`kubectl get --raw /api/v1/namespaces/<ns>/pods/<pod>:8080/proxy/metrics`) arrives from
  the control-plane host, and a hostNetwork scraper on another node from that node's address;
  both would need an `ipBlock` peer. Inferred from the documented semantics, not measured.
- *(added 2026-09-27 at 84a39c2)* The metrics Service selects on the selector labels alone
  ([`service.yaml`](../../deploy/helm/valkey-operator/templates/service.yaml) lines 22–23), so
  the hook pod matches it during an upgrade; the hook has no `metrics` port and cannot serve.
  What the EndpointSlice controller records for it was not checked. Unrelated to the policy.

## Impact

Live on every install: the inventory of [ticket 048](048-the-operator-metrics-endpoint-is-unauthenticated.md)
is readable from any pod, and the health port answers anyone. An operator can write such a
policy today; the chart does not offer one. *(precised 2026-09-27 at 84a39c2: the health port
answers only `ok`, or the check names with `?verbose` (Fact), so the exposure that matters is
`:8080`. On a CNI that does not enforce NetworkPolicy no policy, the chart's or the operator's
own, changes any of this.)*

## Options

Two decisions, in order: the shape of the policy, then where its enforcement is proven.
Egress filtering and a default-on policy are not among the options (History, 2026-09-27 at
84a39c2, with reasons).

### Decision 1: the shape of the operator pod's policy

**Mechanism.** Today the chart renders no NetworkPolicy (Fact). The operator pod listens on
`:8080`, the controller-runtime metrics server with a bind address only
([`cmd/main.go`](../../cmd/main.go) line 105), serving the `vko_valkey_*` fleet inventory
(ADR 0021), and on `:8081`, which answers `ok` (lines 188–195). Nothing in this repository
connects to the pod except kubelet: no webhook server, no call to either port (Fact). The chart's
selector labels are on the operator pod and on the pre-upgrade hook pod, which declares no port.
The choice decides whether the chart restricts who reaches `:8080` on a CNI that enforces
NetworkPolicy. It does not change: authentication of the endpoint (ADR 0018 D9/D10, ticket 048
decision 2); the workload policies (ADR 0013 D7); the operator's RBAC; the default install,
because the chart's own objects default to off (`values.yaml` lines 101–104); any running pod,
because a separate template adds one object and leaves the Deployment's pod template untouched
(`deployment.yaml` lines 13–23 carry no values checksum), so turning the value on restarts
neither the operator nor any Valkey pod and puts no dataset at risk; and nothing at all on a CNI
that does not enforce.

- **A — an ingress-only chart policy, default off (recommended).** One value renders one
  NetworkPolicy in the release namespace:
  - `podSelector` = the chart's selector labels alone. It also selects the hook pod, which costs
    the hook nothing: it declares no port, and replies to its own outbound connections are
    implicitly allowed (network-policies.md lines 59–72; not run on a cluster). No
    `component` exclusion, because a `podLabels` entry `app.kubernetes.io/component` would take
    the operator pod out of its own policy, fail-open (`deployment.yaml` lines 21–23,
    `values.yaml` line 65).
  - `policyTypes: [Ingress]`.
  - `:8080`: a rule on the named port `metrics` whose `from` is a user-supplied list of
    `NetworkPolicyPeer` entries, passed through `toYaml`, so a pod selector, a namespace selector
    or an `ipBlock` (for readers that are not pods, Not verified) all fit. **With an empty list
    the rule is omitted**, never rendered as `from: []`, which would admit every source (Fact).
    Then no pod on the pod network reaches `:8080`; how much of the resident node still does is
    CNI-specific (all of it by the documentation, hostNetwork pods undefined there; root-owned
    sockets on kube-network-policies, Fact). The named port follows whatever the Deployment declares, so the
    rule stays right whichever way ticket 048 decision 1 goes; if that decision turns the
    endpoint off, the `metrics` container port is dropped and the template omits the `:8080` rule
    (a named rule matching no port would also be harmless).
  - `:8081`: a rule on the named port `health` with no `from`, the shape of the sidecar health
    rule (`networkpolicy.go` lines 116–127). Not because probes need it — by the documentation
    the node is always admitted — but because the only thing a closed `:8081` hides is `ok`,
    while it would make the liveness of the fleet's only reconciler (`values.yaml` line 3,
    `replicaCount: 1`; liveness every 20 s, `deployment.yaml` lines 62–67) depend on how a CNI
    implements the node allowance, which at least one engine got wrong once
    (kube-network-policies issue #65, now a uid-0 bypass only, Fact). This is a fixed detail of
    A, not an owner decision.
  - Render refusals: the render fails when the peer list is empty and `metrics.service.enabled`,
    `metrics.serviceMonitor.enabled` or `metrics.prometheusRule.enabled` is on — the Service
    would have no reachable consumer, the ServiceMonitor would scrape nothing, and
    `ValkeyMetricsAbsent` ([`prometheusrule.yaml`](../../deploy/helm/valkey-operator/templates/prometheusrule.yaml)
    lines 161–162) would fire forever. The same three values ticket 048 decision 1 (1D) refuses
    with the endpoint off, so both refusals read alike (by reading; not rendered).
  - Value names: default to something that says it is the operator pod's policy, for example
    `operatorNetworkPolicy.enabled` (default `false`) and `operatorNetworkPolicy.metricsFrom`
    (default `[]`), or carry a disambiguating comment against the CR field
    `spec.networkPolicy.enabled` the way `values.yaml` lines 90–92 do for `metrics`. A naming
    default, not an owner decision.

  Cost S: `templates/networkpolicy.yaml`, the values and their comment, the refusals in
  `_helpers.tpl`, README rows, the ADR amendment, the docs, and the enforcement check of
  decision 2. Consequences: on an enforcing CNI only the listed peers (and the resident node)
  read `:8080`; a chart value lives in the Helm install definition, not on a CR, so Flux's
  prune of hand-set CR annotations does not touch it; on a non-enforcing CNI it does nothing,
  silently, which is why decision 2 needs a negative control.
- **C — a documented example policy, no template.** A section under `docs/operations/` shows the
  policy; installers apply it themselves (in production, as one more manifest in a Flux
  Kustomization). Cost XS: one docs section, no chart surface, no render logic. Consequences:
  the copied `podSelector` must match `app.kubernetes.io/name` and `instance`, which depend on
  `nameOverride` and the release name (`_helpers.tpl` lines 4–6, 46–49); a copy that matches no
  pod isolates nothing and fails open silently. Nothing refuses the scraper-without-peer
  combinations, and the example is checked by no render (ticket 058 renders the chart, not a
  docs snippet).

**A is marked** because it is the only option that makes H-14's `:8080` exposure closable from
the chart at no risk to reconciling: the operator pod has no ingress dependency besides kubelet
(Fact), its selector is the chart's own selector labels and cannot fall out of step with the
Deployment, and it refuses at render time the combinations it would break. It beats the
runner-up C because C's hand-copied selector depends on `nameOverride` and the release name and
fails open without a signal when it stops matching, and C carries no refusal; A's extra cost over
C is one template, two values and three equality checks.

### Decision 2: where enforcement is proven

**Mechanism.** A NetworkPolicy is worth something only where the CNI enforces it, and a green
test without a refused request cannot tell "enforced and harmless" from "vacuous". CI's e2e legs
install the chart with `test/e2e/helm-values.yaml` on kind v0.33.0 with kindnet (Fact). kindnet
enforces upstream, but inside CI's Docker-in-Docker runners its nfqueue path is unverified and its
default is fail-open (Fact). No e2e reads `:8080` or `:8081`, so turning the value on in
`helm-values.yaml` breaks no existing e2e (by reading test code, not run), and the fleet-upgrade
e2e then upgrades from 1.12.8 (which ignores the unknown key) into the policy, exercising the hook
under it. The outcome is decided by the cluster dataplane, which only the e2e tier has; that
extends [ADR 0017](../adr/0017-test-and-ci-policy.md) D2's line ("does the API server or a real
Valkey change the outcome"), it does not follow from its wording. The choice changes what proves
the policy works and for how long. It does not change the policy.

- **L — one local Kind check, recorded here.** On `make kind-create` (overlayfs, three workers):
  value on, a pod outside the peer list is refused on `:8080`, a listed pod is answered, an e2e
  Valkey resource reaches `OK`, a `helm upgrade` completes its hook. Cost XS, by hand.
  Consequence: a one-time measurement; a later change that makes the operator pod need ingress
  (a webhook server, for example) merges green and breaks only the installs that turned the
  value on.
- **E — CI: the value on in `test/e2e/helm-values.yaml` plus one e2e subtest with a negative
  control (recommended).** The peer list names a test-pod label; the subtest asserts that a pod
  outside the list (not hostNetwork) is refused on `:8080` and a listed pod is answered
  (ADR 0017 D11 positive control, D29 fail loudly). Every leg then runs the full suite with the
  operator pod under the policy. Cost S: one values block and one subtest. Consequences: the
  default-off path is covered only by a render check (ticket 058, by hand until then). If the
  first CI run shows that Docker-in-Docker kindnet does not enforce, the negative control fails;
  the subtest then needs a gate like `E2E_REQUIRE_USER_NAMESPACES`, which by ADR 0017 D5 no CI
  leg could set, the enforcement half would run only on local Kind, and E would give no more
  enforcement coverage than L (effort toward M). The positive half — the suite reconciles and the
  hook completes with the value on — runs in CI either way.

**E is marked** because it re-proves both halves on every PR instead of once, and the regression
it catches (new ingress needed by the operator pod) is invisible to every other tier. It beats
the runner-up L because L is a single measurement no later change re-runs; L's one advantage,
certainty that enforcement is observable at all, is exactly what E's first CI run measures, and
the outcome where CI cannot enforce is written down above rather than assumed away.

## Work list

**Not waiting on a decision:** nothing in code. The `:8081` rule, the empty-list omission, the
named ports, the render refusals and the value naming are fixed details of option A (Options),
built only with it.

**Waiting on decision 1 (A):**

1. `templates/networkpolicy.yaml`, the values and their comment, the refusals.
2. `helm template`: no policy by default, exactly one with the value on, the three refusals, and
   the rendered Deployment identical with and without the value. Add these cases to
   [ticket 058](058-no-ci-gate-renders-the-chart.md)'s matrix when that gate lands.
3. Rows in the README [Helm chart values](../../README.md#helm-chart-values) table.
4. H-14 in [`operator-pod-posture.md`](../security/operator-pod-posture.md) lines 100–105, and
   H-13's mitigation sentence (lines 97–98), which stays true and should name the value. H-14's
   "or where it connects" then says that the operator pod's egress is deliberately not filtered,
   and why (the RBAC reason, Fact).
5. ~~A local Kind enforcement check (below).~~ *(corrected 2026-09-27 at 84a39c2: where
   enforcement is proven is decision 2; under L a local Kind check, under E the CI subtest.)*

~~Coordinate with [ticket 048](048-the-operator-metrics-endpoint-is-unauthenticated.md)
decision 1, which edits the same `deployment.yaml` and `values.yaml` lines.~~ *(corrected
2026-09-27 at 84a39c2: option A adds its own template and values and edits no line of
`deployment.yaml`. The coupling with [ticket 048](048-the-operator-metrics-endpoint-is-unauthenticated.md)
decision 1 is the port and the mode: the `:8080` rule uses the named port `metrics` and is
omitted while that decision's switch has the endpoint turned off, and the two tickets' render
refusals name the same three values. Ticket 048 corrected its side the same day, and asks to be
told the value name of this ticket (its work list, "Waiting on decision 1").)*

**Close (ADR 0034):** the default-off operator-namespace policy is a durable default. Record
it in an ADR (an amendment of ADR 0013 D7 or a new ADR, with a line in the index). Then the
README rows and H-14. `git grep -n 'T49\|049-no-networkpolicy'` outside `docs/tickets/` (none
today), then move to `archive/`. *(extended 2026-09-27 at 84a39c2)* The ADR amendment also
records: egress filtering of the operator pod refused (the "egress API server + Valkey ports"
half of the archived 031 row), with the RBAC reason (Fact; it does not
refuse [H-10](../security/isolation-and-tenancy.md#h-10), egress for data pods, which hold no
NetworkPolicy grant); a default-on policy refused (ADR 0018 D9 calls the open endpoint posture);
and, for ADR 0013 D7's open health ports, the robustness reason next to the probe sentence
(which is true but does not by itself require opening the port). Also update ADR 0018
Consequences lines 148–150 (becomes false), ADR 0033 lines 714–716 (goes stale), and let ADR 0021
lines 155–157, `values.yaml` lines 106–111 and monitoring.md lines 82–84 name the value.

## Decision

None yet.

## Verification

- `helm template` renders no NetworkPolicy by default and exactly one, selecting the operator
  pod, with the value on; the rendered Deployment is byte-identical either way; each of the
  three refusals fails the render with an empty peer list.
- On a cluster whose CNI enforces NetworkPolicy: a pod outside the peer list cannot reach
  `:8080`, the scraper can, and the operator keeps reconciling (an e2e Valkey resource reaches
  `OK`).
- *(added 2026-09-27)* With the value on and no peer listed, no pod reaches `:8080` (negative
  control for the omitted rule). A `helm upgrade` with the value on still completes its
  pre-upgrade hook. *(precised 2026-09-27 at 84a39c2: "no pod" means no pod on the pod network;
  the probe pod of every negative control must not be hostNetwork, because the resident node is
  admitted and hostNetwork traffic may be treated as node traffic, see Fact.)*

## History

- 2026-09-27 — re-verified at 84a39c2 - checked every Fact line at HEAD (all hold; locations
  re-read, `admission_recovery_test.go` 417–432, `checker.go` 446–450, H-14 now
  `operator-pod-posture.md` 100–105 after `bcc63c9` added the H-13 correction), re-rendered the
  chart (`helm template rel deploy/helm/valkey-operator --namespace vko`, helm v3.21.3: exit 0,
  no NetworkPolicy), grepped every dialer in `cmd/`, `internal/`, `api/` (one, `valkeyclient`),
  and read upstream: network-policies.md (node and reply traffic, implementation-defined
  rewriting), kind v0.24.0 notes and v0.33.0 `const_cni.go`, kube-network-policies main
  `1501ac7` (uid-0 bypass, fail-open default, named ports), `k8s.io/api` v0.37.1 types, and
  controller-runtime v0.25.1 (lazy webhook server, healthz output). No container was started.
  **Found false or outdated:** struck in place where the text survives (frontmatter, Not
  verified, work list); the Options section was rewritten, so its superseded wording is quoted
  here verbatim. Option A's reason for `:8081`: "`:8081` from anywhere, because kubelet probes
  come from the node — the reason ADR 0013 D7 leaves the sidecar health port open" (the node is
  always admitted, so probes need no rule; the port stays open for another reason, Options).
  Option A: "With the value on and no peer listed, the template omits the `:8080` rule, so
  nobody reaches `:8080`" (no pod on the pod network does; the resident node does). Option B:
  "the hook pod, which needs the API server to patch the CRD" (it lists and patches Valkey CRs).
  The recommendation: "A is marked because it covers the exposure H-14 names first (who reaches
  `:8080` and `:8081`)", and the threat line's `:8081` and egress (an overclaim relative to the
  recommended shape, which covers `:8080` only, not a false description of today's gap, so
  urgency rule 1 does not match). Also: the T48 coordination line (A edits no `deployment.yaml`
  line); work list item 4's H-14 lines (location drift, fixed directly); the Not-verified items on the egress set, kindnet and
  empty `from`, now answered. A review claim that the resident node's admission covers
  hostNetwork pods by the documentation was checked and not taken: network-policies.md lines
  402–412 call hostNetwork behaviour undefined (Fact). Fact bullet 3 completed (the operator namespace
  is admitted on the Sentinel ports too). Not filed: the audit's proposed finding against ADR
  0013 D7's probe sentence — it is true, the open health port is a sound robustness choice, and
  the missing reason goes into the D7 amendment at close. **Options:** rewritten as two
  decisions. Removed: **B** (ingress and egress) — a compromised operator can delete its own
  policy or egress from a workload it creates (`clusterrole.yaml` 73–85, 97–109), so the rule
  protects nothing, while a wrong API-server rule stops the fleet's only reconciler; **D**
  (added and removed in this pass: the policy on by default) — ADR 0018 D9 calls the open
  endpoint posture, not a defect, the chart cannot know the scraper, and it would cut scrapes or
  fail every ServiceMonitor render on upgrade; **":8081 closed"** (the audit's decision on the
  health port, no rule on `:8081`) — its only gain is hiding `ok`, against making the singleton
  operator's liveness depend on a CNI's node handling, so it is not a real choice and the open
  rule became a fixed detail of A; the struck hook-exclusion sub-bullet of A (reason in the
  review entry below). Added: decision 2 (L local once, E CI with negative control, E
  recommended; it replaces work list item 5's fixed "local Kind"). The recommendation of
  decision 1 is unchanged (A); its justification now names `:8080` only and the runner-up C.
  A's detail gained named ports, the extended refusal set (Service and PrometheusRule too, as
  ticket 048's 1D), `ipBlock` peers, the no-restart statement and the value-naming default.
  Close list extended (ADR 0018 148–150, ADR 0033 714–716, ADR 0021, `values.yaml`,
  monitoring.md, the refusals of B and D, the egress half of the archived 031 row); work list
  item 4 now also rewrites H-14's "or where it connects". **Frontmatter:** state filed -> analysed (every fact
  the decisions rest on is verified; enforcement in CI and production is decision 2's
  measurement); threat and severity comment rewritten to `:8080` only; `blocked-by: decision`
  added (it was never set, so the enriched entry's "blocked-by unchanged" was vacuous); urgency
  later (rule 4) and effort S re-derived and unchanged, effort comment notes the move toward M.
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
