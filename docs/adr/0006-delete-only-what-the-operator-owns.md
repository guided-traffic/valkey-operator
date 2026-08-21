# ADR 0006: Delete Only What the Operator Can Prove It Owns

## Status

Accepted. Date: 2026-08-21.

Implemented on branch `feat/support-pdb`. The PDB half is covered by unit, envtest and
e2e tests in the tree: [`internal/controller/pdb_test.go`](../../internal/controller/pdb_test.go),
[`test/integration/pdb_uid_precondition_test.go`](../../test/integration/pdb_uid_precondition_test.go)
and [`test/e2e/pdb_test.go`](../../test/e2e/pdb_test.go). That those files exist and
assert the guard is verified by reading; whether they ever ran green in CI is not
verifiable from this repository. The legacy Sentinel TLS half landed as commit `b81e0ed`
("fix(controller): gate the legacy Sentinel TLS cleanup on provenance").

Three items stay **open**: the name-only cleanups (`cleanupMetricsService`,
`cleanupObserverDeployment`, `cleanupServiceMonitor`), the delete-and-recreate in
`reconcileSidecarRoleBinding`, and `deleteLegacyServices`, which scans ownerReferences
but takes its Delete without a UID precondition — see Residual risks.

## Context

Every object this operator manages is named from the CR name. `spec.podDisruptionBudget`
produces `<cr-name>` and `<cr-name>-sentinel`; the split-certificate TLS mode produces
`<cr-name>-sentinel-tls`. **Generated names collide with pre-existing objects by
design**, because the CR name is chosen by whoever may `create valkeys` in the namespace.

Two concrete collisions turn that into destruction. Both were found by reading the code
on this branch; neither is a report of an object observed being destroyed on a cluster,
and the second bullet covers two objects whose exposure differs:

* **PodDisruptionBudgets.** `cleanupPodDisruptionBudget` fetched by name only and
  deleted unconditionally, and the `PodDisruptionBudgets` reconcile step has no `when`
  predicate — so it ran on every pass of every CR whose block is absent or disabled,
  i.e. every pre-existing CR after the upgrade. The collision is the *expected*
  configuration, not an edge case: the opt-in design's own rationale is that users
  already manage their own budgets, the natural name for a hand-written PDB covering the
  data pods is the StatefulSet name, and hand-creating exactly that PDB was the obvious
  remediation before the feature existed. That it was ever *documented* as such is not
  verified in this repository: nothing in the tree before `604cd91`, the commit that
  added the feature, mentions PodDisruptionBudgets at all. Such a budget carries no
  ownerReference, so its deletion triggers no `Owns()` event either — it just
  disappears, and recreating it lasts until the next pass. The update path was the mirror
  image: Get → HasChanged → Update silently *adopted* a foreign budget the moment the
  user set `enabled: true`.
* **The legacy Sentinel TLS Secret and Certificate.** The `unifiedCertificate`
  migration deleted `<cr-name>-sentinel-tls` on the name alone. The Helm binding is a
  ClusterRoleBinding, so the `delete` grant on core `secrets` is cluster-wide. A
  principal who may create `Valkey` CRs in namespace X can name one so the derived name
  collides with an unrelated Secret in X, set `spec.tls.certManager` plus
  `spec.tls.unifiedCertificate: true`, and the operator deletes a Secret that is not
  theirs. It needs no other rights and no timing: with Sentinel disabled
  `sentinelRolloutComplete` returns true immediately and `IsUnifiedCertificateEnabled`
  never consults Sentinel, so the Get/Delete pair runs on the **first** reconcile. The
  Certificate half has the identical shape and predates the branch: the name-only delete
  arrived with `1657c08`, which `git tag --contains` places in every `v1.10.x` tag, so
  unlike the Secret half it is live in released operators — and its collision is inferred
  from that code shape, never observed. The Secret half was unreachable through the
  shipping chart until `9e5634d` added `""/secrets: delete` to it, and that commit is
  branch-only; the kubebuilder marker and `config/rbac/role.yaml` carried the verb all
  along, so a kustomize install had it before that. Deleting a foreign Certificate stops
  somebody else's issuance and renewal.

