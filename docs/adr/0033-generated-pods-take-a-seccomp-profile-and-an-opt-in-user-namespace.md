# ADR 0033: Generated pods take a seccomp profile and an opt-in user namespace, and the operator's own pods carry the same posture

## Status

Accepted. Date: 2026-09-26. Decided by Hans during the T31/T32 work on `feat/rootless`
([`local_T31-generated-pods-run-as-root.md`](../tickets/local_T31-generated-pods-run-as-root.md),
section "Extension 2026-09-26"), in three answers: the seccomp profile is configurable as
`RuntimeDefault` or `Localhost` and never `Unconfined`; user namespaces are opt-in everywhere;
Sentinel gets a resources field without a default, and no container gets a new default.

Amended 2026-09-26, later the same day: **D9 is new — the operator writes a `Localhost` profile
only when its allow-list names it, and the list is empty by default.** The risk was put to Hans
after D1 was implemented: Pod Security `restricted` accepts every `Localhost` profile, so under
D1 as first written whoever may create a Valkey resource could run its pods under any profile
file an administrator ever put on a node, an allow-by-default one included. He first answered
"document only" — the operator keeps no allow-list, and an administrator who wants the choice
narrower writes an admission policy — and then reversed that in the same session and decided
the allow-list, default-deny. Both "document only" and removing `Localhost` are recorded under
*Alternatives Considered*. D1 is amended in place (`Unconfined` is refused by name; a `Localhost`
profile only through D9; a second CEL rule refuses an absolute path and a `..` element), D3's
ranking sentence makes room for D9's reason, and the *Consequences* and *Residual risks*
bullets that stated the superseded stance are struck through where they stood.

Amended 2026-09-26, the third time that day: **D9's gate moved from the head of the three
workload steps to the write.** At the head it hid a foreign StatefulSet (no `ForeignObject`),
froze the `StorageSpecNotApplied` level and skipped the TLS material record; the residual risk
that said so is closed below. In the same change the chart refuses an absolute or `..`
`podSecurity.seccompProfile.localhostProfile` for the operator's own pods at render (D6, D9
*Scope*), and the `localhostProfile` field documentation names the allow-list (D9). The e2e of
this ADR found a pre-existing split-brain bug in the Sentinel rolling update, decided as
[ADR 0025](0025-a-split-brain-warning-means-one-that-did-not-resolve-itself.md) D9 (*Residual
risks*, Verification). *(Added 2026-09-26.)* ADR 0025 D9 was amended later the same day — its
window carries its own 90 s clock (`ownFailoverInFlight`), and the failover state is armed in one
write together with its timestamp (`setFailoverTriggered`,
[ADR 0010](0010-every-rolling-update-wait-is-bounded.md) D14) — and the final e2e run found a
fixture defect in `TestE2E_SidecarFailoverDrainMaster`; neither changes a decision of this ADR,
both change what its Verification records.

It builds on [ADR 0032](0032-generated-pods-run-rootless.md) and ships in the same, still
unreleased, release: every pod-spec change below rides the roll ADR 0032 already causes on
every data and Sentinel tier (on a persistent data tier the first of its two), so it adds no
roll of its own to an operator upgrade.

Implemented:

- `spec.podSecurity` (`seccompProfile`, `userNamespaces`) and `spec.sentinel.resources` in
  [`valkey_types.go`](../../api/v1/valkey_types.go), with the enum and the first CEL rule in the
  CRD (the second, the path rule, is listed with D9 below);
- the pod-level and container-level fields in
  [`pod_security.go`](../../internal/builder/pod_security.go) (`applyPodHardening`,
  `restrictedContainerSecurityContext`, both posture functions), their drift comparisons
  (`podHardeningChanged`, `containerSecurityContextChanged`);
- the dropped-`hostUsers` report in
  [`pod_hardening.go`](../../internal/controller/pod_hardening.go) (`writeWorkload`) and its
  `ReconcileBlocked` reason;
- the digest fixes: `ExtractVersionFromImage`
  ([`labels.go`](../../internal/common/labels.go)), `DefaultMetricsExporterImage`, and
  `image.digest` in the chart;
- the chart's operator and hook pods
  ([`_helpers.tpl`](../../deploy/helm/valkey-operator/templates/_helpers.tpl),
  `valkey-operator.podHardening`, `valkey-operator.containerSecurityContext`,
  `valkey-operator.image`);
- *(since the D9 amendment)* the allow-list: the flag `--allowed-seccomp-localhost-profiles` and
  `profileList` in [`cmd/main.go`](../../cmd/main.go), `ValkeyReconciler.AllowedSeccompLocalhostProfiles`,
  `seccompProfileAllowed` and `errSeccompProfileNotAllowed` in
  [`pod_hardening.go`](../../internal/controller/pod_hardening.go), the gate ~~at the head of~~
  at the write of *(moved 2026-09-26, D9)* `reconcileStatefulSet`, `reconcileSentinelStatefulSet`
  and `reconcileObserverDeployment`, `ReasonSeccompProfileNotAllowed` and its rank in
  [`reconcile_blocked.go`](../../internal/controller/reconcile_blocked.go); the chart value
  `valkeyPodSecurity.allowedSeccompLocalhostProfiles` with the helper
  `valkey-operator.allowedSeccompLocalhostProfiles`; the CEL path rule on `SeccompProfileSpec`;
  *(added 2026-09-26)* the `LocalhostProfile` doc comment in
  [`valkey_types.go`](../../api/v1/valkey_types.go) naming the allow-list, regenerated into the
  description of both CRD copies; the render-time path refusal for the operator's own profile in
  `valkey-operator.podHardening`, and *(added 2026-09-26)* the comment on
  `podSecurity.seccompProfile.localhostProfile` in
  [`values.yaml`](../../deploy/helm/valkey-operator/values.yaml) stating it ("not absolute and
  without a '..' element, or the render fails").

~~Verification is recorded under *Residual risks*: unit and integration (envtest 1.29) run; the
e2e on Kind (Kubernetes v1.36.1, the local cluster of ADR 0032) **not yet run** at the time of
writing. The container runtime and kernel versions of that node are not recorded in this
repository.~~ *(Superseded 2026-09-26 by the runs recorded under Residual risks.)* ~~In short:
on 2026-09-26 unit, lint, cyclo and integration (envtest 1.29) ran green, and the fleet-upgrade
e2e and both full e2e suites ran on Kind — Kubernetes 1.36.1, containerd 2.3.1, runc 1.4.2,
Linux 6.10 — all of them **before D9 and the CEL path rule were added**. Of D9,~~ ~~only the unit
tier has run~~ ~~*(amended 2026-09-26)* the unit tier (an uncached `make test-unit` run) and the
chart renders by hand have run; its integration test, the new CEL rows and its e2e subtest have
not.~~ *(Superseded 2026-09-26 by the final runs.)* ~~In short: one operator image built from the
final code — D9 with the gate at the write, the CEL path rule and ADR 0025 D9 included — ran on
Kind (Kubernetes 1.36.1, containerd 2.3.1, runc 1.4.2, Linux 6.10): the fleet-upgrade e2e from
1.12.8, both full suites 53/53 (Valkey 9 and Valkey 8) and two more Valkey 8 runs of the hardening
and restricted-namespace e2e, all green, D9's refusal subtest green on every run.~~ *(Relabelled
2026-09-26: that image predates ADR 0025 D9's own clock and its one-write arming, so that run is
the one before the final one, not the final one.)* In short, two e2e runs on Kind (Kubernetes
1.36.1, containerd 2.3.1, runc 1.4.2, Linux 6.10), each on one operator image:

