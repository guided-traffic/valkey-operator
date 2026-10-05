# Fleet upgrade analysis: wds18-main, operator 1.10.48 → `feat/support-pdb`

> **Archived 2026-09-27** as ticket 038, renamed from `local_fleet_upgrade_analysis.md` when the tickets were numbered.

> **Status: HISTORICAL — superseded, no open work. Reviewed 2026-08-26.** Index:
> [`039-findings-from-the-1-11-0-fleet-rollout.md`](039-findings-from-the-1-11-0-fleet-rollout.md). Keep this line current.
>
> This is the pre-upgrade analysis written on 2026-08-21 for the 1.10.48 → `feat/support-pdb`
> step. **That upgrade happened** — it went live on 2026-08-22 ~21:32 UTC as 1.11.0. What the
> rollout actually produced, including everything this document could only predict, is in
> [`039-findings-from-the-1-11-0-fleet-rollout.md`](039-findings-from-the-1-11-0-fleet-rollout.md), which supersedes it.
>
> Read this file for the *before* picture only. Every version number in it is stale: the
> fleet now runs **v1.11.1** (`origin/main` = `81e1108`), and the branch carrying T1–T6, T11
> and T14 is **10 commits ahead of that and unreleased** — see the cross-cutting note in the
> index of `039-findings-from-the-1-11-0-fleet-rollout.md`.

Date: 2026-08-21. Cluster: `wds18-main`
(`/Users/hfi/repos/business_onpremise/kubernetes_configs/wds18-k8s-main`), read-only inspection
only. Nothing was changed on the cluster while producing this document.

Every number below is either **measured** against the live cluster / this repository, or marked
**not verified**. There is no third category.

---

## 1. What is actually deployed

| Fact | Value | How established |
|---|---|---|
| Operator image | `guidedtraffic/valkey-operator:1.10.48` | `kubectl -n database-operators get deploy valkey-operator` |
| Install method | Flux `HelmRelease` `database-operators/valkey-operator`, chart `valkey-operator@1.10.48`, release **v84** | `kubectl get helmrelease -A` |
| Chart source | `HelmRepository flux-system/valkey-operator`, **`version: "*"`**, `reconcileStrategy: ChartVersion`, `interval: 1m` | HelmRelease spec |
| Rollback depth | `maxHistory: 2` | HelmRelease spec |
| Pre-upgrade hook | `preUpgradeHook.enabled: true` | HelmRelease values |
| Valkey CRs | 12 | `kubectl get valkey -A` |
| Git position of 1.10.48 | tag `v1.10.48` = `3a8b660`, reachable from `origin/main` — so **deployed == main** | `git merge-base --is-ancestor` |
| Upgrade delta | **48 non-dependency commits**, i.e. the entire `feat/support-pdb` branch | `git log v1.10.48..HEAD` |

### The fleet

| Namespace / CR | replicas | Sentinel | TLS | persistence | phase | status.operatorVersion |
|---|---|---|---|---|---|---|
| `database-examples/valkey8` | 3 | – | – | – | OK | 1.10.48 |
| `database-examples/valkey8-tls` | 3 | – | yes | – | OK | 1.10.48 |
| `database-examples/valkey9` | 3 | – | – | – | OK | 1.10.48 |
| `database-examples/valkey9-tls` | 3 | – | yes | – | OK | 1.10.48 |
| `database-examples/valkey8-sentinal` | 3 | 3 | – | – | OK | 1.10.48 |
| `database-examples/valkey8-sentinal-tls` | 3 | 3 | yes | – | OK | 1.10.48 |
| `database-examples/valkey9-sentinal` | 3 | 3 | – | – | OK | 1.10.48 |
| `database-examples/valkey9-sentinal-tls` | 3 | 3 | yes | – | OK | 1.10.48 |
| **`gitlab/gitlab-valkey`** | 3 | 3 | yes | **yes** | **Error** | **1.9.6** |
| `gpt/gpt-valkey` | 3 | 3 | yes | – | OK | 1.10.48 |
| `harbor/harbor-valkey` | 3 | 3 | yes | – | OK | 1.10.48 |
| `iam/oauth2-valkey` | 3 | 3 | yes | – | OK | 1.10.48 |

The eight `database-examples` instances are throwaway; four instances are production
(`gitlab`, `gpt`, `harbor`, `iam`). The masters sit on mixed ordinals (`harbor-valkey-0`,
`gpt-valkey-1`, `oauth2-valkey-2`, `gitlab-valkey-2`) — the supported non-pod-0 end state.

---

## 2. Three findings that change the plan