## Decision

**D1 — No delete by generated name alone.** Every deletion the reconcile path performs
on an object under an operator-generated name requires a **provenance proof** and a
**UID delete precondition**. The canonical implementation is `cleanupPodDisruptionBudget`
([`internal/controller/pdb.go`](../../internal/controller/pdb.go)).

The rule binds every site added since the UID precondition landed. Three pre-existing sites do not yet satisfy
it and are tracked under Residual risks; `deleteLegacyServices` is the closest of them —
it scans `svc.OwnerReferences` for the CR UID, but matches *any* ownerReference rather
than the controller one D2 designates, and takes the Delete with no precondition.

**D2 — Provenance is the controller ownerReference, never a label.**
`metav1.IsControlledBy(obj, v)`, not `app.kubernetes.io/managed-by`. A label is
something a copied manifest or a GitOps template carries; the controller ownerReference
is not. A label-based check would let a hand-copied object be mistaken for an
operator-owned one and destroyed.

**D3 — Prefer self-issued ownership facts over external conventions.** The Certificate
delete rests on `IsControlledBy`, because the operator sets that ownerReference itself
on every Certificate it creates. An ownership check that rests on our own writes cannot
be invalidated by an upstream release.

**D4 — Where a self-issued fact is unavailable, a *named* external proof may stand in,
and only that one.** The legacy Secret is deleted on one of exactly two proofs:

* (a′) the same pass found a Certificate under the legacy name that this Valkey
  controls and whose `spec.secretName` is that legacy name — a derived, in-pass proof
  costing no second read; or
* (a) cert-manager's `cert-manager.io/certificate-name` annotation names the legacy
  **Certificate**.

The retroactive annotation proof exists because the population that actually needs
cleaning was issued long before this guard existed. Verified against cert-manager
v1.21.1 on the reference cluster (2026-08-21): present on 119 of 119 issued Secrets.
That sweep is a cluster measurement and is not reproducible from this repository. Its
in-tree traces are the comments on `certManagerCertificateNameAnnotation` and on
`deleteLegacySentinelSecret`
([`internal/controller/valkey_controller.go`](../../internal/controller/valkey_controller.go)),
and the fixture comment on `newLegacySentinelSecret`
([`internal/controller/valkey_controller_test.go`](../../internal/controller/valkey_controller_test.go)).

**D5 — The annotation is compared against the *Certificate* name, not the Secret
name.** For the legacy Sentinel material the two coincide, because
`SentinelCertificateName` and `SentinelTLSSecretName` both derive `<cr>-sentinel-tls`
in split-cert mode — but the comparison is written against the Certificate name so that
a future split of those derivations fails loudly instead of silently authorising the
wrong Secret. The semantics were verified on a real cluster where the two differ
(`cert-manager/trust-manager-tls` carries `cert-manager.io/certificate-name: trust-manager`)
— same sweep, measured on a cluster and not reproducible from this repository.

**D6 — `type: kubernetes.io/tls` is a hard precondition above both proofs.**
`deleteLegacySentinelSecret` refuses any Secret of another type before evaluating
provenance. A Secret that is not `kubernetes.io/tls` was not issued by cert-manager
whatever it claims (cert-manager behaviour, corroborated only by the same cluster sweep
and not checkable here), so this removes the class of accidental collateral — token,
config and registry Secrets that happen to collide with the derived name. It is a
filter, not the guard: an attacker can still aim the name at a genuine TLS Secret.

**D7 — `controller.cert-manager.io/fao` is not an accept-path.** It is present on all
124 fao-labelled Secrets of the reference cluster (same 2026-08-21 sweep, measured on a
cluster and not reproducible from this repository) and is an independent cert-manager
signal — and it says "cert-manager owns this", not "we own this". Admitting it would
authorise deleting every foreign cert-manager-issued Secret that happens to carry the
derived name, reopening the exact hazard the guard closes.