- **The run before the final one** — D9 with the gate at the write, the CEL path rule and ADR 0025
  D9 in its first form (no clock of its own): the fleet-upgrade e2e from 1.12.8, both full suites
  53/53 (Valkey 9 and Valkey 8) and two more Valkey 8 runs of the hardening and
  restricted-namespace e2e, all green.
- **The final run** — the final operator code, ADR 0025 D9's own 90 s clock and the one-write
  arming of ADR 0010 D14 included; the e2e fix below came after it: the fleet-upgrade e2e from
  1.12.8 green, the full suite 53/53 on Valkey 8 and **52/53 on Valkey 9**, and two more Valkey 8
  runs of the hardening and restricted-namespace e2e green. The one failure,
  `TestE2E_SidecarFailoverDrainMaster`, is a fixture defect of that test, not a finding against
  this ADR: its delete subtest passed in 0.38 s because every wait after deleting the master was
  already met by the terminating old master, which kubelet keeps Ready
  ([ADR 0026](0026-a-pod-being-deleted-is-not-available.md)), and its data check then read
  `DBSIZE 0` from that pod's empty replacement. Diagnosed from the test code and its timing and
  supported by a watcher on green runs; the red run's pod logs were lost with the CR, and the
  operator log of that cluster shows no operator action between its creation and its deletion.
  Whether that run's new master held every key was not observed.
  Fixed afterwards by waiting for the replacement's new UID (`waitForPodRecreated`,
  [ADR 0017](0017-test-and-ci-policy.md) D50) and green 8 of 8 on Valkey 9, run alone.

D9's refusal subtest is part of the hardening test and was green on every run of both. The
integration tier (envtest 1.29), D9's test and the CEL path rows included, ran green repeatedly —
*(precised 2026-09-26)* on the tree before the gate moved to the write and before ADR 0025 D9,
which is when the recorded runs were made (read from the run logs' and the source files'
timestamps); ~~on the final code that tier has run only inside the CI-parity run named below~~
*(updated 2026-09-26)* it ran green again inside the CI-parity run below, on the tree before ADR
0025 D9's own clock and the one-write arming. Mutations killed: 8 of 8 of the hardening code, 7 of
7 of the D9 code (the gate position included), ~~1 of 1 of the ADR 0025 D9 guard this e2e led
to~~ *(updated 2026-09-26)* 3 of 3 of the ADR 0025 D9 guard this e2e led to (no guard, no clock,
no timestamp check) and 1 of 1 of the one-write arming (the write split again). ~~**Not
claimed:** lint, cyclo, gosec, vuln, the unit and integration coverage targets and the
image-tools check on the final code — a CI-parity run of those was still in progress when this
was written —~~ *(Updated 2026-09-26.)* A CI-parity run in a clean copy of the tree before ADR
0025 D9's own clock, the one-write arming and the drain-test fix was green: `make generate-all`
(no diff with a fresh controller-gen v0.22.0), lint (golangci-lint v2.14.0, 0 issues), cyclo,
gosec v2.29.0 (0 issues), vuln (no vulnerabilities), the unit and integration coverage targets,
the image-tools check and the release-tooling check. **Not claimed:** its rerun on the final code,
still in progress when this was written, and CI, which has not seen the change, which is
uncommitted.

## Context

ADR 0032 removed root from every generated pod. It fixed the identity (uid/gid/fsGroup 999,
`runAsNonRoot`), the container posture (no privilege escalation, read-only root filesystem,
`drop: [ALL]`) and the seccomp profile (`RuntimeDefault`, hard-wired). Hans then asked for the
remaining pod-manifest hardening as well, for every Valkey pod and for the operator itself, using
a reference manifest as the target. That manifest adds `automountServiceAccountToken: false`,
`hostNetwork`/`hostPID`/`hostIPC: false`, `hostUsers: false`, explicit `runAsUser`/`runAsGroup`/
`fsGroup`, `privileged: false`, an image pinned by digest, and requests and limits.

Read in the tree before this change:

- `automountServiceAccountToken: false` already held for every generated pod (ADR 0031: the
  data pod projects its token to the sidecar alone; Sentinel and observer mount none). The
  operator and its pre-upgrade hook need the token, and keep it.
- `hostNetwork`, `hostPID` and `hostIPC` are plain booleans with `omitempty`; nothing sets
  them, and the API has no representation for an explicit `false`.
- `hostUsers` was unset everywhere, and `seccompProfile` could not be anything but
  `RuntimeDefault`.
- `privileged` was unset (the API default is `false`).
- The observer and the operator ran as whatever user the operator image declares without naming
  it: the `nonroot` user, 65532, of its base `gcr.io/distroless/static-debian12:nonroot`
  ([`Containerfile`](../../Containerfile) has no `USER` line of its own).
- **A digest-pinned `spec.image` could not be deployed.** `ExtractVersionFromImage` returned
  `sha256:<64 hex>` as the `app.kubernetes.io/version` label: 71 characters with a colon, where a
  label value allows 63 without one. Every object carrying the label would have been refused. The
  unit test pinned that output with a 6-character fake digest, short enough to pass the length
  rule and never checked for validity.
- The exporter default was a bare tag, `oliver006/redis_exporter:v1.66.0`, which a re-push can
  change under the pods.
- The Sentinel containers, the sidecar and every init container state no requests or limits,
  and Sentinel had no field to set them. A namespace with a cpu/memory `ResourceQuota` refuses
  such a pod.

Two properties of the new fields decided their shape. A user namespace needs node support
(Kubernetes 1.33, where `UserNamespacesSupport` turned on by default, or the gate enabled
before that — the gate's history read in `pkg/features/kube_features.go` of Kubernetes
v1.36.4; containerd 2.0 or CRI-O 1.25, Linux 6.3 for idmapped tmpfs, and idmap support in the
data volume's file system, which NFS lacks — upstream requirements, not verified in this
repository), and a pod on a node without it does not start. And an API server with the gate off
**drops `hostUsers` from a pod template without an error** (`dropDisabledFields` in
`pkg/api/pod/util.go`, read in v1.36.4): envtest runs Kubernetes 1.29, where the gate is alpha
and off, and the integration tier measured exactly that.

## Decision

**D1 — The seccomp profile is `RuntimeDefault` or `Localhost`, per Valkey resource, never
`Unconfined`** *(a `Localhost` one only through the allow-list of D9, since 2026-09-26)*.
`spec.podSecurity.seccompProfile` sets the pod-level profile of the data, Sentinel
and observer pods (`GetSeccompProfile`). Omitted, or `type: RuntimeDefault`, is the runtime's
default filter, as before; an explicit `RuntimeDefault` moves no hash and rolls nothing.
`Localhost` requires `localhostProfile`, a path below the kubelet's seccomp directory. The CRD
enforces the enum `RuntimeDefault;Localhost` (`type` defaults to `RuntimeDefault`) and, by the
first of its two CEL rules on `SeccompProfileSpec`, `self.type == 'Localhost' ? (has(self.localhostProfile) &&
size(self.localhostProfile) > 0) : !has(self.localhostProfile)`, that the path is set and
non-empty for `Localhost` and absent otherwise. `Unconfined` cannot be expressed, so every
generated pod runs under a seccomp filter and Pod Security `restricted` (which allows
`RuntimeDefault` and `Localhost`) stays satisfiable. A `Localhost` profile must allow what every
generated container does, the chown of the migration-only `fix-data-ownership` init container
included; a profile missing on a node keeps a pod scheduled there from starting.

