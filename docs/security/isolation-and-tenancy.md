# Isolation and tenancy

What keeps one Valkey cluster apart from another and from the rest of its namespace, and —
to be read before a namespace is treated as a tenant boundary — what does not. The delete
guards are [ADR 0006](../adr/0006-delete-only-what-the-operator-owns.md), the write guards [ADR 0020](../adr/0020-write-only-what-the-operator-owns.md). What each grant
permits is [privilege footprint](privilege-footprint.md); the syscall filter a CR author
chooses is [seccomp profiles](seccomp-profiles.md).

## What holds

- Every generated object carries an ownerReference to its CR, so deleting the CR
  removes the whole cluster and nothing survives except user-owned Secrets and
  PVCs. Turning `spec.persistence.enabled` off leaves them behind too: it needs
  the manual StatefulSet migration in
  [ADR 0023](../adr/0023-volume-claim-templates-are-immutable.md), and the
  operator never deletes a PVC — so the RDB/AOF data of a cluster that is no
  longer persistent stays on disk until someone removes the claims by hand.
- Each Valkey CR gets **its own** ServiceAccount, Role and RoleBinding
  (`<cr-name>-sidecar`), and the Role names the pods it may patch, so the blast
  radius of a stolen sidecar token is **one cluster** — not the namespace, and not
  the fleet ([the per-instance sidecar Role](privilege-footprint.md#the-per-instance-sidecar-role)).
- The observer runs under `<cr-name>-observer`, which is bound to no Role and
  mounts no token at all
  ([ADR 0012](../adr/0012-the-sidecar-records-its-drain-promotion-on-the-pod.md) D8 step 2).
- Every pod template the operator renders is rootless (uid 999, no capability, `no_new_privs`,
  a seccomp filter that is never `Unconfined`, a read-only root filesystem), with no option
  ([workload pod posture](workload-pod-posture.md#the-fields-on-every-generated-pod),
  [ADR 0032](../adr/0032-generated-pods-run-rootless.md) D1). The one exception is the
  migration repair's root init container while it is on the template
  ([rootless migration](rootless-migration.md#fix-data-ownership-the-one-root-process)). The
  unit tier checks every template against Pod Security `restricted`; on a node it ran on Kind,
  locally and in CI's E2E legs on `e6a9d7c`, the user-namespace half locally only ([H-16](workload-pod-posture.md#h-16),
  [ADR 0017](../adr/0017-test-and-ci-policy.md) D5) *(corrected 2026-09-27: this bullet said the
  rootless posture was verified on a node locally only, not in CI; ADR 0017 D5 records all three
  E2E legs green on `e6a9d7c`, only the user-namespace subtest skipped there)*. A user namespace
  is opt-in per Valkey resource and off by default ([user namespaces](user-namespaces.md)).
- `spec.networkPolicy.enabled` writes ingress-only NetworkPolicies
  ([`internal/builder/networkpolicy.go`](../../internal/builder/networkpolicy.go)):
  the data port accepts traffic from Valkey pods, Sentinel pods, observer pods and
  **the operator namespace** (matched by `kubernetes.io/metadata.name`); the
  sidecar health port and the exporter port are open to everyone, because kubelet
  probes come from the node and Prometheus is not locatable from the CR.
- The PDB cleanup never deletes a budget it does not own (ownerReference check)
  and sends a **UID delete precondition** so a name reused between the read and
  the delete is not destroyed ([`internal/controller/pdb.go`](../../internal/controller/pdb.go)).
- The operator **refuses to write** the observer ServiceAccount and the sidecar
  ServiceAccount, Role and RoleBinding when a generated name is held by an object
  it does not control, and it never grants the sidecar Role to a subject it does
  not own. A collision leaves that CR unwritable and visibly blocked instead of
  handing `pods: patch` to a stranger
  ([ADR 0020](../adr/0020-write-only-what-the-operator-owns.md),
  [`internal/controller/foreign_object.go`](../../internal/controller/foreign_object.go)).

  `BuildSidecarRoleBinding` names its subject by name, without a UID, so until 2026-08-21 the
  Role was granted to whatever identity held `<cr-name>-sidecar` — created by the operator or
  not. The observer refusal keeps the Deployment running because that identity grants it
  nothing.
- The same refusal covers the **data and Sentinel StatefulSets and the observer
  Deployment**, and a foreign StatefulSet is treated as **absent** by every other
  consumer: it is not nudged, no rolling update deletes its pods, and its replica
  counts never enter the CR status (ADR 0020 D8). The observer Deployment cleanup
  deletes only what the CR provably owns, with a UID precondition.

  The data StatefulSet carries the bare CR name — the likeliest name for an accidental or
  aimed collision — and until 2026-08-22 the Update installed the pod template into whatever
  held it. Both StatefulSet writes refuse and fail the pass; the observer Deployment refuses
  without failing. Operator upgrades are unaffected: every release since the first commit
  stamps the controller reference on create.

## What does not hold

Read this before treating a namespace as a tenant boundary.

- **The NetworkPolicies are ingress-only.** No egress rule is written, so a
  compromised Valkey pod may open connections anywhere, including to the API
  server.
- **The sidecar can patch any metadata on its own cluster's pods, and `pods: patch`
  is wider than metadata.** The grant is no longer namespace-wide —
  `resourceNames` limits it to `<cr-name>-0 … <cr-name>-N` ([the per-instance sidecar Role](privilege-footprint.md#the-per-instance-sidecar-role)) — and
  since 2026-08-27 only the **sidecar container** holds the token that carries it
  ([ADR 0012](../adr/0012-the-sidecar-records-its-drain-promotion-on-the-pod.md) D8
  step 4). Within that list it is still unrestricted. What a compromised sidecar can
  rewrite, enumerated rather than sampled — this list used to say "the third field"
  and stop at two:

  | Field | What it buys |
  |---|---|
  | `instanceRole` label | the `-rw` and `-r` Services select on it; setting `master` diverts client writes |
  | `vko.gtrfc.com/drain-promoted-at` | the operator consumes it as promotion evidence ([ADR 0012](../adr/0012-the-sidecar-records-its-drain-promotion-on-the-pod.md) D6, [ADR 0028](../adr/0028-a-demotion-may-not-discard-the-only-dataset.md)) |
  | `vko.gtrfc.com/config-hash` | suppresses the rolling update for a config change |
  | `vko.gtrfc.com/pod-spec-hash` | suppresses the rolling update for a pod-spec change |
  | ~~`vko.gtrfc.com/tls-material-hash`~~ | it did suppress the certificate-rotation roll **and** the `TLSMaterialStale` report; the record moved into pod spec on 2026-08-27 and the annotation is now inert ([ADR 0031](../adr/0031-a-record-the-operator-trusts-lives-in-pod-spec.md)) |
  | any selector label | deleting one detaches the pod from its StatefulSet controllerRef; `podIsOurs` then reads false. The operator holds that CR's roll and reports `ReconcileBlocked=True/ForeignObject` naming the pod ([ADR 0020](../adr/0020-write-only-what-the-operator-owns.md) D9) until a human deletes or re-attaches it; it never touches the pod |
  | `metadata.ownerReferences` | not in apimachinery's immutable ObjectMeta set — that set is exactly `name`, `namespace`, `uid`, `creationTimestamp`, `deletionTimestamp`, `deletionGracePeriodSeconds`. Same hold-and-report as above |
  | `metadata.finalizers` | same; a foreign finalizer keeps the pod from ever being deleted |
  | `spec.containers[*].image` | one of the five entries the API server allows a pod update to change. Since 2026-09-28 the data and Sentinel tiers compare **every** container and init-container image against the persisted template, so a swapped image runs at once but is replaced by the next data-tier roll — except on a single-replica non-persistent cluster, where a swap that also moves the sidecar image is deferred with it ([ADR 0007](../adr/0007-failover-aware-rolling-update.md) D2, D6) |

  Nine rows, of which the struck-through one is no longer reachable: eight are live.
  For every hash still in that table the **deletion** is cheaper than the forgery,
  because both consumers carry a presence guard (`recorded != "" && recorded !=
  desired`), so setting the key to `null` makes the pod unmeasured rather than
  mismatched. That is why the TLS fingerprint's answer was to leave pod metadata and
  not to get a stronger digest, and it is the argument for moving `config-hash` and
  `pod-spec-hash` next ([ADR 0031](../adr/0031-a-record-the-operator-trusts-lives-in-pod-spec.md)
  D6). Nothing narrower is expressible for the label and the drain stamp: those
  are the writes the sidecar exists to make
  ([ADR 0012](../adr/0012-the-sidecar-records-its-drain-promotion-on-the-pod.md) D8).
- ~~**The workload pods have no securityContext at all.** No `runAsNonRoot`, no
  `readOnlyRootFilesystem`, no `capabilities: drop [ALL]`, no
  `seccompProfile` — verified by the absence of any `SecurityContext` in
  `internal/builder`. The operator's *own* Deployment sets all four
  ([`deployment.yaml`](../../deploy/helm/valkey-operator/templates/deployment.yaml)); the
  clusters it creates inherit whatever the namespace's Pod Security admission
  level allows. A restricted-PSA namespace will reject these pods outright.~~
  **Superseded 2026-09-26 by [ADR 0032](../adr/0032-generated-pods-run-rootless.md) D1:
  every pod template the operator renders is rootless, with no option ([workload pod posture](workload-pod-posture.md)).** The struck list was
  also short by one: it named four controls and omitted `allowPrivilegeEscalation: false`,
  the fifth the operator's own Deployment sets
  ([ADR 0013](../adr/0013-operator-is-cluster-wide-privileged.md) D8) and every generated
  container now carries. What still does not hold is the **migration window**. Pods an
  earlier operator built stay root until their roll replaces them, and some never are
  ([how existing clusters move](rootless-migration.md#how-existing-clusters-move),
  [H-21](rootless-migration.md#h-21)). Data pods created while the repair was on the template
  keep its root init container until the second roll
  ([fix-data-ownership](rootless-migration.md#fix-data-ownership-the-one-root-process)).
- ~~**The data pod mounts the sidecar token into every container.**
  `automountServiceAccountToken` is disabled on the observer pod and nowhere else,
  so the `valkey` and `exporter` containers carry the sidecar token too. It is a
  pod-level field, so splitting it per container is not expressible in Kubernetes;
  a separate ServiceAccount per container would need a separate pod.~~
  **No longer true, and the last sentence never was.** Since 2026-08-27 the data pod
  sets `automountServiceAccountToken: false` and projects the token into the
  `sidecar` container alone; the Sentinel pod sets the flag and projects nothing.
  The split needs no second ServiceAccount and no second pod — a hand-declared
  `projected` volume with a `serviceAccountToken` source, mounted into one
  container, has been GA since Kubernetes 1.20
  ([ADR 0012](../adr/0012-the-sidecar-records-its-drain-promotion-on-the-pod.md) D8
  step 4).
- **A CR author picks the image.** `spec.image` and `spec.metrics.image` are
  arbitrary strings with no registry allowlist, and ~~the pods run with the
  namespace's default security posture~~ (superseded 2026-09-26 by
  [ADR 0032](../adr/0032-generated-pods-run-rootless.md) D1) whatever user the image
  declares, its containers run as uid 999 with no capability, `no_new_privs` and
  ~~`RuntimeDefault` seccomp~~ the seccomp filter of `spec.podSecurity.seccompProfile`
  *(amended 2026-09-26, ADR 0033 D1)* ([workload pod posture](workload-pod-posture.md#the-fields-on-every-generated-pod)). The choice no longer buys root inside the
  container; it still buys arbitrary code next to the cluster password and the dataset —
  and in a pod created while the migration repair was on the template, until the second roll
  replaces it, `spec.image` is also the image of the one root init container. A digest-pinned
  `spec.image` is deployable since 2026-09-26 (it used to produce an invalid
  `app.kubernetes.io/version` label, ADR 0033 D5), so a CR author *can* pin what runs; nothing
  makes them.
- **A CR author also picks the seccomp profile**, among the `Localhost` profiles the
  operator's allow-list names — none by default. What that choice buys, and what the
  allow-list does and does not defend against, is
  [seccomp profiles](seccomp-profiles.md#what-a-cr-author-chooses).
- **Every managed object name is derived from the CR name, and the pod door is
  still open.** There is no admission webhook constraining CR names
  ([ADR 0015](../adr/0015-one-crd-validated-by-schema-only.md)), so whoever may
  `create valkeys` in a namespace chooses the names of that CR's derived objects.
  Since the 2026-08-22 amendment of
  [ADR 0020](../adr/0020-write-only-what-the-operator-owns.md) **every managed
  object family is guarded on both sides**: fourteen reconcile paths refuse to
  write an object the CR does not control, and every delete except
  `deleteLegacyServices` proves ownership and sends a UID precondition
  ([ADR 0006](../adr/0006-delete-only-what-the-operator-owns.md)). In particular
  the operator no longer stamps its controller ownerReference onto a ServiceMonitor
  or a cert-manager Certificate it did not verify, so a CR deletion can no longer
  garbage-collect a foreign object it adopted by name.

  Until 2026-08-22 the `reconcileCertificate` branch that stamped the ownerReference also
  rewrote a foreign Certificate's `secretName` and `issuerRef`, which cost the other party their Secret without
  waiting for any deletion, and `cleanupMetricsService`, `cleanupServiceMonitor` and the
  NetworkPolicy half of `cleanupObserverDeployment` deleted whatever held the derived name —
  the trigger was one boolean in a CR its author controls. `replicaConfigMaster` treats a
  foreign replica ConfigMap as absent, so a stranger's `replicaof` directive can no longer
  feed the master authority.

  Pods are covered too, since 2026-08-22: a pod's controller is its
  StatefulSet rather than the CR, so the proof runs `pod -> StatefulSet -> CR`
  (ADR 0020 D9). That closed three unequal doors — the sidecar Role granting
  `patch` on a foreign pod, an annotation Patch onto one, and the rolling update
  reading, counting and **deleting** one. ~~Only the first of those needed anything
  beyond the label set.~~ *(Corrected 2026-09-27, read against the code before the
  amendment: the annotation Patch needed only the label set — `clearDrainStamps` listed
  pods by the cluster's selector labels and patched every match that carried a drain
  stamp; the sidecar Role grant needed the label set plus a name of the
  `<cr-name>-<ordinal>` form, because `SidecarRolePodNames` already dropped every other
  listed name; and the rolling update needed no label at all, only a pod under a
  generated name below `spec.replicas`, which it read by name. None of the three needed
  the CR password or a per-pod record under the headless Service, which the steady-state
  command paths did ([ADR 0020](../adr/0020-write-only-what-the-operator-owns.md)
  Context). ADR 0020 says the sidecar grant needed only the label set, which leaves out
  the name filter.)*

  Two gaps remain, both stated rather than fixed. **The guards protect only
  forward:** an object a *previous* release already stamped passes the ownership
  check, and nothing in the object distinguishes it from a genuine child — the same
  Update replaced its labels and wrote the operator-version annotation. Look before
  upgrading; there is no detection. And **upstream adoption bounds the pod guard:**
  the statefulset-controller adopts an orphan pod that matches its selector and
  stamps its own controller reference, so a pod built to carry a cluster's label set
  and left without a controller becomes genuinely that cluster's by Kubernetes' own
  rules. The guard closes collisions and strays, not a deliberate mimic.
- **Namespace is not a trust boundary for the operator itself.** It watches and
  writes everywhere.

## What this does not cover

<a id="h-9"></a>

### H-9: Before upgrading, look for objects an earlier release already adopted

The ADR 0020 D1 guard on every managed kind (2026-08-22) is not retroactive.
A ServiceMonitor or cert-manager Certificate that collided with a derived
name under an earlier release carries this CR's controller ownerReference
today, and deleting the CR will garbage-collect it. No field distinguishes
such an object from a genuine child, so this cannot be automated: compare the
ServiceMonitors and Certificates under `<cr>` names against what you expect
the operator to have created, in every namespace that runs a Valkey.

<a id="h-10"></a>

### H-10: Add egress NetworkPolicies

Today's policies are ingress-only, so a
compromised data pod can talk to anything, the API server included.

<a id="h-11"></a>

### H-11: Restrict who may `create valkeys`

A CR author chooses the image the
cluster runs and the name every generated object gets, and generated names
collide with existing objects by design.
[ADR 0006](../adr/0006-delete-only-what-the-operator-owns.md) closed the
deletes and [ADR 0020](../adr/0020-write-only-what-the-operator-owns.md)
closed every write, so a collision is now refused and reported rather than
acted on. What the guards do **not** undo is the image choice, the objects an
earlier release already adopted, or the pod door ([what does not hold](#what-does-not-hold)). The CR-name
grant stays the control that bounds all three, and any new write or delete by
generated name needs the same provenance discipline. Since 2026-09-26 a CR author
also chooses the seccomp profile ~~among those on the nodes~~ among the `Localhost`
profiles the operator's allow-list names, none by default *(amended 2026-09-26, ADR 0033
D9)* ([what a CR author chooses](seccomp-profiles.md#what-a-cr-author-chooses), [H-19](seccomp-profiles.md#h-19)).