**D8 — A UID precondition complements a provenance check and never substitutes for
one.** Three reconcile-path deletes carry `client.Preconditions{UID: &obj.UID}`:
`cleanupPodDisruptionBudget`, `deleteLegacySentinelCertificate` and
`deleteLegacySentinelSecret`. For the PDB and the Secret it closes only the
read-then-delete race, because those decisions are made on cache-backed reads. The
Certificate is read as `unstructured` and the manager runs with default client options
(`cmd/main.go` sets only Scheme, probe address and leader election), so
`Cache.Unstructured` is false and that Get bypasses the cache — there the precondition
closes only the API round-trip window. In neither case does it establish provenance: a
**name collision passes it perfectly** — the UID read is the foreign object's own UID,
so the Delete matches and succeeds. With no rollout window (Sentinel disabled) there is
not even a race left for it to win.

**D9 — UID only, never ResourceVersion.** kube-controller-manager rewrites PDB
`.status` (`disruptionsAllowed`, `currentHealthy`) continuously, so a cache-backed read
is routinely a few revisions behind and an RV precondition would reject nearly every
cleanup, forever. A changed ResourceVersion is still the same object; only a changed UID
is a different one. The nil ResourceVersion is asserted *with its reason* inside
`TestCleanupPodDisruptionBudget_DeletesWithUIDPrecondition` so it cannot be "fixed"
later.

**D10 — A precondition `Conflict` is the guard working, not a pass failure.** It is
logged and the function returns nil; a different UID under that name is by definition
not the object this pass decided about, and the next pass re-Gets the name and takes the
foreign-object branch. On the Certificate, a conflict additionally **revokes the in-pass
proof**, so an unstamped Secret beside it is then left alone.

**D11 — Fail direction: no proof means keep the object and warn.** A
`LegacySentinelTLSNotOwned` Warning Event names what was missing, modelled on
`warnPodDisruptionBudgetNotOwned`. A stranded Secret and an occupied name are
recoverable by hand; deleting a Secret the operator never created is not. If a future
cert-manager release renames `cert-manager.io/certificate-name`, (a′) still covers the
in-pass case and the failure is a stranded Secret, not a deletion.

**D12 — Every optional cleanup Delete is taken behind a GET-first existence check, and
its verb must be in the shipping Helm ClusterRole.** Both halves are required. The
apiserver evaluates **authorization before existence**, so a Delete against a
non-existent object on a cluster whose role lacks the verb returns 403, not 404. Where
the missing `""/secrets: delete` verb landed, the effect was permanent — the chain below
is traced through the code, never reproduced against an apiserver (see Residual risks):
the Certificate delete succeeded (that verb was in the chart), the Secret delete 403'd on
every pass, and every reconcile of that CR ended in error — permanent
`ReconcileBlocked`, error phase, endless requeue, stale TLS material left behind. Not a
crashloop; a CR that never reaches a clean state again. See [ADR 0014](0014-rbac-lives-in-three-places.md).

**D13 — Granting a destructive verb and guarding its call site are one change.**
Granting `delete` for a narrow caller is not sufficient — the delete site must establish
provenance for the object it removes. The RBAC fix that made the legacy Secret delete
reachable on the Helm install path is what turned a dormant name-based delete into a
live hazard.

**D14 — Every child object the operator creates carries an ownerReference to the CR.**
Typed resources go through `controllerutil.SetControllerReference`; the two
`unstructured` ones set it explicitly (`ServiceMonitorOwnerRef`, `CertificateOwnerRef`).
This is what bounds the mid-pass-deletion residual of
[ADR 0001](0001-continue-reconciling-past-a-rejected-write.md): a child created after the
CR is gone is garbage-collected, with no orphan left behind.