*(Amended 2026-09-26.)* **`Unconfined` is refused by name only.** A
`Localhost` file can allow every syscall and still pass `restricted`, so "runs under a seccomp
filter" says nothing about how strict the filter is; which `Localhost` profile a Valkey resource
may name is decided by D9, and with the default empty allow-list it may name none. A second CEL
rule, `!has(self.localhostProfile) || (!self.localhostProfile.startsWith('/') &&
!self.localhostProfile.matches('(^|/)[.][.](/|$)'))`, refuses an absolute path and a `..`
element on the CR itself — the two shapes the API server refuses on every pod-template write
(`validateLocalDescendingPath`, read in v1.36.4) — while a name that merely contains two dots
(`profiles/..valkey..json`) stays valid ([`valkey_types.go`](../../api/v1/valkey_types.go),
`SeccompProfileSpec`; the chart's CRD copy carries the same rule).

**D2 — A user namespace is opt-in, per Valkey resource.** `spec.podSecurity.userNamespaces: true`
sets `hostUsers: false` on the data, Sentinel and observer pods (`applyPodHardening`); omitted or
false leaves the field unset, so existing clusters change nothing. `hostUsers` is compared
**exactly**, not as a subset (`podHardeningChanged`): opting out leaves the desired field unset,
and a subset comparison would never converge the persisted `false` back — the
`capabilities.add` argument of ADR 0032 D5, for a field whose unset value is the weaker one.
Both fields of `spec.podSecurity` move both pod-spec hashes (they hash the whole built
`PodSpec`), so the data tier rolls failover-aware and the Sentinel tier rolls as for any
template change (ADR 0005 D7); the observer Deployment carries no hash and is rewritten by its
own comparison (`ObserverDeploymentHasChanged`). Inside the user namespace the ADR 0032
securityContext is unchanged — uid 999, `drop: [ALL]`, no privilege escalation — read from the
builder; that it still yields an empty bounding set and `no_new_privs` on a node is what the
e2e checks, ~~not yet run~~ *(run 2026-09-26, see Residual risks: a user namespace on every
data and Sentinel pod, `Uid 999`, `CapBnd 0`, `NoNewPrivs 1` on the `valkey` container of
pod 0)*.

**D3 — A user namespace the API server dropped blocks the pass.** Every create and update of the
data StatefulSet, the Sentinel StatefulSet and the observer Deployment — every write that
carries a pod template; the nudge is a metadata-only merge patch — goes through `writeWorkload`,
which reads the stored template out of the write's answer. That depends on the client: the
controller-runtime client zeroes the object before it decodes the answer into it
(`targetZeroingDecoder` in `pkg/client/apiutil`, read in v0.25.1), so a field the server dropped
reads back unset; a client that decoded over the sent object would keep `false` and never report.
When the operator sent `hostUsers: false` and the stored template has none, the step fails with
`errUserNamespacesDropped`: `ReconcileBlocked=True/UserNamespacesUnsupported`, phase `Error`,
and a message naming the gate and both ways out. The drift comparison sees the same difference on
the next pass and writes again, so the report stands for as long as the cluster drops the field;
the rate limiter paces the retries. In `reconcileBlockedReason` it ranks ~~directly below
`RecreateRequired`~~ below `RecreateRequired` and D9's `SeccompProfileNotAllowed` *(amended
2026-09-26)* and above an admission rejection: it too clears only when a human acts. The
write itself is not withheld — the rest of the template still applies — so the pods run without a
user namespace and the CR says so, which is the ADR 0002 reading of a spec the operator accepted
and cannot apply.

