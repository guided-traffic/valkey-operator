# ADR 0035: The README Advertises the Operator and Carries the Reference; the Explanations Live Under `docs/`

## Status

Accepted, amended 2026-09-27 (D6 re-decided: the README carries no decisions and does not link to
ADRs). Date: 2026-09-27.

**Amended 2026-09-29 (no decision changes):** `spec.networkPolicy` has its operations page,
[network-policy.md](../operations/network-policy.md), written with
[ADR 0039](0039-a-networkpolicy-admits-only-the-components-this-repository-deploys.md); the three
places below that recorded it as missing are struck in place.

**Re-decided 2026-09-27 by the owner: D6.** The README is the project's front page and the entry
point for administrators who consider or run the operator; decisions have no place in it. Every
ADR citation, every decision rationale ("no opt-out", "so upgrades change nothing") and the ADR
row of the documentation table were removed from it in the same uncommitted change. Contributors
reach the ADRs through `DEVELOPER.md` and `docs/developer/`.

The owner adopted this layout for the repository on 2026-09-27. It comes from a sibling project,
adapted in three places: the complete CRD reference and the complete Helm chart values table
stay in the README (D3), `DEVELOPER.md` is new here (D5), and ADRs may link into the code
([ADR 0034](0034-tickets-are-work-lists-that-get-archived.md) D8).

**Amended 2026-09-27 (correction, no decision changes):** the Residual risks sentence on where
the operator's command-line flags are mentioned said `--health-probe-bind-address` appears on no
page under `docs/`; ADR 0018 D9 names it. The sentence is struck through in place and restated.

**Amended again 2026-09-27 (corrections, no decision changes):** a second review pass of the
same change found three more wrong statements that the move had carried over from the README at
`HEAD`. Each is corrected with a dated note where it now stands:

- the observer's table of created objects left out the observer ServiceAccount
  (`docs/operations/observer.md`);