The one child class this does not cover is the PVCs the StatefulSet controller creates
from `volumeClaimTemplates`
([`internal/builder/statefulset.go`](../../internal/builder/statefulset.go)): the
operator never creates them, the template sets no ownerReference, and no
`persistentVolumeClaimRetentionPolicy` is set anywhere in the tree — so they carry no
reference to the CR and survive its deletion. The garbage-collection argument this
decision lends to ADR 0001 stops at persistent volumes.

**D15 — Cleanup of the legacy material is additionally deferred** until
`sentinelRolloutComplete` reports that no Sentinel pod still belongs to the previous
revision, and the *active* Secret is never deleted.

## Consequences

* A user who leaves a foreign budget under the managed name gets **no operator-managed
  budget at all** for that StatefulSet, by design, with a Warning saying so. Documented
  at the `Enabled` field in `api/v1/valkey_types.go`, in the Helm `values.yaml` PDB
  comment block, and in the README.
* Stale objects the operator cannot prove it owns are left behind and reported as
  Events — recoverable by hand — rather than deleted. The operator is loud about it: a
  Warning per refusing pass.
* A Certificate whose UID changed mid-pass yields no proof for the Secret that pass, so
  the cleanup defers to a later reconcile.
* A divergence of the two TLS name derivations turns into a refusal (stranded Secret),
  not a wrong deletion.
* Unit tests must set a real UID on the CR fixture so ownerReference comparisons are
  meaningful rather than two empty strings. The fake client mints no UIDs on Create and
  never enforces a UID precondition — it *does* enforce a **ResourceVersion**
  precondition (`fakeClient.Delete`, controller-runtime v0.24.1,
  `pkg/client/fake/client.go:705`), so a unit test can pin exactly the behaviour D9
  rejects and still pass. The real apiserver rejection is therefore unobservable in a
  unit test — which is why an envtest integration test exists for exactly that half
  ([ADR 0017](0017-test-and-ci-policy.md)).
* **The guard bounds only what the reconcile path touches.** The cluster-wide
  `secrets: get,list,watch,delete` grant is untouched, so a compromised operator is
  unaffected by it — that is a separate item in
  [ADR 0013](0013-operator-is-cluster-wide-privileged.md).
* Any **new** delete-by-generated-name path must adopt the same discipline.

## Alternatives Considered

### Delete by name

The defect, twice. Rejected.

### Check `app.kubernetes.io/managed-by` instead of the ownerReference

Rejected: labels are forgeable and copyable, and a copied manifest would be mistaken for
an operator-owned object.

### `client.Preconditions{UID, ResourceVersion}`

Rejected: the disruption controller's continuous `.status` rewrites would make nearly
every PDB cleanup fail its precondition, forever.

### Return the error on a precondition `Conflict`

Rejected: it fails the whole reconcile pass for a condition that means the safety
mechanism did its job.

### Admit `controller.cert-manager.io/fao` as a second accept-path

Rejected — see D7. The cost is one fewer accept-path: the orphaned-Secret migration case
rests on the certificate-name annotation alone.

### Gate the Certificate delete on the cert-manager annotation as well

Unnecessary, weaker, and externally coupled — the operator sets the ownerReference
itself.

### Do not delete the legacy material at all; let an admin remove it

Verified concretely lossy: `--enable-certificate-owner-ref` is off on the reference
cluster, so cert-manager does not garbage-collect the Secret. That is a cluster
observation, not reproducible from this repository; it is recorded in the doc comment on
`deleteLegacySentinelSecret`. Result: stale TLS material and an occupied name,
indefinitely.

### Stamp our own label through cert-manager's `spec.secretTemplate`

Dead on arrival: it stamps only Secrets issued from that change onward, and legacy
Secrets are by definition older.

### Rely on the UID precondition alone, without a provenance check

Explicitly recorded as *not* closing the hazard — see D8. A name collision passes a UID
precondition perfectly.