**D4 — Every generated pod states the hardening it relies on.** `privileged: false` on every
container (`restrictedContainerSecurityContext`, and the repair container's own context), and
`enableServiceLinks: false` on every pod: kubelet otherwise injects `<NAME>_SERVICE_HOST`,
`<NAME>_SERVICE_PORT`, a `<NAME>_SERVICE_PORT_<PORT_NAME>` per named port and the Docker-link
`<NAME>_PORT*` variables for every Service of the namespace that has a cluster IP into every
container — an inventory of the namespace that no process here reads, and a name-collision
surface for the variables they do read. The `kubernetes` Service of the `default` namespace is
injected regardless (`KUBERNETES_SERVICE_*`, `KUBERNETES_PORT*`; `getServiceEnvVarMap` in the
kubelet and `envvars.FromServices`, read in v1.36.4).
`hostNetwork`, `hostPID` and `hostIPC` stay unset, because the API cannot carry an explicit
`false`; a test asserts them false on every rendered template so that a builder setting one
fails. The observer is pinned to uid, gid and fsGroup 65532, the operator image's numeric
`nonroot` user, instead of inheriting whatever an image built from another base declares.
This supersedes ADR 0032 D1's "(its image user, 65532, is numeric)", struck through there; the
observer's profile, `RuntimeDefault` in ADR 0032 D1, is the one D1 here selects.
`automountServiceAccountToken` stays as ADR 0031 set it.

**D5 — Images can be, and by default are, pinned by digest.** `ExtractVersionFromImage` never
returns a digest: `repo:tag@sha256:…` yields the tag, a digest-only reference yields an empty
label value, a bare repository still yields `latest`, and the unit test now checks every result
with `validation.IsValidLabelValue`. `DefaultMetricsExporterImage` is
`oliver006/redis_exporter:v1.66.0@sha256:d98e6db8…` — the digest of the multi-arch image index
behind that tag, read with `docker buildx imagetools inspect` on 2026-09-26 and re-read the
same day from the registry API (the tag's `docker-content-digest`, media type
`application/vnd.oci.image.index.v1+json`); the tag stays for the reader. The chart takes
`image.digest` (validated as `sha256:` plus 64 lowercase hex characters at render time) and
renders `repository:tag@digest` for the operator, the hook, `--operator-image` and
`OPERATOR_IMAGE`, so the sidecar and the observer the operator generates run the pinned image
too. `spec.image` stays the CR author's choice; a digest there now yields a valid label and an
accepted StatefulSet (integration tier), and a cluster running from one is what the e2e
checks, ~~not yet run~~ *(run 2026-09-26: data pod 0 ran the `tag@digest` image and carried
the tag as its version label)*.

**D6 — The operator's own pods carry the same posture.** The Deployment and the pre-upgrade hook
Job share `valkey-operator.podHardening`: uid, gid and fsGroup 65532, `runAsNonRoot`, the seccomp
profile from `podSecurity.seccompProfile` (`RuntimeDefault` or `Localhost`; anything else, a
`Localhost` without a path, or a path without `Localhost` fails the render — and, *added
2026-09-26*, a `localhostProfile` that is absolute or has a `..` element, the two shapes D1's
second CEL rule refuses on the CR and the API server refuses on every pod write), `hostUsers: false`
behind `podSecurity.userNamespaces` (default off), `enableServiceLinks: false`, and
`automountServiceAccountToken: true` — stated, because both pods talk to the API server — and
`valkey-operator.containerSecurityContext` (`privileged: false`, no escalation, read-only root,
`drop: [ALL]`). The chart does not reach the Valkey pods; those take `spec.podSecurity`.

**D7 — Sentinel takes resources; nothing gets a new default.** `spec.sentinel.resources`
(`GetSentinelResources`) goes to every container of the Sentinel pod, the init container
included, because a cpu/memory `ResourceQuota` admits a pod that sets no pod-level resources only
when every container, init containers included, states the values (`podEvaluator.Constraints`,
read in v1.36.4), and an init container whose request equals the main container's never adds to
the pod's effective request. Omitted means no requests and no limits, as before. The sidecar and
the data pod's init containers keep stating none, and no container gets a new default — the
observer keeps the request it already had (50m CPU, 64Mi memory, `GetObserverResources`): a
limit guessed too low is an OOM kill in a process that holds the drain promotion (ADR 0012),
and a measured default was the alternative that lost.

**D8 — What is deliberately not set.** No AppArmor profile: kubelet refuses a pod requesting any
AppArmor profile other than `Unconfined` on a node where AppArmor is not enabled (`isRequired`
and `validateHost` in `pkg/security/apparmor`, wired in as a kubelet admit handler; read in
v1.36.4 on 2026-09-26, not measured), so an explicit `RuntimeDefault` would break every node
without AppArmor — the SELinux-based distributions among them. A container that names none is
handed to the runtime with no profile (`getAppArmorProfile` in `pkg/kubelet/kuberuntime`, read
in v1.36.4); that a runtime on an AppArmor node then applies its own default profile is runtime
behaviour, neither read in source nor measured here. No SELinux options, no `runtimeClassName`,
no sysctls: nothing in the reference manifest asks for them, and each is cluster-specific.

**D9 — A `Localhost` profile is written only when the operator's allow-list names it; the list
is empty by default.** *(Decided 2026-09-26; see Status.)*

- **The list** is the operator flag `--allowed-seccomp-localhost-profiles`: comma-separated paths
  relative to the kubelet's seccomp directory. `profileList` trims each entry and drops blanks,
  so an unset flag is an empty list, never a list holding one empty entry; `newReconciler` hands
  it to the reconciler as `AllowedSeccompLocalhostProfiles`. The chart value is
  `valkeyPodSecurity.allowedSeccompLocalhostProfiles: []` # default; `deployment.yaml` renders the
  flag only for a non-empty list, and `valkey-operator.allowedSeccompLocalhostProfiles` fails the
  render on an empty entry, a leading `/`, a `,` inside an entry or a `..` element — the D1 path
  rule plus the separator (an entry of blanks, or one with surrounding blanks, passes the render
  and fails closed in `profileList`; Residual risks).
- **The rule** (`seccompProfileAllowed`): an omitted profile and `RuntimeDefault` are always
  allowed; `Localhost` is allowed only when `localhostProfile` is **exactly** one of the entries
  (`slices.Contains`: no prefix, no wildcard, no path normalisation); an empty list refuses every
  `Localhost` profile. Default-deny, because a `Localhost` profile is only as strict as its file
  and Pod Security `restricted` accepts every one: without the list, whoever may create a Valkey
  resource picks among every profile an administrator ever installed on a node. *(Added
  2026-09-26.)* The field says so where a CR author reads it: the `LocalhostProfile` doc comment
  in [`valkey_types.go`](../../api/v1/valkey_types.go), and with it the regenerated description
  in both CRD copies (`config/crd/bases`, the chart's `templates/crd.yaml`), states that the
  operator writes the path only when `--allowed-seccomp-localhost-profiles` lists it, and that the
  list is empty by default.
- **The refusal withholds ~~every workload write~~ every write of a workload's pod template, on
  create and on update** *(narrowed 2026-09-26: two workload writes carry no template and are not
  gated — the metadata-only nudge of a StatefulSet short of pods, from `reconcileWorkload`, and
  the delete of the observer Deployment once `spec.observer.enabled` is off, from
  `reconcileObserver`)*.
  ~~`reconcileStatefulSet` returns `errSeccompProfileNotAllowed` before it builds, reads or writes
  anything;~~ *(Superseded 2026-09-26: at the head of the step the gate hid a foreign
  StatefulSet, froze `StorageSpecNotApplied` and skipped the TLS material record — Residual
  risks.)* **The gate sits at the write, after every proof and guard of the step and before its
  drift detection.** `reconcileStatefulSet` returns `errSeccompProfileNotAllowed` on the create
  path after `ensureTLSMaterialRecord`, right before `writeWorkload`; on the update path after
  the ownership proof (`IsControlledBy`), `guardVolumeClaimTemplates`, `ensureTLSMaterialRecord`
  and the repair decision (`dataOwnershipRepairNeeded`), and before `StatefulSetHasChanged`. So a
  foreign StatefulSet is still reported as `ForeignObject`, the `StorageSpecNotApplied` level is
  still re-measured, the TLS record is still derived, and a live template that already carries a
  profile the list no longer holds is reported even when nothing drifted — the gate asks the
  spec, not the difference. `reconcileSentinelStatefulSet` withholds at the same two positions,
  and `reconcileObserverDeployment` on its create path and after its ownership proof (it has
  neither a claim guard nor a TLS record); both return nil, without writing and without an
  error. The data StatefulSet step is **the one reporter** — it runs in every pass on every
  topology (it has no `when` in `resourceReconcileSteps`) — so one cause yields one message, not
  three; it reports only when it reaches its gate (Residual risks). The pass is blocked as in ADR 0002:
  `ReconcileBlocked=True/SeccompProfileNotAllowed`, phase `Error`, a message naming the profile,
  the flag and the chart value; the rate limiter paces the retries. Everything else in the pass
  runs as in any blocked pass — `runReconcileSteps` joins the step errors and carries on — so
  the ConfigMaps, Services, sidecar RBAC, certificates, PodDisruptionBudgets, NetworkPolicies and
  metrics objects are still reconciled, and the workload pass still runs against the persisted
  templates. The running pods keep the last template that was written. The block clears when an
  administrator lists the profile (a flag, so an operator rollout) or the spec names a listed
  profile or `RuntimeDefault`, or omits the profile.
- **Unlike D3, nothing of the template is applied.** The profile is a field of the one pod
  template, so applying "the rest" would mean writing either the refused profile or a profile
  the spec did not ask for.
- **Rank.** In `reconcileBlockedReason` it comes after `ForeignObject` and `RecreateRequired` and
  before `UserNamespacesUnsupported` and an admission rejection: it too clears only when a human
  acts, and it outranks the dropped `hostUsers` because nothing of the spec was applied at all.
  ~~Read from the code: `RecreateRequired` and `UserNamespacesUnsupported` come only from the
  workload steps D9 withholds, so neither can occur in the same pass today; the rank decides
  against a foreign object or an admission rejection from another step, and the message carries
  every step's error either way. A foreign *data or Sentinel StatefulSet* never meets it: the gate
  runs before those steps' ownership check, so while D9 refuses, such an object is not reported
  at all (Residual risks).~~ *(Superseded 2026-09-26 with the gate move.)* Read from the code:
  `UserNamespacesUnsupported` comes only from `writeWorkload`, which D9 withholds in all three
  steps, so the two never share a pass. `RecreateRequired` comes from the data step's claim guard,
  which now runs first and returns before the gate, so that step reports one or the other (the
  Sentinel guard compares empty against empty and cannot raise it today). A foreign data
  StatefulSet likewise ends the data step at its ownership proof, and the CR says
  `ForeignObject`. The rank therefore decides against a foreign object from another step — a
  foreign Sentinel StatefulSet, a ConfigMap, a Service — which outranks D9, and against an
  admission rejection, which D9 outranks; the message carries every step's error either way.
- **Scope.** The list binds Valkey resources only. The chart's own `podSecurity.seccompProfile`
  for the operator and hook pods (D6) is the installer's choice and is not checked against it.
  *(Added 2026-09-26.)* It is checked for shape: `valkey-operator.podHardening` fails the render
  on a `localhostProfile` with a leading `/` or a `..` element (the D1 path rule, the same regular
  expression), so an installer meets the refusal at `helm template`/`helm upgrade` and not as an
  operator Deployment the API server refuses to store; the value's comment in `values.yaml` says
  so *(added 2026-09-26)*.

## Consequences

- Nothing rolls for this ADR alone: its pod-spec changes ship in the release whose rootless
  posture already rolls every data and Sentinel tier *(a persistent data tier twice, the second
  roll of ADR 0032 D2; noted 2026-09-26)*, and the observer Deployment, once. The
  non-persistent single pod ADR 0032 D3 defers is deferred with them.
- A cluster whose nodes lack user-namespace support cannot use D2. If the API server drops the
  field, D3 says so; if the API server keeps it and a node's runtime or kernel cannot honour it,
  the first replacement pod does not start, the roll holds there, and after
  `spec.rollingUpdate.syncTimeout` `PodAvailabilityStalled` names it (ADR 0026 D11; read, not
  measured with a user namespace). A single pod is not covered (ADR 0032 D7).
- On an API server that drops the field, turning `userNamespaces` on still moves both pod-spec
  hashes — they are computed from the built spec, which carries `hostUsers: false` — so every
  data and Sentinel tier rolls once onto a template stored without it, and turning it back off
  rolls them again. That roll runs in blocked passes: phase `Error` instead of its progress, and
  a pass error that discards the roll's own requeue, so it advances on the rate limiter (capped
  at 30 s, ADR 0002 D8) and the owned-StatefulSet watch. Read from `ComputePodSpecHash`,
  `ComputeSentinelPodSpecHash`, `podOutdated`, `sentinelPodNeedsUpdate` and `Reconcile`, not
  measured.
- The exact `hostUsers` comparison (D2) fights a mutating admission policy that sets
  `hostUsers: false` on the template of a Valkey resource without the opt-in: the operator
  writes the field back out on every pass. Opting in is how such a cluster agrees with its
  policy.
- A `Localhost` profile is node state the operator cannot see: it must exist on every node a pod
  may land on before it is referenced, and must allow every generated container. Missing, it
  behaves like the unsupported node above; too strict, a container fails at a syscall; too
  loose, it filters less than `RuntimeDefault` — the allow-by-default e2e fixture is such a
  profile — and still passes Pod Security `restricted`, which accepts every `Localhost` profile
  (`check_seccompProfile_restricted.go`, read in `k8s.io/pod-security-admission` v0.37.0). D1
  keeps the pods under *a* filter; ~~how strict it is rests with whoever installs the file, and
  a CR author chooses among the files the nodes hold.~~ *(Superseded 2026-09-26 by D9: a CR
  author chooses among the files the operator's allow-list names, none by default; how strict
  each one is rests with whoever lists it and installs it.)*
- D9's default-deny changes nothing at the upgrade: `spec.podSecurity` is new in this release,
  so no existing Valkey resource names a `Localhost` profile. A cluster that wants one needs the
  chart entry — and with it an operator rollout — before the operator writes it.
- A cluster whose profile is later removed from the allow-list, or whose spec is edited to name
  an unlisted one, stops receiving workload template writes until one of the two is fixed —
  image changes, replica changes, TLS rotations and the removal of the ADR 0032 repair
  included — while its pods
  keep running on the last template. The CR shows `ReconcileBlocked=True/SeccompProfileNotAllowed`
  and phase `Error`; a rotation it holds back also shows as `TLSMaterialStale`, which compares
  the pods with the Secret rather than with the template (`scanTierTLSMaterial`). The ConfigMaps
  are still written, so a pod that restarts for another reason meanwhile boots with a
  configuration its template's config hash does not record — as under any refused StatefulSet
  write (`RecreateRequired`, ADR 0023). Read, not measured.
- The exporter default digest (D5) moves the data pod-spec hash of every metrics-enabled cluster
  that leaves `spec.metrics.image` empty: `MetricsImage` falls back to
  `DefaultMetricsExporterImage`, and the hash covers the whole built `PodSpec`. It rides the same
  release roll as the ADR 0032 posture, and where ADR 0032 D3 defers a non-persistent single
  pod, the exporter change waits with it.
- Changing `spec.podSecurity` on a non-persistent single pod that already runs rootless replaces
  it and loses its data, like any pod-spec change there
  ([ADR 0007](0007-failover-aware-rolling-update.md) D7): `isSidecarOnlyChange` reads it as more
  than a sidecar change, and the standalone path deletes the pod. *(Precise reading, added
  2026-09-26: `isSidecarOnlyChange` compares images only and is false because the sidecar image
  did not move; if an operator upgrade moves the sidecar image at the same time, it is true and
  the whole pending change, `spec.podSecurity` included, is deferred under
  `SidecarUpdatePending`, ADR 0007 D6.)* A single pod still running as
  root defers it with the posture instead (`singlePodDeferral`, ADR 0032 D3), because neither the
  image, the TLS material record nor the config hash moved.
- The sidecar and the data pod's init containers still state no resources, so a namespace with a
  cpu/memory `ResourceQuota` still refuses the data pods.
- A digest-only `spec.image` gets an empty `app.kubernetes.io/version` label.
- Setting `image.digest` on an installed operator changes `--operator-image`, and with it the
  sidecar image of every data pod and the observer's image: the data tiers roll as for any
  operator image change (a rootless single standalone pod defers it, ADR 0007 D6) and the
  observer Deployment is rewritten. Set in the same upgrade that moves the tag, it costs nothing
  extra.
- The observer's uid/gid/fsGroup 65532, `privileged: false` and `enableServiceLinks: false` are
  new in the templates of existing observer Deployments too; the observer is rewritten once,
  together with the ADR 0032 posture.

## Alternatives Considered

- **A fully configurable seccomp profile, `Unconfined` included**, as an escape hatch for a
  syscall `RuntimeDefault` blocks. No such case is known for Valkey, and it would let a CR author
  run the pods without a filter and break `restricted` for the namespace.
- **A fixed `RuntimeDefault`.** Simplest, but a cluster with its own profiles (Security Profiles
  Operator) could only get one through a mutating policy, and D5 of ADR 0032 would rewrite that
  policy's profile back on every pass.
- **User namespaces on by default**, fleet-wide or for the operator pod alone. Every cluster
  without node support would stall one replica per data tier at the upgrade, and NFS-backed
  claims would never start — the opposite of upgrade-neutral defaults (ADR 0005).
- **Detecting node support for user namespaces** (`status.runtimeHandlers[].features`) before
  writing. It needs node read access for the operator, and a scheduler that knows nothing of it
  can still place a pod on the one node without support.
- **Withholding the whole write when `hostUsers` would be dropped** (a dry-run first). The rest of
  the template — an image change, a TLS rotation — would then be held hostage to a feature
  gate; D3 applies the rest and reports the part it cannot.
- **Measured default requests and memory limits for every operator-owned container.** Makes
  quota namespaces work, but a limit that is right on Kind can be wrong under a heavier load,
  and an OOM-killed sidecar breaks the drain promotion; Hans decided for the field only.
- **Fields for the sidecar and the init containers as well.** More API surface with no known
  user.
- **An explicit AppArmor `RuntimeDefault`** (D8).
- **Document only** — no allow-list in the operator; the security documentation tells
  administrators to install only profiles they accept for every Valkey pod and, if the choice
  must be narrower, to write an admission policy on `spec.podSecurity.seccompProfile` or on the
  generated pods. Hans chose it first and reversed it the same day (Status). It leaves the wide
  choice as the installed default, and the narrowing lives outside the operator, where nothing
  checks that it exists.
- **Remove `Localhost`** — back to the fixed `RuntimeDefault` above, with the same cost: a
  cluster with its own profiles could get one only through a mutating policy, which the ADR 0032
  D5 comparison rewrites back on every pass.
- **An allow-list in the operator, default-deny** — chosen (D9). The administrator who installs
  the files also names the ones a Valkey resource may use. It costs a flag to keep in step with
  the nodes, an operator rollout for every change of the list, and a string comparison that
  cannot see the file it names (Residual risks).

## Residual risks

- **Verification** *(filled in with the runs of 2026-09-26)*:
  - unit: `TestPodHardening_DefaultsOnEveryTemplate` (`podSecurityMatrix`, the 72 clusters the
    ADR 0032 Pod Security guard renders: three topologies × TLS × auth × metrics × three
    persistence settings, each with its data, observer and, where enabled, Sentinel template),
    `TestPodHardening_OptInsReachEveryPodKind` (still `restricted` with both opt-ins),
    `TestPodHardening_OptInsMoveThePodSpecHashes`, `TestPodSpecChanged_Hardening`,
    `TestObserverDeploymentHasChanged_Hardening`, `TestSentinelResources_ReachEveryContainer`,
    `TestWriteWorkload_ReportsADroppedUserNamespace`, `TestWriteWorkload_StoresTheUserNamespace`,
    `TestExtractVersionFromImage` (every result a valid label value),
    `TestPodSecurityAccessors`, `TestDefaultMetricsExporterImage_IsPinnedByDigest` — green in
    `make test-unit`, with `make lint` ~~(golangci-lint v2.14.0)~~ *(corrected 2026-09-26: that
    run invoked the stale unversioned `bin/golangci-lint`, which reports 2.13.1, not the pinned
    v2.14.0 — the tool-path defect of [ADR 0017](0017-test-and-ci-policy.md) D49; read from the
    run's output and `--version`)* and `make cyclo` green and 8 of 8
    mutations of this ADR's code killed, all before D9 existed. D9: `TestSeccompProfileAllowed`,
    `TestSeccompProfileNotAllowed_NoWorkloadIsWritten` (create, update, a listed profile reaching
    every workload), `TestProfileList`, `TestBindOperatorFlags_AllFlagsParsed`,
    `TestNewReconciler` — green in `make test-unit` on 2026-09-26, ~~served from the Go test cache
    (so an earlier run on the same sources, not a `-count=1` run)~~ *(superseded 2026-09-26: rerun
    uncached, `GOFLAGS=-count=1 make test-unit`, exit 0, every package `ok` and none `(cached)`,
    no FAIL, no SKIP, all five listed tests PASS)*. ~~The revert checks in their doc
    comments are written down, not recorded as executed, and no mutation of the D9 code was run;~~
    *(Superseded 2026-09-26.)* The gate at the write is pinned by
    `TestSeccompProfileNotAllowed_GateSitsAtTheWrite`: a foreign data StatefulSet under an
    unlisted profile still fails the step with `errForeignObject` (the row a gate at the head of
    the step fails), and a live template whose profile the list no longer holds is refused and
    not written (the row a gate inside the drift branch fails). **7 of 7 mutations of the D9 code
    killed**, mutations of the gate position among them; which seven is not recorded in this
    repository. Read in the tests: no row combines D9 with a claim conflict or with a TLS
    template, so the gate's order relative to `guardVolumeClaimTemplates` and
    `ensureTLSMaterialRecord` is read from the code, not pinned. ~~The full unit tier, `make lint`
    and `make cyclo` on the final code are part of a CI-parity run that was still in progress
    when this was written; no result of it is claimed here;~~ *(Updated 2026-09-26.)* The
    CI-parity run in a clean copy — the full unit tier through its coverage target, `make lint`
    (golangci-lint v2.14.0, 0 issues) and `make cyclo` among its gates — was green on the tree
    before ADR 0025 D9's own clock and the one-write arming (Status); ~~its rerun on the final code
    is not claimed~~ *(rerun green, 2026-09-26: every gate target on the final tree, see
    [ADR 0017](0017-test-and-ci-policy.md) D49)*;
  - integration (envtest, Kubernetes 1.29): the enum refuses `Unconfined`, and the first CEL
    rule a `Localhost` without or with an empty path and a path with `RuntimeDefault`; a
    `seccompProfile: {}` defaults to `RuntimeDefault`; **the 1.29 API server stores the data
    StatefulSet without `hostUsers`, the CR reports `ReconcileBlocked/UserNamespacesUnsupported`
    and phase `Error`, and setting the field back releases it**; the hardened templates — a
    `Localhost` profile, Sentinel resources and a digest-pinned image, without `hostUsers`,
    which this server drops — read back without drift. No test round-trips a template stored
    *with* `hostUsers: false` through the drift comparison: envtest cannot store it, and the e2e
    does not check that the templates stop being written. All of this ran before D9 and the CEL
    path rule. ~~**Not yet run:**~~ *(Run since, 2026-09-26: the integration tier green
    repeatedly with D9 and the CEL path rule in the tree, these two included:)*
    `TestPodSecurity_LocalhostProfileAllowList_Integration` (an
    unlisted profile: `ReconcileBlocked/SeccompProfileNotAllowed`, phase `Error`, no StatefulSet
    read past the cache; a listed one reaching the StatefulSet) and the path rows of
    `TestPodSecurity_CRDValidatesTheSeccompProfile_Integration` (absolute, leading, inner and
    trailing `..` refused; dots inside a name accepted). The CEL path rule has no unit test;
    these rows are its only check. *(Precised 2026-09-26.)* The recorded runs predate the D9 gate
    move and ADR 0025 D9; ~~on the final code the integration tier has run only inside the
    CI-parity run, whose result is not claimed~~ *(updated 2026-09-26)* the CI-parity run's
    integration coverage target was green on the tree with the gate at the write, before ADR 0025
    D9's own clock and the one-write arming; its rerun on the final code is not claimed;
  - e2e: `TestE2E_PodHardening_UserNamespacesLocalhostSeccompAndDigest` — ~~*result recorded in
    the ticket once the run completes; not yet run at the time of writing.*~~ *(Superseded
    2026-09-26.)* Run on Kind (Kubernetes 1.36.1, containerd 2.3.1, runc 1.4.2, Linux 6.10),
    before D9. Valkey 8: the full suite 53/53 green. Valkey 9: 52/53, the one failure this
    test's own owner assertion on `/data` — the Kind hostPath volume root is root-owned `0777`,
    and a cluster this operator built never ran the ADR 0032 repair, so it stays uid 0. The
    assertion now compares the volume root's owner before and after the move; with it the test
    passed on Valkey 8 and, rerun alone, on Valkey 9. The fleet-upgrade e2e from 1.12.8 was
    green, including exactly two `RollingUpdateComplete` per persistent tier and nothing rolling
    after the second roll. ~~**Not yet run:** the D9 subtest "a Localhost profile the operator
    does not allow is refused and reported"; a rerun of the fleet-upgrade e2e and both full
    suites with D9 and the CEL path rule is in progress;~~ *(Superseded 2026-09-26 by the final
    runs.)* ~~**Final runs, 2026-09-26**~~ **The runs before the final one, 2026-09-26**
    *(relabelled 2026-09-26: their image predates ADR 0025 D9's own clock and the one-write
    arming)*, same Kind versions, one operator image built from ~~the final code~~ the code of
    that time (D9 with the gate at the write, the CEL path rule, ADR 0025 D9 in its first form):
    the fleet-upgrade e2e from 1.12.8 green; the full suite 53/53 on Valkey 9 and 53/53 on
    Valkey 8; two more Valkey 8 runs of this test and `TestE2E_PodSecurity_RestrictedNamespace`,
    green. The D9 subtest "a Localhost profile the operator does not allow is refused and
    reported" (a single-pod cluster through the real chart, whose `test/e2e/helm-values.yaml`
    lists two other profiles: `ReconcileBlocked=True/SeccompProfileNotAllowed`, no StatefulSet)
    was green on every run. **This test is where
    [ADR 0025](0025-a-split-brain-warning-means-one-that-did-not-resolve-itself.md) D9 was
    found**: its cluster `hard` (3+3 Sentinel, AOF, metrics, observer) rolls through a Sentinel
    failover when the patch moves it, and on the Valkey 8 leg of the run before ~~the final
    one~~ ADR 0025 D9 *(re-anchored 2026-09-26, the final run having moved)* the Sentinel
    rolling update demoted the replica Sentinel was promoting, in a
    reset-and-retrigger loop over ten minutes, and the leg went red — a pre-existing bug on
    `main`, not one of this ADR's changes. Operator log for `hard`: that run 11 failover
    triggers, 11 demotions, 9 failover timeouts over both legs — one trigger and one demotion on
    Valkey 9, whose failover completed anyway and whose leg stayed green, and ten triggers, ten
    demotions and nine timeouts on Valkey 8 until the subtest's ten-minute wait ran out (the
    per-leg split read from the log's timestamps against each leg's run, 2026-09-26); ~~the final
    run~~ the run with ADR 0025 D9 in its first form *(relabelled 2026-09-26)* 4 triggers (one per
    run of the test), 0 demotions, 0 timeouts. ~~The ADR 0025 D9 guard: 1 of 1 mutation killed;~~
    *(superseded 2026-09-26: 3 of 3 since the clock, at the end of this bullet)* **Final run,
    2026-09-26** *(added)*, same Kind versions, one operator image built from the final operator
    code — ADR 0025 D9's own 90 s clock (`ownFailoverInFlight`) and the one-write arming
    (`setFailoverTriggered`, [ADR 0010](0010-every-rolling-update-wait-is-bounded.md) D14)
    included, the drain-test fix below not yet: the fleet-upgrade e2e from 1.12.8 green; the
    full suite 53/53 on Valkey 8 and 52/53 on Valkey 9; two more Valkey 8 runs of this test and
    `TestE2E_PodSecurity_RestrictedNamespace`, green; this test, and with it the D9 subtest,
    green on every run. The one failure was `TestE2E_SidecarFailoverDrainMaster` — a fixture
    that waited on controller state after deleting the master, satisfied by the terminating old
    master ([ADR 0026](0026-a-pod-being-deleted-is-not-available.md)), whose data check then read
    `DBSIZE 0` from the replacement; diagnosed from the test code and its timing, not from the
    red run's pod logs, which were lost with the CR. It now waits for the replacement's new UID
    ([ADR 0017](0017-test-and-ci-policy.md) D50) and ran green 8 of 8 on Valkey 9 alone;
    it is not one of this ADR's changes. The operator log counts for `hard` in the final run are
    not recorded here. Mutations: the ADR 0025 D9 guard 3 of 3 killed (no guard, no clock, no
    timestamp check), the one-write arming 1 of 1 (the write split again);
  - chart: `helm lint` and `helm template` with the defaults, with digest, userns and
    `Localhost`, and each refused value — run by hand. D9, run by hand on 2026-09-26: the
    default renders no `--allowed-seccomp-localhost-profiles`, a two-entry list renders it
    comma-joined, `test/e2e/helm-values.yaml` renders its two profiles, `profiles/..v..json` is
    accepted, and `""`, `/abs.json`, `../x.json`, `profiles/../x.json`, `profiles/..` and `a,b`
    each fail the render. ~~In CI only the default path is rendered, by the e2e job's
    `helm install` (`.github/workflows/release.yml`);~~ *(Amended 2026-09-26: the e2e job's
    `helm install` renders the defaults plus `test/e2e/helm-values.yaml`, which since D9 carries
    a two-entry allow-list, so the flag path is rendered in CI;)* **no CI gate renders the
    digest, user-namespace or `Localhost` paths** *(of the chart's own `image.digest` and
    `podSecurity`, clarified 2026-09-26)* **or checks a refusal**. The render checks refuse only
    the exact shapes listed: an entry of blanks alone (`" "`) passes the render, and an entry
    with surrounding blanks (`" /abs.json"`) passes it without the leading-`/` check seeing the
    slash; `profileList` trims both, into no entry and into an entry no CR can name (the CEL path
    rule refuses it), so both fail closed (rendered by hand on 2026-09-26). *(Added
    2026-09-26.)* The operator's own `podSecurity.seccompProfile` with `type: Localhost`, rendered
    by hand the same day: `/abs.json`, `../x.json`, `profiles/../x.json` and `profiles/..` each
    fail the render ("must be a relative path without '..'"), `profiles/..v..json` and
    `profiles/ok.json` render; `helm lint` passes. No CI gate renders these either. *(Added
    2026-09-26.)* Re-rendered by hand with the final `values.yaml`, whose comment now states the
    refusal: `/abs.json`, `../x.json` and `profiles/../x.json` fail with that message,
    `profiles/..v..json` renders, `helm lint` passes.