- the label table gave every managed resource `component: valkey | sentinel` and the version
  label, which the observer objects do not carry (the README's naming conventions);
- the pod-level labels named `instanceName` and `instanceRole` for every pod (the same
  section).

Context and Residual risks now count six corrections, struck and restated in place. The
Residual risks claim that every row of the naming tables was checked is struck and restated,
because the same pass found rows the check had let through. `docs/operations/` gained
`authentication.md`, so its page count below is struck and restated, and so are the line
counts of the README and `DEVELOPER.md` and the Residual risks mention of the observer's object
table, which the same pass replaced with prose. None of this changes what is built, so the
record's row in the [index](README.md) keeps its State.

**Implemented in the change that wrote this record, which is not committed yet.** Verified in
the working tree on 2026-09-27:

- The README went from 1780 lines (`HEAD` = `f5c6886`) to ~~about 580: 579 when the move
  finished, 580 after the review pass of the same change~~ about 600: 579 when the move
  finished, 580 after the first review pass of the same change, 602 after the second
  *(corrected 2026-09-27, measured with `wc -l` at 10:20 that day)*.
- `docs/operations/` exists, with an index and ~~thirteen~~ fourteen subject pages
  *(corrected 2026-09-27: `authentication.md` was added in the second review pass)*.
- `DEVELOPER.md` (~~466~~ 472 lines, *corrected 2026-09-27, measured with `wc -l` at 10:20*) and
  `docs/developer/` (an index and four pages) exist. Neither existed before.
- Every emoji heading of the README carries an explicit anchor.

**What is not done:**

- The explanations were moved verbatim and not re-verified against the code (Residual risks).
- The knowledge graph under `graphify-out/` is rebuilt by a separate run and does not know the
  new directories yet.
- The operator's command-line flags have neither a reference table nor a page of their own.
- ~~`spec.networkPolicy` has reference rows in the README but no page under `docs/operations/`
  that explains what the generated NetworkPolicies allow.~~ *(Done 2026-09-29.)*

## Context

On 2026-09-27 the README was 1780 lines, and it carried five kinds of statement in one scroll:

- the pitch and the feature list;
- the fast start;
- the complete CRD reference;
- the explanations of how each feature behaves;
- the contributor material — build, test and the architecture diagram.

Its first reader, somebody deciding whether to use the operator, did not reach the first
manifest before line 288. The "Quick Start" section opened with a 232-line upgrade guide.
The CRD reference ran from line 654 to line 1307, because the explanation of each field sat
inside its table section. `spec.podSecurity` alone took 148 lines, `spec.persistence` 111 and
`spec.podDisruptionBudget` 80. TLS took another 182 lines after the reference. The Helm chart
values began at line 1568, and the architecture diagram came just before the licence.

Being long was not the worst of it. The reference and the explanations lived in one place, so
nobody could see what the reference was missing. Three things are measured against the
README at `HEAD`:

- **The Helm values block was incomplete.** It left out `imagePullSecrets`, `nameOverride`,
  `fullnameOverride`, `serviceAccount.*`, `podLabels`, `podAnnotations`, `nodeSelector`,
  `tolerations`, `affinity` and `preUpgradeHook.*`, all of which `values.yaml` defines.
- **`spec.networkPolicy` had one row and no sub-table.**
- **~~Three~~ Six statements were wrong** *(corrected 2026-09-27: the last three were found
  by the second review pass of this change, Status)*:
  - The `spec.observer.resources` default claimed a 128Mi limit, which `GetObserverResources`
    never sets.
  - One unified-certificate example had no `spec.image`, which the CRD requires.
  - The HA example's table of created objects listed a `<name>` Service. The operator does not
    create that Service: it deletes it as a legacy name (`deleteLegacyServices`).
  - The table of objects the observer creates listed a Deployment and a NetworkPolicy and left
    out the observer ServiceAccount, which `BuildObserverServiceAccount` creates.
  - The label table gave every managed resource `app.kubernetes.io/component: valkey | sentinel`
    and `app.kubernetes.io/version`. The observer Deployment, pod, ServiceAccount and
    NetworkPolicy are labelled by `ObserverLabels`: `component: observer`, and no version label.
  - The pod-level labels listed `vko.gtrfc.com/instanceName` and `vko.gtrfc.com/instanceRole`
    for every pod. Nothing writes `instanceName`, and `instanceRole` is written only by the
    sidecar of a data pod, onto its own pod.

The layout had room for only three homes: the README, `SECURITY_ARCHITECTURE.md` and the ADRs.
`DEVELOPER.md` did not exist, although the three-file layout this repository followed names it
([ADR 0013](0013-operator-is-cluster-wide-privileged.md) listed it as an open residual). So
anything that was neither a decision nor a security statement went into the README by default.
Nobody chose the README as the home for that material. It was the only place left.

## Decision

**D1 — The README is the front page.** Its job is to make somebody want the operator and let
them start it. It carries, in this order:

- the pitch and a small data-flow diagram;
- the key features;
- the naming conventions;
- the documentation map;
- the fast start;
- the complete CRD reference;
- the complete Helm chart values;
- the development entry point;
- the licence.

A reader who has never run the operator can read it from top to bottom.

**D2 — Five homes, and a durable statement goes to exactly one.**

| Kind of statement | Home |
|---|---|
| A decision: what was decided, why, what was rejected | an ADR in `docs/adr/` |
| How the code works — a subsystem, an invariant, the contributor workflow | `docs/developer/` and `DEVELOPER.md` |
| What somebody running the operator needs | `docs/operations/` |
| The threat model and the gap each mechanism leaves | `docs/security/` ([ADR 0036](0036-the-security-architecture-is-one-page-per-perspective.md)) |
| Work still outstanding | a ticket in `docs/tickets/` ([ADR 0034](0034-tickets-are-work-lists-that-get-archived.md)) |

The README is none of the five. It is the front page, it holds the two reference tables of D3,
and it points at everything else.

**D3 — The complete CRD reference and the complete Helm chart values table live in the README,
and nowhere else.** Every `spec` and `status` field and every chart value, with its default,
has exactly one row. A page under `docs/operations/` explains a setting and never restates the
list, because two lists drift and nobody sees the drift. A reference table whose fields need
more than their row links the page that explains them. ~~`spec.networkPolicy` has no such page
yet (Residual risks).~~ *(It has since 2026-09-29.)*

**D4 — One subject per operations page.** A new subject becomes a new page. It is never added
as a section to the page of another subject, so a page's file name keeps predicting its
content. The operations index says which page to read when.

**D5 — Developer pages may, and should, name files and functions.** That is their purpose,
and it is why they go stale when the tree moves. Whoever moves the tree updates them in the same
change. `DEVELOPER.md` is the contributor's entry point: the repository layout, the
build-and-test matrix, continuous integration and the release, the extension checklists, the
toolchain versions and the conventions. It never repeats a page of `docs/developer/`.

**D6 — The README links; it does not explain.** When a README paragraph starts explaining a
mechanism, the explanation belongs on the page the paragraph links to. The README links to
documents ~~, to the configuration a user opens (`values.yaml`) and to ADRs~~ and to the
configuration a user opens (`values.yaml`) *(re-decided 2026-09-27, see Status)*. It never links
into the source tree. **It carries no decisions:** no ADR citation, no rationale for why the
operator behaves as it does, no record of what was rejected - it states what the operator does
and how to configure it.

**D7 — Every emoji heading in the README carries an explicit HTML anchor.** The anchor that
GitHub generates for an emoji heading is neither stable nor guessable. An anchor that other
documents already cite stays when its heading changes. `#crd-reference`, `#helm-chart-values`,
`#quick-start` and `#common-labels` still resolve.

## Consequences

- **The README can be read end to end again**, at about ~~580~~ 600 lines instead of 1780
  *(corrected 2026-09-27, Status)*. Of those,
  the two reference tables and the naming tables are most of the length, by design.
- **Two directories more to keep current.** A page under `docs/operations/` or
  `docs/developer/` goes stale the way any page does. Nothing automated checks it, and nothing
  checked the README before either.
- **Following a reference into its explanation is now a hop.** A reader who finds a field in
  the README follows a link to learn what it does. The price is paid by the reader already deep
  in one subject, which is the right reader to charge.
- **The documentation map had to be rewritten**, together with every link that pointed at a
  moved README section. A mechanical link check proves that each target exists. It does not
  prove the target is the right one.
- **`CLAUDE.md` is none of the homes.** It carries working rules for agents and points at the
  homes. Whatever it repeats from them can drift like any other duplicate.

## Alternatives Considered

**Leave everything in the README and accept the length.** Rejected. The page had outgrown its
first reader, and its length hid three wrong statements and an incomplete values block that a
reference-only table exposed at once.

**Move the CRD reference out as well, to a page under `docs/operations/`.** Rejected. The
reference is the part of the documentation a user of a `Valkey` resource keeps coming back to,
and the README is the page every visitor of the repository lands on. One table on the front
page is one home that nobody has to find. The sibling project keeps its configuration key
reference in its README by the same rule.

**Fold the operator reference into `docs/developer/`.** Rejected, although it was the cheaper
option. That directory is for people changing the code. Somebody running the operator should
not have to decide whether they are a contributor before finding a TLS port.

**One large `docs/operations.md`.** Rejected. It moves the problem one level down: the same page
under a different name, and a question still has no address of its own.

**Keep contributor material in the README and create no `DEVELOPER.md`.** Rejected. The build
matrix, the CI jobs and the extension checklists are for a different reader than the fast start.
The three-file layout this repository followed already named `DEVELOPER.md`, and its absence
was an open residual of ADR 0013.

## Residual risks

- **The explanations were moved verbatim, not re-verified.** They were true when they were
  written, and the move did not check them against the code again. Only these were checked for
  this change:
  - the new connective prose;
  - ~~every row of the naming tables;~~ the naming tables, row by row *(corrected 2026-09-27:
    the second review pass of this change found rows that check had let through, and
    corrected them in the README: the observer labels, the pod-level labels, the condition
    under which the ServiceMonitor is created, which left out `spec.metrics.enabled`, and the
    sidecar's `health` port, missing from the container row)*;
  - the CRD defaults, against the kubebuilder markers and the getters in `api/v1`;
  - the Helm values, against `values.yaml` and the templates;
  - the condition kinds, against `condition_registry.go`.

  The completeness of the Helm table was re-checked by key for this record: every leaf key of
  `values.yaml` has a row. The CRD reference was spot-checked by JSON field name only, not by
  path.
- **~~Three~~ Six corrections were made while moving or in the review passes of the same
  change, and each is visible where it was made**, with a dated note: the observer resources
  default, the missing `spec.image`, and the created-objects table, dropped in favour of the
  naming tables; then the observer ServiceAccount missing from the observer's table, dropped
  the same way (`docs/operations/observer.md`), and the observer labels and the pod-level labels
  of the README's naming conventions *(corrected 2026-09-27: the last three came from the
  second review pass, Status)*.
- **The two old documents overlapped, and the move first carried the overlap over.** The
  `Localhost` allow-list, the rootless migration, user namespaces and the `check-data-writable`
  pre-flight each appeared both under `docs/security/` and under `docs/operations/`. A review
  pass in the same change split them. What a mechanism defends against and leaves open stays on
  the security page. The procedure — what to set, what rolls, how to fix a volume — stays on
  the operations page. Each side links the other: `pod-security.md`, `upgrading.md` and
  `persistence.md` on one side, `seccomp-profiles.md`, `user-namespaces.md` and
  `rootless-migration.md` on the other (read 2026-09-27). **Not verified:** that no restated
  sentence is left. The pages were not compared line by line for this record, and nothing
  keeps the two sides apart but review.
- **Nothing enforces D3, D4 or D6.** An explanation can drift back into the README, and a
  restated list can appear on an operations page. Only review catches either.
- **Two rows have no operations page that explains them.** *(Since 2026-09-29 one:
  `spec.networkPolicy` is explained by [network-policy.md](../operations/network-policy.md), and
  the rest of this sentence about it is history.)* Under `docs/operations/`,
  `spec.networkPolicy` comes up only in passing: an example's security note and ~~the observer's
  object table~~ the observer page's paragraph on what it creates *(corrected 2026-09-27: the
  second review pass replaced that table with prose)*. No page there says what the generated NetworkPolicies allow. The ingress rules
  are an ADR 0013 D7 decision, and their gap is H-10 on the security side.
  `status.operatorVersion` is otherwise named only on the developer page about the reconcile
  loop, for when it is written. The operator's command-line flags
  (`--max-concurrent-reconciles`, `--allowed-seccomp-localhost-profiles`,
  `--metrics-bind-address`, `--health-probe-bind-address`, `--operator-image`, `--leader-elect`,
  and since 2026-09-29 `--operator-pod-selector`, read in `cmd/main.go`) have no reference table. ~~All but `--health-probe-bind-address` are
  mentioned on a page under `docs/` or in `DEVELOPER.md` (searched 2026-09-27), and none has a
  row of its own in a reference table.~~ *(corrected 2026-09-27: ADR 0018 D9 is a page under
  `docs/` and names `--health-probe-bind-address`.)* All but `--health-probe-bind-address` are
  mentioned on a page under `docs/operations/`, `docs/security/` or `docs/developer/`, or in
  `DEVELOPER.md`; `--health-probe-bind-address` appears only in the text of ADR 0018 D9
  (searched 2026-09-27). None has a row of its own in a reference table.