### Leave the delete name-based and rely on the narrowness of the trigger conditions

Rejected. The RBAC fix was not in question; the missing guard on the delete was.

## Residual risks

* **`cleanupMetricsService`, `cleanupObserverDeployment` and `cleanupServiceMonitor`
  still delete by name.** Their names are operator-suffixed (`-metrics`, `-observer`)
  and are not names a hand-written object would plausibly carry, so this is a
  consistency follow-up rather than a severity-driven fix. A hand-written object under
  one of those names is still deleted on every pass. `cleanupObserverDeployment` deletes
  two objects this way: the `-observer` Deployment and, with NetworkPolicies enabled, the
  `-observer` NetworkPolicy.
* **`reconcileSidecarRoleBinding` deletes and recreates by name.** `RoleRef` is
  immutable, so a live RoleBinding under `<cr>-sidecar` whose `RoleRef` differs from the
  desired one is deleted and rebuilt — with neither `IsControlledBy` nor a UID
  precondition; the only `SetControllerReference` in that function is on the *desired*
  object. The suffix makes an accidental collision unlikely, but unlike the `-metrics`
  and `-observer` cases the trigger is guaranteed to fire for a foreign object: a
  hand-written RoleBinding under that name points at a different Role by construction.
  The step is wired unconditionally, so it runs on every pass of every CR.
* **`deleteLegacyServices` has a provenance scan but no UID precondition.** It deletes
  Services named `<cr>` — the same generated name the data StatefulSet and the data PDB
  carry — and `<cr>-read`, only when an ownerReference UID matches the CR. Two deviations
  from D1: the Delete carries no `client.Preconditions`, so the read-then-delete race D8
  closes elsewhere is open here, and the scan accepts *any* ownerReference rather than
  the controller ownerReference D2 designates as the provenance signal.
* **`apierrors.IsConflict` on a Delete is not exclusively a precondition failure.** An
  admission or quota conflict is swallowed the same way. Accepted: other Delete
  conflicts are effectively unreachable here and the pass is re-driven on the next
  reconcile.
* **The `kubernetes.io/tls` precondition does not close the hazard on its own** — an
  attacker can aim the generated name at a genuine TLS Secret. It filters accidents, not
  attacks.
* **The `cert-manager.io/certificate-name` annotation key was verified only against
  cert-manager v1.21.1.** Older releases are unverified; the failure direction there is
  the safe one (a refusal).
* **The 403 loop of D12 was read from the code, never reproduced against a real
  apiserver.** It is invisible on fresh installs, which is why the drift stayed hidden:
  a fresh unified-mode cluster never has the legacy Secret, so the GET returns NotFound
  and no Delete is attempted.
* Any new resource kind — especially `unstructured` ones that bypass `controllerutil` —
  must set the ownerReference explicitly, or D14's argument breaks silently.

## References

* [`internal/controller/pdb.go`](../../internal/controller/pdb.go) — `cleanupPodDisruptionBudget`, `reconcilePodDisruptionBudget`
* [`internal/controller/valkey_controller.go`](../../internal/controller/valkey_controller.go) — `deleteLegacySentinelCertificate`, `deleteLegacySentinelSecret`, `legacySentinelSecretIsOurs`, `sentinelRolloutComplete`, `warnLegacySentinelTLSNotOwned`
* [`internal/builder/certificate.go`](../../internal/builder/certificate.go) — `SentinelCertificateName`, `SentinelTLSSecretName`, `CertificateOwnerRef`
* [ADR 0004](0004-opt-in-poddisruptionbudgets.md) — the PDB feature this guard protects
* [ADR 0013](0013-operator-is-cluster-wide-privileged.md) — the cluster-wide grants this guard does *not* narrow
* [ADR 0014](0014-rbac-lives-in-three-places.md) — why a verb and its call-site guard ship together
* [ADR 0016](0016-authentication-and-tls-posture.md) — the `unifiedCertificate` migration that needs the cleanup