- The chart default for `image.digest` is empty: nothing in the release pipeline stamps the
  digest of the image it pushes into the chart. Pinning the operator is left to the installer.
- `DefaultMetricsExporterImage` is not maintained by Renovate (it was not before either), so the
  pinned v1.66.0 ages until someone moves it by hand.
- `hostUsers` on an API server with the gate off is reported, but a **kubelet or runtime** without
  support behind an API server with the gate on is not detected before a pod fails to start.
- The chart's `podSecurity.userNamespaces` has no D3: an API server with the gate off drops
  `hostUsers` from the operator Deployment and the hook Job without an error, and nothing
  reports it.
- AppArmor behaviour (D8) is read from upstream source, not measured on a node without AppArmor.
- `ExtractVersionFromImage` still returns a tag as it is. A tag may be up to 128 characters,
  begin with `_` and end with `_`, `.` or `-` (`[\w][\w.-]{0,127}`, read in
  `github.com/distribution/reference` v0.6.0), while a label value allows at most 63 characters
  and must begin and end alphanumeric (`IsLabelValue`, read in apimachinery v0.37.0): the CR is
  accepted, and every object carrying the label is refused, as with the digest. Read, not
  measured.
- ~~The CRD checks only that `localhostProfile` is non-empty. An absolute path or a `..` element is
  accepted on the CR and refused by the API server on every pod-template write
  (`validateLocalDescendingPath`, read in v1.36.4), so the pass is blocked —
  `ReconcileBlocked=True/WriteFailed`, phase `Error` — until the spec is fixed, while the pods keep
  the last template. Read, not tested.~~ *(Closed 2026-09-26 by the CEL path rule of D1, which
  refuses both on the CR; ~~its integration rows have not run yet~~ its integration rows ran
  green the same day, see Verification.)*