- **Not verified:** how GitHub renders the Mermaid entity codes in the README diagram, and the
  chart-repository install path of the fast start. Both were read from the workflows and not
  executed.
- **One piece of prose outside the documentation still sends a reader to the README for an
  explanation.** The chart's PrometheusRule description (`prometheusrule.yaml`) says "see the
  spec.persistence section of the README". That section now only holds the field table and a
  link to the persistence operations page, which has the explanation, so the pointer costs the
  reader one extra hop but is not dead. The same pointer in the `StatefulSetRecreateRequired` Event
  message of `volumeclaim_conflict.go` was retargeted in this change. Checked on 2026-09-27.

## References

- [README.md](../../README.md) — the front page this decision defines, with the
  [CRD reference](../../README.md#crd-reference) and the
  [Helm chart values](../../README.md#helm-chart-values)
- [docs/operations/README.md](../operations/README.md) — the operations index
- [DEVELOPER.md](../../DEVELOPER.md) and [docs/developer/README.md](../developer/README.md) — the
  contributor entry point and the per-subsystem pages
- [ADR 0034](0034-tickets-are-work-lists-that-get-archived.md) — the ticket lifecycle, and why an
  ADR here may link into the code
- [ADR 0036](0036-the-security-architecture-is-one-page-per-perspective.md) — the security home,
  one page per perspective
- [ADR 0013](0013-operator-is-cluster-wide-privileged.md) — whose `DEVELOPER.md` residual this
  change closes
- [`api/v1/valkey_types.go`](../../api/v1/valkey_types.go) — `GetObserverResources` and the
  `Image` marker behind two of the ~~three~~ six corrections
- [`internal/controller/valkey_controller.go`](../../internal/controller/valkey_controller.go) —
  `deleteLegacyServices`, behind the third
- [`internal/builder/observer.go`](../../internal/builder/observer.go) —
  `BuildObserverServiceAccount` and `ObserverLabels`, behind the fourth and the fifth
- [`internal/sidecar/labeler.go`](../../internal/sidecar/labeler.go) — the one writer of
  `instanceRole`, behind the sixth
- [`deploy/helm/valkey-operator/values.yaml`](../../deploy/helm/valkey-operator/values.yaml) — what
  the Helm chart values table is checked against