### 2.1 The fleet upgrades itself, unattended, the moment a release is published

`HelmRelease.spec.chart.spec.version` is `"*"` with `reconcileStrategy: ChartVersion` and a
1-minute interval. Merging this branch to `main` publishes a chart through semantic-release
(`.releaserc.json` releases from `main`), and Flux picks it up within a minute. **There is no
approval step between "merge to main" and "all 12 production and example clusters restart
their pods".**

This is the single most urgent item, and it is independent of everything else in this
document: pin the version before the branch merges.

### 2.2 `gitlab/gitlab-valkey` has been broken for months and this upgrade does not fix it

```
message: Failed to reconcile StatefulSet: StatefulSet.apps "gitlab-valkey" is invalid:
         spec.template.spec.containers[0].volumeMounts[1].name: Not found: "data"
status.operatorVersion: 1.9.6      # the fleet runs 1.10.48
sidecar image in the running pods: 1.9.6
```

Measured cause: the running StatefulSet has **no `volumeClaimTemplates`** and carries a plain
pod-level volume named `data`, i.e. it was created while `persistence.enabled` was false. The
CR now says `persistence.enabled: true` (`5Gi`, `local-storage`). The operator's desired pod
template therefore mounts `data` from a volumeClaimTemplate — but `reconcileStatefulSet` only
writes `Spec.Replicas`, `Spec.Template` and `Labels`
([valkey_controller.go:1089](internal/controller/valkey_controller.go#L1089)), never
`volumeClaimTemplates`, and those are immutable on an existing StatefulSet anyway. The result
is a pod template referencing a volume that does not exist → rejected by the API server on
every pass, forever.

**Consequence for the fleet upgrade:** this CR will not roll, will not pick up the new sidecar,
and will keep reporting `Error`. It needs its own remediation (section 6), and it is the only
instance in the fleet with a real downtime risk.

**The general rule this exposes:** the operator does not support toggling
`spec.persistence.enabled` on an existing cluster. That is worth an ADR of its own; it is not
caused by this branch.

**Where the change came from.** `spec.persistence` on the live object is owned by the field
manager `kustomize-controller` (apply, 2026-04-25T09:51:08Z), so it arrived through Flux, not
through a hand edit. The Kustomization `flux-system/gitlab-valkey` applies
`./apps/gitlab/valkey` from the GitRepository **`wds18-apps-flux`**
(`github.com/hans-fischer/wds18-apps-flux`) — **not** from the `k8s-flux-mgmt` checkout, which
is a different repository (`gitlab.sutorbank.cloud/devops/flux-environments/k8s-flux-mgmt`) and
is not referenced by any GitRepository on this cluster. Harbor and GPT come from
`wds18-apps-flux` as well; the operator itself and `database-examples` come from
`k8s-base-flux`. **Not verified:** `wds18-apps-flux` is not checked out locally, so the
persistence block there was inferred from field ownership rather than read.

**The mechanism, exactly.** The `data` volumeMount on the valkey container is unconditional and
sits at index 1 ([statefulset.go:611](internal/builder/statefulset.go#L611)). With persistence
**off** the operator supplies `data` as a pod-level `emptyDir`
([statefulset.go:524](internal/builder/statefulset.go#L524)); with persistence **on** it does
not, and expects `data` to come from `spec.volumeClaimTemplates`
([statefulset.go:138](internal/builder/statefulset.go#L138)). `volumeClaimTemplates` is written
**only on Create** ([valkey_controller.go:1102](internal/controller/valkey_controller.go#L1102));
the update path copies `Spec.Replicas`, `Spec.Template` and `Labels` and nothing else, and
`StatefulSetHasChanged` never compares the field. So switching persistence on removes the
emptyDir from the desired template without ever adding the claim template — the mount resolves
to nothing and the API server rejects the write on every pass. The reverse direction (on → off)
does converge, because the emptyDir is simply added back. The Sentinel StatefulSet is unaffected:
it always carries a pod-level `data` emptyDir regardless of persistence
([sentinel.go:292](internal/builder/sentinel.go#L292)).

### 2.3 There is no way to stage the rollout

Measured: the operator is cluster-wide (no namespace-scoping flag in
[cmd/main.go](cmd/main.go), `bindOperatorFlags` has exactly four flags), and there is **no
pause or hold switch** — neither a CR field nor an annotation. `ConditionTypeRollingUpdatePaused`
is a *failure* state set by `pauseRollingUpdate` after a sync timeout
([rolling_update.go:1443](internal/controller/rolling_update.go#L1443)), not a user control.

So the moment the new operator becomes leader, every CR whose pod-spec hash changed enters the
rolling update, `maxConcurrentReconciles` at a time. Canarying `database-examples` before
`harbor` is **not possible today** — and lowering `maxConcurrentReconciles` does not create a
gate either, only a narrower simultaneous blast radius (section 5, step 4). See section 7 for
the two ways out.

---

## 3. What the upgrade does to a running cluster, per object

### 3.1 CRD — additive only, upgrade-neutral

`git diff v1.10.48..HEAD -- config/crd/bases/` adds exactly two blocks:

* `spec.antiAffinity` (`mode` default **`off`**, `topologyKey` default `kubernetes.io/hostname`)
* `spec.podDisruptionBudget` (`enabled` default **`false`**, `maxUnavailable` default `1`)

plus prose on `spec.sentinel.replicas`. No field removed, no validation tightened, no required
field added. Applying the new CRD against the 12 existing CRs changes nothing about them, and
`mode: off` renders **no** affinity term (`BuildPodAntiAffinity` returns `nil`,
[affinity.go:20](internal/builder/affinity.go#L20)), so scheduling is untouched.

### 3.2 ClusterRole — three new grants, needed *before* the new operator runs

| Grant | Why | Failure without it |
|---|---|---|
| `secrets: delete` | `reconcileLegacySentinelCertificateCleanup` | 403 on every pass — only for CRs with `tls.unifiedCertificate: true`; **none in this fleet** |
| `events.k8s.io: events` | the operator records through `events.k8s.io/v1` | Events silently lost |
| `policy: poddisruptionbudgets` (full) | `spec.podDisruptionBudget` | 403 — only when a CR opts in; **none in this fleet** |

All three are in `deploy/helm/valkey-operator/templates/clusterrole.yaml`, and Helm's fixed
install order applies RBAC before workloads, so a chart upgrade carries them in the right
order. This is only a hazard if someone bumps the image without the chart — which the README
already calls unsupported.

### 3.3 Pod templates — measured, not guessed

I recomputed the operator's own hashes for the live CRs with a throwaway program
(`tmp/hashcheck`, gitignored) that imports `internal/builder` and hashes the built pod spec, the
same way `ComputePodSpecHash` / `ComputeSentinelPodSpecHash` do. Running it at branch HEAD **with
the currently deployed image tag** reproduces the live annotations exactly for the Sentinel-enabled
data StatefulSets and for every config hash — which is what makes the deltas below trustworthy.

| CR | data hash live | HEAD @ old tag | HEAD @ new tag | sentinel hash live | HEAD | config hash |
|---|---|---|---|---|---|---|
| `harbor/harbor-valkey` | `86c97756` | `86c97756` = | `88f2ca5d` ≠ | `de487ed3` | `97247556` ≠ | unchanged |
| `gpt/gpt-valkey` | `52dbfeb4` | `52dbfeb4` = | `2f406427` ≠ | `eede0050` | `f715bd6b` ≠ | unchanged |
| `iam/oauth2-valkey` | `f3479ba6` | `f3479ba6` = | `6ffd551b` ≠ | `25f37bfb` | `2c4c072e` ≠ | unchanged |
| `database-examples/valkey8` | `2802ce32` | `cf5ae542` **≠** | `b1cebaf1` ≠ | n/a | n/a | unchanged |

Reading:

* **Data pods roll on every instance** — because the sidecar container image is part of the pod
  spec and the chart derives `--operator-image` from the operator tag. This is by design and
  the README documents it.
* **The init-container script change is not neutral for non-Sentinel clusters.** The new
  Phase-2 known-master block ([statefulset.go:432](internal/builder/statefulset.go#L432))
  changes the built spec for `valkey8`/`valkey8-tls`/`valkey9`/`valkey9-tls` even at an
  unchanged image tag, and leaves the Sentinel-enabled clusters byte-identical. Nothing in this
  fleet depends on that difference, but it is the one delta that would survive an image pin.
* **Sentinel pods roll on every Sentinel-enabled instance** — `buildSentinelPodSpec` now sets
  `terminationGracePeriodSeconds: 30` explicitly ([sentinel.go:386](internal/builder/sentinel.go#L386))
  where it previously left it nil. The stored object already *has* 30 (API default), but the
  hash is computed over the **built** spec, so the annotation changes and the template with it.
  This is a one-time roll; it is the fix for a drift-rewrite loop, and after this upgrade the
  Sentinel template stops changing.
* **ConfigMaps do not change** — every config hash is identical, `internal/builder/configmap.go`
  is untouched in the delta.
* **Observer Deployments** roll on the eight example CRs (`fa50b89` gives the observer a
  token-less ServiceAccount); the four production CRs have no observer.

### 3.4 Ownership — the new refusal guard blocks nothing in this fleet

`9f1efaa` (ADR 0020) refuses writes onto a generated name the operator cannot prove it owns. I
audited **every** generated name of all 12 CRs against the live cluster — StatefulSets,
Services, ConfigMaps, NetworkPolicies, ServiceAccounts, Roles, RoleBindings, Certificates,
ServiceMonitors, PDBs:

```
total objects checked: 200+
not owned:            0
absent:               8   (the <cr>-observer ServiceAccount, which this branch introduces)
```

Every managed object carries the CR's controller ownerReference with a matching UID. The eight
absent observer ServiceAccounts are created fresh by the new operator. **No refusal, no
`ReconcileBlocked`, no manual ownerReference repair.** (Script: `scratchpad/audit.py`.)

The hand-written NetworkPolicies in the Flux repo (`harbor-valkey-cluster`,
`harbor-valkey-sentinel`, and the GitLab equivalents) do **not** collide: the operator generates
`np-harbor-valkey` / `np-harbor-valkey-sentinel` from `networkPolicy.namePrefix: np`, a
different namespace of names.

### 3.5 Pre-upgrade hook

The chart's `pre-upgrade-job.yaml` runs `manager migrate` with the **new** image and patches
missing defaults into every CR ([cmd/migrate/migrate.go](cmd/migrate/migrate.go), `applyDefaults`).
Flux drives Helm through the Helm SDK, so the hook does run under a `HelmRelease`.

Against this fleet it will set `spec.tls.certManager.issuer.group: cert-manager.io` on the CRs
that omit it (harbor does; gitlab already has it). That is a spec write on a Flux-managed
object. **Not verified:** whether the kustomize-controller's server-side-apply then fights over
that field on its next pass. It declares no `group` in Git, so under SSA it should not claim
ownership of a field it does not set — but this was reasoned from the SSA contract, not
observed on this cluster.

---

## 4. What the interruption actually looks like

Harbor connects through **Sentinel over TLS**, addressing the three Sentinel pods by their
headless DNS names on port 36379 (`components/harbor/release/helm-release.yml`,
`sentinelMasterSet: harbor-valkey`). So per instance the upgrade produces two phases:

1. **Data rolling update** (failover-aware, [ADR 0007](docs/adr/0007-failover-aware-rolling-update.md)):
   replicas replaced one at a time, waiting for replication sync after each; then a controlled
   failover; then the former master. Client impact: one failover window. Sentinel publishes the
   new master and go-redis reconnects. Writes fail for the duration of the failover, reads
   continue against replicas.
2. **Sentinel rolling update**, which only starts once the data update reports complete
   ([valkey_controller.go:370](internal/controller/valkey_controller.go#L370)): pods replaced
   one by one with a quorum check before each deletion. With 3 Sentinels, quorum 2 holds
   throughout. Client impact: one of three discovery endpoints is briefly unreachable — go-redis
   fails over to the next.

Bounds that cap a stuck instance rather than describe the normal case:
`sentinelAwarenessTimeout` 90 s, `replicaReconnectTimeout` 90 s, `finalizationStallTimeout`
2 min ([rolling_update.go](internal/controller/rolling_update.go)). A healthy 3+3 instance is
minutes; a wedged one is bounded and escalates through the documented states rather than
hanging.

**Not verified:** no timing was measured on this cluster or in Kind. The numbers above are the
code's bounds, not observed durations.

With `maxConcurrentReconciles: 4` (the new chart default) up to four instances are in that
state simultaneously.

---

## 5. Recommended GitOps procedure

Steps 1–3 are worth doing **before** the branch merges, because of 2.1.

**Step 1 — pin the operator version (do this first, independent of everything else).**

```yaml
# HelmRelease database-operators/valkey-operator
spec:
  chart:
    spec:
      version: "1.10.48"      # was: "*"
```

From then on, an upgrade is a reviewable commit that changes one line.

**Step 2 — raise `maxHistory`.** `maxHistory: 2` gives two rollback points for a change of this
size. `maxHistory: 5`.

**Step 3 — remediate `gitlab/gitlab-valkey`** (section 6) or accept that it stays on 1.9.6.
Doing it *before* the upgrade means one broken thing at a time.

**Step 4 — bound the blast radius for the upgrade window.** In the same commit that bumps the
version:

```yaml
spec:
  values:
    maxConcurrentReconciles: 1     # one instance rolls at a time
```

Revert it to 4 in a follow-up commit once the fleet is green.

**What this does and does not buy.** It bounds how many reconcile *passes* run at the same
moment, so at most one cluster is churning pods at any instant. It does **not** gate instance 2
behind instance 1 finishing: a rolling update returns to the work queue between its steps
(`RequeueAfter`), and the single worker then picks up whichever CR is due next. All twelve
therefore still progress, interleaved. It reduces simultaneous blast radius; it is not a canary
gate. Nothing in the operator today is a canary gate — that is section 7.

**Step 5 — the version bump.** One commit: `version: "1.10.48"` → the new release. Flux runs
`helm upgrade`, which applies CRD → ClusterRole → pre-upgrade Job → Deployment in Helm's order.

**Step 6 — watch.**

```bash
kubectl -n database-operators rollout status deploy/valkey-operator
kubectl get valkey -A -w
kubectl get events -A --field-selector type=Warning | grep -i valkey
```

Every instance must return to `PHASE=OK` with `READY == REPLICAS`, and
`status.operatorVersion` must show the new version. `gitlab-valkey` will not.

**Step 7 — rollback, if needed.** `flux suspend hr valkey-operator -n database-operators` stops
the reconcile loop; reverting the version commit and resuming rolls the operator back. Note
that a rollback rolls the **pods** a second time (the sidecar tag goes back), so it is not free —
suspending is the cheaper first move while diagnosing.

---

## 6. `gitlab/gitlab-valkey` remediation

The StatefulSet must be recreated with volumeClaimTemplates. The pods currently hold their data
in a non-persistent volume, so the data lives only in the running processes and their
replication stream.

Sketch, **not executed and not verified on this cluster**:

1. Confirm the master (`status.masterPod`, currently `gitlab-valkey-2`) and that all three pods
   are in sync.
2. `kubectl -n gitlab delete sts gitlab-valkey --cascade=orphan` — the pods keep running and
   keep serving.
3. The operator recreates the StatefulSet, now with the `data` volumeClaimTemplate, and adopts
   the running pods by label.
4. The pods no longer match the template, so the failover-aware rolling update replaces them one
   at a time: each new pod comes up with a PVC, syncs from the master, and only then is the next
   one taken — the master last, after a controlled failover.

Risks to weigh before doing it: `local-storage` PVCs must actually bind on the nodes the pods
land on (with a `WaitForFirstConsumer` class that is decided at scheduling time); and step 2
leaves the cluster without a StatefulSet controller for as long as the operator needs to
recreate it, so it should be done with the operator healthy and watched.

Whether this happens before or after the operator upgrade is a real choice: before means one
change at a time; after means the new operator's bounded waits and `ReconcileBlocked` reporting
are already in place while doing it.

---

## 7. The staging gap, and the two ways to close it

Section 2.3 established there is no way to upgrade `harbor` after `database-examples`. Two
shapes close it:

**A — a per-CR hold on pod replacement.** A CR field (`spec.updatePolicy.paused: true`, or an
annotation) that the operator honours at the top of the rolling-update check: it keeps
reconciling, keeps failing over, keeps reporting status, but does not replace pods. Staged
rollout then becomes pure GitOps: set the hold on all CRs, upgrade the operator, then remove
the hold one commit at a time. Needs a status condition so a held instance is not silently
stale, and a decision about what a held instance reports as its phase.

**B — a separate sidecar image value.** Decouple `--operator-image` from `image.tag` so the
operator can be upgraded without changing the managed pod spec, then bump the sidecar
separately. Weaker: it does not cover the init-script change (3.3), which is not image-gated,
so non-Sentinel clusters would still roll on the operator upgrade.

A is the one that actually delivers staged rollout. It is also a new API field and therefore a
decision, not a patch.

---

## 8. Residual risks and what was not verified

* **No timing was measured.** Every duration in section 4 is a code bound, not an observation.
* **Nothing was reproduced in Kind.** The e2e test proposed alongside this document is what
  turns the measured hashes into an executed proof.
* **The SSA interaction between the pre-upgrade hook and the kustomize-controller** (3.5) is
  reasoned from the contract only.
* **`local-storage` PVC binding** for the GitLab remediation is untested.
* **The hash reproduction covers four CRs**, not all twelve — harbor, gpt, iam and valkey8 were
  chosen to cover Sentinel/non-Sentinel and TLS/non-TLS. The remaining eight are structurally
  identical to one of those four.
* **The ownership audit is a point-in-time snapshot** of 2026-08-21. An object whose
  ownerReference is removed between now and the upgrade would be refused by the new guard.
  Re-run `scratchpad/audit.py` immediately before the upgrade.