- **The allow-list trusts the files it names.** It compares a path string and cannot see what
  the file on a node contains, whether every node holds the same file, or where a symlink there
  points. A listed allow-by-default profile filters less than `RuntimeDefault` and still passes
  `restricted`; the e2e values list one (`profiles/vko-e2e.json`, `defaultAction:
  SCMP_ACT_ALLOW` with a short deny list), acceptable on a test cluster only. D9 moves the choice from the CR author to the administrator; it does not judge the
  profile.
- **Exact match only.** No wildcard, no directory entry, no normalisation: a directory of
  profiles is listed file by file, and `profiles/./a.json` or `profiles//a.json` is refused
  although it names the same file as a listed `profiles/a.json`. The failure direction is a
  refusal, never an unlisted file accepted.
- ~~Nothing pins that the data StatefulSet step stays unconditional in `resourceReconcileSteps`.~~
  *(Narrowed 2026-09-26.)* No unit test pins that the data StatefulSet step stays unconditional
  in `resourceReconcileSteps` — the two step tests assert order only
  (`TestResourceReconcileSteps_RBACBeforeStatefulSet`,
  `TestResourceReconcileSteps_StatefulSetBeforeSentinelResources`) — and
  `TestPodSecurity_LocalhostProfileAllowList_Integration`, which would notice, covers one
  standalone cluster and ~~has not run yet~~ ran green on 2026-09-26. If the step gained a
  `when`, a pass it skipped would withhold the Sentinel and observer writes with no report at
  all. Read, not tested.
- **The one reporter reports only when the data step reaches its gate** *(added 2026-09-26,
  with the gate move)*. The data step now ends earlier in three cases, and in each the Sentinel
  and observer steps still withhold their writes while the pass does not name D9: a foreign
  data StatefulSet (the CR says `ForeignObject` — intended, the collision is said first), a claim
  conflict (`RecreateRequired`), and a TLS template the step cannot arm (ADR 0030 D12 case 3:
  the Secret unreadable and no record to inherit; the step returns nil, so nothing in the pass
  names D9 — and, without another step's error, the pass is not blocked at all — until the
  Secret is readable; on the create path the seconds until cert-manager issues). Nothing is written in any of the three; what is missing is
  only the reason for the Sentinel and observer tiers. Read from `reconcileStatefulSet` and
  `ensureTLSMaterialRecord`, not tested.
- ~~**While D9 refuses, the StatefulSet steps measure nothing.** The gate sits ahead of everything
  else in `reconcileStatefulSet` and `reconcileSentinelStatefulSet`, so a refused pass also skips
  their ownership check (a foreign StatefulSet under the generated name is neither reported as
  `ForeignObject` nor given its Warning Event; every other consumer treats it as absent),
  `guardVolumeClaimTemplates` — the evaluator and the only clear of `StorageSpecNotApplied`, which
  therefore keeps the value of the last unblocked pass, against the ADR 0027 rule that a level
  is re-measured every pass — and `ensureTLSMaterialRecord`. The CR is still blocked, with D9's
  reason; what it cannot say meanwhile is anything else about those two objects. Read from
  `reconcileStatefulSet`, `reconcileSentinelStatefulSet` and `guardVolumeClaimTemplates`, not
  tested.~~ *(Closed 2026-09-26: the gate moved to the write, after the ownership proof, the
  claim guard and the TLS record (D9). A foreign StatefulSet is reported as `ForeignObject`
  again — pinned by `TestSeccompProfileNotAllowed_GateSitsAtTheWrite` — and
  `StorageSpecNotApplied` and the TLS record are measured in a refused pass as in any other,
  read from the code. What the move leaves is the preceding bullet.)*
- Not weighed in D7: a pod with pod-level `spec.resources` (`PodLevelResources`, on by default
  since Kubernetes 1.34, read in v1.36.4) is exempt from the per-container quota check, which
  would be a way to admit the data pods under a quota without a field per container.
- Out of scope here and open, from the ticket's list: the unauthenticated operator metrics
  endpoint (ADR 0021), a NetworkPolicy for the operator namespace, least-privilege Valkey ACL
  users for probes, sidecar, exporter and observer, and the password-rotation gap (ADR 0030).

## References

- [`api/v1/valkey_types.go`](../../api/v1/valkey_types.go) — `PodSecuritySpec`,
  `SeccompProfileSpec`, `SentinelSpec.Resources`, `GetSeccompProfile`, `UsesUserNamespaces`,
  `GetSentinelResources`, `DefaultMetricsExporterImage`, `ReasonUserNamespacesUnsupported`,
  `ReasonSeccompProfileNotAllowed`, the two CEL rules on `SeccompProfileSpec`
- [`cmd/main.go`](../../cmd/main.go) — `bindOperatorFlags` (`--allowed-seccomp-localhost-profiles`),
  `profileList`, `newReconciler`
- [`internal/builder/pod_security.go`](../../internal/builder/pod_security.go) —
  `applyPodHardening`, `applyValkeyPodSecurity`, `applyObserverPodSecurity`, `OperatorUID`,
  `podHardeningChanged`
- [`internal/builder/sentinel.go`](../../internal/builder/sentinel.go) — Sentinel resources
- [`internal/controller/pod_hardening.go`](../../internal/controller/pod_hardening.go) —
  `writeWorkload`, `errUserNamespacesDropped`, `seccompProfileAllowed`,
  `errSeccompProfileNotAllowed`;
  [`reconcile_blocked.go`](../../internal/controller/reconcile_blocked.go) —
  `reconcileBlockedReason`;
  [`valkey_controller.go`](../../internal/controller/valkey_controller.go) — the D9 gate in
  `reconcileStatefulSet`, `reconcileSentinelStatefulSet`, `reconcileObserverDeployment`;
  `resourceReconcileSteps`, `runReconcileSteps`
- [`internal/common/labels.go`](../../internal/common/labels.go) — `ExtractVersionFromImage`
- [`deploy/helm/valkey-operator/templates/_helpers.tpl`](../../deploy/helm/valkey-operator/templates/_helpers.tpl)
  (`valkey-operator.allowedSeccompLocalhostProfiles`),
  [`deployment.yaml`](../../deploy/helm/valkey-operator/templates/deployment.yaml),
  [`values.yaml`](../../deploy/helm/valkey-operator/values.yaml)
  (`valkeyPodSecurity.allowedSeccompLocalhostProfiles`),
  [`test/e2e/helm-values.yaml`](../../test/e2e/helm-values.yaml) (the two e2e profiles)
- Tests: [`pod_hardening_test.go`](../../internal/builder/pod_hardening_test.go),
  [`controller/pod_hardening_test.go`](../../internal/controller/pod_hardening_test.go),
  [`cmd/main_test.go`](../../cmd/main_test.go),
  [`integration/pod_hardening_test.go`](../../test/integration/pod_hardening_test.go),
  [`e2e/pod_hardening_test.go`](../../test/e2e/pod_hardening_test.go)
- The decision round, including the reversal: [T31](../tickets/local_T31-generated-pods-run-as-root.md),
  section "Extension 2026-09-26"
- [ADR 0032](0032-generated-pods-run-rootless.md) (the posture this extends),
  [ADR 0031](0031-a-record-the-operator-trusts-lives-in-pod-spec.md) (the token split),
  [ADR 0002](0002-surface-a-blocked-reconcile-on-the-cr.md) (blocked passes),
  [ADR 0005](0005-upgrade-neutral-defaults-and-anti-affinity.md) (upgrade-neutral defaults),
  [ADR 0026](0026-a-pod-being-deleted-is-not-available.md) D11 (`PodAvailabilityStalled`),
  [ADR 0017](0017-test-and-ci-policy.md) D35 (the cyclo scope amended by this change),
  [ADR 0025](0025-a-split-brain-warning-means-one-that-did-not-resolve-itself.md) D9 (the
  split-brain bug this ADR's e2e found, `resolveSplitBrainUnlessFailingOver` in
  [`rolling_update.go`](../../internal/controller/rolling_update.go); since its amendment also
  `ownFailoverInFlight` and `setFailoverTriggered`, tests in
  [`split_brain_failover_test.go`](../../internal/controller/split_brain_failover_test.go)),
  [ADR 0010](0010-every-rolling-update-wait-is-bounded.md) D14 (the one-write arming),
  [ADR 0017](0017-test-and-ci-policy.md) D50 (the fixture rule the drain-test fix in
  [`test/e2e/sidecar_test.go`](../../test/e2e/sidecar_test.go) applies),
  [ADR 0027](0027-conditions-are-levels-edges-or-history.md) (the level rule the head-of-step gate
  broke for `StorageSpecNotApplied`), [ADR 0030](0030-rotating-certificates-rotate-the-instances-that-cannot-reload-them.md)
  D12 (`ensureTLSMaterialRecord`, which now runs ahead of the gate)
